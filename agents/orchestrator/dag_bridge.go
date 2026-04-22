package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	containerpod "github.com/adalundhe/sylk/core/container/pod"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/pipeline/taskstate"
	"github.com/adalundhe/sylk/core/versioning"
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
		DefaultRetries:       0,
		MaxConcurrencyPerDAG: 8,
	}
}

// DAGBridgeDeps groups required dependencies for the DAG bridge.
type DAGBridgeDeps struct {
	Store        *Store
	Journal      *OrchestratorJournal
	Buffers      *BufferRegistry
	Scope        *concurrency.GoroutineScope
	Orchestrator *Orchestrator
	ActivityPub  events.ActivityPublisher
	Activator    guide.PodActivator // optional on-demand pod activator
	SessionID    string
	AgentID      string
}

// ActiveDAGMeta is bridge-level metadata for a running DAG.
type ActiveDAGMeta struct {
	PlanID         string
	SessionID      string
	Revision       int
	Dispatcher     *BusNodeDispatcher
	Pod            *shared.AgentPod
	TaskPods       map[string]*shared.AgentPod
	TaskPodFactory func(taskID string) (*shared.AgentPod, error)
	CancelFunc     context.CancelFunc
	Unsub          func()
	StartedAt      time.Time

	// SubNodeMap maps expanded sub-node IDs to their original parent
	// node IDs. Populated during pipeline expansion.
	SubNodeMap             map[string]string // subNodeID → parentNodeID
	SubNodeTasks           map[string]taskPipelineRef
	TaskLabels             map[string]string
	ResetPendingTaskPods   map[string]struct{}
	ReleasePendingTaskPods map[string]struct{}
}

// DAGBridge wires the dag.Scheduler into the orchestrator's bus/WAL/store/buffers.
type DAGBridge struct {
	mu                 sync.RWMutex
	scheduler          *dag.Scheduler
	bus                guide.EventBus
	store              *Store
	journal            *OrchestratorJournal
	buffers            *BufferRegistry
	scope              *concurrency.GoroutineScope
	config             DAGBridgeConfig
	activator          guide.PodActivator
	registrar          PipelineRegistrar
	runtime            container.ContainerRuntime
	quota              *container.ResourceQuota
	specReg            *container.AgentSpecRegistry
	sessionVFS         func(sessionID string) *versioning.SessionVFS
	waitDispatchPermit func(ctx context.Context, sessionID, planID, dagID string) error
	isExecutionHeld    func(sessionID, planID, dagID, nodeID string) bool
	sessionID          string
	agentID            string
	logger             *slog.Logger

	activityPub        events.ActivityPublisher
	eventLogger        *agentlog.SessionEventLogger
	orchestrator       *Orchestrator
	activeDAGs         map[string]*ActiveDAGMeta
	gates              map[string]*dag.DecisionGate // per-DAG decision gates
	scribeFactory      shared.ScribeFactory         // optional Scribe factory for pipeline pods
	taskPodMonitorOnce sync.Once
}

// NewDAGBridge creates a bridge between the DAG scheduler and orchestrator subsystems.
func NewDAGBridge(cfg DAGBridgeConfig, deps DAGBridgeDeps) *DAGBridge {
	schedulerCfg := dag.SchedulerConfig{
		MaxConcurrentDAGs: cfg.MaxConcurrentDAGs,
		DefaultPolicy: dag.ExecutionPolicy{
			FailurePolicy:  dag.FailurePolicyContinue,
			MaxConcurrency: cfg.MaxConcurrencyPerDAG,
			DefaultTimeout: cfg.DefaultNodeTimeout,
			DefaultRetries: cfg.DefaultRetries,
			RetryBackoff:   time.Second,
		},
		Scope: deps.Scope,
	}

	b := &DAGBridge{
		scheduler:    dag.NewScheduler(schedulerCfg, deps.Scope),
		store:        deps.Store,
		journal:      deps.Journal,
		buffers:      deps.Buffers,
		scope:        deps.Scope,
		config:       cfg,
		activator:    deps.Activator,
		sessionID:    deps.SessionID,
		agentID:      deps.AgentID,
		activityPub:  deps.ActivityPub,
		orchestrator: deps.Orchestrator,
		activeDAGs:   make(map[string]*ActiveDAGMeta),
		gates:        make(map[string]*dag.DecisionGate),
	}

	b.scheduler.SetDefaultLayerGate(b.compositeGate())
	b.startTaskPodMonitor()
	return b
}

// SetBus sets the event bus after construction (wired during Start).
func (b *DAGBridge) SetBus(bus guide.EventBus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bus = bus
	b.subscribeGlobalReviewEvents(bus)
}

// subscribeGlobalReviewEvents subscribes to global review lifecycle
// events so the DAG bridge can unblock layer progression without the
// orchestrator mediating the global review path. The global inspector
// publishes these events after accepting or rejecting a checkpoint.
func (b *DAGBridge) subscribeGlobalReviewEvents(bus guide.EventBus) {
	if bus == nil {
		return
	}
	_, _ = bus.SubscribeAsync(shared.TopicGlobalReviewComplete, func(msg *guide.Message) error {
		if msg == nil || msg.Payload == nil {
			return nil
		}
		event, ok := msg.Payload.(*shared.GlobalReviewCompleteEvent)
		if !ok {
			// Try JSON decode for cross-process messages.
			raw, jsonOk := msg.Payload.(json.RawMessage)
			if !jsonOk {
				return nil
			}
			var decoded shared.GlobalReviewCompleteEvent
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil
			}
			event = &decoded
		}
		nodeID := strings.TrimSpace(event.NodeID)
		if nodeID == "" {
			return nil
		}
		b.NotifyNodeComplete(nodeID, &dag.NodeResult{
			NodeID:  nodeID,
			State:   dag.NodeStateSucceeded,
			EndTime: time.Now().UTC(),
		})
		return nil
	})
	_, _ = bus.SubscribeAsync(shared.TopicGlobalReviewFailed, func(msg *guide.Message) error {
		if msg == nil || msg.Payload == nil {
			return nil
		}
		event, ok := msg.Payload.(*shared.GlobalReviewFailedEvent)
		if !ok {
			raw, jsonOk := msg.Payload.(json.RawMessage)
			if !jsonOk {
				return nil
			}
			var decoded shared.GlobalReviewFailedEvent
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil
			}
			event = &decoded
		}
		nodeID := strings.TrimSpace(event.NodeID)
		if nodeID == "" {
			return nil
		}
		b.NotifyNodeComplete(nodeID, &dag.NodeResult{
			NodeID:  nodeID,
			State:   dag.NodeStateFailed,
			Error:   fmt.Errorf("%s", strings.TrimSpace(event.Reason)),
			EndTime: time.Now().UTC(),
		})
		return nil
	})
}

// SetActivator sets the on-demand agent activator after construction.
func (b *DAGBridge) SetActivator(a guide.PodActivator) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activator = a
}

// SetRegistrar sets the pipeline registrar that registers activated agents
// with the Guide's routing layer.
func (b *DAGBridge) SetRegistrar(fn PipelineRegistrar) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.registrar = fn
}

// SetTaskPodInfra wires the runtime/spec/VFS dependencies required to build
// real task-scoped pipeline pods.
func (b *DAGBridge) SetTaskPodInfra(
	runtime container.ContainerRuntime,
	specReg *container.AgentSpecRegistry,
	sessionVFS func(sessionID string) *versioning.SessionVFS,
) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runtime = runtime
	b.specReg = specReg
	b.sessionVFS = sessionVFS
}

