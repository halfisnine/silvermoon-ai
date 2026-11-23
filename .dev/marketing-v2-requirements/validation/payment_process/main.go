// Two real gateway processes share a private database and Redis namespace.
// Users are seeded only because registration integration belongs to MV2-009;
// all credentials, paid-code writes, redemption and demotion use real HTTP.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	"gorm.io/gorm"
)

const trustedOrigin = "https://payment-fixture.invalid"

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
	cmd := exec.Command("/fixtures/payment-gateway", "--port", port, "--log-dir", "/tmp/mv2-payment-"+port)
	cmd.Env = append(os.Environ(), "NODE_TYPE=master", "PORT="+port, "UPDATE_TASK=false", "SESSION_SECRET=isolated-payment-process-secret", "CRYPTO_SECRET=isolated-payment-process-secret", "SESSION_COOKIE_SECURE=true", "SESSION_COOKIE_TRUSTED_URL="+trustedOrigin, "PASSWORD_LOGIN_ENCRYPTION_ENABLED=false", "BATCH_UPDATE_ENABLED=true", "MEMORY_CACHE_ENABLED=true", "GIN_MODE=release", "CRITICAL_RATE_LIMIT=200")
	log, err := os.Create("/fixtures/payment-gateway-" + port + "-" + os.Getenv("PAYMENT_FIXTURE_STAMP") + ".log")
	must(err)
	safeLog := &safeGatewayLog{file: log}
	cmd.Stdout, cmd.Stderr = safeLog, safeLog
	must(cmd.Start())
	fmt.Printf("PAYMENT_GATEWAY_STARTED port=%s pid=%d\n", port, cmd.Process.Pid)
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

