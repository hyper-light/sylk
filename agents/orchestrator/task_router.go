package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	Prompt        string         `json:"prompt"`
	Context       map[string]any `json:"context,omitempty"`
	ParentResults map[string]any `json:"parent_results,omitempty"`
	SessionID     string         `json:"session_id"`
}

// TaskRouterConfig provides construction parameters for TaskRouter.
type TaskRouterConfig struct {
	Bus       guide.EventBus
	Scope     *concurrency.GoroutineScope
	AgentID   string
	SessionID string
	Logger    *slog.Logger
}

// TaskRouter routes DAG-dispatched tasks to pipeline agents through
// the Guide's direct consultation protocol. All inter-agent communication
// flows through guide.requests → request.<type>.<id> → response.<type>.<id>,
// enforcing audit, rate limiting, and policy.
type TaskRouter struct {
	bus       guide.EventBus
	scope     *concurrency.GoroutineScope
	agentID   string
	sessionID string
	logger    *slog.Logger

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
		bus:       cfg.Bus,
		scope:     cfg.Scope,
		agentID:   cfg.AgentID,
		sessionID: cfg.SessionID,
		logger:    logger,
		pending:   make(map[string]*pendingRoute),
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
		TargetAgentID:   task.AgentType,
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

// handleRouteResponse extracts the pipeline agent's result from the
// Guide-correlated RouteResponse and publishes a PipelineUpdate.
func (r *TaskRouter) handleRouteResponse(task *PipelineTask, msg *guide.Message) {
	if msg == nil {
		r.publishFailure(task, fmt.Errorf("route cancelled for node %s", task.NodeID))
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
			TargetAgentID: pr.task.AgentType,
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
		AgentID:   r.agentID,
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

// RouteCompound dispatches a compound pipeline task: first to the primary
// agent, then to each co-agent for review. In adversarial mode, co-agents
// can push back for bounded revision rounds.
func (r *TaskRouter) RouteCompound(ctx context.Context, task *CompoundPipelineTask) (*dag.CompoundNodeResult, error) {
	// Step 1: Route to primary agent
	primaryCh := make(chan *guide.Message, 1)
	corrID := "pipe_" + uuid.NewString()[:12]

	r.pendingMu.Lock()
	r.pending[corrID] = &pendingRoute{task: &task.PipelineTask, ch: primaryCh}
	r.pendingMu.Unlock()
	defer r.clearPending(corrID)

	req := &guide.RouteRequest{
		CorrelationID:   corrID,
		Input:           encodeTaskInput(&task.PipelineTask),
		TargetAgentID:   task.AgentType,
		ExplicitTarget:  true,
		SourceAgentID:   r.agentID,
		SourceAgentName: "orchestrator",
		SessionID:       task.SessionID,
		Timestamp:       time.Now(),
		Metadata:        extractDispatchMetadata(task.Context),
	}
	msg := guide.NewRequestMessage(generateMessageID(), req)
	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("route compound primary: %w", err)
	}

	// Wait for primary result
	var primaryMsg *guide.Message
	select {
	case primaryMsg = <-primaryCh:
	case <-ctx.Done():
		return nil, ctx.Err()
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
	corrID := "co_" + uuid.NewString()[:12]
	ch := make(chan *guide.Message, 1)

	r.pendingMu.Lock()
	r.pending[corrID] = &pendingRoute{task: &task.PipelineTask, ch: ch}
	r.pendingMu.Unlock()
	defer r.clearPending(corrID)

	coInput, _ := json.Marshal(map[string]any{
		"review_request":  true,
		"primary_output":  primaryOutput,
		"original_prompt": task.Prompt,
		"round":           round,
	})

	req := &guide.RouteRequest{
		CorrelationID:   corrID,
		Input:           string(coInput),
		TargetAgentID:   coAgent,
		ExplicitTarget:  true,
		SourceAgentID:   r.agentID,
		SourceAgentName: "orchestrator",
		SessionID:       task.SessionID,
		Timestamp:       time.Now(),
	}
	msg := guide.NewRequestMessage(generateMessageID(), req)
	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("route co-agent %s: %w", coAgent, err)
	}

	select {
	case respMsg := <-ch:
		if respMsg == nil {
			return &dag.NodeResult{State: dag.NodeStateFailed, Error: fmt.Errorf("co-agent route cancelled")}, nil
		}
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
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func parseCoError(resp *guide.RouteResponse) error {
	if resp == nil || resp.Success {
		return nil
	}
	return fmt.Errorf("%s", resp.Error)
}
