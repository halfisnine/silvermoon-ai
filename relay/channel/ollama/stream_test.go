package ollama

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaChatHandlerNonStreamToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		raw    string
		wantID string
	}{
		{
			name:   "compact json per-line parse path",
			raw:    `{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_upstream","function":{"name":"get_weather","arguments":{"city":"Paris","days":0}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
			wantID: "call_upstream",
		},
		{
			name: "pretty json fallback parse path",
			raw: `{
  "model": "llama3.1",
  "created_at": "2026-05-27T12:00:00Z",
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "function": {
          "name": "get_weather",
          "arguments": {
            "city": "Paris",
            "days": 0
          }
        }
      }
    ]
  },
  "done": true,
  "done_reason": "stop",
  "prompt_eval_count": 5,
  "eval_count": 7
}`,
			wantID: "call_0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.raw)),
			}

			usage, apiErr := ollamaChatHandler(c, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
			}, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 12, usage.TotalTokens)

			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
			require.Len(t, out.Choices, 1)
			assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)

			var toolCalls []dto.ToolCallResponse
			require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &toolCalls))
			require.Len(t, toolCalls, 1)
			assert.Equal(t, tt.wantID, toolCalls[0].ID)
			assert.Equal(t, "function", toolCalls[0].Type)
			assert.Equal(t, "get_weather", toolCalls[0].Function.Name)
			assert.Nil(t, toolCalls[0].Index)

			var args map[string]any
			require.NoError(t, common.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args))
			assert.Equal(t, "Paris", args["city"])
			assert.Equal(t, float64(0), args["days"])
		})
	}
}

func TestBuildOllamaStreamDelta(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		startIndex    int
		wantContent   string
		wantReasoning string
		wantToolCalls []struct {
			id        string
			name      string
			index     int
			arguments string
		}
		wantNextIndex int
		wantPayload   bool
	}{
		{
			name:        "chat content",
			raw:         `{"message":{"content":"hello"}}`,
			wantContent: "hello",
			wantPayload: true,
		},
		{
			name:        "generate content",
			raw:         `{"response":"hello"}`,
			wantContent: "hello",
			wantPayload: true,
		},
		{
			name:          "thinking json string",
			raw:           `{"message":{"thinking":"consider this"}}`,
			wantReasoning: "consider this",
			wantPayload:   true,
		},
		{
			name:          "thinking raw fallback",
			raw:           `{"message":{"thinking":{"step":"consider this"}}}`,
			wantReasoning: `{"step":"consider this"}`,
			wantPayload:   true,
		},
		{
			name:       "multiple tool calls",
			raw:        `{"message":{"tool_calls":[{"id":"call_upstream","function":{"name":"get_weather","arguments":{"city":"Paris"}}},{"function":{"name":"get_time","arguments":{"timezone":"UTC"}}}]}}`,
			startIndex: 2,
			wantToolCalls: []struct {
				id        string
				name      string
				index     int
				arguments string
			}{
				{id: "call_upstream", name: "get_weather", index: 2, arguments: `{"city":"Paris"}`},
				{id: "call_3", name: "get_time", index: 3, arguments: `{"timezone":"UTC"}`},
			},
			wantNextIndex: 4,
			wantPayload:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chunk ollamaChatStreamChunk
			require.NoError(t, common.Unmarshal([]byte(tt.raw), &chunk))

			toolCallIndex := tt.startIndex
			delta, hasPayload := buildOllamaStreamDelta(&chunk, "response-id", 123, "model", &toolCallIndex)
			assert.Equal(t, tt.wantPayload, hasPayload)
			assert.Equal(t, tt.wantContent, delta.Choices[0].Delta.GetContentString())
			assert.Equal(t, tt.wantReasoning, delta.Choices[0].Delta.GetReasoningContent())
			assert.Equal(t, tt.wantNextIndex, toolCallIndex)

			if tt.wantToolCalls == nil {
				assert.Empty(t, delta.Choices[0].Delta.ToolCalls)
				return
			}
			require.Len(t, delta.Choices[0].Delta.ToolCalls, len(tt.wantToolCalls))
			for i, want := range tt.wantToolCalls {
				got := delta.Choices[0].Delta.ToolCalls[i]
				assert.Equal(t, want.id, got.ID)
				assert.Equal(t, "function", got.Type)
				assert.Equal(t, want.name, got.Function.Name)
				assert.Equal(t, want.arguments, got.Function.Arguments)
				require.NotNil(t, got.Index)
				assert.Equal(t, want.index, *got.Index)
			}
		})
	}
}

func TestOllamaStreamHandlerDoneToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"model":"qwen3-coder","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"北京"}}},{"function":{"name":"get_time","arguments":{"timezone":"Asia/Shanghai"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`)),
	}

	usage, apiErr := ollamaStreamHandler(c, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
	}, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage.TotalTokens)

	var chunks []dto.ChatCompletionsStreamResponse
	for _, line := range strings.Split(w.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, common.Unmarshal([]byte(data), &chunk))
		chunks = append(chunks, chunk)
	}

	var toolChunkIndex, stopChunkIndex int = -1, -1
	for i := range chunks {
		if len(chunks[i].Choices) == 0 {
			continue
		}
		if len(chunks[i].Choices[0].Delta.ToolCalls) > 0 {
			toolChunkIndex = i
		}
		if chunks[i].Choices[0].FinishReason != nil {
			stopChunkIndex = i
		}
	}
	require.NotEqual(t, -1, toolChunkIndex)
	require.NotEqual(t, -1, stopChunkIndex)
	assert.Less(t, toolChunkIndex, stopChunkIndex)

	toolCalls := chunks[toolChunkIndex].Choices[0].Delta.ToolCalls
	require.Len(t, toolCalls, 2)
	assert.Equal(t, "call_0", toolCalls[0].ID)
	assert.Equal(t, "call_1", toolCalls[1].ID)
	require.NotNil(t, toolCalls[0].Index)
	require.NotNil(t, toolCalls[1].Index)
	assert.Equal(t, 0, *toolCalls[0].Index)
	assert.Equal(t, 1, *toolCalls[1].Index)
	assert.Equal(t, constant.FinishReasonToolCalls, *chunks[stopChunkIndex].Choices[0].FinishReason)
	assert.Contains(t, w.Body.String(), "data: [DONE]")
}

