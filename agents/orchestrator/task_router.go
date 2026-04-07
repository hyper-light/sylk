package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/google/uuid"
)

// PipelineTask is the task payload forwarded to a pipeline agent container
// via the Guide's direct consultation protocol.
type PipelineTask struct {
	NodeID        string         `json:"node_id"`
	DAGID         string         `json:"dag_id"`
	TaskID        string         `json:"task_id"`
	AgentType     string         `json:"agent_type"`
	TargetAgentID string         `json:"target_agent_id,omitempty"`
	Prompt        string         `json:"prompt"`
	Context       map[string]any `json:"context,omitempty"`
	ParentResults map[string]any `json:"parent_results,omitempty"`
	SessionID     string         `json:"session_id"`
}

// TaskRouterConfig provides construction parameters for TaskRouter.
type TaskRouterConfig struct {
	Bus                       guide.EventBus
	Scope                     *concurrency.GoroutineScope
	AgentID                   string
	SessionID                 string
	Logger                    *slog.Logger
	StreamMirrorTargetAgentID string
	EventLogger               *agentlog.SessionEventLogger
	OnNodeActivity            func(string, string)
	OnVisibleRoutePublish     func(*guide.RouteRequest) error
	OnVisibleRouteTerminal    func(string, map[string]any, *guide.RouteResponse)
}

// TaskRouter routes DAG-dispatched tasks to pipeline agents through
// the Guide's direct consultation protocol. All inter-agent communication
// flows through guide.requests → request.<type>.<id> → response.<type>.<id>,
// enforcing audit, rate limiting, and policy.
type TaskRouter struct {
	bus                    guide.EventBus
	scope                  *concurrency.GoroutineScope
	agentID                string
	sessionID              string
	logger                 *slog.Logger
	streamMirrorTargetID   string
	eventLogger            *agentlog.SessionEventLogger
	onNodeActivity         func(string, string)
	onVisibleRoutePublish  func(*guide.RouteRequest) error
	onVisibleRouteTerminal func(string, map[string]any, *guide.RouteResponse)

	pendingMu sync.Mutex
	pending   map[string]*pendingRoute // correlationID → pending

	visibleMu sync.Mutex
	visible   map[string]*visibleRoute // root correlationID → visible route state
	// descendant correlationID → root visible route correlationID
	visibleDescendants map[string]string

	followupMu     sync.Mutex
	followupActive map[string]string
	followupQueued map[string][]*guide.RouteRequest

	protocolRouteMu     sync.Mutex
	protocolRouteActive map[string]string
	protocolRouteCorr   map[string]string
	protocolRouteTask   map[string]*PipelineTask
}

// pendingRoute tracks a dispatched task awaiting a Guide-routed response.
type pendingRoute struct {
	task   *PipelineTask
	ch     chan *guide.Message
	closed bool // set by CancelAllPending before closing ch
}

type visibleRoute struct {
	serializedFollowup bool
	queueKey           string
	metadata           map[string]any
}

// NewTaskRouter creates a TaskRouter that routes tasks through the Guide.
func NewTaskRouter(cfg TaskRouterConfig) *TaskRouter {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskRouter{
		bus:                    cfg.Bus,
		scope:                  cfg.Scope,
		agentID:                cfg.AgentID,
		sessionID:              cfg.SessionID,
		logger:                 logger,
		streamMirrorTargetID:   defaultStreamMirrorTarget(cfg.StreamMirrorTargetAgentID),
		eventLogger:            cfg.EventLogger,
		onNodeActivity:         cfg.OnNodeActivity,
		onVisibleRoutePublish:  cfg.OnVisibleRoutePublish,
		onVisibleRouteTerminal: cfg.OnVisibleRouteTerminal,
		pending:                make(map[string]*pendingRoute),
		visible:                make(map[string]*visibleRoute),
		visibleDescendants:     make(map[string]string),
		followupActive:         make(map[string]string),
		followupQueued:         make(map[string][]*guide.RouteRequest),
		protocolRouteActive:    make(map[string]string),
		protocolRouteCorr:      make(map[string]string),
		protocolRouteTask:      make(map[string]*PipelineTask),
	}
}

func (r *TaskRouter) SetEventLogger(el *agentlog.SessionEventLogger) {
	r.eventLogger = el
}

// Route dispatches a pipeline task through the Guide's direct consultation
// protocol. The task is published as a RouteRequest to guide.requests with
// TargetAgentID pre-set, so the Guide skips classification but still enforces
// audit logging, rate limiting, activation, and network policy.
//
// A tracked goroutine waits for the Guide-correlated response and publishes
// the result as a PipelineUpdate to pipeline.update.<agentType>.
func (r *TaskRouter) Route(task *PipelineTask) error {
	return r.RouteWithLifecycle(task, nil)
}

// RouteWithLifecycle dispatches a pipeline task and selects on a done channel
// so the waiting goroutine exits when the corresponding Dispatch call returns,
// preventing goroutine leaks under FailurePolicyContinue.
// A nil done channel blocks forever in select — correct for the non-lifecycle case.
func (r *TaskRouter) RouteWithLifecycle(task *PipelineTask, done <-chan struct{}) error {
	if protocolPipelineTaskEligible(task) {
		return r.routeProtocolPipelineTask(task, done)
	}

	corrID := "pipe_" + uuid.NewString()[:12]
	waitCh := r.registerPending(corrID, task)
	r.logTrace("task_router_pending_registered", "debug", agentlog.EventTaskDispatched, corrID, routeTraceData(task))

	req := &guide.RouteRequest{
		CorrelationID:   corrID,
		Input:           encodeTaskInput(task),
		TargetAgentID:   routeTargetAgentID(task),
		ExplicitTarget:  true,
		SourceAgentID:   r.agentID,
		SourceAgentName: "orchestrator",
		SessionID:       task.SessionID,
		Timestamp:       time.Now(),
		Metadata:        extractDispatchMetadata(task.Context),
	}

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = map[string]any{
		"pipeline_task": true,
		"dag_id":        task.DAGID,
		"node_id":       task.NodeID,
		"task_id":       task.TaskID,
	}

	r.logTrace("task_router_route_publish_begin", "debug", agentlog.EventTaskDispatched, corrID, routeTraceData(task))
	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		r.clearPending(corrID)
		r.logTrace("task_router_route_publish_failed", "error", agentlog.EventError, corrID, mergeRouteTraceData(routeTraceData(task), map[string]any{
			"error": err.Error(),
		}))
		r.publishFailure(task, fmt.Errorf("publish route request: %w", err))
		return err
	}
	r.logTrace("task_router_route_publish_ok", "debug", agentlog.EventTaskDispatched, corrID, routeTraceData(task))

	return r.scope.Go("route-"+task.NodeID, 0, func(ctx context.Context) error {
		defer func() {
			r.clearPending(corrID)
			r.logTrace("task_router_pending_cleared", "debug", agentlog.EventTaskDispatched, corrID, routeTraceData(task))
		}()
		r.logTrace("task_router_route_wait_begin", "debug", agentlog.EventTaskDispatched, corrID, routeTraceData(task))

		doneCh := done
		for {
			select {
			case resp := <-waitCh:
				responseType := ""
				sourceAgentID := ""
				if resp != nil {
					responseType = string(resp.Type)
					sourceAgentID = resp.SourceAgentID
				}
				r.logTrace("task_router_route_wait_response", "debug", agentlog.EventTaskDispatched, corrID, mergeRouteTraceData(routeTraceData(task), map[string]any{
					"response_type":   responseType,
					"source_agent_id": sourceAgentID,
				}))
				r.handleRouteResponse(task, resp)
				return nil
			case <-ctx.Done():
				r.logTrace("task_router_route_wait_context_done", "error", agentlog.EventError, corrID, mergeRouteTraceData(routeTraceData(task), map[string]any{
					"error": ctx.Err().Error(),
				}))
				r.publishFailure(task, ctx.Err())
				return nil
			case <-doneCh:
				// Dispatch returned early (usually timeout). Keep the route open so
				// late terminal responses still reconcile the node instead of being dropped.
				r.logTrace("task_router_route_wait_done_closed", "debug", agentlog.EventTaskDispatched, corrID, routeTraceData(task))
				doneCh = nil
			}
		}
	})
}

