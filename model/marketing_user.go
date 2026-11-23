package model

import (
	"fmt"

	"gorm.io/gorm"
)

// AuthorizeMarketingUser is checked inside the same transaction as private
// projections, not against a cached role or a caller-supplied owner ID.
func AuthorizeMarketingUser(tx *gorm.DB, userID int) error {
	return lockMarketingLotteryActor(tx, userID, false)
}

func MarketingUserDrawReference(userID int, operation string) (string, error) {
	if userID <= 0 || !marketingAdminOperationKey.MatchString(operation) {
		return "", ErrMarketingInput
	}
	return fmt.Sprintf("user:%d:%s", userID, operation), nil
}

func GetMarketingUserDrawByRequest(tx *gorm.DB, userID int, operation string) (*MarketingLotteryDraw, error) {
	if err := AuthorizeMarketingUser(tx, userID); err != nil {
		return nil, err
	}
	reference, err := MarketingUserDrawReference(userID, operation)
	if err != nil {
		return nil, err
	}
	key, err := marketingSourceKey("lottery_draw", reference)
	if err != nil {
		return nil, err
	}
	var draw MarketingLotteryDraw
	err = lockForUpdate(tx).Where("request_key = ? AND user_id = ?", key, userID).First(&draw).Error
	return &draw, err
}

func PublicMarketingActivities(tx *gorm.DB, now int64) *gorm.DB {
	return tx.Model(&MarketingActivity{}).Where("enabled_at > ? AND published = ? AND (ends_at = ? OR ends_at > ?)", 0, true, 0, now-72*3600)
}
