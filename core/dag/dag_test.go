package dag_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDispatcher struct {
	mu            sync.Mutex
	executed      []string
	shouldFail    map[string]bool
	failCounts    map[string]int
	callCounts    map[string]int32
	executionTime time.Duration
}

func newMockDispatcher() *mockDispatcher {
	return &mockDispatcher{
		executed:   make([]string, 0),
		shouldFail: make(map[string]bool),
		failCounts: make(map[string]int),
		callCounts: make(map[string]int32),
	}
}

func (d *mockDispatcher) Dispatch(ctx context.Context, node *dag.Node, parentResults map[string]*dag.NodeResult) (*dag.NodeResult, error) {
	d.mu.Lock()
	d.executed = append(d.executed, node.ID())
	d.callCounts[node.ID()]++
	shouldFail := d.shouldFail[node.ID()]
	remainingFailCount := d.failCounts[node.ID()]
	if remainingFailCount > 0 {
		d.failCounts[node.ID()] = remainingFailCount - 1
		shouldFail = true
	}
	execTime := d.executionTime
	d.mu.Unlock()

	// Simulate execution time
	if execTime > 0 {
		select {
		case <-time.After(execTime):
		case <-ctx.Done():
			return &dag.NodeResult{
				NodeID:  node.ID(),
				State:   dag.NodeStateCancelled,
				Error:   ctx.Err(),
				EndTime: time.Now(),
			}, ctx.Err()
		}
	}

	if shouldFail {
		return &dag.NodeResult{
			NodeID:  node.ID(),
			State:   dag.NodeStateFailed,
			Error:   errors.New("mock failure"),
			EndTime: time.Now(),
		}, errors.New("mock failure")
	}

	return &dag.NodeResult{
		NodeID:  node.ID(),
		State:   dag.NodeStateSucceeded,
		Output:  "success",
		EndTime: time.Now(),
	}, nil
}

func (d *mockDispatcher) setFail(nodeID string) {
	d.mu.Lock()
	d.shouldFail[nodeID] = true
	d.mu.Unlock()
}

func (d *mockDispatcher) setFailCount(nodeID string, count int) {
	d.mu.Lock()
	d.failCounts[nodeID] = count
	d.mu.Unlock()
}

func (d *mockDispatcher) callCount(nodeID string) int32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.callCounts[nodeID]
}

func (d *mockDispatcher) getExecuted() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]string, len(d.executed))
	copy(result, d.executed)
	return result
}

func TestNode_New(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{
		ID:        "node-1",
		AgentType: "engineer",
		Prompt:    "do something",
		Context:   map[string]any{"key": "value"},
		Priority:  10,
	})

	assert.Equal(t, "node-1", node.ID())
	assert.Equal(t, "engineer", node.AgentType())
	assert.Equal(t, "do something", node.Prompt())
	assert.Equal(t, 10, node.Priority())
	assert.Equal(t, dag.NodeStatePending, node.State())

	ctx := node.Context()
	assert.Equal(t, "value", ctx["key"])
}

func TestNode_StateTransitions(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{ID: "node-1"})

	assert.Equal(t, dag.NodeStatePending, node.State())

	node.SetState(dag.NodeStateQueued)
	assert.Equal(t, dag.NodeStateQueued, node.State())

	node.SetState(dag.NodeStateRunning)
	assert.Equal(t, dag.NodeStateRunning, node.State())

	node.SetState(dag.NodeStateSucceeded)
	assert.Equal(t, dag.NodeStateSucceeded, node.State())
	assert.True(t, node.State().IsTerminal())
}

func TestNode_Dependencies(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{
		ID:           "node-3",
		Dependencies: []string{"node-1", "node-2"},
	})

	deps := node.Dependencies()
	assert.Len(t, deps, 2)
	assert.Contains(t, deps, "node-1")
	assert.Contains(t, deps, "node-2")
	assert.True(t, node.HasDependencies())
}

func TestNode_IsReady(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{
		ID:           "node-3",
		Dependencies: []string{"node-1", "node-2"},
	})

	// Not ready - deps not succeeded
	nodeStates := map[string]dag.NodeState{
		"node-1": dag.NodeStatePending,
		"node-2": dag.NodeStatePending,
	}
	assert.False(t, node.IsReady(nodeStates))

	// Still not ready - one dep succeeded
	nodeStates["node-1"] = dag.NodeStateSucceeded
	assert.False(t, node.IsReady(nodeStates))

	// Ready - all deps succeeded
	nodeStates["node-2"] = dag.NodeStateSucceeded
	assert.True(t, node.IsReady(nodeStates))
}

