// Real HTTP registration and invitation awards across two gateways.
// Only administrator bootstrap and activity/provider configuration are fixtures.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"gorm.io/gorm"
)

const trustedOrigin = "https://registration-fixture.invalid"

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
	cmd := exec.Command("/fixtures/registration-gateway", "--port", port, "--log-dir", "/tmp/mv2-registration-"+port)
	cmd.Env = append(os.Environ(), "NODE_TYPE=master", "PORT="+port, "UPDATE_TASK=false", "SESSION_SECRET=isolated-registration-process-secret", "CRYPTO_SECRET=isolated-registration-process-secret", "SESSION_COOKIE_SECURE=true", "SESSION_COOKIE_TRUSTED_URL="+trustedOrigin, "PASSWORD_LOGIN_ENCRYPTION_ENABLED=false", "BATCH_UPDATE_ENABLED=true", "MEMORY_CACHE_ENABLED=true", "GIN_MODE=release", "CRITICAL_RATE_LIMIT=200")
	log, err := os.Create("/fixtures/registration-gateway-" + port + "-" + os.Getenv("REGISTRATION_FIXTURE_STAMP") + ".log")
	must(err)
	safeLog := &safeGatewayLog{file: log}
	cmd.Stdout, cmd.Stderr = safeLog, safeLog
	must(cmd.Start())
	fmt.Printf("REGISTRATION_GATEWAY_STARTED port=%s pid=%d\n", port, cmd.Process.Pid)
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

func createOnline(port string, who identity, amount int64) model.TopUp {
	r := call(port, "POST", "/api/user/pay", who.token, map[string]any{"amount": amount, "payment_method": "alipay"}, nil, nil)
	check(r.status == 200 && r.Message == "success", "actual Epay checkout creation")
	var params map[string]string
	must(common.Unmarshal(r.Data, &params))
	check(params["out_trade_no"] != "" && params["sign"] != "", "SDK generated signed checkout fields")
	var order model.TopUp
	must(model.DB.Where("trade_no = ?", params["out_trade_no"]).First(&order).Error)
	check(order.UserId == who.user.Id && order.PaymentProvider == model.PaymentProviderEpay && order.Status == common.TopUpStatusPending, "checkout persisted matching pending order")
	return order
}
func signedReceipt(order model.TopUp, key string) map[string]string {
	params, err := epay.GenerateParamsWithError(map[string]string{"pid": "fixture-merchant", "money": "12.00", "trade_no": "provider-" + order.TradeNo, "out_trade_no": order.TradeNo, "type": "alipay", "trade_status": "TRADE_SUCCESS"}, key)
	must(err)
	return params
}
func notify(port, method string, params map[string]string) string {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	endpoint := "http://127.0.0.1:" + port + "/api/user/epay/notify"
	body := ""
	if method == "GET" {
		endpoint += "?" + values.Encode()
	} else {
		body = values.Encode()
	}
	req, err := http.NewRequest(method, endpoint, strings.NewReader(body))
	check(err == nil, "callback request construction")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 20 * time.Second}
	r, err := client.Do(req)
	check(err == nil, "callback transport")
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	must(err)
	check(r.StatusCode == 200, "callback HTTP status")
	return string(data)
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

var cardMoneyRevision string

func codeBody(name, paid, credit string) map[string]any {
	return map[string]any{"name": name, "count": 1, "paid_amount_cny": paid, "credit_amount": credit, "credit_currency": "CNY", "money_revision": cardMoneyRevision, "sales_channel": "registered-card-shop", "sales_batch": "http-fixture-batch", "sales_evidence": "fixture-receipt", "expired_time": 0}
}
func createCode(port string, admin identity, body map[string]any) model.Redemption {
	r := call(port, "POST", "/api/redemption/", admin.token, body, nil, nil)
	success(r, "create paid code")
	var keys []string
	must(common.Unmarshal(r.Data, &keys))
	check(len(keys) == 1, "one code returned")
	var code model.Redemption
	must(model.DB.Where("key = ?", keys[0]).First(&code).Error)
	return code
}
func giftCount(user int, activity int64) int64 {
	var n int64
	must(model.DB.Model(&model.MarketingActivityReward{}).Where("user_id = ? AND activity_id = ? AND state = ?", user, activity, "credited").Count(&n).Error)
	return n
}

