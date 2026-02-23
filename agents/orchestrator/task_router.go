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
	task *PipelineTask
	ch   chan *guide.Message
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
	corrID := "pipe_" + uuid.NewString()[:12]
	waitCh := r.registerPending(corrID, task)

	req := &guide.RouteRequest{
		CorrelationID: corrID,
		Input:         encodeTaskInput(task),
		TargetAgentID: task.AgentType,
		ExplicitTarget: true,
		SourceAgentID:  r.agentID,
		SourceAgentName: "orchestrator",
		SessionID:      task.SessionID,
		Timestamp:      time.Now(),
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
	r.pendingMu.Unlock()

	if pr == nil {
		return false
	}

	select {
	case pr.ch <- msg:
	default:
	}
	return true
}

// handleRouteResponse extracts the pipeline agent's result from the
// Guide-correlated RouteResponse and publishes a PipelineUpdate.
func (r *TaskRouter) handleRouteResponse(task *PipelineTask, msg *guide.Message) {
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
