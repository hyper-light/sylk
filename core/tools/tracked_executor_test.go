package tools

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSupervisor struct {
	agentID          string
	beginCalled      atomic.Bool
	endCalled        atomic.Bool
	trackCalled      atomic.Bool
	trackedResources []concurrency.TrackedResource
	beginErr         error
	lastOp           *Operation
}

func (m *mockSupervisor) AgentID() string {
	return m.agentID
}

func (m *mockSupervisor) BeginOperation(opType OperationType, description string, timeout time.Duration) (*Operation, error) {
	m.beginCalled.Store(true)
	if m.beginErr != nil {
		return nil, m.beginErr
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	op := &Operation{
		ID:          "op-1",
		Type:        opType,
		AgentID:     m.agentID,
		Description: description,
		StartedAt:   time.Now(),
		Deadline:    time.Now().Add(timeout),
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	m.lastOp = op
	return op, nil
}

func (m *mockSupervisor) EndOperation(op *Operation, result any, err error) {
	m.endCalled.Store(true)
	if op.cancel != nil {
		op.cancel()
	}
}

func (m *mockSupervisor) TrackResource(op *Operation, res concurrency.TrackedResource) {
	m.trackCalled.Store(true)
	m.trackedResources = append(m.trackedResources, res)
}

type mockTool struct {
	name        string
	command     string
	args        []string
	timeout     time.Duration
	parseResult any
	parseErr    error
}

func (t *mockTool) Name() string                        { return t.name }
func (t *mockTool) Command() string                     { return t.command }
func (t *mockTool) Args(params map[string]any) []string { return t.args }
func (t *mockTool) Timeout() time.Duration              { return t.timeout }
func (t *mockTool) ParseOutput(output []byte) (any, error) {
	if t.parseErr != nil {
		return nil, t.parseErr
	}
	if t.parseResult != nil {
		return t.parseResult, nil
	}
	return string(output), nil
}

func TestTrackedToolExecutor_TracksOperationLifecycle(t *testing.T) {
	supervisor := &mockSupervisor{agentID: "test-agent"}
	executor := NewTrackedToolExecutor(nil, nil, nil, DefaultTrackedToolExecutorConfig())

	tool := &mockTool{
		name:    "echo",
		command: "echo",
		args:    []string{"hello"},
		timeout: time.Second,
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, supervisor, tool, nil)

	require.NoError(t, err)
	assert.Contains(t, result.(string), "hello")
	assert.True(t, supervisor.beginCalled.Load())
	assert.True(t, supervisor.endCalled.Load())
}

func TestTrackedToolExecutor_TracksProcess(t *testing.T) {
	supervisor := &mockSupervisor{agentID: "test-agent"}
	executor := NewTrackedToolExecutor(nil, nil, nil, DefaultTrackedToolExecutorConfig())

	tool := &mockTool{
		name:    "echo",
		command: "echo",
		args:    []string{"test"},
		timeout: time.Second,
	}

	ctx := context.Background()
	_, err := executor.Execute(ctx, supervisor, tool, nil)

	require.NoError(t, err)
	assert.True(t, supervisor.trackCalled.Load())
	assert.Len(t, supervisor.trackedResources, 1)
	assert.Equal(t, concurrency.ResourceTypeProcess, supervisor.trackedResources[0].Type())
}

func TestTrackedToolExecutor_RespectsMaxTimeout(t *testing.T) {
	cfg := TrackedToolExecutorConfig{MaxTimeout: 100 * time.Millisecond}
	executor := NewTrackedToolExecutor(nil, nil, nil, cfg)

	assert.Equal(t, 100*time.Millisecond, executor.MaxTimeout())

	normalized := executor.normalizeTimeout(time.Hour)
	assert.Equal(t, 100*time.Millisecond, normalized)

	normalized = executor.normalizeTimeout(50 * time.Millisecond)
	assert.Equal(t, 50*time.Millisecond, normalized)

	normalized = executor.normalizeTimeout(0)
	assert.Equal(t, 100*time.Millisecond, normalized)
}

func TestTrackedToolExecutor_HandlesBeginOperationError(t *testing.T) {
	supervisor := &mockSupervisor{
		agentID:  "test-agent",
		beginErr: ErrTooManyOperations,
	}
	executor := NewTrackedToolExecutor(nil, nil, nil, DefaultTrackedToolExecutorConfig())

	tool := &mockTool{
		name:    "echo",
		command: "echo",
		args:    []string{"hello"},
		timeout: time.Second,
	}

	ctx := context.Background()
	_, err := executor.Execute(ctx, supervisor, tool, nil)

	assert.ErrorIs(t, err, ErrTooManyOperations)
	assert.False(t, supervisor.endCalled.Load())
}

func TestTrackedToolExecutor_HandlesProcessError(t *testing.T) {
	supervisor := &mockSupervisor{agentID: "test-agent"}
	executor := NewTrackedToolExecutor(nil, nil, nil, DefaultTrackedToolExecutorConfig())

	tool := &mockTool{
		name:    "nonexistent",
		command: "nonexistent_command_xyz123",
		args:    nil,
		timeout: time.Second,
	}

	ctx := context.Background()
	_, err := executor.Execute(ctx, supervisor, tool, nil)

	assert.Error(t, err)
	assert.True(t, supervisor.endCalled.Load())
}

func TestTrackedToolExecutor_HandlesParseError(t *testing.T) {
	supervisor := &mockSupervisor{agentID: "test-agent"}
	executor := NewTrackedToolExecutor(nil, nil, nil, DefaultTrackedToolExecutorConfig())

	parseErr := errors.New("parse failed")
	tool := &mockTool{
		name:     "echo",
		command:  "echo",
		args:     []string{"hello"},
		timeout:  time.Second,
		parseErr: parseErr,
	}

	ctx := context.Background()
	_, err := executor.Execute(ctx, supervisor, tool, nil)

	assert.ErrorIs(t, err, parseErr)
	assert.True(t, supervisor.endCalled.Load())
}

func TestTrackedToolExecutor_WorksWithoutSandbox(t *testing.T) {
	supervisor := &mockSupervisor{agentID: "test-agent"}
	executor := NewTrackedToolExecutor(nil, nil, nil, DefaultTrackedToolExecutorConfig())

	tool := &mockTool{
		name:    "ls",
		command: "ls",
		args:    []string{"-la"},
		timeout: time.Second,
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, supervisor, tool, nil)

	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestTrackedToolExecutor_BuildsDescription(t *testing.T) {
	executor := NewTrackedToolExecutor(nil, nil, nil, DefaultTrackedToolExecutorConfig())

	tool := &mockTool{
		name:    "git",
		command: "/usr/bin/git",
	}

	desc := executor.buildDescription(tool)
	assert.Contains(t, desc, "tool=git")
	assert.Contains(t, desc, "cmd=/usr/bin/git")
}

func TestTrackedToolExecutor_DefaultConfig(t *testing.T) {
	cfg := DefaultTrackedToolExecutorConfig()
	assert.Equal(t, 5*time.Minute, cfg.MaxTimeout)
}

func TestTrackedToolExecutor_OperationType(t *testing.T) {
	supervisor := &mockSupervisor{agentID: "test-agent"}
	executor := NewTrackedToolExecutor(nil, nil, nil, DefaultTrackedToolExecutorConfig())

	tool := &mockTool{
		name:    "echo",
		command: "echo",
		args:    []string{"test"},
		timeout: time.Second,
	}

	ctx := context.Background()
	_, _ = executor.Execute(ctx, supervisor, tool, nil)

	assert.Equal(t, OpTypeToolExecution, supervisor.lastOp.Type)
}
