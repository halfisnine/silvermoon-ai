package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarketingPricingEvidenceReplaysRequestDependencies(t *testing.T) {
	for _, source := range []string{"param", "header"} {
		t.Run(source, func(t *testing.T) {
			var saved []string
			for _, multiplier := range []int{2, 8} {
				expression := `tier("base", c <= 100 ? c : c * param("billing_multiplier"))`
				input := billingexpr.RequestInput{Body: []byte(fmt.Sprintf(`{"billing_multiplier":%d,"prompt":"private-prompt","image":"private-media"}`, multiplier)), Headers: map[string]string{"X-Custom-Price": fmt.Sprint(multiplier), "Authorization": "Bearer private-key", "Cookie": "private-cookie"}}
				if source == "header" {
					expression = `tier("base", c <= 100 ? c : c * float(header("X-Custom-Price")))`
				}
				info := &relaycommon.RelayInfo{OriginModelName: "test", BillingRequestInput: &input, TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), ExprVersion: 1, QuotaPerUnit: 1_000_000, GroupRatio: 1}}
				estimated, _, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{C: 50}, input)
				require.NoError(t, err)
				require.Equal(t, float64(50), estimated)
				encoded, err := marketingPricingEvidence(info)
				require.NoError(t, err)
				saved = append(saved, encoded)
				for _, secret := range []string{"private-prompt", "private-media", "private-key", "private-cookie"} {
					assert.NotContains(t, encoded, secret)
				}
				// Discard every original request and compiled program before recovery.
				info.BillingRequestInput = nil
				input = billingexpr.RequestInput{}
				billingexpr.InvalidateCache()
				recovered, err := ReplayMarketingTieredPricing(encoded, billingexpr.TokenParams{C: 200}, nil, nil, nil)
				require.NoError(t, err)
				assert.Equal(t, 200*multiplier, recovered.ActualQuotaAfterGroup)
			}
			assert.NotEqual(t, saved[0], saved[1], "identical estimates must not collapse distinct future pricing dependencies")
		})
	}
}

func TestMarketingPricingEvidencePreservesPrivacyNormalizationAndTracing(t *testing.T) {
	expression := `tier("base", p * 2 + cr * 0.5 + img * 3 + c * param("rates.0")) * (has(header("Authorization"), "Bearer") && has(param("prompt"), "premium") ? 2 : 1)`
	input := billingexpr.RequestInput{Body: []byte(`{"rates":[4],"prompt":"premium private-prompt-content","image":"private-image"}`), Headers: map[string]string{"Authorization": "Bearer private-real-credential"}}
	info := &relaycommon.RelayInfo{OriginModelName: "test", BillingRequestInput: &input, TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), ExprVersion: 1, QuotaPerUnit: 1_000_000, GroupRatio: 1}}
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 10}
	usage.PromptTokensDetails.CachedTokens = 20
	usage.PromptTokensDetails.ImageTokens = 30
	params := BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
	require.Equal(t, float64(50), params.P)
	original, err := billingexpr.ComputeTieredQuotaWithRequest(info.TieredBillingSnapshot, params, input)
	require.NoError(t, err)
	encoded, err := marketingPricingEvidence(info)
	require.NoError(t, err)
	for _, secret := range []string{"private-prompt-content", "private-image", "private-real-credential"} {
		assert.NotContains(t, encoded, secret)
	}
	input = billingexpr.RequestInput{}
	info.BillingRequestInput = nil
	billingexpr.InvalidateCache()
	recovered, err := ReplayMarketingTieredPricing(encoded, params, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, original.ActualQuotaAfterGroup, recovered.ActualQuotaAfterGroup)
	assert.Equal(t, original.RequestRules, recovered.RequestRules)
	require.Len(t, recovered.RequestRules, 1)
	assert.True(t, recovered.RequestRules[0].Matched)
}

func TestMarketingPricingEvidenceRejectsUnsafeFutureDependencyBeforeReservation(t *testing.T) {
	for _, expression := range []string{
		`tier("base", float(header("Authorization") + string(c)))`,
		`tier("base", c * param(c > 100 ? "high" : "low"))`,
	} {
		input := billingexpr.RequestInput{Headers: map[string]string{"Authorization": "private-credential"}, Body: []byte(`{"high":2,"low":8}`)}
		info := &relaycommon.RelayInfo{UserId: 1, TokenId: 1, RequestId: "unsafe-pricing", OriginModelName: "test", BillingRequestInput: &input, TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression}}
		_, err := marketingPricingEvidence(info)
		require.ErrorContains(t, err, "cannot be retained safely")
		assert.NotContains(t, err.Error(), "private-credential")
		// Failure happens before a reservation or dispatch can be created.
		session, err := NewMarketingBillingSession(info, 1)
		require.ErrorContains(t, err, "cannot be retained safely")
		assert.Nil(t, session)
	}
}

