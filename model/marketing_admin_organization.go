package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// A single catalog mutex orders infrequent administrator configuration writes.
// User requests do not take this lock. Archive state is an administrator-only
// projection, never an eligibility, availability or financial condition.
type MarketingAdminCatalog struct {
	ID int `gorm:"primaryKey;autoIncrement:false"`
}

func (MarketingAdminCatalog) TableName() string { return "marketing_v2_admin_catalog" }

func MarketingAdminDeletableActivities(tx *gorm.DB, rows []MarketingActivity) (map[int64]bool, error) {
	eligible := map[int64]bool{}
	ids := []int64{}
	for _, row := range rows {
		if row.EnabledAt == 0 {
			eligible[row.ID] = true
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return eligible, nil
	}
	for _, entity := range []any{&MarketingActivityProgress{}, &MarketingActivityDecision{}, &MarketingActivityReward{}, &MarketingCheckinDay{}, &MarketingCheckinClaim{}} {
		var used []int64
		if err := tx.Model(entity).Where("activity_id IN (?)", ids).Distinct("activity_id").Pluck("activity_id", &used).Error; err != nil {
			return nil, err
		}
		for _, id := range used {
			delete(eligible, id)
		}
	}
	return eligible, nil
}

// Evaluate a whole page in batches rather than scanning all activity specs for
// every pool. Both ordinary and consecutive check-in rewards can reference it.
func MarketingAdminDeletablePools(tx *gorm.DB, rows []MarketingLotteryPool, now int64) (map[int64]bool, error) {
	eligible := map[int64]bool{}
	ids := []int64{}
	for _, row := range rows {
		if now < row.StartsAt && row.Issued == 0 {
			eligible[row.ID] = true
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return eligible, nil
	}
	var used []int64
	if err := tx.Model(&MarketingLotteryIssue{}).Where("pool_id IN (?)", ids).Distinct("pool_id").Pluck("pool_id", &used).Error; err != nil {
		return nil, err
	}
	for _, id := range used {
		delete(eligible, id)
	}
	var activities []MarketingActivity
	if err := tx.Select("id", "spec").Find(&activities).Error; err != nil {
		return nil, err
	}
	for _, activity := range activities {
		var spec MarketingActivitySpec
		if err := common.UnmarshalJsonStr(activity.Spec, &spec); err != nil {
			return nil, err
		}
		rewards := spec.Rewards
		if spec.Checkin != nil {
			rewards = append([]MarketingActivityRewardSpec{}, rewards...)
			for _, milestone := range spec.Checkin.Milestones {
				rewards = append(rewards, milestone.Rewards...)
			}
		}
		for _, reward := range rewards {
			if reward.Kind == "chance" {
				delete(eligible, reward.PoolID)
			}
		}
	}
	return eligible, nil
}

// Called inside ApplyMarketingAdminRequest, after current authorization, the
// catalog mutex, revision check and object row lock. Deletion is restricted to
// unused objects; actual operation/audit receipts are retained for retries.
func OrganizeMarketingAdmin(tx *gorm.DB, resource string, id int64, action string, now int64) error {
	archivedAt := now
	if action == "unarchive" {
		archivedAt = 0
	}
	if action != "archive" && action != "unarchive" && action != "delete" {
		return ErrMarketingInput
	}
	if resource == "activities" {
		var row MarketingActivity
		if err := lockForUpdate(tx).First(&row, id).Error; err != nil {
			return err
		}
		if action == "delete" {
			allowed, err := MarketingAdminDeletableActivities(tx, []MarketingActivity{row})
			if err != nil {
				return err
			}
			if !allowed[id] {
				return ErrMarketingConflict
			}
			return tx.Delete(&row).Error
		}
		return tx.Model(&row).Updates(map[string]any{"admin_archived_at": archivedAt, "revision": row.Revision + 1}).Error
	}
	if resource != "pools" {
		return ErrMarketingInput
	}
	pool, err := lockMarketingLotteryPool(tx, id)
	if err != nil {
		return err
	}
	if action == "delete" {
		allowed, err := MarketingAdminDeletablePools(tx, []MarketingLotteryPool{*pool}, now)
		if err != nil {
			return err
		}
		if !allowed[id] {
			return ErrMarketingConflict
		}
		// Use a current read, even under MySQL repeatable-read: issuance can commit
		// while this transaction waits for the pool lock, including failed attempts.
		var issues []MarketingLotteryIssue
		if err := lockForUpdate(tx).Where("pool_id = ?", id).Limit(1).Find(&issues).Error; err != nil {
			return err
		}
		if len(issues) != 0 {
			return ErrMarketingConflict
		}
		if err := tx.Where("pool_id = ?", id).Delete(&MarketingLotteryPrize{}).Error; err != nil {
			return err
		}
		return tx.Delete(pool).Error
	}
	return tx.Model(pool).Updates(map[string]any{"admin_archived_at": archivedAt, "revision": pool.Revision + 1}).Error
}
