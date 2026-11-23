package model

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/marketing"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Activity records never own a second wallet. Facts freeze existing rules;
// decisions commit qualification and chance allocation before gift delivery.
type MarketingActivity struct {
	EnabledAt int64  `gorm:"not null"`
	Published bool   `gorm:"not null"`
	ID        int64  `gorm:"primaryKey"`
	Name      string `gorm:"size:160;not null"`
	Kind      string `gorm:"size:24;not null;index"`
	StartsAt  int64  `gorm:"not null"`
	EndsAt    int64  `gorm:"not null"`
	CreatedAt int64  `gorm:"not null"`
	Revision  int64  `gorm:"not null"`
	Spec      string `gorm:"type:text;not null"`
	Pauses    string `gorm:"type:text;not null"`
	Paused    bool   `gorm:"not null"`
}

func (MarketingActivity) TableName() string { return "marketing_v2_activities" }

type MarketingActivityOperation struct {
	ID           int64  `gorm:"primaryKey"`
	OperationKey string `gorm:"size:64;not null;uniqueIndex"`
	ActorID      int    `gorm:"not null"`
	ActivityID   int64  `gorm:"not null;index"`
	Kind         string `gorm:"size:16;not null"`
	PayloadHash  string `gorm:"size:64;not null"`
	Details      string `gorm:"type:text;not null"`
	RecordedAt   int64  `gorm:"not null"`
}

func (MarketingActivityOperation) TableName() string { return "marketing_v2_activity_operations" }

type MarketingInvitation struct {
	UserID          int    `gorm:"primaryKey;autoIncrement:false"`
	InviterID       int    `gorm:"not null;index"`
	EstablishedAt   int64  `gorm:"not null"`
	SourceReference string `gorm:"size:128;not null"`
}

func (MarketingInvitation) TableName() string { return "marketing_v2_invitations" }

type MarketingActivityFact struct {
	ID              int64  `gorm:"primaryKey"`
	SourceKey       string `gorm:"size:64;not null;uniqueIndex"`
	SourceReference string `gorm:"size:128;not null"`
	UserID          int    `gorm:"not null;index:idx_mv2_fact_order,priority:1"`
	Kind            string `gorm:"size:24;not null"`
	OccurredAt      int64  `gorm:"not null"`
	PaymentID       int64  `gorm:"not null"`
	PaymentSequence int64  `gorm:"not null"`
	PayloadHash     string `gorm:"size:64;not null"`
	Payload         string `gorm:"type:text;not null"`
	Rules           string `gorm:"type:text;not null"`
	State           string `gorm:"size:16;not null;index:idx_mv2_fact_order,priority:2"`
	DeliveryState   string `gorm:"size:16;not null"`
}

func (MarketingActivityFact) TableName() string { return "marketing_v2_activity_facts" }

type MarketingActivityProgress struct {
	ID              int64  `gorm:"primaryKey"`
	ProgressKey     string `gorm:"size:64;not null;uniqueIndex"`
	ActivityID      int64  `gorm:"not null;index"`
	UserID          int    `gorm:"not null;index"`
	FriendID        int    `gorm:"not null"`
	ProcessedAwards int64  `gorm:"not null"`
	RemainderCents  int64  `gorm:"not null"`
	LastFactID      int64  `gorm:"not null"`
}

func (MarketingActivityProgress) TableName() string { return "marketing_v2_activity_progress" }

type MarketingActivityDecision struct {
	ID              int64  `gorm:"primaryKey"`
	DecisionKey     string `gorm:"size:64;not null;uniqueIndex"`
	FactID          int64  `gorm:"not null;index"`
	ActivityID      int64  `gorm:"not null;index"`
	UserID          int    `gorm:"not null;index"`
	FirstOrdinal    int64  `gorm:"not null"`
	Count           int64  `gorm:"not null"`
	NextOrdinal     int64  `gorm:"not null"`
	State           string `gorm:"size:16;not null"`
	RuleSnapshot    string `gorm:"type:text;not null"`
	ProbabilitySeed string `gorm:"size:64;not null" json:"-"`
}

func (MarketingActivityDecision) TableName() string { return "marketing_v2_activity_decisions" }

type MarketingActivityReward struct {
	ID         int64  `gorm:"primaryKey"`
	RewardKey  string `gorm:"size:64;not null;uniqueIndex"`
	FactID     int64  `gorm:"not null;index"`
	DecisionID int64  `gorm:"not null;index"`
	ActivityID int64  `gorm:"not null;index"`
	UserID     int    `gorm:"not null;index"`
	Ordinal    int64  `gorm:"not null"`
	Position   int    `gorm:"not null"`
	Kind       string `gorm:"size:16;not null"`
	State      string `gorm:"size:24;not null"`
	Spec       string `gorm:"type:text;not null"`
	Amount     string `gorm:"type:text;not null"`
	GiftID     int64  `gorm:"not null"`
	IssueID    int64  `gorm:"not null"`
	CreditedAt int64  `gorm:"not null"`
}

func (MarketingActivityReward) TableName() string { return "marketing_v2_activity_rewards" }

