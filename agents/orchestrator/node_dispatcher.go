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
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/container"
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
// Per-node activation: each dispatched node atomically activates its agent,
// registers it with the Guide, and acquires a demotion guard via the AgentPod.
// Guards are released on node completion or DAG cleanup.
//
// When the pod is nil, the dispatcher falls back to EnsurePodActive on the
// activator (best-effort activation without demotion guards).
type BusNodeDispatcher struct {
	bus                guide.EventBus
	agentID            string
	sessionID          string
	planID             string
	dagID              string
	buffers            *BufferRegistry
	activator          guide.PodActivator // fallback when pod is nil
	pod                *shared.AgentPod   // per-node guard lifecycle manager
	podResolver        func(*dag.Node) *shared.AgentPod
	// podEnsurer returns a fully-ready pod (atomic with VFS binding)
	// for the given node. Called at dispatch entry; on error, the
	// dispatch fails for that node and no work is published. Strict
	// invariant: a pod returned by podEnsurer has its VFS volume
	// configured and its sub-node bookkeeping applied.
	podEnsurer func(ctx context.Context, node *dag.Node) (*shared.AgentPod, error)
	ackTimeout         time.Duration
	pending            sync.Map // nodeID → chan *dag.NodeResult
	dispatchDone       sync.Map // nodeID → chan struct{}
	ackResults         sync.Map // nodeID → *ACKResult
	ackWaiters         sync.Map // nodeID → chan *ACKResult
	nodePods           sync.Map // nodeID → *shared.AgentPod
	waitDispatchPermit func(ctx context.Context, sessionID, planID, dagID string) error
	isExecutionHeld    func(sessionID, planID, dagID, nodeID string) bool
	eventLogger        *agentlog.SessionEventLogger
	lastActivity       sync.Map // nodeID → time.Time
	activityGrace      time.Duration
	contextQuota       *container.ResourceQuota
	specRegistry       *container.AgentSpecRegistry
	contextLeases      sync.Map // nodeID → *container.ContextLease

	// onACK is called when an agent ACKs a node dispatch. Optional.
	onACK func(nodeID string, ack *ACKResult)
	// onGuardReleased is called after the node's guard/pod lease is released.
	onGuardReleased func(nodeID string)

	// nodeClaimIDs tracks the claim ID posted for each node dispatch
	// so OnNodeComplete can submit a testament against it.
	nodeClaimIDs sync.Map // nodeID → string (claimID)
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

// SetPlanID records the plan that owns this DAG. The dispatch permit
// waiter uses (sessionID, planID) to scope execution-hold lookups;
// holds opened under a different plan must not block this DAG.
func (d *BusNodeDispatcher) SetPlanID(planID string) {
	d.planID = strings.TrimSpace(planID)
}

// SetDispatchPermitWaiter installs a callback that may block before new node
// dispatches are published. Used to enforce orchestrator execution holds.
func (d *BusNodeDispatcher) SetDispatchPermitWaiter(fn func(ctx context.Context, sessionID, planID, dagID string) error) {
	d.waitDispatchPermit = fn
}

// SetExecutionHoldChecker installs a callback used to determine whether a
// node belongs to a DAG currently paused by an execution hold.
func (d *BusNodeDispatcher) SetExecutionHoldChecker(fn func(sessionID, planID, dagID, nodeID string) bool) {
	d.isExecutionHeld = fn
}

// SetACKCallback registers a function called on every successful ACK.
func (d *BusNodeDispatcher) SetACKCallback(fn func(nodeID string, ack *ACKResult)) {
	d.onACK = fn
}

// SetGuardReleasedCallback registers a function called after node guard release.
func (d *BusNodeDispatcher) SetGuardReleasedCallback(fn func(nodeID string)) {
	d.onGuardReleased = fn
}

// SetEventLogger installs the session JSONL logger for dispatch traces.
func (d *BusNodeDispatcher) SetEventLogger(el *agentlog.SessionEventLogger) {
	d.eventLogger = el
}

// SetContextBudget installs the adaptive live-context budget controller.
func (d *BusNodeDispatcher) SetContextBudget(quota *container.ResourceQuota, specRegistry *container.AgentSpecRegistry) {
	d.contextQuota = quota
	d.specRegistry = specRegistry
}

// SetPodEnsurer installs a node-aware pod ensurer that lazily
// constructs the pod (atomically with its VFS binding) at dispatch
// time. Errors propagate so dispatch fails before any work is sent.
// Preferred over SetPodResolver for production wiring; the resolver
// is left as a cache-only lookup for code paths that should not
// trigger creation.
func (d *BusNodeDispatcher) SetPodEnsurer(fn func(ctx context.Context, node *dag.Node) (*shared.AgentPod, error)) {
	d.podEnsurer = fn
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
// firstClaimID returns the first non-empty claim ID from a posted
// claims slice, for log decoration only. Empty when no claims or all
// IDs blank.
func firstClaimID(posted []claims.Claim) string {
	for _, c := range posted {
		if c.ID != "" {
			return c.ID
		}
	}
	return ""
}

func (d *BusNodeDispatcher) Dispatch(ctx context.Context, node *dag.Node, parentResults map[string]*dag.NodeResult) (*dag.NodeResult, error) {
	dispatchStart := time.Now()
	taskID, taskSlug := dispatchTaskIdentity(node)
	d.logNodeTrace(node, "dispatch_enter", agentlog.EventTaskDispatched, map[string]any{
		"task_id":    taskID,
		"task_slug":  taskSlug,
		"agent_type": node.AgentType(),
		"co_agents":  node.CoAgents(),
	})
	// Lazy-pod ensure: when an ensurer is installed, build the pod
	// (atomically with its VFS volume) before any other dispatch
	// setup. On failure the entire dispatch fails — no message is
	// published, no claim is posted, no permit is acquired.
	if d.podEnsurer != nil {
		ensureStart := time.Now()
		d.logNodeTrace(node, "dispatch_pod_ensure_begin", agentlog.EventTaskDispatched, map[string]any{
			"task_id": taskID,
		})
		pod, err := d.podEnsurer(ctx, node)
		if err != nil {
			d.logNodeTrace(node, "dispatch_pod_ensure_failed", agentlog.EventError, map[string]any{
				"task_id":     taskID,
				"elapsed_ms":  time.Since(ensureStart).Milliseconds(),
				"error":       err.Error(),
			})
			return nil, fmt.Errorf("dispatch %s: ensure pod: %w", node.ID(), err)
		}
		d.logNodeTrace(node, "dispatch_pod_ensure_done", agentlog.EventTaskDispatched, map[string]any{
			"task_id":    taskID,
			"elapsed_ms": time.Since(ensureStart).Milliseconds(),
			"pod_nil":    pod == nil,
		})
		if pod != nil {
			d.nodePods.Store(node.ID(), pod)
			defer d.nodePods.Delete(node.ID())
		}
	}
	ch := make(chan *dag.NodeResult, 1)
	done := make(chan struct{})
	ackCh := make(chan *ACKResult, 1)
	activityWindow := d.nodeActivityWindow(ctx)
	d.pending.Store(node.ID(), ch)
	d.dispatchDone.Store(node.ID(), done)
	d.ackWaiters.Store(node.ID(), ackCh)
	if d.podEnsurer == nil {
		if pod := d.resolvePod(node); pod != nil {
			d.nodePods.Store(node.ID(), pod)
			defer d.nodePods.Delete(node.ID())
		}
	}
	defer d.pending.Delete(node.ID())
	defer d.dispatchDone.Delete(node.ID())
	defer d.ackWaiters.Delete(node.ID())
	defer d.nodeClaimIDs.Delete(node.ID())
	defer close(done)
	defer d.releaseContextLease(node.ID())

	leaseStart := time.Now()
	d.logNodeTrace(node, "dispatch_context_lease_begin", agentlog.EventTaskDispatched, map[string]any{"task_id": taskID})
	if err := d.acquireContextLease(ctx, node); err != nil {
		d.logNodeTrace(node, "dispatch_context_lease_failed", agentlog.EventError, map[string]any{
			"task_id":    taskID,
			"elapsed_ms": time.Since(leaseStart).Milliseconds(),
			"error":      err.Error(),
		})
		return nil, err
	}
	d.logNodeTrace(node, "dispatch_context_lease_ok", agentlog.EventTaskDispatched, map[string]any{
		"task_id":    taskID,
		"elapsed_ms": time.Since(leaseStart).Milliseconds(),
	})

	// Activate target agent if demoted/cold. This is where
	// pod.HoldForNode runs containers and triggers on-demand agent
	// creators (slow first time per-agent-type if cold-start cost
	// hasn't been amortized).
	activateStart := time.Now()
	d.logNodeTrace(node, "dispatch_activate_begin", agentlog.EventTaskDispatched, map[string]any{
		"task_id":     taskID,
		"agent_types": NodeAgentTypes(node),
	})
	if err := d.activateAgents(ctx, node); err != nil {
		d.logNodeTrace(node, "dispatch_activate_failed", agentlog.EventError, map[string]any{
			"task_id":    taskID,
			"elapsed_ms": time.Since(activateStart).Milliseconds(),
			"error":      err.Error(),
		})
		return nil, err
	}
	d.logNodeTrace(node, "dispatch_activate_ok", agentlog.EventTaskDispatched, map[string]any{
		"task_id":    taskID,
		"elapsed_ms": time.Since(activateStart).Milliseconds(),
	})

	if d.waitDispatchPermit != nil {
		permitStart := time.Now()
		d.logNodeTrace(node, "dispatch_permit_wait_begin", agentlog.EventTaskDispatched, map[string]any{"task_id": taskID})
		if err := d.waitDispatchPermit(ctx, d.sessionID, d.planID, d.dagID); err != nil {
			d.logNodeTrace(node, "dispatch_permit_wait_failed", agentlog.EventError, map[string]any{
				"task_id":    taskID,
				"elapsed_ms": time.Since(permitStart).Milliseconds(),
				"error":      err.Error(),
			})
			return nil, err
		}
		d.logNodeTrace(node, "dispatch_permit_wait_ok", agentlog.EventTaskDispatched, map[string]any{
			"task_id":    taskID,
			"elapsed_ms": time.Since(permitStart).Milliseconds(),
		})
	}

	// Post Action+Claim on session claims board for this DAG node.
	if board := claims.DefaultSessionBoardRegistry().Lookup(d.sessionID); board != nil {
		boardStart := time.Now()
		d.logNodeTrace(node, "dispatch_node_claim_post_begin", agentlog.EventTaskDispatched, map[string]any{"task_id": taskID})
		action := claims.Action{
			AgentID: d.agentID,
			Type:    claims.ActionTypeTask,
		}
		nodeClaim := claims.Claim{
			Title:       "DAG node dispatch: " + node.ID(),
			Description: node.Prompt(),
			ActionType:  claims.ActionTypeTask,
			Scope: []claims.ClaimScopeEntry{
				{Kind: "dag_node", Key: node.ID()},
				{Kind: "dag", Key: d.dagID},
			},
			Relations: shared.AttachCausedByFromContext(ctx, []claims.Relation{
				{Related: d.agentID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: node.AgentType(), RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			}),
			Validations: []*claims.Validation{{
				Type: claims.ValidationTypeReceipt, Required: true,
				Description: "Node execution completed",
				QualityBar:  "agent acknowledges and completes work",
				Status:      claims.ValidationStatusPending,
			}},
		}
		postedClaims := []claims.Claim{nodeClaim}
		if err := board.PostAction(ctx, action, postedClaims); err != nil {
			slog.Warn("node_dispatch_post_claim_failed", "node_id", node.ID(), "task_id", taskID, "error", err.Error())
			d.logNodeTrace(node, "dispatch_node_claim_post_failed", agentlog.EventError, map[string]any{
				"task_id":    taskID,
				"elapsed_ms": time.Since(boardStart).Milliseconds(),
				"error":      err.Error(),
			})
		} else {
			if len(postedClaims) > 0 {
				d.nodeClaimIDs.Store(node.ID(), postedClaims[0].ID)
			}
			d.logNodeTrace(node, "dispatch_node_claim_post_ok", agentlog.EventTaskDispatched, map[string]any{
				"task_id":    taskID,
				"elapsed_ms": time.Since(boardStart).Milliseconds(),
				"claim_id":   firstClaimID(postedClaims),
			})
		}
	} else {
		d.logNodeTrace(node, "dispatch_node_claim_skipped_no_board", agentlog.EventTaskDispatched, map[string]any{
			"task_id":    taskID,
			"session_id": d.sessionID,
		})
	}

	// Build and publish dispatch message with ACK topic.
	ackTopic := d.ackTopicForNode(node.ID())
	msg := d.buildDispatchMessage(node, parentResults, ackTopic)
	d.logNodeTrace(node, "dispatch_message_built", agentlog.EventTaskDispatched, map[string]any{
		"task_id":   taskID,
		"ack_topic": ackTopic,
	})

	// Phase 1: Dispatch + ACK
	publishStart := time.Now()
	d.logNodeTrace(node, "dispatch_phase1_begin", agentlog.EventTaskDispatched, map[string]any{
		"task_id":   taskID,
		"ack_topic": ackTopic,
	})
	ack, err := d.dispatchAndWaitACK(ctx, node, msg, ackTopic, ackCh)
	if err != nil {
		d.logNodeTrace(node, "dispatch_ack_failed", agentlog.EventError, map[string]any{
			"task_id":    taskID,
			"elapsed_ms": time.Since(publishStart).Milliseconds(),
			"total_ms":   time.Since(dispatchStart).Milliseconds(),
			"error":      err.Error(),
		})
		return nil, err
	}
	d.logNodeTrace(node, "dispatch_phase1_done", agentlog.EventTaskDispatched, map[string]any{
		"task_id":    taskID,
		"agent_id":   ack.AgentID,
		"elapsed_ms": time.Since(publishStart).Milliseconds(),
		"total_ms":   time.Since(dispatchStart).Milliseconds(),
	})

	d.ackResults.Store(node.ID(), ack)
	defer d.ackResults.Delete(node.ID())
	d.RecordActivity(node.ID())
	defer d.lastActivity.Delete(node.ID())
	if ack.AgentID != "" {
		d.bindContextLeaseAlias(node.ID(), ack.AgentID)
	}

	// Transition node to Acked — visible to health monitor and WAL.
	node.SetState(dag.NodeStateAcked)

	if d.onACK != nil {
		d.onACK(node.ID(), ack)
	}
	d.logNodeTrace(node, "dispatch_acked", agentlog.EventNodeAcked, map[string]any{
		"task_id":        taskID,
		"ack_agent_id":   ack.AgentID,
		"ack_agent_type": ack.AgentType,
	})

	// Phase 2: Wait for result while enforcing an inactivity lease.
	phase2Start := time.Now()
	d.logNodeTrace(node, "dispatch_phase2_begin", agentlog.EventTaskDispatched, map[string]any{
		"task_id":            taskID,
		"activity_window_ms": activityWindow.Milliseconds(),
	})
	result, resultErr := d.waitForNodeResult(ctx, ch, node, activityWindow)
	if resultErr != nil {
		d.logNodeTrace(node, "dispatch_phase2_failed", agentlog.EventError, map[string]any{
			"task_id":    taskID,
			"elapsed_ms": time.Since(phase2Start).Milliseconds(),
			"total_ms":   time.Since(dispatchStart).Milliseconds(),
			"error":      resultErr.Error(),
		})
		return result, resultErr
	}
	d.logNodeTrace(node, "dispatch_phase2_done", agentlog.EventTaskDispatched, map[string]any{
		"task_id":    taskID,
		"elapsed_ms": time.Since(phase2Start).Milliseconds(),
		"total_ms":   time.Since(dispatchStart).Milliseconds(),
		"state":      resultStateString(result),
	})
	return result, nil
}

// resultStateString returns the node result state name for log
// decoration. Empty when the result is nil.
func resultStateString(r *dag.NodeResult) string {
	if r == nil {
		return ""
	}
	return r.State.String()
}

func (d *BusNodeDispatcher) nodeActivityWindow(ctx context.Context) time.Duration {
	activityWindow := d.activityGrace
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			if remaining > activityWindow {
				activityWindow = remaining
			}
		}
	}
	if activityWindow > 0 {
		return activityWindow
	}
	return time.Minute
}

func (d *BusNodeDispatcher) waitForNodeResult(
	ctx context.Context,
	ch <-chan *dag.NodeResult,
	node *dag.Node,
	activityWindow time.Duration,
) (*dag.NodeResult, error) {
	nodeID := ""
	if node != nil {
		nodeID = node.ID()
	}
	ctxDone := ctx.Done()
	deadlineExtended := false
	holdShielded := false
	ticker := time.NewTicker(activityPollEvery(activityWindow))
	defer ticker.Stop()

	for {
		select {
		case result := <-ch:
			if result != nil {
				d.logNodeTrace(node, "dispatch_result_received", agentlog.EventNodeCompleted, map[string]any{
					"result_state": result.State.String(),
				})
			}
			return result, nil
		case <-ticker.C:
			if d.hasRecentActivityWithin(nodeID, activityWindow) {
				holdShielded = false
				continue
			}
			if d.executionHoldActive(nodeID) {
				if !holdShielded {
					d.logNodeTrace(node, "dispatch_inactivity_shielded", agentlog.EventTaskDispatched, map[string]any{
						"activity_grace_ms": activityWindow.Milliseconds(),
					})
					holdShielded = true
				}
				d.RecordActivity(nodeID)
				continue
			}
			d.logNodeTrace(node, "dispatch_inactivity_timeout", agentlog.EventError, map[string]any{
				"activity_grace_ms": activityWindow.Milliseconds(),
			})
			return nil, context.DeadlineExceeded
		case <-ctxDone:
			if err := ctx.Err(); err != nil {
				if !errors.Is(err, context.DeadlineExceeded) {
					d.logNodeTrace(node, "dispatch_context_done", agentlog.EventError, map[string]any{
						"error": err.Error(),
					})
					return nil, err
				}
				if d.executionHoldActive(nodeID) {
					if !deadlineExtended {
						d.logNodeTrace(node, "dispatch_context_deadline_shielded", agentlog.EventTaskDispatched, map[string]any{
							"error":             err.Error(),
							"activity_grace_ms": activityWindow.Milliseconds(),
						})
					}
					d.RecordActivity(nodeID)
					deadlineExtended = true
					ctxDone = nil
					continue
				}
				if !d.hasRecentActivityWithin(nodeID, activityWindow) {
					d.logNodeTrace(node, "dispatch_context_done", agentlog.EventError, map[string]any{
						"error": err.Error(),
					})
					return nil, err
				}
				if !deadlineExtended {
					d.logNodeTrace(node, "dispatch_context_lease_extended", agentlog.EventTaskDispatched, map[string]any{
						"error":             err.Error(),
						"activity_grace_ms": activityWindow.Milliseconds(),
					})
					deadlineExtended = true
				}
				ctxDone = nil
			}
		}
	}
}

func (d *BusNodeDispatcher) executionHoldActive(nodeID string) bool {
	if d == nil || d.isExecutionHeld == nil {
		return false
	}
	return d.isExecutionHeld(d.sessionID, d.planID, d.dagID, strings.TrimSpace(nodeID))
}

func activityPollEvery(activityWindow time.Duration) time.Duration {
	tickEvery := activityWindow / 2
	if tickEvery <= 0 {
		tickEvery = time.Second
	}
	if tickEvery > time.Second {
		tickEvery = time.Second
	}
	return tickEvery
}

func (d *BusNodeDispatcher) RecordActivity(nodeID string) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return
	}
	d.lastActivity.Store(nodeID, time.Now())
}

