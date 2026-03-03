package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/google/uuid"
)

// defaultACKTimeout is the maximum time to wait for an agent ACK.
const defaultACKTimeout = 10 * time.Second

// ACKResult carries the acknowledgment data from a pipeline agent.
type ACKResult struct {
	AgentID   string
	AgentType string
	AckedAt   time.Time
}

// BusNodeDispatcher implements dag.NodeDispatcher by routing node execution
// to pipeline agents via the EventBus with a two-phase ACK protocol.
//
// Per-node activation: each dispatched node atomically activates its agent
// (HoldActive), registers it with the Guide, and acquires a demotion guard
// via the PipelinePod. Guards are released on node completion or DAG cleanup.
//
// When the pod is nil, the dispatcher falls back to EnsureActive on the
// activator (best-effort activation without demotion guards).
type BusNodeDispatcher struct {
	bus        guide.EventBus
	agentID    string
	sessionID  string
	dagID      string
	buffers    *BufferRegistry
	activator  guide.AgentActivator // fallback when pod is nil
	pod        *PipelinePod         // per-node guard lifecycle manager
	ackTimeout time.Duration
	pending    sync.Map // nodeID → chan *dag.NodeResult
	dispatchDone sync.Map // nodeID → chan struct{}
	ackResults sync.Map // nodeID → *ACKResult

	// onACK is called when an agent ACKs a node dispatch. Optional.
	onACK func(nodeID string, ack *ACKResult)
}

// compile-time assertion
var _ dag.NodeDispatcher = (*BusNodeDispatcher)(nil)

// NewBusNodeDispatcher creates a new dispatcher wired to the event bus.
//
// The pod is the preferred activation path — it acquires demotion guards,
// registers with the Guide, and provides full observability. When pod is
// nil, the dispatcher falls back to activator.EnsureActive (best-effort,
// no demotion guards). Both may be nil for test scenarios.
func NewBusNodeDispatcher(bus guide.EventBus, agentID, sessionID, dagID string, buffers *BufferRegistry, activator guide.AgentActivator, pod *PipelinePod) *BusNodeDispatcher {
	return &BusNodeDispatcher{
		bus:        bus,
		agentID:    agentID,
		sessionID:  sessionID,
		dagID:      dagID,
		buffers:    buffers,
		activator:  activator,
		pod:        pod,
		ackTimeout: defaultACKTimeout,
	}
}

// SetACKTimeout overrides the default ACK timeout.
func (d *BusNodeDispatcher) SetACKTimeout(timeout time.Duration) {
	d.ackTimeout = timeout
}

// SetACKCallback registers a function called on every successful ACK.
func (d *BusNodeDispatcher) SetACKCallback(fn func(nodeID string, ack *ACKResult)) {
	d.onACK = fn
}

// GetACKResult returns the ACK data for a node, or nil if not yet acked.
func (d *BusNodeDispatcher) GetACKResult(nodeID string) *ACKResult {
	val, ok := d.ackResults.Load(nodeID)
	if !ok {
		return nil
	}
	return val.(*ACKResult)
}

