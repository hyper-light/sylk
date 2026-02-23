package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
)

// DAGBridgeConfig configures DAG execution parameters.
type DAGBridgeConfig struct {
	MaxConcurrentDAGs    int           `json:"max_concurrent_dags"`
	DefaultNodeTimeout   time.Duration `json:"default_node_timeout"`
	DefaultRetries       int           `json:"default_retries"`
	MaxConcurrencyPerDAG int           `json:"max_concurrency_per_dag"`
}

// DefaultDAGBridgeConfig returns defaults.
func DefaultDAGBridgeConfig() DAGBridgeConfig {
	return DAGBridgeConfig{
		MaxConcurrentDAGs:    4,
		DefaultNodeTimeout:   5 * time.Minute,
		DefaultRetries:       1,
		MaxConcurrencyPerDAG: 8,
	}
}

// DAGBridgeDeps groups required dependencies for the DAG bridge.
type DAGBridgeDeps struct {
	Store       *Store
	Journal     *OrchestratorJournal
	Buffers     *BufferRegistry
	Scope       *concurrency.GoroutineScope
	ActivityBus *events.ActivityEventBus
	SessionID   string
	AgentID     string
}

// ActiveDAGMeta is bridge-level metadata for a running DAG.
type ActiveDAGMeta struct {
	PlanID     string
	SessionID  string
	Revision   int
	Dispatcher *BusNodeDispatcher
	CancelFunc context.CancelFunc
	StartedAt  time.Time
}

// DAGBridge wires the dag.Scheduler into the orchestrator's bus/WAL/store/buffers.
type DAGBridge struct {
	mu        sync.RWMutex
	scheduler *dag.Scheduler
	bus       guide.EventBus
	store     *Store
	journal   *OrchestratorJournal
	buffers   *BufferRegistry
	scope     *concurrency.GoroutineScope
	config    DAGBridgeConfig
	sessionID string
	agentID   string

	activityBus *events.ActivityEventBus
	activeDAGs  map[string]*ActiveDAGMeta
	unsubs      []func() // scheduler event unsubscribe functions
}

// NewDAGBridge creates a bridge between the DAG scheduler and orchestrator subsystems.
func NewDAGBridge(cfg DAGBridgeConfig, deps DAGBridgeDeps) *DAGBridge {
	schedulerCfg := dag.SchedulerConfig{
		MaxConcurrentDAGs: cfg.MaxConcurrentDAGs,
		DefaultPolicy: dag.ExecutionPolicy{
			FailurePolicy:  dag.FailurePolicyFailFast,
			MaxConcurrency: cfg.MaxConcurrencyPerDAG,
			DefaultTimeout: cfg.DefaultNodeTimeout,
			DefaultRetries: cfg.DefaultRetries,
			RetryBackoff:   time.Second,
		},
		Scope: deps.Scope,
	}

	return &DAGBridge{
		scheduler:   dag.NewScheduler(schedulerCfg, deps.Scope),
		store:       deps.Store,
		journal:     deps.Journal,
		buffers:     deps.Buffers,
		scope:       deps.Scope,
		config:      cfg,
		sessionID:   deps.SessionID,
		agentID:     deps.AgentID,
		activityBus: deps.ActivityBus,
		activeDAGs:  make(map[string]*ActiveDAGMeta),
	}
}

// SetBus sets the event bus after construction (wired during Start).
func (b *DAGBridge) SetBus(bus guide.EventBus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bus = bus
}

// Execute builds/receives a DAG from a plan, journals, persists, and submits to the scheduler.
func (b *DAGBridge) Execute(ctx context.Context, d *dag.DAG, planID, sessionID string) (string, error) {
	b.mu.Lock()
	bus := b.bus
	b.mu.Unlock()

	if bus == nil {
		return "", fmt.Errorf("dag bridge: bus not set")
	}

	// 1. WAL: LogDAGStart
	dagJSON, _ := d.MarshalJSON()
	if err := b.journal.LogDAGStart(d.ID(), string(dagJSON)); err != nil {
		return "", fmt.Errorf("dag bridge: wal start: %w", err)
	}

	// 2. SQLite: InsertDAGExecution
	policyJSON, _ := json.Marshal(d.Policy())
	if err := b.store.InsertDAGExecution(
		d.ID(), planID, sessionID, d.Name(),
		string(policyJSON), string(dagJSON),
		d.LayerCount(), d.NodeCount(),
	); err != nil {
		return "", fmt.Errorf("dag bridge: store insert: %w", err)
	}

	// 3. Create BusNodeDispatcher
	dispatcher := NewBusNodeDispatcher(bus, b.agentID, sessionID, d.ID(), b.buffers)

	// 4. Track active DAG
	dagCtx, dagCancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.activeDAGs[d.ID()] = &ActiveDAGMeta{
		PlanID:     planID,
		SessionID:  sessionID,
		Dispatcher: dispatcher,
		CancelFunc: dagCancel,
		StartedAt:  time.Now(),
	}
	b.mu.Unlock()

	// 5. Subscribe to DAG events for WAL/SQLite forwarding
	unsub := b.scheduler.Subscribe(b.dagEventForwarder(d.ID(), planID))
	b.mu.Lock()
	b.unsubs = append(b.unsubs, unsub)
	b.mu.Unlock()

	// 6. Submit to scheduler (async execution)
	_, err := b.scheduler.Submit(dagCtx, d, dispatcher)
	if err != nil {
		dagCancel()
		b.mu.Lock()
		delete(b.activeDAGs, d.ID())
		b.mu.Unlock()
		b.journal.LogDAGAbort(d.ID(), err.Error())
		b.store.UpdateDAGState(d.ID(), "failed", err.Error())
		return "", fmt.Errorf("dag bridge: submit: %w", err)
	}

	return d.ID(), nil
}