func (d *BusNodeDispatcher) hasRecentActivity(nodeID string) bool {
	return d.hasRecentActivityWithin(nodeID, d.activityGrace)
}

func (d *BusNodeDispatcher) hasRecentActivityWithin(nodeID string, activityWindow time.Duration) bool {
	if activityWindow <= 0 {
		activityWindow = time.Minute
	}
	val, ok := d.lastActivity.Load(strings.TrimSpace(nodeID))
	if !ok {
		return false
	}
	ts, ok := val.(time.Time)
	if !ok {
		return false
	}
	return time.Since(ts) <= activityWindow
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
// dispatch. Uses the AgentPod (preferred) for per-node demotion guards
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
		if d.onGuardReleased != nil {
			d.onGuardReleased(nodeID)
		}
		return
	}
	if d.pod != nil {
		d.pod.ReleaseForNode(nodeID)
	}
	if d.onGuardReleased != nil {
		d.onGuardReleased(nodeID)
	}
}

func (d *BusNodeDispatcher) acquireContextLease(ctx context.Context, node *dag.Node) error {
	if d.contextQuota == nil || node == nil {
		return nil
	}
	req := d.contextLeaseRequestForNode(node)
	lease, err := d.contextQuota.TryAcquireContextLease(req)
	if err != nil {
		var deferred *container.ContextBudgetDeferredError
		if errors.As(err, &deferred) {
			return &dag.NodeDeferredError{
				Reason:     deferred.Error(),
				RetryAfter: deferred.RetryAfter,
			}
		}
		return err
	}
	if lease != nil {
		d.contextLeases.Store(node.ID(), lease)
	}
	return nil
}

