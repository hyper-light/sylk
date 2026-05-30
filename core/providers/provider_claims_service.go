package providers

import (
	"context"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const providerGatewayParticipantID = "sys:provider_gateway"

type completedProviderGatewayBackend struct {
	data claims.ProviderGatewayCallArtifactData
}

func (b completedProviderGatewayBackend) HandleProviderGatewayCall(_ context.Context, _ claims.ProviderGatewayCallRequest) (claims.ProviderGatewayCallArtifactData, error) {
	return b.data, nil
}

func recordProviderGatewayServiceClaim(ctx context.Context, providerName string, req *Request, mode string, trace llmDispatchTrace, resp *Response, dispatchErr error) {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil || acc.Board() == nil || trace.dispatchID == "" {
		return
	}
	data := providerGatewayArtifactData(providerName, req, mode, trace, resp, dispatchErr)
	participant, err := providerGatewayParticipant(acc.Board(), providerName, acc.SessionID())
	if err != nil {
		acc.Board().RecordNotificationError("provider gateway participant: " + err.Error())
		return
	}
	invokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), providerGatewayServiceTimeout())
	defer cancel()
	_, err = claims.InvokeServiceClaim(invokeCtx, claims.ServiceInvocationOptions{
		Board:          acc.Board(),
		Handler:        claims.NewProviderGatewayService(claims.InfrastructureServiceConfig{ProviderBackend: completedProviderGatewayBackend{data: data}, ProviderPartialLimit: len(data.PartialSummaries)}),
		Participant:    participant,
		IssuerID:       firstClaimsGatewayString(acc.AgentID(), data.Identity),
		SubjectID:      providerGatewayParticipantID,
		Title:          "Provider gateway: " + data.Operation,
		Description:    "Record provider gateway dispatch outcome as claims-plane service evidence.",
		ActionType:     claims.ActionTypeTask,
		ExpectedCall:   claims.ExpectedToolCall{ID: trace.dispatchID, Tool: data.Operation, Arguments: providerGatewayCallArguments(data)},
		IdempotencyKey: "provider.gateway:" + trace.dispatchID,
		Reason:         "provider gateway service claim generated",
	})
	if err != nil {
		acc.Board().RecordNotificationError("provider gateway service claim: " + err.Error())
	}
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
