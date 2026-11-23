package service

import (
	"context"
	"net/url"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MarketingUserFilter struct {
	Page, PageSize            int
	Kind, State, ActivityKind string
	StartsAt, EndsAt          int64
}

func (f MarketingUserFilter) Validate() error {
	if f.Page < 1 || f.Page > 1000000 || f.PageSize < 1 || f.PageSize > 100 || f.StartsAt < 0 || f.EndsAt < 0 || (f.EndsAt > 0 && f.StartsAt >= f.EndsAt) {
		return model.ErrMarketingInput
	}
	return nil
}

type MarketingUserActivityForm struct {
	Name         string                 `json:"name"`
	Kind         string                 `json:"kind"`
	Condition    string                 `json:"condition"`
	Frequency    string                 `json:"frequency"`
	StartsAt     int64                  `json:"starts_at"`
	EndsAt       int64                  `json:"ends_at"`
	ThresholdCNY string                 `json:"threshold_cny,omitempty"`
	Rewards      []MarketingAdminReward `json:"rewards"`
	Checkin      *MarketingAdminCheckin `json:"checkin"`
}
type MarketingUserPoolSummary struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	State             string `json:"state"`
	StartsAt          int64  `json:"starts_at"`
	EndsAt            int64  `json:"ends_at"`
	Capacity          int64  `json:"capacity"`
	RemainingCapacity int64  `json:"remaining_capacity"`
}
type MarketingUserActivity struct {
	ID        int64                      `json:"id"`
	State     string                     `json:"state"`
	ServerNow int64                      `json:"server_now"`
	Activity  MarketingUserActivityForm  `json:"activity"`
	Pools     []MarketingUserPoolSummary `json:"pools"`
}

func marketingUserPoolSummary(pool model.MarketingLotteryPool, now int64) MarketingUserPoolSummary {
	state := "active"
	if now < pool.StartsAt {
		state = "scheduled"
	}
	if pool.Paused {
		state = "paused"
	}
	if now >= pool.EndsAt {
		state = "ended"
	}
	return MarketingUserPoolSummary{pool.ID, pool.Name, state, pool.StartsAt, pool.EndsAt, pool.Capacity, max(int64(0), pool.Capacity-pool.Issued)}
}
func marketingUserActivityView(tx *gorm.DB, row model.MarketingActivity, now int64) (MarketingUserActivity, error) {
	v, err := marketingAdminActivityView(tx, row, now)
	if err != nil {
		return MarketingUserActivity{}, err
	}
	a := v.Activity
	result := MarketingUserActivity{ID: row.ID, State: v.State, ServerNow: now, Activity: MarketingUserActivityForm{a.Name, a.Kind, a.Condition, a.Frequency, a.StartsAt, a.EndsAt, a.ThresholdCNY, a.Rewards, a.Checkin}, Pools: []MarketingUserPoolSummary{}}
	rewards := slices.Clone(a.Rewards)
	if a.Checkin != nil {
		for _, m := range a.Checkin.Milestones {
			rewards = append(rewards, m.Rewards...)
		}
	}
	ids := []int64{}
	for _, r := range rewards {
		if r.Kind == "chance" && !slices.Contains(ids, r.PoolID) {
			ids = append(ids, r.PoolID)
		}
	}
	if len(ids) > 0 {
		var pools []model.MarketingLotteryPool
		if err := tx.Where("id IN ?", ids).Order("id").Find(&pools).Error; err != nil {
			return result, err
		}
		for _, p := range pools {
			result.Pools = append(result.Pools, marketingUserPoolSummary(p, now))
		}
	}
	return result, nil
}
func ReadMarketingUserActivities(ctx context.Context, userID int, id int64, f MarketingUserFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if f.Kind != "" && !slices.Contains([]string{"registration", "checkin", "recharge", "invitation"}, f.Kind) {
		return nil, model.ErrMarketingInput
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingUser(tx, userID); err != nil {
			return err
		}
		now := time.Now().Unix()
		q := model.PublicMarketingActivities(tx, now)
		if id > 0 {
			q = q.Where("id = ?", id)
		}
		if f.Kind != "" {
			q = q.Where("kind = ?", f.Kind)
		}
		switch f.State {
		case "":
		case "active":
			q = q.Where("starts_at <= ? AND paused = ? AND (ends_at = ? OR ends_at > ?)", now, false, 0, now)
		case "scheduled":
			q = q.Where("starts_at > ? AND paused = ?", now, false)
		case "paused":
			q = q.Where("paused = ? AND (ends_at = ? OR ends_at > ?)", true, 0, now)
		case "ended":
			q = q.Where("ends_at > ? AND ends_at <= ?", 0, now)
		default:
			return model.ErrMarketingInput
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		var rows []model.MarketingActivity
		// CASE uses bound values; no dialect-specific boolean literals or user SQL.
		order := gorm.Expr("CASE WHEN ends_at > 0 AND ends_at <= ? THEN 2 WHEN starts_at > ? THEN 1 ELSE 0 END, starts_at DESC, id DESC", now, now)
		if err := q.Clauses(clause.OrderBy{Expression: order}).Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
			return err
		}
		items := []MarketingUserActivity{}
		for _, row := range rows {
			item, err := marketingUserActivityView(tx, row, now)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		if id > 0 {
			if len(items) != 1 {
				return gorm.ErrRecordNotFound
			}
			result = items[0]
		} else {
			result = MarketingAdminPage{Items: items, Total: total, Page: f.Page, PageSize: f.PageSize}
		}
		return nil
	})
	return result, err
}

