package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPaidRedemptionAmountsUseTrustedSettings(t *testing.T) {
	previousSettings := *operation_setting.GetGeneralSetting()
	previousQuota, previousRate := common.QuotaPerUnit, operation_setting.USDExchangeRate
	t.Cleanup(func() {
		*operation_setting.GetGeneralSetting() = previousSettings
		common.QuotaPerUnit, operation_setting.USDExchangeRate = previousQuota, previousRate
	})
	common.QuotaPerUnit = 500000
	operation_setting.USDExchangeRate = 2.5
	for _, tc := range []struct {
		mode, currency, amount string
		customRate             float64
		quota                  int64
	}{
		{"CNY", "CNY", "10", 1, 2000000},
		{"USD", "USD", "10", 1, 5000000},
		{"CUSTOM", "¤", "6", 3, 1000000},
		{"TOKENS", "USD", "10", 1, 5000000},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			settings := operation_setting.GetGeneralSetting()
			settings.QuotaDisplayType = tc.mode
			settings.CustomCurrencySymbol = "¤"
			settings.CustomCurrencyExchangeRate = tc.customRate
			configuration, err := service.CurrentMarketingMoneyContext()
			require.NoError(t, err)
			spec, err := (paidRedemptionRequest{
				Name: "Paid card", PaidAmountCNY: "0.01", CreditAmount: tc.amount,
				CreditCurrency: tc.currency, MoneyRevision: configuration.Revision, SalesChannel: "shop", SalesBatch: "november",
			}).saleSpec()
			require.NoError(t, err)
			assert.EqualValues(t, 1, spec.PaidCents, "sale proceeds never come from the wallet conversion")
			assert.Equal(t, tc.currency, spec.Money.Currency)
			quota, err := spec.Money.Quota()
			require.NoError(t, err)
			assert.Equal(t, tc.quota, quota)
			common.QuotaPerUnit = 17
			frozen, err := spec.Money.Quota()
			require.NoError(t, err)
			assert.Equal(t, quota, frozen, "later settings cannot reinterpret the code credit")
			common.QuotaPerUnit = 500000
		})
	}
	for _, amount := range []string{"0", "-1", "0.001", "1.001", "1e2", "NaN", "Inf", " 1", "1 ", "90071992547409.92"} {
		_, err := service.ParseMarketingPaidCNY(amount)
		assert.Error(t, err, amount)
	}
	maxCents, err := service.ParseMarketingPaidCNY("90071992547409.91")
	require.NoError(t, err)
	assert.EqualValues(t, common.MaxWalletQuota, maxCents)
	settings := operation_setting.GetGeneralSetting()
	settings.QuotaDisplayType = "CNY"
	_, err = (paidRedemptionRequest{Name: "Card", PaidAmountCNY: "1", CreditAmount: "10", CreditCurrency: "USD"}).saleSpec()
	require.Error(t, err, "stale browser currency cannot silently change the credited amount")

	configuration, err := service.CurrentMarketingMoneyContext()
	require.NoError(t, err)
	request := paidRedemptionRequest{Name: "Paid", PaidAmountCNY: "1", CreditAmount: "2", CreditCurrency: "CNY", MoneyRevision: configuration.Revision, SalesChannel: "shop", SalesBatch: "batch"}
	operation_setting.USDExchangeRate = 3
	_, err = request.saleSpec()
	require.Error(t, err, "same currency with changed exchange rate must reject the stale editor")
	configuration, err = service.CurrentMarketingMoneyContext()
	require.NoError(t, err)
	request.MoneyRevision = configuration.Revision
	common.QuotaPerUnit = 100000
	_, err = request.saleSpec()
	require.Error(t, err, "changed accounting conversion must reject the stale editor")
	operation_setting.USDExchangeRate = 0
	_, err = service.MarketingDisplayMoney("10")
	require.Error(t, err, "invalid conversion cannot default to a fabricated rate")
}

