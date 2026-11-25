package service

import (
	"errors"
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// MarketingBillingSession coordinates a request; its fields are not the source
// of truth for balances, dispatch ownership or terminal settlement. Every write
// below is a business database transaction and bypasses legacy quota caches.
type MarketingBillingSession struct {
	mu       sync.Mutex
	info     *relaycommon.RelayInfo
	funding  model.MarketingFunding
	dispatch *model.MarketingDispatch
	estimate int
}

var _ relaycommon.BillingSettler = (*MarketingBillingSession)(nil)

func NewMarketingBillingSession(info *relaycommon.RelayInfo, estimate int) (*MarketingBillingSession, error) {
	if info == nil || estimate < 0 || info.UserId <= 0 || info.RequestId == "" {
		return nil, model.ErrMarketingInput
	}
	// A zero estimate is not proof of a free request. Keep a minimal source
	// reservation so an exhausted wallet or token cannot dispatch paid work.
	if !info.PriceData.FreeModel {
		estimate = max(estimate, 1)
	}
	pricing, err := marketingPricingEvidence(info)
	if err != nil {
		return nil, err
	}
	tokenID := info.TokenId
	if info.IsPlayground {
		tokenID = 0
	}
	var funding *model.MarketingFunding
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var reserveErr error
		funding, reserveErr = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{
			UserID: info.UserId, TokenID: tokenID, Playground: info.IsPlayground,
			RequestReference: info.RequestId, QualifiedAt: info.StartTime.Unix(),
			Quota: int64(estimate), PricingEvidence: pricing,
		})
		return reserveErr
	})
	if err != nil {
		return nil, err
	}
	info.FinalPreConsumedQuota = estimate
	info.BillingSource = BillingSourceWallet
	return &MarketingBillingSession{info: info, funding: *funding, estimate: estimate}, nil
}

func (s *MarketingBillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.estimate
}

func (s *MarketingBillingSession) Reserve(target int) error {
	return s.reserve(target, false)
}

func (s *MarketingBillingSession) ReserveBeforeSubmit(target int) error {
	return s.reserve(target, true)
}

func (s *MarketingBillingSession) reserve(target int, beforeDispatch bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target < 0 {
		return model.ErrMarketingInput
	}
	if !s.info.PriceData.FreeModel {
		target = max(target, 1)
	}
	var funding *model.MarketingFunding
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var reserveErr error
		if beforeDispatch {
			funding, reserveErr = model.IncreaseMarketingReservation(tx, s.info.UserId, s.funding.ID, int64(target))
		} else {
			funding, reserveErr = model.ExtendMarketingReservation(tx, s.info.UserId, s.funding.ID, int64(target))
		}
		return reserveErr
	})
	if err != nil {
		return err
	}
	s.funding = *funding
	s.estimate, err = common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(funding.ReservedQuota))
	if err != nil {
		return err
	}
	s.info.FinalPreConsumedQuota = s.estimate
	return nil
}

// BeginDispatch returns only after the one-use dispatch fact has committed.
// A retry of this method may discover the previous commit, but cannot send again.
func (s *MarketingBillingSession) BeginDispatch() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pricing, err := marketingPricingEvidence(s.info)
	if err != nil {
		return err
	}
	var dispatch *model.MarketingDispatch
	var created bool
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var dispatchErr error
		dispatch, created, dispatchErr = model.BeginMarketingDispatch(tx, model.MarketingDispatchInput{
			UserID: s.info.UserId, FundingID: s.funding.ID,
			Reference: fmt.Sprintf("%s/attempt/%d", s.info.RequestId, s.info.RetryIndex), PricingEvidence: pricing,
		})
		return dispatchErr
	})
	if err != nil {
		return err
	}
	s.dispatch = dispatch
	if !created {
		return model.ErrMarketingConflict
	}
	return nil
}

