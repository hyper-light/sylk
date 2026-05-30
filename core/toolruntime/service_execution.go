package toolruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	toolRuntimeParticipantID              = "sys:tool_runtime"
	toolRuntimeServiceInvocationTimeout   = 30 * time.Second
	toolRuntimeServiceExpectedCallCount   = 1
	toolRuntimeServiceConcurrencyCapacity = toolRuntimeServiceExpectedCallCount
)

type toolRuntimeServiceBypassKey struct{}

type runtimeServiceBackend struct {
	runtime         *Runtime
	grant           *GuardianControlGrant
	transientActive map[string]struct{}
	restricted      bool
	raw             RawExecutionResult
	err             error
}

func (r *Runtime) executeRawServiceClaim(
	ctx context.Context,
	inv Invocation,
	approvedGrant *GuardianControlGrant,
	transientActive map[string]struct{},
	restricted bool,
) (RawExecutionResult, error, bool) {
	if r == nil {
		return RawExecutionResult{}, nil, false
	}
	if toolRuntimeServiceBypassed(ctx) {
		return RawExecutionResult{}, nil, false
	}
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil || acc.Board() == nil {
		return RawExecutionResult{}, nil, false
	}
	backend := &runtimeServiceBackend{runtime: r, grant: approvedGrant, transientActive: transientActive, restricted: restricted}
	handler := claims.NewToolRuntimeService(claims.InfrastructureServiceConfig{ToolBackend: backend})
	participant, err := runtimeServiceParticipant(acc.Board(), inv)
	if err != nil {
		return RawExecutionResult{}, err, true
	}
	_, err = claims.InvokeServiceClaim(ctx, claims.ServiceInvocationOptions{
		Board:          acc.Board(),
		Handler:        handler,
		Participant:    participant,
		IssuerID:       firstRuntimeServiceString(inv.AgentID, acc.AgentID()),
		SubjectID:      toolRuntimeParticipantID,
		Title:          "Execute tool: " + strings.TrimSpace(inv.ToolCall.Name),
		Description:    "Execute tool invocation through the tool runtime service.",
		ActionType:     claims.ActionTypeTask,
		ExpectedCall:   runtimeServiceExpectedCall(r, inv),
		IdempotencyKey: runtimeServiceIdempotencyKey(inv),
		Reason:         "tool runtime service claim generated",
	})
	if err != nil {
		return RawExecutionResult{}, err, true
	}
	return backend.raw, backend.err, true
}

func (b *runtimeServiceBackend) HandleToolRuntimeExecution(ctx context.Context, req claims.ToolRuntimeExecutionRequest) (claims.ToolRuntimeExecutionArtifactData, error) {
	data := req.Requested
	inv, err := invocationFromClaims(req, data)
	if err != nil {
		return failRuntimeServiceData(data, err), nil
	}
	if req.Call.Tool == claims.ToolRuntimeToolValidateInvocation {
		return b.validate(ctx, inv, data), nil
	}
	if req.Call.Tool == claims.ToolRuntimeToolQueryPolicy {
		return b.queryPolicy(data, inv), nil
	}
	return b.execute(ctx, inv, data), nil
}

func (b *runtimeServiceBackend) execute(ctx context.Context, inv Invocation, data claims.ToolRuntimeExecutionArtifactData) claims.ToolRuntimeExecutionArtifactData {
	started := time.Now().UTC()
	if strings.TrimSpace(data.FailureReason) != "" || strings.TrimSpace(data.PolicyDecision) == "denied" {
		b.err = fmt.Errorf("%s", firstRuntimeServiceString(data.FailureReason, "tool policy denied"))
		return finishRuntimeServiceExecution(data, started, RawExecutionResult{}, inv, b.err, ctx)
	}
	b.raw, b.err = b.runtime.executeRawDirect(withToolRuntimeServiceBypass(ctx), inv, b.grant, b.transientActive, b.restricted)
	return finishRuntimeServiceExecution(data, started, b.raw, inv, b.err, ctx)
}

