package architect

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

const toolControlAction = "tool_execution_control"

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
	if c.owner.bus == nil {
		return nil, fmt.Errorf("architect bus is unavailable for guardian tool control")
	}

	correlationID := uuid.New().String()
	waitCh := c.owner.registerPendingBusWait(correlationID)
	defer c.owner.clearPendingBusWait(correlationID)

	action := &guide.ActionRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: req.CorrelationID,
		SourceAgentID:       c.owner.id,
		SourceAgentName:     "architect",
		TargetAgentID:       c.guardianAgentID(),
		Action:              toolControlAction,
		Data:                &req,
		Timestamp:           time.Now(),
	}
	msg := guide.NewActionMessage(uuid.New().String(), action)
	if c.owner.channels != nil {
		msg = msg.WithReplyTo(c.owner.channels.Responses)
	}

	targetChannels := guide.NewAgentChannels("guardian", c.guardianAgentID())
	if err := c.owner.bus.Publish(targetChannels.Requests, msg); err != nil {
		return nil, fmt.Errorf("publish guardian tool control request: %w", err)
	}

	controlCtx, cancel := context.WithTimeout(ctx, routeSyncTimeout)
	defer cancel()

	select {
	case <-controlCtx.Done():
		return nil, fmt.Errorf("guardian tool control request timed out: %w", controlCtx.Err())
	case response := <-waitCh:
		if response == nil {
			return nil, fmt.Errorf("guardian tool control response was nil")
		}
		routeResp, ok := response.GetRouteResponse()
		if !ok || routeResp == nil {
			return nil, fmt.Errorf("guardian tool control returned unexpected payload")
		}
		if !routeResp.Success {
			if routeResp.Error != "" {
				return nil, fmt.Errorf("guardian tool control denied execution: %s", routeResp.Error)
			}
			return nil, fmt.Errorf("guardian tool control denied execution")
		}
		switch grant := routeResp.Data.(type) {
		case *toolruntime.GuardianControlGrant:
			return grant, nil
		case toolruntime.GuardianControlGrant:
			return &grant, nil
		default:
			return nil, fmt.Errorf("guardian tool control returned unsupported grant payload %T", routeResp.Data)
		}
	}
}

func (c *architectGuardianControlPlane) guardianAgentID() string {
	if c == nil || c.owner == nil {
		return "guardian"
	}
	c.owner.knownAgentsMu.RLock()
	defer c.owner.knownAgentsMu.RUnlock()
	for _, ann := range c.owner.knownAgents {
		if ann == nil {
			continue
		}
		if ann.AgentType == "guardian" && ann.AgentID != "" {
			return ann.AgentID
		}
	}
	return "guardian"
}