// SetContextQuota wires the live session token budget used for node admission.
func (b *DAGBridge) SetContextQuota(quota *container.ResourceQuota) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.quota = quota
}

// SetDispatchPermitWaiter installs a callback invoked before a node dispatch
// is published onto the bus. Used by the orchestrator control plane to pause
// new pipeline work while validation/remediation holds are active.
func (b *DAGBridge) SetDispatchPermitWaiter(fn func(ctx context.Context, sessionID, planID, dagID string) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.waitDispatchPermit = fn
}

// SetExecutionHoldChecker installs a callback used to shield active nodes from
// timeout cancellation while the orchestrator has paused a DAG.
func (b *DAGBridge) SetExecutionHoldChecker(fn func(sessionID, planID, dagID, nodeID string) bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.isExecutionHeld = fn
}

// SetLogger sets the structured logger for pipeline pod diagnostics.
func (b *DAGBridge) SetLogger(l *slog.Logger) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logger = l
}

// SetEventLogger sets the JSONL event logger for DAG event instrumentation.
func (b *DAGBridge) SetEventLogger(el *agentlog.SessionEventLogger) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eventLogger = el
}

// SetScribeFactory sets the Scribe factory for pipeline pods.
func (b *DAGBridge) SetScribeFactory(f shared.ScribeFactory) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scribeFactory = f
}

// Execute builds/receives a DAG from a plan, journals, persists, and submits to the scheduler.
func (b *DAGBridge) Execute(ctx context.Context, d *dag.DAG, planID, sessionID string) (string, error) {
	b.mu.Lock()
	bus := b.bus
	b.mu.Unlock()

	if bus == nil {
		return "", fmt.Errorf("dag bridge: bus not set")
	}
	b.logTrace("dag_execute_begin", agentlog.EventRegistryEvent, map[string]any{
		"dag_id":       d.ID(),
		"plan_id":      planID,
		"session_id":   sessionID,
		"nodes_total":  d.NodeCount(),
		"layers_total": d.LayerCount(),
	})

	subNodeMap := map[string]string{}
	b.logTrace("dag_execute_expanded", agentlog.EventRegistryEvent, map[string]any{
		"dag_id":            d.ID(),
		"expanded_nodes":    d.NodeCount(),
		"expanded_layers":   d.LayerCount(),
		"expanded_subnodes": len(subNodeMap),
	})
	if disabled := disableProtocolPipelineNodeTimeouts(d); disabled > 0 {
		b.logTrace("dag_execute_protocol_no_timeout", agentlog.EventRegistryEvent, map[string]any{
			"dag_id":                  d.ID(),
			"protocol_nodes_disabled": disabled,
		})
	}

	// 1. WAL: LogDAGStart
	dagJSON, _ := d.MarshalJSON()
	if err := b.journal.LogDAGStart(d.ID(), string(dagJSON)); err != nil {
		return "", fmt.Errorf("dag bridge: wal start: %w", err)
	}
	shared.LogAgentEvent(b.eventLogger, agentlog.EventDAGStarted,
		b.agentID, sessionID, "", "info",
		&agentlog.DAGPayload{DAGID: d.ID(), State: "started"})

	// 2. SQLite: InsertDAGExecution
	policyJSON, _ := json.Marshal(d.Policy())
	if err := b.store.InsertDAGExecution(
		d.ID(), planID, sessionID, d.Name(),
		string(policyJSON), string(dagJSON),
		d.LayerCount(), d.NodeCount(),
	); err != nil {
		return "", fmt.Errorf("dag bridge: store insert: %w", err)
	}
	b.logTrace("dag_execute_store_inserted", agentlog.EventRegistryEvent, map[string]any{
		"dag_id":       d.ID(),
		"nodes_total":  d.NodeCount(),
		"layers_total": d.LayerCount(),
	})

	// 3. Create task-scoped pipeline pods so task_id is the canonical
	// execution, routing, and filesystem boundary.
	b.mu.RLock()
	activator := b.activator
	registrar := b.registrar
	logger := b.logger
	runtime := b.runtime
	quota := b.quota
	specReg := b.specReg
	sessionVFS := b.sessionVFS
	waitDispatchPermit := b.waitDispatchPermit
	isExecutionHeld := b.isExecutionHeld
	b.mu.RUnlock()

	b.mu.RLock()
	scribeFactory := b.scribeFactory
	b.mu.RUnlock()

	taskPodDefs := collectPipelineTaskPods(d)
	taskPodDefsByID := indexPipelineTaskPodDefs(taskPodDefs)
	b.logTrace("dag_execute_task_pods_build_begin", agentlog.EventRegistryEvent, map[string]any{
		"dag_id":          d.ID(),
		"task_pod_count":  len(taskPodDefs),
		"task_pod_traces": tracePipelineTaskPodDefs(taskPodDefs),
	})
	taskPods, err := b.buildTaskPods(ctx, taskPodDefs, sessionID, runtime, specReg, registrar, logger, scribeFactory, sessionVFS)
	if err != nil {
		b.logTrace("dag_execute_task_pods_failed", agentlog.EventError, map[string]any{
			"dag_id": d.ID(),
			"error":  err.Error(),
		})
		return "", b.abortPreSubmitDAG(d.ID(), err)
	}
	b.logTrace("dag_execute_task_pods_built", agentlog.EventRegistryEvent, map[string]any{
		"dag_id":          d.ID(),
		"task_pod_count":  len(taskPods),
		"task_pod_traces": tracePipelineTaskPodDefs(taskPodDefs),
	})

	// 3b. Create BusNodeDispatcher with node-aware task-pod activation.
	dispatcher := NewBusNodeDispatcher(bus, b.agentID, sessionID, d.ID(), b.buffers, activator, nil)
	dispatcher.SetPlanID(planID)
	dispatcher.activityGrace = b.config.DefaultNodeTimeout
	dispatcher.SetEventLogger(b.eventLogger)
	dispatcher.SetDispatchPermitWaiter(waitDispatchPermit)
	dispatcher.SetExecutionHoldChecker(isExecutionHeld)
	dispatcher.SetContextBudget(quota, specReg)
	dispatcher.SetPodResolver(func(node *dag.Node) *shared.AgentPod {
		if node == nil {
			return nil
		}
		taskID, _ := dispatchTaskIdentity(node)
		return taskPods[taskID]
	})
	dagID := d.ID()
	dispatcher.SetGuardReleasedCallback(func(string) {
		b.releasePendingCompletedTaskPods(dagID)
		b.maintainTaskPods(context.Background())
	})

	// 3c. Create decision gate for this DAG
	gate := dag.NewDecisionGate(d, b.onNonBlockingFailure, b.onBlockingDecisionRequired)
	b.mu.Lock()
	b.gates[d.ID()] = gate
	b.mu.Unlock()

	// 4. Track active DAG
	//
	// Decouple the DAG lifecycle from the caller's request context.
	// DAGs are async — they outlive the tool-loop request that submitted
	// them. WithoutCancel preserves context values (logging metadata,
	// etc.) while preventing the request's defer-cancel from cascading
	// into a running DAG. Explicit cancellation is still available via
	// Cancel() and CancelAll().
	dagCtx, dagCancel := context.WithCancel(context.WithoutCancel(ctx))
	b.mu.Lock()
	b.activeDAGs[d.ID()] = &ActiveDAGMeta{
		PlanID:     planID,
		SessionID:  sessionID,
		Dispatcher: dispatcher,
		TaskPods:   taskPods,
		TaskPodFactory: func(taskID string) (*shared.AgentPod, error) {
			def, ok := taskPodDefsByID[taskID]
			if !ok {
				return nil, fmt.Errorf("task pod %s: unknown task", taskID)
			}
			svfs := sessionVFS(sessionID)
			if svfs == nil {
				return nil, fmt.Errorf("dag bridge: session VFS not configured for session %s", sessionID)
			}
			return buildTaskAgentPod(def, sessionID, runtime, specReg, registrar, logger, scribeFactory, svfs, b.eventLogger, b.activityPub, b.agentID)
		},
		CancelFunc:             dagCancel,
		StartedAt:              time.Now(),
		SubNodeMap:             subNodeMap,
		SubNodeTasks:           make(map[string]taskPipelineRef, len(subNodeMap)),
		TaskLabels:             make(map[string]string, len(taskPods)),
		ResetPendingTaskPods:   make(map[string]struct{}),
		ReleasePendingTaskPods: make(map[string]struct{}),
	}
	b.mu.Unlock()

	// 5. Subscribe to DAG events for WAL/SQLite forwarding
	unsub := b.scheduler.Subscribe(b.dagEventForwarder(d.ID(), planID))
	b.mu.Lock()
	b.activeDAGs[d.ID()].Unsub = unsub
	b.mu.Unlock()
	b.logTrace("dag_execute_subscribed", agentlog.EventRegistryEvent, map[string]any{
		"dag_id": d.ID(),
	})

	// 6. Register expanded sub-nodes with their task pod for per-node observability.
	//     Sorted iteration ensures deterministic registration order.
	for _, subNodeID := range slices.Sorted(maps.Keys(subNodeMap)) {
		parentNodeID := subNodeMap[subNodeID]
		node, ok := d.GetNode(subNodeID)
		if !ok {
			dagCancel()
			b.cleanupDAG(d.ID())
			errMsg := fmt.Sprintf("sub-node %s missing from expanded DAG", subNodeID)
			b.journal.LogDAGAbort(d.ID(), errMsg)
			b.store.UpdateDAGState(d.ID(), "failed", errMsg)
			return "", fmt.Errorf("dag bridge: %s", errMsg)
		}
		stage := string(StageFromSubNodeID(subNodeID))
		taskID, _ := dispatchTaskIdentity(node)
		b.mu.Lock()
		if meta := b.activeDAGs[d.ID()]; meta != nil {
			taskLabel := ""
			if ctx := node.Context(); ctx != nil {
				taskLabel, _ = ctx["task_slug"].(string)
			}
			meta.SubNodeTasks[subNodeID] = taskPipelineRef{
				TaskID:    taskID,
				TaskLabel: taskLabel,
				Stage:     stage,
				AgentType: node.AgentType(),
			}
			if taskID != "" && taskLabel != "" {
				meta.TaskLabels[taskID] = taskLabel
			}
		}
		b.mu.Unlock()
		if pod := taskPods[taskID]; pod != nil {
			pod.RegisterSubNode(subNodeID, parentNodeID, stage, node.AgentType())
		}
	}

	// 6b. Wire ACK callback now that the task pods are available.
	dispatcher.SetACKCallback(func(nodeID string, ack *ACKResult) {
		b.journal.LogNodeAcked(dagID, nodeID, ack.AgentID)
		if _, isSub := subNodeMap[nodeID]; !isSub {
			return
		}
		node, ok := d.GetNode(nodeID)
		if !ok {
			return
		}
		taskID, _ := dispatchTaskIdentity(node)
		if pod := taskPods[taskID]; pod != nil {
			pod.RecordSubNodeACK(nodeID, ack.AgentID, ack.AckedAt)
		}
	})

	// 7. Submit to scheduler (async execution)
	b.logTrace("dag_execute_submit_begin", agentlog.EventRegistryEvent, map[string]any{
		"dag_id": d.ID(),
	})
	_, err = b.scheduler.Submit(dagCtx, d, dispatcher)
	if err != nil {
		dagCancel()
		b.cleanupDAG(d.ID())
		b.journal.LogDAGAbort(d.ID(), err.Error())
		b.store.UpdateDAGState(d.ID(), "failed", err.Error())
		b.logTrace("dag_execute_submit_failed", agentlog.EventError, map[string]any{
			"dag_id": d.ID(),
			"error":  err.Error(),
		})
		return "", fmt.Errorf("dag bridge: submit: %w", err)
	}
	b.logTrace("dag_execute_submit_ok", agentlog.EventRegistryEvent, map[string]any{
		"dag_id": d.ID(),
	})

	return d.ID(), nil
}

