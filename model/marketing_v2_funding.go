package model

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var (
	ErrMarketingInsufficient = errors.New("marketing funds insufficient")
	ErrMarketingReview       = errors.New("marketing funding requires reconciliation")
	ErrMarketingAccess       = errors.New("marketing funding access denied")
)

// Token limits apply to total consumption, including gifts. Holds are separate
// from consumed quota and never go through the legacy Redis/batch-write path.
type MarketingTokenHold struct {
	TokenID       int   `gorm:"primaryKey;autoIncrement:false"`
	ReservedQuota int64 `gorm:"not null"`
}

func (MarketingTokenHold) TableName() string { return "marketing_v2_token_holds" }

type MarketingFundingOperation struct {
	ID          int64  `gorm:"primaryKey"`
	FundingID   int64  `gorm:"not null;uniqueIndex:idx_mv2_funding_version,priority:1"`
	Version     int64  `gorm:"not null;uniqueIndex:idx_mv2_funding_version,priority:2"`
	PayloadHash string `gorm:"size:64;not null"`
	Outcome     string `gorm:"type:text;not null"`
}

func (MarketingFundingOperation) TableName() string { return "marketing_v2_funding_operations" }

// Debit lots retain actual consumption order. Repricing reverses the newest
// outstanding debit first, even if an earlier gift became available again.
type MarketingDebit struct {
	RoundID           int64 `gorm:"not null;index"`
	ID                int64 `gorm:"primaryKey"`
	FundingID         int64 `gorm:"not null;index"`
	GiftID            int64 `gorm:"not null"`
	Quota             int64 `gorm:"not null"`
	ReversedQuota     int64 `gorm:"not null"`
	TokenQuotaCharged bool  `gorm:"not null"`
	Version           int64 `gorm:"not null"`
}

func (MarketingDebit) TableName() string { return "marketing_v2_debits" }

type MarketingReservationInput struct {
	UserID  int
	TokenID int
	// Playground is set only by the authenticated server route, never client JSON.
	Playground       bool
	RequestReference string
	QualifiedAt      int64
	Quota            int64
	// An immutable snapshot/reference sufficient to reconstruct original pricing.
	// Never put API keys or raw request credentials in this field.
	PricingEvidence string
}

type MarketingSettlementInput struct {
	UserID    int
	FundingID int64
	Version   int64
	// Nil means unknown actual cost, never zero or the pre-consumption estimate.
	ActualQuota *int64
	Final       bool
	// upstream_progress, upstream_terminal, pre_dispatch_cancel, unknown, or
	// admin_reconcile (the latter is restricted by the future maintenance caller).
	EvidenceKind string
	Evidence     string
}

// fundingBook is a transaction-local snapshot, not a process-wide balance cache.
// Every mutation holds the authoritative user then token row in that order.
type fundingBook struct {
	roundID       int64
	qualifiedAt   int64
	user          *User
	account       *MarketingAccount
	token         Token
	hasToken      bool
	tokenHold     MarketingTokenHold
	gifts         []MarketingGift
	originalGifts []MarketingGift
	allocations   []MarketingAllocation
}

