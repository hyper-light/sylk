package escalation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/handoff"
)

// EscalationRequest is published to the escalation topic when an agent
// determines that escalation is warranted.
type EscalationRequest struct {
	SourceAgent string           `json:"source_agent"`
	TaskID      string           `json:"task_id"`
	Confidence  *ConfidenceLevel `json:"confidence"`
	Target      *EscalationTarget `json:"target"`
	Context     map[string]any   `json:"context,omitempty"`
	Depth       int              `json:"depth"`
	Timestamp   time.Time        `json:"timestamp"`
}

// EscalationResponse is the reply from the escalation handler.
type EscalationResponse struct {
	Accepted   bool      `json:"accepted"`
	Resolution string    `json:"resolution"`
	NewTaskID  string    `json:"new_task_id,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// Escalator evaluates confidence and routes escalation requests.
type Escalator struct {
	policy  *EscalationPolicy
	weights ConfidenceWeights
	history *EscalationHistory

	pendingMu sync.Mutex
	pending   map[string]chan *EscalationResponse
}

// NewEscalator creates an Escalator with the given policy and weights.
func NewEscalator(policy *EscalationPolicy, weights ConfidenceWeights) *Escalator {
	if policy == nil {
		policy = DefaultEscalationPolicy()
	}
	return &Escalator{
		policy:  policy,
		weights: weights,
		history: NewEscalationHistory(64),
		pending: make(map[string]chan *EscalationResponse),
	}
}

// ReportConfidence evaluates confidence against policy thresholds.
// Returns the escalation target if warranted, nil otherwise.
// Does NOT publish to the bus — used by the report_confidence skill.
func (esc *Escalator) ReportConfidence(
	confidence *ConfidenceLevel,
	category handoff.AgentCategory,
	depth int,
) (*EscalationTarget, bool) {
	return DetermineEscalation(confidence, esc.weights, esc.policy, category, esc.history, depth)
}

// RecordEvent records an escalation event in the history.
func (esc *Escalator) RecordEvent(event EscalationEvent) {
	esc.history.Record(event)
}

// Escalate performs a synchronous escalation: evaluate, record, and return.
// The caller is responsible for publishing to the bus if needed.
func (esc *Escalator) Escalate(
	ctx context.Context,
	confidence *ConfidenceLevel,
	category handoff.AgentCategory,
	depth int,
) (*EscalationTarget, error) {
	target, warranted := esc.ReportConfidence(confidence, category, depth)
	if !warranted {
		return nil, nil
	}

	esc.history.Record(EscalationEvent{
		TaskID:     confidence.TaskID,
		Confidence: confidence.Composite(esc.weights),
		Timestamp:  time.Now(),
		Reason:     target.Reason,
		Depth:      depth,
	})

	_ = ctx // reserved for future bus publish with timeout
	return target, nil
}

// BuildEscalationRequest constructs a request from confidence and target.
func BuildEscalationRequest(
	sourceAgent string,
	confidence *ConfidenceLevel,
	target *EscalationTarget,
	depth int,
) *EscalationRequest {
	return &EscalationRequest{
		SourceAgent: sourceAgent,
		TaskID:      confidence.TaskID,
		Confidence:  confidence,
		Target:      target,
		Depth:       depth,
		Timestamp:   time.Now(),
	}
}

// DeliverResponse delivers an escalation response to a pending waiter.
func (esc *Escalator) DeliverResponse(taskID string, resp *EscalationResponse) {
	esc.pendingMu.Lock()
	ch := esc.pending[taskID]
	esc.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

// RegisterPending creates a response channel for a pending escalation.
func (esc *Escalator) RegisterPending(taskID string) <-chan *EscalationResponse {
	ch := make(chan *EscalationResponse, 1)
	esc.pendingMu.Lock()
	esc.pending[taskID] = ch
	esc.pendingMu.Unlock()
	return ch
}

// ClearPending removes a pending escalation channel.
func (esc *Escalator) ClearPending(taskID string) {
	esc.pendingMu.Lock()
	delete(esc.pending, taskID)
	esc.pendingMu.Unlock()
}

// WaitForResponse waits for an escalation response with a timeout.
func (esc *Escalator) WaitForResponse(ctx context.Context, taskID string, timeout time.Duration) (*EscalationResponse, error) {
	ch := esc.RegisterPending(taskID)
	defer esc.ClearPending(taskID)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("escalation response for task %q timed out: %w", taskID, ctx.Err())
	case resp := <-ch:
		return resp, nil
	}
}