// WaitForBudget lets the DAG executor pause until quota pressure changes or
// the suggested retry window elapses.
func (d *BusNodeDispatcher) WaitForBudget(ctx context.Context, retryAt time.Time) error {
	waitFor := time.Until(retryAt)
	if waitFor <= 0 {
		return nil
	}
	if d.contextQuota == nil {
		timer := time.NewTimer(waitFor)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	return d.contextQuota.WaitForContextBudget(ctx, waitFor)
}

func (d *BusNodeDispatcher) bindContextLeaseAlias(nodeID, alias string) {
	val, ok := d.contextLeases.Load(strings.TrimSpace(nodeID))
	if !ok {
		return
	}
	lease, _ := val.(*container.ContextLease)
	if lease == nil {
		return
	}
	lease.BindAlias(alias)
}

func (d *BusNodeDispatcher) releaseContextLease(nodeID string) {
	val, ok := d.contextLeases.LoadAndDelete(strings.TrimSpace(nodeID))
	if !ok {
		return
	}
	lease, _ := val.(*container.ContextLease)
	if lease != nil {
		lease.Release()
	}
}

func (d *BusNodeDispatcher) contextLeaseRequestForNode(node *dag.Node) container.ContextLeaseRequest {
	req := container.ContextLeaseRequest{
		ClaimID:         strings.TrimSpace(node.ID()),
		AgentType:       strings.TrimSpace(node.AgentType()),
		PromptBytes:     len(node.Prompt()),
		DependencyCount: len(node.Dependencies()),
		CoAgentCount:    len(node.CoAgents()),
	}
	if ctx := node.Context(); ctx != nil {
		req.ContextFieldCount = len(ctx)
	}

	if d.specRegistry != nil {
		if spec, err := d.specRegistry.SpecForAgent(node.AgentType()); err == nil {
			req.RequestTokens = int64(spec.Resources.ContextWindowRequest)
			req.HardLimitTokens = int64(spec.Resources.ContextWindowLimit)
		}
	}
	if req.RequestTokens <= 0 && req.HardLimitTokens > 0 {
		req.RequestTokens = req.HardLimitTokens * 3 / 4
	}
	return req
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
	if targetAgentID := pipelineWorkerTargetAgentID(d.sessionID, taskID, node.AgentType()); targetAgentID != "" && targetAgentID != node.AgentType() {
		payload["target_agent_id"] = targetAgentID
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
	d.releaseContextLease(nodeID)
	d.ReleaseGuard(nodeID)

	// Submit testament against the claim posted during dispatch.
	if claimIDVal, ok := d.nodeClaimIDs.LoadAndDelete(nodeID); ok {
		claimID, isStr := claimIDVal.(string)
		if !isStr || claimID == "" {
			slog.Warn("node_claim_id_invalid_type", "node_id", nodeID)
		} else if board := claims.DefaultSessionBoardRegistry().Lookup(d.sessionID); board != nil {
			state := "succeeded"
			var artifacts []*claims.Artifact
			if result != nil {
				state = result.State.String()
				artifacts = append(artifacts, &claims.Artifact{
					AgentID: d.agentID, SessionID: d.sessionID,
					Kind: "node_result", Reference: state,
				})
				if result.Error != nil {
					artifacts = append(artifacts, &claims.Artifact{
						AgentID: d.agentID, SessionID: d.sessionID,
						Kind: "error", Reference: result.Error.Error(),
					})
				}
			}
			testament := claims.Testament{
				AgentID:   d.agentID,
				SessionID: d.sessionID,
				Summary:   "Node completed: " + nodeID + " state=" + state,
				Relations: []claims.Relation{
					{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipTestament},
					{Related: d.agentID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				},
				Artifacts: artifacts,
			}
			action := claims.Action{AgentID: d.agentID, Type: claims.ActionTypeTestament}
			tctx, tcancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer tcancel()
			if err := board.SubmitTestaments(tctx, action, []claims.Testament{testament}); err != nil {
				slog.Warn("node_complete_testament_failed", "node_id", nodeID, "error", err.Error())
			}
		}
	}

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
