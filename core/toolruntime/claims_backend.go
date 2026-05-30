package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

type ClaimsBackendConfig struct {
	Surface            Surface
	OutputSummaryLimit int
	Clock              func() time.Time
}

type ClaimsBackend struct {
	surface            Surface
	outputSummaryLimit int
	clock              func() time.Time
}

func NewClaimsBackend(cfg ClaimsBackendConfig) *ClaimsBackend {
	return &ClaimsBackend{
		surface:            cfg.Surface,
		outputSummaryLimit: cfg.OutputSummaryLimit,
		clock:              firstClaimsToolClock(cfg.Clock),
	}
}

func (b *ClaimsBackend) HandleToolRuntimeExecution(ctx context.Context, req claims.ToolRuntimeExecutionRequest) (claims.ToolRuntimeExecutionArtifactData, error) {
	data := req.Requested
	if b == nil || b.surface == nil {
		data.FailureReason = "tool runtime surface not configured"
		return data, nil
	}
	inv, err := invocationFromClaims(req, data)
	if err != nil {
		data.FailureReason = err.Error()
		return data, nil
	}
	switch req.Call.Tool {
	case claims.ToolRuntimeToolValidateInvocation:
		return b.validate(ctx, inv, data), nil
	case claims.ToolRuntimeToolQueryPolicy:
		return b.queryPolicy(data, inv), nil
	}
	return b.execute(ctx, req, inv, data), nil
}

func (b *ClaimsBackend) validate(ctx context.Context, inv Invocation, data claims.ToolRuntimeExecutionArtifactData) claims.ToolRuntimeExecutionArtifactData {
	started := firstClaimsToolTime(data.StartedAt, b.clock())
	data.StartedAt = started
	if err := b.surface.ValidateBatch([]Invocation{inv}); err != nil {
		data.PolicyDecision = claimsToolDeniedDecision(data.PolicyDecision)
		return b.finish(data, started, "", err)
	}
	if !b.surface.Allows(inv.ToolCall.Name) {
		data.PolicyDecision = claimsToolDeniedDecision(data.PolicyDecision)
		return b.finish(data, started, "", fmt.Errorf("tool %q is not allowed", inv.ToolCall.Name))
	}
	data.PolicyDecision = firstClaimsToolString(data.PolicyDecision, "allowed")
	data.ExecutionMode = firstClaimsToolString(data.ExecutionMode, "policy_validated")
	return b.finish(data, started, "validated", claimsToolContextErr(ctx))
}

func (b *ClaimsBackend) queryPolicy(data claims.ToolRuntimeExecutionArtifactData, inv Invocation) claims.ToolRuntimeExecutionArtifactData {
	started := firstClaimsToolTime(data.StartedAt, b.clock())
	data.StartedAt = started
	if !b.surface.Allows(inv.ToolCall.Name) {
		data.PolicyDecision = claimsToolDeniedDecision(data.PolicyDecision)
		return b.finish(data, started, "", fmt.Errorf("tool %q is not allowed", inv.ToolCall.Name))
	}
	data.PolicyDecision = firstClaimsToolString(data.PolicyDecision, "allowed")
	data.ExecutionMode = firstClaimsToolString(data.ExecutionMode, "policy_query")
	return b.finish(data, started, "policy allows "+inv.ToolCall.Name, nil)
}

func (b *ClaimsBackend) execute(ctx context.Context, req claims.ToolRuntimeExecutionRequest, inv Invocation, data claims.ToolRuntimeExecutionArtifactData) claims.ToolRuntimeExecutionArtifactData {
	started := firstClaimsToolTime(data.StartedAt, b.clock())
	data.StartedAt = started
	if strings.TrimSpace(data.FailureReason) != "" || strings.TrimSpace(data.PolicyDecision) == "denied" {
		data.PolicyDecision = claimsToolDeniedDecision(data.PolicyDecision)
		return b.finish(data, started, "", fmt.Errorf("%s", firstClaimsToolString(data.FailureReason, "tool policy denied")))
	}
	if err := b.surface.ValidateBatch([]Invocation{inv}); err != nil {
		data.PolicyDecision = claimsToolDeniedDecision(data.PolicyDecision)
		return b.finish(data, started, "", err)
	}
	if !b.surface.Allows(inv.ToolCall.Name) {
		data.PolicyDecision = claimsToolDeniedDecision(data.PolicyDecision)
		return b.finish(data, started, "", fmt.Errorf("tool %q is not allowed", inv.ToolCall.Name))
	}
	execCtx, acc := claimsToolContext(ctx, req, inv.AgentID)
	result, err := b.surface.Execute(execCtx, inv)
	data.ToolName = firstClaimsToolString(result.ToolName, data.ToolName, inv.ToolCall.Name)
	data.OutputRefs = append(data.OutputRefs, claimsToolOutputRefs(acc)...)
	data.GuardianClaimID = firstClaimsToolString(data.GuardianClaimID, claimsToolGuardianClaimID(acc))
	return b.finish(data, started, result.Output, err)
}

