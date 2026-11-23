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

type MarketingUserChance struct {
	ID         int64  `json:"id"`
	Remaining  int64  `json:"remaining"`
	ExpiresAt  int64  `json:"expires_at"`
	State      string `json:"state"`
	SourceName string `json:"source_name"`
}
type MarketingUserPrize struct {
	ID        int64                `json:"id"`
	Name      string               `json:"name"`
	Available bool                 `json:"available"`
	State     string               `json:"state"`
	NoPrize   bool                 `json:"no_prize"`
	Gifts     []MarketingAdminGift `json:"gifts"`
}
type MarketingUserPool struct {
	MarketingUserPoolSummary
	Style            string                `json:"style"`
	ServerNow        int64                 `json:"server_now"`
	AvailableChances int64                 `json:"available_chances"`
	CanDraw          bool                  `json:"can_draw"`
	Chances          []MarketingUserChance `json:"chances"`
	Prizes           []MarketingUserPrize  `json:"prizes"`
}
type MarketingUserDraw struct {
	ID        int64                  `json:"id"`
	PoolID    int64                  `json:"pool_id"`
	PoolName  string                 `json:"pool_name"`
	PrizeID   int64                  `json:"prize_id"`
	PrizeName string                 `json:"prize_name"`
	NoPrize   bool                   `json:"no_prize"`
	State     string                 `json:"state"`
	DrawnAt   int64                  `json:"drawn_at"`
	Rewards   []MarketingAdminReward `json:"rewards"`
}

func marketingUserPoolView(tx *gorm.DB, userID int, pool model.MarketingLotteryPool, now int64) (MarketingUserPool, error) {
	result := MarketingUserPool{MarketingUserPoolSummary: marketingUserPoolSummary(pool, now), Style: pool.Style, ServerNow: now, Chances: []MarketingUserChance{}, Prizes: []MarketingUserPrize{}}
	configuration, err := CurrentMarketingMoneyContext()
	if err != nil {
		return result, err
	}
	var chances []model.MarketingLotteryChance
	if err := tx.Where("user_id = ? AND pool_id = ?", userID, pool.ID).Order("expires_at,id").Find(&chances).Error; err != nil {
		return result, err
	}
	for _, c := range chances {
		state := c.State
		remaining := c.Remaining
		if state == "ready" {
			if remaining == 0 {
				state = "used"
			} else if now >= c.ExpiresAt {
				state = "expired"
				remaining = 0
			} else {
				result.AvailableChances += remaining
			}
		}
		if state == "cleared" {
			remaining = 0
		}
		sourceName := pool.Name
		var source struct{ Name string }
		err := tx.Model(&model.MarketingActivityReward{}).Select("marketing_v2_activities.name").Joins("JOIN marketing_v2_activities ON marketing_v2_activities.id = marketing_v2_activity_rewards.activity_id").Where("marketing_v2_activity_rewards.issue_id = ? AND marketing_v2_activity_rewards.user_id = ?", c.IssueID, userID).Take(&source).Error
		if err == nil {
			sourceName = source.Name
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return result, err
		}
		result.Chances = append(result.Chances, MarketingUserChance{c.ID, remaining, c.ExpiresAt, state, sourceName})
	}
	var prizes []model.MarketingLotteryPrize
	if err := tx.Where("pool_id = ?", pool.ID).Order("id").Find(&prizes).Error; err != nil {
		return result, err
	}
	hasPrize := false
	for _, prize := range prizes {
		var gifts []model.MarketingLotteryGiftSpec
		if err := common.UnmarshalJsonStr(prize.Gifts, &gifts); err != nil {
			return result, err
		}
		view := MarketingUserPrize{ID: prize.ID, Name: prize.Name, Available: prize.Stock != 0 && prize.Weight > 0, State: "available", NoPrize: len(gifts) == 0, Gifts: []MarketingAdminGift{}}
		if prize.Stock == 0 {
			view.State = "sold_out"
		} else if prize.Weight <= 0 {
			view.State = "unavailable"
		}
		hasPrize = hasPrize || view.Available
		for _, gift := range gifts {
			quota, err := gift.Money.Quota()
			if err != nil {
				return result, err
			}
			amount, currency, err := marketingAdminDisplayAmount(quota, configuration)
			if err != nil {
				return result, err
			}
			view.Gifts = append(view.Gifts, MarketingAdminGift{Amount: amount, Currency: currency, ValidSeconds: gift.ValidSeconds})
		}
		result.Prizes = append(result.Prizes, view)
	}
	result.CanDraw = result.State == "active" && result.AvailableChances > 0 && hasPrize
	return result, nil
}
func ReadMarketingUserPools(ctx context.Context, userID int, id int64, f MarketingUserFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingUser(tx, userID); err != nil {
			return err
		}
		now := time.Now().Unix()
		ids, err := marketingUserVisiblePoolIDs(tx, userID, now)
		if err != nil {
			return err
		}
		if id > 0 && !slices.Contains(ids, id) {
			return gorm.ErrRecordNotFound
		}
		q := tx.Model(&model.MarketingLotteryPool{}).Where("id IN ?", ids)
		if id > 0 {
			q = q.Where("id = ?", id)
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		var rows []model.MarketingLotteryPool
		if err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
			return err
		}
		items := []MarketingUserPool{}
		for _, row := range rows {
			view, err := marketingUserPoolView(tx, userID, row, now)
			if err != nil {
				return err
			}
			items = append(items, view)
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
func marketingUserDrawView(tx *gorm.DB, userID int, drawID int64) (MarketingUserDraw, error) {
	row, _, err := model.GetMarketingLotteryDraw(tx, userID, drawID)
	if err != nil {
		return MarketingUserDraw{}, err
	}
	record, err := marketingAdminLotteryRewardView(tx, *row)
	if err != nil {
		return MarketingUserDraw{}, err
	}
	userRecord, err := marketingUserRewardView(tx, record)
	if err != nil {
		return MarketingUserDraw{}, err
	}
	var prize model.MarketingLotteryPrize
	if err := common.UnmarshalJsonStr(row.PrizeSnapshot, &prize); err != nil {
		return MarketingUserDraw{}, err
	}
	if row.State == "complete" {
		row.State = "completed"
	}
	return MarketingUserDraw{row.ID, row.PoolID, record.SourceName, row.PrizeID, prize.Name, len(record.Rewards) == 0, row.State, row.DrawnAt, userRecord.Rewards}, nil
}
func ReadMarketingUserDraw(ctx context.Context, userID int, id int64, operation string) (any, error) {
	var result MarketingUserDraw
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingUser(tx, userID); err != nil {
			return err
		}
		if operation != "" {
			draw, err := model.GetMarketingUserDrawByRequest(tx, userID, operation)
			if err != nil {
				return err
			}
			id = draw.ID
		}
		var err error
		result, err = marketingUserDrawView(tx, userID, id)
		return err
	})
	return result, err
}
func SubmitMarketingUserDraw(ctx context.Context, userID int, poolID int64, operation string) (any, error) {
	reference, err := model.MarketingUserDrawReference(userID, operation)
	if err != nil {
		return nil, err
	}
	result, err := DrawMarketingLottery(ctx, userID, poolID, reference)
	// Once the draw committed, a component-delivery failure is pending, never
	// an unsuccessful draw that invites a new operation key and another chance.
	if result == nil {
		return nil, err
	}
	return ReadMarketingUserDraw(ctx, userID, result.Draw.ID, "")
}
