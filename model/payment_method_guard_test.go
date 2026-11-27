package model

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func insertUserForPaymentGuardTest(t *testing.T, id int, quota int) *User {
	t.Helper()
	require.NoError(t, migrateMarketingV2(DB))
	cleanup := func() {
		for _, record := range []any{&MarketingActivityDeliveryCursor{}, &MarketingActivityReward{}, &MarketingActivityDecision{}, &MarketingActivityProgress{}, &MarketingActivityFact{}, &MarketingGift{}, &MarketingLedgerEntry{}, &MarketingPayment{}, &MarketingAccount{}} {
			require.NoError(t, DB.Where("user_id = ?", id).Delete(record).Error)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	user := &User{
		Id:       id,
		Username: "payment_guard_user",
		Status:   common.UserStatusEnabled,
		Quota:    quota,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func insertSubscriptionPlanForPaymentGuardTest(t *testing.T, id int) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Id:            id,
		Title:         "Guard Plan",
		PriceAmount:   9.99,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	return plan
}

func insertSubscriptionOrderForPaymentGuardTest(t *testing.T, tradeNo string, userID int, planID int, paymentProvider string) {
	t.Helper()
	order := &SubscriptionOrder{
		UserId:          userID,
		PlanId:          planID,
		Money:           9.99,
		TradeNo:         tradeNo,
		PaymentMethod:   paymentProvider,
		PaymentProvider: paymentProvider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, DB.Create(order).Error)
}

func insertTopUpForPaymentGuardTest(t *testing.T, tradeNo string, userID int, paymentProvider string) {
	t.Helper()
	topUp := &TopUp{
		UserId:          userID,
		Amount:          2,
		Money:           9.99,
		TradeNo:         tradeNo,
		PaymentMethod:   paymentProvider,
		PaymentProvider: paymentProvider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, topUp.Insert())
}

func getTopUpStatusForPaymentGuardTest(t *testing.T, tradeNo string) string {
	t.Helper()
	topUp := GetTopUpByTradeNo(tradeNo)
	require.NotNil(t, topUp)
	return topUp.Status
}

func countUserSubscriptionsForPaymentGuardTest(t *testing.T, userID int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", userID).Count(&count).Error)
	return count
}

func getUserQuotaForPaymentGuardTest(t *testing.T, userID int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").Where("id = ?", userID).First(&user).Error)
	return user.Quota
}

func TestRechargeWaffoPancake_RejectsMismatchedPaymentMethod(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 101, 0)
	insertTopUpForPaymentGuardTest(t, "waffo-pancake-guard", 101, PaymentProviderStripe)

	err := RechargeWaffoPancake("waffo-pancake-guard")
	require.Error(t, err)

	topUp := GetTopUpByTradeNo("waffo-pancake-guard")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
	assert.Equal(t, 0, getUserQuotaForPaymentGuardTest(t, 101))
}

func TestUpdatePendingTopUpStatus_RejectsMismatchedPaymentProvider(t *testing.T) {
	testCases := []struct {
		name                    string
		tradeNo                 string
		storedPaymentProvider   string
		expectedPaymentProvider string
		targetStatus            string
	}{
		{
			name:                    "stripe expire",
			tradeNo:                 "stripe-expire-guard",
			storedPaymentProvider:   PaymentProviderCreem,
			expectedPaymentProvider: PaymentProviderStripe,
			targetStatus:            common.TopUpStatusExpired,
		},
		{
			name:                    "waffo failed",
			tradeNo:                 "waffo-failed-guard",
			storedPaymentProvider:   PaymentProviderStripe,
			expectedPaymentProvider: PaymentProviderWaffo,
			targetStatus:            common.TopUpStatusFailed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			insertUserForPaymentGuardTest(t, 150, 0)
			insertTopUpForPaymentGuardTest(t, tc.tradeNo, 150, tc.storedPaymentProvider)

			err := UpdatePendingTopUpStatus(tc.tradeNo, tc.expectedPaymentProvider, tc.targetStatus)
			require.ErrorIs(t, err, ErrPaymentMethodMismatch)
			assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, tc.tradeNo))
		})
	}
}

