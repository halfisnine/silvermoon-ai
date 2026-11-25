// Real OS workers exercise production lottery transactions. HTTP authorization is
// deliberately outside this core fixture and belongs to MV2-012/013.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"gorm.io/gorm"
)

type command struct {
	Action                                        string
	UserID, ActorID                               int
	PoolID, DrawID, AwardID, PrizeID, Amount, Now int64
	Reference, Kind                               string
	CloseRedis, LoseReply, CrashBeforeCommit      bool
}
type result struct {
	Service *service.MarketingLotteryResult
	Pool    *model.MarketingLotteryPool
	Issue   *model.MarketingLotteryIssue
	Draw    *model.MarketingLotteryDraw
	Award   *model.MarketingLotteryAward
	Error   string
}
type process struct {
	cmd        *exec.Cmd
	input      io.WriteCloser
	output     *bufio.Scanner
	backendPID int
	cancel     context.CancelFunc
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func check(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func emit(label string, value any) {
	b, err := common.Marshal(value)
	must(err)
	fmt.Printf("%s %s\n", label, b)
}

func main() {
	worker := len(os.Args) > 1 && os.Args[1] == "worker"
	check(strings.Contains(os.Getenv("SQL_DSN"), "/marketing_v2_lottery_process?"), "isolated lottery database required")
	check(os.Getenv("REDIS_CONN_STRING") == "redis://redis:6379/13", "isolated Redis DB13 required")
	common.CryptoSecret = "isolated-marketing-process-fixture-secret"
	common.IsMasterNode = !worker
	common.BatchUpdateEnabled = true
	must(model.InitDB())
	must(common.InitRedisClient())
	if worker {
		work()
		return
	}
	defer common.RDB.Close()
	lastSlots(false)
	lastSlots(true)
	wholeChanceAward()
	pausedIssuance()
	finalStock()
	duplicateAndRecovery()
	precommitCrash()
	deadlineRaces()
	serviceDelivery()
	fmt.Println("LOTTERY_PROCESS_MATRIX_OK two_real_os_workers shared_postgresql shared_redis_db_13 core_only_http_auth_deferred")
}

func work() {
	var c command
	must(common.UnmarshalJsonStr(os.Args[2], &c))
	if c.CloseRedis {
		must(common.RDB.Close())
		check(common.RDB.Ping(context.Background()).Err() != nil, "Redis must actually fail")
	}
	tx := model.DB.Begin()
	must(tx.Error)
	var backend int
	must(tx.Raw("SELECT pg_backend_pid()").Scan(&backend).Error)
	emit("READY", map[string]int{"os_pid": os.Getpid(), "backend_pid": backend})
	scanner := bufio.NewScanner(os.Stdin)
	check(scanner.Scan(), "missing worker barrier")
	r := result{}
	var err error
	if strings.HasPrefix(c.Action, "service_") {
		// Services own their transactions; this readiness connection is not held.
		must(tx.Rollback().Error)
		switch c.Action {
		case "service_draw":
			r.Service, err = service.DrawMarketingLottery(context.Background(), c.UserID, c.PoolID, c.Reference)
		case "service_get":
			r.Service, err = service.GetMarketingLotteryDraw(context.Background(), c.UserID, c.DrawID)
		case "service_fulfill":
			r.Service, err = service.FulfillMarketingLotteryDraw(context.Background(), c.UserID, c.DrawID)
		default:
			panic("unknown service action")
		}
		if err != nil {
			r.Error = err.Error()
		}
		if c.LoseReply {
			check(err == nil, "lost-reply service failed before response")
			os.Exit(73)
		}
		emit("RESULT", r)
		return
	}

	switch c.Action {
	case "issue":
		r.Issue, err = model.IssueMarketingLotteryChances(tx, c.UserID, c.PoolID, c.Reference, c.Amount, c.Now)
	case "draw":
		r.Draw, err = model.DrawMarketingLotteryChance(tx, c.UserID, c.PoolID, c.Reference, c.Now)
	case "fulfill":
		r.Award, err = model.FulfillMarketingLotteryAward(tx, c.UserID, c.AwardID, c.Now)
	case "mutate":
		r.Pool, err = model.MutateMarketingLotteryPool(tx, c.UserID, c.PoolID, c.Reference, c.Kind, c.PrizeID, c.Amount, c.Now)
	case "clear":
		actorID := c.ActorID
		if actorID == 0 {
			actorID = c.UserID
		}
		err = model.ClearMarketingLotteryChances(tx, actorID, c.UserID, c.Reference, c.Now)
	default:
		panic("unknown action")
	}
	if err != nil {
		must(tx.Rollback().Error)
		emit("RESULT", result{Error: err.Error()})
		return
	}
	if c.CrashBeforeCommit {
		emit("MUTATED", map[string]int{"os_pid": os.Getpid()})
		scanner.Scan()
		panic("crash victim resumed")
	}
	must(tx.Commit().Error)
	if c.LoseReply {
		os.Exit(73)
	}
	emit("RESULT", r)
}

func start(c command) *process {
	payload, err := common.Marshal(c)
	must(err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	child := exec.CommandContext(ctx, os.Args[0], "worker", string(payload))
	input, err := child.StdinPipe()
	must(err)
	output, err := child.StdoutPipe()
	must(err)
	child.Stderr = os.Stderr
	must(child.Start())
	p := &process{cmd: child, input: input, output: bufio.NewScanner(output), cancel: cancel}
	p.output.Buffer(make([]byte, 4096), 1024*1024)
	for p.output.Scan() {
		line := p.output.Text()
		if data, ok := strings.CutPrefix(line, "READY "); ok {
			var ids map[string]int
			must(common.UnmarshalJsonStr(data, &ids))
			p.backendPID = ids["backend_pid"]
			fmt.Printf("WORKER_READY os_pid=%d postgres_pid=%d action=%s\n", ids["os_pid"], p.backendPID, c.Action)
			return p
		}
		fmt.Println(line)
	}
	panic("worker failed before READY")
}
func (p *process) release() { _, err := io.WriteString(p.input, "go\n"); must(err) }
func (p *process) finish(expectedExit int) result {
	defer p.cancel()
	defer p.input.Close()
	var r result
	found := false
	for p.output.Scan() {
		line := p.output.Text()
		if data, ok := strings.CutPrefix(line, "RESULT "); ok {
			must(common.UnmarshalJsonStr(data, &r))
			found = true
		} else {
			fmt.Println(line)
		}
	}
	must(p.output.Err())
	err := p.cmd.Wait()
	if expectedExit == 0 {
		must(err)
		check(found, "missing response")
	} else {
		var e *exec.ExitError
		check(errors.As(err, &e) && e.ExitCode() == expectedExit, "wrong worker exit")
		check(!found, "lost response unexpectedly delivered")
	}
	return r
}
func run(c command) result { p := start(c); p.release(); return p.finish(0) }
func pair(a, b command) (result, result) {
	p := start(a)
	q := start(b)
	p.release()
	q.release()
	return p.finish(0), q.finish(0)
}
func lost(c command)   { c.LoseReply = true; p := start(c); p.release(); p.finish(73) }
func success(r result) { check(r.Error == "", "operation failed: "+r.Error) }

func user(name string) int {
	u := model.User{Username: "lottery_" + name, AffCode: "lottery_" + name, Status: common.UserStatusEnabled, Role: common.RoleAdminUser}
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&u).Error; err != nil {
			return err
		}
		return tx.Create(&model.MarketingAccount{UserID: u.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "lottery-fixture:" + name}).Error
	}))
	return u.Id
}
func gift(amount string, seconds int64) model.MarketingLotteryGiftSpec {
	return model.MarketingLotteryGiftSpec{Money: model.MarketingMoneySnapshot{Amount: amount, QuotaPerUnit: "100", CurrencyPerUnit: "2", Currency: "CNY"}, ValidSeconds: seconds}
}
func pool(owner int, name string, capacity int64, prizes ...model.MarketingLotteryPrizeSpec) *model.MarketingLotteryPool {
	var p *model.MarketingLotteryPool
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = model.CreateMarketingLotteryPool(tx, owner, "create-"+name, model.MarketingLotteryPoolSpec{Name: name, Style: "wheel", StartsAt: 100, EndsAt: 200, Capacity: capacity, Prizes: prizes}, 50)
		return err
	}))
	return p
}
func issue(u int, p int64, key string, count int64) command {
	return command{Action: "issue", UserID: u, PoolID: p, Reference: key, Amount: count, Now: 110}
}
func draw(u int, p int64, reference string, now int64) command {
	return command{Action: "draw", UserID: u, PoolID: p, Reference: reference, Now: now}
}
func mutate(u int, p int64, key, kind string, amount, now int64) command {
	return command{Action: "mutate", UserID: u, PoolID: p, Reference: key, Kind: kind, Amount: amount, Now: now}
}
func chances(issueID int64) []model.MarketingLotteryChance {
	var v []model.MarketingLotteryChance
	must(model.DB.Where("issue_id = ?", issueID).Order("id").Find(&v).Error)
	return v
}
func awards(drawID int64) []model.MarketingLotteryAward {
	var v []model.MarketingLotteryAward
	must(model.DB.Where("draw_id = ?", drawID).Order("id").Find(&v).Error)
	return v
}
func currentPool(id int64) model.MarketingLotteryPool {
	var p model.MarketingLotteryPool
	must(model.DB.First(&p, id).Error)
	return p
}
func currentChance(id int64) model.MarketingLotteryChance {
	var c model.MarketingLotteryChance
	must(model.DB.First(&c, id).Error)
	return c
}
func currentDraw(id int64) model.MarketingLotteryDraw {
	var d model.MarketingLotteryDraw
	must(model.DB.First(&d, id).Error)
	return d
}

