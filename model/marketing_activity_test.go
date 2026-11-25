package model

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMarketingActivity(t *testing.T) {
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
			common.SQLitePath = filepath.Join(t.TempDir(), "activity.sqlite")
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
			require.NoError(t, db.AutoMigrate(&User{}, &ExternalIdentityClaim{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				users := db.Unscoped().Model(&User{}).Select("id").Where("username LIKE ?", "mv2act_%")
				pools := db.Model(&MarketingLotteryPool{}).Select("id").Where("name LIKE ?", "mv2act_%")
				for _, record := range []any{&ExternalIdentityClaim{}, &MarketingActivityDeliveryCursor{}, &MarketingActivityReward{}, &MarketingActivityDecision{}, &MarketingActivityProgress{}, &MarketingActivityFact{}, &MarketingInvitation{}, &MarketingLotteryAward{}, &MarketingLotteryDraw{}, &MarketingLotteryChance{}, &MarketingLotteryIssue{}, &MarketingGift{}, &MarketingLedgerEntry{}, &MarketingPayment{}, &MarketingAccount{}} {
					require.NoError(t, db.Where("user_id IN (?)", users).Delete(record).Error)
				}
				require.NoError(t, db.Where("actor_id IN (?)", users).Delete(&MarketingActivityOperation{}).Error)
				require.NoError(t, db.Where("name LIKE ?", "mv2act_%").Delete(&MarketingActivity{}).Error)
				require.NoError(t, db.Where("actor_id IN (?)", users).Delete(&MarketingLotteryOperation{}).Error)
				require.NoError(t, db.Where("pool_id IN (?)", pools).Delete(&MarketingLotteryPrize{}).Error)
				require.NoError(t, db.Where("name LIKE ?", "mv2act_%").Delete(&MarketingLotteryPool{}).Error)
				require.NoError(t, db.Unscoped().Where("username LIKE ?", "mv2act_%").Delete(&User{}).Error)
			}
			cleanup()
			t.Cleanup(cleanup)
			admin := marketingActivityUserTest(t, db, "admin", 1, 0)
			require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
			money := MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: "50", CurrencyPerUnit: "1", Currency: "CNY"}
			gift := MarketingActivityRewardSpec{Kind: "gift", Calculation: "fixed", Money: money, ValidSeconds: 10}

			t.Run("registration_atomic_fact_and_no_legacy_money", func(t *testing.T) {
				oldNew, oldInvitee, oldInviter := common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter
				common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = 900, 800, 700
				t.Cleanup(func() {
					common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = oldNew, oldInvitee, oldInviter
				})
				inviter := marketingActivityUserTest(t, db, "reg_owner", 1, 0)
				own := MarketingActivitySpec{Name: "mv2act_reg_actual", Kind: "registration", Condition: "registration", Frequency: "once", StartsAt: 10000, EndsAt: 10100, Rewards: []MarketingActivityRewardSpec{gift}}
				marketingActivityCreateTest(t, db, admin.Id, "reg-actual", own, 9990)
				invited := own
				invited.Name, invited.Kind, invited.Condition = "mv2act_reg_invite", "invitation", "friend_registration"
				marketingActivityCreateTest(t, db, admin.Id, "reg-invite", invited, 9990)
				user := User{Username: "mv2act_reg_new", Password: "safe-new-password", Quota: 12345, AffQuota: 555, CreatedAt: 1, Role: common.RoleCommonUser}
				original := user
				rollback := errors.New("OAuth binding failed")
				var rolledID int
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					attempt := original
					if err := attempt.insertMarketingRegistration(tx, inviter.Id, func() int64 { return 10010 }); err != nil {
						return err
					}
					rolledID = attempt.Id
					return rollback
				}), rollback)
				var count int64
				for _, table := range []any{&MarketingAccount{}, &MarketingInvitation{}, &MarketingActivityFact{}} {
					require.NoError(t, db.Model(table).Where("user_id = ?", rolledID).Count(&count).Error)
					assert.Zero(t, count)
				}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					return user.insertMarketingRegistration(tx, inviter.Id, func() int64 { return 10010 })
				}))
				assert.True(t, common.ValidatePasswordAndHash("safe-new-password", user.Password))
				assert.EqualValues(t, 10010, user.CreatedAt)
				assert.Zero(t, user.Quota)
				assert.Zero(t, user.AffQuota)
				var account MarketingAccount
				require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
				assert.Equal(t, "verified", account.OriginState)
				assert.Equal(t, MarketingPaymentHistoryNone, account.PaymentHistory)
				assert.Equal(t, "registration:"+strconv.Itoa(user.Id), account.PaymentHistoryEvidence)
				var relation MarketingInvitation
				require.NoError(t, db.First(&relation, "user_id = ?", user.Id).Error)
				assert.Equal(t, inviter.Id, relation.InviterID)
				assert.Equal(t, user.CreatedAt, relation.EstablishedAt)
				for range 2 {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := CaptureMarketingRegistrationFact(tx, user.Id, "registration:"+strconv.Itoa(user.Id), user.CreatedAt)
						return err
					}))
					marketingActivityProcessTest(t, db, user.Id, 10011)
					marketingActivityDeliverTest(t, db, user.Id, 10012)
					user.FinishInsert(inviter.Id)
					user.FinalizeOAuthUserCreation(inviter.Id)
				}
				for _, id := range []int{user.Id, inviter.Id} {
					var stored User
					require.NoError(t, db.First(&stored, id).Error)
					assert.Zero(t, stored.Quota)
					assert.Zero(t, stored.AffQuota)
					assert.Zero(t, stored.AffHistoryQuota)
					require.NoError(t, db.Model(&MarketingGift{}).Where("user_id = ?", id).Count(&count).Error)
					assert.EqualValues(t, 1, count)
				}
				require.NoError(t, db.First(&inviter, inviter.Id).Error)
				assert.Equal(t, 1, inviter.AffCount)
				payment := marketingActivityPaymentTest(t, db, user.Id, "reg-first-paid", 100, 10020, "25")
				assert.Equal(t, MarketingFirstPaymentConfirmed, payment.FirstStatus)
				user.FinishInsert(inviter.Id)
				require.NoError(t, db.First(&account, "user_id = ?", user.Id).Error)
				assert.Equal(t, MarketingPaymentHistoryPaid, account.PaymentHistory)
				assert.Equal(t, payment.ID, account.FirstPaymentID)
				stale := user
				stale.InviterId = admin.Id
				require.NoError(t, stale.Update(false))
				assert.Equal(t, inviter.Id, stale.InviterId)
				require.Error(t, stale.TransferAffQuotaToQuota(1))
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 25, user.Quota)
			})

			t.Run("registration_rejects_failure_and_stale_invitation", func(t *testing.T) {
				for _, scenario := range []string{"disabled", "deleted", "missing"} {
					t.Run(scenario, func(t *testing.T) {
						inviter := marketingActivityUserTest(t, db, "reg_"+scenario, 1, 0)
						switch scenario {
						case "disabled":
							require.NoError(t, db.Model(&User{}).Where("id = ?", inviter.Id).Update("status", common.UserStatusDisabled).Error)
						case "deleted":
							require.NoError(t, db.Delete(&inviter).Error)
						case "missing":
							require.NoError(t, db.Unscoped().Delete(&inviter).Error)
						}
						user := User{Username: "mv2act_r_" + scenario, InviterId: inviter.Id, Role: common.RoleCommonUser}
						require.NoError(t, user.Insert(inviter.Id))
						assert.Zero(t, user.InviterId)
						var count int64
						require.NoError(t, db.Model(&MarketingInvitation{}).Where("user_id = ?", user.Id).Count(&count).Error)
						assert.Zero(t, count)
					})
				}
				user := User{Username: "mv2act_reg_failure", Password: "original-password", Role: common.RoleCommonUser}
				original := user
				failure := errors.New("registration fact unavailable")
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reg:fail-fact", func(tx *gorm.DB) {
					if tx.Statement.Table == (MarketingActivityFact{}).TableName() {
						tx.AddError(failure)
					}
				}))
				err := user.Insert(0)
				require.NoError(t, db.Callback().Create().Remove("reg:fail-fact"))
				require.ErrorIs(t, err, failure)
				assert.Equal(t, original, user)
				var count int64
				require.NoError(t, db.Model(&User{}).Where("username = ?", original.Username).Count(&count).Error)
				assert.Zero(t, count)
				require.NoError(t, user.Insert(0))
				assert.True(t, common.ValidatePasswordAndHash("original-password", user.Password))
				assert.ErrorIs(t, user.InsertWithTx(db, 0), ErrMarketingTransaction)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return user.InsertWithTx(tx, 0) }), ErrMarketingInput)
			})

			t.Run("registration_two_connections_and_inviter_status_at_lock", func(t *testing.T) {
				inviter := marketingActivityUserTest(t, db, "reg_parallel_owner", 1, 0)
				marketingActivityConcurrentTest(t, inviter.Id, func(tx *gorm.DB, worker int) error {
					// A fresh input on each complete transaction attempt is required.
					user := User{Username: "mv2act_reg_p" + strconv.Itoa(worker), Role: common.RoleCommonUser}
					return user.InsertWithTx(tx, inviter.Id)
				})
				var count int64
				require.NoError(t, db.Model(&MarketingInvitation{}).Where("inviter_id = ?", inviter.Id).Count(&count).Error)
				assert.EqualValues(t, 2, count)
				require.NoError(t, db.First(&inviter, inviter.Id).Error)
				assert.Equal(t, 2, inviter.AffCount)

				for _, deleted := range []bool{false, true} {
					name := "reg_wait_" + strconv.FormatBool(deleted)
					owner := marketingActivityUserTest(t, db, name, 1, 0)
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					ready, resume := make(chan struct{}), make(chan struct{})
					results := make(chan error, 1)
					type registrationGateKey struct{}
					first := true
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reg:inviter-lock", func(tx *gorm.DB) {
						if tx.Statement.Context.Value(registrationGateKey{}) != nil && tx.Statement.Table == "users" && first {
							first = false
							close(ready)
							select {
							case <-resume:
							case <-ctx.Done():
								tx.AddError(ctx.Err())
							}
						}
					}))
					user := User{Username: "mv2act_rwait_" + strconv.FormatBool(deleted), Role: common.RoleCommonUser}
					go func() {
						results <- db.WithContext(context.WithValue(ctx, registrationGateKey{}, true)).Transaction(func(tx *gorm.DB) error { return user.InsertWithTx(tx, owner.Id) })
					}()
					select {
					case <-ready:
					case <-ctx.Done():
						cancel()
						t.Fatal(ctx.Err())
					}
					var mutationErr error
					if deleted {
						mutationErr = db.Delete(&owner).Error
					} else {
						mutationErr = db.Model(&User{}).Where("id = ?", owner.Id).Update("status", common.UserStatusDisabled).Error
					}
					close(resume)
					err := <-results
					cancel()
					require.NoError(t, db.Callback().Query().Remove("reg:inviter-lock"))
					require.NoError(t, mutationErr)
					require.NoError(t, err)
					assert.Zero(t, user.InviterId)
					require.NoError(t, db.Model(&MarketingInvitation{}).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
				}
			})

			t.Run("builtin_registration_identity_has_one_owner", func(t *testing.T) {
				for _, column := range builtinIdentityColumns {
					t.Run(column, func(t *testing.T) {
						var rejected [2]bool
						marketingActivityConcurrentTest(t, admin.Id, func(tx *gorm.DB, worker int) error {
							user := marketingRegistrationIdentityTest("mv2act_id_"+column+strconv.Itoa(worker), column, "same-subject")
							err := user.InsertWithTx(tx, 0)
							if errors.Is(err, ErrExternalIdentityAlreadyClaimed) {
								rejected[worker] = true
								// The loser must roll back the provisional user, account and fact.
								return err
							}
							return err
						}, ErrExternalIdentityAlreadyClaimed)
						assert.NotEqual(t, rejected[0], rejected[1])
						var users []User
						require.NoError(t, db.Where("username LIKE ?", "mv2act_id_"+column+"%").Find(&users).Error)
						require.Len(t, users, 1)
						assert.Equal(t, "same-subject", builtinIdentityValues(&users[0])[column])
						var count int64
						require.NoError(t, db.Model(&MarketingActivityFact{}).Where("user_id = ? AND kind = ?", users[0].Id, "registration").Count(&count).Error)
						assert.EqualValues(t, 1, count)
					})
				}
			})

			t.Run("builtin_identity_legacy_owner_unbind_and_replacement", func(t *testing.T) {
				for _, column := range builtinIdentityColumns {
					owner := marketingActivityUserTest(t, db, "legacy_"+column, 1, 0)
					subject := " Legacy-" + column + strings.Repeat("x", 130)
					require.NoError(t, db.Model(&User{}).Where("id = ?", owner.Id).Update(column, subject).Error)
					candidate := marketingRegistrationIdentityTest("mv2act_claim_"+column, column, subject)
					require.ErrorIs(t, candidate.Insert(0), ErrExternalIdentityAlreadyClaimed)
					assert.Zero(t, candidate.Id)
					var count int64
					require.NoError(t, db.Model(&User{}).Where("username = ?", candidate.Username).Count(&count).Error)
					assert.Zero(t, count)
					// Baseline backfill is repeatable and does not rewrite raw subject.
					require.NoError(t, InitializeExternalIdentityClaims())
					require.NoError(t, InitializeExternalIdentityClaims())
					require.NoError(t, db.First(&owner, owner.Id).Error)
					assert.Equal(t, subject, builtinIdentityValues(&owner)[column])
					require.NoError(t, db.Model(&MarketingActivityFact{}).Where("user_id = ?", owner.Id).Count(&count).Error)
					assert.Zero(t, count)
					require.NoError(t, UpdateUserBindColumn(owner.Id, column, ""))
					require.NoError(t, candidate.Insert(0))
					require.ErrorIs(t, UpdateUserBindColumn(owner.Id, column, subject), ErrExternalIdentityAlreadyClaimed)
					require.NoError(t, UpdateUserBindColumn(owner.Id, column, "replacement"+column))
					require.ErrorIs(t, UpdateUserBindColumn(owner.Id, column, subject), ErrExternalIdentityAlreadyClaimed)
					require.NoError(t, db.First(&owner, owner.Id).Error)
					assert.Equal(t, "replacement"+column, builtinIdentityValues(&owner)[column])
					// Ordinary profile snapshots cannot bypass claims.
					stale := marketingRegistrationIdentityTest(owner.Username, column, subject)
					stale.Id = owner.Id
					require.NoError(t, stale.Update(false))
					assert.Equal(t, "replacement"+column, builtinIdentityValues(&stale)[column])
				}
				github := marketingActivityUserTest(t, db, "legacy_github_migrate", 1, 0)
				require.NoError(t, UpdateUserBindColumn(github.Id, "github_id", "old-github-name"))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					changed, err := MigrateLegacyGitHubBindingWithTx(tx, github.Id, "old-github-name", "123456")
					assert.True(t, changed)
					return err
				}))
				oldName := marketingRegistrationIdentityTest("mv2act_old_gh", "github_id", "old-github-name")
				require.NoError(t, oldName.Insert(0))
				duplicate := marketingRegistrationIdentityTest("mv2act_dup_gh", "github_id", "123456")
				require.ErrorIs(t, duplicate.Insert(0), ErrExternalIdentityAlreadyClaimed)
				require.NoError(t, github.ClearBinding("github"))
				require.NoError(t, duplicate.Insert(0))
			})

			t.Run("draft_activation_and_visibility_are_distinct", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "draft", 1, 0)
				spec := MarketingActivitySpec{Name: "mv2act_draft", Published: true, Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 1, StartsAt: 10, EndsAt: 90, Rewards: []MarketingActivityRewardSpec{gift}}
				var activity *MarketingActivity
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					activity, err = CreateMarketingActivity(tx, admin.Id, "act:draft-create", spec, 5)
					return err
				}))
				assert.Zero(t, activity.EnabledAt)
				assert.True(t, activity.Published)
				marketingActivityPaymentTest(t, db, user.Id, "draft-before", 1, 20, "1")
				spec.Name = "mv2act_draft_edited"
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := UpdateMarketingActivity(tx, admin.Id, activity.ID, "act:draft-edit", spec, 25)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					activity, err = ActivateMarketingActivity(tx, admin.Id, activity.ID, "act:draft-activate", 30)
					return err
				}))
				assert.EqualValues(t, 30, activity.EnabledAt)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SetMarketingActivityPublished(tx, admin.Id, activity.ID, "act:draft-hide", false, 31)
					return err
				}))
				marketingActivityPaymentTest(t, db, user.Id, "draft-after", 1, 40, "1")
				marketingActivityProcessTest(t, db, user.Id, 41)
				var rewards []MarketingActivityReward
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&rewards).Error)
				require.Len(t, rewards, 1)
				var fact MarketingActivityFact
				require.NoError(t, db.First(&fact, rewards[0].FactID).Error)
				assert.EqualValues(t, 40, fact.OccurredAt)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					again, err := ActivateMarketingActivity(tx, admin.Id, activity.ID, "act:draft-activate-again", 42)
					if err == nil {
						assert.EqualValues(t, 30, again.EnabledAt)
					}
					return err
				}))
				require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleCommonUser).Error)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := ActivateMarketingActivity(tx, admin.Id, activity.ID, "act:draft-activate", 43)
					return err
				}), ErrMarketingAccess)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SetMarketingActivityPublished(tx, admin.Id, activity.ID, "act:draft-hide", false, 43)
					return err
				}), ErrMarketingAccess)
				require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
			})

			t.Run("sequential_facts_exclusion_and_shortage", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ordered", 1, 0)
				pool := marketingActivityPoolTest(t, db, admin.Id, "ordered", 100, 1000, 2, 8, money)
				highSpec := MarketingActivitySpec{Name: "mv2act_high", Kind: "recharge", Condition: "cumulative_payments", Frequency: "every", ThresholdCents: 100, StartsAt: 100, EndsAt: 1000, ExclusiveGroup: "exclusive", Priority: 2, Rewards: []MarketingActivityRewardSpec{gift, {Kind: "chance", PoolID: pool.ID, Count: 3, ChanceMode: "direct"}}}
				high := marketingActivityCreateTest(t, db, admin.Id, "high", highSpec, 90)
				lowSpec := highSpec
				lowSpec.Name = "mv2act_low"
				lowSpec.Priority = 1
				lowSpec.Rewards = []MarketingActivityRewardSpec{gift}
				low := marketingActivityCreateTest(t, db, admin.Id, "low", lowSpec, 90)
				stackSpec := lowSpec
				stackSpec.Name = "mv2act_stack"
				stackSpec.ExclusiveGroup = ""
				stack := marketingActivityCreateTest(t, db, admin.Id, "stack", stackSpec, 90)
				firstSpec := stackSpec
				firstSpec.Name = "mv2act_first"
				firstSpec.Condition = "first_successful_payment"
				firstSpec.Frequency = "once"
				first := marketingActivityCreateTest(t, db, admin.Id, "first", firstSpec, 90)
				p1 := marketingActivityPaymentTest(t, db, user.Id, "order1", 60, 110, "100")
				p2 := marketingActivityPaymentTest(t, db, user.Id, "order2", 40, 120, "100")
				assert.EqualValues(t, 1, p1.Sequence)
				assert.EqualValues(t, 2, p2.Sequence)
				var captured []MarketingActivityFact
				require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&captured).Error)
				require.Len(t, captured, 2)
				assert.Equal(t, p1.ID, captured[0].PaymentID)
				assert.Equal(t, p2.ID, captured[1].PaymentID)
				var fact *MarketingActivityFact
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					fact, err = ProcessNextMarketingActivityFact(tx, user.Id, 130)
					return err
				}))
				assert.Equal(t, p1.ID, fact.PaymentID)
				var progress MarketingActivityProgress
				require.NoError(t, db.Where("activity_id = ? AND user_id = ?", high.ID, user.Id).First(&progress).Error)
				assert.EqualValues(t, 60, progress.RemainderCents)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					fact, err = ProcessNextMarketingActivityFact(tx, user.Id, 131)
					return err
				}))
				assert.Equal(t, p2.ID, fact.PaymentID)
				var decisions []MarketingActivityDecision
				require.NoError(t, db.Where("fact_id = ?", fact.ID).Find(&decisions).Error)
				require.Len(t, decisions, 3)
				states := map[int64]string{}
				for _, d := range decisions {
					states[d.ActivityID] = d.State
				}
				assert.Equal(t, "selected", states[high.ID])
				assert.Equal(t, "suppressed", states[low.ID])
				assert.Equal(t, "selected", states[stack.ID])
				assert.NotContains(t, states, first.ID)
				var rewards []MarketingActivityReward
				require.NoError(t, db.Where("fact_id = ?", fact.ID).Order("id").Find(&rewards).Error)
				require.Len(t, rewards, 3)
				assert.Equal(t, "shortage", rewards[1].State)
				progress = MarketingActivityProgress{}
				require.NoError(t, db.Where("activity_id = ? AND user_id = ?", low.ID, user.Id).First(&progress).Error)
				assert.EqualValues(t, 1, progress.ProcessedAwards)
				assert.Zero(t, progress.RemainderCents)
				marketingActivityDeliverTest(t, db, user.Id, 140)
				require.NoError(t, db.First(fact, fact.ID).Error)
				assert.Equal(t, "complete", fact.DeliveryState)
				var gifted []MarketingGift
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifted).Error)
				require.Len(t, gifted, 2)
				assert.EqualValues(t, 100, gifted[0].GrantedQuota)
				assert.EqualValues(t, 150, gifted[0].ExpiresAt)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "act:append", "append", 0, 3, 150)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					again, err := ProcessNextMarketingActivityFact(tx, user.Id, 151)
					assert.Nil(t, again)
					return err
				}))
				require.NoError(t, db.First(&rewards[1], rewards[1].ID).Error)
				assert.Equal(t, "shortage", rewards[1].State)
				marketingActivityPaymentTest(t, db, user.Id, "order3", 100, 160, "100")
				marketingActivityProcessTest(t, db, user.Id, 161)
				var issued int64
				require.NoError(t, db.Model(&MarketingLotteryIssue{}).Where("user_id = ? AND state = ?", user.Id, "issued").Count(&issued).Error)
				assert.EqualValues(t, 1, issued)
				require.NoError(t, db.First(pool, pool.ID).Error)
				assert.EqualValues(t, 3, pool.Issued)
				// A recorded real payment and its event roll back together.
				sentinel := errors.New("abort posting")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "act:rollback", PaidCents: 100, PostedAt: 170, Money: money})
					if err != nil {
						return err
					}
					return sentinel
				}), sentinel)
				var payments, facts int64
				require.NoError(t, db.Model(&MarketingPayment{}).Where("user_id = ?", user.Id).Count(&payments).Error)
				require.NoError(t, db.Model(&MarketingActivityFact{}).Where("user_id = ?", user.Id).Count(&facts).Error)
				assert.Equal(t, payments, facts)
				assert.EqualValues(t, 3, facts)
			})

			t.Run("snapshots_pause_copy_and_first_qualifying_payment", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "snapshot", 1, 0)
				spec := MarketingActivitySpec{Name: "mv2act_snapshot", Kind: "recharge", Condition: "single_payment", Frequency: "once", ThresholdCents: 100, StartsAt: 1100, EndsAt: 2000, Rewards: []MarketingActivityRewardSpec{gift}}
				activity := marketingActivityCreateTest(t, db, admin.Id, "snapshot", spec, 1090)
				marketingActivityPaymentTest(t, db, user.Id, "snap-small", 30, 1110, "30")
				marketingActivityPaymentTest(t, db, user.Id, "snap-first", 100, 1120, "100")
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SetMarketingActivityPaused(tx, admin.Id, activity.ID, "act:pause", true, 1130)
					return err
				}))
				marketingActivityPaymentTest(t, db, user.Id, "snap-paused", 100, 1140, "100")
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SetMarketingActivityPaused(tx, admin.Id, activity.ID, "act:resume", false, 1150)
					return err
				}))
				// New backdated activity is absent from already captured fact snapshots.
				copySpec := spec
				copySpec.Name = "mv2act_copy"
				copySpec.Condition = "cumulative_payments"
				copySpec.Frequency = "every"
				copy := marketingActivityCreateTest(t, db, admin.Id, "copy", copySpec, 1160)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := UpdateMarketingActivity(tx, admin.Id, activity.ID, "act:forbidden", copySpec, 1160)
					return err
				}), ErrMarketingConflict)
				marketingActivityProcessTest(t, db, user.Id, 1170)
				marketingActivityDeliverTest(t, db, user.Id, 1171)
				var rewards []MarketingActivityReward
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&rewards).Error)
				require.Len(t, rewards, 1)
				assert.Equal(t, activity.ID, rewards[0].ActivityID)
				var source MarketingActivityFact
				require.NoError(t, db.First(&source, rewards[0].FactID).Error)
				assert.EqualValues(t, 1120, source.OccurredAt)
				marketingActivityPaymentTest(t, db, user.Id, "snap-copy-small", 70, 1180, "70")
				marketingActivityProcessTest(t, db, user.Id, 1181)
				var copyProgress MarketingActivityProgress
				require.NoError(t, db.Where("activity_id = ? AND user_id = ?", copy.ID, user.Id).First(&copyProgress).Error)
				assert.EqualValues(t, 70, copyProgress.RemainderCents)
				assert.Zero(t, copyProgress.ProcessedAwards)
				marketingActivityPaymentTest(t, db, user.Id, "snap-copy-full", 30, 1190, "30")
				marketingActivityProcessTest(t, db, user.Id, 1191)
				require.NoError(t, db.First(&copyProgress, copyProgress.ID).Error)
				assert.EqualValues(t, 1, copyProgress.ProcessedAwards)
				assert.Zero(t, copyProgress.RemainderCents)
			})

			t.Run("bounded_large_fact_and_capacity_tranche", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "large", 1, 0)
				pool := marketingActivityPoolTest(t, db, admin.Id, "large", 2100, 3000, 1, -1, money)
				spec := MarketingActivitySpec{Name: "mv2act_large", Kind: "recharge", Condition: "cumulative_payments", Frequency: "every", ThresholdCents: 100, StartsAt: 2100, EndsAt: 3000, Rewards: []MarketingActivityRewardSpec{{Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "probability", ProbabilityPPM: 1000000}, {Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "probability", ProbabilityPPM: 0}}}
				marketingActivityCreateTest(t, db, admin.Id, "large", spec, 2090)
				marketingActivityPaymentTest(t, db, user.Id, "large70", 7000, 2110, "7000")
				var fact *MarketingActivityFact
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					fact, err = ProcessNextMarketingActivityFact(tx, user.Id, 2120)
					return err
				}))
				assert.Equal(t, "deciding", fact.State)
				var decision MarketingActivityDecision
				require.NoError(t, db.Where("fact_id = ?", fact.ID).First(&decision).Error)
				assert.EqualValues(t, 70, decision.Count)
				assert.EqualValues(t, 32, decision.NextOrdinal)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "act:large-append", "append", 0, 100, 2130)
					return err
				}))
				marketingActivityProcessTest(t, db, user.Id, 2140)
				var issued, shortage, miss int64
				require.NoError(t, db.Model(&MarketingActivityReward{}).Where("fact_id = ? AND state = ?", fact.ID, "issued").Count(&issued).Error)
				require.NoError(t, db.Model(&MarketingActivityReward{}).Where("fact_id = ? AND state = ?", fact.ID, "shortage").Count(&shortage).Error)
				require.NoError(t, db.Model(&MarketingActivityReward{}).Where("fact_id = ? AND state = ?", fact.ID, "probability_miss").Count(&miss).Error)
				assert.EqualValues(t, 1, issued)
				assert.EqualValues(t, 69, shortage)
				assert.EqualValues(t, 70, miss)
				require.NoError(t, db.First(&decision, decision.ID).Error)
				assert.EqualValues(t, 70, decision.NextOrdinal)
				marketingActivityPaymentTest(t, db, user.Id, "large-new", 100, 2150, "100")
				marketingActivityProcessTest(t, db, user.Id, 2151)
				require.NoError(t, db.First(pool, pool.ID).Error)
				assert.EqualValues(t, 2, pool.Issued)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := IssueMarketingLotteryChancesAtCapacity(tx, user.Id, pool.ID, "act:ceiling-key", 1, 2152, 101)
					return err
				}))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := IssueMarketingLotteryChancesAtCapacity(tx, user.Id, pool.ID, "act:ceiling-key", 1, 2153, 102)
					return err
				}), ErrMarketingConflict)
			})

			t.Run("registration_checkin_invitation_and_amount_sources", func(t *testing.T) {
				inviter := marketingActivityUserTest(t, db, "inviter", 1, 0)
				friend := marketingActivityUserTest(t, db, "friend", 3110, inviter.Id)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := RecordMarketingInvitation(tx, friend.Id, inviter.Id, "act:relation", 3110)
					return err
				}))
				own := MarketingActivitySpec{Name: "mv2act_register", Kind: "registration", Condition: "registration", Frequency: "once", StartsAt: 3100, EndsAt: 4000, Rewards: []MarketingActivityRewardSpec{gift}}
				marketingActivityCreateTest(t, db, admin.Id, "register", own, 3090)
				invited := own
				invited.Name = "mv2act_invite-reg"
				invited.Kind = "invitation"
				invited.Condition = "friend_registration"
				marketingActivityCreateTest(t, db, admin.Id, "invite-reg", invited, 3090)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CaptureMarketingRegistrationFact(tx, friend.Id, "act:registration", 3110)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CaptureMarketingRegistrationFact(tx, friend.Id, "act:registration", 3110)
					return err
				}))
				marketingActivityProcessTest(t, db, friend.Id, 3120)
				marketingActivityDeliverTest(t, db, friend.Id, 3121)
				var ownGifts, inviteGifts int64
				require.NoError(t, db.Model(&MarketingGift{}).Where("user_id = ?", friend.Id).Count(&ownGifts).Error)
				require.NoError(t, db.Model(&MarketingGift{}).Where("user_id = ?", inviter.Id).Count(&inviteGifts).Error)
				assert.EqualValues(t, 1, ownGifts)
				assert.EqualValues(t, 1, inviteGifts)
				paid := MarketingActivitySpec{Name: "mv2act_paid-ratio", Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 1, StartsAt: 3100, EndsAt: 4000, Rewards: []MarketingActivityRewardSpec{{Kind: "gift", Calculation: "paid_ratio", Ratio: "0.5"}, {Kind: "gift", Calculation: "paid_ratio", Ratio: "0.000000000001"}}}
				marketingActivityCreateTest(t, db, admin.Id, "paid-ratio", paid, 3130)
				cash := MarketingActivitySpec{Name: "mv2act_cash-ratio", Kind: "invitation", Condition: "friend_ratio", Frequency: "every", StartsAt: 3100, EndsAt: 4000, Rewards: []MarketingActivityRewardSpec{{Kind: "gift", Calculation: "cash_ratio", Ratio: "0.5", Money: MarketingMoneySnapshot{QuotaPerUnit: "50", CurrencyPerUnit: "1", Currency: "CNY"}}}}
				marketingActivityCreateTest(t, db, admin.Id, "cash-ratio", cash, 3130)
				payment := marketingActivityPaymentTest(t, db, friend.Id, "ratio", 100, 3140, "20") // actual ¥1, credited 20 internal quota
				marketingActivityProcessTest(t, db, friend.Id, 3141)
				var ratioFact MarketingActivityFact
				require.NoError(t, db.Where("payment_id = ?", payment.ID).First(&ratioFact).Error)
				var ratioRewards []MarketingActivityReward
				require.NoError(t, db.Where("fact_id = ?", ratioFact.ID).Order("id").Find(&ratioRewards).Error)
				require.Len(t, ratioRewards, 3)
				assert.Equal(t, "zero_amount", ratioRewards[1].State)
				marketingActivityDeliverTest(t, db, friend.Id, 3142)
				var paidGift, cashGift MarketingGift
				require.NoError(t, db.Where("source_reference = ?", "activity:"+ratioRewards[0].RewardKey).First(&paidGift).Error)
				require.NoError(t, db.Where("source_reference = ?", "activity:"+ratioRewards[2].RewardKey).First(&cashGift).Error)
				assert.EqualValues(t, 10, paidGift.GrantedQuota)
				assert.EqualValues(t, 25, cashGift.GrantedQuota)
				var paidEvidence MarketingRewardAmount
				require.NoError(t, common.UnmarshalJsonStr(ratioRewards[0].Amount, &paidEvidence))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := GrantMarketingRewardGift(tx, MarketingGiftInput{UserID: friend.Id, Kind: "invitation", Reference: "act:wrong-ratio-kind", GrantedAt: 3143}, paidEvidence)
					return err
				}), ErrMarketingInput)

				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					again, err := FulfillMarketingActivityReward(tx, friend.Id, ratioRewards[0].ID, 3200)
					if err == nil {
						assert.Equal(t, paidGift.ID, again.GiftID)
						assert.EqualValues(t, 3142, again.CreditedAt)
					}
					return err
				}))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := FulfillMarketingActivityReward(tx, inviter.Id, ratioRewards[0].ID, 3200)
					return err
				}), ErrMarketingAccess)
				checkin := MarketingActivitySpec{Name: "mv2act_checkin", Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: 3100, EndsAt: 4000, Rewards: []MarketingActivityRewardSpec{gift}, Checkin: &MarketingActivityCheckinSpec{Condition: "none", Milestones: []MarketingActivityMilestone{{Days: 7, Rewards: []MarketingActivityRewardSpec{gift}}}}}
				checkinActivity := marketingActivityCreateTest(t, db, admin.Id, "checkin", checkin, 3150)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CaptureMarketingCheckinFact(tx, friend.Id, "act:checkin", "2026-11-23", 3160, []MarketingActivityCheckinClaim{{ActivityID: checkinActivity.ID, Ordinal: 1}, {ActivityID: checkinActivity.ID, MilestoneDays: 7, Ordinal: 1}})
					return err
				}))
				marketingActivityProcessTest(t, db, friend.Id, 3161)
				marketingActivityDeliverTest(t, db, friend.Id, 3162)
				var daily int64
				require.NoError(t, db.Model(&MarketingActivityReward{}).Where("activity_id = ? AND user_id = ? AND state = ?", checkinActivity.ID, friend.Id, "credited").Count(&daily).Error)
				assert.EqualValues(t, 2, daily)
			})

			t.Run("disabled_payment_and_beneficiary_do_not_erase_other_awards", func(t *testing.T) {
				inviter := marketingActivityUserTest(t, db, "disabled_inv", 1, 0)
				friend := marketingActivityUserTest(t, db, "disabled_friend", 4110, inviter.Id)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := RecordMarketingInvitation(tx, friend.Id, inviter.Id, "act:disabled-relation", 4110)
					return err
				}))
				pool := marketingActivityPoolTest(t, db, admin.Id, "disabled", 4100, 5000, 2, 2, money)
				own := MarketingActivitySpec{Name: "mv2act_disabled-own", Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 1, StartsAt: 4100, EndsAt: 5000, Rewards: []MarketingActivityRewardSpec{gift}}
				marketingActivityCreateTest(t, db, admin.Id, "disabled-own", own, 4090)
				invited := own
				invited.Name = "mv2act_disabled-invite"
				invited.Kind = "invitation"
				invited.Condition = "friend_cumulative"
				invited.Rewards = []MarketingActivityRewardSpec{gift, {Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "direct"}}
				marketingActivityCreateTest(t, db, admin.Id, "disabled-invite", invited, 4090)
				require.NoError(t, db.Model(&User{}).Where("id IN ?", []int{friend.Id, inviter.Id}).Update("status", common.UserStatusDisabled).Error)
				payment := marketingActivityPaymentTest(t, db, friend.Id, "disabled-paid", 1, 4120, "10")
				assert.Positive(t, payment.ID)
				marketingActivityProcessTest(t, db, friend.Id, 4121)
				require.NoError(t, db.Model(&User{}).Where("id = ?", friend.Id).Update("status", common.UserStatusEnabled).Error)
				var items []MarketingActivityReward
				require.NoError(t, db.Where("user_id = ?", friend.Id).Find(&items).Error)
				require.Len(t, items, 1)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := FulfillMarketingActivityReward(tx, friend.Id, items[0].ID, 4122)
					return err
				}))
				var invItems []MarketingActivityReward
				require.NoError(t, db.Where("user_id = ?", inviter.Id).Order("id").Find(&invItems).Error)
				require.Len(t, invItems, 2)
				assert.Equal(t, "issued", invItems[1].State)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := FulfillMarketingActivityReward(tx, inviter.Id, invItems[0].ID, 4122)
					return err
				}), ErrMarketingAccess)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := DrawMarketingLotteryChance(tx, inviter.Id, pool.ID, "act:disabled-draw", 4122)
					return err
				}), ErrMarketingAccess)
				require.NoError(t, db.First(pool, pool.ID).Error)
				assert.EqualValues(t, 1, pool.Issued)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					work, err := GetMarketingActivityDeliveryWork(tx, friend.Id, 0, 128)
					if err == nil {
						require.Len(t, work.Rewards, 1)
						assert.Equal(t, inviter.Id, work.Rewards[0].UserID)
					}
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					work, err := GetMarketingActivityDeliveryWork(tx, inviter.Id, 0, 128)
					if err == nil {
						assert.Empty(t, work.Rewards)
					}
					return err
				}))
			})

			t.Run("delivery_cursor_rotates_failed_prefix_and_current_snapshot", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "cursor", 1, 0)
				second := gift
				second.Money.Amount = "0.2"
				spec := MarketingActivitySpec{Name: "mv2act_cursor", Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 1, StartsAt: 6100, EndsAt: 7000, Rewards: []MarketingActivityRewardSpec{gift, second}}
				marketingActivityCreateTest(t, db, admin.Id, "cursor", spec, 6090)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := GrantMarketingGift(tx, MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: "act:cursor-cap", GrantedAt: 6100, Money: MarketingMoneySnapshot{Amount: strconv.FormatInt(common.MaxWalletQuota-50, 10), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
					return err
				}))
				var stale *gorm.DB
				if dialect != "sqlite" {
					stale = db.Begin()
					require.NoError(t, stale.Error)
					defer func() { _ = stale.Rollback().Error }()
					var previous User
					require.NoError(t, stale.First(&previous, user.Id).Error)
				}
				payment := marketingActivityPaymentTest(t, db, user.Id, "cursor", 1, 6110, "1")
				marketingActivityProcessTest(t, db, user.Id, 6111)
				if stale != nil {
					work, err := GetMarketingActivityDeliveryWork(stale, user.Id, 0, 128)
					require.NoError(t, err)
					require.Len(t, work.Rewards, 2)
					require.NoError(t, stale.Commit().Error)
				}
				var firstWork, secondWork, wrapped *MarketingActivityDeliveryWork
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					firstWork, err = NextMarketingActivityDeliveryWork(tx, user.Id, 1)
					return err
				}))
				require.Len(t, firstWork.Rewards, 1)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := FulfillMarketingActivityReward(tx, user.Id, firstWork.Rewards[0].ID, 6112)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					secondWork, err = NextMarketingActivityDeliveryWork(tx, user.Id, 1)
					return err
				}))
				require.Len(t, secondWork.Rewards, 1)
				assert.Greater(t, secondWork.Rewards[0].ID, firstWork.Rewards[0].ID)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := FulfillMarketingActivityReward(tx, user.Id, secondWork.Rewards[0].ID, 6113)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					wrapped, err = NextMarketingActivityDeliveryWork(tx, user.Id, 1)
					return err
				}))
				require.Len(t, wrapped.Rewards, 1)
				assert.Equal(t, firstWork.Rewards[0].ID, wrapped.Rewards[0].ID)
				var fact MarketingActivityFact
				require.NoError(t, db.Where("payment_id = ?", payment.ID).First(&fact).Error)
				assert.Equal(t, "decided", fact.State)
				assert.Equal(t, "pending", fact.DeliveryState)
				var first, delivered MarketingActivityReward
				require.NoError(t, db.First(&first, firstWork.Rewards[0].ID).Error)
				require.NoError(t, db.First(&delivered, secondWork.Rewards[0].ID).Error)
				assert.Equal(t, "pending", first.State)
				assert.Zero(t, first.GiftID)
				assert.Equal(t, "credited", delivered.State)
				assert.Positive(t, delivered.GiftID)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					again, err := FulfillMarketingActivityReward(tx, user.Id, delivered.ID, 6114)
					if err == nil {
						assert.Equal(t, delivered.GiftID, again.GiftID)
					}
					return err
				}))
			})

			t.Run("overlapping_workers_plan_and_credit_once", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "concurrent", 1, 0)
				pool := marketingActivityPoolTest(t, db, admin.Id, "concurrent", 7100, 8000, 10, -1, money)
				spec := MarketingActivitySpec{Name: "mv2act_concurrent_high", Kind: "recharge", Condition: "cumulative_payments", Frequency: "every", ThresholdCents: 100, StartsAt: 7100, EndsAt: 8000, ExclusiveGroup: "concurrent", Priority: 2, Rewards: []MarketingActivityRewardSpec{
					gift,
					{Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "probability", ProbabilityPPM: 1000000},
					{Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "probability", ProbabilityPPM: 0},
					{Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "probability", ProbabilityPPM: 500000},
				}}
				high := marketingActivityCreateTest(t, db, admin.Id, "concurrent-high", spec, 7090)
				spec.Name, spec.Priority = "mv2act_concurrent_low", 1
				spec.Rewards = []MarketingActivityRewardSpec{gift}
				low := marketingActivityCreateTest(t, db, admin.Id, "concurrent-low", spec, 7090)
				spec.Name, spec.ExclusiveGroup = "mv2act_concurrent_stack", ""
				stack := marketingActivityCreateTest(t, db, admin.Id, "concurrent-stack", spec, 7090)
				payment := marketingActivityPaymentTest(t, db, user.Id, "concurrent", 150, 7110, "300")
				facts := make([]*MarketingActivityFact, 2)
				marketingActivityConcurrentTest(t, user.Id, func(tx *gorm.DB, worker int) error {
					var err error
					facts[worker], err = ProcessNextMarketingActivityFact(tx, user.Id, 7120)
					return err
				})
				processed := 0
				for _, fact := range facts {
					if fact != nil {
						processed++
						assert.Equal(t, payment.ID, fact.PaymentID)
					}
				}
				assert.Equal(t, 1, processed)
				var fact MarketingActivityFact
				require.NoError(t, db.Where("user_id = ?", user.Id).First(&fact).Error)
				assert.Equal(t, "decided", fact.State)
				assert.Equal(t, "pending", fact.DeliveryState)
				var decisions []MarketingActivityDecision
				require.NoError(t, db.Where("fact_id = ?", fact.ID).Order("id").Find(&decisions).Error)
				require.Len(t, decisions, 3)
				for _, decision := range decisions {
					assert.EqualValues(t, 1, decision.Count)
					if decision.ActivityID == low.ID {
						assert.Equal(t, "suppressed", decision.State)
					} else {
						assert.Contains(t, []int64{high.ID, stack.ID}, decision.ActivityID)
						assert.Equal(t, "selected", decision.State)
						assert.EqualValues(t, 1, decision.NextOrdinal)
					}
				}
				var progress []MarketingActivityProgress
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&progress).Error)
				require.Len(t, progress, 3)
				for _, p := range progress {
					assert.EqualValues(t, 1, p.ProcessedAwards)
					assert.EqualValues(t, 50, p.RemainderCents)
					assert.Equal(t, fact.ID, p.LastFactID)
				}
				var rewards []MarketingActivityReward
				require.NoError(t, db.Where("fact_id = ?", fact.ID).Order("activity_id, position").Find(&rewards).Error)
				require.Len(t, rewards, 5)
				assert.Equal(t, "pending", rewards[0].State)
				assert.Equal(t, "issued", rewards[1].State)
				assert.Equal(t, "probability_miss", rewards[2].State)
				assert.Contains(t, []string{"issued", "probability_miss"}, rewards[3].State)
				assert.Equal(t, "pending", rewards[4].State)
				issued := int64(1)
				if rewards[3].State == "issued" {
					issued++
				}
				var issues []MarketingLotteryIssue
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&issues).Error)
				require.Len(t, issues, int(issued))
				for _, issue := range issues {
					assert.Contains(t, []int64{rewards[1].IssueID, rewards[3].IssueID}, issue.ID)
					assert.Equal(t, "issued", issue.State)
					assert.EqualValues(t, 1, issue.Count)
				}
				var chances []MarketingLotteryChance
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&chances).Error)
				require.Len(t, chances, int(issued))
				for _, chance := range chances {
					assert.EqualValues(t, 1, chance.Granted)
					assert.EqualValues(t, 1, chance.Remaining)
				}
				require.NoError(t, db.First(pool, pool.ID).Error)
				assert.Equal(t, issued, pool.Issued)
				// Repeat planning while both workers hold pre-call snapshots. Neither
				// an already issued chance nor the real 50% decision can be rerolled.
				marketingActivityConcurrentTest(t, user.Id, func(tx *gorm.DB, worker int) error {
					var err error
					facts[worker], err = ProcessNextMarketingActivityFact(tx, user.Id, 7121)
					return err
				})
				assert.Nil(t, facts[0])
				assert.Nil(t, facts[1])
				var repeated []MarketingActivityReward
				require.NoError(t, db.Where("fact_id = ?", fact.ID).Order("activity_id, position").Find(&repeated).Error)
				assert.Equal(t, rewards, repeated)
				var repeatedDecisions []MarketingActivityDecision
				require.NoError(t, db.Where("fact_id = ?", fact.ID).Order("id").Find(&repeatedDecisions).Error)
				assert.Equal(t, decisions, repeatedDecisions)
				credited := make([]*MarketingActivityReward, 2)
				marketingActivityConcurrentTest(t, user.Id, func(tx *gorm.DB, worker int) error {
					var err error
					credited[worker], err = FulfillMarketingActivityReward(tx, user.Id, rewards[0].ID, 7130)
					return err
				})
				require.NotNil(t, credited[0])
				require.NotNil(t, credited[1])
				assert.Positive(t, credited[0].GiftID)
				assert.Equal(t, credited[0], credited[1])
				var gifts []MarketingGift
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifts).Error)
				require.Len(t, gifts, 1)
				assert.Equal(t, credited[0].GiftID, gifts[0].ID)
				assert.EqualValues(t, 100, gifts[0].GrantedQuota)
				assert.EqualValues(t, 100, gifts[0].RemainingQuota)
				assert.EqualValues(t, 7140, gifts[0].ExpiresAt)
				var entries int64
				require.NoError(t, db.Model(&MarketingLedgerEntry{}).Where("user_id = ?", user.Id).Count(&entries).Error)
				assert.EqualValues(t, 2, entries, "one payment and one gift entry")
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 300, user.Quota, "gifts never increase paid money")
				marketingActivityDeliverTest(t, db, user.Id, 7131)
				require.NoError(t, db.First(&fact, fact.ID).Error)
				assert.Equal(t, "complete", fact.DeliveryState)
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifts).Error)
				assert.Len(t, gifts, 2, "only the selected and stackable activities award gifts")
				require.NoError(t, db.First(pool, pool.ID).Error)
				assert.Equal(t, issued, pool.Issued)
			})

			t.Run("overlapping_users_compete_for_last_chance_without_losing_gifts", func(t *testing.T) {
				users := []User{
					marketingActivityUserTest(t, db, "last_slot_a", 1, 0),
					marketingActivityUserTest(t, db, "last_slot_b", 1, 0),
				}
				pool := marketingActivityPoolTest(t, db, admin.Id, "last_slot", 8100, 9000, 1, -1, money)
				spec := MarketingActivitySpec{Name: "mv2act_last_slot", Kind: "recharge", Condition: "single_payment", Frequency: "every", ThresholdCents: 100, StartsAt: 8100, EndsAt: 9000, Rewards: []MarketingActivityRewardSpec{gift, {Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "direct"}}}
				marketingActivityCreateTest(t, db, admin.Id, "last-slot", spec, 8090)
				for i, user := range users {
					marketingActivityPaymentTest(t, db, user.Id, "last-slot-"+strconv.Itoa(i), 100, 8110, "100")
				}
				facts := make([]*MarketingActivityFact, 2)
				marketingActivityConcurrentTest(t, users[0].Id, func(tx *gorm.DB, worker int) error {
					var err error
					facts[worker], err = ProcessNextMarketingActivityFact(tx, users[worker].Id, 8120)
					return err
				})
				giftRewards := make([]MarketingActivityReward, 2)
				chanceRewards := make([]MarketingActivityReward, 2)
				states := []string{}
				for i, fact := range facts {
					require.NotNil(t, fact)
					assert.Equal(t, users[i].Id, fact.UserID)
					assert.Equal(t, "decided", fact.State)
					var rewards []MarketingActivityReward
					require.NoError(t, db.Where("fact_id = ?", fact.ID).Order("position").Find(&rewards).Error)
					require.Len(t, rewards, 2)
					giftRewards[i], chanceRewards[i] = rewards[0], rewards[1]
					assert.Equal(t, "pending", giftRewards[i].State)
					states = append(states, chanceRewards[i].State)
				}
				assert.ElementsMatch(t, []string{"issued", "shortage"}, states)
				var chances []MarketingLotteryChance
				require.NoError(t, db.Where("pool_id = ?", pool.ID).Find(&chances).Error)
				require.Len(t, chances, 1)
				for _, reward := range chanceRewards {
					if reward.State == "issued" {
						assert.Equal(t, reward.UserID, chances[0].UserID)
						assert.Equal(t, reward.IssueID, chances[0].IssueID)
					}
				}
				assert.EqualValues(t, 1, chances[0].Remaining)
				require.NoError(t, db.First(pool, pool.ID).Error)
				assert.EqualValues(t, 1, pool.Issued)
				marketingActivityConcurrentTest(t, users[0].Id, func(tx *gorm.DB, worker int) error {
					_, err := FulfillMarketingActivityReward(tx, users[worker].Id, giftRewards[worker].ID, 8130)
					return err
				})
				for i, user := range users {
					var gifts []MarketingGift
					require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifts).Error)
					require.Len(t, gifts, 1)
					assert.EqualValues(t, 100, gifts[0].RemainingQuota)
					require.NoError(t, db.First(&user, user.Id).Error)
					assert.Equal(t, 100, user.Quota)
					require.NoError(t, db.First(facts[i], facts[i].ID).Error)
					assert.Equal(t, "complete", facts[i].DeliveryState)
				}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := MutateMarketingLotteryPool(tx, admin.Id, pool.ID, "act:last-slot-append", "append", 0, 1, 8140)
					return err
				}))
				marketingActivityConcurrentTest(t, users[0].Id, func(tx *gorm.DB, worker int) error {
					var err error
					facts[worker], err = ProcessNextMarketingActivityFact(tx, users[worker].Id, 8150)
					return err
				})
				assert.Nil(t, facts[0])
				assert.Nil(t, facts[1])
				for _, prior := range chanceRewards {
					var current MarketingActivityReward
					require.NoError(t, db.First(&current, prior.ID).Error)
					assert.Equal(t, prior, current, "appending capacity does not change the winner or rescue historical shortage")
				}
				require.NoError(t, db.First(pool, pool.ID).Error)
				assert.EqualValues(t, 2, pool.Capacity)
				assert.EqualValues(t, 1, pool.Issued)
			})

			t.Run("transaction_failure_rolls_back_gift_without_unsafe_retry", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "retry_errors", 1, 0)
				for _, failure := range []struct {
					name string
					err  error
				}{
					{"business", ErrMarketingInput},
					{"lock_wait_timeout", &mysql.MySQLError{Number: 1205, Message: "Lock wait timeout exceeded"}},
					{"connection_lost", driver.ErrBadConn},
					{"transport_eof", io.ErrUnexpectedEOF},
				} {
					attempts := 0
					err := WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
						attempts++
						if _, err := GrantMarketingGift(tx, MarketingGiftInput{UserID: user.Id, Kind: "admin_gift", Reference: "act:retry-" + failure.name, GrantedAt: 9100, Money: money}); err != nil {
							return err
						}
						return failure.err
					})
					require.ErrorIs(t, err, failure.err, failure.name)
					assert.Equal(t, 1, attempts, failure.name)
				}
				for _, record := range []any{&MarketingGift{}, &MarketingLedgerEntry{}} {
					var count int64
					require.NoError(t, db.Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count, "failed transaction must not leave a gift or ledger entry")
				}
			})

			t.Run("server_rejects_unapproved_combinations", func(t *testing.T) {
				base := MarketingActivitySpec{Name: "mv2act_invalid", Kind: "recharge", Condition: "cumulative_payments", Frequency: "every", ThresholdCents: 100, StartsAt: 5100, EndsAt: 6000, Rewards: []MarketingActivityRewardSpec{gift}}
				cases := []MarketingActivitySpec{}
				invalid := base
				invalid.Rewards = []MarketingActivityRewardSpec{{Kind: "gift", Calculation: "paid_ratio", Ratio: "0.1"}}
				cases = append(cases, invalid)
				invalid = base
				invalid.Kind = "registration"
				invalid.Condition = "registration"
				invalid.Frequency = "once"
				invalid.Rewards = []MarketingActivityRewardSpec{{Kind: "chance", PoolID: 1, Count: 1, ChanceMode: "probability", ProbabilityPPM: 500000}}
				cases = append(cases, invalid)
				invalid = base
				invalid.Kind = "invitation"
				invalid.Condition = "friend_cumulative"
				invalid.Rewards = []MarketingActivityRewardSpec{{Kind: "gift", Calculation: "cash_ratio", Ratio: "0.1", Money: money}}
				cases = append(cases, invalid)
				invalid = base
				invalid.Rewards = []MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", Money: money, ChanceMode: "probability"}}
				cases = append(cases, invalid)
				for i, spec := range cases {
					require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := CreateMarketingActivity(tx, admin.Id, "act:invalid:"+strconv.Itoa(i), spec, 5090)
						return err
					}), ErrMarketingInput)
				}
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CreateMarketingActivity(tx, marketingActivityUserTest(t, db, "unauthorized", 1, 0).Id, "act:unauthorized", base, 5090)
					return err
				}), ErrMarketingAccess)
			})
		})
	}
}

