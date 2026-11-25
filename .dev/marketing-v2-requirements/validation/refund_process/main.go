// Refund maintenance validation uses actual gateways and the production CLI.
// Only the external model provider and synthetic account history are fixtures.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const trustedOrigin = "https://refund-fixture.invalid"

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
func launch(port string, poll bool) *gateway {
	cmd := exec.Command("/fixtures/refund-gateway", "--port", port, "--log-dir", "/tmp/mv2-refund-"+port)
	cmd.Env = append(os.Environ(), "NODE_TYPE=master", "PORT="+port, fmt.Sprintf("UPDATE_TASK=%t", poll), "SESSION_SECRET=isolated-refund-process-secret", "CRYPTO_SECRET=isolated-refund-process-secret", "SESSION_COOKIE_SECURE=true", "SESSION_COOKIE_TRUSTED_URL="+trustedOrigin, "PASSWORD_LOGIN_ENCRYPTION_ENABLED=false", "BATCH_UPDATE_ENABLED=true", "MEMORY_CACHE_ENABLED=true", "GIN_MODE=release", "CRITICAL_RATE_LIMIT=200")
	log, err := os.Create("/fixtures/refund-gateway-" + port + "-" + os.Getenv("REFUND_FIXTURE_STAMP") + ".log")
	must(err)
	safeLog := &safeGatewayLog{file: log}
	cmd.Stdout, cmd.Stderr = safeLog, safeLog
	must(cmd.Start())
	fmt.Printf("REFUND_GATEWAY_STARTED port=%s pid=%d\n", port, cmd.Process.Pid)
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

func seedUser(name string, role int) identity {
	password := common.GetUUID() + "aA9!"
	hash, err := common.Password2Hash(password)
	must(err)
	u := model.User{Username: name, Password: hash, AffCode: name, Role: role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	must(model.DB.Create(&u).Error)
	must(model.DB.Create(&model.MarketingAccount{UserID: u.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "isolated new-account fixture"}).Error)
	return identity{user: u, password: password}
}
func credit(userID int, reference, amount string, cents int64) {
	must(model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: userID, Kind: "online", Reference: reference, PaidCents: cents, PostedAt: common.GetTimestamp(), Money: model.MarketingMoneySnapshot{Amount: amount, QuotaPerUnit: "500000", CurrencyPerUnit: "1", Currency: "CNY"}})
		return err
	}))
}
func gift(userID int, reference, amount string, duration int64) *model.MarketingGift {
	var value *model.MarketingGift
	now := common.GetTimestamp()
	expires := int64(0)
	if duration > 0 {
		expires = now + duration
	}
	must(model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		value, err = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: userID, Kind: "admin_gift", Reference: reference, GrantedAt: now, ExpiresAt: expires, Money: model.MarketingMoneySnapshot{Amount: amount, QuotaPerUnit: "500000", CurrencyPerUnit: "1", Currency: "CNY"}})
		return err
	}))
	return value
}
func apiKey(userID int) model.Token {
	token := model.Token{UserId: userID, Key: common.GetUUID() + common.GetUUID()[:16], Name: "refund isolated test", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10000000, Group: "default"}
	must(model.DB.Create(&token).Error)
	return token
}
func manage(port string, actor identity, userID int, action string) {
	proofReply := call(port, "POST", "/api/verify", actor.token, map[string]any{"method": "password", "scope": "admin.user.manage", "context": map[string]any{"user_id": userID, "action": action}, "password": actor.password}, nil, nil)
	success(proofReply, "actual management password proof")
	var proof struct {
		Token string `json:"proof_token"`
	}
	must(common.Unmarshal(proofReply.Data, &proof))
	check(proof.Token != "", "proof returned")
	success(call(port, "POST", "/api/user/manage", actor.token, map[string]any{"id": userID, "action": action}, http.Header{"X-Security-Proof": {proof.Token}}, nil), "actual management "+action)
}
func waitUntil(label string, condition func() bool) {
	deadline := time.NewTimer(55 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			panic("timeout " + label)
		case <-tick.C:
		}
	}
}
func relay(port string, token model.Token, content string, stream bool, override ...string) int {
	modelName := "gpt-4o"
	if len(override) > 0 {
		modelName = override[0]
	}
	raw, err := common.Marshal(map[string]any{"model": modelName, "stream": stream, "messages": []map[string]string{{"role": "user", "content": content}}})
	must(err)
	req, err := http.NewRequest("POST", "http://127.0.0.1:"+port+"/v1/chat/completions", strings.NewReader(string(raw)))
	must(err)
	req.Header.Set("Authorization", "Bearer sk-"+token.Key)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 55 * time.Second}
	resp, err := client.Do(req)
	must(err)
	_, err = io.Copy(io.Discard, resp.Body)
	must(err)
	must(resp.Body.Close())
	return resp.StatusCode
}

type provider struct {
	started     chan struct{}
	release     chan struct{}
	calls       atomic.Int32
	mjSubmitted atomic.Int32
	mjComplete  atomic.Bool
}

