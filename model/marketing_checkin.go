package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/marketing"
	"gorm.io/gorm"
)

// Each activity owns its calendar and makeup allowance. Old checkins are never
// consulted, including when a copied activity has an identical name or rules.
type MarketingCheckinDay struct {
	ID                 int64  `gorm:"primaryKey"`
	UserID             int    `gorm:"not null;uniqueIndex:idx_mv2_checkin_day,priority:1"`
	ActivityID         int64  `gorm:"not null;uniqueIndex:idx_mv2_checkin_day,priority:2"`
	Day                string `gorm:"size:10;not null;uniqueIndex:idx_mv2_checkin_day,priority:3"`
	Makeup             bool   `gorm:"not null"`
	RecordedAt         int64  `gorm:"not null"`
	FactID             int64  `gorm:"not null;index"`
	QualificationDay   string `gorm:"size:10;not null"`
	QualifiedPaidCents int64  `gorm:"not null"`
}

func (MarketingCheckinDay) TableName() string { return "marketing_v2_checkin_days" }

type MarketingCheckinClaim struct {
	AnchorDay     string `gorm:"size:10;not null"`
	ID            int64  `gorm:"primaryKey"`
	UserID        int    `gorm:"not null;uniqueIndex:idx_mv2_checkin_claim,priority:1"`
	ActivityID    int64  `gorm:"not null;uniqueIndex:idx_mv2_checkin_claim,priority:2"`
	MilestoneDays int    `gorm:"not null;uniqueIndex:idx_mv2_checkin_claim,priority:3"`
	Ordinal       int64  `gorm:"not null;uniqueIndex:idx_mv2_checkin_claim,priority:4"`
	FactID        int64  `gorm:"not null;index"`
	RecordedAt    int64  `gorm:"not null"`
}

func (MarketingCheckinClaim) TableName() string { return "marketing_v2_checkin_claims" }

type MarketingCheckinResult struct {
	Day             string
	Makeup          bool
	AlreadyRecorded bool
	FactID          int64
	ActivityIDs     []int64
}

type MarketingCheckinCalendarActivity struct {
	Activity         MarketingActivity
	Spec             MarketingActivitySpec
	Records          []MarketingCheckinDay
	Streak           int
	MakeupUsed       int
	MakeupRemaining  int
	MakeupDates      []string
	TodayEligibility string // eligible, already_signed, payment_required, inactive, suppressed
}
type MarketingCheckinCalendar struct {
	CanCheckin     bool
	CheckedInToday bool
	Month          string
	Today          string
	Activities     []MarketingCheckinCalendarActivity
}

func SubmitMarketingCheckin(ctx context.Context, userID int, targetDay string) (*MarketingCheckinResult, error) {
	var result *MarketingCheckinResult
	err := WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		result, err = commitMarketingCheckinWithClock(tx, userID, targetDay, common.GetTimestamp)
		return err
	})
	return result, err
}
func CommitMarketingCheckin(tx *gorm.DB, userID int, targetDay string, now int64) (*MarketingCheckinResult, error) {
	return commitMarketingCheckinWithClock(tx, userID, targetDay, func() int64 { return now })
}

