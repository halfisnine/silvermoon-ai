// Real process activity-engine validation. Payment and gift facts are persisted
// through production model/service operations; HTTP integrations are later work.
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
	Action, Reference, Day                   string
	UserID, InviterID                        int
	ItemID, Now                              int64
	Payment                                  model.MarketingPaymentInput
	Claims                                   []model.MarketingActivityCheckinClaim
	CloseRedis, LoseReply, CrashBeforeCommit bool
}
type result struct {
	Service    *service.MarketingActivityProcessingResult
	Fact       *model.MarketingActivityFact
	Reward     *model.MarketingActivityReward
	Payment    *model.MarketingPayment
	Invitation *model.MarketingInvitation
	Error      string
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

func start(c command) *process {
	payload, err := common.Marshal(c)
	must(err)
	timeout := 30 * time.Second
	if c.Action == "service" {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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

func main() {
	worker := len(os.Args) > 1 && os.Args[1] == "worker"
	check(strings.Contains(os.Getenv("SQL_DSN"), "/marketing_v2_activity_process?"), "isolated activity database required")
	check(os.Getenv("REDIS_CONN_STRING") == "redis://redis:6379/12", "isolated Redis DB12 required")
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
	paymentOrder()
	exclusionAndSupply()
	probabilityOnce()
	pauseAndCopy()
	commitmentRecovery()
	invitationAndRegistration()
	checkinRewards()
	draftAndPublication()
	serviceOrchestration()
	serviceFairness()
	fmt.Println("ACTIVITY_PROCESS_MATRIX_OK real_os_workers shared_postgresql shared_redis_db_12 core_only_http_integration_deferred")
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
	if c.Action == "service" {
		must(tx.Rollback().Error)
		r.Service, err = service.ProcessMarketingActivityFacts(context.Background(), c.UserID)
		if err != nil {
			r.Error = err.Error()
		}
		if c.LoseReply {
			check(err == nil, "service lost response failed before commit")
			os.Exit(73)
		}
		emit("RESULT", r)
		return
	}
	switch c.Action {
	case "payment":
		r.Payment, err = model.CreditMarketingPayment(tx, c.Payment)
	case "process":
		r.Fact, err = model.ProcessNextMarketingActivityFact(tx, c.UserID, c.Now)
	case "fulfill":
		r.Reward, err = model.FulfillMarketingActivityReward(tx, c.UserID, c.ItemID, c.Now)
	case "registration":
		r.Fact, err = model.CaptureMarketingRegistrationFact(tx, c.UserID, c.Reference, c.Now)
	case "invitation":
		r.Invitation, err = model.RecordMarketingInvitation(tx, c.UserID, c.InviterID, c.Reference, c.Now)
	case "checkin":
		r.Fact, err = model.CaptureMarketingCheckinFact(tx, c.UserID, c.Reference, c.Day, c.Now, c.Claims)
	default:
		panic("unknown activity worker action")
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

func user(name string, at int64, inviter int) int {
	u := model.User{Username: "activity_" + name, AffCode: "activity_" + name, Status: common.UserStatusEnabled, Role: common.RoleAdminUser, CreatedAt: at, InviterId: inviter}
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&u).Error; err != nil {
			return err
		}
		return tx.Create(&model.MarketingAccount{UserID: u.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "activity-fixture:" + name}).Error
	}))
	return u.Id
}
func money(amount string) model.MarketingMoneySnapshot {
	return model.MarketingMoneySnapshot{Amount: amount, QuotaPerUnit: "100", CurrencyPerUnit: "2", Currency: "CNY"}
}
func fixed(amount string) model.MarketingActivityRewardSpec {
	return model.MarketingActivityRewardSpec{Kind: "gift", Calculation: "fixed", Money: money(amount), ValidSeconds: 600}
}
func chance(poolID, count int64) model.MarketingActivityRewardSpec {
	return model.MarketingActivityRewardSpec{Kind: "chance", PoolID: poolID, Count: count, ChanceMode: "direct"}
}
func activity(admin int, spec model.MarketingActivitySpec) *model.MarketingActivity {
	var a *model.MarketingActivity
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = model.CreateMarketingActivity(tx, admin, "create-"+spec.Name, spec, spec.StartsAt-10)
		if err != nil {
			return err
		}
		a, err = model.ActivateMarketingActivity(tx, admin, a.ID, "activate-"+spec.Name, spec.StartsAt-10)
		return err
	}))
	return a
}
func recharge(name, condition, frequency string, start int64, rewards ...model.MarketingActivityRewardSpec) model.MarketingActivitySpec {
	return model.MarketingActivitySpec{Name: name, Kind: "recharge", Condition: condition, Frequency: frequency, StartsAt: start, EndsAt: start + 900, ThresholdCents: 10000, Rewards: rewards}
}
func pool(admin int, name string, start, capacity, stock int64) *model.MarketingLotteryPool {
	var p *model.MarketingLotteryPool
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = model.CreateMarketingLotteryPool(tx, admin, "pool-"+name, model.MarketingLotteryPoolSpec{Name: name, Style: "wheel", StartsAt: start, EndsAt: start + 900, Capacity: capacity, Prizes: []model.MarketingLotteryPrizeSpec{{Name: "gift", Weight: 1, Stock: stock, Gifts: []model.MarketingLotteryGiftSpec{{Money: money("1"), ValidSeconds: 60}}}}}, start-10)
		return err
	}))
	return p
}
func payment(u int, key string, cents int64, amount string, at int64) command {
	return command{Action: "payment", Payment: model.MarketingPaymentInput{UserID: u, Kind: "online", Reference: key, PaidCents: cents, Money: money(amount), PostedAt: at}}
}
func decide(u int, at int64) command { return command{Action: "process", UserID: u, Now: at} }
func drain(u int, at int64) {
	for range 20 {
		r := run(decide(u, at))
		success(r)
		if r.Fact == nil {
			return
		}
	}
	panic("fact processing did not finish bounded fixture")
}
func rewardRows(u int) []model.MarketingActivityReward {
	var rows []model.MarketingActivityReward
	must(model.DB.Where("user_id = ?", u).Order("id").Find(&rows).Error)
	return rows
}
func gifts(u int) []model.MarketingGift {
	var rows []model.MarketingGift
	must(model.DB.Where("user_id = ?", u).Order("id").Find(&rows).Error)
	return rows
}
func fulfill(u int, at int64) {
	for _, r := range rewardRows(u) {
		if r.State == "pending" {
			success(run(command{Action: "fulfill", UserID: u, ItemID: r.ID, Now: at}))
		}
	}
}
func principal(u int, want int64) {
	var v model.User
	must(model.DB.First(&v, u).Error)
	check(int64(v.Quota) == want, "marketing reward changed refundable principal")
}
func factRows(u int) []model.MarketingActivityFact {
	var rows []model.MarketingActivityFact
	must(model.DB.Where("user_id = ?", u).Order("id").Find(&rows).Error)
	return rows
}
func assertGiftLedger(u int, count int, total int64) {
	gs := gifts(u)
	check(len(gs) == count, "gift count missing or duplicate")
	var sum int64
	for _, g := range gs {
		sum += g.GrantedQuota
		check(g.RemainingQuota == g.GrantedQuota, "new gift balance mismatch")
	}
	check(sum == total, "gift total mismatch")
	var entries []model.MarketingLedgerEntry
	must(model.DB.Where("user_id = ? AND kind = ?", u, "gift_credit").Find(&entries).Error)
	sum = 0
	for _, e := range entries {
		sum += e.AmountQuota
		check(e.GiftID > 0, "gift ledger lost original source")
	}
	check(len(entries) == count && sum == total, "gift ledger missing or duplicate")
}
func paymentOrder() {
	admin, u := user("order_admin", 900, 0), user("order", 900, 0)
	first := activity(admin, recharge("order_first", "first_successful_payment", "once", 1000, fixed("1")))
	once := activity(admin, recharge("order_single_once", "single_payment", "once", 1000, fixed("2")))
	every := activity(admin, recharge("order_single_every", "single_payment", "every", 1000, fixed("3")))
	cumOnce := activity(admin, recharge("order_cum_once", "cumulative_payments", "once", 1000, fixed("4")))
	cumulative := activity(admin, recharge("order_cum_every", "cumulative_payments", "every", 1000, fixed("5")))
	firstPay := payment(u, "order-first", 3000, "30", 1100)
	lost(firstPay)
	p1 := run(firstPay)
	success(p1)
	secondPay := payment(u, "order-second", 10000, "150", 1200)
	secondPay.Payment.Kind = "paid_code"
	p2, p2retry := pair(secondPay, secondPay)
	success(p2)
	success(p2retry)
	check(p2.Payment.ID == p2retry.Payment.ID, "duplicate posting created duplicate payment")
	p3 := run(payment(u, "order-third", 25000, "300", 1300))
	success(p3)
	check(p1.Payment.Sequence == 1 && p1.Payment.FirstStatus == "first" && p2.Payment.Sequence == 2 && p2.Payment.FirstStatus == "later" && p3.Payment.Sequence == 3, "payment identity or shared online/code sequence changed")
	a, b := pair(decide(u, 2000), decide(u, 2000))
	success(a)
	success(b)
	check(a.Fact != nil && b.Fact != nil && a.Fact.ID != b.Fact.ID, "concurrent workers processed same fact twice")
	drain(u, 2000)
	fulfill(u, 2001)
	counts := map[int64]int{}
	for _, r := range rewardRows(u) {
		check(r.State == "credited" && r.GiftID > 0, "decided reward not credited")
		counts[r.ActivityID]++
	}
	check(counts[first.ID] == 0 && counts[once.ID] == 1 && counts[every.ID] == 2 && counts[cumOnce.ID] == 1 && counts[cumulative.ID] == 3, "first/single/cumulative ordering changed eligibility")
	var progress model.MarketingActivityProgress
	must(model.DB.Where("activity_id = ? AND user_id = ?", cumulative.ID, u).First(&progress).Error)
	check(progress.ProcessedAwards == 3 && progress.RemainderCents == 8000, "cumulative thresholds or remainder wrong")
	facts := factRows(u)
	check(len(facts) == 3, "payment retry duplicated facts")
	for i, f := range facts {
		check(f.PaymentSequence == int64(i+1) && f.State == "decided", "payment facts did not preserve posting order")
	}
	a, b = pair(decide(u, 2100), decide(u, 2100))
	success(a)
	success(b)
	check(a.Fact == nil && b.Fact == nil, "decided facts replayed")
	assertGiftLedger(u, 7, 1350)
	principal(u, 24000)
	fmt.Println("SCENARIO_OK posting_sequence_duplicate_online_code_first_and_five_recharge_modes_cumulative_no_double_gift")
}
func exclusionAndSupply() {
	admin, u := user("exclude_admin", 1900, 0), user("exclude", 1900, 0)
	p := pool(admin, "exclude", 2000, 2, 6)
	highSpec := recharge("exclude_high", "single_payment", "every", 2000, fixed("2"), chance(p.ID, 3))
	highSpec.ExclusiveGroup = "exclusive"
	highSpec.Priority = 10
	high := activity(admin, highSpec)
	lowSpec := recharge("exclude_low", "single_payment", "every", 2000, fixed("99"))
	lowSpec.ExclusiveGroup = "exclusive"
	lowSpec.Priority = 1
	low := activity(admin, lowSpec)
	stack := activity(admin, recharge("exclude_stack", "single_payment", "every", 2000, fixed("1")))
	// Same-priority activities use a stable activity ID, not worker scheduling.
	tieSpec := recharge("exclude_tie_first", "single_payment", "every", 2000, fixed("3"))
	tieSpec.ExclusiveGroup = "tie"
	tieSpec.Priority = 5
	tie := activity(admin, tieSpec)
	tieSpec.Name = "exclude_tie_later"
	tieSpec.Rewards = []model.MarketingActivityRewardSpec{fixed("88")}
	tieLost := activity(admin, tieSpec)
	success(run(payment(u, "exclude-pay-first", 10000, "100", 2100)))
	a, b := pair(decide(u, 2200), decide(u, 2200))
	success(a)
	success(b)
	fulfill(u, 2201)
	var shortage model.MarketingActivityReward
	counts := map[int64]int{}
	for _, r := range rewardRows(u) {
		counts[r.ActivityID]++
		if r.Kind == "chance" {
			shortage = r
		}
	}
	check(shortage.State == "shortage" && counts[high.ID] == 2 && counts[low.ID] == 0 && counts[stack.ID] == 1 && counts[tie.ID] == 1 && counts[tieLost.ID] == 0, "supply failure changed exclusive winner or fixed gift")
	check(shortage.IssueID > 0, "shortage lost real lottery source")
	var issue model.MarketingLotteryIssue
	must(model.DB.First(&issue, shortage.IssueID).Error)
	check(issue.State == "shortage" && issue.Count == 3, "shortage not entire chance item")
	delayed := user("exclude_delayed", 1900, 0)
	success(run(payment(delayed, "captured-before-append", 10000, "100", 2210)))
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.MutateMarketingLotteryPool(tx, admin, p.ID, "exclude-append", "append", 0, 4, 2250)
		return err
	}))
	drain(u, 2260)
	fulfill(u, 2261)
	drain(delayed, 2260)
	fulfill(delayed, 2261)
	for _, r := range rewardRows(delayed) {
		if r.Kind == "chance" {
			check(r.State == "shortage", "captured old fact consumed subsequently added capacity")
		}
	}
	assertGiftLedger(delayed, 3, 300)
	principal(delayed, 5000)
	var old model.MarketingActivityReward
	must(model.DB.First(&old, shortage.ID).Error)
	check(old.State == "shortage" && old.IssueID == shortage.IssueID, "historical shortage backfilled")
	success(run(payment(u, "exclude-pay-new", 10000, "100", 2300)))
	drain(u, 2310)
	fulfill(u, 2311)
	var issued int
	for _, r := range rewardRows(u) {
		if r.Kind == "chance" && r.State == "issued" {
			issued++
			var batch model.MarketingLotteryChance
			must(model.DB.Where("issue_id = ?", r.IssueID).First(&batch).Error)
			check(batch.Granted == 3 && batch.Remaining == 3, "new chance item split")
		}
	}
	check(issued == 1, "new qualification did not receive appended supply")
	assertGiftLedger(u, 6, 600)
	principal(u, 10000)
	fmt.Println("SCENARIO_OK persistent_priority_tie_and_stack_shortage_not_fallback_fixed_gift_independent_no_history_backfill")
}
func probabilityOnce() {
	admin, u := user("prob_admin", 2900, 0), user("prob", 2900, 0)
	guaranteed := pool(admin, "prob_guaranteed", 3000, 100, -1)
	var emptyAllowed *model.MarketingLotteryPool
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		emptyAllowed, err = model.CreateMarketingLotteryPool(tx, admin, "prob-empty", model.MarketingLotteryPoolSpec{Name: "prob_empty", Style: "blind_box", StartsAt: 3000, EndsAt: 3900, Capacity: 100, Prizes: []model.MarketingLotteryPrizeSpec{{Name: "gift", Weight: 1, Stock: -1, Gifts: []model.MarketingLotteryGiftSpec{{Money: money("1")}}}, {Name: "empty", Weight: 1, Stock: -1}}}, 2990)
		return err
	}))
	ids := map[int64]int64{}
	for _, tc := range []struct {
		name      string
		ppm, pool int64
	}{{"zero", 0, guaranteed.ID}, {"full", 1000000, emptyAllowed.ID}, {"middle", 500000, guaranteed.ID}} {
		r := chance(tc.pool, 1)
		r.ChanceMode = "probability"
		r.ProbabilityPPM = tc.ppm
		a := activity(admin, recharge("prob_"+tc.name, "cumulative_payments", "every", 3000, fixed("1"), r))
		ids[a.ID] = tc.ppm
	}
	c := payment(u, "prob-payment", 25000, "25", 3100)
	success(run(c))
	lost(decide(u, 3200))
	drain(u, 3201)
	fulfill(u, 3202)
	states := map[int64]string{}
	for _, r := range rewardRows(u) {
		states[r.ID] = r.State
		if r.Kind != "chance" {
			continue
		}
		switch ids[r.ActivityID] {
		case 0:
			check(r.State == "probability_miss" && r.IssueID == 0, "zero probability became technical failure or chance")
		case 1000000:
			check(r.State == "issued" && r.IssueID > 0, "full probability failed despite supply")
		default:
			check(r.State == "issued" || r.State == "probability_miss", "probability result invalid")
		}
	}
	a, b := pair(c, c)
	success(a)
	success(b)
	a, b = pair(decide(u, 3250), decide(u, 3250))
	success(a)
	success(b)
	for _, r := range rewardRows(u) {
		check(states[r.ID] == r.State, "probability result changed on retry")
	}
	check(len(rewardRows(u)) == 12, "probability replay created new items")
	assertGiftLedger(u, 6, 300)
	principal(u, 1250)
	fmt.Println("SCENARIO_OK persisted_zero_full_and_random_probability_once_fixed_gifts_independent_both_pool_result_modes")
}
func pauseAndCopy() {
	admin, u := user("pause_admin", 3900, 0), user("pause", 3900, 0)
	cumulative := activity(admin, recharge("pause_cumulative", "cumulative_payments", "every", 4000, fixed("2")))
	directSpec := recharge("pause_direct", "single_payment", "every", 4000, fixed("1"))
	directSpec.ThresholdCents = 1
	direct := activity(admin, directSpec)
	success(run(payment(u, "before-pause", 6000, "60", 4100)))
	for _, id := range []int64{cumulative.ID, direct.ID} {
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			_, err := model.SetMarketingActivityPaused(tx, admin, id, fmt.Sprintf("pause-%d", id), true, 4200)
			return err
		}))
	}
	success(run(payment(u, "during-pause", 5000, "50", 4250)))
	for _, id := range []int64{cumulative.ID, direct.ID} {
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			_, err := model.SetMarketingActivityPaused(tx, admin, id, fmt.Sprintf("resume-%d", id), false, 4300)
			return err
		}))
	}
	success(run(payment(u, "after-pause", 4000, "40", 4350)))
	a, b := pair(decide(u, 4950), decide(u, 4950))
	success(a)
	success(b)
	drain(u, 4950)
	fulfill(u, 4951)
	assertGiftLedger(u, 3, 200)
	principal(u, 7500)
	facts := factRows(u)
	for _, r := range rewardRows(u) {
		check(r.FactID != facts[1].ID, "paused fact backfilled after resume")
	}
	var progress model.MarketingActivityProgress
	must(model.DB.Where("activity_id = ? AND user_id = ?", cumulative.ID, u).First(&progress).Error)
	check(progress.ProcessedAwards == 1 && progress.RemainderCents == 0, "paused payment entered cumulative amount")
	// Copying is creation with a new identity and new period, never progress import.
	v := user("copy", 4900, 0)
	oldSpec := recharge("copy_old", "cumulative_payments", "every", 5000, fixed("2"))
	old := activity(admin, oldSpec)
	success(run(payment(v, "copy-before", 6000, "60", 5100)))
	drain(v, 5200)
	cloneSpec := oldSpec
	cloneSpec.Name = "copy_new"
	cloneSpec.StartsAt = 5500
	cloneSpec.EndsAt = 6400
	clone := activity(admin, cloneSpec)
	success(run(payment(v, "copy-shared-period", 4000, "40", 5600)))
	drain(v, 5650)
	fulfill(v, 5651)
	counts := map[int64]int{}
	for _, r := range rewardRows(v) {
		counts[r.ActivityID]++
	}
	check(counts[old.ID] == 1 && counts[clone.ID] == 0, "copy imported old remainder or events")
	success(run(payment(v, "copy-new-period", 6000, "60", 6000)))
	drain(v, 6100)
	fulfill(v, 6101)
	counts = map[int64]int{}
	for _, r := range rewardRows(v) {
		counts[r.ActivityID]++
	}
	check(counts[old.ID] == 1 && counts[clone.ID] == 1, "new-period facts did not earn independent new activity reward")
	assertGiftLedger(v, 2, 200)
	principal(v, 8000)
	fmt.Println("SCENARIO_OK paused_history_snapshot_late_delivery_no_pause_backfill_copy_new_identity_no_old_remainder")
}
func commitmentRecovery() {
	admin, u, v := user("commit_admin", 6900, 0), user("commit", 6900, 0), user("commit_other", 6900, 0)
	p := pool(admin, "commit", 7000, 3, 3)
	spec := recharge("commit_combo", "single_payment", "every", 7000, fixed("2"), fixed("3"), chance(p.ID, 3))
	activity(admin, spec)
	success(run(payment(u, "commit-payment", 10000, "100", 7100)))
	lost(decide(u, 7200))
	var reserved model.MarketingLotteryPool
	must(model.DB.First(&reserved, p.ID).Error)
	check(reserved.Issued == 3, "committed chance obligation did not reserve real supply")
	success(run(payment(v, "commit-competitor", 10000, "100", 7210)))
	drain(v, 7220)
	for _, r := range rewardRows(v) {
		if r.Kind == "chance" {
			check(r.State == "shortage", "competitor stole committed chance supply")
		}
	}
	var items []model.MarketingActivityReward
	for _, r := range rewardRows(u) {
		if r.Kind == "gift" {
			items = append(items, r)
		} else {
			check(r.State == "issued" && r.IssueID > 0, "committed chance is not real issuance")
		}
	}
	check(len(items) == 2, "full combination responsibility not saved")
	first := command{Action: "fulfill", UserID: u, ItemID: items[0].ID, Now: 7230}
	lost(first)
	must(model.DB.Exec(fmt.Sprintf("ALTER TABLE marketing_v2_gifts ADD CONSTRAINT activity_delivery_failure CHECK (user_id <> %d OR granted_quota <> 150)", u)).Error)
	failed := run(command{Action: "fulfill", UserID: u, ItemID: items[1].ID, Now: 7240})
	check(failed.Error != "", "database-induced second delivery failure missing")
	check(len(gifts(u)) == 1, "failed component rolled back earlier committed gift")
	var fact model.MarketingActivityFact
	must(model.DB.First(&fact, items[0].FactID).Error)
	check(fact.DeliveryState == "pending", "partial combo shown complete")
	a, b := pair(first, first)
	success(a)
	success(b)
	check(a.Reward.GiftID == b.Reward.GiftID, "first component replay duplicated gift")
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts DROP CONSTRAINT activity_delivery_failure").Error)
	second := command{Action: "fulfill", UserID: u, ItemID: items[1].ID, Now: 7250}
	a, b = pair(second, second)
	success(a)
	success(b)
	check(a.Reward.GiftID == b.Reward.GiftID, "second component recovery duplicated gift")
	must(model.DB.First(&fact, items[0].FactID).Error)
	check(fact.DeliveryState == "complete", "recovered combination not complete")
	assertGiftLedger(u, 2, 250)
	principal(u, 5000)
	fmt.Println("SCENARIO_OK committed_combo_and_chance_supply_survive_process_exit_real_partial_database_failure_recovers_once")
}
func invitationAndRegistration() {
	admin, inviter := user("invite_admin", 9900, 0), user("inviter", 9900, 0)
	a, b := user("friend_a", 10100, inviter), user("friend_b", 10110, inviter)
	oldFriend := user("old_friend", 9900, inviter)
	for _, row := range []struct {
		u   int
		at  int64
		ref string
	}{{a, 10100, "relation-a"}, {b, 10110, "relation-b"}, {oldFriend, 9900, "relation-old"}} {
		success(run(command{Action: "invitation", UserID: row.u, InviterID: inviter, Reference: row.ref, Now: row.at}))
	}
	activity(admin, model.MarketingActivitySpec{Name: "register_gift", Kind: "registration", Condition: "registration", Frequency: "once", StartsAt: 10000, EndsAt: 10900, Rewards: []model.MarketingActivityRewardSpec{fixed("1")}})
	activity(admin, model.MarketingActivitySpec{Name: "invite_register", Kind: "invitation", Condition: "friend_registration", Frequency: "once", StartsAt: 10000, EndsAt: 10900, Rewards: []model.MarketingActivityRewardSpec{fixed("2")}})
	for _, tc := range []struct{ name, frequency, amount string }{{"invite_cum_once", "once", "3"}, {"invite_cum_every", "every", "4"}} {
		activity(admin, model.MarketingActivitySpec{Name: tc.name, Kind: "invitation", Condition: "friend_cumulative", Frequency: tc.frequency, StartsAt: 10000, EndsAt: 10900, ThresholdCents: 10000, Rewards: []model.MarketingActivityRewardSpec{fixed(tc.amount)}})
	}
	ratio := model.MarketingActivityRewardSpec{Kind: "gift", Calculation: "cash_ratio", Ratio: "0.1", Money: money("1")}
	activity(admin, model.MarketingActivitySpec{Name: "invite_cash_ratio", Kind: "invitation", Condition: "friend_ratio", Frequency: "every", StartsAt: 10000, EndsAt: 10900, Rewards: []model.MarketingActivityRewardSpec{ratio}})
	paid := model.MarketingActivityRewardSpec{Kind: "gift", Calculation: "paid_ratio", Ratio: "0.1"}
	paidSpec := recharge("friend_paid_ratio", "single_payment", "every", 10000, paid)
	paidSpec.ThresholdCents = 1
	activity(admin, paidSpec)
	ca := command{Action: "registration", UserID: a, Reference: "friend-registration-a", Now: 10100}
	x, y := pair(ca, ca)
	success(x)
	success(y)
	check(x.Fact.ID == y.Fact.ID, "registration fact duplicated")
	success(run(command{Action: "registration", UserID: b, Reference: "friend-registration-b", Now: 10110}))
	success(run(command{Action: "registration", UserID: oldFriend, Reference: "friend-registration-old", Now: 9900}))
	success(run(payment(a, "friend-a-60", 6000, "200", 10200)))
	success(run(payment(b, "friend-b-40", 4000, "40", 10210)))
	x, y = pair(decide(a, 10300), decide(b, 10300))
	success(x)
	success(y)
	drain(a, 10300)
	drain(b, 10300)
	for _, r := range rewardRows(inviter) {
		var act model.MarketingActivity
		must(model.DB.First(&act, r.ActivityID).Error)
		check(act.Name != "invite_cum_once" && act.Name != "invite_cum_every", "friends 60 and 40 incorrectly pooled")
	}
	success(run(payment(a, "friend-a-extra-40", 4000, "80", 10400)))
	drain(a, 10410)
	success(run(payment(oldFriend, "old-friend-100", 10000, "100", 10420)))
	drain(oldFriend, 10430)
	fulfill(inviter, 10500)
	fulfill(a, 10500)
	fulfill(b, 10500)
	fulfill(oldFriend, 10500)
	assertGiftLedger(inviter, 7, 1250)
	assertGiftLedger(a, 3, 1450)
	assertGiftLedger(b, 2, 250)
	assertGiftLedger(oldFriend, 1, 500)
	principal(inviter, 0)
	principal(a, 14000)
	principal(b, 2000)
	principal(oldFriend, 5000)
	for _, r := range rewardRows(inviter) {
		var f model.MarketingActivityFact
		must(model.DB.First(&f, r.FactID).Error)
		check(f.UserID == a || f.UserID == b, "old invitation or wrong friend entered reward source")
	}
	fmt.Println("SCENARIO_OK registration_and_invitation_owner_sources_friend_counters_separate_cash_ratio_vs_paid_ratio_old_invitation_excluded")
}
func checkinRewards() {
	admin, u := user("checkin_admin", 10900, 0), user("checkin", 10900, 0)
	spec := model.MarketingActivitySpec{Name: "checkin_combo", Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: 11000, EndsAt: 11900, Rewards: []model.MarketingActivityRewardSpec{fixed("1")}, Checkin: &model.MarketingActivityCheckinSpec{Condition: "none", CycleDays: 7, Milestones: []model.MarketingActivityMilestone{{Days: 7, Rewards: []model.MarketingActivityRewardSpec{fixed("2")}}}}}
	a := activity(admin, spec)
	// These are trusted calendar claims at the engine boundary. This does not
	// claim to validate the later calendar/continuity/makeup HTTP implementation.
	c := command{Action: "checkin", UserID: u, Reference: "checkin-day", Day: "1970-01-01", Now: 11100, Claims: []model.MarketingActivityCheckinClaim{{ActivityID: a.ID, MilestoneDays: 0, Ordinal: 1}, {ActivityID: a.ID, MilestoneDays: 7, Ordinal: 1}}}
	x, y := pair(c, c)
	success(x)
	success(y)
	check(x.Fact.ID == y.Fact.ID, "calendar fact duplicated")
	x, y = pair(decide(u, 11200), decide(u, 11200))
	success(x)
	success(y)
	fulfill(u, 11201)
	assertGiftLedger(u, 2, 150)
	principal(u, 0)
	fmt.Println("SCENARIO_OK trusted_checkin_fact_daily_plus_milestone_one_combination_repeat_safe_calendar_rules_deferred")
}