func (b *DAGBridge) abortPreSubmitDAG(dagID string, err error) error {
	if err == nil {
		return nil
	}
	b.journal.LogDAGAbort(dagID, err.Error())
	b.store.UpdateDAGState(dagID, "failed", err.Error())
	return err
}

func (b *DAGBridge) buildTaskPods(
	ctx context.Context,
	taskDefs []pipelineTaskPodDef,
	sessionID string,
	runtime container.ContainerRuntime,
	specReg *container.AgentSpecRegistry,
	registrar PipelineRegistrar,
	logger *slog.Logger,
	scribeFactory shared.ScribeFactory,
	sessionVFSLookup func(sessionID string) *versioning.SessionVFS,
) (map[string]*shared.AgentPod, error) {
	if len(taskDefs) == 0 {
		return nil, nil
	}
	if runtime == nil || specReg == nil || sessionVFSLookup == nil {
		return nil, fmt.Errorf("dag bridge: task pipeline infra not configured")
	}

	svfs := sessionVFSLookup(sessionID)
	if svfs == nil {
		return nil, fmt.Errorf("dag bridge: session VFS not configured for session %s", sessionID)
	}

	taskPods := make(map[string]*shared.AgentPod, len(taskDefs))
	for _, taskDef := range taskDefs {
		b.logTrace("task_pod_build_begin", agentlog.EventRegistryEvent, tracePipelineTaskPodDef(taskDef))
		taskPod, err := buildTaskAgentPod(taskDef, sessionID, runtime, specReg, registrar, logger, scribeFactory, svfs, b.eventLogger, b.activityPub, b.agentID)
		if err != nil {
			b.logTrace("task_pod_build_failed", agentlog.EventError, mergeTraceMaps(tracePipelineTaskPodDef(taskDef), map[string]any{
				"error": err.Error(),
			}))
			releaseTaskPods(taskPods)
			return nil, err
		}
		taskPods[taskDef.TaskID] = taskPod
		b.logTrace("task_pod_build_ready", agentlog.EventRegistryEvent, mergeTraceMaps(tracePipelineTaskPodDef(taskDef), map[string]any{
			"member_types": append([]string(nil), PipelineAgentTypes[:]...),
		}))
	}

	return taskPods, nil
}

type pipelineTaskPodDef struct {
	TaskID   string
	TaskSlug string
	Files    []string
}

