package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// MarketingMaintenanceRequest is an operator-only local command contract, not an
// HTTP API. Money is entered in site display units, except explicit CNY cash.
type MarketingMaintenanceRequest struct {
	Action                      string          `json:"action"`
	ActorID                     int             `json:"actor_id"`
	UserID                      int             `json:"user_id"`
	Reference                   string          `json:"reference"`
	Evidence                    string          `json:"evidence"`
	AuthorizationReference      string          `json:"authorization_reference"`
	WriterIsolationEvidence     string          `json:"writer_isolation_evidence"`
	ConversionEvidence          string          `json:"conversion_evidence"`
	Currency                    string          `json:"currency"`
	ExpectedBalance             string          `json:"expected_balance"`
	ExpectedAffBalance          string          `json:"expected_aff_balance"`
	PaidOpeningAmount           string          `json:"paid_opening_amount"`
	GiftOpeningAmount           string          `json:"gift_opening_amount"`
	AffDisposition              string          `json:"aff_disposition"`
	AffGiftAmount               string          `json:"aff_gift_amount"`
	PaymentHistory              string          `json:"payment_history"`
	ExpectedRevision            int64           `json:"expected_revision"`
	ExactRefundCNY              string          `json:"exact_refund_cny"`
	RefundedCNY                 string          `json:"refunded_cny"`
	FundingID                   int64           `json:"funding_id"`
	Version                     int64           `json:"version"`
	FinalAmount                 *string         `json:"final_amount"`
	TerminalEvidence            string          `json:"terminal_evidence"`
	SourceEvidence              string          `json:"source_evidence"`
	RealtimeTerminal            json.RawMessage `json:"realtime_terminal"`
	OriginalRequestID           string          `json:"original_request_id"`
	ObservedAt                  int64           `json:"observed_at"`
	TaskKind                    string          `json:"task_kind"`
	TaskID                      int             `json:"task_id"`
	Terminal                    string          `json:"terminal"`
	ExpectedTaskAmount          string          `json:"expected_task_amount"`
	ConfirmedNetDebitAmount     string          `json:"confirmed_net_debit_amount"`
	ConfirmedUserUsageAmount    string          `json:"confirmed_user_usage_amount"`
	ConfirmedTokenDebitAmount   *string         `json:"confirmed_token_debit_amount"`
	ConfirmedChannelUsageAmount *string         `json:"confirmed_channel_usage_amount"`
}

type marketingMaintenanceAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func marketingMaintenanceDisplay(value int64) (marketingMaintenanceAmount, error) {
	if value < -common.MaxWalletQuota || value > common.MaxWalletQuota {
		return marketingMaintenanceAmount{}, model.ErrMarketingInput
	}
	negative := value < 0
	if negative {
		value = -value
	}
	amount, currency, err := MarketingWalletDisplayAmount(value)
	if negative {
		amount = "-" + amount
	}
	return marketingMaintenanceAmount{amount, currency}, err
}