func serviceOrchestration() {
	now := time.Now().Unix()
	admin, u := user("service_admin", now-200, 0), user("service", now-10, 0)
	activity(admin, model.MarketingActivitySpec{Name: "service_registration", Kind: "registration", Condition: "registration", Frequency: "once", StartsAt: now - 100, EndsAt: now + 800, Rewards: []model.MarketingActivityRewardSpec{fixed("3"), fixed("2")}})
	success(run(command{Action: "registration", UserID: u, Reference: "service-registration", Now: now - 10}))
	must(model.DB.Exec(fmt.Sprintf("ALTER TABLE marketing_v2_gifts ADD CONSTRAINT activity_service_failure CHECK (user_id <> %d OR granted_quota <> 150)", u)).Error)
	failed := run(command{Action: "service", UserID: u})
	check(failed.Error != "" && failed.Service != nil && failed.Service.Pending && failed.Service.DeliveredItems == 1, "service did not report partial failure while continuing independent gift")
	rows := rewardRows(u)
	check(len(rows) == 2 && rows[0].State == "pending" && rows[1].State == "credited", "failed first gift prevented later independent gift")
	check(len(gifts(u)) == 1 && gifts(u)[0].GrantedQuota == 100, "service partial actual ledger incorrect")
	firstGift := rows[1].GiftID
	firstTime := rows[1].CreditedAt
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts DROP CONSTRAINT activity_service_failure").Error)
	a, b := pair(command{Action: "service", UserID: u}, command{Action: "service", UserID: u})
	success(a)
	success(b)
	check(a.Service != nil && b.Service != nil && !a.Service.Pending && !b.Service.Pending, "service did not finish pending recovery")
	rows = rewardRows(u)
	check(rows[1].GiftID == firstGift && rows[1].CreditedAt == firstTime, "service replaced already credited independent gift")
	lost(command{Action: "service", UserID: u})
	success(run(command{Action: "service", UserID: u}))
	assertGiftLedger(u, 2, 250)
	principal(u, 0)
	// Nine real facts exceed one bounded worker invocation (eight decision
	// batches). Pending must reflect remaining facts; a later worker finishes them.
	v := user("service_batches", now-10, 0)
	spec := recharge("service_recharge", "single_payment", "every", now-100, fixed("0.02"))
	spec.ThresholdCents = 1
	activity(admin, spec)
	for i := range 9 {
		c := payment(v, fmt.Sprintf("service-batch-%d", i), 1, "1", now-9+int64(i))
		must(model.DB.Transaction(func(tx *gorm.DB) error { _, err := model.CreditMarketingPayment(tx, c.Payment); return err }))
	}
	partial := run(command{Action: "service", UserID: v})
	success(partial)
	check(partial.Service != nil && partial.Service.Pending, "bounded service work falsely reported all facts finished")
	a, b = pair(command{Action: "service", UserID: v}, command{Action: "service", UserID: v})
	success(a)
	success(b)
	check(!a.Service.Pending && !b.Service.Pending, "subsequent workers failed to finish remaining fact")
	assertGiftLedger(v, 9, 9)
	principal(v, 450)
	fmt.Println("SCENARIO_OK real_service_partial_failure_continues_independent_gift_pending_recovery_and_batch_limit_not_reward_limit")
}