func collectPipelineTaskPods(d *dag.DAG) []pipelineTaskPodDef {
	if d == nil {
		return nil
	}
	byTask := make(map[string]*pipelineTaskPodDef)
	for _, node := range d.Nodes() {
		if !isPipelineTaskNode(node) {
			continue
		}
		taskID, taskSlug := dispatchTaskIdentity(node)
		if taskID == "" {
			continue
		}
		def := byTask[taskID]
		if def == nil {
			def = &pipelineTaskPodDef{TaskID: taskID, TaskSlug: taskSlug}
			byTask[taskID] = def
		}
		if def.TaskSlug == "" {
			def.TaskSlug = taskSlug
		}
		def.Files = appendUniqueStrings(def.Files, extractAffectedFiles(node.Context())...)
	}

	taskIDs := slices.Sorted(maps.Keys(byTask))
	result := make([]pipelineTaskPodDef, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		result = append(result, *byTask[taskID])
	}
	return result
}

func isPipelineTaskNode(node *dag.Node) bool {
	if node == nil {
		return false
	}
	switch node.AgentType() {
	case "engineer", "designer", "inspector-pipeline", "tester-pipeline":
		return true
	default:
		return false
	}
}

func disableProtocolPipelineNodeTimeouts(d *dag.DAG) int {
	if d == nil {
		return 0
	}
	disabled := 0
	for _, node := range d.Nodes() {
		if !protocolPipelineNodeEligible(node) {
			continue
		}
		if node.Timeout() != 0 {
			continue
		}
		node.SetTimeout(dag.NoTimeout)
		disabled++
	}
	return disabled
}

func protocolPipelineNodeEligible(node *dag.Node) bool {
	if node == nil {
		return false
	}
	return protocolPipelineWorkerEligible(node.AgentType()) &&
		protocolPipelineStageEligible(taskContextString(node.Context(), "pipeline_stage"))
}

func extractAffectedFiles(ctx map[string]any) []string {
	if len(ctx) == 0 {
		return nil
	}
	var files []string
	var appendPaths func(value any)
	appendPaths = func(value any) {
		switch typed := value.(type) {
		case []string:
			files = appendUniqueStrings(files, typed...)
		case []any:
			for _, entry := range typed {
				switch item := entry.(type) {
				case string:
					files = appendUniqueStrings(files, item)
				case map[string]any:
					if path, _ := item["path"].(string); strings.TrimSpace(path) != "" {
						files = appendUniqueStrings(files, strings.TrimSpace(path))
					}
				}
			}
		case map[string]any:
			for _, key := range []string{"read_set", "write_set", "test_surface", "prefetch_paths"} {
				appendPaths(typed[key])
			}
		}
	}
	appendPaths(ctx["affected_files"])
	appendPaths(ctx["workspace"])
	return files
}

