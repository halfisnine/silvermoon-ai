package model

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSearchRedemptionsFiltersAndPaginates(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	})

	now := common.GetTimestamp()
	redemptions := []Redemption{
		{Id: 1, Name: "alpha-active", Key: "00000000000000000000000000000001", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: 0},
		{Id: 2, Name: "alpha-future", Key: "00000000000000000000000000000002", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now + 3600},
		{Id: 3, Name: "alpha-expired", Key: "00000000000000000000000000000003", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now - 10},
		{Id: 4, Name: "beta-disabled", Key: "00000000000000000000000000000004", Status: common.RedemptionCodeStatusDisabled, ExpiredTime: 0},
		{Id: 5, Name: "beta-used", Key: "00000000000000000000000000000005", Status: common.RedemptionCodeStatusUsed, ExpiredTime: 0},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	tests := []struct {
		name      string
		keyword   string
		status    string
		startIdx  int
		num       int
		wantTotal int64
		wantIds   []int
	}{
		{
			name:      "no filters returns all rows",
			num:       10,
			wantTotal: 5,
			wantIds:   []int{5, 4, 3, 2, 1},
		},
		{
			name:      "keyword filters by name prefix",
			keyword:   "alpha",
			num:       10,
			wantTotal: 3,
			wantIds:   []int{3, 2, 1},
		},
		{
			name:      "enabled status excludes expired rows",
			status:    "1",
			num:       10,
			wantTotal: 2,
			wantIds:   []int{2, 1},
		},
		{
			name:      "expired status returns enabled expired rows",
			status:    "expired",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{3},
		},
		{
			name:      "disabled status",
			status:    "2",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{4},
		},
		{
			name:      "used status",
			status:    "3",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{5},
		},
		{
			name:      "pagination keeps unpaged total",
			startIdx:  1,
			num:       2,
			wantTotal: 5,
			wantIds:   []int{4, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := SearchRedemptions(tt.keyword, tt.status, tt.startIdx, tt.num)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total)
			gotIds := make([]int, 0, len(rows))
			for _, row := range rows {
				gotIds = append(gotIds, row.Id)
			}
			assert.Equal(t, tt.wantIds, gotIds)
		})
	}
}