type MarketingActivityRewardSpec struct {
	Kind           string
	Money          MarketingMoneySnapshot
	Calculation    string
	Ratio          string
	ValidSeconds   int64
	PoolID         int64
	Count          int64
	ChanceMode     string
	ProbabilityPPM int64
}
type MarketingActivityMilestone struct {
	Days    int
	Rewards []MarketingActivityRewardSpec
}
type MarketingActivityCheckinSpec struct {
	Condition      string // none, today_paid, previous_day_paid
	ThresholdCents int64
	CycleDays      int // zero = once per milestone; positive = repeating cycle
	MakeupDays     int
	MakeupLimit    int
	MakeupDaily    bool
	Milestones     []MarketingActivityMilestone
}
type MarketingActivitySpec struct {
	Published                                        bool
	Name, Kind, Condition, Frequency, ExclusiveGroup string
	Priority                                         int
	StartsAt, EndsAt, ThresholdCents                 int64
	Rewards                                          []MarketingActivityRewardSpec
	Checkin                                          *MarketingActivityCheckinSpec
}

// Check-in claims are supplied only by the calendar transaction (MV2-010),
// after checking the target day, eligibility, continuity and makeup allowance.
// They are not an HTTP request or a replacement for calendar validation.
type MarketingActivityCheckinClaim struct {
	ActivityID    int64
	MilestoneDays int
	Ordinal       int64
}
type marketingActivityPayload struct {
	Payment    *MarketingPayment
	Invitation *MarketingInvitation
	CheckinDay string
	Claims     []MarketingActivityCheckinClaim
}
type marketingActivityRuleSnapshot struct {
	ActivityID int64
	Revision   int64
	UserID     int
	Spec       MarketingActivitySpec
	Ceilings   map[int64]int64
	Claims     []MarketingActivityCheckinClaim
}
type marketingActivityPause struct{ Start, End int64 }

// Activity facts represent already committed external events. Disabled users
// must not make payment capture or another beneficiary's rewards disappear.
func lockMarketingActivityUsers(tx *gorm.DB, ids []int) error {
	if tx == nil || tx.Statement == nil {
		return ErrMarketingTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrMarketingTransaction
	}
	ids = append([]int{}, ids...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if id <= 0 {
			return ErrMarketingInput
		}
		var user User
		if err := lockForUpdate(tx).First(&user, id).Error; err != nil {
			return err
		}
	}
	return nil
}

