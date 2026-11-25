package model

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var marketingAdminOperationKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// AuthorizeMarketingAdmin checks current database authorization within the
// transaction that reads or changes marketing data, including request replays.
func AuthorizeMarketingAdmin(tx *gorm.DB, actorID int) error {
	return lockMarketingLotteryActor(tx, actorID, true)
}

// ApplyMarketingAdminRequest binds a stable browser operation to its original
// input before evaluating current conversion settings. The domain operation
// remains responsible for stock, time, immutable rules and financial safety.
// Existing operation tables hold the HTTP envelope; no second reward ledger is
// created. Authorization always precedes the replay shortcut.
func ApplyMarketingAdminRequest(tx *gorm.DB, actorID int, pool bool, reference, action string, id, revision int64, input any, now int64, apply func(string) (int64, error)) (int64, error) {
	if !marketingAdminOperationKey.MatchString(reference) || revision < 0 || id < 0 || now <= 0 {
		return 0, ErrMarketingInput
	}
	if err := AuthorizeMarketingAdmin(tx, actorID); err != nil {
		return 0, err
	}
	// Serialize configuration writes across administrators/instances. This lock
	// is never acquired by user participation or billing. In particular, deleting
	// an unused pool cannot race an activity being configured to reference it.
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MarketingAdminCatalog{ID: 1}).Error; err != nil {
		return 0, err
	}
	var catalog MarketingAdminCatalog
	if err := lockForUpdate(tx).First(&catalog, 1).Error; err != nil {
		return 0, err
	}
	key := fmt.Sprintf("http:%d:%s", actorID, reference)
	envelope := struct {
		Action       string
		ID, Revision int64
		Input        any
	}{action, id, revision, input}
	if pool {
		op, replay, err := marketingLotteryOperation(tx, actorID, key, "http", envelope)
		if err != nil {
			return 0, err
		}
		if replay {
			return op.PoolID, nil
		}
		if id > 0 {
			current, err := lockMarketingLotteryPool(tx, id)
			if err != nil {
				return 0, err
			}
			if current.Revision != revision {
				return 0, ErrMarketingConflict
			}
		}
		result, err := apply("domain:" + key)
		if err != nil {
			return 0, err
		}
		op.PoolID, op.RecordedAt = result, now
		return result, tx.Create(op).Error
	}
	op, replay, err := marketingActivityOperation(tx, actorID, key, "http", envelope)
	if err != nil {
		return 0, err
	}
	if replay {
		return op.ActivityID, nil
	}
	if id > 0 {
		var current MarketingActivity
		if err := lockForUpdate(tx).First(&current, id).Error; err != nil {
			return 0, err
		}
		if current.Revision != revision {
			return 0, ErrMarketingConflict
		}
	}
	result, err := apply("domain:" + key)
	if err != nil {
		return 0, err
	}
	op.ActivityID, op.RecordedAt = result, now
	return result, tx.Create(op).Error
}

type MarketingAdminFilter struct {
	Page, PageSize                          int
	Keyword, Kind, ExclusiveGroup, Username string
	Archive                                 string
	UserID                                  int
	ActivityID, StartsAt, EndsAt            int64
}

func (f MarketingAdminFilter) Validate() error {
	if (f.Archive != "" && f.Archive != "active" && f.Archive != "archived" && f.Archive != "all") || f.Page < 1 || f.Page > 1000000 || f.PageSize < 1 || f.PageSize > 100 || len(f.Username) > 64 || len(f.Keyword) > 160 || len(f.ExclusiveGroup) > 64 || f.UserID < 0 || f.ActivityID < 0 || f.StartsAt < 0 || f.EndsAt < 0 || (f.EndsAt > 0 && f.EndsAt < f.StartsAt) {
		return ErrMarketingInput
	}
	return nil
}
func ListMarketingAdminActivities(tx *gorm.DB, f MarketingAdminFilter) ([]MarketingActivity, int64, error) {
	if err := f.Validate(); err != nil {
		return nil, 0, err
	}
	q := tx.Model(&MarketingActivity{})
	if f.Archive == "" || f.Archive == "active" {
		q = q.Where("admin_archived_at = ?", 0)
	}
	if f.Archive == "archived" {
		q = q.Where("admin_archived_at > ?", 0)
	}
	if f.ExclusiveGroup != "" {
		q = q.Where("exclusive_group = ?", f.ExclusiveGroup)
		// Keep the indexed equality prefilter, then apply the same byte-exact
		// identity that the rule engine uses under MySQL's CI collations.
		if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
			q = q.Where("CAST(exclusive_group AS BINARY) = CAST(? AS BINARY)", f.ExclusiveGroup)
		}
	}
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.Keyword != "" {
		q = q.Where("name LIKE ?", "%"+strings.ReplaceAll(f.Keyword, "%", "\\%")+"%")
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	var rows []MarketingActivity
	err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error
	return rows, count, err
}
func ListMarketingAdminPools(tx *gorm.DB, f MarketingAdminFilter) ([]MarketingLotteryPool, int64, error) {
	if err := f.Validate(); err != nil {
		return nil, 0, err
	}
	q := tx.Model(&MarketingLotteryPool{})
	if f.Archive == "" || f.Archive == "active" {
		q = q.Where("admin_archived_at = ?", 0)
	}
	if f.Archive == "archived" {
		q = q.Where("admin_archived_at > ?", 0)
	}
	if f.Keyword != "" {
		q = q.Where("name LIKE ?", "%"+strings.ReplaceAll(f.Keyword, "%", "\\%")+"%")
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	var rows []MarketingLotteryPool
	err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error
	return rows, count, err
}

// Older development snapshots did not have the indexed group projection. This
// bounded, idempotent backfill reads only the new system's authoritative spec;
// it does not import or reinterpret any old marketing records.
func BackfillMarketingAdminGroups(db *gorm.DB) error {
	var after int64
	for {
		var rows []MarketingActivity
		if err := db.Where("id > ? AND exclusive_group = ?", after, "").Order("id").Limit(100).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			after = row.ID
			var spec MarketingActivitySpec
			if err := common.UnmarshalJsonStr(row.Spec, &spec); err != nil {
				return err
			}
			if spec.ExclusiveGroup != "" {
				if err := db.Model(&MarketingActivity{}).Where("id = ? AND spec = ? AND exclusive_group = ?", row.ID, row.Spec, "").Update("exclusive_group", spec.ExclusiveGroup).Error; err != nil {
					return err
				}
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

type MarketingAdminGroup struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

func ListMarketingAdminGroups(tx *gorm.DB, f MarketingAdminFilter) ([]MarketingAdminGroup, int64, error) {
	if err := f.Validate(); err != nil {
		return nil, 0, err
	}
	q := tx.Model(&MarketingActivity{}).Where("exclusive_group <> ?", "")
	if f.Keyword != "" {
		q = q.Where("exclusive_group LIKE ?", "%"+f.Keyword+"%")
	}
	// SQL group identity must match Go string identity. MySQL's usual Unicode
	// collation otherwise merges different activity groups (case or accents).
	groupColumn := "exclusive_group"
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		groupColumn = "CAST(exclusive_group AS BINARY)"
	}
	var count int64
	if err := q.Select("COUNT(DISTINCT " + groupColumn + ")").Scan(&count).Error; err != nil {
		return nil, 0, err
	}
	rows := []MarketingAdminGroup{}
	err := q.Select(groupColumn + " AS name, COUNT(*) AS count").Group(groupColumn).Order(groupColumn).Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Scan(&rows).Error
	return rows, count, err
}