func appendUniqueStrings(dst []string, values ...string) []string {
	if len(values) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, existing := range dst {
		seen[existing] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func buildTaskAgentPod(
	task pipelineTaskPodDef,
	sessionID string,
	runtime container.ContainerRuntime,
	specReg *container.AgentSpecRegistry,
	registrar PipelineRegistrar,
	logger *slog.Logger,
	scribeFactory shared.ScribeFactory,
	svfs *versioning.SessionVFS,
	eventLogger *agentlog.SessionEventLogger,
	activityPub events.ActivityPublisher,
	agentID string,
) (*shared.AgentPod, error) {
	policy := &containerpod.PodPolicy{
		PodID:       task.TaskID,
		PodType:     containerpod.PodTypePipeline,
		MemberTypes: PipelineAgentTypes[:],
		MinTier:     containerpod.TierCold,
		IdleToWarm:  30 * time.Second,
		IdleToCool:  90 * time.Second,
		IdleToCold:  5 * time.Minute,
	}

	volumeManager := containerpod.NewVolumeManager(containerpod.VolumeManagerConfig{
		Volumes: []containerpod.ManagedVolume{
			containerpod.NewVFSVolume(containerpod.VFSVolumeConfig{
				Name:        "workspace",
				PipelineID:  task.TaskID,
				SessionID:   versioning.SessionID(sessionID),
				WorkingDir:  svfs.WorkingDir(),
				SessionVFS:  svfs,
				Files:       task.Files,
				EventLogger: eventLogger,
				AgentID:     agentID,
			}),
		},
		Mounts: map[string]string{
			"engineer":           "workspace",
			"designer":           "workspace",
			"inspector-pipeline": "workspace",
			"tester-pipeline":    "workspace",
		},
	})

	specs := make([]container.ContainerSpec, 0, len(PipelineAgentTypes))
	for _, agentType := range PipelineAgentTypes {
		spec, err := specReg.SpecForAgent(agentType)
		if err != nil {
			return nil, fmt.Errorf("task pod %s: spec for %s: %w", task.TaskID, agentType, err)
		}
		spec.Name = TaskScopedRoutingName(task.TaskSlug, task.TaskID, agentType)
		if spec.Labels == nil {
			spec.Labels = make(map[string]string, 4)
		}
		spec.Labels["pipeline_worker_id"] = shared.PipelineWorkerCanonicalID(sessionID, task.TaskID, agentType)
		spec.Labels["task_id"] = task.TaskID
		if task.TaskSlug != "" {
			spec.Labels["task_slug"] = task.TaskSlug
		}
		specs = append(specs, spec)
	}

	return shared.NewAgentPod(shared.AgentPodConfig{
		PodID:     task.TaskID,
		SessionID: sessionID,
		Registrar: func(ctx context.Context, agentType string) error {
			if registrar == nil {
				return nil
			}
			return registrar(ctx, task.TaskID, agentType)
		},
		ActivityPub:            activityPub,
		RegistrationVisibility: events.VisibilitySystem,
		Logger:                 logger,
		MemberTypes:            PipelineAgentTypes[:],
		DisplayNames:           PipelineAgentDisplayNames,
		EventLogger:            eventLogger,
		ScribeFactory:          scribeFactory,
		Policy:                 policy,
		Runtime:                runtime,
		VolumeManager:          volumeManager,
		Specs:                  specs,
	}), nil
}

func releaseTaskPods(taskPods map[string]*shared.AgentPod) {
	for _, pod := range taskPods {
		if pod == nil {
			continue
		}
		pod.Release()
	}
}

func preActivateTaskPods(
	ctx context.Context,
	dagID string,
	taskPods map[string]*shared.AgentPod,
	taskDefs map[string]pipelineTaskPodDef,
	trace func(string, agentlog.EventType, map[string]any),
) error {
	for _, taskID := range slices.Sorted(maps.Keys(taskPods)) {
		pod := taskPods[taskID]
		if pod == nil {
			continue
		}
		def := taskDefs[taskID]
		if trace != nil {
			trace("task_pod_preactivate_begin", agentlog.EventRegistryEvent, mergeTraceMaps(tracePipelineTaskPodDef(def), map[string]any{
				"dag_id": dagID,
			}))
		}
		if err := pod.PreActivateStrict(ctx); err != nil {
			if trace != nil {
				trace("task_pod_preactivate_failed", agentlog.EventError, mergeTraceMaps(tracePipelineTaskPodDef(def), map[string]any{
					"dag_id": dagID,
					"error":  err.Error(),
				}))
			}
			return fmt.Errorf("task pod %s: pre-activate: %w", taskID, err)
		}
		if trace != nil {
			trace("task_pod_preactivate_done", agentlog.EventRegistryEvent, mergeTraceMaps(tracePipelineTaskPodDef(def), map[string]any{
				"dag_id": dagID,
			}))
		}
	}
	return nil
}

func advertiseTaskPods(
	taskPods map[string]*shared.AgentPod,
	taskDefs map[string]pipelineTaskPodDef,
	dagID string,
	trace func(string, agentlog.EventType, map[string]any),
) {
	for _, taskID := range slices.Sorted(maps.Keys(taskPods)) {
		advertiseTaskPod(taskPods[taskID], taskDefs[taskID], dagID, trace)
	}
}

func advertiseTaskPod(
	pod *shared.AgentPod,
	def pipelineTaskPodDef,
	dagID string,
	trace func(string, agentlog.EventType, map[string]any),
) {
	if pod == nil {
		return
	}
	logTaskPodAdvertise(trace, "task_pod_advertise_begin", def, dagID)
	pod.AdvertiseMembers()
	logTaskPodAdvertise(trace, "task_pod_advertise_done", def, dagID)
}

func logTaskPodAdvertise(
	trace func(string, agentlog.EventType, map[string]any),
	event string,
	def pipelineTaskPodDef,
	dagID string,
) {
	if trace == nil {
		return
	}
	trace(event, agentlog.EventRegistryEvent, mergeTraceMaps(tracePipelineTaskPodDef(def), map[string]any{
		"dag_id": dagID,
	}))
}

func indexPipelineTaskPodDefs(defs []pipelineTaskPodDef) map[string]pipelineTaskPodDef {
	index := make(map[string]pipelineTaskPodDef, len(defs))
	for _, def := range defs {
		index[def.TaskID] = def
	}
	return index
}

func tracePipelineTaskPodDefs(defs []pipelineTaskPodDef) []map[string]any {
	if len(defs) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		result = append(result, tracePipelineTaskPodDef(def))
	}
	return result
}

func tracePipelineTaskPodDef(def pipelineTaskPodDef) map[string]any {
	return map[string]any{
		"task_id":       def.TaskID,
		"task_slug":     def.TaskSlug,
		"files_count":   len(def.Files),
		"files_preview": previewPipelineTraceStrings(def.Files, 8),
	}
}

func previewPipelineTraceStrings(values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	if limit <= 0 || len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}

func mergeTraceMaps(base map[string]any, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
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
	b.cleanupDAG(dagID)
	b.journal.LogDAGCancel(dagID, reason)
	b.store.UpdateDAGState(dagID, "cancelled", reason)

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

// GetDispatcherForDAG returns the BusNodeDispatcher for an active DAG,
// or nil if the DAG is not active.
func (b *DAGBridge) GetDispatcherForDAG(dagID string) *BusNodeDispatcher {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if meta, ok := b.activeDAGs[dagID]; ok {
		return meta.Dispatcher
	}
	return nil
}

// RecordDispatchActivity refreshes the liveness lease for a dispatched node.
func (b *DAGBridge) RecordDispatchActivity(dagID, nodeID string) {
	dagID = strings.TrimSpace(dagID)
	nodeID = strings.TrimSpace(nodeID)
	if dagID == "" || nodeID == "" {
		return
	}

	b.mu.RLock()
	meta, ok := b.activeDAGs[dagID]
	b.mu.RUnlock()
	if !ok || meta == nil || meta.Dispatcher == nil {
		return
	}

	dispatcher := meta.Dispatcher
	dispatcher.RecordActivity(nodeID)
	if parentID, isSubNode := ParentNodeID(nodeID); isSubNode {
		parentID = strings.TrimSpace(parentID)
		if parentID != "" && parentID != nodeID {
			dispatcher.RecordActivity(parentID)
		}
	}
}

// NotifyNodeComplete resolves a pending node dispatch. For sub-nodes from
// pipeline expansion, only the :execute sub-node unblocks the parent
// dispatcher — inspect and test results are recorded but don't resolve
// the original dispatch.
func (b *DAGBridge) NotifyNodeComplete(nodeID string, result *dag.NodeResult) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, meta := range b.activeDAGs {
		// Direct match — not a sub-node or already the dispatched ID.
		meta.Dispatcher.OnNodeComplete(nodeID, result)

		// If this is an :execute sub-node, also resolve the parent node.
		if meta.SubNodeMap != nil {
			if parentID, ok := meta.SubNodeMap[nodeID]; ok {
				stage := StageFromSubNodeID(nodeID)
				if stage == StageExecute {
					meta.Dispatcher.OnNodeComplete(parentID, result)
				}
			}
		}
	}
}

// ResolveSubNode returns the parent node ID for a sub-node, or the
// original nodeID if it is not an expanded sub-node.
func (b *DAGBridge) ResolveSubNode(dagID, nodeID string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	meta, ok := b.activeDAGs[dagID]
	if !ok || meta.SubNodeMap == nil {
		return nodeID
	}
	if parentID, ok := meta.SubNodeMap[nodeID]; ok {
		return parentID
	}
	return nodeID
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
		b.logTrace("dag_recovery_mark_failed", agentlog.EventError, map[string]any{
			"dag_id":      entry.DAGID,
			"prior_state": row.State,
		})
		b.publishActivity(events.EventTypeAgentError,
			fmt.Sprintf("DAG %s recovered from crash (marked failed)", entry.DAGID))
	}

	return nil
}

// Close shuts down the scheduler and unsubscribes remaining per-DAG event handlers.
func (b *DAGBridge) Close() error {
	b.mu.Lock()
	remaining := make([]*ActiveDAGMeta, 0, len(b.activeDAGs))
	for _, meta := range b.activeDAGs {
		remaining = append(remaining, meta)
	}
	b.activeDAGs = make(map[string]*ActiveDAGMeta)
	b.mu.Unlock()

	for _, meta := range remaining {
		releaseTaskPods(meta.TaskPods)
		if meta.Pod != nil {
			meta.Pod.Release()
		}
		if meta.Unsub != nil {
			meta.Unsub()
		}
	}
	return b.scheduler.Close()
}