// Cancel cancels a running DAG.
func (b *DAGBridge) Cancel(dagID, reason string) error {
	b.mu.Lock()
	meta, ok := b.activeDAGs[dagID]
	b.mu.Unlock()

	if !ok {
		return b.scheduler.Cancel(dagID)
	}

	meta.CancelFunc()
	b.journal.LogDAGCancel(dagID, reason)
	b.store.UpdateDAGState(dagID, "cancelled", reason)

	b.mu.Lock()
	delete(b.activeDAGs, dagID)
	b.mu.Unlock()

	return nil
}

// CancelAll cancels all running DAGs.
func (b *DAGBridge) CancelAll(reason string) {
	b.mu.Lock()
	ids := make([]string, 0, len(b.activeDAGs))
	for id := range b.activeDAGs {
		ids = append(ids, id)
	}
	b.mu.Unlock()

	for _, id := range ids {
		b.Cancel(id, reason)
	}
}

// Modify applies architect mid-flight modifications to a running DAG.
func (b *DAGBridge) Modify(dagID string, mod *DAGModification) error {
	b.mu.Lock()
	meta, ok := b.activeDAGs[dagID]
	if !ok {
		b.mu.Unlock()
		return dag.ErrDAGNotFound
	}
	meta.Revision++
	revision := meta.Revision
	b.mu.Unlock()

	// WAL + SQLite
	diffJSON, _ := json.Marshal(mod)
	b.journal.LogDAGModify(dagID, revision, string(diffJSON))
	b.store.InsertDAGRevision(dagID, revision, string(diffJSON), mod.Reason)
	return nil
}

// Status returns current status for a DAG from the scheduler.
func (b *DAGBridge) Status(dagID string) (*dag.DAGStatus, error) {
	return b.scheduler.Status(dagID)
}

// List returns all active DAG statuses.
func (b *DAGBridge) List() []*dag.DAGStatus {
	return b.scheduler.List()
}

// NotifyNodeComplete resolves a pending node dispatch.
func (b *DAGBridge) NotifyNodeComplete(nodeID string, result *dag.NodeResult) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, meta := range b.activeDAGs {
		meta.Dispatcher.OnNodeComplete(nodeID, result)
	}
}

// RecoverFromWAL replays incomplete DAGs on startup.
func (b *DAGBridge) RecoverFromWAL(ctx context.Context) error {
	incomplete, err := b.journal.FindIncompleteDAGs()
	if err != nil {
		return fmt.Errorf("dag bridge: find incomplete: %w", err)
	}

	for i := range incomplete {
		entry := &incomplete[i]
		row, err := b.store.GetDAGExecution(entry.DAGID)
		if err != nil || row == nil {
			b.journal.LogDAGAbort(entry.DAGID, "not found in store during recovery")
			continue
		}

		// If the DAG was in a terminal state in the store, just close the WAL entry
		switch row.State {
		case "succeeded", "failed", "cancelled":
			b.journal.LogDAGComplete(entry.DAGID, row.State)
			continue
		}

		// Mark as failed — resumption of partially-complete DAGs requires
		// the architect to re-submit.
		b.journal.LogDAGAbort(entry.DAGID, "crash recovery: marked as failed")
		b.store.UpdateDAGState(entry.DAGID, "failed", "crash recovery: incomplete at startup")
		b.publishActivity(events.EventTypeAgentError,
			fmt.Sprintf("DAG %s recovered from crash (marked failed)", entry.DAGID))
	}

	return nil
}

// Close shuts down the scheduler and unsubscribes events.
func (b *DAGBridge) Close() error {
	b.mu.Lock()
	unsubs := b.unsubs
	b.unsubs = nil
	b.mu.Unlock()

	for _, unsub := range unsubs {
		unsub()
	}
	return b.scheduler.Close()
}

