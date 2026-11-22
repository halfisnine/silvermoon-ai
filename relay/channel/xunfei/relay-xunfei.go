package xunfei

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// https://console.xfyun.cn/services/cbm
// https://www.xfyun.cn/doc/spark/Web.html

func requestOpenAI2Xunfei(request dto.GeneralOpenAIRequest, xunfeiAppId string, domain string) *XunfeiChatRequest {
	messages := make([]XunfeiMessage, 0, len(request.Messages))
	shouldCovertSystemMessage := !strings.HasSuffix(request.Model, "3.5")
	for _, message := range request.Messages {
		if message.Role == "system" && shouldCovertSystemMessage {
			messages = append(messages, XunfeiMessage{
				Role:    "user",
				Content: message.StringContent(),
			})
			messages = append(messages, XunfeiMessage{
				Role:    "assistant",
				Content: "Okay",
			})
		} else {
			messages = append(messages, XunfeiMessage{
				Role:    message.Role,
				Content: message.StringContent(),
			})
		}
	}
	xunfeiRequest := XunfeiChatRequest{}
	xunfeiRequest.Header.AppId = xunfeiAppId
	xunfeiRequest.Parameter.Chat.Domain = domain
	xunfeiRequest.Parameter.Chat.Temperature = request.Temperature
	xunfeiRequest.Parameter.Chat.TopK = lo.FromPtrOr(request.N, 0)
	xunfeiRequest.Parameter.Chat.MaxTokens = request.GetMaxTokens()
	xunfeiRequest.Payload.Message.Text = messages
	return &xunfeiRequest
}

func responseXunfei2OpenAI(response *XunfeiChatResponse) *dto.OpenAITextResponse {
	if len(response.Payload.Choices.Text) == 0 {
		response.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: response.Payload.Choices.Text[0].Content,
		},
		FinishReason: constant.FinishReasonStop,
	}
	fullTextResponse := dto.OpenAITextResponse{
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   response.Payload.Usage.Text,
	}
	return &fullTextResponse
}

func streamResponseXunfei2OpenAI(xunfeiResponse *XunfeiChatResponse) *dto.ChatCompletionsStreamResponse {
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(xunfeiResponse.Payload.Choices.Text[0].Content)
	if xunfeiResponse.Payload.Choices.Status == 2 {
		choice.FinishReason = &constant.FinishReasonStop
	}
	response := dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Created: common.GetTimestamp(),
		Model:   "SparkDesk",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func buildXunfeiAuthUrl(hostUrl string, apiKey, apiSecret string) string {
	HmacWithShaToBase64 := func(algorithm, data, key string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(data))
		encodeData := mac.Sum(nil)
		return base64.StdEncoding.EncodeToString(encodeData)
	}
	ul, err := url.Parse(hostUrl)
	if err != nil {
		fmt.Println(err)
	}
	date := time.Now().UTC().Format(time.RFC1123)
	signString := []string{"host: " + ul.Host, "date: " + date, "GET " + ul.Path + " HTTP/1.1"}
	sign := strings.Join(signString, "\n")
	sha := HmacWithShaToBase64("hmac-sha256", sign, apiSecret)
	authUrl := fmt.Sprintf("hmac username=\"%s\", algorithm=\"%s\", headers=\"%s\", signature=\"%s\"", apiKey,
		"hmac-sha256", "host date request-line", sha)
	authorization := base64.StdEncoding.EncodeToString([]byte(authUrl))
	v := url.Values{}
	v.Add("host", ul.Host)
	v.Add("date", date)
	v.Add("authorization", authorization)
	callUrl := hostUrl + "?" + v.Encode()
	return callUrl
}