func commitMarketingCheckinWithClock(tx *gorm.DB, userID int, targetDay string, clock func() int64) (*MarketingCheckinResult, error) {
	if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
		return nil, err
	}
	var user User
	if err := tx.First(&user, userID).Error; err != nil {
		return nil, err
	}
	sequence, err := nextMarketingFactSequence(tx)
	if err != nil {
		return nil, err
	}
	var activities []MarketingActivity
	if err := lockForUpdate(tx).Where("kind = ? AND enabled_at > 0", "checkin").Order("id").Find(&activities).Error; err != nil {
		return nil, err
	}
	now := clock()
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	today := time.Unix(now, 0).In(marketing.ShanghaiCalendar).Format("2006-01-02")
	makeup := targetDay != ""
	if !makeup {
		targetDay = today
	}
	date, err := marketing.ParseCheckinDay(targetDay)
	if err != nil {
		return nil, common.NewMessage("Invalid check-in date")
	}
	if targetDay > today || (makeup && targetDay == today) {
		return nil, common.NewMessage("Makeup check-in requires a past date")
	}
	key, err := marketingSourceKey("activity_fact:checkin", fmt.Sprintf("%d:%s", userID, targetDay))
	if err != nil {
		return nil, err
	}
	var prior MarketingActivityFact
	err = lockForUpdate(tx).Where("source_key = ?", key).First(&prior).Error
	if err == nil {
		var records []MarketingCheckinDay
		if err := tx.Where("user_id = ? AND fact_id = ?", userID, prior.ID).Order("activity_id").Find(&records).Error; err != nil {
			return nil, err
		}
		result := &MarketingCheckinResult{Day: targetDay, AlreadyRecorded: true, FactID: prior.ID, ActivityIDs: []int64{}}
		for _, record := range records {
			result.ActivityIDs = append(result.ActivityIDs, record.ActivityID)
			result.Makeup = record.Makeup
		}
		return result, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	snapshots := []marketingActivityRuleSnapshot{}
	records := []MarketingCheckinDay{}
	claims := []MarketingCheckinClaim{}
	result := &MarketingCheckinResult{Day: targetDay, Makeup: makeup, ActivityIDs: []int64{}}
	for _, activity := range activities {
		if activity.StartsAt > now || (activity.EndsAt > 0 && activity.EndsAt <= now) || activity.Paused {
			continue
		}
		var spec MarketingActivitySpec
		if err := common.UnmarshalJsonStr(activity.Spec, &spec); err != nil {
			return nil, err
		}
		if spec.Checkin == nil {
			return nil, ErrMarketingConflict
		}
		eligible, err := marketingCheckinActivityDay(activity, user.CreatedAt, date, now, makeup, 0)
		if err != nil {
			return nil, err
		}
		if !eligible {
			continue
		}
		var history []MarketingCheckinDay
		if err := lockForUpdate(tx).Where("user_id = ? AND activity_id = ?", userID, activity.ID).Order("day").Find(&history).Error; err != nil {
			return nil, err
		}
		used := 0
		days := make([]string, 0, len(history)+1)
		already := false
		for _, record := range history {
			if record.Makeup {
				used++
			}
			days = append(days, record.Day)
			already = already || record.Day == targetDay
		}
		if already {
			continue
		}
		if makeup {
			earliest := time.Unix(now, 0).In(marketing.ShanghaiCalendar).AddDate(0, 0, -spec.Checkin.MakeupDays).Format("2006-01-02")
			if spec.Checkin.MakeupDays <= 0 || targetDay < earliest || used >= spec.Checkin.MakeupLimit {
				continue
			}
		}
		qualified, paidDay, paid, qualifiedAt, err := marketingCheckinPaidQualification(tx, userID, *spec.Checkin, date, now)
		if err != nil {
			return nil, err
		}
		if !qualified {
			continue
		}
		historical, err := marketingCheckinActivityDay(activity, user.CreatedAt, date, now, makeup, qualifiedAt)
		if err != nil {
			return nil, err
		}
		if !historical {
			continue
		}
		days = append(days, targetDay)
		snapshot := marketingActivityRuleSnapshot{ActivityID: activity.ID, Revision: activity.Revision, UserID: userID, Spec: spec, Claims: []MarketingActivityCheckinClaim{}}
		if len(spec.Rewards) > 0 && (!makeup || spec.Checkin.MakeupDaily) {
			// Dates are a stable daily ordinal independent from names or renumbering.
			snapshot.Claims = append(snapshot.Claims, MarketingActivityCheckinClaim{ActivityID: activity.ID, Ordinal: date.Unix()/86400 + 1})
		}
		segment, err := marketing.CheckinSegment(days, targetDay)
		if err != nil {
			return nil, err
		}
		for _, milestone := range spec.Checkin.Milestones {
			eligibleCount, err := marketing.CheckinMilestoneCount(segment, milestone.Days, spec.Checkin.CycleDays)
			if err != nil {
				return nil, err
			}
			var priorClaims []MarketingCheckinClaim
			if err := lockForUpdate(tx).Where("user_id = ? AND activity_id = ? AND milestone_days = ?", userID, activity.ID, milestone.Days).Order("ordinal DESC").Find(&priorClaims).Error; err != nil {
				return nil, err
			}
			var highwater, claimedInSegment int64
			if len(priorClaims) > 0 {
				highwater = priorClaims[0].Ordinal
			}
			for _, claim := range priorClaims {
				if claim.AnchorDay >= segment[0] && claim.AnchorDay <= segment[len(segment)-1] {
					claimedInSegment++
				}
			}
			if spec.Checkin.CycleDays == 0 {
				claimedInSegment = int64(len(priorClaims))
			}
			for range max(int64(0), eligibleCount-claimedInSegment) {
				highwater++
				snapshot.Claims = append(snapshot.Claims, MarketingActivityCheckinClaim{ActivityID: activity.ID, MilestoneDays: milestone.Days, Ordinal: highwater})
				claims = append(claims, MarketingCheckinClaim{UserID: userID, ActivityID: activity.ID, MilestoneDays: milestone.Days, Ordinal: highwater, RecordedAt: now, AnchorDay: targetDay})
			}
		}
		snapshots = append(snapshots, snapshot)
		records = append(records, MarketingCheckinDay{UserID: userID, ActivityID: activity.ID, Day: targetDay, Makeup: makeup, RecordedAt: now, QualificationDay: paidDay, QualifiedPaidCents: paid})
		result.ActivityIDs = append(result.ActivityIDs, activity.ID)
	}
	// Resolve exclusivity before recording participation or consuming a makeup
	// allowance. A milestone-only activity participates even on a no-prize day.
	selected := selectMarketingCheckinActivities(snapshots)
	filtered := snapshots[:0]
	for _, snapshot := range snapshots {
		if selected[snapshot.ActivityID] {
			filtered = append(filtered, snapshot)
		}
	}
	snapshots = filtered
	filteredRecords := records[:0]
	result.ActivityIDs = []int64{}
	for _, record := range records {
		if selected[record.ActivityID] {
			filteredRecords = append(filteredRecords, record)
			result.ActivityIDs = append(result.ActivityIDs, record.ActivityID)
		}
	}
	records = filteredRecords
	filteredClaims := claims[:0]
	for _, claim := range claims {
		if selected[claim.ActivityID] {
			filteredClaims = append(filteredClaims, claim)
		}
	}
	claims = filteredClaims
	if len(records) == 0 {
		return nil, common.NewMessage("No eligible check-in activity for this date")
	}
	specs := make([]MarketingActivitySpec, 0, len(snapshots))
	for _, snapshot := range snapshots {
		specs = append(specs, snapshot.Spec)
	}
	ceilings, err := marketingActivityPoolCeilings(tx, specs)
	if err != nil {
		return nil, err
	}
	payload := marketingActivityPayload{CheckinDay: targetDay}
	for i := range snapshots {
		snapshots[i].Ceilings = ceilings
		payload.Claims = append(payload.Claims, snapshots[i].Claims...)
	}
	rules, err := common.Marshal(snapshots)
	if err != nil {
		return nil, err
	}
	data, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hash, err := marketingDigest(payload)
	if err != nil {
		return nil, err
	}
	fact := MarketingActivityFact{Sequence: sequence, SourceKey: key, SourceReference: "checkin:" + targetDay, UserID: userID, Kind: "checkin", OccurredAt: now, PayloadHash: hash, Payload: string(data), Rules: string(rules), State: "captured", DeliveryState: "pending"}
	if err := tx.Create(&fact).Error; err != nil {
		return nil, err
	}
	for i := range records {
		records[i].FactID = fact.ID
	}
	if err := tx.Create(&records).Error; err != nil {
		return nil, err
	}
	if len(claims) > 0 {
		for i := range claims {
			claims[i].FactID = fact.ID
		}
		if err := tx.Create(&claims).Error; err != nil {
			return nil, err
		}
	}
	result.FactID = fact.ID
	return result, nil
}

