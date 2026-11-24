package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMarketingMaintenance(t *testing.T) {
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
			common.SQLitePath = filepath.Join(t.TempDir(), "maintenance.sqlite")
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
			require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Task{}, &Midjourney{}, &Redemption{}, &UserSubscription{}, &TopUp{}, &Channel{}, &QuotaData{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				users := db.Unscoped().Model(&User{}).Select("id").Where("username LIKE ?", "mv2act_mt_%")
				fundingIDs := db.Model(&MarketingFunding{}).Select("id").Where("user_id IN (?)", users)
				for _, record := range []any{&QuotaData{}, &MarketingAllocation{}, &MarketingDebit{}, &MarketingFundingOperation{}, &MarketingDispatch{}, &MarketingUsageProjection{}} {
					require.NoError(t, db.Where("funding_id IN (?)", fundingIDs).Delete(record).Error)
				}
				tokenIDs := db.Unscoped().Model(&Token{}).Select("id").Where("user_id IN (?)", users)
				require.NoError(t, db.Where("token_id IN (?)", tokenIDs).Delete(&MarketingTokenHold{}).Error)

				for _, record := range []any{&MarketingMaintenanceOperation{}, &MarketingCheckinDay{}, &MarketingCheckinClaim{}, &MarketingActivityDeliveryCursor{}, &MarketingActivityReward{}, &MarketingActivityDecision{}, &MarketingActivityProgress{}, &MarketingActivityFact{}, &MarketingInvitation{}, &MarketingGift{}, &MarketingLedgerEntry{}, &MarketingPayment{}, &MarketingFunding{}, &MarketingAccount{}, &MarketingLotteryChance{}, &MarketingLotteryAward{}, &MarketingLotteryDraw{}, &Task{}, &Midjourney{}, &Token{}, &UserSubscription{}} {
					require.NoError(t, db.Unscoped().Where("user_id IN (?)", users).Delete(record).Error)
				}
				require.NoError(t, db.Where("actor_id IN (?)", users).Delete(&MarketingActivityOperation{}).Error)
				require.NoError(t, db.Where("actor_id IN (?)", users).Delete(&MarketingLotteryOperation{}).Error)
				for _, ref := range []string{"mt-clear", "mt-inviter-clear", "mt-friend-clear"} {
					key, err := marketingSourceKey("maintenance", ref)
					require.NoError(t, err)
					lotteryKey, err := marketingSourceKey("lottery_admin", "assets:"+key)
					require.NoError(t, err)
					require.NoError(t, db.Where("operation_key = ?", lotteryKey).Delete(&MarketingLotteryOperation{}).Error)
				}

				require.NoError(t, db.Where("name LIKE ?", "mv2act_mt_%").Delete(&MarketingActivity{}).Error)
				require.NoError(t, db.Where("name = ?", "mv2mt_partial_channel").Delete(&Channel{}).Error)
				require.NoError(t, db.Unscoped().Where("username LIKE ?", "mv2act_mt_%").Delete(&User{}).Error)
			}
			cleanup()
			t.Cleanup(cleanup)
			admin := marketingActivityUserTest(t, db, "mt_admin", 1, 0)
			require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
			money := MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "100", CurrencyPerUnit: "1", Currency: "CNY"}
			gift := MarketingActivityRewardSpec{Kind: "gift", Calculation: "fixed", Money: money}
			reviewed200 := int64(200)

			t.Run("baseline_inventory_is_readonly_and_source_review_separates_all_gifts", func(t *testing.T) {
				user := User{Username: "mv2act_mt_baseline", AffCode: "mv2act_mt_baseline", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: 1000, AffQuota: 200, AffCount: 3, AffHistoryQuota: 400}
				require.NoError(t, db.Create(&user).Error)
				inventory, err := GetMarketingMaintenanceInventory(context.Background(), admin.Id, user.Id)
				require.NoError(t, err)
				assert.Nil(t, inventory.Account)
				var n int64
				require.NoError(t, db.Model(&MarketingAccount{}).Where("user_id = ?", user.Id).Count(&n).Error)
				assert.Zero(t, n)
				openingGift := money
				openingGift.Amount = "4"
				affGift := money
				affGift.Amount = "2"
				input := MarketingOriginReviewInput{ActorID: admin.Id, UserID: user.Id, Reference: "mt-origin", Evidence: "verified payment receipts and baseline gift journal", ConversionEvidence: "original CNY conversion", LocalIdentity: "uid:0", AuthorizationReference: "operator-session-1", ExpectedQuota: 1000, ExpectedAffQuota: 200, PaidOpeningQuota: 600, GiftOpeningMoney: openingGift, AffDisposition: "gift", AffGiftMoney: affGift, PaymentHistory: "paid"}
				var first *MarketingMaintenanceOperation
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; first, err = ReviewMarketingOrigin(tx, input, 100); return err }))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					again, err := ReviewMarketingOrigin(tx, input, 101)
					require.NoError(t, err)
					assert.Equal(t, first.ID, again.ID)
					return err
				}))
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 600, user.Quota)
				assert.Zero(t, user.AffQuota)
				assert.Equal(t, 3, user.AffCount)
				assert.Equal(t, 400, user.AffHistoryQuota)
				var gifts []MarketingGift
				require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&gifts).Error)
				require.Len(t, gifts, 2)
				assert.EqualValues(t, 400, gifts[0].RemainingQuota)
				assert.EqualValues(t, 200, gifts[1].RemainingQuota)
				require.NoError(t, db.Model(&MarketingPayment{}).Where("user_id = ?", user.Id).Count(&n).Error)
				assert.Zero(t, n, "source classification is not a new cash payment")
				require.NoError(t, db.Model(&MarketingActivityFact{}).Where("user_id = ?", user.Id).Count(&n).Error)
				assert.Zero(t, n)
				changed := input
				changed.PaidOpeningQuota = 601
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReviewMarketingOrigin(tx, changed, 102); return err }), ErrMarketingConflict)
				require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleCommonUser).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReviewMarketingOrigin(tx, input, 102); return err }), ErrMarketingAccess)
				require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
			})

			t.Run("only_root_can_review_own_baseline_and_replays_require_current_authority", func(t *testing.T) {
				root := User{Username: "mv2act_mt_owner", AffCode: "mv2act_mt_owner", Status: common.UserStatusEnabled, Role: common.RoleRootUser, Quota: 200}
				require.NoError(t, db.Create(&root).Error)
				inventory, err := GetMarketingMaintenanceInventory(context.Background(), root.Id, root.Id)
				require.NoError(t, err)
				assert.Nil(t, inventory.Account)
				originMoney := money
				originMoney.Amount = "2"
				input := MarketingOriginReviewInput{ActorID: root.Id, UserID: root.Id, Reference: "mt-owner-origin", Evidence: "initial owner grant, no cash paid", ConversionEvidence: "initial conversion records", LocalIdentity: "uid:0", AuthorizationReference: "operator", ExpectedQuota: 200, GiftOpeningMoney: originMoney, AffDisposition: "none", PaymentHistory: "unknown"}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReviewMarketingOrigin(tx, input, 100); return err }))
				require.NoError(t, db.First(&root, root.Id).Error)
				assert.Zero(t, root.Quota)
				var n int64
				require.NoError(t, db.Model(&MarketingPayment{}).Where("user_id = ?", root.Id).Count(&n).Error)
				assert.Zero(t, n)
				var account MarketingAccount
				require.NoError(t, db.First(&account, "user_id = ?", root.Id).Error)
				assert.Equal(t, "unknown", account.PaymentHistory)
				clear := MarketingAssetClearInput{ActorID: root.Id, UserID: root.Id, Reference: "mt-owner-clear", Evidence: "owner clear", ConversionEvidence: "no paid cash", LocalIdentity: "uid:0", AuthorizationReference: "operator", ExpectedRevision: account.Revision, ExactRefundCNY: "0"}
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, clear, 110); return err }), ErrMarketingConflict)
				require.NoError(t, db.Model(&root).Update("role", common.RoleCommonUser).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReviewMarketingOrigin(tx, input, 111); return err }), ErrMarketingAccess)
				require.NoError(t, db.Model(&root).Updates(map[string]any{"role": common.RoleRootUser, "status": common.UserStatusDisabled}).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReviewMarketingOrigin(tx, input, 112); return err }), ErrMarketingAccess)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, clear, 112); return err }), ErrMarketingAccess)
			})

			t.Run("clear_prepares_old_qualifications_retains_history_and_new_events_only", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "mt_clear", 1, 0)
				cumulative := marketingActivityCreateTest(t, db, admin.Id, "mt-cumulative", MarketingActivitySpec{Name: "mv2act_mt_cumulative", Kind: "recharge", Condition: "cumulative_payments", Frequency: "every", ThresholdCents: 1000, StartsAt: 100, EndsAt: 1000, Rewards: []MarketingActivityRewardSpec{gift}}, 90)
				once := marketingActivityCreateTest(t, db, admin.Id, "mt-once", MarketingActivitySpec{Name: "mv2act_mt_once", Kind: "recharge", Condition: "cumulative_payments", Frequency: "once", ThresholdCents: 1000, StartsAt: 100, EndsAt: 1000, Rewards: []MarketingActivityRewardSpec{gift}}, 90)
				marketingActivityPaymentTest(t, db, user.Id, "mt-old-payment", 1500, 110, "1809")
				grant := MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: "mt-direct-before-clear", GrantedAt: 110, Money: money}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := GrantMarketingGift(tx, grant); return err }))
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
				var account MarketingAccount
				require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
				input := MarketingAssetClearInput{ActorID: admin.Id, UserID: user.Id, Reference: "mt-clear", Evidence: "manual refund platform receipt", ConversionEvidence: "reviewed original cash ratio 1000 per CNY", LocalIdentity: "uid:0", AuthorizationReference: "operator-session-1", ExpectedPaidQuota: 1809, ExpectedRevision: account.Revision, ExactRefundCNY: "1.809", RefundedCents: 181}
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, input, 130); return err }), ErrMarketingClearPreparation)
				pending, err := PlanMarketingClearQualificationsAuthorized(context.Background(), input, 100)
				require.NoError(t, err)
				assert.False(t, pending)
				var operations int64
				require.NoError(t, db.Model(&MarketingMaintenanceOperation{}).Where("user_id = ? AND kind = ?", user.Id, "clear_prepare").Count(&operations).Error)
				assert.EqualValues(t, 1, operations)
				// Commit gift obligations before clearing; no foreground delivery
				// will remain to refresh their fact after the clear transaction.
				marketingActivityProcessTest(t, db, user.Id, 125)
				var op *MarketingMaintenanceOperation
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; op, err = ClearMarketingAssets(tx, input, 130); return err }))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					work, err := NextMarketingActivityDeliveryWork(tx, user.Id, 128)
					require.NoError(t, err)
					assert.Empty(t, work.Rewards)
					return nil
				}))
				pendingSources, err := PendingMarketingRewardSources(context.Background(), 0, 100)
				require.NoError(t, err)
				assert.NotContains(t, pendingSources, user.Id, "cleared obligations must leave background recovery")
				var result MarketingAssetClearResult
				require.NoError(t, common.UnmarshalJsonStr(op.Result, &result))
				assert.Equal(t, "0.001", result.RoundingDifferenceCNY)
				assert.EqualValues(t, 100, result.GiftQuota)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Zero(t, user.Quota)
				var progress MarketingActivityProgress
				require.NoError(t, db.Where("activity_id = ? AND user_id = ?", cumulative.ID, user.Id).First(&progress).Error)
				assert.EqualValues(t, 1, progress.ProcessedAwards)
				assert.Zero(t, progress.RemainderCents)
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusEnabled).Error)
				// Same clear operation cannot erase a later payment. Exact replay is audit-only.
				marketingActivityPaymentTest(t, db, user.Id, "mt-new-payment", 500, 140, "500")
				pending, err = PlanMarketingClearQualificationsAuthorized(context.Background(), input, 100)
				require.NoError(t, err)
				assert.False(t, pending)
				var newFact MarketingActivityFact
				require.NoError(t, db.Where("user_id = ? AND occurred_at = ?", user.Id, 140).First(&newFact).Error)
				assert.Equal(t, "captured", newFact.State, "old clear replay must not plan fresh awards")
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					again, err := ClearMarketingAssets(tx, input, 150)
					require.NoError(t, err)
					assert.Equal(t, op.ID, again.ID)
					return err
				}))
				marketingActivityProcessTest(t, db, user.Id, 160)
				marketingActivityDeliverTest(t, db, user.Id, 160)
				var gifts []MarketingGift
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifts).Error)
				require.Len(t, gifts, 1)
				assert.Zero(t, gifts[0].RemainingQuota)
				assert.EqualValues(t, 100, gifts[0].ClearedQuota)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					again, err := GrantMarketingGift(tx, grant)
					require.NoError(t, err)
					assert.Zero(t, again.RemainingQuota)
					return err
				}))
				marketingActivityPaymentTest(t, db, user.Id, "mt-new-payment2", 500, 170, "500")
				marketingActivityProcessTest(t, db, user.Id, 180)
				marketingActivityDeliverTest(t, db, user.Id, 180)
				require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&gifts).Error)
				require.Len(t, gifts, 2)
				assert.EqualValues(t, 100, gifts[1].RemainingQuota)
				require.NoError(t, db.Where("activity_id = ? AND user_id = ?", once.ID, user.Id).First(&MarketingActivityProgress{}).Error)
				require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
				assert.Equal(t, MarketingPaymentHistoryPaid, account.PaymentHistory)
				assert.Positive(t, account.FirstPaymentID)
			})

			t.Run("clearing_inviter_does_not_clear_friends_and_new_invitation_payments_work", func(t *testing.T) {
				inviter := marketingActivityUserTest(t, db, "mt_inviter", 1, 0)
				friend := marketingActivityUserTest(t, db, "mt_friend", 2010, inviter.Id)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := RecordMarketingInvitation(tx, friend.Id, inviter.Id, "mt-relation", 2010)
					return err
				}))
				own := MarketingActivitySpec{Name: "mv2act_mt_friend_own", Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 1, StartsAt: 2000, EndsAt: 3000, Rewards: []MarketingActivityRewardSpec{gift}}
				marketingActivityCreateTest(t, db, admin.Id, "mt-friend-own", own, 1990)
				invite := MarketingActivitySpec{Name: "mv2act_mt_invite", Kind: "invitation", Condition: "friend_cumulative", Frequency: "every", ThresholdCents: 1000, StartsAt: 2000, EndsAt: 3000, Rewards: []MarketingActivityRewardSpec{gift}}
				activity := marketingActivityCreateTest(t, db, admin.Id, "mt-invite", invite, 1990)
				marketingActivityPaymentTest(t, db, friend.Id, "mt-friend-old", 1500, 2020, "1500")
				require.NoError(t, db.Model(&User{}).Where("id = ?", inviter.Id).Update("status", common.UserStatusDisabled).Error)
				input := MarketingAssetClearInput{ActorID: admin.Id, UserID: inviter.Id, Reference: "mt-inviter-clear", Evidence: "manual forfeiture of gifts with no paid balance", ConversionEvidence: "paid balance zero", LocalIdentity: "uid:0", AuthorizationReference: "operator", ExactRefundCNY: "0"}
				pending, err := PlanMarketingClearQualificationsAuthorized(context.Background(), input, 100)
				require.NoError(t, err)
				assert.False(t, pending)
				marketingActivityProcessTest(t, db, friend.Id, 2025)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, input, 2030); return err }))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					work, err := NextMarketingActivityDeliveryWork(tx, friend.Id, 128)
					require.NoError(t, err)
					require.Len(t, work.Rewards, 1)
					assert.Equal(t, friend.Id, work.Rewards[0].UserID)
					var fact MarketingActivityFact
					require.NoError(t, tx.First(&fact, work.Rewards[0].FactID).Error)
					assert.Equal(t, "pending", fact.DeliveryState, "another beneficiary still has an obligation")
					return nil
				}))
				require.NoError(t, db.Model(&User{}).Where("id = ?", inviter.Id).Update("status", common.UserStatusEnabled).Error)
				marketingActivityProcessTest(t, db, friend.Id, 2040)
				marketingActivityDeliverTest(t, db, friend.Id, 2040)
				var count int64
				require.NoError(t, db.Model(&MarketingGift{}).Where("user_id = ?", inviter.Id).Count(&count).Error)
				assert.Zero(t, count)
				require.NoError(t, db.Model(&MarketingGift{}).Where("user_id = ?", friend.Id).Count(&count).Error)
				assert.EqualValues(t, 1, count)
				var progress MarketingActivityProgress
				require.NoError(t, db.Where("user_id = ? AND activity_id = ?", inviter.Id, activity.ID).First(&progress).Error)
				assert.EqualValues(t, 1, progress.ProcessedAwards)
				assert.Zero(t, progress.RemainderCents)
				marketingActivityPaymentTest(t, db, friend.Id, "mt-friend-new", 1000, 2050, "1000")
				marketingActivityProcessTest(t, db, friend.Id, 2060)
				marketingActivityDeliverTest(t, db, friend.Id, 2060)
				require.NoError(t, db.Model(&MarketingGift{}).Where("user_id = ?", inviter.Id).Count(&count).Error)
				assert.EqualValues(t, 1, count)
				// Clearing the friend must not erase the inviter's next incomplete threshold.
				marketingActivityPaymentTest(t, db, friend.Id, "mt-friend-partial", 400, 2070, "400")
				require.NoError(t, db.Model(&User{}).Where("id = ?", friend.Id).Update("status", common.UserStatusDisabled).Error)
				var account MarketingAccount
				require.NoError(t, db.First(&account, "user_id = ?", friend.Id).Error)
				input.UserID = friend.Id
				input.Reference = "mt-friend-clear"
				input.ExpectedPaidQuota = 2900
				input.ExpectedRevision = account.Revision
				input.ExactRefundCNY = "29"
				input.RefundedCents = 2900
				pending, err = PlanMarketingClearQualificationsAuthorized(context.Background(), input, 100)
				require.NoError(t, err)
				assert.False(t, pending)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, input, 2080); return err }))
				require.NoError(t, db.First(&progress, progress.ID).Error)
				assert.EqualValues(t, 400, progress.RemainderCents)
			})

			t.Run("disabled_is_not_cleared_and_unsettled_cost_blocks_clearing", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "mt_review", 1, 0)
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{"status": common.UserStatusDisabled, "quota": 100}).Error)
				funding := MarketingFunding{UserID: user.Id, RequestKey: "mt_unknown", State: "review", PricingEvidence: "original tariff", UsageEvidence: "connection lost", PayloadHash: "mt_unknown"}
				require.NoError(t, db.Create(&funding).Error)
				input := MarketingAssetClearInput{ActorID: admin.Id, UserID: user.Id, Reference: "mt-review-clear", Evidence: "manual request", ConversionEvidence: "original rate", LocalIdentity: "uid:0", AuthorizationReference: "operator", ExpectedPaidQuota: 100, ExactRefundCNY: "1", RefundedCents: 100}
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, input, 200); return err }), ErrMarketingReview)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 100, user.Quota)
				require.NoError(t, db.Delete(&funding).Error)
				token := Token{UserId: user.Id, Key: "mt-soft-deleted-hold", Status: common.TokenStatusDisabled}
				require.NoError(t, db.Create(&token).Error)
				require.NoError(t, db.Create(&MarketingTokenHold{TokenID: token.Id, ReservedQuota: 7}).Error)
				require.NoError(t, db.Delete(&token).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, input, 200); return err }), ErrMarketingReview, "soft-deleted token hold remains a liability")
				require.NoError(t, db.Where("token_id = ?", token.Id).Delete(&MarketingTokenHold{}).Error)

				active := input
				require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusEnabled).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, active, 200); return err }), ErrMarketingConflict)
				self := input
				self.UserID = admin.Id
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ClearMarketingAssets(tx, self, 200); return err }), ErrMarketingAccess)
			})

			t.Run("legacy_wallet_writers_cannot_bypass_reviewed_or_unreviewed_ledger", func(t *testing.T) {
				oldBatch, oldRedis := common.BatchUpdateEnabled, common.RedisEnabled
				t.Cleanup(func() { common.BatchUpdateEnabled = oldBatch; common.RedisEnabled = oldRedis })
				common.RedisEnabled = false
				for _, origin := range []string{"verified", "unverified"} {
					user := marketingActivityUserTest(t, db, "mt_guard_"+origin, 1, 0)
					require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 100).Error)
					require.NoError(t, db.Model(&MarketingAccount{}).Where("user_id = ?", user.Id).Update("origin_state", origin).Error)
					for _, batch := range []bool{false, true} {
						common.BatchUpdateEnabled = batch
						require.ErrorIs(t, IncreaseUserQuota(user.Id, 10, false), ErrMarketingBalanceEdit)
						require.ErrorIs(t, DecreaseUserQuota(user.Id, 10, false), ErrMarketingBalanceEdit)
						require.ErrorIs(t, persistUserQuotaDelta(user.Id, 10), ErrMarketingBalanceEdit)
						reserved, err := TryReserveUserQuota(user.Id, 10)
						require.ErrorIs(t, err, ErrMarketingBalanceEdit)
						assert.False(t, reserved)
					}
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 100, user.Quota)
				}
			})

			t.Run("managed_task_reconciliation_closes_task_and_late_worker_uses_current_cost", func(t *testing.T) {
				for _, kind := range []string{"task", "midjourney"} {
					user := marketingActivityUserTest(t, db, "mt_managed_"+kind, 1, 0)
					require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 100).Error)
					token := Token{UserId: user.Id, Key: "mt-managed-" + kind, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
					require.NoError(t, db.Create(&token).Error)
					funding := reserveMarketingTest(t, db, user, token, "mt-managed-"+kind, 100, 20)
					outcome := &MarketingTaskOutcome{Evidence: "upstream disconnected without final charge", Record: Log{UserId: user.Id, RequestId: "mt-managed-" + kind, ModelName: "original-model"}}
					var taskID int64
					if kind == "task" {
						task := Task{UserId: user.Id, MarketingFundingID: funding.ID, MarketingBillingPending: true, Status: TaskStatusFailure, PrivateData: TaskPrivateData{MarketingOutcome: outcome}}
						require.NoError(t, db.Create(&task).Error)
						taskID = task.ID
					} else {
						task := Midjourney{UserId: user.Id, MarketingFundingID: funding.ID, MarketingBillingPending: true, Status: "FAILURE", MarketingBillingData: TaskPrivateData{MarketingOutcome: outcome}}
						require.NoError(t, db.Create(&task).Error)
						taskID = int64(task.Id)
					}
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						if kind == "task" {
							funding, err = SettleMarketingTask(tx, taskID)
						} else {
							funding, err = SettleMarketingMidjourney(tx, int(taskID))
						}
						return err
					}))
					assert.Equal(t, "review", funding.State)
					assert.False(t, funding.CostKnown)
					actual := int64(30)
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						op, replay, err := BeginMarketingMaintenanceOperation(tx, admin.Id, user.Id, "reconcile", "mt-managed-final-"+kind, "provider terminal charge receipt", "uid:0", "operator", actual, 200)
						if err != nil {
							return err
						}
						require.False(t, replay)
						funding, err = SettleMarketingConsumption(tx, MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: funding.Revision + 1, ActualQuota: &actual, Final: true, EvidenceKind: "admin_reconcile", Evidence: "original tariff and provider charge"}, outcome.Record)
						if err != nil {
							return err
						}
						if err := ReconcileMarketingTaskOutcome(tx, user.Id, funding.ID, "SUCCESS", 200); err != nil {
							return err
						}
						return CompleteMarketingMaintenanceOperation(tx, op, map[string]any{"funding_id": funding.ID})
					}))
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var current *MarketingFunding
						var err error
						if kind == "task" {
							current, err = SettleMarketingTask(tx, taskID)
						} else {
							current, err = SettleMarketingMidjourney(tx, int(taskID))
						}
						require.NoError(t, err)
						assert.Equal(t, funding.Revision, current.Revision)
						assert.EqualValues(t, 30, current.SettledQuota)
						return err
					}))
					inventory, err := GetMarketingMaintenanceInventory(context.Background(), admin.Id, user.Id)
					require.NoError(t, err)
					assert.Empty(t, inventory.Funding)
					assert.Empty(t, inventory.Tasks)
					assert.Empty(t, inventory.Midjourney)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 70, user.Quota)
				}
			})

			t.Run("legacy_partial_refund_uses_independent_confirmed_sources", func(t *testing.T) {
				user := User{Username: "mv2act_mt_partial", AffCode: "mv2act_mt_partial", Status: common.UserStatusDisabled, Role: common.RoleCommonUser, Quota: 1000, UsedQuota: 300}
				require.NoError(t, db.Create(&user).Error)
				token := Token{UserId: user.Id, Key: "mt-deleted-unlimited", UnlimitedQuota: true, RemainQuota: -200, UsedQuota: 200, Status: common.TokenStatusDisabled}
				require.NoError(t, db.Create(&token).Error)
				require.NoError(t, db.Delete(&token).Error)
				channel := Channel{Name: "mv2mt_partial_channel", UsedQuota: 500}
				require.NoError(t, db.Create(&channel).Error)
				task := Task{UserId: user.Id, Quota: 200, Status: TaskStatusFailure, ChannelId: channel.Id, PrivateData: TaskPrivateData{TokenId: token.Id}}
				require.NoError(t, db.Create(&task).Error)
				input := MarketingLegacyTaskReconcileInput{ActorID: admin.Id, UserID: user.Id, Reference: "mt-partial-final", Evidence: "wallet refund succeeded but later steps failed; actual charge independently verified", LocalIdentity: "uid:0", AuthorizationReference: "operator", TaskKind: "task", TaskID: task.ID, ExpectedQuota: 200, ConfirmedNetDebitQuota: 0, ConfirmedUserUsageQuota: &reviewed200, ConfirmedTokenDebitQuota: &reviewed200, ConfirmedChannelUsageQuota: &reviewed200, ActualQuota: 50, Terminal: "FAILURE", OriginalSourceEvidence: "separate wallet/token/user/channel journals", WriterIsolationEvidence: "all writers stopped"}
				missing := input
				missing.ConfirmedTokenDebitQuota = nil
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReconcileMarketingLegacyTask(tx, missing, 300); return err }), ErrMarketingInput)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReconcileMarketingLegacyTask(tx, input, 300); return err }))
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 950, user.Quota)
				assert.Equal(t, 150, user.UsedQuota)
				require.NoError(t, db.Unscoped().First(&token, token.Id).Error)
				assert.Equal(t, -50, token.RemainQuota)
				assert.Equal(t, 50, token.UsedQuota)
				require.NoError(t, db.First(&channel, channel.Id).Error)
				assert.EqualValues(t, 350, channel.UsedQuota)
				// A fully returned zero-cost task with stale task.quota must not refund
				// that quota a second time or alter unrelated historical consumption.
				zero := int64(0)
				second := Task{UserId: user.Id, Quota: 200, Status: TaskStatusFailure}
				require.NoError(t, db.Create(&second).Error)
				input.Reference = "mt-already-returned"
				input.TaskID = second.ID
				input.ActualQuota = 0
				input.ConfirmedUserUsageQuota = &zero
				input.ConfirmedTokenDebitQuota = nil
				input.ConfirmedChannelUsageQuota = nil
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReconcileMarketingLegacyTask(tx, input, 301); return err }))
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 950, user.Quota)
				assert.Equal(t, 150, user.UsedQuota)
			})

			t.Run("legacy_terminal_is_not_proof_of_financial_completion", func(t *testing.T) {
				for _, kind := range []string{"task", "midjourney"} {
					user := User{Username: "mv2act_mt_terminal_" + kind, AffCode: "mv2act_mt_terminal_" + kind, Status: common.UserStatusDisabled, Role: common.RoleCommonUser, Quota: 800, UsedQuota: 200}
					require.NoError(t, db.Create(&user).Error)
					var taskID int64
					if kind == "task" {
						task := Task{UserId: user.Id, Quota: 200, Status: TaskStatusFailure}
						require.NoError(t, db.Create(&task).Error)
						taskID = task.ID
					} else {
						task := Midjourney{UserId: user.Id, Quota: 200, Status: "FAILURE"}
						require.NoError(t, db.Create(&task).Error)
						taskID = int64(task.Id)
					}
					review := MarketingOriginReviewInput{ActorID: admin.Id, UserID: user.Id, Reference: "mt-terminal-origin-" + kind, Evidence: "old receipt", ConversionEvidence: "old conversion", LocalIdentity: "uid:0", AuthorizationReference: "operator", ExpectedQuota: 800, PaidOpeningQuota: 800, AffDisposition: "none", PaymentHistory: "paid"}
					require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReviewMarketingOrigin(tx, review, 300); return err }), ErrMarketingReview)
					inventory, err := GetMarketingMaintenanceInventory(context.Background(), admin.Id, user.Id)
					require.NoError(t, err)
					assert.Equal(t, 1, len(inventory.Tasks)+len(inventory.Midjourney))
					input := MarketingLegacyTaskReconcileInput{ActorID: admin.Id, UserID: user.Id, Reference: "mt-terminal-final-" + kind, Evidence: "confirmed upstream failed with known charge", LocalIdentity: "uid:0", AuthorizationReference: "operator", TaskKind: kind, TaskID: taskID, ExpectedQuota: 200, ConfirmedNetDebitQuota: 200, ConfirmedUserUsageQuota: &reviewed200, ActualQuota: 50, Terminal: "FAILURE", OriginalSourceEvidence: "wallet precharge journal", WriterIsolationEvidence: "all writers drained"}
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReconcileMarketingLegacyTask(tx, input, 301); return err }))
					changed := input
					changed.Reference += "-different-key"
					changed.ExpectedQuota = 50
					require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReconcileMarketingLegacyTask(tx, changed, 302); return err }), ErrMarketingConflict)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 950, user.Quota)
					assert.Equal(t, 50, user.UsedQuota)
					review.ExpectedQuota = 950
					review.PaidOpeningQuota = 950
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReviewMarketingOrigin(tx, review, 303); return err }))
				}
			})

			t.Run("legacy_task_requires_proven_cost_and_preserves_original_source", func(t *testing.T) {
				user := User{Username: "mv2act_mt_legacy", AffCode: "mv2act_mt_legacy", Status: common.UserStatusDisabled, Role: common.RoleCommonUser, Quota: 800, UsedQuota: 200}
				require.NoError(t, db.Create(&user).Error)
				task := Task{UserId: user.Id, Quota: 200, Status: TaskStatusInProgress}
				require.NoError(t, db.Create(&task).Error)
				input := MarketingLegacyTaskReconcileInput{ActorID: admin.Id, UserID: user.Id, Reference: "mt-legacy-final", Evidence: "provider terminal charge receipt", LocalIdentity: "uid:0", AuthorizationReference: "operator", TaskKind: "task", TaskID: task.ID, ExpectedQuota: 200, ConfirmedNetDebitQuota: 200, ConfirmedUserUsageQuota: &reviewed200, ActualQuota: 50, Terminal: "SUCCESS", OriginalSourceEvidence: "task original wallet precharge", WriterIsolationEvidence: "all worker instances stopped"}
				missing := input
				missing.WriterIsolationEvidence = ""
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReconcileMarketingLegacyTask(tx, missing, 300); return err }), ErrMarketingInput)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReconcileMarketingLegacyTask(tx, input, 300); return err }))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReconcileMarketingLegacyTask(tx, input, 301); return err }))
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 950, user.Quota)
				var account MarketingAccount
				require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
				assert.EqualValues(t, 800, account.OpeningQuota)
				assert.EqualValues(t, 150, account.OpeningAdjustmentQuota)
				assert.Equal(t, "unverified", account.OriginState)
				review := MarketingOriginReviewInput{ActorID: admin.Id, UserID: user.Id, Reference: "mt-legacy-origin", Evidence: "original payment proof", ConversionEvidence: "original conversion", LocalIdentity: "uid:0", AuthorizationReference: "operator", ExpectedQuota: 950, PaidOpeningQuota: 950, AffDisposition: "none", PaymentHistory: "paid"}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := ReviewMarketingOrigin(tx, review, 302); return err }))
				var count int64
				require.NoError(t, db.Model(&MarketingPayment{}).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.Zero(t, count)
			})
		})
	}
}
