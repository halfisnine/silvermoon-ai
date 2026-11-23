package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// MarketingMaintenanceUsageRecord recovers only the original request identity
// and frozen safe metadata. A crash before the first projection does not make
// known final fees impossible to reconcile, or authorize a fabricated request.
func MarketingMaintenanceUsageRecord(tx *gorm.DB, userID int, fundingID int64, originalRequestID string) (Log, error) {
	_, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return Log{}, err
	}
	var projection MarketingUsageProjection
	err = lockForUpdate(tx).Where("funding_id = ?", fundingID).Order("version DESC").First(&projection).Error
	if err == nil {
		var record Log
		if err := common.UnmarshalJsonStr(projection.Payload, &record); err != nil {
			return Log{}, err
		}
		if record.UserId != userID || record.RequestId == "" || record.UsageEventID != funding.RequestKey {
			return Log{}, ErrMarketingConflict
		}
		if originalRequestID != "" && originalRequestID != record.RequestId {
			return Log{}, ErrMarketingConflict
		}
		return record, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Log{}, err
	}
	key, err := marketingSourceKey("request", originalRequestID)
	if err != nil || key != funding.RequestKey {
		return Log{}, ErrMarketingConflict
	}
	var pricing struct {
		Model     string `json:"model"`
		ChannelID int    `json:"channel_id"`
		Group     string `json:"group"`
	}
	if err := common.UnmarshalJsonStr(funding.PricingEvidence, &pricing); err != nil {
		return Log{}, err
	}
	if pricing.Model == "" || pricing.ChannelID < 0 {
		return Log{}, ErrMarketingReview
	}
	// No token counts are invented from a fee. Future display can distinguish
	// unavailable quantities from a genuine zero through this retained marker.
	return Log{UserId: userID, RequestId: originalRequestID, UsageEventID: funding.RequestKey, CreatedAt: funding.QualifiedAt, Type: LogTypeConsume, ModelName: pricing.Model, ChannelId: pricing.ChannelID, Group: pricing.Group, TokenId: funding.TokenID, Other: `{"usage_quantity_unknown":true,"admin_info":{"recovered_before_log_projection":true}}`}, nil
}
