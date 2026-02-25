package engineer

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/google/uuid"
)

// routeSyncTimeout bounds how long the engineer waits for a bus response.
// Uses the shared default rather than a local magic number.
var routeSyncTimeout = shared.DefaultConsultationTimeout

func (e *Engineer) registerPendingConsult(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	e.pendingMu.Lock()
	e.pendingConsults[correlationID] = ch
	e.pendingMu.Unlock()
	return ch
}

func (e *Engineer) clearPendingConsult(correlationID string) {
	e.pendingMu.Lock()
	delete(e.pendingConsults, correlationID)
	e.pendingMu.Unlock()
}

// deliverConsultResponse delivers terminal messages (response or error) to
// synchronous waiters. Stream events are filtered out.
func (e *Engineer) deliverConsultResponse(msg *guide.Message) {
	if msg == nil || msg.CorrelationID == "" {
		return
	}
	if msg.Type != guide.MessageTypeResponse && msg.Type != guide.MessageTypeError {
		return
	}
	e.pendingMu.Lock()
	ch := e.pendingConsults[msg.CorrelationID]
	e.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

func (e *Engineer) publishConsultRequest(req *guide.RouteRequest) error {
	if req == nil {
		return fmt.Errorf("route request is required")
	}
	if req.CorrelationID == "" {
		req.CorrelationID = "corr_" + uuid.NewString()
	}
	req.SourceAgentID = e.id
	req.SourceAgentName = "engineer"
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}
	msg := guide.NewRequestMessage(e.generateMessageID(), req)
	return e.bus.Publish(guide.TopicGuideRequests, msg)
}

// requestConsultSync publishes a RouteRequest and waits synchronously for
// the response, bounded by routeSyncTimeout.
func (e *Engineer) requestConsultSync(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error) {
	if e.bus == nil || !e.running {
		return nil, fmt.Errorf("engineer bus is unavailable")
	}
	if req == nil {
		return nil, fmt.Errorf("route request is required")
	}
	if req.CorrelationID == "" {
		req.CorrelationID = "corr_" + uuid.NewString()
	}

	waitCh := e.registerPendingConsult(req.CorrelationID)
	defer e.clearPendingConsult(req.CorrelationID)

	if err := e.publishConsultRequest(req); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, routeSyncTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("consultation to %q timed out after %s: %w",
			req.TargetAgentID, routeSyncTimeout, ctx.Err())
	case response := <-waitCh:
		return response, nil
	}
}

// requestConsultation is the high-level consultation helper that builds a
// RouteRequest, calls requestConsultSync, and returns ConsultationEvidence.
func (e *Engineer) requestConsultation(
	ctx context.Context,
	target, query, scope, sessionID string,
) (*shared.ConsultationEvidence, error) {
	req := &guide.RouteRequest{
		Input:         query,
		TargetAgentID: target,
		SessionID:     sessionID,
	}
	response, err := e.requestConsultSync(ctx, req)
	if err != nil {
		return failedConsultEvidence(target, query, scope, req.CorrelationID, err), err
	}
	return buildConsultEvidence(target, query, scope, req.CorrelationID, response), nil
}

func buildConsultEvidence(
	target, query, scope, correlationID string,
	msg *guide.Message,
) *shared.ConsultationEvidence {
	evidence := &shared.ConsultationEvidence{
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

func failedConsultEvidence(target, query, scope, corr string, err error) *shared.ConsultationEvidence {
	evidence := &shared.ConsultationEvidence{
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