// dagEventForwarder returns an event handler that writes to WAL + SQLite.
func (b *DAGBridge) dagEventForwarder(dagID, planID string) dag.EventHandler {
	return func(event *dag.Event) {
		if event.DAGID != dagID {
			return
		}

		el := b.eventLogger
		switch event.Type {
		case dag.EventExecutionRunStarted:
			if receipt, err := b.store.MarkPlanHandoffReceiptRunningByDAGID(dagID); err != nil {
				b.logTrace("plan_handoff_receipt_running_failed", agentlog.EventError, map[string]any{
					"dag_id": dagID,
					"error":  err.Error(),
				})
			} else if receipt != nil && b.orchestrator != nil {
				b.orchestrator.publishPlanHandoffReceiptUpdate(receipt, "running")
			}
			b.logTrace("dag_execution_run_started", agentlog.EventRegistryEvent, map[string]any{
				"dag_id": dagID,
			})

		case dag.EventExecutionRunFinished:
			b.logTrace("dag_execution_run_finished", agentlog.EventRegistryEvent, map[string]any{
				"dag_id":          dagID,
				"result_state":    stringFromEventData(event.Data, "state"),
				"nodes_succeeded": intFromEventData(event.Data, "nodes_succeeded"),
				"nodes_failed":    intFromEventData(event.Data, "nodes_failed"),
				"nodes_skipped":   intFromEventData(event.Data, "nodes_skipped"),
				"error":           stringFromEventData(event.Data, "error"),
			})

		case dag.EventLayerStarted:
			shared.LogAgentEvent(el, agentlog.EventLayerStarted,
				b.agentID, b.sessionID, "", "info",
				&agentlog.DAGPayload{DAGID: dagID, State: "layer_started", Layer: event.Layer})

		case dag.EventLayerCompleted:
			shared.LogAgentEvent(el, agentlog.EventLayerCompleted,
				b.agentID, b.sessionID, "", "info",
				&agentlog.DAGPayload{DAGID: dagID, State: "layer_completed", Layer: event.Layer})

		case dag.EventNodeDispatched:
			b.journal.LogNodeDispatch(dagID, event.NodeID, "")
			shared.LogAgentEvent(el, agentlog.EventNodeDispatched,
				b.agentID, b.sessionID, "", "info",
				&agentlog.DAGPayload{DAGID: dagID, NodeID: event.NodeID, State: "dispatched"})

		case dag.EventNodeAcked:
			agentID, _ := event.Data["agent_id"].(string)
			b.journal.LogNodeAcked(dagID, event.NodeID, agentID)

		case dag.EventNodeStarted:
			// Legacy — still emitted for non-ACK nodes.
			b.journal.LogNodeDispatch(dagID, event.NodeID, "")

		case dag.EventStageTransition:
			fromStage, _ := event.Data["from_stage"].(string)
			toStage, _ := event.Data["to_stage"].(string)
			b.journal.LogStageTransition(dagID, event.NodeID, fromStage, toStage)
			shared.LogAgentEvent(el, agentlog.EventStageTransition,
				b.agentID, b.sessionID, "", "info",
				&agentlog.PipelinePayload{PipelineID: dagID, Stage: toStage, State: fromStage})

		case dag.EventNodeCompleted:
			b.journal.LogNodeResult(dagID, event.NodeID, "succeeded", "")
			b.updateProgressFromScheduler(dagID)
			b.publishCompletedTaskPipelineState(dagID, event.NodeID)
			shared.LogAgentEvent(el, agentlog.EventNodeCompleted,
				b.agentID, b.sessionID, "", "info",
				&agentlog.DAGPayload{DAGID: dagID, NodeID: event.NodeID, State: "succeeded"})

		case dag.EventNodeFailed:
			errMsg := ""
			if v, ok := event.Data["error"]; ok {
				errMsg, _ = v.(string)
			}
			b.journal.LogNodeResult(dagID, event.NodeID, "failed", errMsg)
			b.updateProgressFromScheduler(dagID)
			b.publishFailedTaskPipelineState(dagID, event.NodeID)
			// Parallel-global-VFS design: do NOT mark the task pod for
			// reset on node failure. Pod reset is driven solely by the
			// orchestrator's explicit DecisionRetry path (for infra-
			// class retries) or by DAG-terminal cleanup. A node failure
			// that maps to a pipeline rejection (inspector reject,
			// global-review refusal, audit rejection) is resolved by
			// the architect dispatching a remediation DAG whose fix
			// tasks have their own pods; the original pipeline's pod
			// has already released at pipeline-inspector-accept
			// boundary. See docs/PARALLEL_GLOBAL_VFS.md.
			shared.LogAgentEvent(el, agentlog.EventNodeFailed,
				b.agentID, b.sessionID, "", "error",
				&agentlog.DAGPayload{DAGID: dagID, NodeID: event.NodeID, State: "failed", Err: errMsg})

		case dag.EventDAGCompleted:
			b.journal.LogDAGComplete(dagID, "succeeded")
			b.store.UpdateDAGState(dagID, "succeeded", "")
			b.onDAGComplete(dagID, planID)
			shared.LogAgentEvent(el, agentlog.EventDAGCompleted,
				b.agentID, b.sessionID, "", "info",
				&agentlog.DAGPayload{DAGID: dagID, State: "succeeded"})

		case dag.EventDAGFailed:
			errMsg := errorFromEvent(event)
			b.journal.LogDAGComplete(dagID, "failed")
			b.store.UpdateDAGState(dagID, "failed", errMsg)
			b.onDAGFailed(dagID, planID, errMsg)
			shared.LogAgentEvent(el, agentlog.EventDAGAborted,
				b.agentID, b.sessionID, "", "error",
				&agentlog.DAGPayload{DAGID: dagID, State: "failed", Err: errMsg})

		case dag.EventDAGCancelled:
			b.journal.LogDAGCancel(dagID, "cancelled via scheduler")
			b.store.UpdateDAGState(dagID, "cancelled", "")
			b.publishCancelledTaskPipelineStates(dagID)
			b.cleanupDAG(dagID)
			shared.LogAgentEvent(el, agentlog.EventDAGCancelled,
				b.agentID, b.sessionID, "", "info",
				&agentlog.DAGPayload{DAGID: dagID, State: "cancelled"})
		}
	}
}

func stringFromEventData(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}

