package model

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMarketingMoneyConversion(t *testing.T) {
	t.Run("reward_bases_and_single_rounding", func(t *testing.T) {
		base := MarketingMoneySnapshot{Amount: "100", QuotaPerUnit: "50", CurrencyPerUnit: "1", Currency: "CNY"}
		data, err := common.Marshal(base)
		require.NoError(t, err)
		payment := MarketingPayment{ID: 1, PaidCents: 20000, CreditedQuota: 5000, Conversion: string(data)}
		for _, tc := range []struct {
			name, mode, ratio string
			want              int64
		}{
			{"fixed_face_value", "fixed", "", 5000},
			{"credited_paid_not_cash", "paid_ratio", "0.1", 500},
			{"actual_cash_not_credited", "cash_ratio", "0.1", 1000},
			{"small_ratio_records_zero", "paid_ratio", "0.000001", 0},
			{"fraction_rounds_half_up", "paid_ratio", "0.0001", 1},
			{"larger_than_paid_is_not_an_implicit_cap", "paid_ratio", "2", 10000},
		} {
			t.Run(tc.name, func(t *testing.T) {
				amount, err := CalculateMarketingRewardAmount(tc.mode, tc.ratio, base, &payment)
				require.NoError(t, err)
				assert.Equal(t, tc.want, amount.Quota)
				if tc.mode != "fixed" {
					assert.Equal(t, payment.ID, amount.PaymentID)
					assert.Equal(t, int64(20000), amount.PaidCents)
					assert.Equal(t, int64(5000), amount.CreditedQuota)
				}
			})
		}
		for _, ratio := range []string{"0", "-1", "NaN", "1e8", "0.1234567890123", "9999999999999999"} {
			_, err := CalculateMarketingRewardAmount("paid_ratio", ratio, base, &payment)
			require.ErrorIs(t, err, ErrMarketingInput, ratio)
		}
		wrong := payment
		wrong.CreditedQuota++
		_, err = CalculateMarketingRewardAmount("paid_ratio", "0.1", base, &wrong)
		require.ErrorIs(t, err, ErrMarketingInput)
		_, err = CalculateMarketingRewardAmount("cash_ratio", "0.1", MarketingMoneySnapshot{Currency: "USD", QuotaPerUnit: "50", CurrencyPerUnit: "1"}, &payment)
		require.ErrorIs(t, err, ErrMarketingInput)
		cny := MarketingMoneySnapshot{Currency: "CNY", QuotaPerUnit: "500000", CurrencyPerUnit: "7.3"}
		payment.PaidCents = 10000
		amount, err := CalculateMarketingRewardAmount("cash_ratio", "0.05", cny, &payment)
		require.NoError(t, err)
		assert.Equal(t, int64(342466), amount.Quota)
		// One resulting quota cannot be represented by a 12-decimal display
		// amount at this valid rate. Credited-paid ratios must not round twice.
		extreme := MarketingMoneySnapshot{Currency: "CNY", Amount: "0.000000000001", QuotaPerUnit: "9999999999999999", CurrencyPerUnit: "1"}
		data, err = common.Marshal(extreme)
		require.NoError(t, err)
		payment.Conversion, payment.CreditedQuota = string(data), 10000
		amount, err = CalculateMarketingRewardAmount("paid_ratio", "0.0001", MarketingMoneySnapshot{}, &payment)
		require.NoError(t, err)
		assert.Equal(t, int64(1), amount.Quota)
		assert.Equal(t, extreme, amount.Money)
	})
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
			require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &QuotaData{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				userIDs := db.Unscoped().Model(&User{}).Select("id").Where("username LIKE ?", "mv2_%")
				fundingIDs := db.Model(&MarketingFunding{}).Select("id").Where("user_id IN (?)", userIDs)
				require.NoError(t, db.Where("funding_id IN (?)", fundingIDs).Delete(&QuotaData{}).Error)
				for _, record := range []any{&MarketingAllocation{}, &MarketingDebit{}, &MarketingFundingOperation{}, &MarketingDispatch{}, &MarketingUsageProjection{}, &MarketingRealtimeRound{}, &MarketingRealtimeEvent{}} {
					require.NoError(t, db.Where("funding_id IN (?)", fundingIDs).Delete(record).Error)
				}
				tokenIDs := db.Unscoped().Model(&Token{}).Select("id").Where("user_id IN (?)", userIDs)
				require.NoError(t, db.Where("token_id IN (?)", tokenIDs).Delete(&MarketingTokenHold{}).Error)
				require.NoError(t, db.Unscoped().Where("user_id IN (?)", userIDs).Delete(&Token{}).Error)
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

			t.Run("funding", func(t *testing.T) { testMarketingFunding(t, db, other, dialect) })
			t.Run("realtime", func(t *testing.T) { testMarketingRealtime(t, db, other) })

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

type marketingGiftFixture struct{ amount, expires int64 }

func marketingFundingFixture(t *testing.T, db *gorm.DB, name string, paid, limit int64, gifts []marketingGiftFixture) (User, Token, []MarketingGift) {
	t.Helper()
	user := User{Username: "mv2_" + name, AffCode: "mv2_" + name}
	token := Token{Key: "mv2-test-only-" + name, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: int(limit)}
	awards := make([]MarketingGift, 0, len(gifts))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		if err := tx.Create(&MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: MarketingPaymentHistoryNone, PaymentHistoryEvidence: "registration:" + name}).Error; err != nil {
			return err
		}
		token.UserId = user.Id
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		if paid > 0 {
			_, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "funding-" + name, PaidCents: paid, PostedAt: 50, Money: MarketingMoneySnapshot{Amount: strconv.FormatInt(paid, 10), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			if err != nil {
				return err
			}
		}
		for i, gift := range gifts {
			award, err := GrantMarketingGift(tx, MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: name + "-gift-" + strconv.Itoa(i), GrantedAt: 50, ExpiresAt: gift.expires, Money: MarketingMoneySnapshot{Amount: strconv.FormatInt(gift.amount, 10), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			if err != nil {
				return err
			}
			awards = append(awards, *award)
		}
		return nil
	}))
	return user, token, awards
}

func reserveMarketingTest(t *testing.T, db *gorm.DB, user User, token Token, reference string, at, amount int64) *MarketingFunding {
	t.Helper()
	var funding *MarketingFunding
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		funding, err = ReserveMarketingFunding(tx, MarketingReservationInput{UserID: user.Id, TokenID: token.Id, RequestReference: reference, QualifiedAt: at, Quota: amount, PricingEvidence: "fixed-price:test-v1"})
		return err
	}))
	return funding
}

func settleMarketingTest(t *testing.T, db *gorm.DB, funding *MarketingFunding, amount *int64, final bool, kind string) *MarketingFunding {
	t.Helper()
	var result *MarketingFunding
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = SettleMarketingFunding(tx, MarketingSettlementInput{UserID: funding.UserID, FundingID: funding.ID, Version: funding.Revision + 1, ActualQuota: amount, Final: final, EvidenceKind: kind, Evidence: "trusted-test-usage"})
		return err
	}))
	return result
}

