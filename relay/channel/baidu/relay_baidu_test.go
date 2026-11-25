package baidu

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaiduChatBillingEvidence(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, tc := range []struct {
		name, output, usage          string
		stream, terminal, incomplete bool
		wantOutput                   int
	}{
		{name: "ordinary output only", output: "hello", usage: `{"completion_tokens":2,"total_tokens":2}`, incomplete: true, wantOutput: 2},
		{name: "stream output only", stream: true, terminal: true, output: "hello", usage: `{"total_tokens":2}`, incomplete: true, wantOutput: 2},
		{name: "ordinary prompt only", output: "hello", usage: `{"prompt_tokens":4,"total_tokens":4}`, incomplete: true},
		{name: "ordinary complete", output: "hello", usage: `{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}`, wantOutput: 2},
		{name: "ordinary empty output", usage: `{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}`},
		{name: "stream prompt only", stream: true, terminal: true, output: "hello", usage: `{"prompt_tokens":4,"total_tokens":4}`, incomplete: true},
		{name: "stream derives output from total", stream: true, terminal: true, output: "hello", usage: `{"prompt_tokens":4,"total_tokens":6}`, wantOutput: 2},
		{name: "stream missing terminal", stream: true, output: "hello", usage: `{"prompt_tokens":4,"total_tokens":6}`, wantOutput: 2},
		{name: "stream empty output", stream: true, terminal: true, usage: `{"prompt_tokens":4,"total_tokens":4}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: tc.stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ernie"}}
			body := fmt.Sprintf(`{"result":%q,"usage":%s,"is_end":%t}`, tc.output, tc.usage, tc.terminal)
			if tc.stream {
				body = "data: " + body + "\n\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
			if tc.stream {
				err, usage := baiduStreamHandler(ctx, info, resp)
				require.Nil(t, err)
				assert.Equal(t, tc.wantOutput, usage.CompletionTokens)
				outcome := info.StreamStatus.OutcomeSnapshot()
				assert.True(t, outcome.ExpectsTerminal)
				assert.Equal(t, tc.terminal, outcome.Response == relaycommon.ResponseOutcomeCompleted)
			} else {
				err, usage := baiduHandler(ctx, info, resp)
				require.Nil(t, err)
				assert.Equal(t, tc.wantOutput, usage.CompletionTokens)
			}
			assert.Equal(t, tc.incomplete, common.GetContextKeyBool(ctx, constant.ContextKeyEstimatedBillingUsage))
		})
	}
}
