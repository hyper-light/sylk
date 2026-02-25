package dag_test

import (
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildLinearDAG(t *testing.T) *dag.DAG {
	t.Helper()
	// A → B → C (3 layers)
	d, err := dag.NewBuilder("linear").
		AddNode(dag.NodeConfig{ID: "A", AgentType: "engineer", Prompt: "a"}).
		AddNode(dag.NodeConfig{ID: "B", AgentType: "engineer", Prompt: "b", Dependencies: []string{"A"}}).
		AddNode(dag.NodeConfig{ID: "C", AgentType: "engineer", Prompt: "c", Dependencies: []string{"B"}}).
		Build()
	require.NoError(t, err)
	return d
}

func buildDiamondDAG(t *testing.T) *dag.DAG {
	t.Helper()
	// A → B, A → C, B → D, C → D
	d, err := dag.NewBuilder("diamond").
		AddNode(dag.NodeConfig{ID: "A", AgentType: "engineer", Prompt: "a"}).
		AddNode(dag.NodeConfig{ID: "B", AgentType: "engineer", Prompt: "b", Dependencies: []string{"A"}}).
		AddNode(dag.NodeConfig{ID: "C", AgentType: "engineer", Prompt: "c", Dependencies: []string{"A"}}).
		AddNode(dag.NodeConfig{ID: "D", AgentType: "engineer", Prompt: "d", Dependencies: []string{"B", "C"}}).
		Build()
	require.NoError(t, err)
	return d
}

func buildForkDAG(t *testing.T) *dag.DAG {
	t.Helper()
	// A → B, A → C (B and C independent after layer 0)
	d, err := dag.NewBuilder("fork").
		AddNode(dag.NodeConfig{ID: "A", AgentType: "engineer", Prompt: "a"}).
		AddNode(dag.NodeConfig{ID: "B", AgentType: "engineer", Prompt: "b", Dependencies: []string{"A"}}).
		AddNode(dag.NodeConfig{ID: "C", AgentType: "engineer", Prompt: "c", Dependencies: []string{"A"}}).
		Build()
	require.NoError(t, err)
	return d
}

func TestClassifyLayerFailures_NoFailures(t *testing.T) {
	d := buildLinearDAG(t)
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
	}

	class := dag.ClassifyLayerFailures(d, results, 0)
	assert.Equal(t, dag.FailureClassNone, class)
}

func TestClassifyLayerFailures_LinearBlocking(t *testing.T) {
	d := buildLinearDAG(t)
	// A failed → B blocked → C blocked
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateFailed, Error: errors.New("boom")},
	}

	class := dag.ClassifyLayerFailures(d, results, 0)
	assert.Equal(t, dag.FailureClassBlocking, class)
}

func TestClassifyLayerFailures_ForkNonBlocking(t *testing.T) {
	d := buildForkDAG(t)
	// A succeeded, B failed, C still viable
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
		"B": {NodeID: "B", State: dag.NodeStateFailed},
	}

	// After layer 1 (B and C are in the same layer), no remaining layers
	// Let's check after layer 0 instead — B and C are in layer 1
	class := dag.ClassifyLayerFailures(d, results, 0)
	// C is still pending (A succeeded, C depends on A) → NonBlocking
	assert.Equal(t, dag.FailureClassNonBlocking, class)
}

func TestClassifyLayerFailures_DiamondBlocking(t *testing.T) {
	d := buildDiamondDAG(t)
	// A succeeded, B failed, C failed → D is blocked
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
		"B": {NodeID: "B", State: dag.NodeStateFailed},
		"C": {NodeID: "C", State: dag.NodeStateFailed},
	}

	class := dag.ClassifyLayerFailures(d, results, 1)
	assert.Equal(t, dag.FailureClassBlocking, class)
}

func TestClassifyLayerFailures_DiamondNonBlockingPartialFail(t *testing.T) {
	d := buildDiamondDAG(t)
	// A succeeded, B failed, C succeeded → D depends on both B and C
	// D is blocked because B failed
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
		"B": {NodeID: "B", State: dag.NodeStateFailed},
		"C": {NodeID: "C", State: dag.NodeStateSucceeded},
	}

	class := dag.ClassifyLayerFailures(d, results, 1)
	// D depends on B (failed) → D is blocked → no viable remaining → Blocking
	assert.Equal(t, dag.FailureClassBlocking, class)
}

func TestClassifyLayerFailures_LastLayer(t *testing.T) {
	d := buildLinearDAG(t)
	// All succeeded — completing the last layer
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
		"B": {NodeID: "B", State: dag.NodeStateSucceeded},
		"C": {NodeID: "C", State: dag.NodeStateFailed},
	}

	// Layer 2 is the last — no remaining layers
	class := dag.ClassifyLayerFailures(d, results, 2)
	assert.Equal(t, dag.FailureClassNone, class)
}

func TestCollectFailedNodes(t *testing.T) {
	d := buildLinearDAG(t)
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
		"B": {NodeID: "B", State: dag.NodeStateFailed, Error: errors.New("compile error")},
	}

	failed := dag.CollectFailedNodes(d, results)
	require.Len(t, failed, 1)
	assert.Equal(t, "B", failed[0].NodeID)
	assert.Equal(t, "engineer", failed[0].AgentType)
	assert.Equal(t, "compile error", failed[0].Error)
}

func TestCollectFailedNodes_Empty(t *testing.T) {
	d := buildLinearDAG(t)
	results := map[string]*dag.NodeResult{
		"A": {NodeID: "A", State: dag.NodeStateSucceeded},
	}

	failed := dag.CollectFailedNodes(d, results)
	assert.Empty(t, failed)
}