func loadMarketingFundingBook(tx *gorm.DB, userID, tokenID int) (*fundingBook, error) {
	if tx == nil {
		return nil, ErrMarketingTransaction
	}
	user, account, err := lockMarketingAccount(tx.Unscoped(), userID)
	if err != nil {
		return nil, err
	}
	if tokenID < 0 {
		return nil, ErrMarketingAccess
	}
	book := &fundingBook{user: user, account: account, hasToken: tokenID > 0}
	// Browser playground calls have an authenticated user but no API token.
	// They reserve the same assets, without inventing a token or token budget.
	if book.hasToken {
		// Existing requests still settle after token deletion/revocation.
		if err = lockForUpdate(tx.Unscoped()).First(&book.token, tokenID).Error; err != nil {
			return nil, err
		}
		if book.token.UserId != userID {
			return nil, ErrMarketingAccess
		}
		err = lockForUpdate(tx).First(&book.tokenHold, "token_id = ?", tokenID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			book.tokenHold.TokenID = tokenID
			err = tx.Create(&book.tokenHold).Error
		}
		if err != nil {
			return nil, err
		}
	}
	if user.Quota < 0 || int64(user.Quota) > common.MaxWalletQuota || account.PaidReserved < 0 || book.tokenHold.ReservedQuota < 0 || book.tokenHold.ReservedQuota > common.MaxWalletQuota || book.token.UsedQuota < 0 || int64(book.token.UsedQuota) > common.MaxWalletQuota {
		return nil, ErrMarketingReview
	}
	if !book.token.UnlimitedQuota && (book.token.RemainQuota < 0 || int64(book.token.RemainQuota) > common.MaxWalletQuota) {
		return nil, ErrMarketingReview
	}
	if err = lockForUpdate(tx).Where("user_id = ?", userID).Find(&book.gifts).Error; err != nil {
		return nil, err
	}
	for _, gift := range book.gifts {
		if gift.RemainingQuota < 0 || gift.RemainingQuota > common.MaxWalletQuota || gift.ReservedQuota < 0 || gift.ReservedQuota > gift.RemainingQuota || gift.ConsumedQuota < 0 || gift.ConsumedQuota > common.MaxWalletQuota {
			return nil, ErrMarketingReview
		}
	}
	slices.SortFunc(book.gifts, func(a, b MarketingGift) int {
		if a.ExpiresAt != b.ExpiresAt {
			if a.ExpiresAt == 0 {
				return 1
			}
			if b.ExpiresAt == 0 {
				return -1
			}
			if a.ExpiresAt < b.ExpiresAt {
				return -1
			}
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	book.originalGifts = slices.Clone(book.gifts)
	return book, nil
}

func (book *fundingBook) allocation(fundingID, giftID int64) *MarketingAllocation {
	return book.allocationForRound(fundingID, giftID, book.roundID)
}

func (book *fundingBook) allocationForRound(fundingID, giftID, roundID int64) *MarketingAllocation {
	for i := range book.allocations {
		if book.allocations[i].GiftID == giftID && book.allocations[i].RoundID == roundID {
			return &book.allocations[i]
		}
	}
	book.allocations = append(book.allocations, MarketingAllocation{FundingID: fundingID, GiftID: giftID, RoundID: roundID})
	return &book.allocations[len(book.allocations)-1]
}

func (book *fundingBook) save(tx *gorm.DB, funding *MarketingFunding) error {
	if book.account.Revision == math.MaxInt64 {
		return ErrMarketingInput
	}
	// All fields below are read under database locks. The account revision also
	// rejects stale SQLite writers; callers must retry the whole transaction.
	changed := tx.Model(&MarketingAccount{}).Where("user_id = ? AND revision = ?", book.user.Id, book.account.Revision).Updates(map[string]any{"paid_reserved": book.account.PaidReserved, "revision": book.account.Revision + 1})
	if changed.Error != nil {
		return changed.Error
	}
	if changed.RowsAffected != 1 {
		return ErrMarketingConflict
	}
	if err := tx.Unscoped().Model(&User{}).Where("id = ?", book.user.Id).Update("quota", book.user.Quota).Error; err != nil {
		return err
	}
	if book.hasToken {
		if err := tx.Unscoped().Model(&Token{}).Where("id = ?", book.token.Id).Updates(map[string]any{"remain_quota": book.token.RemainQuota, "used_quota": book.token.UsedQuota}).Error; err != nil {
			return err
		}
		if err := tx.Model(&MarketingTokenHold{}).Where("token_id = ?", book.token.Id).Update("reserved_quota", book.tokenHold.ReservedQuota).Error; err != nil {
			return err
		}
	}
	for i := range book.gifts {
		gift := &book.gifts[i]
		original := book.originalGifts[i]
		if gift.RemainingQuota == original.RemainingQuota && gift.ReservedQuota == original.ReservedQuota && gift.ConsumedQuota == original.ConsumedQuota {
			continue
		}
		if err := tx.Model(gift).Updates(map[string]any{"remaining_quota": gift.RemainingQuota, "reserved_quota": gift.ReservedQuota, "consumed_quota": gift.ConsumedQuota}).Error; err != nil {
			return err
		}
	}
	for i := range book.allocations {
		allocation := &book.allocations[i]
		if allocation.ID == 0 {
			if allocation.ReservedQuota == 0 && allocation.ConsumedQuota == 0 {
				continue
			}
			if err := tx.Create(allocation).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(allocation).Updates(map[string]any{"reserved_quota": allocation.ReservedQuota, "consumed_quota": allocation.ConsumedQuota}).Error; err != nil {
				return err
			}
		}
	}
	return tx.Save(funding).Error
}

// ReserveMarketingFunding runs in the caller's transaction. There is no legacy
// user/token quota call or Redis fallback: database holds are the only authority.
func ReserveMarketingFunding(tx *gorm.DB, input MarketingReservationInput) (*MarketingFunding, error) {
	if input.Playground != (input.TokenID == 0) || input.TokenID < 0 {
		return nil, ErrMarketingAccess
	}
	if input.QualifiedAt <= 0 || input.QualifiedAt > common.GetTimestamp() || input.Quota < 0 || input.Quota > common.MaxWalletQuota || strings.TrimSpace(input.PricingEvidence) == "" || len(input.PricingEvidence) > 65536 {
		return nil, ErrMarketingInput
	}
	key, err := marketingSourceKey("request", input.RequestReference)
	if err != nil {
		return nil, err
	}
	hash, err := marketingDigest(input)
	if err != nil {
		return nil, err
	}
	book, err := loadMarketingFundingBook(tx, input.UserID, input.TokenID)
	if err != nil {
		return nil, err
	}
	// Re-check authorization even for reservation retries; revocation must not
	// turn an old request key into permission to start another upstream request.
	if book.user.Status != common.UserStatusEnabled || book.user.DeletedAt.Valid || (book.hasToken && (book.token.Status != common.TokenStatusEnabled || book.token.DeletedAt.Valid || (book.token.ExpiredTime != -1 && book.token.ExpiredTime <= common.GetTimestamp()))) {
		return nil, ErrMarketingAccess
	}
	if book.account.OriginState != "verified" {
		return nil, ErrMarketingReview
	}
	var prior MarketingFunding
	err = lockForUpdate(tx).Where("request_key = ?", key).First(&prior).Error
	if err == nil {
		if prior.PayloadHash != hash {
			return nil, ErrMarketingConflict
		}
		return &prior, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	// A current locking read avoids a previously established MySQL snapshot.
	var unresolved []MarketingFunding
	if err = lockForUpdate(tx).Where("user_id = ? AND state = ?", input.UserID, "review").Limit(1).Find(&unresolved).Error; err != nil {
		return nil, err
	}
	if len(unresolved) > 0 {
		return nil, ErrMarketingReview
	}
	// Exhausted limited tokens cannot start another request, even when input
	// estimation is zero or a stale shared cache still shows funds.
	if book.hasToken && !book.token.UnlimitedQuota && int64(book.token.RemainQuota)-book.tokenHold.ReservedQuota <= 0 {
		return nil, ErrMarketingInsufficient
	}
	if book.hasToken && (book.tokenHold.ReservedQuota > common.MaxWalletQuota-input.Quota || (!book.token.UnlimitedQuota && int64(book.token.RemainQuota)-book.tokenHold.ReservedQuota < input.Quota)) {
		return nil, ErrMarketingInsufficient
	}
	funding := MarketingFunding{RequestKey: key, PayloadHash: hash, UserID: input.UserID, TokenID: input.TokenID, QualifiedAt: input.QualifiedAt, ReservedQuota: input.Quota, State: "reserved", PricingEvidence: input.PricingEvidence}
	if err = tx.Create(&funding).Error; err != nil {
		return nil, err
	}
	remaining := input.Quota
	for i := range book.gifts {
		gift := &book.gifts[i]
		if gift.GrantedAt > input.QualifiedAt || (gift.ExpiresAt != 0 && gift.ExpiresAt <= input.QualifiedAt) {
			continue
		}
		take := min(remaining, gift.RemainingQuota-gift.ReservedQuota)
		if take == 0 {
			continue
		}
		gift.ReservedQuota += take
		book.allocation(funding.ID, gift.ID).ReservedQuota += take
		remaining -= take
		if remaining == 0 {
			break
		}
	}
	if remaining > 0 {
		if int64(book.user.Quota)-book.account.PaidReserved < remaining {
			return nil, ErrMarketingInsufficient
		}
		book.account.PaidReserved += remaining
		book.allocation(funding.ID, 0).ReservedQuota = remaining
	}
	if book.hasToken {
		book.tokenHold.ReservedQuota += input.Quota
	}
	if err = book.save(tx, &funding); err != nil {
		return nil, err
	}
	return &funding, nil
}

// SettleMarketingFunding applies a monotonically numbered cumulative-cost fact.
// A review outcome is a committed result, not an error to roll back: it retains
// real usage and paid sources while callers stop further upstream generation.
func SettleMarketingFunding(tx *gorm.DB, input MarketingSettlementInput) (*MarketingFunding, error) {
	if input.FundingID <= 0 || input.Version <= 0 || strings.TrimSpace(input.Evidence) == "" || len(input.Evidence) > 65536 {
		return nil, ErrMarketingInput
	}
	if input.ActualQuota != nil && (*input.ActualQuota < 0 || *input.ActualQuota > common.MaxWalletQuota) {
		return nil, ErrMarketingInput
	}
	switch input.EvidenceKind {
	case "upstream_progress":
		if input.Final || input.ActualQuota == nil {
			return nil, ErrMarketingInput
		}
	case "upstream_terminal", "admin_reconcile":
		if !input.Final || input.ActualQuota == nil {
			return nil, ErrMarketingInput
		}
	case "pre_dispatch_cancel":
		if !input.Final || input.ActualQuota == nil || *input.ActualQuota != 0 {
			return nil, ErrMarketingInput
		}
	case "unknown":
		if input.ActualQuota != nil {
			return nil, ErrMarketingInput
		}
	default:
		return nil, ErrMarketingInput
	}
	// Acquire the supplied owner first, then use a current read. A plain
	// discovery query can miss a newly committed request in MySQL's old snapshot.
	if tx == nil {
		return nil, ErrMarketingTransaction
	}
	if _, _, err := lockMarketingAccount(tx.Unscoped(), input.UserID); err != nil {
		return nil, err
	}
	var discovered MarketingFunding
	if err := lockForUpdate(tx).First(&discovered, input.FundingID).Error; err != nil {
		return nil, err
	}
	if discovered.UserID != input.UserID {
		return nil, ErrMarketingAccess
	}
	book, err := loadMarketingFundingBook(tx, input.UserID, discovered.TokenID)
	if err != nil {
		return nil, err
	}
	funding := discovered
	if funding.Realtime {
		return nil, ErrMarketingConflict
	}
	hash, err := marketingDigest(input)
	if err != nil {
		return nil, err
	}
	var prior MarketingFundingOperation
	err = lockForUpdate(tx).Where("funding_id = ? AND version = ?", funding.ID, input.Version).First(&prior).Error
	if err == nil {
		if prior.PayloadHash != hash {
			return nil, ErrMarketingConflict
		}
		var outcome MarketingFunding
		if err = common.UnmarshalJsonStr(prior.Outcome, &outcome); err != nil {
			return nil, err
		}
		return &outcome, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if funding.Revision == math.MaxInt64 || input.Version != funding.Revision+1 || funding.State == "settled" {
		return nil, ErrMarketingConflict
	}
	if funding.FinalObserved && !input.Final {
		return nil, ErrMarketingConflict
	}
	if input.EvidenceKind == "pre_dispatch_cancel" {
		if err = marketingRequestCanDispatch(tx, &funding); err != nil {
			return nil, err
		}
	}

	if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	funding.Revision = input.Version
	funding.UsageEvidence = input.EvidenceKind + ":" + input.Evidence
	funding.FinalObserved = input.Final
	funding.CostKnown = input.ActualQuota != nil
	funding.State = "active"
	funding.ReviewReason = ""
	if input.ActualQuota == nil {
		funding.State = "review"
		funding.ReviewReason = "unknown_cost"
	} else {
		funding.ActualQuota = *input.ActualQuota
		if funding.ActualQuota < funding.SettledQuota {
			// A legitimate terminal cost must survive even if a refund cannot
			// fit today's wallet/token bounds. Undo partial financial reversal,
			// then commit the true cost and pending difference for reconciliation.
			savepoint := fmt.Sprintf("mv2_reverse_%d_%d", funding.ID, funding.Revision)
			if err = tx.SavePoint(savepoint).Error; err != nil {
				return nil, err
			}
			err = book.reverse(tx, &funding, funding.SettledQuota-funding.ActualQuota)
			if errors.Is(err, ErrMarketingInput) || errors.Is(err, ErrMarketingReview) {
				if rollbackErr := tx.RollbackTo(savepoint).Error; rollbackErr != nil {
					return nil, rollbackErr
				}
				book, err = loadMarketingFundingBook(tx, input.UserID, funding.TokenID)
				if err != nil {
					return nil, err
				}
				if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Find(&book.allocations).Error; err != nil {
					return nil, err
				}
				funding.SettledQuota = discovered.SettledQuota
				funding.ReservedQuota = discovered.ReservedQuota
				funding.ReviewReason = "reversal_requires_reconciliation"
			} else if err != nil {
				return nil, err
			}
		}
		if funding.ActualQuota > funding.SettledQuota {
			if err = book.consume(tx, &funding, funding.ActualQuota-funding.SettledQuota); err != nil {
				return nil, err
			}
		}
		if funding.SettledQuota != funding.ActualQuota {
			funding.State = "review"
			if funding.ReviewReason == "" {
				funding.ReviewReason = "insufficient_funds_or_token_limit"
			}
		} else if input.Final {
			if err = book.releaseHolds(&funding, funding.ReservedQuota, -1); err != nil {
				return nil, err
			}
			funding.State = "settled"
		}
	}
	if err = book.save(tx, &funding); err != nil {
		return nil, err
	}
	outcome, err := common.Marshal(funding)
	if err != nil {
		return nil, err
	}
	if err = tx.Create(&MarketingFundingOperation{FundingID: funding.ID, Version: input.Version, PayloadHash: hash, Outcome: string(outcome)}).Error; err != nil {
		return nil, err
	}
	return &funding, nil
}

// releaseHolds consumes/relinquishes this request's reservation, including when
// a previously unavailable earlier gift now replaces a paid reservation. Token
// holds must fall by the total consumed amount, not only the same-source part.
func (book *fundingBook) releaseHolds(funding *MarketingFunding, amount, preferredGiftID int64) error {
	if amount < 0 || amount > funding.ReservedQuota || (book.hasToken && amount > book.tokenHold.ReservedQuota) {
		return ErrMarketingReview
	}
	released := amount
	for pass := range 2 {
		for i := len(book.allocations) - 1; i >= 0 && amount > 0; i-- {
			allocation := &book.allocations[i]
			if allocation.RoundID != book.roundID {
				continue
			}
			if (pass == 0) != (allocation.GiftID == preferredGiftID) {
				continue
			}
			take := min(amount, allocation.ReservedQuota)
			if take == 0 {
				continue
			}
			if allocation.GiftID == 0 {
				if book.account.PaidReserved < take {
					return ErrMarketingReview
				}
				book.account.PaidReserved -= take
			} else {
				index := slices.IndexFunc(book.gifts, func(g MarketingGift) bool { return g.ID == allocation.GiftID })
				if index < 0 || book.gifts[index].ReservedQuota < take {
					return ErrMarketingReview
				}
				book.gifts[index].ReservedQuota -= take
			}
			allocation.ReservedQuota -= take
			amount -= take
		}
	}
	if amount != 0 {
		return ErrMarketingReview
	}
	funding.ReservedQuota -= released
	if book.hasToken {
		book.tokenHold.ReservedQuota -= released
	}
	return nil
}

func (book *fundingBook) consume(tx *gorm.DB, funding *MarketingFunding, amount int64) error {
	qualifiedAt := funding.QualifiedAt
	if book.roundID != 0 {
		qualifiedAt = book.qualifiedAt
	}
	var reserved int64
	for _, a := range book.allocations {
		if a.RoundID == book.roundID {
			reserved += a.ReservedQuota
		}
	}
	tokenAvailable := int64(common.MaxWalletQuota)
	if book.hasToken {
		tokenAvailable -= int64(book.token.UsedQuota)
		if !book.token.UnlimitedQuota {
			tokenAvailable = min(tokenAvailable, int64(book.token.RemainQuota)-book.tokenHold.ReservedQuota+reserved)
		}
	}
	amount = min(amount, max(int64(0), tokenAvailable))
	// Gifts and then paid source use current availability, excluding other holds.
	for i := 0; i <= len(book.gifts) && amount > 0; i++ {
		var gift *MarketingGift
		var sourceID int64
		if i < len(book.gifts) {
			gift = &book.gifts[i]
			if gift.GrantedAt > qualifiedAt || (gift.ExpiresAt != 0 && gift.ExpiresAt <= qualifiedAt) {
				continue
			}
			sourceID = gift.ID
		}
		allocation := book.allocation(funding.ID, sourceID)
		available := int64(book.user.Quota) - book.account.PaidReserved + allocation.ReservedQuota
		if gift != nil {
			available = gift.RemainingQuota - gift.ReservedQuota + allocation.ReservedQuota
		}
		take := min(amount, available)
		if take <= 0 {
			continue
		}
		debitAmount, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(take))
		if err != nil {
			return err
		}
		if err = book.releaseHolds(funding, min(take, reserved), sourceID); err != nil {
			return err
		}
		reserved -= min(take, reserved)
		if gift != nil {
			gift.RemainingQuota -= take
			gift.ConsumedQuota += take
		} else {
			book.user.Quota -= debitAmount
		}
		allocation.ConsumedQuota += take
		funding.SettledQuota += take
		if book.hasToken {
			book.token.UsedQuota += debitAmount
			if !book.token.UnlimitedQuota {
				book.token.RemainQuota -= debitAmount
			}
		}
		debit := MarketingDebit{FundingID: funding.ID, RoundID: book.roundID, GiftID: sourceID, Quota: take, Version: funding.Revision, TokenQuotaCharged: book.hasToken && !book.token.UnlimitedQuota}
		if err := tx.Create(&debit).Error; err != nil {
			return err
		}
		key, err := marketingDigest([]any{"debit", debit.ID})
		if err != nil {
			return err
		}
		if err = tx.Create(&MarketingLedgerEntry{OperationKey: key, UserID: funding.UserID, Kind: "consume", FundingID: funding.ID, DebitID: debit.ID, GiftID: sourceID, AmountQuota: take, RecordedAt: common.GetTimestamp()}).Error; err != nil {
			return err
		}
		amount -= take
	}
	return nil
}

func (book *fundingBook) reverse(tx *gorm.DB, funding *MarketingFunding, amount int64) error {
	var debits []MarketingDebit
	if err := lockForUpdate(tx).Where("funding_id = ? AND quota > reversed_quota", funding.ID).Order("id DESC").Find(&debits).Error; err != nil {
		return err
	}
	var giftTotal int64
	for _, gift := range book.gifts {
		if gift.RemainingQuota > common.MaxWalletQuota-giftTotal {
			return ErrMarketingInput
		}
		giftTotal += gift.RemainingQuota
	}
	for i := range debits {
		debit := &debits[i]
		take := min(amount, debit.Quota-debit.ReversedQuota)
		if take <= 0 {
			continue
		}
		debitAmount, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(take))
		if err != nil {
			return err
		}
		allocation := book.allocationForRound(funding.ID, debit.GiftID, debit.RoundID)
		if allocation.ConsumedQuota < take || (book.hasToken && int64(book.token.UsedQuota) < take) {
			return ErrMarketingReview
		}
		// Keep reversed consumption reserved for this in-flight request. Finalization
		// releases it; an expired gift remains expired for every new request.
		if debit.GiftID == 0 {
			if int64(book.user.Quota) > common.MaxWalletQuota-take {
				return ErrMarketingInput
			}
			book.user.Quota += debitAmount
			book.account.PaidReserved += take
		} else {
			index := slices.IndexFunc(book.gifts, func(g MarketingGift) bool { return g.ID == debit.GiftID })
			if index < 0 {
				return ErrMarketingReview
			}
			gift := &book.gifts[index]
			if gift.ConsumedQuota < take || gift.RemainingQuota > common.MaxWalletQuota-take {
				return ErrMarketingReview
			}
			if giftTotal > common.MaxWalletQuota-take {
				return ErrMarketingInput
			}
			giftTotal += take
			gift.RemainingQuota += take
			gift.ReservedQuota += take
			gift.ConsumedQuota -= take
		}
		if (book.hasToken && book.tokenHold.ReservedQuota > common.MaxWalletQuota-take) || funding.ReservedQuota > common.MaxWalletQuota-take {
			return ErrMarketingInput
		}
		if debit.TokenQuotaCharged {
			if int64(book.token.RemainQuota) > common.MaxWalletQuota-take {
				return ErrMarketingInput
			}
			book.token.RemainQuota += debitAmount
		}
		if book.hasToken {
			book.token.UsedQuota -= debitAmount
			book.tokenHold.ReservedQuota += take
		}
		allocation.ConsumedQuota -= take
		allocation.ReservedQuota += take
		funding.SettledQuota -= take
		funding.ReservedQuota += take
		debit.ReversedQuota += take
		if err := tx.Model(debit).Update("reversed_quota", debit.ReversedQuota).Error; err != nil {
			return err
		}
		key, err := marketingDigest([]any{"reverse", funding.ID, funding.Revision, debit.ID})
		if err != nil {
			return err
		}
		if err = tx.Create(&MarketingLedgerEntry{OperationKey: key, UserID: funding.UserID, Kind: "consume_reversal", FundingID: funding.ID, DebitID: debit.ID, GiftID: debit.GiftID, AmountQuota: take, RecordedAt: common.GetTimestamp()}).Error; err != nil {
			return err
		}
		amount -= take
		if amount == 0 {
			return nil
		}
	}
	return ErrMarketingReview
}