// The calendar and mutation path resolve the same qualified candidates, including
// enabled activities hidden from the public list.
func selectMarketingCheckinActivities(snapshots []marketingActivityRuleSnapshot) map[int64]bool {
	winners := map[string]int64{}
	winnerSpecs := map[string]MarketingActivitySpec{}
	for _, snapshot := range snapshots {
		group := snapshot.Spec.ExclusiveGroup
		if group == "" {
			continue
		}
		prior, ok := winnerSpecs[group]
		if !ok || snapshot.Spec.Priority > prior.Priority || (snapshot.Spec.Priority == prior.Priority && snapshot.ActivityID < winners[group]) {
			winners[group] = snapshot.ActivityID
			winnerSpecs[group] = snapshot.Spec
		}
	}

	selected := map[int64]bool{}
	for _, snapshot := range snapshots {
		if snapshot.Spec.ExclusiveGroup == "" || winners[snapshot.Spec.ExclusiveGroup] == snapshot.ActivityID {
			selected[snapshot.ActivityID] = true
		}
	}
	return selected
}

func marketingCheckinActivityDay(activity MarketingActivity, createdAt int64, date time.Time, now int64, makeup bool, qualifiedAt int64) (bool, error) {
	start := max(date.Unix(), activity.StartsAt, activity.EnabledAt, activity.CreatedAt, createdAt, qualifiedAt)
	end := date.AddDate(0, 0, 1).Unix()
	if activity.EndsAt > 0 {
		end = min(end, activity.EndsAt)
	}
	end = min(end, now+1)
	if start >= end {
		return false, nil
	}
	var pauses []marketingActivityPause
	if err := common.UnmarshalJsonStr(activity.Pauses, &pauses); err != nil {
		return false, err
	}
	if !makeup {
		start = now
		end = now + 1
	}
	for _, pause := range pauses {
		pauseEnd := pause.End
		if pauseEnd == 0 {
			pauseEnd = end
		}
		if pause.Start <= start && pauseEnd > start {
			start = pauseEnd
		}
		if pause.Start > start && pause.Start < end {
			return true, nil
		}
	}
	return start < end, nil
}

