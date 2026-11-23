package service

import (
	"fmt"
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ParseMarketingRealtimeUsage checks presence before decoding the public DTO.
// Missing facts, including a missing explicit zero, cannot become a free call.
// Only token counts are retained; audio, transcripts and instructions are not.
func ParseMarketingRealtimeUsage(raw []byte) (*dto.RealtimeUsage, error) {
	var fields struct {
		Total  *int `json:"total_tokens"`
		Input  *int `json:"input_tokens"`
		Output *int `json:"output_tokens"`
	}
	if err := common.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields.Total == nil || fields.Input == nil || fields.Output == nil {
		return nil, fmt.Errorf("realtime usage is incomplete")
	}
	var usage dto.RealtimeUsage
	if err := common.Unmarshal(raw, &usage); err != nil {
		return nil, err
	}
	if err := validateMarketingRealtimeUsage(usage); err != nil {
		return nil, err
	}
	if usage.InputTokenDetails.TextTokens+usage.InputTokenDetails.AudioTokens+usage.InputTokenDetails.ImageTokens != usage.InputTokens ||
		usage.OutputTokenDetails.TextTokens+usage.OutputTokenDetails.AudioTokens+usage.OutputTokenDetails.ImageTokens != usage.OutputTokens {
		return nil, fmt.Errorf("realtime usage modality breakdown is incomplete")
	}
	return &usage, nil
}

func validateMarketingRealtimeUsage(usage dto.RealtimeUsage) error {
	i, o := usage.InputTokenDetails, usage.OutputTokenDetails
	for _, n := range []int{usage.TotalTokens, usage.InputTokens, usage.OutputTokens, i.TextTokens, i.AudioTokens, i.ImageTokens, i.CachedTokens, o.TextTokens, o.AudioTokens, o.ImageTokens} {
		if n < 0 || n > math.MaxInt32 {
			return model.ErrMarketingInput
		}
	}
	if usage.InputTokens > math.MaxInt32-usage.OutputTokens || usage.TotalTokens != usage.InputTokens+usage.OutputTokens ||
		i.AudioTokens > usage.InputTokens || i.TextTokens > usage.InputTokens-i.AudioTokens || i.ImageTokens > usage.InputTokens-i.AudioTokens-i.TextTokens ||
		o.AudioTokens > usage.OutputTokens || o.TextTokens > usage.OutputTokens-o.AudioTokens || o.ImageTokens > usage.OutputTokens-o.AudioTokens-o.TextTokens || i.CachedTokens > usage.InputTokens {
		return model.ErrMarketingInput
	}
	if details := i.CachedTokensDetails; details != nil {
		remaining := i.CachedTokens
		for _, n := range []*int{details.TextTokens, details.AudioTokens, details.ImageTokens} {
			if n != nil {
				if *n < 0 || *n > remaining {
					return model.ErrMarketingInput
				}
				remaining -= *n
			}
		}
	}
	return nil
}

// SumMarketingRealtimeUsage accumulates actual facts, independently of costs.
// A cheaper pricing tier never erases tokens or changes the original facts.
func SumMarketingRealtimeUsage(usages []dto.RealtimeUsage) (dto.RealtimeUsage, error) {
	var total dto.RealtimeUsage
	for _, usage := range usages {
		if err := validateMarketingRealtimeUsage(usage); err != nil {
			return total, err
		}
		if total.TotalTokens > math.MaxInt32-usage.TotalTokens {
			return total, model.ErrMarketingInput
		}
		total.TotalTokens += usage.TotalTokens
		total.InputTokens += usage.InputTokens
		total.OutputTokens += usage.OutputTokens
		total.InputTokenDetails.TextTokens += usage.InputTokenDetails.TextTokens
		total.InputTokenDetails.AudioTokens += usage.InputTokenDetails.AudioTokens
		total.InputTokenDetails.ImageTokens += usage.InputTokenDetails.ImageTokens
		total.InputTokenDetails.CachedTokens += usage.InputTokenDetails.CachedTokens
		total.OutputTokenDetails.TextTokens += usage.OutputTokenDetails.TextTokens
		total.OutputTokenDetails.AudioTokens += usage.OutputTokenDetails.AudioTokens
		total.OutputTokenDetails.ImageTokens += usage.OutputTokenDetails.ImageTokens
		if details := usage.InputTokenDetails.CachedTokensDetails; details != nil {
			if total.InputTokenDetails.CachedTokensDetails == nil {
				total.InputTokenDetails.CachedTokensDetails = &dto.CachedTokenDetails{}
			}
			dest := total.InputTokenDetails.CachedTokensDetails
			for _, pair := range []struct {
				target **int
				value  *int
			}{{&dest.TextTokens, details.TextTokens}, {&dest.AudioTokens, details.AudioTokens}, {&dest.ImageTokens, details.ImageTokens}} {
				if pair.value == nil {
					continue
				}
				if *pair.target == nil {
					*pair.target = new(int)
				}
				**pair.target += *pair.value
			}
		}
	}
	return total, nil
}

// PriceMarketingRealtimeUsage replays the connection's frozen pricing contract.
// All rounds are priced together so rounding and cumulative tiers are preserved.
func PriceMarketingRealtimeUsage(pricing string, usage dto.RealtimeUsage, at time.Time) (int64, error) {
	if err := validateMarketingRealtimeUsage(usage); err != nil {
		return 0, err
	}
	var frozen struct {
		Price        types.PriceData              `json:"price"`
		Tiered       *billingexpr.BillingSnapshot `json:"tiered"`
		QuotaPerUnit float64                      `json:"quota_per_unit"`
	}
	if err := common.UnmarshalJsonStr(pricing, &frozen); err != nil {
		return 0, err
	}
	if frozen.Tiered != nil {
		canonical := &dto.Usage{PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens, PromptTokensDetails: usage.InputTokenDetails.Clone(), CompletionTokenDetails: usage.OutputTokenDetails}
		params := BuildTieredTokenParams(canonical, false, billingexpr.UsedVarsByHash(frozen.Tiered.ExprString, frozen.Tiered.ExprHash))
		result, err := ReplayMarketingTieredPricing(pricing, params, &at, nil, nil)
		return int64(result.ActualQuotaAfterGroup), err
	}
	p := frozen.Price
	ratios := []float64{p.GroupRatioInfo.GroupRatio}
	if p.UsePrice {
		ratios = append(ratios, p.ModelPrice, frozen.QuotaPerUnit)
	} else {
		ratios = append(ratios, p.ModelRatio, p.CompletionRatio, p.AudioRatio, p.AudioCompletionRatio)
	}
	for _, ratio := range ratios {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			return 0, model.ErrMarketingInput
		}
	}
	quota := decimal.Zero
	if p.UsePrice {
		quota = decimal.NewFromFloat(p.ModelPrice).Mul(decimal.NewFromFloat(frozen.QuotaPerUnit))
	} else {
		if usage.InputTokenDetails.TextTokens+usage.InputTokenDetails.AudioTokens != usage.InputTokens || usage.OutputTokenDetails.TextTokens+usage.OutputTokenDetails.AudioTokens != usage.OutputTokens {
			return 0, fmt.Errorf("realtime modality usage is incomplete for ratio pricing")
		}
		audioRatio := decimal.NewFromFloat(p.AudioRatio)
		quota = decimal.NewFromInt(int64(usage.InputTokenDetails.TextTokens)).
			Add(decimal.NewFromInt(int64(usage.OutputTokenDetails.TextTokens)).Mul(decimal.NewFromFloat(p.CompletionRatio))).
			Add(decimal.NewFromInt(int64(usage.InputTokenDetails.AudioTokens)).Mul(audioRatio)).
			Add(decimal.NewFromInt(int64(usage.OutputTokenDetails.AudioTokens)).Mul(audioRatio).Mul(decimal.NewFromFloat(p.AudioCompletionRatio))).
			Mul(decimal.NewFromFloat(p.ModelRatio))
	}
	quota = quota.Mul(decimal.NewFromFloat(p.GroupRatioInfo.GroupRatio))
	if !p.UsePrice && usage.TotalTokens > 0 && p.ModelRatio > 0 && p.GroupRatioInfo.GroupRatio > 0 && quota.LessThanOrEqual(decimal.Zero) {
		quota = decimal.NewFromInt(1)
	}
	value, clamp := common.QuotaFromDecimalChecked(quota)
	if clamp != nil {
		return 0, model.ErrMarketingInput
	}
	return int64(value), nil
}

