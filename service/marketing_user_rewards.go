package service

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type MarketingUserRewardRecord struct {
	ID         int64                  `json:"id"`
	Kind       string                 `json:"kind"`
	SourceName string                 `json:"source_name"`
	State      string                 `json:"state"`
	OccurredAt int64                  `json:"occurred_at"`
	CreditedAt int64                  `json:"credited_at"`
	Rewards    []MarketingAdminReward `json:"rewards"`
	Source     map[string]any         `json:"source"`
}

func marketingUserRewardView(tx *gorm.DB, record MarketingAdminRewardRecord) (MarketingUserRewardRecord, error) {
	for i := range record.Rewards {
		if record.Rewards[i].Kind != "gift" {
			continue
		}
		var gift model.MarketingGift
		q := tx.Where("user_id = ?", record.UserID)
		if record.Kind == "activity" {
			q = q.Where("id IN (?)", tx.Model(&model.MarketingActivityReward{}).Select("gift_id").Where("id = ?", record.ID))
		} else {
			q = q.Where("id IN (?)", tx.Model(&model.MarketingLotteryAward{}).Select("gift_id").Where("draw_id = ? AND position = ?", record.ID, i))
		}
		err := q.First(&gift).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return MarketingUserRewardRecord{}, err
		}
		snapshot, err := marketingUserGiftConversion(gift)
		if err != nil {
			return MarketingUserRewardRecord{}, err
		}
		amount, currency, err := marketingAdminDisplayAmount(gift.GrantedQuota, MarketingMoneyContext{Display: snapshot})
		if err != nil {
			return MarketingUserRewardRecord{}, err
		}
		record.Rewards[i].Amount, record.Rewards[i].Currency = amount, currency
	}

	if record.State == "complete" {
		record.State = "completed"
	}
	source := map[string]any{}
	for _, key := range []string{"reason_key", "activity_id", "trigger", "checkin_date", "friend_username", "pool_id", "prize_name", "no_prize", "opportunity_activity"} {
		if value, ok := record.Source[key]; ok {
			source[key] = value
		}
	}
	if _, isInvitation := record.Source["friend_username"]; !isInvitation {
		for _, key := range []string{"payment_kind", "paid_cny"} {
			if value, ok := record.Source[key]; ok {
				source[key] = value
			}
		}
	}
	return MarketingUserRewardRecord{record.ID, record.Kind, record.SourceName, record.State, record.OccurredAt, record.CreditedAt, record.Rewards, source}, nil
}
func ReadMarketingUserRewards(ctx context.Context, userID int, id int64, f MarketingUserFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if f.Kind == "" {
		f.Kind = "activity"
	}
	if f.Kind != "activity" && f.Kind != "lottery" {
		return nil, model.ErrMarketingInput
	}
	if f.ActivityKind != "" && (!slices.Contains([]string{"registration", "recharge", "checkin", "invitation"}, f.ActivityKind) || f.Kind != "activity") {
		return nil, model.ErrMarketingInput
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingUser(tx, userID); err != nil {
			return err
		}
		items := []MarketingUserRewardRecord{}
		var total int64
		if f.Kind == "activity" {
			q := tx.Model(&model.MarketingActivityReward{}).Where("user_id = ?", userID)
			if id > 0 {
				q = q.Where("id = ?", id)
			}
			if f.ActivityKind != "" {
				q = q.Where("activity_id IN (?)", tx.Model(&model.MarketingActivity{}).Select("id").Where("kind = ?", f.ActivityKind))
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
				view, err := marketingAdminActivityRewardView(tx, row)
				if err != nil {
					return err
				}
				userView, err := marketingUserRewardView(tx, view)
				if err != nil {
					return err
				}
				items = append(items, userView)
			}
		} else {
			q := tx.Model(&model.MarketingLotteryDraw{}).Where("user_id = ?", userID)
			if id > 0 {
				q = q.Where("id = ?", id)
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
				view, err := marketingAdminLotteryRewardView(tx, row)
				if err != nil {
					return err
				}
				userView, err := marketingUserRewardView(tx, view)
				if err != nil {
					return err
				}
				items = append(items, userView)
			}
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

type MarketingUserGift struct {
	ID              int64  `json:"id"`
	SourceName      string `json:"source_name"`
	SourceNameKey   string `json:"source_name_key,omitempty"`
	SourceKind      string `json:"source_kind"`
	Currency        string `json:"currency"`
	GrantedAmount   string `json:"granted_amount"`
	ConsumedAmount  string `json:"consumed_amount"`
	RemainingAmount string `json:"remaining_amount"`
	ExpiredAmount   string `json:"expired_amount"`
	ClearedAmount   string `json:"cleared_amount"`
	CreditedAt      int64  `json:"credited_at"`
	ExpiresAt       int64  `json:"expires_at"`
	State           string `json:"state"`
	RewardID        int64  `json:"reward_id,omitempty"`
	RewardKind      string `json:"reward_kind,omitempty"`
}

func ReadMarketingUserGifts(ctx context.Context, userID int, id int64, f MarketingUserFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingUser(tx, userID); err != nil {
			return err
		}
		q := tx.Model(&model.MarketingGift{}).Where("user_id = ?", userID)
		if id > 0 {
			q = q.Where("id = ?", id)
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		var rows []model.MarketingGift
		if err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
			return err
		}
		items := []MarketingUserGift{}
		now := time.Now().Unix()
		for _, gift := range rows {
			item := MarketingUserGift{ID: gift.ID, SourceKind: "gift", SourceName: "", SourceNameKey: "Activity reward", CreditedAt: gift.GrantedAt, ExpiresAt: gift.ExpiresAt, State: "active"}
			remaining, expired := gift.RemainingQuota, gift.ExpiredQuota
			if gift.ClearedQuota > 0 {
				item.State = "cleared"
			} else if gift.ExpiresAt > 0 && gift.ExpiresAt <= now {
				item.State = "expired"
				expired += remaining - gift.ReservedQuota
				remaining = gift.ReservedQuota
			} else if remaining == 0 {
				item.State = "used"
			}
			snapshot, err := marketingUserGiftConversion(gift)
			if err != nil {
				return err
			}
			configuration := MarketingMoneyContext{Display: snapshot}
			for _, field := range []struct {
				quota  int64
				target *string
			}{{gift.GrantedQuota, &item.GrantedAmount}, {gift.ConsumedQuota, &item.ConsumedAmount}, {remaining, &item.RemainingAmount}, {expired, &item.ExpiredAmount}, {gift.ClearedQuota, &item.ClearedAmount}} {
				*field.target, item.Currency, err = marketingAdminDisplayAmount(field.quota, configuration)
				if err != nil {
					return err
				}
			}
			var activityReward model.MarketingActivityReward
			err = tx.Where("gift_id = ? AND user_id = ?", gift.ID, userID).First(&activityReward).Error
			if err == nil {
				var activity model.MarketingActivity
				if err := tx.First(&activity, activityReward.ActivityID).Error; err != nil {
					return err
				}
				item.SourceName = activity.Name
				item.SourceNameKey = ""
				item.RewardID = activityReward.ID
				item.RewardKind = "activity"
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			} else {
				var award model.MarketingLotteryAward
				err := tx.Where("gift_id = ? AND user_id = ?", gift.ID, userID).First(&award).Error
				if err == nil {
					var pool model.MarketingLotteryPool
					if err := tx.First(&pool, award.PoolID).Error; err != nil {
						return err
					}
					item.SourceName = pool.Name
					item.SourceNameKey = ""
					item.RewardID = award.DrawID
					item.RewardKind = "lottery"
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
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

// marketingUserGiftConversion uses the original grant evidence, including ratio
// grants whose Money.Amount is the payment face or a conversion unit rather than
// the awarded face. Current site settings must never rewrite historical money.
func marketingUserGiftConversion(gift model.MarketingGift) (model.MarketingMoneySnapshot, error) {
	var amount model.MarketingRewardAmount
	if err := common.UnmarshalJsonStr(gift.Conversion, &amount); err != nil {
		return model.MarketingMoneySnapshot{}, err
	}
	if amount.Calculation != "" {
		conversion, err := common.Marshal(amount.Money)
		if err != nil {
			return model.MarketingMoneySnapshot{}, err
		}
		payment := &model.MarketingPayment{ID: amount.PaymentID, PaidCents: amount.PaidCents, CreditedQuota: amount.CreditedQuota, Conversion: string(conversion)}
		verified, err := model.CalculateMarketingRewardAmount(amount.Calculation, amount.Ratio, amount.Money, payment)
		if err != nil || verified != amount || amount.Quota != gift.GrantedQuota {
			return model.MarketingMoneySnapshot{}, model.ErrMarketingInput
		}
		return amount.Money, nil
	}
	var snapshot model.MarketingMoneySnapshot
	if err := common.UnmarshalJsonStr(gift.Conversion, &snapshot); err != nil {
		return snapshot, err
	}
	quota, err := snapshot.Quota()
	if err != nil || quota != gift.GrantedQuota {
		return snapshot, model.ErrMarketingInput
	}
	return snapshot, nil
}
