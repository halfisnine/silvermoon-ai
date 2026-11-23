package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// MarketingActivityProcessingResult describes internal work, not user claims or
// a promise that every reward was delivered. Batches bound one worker invocation;
// they never cap a user's qualifying rewards.
type MarketingActivityProcessingResult struct {
	DecisionBatches int
	DeliveredItems  int
	Pending         bool
}

// ProcessMarketingActivityFacts handles oldest facts first and then delivers
// durable gift obligations independently. A failed item never prevents attempts
// to deliver other items. Interrupted work resumes through this same entry point.
func ProcessMarketingActivityFacts(ctx context.Context, sourceUserID int) (*MarketingActivityProcessingResult, error) {
	result := &MarketingActivityProcessingResult{}
	var failures []error
	for range 8 {
		var fact *model.MarketingActivityFact
		err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			var err error
			fact, err = model.ProcessNextMarketingActivityFact(tx, sourceUserID, time.Now().Unix())
			return err
		})
		if err != nil {
			failures = append(failures, err)
			break
		}
		if fact == nil {
			break
		}
		result.DecisionBatches++
	}
	attempted := make(map[int64]bool)
	for range 8 {
		var work *model.MarketingActivityDeliveryWork
		err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			var err error
			work, err = model.NextMarketingActivityDeliveryWork(tx, sourceUserID, 128)
			return err
		})
		if err != nil {
			return nil, errors.Join(append(failures, err)...)
		}
		if len(work.Rewards) == 0 {
			break
		}
		newAttempts := 0
		for _, item := range work.Rewards {
			if attempted[item.ID] {
				continue
			}
			attempted[item.ID] = true
			newAttempts++
			err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
				_, err := model.FulfillMarketingActivityReward(tx, item.UserID, item.ID, time.Now().Unix())
				return err
			})
			if err != nil {
				failures = append(failures, err)
				continue
			}
			result.DeliveredItems++
		}
		if newAttempts == 0 {
			break
		}
	}
	// Read again from the beginning: an earlier failed item must keep Pending
	// true even if later independent gifts were delivered successfully.
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		work, err := model.GetMarketingActivityDeliveryWork(tx, sourceUserID, 0, 1)
		if err == nil {
			result.Pending = work.Undecided || len(work.Rewards) != 0
		}
		return err
	})
	if err != nil {
		return nil, errors.Join(append(failures, err)...)
	}
	return result, errors.Join(failures...)
}
