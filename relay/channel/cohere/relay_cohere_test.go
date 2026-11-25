package cohere

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCohereChatUsageProvenance(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			text      string
			output    int
			reason    string
			outcome   relaycommon.ResponseOutcome
			estimated bool
		}{
			{"reported output", "hello", 2, "COMPLETE", relaycommon.ResponseOutcomeCompleted, false},
			{"missing output count", "hello", 0, "COMPLETE", relaycommon.ResponseOutcomeCompleted, true},
			{"empty output", "", 0, "COMPLETE", relaycommon.ResponseOutcomeCompleted, false},
			{"maximum tokens", "hello", 2, "MAX_TOKENS", relaycommon.ResponseOutcomeCompleted, false},
			{"failure", "hello", 2, "ERROR", relaycommon.ResponseOutcomeFailed, false},
			{"cancelled", "hello", 2, "USER_CANCEL", relaycommon.ResponseOutcomeCancelled, false},
			{"unrecognized terminal", "hello", 2, "future_reason", relaycommon.ResponseOutcomeIncomplete, false},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, tc.name), func(t *testing.T) {
				result := CohereResponseResult{Text: tc.text, FinishReason: tc.reason}
				result.Meta.BilledUnits.InputTokens = 4
				result.Meta.BilledUnits.OutputTokens = tc.output
				var payload any = result
				if stream {
					payload = CohereResponse{IsFinished: true, FinishReason: tc.reason, Response: &result}
				}
				body, err := common.Marshal(payload)
				require.NoError(t, err)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
				resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}
				handler := cohereHandler
				if stream {
					handler = cohereStreamHandler
				}
				usage, apiErr := handler(c, info, resp)
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				assert.Equal(t, 4, usage.PromptTokens)
				assert.Equal(t, tc.output, usage.CompletionTokens)
				assert.Equal(t, 4+tc.output, usage.TotalTokens)
				assert.Equal(t, tc.estimated, common.GetContextKeyBool(c, constant.ContextKeyEstimatedBillingUsage))
				outcome := info.StreamStatus.OutcomeSnapshot()
				assert.True(t, outcome.ExpectsTerminal)
				assert.Equal(t, tc.outcome, outcome.Response)
				assert.Equal(t, relaycommon.StreamEndReasonDone, outcome.EndReason)
			})
		}
	}
}

func TestCohereMissingTerminalAndMalformedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stream  bool
		body    string
		outcome relaycommon.ResponseOutcome
	}{
		{"nonstream missing finish reason", false, `{"text":"hello","meta":{"billed_units":{"input_tokens":4,"output_tokens":2}}}`, relaycommon.ResponseOutcomeUnknown},
		{"stream EOF without end", true, `{"text":"hello","is_finished":false}`, relaycommon.ResponseOutcomeUnknown},
		{"malformed chunk before end", true, "invalid\n" + `{"is_finished":true,"finish_reason":"COMPLETE","response":{"meta":{"billed_units":{"input_tokens":4,"output_tokens":2}}}}`, relaycommon.ResponseOutcomeIncomplete},
		{"earlier output missing count", true, "{\"text\":\"hello\"}\n" + `{"is_finished":true,"finish_reason":"COMPLETE","response":{"meta":{"billed_units":{"input_tokens":4,"output_tokens":0}}}}`, relaycommon.ResponseOutcomeCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			info := &relaycommon.RelayInfo{IsStream: tc.stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}
			handler := cohereHandler
			if tc.stream {
				handler = cohereStreamHandler
			}
			_, apiErr := handler(c, info, resp)
			require.Nil(t, apiErr)
			assert.Equal(t, tc.outcome, info.StreamStatus.OutcomeSnapshot().Response)
			if tc.stream && tc.outcome == relaycommon.ResponseOutcomeUnknown {
				assert.NotContains(t, w.Body.String(), "[DONE]")
			}
			if tc.name == "earlier output missing count" {
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyEstimatedBillingUsage))
			}
		})
	}
}

// A blocked upstream read must exit when the client disconnects; no producer
// goroutine may remain waiting to send a line after the handler returns.
type cancellableCohereBody struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (b *cancellableCohereBody) Read([]byte) (int, error) {
	close(b.started)
	<-b.closed
	return 0, errors.New("body closed")
}
func (b *cancellableCohereBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestCohereStreamCancellationClosesBlockedBody(t *testing.T) {
	body := &cancellableCohereBody{started: make(chan struct{}), closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cohereStreamHandler(c, info, &http.Response{StatusCode: http.StatusOK, Body: body})
	}()
	select {
	case <-body.started:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream read did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not exit after cancellation")
	}
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.OutcomeSnapshot().EndReason)
	assert.NotEqual(t, relaycommon.ResponseOutcomeCompleted, info.StreamStatus.OutcomeSnapshot().Response)
}

func TestCohereRerankInputOnlyUsageIsReported(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "rerank-v3"}}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[],"meta":{"billed_units":{"input_tokens":4,"output_tokens":0}}}`))}
	usage, apiErr := cohereRerankHandler(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 4, usage.TotalTokens)
	assert.Equal(t, 0, usage.CompletionTokens)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyEstimatedBillingUsage))
}

func TestCohereUsageFieldPresence(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name           string
			counts         string
			prompt, output int
			estimated      bool
		}{
			{"explicit zeros", `"input_tokens":0,"output_tokens":0`, 0, 0, false},
			{"explicit zero input", `"input_tokens":0,"output_tokens":2`, 0, 2, false},
			{"missing input", `"output_tokens":2`, 0, 2, true},
			{"null input", `"input_tokens":null,"output_tokens":2`, 0, 2, true},
			{"missing output", `"input_tokens":4`, 4, 0, true},
			{"null output", `"input_tokens":4,"output_tokens":null`, 4, 0, true},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, tc.name), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
				body := `{"finish_reason":"COMPLETE","meta":{"billed_units":{` + tc.counts + `}}}`
				if stream {
					body = `{"is_finished":true,"finish_reason":"COMPLETE","response":` + body + `}`
				}
				resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
				handler := cohereHandler
				if stream {
					handler = cohereStreamHandler
				}
				usage, apiErr := handler(c, info, resp)
				require.Nil(t, apiErr)
				assert.Equal(t, tc.prompt, usage.PromptTokens)
				assert.Equal(t, tc.output, usage.CompletionTokens)
				assert.Equal(t, tc.estimated, common.GetContextKeyBool(c, constant.ContextKeyEstimatedBillingUsage))
				assert.Equal(t, relaycommon.ResponseOutcomeCompleted, info.StreamStatus.OutcomeSnapshot().Response)
			})
		}
	}
}

func TestCohereStreamReadFailureCannotBecomeKnownUsage(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
	body := io.NopCloser(io.MultiReader(strings.NewReader("{\"text\":\"hello\"}\n"), iotest.ErrReader(io.ErrUnexpectedEOF)))
	_, apiErr := cohereStreamHandler(c, info, &http.Response{Body: body})
	require.Nil(t, apiErr)
	assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.OutcomeSnapshot().EndReason)
	assert.NotEqual(t, relaycommon.ResponseOutcomeCompleted, info.StreamStatus.OutcomeSnapshot().Response)
}
