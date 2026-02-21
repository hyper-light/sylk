package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

func sessionIDFromForwarded(fwd *guide.ForwardedRequest) string {
	if fwd == nil {
		return ""
	}
	return fwd.SessionID
}

func (a *Architect) registerPendingBusWait(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	a.pendingMu.Lock()
	a.pendingBus[correlationID] = ch
	a.pendingMu.Unlock()
	return ch
}

func (a *Architect) clearPendingBusWait(correlationID string) {
	a.pendingMu.Lock()
	delete(a.pendingBus, correlationID)
	a.pendingMu.Unlock()
}

func (a *Architect) deliverPendingBusMessage(msg *guide.Message) {
	if msg == nil || msg.CorrelationID == "" {
		return
	}
	a.pendingMu.Lock()
	ch := a.pendingBus[msg.CorrelationID]
	a.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

func (a *Architect) requestRouteSync(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error) {
	if a.bus == nil || !a.running {
		return nil, fmt.Errorf("architect bus is unavailable")
	}
	if req == nil {
		return nil, fmt.Errorf("route request is required")
	}
	req.CorrelationID = ensureCorrelationID(req.CorrelationID)
	req.SourceAgentID = "architect"
	req.SourceAgentName = "architect"
	req.Timestamp = time.Now()

	waitCh := a.registerPendingBusWait(req.CorrelationID)
	defer a.clearPendingBusWait(req.CorrelationID)

	if err := a.publishRouteRequest(req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-waitCh:
		return response, nil
	}
}

func (a *Architect) publishRouteRequest(req *guide.RouteRequest) error {
	if req == nil {
		return fmt.Errorf("route request is required")
	}
	req.CorrelationID = ensureCorrelationID(req.CorrelationID)
	req.SourceAgentID = "architect"
	req.SourceAgentName = "architect"
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
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
	req := &guide.RouteRequest{
		Input:         query,
		TargetAgentID: target,
		SessionID:     sessionID,
	}
	response, err := a.requestRouteSync(ctx, req)
	if err != nil {
		return failedConsultation(target, query, scope, req.CorrelationID, err), err
	}
	return buildConsultationEvidence(target, query, scope, req.CorrelationID, response), nil
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

func (a *Architect) publishActionSuccess(req *guide.ActionRequest, data any) error {
	resp := &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             true,
		Data:                data,
		RespondingAgentID:   "architect",
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
		RespondingAgentID:   "architect",
		RespondingAgentName: "architect",
	}
	msg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(guide.TopicResponses(req.SourceAgentID, req.SourceAgentID), msg)
}