// PublishUserVisibleRoute publishes a direct Guide route request whose UI
// visibility is owned by Guide-delivered stream/terminal traffic instead of
// router-side chat mirroring.
func (r *TaskRouter) PublishUserVisibleRoute(req *guide.RouteRequest) error {
	if r == nil {
		return fmt.Errorf("task router is not configured")
	}
	if r.bus == nil {
		return fmt.Errorf("task router bus is not configured")
	}
	if req == nil {
		return fmt.Errorf("route request is required")
	}
	if routeRequiresSerializedFollowup(req) {
		return r.publishSerializedFollowupRoute(req)
	}
	return r.publishVisibleRouteNow(req)
}

func (r *TaskRouter) publishVisibleRouteNow(req *guide.RouteRequest) error {
	if r == nil || req == nil {
		return fmt.Errorf("route request is required")
	}
	req.Metadata = guide.MetadataWithVisibleTarget(req.Metadata, r.streamMirrorTargetID)

	corrID := strings.TrimSpace(req.CorrelationID)
	if corrID == "" {
		corrID = "route_" + generateMessageID()
		req.CorrelationID = corrID
	}
	r.trackVisibleRoute(corrID, visibleRouteFromRequest(req))
	if r.onVisibleRoutePublish != nil {
		if err := r.onVisibleRoutePublish(cloneRouteRequest(req)); err != nil {
			r.clearVisibleRoute(corrID)
			return err
		}
	}

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = cloneMetadata(req.Metadata)
	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		r.clearVisibleRoute(corrID)
		return err
	}
	return nil
}

func protocolSerializedRouteKey(req *guide.RouteRequest) string {
	if req == nil || !metadataBool(req.Metadata, "pipeline_task") {
		return ""
	}
	return strings.TrimSpace(req.TargetAgentID)
}

func protocolTaskFromRouteRequest(req *guide.RouteRequest) *PipelineTask {
	if req == nil || !metadataBool(req.Metadata, "pipeline_task") {
		return nil
	}
	taskID := metadataString(req.Metadata, "task_id")
	agentType := metadataString(req.Metadata, "agent_type")
	if taskID == "" || agentType == "" {
		return nil
	}
	ctx := map[string]any{}
	if stage := metadataString(req.Metadata, "pipeline_stage"); stage != "" {
		ctx["pipeline_stage"] = stage
	}
	if taskSlug := metadataString(req.Metadata, "task_slug"); taskSlug != "" {
		ctx["task_slug"] = taskSlug
	}
	if taskName := metadataString(req.Metadata, "task_name"); taskName != "" {
		ctx["task_name"] = taskName
	}
	return &PipelineTask{
		NodeID:        firstNonEmpty(metadataString(req.Metadata, "node_id"), taskID),
		DAGID:         metadataString(req.Metadata, "dag_id"),
		TaskID:        taskID,
		AgentType:     agentType,
		TargetAgentID: strings.TrimSpace(req.TargetAgentID),
		SessionID:     strings.TrimSpace(req.SessionID),
		Context:       ctx,
	}
}

func (r *TaskRouter) publishProtocolRoute(req *guide.RouteRequest) (bool, error) {
	if r == nil || req == nil {
		return false, fmt.Errorf("route request is required")
	}
	if r.bus == nil {
		return false, fmt.Errorf("task router bus is not configured")
	}

	corrID := strings.TrimSpace(req.CorrelationID)
	if corrID == "" {
		corrID = "pipe_" + generateMessageID()
		req.CorrelationID = corrID
	}

	queueKey := protocolSerializedRouteKey(req)
	protocolTask := protocolTaskFromRouteRequest(req)
	if queueKey != "" {
		r.protocolRouteMu.Lock()
		if activeCorrID := strings.TrimSpace(r.protocolRouteActive[queueKey]); activeCorrID != "" && activeCorrID != corrID {
			r.protocolRouteMu.Unlock()
			r.logTrace("task_router_protocol_route_suppressed_active", "info", agentlog.EventPipelineStateChange, corrID, map[string]any{
				"target_agent_id":       strings.TrimSpace(req.TargetAgentID),
				"active_correlation_id": activeCorrID,
				"task_id":               metadataString(req.Metadata, "task_id"),
				"agent_type":            metadataString(req.Metadata, "agent_type"),
			})
			return false, nil
		}
		r.protocolRouteActive[queueKey] = corrID
		r.protocolRouteCorr[corrID] = queueKey
		if protocolTask != nil {
			r.protocolRouteTask[queueKey] = clonePipelineTask(protocolTask)
		}
		r.protocolRouteMu.Unlock()
	}

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = cloneMetadata(req.Metadata)
	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		if queueKey != "" {
			r.protocolRouteMu.Lock()
			if r.protocolRouteActive[queueKey] == corrID {
				delete(r.protocolRouteActive, queueKey)
				delete(r.protocolRouteTask, queueKey)
			}
			delete(r.protocolRouteCorr, corrID)
			r.protocolRouteMu.Unlock()
		}
		return false, err
	}
	return true, nil
}

func protocolSerializedRouteKeyFromMessage(msg *guide.Message) string {
	task := protocolTaskFromResponseMetadata(msg)
	if task == nil {
		return ""
	}
	return pipelineWorkerTargetAgentID(task.TaskID, task.AgentType)
}

func (r *TaskRouter) clearProtocolRoute(msg *guide.Message) {
	if r == nil {
		return
	}
	if msg == nil {
		return
	}
	correlationID := strings.TrimSpace(msg.CorrelationID)
	if correlationID == "" {
		// Fall through: some protocol responses do not preserve the originating
		// route correlation, but still identify the responding pipeline agent in
		// metadata.
	}
	r.protocolRouteMu.Lock()
	defer r.protocolRouteMu.Unlock()
	queueKey := strings.TrimSpace(r.protocolRouteCorr[correlationID])
	if correlationID != "" {
		delete(r.protocolRouteCorr, correlationID)
	}
	if queueKey == "" {
		queueKey = protocolSerializedRouteKeyFromMessage(msg)
	}
	if queueKey == "" {
		return
	}
	if activeCorrID := strings.TrimSpace(r.protocolRouteActive[queueKey]); activeCorrID == correlationID || correlationID == "" {
		delete(r.protocolRouteActive, queueKey)
		delete(r.protocolRouteTask, queueKey)
		return
	}
	// Some protocol terminals arrive with a fresh correlation but still
	// represent completion for the currently active task-scoped pipeline worker.
	if strings.TrimSpace(r.protocolRouteActive[queueKey]) != "" {
		delete(r.protocolRouteActive, queueKey)
		delete(r.protocolRouteTask, queueKey)
	}
}