// Dispatch sends a node execution request to the bus and blocks until a result
// arrives or the context is cancelled. Uses a two-phase protocol:
//
// Phase 1: Publish task dispatch with ack_topic, wait for agent ACK.
// Phase 2: Block on result channel for execution completion.
func (d *BusNodeDispatcher) Dispatch(ctx context.Context, node *dag.Node, parentResults map[string]*dag.NodeResult) (*dag.NodeResult, error) {
	ch := make(chan *dag.NodeResult, 1)
	done := make(chan struct{})
	d.pending.Store(node.ID(), ch)
	d.dispatchDone.Store(node.ID(), done)
	defer d.pending.Delete(node.ID())
	defer d.dispatchDone.Delete(node.ID())
	defer close(done)

	// Activate target agent if demoted/cold.
	if err := d.activateAgents(ctx, node); err != nil {
		return nil, err
	}

	// Build and publish dispatch message with ACK topic.
	ackTopic := d.ackTopicForNode(node.ID())
	msg := d.buildDispatchMessage(node, parentResults, ackTopic)

	// Phase 1: Dispatch + ACK
	ack, err := d.dispatchAndWaitACK(ctx, node, msg, ackTopic)
	if err != nil {
		return nil, err
	}

	d.ackResults.Store(node.ID(), ack)
	defer d.ackResults.Delete(node.ID())

	// Transition node to Acked — visible to health monitor and WAL.
	node.SetState(dag.NodeStateAcked)

	if d.onACK != nil {
		d.onACK(node.ID(), ack)
	}

	// Phase 2: Wait for result
	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// dispatchAndWaitACK publishes the task dispatch and waits for ACK.
func (d *BusNodeDispatcher) dispatchAndWaitACK(ctx context.Context, node *dag.Node, msg *guide.Message, ackTopic string) (*ACKResult, error) {
	ackCh := make(chan *ACKResult, 1)

	sub, err := d.bus.Subscribe(ackTopic, func(ackMsg *guide.Message) error {
		if ackMsg.Type != guide.MessageTypeAck {
			return nil
		}
		ack := d.extractACKResult(ackMsg)
		select {
		case ackCh <- ack:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe ack topic for node %s: %w", node.ID(), err)
	}
	defer sub.Unsubscribe()

	if err := d.bus.Publish("tasks.dispatch", msg); err != nil {
		return nil, fmt.Errorf("dispatch node %s: %w", node.ID(), err)
	}

	ackDeadline := d.ackTimeout
	ackCtx, ackCancel := context.WithTimeout(ctx, ackDeadline)
	defer ackCancel()

	select {
	case ack := <-ackCh:
		return ack, nil
	case <-ackCtx.Done():
		if errors.Is(ackCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, fmt.Errorf("node %s: %w", node.ID(), dag.ErrDispatchNotAcked)
		}
		return nil, ctx.Err()
	}
}

// extractACKResult parses the ACK payload from a bus message.
func (d *BusNodeDispatcher) extractACKResult(msg *guide.Message) *ACKResult {
	ack := &ACKResult{
		AgentID: msg.SourceAgentID,
		AckedAt: msg.Timestamp,
	}

	payload, ok := msg.Payload.(map[string]any)
	if !ok {
		return ack
	}

	if agentType, ok := payload["agent_type"].(string); ok {
		ack.AgentType = agentType
	}
	if ackedAt, ok := payload["acked_at"].(time.Time); ok {
		ack.AckedAt = ackedAt
	}

	return ack
}

// activateAgents ensures the target agent and co-agents are active before
// dispatch. Uses the PipelinePod (preferred) for per-node demotion guards
// with full observability, falling back to EnsureActive when no pod is
// available.
func (d *BusNodeDispatcher) activateAgents(ctx context.Context, node *dag.Node) error {
	// Preferred path: pod manages guard lifecycle with observability.
	if d.pod != nil {
		return d.pod.HoldForNode(ctx, node.ID(), node)
	}

	// Fallback: best-effort activation without demotion guards.
	if d.activator == nil {
		return nil
	}

	if err := d.activator.EnsureActive(ctx, node.AgentType()); err != nil {
		return fmt.Errorf("activate %s for node %s: %w", node.AgentType(), node.ID(), err)
	}
	for _, co := range node.CoAgents() {
		if err := d.activator.EnsureActive(ctx, co); err != nil {
			return fmt.Errorf("activate co-agent %s for node %s: %w", co, node.ID(), err)
		}
	}
	return nil
}

// ReleaseGuard releases the demotion guard for a specific node via the pod.
// No-op when the pod is nil. Safe to call multiple times.
func (d *BusNodeDispatcher) ReleaseGuard(nodeID string) {
	if d.pod != nil {
		d.pod.ReleaseForNode(nodeID)
	}
}

// ReleaseAllGuards releases all outstanding demotion guards via the pod.
// Called during DAG cleanup to free guards for nodes that didn't complete
// normally. No-op when the pod is nil.
func (d *BusNodeDispatcher) ReleaseAllGuards() {
	if d.pod != nil {
		d.pod.Release()
	}
}

// buildDispatchMessage constructs the task dispatch bus message.
func (d *BusNodeDispatcher) buildDispatchMessage(node *dag.Node, parentResults map[string]*dag.NodeResult, ackTopic string) *guide.Message {
	parentSummaries := make(map[string]any, len(parentResults))
	for id, r := range parentResults {
		parentSummaries[id] = map[string]any{
			"state":  r.State.String(),
			"output": r.Output,
		}
	}

	taskID := uuid.New().String()

	payload := map[string]any{
		"task_id":        taskID,
		"node_id":        node.ID(),
		"dag_id":         d.dagID,
		"agent_type":     node.AgentType(),
		"prompt":         node.Prompt(),
		"context":        node.Context(),
		"parent_results": parentSummaries,
		"ack_topic":      ackTopic,
		"ack_deadline":   time.Now().Add(d.ackTimeout),
	}

	if node.IsCompound() {
		payload["co_agents"] = node.CoAgents()
		payload["collaboration_mode"] = node.CollaborationMode().String()
		payload["max_review_rounds"] = node.MaxReviewRounds()
	}

	return &guide.Message{
		ID:            uuid.New().String(),
		Type:          guide.MessageTypeTaskDispatch,
		SourceAgentID: d.agentID,
		Payload:       payload,
		Metadata: map[string]any{
			"session_id": d.sessionID,
			"dag_id":     d.dagID,
			"ack_topic":  ackTopic,
		},
		Timestamp: time.Now(),
	}
}

// ackTopicForNode returns the unique ACK topic for a node dispatch.
func (d *BusNodeDispatcher) ackTopicForNode(nodeID string) string {
	return "tasks.ack." + d.dagID + "." + nodeID
}

// DispatchDone returns a channel that is closed when the Dispatch call for
// nodeID returns (success or timeout). Returns nil if no dispatch is active
// for the given node; a nil channel blocks forever in select, which is the
// correct fallback for the non-lifecycle case.
func (d *BusNodeDispatcher) DispatchDone(nodeID string) <-chan struct{} {
	if val, ok := d.dispatchDone.Load(nodeID); ok {
		return val.(chan struct{})
	}
	return nil
}

// OnNodeComplete is called by the orchestrator when a pipeline agent responds.
// It releases the node's demotion guard and resolves the pending dispatch.
func (d *BusNodeDispatcher) OnNodeComplete(nodeID string, result *dag.NodeResult) {
	d.ReleaseGuard(nodeID)

	val, ok := d.pending.Load(nodeID)
	if !ok {
		slog.Warn("node complete for unknown node", "node_id", nodeID, "dag_id", d.dagID)
		return
	}
	ch := val.(chan *dag.NodeResult)
	select {
	case ch <- result:
	default:
		// Channel already has a result — drop duplicate
	}
}