func TestMarketingPricingEvidenceReplaysActualEvaluationClock(t *testing.T) {
	expression := `tier("timed", c * (hour("UTC") == 1 ? 2 : 8))`
	at := time.Date(2026, 11, 23, 1, 59, 59, 0, time.UTC)
	info := &relaycommon.RelayInfo{TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), ExprVersion: 1, QuotaPerUnit: 1_000_000, GroupRatio: 1}, Billing: &MarketingBillingSession{}}
	// Sending a request does not fix its later time-dependent price.
	before, err := marketingPricingEvidence(info)
	require.NoError(t, err)
	_, err = ReplayMarketingTieredPricing(before, billingexpr.TokenParams{C: 200}, nil, nil, nil)
	require.ErrorContains(t, err, "evaluation time")
	recovered, err := ReplayMarketingTieredPricing(before, billingexpr.TokenParams{C: 200}, &at, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 400, recovered.ActualQuotaAfterGroup)
	// Capture the clock actually used by the production settlement entry point.
	ok, quota, _ := TryTieredSettle(info, billingexpr.TokenParams{C: 200})
	require.True(t, ok)
	require.NotNil(t, info.BillingEvaluationTime)
	encoded, err := marketingPricingEvidence(info)
	require.NoError(t, err)
	future := info.BillingEvaluationTime.Add(24*time.Hour + 3*time.Hour)
	replay, err := ReplayMarketingTieredPricing(encoded, billingexpr.TokenParams{C: 200}, &future, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, quota, replay.ActualQuotaAfterGroup)
	// Evidence is serializable, and the original expression hash is enforced.
	frozen, err := billingexpr.CaptureRequestPricing(expression, billingexpr.RequestInput{})
	require.NoError(t, err)
	b, err := common.Marshal(frozen)
	require.NoError(t, err)
	var restored billingexpr.RequestPricingEvidence
	require.NoError(t, common.Unmarshal(b, &restored))
	_, _, err = billingexpr.RunExprWithRequest(`tier("other", c)`, billingexpr.TokenParams{C: 1}, billingexpr.RequestInput{Evidence: &restored, EvaluationTime: &at})
	require.ErrorContains(t, err, "does not match")
}

func TestMarketingPricingEvidencePreservesTaskAndImageQuantities(t *testing.T) {
	at := time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
	expression := `tier("task", u("seconds") * (hour("UTC") == 1 ? 2 : 8))`
	snap := &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), ExprVersion: 1, QuotaPerUnit: 100, GroupRatio: 1, TaskUsageBilling: true, UsageFacts: map[string]any{"seconds": 2.0}, SettlementTime: &at}
	result, facts, err := EvaluateTaskCompletionUsageAt(snap, map[string]any{"seconds": 4.0}, &at)
	require.NoError(t, err)
	assert.Equal(t, 800, result.ActualQuotaAfterGroup)
	encoded, err := marketingPricingEvidence(&relaycommon.RelayInfo{TieredBillingSnapshot: snap})
	require.NoError(t, err)
	snap = nil
	billingexpr.InvalidateCache()
	restored, err := ReplayMarketingTieredPricing(encoded, billingexpr.TokenParams{}, nil, facts, nil)
	require.NoError(t, err)
	assert.Equal(t, 800, restored.ActualQuotaAfterGroup)
	expression = `tier("image", fixed(0.01)) * image_count`
	estimated := 2
	encoded, err = marketingPricingEvidence(&relaycommon.RelayInfo{TieredBillingSnapshot: &billingexpr.BillingSnapshot{EstimatedImageCount: &estimated, BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), ExprVersion: 1, QuotaPerUnit: 10000, GroupRatio: 1}})
	require.NoError(t, err)
	restored, err = ReplayMarketingTieredPricing(encoded, billingexpr.TokenParams{}, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 200, restored.ActualQuotaAfterGroup)
	actual := 3
	restored, err = ReplayMarketingTieredPricing(encoded, billingexpr.TokenParams{}, nil, nil, &actual)
	require.NoError(t, err)
	assert.Equal(t, 300, restored.ActualQuotaAfterGroup)
}