func (r *TaskRouter) publishSerializedFollowupRoute(req *guide.RouteRequest) error {
	if r == nil || req == nil {
		return fmt.Errorf("route request is required")
	}
	corrID := strings.TrimSpace(req.CorrelationID)
	if corrID == "" {
		corrID = "route_" + generateMessageID()
		req.CorrelationID = corrID
	}
	queueKey := visibleFollowupQueueKey(req)
	if queueKey == "" {
		return r.publishVisibleRouteNow(req)
	}

	r.followupMu.Lock()
	if activeCorrID := strings.TrimSpace(r.followupActive[queueKey]); activeCorrID != "" {
		r.followupQueued[queueKey] = insertQueuedFollowupRoute(r.followupQueued[queueKey], cloneRouteRequest(req))
		r.followupMu.Unlock()
		return nil
	}
	r.followupActive[queueKey] = corrID
	r.followupMu.Unlock()

	if err := r.publishVisibleRouteNow(req); err != nil {
		r.followupMu.Lock()
		if r.followupActive[queueKey] == corrID {
			delete(r.followupActive, queueKey)
		}
		r.followupMu.Unlock()
		return err
	}
	return nil
}

// DeliverResponse is called by the Orchestrator's response handler when a
// message arrives on response.orchestrator.orchestrator. If the correlationID
// matches a pending pipeline route, the message is forwarded to the waiting
// goroutine. Returns true if the message was consumed.
func (r *TaskRouter) DeliverResponse(msg *guide.Message) bool {
	if msg == nil || msg.CorrelationID == "" {
		return false
	}
	if msg.Type == guide.MessageTypeStream {
		if r.mirrorStreamToUser(msg) {
			return true
		}
		if r.consumeVisibleRouteStream(msg) {
			return true
		}
		return r.mirrorProtocolStreamToUser(msg)
	}
	if !isTerminalRouteMessage(msg) {
		return false
	}
	r.clearProtocolRoute(msg)

	r.pendingMu.Lock()
	pr := r.pending[msg.CorrelationID]
	if pr == nil || pr.closed {
		r.pendingMu.Unlock()
		if r.consumeVisibleRouteTerminal(msg) {
			return true
		}
		if r.consumeProtocolTerminal(msg) {
			return true
		}
		if pr == nil {
			r.logger.Warn("response has no pending route", "correlation_id", msg.CorrelationID)
			r.logTrace("task_router_response_unmatched", "warn", agentlog.EventError, msg.CorrelationID, map[string]any{
				"message_type":    string(msg.Type),
				"source_agent_id": msg.SourceAgentID,
				"target_agent_id": msg.TargetAgentID,
			})
		} else {
			r.logTrace("task_router_response_closed", "warn", agentlog.EventError, msg.CorrelationID, mergeRouteTraceData(routeTraceData(pr.task), map[string]any{
				"message_type": string(msg.Type),
			}))
		}
		return pr != nil
	}
	r.pendingMu.Unlock()
	if r.onNodeActivity != nil && pr.task != nil {
		r.onNodeActivity(pr.task.DAGID, pr.task.NodeID)
	}

	select {
	case pr.ch <- msg:
	default:
	}
	r.logTrace("task_router_response_delivered", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, mergeRouteTraceData(routeTraceData(pr.task), map[string]any{
		"message_type":    string(msg.Type),
		"source_agent_id": msg.SourceAgentID,
		"target_agent_id": msg.TargetAgentID,
	}))
	return true
}

func defaultStreamMirrorTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return "tui"
	}
	return target
}

func (r *TaskRouter) trackVisibleRoute(corrID string, route *visibleRoute) {
	if r == nil || strings.TrimSpace(corrID) == "" || route == nil {
		return
	}
	r.visibleMu.Lock()
	r.visible[corrID] = route
	r.visibleMu.Unlock()
}

func (r *TaskRouter) visibleRoute(corrID string) *visibleRoute {
	if r == nil || strings.TrimSpace(corrID) == "" {
		return nil
	}
	r.visibleMu.Lock()
	defer r.visibleMu.Unlock()
	route := r.visible[strings.TrimSpace(corrID)]
	if route == nil {
		return nil
	}
	return cloneVisibleRoute(route)
}

func (r *TaskRouter) clearVisibleRoute(corrID string) {
	if r == nil || strings.TrimSpace(corrID) == "" {
		return
	}
	rootCorrID := strings.TrimSpace(corrID)
	r.visibleMu.Lock()
	delete(r.visible, rootCorrID)
	for childCorrID, mappedRoot := range r.visibleDescendants {
		if strings.TrimSpace(mappedRoot) == rootCorrID {
			delete(r.visibleDescendants, childCorrID)
		}
	}
	r.visibleMu.Unlock()
}

func cloneVisibleRoute(route *visibleRoute) *visibleRoute {
	if route == nil {
		return nil
	}
	return &visibleRoute{
		serializedFollowup: route.serializedFollowup,
		queueKey:           route.queueKey,
		metadata:           cloneMetadata(route.metadata),
	}
}

func (r *TaskRouter) visibleRouteForStream(corrID string, metadata map[string]any) (*visibleRoute, string) {
	if r == nil {
		return nil, ""
	}
	corrID = strings.TrimSpace(corrID)
	if corrID == "" {
		return nil, ""
	}

	r.visibleMu.Lock()
	defer r.visibleMu.Unlock()

	if route := r.visible[corrID]; route != nil {
		return cloneVisibleRoute(route), corrID
	}

	if rootCorrID := strings.TrimSpace(r.visibleDescendants[corrID]); rootCorrID != "" {
		if route := r.visible[rootCorrID]; route != nil {
			return cloneVisibleRoute(route), rootCorrID
		}
		delete(r.visibleDescendants, corrID)
	}

	parentCorrID := strings.TrimSpace(metadataString(metadata, "chat_parent_correlation_id"))
	if parentCorrID == "" {
		return nil, ""
	}

	rootCorrID := parentCorrID
	if route := r.visible[rootCorrID]; route != nil {
		r.visibleDescendants[corrID] = rootCorrID
		return cloneVisibleRoute(route), rootCorrID
	}

	rootCorrID = strings.TrimSpace(r.visibleDescendants[parentCorrID])
	if rootCorrID == "" {
		return nil, ""
	}
	if route := r.visible[rootCorrID]; route != nil {
		r.visibleDescendants[corrID] = rootCorrID
		return cloneVisibleRoute(route), rootCorrID
	}
	delete(r.visibleDescendants, parentCorrID)
	return nil, ""
}

func (r *TaskRouter) visibleRouteForTerminal(corrID string) (*visibleRoute, string) {
	if r == nil {
		return nil, ""
	}
	corrID = strings.TrimSpace(corrID)
	if corrID == "" {
		return nil, ""
	}

	r.visibleMu.Lock()
	defer r.visibleMu.Unlock()

	if route := r.visible[corrID]; route != nil {
		return cloneVisibleRoute(route), corrID
	}
	if rootCorrID := strings.TrimSpace(r.visibleDescendants[corrID]); rootCorrID != "" {
		if route := r.visible[rootCorrID]; route != nil {
			return cloneVisibleRoute(route), rootCorrID
		}
		delete(r.visibleDescendants, corrID)
	}
	return nil, ""
}

