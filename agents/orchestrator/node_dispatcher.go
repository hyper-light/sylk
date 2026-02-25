package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/google/uuid"
)

// BusNodeDispatcher implements dag.NodeDispatcher by routing node execution
// to pipeline agents via the EventBus.
type BusNodeDispatcher struct {
	bus       guide.EventBus
	agentID   string
	sessionID string
	dagID     string
	buffers   *BufferRegistry
	activator guide.AgentActivator
	pending   sync.Map // nodeID → chan *dag.NodeResult
}

// compile-time assertion
var _ dag.NodeDispatcher = (*BusNodeDispatcher)(nil)

// NewBusNodeDispatcher creates a new dispatcher wired to the event bus.
// The activator is optional — when non-nil, Dispatch calls EnsureActive
// before publishing to guarantee the target agent is hot.
func NewBusNodeDispatcher(bus guide.EventBus, agentID, sessionID, dagID string, buffers *BufferRegistry, activator guide.AgentActivator) *BusNodeDispatcher {
	return &BusNodeDispatcher{
		bus:       bus,
		agentID:   agentID,
		sessionID: sessionID,
		dagID:     dagID,
		buffers:   buffers,
		activator: activator,
	}
}

// Dispatch sends a node execution request to the bus and blocks until a result
// arrives or the context is cancelled.
func (d *BusNodeDispatcher) Dispatch(ctx context.Context, node *dag.Node, parentResults map[string]*dag.NodeResult) (*dag.NodeResult, error) {
	ch := make(chan *dag.NodeResult, 1)
	d.pending.Store(node.ID(), ch)
	defer d.pending.Delete(node.ID())

	// Build parent result summaries for the dispatch payload
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
	}

	// Include compound node fields when present
	if node.IsCompound() {
		payload["co_agents"] = node.CoAgents()
		payload["collaboration_mode"] = node.CollaborationMode().String()
		payload["max_review_rounds"] = node.MaxReviewRounds()
	}

	msg := &guide.Message{
		ID:            uuid.New().String(),
		Type:          guide.MessageTypeTaskDispatch,
		SourceAgentID: d.agentID,
		Payload:       payload,
		Metadata: map[string]any{
			"session_id": d.sessionID,
			"dag_id":     d.dagID,
		},
		Timestamp: time.Now(),
	}

	// Activate target agent if demoted/cold.
	if d.activator != nil {
		if err := d.activator.EnsureActive(ctx, node.AgentType()); err != nil {
			return nil, fmt.Errorf("activate %s for node %s: %w", node.AgentType(), node.ID(), err)
		}
	}

	if err := d.bus.Publish("tasks.dispatch", msg); err != nil {
		return nil, fmt.Errorf("dispatch node %s: %w", node.ID(), err)
	}

	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// OnNodeComplete is called by the orchestrator when a pipeline agent responds.
// It resolves the pending dispatch for the given node.
func (d *BusNodeDispatcher) OnNodeComplete(nodeID string, result *dag.NodeResult) {
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
