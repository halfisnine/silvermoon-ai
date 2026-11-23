package model

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrMarketingClearPreparation = errors.New("marketing qualification preparation required before clearing")

// The singleton orders only marketing facts and rare account clearing. Relay
// accounting never takes this lock. Hold it until the enclosing transaction ends.
type MarketingFactSequence struct {
	ID       int   `gorm:"primaryKey;autoIncrement:false"`
	Sequence int64 `gorm:"not null"`
}

func (MarketingFactSequence) TableName() string { return "marketing_v2_fact_sequence" }
func initializeMarketingFactSequence(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&MarketingFactSequence{ID: 1}).Error
}
func nextMarketingFactSequence(tx *gorm.DB) (int64, error) {
	var row MarketingFactSequence
	if err := lockForUpdate(tx).First(&row, 1).Error; err != nil {
		return 0, err
	}
	if row.Sequence == math.MaxInt64 {
		return 0, ErrMarketingInput
	}
	result := tx.Model(&MarketingFactSequence{}).Where("id = ? AND sequence = ?", 1, row.Sequence).Update("sequence", row.Sequence+1)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, ErrMarketingConflict
	}
	return row.Sequence + 1, nil
}
func marketingFactWasCleared(tx *gorm.DB, userID int, sequence int64) (bool, error) {
	var account MarketingAccount
	err := lockForUpdate(tx).First(&account, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return account.ClearFactSequence > 0 && sequence <= account.ClearFactSequence, err
}

// All fields are durable evidence. A replay returns its original result, never
// the current balance. Reference is globally unique across maintenance kinds.
type MarketingMaintenanceOperation struct {
	ID           int64  `gorm:"primaryKey"`
	OperationKey string `gorm:"size:64;not null;uniqueIndex"`
	ActorID      int    `gorm:"not null"`
	UserID       int    `gorm:"not null;index"`
	Kind         string `gorm:"size:32;not null"`
	PayloadHash  string `gorm:"size:64;not null"`
	Details      string `gorm:"type:text;not null"`
	Result       string `gorm:"type:text;not null"`
	RecordedAt   int64  `gorm:"not null"`
}

func (MarketingMaintenanceOperation) TableName() string { return "marketing_v2_maintenance_operations" }

type MarketingOriginReviewInput struct {
	ActorID, UserID                                   int
	Reference, Evidence, ConversionEvidence           string
	LocalIdentity, AuthorizationReference             string
	ExpectedQuota, ExpectedAffQuota, PaidOpeningQuota int64
	GiftOpeningMoney                                  MarketingMoneySnapshot
	AffDisposition                                    string
	AffGiftMoney                                      MarketingMoneySnapshot
	PaymentHistory                                    string
}
type MarketingAssetClearInput struct {
	ActorID, UserID                         int
	Reference, Evidence, ConversionEvidence string
	LocalIdentity, AuthorizationReference   string
	ExpectedPaidQuota, ExpectedRevision     int64
	ExactRefundCNY                          string
	RefundedCents                           int64
}
type MarketingAssetClearResult struct {
	PaidQuota             int64
	GiftQuota             int64
	ExpiredGiftQuota      int64
	FactSequence          int64
	RefundedCents         int64
	ExactRefundCNY        string
	RoundingDifferenceCNY string
}
type MarketingMaintenanceInventory struct {
	UserID, Status        int
	Quota, AffQuota       int64
	AffHistoryQuota       int64
	Account               *MarketingAccount
	Gifts                 []MarketingGift
	Funding               []MarketingFunding
	Tasks                 []Task
	Midjourney            []Midjourney
	Payments              []MarketingPayment
	Redemptions           []Redemption
	Subscriptions         []UserSubscription
	TopUps                []TopUp
	PendingQualifications int64
}

func lockMarketingMaintenanceUsers(tx *gorm.DB, actorID, userID int) (*User, error) {
	if actorID <= 0 || userID <= 0 {
		return nil, ErrMarketingInput
	}
	ids := []int{actorID, userID}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if err := lockMarketingActivityUsers(tx, ids); err != nil {
		return nil, err
	}
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	var user User
	if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
		return nil, err
	}
	var actor User
	if err := lockForUpdate(tx).First(&actor, actorID).Error; err != nil {
		return nil, err
	}
	if actor.Role != common.RoleRootUser && actor.Role <= user.Role {
		return nil, ErrMarketingAccess
	}
	return &user, nil
}