// dagEventForwarder returns an event handler that writes to WAL + SQLite.
func (b *DAGBridge) dagEventForwarder(dagID, planID string) dag.EventHandler {
	return func(event *dag.Event) {
		if event.DAGID != dagID {
			return
		}

		switch event.Type {
		case dag.EventNodeStarted:
			b.journal.LogNodeDispatch(dagID, event.NodeID, "")

		case dag.EventNodeCompleted:
			b.journal.LogNodeResult(dagID, event.NodeID, "succeeded", "")
			b.updateProgressFromScheduler(dagID)

		case dag.EventNodeFailed:
			errMsg := ""
			if v, ok := event.Data["error"]; ok {
				errMsg, _ = v.(string)
			}
			b.journal.LogNodeResult(dagID, event.NodeID, "failed", errMsg)
			b.updateProgressFromScheduler(dagID)

		case dag.EventDAGCompleted:
			b.journal.LogDAGComplete(dagID, "succeeded")
			b.store.UpdateDAGState(dagID, "succeeded", "")
			b.onDAGComplete(dagID, planID)

		case dag.EventDAGFailed:
			errMsg := errorFromEvent(event)
			b.journal.LogDAGComplete(dagID, "failed")
			b.store.UpdateDAGState(dagID, "failed", errMsg)
			b.onDAGFailed(dagID, planID, errMsg)

		case dag.EventDAGCancelled:
			b.journal.LogDAGCancel(dagID, "cancelled via scheduler")
			b.store.UpdateDAGState(dagID, "cancelled", "")
			b.cleanupDAG(dagID)
		}
	}
}

func (b *DAGBridge) updateProgressFromScheduler(dagID string) {
	status, err := b.scheduler.Status(dagID)
	if err != nil {
		return
	}
	succeeded := 0
	failed := 0
	skipped := 0
	for _, state := range status.NodeStates {
		switch state {
		case dag.NodeStateSucceeded:
			succeeded++
		case dag.NodeStateFailed:
			failed++
		case dag.NodeStateSkipped:
			skipped++
		}
	}
	b.store.UpdateDAGProgress(dagID, status.CurrentLayer, succeeded, failed, skipped)
}

func (b *DAGBridge) onDAGComplete(dagID, planID string) {
	b.cleanupDAG(dagID)
	b.publishDAGStatusToBus(dagID, "succeeded", "")
	b.publishActivity(events.EventTypeSuccess, fmt.Sprintf("DAG %s completed successfully", dagID))
}

func (b *DAGBridge) onDAGFailed(dagID, planID, errMsg string) {
	b.cleanupDAG(dagID)
	b.publishDAGStatusToBus(dagID, "failed", errMsg)
	b.publishActivity(events.EventTypeAgentError, fmt.Sprintf("DAG %s failed: %s", dagID, errMsg))
}

func (b *DAGBridge) cleanupDAG(dagID string) {
	b.mu.Lock()
	delete(b.activeDAGs, dagID)
	b.mu.Unlock()
}

func (b *DAGBridge) publishDAGStatusToBus(dagID, state, errMsg string) {
	b.mu.RLock()
	bus := b.bus
	b.mu.RUnlock()

	if bus == nil {
		return
	}

	msg := &guide.Message{
		ID:            generateMessageID(),
		Type:          guide.MessageTypeDAGStatus,
		SourceAgentID: b.agentID,
		Payload: map[string]any{
			"dag_id": dagID,
			"state":  state,
			"error":  errMsg,
		},
		Timestamp: time.Now(),
	}
	bus.Publish("dag.status", msg)
}

func (b *DAGBridge) publishActivity(eventType events.EventType, content string) {
	if b.activityBus == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, b.sessionID, content)
	evt.AgentID = b.agentID
	evt.Data["agent_type"] = "orchestrator"
	evt.Data["agent_name"] = "Orchestrator"
	b.activityBus.Publish(evt)
}

func errorFromEvent(event *dag.Event) string {
	if event.Data == nil {
		return ""
	}
	if v, ok := event.Data["error"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// DAGSnapshots returns snapshot summaries for state_snapshot.
func (b *DAGBridge) DAGSnapshots(limit int) []dagSnap {
	statuses := b.scheduler.List()
	snaps := make([]dagSnap, 0, min(len(statuses), limit))
	for _, s := range statuses {
		b.mu.RLock()
		meta := b.activeDAGs[s.ID]
		b.mu.RUnlock()

		planID := ""
		dur := ""
		if meta != nil {
			planID = meta.PlanID
			dur = time.Since(meta.StartedAt).Truncate(time.Second).String()
		}

		snaps = append(snaps, dagSnap{
			ID:           s.ID,
			PlanID:       planID,
			State:        s.State.String(),
			CurrentLayer: s.CurrentLayer,
			TotalLayers:  s.TotalLayers,
			Progress:     s.Progress,
			NodesFailed:  countNodeState(s.NodeStates, dag.NodeStateFailed),
			Duration:     dur,
		})
		if len(snaps) >= limit {
			break
		}
	}
	return snaps
}

func countNodeState(states map[string]dag.NodeState, target dag.NodeState) int {
	count := 0
	for _, s := range states {
		if s == target {
			count++
		}
	}
	return count
}
