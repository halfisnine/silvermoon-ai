package service

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type MarketingAdminRewardRecord struct {
	ID         int64                  `json:"id"`
	Kind       string                 `json:"kind"`
	UserID     int                    `json:"user_id"`
	Username   string                 `json:"username"`
	SourceName string                 `json:"source_name"`
	State      string                 `json:"state"`
	OccurredAt int64                  `json:"occurred_at"`
	CreditedAt int64                  `json:"credited_at"`
	Rewards    []MarketingAdminReward `json:"rewards"`
	Source     map[string]any         `json:"source"`
}

// Reward provenance is resolved from durable business rows, never from log
// text. This projection deliberately excludes redemption keys, probability
// seeds, credentials and internal pricing/rule snapshots.
func ReadMarketingAdminRewards(ctx context.Context, actorID int, id int64, f model.MarketingAdminFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if f.Kind == "" {
		f.Kind = "activity"
	}
	if f.Kind != "activity" && f.Kind != "lottery" {
		return nil, model.ErrMarketingInput
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
			return err
		}
		if f.Username != "" {
			var user model.User
			err := tx.Unscoped().Select("id").Where("username = ?", f.Username).First(&user).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				result = MarketingAdminPage{Items: []MarketingAdminRewardRecord{}, Total: 0, Page: f.Page, PageSize: f.PageSize}
				if id > 0 {
					return gorm.ErrRecordNotFound
				}
				return nil
			}
			if err != nil {
				return err
			}
			if f.UserID > 0 && f.UserID != user.Id {
				return model.ErrMarketingInput
			}
			f.UserID = user.Id
		}
		records := []MarketingAdminRewardRecord{}
		var total int64
		if f.Kind == "activity" {
			q := tx.Model(&model.MarketingActivityReward{})
			if id > 0 {
				q = q.Where("id = ?", id)
			}
			if f.UserID > 0 {
				q = q.Where("user_id = ?", f.UserID)
			}
			if f.ActivityID > 0 {
				q = q.Where("activity_id = ?", f.ActivityID)
			}
			if f.StartsAt > 0 || f.EndsAt > 0 {
				facts := tx.Model(&model.MarketingActivityFact{}).Select("id")
				if f.StartsAt > 0 {
					facts = facts.Where("occurred_at >= ?", f.StartsAt)
				}
				if f.EndsAt > 0 {
					facts = facts.Where("occurred_at < ?", f.EndsAt)
				}
				q = q.Where("fact_id IN (?)", facts)
			}
			if err := q.Count(&total).Error; err != nil {
				return err
			}
			var rows []model.MarketingActivityReward
			if err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				record, err := marketingAdminActivityRewardView(tx, row)
				if err != nil {
					return err
				}
				records = append(records, record)
			}
		} else {
			if f.ActivityID > 0 {
				return model.ErrMarketingInput
			}
			q := tx.Model(&model.MarketingLotteryDraw{})
			if id > 0 {
				q = q.Where("id = ?", id)
			}
			if f.UserID > 0 {
				q = q.Where("user_id = ?", f.UserID)
			}
			if f.StartsAt > 0 {
				q = q.Where("drawn_at >= ?", f.StartsAt)
			}
			if f.EndsAt > 0 {
				q = q.Where("drawn_at < ?", f.EndsAt)
			}
			if err := q.Count(&total).Error; err != nil {
				return err
			}
			var rows []model.MarketingLotteryDraw
			if err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				record, err := marketingAdminLotteryRewardView(tx, row)
				if err != nil {
					return err
				}
				records = append(records, record)
			}
		}
		if id > 0 {
			if len(records) != 1 {
				return gorm.ErrRecordNotFound
			}
			result = records[0]
		} else {
			result = MarketingAdminPage{Items: records, Total: total, Page: f.Page, PageSize: f.PageSize}
		}
		return nil
	})
	return result, err
}
func marketingAdminActivityRewardView(tx *gorm.DB, row model.MarketingActivityReward) (MarketingAdminRewardRecord, error) {
	view := MarketingAdminRewardRecord{ID: row.ID, Kind: "activity", UserID: row.UserID, State: row.State, CreditedAt: row.CreditedAt, Source: map[string]any{}}
	configuration, err := CurrentMarketingMoneyContext()
	if err != nil {
		return view, err
	}
	var user model.User
	if err := tx.Unscoped().Select("id", "username").First(&user, row.UserID).Error; err != nil {
		return view, err
	}
	view.Username = user.Username
	var activity model.MarketingActivity
	if err := tx.First(&activity, row.ActivityID).Error; err != nil {
		return view, err
	}
	view.SourceName = activity.Name
	var fact model.MarketingActivityFact
	if err := tx.First(&fact, row.FactID).Error; err != nil {
		return view, err
	}
	view.OccurredAt = fact.OccurredAt
	view.Source["reason_key"] = marketingAdminRewardReason(row.State)
	view.Source["activity_id"] = row.ActivityID
	view.Source["trigger"] = fact.Kind
	var spec model.MarketingActivityRewardSpec
	if err := common.UnmarshalJsonStr(row.Spec, &spec); err != nil {
		return view, err
	}
	view.Rewards, err = marketingAdminRewardsView(tx, []model.MarketingActivityRewardSpec{spec}, configuration)
	if err != nil {
		return view, err
	}
	// A percentage rule describes how a reward was calculated. Its actual
	// credited amount comes from the gift/amount row, not its placeholder rule.
	if row.GiftID > 0 {
		var gift model.MarketingGift
		if err := tx.First(&gift, row.GiftID).Error; err != nil {
			return view, err
		}
		amount, currency, err := marketingAdminDisplayAmount(gift.GrantedQuota, configuration)
		if err != nil {
			return view, err
		}
		view.Rewards[0].Amount, view.Rewards[0].Currency = amount, currency
		view.Source["expires_at"] = gift.ExpiresAt
		view.Rewards[0].ExpiresAt = &gift.ExpiresAt
		view.Rewards[0].CreditedAt = gift.GrantedAt
		view.Rewards[0].State = row.State
	} else if row.Amount != "" {
		var amount model.MarketingRewardAmount
		if err := common.UnmarshalJsonStr(row.Amount, &amount); err != nil {
			return view, err
		}
		view.Rewards[0].Amount, view.Rewards[0].Currency, err = marketingAdminDisplayAmount(amount.Quota, configuration)
		if err != nil {
			return view, err
		}
	}
	if row.Kind == "chance" && row.IssueID > 0 && row.State == "issued" {
		var issue model.MarketingLotteryIssue
		if err := tx.First(&issue, row.IssueID).Error; err != nil {
			return view, err
		}
		view.CreditedAt = issue.RecordedAt
		view.Rewards[0].CreditedAt = issue.RecordedAt
		view.Rewards[0].State = row.State
		var chance model.MarketingLotteryChance
		if err := tx.Where("issue_id = ?", row.IssueID).First(&chance).Error; err != nil {
			return view, err
		}
		view.Rewards[0].ExpiresAt = &chance.ExpiresAt
	}
	if fact.PaymentID > 0 {
		var payment model.MarketingPayment
		if err := tx.First(&payment, fact.PaymentID).Error; err != nil {
			return view, err
		}
		view.Source["payment_id"] = payment.ID
		if payment.SourceKind == "online" {
			var order model.TopUp
			err := tx.Select("id", "trade_no").Where("payment_id = ?", payment.ID).First(&order).Error
			if err == nil {
				view.Source["payment_reference"] = order.TradeNo
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return view, err
			}
		}
		view.Source["payment_kind"] = payment.SourceKind
		view.Source["paid_cny"] = decimal.NewFromInt(payment.PaidCents).Shift(-2).String()
		view.Source["posted_at"] = payment.PostedAt
		var redemption model.Redemption
		err := tx.Unscoped().Select("id", "name", "sales_channel", "sales_batch", "sales_evidence").Where("payment_id = ?", payment.ID).First(&redemption).Error
		if err == nil {
			view.Source["redemption_name"] = redemption.Name
			view.Source["sales_channel"] = redemption.SalesChannel
			view.Source["sales_batch"] = redemption.SalesBatch
			view.Source["sales_evidence"] = redemption.SalesEvidence
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return view, err
		}
	}
	var payload struct{ CheckinDay string }
	if err := common.UnmarshalJsonStr(fact.Payload, &payload); err != nil {
		return view, err
	}
	if payload.CheckinDay != "" {
		view.Source["checkin_date"] = payload.CheckinDay
	}
	if activity.Kind == "invitation" {
		var friend model.User
		if err := tx.Unscoped().Select("id", "username").First(&friend, fact.UserID).Error; err != nil {
			return view, err
		}
		view.Source["friend_username"] = friend.Username
	}
	return view, nil
}
func marketingAdminLotteryRewardView(tx *gorm.DB, row model.MarketingLotteryDraw) (MarketingAdminRewardRecord, error) {
	view := MarketingAdminRewardRecord{ID: row.ID, Kind: "lottery", UserID: row.UserID, State: row.State, OccurredAt: row.DrawnAt, Rewards: []MarketingAdminReward{}, Source: map[string]any{"pool_id": row.PoolID}}
	configuration, err := CurrentMarketingMoneyContext()
	if err != nil {
		return view, err
	}
	var user model.User
	if err := tx.Unscoped().Select("id", "username").First(&user, row.UserID).Error; err != nil {
		return view, err
	}
	view.Username = user.Username
	var pool model.MarketingLotteryPool
	if err := tx.First(&pool, row.PoolID).Error; err != nil {
		return view, err
	}
	view.SourceName = pool.Name
	var prize model.MarketingLotteryPrize
	if err := common.UnmarshalJsonStr(row.PrizeSnapshot, &prize); err != nil {
		return view, err
	}
	view.Source["prize_name"] = prize.Name
	var awards []model.MarketingLotteryAward
	if err := tx.Where("draw_id = ?", row.ID).Order("position").Find(&awards).Error; err != nil {
		return view, err
	}
	view.Source["no_prize"] = len(awards) == 0
	for _, award := range awards {
		var spec model.MarketingLotteryGiftSpec
		if err := common.UnmarshalJsonStr(award.GiftSpec, &spec); err != nil {
			return view, err
		}
		quota, err := spec.Money.Quota()
		if err != nil {
			return view, err
		}
		amount, currency, err := marketingAdminDisplayAmount(quota, configuration)
		if err != nil {
			return view, err
		}
		item := MarketingAdminReward{Kind: "gift", Calculation: "fixed", Amount: amount, Currency: currency, ValidSeconds: spec.ValidSeconds, State: award.State, CreditedAt: award.CreditedAt}
		if award.GiftID > 0 {
			var gift model.MarketingGift
			if err := tx.First(&gift, award.GiftID).Error; err != nil {
				return view, err
			}
			item.ExpiresAt = &gift.ExpiresAt
		}
		view.Rewards = append(view.Rewards, item)
		view.CreditedAt = max(view.CreditedAt, award.CreditedAt)
	}
	var chance model.MarketingLotteryChance
	if err := tx.First(&chance, row.ChanceID).Error; err != nil {
		return view, err
	}
	var origin model.MarketingActivityReward
	err = tx.Where("issue_id = ?", chance.IssueID).First(&origin).Error
	if err == nil {
		var activity model.MarketingActivity
		if err := tx.First(&activity, origin.ActivityID).Error; err != nil {
			return view, err
		}
		view.Source["opportunity_activity"] = activity.Name
		view.Source["opportunity_reward_id"] = origin.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return view, err
	}
	return view, nil
}

func marketingAdminRewardReason(state string) string {
	switch state {
	case "shortage":
		return "No opportunities remained when this reward was processed"
	case "probability_miss":
		return "The opportunity probability check did not select this reward"
	case "pool_expired":
		return "The prize pool had ended when this reward was processed"
	case "pending":
		return "Reward delivery is pending"
	case "cleared":
		return "This reward was cleared after an account refund"
	case "zero_amount":
		return "The calculated reward rounded to zero"
	default:
		return ""
	}
}