func verifyOnline(root, admin identity, users []identity, first, single, cumulative int64, key string) {
	// Wrong signed facts are rejected before the pending order or any award changes.
	order := createOnline("18381", users[0], 12)
	check(order.ExpectedPaidCents == 1200 && order.MerchantID == "fixture-merchant", "checkout freezes actual CNY and merchant")
	var frozen model.MarketingMoneySnapshot
	must(common.UnmarshalJsonStr(order.CreditedConversion, &frozen))
	quota, err := frozen.Quota()
	must(err)
	check(quota == 600000, "checkout freezes configured quota amount")
	for _, scenario := range []struct{ name, field, value, reply string }{{"wrong-signature", "sign", "invalid-signature", "fail"}, {"wrong-money", "money", "11.99", "fail"}, {"wrong-merchant", "pid", "foreign-merchant", "fail"}, {"missing-order", "out_trade_no", "not-an-order", "fail"}, {"not-paid", "trade_status", "WAIT_BUYER_PAY", "success"}} {
		params := signedReceipt(order, key)
		params[scenario.field] = scenario.value
		if scenario.field != "sign" {
			params, err = epay.GenerateParamsWithError(params, key)
			must(err)
		}
		check(notify("18382", "POST", params) == scenario.reply, "invalid payment receipt: "+scenario.name)
		var current model.TopUp
		must(model.DB.First(&current, order.Id).Error)
		check(current.Status == common.TopUpStatusPending && current.PaymentID == 0 && len(paymentRows(users[0].user.Id)) == 0, "invalid notification cannot post payment")
		check(giftCount(users[0].user.Id, first) == 0 && giftCount(users[0].user.Id, single) == 0, "invalid notification cannot award gifts")
	}
	var replies [2]string
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 2 {
		wg.Go(func() {
			<-start
			replies[i] = notify([]string{"18381", "18382"}[i], []string{"GET", "POST"}[i], signedReceipt(order, key))
		})
	}
	close(start)
	wg.Wait()
	check(replies[0] == "success" && replies[1] == "success", "two gateway duplicate callbacks acknowledged")
	rows := paymentRows(users[0].user.Id)
	check(len(rows) == 1 && rows[0].SourceKind == "online" && rows[0].SourceReference == fmt.Sprintf("topup:%d", order.Id) && rows[0].PaidCents == 1200 && rows[0].CreditedQuota == 600000 && rows[0].FirstStatus == model.MarketingFirstPaymentConfirmed, "callbacks create one first paid fact")
	check(giftCount(users[0].user.Id, first) == 1 && giftCount(users[0].user.Id, single) == 1, "callback immediately delivers rewards once")
	var current model.TopUp
	must(model.DB.First(&current, order.Id).Error)
	check(current.PaymentID == rows[0].ID && current.ProviderReference == "provider-"+order.TradeNo, "provider reference links payment")
	badReplay := signedReceipt(order, key)
	badReplay["money"] = "1.00"
	badReplay, err = epay.GenerateParamsWithError(badReplay, key)
	must(err)
	check(notify("18382", "POST", badReplay) == "fail" && len(paymentRows(users[0].user.Id)) == 1, "completed order does not excuse wrong amount replay")
	fmt.Println("PAYMENT_EPAY_CALLBACK_OK real_checkout=true real_md5=true invalid_facts_rejected=true duplicate_gateways=true immediate_rewards=true")

	manualOrder := createOnline("18382", users[0], 12)
	manual := map[string]any{"trade_no": manualOrder.TradeNo, "paid_amount_cny": "12.00", "evidence": "verified-provider-receipt"}
	denied(call("18381", "POST", "/api/user/topup/complete", users[0].token, manual, nil, nil), "ordinary user cannot manually credit")
	r := call("18381", "POST", "/api/user/topup/complete", admin.token, map[string]any{"trade_no": manualOrder.TradeNo}, nil, nil)
	check(!r.Success && len(paymentRows(users[0].user.Id)) == 1, "manual completion requires actual payment evidence")
	r = call("18381", "POST", "/api/user/topup/complete", admin.token, map[string]any{"trade_no": manualOrder.TradeNo, "paid_amount_cny": "11.00", "evidence": "receipt-does-not-match"}, nil, nil)
	check(!r.Success && len(paymentRows(users[0].user.Id)) == 1, "manual completion cannot alter actual order price")
	start = make(chan struct{})
	var manualResponse response
	wg.Go(func() { <-start; replies[0] = notify("18381", "POST", signedReceipt(manualOrder, key)) })
	wg.Go(func() {
		<-start
		manualResponse = call("18382", "POST", "/api/user/topup/complete", admin.token, manual, nil, nil)
	})
	close(start)
	wg.Wait()
	check(replies[0] == "success", "callback competing with manual completion")
	success(manualResponse, "manual completion competing with callback")
	rows = paymentRows(users[0].user.Id)
	check(len(rows) == 2 && rows[1].Sequence == 2, "manual and callback share single order source")
	check(giftCount(users[0].user.Id, first) == 1 && giftCount(users[0].user.Id, single) == 2 && giftCount(users[0].user.Id, cumulative) == 1, "manual callback competition awards once")
	success(call("18381", "POST", "/api/user/topup/complete", admin.token, manual, nil, nil), "manual replay")
	check(len(paymentRows(users[0].user.Id)) == 2, "manual replay leaves payment count unchanged")
	solo := createOnline("18381", users[0], 12)
	success(call("18382", "POST", "/api/user/topup/complete", admin.token, map[string]any{"trade_no": solo.TradeNo, "paid_amount_cny": "12.00", "evidence": "manual-only-provider-receipt"}, nil, nil), "standalone manual completion")
	must(model.DB.First(&solo, solo.Id).Error)
	check(solo.CompletionActorID == admin.user.Id && solo.CompletionEvidence == "manual-only-provider-receipt" && solo.PaymentID > 0, "manual completion preserves actual actor and evidence")
	check(len(paymentRows(users[0].user.Id)) == 3 && giftCount(users[0].user.Id, single) == 3, "standalone manual completion posts payment and immediate reward")
	fmt.Println("PAYMENT_EPAY_MANUAL_RACE_OK callback_manual_same_source=true evidence_required=true user_forbidden=true")

	mixedOrder := createOnline("18381", users[1], 12)
	code := createCode("18382", admin, codeBody("online-code-race", "12", "3"))
	start = make(chan struct{})
	var codeResponse response
	wg.Go(func() { <-start; replies[0] = notify("18381", "POST", signedReceipt(mixedOrder, key)) })
	wg.Go(func() {
		<-start
		codeResponse = call("18382", "POST", "/api/user/topup", users[1].token, map[string]any{"key": code.Key}, nil, nil)
	})
	close(start)
	wg.Wait()
	check(replies[0] == "success", "mixed online callback")
	success(codeResponse, "mixed card redemption")
	rows = paymentRows(users[1].user.Id)
	check(len(rows) == 2 && rows[0].Sequence == 1 && rows[1].Sequence == 2 && rows[0].FirstStatus == model.MarketingFirstPaymentConfirmed && rows[1].FirstStatus == model.MarketingFirstPaymentLater, "online and code share one ordered first-payment history")
	check(rows[0].SourceKind != rows[1].SourceKind && rows[0].PaidCents+rows[1].PaidCents == 2400 && rows[0].CreditedQuota+rows[1].CreditedQuota == 675000, "online and code retain distinct credited amounts and actual paid amounts")
	check(giftCount(users[1].user.Id, first) == 1 && giftCount(users[1].user.Id, single) == 2 && giftCount(users[1].user.Id, cumulative) == 1, "online and code share cumulative rewards")
	fmt.Println("PAYMENT_EPAY_CARD_SEQUENCE_OK first=1 payments=2 cash_cents=2400 principal_quota=675000")

	frozenOrder := createOnline("18381", users[2], 12)
	for _, port := range []string{"18381", "18382"} {
		success(call(port, "PUT", "/api/option/", root.token, map[string]any{"key": "QuotaPerUnit", "value": "70000"}, nil, nil), "change configured quota through root HTTP")
		status := call(port, "GET", "/api/status", "", nil, nil, nil)
		success(status, "read actual active status")
		var data struct {
			QuotaPerUnit float64 `json:"quota_per_unit"`
		}
		must(common.Unmarshal(status.Data, &data))
		check(data.QuotaPerUnit == 70000, "configuration active on each gateway before settlement")
	}
	check(notify("18382", "POST", signedReceipt(frozenOrder, key)) == "success", "settle old checkout after configuration change")
	rows = paymentRows(users[2].user.Id)
	check(len(rows) == 1 && rows[0].CreditedQuota == 600000 && rows[0].Conversion == frozenOrder.CreditedConversion, "old checkout preserves original quota conversion")
	newOrder := createOnline("18382", users[2], 12)
	check(notify("18381", "POST", signedReceipt(newOrder, key)) == "success", "settle new checkout with new configuration")
	rows = paymentRows(users[2].user.Id)
	check(len(rows) == 2 && rows[1].CreditedQuota == 840000 && rows[1].PaidCents == 1200, "new checkout uses updated quota configuration independently of actual cash")
	fmt.Println("PAYMENT_EPAY_FROZEN_QUOTA_OK original=600000 changed=840000 actual_cash_unchanged=true status_checked_both_gateways=true")

	var countBefore, countAfter int64
	must(model.DB.Model(&model.TopUp{}).Count(&countBefore).Error)
	for _, path := range []string{"/api/user/stripe/pay", "/api/user/creem/pay", "/api/user/waffo/pay", "/api/user/waffo-pancake/pay"} {
		r := call("18381", "POST", path, users[2].token, map[string]any{"amount": 12, "payment_method": "stripe", "product_id": "fixture"}, nil, nil)
		check(!r.Success && r.Message != "success", "unsupported payment creation rejected")
	}
	must(model.DB.Model(&model.TopUp{}).Count(&countAfter).Error)
	check(countBefore == countAfter, "unsupported providers cannot create pending orders")
	for _, path := range []string{"/api/stripe/webhook", "/api/creem/webhook", "/api/waffo/webhook", "/api/waffo-pancake/webhook/test"} {
		r := call("18382", "POST", path, "", map[string]any{}, nil, nil)
		check(r.status == 403, "unsupported provider notification is blocked before verification")
	}
	// A real enabled legacy plan must not reopen the unsupported currency path.
	plan := model.SubscriptionPlan{Title: "Isolated unsupported plan", Enabled: true, StripePriceId: "fixture-price", CreemProductId: "fixture", WaffoPancakeProductId: "fixture-product"}
	must(model.DB.Create(&plan).Error)
	var beforeUser, afterUser model.User
	must(model.DB.First(&beforeUser, users[2].user.Id).Error)
	var rowsBefore []int64
	records := []any{&model.SubscriptionOrder{}, &model.TopUp{}, &model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingActivityFact{}}
	for _, record := range records {
		var count int64
		must(model.DB.Model(record).Count(&count).Error)
		rowsBefore = append(rowsBefore, count)
	}
	for _, port := range []string{"18381", "18382"} {
		for _, path := range []string{"/api/subscription/stripe/pay", "/api/subscription/creem/pay", "/api/subscription/waffo-pancake/pay"} {
			r := call(port, "POST", path, users[2].token, map[string]any{"plan_id": plan.Id}, nil, nil)
			check(r.status == 200 && !r.Success && r.MessageKey == "Unsupported payment channel", "configured unsupported subscription rejected by explicit guard")
		}
	}
	for i, record := range records {
		var count int64
		must(model.DB.Model(record).Count(&count).Error)
		check(count == rowsBefore[i], "unsupported subscription does not create orders, payments, gifts or facts")
	}
	must(model.DB.First(&afterUser, users[2].user.Id).Error)
	check(afterUser.Quota == beforeUser.Quota, "unsupported subscription leaves principal unchanged")
	fmt.Println("PAYMENT_UNSUPPORTED_SUBSCRIPTIONS_OK configured_enabled_plan=true gateways=2 rejected=6 no_orders_or_rewards=true principal_unchanged=true")
	fmt.Println("PAYMENT_UNSUPPORTED_PROVIDERS_OK settings_populated=true creation_rejected=4 notification_rejected=4 no_orders=true")
}
func seedUser(name string, role int) identity {
	password := common.GetUUID() + "aA9!"
	hash, err := common.Password2Hash(password)
	must(err)
	u := model.User{Username: name, Password: hash, AffCode: name, Status: common.UserStatusEnabled, Group: "default", Role: role, AuthVersion: 1}
	must(model.DB.Create(&u).Error)
	// Explicit fixture substitute for the not-yet-integrated registration fact.
	must(model.DB.Create(&model.MarketingAccount{UserID: u.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "isolated new user fixture, not registration acceptance"}).Error)
	return identity{user: u, password: password}
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
func codeBody(name, paid, credit string) map[string]any {
	return map[string]any{"name": name, "count": 1, "paid_amount_cny": paid, "credit_amount": credit, "credit_currency": "CNY", "sales_channel": "registered-card-shop", "sales_batch": "http-fixture-batch", "sales_evidence": "fixture-receipt", "expired_time": 0}
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
func cardState() [32]byte {
	var cards []model.Redemption
	must(model.DB.Unscoped().Order("id").Find(&cards).Error)
	data, err := common.Marshal(cards)
	must(err)
	return sha256.Sum256(data)
}
func paymentRows(user int) []model.MarketingPayment {
	var rows []model.MarketingPayment
	must(model.DB.Where("user_id = ?", user).Order("sequence").Find(&rows).Error)
	return rows
}
func giftCount(user int, activity int64) int64 {
	var n int64
	must(model.DB.Model(&model.MarketingActivityReward{}).Where("user_id = ? AND activity_id = ? AND state = ?", user, activity, "credited").Count(&n).Error)
	return n
}
func activity(admin int, name, condition, frequency string, threshold int64, gift string) int64 {
	now := common.GetTimestamp()
	spec := model.MarketingActivitySpec{Name: name, Kind: "recharge", Condition: condition, Frequency: frequency, StartsAt: now - 10, EndsAt: now + 1800, ThresholdCents: threshold, Rewards: []model.MarketingActivityRewardSpec{{Kind: "gift", Calculation: "fixed", ValidSeconds: 3600, Money: model.MarketingMoneySnapshot{Amount: gift, QuotaPerUnit: "100", CurrencyPerUnit: "2", Currency: "CNY"}}}}
	var a *model.MarketingActivity
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = model.CreateMarketingActivity(tx, admin, "create-"+name, spec, now-20)
		if err != nil {
			return err
		}
		a, err = model.ActivateMarketingActivity(tx, admin, a.ID, "activate-"+name, now-20)
		return err
	}))
	return a.ID
}
func main() {
	check(strings.Contains(os.Getenv("SQL_DSN"), "/marketing_v2_payment_process?"), "isolated payment DB required")
	check(os.Getenv("REDIS_CONN_STRING") == "redis://redis:6379/11", "isolated Redis DB11 required")
	common.CryptoSecret = "isolated-payment-process-secret"
	common.IsMasterNode = true
	must(model.InitDB())
	must(model.InitLogDB())
	must(common.InitRedisClient())
	defer common.RDB.Close()
	must(common.RDB.FlushDB(context.Background()).Err())
	var version string
	must(model.DB.Raw("SELECT version()").Scan(&version).Error)
	fmt.Printf("PAYMENT_DATABASE %s redis_db=11\n", version)
	root := seedUser("paymentroot", common.RoleRootUser)
	admin := seedUser("paymentadmin", common.RoleAdminUser)
	a := seedUser("paymenta", common.RoleCommonUser)
	b := seedUser("paymentb", common.RoleCommonUser)
	c := seedUser("paymentc", common.RoleCommonUser)
	d := seedUser("paymentd", common.RoleCommonUser)
	onlineUsers := []identity{seedUser("paymentonline", common.RoleCommonUser), seedUser("paymentmixed", common.RoleCommonUser), seedUser("paymentfrozen", common.RoleCommonUser)}
	epayKey := common.GetUUID()
	settings := map[string]string{
		"QuotaPerUnit": "50000", "USDExchangeRate": "2", "general_setting.quota_display_type": "CNY",
		"payment_setting.compliance_confirmed": "true", "payment_setting.compliance_terms_version": "v1",
		"PayAddress": "https://epay-fixture.invalid", "EpayId": "fixture-merchant", "EpayKey": epayKey,
		"Price": "1", "MinTopUp": "1", "CustomCallbackAddress": trustedOrigin,
		"PayMethods": `[{"name":"Alipay","type":"alipay"}]`,
		// Populated unsupported-provider settings must not open another credit path.
		"StripeApiSecret": "fixture-not-real", "StripeWebhookSecret": "fixture-not-real", "StripePriceId": "fixture-price",
		"CreemApiKey": "fixture-not-real", "CreemWebhookSecret": "fixture-not-real",
		"CreemProducts": `[{"product_id":"fixture","price":12,"quota":50000,"currency":"USD"}]`,
		"WaffoEnabled":  "true", "WaffoApiKey": "fixture-not-real", "WaffoPrivateKey": "fixture-not-real", "WaffoPublicCert": "fixture-not-real",
		"WaffoPancakeMerchantID": "fixture-merchant", "WaffoPancakePrivateKey": "fixture-not-real", "WaffoPancakeProductID": "fixture-product", "WaffoPancakeStoreID": "fixture-store",
	}
	for key, value := range settings {
		must(model.DB.Create(&model.Option{Key: key, Value: value}).Error)
	}

	first := activity(root.user.Id, "http-first", "first_successful_payment", "once", 1000, "1.5")
	single := activity(root.user.Id, "http-single", "single_payment", "every", 1000, "1")
	cumulative := activity(root.user.Id, "http-cumulative", "cumulative_payments", "every", 2000, "2")
	g1 := launch("18381")
	defer g1.stop()
	g2 := launch("18382")
	defer g2.stop()
	for _, who := range []*identity{&root, &admin, &a, &b, &c, &d} {
		login("18381", who)
	}
	for i := range onlineUsers {
		login("18381", &onlineUsers[i])
	}

	// All six administrative write routes enforce server identity and role.
	base := createCode("18381", admin, codeBody("auth-target", "12", "3"))
	change := codeBody("auth-change", "9", "4")
	change["id"] = base.Id
	writes := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/redemption/", codeBody("unauthorized", "1", "1")},
		{"PUT", "/api/redemption/", change},
		{"PUT", "/api/redemption/?status_only=true", map[string]any{"id": base.Id, "status": common.RedemptionCodeStatusDisabled}},
		{"DELETE", "/api/redemption/" + strconv.Itoa(base.Id), nil},
		{"POST", "/api/redemption/batch", map[string]any{"ids": []int{base.Id}}},
		{"DELETE", "/api/redemption/invalid", nil},
	}
	before := cardState()
	for _, w := range writes {
		denied(call("18381", w.method, w.path, "", w.body, nil, nil), "anonymous card mutation")
		denied(call("18382", w.method, w.path, a.token, w.body, nil, nil), "ordinary user card mutation")
		denied(call("18382", w.method, w.path, "", w.body, http.Header{"Origin": []string{"https://foreign.invalid"}}, admin.cookie), "cookie-only card mutation")
	}
	check(before == cardState(), "unauthorized calls preserve all card records")
	denied(call("18382", "POST", "/api/user/topup", "", map[string]any{"key": base.Key}, http.Header{"Origin": []string{"https://foreign.invalid"}}, a.cookie), "cookie-only redemption")
	preflight := call("18382", "OPTIONS", "/api/redemption/", "", nil, http.Header{"Origin": []string{"https://foreign.invalid"}, "Access-Control-Request-Method": []string{"POST"}, "Access-Control-Request-Headers": []string{"authorization,content-type"}}, nil)
	// Relay's later global CORS handler also handles unmatched OPTIONS. Its
	// wildcard Origin cannot authorize credentialed cookie requests, and a
	// wildcard Allow-Headers does not explicitly authorize Authorization.
	check(preflight.header.Get("Access-Control-Allow-Origin") != "https://foreign.invalid", "preflight does not reflect a foreign credentialed origin")
	check(!strings.Contains(strings.ToLower(preflight.header.Get("Access-Control-Allow-Headers")), "authorization"), "preflight does not explicitly allow Authorization")
	fmt.Println("PAYMENT_CARD_AUTH_OK routes=6 anonymous=true ordinary=true cookie_only=true no_mutation=true")
	before = cardState()
	for _, invalid := range []struct{ field, value string }{{"paid_amount_cny", "0"}, {"paid_amount_cny", "1.001"}, {"credit_amount", "-1"}, {"credit_currency", "USD"}, {"sales_channel", ""}, {"sales_batch", ""}} {
		body := codeBody("invalid-sale", "12", "3")
		body[invalid.field] = invalid.value
		r := call("18381", "POST", "/api/redemption/", admin.token, body, nil, nil)
		check(!r.Success, "invalid paid-code sale rejected: "+invalid.field)
	}
	raw := call("18381", "POST", "/api/redemption/", admin.token, map[string]any{"name": "old-raw-quota", "count": 1, "quota": 50000}, nil, nil)
	check(!raw.Success && cardState() == before, "raw quota and incomplete sale cannot create paid codes")
	legacy := model.Redemption{UserId: admin.user.Id, Key: common.GetUUID(), Name: "missing-sale", Status: common.RedemptionCodeStatusEnabled, Quota: 50000, CreatedTime: common.GetTimestamp()}
	must(model.DB.Create(&legacy).Error)
	raw = call("18381", "POST", "/api/user/topup", d.token, map[string]any{"key": legacy.Key}, nil, nil)
	check(!raw.Success && len(paymentRows(d.user.Id)) == 0, "missing historical sale cannot fabricate cash or first payment")
	must(model.DB.First(&legacy, legacy.Id).Error)
	check(legacy.Status == common.RedemptionCodeStatusEnabled && legacy.UsedUserId == 0 && legacy.PaymentID == 0, "invalid legacy redemption leaves credential unchanged")
	fmt.Println("PAYMENT_CARD_VALIDATION_OK actual_cny_precision=true stale_currency=true legacy_missing_sale_rejected=true")

	// Supplied trusted fields cannot override actor, quota, conversion or buyer.
	forged := codeBody("identity-fields", "12", "3")
	forged["user_id"] = a.user.Id
	forged["used_user_id"] = b.user.Id
	forged["quota"] = 99999999
	forged["paid_cents"] = 1
	forged["payment_id"] = 999
	forged["quota_per_unit"] = "1"
	forged["currency_per_unit"] = "1"
	code := createCode("18382", admin, forged)
	check(code.UserId == admin.user.Id && code.UsedUserId == 0 && code.Quota == 75000 && code.PaidCents == 1200 && code.PaymentID == 0, "server-only trusted identity and money fields")
	check(code.SalesChannel == "registered-card-shop" && code.SalesBatch == "http-fixture-batch" && code.SalesEvidence == "fixture-receipt", "registered sale evidence preserved")
	var snapshot model.MarketingMoneySnapshot
	must(common.UnmarshalJsonStr(code.CreditedConversion, &snapshot))
	check(snapshot.Amount == "3" && snapshot.QuotaPerUnit == "50000" && snapshot.CurrencyPerUnit == "2" && snapshot.Currency == "CNY", "server frozen conversion")

	var race [2]response
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, who := range []identity{a, b} {
		wg.Go(func() {
			<-start
			race[i] = call([]string{"18381", "18382"}[i], "POST", "/api/user/topup", who.token, map[string]any{"key": code.Key, "user_id": root.user.Id}, nil, nil)
		})
	}
	close(start)
	wg.Wait()
	check(race[0].Success != race[1].Success, "exactly one user redeems raced code")
	winner, loser := a, b
	if race[1].Success {
		winner, loser = b, a
	}
	must(model.DB.First(&code, code.Id).Error)
	check(code.UsedUserId == winner.user.Id, "code belongs to authenticated winner")
	rows := paymentRows(winner.user.Id)
	check(len(rows) == 1 && rows[0].PaidCents == 1200 && rows[0].CreditedQuota == 75000 && rows[0].SourceKind == "paid_code" && rows[0].SourceReference == fmt.Sprintf("redemption:%d", code.Id) && rows[0].FirstStatus == model.MarketingFirstPaymentConfirmed && rows[0].Sequence == 1, "winner has one first paid fact")
	check(code.PaymentID == rows[0].ID && code.CreditedConversion == rows[0].Conversion, "card links immutable payment snapshot")
	check(len(paymentRows(loser.user.Id)) == 0 && len(paymentRows(root.user.Id)) == 0, "loser and forged user get no money")
	check(giftCount(winner.user.Id, first) == 1 && giftCount(winner.user.Id, single) == 1 && giftCount(winner.user.Id, cumulative) == 0, "HTTP redemption triggers immediate matching gifts")
	success(call("18382", "POST", "/api/user/topup", winner.token, map[string]any{"key": code.Key}, nil, nil), "same-user redemption retry")
	check(len(paymentRows(winner.user.Id)) == 1 && giftCount(winner.user.Id, first) == 1 && giftCount(winner.user.Id, single) == 1, "retry does not duplicate facts or rewards")
	var logs int64
	must(model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", winner.user.Id, model.LogTypeTopup).Count(&logs).Error)
	check(logs == 1, "redemption retry cannot duplicate top-up audit")
	fmt.Println("PAYMENT_CARD_RACE_OK two_gateways=true two_users=true server_identity=true single_payment=true immediate_rewards=true")

	// Two different codes arriving together share one first-payment sequence.
	codes := []model.Redemption{createCode("18381", admin, codeBody("sequence-one", "12", "3")), createCode("18382", admin, codeBody("sequence-two", "12", "3"))}
	start = make(chan struct{})
	for i := range codes {
		wg.Go(func() {
			<-start
			race[i] = call([]string{"18381", "18382"}[i], "POST", "/api/user/topup", c.token, map[string]any{"key": codes[i].Key}, nil, nil)
		})
	}
	close(start)
	wg.Wait()
	for _, r := range race {
		success(r, "parallel different codes")
	}
	rows = paymentRows(c.user.Id)
	check(len(rows) == 2 && rows[0].Sequence == 1 && rows[1].Sequence == 2 && rows[0].FirstStatus == model.MarketingFirstPaymentConfirmed && rows[1].FirstStatus == model.MarketingFirstPaymentLater, "two instances serialize first payment sequence")
	check(giftCount(c.user.Id, first) == 1 && giftCount(c.user.Id, single) == 2 && giftCount(c.user.Id, cumulative) == 1, "first single and cumulative rules share actual paid facts")
	var stored model.User
	must(model.DB.First(&stored, c.user.Id).Error)
	check(stored.Quota == 150000, "gifts never increase refundable principal")
	var gifts []model.MarketingGift
	must(model.DB.Where("user_id = ?", c.user.Id).Find(&gifts).Error)
	var granted int64
	for _, g := range gifts {
		granted += g.GrantedQuota
	}
	check(granted == 275, "independent gift wallet expected awards")
	fmt.Println("PAYMENT_CARD_SEQUENCE_OK first=1 payments=2 cumulative=1 principal=150000 gift_quota=275")

	cheap := createCode("18381", admin, codeBody("low-price-high-face", "5", "40"))
	success(call("18381", "POST", "/api/user/topup", d.token, map[string]any{"key": cheap.Key}, nil, nil), "low price higher face redemption")
	rows = paymentRows(d.user.Id)
	check(len(rows) == 1 && rows[0].PaidCents == 500 && rows[0].CreditedQuota == 1000000, "cash and credit are separate")
	check(giftCount(d.user.Id, first) == 0 && giftCount(d.user.Id, single) == 0 && giftCount(d.user.Id, cumulative) == 0, "high face cannot cross actual cash threshold")
	second := createCode("18381", admin, codeBody("later-qualifies", "12", "3"))
	success(call("18382", "POST", "/api/user/topup", d.token, map[string]any{"key": second.Key}, nil, nil), "second payment crosses single threshold")
	check(giftCount(d.user.Id, first) == 0 && giftCount(d.user.Id, single) == 1 && giftCount(d.user.Id, cumulative) == 0, "first qualification is not reset by later qualifying recharge")
	fmt.Println("PAYMENT_CARD_PAID_THRESHOLD_OK face_not_cash=true first_not_reassigned=true")

	usedBefore := cardState()
	edit := codeBody("rewrite-used", "1", "1")
	edit["id"] = code.Id
	for _, w := range []struct {
		method, path string
		body         any
	}{{"PUT", "/api/redemption/", edit}, {"PUT", "/api/redemption/?status_only=true", map[string]any{"id": code.Id, "status": common.RedemptionCodeStatusEnabled}}} {
		r := call("18381", w.method, w.path, admin.token, w.body, nil, nil)
		check(!r.Success, "used card financial history cannot be rewritten")
	}
	check(usedBefore == cardState(), "used card remains unchanged after mutation attempts")
	// Exercise authorized update/status/delete paths on a still unused code.
	change["paid_amount_cny"] = "11"
	change["credit_amount"] = "6"
	success(call("18382", "PUT", "/api/redemption/", admin.token, change, nil, nil), "update unused sale")
	success(call("18382", "PUT", "/api/redemption/?status_only=true", admin.token, map[string]any{"id": base.Id, "status": common.RedemptionCodeStatusDisabled}, nil, nil), "disable unused code")
	success(call("18382", "POST", "/api/redemption/batch", admin.token, map[string]any{"ids": []int{base.Id}}, nil, nil), "delete unused batch")
	deletable := createCode("18382", admin, codeBody("delete-one", "1", "1"))
	success(call("18382", "DELETE", "/api/redemption/"+strconv.Itoa(deletable.Id), admin.token, nil, nil, nil), "delete unused individual")
	success(call("18382", "DELETE", "/api/redemption/invalid", admin.token, nil, nil, nil), "authorized invalid cleanup")
	check(len(paymentRows(winner.user.Id)) == 1, "administrative edits cannot create payments")
	var retained model.Redemption
	must(model.DB.Unscoped().First(&retained, code.Id).Error)
	check(retained.DeletedAt.Valid && retained.PaymentID == code.PaymentID && retained.CreditedConversion == code.CreditedConversion && retained.PaidCents == 1200 && retained.SalesEvidence == code.SalesEvidence, "soft cleanup preserves sale evidence and immutable payment link")
	fmt.Println("PAYMENT_CARD_MUTATION_OK unused_edit=true used_history_preserved=true no_payment_fabrication=true")
	verifyOnline(root, admin, onlineUsers, first, single, cumulative, epayKey)

	// Refresh uses cookies, unlike card APIs. Test its actual secure Origin guard.
	for _, headers := range []http.Header{{"Origin": []string{"https://foreign.invalid"}}, {"Origin": []string{"null"}}, {}, {"Origin": []string{trustedOrigin, "https://foreign.invalid"}}, {"Origin": []string{trustedOrigin + ", https://foreign.invalid"}}} {
		r := call("18382", "POST", "/api/user/auth/refresh", "", map[string]any{}, headers, a.cookie)
		check(r.status == 403 && r.Code == "AUTH_ORIGIN_FORBIDDEN", "secure refresh rejects invalid Origin before consuming cookie")
	}
	r := call("18382", "POST", "/api/user/auth/refresh", "", map[string]any{}, http.Header{"Origin": []string{trustedOrigin}}, a.cookie)
	success(r, "trusted-origin refresh")
	for _, cookie := range r.cookies {
		if cookie.Value != "" && cookie.MaxAge >= 0 {
			a.cookie = cookie
			break
		}
	}
	r = call("18381", "POST", "/api/user/auth/refresh", "", map[string]any{}, http.Header{"Referer": []string{trustedOrigin + "/console"}}, a.cookie)
	success(r, "trusted Referer fallback")
	fmt.Println("PAYMENT_ORIGIN_OK secure_mode=true invalid_origins=5 trusted_origin=true referer=true transport=loopback_server_guard_only")

	// Revocation is performed through the real password proof and admin route.
	proofResp := call("18381", "POST", "/api/verify", root.token, map[string]any{"method": "password", "scope": "admin.user.manage", "context": map[string]any{"user_id": admin.user.Id, "action": "demote"}, "password": root.password}, nil, nil)
	success(proofResp, "root step-up password verification")
	var proof struct {
		Token string `json:"proof_token"`
	}
	must(common.Unmarshal(proofResp.Data, &proof))
	check(proof.Token != "", "proof token exists")
	success(call("18381", "POST", "/api/user/manage", root.token, map[string]any{"id": admin.user.Id, "action": "demote"}, http.Header{"X-Security-Proof": []string{proof.Token}}, nil), "real admin demotion")
	before = cardState()
	for _, w := range writes {
		denied(call("18382", w.method, w.path, admin.token, w.body, nil, nil), "revoked admin cross-gateway replay")
	}
	check(before == cardState(), "revoked admin cannot mutate codes")
	denied(call("18382", "POST", "/api/user/topup/complete", admin.token, map[string]any{"trade_no": "fixture-does-not-exist"}, nil, nil), "revoked admin manual completion")
	fmt.Println("PAYMENT_REVOCATION_OK real_login=true real_step_up=true real_demotion=true other_gateway_replay_denied=true")
	fmt.Println("PAYMENT_ALL_ENTRIES_OK registration_integration=not_claimed supported=paid_code,epay_CNY")
}