func TestDeleteRedemptionBatch(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver, logDriver gorm.Dialector
			dbType := common.DatabaseTypeSQLite
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
				logDriver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
				logDSN := os.Getenv("TEST_MYSQL_LOG_DSN")
				if logDSN == "" {
					logDSN = dsn
				}
				logDriver = mysql.Open(logDSN)
				dbType = common.DatabaseTypeMySQL
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
				logDSN := os.Getenv("TEST_POSTGRES_LOG_DSN")
				if logDSN == "" {
					logDSN = dsn
				}
				logDriver = postgres.Open(logDSN)
				dbType = common.DatabaseTypePostgreSQL
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)

			logDB, err := gorm.Open(logDriver, &gorm.Config{})
			require.NoError(t, err)
			logSQL, err := logDB.DB()
			require.NoError(t, err)
			logSQL.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, logSQL.Close()) })
			previousDB, previousLogDB := model.DB, model.LOG_DB
			previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
			previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret
			model.DB, model.LOG_DB = db, logDB
			common.SetDatabaseTypes(dbType, dbType)
			common.RedisEnabled = false
			common.SessionSecret = "redemption-batch-session-fixture"
			t.Cleanup(func() {
				model.DB, model.LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMain, previousLog)
				common.RedisEnabled = previousRedis
				common.SessionSecret = previousSecret
			})
			for _, table := range []any{&model.User{}, &model.Redemption{}, &model.UserSession{}} {
				require.False(t, db.Migrator().HasTable(table), "use an empty test database")
				require.NoError(t, db.AutoMigrate(table))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
			}
			require.False(t, logDB.Migrator().HasTable(&model.AuditLog{}), "use an empty test log database")
			require.NoError(t, logDB.AutoMigrate(&model.AuditLog{}))
			t.Cleanup(func() { require.NoError(t, logDB.Migrator().DropTable(&model.AuditLog{})) })
			admin := model.User{Username: "redemption-audit-admin", Password: "unused", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
			require.NoError(t, db.Create(&admin).Error)
			bundle, err := service.CreateLoginSession(admin.Id, "password", "127.0.0.1", "redemption-batch-test")
			require.NoError(t, err)
			token := bundle.AccessToken
			codes := make([]model.Redemption, 16)
			for index := range codes {
				codes[index] = model.Redemption{Name: "selected", Key: fmt.Sprintf("%032d", index+1), Quota: 100, Status: common.RedemptionCodeStatusEnabled}
			}
			codes[1].Status = common.RedemptionCodeStatusUsed
			codes[15].Name = "unselected"
			codes[15].Status = common.RedemptionCodeStatusDisabled
			require.NoError(t, model.DB.Create(&codes).Error)
			router := gin.New()
			router.Use(middleware.RequestId())
			router.POST("/api/redemption/batch", middleware.AdminAuth(), DeleteRedemptionBatch)

			t.Run("display_and_current_authorization", func(t *testing.T) {
				previousSettings := *operation_setting.GetGeneralSetting()
				previousQuota, previousRate := common.QuotaPerUnit, operation_setting.USDExchangeRate
				t.Cleanup(func() {
					*operation_setting.GetGeneralSetting() = previousSettings
					common.QuotaPerUnit, operation_setting.USDExchangeRate = previousQuota, previousRate
				})
				common.QuotaPerUnit, operation_setting.USDExchangeRate = 500000, 2.5
				operation_setting.GetGeneralSetting().QuotaDisplayType = "CNY"
				original := model.MarketingMoneySnapshot{Amount: "0.0002", Currency: "CNY", QuotaPerUnit: "500000", CurrencyPerUnit: "1"}
				snapshot, err := common.Marshal(original)
				require.NoError(t, err)
				require.NoError(t, db.Model(&codes[0]).Updates(map[string]any{"paid_cents": 1, "credited_conversion": string(snapshot), "sales_channel": "shop", "sales_batch": "launch", "sales_evidence": "receipt"}).Error)
				readRouter := gin.New()
				// A stale session's cached admin role must never authorize the read.
				readRouter.GET("/codes/:id", func(c *gin.Context) { c.Set("id", admin.Id); c.Set("role", common.RoleAdminUser) }, GetRedemption)
				response := httptest.NewRecorder()
				readRouter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/codes/%d", codes[0].Id), nil))
				var result struct {
					Success bool               `json:"success"`
					Data    paidRedemptionView `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				require.True(t, result.Success, response.Body.String())
				assert.Equal(t, "0.01", result.Data.PaidAmountCNY)
				assert.Equal(t, "0.0005", result.Data.CreditAmount)
				assert.Equal(t, "0.0002", result.Data.OriginalCreditAmount)
				assert.True(t, result.Data.SaleRegistered)
				assert.Equal(t, "receipt", result.Data.SalesEvidence)
				assert.NotEmpty(t, result.Data.MoneyRevision)
				var envelope map[string]any
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
				fields := envelope["data"].(map[string]any)
				for _, hidden := range []string{"quota", "paid_cents", "payment_id", "credited_conversion", "user_id", "used_user_id"} {
					assert.NotContains(t, fields, hidden)
				}
				require.NoError(t, db.Model(&admin).Update("role", common.RoleCommonUser).Error)
				response = httptest.NewRecorder()
				readRouter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/codes/%d", codes[0].Id), nil))
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.False(t, result.Success)
				require.NoError(t, db.Model(&admin).Update("role", common.RoleAdminUser).Error)
			})
			overLimit := make([]int, 1001)
			for index := range overLimit {
				overLimit[index] = codes[0].Id
			}
			oversized, err := common.Marshal(map[string]any{"ids": overLimit})
			require.NoError(t, err)
			for _, body := range []string{"{}", `{"ids":[]}`, `{"ids":null}`, `{"ids":[0]}`, `{"ids":[1,-1]}`, `{"ids":["1"]}`, "{", string(oversized)} {
				t.Run("invalid_"+body[:min(len(body), 30)], func(t *testing.T) {
					response := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodPost, "/api/redemption/batch", bytes.NewBufferString(body))
					request.Header.Set("Authorization", "Bearer "+token)
					router.ServeHTTP(response, request)
					var result struct {
						Success bool `json:"success"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
					assert.False(t, result.Success)
					var count int64
					require.NoError(t, model.DB.Model(&model.Redemption{}).Count(&count).Error)
					assert.EqualValues(t, 16, count)
					var events []model.AuditLog
					require.NoError(t, logDB.Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).Find(&events).Error)
					require.Len(t, events, 1)
					assert.False(t, events[0].Success)
					assert.Equal(t, "redemption.delete_batch", events[0].Action)
				})
			}
			err = model.WithMarketingTransaction(t.Context(), func(tx *gorm.DB) error {
				_, err := model.DeletePaidRedemptions(tx, admin.Id, nil)
				return err
			})
			require.Error(t, err)
			requestedIDs := make([]int, 0, 17)
			for _, code := range codes[:15] {
				requestedIDs = append(requestedIDs, code.Id)
			}
			requestedIDs = append(requestedIDs, codes[0].Id, 999999)
			payload, err := common.Marshal(map[string]any{"ids": requestedIDs})
			require.NoError(t, err)
			for _, expectedCount := range []int64{15, 0} {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/redemption/batch", bytes.NewReader(payload))
				request.Header.Set("Authorization", "Bearer "+token)
				router.ServeHTTP(response, request)
				assert.Equal(t, http.StatusOK, response.Code)
				var result struct {
					Success bool  `json:"success"`
					Data    int64 `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.True(t, result.Success)
				assert.Equal(t, expectedCount, result.Data)
				var events []model.AuditLog
				require.NoError(t, logDB.Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).Find(&events).Error)
				require.Len(t, events, 1, "one operation event, without a duplicate single-delete fallback")
				event := events[0]
				assert.Equal(t, "redemption.delete_batch", event.Action)
				assert.Equal(t, fmt.Sprintf("Batch deleted %d redemption codes", expectedCount), event.Content)
				assert.True(t, event.Success)
				assert.Equal(t, admin.Id, event.UserId)
				assert.Equal(t, "/api/redemption/batch", event.Route)
				require.NotNil(t, event.Other.Op)
				encoded, err := common.Marshal(event.Other.Op.Params)
				require.NoError(t, err)
				var params struct {
					Count int64 `json:"count"`
					Total int   `json:"total"`
					IDs   []int `json:"requested_redemption_ids"`
				}
				require.NoError(t, common.Unmarshal(encoded, &params))
				assert.Equal(t, expectedCount, params.Count)
				assert.Equal(t, len(requestedIDs), params.Total)
				assert.Equal(t, requestedIDs, params.IDs)
				encoded, err = common.Marshal(event)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), token)
				for _, code := range codes {
					assert.NotContains(t, string(encoded), code.Key)
				}
			}
			var active []model.Redemption
			require.NoError(t, model.DB.Find(&active).Error)
			require.Len(t, active, 1)
			assert.Equal(t, codes[15], active[0])
			var all []model.Redemption
			require.NoError(t, model.DB.Unscoped().Order("id").Find(&all).Error)
			require.Len(t, all, 16)
			for _, code := range all[:15] {
				assert.True(t, code.DeletedAt.Valid)
			}
			assert.False(t, all[15].DeletedAt.Valid)
		})
	}
}

func TestPaidWalletRejectsUnrecordedBalanceEdits(t *testing.T) {
	db := setupManageUserTestDB(t)
	user := model.User{Username: "paid-wallet-managed", Quota: 1000, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaidReserved: 100}).Error)
	for _, mode := range []string{"add", "subtract", "override"} {
		_, err := model.AdjustUserQuota(user.Id, common.RoleRootUser, mode, 200)
		require.ErrorIs(t, err, model.ErrMarketingBalanceEdit, mode)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 1000, user.Quota, "an old editor must not mint or erase paid funds")
		var account model.MarketingAccount
		require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
		assert.EqualValues(t, 100, account.PaidReserved)
	}
}