func (p *provider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/mj/submit/imagine" {
		p.mjSubmitted.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":1,"result":"refund-inflight-mj"}`)
		return
	}
	if r.URL.Path == "/mj/task/list-by-condition" {
		status, progress := "IN_PROGRESS", "10%"
		if p.mjComplete.Load() {
			status, progress = "SUCCESS", "100%"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"id":"refund-inflight-mj","status":%q,"progress":%q}]`, status, progress)
		return
	}
	if r.URL.Path != "/v1/chat/completions" {
		w.WriteHeader(404)
		return
	}
	body, err := io.ReadAll(r.Body)
	must(err)
	p.calls.Add(1)
	if strings.Contains(string(body), "unknown-cost") {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"unknown-fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
		return
	}
	if strings.Contains(string(body), "hold-inflight") {
		close(p.started)
		select {
		case <-p.release:
		case <-r.Context().Done():
			return
		}
	}
	if strings.Contains(string(body), `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"refund-fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
	} else {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"refund-fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
	}
}
func disableScenario(root identity, p *provider) (identity, []model.Token) {
	who := seedUser("refund_inflight", common.RoleCommonUser)
	credit(who.user.Id, "inflight-paid", "10", 1000)
	g := gift(who.user.Id, "inflight-gift", "2", 3600)
	tokens := []model.Token{apiKey(who.user.Id), apiKey(who.user.Id)}
	for i, t := range tokens {
		check(relay([]string{"18681", "18682"}[i], t, "warm-cache", false) == 200, "warm both SK and auth caches")
	}
	taskReply := call("18681", "POST", "/mj/submit/imagine", "sk-"+tokens[1].Key, map[string]any{"prompt": "test"}, nil, nil)
	check(taskReply.status == 200, "async task accepted before disable")
	var task model.Midjourney
	must(model.DB.Where("mj_id = ?", "refund-inflight-mj").First(&task).Error)
	check(task.MarketingFundingID > 0 && task.MarketingBillingPending, "async task keeps durable source hold")
	reply := make(chan int, 1)
	go func() { reply <- relay("18681", tokens[0], "hold-inflight", true) }()
	select {
	case <-p.started:
	case <-time.After(15 * time.Second):
		panic("provider did not receive inflight request")
	}
	denied(call("18681", "POST", "/api/user/manage", "", map[string]any{"id": who.user.Id, "action": "disable"}, nil, nil), "anonymous disable forbidden")
	noProof := call("18681", "POST", "/api/user/manage", root.token, map[string]any{"id": who.user.Id, "action": "disable"}, nil, nil)
	check(!noProof.Success, "disable requires transaction proof")
	manage("18682", root, who.user.Id, "disable")
	var stored model.User
	must(model.DB.First(&stored, who.user.Id).Error)
	must(model.DB.First(g, g.ID).Error)
	check(stored.Status == common.UserStatusDisabled && stored.Quota == 5000000 && g.RemainingQuota == 999000, "ordinary disable preserves money and gifts")
	before := p.calls.Load()
	for _, port := range []string{"18681", "18682"} {
		for _, token := range tokens {
			check(relay(port, token, "disabled-new", false) == 403, "every SK denied across gateways")
		}
	}
	check(p.calls.Load() == before, "disabled calls never reach provider")
	blocked := maintenanceRequest(root.user.Id, who.user.Id, "clear", "occupied-clear")
	blocked.ExactRefundCNY = "10"
	blocked.RefundedCNY = "10"
	rejected(cli(blocked, true), "clear cannot erase live SSE or async task")
	close(p.release)
	check(<-reply == 200, "inflight response completes")
	waitUntil("disabled inflight settles", func() bool {
		var count int64
		must(model.DB.Model(&model.MarketingFunding{}).Where("user_id = ? AND state = ?", who.user.Id, "settled").Count(&count).Error)
		return count == 3
	})
	must(model.DB.First(&stored, who.user.Id).Error)
	must(model.DB.First(g, g.ID).Error)
	check(stored.Status == common.UserStatusDisabled && stored.Quota == 5000000 && g.ConsumedQuota == 1500 && g.RemainingQuota == 998500, "disabled inflight settles original gift source")
	p.mjComplete.Store(true)
	waitUntil("disabled async task settles", func() bool {
		var current model.Midjourney
		return model.DB.First(&current, task.Id).Error == nil && current.Status == "SUCCESS" && !current.MarketingBillingPending
	})
	must(model.DB.First(&stored, who.user.Id).Error)
	must(model.DB.First(g, g.ID).Error)
	check(stored.Status == common.UserStatusDisabled && stored.Quota == 5000000 && g.ConsumedQuota == 3000 && g.RemainingQuota == 997000, "disabled async also settles original source")
	check(p.mjSubmitted.Load() == 1, "async never resubmitted")
	fmt.Println("REFUND_ASYNC_OK disabled=true other_gateway_polls=true source_settled=true no_resubmit=true")
	fmt.Println("REFUND_DISABLE_OK actual_admin_proof=true two_gateways=true two_keys=true warm_cache=true ordinary_disable_no_clear=true inflight_original_source=true")
	return who, tokens
}
func main() {
	check(strings.Contains(os.Getenv("SQL_DSN"), "/marketing_v2_refund_process?"), "isolated refund database")
	check(os.Getenv("REDIS_CONN_STRING") == "redis://redis:6379/8", "isolated Redis DB8")
	workerMode := len(os.Args) > 1 && os.Args[1] == "payment-worker"
	common.IsMasterNode = !workerMode
	common.CryptoSecret = "isolated-refund-process-secret"
	must(model.InitDB())
	must(model.InitLogDB())
	must(common.InitRedisClient())
	defer common.RDB.Close()
	if workerMode {
		paymentWorker()
		return
	}
	must(common.RDB.FlushDB(context.Background()).Err())
	var version string
	must(model.DB.Raw("SELECT version()").Scan(&version).Error)
	fmt.Printf("REFUND_DATABASE %s redis_db=8\n", version)
	root := seedUser("refundroot", common.RoleRootUser)
	for key, value := range map[string]string{"QuotaPerUnit": "500000", "USDExchangeRate": "1", "general_setting.quota_display_type": "CNY", "ModelPrice": `{"gpt-4o":0.001,"mj_imagine":0.003}`, "ModelRatio": `{"refund-unknown":1}`, "payment_setting.compliance_confirmed": "true", "payment_setting.compliance_terms_version": "v1"} {
		must(model.DB.Create(&model.Option{Key: key, Value: value}).Error)
	}
	p := &provider{started: make(chan struct{}), release: make(chan struct{})}
	upstream := httptest.NewServer(http.HandlerFunc(p.serve))
	defer upstream.Close()
	base := upstream.URL
	channel := model.Channel{Name: "refund-openai", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "isolated-upstream", BaseURL: &base, Models: "gpt-4o,refund-unknown", Group: "default"}
	must(channel.Insert())
	mjChannel := model.Channel{Name: "refund-mj", Type: constant.ChannelTypeMidjourney, Status: common.ChannelStatusEnabled, Key: "isolated-mj", BaseURL: &base, Models: "mj_imagine", Group: "default"}
	must(mjChannel.Insert())
	a := launch("18681", false)
	defer a.stop()
	b := launch("18682", true)
	defer func() { b.stop() }()
	login("18681", &root)
	legacyHTTPGuards(root)
	who, tokens := disableScenario(root, p)
	maintenanceScenarios(root, who, tokens, func() { b.stop(); b = launch("18682", false) })
}

func legacyHTTPGuards(root identity) {
	managed := seedUser("refund_guard", common.RoleCommonUser)
	credit(managed.user.Id, "guard-paid", "10", 1000)
	login("18681", &managed)
	before := managed.user
	must(model.DB.First(&before, before.Id).Error)
	plan := model.SubscriptionPlan{Title: "legacy active plan must not reopen purchases", Enabled: true, PriceAmount: 1, Currency: "USD", DurationUnit: "month", DurationValue: 1}
	must(model.DB.Create(&plan).Error)
	purchase := call("18682", "POST", "/api/subscription/balance/pay", managed.token, map[string]any{"plan_id": plan.Id}, nil, nil)
	check(purchase.status != 404 && purchase.status != 500 && !purchase.Success, "subscription balance purchase is unavailable in marketing deployment")
	var after model.User
	must(model.DB.First(&after, before.Id).Error)
	check(after.Quota == before.Quota, "subscription cannot debit paid money outside ledger")
	for _, mode := range []string{"add", "subtract", "override"} {
		result := call("18681", "POST", "/api/user/manage", root.token, map[string]any{"id": managed.user.Id, "action": "add_quota", "mode": mode, "value": 1}, nil, nil)
		check(result.status != 404 && result.status != 500 && !result.Success, "raw admin balance edit rejected "+mode)
	}
	must(model.DB.First(&after, before.Id).Error)
	check(after.Quota == before.Quota, "admin raw edits do not alter paid funds")
	aff := call("18681", "POST", "/api/user/aff_transfer", managed.token, map[string]any{"quota": 1}, nil, nil)
	check(aff.status != 404 && aff.status != 500 && !aff.Success, "legacy invitation cash transfer unavailable")
	fmt.Println("REFUND_LEGACY_HTTP_GUARDS_OK subscription_balance=false admin_raw_edits=false invitation_cash_transfer=false")
}

type cliProcess struct {
	tag    string
	cmd    *exec.Cmd
	input  io.WriteCloser
	output bytes.Buffer
	stderr bytes.Buffer
	cancel context.CancelFunc
}
type cliReply struct {
	Executed    bool            `json:"executed"`
	OperationID int64           `json:"operation_id"`
	ErrorText   string          `json:"-"`
	Success     bool            `json:"success"`
	Data        json.RawMessage `json:"data"`
	Result      json.RawMessage `json:"result"`
	Error       json.RawMessage `json:"error"`
	Exit        int             `json:"-"`
	Raw         []byte          `json:"-"`
}

