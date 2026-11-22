// Runs two actual gateway executables against an isolated shared PostgreSQL
// and Redis. Only the external model provider is an HTTP fixture.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

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

func eventually(label string, condition func() bool) {
	deadline := time.NewTimer(55 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			panic("timeout: " + label)
		case <-ticker.C:
		}
	}
}

func launch(port string, poll bool) *exec.Cmd {
	cmd := exec.Command("/fixtures/marketing-gateway", "--port", port, "--log-dir", "/tmp/mv2-gateway-"+port)
	cmd.Env = append(os.Environ(), "NODE_TYPE=master", "PORT="+port, fmt.Sprintf("UPDATE_TASK=%t", poll), "SESSION_SECRET=isolated-gateway-process-secret", "CRYPTO_SECRET=isolated-gateway-process-secret", "BATCH_UPDATE_ENABLED=true", "MEMORY_CACHE_ENABLED=true", "GIN_MODE=release")
	log, err := os.Create("/fixtures/gateway-" + port + ".log")
	must(err)
	cmd.Stdout, cmd.Stderr = log, log
	must(cmd.Start())
	must(log.Close())
	fmt.Printf("GATEWAY_STARTED port=%s os_pid=%d\n", port, cmd.Process.Pid)
	eventually("gateway ready "+port, func() bool {
		resp, err := http.Get("http://127.0.0.1:" + port + "/api/status")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == 200
	})
	return cmd
}