// Begin/Complete must enclose the financial mutation in ONE transaction. The
// caller is a controlled operator service, never a public actor-ID HTTP API.
func BeginMarketingMaintenanceOperation(tx *gorm.DB, actorID, userID int, kind, reference, evidence, localIdentity, authorizationReference string, payload any, now int64) (*MarketingMaintenanceOperation, bool, error) {
	if strings.TrimSpace(localIdentity) == "" || strings.TrimSpace(authorizationReference) == "" {
		return nil, false, ErrMarketingInput
	}
	if now <= 0 || strings.TrimSpace(evidence) == "" || len(evidence) > 65536 || kind == "" || len(kind) > 32 {
		return nil, false, ErrMarketingInput
	}
	if _, err := lockMarketingMaintenanceUsers(tx, actorID, userID); err != nil {
		return nil, false, err
	}
	key, err := marketingSourceKey("maintenance", reference)
	if err != nil {
		return nil, false, err
	}
	details, err := common.Marshal(struct {
		ActorID, UserID                                       int
		Kind, Evidence, LocalIdentity, AuthorizationReference string
		Payload                                               any
	}{actorID, userID, kind, evidence, localIdentity, authorizationReference, payload})
	if err != nil {
		return nil, false, err
	}
	hash, err := marketingDigest(string(details))
	if err != nil {
		return nil, false, err
	}
	var prior MarketingMaintenanceOperation
	err = lockForUpdate(tx).Where("operation_key = ?", key).First(&prior).Error
	if err == nil {
		if prior.PayloadHash != hash {
			return nil, false, ErrMarketingConflict
		}
		return &prior, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	return &MarketingMaintenanceOperation{OperationKey: key, ActorID: actorID, UserID: userID, Kind: kind, PayloadHash: hash, Details: string(details), RecordedAt: now}, false, nil
}
func CompleteMarketingMaintenanceOperation(tx *gorm.DB, op *MarketingMaintenanceOperation, result any) error {
	if op == nil || op.ID != 0 {
		return ErrMarketingConflict
	}
	data, err := common.Marshal(result)
	if err != nil {
		return err
	}
	op.Result = string(data)
	return tx.Create(op).Error
}

// Read-only inventory deliberately does not call lockMarketingAccount: inspecting
// a formal baseline account must not classify or initialize any money.
func GetMarketingMaintenanceInventory(ctx context.Context, actorID, userID int) (*MarketingMaintenanceInventory, error) {
	var out *MarketingMaintenanceInventory
	err := WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		user, err := lockMarketingMaintenanceUsers(tx, actorID, userID)
		if err != nil {
			return err
		}
		out = &MarketingMaintenanceInventory{UserID: userID, Status: user.Status, Quota: int64(user.Quota), AffQuota: int64(user.AffQuota), AffHistoryQuota: int64(user.AffHistoryQuota)}
		var account MarketingAccount
		if err := lockForUpdate(tx).First(&account, "user_id = ?", userID).Error; err == nil {
			out.Account = &account
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := lockForUpdate(tx).Where("user_id = ?", userID).Order("id").Find(&out.Gifts).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("user_id = ? AND (state <> ? OR reserved_quota <> ?)", userID, "settled", 0).Order("id").Find(&out.Funding).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("user_id = ? AND (status NOT IN ? OR marketing_billing_pending = ? OR (marketing_funding_id = 0 AND quota > 0 AND marketing_legacy_reviewed = ?))", userID, []string{"SUCCESS", "FAILURE"}, true, false).Order("id").Find(&out.Tasks).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("user_id = ? AND (status NOT IN ? OR marketing_billing_pending = ? OR (marketing_funding_id = 0 AND quota > 0 AND marketing_legacy_reviewed = ?))", userID, []string{"SUCCESS", "FAILURE"}, true, false).Order("id").Find(&out.Midjourney).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Order("id").Find(&out.Payments).Error; err != nil {
			return err
		}
		if err := tx.Omit("key").Where("used_user_id = ?", userID).Order("id").Find(&out.Redemptions).Error; err != nil {
			return err
		}
		if err := tx.Omit("completion_evidence").Where("user_id = ?", userID).Order("id").Find(&out.TopUps).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Order("id").Find(&out.Subscriptions).Error; err != nil {
			return err
		}
		return marketingRelatedFacts(tx, userID).Where("state = ?", "captured").Count(&out.PendingQualifications).Error
	})
	return out, err
}
func marketingRelatedFacts(tx *gorm.DB, userID int) *gorm.DB {
	friends := tx.Model(&MarketingInvitation{}).Select("user_id").Where("inviter_id = ?", userID)
	return tx.Model(&MarketingActivityFact{}).Where("(user_id = ? OR user_id IN (?))", userID, friends)
}

