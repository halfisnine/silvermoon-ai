package model

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMarketingLottery(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := "local"
			if dialect != "sqlite" {
				dsn = os.Getenv("MV2_" + strings.ToUpper(dialect) + "_DSN")
			}
			if dsn == "" {
				if os.Getenv("MV2_REQUIRE_DATABASES") == "1" {
					t.Fatal("required database missing: " + dialect)
				}
				t.Skip("matrix database not configured")
			}
			if dialect != "sqlite" {
				require.Contains(t, dsn, "/marketing_v2_behavior")
			}
			oldDB, oldType, oldPath := DB, common.MainDatabaseType(), common.SQLitePath
			common.SQLitePath = filepath.Join(t.TempDir(), "lottery.sqlite")
			t.Cleanup(func() { DB = oldDB; common.SetMainDatabaseType(oldType); common.SQLitePath = oldPath; initCol() })
			t.Setenv("MV2_TEST_DSN", dsn)
			db, dbType, err := chooseDB("MV2_TEST_DSN", false)
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
			DB = db
			common.SetMainDatabaseType(dbType)
			initCol()
			require.NoError(t, db.AutoMigrate(&User{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				users := db.Unscoped().Model(&User{}).Select("id").Where("username LIKE ?", "mv2lot_%")
				pools := db.Model(&MarketingLotteryPool{}).Select("id").Where("name LIKE ?", "mv2lot_%")
				for _, record := range []any{&MarketingLotteryAward{}, &MarketingLotteryDraw{}, &MarketingLotteryChance{}, &MarketingLotteryIssue{}, &MarketingGift{}, &MarketingLedgerEntry{}, &MarketingAccount{}} {
					require.NoError(t, db.Where("user_id IN (?)", users).Delete(record).Error)
				}
				require.NoError(t, db.Where("actor_id IN (?)", users).Delete(&MarketingLotteryOperation{}).Error)
				require.NoError(t, db.Where("pool_id IN (?)", pools).Delete(&MarketingLotteryPrize{}).Error)
				require.NoError(t, db.Where("name LIKE ?", "mv2lot_%").Delete(&MarketingLotteryPool{}).Error)
				require.NoError(t, db.Unscoped().Where("username LIKE ?", "mv2lot_%").Delete(&User{}).Error)
			}
			cleanup()
			t.Cleanup(cleanup)
			admin := User{Username: "mv2lot_admin", AffCode: "mv2lot_admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
			user := User{Username: "mv2lot_user", AffCode: "mv2lot_user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			other := User{Username: "mv2lot_other", AffCode: "mv2lot_other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&admin).Error)
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&other).Error)
			money := MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: "50", CurrencyPerUnit: "1", Currency: "CNY"}
			spec := MarketingLotteryPoolSpec{Name: "mv2lot_combination", Style: "wheel", StartsAt: 100, EndsAt: 1000, Capacity: 2, Prizes: []MarketingLotteryPrizeSpec{{Name: "combination", Weight: 1, Stock: 2, Gifts: []MarketingLotteryGiftSpec{{Money: money, ValidSeconds: 10}, {Money: money}}}, {Name: "zero weight", Weight: 0, Stock: -1, Gifts: []MarketingLotteryGiftSpec{{Money: money}}}}}
			var pool *MarketingLotteryPool
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				pool, err = CreateMarketingLotteryPool(tx, admin.Id, "lot:create", spec, 90)
				return err
			}))
			var accountCount int64
			require.NoError(t, db.Model(&MarketingAccount{}).Where("user_id = ?", admin.Id).Count(&accountCount).Error)
			assert.Zero(t, accountCount, "configuration must not activate an administrator wallet")
			require.ErrorIs(t, func() error { _, err := CreateMarketingLotteryPool(db, admin.Id, "outside", spec, 90); return err }(), ErrMarketingTransaction)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := CreateMarketingLotteryPool(tx, admin.Id, "lot:create", spec, 1100)
				return err
			}))
			require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleCommonUser).Error)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := CreateMarketingLotteryPool(tx, admin.Id, "lot:create", spec, 90)
				return err
			}), ErrMarketingAccess)
			require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
			var shortage *MarketingLotteryIssue
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				shortage, err = IssueMarketingLotteryChances(tx, user.Id, pool.ID, "lot:shortage", 3, 95)
				return err
			}))
			assert.Equal(t, "shortage", shortage.State)
			var issue *MarketingLotteryIssue
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				issue, err = IssueMarketingLotteryChances(tx, user.Id, pool.ID, "lot:issue", 2, 95)
				return err
			}))
			assert.Equal(t, "issued", issue.State)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := UpdateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:update", spec, 96)
				return err
			}), ErrMarketingConflict)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:badappend", "append", 0, 1, 96)
				return err
			}), ErrMarketingInsufficient)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := DrawMarketingLotteryChance(tx, other.Id, pool.ID, "lot:wrong-owner", 110)
				return err
			}), ErrMarketingAccess)
			var draw *MarketingLotteryDraw
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				draw, err = DrawMarketingLotteryChance(tx, user.Id, pool.ID, "lot:draw", 110)
				return err
			}))
			assert.Equal(t, "pending", draw.State)
			var awards []MarketingLotteryAward
			require.NoError(t, db.Where("draw_id = ?", draw.ID).Order("position").Find(&awards).Error)
			require.Len(t, awards, 2)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				hidden, items, err := GetMarketingLotteryDraw(tx, other.Id, draw.ID)
				assert.Nil(t, hidden)
				assert.Nil(t, items)
				return err
			}), gorm.ErrRecordNotFound)
			require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				hidden, items, err := GetMarketingLotteryDraw(tx, user.Id, draw.ID)
				assert.Nil(t, hidden)
				assert.Nil(t, items)
				return err
			}), ErrMarketingAccess)
			require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusEnabled).Error)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				again, err := DrawMarketingLotteryChance(tx, user.Id, pool.ID, "lot:draw", 111)
				if err == nil {
					assert.Equal(t, draw.ID, again.ID)
				}
				return err
			}))
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := DrawMarketingLotteryChance(tx, other.Id, pool.ID, "lot:draw", 111)
				return err
			}), ErrMarketingConflict)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := FulfillMarketingLotteryAward(tx, other.Id, awards[0].ID, 120)
				return err
			}), ErrMarketingAccess)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := FulfillMarketingLotteryAward(tx, user.Id, awards[0].ID, 120)
				return err
			}))
			require.NoError(t, db.First(draw, draw.ID).Error)
			assert.Equal(t, "pending", draw.State)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				snapshot, items, err := GetMarketingLotteryDraw(tx, user.Id, draw.ID)
				if err != nil {
					return err
				}
				assert.Equal(t, "pending", snapshot.State)
				require.Len(t, items, 2)
				assert.Equal(t, awards[0].ID, items[0].ID)
				assert.Equal(t, "credited", items[0].State)
				assert.Positive(t, items[0].GiftID)
				assert.EqualValues(t, 120, items[0].CreditedAt)
				assert.Equal(t, awards[1].ID, items[1].ID)
				assert.Equal(t, "pending", items[1].State)
				assert.Zero(t, items[1].GiftID)
				assert.Zero(t, items[1].CreditedAt)
				return nil
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := FulfillMarketingLotteryAward(tx, user.Id, awards[0].ID, 999)
				return err
			}))
			var firstGift MarketingGift
			require.NoError(t, db.Where("source_reference = ?", "lottery-award:"+strconv.FormatInt(awards[0].ID, 10)).First(&firstGift).Error)
			assert.EqualValues(t, 120, firstGift.GrantedAt)
			assert.EqualValues(t, 130, firstGift.ExpiresAt)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := FulfillMarketingLotteryAward(tx, user.Id, awards[1].ID, 140)
				return err
			}))
			require.NoError(t, db.First(draw, draw.ID).Error)
			assert.Equal(t, "complete", draw.State)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				snapshot, items, err := GetMarketingLotteryDraw(tx, user.Id, draw.ID)
				if err != nil {
					return err
				}
				assert.Equal(t, "complete", snapshot.State)
				require.Len(t, items, 2)
				for _, item := range items {
					assert.Equal(t, "credited", item.State)
					assert.Positive(t, item.GiftID)
				}
				assert.Equal(t, firstGift.ID, items[0].GiftID)
				assert.EqualValues(t, 120, items[0].CreditedAt)
				assert.EqualValues(t, 140, items[1].CreditedAt)
				return nil
			}))
			var gifts []MarketingGift
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifts).Error)
			require.Len(t, gifts, 2)
			assert.EqualValues(t, 100, gifts[0].GrantedQuota)
			var unchanged User
			require.NoError(t, db.First(&unchanged, user.Id).Error)
			assert.Zero(t, unchanged.Quota)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:badappend2", "append", 0, 1, 150)
				return err
			}), ErrMarketingInsufficient, "remaining opportunity is already promised")
			var prize MarketingLotteryPrize
			require.NoError(t, db.Where("pool_id = ? AND weight = ?", pool.ID, 1).First(&prize).Error)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:restock", "restock", prize.ID, 3, 150)
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:append", "append", 0, 3, 150)
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				again, err := IssueMarketingLotteryChances(tx, user.Id, pool.ID, "lot:shortage", 3, 150)
				if err == nil {
					assert.Equal(t, "shortage", again.State)
				}
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:pause", "pause", 0, 0, 151)
				return err
			}))
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := DrawMarketingLotteryChance(tx, user.Id, pool.ID, "lot:paused", 160)
				return err
			}), ErrMarketingAccess)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:extend", "extend", 0, 2000, 160)
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:resume", "resume", 0, 0, 161)
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := DrawMarketingLotteryChance(tx, user.Id, pool.ID, "lot:clear-pending", 170)
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := IssueMarketingLotteryChances(tx, user.Id, pool.ID, "lot:new", 3, 170)
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return ClearMarketingLotteryChances(tx, admin.Id, user.Id, "lot:clear", 180) }))
			var cleared []MarketingLotteryChance
			require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&cleared).Error)
			require.Len(t, cleared, 2)
			assert.EqualValues(t, 3, cleared[1].Cleared)
			assert.Zero(t, cleared[1].Remaining)
			var pendingAwards []MarketingLotteryAward
			require.NoError(t, db.Where("user_id = ? AND state = ?", user.Id, "cleared").Find(&pendingAwards).Error)
			require.Len(t, pendingAwards, 2)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := FulfillMarketingLotteryAward(tx, user.Id, pendingAwards[0].ID, 190)
				return err
			}), ErrMarketingAccess)
			require.NoError(t, db.First(pool, pool.ID).Error)
			assert.EqualValues(t, 5, pool.Issued)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:extend-cleared", "extend", 0, 3000, 190)
				return err
			}))
			require.NoError(t, db.First(&cleared[1], cleared[1].ID).Error)
			assert.EqualValues(t, 2000, cleared[1].ExpiresAt)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "lot:expired-extend", "extend", 0, 4000, 3000)
				return err
			}), ErrMarketingInput)

			t.Run("eligible_supply_empty_and_large_batch", func(t *testing.T) {
				emptySpec := MarketingLotteryPoolSpec{Name: "mv2lot_empty", Style: "blind_box", StartsAt: 100, EndsAt: 1000, Capacity: 1, Prizes: []MarketingLotteryPrizeSpec{{Name: "empty", Weight: 1, Stock: -1}, {Name: "unreachable", Weight: 0, Stock: -1, Gifts: []MarketingLotteryGiftSpec{{Money: money}}}}}
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreateMarketingLotteryPool(tx, admin.Id, "lot:bad-empty", emptySpec, 90)
					return err
				}), ErrMarketingInsufficient)
				emptySpec.Prizes[1].Weight = 1
				emptySpec.Capacity = 1000000000
				var unlimited *MarketingLotteryPool
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					unlimited, err = CreateMarketingLotteryPool(tx, admin.Id, "lot:unlimited", emptySpec, 90)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := IssueMarketingLotteryChances(tx, user.Id, unlimited.ID, "lot:huge", emptySpec.Capacity, 100)
					return err
				}))
				var batches int64
				require.NoError(t, db.Model(&MarketingLotteryChance{}).Where("pool_id = ?", unlimited.ID).Count(&batches).Error)
				assert.EqualValues(t, 1, batches)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := DrawMarketingLotteryChance(tx, user.Id, unlimited.ID, "lot:exact-expiry", 1000)
					return err
				}), ErrMarketingAccess)
				require.NoError(t, db.First(unlimited, unlimited.ID).Error)
				assert.EqualValues(t, 1000000000, unlimited.Issued)
			})
			t.Run("paused_pool_issues_but_cannot_draw_or_extend_lifetime_implicitly", func(t *testing.T) {
				pausedSpec := MarketingLotteryPoolSpec{Name: "mv2lot_paused", Style: "wheel", StartsAt: 100, EndsAt: 1000, Capacity: 2, Prizes: []MarketingLotteryPrizeSpec{{Name: "gift", Weight: 1, Stock: 2, Gifts: []MarketingLotteryGiftSpec{{Money: money}}}}}
				var pausedPool *MarketingLotteryPool
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					pausedPool, err = CreateMarketingLotteryPool(tx, admin.Id, "lot:pause-pool", pausedSpec, 90)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, pausedPool.ID, "lot:pause-before-issue", "pause", 0, 0, 101)
					return err
				}))
				var issued *MarketingLotteryIssue
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					issued, err = IssueMarketingLotteryChances(tx, user.Id, pausedPool.ID, "lot:issued-while-paused", 2, 102)
					return err
				}))
				assert.Equal(t, "issued", issued.State)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					shortage, err := IssueMarketingLotteryChances(tx, user.Id, pausedPool.ID, "lot:paused-shortage", 1, 103)
					if err == nil {
						assert.Equal(t, "shortage", shortage.State)
					}
					return err
				}))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := DrawMarketingLotteryChance(tx, user.Id, pausedPool.ID, "lot:paused-draw-attempt", 104)
					return err
				}), ErrMarketingAccess)
				var batch MarketingLotteryChance
				require.NoError(t, db.Where("issue_id = ?", issued.ID).First(&batch).Error)
				assert.EqualValues(t, 2, batch.Remaining)
				assert.EqualValues(t, 1000, batch.ExpiresAt)
				var draws int64
				require.NoError(t, db.Model(&MarketingLotteryDraw{}).Where("pool_id = ?", pausedPool.ID).Count(&draws).Error)
				assert.Zero(t, draws)
				require.NoError(t, db.First(pausedPool, pausedPool.ID).Error)
				assert.EqualValues(t, 2, pausedPool.Issued)
				assert.True(t, pausedPool.Paused)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, pausedPool.ID, "lot:resume-issued-pool", "resume", 0, 0, 105)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := DrawMarketingLotteryChance(tx, user.Id, pausedPool.ID, "lot:paused-draw-attempt", 106)
					return err
				}))
				require.NoError(t, db.First(&batch, batch.ID).Error)
				assert.EqualValues(t, 1, batch.Remaining)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, pausedPool.ID, "lot:pause-until-expiry", "pause", 0, 0, 107)
					return err
				}))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := DrawMarketingLotteryChance(tx, user.Id, pausedPool.ID, "lot:paused-expired-draw", 1000)
					return err
				}), ErrMarketingAccess)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := IssueMarketingLotteryChances(tx, user.Id, pausedPool.ID, "lot:paused-expired-issue", 1, 1000)
					return err
				}), ErrMarketingAccess)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, pausedPool.ID, "lot:resume-after-expiry", "resume", 0, 0, 1001)
					return err
				}))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := DrawMarketingLotteryChance(tx, user.Id, pausedPool.ID, "lot:paused-expired-draw", 1001)
					return err
				}), ErrMarketingAccess)
				require.NoError(t, db.First(&batch, batch.ID).Error)
				assert.EqualValues(t, 1, batch.Remaining)
				assert.EqualValues(t, 1000, batch.ExpiresAt)
				require.NoError(t, db.First(pausedPool, pausedPool.ID).Error)
				assert.EqualValues(t, 2, pausedPool.Issued)
			})

			t.Run("empty_result_and_exhausted_prize", func(t *testing.T) {
				originalRandom := rand.Reader
				t.Cleanup(func() { rand.Reader = originalRandom })
				// Deterministic server entropy for boundary tests, never a product draw input.
				rand.Reader = bytes.NewReader(make([]byte, 64))
				emptySpec := MarketingLotteryPoolSpec{Name: "mv2lot_empty_draw", Style: "blind_box", StartsAt: 100, EndsAt: 1000, Capacity: 2, Prizes: []MarketingLotteryPrizeSpec{{Name: "empty", Weight: 1, Stock: -1}, {Name: "real", Weight: 1, Stock: 2, Gifts: []MarketingLotteryGiftSpec{{Money: money}}}}}
				var emptyPool *MarketingLotteryPool
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					emptyPool, err = CreateMarketingLotteryPool(tx, admin.Id, "lot:empty-draw", emptySpec, 90)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := IssueMarketingLotteryChances(tx, user.Id, emptyPool.ID, "lot:empty-issue", 2, 100)
					return err
				}))
				var emptyDraw *MarketingLotteryDraw
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					emptyDraw, err = DrawMarketingLotteryChance(tx, user.Id, emptyPool.ID, "lot:empty-result", 101)
					return err
				}))
				assert.Equal(t, "complete", emptyDraw.State)
				var awardCount int64
				require.NoError(t, db.Model(&MarketingLotteryAward{}).Where("draw_id = ?", emptyDraw.ID).Count(&awardCount).Error)
				assert.Zero(t, awardCount)
				require.NoError(t, db.First(emptyPool, emptyPool.ID).Error)
				assert.EqualValues(t, 2, emptyPool.Issued)
				// Empty draws free physical supply, but only explicit addition reissues it.
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, emptyPool.ID, "lot:empty-add", "append", 0, 1, 102)
					return err
				}))
				rand.Reader = bytes.NewReader(nil)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := DrawMarketingLotteryChance(tx, user.Id, emptyPool.ID, "lot:entropy-failed", 103)
					return err
				}))
				var remaining MarketingLotteryChance
				require.NoError(t, db.Where("pool_id = ?", emptyPool.ID).First(&remaining).Error)
				assert.EqualValues(t, 1, remaining.Remaining)
				var failures int64
				require.NoError(t, db.Model(&MarketingLotteryDraw{}).Where("pool_id = ?", emptyPool.ID).Count(&failures).Error)
				assert.EqualValues(t, 1, failures)
				rand.Reader = bytes.NewReader(make([]byte, 64))
				finiteSpec := MarketingLotteryPoolSpec{Name: "mv2lot_finite", Style: "wheel", StartsAt: 100, EndsAt: 1000, Capacity: 2, Prizes: []MarketingLotteryPrizeSpec{{Name: "grand", Weight: 1, Stock: 1, Gifts: []MarketingLotteryGiftSpec{{Money: money}}}, {Name: "small", Weight: 1, Stock: 1, Gifts: []MarketingLotteryGiftSpec{{Money: money}}}}}
				var finitePool *MarketingLotteryPool
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					finitePool, err = CreateMarketingLotteryPool(tx, admin.Id, "lot:finite", finiteSpec, 90)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := IssueMarketingLotteryChances(tx, user.Id, finitePool.ID, "lot:finite-issue", 2, 100)
					return err
				}))
				var first, second *MarketingLotteryDraw
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					first, err = DrawMarketingLotteryChance(tx, user.Id, finitePool.ID, "lot:finite-first", 101)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					second, err = DrawMarketingLotteryChance(tx, user.Id, finitePool.ID, "lot:finite-second", 102)
					return err
				}))
				assert.NotEqual(t, first.PrizeID, second.PrizeID)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, finitePool.ID, "lot:pending-overissue", "append", 0, 1, 103)
					return err
				}), ErrMarketingInsufficient)
				// A wallet limit is a recoverable delivery failure, not another draw.
				var responsibilities []MarketingLotteryAward
				require.NoError(t, db.Where("draw_id = ?", first.ID).Find(&responsibilities).Error)
				require.Len(t, responsibilities, 1)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := GrantMarketingGift(tx, MarketingGiftInput{UserID: other.Id, Kind: "admin_gift", Reference: "lot:wallet-cap", GrantedAt: 100, Money: MarketingMoneySnapshot{Amount: "9007199254740991", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
					return err
				}))
				// Use a dedicated winning draw owned by the capped user.
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, finitePool.ID, "lot:cap-restock", "restock", first.PrizeID, 1, 104)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, finitePool.ID, "lot:cap-append", "append", 0, 1, 104)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := IssueMarketingLotteryChances(tx, other.Id, finitePool.ID, "lot:cap-issue", 1, 104)
					return err
				}))
				var capped *MarketingLotteryDraw
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					capped, err = DrawMarketingLotteryChance(tx, other.Id, finitePool.ID, "lot:capped", 105)
					return err
				}))
				var responsibility MarketingLotteryAward
				require.NoError(t, db.Where("draw_id = ?", capped.ID).First(&responsibility).Error)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := FulfillMarketingLotteryAward(tx, other.Id, responsibility.ID, 106)
					return err
				}))
				require.NoError(t, db.First(capped, capped.ID).Error)
				assert.Equal(t, "pending", capped.State)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					again, err := DrawMarketingLotteryChance(tx, other.Id, finitePool.ID, "lot:capped", 107)
					if err == nil {
						assert.Equal(t, capped.ID, again.ID)
					}
					return err
				}))
				require.NoError(t, db.First(&responsibility, responsibility.ID).Error)
				assert.Zero(t, responsibility.GiftID)
				assert.Equal(t, "pending", responsibility.State)
			})

			if dialect != "sqlite" {
				t.Run("existing_snapshot_uses_current_obligations", func(t *testing.T) {
					currentSpec := MarketingLotteryPoolSpec{Name: "mv2lot_snapshot", Style: "wheel", StartsAt: 100, EndsAt: 1000, Capacity: 1, Prizes: []MarketingLotteryPrizeSpec{{Name: "gift", Weight: 1, Stock: 2, Gifts: []MarketingLotteryGiftSpec{{Money: money}}}}}
					var currentPool *MarketingLotteryPool
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						currentPool, err = CreateMarketingLotteryPool(tx, admin.Id, "lot:snapshot-pool", currentSpec, 90)
						return err
					}))
					stale := db.Begin()
					require.NoError(t, stale.Error)
					defer func() { _ = stale.Rollback().Error }()
					var previous MarketingLotteryPool
					require.NoError(t, stale.First(&previous, currentPool.ID).Error)
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := IssueMarketingLotteryChances(tx, user.Id, currentPool.ID, "lot:snapshot-issue", 1, 100)
						return err
					}))
					_, err := MutateMarketingLotteryPool(stale, admin.Id, currentPool.ID, "lot:snapshot-append", "append", 0, 2, 101)
					require.ErrorIs(t, err, ErrMarketingInsufficient)
					require.NoError(t, stale.Rollback().Error)
					stale = db.Begin()
					require.NoError(t, stale.Error)
					require.NoError(t, stale.First(&previous, currentPool.ID).Error)
					var original *MarketingLotteryDraw
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						original, err = DrawMarketingLotteryChance(tx, user.Id, currentPool.ID, "lot:snapshot-draw", 102)
						return err
					}))
					replay, err := DrawMarketingLotteryChance(stale, user.Id, currentPool.ID, "lot:snapshot-draw", 103)
					require.NoError(t, err)
					assert.Equal(t, original.ID, replay.ID)
					require.NoError(t, stale.Commit().Error)
					stale = db.Begin()
					require.NoError(t, stale.Error)
					require.NoError(t, stale.First(&previous, currentPool.ID).Error)
					var responsibility MarketingLotteryAward
					require.NoError(t, db.Where("draw_id = ?", original.ID).First(&responsibility).Error)
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := FulfillMarketingLotteryAward(tx, user.Id, responsibility.ID, 104)
						return err
					}))
					snapshot, items, err := GetMarketingLotteryDraw(stale, user.Id, original.ID)
					require.NoError(t, err)
					assert.Equal(t, "complete", snapshot.State)
					require.Len(t, items, 1)
					assert.Equal(t, "credited", items[0].State)
					assert.Positive(t, items[0].GiftID)
					assert.EqualValues(t, 104, items[0].CreditedAt)
					fulfilled, err := FulfillMarketingLotteryAward(stale, user.Id, responsibility.ID, 105)
					require.NoError(t, err)
					assert.EqualValues(t, 104, fulfilled.CreditedAt)
					require.NoError(t, stale.Commit().Error)
				})
			}

			if dialect == "sqlite" {
				testMarketingLotteryTransactionRetry(t, db, pool.ID)
			}
		})
	}
}