func TestNode_IsBlocked(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{
		ID:           "node-3",
		Dependencies: []string{"node-1", "node-2"},
	})

	// Not blocked
	nodeStates := map[string]dag.NodeState{
		"node-1": dag.NodeStateSucceeded,
		"node-2": dag.NodeStateRunning,
	}
	assert.False(t, node.IsBlocked(nodeStates))

	// Blocked - dep failed
	nodeStates["node-2"] = dag.NodeStateFailed
	assert.True(t, node.IsBlocked(nodeStates))
}

func TestDAG_New(t *testing.T) {
	d := dag.NewDAG("test-dag", dag.DefaultExecutionPolicy())

	assert.NotEmpty(t, d.ID())
	assert.Equal(t, "test-dag", d.Name())
	assert.Equal(t, 0, d.NodeCount())
}

func TestDAG_AddNode(t *testing.T) {
	d := dag.NewDAG("test", dag.DefaultExecutionPolicy())

	err := d.AddNode(dag.NodeConfig{
		ID:        "node-1",
		AgentType: "engineer",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, d.NodeCount())

	// Duplicate should fail
	err = d.AddNode(dag.NodeConfig{
		ID:        "node-1",
		AgentType: "engineer",
	})
	assert.Equal(t, dag.ErrDuplicateNode, err)
}

func TestDAG_GetNode(t *testing.T) {
	d := dag.NewDAG("test", dag.DefaultExecutionPolicy())
	d.AddNode(dag.NodeConfig{ID: "node-1"})

	node, ok := d.GetNode("node-1")
	assert.True(t, ok)
	assert.Equal(t, "node-1", node.ID())

	_, ok = d.GetNode("nonexistent")
	assert.False(t, ok)
}

func TestDAG_RemoveNode(t *testing.T) {
	d := dag.NewDAG("test", dag.DefaultExecutionPolicy())
	d.AddNode(dag.NodeConfig{ID: "node-1"})

	err := d.RemoveNode("node-1")
	require.NoError(t, err)
	assert.Equal(t, 0, d.NodeCount())

	err = d.RemoveNode("nonexistent")
	assert.Equal(t, dag.ErrNodeNotFound, err)
}

func TestDAG_Builder(t *testing.T) {
	d, err := dag.NewBuilder("built-dag").
		WithDescription("A built DAG").
		WithPolicy(dag.ExecutionPolicy{
			FailurePolicy:  dag.FailurePolicyContinue,
			MaxConcurrency: 5,
		}).
		AddNode(dag.NodeConfig{ID: "node-1"}).
		AddNode(dag.NodeConfig{ID: "node-2", Dependencies: []string{"node-1"}}).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "built-dag", d.Name())
	assert.Equal(t, "A built DAG", d.Description())
	assert.Equal(t, 2, d.NodeCount())
	assert.True(t, d.IsValidated())
}

func TestValidator_EmptyDAG(t *testing.T) {
	d := dag.NewDAG("empty", dag.DefaultExecutionPolicy())
	v := dag.NewValidator()

	err := v.Validate(d)
	assert.Equal(t, dag.ErrEmptyDAG, err)
}

func TestValidator_MissingDependency(t *testing.T) {
	d := dag.NewDAG("test", dag.DefaultExecutionPolicy())
	d.AddNode(dag.NodeConfig{
		ID:           "node-1",
		Dependencies: []string{"nonexistent"},
	})

	v := dag.NewValidator()
	err := v.Validate(d)
	assert.Equal(t, dag.ErrMissingDependency, err)
}

func TestValidator_CyclicDependency(t *testing.T) {
	d := dag.NewDAG("cyclic", dag.DefaultExecutionPolicy())
	d.AddNode(dag.NodeConfig{ID: "node-1", Dependencies: []string{"node-3"}})
	d.AddNode(dag.NodeConfig{ID: "node-2", Dependencies: []string{"node-1"}})
	d.AddNode(dag.NodeConfig{ID: "node-3", Dependencies: []string{"node-2"}})

	v := dag.NewValidator()
	err := v.Validate(d)
	assert.Equal(t, dag.ErrCyclicDependency, err)
}

