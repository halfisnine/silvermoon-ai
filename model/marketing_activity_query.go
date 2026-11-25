package model

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// PendingMarketingRewardSources returns source users, not beneficiaries: one
// registration/payment may also owe a gift to its inviter. A failed earlier
// source must not prevent a later keyset page from making progress.
func PendingMarketingRewardSources(ctx context.Context, afterUserID, limit int) ([]int, error) {
	if afterUserID < 0 || limit < 1 || limit > 100 {
		return nil, ErrMarketingInput
	}
	var users []int
	err := DB.WithContext(ctx).Model(&MarketingActivityFact{}).Distinct("user_id").
		Where("user_id > ? AND delivery_state = ?", afterUserID, "pending").
		Order("user_id").Limit(limit).Pluck("user_id", &users).Error
	return users, err
}

// PendingMarketingLotteryDraws reads only committed, unfinished results. The
// worker fulfills their original awards; it must never consume another chance.
func PendingMarketingLotteryDraws(ctx context.Context, afterID int64, limit int) ([]MarketingLotteryDraw, error) {
	if afterID < 0 || limit < 1 || limit > 100 {
		return nil, ErrMarketingInput
	}
	var draws []MarketingLotteryDraw
	err := DB.WithContext(ctx).Select("id", "user_id").Where("id > ? AND state = ?", afterID, "pending").
		Order("id").Limit(limit).Find(&draws).Error
	return draws, err
}

// MarketingActivityDeliveryCursor rotates technical retry work across calls.
// Moving it does not complete or discard an obligation; pending rows remain the
// authority and will be visited again after the cursor reaches the end.
type MarketingActivityDeliveryCursor struct {
	UserID  int   `gorm:"primaryKey;autoIncrement:false"`
	AfterID int64 `gorm:"not null"`
}

func (MarketingActivityDeliveryCursor) TableName() string {
	return "marketing_v2_activity_delivery_cursors"
}

// MarketingActivityDeliveryWork is an internal work snapshot. It deliberately
// excludes rule payloads, probability seeds and monetary storage fields.
type MarketingActivityDeliveryWork struct {
	Rewards   []MarketingActivityReward
	Undecided bool
}

// GetMarketingActivityDeliveryWork is for trusted event processing, not an HTTP
// user-rewards endpoint. An already recorded payment remains a fact when its
// payer is disabled. Actual delivery rechecks the beneficiary's current access.
func GetMarketingActivityDeliveryWork(tx *gorm.DB, sourceUserID int, afterID int64, limit int) (*MarketingActivityDeliveryWork, error) {
	if tx == nil || tx.Statement == nil {
		return nil, ErrMarketingTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrMarketingTransaction
	}
	if sourceUserID <= 0 || afterID < 0 || limit < 1 || limit > 256 {
		return nil, ErrMarketingInput
	}
	var user User
	if err := lockForUpdate(tx).First(&user, sourceUserID).Error; err != nil {
		return nil, err
	}
	var fact MarketingActivityFact
	err := lockForUpdate(tx).Where("user_id = ? AND state <> ?", sourceUserID, "decided").Order("id").First(&fact).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	work := &MarketingActivityDeliveryWork{Undecided: err == nil}
	// Source-user serialization matches decision/delivery transactions. A joined
	// current read avoids a stale MySQL subquery hiding committed reward work.
	err = lockForUpdate(tx).Model(&MarketingActivityReward{}).
		Select("marketing_v2_activity_rewards.id, marketing_v2_activity_rewards.user_id, marketing_v2_activity_rewards.fact_id, marketing_v2_activity_rewards.state").
		Joins("JOIN marketing_v2_activity_facts ON marketing_v2_activity_facts.id = marketing_v2_activity_rewards.fact_id").
		Where("marketing_v2_activity_facts.user_id = ? AND marketing_v2_activity_rewards.state = ? AND marketing_v2_activity_rewards.id > ?", sourceUserID, "pending", afterID).
		Order("marketing_v2_activity_rewards.id").Limit(limit).Find(&work.Rewards).Error
	if err != nil {
		return nil, err
	}
	return work, nil
}

// NextMarketingActivityDeliveryWork advances a shared cursor while holding the
// source-user lock. A crashed worker loses no work: no reward state is changed,
// and another worker eventually wraps to the still-pending obligations.
func NextMarketingActivityDeliveryWork(tx *gorm.DB, sourceUserID int, limit int) (*MarketingActivityDeliveryWork, error) {
	if err := lockMarketingActivityUsers(tx, []int{sourceUserID}); err != nil {
		return nil, err
	}
	var cursor MarketingActivityDeliveryCursor
	err := lockForUpdate(tx).First(&cursor, "user_id = ?", sourceUserID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		cursor.UserID = sourceUserID
		if err := tx.Create(&cursor).Error; err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	work, err := GetMarketingActivityDeliveryWork(tx, sourceUserID, cursor.AfterID, limit)
	if err != nil {
		return nil, err
	}
	if len(work.Rewards) == 0 && cursor.AfterID != 0 {
		work, err = GetMarketingActivityDeliveryWork(tx, sourceUserID, 0, limit)
		if err != nil {
			return nil, err
		}
	}
	if len(work.Rewards) > 0 {
		cursor.AfterID = work.Rewards[len(work.Rewards)-1].ID
		if err := tx.Save(&cursor).Error; err != nil {
			return nil, err
		}
	} else {
		// Clearing a beneficiary removes obligations without taking locks on
		// other source users. After wrapping, this current read proves that no
		// beneficiary has work left for this source. The source lock prevents
		// new decisions/deliveries while we converge completed fact markers.
		if err := tx.Model(&MarketingActivityFact{}).
			Where("user_id = ? AND state = ? AND delivery_state = ?", sourceUserID, "decided", "pending").
			Update("delivery_state", "complete").Error; err != nil {
			return nil, err
		}
	}
	return work, nil
}
