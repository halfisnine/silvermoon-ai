package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMarketingUserHTTP(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	models := append(marketingRegistrationFixtureModels(), &model.Redemption{}, &model.TopUp{}, &model.MarketingCheckinDay{}, &model.MarketingCheckinClaim{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.Token{}, &model.MarketingRealtimeRound{})
	require.NoError(t, model.DB.AutoMigrate(models...))
	require.NoError(t, model.DB.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
	units, rate, general := common.QuotaPerUnit, operation_setting.USDExchangeRate, *operation_setting.GetGeneralSetting()
	common.QuotaPerUnit = 50000
	operation_setting.USDExchangeRate = 3
	operation_setting.GetGeneralSetting().QuotaDisplayType = "CNY"
	t.Cleanup(func() {
		common.QuotaPerUnit = units
		operation_setting.USDExchangeRate = rate
		*operation_setting.GetGeneralSetting() = general
	})
	admin := model.User{Username: "user-api-admin", AffCode: "ua-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	other := model.User{Username: "user-api-other", AffCode: "ua-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&admin).Error)
	require.NoError(t, model.DB.Create(&other).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "user-marketing")
	require.NoError(t, err)
	otherBundle, err := service.CreateLoginSession(other.Id, "password", "127.0.0.1", "user-marketing-other")
	require.NoError(t, err)
	router := gin.New()
	g := router.Group("/api/marketing/user", middleware.UserAuth())
	g.GET("/:resource", MarketingUserRead)
	g.GET("/:resource/:id", MarketingUserRead)
	g.GET("/rewards/:kind/:id", MarketingUserRead)
	g.GET("/draws/by-request/:operation_key", MarketingUserRead)
	g.POST("/pools/:id/draw", MarketingUserDraw)
	request := func(method, path string, body any, token string) *httptest.ResponseRecorder {
		data, err := common.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, "/api/marketing/user"+path, strings.NewReader(string(data)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://untrusted.example")
		req.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: bundle.RefreshToken})
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, v any) {
		require.Equal(t, 200, w.Code, w.Body.String())
		var e struct {
			Success bool
			Data    common.RawMessage
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &e))
		require.True(t, e.Success, w.Body.String())
		require.NoError(t, common.Unmarshal(e.Data, v))
	}
	now := time.Now().Unix()
	money := model.MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: "50000", CurrencyPerUnit: "3", Currency: "CNY"}
	var pool *model.MarketingLotteryPool
	var active *model.MarketingActivity
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		pool, err = model.CreateMarketingLotteryPool(tx, admin.Id, "user-http-pool", model.MarketingLotteryPoolSpec{Name: "Public pool", Style: "wheel", StartsAt: now - 100, EndsAt: now + 3600, Capacity: 5, Prizes: []model.MarketingLotteryPrizeSpec{{Name: "Gift", Weight: 1, Stock: 5, Gifts: []model.MarketingLotteryGiftSpec{{Money: money, ValidSeconds: 3600}, {Money: money}}}, {Name: "Exhausted", Weight: 1, Stock: 0, Gifts: []model.MarketingLotteryGiftSpec{{Money: money}}}}}, now-100)
		if err != nil {
			return err
		}
		for i, kind := range []string{"registration", "recharge", "checkin", "invitation"} {
			condition, frequency := "registration", "once"
			spec := model.MarketingActivitySpec{Name: kind, Kind: kind, Published: true, StartsAt: now - 100, EndsAt: now + 1800, Rewards: []model.MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", Money: money, ValidSeconds: 3600}, {Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "direct"}}}
			switch kind {
			case "recharge":
				condition, frequency = "single_payment", "every"
				spec.ThresholdCents = 100
			case "checkin":
				condition, frequency = "checkin", "every"
				spec.Checkin = &model.MarketingActivityCheckinSpec{Condition: "none"}
			case "invitation":
				condition = "friend_registration"
			}
			spec.Condition, spec.Frequency = condition, frequency
			a, e := model.CreateMarketingActivity(tx, admin.Id, fmt.Sprintf("user-http-create-%d", i), spec, now-100)
			if e != nil {
				return e
			}
			if _, e = model.ActivateMarketingActivity(tx, admin.Id, a.ID, fmt.Sprintf("user-http-enable-%d", i), now-100); e != nil {
				return e
			}
			if i == 0 {
				active = a
			}
		}
		_, err = model.IssueMarketingLotteryChances(tx, user.Id, pool.ID, "user-http-chance", 3, now)
		return err
	}))

	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.CaptureMarketingRegistrationFact(tx, user.Id, "user-registration", user.CreatedAt)
		return err
	}))
	_, err = service.ProcessMarketingActivityFacts(context.Background(), user.Id)
	require.NoError(t, err)
	poolPath := fmt.Sprintf("/pools/%d", pool.ID)
	assert.Equal(t, http.StatusUnauthorized, request("POST", poolPath+"/draw", map[string]string{"operation_key": "one"}, "").Code)
	var page struct {
		Items []service.MarketingUserActivity
		Total int64
	}
	w := request("GET", "/activities", nil, bundle.AccessToken)
	decode(w, &page)
	assert.EqualValues(t, 4, page.Total)
	for _, hidden := range []string{"priority", "exclusive_group", "money_revision", "QuotaPerUnit", "quota"} {
		assert.NotContains(t, w.Body.String(), hidden)
	}
	var p service.MarketingUserPool
	decode(request("GET", poolPath, nil, bundle.AccessToken), &p)
	assert.EqualValues(t, 4, p.AvailableChances)
	assert.True(t, p.CanDraw)
	assert.Equal(t, "sold_out", p.Prizes[1].State)
	assert.False(t, p.Prizes[1].Available)
	var draw service.MarketingUserDraw
	decode(request("POST", poolPath+"/draw", map[string]string{"operation_key": "one"}, bundle.AccessToken), &draw)
	assert.Equal(t, "completed", draw.State)
	require.Len(t, draw.Rewards, 2)
	assert.NotEqual(t, *draw.Rewards[0].ExpiresAt, *draw.Rewards[1].ExpiresAt)
	var replay service.MarketingUserDraw
	decode(request("GET", "/draws/by-request/one", nil, bundle.AccessToken), &replay)
	assert.Equal(t, draw.ID, replay.ID)
	assert.Equal(t, http.StatusNotFound, request("GET", fmt.Sprintf("/draws/%d", draw.ID), nil, otherBundle.AccessToken).Code)
	require.NoError(t, model.DB.Model(pool).Update("paused", true).Error)
	decode(request("POST", poolPath+"/draw", map[string]string{"operation_key": "one"}, bundle.AccessToken), &replay)
	assert.Equal(t, draw.ID, replay.ID)
	assert.Equal(t, http.StatusForbidden, request("POST", poolPath+"/draw", map[string]string{"operation_key": "two"}, bundle.AccessToken).Code)
	decode(request("GET", poolPath, nil, bundle.AccessToken), &p)
	assert.EqualValues(t, 3, p.AvailableChances)
	assert.False(t, p.CanDraw)

	var rewardPage struct {
		Items []service.MarketingUserRewardRecord
		Total int64
	}
	rewardResponse := request("GET", "/rewards?kind=activity&activity_kind=registration", nil, bundle.AccessToken)
	decode(rewardResponse, &rewardPage)
	require.EqualValues(t, 2, rewardPage.Total)
	for _, hidden := range []string{"username", "user_id", "sales_evidence", "sales_batch", "payment_reference", "quota", "ProbabilitySeed"} {
		assert.NotContains(t, rewardResponse.Body.String(), hidden)
	}
	assert.Equal(t, http.StatusNotFound, request("GET", fmt.Sprintf("/rewards/activity/%d", rewardPage.Items[0].ID), nil, otherBundle.AccessToken).Code)
	var lotteryPage struct {
		Items []service.MarketingUserRewardRecord
		Total int64
	}
	decode(request("GET", "/rewards?kind=lottery", nil, bundle.AccessToken), &lotteryPage)
	require.EqualValues(t, 1, lotteryPage.Total)
	// Publication and start/end boundaries apply to cards, not existing assets.
	activityPath := fmt.Sprintf("/activities/%d", active.ID)
	require.NoError(t, model.DB.Model(active).Update("published", false).Error)
	assert.Equal(t, http.StatusNotFound, request("GET", activityPath, nil, bundle.AccessToken).Code)
	require.NoError(t, model.DB.Model(active).Updates(map[string]any{"published": true, "enabled_at": 0}).Error)
	assert.Equal(t, http.StatusNotFound, request("GET", activityPath, nil, bundle.AccessToken).Code)
	require.NoError(t, model.DB.Model(active).Updates(map[string]any{"enabled_at": now - 60, "starts_at": now + 600, "ends_at": now + 3600}).Error)
	decode(request("GET", "/activities?state=scheduled", nil, bundle.AccessToken), &page)
	require.EqualValues(t, 1, page.Total)
	assert.Equal(t, "scheduled", page.Items[0].State)
	require.NoError(t, model.DB.Model(active).Updates(map[string]any{"starts_at": now - 80*3600, "ends_at": now - 71*3600}).Error)
	decode(request("GET", "/activities?state=ended", nil, bundle.AccessToken), &page)
	require.EqualValues(t, 1, page.Total)
	assert.Equal(t, "ended", page.Items[0].State)
	// Center expiry is a visibility rule only. Owner opportunities remain attached
	// to the independent original pool after every source disappears.
	require.NoError(t, model.DB.Model(&model.MarketingActivity{}).Where("id > ?", 0).Updates(map[string]any{"ends_at": now - 72*3600}).Error)
	decode(request("GET", "/activities", nil, bundle.AccessToken), &page)
	assert.Zero(t, page.Total)
	decode(request("GET", poolPath, nil, bundle.AccessToken), &p)
	assert.EqualValues(t, 3, p.AvailableChances)
	assert.Equal(t, http.StatusNotFound, request("GET", poolPath, nil, otherBundle.AccessToken).Code)
	// Permanent and expiring gift histories preserve their recorded conversion.
	var gifts struct {
		Items []service.MarketingUserGift
		Total int64
	}
	decode(request("GET", "/gifts", nil, bundle.AccessToken), &gifts)
	require.Len(t, gifts.Items, 3)
	before := gifts.Items[0].GrantedAmount
	operation_setting.USDExchangeRate = 9
	decode(request("GET", "/gifts", nil, bundle.AccessToken), &gifts)
	assert.Equal(t, before, gifts.Items[0].GrantedAmount)
	assert.Equal(t, "CNY", gifts.Items[0].Currency)
	// Both ratio modes retain their actual awarded face, not the payment face or
	// the unit conversion evidence. Actual cash differs from credited face here.
	for _, tc := range []struct{ mode, kind, want string }{{"paid_ratio", "recharge", "3"}, {"cash_ratio", "invitation", "6"}} {
		var ratioGift *model.MarketingGift
		require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
			payment, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "history-" + tc.mode, PaidCents: 6000, PostedAt: now, Money: model.MarketingMoneySnapshot{Amount: "30", Currency: "CNY", QuotaPerUnit: "50000", CurrencyPerUnit: "3"}})
			if err != nil {
				return err
			}
			amount, err := model.CalculateMarketingRewardAmount(tc.mode, "0.1", model.MarketingMoneySnapshot{Currency: "CNY", QuotaPerUnit: "50000", CurrencyPerUnit: "3"}, payment)
			if err != nil {
				return err
			}
			ratioGift, err = model.GrantMarketingRewardGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: tc.kind, Reference: "history-gift-" + tc.mode, GrantedAt: now}, amount)
			return err
		}))
		var history service.MarketingUserGift
		decode(request("GET", fmt.Sprintf("/gifts/%d", ratioGift.ID), nil, bundle.AccessToken), &history)
		assert.Equal(t, tc.want, history.GrantedAmount)
		assert.Equal(t, tc.want, history.RemainingAmount)
		assert.Equal(t, "0", history.ConsumedAmount)
		assert.Equal(t, "Activity reward", history.SourceNameKey)
	}
	assert.Equal(t, http.StatusNotFound, request("GET", fmt.Sprintf("/gifts/%d", gifts.Items[0].ID), nil, otherBundle.AccessToken).Code)
	assert.Equal(t, http.StatusBadRequest, request("GET", fmt.Sprintf("/gifts?user_id=%d", other.Id), nil, bundle.AccessToken).Code)
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Model(&other).Updates(map[string]any{"inviter_id": user.Id, "created_at": now}).Error; err != nil {
			return err
		}
		_, err := model.RecordMarketingInvitation(tx, other.Id, user.Id, "user-http-invite", now)
		return err
	}))
	var invitation struct {
		InvitationPath string `json:"invitation_path"`
		FriendCount    int64  `json:"friend_count"`
	}
	decode(request("GET", "/invitation", nil, bundle.AccessToken), &invitation)
	assert.EqualValues(t, 1, invitation.FriendCount)
	assert.Contains(t, invitation.InvitationPath, "aff=")

	// Reservation is a subset of RemainingQuota, not an additional balance.
	// A historical round retains only its committed hold after expiry; settlement
	// moves actual spend from that remainder, never from the expiration column.
	heldUser := model.User{Username: "held-gift-user", AffCode: "held-gift-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&heldUser).Error)
	heldBundle, err := service.CreateLoginSession(heldUser.Id, "password", "127.0.0.1", "held-gift")
	require.NoError(t, err)
	var heldGift *model.MarketingGift
	var funding *model.MarketingFunding
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		if err := tx.Create(&model.MarketingAccount{UserID: heldUser.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone}).Error; err != nil {
			return err
		}
		heldGift, err = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: heldUser.Id, Kind: "registration", Reference: "held-user-gift", GrantedAt: now - 100, ExpiresAt: now - 10, Money: model.MarketingMoneySnapshot{Amount: "3", Currency: "CNY", QuotaPerUnit: "100", CurrencyPerUnit: "1"}})
		if err != nil {
			return err
		}
		funding, err = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{UserID: heldUser.Id, Playground: true, RequestReference: "held-before-expiry", QualifiedAt: now - 20, Quota: 100, PricingEvidence: "fixed:test"})
		return err
	}))
	var held service.MarketingUserGift
	decode(request("GET", fmt.Sprintf("/gifts/%d", heldGift.ID), nil, heldBundle.AccessToken), &held)
	assert.Equal(t, "3", held.GrantedAmount)
	assert.Equal(t, "1", held.RemainingAmount)
	assert.Equal(t, "2", held.ExpiredAmount)
	assert.Equal(t, "0", held.ConsumedAmount)
	actual := int64(100)
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: heldUser.Id, FundingID: funding.ID, Version: 1, ActualQuota: &actual, Final: true, EvidenceKind: "upstream_terminal", Evidence: "actual:100"})
		return err
	}))
	decode(request("GET", fmt.Sprintf("/gifts/%d", heldGift.ID), nil, heldBundle.AccessToken), &held)
	assert.Equal(t, "0", held.RemainingAmount)
	assert.Equal(t, "1", held.ConsumedAmount)
	assert.Equal(t, "2", held.ExpiredAmount)
	var storedGift model.MarketingGift
	require.NoError(t, model.DB.First(&storedGift, heldGift.ID).Error)
	assert.EqualValues(t, 0, storedGift.ReservedQuota)

	scoped, _ := createScopedAccessToken(t, user.Id, now+3600, "wallet:read", "wallet:write")
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, request("GET", "/gifts", nil, scoped).Code)
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, request("POST", poolPath+"/draw", map[string]string{"operation_key": "pat"}, scoped).Code)
	// A committed combination can be partly delivered. Replaying recovers the
	// original result; the second component failure cannot reroll or use a chance.
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "resume-user-http", "resume", 0, 0, time.Now().Unix())
		return err
	}))
	inserts := 0
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("test:user-award-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "marketing_v2_gifts" {
			inserts++
			if inserts == 2 {
				tx.AddError(errors.New("injected component failure"))
			}
		}
	}))
	response := request("POST", poolPath+"/draw", map[string]string{"operation_key": "partial"}, bundle.AccessToken)
	require.NoError(t, model.DB.Callback().Create().Remove("test:user-award-failure"))
	var partial service.MarketingUserDraw
	decode(response, &partial)
	assert.Equal(t, "pending", partial.State)
	require.Len(t, partial.Rewards, 2)
	assert.Equal(t, "credited", partial.Rewards[0].State)
	assert.Equal(t, "pending", partial.Rewards[1].State)
	assert.Nil(t, partial.Rewards[1].ExpiresAt)
	decode(request("POST", poolPath+"/draw", map[string]string{"operation_key": "partial"}, bundle.AccessToken), &replay)
	assert.Equal(t, partial.ID, replay.ID)
	assert.Equal(t, "completed", replay.State)
	decode(request("GET", poolPath, nil, bundle.AccessToken), &p)
	assert.EqualValues(t, 2, p.AvailableChances)
	// An expired opportunity remains expired after extending the original pool.
	var expiredChance model.MarketingLotteryChance
	require.NoError(t, model.DB.Where("user_id = ? AND pool_id = ? AND remaining > ?", user.Id, pool.ID, 0).First(&expiredChance).Error)
	require.NoError(t, model.DB.Model(&expiredChance).Update("expires_at", now-1).Error)
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "user-http-expired-extend", "extend", 0, now+5400, time.Now().Unix())
		return err
	}))
	decode(request("GET", poolPath, nil, bundle.AccessToken), &p)
	assert.Equal(t, int64(2)-expiredChance.Remaining, p.AvailableChances)
	for _, chance := range p.Chances {
		if chance.ID == expiredChance.ID {
			assert.Equal(t, "expired", chance.State)
			assert.Zero(t, chance.Remaining)
			assert.Equal(t, now-1, chance.ExpiresAt)
		}
	}
	// Clearing and a subsequent extension cannot resurrect the old chances.
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		return model.ClearMarketingLotteryChances(tx, admin.Id, user.Id, "user-http-clear", time.Now().Unix())
	}))
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "user-http-extend", "extend", 0, now+7200, time.Now().Unix())
		return err
	}))
	decode(request("GET", poolPath, nil, bundle.AccessToken), &p)
	assert.Zero(t, p.AvailableChances)
	assert.False(t, p.CanDraw)
	for _, chance := range p.Chances {
		assert.Equal(t, "cleared", chance.State)
		assert.Zero(t, chance.Remaining)
	}
	_, err = model.RevokeAllUserSessions(other.Id, "test-revocation")
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, request("GET", "/gifts", nil, otherBundle.AccessToken).Code)
	require.NoError(t, model.DB.Model(user).Update("status", common.UserStatusDisabled).Error)
	assert.Contains(t, []int{http.StatusForbidden, http.StatusUnauthorized}, request("GET", "/gifts", nil, bundle.AccessToken).Code)
	assert.Contains(t, []int{http.StatusForbidden, http.StatusUnauthorized}, request("POST", poolPath+"/draw", map[string]string{"operation_key": "one"}, bundle.AccessToken).Code)
}
