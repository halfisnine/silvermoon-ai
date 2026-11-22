// Real OS-process verification for the MV2 funding model. This intentionally
// does not claim to exercise HTTP authentication or relay integration.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type command struct {
	Action            string
	Reserve           model.MarketingReservationInput
	Settle            model.MarketingSettlementInput
	CloseRedis        bool
	LoseReply         bool
	CrashBeforeCommit bool
}
type result struct {
	Funding *model.MarketingFunding
	Error   string
}
type process struct {
	cmd        *exec.Cmd
	input      io.WriteCloser
	output     *bufio.Scanner
	backendPID int
	cancel     context.CancelFunc
}
type fixture struct {
	user  model.User
	token model.Token
	gift  model.MarketingGift
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
func emit(label string, data any) {
	b, err := common.Marshal(data)
	must(err)
	fmt.Printf("%s %s\n", label, b)
}

func main() {
	worker := len(os.Args) > 1 && os.Args[1] == "worker"
	check(strings.Contains(os.Getenv("SQL_DSN"), "/marketing_v2_funding_process?"), "isolated funding database required")
	check(os.Getenv("REDIS_CONN_STRING") == "redis://redis:6379/15", "isolated shared Redis logical database required")
	common.CryptoSecret = "isolated-marketing-process-fixture-secret"
	common.IsMasterNode = !worker
	common.BatchUpdateEnabled = true // Deliberately enable legacy queue: core must bypass it.
	must(model.InitDB())
	must(common.InitRedisClient())
	if worker {
		work()
		return
	}
	defer common.RDB.Close()
	staleContention("stale", false, false)
	staleContention("expired", true, false)
	staleContention("closed", false, true)
	duplicateFacts()
	lostResponses()
	transactionCrash()
	fmt.Println("FUNDING_PROCESS_MATRIX_OK two_real_worker_processes shared_postgresql shared_redis_db_15")
}

func work() {
	var cmd command
	must(common.UnmarshalJsonStr(os.Args[2], &cmd))
	if cmd.CloseRedis {
		must(common.RDB.Close())
		check(common.RDB.Ping(context.Background()).Err() != nil, "Redis connection must actually fail")
	}
	tx := model.DB.Begin()
	must(tx.Error)
	var backend int
	must(tx.Raw("SELECT pg_backend_pid()").Scan(&backend).Error)
	emit("READY", map[string]int{"os_pid": os.Getpid(), "backend_pid": backend})
	scanner := bufio.NewScanner(os.Stdin)
	check(scanner.Scan(), "missing start barrier")
	var funding *model.MarketingFunding
	var err error
	switch cmd.Action {
	case "reserve":
		funding, err = model.ReserveMarketingFunding(tx, cmd.Reserve)
	case "settle":
		funding, err = model.SettleMarketingFunding(tx, cmd.Settle)
	default:
		panic("unknown worker action")
	}
	if err != nil {
		must(tx.Rollback().Error)
		if errors.Is(err, model.ErrMarketingInsufficient) {
			emit("RESULT", result{Error: "insufficient"})
			return
		}
		panic(err)
	}
	if cmd.CrashBeforeCommit {
		emit("MUTATED", map[string]int{"os_pid": os.Getpid()})
		// Parent SIGKILLs this process after confirming another backend waits on its
		// real PostgreSQL row lock. No deferred rollback runs in the killed process.
		scanner.Scan()
		panic("crash worker unexpectedly resumed")
	}
	must(tx.Commit().Error)
	if cmd.LoseReply {
		os.Exit(73)
	}
	emit("RESULT", result{Funding: funding})
}

func start(cmd command) *process {
	payload, err := common.Marshal(cmd)
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
			fmt.Printf("WORKER_READY os_pid=%d postgres_pid=%d\n", ids["os_pid"], p.backendPID)
			return p
		}
		fmt.Println(line)
	}
	panic("worker exited before readiness")
}
func (p *process) release() { _, err := io.WriteString(p.input, "go\n"); must(err) }
func (p *process) finish(expectedExit int) result {
	defer p.cancel()
	defer p.input.Close()
	var got result
	found := false
	for p.output.Scan() {
		line := p.output.Text()
		if data, ok := strings.CutPrefix(line, "RESULT "); ok {
			must(common.UnmarshalJsonStr(data, &got))
			found = true
		} else {
			fmt.Println(line)
		}
	}
	must(p.output.Err())
	err := p.cmd.Wait()
	if expectedExit == 0 {
		must(err)
		check(found, "missing committed response")
	} else {
		var exit *exec.ExitError
		check(errors.As(err, &exit) && exit.ExitCode() == expectedExit, "unexpected process termination")
		check(!found, "lost response scenario unexpectedly returned a result")
	}
	return got
}
func run(cmd command) result { p := start(cmd); p.release(); return p.finish(0) }
func pair(a, b command) (result, result) {
	p := start(a)
	q := start(b)
	p.release()
	q.release()
	return p.finish(0), q.finish(0)
}