// RunMarketingMaintenance is callable only from the trusted local CLI. identity
// comes from the OS, never from request JSON. Preview does not create accounts.
func RunMarketingMaintenance(ctx context.Context, input MarketingMaintenanceRequest, identity string, execute bool) (map[string]any, error) {
	// JSON null is an absent optional terminal, not a Realtime response.
	if bytes.Equal(bytes.TrimSpace(input.RealtimeTerminal), []byte("null")) {
		input.RealtimeTerminal = nil
	}
	switch input.Action {
	case "inventory", "review-origin", "clear", "reconcile":
	default:
		return nil, model.ErrMarketingInput
	}
	inventory, err := model.GetMarketingMaintenanceInventory(ctx, input.ActorID, input.UserID)
	if err != nil {
		return nil, err
	}
	if !execute || input.Action == "inventory" {
		return marketingMaintenanceInventoryView(inventory)
	}
	if strings.TrimSpace(input.AuthorizationReference) == "" || strings.TrimSpace(input.Evidence) == "" || strings.TrimSpace(input.Reference) == "" || strings.TrimSpace(identity) == "" {
		return nil, model.ErrMarketingInput
	}
	// Isolation is an operator attestation referencing the stopped writers and
	// drained/isolated queues. It is not evidence of a zero upstream charge.
	if strings.TrimSpace(input.WriterIsolationEvidence) == "" {
		return nil, fmt.Errorf("writer isolation evidence is required")
	}
	conversion, err := MarketingDisplayMoney("1")
	if err != nil {
		return nil, err
	}
	if input.Currency != conversion.Currency {
		return nil, fmt.Errorf("currency does not match current site settings")
	}
	var op *model.MarketingMaintenanceOperation
	switch input.Action {
	case "review-origin":
		expected, err := marketingMaintenanceQuota(input.ExpectedBalance)
		if err != nil {
			return nil, err
		}
		aff, err := marketingMaintenanceQuota(input.ExpectedAffBalance)
		if err != nil {
			return nil, err
		}
		paid, err := marketingMaintenanceQuota(input.PaidOpeningAmount)
		if err != nil {
			return nil, err
		}
		gift, err := marketingMaintenanceMoney(input.GiftOpeningAmount)
		if err != nil {
			return nil, err
		}
		affGift, err := marketingMaintenanceMoney(input.AffGiftAmount)
		if err != nil {
			return nil, err
		}
		evidence, err := common.Marshal(struct {
			Input             MarketingMaintenanceRequest
			DisplayConversion model.MarketingMoneySnapshot
		}{input, conversion})
		if err != nil {
			return nil, err
		}
		err = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			var err error
			op, err = model.ReviewMarketingOrigin(tx, model.MarketingOriginReviewInput{ActorID: input.ActorID, UserID: input.UserID, Reference: input.Reference, Evidence: string(evidence), LocalIdentity: identity, AuthorizationReference: input.AuthorizationReference, ConversionEvidence: input.ConversionEvidence, ExpectedQuota: expected, ExpectedAffQuota: aff, PaidOpeningQuota: paid, GiftOpeningMoney: gift, AffDisposition: input.AffDisposition, AffGiftMoney: affGift, PaymentHistory: input.PaymentHistory}, common.GetTimestamp())
			return err
		})
		if err != nil {
			return nil, err
		}
	case "clear":
		expected, err := marketingMaintenanceQuota(input.ExpectedBalance)
		if err != nil {
			return nil, err
		}
		if !marketingPaidCNYPattern.MatchString(input.RefundedCNY) {
			return nil, model.ErrMarketingInput
		}
		cash, err := decimal.NewFromString(input.RefundedCNY)
		if err != nil || cash.IsNegative() || !cash.Equal(cash.Round(2)) || cash.Shift(2).GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
			return nil, model.ErrMarketingInput
		}
		evidence, err := common.Marshal(struct {
			Input             MarketingMaintenanceRequest
			DisplayConversion model.MarketingMoneySnapshot
		}{input, conversion})
		if err != nil {
			return nil, err
		}
		clear := model.MarketingAssetClearInput{ActorID: input.ActorID, UserID: input.UserID, Reference: input.Reference, Evidence: string(evidence), LocalIdentity: identity, AuthorizationReference: input.AuthorizationReference, ConversionEvidence: input.ConversionEvidence, ExpectedPaidQuota: expected, ExpectedRevision: input.ExpectedRevision, ExactRefundCNY: input.ExactRefundCNY, RefundedCents: cash.Shift(2).IntPart()}
		// Preparation is bounded. Retrying the same command resumes immutable old
		// qualifications; the final transaction refuses newly captured facts.
		pending, err := model.PlanMarketingClearQualificationsAuthorized(ctx, clear, 100)
		if err != nil {
			return nil, err
		}
		if pending {
			return nil, model.ErrMarketingClearPreparation
		}
		err = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			var err error
			op, err = model.ClearMarketingAssets(tx, clear, common.GetTimestamp())
			return err
		})
		if err != nil {
			return nil, err
		}
	case "reconcile":
		if strings.TrimSpace(input.TerminalEvidence) == "" || strings.TrimSpace(input.SourceEvidence) == "" {
			return nil, model.ErrMarketingInput
		}
		err = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			var replay bool
			var err error
			if input.TaskKind != "" {
				if input.FinalAmount == nil {
					return model.ErrMarketingInput
				}
				expected, err := marketingMaintenanceQuota(input.ExpectedTaskAmount)
				if err != nil {
					return err
				}
				actual, err := marketingMaintenanceQuota(*input.FinalAmount)
				if err != nil {
					return err
				}
				netDebit, err := marketingMaintenanceQuota(input.ConfirmedNetDebitAmount)
				if err != nil {
					return err
				}
				userUsage, err := marketingMaintenanceQuota(input.ConfirmedUserUsageAmount)
				if err != nil {
					return err
				}
				var tokenDebit, channelUsage *int64
				if input.ConfirmedTokenDebitAmount != nil {
					value, err := marketingMaintenanceQuota(*input.ConfirmedTokenDebitAmount)
					if err != nil {
						return err
					}
					tokenDebit = &value
				}
				if input.ConfirmedChannelUsageAmount != nil {
					value, err := marketingMaintenanceQuota(*input.ConfirmedChannelUsageAmount)
					if err != nil {
						return err
					}
					channelUsage = &value
				}
				evidence, err := common.Marshal(struct {
					Input             MarketingMaintenanceRequest
					DisplayConversion model.MarketingMoneySnapshot
				}{input, conversion})
				if err != nil {
					return err
				}
				op, err = model.ReconcileMarketingLegacyTask(tx, model.MarketingLegacyTaskReconcileInput{ActorID: input.ActorID, UserID: input.UserID, Reference: input.Reference, Evidence: string(evidence), LocalIdentity: identity, AuthorizationReference: input.AuthorizationReference, TaskKind: input.TaskKind, TaskID: int64(input.TaskID), ExpectedQuota: expected, ActualQuota: actual, ConfirmedNetDebitQuota: netDebit, ConfirmedUserUsageQuota: &userUsage, ConfirmedTokenDebitQuota: tokenDebit, ConfirmedChannelUsageQuota: channelUsage, Terminal: input.Terminal, OriginalSourceEvidence: input.SourceEvidence, WriterIsolationEvidence: input.WriterIsolationEvidence}, common.GetTimestamp())
				return err
			}
			audited := input
			audited.RealtimeTerminal = nil
			terminalHash := ""
			if len(input.RealtimeTerminal) > 0 {
				digest := sha256.Sum256(input.RealtimeTerminal)
				terminalHash = hex.EncodeToString(digest[:])
			}
			// Bind the provider receipt without storing transcripts, audio,
			// arbitrary metadata or credentials from a full response payload.
			payload := struct {
				Input                  MarketingMaintenanceRequest
				RealtimeTerminalDigest string
				DisplayConversion      model.MarketingMoneySnapshot
			}{audited, terminalHash, conversion}
			op, replay, err = model.BeginMarketingMaintenanceOperation(tx, input.ActorID, input.UserID, "reconcile", input.Reference, input.Evidence, identity, input.AuthorizationReference, payload, common.GetTimestamp())
			if err != nil || replay {
				return err
			}
			if input.FundingID <= 0 {
				return model.ErrMarketingInput
			}
			var funding *model.MarketingFunding
			if len(input.RealtimeTerminal) > 0 {
				snapshot, closeErr := model.GetMarketingRealtimeSnapshot(tx, input.UserID, input.FundingID)
				if closeErr != nil {
					return closeErr
				}
				if !snapshot.Funding.RealtimeClosed {
					if _, closeErr = model.CloseMarketingRealtimeFunding(tx, input.UserID, input.FundingID, input.WriterIsolationEvidence); closeErr != nil {
						return closeErr
					}
				}
				funding, err = reconcileMarketingRealtimeResponse(tx, input.UserID, input.FundingID, input.RealtimeTerminal, time.Unix(input.ObservedAt, 0), input.OriginalRequestID)
				// A credible terminal can leave other rounds unresolved. Persist that fact
				// without pretending the whole connection is settled.
				if errors.Is(err, model.ErrMarketingReview) && funding != nil {
					err = nil
				}
			} else {
				if input.FinalAmount == nil || input.Version <= 0 {
					return model.ErrMarketingInput
				}
				quota, parseErr := marketingMaintenanceQuota(*input.FinalAmount)
				if parseErr != nil {
					return parseErr
				}
				var prior model.MarketingFunding
				if err = tx.First(&prior, input.FundingID).Error; err != nil {
					return err
				}
				if prior.UserID != input.UserID || prior.Realtime || prior.Revision == math.MaxInt64 {
					return model.ErrMarketingConflict
				}
				record, recordErr := model.MarketingMaintenanceUsageRecord(tx, input.UserID, input.FundingID, input.OriginalRequestID)
				if recordErr != nil {
					return fmt.Errorf("load original request evidence: %w", recordErr)
				}
				evidence, marshalErr := common.Marshal(payload)
				if marshalErr != nil {
					return marshalErr
				}
				funding, err = model.SettleMarketingConsumption(tx, model.MarketingSettlementInput{UserID: input.UserID, FundingID: input.FundingID, Version: input.Version, ActualQuota: &quota, Final: true, EvidenceKind: "admin_reconcile", Evidence: string(evidence)}, record)
			}
			if err != nil {
				return fmt.Errorf("reconcile original funding: %w", err)
			}
			if err := model.ReconcileMarketingTaskOutcome(tx, input.UserID, funding.ID, input.Terminal, common.GetTimestamp()); err != nil {
				return fmt.Errorf("record reconciled task outcome: %w", err)
			}
			return model.CompleteMarketingMaintenanceOperation(tx, op, map[string]any{"funding_id": funding.ID, "state": funding.State, "revision": funding.Revision})
		})
	}
	if err != nil {
		return nil, err
	}
	cacheErr := model.RefreshMarketingMaintenanceCaches(input.UserID)
	output := map[string]any{"executed": true, "operation_id": op.ID, "action": input.Action, "user_id": input.UserID, "recorded_at": op.RecordedAt}
	if cacheErr != nil {
		output["cache_refresh_pending"] = true
	}
	if input.Action == "clear" {
		var cleared model.MarketingAssetClearResult
		if err := common.UnmarshalJsonStr(op.Result, &cleared); err != nil {
			return nil, err
		}
		paid, err := marketingMaintenanceDisplay(cleared.PaidQuota)
		if err != nil {
			return nil, err
		}
		gift, err := marketingMaintenanceDisplay(cleared.GiftQuota)
		if err != nil {
			return nil, err
		}
		output["cleared_paid"] = paid
		output["cleared_gift"] = gift
		output["exact_refund_cny"] = cleared.ExactRefundCNY
		output["refunded_cny"] = decimal.NewFromInt(cleared.RefundedCents).Shift(-2).String()
		output["rounding_difference_cny"] = cleared.RoundingDifferenceCNY
	} else if input.Action == "reconcile" && input.TaskKind == "" {
		var result struct {
			State     string `json:"state"`
			FundingID int64  `json:"funding_id"`
			Revision  int64  `json:"revision"`
		}
		if err := common.UnmarshalJsonStr(op.Result, &result); err != nil {
			return nil, err
		}
		output["funding_id"] = result.FundingID
		output["state"] = result.State
		output["revision"] = result.Revision
	}
	return output, nil
}

