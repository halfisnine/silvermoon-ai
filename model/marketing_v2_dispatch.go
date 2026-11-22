package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// A committed dispatch is permission for exactly one sender, not a retry lease.
// Losing the response or the process never makes it safe to send again. Only a
// positively identified rejection before execution permits another attempt.
type MarketingDispatch struct {
	ID              int64  `gorm:"primaryKey"`
	FundingID       int64  `gorm:"not null;index"`
	ReferenceKey    string `gorm:"size:64;not null;uniqueIndex"`
	PayloadHash     string `gorm:"size:64;not null"`
	State           string `gorm:"size:24;not null"`
	PricingEvidence string `gorm:"type:text;not null"`
	ResultEvidence  string `gorm:"type:text;not null"`
	CreatedAt       int64  `gorm:"not null"`
}

func (MarketingDispatch) TableName() string { return "marketing_v2_dispatches" }

// ObserveMarketingDispatch freezes the safe provider receipt before the task
// row is inserted. A failed task insert must not lose its query/reconcile ID.
// It does not authorize another send or claim that an estimate is final usage.
func ObserveMarketingDispatch(tx *gorm.DB, userID int, fundingID, dispatchID int64, evidence string) error {
	if strings.TrimSpace(evidence) == "" || len(evidence) > 65536 {
		return ErrMarketingInput
	}
	if _, _, err := lockMarketingRequest(tx, userID, fundingID); err != nil {
		return err
	}
	var attempt MarketingDispatch
	if err := lockForUpdate(tx).Where("id = ? AND funding_id = ?", dispatchID, fundingID).First(&attempt).Error; err != nil {
		return err
	}
	if attempt.State != "dispatched" || (attempt.ResultEvidence != "" && attempt.ResultEvidence != evidence) {
		return ErrMarketingConflict
	}
	return tx.Model(&attempt).Update("result_evidence", evidence).Error
}

type MarketingDispatchInput struct {
	UserID          int
	FundingID       int64
	Reference       string
	PricingEvidence string
}

// lockMarketingRequest always acquires the owner lock first, matching credit,
// reservation and settlement. Discovery never relies on an old MVCC snapshot.
func lockMarketingRequest(tx *gorm.DB, userID int, fundingID int64) (*fundingBook, *MarketingFunding, error) {
	if _, _, err := lockMarketingAccount(tx.Unscoped(), userID); err != nil {
		return nil, nil, err
	}
	var funding MarketingFunding
	if err := lockForUpdate(tx).First(&funding, fundingID).Error; err != nil {
		return nil, nil, err
	}
	if funding.UserID != userID {
		return nil, nil, ErrMarketingAccess
	}
	book, err := loadMarketingFundingBook(tx, userID, funding.TokenID)
	return book, &funding, err
}

func (book *fundingBook) authorizeRequest() error {
	if book.user.Status != common.UserStatusEnabled || book.user.DeletedAt.Valid {
		return ErrMarketingAccess
	}
	if book.hasToken && (book.token.Status != common.TokenStatusEnabled || book.token.DeletedAt.Valid || (book.token.ExpiredTime != -1 && book.token.ExpiredTime <= common.GetTimestamp())) {
		return ErrMarketingAccess
	}
	if book.account.OriginState != "verified" {
		return ErrMarketingReview
	}
	return nil
}

func marketingRequestCanDispatch(tx *gorm.DB, funding *MarketingFunding) error {
	if funding.State != "reserved" || funding.Revision != 0 {
		return ErrMarketingConflict
	}
	var attempts []MarketingDispatch
	if err := lockForUpdate(tx).Where("funding_id = ? AND state <> ?", funding.ID, "rejected").Limit(1).Find(&attempts).Error; err != nil {
		return err
	}
	if len(attempts) != 0 {
		return ErrMarketingConflict
	}
	return nil
}

// IncreaseMarketingReservation raises a pre-dispatch estimate, never records it
// as actual consumption. Repeating an absolute target cannot reserve twice.
func IncreaseMarketingReservation(tx *gorm.DB, userID int, fundingID, target int64) (*MarketingFunding, error) {
	return increaseMarketingReservation(tx, userID, fundingID, target, true)
}

// ExtendMarketingReservation may increase an in-flight estimate, but never
// settles it as usage or authorizes a second upstream dispatch.
func ExtendMarketingReservation(tx *gorm.DB, userID int, fundingID, target int64) (*MarketingFunding, error) {
	return increaseMarketingReservation(tx, userID, fundingID, target, false)
}