func draftAndPublication() {
	admin, u := user("draft_admin", 11900, 0), user("draft", 11900, 0)
	spec := recharge("draft_private_public", "single_payment", "every", 12000, fixed("2"))
	spec.ThresholdCents = 1
	var a *model.MarketingActivity
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = model.CreateMarketingActivity(tx, admin, "draft-create", spec, 11990)
		return err
	}))
	check(a.EnabledAt == 0 && !a.Published, "new activity should be an unpublished draft")
	old := payment(u, "draft-time-payment", 1, "1", 12100)
	success(run(old))
	drain(u, 12150)
	check(len(rewardRows(u)) == 0, "draft activity issued rewards")
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = model.ActivateMarketingActivity(tx, admin, a.ID, "draft-activate", 12200)
		return err
	}))
	check(a.EnabledAt == 12200 && !a.Published, "activation unexpectedly published activity")
	success(run(old))
	drain(u, 12210)
	check(len(rewardRows(u)) == 0, "activation backfilled draft-era fact")
	success(run(payment(u, "private-enabled-payment", 1, "1", 12300)))
	drain(u, 12310)
	fulfill(u, 12311)
	assertGiftLedger(u, 1, 100)
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.SetMarketingActivityPublished(tx, admin, a.ID, "draft-publish", true, 12400)
		return err
	}))
	success(run(payment(u, "published-enabled-payment", 1, "1", 12500)))
	drain(u, 12510)
	fulfill(u, 12511)
	assertGiftLedger(u, 2, 200)
	principal(u, 150)
	fmt.Println("SCENARIO_OK draft_no_award_no_activation_backfill_enabled_private_awards_publication_does_not_change_eligibility")
}