func assertMarketingSources(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	var account MarketingAccount
	var user User
	require.NoError(t, db.First(&account, "user_id = ?", userID).Error)
	require.NoError(t, db.Unscoped().First(&user, userID).Error)
	var gifts []MarketingGift
	require.NoError(t, db.Where("user_id = ?", userID).Find(&gifts).Error)
	for _, gift := range gifts {
		assert.Equal(t, gift.GrantedQuota, gift.RemainingQuota+gift.ConsumedQuota+gift.ExpiredQuota+gift.ClearedQuota)
		assert.GreaterOrEqual(t, gift.RemainingQuota, gift.ReservedQuota)
		var sums struct{ Reserved, Consumed int64 }
		require.NoError(t, db.Model(&MarketingAllocation{}).Where("gift_id = ?", gift.ID).Select("COALESCE(SUM(reserved_quota),0) AS reserved, COALESCE(SUM(consumed_quota),0) AS consumed").Scan(&sums).Error)
		assert.Equal(t, gift.ReservedQuota, sums.Reserved)
		assert.Equal(t, gift.ConsumedQuota, sums.Consumed)
	}
	var paid struct{ Reserved, Consumed int64 }
	ids := db.Model(&MarketingFunding{}).Select("id").Where("user_id = ?", userID)
	require.NoError(t, db.Model(&MarketingAllocation{}).Where("funding_id IN (?) AND gift_id = 0", ids).Select("COALESCE(SUM(reserved_quota),0) AS reserved, COALESCE(SUM(consumed_quota),0) AS consumed").Scan(&paid).Error)
	var credited int64
	require.NoError(t, db.Model(&MarketingPayment{}).Where("user_id = ?", userID).Select("COALESCE(SUM(credited_quota),0)").Scan(&credited).Error)
	assert.Equal(t, account.PaidReserved, paid.Reserved)
	assert.Equal(t, account.OpeningQuota+credited-paid.Consumed, int64(user.Quota))
	assert.GreaterOrEqual(t, int64(user.Quota), account.PaidReserved)
	var tokens []Token
	require.NoError(t, db.Unscoped().Where("user_id = ?", userID).Find(&tokens).Error)
	for _, token := range tokens {
		var hold MarketingTokenHold
		err := db.First(&hold, "token_id = ?", token.Id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		require.NoError(t, err)
		var totals struct{ Reserved, Consumed int64 }
		require.NoError(t, db.Model(&MarketingFunding{}).Where("token_id = ?", token.Id).Select("COALESCE(SUM(reserved_quota),0) AS reserved, COALESCE(SUM(settled_quota),0) AS consumed").Scan(&totals).Error)
		assert.Equal(t, totals.Reserved, hold.ReservedQuota)
		assert.Equal(t, totals.Consumed, int64(token.UsedQuota))
		if !token.UnlimitedQuota {
			assert.GreaterOrEqual(t, int64(token.RemainQuota), hold.ReservedQuota)
		}
	}
}

func testMarketingFunding(t *testing.T, db, other *gorm.DB, dialect string) {
	t.Run("consumption_and_log_snapshot_commit_together", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "usage-snapshot", 100, 100, []marketingGiftFixture{{20, 200}})
		funding := reserveMarketingTest(t, db, user, token, "usage-snapshot", 100, 30)
		actual := int64(40)
		input := MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 1, ActualQuota: &actual, Final: false, EvidenceKind: "upstream_progress", Evidence: "provider measured cumulative usage"}
		record := Log{UserId: user.Id, RequestId: "usage-snapshot", Username: user.Username, PromptTokens: 4, CompletionTokens: 6}
		// A malformed log cannot leave finance committed without recovery data.
		record.Other = "invalid"
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := SettleMarketingConsumption(tx, input, record)
			return err
		}))
		require.NoError(t, db.First(funding, funding.ID).Error)
		assert.Zero(t, funding.SettledQuota)
		assert.Zero(t, funding.Revision)
		record.Other = "{}"
		for range 2 {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				funding, err = SettleMarketingConsumption(tx, input, record)
				return err
			}))
		}
		var projections []MarketingUsageProjection
		require.NoError(t, db.Where("funding_id = ?", funding.ID).Find(&projections).Error)
		require.Len(t, projections, 1)
		assert.Zero(t, projections[0].PublishedAt, "financial commit does not require a working log database")
		var frozen Log
		require.NoError(t, common.UnmarshalJsonStr(projections[0].Payload, &frozen))
		assert.Equal(t, 40, frozen.Quota)
		assert.EqualValues(t, 100, frozen.CreatedAt)
		var metadata struct {
			Funding struct {
				Paid int64 `json:"paid_quota"`
				Gift int64 `json:"gift_quota"`
			} `json:"funding"`
		}
		require.NoError(t, common.UnmarshalJsonStr(frozen.Other, &metadata))
		assert.EqualValues(t, 20, metadata.Funding.Paid)
		assert.EqualValues(t, 20, metadata.Funding.Gift)
		var contribution QuotaData
		require.NoError(t, db.Where("funding_id = ?", funding.ID).First(&contribution).Error)
		assert.Equal(t, 1, contribution.Count)
		assert.Equal(t, 40, contribution.Quota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 40, user.UsedQuota)
		assert.Equal(t, 1, user.RequestCount)
		// A lower cumulative final price reverses its real source and updates
		// the same dashboard contribution, never a second request or refund row.
		revised := int64(30)
		final := MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 2, ActualQuota: &revised, Final: true, EvidenceKind: "upstream_terminal", Evidence: "provider final cumulative price"}
		for range 2 {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := SettleMarketingConsumption(tx, final, record)
				return err
			}))
		}
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 90, user.Quota)
		assert.Equal(t, 30, user.UsedQuota)
		assert.Equal(t, 1, user.RequestCount)
		require.NoError(t, db.Where("funding_id = ?", funding.ID).First(&contribution).Error)
		assert.Equal(t, 30, contribution.Quota)
		assert.Equal(t, 1, contribution.Count)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("playground_uses_assets_without_token_budget", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "playground", 100, 100, []marketingGiftFixture{{30, 200}})
		input := MarketingReservationInput{UserID: user.Id, Playground: true, RequestReference: "playground", QualifiedAt: 100, Quota: 40, PricingEvidence: "fixed:test"}
		var funding *MarketingFunding
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			funding, err = ReserveMarketingFunding(tx, input)
			return err
		}))
		actual := int64(50)
		funding = settleMarketingTest(t, db, funding, &actual, false, "upstream_progress")
		actual = 10
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "settled", funding.State)
		assertMarketingSources(t, db, user.Id)
		require.NoError(t, db.First(&token, token.Id).Error)
		assert.Equal(t, 100, token.RemainQuota)
		assert.Zero(t, token.UsedQuota)
		var count int64
		require.NoError(t, db.Model(&MarketingTokenHold{}).Where("token_id = 0").Count(&count).Error)
		assert.Zero(t, count, "playground must not invent a shared token hold")
		input.RequestReference = "playground-invalid"
		input.Playground = false
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := ReserveMarketingFunding(tx, input)
			return err
		}), ErrMarketingAccess)
		input.Playground = true
		require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := ReserveMarketingFunding(tx, input)
			return err
		}), ErrMarketingAccess)
	})
	t.Run("dispatch_and_reservation_are_durable", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "dispatch", 100, 100, []marketingGiftFixture{{20, 200}})
		funding := reserveMarketingTest(t, db, user, token, "dispatch-funding", 100, 10)
		for range 2 {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := IncreaseMarketingReservation(tx, user.Id, funding.ID, 40)
				return err
			}))
		}
		require.NoError(t, db.First(funding, funding.ID).Error)
		assert.EqualValues(t, 40, funding.ReservedQuota)
		assert.Zero(t, funding.SettledQuota)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := IncreaseMarketingReservation(tx, user.Id, funding.ID, 101)
			return err
		}), ErrMarketingInsufficient)
		input := MarketingDispatchInput{UserID: user.Id, FundingID: funding.ID, Reference: "dispatch-1", PricingEvidence: "final-channel-price:test"}
		var dispatch *MarketingDispatch
		var created bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			dispatch, created, err = BeginMarketingDispatch(tx, input)
			return err
		}))
		assert.True(t, created)
		// Simulate lost acknowledgement: a second connection must not send again.
		require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
			_, created, err := BeginMarketingDispatch(tx, input)
			assert.False(t, created)
			return err
		}))
		zero := int64(0)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := SettleMarketingFunding(tx, MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 1, ActualQuota: &zero, Final: true, EvidenceKind: "pre_dispatch_cancel", Evidence: "local save failed"})
			return err
		}), ErrMarketingConflict)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := IncreaseMarketingReservation(tx, user.Id, funding.ID, 50)
			return err
		}), ErrMarketingConflict)
		input.Reference = "dispatch-2"
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, _, err := BeginMarketingDispatch(tx, input)
			return err
		}), ErrMarketingConflict)
		for range 2 {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				return RejectMarketingDispatch(tx, user.Id, funding.ID, dispatch.ID, "provider explicitly rejected before execution")
			}))
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, created, err := BeginMarketingDispatch(tx, input)
			assert.True(t, created)
			return err
		}))
		funding = settleMarketingTest(t, db, funding, nil, true, "unknown")
		assert.Equal(t, "review", funding.State)
		assert.EqualValues(t, 40, funding.ReservedQuota)
		actual := int64(45)
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "settled", funding.State)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("dispatch_rechecks_owner_and_revocation", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "dispatch-access", 100, 100, nil)
		stranger, _, _ := marketingFundingFixture(t, db, "dispatch-stranger", 100, 100, nil)
		funding := reserveMarketingTest(t, db, user, token, "dispatch-access", 100, 10)
		input := MarketingDispatchInput{UserID: stranger.Id, FundingID: funding.ID, Reference: "dispatch-access", PricingEvidence: "fixed:test"}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, _, err := BeginMarketingDispatch(tx, input)
			return err
		}), ErrMarketingAccess)
		input.UserID = user.Id
		require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("status", common.TokenStatusDisabled).Error)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, _, err := BeginMarketingDispatch(tx, input)
			return err
		}), ErrMarketingAccess)
		zero := int64(0)
		funding = settleMarketingTest(t, db, funding, &zero, true, "pre_dispatch_cancel")
		assert.Equal(t, "settled", funding.State)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("source_order_and_final_replay", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "order", 100, 500, []marketingGiftFixture{{10, 200}, {20, 200}, {30, 0}})
		funding := reserveMarketingTest(t, db, user, token, "fund-order", 100, 45)
		var allocations []MarketingAllocation
		require.NoError(t, db.Where("funding_id = ?", funding.ID).Order("id").Find(&allocations).Error)
		require.Len(t, allocations, 3)
		for i, want := range []int64{10, 20, 15} {
			assert.Equal(t, gifts[i].ID, allocations[i].GiftID)
			assert.Equal(t, want, allocations[i].ReservedQuota)
		}
		actual := int64(75)
		input := MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 1, ActualQuota: &actual, Final: true, EvidenceKind: "upstream_terminal", Evidence: "complete:75"}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; funding, err = SettleMarketingFunding(tx, input); return err }))
		assert.Equal(t, "settled", funding.State)
		assert.EqualValues(t, 75, funding.SettledQuota)
		assert.Zero(t, funding.ReservedQuota)
		require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
			again, err := SettleMarketingFunding(tx, input)
			if err == nil {
				assert.Equal(t, *funding, *again)
			}
			return err
		}))
		input.Evidence = "different"
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := SettleMarketingFunding(tx, input); return err }), ErrMarketingConflict)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 85, user.Quota)
		require.NoError(t, db.First(&token, token.Id).Error)
		assert.Equal(t, 75, token.UsedQuota)
		assert.Equal(t, 425, token.RemainQuota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("original_source_lifo_after_earlier_hold_released", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "lifo", 100, 500, []marketingGiftFixture{{20, 200}})
		a := reserveMarketingTest(t, db, user, token, "lifo-a", 100, 10)
		b := reserveMarketingTest(t, other, user, token, "lifo-b", 100, 0)
		amount := int64(20)
		b = settleMarketingTest(t, db, b, &amount, false, "upstream_progress")
		zero := int64(0)
		a = settleMarketingTest(t, other, a, &zero, true, "pre_dispatch_cancel")
		assert.Equal(t, "settled", a.State)
		amount = 25
		b = settleMarketingTest(t, db, b, &amount, false, "upstream_progress")
		amount = 22
		b = settleMarketingTest(t, other, b, &amount, true, "upstream_terminal")
		var gift MarketingGift
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.EqualValues(t, 12, gift.ConsumedQuota)
		assert.EqualValues(t, 8, gift.RemainingQuota)
		assert.Zero(t, gift.ReservedQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 90, user.Quota, "newest gift debit reverses before the earlier paid debit")
		expired := reserveMarketingTest(t, db, user, token, "after-expiry", 200, 5)
		var allocation MarketingAllocation
		require.NoError(t, db.Where("funding_id = ?", expired.ID).First(&allocation).Error)
		assert.Zero(t, allocation.GiftID)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("gift_replaces_paid_hold_without_overholding_token", func(t *testing.T) {
		user, tokenA, _ := marketingFundingFixture(t, db, "replacehold", 100, 100, []marketingGiftFixture{{60, 200}})
		tokenB := Token{UserId: user.Id, Key: "mv2-test-replacehold-B", RemainQuota: 100, ExpiredTime: -1}
		require.NoError(t, db.Create(&tokenB).Error)
		a := reserveMarketingTest(t, db, user, tokenA, "replace-a", 100, 60)
		b := reserveMarketingTest(t, other, user, tokenB, "replace-b", 100, 80)
		zero := int64(0)
		settleMarketingTest(t, db, a, &zero, true, "pre_dispatch_cancel")
		amount := int64(40)
		b = settleMarketingTest(t, db, b, &amount, false, "upstream_progress")
		assert.EqualValues(t, 40, b.ReservedQuota)
		reserveMarketingTest(t, other, user, tokenB, "replace-c", 100, 20)
		assertMarketingSources(t, db, user.Id)
	})
	if dialect == "mysql" {
		t.Run("settlement_after_old_snapshot", func(t *testing.T) {
			user, token, _ := marketingFundingFixture(t, db, "settlesnapshot", 100, 100, nil)
			stale := db.Begin()
			require.NoError(t, stale.Error)
			defer stale.Rollback()
			var count int64
			require.NoError(t, stale.Model(&MarketingFunding{}).Where("user_id = ?", user.Id).Count(&count).Error)
			assert.Zero(t, count)
			funding := reserveMarketingTest(t, other, user, token, "settlesnapshot", 100, 20)
			amount := int64(10)
			result, err := SettleMarketingFunding(stale, MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 1, ActualQuota: &amount, Final: true, EvidenceKind: "upstream_terminal", Evidence: "terminal:10"})
			require.NoError(t, err)
			assert.Equal(t, "settled", result.State)
			require.NoError(t, stale.Commit().Error)
			assertMarketingSources(t, db, user.Id)
		})
	}

	t.Run("access_checks_and_immutable_versions", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "access", 100, 100, nil)
		funding := reserveMarketingTest(t, db, user, token, "access", 100, 10)
		input := MarketingReservationInput{UserID: user.Id, TokenID: token.Id, RequestReference: "access", QualifiedAt: 100, Quota: 10, PricingEvidence: "fixed-price:test-v1"}
		require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
			again, err := ReserveMarketingFunding(tx, input)
			if err == nil {
				assert.Equal(t, *funding, *again)
			}
			return err
		}))
		input.Quota = 11
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := ReserveMarketingFunding(tx, input); return err }), ErrMarketingConflict)
		otherUser, _, _ := marketingFundingFixture(t, db, "accessother", 100, 100, nil)
		input.UserID = otherUser.Id
		input.RequestReference = "other-owner"
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := ReserveMarketingFunding(tx, input); return err }), ErrMarketingAccess)
		input.UserID = user.Id
		input.RequestReference = "expired"
		require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("expired_time", 150).Error)
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := ReserveMarketingFunding(tx, input); return err }), ErrMarketingAccess, "a past qualification time must not bypass current token expiry")
		amount := int64(10)
		settle := MarketingSettlementInput{UserID: otherUser.Id, FundingID: funding.ID, Version: 1, ActualQuota: &amount, Final: true, EvidenceKind: "upstream_terminal", Evidence: "done"}
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := SettleMarketingFunding(tx, settle); return err }), ErrMarketingAccess)
		settle.UserID = user.Id
		settle.Version = 2
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := SettleMarketingFunding(tx, settle); return err }), ErrMarketingConflict)
		settle.Version = 1
		settle.EvidenceKind = "estimate"
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := SettleMarketingFunding(tx, settle); return err }), ErrMarketingInput)
		// Deleting a credential disallows new work without losing actual in-flight cost.
		require.NoError(t, db.Delete(&token).Error)
		settleMarketingTest(t, db, funding, &amount, true, "upstream_terminal")
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("token_reversal_uses_each_original_debit", func(t *testing.T) {
		for _, unlimited := range []bool{false, true} {
			name := "switchfinite"
			if unlimited {
				name = "switchunlimited"
			}
			user, token, _ := marketingFundingFixture(t, db, name, 100, 100, nil)
			require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("unlimited_quota", unlimited).Error)
			funding := reserveMarketingTest(t, db, user, token, name, 100, 0)
			actual := int64(10)
			funding = settleMarketingTest(t, db, funding, &actual, false, "upstream_progress")
			require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{"unlimited_quota": !unlimited, "remain_quota": 5}).Error)
			actual = 0
			funding = settleMarketingTest(t, other, funding, &actual, true, "upstream_terminal")
			assert.Equal(t, "settled", funding.State)
			require.NoError(t, db.First(&token, token.Id).Error)
			expected := 15
			if unlimited {
				expected = 5
			}
			assert.Equal(t, expected, token.RemainQuota)
			assert.Zero(t, token.UsedQuota)
			assertMarketingSources(t, db, user.Id)
		}
		user, token, _ := marketingFundingFixture(t, db, "switchmixed", 100, 100, nil)
		require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("unlimited_quota", true).Error)
		funding := reserveMarketingTest(t, db, user, token, "switchmixed", 100, 0)
		actual := int64(10)
		funding = settleMarketingTest(t, db, funding, &actual, false, "upstream_progress")
		require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{"unlimited_quota": false, "remain_quota": 5}).Error)
		actual = 12
		funding = settleMarketingTest(t, db, funding, &actual, false, "upstream_progress")
		require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("unlimited_quota", true).Error)
		actual = 16
		funding = settleMarketingTest(t, db, funding, &actual, false, "upstream_progress")
		require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("unlimited_quota", false).Error)
		actual = 11
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		require.NoError(t, db.First(&token, token.Id).Error)
		assert.Equal(t, 4, token.RemainQuota)
		assert.Equal(t, 11, token.UsedQuota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("paid_capacity_preserves_terminal_and_recovers", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "paidcapacity", 100, 500, nil)
		funding := reserveMarketingTest(t, db, user, token, "paidcapacity", 100, 0)
		actual := int64(20)
		funding = settleMarketingTest(t, db, funding, &actual, false, "upstream_progress")
		otherFunding := reserveMarketingTest(t, other, user, token, "paidcapacity-other", 100, 10)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "paidcapacity-topup", PaidCents: 100, PostedAt: 101, Money: MarketingMoneySnapshot{Amount: strconv.FormatInt(common.MaxWalletQuota-80, 10), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			return err
		}))
		actual = 10
		before := *funding
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "review", funding.State)
		assert.EqualValues(t, 10, funding.ActualQuota)
		assert.EqualValues(t, 20, funding.SettledQuota)
		assert.True(t, funding.FinalObserved)
		assert.True(t, funding.CostKnown)
		assert.Equal(t, "reversal_requires_reconciliation", funding.ReviewReason)
		again := settleMarketingTest(t, other, &before, &actual, true, "upstream_terminal")
		assert.Equal(t, *funding, *again)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, common.MaxWalletQuota, user.Quota)
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error {
			_, err := ReserveMarketingFunding(tx, MarketingReservationInput{UserID: user.Id, TokenID: token.Id, RequestReference: "capacity-blocked", QualifiedAt: 102, Quota: 1, PricingEvidence: "fixed:test"})
			return err
		}), ErrMarketingReview)
		settleMarketingTest(t, other, otherFunding, &actual, true, "upstream_terminal")
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "settled", funding.State)
		assert.EqualValues(t, 10, funding.SettledQuota)
		assert.Empty(t, funding.ReviewReason)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, common.MaxWalletQuota, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("gift_capacity_rolls_back_partial_multisource_reversal", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "giftcapacity", 100, 500, []marketingGiftFixture{{10, 200}, {10, 0}})
		funding := reserveMarketingTest(t, db, user, token, "giftcapacity", 100, 0)
		actual := int64(30)
		funding = settleMarketingTest(t, db, funding, &actual, false, "upstream_progress")
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := GrantMarketingGift(tx, MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: "giftcapacity-new", GrantedAt: 101, Money: MarketingMoneySnapshot{Amount: strconv.FormatInt(common.MaxWalletQuota, 10), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			return err
		}))
		otherFunding := reserveMarketingTest(t, other, user, token, "giftcapacity-other", 102, 10)
		actual = 10
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "review", funding.State)
		assert.EqualValues(t, 10, funding.ActualQuota)
		assert.EqualValues(t, 30, funding.SettledQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 90, user.Quota, "earlier paid reversal must roll back when a later gift reversal fails")
		var reversed int64
		require.NoError(t, db.Model(&MarketingLedgerEntry{}).Where("funding_id = ? AND kind = ?", funding.ID, "consume_reversal").Count(&reversed).Error)
		assert.Zero(t, reversed)
		assertMarketingSources(t, db, user.Id)
		settleMarketingTest(t, other, otherFunding, &actual, true, "upstream_terminal")
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "settled", funding.State)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 100, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("token_restore_capacity_is_recoverable", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "tokencapacity", 100, 100, nil)
		funding := reserveMarketingTest(t, db, user, token, "tokencapacity", 100, 0)
		actual := int64(20)
		funding = settleMarketingTest(t, db, funding, &actual, false, "upstream_progress")
		require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("remain_quota", common.MaxWalletQuota).Error)
		actual = 10
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "review", funding.State)
		assert.EqualValues(t, 10, funding.ActualQuota)
		assert.EqualValues(t, 20, funding.SettledQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 80, user.Quota)
		require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("remain_quota", common.MaxWalletQuota-10).Error)
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "settled", funding.State)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 90, user.Quota)
		require.NoError(t, db.First(&token, token.Id).Error)
		assert.Equal(t, common.MaxWalletQuota, token.RemainQuota)
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("unknown_preserves_holds_and_known_terminal_recovers", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "unknown", 50, 500, []marketingGiftFixture{{20, 200}})
		funding := reserveMarketingTest(t, db, user, token, "unknown", 100, 30)
		funding = settleMarketingTest(t, db, funding, nil, true, "unknown")
		assert.Equal(t, "review", funding.State)
		assert.False(t, funding.CostKnown)
		assert.EqualValues(t, 30, funding.ReservedQuota)
		partial := int64(5)
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error {
			_, err := SettleMarketingFunding(tx, MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 2, ActualQuota: &partial, Final: false, EvidenceKind: "upstream_progress", Evidence: "partial late progress"})
			return err
		}), ErrMarketingConflict, "partial progress cannot resolve a final unknown cost")
		zero := int64(0)
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error {
			_, err := SettleMarketingFunding(tx, MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 2, ActualQuota: &zero, Final: true, EvidenceKind: "pre_dispatch_cancel", Evidence: "cannot pretend it was never dispatched"})
			return err
		}), ErrMarketingConflict)
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error {
			_, err := ReserveMarketingFunding(tx, MarketingReservationInput{UserID: user.Id, TokenID: token.Id, RequestReference: "blocked-unknown", QualifiedAt: 101, Quota: 1, PricingEvidence: "fixed:test"})
			return err
		}), ErrMarketingReview)
		actual := int64(10)
		funding = settleMarketingTest(t, other, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "settled", funding.State)
		assert.Zero(t, funding.ReservedQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 50, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("insufficient_keeps_true_cost_then_recovers", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "short", 50, 500, []marketingGiftFixture{{10, 200}})
		funding := reserveMarketingTest(t, db, user, token, "short", 100, 30)
		actual := int64(200)
		funding = settleMarketingTest(t, db, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "review", funding.State)
		assert.EqualValues(t, 200, funding.ActualQuota)
		assert.EqualValues(t, 60, funding.SettledQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Zero(t, user.Quota)
		require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
			_, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "short-topup", PaidCents: 200, PostedAt: 201, Money: MarketingMoneySnapshot{Amount: "200", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			return err
		}))
		funding = settleMarketingTest(t, other, funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "settled", funding.State)
		assert.EqualValues(t, 200, funding.SettledQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 60, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("rollback_and_revocation", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rollbackfund", 100, 40, nil)
		funding := reserveMarketingTest(t, db, user, token, "rollbackfund", 100, 30)
		actual := int64(20)
		input := MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 1, ActualQuota: &actual, Final: true, EvidenceKind: "upstream_terminal", Evidence: "terminal20"}
		abort := errors.New("rollback after all financial writes")
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := SettleMarketingFunding(tx, input)
			if err != nil {
				return err
			}
			return abort
		}), abort)
		require.NoError(t, db.First(&funding, funding.ID).Error)
		assert.Zero(t, funding.Revision)
		assert.EqualValues(t, 30, funding.ReservedQuota)
		require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := SettleMarketingFunding(tx, input); return err }))
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error {
			_, err := ReserveMarketingFunding(tx, MarketingReservationInput{UserID: user.Id, TokenID: token.Id, RequestReference: "revoked", QualifiedAt: 101, Quota: 1, PricingEvidence: "fixed:test"})
			return err
		}), ErrMarketingAccess)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("concurrent_holds_and_token_ceiling", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "parallel", 100, 100, nil)
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make([]error, 2)
		for i, conn := range []*gorm.DB{db, other} {
			wg.Go(func() {
				<-start
				results[i] = conn.Transaction(func(tx *gorm.DB) error {
					_, err := ReserveMarketingFunding(tx, MarketingReservationInput{UserID: user.Id, TokenID: token.Id, RequestReference: "parallel-" + strconv.Itoa(i), QualifiedAt: 100, Quota: 80, PricingEvidence: "fixed:test"})
					return err
				})
			})
		}
		close(start)
		wg.Wait()
		wins := 0
		for _, err := range results {
			if err == nil {
				wins++
			} else if dialect == "sqlite" && strings.Contains(strings.ToLower(err.Error()), "locked") {
				continue
			} else {
				require.ErrorIs(t, err, ErrMarketingInsufficient)
			}
		}
		assert.Equal(t, 1, wins)
		assertMarketingSources(t, db, user.Id)
		var funding MarketingFunding
		require.NoError(t, db.Where("user_id = ?", user.Id).First(&funding).Error)
		actual := int64(120)
		result := settleMarketingTest(t, db, &funding, &actual, true, "upstream_terminal")
		assert.Equal(t, "review", result.State)
		assert.EqualValues(t, 100, result.SettledQuota)
		assert.EqualValues(t, 120, result.ActualQuota)
		require.NoError(t, db.First(&token, token.Id).Error)
		assert.Zero(t, token.RemainQuota)
	})
}