func startCLI(execute, enabled bool, file ...string) *cliProcess {
	return startCLIWithRedis(execute, enabled, os.Getenv("REDIS_CONN_STRING"), file...)
}
func startCLIWithRedis(execute, enabled bool, redisURL string, file ...string) *cliProcess {
	inputPath := "-"
	if len(file) > 0 {
		inputPath = file[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	args := []string{"marketing-maintenance", "--input", inputPath}
	if execute {
		args = append(args, "--execute")
	}
	tag := "refund-cli-" + common.GetUUID()
	cmd := exec.CommandContext(ctx, "/fixtures/refund-gateway", args...)
	cmd.Env = append(os.Environ(), "NODE_TYPE=slave", "CRYPTO_SECRET=isolated-refund-process-secret", "SESSION_SECRET=isolated-refund-process-secret", fmt.Sprintf("MARKETING_MAINTENANCE_ENABLED=%t", enabled), "SQL_DSN="+os.Getenv("SQL_DSN")+"&application_name="+tag, "REDIS_CONN_STRING="+redisURL)
	p := &cliProcess{cmd: cmd, cancel: cancel, tag: tag}
	var err error
	p.input, err = cmd.StdinPipe()
	must(err)
	cmd.Stdout = &p.output
	cmd.Stderr = &p.stderr
	must(cmd.Start())
	return p
}
func (p *cliProcess) release(input any) {
	body, err := common.Marshal(input)
	must(err)
	_, err = p.input.Write(body)
	must(err)
	must(p.input.Close())
}
func (p *cliProcess) finish() cliReply {
	defer p.cancel()
	err := p.cmd.Wait()
	r := cliReply{Raw: append([]byte(nil), p.output.Bytes()...)}
	if err != nil {
		if p.cmd.ProcessState == nil {
			panic(err)
		}
		r.Exit = p.cmd.ProcessState.ExitCode()
	}
	r.ErrorText = credentialHex.ReplaceAllString(p.stderr.String(), "[redacted]")
	if r.Exit != 0 {
		check(len(r.Raw) == 0, "failed CLI must not emit successful result")
		return r
	}
	if err := common.Unmarshal(r.Raw, &r); err != nil {
		fmt.Print(credentialHex.ReplaceAllString(p.output.String(), "[redacted]"))
		fmt.Print(r.ErrorText)
		panic("maintenance stdout is not one JSON result")
	}
	r.Success = true
	return r
}
func cli(input any, execute bool) cliReply {
	p := startCLI(execute, execute)
	p.release(input)
	return p.finish()
}
func cliPair(a, b any) (cliReply, cliReply) {
	x, y := startCLI(true, true), startCLI(true, true)
	var done sync.WaitGroup
	done.Go(func() { x.release(a) })
	done.Go(func() { y.release(b) })
	done.Wait()
	return x.finish(), y.finish()
}

func maintenanceRequest(actorID, userID int, action, reference string) service.MarketingMaintenanceRequest {
	var user model.User
	must(model.DB.First(&user, userID).Error)
	var account model.MarketingAccount
	err := model.DB.First(&account, "user_id = ?", userID).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		must(err)
	}
	return service.MarketingMaintenanceRequest{Action: action, ActorID: actorID, UserID: userID, Reference: reference, Evidence: "isolated original payment/usage document " + reference, AuthorizationReference: "operator approval fixture " + reference, WriterIsolationEvidence: "fixture controls all gateways and provider barriers; legacy writers stopped", ConversionEvidence: "reviewed original CNY conversion 500000 units per CNY", Currency: "CNY", ExpectedBalance: decimal.NewFromInt(int64(user.Quota)).Div(decimal.NewFromInt(500000)).String(), ExpectedAffBalance: decimal.NewFromInt(int64(user.AffQuota)).Div(decimal.NewFromInt(500000)).String(), ExpectedRevision: account.Revision, PaidOpeningAmount: "0", GiftOpeningAmount: "0", AffGiftAmount: "0", AffDisposition: "none", PaymentHistory: "unknown"}
}
func acceptedCLI(r cliReply, label string) {
	if !r.Success || r.Exit != 0 {
		fmt.Print(r.ErrorText)
	}
	check(r.Success && r.Exit == 0, label)
}
func rejected(r cliReply, label string) { check(!r.Success && r.Exit != 0, label) }
func assertNoAssets(userID int) {
	var user model.User
	must(model.DB.First(&user, userID).Error)
	check(user.Quota == 0, "cleared paid balance stays zero")
	var gifts []model.MarketingGift
	must(model.DB.Where("user_id = ?", userID).Find(&gifts).Error)
	for _, g := range gifts {
		check(g.RemainingQuota == 0 && g.ReservedQuota == 0, "old gift not restored")
	}
	var chances []model.MarketingLotteryChance
	must(model.DB.Where("user_id = ?", userID).Find(&chances).Error)
	for _, c := range chances {
		check(c.Remaining == 0, "old chance not restored")
	}
	var progress []model.MarketingActivityProgress
	must(model.DB.Where("user_id = ?", userID).Find(&progress).Error)
	for _, p := range progress {
		check(p.RemainderCents == 0, "old remainder not restored")
	}
}
func maintenanceScenarios(root, who identity, tokens []model.Token, restart func()) {
	req := maintenanceRequest(root.user.Id, who.user.Id, "clear", "clear-inflight-completed")
	req.ExactRefundCNY = "10"
	req.RefundedCNY = "10"
	var before int64
	must(model.DB.Model(&model.MarketingMaintenanceOperation{}).Count(&before).Error)
	preview := cli(req, false)
	acceptedCLI(preview, "default preview succeeds")
	check(!preview.Executed && preview.OperationID == 0, "preview does not execute")
	var after int64
	must(model.DB.Model(&model.MarketingMaintenanceOperation{}).Count(&after).Error)
	check(after == before, "preview creates no operation")
	var user model.User
	must(model.DB.First(&user, who.user.Id).Error)
	check(user.Quota == 5000000, "preview does not clear paid balance")
	p := startCLI(true, false)
	p.release(req)
	rejected(p.finish(), "execution disabled by default")
	unprivileged := seedUser("refund_unprivileged", common.RoleCommonUser)
	bad := req
	bad.ActorID = unprivileged.user.Id
	rejected(cli(bad, true), "ordinary actor cannot clear")
	bad = req
	bad.AuthorizationReference = ""
	rejected(cli(bad, true), "authorization reference required")
	bad = req
	bad.WriterIsolationEvidence = ""
	rejected(cli(bad, true), "writer isolation required")
	bad = req
	bad.Currency = "USD"
	rejected(cli(bad, true), "input currency mismatch rejected")
	a, b := cliPair(req, req)
	acceptedCLI(a, "concurrent clear first")
	acceptedCLI(b, "concurrent clear replay")
	check(a.Executed && b.Executed && a.OperationID == b.OperationID && a.OperationID > 0, "one durable clear operation")
	assertNoAssets(who.user.Id)
	bad = req
	bad.Evidence = "different otherwise valid original payment evidence"
	rejected(cli(bad, true), "same operation different payload rejected")
	acceptedCLI(cli(req, true), "lost receipt replay returns prior result")
	restart()
	manage("18681", root, who.user.Id, "enable")
	assertNoAssets(who.user.Id)
	for i, token := range tokens {
		check(relay([]string{"18681", "18682"}[i], token, "cleared-enabled", false) == 403, "enable does not restore cleared funds")
	}
	var account model.MarketingAccount
	must(model.DB.First(&account, "user_id = ?", who.user.Id).Error)
	check(account.PaymentSequence == 1 && account.FirstPaymentID > 0, "clear preserves paid/first history")
	fmt.Println("REFUND_CLEAR_OK preview_readonly=true execution_opt_in=true authorization=true current_role=true currency_guard=true two_process_once=true replay=true enable_no_restore=true first_history_retained=true")
	roundingScenario(root)
	legacyScenario(root)
	rewardBoundaryScenario(root)
	managedReconcileScenario(root)
	maintenanceCacheScenario(root)
	cliFileScenario(root, who)
	zeroAndSubscriptionScenario(root)
	previousRefundScenario(root)
	sequenceBarrierScenario(root)
	fmt.Println("REFUND_ALL_OK actual_cli=true actual_gateways=true no_extra_middleware=true")
}
func roundingScenario(root identity) {
	who := seedUser("refund_rounding", common.RoleCommonUser)
	credit(who.user.Id, "rounding-paid", "1.809", 181)
	gift(who.user.Id, "rounding-free", "100", 0)
	req := maintenanceRequest(root.user.Id, who.user.Id, "clear", "rounding-clear")
	req.ExactRefundCNY = "1.809"
	req.RefundedCNY = "1.81"
	rejected(cli(req, true), "enabled account cannot clear")
	manage("18681", root, who.user.Id, "disable")
	acceptedCLI(cli(req, true), "rounded cash clear")
	var op model.MarketingMaintenanceOperation
	must(model.DB.Where("user_id = ? AND kind = ?", who.user.Id, "asset_clear").First(&op).Error)
	var result model.MarketingAssetClearResult
	must(common.UnmarshalJsonStr(op.Result, &result))
	check(result.ExactRefundCNY == "1.809" && result.RefundedCents == 181 && result.RoundingDifferenceCNY == "0.001" && result.GiftQuota == 50000000, "precise principal/refund/tail separated from nonrefundable gifts")
	assertNoAssets(who.user.Id)
	fmt.Println("REFUND_ROUNDING_OK exact=1.809 cash=1.81 tail=0.001 gift_cash=0")
}
func legacyScenario(root identity) {
	user := model.User{Username: "refund_legacy", AffCode: "refund_legacy", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1000000, UsedQuota: 300000, RequestCount: 3, AffQuota: 100000, AffHistoryQuota: 200000, Group: "default"}
	must(model.DB.Create(&user).Error)
	task := model.Task{UserId: user.Id, TaskID: "fixture-legacy-task", Status: model.TaskStatusInProgress, Quota: 100000, CreatedAt: common.GetTimestamp(), PrivateData: model.TaskPrivateData{BillingSource: "wallet"}}
	must(model.DB.Create(&task).Error)
	mj := model.Midjourney{UserId: user.Id, MjId: "fixture-legacy-mj", Status: "IN_PROGRESS", Progress: "10%", Quota: 150000}
	must(model.DB.Create(&mj).Error)
	terminal := model.Task{UserId: user.Id, TaskID: "fixture-legacy-terminal", Status: model.TaskStatusSuccess, Progress: "100%", Quota: 50000, PrivateData: model.TaskPrivateData{BillingSource: "wallet"}}
	must(model.DB.Create(&terminal).Error)
	inventory := maintenanceRequest(root.user.Id, user.Id, "inventory", "legacy-inventory")
	r := cli(inventory, false)
	acceptedCLI(r, "legacy inventory")
	check(strings.Contains(string(r.Raw), "unresolved_tasks") && strings.Contains(string(r.Raw), "midjourney"), "inventory includes old tasks")
	var count int64
	must(model.DB.Model(&model.MarketingAccount{}).Where("user_id = ?", user.Id).Count(&count).Error)
	check(count == 0, "preview does not classify legacy money")
	req := maintenanceRequest(root.user.Id, user.Id, "review-origin", "legacy-origin")
	req.PaidOpeningAmount = "1.5"
	req.GiftOpeningAmount = "0.5"
	req.AffDisposition = "gift"
	req.AffGiftAmount = "0.2"
	rejected(cli(req, true), "cannot classify while old task and MJ can still write")
	reconcile := maintenanceRequest(root.user.Id, user.Id, "reconcile", "legacy-task-final")
	reconcile.TaskKind = "task"
	reconcile.TaskID = int(task.ID)
	reconcile.ExpectedTaskAmount = "0.2"
	reconcile.ConfirmedNetDebitAmount = "0.2"
	reconcile.ConfirmedUserUsageAmount = "0.2"
	reconcile.Terminal = "SUCCESS"
	reconcile.TerminalEvidence = "provider terminal receipt"
	reconcile.SourceEvidence = "original wallet reservation document"
	rejected(cli(reconcile, true), "unknown final charge cannot be inferred from isolation")
	final := "0.1"
	reconcile.FinalAmount = &final
	a, b := cliPair(reconcile, reconcile)
	acceptedCLI(a, "legacy reconciliation")
	acceptedCLI(b, "legacy reconciliation replay")
	check(a.OperationID == b.OperationID, "legacy returned difference once")
	reconcile = maintenanceRequest(root.user.Id, user.Id, "reconcile", "legacy-mj-final")
	reconcile.TaskKind = "midjourney"
	reconcile.TaskID = mj.Id
	reconcile.ExpectedTaskAmount = "0.3"
	reconcile.ConfirmedNetDebitAmount = "0.3"
	reconcile.ConfirmedUserUsageAmount = "0.3"
	reconcile.Terminal = "FAILURE"
	reconcile.TerminalEvidence = "provider failure with trustworthy zero charge"
	reconcile.SourceEvidence = "original wallet reservation document"
	zero := "0"
	reconcile.FinalAmount = &zero
	acceptedCLI(cli(reconcile, true), "legacy MJ confirmed zero reconciliation")
	must(model.DB.First(&user, user.Id).Error)
	check(user.Quota == 1200000, "old task original-source differences restored once to unverified balance")
	req = maintenanceRequest(root.user.Id, user.Id, "review-origin", "legacy-origin-final")
	req.PaidOpeningAmount = "1.5"
	req.GiftOpeningAmount = "0.9"
	req.AffDisposition = "gift"
	req.AffGiftAmount = "0.2"
	rejected(cli(req, true), "legacy SUCCESS with unreviewed charge still blocks origin classification")
	reconcile = maintenanceRequest(root.user.Id, user.Id, "reconcile", "legacy-terminal-review")
	reconcile.TaskKind = "task"
	reconcile.TaskID = int(terminal.ID)
	reconcile.ExpectedTaskAmount = "0.1"
	reconcile.ConfirmedNetDebitAmount = "0.1"
	reconcile.ConfirmedUserUsageAmount = "0.1"
	reconcile.Terminal = "SUCCESS"
	reconcile.TerminalEvidence = "trusted terminal settlement retained"
	reconcile.SourceEvidence = "persisted original wallet charge"
	terminalAmount := "0.1"
	reconcile.FinalAmount = &terminalAmount
	acceptedCLI(cli(reconcile, true), "terminal legacy review without fabricating refund")
	duplicate := reconcile
	duplicate.Reference = "legacy-terminal-review-new-key"
	rejected(cli(duplicate, true), "new operation cannot re-reconcile already reviewed task")
	req = maintenanceRequest(root.user.Id, user.Id, "review-origin", "legacy-origin-final")
	req.PaidOpeningAmount = "1.5"
	req.GiftOpeningAmount = "0.9"
	req.AffDisposition = "gift"
	req.AffGiftAmount = "0.2"
	a, b = cliPair(req, req)
	acceptedCLI(a, "origin review")
	acceptedCLI(b, "origin replay")
	check(a.OperationID == b.OperationID, "origin classified once")
	must(model.DB.First(&user, user.Id).Error)
	check(user.Quota == 750000 && user.AffQuota == 0 && user.AffHistoryQuota == 200000, "classification separates gifted history without destroying history")
	var account model.MarketingAccount
	must(model.DB.First(&account, "user_id = ?", user.Id).Error)
	check(account.OriginState == "verified" && account.PaymentHistory == "unknown" && account.PaymentSequence == 0, "classification never fabricates cash or first payment")
	must(model.DB.Model(&model.MarketingPayment{}).Where("user_id = ?", user.Id).Count(&count).Error)
	check(count == 0, "legacy review creates no paid receipt")
	fmt.Println("REFUND_LEGACY_OK preview_no_classification=true task_and_mj_block=true unknown_final_rejected=true two_process_original_source_once=true paid_gift_aff_separate=true no_fake_receipt=true")
}

func createActivity(actorID int, spec model.MarketingActivitySpec) *model.MarketingActivity {
	var a *model.MarketingActivity
	must(model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		a, err = model.CreateMarketingActivity(tx, actorID, "refund-create-"+spec.Name, spec, spec.StartsAt-1)
		if err != nil {
			return err
		}
		a, err = model.ActivateMarketingActivity(tx, actorID, a.ID, "refund-enable-"+spec.Name, spec.StartsAt-1)
		return err
	}))
	return a
}
func processRewards(userID int) {
	r, err := service.ProcessMarketingActivityFacts(context.Background(), userID)
	must(err)
	check(!r.Pending, "reward recovery completed")
}
func rewardBoundaryScenario(root identity) {
	now := common.GetTimestamp()
	who := seedUser("refund_rewards", common.RoleCommonUser)
	friend := seedUser("refund_friend", common.RoleCommonUser)
	money := model.MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "500000", CurrencyPerUnit: "1", Currency: "CNY"}
	var pool *model.MarketingLotteryPool
	must(model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		pool, err = model.CreateMarketingLotteryPool(tx, root.user.Id, "refund-pool", model.MarketingLotteryPoolSpec{Name: "refund pool", Style: "wheel", StartsAt: now - 10, EndsAt: now + 3600, Capacity: 100, Prizes: []model.MarketingLotteryPrizeSpec{{Name: "combo", Weight: 1, Stock: -1, Gifts: []model.MarketingLotteryGiftSpec{{Money: money, ValidSeconds: 600}, {Money: money, ValidSeconds: 0}}}}}, now-11)
		return err
	}))
	fixed := model.MarketingActivityRewardSpec{Kind: "gift", Calculation: "fixed", Money: money, ValidSeconds: 600}
	single := createActivity(root.user.Id, model.MarketingActivitySpec{Name: "refund_single", Kind: "recharge", Condition: "single_payment", Frequency: "every", StartsAt: now - 10, EndsAt: now + 3600, ThresholdCents: 1, Rewards: []model.MarketingActivityRewardSpec{fixed, {Kind: "chance", PoolID: pool.ID, Count: 1, ChanceMode: "direct"}}})
	first := createActivity(root.user.Id, model.MarketingActivitySpec{Name: "refund_first", Kind: "recharge", Condition: "first_successful_payment", Frequency: "once", StartsAt: now - 10, EndsAt: now + 3600, ThresholdCents: 1, Rewards: []model.MarketingActivityRewardSpec{fixed}})
	cumulative := createActivity(root.user.Id, model.MarketingActivitySpec{Name: "refund_cumulative", Kind: "recharge", Condition: "cumulative_payments", Frequency: "every", StartsAt: now - 10, EndsAt: now + 3600, ThresholdCents: 200, Rewards: []model.MarketingActivityRewardSpec{fixed}})
	invite := createActivity(root.user.Id, model.MarketingActivitySpec{Name: "refund_invite", Kind: "invitation", Condition: "friend_ratio", Frequency: "every", StartsAt: now - 10, EndsAt: now + 3600, Rewards: []model.MarketingActivityRewardSpec{{Kind: "gift", Calculation: "cash_ratio", Ratio: "0.1", Money: money}}})
	must(model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", friend.user.Id).Updates(map[string]any{"inviter_id": who.user.Id, "created_at": now}).Error; err != nil {
			return err
		}
		_, err := model.RecordMarketingInvitation(tx, friend.user.Id, who.user.Id, "refund-friend", now)
		return err
	}))
	credit(who.user.Id, "refund-rewards-initial", "3", 300)
	processRewards(who.user.Id)
	var draw *model.MarketingLotteryDraw
	must(model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.IssueMarketingLotteryChances(tx, who.user.Id, pool.ID, "refund-extra-chances", 4, now)
		if err != nil {
			return err
		}
		draw, err = model.DrawMarketingLotteryChance(tx, who.user.Id, pool.ID, "refund-composite-draw", now)
		return err
	}))
	var awards []model.MarketingLotteryAward
	must(model.DB.Where("draw_id = ?", draw.ID).Order("id").Find(&awards).Error)
	check(len(awards) == 2, "composite lottery award created")
	must(model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		_, err := model.FulfillMarketingLotteryAward(tx, who.user.Id, awards[0].ID, now)
		return err
	}))
	credit(who.user.Id, "refund-own-delayed", "0.5", 50)
	credit(friend.user.Id, "refund-friend-delayed", "0.5", 50)
	manage("18681", root, who.user.Id, "disable")
	req := maintenanceRequest(root.user.Id, who.user.Id, "clear", "reward-boundary-clear")
	req.ExactRefundCNY = "3.5"
	req.RefundedCNY = "3.5"
	a, b := cliPair(req, req)
	acceptedCLI(a, "clear also closes old pending own/friend reward obligations")
	acceptedCLI(b, "concurrent clear with old qualifications replays")
	check(a.OperationID == b.OperationID, "reward clear happens once")
	assertNoAssets(who.user.Id)
	var p model.MarketingActivityProgress
	must(model.DB.Where("activity_id = ? AND user_id = ?", cumulative.ID, who.user.Id).First(&p).Error)
	check(p.ProcessedAwards == 1 && p.RemainderCents == 0, "cumulative old ordinal retained and remainder cleared")
	must(model.DB.First(pool, pool.ID).Error)
	issued := pool.Issued
	check(issued == 5, "clear does not recycle issued lottery supply")
	manage("18681", root, who.user.Id, "enable")
	processRewards(who.user.Id)
	processRewards(friend.user.Id)
	assertNoAssets(who.user.Id)
	closed, err := service.FulfillMarketingLotteryDraw(context.Background(), who.user.Id, draw.ID)
	must(err)
	check(closed.Draw.State == "cleared", "old pending composite result not fulfilled after clear")
	assertNoAssets(who.user.Id)
	credit(who.user.Id, "refund-own-new", "1", 100)
	processRewards(who.user.Id)
	credit(friend.user.Id, "refund-friend-new", "1", 100)
	processRewards(friend.user.Id)
	var newSingle, newFirst, newInvite int64
	must(model.DB.Model(&model.MarketingActivityReward{}).Where("user_id = ? AND activity_id = ? AND state = ? AND kind = ?", who.user.Id, single.ID, "credited", "gift").Count(&newSingle).Error)
	must(model.DB.Model(&model.MarketingActivityReward{}).Where("user_id = ? AND activity_id = ? AND state = ?", who.user.Id, first.ID, "credited").Count(&newFirst).Error)
	must(model.DB.Model(&model.MarketingActivityReward{}).Where("user_id = ? AND activity_id = ? AND state = ?", who.user.Id, invite.ID, "credited").Count(&newInvite).Error)
	check(newSingle == 2 && newFirst == 1 && newInvite == 1, "new rewards allowed while first eligibility and old counts persist")
	must(model.DB.Where("activity_id = ? AND user_id = ?", cumulative.ID, who.user.Id).First(&p).Error)
	check(p.ProcessedAwards == 1 && p.RemainderCents == 100, "new payment starts cumulative remainder from zero")
	var gifts []model.MarketingGift
	must(model.DB.Where("user_id = ?", who.user.Id).Find(&gifts).Error)
	var remaining int64
	for _, g := range gifts {
		remaining += g.RemainingQuota
	}
	check(remaining == 550000, "only new single and new inviter gifts available")
	var chances []model.MarketingLotteryChance
	must(model.DB.Where("user_id = ?", who.user.Id).Find(&chances).Error)
	var ready int64
	for _, c := range chances {
		ready += c.Remaining
	}
	check(ready == 1, "only new payment chance remains usable")
	acceptedCLI(cli(req, true), "old clear replay after new credits")
	var user model.User
	must(model.DB.First(&user, who.user.Id).Error)
	check(user.Quota == 500000, "old clear replay does not erase new payment")
	fmt.Println("REFUND_REWARD_BOUNDARY_OK old_own_and_friend_facts_closed=true partial_lottery_closed=true issuance_not_recycled=true reenable_no_old_awards=true new_payment_rewards=true first_preserved=true remainder_restarts=true old_clear_replay_preserves_new_money=true")
}