// ReconcileMarketingRealtimeResponse accepts a trusted provider terminal for an
// already closed connection. This is an internal reconciliation entry, not an
// HTTP handler: callers must obtain authoritative evidence and authorize the
// account operation before invoking it. It never dispatches upstream, accepts a
// caller-selected price, or creates a new funding request. The original log and
// frozen pricing come from the business database, not current site settings.
func ReconcileMarketingRealtimeResponse(userID int, fundingID int64, raw []byte, at time.Time) (*model.MarketingFunding, error) {
	if userID <= 0 || fundingID <= 0 || at.Unix() <= 0 || at.Unix() > common.GetTimestamp() {
		return nil, model.ErrMarketingInput
	}
	var event marketingRealtimeEvent
	if err := common.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	if event.Type != "response.done" || event.Response == nil || event.Response.ID == "" {
		return nil, model.ErrMarketingInput
	}
	var funding model.MarketingFunding
	var record model.Log
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		snapshot, err := model.GetMarketingRealtimeSnapshot(tx, userID, fundingID)
		if err != nil {
			return err
		}
		if !snapshot.Funding.RealtimeClosed {
			return model.ErrMarketingConflict
		}
		funding = *snapshot.Funding
		var projection model.MarketingUsageProjection
		if err = tx.Where("funding_id = ?", fundingID).Order("version DESC").First(&projection).Error; err != nil {
			return err
		}
		if err = common.UnmarshalJsonStr(projection.Payload, &record); err != nil {
			return err
		}
		if record.UserId != userID || record.RequestId == "" || record.UsageEventID != funding.RequestKey {
			return model.ErrMarketingConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	info := &relaycommon.RelayInfo{
		UserId: userID, TokenId: funding.TokenID, RequestId: record.RequestId,
		StartTime: time.Unix(funding.QualifiedAt, 0),
	}
	session := &MarketingBillingSession{info: info, funding: funding}
	info.Billing = session
	coordinator, err := NewMarketingRealtimeCoordinator(nil, info)
	if err != nil {
		return nil, err
	}
	coordinator.replayLog = &record
	_, err = coordinator.ProcessServerEvent(raw, at)
	return &session.funding, err
}