func marketingCheckinPaidQualification(tx *gorm.DB, userID int, spec MarketingActivityCheckinSpec, date time.Time, now int64) (bool, string, int64, int64, error) {
	qualifiedAt := date.Unix()
	if spec.Condition == "none" {
		return true, "", 0, qualifiedAt, nil
	}
	if spec.Condition == "previous_day_paid" {
		date = date.AddDate(0, 0, -1)
	}
	if spec.Condition != "today_paid" && spec.Condition != "previous_day_paid" {
		return false, "", 0, 0, ErrMarketingInput
	}
	var payments []MarketingPayment
	if err := tx.Select("id", "paid_cents", "posted_at").Where("user_id = ? AND posted_at >= ? AND posted_at < ?", userID, date.Unix(), min(date.AddDate(0, 0, 1).Unix(), now+1)).Order("posted_at, id").Find(&payments).Error; err != nil {
		return false, "", 0, 0, err
	}
	var total int64
	for _, payment := range payments {
		if payment.PaidCents <= 0 {
			return false, "", 0, 0, ErrMarketingConflict
		}
		if payment.PaidCents >= spec.ThresholdCents-total {
			total = spec.ThresholdCents
			if spec.Condition == "today_paid" {
				qualifiedAt = payment.PostedAt
			}
			break
		}
		total += payment.PaidCents
	}
	return total >= spec.ThresholdCents, date.Format("2006-01-02"), total, qualifiedAt, nil
}