func managedReconcileScenario(root identity) {
	who := seedUser("refund_unknown", common.RoleCommonUser)
	credit(who.user.Id, "unknown-paid", "10", 1000)
	token := apiKey(who.user.Id)
	check(relay("18682", token, "unknown-cost", false, "refund-unknown") == 200, "actual call with unavailable final usage completes transport")
	var funding model.MarketingFunding
	waitUntil("unknown usage retained", func() bool {
		return model.DB.Where("user_id = ?", who.user.Id).First(&funding).Error == nil && funding.State == "review"
	})
	check(!funding.CostKnown && funding.ReservedQuota > 0, "unknown cost never assumed zero")
	manage("18681", root, who.user.Id, "disable")
	clear := maintenanceRequest(root.user.Id, who.user.Id, "clear", "unknown-clear")
	clear.ExactRefundCNY = "10"
	clear.RefundedCNY = "10"
	rejected(cli(clear, true), "unknown cost blocks clear even when disabled and isolated")
	manage("18681", root, who.user.Id, "enable")
	req := maintenanceRequest(root.user.Id, who.user.Id, "reconcile", "actual-unknown-reconciliation")
	req.FundingID = funding.ID
	req.Version = funding.Revision + 1
	check(req.Version == 2, "first unknown terminal requires next settlement version two")
	req.TerminalEvidence = "original fixture provider supplies corrected terminal receipt"
	req.SourceEvidence = "original source allocations for captured funding"
	rejected(cli(req, true), "explicit final amount required")
	value := "0.00001"
	req.FinalAmount = &value
	bad := req
	bad.SourceEvidence = ""
	rejected(cli(bad, true), "source evidence required")
	a, b := cliPair(req, req)
	acceptedCLI(a, "actual request reconciled while account enabled")
	acceptedCLI(b, "actual request concurrent replay")
	check(a.OperationID == b.OperationID, "reconcile applied once")
	must(model.DB.First(&funding, funding.ID).Error)
	check(funding.State == "settled" && funding.SettledQuota == 5 && funding.ReservedQuota == 0, "trusted corrected charge settles and releases hold")
	var user model.User
	must(model.DB.First(&user, who.user.Id).Error)
	check(user.Quota == 4999995 && user.Status == common.UserStatusEnabled, "reconcile deducts true original paid source without requiring disabled account")
	fmt.Println("REFUND_MANAGED_RECONCILE_OK actual_unknown_call=true unknown_blocks_clear=true explicit_final_and_source_required=true two_process_once=true enabled_reconcile_allowed=true exact_original_source=true")
}

