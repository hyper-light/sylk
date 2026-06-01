package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const providerGatewayParticipantID = "sys:provider_gateway"

var errProviderGatewayServiceUnavailable = errors.New("provider gateway service unavailable")

type providerGatewayExecutionBackend struct {
	inner *ClaimsGatewayBackend
	mu    sync.Mutex
	data  claims.ProviderGatewayCallArtifactData
	resp  *Response
}

func newProviderGatewayExecutionBackend(provider ProviderAdapter, req *Request) *providerGatewayExecutionBackend {
	return &providerGatewayExecutionBackend{
		inner: NewClaimsGatewayBackend(ClaimsGatewayBackendConfig{
			Provider:             provider,
			Request:              req,
			ResponseSummaryLimit: llmDispatchContentSummaryCap,
			PartialSummaryLimit:  defaultProviderGatewayPartialLimit(),
		}),
	}
}

func (b *providerGatewayExecutionBackend) HandleProviderGatewayCall(ctx context.Context, req claims.ProviderGatewayCallRequest) (data claims.ProviderGatewayCallArtifactData, err error) {
	data = req.Requested
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err = fmt.Errorf("provider gateway execution fault: %v", recovered)
		data.Error = err.Error()
		data.FailureReason = err.Error()
		data.Status = claims.InfrastructureStatusFailed
		if b != nil {
			b.mu.Lock()
			b.data = data
			b.resp = nil
			b.mu.Unlock()
		}
		claims.RouteDebugLog().Info("provider_gateway_execution_fault",
			"error", err.Error(),
			"operation", data.Operation,
			"model", data.Model,
			"identity", data.Identity,
		)
	}()
	if b == nil || b.inner == nil {
		return data, errProviderGatewayServiceUnavailable
	}
	data, resp, err := b.inner.ExecuteProviderGatewayCall(ctx, req)
	b.mu.Lock()
	b.data = data
	b.resp = cloneProviderResponse(resp)
	b.mu.Unlock()
	return data, err
}

