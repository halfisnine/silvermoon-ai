package openai

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// All coordinator methods belong to the transport's single event loop. Readers
// never parse events, change funding, or perform a separate refund on exit.
type marketingRealtimeCoordinator interface {
	ProcessClientEvent([]byte, time.Time) ([]byte, error)
	ProcessServerEvent([]byte, time.Time) ([]byte, error)
	Close(string) error
	Usage() *dto.RealtimeUsage
}

type marketingRealtimeMessage struct {
	kind int
	body []byte
	at   time.Time
	err  error
}

func readMarketingRealtime(conn *websocket.Conn, messages chan<- marketingRealtimeMessage) {
	defer close(messages)
	defer func() {
		if recover() != nil {
			messages <- marketingRealtimeMessage{err: errors.New("realtime reader failed")}
		}
	}()
	for {
		kind, body, err := conn.ReadMessage()
		messages <- marketingRealtimeMessage{kind: kind, body: body, at: time.Now(), err: err}
		if err != nil {
			return
		}
	}
}

func openaiManagedRealtimeHandler(c *gin.Context, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.RealtimeUsage) {
	coordinator, err := service.NewMarketingRealtimeCoordinator(c, info)
	if err != nil {
		_ = info.TargetWs.Close()
		_ = info.ClientWs.Close()
		return types.NewError(err, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry()), nil
	}
	return relayMarketingRealtime(c, info, coordinator)
}

func relayMarketingRealtime(c *gin.Context, info *relaycommon.RelayInfo, coordinator marketingRealtimeCoordinator) (*types.NewAPIError, *dto.RealtimeUsage) {
	client, upstream := info.ClientWs, info.TargetWs
	ctx := context.Background()
	if c.Request != nil {
		ctx = c.Request.Context()
	}
	var closeOnce sync.Once
	closeSockets := func() {
		closeOnce.Do(func() {
			// Closing the actual upstream connection stops further generation and
			// interrupts both blocked readers and writes, before any settlement.
			_ = upstream.Close()
			_ = client.Close()
		})
	}
	stopCancellation := context.AfterFunc(ctx, closeSockets)
	defer stopCancellation()

	clientInbox := make(chan marketingRealtimeMessage, 32)
	serverInbox := make(chan marketingRealtimeMessage, 32)
	clientMessages, serverMessages := clientInbox, serverInbox
	var readers sync.WaitGroup
	readers.Go(func() { readMarketingRealtime(client, clientInbox) })
	readers.Go(func() { readMarketingRealtime(upstream, serverInbox) })
	idleTimeout := time.Duration(constant.StreamingTimeout) * time.Second
	var idle *time.Timer
	var idleExpired <-chan time.Time
	if idleTimeout > 0 {
		idle = time.NewTimer(idleTimeout)
		idleExpired = idle.C
		defer idle.Stop()
	}
	var relayErr error
	reason := "connection_closed"

relayLoop:
	for {
		select {
		case message, ok := <-clientMessages:
			if !ok || message.err != nil {
				reason = "client_closed"
				break relayLoop
			}
			if ctx.Err() != nil {
				reason = "request_cancelled"
				break relayLoop
			}
			outgoing, err := coordinator.ProcessClientEvent(message.body, message.at)
			if err != nil {
				relayErr, reason = err, "client_event_rejected"
				break relayLoop
			}
			if ctx.Err() != nil {
				reason = "request_cancelled"
				break relayLoop
			}
			if err := upstream.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
				relayErr, reason = err, "upstream_write_failed"
				break relayLoop
			}
			if err := upstream.WriteMessage(message.kind, outgoing); err != nil {
				relayErr, reason = err, "upstream_write_failed"
				break relayLoop
			}
			if idle != nil {
				idle.Reset(idleTimeout)
			}
		case message, ok := <-serverMessages:
			if !ok || message.err != nil {
				reason = "upstream_closed"
				break relayLoop
			}
			info.SetFirstResponseTime()
			outgoing, err := coordinator.ProcessServerEvent(message.body, message.at)
			if err != nil {
				relayErr, reason = err, "server_event_rejected"
				break relayLoop
			}
			if err := client.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
				relayErr, reason = err, "client_write_failed"
				break relayLoop
			}
			if err := client.WriteMessage(message.kind, outgoing); err != nil {
				relayErr, reason = err, "client_write_failed"
				break relayLoop
			}
			if idle != nil {
				idle.Reset(idleTimeout)
			}
		case <-ctx.Done():
			reason = "request_cancelled"
			break relayLoop
		case <-idleExpired:
			reason = "idle_timeout"
			break relayLoop
		}
	}

	closeSockets()
	// Drain after closing both sockets rather than abandoning a full channel.
	// Readers can finish their final send; already-read provider usage is still
	// recorded in order, without forwarding any new work or racing Close.
	for clientMessages != nil || serverMessages != nil {
		select {
		case _, ok := <-clientMessages:
			if !ok {
				clientMessages = nil
			}
		case message, ok := <-serverMessages:
			if !ok {
				serverMessages = nil
				continue
			}
			if message.err == nil {
				if _, err := coordinator.ProcessServerEvent(message.body, message.at); err != nil {
					relayErr = errors.Join(relayErr, err)
				}
			}
		}
	}
	readers.Wait()
	if err := coordinator.Close(reason); err != nil {
		relayErr = errors.Join(relayErr, err)
	}
	if relayErr != nil {
		return types.NewError(fmt.Errorf("realtime session ended: %w", relayErr), types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry()), coordinator.Usage()
	}
	return nil, coordinator.Usage()
}