func intFromEventData(data map[string]any, key string) int {
	if data == nil {
		return 0
	}
	switch value := data[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func (b *DAGBridge) publishCompletedTaskPipelineState(dagID, nodeID string) {
	b.mu.RLock()
	meta := b.activeDAGs[dagID]
	if meta == nil {
		b.mu.RUnlock()
		return
	}
	ref, ok := meta.SubNodeTasks[nodeID]
	bus := b.bus
	b.mu.RUnlock()
	if !ok || strings.TrimSpace(ref.Stage) != string(StageExecute) {
		return
	}
	publishTaskPipelineState(bus, b.agentID, ref.TaskID, ref.TaskLabel, taskstate.StatusCompleted, ref.AgentType)
}

func (b *DAGBridge) publishFailedTaskPipelineState(dagID, nodeID string) {
	b.mu.RLock()
	meta := b.activeDAGs[dagID]
	if meta == nil {
		b.mu.RUnlock()
		return
	}
	ref, ok := meta.SubNodeTasks[nodeID]
	bus := b.bus
	b.mu.RUnlock()
	if !ok || strings.TrimSpace(ref.TaskID) == "" {
		return
	}
	publishTaskPipelineState(bus, b.agentID, ref.TaskID, ref.TaskLabel, taskstate.StatusFailed, ref.AgentType)
}

func (b *DAGBridge) publishCancelledTaskPipelineStates(dagID string) {
	b.mu.RLock()
	meta := b.activeDAGs[dagID]
	b.mu.RUnlock()
	if meta == nil {
		return
	}
	b.mu.RLock()
	bus := b.bus
	b.mu.RUnlock()
	for taskID, taskLabel := range meta.TaskLabels {
		publishTaskPipelineState(bus, b.agentID, taskID, taskLabel, taskstate.StatusCancelled, "")
	}
}

func (b *DAGBridge) markTaskPodResetPending(dagID, nodeID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	meta := b.activeDAGs[dagID]
	if meta == nil {
		return
	}
	ref, ok := meta.SubNodeTasks[nodeID]
	if !ok || strings.TrimSpace(ref.TaskID) == "" {
		return
	}
	if meta.ResetPendingTaskPods == nil {
		meta.ResetPendingTaskPods = make(map[string]struct{})
	}
	meta.ResetPendingTaskPods[ref.TaskID] = struct{}{}
}

func (b *DAGBridge) ResetPendingTaskPods(dagID string) {
	b.mu.RLock()
	meta := b.activeDAGs[dagID]
	if meta == nil {
		b.mu.RUnlock()
		return
	}
	taskIDs := make([]string, 0, len(meta.ResetPendingTaskPods))
	for taskID := range meta.ResetPendingTaskPods {
		taskIDs = append(taskIDs, taskID)
	}
	b.mu.RUnlock()
	slices.Sort(taskIDs)
	for _, taskID := range taskIDs {
		if err := b.resetTaskPod(dagID, taskID); err != nil {
			b.logTrace("task_pod_reset_failed", agentlog.EventError, map[string]any{
				"dag_id":  dagID,
				"task_id": taskID,
				"error":   err.Error(),
			})
		}
	}
}

func (b *DAGBridge) ReleaseCompletedTaskResources(dagID, taskID string) {
	dagID = strings.TrimSpace(dagID)
	taskID = strings.TrimSpace(taskID)
	if dagID == "" || taskID == "" {
		return
	}
	if b.releaseCompletedTaskResourcesNow(dagID, taskID) {
		return
	}
	b.releasePendingCompletedTaskPods(dagID)
}

func (b *DAGBridge) releaseCompletedTaskResourcesNow(dagID, taskID string) bool {
	var (
		taskPod *shared.AgentPod
		buffers *BufferRegistry
	)

	b.mu.Lock()
	meta := b.activeDAGs[dagID]
	if meta == nil {
		b.mu.Unlock()
		return false
	}
	if meta.ReleasePendingTaskPods == nil {
		meta.ReleasePendingTaskPods = make(map[string]struct{})
	}
	taskPod = meta.TaskPods[taskID]
	if taskPod == nil {
		delete(meta.ReleasePendingTaskPods, taskID)
		b.mu.Unlock()
		return false
	}
	if taskPod.HasActiveRequests() {
		meta.ReleasePendingTaskPods[taskID] = struct{}{}
		b.mu.Unlock()
		return false
	}
	delete(meta.TaskPods, taskID)
	delete(meta.TaskLabels, taskID)
	delete(meta.ResetPendingTaskPods, taskID)
	delete(meta.ReleasePendingTaskPods, taskID)
	buffers = b.buffers
	b.mu.Unlock()

	if buffers != nil {
		buffers.ReleaseTask(taskID)
	}
	taskPod.Release()
	b.logTrace("task_pod_released_after_ot_ack", agentlog.EventRegistryEvent, map[string]any{
		"dag_id":  dagID,
		"task_id": taskID,
	})
	return true
}

func (b *DAGBridge) releasePendingCompletedTaskPods(dagID string) {
	b.mu.RLock()
	meta := b.activeDAGs[dagID]
	if meta == nil {
		b.mu.RUnlock()
		return
	}
	taskIDs := make([]string, 0, len(meta.ReleasePendingTaskPods))
	for taskID := range meta.ReleasePendingTaskPods {
		taskIDs = append(taskIDs, taskID)
	}
	b.mu.RUnlock()

	slices.Sort(taskIDs)
	for _, taskID := range taskIDs {
		_ = b.releaseCompletedTaskResourcesNow(dagID, taskID)
	}
}

func (b *DAGBridge) resetTaskPod(dagID, taskID string) error {
	b.mu.RLock()
	meta := b.activeDAGs[dagID]
	bus := b.bus
	b.mu.RUnlock()
	if meta == nil {
		return nil
	}
	factory := meta.TaskPodFactory
	if factory == nil {
		return fmt.Errorf("task pod %s: factory is not configured", taskID)
	}
	newPod, err := factory(taskID)
	if err != nil {
		return err
	}

	b.mu.Lock()
	meta = b.activeDAGs[dagID]
	if meta == nil {
		b.mu.Unlock()
		newPod.Release()
		return nil
	}
	if _, pending := meta.ResetPendingTaskPods[taskID]; !pending {
		b.mu.Unlock()
		newPod.Release()
		return nil
	}
	oldPod := meta.TaskPods[taskID]
	taskLabel := meta.TaskLabels[taskID]
	meta.TaskPods[taskID] = newPod
	delete(meta.ResetPendingTaskPods, taskID)
	b.mu.Unlock()

	if oldPod != nil {
		oldPod.Release()
	}
	publishTaskPipelineState(bus, b.agentID, taskID, taskLabel, taskstate.StatusPending, "")
	return nil
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
	meta := b.activeDAGs[dagID]
	delete(b.activeDAGs, dagID)
	delete(b.gates, dagID)
	b.mu.Unlock()

	if meta == nil {
		return
	}
	// Release pod guards directly (pod.Release is idempotent and logs
	// diagnostic summaries of unreleased nodes).
	releaseTaskPods(meta.TaskPods)
	if meta.Pod != nil {
		meta.Pod.Release()
	}
	if meta.Unsub != nil {
		meta.Unsub()
	}
}

func (b *DAGBridge) startTaskPodMonitor() {
	if b.scope == nil {
		return
	}
	b.taskPodMonitorOnce.Do(func() {
		_ = b.scope.Go("dagbridge-task-pod-monitor", 0, func(ctx context.Context) error {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					b.maintainTaskPods(ctx)
				}
			}
		})
	})
}

func (b *DAGBridge) maintainTaskPods(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.releasePendingCompletedTaskPodsForAll()
	pods := b.snapshotTaskPods()
	if len(pods) == 0 {
		return
	}

	for _, taskPod := range pods {
		b.demoteIdleTaskPod(ctx, taskPod)
	}

	usage := b.currentQuotaUsage()
	if usage.ContainerCount <= 0 {
		return
	}
	target := usage.ContainerSoftCapacity
	if target <= 0 {
		target = usage.ContainerLimit
	}
	if target <= 0 || usage.ContainerCount <= target {
		return
	}

	candidates := b.pressureDemotionCandidates(pods)
	for _, taskPod := range candidates {
		if usage.ContainerCount <= target {
			break
		}
		if err := taskPod.Demote(ctx, containerpod.TierCool); err != nil {
			b.logTrace("task_pod_pressure_demote_failed", agentlog.EventError, map[string]any{
				"pod_id": taskPod.Policy().PodID,
				"error":  err.Error(),
			})
			continue
		}
		usage = b.currentQuotaUsage()
	}
}

func (b *DAGBridge) releasePendingCompletedTaskPodsForAll() {
	b.mu.RLock()
	dagIDs := make([]string, 0, len(b.activeDAGs))
	for dagID := range b.activeDAGs {
		dagIDs = append(dagIDs, dagID)
	}
	b.mu.RUnlock()

	slices.Sort(dagIDs)
	for _, dagID := range dagIDs {
		b.releasePendingCompletedTaskPods(dagID)
	}
}

func (b *DAGBridge) snapshotTaskPods() []*shared.AgentPod {
	b.mu.RLock()
	defer b.mu.RUnlock()

	pods := make([]*shared.AgentPod, 0)
	for _, meta := range b.activeDAGs {
		if meta == nil {
			continue
		}
		for _, taskPod := range meta.TaskPods {
			if taskPod == nil {
				continue
			}
			pods = append(pods, taskPod)
		}
	}
	return pods
}

func (b *DAGBridge) currentQuotaUsage() container.ResourceQuotaUsage {
	b.mu.RLock()
	quota := b.quota
	b.mu.RUnlock()
	if quota == nil {
		return container.ResourceQuotaUsage{}
	}
	return quota.Usage()
}

