package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/google/uuid"
)

func sessionIDFromForwarded(fwd *guide.ForwardedRequest) string {
	if fwd == nil {
		return ""
	}
	return fwd.SessionID
}

// routeHopsKey carries the incoming ForwardedRequest's hop count through
// the request context so that requestRouteSync (and publishRouteRequest)
// can propagate it into outgoing RouteRequests. This enables the Guide's
// structural loop detection across agent-created sub-requests.
type routeHopsKey struct{}

func withRouteHops(ctx context.Context, hops int) context.Context {
	return context.WithValue(ctx, routeHopsKey{}, hops)
}

func routeHopsFromContext(ctx context.Context) int {
	hops, _ := ctx.Value(routeHopsKey{}).(int)
	return hops
}

func (a *Architect) registerPendingBusWait(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	a.pendingMu.Lock()
	pendingCount := len(a.pendingBus)
	a.pendingBus[correlationID] = ch
	a.pendingMu.Unlock()
	a.logInfo("registerPendingBusWait: registered",
		"correlation_id", correlationID,
		"total_pending", pendingCount+1)
	return ch
}

func (a *Architect) clearPendingBusWait(correlationID string) {
	a.pendingMu.Lock()
	delete(a.pendingBus, correlationID)
	pendingCount := len(a.pendingBus)
	a.pendingMu.Unlock()
	a.logInfo("clearPendingBusWait: cleared",
		"correlation_id", correlationID,
		"remaining_pending", pendingCount)
}

func (a *Architect) deliverPendingBusMessage(msg *guide.Message) {
	if msg == nil || msg.CorrelationID == "" {
		return
	}
	// Only deliver terminal messages (response or error) to synchronous
	// waiters. Stream events (start, chunk, complete) arrive on the same
	// channel via the Guide relay and must be filtered out — otherwise
	// requestRouteSync returns a stream event instead of the real response.
	if msg.Type != guide.MessageTypeResponse && msg.Type != guide.MessageTypeError {
		a.logInfo("deliverPendingBusMessage: filtered non-terminal message",
			"correlation_id", msg.CorrelationID,
			"msg_type", string(msg.Type))
		return
	}
	a.pendingMu.Lock()
	ch := a.pendingBus[msg.CorrelationID]
	a.pendingMu.Unlock()
	if ch == nil {
		a.logInfo("deliverPendingBusMessage: no waiter for correlation",
			"correlation_id", msg.CorrelationID,
			"msg_type", string(msg.Type))
		return
	}
	select {
	case ch <- msg:
		a.logInfo("deliverPendingBusMessage: delivered",
			"correlation_id", msg.CorrelationID,
			"msg_type", string(msg.Type))
	default:
		a.logWarn("deliverPendingBusMessage: channel full, message dropped",
			"correlation_id", msg.CorrelationID,
			"msg_type", string(msg.Type))
	}
}

// routeSyncTimeout bounds how long the architect waits for a bus response.
// If the orchestrator (or any other target) fails to respond within this
// window, the caller receives a timeout error rather than blocking forever.
const routeSyncTimeout = 60 * time.Second

