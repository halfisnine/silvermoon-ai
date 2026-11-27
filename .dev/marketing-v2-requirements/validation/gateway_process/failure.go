package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// External failures exercise the actual relay/refund/retry controller on two
// gateway processes. The account, dispatch and source ledgers are real shared DBs.
func runGatewayFailures() {
	var mu sync.Mutex
	calls := map[string]int{}
	var expiry int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		must(err)
		var payload struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		must(common.Unmarshal(data, &payload))
		name := "task-bad-receipt"
		if len(payload.Messages) > 0 {
			name = payload.Messages[0].Content
		}
		mu.Lock()
		calls[name]++
		attempt := calls[name]
		expiresAt := expiry
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case name == "task-bad-receipt":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"code":1,"result":`)
		case strings.HasPrefix(name, "503") || name == "expired":
			if name == "expired" {
				eventually("gift expired before failure", func() bool { return common.GetTimestamp() >= expiresAt })
			}
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"code":"quota_exhausted","message":"当前服务暂不可用，请稍后重试。","param":null,"type":"server_error"}}`)
		case name == "400":
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"code":"invalid_request","message":"fixture invalid request","type":"invalid_request_error"}}`)
		case name == "500" || (name == "retry" && attempt == 1):
			w.WriteHeader(500)
			fmt.Fprint(w, `{"error":{"code":"server_error","message":"fixture transient error","type":"server_error"}}`)
		case name == "network":
			conn, _, err := w.(http.Hijacker).Hijack()
			must(err)
			must(conn.Close())
		case name == "malformed":
			fmt.Fprint(w, `{"choices":`)
		case name == "empty":
			return
		default:
			fmt.Fprint(w, `{"id":"failure-fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
		}
	}))
	defer upstream.Close()
	for _, option := range []model.Option{{Key: "ModelPrice", Value: `{"gpt-4o":0.001,"mj_imagine":0.001}`}, {Key: "RetryTimes", Value: "2"}} {
		must(model.DB.Create(&option).Error)
	}
	base := upstream.URL
	for _, ch := range []model.Channel{
		{Name: "failure-openai", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "fixture", AutoBan: common.GetPointer(0), BaseURL: &base, Models: "gpt-4o", Group: "default"},
		{Name: "failure-mj", Type: constant.ChannelTypeMidjourney, Status: common.ChannelStatusEnabled, Key: "fixture", AutoBan: common.GetPointer(0), BaseURL: &base, Models: "mj_imagine", Group: "default"},
	} {
		must(ch.Insert())
	}
	a, b := launch("18371", false), launch("18372", false)
	defer func() { _ = a.Process.Kill(); _ = a.Wait(); _ = b.Process.Kill(); _ = b.Wait() }()
	for index, scenario := range []struct {
		name string
		gift int
	}{
		{"503-gift", 600}, {"503-mixed", 200}, {"503-paid", 0},
		{"400", 200}, {"500", 200}, {"network", 200}, {"malformed", 200}, {"empty", 200},
		{"retry", 200}, {"expired", 600}, {"task-bad-receipt", 200},
	} {
		expiresAt := int64(0)
		if scenario.name == "expired" {
			expiresAt = common.GetTimestamp() + 3
			mu.Lock()
			expiry = expiresAt
			mu.Unlock()
		}
		user, token, gift := realtimeFixtureAccount(fmt.Sprintf("failure%dcase", index), scenario.gift, expiresAt)
		path := "/v1/chat/completions"
		body := fmt.Sprintf(`{"model":"gpt-4o","messages":[{"role":"user","content":%q}]}`, scenario.name)
		if scenario.name == "task-bad-receipt" {
			path, body = "/mj/submit/imagine", `{"prompt":"test"}`
		}
		req, err := http.NewRequest("POST", "http://127.0.0.1:18371"+path, strings.NewReader(body))
		must(err)
		req.Header.Set("Authorization", "Bearer sk-"+token.Key)
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 20 * time.Second}
		resp, err := client.Do(req)
		must(err)
		result, err := io.ReadAll(resp.Body)
		must(err)
		must(resp.Body.Close())
		if scenario.name == "retry" {
			check(resp.StatusCode == 200, fmt.Sprintf("retry failed: %d %s", resp.StatusCode, result))
		} else {
			check(resp.StatusCode != 200 && resp.StatusCode != 403, fmt.Sprintf("wrong failure status case=%s status=%d body=%s", scenario.name, resp.StatusCode, result))
		}
		var funding model.MarketingFunding
		eventually("failure finalized "+scenario.name, func() bool {
			return model.DB.Where("user_id = ?", user.Id).First(&funding).Error == nil && funding.FinalObserved
		})
		mu.Lock()
		attempts := calls[scenario.name]
		mu.Unlock()
		check(attempts > 0, "failure did not reach upstream")
		if scenario.name == "task-bad-receipt" {
			check(attempts == 1 && funding.State == "review" && funding.ReservedQuota > 0 && !funding.CostKnown, "ambiguous accepted task retried/refunded")
		} else {
			check(funding.State == "settled" && funding.CostKnown && funding.ReservedQuota == 0, "ordinary failure retained review/holds")
			if scenario.name == "retry" {
				check(attempts == 2 && funding.SettledQuota == 500, "retry did not settle once")
			} else {
				check(funding.SettledQuota == 0, "failed request charged")
			}
		}
		// Another real process must admit the next request; unknown task holds
		// remain isolated and must not be spent again by the ordinary request.
		request("18372", "/v1/chat/completions", token.Key, `{"model":"gpt-4o","messages":[{"role":"user","content":"success"}]}`)
		eventually("other process settled follow-up", func() bool {
			var next model.MarketingFunding
			return model.DB.Where("user_id = ? AND id <> ?", user.Id, funding.ID).First(&next).Error == nil && next.State == "settled"
		})
		must(model.DB.First(&user, user.Id).Error)
		must(model.DB.First(&token, token.Id).Error)
		spent := 500
		if scenario.name == "retry" {
			spent = 1000
		}
		giftSpent := min(scenario.gift, spent)
		if scenario.name == "expired" || scenario.name == "task-bad-receipt" {
			giftSpent = 0
		}
		check(user.Quota == 5000-(spent-giftSpent) && user.UsedQuota == spent, "paid charge/source accounting mismatch")
		check(token.RemainQuota == 10000-spent && token.UsedQuota == spent, "token double refund or charge")
		if scenario.gift > 0 {
			must(model.DB.First(&gift, gift.ID).Error)
			check(gift.ConsumedQuota == int64(giftSpent), "gift double spend or expired gift reused")
		}
		fmt.Printf("GATEWAY_FAILURE_OK case=%s attempts=%d state=%s next_other_process=success paid=%d gift_spent=%d\n", scenario.name, attempts, funding.State, user.Quota, giftSpent)
	}
}
