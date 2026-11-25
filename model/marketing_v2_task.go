package model

import (
	"gorm.io/gorm"
)

// MarketingTaskOutcome is frozen alongside the terminal task transition. A
// second process can finish finance without querying or resubmitting upstream.
// Nil ActualQuota means unknown cost, never a zero-cost failure.
type MarketingTaskOutcome struct {
	ActualQuota *int64 `json:"actual_quota"`
	Evidence    string `json:"evidence"`
	Record      Log    `json:"record"`
}

func SettleMarketingTask(tx *gorm.DB, taskID int64) (*MarketingFunding, error) {
	var task Task
	if err := tx.First(&task, taskID).Error; err != nil {
		return nil, err
	}
	if task.MarketingFundingID <= 0 || (task.Status != TaskStatusSuccess && task.Status != TaskStatusFailure) {
		return nil, ErrMarketingConflict
	}
	if _, _, err := lockMarketingAccount(tx, task.UserId); err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).First(&task, taskID).Error; err != nil {
		return nil, err
	}
	var current MarketingFunding
	if err := lockForUpdate(tx).First(&current, task.MarketingFundingID).Error; err != nil {
		return nil, err
	}
	if current.UserID != task.UserId {
		return nil, ErrMarketingConflict
	}
	// A maintenance reconciliation may have advanced beyond the frozen version-1
	// worker outcome. Return current accounting; never replay old task amounts.
	if current.Revision > 0 {
		err := tx.Model(&task).Updates(map[string]any{"marketing_billing_pending": false, "quota": current.SettledQuota}).Error
		return &current, err
	}
	outcome := task.PrivateData.MarketingOutcome
	if outcome == nil {
		return nil, ErrMarketingReview
	}
	kind := "upstream_terminal"
	if outcome.ActualQuota == nil {
		kind = "unknown"
	}
	funding, err := SettleMarketingConsumption(tx, MarketingSettlementInput{
		UserID: task.UserId, FundingID: task.MarketingFundingID, Version: 1,
		ActualQuota: outcome.ActualQuota, Final: true, EvidenceKind: kind, Evidence: outcome.Evidence,
	}, outcome.Record)
	if err != nil {
		return nil, err
	}
	result := tx.Model(&Task{}).Where("id = ? AND marketing_funding_id = ?", task.ID, funding.ID).
		Updates(map[string]any{"marketing_billing_pending": false, "quota": funding.SettledQuota})
	return funding, result.Error
}

func PendingMarketingTasks(afterID int64, limit int) ([]Task, error) {
	if limit <= 0 || limit > 1000 {
		return nil, ErrMarketingInput
	}
	var tasks []Task
	err := DB.Where("id > ? AND marketing_billing_pending = ? AND status IN ?", afterID, true, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).
		Order("id ASC").Limit(limit).Find(&tasks).Error
	return tasks, err
}

// SettleMarketingMidjourney uses the same source allocation and usage projection
// contract as other tasks, but keeps the existing Midjourney task identity.
func SettleMarketingMidjourney(tx *gorm.DB, taskID int) (*MarketingFunding, error) {
	var task Midjourney
	if err := tx.First(&task, taskID).Error; err != nil {
		return nil, err
	}
	if task.MarketingFundingID <= 0 || (task.Status != "SUCCESS" && task.Status != "FAILURE") {
		return nil, ErrMarketingConflict
	}
	if _, _, err := lockMarketingAccount(tx, task.UserId); err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).First(&task, taskID).Error; err != nil {
		return nil, err
	}
	var current MarketingFunding
	if err := lockForUpdate(tx).First(&current, task.MarketingFundingID).Error; err != nil {
		return nil, err
	}
	if current.UserID != task.UserId {
		return nil, ErrMarketingConflict
	}
	if current.Revision > 0 {
		err := tx.Model(&task).Updates(map[string]any{"marketing_billing_pending": false, "quota": current.SettledQuota}).Error
		return &current, err
	}
	outcome := task.MarketingBillingData.MarketingOutcome
	if outcome == nil {
		return nil, ErrMarketingReview
	}
	kind := "upstream_terminal"
	if outcome.ActualQuota == nil {
		kind = "unknown"
	}
	funding, err := SettleMarketingConsumption(tx, MarketingSettlementInput{
		UserID: task.UserId, FundingID: task.MarketingFundingID, Version: 1,
		ActualQuota: outcome.ActualQuota, Final: true, EvidenceKind: kind, Evidence: outcome.Evidence,
	}, outcome.Record)
	if err != nil {
		return nil, err
	}
	err = tx.Model(&Midjourney{}).Where("id = ? AND marketing_funding_id = ?", task.Id, funding.ID).
		Updates(map[string]any{"marketing_billing_pending": false, "quota": funding.SettledQuota}).Error
	return funding, err
}
