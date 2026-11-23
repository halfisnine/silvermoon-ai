package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// The configured DSNs must point at isolated test databases. Each dialect runs
// the real reservation, settlement and log paths with the same billing cases.
func TestFixedPriceBillingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name   common.DatabaseType
		env    string
		logEnv string
	}{
		{common.DatabaseTypeSQLite, "", ""},
		{common.DatabaseTypeMySQL, "TEST_FIXED_MYSQL_DSN", "TEST_FIXED_MYSQL_LOG_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_FIXED_POSTGRES_DSN", "TEST_FIXED_POSTGRES_LOG_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect.env != "" {
				dsn := os.Getenv(dialect.env)
				if dsn == "" {
					t.Skip(dialect.env + " is not configured")
				}
				if dialect.name == common.DatabaseTypeMySQL {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			logDB := db
			if logDSN := os.Getenv(dialect.logEnv); logDSN != "" || dialect.name == common.DatabaseTypeSQLite {
				var logDriver gorm.Dialector = sqlite.Open(":memory:")
				if dialect.name == common.DatabaseTypeMySQL {
					logDriver = mysql.Open(logDSN)
				} else if dialect.name == common.DatabaseTypePostgreSQL {
					logDriver = postgres.New(postgres.Config{DSN: logDSN, PreferSimpleProtocol: true})
				}
				logDB, err = gorm.Open(logDriver, &gorm.Config{})
				require.NoError(t, err)
				logSQL, err := logDB.DB()
				require.NoError(t, err)
				logSQL.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, logSQL.Close()) })
			}
			oldDB, oldLogDB := model.DB, model.LOG_DB
			oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			model.DB, model.LOG_DB = db, logDB
			common.SetDatabaseTypes(dialect.name, dialect.name)
			t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLogDB; common.SetDatabaseTypes(oldMainType, oldLogType) })
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.MarketingAccount{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			versionQuery := "select version()"
			if dialect.name == common.DatabaseTypeSQLite {
				versionQuery = "select sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database: %s", version)
			runFixedPriceAccountingCases(t, db, logDB)
		})
	}
}

// The fixture establishes a real paid posting rather than relabeling an old
// unclassified balance. Its conversion is explicit and deliberately independent
// of the model pricing unit, just as a historical posting snapshot is.
func seedPricingTestMarketingWallet(t *testing.T, db *gorm.DB, userID, quota int) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&model.MarketingAccount{}, &model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingLedgerEntry{}, &model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.MarketingDispatch{}, &model.MarketingUsageProjection{}, &model.QuotaData{}, &model.MarketingActivity{}, &model.MarketingInvitation{}, &model.MarketingFactSequence{}, &model.MarketingActivityFact{}, &model.MarketingActivityDeliveryCursor{}))
	require.NoError(t, db.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userID).Update("quota", 0).Error)
	require.NoError(t, db.Create(&model.MarketingAccount{UserID: userID, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "pricing fixture registration"}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: userID, Kind: "online", Reference: fmt.Sprintf("pricing-fixture-%d", userID), PaidCents: int64(quota), PostedAt: common.GetTimestamp(), Money: model.MarketingMoneySnapshot{Amount: decimal.NewFromInt(int64(quota)).Shift(-2).String(), QuotaPerUnit: "100", CurrencyPerUnit: "1", Currency: "CNY"}})
		return err
	}))
	t.Cleanup(func() {
		funds := db.Model(&model.MarketingFunding{}).Select("id").Where("user_id = ?", userID)
		for _, record := range []any{&model.MarketingAllocation{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.MarketingDispatch{}, &model.MarketingUsageProjection{}} {
			require.NoError(t, db.Where("funding_id IN (?)", funds).Delete(record).Error)
		}
		tokens := db.Model(&model.MarketingFunding{}).Select("token_id").Where("user_id = ?", userID)
		require.NoError(t, db.Where("token_id IN (?)", tokens).Delete(&model.MarketingTokenHold{}).Error)
		for _, record := range []any{&model.MarketingLedgerEntry{}, &model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingActivityFact{}, &model.MarketingFunding{}, &model.MarketingAccount{}, &model.QuotaData{}} {
			require.NoError(t, db.Where("user_id = ?", userID).Delete(record).Error)
		}
	})
}

func runFixedPriceAccountingCases(t *testing.T, db, logDB *gorm.DB) {
	t.Helper()
	const mixed = `len <= 32000 ? tier("short", fixed(0.01)) : tier("long", p * 2)`
	const flat = `tier("request", fixed(0.01))`
	const startingQuota = 2_000_000
	const imageExpression = `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`
	imageUsage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 300, ImageTokens: 600, CachedTokensDetails: &dto.CachedTokenDetails{ImageTokens: common.GetPointer(200)}}}
	operation_setting.SetToolPriceForTest("fixed_billing_tool", 4)
	t.Cleanup(func() { operation_setting.DeleteToolPriceForTest("fixed_billing_tool") })
	for index, tc := range []struct {
		name, expression                          string
		estimate                                  int
		usage                                     *dto.Usage
		audio, stream, refund, insufficient, tool bool
		review, realtime, reserveInsufficient     bool
		wallet, outboundImages, retryReservation  int
		groupRatio                                float64
		want                                      int
		unit                                      billingexpr.BillingUnit
		requestedImages, actualImages             int
	}{
		{name: "missing usage charges once", expression: flat, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "zero usage charges once", expression: flat, usage: &dto.Usage{}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "stream charges once", expression: flat, stream: true, usage: &dto.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "audio zero usage charges once", expression: flat, audio: true, usage: &dto.Usage{}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "audio missing usage charges once", expression: flat, audio: true, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "token reservation refunds to fixed price", expression: mixed, estimate: 50000, usage: &dto.Usage{PromptTokens: 100, TotalTokens: 100}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "fixed reservation settles token fallback", expression: mixed, estimate: 100, usage: &dto.Usage{PromptTokens: 50000, TotalTokens: 50000}, want: 50000, unit: billingexpr.BillingUnitToken},
		{name: "missing token usage retains reservation for review", expression: mixed, estimate: 50000, review: true, unit: billingexpr.BillingUnitToken},
		{name: "evaluation error retains fixed reservation metadata", expression: `p == 50 ? tier("error", param("missing") * p + img_cr * 2) : tier("request", fixed(0.01))`, estimate: 100, usage: &dto.Usage{PromptTokens: 50, TotalTokens: 50}, review: true, unit: billingexpr.BillingUnitRequest},
		{name: "explicit zero remains free", expression: `tier("free", fixed(0))`, usage: &dto.Usage{PromptTokens: 100, TotalTokens: 100}, unit: billingexpr.BillingUnitRequest},
		{name: "multipliers and separate tool surcharge", expression: flat + ` * (param("fast") == true ? 2 : 1)`, groupRatio: 1.5, tool: true, want: 18000, unit: billingexpr.BillingUnitRequest},
		{name: "failed request refunds exactly once", expression: flat, refund: true},
		{name: "insufficient wallet never reserves tokens", expression: flat, insufficient: true},
		{name: "image cache stream settles usage and refunds unused reservation", expression: imageExpression, estimate: 10000, usage: imageUsage, stream: true, want: 4113, unit: billingexpr.BillingUnitToken},
		{name: "audio settlement records image cache billing inputs", expression: imageExpression, estimate: 10000, usage: imageUsage, audio: true, want: 4113, unit: billingexpr.BillingUnitToken},
		{name: "realtime records actual expression inputs", expression: imageExpression, estimate: 10000, usage: imageUsage, realtime: true, want: 4000, unit: billingexpr.BillingUnitToken},
		{name: "image cache insufficient wallet never reserves tokens", expression: imageExpression, estimate: 10000, insufficient: true},
		{name: "image quantity refunds missing images", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 3, actualImages: 2, want: 40000, unit: billingexpr.BillingUnitRequest},
		{name: "image quantity keeps request when actual missing", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 3, want: 60000, unit: billingexpr.BillingUnitRequest},
		{name: "image quantity zero price stays free", expression: `tier("image", fixed(0)) * image_count`, requestedImages: 3, actualImages: 2, unit: billingexpr.BillingUnitRequest},
		{name: "image quantity failure refunds reservation", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 3, refund: true},
		{name: "image quantity exceeds one-image wallet before submission", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 4, wallet: 20000, insufficient: true},
		{name: "image override reserves extra quantity", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 1, outboundImages: 4, want: 80000, unit: billingexpr.BillingUnitRequest},
		{name: "image override cannot exceed remaining wallet", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 1, outboundImages: 4, wallet: 40000, reserveInsufficient: true, refund: true},
		{name: "task retry holds a pricier channel and refunds all of it", expression: flat, retryReservation: 40000, refund: true},
		{name: "task retry cannot exceed remaining wallet", expression: flat, retryReservation: 40000, wallet: 20000, reserveInsufficient: true, refund: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota := startingQuota
			if tc.insufficient {
				quota = 1
			}
			if tc.wallet > 0 {
				quota = tc.wallet
			}
			user := model.User{Username: fmt.Sprintf("fixed_billing_%d", index), Quota: quota, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			seedPricingTestMarketingWallet(t, db, user.Id, quota)
			token := model.Token{UserId: user.Id, Key: fmt.Sprintf("fixed-billing-test-%d", index), Name: "fixed-billing", RemainQuota: startingQuota, Status: common.TokenStatusEnabled, ExpiredTime: -1}
			require.NoError(t, db.Create(&token).Error)
			channel := model.Channel{Name: "fixed-billing", Key: "unused", Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&channel).Error)
			t.Cleanup(func() {
				require.NoError(t, logDB.Where("user_id = ?", user.Id).Delete(&model.Log{}).Error)
				require.NoError(t, db.Unscoped().Delete(&token).Error)
				require.NoError(t, db.Unscoped().Delete(&user).Error)
				require.NoError(t, db.Unscoped().Delete(&channel).Error)
			})
			group := tc.groupRatio
			if group == 0 {
				group = 1
			}
			request := &billingexpr.RequestInput{Body: []byte(`{"fast":true}`)}
			if tc.requestedImages > 0 {
				request.ImageCount = &tc.requestedImages
			}
			cost, trace, err := billingexpr.RunExprWithRequest(tc.expression, billingexpr.TokenParams{P: float64(tc.estimate), Len: float64(tc.estimate)}, *request)
			require.NoError(t, err)
			reservation, err := billingexpr.QuotaRoundStrict(cost / 1_000_000 * common.QuotaPerUnit * group)
			require.NoError(t, err)
			snapshot := &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: tc.expression, ExprHash: billingexpr.ExprHashString(tc.expression), QuotaPerUnit: common.QuotaPerUnit, GroupRatio: group, EstimatedTier: trace.MatchedTier, EstimatedBillingUnit: trace.BillingUnit, EstimatedFixedPrice: trace.FixedPrice, EstimatedQuotaAfterGroup: reservation}
			snapshot.EstimatedImageCount = trace.ImageCount
			info := &relaycommon.RelayInfo{RequestId: fmt.Sprintf("fixed-price-request-%d", user.Id), UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}, OriginModelName: "fixed-test", UsingGroup: "default", UserGroup: "default", UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}, ForcePreConsume: true, StartTime: time.Now(), IsStream: tc.stream, RelayFormat: types.RelayFormatOpenAI, PriceData: hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: group}}, TieredBillingSnapshot: snapshot, BillingRequestInput: request}
			info.SetEstimatePromptTokens(tc.estimate)
			if tc.stream {
				info.StreamStatus = &relaycommon.StreamStatus{}
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.expression == imageExpression {
				ctx.Request.URL.Path = "/v1/images/generations"
				info.RelayMode = relayconstant.RelayModeImagesGenerations
			}
			apiErr := PreConsumeBilling(ctx, reservation, info)
			if tc.insufficient {
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
			} else {
				require.Nil(t, apiErr)
				held, err := model.GetUserQuota(user.Id, true)
				require.NoError(t, err)
				assert.Equal(t, quota, held, "reservation is not settled consumption")
				var account model.MarketingAccount
				require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
				assert.EqualValues(t, max(reservation, 1), account.PaidReserved, "admission hold is released when the actual fixed price is zero")
				if tc.outboundImages > 0 {
					reserveErr := PrepareImageBillingForRequest(ctx, info, tc.outboundImages)
					if tc.reserveInsufficient {
						require.NotNil(t, reserveErr)
						assert.Equal(t, types.ErrorCodeInsufficientUserQuota, reserveErr.GetErrorCode())
						assert.Equal(t, reservation, info.Billing.GetPreConsumedQuota())
					} else {
						require.Nil(t, reserveErr)
						assert.Equal(t, tc.want, info.Billing.GetPreConsumedQuota())
					}
				}
				if tc.retryReservation > 0 {
					retryErr := PreConsumeBilling(ctx, tc.retryReservation, info)
					if tc.reserveInsufficient {
						require.NotNil(t, retryErr)
						assert.Equal(t, types.ErrorCodeInsufficientUserQuota, retryErr.GetErrorCode())
						assert.Equal(t, reservation, info.Billing.GetPreConsumedQuota())
					} else {
						require.Nil(t, retryErr)
						assert.Equal(t, tc.retryReservation, info.Billing.GetPreConsumedQuota())
					}
				}
				if tc.refund {
					info.Billing.Refund(ctx)
					info.Billing.Refund(ctx)
					session := info.Billing.(*MarketingBillingSession)
					assert.Equal(t, "settled", session.funding.State)
					assert.Zero(t, session.funding.ReservedQuota)
				} else {
					info.UpdateImageCount(int64(tc.actualImages))
					if tc.tool {
						info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{"fixed_billing_tool": {CallCount: 1}}}
					}
					if tc.realtime {
						PostWssConsumeQuota(ctx, info, info.OriginModelName, &dto.RealtimeUsage{
							InputTokens: tc.usage.PromptTokens, OutputTokens: tc.usage.CompletionTokens, TotalTokens: tc.usage.TotalTokens,
						}, nil)
					} else if tc.audio {
						PostAudioConsumeQuota(ctx, info, tc.usage, nil)
					} else {
						PostTextConsumeQuota(ctx, info, tc.usage, nil)
					}
					session := info.Billing.(*MarketingBillingSession)
					kind, evidence, ok := strings.Cut(session.funding.UsageEvidence, ":")
					require.True(t, ok)
					var actual *int64
					if session.funding.CostKnown {
						actual = common.GetPointer(session.funding.ActualQuota)
					}
					replayErr := session.SettleEvidence(actual, true, kind, evidence, 1)
					if tc.review {
						require.ErrorIs(t, replayErr, model.ErrMarketingReview)
						assert.False(t, session.funding.CostKnown)
						assert.EqualValues(t, reservation, session.funding.ReservedQuota)
					} else {
						require.NoError(t, replayErr, "the same settlement fact must not charge twice")
						assert.Equal(t, "settled", session.funding.State)
					}
					var log model.Log
					require.NoError(t, logDB.Where("user_id = ?", user.Id).Take(&log).Error)
					assert.Equal(t, tc.want, log.Quota)
					assert.Equal(t, tc.stream, log.IsStream)
					assert.NotContains(t, log.Content, "nothing was charged")
					var other map[string]any
					require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
					assert.Equal(t, string(tc.unit), other["billing_unit"])
					if tc.requestedImages > 0 {
						count := tc.actualImages
						if count == 0 {
							count = tc.requestedImages
							if tc.outboundImages > 0 {
								count = tc.outboundImages
							}
						}
						assert.Equal(t, float64(count), other["image_count"])
						assert.Equal(t, tc.requestedImages, *request.ImageCount, "actual count must not mutate the frozen request")
					}
					if tc.expression == imageExpression {
						billable, ok := other["billing_tokens"].(map[string]any)
						require.True(t, ok)
						if tc.realtime {
							assert.Equal(t, float64(0), other["image_cache_tokens"])
							assert.Equal(t, float64(1000), billable["p"])
							assert.Equal(t, float64(0), billable["cr"])
							assert.Equal(t, float64(0), billable["img"])
						} else {
							if !tc.audio {
								assert.Equal(t, float64(300), other["cache_tokens"])
							}
							assert.Equal(t, float64(200), other["image_cache_tokens"])
							assert.Equal(t, float64(300), billable["p"])
							assert.Equal(t, float64(100), billable["cr"])
							assert.Equal(t, float64(400), billable["img"])
						}
					} else {
						assert.NotContains(t, other, "billing_tokens")
						assert.NotContains(t, other, "image_cache_tokens")
					}
					if tc.unit == billingexpr.BillingUnitRequest {
						assert.Contains(t, other, "fixed_price")
					} else {
						assert.NotContains(t, other, "fixed_price")
					}
				}
			}
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, quota-tc.want, user.Quota)
			assert.Equal(t, startingQuota-tc.want, token.RemainQuota)
			assert.Equal(t, tc.want, user.UsedQuota)
			assert.Equal(t, tc.want, token.UsedQuota)
			if !tc.refund && !tc.insufficient {
				assert.Equal(t, 1, user.RequestCount)
			}
		})
	}
}