func seed(name string) fixture {
	f := fixture{user: model.User{Username: "proc_" + name, AffCode: "proc_" + name, Status: common.UserStatusEnabled}, token: model.Token{Key: "disabled-external-use-process-" + name, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}}
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&f.user).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.MarketingAccount{UserID: f.user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "fixture:new-account:" + name}).Error; err != nil {
			return err
		}
		f.token.UserId = f.user.Id
		if err := tx.Create(&f.token).Error; err != nil {
			return err
		}
		_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: f.user.Id, Kind: "online", Reference: "process-paid-" + name, PaidCents: 20, PostedAt: 50, Money: model.MarketingMoneySnapshot{Amount: "20", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
		if err != nil {
			return err
		}
		gift, err := model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: f.user.Id, Kind: "admin_gift", Reference: "process-gift-" + name, GrantedAt: 50, Money: model.MarketingMoneySnapshot{Amount: "30", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
		if err == nil {
			f.gift = *gift
		}
		return err
	}))
	return f
}
func reservation(f fixture, reference string, quota int64) command {
	return command{Action: "reserve", Reserve: model.MarketingReservationInput{UserID: f.user.Id, TokenID: f.token.Id, RequestReference: reference, QualifiedAt: 100, Quota: quota, PricingEvidence: "fixed:process-fixture-v1"}}
}
func settlement(funding *model.MarketingFunding, quota int64) command {
	return command{Action: "settle", Settle: model.MarketingSettlementInput{UserID: funding.UserID, FundingID: funding.ID, Version: 1, ActualQuota: &quota, Final: true, EvidenceKind: "upstream_terminal", Evidence: "fixture:verified-terminal"}}
}
func sources(f fixture, paid, remaining, consumed, hold int64, facts int64) {
	var user model.User
	var token model.Token
	var gift model.MarketingGift
	var account model.MarketingAccount
	var tokenHold model.MarketingTokenHold
	must(model.DB.First(&user, f.user.Id).Error)
	must(model.DB.First(&token, f.token.Id).Error)
	must(model.DB.First(&gift, f.gift.ID).Error)
	must(model.DB.First(&account, "user_id = ?", f.user.Id).Error)
	must(model.DB.First(&tokenHold, "token_id = ?", f.token.Id).Error)
	check(int64(user.Quota) == paid, "paid balance mismatch")
	check(gift.RemainingQuota == remaining && gift.ConsumedQuota == consumed, "gift conservation mismatch")
	check(account.PaidReserved+gift.ReservedQuota == hold && tokenHold.ReservedQuota == hold, "source/token hold mismatch")
	check(int64(token.UsedQuota) == 50-paid-remaining && int64(token.RemainQuota) == 100-int64(token.UsedQuota), "token total differs from actual source consumption")
	var count int64
	must(model.DB.Model(&model.MarketingFunding{}).Where("user_id = ?", f.user.Id).Count(&count).Error)
	check(count == facts, "duplicate or missing funding fact")
	var entries []model.MarketingLedgerEntry
	must(model.DB.Where("user_id = ? AND kind = ?", f.user.Id, "consume").Find(&entries).Error)
	var total int64
	for _, entry := range entries {
		total += entry.AmountQuota
	}
	check(total == 50-paid-remaining, "ledger consumption duplicated or omitted")
}