func lastSlots(redisDown bool) {
	name := "slots"
	if redisDown {
		name = "slots_redis_down"
	}
	u, v := user(name+"a"), user(name+"b")
	p := pool(u, name, 1, model.MarketingLotteryPrizeSpec{Name: "gift", Weight: 1, Stock: 2, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 60)}})
	a, b := issue(u, p.ID, name+"a", 1), issue(v, p.ID, name+"b", 1)
	a.CloseRedis = redisDown
	b.CloseRedis = redisDown
	x, y := pair(a, b)
	success(x)
	success(y)
	check((x.Issue.State == "issued" && y.Issue.State == "shortage") || (y.Issue.State == "issued" && x.Issue.State == "shortage"), "last slot must have one whole winner")
	check(currentPool(p.ID).Issued == 1, "last slot oversold")
	ma, mb := mutate(u, p.ID, name+"append-a", "append", 1, 120), mutate(v, p.ID, name+"append-b", "append", 1, 120)
	ma.CloseRedis = redisDown
	mb.CloseRedis = redisDown
	x, y = pair(ma, mb)
	check((x.Error == "" && y.Error != "") || (y.Error == "" && x.Error != ""), "concurrent additions counted supply twice")
	check(currentPool(p.ID).Capacity == 2, "invalid capacity after concurrent additions")
	var operations int64
	must(model.DB.Model(&model.MarketingLotteryOperation{}).Where("pool_id = ? AND kind = ?", p.ID, "append").Count(&operations).Error)
	check(operations == 1, "append audit missing or rejected addition recorded successful")
	// Historical shortage remains a recorded non-award after supply is added.
	x, y = pair(a, b)
	success(x)
	success(y)
	check(currentPool(p.ID).Issued == 1, "addition backfilled historical shortage")
	c := issue(u, p.ID, name+"three", 3)
	c.CloseRedis = redisDown
	x = run(c)
	success(x)
	check(x.Issue.State == "shortage" && len(chances(x.Issue.ID)) == 0, "insufficient award split into partial chances")
	check(currentPool(p.ID).Issued == 1, "shortage consumed remaining slot")
	fmt.Printf("SCENARIO_OK %s last_slot_and_append_single_winner_no_historical_backfill_no_partial_issue\n", name)
}

