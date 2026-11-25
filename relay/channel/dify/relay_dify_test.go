package dify

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

func TestDifyBillingUsageCompleteness(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, output, usage string
			incomplete          bool
		}{
			{"prompt only", "hello", `{"prompt_tokens":4,"total_tokens":4}`, true},
			{"output only", "hello", `{"completion_tokens":2,"total_tokens":2}`, true},
			{"contradictory zero output", "hello", `{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}`, true},
			{"complete", "hello", `{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}`, false},
			{"empty output", "", `{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}`, false},
		} {
			t.Run(fmt.Sprintf("%t/%s", stream, tc.name), func(t *testing.T) {
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify"}}
				body := fmt.Sprintf(`{"answer":%q,"metadata":{"usage":%s}}`, tc.output, tc.usage)
				if stream {
					body = fmt.Sprintf("data: {\"event\":\"message\",\"answer\":%q}\n\ndata: {\"event\":\"message_end\",\"metadata\":{\"usage\":%s}}\n\n", tc.output, tc.usage)
				}
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
				if stream {
					usage, err := difyStreamHandler(ctx, info, resp)
					require.Nil(t, err)
					require.NotNil(t, usage)
					assert.Equal(t, relaycommon.ResponseOutcomeCompleted, info.StreamStatus.OutcomeSnapshot().Response)
				} else {
					usage, err := difyHandler(ctx, info, resp)
					require.Nil(t, err)
					require.NotNil(t, usage)
				}
				assert.Equal(t, tc.incomplete, common.GetContextKeyBool(ctx, constant.ContextKeyEstimatedBillingUsage))
			})
		}
	}
}