// Preparation records eligibility/once-history only, never credits gifts or
// chances. A concurrent fact causes final clear to retry this bounded phase.
func PlanMarketingClearQualificationsAuthorized(ctx context.Context, input MarketingAssetClearInput, limit int) (bool, error) {
	actorID, userID := input.ActorID, input.UserID
	if strings.TrimSpace(input.Evidence) == "" || strings.TrimSpace(input.LocalIdentity) == "" || strings.TrimSpace(input.AuthorizationReference) == "" {
		return false, ErrMarketingInput
	}
	baseKey, err := marketingSourceKey("prepare_clear", input.Reference)
	if err != nil {
		return false, err
	}
	if limit < 1 || limit > 1000 {
		return false, ErrMarketingInput
	}
	for range limit {
		processed := false
		err := WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			// Discover immutable source identity before the sorted user locks.
			var next MarketingActivityFact
			err := marketingRelatedFacts(tx, userID).Where("state = ?", "captured").Order("id").First(&next).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				_, err = marketingClearPreparationPreflight(tx, input)
				return err
			}
			if err != nil {
				return err
			}
			ids := []int{actorID, userID, next.UserID}
			var invitation MarketingInvitation
			err = tx.First(&invitation, "user_id = ?", next.UserID).Error
			if err == nil {
				ids = append(ids, invitation.InviterID)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			slices.Sort(ids)
			ids = slices.Compact(ids)
			if err := lockMarketingActivityUsers(tx, ids); err != nil {
				return err
			}
			if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
				return err
			}
			completed, err := marketingClearPreparationPreflight(tx, input)
			if err != nil {
				return err
			}
			if completed {
				return nil
			}
			// Current read avoids stale REPEATABLE READ eligibility after waiting.
			if err := lockForUpdate(tx).First(&next, next.ID).Error; err != nil {
				return err
			}
			if next.State != "captured" {
				processed = true
				return nil
			}
			var payload marketingActivityPayload
			var snapshots []marketingActivityRuleSnapshot
			if err := common.UnmarshalJsonStr(next.Payload, &payload); err != nil {
				return err
			}
			if err := common.UnmarshalJsonStr(next.Rules, &snapshots); err != nil {
				return err
			}
			for _, snapshot := range snapshots {
				if !slices.Contains(ids, snapshot.UserID) {
					return ErrMarketingConflict
				}
			}
			op, replay, err := BeginMarketingMaintenanceOperation(tx, actorID, userID, "clear_prepare", baseKey+":"+strconv.FormatInt(next.ID, 10), input.Evidence, input.LocalIdentity, input.AuthorizationReference, struct {
				FactID int64
				Clear  MarketingAssetClearInput
			}{next.ID, input}, common.GetTimestamp())
			if err != nil {
				return err
			}
			if replay {
				return ErrMarketingConflict
			}
			if err := planMarketingActivityFact(tx, &next, payload, snapshots); err != nil {
				return err
			}
			processed = true
			return CompleteMarketingMaintenanceOperation(tx, op, map[string]any{"fact_id": next.ID, "state": next.State})
		})
		if err != nil {
			return false, err
		}
		if !processed {
			return false, nil
		}
	}
	return true, nil
}

// A failed or replayed clear must not plan fresh awards as a side effect.
func marketingClearPreparationPreflight(tx *gorm.DB, input MarketingAssetClearInput) (bool, error) {
	if strings.TrimSpace(input.ConversionEvidence) == "" || !marketingDecimal.MatchString(input.ExactRefundCNY) || input.RefundedCents < 0 || input.RefundedCents > common.MaxWalletQuota {
		return false, ErrMarketingInput
	}
	exact, err := decimal.NewFromString(input.ExactRefundCNY)
	if err != nil {
		return false, ErrMarketingInput
	}
	if !exact.Round(2).Mul(decimal.NewFromInt(100)).Equal(decimal.NewFromInt(input.RefundedCents)) {
		return false, ErrMarketingInput
	}
	_, replay, err := BeginMarketingMaintenanceOperation(tx, input.ActorID, input.UserID, "asset_clear", input.Reference, input.Evidence, input.LocalIdentity, input.AuthorizationReference, input, common.GetTimestamp())
	if err != nil || replay {
		return replay, err
	}
	user, account, err := lockMarketingAccount(tx, input.UserID)
	if err != nil {
		return false, err
	}
	if user.Status != common.UserStatusDisabled || account.OriginState != "verified" || int64(user.Quota) != input.ExpectedPaidQuota || account.Revision != input.ExpectedRevision || user.AffQuota != 0 {
		return false, ErrMarketingConflict
	}
	if user.Quota < 0 || account.PaidReserved != 0 {
		return false, ErrMarketingReview
	}
	if (user.Quota == 0) != exact.IsZero() {
		return false, ErrMarketingInput
	}
	return false, marketingAssertSettled(tx, input.UserID)
}

