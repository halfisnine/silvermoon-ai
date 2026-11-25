package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Each version is a complete immutable log projection. Delivery can repeat or
// arrive out of order; the log reader exposes only the newest logical version.
// Financial facts remain in the main database when the log database is down.
type MarketingUsageProjection struct {
	ExportEnabled bool   `gorm:"not null;default:false"`
	ID            int64  `gorm:"primaryKey"`
	FundingID     int64  `gorm:"not null;uniqueIndex:idx_mv2_usage_version,priority:1"`
	Version       int64  `gorm:"not null;uniqueIndex:idx_mv2_usage_version,priority:2"`
	Payload       string `gorm:"type:text;not null"`
	CreatedAt     int64  `gorm:"not null"`
	PublishedAt   int64  `gorm:"not null;index"`
}

func (MarketingUsageProjection) TableName() string { return "marketing_v2_usage_projections" }

// SettleMarketingConsumption atomically records funds and the corresponding log
// snapshot. The caller supplies safe metadata from the original request, never
// a client-selected owner, funding ID, cost, version or source split.
func SettleMarketingConsumption(tx *gorm.DB, input MarketingSettlementInput, record Log) (*MarketingFunding, error) {
	if record.UserId != input.UserID || record.RequestId == "" || record.PromptTokens < 0 || record.CompletionTokens < 0 {
		return nil, ErrMarketingInput
	}
	funding, err := SettleMarketingFunding(tx, input)
	if err != nil {
		return nil, err
	}
	return RecordMarketingFundingUsage(tx, funding, record)
}

