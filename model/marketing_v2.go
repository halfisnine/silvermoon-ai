package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	MarketingPaymentHistoryUnknown = "unknown"
	MarketingPaymentHistoryNone    = "none"
	MarketingPaymentHistoryPaid    = "paid"
	MarketingFirstPaymentUnknown   = "unknown"
	MarketingFirstPaymentConfirmed = "first"
	MarketingFirstPaymentLater     = "later"
)

var (
	ErrMarketingInput       = errors.New("invalid marketing accounting input")
	ErrMarketingConflict    = errors.New("marketing source already has different accounting facts")
	ErrMarketingTransaction = errors.New("marketing accounting requires a database transaction")
)

// These records are not yet connected to payment or relay routes. Their migration
// does not reclassify existing users.quota, enable awards or import old marketing.
type MarketingAccount struct {
	UserID       int   `gorm:"primaryKey;autoIncrement:false"`
	OpeningQuota int64 `gorm:"not null"`
	// Existing balances can contain old gifts. Empty/unknown is never proof of cash.
	OriginState     string `gorm:"size:24;not null"`
	PaidReserved    int64  `gorm:"not null"`
	PaymentSequence int64  `gorm:"not null"`
	// None is established only by the trusted transaction that inserts a NEW
	// user (registration integration, MV2-009), never inferred from zero balance.
	// Legacy discovery defaults to unknown; paid proves a prior payment exists.
	PaymentHistory         string `gorm:"size:16;not null"`
	PaymentHistoryEvidence string `gorm:"size:160;not null"`
	// Only a confirmed global first payment gets this reference. Zero with
	// history=paid means earlier history could not identify the true first.
	FirstPaymentID int64 `gorm:"not null"`
	Revision       int64 `gorm:"not null"`
}

func (MarketingAccount) TableName() string { return "marketing_v2_accounts" }

type MarketingPayment struct {
	ID              int64  `gorm:"primaryKey"`
	SourceKey       string `gorm:"size:64;not null;uniqueIndex"`
	SourceKind      string `gorm:"size:24;not null"`
	SourceReference string `gorm:"size:128;not null"`
	UserID          int    `gorm:"not null;uniqueIndex:idx_mv2_payment_order,priority:1"`
	Sequence        int64  `gorm:"not null;uniqueIndex:idx_mv2_payment_order,priority:2"`
	FirstStatus     string `gorm:"size:16;not null"`
	PaidCents       int64  `gorm:"not null"`
	CreditedQuota   int64  `gorm:"not null"`
	PostedAt        int64  `gorm:"not null;index"`
	PayloadHash     string `gorm:"size:64;not null"`
	Conversion      string `gorm:"type:text;not null"`
}

func (MarketingPayment) TableName() string { return "marketing_v2_payments" }

type MarketingGift struct {
	ID              int64  `gorm:"primaryKey"`
	AwardKey        string `gorm:"size:64;not null;uniqueIndex"`
	UserID          int    `gorm:"not null;index:idx_mv2_gift_order,priority:1"`
	SourceKind      string `gorm:"size:24;not null"`
	SourceReference string `gorm:"size:128;not null"`
	GrantedQuota    int64  `gorm:"not null"`
	RemainingQuota  int64  `gorm:"not null"`
	ReservedQuota   int64  `gorm:"not null"`
	ConsumedQuota   int64  `gorm:"not null"`
	ExpiredQuota    int64  `gorm:"not null"`
	ClearedQuota    int64  `gorm:"not null"`
	GrantedAt       int64  `gorm:"not null"`
	ExpiresAt       int64  `gorm:"not null;index:idx_mv2_gift_order,priority:2"` // 0 = permanent, not unlimited.
	PayloadHash     string `gorm:"size:64;not null"`
	Conversion      string `gorm:"type:text;not null"`
}

func (MarketingGift) TableName() string { return "marketing_v2_gifts" }

