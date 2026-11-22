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
			require.NoError(t, db.AutoMigrate(&User{}, &Token{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				userIDs := db.Unscoped().Model(&User{}).Select("id").Where("username LIKE ?", "mv2_%")
				fundingIDs := db.Model(&MarketingFunding{}).Select("id").Where("user_id IN (?)", userIDs)
				for _, record := range []any{&MarketingAllocation{}, &MarketingDebit{}, &MarketingFundingOperation{}} {
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
