package cohere

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func requestOpenAI2Cohere(textRequest dto.GeneralOpenAIRequest) *CohereRequest {
	cohereReq := CohereRequest{
		Model:       textRequest.Model,
		ChatHistory: []ChatHistory{},
		Message:     "",
		Stream:      lo.FromPtrOr(textRequest.Stream, false),
		MaxTokens:   textRequest.GetMaxTokens(),
	}
	if common.CohereSafetySetting != "NONE" {
		cohereReq.SafetyMode = common.CohereSafetySetting
	}
	if cohereReq.MaxTokens == 0 {
		cohereReq.MaxTokens = 4000
	}
	for _, msg := range textRequest.Messages {
		if msg.Role == "user" {
			cohereReq.Message = msg.StringContent()
		} else {
			var role string
			if msg.Role == "assistant" {
				role = "CHATBOT"
			} else if msg.Role == "system" {
				role = "SYSTEM"
			} else {
				role = "USER"
			}
			cohereReq.ChatHistory = append(cohereReq.ChatHistory, ChatHistory{
				Role:    role,
				Message: msg.StringContent(),
			})
		}
	}

	return &cohereReq
}

func requestConvertRerank2Cohere(rerankRequest dto.RerankRequest) *CohereRerankRequest {
	topN := lo.FromPtrOr(rerankRequest.TopN, 1)
	if topN <= 0 {
		topN = 1
	}
	cohereReq := CohereRerankRequest{
		Query:           rerankRequest.Query,
		Documents:       rerankRequest.Documents,
		Model:           rerankRequest.Model,
		TopN:            topN,
		ReturnDocuments: true,
	}
	return &cohereReq
}

func stopReasonCohere2OpenAI(reason string) string {
	switch reason {
	case "COMPLETE":
		return "stop"
	case "MAX_TOKENS":
		return "max_tokens"
	default:
		return reason
	}
}

// Decode count presence separately from numeric values: omitted/null usage is
// not equivalent to an explicit provider-reported zero.
type cohereBillingMeta struct {
	BilledUnits struct {
		InputTokens  *int `json:"input_tokens"`
		OutputTokens *int `json:"output_tokens"`
	} `json:"billed_units"`
}

type cohereBillingResult struct {
	CohereResponseResult
	Meta cohereBillingMeta `json:"meta"`
}

// Cohere supplies explicit terminal reasons for both streaming and ordinary chat.
// Keep provider failures distinct from a successful transport close.
func markCohereTerminal(info *relaycommon.RelayInfo, reason string, finished bool) {
	switch reason {
	case "ERROR", "ERROR_TOXIC", "ERROR_LIMIT":
		info.StreamStatus.MarkFailed(reason, "provider_error", 0)
	case "USER_CANCEL":
		info.StreamStatus.MarkCancelled()
	case "COMPLETE", "MAX_TOKENS", "STOP_SEQUENCE":
		info.StreamStatus.MarkCompleted()
	case "":
		if finished {
			info.StreamStatus.MarkCompleted()
		}
	default:
		info.StreamStatus.MarkIncomplete("unknown_finish_reason")
	}
}

func cohereStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.RequireTerminal()
	defer service.CloseResponseBodyGracefully(resp)
	if c.Request != nil {
		stop := context.AfterFunc(c.Request.Context(), func() { _ = resp.Body.Close() })
		defer stop()
	}
	responseId := helper.GetResponseID(c)
	createdTime := common.GetTimestamp()
	usage := &dto.Usage{}
	var responseText strings.Builder
	var hasOutput bool
	var reportedUsage bool
	scanner := helper.NewStreamScanner(resp.Body)
	helper.SetEventStreamHeaders(c)
	isFirst := true
	for scanner.Scan() {
		data := strings.TrimSpace(scanner.Text())
		if data == "" {
			continue
		}
		if isFirst {
			isFirst = false
			info.FirstResponseTime = time.Now()
		}
		var cohereResp struct {
			CohereResponse
			Response *cohereBillingResult `json:"response"`
		}
		if err := common.Unmarshal([]byte(data), &cohereResp); err != nil {
			info.StreamStatus.MarkIncomplete("invalid_provider_chunk")
			info.StreamStatus.RecordError("invalid_provider_chunk")
			continue
		}
		openaiResp := dto.ChatCompletionsStreamResponse{
			Id: responseId, Created: createdTime, Object: "chat.completion.chunk", Model: info.UpstreamModelName,
		}
		hasOutput = hasOutput || cohereResp.Text != ""
		if cohereResp.IsFinished {
			finishReason := cohereResp.FinishReason
			if cohereResp.Response != nil {
				usage.PromptTokens = lo.FromPtrOr(cohereResp.Response.Meta.BilledUnits.InputTokens, 0)
				usage.CompletionTokens = lo.FromPtrOr(cohereResp.Response.Meta.BilledUnits.OutputTokens, 0)
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
				reportedUsage = cohereResp.Response.Meta.BilledUnits.InputTokens != nil && cohereResp.Response.Meta.BilledUnits.OutputTokens != nil
				hasOutput = hasOutput || cohereResp.Response.Text != ""
				if finishReason == "" {
					finishReason = cohereResp.Response.FinishReason
				}
			}
			markCohereTerminal(info, finishReason, true)
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
			finishReason = stopReasonCohere2OpenAI(finishReason)
			openaiResp.Choices = []dto.ChatCompletionsStreamResponseChoice{{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{}, Index: 0, FinishReason: &finishReason,
			}}
		} else {
			openaiResp.Choices = []dto.ChatCompletionsStreamResponseChoice{{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant", Content: &cohereResp.Text}, Index: 0,
			}}
			responseText.WriteString(cohereResp.Text)
		}
		if err := helper.ObjectData(c, openaiResp); err != nil {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			break
		}
		if cohereResp.IsFinished {
			helper.Done(c)
			break
		}
	}
	if c.Request != nil && c.Request.Context().Err() != nil {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
	} else if err := scanner.Err(); err != nil {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
	} else {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	}
	if !reportedUsage && usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		usage = service.ResponseText2Usage(c, responseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	}
	if !reportedUsage || (hasOutput && usage.CompletionTokens == 0) {
		common.SetContextKey(c, constant.ContextKeyEstimatedBillingUsage, true)
	}
	return usage, nil
}

func cohereHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.RequireTerminal()
	defer service.CloseResponseBodyGracefully(resp)
	createdTime := common.GetTimestamp()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	var cohereResp cohereBillingResult
	err = common.Unmarshal(responseBody, &cohereResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	usage := dto.Usage{}
	usage.PromptTokens = lo.FromPtrOr(cohereResp.Meta.BilledUnits.InputTokens, 0)
	usage.CompletionTokens = lo.FromPtrOr(cohereResp.Meta.BilledUnits.OutputTokens, 0)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	markCohereTerminal(info, cohereResp.FinishReason, false)
	if cohereResp.FinishReason != "" {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	} else {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	}
	if cohereResp.Meta.BilledUnits.InputTokens == nil || cohereResp.Meta.BilledUnits.OutputTokens == nil || (cohereResp.Text != "" && usage.CompletionTokens == 0) {
		common.SetContextKey(c, constant.ContextKeyEstimatedBillingUsage, true)
	}

	var openaiResp dto.TextResponse
	openaiResp.Id = cohereResp.ResponseId
	openaiResp.Created = createdTime
	openaiResp.Object = "chat.completion"
	openaiResp.Model = info.UpstreamModelName
	openaiResp.Usage = usage

	openaiResp.Choices = []dto.OpenAITextResponseChoice{
		{
			Index:        0,
			Message:      dto.Message{Content: cohereResp.Text, Role: "assistant"},
			FinishReason: stopReasonCohere2OpenAI(cohereResp.FinishReason),
		},
	}

	jsonResponse, err := common.Marshal(openaiResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)
	return &usage, nil
}

func cohereRerankHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	service.CloseResponseBodyGracefully(resp)
	var cohereResp CohereRerankResponseResult
	err = common.Unmarshal(responseBody, &cohereResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	usage := dto.Usage{}
	if cohereResp.Meta.BilledUnits.InputTokens == 0 {
		common.SetContextKey(c, constant.ContextKeyEstimatedBillingUsage, true)
		usage.PromptTokens = info.GetEstimatePromptTokens()
		usage.CompletionTokens = 0
		usage.TotalTokens = info.GetEstimatePromptTokens()
	} else {
		usage.PromptTokens = cohereResp.Meta.BilledUnits.InputTokens
		usage.CompletionTokens = cohereResp.Meta.BilledUnits.OutputTokens
		usage.TotalTokens = cohereResp.Meta.BilledUnits.InputTokens + cohereResp.Meta.BilledUnits.OutputTokens
	}

	var rerankResp dto.RerankResponse
	rerankResp.Results = cohereResp.Results
	rerankResp.Usage = usage

	jsonResponse, err := common.Marshal(rerankResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return &usage, nil
}
