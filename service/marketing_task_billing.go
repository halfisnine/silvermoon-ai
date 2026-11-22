package service

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// AttachMarketingTask preserves the original request identity and presentation
// metadata before insertion. Async submission reserves funds; it is not proof
// that the estimated price is the final cost.
func AttachMarketingTask(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) bool {
	session, ok := info.Billing.(*MarketingBillingSession)
	if !ok {
		return false
	}
	session.mu.Lock()
	task.MarketingFundingID = session.funding.ID
	session.mu.Unlock()
	task.MarketingBillingPending = true
	other := taskBillingOther(task)
	other.SetPublic("request_path", c.Request.URL.Path)
	task.PrivateData.MarketingLog = model.BuildConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId, ModelName: info.OriginModelName,
		TokenName: c.GetString("token_name"), TokenId: info.TokenId,
		Group: info.UsingGroup, Other: other,
	})
	task.PrivateData.MarketingLog.RequestId = info.RequestId
	return true
}

// PrepareMarketingTaskOutcome has no financial side effects. Its result must
// be persisted with the terminal task CAS, before attempting settlement.
// submittedQuota is used only for a trusted immediate result, whose original
// submit pricing contract has already evaluated the completion facts.
func PrepareMarketingTaskOutcome(task *model.Task, result *relaycommon.TaskInfo, adaptor TaskPollingAdaptor, submittedQuota *int) error {
	if task.MarketingFundingID == 0 {
		return nil
	}
	if task.PrivateData.MarketingLog == nil {
		return model.ErrMarketingReview
	}
	var actual *int64
	var clamp *common.QuotaClamp
	bc := task.PrivateData.BillingContext
	if result != nil && !result.BillingUnknown {
		quota, known := 0, false
		switch {
		case submittedQuota != nil:
			quota, known = *submittedQuota, true
		case task.Status == model.TaskStatusFailure:
			// Only an authoritative provider failure enters this branch.
			// Timeouts, missing tasks and query failures set BillingUnknown.
			known = true
		case bc != nil && bc.TieredSnapshot != nil:
			evaluationTime := time.Now()
			settlement, facts, err := EvaluateTaskCompletionUsageAt(bc.TieredSnapshot, result.UsageFacts, &evaluationTime)
			if err == nil {
				quota, known, clamp = settlement.ActualQuotaAfterGroup, true, settlement.Clamp
				bc.TieredSnapshot.SettlementTime = &evaluationTime
				bc.TieredSnapshot.UsageFacts = facts
				bc.TieredSnapshot.EstimatedTier = settlement.MatchedTier
			}
		case bc != nil && bc.PerCallBilling:
			quota, known = task.Quota, true
		default:
			if adaptor != nil {
				quota = adaptor.AdjustBillingOnComplete(task, result)
				known = quota > 0
			}
			tokens := result.TotalTokens
			if tokens == 0 {
				tokens = result.CompletionTokens
			}
			if !known && tokens > 0 && bc != nil && bc.ModelRatio > 0 {
				multiplier := 1.0
				if price := taskBillingContextPriceData(bc); price != nil {
					multiplier = price.OtherRatioMultiplier()
				}
				quota, clamp = common.QuotaFromFloatChecked(float64(tokens) * bc.ModelRatio * bc.GroupRatio * multiplier)
				known = true
			}
		}
		if known && quota >= 0 {
			amount := int64(quota)
			actual = &amount
		}
	}
	// Whitelist usage evidence: never store URLs, raw provider payloads, plugin
	// state, channel credentials or prompt text in the financial snapshot.
	evidence := map[string]any{"task_id": task.TaskID, "status": task.Status, "actual_quota": actual}
	if result != nil {
		evidence["usage_facts"] = result.UsageFacts
		evidence["total_tokens"] = result.TotalTokens
		evidence["completion_tokens"] = result.CompletionTokens
		evidence["billing_unknown"] = result.BillingUnknown
	}
	encoded, err := common.Marshal(evidence)
	if err != nil {
		return err
	}
	record := *task.PrivateData.MarketingLog
	other := taskBillingOther(task)
	attachQuotaSaturationToOther(other, clamp)
	encodedOther, err := common.Marshal(other)
	if err != nil {
		return err
	}
	record.Other = string(encodedOther)
	if result != nil {
		record.CompletionTokens = max(result.CompletionTokens, 0)
		record.PromptTokens = max(result.TotalTokens-record.CompletionTokens, 0)
	}
	task.PrivateData.MarketingOutcome = &model.MarketingTaskOutcome{ActualQuota: actual, Evidence: string(encoded), Record: record}
	return nil
}

func FinalizeMarketingTask(task *model.Task) error {
	var funding *model.MarketingFunding
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var settleErr error
		funding, settleErr = model.SettleMarketingTask(tx, task.ID)
		return settleErr
	})
	if err != nil {
		return err
	}
	task.Quota, err = common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(funding.SettledQuota))
	if err != nil {
		return err
	}
	task.MarketingBillingPending = false
	var projection model.MarketingUsageProjection
	if err = model.DB.Where("funding_id = ? AND version = ?", funding.ID, funding.Revision).First(&projection).Error; err != nil {
		return err
	}
	return model.PublishMarketingUsage(projection)
}

// Recovery never calls a provider or modifies the stored pricing decision.
// Concurrent workers replay the same immutable final version.
func RecoverMarketingTasks(ctx context.Context) error {
	var lastID int64
	for {
		tasks, err := model.PendingMarketingTasks(lastID, 100)
		if err != nil {
			return err
		}
		for i := range tasks {
			if err := ctx.Err(); err != nil {
				return err
			}
			lastID = tasks[i].ID
			if err := FinalizeMarketingTask(&tasks[i]); err != nil {
				common.SysError(fmt.Sprintf("marketing task settlement remains pending (task=%d): %v", tasks[i].ID, err))
			}
		}
		if len(tasks) < 100 {
			return nil
		}
	}
}
