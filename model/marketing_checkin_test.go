package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/marketing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMarketingCheckin(t *testing.T) {
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
			common.SQLitePath = filepath.Join(t.TempDir(), "checkin.sqlite")
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
			require.NoError(t, db.AutoMigrate(&User{}, &Checkin{}))
			require.NoError(t, migrateMarketingV2(db))
			cleanup := func() {
				users := db.Unscoped().Model(&User{}).Select("id").Where("username LIKE ?", "mv2act_ci_%")
				for _, record := range []any{&MarketingCheckinDay{}, &MarketingCheckinClaim{}, &MarketingActivityDeliveryCursor{}, &MarketingActivityReward{}, &MarketingActivityDecision{}, &MarketingActivityProgress{}, &MarketingActivityFact{}, &MarketingInvitation{}, &MarketingGift{}, &MarketingLedgerEntry{}, &MarketingPayment{}, &MarketingAccount{}, &Checkin{}} {
					require.NoError(t, db.Where("user_id IN (?)", users).Delete(record).Error)
				}
				require.NoError(t, db.Where("actor_id IN (?)", users).Delete(&MarketingActivityOperation{}).Error)
				require.NoError(t, db.Where("name LIKE ?", "mv2act_ci_%").Delete(&MarketingActivity{}).Error)
				require.NoError(t, db.Unscoped().Where("username LIKE ?", "mv2act_ci_%").Delete(&User{}).Error)
			}
			cleanup()
			t.Cleanup(cleanup)
			admin := marketingActivityUserTest(t, db, "ci_admin", 1, 0)
			require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
			money := MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "100", CurrencyPerUnit: "1", Currency: "CNY"}
			daily := MarketingActivityRewardSpec{Kind: "gift", Calculation: "fixed", Money: money, ValidSeconds: 86400}
			bonus := daily
			bonus.Money.Amount = "2"

			t.Run("daily_and_once_milestone_are_separate_and_replay_safe", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_seven", 1, 0)
				start := checkinTimeTest(t, "2027-01-01")
				spec := MarketingActivitySpec{Name: "mv2act_ci_seven", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 30*86400, Rewards: []MarketingActivityRewardSpec{daily}, Checkin: &MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 10, MakeupLimit: 2, MakeupDaily: true, Milestones: []MarketingActivityMilestone{{Days: 7, Rewards: []MarketingActivityRewardSpec{bonus}}}}}
				activity := marketingActivityCreateTest(t, db, admin.Id, "ci-seven", spec, start-100)
				require.NoError(t, db.Create(&Checkin{UserId: user.Id, CheckinDate: "2026-12-31", QuotaAwarded: 99999}).Error)
				for day := range 7 {
					now := start + int64(day)*86400 + 3600
					first := commitCheckinTest(t, db, user.Id, "", now)
					again := commitCheckinTest(t, db, user.Id, "", now+1)
					assert.True(t, again.AlreadyRecorded)
					assert.Equal(t, first.FactID, again.FactID)
					marketingActivityProcessTest(t, db, user.Id, now+2)
					marketingActivityDeliverTest(t, db, user.Id, now+3)
				}
				var gifts []MarketingGift
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&gifts).Error)
				require.Len(t, gifts, 8)
				var total int64
				for _, gift := range gifts {
					total += gift.GrantedQuota
				}
				assert.EqualValues(t, 900, total)
				var record User
				require.NoError(t, db.First(&record, user.Id).Error)
				assert.Zero(t, record.Quota)
				var claims []MarketingCheckinClaim
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&claims).Error)
				require.Len(t, claims, 1)
				assert.EqualValues(t, 1, claims[0].Ordinal)
				cal, err := GetMarketingCheckinCalendar(context.Background(), user.Id, "2027-01", start+6*86400+4000)
				require.NoError(t, err)
				require.Len(t, cal.Activities, 1)
				assert.Equal(t, 7, cal.Activities[0].Streak)
				assert.Len(t, cal.Activities[0].Records, 7)
				assert.Equal(t, "already_signed", cal.Activities[0].TodayEligibility)
				assert.Equal(t, activity.ID, cal.Activities[0].Activity.ID)
				_, err = UserCheckin(user.Id)
				require.Error(t, err)
			})

			t.Run("makeup_merges_cycles_without_replaying_promised_rewards", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_cycles", 1, 0)
				start := checkinTimeTest(t, "2027-02-01")
				spec := MarketingActivitySpec{Name: "mv2act_ci_cycle", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 28*86400, Checkin: &MarketingActivityCheckinSpec{Condition: "none", CycleDays: 7, MakeupDays: 14, MakeupLimit: 2, Milestones: []MarketingActivityMilestone{{Days: 7, Rewards: []MarketingActivityRewardSpec{bonus}}}}}
				marketingActivityCreateTest(t, db, admin.Id, "ci-cycle", spec, start-10)
				for day := range 15 {
					if day == 7 {
						continue
					}
					commitCheckinTest(t, db, user.Id, "", start+int64(day)*86400+3600)
				}
				// Neither claim has been delivered yet. Makeup must count commitments.
				commitCheckinTest(t, db, user.Id, "2027-02-08", start+14*86400+4000)
				var claims []MarketingCheckinClaim
				require.NoError(t, db.Where("user_id = ?", user.Id).Order("ordinal").Find(&claims).Error)
				require.Len(t, claims, 2)
				for day := 15; day < 21; day++ {
					commitCheckinTest(t, db, user.Id, "", start+int64(day)*86400+3600)
				}
				require.NoError(t, db.Where("user_id = ?", user.Id).Order("ordinal").Find(&claims).Error)
				require.Len(t, claims, 3)
				assert.EqualValues(t, 3, claims[2].Ordinal)
				marketingActivityProcessTest(t, db, user.Id, start+21*86400)
				marketingActivityDeliverTest(t, db, user.Id, start+21*86400)
				var gifts int64
				require.NoError(t, db.Model(&MarketingGift{}).Where("user_id = ?", user.Id).Count(&gifts).Error)
				assert.EqualValues(t, 3, gifts)
			})

			t.Run("merged_surplus_does_not_consume_new_segment_reward", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_anchor", 1, 0)
				start := checkinTimeTest(t, "2027-08-01")
				spec := MarketingActivitySpec{Name: "mv2act_ci_anchor", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 20*86400, Checkin: &MarketingActivityCheckinSpec{Condition: "none", CycleDays: 7, MakeupDays: 5, MakeupLimit: 1, Milestones: []MarketingActivityMilestone{{Days: 1, Rewards: []MarketingActivityRewardSpec{bonus}}}}}
				marketingActivityCreateTest(t, db, admin.Id, "ci-anchor", spec, start-10)
				commitCheckinTest(t, db, user.Id, "", start+3600)
				commitCheckinTest(t, db, user.Id, "", start+2*86400+3600)
				commitCheckinTest(t, db, user.Id, "2027-08-02", start+2*86400+4000)
				var count int64
				require.NoError(t, db.Model(&MarketingCheckinClaim{}).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.EqualValues(t, 2, count)
				commitCheckinTest(t, db, user.Id, "", start+4*86400+3600)
				require.NoError(t, db.Model(&MarketingCheckinClaim{}).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.EqualValues(t, 3, count)
			})

			t.Run("makeup_original_qualification_cross_month_and_copy_boundaries", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_paid", 1, 0)
				start := checkinTimeTest(t, "2027-03-25")
				spec := MarketingActivitySpec{Name: "mv2act_ci_paid", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: checkinTimeTest(t, "2027-04-10"), Rewards: []MarketingActivityRewardSpec{daily}, Checkin: &MarketingActivityCheckinSpec{Condition: "today_paid", ThresholdCents: 100, MakeupDays: 14, MakeupLimit: 2, MakeupDaily: true}}
				activity := marketingActivityCreateTest(t, db, admin.Id, "ci-paid", spec, start-10)
				// Split successful payments count; credited face value is unrelated.
				marketingActivityPaymentTest(t, db, user.Id, "ci-paid-1", 40, start+3600, "9999")
				marketingActivityPaymentTest(t, db, user.Id, "ci-paid-2", 60, start+7200, "1")
				marketingActivityPaymentTest(t, db, user.Id, "ci-paid-3", 100, start+86400+3600, "1")
				first := commitCheckinTest(t, db, user.Id, "2027-03-25", checkinTimeTest(t, "2027-03-30")+3600)
				assert.True(t, first.Makeup)
				commitCheckinTest(t, db, user.Id, "2027-03-26", checkinTimeTest(t, "2027-04-01")+3600)
				var count int64
				require.NoError(t, db.Model(&MarketingCheckinDay{}).Where("user_id = ? AND activity_id = ? AND makeup = ?", user.Id, activity.ID, true).Count(&count).Error)
				assert.EqualValues(t, 2, count)
				marketingActivityPaymentTest(t, db, user.Id, "ci-paid-today", 100, checkinTimeTest(t, "2027-04-01")+4000, "1")
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "2027-03-27", checkinTimeTest(t, "2027-04-01")+5000)
					return err
				}))
				// Copying with a backdated start cannot manufacture a historical activity.
				copySpec := spec
				copySpec.Name = "mv2act_ci_copy"
				copied := marketingActivityCreateTest(t, db, admin.Id, "ci-copy", copySpec, checkinTimeTest(t, "2027-04-01")+5000)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "2027-03-25", checkinTimeTest(t, "2027-04-01")+6000)
					return err
				})) // replay is allowed, but does not attach copy
				require.NoError(t, db.Model(&MarketingCheckinDay{}).Where("user_id = ? AND activity_id = ?", user.Id, copied.ID).Count(&count).Error)
				assert.Zero(t, count)
				marketingActivityPaymentTest(t, db, user.Id, "ci-copy-historical", 100, checkinTimeTest(t, "2027-03-28")+3600, "1")
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "2027-03-28", checkinTimeTest(t, "2027-04-01")+6000)
					return err
				}))

				cal, err := GetMarketingCheckinCalendar(context.Background(), user.Id, "2027-03", checkinTimeTest(t, "2027-04-01")+6000)
				require.NoError(t, err)
				for _, item := range cal.Activities {
					if item.Activity.ID == activity.ID {
						assert.Zero(t, item.MakeupRemaining)
					}
				}
			})

			t.Run("no_qualification_does_not_consume_day_and_previous_day_uses_cash", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_previous", 1, 0)
				start := checkinTimeTest(t, "2027-05-01")
				spec := MarketingActivitySpec{Name: "mv2act_ci_previous", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 20*86400, Rewards: []MarketingActivityRewardSpec{daily}, Checkin: &MarketingActivityCheckinSpec{Condition: "previous_day_paid", ThresholdCents: 100, MakeupDays: 5, MakeupLimit: 2}}
				marketingActivityCreateTest(t, db, admin.Id, "ci-previous", spec, start-10)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "", start+86400+3600)
					return err
				}))
				var count int64
				require.NoError(t, db.Model(&MarketingActivityFact{}).Where("user_id = ? AND kind = ?", user.Id, "checkin").Count(&count).Error)
				assert.Zero(t, count)
				marketingActivityPaymentTest(t, db, user.Id, "ci-before", 100, start+7200, "1")
				result := commitCheckinTest(t, db, user.Id, "", start+86400+4000)
				assert.False(t, result.AlreadyRecorded)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "2027-05-02", start+86400+5000)
					return err
				}))
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "2027-05-20", start+20*86400)
					return err
				}))
			})

			t.Run("exclusive_milestone_only_activity_owns_calendar", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_exclusive", 1, 0)
				start := checkinTimeTest(t, "2027-06-01")
				low := MarketingActivitySpec{Name: "mv2act_ci_low", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 20*86400, ExclusiveGroup: "ci-ex", Rewards: []MarketingActivityRewardSpec{daily}, Checkin: &MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 5, MakeupLimit: 1, MakeupDaily: true}}
				lower := marketingActivityCreateTest(t, db, admin.Id, "ci-low", low, start-10)
				high := low
				high.Name = "mv2act_ci_high"
				high.Priority = 5
				high.Rewards = nil
				high.Checkin = &MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 5, MakeupLimit: 1, Milestones: []MarketingActivityMilestone{{Days: 3, Rewards: []MarketingActivityRewardSpec{bonus}}}}
				higher := marketingActivityCreateTest(t, db, admin.Id, "ci-high", high, start-10)
				calendar, err := GetMarketingCheckinCalendar(context.Background(), user.Id, "2027-06", start+2*86400+3600)
				require.NoError(t, err)
				require.Len(t, calendar.Activities, 2)
				for _, item := range calendar.Activities {
					if item.Activity.ID == lower.ID {
						assert.Equal(t, "suppressed", item.TodayEligibility)
						assert.Empty(t, item.MakeupDates)
					} else {
						assert.Equal(t, "eligible", item.TodayEligibility)
						assert.Contains(t, item.MakeupDates, "2027-06-02")
					}
				}
				require.NoError(t, db.Model(&MarketingActivity{}).Where("id = ?", higher.ID).Update("published", false).Error)
				calendar, err = GetMarketingCheckinCalendar(context.Background(), user.Id, "2027-06", start+2*86400+3600)
				require.NoError(t, err)
				require.Len(t, calendar.Activities, 1)
				assert.Equal(t, lower.ID, calendar.Activities[0].Activity.ID)
				assert.Equal(t, "suppressed", calendar.Activities[0].TodayEligibility)
				assert.Empty(t, calendar.Activities[0].MakeupDates)
				assert.True(t, calendar.CanCheckin)
				assert.False(t, calendar.CheckedInToday)
				first := commitCheckinTest(t, db, user.Id, "", start+3600)
				assert.Equal(t, []int64{higher.ID}, first.ActivityIDs)
				calendar, err = GetMarketingCheckinCalendar(context.Background(), user.Id, "2027-06", start+4000)
				require.NoError(t, err)
				assert.False(t, calendar.CanCheckin)
				assert.True(t, calendar.CheckedInToday)
				commitCheckinTest(t, db, user.Id, "", start+2*86400+3600)
				repaired := commitCheckinTest(t, db, user.Id, "2027-06-02", start+2*86400+4000)
				assert.Equal(t, []int64{higher.ID}, repaired.ActivityIDs)
				var count int64
				require.NoError(t, db.Model(&MarketingCheckinDay{}).Where("user_id = ? AND activity_id = ?", user.Id, lower.ID).Count(&count).Error)
				assert.Zero(t, count)
				marketingActivityProcessTest(t, db, user.Id, start+2*86400+5000)
				marketingActivityDeliverTest(t, db, user.Id, start+2*86400+5001)
				require.NoError(t, db.Model(&MarketingGift{}).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.EqualValues(t, 1, count)
			})

			t.Run("locked_clock_crosses_shanghai_midnight_and_activity_end", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_clock", 1, 0)
				start := checkinTimeTest(t, "2027-09-01")
				spec := MarketingActivitySpec{Name: "mv2act_ci_clock", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 2*86400, Rewards: []MarketingActivityRewardSpec{daily}, Checkin: &MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 5, MakeupLimit: 2}}
				marketingActivityCreateTest(t, db, admin.Id, "ci-clock", spec, start-10)
				var result *MarketingCheckinResult
				require.NoError(t, paidPostingBoundaryTest(t, db, (MarketingActivity{}).TableName(), start+86400-1, start+86400, func(tx *gorm.DB, clock func() int64) error {
					var err error
					result, err = commitMarketingCheckinWithClock(tx, user.Id, "", clock)
					return err
				}))
				assert.Equal(t, "2027-09-02", result.Day)
				require.Error(t, paidPostingBoundaryTest(t, db, (MarketingActivity{}).TableName(), start+2*86400-1, start+2*86400, func(tx *gorm.DB, clock func() int64) error {
					_, err := commitMarketingCheckinWithClock(tx, user.Id, "", clock)
					return err
				}))
			})

			t.Run("historical_pause_and_failed_fact_do_not_create_calendar", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_pause", 1, 0)
				start := checkinTimeTest(t, "2027-10-01")
				spec := MarketingActivitySpec{Name: "mv2act_ci_pause", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 20*86400, Rewards: []MarketingActivityRewardSpec{daily}, Checkin: &MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 5, MakeupLimit: 2, Milestones: []MarketingActivityMilestone{{Days: 1, Rewards: []MarketingActivityRewardSpec{bonus}}}}}
				activity := marketingActivityCreateTest(t, db, admin.Id, "ci-pause", spec, start-10)
				for _, change := range []struct {
					paused bool
					at     int64
					key    string
				}{{true, start, "ci-pause-start"}, {false, start + 86400, "ci-pause-end"}} {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := SetMarketingActivityPaused(tx, admin.Id, activity.ID, change.key, change.paused, change.at)
						return err
					}))
				}
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "2027-10-01", start+86400+3600)
					return err
				}))
				failure := errors.New("calendar write failed")
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("ci:calendar-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == (MarketingCheckinDay{}).TableName() {
						tx.AddError(failure)
					}
				}))
				err := db.Transaction(func(tx *gorm.DB) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "", start+86400+3600)
					return err
				})
				require.NoError(t, db.Callback().Create().Remove("ci:calendar-failure"))
				require.ErrorIs(t, err, failure)
				var count int64
				for _, table := range []any{&MarketingCheckinDay{}, &MarketingCheckinClaim{}, &MarketingActivityFact{}} {
					require.NoError(t, db.Model(table).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
				}
				result := commitCheckinTest(t, db, user.Id, "", start+86400+3600)
				assert.False(t, result.AlreadyRecorded)
			})

			t.Run("makeup_requires_paid_threshold_and_open_interval_to_overlap", func(t *testing.T) {
				for _, tc := range []struct {
					name, day, month string
					resume           int64
					eligible         bool
				}{{"late-payment", "2028-01-01", "2028-01", 25 * 3600, false}, {"resumed-same-day", "2028-02-01", "2028-02", 12 * 3600, true}} {
					t.Run(tc.name, func(t *testing.T) {
						user := marketingActivityUserTest(t, db, "ci_"+tc.name, 1, 0)
						start := checkinTimeTest(t, tc.day)
						spec := MarketingActivitySpec{Name: "mv2act_ci_" + tc.name, Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 20*86400, Rewards: []MarketingActivityRewardSpec{daily}, Checkin: &MarketingActivityCheckinSpec{Condition: "today_paid", ThresholdCents: 100, MakeupDays: 5, MakeupLimit: 2}}
						activity := marketingActivityCreateTest(t, db, admin.Id, "ci-"+tc.name, spec, start-10)
						for _, change := range []struct {
							paused bool
							at     int64
							key    string
						}{{true, start + 10*3600, "pause"}, {false, start + tc.resume, "resume"}} {
							require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
								_, err := SetMarketingActivityPaused(tx, admin.Id, activity.ID, tc.name+change.key, change.paused, change.at)
								return err
							}))
						}
						marketingActivityPaymentTest(t, db, user.Id, "ci-"+tc.name, 100, start+11*3600, "1")
						calendar, err := GetMarketingCheckinCalendar(context.Background(), user.Id, tc.month, start+26*3600)
						require.NoError(t, err)
						require.Len(t, calendar.Activities, 1)
						if tc.eligible {
							assert.Contains(t, calendar.Activities[0].MakeupDates, tc.day)
						} else {
							assert.NotContains(t, calendar.Activities[0].MakeupDates, tc.day)
						}
						err = db.Transaction(func(tx *gorm.DB) error {
							_, err := CommitMarketingCheckin(tx, user.Id, tc.day, start+26*3600)
							return err
						})
						if tc.eligible {
							require.NoError(t, err)
						} else {
							require.Error(t, err)
						}
					})
				}
			})

			t.Run("two_connections_same_day_and_last_makeup", func(t *testing.T) {
				user := marketingActivityUserTest(t, db, "ci_parallel", 1, 0)
				start := checkinTimeTest(t, "2027-07-01")
				spec := MarketingActivitySpec{Name: "mv2act_ci_parallel", Published: true, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: start, EndsAt: start + 20*86400, Rewards: []MarketingActivityRewardSpec{daily}, Checkin: &MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 5, MakeupLimit: 1, MakeupDaily: true}}
				marketingActivityCreateTest(t, db, admin.Id, "ci-parallel", spec, start-10)
				marketingActivityConcurrentTest(t, user.Id, func(tx *gorm.DB, _ int) error {
					_, err := CommitMarketingCheckin(tx, user.Id, "", start+3*86400+3600)
					return err
				})
				var count int64
				require.NoError(t, db.Model(&MarketingCheckinDay{}).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.EqualValues(t, 1, count)
				var rejected [2]bool
				marketingActivityConcurrentTest(t, user.Id, func(tx *gorm.DB, worker int) error {
					target := "2027-07-01"
					if worker == 1 {
						target = "2027-07-02"
					}
					_, err := CommitMarketingCheckin(tx, user.Id, target, start+3*86400+4000)
					if err != nil && err.Error() == "No eligible check-in activity for this date" {
						rejected[worker] = true
						return nil
					}
					return err
				})
				assert.NotEqual(t, rejected[0], rejected[1])
				require.NoError(t, db.Model(&MarketingCheckinDay{}).Where("user_id = ? AND makeup = ?", user.Id, true).Count(&count).Error)
				assert.EqualValues(t, 1, count)
			})
		})
	}
}

func checkinTimeTest(t *testing.T, day string) int64 {
	t.Helper()
	date, err := marketing.ParseCheckinDay(day)
	require.NoError(t, err)
	return date.Unix()
}
func commitCheckinTest(t *testing.T, db *gorm.DB, userID int, day string, now int64) *MarketingCheckinResult {
	t.Helper()
	var result *MarketingCheckinResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = CommitMarketingCheckin(tx, userID, day, now)
		return err
	}))
	return result
}