// Both first attempts have begun a transaction and read the database before
// either business operation starts. Transactions therefore occupy distinct
// physical connections and overlap even on a single-core test machine. Only
// the production whole-transaction retry handles SQLite contention and MySQL
// deadlock victims; failed operations are never retried statement by statement.
func marketingActivityConcurrentTest(t *testing.T, userID int, apply func(*gorm.DB, int) error, permitted ...error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
	}()
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	for worker := range 2 {
		workers.Go(func() {
			first := true
			results <- WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
				if first {
					first = false
					var user User
					if err := tx.First(&user, userID).Error; err != nil {
						return err
					}
					ready <- struct{}{}
					select {
					case <-start:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return apply(tx, worker)
			})
		})
	}
	for range 2 {
		select {
		case <-ready:
		case err := <-results:
			t.Fatalf("worker ended before overlapping transactions were ready: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(start)
	for range 2 {
		select {
		case err := <-results:
			if len(permitted) > 0 && errors.Is(err, permitted[0]) {
				continue
			}
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func marketingRegistrationIdentityTest(name, column, subject string) User {
	user := User{Username: name, Role: common.RoleCommonUser}
	switch column {
	case "github_id":
		user.GitHubId = subject
	case "discord_id":
		user.DiscordId = subject
	case "oidc_id":
		user.OidcId = subject
	case "linux_do_id":
		user.LinuxDOId = subject
	case "wechat_id":
		user.WeChatId = subject
	}
	return user
}

func marketingActivityUserTest(t *testing.T, db *gorm.DB, name string, at int64, inviter int) User {
	t.Helper()
	user := User{Username: "mv2act_" + name, AffCode: "mv2act_" + name, CreatedAt: at, InviterId: inviter, Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: MarketingPaymentHistoryNone, PaymentHistoryEvidence: "registration:" + name}).Error)
	return user
}
func marketingActivityCreateTest(t *testing.T, db *gorm.DB, admin int, key string, spec MarketingActivitySpec, now int64) *MarketingActivity {
	t.Helper()
	var activity *MarketingActivity
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		activity, err = CreateMarketingActivity(tx, admin, "activity-create:"+key, spec, now)
		if err != nil {
			return err
		}
		activity, err = ActivateMarketingActivity(tx, admin, activity.ID, "activity-activate:"+key, now)
		return err
	}))
	return activity
}
func marketingActivityPoolTest(t *testing.T, db *gorm.DB, admin int, name string, start, end, capacity, stock int64, money MarketingMoneySnapshot) *MarketingLotteryPool {
	t.Helper()
	var pool *MarketingLotteryPool
	spec := MarketingLotteryPoolSpec{Name: "mv2act_" + name, Style: "wheel", StartsAt: start, EndsAt: end, Capacity: capacity, Prizes: []MarketingLotteryPrizeSpec{{Name: "gift", Weight: 1, Stock: stock, Gifts: []MarketingLotteryGiftSpec{{Money: money}}}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		pool, err = CreateMarketingLotteryPool(tx, admin, "activity-pool:"+name, spec, start-10)
		return err
	}))
	return pool
}
func marketingActivityPaymentTest(t *testing.T, db *gorm.DB, userID int, key string, cents, at int64, credited string) *MarketingPayment {
	t.Helper()
	var payment *MarketingPayment
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		payment, err = CreditMarketingPayment(tx, MarketingPaymentInput{UserID: userID, Kind: "online", Reference: "activity-pay:" + key, PaidCents: cents, PostedAt: at, Money: MarketingMoneySnapshot{Amount: credited, QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
		return err
	}))
	return payment
}
func marketingActivityProcessTest(t *testing.T, db *gorm.DB, userID int, now int64) {
	t.Helper()
	for {
		var fact *MarketingActivityFact
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			fact, err = ProcessNextMarketingActivityFact(tx, userID, now)
			return err
		}))
		if fact == nil {
			return
		}
	}
}
func marketingActivityDeliverTest(t *testing.T, db *gorm.DB, sourceUserID int, now int64) {
	t.Helper()
	var work *MarketingActivityDeliveryWork
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		work, err = GetMarketingActivityDeliveryWork(tx, sourceUserID, 0, 256)
		return err
	}))
	for _, reward := range work.Rewards {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := FulfillMarketingActivityReward(tx, reward.UserID, reward.ID, now)
			return err
		}))
	}
}
