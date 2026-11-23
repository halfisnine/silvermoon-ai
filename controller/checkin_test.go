package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/marketing"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMarketingCheckinHTTP(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	models := append(marketingRegistrationFixtureModels(), &model.MarketingCheckinDay{}, &model.MarketingCheckinClaim{}, &model.Checkin{})
	require.NoError(t, model.DB.AutoMigrate(models...))
	require.NoError(t, model.DB.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
	oldUnits, oldExchange := common.QuotaPerUnit, operation_setting.USDExchangeRate
	oldGeneral, oldCheckin := *operation_setting.GetGeneralSetting(), *operation_setting.GetCheckinSetting()
	common.QuotaPerUnit, operation_setting.USDExchangeRate = 50000, 2
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	operation_setting.GetCheckinSetting().Enabled = true
	operation_setting.GetCheckinSetting().MinQuota = 900000
	operation_setting.GetCheckinSetting().MaxQuota = 900000
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate = oldUnits, oldExchange
		*operation_setting.GetGeneralSetting() = oldGeneral
		*operation_setting.GetCheckinSetting() = oldCheckin
	})
	admin := model.User{Username: "checkin-admin", AffCode: "checkin-admin", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&admin).Error)
	now := time.Now().In(marketing.ShanghaiCalendar)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, marketing.ShanghaiCalendar).AddDate(0, 0, -2).Unix()
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("created_at", start).Error)
	var activity *model.MarketingActivity
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		activity, err = model.CreateMarketingActivity(tx, admin.Id, "checkin-http-create", model.MarketingActivitySpec{
			Published: true, Name: "daily-and-streak", Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: now.AddDate(0, 0, 4).Unix(),
			Rewards: []model.MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", Money: model.MarketingMoneySnapshot{Amount: "2", Currency: "CNY", QuotaPerUnit: "70000", CurrencyPerUnit: "1"}, ValidSeconds: 3600}},
			Checkin: &model.MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 2, MakeupLimit: 1, MakeupDaily: false, Milestones: []model.MarketingActivityMilestone{{Days: 1, Rewards: []model.MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", Money: model.MarketingMoneySnapshot{Amount: "3", Currency: "CNY", QuotaPerUnit: "70000", CurrencyPerUnit: "1"}}}}}},
		}, start)
		if err != nil {
			return err
		}
		_, err = model.ActivateMarketingActivity(tx, admin.Id, activity.ID, "checkin-http-enable", start)
		return err
	}))
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "checkin-http")
	require.NoError(t, err)
	router := gin.New()
	router.Use(middleware.UserAuth())
	router.GET("/api/user/checkin", GetCheckinStatus)
	router.POST("/api/user/checkin", DoCheckin)
	router.POST("/api/user/checkin/makeup", DoMakeupCheckin)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
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
	assert.Equal(t, http.StatusUnauthorized, request("POST", "/api/user/checkin", "{}", "").Code, "cookies cannot authenticate a check-in")
	var facts int64
	require.NoError(t, model.DB.Model(&model.MarketingActivityFact{}).Count(&facts).Error)
	assert.Zero(t, facts)
	response := request("GET", "/api/user/checkin", "", bundle.AccessToken)
	var calendar struct {
		Success bool `json:"success"`
		Data    struct {
			Activities []marketingCheckinActivityView `json:"activities"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &calendar))
	require.True(t, calendar.Success, response.Body.String())
	require.Len(t, calendar.Data.Activities, 1)
	assert.Equal(t, "5.6", calendar.Data.Activities[0].DailyRewards[0].Amount)
	assert.Equal(t, "CNY", calendar.Data.Activities[0].DailyRewards[0].Currency)
	for _, hidden := range []string{"quota", "QuotaPerUnit", "CurrencyPerUnit", "ThresholdCents", "qualification_day"} {
		assert.NotContains(t, response.Body.String(), hidden)
	}
	assert.Contains(t, calendar.Data.Activities[0].MakeupDates, now.AddDate(0, 0, -1).Format("2006-01-02"))
	for attempt := range 2 {
		response = request("POST", "/api/user/checkin", `{"user_id":99999,"date":"1999-01-01","now":1,"activity_id":99999,"quota":9999999}`, bundle.AccessToken)
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				Date    string `json:"date"`
				Already bool   `json:"already_recorded"`
				Pending bool   `json:"rewards_pending"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success, response.Body.String())
		assert.Equal(t, now.Format("2006-01-02"), result.Data.Date)
		assert.Equal(t, attempt > 0, result.Data.Already)
		assert.False(t, result.Data.Pending)
	}
	var gifts []model.MarketingGift
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).Find(&gifts).Error)
	require.Len(t, gifts, 2)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Zero(t, stored.Quota)
	var oldDays int64
	require.NoError(t, model.DB.Model(&model.Checkin{}).Count(&oldDays).Error)
	assert.Zero(t, oldDays)
	for _, body := range []string{`{}`, `{"date":"2026-02-30"}`, `{"date":"` + now.Format("2006-01-02") + `"}`, `{"date":"` + now.AddDate(0, 0, 1).Format("2006-01-02") + `"}`, `{"date":"2026-11-22"} {}`, strings.Repeat("x", 1025)} {
		response = request("POST", "/api/user/checkin/makeup", body, bundle.AccessToken)
		var result struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		assert.False(t, result.Success, response.Body.String())
	}
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	response = request("POST", "/api/user/checkin/makeup", `{"date":"`+yesterday+`","user_id":99999}`, bundle.AccessToken)
	var result struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	var days []model.MarketingCheckinDay
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).Find(&days).Error)
	assert.Len(t, days, 2)
	gifts = nil
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).Find(&gifts).Error)
	assert.Len(t, gifts, 2, "makeup without daily award must not repeat milestone")
	response = request("GET", "/api/user/checkin?month=2026-99", "", bundle.AccessToken)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.False(t, result.Success)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
	assert.Equal(t, http.StatusUnauthorized, request("POST", "/api/user/checkin/makeup", `{"date":"`+yesterday+`"}`, bundle.AccessToken).Code)
}
