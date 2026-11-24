package service

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/model"
)

// RecoverMarketingRewards reuses the same decision and delivery transactions
// as foreground requests. The scheduler lease reduces duplicate work only;
// persisted decisions, stock reservations and grant keys provide correctness.
// Keep the first error while visiting later sources/pages. Failed obligations
// remain pending and are revisited on the next pass, without a user retry.
func RecoverMarketingRewards(ctx context.Context) error {
	var firstErr error
	afterUserID := 0
	for {
		users, err := model.PendingMarketingRewardSources(ctx, afterUserID, 100)
		if err != nil {
			return err
		}
		for _, userID := range users {
			if err := ctx.Err(); err != nil {
				return err
			}
			afterUserID = userID
			if _, err := ProcessMarketingActivityFacts(ctx, userID); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("marketing reward recovery for source %d: %w", userID, err)
			}
		}
		if len(users) < 100 {
			break
		}
	}
	var afterID int64
	for {
		draws, err := model.PendingMarketingLotteryDraws(ctx, afterID, 100)
		if err != nil {
			return err
		}
		for _, draw := range draws {
			if err := ctx.Err(); err != nil {
				return err
			}
			afterID = draw.ID
			if _, err := FulfillMarketingLotteryDraw(ctx, draw.UserID, draw.ID); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("marketing lottery recovery for draw %d: %w", draw.ID, err)
			}
		}
		if len(draws) < 100 {
			return firstErr
		}
	}
}
