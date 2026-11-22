package service

import (
	"context"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ReserveMidjourneyFunding preserves legacy billing for baseline accounts.
// Managed accounts must cover the request from the shared persistent ledger
// before submitting, including zero-priced actions and gift-only wallets.
func ReserveMidjourneyFunding(info *relaycommon.RelayInfo, quota int) (*MarketingBillingSession, error) {
	managed, err := model.HasMarketingAccount(info.UserId)
	if err != nil || !managed {
		return nil, err
	}
	session, err := NewMarketingBillingSession(info, quota)
	if err != nil {
		return nil, err
	}
	info.Billing = session
	return session, nil
}

// AttachMarketingMidjourney freezes the accepted receipt and fixed-price
// contract before task insertion. Pending tasks hold funds, not a final debit.
func AttachMarketingMidjourney(c *gin.Context, info *relaycommon.RelayInfo, task *model.Midjourney, httpStatus int) error {
	session, managed := info.Billing.(*MarketingBillingSession)
	if !managed {
		return nil
	}
	session.mu.Lock()
	task.MarketingFundingID = session.funding.ID
	session.mu.Unlock()
	task.MarketingBillingPending = true
	task.TokenId = info.TokenId
	other := GenerateMjOtherInfo(info, info.PriceData)
	other.SetPublic("task_id", task.MjId)
	other.SetPublic("request_path", c.Request.URL.Path)
	AppendRelayLogAdminInfo(c, info, other)
	task.MarketingBillingData.MarketingLog = model.BuildConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: task.ChannelId, ModelName: CovertMjpActionToModelName(task.Action),
		TokenName: c.GetString("token_name"), TokenId: info.TokenId, Group: info.UsingGroup, Other: other,
	})
	task.MarketingBillingData.MarketingLog.RequestId = info.RequestId
	accepted := httpStatus == http.StatusOK && (task.Code == 1 || task.Code == 21 || task.Code == 22)
	if !accepted {
		// Non-success HTTP/unknown application codes do not prove non-execution.
		knownRejection := httpStatus == http.StatusOK && (task.Code == 3 || task.Code == 4 || task.Code == 23 || task.Code == 24)
		task.Status, task.Progress = "FAILURE", "100%"
		if err := PrepareMarketingMidjourneyOutcome(task, knownRejection); err != nil {
			return err
		}
	} else if task.Status == "SUCCESS" || task.Status == "FAILURE" {
		if err := PrepareMarketingMidjourneyOutcome(task, true); err != nil {
			return err
		}
	}
	// HTTP rejection evidence is already durable and authorizes cancellation;
	// do not overwrite it with an observation of a dispatched attempt.
	if httpStatus == http.StatusUnauthorized || httpStatus == http.StatusForbidden || httpStatus == http.StatusTooManyRequests {
		return PrepareMarketingMidjourneyOutcome(task, true)
	}
	result := &relaycommon.TaskInfo{Status: task.Status}
	if outcome := task.MarketingBillingData.MarketingOutcome; outcome != nil {
		result.BillingUnknown = outcome.ActualQuota == nil
	}
	return session.RememberTaskReceipt(task.MjId, task.MjId, result, task.Quota)
}

// PrepareMarketingMidjourneyOutcome runs before the terminal CAS. An uncertain
// timeout/missing channel is stored as unknown, never as a free completion.
func PrepareMarketingMidjourneyOutcome(task *model.Midjourney, authoritative bool) error {
	if task.MarketingFundingID == 0 {
		return nil
	}
	if task.MarketingBillingData.MarketingLog == nil {
		return model.ErrMarketingReview
	}
	var actual *int64
	if authoritative && (task.Status == "SUCCESS" || task.Status == "FAILURE") {
		quota := int64(task.Quota)
		if task.Status == "FAILURE" {
			quota = 0
		}
		actual = &quota
	}
	evidence, err := common.Marshal(map[string]any{
		"task_id": task.MjId, "status": task.Status, "actual_quota": actual,
		"provider_confirmed": authoritative,
	})
	if err != nil {
		return err
	}
	task.MarketingBillingData.MarketingOutcome = &model.MarketingTaskOutcome{
		ActualQuota: actual, Evidence: string(evidence), Record: *task.MarketingBillingData.MarketingLog,
	}
	return nil
}

func FinalizeMarketingMidjourney(task *model.Midjourney) error {
	var funding *model.MarketingFunding
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var settleErr error
		funding, settleErr = model.SettleMarketingMidjourney(tx, task.Id)
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

func RecoverMarketingMidjourney(ctx context.Context) error {
	// Keyset paging lets later tasks recover even if an earlier record requires
	// manual reconciliation. No upstream submission is performed here.
	lastID := 0
	for {
		var tasks []model.Midjourney
		if err := model.DB.Where("id > ? AND marketing_billing_pending = ? AND status IN ?", lastID, true, []string{"SUCCESS", "FAILURE"}).Order("id ASC").Limit(100).Find(&tasks).Error; err != nil {
			return err
		}
		for i := range tasks {
			if err := ctx.Err(); err != nil {
				return err
			}
			lastID = tasks[i].Id
			if err := FinalizeMarketingMidjourney(&tasks[i]); err != nil {
				common.SysError(fmt.Sprintf("marketing Midjourney settlement remains pending (task=%d): %v", tasks[i].Id, err))
			}
		}
		if len(tasks) < 100 {
			return nil
		}
	}
}

// MarkMarketingMidjourneyUnknown closes polling without claiming a zero cost.
func MarkMarketingMidjourneyUnknown(task *model.Midjourney, reason string) error {
	previous := task.Status
	task.Status, task.Progress, task.FailReason = "FAILURE", "100%", reason
	if err := PrepareMarketingMidjourneyOutcome(task, false); err != nil {
		return err
	}
	won, err := task.UpdateWithStatus(previous)
	if err != nil || !won {
		return err
	}
	return FinalizeMarketingMidjourney(task)
}
