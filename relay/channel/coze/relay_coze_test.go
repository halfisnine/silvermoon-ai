package coze

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

func TestCozeStreamBillingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, output, usage string
		incomplete, broken  bool
	}{
		{name: "prompt only", output: "hello", usage: `{"input_count":4,"token_count":4}`, incomplete: true},
		{name: "output only", output: "hello", usage: `{"output_count":2,"token_count":2}`, incomplete: true},
		{name: "contradictory zero output", output: "hello", usage: `{"input_count":4,"output_count":0,"token_count":4}`, incomplete: true},
		{name: "complete", output: "hello", usage: `{"input_count":4,"output_count":2,"token_count":6}`},
		{name: "empty output", usage: `{"input_count":4,"output_count":0,"token_count":4}`},
		{name: "failure after terminal", output: "hello", usage: `{"input_count":4,"output_count":2,"token_count":6}`, broken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze"}}
			body := fmt.Sprintf("event: conversation.message.delta\ndata: {\"content\":%q}\n\nevent: conversation.chat.completed\ndata: {\"usage\":%s}\n\n", tc.output, tc.usage)
			if tc.broken {
				body += "event: error\ndata: {\"code\":500,\"message\":\"failure\"}\n\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
			usage, err := cozeChatStreamHandler(ctx, info, resp)
			require.Nil(t, err)
			require.NotNil(t, usage)
			assert.Equal(t, tc.incomplete, common.GetContextKeyBool(ctx, constant.ContextKeyEstimatedBillingUsage))
			outcome := info.StreamStatus.OutcomeSnapshot()
			assert.True(t, outcome.ExpectsTerminal)
			assert.Equal(t, !tc.broken, outcome.Response == relaycommon.ResponseOutcomeCompleted)
		})
	}
}
