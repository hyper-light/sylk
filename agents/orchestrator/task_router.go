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
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/dag"
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

	pendingMu sync.Mutex
	pending   map[string]*pendingRoute // correlationID → pending
}

// pendingRoute tracks a dispatched task awaiting a Guide-routed response.
type pendingRoute struct {
	task   *PipelineTask
	ch     chan *guide.Message
	closed bool // set by CancelAllPending before closing ch
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
		pending:              make(map[string]*pendingRoute),
	}
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
	corrID := "pipe_" + uuid.NewString()[:12]
	waitCh := r.registerPending(corrID, task)

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

	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		r.clearPending(corrID)
		r.publishFailure(task, fmt.Errorf("publish route request: %w", err))
		return err
	}

	return r.scope.Go("route-"+task.NodeID, 0, func(ctx context.Context) error {
		defer r.clearPending(corrID)

		select {
		case resp := <-waitCh:
			r.handleRouteResponse(task, resp)
		case <-ctx.Done():
			r.publishFailure(task, ctx.Err())
		case <-done:
			// Dispatch returned (success or timeout) — stop waiting.
		}
		return nil
	})
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
		return r.mirrorStreamToUser(msg)
	}
	if !isTerminalRouteMessage(msg) {
		return false
	}

	r.pendingMu.Lock()
	pr := r.pending[msg.CorrelationID]
	if pr == nil || pr.closed {
		r.pendingMu.Unlock()
		if pr == nil {
			r.logger.Warn("response has no pending route", "correlation_id", msg.CorrelationID)
		}
		return pr != nil
	}
	r.pendingMu.Unlock()

	select {
	case pr.ch <- msg:
	default:
	}
	return true
}

func defaultStreamMirrorTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return "tui"
	}
	return target
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

	if r.streamMirrorTargetID == "" || r.bus == nil {
		return true
	}

	mirrored := &guide.StreamResponse{
		CorrelationID:       stream.CorrelationID,
		RespondingAgentID:   firstNonEmpty(stream.RespondingAgentID, msg.SourceAgentID, routeTargetAgentID(pr.task)),
		RespondingAgentName: firstNonEmpty(stream.RespondingAgentName, pr.task.AgentType),
		TargetAgentID:       r.streamMirrorTargetID,
		Metadata:            cloneMetadata(stream.Metadata),
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
	}
	return true
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
		r.publishFailure(task, fmt.Errorf("route cancelled for node %s", task.NodeID))
		return
	}
	if msg.Type == guide.MessageTypeError {
		errText, ok := msg.GetError()
		if !ok || errText == "" {
			errText = fmt.Sprintf("route error for node %s", task.NodeID)
		}
		r.publishFailure(task, fmt.Errorf("%s", errText))
		return
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		r.publishFailure(task, fmt.Errorf("invalid route response for node %s", task.NodeID))
		return
	}
	if !resp.Success {
		r.publishFailure(task, fmt.Errorf("%s", resp.Error))
		return
	}

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
	}
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

// CompoundPipelineTask extends PipelineTask with co-tenancy fields for
// compound node dispatch.
type CompoundPipelineTask struct {
	PipelineTask
	CoAgents          []string              `json:"co_agents,omitempty"`
	CollaborationMode dag.CollaborationMode `json:"collaboration_mode"`
	MaxReviewRounds   int                   `json:"max_review_rounds"`
	PrimaryResult     *dag.NodeResult       `json:"-"`
}

// RouteCompoundWithLifecycle executes a compound task in a tracked goroutine
// and emits a single pipeline update for the parent node.
func (r *TaskRouter) RouteCompoundWithLifecycle(ctx context.Context, task *CompoundPipelineTask, done <-chan struct{}) error {
	if task == nil {
		return fmt.Errorf("compound task cannot be nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.scope.Go("route-compound-"+task.NodeID, 0, func(routeCtx context.Context) error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return nil
		default:
		}

		result, err := r.RouteCompound(routeCtx, task)
		if err != nil {
			r.publishFailure(&task.PipelineTask, err)
			return nil
		}
		r.publishSucceeded(&task.PipelineTask, compoundResultOutput(result))
		return nil
	})
}

// RouteCompound dispatches a compound pipeline task: first to the primary
// agent, then to each co-agent for review. In adversarial mode, co-agents
// can push back for bounded revision rounds.
func (r *TaskRouter) RouteCompound(ctx context.Context, task *CompoundPipelineTask) (*dag.CompoundNodeResult, error) {
	primaryMsg, err := r.routeSingle(ctx, &task.PipelineTask)
	if err != nil {
		return nil, fmt.Errorf("route compound primary: %w", err)
	}

	if primaryMsg == nil {
		return &dag.CompoundNodeResult{
			PrimaryResult: &dag.NodeResult{
				NodeID: task.NodeID,
				State:  dag.NodeStateFailed,
				Error:  fmt.Errorf("primary agent route cancelled"),
			},
		}, fmt.Errorf("compound primary: route cancelled")
	}

	resp, ok := primaryMsg.GetRouteResponse()
	if !ok || resp == nil || !resp.Success {
		errMsg := "primary agent failed"
		if resp != nil {
			errMsg = resp.Error
		}
		return &dag.CompoundNodeResult{
			PrimaryResult: &dag.NodeResult{
				NodeID: task.NodeID,
				State:  dag.NodeStateFailed,
				Error:  fmt.Errorf("%s", errMsg),
			},
		}, fmt.Errorf("compound primary: %s", errMsg)
	}

	result := &dag.CompoundNodeResult{
		PrimaryResult: &dag.NodeResult{
			NodeID: task.NodeID,
			State:  dag.NodeStateSucceeded,
			Output: resp.Data,
		},
		CoResults: make(map[string]*dag.NodeResult, len(task.CoAgents)),
		Consensus: true,
	}

	if len(task.CoAgents) == 0 {
		return result, nil
	}

	// Step 2: Route to co-agents for review
	maxRounds := task.MaxReviewRounds
	if maxRounds <= 0 {
		// Fallback: sequential mode gets exactly 1 review, no pushback.
		maxRounds = int(dag.CollaborationSequential) + 1
	}

	for round := range maxRounds {
		allAccepted := true
		for _, coAgent := range task.CoAgents {
			coResult, err := r.routeCoAgent(ctx, task, coAgent, resp.Data, round)
			if err != nil {
				result.CoResults[coAgent] = &dag.NodeResult{
					NodeID: task.NodeID,
					State:  dag.NodeStateFailed,
					Error:  err,
				}
				result.Consensus = false
				continue
			}
			result.CoResults[coAgent] = coResult
			if coResult.State != dag.NodeStateSucceeded {
				allAccepted = false
				result.Consensus = false
			}
		}

		result.ReviewRoundsUsed = round + 1

		if allAccepted || task.CollaborationMode == dag.CollaborationSequential {
			break
		}
	}

	return result, nil
}

