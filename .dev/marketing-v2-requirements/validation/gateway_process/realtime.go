package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// Only the external provider is controlled. Both clients connect to the real
// gateway processes, whose coordinators use their shared PostgreSQL and Redis.
type realtimeGatewayFixture struct {
	connections atomic.Int32
	sessions    sync.Map
}

type realtimeProviderSession struct {
	conn                *websocket.Conn
	writeMu             sync.Mutex
	closed              chan struct{}
	sequence            int
	commits             int
	appends             atomic.Int32
	metadata            sync.Map
	omitCreatedMetadata atomic.Bool
	holdManual          atomic.Bool
	pendingManual       chan string
	pongs               chan string
}

func (s *realtimeProviderSession) send(message any) {
	data, err := common.Marshal(message)
	must(err)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	must(s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)))
	must(s.conn.WriteMessage(websocket.TextMessage, data))
}

func (f *realtimeGatewayFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	id := fmt.Sprintf("realtime-session-%d", f.connections.Add(1))
	session := &realtimeProviderSession{conn: conn, closed: make(chan struct{}), pendingManual: make(chan string, 1), pongs: make(chan string, 1)}
	conn.SetPongHandler(func(data string) error { session.pongs <- data; return nil })
	f.sessions.Store(id, session)
	defer conn.Close()
	defer close(session.closed)
	session.send(map[string]any{"type": "session.created", "session": map[string]any{"id": id, "model": "realtime-fixture", "input_audio_format": "pcm16", "output_audio_format": "pcm16", "turn_detection": map[string]any{"type": "server_vad", "threshold": 0.5, "prefix_padding_ms": 300, "silence_duration_ms": 500}, "input_audio_transcription": nil}})
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var event struct {
			Type     string `json:"type"`
			Response *struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"response"`
		}
		must(common.Unmarshal(raw, &event))
		switch event.Type {
		case "input_audio_buffer.append":
			session.appends.Add(1)
		case "input_audio_buffer.commit":
			session.commits++
			session.send(map[string]any{"type": "input_audio_buffer.committed", "item_id": fmt.Sprintf("%s-audio-%d", id, session.commits)})
		case "response.create":
			session.sequence++
			responseID := fmt.Sprintf("%s-response-%d", id, session.sequence)
			if event.Response != nil && event.Response.Metadata != nil {
				session.metadata.Store(responseID, event.Response.Metadata)
			}
			if session.holdManual.Load() {
				session.pendingManual <- responseID
			} else {
				session.created(responseID)
			}
		}
	}
}

func (s *realtimeProviderSession) created(responseID string) {
	response := map[string]any{"id": responseID, "status": "in_progress"}
	if metadata, ok := s.metadata.Load(responseID); ok && !s.omitCreatedMetadata.Load() {
		response["metadata"] = metadata
	}
	s.send(map[string]any{"type": "response.created", "response": response})
}

// A client-owned key deliberately collides with the preferred internal key.
// The gateway must preserve it while adding and later hiding its own marker.
const realtimeManualCreate = `{"type":"response.create","response":{"metadata":{"fixture_label":"preserve-me","_silvermoon_request":"client-owned"}}}`

func checkRealtimeClientMetadata(event map[string]any, required bool) {
	response := event["response"].(map[string]any)
	metadata, present := response["metadata"].(map[string]any)
	if !present && !required {
		return
	}
	check(present && len(metadata) == 2 && metadata["fixture_label"] == "preserve-me" && metadata["_silvermoon_request"] == "client-owned", "realtime metadata changed or internal correlation leaked")
}

func realtimeRead(conn *websocket.Conn, want string) map[string]any {
	must(conn.SetReadDeadline(time.Now().Add(15 * time.Second)))
	_, raw, err := conn.ReadMessage()
	must(err)
	var event map[string]any
	must(common.Unmarshal(raw, &event))
	check(event["type"] == want, fmt.Sprintf("expected realtime %s, got %s", want, raw))
	return event
}

func realtimeDial(f *realtimeGatewayFixture, port, key string) (*websocket.Conn, *realtimeProviderSession) {
	header := http.Header{"Authorization": []string{"Bearer sk-" + key}}
	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+port+"/v1/realtime?model=realtime-fixture", header)
	must(err)
	event := realtimeRead(conn, "session.created")
	sessionID := event["session"].(map[string]any)["id"].(string)
	value, ok := f.sessions.Load(sessionID)
	check(ok, "provider session identity missing")
	return conn, value.(*realtimeProviderSession)
}

func realtimeStartRound(conn *websocket.Conn) string {
	must(conn.WriteMessage(websocket.TextMessage, []byte(realtimeManualCreate)))
	event := realtimeRead(conn, "response.created")
	checkRealtimeClientMetadata(event, false)
	return event["response"].(map[string]any)["id"].(string)
}

func realtimeFinishRound(conn *websocket.Conn, provider *realtimeProviderSession, responseID string) {
	textTokens, audioTokens := 120, 0
	if provider.appends.Load() > 0 {
		textTokens, audioTokens = 0, 120
	}
	response := map[string]any{
		"id": responseID, "status": "completed", "usage": map[string]any{
			"total_tokens": 120, "input_tokens": 120, "output_tokens": 0,
			"input_token_details":  map[string]any{"text_tokens": textTokens, "audio_tokens": audioTokens, "cached_tokens": 0},
			"output_token_details": map[string]any{"text_tokens": 0, "audio_tokens": 0},
		},
	}
	metadata, manual := provider.metadata.Load(responseID)
	if manual {
		fields := metadata.(map[string]any)
		check(len(fields) == 3 && fields["fixture_label"] == "preserve-me" && fields["_silvermoon_request"] == "client-owned", "manual request correlation was not transmitted alongside original metadata")
		response["metadata"] = metadata
	}
	provider.send(map[string]any{"type": "response.done", "response": response})
	event := realtimeRead(conn, "response.done")
	if manual {
		checkRealtimeClientMetadata(event, true)
	}
}

type realtimeFixtureBudget struct{ PaidQuota, TokenQuota int }

func realtimeFixtureAccount(label string, giftQuota int, expiresAt int64, budgets ...realtimeFixtureBudget) (model.User, model.Token, model.MarketingGift) {
	user := model.User{Username: "realtime_" + label, AffCode: "rt_" + label, Status: common.UserStatusEnabled, Group: "default", Role: common.RoleCommonUser}
	token := model.Token{Key: fmt.Sprintf("realtime%-40s", label), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10000, Group: "default"}
	token.Key = strings.ReplaceAll(token.Key, " ", "0")
	var gift model.MarketingGift
	principal := 5000
	if len(budgets) > 0 {
		principal = budgets[0].PaidQuota
		token.RemainQuota = budgets[0].TokenQuota
	}
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		token.UserId = user.Id
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "realtime fixture registration"}).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		if _, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "realtime-" + label, PaidCents: 100, PostedAt: now, Money: model.MarketingMoneySnapshot{Amount: fmt.Sprint(principal), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}}); err != nil {
			return err
		}
		if giftQuota == 0 {
			return nil
		}
		stored, err := model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: "recharge", Reference: "realtime-" + label, GrantedAt: now, ExpiresAt: expiresAt, Money: model.MarketingMoneySnapshot{Amount: fmt.Sprint(giftQuota), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
		if err == nil {
			gift = *stored
		}
		return err
	}))
	return user, token, gift
}

func realtimeCloseAndCheck(conn *websocket.Conn, provider *realtimeProviderSession) {
	must(conn.Close())
	eventually("realtime actual upstream closed", func() bool {
		select {
		case <-provider.closed:
			return true
		default:
			return false
		}
	})
}

func realtimeSettled(userID int, count int, quota int64) []model.MarketingFunding {
	var rows []model.MarketingFunding
	eventually("realtime durable final settlement", func() bool {
		if model.DB.Where("user_id = ? AND realtime = ?", userID, true).Order("id").Find(&rows).Error != nil || len(rows) != count {
			return false
		}
		var sum int64
		for _, row := range rows {
			if !row.RealtimeClosed || row.State != "settled" || !row.CostKnown || row.ReservedQuota != 0 {
				return false
			}
			sum += row.SettledQuota
		}
		return sum == quota
	})
	return rows
}

func runRealtimeGatewayScenarios(f *realtimeGatewayFixture) {
	// Two separate processes reserve and settle the same user's limited gift.
	user, token, gift := realtimeFixtureAccount("parallel", 90, 0)
	clientA, providerA := realtimeDial(f, "18371", token.Key)
	clientB, providerB := realtimeDial(f, "18372", token.Key)
	gate := make(chan struct{})
	type started struct {
		conn     *websocket.Conn
		provider *realtimeProviderSession
		response string
		failure  any
	}
	startedRounds := make(chan started, 2)
	var clients sync.WaitGroup
	for _, pair := range []started{{conn: clientA, provider: providerA}, {conn: clientB, provider: providerB}} {
		clients.Go(func() {
			defer func() { pair.failure = recover(); startedRounds <- pair }()
			<-gate
			pair.response = realtimeStartRound(pair.conn)
		})
	}
	close(gate)
	clients.Wait()
	for range 2 {
		round := <-startedRounds
		check(round.failure == nil, fmt.Sprintf("concurrent realtime start failed: %v", round.failure))
		realtimeFinishRound(round.conn, round.provider, round.response)
	}
	realtimeCloseAndCheck(clientA, providerA)
	realtimeCloseAndCheck(clientB, providerB)
	realtimeSettled(user.Id, 2, 240)
	must(model.DB.First(&user, user.Id).Error)
	must(model.DB.First(&token, token.Id).Error)
	must(model.DB.First(&gift, gift.ID).Error)
	check(gift.ConsumedQuota == 90 && gift.RemainingQuota == 0 && gift.ReservedQuota == 0, "concurrent realtime gift overspent or leaked")
	check(user.Quota == 4850 && user.UsedQuota == 240, "concurrent realtime paid/gift charge mismatch")
	check(token.RemainQuota == 9760 && token.UsedQuota == 240, "concurrent realtime API token mismatch")
	fmt.Println("GATEWAY_REALTIME_OK case=shared_account_parallel two_processes=true gift=90 paid=150 actual=240")

	// Continuous chunks remain one input buffer and the first response starts
	// before expiry. A subsequent response on the same socket must requalify.
	expiresAt := common.GetTimestamp() + 5
	user, token, gift = realtimeFixtureAccount("expiry", 200, expiresAt)
	client, provider := realtimeDial(f, "18371", token.Key)
	// Three PCM16 chunks total 100 ms at 24 kHz, satisfying the provider's
	// minimum committed audio duration instead of inventing one-byte audio.
	audio := base64.StdEncoding.EncodeToString(make([]byte, 1600))
	for range 3 {
		must(client.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"input_audio_buffer.append","audio":%q}`, audio))))
	}
	// Manual audio responses consume a provider-confirmed committed buffer.
	// This fixture does not establish automatic VAD response association.
	must(client.WriteMessage(websocket.TextMessage, []byte(`{"type":"input_audio_buffer.commit"}`)))
	committed := realtimeRead(client, "input_audio_buffer.committed")
	itemID, ok := committed["item_id"].(string)
	check(ok && itemID != "", "provider did not confirm the committed audio item")
	first := realtimeStartRound(client)
	check(provider.appends.Load() == 3, "continuous audio chunks missing")
	var rounds []model.MarketingRealtimeRound
	var funding model.MarketingFunding
	must(model.DB.Where("user_id = ? AND realtime = ?", user.Id, true).First(&funding).Error)
	must(model.DB.Where("funding_id = ?", funding.ID).Find(&rounds).Error)
	check(len(rounds) == 1 && rounds[0].QualifiedAt < expiresAt, "audio chunks incorrectly became rounds or missed expiry boundary")
	eventually("realtime gift natural expiry", func() bool { return common.GetTimestamp() >= expiresAt })
	realtimeFinishRound(client, provider, first)
	second := realtimeStartRound(client)
	realtimeFinishRound(client, provider, second)
	realtimeCloseAndCheck(client, provider)
	realtimeSettled(user.Id, 1, 240)
	must(model.DB.First(&user, user.Id).Error)
	must(model.DB.First(&gift, gift.ID).Error)
	must(model.DB.Where("funding_id = ?", funding.ID).Order("id").Find(&rounds).Error)
	check(len(rounds) == 2 && rounds[1].QualifiedAt >= expiresAt, "second response retained old round qualification")
	check(gift.ConsumedQuota == 120 && gift.RemainingQuota == 80 && gift.ReservedQuota == 0 && user.Quota == 4880, "expired old-round gift/new-round paid sources wrong")
	fmt.Println("GATEWAY_REALTIME_OK case=round_expiry continuous_appends=3 rounds=2 gift=120 paid=120")

	runRealtimeAutomaticScenarios(f)
	runRealtimeExhaustionScenarios(f)
	runRealtimeRejectedStartScenarios(f)

	// An interrupted generation has unknown cost, not a synthetic zero bill.
	recoveryGiftExpiry := common.GetTimestamp() + 5
	user, token, gift = realtimeFixtureAccount("unknown", 200, recoveryGiftExpiry)
	client, provider = realtimeDial(f, "18371", token.Key)
	unknownResponse := realtimeStartRound(client)
	provider.send(map[string]any{"type": "response.output_text.delta", "response_id": unknownResponse, "delta": "An unfinished response with no final usage"})
	_ = realtimeRead(client, "response.output_text.delta")
	realtimeCloseAndCheck(client, provider)
	funding = model.MarketingFunding{}
	eventually("unknown realtime review", func() bool {
		return model.DB.Where("user_id = ? AND realtime = ?", user.Id, true).First(&funding).Error == nil && funding.RealtimeClosed && funding.State == "review"
	})
	check(!funding.CostKnown && funding.SettledQuota == 0 && funding.ReservedQuota > 0, "unknown realtime cost released as zero")
	before := f.connections.Load()
	independent, independentProvider := realtimeDial(f, "18372", token.Key)
	check(f.connections.Load() == before+1, "unresolved cost alone blocked an independent connection")
	realtimeCloseAndCheck(independent, independentProvider)
	eventually("independent empty connection settled", func() bool {
		var settled int64
		must(model.DB.Model(&model.MarketingFunding{}).Where("user_id = ? AND realtime = ? AND state = ?", user.Id, true, "settled").Count(&settled).Error)
		return settled == 1
	})
	must(model.DB.First(&user, user.Id).Error)
	must(model.DB.First(&token, token.Id).Error)
	check(user.Quota == 5000 && user.UsedQuota == 0 && token.RemainQuota == 10000 && token.UsedQuota == 0, "unknown realtime usage consumed an estimate")
	fmt.Println("GATEWAY_REALTIME_OK case=unknown_disconnect review=true other_process_allowed_with_available_funds=true actual_upstream_closed=true")

	// A separate trusted validation process supplies the provider's saved final
	// record to the internal reconciliation entry point. This is deliberately
	// NOT a gateway auto-retry, a provider resend, or a public recovery endpoint.
	eventually("gift expiry before trusted late reconciliation", func() bool { return common.GetTimestamp() >= recoveryGiftExpiry })
	metadata, ok := provider.metadata.Load(unknownResponse)
	check(ok, "original manual provider correlation missing")
	trustedProviderRecord, err := common.Marshal(map[string]any{"type": "response.done", "response": map[string]any{
		"id": unknownResponse, "status": "completed", "metadata": metadata, "usage": map[string]any{
			"total_tokens": 120, "input_tokens": 100, "output_tokens": 20,
			"input_token_details":  map[string]any{"text_tokens": 100, "audio_tokens": 0, "cached_tokens": 0},
			"output_token_details": map[string]any{"text_tokens": 20, "audio_tokens": 0},
		},
	}})
	must(err)
	recoveredAt := time.Now()
	recovered, err := service.ReconcileMarketingRealtimeResponse(user.Id, funding.ID, trustedProviderRecord, recoveredAt)
	must(err)
	check(recovered.ID == funding.ID && recovered.State == "settled" && recovered.CostKnown && recovered.SettledQuota == 120, "trusted original terminal did not settle original connection")
	_, err = service.ReconcileMarketingRealtimeResponse(user.Id, funding.ID, trustedProviderRecord, recoveredAt)
	must(err)
	must(model.DB.First(&user, user.Id).Error)
	must(model.DB.First(&token, token.Id).Error)
	must(model.DB.First(&gift, gift.ID).Error)
	rounds = nil
	must(model.DB.Where("funding_id = ?", funding.ID).Find(&rounds).Error)
	check(len(rounds) == 1 && rounds[0].QualifiedAt < recoveryGiftExpiry && rounds[0].Terminal && !rounds[0].Unknown, "trusted late record moved to a new qualifying round")
	check(gift.ConsumedQuota == 120 && gift.RemainingQuota == 80 && gift.ReservedQuota == 0 && user.Quota == 5000 && token.RemainQuota == 9880, "late reconciliation lost old gift eligibility or duplicated debit")
	newClient, newProvider := realtimeDial(f, "18372", token.Key)
	newResponse := realtimeStartRound(newClient)
	realtimeFinishRound(newClient, newProvider, newResponse)
	realtimeCloseAndCheck(newClient, newProvider)
	realtimeSettled(user.Id, 3, 240)
	must(model.DB.First(&gift, gift.ID).Error)
	must(model.DB.First(&user, user.Id).Error)
	must(model.DB.First(&token, token.Id).Error)
	check(gift.ConsumedQuota == 120 && user.Quota == 4880 && token.RemainQuota == 9760, "new connection reused expired gift or old reconciliation debt")
	fmt.Println("GATEWAY_REALTIME_OK case=trusted_internal_recovery process=validation_harness auto_gateway_recovery=false original_round=true original_gift=120 duplicate_idempotent=true other_gateway_reopens=true")
}

