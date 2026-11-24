package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type recoveryCommand struct {
	TaskID string
	Runner string
	Stale  bool
}

// The old process takes a delivery snapshot before losing its database lease.
// Its snapshot is intentionally still executable: financial uniqueness, rather
// than a scheduling lease alone, must make the takeover safe.
func recoverUsageWorker(cmd recoveryCommand) {
	var snapshot []model.MarketingUsageProjection
	must(model.DB.Where("published_at = 0").Order("id").Limit(2).Find(&snapshot).Error)
	var pid int
	must(model.DB.Raw("SELECT pg_backend_pid()").Scan(&pid).Error)
	emit("READY", map[string]int{"os_pid": os.Getpid(), "backend_pid": pid})
	check(bufio.NewScanner(os.Stdin).Scan(), "missing recovery barrier")
	for _, projection := range snapshot {
		must(model.PublishMarketingUsage(projection))
	}
	must(model.RecoverMarketingUsage(context.Background(), 2))
	err := model.FinishSystemTask(cmd.TaskID, cmd.Runner, model.SystemTaskStatusSucceeded, nil, "")
	if cmd.Stale {
		check(errors.Is(err, model.ErrSystemTaskLockLost), "expired worker rewrote task completion")
	} else {
		must(err)
	}
	emit("RESULT", result{})
}

func recoveryLeaseAndPagination() {
	const pageSize = 2
	var fixtures []fixture
	var projections []model.MarketingUsageProjection
	for i := range pageSize*2 + 1 {
		f := seed(fmt.Sprintf("recovery-%d", i))
		fixtures = append(fixtures, f)
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			funding, err := model.ReserveMarketingFunding(tx, reservation(f, fmt.Sprintf("recovery-%d", i), 40).Reserve)
			if err != nil {
				return err
			}
			_, err = model.SettleMarketingConsumption(tx, settlement(funding, 40).Settle, model.Log{
				UserId: f.user.Id, RequestId: fmt.Sprintf("recovery-%d", i), ModelName: "recovery-fixture", TokenId: f.token.Id,
			})
			return err
		}))
	}
	must(model.DB.Where("published_at = 0").Order("id").Find(&projections).Error)
	check(len(projections) == 5, "expected three delivery pages")
	first, err := model.CreateSystemTask(model.SystemTaskTypeAsyncTaskPoll, nil, nil)
	must(err)
	_, claimed, err := model.ClaimSystemTask(first.ID, first.Type, "old-worker", common.GetTimestamp()+60)
	must(err)
	check(claimed, "old worker must hold database lease")
	old := start(command{Recovery: &recoveryCommand{TaskID: first.TaskID, Runner: "old-worker", Stale: true}})
	// Move only this fixture's expiry into the past; no wall-clock sleep, fake
	// repository, or process-local lock stands in for the production lease APIs.
	must(model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", first.TaskID).Update("locked_until", common.GetTimestamp()-1).Error)
	must(model.ExpireStaleSystemTaskLocks(common.GetTimestamp()))
	second, err := model.CreateSystemTask(first.Type, nil, nil)
	must(err)
	_, claimed, err = model.ClaimSystemTask(second.ID, second.Type, "new-worker", common.GetTimestamp()+60)
	must(err)
	check(claimed, "new worker must acquire expired lease")
	fresh := start(command{Recovery: &recoveryCommand{TaskID: second.TaskID, Runner: "new-worker"}})
	// Both processes enter the same real publication lock before the barrier is
	// released, proving overlap rather than two sequential retries.
	barrier := model.DB.Begin()
	must(barrier.Error)
	must(barrier.Exec("SELECT user_id FROM marketing_v2_accounts WHERE user_id = ? FOR UPDATE", fixtures[0].user.Id).Error)
	old.release()
	fresh.release()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int64
		must(model.DB.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid IN (?, ?)", old.backendPID, fresh.backendPID).Scan(&waiting).Error)
		if waiting == 2 {
			fmt.Println("POSTGRES_LOCK_OBSERVED recovery_old_and_new_workers=2 expired_lease=true")
			break
		}
		check(time.Now().Before(deadline), "recovery workers did not overlap on the real account lock")
		time.Sleep(10 * time.Millisecond)
	}
	must(barrier.Commit().Error)
	check(old.finish(0).Error == "" && fresh.finish(0).Error == "", "recovery process failed")
	for _, f := range fixtures {
		sources(f, 10, 0, 30, 0, 1)
		var u model.User
		must(model.DB.First(&u, f.user.Id).Error)
		check(u.RequestCount == 1 && u.UsedQuota == 40, "recovery repeated request summary")
		rows, total, err := model.GetUserLogs(f.user.Id, model.LogFilter{}, 0, 10)
		must(err)
		check(total == 1 && len(rows) == 1 && rows[0].Quota == 40, "recovery split, duplicated or lost logical usage")
	}
	var pending int64
	must(model.DB.Model(&model.MarketingUsageProjection{}).Where("published_at = 0").Count(&pending).Error)
	check(pending == 0, "recovery stopped at the first page")
	must(model.DB.First(first, first.ID).Error)
	check(first.Status == model.SystemTaskStatusFailed && first.Error == "task lease expired", "stale completion overwrote expired task")
	must(model.DB.First(second, second.ID).Error)
	check(second.Status == model.SystemTaskStatusSucceeded, "new lease owner did not complete")
	fmt.Println("SCENARIO_OK lease_expiry_two_process_overlap_three_pages_one_financial_effect_one_log_per_request")

	// Lost acknowledgements plus a malformed earliest projection must not starve
	// later pages. The corruption is restored, and the original fact is replayed.
	must(model.DB.Model(&model.MarketingUsageProjection{}).Where("id IN ?", []int64{projections[0].ID, projections[1].ID, projections[2].ID, projections[3].ID, projections[4].ID}).Update("published_at", 0).Error)
	must(model.DB.Model(&model.MarketingUsageProjection{}).Where("id = ?", projections[0].ID).Update("payload", "invalid-fixture-json").Error)
	check(model.RecoverMarketingUsage(context.Background(), pageSize) != nil, "corrupt projection must report failure")
	must(model.DB.Model(&model.MarketingUsageProjection{}).Where("published_at = 0").Count(&pending).Error)
	check(pending == 1, "early failure starved later pages")
	must(model.DB.Model(&model.MarketingUsageProjection{}).Where("id = ?", projections[0].ID).Update("payload", projections[0].Payload).Error)
	must(model.RecoverMarketingUsage(context.Background(), pageSize))
	for _, f := range fixtures {
		sources(f, 10, 0, 30, 0, 1)
		_, total, err := model.GetUserLogs(f.user.Id, model.LogFilter{}, 0, 10)
		must(err)
		check(total == 1, "lost publication acknowledgement duplicated logical usage")
	}
	fmt.Println("SCENARIO_OK recovery_bad_first_row_does_not_starve_later_pages_lost_ack_remains_one_log")
}