func maintenanceCacheScenario(root identity) {
	who := seedUser("refund_cache", common.RoleCommonUser)
	credit(who.user.Id, "cache-paid", "10", 1000)
	token := apiKey(who.user.Id)
	check(relay("18682", token, "cache-unknown-cost", false, "refund-unknown") == 200, "cache fixture actual unknown-cost request")
	var funding model.MarketingFunding
	waitUntil("cache fixture unknown usage retained", func() bool {
		return model.DB.Where("user_id = ?", who.user.Id).First(&funding).Error == nil && funding.State == "review"
	})
	ctx := context.Background()
	cachedUser, err := model.GetUserCache(who.user.Id)
	must(err)
	cachedToken, err := model.GetTokenByKey(token.Key, false)
	must(err)
	check(cachedUser.Quota == 5000000 && cachedToken.RemainQuota == 10000000, "user and token caches hold pre-reconciliation balances")
	deletedToken := apiKey(who.user.Id)
	_, err = model.GetTokenByKey(deletedToken.Key, false)
	must(err)
	// Preserve a legitimate pre-deletion cache snapshot to simulate an old
	// writer whose deletion committed without invalidating the shared cache.
	must(model.DB.Delete(&deletedToken).Error)
	cacheKeys := []string{fmt.Sprintf("user:%d", who.user.Id), "token:" + common.GenerateHMAC(token.Key), "token:" + common.GenerateHMAC(deletedToken.Key)}
	count, err := common.RDB.Exists(ctx, cacheKeys...).Result()
	must(err)
	check(count == int64(len(cacheKeys)), "all three caches are actually warm before maintenance")
	req := maintenanceRequest(root.user.Id, who.user.Id, "reconcile", "cache-unknown-reconciliation")
	req.FundingID = funding.ID
	req.Version = funding.Revision + 1
	req.TerminalEvidence = "original provider corrected terminal cost"
	req.SourceEvidence = "original paid allocation and request identity"
	amount := "0.00001"
	req.FinalAmount = &amount
	var beforeOps int64
	must(model.DB.Model(&model.MarketingMaintenanceOperation{}).Count(&beforeOps).Error)
	// Port 1 in this isolated fixture container has no listener. Small client
	// timeouts and no retries test refusal without a wall-clock timeout test.
	for _, redisURL := range []string{"invalid://isolated-cache", "redis://127.0.0.1:1/8?dial_timeout=25ms&read_timeout=25ms&write_timeout=25ms&max_retries=-1"} {
		p := startCLIWithRedis(false, false, redisURL)
		p.release(req)
		preview := p.finish()
		acceptedCLI(preview, "preview does not depend on shared cache availability")
		check(!preview.Executed, "cache-unavailable preview remains read-only")
		p = startCLIWithRedis(true, true, redisURL)
		p.release(req)
		result := p.finish()
		rejected(result, "execute requires a valid reachable shared cache")
		if strings.HasPrefix(redisURL, "redis://") {
			check(strings.Contains(result.ErrorText, "Cannot reach the configured shared cache"), "closed Redis port reaches connection validation")
		} else {
			check(strings.Contains(result.ErrorText, "Invalid deployment cache configuration"), "malformed Redis configuration rejected")
		}
		var afterOps int64
		must(model.DB.Model(&model.MarketingMaintenanceOperation{}).Count(&afterOps).Error)
		check(afterOps == beforeOps, "unavailable cache writes no operation")
		var retained model.MarketingFunding
		must(model.DB.First(&retained, funding.ID).Error)
		check(retained.Revision == funding.Revision && retained.State == "review" && retained.SettledQuota == 0 && retained.ReservedQuota == funding.ReservedQuota, "unavailable cache preserves unresolved funding and hold")
		var retainedUser model.User
		must(model.DB.First(&retainedUser, who.user.Id).Error)
		var retainedToken model.Token
		must(model.DB.First(&retainedToken, token.Id).Error)
		check(retainedUser.Quota == 5000000 && retainedToken.RemainQuota == 10000000, "unavailable cache does not mutate principal or token quota")
	}
	acceptedCLI(cli(req, true), "reachable shared cache permits reconciliation")
	count, err = common.RDB.Exists(ctx, cacheKeys...).Result()
	must(err)
	check(count == 0, "CLI invalidates user and every token including soft-deleted token")
	cachedUser, err = model.GetUserCache(who.user.Id)
	must(err)
	cachedToken, err = model.GetTokenByKey(token.Key, false)
	must(err)
	check(cachedUser.Quota == 4999995 && cachedToken.RemainQuota == 9999995 && cachedToken.UsedQuota == 5, "fresh reads expose corrected principal and token balances")
	_, err = model.GetTokenByKey(deletedToken.Key, false)
	check(err == gorm.ErrRecordNotFound, "soft-deleted token cannot be recovered from stale shared cache")
	fmt.Println("REFUND_MAINTENANCE_CACHE_OK real_redis=true warm_user_and_token=true soft_deleted_token_invalidated=true corrected_balances_reloaded=true invalid_config_and_connection_refusal_block_execute=true no_operation_or_financial_mutation=true preview_remains_readonly=true")
}