func finishRuntimeServiceExecution(data claims.ToolRuntimeExecutionArtifactData, started time.Time, raw RawExecutionResult, inv Invocation, err error, ctx context.Context) claims.ToolRuntimeExecutionArtifactData {
	data.ToolName = firstRuntimeServiceString(raw.ToolName, data.ToolName, inv.ToolCall.Name)
	data.StartedAt = firstRuntimeServiceTime(data.StartedAt, started)
	data.EndedAt = time.Now().UTC()
	data.Duration = data.EndedAt.Sub(data.StartedAt)
	data.OutputSummary = runtimeServiceOutputSummary(raw)
	if acc := claims.AccumulatorFromContext(ctx); acc != nil {
		data.OutputRefs = append(data.OutputRefs, claimsToolOutputRefs(acc)...)
		data.GuardianClaimID = firstRuntimeServiceString(data.GuardianClaimID, claimsToolGuardianClaimID(acc))
	}
	data.Status = claims.InfrastructureStatusOK
	if err != nil {
		data.PolicyDecision = runtimeServiceDeniedDecision(data.PolicyDecision)
		data.ExitStatus = firstRuntimeServiceInt(data.ExitStatus, claimsToolFailureExitStatus())
		data.FailureReason = err.Error()
		data.Status = claims.InfrastructureStatusFailed
	}
	return data
}

func (b *runtimeServiceBackend) validate(ctx context.Context, inv Invocation, data claims.ToolRuntimeExecutionArtifactData) claims.ToolRuntimeExecutionArtifactData {
	started := time.Now().UTC()
	data.StartedAt = firstRuntimeServiceTime(data.StartedAt, started)
	_, policy, err := b.runtime.validateInvocation(inv)
	if err != nil {
		return finishRuntimeServiceValidation(data, started, err)
	}
	data.ExecutionMode = firstRuntimeServiceString(data.ExecutionMode, string(policy.Execution))
	data.PolicyRule = firstRuntimeServiceString(data.PolicyRule, policyRule(policy))
	if !b.runtime.isActive(inv.ToolCall.Name, b.transientActive, b.restricted) {
		return finishRuntimeServiceValidation(data, started, fmt.Errorf("tool %q is outside the active tool set", inv.ToolCall.Name))
	}
	return finishRuntimeServiceValidation(data, started, ctxErr(ctx))
}

func (b *runtimeServiceBackend) queryPolicy(data claims.ToolRuntimeExecutionArtifactData, inv Invocation) claims.ToolRuntimeExecutionArtifactData {
	started := time.Now().UTC()
	data.StartedAt = firstRuntimeServiceTime(data.StartedAt, started)
	_, policy, err := b.runtime.validateInvocation(inv)
	if err != nil {
		return finishRuntimeServiceValidation(data, started, err)
	}
	data.ExecutionMode = firstRuntimeServiceString(data.ExecutionMode, string(policy.Execution))
	data.PolicyRule = firstRuntimeServiceString(data.PolicyRule, policyRule(policy))
	return finishRuntimeServiceValidation(data, started, nil)
}

func runtimeServiceExpectedCall(r *Runtime, inv Invocation) claims.ExpectedToolCall {
	args := runtimeServiceCallArgs(r, inv)
	return claims.ExpectedToolCall{
		ID:        firstRuntimeServiceString(inv.ToolCall.ID, inv.CorrelationID),
		Tool:      claims.ToolRuntimeToolExecuteTool,
		Arguments: args,
	}
}

func runtimeServiceCallArgs(r *Runtime, inv Invocation) map[string]any {
	toolName, policy, err := r.validateInvocation(inv)
	decision := "allowed"
	failure := ""
	if err != nil {
		decision = "denied"
		failure = err.Error()
	}
	return map[string]any{
		"tool_name":         firstRuntimeServiceString(toolName, inv.ToolCall.Name),
		"args":              rawInvocationArgs(inv.ToolCall.Arguments),
		"redacted_args":     redactedInvocationArgs(inv.ToolCall.Arguments),
		"policy_decision":   decision,
		"policy_rule":       policyRule(policy),
		"execution_mode":    string(policy.Execution),
		"sandbox_scope":     runtimeServiceSandboxScope(inv),
		"failure_reason":    failure,
		"idempotency_nonce": runtimeServiceIdempotencyKey(inv),
	}
}

