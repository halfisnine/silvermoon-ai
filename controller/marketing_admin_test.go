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
	models := append(marketingRegistrationFixtureModels(), &model.Redemption{}, &model.TopUp{})
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
	for range 2 {
		require.NoError(t, model.DB.AutoMigrate(&model.MarketingActivity{}))
		require.NoError(t, model.BackfillMarketingAdminGroups(model.DB))
	}
	var afterMigration model.MarketingActivity
	require.NoError(t, model.DB.First(&afterMigration, activity.ID).Error)
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
