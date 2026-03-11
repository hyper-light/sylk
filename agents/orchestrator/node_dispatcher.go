package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
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
// (HoldPodActive), registers it with the Guide, and acquires a demotion guard
// via the PipelinePod. Guards are released on node completion or DAG cleanup.
//
// When the pod is nil, the dispatcher falls back to EnsurePodActive on the
// activator (best-effort activation without demotion guards).
type BusNodeDispatcher struct {
	bus                guide.EventBus
	agentID            string
	sessionID          string
	dagID              string
	buffers            *BufferRegistry
	activator          guide.PodActivator // fallback when pod is nil
	pod                *shared.AgentPod   // per-node guard lifecycle manager
	podResolver        func(*dag.Node) *shared.AgentPod
	ackTimeout         time.Duration
	pending            sync.Map // nodeID → chan *dag.NodeResult
	dispatchDone       sync.Map // nodeID → chan struct{}
	ackResults         sync.Map // nodeID → *ACKResult
	ackWaiters         sync.Map // nodeID → chan *ACKResult
	nodePods           sync.Map // nodeID → *shared.AgentPod
	waitDispatchPermit func(context.Context, string, string) error
	eventLogger        *agentlog.SessionEventLogger

	// onACK is called when an agent ACKs a node dispatch. Optional.
	onACK func(nodeID string, ack *ACKResult)
}

// compile-time assertion
var _ dag.NodeDispatcher = (*BusNodeDispatcher)(nil)

