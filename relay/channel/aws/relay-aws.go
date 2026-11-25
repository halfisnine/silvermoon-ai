package aws

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockruntimeTypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go/auth/bearer"
)

// getAwsErrorStatusCode extracts HTTP status code from AWS SDK error
func getAwsErrorStatusCode(err error) int {
	// Check for HTTP response error which contains status code
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		return httpErr.HTTPStatusCode()
	}
	// Default to 500 if we can't determine the status code
	return http.StatusInternalServerError
}

func newAwsInvokeContext(parent context.Context) (context.Context, context.CancelFunc) {
	if common.RelayTimeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(common.RelayTimeout)*time.Second)
}

func newAwsInvokeError(requestContext context.Context, err error, operation string) *types.NewAPIError {
	options := make([]types.NewAPIErrorOptions, 0, 1)
	if requestContext.Err() != nil {
		options = append(options, types.ErrOptionWithSkipRetry())
	}
	return types.NewOpenAIError(
		errors.Wrap(err, operation),
		types.ErrorCodeAwsInvokeError,
		getAwsErrorStatusCode(err),
		options...,
	)
}

// AWS InvokeModel performs network I/O in DoResponse, after DoRequest has only
// prepared the SDK input. Commit dispatch here, before the SDK can send it.
// Its internal retryer must not resend an execution with an unknown result.
func beginAwsDispatch(c *gin.Context, info *relaycommon.RelayInfo) ([]func(*bedrockruntime.Options), *types.NewAPIError) {
	funding, managed := info.Billing.(*service.MarketingBillingSession)
	if !managed {
		return nil, nil
	}
	if err := funding.BeginDispatch(); err != nil {
		return nil, types.NewError(errors.New(service.MarketingBillingMessage(c, err)), types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return []func(*bedrockruntime.Options){func(options *bedrockruntime.Options) {
		options.Retryer = aws.NopRetryer{}
		// Production uses the shared service HTTP client. A 307/308 redirect
		// would resend the POST below the SDK retryer, so isolate its policy.
		if client, ok := options.HTTPClient.(*http.Client); ok {
			guarded := *client
			guarded.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			}
			options.HTTPClient = &guarded
		}
	}}, nil
}

func awsDispatchError(c *gin.Context, info *relaycommon.RelayInfo, err error, operation string) *types.NewAPIError {
	if _, managed := info.Billing.(*service.MarketingBillingSession); managed {
		// A transport/SDK error is not proof that execution never started.
		return types.NewError(errors.New("AWS upstream request failed"), types.ErrorCodeAwsInvokeError, types.ErrOptionWithSkipRetry())
	}
	return newAwsInvokeError(c.Request.Context(), err, operation)
}

func newAwsClient(c *gin.Context, info *relaycommon.RelayInfo) (*bedrockruntime.Client, error) {
	httpClient, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}

	awsSecret := strings.Split(info.ApiKey, "|")
	var client *bedrockruntime.Client
	switch len(awsSecret) {
	case 2:
		apiKey := awsSecret[0]
		region := awsSecret[1]
		client = bedrockruntime.New(bedrockruntime.Options{
			Region:                  region,
			BearerAuthTokenProvider: bearer.StaticTokenProvider{Token: bearer.Token{Value: apiKey}},
			HTTPClient:              httpClient,
		})
	case 3:
		ak := awsSecret[0]
		sk := awsSecret[1]
		region := awsSecret[2]
		client = bedrockruntime.New(bedrockruntime.Options{
			Region:      region,
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(ak, sk, "")),
			HTTPClient:  httpClient,
		})
	default:
		return nil, errors.New("invalid aws secret key")
	}

	return client, nil
}