// An exact three-versus-two boundary protects the indivisible reward contract.
func wholeChanceAward() {
	u := user("whole")
	p := pool(u, "whole", 2, model.MarketingLotteryPrizeSpec{Name: "gift", Weight: 1, Stock: 3, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 0)}})
	c := issue(u, p.ID, "whole-three", 3)
	a, b := pair(c, c)
	success(a)
	success(b)
	check(a.Issue.ID == b.Issue.ID && a.Issue.State == "shortage" && len(chances(a.Issue.ID)) == 0, "three-for-two split or duplicated shortage")
	check(currentPool(p.ID).Issued == 0, "three-for-two consumed partial capacity")
	success(run(mutate(u, p.ID, "whole-append", "append", 1, 120)))
	a = run(c)
	success(a)
	check(a.Issue.State == "shortage" && currentPool(p.ID).Issued == 0, "historical three-for-two backfilled after addition")
	a = run(issue(u, p.ID, "whole-new-qualification", 3))
	success(a)
	batches := chances(a.Issue.ID)
	check(a.Issue.State == "issued" && len(batches) == 1 && batches[0].Granted == 3 && batches[0].Remaining == 3, "new qualification did not receive whole batch")
	fmt.Println("SCENARIO_OK exact_three_chances_two_slots_no_split_no_backfill_new_qualification_uses_added_slots")
}