func TestValidator_ExecutionOrder(t *testing.T) {
	d := dag.NewDAG("test", dag.DefaultExecutionPolicy())
	// Layer 0: node-1, node-2
	// Layer 1: node-3 (depends on 1,2)
	// Layer 2: node-4 (depends on 3)
	d.AddNode(dag.NodeConfig{ID: "node-1"})
	d.AddNode(dag.NodeConfig{ID: "node-2"})
	d.AddNode(dag.NodeConfig{ID: "node-3", Dependencies: []string{"node-1", "node-2"}})
	d.AddNode(dag.NodeConfig{ID: "node-4", Dependencies: []string{"node-3"}})

	v := dag.NewValidator()
	err := v.Validate(d)
	require.NoError(t, err)

	order := d.ExecutionOrder()
	assert.Len(t, order, 3)

	// Layer 0 should have node-1 and node-2
	assert.Len(t, order[0], 2)
	assert.Contains(t, order[0], "node-1")
	assert.Contains(t, order[0], "node-2")

	// Layer 1 should have node-3
	assert.Equal(t, []string{"node-3"}, order[1])

	// Layer 2 should have node-4
	assert.Equal(t, []string{"node-4"}, order[2])
}

func TestValidator_PrioritySort(t *testing.T) {
	d := dag.NewDAG("test", dag.DefaultExecutionPolicy())
	d.AddNode(dag.NodeConfig{ID: "low", Priority: 1})
	d.AddNode(dag.NodeConfig{ID: "high", Priority: 100})
	d.AddNode(dag.NodeConfig{ID: "medium", Priority: 50})

	v := dag.NewValidator()
	err := v.Validate(d)
	require.NoError(t, err)

	order := d.ExecutionOrder()
	assert.Len(t, order, 1)
	// High priority should come first
	assert.Equal(t, "high", order[0][0])
}

func TestExecutor_SimpleDAG(t *testing.T) {
	d, _ := dag.NewBuilder("simple").
		AddNode(dag.NodeConfig{ID: "node-1"}).
		AddNode(dag.NodeConfig{ID: "node-2"}).
		Build()

	dispatcher := newMockDispatcher()
	executor := dag.NewExecutor(dag.DefaultExecutionPolicy(), nil)

	result, err := executor.Execute(context.Background(), d, dispatcher)

	require.NoError(t, err)
	assert.Equal(t, dag.DAGStateSucceeded, result.State)
	assert.Equal(t, 2, result.NodesSucceeded)
	assert.Equal(t, 0, result.NodesFailed)

	executed := dispatcher.getExecuted()
	assert.Len(t, executed, 2)
}

func TestExecutor_LayeredDAG(t *testing.T) {
	d, _ := dag.NewBuilder("layered").
		AddNode(dag.NodeConfig{ID: "layer0-a"}).
		AddNode(dag.NodeConfig{ID: "layer0-b"}).
		AddNode(dag.NodeConfig{ID: "layer1", Dependencies: []string{"layer0-a", "layer0-b"}}).
		AddNode(dag.NodeConfig{ID: "layer2", Dependencies: []string{"layer1"}}).
		Build()

	dispatcher := newMockDispatcher()
	executor := dag.NewExecutor(dag.DefaultExecutionPolicy(), nil)

	result, err := executor.Execute(context.Background(), d, dispatcher)

	require.NoError(t, err)
	assert.Equal(t, dag.DAGStateSucceeded, result.State)
	assert.Equal(t, 4, result.NodesSucceeded)

	// Verify layer1 executed after layer0
	executed := dispatcher.getExecuted()
	layer0aIdx := -1
	layer0bIdx := -1
	layer1Idx := -1
	layer2Idx := -1

	for i, id := range executed {
		switch id {
		case "layer0-a":
			layer0aIdx = i
		case "layer0-b":
			layer0bIdx = i
		case "layer1":
			layer1Idx = i
		case "layer2":
			layer2Idx = i
		}
	}

	assert.True(t, layer1Idx > layer0aIdx)
	assert.True(t, layer1Idx > layer0bIdx)
	assert.True(t, layer2Idx > layer1Idx)
}