// NewBusNodeDispatcher creates a new dispatcher wired to the event bus.
//
// The pod is the preferred activation path — it acquires demotion guards,
// registers with the Guide, and provides full observability. When pod is
// nil, the dispatcher falls back to activator.EnsurePodActive (best-effort,
// no demotion guards). Both may be nil for test scenarios.
func NewBusNodeDispatcher(bus guide.EventBus, agentID, sessionID, dagID string, buffers *BufferRegistry, activator guide.PodActivator, pod *shared.AgentPod) *BusNodeDispatcher {
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

// SetDispatchPermitWaiter installs a callback that may block before new node
// dispatches are published. Used to enforce orchestrator execution holds.
func (d *BusNodeDispatcher) SetDispatchPermitWaiter(fn func(context.Context, string, string) error) {
	d.waitDispatchPermit = fn
}

// SetACKCallback registers a function called on every successful ACK.
func (d *BusNodeDispatcher) SetACKCallback(fn func(nodeID string, ack *ACKResult)) {
	d.onACK = fn
}

// SetEventLogger installs the session JSONL logger for dispatch traces.
func (d *BusNodeDispatcher) SetEventLogger(el *agentlog.SessionEventLogger) {
	d.eventLogger = el
}

// SetPodResolver installs a node-aware pod resolver. When present, it
// overrides the single-pod field for dispatch/guard lifecycle.
func (d *BusNodeDispatcher) SetPodResolver(fn func(*dag.Node) *shared.AgentPod) {
	d.podResolver = fn
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
	d.logNodeTrace(node, "dispatch_enter", agentlog.EventTaskDispatched, nil)
	ch := make(chan *dag.NodeResult, 1)
	done := make(chan struct{})
	ackCh := make(chan *ACKResult, 1)
	d.pending.Store(node.ID(), ch)
	d.dispatchDone.Store(node.ID(), done)
	d.ackWaiters.Store(node.ID(), ackCh)
	if pod := d.resolvePod(node); pod != nil {
		d.nodePods.Store(node.ID(), pod)
		defer d.nodePods.Delete(node.ID())
	}
	defer d.pending.Delete(node.ID())
	defer d.dispatchDone.Delete(node.ID())
	defer d.ackWaiters.Delete(node.ID())
	defer close(done)

	// Activate target agent if demoted/cold.
	d.logNodeTrace(node, "dispatch_activate_begin", agentlog.EventTaskDispatched, nil)
	if err := d.activateAgents(ctx, node); err != nil {
		d.logNodeTrace(node, "dispatch_activate_failed", agentlog.EventError, map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}
	d.logNodeTrace(node, "dispatch_activate_ok", agentlog.EventTaskDispatched, nil)

	if d.waitDispatchPermit != nil {
		d.logNodeTrace(node, "dispatch_permit_wait_begin", agentlog.EventTaskDispatched, nil)
		if err := d.waitDispatchPermit(ctx, d.sessionID, d.dagID); err != nil {
			d.logNodeTrace(node, "dispatch_permit_wait_failed", agentlog.EventError, map[string]any{
				"error": err.Error(),
			})
			return nil, err
		}
		d.logNodeTrace(node, "dispatch_permit_wait_ok", agentlog.EventTaskDispatched, nil)
	}

	// Build and publish dispatch message with ACK topic.
	ackTopic := d.ackTopicForNode(node.ID())
	msg := d.buildDispatchMessage(node, parentResults, ackTopic)

	// Phase 1: Dispatch + ACK
	ack, err := d.dispatchAndWaitACK(ctx, node, msg, ackTopic, ackCh)
	if err != nil {
		d.logNodeTrace(node, "dispatch_ack_failed", agentlog.EventError, map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}

	d.ackResults.Store(node.ID(), ack)
	defer d.ackResults.Delete(node.ID())

	// Transition node to Acked — visible to health monitor and WAL.
	node.SetState(dag.NodeStateAcked)

	if d.onACK != nil {
		d.onACK(node.ID(), ack)
	}
	d.logNodeTrace(node, "dispatch_acked", agentlog.EventNodeAcked, map[string]any{
		"ack_agent_id":   ack.AgentID,
		"ack_agent_type": ack.AgentType,
	})

	// Phase 2: Wait for result
	select {
	case result := <-ch:
		if result != nil {
			d.logNodeTrace(node, "dispatch_result_received", agentlog.EventNodeCompleted, map[string]any{
				"result_state": result.State.String(),
			})
		}
		return result, nil
	case <-ctx.Done():
		d.logNodeTrace(node, "dispatch_context_done", agentlog.EventError, map[string]any{
			"error": ctx.Err().Error(),
		})
		return nil, ctx.Err()
	}
}

// dispatchAndWaitACK publishes the task dispatch and waits for ACK.
func (d *BusNodeDispatcher) dispatchAndWaitACK(ctx context.Context, node *dag.Node, msg *guide.Message, ackTopic string, ackCh <-chan *ACKResult) (*ACKResult, error) {
	d.logNodeTrace(node, "dispatch_ack_subscribe_begin", agentlog.EventTaskDispatched, map[string]any{
		"ack_topic":      ackTopic,
		"ack_timeout_ms": d.ackTimeout.Milliseconds(),
	})
	sub, err := d.bus.Subscribe(ackTopic, func(ackMsg *guide.Message) error {
		if ackMsg.Type != guide.MessageTypeAck {
			return nil
		}
		ack := d.extractACKResult(ackMsg)
		d.Acknowledge(node.ID(), ack)
		return nil
	})
	if err != nil {
		d.logNodeTrace(node, "dispatch_ack_subscribe_failed", agentlog.EventError, map[string]any{
			"ack_topic": ackTopic,
			"error":     err.Error(),
		})
		return nil, fmt.Errorf("subscribe ack topic for node %s: %w", node.ID(), err)
	}
	defer sub.Unsubscribe()
	d.logNodeTrace(node, "dispatch_ack_subscribe_ok", agentlog.EventTaskDispatched, map[string]any{
		"ack_topic": ackTopic,
	})

	if err := d.bus.Publish("tasks.dispatch", msg); err != nil {
		d.logNodeTrace(node, "dispatch_publish_failed", agentlog.EventError, map[string]any{
			"ack_topic": ackTopic,
			"error":     err.Error(),
		})
		return nil, fmt.Errorf("dispatch node %s: %w", node.ID(), err)
	}
	d.logNodeTrace(node, "dispatch_published", agentlog.EventTaskDispatched, map[string]any{
		"ack_topic": ackTopic,
	})

	ackDeadline := d.ackTimeout
	ackCtx, ackCancel := context.WithTimeout(ctx, ackDeadline)
	defer ackCancel()

	select {
	case ack := <-ackCh:
		d.logNodeTrace(node, "dispatch_ack_received", agentlog.EventNodeAcked, map[string]any{
			"ack_agent_id":   ack.AgentID,
			"ack_agent_type": ack.AgentType,
		})
		return ack, nil
	case <-ackCtx.Done():
		d.logNodeTrace(node, "dispatch_ack_wait_expired", agentlog.EventError, map[string]any{
			"error": ackCtx.Err().Error(),
		})
		if errors.Is(ackCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, fmt.Errorf("node %s: %w", node.ID(), dag.ErrDispatchNotAcked)
		}
		return nil, ctx.Err()
	}
}

func (d *BusNodeDispatcher) logNodeTrace(node *dag.Node, event string, eventCode agentlog.EventType, data map[string]any) {
	trace := map[string]any{
		"dag_id": d.dagID,
	}
	if node != nil {
		trace["node_id"] = node.ID()
		trace["agent_type"] = node.AgentType()
		taskID, taskSlug := dispatchTaskIdentity(node)
		if taskID != "" {
			trace["task_id"] = taskID
		}
		if taskSlug != "" {
			trace["task_slug"] = taskSlug
		}
		if ctx := node.Context(); ctx != nil {
			if ackTopic, ok := ctx["ack_topic"].(string); ok && strings.TrimSpace(ackTopic) != "" {
				trace["ack_topic"] = strings.TrimSpace(ackTopic)
			}
		}
	}
	for key, value := range data {
		trace[key] = value
	}
	d.logTrace(event, eventCode, trace)
}

// Acknowledge resolves the pending ACK wait for a dispatched node.
// It is safe to call from multiple sources; only the first ACK is retained.
func (d *BusNodeDispatcher) Acknowledge(nodeID string, ack *ACKResult) bool {
	if ack == nil {
		return false
	}
	val, ok := d.ackWaiters.Load(strings.TrimSpace(nodeID))
	if !ok {
		return false
	}
	ch, _ := val.(chan *ACKResult)
	if ch == nil {
		return false
	}
	select {
	case ch <- ack:
		return true
	default:
		return false
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
// with full observability, falling back to EnsurePodActive when no pod is
// available.
func (d *BusNodeDispatcher) activateAgents(ctx context.Context, node *dag.Node) error {
	pod := d.resolvePod(node)

	// Preferred path: pod manages guard lifecycle with observability.
	if pod != nil {
		return pod.HoldForNode(ctx, node.ID(), NodeAgentTypes(node))
	}

	// Fallback: best-effort activation without demotion guards.
	if d.activator == nil {
		return nil
	}

	podID := d.activator.PodForAgent(node.AgentType())
	if err := d.activator.EnsurePodActive(ctx, podID); err != nil {
		return fmt.Errorf("activate %s for node %s: %w", node.AgentType(), node.ID(), err)
	}
	for _, co := range node.CoAgents() {
		coPodID := d.activator.PodForAgent(co)
		if err := d.activator.EnsurePodActive(ctx, coPodID); err != nil {
			return fmt.Errorf("activate co-agent %s for node %s: %w", co, node.ID(), err)
		}
	}
	return nil
}

// ReleaseGuard releases the demotion guard for a specific node via the pod.
// No-op when the pod is nil. Safe to call multiple times.
func (d *BusNodeDispatcher) ReleaseGuard(nodeID string) {
	if pod, ok := d.nodePods.Load(nodeID); ok {
		if resolved, ok := pod.(*shared.AgentPod); ok && resolved != nil {
			resolved.ReleaseForNode(nodeID)
		}
		return
	}
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

func (d *BusNodeDispatcher) resolvePod(node *dag.Node) *shared.AgentPod {
	if d.podResolver != nil && node != nil {
		if pod := d.podResolver(node); pod != nil {
			return pod
		}
	}
	return d.pod
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

	taskID, taskSlug := dispatchTaskIdentity(node)

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
	if taskSlug != "" {
		payload["task_slug"] = taskSlug
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

func dispatchTaskIdentity(node *dag.Node) (string, string) {
	if node == nil {
		return "", ""
	}
	var taskID, taskSlug string
	if ctx := node.Context(); ctx != nil {
		if id, ok := ctx["task_id"].(string); ok {
			taskID = strings.TrimSpace(id)
		}
		if slug, ok := ctx["task_slug"].(string); ok {
			taskSlug = strings.TrimSpace(slug)
		}
	}
	if taskID == "" {
		taskID = strings.TrimSpace(node.ID())
	}
	return taskID, taskSlug
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