// A pool pause governs drawing, not automatic opportunity awards originating
// from an otherwise eligible activity. Its finite shared capacity still applies.
func pausedIssuance() {
	admin := user("paused_admin")
	u, v := user("paused_issue_a"), user("paused_issue_b")
	p := pool(admin, "paused_issuance", 1, model.MarketingLotteryPrizeSpec{Name: "gift", Weight: 1, Stock: 1, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 60)}})
	success(run(mutate(admin, p.ID, "pause-before-issue", "pause", 0, 105)))
	a, b := pair(issue(u, p.ID, "paused-source-a", 1), issue(v, p.ID, "paused-source-b", 1))
	success(a)
	success(b)
	check((a.Issue.State == "issued" && b.Issue.State == "shortage") || (b.Issue.State == "issued" && a.Issue.State == "shortage"), "paused issuance must accept exactly one final-slot award")
	winner := a.Issue
	if winner.State != "issued" {
		winner = b.Issue
	}
	batch := chances(winner.ID)
	check(len(batch) == 1 && batch[0].Remaining == 1 && currentPool(p.ID).Issued == 1, "paused issuance oversold final capacity")
	rejected := run(draw(winner.UserID, p.ID, "paused-award-draw", 120))
	check(rejected.Error != "" && rejected.Draw == nil && currentChance(batch[0].ID).Remaining == 1, "paused draw consumed newly awarded chance")
	success(run(mutate(admin, p.ID, "resume-after-issue", "resume", 0, 130)))
	granted := run(draw(winner.UserID, p.ID, "paused-award-draw", 140))
	success(granted)
	check(granted.Draw != nil && currentChance(batch[0].ID).Remaining == 0, "resumed pool cannot draw opportunity awarded during pause")
	expired := pool(admin, "paused_issuance_expiry", 1, model.MarketingLotteryPrizeSpec{Name: "gift", Weight: 1, Stock: 1, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 60)}})
	success(run(mutate(admin, expired.ID, "pause-before-expiry-issue", "pause", 0, 105)))
	awarded := run(issue(u, expired.ID, "paused-expiring-source", 1))
	success(awarded)
	check(awarded.Issue.State == "issued", "paused source award was blocked")
	expiryBatch := chances(awarded.Issue.ID)[0]
	check(run(draw(u, expired.ID, "paused-expired-draw", 200)).Error != "", "paused opportunity ignored exact expiry")
	success(run(mutate(admin, expired.ID, "resume-after-natural-expiry", "resume", 0, 201)))
	check(run(draw(u, expired.ID, "paused-expired-draw", 201)).Error != "", "resume resurrected opportunity that expired while paused")
	check(currentChance(expiryBatch.ID).Remaining == 1 && currentChance(expiryBatch.ID).ExpiresAt == 200 && currentPool(expired.ID).Issued == 1, "pause expired chance consumed or issuance slot recycled")
	fmt.Println("SCENARIO_OK paused_pool_accepts_source_awards_last_slot_single_winner_draw_denied_resume_allowed_natural_expiry")
}