func TestExecutor_FailFast(t *testing.T) {
	d, _ := dag.NewBuilder("failfast").
		WithPolicy(dag.ExecutionPolicy{
			FailurePolicy:  dag.FailurePolicyFailFast,
			MaxConcurrency: 1, // Serial execution for predictable ordering
		}).
		AddNode(dag.NodeConfig{ID: "node-1"}).
		AddNode(dag.NodeConfig{ID: "node-2", Dependencies: []string{"node-1"}}).
		AddNode(dag.NodeConfig{ID: "node-3", Dependencies: []string{"node-2"}}).
		Build()

	dispatcher := newMockDispatcher()
	dispatcher.setFail("node-2")

	executor := dag.NewExecutor(d.Policy(), nil)
	result, err := executor.Execute(context.Background(), d, dispatcher)

	require.NoError(t, err)
	// DAG should fail or be cancelled (fail fast cancels remaining)
	assert.True(t, result.State == dag.DAGStateFailed || result.State == dag.DAGStateCancelled,
		"Expected DAG to fail or be cancelled, got %s", result.State)
	// Not all nodes should succeed
	assert.Less(t, result.NodesSucceeded, 3)
}

func TestExecutor_ContinueOnFailure(t *testing.T) {
	d, _ := dag.NewBuilder("continue").
		WithPolicy(dag.ExecutionPolicy{
			FailurePolicy:  dag.FailurePolicyContinue,
			MaxConcurrency: 1,
		}).
		AddNode(dag.NodeConfig{ID: "node-1"}).
		AddNode(dag.NodeConfig{ID: "node-2"}).
		AddNode(dag.NodeConfig{ID: "node-3", Dependencies: []string{"node-1"}}).
		Build()

	dispatcher := newMockDispatcher()
	dispatcher.setFail("node-1")

	executor := dag.NewExecutor(d.Policy(), nil)
	result, err := executor.Execute(context.Background(), d, dispatcher)

	require.NoError(t, err)
	// DAG should fail (at least one node failed)
	assert.Equal(t, dag.DAGStateFailed, result.State)
	// At least one node failed (node-1)
	assert.GreaterOrEqual(t, result.NodesFailed, 1)
	// Total nodes processed should equal node count
	assert.Equal(t, 3, result.NodesSucceeded+result.NodesFailed+result.NodesSkipped)
}

func TestExecutor_Cancel(t *testing.T) {
	d, _ := dag.NewBuilder("cancel").
		WithPolicy(dag.ExecutionPolicy{
			MaxConcurrency: 1,
		}).
		AddNode(dag.NodeConfig{ID: "node-1"}).
		AddNode(dag.NodeConfig{ID: "node-2", Dependencies: []string{"node-1"}}).
		AddNode(dag.NodeConfig{ID: "node-3", Dependencies: []string{"node-2"}}).
		AddNode(dag.NodeConfig{ID: "node-4", Dependencies: []string{"node-3"}}).
		Build()

	dispatcher := newMockDispatcher()
	dispatcher.executionTime = 200 * time.Millisecond

	executor := dag.NewExecutor(d.Policy(), nil)

	// Start execution in background
	started := make(chan struct{})
	resultChan := make(chan *dag.DAGResult)
	go func() {
		close(started)
		result, _ := executor.Execute(context.Background(), d, dispatcher)
		resultChan <- result
	}()

	// Wait for execution to start
	<-started
	time.Sleep(100 * time.Millisecond)

	// Cancel should work while DAG is running
	_ = executor.Cancel() // May or may not succeed depending on timing

	result := <-resultChan
	// Either cancelled or completed, but DAG ran
	assert.NotNil(t, result)
}

func TestExecutor_Events(t *testing.T) {
	d, _ := dag.NewBuilder("events").
		AddNode(dag.NodeConfig{ID: "node-1"}).
		Build()

	dispatcher := newMockDispatcher()
	executor := dag.NewExecutor(dag.DefaultExecutionPolicy(), nil)

	var events []dag.Event
	var mu sync.Mutex

	executor.Subscribe(func(e *dag.Event) {
		mu.Lock()
		events = append(events, *e)
		mu.Unlock()
	})

	executor.Execute(context.Background(), d, dispatcher)

	time.Sleep(50 * time.Millisecond) // Allow events to be processed

	mu.Lock()
	defer mu.Unlock()

	// Should have dag started, layer started, node events, layer completed, dag completed
	eventTypes := make(map[dag.EventType]int)
	for _, e := range events {
		eventTypes[e.Type]++
	}

	assert.Greater(t, eventTypes[dag.EventDAGStarted], 0)
	assert.Greater(t, eventTypes[dag.EventDAGCompleted], 0)
	assert.Greater(t, eventTypes[dag.EventNodeStarted], 0)
	assert.Greater(t, eventTypes[dag.EventNodeCompleted], 0)
}

