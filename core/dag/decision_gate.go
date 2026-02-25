package dag

import (
	"context"
	"sync"
)

// =============================================================================
// Decision Types
// =============================================================================

// DecisionKind represents the user's response to a blocking failure.
type DecisionKind int

const (
	// DecisionRetry replays the failed layer.
	DecisionRetry DecisionKind = iota
	// DecisionSkip skips the blocking failure and continues.
	DecisionSkip
	// DecisionAbort cancels the entire DAG.
	DecisionAbort
)

// LayerDecision carries a resolved decision for a specific DAG layer.
type LayerDecision struct {
	DAGID    string
	LayerIdx int
	Decision DecisionKind
}

// LayerDecisionRequest is published when a blocking failure requires
// user intervention.
type LayerDecisionRequest struct {
	DAGID       string
	LayerIdx    int
	FailedNodes []FailedNodeSummary
	Class       FailureClass
}

// =============================================================================
// Decision Gate
// =============================================================================

// NonBlockingCallback is invoked when failures are non-blocking.
type NonBlockingCallback func(dagID string, failures []FailedNodeSummary)

// DecisionRequestCallback is invoked when a blocking failure requires a decision.
type DecisionRequestCallback func(req LayerDecisionRequest)

// DecisionGate manages layer-boundary failure classification and decision
// collection for a single DAG execution.
type DecisionGate struct {
	mu             sync.Mutex
	dag            *DAG
	onNonBlocking  NonBlockingCallback
	onDecisionReq  DecisionRequestCallback
	pending        map[string]chan LayerDecision // dagID → decision channel
}

// NewDecisionGate creates a gate for the given DAG with callbacks for
// non-blocking notifications and blocking decision requests.
func NewDecisionGate(d *DAG, onNonBlocking NonBlockingCallback, onDecisionReq DecisionRequestCallback) *DecisionGate {
	return &DecisionGate{
		dag:           d,
		onNonBlocking: onNonBlocking,
		onDecisionReq: onDecisionReq,
		pending:       make(map[string]chan LayerDecision),
	}
}

// Gate returns a LayerGate closure suitable for the executor.
func (g *DecisionGate) Gate() LayerGate {
	return func(ctx context.Context, dagID string, layerIdx int, nodeResults map[string]*NodeResult) error {
		class := ClassifyLayerFailures(g.dag, nodeResults, layerIdx)
		return g.handleClassification(ctx, dagID, layerIdx, nodeResults, class)
	}
}

// ResolveDecision delivers a user decision to the waiting gate.
func (g *DecisionGate) ResolveDecision(dagID string, decision DecisionKind) {
	g.mu.Lock()
	ch, ok := g.pending[dagID]
	g.mu.Unlock()

	if !ok {
		return
	}

	select {
	case ch <- LayerDecision{DAGID: dagID, Decision: decision}:
	default:
	}
}

func (g *DecisionGate) handleClassification(ctx context.Context, dagID string, layerIdx int, nodeResults map[string]*NodeResult, class FailureClass) error {
	switch class {
	case FailureClassNone:
		return nil
	case FailureClassNonBlocking:
		return g.handleNonBlocking(dagID, nodeResults)
	case FailureClassBlocking:
		return g.handleBlocking(ctx, dagID, layerIdx, nodeResults)
	}
	return nil
}

func (g *DecisionGate) handleNonBlocking(dagID string, nodeResults map[string]*NodeResult) error {
	if g.onNonBlocking != nil {
		failures := CollectFailedNodes(g.dag, nodeResults)
		g.onNonBlocking(dagID, failures)
	}
	return nil
}

func (g *DecisionGate) handleBlocking(ctx context.Context, dagID string, layerIdx int, nodeResults map[string]*NodeResult) error {
	failures := CollectFailedNodes(g.dag, nodeResults)

	if g.onDecisionReq != nil {
		g.onDecisionReq(LayerDecisionRequest{
			DAGID:       dagID,
			LayerIdx:    layerIdx,
			FailedNodes: failures,
			Class:       FailureClassBlocking,
		})
	}

	ch := make(chan LayerDecision, 1)
	g.mu.Lock()
	g.pending[dagID] = ch
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.pending, dagID)
		g.mu.Unlock()
	}()

	select {
	case decision := <-ch:
		return g.applyDecision(decision.Decision)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *DecisionGate) applyDecision(decision DecisionKind) error {
	switch decision {
	case DecisionRetry:
		return ErrLayerRetry
	case DecisionSkip:
		return nil
	case DecisionAbort:
		return ErrDAGCancelled
	}
	return nil
}