func runtimeServiceParticipant(board *claims.ClaimsBoard, inv Invocation) (claims.ParticipantRegistration, error) {
	return claims.NewParticipantRegistration(
		claims.ParticipantCategoryService,
		toolRuntimeParticipantID,
		map[string]string{
			"session_id":       firstRuntimeServiceString(board.SessionID(), board.BoardID()),
			"agent_uid":        firstRuntimeServiceString(inv.AgentID, inv.CapabilityScope),
			"capability_scope": firstRuntimeServiceString(inv.CapabilityScope, inv.AgentID),
		},
		toolRuntimeServiceExpectedCallCount,
		toolRuntimeServiceConcurrencyCapacity,
		toolRuntimeServiceInvocationTimeout,
		claims.HandlerDeterminismSideEffect,
		[]claims.ActionType{claims.ActionTypeTask},
	)
}

func runtimeServiceIdempotencyKey(inv Invocation) string {
	return strings.Join([]string{
		"tool_runtime",
		strings.TrimSpace(inv.AgentID),
		strings.TrimSpace(inv.CorrelationID),
		strings.TrimSpace(inv.ToolCall.ID),
		HashArguments(inv.ToolCall.Arguments),
	}, ":")
}

func runtimeServiceSandboxScope(inv Invocation) map[string]string {
	return map[string]string{
		"agent_uid":        strings.TrimSpace(inv.AgentID),
		"capability_scope": strings.TrimSpace(inv.CapabilityScope),
		"correlation_id":   strings.TrimSpace(inv.CorrelationID),
	}
}

func runtimeServiceOutputSummary(raw RawExecutionResult) string {
	if raw.Data == nil {
		return ""
	}
	out, err := marshalOutput(raw.Data)
	if err != nil {
		return err.Error()
	}
	return out
}

func finishRuntimeServiceValidation(data claims.ToolRuntimeExecutionArtifactData, started time.Time, err error) claims.ToolRuntimeExecutionArtifactData {
	data.EndedAt = time.Now().UTC()
	data.Duration = data.EndedAt.Sub(started)
	data.Status = claims.InfrastructureStatusOK
	if err == nil {
		data.OutputSummary = firstRuntimeServiceString(data.OutputSummary, "validated")
		data.PolicyDecision = firstRuntimeServiceString(data.PolicyDecision, "allowed")
		return data
	}
	return failRuntimeServiceData(data, err)
}

func failRuntimeServiceData(data claims.ToolRuntimeExecutionArtifactData, err error) claims.ToolRuntimeExecutionArtifactData {
	data.PolicyDecision = runtimeServiceDeniedDecision(data.PolicyDecision)
	data.ExitStatus = firstRuntimeServiceInt(data.ExitStatus, claimsToolFailureExitStatus())
	data.FailureReason = err.Error()
	data.Status = claims.InfrastructureStatusFailed
	return data
}

func rawInvocationArgs(raw string) map[string]any {
	args, _, err := decodeInputObject(raw)
	if err != nil {
		return map[string]any{"raw_json_hash": shortToolDigest(raw)}
	}
	return args
}

func policyRule(policy ToolPolicy) string {
	values := []string{strings.TrimSpace(string(policy.Effect)), strings.TrimSpace(string(policy.Domain))}
	return strings.Trim(strings.Join(values, ":"), ":")
}

func runtimeServiceDeniedDecision(current string) string {
	current = strings.TrimSpace(current)
	if current != "" && current != "allowed" {
		return current
	}
	return "denied"
}

func withToolRuntimeServiceBypass(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolRuntimeServiceBypassKey{}, true)
}

func toolRuntimeServiceBypassed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(toolRuntimeServiceBypassKey{}).(bool)
	return value
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func firstRuntimeServiceString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstRuntimeServiceTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func firstRuntimeServiceInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