func (r *TaskRouter) clearVisibleDescendant(corrID, rootCorrID string) {
	if r == nil {
		return
	}
	corrID = strings.TrimSpace(corrID)
	rootCorrID = strings.TrimSpace(rootCorrID)
	if corrID == "" || rootCorrID == "" || corrID == rootCorrID {
		return
	}
	r.visibleMu.Lock()
	if strings.TrimSpace(r.visibleDescendants[corrID]) == rootCorrID {
		delete(r.visibleDescendants, corrID)
	}
	r.visibleMu.Unlock()
}

func shouldReleaseVisibleRoute(route *visibleRoute, resp *guide.RouteResponse) bool {
	if route == nil || resp == nil {
		return true
	}
	if !route.serializedFollowup {
		return true
	}
	if !resp.Success {
		return true
	}
	if !metadataBool(route.metadata, "global_review") {
		return true
	}
	turnResp, err := agentshared.DecodeGlobalReviewTurnResponse(resp.Data)
	if err != nil || turnResp == nil || turnResp.Action == nil {
		return true
	}
	switch turnResp.Action.Type {
	case agentshared.GlobalReviewActionAccept, agentshared.GlobalReviewActionCommit, agentshared.GlobalReviewActionRefusal:
		return true
	default:
		return false
	}
}

func visibleRouteFromRequest(req *guide.RouteRequest) *visibleRoute {
	if req == nil {
		return nil
	}
	return &visibleRoute{
		serializedFollowup: routeRequiresSerializedFollowup(req),
		queueKey:           visibleFollowupQueueKey(req),
		metadata:           cloneMetadata(req.Metadata),
	}
}

func (r *TaskRouter) mirrorStreamToUser(msg *guide.Message) bool {
	if msg == nil || msg.CorrelationID == "" {
		return false
	}
	stream, ok := msg.GetStreamResponse()
	if !ok || stream == nil || stream.Event == nil {
		return false
	}

	r.pendingMu.Lock()
	pr := r.pending[msg.CorrelationID]
	if pr == nil || pr.closed {
		r.pendingMu.Unlock()
		return false
	}
	r.pendingMu.Unlock()
	if r.onNodeActivity != nil && pr != nil && pr.task != nil {
		r.onNodeActivity(pr.task.DAGID, pr.task.NodeID)
	}

	if r.streamMirrorTargetID == "" || r.bus == nil {
		return true
	}

	mirrored := &guide.StreamResponse{
		CorrelationID:       stream.CorrelationID,
		RespondingAgentID:   firstNonEmpty(stream.RespondingAgentID, msg.SourceAgentID, routeTargetAgentID(pr.task)),
		RespondingAgentName: firstNonEmpty(stream.RespondingAgentName, pr.task.AgentType),
		TargetAgentID:       r.streamMirrorTargetID,
		Metadata:            enrichMirroredStreamMetadata(pr.task, cloneMetadata(stream.Metadata)),
		Event:               cloneStreamEvent(stream.Event),
	}
	mirroredMsg := &guide.Message{
		ID:            generateMessageID(),
		CorrelationID: mirrored.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       mirrored,
		SourceAgentID: mirrored.RespondingAgentID,
		TargetAgentID: r.streamMirrorTargetID,
		Timestamp:     time.Now(),
	}
	if err := r.bus.Publish(guide.TopicResponses(r.streamMirrorTargetID, r.streamMirrorTargetID), mirroredMsg); err != nil {
		r.logger.Warn("mirror pipeline stream to user", "correlation_id", msg.CorrelationID, "error", err)
		r.logTrace("task_router_stream_mirror_failed", "warn", agentlog.EventError, msg.CorrelationID, mergeRouteTraceData(routeTraceData(pr.task), map[string]any{
			"mirror_target":   r.streamMirrorTargetID,
			"source_agent_id": msg.SourceAgentID,
			"error":           err.Error(),
		}))
		return true
	}
	r.logTrace("task_router_stream_mirrored", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, mergeRouteTraceData(routeTraceData(pr.task), map[string]any{
		"mirror_target":   r.streamMirrorTargetID,
		"source_agent_id": msg.SourceAgentID,
	}))
	return true
}

func (r *TaskRouter) consumeVisibleRouteStream(msg *guide.Message) bool {
	if msg == nil || msg.CorrelationID == "" {
		return false
	}
	stream, ok := msg.GetStreamResponse()
	if !ok || stream == nil || stream.Event == nil {
		return false
	}
	_, rootCorrID := r.visibleRouteForStream(msg.CorrelationID, stream.Metadata)
	if rootCorrID == "" {
		return false
	}
	if stream.Event.Type == guide.StreamEventComplete || stream.Event.Type == guide.StreamEventError {
		r.clearVisibleDescendant(msg.CorrelationID, rootCorrID)
	}
	r.logTrace("task_router_visible_stream_observed", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, map[string]any{
		"source_agent_id": msg.SourceAgentID,
		"visible_root":    rootCorrID,
	})
	return true
}

func (r *TaskRouter) mirrorProtocolStreamToUser(msg *guide.Message) bool {
	if msg == nil || msg.CorrelationID == "" {
		return false
	}
	stream, ok := msg.GetStreamResponse()
	if !ok || stream == nil || stream.Event == nil {
		return false
	}
	task := protocolTaskFromStreamMetadata(stream)
	if task == nil {
		return false
	}
	if r.onNodeActivity != nil {
		r.onNodeActivity(task.DAGID, task.NodeID)
	}
	if r.streamMirrorTargetID == "" || r.bus == nil {
		return true
	}

	mirrored := &guide.StreamResponse{
		CorrelationID:       stream.CorrelationID,
		RespondingAgentID:   mirroredStreamResponderID(stream, msg, routeTargetAgentID(task), task.AgentType),
		RespondingAgentName: firstNonEmpty(stream.RespondingAgentName, task.AgentType),
		TargetAgentID:       r.streamMirrorTargetID,
		Metadata:            enrichMirroredStreamMetadata(task, cloneMetadata(stream.Metadata)),
		Event:               cloneStreamEvent(stream.Event),
	}
	mirroredMsg := &guide.Message{
		ID:            generateMessageID(),
		CorrelationID: mirrored.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       mirrored,
		SourceAgentID: mirrored.RespondingAgentID,
		TargetAgentID: r.streamMirrorTargetID,
		Timestamp:     time.Now(),
	}
	if err := r.bus.Publish(guide.TopicResponses(r.streamMirrorTargetID, r.streamMirrorTargetID), mirroredMsg); err != nil {
		r.logger.Warn("mirror protocol pipeline stream to user", "correlation_id", msg.CorrelationID, "error", err)
		r.logTrace("task_router_protocol_stream_mirror_failed", "warn", agentlog.EventError, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
			"mirror_target":   r.streamMirrorTargetID,
			"source_agent_id": msg.SourceAgentID,
			"error":           err.Error(),
		}))
		return true
	}
	r.logTrace("task_router_protocol_stream_mirrored", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
		"mirror_target":   r.streamMirrorTargetID,
		"source_agent_id": msg.SourceAgentID,
	}))
	return true
}

func (r *TaskRouter) consumeVisibleRouteTerminal(msg *guide.Message) bool {
	if msg == nil || msg.CorrelationID == "" {
		return false
	}
	route, rootCorrID := r.visibleRouteForTerminal(msg.CorrelationID)
	if route == nil {
		return false
	}
	resp := visibleRouteResponse(msg)
	if resp == nil {
		return false
	}
	releaseRoute := shouldReleaseVisibleRoute(route, resp)
	defer func() {
		if msg.CorrelationID != rootCorrID {
			r.clearVisibleDescendant(msg.CorrelationID, rootCorrID)
		}
		if releaseRoute {
			r.clearVisibleRoute(rootCorrID)
			r.advanceSerializedFollowupRoute(route, rootCorrID)
		}
	}()
	if r.onVisibleRouteTerminal != nil {
		r.onVisibleRouteTerminal(rootCorrID, cloneMetadata(route.metadata), resp)
	}
	r.logTrace("task_router_visible_terminal_observed", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, map[string]any{
		"source_agent_id": msg.SourceAgentID,
		"success":         resp.Success,
	})
	return true
}