func increaseMarketingReservation(tx *gorm.DB, userID int, fundingID, target int64, beforeDispatch bool) (*MarketingFunding, error) {
	if tx == nil || fundingID <= 0 || target < 0 || target > common.MaxWalletQuota {
		return nil, ErrMarketingInput
	}
	book, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return nil, err
	}
	if funding.State != "reserved" || funding.Revision != 0 {
		return nil, ErrMarketingConflict
	}
	if beforeDispatch {
		if err = book.authorizeRequest(); err != nil {
			return nil, err
		}
		if err = marketingRequestCanDispatch(tx, funding); err != nil {
			return nil, err
		}
	}
	if target <= funding.ReservedQuota {
		return funding, nil
	}
	delta := target - funding.ReservedQuota
	if book.hasToken && (book.tokenHold.ReservedQuota > common.MaxWalletQuota-delta || (!book.token.UnlimitedQuota && int64(book.token.RemainQuota)-book.tokenHold.ReservedQuota < delta)) {
		return nil, ErrMarketingInsufficient
	}
	if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	remaining := delta
	for i := range book.gifts {
		gift := &book.gifts[i]
		if gift.GrantedAt > funding.QualifiedAt || (gift.ExpiresAt != 0 && gift.ExpiresAt <= funding.QualifiedAt) {
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
		book.allocation(funding.ID, 0).ReservedQuota += remaining
	}
	if book.hasToken {
		book.tokenHold.ReservedQuota += delta
	}
	funding.ReservedQuota = target
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	return funding, nil
}

// BeginMarketingDispatch must commit before sending any upstream bytes. The
// caller may send only when created=true AND the transaction commit succeeded.
// A replay returns created=false even after a crash or lost acknowledgement.
func BeginMarketingDispatch(tx *gorm.DB, input MarketingDispatchInput) (*MarketingDispatch, bool, error) {
	if tx == nil || input.FundingID <= 0 || strings.TrimSpace(input.PricingEvidence) == "" || len(input.PricingEvidence) > 65536 {
		return nil, false, ErrMarketingInput
	}
	key, err := marketingSourceKey("dispatch", input.Reference)
	if err != nil {
		return nil, false, err
	}
	hash, err := marketingDigest(input)
	if err != nil {
		return nil, false, err
	}
	book, funding, err := lockMarketingRequest(tx, input.UserID, input.FundingID)
	if err != nil {
		return nil, false, err
	}
	if err = book.authorizeRequest(); err != nil {
		return nil, false, err
	}
	var existing MarketingDispatch
	err = lockForUpdate(tx).Where("reference_key = ?", key).First(&existing).Error
	if err == nil {
		if existing.PayloadHash != hash {
			return nil, false, ErrMarketingConflict
		}
		return &existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if err = marketingRequestCanDispatch(tx, funding); err != nil {
		return nil, false, err
	}
	attempt := MarketingDispatch{FundingID: funding.ID, ReferenceKey: key, PayloadHash: hash, State: "dispatched", PricingEvidence: input.PricingEvidence, CreatedAt: common.GetTimestamp()}
	if err = tx.Create(&attempt).Error; err != nil {
		return nil, false, err
	}
	// Force a write under the same account revision for SQLite as well.
	if err = book.save(tx, funding); err != nil {
		return nil, false, err
	}
	return &attempt, true, nil
}

// RejectMarketingDispatch accepts ONLY trusted proof of non-execution from the
// server's transport/provider adapter. Network failures, polling timeouts and
// unparseable results are not rejection evidence and must never call this API.
func RejectMarketingDispatch(tx *gorm.DB, userID int, fundingID, dispatchID int64, evidence string) error {
	if tx == nil || fundingID <= 0 || dispatchID <= 0 || strings.TrimSpace(evidence) == "" || len(evidence) > 65536 {
		return ErrMarketingInput
	}
	book, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return err
	}
	var attempt MarketingDispatch
	if err = lockForUpdate(tx).First(&attempt, dispatchID).Error; err != nil {
		return err
	}
	if attempt.FundingID != funding.ID {
		return ErrMarketingAccess
	}
	if attempt.State == "rejected" {
		if attempt.ResultEvidence != evidence {
			return ErrMarketingConflict
		}
		return nil
	}
	if funding.Revision != 0 || funding.State != "reserved" {
		return ErrMarketingConflict
	}
	if err = tx.Model(&attempt).Updates(map[string]any{"state": "rejected", "result_evidence": evidence}).Error; err != nil {
		return err
	}
	return book.save(tx, funding)
}
