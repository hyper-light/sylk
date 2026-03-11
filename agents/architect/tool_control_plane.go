package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

type architectGuardianControlPlane struct {
	owner *Architect
}

func newArchitectGuardianControlPlane(owner *Architect) toolruntime.GuardianControlPlane {
	return &architectGuardianControlPlane{owner: owner}
}

func (c *architectGuardianControlPlane) RequestGrant(
	ctx context.Context,
	req toolruntime.GuardianControlRequest,
) (*toolruntime.GuardianControlGrant, error) {
	if c == nil || c.owner == nil {
		return nil, fmt.Errorf("architect guardian control plane is not configured")
	}
	if c.owner.bus == nil || !c.owner.running {
		return nil, fmt.Errorf("architect bus is unavailable for guardian tool control")
	}

	sessionID := normalizeSessionID(architectSessionIDFromContext(ctx))
	correlationID := "guardian_" + uuid.NewString()
	targetAgentID := c.guardianAgentID()
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode guardian tool control request: %w", err)
	}

	record := &ArchitectContinuation{
		ID:                      "cont_" + uuid.NewString(),
		Kind:                    continuationKindGuardianApproval,
		State:                   continuationStatusPending,
		PlanID:                  c.planIDForRequest(sessionID, req),
		SessionID:               sessionID,
		TargetAgentID:           targetAgentID,
		ResponseCorrelationID:   correlationID,
		InvocationCorrelationID: req.CorrelationID,
		ToolName:                req.ToolName,
		ToolCallID:              req.ToolID,
		RawArguments:            req.Arguments,
		RequestJSON:             string(payload),
		CreatedAt:               time.Now().UTC(),
		ExpiresAt:               time.Now().UTC().Add(routeSyncTimeout),
	}
	userMessage := guardianPendingMessage(req.ToolName)
	if err := c.owner.recordPendingContinuation(c.planForRequest(sessionID, record.PlanID, req), record, userMessage); err != nil {
		return nil, err
	}

	routeReq := &guide.RouteRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: req.CorrelationID,
		Input:               string(payload),
		TargetAgentID:       targetAgentID,
		SessionID:           sessionID,
		Metadata: map[string]any{
			"direct_skill": "tool_execution_control",
			"tool_name":    req.ToolName,
		},
	}
	if err := c.owner.publishRouteRequest(routeReq); err != nil {
		plan := c.planForRequest(sessionID, record.PlanID, req)
		if plan != nil {
			c.owner.clearPlanPendingContinuationBestEffort(plan, correlationID, "guardian tool control publish failed")
		}
		c.owner.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "guardian tool control publish failed")
		return nil, fmt.Errorf("publish guardian tool control request: %w", err)
	}

	payloadMap := map[string]any{
		"status":         "guardian_approval_pending",
		"tool_name":      req.ToolName,
		"plan_id":        record.PlanID,
		"correlation_id": correlationID,
		"target_agent":   targetAgentID,
		"user_message":   userMessage,
	}
	return nil, skills.NewDelegatedError(payloadMap, userMessage)
}

func guardianPendingMessage(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "route_plan_acceptance":
		return "I'm routing your plan response through Guardian approval now. I'll continue the acceptance flow and update you shortly."
	case "ask_user_question":
		return "I'm routing the clarification request through Guardian approval now. I'll send the question as soon as it's cleared."
	default:
		return "I'm routing this operation through Guardian approval now and will update you shortly."
	}
}

func (c *architectGuardianControlPlane) planIDForRequest(
	sessionID string,
	req toolruntime.GuardianControlRequest,
) string {
	if req.Input != nil {
		if planID, _ := req.Input["plan_id"].(string); strings.TrimSpace(planID) != "" {
			return strings.TrimSpace(planID)
		}
	}
	plan := c.planForRequest(sessionID, "", req)
	if plan == nil {
		return ""
	}
	return plan.ID
}

func (c *architectGuardianControlPlane) planForRequest(
	sessionID string,
	planID string,
	req toolruntime.GuardianControlRequest,
) *DesignPlan {
	if c == nil || c.owner == nil || c.owner.planStore == nil {
		return nil
	}
	if trimmed := strings.TrimSpace(planID); trimmed != "" {
		return c.owner.planStore.Get(trimmed)
	}
	switch strings.TrimSpace(req.ToolName) {
	case "route_plan_acceptance":
		return c.owner.latestReadyPlan(sessionID)
	case "ask_user_question":
		return c.owner.latestConsultingPlan(sessionID)
	default:
		return nil
	}
}

func (c *architectGuardianControlPlane) guardianAgentID() string {
	if c == nil || c.owner == nil {
		return "guardian"
	}
	return c.owner.knownAgentIDByType("guardian", "guardian")
}

func (a *Architect) knownAgentIDByType(agentType, fallback string) string {
	if a == nil {
		return fallback
	}
	targetType := strings.TrimSpace(agentType)
	if targetType == "" {
		return fallback
	}
	a.knownAgentsMu.RLock()
	defer a.knownAgentsMu.RUnlock()
	for _, ann := range a.knownAgents {
		if ann == nil {
			continue
		}
		if ann.AgentType == targetType && strings.TrimSpace(ann.AgentID) != "" {
			return strings.TrimSpace(ann.AgentID)
		}
	}
	return fallback
}
