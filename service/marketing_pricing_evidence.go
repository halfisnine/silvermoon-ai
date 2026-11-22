package service

import (
	"fmt"
	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"maps"
	"math"
	"strings"
	"time"
)

func marketingPricingEvidence(info *relaycommon.RelayInfo) (string, error) {
	// Explicit safe fields: never serialize RelayInfo, request headers, prompts,
	// token keys, URLs, or provider credentials into the financial evidence.
	channelID := 0
	if info.ChannelMeta != nil {
		channelID = info.ChannelId
	}
	requestPricing := map[string]any{}
	if request := info.BillingRequestInput; request != nil {
		// Only billing scalars are retained. A custom expression that reads
		// another field is captured independently as derived expression facts below;
		// these display scalars are not a replacement for that replay evidence.
		parameters := map[string]any{}
		for _, path := range []string{"n", "size", "quality", "service_tier", "duration", "seconds", "resolution", "width", "height", "parameters.n", "parameters.size", "parameters.quality", "parameters.duration", "parameters.resolution"} {
			value := ojson.GetBytes(request.Body, path)
			if !value.Exists() {
				continue
			}
			switch scalar := value.Value().(type) {
			case float64:
				if !math.IsNaN(scalar) && !math.IsInf(scalar, 0) {
					parameters[path] = scalar
				}
			case string:
				if len(scalar) <= 128 {
					parameters[path] = scalar
				}
			case bool, nil:
				parameters[path] = scalar
			}
		}
		headers := map[string]string{}
		for name, value := range request.Headers {
			key := strings.ToLower(strings.TrimSpace(name))
			if (key == "anthropic-beta" || key == "openai-beta") && len(value) <= 256 {
				headers[key] = strings.TrimSpace(value)
			}
		}
		requestPricing["parameters"] = parameters
		requestPricing["headers"] = headers
		requestPricing["complete_request"] = false
	}
	if snap := info.TieredBillingSnapshot; snap != nil && snap.ExprString != "" {
		input := billingexpr.RequestInput{}
		if info.BillingRequestInput != nil && !snap.TaskUsageBilling {
			input = *info.BillingRequestInput
		}
		frozen, err := billingexpr.CaptureRequestPricing(snap.ExprString, input)
		if err != nil {
			return "", err
		}
		requestPricing["expression"] = frozen
	}
	data, err := common.Marshal(map[string]any{
		"model": info.GetBillingModelName(), "channel_id": channelID,
		"group": info.UsingGroup, "price": info.PriceData,
		"other_ratios":   info.PriceData.OtherRatios(),
		"quota_per_unit": common.QuotaPerUnit, "tiered": info.TieredBillingSnapshot,
		"image_count": info.BillingImageCount, "request_pricing": requestPricing, "evaluation_time": info.BillingEvaluationTime,
	})
	return string(data), err
}

// ReplayMarketingTieredPricing accepts authoritative normalized usage and,
// for a previously unknown result, an explicit reconciliation evaluation time.
// A known settlement's persisted time always wins over the recovery clock.
func ReplayMarketingTieredPricing(encoded string, params billingexpr.TokenParams, evaluationTime *time.Time, usage map[string]any, actualImageCount *int) (billingexpr.TieredResult, error) {
	var evidence struct {
		Tiered         *billingexpr.BillingSnapshot `json:"tiered"`
		ImageCount     *int                         `json:"image_count"`
		EvaluationTime *time.Time                   `json:"evaluation_time"`
		Request        struct {
			Expression *billingexpr.RequestPricingEvidence `json:"expression"`
		} `json:"request_pricing"`
	}
	if err := common.UnmarshalJsonStr(encoded, &evidence); err != nil {
		return billingexpr.TieredResult{}, err
	}
	if evidence.Tiered == nil || evidence.Request.Expression == nil {
		return billingexpr.TieredResult{}, fmt.Errorf("original expression pricing evidence is unavailable")
	}
	if evidence.EvaluationTime != nil {
		evaluationTime = evidence.EvaluationTime
	}
	if evidence.Tiered.TaskUsageBilling && evidence.Tiered.SettlementTime != nil {
		evaluationTime = evidence.Tiered.SettlementTime
	}
	if evidence.ImageCount == nil {
		evidence.ImageCount = evidence.Tiered.EstimatedImageCount
	}
	if actualImageCount != nil {
		evidence.ImageCount = actualImageCount
	}
	if evidence.Tiered.TaskUsageBilling {
		merged := maps.Clone(evidence.Tiered.UsageFacts)
		if merged == nil {
			merged = map[string]any{}
		}
		maps.Copy(merged, usage)
		usage = merged
	}
	return billingexpr.ComputeTieredQuotaWithRequest(evidence.Tiered, params, billingexpr.RequestInput{Evidence: evidence.Request.Expression, EvaluationTime: evaluationTime, ImageCount: evidence.ImageCount, Usage: usage})
}