func doAwsClientRequest(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor, requestBody io.Reader) (any, error) {
	awsCli, err := newAwsClient(c, info)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelAwsClientError)
	}
	a.AwsClient = awsCli

	// 获取对应的AWS模型ID
	awsModelId := getAwsModelID(info.UpstreamModelName)

	awsRegionPrefix := getAwsRegionPrefix(awsCli.Options().Region)
	canCrossRegion := awsModelCanCrossRegion(awsModelId, awsRegionPrefix)
	if canCrossRegion {
		awsModelId = awsModelCrossRegion(awsModelId, awsRegionPrefix)
	}

	// init empty request.header
	requestHeader := http.Header{}
	a.SetupRequestHeader(c, &requestHeader, info)
	headerOverride, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	for key, value := range headerOverride {
		requestHeader.Set(key, value)
	}

	if isNovaModel(awsModelId) {
		var novaReq *NovaRequest
		err = common.DecodeJson(requestBody, &novaReq)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "decode nova request fail"), types.ErrorCodeBadRequestBody)
		}

		// 使用InvokeModel API，但使用Nova格式的请求体
		awsReq := &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(awsModelId),
			Accept:      aws.String("application/json"),
			ContentType: aws.String("application/json"),
		}

		reqBody, err := common.Marshal(novaReq)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "marshal nova request"), types.ErrorCodeBadResponseBody)
		}
		awsReq.Body = reqBody
		a.AwsReq = awsReq
		return nil, nil
	} else {
		awsClaudeReq, err := formatRequest(requestBody, requestHeader)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "format aws request fail"), types.ErrorCodeBadRequestBody)
		}

		if info.IsStream {
			awsReq := &bedrockruntime.InvokeModelWithResponseStreamInput{
				ModelId:     aws.String(awsModelId),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
			}
			awsReq.Body, err = buildAwsRequestBody(c, info, awsClaudeReq)
			if err != nil {
				return nil, types.NewError(errors.Wrap(err, "marshal aws request fail"), types.ErrorCodeBadRequestBody)
			}
			a.AwsReq = awsReq
			return nil, nil
		} else {
			awsReq := &bedrockruntime.InvokeModelInput{
				ModelId:     aws.String(awsModelId),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
			}
			awsReq.Body, err = buildAwsRequestBody(c, info, awsClaudeReq)
			if err != nil {
				return nil, types.NewError(errors.Wrap(err, "marshal aws request fail"), types.ErrorCodeBadRequestBody)
			}
			a.AwsReq = awsReq
			return nil, nil
		}
	}
}

// buildAwsRequestBody prepares the payload for AWS requests, applying passthrough rules when enabled.
func buildAwsRequestBody(c *gin.Context, info *relaycommon.RelayInfo, awsClaudeReq any) ([]byte, error) {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, errors.Wrap(err, "get request body for pass-through fail")
		}
		body, err := storage.Bytes()
		if err != nil {
			return nil, errors.Wrap(err, "get request body bytes fail")
		}
		// Only model and stream are removed: the rest keeps the client's bytes
		// and key order. The Doc reads body in place and never changes it.
		if !ojson.ValidBytes(body) || !ojson.GetBytesNoCopy(body, "@this").IsObject() {
			return nil, errors.New("pass-through unmarshal request body fail: request body must be a JSON object")
		}
		data := ojson.NewDoc(body)
		for _, key := range []string{"model", "stream"} {
			if err := relaycommon.DeleteJSONMember(data, key); err != nil {
				return nil, errors.Wrap(err, "pass-through unmarshal request body fail")
			}
		}
		return data.Bytes(), nil
	}
	return common.Marshal(awsClaudeReq)
}

func getAwsRegionPrefix(awsRegionId string) string {
	parts := strings.Split(awsRegionId, "-")
	regionPrefix := ""
	if len(parts) > 0 {
		regionPrefix = parts[0]
	}
	return regionPrefix
}

func awsModelCanCrossRegion(awsModelId, awsRegionPrefix string) bool {
	regionSet, exists := awsModelCanCrossRegionMap[awsModelId]
	return exists && regionSet[awsRegionPrefix]
}