// Unknown final costs and legacy live tasks block both initial source review and
// clearing. Disabling an account is not evidence that an upstream call was free.
func marketingAssertSettled(tx *gorm.DB, userID int) error {
	var funding []MarketingFunding
	if err := lockForUpdate(tx).Where("user_id = ? AND (state <> ? OR reserved_quota <> ? OR cost_known = ? OR final_observed = ?)", userID, "settled", 0, false, false).Find(&funding).Error; err != nil {
		return err
	}
	if len(funding) > 0 {
		return ErrMarketingReview
	}
	var gifts []MarketingGift
	if err := lockForUpdate(tx).Where("user_id = ? AND reserved_quota <> ?", userID, 0).Find(&gifts).Error; err != nil {
		return err
	}
	if len(gifts) > 0 {
		return ErrMarketingReview
	}
	var tasks []Task
	if err := lockForUpdate(tx).Where("user_id = ? AND (status NOT IN ? OR marketing_billing_pending = ? OR (marketing_funding_id = 0 AND quota > 0 AND marketing_legacy_reviewed = ?))", userID, []string{"SUCCESS", "FAILURE"}, true, false).Find(&tasks).Error; err != nil {
		return err
	}
	if len(tasks) > 0 {
		return ErrMarketingReview
	}
	var mj []Midjourney
	if err := lockForUpdate(tx).Where("user_id = ? AND (status NOT IN ? OR marketing_billing_pending = ? OR (marketing_funding_id = 0 AND quota > 0 AND marketing_legacy_reviewed = ?))", userID, []string{"SUCCESS", "FAILURE"}, true, false).Find(&mj).Error; err != nil {
		return err
	}
	if len(mj) > 0 {
		return ErrMarketingReview
	}
	var holds []MarketingTokenHold
	tokens := tx.Unscoped().Model(&Token{}).Select("id").Where("user_id = ?", userID)
	if err := lockForUpdate(tx).Where("token_id IN (?) AND reserved_quota <> ?", tokens, 0).Find(&holds).Error; err != nil {
		return err
	}
	if len(holds) > 0 {
		return ErrMarketingReview
	}
	return nil
}