func TestCalculateTextQuotaSummaryUnifiedForClaudeSemantic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
	}

	priceData := hosttypes.PriceData{
		ModelRatio:           1,
		CompletionRatio:      2,
		CacheRatio:           0.1,
		CacheCreationRatio:   1.25,
		CacheCreation5mRatio: 1.25,
		CacheCreation1hRatio: 2,
		GroupRatioInfo: hosttypes.GroupRatioInfo{
			GroupRatio: 1,
		},
	}

	chatRelayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}
	messageRelayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}

	chatSummary := calculateTextQuotaSummary(ctx, chatRelayInfo, usage)
	messageSummary := calculateTextQuotaSummary(ctx, messageRelayInfo, usage)

	require.Equal(t, messageSummary.Quota, chatSummary.Quota)
	require.Equal(t, messageSummary.CacheCreationTokens5m, chatSummary.CacheCreationTokens5m)
	require.Equal(t, messageSummary.CacheCreationTokens1h, chatSummary.CacheCreationTokens1h)
	require.True(t, chatSummary.IsClaudeUsageSemantic)
	require.Equal(t, 1488, chatSummary.Quota)
}

func TestCalculateTextQuotaSummaryUsesSplitClaudeCacheCreationRatios(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:           1,
			CompletionRatio:      1,
			CacheRatio:           0,
			CacheCreationRatio:   1,
			CacheCreation5mRatio: 2,
			CacheCreation1hRatio: 3,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 10,
		},
		ClaudeCacheCreation5mTokens: 2,
		ClaudeCacheCreation1hTokens: 3,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// 100 + remaining(5)*1 + 2*2 + 3*3 = 118
	require.Equal(t, 118, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesAnthropicUsageSemanticFromUpstreamUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:           1,
			CompletionRatio:      2,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		UsageSemantic:    "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, "anthropic", summary.UsageSemantic)
	require.Equal(t, 1488, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesClaudeBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:           1,
			CompletionRatio:      2,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo:       hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{
			InputTokens:              70,
			CacheReadInputTokens:     30,
			CacheCreationInputTokens: 20,
			OutputTokens:             7,
			CacheCreation: &dto.ClaudeCacheCreationUsage{
				Ephemeral5mInputTokens: 12,
				Ephemeral1hInputTokens: 8,
			},
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticAnthropic, summary.UsageSemantic)
	require.Equal(t, 70, summary.PromptTokens)
	require.Equal(t, 7, summary.CompletionTokens)
	require.Equal(t, 30, summary.CacheTokens)
	require.Equal(t, 20, summary.CacheCreationTokens)
	require.Equal(t, 12, summary.CacheCreationTokens5m)
	require.Equal(t, 8, summary.CacheCreationTokens1h)
	require.Equal(t, 118, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesGeminiBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gemini-2.5-flash",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			CacheRatio:      0.1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{
			PromptTokenCount:        100,
			ToolUsePromptTokenCount: 5,
			CandidatesTokenCount:    20,
			ThoughtsTokenCount:      3,
			TotalTokenCount:         128,
			CachedContentTokenCount: 7,
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.False(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticGemini, summary.UsageSemantic)
	require.Equal(t, 105, summary.PromptTokens)
	require.Equal(t, 23, summary.CompletionTokens)
	require.Equal(t, 7, summary.CacheTokens)
	require.Equal(t, 128, summary.TotalTokens)
	require.Equal(t, 145, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesOpenAIBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "gpt-4o",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{
			PromptTokens:     80,
			CompletionTokens: 9,
			TotalTokens:      89,
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.False(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticOpenAI, summary.UsageSemantic)
	require.Equal(t, 80, summary.PromptTokens)
	require.Equal(t, 9, summary.CompletionTokens)
	require.Equal(t, 89, summary.TotalTokens)
	require.Equal(t, 98, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesOpenAIResponsesInputTokenDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-4o",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			CacheRatio:      0.25,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	responsesUsage := &dto.Usage{
		InputTokens:  100,
		OutputTokens: 10,
		TotalTokens:  110,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 40,
		},
	}
	convertedUsage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 10,
		TotalTokens:      110,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 40,
		},
		BillingUsage: dto.NewOpenAIResponsesBillingUsage(responsesUsage),
	}

	effectiveUsage := effectiveBillingUsage(convertedUsage)
	require.Equal(t, 40, effectiveUsage.PromptTokensDetails.CachedTokens)
	require.Zero(t, convertedUsage.BillingUsage.OpenAIUsage.PromptTokensDetails.CachedTokens)

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveUsage)
	require.Equal(t, 40, summary.CacheTokens)
	// 60 uncached input + 40*0.25 cached input + 10*2 output = 90.
	require.Equal(t, 90, summary.Quota)
}

func TestUsageFromOpenAIBillingUsageNormalizesCacheDetailsWithoutOverwritingCanonicalValues(t *testing.T) {
	responsesUsage := &dto.Usage{
		InputTokens:          100,
		OutputTokens:         10,
		PromptCacheHitTokens: 55,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 8,
			TextTokens:   12,
		},
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens:         40,
			CachedCreationTokens: 5,
			CacheWriteTokens:     6,
			TextTokens:           60,
			ImageTokens:          7,
			AudioTokens:          9,
		},
	}

	billingUsage := dto.NewOpenAIResponsesBillingUsage(responsesUsage)
	usage := effectiveBillingUsage(&dto.Usage{BillingUsage: billingUsage})

	require.Equal(t, 8, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 5, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 6, usage.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 12, usage.PromptTokensDetails.TextTokens)
	require.Equal(t, 7, usage.PromptTokensDetails.ImageTokens)
	require.Equal(t, 9, usage.PromptTokensDetails.AudioTokens)
	require.Zero(t, billingUsage.OpenAIUsage.PromptTokensDetails.CachedCreationTokens)
}

func TestUsageFromOpenAIBillingUsageFallsBackToPromptCacheHitTokens(t *testing.T) {
	usage := effectiveBillingUsage(&dto.Usage{
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{
			PromptTokens:         100,
			CompletionTokens:     10,
			PromptCacheHitTokens: 35,
		}),
	})

	require.Equal(t, 35, usage.PromptTokensDetails.CachedTokens)
}

func TestCalculateTextQuotaSummaryNormalizesOpenAIResponsesBillingUsageDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "gpt-5.6-sol",
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    2,
			CacheRatio:         0.5,
			CacheCreationRatio: 2,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	responsesDetails := dto.InputTokenDetails{
		CachedTokens:     80,
		CacheWriteTokens: 10,
		TextTokens:       100,
	}
	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		BillingUsage: dto.NewOpenAIResponsesBillingUsage(&dto.Usage{
			InputTokens:        100,
			OutputTokens:       10,
			TotalTokens:        110,
			InputTokensDetails: &responsesDetails,
		}),
	}

	effectiveUsage := effectiveBillingUsage(usage)
	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveUsage)

	require.Equal(t, dto.BillingUsageSourceOAIResponses, effectiveUsage.UsageSource)
	require.Equal(t, responsesDetails, effectiveUsage.PromptTokensDetails)
	require.Equal(t, 100, summary.PromptTokens)
	require.Equal(t, 10, summary.CompletionTokens)
	require.Equal(t, 80, summary.CacheTokens)
	require.Equal(t, 10, summary.CacheCreationTokens)
	// (100-80-10) + 80*0.5 + 10*2 + 10*2 = 90
	require.Equal(t, 90, summary.Quota)
}

func TestUsageBillingPathForLog(t *testing.T) {
	require.Equal(t, usageBillingPathAnthropic, usageBillingPathForLog(true, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	}))
	invalidBillingUsage := &dto.Usage{
		PromptTokens: 1,
		BillingUsage: &dto.BillingUsage{
			Source:   dto.BillingUsageSourceClaudeMessages,
			Semantic: dto.BillingUsageSemanticAnthropic,
		},
	}
	require.Equal(t, usageBillingPathLocal, usageBillingPathForLog(true, invalidBillingUsage))
	require.Equal(t, usageBillingPathUpstream, usageBillingPathForLog(false, invalidBillingUsage))
	require.Equal(t, usageBillingPathUpstream, usageBillingPathForLog(false, &dto.Usage{}))
	require.Equal(t, usageBillingPathOpenAI, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 1}),
	}))
	require.Equal(t, usageBillingPathAnthropic, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	}))
	require.Equal(t, usageBillingPathGemini, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{PromptTokenCount: 1}),
	}))
	require.Equal(t, usageBillingPathGeminiEstimated, usageBillingPathForLog(true, &dto.Usage{
		BillingUsage: dto.NewEstimatedGeminiChatBillingUsage(&dto.Usage{PromptTokens: 1}),
	}))
}

func TestAppendUsageBillingPathForLogWritesAdminInfo(t *testing.T) {
	other := model.NewLogOther()
	appendUsageBillingPathForLog(other, true, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	})

	adminInfo, ok := other.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, usageBillingPathAnthropic, adminInfo["usage_billing_path"])

	other = model.NewLogOther()
	appendUsageBillingPathForLog(other, true, nil)
	adminInfo, ok = other.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, usageBillingPathLocal, adminInfo["usage_billing_path"])
}

func TestCacheWriteTokensTotal(t *testing.T) {
	t.Run("split cache creation", func(t *testing.T) {
		summary := textQuotaSummary{
			CacheCreationTokens:   50,
			CacheCreationTokens5m: 10,
			CacheCreationTokens1h: 20,
		}
		require.Equal(t, 50, cacheWriteTokensTotal(summary))
	})

	t.Run("legacy cache creation", func(t *testing.T) {
		summary := textQuotaSummary{CacheCreationTokens: 50}
		require.Equal(t, 50, cacheWriteTokensTotal(summary))
	})

	t.Run("split cache creation without aggregate remainder", func(t *testing.T) {
		summary := textQuotaSummary{
			CacheCreationTokens5m: 10,
			CacheCreationTokens1h: 20,
		}
		require.Equal(t, 30, cacheWriteTokensTotal(summary))
	})
}

