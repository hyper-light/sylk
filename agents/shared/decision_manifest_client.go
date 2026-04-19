package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/manifest"
	"github.com/google/uuid"
)

// DecisionManifestClient routes manifest_declare / manifest_query actions
// from any agent to the orchestrator over the bus, mirroring the shape of
// CoordinationClient. The client is stateless — wire it once per agent and
// reuse across skill handlers.
type DecisionManifestClient struct {
	Bus             guide.EventBus
	BusProvider     func() guide.EventBus
	SourceAgentID   func() string
	SourceAgentType func() string
	SessionID       func() string
	RegisterPending func(string) <-chan *guide.Message
	ClearPending    func(string)
	Timeout         time.Duration
}

// IsConfigured reports whether the client has the minimum wiring required
// to route a request. False indicates a test/dev environment without a
// live manifest store; gates that check this should degrade gracefully
// (skip enforcement) rather than refuse the underlying operation —
// otherwise pure-Go test fixtures that don't spin up the orchestrator
// can't exercise mutation paths the gate guards.
//
// In production the orchestrator initializes the store at startup and
// every agent wires the client; this method should always return true.
func (c DecisionManifestClient) IsConfigured() bool {
	return c.eventBus() != nil &&
		c.RegisterPending != nil &&
		c.ClearPending != nil &&
		c.SourceAgentID != nil &&
		c.SourceAgentType != nil
}

// Declare publishes a manifest_declare action and returns the resolved
// outcome (the persisted decision plus any conflict the orchestrator
// detected against existing scope-overlapping decisions).
func (c DecisionManifestClient) Declare(ctx context.Context, in manifest.DeclareDecisionInput) (*manifest.DeclareDecisionResult, error) {
	var out manifest.DeclareDecisionResult
	if err := c.request(ctx, manifest.ActionDeclareDecision, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Query publishes a manifest_query action and returns the matching
// decisions plus the resolution-policy winner.
func (c DecisionManifestClient) Query(ctx context.Context, in manifest.QueryDecisionsInput) (*manifest.QueryDecisionsResult, error) {
	var out manifest.QueryDecisionsResult
	if err := c.request(ctx, manifest.ActionQueryDecisions, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c DecisionManifestClient) request(ctx context.Context, action string, payload any, out any) error {
	bus := c.eventBus()
	if bus == nil || c.RegisterPending == nil || c.ClearPending == nil ||
		c.SourceAgentID == nil || c.SourceAgentType == nil {
		return fmt.Errorf("decision manifest client is not configured")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultConsultationTimeout
	}

	correlationID := "dm_" + uuid.NewString()[:12]
	waitCh := c.RegisterPending(correlationID)
	defer c.ClearPending(correlationID)

	req := &guide.ActionRequest{
		CorrelationID:   correlationID,
		SourceAgentID:   strings.TrimSpace(c.SourceAgentID()),
		SourceAgentName: strings.TrimSpace(c.SourceAgentType()),
		TargetAgentID:   "orchestrator",
		Action:          action,
		Data:            payload,
		Timestamp:       time.Now(),
	}
	if err := bus.Publish(guide.TopicGuideRequests, guide.NewActionMessage("", req)); err != nil {
		return fmt.Errorf("publish decision manifest action %s: %w", action, err)
	}

	var msg *guide.Message
	err := RunWithContextLease(ctx, ContextLeaseConfig{
		AttemptTimeout: timeout,
		MaxRefreshes:   DefaultConsultationLeaseRefreshes,
	}, func(waitCtx context.Context) error {
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case msg = <-waitCh:
			return nil
		}
	})
	if err != nil {
		return WrapLeaseTimeoutError(fmt.Sprintf("decision manifest action %s", action), timeout, err)
	}
	if msg == nil {
		return fmt.Errorf("decision manifest action %s returned empty response", action)
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		if errText, ok := msg.GetError(); ok && strings.TrimSpace(errText) != "" {
			return fmt.Errorf("decision manifest action %s failed: %s", action, errText)
		}
		return fmt.Errorf("decision manifest action %s returned invalid response", action)
	}
	if !resp.Success {
		return fmt.Errorf("decision manifest action %s failed: %s", action, resp.Error)
	}
	if out == nil {
		return nil
	}
	encoded, err := json.Marshal(resp.Data)
	if err != nil {
		return fmt.Errorf("encode decision manifest action %s result: %w", action, err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("decode decision manifest action %s result: %w", action, err)
	}
	return nil
}

func (c DecisionManifestClient) eventBus() guide.EventBus {
	if c.BusProvider != nil {
		if bus := c.BusProvider(); bus != nil {
			return bus
		}
	}
	return c.Bus
}
