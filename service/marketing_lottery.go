package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// MarketingLotteryResult is an internal delivery snapshot. HTTP integrations
// must project configured display amounts rather than expose storage quotas.
type MarketingLotteryResult struct {
	Draw   *model.MarketingLotteryDraw
	Awards []model.MarketingLotteryAward
}

// DrawMarketingLottery commits the result and all obligations before delivery.
// A failed delivery can resume the same result using the same request reference.
func DrawMarketingLottery(ctx context.Context, userID int, poolID int64, reference string) (*MarketingLotteryResult, error) {
	var draw *model.MarketingLotteryDraw
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		draw, err = model.DrawMarketingLotteryChance(tx, userID, poolID, reference, time.Now().Unix())
		return err
	})
	if err != nil {
		return nil, err
	}
	return FulfillMarketingLotteryDraw(ctx, userID, draw.ID)
}

func GetMarketingLotteryDraw(ctx context.Context, userID int, drawID int64) (*MarketingLotteryResult, error) {
	var result MarketingLotteryResult
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		result.Draw, result.Awards, err = model.GetMarketingLotteryDraw(tx, userID, drawID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// FulfillMarketingLotteryDraw never rerolls. Each component credits once, and
// only the final component completes the result. Return an authorized fresh
// snapshot with a delivery error so partial payment cannot appear complete.
func FulfillMarketingLotteryDraw(ctx context.Context, userID int, drawID int64) (*MarketingLotteryResult, error) {
	result, err := GetMarketingLotteryDraw(ctx, userID, drawID)
	if err != nil || result.Draw.State != "pending" {
		return result, err
	}
	var deliveryErr error
	for _, award := range result.Awards {
		if award.State != "pending" {
			continue
		}
		deliveryErr = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			_, err := model.FulfillMarketingLotteryAward(tx, userID, award.ID, time.Now().Unix())
			return err
		})
		if deliveryErr != nil {
			break
		}
	}
	result, err = GetMarketingLotteryDraw(ctx, userID, drawID)
	if err != nil {
		return nil, err
	}
	return result, deliveryErr
}