func (b *ClaimsBackend) finish(data claims.ToolRuntimeExecutionArtifactData, started time.Time, output string, err error) claims.ToolRuntimeExecutionArtifactData {
	ended := b.clock()
	data.EndedAt = firstClaimsToolTime(data.EndedAt, ended)
	data.Duration = data.EndedAt.Sub(started)
	data.OutputSummary = boundedClaimsToolText(firstClaimsToolString(output, data.OutputSummary), b.summaryLimit(data, output))
	if err != nil {
		data.ExitStatus = firstClaimsToolInt(data.ExitStatus, claimsToolFailureExitStatus())
		data.FailureReason = err.Error()
	}
	return data
}

func invocationFromClaims(req claims.ToolRuntimeExecutionRequest, data claims.ToolRuntimeExecutionArtifactData) (Invocation, error) {
	arguments, err := toolArgumentsJSON(req.Call.Arguments)
	if err != nil {
		return Invocation{}, err
	}
	claimID := ""
	claimAgentID := ""
	var claimRelations []claims.Relation
	if req.Claim != nil {
		claimID = req.Claim.ID
		claimAgentID = req.Claim.AgentID
		claimRelations = req.Claim.Relations
	}
	agentID := firstClaimsToolString(claimsToolStringArg(req.Call.Arguments, "agent_id"), claims.IssuerAgentID(claimRelations), claimAgentID, req.Participant.RouteKey)
	return Invocation{
		ToolCall: providers.ToolCall{
			ID:        firstClaimsToolString(req.Call.ID, data.ToolName),
			Name:      data.ToolName,
			Arguments: arguments,
		},
		AgentID:         agentID,
		CorrelationID:   firstClaimsToolString(claimsToolStringArg(req.Call.Arguments, "correlation_id"), req.Call.ID, claimID),
		CapabilityScope: firstClaimsToolString(claimsToolStringArg(req.Call.Arguments, "capability_scope"), claimsToolNestedStringArg(req.Call.Arguments, "sandbox_scope", "capability_scope"), req.Participant.RouteKey, agentID),
	}, nil
}

func toolArgumentsJSON(args map[string]any) (string, error) {
	value, ok := args["args"]
	if !ok {
		value = args
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func claimsToolContext(ctx context.Context, req claims.ToolRuntimeExecutionRequest, agentID string) (context.Context, *claims.TestamentAccumulator) {
	if ctx == nil {
		ctx = context.Background()
	}
	acc := claims.AccumulatorFromContext(ctx)
	if acc != nil {
		return ctx, acc
	}
	sessionID := ""
	claimID := ""
	if req.Claim != nil {
		sessionID = req.Claim.SessionID
		claimID = req.Claim.ID
	}
	if req.Board != nil {
		sessionID = firstClaimsToolString(sessionID, req.Board.SessionID())
	}
	acc = claims.NewTestamentAccumulator(agentID, sessionID).WithBoard(req.Board).WithClaimID(claimID)
	return claims.WithTestamentAccumulator(ctx, acc), acc
}

func claimsToolGuardianClaimID(acc *claims.TestamentAccumulator) string {
	for _, artifact := range claimsToolArtifacts(acc) {
		if value, ok := artifact.Metadata["guardian_claim_id"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func claimsToolOutputRefs(acc *claims.TestamentAccumulator) []string {
	artifacts := claimsToolArtifacts(acc)
	out := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.ID) != "" {
			out = append(out, artifact.ID)
		}
	}
	return out
}

func claimsToolArtifacts(acc *claims.TestamentAccumulator) []*claims.Artifact {
	if acc == nil {
		return nil
	}
	return acc.Artifacts()
}

func (b *ClaimsBackend) summaryLimit(data claims.ToolRuntimeExecutionArtifactData, output string) int {
	if b.outputSummaryLimit > 0 {
		return b.outputSummaryLimit
	}
	if len(data.OutputSummary) > 0 {
		return len(data.OutputSummary)
	}
	return len(output)
}

func boundedClaimsToolText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}

func claimsToolFailureExitStatus() int {
	return 1
}

func claimsToolStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func claimsToolNestedStringArg(args map[string]any, parent, key string) string {
	if args == nil {
		return ""
	}
	parentValue, ok := args[parent].(map[string]string)
	if ok {
		return strings.TrimSpace(parentValue[key])
	}
	anyMap, ok := args[parent].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := anyMap[key].(string)
	return strings.TrimSpace(text)
}

func claimsToolDeniedDecision(current string) string {
	current = strings.TrimSpace(current)
	if current != "" && current != "allowed" {
		return current
	}
	return "denied"
}

func claimsToolContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func firstClaimsToolClock(clock func() time.Time) func() time.Time {
	if clock != nil {
		return clock
	}
	return time.Now
}

func firstClaimsToolString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstClaimsToolTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func firstClaimsToolInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