// RecordMarketingFundingUsage projects already committed-in-this-transaction
// financial facts without applying settlement again. Realtime uses one logical
// connection log even though its internal allocations belong to multiple rounds.
func RecordMarketingFundingUsage(tx *gorm.DB, funding *MarketingFunding, record Log) (*MarketingFunding, error) {
	if tx == nil || funding == nil || record.UserId != funding.UserID || record.RequestId == "" || record.PromptTokens < 0 || record.CompletionTokens < 0 {
		return nil, ErrMarketingInput
	}
	if _, _, err := lockMarketingAccount(tx.Unscoped(), funding.UserID); err != nil {
		return nil, err
	}
	var err error
	var existing MarketingUsageProjection
	err = lockForUpdate(tx).Where("funding_id = ? AND version = ?", funding.ID, funding.Revision).First(&existing).Error
	if err == nil {
		return funding, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var current MarketingFunding
	if err = lockForUpdate(tx).First(&current, funding.ID).Error; err != nil {
		return nil, err
	}
	if current.UserID != funding.UserID {
		return nil, ErrMarketingAccess
	}
	if current.Revision != funding.Revision {
		return nil, ErrMarketingConflict
	}
	funding = &current
	var allocations []MarketingAllocation
	if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Find(&allocations).Error; err != nil {
		return nil, err
	}
	var paid, gift int64
	sources := make([]map[string]int64, 0, len(allocations))
	sourceIndexes := map[int64]int{}
	for _, allocation := range allocations {
		if allocation.ConsumedQuota < 0 || allocation.ConsumedQuota > common.MaxWalletQuota-paid-gift {
			return nil, ErrMarketingReview
		}
		if allocation.GiftID == 0 {
			paid += allocation.ConsumedQuota
		} else {
			gift += allocation.ConsumedQuota
			if allocation.ConsumedQuota > 0 {
				if index, exists := sourceIndexes[allocation.GiftID]; exists {
					sources[index]["quota"] += allocation.ConsumedQuota
				} else {
					sourceIndexes[allocation.GiftID] = len(sources)
					sources = append(sources, map[string]int64{"gift_id": allocation.GiftID, "quota": allocation.ConsumedQuota})
				}
			}
		}
	}
	if paid+gift != funding.SettledQuota {
		return nil, ErrMarketingReview
	}
	metadata := map[string]any{}
	if record.Other != "" {
		if err = common.UnmarshalJsonStr(record.Other, &metadata); err != nil {
			return nil, err
		}
		if metadata == nil {
			metadata = map[string]any{}
		}
	}
	metadata["funding"] = map[string]any{
		"state": funding.State, "cost_known": funding.CostKnown,
		"paid_quota": paid, "gift_quota": gift, "gift_sources": sources,
		"actual_quota": funding.ActualQuota, "settled_quota": funding.SettledQuota,
	}
	other, err := common.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	record.Id = 0
	record.Type = LogTypeConsume
	record.CreatedAt = funding.QualifiedAt
	record.UsageEventID = funding.RequestKey
	record.UsageRevision = funding.Revision
	record.Other = string(other)
	record.Quota, err = common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(funding.SettledQuota))
	if err != nil {
		return nil, err
	}
	payload, err := common.Marshal(record)
	if err != nil {
		return nil, err
	}
	var prior MarketingUsageProjection
	err = lockForUpdate(tx).Where("funding_id = ? AND version < ?", funding.ID, funding.Revision).Order("version DESC").First(&prior).Error
	first := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !first {
		return nil, err
	}
	previousQuota := 0
	exportEnabled := common.DataExportEnabled && common.LogConsumeEnabled
	if !first {
		var previousRecord Log
		if err = common.UnmarshalJsonStr(prior.Payload, &previousRecord); err != nil {
			return nil, err
		}
		previousQuota = previousRecord.Quota
		exportEnabled = prior.ExportEnabled
	}
	var contribution QuotaData
	if exportEnabled && !first {
		if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).First(&contribution).Error; err != nil {
			return nil, err
		}
	}
	if int64(record.PromptTokens) > common.MaxWalletQuota-int64(record.CompletionTokens) {
		return nil, ErrMarketingInput
	}
	contribution.FundingID = &funding.ID
	contribution.UserID = funding.UserID
	contribution.Username = record.Username
	contribution.ModelName = record.ModelName
	contribution.CreatedAt = record.CreatedAt - record.CreatedAt%3600
	contribution.UseGroup = record.Group
	contribution.TokenID = record.TokenId
	contribution.ChannelID = record.ChannelId
	contribution.Count = 1
	contribution.Quota = record.Quota
	contribution.TokenUsed = record.PromptTokens + record.CompletionTokens
	var details struct {
		CacheRead  int64 `json:"cache_tokens"`
		CacheWrite int64 `json:"cache_write_tokens"`
		Root       struct {
			NodeName string `json:"node_name"`
		} `json:"root_info"`
	}
	if err = common.Unmarshal(other, &details); err != nil {
		return nil, err
	}
	if details.CacheRead < 0 || details.CacheRead > common.MaxWalletQuota || details.CacheWrite < 0 || details.CacheWrite > common.MaxWalletQuota {
		return nil, ErrMarketingInput
	}
	contribution.CacheReadTokens = details.CacheRead
	contribution.CacheCreationTokens = details.CacheWrite
	if first {
		contribution.NodeName = details.Root.NodeName
		if contribution.NodeName == "" {
			contribution.NodeName = common.NodeName
		}
	}
	if exportEnabled {
		if err = tx.Save(&contribution).Error; err != nil {
			return nil, err
		}
	}
	delta := record.Quota - previousQuota
	updates := map[string]any{"used_quota": gorm.Expr("used_quota + ?", delta)}
	if first {
		updates["request_count"] = gorm.Expr("request_count + 1")
	}
	if err = tx.Unscoped().Model(&User{}).Where("id = ?", funding.UserID).Updates(updates).Error; err != nil {
		return nil, err
	}
	if record.ChannelId > 0 && delta != 0 {
		if err = tx.Model(&Channel{}).Where("id = ?", record.ChannelId).Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error; err != nil {
			return nil, err
		}
	}
	projection := MarketingUsageProjection{ExportEnabled: exportEnabled, FundingID: funding.ID, Version: funding.Revision, Payload: string(payload), CreatedAt: record.CreatedAt}
	if !common.LogConsumeEnabled {
		// Disabled at the time of consumption stays disabled on recovery too.
		// Financial facts and counters remain durable in the business database.
		projection.PublishedAt = -1
	}
	if err = tx.Create(&projection).Error; err != nil {
		return nil, err
	}
	return funding, nil
}