func (a *Architect) requestRouteSync(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error) {
	if a.bus == nil || !a.running {
		a.logWarn("requestRouteSync: bus unavailable",
			"bus_nil", a.bus == nil, "running", a.running)
		return nil, fmt.Errorf("architect bus is unavailable")
	}
	if req == nil {
		return nil, fmt.Errorf("route request is required")
	}
	req.CorrelationID = ensureCorrelationID(req.CorrelationID)
	req.SourceAgentID = a.id
	req.SourceAgentName = "architect"
	req.Timestamp = time.Now()

	// When a TargetAgentID is specified, mark the request as explicit so the
	// Guide routes directly instead of reclassifying the input. Without this,
	// plan handoff payloads (which look like planning requests) get routed
	// back to the architect, creating an infinite dispatch loop (OOM).
	if req.TargetAgentID != "" {
		req.ExplicitTarget = true
	}

	// Propagate the hop count from the incoming ForwardedRequest so the
	// Guide's structural loop detection spans the full request chain.
	if req.Hops == 0 {
		req.Hops = routeHopsFromContext(ctx)
	}

	a.logInfo("requestRouteSync: BLOCKING WAIT START",
		"target", req.TargetAgentID,
		"correlation_id", req.CorrelationID,
		"timeout", routeSyncTimeout.String(),
		"parent_ctx_deadline", contextDeadlineString(ctx),
		"input_len", len(req.Input))

	waitCh := a.registerPendingBusWait(req.CorrelationID)
	defer a.clearPendingBusWait(req.CorrelationID)

	publishStart := time.Now()
	if err := a.publishRouteRequest(req); err != nil {
		a.logWarn("requestRouteSync: publish failed",
			"target", req.TargetAgentID,
			"correlation_id", req.CorrelationID,
			"err", err)
		return nil, err
	}
	a.logInfo("requestRouteSync: published, now waiting",
		"target", req.TargetAgentID,
		"correlation_id", req.CorrelationID,
		"publish_elapsed", time.Since(publishStart).String())

	ctx, cancel := context.WithTimeout(ctx, routeSyncTimeout)
	defer cancel()

	blockStart := time.Now()
	select {
	case <-ctx.Done():
		elapsed := time.Since(blockStart)
		a.logWarn("requestRouteSync: TIMED OUT",
			"target", req.TargetAgentID,
			"correlation_id", req.CorrelationID,
			"blocked_for", elapsed.String(),
			"timeout", routeSyncTimeout.String(),
			"ctx_err", ctx.Err())
		return nil, fmt.Errorf("route request to %q timed out after %s: %w",
			req.TargetAgentID, routeSyncTimeout, ctx.Err())
	case response := <-waitCh:
		elapsed := time.Since(blockStart)
		a.logInfo("requestRouteSync: RESPONSE RECEIVED",
			"target", req.TargetAgentID,
			"correlation_id", req.CorrelationID,
			"blocked_for", elapsed.String(),
			"response_type", string(response.Type))
		return response, nil
	}
}

func (a *Architect) publishRouteRequest(req *guide.RouteRequest) error {
	if req == nil {
		return fmt.Errorf("route request is required")
	}
	req.CorrelationID = ensureCorrelationID(req.CorrelationID)
	req.SourceAgentID = a.id
	req.SourceAgentName = "architect"
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}
	if req.TargetAgentID != "" {
		req.ExplicitTarget = true
	}
	msg := guide.NewRequestMessage(a.generateMessageID(), req)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

func (a *Architect) requestConsultation(
	ctx context.Context,
	target string,
	query string,
	scope string,
	sessionID string,
) (*ConsultationEvidence, error) {
	a.logInfo("requestConsultation: entry",
		"target", target,
		"query", truncateString(query, 120),
		"scope", scope,
		"ctx_deadline", contextDeadlineString(ctx))
	if !a.isAgentRegistered(target) {
		a.logInfo("requestConsultation: skipped (agent not registered)",
			"target", target)
		return failedConsultation(target, query, scope, "", fmt.Errorf("agent %q is not registered", target)), nil
	}
	req := &guide.RouteRequest{
		Input:         query,
		TargetAgentID: target,
		SessionID:     sessionID,
	}
	shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventConsultationSent,
		a.id, sessionID, "", "info",
		&agentlog.ConsultPayload{Target: target})

	consultStart := time.Now()
	response, err := a.requestRouteSync(ctx, req)
	elapsed := time.Since(consultStart)

	shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventConsultationRecv,
		a.id, sessionID, req.CorrelationID, "info",
		&agentlog.ConsultPayload{Target: target, Success: err == nil, DurNs: elapsed.Nanoseconds()})

	if err != nil {
		a.logWarn("requestConsultation: FAILED",
			"target", target,
			"elapsed", elapsed.String(),
			"err", err)
		return failedConsultation(target, query, scope, req.CorrelationID, err), err
	}
	a.logInfo("requestConsultation: success",
		"target", target,
		"elapsed", elapsed.String(),
		"correlation_id", req.CorrelationID)
	return buildConsultationEvidence(target, query, scope, req.CorrelationID, response), nil
}

// isAgentRegistered checks whether the target agent has announced itself
// on the registry bus. Returns false if the agent has never registered or
// has since unregistered.
func (a *Architect) isAgentRegistered(target string) bool {
	normalized := strings.ToLower(strings.TrimSpace(target))
	if normalized == "" {
		return false
	}
	a.knownAgentsMu.RLock()
	_, ok := a.knownAgents[normalized]
	a.knownAgentsMu.RUnlock()
	return ok
}

func failedConsultation(target, query, scope, corr string, err error) *ConsultationEvidence {
	evidence := &ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: corr,
		Success:     false,
		RequestedAt: time.Now(),
		ReceivedAt:  time.Now(),
	}
	if err != nil {
		evidence.Error = err.Error()
	}
	return evidence
}

