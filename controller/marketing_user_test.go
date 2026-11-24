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
				spec.Rewards[1].Count = 2
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

	// Invitation summaries belong to the inviter and correlate through the
	// friend's durable event, rather than current activity visibility.
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Model(&model.MarketingActivity{}).Where("kind = ?", "invitation").Update("ends_at", now+1800).Error; err != nil {
			return err
		}
		_, err := model.CaptureMarketingRegistrationFact(tx, other.Id, "invited-friend-register", now)
		return err
	}))
	_, err = service.ProcessMarketingActivityFacts(context.Background(), other.Id)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.MarketingActivity{}).Where("kind = ?", "invitation").Updates(map[string]any{"ends_at": now - 72*3600, "published": false}).Error)
	var friendRewards struct {
		Friends struct {
			Items []struct {
				Username string
				Rewards  struct{ Granted, Pending, Other int64 }
			}
			Total int64
		}
	}
	invitationResponse := request("GET", "/invitation", nil, bundle.AccessToken)
	decode(invitationResponse, &friendRewards)
	require.Len(t, friendRewards.Friends.Items, 1)
	assert.Equal(t, other.Username, friendRewards.Friends.Items[0].Username)
	assert.EqualValues(t, 1, friendRewards.Friends.Items[0].Rewards.Granted)
	assert.EqualValues(t, 0, friendRewards.Friends.Items[0].Rewards.Pending)
	assert.EqualValues(t, 1, friendRewards.Friends.Items[0].Rewards.Other)
	filtered := "/rewards?kind=activity&activity_kind=invitation&friend_username=" + other.Username
	invitationRewardResponse := request("GET", filtered, nil, bundle.AccessToken)
	decode(invitationRewardResponse, &rewardPage)
	require.EqualValues(t, 2, rewardPage.Total)
	for _, item := range rewardPage.Items {
		assert.Equal(t, "invitation", item.SourceName)
		assert.Equal(t, other.Username, item.Source["friend_username"])
	}
	for _, hidden := range []string{"paid_cny", "payment_reference", "sales_evidence", "quota", "user_id"} {
		assert.NotContains(t, invitationRewardResponse.Body.String(), hidden)
		assert.NotContains(t, invitationResponse.Body.String(), hidden)
	}
	decode(request("GET", filtered, nil, otherBundle.AccessToken), &rewardPage)
	assert.Zero(t, rewardPage.Total)
	decode(request("GET", "/rewards?kind=activity&activity_kind=invitation&friend_username="+user.Username, nil, bundle.AccessToken), &rewardPage)
	assert.Zero(t, rewardPage.Total)
	assert.Equal(t, http.StatusBadRequest, request("GET", "/rewards?kind=lottery&friend_username="+other.Username, nil, bundle.AccessToken).Code)
	assert.Equal(t, http.StatusBadRequest, request("GET", "/rewards?kind=activity&activity_kind=invitation&friend_username="+strings.Repeat("x", 129), nil, bundle.AccessToken).Code)
	var invitationGift model.MarketingActivityReward
	require.NoError(t, model.DB.Where("user_id = ? AND kind = ? AND state = ?", user.Id, "gift", "credited").Order("id DESC").First(&invitationGift).Error)
	require.NoError(t, model.DB.Model(&invitationGift).Update("state", "pending").Error)
	decode(request("GET", "/invitation", nil, bundle.AccessToken), &friendRewards)
	assert.EqualValues(t, 0, friendRewards.Friends.Items[0].Rewards.Granted)
	assert.EqualValues(t, 1, friendRewards.Friends.Items[0].Rewards.Pending)
	require.NoError(t, model.DB.Model(&invitationGift).Update("state", "credited").Error)
	quietFriend := model.User{Username: "quiet-invited-friend", AffCode: "quiet-invited-friend", CreatedAt: now, InviterId: user.Id, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&quietFriend).Error)
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.RecordMarketingInvitation(tx, quietFriend.Id, user.Id, "quiet-friend", now)
		return err
	}))
	decode(request("GET", "/invitation?page=1&page_size=1", nil, bundle.AccessToken), &friendRewards)
	require.Len(t, friendRewards.Friends.Items, 1)
	assert.EqualValues(t, 2, friendRewards.Friends.Total)
	assert.Equal(t, quietFriend.Username, friendRewards.Friends.Items[0].Username)
	assert.Zero(t, friendRewards.Friends.Items[0].Rewards.Granted)
	assert.Zero(t, friendRewards.Friends.Items[0].Rewards.Pending)
	assert.Zero(t, friendRewards.Friends.Items[0].Rewards.Other)
	decode(request("GET", "/invitation?page=2&page_size=1", nil, bundle.AccessToken), &friendRewards)
	require.Len(t, friendRewards.Friends.Items, 1)
	assert.Equal(t, other.Username, friendRewards.Friends.Items[0].Username)
	assert.EqualValues(t, 1, friendRewards.Friends.Items[0].Rewards.Granted)

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