func (s *MarketingBillingSession) RejectDispatch(evidence string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatch == nil {
		return model.ErrMarketingConflict
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		return model.RejectMarketingDispatch(tx, s.info.UserId, s.funding.ID, s.dispatch.ID, evidence)
	})
}

func (s *MarketingBillingSession) RememberTaskReceipt(upstreamID, publicID string, result *relaycommon.TaskInfo, calculatedQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatch == nil {
		return model.ErrMarketingConflict
	}
	receipt := map[string]any{"upstream_task_id": upstreamID, "task_id": publicID, "calculated_quota": calculatedQuota, "terminal": false}
	if result != nil {
		receipt["terminal"] = result.Status == model.TaskStatusSuccess || result.Status == model.TaskStatusFailure
		receipt["status"] = result.Status
		receipt["billing_unknown"] = result.BillingUnknown
		receipt["usage_facts"] = result.UsageFacts
	}
	evidence, err := common.Marshal(receipt)
	if err != nil {
		return err
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		return model.ObserveMarketingDispatch(tx, s.info.UserId, s.funding.ID, s.dispatch.ID, string(evidence))
	})
}

// Settle is used only for a known terminal amount. Unknown outcomes must call
// PreserveUnknown instead; an estimate is never promoted to actual consumption.
func (s *MarketingBillingSession) Settle(actual int) error {
	amount := int64(actual)
	return s.SettleEvidence(&amount, true, "upstream_terminal", "terminal amount from existing pricing contract", 1)
}

// Versions and evidence are stable facts chosen by the server's protocol path.
// Retrying the same fact therefore replays its persisted result, not a new debit.
func (s *MarketingBillingSession) SettleEvidence(actual *int64, final bool, kind, evidence string, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var funding *model.MarketingFunding
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var settleErr error
		funding, settleErr = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{
			UserID: s.info.UserId, FundingID: s.funding.ID, Version: version,
			ActualQuota: actual, Final: final, EvidenceKind: kind, Evidence: evidence,
		})
		return settleErr
	})
	if err != nil {
		return err
	}
	s.funding = *funding
	if funding.State == "review" {
		return model.ErrMarketingReview
	}
	return nil
}

func (s *MarketingBillingSession) PreserveUnknown(evidence string) error {
	return s.SettleEvidence(nil, true, "unknown", evidence, 1)
}

func (s *MarketingBillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.funding.Realtime && s.funding.State != "settled" && !s.funding.FinalObserved
}

// Refund means finalize a failed request, not necessarily credit its reservation.
// The database rejects pre-dispatch cancellation after any unresolved dispatch.
func (s *MarketingBillingSession) Refund(c *gin.Context) {
	if !s.NeedsRefund() {
		return
	}
	zero := int64(0)
	err := s.SettleEvidence(&zero, true, "pre_dispatch_cancel", "request stopped before any accepted dispatch", 1)
	if errors.Is(err, model.ErrMarketingConflict) {
		err = s.PreserveUnknown("request failed after possible upstream execution")
	}
	if err != nil && !errors.Is(err, model.ErrMarketingReview) {
		common.SysError(fmt.Sprintf("marketing request finalization requires reconciliation (user=%d funding=%d): %v", s.info.UserId, s.funding.ID, err))
	}
}