func cliFileScenario(root, who identity) {
	req := maintenanceRequest(root.user.Id, who.user.Id, "inventory", "file-inventory")
	body, err := common.Marshal(req)
	must(err)
	directory, err := os.MkdirTemp("", "refund-cli-private-")
	must(err)
	defer os.RemoveAll(directory)
	filename := directory + "/input.json"
	must(os.WriteFile(filename, body, 0600))
	p := startCLI(false, false, filename)
	must(p.input.Close())
	acceptedCLI(p.finish(), "private owned JSON file accepted")
	must(os.Chmod(filename, 0644))
	p = startCLI(false, false, filename)
	must(p.input.Close())
	rejected(p.finish(), "group-readable input rejected")
	must(os.Chmod(filename, 0600))
	link := directory + "/link.json"
	must(os.Symlink(filename, link))
	p = startCLI(false, false, link)
	must(p.input.Close())
	rejected(p.finish(), "symlink input rejected")
	fmt.Println("REFUND_CLI_FILE_OK private_owned=true group_readable_rejected=true symlink_rejected=true no_secrets_in_argv=true")
}

func zeroAndSubscriptionScenario(root identity) {
	who := seedUser("refund_gift_only", common.RoleCommonUser)
	gift(who.user.Id, "gift-only", "2", 0)
	manage("18681", root, who.user.Id, "disable")
	req := maintenanceRequest(root.user.Id, who.user.Id, "clear", "gift-only-clear")
	req.ExactRefundCNY = "0"
	req.RefundedCNY = "0.00"
	acceptedCLI(cli(req, true), "zero cash gift-only clear")
	assertNoAssets(who.user.Id)
	legacy := model.User{Username: "refund_all_paid", AffCode: "refund_all_paid", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 500000}
	must(model.DB.Create(&legacy).Error)
	req = maintenanceRequest(root.user.Id, legacy.Id, "review-origin", "all-paid-origin")
	req.PaidOpeningAmount = "1"
	req.PaymentHistory = "paid"
	acceptedCLI(cli(req, true), "origin with explicitly zero gift and invitation fields")
	var account model.MarketingAccount
	must(model.DB.First(&account, "user_id = ?", legacy.Id).Error)
	check(account.OriginState == "verified" && account.PaymentHistory == "paid" && account.FirstPaymentID == 0, "verified previous cash does not fabricate first receipt")
	subUser := model.User{Username: "refund_legacy_sub", AffCode: "refund_legacy_sub", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1000000, UsedQuota: 100000}
	must(model.DB.Create(&subUser).Error)
	subscription := model.UserSubscription{UserId: subUser.Id, PlanId: 1, AmountTotal: 1000000, AmountUsed: 100000, Status: "active", Source: "admin"}
	must(model.DB.Create(&subscription).Error)
	task := model.Task{UserId: subUser.Id, TaskID: "legacy-subscription-unreviewed", Status: model.TaskStatusSuccess, Quota: 100000, PrivateData: model.TaskPrivateData{BillingSource: "subscription", SubscriptionId: subscription.Id}}
	must(model.DB.Create(&task).Error)
	req = maintenanceRequest(root.user.Id, subUser.Id, "reconcile", "legacy-subscription-must-not-credit-cash")
	req.TaskKind = "task"
	req.TaskID = int(task.ID)
	req.ExpectedTaskAmount = "0.2"
	req.ConfirmedNetDebitAmount = "0.2"
	req.ConfirmedUserUsageAmount = "0.2"
	req.Terminal = "SUCCESS"
	req.TerminalEvidence = "terminal source check"
	req.SourceEvidence = "subscription reservation, not paid wallet"
	final := "0.1"
	req.FinalAmount = &final
	rejected(cli(req, true), "unsupported historical subscription source cannot become wallet cash")
	must(model.DB.First(&subUser, subUser.Id).Error)
	must(model.DB.First(&subscription, subscription.Id).Error)
	check(subUser.Quota == 1000000 && subscription.AmountUsed == 100000, "subscription rejection preserves original assets")
	fmt.Println("REFUND_ZERO_AND_SUBSCRIPTION_OK zero_cash_clear=true explicit_zero_origin=true legacy_subscription_no_fake_cash=true")
}

