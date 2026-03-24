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
}

// TaskRouter routes DAG-dispatched tasks to pipeline agents through
// the Guide's direct consultation protocol. All inter-agent communication
// flows through guide.requests → request.<type>.<id> → response.<type>.<id>,
// enforcing audit, rate limiting, and policy.
type TaskRouter struct {
	bus                  guide.EventBus
	scope                *concurrency.GoroutineScope
	agentID              string
	sessionID            string
	logger               *slog.Logger
	streamMirrorTargetID string
	eventLogger          *agentlog.SessionEventLogger
	onNodeActivity       func(string, string)

	pendingMu sync.Mutex
	pending   map[string]*pendingRoute // correlationID → pending

	visibleMu sync.Mutex
	visible   map[string]*visibleRoute // correlationID → mirrored user-visible route
}

// pendingRoute tracks a dispatched task awaiting a Guide-routed response.
type pendingRoute struct {
	task   *PipelineTask
	ch     chan *guide.Message
	closed bool // set by CancelAllPending before closing ch
}

type visibleRoute struct {
	agentType string
	metadata  map[string]any
}

// NewTaskRouter creates a TaskRouter that routes tasks through the Guide.
func NewTaskRouter(cfg TaskRouterConfig) *TaskRouter {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskRouter{
		bus:                  cfg.Bus,
		scope:                cfg.Scope,
		agentID:              cfg.AgentID,
		sessionID:            cfg.SessionID,
		logger:               logger,
		streamMirrorTargetID: defaultStreamMirrorTarget(cfg.StreamMirrorTargetAgentID),
		eventLogger:          cfg.EventLogger,
		onNodeActivity:       cfg.OnNodeActivity,
		pending:              make(map[string]*pendingRoute),
		visible:              make(map[string]*visibleRoute),
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

// PublishUserVisibleRoute publishes a direct Guide route request and mirrors the
// resulting stream/terminal responses onto the TUI response topic so the chat
// panel sees the full conversation.
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

	corrID := strings.TrimSpace(req.CorrelationID)
	if corrID == "" {
		corrID = "route_" + generateMessageID()
		req.CorrelationID = corrID
	}
	r.trackVisibleRoute(corrID, visibleRouteFromRequest(req))

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = cloneMetadata(req.Metadata)
	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		r.clearVisibleRoute(corrID)
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
		if r.mirrorVisibleRouteStreamToUser(msg) {
			return true
		}
		return r.mirrorProtocolStreamToUser(msg)
	}
	if !isTerminalRouteMessage(msg) {
		return false
	}

	r.pendingMu.Lock()
	pr := r.pending[msg.CorrelationID]
	if pr == nil || pr.closed {
		r.pendingMu.Unlock()
		if r.mirrorVisibleRouteTerminalToUser(msg) {
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
	route := r.visible[corrID]
	if route == nil {
		return nil
	}
	cloned := &visibleRoute{
		agentType: route.agentType,
		metadata:  cloneMetadata(route.metadata),
	}
	return cloned
}

func (r *TaskRouter) clearVisibleRoute(corrID string) {
	if r == nil || strings.TrimSpace(corrID) == "" {
		return
	}
	r.visibleMu.Lock()
	delete(r.visible, corrID)
	r.visibleMu.Unlock()
}

func visibleRouteFromRequest(req *guide.RouteRequest) *visibleRoute {
	if req == nil {
		return nil
	}
	metadata := cloneMetadata(req.Metadata)
	if metadata == nil {
		metadata = make(map[string]any, 4)
	}
	agentType := metadataString(metadata, "agent_type")
	if agentType == "" {
		agentType = strings.TrimSpace(req.TargetAgentID)
		if agentType != "" {
			metadata["agent_type"] = agentType
		}
	}
	return &visibleRoute{
		agentType: agentType,
		metadata:  metadata,
	}
}

func enrichVisibleRouteMetadata(route *visibleRoute, metadata map[string]any) map[string]any {
	if route == nil {
		return metadata
	}
	if metadata == nil {
		metadata = make(map[string]any, len(route.metadata)+1)
	}
	for key, value := range route.metadata {
		if _, exists := metadata[key]; !exists {
			metadata[key] = value
		}
	}
	if _, ok := metadata["agent_type"]; !ok && strings.TrimSpace(route.agentType) != "" {
		metadata["agent_type"] = strings.TrimSpace(route.agentType)
	}
	return metadata
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

func (r *TaskRouter) mirrorVisibleRouteStreamToUser(msg *guide.Message) bool {
	if msg == nil || msg.CorrelationID == "" {
		return false
	}
	stream, ok := msg.GetStreamResponse()
	if !ok || stream == nil || stream.Event == nil {
		return false
	}
	route := r.visibleRoute(msg.CorrelationID)
	if route == nil {
		return false
	}
	if r.streamMirrorTargetID == "" || r.bus == nil {
		return true
	}

	mirrored := &guide.StreamResponse{
		CorrelationID:       stream.CorrelationID,
		RespondingAgentID:   firstNonEmpty(stream.RespondingAgentID, msg.SourceAgentID, strings.TrimSpace(route.agentType)),
		RespondingAgentName: firstNonEmpty(stream.RespondingAgentName, agentshared.AgentDisplayName(route.agentType)),
		TargetAgentID:       r.streamMirrorTargetID,
		Metadata:            enrichVisibleRouteMetadata(route, cloneMetadata(stream.Metadata)),
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
		r.logger.Warn("mirror visible route stream to user", "correlation_id", msg.CorrelationID, "error", err)
		r.logTrace("task_router_visible_stream_mirror_failed", "warn", agentlog.EventError, msg.CorrelationID, map[string]any{
			"mirror_target":   r.streamMirrorTargetID,
			"source_agent_id": msg.SourceAgentID,
			"agent_type":      route.agentType,
			"error":           err.Error(),
		})
		return true
	}
	r.logTrace("task_router_visible_stream_mirrored", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, map[string]any{
		"mirror_target":   r.streamMirrorTargetID,
		"source_agent_id": msg.SourceAgentID,
		"agent_type":      route.agentType,
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
		RespondingAgentID:   firstNonEmpty(stream.RespondingAgentID, routeTargetAgentID(task)),
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

func (r *TaskRouter) mirrorVisibleRouteTerminalToUser(msg *guide.Message) bool {
	if msg == nil || msg.CorrelationID == "" {
		return false
	}
	route := r.visibleRoute(msg.CorrelationID)
	if route == nil {
		return false
	}
	defer r.clearVisibleRoute(msg.CorrelationID)
	if r.streamMirrorTargetID == "" || r.bus == nil {
		return true
	}

	resp := mirroredVisibleRouteResponse(msg, route)
	if resp == nil {
		return false
	}
	mirrored := guide.NewResponseMessage(generateMessageID(), resp)
	mirrored.TargetAgentID = r.streamMirrorTargetID
	if err := r.bus.Publish(guide.TopicResponses(r.streamMirrorTargetID, r.streamMirrorTargetID), mirrored); err != nil {
		r.logger.Warn("mirror visible route terminal to user", "correlation_id", msg.CorrelationID, "error", err)
		r.logTrace("task_router_visible_terminal_mirror_failed", "warn", agentlog.EventError, msg.CorrelationID, map[string]any{
			"mirror_target":   r.streamMirrorTargetID,
			"source_agent_id": msg.SourceAgentID,
			"agent_type":      route.agentType,
			"error":           err.Error(),
		})
		return true
	}
	r.logTrace("task_router_visible_terminal_mirrored", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, map[string]any{
		"mirror_target":   r.streamMirrorTargetID,
		"source_agent_id": msg.SourceAgentID,
		"agent_type":      route.agentType,
		"success":         resp.Success,
	})
	return true
}

func mirroredVisibleRouteResponse(msg *guide.Message, route *visibleRoute) *guide.RouteResponse {
	if msg == nil {
		return nil
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		return &guide.RouteResponse{
			CorrelationID:       resp.CorrelationID,
			Success:             resp.Success,
			Data:                resp.Data,
			Error:               resp.Error,
			RespondingAgentID:   firstNonEmpty(resp.RespondingAgentID, msg.SourceAgentID, strings.TrimSpace(route.agentType)),
			RespondingAgentName: firstNonEmpty(resp.RespondingAgentName, agentshared.AgentDisplayName(route.agentType)),
			ProcessingTime:      resp.ProcessingTime,
		}
	}
	if errText, ok := msg.GetError(); ok {
		return &guide.RouteResponse{
			CorrelationID:       msg.CorrelationID,
			Success:             false,
			Error:               errText,
			RespondingAgentID:   firstNonEmpty(msg.SourceAgentID, strings.TrimSpace(route.agentType)),
			RespondingAgentName: agentshared.AgentDisplayName(route.agentType),
		}
	}
	return nil
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
		r.logTrace("task_router_protocol_terminal_failed_recovered", "warn", agentlog.EventError, msg.CorrelationID, mergeRouteTraceData(routeTraceData(task), map[string]any{
			"source_agent_id": msg.SourceAgentID,
			"error":           errText,
		}))
		return true
	}
	turnResp, err := agentshared.DecodePipelineTurnResponse(resp.Data)
	if err != nil || turnResp == nil {
		return false
	}
	if turnResp.Action == nil && len(turnResp.Processed) == 0 {
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
	}
	r.logTrace("task_router_protocol_terminal_consumed", "debug", agentlog.EventTaskDispatched, msg.CorrelationID, map[string]any{
		"source_agent_id": msg.SourceAgentID,
		"target_agent_id": msg.TargetAgentID,
		"action_type":     actionType,
	})
	return true
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