func TestCompleteSubscriptionOrder_RejectsAllProvidersWithoutMutation(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 202, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 301)
	insertSubscriptionOrderForPaymentGuardTest(t, "sub-guard-order", 202, plan.Id, PaymentProviderStripe)

	err := CompleteSubscriptionOrder("sub-guard-order", `{"provider":"epay"}`, PaymentProviderEpay, "alipay")
	require.ErrorIs(t, err, ErrSubscriptionsUnavailable)

	require.ErrorIs(t, CompleteSubscriptionOrder("sub-guard-order", `{"provider":"stripe"}`, PaymentProviderStripe, "stripe"), ErrSubscriptionsUnavailable)
	order := GetSubscriptionOrderByTradeNo("sub-guard-order")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
	assert.Zero(t, countUserSubscriptionsForPaymentGuardTest(t, 202))

	topUp := GetTopUpByTradeNo("sub-guard-order")
	assert.Nil(t, topUp)
}

func TestExpireSubscriptionOrder_RejectsMismatchedPaymentProvider(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 303, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 401)
	insertSubscriptionOrderForPaymentGuardTest(t, "sub-expire-guard", 303, plan.Id, PaymentProviderStripe)

	err := ExpireSubscriptionOrder("sub-expire-guard", PaymentProviderCreem)
	require.ErrorIs(t, err, ErrPaymentMethodMismatch)

	order := GetSubscriptionOrderByTradeNo("sub-expire-guard")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
}

func createEpayTestOrder(t *testing.T, userId int, tradeNo string, provider string, status string) TopUp {
	t.Helper()
	topUp := TopUp{
		UserId:          userId,
		Amount:          2,
		Money:           10.0,
		TradeNo:         tradeNo,
		PaymentMethod:   "alipay",
		PaymentProvider: provider,
		CreateTime:      common.GetTimestamp(),
		Status:          status,
	}
	if provider == PaymentProviderEpay {
		topUp.Status = common.TopUpStatusPending
		require.NoError(t, topUp.FreezeEpayTopUp(MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64), CurrencyPerUnit: "1", Currency: "USD"}, 1000, "test-merchant"))
		topUp.Status = status
	}
	require.NoError(t, DB.Create(&topUp).Error)
	return topUp
}

func TestRechargeEpayCreditsQuotaExactlyOnce(t *testing.T) {
	truncateTables(t)

	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	user := insertUserForPaymentGuardTest(t, 501, 0)
	order := createEpayTestOrder(t, user.Id, "EPAYTESTONCE", PaymentProviderEpay, common.TopUpStatusPending)

	alreadyDone, err := RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), "127.0.0.1")
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	assert.Equal(t, 2*500000, getUserQuotaForPaymentGuardTest(t, user.Id))

	reloaded := GetTopUpByTradeNo(order.TradeNo)
	require.NotNil(t, reloaded)
	assert.Equal(t, common.TopUpStatusSuccess, reloaded.Status)
	assert.NotZero(t, reloaded.CompleteTime)

	alreadyDone, err = RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), "127.0.0.1")
	require.NoError(t, err)
	assert.True(t, alreadyDone)
	assert.Equal(t, 2*500000, getUserQuotaForPaymentGuardTest(t, user.Id))
}

func TestRechargeEpayKeepsRedisAndDatabaseCreditInSync(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 5
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	user := insertUserForPaymentGuardTest(t, 502, 7)
	require.NoError(t, populateUserCache(*user))
	order := createEpayTestOrder(t, user.Id, "EPAYTESTREDISSYNC", PaymentProviderEpay, common.TopUpStatusPending)

	alreadyDone, err := RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), "127.0.0.1")
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	assert.Equal(t, 17, getUserQuotaForPaymentGuardTest(t, user.Id))
	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 17, cached.Quota)

	alreadyDone, err = RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), "127.0.0.1")
	require.NoError(t, err)
	assert.True(t, alreadyDone)
	cached, err = cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 17, cached.Quota)
}