func routeRequiresSerializedFollowup(req *guide.RouteRequest) bool {
	if req == nil {
		return false
	}
	if strings.TrimSpace(metadataString(req.Metadata, "serialized_followup_queue_key")) != "" {
		return true
	}
	if !metadataBool(req.Metadata, "ot_handoff_followup") {
		return false
	}
	switch strings.TrimSpace(req.TargetAgentID) {
	case "inspector", "tester":
		return true
	default:
		return false
	}
}

func visibleFollowupQueueKey(req *guide.RouteRequest) string {
	if req == nil {
		return ""
	}
	if queueKey := strings.TrimSpace(metadataString(req.Metadata, "serialized_followup_queue_key")); queueKey != "" {
		return queueKey
	}
	if !routeRequiresSerializedFollowup(req) {
		return ""
	}
	return strings.TrimSpace(req.TargetAgentID)
}

func (r *TaskRouter) advanceSerializedFollowupRoute(route *visibleRoute, corrID string) {
	if r == nil || route == nil || !route.serializedFollowup {
		return
	}
	queueKey := strings.TrimSpace(route.queueKey)
	if queueKey == "" {
		return
	}

	var next *guide.RouteRequest
	r.followupMu.Lock()
	if activeCorrID := strings.TrimSpace(r.followupActive[queueKey]); activeCorrID == corrID {
		delete(r.followupActive, queueKey)
	}
	if strings.TrimSpace(r.followupActive[queueKey]) == "" {
		pending := r.followupQueued[queueKey]
		if len(pending) > 0 {
			next = pending[0]
			if len(pending) == 1 {
				delete(r.followupQueued, queueKey)
			} else {
				r.followupQueued[queueKey] = append([]*guide.RouteRequest(nil), pending[1:]...)
			}
			r.followupActive[queueKey] = strings.TrimSpace(next.CorrelationID)
		}
	}
	r.followupMu.Unlock()

	if next == nil {
		return
	}
	if err := r.publishVisibleRouteNow(next); err != nil {
		r.followupMu.Lock()
		if r.followupActive[queueKey] == strings.TrimSpace(next.CorrelationID) {
			delete(r.followupActive, queueKey)
		}
		r.followupQueued[queueKey] = append([]*guide.RouteRequest{cloneRouteRequest(next)}, r.followupQueued[queueKey]...)
		r.followupMu.Unlock()
		r.logger.Warn("publish queued OT global follow-up route",
			"correlation_id", next.CorrelationID,
			"target_agent_id", next.TargetAgentID,
			"error", err)
	}
}

func visibleRouteResponse(msg *guide.Message) *guide.RouteResponse {
	if msg == nil {
		return nil
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		cloned := *resp
		return &cloned
	}
	if errText, ok := msg.GetError(); ok {
		return &guide.RouteResponse{
			CorrelationID:     msg.CorrelationID,
			Success:           false,
			Error:             errText,
			RespondingAgentID: strings.TrimSpace(msg.SourceAgentID),
		}
	}
	return nil
}

func mirroredStreamResponderID(stream *guide.StreamResponse, msg *guide.Message, fallbackTargetAgentID, fallbackAgentType string) string {
	if stream != nil {
		if responderID := strings.TrimSpace(stream.RespondingAgentID); responderID != "" {
			return responderID
		}
		if runtimeID := strings.TrimSpace(metadataString(stream.Metadata, "runtime_agent_id")); runtimeID != "" {
			return runtimeID
		}
	}
	return mirroredTerminalResponderID("", msg, fallbackTargetAgentID, fallbackAgentType)
}

func mirroredTerminalResponderID(explicitResponderID string, msg *guide.Message, fallbackTargetAgentID, fallbackAgentType string) string {
	if responderID := strings.TrimSpace(explicitResponderID); responderID != "" {
		return responderID
	}
	if runtimeID := strings.TrimSpace(messageMetadataString(msg, "runtime_agent_id")); runtimeID != "" {
		return runtimeID
	}
	if sourceID := mirroredMessageSourceAgentID(msg, fallbackAgentType); sourceID != "" {
		return sourceID
	}
	if fallbackTargetAgentID = strings.TrimSpace(fallbackTargetAgentID); fallbackTargetAgentID != "" {
		return fallbackTargetAgentID
	}
	return ""
}

func mirroredMessageSourceAgentID(msg *guide.Message, fallbackAgentType string) string {
	if msg == nil {
		return ""
	}
	sourceID := strings.TrimSpace(msg.SourceAgentID)
	if sourceID == "" || strings.EqualFold(sourceID, "guide") {
		return ""
	}
	if fallbackAgentType = strings.TrimSpace(fallbackAgentType); fallbackAgentType != "" && strings.EqualFold(sourceID, fallbackAgentType) {
		return ""
	}
	return sourceID
}

func messageMetadataString(msg *guide.Message, key string) string {
	if msg == nil {
		return ""
	}
	return metadataString(msg.Metadata, key)
}

func protocolTaskFromStreamMetadata(stream *guide.StreamResponse) *PipelineTask {
	if stream == nil || !metadataBool(stream.Metadata, "pipeline_task") {
		return nil
	}
	taskID := metadataString(stream.Metadata, "task_id")
	dagID := metadataString(stream.Metadata, "dag_id")
	nodeID := metadataString(stream.Metadata, "node_id")
	agentType := metadataString(stream.Metadata, "agent_type")
	if taskID == "" || dagID == "" || nodeID == "" || agentType == "" {
		return nil
	}
	ctx := map[string]any{}
	if taskSlug := metadataString(stream.Metadata, "task_slug"); taskSlug != "" {
		ctx["task_slug"] = taskSlug
	}
	if taskName := metadataString(stream.Metadata, "task_name"); taskName != "" {
		ctx["task_name"] = taskName
	}
	return &PipelineTask{
		NodeID:        nodeID,
		DAGID:         dagID,
		TaskID:        taskID,
		AgentType:     agentType,
		TargetAgentID: firstNonEmpty(strings.TrimSpace(stream.RespondingAgentID), pipelineWorkerTargetAgentID(taskID, agentType)),
		Context:       ctx,
	}
}

func protocolTaskFromResponseMetadata(msg *guide.Message) *PipelineTask {
	if msg == nil || !metadataBool(msg.Metadata, "pipeline_task") {
		return nil
	}
	taskID := metadataString(msg.Metadata, "task_id")
	agentType := metadataString(msg.Metadata, "agent_type")
	if taskID == "" || agentType == "" {
		return nil
	}
	ctx := map[string]any{}
	if stage := metadataString(msg.Metadata, "pipeline_stage"); stage != "" {
		ctx["pipeline_stage"] = stage
	} else if stage := protocolStageForAgentType(agentType); stage != "" {
		ctx["pipeline_stage"] = stage
	}
	if taskSlug := metadataString(msg.Metadata, "task_slug"); taskSlug != "" {
		ctx["task_slug"] = taskSlug
	}
	if taskName := metadataString(msg.Metadata, "task_name"); taskName != "" {
		ctx["task_name"] = taskName
	}
	return &PipelineTask{
		NodeID:        firstNonEmpty(metadataString(msg.Metadata, "node_id"), taskID),
		DAGID:         metadataString(msg.Metadata, "dag_id"),
		TaskID:        taskID,
		AgentType:     agentType,
		TargetAgentID: pipelineWorkerTargetAgentID(taskID, agentType),
		Context:       ctx,
	}
}