func runRealtimeAutomaticScenarios(f *realtimeGatewayFixture) {
	// The official VAD sequence commits an input item before its automatic
	// response. The item ID is not fabricated as a response correlation field.
	for _, pendingB := range []bool{false, true} {
		label := "vad_known"
		if pendingB {
			label = "vad_pending_b"
		}
		expiresAt := common.GetTimestamp() + 5
		user, token, gift := realtimeFixtureAccount(label, 200, expiresAt)
		client, provider := realtimeDial(f, "18372", token.Key)
		// Exercise official omitted defaults, then a partial provider-confirmed
		// update. Neither event supplies create_response or idle_timeout_ms.
		// This fixture drives protocol events; it does not infer an undocumented
		// input-item-to-response identity from the synthesized audio.
		provider.send(map[string]any{"type": "session.updated", "session": map[string]any{"turn_detection": map[string]any{"threshold": 0.6}}})
		realtimeRead(client, "session.updated")
		audio := base64.StdEncoding.EncodeToString(make([]byte, 4800))
		must(client.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"input_audio_buffer.append","audio":%q}`, audio))))
		eventually("VAD input A delivered", func() bool { return provider.appends.Load() == 1 })
		provider.send(map[string]any{"type": "input_audio_buffer.speech_started", "audio_start_ms": 0, "item_id": "vad-input-a"})
		realtimeRead(client, "input_audio_buffer.speech_started")
		provider.send(map[string]any{"type": "input_audio_buffer.speech_stopped", "audio_end_ms": 100, "item_id": "vad-input-a"})
		realtimeRead(client, "input_audio_buffer.speech_stopped")
		provider.send(map[string]any{"type": "input_audio_buffer.committed", "item_id": "vad-input-a", "previous_item_id": nil})
		realtimeRead(client, "input_audio_buffer.committed")
		provider.send(map[string]any{"type": "conversation.item.created", "item": map[string]any{"id": "vad-input-a", "type": "message", "role": "user", "content": []any{map[string]any{"type": "input_audio", "transcript": ""}}}})
		realtimeRead(client, "conversation.item.created")
		eventually("VAD gift expiry before automatic response observed", func() bool { return common.GetTimestamp() >= expiresAt })
		if pendingB {
			// B arrives after A was committed but before A's response is created.
			// A's known completion must not consume this separate current buffer.
			must(client.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"input_audio_buffer.append","audio":%q}`, audio))))
			eventually("VAD new buffer B delivered", func() bool { return provider.appends.Load() == 2 })
		}
		provider.created("vad-auto-response")
		realtimeRead(client, "response.created")
		realtimeFinishRound(client, provider, "vad-auto-response")
		realtimeCloseAndCheck(client, provider)
		var funding model.MarketingFunding
		if pendingB {
			eventually("VAD pending B review", func() bool {
				return model.DB.Where("user_id = ? AND realtime = ?", user.Id, true).First(&funding).Error == nil && funding.RealtimeClosed && funding.State == "review"
			})
			check(funding.SettledQuota == 120 && funding.ReservedQuota > 0 && !funding.CostKnown, "new buffer B was silently discharged by response A")
		} else {
			funding = realtimeSettled(user.Id, 1, 120)[0]
		}
		must(model.DB.First(&gift, gift.ID).Error)
		must(model.DB.First(&user, user.Id).Error)
		must(model.DB.First(&token, token.Id).Error)
		check(gift.ConsumedQuota == 0 && gift.ReservedQuota == 0 && user.Quota == 4880, "automatic VAD response inherited pre-expiry audio qualification")
		check(token.RemainQuota == 9880 && token.UsedQuota == 120, "automatic VAD response token usage disagrees with true cost")
		var rounds []model.MarketingRealtimeRound
		must(model.DB.Where("funding_id = ?", funding.ID).Find(&rounds).Error)
		check(len(rounds) == 1 && rounds[0].QualifiedAt >= expiresAt, "VAD input item incorrectly became old qualifying round")
		fmt.Printf("GATEWAY_REALTIME_OK case=%s automatic_qualification=first_created gift=0 paid=120 pending_b=%t state=%s\n", label, pendingB, funding.State)
	}

	// A connection opened before expiry is not itself a qualifying round.
	// An idle automatic response has only its first observed created event.
	expiresAt := common.GetTimestamp() + 5
	user, token, gift := realtimeFixtureAccount("idle_auto", 200, expiresAt)
	client, provider := realtimeDial(f, "18372", token.Key)
	eventually("idle automatic gift expiry", func() bool { return common.GetTimestamp() >= expiresAt })
	provider.created("idle-auto-response")
	realtimeRead(client, "response.created")
	realtimeFinishRound(client, provider, "idle-auto-response")
	realtimeCloseAndCheck(client, provider)
	rows := realtimeSettled(user.Id, 1, 120)
	must(model.DB.First(&user, user.Id).Error)
	must(model.DB.First(&gift, gift.ID).Error)
	check(user.Quota == 4880 && gift.ConsumedQuota == 0 && gift.ReservedQuota == 0, "idle automatic response inherited connection gift qualification")
	var rounds []model.MarketingRealtimeRound
	must(model.DB.Where("funding_id = ?", rows[0].ID).Find(&rounds).Error)
	check(len(rounds) == 1 && rounds[0].QualifiedAt >= expiresAt, "idle automatic response lacks its own observed start")
	fmt.Println("GATEWAY_REALTIME_OK case=idle_auto_expiry gift=0 paid=120 qualification=first_observed_created")

	// The manual start precedes expiry, while an automatic response is observed
	// afterwards, before the manual response is acknowledged. created metadata
	// is optional; only response.done has the documented metadata echo guarantee.
	expiresAt = common.GetTimestamp() + 5
	user, token, gift = realtimeFixtureAccount("interleaved", 200, expiresAt)
	client, provider = realtimeDial(f, "18371", token.Key)
	provider.holdManual.Store(true)
	provider.omitCreatedMetadata.Store(true)
	must(client.WriteMessage(websocket.TextMessage, []byte(realtimeManualCreate)))
	var manualResponse string
	select {
	case manualResponse = <-provider.pendingManual:
	case <-time.After(10 * time.Second):
		panic("manual response.create was not forwarded")
	}
	var funding model.MarketingFunding
	must(model.DB.Where("user_id = ? AND realtime = ?", user.Id, true).First(&funding).Error)
	rounds = nil
	must(model.DB.Where("funding_id = ?", funding.ID).Find(&rounds).Error)
	check(len(rounds) == 1 && rounds[0].QualifiedAt < expiresAt, "manual start was not qualified before expiry")
	manualRoundID := rounds[0].ID
	manualQualifiedAt := rounds[0].QualifiedAt
	eventually("interleaved gift expiry", func() bool { return common.GetTimestamp() >= expiresAt })
	provider.created("interleaved-auto-response")
	realtimeRead(client, "response.created")
	realtimeFinishRound(client, provider, "interleaved-auto-response")
	must(model.DB.First(&gift, gift.ID).Error)
	must(model.DB.First(&user, user.Id).Error)
	check(gift.ConsumedQuota == 0 && user.Quota == 4880, "automatic response stole pending manual qualification")
	provider.created(manualResponse)
	realtimeRead(client, "response.created")
	var autoRound, observedManual *model.MarketingRealtimeRound
	must(model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		autoRound, err = model.FindMarketingRealtimeRoundByResponse(tx, user.Id, funding.ID, "interleaved-auto-response")
		if err != nil {
			return err
		}
		observedManual, err = model.FindMarketingRealtimeRoundByResponse(tx, user.Id, funding.ID, manualResponse)
		return err
	}))
	check(observedManual.Observed && observedManual.ID != manualRoundID && observedManual.QualifiedAt >= expiresAt && observedManual.ResponseKey != "" && observedManual.ResponseOrder > autoRound.ResponseOrder, "metadata-free manual response did not preserve independent observed identity and order")
	realtimeFinishRound(client, provider, manualResponse)
	realtimeCloseAndCheck(client, provider)
	realtimeSettled(user.Id, 1, 240)
	must(model.DB.First(&gift, gift.ID).Error)
	must(model.DB.First(&user, user.Id).Error)
	check(gift.ConsumedQuota == 120 && gift.RemainingQuota == 80 && gift.ReservedQuota == 0 && user.Quota == 4880, "done metadata did not restore the original manual round qualification")
	// The durable manual intent aliases the observed canonical response. Follow
	// that link rather than treating the pre-response placeholder as a debit lot.
	var canonical model.MarketingRealtimeRound
	must(model.DB.Where("funding_id = ? AND id = ?", funding.ID, manualRoundID).First(&canonical).Error)
	check(canonical.SupersededBy != 0, "manual intent lost its durable canonical alias")
	seen := map[int64]bool{}
	for canonical.SupersededBy != 0 {
		check(!seen[canonical.ID], "realtime round alias cycle")
		seen[canonical.ID] = true
		next := canonical.SupersededBy
		canonical = model.MarketingRealtimeRound{}
		must(model.DB.Where("funding_id = ? AND id = ?", funding.ID, next).First(&canonical).Error)
	}
	check(canonical.ID == observedManual.ID && canonical.QualifiedAt == manualQualifiedAt && canonical.QualifiedAt < expiresAt, "canonical response did not retain original manual qualification")
	check(canonical.ResponseKey == observedManual.ResponseKey && canonical.ResponseOrder == observedManual.ResponseOrder && canonical.ResponseOrder > autoRound.ResponseOrder, "correlation changed canonical provider identity or response order")
	check(canonical.Terminal && !canonical.Unknown && canonical.UsageJSON != "", "canonical manual response lost trusted terminal usage")
	var allocations []model.MarketingAllocation
	must(model.DB.Where("funding_id = ? AND round_id = ?", funding.ID, canonical.ID).Find(&allocations).Error)
	var manualGift int64
	for _, allocation := range allocations {
		if allocation.GiftID == gift.ID {
			manualGift += allocation.ConsumedQuota
		}
	}
	check(manualGift == 120, "manual response charge was attached to another round")
	fmt.Println("GATEWAY_REALTIME_OK case=manual_auto_interleaved created_metadata=absent done_metadata=echoed gift_manual=120 paid_auto=120 client_metadata=preserved")
}

