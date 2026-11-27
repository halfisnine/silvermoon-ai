package controller

import (
	"context"
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

// The complete HTTP surface uses real session validation, database roles,
// financial conversion and domain mutators. The fixture supports all three
// database engines through TEST_SECURITY_DIALECT and its isolated database DSN.
func TestMarketingAdminHTTP(t *testing.T) {
	admin, _ := setupSecurityEnrollmentTest(t)
	require.NoError(t, model.DB.Model(admin).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, model.PublishUserAuthCache(admin.Id))
	models := append(marketingRegistrationFixtureModels(), &model.Redemption{}, &model.TopUp{}, &model.MarketingAdminCatalog{}, &model.MarketingCheckinDay{}, &model.MarketingCheckinClaim{})
	require.NoError(t, model.DB.AutoMigrate(models...))
	require.NoError(t, model.DB.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
	oldUnits, oldExchange := common.QuotaPerUnit, operation_setting.USDExchangeRate
	oldGeneral := *operation_setting.GetGeneralSetting()
	common.QuotaPerUnit, operation_setting.USDExchangeRate = 50000, 2
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate = oldUnits, oldExchange
		*operation_setting.GetGeneralSetting() = oldGeneral
	})
	bundle, err := service.CreateLoginSession(admin.Id, "password", "127.0.0.1", "marketing-admin-test")
	require.NoError(t, err)
	router := gin.New()
	group := router.Group("/api/marketing/admin", middleware.AdminAuth())
	group.GET("/metadata", MarketingAdminRead)
	group.GET("/:resource", MarketingAdminRead)
	group.GET("/:resource/:id", MarketingAdminRead)
	group.GET("/rewards/:kind/:id", MarketingAdminRead)
	group.POST("/:resource", MarketingAdminWrite)
	group.PUT("/:resource/:id", MarketingAdminWrite)
	group.POST("/:resource/:id/:action", MarketingAdminWrite)
	request := func(method, path string, body any, token string) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			var err error
			data, err = common.Marshal(body)
			require.NoError(t, err)
		}
		req := httptest.NewRequest(method, "/api/marketing/admin"+path, strings.NewReader(string(data)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://untrusted.example")
		req.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: bundle.RefreshToken})
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	decode := func(response *httptest.ResponseRecorder, destination any) {
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var envelope struct {
			Success bool              `json:"success"`
			Data    common.RawMessage `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
		require.True(t, envelope.Success, response.Body.String())
		require.NoError(t, common.Unmarshal(envelope.Data, destination))
	}
	assert.Equal(t, http.StatusUnauthorized, request("POST", "/activities", map[string]any{}, "").Code, "a cross-site refresh cookie cannot authenticate a mutation")
	var metadata struct {
		Currency string `json:"currency"`
		Revision string `json:"money_revision"`
	}
	decode(request("GET", "/metadata", nil, bundle.AccessToken), &metadata)
	assert.Equal(t, "CNY", metadata.Currency)
	require.NotEmpty(t, metadata.Revision)
	now := time.Now().Unix()
	poolInput := service.MarketingAdminRequest{OperationKey: "create-pool", MoneyRevision: metadata.Revision, Pool: &service.MarketingAdminPoolForm{Name: "Limited pool", Style: "wheel", StartsAt: now + 3600, EndsAt: now + 86400, Capacity: 2, Prizes: []service.MarketingAdminPrize{{Name: "Gift", Weight: 1, Stock: 2, Gifts: []service.MarketingAdminGift{{Amount: "0.00004", Currency: "CNY", ValidSeconds: 3600}}}}}}
	var pool service.MarketingAdminPoolView
	response := request("POST", "/pools", poolInput, bundle.AccessToken)
	decode(response, &pool)
	assert.Equal(t, "0.00004", pool.Pool.Prizes[0].Gifts[0].Amount)
	assert.Zero(t, pool.Revision)
	for _, hidden := range []string{"QuotaPerUnit", "CurrencyPerUnit", "quota", "PayloadHash", "Spec"} {
		assert.NotContains(t, response.Body.String(), hidden)
	}
	var replay service.MarketingAdminPoolView
	decode(request("POST", "/pools", poolInput, bundle.AccessToken), &replay)
	assert.Equal(t, pool.ID, replay.ID)
	changed := poolInput
	changed.Pool = &service.MarketingAdminPoolForm{Name: "changed"}
	assert.Equal(t, http.StatusConflict, request("POST", "/pools", changed, bundle.AccessToken).Code)
	zero := int64(0)
	op := service.MarketingAdminRequest{OperationKey: "append-without-supply", Revision: &zero, Amount: 1}
	poolPath := fmt.Sprintf("/pools/%d", pool.ID)
	assert.Equal(t, http.StatusConflict, request("POST", poolPath+"/append", op, bundle.AccessToken).Code)
	op.OperationKey = "restock"
	op.PrizeID = pool.Pool.Prizes[0].ID
	op.Amount = 3
	decode(request("POST", poolPath+"/restock", op, bundle.AccessToken), &pool)
	assert.EqualValues(t, 5, pool.Pool.Prizes[0].Stock)
	decode(request("POST", poolPath+"/restock", op, bundle.AccessToken), &replay)
	assert.EqualValues(t, 5, replay.Pool.Prizes[0].Stock, "retry does not add stock twice")
	op = service.MarketingAdminRequest{OperationKey: "append-without-supply", Revision: &pool.Revision, Amount: 1}
	decode(request("POST", poolPath+"/append", op, bundle.AccessToken), &pool)
	assert.EqualValues(t, 3, pool.Pool.Capacity, "failed operation key can be corrected and retried")
	assert.Equal(t, http.StatusConflict, request("POST", poolPath+"/pause", service.MarketingAdminRequest{OperationKey: "stale", Revision: &zero}, bundle.AccessToken).Code)
	invalidPrize := service.MarketingAdminRequest{OperationKey: "bad-prize", Revision: &pool.Revision, PrizeID: 999999, Amount: 1}
	assert.Equal(t, http.StatusBadRequest, request("POST", poolPath+"/restock", invalidPrize, bundle.AccessToken).Code)
	// Same currency but a changed quota conversion must invalidate an open form.
	operation_setting.USDExchangeRate = 4
	staleMoney := poolInput
	staleMoney.OperationKey = "stale-money"
	assert.Equal(t, http.StatusConflict, request("POST", "/pools", staleMoney, bundle.AccessToken).Code)
	decode(request("POST", "/pools", poolInput, bundle.AccessToken), &replay)
	assert.Equal(t, pool.ID, replay.ID, "completed operation replay uses its original conversion")
	operation_setting.USDExchangeRate = 2
	// Invalid cumulative percentage rewards cannot be smuggled in via direct HTTP.
	activityInput := service.MarketingAdminRequest{OperationKey: "activity-corrected", MoneyRevision: metadata.Revision, Activity: &service.MarketingAdminActivityForm{Name: "Recharge", Kind: "recharge", Published: true, StartsAt: now - 1, EndsAt: now + 86400, Condition: "cumulative_payments", Frequency: "every", ThresholdCNY: "10", ExclusiveGroup: "shared", Priority: 2, Rewards: []service.MarketingAdminReward{{Kind: "gift", Calculation: "paid_ratio", Currency: "CNY", RatioPercent: "5"}}}}
	assert.Equal(t, http.StatusBadRequest, request("POST", "/activities", activityInput, bundle.AccessToken).Code)
	activityInput.Activity.Rewards = []service.MarketingAdminReward{{Kind: "gift", Calculation: "fixed", Amount: "2", Currency: "CNY", ValidSeconds: 3600}, {Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "direct"}}
	var activity service.MarketingAdminActivityView
	decode(request("POST", "/activities", activityInput, bundle.AccessToken), &activity)
	assert.Equal(t, "draft", activity.State)
	assert.Equal(t, "2", activity.Activity.Rewards[0].Amount)
	assert.Equal(t, "Limited pool", activity.Activity.Rewards[1].PoolName)
	activityPath := fmt.Sprintf("/activities/%d", activity.ID)
	activate := service.MarketingAdminRequest{OperationKey: "activate-activity", Revision: &activity.Revision}
	decode(request("POST", activityPath+"/activate", activate, bundle.AccessToken), &activity)
	assert.Equal(t, "active", activity.State)
	assert.False(t, activity.Editable)
	edit := activityInput
	edit.OperationKey = "locked-edit"
	edit.Revision = &activity.Revision
	assert.Equal(t, http.StatusConflict, request("PUT", activityPath, edit, bundle.AccessToken).Code)
	copyInput := activityInput
	copyForm := *activityInput.Activity
	copyForm.Name = "Copied recharge"
	copyInput.Activity = &copyForm
	copyInput.OperationKey = "copy-activity"
	var copied service.MarketingAdminActivityView
	decode(request("POST", "/activities", copyInput, bundle.AccessToken), &copied)
	assert.NotEqual(t, activity.ID, copied.ID)
	assert.Equal(t, "draft", copied.State)
	// A successful payment qualifies using actual CNY, while its fixed gift is
	// recorded using the trusted site conversion. The admin record follows the
	// real payment and recipient, not arbitrary client-supplied provenance.
	target := model.User{Username: "marketing-recipient", AffCode: "marketing-recipient", Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
	require.NoError(t, model.DB.Create(&target).Error)
	require.NoError(t, model.DB.Create(&model.MarketingAccount{UserID: target.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: target.Id, Kind: "online", Reference: "admin-http-payment", PaidCents: 1000, PostedAt: time.Now().Unix(), Money: model.MarketingMoneySnapshot{Amount: "10", Currency: "CNY", QuotaPerUnit: "50000", CurrencyPerUnit: "2"}})
		return err
	}))
	var payment model.MarketingPayment
	require.NoError(t, model.DB.Where("user_id = ?", target.Id).First(&payment).Error)
	require.NoError(t, model.DB.Create(&model.TopUp{UserId: target.Id, PaymentID: payment.ID, TradeNo: "order-admin-http"}).Error)
	_, err = service.ProcessMarketingActivityFacts(context.Background(), target.Id)
	require.NoError(t, err)
	var rewards struct {
		Items []service.MarketingAdminRewardRecord `json:"items"`
		Total int64                                `json:"total"`
	}
	decode(request("GET", fmt.Sprintf("/rewards?user_id=%d&activity_id=%d", target.Id, activity.ID), nil, bundle.AccessToken), &rewards)
	require.Len(t, rewards.Items, 2)
	assert.EqualValues(t, 2, rewards.Total)
	for _, reward := range rewards.Items {
		assert.Equal(t, "10", reward.Source["paid_cny"])
		assert.Equal(t, "order-admin-http", reward.Source["payment_reference"])
		assert.Equal(t, "marketing-recipient", reward.Username)
		assert.Equal(t, "Recharge", reward.SourceName)
	}
	// A promised opportunity locks an otherwise unstarted pool against reducing
	// existing supply. Independent append/restock/extend remain explicit actions.
	decode(request("GET", poolPath, nil, bundle.AccessToken), &pool)
	assert.False(t, pool.Editable)
	poolEdit := poolInput
	poolEdit.OperationKey = "edit-promised-pool"
	poolEdit.Revision = &pool.Revision
	assert.Equal(t, http.StatusConflict, request("PUT", poolPath, poolEdit, bundle.AccessToken).Code)
	extend := service.MarketingAdminRequest{OperationKey: "extend-pool", Revision: &pool.Revision, EndsAt: now + 172800}
	decode(request("POST", poolPath+"/extend", extend, bundle.AccessToken), &pool)
	var chance model.MarketingLotteryChance
	require.NoError(t, model.DB.Where("user_id = ?", target.Id).First(&chance).Error)
	assert.Equal(t, extend.EndsAt, chance.ExpiresAt)
	for _, path := range []string{"/activities?page_size=101", "/activities?page=0", "/pools?page_size=-1", "/rewards?user_id=-1"} {
		assert.Equal(t, http.StatusBadRequest, request("GET", path, nil, bundle.AccessToken).Code)
	}
	// All four scene forms traverse the same server conversion and conditional
	// validation, including nested daily/streak rewards and invitation ratios.
	fixed := []service.MarketingAdminReward{{Kind: "gift", Calculation: "fixed", Amount: "1", Currency: "CNY", ValidSeconds: 3600}}
	sceneForms := []service.MarketingAdminActivityForm{
		{Name: "Registration", Kind: "registration", Condition: "registration", Frequency: "once", StartsAt: now + 3600, EndsAt: now + 86400, Rewards: fixed},
		{Name: "Daily", Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: now + 3600, EndsAt: now + 86400, Rewards: fixed, Checkin: &service.MarketingAdminCheckin{Condition: "today_paid", ThresholdCNY: "5", CycleDays: 7, MakeupDays: 2, MakeupLimit: 1, MakeupDaily: true, Milestones: []service.MarketingAdminMilestone{{Days: 7, Rewards: fixed}}}},
		{Name: "Invitation", Kind: "invitation", Condition: "friend_ratio", Frequency: "every", StartsAt: now + 3600, EndsAt: now + 86400, Rewards: []service.MarketingAdminReward{{Kind: "gift", Calculation: "cash_ratio", RatioPercent: "5", Currency: "CNY"}}},
		{Name: "First payment", Kind: "recharge", Condition: "first_payment", Frequency: "once", ThresholdCNY: "10", StartsAt: now + 3600, EndsAt: now + 86400, Rewards: fixed},
	}
	for i, form := range sceneForms {
		input := service.MarketingAdminRequest{OperationKey: fmt.Sprintf("scene-%d", i), MoneyRevision: metadata.Revision, Activity: &form}
		var view service.MarketingAdminActivityView
		decode(request("POST", "/activities", input, bundle.AccessToken), &view)
		assert.Equal(t, form.Condition, view.Activity.Condition)
		assert.Equal(t, form.Rewards[0].Calculation, view.Activity.Rewards[0].Calculation)
		if form.Kind == "checkin" {
			require.NotNil(t, view.Activity.Checkin)
			assert.Equal(t, "5", view.Activity.Checkin.ThresholdCNY)
			require.Len(t, view.Activity.Checkin.Milestones, 1)
			assert.Equal(t, 7, view.Activity.Checkin.Milestones[0].Days)
		}
		input.OperationKey = fmt.Sprintf("edit-scene-%d", i)
		input.Revision = &view.Revision
		input.Activity.Name += " edited"
		decode(request("PUT", fmt.Sprintf("/activities/%d", view.ID), input, bundle.AccessToken), &view)
		assert.Equal(t, input.Activity.Name, view.Activity.Name)
	}
	var groups struct {
		Items []model.MarketingAdminGroup `json:"items"`
		Total int64                       `json:"total"`
	}
	decode(request("GET", "/groups?keyword=shared", nil, bundle.AccessToken), &groups)
	require.Len(t, groups.Items, 1)
	assert.Equal(t, "shared", groups.Items[0].Name)
	assert.EqualValues(t, 2, groups.Items[0].Count)
	for i, groupName := range []string{"GroupA", "groupa"} {
		form := sceneForms[0]
		form.Name = groupName
		form.ExclusiveGroup = groupName
		input := service.MarketingAdminRequest{OperationKey: fmt.Sprintf("case-group-%d", i), MoneyRevision: metadata.Revision, Activity: &form}
		var view service.MarketingAdminActivityView
		decode(request("POST", "/activities", input, bundle.AccessToken), &view)
	}
	decode(request("GET", "/groups?page_size=100", nil, bundle.AccessToken), &groups)
	groupCounts := map[string]int64{}
	for _, g := range groups.Items {
		groupCounts[g.Name] = g.Count
	}
	assert.EqualValues(t, 1, groupCounts["GroupA"])
	assert.EqualValues(t, 1, groupCounts["groupa"], "SQL collation must not merge distinct rule-engine groups")
	var members struct {
		Items []service.MarketingAdminActivityView `json:"items"`
		Total int64                                `json:"total"`
	}
	decode(request("GET", "/activities?exclusive_group=shared&page_size=1", nil, bundle.AccessToken), &members)
	assert.EqualValues(t, 2, members.Total)
	require.Len(t, members.Items, 1)
	for _, groupName := range []string{"GroupA", "groupa"} {
		decode(request("GET", "/activities?exclusive_group="+groupName, nil, bundle.AccessToken), &members)
		assert.EqualValues(t, 1, members.Total)
		require.Len(t, members.Items, 1)
		assert.Equal(t, groupName, members.Items[0].Activity.ExclusiveGroup)
	}
	decode(request("GET", "/rewards?username=marketing-recipient", nil, bundle.AccessToken), &rewards)
	assert.EqualValues(t, 2, rewards.Total)
	var detail service.MarketingAdminRewardRecord
	for _, reward := range rewards.Items {
		decode(request("GET", fmt.Sprintf("/rewards/activity/%d", reward.ID), nil, bundle.AccessToken), &detail)
		require.Len(t, detail.Rewards, 1)
		require.NotNil(t, detail.Rewards[0].ExpiresAt)
		assert.Positive(t, detail.Rewards[0].CreditedAt)
	}
	// Actual award expirations are projected separately for every component.
	activeInput := service.MarketingAdminRequest{OperationKey: "active-pool", MoneyRevision: metadata.Revision, Pool: &service.MarketingAdminPoolForm{Name: "Combination", Style: "blind_box", StartsAt: now - 1, EndsAt: now + 86400, Capacity: 1, Prizes: []service.MarketingAdminPrize{{Name: "Two gifts", Weight: 1, Stock: 1, Gifts: []service.MarketingAdminGift{{Amount: "1", Currency: "CNY", ValidSeconds: 3600}, {Amount: "2", Currency: "CNY"}}}}}}
	var activePool service.MarketingAdminPoolView
	decode(request("POST", "/pools", activeInput, bundle.AccessToken), &activePool)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.IssueMarketingLotteryChances(tx, target.Id, activePool.ID, "admin-http-chance", 1, time.Now().Unix())
		return err
	}))
	draw, err := service.DrawMarketingLottery(context.Background(), target.Id, activePool.ID, "admin-http-draw")
	require.NoError(t, err)
	decode(request("GET", fmt.Sprintf("/rewards/lottery/%d", draw.Draw.ID), nil, bundle.AccessToken), &detail)
	require.Len(t, detail.Rewards, 2)
	require.NotNil(t, detail.Rewards[0].ExpiresAt)
	require.NotNil(t, detail.Rewards[1].ExpiresAt)
	assert.Equal(t, detail.Rewards[0].CreditedAt+3600, *detail.Rewards[0].ExpiresAt)
	assert.Zero(t, *detail.Rewards[1].ExpiresAt)
	assert.Equal(t, "Two gifts", detail.Source["prize_name"])
	t.Run("organize administrator lists without changing user availability", func(t *testing.T) {
		var freshPool model.MarketingLotteryPool
		require.NoError(t, model.DB.First(&freshPool, activePool.ID).Error)
		activePool.Revision = freshPool.Revision
		userFilter := service.MarketingUserFilter{Page: 1, PageSize: 100}
		rewardsBefore, err := service.ReadMarketingUserRewards(context.Background(), target.Id, 0, userFilter)
		require.NoError(t, err)
		giftsBefore, err := service.ReadMarketingUserGifts(context.Background(), target.Id, 0, userFilter)
		require.NoError(t, err)
		var publicBefore []model.MarketingActivity
		require.NoError(t, model.PublicMarketingActivities(model.DB, now).Order("id").Find(&publicBefore).Error)
		for _, item := range []struct {
			path     string
			revision int64
		}{{activityPath, activity.Revision}, {poolPath, pool.Revision}, {fmt.Sprintf("/pools/%d", activePool.ID), activePool.Revision}} {
			input := service.MarketingAdminRequest{OperationKey: fmt.Sprintf("archive-%d", item.revision) + strings.ReplaceAll(item.path, "/", "-"), Revision: &item.revision}
			var archived struct {
				ID       int64 `json:"id"`
				Revision int64 `json:"revision"`
				Archived bool  `json:"archived"`
			}
			decode(request("POST", item.path+"/archive", input, bundle.AccessToken), &archived)
			assert.True(t, archived.Archived)
			var publicArchived []model.MarketingActivity
			require.NoError(t, model.PublicMarketingActivities(model.DB, now).Order("id").Find(&publicArchived).Error)
			require.Len(t, publicArchived, len(publicBefore))
			for i := range publicArchived {
				assert.Equal(t, publicBefore[i].ID, publicArchived[i].ID)
				assert.Equal(t, publicBefore[i].Spec, publicArchived[i].Spec)
				assert.Equal(t, publicBefore[i].EnabledAt, publicArchived[i].EnabledAt)
			}
			rewardsAfter, err := service.ReadMarketingUserRewards(context.Background(), target.Id, 0, userFilter)
			require.NoError(t, err)
			assert.Equal(t, rewardsBefore, rewardsAfter)
			giftsAfter, err := service.ReadMarketingUserGifts(context.Background(), target.Id, 0, userFilter)
			require.NoError(t, err)
			assert.Equal(t, giftsBefore, giftsAfter)
			if item.path == fmt.Sprintf("/pools/%d", activePool.ID) {
				_, err := service.ReadMarketingUserPools(context.Background(), target.Id, activePool.ID, userFilter)
				require.NoError(t, err, "archived pool remains visible to its participant")
				_, err = service.ReadMarketingUserDraw(context.Background(), target.Id, draw.Draw.ID, "")
				require.NoError(t, err, "archived pool retains past results")
			}
			var replayed struct {
				Archived bool `json:"archived"`
			}
			decode(request("POST", item.path+"/archive", input, bundle.AccessToken), &replayed)
			assert.True(t, replayed.Archived, "stable retries preserve the result")
			assert.Equal(t, http.StatusConflict, request("POST", item.path+"/unarchive", service.MarketingAdminRequest{OperationKey: "stale-unarchive" + strings.ReplaceAll(item.path, "/", "-"), Revision: &item.revision}, bundle.AccessToken).Code)
			var listed struct {
				Items []struct {
					ID int64 `json:"id"`
				} `json:"items"`
			}
			resource := "/" + strings.Split(item.path, "/")[1]
			decode(request("GET", resource, nil, bundle.AccessToken), &listed)
			for _, row := range listed.Items {
				assert.NotEqual(t, archived.ID, row.ID)
			}
			decode(request("GET", resource+"?archive=archived", nil, bundle.AccessToken), &listed)
			require.Len(t, listed.Items, 1)
			assert.Equal(t, archived.ID, listed.Items[0].ID)
			decode(request("GET", resource+"?archive=all", nil, bundle.AccessToken), &listed)
			assert.NotEmpty(t, listed.Items)
			assert.Equal(t, http.StatusConflict, request("POST", item.path+"/delete", service.MarketingAdminRequest{OperationKey: "used-delete" + strings.ReplaceAll(item.path, "/", "-"), Revision: &archived.Revision}, bundle.AccessToken).Code)
			decode(request("POST", item.path+"/unarchive", service.MarketingAdminRequest{OperationKey: "restore" + strings.ReplaceAll(item.path, "/", "-"), Revision: &archived.Revision}, bundle.AccessToken), &archived)
			assert.False(t, archived.Archived)
			if resource == "/activities" {
				activity.Revision = archived.Revision
			} else if item.path == poolPath {
				pool.Revision = archived.Revision
			} else {
				activePool.Revision = archived.Revision
			}
		}
		var publicAfter []model.MarketingActivity
		require.NoError(t, model.PublicMarketingActivities(model.DB, now).Order("id").Find(&publicAfter).Error)
		require.Len(t, publicAfter, len(publicBefore))
		for i := range publicBefore {
			publicBefore[i].Revision = publicAfter[i].Revision
		}
		assert.Equal(t, publicBefore, publicAfter, "only administrator organization changed")
		assert.Equal(t, http.StatusBadRequest, request("GET", "/activities?archive=invalid", nil, bundle.AccessToken).Code)
		assert.Equal(t, http.StatusBadRequest, request("GET", "/pools?archive=invalid", nil, bundle.AccessToken).Code)
		// Both records are unused: delete a draft first, then its now-unreferenced pool.
		poolInput.OperationKey = "unused-pool"
		var unusedPool service.MarketingAdminPoolView
		decode(request("POST", "/pools", poolInput, bundle.AccessToken), &unusedPool)
		unusedActivity := activityInput
		unusedActivity.OperationKey = "unused-activity"
		form := *activityInput.Activity
		form.Rewards = []service.MarketingAdminReward{{Kind: "chance", PoolID: unusedPool.ID, Count: 1, ChanceMode: "direct"}}
		unusedActivity.Activity = &form
		var draft service.MarketingAdminActivityView
		decode(request("POST", "/activities", unusedActivity, bundle.AccessToken), &draft)
		unusedPoolPath := fmt.Sprintf("/pools/%d", unusedPool.ID)
		assert.Equal(t, http.StatusConflict, request("POST", unusedPoolPath+"/delete", service.MarketingAdminRequest{OperationKey: "referenced-delete", Revision: &unusedPool.Revision}, bundle.AccessToken).Code)
		for _, item := range []struct {
			path     string
			revision int64
		}{{fmt.Sprintf("/activities/%d", draft.ID), draft.Revision}, {unusedPoolPath, unusedPool.Revision}} {
			input := service.MarketingAdminRequest{OperationKey: "unused-delete" + strings.ReplaceAll(item.path, "/", "-"), Revision: &item.revision}
			var deleted struct {
				Deleted bool `json:"deleted"`
			}
			decode(request("POST", item.path+"/delete", input, bundle.AccessToken), &deleted)
			assert.True(t, deleted.Deleted)
			decode(request("POST", item.path+"/delete", input, bundle.AccessToken), &deleted)
			assert.Equal(t, http.StatusNotFound, request("GET", item.path, nil, bundle.AccessToken).Code)
		}
		// Competing administrators use different actor row locks and database
		// connections; either the reference commits or deletion wins, never both.
		secondAdmin := model.User{Username: "marketing-organizer", AffCode: "marketing-organizer", Status: common.UserStatusEnabled, Role: common.RoleAdminUser}
		require.NoError(t, model.DB.Create(&secondAdmin).Error)
		poolInput.OperationKey = "concurrent-pool"
		var concurrentPool service.MarketingAdminPoolView
		decode(request("POST", "/pools", poolInput, bundle.AccessToken), &concurrentPool)
		concurrentForm := *unusedActivity.Activity
		concurrentForm.Name = "Concurrent reference"
		concurrentForm.Rewards = []service.MarketingAdminReward{{Kind: "chance", PoolID: concurrentPool.ID, Count: 1, ChanceMode: "direct"}}
		ready := make(chan struct{})
		outcomes := make(chan error, 2)
		go func() {
			<-ready
			_, err := service.WriteMarketingAdmin(context.Background(), admin.Id, "activities", "create", 0, service.MarketingAdminRequest{OperationKey: "concurrent-reference", MoneyRevision: metadata.Revision, Activity: &concurrentForm})
			outcomes <- err
		}()
		go func() {
			<-ready
			_, err := service.WriteMarketingAdmin(context.Background(), secondAdmin.Id, "pools", "delete", concurrentPool.ID, service.MarketingAdminRequest{OperationKey: "concurrent-delete", Revision: &concurrentPool.Revision})
			outcomes <- err
		}()
		close(ready)
		first, second := <-outcomes, <-outcomes
		assert.NotEqual(t, first == nil, second == nil, "reference creation and deletion cannot both commit")
		var surviving []model.MarketingActivity
		require.NoError(t, model.DB.Where("name = ?", concurrentForm.Name).Find(&surviving).Error)
		var remainingPool model.MarketingLotteryPool
		err = model.DB.First(&remainingPool, concurrentPool.ID).Error
		if len(surviving) > 0 {
			assert.NoError(t, err, "committed reference retains pool")
		} else {
			assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
		}
	})

	t.Run("manual_gifts_preserve_assets_and_retry_only_unsuccessful_recipients", func(t *testing.T) {
		// Upgrade a populated marketing database; repeat migration and retain prior
		// gifts, paid money, and all existing ledger facts unchanged.
		var prior []model.MarketingGift
		require.NoError(t, model.DB.Order("id").Find(&prior).Error)
		for range 2 {
			require.NoError(t, model.DB.AutoMigrate(&model.MarketingGiftBatch{}, &model.MarketingGiftRecipient{}))
		}
		var after []model.MarketingGift
		require.NoError(t, model.DB.Order("id").Find(&after).Error)
		assert.Equal(t, prior, after)
		first := model.User{Username: "manual-first", AffCode: "manual-first", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: 19}
		second := model.User{Username: "manual-second", AffCode: "manual-second", Status: common.UserStatusDisabled, Role: common.RoleCommonUser, Quota: 23}
		require.NoError(t, model.DB.Create(&first).Error)
		require.NoError(t, model.DB.Create(&second).Error)
		var current struct {
			Currency string `json:"currency"`
			Revision string `json:"money_revision"`
		}
		decode(request("GET", "/metadata", nil, bundle.AccessToken), &current)
		input := service.MarketingGiftBatchInput{OperationKey: "manual-partial", MoneyRevision: current.Revision, UserIDs: []int{second.Id, first.Id}, Amount: "0.00004", Currency: current.Currency, ValidSeconds: 3600, Reason: "internal compensation evidence"}
		assert.Equal(t, http.StatusUnauthorized, request("POST", "/gift-batches", input, "").Code)
		var batch service.MarketingGiftBatchView
		response := request("POST", "/gift-batches", input, bundle.AccessToken)
		decode(response, &batch)
		assert.Equal(t, 2, batch.Total)
		assert.Equal(t, 1, batch.Credited)
		assert.Equal(t, 1, batch.Failed)
		assert.Equal(t, "0.00004", batch.TotalAmount)
		require.Len(t, batch.Recipients, 2)
		var gifted model.MarketingGift
		require.NoError(t, model.DB.Where("user_id = ?", first.Id).First(&gifted).Error)
		assert.EqualValues(t, 1, gifted.GrantedQuota)
		assert.EqualValues(t, 3600, gifted.ExpiresAt-gifted.GrantedAt)
		assert.Equal(t, "admin_gift", gifted.SourceKind)
		assert.NotContains(t, response.Body.String(), "QuotaPerUnit")
		// A changed form using the same operation key must never re-credit anyone.
		changed := input
		changed.Amount = "2"
		assert.Equal(t, http.StatusConflict, request("POST", "/gift-batches", changed, bundle.AccessToken).Code)
		var replay service.MarketingGiftBatchView
		decode(request("POST", "/gift-batches", input, bundle.AccessToken), &replay)
		assert.Equal(t, batch.ID, replay.ID)
		require.NoError(t, model.DB.Model(&second).Update("status", common.UserStatusEnabled).Error)
		common.QuotaPerUnit = 100000
		decode(request("POST", "/gift-batches", input, bundle.AccessToken), &replay)
		assert.Equal(t, 2, replay.Credited)
		assert.Zero(t, replay.Failed)
		assert.Equal(t, "0.00008", replay.TotalAmount)
		var secondGift model.MarketingGift
		require.NoError(t, model.DB.Where("user_id = ?", second.Id).First(&secondGift).Error)
		assert.EqualValues(t, 1, secondGift.GrantedQuota, "retry retains original conversion")
		assert.EqualValues(t, 3600, secondGift.ExpiresAt-secondGift.GrantedAt)
		common.QuotaPerUnit = 50000
		var paidFirst, paidSecond model.User
		require.NoError(t, model.DB.First(&paidFirst, first.Id).Error)
		require.NoError(t, model.DB.First(&paidSecond, second.Id).Error)
		assert.Equal(t, 19, paidFirst.Quota)
		assert.Equal(t, 23, paidSecond.Quota)
		for _, record := range []any{&model.MarketingPayment{}, &model.MarketingActivityFact{}} {
			var count int64
			require.NoError(t, model.DB.Model(record).Where("user_id IN ?", []int{first.Id, second.Id}).Count(&count).Error)
			assert.Zero(t, count, "manual gifts are not payments or activity triggers")
		}
		// Administrator list is global and includes the source and linked batch.
		var gifts struct {
			Items []service.MarketingAdminGiftView `json:"items"`
			Total int64                            `json:"total"`
		}
		decode(request("GET", "/gifts?kind=admin_gift&keyword=manual-&validity=limited", nil, bundle.AccessToken), &gifts)
		assert.EqualValues(t, 2, gifts.Total)
		for _, g := range gifts.Items {
			assert.Equal(t, "Administrator gift", g.SourceNameKey)
			assert.Equal(t, batch.ID, g.BatchID)
		}
		// Owner projections expose their own gift source, never internal reasons or
		// another user's gift. The same route remains owner-scoped for admins too.
		ug := router.Group("/api/marketing/user", middleware.UserAuth())
		ug.GET("/:resource", MarketingUserRead)
		ug.GET("/:resource/:id", MarketingUserRead)
		ownBundle, err := service.CreateLoginSession(first.Id, "password", "127.0.0.1", "gift-owner")
		require.NoError(t, err)
		ownerRequest := func(path, token string) *httptest.ResponseRecorder {
			req := httptest.NewRequest("GET", "/api/marketing/user"+path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			return rec
		}
		var owned struct {
			Items []service.MarketingUserGift `json:"items"`
			Total int64                       `json:"total"`
		}
		ownerResponse := ownerRequest("/gifts", ownBundle.AccessToken)
		decode(ownerResponse, &owned)
		require.Len(t, owned.Items, 1)
		assert.Equal(t, gifted.ID, owned.Items[0].ID)
		assert.Equal(t, "Administrator gift", owned.Items[0].SourceNameKey)
		assert.NotContains(t, ownerResponse.Body.String(), input.Reason)
		assert.NotContains(t, ownerResponse.Body.String(), "actor_id")
		assert.NotContains(t, ownerResponse.Body.String(), "batch_id")
		assert.Equal(t, http.StatusNotFound, ownerRequest(fmt.Sprintf("/gifts/%d", secondGift.ID), ownBundle.AccessToken).Code)
		assert.Equal(t, http.StatusForbidden, request("GET", "/gifts", nil, ownBundle.AccessToken).Code)
		assert.Equal(t, http.StatusForbidden, request("POST", fmt.Sprintf("/gift-batches/%d/retry", batch.ID), map[string]any{}, ownBundle.AccessToken).Code)
		// Permanent and expired amounts stay gifts; expiry is not consumption.
		permanent := input
		permanent.OperationKey = "manual-permanent"
		permanent.UserIDs = []int{first.Id}
		permanent.ValidSeconds = 0
		decode(request("POST", "/gift-batches", permanent, bundle.AccessToken), &replay)
		require.Len(t, replay.Recipients, 1)
		require.NotNil(t, replay.Recipients[0].Gift)
		assert.Zero(t, replay.Recipients[0].Gift.ExpiresAt)
		require.NoError(t, model.DB.Model(&gifted).Update("expires_at", time.Now().Unix()-1).Error)
		decode(request("GET", "/gifts?keyword=manual-first&state=expired", nil, bundle.AccessToken), &gifts)
		require.Len(t, gifts.Items, 1)
		assert.Equal(t, "0.00004", gifts.Items[0].ExpiredAmount)
		assert.Equal(t, "0", gifts.Items[0].ConsumedAmount)
		assert.Equal(t, "0", gifts.Items[0].RemainingAmount)
		// New HTTP data is bounded and validated before a batch is accepted.
		for _, tc := range []struct {
			name   string
			modify func(*service.MarketingGiftBatchInput)
		}{
			{"duplicate", func(v *service.MarketingGiftBatchInput) { v.UserIDs = []int{first.Id, first.Id} }},
			{"zero", func(v *service.MarketingGiftBatchInput) { v.Amount = "0" }},
			{"negative", func(v *service.MarketingGiftBatchInput) { v.Amount = "-1" }},
			{"blank_reason", func(v *service.MarketingGiftBatchInput) { v.Reason = " " }},
			{"invalid_duration", func(v *service.MarketingGiftBatchInput) { v.ValidSeconds = -1 }},
			{"too_long", func(v *service.MarketingGiftBatchInput) { v.ValidSeconds = model.MarketingGiftMaxValidity + 1 }},
			{"too_many", func(v *service.MarketingGiftBatchInput) { v.UserIDs = make([]int, 101) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				v := input
				v.OperationKey = "invalid-" + tc.name
				tc.modify(&v)
				assert.Equal(t, http.StatusBadRequest, request("POST", "/gift-batches", v, bundle.AccessToken).Code)
			})
		}
		// A ledger failure after its INSERT rolls back the gift and wallet revision
		// before recording failure; two different admins can then retry once.
		t.Run("ledger_rollback_and_concurrent_retry", func(t *testing.T) {
			var accountBefore model.MarketingAccount
			require.NoError(t, model.DB.First(&accountBefore, first.Id).Error)
			var countBefore int64
			require.NoError(t, model.DB.Model(&model.MarketingGift{}).Where("user_id = ?", first.Id).Count(&countBefore).Error)
			const callback = "test:manual_gift_ledger_failure"
			require.NoError(t, model.DB.Callback().Create().After("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "marketing_v2_ledger" {
					tx.AddError(fmt.Errorf("injected ledger write failure"))
				}
			}))
			defer model.DB.Callback().Create().Remove(callback)
			v := permanent
			v.OperationKey = "manual-ledger-rollback"
			var failed service.MarketingGiftBatchView
			decode(request("POST", "/gift-batches", v, bundle.AccessToken), &failed)
			assert.Zero(t, failed.Credited)
			assert.Equal(t, 1, failed.Failed)
			require.Len(t, failed.Recipients, 1)
			assert.Equal(t, "Gift delivery failed; retry this batch", failed.Recipients[0].ErrorKey)
			var countAfter int64
			require.NoError(t, model.DB.Model(&model.MarketingGift{}).Where("user_id = ?", first.Id).Count(&countAfter).Error)
			assert.Equal(t, countBefore, countAfter)
			var accountAfter model.MarketingAccount
			require.NoError(t, model.DB.First(&accountAfter, first.Id).Error)
			assert.Equal(t, accountBefore, accountAfter)
			require.NoError(t, model.DB.Callback().Create().Remove(callback))
			other := model.User{Username: "manual-other-admin", AffCode: "manual-other-admin", Status: common.UserStatusEnabled, Role: common.RoleAdminUser}
			require.NoError(t, model.DB.Create(&other).Error)
			ready := make(chan struct{})
			outcomes := make(chan error, 2)
			for _, actorID := range []int{admin.Id, other.Id} {
				go func() {
					<-ready
					_, e := service.RetryMarketingAdminGifts(context.Background(), actorID, failed.ID)
					outcomes <- e
				}()
			}
			close(ready)
			require.NoError(t, <-outcomes)
			require.NoError(t, <-outcomes)
			decode(request("GET", fmt.Sprintf("/gift-batches/%d", failed.ID), nil, bundle.AccessToken), &failed)
			assert.Equal(t, 1, failed.Credited)
			assert.Zero(t, failed.Failed)
			require.NoError(t, model.DB.Model(&model.MarketingGift{}).Where("user_id = ?", first.Id).Count(&countAfter).Error)
			assert.Equal(t, countBefore+1, countAfter)
			var ledgerCount int64
			require.NotNil(t, failed.Recipients[0].Gift)
			require.NoError(t, model.DB.Model(&model.MarketingLedgerEntry{}).Where("gift_id = ? AND kind = ?", failed.Recipients[0].Gift.ID, "gift_credit").Count(&ledgerCount).Error)
			assert.EqualValues(t, 1, ledgerCount)
		})
		// Current role is checked even for an existing operation-key replay.
		require.NoError(t, model.DB.Model(admin).Update("role", common.RoleCommonUser).Error)
		assert.Equal(t, http.StatusForbidden, request("POST", "/gift-batches", input, bundle.AccessToken).Code)
		require.NoError(t, model.DB.Model(admin).Update("role", common.RoleAdminUser).Error)
	})
	// Exercise an existing new-system activity table that predates the indexed
	// projection. Restart migration twice without changing authoritative specs.
	var beforeMigration model.MarketingActivity
	require.NoError(t, model.DB.First(&beforeMigration, activity.ID).Error)
	// A real upgrade restarts the application. Discard idle driver statement
	// caches before altering SELECT * result columns in this same-process test.
	connection, err := model.DB.DB()
	require.NoError(t, err)
	connection.SetMaxIdleConns(0)
	require.NoError(t, model.DB.Migrator().DropIndex(&model.MarketingActivity{}, "idx_marketing_v2_activities_exclusive_group"))
	require.NoError(t, model.DB.Migrator().DropColumn(&model.MarketingActivity{}, "ExclusiveGroup"))
	require.NoError(t, model.DB.Migrator().DropColumn(&model.MarketingActivity{}, "AdminArchivedAt"))
	require.NoError(t, model.DB.Migrator().DropColumn(&model.MarketingLotteryPool{}, "AdminArchivedAt"))
	require.NoError(t, model.DB.Migrator().DropTable(&model.MarketingAdminCatalog{}))
	var poolBefore model.MarketingLotteryPool
	require.NoError(t, model.DB.First(&poolBefore, pool.ID).Error)
	for range 2 {
		require.NoError(t, model.DB.AutoMigrate(&model.MarketingAdminCatalog{}, &model.MarketingActivity{}, &model.MarketingLotteryPool{}))
		require.NoError(t, model.BackfillMarketingAdminGroups(model.DB))
	}
	var afterMigration model.MarketingActivity
	require.NoError(t, model.DB.First(&afterMigration, activity.ID).Error)
	var poolAfter model.MarketingLotteryPool
	require.NoError(t, model.DB.First(&poolAfter, pool.ID).Error)
	assert.Equal(t, poolBefore, poolAfter)
	assert.Zero(t, afterMigration.AdminArchivedAt)
	assert.True(t, model.DB.Migrator().HasIndex(&model.MarketingActivity{}, "idx_marketing_v2_activities_admin_archived_at"))
	assert.True(t, model.DB.Migrator().HasIndex(&model.MarketingLotteryPool{}, "idx_marketing_v2_lottery_pools_admin_archived_at"))
	assert.Equal(t, beforeMigration.Spec, afterMigration.Spec)
	assert.Equal(t, "shared", afterMigration.ExclusiveGroup)
	assert.True(t, model.DB.Migrator().HasIndex(&model.MarketingActivity{}, "idx_marketing_v2_activities_exclusive_group"))
	// Scoped management tokens do not silently become browser administrator sessions.
	scoped, _ := createScopedAccessToken(t, admin.Id, time.Now().Unix()+3600, "profile:read")
	response = request("GET", "/activities", nil, scoped)
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, response.Code)
	// Database role recheck is exercised after a successful mutation, using the
	// same token and operation key; stale authorization never blesses a replay.
	require.NoError(t, model.DB.Model(admin).Update("role", common.RoleCommonUser).Error)
	assert.Equal(t, http.StatusForbidden, request("POST", "/pools", poolInput, bundle.AccessToken).Code)
	assert.Equal(t, http.StatusForbidden, request("GET", "/activities", nil, bundle.AccessToken).Code)
	require.NoError(t, model.DB.Model(admin).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, model.DB.Model(admin).Update("status", common.UserStatusDisabled).Error)
	response = request("POST", "/activities", activityInput, bundle.AccessToken)
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, response.Code)
}