func (b *DAGBridge) demoteIdleTaskPod(ctx context.Context, taskPod *shared.AgentPod) {
	if taskPod == nil || taskPod.HasActiveRequests() || taskPod.IsReleased() {
		return
	}
	policy := taskPod.Policy()
	if policy == nil {
		return
	}
	current := taskPod.LoadTier()
	target := current
	idle := taskPod.IdleDuration()

	switch {
	case policy.IdleToCold > 0 && idle >= policy.IdleToCold:
		target = containerpod.TierCold
	case policy.IdleToCool > 0 && idle >= policy.IdleToCool:
		target = containerpod.TierCool
	case policy.IdleToWarm > 0 && idle >= policy.IdleToWarm:
		target = containerpod.TierWarm
	}

	if target >= current {
		return
	}
	if err := taskPod.Demote(ctx, target); err != nil {
		b.logTrace("task_pod_idle_demote_failed", agentlog.EventError, map[string]any{
			"pod_id": taskPod.Policy().PodID,
			"error":  err.Error(),
		})
	}
}

func (b *DAGBridge) pressureDemotionCandidates(pods []*shared.AgentPod) []*shared.AgentPod {
	candidates := make([]*shared.AgentPod, 0, len(pods))
	for _, taskPod := range pods {
		if taskPod == nil || taskPod.HasActiveRequests() || taskPod.IsReleased() {
			continue
		}
		switch taskPod.LoadTier() {
		case containerpod.TierHot, containerpod.TierWarm:
			candidates = append(candidates, taskPod)
		}
	}
	slices.SortFunc(candidates, func(left, right *shared.AgentPod) int {
		if left == nil && right == nil {
			return 0
		}
		if left == nil {
			return 1
		}
		if right == nil {
			return -1
		}
		if idleCmp := cmpDuration(right.IdleDuration(), left.IdleDuration()); idleCmp != 0 {
			return idleCmp
		}
		return cmpTier(right.LoadTier(), left.LoadTier())
	})
	return candidates
}

func cmpDuration(left, right time.Duration) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func cmpTier(left, right containerpod.ActivationTier) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
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
	if b.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, b.sessionID, content)
	evt.AgentID = b.agentID
	evt.Data["agent_type"] = "orchestrator"
	evt.Data["agent_name"] = "Orchestrator"
	b.activityPub.PublishActivity(evt)
}

func blockingDecisionActivityContent(dagID string, layerIdx int, failures []dag.FailedNodeSummary) string {
	base := fmt.Sprintf("DAG %s layer %d has blocking failures", dagID, layerIdx)
	summary := summarizeBlockingFailures(failures)
	if summary == "" {
		return base + " — awaiting decision"
	}
	return fmt.Sprintf("%s: %s — awaiting decision", base, summary)
}

func summarizeBlockingFailures(failures []dag.FailedNodeSummary) string {
	if len(failures) == 0 {
		return ""
	}
	const maxSummaries = 2
	parts := make([]string, 0, min(len(failures), maxSummaries+1))
	for _, failure := range failures[:min(len(failures), maxSummaries)] {
		parts = append(parts, blockingFailureSummary(failure))
	}
	if extra := len(failures) - min(len(failures), maxSummaries); extra > 0 {
		parts = append(parts, fmt.Sprintf("%d more failure(s)", extra))
	}
	return strings.Join(parts, "; ")
}

func blockingFailureSummary(failure dag.FailedNodeSummary) string {
	label := firstNonEmpty(
		strings.Join(strings.Fields(strings.TrimSpace(failure.NodeName)), " "),
		strings.Join(strings.Fields(strings.TrimSpace(failure.NodeID)), " "),
		strings.Join(strings.Fields(strings.TrimSpace(failure.AgentType)), " "),
		"unknown node",
	)
	if agentType := strings.Join(strings.Fields(strings.TrimSpace(failure.AgentType)), " "); agentType != "" && !strings.EqualFold(agentType, label) {
		label = fmt.Sprintf("%s [%s]", label, agentType)
	}
	errText := strings.Join(strings.Fields(strings.TrimSpace(failure.Error)), " ")
	if errText == "" {
		errText = "failed without error details"
	}
	return fmt.Sprintf("%s: %s", label, truncateForDisplay(errText, 160))
}

func subNodeMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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
func (b *DAGBridge) DAGSnapshots(limit int) []DAGSnap {
	statuses := b.scheduler.List()
	snaps := make([]DAGSnap, 0, min(len(statuses), limit))
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

		snaps = append(snaps, DAGSnap{
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

// compositeGate returns a LayerGate that delegates to per-DAG decision gates.
func (b *DAGBridge) compositeGate() dag.LayerGate {
	return func(ctx context.Context, dagID string, layerIdx int, nodeResults map[string]*dag.NodeResult) error {
		b.mu.RLock()
		gate, ok := b.gates[dagID]
		b.mu.RUnlock()

		if ok {
			if err := gate.Gate()(ctx, dagID, layerIdx, nodeResults); err != nil {
				return err
			}
		}
		return nil
	}
}

// onNonBlockingFailure publishes per-failure activity notifications.
func (b *DAGBridge) onNonBlockingFailure(dagID string, failures []dag.FailedNodeSummary) {
	for _, f := range failures {
		name := f.NodeName
		if name == "" {
			name = f.NodeID
		}
		b.publishActivity(events.EventTypeAgentAction,
			fmt.Sprintf("%s failed! Non-blocking and continuing.", name))
	}
}

// onBlockingDecisionRequired publishes a bus message requesting a user decision.
func (b *DAGBridge) onBlockingDecisionRequired(req dag.LayerDecisionRequest) {
	b.mu.RLock()
	bus := b.bus
	b.mu.RUnlock()

	if bus == nil {
		return
	}

	msg := &guide.Message{
		ID:            generateMessageID(),
		Type:          guide.MessageTypeLayerDecision,
		SourceAgentID: b.agentID,
		Payload: map[string]any{
			"dag_id":       req.DAGID,
			"layer_idx":    req.LayerIdx,
			"failed_nodes": req.FailedNodes,
			"class":        "blocking",
		},
		Timestamp: time.Now(),
	}
	bus.Publish("dag.decision", msg)

	b.publishActivity(events.EventTypeAgentError, blockingDecisionActivityContent(req.DAGID, req.LayerIdx, req.FailedNodes))
}

// ResolveDecision delivers a user decision to the waiting gate for a DAG.
func (b *DAGBridge) ResolveDecision(dagID string, decision dag.DecisionKind) {
	b.mu.RLock()
	gate, ok := b.gates[dagID]
	b.mu.RUnlock()

	if !ok {
		return
	}
	gate.ResolveDecision(dagID, decision)
}

// AcknowledgeDispatch records that the routing fabric has accepted a node
// dispatch. This provides a durable ACK path even if the agent-side ack_topic
// path is delayed or unavailable.
func (b *DAGBridge) AcknowledgeDispatch(dagID, nodeID, agentID, agentType string) bool {
	b.mu.RLock()
	meta := b.activeDAGs[dagID]
	b.mu.RUnlock()
	if meta == nil || meta.Dispatcher == nil {
		return false
	}
	resolvedAgentID := strings.TrimSpace(agentID)
	if resolvedAgentID == "" {
		resolvedAgentID = strings.TrimSpace(agentType)
	}
	ack := &ACKResult{
		AgentID:   resolvedAgentID,
		AgentType: strings.TrimSpace(agentType),
		AckedAt:   time.Now(),
	}
	return meta.Dispatcher.Acknowledge(nodeID, ack)
}