func register(port, name, inviter string) identity {
	who := identity{password: common.GetUUID() + "aA9!"}
	r := call(port, "POST", "/api/user/register", "", map[string]any{"username": name, "password": who.password, "aff_code": inviter, "quota": 99999999, "aff_quota": 99999999, "inviter_id": 1, "role": 100, "status": 1}, nil, nil)
	success(r, "real password registration "+name)
	must(model.DB.Where("username = ?", name).First(&who.user).Error)
	check(who.user.Role == common.RoleCommonUser && who.user.Quota == 0 && who.user.AffQuota == 0, "registration fields cannot forge principal or role")
	var account model.MarketingAccount
	must(model.DB.First(&account, "user_id = ?", who.user.Id).Error)
	check(account.OriginState == "verified" && account.PaymentHistory == model.MarketingPaymentHistoryNone, "registration creates trusted unpaid account")
	var facts []model.MarketingActivityFact
	must(model.DB.Where("user_id = ? AND kind = ?", who.user.Id, "registration").Find(&facts).Error)
	check(len(facts) == 1, "registration creates exactly one fact")
	login(port, &who)
	return who
}
func setOption(root identity, key, value string) {
	for _, port := range []string{"18481", "18482"} {
		success(call(port, "PUT", "/api/option/", root.token, map[string]any{"key": key, "value": value}, nil, nil), "root option update "+key)
	}
}
func snapshotCounts() []int64 {
	var result []int64
	for _, record := range []any{&model.User{}, &model.MarketingAccount{}, &model.MarketingInvitation{}, &model.MarketingActivityFact{}, &model.MarketingActivityReward{}, &model.MarketingGift{}, &model.MarketingPayment{}} {
		var n int64
		must(model.DB.Model(record).Count(&n).Error)
		result = append(result, n)
	}
	return result
}
func unchanged(before []int64, label string) {
	after := snapshotCounts()
	check(fmt.Sprint(before) == fmt.Sprint(after), label)
}
func activity(admin int, kind, condition, frequency, amount string, start int64, threshold int64) int64 {
	reward := model.MarketingActivityRewardSpec{Kind: "gift", Calculation: "fixed", ValidSeconds: 3600, Money: model.MarketingMoneySnapshot{Amount: amount, QuotaPerUnit: "50000", CurrencyPerUnit: "2", Currency: "CNY"}}
	if condition == "friend_ratio" {
		reward.Calculation = "cash_ratio"
		reward.Ratio = "0.05"
		reward.Money.Amount = ""
	}
	name := kind + "-" + condition + "-" + frequency
	spec := model.MarketingActivitySpec{Name: name, Kind: kind, Condition: condition, Frequency: frequency, StartsAt: start, EndsAt: start + 1800, ThresholdCents: threshold, Rewards: []model.MarketingActivityRewardSpec{reward}}
	var a *model.MarketingActivity
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = model.CreateMarketingActivity(tx, admin, "create-"+name, spec, start-1)
		if err != nil {
			return err
		}
		a, err = model.ActivateMarketingActivity(tx, admin, a.ID, "activate-"+name, start-1)
		return err
	}))
	return a.ID
}
func giftQuota(user int, activity int64) int64 {
	var gifts []model.MarketingGift
	must(model.DB.Joins("JOIN marketing_v2_activity_rewards ON marketing_v2_activity_rewards.gift_id = marketing_v2_gifts.id").Where("marketing_v2_activity_rewards.user_id = ? AND activity_id = ?", user, activity).Find(&gifts).Error)
	var total int64
	for _, g := range gifts {
		total += g.GrantedQuota
	}
	return total
}
func trace(user int) {
	var rewards []model.MarketingActivityReward
	must(model.DB.Where("user_id = ? AND state = ?", user, "credited").Find(&rewards).Error)
	check(len(rewards) > 0, "trace has rewards")
	for _, r := range rewards {
		var f model.MarketingActivityFact
		var d model.MarketingActivityDecision
		var g model.MarketingGift
		must(model.DB.First(&f, r.FactID).Error)
		must(model.DB.First(&d, r.DecisionID).Error)
		must(model.DB.First(&g, r.GiftID).Error)
		check(d.FactID == f.ID && d.UserID == r.UserID && g.UserID == r.UserID && g.SourceReference != "", "reward source and beneficiary match")
		var n int64
		must(model.DB.Model(&model.MarketingLedgerEntry{}).Where("user_id = ? AND gift_id = ?", r.UserID, g.ID).Count(&n).Error)
		check(n == 1, "gift has unique ledger source")
		if f.Kind == "payment" {
			var p model.MarketingPayment
			must(model.DB.First(&p, f.PaymentID).Error)
			check(p.UserID == f.UserID && p.PaidCents > 0, "invitation fact links actual paying friend")
		}
	}
}