func validateMarketingActivitySpec(spec MarketingActivitySpec, now int64) error {
	if now <= 0 || strings.TrimSpace(spec.Name) == "" || len(spec.Name) > 160 || len(spec.ExclusiveGroup) > 64 || strings.TrimSpace(spec.ExclusiveGroup) != spec.ExclusiveGroup || spec.StartsAt <= 0 || (spec.EndsAt != 0 && (spec.EndsAt <= spec.StartsAt || spec.EndsAt <= now)) || len(spec.Rewards) > 20 {
		return ErrMarketingInput
	}
	switch spec.Kind {
	case "registration":
		if spec.Condition != "registration" || spec.Frequency != "once" || spec.Checkin != nil {
			return ErrMarketingInput
		}
	case "recharge":
		if spec.Checkin != nil {
			return ErrMarketingInput
		}
		if err := (marketing.RechargeRule{Condition: marketing.RechargeCondition(spec.Condition), Frequency: marketing.RewardFrequency(spec.Frequency), ThresholdCents: spec.ThresholdCents}).Validate(); err != nil {
			return ErrMarketingInput
		}
	case "invitation":
		if spec.Checkin != nil {
			return ErrMarketingInput
		}
		switch spec.Condition {
		case "friend_registration":
			if spec.Frequency != "once" {
				return ErrMarketingInput
			}
		case "friend_cumulative":
			if spec.ThresholdCents <= 0 || (spec.Frequency != "once" && spec.Frequency != "every") {
				return ErrMarketingInput
			}
		case "friend_ratio":
			if spec.Frequency != "every" {
				return ErrMarketingInput
			}
		default:
			return ErrMarketingInput
		}
	case "checkin":
		if spec.Condition != "checkin" || spec.Frequency != "every" || spec.Checkin == nil {
			return ErrMarketingInput
		}
		c := spec.Checkin
		if c.CycleDays < 0 || c.MakeupDays < 0 || c.MakeupLimit < 0 || len(c.Milestones) > 100 || (c.MakeupDays == 0) != (c.MakeupLimit == 0) {
			return ErrMarketingInput
		}
		switch c.Condition {
		case "none":
			if c.ThresholdCents != 0 {
				return ErrMarketingInput
			}
		case "today_paid", "previous_day_paid":
			if c.ThresholdCents <= 0 {
				return ErrMarketingInput
			}
		default:
			return ErrMarketingInput
		}
		seen := map[int]bool{}
		for _, milestone := range c.Milestones {
			if milestone.Days <= 0 || seen[milestone.Days] || (c.CycleDays > 0 && milestone.Days > c.CycleDays) {
				return ErrMarketingInput
			}
			seen[milestone.Days] = true
			if err := validateMarketingActivityRewards(spec, milestone.Rewards); err != nil {
				return err
			}
		}
	default:
		return ErrMarketingInput
	}
	if len(spec.Rewards) == 0 && (spec.Checkin == nil || len(spec.Checkin.Milestones) == 0) {
		return ErrMarketingInput
	}
	if len(spec.Rewards) > 0 {
		return validateMarketingActivityRewards(spec, spec.Rewards)
	}
	return nil
}
func validateMarketingActivityRewards(spec MarketingActivitySpec, rewards []MarketingActivityRewardSpec) error {
	if len(rewards) == 0 || len(rewards) > 20 {
		return ErrMarketingInput
	}
	for _, r := range rewards {
		switch r.Kind {
		case "gift":
			if r.ValidSeconds < 0 || r.PoolID != 0 || r.Count != 0 || r.ChanceMode != "" || r.ProbabilityPPM != 0 {
				return ErrMarketingInput
			}
			switch r.Calculation {
			case "fixed":
				if r.Ratio != "" {
					return ErrMarketingInput
				}
				if _, err := r.Money.Quota(); err != nil {
					return err
				}
			case "paid_ratio", "cash_ratio":
				if !marketingDecimal.MatchString(r.Ratio) {
					return ErrMarketingInput
				}
				ratio, err := decimal.NewFromString(r.Ratio)
				if err != nil || !ratio.IsPositive() {
					return ErrMarketingInput
				}
				if r.Calculation == "paid_ratio" && (spec.Kind != "recharge" || spec.Condition == "cumulative_payments") {
					return ErrMarketingInput
				}
				if r.Calculation == "cash_ratio" && (spec.Kind != "invitation" || spec.Condition != "friend_ratio") {
					return ErrMarketingInput
				}
				if r.Calculation == "cash_ratio" {
					if r.Money.Currency != "CNY" {
						return ErrMarketingInput
					}
					for _, value := range []string{r.Money.QuotaPerUnit, r.Money.CurrencyPerUnit} {
						if !marketingDecimal.MatchString(value) {
							return ErrMarketingInput
						}
						number, err := decimal.NewFromString(value)
						if err != nil || !number.IsPositive() {
							return ErrMarketingInput
						}
					}
				}
			default:
				return ErrMarketingInput
			}
			if spec.Condition == "friend_ratio" && r.Calculation != "cash_ratio" {
				return ErrMarketingInput
			}
		case "chance":
			if spec.Condition == "friend_ratio" || r.PoolID <= 0 || r.Count <= 0 || r.Calculation != "" || r.Ratio != "" || r.ValidSeconds != 0 {
				return ErrMarketingInput
			}
			switch r.ChanceMode {
			case "direct":
				if r.ProbabilityPPM != 0 {
					return ErrMarketingInput
				}
			case "probability":
				if spec.Kind != "recharge" || spec.Condition != "cumulative_payments" || r.ProbabilityPPM < 0 || r.ProbabilityPPM > 1000000 {
					return ErrMarketingInput
				}
			default:
				return ErrMarketingInput
			}
		default:
			return ErrMarketingInput
		}
	}
	return nil
}
func marketingActivityOperation(tx *gorm.DB, actorID int, reference, kind string, payload any) (*MarketingActivityOperation, bool, error) {
	key, err := marketingSourceKey("activity_admin", reference)
	if err != nil {
		return nil, false, err
	}
	hash, err := marketingDigest(payload)
	if err != nil {
		return nil, false, err
	}
	var op MarketingActivityOperation
	err = lockForUpdate(tx).Where("operation_key = ?", key).First(&op).Error
	if err == nil {
		if op.ActorID != actorID || op.Kind != kind || op.PayloadHash != hash {
			return nil, false, ErrMarketingConflict
		}
		return &op, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	details, err := common.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return &MarketingActivityOperation{OperationKey: key, ActorID: actorID, Kind: kind, PayloadHash: hash, Details: string(details)}, false, nil
}
func CreateMarketingActivity(tx *gorm.DB, actorID int, reference string, spec MarketingActivitySpec, now int64) (*MarketingActivity, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	op, replay, err := marketingActivityOperation(tx, actorID, reference, "create", spec)
	if err != nil {
		return nil, err
	}
	if replay {
		var activity MarketingActivity
		err := lockForUpdate(tx).First(&activity, op.ActivityID).Error
		return &activity, err
	}
	if err := validateMarketingActivitySpec(spec, now); err != nil {
		return nil, err
	}
	data, err := common.Marshal(spec)
	if err != nil {
		return nil, err
	}
	activity := MarketingActivity{Name: spec.Name, Kind: spec.Kind, StartsAt: spec.StartsAt, EndsAt: spec.EndsAt, CreatedAt: now, Revision: 1, Spec: string(data), Pauses: "[]", Published: spec.Published}
	if err := tx.Create(&activity).Error; err != nil {
		return nil, err
	}
	if _, err := marketingActivityPoolCeilings(tx, []MarketingActivitySpec{spec}); err != nil {
		return nil, err
	}
	op.ActivityID = activity.ID
	op.RecordedAt = now
	return &activity, tx.Create(op).Error
}
func UpdateMarketingActivity(tx *gorm.DB, actorID int, activityID int64, reference string, spec MarketingActivitySpec, now int64) (*MarketingActivity, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	var activity MarketingActivity
	if err := lockForUpdate(tx).First(&activity, activityID).Error; err != nil {
		return nil, err
	}
	op, replay, err := marketingActivityOperation(tx, actorID, reference, "update", struct {
		ID   int64
		Spec MarketingActivitySpec
	}{activityID, spec})
	if err != nil {
		return nil, err
	}
	if replay {
		return &activity, nil
	}
	if activity.EnabledAt > 0 && now >= activity.StartsAt {
		return nil, ErrMarketingConflict
	}
	if err := validateMarketingActivitySpec(spec, now); err != nil {
		return nil, err
	}
	if _, err := marketingActivityPoolCeilings(tx, []MarketingActivitySpec{spec}); err != nil {
		return nil, err
	}
	data, err := common.Marshal(spec)
	if err != nil {
		return nil, err
	}
	activity.Published = spec.Published
	activity.Name = spec.Name
	activity.Kind = spec.Kind
	activity.StartsAt = spec.StartsAt
	activity.EndsAt = spec.EndsAt
	activity.Spec = string(data)
	activity.Revision++
	if err := tx.Save(&activity).Error; err != nil {
		return nil, err
	}
	op.ActivityID = activity.ID
	op.RecordedAt = now
	return &activity, tx.Create(op).Error
}

// Activation is one-way; pausing controls future participation without changing
// the original activation timestamp or reviving previously captured facts.
func ActivateMarketingActivity(tx *gorm.DB, actorID int, activityID int64, reference string, now int64) (*MarketingActivity, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	var activity MarketingActivity
	if err := lockForUpdate(tx).First(&activity, activityID).Error; err != nil {
		return nil, err
	}
	op, replay, err := marketingActivityOperation(tx, actorID, reference, "activate", activityID)
	if err != nil {
		return nil, err
	}
	if replay {
		return &activity, nil
	}
	if now < activity.CreatedAt || (activity.EndsAt > 0 && now >= activity.EndsAt) {
		return nil, ErrMarketingInput
	}
	if activity.EnabledAt == 0 {
		activity.EnabledAt = now
		activity.Revision++
		if err := tx.Save(&activity).Error; err != nil {
			return nil, err
		}
	}
	op.ActivityID = activityID
	op.RecordedAt = now
	return &activity, tx.Create(op).Error
}
func SetMarketingActivityPublished(tx *gorm.DB, actorID int, activityID int64, reference string, published bool, now int64) (*MarketingActivity, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	var activity MarketingActivity
	if err := lockForUpdate(tx).First(&activity, activityID).Error; err != nil {
		return nil, err
	}
	op, replay, err := marketingActivityOperation(tx, actorID, reference, "publish", struct {
		ID        int64
		Published bool
	}{activityID, published})
	if err != nil {
		return nil, err
	}
	if replay {
		return &activity, nil
	}
	var spec MarketingActivitySpec
	if err := common.UnmarshalJsonStr(activity.Spec, &spec); err != nil {
		return nil, err
	}
	spec.Published = published
	data, err := common.Marshal(spec)
	if err != nil {
		return nil, err
	}
	activity.Spec = string(data)
	activity.Published = published
	activity.Revision++
	if err := tx.Save(&activity).Error; err != nil {
		return nil, err
	}
	op.ActivityID = activityID
	op.RecordedAt = now
	return &activity, tx.Create(op).Error
}

func SetMarketingActivityPaused(tx *gorm.DB, actorID int, activityID int64, reference string, paused bool, now int64) (*MarketingActivity, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	var activity MarketingActivity
	if err := lockForUpdate(tx).First(&activity, activityID).Error; err != nil {
		return nil, err
	}
	op, replay, err := marketingActivityOperation(tx, actorID, reference, "pause", struct {
		ID     int64
		Paused bool
	}{activityID, paused})
	if err != nil {
		return nil, err
	}
	if replay {
		return &activity, nil
	}
	var pauses []marketingActivityPause
	if err := common.UnmarshalJsonStr(activity.Pauses, &pauses); err != nil {
		return nil, err
	}
	if activity.Paused != paused {
		if paused {
			pauses = append(pauses, marketingActivityPause{Start: now})
		} else {
			if len(pauses) == 0 || pauses[len(pauses)-1].End != 0 || now < pauses[len(pauses)-1].Start {
				return nil, ErrMarketingConflict
			}
			pauses[len(pauses)-1].End = now
		}
	}
	data, err := common.Marshal(pauses)
	if err != nil {
		return nil, err
	}
	activity.Pauses = string(data)
	activity.Paused = paused
	activity.Revision++
	if err := tx.Save(&activity).Error; err != nil {
		return nil, err
	}
	op.ActivityID = activity.ID
	op.RecordedAt = now
	return &activity, tx.Create(op).Error
}

// Referenced pools always lock in ID order, independent of form reward order.
func marketingActivityPoolCeilings(tx *gorm.DB, specs []MarketingActivitySpec) (map[int64]int64, error) {
	ids := []int64{}
	for _, spec := range specs {
		rewards := append([]MarketingActivityRewardSpec{}, spec.Rewards...)
		if spec.Checkin != nil {
			for _, milestone := range spec.Checkin.Milestones {
				rewards = append(rewards, milestone.Rewards...)
			}
		}
		for _, reward := range rewards {
			if reward.Kind == "chance" {
				ids = append(ids, reward.PoolID)
			}
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	ceilings := make(map[int64]int64, len(ids))
	for _, id := range ids {
		pool, err := lockMarketingLotteryPool(tx, id)
		if err != nil {
			return nil, err
		}
		ceilings[id] = pool.Capacity
	}
	return ceilings, nil
}

func RecordMarketingInvitation(tx *gorm.DB, inviteeID, inviterID int, reference string, at int64) (*MarketingInvitation, error) {
	if inviteeID == inviterID || at <= 0 {
		return nil, ErrMarketingInput
	}
	if _, err := marketingSourceKey("invitation", reference); err != nil {
		return nil, err
	}
	for _, id := range []int{min(inviteeID, inviterID), max(inviteeID, inviterID)} {
		if err := lockMarketingLotteryActor(tx, id, false); err != nil {
			return nil, err
		}
	}
	var user User
	if err := lockForUpdate(tx).First(&user, inviteeID).Error; err != nil {
		return nil, err
	}
	if user.InviterId != inviterID || user.CreatedAt != at {
		return nil, ErrMarketingConflict
	}
	var relation MarketingInvitation
	err := lockForUpdate(tx).First(&relation, "user_id = ?", inviteeID).Error
	if err == nil {
		if relation.InviterID != inviterID || relation.EstablishedAt != at || relation.SourceReference != reference {
			return nil, ErrMarketingConflict
		}
		return &relation, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	relation = MarketingInvitation{UserID: inviteeID, InviterID: inviterID, EstablishedAt: at, SourceReference: reference}
	return &relation, tx.Create(&relation).Error
}
func CaptureMarketingRegistrationFact(tx *gorm.DB, userID int, reference string, occurredAt int64) (*MarketingActivityFact, error) {
	if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
		return nil, err
	}
	var user User
	if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
		return nil, err
	}
	if user.CreatedAt != occurredAt {
		return nil, ErrMarketingConflict
	}
	return captureMarketingActivityFact(tx, userID, "registration", reference, occurredAt, marketingActivityPayload{})
}
func CaptureMarketingPaymentFact(tx *gorm.DB, payment *MarketingPayment) (*MarketingActivityFact, error) {
	if err := lockMarketingActivityUsers(tx, nil); err != nil {
		return nil, err
	}
	if payment == nil || payment.ID <= 0 {
		return nil, ErrMarketingInput
	}
	// Read the actual posting from this transaction, not caller-supplied money.
	if err := lockMarketingActivityUsers(tx, []int{payment.UserID}); err != nil {
		return nil, err
	}
	var posted MarketingPayment
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", payment.ID, payment.UserID).First(&posted).Error; err != nil {
		return nil, err
	}
	return captureMarketingActivityFact(tx, posted.UserID, "payment", "payment:"+strconv.FormatInt(posted.ID, 10), posted.PostedAt, marketingActivityPayload{Payment: &posted})
}
func CaptureMarketingCheckinFact(tx *gorm.DB, userID int, reference, day string, occurredAt int64, claims []MarketingActivityCheckinClaim) (*MarketingActivityFact, error) {
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return nil, ErrMarketingInput
	}
	seen := map[string]bool{}
	for _, claim := range claims {
		key := fmt.Sprintf("%d:%d", claim.ActivityID, claim.MilestoneDays)
		if claim.ActivityID <= 0 || claim.MilestoneDays < 0 || claim.Ordinal <= 0 || seen[key] {
			return nil, ErrMarketingInput
		}
		seen[key] = true
	}
	return captureMarketingActivityFact(tx, userID, "checkin", reference, occurredAt, marketingActivityPayload{CheckinDay: day, Claims: claims})
}
func captureMarketingActivityFact(tx *gorm.DB, userID int, kind, reference string, at int64, payload marketingActivityPayload) (*MarketingActivityFact, error) {
	if at <= 0 {
		return nil, ErrMarketingInput
	}
	if kind == "payment" {
		if err := lockMarketingActivityUsers(tx, []int{userID}); err != nil {
			return nil, err
		}
	} else if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
		return nil, err
	}
	if _, err := marketingSourceKey(kind, reference); err != nil {
		return nil, err
	}
	identity := reference
	if kind == "registration" {
		identity = strconv.Itoa(userID)
	}
	if kind == "checkin" {
		identity = fmt.Sprintf("%d:%s", userID, payload.CheckinDay)
	}
	key, err := marketingSourceKey("activity_fact:"+kind, identity)
	if err != nil {
		return nil, err
	}
	// Invitation is immutable and captured now, never inferred later from AffCode.
	var relation MarketingInvitation
	err = lockForUpdate(tx).First(&relation, "user_id = ?", userID).Error
	if err == nil {
		payload.Invitation = &relation
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	digest, err := marketingDigest(struct {
		UserID          int
		Kind, Reference string
		At              int64
		Payload         marketingActivityPayload
	}{userID, kind, reference, at, payload})
	if err != nil {
		return nil, err
	}
	var prior MarketingActivityFact
	err = lockForUpdate(tx).Where("source_key = ?", key).First(&prior).Error
	if err == nil {
		if prior.PayloadHash != digest {
			return nil, ErrMarketingConflict
		}
		return &prior, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var activities []MarketingActivity
	if err := lockForUpdate(tx).Where("enabled_at > 0 AND enabled_at <= ? AND created_at <= ? AND starts_at <= ? AND (ends_at = 0 OR ends_at > ?)", at, at, at, at).Order("id").Find(&activities).Error; err != nil {
		return nil, err
	}
	snapshots := []marketingActivityRuleSnapshot{}
	specs := []MarketingActivitySpec{}
	for _, activity := range activities {
		eligibleKind := (kind == "registration" && activity.Kind == "registration") || (kind == "payment" && activity.Kind == "recharge") || (kind == "checkin" && activity.Kind == "checkin") || ((kind == "registration" || kind == "payment") && activity.Kind == "invitation" && payload.Invitation != nil)
		if !eligibleKind {
			continue
		}
		var pauses []marketingActivityPause
		if err := common.UnmarshalJsonStr(activity.Pauses, &pauses); err != nil {
			return nil, err
		}
		paused := false
		for _, pause := range pauses {
			if at >= pause.Start && (pause.End == 0 || at < pause.End) {
				paused = true
				break
			}
		}
		if paused {
			continue
		}
		var spec MarketingActivitySpec
		if err := common.UnmarshalJsonStr(activity.Spec, &spec); err != nil {
			return nil, err
		}
		beneficiary := userID
		if activity.Kind == "invitation" {
			if payload.Invitation.EstablishedAt < activity.StartsAt || (activity.EndsAt > 0 && payload.Invitation.EstablishedAt >= activity.EndsAt) {
				continue
			}
			beneficiary = payload.Invitation.InviterID
		}
		claims := []MarketingActivityCheckinClaim{}
		if kind == "checkin" {
			for _, claim := range payload.Claims {
				if claim.ActivityID == activity.ID {
					claims = append(claims, claim)
				}
			}
			if len(claims) == 0 {
				continue
			}
		}
		snapshots = append(snapshots, marketingActivityRuleSnapshot{ActivityID: activity.ID, Revision: activity.Revision, UserID: beneficiary, Spec: spec, Claims: claims})
		specs = append(specs, spec)
	}
	ceilings, err := marketingActivityPoolCeilings(tx, specs)
	if err != nil {
		return nil, err
	}
	for i := range snapshots {
		snapshots[i].Ceilings = ceilings
	}
	rules, err := common.Marshal(snapshots)
	if err != nil {
		return nil, err
	}
	data, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	fact := MarketingActivityFact{SourceKey: key, SourceReference: reference, UserID: userID, Kind: kind, OccurredAt: at, PayloadHash: digest, Payload: string(data), Rules: string(rules), State: "captured", DeliveryState: "pending"}
	if payload.Payment != nil {
		fact.PaymentID = payload.Payment.ID
		fact.PaymentSequence = payload.Payment.Sequence
	}
	return &fact, tx.Create(&fact).Error
}

// ProcessNext never accepts an event ID: a caller cannot jump over the earliest
// unresolved fact. The 32-qualification work slice is not a reward count limit.
func ProcessNextMarketingActivityFact(tx *gorm.DB, sourceUserID int, now int64) (*MarketingActivityFact, error) {
	if err := lockMarketingActivityUsers(tx, nil); err != nil {
		return nil, err
	}
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	// Relation is immutable. Discover lock identities before taking user locks,
	// then verify all frozen beneficiaries under current reads below.
	users := []int{sourceUserID}
	var invitation MarketingInvitation
	err := tx.First(&invitation, "user_id = ?", sourceUserID).Error
	if err == nil {
		users = append(users, invitation.InviterID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	slices.Sort(users)
	users = slices.Compact(users)
	if err := lockMarketingActivityUsers(tx, users); err != nil {
		return nil, err
	}
	var fact MarketingActivityFact
	err = lockForUpdate(tx).Where("user_id = ? AND state <> ?", sourceUserID, "decided").Order("id").First(&fact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshots []marketingActivityRuleSnapshot
	var payload marketingActivityPayload
	if err := common.UnmarshalJsonStr(fact.Rules, &snapshots); err != nil {
		return nil, err
	}
	if err := common.UnmarshalJsonStr(fact.Payload, &payload); err != nil {
		return nil, err
	}
	for _, snapshot := range snapshots {
		if !slices.Contains(users, snapshot.UserID) {
			return nil, ErrMarketingConflict
		}
	}
	if fact.State == "captured" {
		if err := planMarketingActivityFact(tx, &fact, payload, snapshots); err != nil {
			return nil, err
		}
	}
	var decisions []MarketingActivityDecision
	if err := lockForUpdate(tx).Where("fact_id = ?", fact.ID).Order("id").Find(&decisions).Error; err != nil {
		return nil, err
	}
	specs := make([]MarketingActivitySpec, 0, len(snapshots))
	for _, snapshot := range snapshots {
		specs = append(specs, snapshot.Spec)
	}
	if _, err := marketingActivityPoolCeilings(tx, specs); err != nil {
		return nil, err
	}
	remainingWork := int64(32)
	allDecided := true
	for i := range decisions {
		decision := &decisions[i]
		if decision.State == "suppressed" {
			continue
		}
		var snapshot marketingActivityRuleSnapshot
		if err := common.UnmarshalJsonStr(decision.RuleSnapshot, &snapshot); err != nil {
			return nil, err
		}
		for decision.NextOrdinal < decision.Count && remainingWork > 0 {
			ordinal := decision.FirstOrdinal + decision.NextOrdinal
			for position, spec := range snapshot.Spec.Rewards {
				key, err := marketingDigest(struct {
					Decision, Ordinal int64
					Position          int
				}{decision.ID, ordinal, position})
				if err != nil {
					return nil, err
				}
				reward := MarketingActivityReward{RewardKey: key, FactID: fact.ID, DecisionID: decision.ID, ActivityID: decision.ActivityID, UserID: decision.UserID, Ordinal: ordinal, Position: position, Kind: spec.Kind, State: "pending", Amount: ""}
				encoded, err := common.Marshal(spec)
				if err != nil {
					return nil, err
				}
				reward.Spec = string(encoded)
				if spec.Kind == "gift" {
					amount, err := CalculateMarketingRewardAmount(spec.Calculation, spec.Ratio, spec.Money, payload.Payment)
					if err == nil {
						data, err := common.Marshal(amount)
						if err != nil {
							return nil, err
						}
						reward.Amount = string(data)
						if amount.Quota == 0 {
							reward.State = "zero_amount"
						}
					}
					// Invalid at-runtime amount (e.g. extreme ratio) remains a visible,
					// recoverable responsibility, never a probability miss or zero gift.
				} else {
					hit, err := marketingActivityChanceHit(decision.ProbabilitySeed, ordinal, position, spec)
					if err != nil {
						return nil, err
					}
					if !hit {
						reward.State = "probability_miss"
					} else {
						pool, err := lockMarketingLotteryPool(tx, spec.PoolID)
						if err != nil {
							return nil, err
						}
						if now >= pool.EndsAt {
							reward.State = "pool_expired"
						} else {
							issue, err := IssueMarketingLotteryChancesAtCapacity(tx, reward.UserID, spec.PoolID, "activity:"+key, spec.Count, now, snapshot.Ceilings[spec.PoolID])
							if err != nil {
								return nil, err
							}
							reward.IssueID = issue.ID
							reward.State = issue.State
						}
					}
				}
				if err := tx.Create(&reward).Error; err != nil {
					return nil, err
				}
			}
			decision.NextOrdinal++
			remainingWork--
		}
		if err := tx.Save(decision).Error; err != nil {
			return nil, err
		}
		if decision.NextOrdinal < decision.Count {
			allDecided = false
		}
	}
	if allDecided {
		fact.State = "decided"
	} else {
		fact.State = "deciding"
	}
	if err := tx.Save(&fact).Error; err != nil {
		return nil, err
	}
	if err := refreshMarketingActivityDelivery(tx, &fact); err != nil {
		return nil, err
	}
	return &fact, nil
}

func planMarketingActivityFact(tx *gorm.DB, fact *MarketingActivityFact, payload marketingActivityPayload, snapshots []marketingActivityRuleSnapshot) error {
	type candidate struct {
		snapshot     marketingActivityRuleSnapshot
		first, count int64
		part         int
	}
	candidates := []candidate{}
	for _, snapshot := range snapshots {
		spec := snapshot.Spec
		if spec.Kind == "checkin" {
			for _, claim := range snapshot.Claims {
				selected := snapshot
				if claim.MilestoneDays > 0 {
					selected.Spec.Rewards = nil
					for _, milestone := range spec.Checkin.Milestones {
						if milestone.Days == claim.MilestoneDays {
							selected.Spec.Rewards = milestone.Rewards
							break
						}
					}
				}
				if len(selected.Spec.Rewards) == 0 {
					return ErrMarketingInput
				}
				candidates = append(candidates, candidate{selected, claim.Ordinal, 1, claim.MilestoneDays})
			}
			continue
		}
		friend := 0
		if spec.Kind == "invitation" {
			friend = fact.UserID
		}
		key, err := marketingDigest([]int64{snapshot.ActivityID, int64(snapshot.UserID), int64(friend)})
		if err != nil {
			return err
		}
		var progress MarketingActivityProgress
		err = lockForUpdate(tx).Where("progress_key = ?", key).First(&progress).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			progress = MarketingActivityProgress{ProgressKey: key, ActivityID: snapshot.ActivityID, UserID: snapshot.UserID, FriendID: friend}
			if err := tx.Create(&progress).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		first := int64(0)
		if progress.ProcessedAwards < math.MaxInt64 {
			first = progress.ProcessedAwards + 1
		}
		count := int64(0)
		switch spec.Kind {
		case "registration":
			if fact.Kind == "registration" && progress.ProcessedAwards == 0 {
				count = 1
			}
		case "recharge", "invitation":
			if spec.Condition == "friend_registration" {
				if fact.Kind == "registration" && progress.ProcessedAwards == 0 {
					count = 1
				}
			} else if payload.Payment != nil {
				if spec.Condition == "friend_ratio" {
					count = 1
				} else {
					condition := spec.Condition
					if condition == "friend_cumulative" {
						condition = "cumulative_payments"
					}
					var end *time.Time
					if spec.EndsAt != 0 {
						value := time.Unix(spec.EndsAt, 0)
						end = &value
					}
					evaluation, err := marketing.EvaluateRecharge(marketing.RechargeRule{Condition: marketing.RechargeCondition(condition), Frequency: marketing.RewardFrequency(spec.Frequency), ThresholdCents: spec.ThresholdCents}, marketing.EligibilityWindow{Period: marketing.Period{Start: time.Unix(spec.StartsAt, 0), End: end}}, marketing.SuccessfulPayment{PaidCents: payload.Payment.PaidCents, PostedAt: time.Unix(fact.OccurredAt, 0), IsFirst: payload.Payment.FirstStatus == MarketingFirstPaymentConfirmed}, marketing.RechargeProgress{ProcessedAwards: progress.ProcessedAwards, RemainderCents: progress.RemainderCents})
					if err != nil {
						return err
					}
					count = evaluation.NewAwards
					progress.RemainderCents = evaluation.Next.RemainderCents
				}
			}
		}
		if progress.ProcessedAwards > math.MaxInt64-count || (count > 0 && first <= 0) {
			return ErrMarketingInput
		}
		progress.ProcessedAwards += count
		progress.LastFactID = fact.ID
		if err := tx.Save(&progress).Error; err != nil {
			return err
		}
		if count > 0 {
			candidates = append(candidates, candidate{snapshot, first, count, 0})
		}
	}
	// Resolve groups for this fact and beneficiary before any stock checks.
	winners := map[string]int{}
	for i, c := range candidates {
		group := c.snapshot.Spec.ExclusiveGroup
		if group == "" {
			continue
		}
		key := fmt.Sprintf("%d:%s", c.snapshot.UserID, group)
		previous, ok := winners[key]
		if !ok || c.snapshot.Spec.Priority > candidates[previous].snapshot.Spec.Priority || (c.snapshot.Spec.Priority == candidates[previous].snapshot.Spec.Priority && c.snapshot.ActivityID < candidates[previous].snapshot.ActivityID) {
			winners[key] = i
		}
	}
	for _, c := range candidates {
		key, err := marketingDigest([]int64{fact.ID, c.snapshot.ActivityID, int64(c.snapshot.UserID), int64(c.part), c.first})
		if err != nil {
			return err
		}
		data, err := common.Marshal(c.snapshot)
		if err != nil {
			return err
		}
		decision := MarketingActivityDecision{DecisionKey: key, FactID: fact.ID, ActivityID: c.snapshot.ActivityID, UserID: c.snapshot.UserID, FirstOrdinal: c.first, Count: c.count, State: "selected", RuleSnapshot: string(data)}
		if c.snapshot.Spec.ExclusiveGroup != "" {
			winner := candidates[winners[fmt.Sprintf("%d:%s", c.snapshot.UserID, c.snapshot.Spec.ExclusiveGroup)]]
			if winner.snapshot.ActivityID != c.snapshot.ActivityID {
				decision.State = "suppressed"
				decision.NextOrdinal = decision.Count
			}
		}
		seed := make([]byte, 32)
		if _, err := rand.Read(seed); err != nil {
			return err
		}
		decision.ProbabilitySeed = hex.EncodeToString(seed)
		if err := tx.Create(&decision).Error; err != nil {
			return err
		}
	}
	fact.State = "deciding"
	return tx.Save(fact).Error
}

// A committed secret fixes every ordinal's decision without a giant transaction.
// Rejection sampling avoids modulo bias; neither seed nor ticket is client input.
func marketingActivityChanceHit(seed string, ordinal int64, position int, spec MarketingActivityRewardSpec) (bool, error) {
	if spec.ChanceMode == "direct" {
		return true, nil
	}
	if spec.ProbabilityPPM == 0 {
		return false, nil
	}
	if spec.ProbabilityPPM == 1000000 {
		return true, nil
	}
	secret, err := hex.DecodeString(seed)
	if err != nil || len(secret) != 32 {
		return false, ErrMarketingInput
	}
	limit := uint64(math.MaxUint64 - math.MaxUint64%1000000)
	var input [24]byte
	binary.BigEndian.PutUint64(input[:8], uint64(ordinal))
	binary.BigEndian.PutUint64(input[8:16], uint64(position))
	for attempt := uint64(0); ; attempt++ {
		binary.BigEndian.PutUint64(input[16:], attempt)
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(input[:])
		ticket := binary.BigEndian.Uint64(mac.Sum(nil)[:8])
		if ticket < limit {
			return ticket%1000000 < uint64(spec.ProbabilityPPM), nil
		}
	}
}
func refreshMarketingActivityDelivery(tx *gorm.DB, fact *MarketingActivityFact) error {
	if fact.State != "decided" {
		return nil
	}
	var pending []MarketingActivityReward
	if err := lockForUpdate(tx).Where("fact_id = ? AND state = ?", fact.ID, "pending").Limit(1).Find(&pending).Error; err != nil {
		return err
	}
	state := "complete"
	if len(pending) > 0 {
		state = "pending"
	}
	fact.DeliveryState = state
	return tx.Model(fact).Update("delivery_state", state).Error
}
func FulfillMarketingActivityReward(tx *gorm.DB, beneficiaryID int, itemID, now int64) (*MarketingActivityReward, error) {
	if err := lockMarketingActivityUsers(tx, nil); err != nil {
		return nil, err
	}
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	// Initial reads locate immutable identities only. Every mutable state is
	// re-read after the source/beneficiary locks in global user-ID order.
	var reward MarketingActivityReward
	if err := tx.First(&reward, itemID).Error; err != nil {
		return nil, err
	}
	if reward.UserID != beneficiaryID {
		return nil, ErrMarketingAccess
	}
	var fact MarketingActivityFact
	if err := tx.First(&fact, reward.FactID).Error; err != nil {
		return nil, err
	}
	users := []int{fact.UserID, beneficiaryID}
	slices.Sort(users)
	users = slices.Compact(users)
	if err := lockMarketingActivityUsers(tx, users); err != nil {
		return nil, err
	}
	if err := lockMarketingLotteryActor(tx, beneficiaryID, false); err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).First(&reward, itemID).Error; err != nil {
		return nil, err
	}
	if reward.State != "pending" {
		return &reward, nil
	}
	if reward.Kind != "gift" {
		return nil, ErrMarketingConflict
	}
	var spec MarketingActivityRewardSpec
	if err := common.UnmarshalJsonStr(reward.Spec, &spec); err != nil {
		return nil, err
	}
	var amount MarketingRewardAmount
	if reward.Amount == "" {
		var payload marketingActivityPayload
		if err := common.UnmarshalJsonStr(fact.Payload, &payload); err != nil {
			return nil, err
		}
		calculated, err := CalculateMarketingRewardAmount(spec.Calculation, spec.Ratio, spec.Money, payload.Payment)
		if err != nil {
			return nil, err
		}
		amount = calculated
	} else if err := common.UnmarshalJsonStr(reward.Amount, &amount); err != nil {
		return nil, err
	}
	if amount.Quota == 0 {
		reward.State = "zero_amount"
	} else {
		expires := int64(0)
		if spec.ValidSeconds > 0 {
			if now > math.MaxInt64-spec.ValidSeconds {
				return nil, ErrMarketingInput
			}
			expires = now + spec.ValidSeconds
		}
		var activity MarketingActivity
		if err := tx.First(&activity, reward.ActivityID).Error; err != nil {
			return nil, err
		}
		kind := activity.Kind
		if kind == "invitation" {
			kind = "invitation"
		}
		gift, err := GrantMarketingRewardGift(tx, MarketingGiftInput{UserID: beneficiaryID, Kind: kind, Reference: "activity:" + reward.RewardKey, GrantedAt: now, ExpiresAt: expires, Money: amount.Money}, amount)
		if err != nil {
			return nil, err
		}
		reward.State = "credited"
		reward.GiftID = gift.ID
		reward.CreditedAt = gift.GrantedAt
	}
	if err := tx.Save(&reward).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).First(&fact, reward.FactID).Error; err != nil {
		return nil, err
	}
	if err := refreshMarketingActivityDelivery(tx, &fact); err != nil {
		return nil, err
	}
	return &reward, nil
}
