package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMarketingMoneyConversion(t *testing.T) {
	for _, tc := range []struct {
		name, amount, quota, rate string
		want                      int64
	}{
		{"configured_rate", "100", "500000", "7", 7142857},
		{"different_rate", "100", "50000", "1", 5000000},
		{"half_up", "0.005", "100", "1", 1},
		{"safe_boundary", "9007199254740991", "1", "1", common.MaxWalletQuota},
		{"overflow", "9007199254740992", "1", "1", 0},
		{"negative", "-1", "50000", "1", 0},
		{"zero", "0", "50000", "1", 0},
		{"zero_rate", "1", "50000", "0", 0},
		{"nonfinite", "NaN", "50000", "1", 0},
		{"exponent", "1e100", "50000", "1", 0},
		{"subquota", "0.001", "1", "1", 0},
		{"excess_precision", "0.1234567890123", "50000", "1", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (MarketingMoneySnapshot{Amount: tc.amount, QuotaPerUnit: tc.quota, CurrencyPerUnit: tc.rate, Currency: "CNY"}).Quota()
			if tc.want == 0 {
				require.ErrorIs(t, err, ErrMarketingInput)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Two independent connection pools exercise database-wide uniqueness rather than
// an in-process lock. CI's matrix runner requires all three real engines.
func TestMarketingPersistence(t *testing.T) {
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
				require.Contains(t, dsn, "/marketing_v2_behavior", "refuse non-fixture database")
			}
			oldDB, oldType, oldPath := DB, common.MainDatabaseType(), common.SQLitePath
			common.SQLitePath = filepath.Join(t.TempDir(), "marketing.sqlite")
			t.Cleanup(func() { DB = oldDB; common.SetMainDatabaseType(oldType); common.SQLitePath = oldPath; initCol() })
			t.Setenv("MV2_TEST_DSN", dsn)
			db, dbType, err := chooseDB("MV2_TEST_DSN", false)
			require.NoError(t, err)
			other, _, err := chooseDB("MV2_TEST_DSN", false)
			require.NoError(t, err)
			for _, conn := range []*gorm.DB{db, other} {
				pool, err := conn.DB()
				require.NoError(t, err)
				pool.SetMaxOpenConns(2)
				t.Cleanup(func() { _ = pool.Close() })
			}
			db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
			other = other.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
			DB = db
			common.SetMainDatabaseType(dbType)
			initCol()
			require.NoError(t, db.AutoMigrate(&User{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				userIDs := db.Model(&User{}).Select("id").Where("username LIKE ?", "mv2_%")
				fundingIDs := db.Model(&MarketingFunding{}).Select("id").Where("user_id IN (?)", userIDs)
				require.NoError(t, db.Where("funding_id IN (?)", fundingIDs).Delete(&MarketingAllocation{}).Error)
				for _, record := range []any{&MarketingLedgerEntry{}, &MarketingFunding{}, &MarketingGift{}, &MarketingPayment{}, &MarketingAccount{}} {
					require.NoError(t, db.Where("user_id IN (?)", userIDs).Delete(record).Error)
				}
				require.NoError(t, db.Unscoped().Where("username LIKE ?", "mv2_%").Delete(&User{}).Error)
			}
			cleanup()
			t.Cleanup(cleanup)
			recorder := &migrationSQLRecorder{}
			require.NoError(t, migrateMarketingV2(db.Session(&gorm.Session{Logger: recorder})))
			assert.Empty(t, recorder.schemaMutations(), "second marketing migration must not mutate schema")
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("engine %s: %s", dialect, version)

			t.Run("source_isolation_retry_and_order", func(t *testing.T) {
				user := User{Username: "mv2_isolation", AffCode: "mv2_isolation", Quota: 137}
				require.NoError(t, db.Create(&user).Error)
				input := MarketingPaymentInput{UserID: user.Id, Kind: "paid_code", Reference: "paid-1", PaidCents: 1234, PostedAt: 100, Money: MarketingMoneySnapshot{Amount: "20", QuotaPerUnit: "50000", CurrencyPerUnit: "2", Currency: "CNY"}}
				_, err := CreditMarketingPayment(db, input)
				require.ErrorIs(t, err, ErrMarketingTransaction)
				var first *MarketingPayment
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; first, err = CreditMarketingPayment(tx, input); return err }))
				assert.EqualValues(t, 500000, first.CreditedQuota)
				assert.EqualValues(t, 1234, first.PaidCents)
				assert.EqualValues(t, 1, first.Sequence)
				assert.Equal(t, MarketingFirstPaymentUnknown, first.FirstStatus)
				require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
					again, err := CreditMarketingPayment(tx, input)
					if err == nil {
						assert.Equal(t, *first, *again)
					}
					return err
				}))
				changed := input
				changed.PaidCents++
				require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := CreditMarketingPayment(tx, changed); return err }), ErrMarketingConflict)
				input.Reference = "paid-2"
				input.PostedAt = 99
				require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
					second, err := CreditMarketingPayment(tx, input)
					if err == nil {
						assert.EqualValues(t, 2, second.Sequence)
						assert.Equal(t, MarketingFirstPaymentLater, second.FirstStatus)
					}
					return err
				}))
				gift := MarketingGiftInput{UserID: user.Id, Kind: "recharge", Reference: "activity:1:paid-1:item:1", GrantedAt: 100, ExpiresAt: 200, Money: MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: "50000", CurrencyPerUnit: "2", Currency: "CNY"}}
				var award *MarketingGift
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; award, err = GrantMarketingGift(tx, gift); return err }))
				require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
					again, err := GrantMarketingGift(tx, gift)
					if err == nil {
						assert.Equal(t, *award, *again)
					}
					return err
				}))
				gift.ExpiresAt = 0
				require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := GrantMarketingGift(tx, gift); return err }), ErrMarketingConflict)
				gift.Reference = "activity:1:paid-1:item:2"
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := GrantMarketingGift(tx, gift); return err }))
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 1000137, user.Quota, "gift never becomes refundable paid balance")
				var account MarketingAccount
				require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
				assert.EqualValues(t, 137, account.OpeningQuota)
				assert.Equal(t, "unverified", account.OriginState)
				assert.EqualValues(t, 2, account.PaymentSequence)
				assert.Equal(t, MarketingPaymentHistoryPaid, account.PaymentHistory)
				assert.Zero(t, account.FirstPaymentID, "unknown old history must not invent the true first")
				var ledger []MarketingLedgerEntry
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&ledger).Error)
				require.Len(t, ledger, 4)
				var decoded MarketingMoneySnapshot
				require.NoError(t, common.UnmarshalJsonStr(first.Conversion, &decoded))
				assert.Equal(t, input.Money, decoded)
			})

			t.Run("first_payment_requires_trusted_registration_fact", func(t *testing.T) {
				var user User
				var first *MarketingPayment
				input := MarketingPaymentInput{Kind: "online", Reference: "new-user-first", PaidCents: 100, PostedAt: 100, Money: MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "50000", CurrencyPerUnit: "1", Currency: "CNY"}}
				// Fixture establishes the real user AND known-empty history in
				// one transaction, as MV2-009 must do in registration. Discovery
				// of an existing user is never allowed to infer this state.
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					user = User{Username: "mv2_new_user", AffCode: "mv2_new_user"}
					if err := tx.Create(&user).Error; err != nil {
						return err
					}
					account := MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: MarketingPaymentHistoryNone, PaymentHistoryEvidence: "registration:new-user-event"}
					return tx.Create(&account).Error
				}))
				input.UserID = user.Id
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; first, err = CreditMarketingPayment(tx, input); return err }))
				assert.Equal(t, MarketingFirstPaymentConfirmed, first.FirstStatus)
				require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
					again, err := CreditMarketingPayment(tx, input)
					if err == nil {
						assert.Equal(t, *first, *again)
					}
					return err
				}))
				input.Reference = "new-user-second"
				input.Kind = "paid_code"
				require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
					payment, err := CreditMarketingPayment(tx, input)
					if err == nil {
						assert.Equal(t, MarketingFirstPaymentLater, payment.FirstStatus)
					}
					return err
				}))
				var account MarketingAccount
				require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
				assert.Equal(t, first.ID, account.FirstPaymentID)
				assert.Equal(t, "registration:new-user-event", account.PaymentHistoryEvidence)
				// Re-enabling or clearing the wallet cannot reset first-payment history.
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 0).Error)
				input.Reference = "new-user-after-zero"
				require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
					payment, err := CreditMarketingPayment(tx, input)
					if err == nil {
						assert.Equal(t, MarketingFirstPaymentLater, payment.FirstStatus)
					}
					return err
				}))
			})

			if dialect == "mysql" {
				t.Run("existing_repeatable_read_snapshot", func(t *testing.T) {
					user := User{Username: "mv2_snapshot", AffCode: "mv2_snapshot"}
					require.NoError(t, db.Create(&user).Error)
					input := MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "snapshot-paid", PaidCents: 100, PostedAt: 100, Money: MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}}
					stale := db.Begin()
					require.NoError(t, stale.Error)
					defer stale.Rollback()
					var count int64
					require.NoError(t, stale.Model(&MarketingAccount{}).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
					var first *MarketingPayment
					require.NoError(t, other.Transaction(func(tx *gorm.DB) error { var err error; first, err = CreditMarketingPayment(tx, input); return err }))
					again, err := CreditMarketingPayment(stale, input)
					require.NoError(t, err)
					assert.Equal(t, *first, *again)
					require.NoError(t, stale.Commit().Error)

					gift := MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: "snapshot-gift", GrantedAt: 100, Money: MarketingMoneySnapshot{Amount: "9007199254740991", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}}
					stale = db.Begin()
					require.NoError(t, stale.Error)
					defer stale.Rollback()
					require.NoError(t, stale.Model(&MarketingGift{}).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
					var award *MarketingGift
					require.NoError(t, other.Transaction(func(tx *gorm.DB) error { var err error; award, err = GrantMarketingGift(tx, gift); return err }))
					same, err := GrantMarketingGift(stale, gift)
					require.NoError(t, err)
					assert.Equal(t, *award, *same)
					gift.Reference = "snapshot-overflow"
					gift.Money.Amount = "1"
					_, err = GrantMarketingGift(stale, gift)
					require.ErrorIs(t, err, ErrMarketingInput, "current gifts must be counted despite the old read snapshot")
					require.NoError(t, stale.Rollback().Error)
				})
			}

			t.Run("source_and_request_constraints", func(t *testing.T) {
				user := User{Username: "mv2_other_owner", AffCode: "mv2_other_owner"}
				require.NoError(t, db.Create(&user).Error)
				var existing MarketingPayment
				require.NoError(t, db.Where("source_reference = ?", "paid-1").First(&existing).Error)
				var money MarketingMoneySnapshot
				require.NoError(t, common.UnmarshalJsonStr(existing.Conversion, &money))
				input := MarketingPaymentInput{UserID: user.Id, Kind: existing.SourceKind, Reference: existing.SourceReference, PaidCents: existing.PaidCents, PostedAt: existing.PostedAt, Money: money}
				require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := CreditMarketingPayment(tx, input); return err }), ErrMarketingConflict)
				var count int64
				require.NoError(t, db.Model(&MarketingAccount{}).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.Zero(t, count, "failed conflicting owner must not create an account")
				funding := MarketingFunding{RequestKey: "request-1", UserID: user.Id, State: "reserved"}
				require.NoError(t, db.Create(&funding).Error)
				duplicate := funding
				duplicate.ID = 0
				require.Error(t, other.Create(&duplicate).Error)
				allocation := MarketingAllocation{FundingID: funding.ID, GiftID: 0, ReservedQuota: 10}
				require.NoError(t, db.Create(&allocation).Error)
				duplicateAllocation := allocation
				duplicateAllocation.ID = 0
				require.Error(t, other.Create(&duplicateAllocation).Error)
				duplicatePayment := existing
				duplicatePayment.ID = 0
				duplicatePayment.SourceKey = "different-source-same-user-sequence"
				require.Error(t, other.Create(&duplicatePayment).Error)
			})

			t.Run("late_failure_rolls_back_every_fact", func(t *testing.T) {
				user := User{Username: "mv2_rollback", AffCode: "mv2_rollback", Quota: 10}
				require.NoError(t, db.Create(&user).Error)
				input := MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "rollback", PaidCents: 100, PostedAt: 100, Money: MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "50000", CurrencyPerUnit: "1", Currency: "CNY"}}
				key, err := marketingSourceKey(input.Kind, input.Reference)
				require.NoError(t, err)
				// A conflicting ledger entry fails after the payment and balance mutations.
				require.NoError(t, db.Create(&MarketingLedgerEntry{OperationKey: key, Kind: "test_conflict"}).Error)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := CreditMarketingPayment(tx, input); return err }))
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 10, user.Quota)
				for _, record := range []any{&MarketingAccount{}, &MarketingPayment{}} {
					var count int64
					require.NoError(t, db.Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
				}
				require.NoError(t, db.Where("operation_key = ?", key).Delete(&MarketingLedgerEntry{}).Error)
				abort := errors.New("caller rollback")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreditMarketingPayment(tx, input)
					if err != nil {
						return err
					}
					return abort
				}), abort)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 10, user.Quota)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					payment, err := CreditMarketingPayment(tx, input)
					if err == nil {
						assert.EqualValues(t, 1, payment.Sequence)
					}
					return err
				}))
			})

			t.Run("two_connections_compete_for_same_fact", func(t *testing.T) {
				user := User{Username: "mv2_concurrent", AffCode: "mv2_concurrent"}
				require.NoError(t, db.Create(&user).Error)
				payment := MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "concurrent", PaidCents: 100, PostedAt: 100, Money: MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "50000", CurrencyPerUnit: "1", Currency: "CNY"}}
				gift := MarketingGiftInput{UserID: user.Id, Kind: "registration", Reference: "concurrent-gift", GrantedAt: 100, Money: payment.Money}
				for _, isGift := range []bool{false, true} {
					apply := func(conn *gorm.DB) error {
						return conn.Transaction(func(tx *gorm.DB) error {
							if isGift {
								_, err := GrantMarketingGift(tx, gift)
								return err
							}
							_, err := CreditMarketingPayment(tx, payment)
							return err
						})
					}
					start := make(chan struct{})
					results := make([]error, 2)
					var wg sync.WaitGroup
					for i, conn := range []*gorm.DB{db, other} {
						wg.Go(func() { <-start; results[i] = apply(conn) })
					}
					close(start)
					wg.Wait()
					for _, err := range results {
						if err != nil {
							require.Equal(t, "sqlite", dialect)
							require.Contains(t, strings.ToLower(err.Error()), "locked")
							require.NoError(t, apply(other), "retry entire SQLite transaction after winning writer commits")
						}
					}
				}
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 50000, user.Quota)
				for _, record := range []any{&MarketingPayment{}, &MarketingGift{}} {
					var count int64
					require.NoError(t, db.Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.EqualValues(t, 1, count)
				}
				var count int64
				require.NoError(t, db.Model(&MarketingLedgerEntry{}).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.EqualValues(t, 2, count)
			})

			t.Run("invalid_unverified_and_overflow", func(t *testing.T) {
				user := User{Username: "mv2_limits", AffCode: "mv2_limits", Quota: common.MaxWalletQuota}
				require.NoError(t, db.Create(&user).Error)
				input := MarketingPaymentInput{UserID: user.Id, Kind: "manual_paid", Reference: "limit", PaidCents: 1, PostedAt: 100, Money: MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}}
				for _, change := range []func(*MarketingPaymentInput){func(p *MarketingPaymentInput) {}, func(p *MarketingPaymentInput) { p.PaidCents = 0 }, func(p *MarketingPaymentInput) { p.PaidCents = -1 }, func(p *MarketingPaymentInput) { p.Reference = "" }, func(p *MarketingPaymentInput) { p.Kind = "unknown_adjustment" }} {
					bad := input
					change(&bad)
					require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := CreditMarketingPayment(tx, bad); return err }), ErrMarketingInput)
				}
				gift := MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: "max-gift", GrantedAt: 100, Money: MarketingMoneySnapshot{Amount: "9007199254740991", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := GrantMarketingGift(tx, gift); return err }))
				gift.Reference = "overflow-gift"
				gift.Money.Amount = "1"
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := GrantMarketingGift(tx, gift); return err }), ErrMarketingInput)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, common.MaxWalletQuota, user.Quota)
			})
		})
	}
}