func GetMarketingCheckinCalendar(ctx context.Context, userID int, month string, now int64) (*MarketingCheckinCalendar, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	if month == "" {
		month = time.Unix(now, 0).In(marketing.ShanghaiCalendar).Format("2006-01")
	}
	start, err := time.ParseInLocation("2006-01", month, marketing.ShanghaiCalendar)
	if err != nil || start.Format("2006-01") != month {
		return nil, common.NewMessage("Invalid check-in month")
	}
	result := &MarketingCheckinCalendar{Month: month, Today: time.Unix(now, 0).In(marketing.ShanghaiCalendar).Format("2006-01-02"), Activities: []MarketingCheckinCalendarActivity{}}
	err = WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
			return err
		}
		var activities []MarketingActivity
		if err := tx.Where("kind = ? AND enabled_at > 0 AND ((starts_at < ? AND (ends_at = 0 OR ends_at > ?)) OR (starts_at <= ? AND (ends_at = 0 OR ends_at > ?)))", "checkin", start.AddDate(0, 1, 0).Unix(), start.Unix(), now, now).Order("id").Find(&activities).Error; err != nil {
			return err
		}
		result.Activities = []MarketingCheckinCalendarActivity{}
		var user User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		factKey, err := marketingSourceKey("activity_fact:checkin", fmt.Sprintf("%d:%s", userID, result.Today))
		if err != nil {
			return err
		}
		var todayFact int64
		if err := tx.Model(&MarketingActivityFact{}).Where("source_key = ?", factKey).Count(&todayFact).Error; err != nil {
			return err
		}
		for _, activity := range activities {
			item := MarketingCheckinCalendarActivity{Activity: activity, Records: []MarketingCheckinDay{}, TodayEligibility: "inactive", MakeupDates: []string{}}
			if err := common.UnmarshalJsonStr(activity.Spec, &item.Spec); err != nil {
				return err
			}
			if item.Spec.Checkin == nil {
				return ErrMarketingConflict
			}
			var history []MarketingCheckinDay
			if err := tx.Where("user_id = ? AND activity_id = ?", userID, activity.ID).Order("day").Find(&history).Error; err != nil {
				return err
			}
			days := make([]string, 0, len(history))
			signed := false
			for _, record := range history {
				days = append(days, record.Day)
				if record.Makeup {
					item.MakeupUsed++
				}
				if record.Day == result.Today {
					signed = true
				}
				if record.Day >= start.Format("2006-01-02") && record.Day < start.AddDate(0, 1, 0).Format("2006-01-02") {
					item.Records = append(item.Records, record)
				}
			}
			item.MakeupRemaining = max(0, item.Spec.Checkin.MakeupLimit-item.MakeupUsed)
			item.Streak = marketing.CheckinCurrentStreak(days, time.Unix(now, 0))
			if activity.StartsAt <= now && (activity.EndsAt == 0 || activity.EndsAt > now) && !activity.Paused {
				item.TodayEligibility = "eligible"
				today, _ := marketing.ParseCheckinDay(result.Today)
				eligible, _, _, qualifiedAt, err := marketingCheckinPaidQualification(tx, userID, *item.Spec.Checkin, today, now)
				if err != nil {
					return err
				}
				if !eligible {
					item.TodayEligibility = "payment_required"
				} else {
					active, err := marketingCheckinActivityDay(activity, user.CreatedAt, today, now, false, qualifiedAt)
					if err != nil {
						return err
					}
					if !active {
						item.TodayEligibility = "inactive"
					}
				}
				if signed || todayFact > 0 {
					item.TodayEligibility = "already_signed"
				}
				if item.MakeupRemaining > 0 && item.Spec.Checkin.MakeupDays > 0 {
					earliest := today.AddDate(0, 0, -item.Spec.Checkin.MakeupDays)
					for target := start; target.Before(start.AddDate(0, 1, 0)) && target.Before(today); target = target.AddDate(0, 0, 1) {
						if target.Before(earliest) {
							continue
						}
						targetDay := target.Format("2006-01-02")
						exists := false
						for _, record := range history {
							if record.Day == targetDay {
								exists = true
								break
							}
						}
						if exists {
							continue
						}
						key, err := marketingSourceKey("activity_fact:checkin", fmt.Sprintf("%d:%s", userID, targetDay))
						if err != nil {
							return err
						}
						var count int64
						if err := tx.Model(&MarketingActivityFact{}).Where("source_key = ?", key).Count(&count).Error; err != nil {
							return err
						}
						if count > 0 {
							continue
						}
						qualified, _, _, qualifiedAt, err := marketingCheckinPaidQualification(tx, userID, *item.Spec.Checkin, target, now)
						if err != nil {
							return err
						}
						historical, err := marketingCheckinActivityDay(activity, user.CreatedAt, target, now, true, qualifiedAt)
						if err != nil {
							return err
						}
						if !historical {
							continue
						}
						if qualified {
							item.MakeupDates = append(item.MakeupDates, targetDay)
						}
					}
				}
			}
			result.Activities = append(result.Activities, item)
		}
		byDay := map[string][]marketingActivityRuleSnapshot{}
		for _, item := range result.Activities {
			snapshot := marketingActivityRuleSnapshot{ActivityID: item.Activity.ID, Spec: item.Spec}
			if item.TodayEligibility == "eligible" {
				byDay[result.Today] = append(byDay[result.Today], snapshot)
			}
			for _, day := range item.MakeupDates {
				byDay[day] = append(byDay[day], snapshot)
			}
		}
		winners := map[string]map[int64]bool{}
		for day, candidates := range byDay {
			winners[day] = selectMarketingCheckinActivities(candidates)
		}
		result.CheckedInToday = todayFact > 0
		result.CanCheckin = !result.CheckedInToday && len(winners[result.Today]) > 0
		visible := make([]MarketingCheckinCalendarActivity, 0, len(result.Activities))
		for _, item := range result.Activities {
			if !item.Activity.Published || item.Activity.StartsAt >= start.AddDate(0, 1, 0).Unix() || (item.Activity.EndsAt > 0 && item.Activity.EndsAt <= start.Unix()) {
				continue
			}
			if item.TodayEligibility == "eligible" && !winners[result.Today][item.Activity.ID] {
				item.TodayEligibility = "suppressed"
			}
			available := make([]string, 0, len(item.MakeupDates))
			for _, day := range item.MakeupDates {
				if winners[day][item.Activity.ID] {
					available = append(available, day)
				}
			}
			item.MakeupDates = available
			visible = append(visible, item)
		}
		result.Activities = visible

		return nil
	})
	return result, err
}