// PublishMarketingUsage may be invoked by several instances. The immutable
// payload and versioned log relation make a lost acknowledgement safe to retry.
func PublishMarketingUsage(projection MarketingUsageProjection) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var funding MarketingFunding
		if err := tx.First(&funding, projection.FundingID).Error; err != nil {
			return err
		}
		// A per-account publication lock orders cleanup against delivery, without
		// serializing unrelated accounts. Cleanup commits its cutoff before it
		// deletes from the independent log store.
		var account MarketingAccount
		if err := lockForUpdate(tx).First(&account, "user_id = ?", funding.UserID).Error; err != nil {
			return err
		}
		var current MarketingUsageProjection
		if err := lockForUpdate(tx).First(&current, projection.ID).Error; err != nil {
			return err
		}
		if current.PublishedAt != 0 {
			return nil
		}
		if current.FundingID != funding.ID {
			return ErrMarketingInput
		}
		var record Log
		if err := common.UnmarshalJsonStr(current.Payload, &record); err != nil {
			return err
		}
		if record.UsageEventID != funding.RequestKey || record.UserId != funding.UserID || record.UsageRevision != current.Version {
			return ErrMarketingInput
		}
		publishedAt := int64(-1)
		now := common.GetTimestamp()
		expired := false
		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			if days := clickHouseLogTTLDays(); days > 0 && record.CreatedAt <= now {
				expired = (now-record.CreatedAt)/86400 >= int64(days)
			}
		}
		if record.CreatedAt >= account.UsageRetainedFrom && !expired {
			logDB := LOG_DB
			mainPool, err := tx.DB()
			if err != nil {
				return err
			}
			logPool, err := LOG_DB.DB()
			if err != nil {
				return err
			}
			if mainPool == logPool {
				logDB = tx
			}
			if err := logDB.Create(&record).Error; err != nil {
				return err
			}
			publishedAt = common.GetTimestamp()
		}
		return tx.Model(&current).Update("published_at", publishedAt).Error
	})
}

// AdvanceMarketingUsageRetention also runs when the log store currently has
// zero matching rows: unpublished records still belong to the cleanup scope.
func AdvanceMarketingUsageRetention(ctx context.Context, before int64) error {
	return DB.WithContext(ctx).Model(&MarketingAccount{}).
		Where("usage_retained_from < ?", before).Update("usage_retained_from", before).Error
}

func RecoverMarketingUsage(ctx context.Context, limit int) error {
	if limit <= 0 || limit > 1000 {
		return ErrMarketingInput
	}
	var lastID int64
	var firstErr error
	for {
		var pending []MarketingUsageProjection
		if err := DB.WithContext(ctx).Where("published_at = 0 AND id > ?", lastID).Order("id").Limit(limit).Find(&pending).Error; err != nil {
			return err
		}
		for _, projection := range pending {
			if err := ctx.Err(); err != nil {
				return err
			}
			lastID = projection.ID
			if err := PublishMarketingUsage(projection); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if len(pending) < limit {
			return firstErr
		}
	}
}

func HasPendingMarketingRecovery() bool {
	var taskID int64
	err := DB.Model(&Task{}).Where("marketing_billing_pending = ? AND status IN ?", true, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Limit(1).Pluck("id", &taskID).Error
	if err != nil {
		common.SysError("marketing task recovery lookup failed: " + err.Error())
		return false
	}
	if taskID != 0 {
		return true
	}
	var projectionID int64
	err = DB.Model(&MarketingUsageProjection{}).Where("published_at = 0").Limit(1).Pluck("id", &projectionID).Error
	if err != nil {
		common.SysError("marketing usage recovery lookup failed: " + err.Error())
		return false
	}
	return projectionID != 0
}

// Reward obligations are separate from pending usage: completing a model task
// must not imply that unrelated registration or payment rewards are complete.
func HasPendingMarketingRewardRecovery() bool {
	users, err := PendingMarketingRewardSources(context.Background(), 0, 1)
	if err != nil {
		common.SysError("marketing reward recovery lookup failed: " + err.Error())
		return false
	}
	if len(users) != 0 {
		return true
	}
	draws, err := PendingMarketingLotteryDraws(context.Background(), 0, 1)
	if err != nil {
		common.SysError("marketing lottery recovery lookup failed: " + err.Error())
		return false
	}
	return len(draws) != 0
}