// These are actual database transactions, not mocked receipts or guessed sale
// values. The same scenarios execute against every required primary dialect.
func TestPaidRedemption(t *testing.T) {
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
			common.SQLitePath = filepath.Join(t.TempDir(), "paid-code.sqlite")
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
			common.SetLogDatabaseType(dbType)
			common.SetMainDatabaseType(dbType)
			initCol()
			require.NoError(t, db.AutoMigrate(&User{}, &Redemption{}, &Log{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				users := db.Unscoped().Model(&User{}).Select("id").Where("username LIKE ?", "mred_%")
				for _, record := range []any{&Log{}, &MarketingActivityDeliveryCursor{}, &MarketingActivityReward{}, &MarketingActivityDecision{}, &MarketingActivityProgress{}, &MarketingActivityFact{}, &MarketingGift{}, &MarketingLedgerEntry{}, &MarketingPayment{}, &MarketingAccount{}} {
					require.NoError(t, db.Where("user_id IN (?)", users).Delete(record).Error)
				}
				require.NoError(t, db.Where("actor_id IN (?)", users).Delete(&MarketingActivityOperation{}).Error)
				require.NoError(t, db.Where("name LIKE ?", "mred_%").Delete(&MarketingActivity{}).Error)
				require.NoError(t, db.Unscoped().Where("name LIKE ?", "mred_%").Delete(&Redemption{}).Error)
				require.NoError(t, db.Unscoped().Where("username LIKE ?", "mred_%").Delete(&User{}).Error)
			}
			cleanup()
			t.Cleanup(cleanup)
			newUser := func(name string, role int) User {
				user := User{Username: "mred_" + name, AffCode: "mred_" + name, Status: common.UserStatusEnabled, Role: role, CreatedAt: 1}
				require.NoError(t, db.Create(&user).Error)
				require.NoError(t, db.Create(&MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: MarketingPaymentHistoryNone, PaymentHistoryEvidence: "registration:" + name}).Error)
				return user
			}
			admin := newUser("admin", common.RoleAdminUser)
			spec := PaidRedemptionSpec{Name: "mred_sale", PaidCents: 12345, Money: MarketingMoneySnapshot{Amount: "20", QuotaPerUnit: "37", CurrencyPerUnit: "2", Currency: "CNY"}, SalesChannel: "shop", SalesBatch: "batch-one", SalesEvidence: "receipt-reference"}
			create := func(name string) *Redemption {
				input := spec
				input.Name = "mred_" + name
				var rows []*Redemption
				require.NoError(t, WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
					var err error
					rows, err = CreatePaidRedemptionBatch(tx, admin.Id, []string{common.GetUUID()}, input, 100)
					return err
				}))
				require.Len(t, rows, 1)
				return rows[0]
			}
			t.Run("paid_code_offsets_negative_balance_once", func(t *testing.T) {
				for i, opening := range []int{-500, -370, -200} {
					name := "debt" + strconv.Itoa(i)
					user := newUser(name, common.RoleCommonUser)
					require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", opening).Error)
					require.NoError(t, db.Model(&MarketingAccount{}).Where("user_id = ?", user.Id).Update("opening_quota", opening).Error)
					code := create(name)
					var paymentID int64
					for attempt := range 2 {
						require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
							result, err := RedeemPaidCode(tx, user.Id, code.Key, 200)
							if err != nil {
								return err
							}
							assert.Equal(t, attempt == 1, result.AlreadyPosted)
							if paymentID == 0 {
								paymentID = result.PaymentID
							} else {
								assert.Equal(t, paymentID, result.PaymentID)
							}
							return nil
						}))
					}
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, opening+370, user.Quota)
					var consumed int64
					require.NoError(t, db.Model(&MarketingLedgerEntry{}).Where("user_id = ? AND kind = ?", user.Id, "consume").Count(&consumed).Error)
					assert.Zero(t, consumed)
				}
			})
			t.Run("paid_snapshot_sequence_activity_and_replay", func(t *testing.T) {
				user := newUser("posting", common.RoleCommonUser)
				var activity *MarketingActivity
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					activity, err = CreateMarketingActivity(tx, admin.Id, "mred:activity", MarketingActivitySpec{Name: "mred_threshold", Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 10000, StartsAt: 100, EndsAt: 250, Rewards: []MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", Money: spec.Money}}}, 90)
					if err != nil {
						return err
					}
					_, err = ActivateMarketingActivity(tx, admin.Id, activity.ID, "mred:activate", 90)
					return err
				}))
				code := create("posting")
				var first *PaidRedemptionResult
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					first, err = RedeemPaidCode(tx, user.Id, code.Key, 200)
					return err
				}))
				assert.False(t, first.AlreadyPosted)
				assert.Equal(t, 370, first.Quota)
				originalRate := common.QuotaPerUnit
				common.QuotaPerUnit = 999
				t.Cleanup(func() { common.QuotaPerUnit = originalRate })
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					replay, err := RedeemPaidCode(tx, user.Id, code.Key, 300)
					if err == nil {
						assert.True(t, replay.AlreadyPosted)
						assert.Equal(t, first.PaymentID, replay.PaymentID)
						assert.Equal(t, first.Quota, replay.Quota)
					}
					return err
				}))
				var payment MarketingPayment
				require.NoError(t, db.First(&payment, first.PaymentID).Error)
				assert.EqualValues(t, 12345, payment.PaidCents)
				assert.EqualValues(t, 370, payment.CreditedQuota)
				assert.Equal(t, code.CreditedConversion, payment.Conversion)
				assert.EqualValues(t, 1, payment.Sequence)
				assert.Equal(t, MarketingFirstPaymentConfirmed, payment.FirstStatus)
				assert.Equal(t, "paid_code", payment.SourceKind)
				assert.Equal(t, "redemption:"+strconv.Itoa(code.Id), payment.SourceReference)
				assert.EqualValues(t, 200, payment.PostedAt)
				marketingActivityProcessTest(t, db, user.Id, 300)
				marketingActivityDeliverTest(t, db, user.Id, 300)
				var gifts []MarketingGift
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifts).Error)
				require.Len(t, gifts, 1)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 370, user.Quota)
				var facts int64
				require.NoError(t, db.Model(&MarketingActivityFact{}).Where("user_id = ?", user.Id).Count(&facts).Error)
				assert.EqualValues(t, 1, facts, "gift does not recursively create a payment fact")
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					online, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "mred:online", PaidCents: 12345, PostedAt: 301, Money: spec.Money})
					if err == nil {
						assert.EqualValues(t, 2, online.Sequence)
						assert.Equal(t, MarketingFirstPaymentLater, online.FirstStatus)
					}
					return err
				}))
				marketingActivityProcessTest(t, db, user.Id, 302)
				marketingActivityDeliverTest(t, db, user.Id, 302)
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifts).Error)
				assert.Len(t, gifts, 1, "posting after activity end is not rewarded")
				other := newUser("other", common.RoleCommonUser)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, other.Id, code.Key, 303); return err }))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SetPaidRedemptionStatus(tx, admin.Id, code.Id, common.RedemptionCodeStatusEnabled, 303)
					return err
				}), ErrPaidRedemptionUsed)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := UpdatePaidRedemption(tx, admin.Id, code.Id, spec, 303); return err }), ErrPaidRedemptionUsed)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					n, err := DeletePaidRedemptions(tx, admin.Id, []int{code.Id})
					assert.EqualValues(t, 1, n)
					return err
				}))
				var archived Redemption
				require.NoError(t, db.Unscoped().First(&archived, code.Id).Error)
				assert.True(t, archived.DeletedAt.Valid)
				assert.Equal(t, first.PaymentID, archived.PaymentID)
				require.NoError(t, db.First(&payment, first.PaymentID).Error)
			})
			t.Run("public_replay_does_not_duplicate_recharge_log", func(t *testing.T) {
				user := newUser("public", common.RoleCommonUser)
				code := create("public")
				for range 2 {
					quota, err := Redeem(code.Key, user.Id)
					require.NoError(t, err)
					assert.Equal(t, 370, quota)
				}
				var logs []Log
				require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, LogTypeTopup).Find(&logs).Error)
				require.Len(t, logs, 1)
				assert.NotContains(t, logs[0].Content, code.Key)
				assert.NotContains(t, logs[0].Other, code.Key)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 370, user.Quota)
			})
			t.Run("rollback_overflow_and_missing_sale", func(t *testing.T) {
				user := newUser("rollback", common.RoleCommonUser)
				code := create("rollback")
				sentinel := errors.New("abort after complete posting")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := RedeemPaidCode(tx, user.Id, code.Key, 400)
					if err != nil {
						return err
					}
					return sentinel
				}), sentinel)
				var stored Redemption
				require.NoError(t, db.First(&stored, code.Id).Error)
				assert.Zero(t, stored.PaymentID)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, stored.Status)
				for _, record := range []any{&MarketingPayment{}, &MarketingLedgerEntry{}, &MarketingActivityFact{}} {
					var n int64
					require.NoError(t, db.Model(record).Where("user_id = ?", user.Id).Count(&n).Error)
					assert.Zero(t, n)
				}
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", common.MaxWalletQuota-100).Error)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, code.Key, 401); return err }))
				require.NoError(t, db.First(&stored, code.Id).Error)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, stored.Status)
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 0).Error)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, code.Key, 402); return err }))
				baseline := Redemption{Name: "mred_unknown", Key: common.GetUUID(), Quota: 100, Status: common.RedemptionCodeStatusEnabled}
				require.NoError(t, db.Create(&baseline).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, baseline.Key, 403); return err }), ErrPaidRedemptionSale)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := UpdatePaidRedemption(tx, admin.Id, baseline.Id, spec, 404)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, baseline.Key, 405); return err }))
				used := Redemption{Name: "mred_old_used", Key: common.GetUUID(), Quota: 100, Status: common.RedemptionCodeStatusUsed, UsedUserId: user.Id, RedeemedTime: 1}
				require.NoError(t, db.Create(&used).Error)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, used.Key, 406); return err }))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := UpdatePaidRedemption(tx, admin.Id, used.Id, spec, 406); return err }), ErrPaidRedemptionUsed)
			})
			t.Run("authorization_status_expiry_and_atomic_batch", func(t *testing.T) {
				user := newUser("permissions", common.RoleCommonUser)
				code := create("permissions")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreatePaidRedemptionBatch(tx, user.Id, []string{common.GetUUID()}, spec, 500)
					return err
				}), ErrMarketingAccess)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := UpdatePaidRedemption(tx, user.Id, code.Id, spec, 500); return err }), ErrMarketingAccess)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := DeletePaidRedemptions(tx, user.Id, []int{code.Id}); return err }), ErrMarketingAccess)
				require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleCommonUser).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SetPaidRedemptionStatus(tx, admin.Id, code.Id, common.RedemptionCodeStatusDisabled, 500)
					return err
				}), ErrMarketingAccess)
				require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SetPaidRedemptionStatus(tx, admin.Id, code.Id, common.RedemptionCodeStatusDisabled, 500)
					return err
				}))
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, code.Key, 501); return err }))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SetPaidRedemptionStatus(tx, admin.Id, code.Id, common.RedemptionCodeStatusEnabled, 502)
					return err
				}))
				expiredSpec := spec
				expiredSpec.ExpiredTime = 503
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := UpdatePaidRedemption(tx, admin.Id, code.Id, expiredSpec, 502)
					return err
				}))
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, code.Key, 504); return err }))
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, code.Key, 503); return err }), ErrMarketingAccess)
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusEnabled).Error)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := RedeemPaidCode(tx, user.Id, code.Key, 503); return err }), "exact expiry boundary preserves original inclusive exchange contract")
				newKey := common.GetUUID()
				err := db.Transaction(func(tx *gorm.DB) error {
					_, err := CreatePaidRedemptionBatch(tx, admin.Id, []string{newKey, code.Key}, spec, 505)
					return err
				})
				require.Error(t, err)
				assert.NotContains(t, err.Error(), code.Key)
				var count int64
				require.NoError(t, db.Model(&Redemption{}).Where(commonKeyCol+" = ?", newKey).Count(&count).Error)
				assert.Zero(t, count)
				invalid := spec
				invalid.PaidCents = 0
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreatePaidRedemptionBatch(tx, admin.Id, []string{common.GetUUID()}, invalid, 505)
					return err
				}), ErrPaidRedemptionSale)
				invalid = spec
				invalid.Money.Amount = "9999999999999999"
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreatePaidRedemptionBatch(tx, admin.Id, []string{common.GetUUID()}, invalid, 505)
					return err
				}))
				invalid = spec
				invalid.SalesEvidence = code.Key
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreatePaidRedemptionBatch(tx, admin.Id, []string{code.Key}, invalid, 505)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					n, err := DeleteInvalidPaidRedemptions(tx, admin.Id, 506)
					assert.Positive(t, n)
					return err
				}))
			})
			t.Run("posting_time_follows_locked_code_read", func(t *testing.T) {
				user := newUser("clock", common.RoleCommonUser)
				code := create("clock_expiry")
				expires := spec
				expires.Name = code.Name
				expires.ExpiredTime = 1000
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := UpdatePaidRedemption(tx, admin.Id, code.Id, expires, 100)
					return err
				}))
				err := paidPostingBoundaryTest(t, db, "redemptions", 999, 1001, func(tx *gorm.DB, clock func() int64) error {
					_, err := redeemPaidCodeWithClock(tx, user.Id, code.Key, clock)
					return err
				})
				require.ErrorIs(t, err, ErrRedeemFailed)
				stored, err := GetRedemptionById(code.Id)
				require.NoError(t, err)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, stored.Status)
				assert.Zero(t, stored.PaymentID)
				assert.Zero(t, stored.RedeemedTime)
				assert.Zero(t, getUserQuotaForPaymentGuardTest(t, user.Id))
				for _, record := range []any{&MarketingPayment{}, &MarketingLedgerEntry{}, &MarketingActivityFact{}} {
					var count int64
					require.NoError(t, db.Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
				}
				var activity *MarketingActivity
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					activity, err = CreateMarketingActivity(tx, admin.Id, "mred:clock-activity", MarketingActivitySpec{Name: "mred_clock", Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 1, StartsAt: 900, EndsAt: 1000, Rewards: []MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", Money: spec.Money}}}, 800)
					if err != nil {
						return err
					}
					_, err = ActivateMarketingActivity(tx, admin.Id, activity.ID, "mred:clock-activate", 800)
					return err
				}))
				active := create("clock_posting")
				var posted *PaidRedemptionResult
				require.NoError(t, paidPostingBoundaryTest(t, db, "redemptions", 999, 1001, func(tx *gorm.DB, clock func() int64) error {
					var err error
					posted, err = redeemPaidCodeWithClock(tx, user.Id, active.Key, clock)
					return err
				}))
				var payment MarketingPayment
				require.NoError(t, db.First(&payment, posted.PaymentID).Error)
				assert.EqualValues(t, 1001, payment.PostedAt)
				stored, err = GetRedemptionById(active.Id)
				require.NoError(t, err)
				assert.Equal(t, payment.PostedAt, stored.RedeemedTime)
				marketingActivityProcessTest(t, db, user.Id, 1002)
				var rewards int64
				require.NoError(t, db.Model(&MarketingActivityReward{}).Where("user_id = ?", user.Id).Count(&rewards).Error)
				assert.Zero(t, rewards, "a transaction arriving before the end cannot backdate a posting after its locked read")
				clockCalls := 0
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					replay, err := redeemPaidCodeWithClock(tx, user.Id, active.Key, func() int64 { clockCalls++; return 2000 })
					if err == nil {
						assert.True(t, replay.AlreadyPosted)
						assert.Equal(t, posted.PaymentID, replay.PaymentID)
					}
					return err
				}))
				assert.Zero(t, clockCalls, "replay uses the original posting without sampling a new time")
			})
			t.Run("sql_diagnostics_never_render_live_codes", func(t *testing.T) {
				code := create("private_logs")
				previousDebug := common.DebugEnabled
				t.Cleanup(func() { common.DebugEnabled = previousDebug })
				for _, debug := range []bool{false, true} {
					common.DebugEnabled = debug
					var output bytes.Buffer
					logged := db.Session(&gorm.Session{Logger: newGormLogger(&output).LogMode(logger.Info)})
					err := logged.Transaction(func(tx *gorm.DB) error {
						_, err := CreatePaidRedemptionBatch(tx, admin.Id, []string{code.Key}, spec, 550)
						return err
					})
					require.Error(t, err)
					assert.NotContains(t, err.Error(), code.Key)
					assert.Contains(t, output.String(), "INSERT")
					assert.Contains(t, output.String(), "error", "driver error code remains useful for diagnosis")
					assert.NotContains(t, output.String(), code.Key)
					output.Reset()
					// A real server-side query failure exercises parameter rendering
					// independently of INSERT diagnostics, including Debug/LogMode.
					var absent Redemption
					err = paidRedemptionDB(logged).Debug().Table("missing_paid_redemption_table").Where(commonKeyCol+" = ?", code.Key).First(&absent).Error
					require.Error(t, err)
					assert.Contains(t, output.String(), "SELECT")
					assert.Regexp(t, `\?|\$1`, output.String())
					assert.NotContains(t, output.String(), code.Key)
				}
			})
			t.Run("overlapping_same_code_and_online_sequence", func(t *testing.T) {
				users := []User{newUser("race_a", common.RoleCommonUser), newUser("race_b", common.RoleCommonUser)}
				code := create("race")
				outcomes := make([]*PaidRedemptionResult, 2)
				failures := make([]error, 2)
				paidRedemptionConcurrentTest(t, users[0].Id, func(tx *gorm.DB, worker int) error {
					var err error
					outcomes[worker], err = RedeemPaidCode(tx, users[worker].Id, code.Key, 600)
					return err
				}, failures)
				winner := -1
				for i, err := range failures {
					if err == nil {
						require.Equal(t, -1, winner)
						winner = i
						require.NotNil(t, outcomes[i])
					} else {
						assert.ErrorIs(t, err, ErrMarketingConflict)
					}
				}
				require.NotEqual(t, -1, winner)
				var paymentCount int64
				require.NoError(t, db.Model(&MarketingPayment{}).Where("source_kind = ? AND source_reference = ?", "paid_code", "redemption:"+strconv.Itoa(code.Id)).Count(&paymentCount).Error)
				assert.EqualValues(t, 1, paymentCount)
				for i := range users {
					require.NoError(t, db.First(&users[i], users[i].Id).Error)
					if i == winner {
						assert.Equal(t, 370, users[i].Quota)
					} else {
						assert.Zero(t, users[i].Quota)
					}
				}
				user := newUser("race_order", common.RoleCommonUser)
				next := create("race_order")
				paidRedemptionConcurrentTest(t, user.Id, func(tx *gorm.DB, worker int) error {
					if worker == 0 {
						_, err := RedeemPaidCode(tx, user.Id, next.Key, 601)
						return err
					}
					_, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "mred:race-online", PaidCents: 100, PostedAt: 601, Money: spec.Money})
					return err
				}, failures)
				require.NoError(t, failures[0])
				require.NoError(t, failures[1])
				var payments []MarketingPayment
				require.NoError(t, db.Where("user_id = ?", user.Id).Order("sequence").Find(&payments).Error)
				require.Len(t, payments, 2)
				assert.EqualValues(t, 1, payments[0].Sequence)
				assert.EqualValues(t, 2, payments[1].Sequence)
				assert.Equal(t, MarketingFirstPaymentConfirmed, payments[0].FirstStatus)
				assert.Equal(t, MarketingFirstPaymentLater, payments[1].FirstStatus)
				paidRedemptionConcurrentTest(t, user.Id, func(tx *gorm.DB, worker int) error {
					var err error
					outcomes[worker], err = RedeemPaidCode(tx, user.Id, next.Key, 602)
					return err
				}, failures)
				for i, err := range failures {
					require.NoError(t, err)
					assert.True(t, outcomes[i].AlreadyPosted)
				}
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 740, user.Quota)
			})
		})
	}
}