func previousRefundScenario(root identity) {
	user := model.User{Username: "refund_old_returned", AffCode: "refund_old_returned", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1000000, UsedQuota: 200000}
	must(model.DB.Create(&user).Error)
	task := model.Task{UserId: user.Id, TaskID: "legacy-refund-completed-marker-lost", Status: model.TaskStatusFailure, Progress: "100%", Quota: 100000, PrivateData: model.TaskPrivateData{BillingSource: "wallet"}}
	must(model.DB.Create(&task).Error)
	req := maintenanceRequest(root.user.Id, user.Id, "reconcile", "legacy-already-returned")
	req.TaskKind = "task"
	req.TaskID = int(task.ID)
	req.ExpectedTaskAmount = "0.2"
	req.Terminal = "FAILURE"
	req.TerminalEvidence = "provider failed with confirmed zero charge"
	req.SourceEvidence = "original refund ledger confirms full return; task marker write was lost"
	zero := "0"
	req.FinalAmount = &zero
	req.ConfirmedUserUsageAmount = "0"
	rejected(cli(req, true), "persisted task quota alone is not proof of remaining debit")
	req.ConfirmedNetDebitAmount = "0"
	a, b := cliPair(req, req)
	acceptedCLI(a, "previously returned legacy charge")
	acceptedCLI(b, "previously returned replay")
	check(a.OperationID == b.OperationID, "already-refunded review once")
	must(model.DB.First(&user, user.Id).Error)
	must(model.DB.First(&task, task.ID).Error)
	check(user.Quota == 1000000 && user.UsedQuota == 200000 && task.MarketingLegacyReviewed, "confirmed net zero never returns the old quota again")
	fmt.Println("REFUND_PREVIOUS_RETURN_OK raw_task_quota_not_evidence=true confirmed_net_zero=true no_duplicate_wallet_credit=true two_process_once=true")
}