func testMarketingLotteryTransactionRetry(t *testing.T, db *gorm.DB, poolID int64) {
	t.Helper()
	var before MarketingLotteryPool
	require.NoError(t, db.First(&before, poolID).Error)
	blocker := db.Begin()
	require.NoError(t, blocker.Error)
	require.NoError(t, blocker.Model(&MarketingLotteryPool{}).Where("id = ?", poolID).Update("name", before.Name).Error)
	attempts := 0
	require.NoError(t, WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		attempts++
		if err := tx.Exec("PRAGMA busy_timeout = 1").Error; err != nil {
			return err
		}
		err := tx.Model(&MarketingLotteryPool{}).Where("id = ?", poolID).Update("revision", gorm.Expr("revision + 1")).Error
		if attempts == 1 {
			require.Error(t, err, "real competing write must block")
			require.NoError(t, blocker.Rollback().Error)
		}
		return err
	}))
	assert.Equal(t, 2, attempts)
	var after MarketingLotteryPool
	require.NoError(t, db.First(&after, poolID).Error)
	assert.Equal(t, before.Revision+1, after.Revision)
	blocker = db.Begin()
	require.NoError(t, blocker.Error)
	require.NoError(t, blocker.Model(&MarketingLotteryPool{}).Where("id = ?", poolID).Update("name", before.Name).Error)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts = 0
	err := WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		attempts++
		if err := tx.Exec("PRAGMA busy_timeout = 1").Error; err != nil {
			return err
		}
		err := tx.Model(&MarketingLotteryPool{}).Where("id = ?", poolID).Update("revision", gorm.Expr("revision + 1")).Error
		require.Error(t, err)
		require.NoError(t, blocker.Rollback().Error)
		cancel()
		return err
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, attempts)
	attempts = 0
	sentinel := errors.New("business failure")
	require.ErrorIs(t, WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error { attempts++; return sentinel }), sentinel)
	assert.Equal(t, 1, attempts)
}
