package dag

// =============================================================================
// Failure Classification
// =============================================================================

// FailureClass categorizes the severity of failures within a completed layer.
type FailureClass int

const (
	// FailureClassNone indicates no failures occurred in the layer.
	FailureClassNone FailureClass = iota
	// FailureClassNonBlocking indicates failures occurred but remaining nodes
	// can still proceed (at least one viable path exists).
	FailureClassNonBlocking
	// FailureClassBlocking indicates failures block all remaining nodes.
	FailureClassBlocking
)

// FailedNodeSummary describes a single failed node for decision reporting.
type FailedNodeSummary struct {
	NodeID    string
	NodeName  string
	AgentType string
	Error     string
}

// ClassifyLayerFailures determines whether failures in the completed layer
// block further execution. It examines which remaining nodes (after
// completedLayerIdx) can still reach Pending state given current results.
func ClassifyLayerFailures(d *DAG, nodeResults map[string]*NodeResult, completedLayerIdx int) FailureClass {
	if !hasAnyFailure(nodeResults) {
		return FailureClassNone
	}

	remaining := collectRemainingNodes(d, completedLayerIdx)
	if len(remaining) == 0 {
		return FailureClassNone
	}

	stateMap := buildStateMap(nodeResults)
	propagateBlocked(d, stateMap, remaining)

	if anyViable(stateMap, remaining) {
		return FailureClassNonBlocking
	}
	return FailureClassBlocking
}

// CollectFailedNodes gathers summary information for all failed nodes.
func CollectFailedNodes(d *DAG, nodeResults map[string]*NodeResult) []FailedNodeSummary {
	var summaries []FailedNodeSummary
	for nodeID, result := range nodeResults {
		if result.State != NodeStateFailed {
			continue
		}
		node, ok := d.GetNode(nodeID)
		if !ok {
			continue
		}
		errMsg := ""
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		name, _ := node.Metadata()["name"].(string)
		summaries = append(summaries, FailedNodeSummary{
			NodeID:    nodeID,
			NodeName:  name,
			AgentType: node.AgentType(),
			Error:     errMsg,
		})
	}
	return summaries
}

// hasAnyFailure returns true if any node result is Failed.
func hasAnyFailure(nodeResults map[string]*NodeResult) bool {
	for _, result := range nodeResults {
		if result.State == NodeStateFailed {
			return true
		}
	}
	return false
}

// buildStateMap creates a node ID → NodeState map from results,
// defaulting unrecorded nodes to NodeStatePending.
func buildStateMap(nodeResults map[string]*NodeResult) map[string]NodeState {
	states := make(map[string]NodeState, len(nodeResults))
	for id, result := range nodeResults {
		states[id] = result.State
	}
	return states
}

// collectRemainingNodes gathers node IDs from layers after completedLayerIdx.
func collectRemainingNodes(d *DAG, completedLayerIdx int) []string {
	layers := d.ExecutionOrder()
	var remaining []string
	for i := completedLayerIdx + 1; i < len(layers); i++ {
		remaining = append(remaining, layers[i]...)
	}
	return remaining
}

// propagateBlocked performs a fixpoint loop: for each Pending remaining node,
// if node.IsBlocked(stateMap), mark it Blocked. Repeat until stable.
// Bounded by len(remaining) iterations.
func propagateBlocked(d *DAG, stateMap map[string]NodeState, remaining []string) {
	for range len(remaining) {
		changed := false
		for _, nodeID := range remaining {
			if stateMap[nodeID] != NodeStatePending {
				continue
			}
			node, ok := d.GetNode(nodeID)
			if !ok {
				continue
			}
			if node.IsBlocked(stateMap) {
				stateMap[nodeID] = NodeStateBlocked
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

// anyViable returns true if any remaining node is still Pending (not blocked).
func anyViable(stateMap map[string]NodeState, remaining []string) bool {
	for _, nodeID := range remaining {
		if stateMap[nodeID] == NodeStatePending {
			return true
		}
	}
	return false
}