func awsModelCrossRegion(awsModelId, awsRegionPrefix string) string {
	modelPrefix, find := awsRegionCrossModelPrefixMap[awsRegionPrefix]
	if !find {
		return awsModelId
	}
	return modelPrefix + "." + awsModelId
}

func getAwsModelID(requestModel string) string {
	if awsModelIDName, ok := awsModelIDMap[requestModel]; ok {
		return awsModelIDName
	}
	return requestModel
}

func awsHandler(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {

	requestContext := c.Request.Context()
	ctx, cancel := newAwsInvokeContext(requestContext)
	defer cancel()

	options, dispatchErr := beginAwsDispatch(c, info)
	if dispatchErr != nil {
		return dispatchErr, nil
	}
	awsResp, err := a.AwsClient.InvokeModel(ctx, a.AwsReq.(*bedrockruntime.InvokeModelInput), options...)
	if err != nil {
		return awsDispatchError(c, info, err, "InvokeModel"), nil
	}

	claudeInfo := &claude.ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}

	// 复制上游 Content-Type 到客户端响应头
	if awsResp.ContentType != nil && *awsResp.ContentType != "" {
		c.Writer.Header().Set("Content-Type", *awsResp.ContentType)
	}

	handlerErr := claude.HandleClaudeResponseData(c, info, claudeInfo, nil, awsResp.Body)
	if handlerErr != nil {
		return handlerErr, nil
	}
	if info.StreamStatus == nil {
		info.StreamStatus = relaycommon.NewStreamStatus()
	}
	info.StreamStatus.RequireTerminal()
	switch ojson.GetBytes(awsResp.Body, "stop_reason").String() {
	case "end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal":
		info.StreamStatus.MarkCompleted()
	}
	return nil, claudeInfo.Usage
}

func awsStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {
	requestContext := c.Request.Context()
	ctx, cancel := newAwsInvokeContext(requestContext)
	defer cancel()

	if info.StreamStatus == nil {
		info.StreamStatus = relaycommon.NewStreamStatus()
	}
	info.StreamStatus.RequireTerminal()
	options, dispatchErr := beginAwsDispatch(c, info)
	if dispatchErr != nil {
		return dispatchErr, nil
	}
	awsResp, err := a.AwsClient.InvokeModelWithResponseStream(ctx, a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput), options...)
	if err != nil {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
		return awsDispatchError(c, info, err, "InvokeModelWithResponseStream"), nil
	}
	stream := awsResp.GetStream()
	defer stream.Close()

	claudeInfo := &claude.ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}

	events := stream.Events()
streamLoop:
	for {
		select {
		case <-ctx.Done():
			break streamLoop
		case event, ok := <-events:
			if !ok {
				break streamLoop
			}
			if ctx.Err() != nil {
				break streamLoop
			}

			switch v := event.(type) {
			case *bedrockruntimeTypes.ResponseStreamMemberChunk:
				info.SetFirstResponseTime()
				respErr := claude.HandleStreamResponseData(c, info, claudeInfo, string(v.Value.Bytes))
				if respErr != nil {
					info.StreamStatus.MarkFailed("", "aws_stream_error", 0)
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, respErr)
					return respErr, nil
				}
			case *bedrockruntimeTypes.UnknownUnionMember:
				info.StreamStatus.MarkFailed("", "aws_stream_error", 0)
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, errors.New("unknown AWS stream event"))
				fmt.Println(common.LogText("unknown tag: %v", v.Tag))
				return types.NewError(errors.New("unknown response type"), types.ErrorCodeInvalidRequest), nil
			default:
				info.StreamStatus.MarkFailed("", "aws_stream_error", 0)
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, errors.New("invalid AWS stream event"))
				fmt.Println(common.LogText("union is nil or unknown type"))
				return types.NewError(errors.New("nil or unknown response type"), types.ErrorCodeInvalidRequest), nil
			}
		}
	}

	// SDK EOF is only transport closure. Claude message_stop is the terminal
	// fact; finalizing a truncated stream would otherwise manufacture [DONE].
	if ctx.Err() != nil {
		if requestContext.Err() != nil {
			info.StreamStatus.MarkCancelled()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, ctx.Err())
		} else {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, ctx.Err())
		}
	} else if streamErr := stream.Err(); streamErr != nil {
		info.StreamStatus.MarkFailed("", "aws_stream_error", 0)
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, streamErr)
	} else if info.StreamStatus.OutcomeSnapshot().Response == relaycommon.ResponseOutcomeCompleted {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	} else {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	}
	_ = stream.Close()
	if info.StreamStatus.OutcomeSnapshot().Response == relaycommon.ResponseOutcomeCompleted {
		claude.HandleStreamFinalResponse(c, info, claudeInfo)
	}
	return nil, claudeInfo.Usage
}

