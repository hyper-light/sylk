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
	"github.com/adalundhe/sylk/core/versioning"
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

func (a *Architect) registerPendingBusWait(correlationID string) *shared.PendingSyncWait {
	wait := shared.NewPendingSyncWait()
	a.pendingMu.Lock()
	if a.pendingBus == nil {
		a.pendingBus = make(map[string]*shared.PendingSyncWait)
	}
	pendingCount := len(a.pendingBus)
	a.pendingBus[correlationID] = wait
	a.pendingMu.Unlock()
	a.logInfo("registerPendingBusWait: registered",
		"correlation_id", correlationID,
		"total_pending", pendingCount+1)
	return wait
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
	switch msg.Type {
	case guide.MessageTypeResponse, guide.MessageTypeError:
		a.pendingMu.Lock()
		wait := a.pendingBus[msg.CorrelationID]
		a.pendingMu.Unlock()
		if wait == nil {
			a.logInfo("deliverPendingBusMessage: no waiter for correlation",
				"correlation_id", msg.CorrelationID,
				"msg_type", string(msg.Type))
			return
		}
		select {
		case wait.Response <- msg:
			a.logInfo("deliverPendingBusMessage: delivered",
				"correlation_id", msg.CorrelationID,
				"msg_type", string(msg.Type))
		default:
			a.logWarn("deliverPendingBusMessage: channel full, message dropped",
				"correlation_id", msg.CorrelationID,
				"msg_type", string(msg.Type))
		}
	case guide.MessageTypeStream:
		delivered := false
		for _, correlationID := range shared.PendingSyncActivityCorrelations(msg) {
			a.pendingMu.Lock()
			wait := a.pendingBus[correlationID]
			a.pendingMu.Unlock()
			if wait == nil {
				continue
			}
			select {
			case wait.Activity <- struct{}{}:
			default:
			}
			delivered = true
		}
		if !delivered {
			a.logInfo("deliverPendingBusMessage: filtered non-terminal message",
				"correlation_id", msg.CorrelationID,
				"msg_type", string(msg.Type))
		}
	default:
		a.logInfo("deliverPendingBusMessage: filtered non-terminal message",
			"correlation_id", msg.CorrelationID,
			"msg_type", string(msg.Type))
	}
}

// routeSyncTimeout bounds how long the architect waits for a bus response.
// If the orchestrator (or any other target) fails to respond within this
// window, the caller receives a timeout error rather than blocking forever.
const routeSyncTimeout = 60 * time.Second

func architectConsultationTimeout(target string) time.Duration {
	return shared.ConsultationInactivityTimeout(target)
}