func request(port, path, key, body string) []byte {
	req, err := http.NewRequest("POST", "http://127.0.0.1:"+port+path, strings.NewReader(body))
	must(err)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	must(err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	must(err)
	check(resp.StatusCode == 200, fmt.Sprintf("gateway %s %s: status=%d body=%s", port, path, resp.StatusCode, data))
	return data
}

func main() {
	check(strings.Contains(os.Getenv("SQL_DSN"), "/marketing_v2_gateway_process?"), "isolated gateway database required")
	check(os.Getenv("REDIS_CONN_STRING") == "redis://redis:6379/14", "isolated shared Redis DB 14 required")
	common.CryptoSecret = "isolated-gateway-process-secret"
	common.IsMasterNode = true
	must(model.InitDB())
	must(model.InitLogDB())
	must(common.InitRedisClient())
	defer common.RDB.Close()
	must(common.RDB.FlushDB(context.Background()).Err())
	var submitted, textCalls, imageCalls atomic.Int32
	var complete atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			data, _ := io.ReadAll(r.Body)
			if strings.Contains(string(data), `"usage-audit`) {
				usage := ""
				content := "hello"
				if strings.Contains(string(data), "zero-usage") {
					usage = `,"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`
				} else if strings.Contains(string(data), "partial-usage") {
					usage = `,"usage":{"completion_tokens":1,"total_tokens":1}`
				} else if strings.Contains(string(data), "prompt-only") {
					usage = `,"usage":{"prompt_tokens":4,"total_tokens":4}`
				} else if strings.Contains(string(data), "empty-output") {
					usage = `,"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}`
					content = ""
				}
				if strings.Contains(string(data), `"stream":true`) {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"id\":\"audit\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]%s}\n\ndata: [DONE]\n\n", content, usage)
				} else {
					fmt.Fprintf(w, `{"id":"audit","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]%s}`, content, usage)
				}
				return
			}
			textCalls.Add(1)
			if strings.Contains(string(data), `"stream":true`) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"fixture-chat\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
			} else {
				fmt.Fprint(w, `{"id":"fixture-chat","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
			}
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(make([]byte, 48000)) // One second of 24 kHz mono PCM16.
		case "/v1/images/generations":
			imageCalls.Add(1)
			fmt.Fprint(w, `{"created":1,"data":[{"b64_json":"aGVsbG8="}]}`)
		case "/mj/submit/imagine":
			submitted.Add(1)
			fmt.Fprint(w, `{"code":1,"result":"shared-mj-task"}`)
		case "/mj/task/list-by-condition":
			status, progress := "IN_PROGRESS", "10%"
			if complete.Load() {
				status, progress = "SUCCESS", "100%"
			}
			fmt.Fprintf(w, `[{"id":"shared-mj-task","status":%q,"progress":%q}]`, status, progress)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	user := model.User{Username: "gateway_fixture", AffCode: "gwfixture", Status: common.UserStatusEnabled, Group: "default", Role: common.RoleCommonUser}
	token := model.Token{Key: "gatewayfixturetoken000000000000000000000000000000", Name: "isolated fixture", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10000, Group: "default"}
	now := common.GetTimestamp()
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		token.UserId = user.Id
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "fixture registration"}).Error; err != nil {
			return err
		}
		if _, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "gateway-paid", PaidCents: 100, PostedAt: now, Money: model.MarketingMoneySnapshot{Amount: "5000", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}}); err != nil {
			return err
		}
		_, err := model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: "recharge", Reference: "gateway-gift", GrantedAt: now, Money: model.MarketingMoneySnapshot{Amount: "600", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
		return err
	}))
	for _, option := range []model.Option{{Key: "ModelRatio", Value: `{"usage-audit-token":1,"tts-1":1,"gpt-4o-mini-tts":1}`}, {Key: "ModelPrice", Value: `{"gpt-4o":0.001,"dall-e-3":0.002,"mj_imagine":0.003,"usage-audit-fixed":0.001}`}} {
		must(model.DB.Create(&option).Error)
	}
	base := upstream.URL
	for _, ch := range []model.Channel{{Name: "fixture-openai", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "fixture-upstream-key", BaseURL: &base, Models: "gpt-4o,dall-e-3,usage-audit-token,usage-audit-fixed,tts-1,gpt-4o-mini-tts", Group: "default"}, {Name: "fixture-mj", Type: constant.ChannelTypeMidjourney, Status: common.ChannelStatusEnabled, Key: "fixture-mj-key", BaseURL: &base, Models: "mj_imagine", Group: "default"}} {
		must(ch.Insert())
	}
	a := launch("18371", false)
	defer func() { _ = a.Process.Kill(); _ = a.Wait() }()
	b := launch("18372", true)
	defer func() { _ = b.Process.Kill(); _ = b.Wait() }()
	for index, scenario := range []struct {
		name                 string
		stream, fixed, audio bool
	}{
		{name: "missing-usage"}, {name: "missing-usage-stream", stream: true},
		{name: "zero-usage"}, {name: "partial-usage"},
		{name: "prompt-only"}, {name: "prompt-only-stream", stream: true}, {name: "empty-output"},
		{name: "fixed-missing-usage", fixed: true},
		{name: "measured-audio", audio: true}, {name: "estimated-audio", audio: true},
	} {
		auditUser := model.User{Username: fmt.Sprintf("usage_audit_%d", index), AffCode: fmt.Sprintf("audit%d", index), Status: common.UserStatusEnabled, Group: "default", Role: common.RoleCommonUser}
		must(model.DB.Create(&auditUser).Error)
		auditToken := model.Token{UserId: auditUser.Id, Key: fmt.Sprintf("usageaudittoken%034d", index), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10000, Group: "default"}
		must(model.DB.Create(&auditToken).Error)
		must(model.DB.Create(&model.MarketingAccount{UserID: auditUser.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error)
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: auditUser.Id, Kind: "online", Reference: scenario.name, PaidCents: 100, PostedAt: now, Money: model.MarketingMoneySnapshot{Amount: "5000", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
			return err
		}))
		modelName := "usage-audit-token"
		if scenario.fixed {
			modelName = "usage-audit-fixed"
		}
		if scenario.audio {
			modelName = "tts-1"
			if scenario.name == "estimated-audio" {
				modelName = "gpt-4o-mini-tts"
			}
			request("18372", "/v1/audio/speech", auditToken.Key, fmt.Sprintf(`{"model":%q,"input":"test","voice":"alloy","response_format":"pcm"}`, modelName))
		} else {
			request("18372", "/v1/chat/completions", auditToken.Key, fmt.Sprintf(`{"model":%q,"stream":%t,"messages":[{"role":"user","content":%q}]}`, modelName, scenario.stream, scenario.name))
		}
		var funding model.MarketingFunding
		eventually("funding terminal evidence "+scenario.name, func() bool {
			return model.DB.Where("user_id = ?", auditUser.Id).First(&funding).Error == nil && funding.FinalObserved
		})
		must(model.DB.First(&auditToken, auditToken.Id).Error)
		if scenario.name == "empty-output" {
			check(funding.State == "settled" && funding.CostKnown && funding.SettledQuota == 4, "legitimate zero output must retain input charge")
			check(auditToken.RemainQuota == 9996, "zero output input token charge")
		} else if scenario.name == "measured-audio" {
			check(funding.State == "settled" && funding.CostKnown && funding.SettledQuota > 0, "measured character and PCM duration price must settle")
			check(int64(auditToken.RemainQuota) == 10000-funding.SettledQuota, "measured audio token charge")
		} else if scenario.fixed {
			check(funding.State == "settled" && funding.CostKnown && funding.SettledQuota == 500, "fixed request must retain its confirmed price")
			check(auditToken.RemainQuota == 9500, "fixed request token charge")
		} else {
			check(funding.State == "review" && !funding.CostKnown && funding.SettledQuota == 0 && funding.ReservedQuota > 0, "estimated usage must retain unresolved funds")
			var hold model.MarketingTokenHold
			must(model.DB.Where("token_id = ?", auditToken.Id).First(&hold).Error)
			check(auditToken.RemainQuota == 10000 && hold.ReservedQuota == funding.ReservedQuota && auditToken.UsedQuota == 0, "unknown usage token hold must remain")
		}
		fmt.Printf("GATEWAY_USAGE_PROVENANCE_OK case=%s known=%t state=%s\n", scenario.name, funding.CostKnown, funding.State)
	}
	request("18371", "/v1/chat/completions", token.Key, `{"model":"gpt-4o","messages":[{"role":"user","content":"test"}]}`)
	request("18372", "/v1/chat/completions", token.Key, `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"test"}]}`)
	request("18371", "/v1/images/generations", token.Key, `{"model":"dall-e-3","prompt":"test","n":1,"size":"1024x1024","quality":"standard"}`)
	data := request("18371", "/mj/submit/imagine", token.Key, `{"prompt":"test"}`)
	check(strings.Contains(string(data), "shared-mj-task"), "accepted task identity missing")
	var task model.Midjourney
	must(model.DB.Where("mj_id = ?", "shared-mj-task").First(&task).Error)
	check(task.MarketingFundingID > 0 && task.MarketingBillingPending, "accepted task must hold durable funds")
	must(a.Process.Kill())
	_ = a.Wait()
	fmt.Println("GATEWAY_KILLED_AFTER_ACCEPT task_durable=true")
	complete.Store(true)
	eventually("other gateway settles accepted task", func() bool {
		var stored model.Midjourney
		return model.DB.First(&stored, task.Id).Error == nil && stored.Status == "SUCCESS" && !stored.MarketingBillingPending
	})
	must(model.DB.First(&user, user.Id).Error)
	must(model.DB.First(&token, token.Id).Error)
	var gift model.MarketingGift
	must(model.DB.Where("user_id = ?", user.Id).First(&gift).Error)
	check(user.UsedQuota == 3500, fmt.Sprintf("actual usage=%d expected=3500", user.UsedQuota))
	check(user.Quota == 2100 && gift.ConsumedQuota == 600 && gift.RemainingQuota == 0, "paid/gift sources do not reconcile")
	check(token.RemainQuota == 6500 && token.UsedQuota == 3500, "API token does not reconcile")
	check(user.RequestCount == 4, "request summaries duplicated")
	rows, count, err := model.GetUserLogs(user.Id, model.LogFilter{}, 0, 10)
	must(err)
	check(count == 4 && len(rows) == 4, "usage logs split or lost")
	check(submitted.Load() == 1 && textCalls.Load() == 2 && imageCalls.Load() == 1, "unexpected upstream resend")
	fmt.Println("GATEWAY_PROCESS_MATRIX_OK two_actual_gateways shared_postgresql shared_redis_db_14 text_stream_image_async_crash_recovery")
}