func TestRechargeEpayUpdatesPaymentMethodToActual(t *testing.T) {
	truncateTables(t)

	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	user := insertUserForPaymentGuardTest(t, 503, 0)
	order := createEpayTestOrder(t, user.Id, "EPAYTESTMETHOD", PaymentProviderEpay, common.TopUpStatusPending)

	alreadyDone, err := RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "wxpay"), "127.0.0.1")
	require.NoError(t, err)
	assert.False(t, alreadyDone)

	reloaded := GetTopUpByTradeNo(order.TradeNo)
	require.NotNil(t, reloaded)
	assert.Equal(t, "wxpay", reloaded.PaymentMethod)
	assert.Equal(t, 2*500000, getUserQuotaForPaymentGuardTest(t, user.Id))
}

func TestRechargeEpayRejectsForeignAndNonPendingOrders(t *testing.T) {
	truncateTables(t)

	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	user := insertUserForPaymentGuardTest(t, 504, 7)

	t.Run("order from another payment provider", func(t *testing.T) {
		order := createEpayTestOrder(t, user.Id, "EPAYTESTSTRIPE", PaymentProviderStripe, common.TopUpStatusPending)
		_, err := RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), "127.0.0.1")
		assert.ErrorIs(t, err, ErrPaymentMethodMismatch)
		assert.Equal(t, 7, getUserQuotaForPaymentGuardTest(t, user.Id))
	})

	t.Run("order that is not pending", func(t *testing.T) {
		order := createEpayTestOrder(t, user.Id, "EPAYTESTEXPIRED", PaymentProviderEpay, common.TopUpStatusExpired)
		_, err := RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), "127.0.0.1")
		assert.ErrorIs(t, err, ErrTopUpStatusInvalid)
		assert.Equal(t, 7, getUserQuotaForPaymentGuardTest(t, user.Id))
	})

	t.Run("missing order", func(t *testing.T) {
		_, err := RechargeEpay("EPAYTESTMISSING", epayTestReceipt("EPAYTESTMISSING", "alipay"), "127.0.0.1")
		assert.ErrorIs(t, err, ErrTopUpNotFound)
	})
}

func TestRechargeEpayRejectsQuotaOverflowBeforeCompletingOrder(t *testing.T) {
	truncateTables(t)
	user := insertUserForPaymentGuardTest(t, 505, 3)
	order := TopUp{UserId: user.Id, Amount: 2, TradeNo: "EPAYTESTOVERFLOW", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending, CreateTime: 100}
	require.ErrorIs(t, order.FreezeEpayTopUp(MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: strconv.FormatInt(common.MaxWalletQuota+1, 10), CurrencyPerUnit: "1", Currency: "USD"}, 1000, "test-merchant"), ErrInvalidTopUpQuota)
	require.NoError(t, DB.Create(&order).Error) // Corrupt/legacy data must also fail at settlement.
	_, err := RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), "127.0.0.1")
	require.Error(t, err)
	assert.Equal(t, 3, getUserQuotaForPaymentGuardTest(t, user.Id))
	assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, order.TradeNo))
}

func TestRechargeEpayEnforcesFinalWalletQuotaLimit(t *testing.T) {
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	testCases := []struct {
		name         string
		currentQuota int
		wantErr      bool
		wantQuota    int
		wantStatus   string
	}{
		{
			name:         "allows exact highest representable wallet balance",
			currentQuota: common.MaxWalletQuota - 1_000_000,
			wantQuota:    common.MaxWalletQuota,
			wantStatus:   common.TopUpStatusSuccess,
		},
		{
			name:         "rejects balance above wallet quota domain",
			currentQuota: common.MaxWalletQuota - 999_999,
			wantErr:      true,
			wantQuota:    common.MaxWalletQuota - 999_999,
			wantStatus:   common.TopUpStatusPending,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			user := insertUserForPaymentGuardTest(t, 506, tc.currentQuota)
			order := createEpayTestOrder(t, user.Id, "EPAYTESTWALLETLIMIT", PaymentProviderEpay, common.TopUpStatusPending)

			_, err := RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), "127.0.0.1")
			if tc.wantErr {
				require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantQuota, getUserQuotaForPaymentGuardTest(t, user.Id))
			assert.Equal(t, tc.wantStatus, getTopUpStatusForPaymentGuardTest(t, order.TradeNo))
		})
	}
}