func finalStock() {
	u, v := user("stocka"), user("stockb")
	p := pool(u, "stock", 2, model.MarketingLotteryPrizeSpec{Name: "one", Weight: 1, Stock: 1, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 60)}}, model.MarketingLotteryPrizeSpec{Name: "two", Weight: 1, Stock: 1, Gifts: []model.MarketingLotteryGiftSpec{gift("2", 0)}})
	x, y := pair(issue(u, p.ID, "stocka", 1), issue(v, p.ID, "stockb", 1))
	success(x)
	success(y)
	check(len(chances(x.Issue.ID)) == 1 && len(chances(y.Issue.ID)) == 1, "issue batch missing")
	x, y = pair(draw(u, p.ID, "stock-draw-a", 120), draw(v, p.ID, "stock-draw-b", 120))
	success(x)
	success(y)
	check(x.Draw.ID != y.Draw.ID && x.Draw.PrizeID != y.Draw.PrizeID, "final prize unit awarded twice")
	check(len(awards(x.Draw.ID)) == 1 && len(awards(y.Draw.ID)) == 1, "missing complete award responsibility")
	check(run(mutate(u, p.ID, "no-stock-append", "append", 1, 130)).Error != "", "pending committed rewards freed stock")
	refill := mutate(u, p.ID, "refill-original", "restock", 1, 130)
	refill.PrizeID = x.Draw.PrizeID
	success(run(refill))
	success(run(mutate(u, p.ID, "append-after-refill", "append", 1, 130)))
	check(currentPool(p.ID).Capacity == 3 && currentPool(p.ID).Issued == 2, "pending draw obligation double-counted after stock reservation")
	fmt.Println("SCENARIO_OK two_users_final_stock_no_oversell_exhausted_prize_removed_pending_awards_supply_counted_once")
}

func duplicateAndRecovery() {
	u := user("recovery")
	p := pool(u, "recovery", 1, model.MarketingLotteryPrizeSpec{Name: "composite", Weight: 1, Stock: 1, Gifts: []model.MarketingLotteryGiftSpec{gift("2", 60), gift("3", 0)}})
	c := issue(u, p.ID, "recovery-source", 1)
	a, b := pair(c, c)
	success(a)
	success(b)
	check(a.Issue.ID == b.Issue.ID, "duplicate source issued twice")
	cs := chances(a.Issue.ID)
	check(len(cs) == 1, "wrong chance count")
	d := draw(u, p.ID, "recovery-draw", 120)
	lost(d) // Actual worker exits after durable draw, before any reward delivery.
	a, b = pair(d, d)
	success(a)
	success(b)
	check(a.Draw.ID == b.Draw.ID, "retry redrew committed result")
	check(a.Draw.State == "pending", "unpaid composite shown complete")
	items := awards(a.Draw.ID)
	check(len(items) == 2, "composite obligation not fully saved")
	first := command{Action: "fulfill", UserID: u, AwardID: items[0].ID, Now: 130}
	lost(first) // First component committed, second component still pending.
	check(currentDraw(a.Draw.ID).State == "pending", "partial composite shown complete")
	var count int64
	must(model.DB.Model(&model.MarketingGift{}).Where("user_id = ?", u).Count(&count).Error)
	check(count == 1, "partial issuance not durable")
	x, y := pair(first, first)
	success(x)
	success(y)
	check(x.Award.GiftID == y.Award.GiftID, "recovery duplicated component")
	second := command{Action: "fulfill", UserID: u, AwardID: items[1].ID, Now: 140}
	x, y = pair(second, second)
	success(x)
	success(y)
	check(currentDraw(a.Draw.ID).State == "complete", "full recovery not complete")
	var gs []model.MarketingGift
	must(model.DB.Where("user_id = ?", u).Order("id").Find(&gs).Error)
	check(len(gs) == 2 && gs[0].GrantedQuota == 100 && gs[1].GrantedQuota == 150, "gift conversion or duplicate credit failure")
	check(gs[0].GrantedAt == 130 && gs[0].ExpiresAt == 190 && gs[1].GrantedAt == 140 && gs[1].ExpiresAt == 0, "gift lifetime not based on actual credit")
	var owner model.User
	must(model.DB.First(&owner, u).Error)
	check(owner.Quota == 0, "lottery gift polluted refundable principal")
	var entries []model.MarketingLedgerEntry
	must(model.DB.Where("user_id = ? AND kind = ?", u, "gift_credit").Find(&entries).Error)
	check(len(entries) == 2 && entries[0].AmountQuota+entries[1].AmountQuota == 250, "real gift ledger missing or duplicated")
	intruder := user("intruder")
	bad := run(draw(intruder, p.ID, "recovery-draw", 150))
	check(bad.Error != "", "foreign chance accepted")
	bad = run(command{Action: "fulfill", UserID: intruder, AwardID: items[0].ID, Now: 150})
	check(bad.Error != "", "foreign award accepted")
	fmt.Println("SCENARIO_OK duplicate_source_draw_and_awards_lost_response_partial_composite_recovered_original_results_real_gift_ledger_ownership")
}