func ReviewMarketingOrigin(tx *gorm.DB, input MarketingOriginReviewInput, now int64) (*MarketingMaintenanceOperation, error) {
	if strings.TrimSpace(input.ConversionEvidence) == "" || (input.PaymentHistory != MarketingPaymentHistoryUnknown && input.PaymentHistory != MarketingPaymentHistoryPaid) {
		return nil, ErrMarketingInput
	}
	op, replay, err := BeginMarketingMaintenanceOperation(tx, input.ActorID, input.UserID, "origin_review", input.Reference, input.Evidence, input.LocalIdentity, input.AuthorizationReference, input, now)
	if err != nil || replay {
		return op, err
	}
	user, account, err := lockMarketingAccount(tx, input.UserID)
	if err != nil {
		return nil, err
	}
	if user.Quota < 0 || int64(user.Quota) > common.MaxWalletQuota || user.AffQuota < 0 || int64(user.AffQuota) > common.MaxWalletQuota {
		return nil, ErrMarketingInput
	}
	if account.OriginState != "unverified" || account.PaidReserved != 0 || int64(user.Quota) != input.ExpectedQuota || int64(user.AffQuota) != input.ExpectedAffQuota {
		return nil, ErrMarketingConflict
	}
	if err := marketingAssertSettled(tx, input.UserID); err != nil {
		return nil, err
	}
	if account.OpeningQuota < 0 || account.OpeningQuota > common.MaxWalletQuota || account.OpeningAdjustmentQuota < -common.MaxWalletQuota || account.OpeningAdjustmentQuota > common.MaxWalletQuota {
		return nil, ErrMarketingInput
	}
	opening := account.OpeningQuota + account.OpeningAdjustmentQuota
	if opening < 0 || opening > int64(user.Quota) || input.PaidOpeningQuota < 0 || input.PaidOpeningQuota > opening {
		return nil, ErrMarketingInput
	}
	giftQuota := opening - input.PaidOpeningQuota
	if giftQuota > 0 {
		quota, err := input.GiftOpeningMoney.Quota()
		if err != nil || quota != giftQuota {
			return nil, ErrMarketingInput
		}
		_, err = GrantMarketingGift(tx, MarketingGiftInput{UserID: input.UserID, Kind: "admin_gift", Reference: "origin:" + op.OperationKey, GrantedAt: now, Money: input.GiftOpeningMoney})
		if err != nil {
			return nil, err
		}
	}
	switch input.AffDisposition {
	case "none":
		if user.AffQuota != 0 {
			return nil, ErrMarketingInput
		}
	case "clear":
		if user.AffQuota < 0 {
			return nil, ErrMarketingInput
		}
	case "gift":
		quota, err := input.AffGiftMoney.Quota()
		if err != nil || quota != int64(user.AffQuota) {
			return nil, ErrMarketingInput
		}
		_, err = GrantMarketingGift(tx, MarketingGiftInput{UserID: input.UserID, Kind: "admin_gift", Reference: "aff:" + op.OperationKey, GrantedAt: now, Money: input.AffGiftMoney})
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrMarketingInput
	}
	if err := tx.Model(&User{}).Where("id = ?", input.UserID).Updates(map[string]any{"quota": int64(user.Quota) - giftQuota, "aff_quota": 0}).Error; err != nil {
		return nil, err
	}
	// Re-read revision after gift grants. Payment history never regresses.
	if err := lockForUpdate(tx).First(account, "user_id = ?", input.UserID).Error; err != nil {
		return nil, err
	}
	history := input.PaymentHistory
	if account.PaymentHistory == MarketingPaymentHistoryPaid {
		history = MarketingPaymentHistoryPaid
	}
	if account.Revision == math.MaxInt64 {
		return nil, ErrMarketingInput
	}
	if err := tx.Model(account).Updates(map[string]any{"origin_state": "verified", "payment_history": history, "payment_history_evidence": op.OperationKey, "revision": account.Revision + 1}).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(&MarketingLedgerEntry{OperationKey: op.OperationKey, UserID: input.UserID, Kind: "origin_reclassification", AmountQuota: -giftQuota, RecordedAt: now}).Error; err != nil {
		return nil, err
	}
	err = CompleteMarketingMaintenanceOperation(tx, op, map[string]any{"opening_quota": account.OpeningQuota, "paid_opening_quota": input.PaidOpeningQuota, "gift_opening_quota": giftQuota, "aff_quota": user.AffQuota, "aff_disposition": input.AffDisposition, "paid_quota": int64(user.Quota) - giftQuota})
	return op, err
}