// RecordMarketingConsumption uses the original pricing result while recording
// certainty separately. A broken stream never turns its estimate into a bill.
func RecordMarketingConsumption(c *gin.Context, info *relaycommon.RelayInfo, params model.RecordConsumeLogParams, usage *dto.Usage, pricingValid, fixed bool) bool {
	session, managed := info.Billing.(*MarketingBillingSession)
	if !managed {
		return false
	}
	estimated := info.BillingUsageEstimated || common.GetContextKeyBool(c, constant.ContextKeyEstimatedBillingUsage) ||
		(usage != nil && usage.BillingUsage != nil && usage.BillingUsage.Estimated)
	confirmedUsage := dto.HasOpenAIUsageTokens(effectiveBillingUsage(usage)) && !estimated
	// A request-priced leaf may still have been selected by estimated token
	// counts. Only a usage-independent price can settle without actual usage.
	if fixed && !confirmedUsage && info.TieredBillingSnapshot != nil {
		snapshot := info.TieredBillingSnapshot
		used := billingexpr.UsedVarsByHash(snapshot.ExprString, snapshot.ExprHash)
		for _, name := range []string{"p", "c", "len", "cr", "cc", "cc1h", "cc12h", "img", "img_cr", "img_o", "ai", "ao", "u"} {
			if used[name] {
				fixed = false
				break
			}
		}
	}
	known := pricingValid && (confirmedUsage || fixed)
	outcome := info.StreamStatus.OutcomeSnapshot()
	if info.IsStream || outcome.ExpectsTerminal {
		complete := outcome.Response == relaycommon.ResponseOutcomeCompleted ||
			(outcome.Response == relaycommon.ResponseOutcomeUnknown && !outcome.HasErrors && (outcome.EndReason == relaycommon.StreamEndReasonDone ||
				(outcome.EndReason == relaycommon.StreamEndReasonEOF && !outcome.ExpectsTerminal)))
		known = known && complete
	}
	var actual *int64
	kind := "unknown"
	if known {
		value := int64(params.Quota)
		actual = &value
		kind = "upstream_terminal"
	}
	pricing, err := marketingPricingEvidence(info)
	if err != nil {
		common.SysError(fmt.Sprintf("marketing final pricing evidence encoding failed: %v", err))
		return true
	}
	evidence, err := common.Marshal(map[string]any{"usage": usage, "usage_estimated": estimated, "usage_confirmed": confirmedUsage, "stream_outcome": outcome, "pricing_valid": pricingValid, "calculated_quota": params.Quota, "pricing": pricing})
	if err != nil {
		common.SysError(fmt.Sprintf("marketing usage evidence encoding failed: %v", err))
		return true
	}
	record := model.BuildConsumeLog(c, info.UserId, params)
	record.RequestId = info.RequestId
	if err := session.recordConsumption(*record, actual, kind, string(evidence), 1); err != nil {
		common.SysError(fmt.Sprintf("marketing consumption requires reconciliation (request=%s): %v", info.RequestId, err))
	}
	return true
}

func (session *MarketingBillingSession) recordConsumption(record model.Log, actual *int64, kind, evidence string, version int64) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	var result *model.MarketingFunding
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var settleErr error
		result, settleErr = model.SettleMarketingConsumption(tx, model.MarketingSettlementInput{
			UserID: session.info.UserId, FundingID: session.funding.ID, Version: version,
			ActualQuota: actual, Final: true, EvidenceKind: kind, Evidence: evidence,
		}, record)
		return settleErr
	})
	if err != nil {
		return err
	}
	session.funding = *result
	var projection model.MarketingUsageProjection
	if err = model.DB.Where("funding_id = ? AND version = ?", result.ID, result.Revision).First(&projection).Error; err == nil {
		err = model.PublishMarketingUsage(projection)
	}
	return err
}

// MarketingBillingMessage keeps internal ledger/database errors out of model
// client responses while honoring the existing reader language selection.
func MarketingBillingMessage(c *gin.Context, err error) string {
	key := i18n.MsgDatabaseError
	switch {
	case errors.Is(err, model.ErrMarketingInsufficient):
		key = i18n.MsgFundingInsufficient
	case errors.Is(err, model.ErrMarketingReview):
		key = i18n.MsgFundingReview
	case errors.Is(err, model.ErrMarketingAccess):
		key = i18n.MsgFundingAccess
	case errors.Is(err, model.ErrMarketingConflict):
		key = i18n.MsgFundingConflict
	case errors.Is(err, model.ErrMarketingInput):
		key = i18n.MsgFundingInput
	}
	return i18n.T(c, key)
}