func precommitCrash() {
	u := user("crash")
	p := pool(u, "crash", 1, model.MarketingLotteryPrizeSpec{Name: "gift", Weight: 1, Stock: 1, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 60)}})
	r := run(issue(u, p.ID, "crash", 1))
	success(r)
	c := draw(u, p.ID, "crash-draw", 120)
	c.CrashBeforeCommit = true
	victim := start(c)
	victim.release()
	found := false
	for victim.output.Scan() {
		line := victim.output.Text()
		if strings.HasPrefix(line, "MUTATED ") {
			found = true
			break
		}
		fmt.Println(line)
	}
	check(found, "draw never reached precommit barrier")
	c.CrashBeforeCommit = false
	retry := start(c)
	retry.release()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var rows []struct{ WaitEventType string }
		must(model.DB.Raw("SELECT wait_event_type FROM pg_stat_activity WHERE pid = ?", retry.backendPID).Scan(&rows).Error)
		if len(rows) == 1 && rows[0].WaitEventType == "Lock" {
			break
		}
		select {
		case <-deadline.C:
			panic("retry did not wait on draw transaction lock")
		case <-ticker.C:
		}
	}
	fmt.Printf("POSTGRES_LOCK_OBSERVED blocked_backend=%d owner_backend=%d\n", retry.backendPID, victim.backendPID)
	must(victim.cmd.Process.Kill())
	victim.finish(-1)
	r = retry.finish(0)
	success(r)
	check(len(awards(r.Draw.ID)) == 1, "killed transaction left duplicate responsibility")
	var n int64
	must(model.DB.Model(&model.MarketingLotteryDraw{}).Where("pool_id = ?", p.ID).Count(&n).Error)
	check(n == 1, "killed draw duplicated persisted result")
	fmt.Println("SCENARIO_OK sigkill_before_draw_commit_real_lock_wait_rollback_and_recovery")
}

