package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/relay/channel/tencent"
	"github.com/QuantumNous/new-api/relay/channel/zhipu"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertedOrdinaryResponseUsageProvenance(t *testing.T) {
	for _, provider := range []struct {
		name     string
		response func(*gin.Context, *http.Response, *relaycommon.RelayInfo) (any, *types.NewAPIError)
	}{
		{"tencent", (&tencent.Adaptor{}).DoResponse},
		{"zhipu", (&zhipu.Adaptor{}).DoResponse},
	} {
		for _, tc := range []struct {
			name          string
			usage         string
			content       string
			secondContent string
			want          dto.Usage
			estimated     bool
		}{
			{name: "complete", usage: `{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}`, content: "answer", want: dto.Usage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7}},
			{name: "prompt only", usage: `{"prompt_tokens":4,"total_tokens":7}`, content: "answer", want: dto.Usage{PromptTokens: 4, TotalTokens: 7}, estimated: true},
			{name: "completion only", usage: `{"completion_tokens":3,"total_tokens":7}`, content: "answer", want: dto.Usage{CompletionTokens: 3, TotalTokens: 7}, estimated: true},
			{name: "total only", usage: `{"total_tokens":7}`, content: "answer", want: dto.Usage{TotalTokens: 7}, estimated: true},
			{name: "missing usage", usage: `null`, content: "answer", estimated: true},
			{name: "null prompt", usage: `{"prompt_tokens":null,"completion_tokens":3}`, content: "answer", want: dto.Usage{CompletionTokens: 3}, estimated: true},
			{name: "null completion", usage: `{"prompt_tokens":4,"completion_tokens":null}`, content: "answer", want: dto.Usage{PromptTokens: 4}, estimated: true},
			{name: "negative prompt", usage: `{"prompt_tokens":-1,"completion_tokens":3}`, content: "answer", want: dto.Usage{PromptTokens: -1, CompletionTokens: 3}, estimated: true},
			{name: "negative completion", usage: `{"prompt_tokens":4,"completion_tokens":-1}`, content: "answer", want: dto.Usage{PromptTokens: 4, CompletionTokens: -1}, estimated: true},
			{name: "output contradicts zero", usage: `{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}`, content: "answer", want: dto.Usage{PromptTokens: 4, TotalTokens: 4}, estimated: true},
			{name: "later choice contradicts zero", usage: `{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}`, secondContent: "answer", want: dto.Usage{PromptTokens: 4, TotalTokens: 4}, estimated: true},
			{name: "legitimate zero output", usage: `{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}`, want: dto.Usage{PromptTokens: 4, TotalTokens: 4}},
			{name: "legitimate zero input", usage: `{"prompt_tokens":0,"completion_tokens":3,"total_tokens":3}`, content: "answer", want: dto.Usage{CompletionTokens: 3, TotalTokens: 3}},
			{name: "legitimate explicit zeros", usage: `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`},
		} {
			t.Run(provider.name+"/"+tc.name, func(t *testing.T) {
				usage := tc.usage
				var body map[string]any
				if provider.name == "tencent" {
					usage = strings.NewReplacer("prompt_tokens", "PromptTokens", "completion_tokens", "CompletionTokens", "total_tokens", "TotalTokens").Replace(usage)
					choices := []any{map[string]any{"Message": map[string]any{"Role": "assistant", "Content": tc.content}, "FinishReason": "stop"}}
					if tc.secondContent != "" {
						choices = append(choices, map[string]any{"Message": map[string]any{"Role": "assistant", "Content": tc.secondContent}, "FinishReason": "stop"})
					}
					var rawUsage any
					require.NoError(t, common.UnmarshalJsonStr(usage, &rawUsage))
					body = map[string]any{"Response": map[string]any{"Choices": choices, "Usage": rawUsage}}
				} else {
					choices := []any{map[string]any{"role": "assistant", "content": tc.content}}
					if tc.secondContent != "" {
						choices = append(choices, map[string]any{"role": "assistant", "content": tc.secondContent})
					}
					var rawUsage any
					require.NoError(t, common.UnmarshalJsonStr(usage, &rawUsage))
					body = map[string]any{"success": true, "code": 200, "data": map[string]any{"task_status": "SUCCESS", "choices": choices, "usage": rawUsage}}
				}
				encoded, err := common.Marshal(body)
				require.NoError(t, err)
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded)))}
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: provider.name}}
				actual, apiErr := provider.response(c, resp, info)
				require.Nil(t, apiErr)
				require.IsType(t, &dto.Usage{}, actual)
				assert.Equal(t, tc.want, *actual.(*dto.Usage), "provenance checks must preserve the original quantities")
				assert.Equal(t, tc.estimated, common.GetContextKeyBool(c, constant.ContextKeyEstimatedBillingUsage))
				assert.Equal(t, http.StatusOK, writer.Code)
				var returned dto.OpenAITextResponse
				require.NoError(t, common.Unmarshal(writer.Body.Bytes(), &returned))
				assert.Equal(t, tc.want, returned.Usage)
				assert.Equal(t, tc.content, returned.Choices[0].Message.StringContent())
			})
		}
	}
}

func TestOpenAIEmptyOutputAndPureInputEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		mode       int
		estimated  bool
	}{
		{"missing output is unknown even with empty content", `{"choices":[{"message":{"content":""}}],"usage":{"prompt_tokens":4,"total_tokens":4}}`, relayconstant.RelayModeChatCompletions, true},
		{"explicit empty output", `{"choices":[{"message":{"content":""}}],"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}`, relayconstant.RelayModeChatCompletions, false},
		{"embedding has only input usage", `{"data":[{"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":4,"total_tokens":4}}`, relayconstant.RelayModeEmbeddings, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/test", nil)
			info := &relaycommon.RelayInfo{RelayMode: tc.mode, ChannelMeta: &relaycommon.ChannelMeta{}}
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}
			usage, err := (&openai.Adaptor{}).DoResponse(ctx, resp, info)
			require.Nil(t, err)
			require.NotNil(t, usage)
			assert.Equal(t, tc.estimated, common.GetContextKeyBool(ctx, constant.ContextKeyEstimatedBillingUsage))
		})
	}
}