var marketingMaintenanceZero = regexp.MustCompile(`^0(?:\.0{1,12})?$`)

// Zero is a legitimate evidenced final fee or absent opening component, but
// remains forbidden for ordinary gift issuance and payment creation.
func marketingMaintenanceMoney(amount string) (model.MarketingMoneySnapshot, error) {
	if !marketingMaintenanceZero.MatchString(amount) {
		return MarketingDisplayMoney(amount)
	}
	money, err := MarketingDisplayMoney("1")
	if err != nil {
		return money, err
	}
	money.Amount = amount
	return money, nil
}

func marketingMaintenanceQuota(amount string) (int64, error) {
	if marketingMaintenanceZero.MatchString(amount) {
		_, err := MarketingDisplayMoney("1")
		return 0, err
	}
	money, err := MarketingDisplayMoney(amount)
	if err != nil {
		return 0, err
	}
	return money.Quota()
}

func marketingMaintenanceInventoryView(in *model.MarketingMaintenanceInventory) (map[string]any, error) {
	balance, err := marketingMaintenanceDisplay(in.Quota)
	if err != nil {
		return nil, err
	}
	aff, err := marketingMaintenanceDisplay(in.AffQuota)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"executed": false, "user_id": in.UserID, "status": in.Status, "balance": balance, "legacy_invitation_balance": aff, "pending_qualifications": in.PendingQualifications}
	if in.Account != nil {
		result["origin_state"] = in.Account.OriginState
		result["revision"] = in.Account.Revision
		result["payment_history"] = in.Account.PaymentHistory
	}
	funding := make([]map[string]any, 0, len(in.Funding))
	for _, f := range in.Funding {
		reserved, err := marketingMaintenanceDisplay(f.ReservedQuota)
		if err != nil {
			return nil, err
		}
		funding = append(funding, map[string]any{"id": f.ID, "state": f.State, "cost_known": f.CostKnown, "final_observed": f.FinalObserved, "revision": f.Revision, "reserved": reserved, "realtime": f.Realtime, "realtime_closed": f.RealtimeClosed})
	}
	result["unresolved_requests"] = funding
	tasks := make([]map[string]any, 0, len(in.Tasks)+len(in.Midjourney))
	for _, t := range in.Tasks {
		tasks = append(tasks, map[string]any{"kind": "task", "id": t.ID, "status": t.Status, "billing_pending": t.MarketingBillingPending})
	}
	for _, t := range in.Midjourney {
		tasks = append(tasks, map[string]any{"kind": "midjourney", "id": t.Id, "status": t.Status, "billing_pending": t.MarketingBillingPending})
	}
	result["unresolved_tasks"] = tasks
	gifts := make([]map[string]any, 0, len(in.Gifts))
	for _, gift := range in.Gifts {
		remaining, err := marketingMaintenanceDisplay(gift.RemainingQuota)
		if err != nil {
			return nil, err
		}
		consumed, err := marketingMaintenanceDisplay(gift.ConsumedQuota)
		if err != nil {
			return nil, err
		}
		gifts = append(gifts, map[string]any{"id": gift.ID, "source": gift.SourceKind, "reference": gift.SourceReference, "remaining": remaining, "consumed": consumed, "expires_at": gift.ExpiresAt})
	}
	result["gifts"] = gifts
	history, err := marketingMaintenanceDisplay(in.AffHistoryQuota)
	if err != nil {
		return nil, err
	}
	result["legacy_invitation_history"] = history
	payments := make([]map[string]any, 0, len(in.Payments))
	for _, payment := range in.Payments {
		credited, err := marketingMaintenanceDisplay(payment.CreditedQuota)
		if err != nil {
			return nil, err
		}
		payments = append(payments, map[string]any{"id": payment.ID, "source": payment.SourceKind, "reference": payment.SourceReference, "actual_paid_cny": decimal.NewFromInt(payment.PaidCents).Shift(-2).String(), "credited": credited, "posted_at": payment.PostedAt})
	}
	result["verified_payments"] = payments
	orders := make([]map[string]any, 0, len(in.TopUps))
	for _, order := range in.TopUps {
		// A baseline top-up log or order amount does not establish cash origin.
		orders = append(orders, map[string]any{"id": order.Id, "trade_no": order.TradeNo, "provider": order.PaymentProvider, "status": order.Status, "verified_payment_id": order.PaymentID})
	}
	result["payment_orders"] = orders
	codes := make([]map[string]any, 0, len(in.Redemptions))
	for _, code := range in.Redemptions {
		credited, err := marketingMaintenanceDisplay(int64(code.Quota))
		if err != nil {
			return nil, err
		}
		codes = append(codes, map[string]any{"id": code.Id, "credited": credited, "sales_channel": code.SalesChannel, "sales_batch": code.SalesBatch, "verified_payment_id": code.PaymentID})
	}
	result["redeemed_codes"] = codes
	subscriptions := make([]map[string]any, 0, len(in.Subscriptions))
	for _, sub := range in.Subscriptions {
		subscriptions = append(subscriptions, map[string]any{"id": sub.Id, "plan_id": sub.PlanId, "status": sub.Status, "end_time": sub.EndTime})
	}
	result["legacy_subscriptions"] = subscriptions
	return result, nil
}