// Invitation relationships are factual and retained when activities end. Only
// the owner's link and friend names/dates cross the console boundary.
func ReadMarketingUserInvitation(ctx context.Context, userID int, f MarketingUserFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingUser(tx, userID); err != nil {
			return err
		}
		var user model.User
		if err := tx.Select("id", "aff_code").First(&user, userID).Error; err != nil {
			return err
		}
		if user.AffCode == "" {
			user.AffCode = common.GetRandomString(8)
			if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("aff_code", user.AffCode).Error; err != nil {
				return err
			}
		}
		type friend struct {
			Username string `json:"username"`
			JoinedAt int64  `json:"joined_at"`
		}
		friends := []friend{}
		var total int64
		q := tx.Model(&model.MarketingInvitation{}).Where("inviter_id = ?", userID)
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		var rows []model.MarketingInvitation
		if err := q.Order("established_at DESC, user_id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var u model.User
			if err := tx.Unscoped().Select("id", "username").First(&u, row.UserID).Error; err != nil {
				return err
			}
			friends = append(friends, friend{u.Username, row.EstablishedAt})
		}
		result = map[string]any{"invitation_code": user.AffCode, "invitation_path": "/sign-up?aff=" + url.QueryEscape(user.AffCode), "friend_count": total, "friends": MarketingAdminPage{Items: friends, Total: total, Page: f.Page, PageSize: f.PageSize}}
		return nil
	})
	return result, err
}

// Public pool membership comes only from published activities or the owner's
// durable opportunities. Batch scanning keeps configuration decoding bounded;
// it never uses database-specific JSON operators.
func marketingUserVisiblePoolIDs(tx *gorm.DB, userID int, now int64) ([]int64, error) {
	ids := []int64{}
	if err := tx.Model(&model.MarketingLotteryChance{}).Where("user_id = ?", userID).Distinct("pool_id").Pluck("pool_id", &ids).Error; err != nil {
		return nil, err
	}
	var after int64
	for {
		var rows []model.MarketingActivity
		if err := model.PublicMarketingActivities(tx, now).Where("id > ?", after).Order("id").Limit(100).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			after = row.ID
			var spec model.MarketingActivitySpec
			if err := common.UnmarshalJsonStr(row.Spec, &spec); err != nil {
				return nil, err
			}
			rewards := slices.Clone(spec.Rewards)
			if spec.Checkin != nil {
				for _, m := range spec.Checkin.Milestones {
					rewards = append(rewards, m.Rewards...)
				}
			}
			for _, r := range rewards {
				if r.Kind == "chance" && !slices.Contains(ids, r.PoolID) {
					ids = append(ids, r.PoolID)
				}
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	return ids, nil
}