func TestCalculateTextQuotaSummaryHandlesLegacyClaudeDerivedOpenAIUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:           1,
			CompletionRatio:      5,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo:       hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     62,
		CompletionTokens: 95,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 3544,
		},
		ClaudeCacheCreation5mTokens: 586,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// 62 + 3544*0.1 + 586*1.25 + 95*5 = 1624.9 => 1624
	require.Equal(t, 1624, summary.Quota)
}

func TestCalculateTextQuotaSummaryBillsOpenAICacheWriteTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-5.1",
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    2,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	t.Run("uncached remainder stays positive", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     1473,
			CompletionTokens: 19,
			PromptTokensDetails: dto.InputTokenDetails{
				CacheWriteTokens: 1470,
			},
		}

		summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

		require.Equal(t, 1470, summary.CacheCreationTokens)
		// (1473-0-1470) + 1470*1.25 + 19*2 = 3 + 1837.5 + 38 = 1878.5 => 1879
		require.Equal(t, 1879, summary.Quota)
	})

	t.Run("uncached remainder clamps to zero", func(t *testing.T) {
		// Real OpenAI payload shape: cached_tokens + cache_write_tokens exceeds
		// prompt_tokens because both are unadjusted prefix counts. The negative
		// remainder must clamp to zero, never turn into a negative base charge.
		usage := &dto.Usage{
			PromptTokens:     3619,
			CompletionTokens: 36,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:     2921,
				CacheWriteTokens: 3616,
			},
		}

		summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

		require.Equal(t, 3619, summary.PromptTokens)
		require.Equal(t, 3616, summary.CacheCreationTokens)
		// max(3619-2921-3616, 0) + 2921*0.1 + 3616*1.25 + 36*2 = 4884.1 => 4884
		require.Equal(t, 4884, summary.Quota)
	})
}

func TestCalculateTextQuotaSummaryBillsCacheCreationInputTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "cache-creation-input",
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	t.Run("creation tokens billed at creation ratio", func(t *testing.T) {
		var usage dto.Usage
		require.NoError(t, common.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":300}}}`), &usage))
		summary := calculateTextQuotaSummary(ctx, relayInfo, &usage)

		assert.Equal(t, 1510, summary.PromptTokens)
		assert.Equal(t, 1200, summary.CacheTokens)
		assert.Equal(t, 300, summary.CacheCreationTokens)
		assert.Zero(t, summary.CacheCreationTokens5m)
		assert.Zero(t, summary.CacheCreationTokens1h)
		// (1510-1200-300) + 1200*0.1 + 300*1.25 + 20 = 525
		assert.Equal(t, 525, summary.Quota)
	})

	t.Run("missing creation field stays in prompt price", func(t *testing.T) {
		var usage dto.Usage
		require.NoError(t, common.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200}}`), &usage))
		summary := calculateTextQuotaSummary(ctx, relayInfo, &usage)

		assert.Zero(t, summary.CacheCreationTokens)
		// (1510-1200) + 1200*0.1 + 20 = 450
		assert.Equal(t, 450, summary.Quota)
	})

	t.Run("cache write and creation scalar take the larger count", func(t *testing.T) {
		var usage dto.Usage
		require.NoError(t, common.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200,"cache_write_tokens":100,"cache_creation_input_tokens":300}}`), &usage))
		summary := calculateTextQuotaSummary(ctx, relayInfo, &usage)

		assert.Equal(t, 300, summary.CacheCreationTokens)
		assert.Zero(t, summary.CacheCreationTokens5m)
		assert.Zero(t, summary.CacheCreationTokens1h)
		assert.Equal(t, 525, summary.Quota)
	})
}

// OpenAI 12h cache writes bill at twice the write price and keep their own
// count in the consume log, with ratio and expression pricing alike.
func TestPostTextConsumeQuotaBillsOpenAI12hCacheWrites(t *testing.T) {
	truncate(t)
	const userID, channelID = 63, 63
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	gin.SetMode(gin.TestMode)
	// The OpenAI prompt caching guide example at gpt-6-astra prices: $10 input,
	// $1 cached, $12.50 30m write and $25 12h write per 1M tokens.
	usage := &dto.Usage{PromptTokens: 12000, TotalTokens: 12000, PromptTokensDetails: dto.InputTokenDetails{
		CachedTokens: 4000, CacheWriteTokens: 7000,
		CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{TTL30mTokens: 3000, TTL12hTokens: 4000},
	}}
	for _, tc := range []struct {
		name     string
		price    hosttypes.PriceData
		expr     string
		ratio12h float64
	}{
		{"ratio", hosttypes.PriceData{ModelRatio: 5, CompletionRatio: 5, CacheRatio: 0.1, CacheCreationRatio: 1.25, CacheCreation12hRatio: 2.5}, "", 2.5},
		{"expression without cc12h", hosttypes.PriceData{}, `tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5)`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			tc.price.GroupRatioInfo = hosttypes.GroupRatioInfo{GroupRatio: 1}
			info := &relaycommon.RelayInfo{
				UserId:          userID,
				OriginModelName: "gpt-6-astra",
				UsingGroup:      "default",
				StartTime:       time.Now(),
				RelayFormat:     types.RelayFormatOpenAIResponses,
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
				PriceData:       tc.price,
			}
			if tc.expr != "" {
				info.TieredBillingSnapshot = makeSnapshot(tc.expr, 1, 0, 0)
			}

			PostTextConsumeQuota(ctx, info, usage, nil)

			log := getLastLog(t)
			require.NotNil(t, log)
			// 1000 input + 4000 cached + 3000 30m + 4000 12h writes = $0.1515.
			assert.Equal(t, 75750, log.Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
			assert.Equal(t, float64(7000), other["cache_creation_tokens"])
			assert.Equal(t, float64(7000), other["cache_write_tokens"])
			assert.Equal(t, float64(4000), other["cache_creation_tokens_12h"])
			assert.Equal(t, tc.ratio12h, other["cache_creation_ratio_12h"])
		})
	}
}

func TestCalculateTextQuotaSummarySeparatesOpenRouterCacheReadFromPromptBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "openai/gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2432,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// OpenRouter OpenAI-format display keeps prompt_tokens as total input,
	// but billing still separates normal input from cache read tokens.
	// quota = (2604 - 2432) + 2432*0.1 + 383 = 798.2 => 798
	require.Equal(t, 2604, summary.PromptTokens)
	require.Equal(t, 798, summary.Quota)
}

func TestCalculateTextQuotaSummarySeparatesOpenRouterCacheCreationFromPromptBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "openai/gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 100,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// prompt_tokens is still logged as total input, but cache creation is billed separately.
	// quota = (2604 - 100) + 100*1.25 + 383 = 3012
	require.Equal(t, 2604, summary.PromptTokens)
	require.Equal(t, 3012, summary.Quota)
}

func TestCalculateTextQuotaSummaryKeepsPrePRClaudeOpenRouterBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "anthropic/claude-3.7-sonnet",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2432,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// Pre-PR PostClaudeConsumeQuota behavior for OpenRouter:
	// prompt = 2604 - 2432 = 172
	// quota = 172 + 2432*0.1 + 383 = 798.2 => 798
	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, 172, summary.PromptTokens)
	require.Equal(t, 798, summary.Quota)
}

func TestComposeTieredTextQuotaKeepsToolCallSurcharges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	// 11 $/1K => 0.011 per completed image output, matching the prior fixed low-tier charge.
	operation_setting.SetToolPriceForTest(dto.BuildInToolImageGeneration, 11.0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest(dto.BuildInToolImageGeneration)
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {
					CallCount: 1,
				},
				dto.BuildInToolFileSearch: {
					CallCount: 2,
				},
				dto.BuildInToolImageGeneration: {
					CallCount: 1,
				},
			},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	quota := composeTieredTextQuota(relayInfo, summary, 1000, &billingexpr.TieredResult{
		ActualQuotaBeforeGroup: 1000,
		ActualQuotaAfterGroup:  1000,
	})

	require.Equal(t, int64(13000), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 14000, quota)
}

func TestComposeTieredTextQuotaFallbackKeepsToolCallSurcharges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1.25},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearch: {ToolName: dto.BuildInToolWebSearch, CallCount: 2},
			},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1.25,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	quota := composeTieredTextQuota(relayInfo, summary, 1250, nil)

	require.Equal(t, int64(12500), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 13750, quota)
}

func TestComposeTieredTextQuotaErrorFallbackUsesPreConsumedQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1.25},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearch: {ToolName: dto.BuildInToolWebSearch, CallCount: 2},
			},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1.25,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// tieredResult=nil simulates a settlement error where TryTieredSettle
	// falls back to FinalPreConsumedQuota (2000), which differs from
	// EstimatedQuotaBeforeGroup * GroupRatio (1250).
	preConsumedFallback := 2000
	quota := composeTieredTextQuota(relayInfo, summary, preConsumedFallback, nil)

	require.Equal(t, int64(12500), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 14500, quota)
}

// TestTryTieredSettleRecordsClampOnOverflow guards that an oversized tiered
// settlement both saturates the quota and records the clamp on RelayInfo, so
// every consume path (text, audio, WSS) can surface it under admin_info.
func TestTryTieredSettleRecordsClampOnOverflow(t *testing.T) {
	// exprOutput = p * 1e12; quotaBeforeGroup = p*1e12 / 1e6 * 5e5 far exceeds
	// the supported single-request range and must saturate.
	exprStr := `tier("base", p * 1000000000000)`
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "overflow-model",
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:  "tiered_expr",
			ExprString:   exprStr,
			ExprHash:     billingexpr.ExprHashString(exprStr),
			GroupRatio:   1,
			QuotaPerUnit: 500_000,
		},
	}

	ok, quota, result := TryTieredSettle(relayInfo, billingexpr.TokenParams{P: 1_000_000_000})

	require.True(t, ok)
	require.NotNil(t, result)
	require.Equal(t, common.MaxQuota, quota, "oversized settlement must clamp, never wrap negative")
	require.NotNil(t, relayInfo.QuotaClamp, "clamp must be recorded on RelayInfo for admin auditing")
	require.Equal(t, common.QuotaClampOverflow, relayInfo.QuotaClamp.Kind)
}

// TestTryTieredSettleNoClampInRange confirms an in-range settlement leaves
// RelayInfo.QuotaClamp nil.
func TestTryTieredSettleNoClampInRange(t *testing.T) {
	exprStr := `tier("base", p * 2 + c * 10)`
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "in-range-model",
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:  "tiered_expr",
			ExprString:   exprStr,
			ExprHash:     billingexpr.ExprHashString(exprStr),
			GroupRatio:   1,
			QuotaPerUnit: 500_000,
		},
	}

	ok, _, result := TryTieredSettle(relayInfo, billingexpr.TokenParams{P: 1000, C: 500})

	require.True(t, ok)
	require.NotNil(t, result)
	require.Nil(t, relayInfo.QuotaClamp, "in-range settlement must not record a clamp")
}

func TestCalculateTextQuotaSummaryFixedPriceAppliesImageCountOnceAndAllowsOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	priceData := hosttypes.PriceData{
		ModelPrice: 0.12,
		UsePrice:   true,
		GroupRatioInfo: hosttypes.GroupRatioInfo{
			GroupRatio: 1,
		},
	}
	priceData.AddOtherRatio("n", 3)
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "dall-e-3",
		PriceData:       priceData,
		StartTime:       time.Now(),
	}
	usage := &dto.Usage{PromptTokens: 1, TotalTokens: 1}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	require.Equal(t, 180000, summary.Quota)

	// An adaptor-reported actual count replaces the requested count rather
	// than multiplying it a second time.
	relayInfo.PriceData.AddOtherRatio("n", 2)
	summary = calculateTextQuotaSummary(ctx, relayInfo, usage)
	require.Equal(t, 120000, summary.Quota)
}

func TestCalculateTextToolCallSurchargeGeneralizedBuiltInTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	operation_setting.SetToolPriceForTest("my_fn", 5.0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("my_fn")
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {CallCount: 2},
				"my_fn":                         {CallCount: 3},
				"unpriced":                      {CallCount: 5},
			},
		},
	}
	summary := &textQuotaSummary{
		ModelName:  "o1",
		GroupRatio: 1,
	}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	expected := decimal.NewFromFloat((10.0*2 + 5.0*3) / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
	require.Len(t, summary.ToolSurchargeItems, 2)
	assert.Equal(t, "my_fn", summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 3, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 5.0, summary.ToolSurchargeItems[0].Price)
	assert.Equal(t, dto.BuildInToolWebSearchPreview, summary.ToolSurchargeItems[1].Name)
	assert.Equal(t, 2, summary.ToolSurchargeItems[1].Count)
	assert.Equal(t, 10.0, summary.ToolSurchargeItems[1].Price)
}

func TestCalculateTextToolCallSurchargeKeepsSearchPreviewFallbackWithCustomFunctions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	operation_setting.SetToolPriceForTest("my_fn", 5)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("my_fn")
	})

	relayInfo := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeChatCompletions,
		OriginModelName: "gpt-4o-search-preview",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				"my_fn": {CallCount: 1},
			},
		},
	}
	summary := &textQuotaSummary{
		ModelName:  relayInfo.OriginModelName,
		GroupRatio: 1,
	}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)

	require.Len(t, summary.ToolSurchargeItems, 2)
	assert.Equal(t, "my_fn", summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, dto.BuildInToolWebSearchPreview, summary.ToolSurchargeItems[1].Name)
	expected := decimal.NewFromFloat((5.0 + 25.0) / 1000).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
}