func runRealtimeExhaustionScenarios(f *realtimeGatewayFixture) {
	for _, known := range []bool{false, true} {
		label := "exhaust_unknown"
		if known {
			label = "exhaust_known"
		}
		user, token, gift := realtimeFixtureAccount(label, 100, 0, realtimeFixtureBudget{PaidQuota: 500, TokenQuota: 10000})
		client, provider := realtimeDial(f, "18371", token.Key)
		responseID := realtimeStartRound(client)
		var locked *gorm.DB
		if known {
			// Hold the same real row used by settlement while the socket reader
			// receives a terminal. Pong proves the earlier done frame was read.
			locked = model.DB.Begin()
			must(locked.Error)
			defer locked.Rollback()
			must(locked.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", gorm.Expr("quota")).Error)
		}
		provider.send(map[string]any{"type": "response.output_text.delta", "response_id": responseID, "delta": strings.Repeat("hello ", 900)})
		if known {
			metadata, _ := provider.metadata.Load(responseID)
			provider.send(map[string]any{"type": "response.done", "response": map[string]any{
				"id": responseID, "status": "completed", "metadata": metadata, "usage": map[string]any{
					"total_tokens": 1200, "input_tokens": 0, "output_tokens": 1200,
					"input_token_details":  map[string]any{"text_tokens": 0, "audio_tokens": 0, "cached_tokens": 0},
					"output_token_details": map[string]any{"text_tokens": 1200, "audio_tokens": 0},
				},
			}})
			must(provider.conn.WriteControl(websocket.PingMessage, []byte("terminal-read"), time.Now().Add(5*time.Second)))
			select {
			case pong := <-provider.pongs:
				check(pong == "terminal-read", "unexpected provider pong")
			case <-time.After(10 * time.Second):
				panic("real gateway reader did not queue terminal before settlement")
			}
			must(locked.Commit().Error)
		}
		eventually("funding stop closes real provider", func() bool {
			select {
			case <-provider.closed:
				return true
			default:
				return false
			}
		})
		// The test never closes the provider or client to trigger this stop.
		_ = client.Close()
		var funding model.MarketingFunding
		eventually("exhausted realtime review", func() bool {
			return model.DB.Where("user_id = ? AND realtime = ?", user.Id, true).First(&funding).Error == nil && funding.RealtimeClosed && funding.State == "review"
		})
		must(model.DB.First(&user, user.Id).Error)
		must(model.DB.First(&token, token.Id).Error)
		must(model.DB.First(&gift, gift.ID).Error)
		if known {
			check(funding.CostKnown && funding.ActualQuota == 1200 && funding.SettledQuota == 600, "queued true terminal lost or debt forgiven at exhaustion")
			check(user.Quota == 0 && gift.ConsumedQuota == 100 && token.RemainQuota == 9400, "exhaustion overdrew balances or missed actual paid sources")
		} else {
			check(!funding.CostKnown && funding.SettledQuota == 0 && funding.ReservedQuota > 0, "exhaustion without terminal charged or released an estimate")
			check(user.Quota == 500 && gift.ConsumedQuota == 0 && token.RemainQuota == 10000, "unknown exhausted response changed actual balances")
		}
		before := f.connections.Load()
		blocked, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:18372/v1/realtime?model=realtime-fixture", http.Header{"Authorization": []string{"Bearer sk-" + token.Key}})
		if err == nil {
			must(blocked.SetReadDeadline(time.Now().Add(10 * time.Second)))
			_, raw, readErr := blocked.ReadMessage()
			if readErr == nil {
				var event struct {
					Type string `json:"type"`
				}
				must(common.Unmarshal(raw, &event))
				check(event.Type == "error", "exhausted account generated on other gateway")
			} else {
				var timeout net.Error
				check(!errors.As(readErr, &timeout) || !timeout.Timeout(), "exhaustion rejection only timed out")
			}
			_ = blocked.Close()
		}
		check(f.connections.Load() == before, "exhausted account dispatched to actual provider on other instance")
		fmt.Printf("GATEWAY_REALTIME_OK case=%s actual_upstream_closed=true known=%t actual=%d settled=%d held=%d\n", label, funding.CostKnown, funding.ActualQuota, funding.SettledQuota, funding.ReservedQuota)
	}
}

// These cases exhaust a token through an ordinary successful response. They do
// not revoke credentials or alter wallet/token balances to force rejection.
func runRealtimeRejectedStartScenarios(f *realtimeGatewayFixture) {
	for _, scenario := range []struct {
		name                 string
		audio, output, known bool
	}{
		{name: "start_late"},
		{name: "text_zero", output: true},
		{name: "audio_zero", output: true, audio: true},
		{name: "text_known", output: true, known: true},
		{name: "audio_known", output: true, audio: true, known: true},
	} {
		user, token, _ := realtimeFixtureAccount("reject_"+scenario.name, 0, 0, realtimeFixtureBudget{PaidQuota: 5000, TokenQuota: 120})
		client, provider := realtimeDial(f, "18371", token.Key)
		first := realtimeStartRound(client)
		realtimeFinishRound(client, provider, first)
		must(model.DB.First(&token, token.Id).Error)
		check(token.RemainQuota == 0 && token.UsedQuota == 120, "first response did not naturally exhaust the token")
		var funding model.MarketingFunding
		must(model.DB.Where("user_id = ? AND realtime = ?", user.Id, true).First(&funding).Error)
		autoResponse := "automatic-unfunded-" + scenario.name
		textTokens, audioTokens := 5, 0
		if scenario.audio {
			textTokens, audioTokens = 0, 5
		}
		trustedTerminal := map[string]any{"type": "response.done", "response": map[string]any{
			"id": autoResponse, "status": "completed", "usage": map[string]any{
				"total_tokens": 5, "input_tokens": 0, "output_tokens": 5,
				"input_token_details":  map[string]any{"text_tokens": 0, "audio_tokens": 0, "cached_tokens": 0},
				"output_token_details": map[string]any{"text_tokens": textTokens, "audio_tokens": audioTokens},
			},
		}}
		var locked *gorm.DB
		if scenario.output {
			locked = model.DB.Begin()
			must(locked.Error)
			defer locked.Rollback()
			// Only pause processing: the unchanged row lets the real WS reader
			// queue created, output, terminal and pong before admission resumes.
			must(locked.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", gorm.Expr("quota")).Error)
		}
		provider.created(autoResponse)
		if scenario.output {
			eventType, delta := "response.output_text.delta", "hello"
			if scenario.audio {
				eventType, delta = "response.output_audio.delta", base64.StdEncoding.EncodeToString(make([]byte, 4800))
			}
			provider.send(map[string]any{"type": eventType, "response_id": autoResponse, "delta": delta})
			if scenario.known {
				provider.send(trustedTerminal)
			} else {
				provider.send(map[string]any{"type": "response.done", "response": map[string]any{
					"id": autoResponse, "status": "completed", "usage": map[string]any{
						"total_tokens": 0, "input_tokens": 0, "output_tokens": 0,
						"input_token_details":  map[string]any{"text_tokens": 0, "audio_tokens": 0, "cached_tokens": 0},
						"output_token_details": map[string]any{"text_tokens": 0, "audio_tokens": 0},
					},
				}})
			}
			must(provider.conn.WriteControl(websocket.PingMessage, []byte("rejected-start-read"), time.Now().Add(5*time.Second)))
			select {
			case pong := <-provider.pongs:
				check(pong == "rejected-start-read", "unexpected rejected-start pong")
			case <-time.After(10 * time.Second):
				panic("rejected-start provider frames were not read before close")
			}
			must(locked.Commit().Error)
		}
		eventually("rejected automatic start physically closes upstream", func() bool {
			select {
			case <-provider.closed:
				return true
			default:
				return false
			}
		})
		_ = client.Close()
		eventually("rejected automatic start durable review", func() bool {
			return model.DB.First(&funding, funding.ID).Error == nil && funding.RealtimeClosed && funding.State == "review"
		})
		var rejected *model.MarketingRealtimeRound
		must(model.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			rejected, err = model.FindMarketingRealtimeRoundByResponse(tx, user.Id, funding.ID, autoResponse)
			return err
		}))
		check(rejected.Observed && rejected.ResponseKey != "" && rejected.ResponseOrder == 2, "rejected start lost original provider identity or ordering")
		if scenario.output {
			check(rejected.OutputObserved && (!scenario.audio || rejected.AudioOutputObserved), "already-read rejected output evidence was discarded")
		}
		check(funding.SettledQuota == 120, "rejected response changed first round's settled charge")
		if scenario.known {
			check(funding.CostKnown && funding.ActualQuota == 125 && rejected.UsageJSON != "" && !rejected.Unknown, "read terminal lost true unpaid cost at rejected start")
		} else {
			check(!funding.CostKnown && rejected.Unknown, "missing or contradictory zero terminal cleared rejected generated output")
		}
		oldID, oldKey, oldOrder, oldQualified := rejected.ID, rejected.ResponseKey, rejected.ResponseOrder, rejected.QualifiedAt
		// No metadata exists for this automatic response. A new service
		// coordinator in this third process must recover solely its durable ID.
		raw, err := common.Marshal(trustedTerminal)
		must(err)
		for range 2 {
			recovered, recoverErr := service.ReconcileMarketingRealtimeResponse(user.Id, funding.ID, raw, time.Now())
			check(errors.Is(recoverErr, model.ErrMarketingReview), fmt.Sprintf("trusted rejected-start recovery must retain unpaid review: %v", recoverErr))
			check(recovered != nil && recovered.CostKnown && recovered.ActualQuota == 125 && recovered.SettledQuota == 120 && recovered.State == "review", "trusted late nonzero usage was erased or unpaid charge forgiven")
		}
		var stored model.MarketingRealtimeRound
		must(model.DB.First(&stored, oldID).Error)
		check(stored.ID == oldID && stored.ResponseKey == oldKey && stored.ResponseOrder == oldOrder && stored.QualifiedAt == oldQualified && stored.Terminal && !stored.Unknown && stored.UsageJSON != "", "late terminal recreated or requalified the rejected response")
		var roundCount int64
		must(model.DB.Model(&model.MarketingRealtimeRound{}).Where("funding_id = ?", funding.ID).Count(&roundCount).Error)
		check(roundCount == 2, "late rejected-start recovery created an extra round")
		must(model.DB.First(&user, user.Id).Error)
		must(model.DB.First(&token, token.Id).Error)
		check(user.Quota == 4880 && token.RemainQuota == 0 && token.UsedQuota == 120, "late terminal double-charged or overran exhausted token")
		// A fresh funded token proves this is the account-wide review gate,
		// rather than merely the exhausted original token's ordinary limit.
		freshToken := model.Token{UserId: user.Id, Key: fmt.Sprintf("reviewbypass%036d", user.Id), Name: "isolated review gate probe", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10000, Group: "default"}
		must(model.DB.Create(&freshToken).Error)
		before := f.connections.Load()
		blocked, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:18372/v1/realtime?model=realtime-fixture", http.Header{"Authorization": []string{"Bearer sk-" + freshToken.Key}})
		if err == nil {
			must(blocked.SetReadDeadline(time.Now().Add(10 * time.Second)))
			_, body, readErr := blocked.ReadMessage()
			if readErr == nil {
				var event struct {
					Type string `json:"type"`
				}
				must(common.Unmarshal(body, &event))
				check(event.Type == "error", "unpaid rejected response admitted a new connection")
			} else {
				var timeout net.Error
				check(!errors.As(readErr, &timeout) || !timeout.Timeout(), "rejected-start review only timed out")
			}
			_ = blocked.Close()
		}
		check(f.connections.Load() == before, "unpaid rejected start dispatched on second gateway")
		must(model.DB.First(&freshToken, freshToken.Id).Error)
		check(freshToken.RemainQuota == 10000 && freshToken.UsedQuota == 0, "account review bypass consumed the fresh token")
		fmt.Printf("GATEWAY_REALTIME_OK case=rejected_%s natural_token_exhaustion=true fresh_token_review_blocked=true actual_upstream_closed=true durable_identity=true output=%t audio=%t recovered_actual=125 settled=120 process=validation_harness no_metadata=true\n", scenario.name, scenario.output, scenario.audio)
	}
}
