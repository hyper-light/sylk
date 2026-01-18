package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSupervisor(t *testing.T) *AgentSupervisor {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)
	budget.RegisterAgent("test-agent", "engineer")

	return NewAgentSupervisor(
		context.Background(),
		"test-agent",
		"engineer",
		"pipeline-1",
		budget,
		DefaultAgentSupervisorConfig(),
	)
}

func TestAgentSupervisor_BeginOperationCreatesTrackedOp(t *testing.T) {
	s := newTestSupervisor(t)

	op, err := s.BeginOperation(OpTypeToolExecution, "test-op", time.Second)
	require.NoError(t, err)

	assert.NotEmpty(t, op.ID)
	assert.Equal(t, OpTypeToolExecution, op.Type)
	assert.Equal(t, "test-agent", op.AgentID)
	assert.Equal(t, "test-op", op.Description)
	assert.Equal(t, 1, s.OperationCount())

	s.EndOperation(op, nil, nil)
	assert.Equal(t, 0, s.OperationCount())
}

func TestAgentSupervisor_BeginOperationBlocksAtMaxConcurrent(t *testing.T) {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)
	budget.RegisterAgent("test-agent", "engineer")

	cfg := DefaultAgentSupervisorConfig()
	cfg.MaxConcurrentOps = 2

	s := NewAgentSupervisor(
		context.Background(),
		"test-agent",
		"engineer",
		"pipeline-1",
		budget,
		cfg,
	)

	op1, _ := s.BeginOperation(OpTypeLLMCall, "op-1", time.Minute)
	op2, _ := s.BeginOperation(OpTypeLLMCall, "op-2", time.Minute)

	blocked := make(chan struct{})
	go func() {
		_, _ = s.BeginOperation(OpTypeLLMCall, "op-3", time.Minute)
		close(blocked)
	}()

	select {
	case <-blocked:
		t.Fatal("should have blocked at max concurrent ops")
	case <-time.After(100 * time.Millisecond):
	}

	s.EndOperation(op1, nil, nil)

	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("should have unblocked")
	}

	s.EndOperation(op2, nil, nil)
}

func TestAgentSupervisor_BeginOperationBlocksWhenPaused(t *testing.T) {
	s := newTestSupervisor(t)

	s.SignalPause()
	assert.True(t, s.pauseBarrier.IsEngaged())

	blocked := make(chan struct{})
	go func() {
		_, _ = s.BeginOperation(OpTypeFileIO, "blocked-op", time.Second)
		close(blocked)
	}()

	select {
	case <-blocked:
		t.Fatal("should have blocked when paused")
	case <-time.After(100 * time.Millisecond):
	}

	s.SignalResume()

	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("should have unblocked after resume")
	}
}

func TestAgentSupervisor_EndOperationCleansUp(t *testing.T) {
	s := newTestSupervisor(t)

	op, _ := s.BeginOperation(OpTypeNetworkIO, "test-op", time.Second)

	res := &testTrackedResource{id: "res-1", resType: ResourceTypeFile}
	s.TrackResource(op, res)

	assert.Equal(t, 1, s.resources.Count())

	s.EndOperation(op, "result", nil)

	assert.True(t, op.IsDone())
	assert.Equal(t, 0, s.resources.Count())
	assert.Equal(t, 0, s.OperationCount())
}

func TestAgentSupervisor_CancelAllCancelsOperations(t *testing.T) {
	s := newTestSupervisor(t)

	op1, _ := s.BeginOperation(OpTypeLLMCall, "op-1", time.Minute)
	op2, _ := s.BeginOperation(OpTypeLLMCall, "op-2", time.Minute)

	s.CancelAll()

	assert.Error(t, op1.Context().Err())
	assert.Error(t, op2.Context().Err())
}

func TestAgentSupervisor_ForceCloseResourcesClosesAll(t *testing.T) {
	s := newTestSupervisor(t)

	op, _ := s.BeginOperation(OpTypeToolExecution, "test-op", time.Second)

	res1 := &testTrackedResource{id: "res-1", resType: ResourceTypeProcess}
	res2 := &testTrackedResource{id: "res-2", resType: ResourceTypeConnection}

	s.TrackResource(op, res1)
	s.TrackResource(op, res2)

	errs := s.ForceCloseResources()

	assert.Empty(t, errs)
	assert.True(t, res1.closed.Load())
	assert.True(t, res2.closed.Load())
}

func TestAgentSupervisor_WaitForCompletion(t *testing.T) {
	s := newTestSupervisor(t)

	op, _ := s.BeginOperation(OpTypeLLMCall, "test-op", time.Second)

	done := make(chan error)
	go func() {
		done <- s.WaitForCompletion(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	s.EndOperation(op, nil, nil)

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("WaitForCompletion should have returned")
	}

	assert.Equal(t, SupervisorStateStopped, s.State())
}

func TestAgentSupervisor_PauseResumeWorksCorrectly(t *testing.T) {
	s := newTestSupervisor(t)

	assert.Equal(t, SupervisorStateRunning, s.State())

	s.SignalPause()
	assert.Equal(t, SupervisorStatePausing, s.State())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := s.WaitForPause(ctx)
	require.NoError(t, err)
	assert.Equal(t, SupervisorStatePaused, s.State())

	s.SignalResume()
	assert.Equal(t, SupervisorStateRunning, s.State())
}

func TestAgentSupervisor_StopAcceptingWork(t *testing.T) {
	s := newTestSupervisor(t)

	s.StopAcceptingWork()
	assert.Equal(t, SupervisorStateStopping, s.State())

	_, err := s.BeginOperation(OpTypeLLMCall, "rejected", time.Second)
	assert.ErrorIs(t, err, ErrSupervisorStopped)
}

func TestAgentSupervisor_MarkOrphansAndReport(t *testing.T) {
	s := newTestSupervisor(t)

	op1, _ := s.BeginOperation(OpTypeLLMCall, "orphan-1", time.Minute)
	op2, _ := s.BeginOperation(OpTypeToolExecution, "orphan-2", time.Minute)

	_ = op1
	_ = op2

	orphans := s.MarkOrphansAndReport()

	assert.Len(t, orphans, 2)
}

func TestAgentSupervisor_ConcurrentOperations(t *testing.T) {
	s := newTestSupervisor(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, err := s.BeginOperation(OpTypeLLMCall, "concurrent", 100*time.Millisecond)
			if err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
			s.EndOperation(op, nil, nil)
		}()
	}

	wg.Wait()
	assert.Equal(t, 0, s.OperationCount())
}

type testTrackedResource struct {
	id      string
	resType ResourceType
	closed  atomic.Bool
}

func (r *testTrackedResource) ID() string         { return r.id }
func (r *testTrackedResource) Type() ResourceType { return r.resType }
func (r *testTrackedResource) IsClosed() bool     { return r.closed.Load() }
func (r *testTrackedResource) ForceClose() error {
	r.closed.Store(true)
	return nil
}
