package model

import "gorm.io/gorm"

// GetMarketingLotteryDraw returns a coherent owner-scoped result. The user lock
// serializes delivery and clearing; current reads also work in existing MySQL
// repeatable-read transactions without exposing an earlier delivery snapshot.
func GetMarketingLotteryDraw(tx *gorm.DB, userID int, drawID int64) (*MarketingLotteryDraw, []MarketingLotteryAward, error) {
	if drawID <= 0 {
		return nil, nil, ErrMarketingInput
	}
	if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
		return nil, nil, err
	}
	var draw MarketingLotteryDraw
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", drawID, userID).First(&draw).Error; err != nil {
		return nil, nil, err
	}
	var awards []MarketingLotteryAward
	if err := lockForUpdate(tx).Where("draw_id = ? AND user_id = ?", drawID, userID).Order("position, id").Find(&awards).Error; err != nil {
		return nil, nil, err
	}
	return &draw, awards, nil
}