// These independent processes invoke the same production recovery entry used by
// HTTP completion. No registration fact is created by the workers.
func work() {
	id, err := strconv.Atoi(os.Args[2])
	must(err)
	fmt.Printf("READY %d\n", os.Getpid())
	scanner := bufio.NewScanner(os.Stdin)
	check(scanner.Scan(), "worker barrier")
	result, err := service.ProcessMarketingActivityFacts(context.Background(), id)
	must(err)
	check(result != nil && !result.Pending, "worker recovered all obligations")
	if len(os.Args) > 3 && os.Args[3] == "lose" {
		os.Exit(73)
	}
	fmt.Println("RECOVERED")
}

type worker struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Scanner
	cancel context.CancelFunc
}

func startWorker(id int, lose bool) *worker {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	args := []string{"worker", strconv.Itoa(id)}
	if lose {
		args = append(args, "lose")
	}
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	in, err := cmd.StdinPipe()
	must(err)
	out, err := cmd.StdoutPipe()
	must(err)
	cmd.Stderr = os.Stderr
	must(cmd.Start())
	w := &worker{cmd: cmd, in: in, out: bufio.NewScanner(out), cancel: cancel}
	w.out.Buffer(make([]byte, 4096), 1024*1024)
	for w.out.Scan() {
		if strings.HasPrefix(w.out.Text(), "READY ") {
			fmt.Printf("REGISTRATION_WORKER_READY pid=%d\n", cmd.Process.Pid)
			return w
		}
	}
	panic("worker not ready")
}
func (w *worker) release() { _, err := io.WriteString(w.in, "go\n"); must(err) }
func (w *worker) finish(lose bool) {
	defer w.cancel()
	defer w.in.Close()
	ok := false
	for w.out.Scan() {
		if w.out.Text() == "RECOVERED" {
			ok = true
		}
	}
	err := w.cmd.Wait()
	if lose {
		var e *exec.ExitError
		check(errors.As(err, &e) && e.ExitCode() == 73, "lost response process exit after commit")
		return
	}
	must(err)
	check(ok, "worker completion")
}

func oauthState(aff string) string {
	r := call("18481", "POST", "/api/oauth/state", "", map[string]any{"provider": "fixture", "intent": "login", "aff": aff}, nil, nil)
	success(r, "real OAuth state")
	var data struct {
		FlowToken string `json:"flow_token"`
	}
	must(common.Unmarshal(r.Data, &data))
	check(data.FlowToken != "", "flow token issued")
	return data.FlowToken
}
func oauthCallback(port, state, code string) response {
	return call(port, "GET", "/api/oauth/fixture?"+url.Values{"state": {state}, "code": {code}}.Encode(), "", nil, nil, nil)
}