func buildConsultationEvidence(
	target string,
	query string,
	scope string,
	correlationID string,
	msg *guide.Message,
) *ConsultationEvidence {
	evidence := &ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: correlationID,
		RequestedAt: time.Now(),
		ReceivedAt:  time.Now(),
	}
	if msg == nil {
		evidence.Success = false
		evidence.Error = "empty consultation response"
		return evidence
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		evidence.Success = resp.Success
		evidence.Data = resp.Data
		evidence.Error = resp.Error
		return evidence
	}
	if errStr, ok := msg.GetError(); ok {
		evidence.Success = false
		evidence.Error = errStr
		return evidence
	}
	evidence.Success = false
	evidence.Error = "unsupported consultation payload"
	return evidence
}

func ensureCorrelationID(correlationID string) string {
	if correlationID != "" {
		return correlationID
	}
	return "corr_" + uuid.NewString()
}

func (a *Architect) handleProposalAction(ctx context.Context, req *guide.ActionRequest) error {
	return a.handleReadResearchAction(ctx, req)
}

func (a *Architect) handleReadResearchAction(ctx context.Context, req *guide.ActionRequest) error {
	if req == nil {
		return nil
	}
	payload, err := json.Marshal(req.Data)
	if err != nil {
		return a.publishActionFailure(req, err)
	}
	result := a.InvokeSkill(ctx, "read_research_paper", payload)
	if req.FireAndForget {
		return nil
	}
	if result == nil {
		return a.publishActionFailure(req, fmt.Errorf("read_research_paper returned nil result"))
	}
	if !result.Success {
		return a.publishActionFailure(req, errors.New(result.Error))
	}
	return a.publishActionSuccess(req, result.Data)
}

func (a *Architect) handleCancelAction(req *guide.ActionRequest) error {
	if req == nil {
		return nil
	}
	correlationID := cancelCorrelationID(req)
	sessionID := cancelSessionID(req)
	if correlationID == "" && sessionID == "" {
		return a.publishActionFailure(req, errors.New("cancel action missing correlation id or session id"))
	}

	cancelledCount := 0
	if correlationID != "" {
		cancelled := a.cancelInFlight(correlationID)
		if a.steering != nil {
			cancelled = a.steering.CancelRequest(correlationID) || cancelled
		}
		if cancelled {
			cancelledCount = 1
		}
	} else if a.steering != nil {
		cancelledCount = a.steering.CancelSession(sessionID)
	}

	// Supersede any stalled plans for this session so the next request
	// starts fresh rather than recovering old plans with stale intent.
	if sessionID != "" {
		a.supersedeStalledPlans(sessionID)
	}

	if req.FireAndForget {
		return nil
	}
	data := map[string]any{
		"cancelled": cancelledCount > 0,
	}
	if correlationID != "" {
		data["correlation_id"] = correlationID
	}
	if sessionID != "" {
		data["session_id"] = sessionID
		data["cancelled_count"] = cancelledCount
	}
	return a.publishActionSuccess(req, data)
}

func cancelSessionID(req *guide.ActionRequest) string {
	if req == nil {
		return ""
	}
	values, ok := req.Data.(map[string]any)
	if !ok || values == nil {
		return ""
	}
	sid, _ := values["session_id"].(string)
	return strings.TrimSpace(sid)
}

func cancelCorrelationID(req *guide.ActionRequest) string {
	if req == nil {
		return ""
	}
	if correlationID := lookupCancelCorrelation(req.Data); correlationID != "" {
		return correlationID
	}
	return strings.TrimSpace(req.CorrelationID)
}

func lookupCancelCorrelation(data any) string {
	values, ok := data.(map[string]any)
	if !ok || values == nil {
		return ""
	}
	value, ok := values["correlation_id"]
	if !ok {
		return ""
	}
	correlationID, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(correlationID)
}

func (a *Architect) publishActionSuccess(req *guide.ActionRequest, data any) error {
	resp := &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             true,
		Data:                data,
		RespondingAgentID:   a.id,
		RespondingAgentName: "architect",
	}
	msg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(guide.TopicResponses(req.SourceAgentID, req.SourceAgentID), msg)
}

func (a *Architect) publishActionFailure(req *guide.ActionRequest, err error) error {
	if req == nil || req.FireAndForget || err == nil {
		return nil
	}
	resp := &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             false,
		Error:               err.Error(),
		RespondingAgentID:   a.id,
		RespondingAgentName: "architect",
	}
	msg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(guide.TopicResponses(req.SourceAgentID, req.SourceAgentID), msg)
}