// Nova模型处理函数
func handleNovaRequest(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {

	requestContext := c.Request.Context()
	ctx, cancel := newAwsInvokeContext(requestContext)
	defer cancel()

	options, dispatchErr := beginAwsDispatch(c, info)
	if dispatchErr != nil {
		return dispatchErr, nil
	}
	awsResp, err := a.AwsClient.InvokeModel(ctx, a.AwsReq.(*bedrockruntime.InvokeModelInput), options...)
	if err != nil {
		return awsDispatchError(c, info, err, "InvokeModel"), nil
	}

	// 解析Nova响应
	var novaResp struct {
		Output struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		StopReason string `json:"stopReason"`
		Usage      struct {
			InputTokens  *int `json:"inputTokens"`
			OutputTokens *int `json:"outputTokens"`
			TotalTokens  *int `json:"totalTokens"`
		} `json:"usage"`
	}

	if err := common.Unmarshal(awsResp.Body, &novaResp); err != nil {
		return types.NewError(errors.Wrap(err, "unmarshal nova response"), types.ErrorCodeBadResponseBody), nil
	}

	var text strings.Builder
	for _, content := range novaResp.Output.Message.Content {
		text.WriteString(content.Text)
	}
	usage := dto.Usage{}
	if novaResp.Usage.InputTokens != nil {
		usage.PromptTokens = *novaResp.Usage.InputTokens
	}
	if novaResp.Usage.OutputTokens != nil {
		usage.CompletionTokens = *novaResp.Usage.OutputTokens
	}
	if novaResp.Usage.TotalTokens != nil {
		usage.TotalTokens = *novaResp.Usage.TotalTokens
	}
	if novaResp.Usage.InputTokens == nil || novaResp.Usage.OutputTokens == nil ||
		usage.PromptTokens < 0 || usage.PromptTokens > common.MaxQuota ||
		usage.CompletionTokens < 0 || usage.CompletionTokens > common.MaxQuota ||
		(text.Len() > 0 && usage.CompletionTokens == 0) {
		info.BillingUsageEstimated = true
	}
	if info.StreamStatus == nil {
		info.StreamStatus = relaycommon.NewStreamStatus()
	}
	info.StreamStatus.RequireTerminal()
	switch novaResp.StopReason {
	case "end_turn", "max_tokens", "stop_sequence", "content_filtered", "guardrail_intervened":
		info.StreamStatus.MarkCompleted()
	}

	// 构造OpenAI格式响应
	response := dto.OpenAITextResponse{
		Id:      helper.GetResponseID(c),
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Model:   info.UpstreamModelName,
		Choices: []dto.OpenAITextResponseChoice{{
			Index: 0,
			Message: dto.Message{
				Role:    "assistant",
				Content: text.String(),
			},
			FinishReason: "stop",
		}},
		Usage: usage,
	}

	c.JSON(http.StatusOK, response)
	return nil, &response.Usage
}