type MarketingFunding struct {
	ID              int64  `gorm:"primaryKey"`
	RequestKey      string `gorm:"size:64;not null;uniqueIndex"`
	PayloadHash     string `gorm:"size:64;not null"`
	PricingEvidence string `gorm:"type:text;not null"`
	UsageEvidence   string `gorm:"type:text;not null"`
	ReviewReason    string `gorm:"size:64;not null"`
	CostKnown       bool   `gorm:"not null"`
	FinalObserved   bool   `gorm:"not null"`
	UserID          int    `gorm:"not null;index"`
	TokenID         int    `gorm:"not null"`
	QualifiedAt     int64  `gorm:"not null"`
	ReservedQuota   int64  `gorm:"not null"`
	ActualQuota     int64  `gorm:"not null"`
	SettledQuota    int64  `gorm:"not null"`
	Revision        int64  `gorm:"not null"`
	State           string `gorm:"size:24;not null"`
}

func (MarketingFunding) TableName() string { return "marketing_v2_funding" }

type MarketingAllocation struct {
	ID            int64 `gorm:"primaryKey"`
	FundingID     int64 `gorm:"not null;uniqueIndex:idx_mv2_allocation_source,priority:1"`
	GiftID        int64 `gorm:"not null;uniqueIndex:idx_mv2_allocation_source,priority:2"` // 0 = paid source.
	ReservedQuota int64 `gorm:"not null"`
	ConsumedQuota int64 `gorm:"not null"`
}

func (MarketingAllocation) TableName() string { return "marketing_v2_allocations" }

type MarketingLedgerEntry struct {
	ID           int64  `gorm:"primaryKey"`
	OperationKey string `gorm:"size:64;not null;uniqueIndex"`
	UserID       int    `gorm:"not null;index"`
	Kind         string `gorm:"size:32;not null"`
	PaymentID    int64  `gorm:"not null"`
	GiftID       int64  `gorm:"not null"`
	FundingID    int64  `gorm:"not null"`
	DebitID      int64  `gorm:"not null"`
	AmountQuota  int64  `gorm:"not null"`
	RecordedAt   int64  `gorm:"not null"`
}

func (MarketingLedgerEntry) TableName() string { return "marketing_v2_ledger" }

func migrateMarketingV2(db *gorm.DB) error {
	return db.AutoMigrate(&MarketingAccount{}, &MarketingPayment{}, &MarketingGift{}, &MarketingFunding{}, &MarketingAllocation{}, &MarketingLedgerEntry{}, &MarketingTokenHold{}, &MarketingFundingOperation{}, &MarketingDebit{})
}

// MarketingMoneySnapshot stores the display amount and its actual conversion
// basis. It is internal, not an HTTP schema. Rates are decimal strings so current
// settings cannot reinterpret a historical credit. Actual CNY paid is separate.
type MarketingMoneySnapshot struct {
	Amount          string
	QuotaPerUnit    string
	CurrencyPerUnit string
	Currency        string
}

var marketingDecimal = regexp.MustCompile(`^[0-9]{1,16}(\.[0-9]{1,12})?$`)

func (m MarketingMoneySnapshot) Quota() (int64, error) {
	if len(m.Currency) < 1 || len(m.Currency) > 16 || strings.TrimSpace(m.Currency) != m.Currency {
		return 0, ErrMarketingInput
	}
	values := []string{m.Amount, m.QuotaPerUnit, m.CurrencyPerUnit}
	numbers := make([]decimal.Decimal, 0, len(values))
	for _, value := range values {
		if !marketingDecimal.MatchString(value) {
			return 0, ErrMarketingInput
		}
		n, err := decimal.NewFromString(value)
		if err != nil || !n.IsPositive() {
			return 0, ErrMarketingInput
		}
		numbers = append(numbers, n)
	}
	quota := numbers[0].Mul(numbers[1]).DivRound(numbers[2], 32).Round(0)
	if !quota.IsPositive() || quota.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return 0, ErrMarketingInput
	}
	result, err := common.WalletQuotaFromDecimalStrict(quota)
	if err != nil {
		return 0, ErrMarketingInput
	}
	return int64(result), nil
}

type MarketingPaymentInput struct {
	UserID    int
	Kind      string
	Reference string
	PaidCents int64
	PostedAt  int64
	Money     MarketingMoneySnapshot
}

type MarketingGiftInput struct {
	UserID int
	Kind   string
	// Reference is a stable award identity, including activity/fact/reward item.
	Reference string
	GrantedAt int64
	ExpiresAt int64
	Money     MarketingMoneySnapshot
}