func protocolStageForAgentType(agentType string) string {
	switch strings.TrimSpace(agentType) {
	case agentshared.PipelineAgentInspector:
		return string(StageInspect)
	case agentshared.PipelineAgentTester:
		return string(StageTest)
	case agentshared.PipelineAgentEngineer, agentshared.PipelineAgentDesigner:
		return string(StageExecute)
	default:
		return ""
	}
}

func protocolTaskStage(task *PipelineTask) string {
	if task == nil {
		return ""
	}
	if stage := taskContextString(task.Context, "pipeline_stage"); stage != "" {
		return stage
	}
	return protocolStageForAgentType(task.AgentType)
}

func protocolTerminalOutput(turnResp *agentshared.PipelineTurnResponse) any {
	if turnResp == nil {
		return nil
	}
	if turnResp.Action != nil && turnResp.Action.Type == agentshared.PipelineProtocolActionOT {
		output := map[string]any{}
		if summary := strings.TrimSpace(turnResp.Action.Summary); summary != "" {
			output["summary"] = summary
		}
		if refs := append([]string(nil), turnResp.Action.EvidenceRefs...); len(refs) > 0 {
			output["evidence_refs"] = refs
		}
		if turnResp.Result != nil {
			output["protocol_result"] = turnResp.Result
		}
		if len(output) > 0 {
			return output
		}
	}
	return turnResp.Result
}

func clonePipelineTask(task *PipelineTask) *PipelineTask {
	if task == nil {
		return nil
	}
	out := *task
	out.Context = clonePipelineTaskContext(task.Context)
	out.ParentResults = clonePipelineParentResults(task.ParentResults)
	return &out
}

func enrichMirroredStreamMetadata(task *PipelineTask, metadata map[string]any) map[string]any {
	if task == nil {
		return metadata
	}
	if metadata == nil {
		metadata = make(map[string]any, 5)
	}
	if _, ok := metadata["agent_type"]; !ok && strings.TrimSpace(task.AgentType) != "" {
		metadata["agent_type"] = strings.TrimSpace(task.AgentType)
	}
	if _, ok := metadata["task_id"]; !ok && strings.TrimSpace(task.TaskID) != "" {
		metadata["task_id"] = strings.TrimSpace(task.TaskID)
	}
	if _, ok := metadata["pipeline_id"]; !ok && strings.TrimSpace(task.TaskID) != "" {
		metadata["pipeline_id"] = strings.TrimSpace(task.TaskID)
	}
	if _, ok := metadata["task_slug"]; !ok {
		if taskSlug := taskContextString(task.Context, "task_slug"); taskSlug != "" {
			metadata["task_slug"] = taskSlug
		}
	}
	if _, ok := metadata["task_name"]; !ok {
		if taskName := taskContextString(task.Context, "task_name"); taskName != "" {
			metadata["task_name"] = taskName
		}
	}
	return metadata
}

func taskContextString(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx[key].(string)
	return strings.TrimSpace(value)
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func metadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func cloneRouteRequest(req *guide.RouteRequest) *guide.RouteRequest {
	if req == nil {
		return nil
	}
	cloned := *req
	cloned.Metadata = cloneMetadata(req.Metadata)
	return &cloned
}

func insertQueuedFollowupRoute(queue []*guide.RouteRequest, req *guide.RouteRequest) []*guide.RouteRequest {
	if req == nil {
		return queue
	}
	for i, existing := range queue {
		if followupRouteBefore(req, existing) {
			queue = append(queue, nil)
			copy(queue[i+1:], queue[i:])
			queue[i] = req
			return queue
		}
	}
	return append(queue, req)
}

func followupRouteBefore(left, right *guide.RouteRequest) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	leftTime := left.Timestamp.UTC()
	rightTime := right.Timestamp.UTC()
	if leftTime.Before(rightTime) {
		return true
	}
	if rightTime.Before(leftTime) {
		return false
	}
	return strings.Compare(strings.TrimSpace(left.CorrelationID), strings.TrimSpace(right.CorrelationID)) < 0
}

func cloneStreamEvent(event *guide.StreamEvent) *guide.StreamEvent {
	if event == nil {
		return nil
	}
	cloned := *event
	return &cloned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// handleRouteResponse extracts the pipeline agent's result from the
// Guide-correlated RouteResponse and publishes a PipelineUpdate.
func (r *TaskRouter) handleRouteResponse(task *PipelineTask, msg *guide.Message) {
	if msg == nil {
		r.logTrace("task_router_terminal_nil", "error", agentlog.EventError, "", routeTraceData(task))
		r.publishFailure(task, fmt.Errorf("route cancelled for node %s", task.NodeID))
		return
	}
	if msg.Type == guide.MessageTypeError {
		errText, ok := msg.GetError()
		if !ok || errText == "" {
			source := firstNonEmpty(strings.TrimSpace(msg.SourceAgentID), strings.TrimSpace(task.AgentType), "unknown agent")
			errText = fmt.Sprintf("route error from %s for node %s", source, task.NodeID)
		}
		r.logTrace("task_router_terminal_error", "error", agentlog.EventError, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
			"error": errText,
		}))
		r.publishFailure(task, fmt.Errorf("%s", errText))
		return
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		r.logTrace("task_router_terminal_invalid", "error", agentlog.EventError, msg.CorrelationID, routeTraceData(task))
		source := firstNonEmpty(strings.TrimSpace(msg.SourceAgentID), strings.TrimSpace(task.AgentType), "unknown agent")
		r.publishFailure(task, fmt.Errorf("invalid route response from %s for node %s", source, task.NodeID))
		return
	}
	if !resp.Success {
		errText := strings.TrimSpace(resp.Error)
		if errText == "" {
			source := firstNonEmpty(
				strings.TrimSpace(resp.RespondingAgentName),
				strings.TrimSpace(resp.RespondingAgentID),
				strings.TrimSpace(task.AgentType),
				"unknown agent",
			)
			errText = fmt.Sprintf("%s returned an unsuccessful route response for node %s without error details", source, task.NodeID)
		}
		r.logTrace("task_router_terminal_failed", "error", agentlog.EventError, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
			"error": errText,
		}))
		r.publishFailure(task, fmt.Errorf("%s", errText))
		return
	}

	r.logTrace("task_router_terminal_success", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, routeTraceData(task))
	r.publishSucceeded(task, resp.Data)
}

func isTerminalRouteMessage(msg *guide.Message) bool {
	if msg == nil {
		return false
	}
	switch msg.Type {
	case guide.MessageTypeResponse, guide.MessageTypeError:
		return true
	default:
		return false
	}
}