func TestCalculateTextToolCallSurchargeDoesNotInferSearchForResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeResponses,
		OriginModelName: "gpt-4o-search-preview",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{},
		},
	}
	summary := &textQuotaSummary{
		ModelName:  relayInfo.OriginModelName,
		GroupRatio: 1,
	}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)

	assert.True(t, surcharge.IsZero())
	assert.Empty(t, summary.ToolSurchargeItems)
}

// Every BuiltInTools count is bounded by MaxBillableToolCallCount at
// settlement, whatever recorded it (upstream-reported counts and per-item
// counts alike).
func TestCalculateTextToolCallSurchargeClampsCallCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	for recorded, billed := range map[int]int{5: 5, 10_000: 10_000, 10_005: 10_000} {
		relayInfo := &relaycommon.RelayInfo{
			OriginModelName: "claude-3-7-sonnet",
			ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
				BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
					dto.BuildInToolWebSearch: {CallCount: recorded},
				},
			},
		}
		summary := &textQuotaSummary{ModelName: relayInfo.OriginModelName, GroupRatio: 1}

		surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)

		assert.Equal(t, []ToolSurchargeItem{{Name: dto.BuildInToolWebSearch, Count: billed, Price: 10}}, summary.ToolSurchargeItems, recorded)
		expected := decimal.NewFromFloat(10.0 * float64(billed) / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		assert.True(t, expected.Equal(surcharge), "recorded %d: got %s want %s", recorded, surcharge, expected)
	}
}

func TestMergeToolSurchargeItemsSaturatesCountOverflow(t *testing.T) {
	items := []ToolSurchargeItem{
		{Name: "custom_fn", Count: math.MaxInt, Price: 5},
		{Name: "custom_fn", Count: 1, Price: 5},
	}

	merged := mergeToolSurchargeItems(items)

	require.Len(t, merged, 1)
	assert.Equal(t, math.MaxInt, merged[0].Count)
}

// A zero-token request (e.g. /v1/alpha/search returns no usage) must still
// bill a tool-call surcharge. Regression for the TotalTokens==0 gate zeroing
// out the surcharge quota.
func TestCalculateTextQuotaSummaryZeroTokensStillBillsToolSurcharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {CallCount: 1},
			},
		},
	}
	relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1

	usage := &dto.Usage{} // zero tokens, mirrors alpha search
	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.Equal(t, 0, summary.TotalTokens)
	assert.False(t, summary.ToolCallSurchargeQuota.IsZero(), "surcharge should be computed")
	assert.Greater(t, summary.Quota, 0, "quota must not be zeroed out for a zero-token web search request")
	expected := common.QuotaFromDecimal(summary.ToolCallSurchargeQuota)
	assert.Equal(t, expected, summary.Quota)
}

func TestClaudeCacheOnlyUsageRemainsBillable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		usage     dto.ClaudeUsage
		wantQuota int
	}{
		{name: "one-hour cache write", usage: dto.ClaudeUsage{CacheCreationInputTokens: 20000, CacheCreation: &dto.ClaudeCacheCreationUsage{Ephemeral1hInputTokens: 20000}}, wantQuota: 100000},
		{name: "five-minute cache write", usage: dto.ClaudeUsage{CacheCreationInputTokens: 100, CacheCreation: &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 100}}, wantQuota: 313},
		{name: "cache read", usage: dto.ClaudeUsage{CacheReadInputTokens: 100}, wantQuota: 25},
		{name: "no usage", usage: dto.ClaudeUsage{}, wantQuota: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				RelayFormat:     types.RelayFormatClaude,
				OriginModelName: "claude-opus-4-8",
				PriceData: hosttypes.PriceData{
					ModelRatio: 2.5, CompletionRatio: 5, CacheRatio: 0.1,
					CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2,
					GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
				},
			}
			usage := &dto.Usage{UsageSemantic: dto.BillingUsageSemanticAnthropic, BillingUsage: dto.NewClaudeMessagesBillingUsage(&tc.usage)}
			summary := calculateTextQuotaSummary(ctx, info, effectiveBillingUsage(usage))
			assert.Zero(t, summary.PromptTokens)
			assert.Zero(t, summary.CompletionTokens)
			assert.Equal(t, tc.wantQuota, summary.Quota)
			assert.Equal(t, tc.wantQuota > 0, summary.hasBillableUsage())
			t.Logf("input=%d output=%d quota=%d", summary.PromptTokens, summary.CompletionTokens, summary.Quota)
		})
	}
}

func TestCalculateTextQuotaSummaryDoesNotApplyRequestMultipliersToToolSurcharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {CallCount: 1},
			},
		},
	}
	relayInfo.PriceData.AddOtherRatio("n", 3)

	summary := calculateTextQuotaSummary(ctx, relayInfo, &dto.Usage{})

	expected := decimal.NewFromFloat(10.0 / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(summary.ToolCallSurchargeQuota))
	assert.Equal(t, common.QuotaFromDecimal(expected), summary.Quota)
}

func TestCalculateTextToolCallSurchargeGeminiGoogleSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.7-flash",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolGoogleSearch: {ToolName: dto.BuildInToolGoogleSearch, CallCount: 1},
			},
		},
	}
	summary := &textQuotaSummary{ModelName: "gemini-3.7-flash", GroupRatio: 1}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	expected := decimal.NewFromFloat(14.0 / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
	require.Len(t, summary.ToolSurchargeItems, 1)
	assert.Equal(t, dto.BuildInToolGoogleSearch, summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 1, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 14.0, summary.ToolSurchargeItems[0].Price)
}

func TestCalculateTextToolCallSurchargeGeminiFunctionCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	operation_setting.SetToolPriceForTest("gemini_surcharge_fn", 5.0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("gemini_surcharge_fn")
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-flash",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				"gemini_surcharge_fn": {CallCount: 2},
			},
		},
	}
	summary := &textQuotaSummary{ModelName: "gemini-2.5-flash", GroupRatio: 1}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	expected := decimal.NewFromFloat(5.0 * 2 / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
	require.Len(t, summary.ToolSurchargeItems, 1)
	assert.Equal(t, "gemini_surcharge_fn", summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 2, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 5.0, summary.ToolSurchargeItems[0].Price)

	other := model.NewLogOther()
	appendToolSurchargeLogInfo(other, summary.ToolSurchargeItems)
	assert.Equal(t, summary.ToolSurchargeItems, other.Snapshot()["tool_surcharges"])
}

func TestCalculateTextToolCallSurchargeImageGenerationDefaultPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest(dto.BuildInToolImageGeneration)
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolImageGeneration: {CallCount: 2},
			},
		},
	}
	summary := &textQuotaSummary{ModelName: "gpt-5.1", GroupRatio: 1.5}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	expected := decimal.NewFromFloat(150.0).
		Mul(decimal.NewFromInt(2)).
		Div(decimal.NewFromInt(1000)).
		Mul(decimal.NewFromFloat(1.5)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
	require.Len(t, summary.ToolSurchargeItems, 1)
	assert.Equal(t, dto.BuildInToolImageGeneration, summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 2, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 150.0, summary.ToolSurchargeItems[0].Price)
}

func TestCalculateTextToolCallSurchargeImageGenerationExplicitZeroDisables(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	operation_setting.SetToolPriceForTest(dto.BuildInToolImageGeneration, 0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest(dto.BuildInToolImageGeneration)
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolImageGeneration: {CallCount: 3},
			},
		},
	}
	summary := &textQuotaSummary{ModelName: "gpt-5.1", GroupRatio: 1}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	assert.True(t, surcharge.IsZero())
	assert.Empty(t, summary.ToolSurchargeItems)
}

func TestCalculateTextQuotaSummaryImageGenerationUsesStructuredSurcharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest(dto.BuildInToolImageGeneration)
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolImageGeneration: {CallCount: 1},
			},
		},
	}
	relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1
	relayInfo.PriceData.ModelRatio = 1
	relayInfo.PriceData.CompletionRatio = 1

	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.Len(t, summary.ToolSurchargeItems, 1)
	assert.Equal(t, dto.BuildInToolImageGeneration, summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 1, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 150.0, summary.ToolSurchargeItems[0].Price)

	expectedSurcharge := decimal.NewFromFloat(150.0 / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expectedSurcharge.Equal(summary.ToolCallSurchargeQuota),
		"got %s want %s", summary.ToolCallSurchargeQuota, expectedSurcharge)
	assert.Greater(t, summary.Quota, 0)
}

func TestAppendToolSurchargeLogInfoWritesOnlyStructuredFields(t *testing.T) {
	items := []ToolSurchargeItem{
		{Name: dto.BuildInToolWebSearch, Count: 2, Price: 10},
		{Name: dto.BuildInToolImageGeneration, Count: 1, Price: 150},
	}
	other := model.NewLogOther()

	appendToolSurchargeLogInfo(other, items)

	fields := other.Snapshot()
	assert.Equal(t, items, fields["tool_surcharges"])
	assert.NotContains(t, fields, "web_search")
	assert.NotContains(t, fields, "web_search_call_count")
	assert.NotContains(t, fields, "web_search_price")
	assert.NotContains(t, fields, "file_search")
	assert.NotContains(t, fields, "image_generation_call")
	assert.NotContains(t, fields, "image_generation_call_price")
}

// A request whose upstream returns no usage is logged without a charge. The
// reason is stored as content_parts, which the web console renders in the
// viewer's language; content keeps the English text.
func TestPostTextConsumeQuotaLogsMissingUsageAsStructuredContent(t *testing.T) {
	truncate(t)
	const userID, channelID = 62, 62
	seedUser(t, userID, 10_000)
	seedChannel(t, channelID)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		OriginModelName: "missing-usage-model",
		UsingGroup:      "default",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
		PriceData:       hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
	}

	PostTextConsumeQuota(ctx, info, &dto.Usage{}, nil)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Zero(t, log.Quota)
	assert.Equal(t, "Upstream returned no usage, so nothing was charged (possibly an upstream timeout)", log.Content)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.Equal(t, []any{
		map[string]any{"key": "Upstream returned no usage, so nothing was charged (possibly an upstream timeout)"},
	}, other["content_parts"])
}

// This extends the existing billing integration fixture: actual model APIs and
// database balances are used; there is no fake funding implementation.
func TestMarketingBillingSessionLifecycle(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect != common.DatabaseTypeSQLite {
				env := "MV2_MYSQL_DSN"
				if dialect == common.DatabaseTypePostgreSQL {
					env = "MV2_POSTGRES_DSN"
				}
				dsn := os.Getenv(env)
				if dsn == "" {
					if os.Getenv("MV2_REQUIRE_DATABASES") == "1" {
						t.Fatal(env + " required")
					}
					t.Skip(env + " is not configured")
				}
				if dialect == common.DatabaseTypeMySQL {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			common.SetDatabaseTypes(dialect, dialect)
			t.Cleanup(func() { common.SetDatabaseTypes(oldMain, oldLog) })
			runMarketingBillingSessionLifecycle(t, driver)
		})
	}
}