func marketingDigest(value any) (string, error) {
	data, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func marketingSourceKey(kind, reference string) (string, error) {
	if len(reference) == 0 || len(reference) > 128 || strings.TrimSpace(reference) != reference {
		return "", ErrMarketingInput
	}
	return marketingDigest([]string{kind, reference})
}

// lockMarketingAccount uses the same user row for all mutations. SQLite callers
// must retry the entire transaction on BUSY, never only its final write. The CAS
// updates below also prevent a stale account snapshot from being committed.
func lockMarketingAccount(tx *gorm.DB, userID int) (*User, *MarketingAccount, error) {
	if tx == nil || tx.Statement == nil {
		return nil, nil, ErrMarketingTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, nil, ErrMarketingTransaction
	}
	if userID <= 0 {
		return nil, nil, ErrMarketingInput
	}
	// Preserve transaction/Unscoped while isolating GORM statement state between
	// the user and account queries, including chained transaction callers.
	tx = tx.Session(&gorm.Session{})
	var user User
	if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
		return nil, nil, err
	}
	var account MarketingAccount
	err := lockForUpdate(tx).First(&account, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		account = MarketingAccount{UserID: userID, OpeningQuota: int64(user.Quota), OriginState: "unverified", PaymentHistory: MarketingPaymentHistoryUnknown}
		err = tx.Create(&account).Error
	}
	if err != nil {
		return nil, nil, err
	}
	return &user, &account, nil
}