func epayTestReceipt(tradeNo, method string) EpayTopUpReceipt {
	return EpayTopUpReceipt{PaidCents: 1000, MerchantID: "test-merchant", PaymentMethod: method, ProviderReference: "provider:" + tradeNo}
}

func TestEpayMarketing(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := "local"
			if dialect != "sqlite" {
				dsn = os.Getenv("MV2_" + strings.ToUpper(dialect) + "_DSN")
			}
			if dsn == "" {
				if os.Getenv("MV2_REQUIRE_DATABASES") == "1" {
					t.Fatal("required database missing: " + dialect)
				}
				t.Skip("matrix database not configured")
			}
			if dialect != "sqlite" {
				require.Contains(t, dsn, "/marketing_v2_behavior")
			}
			oldDB, oldLogDB, oldType, oldLogType, oldPath := DB, LOG_DB, common.MainDatabaseType(), common.LogDatabaseType(), common.SQLitePath
			common.SQLitePath = filepath.Join(t.TempDir(), "epay.sqlite")
			t.Cleanup(func() {
				DB = oldDB
				LOG_DB = oldLogDB
				common.SetMainDatabaseType(oldType)
				common.SetLogDatabaseType(oldLogType)
				common.SQLitePath = oldPath
				initCol()
			})
			t.Setenv("MV2_TEST_DSN", dsn)
			db, dbType, err := chooseDB("MV2_TEST_DSN", false)
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
			DB = db
			LOG_DB = db
			common.SetMainDatabaseType(dbType)
			common.SetLogDatabaseType(dbType)
			initCol()
			require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &Log{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				users := db.Unscoped().Model(&User{}).Select("id").Where("username LIKE ?", "mepay_%")
				for _, record := range []any{&Log{}, &MarketingActivityReward{}, &MarketingActivityDecision{}, &MarketingActivityProgress{}, &MarketingActivityFact{}, &MarketingGift{}, &MarketingLedgerEntry{}, &MarketingPayment{}, &MarketingAccount{}, &TopUp{}} {
					require.NoError(t, db.Where("user_id IN (?)", users).Delete(record).Error)
				}
				require.NoError(t, db.Where("actor_id IN (?)", users).Delete(&MarketingActivityOperation{}).Error)
				require.NoError(t, db.Where("name LIKE ?", "mepay_%").Delete(&MarketingActivity{}).Error)
				require.NoError(t, db.Unscoped().Where("username LIKE ?", "mepay_%").Delete(&User{}).Error)
			}
			cleanup()
			t.Cleanup(cleanup)
			userNumber := 0
			newUser := func(role int) User {
				userNumber++
				name := "mepay_" + strconv.Itoa(userNumber)
				user := User{Username: name, AffCode: name, Role: role, Status: common.UserStatusEnabled, CreatedAt: 1}
				require.NoError(t, db.Create(&user).Error)
				require.NoError(t, db.Create(&MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: MarketingPaymentHistoryNone, PaymentHistoryEvidence: "registration:" + name}).Error)
				return user
			}
			admin := newUser(common.RoleAdminUser)
			money := MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: "37", CurrencyPerUnit: "1", Currency: "USD"}
			create := func(userID int, name string) *TopUp {
				order := &TopUp{UserId: userID, Amount: 2, TradeNo: "mepay_" + name, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", CreateTime: 100, Status: common.TopUpStatusPending}
				require.NoError(t, order.FreezeEpayTopUp(money, 1000, "test-merchant"))
				require.NoError(t, order.Insert())
				return order
			}
			t.Run("callback_offsets_negative_balance_once", func(t *testing.T) {
				for _, opening := range []int{-100, -74, -40} {
					user := newUser(common.RoleCommonUser)
					require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", opening).Error)
					require.NoError(t, db.Model(&MarketingAccount{}).Where("user_id = ?", user.Id).Update("opening_quota", opening).Error)
					order := create(user.Id, "negative-"+strconv.Itoa(user.Id))
					require.NoError(t, ValidateTopUpQuotaCapacity(user.Id, 74))
					var first *EpayTopUpResult
					for i := range 2 {
						require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
							result, err := CreditEpayTopUp(tx, order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), 200)
							if err != nil {
								return err
							}
							assert.Equal(t, i == 1, result.AlreadyPosted)
							if first == nil {
								first = result
							} else {
								assert.Equal(t, first.PaymentID, result.PaymentID)
							}
							return nil
						}))
					}
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, opening+74, user.Quota)
					var consumed int64
					require.NoError(t, db.Model(&MarketingLedgerEntry{}).Where("user_id = ? AND kind = ?", user.Id, "consume").Count(&consumed).Error)
					assert.Zero(t, consumed, "paying an existing signed balance creates no duplicate consumption")
				}
			})
			t.Run("snapshot_receipt_replay_and_manual_evidence", func(t *testing.T) {
				user := newUser(common.RoleCommonUser)
				order := create(user.Id, "snapshot")
				originalRate := common.QuotaPerUnit
				common.QuotaPerUnit = 99999
				t.Cleanup(func() { common.QuotaPerUnit = originalRate })
				originalMoney := order.Money
				for _, bad := range []EpayTopUpReceipt{
					{PaidCents: 999, MerchantID: "test-merchant", PaymentMethod: "alipay", ProviderReference: "provider:" + order.TradeNo},
					{PaidCents: 1000, MerchantID: "foreign", PaymentMethod: "alipay", ProviderReference: "provider:" + order.TradeNo},
				} {
					require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := CreditEpayTopUp(tx, order.TradeNo, bad, 200); return err }), ErrEpayReceipt)
				}
				require.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
				require.Zero(t, getUserQuotaForPaymentGuardTest(t, user.Id))
				done, err := RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "wxpay"), "127.0.0.1")
				require.NoError(t, err)
				assert.False(t, done)
				done, err = RechargeEpay(order.TradeNo, epayTestReceipt(order.TradeNo, "wxpay"), "127.0.0.1")
				require.NoError(t, err)
				assert.True(t, done)
				assert.Equal(t, 74, getUserQuotaForPaymentGuardTest(t, user.Id))
				stored := GetTopUpByTradeNo(order.TradeNo)
				assert.Equal(t, originalMoney, stored.Money)
				assert.Equal(t, "wxpay", stored.PaymentMethod)
				var payment MarketingPayment
				require.NoError(t, db.First(&payment, stored.PaymentID).Error)
				assert.Equal(t, "online", payment.SourceKind)
				assert.Equal(t, "topup:"+strconv.Itoa(order.Id), payment.SourceReference)
				assert.EqualValues(t, 1000, payment.PaidCents)
				assert.EqualValues(t, 74, payment.CreditedQuota)
				assert.EqualValues(t, 1, payment.Sequence)
				assert.Equal(t, MarketingFirstPaymentConfirmed, payment.FirstStatus)
				assert.Equal(t, order.CreditedConversion, payment.Conversion)
				require.NoError(t, ManualCompleteTopUp(admin.Id, order.TradeNo, 1000, "confirmed bank receipt", "127.0.0.1"))
				require.ErrorIs(t, ManualCompleteTopUp(user.Id, order.TradeNo, 1000, "wrong actor", "127.0.0.1"), ErrMarketingAccess)
				require.Error(t, ManualCompleteTopUp(admin.Id, order.TradeNo, 1000, "", "127.0.0.1"))
				var logs int64
				require.NoError(t, db.Model(&Log{}).Where("user_id = ? AND type = ?", user.Id, LogTypeTopup).Count(&logs).Error)
				assert.EqualValues(t, 1, logs)
				next := create(user.Id, "manual")
				require.NoError(t, ManualCompleteTopUp(admin.Id, next.TradeNo, 1000, "receipt-bank-123", "127.0.0.1"))
				stored = GetTopUpByTradeNo(next.TradeNo)
				assert.Equal(t, admin.Id, stored.CompletionActorID)
				assert.Equal(t, "receipt-bank-123", stored.CompletionEvidence)
				public, err := common.Marshal(stored)
				require.NoError(t, err)
				assert.NotContains(t, string(public), "receipt-bank-123")
				assert.NotContains(t, string(public), "completion_evidence")
				assert.Empty(t, stored.ProviderReference)
				done, err = RechargeEpay(next.TradeNo, epayTestReceipt(next.TradeNo, "alipay"), "127.0.0.1")
				require.NoError(t, err)
				assert.True(t, done)
				stored = GetTopUpByTradeNo(next.TradeNo)
				assert.Equal(t, "provider:"+next.TradeNo, stored.ProviderReference)
				require.NoError(t, db.Model(&Log{}).Where("user_id = ? AND type = ?", user.Id, LogTypeTopup).Count(&logs).Error)
				assert.EqualValues(t, 2, logs)
				wrong := epayTestReceipt(next.TradeNo, "alipay")
				wrong.ProviderReference = "other-provider-payment"
				_, err = RechargeEpay(next.TradeNo, wrong, "")
				require.ErrorIs(t, err, ErrEpayReceipt)
				require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleCommonUser).Error)
				require.ErrorIs(t, ManualCompleteTopUp(admin.Id, next.TradeNo, 1000, "receipt-bank-123", ""), ErrMarketingAccess)
				require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
			})
			t.Run("atomic_rollback_legacy_snapshot_and_disabled_payer", func(t *testing.T) {
				user := newUser(common.RoleCommonUser)
				order := create(user.Id, "rollback")
				abort := errors.New("late failure")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreditEpayTopUp(tx, order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), 200)
					if err != nil {
						return err
					}
					return abort
				}), abort)
				stored := GetTopUpByTradeNo(order.TradeNo)
				assert.Equal(t, common.TopUpStatusPending, stored.Status)
				assert.Zero(t, stored.PaymentID)
				for _, record := range []any{&MarketingPayment{}, &MarketingLedgerEntry{}, &MarketingActivityFact{}} {
					var count int64
					require.NoError(t, db.Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
				}
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreditEpayTopUp(tx, order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), 201)
					return err
				}), "already paid cash remains accounted even when the payer is disabled")
				legacy := TopUp{UserId: user.Id, Amount: 2, Money: 10, TradeNo: "mepay_legacy", PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", CreateTime: 1, Status: common.TopUpStatusPending}
				require.NoError(t, db.Create(&legacy).Error)
				_, err := RechargeEpay(legacy.TradeNo, epayTestReceipt(legacy.TradeNo, "alipay"), "")
				require.ErrorIs(t, err, ErrEpaySnapshot)
				require.ErrorIs(t, ManualCompleteTopUp(admin.Id, legacy.TradeNo, 1000, "cash receipt", ""), ErrEpaySnapshot)
				assert.Equal(t, 74, getUserQuotaForPaymentGuardTest(t, user.Id))
			})
			t.Run("callback_and_manual_race_one_fact", func(t *testing.T) {
				user := newUser(common.RoleCommonUser)
				order := create(user.Id, "race")
				results := make([]*EpayTopUpResult, 2)
				failures := make([]error, 2)
				paidRedemptionConcurrentTest(t, user.Id, func(tx *gorm.DB, worker int) error {
					var err error
					if worker == 0 {
						results[worker], err = CreditEpayTopUp(tx, order.TradeNo, epayTestReceipt(order.TradeNo, "alipay"), 300)
					} else {
						results[worker], err = completeEpayTopUp(tx, order.TradeNo, EpayTopUpReceipt{PaidCents: 1000}, admin.Id, "verified cash receipt", 300)
					}
					return err
				}, failures)
				require.NoError(t, failures[0])
				require.NoError(t, failures[1])
				require.NotNil(t, results[0])
				require.NotNil(t, results[1])
				assert.Equal(t, results[0].PaymentID, results[1].PaymentID)
				assert.NotEqual(t, results[0].AlreadyPosted, results[1].AlreadyPosted)
				assert.Equal(t, 74, getUserQuotaForPaymentGuardTest(t, user.Id))
				for _, record := range []any{&MarketingPayment{}, &MarketingLedgerEntry{}, &MarketingActivityFact{}} {
					var count int64
					require.NoError(t, db.Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.EqualValues(t, 1, count)
				}
			})
			t.Run("callback_and_manual_posting_time_follows_locked_order_read", func(t *testing.T) {
				for _, manual := range []bool{false, true} {
					name := "callback_clock"
					actorID, evidence := 0, ""
					if manual {
						name, actorID, evidence = "manual_clock", admin.Id, "verified bank payment"
					}
					user := newUser(common.RoleCommonUser)
					order := create(user.Id, name)
					var activity *MarketingActivity
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						activity, err = CreateMarketingActivity(tx, admin.Id, "mepay:clock:"+name, MarketingActivitySpec{Name: "mepay_" + name, Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 1, StartsAt: 900, EndsAt: 1000, Rewards: []MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", Money: money}}}, 800)
						if err != nil {
							return err
						}
						_, err = ActivateMarketingActivity(tx, admin.Id, activity.ID, "mepay:clock-activate:"+name, 800)
						return err
					}))
					receipt := epayTestReceipt(order.TradeNo, "alipay")
					var posted *EpayTopUpResult
					require.NoError(t, paidPostingBoundaryTest(t, db, "top_ups", 999, 1001, func(tx *gorm.DB, clock func() int64) error {
						var err error
						posted, err = completeEpayTopUpWithClock(tx, order.TradeNo, receipt, actorID, evidence, clock)
						return err
					}))
					var payment MarketingPayment
					require.NoError(t, db.First(&payment, posted.PaymentID).Error)
					assert.EqualValues(t, 1001, payment.PostedAt)
					stored := GetTopUpByTradeNo(order.TradeNo)
					require.NotNil(t, stored)
					assert.Equal(t, payment.PostedAt, stored.CompleteTime)
					marketingActivityProcessTest(t, db, user.Id, 1002)
					var rewards int64
					require.NoError(t, db.Model(&MarketingActivityReward{}).Where("user_id = ?", user.Id).Count(&rewards).Error)
					assert.Zero(t, rewards, "waiting across activity end cannot backdate a callback or manual posting")
					clockCalls := 0
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						replay, err := completeEpayTopUpWithClock(tx, order.TradeNo, receipt, actorID, evidence, func() int64 { clockCalls++; return 2000 })
						if err == nil {
							assert.True(t, replay.AlreadyPosted)
							assert.Equal(t, posted.PaymentID, replay.PaymentID)
						}
						return err
					}))
					assert.Zero(t, clockCalls)
				}
			})
			t.Run("unsupported_legacy_providers_cannot_mint_paid_money", func(t *testing.T) {
				for _, managed := range []bool{false, true} {
					user := newUser(common.RoleCommonUser)
					if !managed {
						require.NoError(t, db.Where("user_id = ?", user.Id).Delete(&MarketingAccount{}).Error)
					}
					for _, provider := range []string{PaymentProviderStripe, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake} {
						tradeNo := "mepay_unsupported_" + strconv.Itoa(user.Id) + provider
						order := TopUp{UserId: user.Id, Amount: 1, Money: 1, TradeNo: tradeNo, PaymentProvider: provider, PaymentMethod: provider, Status: common.TopUpStatusPending, CreateTime: 1}
						require.NoError(t, order.Insert())
						switch provider {
						case PaymentProviderStripe:
							err = Recharge(tradeNo, "", "")
						case PaymentProviderCreem:
							err = RechargeCreem(tradeNo, "", "", "")
						case PaymentProviderWaffo:
							err = RechargeWaffo(tradeNo, "")
						case PaymentProviderWaffoPancake:
							err = RechargeWaffoPancake(tradeNo)
						}
						require.Error(t, err)
						assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(tradeNo).Status)
						assert.Zero(t, getUserQuotaForPaymentGuardTest(t, user.Id))
						require.ErrorIs(t, ManualCompleteTopUp(admin.Id, tradeNo, 100, "cash receipt", ""), ErrPaymentMethodMismatch)
					}
				}
			})
		})
	}
}