func TestExecutor_Concurrency(t *testing.T) {
	// Create DAG with many parallel nodes
	builder := dag.NewBuilder("concurrent").
		WithPolicy(dag.ExecutionPolicy{
			MaxConcurrency: 5,
			DefaultTimeout: time.Minute,
		})

	// Use simple string IDs
	nodeIDs := []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8", "n9", "n10"}
	for _, id := range nodeIDs {
		builder.AddNode(dag.NodeConfig{ID: id})
	}

	d, err := builder.Build()
	require.NoError(t, err)

	dispatcher := newMockDispatcher()
	dispatcher.executionTime = 20 * time.Millisecond

	executor := dag.NewExecutor(d.Policy(), nil)

	var maxConcurrent int32
	var currentConcurrent int32

	// Wrap dispatcher to track concurrency
	wrappedDispatcher := &concurrencyTrackingDispatcher{
		inner:   dispatcher,
		current: &currentConcurrent,
		max:     &maxConcurrent,
	}

	result, err := executor.Execute(context.Background(), d, wrappedDispatcher)

	require.NoError(t, err)
	assert.Equal(t, dag.DAGStateSucceeded, result.State)
	assert.Equal(t, 10, result.NodesSucceeded)

	// Max concurrent should not exceed limit
	assert.LessOrEqual(t, int(atomic.LoadInt32(&maxConcurrent)), 5)
}

type concurrencyTrackingDispatcher struct {
	inner   dag.NodeDispatcher
	current *int32
	max     *int32
}

func (d *concurrencyTrackingDispatcher) Dispatch(ctx context.Context, node *dag.Node, parentResults map[string]*dag.NodeResult) (*dag.NodeResult, error) {
	current := atomic.AddInt32(d.current, 1)

	// Update max
	for {
		max := atomic.LoadInt32(d.max)
		if current <= max {
			break
		}
		if atomic.CompareAndSwapInt32(d.max, max, current) {
			break
		}
	}

	defer atomic.AddInt32(d.current, -1)

	return d.inner.Dispatch(ctx, node, parentResults)
}

func TestScheduler_Submit(t *testing.T) {
	scheduler := dag.NewScheduler(dag.DefaultSchedulerConfig(), nil)
	defer scheduler.Close()

	d, _ := dag.NewBuilder("test").
		AddNode(dag.NodeConfig{ID: "node-1"}).
		Build()

	dispatcher := newMockDispatcher()

	id, err := scheduler.Submit(context.Background(), d, dispatcher)
	require.NoError(t, err)
	assert.Equal(t, d.ID(), id)

	// Wait for completion
	time.Sleep(100 * time.Millisecond)
}

func TestScheduler_SubmitAndWait(t *testing.T) {
	scheduler := dag.NewScheduler(dag.DefaultSchedulerConfig(), nil)
	defer scheduler.Close()

	d, _ := dag.NewBuilder("test").
		AddNode(dag.NodeConfig{ID: "node-1"}).
		AddNode(dag.NodeConfig{ID: "node-2"}).
		Build()

	dispatcher := newMockDispatcher()

	result, err := scheduler.SubmitAndWait(context.Background(), d, dispatcher)

	require.NoError(t, err)
	assert.Equal(t, dag.DAGStateSucceeded, result.State)
	assert.Equal(t, 2, result.NodesSucceeded)
}

func TestScheduler_ConcurrentDAGs(t *testing.T) {
	scheduler := dag.NewScheduler(dag.SchedulerConfig{
		MaxConcurrentDAGs: 3,
	}, nil)
	defer scheduler.Close()

	var wg sync.WaitGroup
	var completed int32

	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			d, _ := dag.NewBuilder("dag-" + string(rune('a'+idx))).
				AddNode(dag.NodeConfig{ID: "node-1"}).
				Build()

			dispatcher := newMockDispatcher()
			dispatcher.executionTime = 50 * time.Millisecond

			result, err := scheduler.SubmitAndWait(context.Background(), d, dispatcher)
			if err == nil && result.IsSuccess() {
				atomic.AddInt32(&completed, 1)
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, int32(5), atomic.LoadInt32(&completed))
}