// CreditMarketingPayment must run inside the SAME transaction as the provider
// order/code transition. It does not verify a provider signature or guess actual
// CNY from credited quota. No caller may separately increment users.quota.
func CreditMarketingPayment(tx *gorm.DB, input MarketingPaymentInput) (*MarketingPayment, error) {
	if input.Kind != "online" && input.Kind != "paid_code" && input.Kind != "manual_paid" {
		return nil, ErrMarketingInput
	}
	if input.PaidCents <= 0 || input.PaidCents > common.MaxWalletQuota || input.PostedAt <= 0 {
		return nil, ErrMarketingInput
	}
	quota, err := input.Money.Quota()
	if err != nil {
		return nil, err
	}
	key, err := marketingSourceKey(input.Kind, input.Reference)
	if err != nil {
		return nil, err
	}
	hash, err := marketingDigest(input)
	if err != nil {
		return nil, err
	}
	user, account, err := lockMarketingAccount(tx, input.UserID)
	if err != nil {
		return nil, err
	}
	var prior MarketingPayment
	err = lockForUpdate(tx).Where("source_key = ?", key).First(&prior).Error
	if err == nil {
		if prior.PayloadHash != hash {
			return nil, ErrMarketingConflict
		}
		return &prior, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if user.Quota < 0 || int64(user.Quota) > common.MaxWalletQuota-quota || account.PaymentSequence == math.MaxInt64 || account.Revision == math.MaxInt64 {
		return nil, ErrMarketingInput
	}
	conversion, err := common.Marshal(input.Money)
	if err != nil {
		return nil, err
	}
	firstStatus := MarketingFirstPaymentUnknown
	switch account.PaymentHistory {
	case MarketingPaymentHistoryNone:
		if account.PaymentSequence != 0 || account.FirstPaymentID != 0 || account.PaymentHistoryEvidence == "" {
			return nil, ErrMarketingConflict
		}
		firstStatus = MarketingFirstPaymentConfirmed
	case MarketingPaymentHistoryPaid:
		firstStatus = MarketingFirstPaymentLater
	case "", MarketingPaymentHistoryUnknown:
		// Empty accommodates an unfinished migration without granting eligibility.
	default:
		return nil, ErrMarketingConflict
	}
	event := MarketingPayment{SourceKey: key, SourceKind: input.Kind, SourceReference: input.Reference, UserID: input.UserID, Sequence: account.PaymentSequence + 1, FirstStatus: firstStatus, PaidCents: input.PaidCents, CreditedQuota: quota, PostedAt: input.PostedAt, PayloadHash: hash, Conversion: string(conversion)}
	if err = tx.Create(&event).Error; err != nil {
		return nil, err
	}
	accountUpdates := map[string]any{"payment_sequence": event.Sequence, "revision": account.Revision + 1, "payment_history": MarketingPaymentHistoryPaid}
	if account.PaymentHistory == "" || account.PaymentHistory == MarketingPaymentHistoryUnknown {
		accountUpdates["payment_history_evidence"] = "payment:" + key
	}
	if firstStatus == MarketingFirstPaymentConfirmed {
		accountUpdates["first_payment_id"] = event.ID
	}
	changed := tx.Model(&MarketingAccount{}).Where("user_id = ? AND revision = ?", input.UserID, account.Revision).Updates(accountUpdates)
	if changed.Error != nil {
		return nil, changed.Error
	}
	if changed.RowsAffected != 1 {
		return nil, ErrMarketingConflict
	}
	changed = tx.Model(&User{}).Where("id = ? AND quota = ?", input.UserID, user.Quota).Update("quota", int64(user.Quota)+quota)
	if changed.Error != nil {
		return nil, changed.Error
	}
	if changed.RowsAffected != 1 {
		return nil, ErrMarketingConflict
	}
	ledger := MarketingLedgerEntry{OperationKey: key, UserID: input.UserID, Kind: "paid_credit", PaymentID: event.ID, AmountQuota: quota, RecordedAt: input.PostedAt}
	if err = tx.Create(&ledger).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

// GrantMarketingGift immediately makes a finite gift available, independently
// of users.quota. Retries must use the originally committed grant time/rates.
func GrantMarketingGift(tx *gorm.DB, input MarketingGiftInput) (*MarketingGift, error) {
	switch input.Kind {
	case "registration", "checkin", "recharge", "invitation", "lottery", "admin_gift":
	default:
		return nil, ErrMarketingInput
	}
	if input.GrantedAt <= 0 || input.ExpiresAt < 0 || (input.ExpiresAt != 0 && input.ExpiresAt <= input.GrantedAt) {
		return nil, ErrMarketingInput
	}
	quota, err := input.Money.Quota()
	if err != nil {
		return nil, err
	}
	key, err := marketingSourceKey(input.Kind, input.Reference)
	if err != nil {
		return nil, err
	}
	hash, err := marketingDigest(input)
	if err != nil {
		return nil, err
	}
	_, account, err := lockMarketingAccount(tx, input.UserID)
	if err != nil {
		return nil, err
	}
	var prior MarketingGift
	err = lockForUpdate(tx).Where("award_key = ?", key).First(&prior).Error
	if err == nil {
		if prior.PayloadHash != hash {
			return nil, ErrMarketingConflict
		}
		return &prior, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	// Bound aggregate remaining gifts as well as each gift; no addition overflow.
	// Locking reads are current reads on MySQL even if the caller established
	// an older REPEATABLE READ snapshot before taking the user lock. An ordinary
	// SUM could miss a concurrently committed gift and bypass the wallet bound.
	var balances []MarketingGift
	if err = lockForUpdate(tx).Select("remaining_quota").Where("user_id = ?", input.UserID).Find(&balances).Error; err != nil {
		return nil, err
	}
	var remaining int64
	for _, balance := range balances {
		if balance.RemainingQuota < 0 || balance.RemainingQuota > common.MaxWalletQuota-remaining {
			return nil, ErrMarketingInput
		}
		remaining += balance.RemainingQuota
	}
	if remaining < 0 || remaining > common.MaxWalletQuota-quota || account.Revision == math.MaxInt64 {
		return nil, ErrMarketingInput
	}
	conversion, err := common.Marshal(input.Money)
	if err != nil {
		return nil, err
	}
	gift := MarketingGift{AwardKey: key, UserID: input.UserID, SourceKind: input.Kind, SourceReference: input.Reference, GrantedQuota: quota, RemainingQuota: quota, GrantedAt: input.GrantedAt, ExpiresAt: input.ExpiresAt, PayloadHash: hash, Conversion: string(conversion)}
	if err = tx.Create(&gift).Error; err != nil {
		return nil, err
	}
	changed := tx.Model(&MarketingAccount{}).Where("user_id = ? AND revision = ?", input.UserID, account.Revision).Update("revision", account.Revision+1)
	if changed.Error != nil {
		return nil, changed.Error
	}
	if changed.RowsAffected != 1 {
		return nil, ErrMarketingConflict
	}
	if err = tx.Create(&MarketingLedgerEntry{OperationKey: key, UserID: input.UserID, Kind: "gift_credit", GiftID: gift.ID, AmountQuota: quota, RecordedAt: input.GrantedAt}).Error; err != nil {
		return nil, err
	}
	return &gift, nil
}
