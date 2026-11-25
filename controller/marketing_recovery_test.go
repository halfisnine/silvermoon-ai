package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Uses the existing real SQLite/MySQL/PostgreSQL fixture. Crossing the 100-row
// recovery page protects later users/results from an earlier failing record.
func TestMarketingScheduledRewardRecovery(t *testing.T) {
	setupSecurityEnrollmentTest(t)
	models := append(marketingRegistrationFixtureModels(), &model.Task{}, &model.MarketingUsageProjection{}, &model.SystemTask{}, &model.SystemTaskLock{})
	require.NoError(t, model.DB.AutoMigrate(models...))
	require.NoError(t, model.DB.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
	previous := constant.UpdateTask
	constant.UpdateTask = false
	t.Cleanup(func() { constant.UpdateTask = previous })
	assert.False(t, (asyncTaskPollHandler{}).Enabled())
	admin := model.User{Username: "recovery-admin", AffCode: "recovery-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&admin).Error)
	now := time.Now().Unix()
	money := model.MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}
	var pool *model.MarketingLotteryPool
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		pool, err = model.CreateMarketingLotteryPool(tx, admin.Id, "recovery-pool", model.MarketingLotteryPoolSpec{Name: "Recovery pool", Style: "wheel", StartsAt: now - 10, EndsAt: now + 3600, Capacity: 101, Prizes: []model.MarketingLotteryPrizeSpec{{Name: "Two gifts", Weight: 1, Stock: 101, Gifts: []model.MarketingLotteryGiftSpec{{Money: money, ValidSeconds: 3600}, {Money: money}}}}}, now-10)
		if err != nil {
			return err
		}
		activity, err := model.CreateMarketingActivity(tx, admin.Id, "recovery-activity", model.MarketingActivitySpec{Name: "Recovery registration", Kind: "registration", Condition: "registration", Frequency: "once", StartsAt: now - 10, Rewards: []model.MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", Money: money}, {Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "direct"}}}, now-10)
		if err != nil {
			return err
		}
		_, err = model.ActivateMarketingActivity(tx, admin.Id, activity.ID, "recovery-activate", now-10)
		return err
	}))
	users := make([]model.User, 101)
	for i := range users {
		users[i] = model.User{Username: fmt.Sprintf("recovery-user-%d", i), AffCode: fmt.Sprintf("rec-%d", i), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, CreatedAt: now}
		require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&users[i]).Error; err != nil {
				return err
			}
			if err := tx.Create(&model.MarketingAccount{UserID: users[i].Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error; err != nil {
				return err
			}
			_, err := model.CaptureMarketingRegistrationFact(tx, users[i].Id, fmt.Sprintf("recovery-registration-%d", i), now)
			return err
		}))
	}
	var first model.MarketingActivityFact
	require.NoError(t, model.DB.Where("user_id = ?", users[0].Id).First(&first).Error)
	originalRules := first.Rules
	require.NoError(t, model.DB.Model(&first).Update("rules", "invalid-fixture-json").Error)
	assert.True(t, (asyncTaskPollHandler{}).Enabled(), "committed rewards enable recovery even with provider polling off")
	task := executeMarketingRecoveryTask(t)
	assert.Equal(t, model.SystemTaskStatusFailed, task.Status)
	var gifts, pending int64
	require.NoError(t, model.DB.Model(&model.MarketingGift{}).Count(&gifts).Error)
	assert.EqualValues(t, 100, gifts, "broken earliest source cannot starve the second page")
	require.NoError(t, model.DB.Model(&model.MarketingActivityFact{}).Where("delivery_state = ?", "pending").Count(&pending).Error)
	assert.EqualValues(t, 1, pending)
	require.NoError(t, model.DB.Model(&first).Update("rules", originalRules).Error)
	assert.Equal(t, model.SystemTaskStatusSucceeded, executeMarketingRecoveryTask(t).Status)
	assert.False(t, (asyncTaskPollHandler{}).Enabled())
	for i, user := range users {
		require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
			_, err := model.DrawMarketingLotteryChance(tx, user.Id, pool.ID, fmt.Sprintf("recovery-draw-%d", i), now)
			return err
		}))
	}
	var award model.MarketingLotteryAward
	require.NoError(t, model.DB.Order("id").First(&award).Error)
	originalSpec := award.GiftSpec
	require.NoError(t, model.DB.Model(&award).Update("gift_spec", "invalid-fixture-json").Error)
	assert.True(t, (asyncTaskPollHandler{}).Enabled())
	assert.Equal(t, model.SystemTaskStatusFailed, executeMarketingRecoveryTask(t).Status)
	require.NoError(t, model.DB.Model(&model.MarketingLotteryDraw{}).Where("state = ?", "pending").Count(&pending).Error)
	assert.EqualValues(t, 1, pending, "failed first draw cannot starve later pages")
	require.NoError(t, model.DB.Model(&award).Update("gift_spec", originalSpec).Error)
	assert.Equal(t, model.SystemTaskStatusSucceeded, executeMarketingRecoveryTask(t).Status)
	require.NoError(t, service.RecoverMarketingRewards(context.Background()))
	assert.False(t, (asyncTaskPollHandler{}).Enabled())
	require.NoError(t, model.DB.Model(&model.MarketingGift{}).Count(&gifts).Error)
	assert.EqualValues(t, 303, gifts, "registration plus two original lottery components, once each")
	var remaining, ledger int64
	require.NoError(t, model.DB.Model(&model.MarketingLotteryChance{}).Select("COALESCE(SUM(remaining),0)").Scan(&remaining).Error)
	assert.Zero(t, remaining)
	require.NoError(t, model.DB.Model(&model.MarketingGift{}).Select("COALESCE(SUM(remaining_quota),0)").Scan(&remaining).Error)
	assert.EqualValues(t, 606, remaining)
	require.NoError(t, model.DB.Model(&model.MarketingLedgerEntry{}).Where("kind = ?", "gift_credit").Select("COALESCE(SUM(amount_quota),0)").Scan(&ledger).Error)
	assert.Equal(t, remaining, ledger)
	var prize model.MarketingLotteryPrize
	require.NoError(t, model.DB.Where("pool_id = ?", pool.ID).First(&prize).Error)
	assert.Zero(t, prize.Stock)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, service.RecoverMarketingRewards(ctx), context.Canceled)
}

func executeMarketingRecoveryTask(t *testing.T) *model.SystemTask {
	t.Helper()
	task, err := model.CreateSystemTask(model.SystemTaskTypeAsyncTaskPoll, nil, nil)
	require.NoError(t, err)
	task, claimed, err := model.ClaimSystemTask(task.ID, task.Type, "recovery-test", time.Now().Unix()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	(asyncTaskPollHandler{}).Run(context.Background(), task, "recovery-test")
	require.NoError(t, model.DB.First(task, task.ID).Error)
	return task
}
