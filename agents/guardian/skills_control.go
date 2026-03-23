package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

type guardianDirectSourceAgentIDKey struct{}

func withGuardianDirectSourceAgentID(ctx context.Context, sourceAgentID string) context.Context {
	return context.WithValue(ctx, guardianDirectSourceAgentIDKey{}, strings.TrimSpace(sourceAgentID))
}

func guardianDirectSourceAgentIDFromContext(ctx context.Context) string {
	source, _ := ctx.Value(guardianDirectSourceAgentIDKey{}).(string)
	return strings.TrimSpace(source)
}

func toolExecutionControlSkill(g *Guardian) *skills.Skill {
	return skills.NewSkill("tool_execution_control").
		Description("Validate and issue deterministic Guardian grants for approval-sensitive tool execution requests.").
		Domain("control").
		Keywords("guardian", "approval", "tool", "grant", "control plane").
		Priority(100).
		Usage("Direct-skill only. Accepts a GuardianControlRequest JSON payload and returns a short-lived execution grant.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var req toolruntime.GuardianControlRequest
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid guardian control request payload: %w", err)
			}
			grant, err := g.evaluateToolExecutionControl(guardianDirectSourceAgentIDFromContext(ctx), &req)
			if err != nil {
				return nil, err
			}
			return grant, nil
		}).
		Build()
}

func (g *Guardian) evaluateToolExecutionControl(
	sourceAgentID string,
	req *toolruntime.GuardianControlRequest,
) (*toolruntime.GuardianControlGrant, error) {
	if req == nil {
		return nil, fmt.Errorf("guardian tool control request is required")
	}
	if strings.TrimSpace(req.AgentID) == "" || strings.TrimSpace(req.CorrelationID) == "" || strings.TrimSpace(req.CapabilityScope) == "" {
		return nil, fmt.Errorf("guardian tool control request is missing invocation identity")
	}
	if trimmedSource := strings.TrimSpace(sourceAgentID); trimmedSource != "" && !g.matchesToolControlRequester(trimmedSource, req.AgentID) {
		return nil, fmt.Errorf("guardian tool control source mismatch: %q != %q", req.AgentID, trimmedSource)
	}
	if req.Policy.Execution != toolruntime.ExecutionModeGuardian {
		return nil, fmt.Errorf("tool %q is not marked for guardian-controlled execution", req.ToolName)
	}
	if !req.Policy.ApprovalSensitive {
		return nil, fmt.Errorf("tool %q is not marked approval-sensitive", req.ToolName)
	}
	return &toolruntime.GuardianControlGrant{
		GrantID:           uuid.New().String(),
		AgentID:           req.AgentID,
		CorrelationID:     req.CorrelationID,
		CapabilityScope:   req.CapabilityScope,
		ToolName:          req.ToolName,
		ArgumentsHash:     req.ArgumentsHash,
		PolicyFingerprint: req.PolicyFingerprint,
		Approved:          true,
		Reason:            "guardian-approved deterministic control-plane grant",
		ExpiresAt:         time.Now().Add(30 * time.Second),
	}, nil
}

func (g *Guardian) matchesToolControlRequester(sourceAgentID, requester string) bool {
	sourceAgentID = strings.TrimSpace(sourceAgentID)
	requester = strings.TrimSpace(requester)
	if sourceAgentID == "" || requester == "" {
		return false
	}
	if sourceAgentID == requester {
		return true
	}
	if g == nil {
		return false
	}

	g.knownMu.RLock()
	ann, ok := g.knownAgents[sourceAgentID]
	g.knownMu.RUnlock()
	if !ok || ann == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(ann.AgentType), requester) {
		return true
	}
	for _, alias := range ann.Aliases {
		if strings.EqualFold(strings.TrimSpace(alias), requester) {
			return true
		}
	}
	return false
}