func (r *TaskRouter) consumeProtocolTerminal(msg *guide.Message) bool {
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		return false
	}
	task := protocolTaskFromResponseMetadata(msg)
	if !resp.Success {
		if task == nil {
			return false
		}
		errText := strings.TrimSpace(resp.Error)
		if errText == "" {
			errText = "protocol pipeline route returned unsuccessful response"
		}
		publishPipelineUpdateMessage(r.bus, r.agentID, &PipelineUpdate{
			DAGID:     task.DAGID,
			NodeID:    task.NodeID,
			TaskID:    task.TaskID,
			AgentID:   routeTargetAgentID(task),
			AgentType: task.AgentType,
			Status:    "failed",
			Stage:     protocolTaskStage(task),
			Progress:  1,
			Message:   errText,
			Error:     errText,
			Timestamp: time.Now().UTC(),
		})
		r.logTrace("task_router_protocol_terminal_failed", "warn", agentlog.EventError, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
			"source_agent_id": msg.SourceAgentID,
			"error":           errText,
		}))
		return true
	}
	turnResp, err := agentshared.DecodePipelineTurnResponse(resp.Data)
	if err != nil || turnResp == nil {
		if task != nil {
			r.publishProtocolMalformedTurnFailure(task, msg, "protocol pipeline route returned an invalid turn envelope")
			return true
		}
		return false
	}
	if turnResp.Action == nil && len(turnResp.Processed) == 0 {
		if task != nil {
			r.publishProtocolMalformedTurnFailure(task, msg, "protocol pipeline turn ended without recording the next pipeline step")
			return true
		}
		return false
	}
	actionType := ""
	if turnResp.Action != nil {
		actionType = string(turnResp.Action.Type)
	}
	if turnResp.Action != nil && turnResp.Action.Type == agentshared.PipelineProtocolActionOT && task != nil {
		publishPipelineUpdateMessage(r.bus, r.agentID, &PipelineUpdate{
			DAGID:     task.DAGID,
			NodeID:    task.NodeID,
			TaskID:    task.TaskID,
			AgentID:   routeTargetAgentID(task),
			AgentType: task.AgentType,
			Status:    "succeeded",
			Stage:     protocolTaskStage(task),
			Progress:  1,
			Message:   strings.TrimSpace(turnResp.Action.Summary),
			Output:    protocolTerminalOutput(turnResp),
			Timestamp: time.Now().UTC(),
		})
		r.logTrace("task_router_protocol_terminal_reconciled", "info", agentlog.EventPipelineStateChange, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
			"source_agent_id": msg.SourceAgentID,
			"action_type":     actionType,
		}))
	} else if turnResp.Action == nil && len(turnResp.Processed) > 0 && task != nil {
		r.publishProtocolMalformedTurnFailure(task, msg, "protocol pipeline turn processed validation but did not record the next pipeline step")
		return true
	} else if turnResp.Action != nil && task != nil {
		r.logTrace("task_router_protocol_terminal_acknowledged", "debug", agentlog.EventPipelineStateChange, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
			"source_agent_id":   msg.SourceAgentID,
			"action_type":       actionType,
			"creates_challenge": turnResp.Action.CreatesChallenge,
		}))
		if turnResp.Action.Type == agentshared.PipelineProtocolActionHandoff {
			// Shared pipeline protocol dispatch already published the exact next
			// route request and running update. The router only acknowledges the
			// explicit turn-closing protocol action here; it no longer reconstructs
			// the next internal pipeline worker from durable protocol state.
		}
	}
	r.logTrace("task_router_protocol_terminal_consumed", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, map[string]any{
		"source_agent_id": msg.SourceAgentID,
		"target_agent_id": msg.TargetAgentID,
		"action_type":     actionType,
	})
	return true
}

func (r *TaskRouter) publishProtocolMalformedTurnFailure(task *PipelineTask, msg *guide.Message, reason string) {
	if r == nil || task == nil || r.bus == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "protocol pipeline turn ended without a valid closing action"
	}
	publishPipelineUpdateMessage(r.bus, r.agentID, &PipelineUpdate{
		DAGID:     task.DAGID,
		NodeID:    task.NodeID,
		TaskID:    task.TaskID,
		AgentID:   routeTargetAgentID(task),
		AgentType: task.AgentType,
		Status:    "failed",
		Stage:     protocolTaskStage(task),
		Progress:  1,
		Message:   reason,
		Error:     reason,
		Timestamp: time.Now().UTC(),
	})
	r.logTrace("task_router_protocol_terminal_invalid", "warn", agentlog.EventError, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
		"source_agent_id": msg.SourceAgentID,
		"error":           reason,
	}))
}

type activePipelineRouteControl struct {
	correlationID string
	task          *PipelineTask
}

func (r *TaskRouter) snapshotActivePipelineRoutes(sessionID, dagID string) []activePipelineRouteControl {
	if r == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	dagID = strings.TrimSpace(dagID)
	routes := make([]activePipelineRouteControl, 0, len(r.pending)+len(r.protocolRouteActive))
	seen := make(map[string]struct{}, len(r.pending)+len(r.protocolRouteActive))

	r.pendingMu.Lock()
	for corrID, pr := range r.pending {
		if pr == nil || pr.closed || pr.task == nil {
			continue
		}
		if sessionID != "" && strings.TrimSpace(pr.task.SessionID) != sessionID {
			continue
		}
		if dagID != "" && strings.TrimSpace(pr.task.DAGID) != dagID {
			continue
		}
		seen[corrID] = struct{}{}
		routes = append(routes, activePipelineRouteControl{
			correlationID: corrID,
			task:          clonePipelineTask(pr.task),
		})
	}
	r.pendingMu.Unlock()

	r.protocolRouteMu.Lock()
	for queueKey, corrID := range r.protocolRouteActive {
		corrID = strings.TrimSpace(corrID)
		if corrID == "" {
			continue
		}
		if _, ok := seen[corrID]; ok {
			continue
		}
		task := r.protocolRouteTask[queueKey]
		if task == nil {
			continue
		}
		if sessionID != "" && strings.TrimSpace(task.SessionID) != sessionID {
			continue
		}
		if dagID != "" && strings.TrimSpace(task.DAGID) != dagID {
			continue
		}
		seen[corrID] = struct{}{}
		routes = append(routes, activePipelineRouteControl{
			correlationID: corrID,
			task:          clonePipelineTask(task),
		})
	}
	r.protocolRouteMu.Unlock()

	return routes
}

func (r *TaskRouter) publishActiveRoutePace(action, sessionID, dagID, reason string) int {
	if r == nil || r.bus == nil {
		return 0
	}
	action = strings.TrimSpace(strings.ToLower(action))
	if action == "" {
		return 0
	}
	routes := r.snapshotActivePipelineRoutes(sessionID, dagID)
	applied := 0
	for _, route := range routes {
		if route.task == nil {
			continue
		}
		req := &guide.ActionRequest{
			CorrelationID: route.correlationID,
			SourceAgentID: r.agentID,
			TargetAgentID: routeTargetAgentID(route.task),
			Action:        "pace",
			Data: map[string]any{
				"correlation_id": route.correlationID,
				"session_id":     route.task.SessionID,
				"pace":           action,
				"reason":         strings.TrimSpace(reason),
			},
			FireAndForget: true,
			Timestamp:     time.Now().UTC(),
		}
		msg := guide.NewActionMessage(generateMessageID(), req)
		if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
			r.logger.Warn("publish pipeline pace action",
				"correlation_id", route.correlationID,
				"agent_type", route.task.AgentType,
				"pace", action,
				"error", err)
			r.logTrace("task_router_active_route_pace_failed", "warn", agentlog.EventError, route.correlationID, mergeRouteTraceData(routeTraceData(route.task), map[string]any{
				"pace":  action,
				"error": err.Error(),
			}))
			continue
		}
		applied++
		r.logTrace("task_router_active_route_pace_published", "debug", agentlog.EventPipelineStateChange, route.correlationID, mergeRouteTraceData(routeTraceData(route.task), map[string]any{
			"pace":   action,
			"reason": strings.TrimSpace(reason),
		}))
	}
	return applied
}