func realtimeMarketingTestConnection(t *testing.T, db *gorm.DB, user User, token Token, name string, at, amount int64) *MarketingFunding {
	t.Helper()
	funding := reserveMarketingTest(t, db, user, token, name, at, amount)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		funding, err = InitMarketingRealtimeFunding(tx, user.Id, funding.ID)
		return err
	}))
	return funding
}
func realtimeMarketingTestResponseReference(t *testing.T, fundingID int64, reference string) string {
	t.Helper()
	key, err := marketingDigest([]any{"realtime_round", fundingID, reference})
	require.NoError(t, err)
	return key
}
func realtimeMarketingTestRound(t *testing.T, db *gorm.DB, funding *MarketingFunding, name string, at, reserve int64, observed bool) *MarketingRealtimeResult {
	t.Helper()
	responseReference := ""
	if observed {
		responseReference = realtimeMarketingTestResponseReference(t, funding.ID, name)
	}
	var result *MarketingRealtimeResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = BeginMarketingRealtimeRound(tx, MarketingRealtimeRoundInput{UserID: funding.UserID, FundingID: funding.ID, Reference: name, QualifiedAt: at, ReserveQuota: reserve, Observed: observed, ResponseReference: responseReference, Evidence: "trusted response start"})
		return err
	}))
	return result
}
func realtimeMarketingTestFact(t *testing.T, db *gorm.DB, funding *MarketingFunding, round *MarketingRealtimeRound, key string, usage *dto.RealtimeUsage, cost *int64, terminal bool) *MarketingRealtimeResult {
	t.Helper()
	var result *MarketingRealtimeResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		snap, err := GetMarketingRealtimeSnapshot(tx, funding.UserID, funding.ID)
		if err != nil {
			return err
		}
		result, err = ApplyMarketingRealtimeFact(tx, MarketingRealtimeFactInput{UserID: funding.UserID, FundingID: funding.ID, RoundID: round.ID, EventReference: key, ResponseReference: round.ReferenceKey, ExpectedRevision: snap.Funding.Revision, Usage: usage, CumulativeQuota: cost, Terminal: terminal, Evidence: "trusted response usage"})
		return err
	}))
	return result
}
func testMarketingRealtime(t *testing.T, db, other *gorm.DB) {
	t.Run("zero_estimate_requires_available_funds_except_frozen_free_model", func(t *testing.T) {
		for index, tc := range []struct {
			name                              string
			paid, tokenQuota                  int64
			gifts                             []marketingGiftFixture
			free, unlimited, holdAll, allowed bool
		}{
			{name: "empty_wallet", tokenQuota: 100},
			{name: "empty_token", paid: 100},
			{name: "all_money_reserved_elsewhere", paid: 10, tokenQuota: 100, holdAll: true},
			{name: "expired_gift", tokenQuota: 100, gifts: []marketingGiftFixture{{10, 200}}},
			{name: "future_gift", tokenQuota: 100, gifts: []marketingGiftFixture{{10, 0}}},
			{name: "permanent_gift", tokenQuota: 100, gifts: []marketingGiftFixture{{10, 0}}, allowed: true},
			{name: "paid_available", paid: 10, tokenQuota: 100, allowed: true},
			{name: "true_free_empty_wallet", tokenQuota: 100, free: true, allowed: true},
			{name: "true_free_empty_finite_token", free: true},
			{name: "true_free_unlimited_token", free: true, unlimited: true, allowed: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				name := "rt-zero-admit-" + strconv.Itoa(index)
				user, token, gifts := marketingFundingFixture(t, db, name, tc.paid, tc.tokenQuota, tc.gifts)
				if tc.unlimited {
					require.NoError(t, db.Model(&token).Update("unlimited_quota", true).Error)
					token.UnlimitedQuota = true
				}
				if tc.name == "future_gift" {
					require.NoError(t, db.Model(&gifts[0]).Update("granted_at", 400).Error)
				}
				if tc.holdAll {
					reserveMarketingTest(t, db, user, token, name+"-other", 300, tc.paid)
				}
				pricing := `{"price":{"FreeModel":false}}`
				if tc.free {
					pricing = `{"price":{"FreeModel":true}}`
				}
				var funding *MarketingFunding
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					funding, err = ReserveMarketingFunding(tx, MarketingReservationInput{UserID: user.Id, TokenID: token.Id, RequestReference: name, QualifiedAt: 300, PricingEvidence: pricing})
					if err != nil {
						return err
					}
					funding, err = InitMarketingRealtimeFunding(tx, user.Id, funding.ID)
					return err
				}))
				manual := realtimeMarketingTestRound(t, db, funding, "manual-zero", 300, 0, false)
				assert.Equal(t, !tc.allowed, manual.Stop)
				if !tc.allowed {
					assert.Nil(t, manual.Round)
					assert.False(t, manual.Created)
				}
				observed := realtimeMarketingTestRound(t, other, funding, "observed-zero", 300, 0, true)
				require.NotNil(t, observed.Round, "an observed reply is persisted even when the new round must stop")
				assert.Equal(t, !tc.allowed, observed.Stop)
				assert.NotEmpty(t, observed.Round.ResponseKey)
				assert.Zero(t, observed.Funding.ReservedQuota, "availability checks do not introduce a minimum precharge")
				assert.Zero(t, observed.Funding.ActualQuota)
				if tc.allowed {
					zero := int64(0)
					result := realtimeMarketingTestFact(t, db, funding, observed.Round, "legitimate-zero-done", &dto.RealtimeUsage{}, &zero, true)
					assert.Zero(t, result.Funding.SettledQuota, "trusted zero usage stays zero")
				}
				assertMarketingSources(t, db, user.Id)
			})
		}
	})

	t.Run("zero_estimate_automatic_cannot_borrow_unrelated_input_hold", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-zero-input-backing", 5, 5, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-zero-input-backing", 100, 0)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 5, 100)
			return err
		}))
		var result *MarketingRealtimeResult
		input := MarketingRealtimeRoundInput{UserID: user.Id, FundingID: funding.ID, Reference: "manual-without-commit", QualifiedAt: 100, InputRemainingQuota: 5, InputRemainingPending: true, Evidence: "input exists but no commit was sent"}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, input)
			return err
		}))
		assert.True(t, result.Stop)
		assert.Nil(t, result.Round)
		input.Reference, input.ResponseReference, input.Observed = "unrelated-auto", "provider-auto", true
		require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, input)
			return err
		}))
		assert.True(t, result.Stop)
		require.NotNil(t, result.Round)
		assert.True(t, result.Funding.RealtimeInputPending)
		assert.EqualValues(t, 5, result.Funding.ReservedQuota)
		var allocations []MarketingAllocation
		require.NoError(t, db.Where("funding_id = ? AND round_id = ?", funding.ID, result.Round.ID).Find(&allocations).Error)
		assert.Empty(t, allocations, "the automatic round cannot take the pending input's allocation")
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("observed_start_is_recoverable_before_first_fact_even_without_funds", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-atomic-identity", 10, 10, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-atomic-identity", 100, 0)
		manual := realtimeMarketingTestRound(t, db, funding, "first", 100, 10, false)
		assert.Zero(t, manual.Round.ResponseOrder, "a manual intent is not an observed generation")
		cost := int64(10)
		realtimeMarketingTestFact(t, db, funding, manual.Round, "first-done", &dto.RealtimeUsage{InputTokens: 10, TotalTokens: 10}, &cost, true)
		input := MarketingRealtimeRoundInput{UserID: user.Id, FundingID: funding.ID, Reference: "observed-second", ResponseReference: "provider-response-second", QualifiedAt: 110, ReserveQuota: 1, Observed: true, Evidence: "real automatic created after earlier consumption"}
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, input)
			return err
		}))
		require.True(t, result.Stop)
		require.NotNil(t, result.Round)
		id := result.Round.ID
		assert.EqualValues(t, 2, result.Round.ResponseOrder)
		assert.NotEmpty(t, result.Round.ResponseKey)
		require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
			recovered, err := FindMarketingRealtimeRoundByResponse(tx, user.Id, funding.ID, "provider-response-second")
			if err != nil {
				return err
			}
			assert.Equal(t, id, recovered.ID)
			assert.EqualValues(t, 110, recovered.QualifiedAt)
			assert.EqualValues(t, 2, recovered.ResponseOrder)
			assert.Empty(t, recovered.UsageJSON)
			return nil
		}))
		require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, input)
			return err
		}))
		assert.Equal(t, id, result.Round.ID)
		assert.EqualValues(t, 2, result.Round.ResponseOrder)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			snapshot, err := GetMarketingRealtimeSnapshot(tx, user.Id, funding.ID)
			if err != nil {
				return err
			}
			result, err = ApplyMarketingRealtimeFact(tx, MarketingRealtimeFactInput{UserID: user.Id, FundingID: funding.ID, RoundID: id, ResponseReference: "provider-response-second", EventReference: "start-replayed", ExpectedRevision: snapshot.Funding.Revision, Evidence: "same observed response"})
			return err
		}))
		assert.EqualValues(t, 2, result.Round.ResponseOrder, "later start facts cannot assign a second order")
		input.Reference = "duplicate-new-local-name"
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error { _, err := BeginMarketingRealtimeRound(tx, input); return err }), ErrMarketingConflict)
		var count int64
		require.NoError(t, db.Model(&MarketingRealtimeRound{}).Where("funding_id = ?", funding.ID).Count(&count).Error)
		assert.EqualValues(t, 2, count)
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("manual_metadata_correlation_preserves_response_order_and_evidence", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "rt-correlation", 100, 100, []marketingGiftFixture{{20, 200}})
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-correlation", 100, 0)
		manual := realtimeMarketingTestRound(t, db, funding, "manual-proof", 190, 10, false)
		auto := realtimeMarketingTestRound(t, other, funding, "automatic-first", 210, 5, true)
		cost := int64(5)
		result := realtimeMarketingTestFact(t, other, funding, auto.Round, "automatic-done", &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}, &cost, true)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.EqualValues(t, 95, user.Quota, "unbound manual admission must not reorder automatic billing")
		observed := realtimeMarketingTestRound(t, db, funding, "observed-second", 211, 10, true)
		start := realtimeMarketingTestFact(t, db, funding, observed.Round, "observed-start", nil, nil, false)
		require.EqualValues(t, 2, start.Round.ResponseOrder)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			snapshot, err := GetMarketingRealtimeSnapshot(tx, user.Id, funding.ID)
			if err != nil {
				return err
			}
			_, err = ApplyMarketingRealtimeFact(tx, MarketingRealtimeFactInput{UserID: user.Id, FundingID: funding.ID, RoundID: observed.Round.ID, EventReference: "audio-observed", ExpectedRevision: snapshot.Funding.Revision, OutputObserved: true, AudioOutputObserved: true, Evidence: "actual audio delta"})
			return err
		}))
		for range 2 {
			require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
				var err error
				result, err = CorrelateMarketingRealtimeResponse(tx, user.Id, funding.ID, manual.Round.ID, observed.Round.ID, 10, "echoed server-issued metadata proof")
				return err
			}))
		}
		assert.Equal(t, observed.Round.ID, result.Round.ID)
		assert.EqualValues(t, 190, result.Round.QualifiedAt)
		assert.EqualValues(t, 2, result.Round.ResponseOrder)
		assert.True(t, result.Round.OutputObserved)
		assert.True(t, result.Round.AudioOutputObserved)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			original, err := FindMarketingRealtimeRoundByReference(tx, user.Id, funding.ID, "manual-proof")
			if err != nil {
				return err
			}
			assert.Equal(t, manual.Round.ID, original.ID)
			assert.Equal(t, observed.Round.ID, original.SupersededBy)
			recovered, err := FindMarketingRealtimeRoundByResponse(tx, user.Id, funding.ID, observed.Round.ReferenceKey)
			if err != nil {
				return err
			}
			assert.Equal(t, observed.Round.ID, recovered.ID)
			snapshot, err := GetMarketingRealtimeSnapshot(tx, user.Id, funding.ID)
			if err != nil {
				return err
			}
			require.Len(t, snapshot.Rounds, 2)
			assert.Equal(t, auto.Round.ID, snapshot.Rounds[0].ID)
			assert.Equal(t, observed.Round.ID, snapshot.Rounds[1].ID)
			return nil
		}))
		cost = 35
		result = realtimeMarketingTestFact(t, other, funding, observed.Round, "manual-done", &dto.RealtimeUsage{InputTokens: 29, OutputTokens: 1, TotalTokens: 30, OutputTokenDetails: dto.OutputTokenDetails{AudioTokens: 1}}, &cost, true)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.EqualValues(t, 85, user.Quota)
		assert.EqualValues(t, 35, result.Funding.SettledQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "all observed replies complete")
			return err
		}))
		assert.Equal(t, "settled", result.Funding.State)
		require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CorrelateMarketingRealtimeResponse(tx, user.Id, funding.ID, manual.Round.ID, observed.Round.ID, 10, "echoed server-issued metadata proof")
			return err
		}))
		assert.EqualValues(t, 35, result.Funding.SettledQuota)
		var gift MarketingGift
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.EqualValues(t, 20, gift.ConsumedQuota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("priced_observation_cannot_inherit_manual_gift_qualification", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "rt-correlation-priced", 100, 100, []marketingGiftFixture{{20, 200}})
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-correlation-priced", 100, 0)
		manual := realtimeMarketingTestRound(t, db, funding, "manual", 190, 5, false)
		observed := realtimeMarketingTestRound(t, db, funding, "observed", 210, 5, true)
		cost := int64(5)
		realtimeMarketingTestFact(t, db, funding, observed.Round, "actual-priced", &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}, &cost, true)
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error {
			_, err := CorrelateMarketingRealtimeResponse(tx, user.Id, funding.ID, manual.Round.ID, observed.Round.ID, 5, "late correlation cannot migrate consumed funds")
			return err
		}), ErrMarketingConflict)
		var persisted MarketingFunding
		require.NoError(t, db.First(&persisted, funding.ID).Error)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.EqualValues(t, 95, user.Quota)
		assert.EqualValues(t, 5, persisted.SettledQuota)
		var gift MarketingGift
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.Zero(t, gift.ConsumedQuota)
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("expiry_cross_round_lifo_and_one_log", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "rt-expiry", 100, 100, []marketingGiftFixture{{10, 200}})
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-expiry", 100, 10)
		assert.Zero(t, funding.ReservedQuota, "connection qualification must not pin the initial gift")
		first := realtimeMarketingTestRound(t, db, funding, "first", 190, 10, false)
		require.False(t, first.Stop)
		cost := int64(15)
		result := realtimeMarketingTestFact(t, db, funding, first.Round, "done-first", &dto.RealtimeUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, &cost, true)
		assert.EqualValues(t, 15, result.Funding.SettledQuota)
		second := realtimeMarketingTestRound(t, other, funding, "second", 210, 4, false)
		require.False(t, second.Stop)
		cost = 19
		result = realtimeMarketingTestFact(t, db, funding, second.Round, "second-progress", &dto.RealtimeUsage{InputTokens: 4, TotalTokens: 4}, &cost, false)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 91, user.Quota)
		cost = 12
		result = realtimeMarketingTestFact(t, db, funding, second.Round, "second-final", &dto.RealtimeUsage{InputTokens: 6, TotalTokens: 6}, &cost, true)
		assert.EqualValues(t, 12, result.Funding.SettledQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 98, user.Quota)
		var gift MarketingGift
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.EqualValues(t, 10, gift.ConsumedQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "all responses terminated")
			if err != nil {
				return err
			}
			_, err = RecordMarketingFundingUsage(tx, result.Funding, Log{UserId: user.Id, RequestId: "rt-expiry", PromptTokens: 16, CompletionTokens: 5})
			return err
		}))
		assert.Equal(t, "settled", result.Funding.State)
		assert.Zero(t, result.Funding.ReservedQuota)
		var count int64
		require.NoError(t, db.Model(&MarketingUsageProjection{}).Where("funding_id = ?", funding.ID).Count(&count).Error)
		assert.EqualValues(t, 1, count)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("unknown_round_blocks_and_late_fact_keeps_qualification", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-unknown", 0, 100, []marketingGiftFixture{{10, 200}})
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-unknown", 100, 1)
		first := realtimeMarketingTestRound(t, db, funding, "first", 190, 5, false)
		waiting := reserveMarketingTest(t, db, user, token, "rt-waiting-before-unknown", 190, 1)
		result := realtimeMarketingTestFact(t, db, funding, first.Round, "missing", nil, nil, true)
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error {
			_, _, err := BeginMarketingDispatch(tx, MarketingDispatchInput{UserID: user.Id, FundingID: waiting.ID, Reference: "must-not-dispatch", PricingEvidence: "frozen:test"})
			return err
		}), ErrMarketingReview)
		assert.True(t, result.Stop)
		assert.Equal(t, "review", result.Funding.State)
		assert.EqualValues(t, 5, result.Funding.ReservedQuota)
		blocked := realtimeMarketingTestRound(t, other, funding, "blocked", 210, 1, false)
		assert.True(t, blocked.Stop)
		assert.False(t, blocked.Created)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "disconnected without final usage")
			return err
		}))
		assert.Equal(t, "review", result.Funding.State)
		cost := int64(7)
		result = realtimeMarketingTestFact(t, other, funding, first.Round, "late", &dto.RealtimeUsage{InputTokens: 7, TotalTokens: 7}, &cost, true)
		assert.Equal(t, "settled", result.Funding.State)
		assert.EqualValues(t, 7, result.Funding.SettledQuota)
		assert.Zero(t, result.Funding.ReservedQuota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("observed_unfunded_reply_preserved_old_debt_not_new_gift", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-debt", 0, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-debt", 100, 0)
		first := realtimeMarketingTestRound(t, db, funding, "auto-first", 100, 3, true)
		require.True(t, first.Created)
		require.True(t, first.Stop)
		cost := int64(5)
		result := realtimeMarketingTestFact(t, db, funding, first.Round, "first-done", &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}, &cost, true)
		assert.EqualValues(t, 5, result.Funding.ActualQuota)
		assert.Zero(t, result.Funding.SettledQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := GrantMarketingGift(tx, MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: "later-rt-debt", GrantedAt: 150, Money: MarketingMoneySnapshot{Amount: "20", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			return err
		}))
		second := realtimeMarketingTestRound(t, other, funding, "auto-second", 200, 2, true)
		require.True(t, second.Created)
		require.True(t, second.Stop)
		cost = 7
		result = realtimeMarketingTestFact(t, db, funding, second.Round, "second-progress", &dto.RealtimeUsage{InputTokens: 2, TotalTokens: 2}, &cost, false)
		assert.EqualValues(t, 7, result.Funding.ActualQuota)
		assert.EqualValues(t, 2, result.Funding.SettledQuota)
		var oldDebit int64
		require.NoError(t, db.Model(&MarketingDebit{}).Where("funding_id = ? AND round_id = ?", funding.ID, first.Round.ID).Count(&oldDebit).Error)
		assert.Zero(t, oldDebit)
		cost = 1
		result = realtimeMarketingTestFact(t, db, funding, second.Round, "discount", &dto.RealtimeUsage{InputTokens: 3, TotalTokens: 3}, &cost, true)
		assert.EqualValues(t, 1, result.Funding.ActualQuota)
		assert.EqualValues(t, 1, result.Funding.SettledQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "all terminal")
			return err
		}))
		assert.Equal(t, "settled", result.Funding.State)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("duplicate_terminal_event_and_changed_usage", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-duplicate", 100, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-duplicate", 100, 0)
		round := realtimeMarketingTestRound(t, db, funding, "first", 100, 5, false)
		cost := int64(5)
		usage := &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}
		first := realtimeMarketingTestFact(t, db, funding, round.Round, "done", usage, &cost, true)
		replay := realtimeMarketingTestFact(t, other, funding, round.Round, "done", usage, &cost, true)
		assert.Equal(t, first.Funding.Revision, replay.Funding.Revision)
		replay = realtimeMarketingTestFact(t, other, funding, round.Round, "different-envelope", usage, &cost, true)
		assert.Equal(t, first.Funding.Revision, replay.Funding.Revision)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			snap, err := GetMarketingRealtimeSnapshot(tx, user.Id, funding.ID)
			if err != nil {
				return err
			}
			_, err = ApplyMarketingRealtimeFact(tx, MarketingRealtimeFactInput{UserID: user.Id, FundingID: funding.ID, RoundID: round.Round.ID, ExpectedRevision: snap.Funding.Revision, EventReference: "decreased", Usage: &dto.RealtimeUsage{InputTokens: 4, TotalTokens: 4}, Terminal: true, Evidence: "contradictory supplier fact"})
			return err
		}), ErrMarketingInput)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 95, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("input_admission_requalifies_and_unmatched_disconnect_stays_unknown", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "rt-input", 100, 100, []marketingGiftFixture{{10, 200}})
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-input", 100, 5)
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 8, 190)
			return err
		}))
		require.False(t, result.Stop)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 9, 210)
			return err
		}))
		var gift MarketingGift
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.Zero(t, gift.ReservedQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "unmatched input disconnected")
			return err
		}))
		assert.Equal(t, "review", result.Funding.State)
		assert.EqualValues(t, 9, result.Funding.ReservedQuota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("zero_estimate_input_is_not_confirmed_free_usage", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-zero-input", 100, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-zero-input", 100, 0)
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 0, 100)
			return err
		}))
		assert.True(t, result.Funding.RealtimeInputPending)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "zero estimate input lost")
			return err
		}))
		assert.Equal(t, "review", result.Funding.State)
		assert.False(t, result.Funding.CostKnown)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("unpriced_later_reply_does_not_inherit_old_qualification", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "rt-parked", 100, 100, []marketingGiftFixture{{10, 200}})
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-parked", 100, 0)
		first := realtimeMarketingTestRound(t, db, funding, "first", 190, 0, false)
		second := realtimeMarketingTestRound(t, db, funding, "second", 210, 0, false)
		realtimeMarketingTestFact(t, db, funding, first.Round, "unknown-first", nil, nil, true)
		result := realtimeMarketingTestFact(t, db, funding, second.Round, "known-second", &dto.RealtimeUsage{InputTokens: 7, TotalTokens: 7}, nil, true)
		assert.True(t, result.Stop)
		assert.Zero(t, result.Funding.SettledQuota)
		result = realtimeMarketingTestFact(t, db, funding, first.Round, "late-first", &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}, nil, true)
		assert.True(t, result.Stop)
		cost := int64(5)
		result = realtimeMarketingTestFact(t, db, funding, first.Round, "price-first", &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}, &cost, true)
		assert.EqualValues(t, 5, result.Funding.SettledQuota)
		assert.True(t, result.Stop, "second response still awaits its own price")
		cost = 12
		result = realtimeMarketingTestFact(t, other, funding, second.Round, "price-second", &dto.RealtimeUsage{InputTokens: 7, TotalTokens: 7}, &cost, true)
		assert.False(t, result.Stop)
		assert.EqualValues(t, 12, result.Funding.SettledQuota)
		var gift MarketingGift
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.EqualValues(t, 5, gift.ConsumedQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 93, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("expired_gift_reversal_stays_original_source", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "rt-gift-reverse", 100, 100, []marketingGiftFixture{{10, 200}})
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-gift-reverse", 100, 0)
		first := realtimeMarketingTestRound(t, db, funding, "first", 190, 10, false)
		cost := int64(15)
		realtimeMarketingTestFact(t, db, funding, first.Round, "first-done", &dto.RealtimeUsage{InputTokens: 15, TotalTokens: 15}, &cost, true)
		second := realtimeMarketingTestRound(t, db, funding, "second", 210, 0, false)
		cost = 4
		result := realtimeMarketingTestFact(t, db, funding, second.Round, "discount", &dto.RealtimeUsage{InputTokens: 1, TotalTokens: 1}, &cost, true)
		assert.EqualValues(t, 4, result.Funding.SettledQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 100, user.Quota)
		var gift MarketingGift
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.EqualValues(t, 6, gift.ReservedQuota)
		assert.EqualValues(t, 4, gift.ConsumedQuota)
		third := realtimeMarketingTestRound(t, db, funding, "third", 220, 2, false)
		cost = 6
		result = realtimeMarketingTestFact(t, db, funding, third.Round, "third-done", &dto.RealtimeUsage{InputTokens: 2, TotalTokens: 2}, &cost, true)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 98, user.Quota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "all done")
			return err
		}))
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.Zero(t, gift.ReservedQuota)
		assert.EqualValues(t, 6, gift.RemainingQuota)
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("reversal_capacity_keeps_actual_cost_and_evidence", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-reverse-bound", 100, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-reverse-bound", 100, 0)
		first := realtimeMarketingTestRound(t, db, funding, "first", 100, 5, false)
		cost := int64(5)
		realtimeMarketingTestFact(t, db, funding, first.Round, "first-done", &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}, &cost, true)
		credit := int64(common.MaxWalletQuota - 95)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "rt-capacity-topup", PaidCents: 100, PostedAt: 110, Money: MarketingMoneySnapshot{Amount: strconv.FormatInt(credit, 10), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			return err
		}))
		second := realtimeMarketingTestRound(t, db, funding, "second", 120, 0, false)
		cost = 2
		result := realtimeMarketingTestFact(t, db, funding, second.Round, "second-done", &dto.RealtimeUsage{InputTokens: 1, TotalTokens: 1}, &cost, true)
		assert.Equal(t, "review", result.Funding.State)
		assert.Equal(t, "reversal_requires_reconciliation", result.Funding.ReviewReason)
		assert.EqualValues(t, 2, result.Funding.ActualQuota)
		assert.EqualValues(t, 5, result.Funding.SettledQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.EqualValues(t, common.MaxWalletQuota, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("new_input_after_commit_survives_previous_reply", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-input-after-commit", 100, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-input-after-commit", 100, 0)
		var result *MarketingRealtimeResult
		// A was committed, then B arrived. The provisional hold covers both until
		// the protocol associates response A, leaving B explicitly unmatched.
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 5, 100)
			return err
		}))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, MarketingRealtimeRoundInput{UserID: user.Id, FundingID: funding.ID, Reference: "reply-A", ResponseReference: realtimeMarketingTestResponseReference(t, funding.ID, "reply-A"), QualifiedAt: 100, ReserveQuota: 3, Observed: true, InputRemainingQuota: 2, InputRemainingPending: true, Evidence: "reply A matches committed item A"})
			return err
		}))
		require.True(t, result.Created)
		require.False(t, result.Stop)
		require.True(t, result.Funding.RealtimeInputPending)
		cost := int64(3)
		result = realtimeMarketingTestFact(t, db, funding, result.Round, "reply-A-done", &dto.RealtimeUsage{InputTokens: 3, TotalTokens: 3}, &cost, true)
		assert.True(t, result.Funding.RealtimeInputPending)
		assert.EqualValues(t, 2, result.Funding.ReservedQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "disconnect while B is unmatched")
			return err
		}))
		assert.Equal(t, "review", result.Funding.State)
		assert.False(t, result.Funding.CostKnown)
		assert.EqualValues(t, 2, result.Funding.ReservedQuota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("late_round_cost_cannot_clear_closed_unmatched_input_review", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-late-pending", 100, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-late-pending", 100, 0)
		waiting := reserveMarketingTest(t, db, user, token, "waiting-before-late-pending", 100, 1)
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, MarketingRealtimeRoundInput{UserID: user.Id, FundingID: funding.ID, Reference: "A", ResponseReference: realtimeMarketingTestResponseReference(t, funding.ID, "A"), QualifiedAt: 100, ReserveQuota: 3, Observed: true, InputRemainingQuota: 2, InputRemainingPending: true, Evidence: "observed A while independent B remains pending"})
			return err
		}))
		round := result.Round
		realtimeMarketingTestFact(t, db, funding, round, "A-unknown", nil, nil, true)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "disconnected with A unknown and B unassociated")
			return err
		}))
		assert.Equal(t, "review", result.Funding.State)
		cost := int64(3)
		for range 2 {
			result = realtimeMarketingTestFact(t, other, funding, round, "A-late-terminal", &dto.RealtimeUsage{InputTokens: 3, TotalTokens: 3}, &cost, true)
			assert.EqualValues(t, 3, result.Funding.ActualQuota)
			assert.EqualValues(t, 3, result.Funding.SettledQuota, "known A must settle without estimating or erasing B")
			assert.Equal(t, "review", result.Funding.State)
			assert.False(t, result.Funding.CostKnown)
			assert.True(t, result.Funding.RealtimeInputPending)
			assert.EqualValues(t, 2, result.Funding.ReservedQuota)
			assert.True(t, result.Stop)
		}
		require.ErrorIs(t, other.Transaction(func(tx *gorm.DB) error {
			_, _, err := BeginMarketingDispatch(tx, MarketingDispatchInput{UserID: user.Id, FundingID: waiting.ID, Reference: "still-blocked", PricingEvidence: "frozen:test"})
			return err
		}), ErrMarketingReview)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 97, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("completed_round_releases_unused_admission_before_next_round", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-release-admission", 100, 200, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-release-admission", 100, 0)
		first := realtimeMarketingTestRound(t, db, funding, "first", 100, 30, false)
		cost := int64(5)
		result := realtimeMarketingTestFact(t, db, funding, first.Round, "first-done", &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}, &cost, true)
		assert.Zero(t, result.Funding.ReservedQuota, "a completed price must not pin its unused estimate until socket close")
		second := realtimeMarketingTestRound(t, other, funding, "second", 110, 80, false)
		require.False(t, second.Stop, "95 remaining paid units can admit an 80-unit next round")
		require.NotNil(t, second.Round)
		cost = 10
		result = realtimeMarketingTestFact(t, other, funding, second.Round, "second-done", &dto.RealtimeUsage{InputTokens: 5, TotalTokens: 5}, &cost, true)
		assert.Zero(t, result.Funding.ReservedQuota)
		assert.EqualValues(t, 10, result.Funding.SettledQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 90, user.Quota)
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("rejected_round_keeps_existing_input_hold", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-failed-start-input", 5, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-failed-start-input", 100, 0)
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 4, 100)
			return err
		}))
		revision := result.Funding.Revision
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, MarketingRealtimeRoundInput{UserID: user.Id, FundingID: funding.ID, Reference: "unaffordable", QualifiedAt: 100, ReserveQuota: 5, InputRemainingQuota: 2, InputRemainingPending: true, Evidence: "client request not forwarded"})
			return err
		}))
		assert.True(t, result.Stop)
		assert.False(t, result.Created)
		assert.Nil(t, result.Round)
		assert.True(t, result.Funding.RealtimeInputPending)
		assert.EqualValues(t, 4, result.Funding.ReservedQuota)
		assert.Equal(t, revision, result.Funding.Revision)
		var count int64
		require.NoError(t, db.Model(&MarketingRealtimeRound{}).Where("funding_id = ?", funding.ID).Count(&count).Error)
		assert.Zero(t, count)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("unfunded_observed_reply_keeps_zero_estimate_pending_input", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-auto-remains-input", 0, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-auto-remains-input", 100, 0)
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 0, 100)
			return err
		}))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, MarketingRealtimeRoundInput{UserID: user.Id, FundingID: funding.ID, Reference: "automatic", ResponseReference: realtimeMarketingTestResponseReference(t, funding.ID, "automatic"), QualifiedAt: 100, ReserveQuota: 1, Observed: true, InputRemainingPending: true, Evidence: "automatic reply unrelated to pending audio"})
			return err
		}))
		assert.True(t, result.Stop)
		require.True(t, result.Created)
		assert.True(t, result.Funding.RealtimeInputPending)
		cost := int64(1)
		result = realtimeMarketingTestFact(t, db, funding, result.Round, "automatic-done", &dto.RealtimeUsage{InputTokens: 1, TotalTokens: 1}, &cost, true)
		assert.True(t, result.Funding.RealtimeInputPending)
		assert.EqualValues(t, 1, result.Funding.ActualQuota)
		assert.Zero(t, result.Funding.SettledQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "pending audio plus unfunded reply")
			return err
		}))
		assert.Equal(t, "review", result.Funding.State)
		assert.False(t, result.Funding.CostKnown)
		assertMarketingSources(t, db, user.Id)
	})

	t.Run("persisted_output_forbids_zero_terminal_and_recovers", func(t *testing.T) {
		for _, priorKnown := range []bool{false, true} {
			name := "rt-observed-output-" + strconv.FormatBool(priorKnown)
			t.Run(name, func(t *testing.T) {
				user, token, _ := marketingFundingFixture(t, db, name, 100, 100, []marketingGiftFixture{{10, 200}})
				funding := realtimeMarketingTestConnection(t, db, user, token, name, 100, 0)
				started := realtimeMarketingTestRound(t, db, funding, "response", 100, 5, false)
				var result *MarketingRealtimeResult
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					snapshot, err := GetMarketingRealtimeSnapshot(tx, user.Id, funding.ID)
					if err != nil {
						return err
					}
					result, err = ApplyMarketingRealtimeFact(tx, MarketingRealtimeFactInput{UserID: user.Id, FundingID: funding.ID, RoundID: started.Round.ID, EventReference: "first-output", ResponseReference: started.Round.ReferenceKey, ExpectedRevision: snapshot.Funding.Revision, OutputObserved: true, Evidence: "nonempty output delta received"})
					return err
				}))
				assert.False(t, result.Stop)
				assert.True(t, result.Round.OutputObserved)
				actual := int64(3)
				knownUsage := &dto.RealtimeUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}
				if priorKnown {
					result = realtimeMarketingTestFact(t, db, funding, started.Round, "known-progress", knownUsage, &actual, false)
				}
				// A separate connection reads durable output evidence; no in-memory flag
				// participates in rejecting the provider's contradictory zero terminal.
				zero := int64(0)
				result = realtimeMarketingTestFact(t, other, funding, started.Round, "contradictory-zero", &dto.RealtimeUsage{}, &zero, true)
				assert.True(t, result.Stop)
				assert.True(t, result.Round.Unknown)
				assert.True(t, result.Round.OutputObserved)
				assert.False(t, result.Funding.CostKnown)
				if priorKnown {
					assert.EqualValues(t, 3, result.Funding.ActualQuota)
					assert.EqualValues(t, 3, result.Funding.SettledQuota)
					require.NotNil(t, result.Round.Usage)
					assert.Equal(t, 2, result.Round.Usage.OutputTokens)
				} else {
					assert.Nil(t, result.Round.Usage)
					assert.EqualValues(t, 5, result.Funding.ReservedQuota)
				}
				assert.Contains(t, result.Funding.UsageEvidence, "output_observed_with_zero_reported_output")
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "zero report unresolved")
					return err
				}))
				assert.Equal(t, "review", result.Funding.State)
				result = realtimeMarketingTestFact(t, other, funding, started.Round, "trusted-late-terminal", knownUsage, &actual, true)
				assert.Equal(t, "settled", result.Funding.State)
				assert.EqualValues(t, 3, result.Funding.SettledQuota)
				assert.Zero(t, result.Funding.ReservedQuota)
				assertMarketingSources(t, db, user.Id)
			})
		}
	})

	t.Run("persisted_audio_output_rejects_text_only_terminal", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-audio-evidence", 100, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-audio-evidence", 100, 0)
		started := realtimeMarketingTestRound(t, db, funding, "response", 100, 5, false)
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			snapshot, err := GetMarketingRealtimeSnapshot(tx, user.Id, funding.ID)
			if err != nil {
				return err
			}
			result, err = ApplyMarketingRealtimeFact(tx, MarketingRealtimeFactInput{UserID: user.Id, FundingID: funding.ID, RoundID: started.Round.ID, EventReference: "first-audio", ResponseReference: started.Round.ReferenceKey, ExpectedRevision: snapshot.Funding.Revision, AudioOutputObserved: true, Evidence: "nonempty audio delta received"})
			return err
		}))
		assert.True(t, result.Round.OutputObserved)
		assert.True(t, result.Round.AudioOutputObserved)
		assert.False(t, result.Stop)
		cost := int64(1)
		result = realtimeMarketingTestFact(t, other, funding, started.Round, "false-text-usage", &dto.RealtimeUsage{OutputTokens: 1, TotalTokens: 1, OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 1}}, &cost, true)
		assert.True(t, result.Stop)
		assert.True(t, result.Round.Unknown)
		assert.False(t, result.Funding.CostKnown)
		assert.Nil(t, result.Round.Usage)
		assert.EqualValues(t, 5, result.Funding.ReservedQuota)
		assert.Contains(t, result.Funding.UsageEvidence, "audio_observed_with_zero_reported_audio_output")
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "audio facts incomplete")
			return err
		}))
		assert.Equal(t, "review", result.Funding.State)
		cost = 3
		result = realtimeMarketingTestFact(t, other, funding, started.Round, "trusted-audio-usage", &dto.RealtimeUsage{OutputTokens: 1, TotalTokens: 1, OutputTokenDetails: dto.OutputTokenDetails{AudioTokens: 1}}, &cost, true)
		assert.Equal(t, "settled", result.Funding.State)
		assert.EqualValues(t, 3, result.Funding.SettledQuota)
		assert.Zero(t, result.Funding.ReservedQuota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("late_input_ack_attaches_existing_round_preserving_new_buffer", func(t *testing.T) {
		user, token, gifts := marketingFundingFixture(t, db, "rt-attach-input", 100, 100, []marketingGiftFixture{{10, 200}})
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-attach-input", 100, 0)
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 5, 100)
			return err
		}))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, MarketingRealtimeRoundInput{UserID: user.Id, FundingID: funding.ID, Reference: "manual-before-ack", QualifiedAt: 100, InputRemainingQuota: 5, InputRemainingPending: true, Evidence: "manual create before commit acknowledgement"})
			return err
		}))
		round := result.Round
		for range 2 {
			require.NoError(t, other.Transaction(func(tx *gorm.DB) error {
				var err error
				result, err = AttachMarketingRealtimeInput(tx, user.Id, funding.ID, round.ID, 3, 2, true)
				return err
			}))
		}
		assert.False(t, result.Stop)
		assert.EqualValues(t, 100, result.Round.QualifiedAt)
		assert.EqualValues(t, 5, result.Funding.ReservedQuota)
		assert.True(t, result.Funding.RealtimeInputPending)
		var gift MarketingGift
		require.NoError(t, db.First(&gift, gifts[0].ID).Error)
		assert.EqualValues(t, 3, gift.ReservedQuota, "late ack uses original response qualification even though that gift is expired now")
		before := *result.Funding
		injected := errors.New("rollback attachment caller")
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := AttachMarketingRealtimeInput(tx, user.Id, funding.ID, round.ID, 6, 1, true)
			if err != nil {
				return err
			}
			return injected
		}), injected)
		var after MarketingFunding
		require.NoError(t, db.First(&after, funding.ID).Error)
		assert.Equal(t, before.Revision, after.Revision)
		assert.Equal(t, before.ReservedQuota, after.ReservedQuota)
		cost := int64(3)
		result = realtimeMarketingTestFact(t, db, funding, round, "manual-done", &dto.RealtimeUsage{InputTokens: 3, TotalTokens: 3}, &cost, true)
		assert.EqualValues(t, 2, result.Funding.ReservedQuota)
		assert.True(t, result.Funding.RealtimeInputPending)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := AttachMarketingRealtimeInput(tx, user.Id, funding.ID, round.ID, 3, 0, false)
			return err
		}), ErrMarketingConflict)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = CloseMarketingRealtimeFunding(tx, user.Id, funding.ID, "later B still unmatched")
			return err
		}))
		assert.Equal(t, "review", result.Funding.State)
		assert.EqualValues(t, 2, result.Funding.ReservedQuota)
		assertMarketingSources(t, db, user.Id)
	})
	t.Run("late_input_ack_insufficiency_is_committed_stop", func(t *testing.T) {
		user, token, _ := marketingFundingFixture(t, db, "rt-attach-insufficient", 5, 100, nil)
		funding := realtimeMarketingTestConnection(t, db, user, token, "rt-attach-insufficient", 100, 0)
		var result *MarketingRealtimeResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = ReserveMarketingRealtimeInput(tx, user.Id, funding.ID, 5, 100)
			return err
		}))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = BeginMarketingRealtimeRound(tx, MarketingRealtimeRoundInput{UserID: user.Id, FundingID: funding.ID, Reference: "already-started", QualifiedAt: 100, InputRemainingQuota: 5, InputRemainingPending: true, PendingCommitAdmission: true, Evidence: "response pending input association"})
			return err
		}))
		round := result.Round
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = AttachMarketingRealtimeInput(tx, user.Id, funding.ID, round.ID, 6, 1, true)
			return err
		}))
		assert.True(t, result.Stop)
		assert.True(t, result.Funding.RealtimeInputPending)
		assert.EqualValues(t, 5, result.Funding.ReservedQuota)
		assert.Equal(t, round.ID, result.Round.ID)
		var persisted MarketingRealtimeRound
		require.NoError(t, db.First(&persisted, round.ID).Error)
		assert.False(t, persisted.Terminal)
		cost := int64(6)
		result = realtimeMarketingTestFact(t, db, funding, round, "real-cost", &dto.RealtimeUsage{InputTokens: 6, TotalTokens: 6}, &cost, true)
		assert.True(t, result.Stop)
		assert.EqualValues(t, 6, result.Funding.ActualQuota)
		assert.EqualValues(t, 4, result.Funding.SettledQuota, "B input keeps its separate reservation")
		assertMarketingSources(t, db, user.Id)
	})

}
