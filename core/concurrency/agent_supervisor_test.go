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

type mockFileBudget struct {
	acquireCalls atomic.Int32
	releaseCalls atomic.Int32
	acquireErr   error
}

func (m *mockFileBudget) Acquire(ctx context.Context) error {
	m.acquireCalls.Add(1)
	return m.acquireErr
}

func (m *mockFileBudget) Release() {
	m.releaseCalls.Add(1)
}

func TestAgentSupervisor_OpenFile_AcquiresBudget(t *testing.T) {
	s := newTestSupervisor(t)
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.txt"

	budget := &mockFileBudget{}
	s.SetFileBudget(budget)

	handle, err := s.OpenFile(context.Background(), tmpFile, FileModeWrite)
	require.NoError(t, err)
	require.NotNil(t, handle)

	assert.Equal(t, int32(1), budget.acquireCalls.Load())
	assert.Equal(t, 1, s.resources.Count())

	err = s.CloseFile(handle)
	require.NoError(t, err)
	assert.Equal(t, int32(1), budget.releaseCalls.Load())
}

func TestAgentSupervisor_OpenFile_NoFileBudget(t *testing.T) {
	s := newTestSupervisor(t)
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.txt"

	handle, err := s.OpenFile(context.Background(), tmpFile, FileModeWrite)
	require.NoError(t, err)
	require.NotNil(t, handle)

	assert.Equal(t, 1, s.resources.Count())
	s.CloseFile(handle)
}

func TestAgentSupervisor_OpenFile_AcquireError(t *testing.T) {
	s := newTestSupervisor(t)

	budget := &mockFileBudget{acquireErr: context.DeadlineExceeded}
	s.SetFileBudget(budget)

	handle, err := s.OpenFile(context.Background(), "/any/path", FileModeRead)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, handle)
}

func TestAgentSupervisor_OpenFile_FileOpenError(t *testing.T) {
	s := newTestSupervisor(t)

	budget := &mockFileBudget{}
	s.SetFileBudget(budget)

	handle, err := s.OpenFile(context.Background(), "/nonexistent/path/file.txt", FileModeRead)
	assert.Error(t, err)
	assert.Nil(t, handle)
	assert.Equal(t, int32(1), budget.releaseCalls.Load(), "budget should be released on file open error")
}

func TestAgentSupervisor_ForceCloseResources_ClosesFiles(t *testing.T) {
	s := newTestSupervisor(t)
	tmpDir := t.TempDir()

	budget := &mockFileBudget{}
	s.SetFileBudget(budget)

	for i := 0; i < 3; i++ {
		tmpFile := tmpDir + "/test" + string(rune('0'+i)) + ".txt"
		_, err := s.OpenFile(context.Background(), tmpFile, FileModeWrite)
		require.NoError(t, err)
	}

	assert.Equal(t, 3, s.resources.Count())

	errs := s.ForceCloseResources()
	assert.Empty(t, errs)
	assert.Equal(t, 0, s.resources.Count())
	assert.Equal(t, int32(3), budget.releaseCalls.Load())
}

func TestTrackedFileHandle_ReadWrite(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.txt"

	s := newTestSupervisor(t)
	handle, err := s.OpenFile(context.Background(), tmpFile, FileModeReadWrite)
	require.NoError(t, err)

	_, err = handle.Write([]byte("hello"))
	require.NoError(t, err)

	_, err = handle.File().Seek(0, 0)
	require.NoError(t, err)

	buf := make([]byte, 5)
	n, err := handle.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", string(buf))

	s.CloseFile(handle)
}

func TestTrackedFileHandle_DoubleCloseSafe(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.txt"

	budget := &mockFileBudget{}
	s := newTestSupervisor(t)
	s.SetFileBudget(budget)

	handle, err := s.OpenFile(context.Background(), tmpFile, FileModeWrite)
	require.NoError(t, err)

	err = handle.ForceClose()
	require.NoError(t, err)

	err = handle.ForceClose()
	require.NoError(t, err)

	assert.Equal(t, int32(1), budget.releaseCalls.Load(), "budget should only be released once")
}

func TestFileMode_ToOSFlags(t *testing.T) {
	assert.Equal(t, 0, FileModeRead.ToOSFlags())
	assert.NotEqual(t, 0, FileModeWrite.ToOSFlags())
	assert.NotEqual(t, 0, FileModeReadWrite.ToOSFlags())
	assert.NotEqual(t, 0, FileModeAppend.ToOSFlags())
}
