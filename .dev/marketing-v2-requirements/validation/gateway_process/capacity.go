package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type capacityProvider struct {
	active atomic.Int64
	peak   atomic.Int64
	calls  atomic.Int64
	nanos  atomic.Int64
}

func (p *capacityProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	started := time.Now()
	active := p.active.Add(1)
	for peak := p.peak.Load(); active > peak; peak = p.peak.Load() {
		if p.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	defer func() { p.active.Add(-1); p.nanos.Add(int64(time.Since(started))) }()
	p.calls.Add(1)
	// Deliberate external-provider latency, not a timing-based correctness test.
	// A sustained load needs overlapping in-flight requests, not a burst counter.
	time.Sleep(500 * time.Millisecond)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"id":"capacity-fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"fixture"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
}

type capacityAccount struct {
	user  model.User
	token model.Token
	gift  model.MarketingGift
}

type capacityResult struct {
	Profile             string         `json:"profile"`
	Concurrency         int            `json:"configured_concurrency"`
	Accounts            int            `json:"accounts"`
	DurationSeconds     float64        `json:"duration_seconds"`
	AdmissionSeconds    float64        `json:"admission_seconds"`
	Requests            int            `json:"completed_requests"`
	Successes           int            `json:"successful_requests"`
	Throughput          float64        `json:"requests_per_second"`
	SuccessThroughput   float64        `json:"successful_requests_per_second"`
	ClientPeak          int64          `json:"client_peak_inflight"`
	ClientMean          float64        `json:"client_mean_inflight"`
	ProviderPeak        int64          `json:"upstream_peak_inflight"`
	ProviderMean        float64        `json:"upstream_mean_inflight"`
	ProviderCalls       int64          `json:"upstream_calls"`
	P50                 float64        `json:"latency_p50_ms"`
	P95                 float64        `json:"latency_p95_ms"`
	P99                 float64        `json:"latency_p99_ms"`
	Errors              map[string]int `json:"error_classes"`
	GatewayRequests     [2]int         `json:"gateway_requests"`
	LockSamples         int            `json:"lock_samples"`
	SamplesWithLockWait int            `json:"samples_with_lock_wait"`
	PeakWaitingBackends int64          `json:"peak_waiting_backends"`
	AccountingVerified  bool           `json:"accounting_verified"`
	AccountingFailure   string         `json:"accounting_failure,omitempty"`
}

func runGatewayCapacity() {
	// These are isolated fixture settings. Connection pools are capped so two
	// gateways cannot overwhelm the test PostgreSQL's 100-connection default.
	must(os.Setenv("SQL_MAX_OPEN_CONNS", "24"))
	must(os.Setenv("SQL_MAX_IDLE_CONNS", "8"))
	must(os.Setenv("GLOBAL_API_RATE_LIMIT", "100000"))
	must(os.Setenv("GLOBAL_WEB_RATE_LIMIT", "100000"))
	provider := &capacityProvider{}
	upstream := httptest.NewServer(provider)
	defer upstream.Close()
	base := upstream.URL
	ch := model.Channel{Name: "mv2-capacity-local", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "isolated-provider", BaseURL: &base, Models: "mv2-capacity", Group: "default"}
	must(ch.Insert())
	must(model.DB.Create(&model.Option{Key: "ModelPrice", Value: `{"mv2-capacity":0.001}`}).Error)
	a, b := launch("18371", false), launch("18372", false)
	defer func() { _ = a.Process.Kill(); _ = a.Wait(); _ = b.Process.Kill(); _ = b.Wait() }()
	var pgVersion string
	must(model.DB.Raw("SELECT version()").Scan(&pgVersion).Error)
	info, err := common.RDB.Info(context.Background(), "server").Result()
	must(err)
	fmt.Printf("CAPACITY_ENV go=%s postgres=%q shared_redis_db=14 pool_per_gateway=24 upstream_latency_ms=500 accounting=gift_then_paid\n", runtime.Version(), pgVersion)
	for line := range strings.SplitSeq(info, "\r\n") {
		if strings.HasPrefix(line, "redis_version:") {
			fmt.Println("CAPACITY_ENV " + line)
		}
	}
	results := make([]capacityResult, 0, 4)
	for _, concurrency := range []int{50, 100} {
		for _, hotspot := range []bool{false, true} {
			results = append(results, measureGatewayCapacity(provider, concurrency, hotspot))
		}
	}
	data, err := common.Marshal(results)
	must(err)
	must(os.WriteFile("/fixtures/capacity-results.json", data, 0644))
	for _, result := range results {
		check(result.AccountingVerified, "capacity ledger verification: "+result.AccountingFailure)
	}
	fmt.Println("CAPACITY_MATRIX_OK sustained_four_profiles actual_gateway_requests shared_pg_redis financial_conservation=true")
}

func measureGatewayCapacity(provider *capacityProvider, concurrency int, hotspot bool) capacityResult {
	accountCount, profile, giftQuota := concurrency, "distributed", 1000
	if hotspot {
		accountCount, profile, giftQuota = 1, "hotspot", 20000
	}
	accounts := make([]capacityAccount, accountCount)
	now := common.GetTimestamp()
	for i := range accountCount {
		label := fmt.Sprintf("cap-%s-%d-%d", profile, concurrency, i)
		account := &accounts[i]
		account.user = model.User{Username: label, AffCode: label, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default"}
		account.token = model.Token{Key: fmt.Sprintf("capacity%040d", concurrency*10000+accountCount*100+i), Name: label, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 3_000_000, Group: "default"}
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&account.user).Error; err != nil {
				return err
			}
			account.token.UserId = account.user.Id
			if err := tx.Create(&account.token).Error; err != nil {
				return err
			}
			if err := tx.Create(&model.MarketingAccount{UserID: account.user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "isolated capacity fixture"}).Error; err != nil {
				return err
			}
			if _, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: account.user.Id, Kind: "online", Reference: label, PaidCents: 400, PostedAt: now, Money: model.MarketingMoneySnapshot{Amount: "2000000", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}}); err != nil {
				return err
			}
			gift, err := model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: account.user.Id, Kind: "recharge", Reference: label, GrantedAt: now, Money: model.MarketingMoneySnapshot{Amount: fmt.Sprint(giftQuota), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			if err == nil {
				account.gift = *gift
			}
			return err
		}))
	}
	provider.peak.Store(0)
	provider.calls.Store(0)
	provider.nanos.Store(0)
	result := capacityResult{Profile: profile, Concurrency: concurrency, Accounts: accountCount, AdmissionSeconds: 20, Errors: map[string]int{}}
	var mutex sync.Mutex
	var durations []float64
	var clientActive, clientPeak, clientNanos atomic.Int64
	perAccount := make([]int, accountCount)
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{MaxIdleConns: 200, MaxIdleConnsPerHost: 100}}
	defer client.CloseIdleConnections()
	start := make(chan struct{})
	started := time.Now()
	until := started.Add(20 * time.Second)
	stopSampling, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				var waiting int64
				must(model.DB.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&waiting).Error)
				result.LockSamples++
				if waiting > 0 {
					result.SamplesWithLockWait++
				}
				result.PeakWaitingBackends = max(result.PeakWaitingBackends, waiting)
			}
		}
	}()
	var workers sync.WaitGroup
	for worker := range concurrency {
		workers.Go(func() {
			<-start
			accountIndex := worker % accountCount
			gateway := worker % 2
			port := []string{"18371", "18372"}[gateway]
			for time.Now().Before(until) {
				requestStarted := time.Now()
				n := clientActive.Add(1)
				for peak := clientPeak.Load(); n > peak; peak = clientPeak.Load() {
					if clientPeak.CompareAndSwap(peak, n) {
						break
					}
				}
				req, err := http.NewRequest("POST", "http://127.0.0.1:"+port+"/v1/chat/completions", strings.NewReader(`{"model":"mv2-capacity","messages":[{"role":"user","content":"test"}]}`))
				if err != nil {
					panic(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer sk-"+accounts[accountIndex].token.Key)
				response, err := client.Do(req)
				failure := ""
				if err != nil {
					failure = "transport"
				} else {
					data, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if readErr != nil {
						failure = "body_read"
					} else if response.StatusCode != 200 {
						failure = fmt.Sprintf("http_%d", response.StatusCode)
					} else if !strings.Contains(string(data), `"capacity-fixture"`) {
						failure = "invalid_response"
					}
				}
				elapsed := time.Since(requestStarted)
				clientActive.Add(-1)
				clientNanos.Add(int64(elapsed))
				mutex.Lock()
				durations = append(durations, float64(elapsed)/float64(time.Millisecond))
				result.GatewayRequests[gateway]++
				if failure == "" {
					result.Successes++
					perAccount[accountIndex]++
				} else {
					result.Errors[failure]++
				}
				mutex.Unlock()
			}
		})
	}
	close(start)
	workers.Wait()
	result.DurationSeconds = time.Since(started).Seconds()
	close(stopSampling)
	<-sampled
	sort.Float64s(durations)
	result.Requests = len(durations)
	check(len(durations) > 0, "capacity sent no requests")
	result.P50, result.P95, result.P99 = durations[(len(durations)-1)*50/100], durations[(len(durations)-1)*95/100], durations[(len(durations)-1)*99/100]
	result.Throughput = float64(result.Requests) / result.DurationSeconds
	result.SuccessThroughput = float64(result.Successes) / result.DurationSeconds
	result.ClientPeak, result.ProviderPeak, result.ProviderCalls = clientPeak.Load(), provider.peak.Load(), provider.calls.Load()
	result.ClientMean = float64(clientNanos.Load()) / float64(time.Second) / result.DurationSeconds
	result.ProviderMean = float64(provider.nanos.Load()) / float64(time.Second) / result.DurationSeconds
	result.AccountingVerified = true
	if int64(result.Successes) != result.ProviderCalls || len(result.Errors) != 0 {
		result.AccountingVerified = false
		result.AccountingFailure = "failed or ambiguous HTTP/provider outcome"
	}
	for i, account := range accounts {
		// HTTP bytes may reach the client just before the handler commits its
		// settlement/log. Drain that bounded tail before taking a ledger snapshot.
		eventually("capacity settlement drain", func() bool {
			var terminal, logged int64
			if model.DB.Model(&model.MarketingFunding{}).Where("user_id = ? AND state = ? AND reserved_quota = 0", account.user.Id, "settled").Count(&terminal).Error != nil {
				return false
			}
			if model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", account.user.Id, model.LogTypeConsume).Count(&logged).Error != nil {
				return false
			}
			return terminal >= int64(perAccount[i]) && logged >= int64(perAccount[i])
		})
		var user model.User
		var token model.Token
		var gift model.MarketingGift
		var holds model.MarketingTokenHold
		must(model.DB.First(&user, account.user.Id).Error)
		must(model.DB.First(&token, account.token.Id).Error)
		must(model.DB.First(&gift, account.gift.ID).Error)
		must(model.DB.First(&holds, "token_id = ?", token.Id).Error)
		cost := int64(perAccount[i]) * 500
		giftUsed := min(cost, int64(giftQuota))
		var ledger, allocations int64
		must(model.DB.Model(&model.MarketingLedgerEntry{}).Select("COALESCE(SUM(amount_quota),0)").Where("user_id = ? AND kind = ?", user.Id, "consume").Scan(&ledger).Error)
		must(model.DB.Model(&model.MarketingAllocation{}).Select("COALESCE(SUM(consumed_quota),0)").Where("funding_id IN (?)", model.DB.Model(&model.MarketingFunding{}).Select("id").Where("user_id = ?", user.Id)).Scan(&allocations).Error)
		var pending, fundings int64
		must(model.DB.Model(&model.MarketingFunding{}).Where("user_id = ?", user.Id).Count(&fundings).Error)
		must(model.DB.Model(&model.MarketingFunding{}).Where("user_id = ? AND (state <> ? OR reserved_quota <> 0)", user.Id, "settled").Count(&pending).Error)
		_, logs, err := model.GetUserLogs(user.Id, model.LogFilter{Type: model.LogTypeConsume}, 0, 1)
		must(err)
		if int64(user.Quota) != 2_000_000-(cost-giftUsed) || int64(user.UsedQuota) != cost || user.RequestCount != perAccount[i] || gift.ConsumedQuota != giftUsed || gift.RemainingQuota != int64(giftQuota)-giftUsed || gift.ReservedQuota != 0 || int64(token.RemainQuota) != 3_000_000-cost || int64(token.UsedQuota) != cost || holds.ReservedQuota != 0 || ledger != cost || allocations != cost || pending != 0 || fundings != int64(perAccount[i]) || logs != int64(perAccount[i]) {
			result.AccountingVerified = false
			result.AccountingFailure = fmt.Sprintf("user=%d expected_calls=%d expected_cost=%d paid=%d used=%d requests=%d gift_used=%d gift_remaining=%d gift_hold=%d token_remaining=%d token_used=%d token_hold=%d ledger=%d allocations=%d pending=%d fundings=%d logs=%d", user.Id, perAccount[i], cost, user.Quota, user.UsedQuota, user.RequestCount, gift.ConsumedQuota, gift.RemainingQuota, gift.ReservedQuota, token.RemainQuota, token.UsedQuota, holds.ReservedQuota, ledger, allocations, pending, fundings, logs)
		}
	}
	encoded, err := common.Marshal(result)
	must(err)
	fmt.Println("CAPACITY_RESULT " + string(encoded))
	return result
}