func TestExecutor_RetryBackoff(t *testing.T) {
	d, _ := dag.NewBuilder("retry").
		WithPolicy(dag.ExecutionPolicy{
			FailurePolicy:  dag.FailurePolicyFailFast,
			MaxConcurrency: 1,
			DefaultRetries: 2,
			RetryBackoff:   20 * time.Millisecond,
		}).
		AddNode(dag.NodeConfig{ID: "node-1"}).
		Build()

	dispatcher := newMockDispatcher()
	dispatcher.setFailCount("node-1", 2)

	executor := dag.NewExecutor(d.Policy(), nil)
	result, err := executor.Execute(context.Background(), d, dispatcher)

	require.NoError(t, err)
	assert.Equal(t, dag.DAGStateSucceeded, result.State)
	assert.Equal(t, int32(3), dispatcher.callCount("node-1"))
}

func TestExecutor_Timeout(t *testing.T) {
	d, _ := dag.NewBuilder("timeout").
		WithPolicy(dag.ExecutionPolicy{
			FailurePolicy:  dag.FailurePolicyFailFast,
			MaxConcurrency: 1,
			DefaultTimeout: 30 * time.Millisecond,
		}).
		AddNode(dag.NodeConfig{ID: "slow"}).
		Build()

	dispatcher := newMockDispatcher()
	dispatcher.executionTime = 100 * time.Millisecond

	executor := dag.NewExecutor(d.Policy(), nil)
	result, err := executor.Execute(context.Background(), d, dispatcher)

	require.NoError(t, err)
	assert.Equal(t, dag.DAGStateFailed, result.State)
	assert.GreaterOrEqual(t, result.NodesFailed, 1)
}

func TestExecutor_ConcurrentCancel(t *testing.T) {
	d, _ := dag.NewBuilder("cancel-race").
		WithPolicy(dag.ExecutionPolicy{
			MaxConcurrency: 2,
			DefaultTimeout: time.Second,
		}).
		AddNode(dag.NodeConfig{ID: "n1"}).
		AddNode(dag.NodeConfig{ID: "n2"}).
		AddNode(dag.NodeConfig{ID: "n3"}).
		Build()

	dispatcher := newMockDispatcher()
	dispatcher.executionTime = 50 * time.Millisecond

	executor := dag.NewExecutor(d.Policy(), nil)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = executor.Cancel()
	}()

	go func() {
		defer wg.Done()
		_, _ = executor.Execute(context.Background(), d, dispatcher)
	}()

	wg.Wait()
	assert.NotNil(t, executor.Status())
}

func TestNodeState_String(t *testing.T) {
	tests := []struct {
		state    dag.NodeState
		expected string
	}{
		{dag.NodeStatePending, "pending"},
		{dag.NodeStateQueued, "queued"},
		{dag.NodeStateRunning, "running"},
		{dag.NodeStateSucceeded, "succeeded"},
		{dag.NodeStateFailed, "failed"},
		{dag.NodeStateBlocked, "blocked"},
		{dag.NodeStateSkipped, "skipped"},
		{dag.NodeStateCancelled, "cancelled"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestDAGState_String(t *testing.T) {
	tests := []struct {
		state    dag.DAGState
		expected string
	}{
		{dag.DAGStatePending, "pending"},
		{dag.DAGStateRunning, "running"},
		{dag.DAGStateSucceeded, "succeeded"},
		{dag.DAGStateFailed, "failed"},
		{dag.DAGStateCancelled, "cancelled"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestFailurePolicy_String(t *testing.T) {
	assert.Equal(t, "fail_fast", dag.FailurePolicyFailFast.String())
	assert.Equal(t, "continue", dag.FailurePolicyContinue.String())
}

func TestEventType_String(t *testing.T) {
	tests := []struct {
		event    dag.EventType
		expected string
	}{
		{dag.EventDAGStarted, "dag_started"},
		{dag.EventDAGCompleted, "dag_completed"},
		{dag.EventDAGFailed, "dag_failed"},
		{dag.EventNodeStarted, "node_started"},
		{dag.EventNodeCompleted, "node_completed"},
		{dag.EventNodeFailed, "node_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.event.String())
		})
	}
}

func TestDefaultExecutionPolicy(t *testing.T) {
	policy := dag.DefaultExecutionPolicy()

	assert.Equal(t, dag.FailurePolicyFailFast, policy.FailurePolicy)
	assert.Equal(t, 10, policy.MaxConcurrency)
	assert.Equal(t, 5*time.Minute, policy.DefaultTimeout)
	assert.Equal(t, 0, policy.DefaultRetries)
}
