package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
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

	sessionID := g.activeSessionID
	requester := strings.TrimSpace(req.AgentID)

	// Post claim: agent requests guardian-controlled tool grant.
	g.guardianPostClaim(context.Background(),
		guardianClaimAction(claims.ActionTypeTask),
		guardianExternalClaim(
			"Grant execution of `"+req.ToolName+"` for "+requester,
			"Agent requests guardian-controlled tool execution grant",
			requester,
			[]claims.ClaimScopeEntry{
				{Kind: "tool", Key: req.ToolName},
				{Kind: "agent", Key: requester},
			},
			claims.ActionTypeTask,
			[]*claims.Validation{
				guardianValidation(claims.ValidationTypeInspection, true, "Tool execution mode is guardian-controlled", "ToolPolicy.ExecutionMode == GuardianControlled"),
				guardianValidation(claims.ValidationTypeInspection, true, "Requester identity matches declared agent", "SourceAgentID matches or aliases to request agent"),
				guardianValidation(claims.ValidationTypeInspection, true, "Tool is flagged approval-sensitive", "ToolPolicy.ApprovalSensitive == true"),
			},
		),
	)

	if strings.TrimSpace(req.AgentID) == "" || strings.TrimSpace(req.CorrelationID) == "" || strings.TrimSpace(req.CapabilityScope) == "" {
		err := fmt.Errorf("guardian tool control request is missing invocation identity")
		g.guardianSubmitTestament(context.Background(), guardianTestamentAction(), guardianTestament(
			sessionID, "Tool grant denied: "+err.Error(), "committed", requester,
			[]*claims.Artifact{guardianArtifact(sessionID, "error", err.Error())},
		))
		return nil, err
	}
	if trimmedSource := strings.TrimSpace(sourceAgentID); trimmedSource != "" && !g.matchesToolControlRequester(trimmedSource, req.AgentID) {
		err := fmt.Errorf("guardian tool control source mismatch: %q != %q", req.AgentID, trimmedSource)
		g.guardianSubmitTestament(context.Background(), guardianTestamentAction(), guardianTestament(
			sessionID, "Tool grant denied: "+err.Error(), "committed", requester,
			[]*claims.Artifact{guardianArtifact(sessionID, "error", err.Error())},
		))
		return nil, err
	}
	if req.Policy.Execution != toolruntime.ExecutionModeGuardian {
		err := fmt.Errorf("tool %q is not marked for guardian-controlled execution", req.ToolName)
		g.guardianSubmitTestament(context.Background(), guardianTestamentAction(), guardianTestament(
			sessionID, "Tool grant denied: "+err.Error(), "committed", requester,
			[]*claims.Artifact{guardianArtifact(sessionID, "error", err.Error())},
		))
		return nil, err
	}
	if !req.Policy.ApprovalSensitive {
		err := fmt.Errorf("tool %q is not marked approval-sensitive", req.ToolName)
		g.guardianSubmitTestament(context.Background(), guardianTestamentAction(), guardianTestament(
			sessionID, "Tool grant denied: "+err.Error(), "committed", requester,
			[]*claims.Artifact{guardianArtifact(sessionID, "error", err.Error())},
		))
		return nil, err
	}

	grant := &toolruntime.GuardianControlGrant{
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
	}
	g.guardianSubmitTestament(context.Background(), guardianTestamentAction(), guardianTestament(
		sessionID,
		"Granted `"+req.ToolName+"` for "+requester+" (30s TTL)",
		"committed", requester,
		[]*claims.Artifact{guardianJSONArtifact(sessionID, "execution_grant", grant)},
	))
	return grant, nil
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