func staleContention(name string, expire, closed bool) {
	f := seed(name)
	ctx := context.Background()
	userKey := "user:" + strconv.Itoa(f.user.Id)
	tokenKey := "token:" + common.GenerateHMAC(f.token.Key)
	must(common.RDB.HSet(ctx, userKey, "Id", f.user.Id, "CacheSchema", 2, "Quota", 1000000).Err())
	must(common.RDB.HSet(ctx, tokenKey, "Id", f.token.Id, "RemainQuota", 1000000, "UsedQuota", 0).Err())
	if expire {
		must(common.RDB.ExpireAt(ctx, userKey, time.Unix(1, 0)).Err())
		must(common.RDB.ExpireAt(ctx, tokenKey, time.Unix(1, 0)).Err())
		n, err := common.RDB.Exists(ctx, userKey, tokenKey).Result()
		must(err)
		check(n == 0, "cache expiry not observed")
	}
	a := reservation(f, name+"-a", 50)
	b := reservation(f, name+"-b", 50)
	a.CloseRedis = closed
	b.CloseRedis = closed
	x, y := pair(a, b)
	check((x.Funding != nil && y.Error == "insufficient") || (y.Funding != nil && x.Error == "insufficient"), "exactly one final-balance reservation must win")
	winner := x.Funding
	if winner == nil {
		winner = y.Funding
	}
	sources(f, 20, 30, 0, 50, 1)
	done := settlement(winner, 50)
	done.CloseRedis = closed
	check(run(done).Funding.State == "settled", "winner did not settle")
	sources(f, 0, 0, 30, 0, 1)
	denied := reservation(f, name+"-after-exhaustion", 1)
	denied.CloseRedis = closed
	check(run(denied).Error == "insufficient", "cache resurrected spent funds")
	if !expire {
		n, err := common.RDB.HGet(ctx, userKey, "Quota").Int64()
		must(err)
		check(n == 1000000, "core unexpectedly modified legacy user cache")
		n, err = common.RDB.HGet(ctx, tokenKey, "RemainQuota").Int64()
		must(err)
		check(n == 1000000, "core unexpectedly modified legacy token cache")
	}
	fmt.Printf("SCENARIO_OK cache_%s last_balance_single_winner paid20_gift30_once\n", name)
}
func duplicateFacts() {
	f := seed("duplicate")
	cmd := reservation(f, "duplicate-request", 50)
	a, b := pair(cmd, cmd)
	check(a.Funding != nil && b.Funding != nil && a.Funding.ID == b.Funding.ID, "duplicate request created multiple reservations")
	sources(f, 20, 30, 0, 50, 1)
	settle := settlement(a.Funding, 35)
	a, b = pair(settle, settle)
	check(a.Funding != nil && b.Funding != nil && a.Funding.State == "settled" && b.Funding.State == "settled", "duplicate settlement failed")
	sources(f, 15, 0, 30, 0, 1)
	var count int64
	must(model.DB.Model(&model.MarketingFundingOperation{}).Where("funding_id = ?", a.Funding.ID).Count(&count).Error)
	check(count == 1, "duplicate settlement operation persisted")
	fmt.Println("SCENARIO_OK concurrent_duplicate_reservation_and_settlement one_fact_one_charge")
}
func lostResponses() {
	f := seed("lost")
	cmd := reservation(f, "lost-request", 50)
	cmd.LoseReply = true
	p := start(cmd)
	p.release()
	p.finish(73)
	cmd.LoseReply = false
	funding := run(cmd).Funding
	check(funding != nil, "lost reservation retry failed")
	sources(f, 20, 30, 0, 50, 1)
	settle := settlement(funding, 35)
	settle.LoseReply = true
	p = start(settle)
	p.release()
	p.finish(73)
	settle.LoseReply = false
	check(run(settle).Funding.State == "settled", "lost settlement retry failed")
	sources(f, 15, 0, 30, 0, 1)
	fmt.Println("SCENARIO_OK process_exit_after_commit retry_returns_original_without_double_charge")
}
func transactionCrash() {
	f := seed("crash")
	cmd := reservation(f, "crashed-request", 40)
	cmd.CrashBeforeCommit = true
	victim := start(cmd)
	victim.release()
	mutated := false
	for victim.output.Scan() {
		if strings.HasPrefix(victim.output.Text(), "MUTATED ") {
			mutated = true
			break
		}
		fmt.Println(victim.output.Text())
	}
	check(mutated, "victim never mutated transaction")
	cmd.CrashBeforeCommit = false
	retry := start(cmd)
	retry.release()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	waiting := false
	for !waiting {
		var rows []struct{ WaitEventType string }
		must(model.DB.Raw("SELECT wait_event_type FROM pg_stat_activity WHERE pid = ?", retry.backendPID).Scan(&rows).Error)
		waiting = len(rows) == 1 && rows[0].WaitEventType == "Lock"
		if waiting {
			break
		}
		select {
		case <-deadline.C:
			panic("retry did not wait on real transaction lock")
		case <-ticker.C:
		}
	}
	fmt.Printf("POSTGRES_LOCK_OBSERVED blocked_backend=%d owner_backend=%d\n", retry.backendPID, victim.backendPID)
	must(victim.cmd.Process.Kill())
	victim.finish(-1)
	got := retry.finish(0)
	check(got.Funding != nil, "crash retry failed")
	sources(f, 20, 30, 0, 40, 1)
	check(run(settlement(got.Funding, 40)).Funding.State == "settled", "crash recovery settlement failed")
	sources(f, 10, 0, 30, 0, 1)
	fmt.Println("SCENARIO_OK sigkill_uncommitted_transaction_rolled_back waiting_process_recovered")
}