func deadlineRaces() {
	u := user("deadline")
	admin := user("deadline_admin")
	p := pool(u, "deadline", 4, model.MarketingLotteryPrizeSpec{Name: "unlimited", Weight: 1, Stock: -1, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 60)}})
	r := run(issue(u, p.ID, "deadlines", 4))
	success(r)
	cs := chances(r.Issue.ID)
	check(len(cs) == 1 && cs[0].Granted == 4 && cs[0].Remaining == 4, "issuance should be one counted batch")
	a, b := pair(draw(u, p.ID, "before-expiry", 199), mutate(admin, p.ID, "extend-live", "extend", 250, 199))
	success(a)
	success(b)
	check(currentChance(cs[0].ID).ExpiresAt == 250, "live remaining batch not extended")
	a, b = pair(draw(u, p.ID, "pause-race", 210), mutate(admin, p.ID, "pause", "pause", 0, 210))
	success(b)
	ch := currentChance(cs[0].ID)
	check((a.Error == "" && ch.Remaining == 2) || (a.Error != "" && ch.Remaining == 3), "pause race consumed denied chance")
	before := ch.Remaining
	check(run(draw(u, p.ID, "while-paused", 220)).Error != "", "paused pool allowed draw")
	check(currentChance(ch.ID).Remaining == before, "paused draw consumed chance")
	success(run(mutate(admin, p.ID, "resume", "resume", 0, 249)))
	a, b = pair(draw(u, p.ID, "at-expiry", 250), mutate(admin, p.ID, "extend-at-expiry", "extend", 300, 250))
	check(a.Error != "", "exact-expiry chance resurrected for draw")
	check(b.Error != "", "expired pool extended")
	check(currentChance(ch.ID).ExpiresAt == 250, "exact expiry extension resurrected old batch")
	check(currentPool(p.ID).Issued == 4, "expiration restored issuance slots")
	v := user("clear")
	q := pool(v, "clear", 1, model.MarketingLotteryPrizeSpec{Name: "gift", Weight: 1, Stock: -1, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 0)}})
	r = run(issue(v, q.ID, "clear-chance", 1))
	success(r)
	cc := chances(r.Issue.ID)[0]
	clear := command{Action: "clear", ActorID: admin, UserID: v, Reference: "refund-clear", Now: 150}
	a, b = pair(clear, mutate(u, q.ID, "extend-clear", "extend", 300, 150))
	success(a)
	success(b)
	check(currentChance(cc.ID).Remaining == 0 && currentChance(cc.ID).Cleared == 1, "extension revived cleared chance")
	check(run(draw(v, q.ID, "after-clear", 160)).Error != "", "cleared chance used")
	check(currentPool(q.ID).Issued == 1, "clear restored issuance slot")
	w := user("clear_draw")
	z := pool(w, "clear_draw", 1, model.MarketingLotteryPrizeSpec{Name: "gift", Weight: 1, Stock: -1, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 0)}})
	r = run(issue(w, z.ID, "clear-draw-chance", 1))
	success(r)
	a, b = pair(command{Action: "clear", ActorID: admin, UserID: w, Reference: "clear-during-draw", Now: 150}, draw(w, z.ID, "draw-during-clear", 150))
	success(a)
	if b.Error == "" {
		check(currentDraw(b.Draw.ID).State == "cleared", "clear left committed pending result recoverable")
		for _, award := range awards(b.Draw.ID) {
			check(award.State == "cleared", "clear left pending composite component live")
			check(run(command{Action: "fulfill", UserID: w, AwardID: award.ID, Now: 160}).Error != "", "cleared component was credited")
		}
	}
	check(currentPool(z.ID).Issued == 1, "clear/draw recycled issuance")
	paused := pool(w, "paused_expiry", 1, model.MarketingLotteryPrizeSpec{Name: "gift", Weight: 1, Stock: -1, Gifts: []model.MarketingLotteryGiftSpec{gift("1", 0)}})
	r = run(issue(w, paused.ID, "paused-expiry", 1))
	success(r)
	success(run(mutate(w, paused.ID, "pause-expiry", "pause", 0, 150)))
	success(run(mutate(w, paused.ID, "resume-expired", "resume", 0, 201)))
	check(run(draw(w, paused.ID, "expired-while-paused", 201)).Error != "", "pause stopped natural expiry")
	check(chances(r.Issue.ID)[0].Remaining == 1 && currentPool(paused.ID).Issued == 1, "expired paused chance consumed or recycled")
	fmt.Println("SCENARIO_OK concurrent_draw_pause_extend_clear_exact_expiry_no_resurrection_no_slot_recycling")
}

