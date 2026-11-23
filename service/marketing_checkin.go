package service

import (
	"context"

	"github.com/QuantumNous/new-api/model"
)

// MarketingCheckinResult separates the committed calendar operation from
// delivery. A delivery failure must never ask the user to earn the day again.
type MarketingCheckinResult struct {
	Checkin *model.MarketingCheckinResult
	Pending bool
}

func SubmitMarketingCheckin(ctx context.Context, userID int, targetDay string) (*MarketingCheckinResult, error) {
	checkin, err := model.SubmitMarketingCheckin(ctx, userID, targetDay)
	if err != nil {
		return nil, err
	}
	result := &MarketingCheckinResult{Checkin: checkin, Pending: true}
	processed, err := ProcessMarketingActivityFacts(ctx, userID)
	if err != nil {
		return result, err
	}
	result.Pending = processed.Pending
	return result, nil
}