type paymentBarrierInput struct {
	UserID           int
	Reference, Phase string
}
type paymentBarrierProcess struct {
	cmd        *exec.Cmd
	input      io.WriteCloser
	output     *bufio.Scanner
	cancel     context.CancelFunc
	backendPID int
}

func paymentWorker() {
	check(common.MainDatabaseType() == common.DatabaseTypePostgreSQL, "barrier fixture requires isolated PostgreSQL")
	var input paymentBarrierInput
	must(common.UnmarshalJsonStr(os.Args[2], &input))
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var id int
		// This standalone PG-only fixture controls a transaction boundary. Product
		// locking remains in the real CreditMarketingPayment implementation.
		if err := tx.Raw("SELECT id FROM users WHERE id = ? FOR UPDATE", input.UserID).Scan(&id).Error; err != nil {
			return err
		}
		payment := model.MarketingPaymentInput{UserID: input.UserID, Kind: "online", Reference: input.Reference, PaidCents: 100, PostedAt: common.GetTimestamp(), Money: model.MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "500000", CurrencyPerUnit: "1", Currency: "CNY"}}
		if input.Phase == "after_sequence" {
			if _, err := model.CreditMarketingPayment(tx, payment); err != nil {
				return err
			}
		}
		var backend int
		if err := tx.Raw("SELECT pg_backend_pid()").Scan(&backend).Error; err != nil {
			return err
		}
		fmt.Printf("PAYMENT_BARRIER_READY %d\n", backend)
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return fmt.Errorf("missing process barrier release")
		}
		if input.Phase == "before_sequence" {
			if _, err := model.CreditMarketingPayment(tx, payment); err != nil {
				return err
			}
		}
		return nil
	}))
	fmt.Println("PAYMENT_BARRIER_COMMITTED")
}
func startPaymentBarrier(input paymentBarrierInput) *paymentBarrierProcess {
	body, err := common.Marshal(input)
	must(err)
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "payment-worker", string(body))
	in, err := cmd.StdinPipe()
	must(err)
	out, err := cmd.StdoutPipe()
	must(err)
	cmd.Stderr = os.Stderr
	must(cmd.Start())
	p := &paymentBarrierProcess{cmd: cmd, input: in, output: bufio.NewScanner(out), cancel: cancel}
	for p.output.Scan() {
		line := p.output.Text()
		if _, err := fmt.Sscanf(line, "PAYMENT_BARRIER_READY %d", &p.backendPID); err == nil {
			fmt.Printf("REFUND_PAYMENT_WORKER_READY phase=%s pid=%d backend=%d\n", input.Phase, cmd.Process.Pid, p.backendPID)
			return p
		}
	}
	panic("payment barrier worker did not become ready")
}
func (p *paymentBarrierProcess) release() {
	_, err := io.WriteString(p.input, "go\n")
	must(err)
	must(p.input.Close())
}
func (p *paymentBarrierProcess) finish() {
	defer p.cancel()
	committed := false
	for p.output.Scan() {
		if p.output.Text() == "PAYMENT_BARRIER_COMMITTED" {
			committed = true
		}
	}
	must(p.output.Err())
	must(p.cmd.Wait())
	check(committed, "real payment process committed")
}
func sequenceBarrierScenario(root identity) {
	for _, phase := range []string{"before_sequence", "after_sequence"} {
		who := seedUser("refund_seq_"+phase, common.RoleCommonUser)
		friend := seedUser("refund_friend_"+phase, common.RoleCommonUser)
		gift(who.user.Id, "seq-gift-"+phase, "1", 0)
		must(model.WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
			at := common.GetTimestamp()
			if err := tx.Model(&model.User{}).Where("id = ?", friend.user.Id).Updates(map[string]any{"inviter_id": who.user.Id, "created_at": at}).Error; err != nil {
				return err
			}
			_, err := model.RecordMarketingInvitation(tx, friend.user.Id, who.user.Id, "seq-invite-"+phase, at)
			return err
		}))
		manage("18681", root, who.user.Id, "disable")
		req := maintenanceRequest(root.user.Id, who.user.Id, "clear", "seq-clear-"+phase)
		req.ExactRefundCNY = "0"
		req.RefundedCNY = "0"
		payment := startPaymentBarrier(paymentBarrierInput{UserID: friend.user.Id, Reference: "seq-payment-" + phase, Phase: phase})
		clearer := startCLI(true, true)
		clearer.release(req)
		if phase == "before_sequence" {
			acceptedCLI(clearer.finish(), "clear commits while friend holds user lock before sequence")
			payment.release()
			payment.finish()
		} else {
			waitUntil("real clear waits for uncommitted payment sequence", func() bool {
				var count int64
				must(model.DB.Raw("SELECT count(*) FROM pg_stat_activity WHERE application_name = ? AND wait_event_type = 'Lock'", clearer.tag).Scan(&count).Error)
				return count == 1
			})
			payment.release()
			payment.finish()
			blocked := clearer.finish()
			rejected(blocked, "clear detects newly committed captured fact and requests preparation")
			check(strings.Contains(blocked.ErrorText, "preparation"), "sequence conflict reports retryable qualification preparation")
			acceptedCLI(cli(req, true), "same clear resumes newly committed old qualification")
		}
		var account model.MarketingAccount
		must(model.DB.First(&account, "user_id = ?", who.user.Id).Error)
		var fact model.MarketingActivityFact
		must(model.DB.Where("user_id = ? AND kind = ?", friend.user.Id, "payment").First(&fact).Error)
		if phase == "before_sequence" {
			check(fact.Sequence > account.ClearFactSequence, "post-clear committed fact is newer than cutoff")
		} else {
			check(fact.Sequence < account.ClearFactSequence, "pre-clear committed fact is included by cutoff")
		}
		manage("18681", root, who.user.Id, "enable")
		processRewards(friend.user.Id)
		var gifts []model.MarketingGift
		must(model.DB.Where("user_id = ?", who.user.Id).Find(&gifts).Error)
		var remaining int64
		for _, g := range gifts {
			remaining += g.RemainingQuota
		}
		expected := int64(0)
		if phase == "before_sequence" {
			expected = 50000
		}
		check(remaining == expected, "only genuinely post-clear invitation entitlement remains usable")
		fmt.Printf("REFUND_SEQUENCE_BARRIER_OK phase=%s real_payment_process=true actual_clear_cli=true fact=%d cutoff=%d gift_remaining=%d\n", phase, fact.Sequence, account.ClearFactSequence, remaining)
	}
}
