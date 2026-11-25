// Package marketing contains deterministic rules for the simplified marketing
// system. Persistence, authorization, payment verification and idempotency belong
// to the host; passing the same fact twice here does not make it a new payment.
package marketing

import (
	"errors"
	"math"
	"time"
)

var (
	ErrInvalidWindow    = errors.New("invalid marketing time window")
	ErrInvalidRule      = errors.New("invalid recharge reward rule")
	ErrInvalidPayment   = errors.New("invalid successful payment fact")
	ErrInvalidProgress  = errors.New("invalid recharge reward progress")
	ErrProgressOverflow = errors.New("recharge reward progress overflow")
)

// Period includes Start and excludes End. A nil End means no deadline.
type Period struct {
	Start time.Time
	End   *time.Time
}

func (p Period) Validate() error {
	if p.Start.IsZero() || (p.End != nil && (p.End.IsZero() || !p.End.After(p.Start))) {
		return ErrInvalidWindow
	}
	return nil
}

func (p Period) Contains(at time.Time) bool {
	return !at.Before(p.Start) && (p.End == nil || at.Before(*p.End))
}

// EligibilityWindow describes an already effective rule's historical schedule.
// Publication controls discovery separately, never whether a committed fact can
// be recovered. Callers must load the schedule that applied to the fact.
type EligibilityWindow struct {
	Period
	Pauses []Period
}

func (w EligibilityWindow) Validate() error {
	if err := w.Period.Validate(); err != nil {
		return err
	}
	for _, pause := range w.Pauses {
		if err := pause.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (w EligibilityWindow) EligibleAt(at time.Time) (bool, error) {
	if err := w.Validate(); err != nil {
		return false, err
	}
	if at.IsZero() {
		return false, ErrInvalidPayment
	}
	if !w.Contains(at) {
		return false, nil
	}
	for _, pause := range w.Pauses {
		if pause.Contains(at) {
			return false, nil
		}
	}
	return true, nil
}

type ActivityDisplay string

const (
	ActivityHidden        ActivityDisplay = "hidden"
	ActivityUpcoming      ActivityDisplay = "upcoming"
	ActivityActive        ActivityDisplay = "active"
	ActivityRecentlyEnded ActivityDisplay = "recently_ended"
)

// DisplayActivity is a read-only time classification of a publicly enabled
// activity. It never revokes gifts or opportunities. now is supplied by the
// server, not by the user's browser. Paused activity presentation is not decided
// here: callers decide whether that activity is publicly enabled.
func DisplayActivity(period Period, publiclyEnabled bool, now time.Time) (ActivityDisplay, error) {
	if err := period.Validate(); err != nil {
		return ActivityHidden, err
	}
	if now.IsZero() {
		return ActivityHidden, ErrInvalidWindow
	}
	if !publiclyEnabled {
		return ActivityHidden, nil
	}
	if now.Before(period.Start) {
		return ActivityUpcoming, nil
	}
	if period.End == nil || now.Before(*period.End) {
		return ActivityActive, nil
	}
	if now.Sub(*period.End) < 72*time.Hour {
		return ActivityRecentlyEnded, nil
	}
	return ActivityHidden, nil
}

type RechargeCondition string
type RewardFrequency string

const (
	FirstSuccessfulPayment RechargeCondition = "first_successful_payment"
	SinglePayment          RechargeCondition = "single_payment"
	CumulativePayments     RechargeCondition = "cumulative_payments"
	RewardOnce             RewardFrequency   = "once"
	RewardEvery            RewardFrequency   = "every"
)

type RechargeRule struct {
	Condition RechargeCondition
	Frequency RewardFrequency
	// ThresholdCents is actual paid CNY, never wallet quota or bonus balance.
	ThresholdCents int64
}

func (r RechargeRule) Validate() error {
	if r.ThresholdCents <= 0 {
		return ErrInvalidRule
	}
	switch r.Condition {
	case FirstSuccessfulPayment:
		if r.Frequency != RewardOnce {
			return ErrInvalidRule
		}
	case SinglePayment, CumulativePayments:
		if r.Frequency != RewardOnce && r.Frequency != RewardEvery {
			return ErrInvalidRule
		}
	default:
		return ErrInvalidRule
	}
	return nil
}

// SuccessfulPayment must be created from a verified, committed posting. Posting
// order determines IsFirst; receipt/worker execution order must not determine it.
// This type deliberately has no credited quota, bonus or processing timestamp.
type SuccessfulPayment struct {
	PaidCents int64
	PostedAt  time.Time
	IsFirst   bool
}

// RechargeProgress belongs to one user and one activity (and one invited friend
// for that activity's independent counter). ProcessedAwards counts qualification
// units, not gifts delivered: a probability miss still consumes that unit.
// Historical fact deduplication must happen in the caller's transaction.
type RechargeProgress struct {
	ProcessedAwards int64
	RemainderCents  int64
}

type RechargeEvaluation struct {
	Eligible  bool
	NewAwards int64
	Next      RechargeProgress
}

func EvaluateRecharge(rule RechargeRule, window EligibilityWindow, payment SuccessfulPayment, progress RechargeProgress) (RechargeEvaluation, error) {
	result := RechargeEvaluation{Next: progress}
	if err := rule.Validate(); err != nil {
		return result, err
	}
	if payment.PaidCents <= 0 || payment.PostedAt.IsZero() {
		return result, ErrInvalidPayment
	}
	if progress.ProcessedAwards < 0 || progress.RemainderCents < 0 || progress.RemainderCents >= rule.ThresholdCents {
		return result, ErrInvalidProgress
	}
	if rule.Frequency == RewardOnce && progress.ProcessedAwards > 1 {
		return result, ErrInvalidProgress
	}
	if rule.Condition != CumulativePayments && progress.RemainderCents != 0 {
		return result, ErrInvalidProgress
	}
	eligible, err := window.EligibleAt(payment.PostedAt)
	if err != nil {
		return result, err
	}
	result.Eligible = eligible
	if !eligible {
		return result, nil
	}
	if rule.Frequency == RewardOnce && progress.ProcessedAwards == 1 {
		return result, nil
	}
	switch rule.Condition {
	case FirstSuccessfulPayment:
		if payment.IsFirst && payment.PaidCents >= rule.ThresholdCents {
			result.NewAwards = 1
		}
	case SinglePayment:
		if payment.PaidCents >= rule.ThresholdCents {
			result.NewAwards = 1
		}
	case CumulativePayments:
		// Divide first: paid + remainder may overflow even when the resulting
		// qualification count and remainder are both representable.
		awards := payment.PaidCents / rule.ThresholdCents
		remainder := payment.PaidCents % rule.ThresholdCents
		needed := rule.ThresholdCents - progress.RemainderCents
		if remainder >= needed {
			awards++ // threshold=1 implies needed=1 and remainder=0, so MaxInt64 cannot increment.
			remainder -= needed
		} else {
			remainder += progress.RemainderCents
		}
		result.NewAwards = awards
		result.Next.RemainderCents = remainder
		if rule.Frequency == RewardOnce && awards > 0 {
			result.NewAwards = 1
			result.Next.RemainderCents = 0 // No further threshold is available in this activity.
		}
	}
	if progress.ProcessedAwards > math.MaxInt64-result.NewAwards {
		return RechargeEvaluation{Next: progress}, ErrProgressOverflow
	}
	result.Next.ProcessedAwards += result.NewAwards
	return result, nil
}
