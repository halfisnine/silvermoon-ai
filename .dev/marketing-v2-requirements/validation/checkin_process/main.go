// Check-in calendar behavior across real gateway and worker processes.
// Historical server times exist only in this fixture, never in product HTTP.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"gorm.io/gorm"
)

const trustedOrigin = "https://checkin-fixture.invalid"

type response struct {
	status     int
	header     http.Header
	cookies    []*http.Cookie
	Success    bool            `json:"success"`
	Message    string          `json:"message"`
	MessageKey string          `json:"message_key"`
	Code       string          `json:"code"`
	Data       json.RawMessage `json:"data"`
}
type identity struct {
	user     model.User
	password string
	token    string
	cookie   *http.Cookie
}

// Signed callback query strings must not become reusable credentials in saved
// fixture logs. Buffer complete lines so split writes cannot evade redaction.
type safeGatewayLog struct {
	mu      sync.Mutex
	file    *os.File
	pending string
}

var credentialHex = regexp.MustCompile(`[[:xdigit:]]{32,}`)

func (w *safeGatewayLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(data)
	for {
		line, rest, ok := strings.Cut(w.pending, "\n")
		if !ok {
			break
		}
		w.pending = rest
		if _, err := fmt.Fprintln(w.file, credentialHex.ReplaceAllString(line, "[redacted]")); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

type gateway struct {
	*exec.Cmd
	log *safeGatewayLog
}

func (g *gateway) stop() {
	_ = g.Process.Kill()
	_ = g.Wait()
	g.log.mu.Lock()
	defer g.log.mu.Unlock()
	if g.log.pending != "" {
		_, _ = fmt.Fprintln(g.log.file, credentialHex.ReplaceAllString(g.log.pending, "[redacted]"))
	}
	_ = g.log.file.Close()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func check(ok bool, label string) {
	if !ok {
		panic(label)
	}
}
func call(port, method, path, token string, body any, headers http.Header, cookie *http.Cookie) response {
	var data []byte
	var err error
	if body != nil {
		data, err = common.Marshal(body)
		must(err)
	}
	req, err := http.NewRequest(method, "http://127.0.0.1:"+port+path, strings.NewReader(string(data)))
	must(err)
	if headers != nil {
		req.Header = headers.Clone()
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	must(err)
	defer resp.Body.Close()
	data, err = io.ReadAll(resp.Body)
	must(err)
	r := response{status: resp.StatusCode, header: resp.Header, cookies: resp.Cookies()}
	if len(data) > 0 {
		_ = common.Unmarshal(data, &r)
	}
	return r
}
func success(r response, label string) {
	check(r.status == 200 && r.Success, fmt.Sprintf("%s status=%d code=%s", label, r.status, r.Code))
}
func denied(r response, label string) {
	check(!r.Success && (r.status == 401 || r.status == 403), fmt.Sprintf("%s status=%d code=%s", label, r.status, r.Code))
}
func launch(port string) *gateway {
	cmd := exec.Command("/fixtures/checkin-gateway", "--port", port, "--log-dir", "/tmp/mv2-checkin-"+port)
	cmd.Env = append(os.Environ(), "NODE_TYPE=master", "PORT="+port, "UPDATE_TASK=false", "SESSION_SECRET=isolated-checkin-process-secret", "CRYPTO_SECRET=isolated-checkin-process-secret", "SESSION_COOKIE_SECURE=true", "SESSION_COOKIE_TRUSTED_URL="+trustedOrigin, "PASSWORD_LOGIN_ENCRYPTION_ENABLED=false", "BATCH_UPDATE_ENABLED=true", "MEMORY_CACHE_ENABLED=true", "GIN_MODE=release", "CRITICAL_RATE_LIMIT=200")
	log, err := os.Create("/fixtures/checkin-gateway-" + port + "-" + os.Getenv("CHECKIN_FIXTURE_STAMP") + ".log")
	must(err)
	safeLog := &safeGatewayLog{file: log}
	cmd.Stdout, cmd.Stderr = safeLog, safeLog
	must(cmd.Start())
	fmt.Printf("CHECKIN_GATEWAY_STARTED port=%s pid=%d\n", port, cmd.Process.Pid)
	deadline := time.Now().Add(55 * time.Second)
	for time.Now().Before(deadline) {
		client := &http.Client{Timeout: time.Second}
		r, err := client.Get("http://127.0.0.1:" + port + "/api/status")
		if err == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				return &gateway{Cmd: cmd, log: safeLog}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	_ = log.Close()
	panic("gateway readiness timeout " + port)
}

func login(port string, who *identity) {
	r := call(port, "POST", "/api/user/login", "", map[string]any{"username": who.user.Username, "password": who.password}, nil, nil)
	success(r, "actual password login")
	var data struct {
		AccessToken string `json:"access_token"`
	}
	must(common.Unmarshal(r.Data, &data))
	check(data.AccessToken != "", "login returned access token")
	who.token = data.AccessToken
	for _, c := range r.cookies {
		if c.Value != "" && c.MaxAge >= 0 {
			who.cookie = c
			break
		}
	}
	check(who.cookie != nil && who.cookie.Secure && who.cookie.HttpOnly, "secure refresh cookie returned")
}

type command struct {
	UserID                             int
	Day                                string
	Now                                int64
	LoseReply, CloseRedis, RecoverOnly bool
}
type result struct {
	Checkin *model.MarketingCheckinResult
	Error   string
}
type worker struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Scanner
	cancel context.CancelFunc
}

func stamp(s string) int64 {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.FixedZone("Asia/Shanghai", 8*3600))
	must(err)
	return t.Unix()
}
func emit(label string, v any) {
	b, err := common.Marshal(v)
	must(err)
	fmt.Printf("%s %s\n", label, b)
}
func commit(user int, day string, now int64) result {
	var r result
	err := model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		r.Checkin, err = model.CommitMarketingCheckin(tx, user, day, now)
		return err
	})
	if err != nil {
		r.Error = err.Error()
		return r
	}
	processed, err := service.ProcessMarketingActivityFacts(context.Background(), user)
	must(err)
	check(!processed.Pending, "check-in delivery must finish")
	return r
}
func accepted(r result) {
	check(r.Error == "" && r.Checkin != nil, "check-in should succeed: "+r.Error)
}
func work() {
	var c command
	must(common.UnmarshalJsonStr(os.Args[2], &c))
	if c.CloseRedis {
		must(common.RDB.Close())
	}
	emit("READY", os.Getpid())
	scanner := bufio.NewScanner(os.Stdin)
	check(scanner.Scan(), "worker barrier")
	if c.RecoverOnly {
		processed, err := service.ProcessMarketingActivityFacts(context.Background(), c.UserID)
		must(err)
		check(!processed.Pending, "recovery completed committed reward obligations")
		emit("RESULT", result{})
		return
	}
	r := commit(c.UserID, c.Day, c.Now)
	if c.LoseReply {
		accepted(r)
		os.Exit(73)
	}
	emit("RESULT", r)
}
func start(c command) *worker {
	b, err := common.Marshal(c)
	must(err)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "worker", string(b))
	in, err := cmd.StdinPipe()
	must(err)
	out, err := cmd.StdoutPipe()
	must(err)
	cmd.Stderr = os.Stderr
	must(cmd.Start())
	p := &worker{cmd: cmd, in: in, out: bufio.NewScanner(out), cancel: cancel}
	p.out.Buffer(make([]byte, 4096), 1024*1024)
	for p.out.Scan() {
		if strings.HasPrefix(p.out.Text(), "READY ") {
			fmt.Printf("CHECKIN_WORKER_READY pid=%d\n", cmd.Process.Pid)
			return p
		}
	}
	panic("worker did not become ready")
}
func (p *worker) release() { _, err := io.WriteString(p.in, "go\n"); must(err) }
func (p *worker) finish(lost bool) result {
	defer p.cancel()
	defer p.in.Close()
	var r result
	found := false
	for p.out.Scan() {
		if s, ok := strings.CutPrefix(p.out.Text(), "RESULT "); ok {
			must(common.UnmarshalJsonStr(s, &r))
			found = true
		}
	}
	err := p.cmd.Wait()
	if lost {
		var e *exec.ExitError
		check(errors.As(err, &e) && e.ExitCode() == 73, "committed response loss exit")
		return r
	}
	must(err)
	check(found, "worker result missing")
	return r
}
func concurrent(a, b command) (result, result) {
	one := start(a)
	two := start(b)
	one.release()
	two.release()
	return one.finish(a.LoseReply), two.finish(b.LoseReply)
}
func user(name string, created int64) model.User {
	u := model.User{Username: name, AffCode: name, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", CreatedAt: created}
	must(model.DB.Create(&u).Error)
	// Calendar fixtures represent pre-existing classified users, not registration
	// integration. No historical registration or signup reward is manufactured.
	must(model.DB.Create(&model.MarketingAccount{UserID: u.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "isolated calendar fixture"}).Error)
	return u
}
func reward(amount string) model.MarketingActivityRewardSpec {
	return model.MarketingActivityRewardSpec{Kind: "gift", Calculation: "fixed", ValidSeconds: 3600, Money: model.MarketingMoneySnapshot{Amount: amount, QuotaPerUnit: "100", CurrencyPerUnit: "1", Currency: "CNY"}}
}
func activity(admin int, name string, from, to int64, spec model.MarketingActivityCheckinSpec, created int64) *model.MarketingActivity {
	value := model.MarketingActivitySpec{Published: true, Name: name, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: from, EndsAt: to, Rewards: []model.MarketingActivityRewardSpec{reward("1")}, Checkin: &spec}
	var a *model.MarketingActivity
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = model.CreateMarketingActivity(tx, admin, "create-"+name, value, created)
		if err != nil {
			return err
		}
		a, err = model.ActivateMarketingActivity(tx, admin, a.ID, "activate-"+name, created)
		return err
	}))
	return a
}
func total(user int, a int64) int64 {
	var gifts []model.MarketingGift
	must(model.DB.Joins("JOIN marketing_v2_activity_rewards ON marketing_v2_activity_rewards.gift_id = marketing_v2_gifts.id").Where("marketing_v2_activity_rewards.user_id = ? AND marketing_v2_activity_rewards.activity_id = ?", user, a).Find(&gifts).Error)
	var quota int64
	for _, g := range gifts {
		quota += g.GrantedQuota
	}
	return quota
}
func rows(user int, a int64) []model.MarketingCheckinDay {
	var out []model.MarketingCheckinDay
	must(model.DB.Where("user_id = ? AND activity_id = ?", user, a).Order("day").Find(&out).Error)
	return out
}
func makeupUsed(user int, a int64) int {
	n := 0
	for _, r := range rows(user, a) {
		if r.Makeup {
			n++
		}
	}
	return n
}
func pay(user int, ref, kind string, cents, at int64) {
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user, Kind: kind, Reference: ref, PaidCents: cents, PostedAt: at, Money: model.MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "100", CurrencyPerUnit: "1", Currency: "CNY"}})
		return err
	}))
}
func noPrincipal(u model.User) {
	var current model.User
	must(model.DB.First(&current, u.Id).Error)
	check(current.Quota == u.Quota, "check-in cannot create paid principal")
}
func milestoneScenario(admin int) {
	s := model.MarketingActivityCheckinSpec{Condition: "none", Milestones: []model.MarketingActivityMilestone{{Days: 7, Rewards: []model.MarketingActivityRewardSpec{reward("2")}}}}
	a := activity(admin, "once-seven", stamp("2025-01-01 00:00:00"), stamp("2025-02-01 00:00:00"), s, stamp("2024-12-31 00:00:00"))
	u := user("once-seven", stamp("2024-12-30 00:00:00"))
	// Legacy calendar and its random quota never contribute to the new streak.
	must(model.DB.Create(&model.Checkin{UserId: u.Id, CheckinDate: "2024-12-31", QuotaAwarded: 999999, CreatedAt: stamp("2024-12-31 12:00:00")}).Error)
	for d := 1; d <= 14; d++ {
		accepted(commit(u.Id, "", stamp(fmt.Sprintf("2025-01-%02d 12:00:00", d))))
		if d == 6 {
			check(total(u.Id, a.ID) == 600, "legacy day must not complete seven-day milestone")
		}
		if d == 7 {
			check(total(u.Id, a.ID) == 900, "seven days daily1 plus extra2 equals9")
		}
	}
	check(total(u.Id, a.ID) == 1600, "once milestone not repeated at day14")
	noPrincipal(u)
	s.CycleDays = 7
	a = activity(admin, "cycle-seven", stamp("2025-02-01 00:00:00"), stamp("2025-03-01 00:00:00"), s, stamp("2025-01-31 00:00:00"))
	u = user("cycle-seven", stamp("2025-01-30 00:00:00"))
	for d := 1; d <= 14; d++ {
		accepted(commit(u.Id, "", stamp(fmt.Sprintf("2025-02-%02d 12:00:00", d))))
	}
	check(total(u.Id, a.ID) == 1800, "two full cycles receive two bonuses")
	broken := user("broken-cycle", stamp("2025-01-30 00:00:00"))
	for _, d := range []int{1, 2, 4, 5, 6, 7, 8, 9, 10} {
		accepted(commit(broken.Id, "", stamp(fmt.Sprintf("2025-02-%02d 12:00:00", d))))
	}
	check(total(broken.Id, a.ID) == 1100, "gap restarts continuous cycle")
	fmt.Println("CHECKIN_STREAK_OK seven_days=9 day_seven=3 once_day14=16 cycle_day14=18 gap_restarts=true old_calendar_ignored=true")
}
func qualificationScenario(admin int) {
	from, to, created := stamp("2025-03-01 00:00:00"), stamp("2025-04-01 00:00:00"), stamp("2025-02-28 00:00:00")
	base := model.MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 7, MakeupLimit: 5, MakeupDaily: true}
	free := activity(admin, "qual-free", from, to, base, created)
	base.Condition = "today_paid"
	base.ThresholdCents = 1000
	today := activity(admin, "qual-today", from, to, base, created)
	base.Condition = "previous_day_paid"
	previous := activity(admin, "qual-previous", from, to, base, created)
	u := user("qualification", created-100)
	pay(u.Id, "march2-before", "online", 999, stamp("2025-03-01 23:59:59"))
	pay(u.Id, "march2-a", "online", 300, stamp("2025-03-02 00:00:00"))
	pay(u.Id, "march2-b", "paid_code", 700, stamp("2025-03-02 12:00:00"))
	r := commit(u.Id, "", stamp("2025-03-02 12:00:01"))
	accepted(r)
	check(len(r.Checkin.ActivityIDs) == 2, "one click includes free and sum-qualified today")
	check(total(u.Id, free.ID) == 100 && total(u.Id, today.ID) == 100 && total(u.Id, previous.ID) == 0, "Shanghai previous day999 excluded from threshold1000")
	accepted(commit(u.Id, "", stamp("2025-03-03 00:00:00")))
	check(total(u.Id, today.ID) == 100 && total(u.Id, previous.ID) == 100, "Shanghai midnight changes payment qualification day")
	pay(u.Id, "march4-new", "online", 2000, stamp("2025-03-04 12:00:00"))
	accepted(commit(u.Id, "2025-03-01", stamp("2025-03-04 12:00:01")))
	check(total(u.Id, today.ID) == 100 && total(u.Id, previous.ID) == 100, "today payment cannot manufacture past day qualification")
	trows := rows(u.Id, today.ID)
	check(len(trows) == 1 && trows[0].QualificationDay == "2025-03-02" && trows[0].QualifiedPaidCents == 1000, "source payment-day proof retained")
	// Qualify an unsigned past day using only its own successful payment facts.
	past := user("qualification-makeup", created-100)
	pay(past.Id, "past-today", "paid_code", 1000, stamp("2025-03-05 10:00:00"))
	accepted(commit(past.Id, "2025-03-05", stamp("2025-03-07 12:00:00")))
	check(total(past.Id, today.ID) == 100, "makeup uses original target-day paid source")
	fmt.Println("CHECKIN_QUALIFICATION_OK all_three_conditions=true shanghai_midnight=true paid_code_and_online_sum=true makeup_target_day_only=true")
}
func makeupScenario(admin int) {
	spec := model.MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 7, MakeupLimit: 2, MakeupDaily: false, Milestones: []model.MarketingActivityMilestone{{Days: 3, Rewards: []model.MarketingActivityRewardSpec{reward("2")}}}}
	a := activity(admin, "cross-month", stamp("2025-04-25 00:00:00"), stamp("2025-05-10 00:00:00"), spec, stamp("2025-04-24 00:00:00"))
	u := user("cross-month", stamp("2025-04-23 00:00:00"))
	accepted(commit(u.Id, "", stamp("2025-04-30 12:00:00")))
	x, y := concurrent(command{UserID: u.Id, Day: "2025-04-29", Now: stamp("2025-04-30 12:00:01")}, command{UserID: u.Id, Day: "2025-04-29", Now: stamp("2025-04-30 12:00:01")})
	accepted(x)
	accepted(y)
	check(makeupUsed(u.Id, a.ID) == 1 && total(u.Id, a.ID) == 100, "same makeup day consumes one allowance and skips daily gift")
	accepted(commit(u.Id, "", stamp("2025-05-02 12:00:00")))
	x, y = concurrent(command{UserID: u.Id, Day: "2025-05-01", Now: stamp("2025-05-04 12:00:00")}, command{UserID: u.Id, Day: "2025-05-03", Now: stamp("2025-05-04 12:00:00")})
	check((x.Error == "") != (y.Error == ""), "only one last makeup allowance wins")
	check(makeupUsed(u.Id, a.ID) == 2, "cross-month allowance never resets")
	if x.Error != "" {
		check(total(u.Id, a.ID) == 200, "failed gap repair cannot award milestone")
	} else {
		check(total(u.Id, a.ID) == 400, "successful gap repair adds extra only")
	}
	retry := commit(u.Id, "2025-04-29", stamp("2025-05-04 13:00:00"))
	accepted(retry)
	check(retry.Checkin.AlreadyRecorded && makeupUsed(u.Id, a.ID) == 2, "replay consumes no additional allowance")
	calendar, err := model.GetMarketingCheckinCalendar(context.Background(), u.Id, "2025-05", stamp("2025-05-04 14:00:00"))
	must(err)
	check(len(calendar.Activities) == 1 && calendar.Activities[0].MakeupUsed == 2 && calendar.Activities[0].MakeupRemaining == 0, "calendar May includes April used allowance")
	v := user("independent-makeup", stamp("2025-04-23 00:00:00"))
	accepted(commit(v.Id, "2025-05-01", stamp("2025-05-02 12:00:00")))
	check(makeupUsed(v.Id, a.ID) == 1, "makeup allowance belongs to each user")
	// A separate deterministic sequence repairs the missing third day, then
	// extends and replays it without re-awarding an already granted milestone.
	fix := user("gap-fix", stamp("2025-04-23 00:00:00"))
	accepted(commit(fix.Id, "", stamp("2025-04-29 12:00:00")))
	accepted(commit(fix.Id, "", stamp("2025-05-01 12:00:00")))
	accepted(commit(fix.Id, "2025-04-30", stamp("2025-05-02 12:00:00")))
	check(total(fix.Id, a.ID) == 400, "makeup repairs continuous milestone")
	accepted(commit(fix.Id, "", stamp("2025-05-02 13:00:00")))
	accepted(commit(fix.Id, "2025-04-30", stamp("2025-05-03 12:00:00")))
	check(total(fix.Id, a.ID) == 500, "repaired milestone cannot repeat")
	spec.MakeupDaily = true
	b := activity(admin, "makeup-daily", stamp("2025-06-01 00:00:00"), stamp("2025-07-01 00:00:00"), spec, stamp("2025-05-31 00:00:00"))
	v = user("makeup-daily", stamp("2025-05-30 00:00:00"))
	accepted(commit(v.Id, "2025-06-01", stamp("2025-06-02 12:00:00")))
	check(total(v.Id, b.ID) == 100, "makeup daily switch grants original daily gift")
	check(commit(v.Id, "2025-06-02", stamp("2025-06-02 12:00:00")).Error != "", "makeup today forbidden")
	check(commit(v.Id, "2025-06-03", stamp("2025-06-02 12:00:00")).Error != "", "future makeup forbidden")
	check(commit(v.Id, "2025-06-03", stamp("2025-06-20 12:00:00")).Error != "", "makeup outside recent N days forbidden")
	fmt.Println("CHECKIN_MAKEUP_OK same_day_process_race=true last_allowance_process_race=true cross_month_total=2 per_user=true daily_switch=true repaired_milestone_once=true")
}
func copyAndEndScenario(admin int) {
	spec := model.MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 7, MakeupLimit: 5, MakeupDaily: true, Milestones: []model.MarketingActivityMilestone{{Days: 3, Rewards: []model.MarketingActivityRewardSpec{reward("2")}}}}
	original := activity(admin, "original", stamp("2025-07-01 00:00:00"), stamp("2025-07-10 00:00:00"), spec, stamp("2025-06-30 00:00:00"))
	u := user("copied-calendar", stamp("2025-06-29 00:00:00"))
	accepted(commit(u.Id, "", stamp("2025-07-02 12:00:00")))
	copied := activity(admin, "copied", original.StartsAt, original.EndsAt, spec, stamp("2025-07-03 12:00:00"))
	accepted(commit(u.Id, "2025-07-01", stamp("2025-07-04 11:00:00")))
	check(total(u.Id, original.ID) == 200 && total(u.Id, copied.ID) == 0, "copy cannot inherit original missing day")
	accepted(commit(u.Id, "", stamp("2025-07-04 12:00:00")))
	accepted(commit(u.Id, "2025-07-03", stamp("2025-07-05 12:00:00")))
	check(total(u.Id, original.ID) == 600 && total(u.Id, copied.ID) == 200, "copied streak excludes original earlier days")
	var renamed model.MarketingActivitySpec
	must(common.UnmarshalJsonStr(original.Spec, &renamed))
	renamed.Name = "renamed-original"
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.UpdateMarketingActivity(tx, admin, original.ID, "rename-after-start", renamed, stamp("2025-07-05 13:00:00"))
		return err
	})
	check(err != nil, "active core updates require new object")
	accepted(commit(u.Id, "2025-07-01", stamp("2025-07-05 14:00:00")))
	check(total(u.Id, original.ID) == 600 && total(u.Id, copied.ID) == 200, "replay does not move original claims to copy")
	check(commit(u.Id, "2025-06-30", stamp("2025-07-05 14:00:00")).Error != "", "makeup before activity start rejected")
	check(commit(u.Id, "2025-07-09", stamp("2025-07-10 00:00:00")).Error != "", "ended activity cannot accept new makeup")
	late := user("late-created", stamp("2025-07-05 10:00:00"))
	check(commit(late.Id, "2025-07-04", stamp("2025-07-06 12:00:00")).Error != "", "user cannot makeup before account existed")
	fmt.Println("CHECKIN_COPY_BOUNDARIES_OK original_history_retained=true copy_no_inherited_days=true rename_not_replay=true before_start_and_account_rejected=true ended_rejected=true")
}
func raceScenario(admin int) {
	s := model.MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 7, MakeupLimit: 3, MakeupDaily: true}
	a := activity(admin, "races", stamp("2025-08-01 00:00:00"), stamp("2025-09-01 00:00:00"), s, stamp("2025-07-31 00:00:00"))
	u := user("race-user", stamp("2025-07-30 00:00:00"))
	x, y := concurrent(command{UserID: u.Id, Now: stamp("2025-08-02 12:00:00")}, command{UserID: u.Id, Now: stamp("2025-08-02 12:00:00")})
	accepted(x)
	accepted(y)
	check(x.Checkin.FactID == y.Checkin.FactID && len(rows(u.Id, a.ID)) == 1 && total(u.Id, a.ID) == 100, "same day multi-process commits one fact and award")
	x, y = concurrent(command{UserID: u.Id, Now: stamp("2025-08-03 23:59:59")}, command{UserID: u.Id, Day: "2025-08-03", Now: stamp("2025-08-04 00:00:00")})
	accepted(x)
	accepted(y)
	check(x.Checkin.FactID == y.Checkin.FactID && len(rows(u.Id, a.ID)) == 2 && total(u.Id, a.ID) == 200 && makeupUsed(u.Id, a.ID) <= 1, "midnight normal/makeup cross race cannot double award")
	p := start(command{UserID: u.Id, Now: stamp("2025-08-05 12:00:00"), LoseReply: true})
	p.release()
	p.finish(true)
	x, y = concurrent(command{UserID: u.Id, Now: stamp("2025-08-05 12:00:00")}, command{UserID: u.Id, Now: stamp("2025-08-05 12:00:00"), CloseRedis: true})
	accepted(x)
	accepted(y)
	check(len(rows(u.Id, a.ID)) == 3 && total(u.Id, a.ID) == 300, "lost reply and failed Redis retain authoritative uniqueness")
	noPrincipal(u)
	fmt.Println("CHECKIN_PROCESS_RACES_OK same_day=true midnight_checkin_makeup=true committed_reply_lost=true redis_failure_replay=true one_award_per_date=true")
}
func anchorScenario(admin int) {
	spec := model.MarketingActivityCheckinSpec{Condition: "none", CycleDays: 7, MakeupDays: 7, MakeupLimit: 2, MakeupDaily: true, Milestones: []model.MarketingActivityMilestone{{Days: 3, Rewards: []model.MarketingActivityRewardSpec{reward("2")}}}}
	a := activity(admin, "anchor-merge", stamp("2025-09-01 00:00:00"), stamp("2025-10-01 00:00:00"), spec, stamp("2025-08-31 00:00:00"))
	u := user("anchor-user", stamp("2025-08-30 00:00:00"))
	for _, d := range []int{1, 2, 3, 5, 6, 7} {
		accepted(commit(u.Id, "", stamp(fmt.Sprintf("2025-09-%02d 12:00:00", d))))
	}
	check(total(u.Id, a.ID) == 1000, "two separate third-day awards issued")
	accepted(commit(u.Id, "2025-09-04", stamp("2025-09-08 12:00:00")))
	check(total(u.Id, a.ID) == 1100, "gap merge adds daily only and keeps previous awards")
	for _, d := range []int{10, 11} {
		accepted(commit(u.Id, "", stamp(fmt.Sprintf("2025-09-%02d 12:00:00", d))))
	}
	x, y := concurrent(command{UserID: u.Id, Now: stamp("2025-09-12 12:00:00")}, command{UserID: u.Id, Now: stamp("2025-09-12 12:00:00")})
	accepted(x)
	accepted(y)
	check(total(u.Id, a.ID) == 1600, "merged old streaks cannot swallow independent new streak bonus")
	var claims []model.MarketingCheckinClaim
	must(model.DB.Where("user_id = ? AND activity_id = ?", u.Id, a.ID).Find(&claims).Error)
	check(len(claims) == 3, "three independent continuous responsibilities retained")
	fmt.Println("CHECKIN_ANCHOR_OK gap_merge_no_reaward=true old_awards_retained=true new_segment_earns_bonus=true concurrent_new_segment_once=true")
}
func exclusionScenario(admin int) {
	u := user("exclusive-checkin", stamp("2025-09-30 00:00:00"))
	var ids []int64
	for i := range 2 {
		name := fmt.Sprintf("exclusive-%d", i)
		spec := model.MarketingActivitySpec{Published: true, Name: name, Kind: "checkin", Condition: "checkin", Frequency: "every", StartsAt: stamp("2025-10-01 00:00:00"), EndsAt: stamp("2025-11-01 00:00:00"), ExclusiveGroup: "checkin-exclusive", Priority: i, Rewards: []model.MarketingActivityRewardSpec{reward("1")}, Checkin: &model.MarketingActivityCheckinSpec{Condition: "none"}}
		var a *model.MarketingActivity
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			a, err = model.CreateMarketingActivity(tx, admin, "create-"+name, spec, stamp("2025-09-30 00:00:00"))
			if err != nil {
				return err
			}
			a, err = model.ActivateMarketingActivity(tx, admin, a.ID, "enable-"+name, stamp("2025-09-30 00:00:00"))
			return err
		}))
		ids = append(ids, a.ID)
	}
	calendar, err := model.GetMarketingCheckinCalendar(context.Background(), u.Id, "2025-10", stamp("2025-10-01 12:00:00"))
	must(err)
	check(len(calendar.Activities) == 2, "published exclusive activities remain visible")
	for _, item := range calendar.Activities {
		if item.Activity.ID == ids[0] {
			check(item.TodayEligibility == "suppressed", "calendar marks priority loser suppressed")
		} else {
			check(item.TodayEligibility == "eligible", "calendar marks priority winner eligible")
		}
	}
	r := commit(u.Id, "", stamp("2025-10-01 12:00:00"))
	accepted(r)
	check(len(r.Checkin.ActivityIDs) == 1 && r.Checkin.ActivityIDs[0] == ids[1] && total(u.Id, ids[0]) == 0 && total(u.Id, ids[1]) == 100, "one click evaluates all activities and awards priority winner")
	fmt.Println("CHECKIN_EXCLUSION_OK one_click_all_eligible=true priority_winner_only=true no_user_activity_selection=true")
}
func pauseQualificationScenario(admin int) {
	spec := model.MarketingActivityCheckinSpec{Condition: "today_paid", ThresholdCents: 1000, MakeupDays: 7, MakeupLimit: 2, MakeupDaily: true}
	a := activity(admin, "paid-before-pause", stamp("2025-11-01 00:00:00"), stamp("2025-12-01 00:00:00"), spec, stamp("2025-10-31 00:00:00"))
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.SetMarketingActivityPaused(tx, admin, a.ID, "pause-nov3", true, stamp("2025-11-03 12:00:00"))
		return err
	}))
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.SetMarketingActivityPaused(tx, admin, a.ID, "resume-nov4", false, stamp("2025-11-04 09:00:00"))
		return err
	}))
	late := user("paid-after-pause", stamp("2025-10-30 00:00:00"))
	early := user("paid-before-pause", stamp("2025-10-30 00:00:00"))
	pay(late.Id, "nov3-late", "online", 1000, stamp("2025-11-03 13:00:00"))
	pay(early.Id, "nov3-early", "paid_code", 1000, stamp("2025-11-03 11:00:00"))
	check(commit(late.Id, "2025-11-03", stamp("2025-11-05 12:00:00")).Error != "", "payment only after pause cannot fabricate historic signup eligibility")
	accepted(commit(early.Id, "2025-11-03", stamp("2025-11-05 12:00:00")))
	check(total(early.Id, a.ID) == 100 && total(late.Id, a.ID) == 0, "target-day eligibility intersects actual unpaused time")
	fmt.Println("CHECKIN_PAUSE_QUALIFICATION_OK before_pause_qualified=true after_pause_payment_rejected=true target_day_intersection=true")
}
func httpScenario(admin model.User) {
	now := common.GetTimestamp()
	location := time.FixedZone("Asia/Shanghai", 8*3600)
	today := time.Unix(now, 0).In(location).Format("2006-01-02")
	s := model.MarketingActivityCheckinSpec{Condition: "none", MakeupDays: 3, MakeupLimit: 2, MakeupDaily: true}
	a := activity(admin.Id, "http-current", now-3*86400, now+3600, s, now-3*86400)
	who := identity{user: user("http-checkin", now-4*86400), password: common.GetUUID() + "aA9!"}
	hash, err := common.Password2Hash(who.password)
	must(err)
	must(model.DB.Model(&who.user).Update("password", hash).Error)
	// Enable large legacy rewards to prove they are no longer a parallel source.
	for key, value := range map[string]string{"checkin_setting.enabled": "true", "checkin_setting.min_quota": "7777777", "checkin_setting.max_quota": "7777777", "QuotaPerUnit": "100", "USDExchangeRate": "1", "general_setting.quota_display_type": "CNY"} {
		must(model.DB.Create(&model.Option{Key: key, Value: value}).Error)
	}
	g1 := launch("18581")
	defer g1.stop()
	g2 := launch("18582")
	defer g2.stop()
	login("18581", &who)
	for _, path := range []string{"/api/user/checkin", "/api/user/checkin/makeup"} {
		denied(call("18581", "POST", path, "", map[string]any{"date": today}, nil, nil), "anonymous checkin")
		denied(call("18582", "POST", path, "", map[string]any{"date": today}, http.Header{"Origin": {"https://attacker.invalid"}}, who.cookie), "cookie-only malicious origin checkin")
	}
	var replies [2]response
	var race sync.WaitGroup
	race.Go(func() { replies[0] = call("18581", "POST", "/api/user/checkin", who.token, nil, nil, nil) })
	race.Go(func() { replies[1] = call("18582", "POST", "/api/user/checkin", who.token, nil, nil, nil) })
	race.Wait()
	success(replies[0], "gateway one checkin")
	success(replies[1], "gateway two replay")
	check(total(who.user.Id, a.ID) == 100 && len(rows(who.user.Id, a.ID)) == 1, "actual gateways grant one daily gift")
	records := rows(who.user.Id, a.ID)
	check(records[0].Day == today, "server Shanghai day controls HTTP")
	spoof := call("18582", "POST", "/api/user/checkin", who.token, map[string]any{"date": "2000-01-01", "now": 1, "user_id": admin.Id, "activity_ids": []int64{a.ID + 1}}, nil, nil)
	check(spoof.status != 500, "spoofed timing handled")
	check(len(rows(who.user.Id, a.ID)) == 1 && len(rows(admin.Id, a.ID)) == 0, "HTTP cannot choose clock or beneficiary")
	r := call("18581", "POST", "/api/user/checkin/makeup", who.token, map[string]any{"date": today, "now": now + 86400}, nil, nil)
	check(!r.Success, "HTTP fake future now cannot makeup today")
	yesterday := time.Unix(now, 0).In(location).AddDate(0, 0, -1).Format("2006-01-02")
	race.Go(func() {
		replies[0] = call("18581", "POST", "/api/user/checkin/makeup", who.token, map[string]any{"date": yesterday}, nil, nil)
	})
	race.Go(func() {
		replies[1] = call("18582", "POST", "/api/user/checkin/makeup", who.token, map[string]any{"date": yesterday}, nil, nil)
	})
	race.Wait()
	success(replies[0], "gateway makeup one")
	success(replies[1], "gateway makeup replay")
	check(total(who.user.Id, a.ID) == 200 && makeupUsed(who.user.Id, a.ID) == 1, "actual gateways perform one makeup gift and allowance")
	r = call("18581", "GET", "/api/user/checkin?month="+today[:7], who.token, nil, nil, nil)
	success(r, "calendar read")
	var calendar struct {
		Today      string `json:"today"`
		Activities []struct {
			Daily []struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			} `json:"daily_rewards"`
			Records []struct {
				Date string `json:"date"`
			} `json:"records"`
		} `json:"activities"`
	}
	must(common.Unmarshal(r.Data, &calendar))
	expectedMonthRows := 1
	if yesterday[:7] == today[:7] {
		expectedMonthRows = 2
	}
	check(calendar.Today == today && len(calendar.Activities) == 1 && len(calendar.Activities[0].Records) == expectedMonthRows, "calendar current user's actual day")
	check(len(calendar.Activities[0].Daily) == 1 && calendar.Activities[0].Daily[0].Amount == "1" && calendar.Activities[0].Daily[0].Currency == "CNY", "calendar displays amount using configuration")
	for _, field := range []string{"quota", "payload_hash", "rules", "qualified_paid_cents", "qualification_day"} {
		check(!strings.Contains(string(r.Data), "\""+field+"\""), "calendar hides internal field "+field)
	}
	noPrincipal(who.user)
	var old int64
	must(model.DB.Model(&model.Checkin{}).Where("user_id = ?", who.user.Id).Count(&old).Error)
	check(old == 0, "old checkin grants no parallel record")
	success(call("18581", "POST", "/api/user/auth/logout", "", nil, http.Header{"Origin": {trustedOrigin}}, who.cookie), "real logout revokes session")
	denied(call("18582", "POST", "/api/user/checkin", who.token, nil, nil, nil), "other gateway rejects revoked session")
	check(total(who.user.Id, a.ID) == 200, "revoked replay has no reward mutation")
	pending := identity{user: user("http-pending", now-4*86400), password: common.GetUUID() + "aA9!"}
	hash, err = common.Password2Hash(pending.password)
	must(err)
	must(model.DB.Model(&pending.user).Update("password", hash).Error)
	login("18581", &pending)
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts ADD CONSTRAINT checkin_fixture_delivery_failure CHECK (false) NOT VALID").Error)
	r = call("18581", "POST", "/api/user/checkin", pending.token, nil, nil, nil)
	success(r, "check-in committed despite gift delivery failure")
	var delivery struct {
		Pending bool `json:"rewards_pending"`
	}
	must(common.Unmarshal(r.Data, &delivery))
	check(delivery.Pending && len(rows(pending.user.Id, a.ID)) == 1 && total(pending.user.Id, a.ID) == 0, "HTTP success accurately reports pending rewards")
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts DROP CONSTRAINT checkin_fixture_delivery_failure").Error)
	x, y := concurrent(command{UserID: pending.user.Id, RecoverOnly: true}, command{UserID: pending.user.Id, RecoverOnly: true})
	check(x.Error == "" && y.Error == "", "two production recovery workers succeed")
	check(total(pending.user.Id, a.ID) == 100 && len(rows(pending.user.Id, a.ID)) == 1, "recovery credits one committed daily award")
	r = call("18582", "POST", "/api/user/checkin", pending.token, nil, nil, nil)
	success(r, "HTTP replay after recovery")
	must(common.Unmarshal(r.Data, &delivery))
	check(!delivery.Pending && total(pending.user.Id, a.ID) == 100, "recovered HTTP replay no duplicate award")
	noPrincipal(pending.user)
	fmt.Println("CHECKIN_PENDING_RECOVERY_OK real_http_committed=true success_pending=true gift_database_failure=true two_process_service_recovery_once=true replay_pending_false=true")
	fmt.Println("CHECKIN_HTTP_OK real_gateways=2 bearer_required=true cookie_only_rejected=true server_clock=true legacy_no_principal=true display_amounts=true actual_makeup_race=true logout_revocation=true")
}
func main() {
	check(strings.Contains(os.Getenv("SQL_DSN"), "/marketing_v2_checkin_process?"), "isolated checkin database")
	check(os.Getenv("REDIS_CONN_STRING") == "redis://redis:6379/9", "isolated Redis DB9")
	workerMode := len(os.Args) > 1 && os.Args[1] == "worker"
	common.CryptoSecret = "isolated-checkin-process-secret"
	common.IsMasterNode = !workerMode
	must(model.InitDB())
	must(model.InitLogDB())
	must(common.InitRedisClient())
	defer common.RDB.Close()
	if workerMode {
		work()
		return
	}
	must(common.RDB.FlushDB(context.Background()).Err())
	var version string
	must(model.DB.Raw("SELECT version()").Scan(&version).Error)
	fmt.Printf("CHECKIN_DATABASE %s redis_db=9\n", version)
	admin := model.User{Username: "checkinroot", AffCode: "checkinroot", Role: common.RoleRootUser, Status: common.UserStatusEnabled, CreatedAt: stamp("2024-01-01 00:00:00")}
	must(model.DB.Create(&admin).Error)
	milestoneScenario(admin.Id)
	qualificationScenario(admin.Id)
	makeupScenario(admin.Id)
	copyAndEndScenario(admin.Id)
	raceScenario(admin.Id)
	anchorScenario(admin.Id)
	exclusionScenario(admin.Id)
	pauseQualificationScenario(admin.Id)
	httpScenario(admin)
	fmt.Println("CHECKIN_ALL_OK historical_clock_fixture_only=true actual_http_current_time=true no_extra_middleware=true")
}