func runMarketingBillingSessionLifecycle(t *testing.T, driver gorm.Dialector) {
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	previous, previousLog := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() { model.DB, model.LOG_DB = previous, previousLog })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.MarketingAccount{}, &model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingLedgerEntry{}, &model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.MarketingDispatch{}, &model.MarketingUsageProjection{}, &model.MarketingRealtimeRound{}, &model.MarketingRealtimeEvent{}, &model.MarketingActivity{}, &model.MarketingActivityOperation{}, &model.MarketingInvitation{}, &model.MarketingFactSequence{}, &model.MarketingMaintenanceOperation{}, &model.TopUp{}, &model.Redemption{}, &model.UserSubscription{}, &model.MarketingActivityFact{}, &model.MarketingActivityProgress{}, &model.MarketingActivityDecision{}, &model.MarketingActivityReward{}, &model.MarketingActivityDeliveryCursor{}, &model.QuotaData{}, &model.Log{}, &model.Task{}, &model.Midjourney{}))
	require.NoError(t, db.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)

	runTag := fmt.Sprint(time.Now().UnixNano())
	for index, scenario := range []string{"zero_estimate_wallet", "zero_estimate_token", "zero_estimate_free", "zero_estimate_released", "estimated_usage", "responses_estimated_usage", "estimated_canonical", "empty_usage", "fixed_without_usage", "fixed_estimated_branch", "nonstream_missing_terminal", "failed_terminal_with_done", "gift_then_paid", "before_dispatch", "ambiguous_dispatch", "crash_without_projection", "revoked_before_dispatch", "text_terminal", "stream_interrupted", "log_outage", "log_retention", "logs_disabled", "export_disabled", "task_recovery", "task_timeout", "task_unknown_usage", "task_provider_failure", "mj_recovery", "mj_timeout", "mj_provider_failure", "violation_fee", "realtime_two_rounds", "realtime_cancelled_usage", "realtime_unknown_late", "realtime_buffer_zero", "realtime_output_zero", "realtime_pending_input", "realtime_pricing_failure", "realtime_audio_mismatch", "realtime_pipelined_commit", "realtime_automatic_expiry", "realtime_terminal_metadata", "realtime_metadata_capacity", "realtime_vad_normal", "realtime_vad_two_inputs", "realtime_vad_uncertain_config", "realtime_vad_default", "realtime_vad_partial", "realtime_vad_ga_default", "realtime_vad_partial_disabled", "realtime_stopped_start_late", "realtime_stopped_text_zero", "realtime_stopped_audio_zero"} {
		t.Run(scenario, func(t *testing.T) {
			user := model.User{Username: "mv2-" + fmt.Sprint(index) + runTag, AffCode: fmt.Sprintf("m%d%s", index, runTag[len(runTag)-6:]), Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error)
			token := model.Token{UserId: user.Id, Key: fmt.Sprintf("marketing-test-%d-%s", index, runTag), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
			if scenario == "zero_estimate_token" {
				token.RemainQuota = 0
			}
			require.NoError(t, db.Create(&token).Error)
			now := common.GetTimestamp()
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				if scenario == "zero_estimate_wallet" || scenario == "zero_estimate_free" {
					return nil
				}
				_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: scenario + runTag, PaidCents: 100, PostedAt: now, Money: model.MarketingMoneySnapshot{Amount: "100", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
				if err != nil {
					return err
				}
				grantedAt, expiresAt := now, now+100
				if scenario == "realtime_automatic_expiry" || scenario == "realtime_terminal_metadata" {
					grantedAt, expiresAt = now-100, now-1
				}
				_, err = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: "recharge", Reference: scenario + runTag, GrantedAt: grantedAt, ExpiresAt: expiresAt, Money: model.MarketingMoneySnapshot{Amount: "20", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
				return err
			}))
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, RequestId: "marketing-" + scenario + runTag, StartTime: time.Unix(now, 0), ChannelMeta: &relaycommon.ChannelMeta{}, OriginModelName: "marketing-model", PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
			vadNormal := scenario == "realtime_vad_normal" || scenario == "realtime_vad_default" || scenario == "realtime_vad_partial" || scenario == "realtime_vad_ga_default"
			vadScenario := vadNormal || scenario == "realtime_vad_two_inputs" || scenario == "realtime_vad_uncertain_config" || scenario == "realtime_vad_partial_disabled"
			if vadScenario {
				info.PriceData.AudioRatio = 1
			}
			if scenario == "realtime_pricing_failure" {
				expression := `p == 0 ? tier("empty", p) : tier("invalid", c / (p-p))`
				info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), ExprVersion: 1, QuotaPerUnit: 1_000_000, GroupRatio: 1}
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			ctx.Set(common.RequestIdKey, info.RequestId)
			ctx.Set("username", user.Username)
			if strings.HasPrefix(scenario, "zero_estimate_") {
				info.PriceData.FreeModel = scenario == "zero_estimate_free"
				apiErr := PreConsumeBilling(ctx, 0, info)
				if scenario == "zero_estimate_wallet" || scenario == "zero_estimate_token" {
					require.NotNil(t, apiErr, "zero estimation cannot authorize unfunded work")
					var count int64
					require.NoError(t, db.Model(&model.MarketingFunding{}).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
					return
				}
				require.Nil(t, apiErr)
				session := info.Billing.(*MarketingBillingSession)
				require.ErrorIs(t, session.ReserveBeforeSubmit(-1), model.ErrMarketingInput)
				require.NoError(t, session.ReserveBeforeSubmit(0))
				wantHold := 1
				if info.PriceData.FreeModel {
					wantHold = 0
				}
				assert.Equal(t, wantHold, session.GetPreConsumedQuota())
				assert.EqualValues(t, wantHold, session.funding.ReservedQuota)
				require.NoError(t, session.BeginDispatch())
				require.NoError(t, session.Settle(0))
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				assert.Zero(t, user.UsedQuota, "minimum admission hold is not a minimum charge")
				assert.Zero(t, token.UsedQuota)
				assert.Zero(t, session.funding.ReservedQuota)
				assert.Equal(t, "settled", session.funding.State)
				return
			}
			initialEstimate, submitEstimate := 10, 30
			if scenario == "realtime_stopped_start_late" || scenario == "realtime_stopped_text_zero" || scenario == "realtime_stopped_audio_zero" {
				initialEstimate, submitEstimate = 0, 0 // Real websocket admission may have no input tokens yet.
			}
			require.Nil(t, PreConsumeBilling(ctx, initialEstimate, info))
			session, ok := info.Billing.(*MarketingBillingSession)
			require.True(t, ok)
			require.NoError(t, session.ReserveBeforeSubmit(submitEstimate))
			assert.Equal(t, max(submitEstimate, 1), session.GetPreConsumedQuota())
			switch scenario {
			case "realtime_two_rounds", "realtime_cancelled_usage", "realtime_unknown_late", "realtime_buffer_zero", "realtime_output_zero", "realtime_pending_input", "realtime_pricing_failure", "realtime_audio_mismatch", "realtime_pipelined_commit", "realtime_automatic_expiry", "realtime_terminal_metadata", "realtime_metadata_capacity", "realtime_vad_normal", "realtime_vad_two_inputs", "realtime_vad_uncertain_config", "realtime_vad_default", "realtime_vad_partial", "realtime_vad_ga_default", "realtime_vad_partial_disabled", "realtime_stopped_start_late", "realtime_stopped_text_zero", "realtime_stopped_audio_zero":
				coordinator, err := NewMarketingRealtimeCoordinator(ctx, info)
				require.NoError(t, err)
				assert.Zero(t, session.funding.ReservedQuota, "connection setup cannot retain old gift eligibility")
				at := time.Unix(now, 0)
				if scenario == "realtime_stopped_start_late" || scenario == "realtime_stopped_text_zero" || scenario == "realtime_stopped_audio_zero" {
					require.Equal(t, 1, session.GetPreConsumedQuota(), "a zero input estimate still reserves one unit for admission, not consumption")
					create, err := coordinator.ProcessClientEvent([]byte(`{"type":"response.create"}`), at)
					require.NoError(t, err)
					require.NoError(t, coordinator.ServerEvent(marketingRealtimeFixtureEcho(t, []byte(`{"type":"response.created","response":{"id":"consume-token-budget"}}`), create), at))
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"consume-token-budget","status":"completed","usage":{"total_tokens":100,"input_tokens":80,"output_tokens":20,"input_token_details":{"text_tokens":80},"output_token_details":{"text_tokens":20}}}}`), at))
					require.NoError(t, db.First(&token, token.Id).Error)
					require.Zero(t, token.RemainQuota, "the first real generation naturally exhausts the token budget")
					require.Equal(t, 100, token.UsedQuota)
					require.Error(t, coordinator.ServerEvent([]byte(`{"type":"response.created","response":{"id":"automatic-unfunded"}}`), at), "automatic generation must immediately signal upstream stop")
					var rounds []model.MarketingRealtimeRound
					require.NoError(t, db.Where("funding_id = ?", session.funding.ID).Order("id").Find(&rounds).Error)
					require.Len(t, rounds, 2)
					stoppedID := rounds[1].ID
					assert.NotEmpty(t, rounds[1].ResponseKey, "an admission stop cannot discard an already observed provider identity")
					assert.EqualValues(t, 2, rounds[1].ResponseOrder)
					assert.Equal(t, now, rounds[1].QualifiedAt)
					if scenario == "realtime_stopped_start_late" {
						require.NoError(t, coordinator.Close("upstream stopped before trusted terminal arrived"))
						late := []byte(`{"type":"response.done","response":{"id":"automatic-unfunded","status":"completed","usage":{"total_tokens":5,"input_tokens":2,"output_tokens":3,"input_token_details":{"text_tokens":2},"output_token_details":{"text_tokens":3}}}}`)
						recovered, err := ReconcileMarketingRealtimeResponse(user.Id, session.funding.ID, late, at)
						require.ErrorIs(t, err, model.ErrMarketingReview, "real cost survives recovery even though the token cannot pay it")
						require.NotNil(t, recovered)
						assert.EqualValues(t, 105, recovered.ActualQuota)
						assert.EqualValues(t, 100, recovered.SettledQuota)
						assert.Equal(t, "review", recovered.State)
						repeated, err := ReconcileMarketingRealtimeResponse(user.Id, session.funding.ID, late, at)
						require.ErrorIs(t, err, model.ErrMarketingReview)
						assert.Equal(t, recovered.Revision, repeated.Revision)
						var stopped model.MarketingRealtimeRound
						require.NoError(t, db.First(&stopped, stoppedID).Error)
						assert.Equal(t, now, stopped.QualifiedAt)
						assert.Contains(t, stopped.UsageJSON, `"total_tokens":5`)
						assert.EqualValues(t, 5, stopped.TargetQuota)
						assert.False(t, stopped.Unknown)
					} else {
						delta := []byte(`{"type":"response.output_text.delta","response_id":"automatic-unfunded","delta":"actually generated"}`)
						if scenario == "realtime_stopped_audio_zero" {
							delta = []byte(`{"type":"response.output_audio.delta","response_id":"automatic-unfunded","delta":"AAAA"}`)
						}
						require.Error(t, coordinator.ServerEvent(delta, at), "drained output keeps the stop condition while preserving facts")
						require.ErrorIs(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"automatic-unfunded","status":"completed","usage":{"total_tokens":0,"input_tokens":0,"output_tokens":0}}}`), at), model.ErrMarketingReview)
						require.NoError(t, coordinator.Close("drained output contradicts explicit zero terminal"))
						var stopped model.MarketingRealtimeRound
						require.NoError(t, db.First(&stopped, stoppedID).Error)
						assert.True(t, stopped.OutputObserved)
						assert.Equal(t, scenario == "realtime_stopped_audio_zero", stopped.AudioOutputObserved)
						assert.True(t, stopped.Unknown)
						assert.Empty(t, stopped.UsageJSON, "zero is not accepted as actual usage after observed output")
						assert.Equal(t, "review", session.funding.State)
						assert.False(t, session.funding.CostKnown)
						assert.EqualValues(t, 100, session.funding.SettledQuota)
					}
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 20, user.Quota)
					require.NoError(t, db.First(&token, token.Id).Error)
					assert.Zero(t, token.RemainQuota)
					assert.Equal(t, 100, token.UsedQuota)
					return
				}

				if vadScenario {
					serverConfig := []byte(`{"type":"session.created","session":{"input_audio_format":"pcm16","output_audio_format":"pcm16","input_audio_transcription":null,"turn_detection":{"type":"server_vad","create_response":true,"idle_timeout_ms":null}}}`)
					if scenario == "realtime_vad_uncertain_config" {
						serverConfig = []byte(`{"type":"session.created","session":{"input_audio_format":"pcm16","output_audio_format":"pcm16","turn_detection":{"type":"server_vad","create_response":true}}}`)
					}
					if scenario == "realtime_vad_default" || scenario == "realtime_vad_partial" {
						// Configuration fields from the official default session example:
						// automatic creation and idle timeout are both omitted.
						serverConfig = []byte(`{"type":"session.created","session":{"input_audio_format":"pcm16","output_audio_format":"pcm16","input_audio_transcription":null,"turn_detection":{"type":"server_vad","threshold":0.5,"prefix_padding_ms":300,"silence_duration_ms":200}}}`)
					}
					if scenario == "realtime_vad_ga_default" {
						serverConfig = []byte(`{"type":"session.created","session":{"audio":{"input":{"format":{"type":"audio/pcm","rate":24000},"transcription":null,"turn_detection":{"type":"server_vad","threshold":0.5}},"output":{"format":{"type":"audio/pcm","rate":24000}}}}}`)
					}
					if scenario == "realtime_vad_partial_disabled" {
						serverConfig = []byte(`{"type":"session.created","session":{"input_audio_format":"pcm16","input_audio_transcription":null,"turn_detection":{"type":"server_vad","create_response":false}}}`)
					}
					require.NoError(t, coordinator.ServerEvent(serverConfig, at))
					if scenario == "realtime_vad_partial" || scenario == "realtime_vad_partial_disabled" {
						require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"session.updated","session":{"turn_detection":{"threshold":0.7}}}`), at))
					}
					if scenario == "realtime_vad_ga_default" {
						require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"session.updated","session":{"audio":{"input":{"turn_detection":{"threshold":0.7}}}}}`), at))
					}
					segments := 1
					if scenario == "realtime_vad_two_inputs" {
						segments = 2
					}
					audio, err := common.Marshal(map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 4800))})
					require.NoError(t, err)
					for segment := range segments {
						require.NoError(t, coordinator.ClientEvent(audio, at))
						item := fmt.Sprintf("vad-input-%d", segment)
						started, err := common.Marshal(map[string]any{"type": "input_audio_buffer.speech_started", "audio_start_ms": segment * 100, "item_id": item})
						require.NoError(t, err)
						stopped, err := common.Marshal(map[string]any{"type": "input_audio_buffer.speech_stopped", "audio_end_ms": (segment + 1) * 100, "item_id": item})
						require.NoError(t, err)
						committed, err := common.Marshal(map[string]any{"type": "input_audio_buffer.committed", "item_id": item})
						require.NoError(t, err)
						require.NoError(t, coordinator.ServerEvent(started, at))
						require.NoError(t, coordinator.ServerEvent(stopped, at))
						require.NoError(t, coordinator.ServerEvent(committed, at))
					}
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.created","response":{"id":"vad-response"}}`), at))
					terminal := []byte(`{"type":"response.done","response":{"id":"vad-response","status":"completed","usage":{"total_tokens":23,"input_tokens":3,"output_tokens":20,"input_token_details":{"audio_tokens":3},"output_token_details":{"text_tokens":20}}}}`)
					require.NoError(t, coordinator.ServerEvent(terminal, at))
					require.NoError(t, coordinator.ServerEvent(terminal, at), "repeated terminal cannot charge input twice")
					require.NoError(t, coordinator.Close("VAD response finished; verify independent input admission"))
					assert.EqualValues(t, 23, session.funding.ActualQuota)
					assert.EqualValues(t, 23, session.funding.SettledQuota)
					assert.Equal(t, 23, coordinator.Usage().TotalTokens)
					remainingHold := int64(segments * 3)
					if vadNormal {
						remainingHold = 0
						assert.Equal(t, "settled", session.funding.State)
						assert.False(t, session.funding.RealtimeInputPending)
					} else {
						assert.Equal(t, "review", session.funding.State)
						assert.True(t, session.funding.RealtimeInputPending)
						assert.False(t, session.funding.CostKnown)
					}
					var inputAllocations []model.MarketingAllocation
					require.NoError(t, db.Where("funding_id = ? AND round_id = ?", session.funding.ID, 0).Find(&inputAllocations).Error)
					var inputHeld int64
					for _, allocation := range inputAllocations {
						inputHeld += allocation.ReservedQuota
					}
					assert.Equal(t, remainingHold, inputHeld, "only unassociated input keeps an admission reservation")
					assert.Equal(t, remainingHold, session.funding.ReservedQuota)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.EqualValues(t, 97-remainingHold, user.Quota)
					var gift model.MarketingGift
					require.NoError(t, db.Where("user_id = ?", user.Id).First(&gift).Error)
					assert.Equal(t, 20-remainingHold, gift.ConsumedQuota)
					assert.Equal(t, remainingHold, gift.ReservedQuota)
					require.NoError(t, db.First(&token, token.Id).Error)
					assert.Equal(t, 77, token.RemainQuota)
					assert.Equal(t, 23, token.UsedQuota)
					var rounds []model.MarketingRealtimeRound
					require.NoError(t, db.Where("funding_id = ?", session.funding.ID).Find(&rounds).Error)
					require.Len(t, rounds, 1, "input items cannot become billable response rounds")
					assert.Equal(t, now, rounds[0].QualifiedAt)
					require.NoError(t, model.RecoverMarketingUsage(context.Background(), 100))
					logs, count, err := model.GetUserLogs(user.Id, model.LogFilter{}, 0, 10)
					require.NoError(t, err)
					require.EqualValues(t, 1, count)
					require.Len(t, logs, 1)
					assert.Equal(t, 23, logs[0].Quota)
					return
				}

				if scenario == "realtime_metadata_capacity" {
					metadata := map[string]any{}
					for i := range 16 {
						metadata[fmt.Sprintf("client_%02d", i)] = fmt.Sprint(i)
					}
					raw, err := common.Marshal(map[string]any{"type": "response.create", "response": map[string]any{"metadata": metadata}})
					require.NoError(t, err)
					forwarded, err := coordinator.ProcessClientEvent(raw, at)
					require.NoError(t, err, "valid full metadata must not require a provider allowlist or lose a user field")
					var sent map[string]any
					require.NoError(t, common.Unmarshal(forwarded, &sent))
					assert.Equal(t, metadata, sent["response"].(map[string]any)["metadata"])
					require.NoError(t, coordinator.ServerEvent(marketingRealtimeFixtureEcho(t, []byte(`{"type":"response.created","response":{"id":"full-metadata"}}`), forwarded), at))
					_, err = coordinator.ProcessClientEvent(raw, at)
					require.ErrorIs(t, err, model.ErrMarketingConflict, "the same full fingerprint cannot identify two unfinished manual responses")
					done := marketingRealtimeFixtureEcho(t, []byte(`{"type":"response.done","response":{"id":"full-metadata","status":"completed","usage":{"total_tokens":30,"input_tokens":10,"output_tokens":20,"input_token_details":{"text_tokens":10},"output_token_details":{"text_tokens":20}}}}`), forwarded)
					visible, err := coordinator.ProcessServerEvent(done, at)
					require.NoError(t, err)
					var received map[string]any
					require.NoError(t, common.Unmarshal(visible, &received))
					assert.Equal(t, metadata, received["response"].(map[string]any)["metadata"])
					secondCreate, err := coordinator.ProcessClientEvent(raw, at)
					require.NoError(t, err, "a terminal response with stable identity makes the same full metadata reusable")
					require.NoError(t, coordinator.ServerEvent(done, at), "late first terminal must retain its own response identity while second is pending")
					assert.EqualValues(t, 30, session.funding.SettledQuota)
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.created","response":{"id":"full-metadata-second"}}`), at))
					secondDone := marketingRealtimeFixtureEcho(t, []byte(`{"type":"response.done","response":{"id":"full-metadata-second","status":"completed","usage":{"total_tokens":5,"input_tokens":5,"output_tokens":0,"input_token_details":{"text_tokens":5}}}}`), secondCreate)
					require.NoError(t, coordinator.Close("lost socket before second full-metadata terminal"))
					assert.Equal(t, "review", session.funding.State)
					recovered, err := ReconcileMarketingRealtimeResponse(user.Id, session.funding.ID, secondDone, at)
					require.NoError(t, err, "metadata-only correlation must survive process loss and preserve the newer intent")
					assert.Equal(t, "settled", recovered.State)
					assert.EqualValues(t, 35, recovered.SettledQuota)
					replayed, err := ReconcileMarketingRealtimeResponse(user.Id, session.funding.ID, done, at)
					require.NoError(t, err, "an old full-metadata terminal remains tied to its persisted response")
					assert.Equal(t, recovered.Revision, replayed.Revision)
					assert.EqualValues(t, 35, replayed.SettledQuota)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 85, user.Quota)
					var rounds []model.MarketingRealtimeRound
					require.NoError(t, db.Where("funding_id = ? AND observed = ?", session.funding.ID, false).Order("id").Find(&rounds).Error)
					require.Len(t, rounds, 2)
					assert.NotEqual(t, rounds[0].ReferenceKey, rounds[1].ReferenceKey, "a repeated fingerprint must create a genuinely new durable intent")
					return
				}
				if scenario == "realtime_terminal_metadata" {
					// The supplier promises metadata at done, not necessarily at created.
					// An intervening automatic reply must never take the earlier gift.
					create, err := coordinator.ProcessClientEvent([]byte(`{"type":"response.create","response":{"metadata":{"_silvermoon_request":"client-owned","label":"preserve"}}}`), at.Add(-2*time.Second))
					require.NoError(t, err)
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.created","response":{"id":"auto-between"}}`), at))
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"auto-between","status":"completed","usage":{"total_tokens":5,"input_tokens":5,"output_tokens":0,"input_token_details":{"text_tokens":5}}}}`), at))
					var gift model.MarketingGift
					require.NoError(t, db.Where("user_id = ?", user.Id).First(&gift).Error)
					assert.Zero(t, gift.ConsumedQuota, "automatic response cannot take the pending manual round's gift")
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 95, user.Quota, "automatic actual usage settles even while manual create awaits correlation")
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.created","response":{"id":"manual-late"}}`), at))
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.output_text.delta","response_id":"manual-late","delta":"hi"}`), at))
					done := marketingRealtimeFixtureEcho(t, []byte(`{"type":"response.done","response":{"id":"manual-late","status":"completed","usage":{"total_tokens":30,"input_tokens":10,"output_tokens":20,"input_token_details":{"text_tokens":10},"output_token_details":{"text_tokens":20}}}}`), create)
					forwarded, err := coordinator.ProcessServerEvent(done, at)
					require.NoError(t, err)
					var visible map[string]any
					require.NoError(t, common.Unmarshal(forwarded, &visible))
					assert.Equal(t, map[string]any{"_silvermoon_request": "client-owned", "label": "preserve"}, visible["response"].(map[string]any)["metadata"])
					require.NoError(t, coordinator.ServerEvent(done, at))
					require.NoError(t, coordinator.Close("terminal-only metadata correlation completed"))
					assert.Equal(t, "settled", session.funding.State)
					assert.EqualValues(t, 35, session.funding.SettledQuota)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 85, user.Quota)
					require.NoError(t, db.First(&gift, gift.ID).Error)
					assert.EqualValues(t, 20, gift.ConsumedQuota)
					var rounds []model.MarketingRealtimeRound
					require.NoError(t, db.Where("funding_id = ? AND superseded_by = ?", session.funding.ID, 0).Order("response_order").Find(&rounds).Error)
					require.Len(t, rounds, 2)
					assert.Equal(t, now, rounds[0].QualifiedAt)
					assert.Equal(t, now-2, rounds[1].QualifiedAt)
					assert.True(t, rounds[1].OutputObserved, "late binding must preserve observed generation evidence")
					return
				}
				if scenario == "realtime_automatic_expiry" {
					// No client create or input timestamp exists for these idle replies.
					// A duplicate notification cannot refresh or backdate the first start.
					firstStart := []byte(`{"type":"response.created","response":{"id":"idle-first"}}`)
					require.NoError(t, coordinator.ServerEvent(firstStart, at.Add(-2*time.Second)))
					require.NoError(t, coordinator.ServerEvent(firstStart, at))
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"idle-first","status":"completed","usage":{"total_tokens":15,"input_tokens":5,"output_tokens":10,"input_token_details":{"text_tokens":5},"output_token_details":{"text_tokens":10}}}}`), at))
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.created","response":{"id":"idle-second"}}`), at))
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"idle-second","status":"completed","usage":{"total_tokens":25,"input_tokens":5,"output_tokens":20,"input_token_details":{"text_tokens":5},"output_token_details":{"text_tokens":20}}}}`), at))
					require.NoError(t, coordinator.Close("both idle responses completed"))
					assert.Equal(t, "settled", session.funding.State)
					assert.EqualValues(t, 40, session.funding.SettledQuota)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 75, user.Quota, "expired gift cannot fund the second automatic response")
					var gift model.MarketingGift
					require.NoError(t, db.Where("user_id = ?", user.Id).First(&gift).Error)
					assert.EqualValues(t, 15, gift.ConsumedQuota)
					var rounds []model.MarketingRealtimeRound
					require.NoError(t, db.Where("funding_id = ?", session.funding.ID).Order("id").Find(&rounds).Error)
					require.Len(t, rounds, 2)
					assert.Equal(t, now-2, rounds[0].QualifiedAt)
					assert.Equal(t, now, rounds[1].QualifiedAt)
					return
				}
				if scenario == "realtime_buffer_zero" {
					require.NoError(t, coordinator.ClientEvent([]byte(`{"type":"input_audio_buffer.append","audio":""}`), at))
					require.NoError(t, coordinator.Close("socket lost before any final usage"))
					assert.Equal(t, "review", session.funding.State)
					assert.False(t, session.funding.CostKnown)
					return
				}
				if scenario == "realtime_pending_input" {
					require.NoError(t, coordinator.ClientEvent([]byte(`{"type":"input_audio_buffer.append","audio":"AAAA"}`), at))
					require.NoError(t, coordinator.ClientEvent([]byte(`{"type":"input_audio_buffer.commit"}`), at))
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"input_audio_buffer.committed","item_id":"input-A"}`), at))
					require.NoError(t, coordinator.ClientEvent([]byte(`{"type":"input_audio_buffer.append","audio":"AAAA"}`), at))
				}
				if scenario == "realtime_pipelined_commit" {
					require.NoError(t, coordinator.ClientEvent([]byte(`{"type":"input_audio_buffer.append","audio":"AAAA"}`), at))
					require.NoError(t, coordinator.ClientEvent([]byte(`{"type":"input_audio_buffer.commit"}`), at))
				}
				forwardedCreate, err := coordinator.ProcessClientEvent([]byte(`{"type":"response.create"}`), at)
				require.NoError(t, err)
				if scenario == "realtime_pipelined_commit" {
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"input_audio_buffer.committed","item_id":"input-A"}`), at))
				}

				require.NoError(t, coordinator.ServerEvent(marketingRealtimeFixtureEcho(t, []byte(`{"type":"response.created","response":{"id":"first"}}`), forwardedCreate), at))
				terminal := []byte(`{"type":"response.done","response":{"id":"first","status":"completed","usage":{"total_tokens":30,"input_tokens":10,"output_tokens":20,"input_token_details":{"text_tokens":10},"output_token_details":{"text_tokens":20}}}}`)
				if scenario == "realtime_cancelled_usage" {
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.output_text.delta","response_id":"first","delta":"hello"}`), at))
					require.NoError(t, coordinator.ClientEvent([]byte(`{"type":"response.cancel","response_id":"first"}`), at))
					terminal = []byte(`{"type":"response.done","response":{"id":"first","status":"cancelled","status_details":{"type":"cancelled","reason":"client_cancelled"},"usage":{"total_tokens":30,"input_tokens":10,"output_tokens":20,"input_token_details":{"text_tokens":10},"output_token_details":{"text_tokens":20}}}}`)
				}

				if scenario == "realtime_audio_mismatch" {
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.output_audio.delta","response_id":"first","delta":"AAAA"}`), at))
					require.ErrorIs(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"first","status":"completed","usage":{"total_tokens":1,"input_tokens":0,"output_tokens":1,"output_token_details":{"text_tokens":1,"audio_tokens":0}}}}`), at), model.ErrMarketingReview)
					require.NoError(t, coordinator.Close("audio output contradicts text-only usage"))
					var round model.MarketingRealtimeRound
					require.NoError(t, db.Where("funding_id = ?", session.funding.ID).First(&round).Error)
					assert.True(t, round.AudioOutputObserved)
					assert.True(t, round.Unknown)
					assert.Equal(t, "review", session.funding.State)
					return
				}
				if scenario == "realtime_pipelined_commit" {
					require.NoError(t, coordinator.ServerEvent(terminal, at))
					require.NoError(t, coordinator.Close("pipelined commit completed"))
					assert.False(t, session.funding.RealtimeInputPending)
					assert.Equal(t, "settled", session.funding.State)
					assert.EqualValues(t, 30, session.funding.SettledQuota)
					return
				}
				if scenario == "realtime_output_zero" {
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.output_text.delta","response_id":"first","delta":"hello"}`), at))
					require.ErrorIs(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"first","status":"completed","usage":{"total_tokens":0,"input_tokens":0,"output_tokens":0}}}`), at), model.ErrMarketingReview)
					require.NoError(t, coordinator.Close("contradictory supplier usage"))
					assert.Equal(t, "review", session.funding.State)
					var round model.MarketingRealtimeRound
					require.NoError(t, db.Where("funding_id = ?", session.funding.ID).First(&round).Error)
					assert.True(t, round.OutputObserved)
					assert.True(t, round.Unknown)
					return
				}
				if scenario == "realtime_pricing_failure" {
					require.Error(t, coordinator.ServerEvent(terminal, at))
					var round model.MarketingRealtimeRound
					require.NoError(t, db.Where("funding_id = ?", session.funding.ID).First(&round).Error)
					assert.Contains(t, round.UsageJSON, `"total_tokens":30`, "pricing failure must not rollback supplier facts")
					require.NoError(t, coordinator.Close("pricing failure with durable facts"))
					assert.Equal(t, "review", session.funding.State)
					return
				}
				if scenario == "realtime_pending_input" {
					require.NoError(t, coordinator.ServerEvent(terminal, at))
					require.NoError(t, coordinator.Close("input B has no final usage"))
					assert.Equal(t, "review", session.funding.State)
					assert.True(t, session.funding.RealtimeInputPending)
					return
				}
				if scenario == "realtime_unknown_late" {
					require.ErrorIs(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"first","status":"cancelled"}}`), at), model.ErrMarketingReview)
					require.NoError(t, coordinator.Close("socket lost without final usage"))
					assert.Equal(t, "review", session.funding.State)
					// Recovery cannot depend on the lost socket's in-memory maps or
					// accept a new owner, response identity, or caller-selected price.
					_, err = ReconcileMarketingRealtimeResponse(user.Id+100000, session.funding.ID, terminal, at)
					require.Error(t, err)
					_, err = ReconcileMarketingRealtimeResponse(user.Id, session.funding.ID, []byte(`{"type":"response.done","response":{"id":"unknown-new-response","status":"completed","usage":{"total_tokens":0,"input_tokens":0,"output_tokens":0}}}`), at)
					require.Error(t, err)
					admin := model.User{Username: "maintenance-" + runTag, AffCode: "ma" + runTag[len(runTag)-8:], Status: common.UserStatusEnabled, Role: common.RoleRootUser}
					require.NoError(t, db.Create(&admin).Error)
					currency, err := MarketingDisplayMoney("1")
					require.NoError(t, err)
					command := MarketingMaintenanceRequest{Action: "reconcile", ActorID: admin.Id, UserID: user.Id, Reference: "rt-" + runTag, Evidence: "provider billing export", AuthorizationReference: "operator approval", WriterIsolationEvidence: "socket closed and writer isolated", Currency: currency.Currency, TerminalEvidence: "provider terminal", SourceEvidence: "original request projection", FundingID: session.funding.ID, RealtimeTerminal: terminal, ObservedAt: at.Unix()}
					// A failed audit insertion must roll back both recovered usage
					// and original-source accounting, even across coordinator calls.
					auditFailure := fmt.Errorf("audit storage unavailable")
					callback := "test_maintenance_audit_failure"
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "marketing_v2_maintenance_operations" {
							tx.AddError(auditFailure)
						}
					}))
					_, err = RunMarketingMaintenance(context.Background(), command, "local-maintenance:test", true)
					require.ErrorIs(t, err, auditFailure)
					require.NoError(t, db.Callback().Create().Remove(callback))
					var unchanged model.MarketingFunding
					require.NoError(t, db.First(&unchanged, session.funding.ID).Error)
					assert.Equal(t, "review", unchanged.State)
					assert.Equal(t, session.funding.Revision, unchanged.Revision)
					result, err := RunMarketingMaintenance(context.Background(), command, "local-maintenance:test", true)
					require.NoError(t, err)
					replay, err := RunMarketingMaintenance(context.Background(), command, "local-maintenance:test", true)
					require.NoError(t, err)
					assert.Equal(t, result, replay)
					changed := command
					changed.SourceEvidence = "different evidence"
					_, err = RunMarketingMaintenance(context.Background(), changed, "local-maintenance:test", true)
					require.ErrorIs(t, err, model.ErrMarketingConflict)
					require.NoError(t, db.Model(&admin).Update("status", common.UserStatusDisabled).Error)
					_, err = RunMarketingMaintenance(context.Background(), command, "local-maintenance:test", true)
					require.Error(t, err, "replay must recheck current authorization")
					recovered, err := ReconcileMarketingRealtimeResponse(user.Id, session.funding.ID, terminal, at)
					require.NoError(t, err)
					assert.Equal(t, "settled", recovered.State)
					assert.EqualValues(t, 30, recovered.SettledQuota)
					repeated, err := ReconcileMarketingRealtimeResponse(user.Id, session.funding.ID, terminal, at)
					require.NoError(t, err)
					assert.Equal(t, recovered.Revision, repeated.Revision)
					assert.Equal(t, recovered.SettledQuota, repeated.SettledQuota)
					session.funding = *recovered
				} else {
					require.NoError(t, coordinator.ServerEvent(terminal, at))
					require.NoError(t, coordinator.ServerEvent(terminal, at.Add(time.Millisecond)))
					forwardedCreate, err = coordinator.ProcessClientEvent([]byte(`{"type":"response.create"}`), at)
					require.NoError(t, err)
					require.NoError(t, coordinator.ServerEvent(marketingRealtimeFixtureEcho(t, []byte(`{"type":"response.created","response":{"id":"second"}}`), forwardedCreate), at))
					require.NoError(t, coordinator.ServerEvent(terminal, at.Add(time.Millisecond)))
					require.NoError(t, coordinator.ServerEvent([]byte(`{"type":"response.done","response":{"id":"second","status":"completed","usage":{"total_tokens":5,"input_tokens":5,"output_tokens":0,"input_token_details":{"text_tokens":5},"output_token_details":{"text_tokens":0}}}}`), at))
					require.NoError(t, coordinator.Close("client closed after final usage"))
					assert.Equal(t, "settled", session.funding.State)
					assert.EqualValues(t, 35, session.funding.SettledQuota)
					assert.Equal(t, 35, coordinator.Usage().TotalTokens)
				}
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 100-int(session.funding.SettledQuota-20), user.Quota)
				require.NoError(t, model.RecoverMarketingUsage(context.Background(), 100))
				var identities int64
				require.NoError(t, db.Model(&model.Log{}).Where("request_id = ?", info.RequestId).Distinct("usage_event_id").Count(&identities).Error)
				assert.EqualValues(t, 1, identities, "one logical connection usage entry")
			case "estimated_usage", "responses_estimated_usage", "estimated_canonical", "empty_usage", "fixed_without_usage", "fixed_estimated_branch", "nonstream_missing_terminal", "failed_terminal_with_done":
				require.NoError(t, session.BeginDispatch())
				usage := &dto.Usage{PromptTokens: 20, CompletionTokens: 20, TotalTokens: 40}
				fixed := false
				switch scenario {
				case "estimated_usage":
					common.SetContextKey(ctx, constant.ContextKeyEstimatedBillingUsage, true)
				case "responses_estimated_usage":
					info.IsStream = true
					info.StreamStatus = relaycommon.NewStreamStatus()
					info.SetEstimatePromptTokens(20)
					accumulator := NewResponsesUsageAccumulator(info)
					accumulator.Observe(&dto.ResponsesStreamResponse{Type: "response.output_text.delta", Delta: "hello"}, nil)
					accumulator.Observe(&dto.ResponsesStreamResponse{Type: "response.completed", Response: &dto.OpenAIResponsesResponse{}}, nil)
					usage = accumulator.Finish()
				case "estimated_canonical":
					usage.BillingUsage = dto.NewEstimatedGeminiChatBillingUsage(usage)
				case "empty_usage":
					usage = &dto.Usage{}
				case "fixed_without_usage":
					usage, fixed = nil, true
				case "nonstream_missing_terminal", "failed_terminal_with_done":
					fixed = true
					info.StreamStatus = relaycommon.NewStreamStatus()
					info.StreamStatus.RequireTerminal()
					if scenario == "failed_terminal_with_done" {
						info.StreamStatus.MarkFailed("provider_failed", "", 500)
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
					}
				case "fixed_estimated_branch":
					usage, fixed = nil, true
					info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{ExprString: `len < 100 ? tier("short", fixed(0.01)) : tier("long", p * 2)`}
				}
				require.True(t, RecordMarketingConsumption(ctx, info, model.RecordConsumeLogParams{ModelName: info.OriginModelName, Quota: 40}, usage, true, fixed))
				require.NoError(t, db.First(&token, token.Id).Error)
				if scenario == "fixed_without_usage" {
					assert.Equal(t, "settled", session.funding.State)
					assert.EqualValues(t, 40, session.funding.SettledQuota)
					assert.Equal(t, 60, token.RemainQuota)
				} else {
					assert.Equal(t, "review", session.funding.State)
					assert.False(t, session.funding.CostKnown)
					assert.Zero(t, session.funding.SettledQuota)
					assert.EqualValues(t, 30, session.funding.ReservedQuota)
					assert.Equal(t, 100, token.RemainQuota)
					var hold model.MarketingTokenHold
					require.NoError(t, db.Where("token_id = ?", token.Id).First(&hold).Error)
					assert.EqualValues(t, 30, hold.ReservedQuota)
				}
			case "gift_then_paid":
				require.NoError(t, session.BeginDispatch())
				require.NoError(t, session.Reserve(40))
				require.ErrorIs(t, session.Reserve(101), model.ErrMarketingInsufficient)
				require.NoError(t, session.Settle(40))
				require.NoError(t, session.Settle(40))
				assert.False(t, session.NeedsRefund())
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				assert.Equal(t, 80, user.Quota)
				assert.Equal(t, 60, token.RemainQuota)
			case "before_dispatch":
				session.Refund(ctx)
				session.Refund(ctx)
				assert.Equal(t, "settled", session.funding.State)
				assert.Zero(t, session.funding.SettledQuota)
				assert.Zero(t, session.funding.ReservedQuota)
			case "ambiguous_dispatch", "crash_without_projection":
				require.NoError(t, session.BeginDispatch())
				require.ErrorIs(t, session.BeginDispatch(), model.ErrMarketingConflict)
				if scenario == "crash_without_projection" {
					admin := model.User{Username: "crash-admin-" + runTag, AffCode: "ca" + runTag[len(runTag)-8:], Role: common.RoleRootUser, Status: common.UserStatusEnabled}
					require.NoError(t, db.Create(&admin).Error)
					amount, currency, err := MarketingWalletDisplayAmount(40)
					require.NoError(t, err)
					command := MarketingMaintenanceRequest{Action: "reconcile", ActorID: admin.Id, UserID: user.Id, Reference: "crash-" + runTag, Evidence: "provider final invoice", AuthorizationReference: "operator approval", WriterIsolationEvidence: "crashed process cannot resume", Currency: currency, TerminalEvidence: "provider terminal export", SourceEvidence: "original request identity from gateway audit", FundingID: session.funding.ID, Version: 1, FinalAmount: &amount, OriginalRequestID: "wrong-request"}
					_, err = RunMarketingMaintenance(context.Background(), command, "local-maintenance:test", true)
					require.ErrorIs(t, err, model.ErrMarketingConflict)
					command.OriginalRequestID = info.RequestId
					wire, err := common.Marshal(command)
					require.NoError(t, err)
					require.NoError(t, common.Unmarshal(wire, &command))
					require.Equal(t, "null", string(command.RealtimeTerminal), "the CLI decodes absent optional raw JSON as null")
					// The cache client can fail after CLI admission. A post-commit cache
					// failure must report committed funds, not invite a fresh debit.
					previousRedis, previousClient := common.RedisEnabled, common.RDB
					failedCache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
					require.NoError(t, failedCache.Close())
					common.RedisEnabled, common.RDB = true, failedCache
					t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
					result, err := RunMarketingMaintenance(context.Background(), command, "local-maintenance:test", true)
					require.NoError(t, err)
					assert.Equal(t, "settled", result["state"])
					assert.Equal(t, true, result["executed"])
					assert.Equal(t, true, result["cache_refresh_pending"])
					common.RedisEnabled, common.RDB = previousRedis, previousClient
					replay, err := RunMarketingMaintenance(context.Background(), command, "local-maintenance:test", true)
					require.NoError(t, err)
					assert.NotContains(t, replay, "cache_refresh_pending")
					delete(result, "cache_refresh_pending")
					assert.Equal(t, result, replay, "replay returns the original financial operation after cache recovery")
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 80, user.Quota)
					assert.Equal(t, 1, user.RequestCount)
					var projections []model.MarketingUsageProjection
					require.NoError(t, db.Where("funding_id = ?", session.funding.ID).Find(&projections).Error)
					require.Len(t, projections, 1)
					var record model.Log
					require.NoError(t, common.UnmarshalJsonStr(projections[0].Payload, &record))
					assert.Equal(t, info.RequestId, record.RequestId)
					assert.Equal(t, 40, record.Quota)
					assert.Contains(t, record.Other, "usage_quantity_unknown")
					return
				}
				session.Refund(ctx)
				assert.Equal(t, "review", session.funding.State)
				assert.EqualValues(t, 30, session.funding.ReservedQuota)
				assert.False(t, session.funding.CostKnown)
				actual := int64(40)
				require.NoError(t, session.SettleEvidence(&actual, true, "upstream_terminal", "recovered provider result", 2))
				assert.Equal(t, "settled", session.funding.State)
			case "text_terminal", "stream_interrupted", "log_outage", "log_retention", "logs_disabled", "export_disabled":
				require.NoError(t, session.BeginDispatch())
				if scenario == "stream_interrupted" {
					info.IsStream = true
					info.StreamStatus = relaycommon.NewStreamStatus()
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, nil)
				}
				if scenario == "log_outage" || scenario == "log_retention" {
					broken, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
					require.NoError(t, err)
					connection, err := broken.DB()
					require.NoError(t, err)
					require.NoError(t, connection.Close())
					model.LOG_DB = broken
				}
				if scenario == "export_disabled" {
					previous := common.DataExportEnabled
					common.DataExportEnabled = false
					t.Cleanup(func() { common.DataExportEnabled = previous })
				}
				if scenario == "logs_disabled" {
					previous := common.LogConsumeEnabled
					common.LogConsumeEnabled = false
					t.Cleanup(func() { common.LogConsumeEnabled = previous })
				}
				usage := &dto.Usage{PromptTokens: 20, CompletionTokens: 20, TotalTokens: 40}
				PostTextConsumeQuota(ctx, info, usage, nil)
				PostTextConsumeQuota(ctx, info, usage, nil)
				model.LOG_DB = db
				var projections []model.MarketingUsageProjection
				require.NoError(t, db.Where("funding_id = ?", session.funding.ID).Find(&projections).Error)
				require.Len(t, projections, 1)
				if scenario == "export_disabled" || scenario == "logs_disabled" {
					var contributions int64
					require.NoError(t, db.Model(&model.QuotaData{}).Where("funding_id = ?", session.funding.ID).Count(&contributions).Error)
					assert.Zero(t, contributions, "disabled reporting does not create dashboard data")
				}

				if scenario == "log_outage" {
					assert.Zero(t, projections[0].PublishedAt)
					require.True(t, model.HasPendingMarketingRecovery())
					require.NoError(t, model.RecoverMarketingUsage(context.Background(), 100))
				}
				if scenario == "log_retention" {
					_, err := model.DeleteOldLogBatch(context.Background(), now+1, 1000)
					require.NoError(t, err)
					require.NoError(t, model.RecoverMarketingUsage(context.Background(), 100))
					require.NoError(t, model.PublishMarketingUsage(projections[0]), "a stale delivery cannot resurrect a deleted request")
					rows, count, err := model.GetUserLogs(user.Id, model.LogFilter{}, 0, 10)
					require.NoError(t, err)
					assert.Empty(t, rows)
					assert.Zero(t, count)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 80, user.Quota)
					assert.Equal(t, 40, user.UsedQuota)
					return
				}
				if scenario == "logs_disabled" {
					assert.EqualValues(t, -1, projections[0].PublishedAt)
					common.LogConsumeEnabled = true
					require.NoError(t, model.RecoverMarketingUsage(context.Background(), 100))
					rows, count, err := model.GetUserLogs(user.Id, model.LogFilter{}, 0, 10)
					require.NoError(t, err)
					assert.Empty(t, rows)
					assert.Zero(t, count)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 80, user.Quota)
					assert.Equal(t, 40, user.UsedQuota)
					return
				}
				rows, total, err := model.GetUserLogs(user.Id, model.LogFilter{}, 0, 10)
				require.NoError(t, err)
				assert.EqualValues(t, 1, total)
				require.Len(t, rows, 1)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 1, user.RequestCount)
				if scenario == "stream_interrupted" {
					assert.Equal(t, "review", session.funding.State)
					assert.EqualValues(t, 30, session.funding.ReservedQuota)
					assert.Zero(t, rows[0].Quota)
					assert.Equal(t, 100, user.Quota)
				} else {
					assert.Equal(t, "settled", session.funding.State)
					assert.Equal(t, 40, rows[0].Quota)
					assert.Equal(t, 80, user.Quota)
					assert.Equal(t, 40, user.UsedQuota)
				}
			case "task_recovery", "task_timeout", "task_unknown_usage", "task_provider_failure":
				require.NoError(t, session.BeginDispatch())
				task := &model.Task{TaskID: "task-" + scenario + runTag, UserId: user.Id, Status: model.TaskStatusSubmitted, Quota: 30, SubmitTime: now}
				task.PrivateData.TokenId = token.Id
				task.PrivateData.NodeName = "original-node"
				task.PrivateData.BillingContext = &model.TaskBillingContext{PerCallBilling: true}
				require.True(t, AttachMarketingTask(ctx, info, task))
				require.NoError(t, task.Insert())
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 100, user.Quota, "submission holds funds without charging the estimate")
				assert.Zero(t, user.RequestCount)
				task.Status = model.TaskStatusSuccess
				result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}
				switch scenario {
				case "task_timeout":
					task.Status = model.TaskStatusFailure
					result = nil
				case "task_unknown_usage":
					result.BillingUnknown = true
				case "task_provider_failure":
					task.Status = model.TaskStatusFailure
					result.Status = model.TaskStatusFailure
				}
				require.NoError(t, PrepareMarketingTaskOutcome(task, result, nil, nil))
				won, err := task.UpdateWithStatus(model.TaskStatusSubmitted)
				require.NoError(t, err)
				require.True(t, won)
				require.True(t, model.HasPendingMarketingRecovery())
				// Simulate process death immediately after terminal CAS. A fresh
				// worker uses persisted facts, even after administrative price edits.
				task.PrivateData.BillingContext.PerCallBilling = false
				require.NoError(t, RecoverMarketingTasks(context.Background()))
				assert.False(t, model.HasPendingMarketingRecovery())
				require.NoError(t, RecoverMarketingTasks(context.Background()))
				require.NoError(t, FinalizeMarketingTask(task))
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				var funding model.MarketingFunding
				require.NoError(t, db.First(&funding, session.funding.ID).Error)
				rows, count, err := model.GetUserLogs(user.Id, model.LogFilter{}, 0, 10)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.EqualValues(t, 1, count)
				assert.Equal(t, 1, user.RequestCount)
				if scenario == "task_recovery" {
					assert.Equal(t, 90, user.Quota)
					assert.Equal(t, 70, token.RemainQuota)
					assert.Equal(t, 30, rows[0].Quota)
					assert.Equal(t, "settled", funding.State)
				} else {
					assert.Equal(t, 100, user.Quota)
					assert.Equal(t, 100, token.RemainQuota)
					assert.Zero(t, rows[0].Quota)
					if scenario == "task_provider_failure" {
						assert.Equal(t, "settled", funding.State)
						assert.Zero(t, funding.ReservedQuota)
					} else {
						assert.Equal(t, "review", funding.State)
						assert.EqualValues(t, 30, funding.ReservedQuota)
					}
				}
			case "mj_recovery", "mj_timeout", "mj_provider_failure":
				require.NoError(t, session.BeginDispatch())
				task := &model.Midjourney{UserId: user.Id, MjId: scenario + runTag, Code: 1, Action: "IMAGINE", Quota: 30, Status: "SUBMITTED", Progress: "0%"}
				require.NoError(t, AttachMarketingMidjourney(ctx, info, task, http.StatusOK))
				require.NoError(t, task.Insert())
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 100, user.Quota)
				assert.Zero(t, user.RequestCount)
				task.Status, task.Progress = "SUCCESS", "100%"
				if scenario != "mj_recovery" {
					task.Status = "FAILURE"
				}
				require.NoError(t, PrepareMarketingMidjourneyOutcome(task, scenario != "mj_timeout"))
				won, err := task.UpdateWithStatus("SUBMITTED")
				require.NoError(t, err)
				require.True(t, won)
				require.True(t, model.HasPendingMarketingMidjourney())
				// A new worker reads only the durable outcome, not this pointer.
				task.Quota = 999
				require.NoError(t, RecoverMarketingMidjourney(context.Background()))
				require.False(t, model.HasPendingMarketingMidjourney())
				require.NoError(t, FinalizeMarketingMidjourney(task))
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				var funding model.MarketingFunding
				require.NoError(t, db.First(&funding, session.funding.ID).Error)
				rows, count, err := model.GetUserLogs(user.Id, model.LogFilter{}, 0, 10)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.EqualValues(t, 1, count)
				assert.Equal(t, 1, user.RequestCount)
				if scenario == "mj_recovery" {
					assert.Equal(t, 90, user.Quota)
					assert.Equal(t, 70, token.RemainQuota)
					assert.Equal(t, 30, rows[0].Quota)
					assert.Equal(t, "settled", funding.State)
				} else {
					assert.Equal(t, 100, user.Quota)
					assert.Equal(t, 100, token.RemainQuota)
					assert.Zero(t, rows[0].Quota)
					if scenario == "mj_timeout" {
						assert.Equal(t, "review", funding.State)
						assert.EqualValues(t, 30, funding.ReservedQuota)
					} else {
						assert.Equal(t, "settled", funding.State)
						assert.Zero(t, funding.ReservedQuota)
					}
				}
			case "violation_fee":
				settings := model_setting.GetGrokSettings()
				previousEnabled, previousAmount := settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount
				settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount = true, 40/common.QuotaPerUnit
				t.Cleanup(func() {
					settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount = previousEnabled, previousAmount
				})
				require.NoError(t, session.BeginDispatch())
				apiErr := types.NewErrorWithStatusCode(fmt.Errorf("provider confirmed fee"), types.ErrorCodeViolationFeeGrokCSAM, http.StatusBadRequest)
				require.True(t, ChargeViolationFeeIfNeeded(ctx, info, apiErr))
				require.True(t, ChargeViolationFeeIfNeeded(ctx, info, apiErr))
				session.Refund(ctx)
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				assert.Equal(t, 80, user.Quota)
				assert.Equal(t, 40, user.UsedQuota)
				assert.Equal(t, 60, token.RemainQuota)
				assert.Equal(t, 1, user.RequestCount)
				rows, count, err := model.GetUserLogs(user.Id, model.LogFilter{}, 0, 10)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.EqualValues(t, 1, count)
				assert.Equal(t, 40, rows[0].Quota)
			case "revoked_before_dispatch":
				require.NoError(t, db.Model(&model.Token{}).Where("id = ?", token.Id).Update("status", common.TokenStatusDisabled).Error)
				require.ErrorIs(t, session.BeginDispatch(), model.ErrMarketingAccess)
				session.Refund(ctx)
				assert.Equal(t, "settled", session.funding.State)
				assert.Zero(t, session.funding.SettledQuota)
			}
		})
	}
}