func ClearMarketingAssets(tx *gorm.DB, input MarketingAssetClearInput, now int64) (*MarketingMaintenanceOperation, error) {
	if strings.TrimSpace(input.ConversionEvidence) == "" || !marketingDecimal.MatchString(input.ExactRefundCNY) || input.RefundedCents < 0 || input.RefundedCents > common.MaxWalletQuota {
		return nil, ErrMarketingInput
	}
	exact, err := decimal.NewFromString(input.ExactRefundCNY)
	if err != nil {
		return nil, ErrMarketingInput
	}
	rounded := exact.Round(2)
	if !rounded.Mul(decimal.NewFromInt(100)).Equal(decimal.NewFromInt(input.RefundedCents)) {
		return nil, ErrMarketingInput
	}
	op, replay, err := BeginMarketingMaintenanceOperation(tx, input.ActorID, input.UserID, "asset_clear", input.Reference, input.Evidence, input.LocalIdentity, input.AuthorizationReference, input, now)
	if err != nil || replay {
		return op, err
	}
	user, account, err := lockMarketingAccount(tx, input.UserID)
	if err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusDisabled || account.OriginState != "verified" || int64(user.Quota) != input.ExpectedPaidQuota || account.Revision != input.ExpectedRevision || user.AffQuota != 0 {
		return nil, ErrMarketingConflict
	}
	if user.Quota < 0 || account.PaidReserved != 0 {
		return nil, ErrMarketingReview
	}
	if (user.Quota == 0) != exact.IsZero() {
		return nil, ErrMarketingInput
	}
	if err := marketingAssertSettled(tx, input.UserID); err != nil {
		return nil, err
	}
	sequence, err := nextMarketingFactSequence(tx)
	if err != nil {
		return nil, err
	}
	// Every earlier event committed before this barrier. No new user locks after
	// it: qualification preparation acquires source+recipient before this phase.
	var pending []MarketingActivityFact
	if err := lockForUpdate(marketingRelatedFacts(tx, input.UserID)).Where("state = ?", "captured").Limit(1).Find(&pending).Error; err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		return nil, ErrMarketingClearPreparation
	}
	var gifts []MarketingGift
	if err := lockForUpdate(tx).Where("user_id = ?", input.UserID).Order("id").Find(&gifts).Error; err != nil {
		return nil, err
	}
	result := MarketingAssetClearResult{PaidQuota: int64(user.Quota), FactSequence: sequence, RefundedCents: input.RefundedCents, ExactRefundCNY: exact.String(), RoundingDifferenceCNY: rounded.Sub(exact).String()}
	for _, gift := range gifts {
		if gift.RemainingQuota == 0 {
			continue
		}
		if gift.RemainingQuota < 0 || gift.ReservedQuota != 0 {
			return nil, ErrMarketingReview
		}
		kind := "gift_clear"
		column := "cleared_quota"
		if gift.ExpiresAt > 0 && gift.ExpiresAt <= now {
			kind = "gift_expire"
			column = "expired_quota"
			result.ExpiredGiftQuota += gift.RemainingQuota
		} else {
			result.GiftQuota += gift.RemainingQuota
		}
		if err := tx.Model(&gift).Updates(map[string]any{column: gorm.Expr(column+" + ?", gift.RemainingQuota), "remaining_quota": 0}).Error; err != nil {
			return nil, err
		}
		key, _ := marketingDigest([]string{op.OperationKey, "gift", strconv.FormatInt(gift.ID, 10)})
		if err := tx.Create(&MarketingLedgerEntry{OperationKey: key, UserID: input.UserID, Kind: kind, GiftID: gift.ID, AmountQuota: -gift.RemainingQuota, RecordedAt: now}).Error; err != nil {
			return nil, err
		}
	}
	if err := ClearMarketingLotteryChances(tx, input.ActorID, input.UserID, "assets:"+op.OperationKey, now); err != nil {
		return nil, err
	}
	if err := tx.Model(&MarketingActivityDecision{}).Where("user_id = ? AND state <> ?", input.UserID, "suppressed").Updates(map[string]any{"state": "cleared", "next_ordinal": gorm.Expr("count")}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&MarketingActivityReward{}).Where("user_id = ? AND state = ?", input.UserID, "pending").Update("state", "cleared").Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&MarketingActivityProgress{}).Where("user_id = ?", input.UserID).Update("remainder_cents", 0).Error; err != nil {
		return nil, err
	}
	if account.Revision == math.MaxInt64 {
		return nil, ErrMarketingInput
	}
	if err := tx.Model(&User{}).Where("id = ?", input.UserID).Update("quota", 0).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(account).Updates(map[string]any{"clear_fact_sequence": sequence, "revision": account.Revision + 1}).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(&MarketingLedgerEntry{OperationKey: op.OperationKey, UserID: input.UserID, Kind: "paid_clear", AmountQuota: -int64(user.Quota), RecordedAt: now}).Error; err != nil {
		return nil, err
	}
	return op, CompleteMarketingMaintenanceOperation(tx, op, result)
}

// Legacy task metadata is not proof that an old multi-step refund succeeded.
// Independent reviewed net debits and usage counters determine each adjustment;
// the stored task quota is only a concurrency/evidence check. No payment is minted.
type MarketingLegacyTaskReconcileInput struct {
	ActorID, UserID                                                               int
	Reference, Evidence, LocalIdentity, AuthorizationReference                    string
	TaskKind                                                                      string
	TaskID                                                                        int64
	ExpectedQuota, ActualQuota, ConfirmedNetDebitQuota                            int64
	ConfirmedUserUsageQuota, ConfirmedTokenDebitQuota, ConfirmedChannelUsageQuota *int64
	Terminal, OriginalSourceEvidence, WriterIsolationEvidence                     string
}

