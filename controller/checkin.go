package controller

import (
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// Only display amounts cross the console boundary. Conversion ratios, stored
// balances, qualification proofs and activity decision internals stay private.
type marketingCheckinRewardView struct {
	Kind         string `json:"kind"`
	Amount       string `json:"amount,omitempty"`
	Currency     string `json:"currency,omitempty"`
	ValidSeconds int64  `json:"valid_seconds"`
	PoolID       int64  `json:"pool_id,omitempty"`
	Count        int64  `json:"count,omitempty"`
}
type marketingCheckinMilestoneView struct {
	Days    int                          `json:"days"`
	Rewards []marketingCheckinRewardView `json:"rewards"`
}
type marketingCheckinDayView struct {
	Date   string `json:"date"`
	Makeup bool   `json:"makeup"`
}
type marketingCheckinActivityView struct {
	ID                 int64                           `json:"id"`
	Name               string                          `json:"name"`
	StartsAt           int64                           `json:"starts_at"`
	EndsAt             int64                           `json:"ends_at"`
	Paused             bool                            `json:"paused"`
	Condition          string                          `json:"condition"`
	RequiredPaymentCNY string                          `json:"required_payment_cny"`
	CycleDays          int                             `json:"cycle_days"`
	MakeupDays         int                             `json:"makeup_days"`
	MakeupLimit        int                             `json:"makeup_limit"`
	MakeupDaily        bool                            `json:"makeup_daily"`
	MakeupUsed         int                             `json:"makeup_used"`
	MakeupRemaining    int                             `json:"makeup_remaining"`
	MakeupDates        []string                        `json:"makeup_dates"`
	Streak             int                             `json:"streak"`
	TodayEligibility   string                          `json:"today_eligibility"`
	DailyRewards       []marketingCheckinRewardView    `json:"daily_rewards"`
	Milestones         []marketingCheckinMilestoneView `json:"milestones"`
	Records            []marketingCheckinDayView       `json:"records"`
}

func marketingCheckinRewardsView(rewards []model.MarketingActivityRewardSpec) ([]marketingCheckinRewardView, error) {
	result := make([]marketingCheckinRewardView, 0, len(rewards))
	for _, reward := range rewards {
		item := marketingCheckinRewardView{Kind: reward.Kind, ValidSeconds: reward.ValidSeconds, PoolID: reward.PoolID, Count: reward.Count}
		if reward.Kind == "gift" {
			quota, err := reward.Money.Quota()
			if err != nil {
				return nil, err
			}
			item.Amount, item.Currency, err = service.MarketingWalletDisplayAmount(quota)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, item)
	}
	return result, nil
}

func GetCheckinStatus(c *gin.Context) {
	calendar, err := model.GetMarketingCheckinCalendar(c.Request.Context(), c.GetInt("id"), c.Query("month"), time.Now().Unix())
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	activities := make([]marketingCheckinActivityView, 0, len(calendar.Activities))
	for _, activity := range calendar.Activities {
		rule := activity.Spec.Checkin
		item := marketingCheckinActivityView{
			ID: activity.Activity.ID, Name: activity.Activity.Name, StartsAt: activity.Activity.StartsAt, EndsAt: activity.Activity.EndsAt, Paused: activity.Activity.Paused,
			Condition: rule.Condition, RequiredPaymentCNY: decimal.NewFromInt(rule.ThresholdCents).Shift(-2).StringFixed(2), CycleDays: rule.CycleDays,
			MakeupDays: rule.MakeupDays, MakeupLimit: rule.MakeupLimit, MakeupDaily: rule.MakeupDaily, MakeupUsed: activity.MakeupUsed, MakeupRemaining: activity.MakeupRemaining,
			MakeupDates: activity.MakeupDates, Streak: activity.Streak, TodayEligibility: activity.TodayEligibility,
			Milestones: []marketingCheckinMilestoneView{}, Records: []marketingCheckinDayView{},
		}
		item.DailyRewards, err = marketingCheckinRewardsView(activity.Spec.Rewards)
		if err != nil {
			marketingAPIError(c, err)
			return
		}
		for _, milestone := range rule.Milestones {
			rewards, err := marketingCheckinRewardsView(milestone.Rewards)
			if err != nil {
				marketingAPIError(c, err)
				return
			}
			item.Milestones = append(item.Milestones, marketingCheckinMilestoneView{Days: milestone.Days, Rewards: rewards})
		}
		for _, record := range activity.Records {
			item.Records = append(item.Records, marketingCheckinDayView{Date: record.Day, Makeup: record.Makeup})
		}
		activities = append(activities, item)
	}
	common.ApiSuccess(c, gin.H{
		"month": calendar.Month, "today": calendar.Today, "activities": activities,
		"can_checkin": calendar.CanCheckin, "checked_in_today": calendar.CheckedInToday,
	})
}

func DoCheckin(c *gin.Context) {
	submitActivityCheckin(c, "")
}

func DoMakeupCheckin(c *gin.Context) {
	var request struct {
		Date string `json:"date"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	if err != nil || common.Unmarshal(body, &request) != nil || len(request.Date) != 10 {
		common.ApiErrorT(c, "Invalid check-in date")
		return
	}
	submitActivityCheckin(c, request.Date)
}

func submitActivityCheckin(c *gin.Context, targetDay string) {
	result, err := service.SubmitMarketingCheckin(c.Request.Context(), c.GetInt("id"), targetDay)
	if result == nil {
		marketingAPIError(c, err)
		return
	}
	if err != nil {
		// The calendar and obligations committed. Never report a failed check-in
		// or leak a database error when only automatic delivery needs recovery.
		logger.LogError(c.Request.Context(), common.LogText("Marketing check-in reward processing failed for user %d", c.GetInt("id")))
	}
	common.ApiSuccess(c, gin.H{
		"date": result.Checkin.Day, "makeup": result.Checkin.Makeup,
		"already_recorded": result.Checkin.AlreadyRecorded, "activity_ids": result.Checkin.ActivityIDs,
		"rewards_pending": result.Pending,
	})
}