func TestMarketingBillingMessagesRespectReaderLanguage(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, tc := range []struct {
		lang string
		err  error
		want string
	}{
		{"en", model.ErrMarketingInsufficient, "Available funds or the API token spending limit cannot cover this request."},
		{"zh-CN", fmt.Errorf("reserve: %w", model.ErrMarketingInsufficient), "可用金额或 API 令牌的消费限额不足，无法执行本次请求。"},
		{"en", model.ErrMarketingConflict, "This request has already been submitted or finalized. Do not resubmit it automatically."},
		{"zh-CN", model.ErrMarketingReview, "账户资金需要核对，请联系管理员。"},
		{"zh-TW", model.ErrMarketingAccess, "此帳號或 API 權杖目前無法發起新請求。"},
	} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		ctx.Request.Header.Set("Accept-Language", tc.lang)
		assert.Equal(t, tc.want, MarketingBillingMessage(ctx, tc.err))
		unexpected := MarketingBillingMessage(ctx, fmt.Errorf("database connection secret-password failed"))
		assert.NotContains(t, unexpected, "secret-password")
		assert.NotContains(t, unexpected, "common.database_error")
	}
}

func TestMarketingPricingEvidenceKeepsBillingScalarsWithoutRequestSecrets(t *testing.T) {
	info := &relaycommon.RelayInfo{OriginModelName: "image-model", ChannelMeta: &relaycommon.ChannelMeta{}, BillingRequestInput: &billingexpr.RequestInput{
		Body:    []byte(`{"n":2,"quality":"high","size":"1024x1024","prompt":"private-prompt-marker","image":"private-image-marker","parameters":{"n":3,"prompt":"nested-secret-marker"}}`),
		Headers: map[string]string{"Authorization": "Bearer private-key-marker", "Cookie": "private-cookie-marker", "Anthropic-Beta": "fast-mode"},
	}}
	encoded, err := marketingPricingEvidence(info)
	require.NoError(t, err)
	var evidence map[string]any
	require.NoError(t, common.UnmarshalJsonStr(encoded, &evidence))
	request := evidence["request_pricing"].(map[string]any)
	assert.Equal(t, false, request["complete_request"])
	parameters := request["parameters"].(map[string]any)
	assert.Equal(t, float64(2), parameters["n"])
	assert.Equal(t, float64(3), parameters["parameters.n"])
	assert.Equal(t, "high", parameters["quality"])
	for _, secret := range []string{"private-prompt-marker", "private-image-marker", "nested-secret-marker", "private-key-marker", "private-cookie-marker"} {
		assert.NotContains(t, encoded, secret)
	}
	assert.Contains(t, encoded, "fast-mode")
}

// The fixture echoes the metadata actually sent by the gateway. It does not
// assume that the next response belongs to a pending client create.
func marketingRealtimeFixtureEcho(t *testing.T, response, create []byte) []byte {
	t.Helper()
	var sent, received map[string]any
	require.NoError(t, common.Unmarshal(create, &sent))
	require.NoError(t, common.Unmarshal(response, &received))
	request, ok := sent["response"].(map[string]any)
	require.True(t, ok)
	received["response"].(map[string]any)["metadata"] = request["metadata"]
	encoded, err := common.Marshal(received)
	require.NoError(t, err)
	return encoded
}