func (b *providerGatewayExecutionBackend) Result() (claims.ProviderGatewayCallArtifactData, *Response) {
	if b == nil {
		return claims.ProviderGatewayCallArtifactData{}, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data, cloneProviderResponse(b.resp)
}

func invokeProviderGatewayServiceClaim(ctx context.Context, provider ProviderAdapter, req *Request, mode string, trace llmDispatchTrace) (claims.ProviderGatewayCallArtifactData, *Response, error) {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil || acc.Board() == nil || provider == nil || trace.dispatchID == "" {
		return claims.ProviderGatewayCallArtifactData{}, nil, errProviderGatewayServiceUnavailable
	}
	data := providerGatewayArtifactData(provider.Name(), req, mode, trace, nil, nil)
	participant, err := providerGatewayParticipant(acc.Board(), provider.Name(), acc.SessionID())
	if err != nil {
		return data, nil, err
	}
	backend := newProviderGatewayExecutionBackend(provider, req)
	invokeCtx, cancel := context.WithTimeout(ctx, providerGatewayServiceTimeout())
	defer cancel()
	result, err := claims.InvokeServiceClaim(invokeCtx, claims.ServiceInvocationOptions{
		Board:          acc.Board(),
		Handler:        claims.NewProviderGatewayService(claims.InfrastructureServiceConfig{ProviderBackend: backend, ProviderPartialLimit: defaultProviderGatewayPartialLimit()}),
		Participant:    participant,
		IssuerID:       firstClaimsGatewayString(acc.AgentID(), data.Identity),
		SubjectID:      providerGatewayParticipantID,
		Title:          "Provider gateway: " + data.Operation,
		Description:    "Execute provider gateway dispatch through the claims-plane service participant.",
		ActionType:     claims.ActionTypeTask,
		ExpectedCall:   claims.ExpectedToolCall{ID: trace.dispatchID, Tool: data.Operation, Arguments: providerGatewayRequestArguments(req, data)},
		IdempotencyKey: "provider.gateway:" + trace.dispatchID,
		Reason:         "provider gateway service claim executed",
	})
	if err != nil {
		return data, nil, err
	}
	backendData, resp := backend.Result()
	return providerGatewayResultData(result.Result, backendData), resp, nil
}

func providerGatewayParticipant(board *claims.ClaimsBoard, providerName, sessionID string) (claims.ParticipantRegistration, error) {
	tools := providerGatewayTools()
	return claims.NewParticipantRegistration(
		claims.ParticipantCategoryService,
		providerGatewayParticipantID,
		map[string]string{
			"provider_type": firstClaimsGatewayString(providerName, "provider"),
			"session_id":    firstClaimsGatewayString(sessionID, board.SessionID(), board.BoardID()),
		},
		len(tools)*len(providerGatewayRecordClasses()),
		len(tools),
		providerGatewayServiceTimeout(),
		claims.HandlerDeterminismNondeterministic,
		[]claims.ActionType{claims.ActionTypeTask},
	)
}

func providerGatewayArtifactData(providerName string, req *Request, mode string, trace llmDispatchTrace, resp *Response, dispatchErr error) claims.ProviderGatewayCallArtifactData {
	data := claims.ProviderGatewayCallArtifactData{
		Operation:         providerGatewayOperation(mode),
		Model:             requestModel(req),
		Identity:          requestIdentity(req),
		TaskRef:           requestTaskRef(req),
		BudgetTokens:      requestBudget(req),
		PromptHash:        promptHash(requestMessages(req), requestSystemPrompt(req)),
		StreamingMode:     mode,
		ProviderRequestID: trace.dispatchID,
		Metadata: map[string]any{
			"provider":    strings.TrimSpace(providerName),
			"dispatch_id": trace.dispatchID,
		},
	}
	if !trace.started.IsZero() {
		data.Latency = time.Since(trace.started)
	}
	if resp != nil {
		data.Model = firstClaimsGatewayString(resp.Model, data.Model)
		data.ResponseSummary = firstClaimsGatewayString(truncateForDispatchSummary(resp.Content), "[empty provider response]")
		data.FinishReason = string(resp.StopReason)
		data.InputTokens = resp.Usage.InputTokens
		data.OutputTokens = resp.Usage.OutputTokens
		data.TotalTokens = firstClaimsGatewayInt(resp.Usage.TotalTokens, resp.Usage.InputTokens+resp.Usage.OutputTokens)
		data.ReasoningTokens = resp.Usage.ReasoningTokens
		data.CacheReadTokens = resp.Usage.CacheReadTokens
		data.CacheWriteTokens = resp.Usage.CacheWriteTokens
		data.ProviderRequestID = firstClaimsGatewayString(providerRequestID(resp), data.ProviderRequestID)
		data.Metadata["tool_calls"] = len(resp.ToolCalls)
	}
	if dispatchErr != nil {
		data.Error = dispatchErr.Error()
		data.FailureReason = dispatchErr.Error()
		data.Status = claims.InfrastructureStatusFailed
	} else {
		data.Status = claims.InfrastructureStatusOK
	}
	return data
}

func providerGatewayCallArguments(data claims.ProviderGatewayCallArtifactData) map[string]any {
	return map[string]any{
		"operation":            data.Operation,
		"model":                data.Model,
		"identity":             data.Identity,
		"task_ref":             data.TaskRef,
		"budget_tokens":        data.BudgetTokens,
		"prompt_hash":          data.PromptHash,
		"streaming_mode":       data.StreamingMode,
		"response_summary":     data.ResponseSummary,
		"finish_reason":        data.FinishReason,
		"provider_request_id":  data.ProviderRequestID,
		"input_tokens":         data.InputTokens,
		"output_tokens":        data.OutputTokens,
		"total_tokens":         data.TotalTokens,
		"reasoning_tokens":     data.ReasoningTokens,
		"cache_read_tokens":    data.CacheReadTokens,
		"cache_write_tokens":   data.CacheWriteTokens,
		"latency":              data.Latency,
		"rate_limit_remaining": data.RateLimitRemaining,
		"rate_limited":         data.RateLimited,
		"error":                data.Error,
		"status":               data.Status,
		"failure_reason":       data.FailureReason,
		"partial_summaries":    append([]string(nil), data.PartialSummaries...),
		"metadata":             cloneProviderMetadata(data.Metadata),
	}
}

func providerGatewayRequestArguments(req *Request, data claims.ProviderGatewayCallArtifactData) map[string]any {
	args := providerGatewayCallArguments(data)
	if req == nil {
		return args
	}
	args["messages"] = cloneProviderMessages(req.Messages)
	args["system_prompt"] = req.SystemPrompt
	args["max_tokens"] = req.MaxTokens
	args["metadata"] = cloneProviderMetadata(req.Metadata)
	return args
}

func providerGatewayOperation(mode string) string {
	switch strings.TrimSpace(mode) {
	case "stream", "stream_with_handler":
		return claims.ProviderGatewayToolCompleteStreaming
	default:
		return claims.ProviderGatewayToolComplete
	}
}

func providerGatewayTools() []string {
	return []string{claims.ProviderGatewayToolComplete, claims.ProviderGatewayToolCompleteStreaming, claims.ProviderGatewayToolEmbedding, claims.ProviderGatewayToolCountTokens}
}

func providerGatewayRecordClasses() []string {
	return []string{claims.InfrastructureStatusOK, claims.InfrastructureStatusFailed, claims.InfrastructureStatusInterrupted}
}

func providerGatewayServiceTimeout() time.Duration {
	return time.Duration(len(providerGatewayTools())*len(providerGatewayRecordClasses())) * time.Second
}

func defaultProviderGatewayPartialLimit() int {
	return len(providerGatewayTools()) * len(providerGatewayRecordClasses())
}

func providerGatewayResultData(result claims.ServiceClaimResult, fallback claims.ProviderGatewayCallArtifactData) claims.ProviderGatewayCallArtifactData {
	for _, artifact := range result.Artifacts {
		data, err := claims.ArtifactData[claims.ProviderGatewayCallArtifactData](artifact)
		if err == nil {
			return data
		}
	}
	return fallback
}

func providerGatewayExecutionError(data claims.ProviderGatewayCallArtifactData) error {
	if data.Error != "" {
		return errors.New(data.Error)
	}
	if data.RateLimited {
		return errors.New(firstClaimsGatewayString(data.FailureReason, "provider rate limited"))
	}
	return nil
}

func providerGatewayResponseFromData(data claims.ProviderGatewayCallArtifactData) *Response {
	return &Response{
		Content:    data.ResponseSummary,
		Model:      data.Model,
		StopReason: StopReason(data.FinishReason),
		Usage: Usage{
			InputTokens:      data.InputTokens,
			OutputTokens:     data.OutputTokens,
			TotalTokens:      data.TotalTokens,
			ReasoningTokens:  data.ReasoningTokens,
			CacheReadTokens:  data.CacheReadTokens,
			CacheWriteTokens: data.CacheWriteTokens,
		},
		ProviderMetadata: cloneProviderMetadata(data.Metadata),
	}
}

func cloneProviderResponse(in *Response) *Response {
	if in == nil {
		return nil
	}
	out := *in
	out.ToolCalls = append([]ToolCall(nil), in.ToolCalls...)
	out.ProviderMetadata = cloneProviderMetadata(in.ProviderMetadata)
	return &out
}

func providerGatewayStreamChunks(resp *Response) []*StreamChunk {
	now := time.Now().UTC()
	if resp == nil {
		return []*StreamChunk{{Type: ChunkTypeEnd, Timestamp: now}}
	}
	chunks := []*StreamChunk{{Type: ChunkTypeStart, Timestamp: now}}
	if resp.Content != "" {
		chunks = append(chunks, &StreamChunk{Type: ChunkTypeText, Text: resp.Content, Timestamp: now})
	}
	chunks = append(chunks, &StreamChunk{
		Type:         ChunkTypeEnd,
		Usage:        &Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, TotalTokens: resp.Usage.TotalTokens, ReasoningTokens: resp.Usage.ReasoningTokens, CacheReadTokens: resp.Usage.CacheReadTokens, CacheWriteTokens: resp.Usage.CacheWriteTokens},
		StopReason:   resp.StopReason,
		Timestamp:    now,
		ProviderData: cloneProviderMetadata(resp.ProviderMetadata),
	})
	for idx, chunk := range chunks {
		chunk.Index = idx
	}
	return chunks
}

func requestMessages(req *Request) []Message {
	if req == nil {
		return nil
	}
	return req.Messages
}

func requestModel(req *Request) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.Model)
}

func requestSystemPrompt(req *Request) string {
	if req == nil {
		return ""
	}
	return req.SystemPrompt
}

func requestBudget(req *Request) int {
	if req == nil {
		return 0
	}
	return req.MaxTokens
}

func requestIdentity(req *Request) string {
	if req == nil || req.Metadata == nil {
		return ""
	}
	return metadataString(req.Metadata, "identity", "agent_id", "agent", "originator")
}

func requestTaskRef(req *Request) string {
	if req == nil || req.Metadata == nil {
		return ""
	}
	return metadataString(req.Metadata, "task_ref", "task_id", "task")
}

func metadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := metadata[key].(string)
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cloneProviderMetadata(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