// Service orchestration uses its real clock and real per-component transactions.
// The database CHECK below induces a real second-credit failure, never a fake
// ledger or a preselected lottery outcome, and is removed before recovery.
func serviceDelivery() {
	now := time.Now().Unix()
	makeLive := func(u int, name string) *model.MarketingLotteryPool {
		var p *model.MarketingLotteryPool
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			p, err = model.CreateMarketingLotteryPool(tx, u, "live-create-"+name, model.MarketingLotteryPoolSpec{Name: name, Style: "wheel", StartsAt: now - 10, EndsAt: now + 3600, Capacity: 1, Prizes: []model.MarketingLotteryPrizeSpec{{Name: "combo", Weight: 1, Stock: 1, Gifts: []model.MarketingLotteryGiftSpec{gift("2", 60), gift("3", 0)}}}}, now-20)
			return err
		}))
		c := issue(u, p.ID, "live-issue-"+name, 1)
		c.Now = now
		success(run(c))
		return p
	}
	u := user("service")
	p := makeLive(u, "service")
	c := command{Action: "service_draw", UserID: u, PoolID: p.ID, Reference: "service-once"}
	a, b := pair(c, c)
	success(a)
	success(b)
	check(a.Service != nil && b.Service != nil && a.Service.Draw.ID == b.Service.Draw.ID, "service replay redrew result")
	check(a.Service.Draw.State == "complete" && len(a.Service.Awards) == 2, "immediate service composite not fully credited")
	for _, award := range a.Service.Awards {
		check(award.State == "credited" && award.GiftID > 0 && award.CreditedAt >= now, "service returned false delivery evidence")
	}
	var n int64
	must(model.DB.Model(&model.MarketingGift{}).Where("user_id = ?", u).Count(&n).Error)
	check(n == 2, "concurrent service delivery duplicated gifts")
	lost(c)
	replay := run(c)
	success(replay)
	check(replay.Service.Draw.ID == a.Service.Draw.ID, "lost service response rerolled")
	snapshot := run(command{Action: "service_get", UserID: u, DrawID: a.Service.Draw.ID})
	success(snapshot)
	check(snapshot.Service.Draw.State == "complete" && len(snapshot.Service.Awards) == 2, "service getter incomplete")
	outsider := user("service_outsider")
	for _, action := range []string{"service_draw", "service_get", "service_fulfill"} {
		denied := run(command{Action: action, UserID: outsider, PoolID: p.ID, DrawID: a.Service.Draw.ID, Reference: c.Reference})
		check(denied.Error != "" && denied.Service == nil, "service exposed foreign result")
	}
	must(model.DB.Model(&model.User{}).Where("id = ?", u).Update("status", common.UserStatusDisabled).Error)
	for _, action := range []string{"service_draw", "service_get", "service_fulfill"} {
		denied := run(command{Action: action, UserID: u, PoolID: p.ID, DrawID: a.Service.Draw.ID, Reference: c.Reference})
		check(denied.Error != "" && denied.Service == nil, "disabled replay exposed result")
	}
	v := user("service_partial")
	q := makeLive(v, "service_partial")
	must(model.DB.Exec(fmt.Sprintf("ALTER TABLE marketing_v2_gifts ADD CONSTRAINT lottery_delivery_failure CHECK (user_id <> %d OR granted_quota <> 150)", v)).Error)
	partial := command{Action: "service_draw", UserID: v, PoolID: q.ID, Reference: "service-partial"}
	failed := run(partial)
	check(failed.Error != "" && failed.Service != nil && failed.Service.Draw.State == "pending", "partial failure did not return pending result plus error")
	check(len(failed.Service.Awards) == 2 && failed.Service.Awards[0].State == "credited" && failed.Service.Awards[1].State == "pending", "partial service snapshot differs from committed ledger")
	firstID := failed.Service.Awards[0].GiftID
	firstAt := failed.Service.Awards[0].CreditedAt
	must(model.DB.Model(&model.MarketingGift{}).Where("user_id = ?", v).Count(&n).Error)
	check(n == 1, "partial delivery did not preserve exactly first actual credit")
	pending := run(command{Action: "service_get", UserID: v, DrawID: failed.Service.Draw.ID})
	success(pending)
	check(pending.Service.Draw.State == "pending", "getter hid partial failure")
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts DROP CONSTRAINT lottery_delivery_failure").Error)
	a, b = pair(partial, command{Action: "service_fulfill", UserID: v, DrawID: failed.Service.Draw.ID})
	success(a)
	success(b)
	check(a.Service.Draw.ID == failed.Service.Draw.ID && b.Service.Draw.ID == failed.Service.Draw.ID && a.Service.Draw.State == "complete" && b.Service.Draw.State == "complete", "service recovery rerolled or failed to complete")
	check(a.Service.Awards[0].GiftID == firstID && a.Service.Awards[0].CreditedAt == firstAt, "recovery replaced original credited component")
	must(model.DB.Model(&model.MarketingGift{}).Where("user_id = ?", v).Count(&n).Error)
	check(n == 2, "service recovery double credited")
	var owner model.User
	must(model.DB.First(&owner, v).Error)
	check(owner.Quota == 0, "service reward entered principal")
	var entries []model.MarketingLedgerEntry
	must(model.DB.Where("user_id = ? AND kind = ?", v, "gift_credit").Find(&entries).Error)
	check(len(entries) == 2 && entries[0].AmountQuota+entries[1].AmountQuota == 250, "service recovery ledger conservation failed")
	fmt.Println("SCENARIO_OK real_service_draw_get_fulfill_immediate_composite_replay_partial_database_failure_resume_owner_disabled_no_data")
}