func main() {
	check(strings.Contains(os.Getenv("SQL_DSN"), "/marketing_v2_registration_process?"), "isolated registration database")
	check(os.Getenv("REDIS_CONN_STRING") == "redis://redis:6379/10", "isolated Redis DB10")
	workerMode := len(os.Args) > 1 && os.Args[1] == "worker"
	common.CryptoSecret = "isolated-registration-process-secret"
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
	fmt.Printf("REGISTRATION_DATABASE %s redis_db=10\n", version)
	root := identity{password: common.GetUUID() + "aA9!"}
	hash, err := common.Password2Hash(root.password)
	must(err)
	root.user = model.User{Username: "registrationroot", Password: hash, AffCode: "bootstrap", Status: common.UserStatusEnabled, Group: "default", Role: common.RoleRootUser, AuthVersion: 1}
	must(model.DB.Create(&root.user).Error)
	epayKey := common.GetUUID()
	settings := map[string]string{"RegisterEnabled": "true", "PasswordRegisterEnabled": "true", "EmailVerificationEnabled": "false", "QuotaForNewUser": "9999999", "QuotaForInvitee": "8888888", "QuotaForInviter": "7777777", "QuotaPerUnit": "50000", "USDExchangeRate": "2", "general_setting.quota_display_type": "CNY", "payment_setting.compliance_confirmed": "true", "payment_setting.compliance_terms_version": "v1", "PayAddress": "https://epay-fixture.invalid", "EpayId": "fixture-merchant", "EpayKey": epayKey, "Price": "1", "MinTopUp": "1", "CustomCallbackAddress": trustedOrigin, "PayMethods": `[{"name":"Alipay","type":"alipay"}]`}
	for key, value := range settings {
		must(model.DB.Create(&model.Option{Key: key, Value: value}).Error)
	}
	// Only the external provider identity endpoints are mocked. The actual OAuth
	// state, callback, account transaction and marketing integrations run in gateway.
	providerToken := common.GetUUID()
	var identityMu sync.Mutex
	identityArrivals := 0
	identityBarrier := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch r.URL.Path {
		case "/api/wechat/user":
			code := r.URL.Query().Get("code")
			if r.Header.Get("Authorization") != "isolated-wechat-secret" {
				result = map[string]any{"success": false, "message": "invalid verification"}
			} else if code == "wechat-good" {
				result = map[string]any{"success": true, "data": "wechat-subject-1"}
			} else if code == "wechat-race-a" || code == "wechat-race-b" {
				identityMu.Lock()
				identityArrivals++
				if identityArrivals == 2 {
					close(identityBarrier)
				}
				identityMu.Unlock()
				select {
				case <-identityBarrier:
				case <-r.Context().Done():
					return
				}
				result = map[string]any{"success": true, "data": "wechat-subject-race"}
			} else {
				result = map[string]any{"success": false, "message": "invalid verification"}
			}
		case "/token":
			must(r.ParseForm())
			if r.Form.Get("code") != "good" || r.Form.Get("client_secret") != "isolated-provider-secret" {
				w.WriteHeader(401)
				result = map[string]string{"error": "invalid_grant"}
			} else {
				result = map[string]any{"access_token": providerToken, "token_type": "Bearer", "expires_in": 600}
			}
		case "/user":
			if r.Header.Get("Authorization") != "Bearer "+providerToken {
				w.WriteHeader(401)
				result = map[string]string{"error": "invalid_token"}
			} else {
				result = map[string]string{"sub": "fixture-subject-1", "preferred_username": "oauthfriend", "name": "OAuth friend", "email": "oauthfriend@example.invalid"}
			}
		default:
			w.WriteHeader(404)
			result = map[string]string{"error": "not_found"}
		}
		data, err := common.Marshal(result)
		must(err)
		_, _ = w.Write(data)
	}))
	defer provider.Close()
	must(model.DB.Create(&model.CustomOAuthProvider{Name: "Fixture", Slug: "fixture", Enabled: true, ClientId: "fixture-client", ClientSecret: "isolated-provider-secret", AuthorizationEndpoint: provider.URL + "/authorize", TokenEndpoint: provider.URL + "/token", UserInfoEndpoint: provider.URL + "/user", AuthStyle: 1}).Error)
	for key, value := range map[string]string{"WeChatAuthEnabled": "true", "WeChatServerAddress": provider.URL, "WeChatServerToken": "isolated-wechat-secret"} {
		must(model.DB.Create(&model.Option{Key: key, Value: value}).Error)
	}
	g1 := launch("18481")
	defer g1.stop()
	g2 := launch("18482")
	defer g2.stop()
	login("18481", &root)
	metadata := call("18481", "GET", "/api/marketing/admin/metadata", root.token, nil, nil, nil)
	success(metadata, "current money context")
	var contextFields map[string]string
	must(common.Unmarshal(metadata.Data, &contextFields))
	cardMoneyRevision = contextFields["money_revision"]
	check(cardMoneyRevision != "", "money revision required by paid-card form")
	inviter := register("18481", "realinviter", "")
	other := register("18482", "otherinviter", "")
	old := register("18481", "oldfriend", inviter.user.AffCode)
	var oldFact model.MarketingActivityFact
	must(model.DB.Where("user_id = ? AND kind = ?", old.user.Id, "registration").First(&oldFact).Error)
	for common.GetTimestamp() <= oldFact.OccurredAt {
		time.Sleep(20 * time.Millisecond)
	}
	start := common.GetTimestamp()
	own := activity(root.user.Id, "registration", "registration", "once", "1", start, 0)
	invited := activity(root.user.Id, "invitation", "friend_registration", "once", "2", start, 0)
	once := activity(root.user.Id, "invitation", "friend_cumulative", "once", "3", start, 2000)
	every := activity(root.user.Id, "invitation", "friend_cumulative", "every", "4", start, 2000)
	ratio := activity(root.user.Id, "invitation", "friend_ratio", "every", "", start, 0)
	a := register("18481", "frienda", inviter.user.AffCode)
	b := register("18482", "friendb", inviter.user.AffCode)
	check(giftCount(a.user.Id, own) == 1 && giftQuota(a.user.Id, own) == 25000 && giftCount(inviter.user.Id, invited) == 2 && giftQuota(inviter.user.Id, invited) == 100000, "successful registration rewards correct separate beneficiaries")
	var relation model.MarketingInvitation
	must(model.DB.First(&relation, "user_id = ?", a.user.Id).Error)
	check(relation.InviterID == inviter.user.Id && relation.EstablishedAt >= start, "real registration invitation")
	fmt.Println("REGISTRATION_SUCCESS_OK trusted_accounts=true atomic_fact=true own_and_inviter_gifts=true principal_zero=true legacy_grants_ignored=true")
	before := snapshotCounts()
	for _, option := range []string{"RegisterEnabled", "PasswordRegisterEnabled"} {
		setOption(root, option, "false")
		r := call("18482", "POST", "/api/user/register", "", map[string]any{"username": "disabledfriend", "password": common.GetUUID(), "aff_code": inviter.user.AffCode}, nil, nil)
		check(!r.Success, "disabled registration rejected")
		unchanged(before, "disabled registration leaves no fact or award")
		setOption(root, option, "true")
	}
	setOption(root, "EmailVerificationEnabled", "true")
	r := call("18481", "POST", "/api/user/register", "", map[string]any{"username": "bademailfriend", "password": common.GetUUID(), "email": "invalid@example.invalid", "verification_code": "wrong", "aff_code": inviter.user.AffCode}, nil, nil)
	check(!r.Success, "unverified email rejected")
	unchanged(before, "email failure creates no award")
	setOption(root, "EmailVerificationEnabled", "false")
	r = call("18482", "POST", "/api/user/register", "", map[string]any{"username": a.user.Username, "password": a.password, "aff_code": other.user.AffCode}, nil, nil)
	check(!r.Success, "duplicate registration rejected")
	unchanged(before, "duplicate registration no rewards")
	success(call("18482", "POST", "/api/user/login", "", map[string]any{"username": a.user.Username, "password": a.password, "aff_code": other.user.AffCode, "inviter_id": other.user.Id}, nil, nil), "login cannot rebind")
	must(model.DB.First(&relation, "user_id = ?", a.user.Id).Error)
	check(relation.InviterID == inviter.user.Id, "login retains inviter")
	unchanged(before, "login no registration replay")
	fmt.Println("REGISTRATION_REJECTIONS_OK disabled=true email_failed=true duplicate=true login_rebind=false no_partial_assets=true")
	// Same code replays and real online notifications both exercise successful
	// payment entry points, without direct inserts into payment or event ledgers.
	for i, who := range []identity{a, b} {
		code := createCode("18481", root, codeBody("friend-"+strconv.Itoa(i), "12", "3"))
		success(call("18482", "POST", "/api/user/topup", who.token, map[string]any{"key": code.Key}, nil, nil), "friend paid code")
		success(call("18481", "POST", "/api/user/topup", who.token, map[string]any{"key": code.Key}, nil, nil), "friend code replay")
	}
	check(giftCount(inviter.user.Id, once) == 0 && giftCount(inviter.user.Id, every) == 0, "friends 12+12 not pooled to threshold 20")
	check(giftQuota(inviter.user.Id, ratio) == 30000, "two 12 CNY payments yield 1.2 CNY gift, not 0.3 face-based")
	for range 3 {
		order := createOnline("18481", a, 12)
		check(notify("18482", "POST", signedReceipt(order, epayKey)) == "success", "invited online payment")
		check(notify("18481", "POST", signedReceipt(order, epayKey)) == "success", "online callback replay")
	}
	var payments []model.MarketingPayment
	must(model.DB.Where("user_id = ?", a.user.Id).Order("sequence").Find(&payments).Error)
	check(len(payments) == 4 && payments[0].FirstStatus == model.MarketingFirstPaymentConfirmed, "actual registration establishes first payment history")
	for i, p := range payments {
		check(p.Sequence == int64(i+1), "successful payment sequence stable")
		if i > 0 {
			check(p.FirstStatus == model.MarketingFirstPaymentLater, "later payments do not reset first")
		}
	}
	check(giftCount(inviter.user.Id, once) == 1 && giftCount(inviter.user.Id, every) == 2, "friend A48 earns once plus two full20 awards")
	check(giftQuota(inviter.user.Id, ratio) == 75000, "friend A48 B12 actual cash total60 at5percent")
	var progress model.MarketingActivityProgress
	must(model.DB.Where("activity_id = ? AND friend_id = ?", every, a.user.Id).First(&progress).Error)
	check(progress.RemainderCents == 800, "friend A remainder retained")
	oldOrder := createOnline("18481", old, 12)
	check(notify("18482", "POST", signedReceipt(oldOrder, epayKey)) == "success", "old relationship real payment")
	check(giftQuota(inviter.user.Id, ratio) == 75000, "pre-activity invitation excluded")
	fmt.Println("REGISTRATION_INVITATION_PAYMENTS_OK separate_friends=true once=1 every=2 remainder_cents=800 actual_cash_ratio=true old_relationship_excluded=true")
	// Reject untrusted OAuth state and failed external token exchange before any
	// user creation; successful callback consumes its original flow exactly once.
	before = snapshotCounts()
	r = oauthCallback("18482", "forged", "good")
	check(!r.Success, "forged OAuth state rejected")
	unchanged(before, "invalid OAuth state no awards")
	state := oauthState(inviter.user.AffCode)
	r = oauthCallback("18482", state, "bad")
	check(!r.Success, "failed OAuth provider exchange rejected")
	unchanged(before, "provider failure no awards")
	state = oauthState(inviter.user.AffCode)
	r = oauthCallback("18482", state, "good")
	success(r, "real OAuth callback registration")
	var oauthUser model.User
	must(model.DB.Where("username = ?", "oauthfriend").First(&oauthUser).Error)
	check(giftCount(oauthUser.Id, own) == 1 && giftCount(inviter.user.Id, invited) == 3, "OAuth registration has own and invitation reward")
	before = snapshotCounts()
	r = oauthCallback("18481", state, "good")
	check(!r.Success, "OAuth consumed state replay rejected")
	unchanged(before, "OAuth replay no award")
	state = oauthState(other.user.AffCode)
	success(oauthCallback("18481", state, "good"), "existing OAuth login")
	unchanged(before, "existing OAuth login no rewards")
	relation = model.MarketingInvitation{}
	must(model.DB.First(&relation, "user_id = ?", oauthUser.Id).Error)
	check(relation.InviterID == inviter.user.Id, "OAuth re-login cannot rebind invitation")
	fmt.Println("REGISTRATION_OAUTH_OK external_identity_mock_only=true real_state_and_callback=true failures_no_awards=true replay_rejected=true existing_login_no_rebind=true")
	before = snapshotCounts()
	r = call("18481", "GET", "/api/oauth/wechat?code=invalid", "", nil, nil, nil)
	check(!r.Success, "invalid WeChat code rejected")
	unchanged(before, "WeChat failure no award")
	success(call("18481", "GET", "/api/oauth/wechat?code=wechat-good", "", nil, nil, nil), "real WeChat registration")
	var wechatUser model.User
	must(model.DB.Where("wechat_id = ?", "wechat-subject-1").First(&wechatUser).Error)
	check(wechatUser.Quota == 0 && wechatUser.AffQuota == 0 && giftCount(wechatUser.Id, own) == 1, "WeChat receives isolated registration gift")
	var wechatAccount model.MarketingAccount
	must(model.DB.First(&wechatAccount, "user_id = ?", wechatUser.Id).Error)
	check(wechatAccount.OriginState == "verified" && wechatAccount.PaymentHistory == model.MarketingPaymentHistoryNone, "WeChat trusted account")
	before = snapshotCounts()
	success(call("18482", "GET", "/api/oauth/wechat?code=wechat-good", "", nil, nil, nil), "existing WeChat login")
	unchanged(before, "existing WeChat login no double gift")
	trace(wechatUser.Id)
	fmt.Println("REGISTRATION_WECHAT_OK actual_auth_handler=true invalid_code_no_award=true registration_gift=true existing_identity_no_duplicate=true")

	// Distinct authorization codes from two gateways resolve to one identity.
	// The external provider barrier ensures both requests are in flight together.
	var raceReplies [2]response
	var race sync.WaitGroup
	race.Go(func() {
		raceReplies[0] = call("18481", "GET", "/api/oauth/wechat?code=wechat-race-a", "", nil, nil, nil)
	})
	race.Go(func() {
		raceReplies[1] = call("18482", "GET", "/api/oauth/wechat?code=wechat-race-b", "", nil, nil, nil)
	})
	race.Wait()
	check(raceReplies[0].Success || raceReplies[1].Success, "one concurrent identity registration succeeds")
	var sameIdentity []model.User
	must(model.DB.Where("wechat_id = ?", "wechat-subject-race").Find(&sameIdentity).Error)
	check(len(sameIdentity) == 1, "concurrent same WeChat identity creates one account")
	var sameFacts int64
	must(model.DB.Model(&model.MarketingActivityFact{}).Where("user_id = ? AND kind = ?", sameIdentity[0].Id, "registration").Count(&sameFacts).Error)
	check(sameFacts == 1 && giftCount(sameIdentity[0].Id, own) == 1, "concurrent identity gets one registration fact and gift")
	before = snapshotCounts()
	for _, port := range []string{"18481", "18482"} {
		success(call(port, "GET", "/api/oauth/wechat?code=wechat-race-a", "", nil, nil, nil), "identity race retry logs into existing account")
	}
	unchanged(before, "concurrent identity retries never add account or gift")
	trace(sameIdentity[0].Id)
	fmt.Println("REGISTRATION_WECHAT_RACE_OK two_gateways=true distinct_codes_same_identity=true one_user_fact_gift=true retries_no_duplicate=true")

	// A historical AffQuota remains historical; even with enough old quota an
	// authenticated request must not turn an invitation asset into principal.
	must(model.DB.Model(&model.User{}).Where("id = ?", other.user.Id).Update("aff_quota", 12345).Error)
	before = snapshotCounts()
	r = call("18482", "POST", "/api/user/aff_transfer", other.token, map[string]any{"quota": 1000}, nil, nil)
	check(!r.Success && r.MessageKey == "Invitation rewards are credited as gifts and cannot be transferred to paid balance", "legacy transfer explicitly rejected")
	must(model.DB.First(&other.user, other.user.Id).Error)
	check(other.user.Quota == 0 && other.user.AffQuota == 12345, "legacy transfer preserves principal and historical amount")
	unchanged(before, "legacy transfer creates no payment or gift")
	fmt.Println("REGISTRATION_LEGACY_TRANSFER_OK sufficient_legacy_aff_quota=true authenticated_route=true rejected=true principal_unchanged=true")

	// Deterministic database fault injected only after all schema startup. New
	// gifts fail while registration/fact/decision commits remain durable.
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts ADD CONSTRAINT registration_fixture_delivery_failure CHECK (false) NOT VALID").Error)
	pending := register("18481", "recoveryfriend", inviter.user.AffCode)
	check(giftCount(pending.user.Id, own) == 0, "delivery fault did not create gift")
	var obligations int64
	must(model.DB.Model(&model.MarketingActivityReward{}).Joins("JOIN marketing_v2_activity_facts ON marketing_v2_activity_facts.id = marketing_v2_activity_rewards.fact_id").Where("marketing_v2_activity_facts.user_id = ? AND marketing_v2_activity_rewards.state = ?", pending.user.Id, "pending").Count(&obligations).Error)
	check(obligations == 2, "registered user and inviter obligations survive delivery failure")
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts DROP CONSTRAINT registration_fixture_delivery_failure").Error)
	first := startWorker(pending.user.Id, true)
	concurrent := startWorker(pending.user.Id, false)
	first.release()
	concurrent.release()
	first.finish(true)
	concurrent.finish(false)
	check(giftCount(pending.user.Id, own) == 1 && giftCount(inviter.user.Id, invited) == 4, "lost reply worker committed once")
	before = snapshotCounts()
	w1 := startWorker(pending.user.Id, false)
	w2 := startWorker(pending.user.Id, false)
	w1.release()
	w2.release()
	w1.finish(false)
	w2.finish(false)
	unchanged(before, "two process recovery retry cannot duplicate fact or gift")
	trace(inviter.user.Id)
	trace(a.user.Id)
	trace(oauthUser.Id)
	trace(pending.user.Id)
	must(model.DB.First(&inviter.user, inviter.user.Id).Error)
	check(inviter.user.Quota == 0 && inviter.user.AffQuota == 0, "all invitation gifts remain nonrefundable")
	fmt.Println("REGISTRATION_RECOVERY_OK actual_registration=true delivery_failure_obligations=2 committed_reply_lost=true two_process_replay=true trace_fact_decision_reward_gift_ledger=true")
	// Both real gateways are replaced with committed but undelivered rewards.
	// No foreground retry or worker CLI is called after restart.
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts ADD CONSTRAINT registration_fixture_delivery_failure CHECK (false) NOT VALID").Error)
	restartUser := register("18481", "restartfriend", inviter.user.AffCode)
	check(giftCount(restartUser.user.Id, own) == 0, "restart fixture must retain pending registration gift")
	g1.stop()
	g2.stop()
	must(model.DB.Exec("ALTER TABLE marketing_v2_gifts DROP CONSTRAINT registration_fixture_delivery_failure").Error)
	var restartDraw *model.MarketingLotteryDraw
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		at := time.Now().Unix()
		money := model.MarketingMoneySnapshot{Amount: "2", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}
		pool, err := model.CreateMarketingLotteryPool(tx, root.user.Id, "restart-recovery-pool", model.MarketingLotteryPoolSpec{Name: "Restart fixture", Style: "wheel", StartsAt: at - 1, EndsAt: at + 3600, Capacity: 1, Prizes: []model.MarketingLotteryPrizeSpec{{Name: "Gift", Weight: 1, Stock: 1, Gifts: []model.MarketingLotteryGiftSpec{{Money: money}}}}}, at-1)
		if err != nil {
			return err
		}
		if _, err = model.IssueMarketingLotteryChances(tx, restartUser.user.Id, pool.ID, "restart-opportunity", 1, at); err != nil {
			return err
		}
		restartDraw, err = model.DrawMarketingLotteryChance(tx, restartUser.user.Id, pool.ID, "restart-original-draw", at)
		return err
	}))
	restartedA := launch("18481")
	defer restartedA.stop()
	restartedB := launch("18482")
	defer restartedB.stop()
	deadline := time.Now().Add(50 * time.Second)
	for {
		must(model.DB.First(restartDraw, restartDraw.ID).Error)
		if giftCount(restartUser.user.Id, own) == 1 && giftCount(inviter.user.Id, invited) == 5 && restartDraw.State == "complete" {
			break
		}
		check(time.Now().Before(deadline), "real gateways failed to recover pending activity/lottery on restart")
		time.Sleep(100 * time.Millisecond)
	}
	var restartDrawCount, restartAwardCount int64
	must(model.DB.Model(&model.MarketingLotteryDraw{}).Where("user_id = ?", restartUser.user.Id).Count(&restartDrawCount).Error)
	must(model.DB.Model(&model.MarketingLotteryAward{}).Where("draw_id = ? AND state = ?", restartDraw.ID, "credited").Count(&restartAwardCount).Error)
	check(restartDrawCount == 1 && restartAwardCount == 1, "startup recovery rerolled or duplicated original lottery")
	trace(restartUser.user.Id)
	fmt.Println("REGISTRATION_STARTUP_RECOVERY_OK gateways_restarted=2 shared_database_lease=true update_task_false=true no_user_retry=true own_and_inviter_gifts_once=true original_lottery_result_once=true")
	fmt.Println("REGISTRATION_ALL_OK actual_password_registration=true actual_generic_oauth=true payment_history_not_reset=true startup_recovery=true")
}