func (a *Architect) requestRouteSync(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error) {
	if a.bus == nil || !a.running {
		a.logWarn("requestRouteSync: bus unavailable",
			"bus_nil", a.bus == nil, "running", a.running)
		return nil, fmt.Errorf("architect bus is unavailable")
	}
	if req == nil {
		return nil, fmt.Errorf("route request is required")
	}
	waitCtx, release := shared.WithoutDeadlineCancellation(ctx)
	defer release()

	req.Metadata = shared.InheritedBranchMetadata(waitCtx, req.Metadata)
	req.CorrelationID = ensureCorrelationID(req.CorrelationID)
	req.SourceAgentID = a.id
	req.SourceAgentName = "architect"
	if req.ParentCorrelationID == "" {
		if stream, ok := shared.StreamMetadataFromContext(waitCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	req.Metadata = shared.RouteMetadataWithInterAgentBranch(waitCtx, req.Metadata)
	if req.TargetAgentID != "" {
		req.ExplicitTarget = true
	}

	// Propagate the hop count from the incoming ForwardedRequest so the
	// Guide's structural loop detection spans the full request chain.
	if req.Hops == 0 {
		req.Hops = routeHopsFromContext(waitCtx)
	}

	response, err := shared.RetryBusyRouteRequest(waitCtx, req.TargetAgentID, shared.DefaultBusyRetryPolicy(req.TargetAgentID), func(attemptCtx context.Context, _ int) (*guide.Message, error) {
		req.CorrelationID = ensureCorrelationID("")
		req.Timestamp = time.Now()

		a.logInfo("requestRouteSync: BLOCKING WAIT START",
			"target", req.TargetAgentID,
			"correlation_id", req.CorrelationID,
			"timeout", architectConsultationTimeout(req.TargetAgentID).String(),
			"parent_ctx_deadline", contextDeadlineString(ctx),
			"input_len", len(req.Input))

		wait := a.registerPendingBusWait(req.CorrelationID)
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

		blockStart := time.Now()
		response, err := shared.WaitForPendingSyncResponse(
			attemptCtx,
			fmt.Sprintf("route request to %q", req.TargetAgentID),
			architectConsultationTimeout(req.TargetAgentID),
			wait,
		)
		if err != nil {
			elapsed := time.Since(blockStart)
			a.logWarn("requestRouteSync: TIMED OUT",
				"target", req.TargetAgentID,
				"correlation_id", req.CorrelationID,
				"blocked_for", elapsed.String(),
				"timeout", architectConsultationTimeout(req.TargetAgentID).String(),
				"ctx_err", err)
			return nil, err
		}
		if busyErr, ok := shared.BusyRouteResponseMessage(response, req.TargetAgentID); ok {
			return nil, busyErr
		}
		elapsed := time.Since(blockStart)
		a.logInfo("requestRouteSync: RESPONSE RECEIVED",
			"target", req.TargetAgentID,
			"correlation_id", req.CorrelationID,
			"blocked_for", elapsed.String(),
			"response_type", string(response.Type))
		return response, nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
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
	return a.requestConsultationWithMetadata(ctx, target, query, scope, sessionID, nil)
}

func (a *Architect) requestConsultationWithMetadata(
	ctx context.Context,
	target string,
	query string,
	scope string,
	sessionID string,
	metadata map[string]any,
) (*ConsultationEvidence, error) {
	a.logInfo("requestConsultation: entry",
		"target", target,
		"query", truncateString(query, 120),
		"scope", scope,
		"ctx_deadline", contextDeadlineString(ctx))
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = normalizeSessionID(firstNonEmpty(
			architectSessionIDFromContext(ctx),
			versioning.SessionIDFromContext(ctx),
		))
	}
	if !a.isAgentRegistered(target) {
		a.logInfo("requestConsultation: skipped (agent not registered)",
			"target", target)
		return failedConsultation(target, query, scope, "", fmt.Errorf("agent %q is not registered", target)), nil
	}
	req := &guide.RouteRequest{
		Input:         query,
		TargetAgentID: target,
		SessionID:     sessionID,
		Metadata:      shared.CloneMetadataMap(metadata),
	}

	// Session cache lookup BEFORE the live bus round-trip. Three gates
	// (pressure dedup, freshness, coverage) must all pass for a hit;
	// if any gate fails we fall through to a real consultation. The
	// gates are derived from observable quantities — no thresholds.
	// See agents/shared/consultation_cache.go for the full rationale.
	if cacheHit := shared.LookupSessionConsultationCacheWithScope(
		shared.DefaultSessionConsultationCache,
		sessionID,
		target,
		query,
		scope,
		shared.ConsultationResearchDepth(req.Metadata),
		shared.ConsultationDepthFromMetadata(req.Metadata)+1,
	); cacheHit.Hit {
		a.logInfo("requestConsultation: SERVED_FROM_SESSION_CACHE",
			"target", target,
			"query", truncateString(query, 120),
			"cached_query", truncateString(cacheHit.Query, 120),
			"age", time.Since(cacheHit.StoredAt).String(),
			"horizon", cacheHit.FreshnessHorizon.String(),
			"reason", cacheHit.Reason)
		return cachedConsultationEvidence(target, query, scope, cacheHit), nil
	}

	admission := shared.AdmitConsultation(ctx, target, query, req.Metadata)
	if !admission.Allowed {
		return failedConsultation(target, query, scope, "", shared.ConsultationAdmissionError(admission)), nil
	}
	researchDepth := shared.ConsultationResearchDepth(req.Metadata)
	spec := shared.InterAgentBranchSpec{
		Kind:       shared.InterAgentToolEventKindConsult,
		ToolName:   "consult_" + strings.ReplaceAll(strings.TrimSpace(target), "-", "_"),
		AgentTypes: []string{target},
		Summary:    query,
		Args: map[string]any{
			"target": target,
			"query":  query,
			"scope":  scope,
			"depth":  string(researchDepth),
		},
	}
	shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventConsultationSent,
		a.id, sessionID, "", "info",
		&agentlog.ConsultPayload{Target: target})

	consultStart := time.Now()
	response, err := shared.WithInterAgentBranchMessage(ctx, spec, func(branchCtx context.Context, branch shared.InterAgentBranchHandle) (*guide.Message, error) {
		req.Metadata = branch.ApplyMetadata(branchCtx, admission.Metadata)
		resp, reqErr := a.requestRouteSync(branchCtx, req)
		shared.RecordConsultationOutcome(branchCtx, admission.AttemptID, reqErr == nil, shared.ConsultationDataFromMessage(resp), reqErr)
		return resp, reqErr
	})
	elapsed := time.Since(consultStart)

	shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventConsultationRecv,
		a.id, sessionID, req.CorrelationID, "info",
		&agentlog.ConsultPayload{Target: target, Success: err == nil, DurNs: elapsed.Nanoseconds()})

	if err != nil {
		if shared.IsAgentBusyError(err) {
			return failedConsultation(target, query, scope, req.CorrelationID, err), shared.ConsultationBusyDelegatedError(target, err)
		}
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
	evidence := buildConsultationEvidence(target, query, scope, req.CorrelationID, response)

	// Write the response into the per-session cache so future
	// near-duplicate consults can be served without a bus round-trip.
	// FreshnessHorizon is read from the response's freshness_horizon
	// field — agents that want their answers cacheable commit to a
	// validity window. Missing/zero ⇒ cache stores the entry but the
	// lookup gate refuses to serve it (explicit contract).
	if evidence != nil {
		shared.StoreSessionConsultationCacheWithScope(
			shared.DefaultSessionConsultationCache,
			sessionID,
			target,
			query,
			scope,
			evidence.Data,
			shared.ExtractFreshnessHorizon(evidence.Data),
			0, // reward: not yet computed at write time; cache uses similarity, not reward, for serve decisions
		)
	}
	return evidence, nil
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

// cachedConsultationEvidence wraps a session-cache hit as a fully
// formed ConsultationEvidence so callers can't tell the answer
// came from cache versus fresh bus call (other than via debug logs
// and the "cached" correlation prefix). Reuses the cached payload
// and timestamps the new evidence with the original storage time
// for staleness telemetry.
func cachedConsultationEvidence(target, query, scope string, hit shared.SessionCacheLookupResult) *ConsultationEvidence {
	now := time.Now()
	return &ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: "cached:" + uuidShortFromTime(hit.StoredAt),
		Success:     true,
		Data:        hit.Response,
		RequestedAt: now,
		ReceivedAt:  hit.StoredAt,
	}
}

// uuidShortFromTime renders a stable short identifier from the
// cached entry's storage timestamp. Used as the synthetic
// Correlation prefix for cache-served evidence so logs / telemetry
// can trace which prior consultation an answer was reused from.
func uuidShortFromTime(t time.Time) string {
	return t.UTC().Format("20060102T150405.000000")
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
	// Phase 2.6 refactor: read_research_paper collapsed into
	// academic_research(action=read). Inject the action field into
	// the incoming payload and invoke the merged skill.
	data, _ := req.Data.(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	data["action"] = "read"
	payload, err := json.Marshal(data)
	if err != nil {
		return a.publishActionFailure(req, err)
	}
	result := a.InvokeSkill(ctx, "academic_research", payload)
	if req.FireAndForget {
		return nil
	}
	if result == nil {
		return a.publishActionFailure(req, fmt.Errorf("academic_research returned nil result"))
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
	a.logInfo("INTERRUPT_DEBUG: architect_cancel_action_applied",
		"correlation_id", correlationID,
		"session_id", sessionID,
		"cancelled_count", cancelledCount,
		"fire_and_forget", req.FireAndForget,
	)

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