// A positive prompt count does not establish complete output usage. Keep the
// provider numbers intact, but force reconciliation for incomplete evidence.
func TestOllamaUsageProvenance(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			body      string
			estimated bool
			terminal  bool
		}{
			{"reported output", `{"done":true,"message":{"content":"hello"},"prompt_eval_count":4,"eval_count":2}`, false, true},
			{"empty output", `{"done":true,"message":{"content":"","thinking":""},"prompt_eval_count":4,"eval_count":0}`, false, true},
			{"missing text count", `{"done":true,"message":{"content":"hello"},"prompt_eval_count":4,"eval_count":0}`, true, true},
			{"missing reasoning count", `{"done":true,"message":{"thinking":"reasoning"},"prompt_eval_count":4,"eval_count":0}`, true, true},
			{"missing tool count", `{"done":true,"message":{"tool_calls":[{"function":{"name":"weather","arguments":{}}}]},"prompt_eval_count":4,"eval_count":0}`, true, true},
			{"missing terminal", `{"done":false,"message":{"content":"hello"},"prompt_eval_count":4,"eval_count":2}`, stream, false},
			{"output in earlier chunk", "{\"done\":false,\"response\":\"hello\"}\n{\"done\":true,\"prompt_eval_count\":4,\"eval_count\":0}", true, true},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, tc.name), func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"}}
				resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}
				handler := ollamaChatHandler
				if stream {
					handler = ollamaStreamHandler
				}
				usage, apiErr := handler(c, info, resp)
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				assert.Equal(t, tc.estimated, common.GetContextKeyBool(c, constant.ContextKeyEstimatedBillingUsage))
				outcome := info.StreamStatus.OutcomeSnapshot()
				assert.True(t, outcome.ExpectsTerminal)
				assert.Equal(t, tc.terminal, outcome.Response == relaycommon.ResponseOutcomeCompleted)
				if tc.terminal {
					assert.Equal(t, 4, usage.PromptTokens)
					assert.Equal(t, relaycommon.StreamEndReasonDone, outcome.EndReason)
				} else {
					assert.Equal(t, relaycommon.StreamEndReasonEOF, outcome.EndReason)
				}
				if stream {
					assert.Equal(t, tc.terminal, strings.Contains(w.Body.String(), "[DONE]"))
				}
			})
		}
	}
}

func TestOllamaMalformedChunkCannotBecomeKnownUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"}}
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("invalid\n{\"done\":true,\"prompt_eval_count\":4,\"eval_count\":2}"))}
			if stream {
				_, apiErr := ollamaStreamHandler(c, info, resp)
				require.NotNil(t, apiErr)
			} else {
				_, apiErr := ollamaChatHandler(c, info, resp)
				require.Nil(t, apiErr)
			}
			assert.Equal(t, relaycommon.ResponseOutcomeIncomplete, info.StreamStatus.OutcomeSnapshot().Response)
		})
	}
}

func TestOllamaUsageFieldPresence(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name           string
			counts         string
			prompt, output int
			estimated      bool
		}{
			{"explicit zeros", `"prompt_eval_count":0,"eval_count":0`, 0, 0, false},
			{"explicit zero input", `"prompt_eval_count":0,"eval_count":2`, 0, 2, false},
			{"missing input", `"eval_count":2`, 0, 2, true},
			{"null input", `"prompt_eval_count":null,"eval_count":2`, 0, 2, true},
			{"missing output", `"prompt_eval_count":4`, 4, 0, true},
			{"null output", `"prompt_eval_count":4,"eval_count":null`, 4, 0, true},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, tc.name), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"}}
				resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"done":true,` + tc.counts + `}`))}
				handler := ollamaChatHandler
				if stream {
					handler = ollamaStreamHandler
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

func TestOllamaStreamReadFailureAndCancellation(t *testing.T) {
	t.Run("upstream read failure", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"}}
		body := io.NopCloser(io.MultiReader(strings.NewReader("{\"response\":\"hello\"}\n"), iotest.ErrReader(io.ErrUnexpectedEOF)))
		_, apiErr := ollamaStreamHandler(c, info, &http.Response{Body: body})
		require.Nil(t, apiErr)
		assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.OutcomeSnapshot().EndReason)
		assert.NotEqual(t, relaycommon.ResponseOutcomeCompleted, info.StreamStatus.OutcomeSnapshot().Response)
	})
	t.Run("cancel blocked read", func(t *testing.T) {
		reader, writer := io.Pipe()
		defer writer.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"}}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = ollamaStreamHandler(c, info, &http.Response{Body: reader})
		}()
		written := make(chan error, 1)
		go func() { _, err := io.WriteString(writer, "{\"response\":\"hello\"}\n"); written <- err }()
		select {
		case err := <-written:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("handler did not read the first chunk")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("handler did not exit after cancellation")
		}
		assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.OutcomeSnapshot().EndReason)
		assert.NotEqual(t, relaycommon.ResponseOutcomeCompleted, info.StreamStatus.OutcomeSnapshot().Response)
	})
}