// The console reads the same primary accounting facts after independent usage
// logs have been removed. Display IDs must never become funding ownership IDs.
func TestMarketingAccountingHTTP(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	models := append(marketingRegistrationFixtureModels(), &model.Redemption{}, &model.TopUp{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.Token{}, &model.MarketingUsageProjection{}, &model.MarketingRealtimeRound{}, &model.MarketingMaintenanceOperation{})
	require.NoError(t, model.DB.AutoMigrate(models...))
	require.NoError(t, model.DB.Create(&model.MarketingFactSequence{ID: 1}).Error)
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
	units, rate, general := common.QuotaPerUnit, operation_setting.USDExchangeRate, *operation_setting.GetGeneralSetting()
	common.QuotaPerUnit, operation_setting.USDExchangeRate = 50000, 3
	operation_setting.GetGeneralSetting().QuotaDisplayType = "CNY"
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate = units, rate
		*operation_setting.GetGeneralSetting() = general
	})
	admin := model.User{Username: "accounting-admin", AffCode: "accounting-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	other := model.User{Username: "accounting-other", AffCode: "accounting-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&admin).Error)
	require.NoError(t, model.DB.Create(&other).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "accounting")
	require.NoError(t, err)
	adminBundle, err := service.CreateLoginSession(admin.Id, "password", "127.0.0.1", "accounting")
	require.NoError(t, err)
	otherBundle, err := service.CreateLoginSession(other.Id, "password", "127.0.0.1", "accounting")
	require.NoError(t, err)
	router := gin.New()
	ug := router.Group("/api/marketing/user", middleware.UserAuth())
	ug.GET("/:resource", MarketingUserRead)
	ug.GET("/:resource/:id", MarketingUserRead)
	ag := router.Group("/api/marketing/admin", middleware.AdminAuth())
	ag.GET("/:resource", MarketingAdminRead)
	ag.GET("/:resource/:id", MarketingAdminRead)
	router.GET("/api/log/self", middleware.UserAuth(), GetUserLogs)
	request := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
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
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var envelope struct {
			Success bool
			Data    common.RawMessage
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope))
		require.True(t, envelope.Success, w.Body.String())
		require.NoError(t, common.Unmarshal(envelope.Data, v))
	}
	now := time.Now().Unix()
	money := model.MarketingMoneySnapshot{Amount: "6", QuotaPerUnit: "50000", CurrencyPerUnit: "3", Currency: "CNY"}
	var funding *model.MarketingFunding
	var payment *model.MarketingPayment
	var gift *model.MarketingGift
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "new accounting fixture user"}).Error; err != nil {
			return err
		}
		var err error
		payment, err = model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "accounting-order", PaidCents: 600, PostedAt: now - 100, Money: money})
		if err != nil {
			return err
		}
		if err := tx.Create(&model.TopUp{UserId: user.Id, TradeNo: "accounting-order", PaymentID: payment.ID, PaymentProvider: model.PaymentProviderEpay, Status: common.TopUpStatusSuccess}).Error; err != nil {
			return err
		}
		giftMoney := money
		giftMoney.Amount = "3"
		gift, err = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: "accounting-gift", GrantedAt: now - 90, ExpiresAt: now + 3600, Money: giftMoney})
		if err != nil {
			return err
		}
		pricing, err := common.Marshal(map[string]any{"model": "accounting-model", "display_money": money, "private_field": "must-not-return"})
		if err != nil {
			return err
		}
		funding, err = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{UserID: user.Id, Playground: true, RequestReference: "accounting-request", QualifiedAt: now - 60, Quota: 75000, PricingEvidence: string(pricing)})
		return err
	}))
	var wallet struct {
		Paid, Gift service.MarketingAccountingMoney
	}
	decode(request("/api/marketing/user/wallet", bundle.AccessToken), &wallet)
	assert.Equal(t, "6", wallet.Paid.Amount, "reservation is not consumption")
	assert.Equal(t, "3", wallet.Gift.Amount)
	actual := int64(75000)
	require.NoError(t, model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		funding, err = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 1, ActualQuota: &actual, Final: true, EvidenceKind: "upstream_terminal", Evidence: "original terminal; secret-not-for-client"})
		if err != nil {
			return err
		}
		log := model.Log{UserId: user.Id, RequestId: "accounting-request", UsageEventID: funding.RequestKey, UsageRevision: funding.Revision, Type: model.LogTypeConsume, Quota: 75000, CreatedAt: now - 60, ModelName: "accounting-model"}
		payload, err := common.Marshal(log)
		if err != nil {
			return err
		}
		return tx.Create(&model.MarketingUsageProjection{FundingID: funding.ID, Version: funding.Revision, Payload: string(payload), CreatedAt: now - 60}).Error
	}))
	var view service.MarketingAccountingRequest
	path := fmt.Sprintf("/api/marketing/user/accounting/%d", funding.ID)
	response := request(path, bundle.AccessToken)
	decode(response, &view)
	require.NotNil(t, view.Total)
	require.NotNil(t, view.Paid)
	require.NotNil(t, view.Gift)
	assert.Equal(t, "4.5", view.Total.Amount)
	assert.Equal(t, "1.5", view.Paid.Amount)
	assert.Equal(t, "3", view.Gift.Amount)
	require.Len(t, view.GiftSources, 1)
	assert.Equal(t, gift.ID, view.GiftSources[0].GiftID)
	assert.Equal(t, "settled", view.State)
	assert.True(t, view.CostKnown)
	assert.True(t, view.FinalObserved)
	for _, secret := range []string{"must-not-return", "secret-not-for-client", "QuotaPerUnit", "quota", "PricingEvidence", "UsageEvidence"} {
		assert.NotContains(t, response.Body.String(), secret)
	}
	assert.Equal(t, http.StatusNotFound, request(path, otherBundle.AccessToken).Code)
	assert.Equal(t, http.StatusUnauthorized, request(path, "").Code)
	assert.Equal(t, http.StatusBadRequest, request("/api/marketing/user/wallet?user_id=99", bundle.AccessToken).Code)
	assert.Equal(t, http.StatusForbidden, request("/api/marketing/admin/accounting", bundle.AccessToken).Code)
	decode(request("/api/marketing/user/accounting?request_id=accounting-request", bundle.AccessToken), &view)
	assert.Equal(t, funding.ID, view.ID)
	// A later configuration change cannot rewrite historical request or payment amounts.
	operation_setting.USDExchangeRate = 9
	common.QuotaPerUnit = 500000
	decode(request(path, bundle.AccessToken), &view)
	assert.Equal(t, "4.5", view.Total.Amount)
	var monthly struct {
		Amounts                []service.MarketingAccountingMoney
		ConversionUnknownCount int64 `json:"conversion_unknown_count"`
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	month := time.Now().In(location).Format("2006-01")
	decode(request("/api/marketing/user/consumption?month="+month, bundle.AccessToken), &monthly)
	require.Len(t, monthly.Amounts, 1)
	assert.Equal(t, "1.5", monthly.Amounts[0].Amount)
	assert.Zero(t, monthly.ConversionUnknownCount)
	assert.Equal(t, http.StatusBadRequest, request("/api/marketing/user/consumption?month=invalid", bundle.AccessToken).Code)
	// Default SQLite may have a single shared main/log pool. The export must
	// finish its cursor before acquiring the owner's accounting transaction.
	t.Run("same pool export retains one call and precise metadata", func(t *testing.T) {
		originalLogDB := model.LOG_DB
		model.LOG_DB = model.DB
		defer func() { model.LOG_DB = originalLogDB }()
		require.NoError(t, model.DB.AutoMigrate(&model.Log{}))
		pool, err := model.DB.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		defer pool.SetMaxOpenConns(0)
		for _, revision := range []int64{1, 1} {
			row := model.Log{UserId: user.Id, Type: model.LogTypeConsume, RequestId: "accounting-request", UsageEventID: funding.RequestKey, UsageRevision: revision, Quota: 75000, CreatedAt: now - 60, Other: `{"precise":9007199254740993,"nested":{"value":9007199254740993}}`}
			require.NoError(t, model.DB.Create(&row).Error)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var exported []*model.Log
		require.NoError(t, service.StreamAccountingLogExport(ctx, false, user.Id, common.RoleCommonUser, model.LogFilter{}, func(row *model.Log) error {
			exported = append(exported, row)
			return nil
		}))
		require.Len(t, exported, 1)
		assert.Contains(t, exported[0].Other, `9007199254740993`)
		var metadata struct {
			Accounting service.MarketingAccountingRequest
		}
		require.NoError(t, common.UnmarshalJsonStr(exported[0].Other, &metadata))
		require.NotNil(t, metadata.Accounting.Total)
		assert.Equal(t, "4.5", metadata.Accounting.Total.Amount)
		assert.Equal(t, "1.5", metadata.Accounting.Paid.Amount)
	})
	var adminView service.MarketingAccountingRequest
	decode(request(fmt.Sprintf("/api/marketing/admin/accounting/%d", funding.ID), adminBundle.AccessToken), &adminView)
	assert.Equal(t, view.Total, adminView.Total)
	var paymentView map[string]any
	paymentResponse := request(fmt.Sprintf("/api/marketing/admin/payments/%d", payment.ID), adminBundle.AccessToken)
	decode(paymentResponse, &paymentView)
	assert.Contains(t, paymentResponse.Body.String(), "accounting-order")
	for _, secret := range []string{"QuotaPerUnit", "quota", "Password", "private_key"} {
		assert.NotContains(t, paymentResponse.Body.String(), secret)
	}
	// Main-store records survive destruction of independent log projections.
	require.NoError(t, model.LOG_DB.Migrator().DropTable(&model.Log{}))
	decode(request(path, bundle.AccessToken), &view)
	assert.Equal(t, "4.5", view.Total.Amount)
	decode(request("/api/marketing/user/consumption?month="+month, bundle.AccessToken), &monthly)
	assert.Equal(t, "1.5", monthly.Amounts[0].Amount)
	// Session validity alone must not override the current account status.
	require.NoError(t, model.DB.Model(user).Update("status", common.UserStatusDisabled).Error)
	assert.Equal(t, http.StatusUnauthorized, request(path, bundle.AccessToken).Code)
	decode(request(fmt.Sprintf("/api/marketing/admin/accounting/%d", funding.ID), adminBundle.AccessToken), &adminView)
	assert.Equal(t, "4.5", adminView.Total.Amount, "disabled users retain auditable history")
}