func paidRedemptionConcurrentTest(t *testing.T, userID int, apply func(*gorm.DB, int) error, failures []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	finished := make(chan int, 2)
	for i := range 2 {
		workers.Go(func() {
			first := true
			failures[i] = WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
				if first {
					first = false
					var user User
					if err := tx.First(&user, userID).Error; err != nil {
						return err
					}
					ready <- struct{}{}
					select {
					case <-start:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return apply(tx, i)
			})
			finished <- i
		})
	}
	for range 2 {
		select {
		case <-ready:
		case i := <-finished:
			t.Fatalf("worker ended before overlap: %v", failures[i])
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(start)
	for range 2 {
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

type paidPostingBoundaryContextKey struct{}

// Pause at a real completed source-row read inside an open transaction. MySQL
// and PostgreSQL retain its row lock; SQLite retains the transaction snapshot.
// The controlled clock advances while the operation waits at this boundary,
// so testing event time never depends on sleeps or synthetic payment records.
func paidPostingBoundaryTest(t *testing.T, db *gorm.DB, table string, before, after int64, apply func(*gorm.DB, func() int64) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), paidPostingBoundaryContextKey{}, true), 30*time.Second)
	var workers sync.WaitGroup
	defer cancel()
	var reached atomic.Bool
	var current, clockCalls atomic.Int64
	current.Store(before)
	ready, release := make(chan struct{}, 1), make(chan struct{})
	callbackName := "test:paid_posting_boundary"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(query *gorm.DB) {
		if query.Error != nil || query.Statement.Table != table || len(query.Statement.Selects) != 0 || query.Statement.Context.Value(paidPostingBoundaryContextKey{}) != true || !reached.CompareAndSwap(false, true) {
			return
		}
		ready <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			query.AddError(ctx.Err())
		}
	}))
	defer func() {
		cancel()
		workers.Wait()
		require.NoError(t, db.Callback().Query().Remove(callbackName))
	}()
	result := make(chan error, 1)
	workers.Go(func() {
		result <- WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			return apply(tx, func() int64 { clockCalls.Add(1); return current.Load() })
		})
	})
	select {
	case <-ready:
	case err := <-result:
		t.Fatalf("operation exited before its locked source read: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assert.Zero(t, clockCalls.Load(), "the posting clock must not run before acquiring/reading the source row")
	current.Store(after)
	close(release)
	select {
	case err := <-result:
		assert.EqualValues(t, 1, clockCalls.Load(), "one post-lock time governs both expiry and all posting fields")
		return err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return ctx.Err()
	}
}