// One HTTP chat request uses one upstream WebSocket generation. An interrupted
// socket is not a completed response, even if earlier frames contained usage.
func xunfeiHandler(c *gin.Context, info *relaycommon.RelayInfo, textRequest dto.GeneralOpenAIRequest, appID, domain, authURL string) (*dto.Usage, *types.NewAPIError) {
	if info.StreamStatus == nil {
		info.StreamStatus = relaycommon.NewStreamStatus()
	}
	info.StreamStatus.RequireTerminal()
	data, err := common.Marshal(requestOpenAI2Xunfei(textRequest, appID, domain))
	if err != nil {
		return nil, types.NewError(errors.New("xunfei request encoding failed"), types.ErrorCodeInvalidRequest)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, response, err := dialer.DialContext(c.Request.Context(), authURL, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		// The URL contains signed credentials; do not expose the dial error.
		return nil, types.NewError(errors.New("xunfei websocket connection failed"), types.ErrorCodeDoRequestFailed)
	}
	defer conn.Close()
	stopCancellation := context.AfterFunc(c.Request.Context(), func() { _ = conn.Close() })
	defer stopCancellation()
	if c.Request.Context().Err() != nil {
		return nil, types.NewError(errors.New("xunfei request cancelled before dispatch"), types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if funding, managed := info.Billing.(*service.MarketingBillingSession); managed {
		if err := funding.BeginDispatch(); err != nil {
			return nil, types.NewError(errors.New(service.MarketingBillingMessage(c, err)), types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
		}
	}
	var usage dto.Usage
	if err = conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return &usage, xunfeiUnknownOutcome(info, usage, "request_write_failed", relaycommon.StreamEndReasonScannerErr)
	}
	if info.IsStream {
		helper.SetEventStreamHeaders(c)
	}
	var content strings.Builder
	observedOutput := false
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			reason := relaycommon.StreamEndReasonScannerErr
			if c.Request.Context().Err() != nil {
				reason = relaycommon.StreamEndReasonClientGone
			}
			return &usage, xunfeiUnknownOutcome(info, usage, "response_interrupted", reason)
		}
		var response XunfeiChatResponse
		if err := common.Unmarshal(message, &response); err != nil {
			return &usage, xunfeiUnknownOutcome(info, usage, "response_decode_failed", relaycommon.StreamEndReasonScannerErr)
		}
		// Require an explicit success code; a missing field is not code zero.
		code, codePresent := ojson.GetBytes(message, "header.code").Value().(float64)
		if !codePresent || code != 0 {
			if codePresent {
				info.StreamStatus.MarkFailed(strconv.Itoa(response.Header.Code), "xunfei_error", 502)
			}
			return &usage, xunfeiUnknownOutcome(info, usage, "upstream_error", relaycommon.StreamEndReasonHandlerStop)
		}
		frameUsage := response.Payload.Usage.Text
		if frameUsage.PromptTokens < 0 || frameUsage.CompletionTokens < 0 || frameUsage.TotalTokens < 0 ||
			frameUsage.PromptTokens > common.MaxQuota-usage.PromptTokens || frameUsage.CompletionTokens > common.MaxQuota-usage.CompletionTokens || frameUsage.TotalTokens > common.MaxQuota-usage.TotalTokens {
			return &usage, xunfeiUnknownOutcome(info, usage, "invalid_usage_quantity", relaycommon.StreamEndReasonHandlerStop)
		}
		usage.PromptTokens += response.Payload.Usage.Text.PromptTokens
		usage.CompletionTokens += response.Payload.Usage.Text.CompletionTokens
		usage.TotalTokens += response.Payload.Usage.Text.TotalTokens
		status, statusPresent := ojson.GetBytes(message, "payload.choices.status").Value().(float64)
		if !statusPresent || (status != 0 && status != 1 && status != 2) {
			return &usage, xunfeiUnknownOutcome(info, usage, "invalid_response_status", relaycommon.StreamEndReasonHandlerStop)
		}
		for _, item := range response.Payload.Choices.Text {
			observedOutput = observedOutput || item.Content != ""
		}
		terminal := status == 2
		if terminal {
			// A completed protocol response can still have unknown token cost.
			// Fixed request prices may settle after this genuine terminal event.
			completeUsage := !(observedOutput && usage.CompletionTokens == 0)
			for _, field := range []string{"prompt_tokens", "completion_tokens"} {
				value, exists := ojson.GetBytes(message, "payload.usage.text."+field).Value().(float64)
				if !exists || value < 0 {
					completeUsage = false
				}
			}
			if !completeUsage {
				info.BillingUsageEstimated = true
				common.SetContextKey(c, constant.ContextKeyEstimatedBillingUsage, true)
			}
			info.StreamStatus.MarkCompleted()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
		}
		if info.IsStream {
			if err := helper.ObjectData(c, streamResponseXunfei2OpenAI(&response)); err != nil && !terminal {
				return &usage, xunfeiUnknownOutcome(info, usage, "client_disconnected", relaycommon.StreamEndReasonClientGone)
			}
			if terminal {
				helper.Done(c)
				return &usage, nil
			}
			continue
		}
		if len(response.Payload.Choices.Text) != 0 {
			content.WriteString(response.Payload.Choices.Text[0].Content)
		}
		if !terminal {
			continue
		}
		if len(response.Payload.Choices.Text) == 0 {
			response.Payload.Choices.Text = []XunfeiChatResponseTextItem{{}}
		}
		response.Payload.Choices.Text[0].Content = content.String()
		encoded, err := common.Marshal(responseXunfei2OpenAI(&response))
		if err != nil {
			return &usage, xunfeiUnknownOutcome(info, usage, "response_encoding_failed", relaycommon.StreamEndReasonHandlerStop)
		}
		c.Writer.Header().Set("Content-Type", "application/json")
		_, _ = c.Writer.Write(encoded)
		return &usage, nil
	}
}

// Persist only classification and observed billing quantities. Provider messages
// and the authenticated URL can contain private data and must not enter evidence.
func xunfeiUnknownOutcome(info *relaycommon.RelayInfo, usage dto.Usage, reason string, end relaycommon.StreamEndReason) *types.NewAPIError {
	info.BillingUsageEstimated = true
	info.StreamStatus.MarkIncomplete(reason)
	info.StreamStatus.SetEndReason(end, nil)
	if funding, managed := info.Billing.(*service.MarketingBillingSession); managed {
		evidence, err := common.Marshal(map[string]any{"protocol": "xunfei_single_chat", "reason": reason, "observed_usage": usage, "stream_outcome": info.StreamStatus.OutcomeSnapshot()})
		if err == nil {
			err = funding.PreserveUnknown(string(evidence))
		}
		if err != nil && !errors.Is(err, model.ErrMarketingReview) {
			common.SysError(fmt.Sprintf("xunfei unresolved funding evidence could not be saved: %v", err))
		}
	}
	return types.NewError(errors.New("xunfei response did not complete successfully"), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
}

func apiVersion2domain(apiVersion string) string {
	switch apiVersion {
	case "v1.1":
		return "lite"
	case "v2.1":
		return "generalv2"
	case "v3.1":
		return "generalv3"
	case "v3.5":
		return "generalv3.5"
	case "v4.0":
		return "4.0Ultra"
	}
	return "general" + apiVersion
}

func getXunfeiAuthUrl(c *gin.Context, apiKey string, apiSecret string, modelName string) (string, string) {
	apiVersion := getAPIVersion(c, modelName)
	domain := apiVersion2domain(apiVersion)
	authUrl := buildXunfeiAuthUrl(fmt.Sprintf("wss://spark-api.xf-yun.com/%s/chat", apiVersion), apiKey, apiSecret)
	return domain, authUrl
}

func getAPIVersion(c *gin.Context, modelName string) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion != "" {
		return apiVersion
	}
	parts := strings.Split(modelName, "-")
	if len(parts) == 2 {
		apiVersion = parts[1]
		return apiVersion

	}
	apiVersion = c.GetString("api_version")
	if apiVersion != "" {
		return apiVersion
	}
	apiVersion = "v1.1"
	common.SysLog(common.LogText("api_version not found, using default: %s", apiVersion))
	return apiVersion
}
