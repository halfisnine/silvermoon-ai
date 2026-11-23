package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixture uses real websocket connections on both sides of the gateway.
// Only the coordinator is substituted: its accounting is tested with real
// databases in service, while this fixture controls transport interleavings.
func managedRealtimeWSPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			accepted <- conn
		}
	}))
	t.Cleanup(server.Close)
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Close() })
	select {
	case gateway := <-accepted:
		t.Cleanup(func() { _ = gateway.Close() })
		return gateway, peer
	case <-time.After(5 * time.Second):
		t.Fatal("websocket upgrade did not finish")
		return nil, nil
	}
}

type managedRealtimeProbe struct {
	clientEvents  []string
	serverEvents  []string
	usage         dto.RealtimeUsage
	closeCalls    int
	closeReason   string
	clientHook    func() error
	clientRewrite func([]byte) []byte
	serverRewrite func([]byte) []byte
	serverHook    func() error
	inCall        atomic.Bool
	concurrent    atomic.Bool
}

func (p *managedRealtimeProbe) ProcessClientEvent(raw []byte, _ time.Time) ([]byte, error) {
	if p.inCall.Swap(true) {
		p.concurrent.Store(true)
	}
	defer p.inCall.Store(false)
	p.clientEvents = append(p.clientEvents, string(raw))
	if p.clientHook != nil {
		if err := p.clientHook(); err != nil {
			return nil, err
		}
	}
	if p.clientRewrite != nil {
		return p.clientRewrite(raw), nil
	}
	return raw, nil
}
func (p *managedRealtimeProbe) ProcessServerEvent(raw []byte, _ time.Time) ([]byte, error) {
	if p.inCall.Swap(true) {
		p.concurrent.Store(true)
	}
	defer p.inCall.Store(false)
	p.serverEvents = append(p.serverEvents, string(raw))
	var event dto.RealtimeEvent
	if err := common.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	if event.Response != nil && event.Response.Usage != nil {
		p.usage = *event.Response.Usage
	}
	if p.serverHook != nil {
		if err := p.serverHook(); err != nil {
			return nil, err
		}
	}
	if p.serverRewrite != nil {
		return p.serverRewrite(raw), nil
	}
	return raw, nil
}
func (p *managedRealtimeProbe) Close(reason string) error {
	if p.inCall.Swap(true) {
		p.concurrent.Store(true)
	}
	defer p.inCall.Store(false)
	p.closeCalls++
	p.closeReason = reason
	return nil
}
func (p *managedRealtimeProbe) Usage() *dto.RealtimeUsage { return &p.usage }

type managedRealtimeResult struct {
	err   *types.NewAPIError
	usage *dto.RealtimeUsage
}

func startManagedRealtimeFixture(t *testing.T, probe *managedRealtimeProbe, configure ...func(*websocket.Conn)) (*websocket.Conn, *websocket.Conn, *websocket.Conn, context.CancelFunc, <-chan managedRealtimeResult) {
	t.Helper()
	gatewayClient, client := managedRealtimeWSPair(t)
	gatewayUpstream, upstream := managedRealtimeWSPair(t)
	for _, configureConn := range configure {
		configureConn(gatewayUpstream)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{ClientWs: gatewayClient, TargetWs: gatewayUpstream}
	done := make(chan managedRealtimeResult, 1)
	go func() {
		err, usage := relayMarketingRealtime(c, info, probe)
		done <- managedRealtimeResult{err: err, usage: usage}
	}()
	return client, upstream, gatewayUpstream, cancel, done
}

func awaitManagedRealtime(t *testing.T, done <-chan managedRealtimeResult) managedRealtimeResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("transport failed to close and join its readers")
		return managedRealtimeResult{}
	}
}

func assertManagedRealtimeClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err := conn.ReadMessage()
	require.Error(t, err)
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) {
		assert.False(t, netErr.Timeout(), "actual websocket must close, not merely stop forwarding")
	}
}

const managedRealtimeTerminal = `{"type":"response.done","response":{"id":"r1","status":"completed","usage":{"total_tokens":6,"input_tokens":4,"output_tokens":2}}}`

func TestManagedRealtimeForwardsContinuousAudioWithoutLocalSettlement(t *testing.T) {
	probe := &managedRealtimeProbe{}
	client, upstream, _, _, done := startManagedRealtimeFixture(t, probe)
	events := []string{
		`{"type":"input_audio_buffer.append","audio":"AA=="}`,
		`{"type":"input_audio_buffer.append","audio":"AQ=="}`,
		`{"type":"input_audio_buffer.append","audio":"Ag=="}`,
		`{"type":"input_audio_buffer.commit"}`,
		`{"type":"response.create"}`,
	}
	for _, event := range events {
		require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(event)))
		require.NoError(t, upstream.SetReadDeadline(time.Now().Add(5*time.Second)))
		kind, forwarded, err := upstream.ReadMessage()
		require.NoError(t, err)
		assert.Equal(t, websocket.TextMessage, kind)
		assert.Equal(t, event, string(forwarded))
	}
	require.NoError(t, upstream.WriteMessage(websocket.TextMessage, []byte(managedRealtimeTerminal)))
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, forwarded, err := client.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, managedRealtimeTerminal, string(forwarded))
	require.NoError(t, client.Close())
	result := awaitManagedRealtime(t, done)
	require.Nil(t, result.err)
	assert.Equal(t, events, probe.clientEvents)
	assert.Equal(t, []string{managedRealtimeTerminal}, probe.serverEvents)
	assert.Equal(t, 6, result.usage.TotalTokens)
	assert.Equal(t, 1, probe.closeCalls)
	assert.False(t, probe.concurrent.Load())
	assertManagedRealtimeClosed(t, upstream)
}

