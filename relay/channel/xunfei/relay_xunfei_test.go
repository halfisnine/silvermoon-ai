package xunfei

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestXunfeiSingleRequestFundingAndProtocol(t *testing.T) {
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	oldDB, oldLog := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLog })
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() { common.SetDatabaseTypes(oldMain, oldLogType) })
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	oldLogs := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = oldLogs })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.MarketingAccount{}, &model.MarketingInvitation{}, &model.MarketingActivity{}, &model.MarketingActivityFact{}, &model.MarketingFactSequence{}, &model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingLedgerEntry{}, &model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.MarketingDispatch{}, &model.MarketingUsageProjection{}, &model.QuotaData{}, &model.Log{}))
	require.NoError(t, db.Create(&model.MarketingFactSequence{ID: 1}).Error)
	terminal := `{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"world"}]},"usage":{"text":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}}}`
	partial := `{"header":{"code":0},"payload":{"choices":{"status":1,"text":[{"content":"hello "}]},"usage":{"text":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}}}`
	for _, stream := range []bool{false, true} {
		for _, scenario := range []struct {
			name                                                        string
			frames                                                      []string
			expectedQuota                                               int
			complete, unknownUsage, fixed, duplicate, cancel, handshake bool
		}{
			{name: "complete", frames: []string{`{"header":{"code":0},"payload":{"choices":{"status":1,"text":[{"content":"hello "}]}}}`, terminal}, complete: true},
			{name: "duplicate_send", frames: []string{terminal}, complete: true, duplicate: true},
			{name: "accepted_then_disconnect"},
			{name: "partial_usage_then_disconnect", frames: []string{partial}},
			{name: "malformed_response", frames: []string{`{"private_error":"private-provider-marker"`}},
			{name: "provider_failure", frames: []string{`{"header":{"code":10013,"message":"private-provider-marker"},"payload":{"choices":{"status":2}}}`}},
			{name: "missing_success_code", frames: []string{`{"header":{},"payload":{"choices":{"status":2}}}`}},
			{name: "negative_usage", frames: []string{`{"header":{"code":0},"payload":{"choices":{"status":2},"usage":{"text":{"prompt_tokens":-1,"completion_tokens":3,"total_tokens":2}}}}`}},
			{name: "cumulative_usage_overflow", frames: []string{fmt.Sprintf(`{"header":{"code":0},"payload":{"choices":{"status":1},"usage":{"text":{"prompt_tokens":%d,"completion_tokens":0,"total_tokens":%d}}}}`, common.MaxQuota, common.MaxQuota), terminal}},
			{name: "missing_completion_usage", frames: []string{`{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"answer"}]},"usage":{"text":{"prompt_tokens":4,"total_tokens":4}}}}`}, complete: true, unknownUsage: true},
			{name: "text_with_zero_completion", frames: []string{`{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"answer"}]},"usage":{"text":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}}}`}, complete: true, unknownUsage: true},
			{name: "earlier_text_with_zero_completion", frames: []string{`{"header":{"code":0},"payload":{"choices":{"status":1,"text":[{"content":"answer"}]}}}`, `{"header":{"code":0},"payload":{"choices":{"status":2},"usage":{"text":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}}}`}, complete: true, unknownUsage: true},
			{name: "no_text_zero_completion", frames: []string{`{"header":{"code":0},"payload":{"choices":{"status":2},"usage":{"text":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}}}`}, complete: true, expectedQuota: 4},
			{name: "fixed_price_complete_without_usage", frames: []string{`{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"answer"}]}}}`}, complete: true, fixed: true},
			{name: "fixed_price_incomplete", frames: []string{partial}, fixed: true},
			{name: "context_cancel_after_send", cancel: true},
			{name: "handshake_failure", handshake: true},
		} {
			t.Run(fmt.Sprintf("%s/stream=%v", scenario.name, stream), func(t *testing.T) {
				name := fmt.Sprintf("%s-%v", scenario.name, stream)
				user := model.User{Username: name, AffCode: name, Status: common.UserStatusEnabled}
				require.NoError(t, db.Create(&user).Error)
				require.NoError(t, db.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error)
				token := model.Token{UserId: user.Id, Key: name, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
				require.NoError(t, db.Create(&token).Error)
				now := common.GetTimestamp()
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: name, PaidCents: 100, PostedAt: now, Money: model.MarketingMoneySnapshot{Amount: "100", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
					if err != nil {
						return err
					}
					_, err = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: "recharge", Reference: name, GrantedAt: now, ExpiresAt: now + 3600, Money: model.MarketingMoneySnapshot{Amount: "5", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
					return err
				}))
				info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, RequestId: name, StartTime: time.Unix(now, 0), IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{}, OriginModelName: "xunfei-fixture", PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
				if scenario.fixed {
					expression := fmt.Sprintf("tier(\"request\", fixed(%g))", 7/common.QuotaPerUnit)
					info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), ExprVersion: 1, QuotaPerUnit: common.QuotaPerUnit, GroupRatio: 1, EstimatedBillingUnit: billingexpr.BillingUnitRequest}
				}
				session, err := service.NewMarketingBillingSession(info, 10)
				require.NoError(t, err)
				info.Billing = session
				var originalFunding model.MarketingFunding
				require.NoError(t, db.Where("user_id = ?", user.Id).First(&originalFunding).Error)
				requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var accepted atomic.Int32
				var barrierVisible atomic.Bool
				serverExited := make(chan struct{}, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer func() { serverExited <- struct{}{} }()
					if scenario.handshake {
						http.Error(w, "rejected", http.StatusUnauthorized)
						return
					}
					conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					if _, _, err = conn.ReadMessage(); err != nil {
						return
					}
					accepted.Add(1)
					var dispatches int64
					if err = db.Model(&model.MarketingDispatch{}).Where("funding_id = ?", originalFunding.ID).Count(&dispatches).Error; err == nil && dispatches == 1 {
						barrierVisible.Store(true)
					}
					if scenario.cancel {
						cancel()
						_, _, _ = conn.ReadMessage()
						return
					}
					for _, frame := range scenario.frames {
						if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
							return
						}
					}
				}))
				defer server.Close()
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(requestContext)
				ctx.Set(common.RequestIdKey, name)
				endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "?authorization=private-auth-marker"
				usage, apiErr := xunfeiHandler(ctx, info, dto.GeneralOpenAIRequest{Model: "SparkDesk"}, "fixture-app", "fixture-domain", endpoint)
				select {
				case <-serverExited:
				case <-time.After(time.Second):
					t.Fatal("upstream handler did not exit after client completion/cancellation")
				}
				if scenario.handshake {
					require.NotNil(t, apiErr)
					assert.Zero(t, accepted.Load())
					assert.NotContains(t, apiErr.Error(), "private-auth-marker")
				} else {
					require.EqualValues(t, 1, accepted.Load())
					assert.True(t, barrierVisible.Load(), "dispatch must be committed before the business frame reaches the provider")
				}
				if scenario.complete {
					require.Nil(t, apiErr)
					require.NotNil(t, usage)
					assert.Equal(t, relaycommon.ResponseOutcomeCompleted, info.StreamStatus.OutcomeSnapshot().Response)
					assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.OutcomeSnapshot().EndReason)
					if stream {
						assert.Contains(t, recorder.Body.String(), "[DONE]")
					}
					if scenario.duplicate {
						retryCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
						retryCtx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(requestContext)
						_, retryErr := xunfeiHandler(retryCtx, info, dto.GeneralOpenAIRequest{Model: "SparkDesk"}, "fixture-app", "fixture-domain", endpoint)
						require.NotNil(t, retryErr)
						assert.True(t, types.IsSkipRetryError(retryErr))
						select {
						case <-serverExited:
						case <-time.After(time.Second):
							t.Fatal("blocked duplicate connection was not closed")
						}
						assert.EqualValues(t, 1, accepted.Load(), "same funding must never send its business frame twice")
					}
					service.PostTextConsumeQuota(ctx, info, usage, nil)
				} else if !scenario.handshake {
					require.NotNil(t, apiErr)
					assert.True(t, types.IsSkipRetryError(apiErr))
					assert.NotContains(t, recorder.Body.String(), "[DONE]")
				}
				// Failure cleanup refunds unused sources, without erasing partial usage.
				session.Refund(ctx)
				var funding model.MarketingFunding
				require.NoError(t, db.Where("user_id = ?", user.Id).First(&funding).Error)
				var gift model.MarketingGift
				require.NoError(t, db.Where("user_id = ?", user.Id).First(&gift).Error)
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				var hold model.MarketingTokenHold
				require.NoError(t, db.First(&hold, "token_id = ?", token.Id).Error)
				var dispatches int64
				require.NoError(t, db.Model(&model.MarketingDispatch{}).Where("funding_id = ?", originalFunding.ID).Count(&dispatches).Error)
				if scenario.handshake {
					assert.Zero(t, dispatches)
				} else {
					assert.EqualValues(t, 1, dispatches)
				}
				if scenario.name == "cumulative_usage_overflow" {
					assert.Equal(t, "review", funding.State)
					assert.True(t, funding.CostKnown)
					assert.EqualValues(t, common.MaxQuota, funding.ActualQuota)
					assert.EqualValues(t, 100, funding.SettledQuota)
					assert.Zero(t, token.RemainQuota)
				} else {
					expectedQuota := scenario.expectedQuota
					if scenario.unknownUsage {
						expectedQuota = 4
					} else if expectedQuota == 0 && (scenario.complete || scenario.name == "partial_usage_then_disconnect" || scenario.name == "fixed_price_incomplete") {
						expectedQuota = 7
					}
					assert.Equal(t, "settled", funding.State)
					assert.True(t, funding.CostKnown)
					giftSpent := min(expectedQuota, 5)
					assert.EqualValues(t, expectedQuota, funding.ActualQuota)
					assert.Zero(t, funding.ReservedQuota)
					assert.Equal(t, 100-(expectedQuota-giftSpent), user.Quota)
					assert.EqualValues(t, 5-giftSpent, gift.RemainingQuota)
					assert.EqualValues(t, giftSpent, gift.ConsumedQuota)
					assert.Equal(t, 100-expectedQuota, token.RemainQuota)
					assert.Equal(t, expectedQuota, token.UsedQuota)
					assert.Zero(t, hold.ReservedQuota)
				}

				for _, secret := range []string{"private-auth-marker", "private-provider-marker"} {
					assert.NotContains(t, funding.PricingEvidence, secret)
					assert.NotContains(t, funding.UsageEvidence, secret)
				}
			})
		}
	}
}