// This exact boundary reproduces starvation at the service's 8*128 item work
// limit. It is a business recovery regression, not a random load benchmark.
func serviceFairness() {
	admin, inviter := user("fair_admin", 12900, 0), user("fair_inviter", 12900, 0)
	source := user("fair_source", 13100, inviter)
	activity(admin, model.MarketingActivitySpec{Name: "fair_old_inviter_items", Kind: "invitation", Condition: "friend_cumulative", Frequency: "every", StartsAt: 13000, EndsAt: 13900, ThresholdCents: 1, Rewards: []model.MarketingActivityRewardSpec{fixed("0.02")}})
	spec := recharge("fair_later_source_item", "single_payment", "every", 13000, fixed("1"))
	spec.ThresholdCents = 1
	activity(admin, spec)
	success(run(command{Action: "invitation", UserID: source, InviterID: inviter, Reference: "fair-invitation", Now: 13100}))
	success(run(payment(source, "fair-payment", 1024, "1", 13200)))
	finished := false
	for range 40 {
		var f *model.MarketingActivityFact
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			f, err = model.ProcessNextMarketingActivityFact(tx, source, 13300)
			return err
		}))
		if f == nil {
			finished = true
			break
		}
	}
	check(finished, "fairness fixture did not persist complete original decisions")
	old := rewardRows(inviter)
	later := rewardRows(source)
	check(len(old) == 1024 && len(later) == 1 && old[1023].ID < later[0].ID, "fairness boundary not created with real activity rewards")
	must(model.DB.Model(&model.User{}).Where("id = ?", inviter).Update("status", common.UserStatusDisabled).Error)
	first := run(command{Action: "service", UserID: source})
	check(first.Error != "" && first.Service != nil && first.Service.Pending, "disabled beneficiary failure not retained")
	second := run(command{Action: "service", UserID: source})
	check(second.Error != "" && second.Service != nil && second.Service.Pending, "failed old obligations disappeared during cursor rotation")
	assertGiftLedger(source, 1, 50)
	principal(source, 50)
	check(len(gifts(inviter)) == 0, "disabled inviter received gift")
	for _, r := range rewardRows(inviter) {
		check(r.State == "pending" && r.GiftID == 0, "failed prefix was skipped permanently or falsely credited")
	}
	must(model.DB.Model(&model.User{}).Where("id = ?", inviter).Update("status", common.UserStatusEnabled).Error)
	done := false
	for range 3 {
		recovered := run(command{Action: "service", UserID: source})
		success(recovered)
		if recovered.Service != nil && !recovered.Service.Pending {
			done = true
			break
		}
	}
	check(done, "rotating cursor failed to revisit old obligations after access restoration")
	assertGiftLedger(inviter, 1024, 1024)
	principal(inviter, 0)
	assertGiftLedger(source, 1, 50)
	principal(source, 50)
	fmt.Println("SCENARIO_OK service_1024_failed_prefix_does_not_starve_later_beneficiary_old_obligations_recover_after_restore")
}