func TestManagedRealtimeCoordinatorFailureStopsActualConnections(t *testing.T) {
	for _, side := range []string{"client", "server"} {
		t.Run(side, func(t *testing.T) {
			probe := &managedRealtimeProbe{}
			reject := func() error { return errors.New("funding unavailable") }
			if side == "client" {
				probe.clientHook = reject
			} else {
				probe.serverHook = reject
			}
			client, upstream, _, _, done := startManagedRealtimeFixture(t, probe)
			if side == "client" {
				require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create"}`)))
			} else {
				require.NoError(t, upstream.WriteMessage(websocket.TextMessage, []byte(managedRealtimeTerminal)))
			}
			result := awaitManagedRealtime(t, done)
			require.NotNil(t, result.err)
			assert.Equal(t, 1, probe.closeCalls)
			assert.False(t, probe.concurrent.Load())
			if side == "server" {
				assert.Equal(t, 6, result.usage.TotalTokens)
			}
			assertManagedRealtimeClosed(t, upstream)
			assertManagedRealtimeClosed(t, client)
		})
	}
}

func TestManagedRealtimeCancellationJoinsBlockedReaders(t *testing.T) {
	probe := &managedRealtimeProbe{}
	client, upstream, _, cancel, done := startManagedRealtimeFixture(t, probe)
	cancel()
	result := awaitManagedRealtime(t, done)
	require.Nil(t, result.err)
	assert.Equal(t, 1, probe.closeCalls)
	assert.Empty(t, probe.clientEvents)
	assert.Empty(t, probe.serverEvents)
	assertManagedRealtimeClosed(t, upstream)
	assertManagedRealtimeClosed(t, client)
}

func TestManagedRealtimeDrainsReadTerminalBeforeClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	probe := &managedRealtimeProbe{clientHook: func() error {
		close(entered)
		<-release
		return nil
	}}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	queued := make(chan struct{}, 1)
	// This ping follows the terminal on the same real connection. The read
	// loop can process it only after placing the terminal in its queue.
	client, upstream, _, cancel, done := startManagedRealtimeFixture(t, probe, func(conn *websocket.Conn) {
		conn.SetPingHandler(func(string) error { queued <- struct{}{}; return nil })
	})
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create"}`)))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator did not receive client event")
	}
	require.NoError(t, upstream.WriteMessage(websocket.TextMessage, []byte(managedRealtimeTerminal)))
	require.NoError(t, upstream.WriteControl(websocket.PingMessage, []byte("after-terminal"), time.Now().Add(time.Second)))
	select {
	case <-queued:
	case <-time.After(5 * time.Second):
		t.Fatal("provider terminal was not read")
	}
	cancel()
	releaseOnce.Do(func() { close(release) })
	result := awaitManagedRealtime(t, done)
	assert.Equal(t, []string{managedRealtimeTerminal}, probe.serverEvents)
	assert.Equal(t, 6, result.usage.TotalTokens)
	assert.Equal(t, 1, probe.closeCalls)
	assert.False(t, probe.concurrent.Load())
	assertManagedRealtimeClosed(t, upstream)
	assertManagedRealtimeClosed(t, client)
}

func TestManagedRealtimeForwardsCoordinatorRewrittenBytes(t *testing.T) {
	clientPayload := `{"type":"response.create","response":{"metadata":{"label":"user"}}}`
	upstreamPayload := `{"type":"response.create","response":{"metadata":{"label":"user","_silvermoon_request":"issued-marker"}}}`
	upstreamReply := `{"type":"response.created","response":{"id":"r1","metadata":{"label":"user","_silvermoon_request":"issued-marker"}}}`
	clientReply := `{"type":"response.created","response":{"id":"r1","metadata":{"label":"user"}}}`
	probe := &managedRealtimeProbe{
		clientRewrite: func([]byte) []byte { return []byte(upstreamPayload) },
		serverRewrite: func([]byte) []byte { return []byte(clientReply) },
	}
	client, upstream, _, _, done := startManagedRealtimeFixture(t, probe)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(clientPayload)))
	require.NoError(t, upstream.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, actual, err := upstream.ReadMessage()
	require.NoError(t, err)
	assert.JSONEq(t, upstreamPayload, string(actual))
	require.NoError(t, upstream.WriteMessage(websocket.TextMessage, []byte(upstreamReply)))
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, actual, err = client.ReadMessage()
	require.NoError(t, err)
	assert.JSONEq(t, clientReply, string(actual))
	require.NoError(t, client.Close())
	result := awaitManagedRealtime(t, done)
	require.Nil(t, result.err)
	assert.Equal(t, []string{clientPayload}, probe.clientEvents)
	assert.Equal(t, []string{upstreamReply}, probe.serverEvents)
	assert.Equal(t, 1, probe.closeCalls)
}