func ReconcileMarketingLegacyTask(tx *gorm.DB, input MarketingLegacyTaskReconcileInput, now int64) (*MarketingMaintenanceOperation, error) {
	if input.TaskID <= 0 || input.ExpectedQuota < 0 || input.ExpectedQuota > common.MaxWalletQuota || input.ActualQuota < 0 || input.ActualQuota > common.MaxWalletQuota || input.ConfirmedNetDebitQuota < 0 || input.ConfirmedNetDebitQuota > common.MaxWalletQuota || (input.Terminal != "SUCCESS" && input.Terminal != "FAILURE") || strings.TrimSpace(input.OriginalSourceEvidence) == "" || strings.TrimSpace(input.WriterIsolationEvidence) == "" {
		return nil, ErrMarketingInput
	}
	if input.ConfirmedUserUsageQuota == nil {
		return nil, ErrMarketingInput
	}
	for _, amount := range []*int64{input.ConfirmedUserUsageQuota, input.ConfirmedTokenDebitQuota, input.ConfirmedChannelUsageQuota} {
		if amount != nil && (*amount < 0 || *amount > common.MaxWalletQuota) {
			return nil, ErrMarketingInput
		}
	}
	op, replay, err := BeginMarketingMaintenanceOperation(tx, input.ActorID, input.UserID, "legacy_task_reconcile", input.Reference, input.Evidence, input.LocalIdentity, input.AuthorizationReference, input, now)
	if err != nil || replay {
		return op, err
	}
	user, account, err := lockMarketingAccount(tx, input.UserID)
	if err != nil {
		return nil, err
	}
	if account.OriginState != "unverified" || account.PaidReserved != 0 {
		return nil, ErrMarketingConflict
	}
	var record any
	var quota int64
	var billingSource string
	var subscriptionID, tokenID, channelID int
	switch input.TaskKind {
	case "task":
		var task Task
		if err := lockForUpdate(tx).First(&task, input.TaskID).Error; err != nil {
			return nil, err
		}
		if task.UserId != input.UserID || task.MarketingFundingID != 0 || task.MarketingLegacyReviewed {
			return nil, ErrMarketingConflict
		}
		quota = int64(task.Quota)
		billingSource = task.PrivateData.BillingSource
		subscriptionID = task.PrivateData.SubscriptionId
		tokenID = task.PrivateData.TokenId
		channelID = task.ChannelId
		record = &task
	case "midjourney":
		var task Midjourney
		if err := lockForUpdate(tx).First(&task, input.TaskID).Error; err != nil {
			return nil, err
		}
		if task.UserId != input.UserID || task.MarketingFundingID != 0 || task.MarketingLegacyReviewed {
			return nil, ErrMarketingConflict
		}
		quota = int64(task.Quota)
		billingSource = task.MarketingBillingData.BillingSource
		subscriptionID = task.MarketingBillingData.SubscriptionId
		tokenID = task.TokenId
		channelID = task.GetBillingChannelId()
		record = &task
	default:
		return nil, ErrMarketingInput
	}
	if billingSource != "" && billingSource != "wallet" || subscriptionID != 0 {
		return nil, ErrMarketingReview
	}
	if quota != input.ExpectedQuota {
		return nil, ErrMarketingConflict
	}
	if account.OpeningQuota < 0 || account.OpeningQuota > common.MaxWalletQuota || account.OpeningAdjustmentQuota < -common.MaxWalletQuota || account.OpeningAdjustmentQuota > common.MaxWalletQuota {
		return nil, ErrMarketingInput
	}
	difference := input.ConfirmedNetDebitQuota - input.ActualQuota
	balance := decimal.NewFromInt(int64(user.Quota)).Add(decimal.NewFromInt(difference))
	if balance.IsNegative() || balance.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return nil, ErrMarketingReview
	}
	opening := decimal.NewFromInt(account.OpeningQuota).Add(decimal.NewFromInt(account.OpeningAdjustmentQuota)).Add(decimal.NewFromInt(difference))
	if opening.IsNegative() || opening.GreaterThan(balance) || account.Revision == math.MaxInt64 {
		return nil, ErrMarketingReview
	}
	converted, err := common.WalletQuotaFromDecimalStrict(balance)
	if err != nil {
		return nil, ErrMarketingInput
	}
	used := decimal.NewFromInt(int64(user.UsedQuota)).Add(decimal.NewFromInt(input.ActualQuota - *input.ConfirmedUserUsageQuota))
	if used.IsNegative() || used.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return nil, ErrMarketingReview
	}
	if tokenID > 0 {
		if input.ConfirmedTokenDebitQuota == nil {
			return nil, ErrMarketingInput
		}
		var token Token
		if err := lockForUpdate(tx.Unscoped()).First(&token, tokenID).Error; err != nil {
			return nil, err
		}
		if token.UserId != input.UserID {
			return nil, ErrMarketingConflict
		}
		tokenDifference := *input.ConfirmedTokenDebitQuota - input.ActualQuota
		remain := decimal.NewFromInt(int64(token.RemainQuota)).Add(decimal.NewFromInt(tokenDifference))
		tokenUsed := decimal.NewFromInt(int64(token.UsedQuota)).Sub(decimal.NewFromInt(tokenDifference))
		if (!token.UnlimitedQuota && remain.IsNegative()) || remain.LessThan(decimal.NewFromInt(-common.MaxWalletQuota)) || remain.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) || tokenUsed.IsNegative() || tokenUsed.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
			return nil, ErrMarketingReview
		}
		if err := tx.Unscoped().Model(&token).Updates(map[string]any{"remain_quota": remain.IntPart(), "used_quota": tokenUsed.IntPart()}).Error; err != nil {
			return nil, err
		}
	}
	if channelID > 0 {
		if input.ConfirmedChannelUsageQuota == nil {
			return nil, ErrMarketingInput
		}
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, channelID).Error; err != nil {
			return nil, err
		}
		channelUsed := decimal.NewFromInt(int64(channel.UsedQuota)).Add(decimal.NewFromInt(input.ActualQuota - *input.ConfirmedChannelUsageQuota))
		if channelUsed.IsNegative() || channelUsed.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
			return nil, ErrMarketingReview
		}
		if err := tx.Model(&channel).Update("used_quota", channelUsed.IntPart()).Error; err != nil {
			return nil, err
		}
	}
	if err := tx.Model(&User{}).Where("id = ?", input.UserID).Updates(map[string]any{"quota": converted, "used_quota": used.IntPart()}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(account).Updates(map[string]any{"opening_adjustment_quota": account.OpeningAdjustmentQuota + difference, "revision": account.Revision + 1}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(record).Updates(map[string]any{"status": input.Terminal, "progress": "100%", "finish_time": now, "quota": input.ActualQuota, "marketing_billing_pending": false, "marketing_legacy_reviewed": true}).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(&MarketingLedgerEntry{OperationKey: op.OperationKey, UserID: input.UserID, Kind: "legacy_task_reconcile", AmountQuota: difference, RecordedAt: now}).Error; err != nil {
		return nil, err
	}
	return op, CompleteMarketingMaintenanceOperation(tx, op, map[string]any{"task_kind": input.TaskKind, "task_id": input.TaskID, "previous_quota": quota, "confirmed_net_debit_quota": input.ConfirmedNetDebitQuota, "actual_quota": input.ActualQuota, "source_adjustment_quota": difference, "terminal": input.Terminal})
}

// ReconcileMarketingTaskOutcome accompanies the authorized funding reconciliation
// in its transaction. The immutable original outcome remains available as evidence.
func ReconcileMarketingTaskOutcome(tx *gorm.DB, userID int, fundingID int64, terminal string, now int64) error {
	if userID <= 0 || fundingID <= 0 || now <= 0 {
		return ErrMarketingInput
	}
	if _, _, err := lockMarketingAccount(tx, userID); err != nil {
		return err
	}
	var funding MarketingFunding
	if err := lockForUpdate(tx).First(&funding, fundingID).Error; err != nil {
		return err
	}
	if funding.UserID != userID {
		return ErrMarketingAccess
	}
	var tasks []Task
	var mj []Midjourney
	if err := lockForUpdate(tx).Where("marketing_funding_id = ?", fundingID).Find(&tasks).Error; err != nil {
		return err
	}
	if err := lockForUpdate(tx).Where("marketing_funding_id = ?", fundingID).Find(&mj).Error; err != nil {
		return err
	}
	if len(tasks)+len(mj) == 0 {
		return nil
	}
	if len(tasks)+len(mj) != 1 {
		return ErrMarketingConflict
	}
	if terminal != "SUCCESS" && terminal != "FAILURE" {
		return ErrMarketingInput
	}
	var record any
	if len(tasks) > 0 {
		if tasks[0].UserId != userID {
			return ErrMarketingConflict
		}
		record = &tasks[0]
	} else {
		if mj[0].UserId != userID {
			return ErrMarketingConflict
		}
		record = &mj[0]
	}
	return tx.Model(record).Updates(map[string]any{"status": terminal, "finish_time": now, "progress": "100%", "quota": funding.SettledQuota, "marketing_billing_pending": false}).Error
}