func (r *TaskRouter) PauseActiveRoutes(sessionID, dagID, reason string) int {
	return r.publishActiveRoutePace("paused", sessionID, dagID, reason)
}

func (r *TaskRouter) ResumeActiveRoutes(sessionID, dagID, reason string) int {
	return r.publishActiveRoutePace("auto", sessionID, dagID, reason)
}

// CancelAllPending cancels all in-flight pipeline routes by publishing a
// cancel action for each pending correlation and closing the response
// channels to unblock waiting goroutines. Called by the DAGBridge when
// the parent request is interrupted.
func (r *TaskRouter) CancelAllPending(reason string) {
	// Mark all pending routes as closed under lock, then close channels
	// and publish cancel actions outside the lock. The closed flag prevents
	// DeliverResponse from sending on a closed channel.
	r.pendingMu.Lock()
	snapshot := make(map[string]*pendingRoute, len(r.pending))
	for corrID, pr := range r.pending {
		pr.closed = true
		snapshot[corrID] = pr
	}
	r.pendingMu.Unlock()

	for corrID, pr := range snapshot {
		r.logger.Info("CancelAllPending: cancelling pipeline route",
			"correlation_id", corrID,
			"agent_type", pr.task.AgentType,
			"node_id", pr.task.NodeID,
			"reason", reason)

		// Publish a cancel action to the Guide so it forwards the
		// interrupt to the pipeline agent.
		action := &guide.ActionRequest{
			CorrelationID: corrID,
			SourceAgentID: r.agentID,
			TargetAgentID: routeTargetAgentID(pr.task),
			Action:        "cancel",
			Data: map[string]any{
				"correlation_id": corrID,
				"session_id":     pr.task.SessionID,
				"reason":         reason,
			},
			FireAndForget: true,
			Timestamp:     time.Now(),
		}
		msg := guide.NewActionMessage(generateMessageID(), action)
		if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
			r.logger.Warn("CancelAllPending: publish cancel failed",
				"correlation_id", corrID, "error", err)
		}

		// Close the response channel to unblock the waiting goroutine.
		// A closed channel returns the zero value, which handleRouteResponse
		// treats as a nil message (route cancelled).
		close(pr.ch)
	}
}

// --- pending registration ---

func (r *TaskRouter) registerPending(corrID string, task *PipelineTask) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	r.pendingMu.Lock()
	r.pending[corrID] = &pendingRoute{task: task, ch: ch}
	r.pendingMu.Unlock()
	return ch
}

func (r *TaskRouter) clearPending(corrID string) {
	r.pendingMu.Lock()
	delete(r.pending, corrID)
	r.pendingMu.Unlock()
}

// --- bus publishing ---

func (r *TaskRouter) publishSucceeded(task *PipelineTask, output any) {
	r.publishPipelineUpdate(task, "succeeded", output, "")
}

func (r *TaskRouter) publishFailure(task *PipelineTask, err error) {
	r.publishPipelineUpdate(task, "failed", nil, err.Error())
}

func (r *TaskRouter) publishPipelineUpdate(task *PipelineTask, status string, output any, errMsg string) {
	update := &PipelineUpdate{
		DAGID:     task.DAGID,
		NodeID:    task.NodeID,
		TaskID:    task.TaskID,
		AgentID:   routeTargetAgentID(task),
		AgentType: task.AgentType,
		Status:    status,
		Progress:  1.0,
		Output:    output,
		Error:     errMsg,
		Timestamp: time.Now(),
	}

	topic := "pipeline.update." + task.AgentType
	msg := &guide.Message{
		ID:            generateMessageID(),
		Type:          guide.MessageTypePipelineUpdate,
		SourceAgentID: r.agentID,
		Payload:       update,
		Timestamp:     time.Now(),
	}

	if err := r.bus.Publish(topic, msg); err != nil {
		r.logger.Error("publish pipeline update",
			"node_id", task.NodeID, "status", status, "error", err)
		r.logTrace("task_router_pipeline_update_publish_failed", "error", agentlog.EventError, "", mergeRouteTraceData(routeTraceData(task), map[string]any{
			"status": status,
			"error":  err.Error(),
		}))
		return
	}
	r.logTrace("task_router_pipeline_update_published", "debug", agentlog.EventPipelineStateChange, "", mergeRouteTraceData(routeTraceData(task), map[string]any{
		"status": status,
		"error":  errMsg,
	}))
}

// extractDispatchMetadata extracts dispatch-protocol keys (e.g. ack_topic)
// from the task context and returns them as RouteRequest.Metadata so the
// Guide merges them into ForwardedRequest.Metadata for the target agent.
func extractDispatchMetadata(ctx map[string]any) map[string]any {
	if ctx == nil {
		return nil
	}
	meta := make(map[string]any, 3)
	if ackTopic, _ := ctx["ack_topic"].(string); ackTopic != "" {
		meta["ack_topic"] = ackTopic
	}
	if dagID, _ := ctx["dag_id"].(string); dagID != "" {
		meta["dag_id"] = dagID
	}
	if nodeID, _ := ctx["node_id"].(string); nodeID != "" {
		meta["node_id"] = nodeID
	}
	if taskID, _ := ctx["task_id"].(string); taskID != "" {
		meta["task_id"] = taskID
	}
	if taskSlug, _ := ctx["task_slug"].(string); taskSlug != "" {
		meta["task_slug"] = taskSlug
	}
	if taskName, _ := ctx["task_name"].(string); taskName != "" {
		meta["task_name"] = taskName
	}
	if agentType, _ := ctx["agent_type"].(string); agentType != "" {
		meta["agent_type"] = agentType
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

// --- encoding ---

// encodeTaskInput serializes the pipeline task as the RouteRequest Input string.
// Pipeline agents decode this from ForwardedRequest.Input.
func encodeTaskInput(task *PipelineTask) string {
	data, err := json.Marshal(task)
	if err != nil {
		return task.Prompt
	}
	return string(data)
}

func (r *TaskRouter) logTrace(event, level string, eventCode agentlog.EventType, corrID string, data map[string]any) {
	if r == nil || r.eventLogger == nil {
		return
	}
	r.eventLogger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     r.agentID,
		SessionID: r.sessionID,
		Event:     event,
		EventCode: eventCode,
		CorrID:    corrID,
		Data:      data,
	})
}

func routeTraceData(task *PipelineTask) map[string]any {
	if task == nil {
		return nil
	}
	data := map[string]any{
		"dag_id":          task.DAGID,
		"node_id":         task.NodeID,
		"task_id":         task.TaskID,
		"agent_type":      task.AgentType,
		"target_agent_id": routeTargetAgentID(task),
	}
	if taskSlug := taskContextString(task.Context, "task_slug"); taskSlug != "" {
		data["task_slug"] = taskSlug
	}
	if ackTopic := taskContextString(task.Context, "ack_topic"); ackTopic != "" {
		data["ack_topic"] = ackTopic
	}
	return data
}

func mergeRouteTraceData(base map[string]any, extra map[string]any) map[string]any {
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

func routeTargetAgentID(task *PipelineTask) string {
	if task == nil {
		return ""
	}
	if target := strings.TrimSpace(task.TargetAgentID); target != "" {
		return target
	}
	return strings.TrimSpace(task.AgentType)
}