// routeCoAgent sends the primary result to a co-agent for review.
func (r *TaskRouter) routeCoAgent(
	ctx context.Context,
	task *CompoundPipelineTask,
	coAgent string,
	primaryOutput any,
	round int,
) (*dag.NodeResult, error) {
	respMsg, err := r.routeSingle(ctx, r.buildCoAgentTask(task, coAgent, primaryOutput, round))
	if err != nil {
		return nil, fmt.Errorf("route co-agent %s: %w", coAgent, err)
	}

	switch {
	case respMsg == nil:
		return &dag.NodeResult{State: dag.NodeStateFailed, Error: fmt.Errorf("co-agent route cancelled")}, nil
	default:
		coResp, ok := respMsg.GetRouteResponse()
		if !ok || coResp == nil {
			return &dag.NodeResult{State: dag.NodeStateFailed, Error: fmt.Errorf("invalid co-agent response")}, nil
		}
		state := dag.NodeStateSucceeded
		if !coResp.Success {
			state = dag.NodeStateFailed
		}
		return &dag.NodeResult{
			NodeID: task.NodeID,
			State:  state,
			Output: coResp.Data,
			Error:  parseCoError(coResp),
		}, nil
	}
}

func (r *TaskRouter) routeSingle(ctx context.Context, task *PipelineTask) (*guide.Message, error) {
	if task == nil {
		return nil, fmt.Errorf("pipeline task cannot be nil")
	}
	corrID := "pipe_" + uuid.NewString()[:12]
	waitCh := r.registerPending(corrID, task)
	defer r.clearPending(corrID)

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
	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, err
	}

	select {
	case resp := <-waitCh:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *TaskRouter) buildCoAgentTask(task *CompoundPipelineTask, coAgent string, primaryOutput any, round int) *PipelineTask {
	ctx := make(map[string]any, len(task.Context)+4)
	for k, v := range task.Context {
		ctx[k] = v
	}
	ctx["compound_role"] = "co_agent"
	ctx["primary_agent_type"] = task.AgentType
	ctx["review_round"] = round + 1
	ctx["agent_type"] = coAgent

	parentResults := make(map[string]any, len(task.ParentResults)+1)
	for k, v := range task.ParentResults {
		parentResults[k] = v
	}
	parentResults[task.NodeID] = map[string]any{
		"state":  dag.NodeStateSucceeded.String(),
		"output": primaryOutput,
	}

	return &PipelineTask{
		NodeID:        task.NodeID,
		DAGID:         task.DAGID,
		TaskID:        task.TaskID,
		AgentType:     coAgent,
		TargetAgentID: pipelineWorkerTargetAgentID(task.TaskID, coAgent),
		Prompt:        scopedCompoundPrompt(task.Context, coAgent, task.Prompt),
		Context:       ctx,
		ParentResults: parentResults,
		SessionID:     task.SessionID,
	}
}

func scopedCompoundPrompt(ctx map[string]any, agentType, fallback string) string {
	if ctx != nil {
		if scoped, ok := ctx["agent_prompts"].(map[string]string); ok {
			if prompt := strings.TrimSpace(scoped[agentType]); prompt != "" {
				return prompt
			}
		}
		if scoped, ok := ctx["agent_prompts"].(map[string]any); ok {
			if prompt, _ := scoped[agentType].(string); strings.TrimSpace(prompt) != "" {
				return strings.TrimSpace(prompt)
			}
		}
	}
	return fallback
}

func compoundResultOutput(result *dag.CompoundNodeResult) map[string]any {
	if result == nil {
		return nil
	}
	output := map[string]any{
		"consensus":          result.Consensus,
		"review_rounds_used": result.ReviewRoundsUsed,
	}
	if result.PrimaryResult != nil {
		output["primary_output"] = result.PrimaryResult.Output
	}
	if len(result.CoResults) > 0 {
		coResults := make(map[string]any, len(result.CoResults))
		for agentType, res := range result.CoResults {
			if res == nil {
				continue
			}
			entry := map[string]any{"state": res.State.String()}
			if res.Output != nil {
				entry["output"] = res.Output
			}
			if res.Error != nil {
				entry["error"] = res.Error.Error()
			}
			coResults[agentType] = entry
		}
		output["co_results"] = coResults
	}
	return output
}

func parseCoError(resp *guide.RouteResponse) error {
	if resp == nil || resp.Success {
		return nil
	}
	return fmt.Errorf("%s", resp.Error)
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
