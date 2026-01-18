package tools

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSignalEmitter is a test double for ToolSignalEmitter.
type mockSignalEmitter struct {
	mu        sync.Mutex
	calls     []signalCall
	callCount atomic.Int64
}

type signalCall struct {
	agentID   string
	sessionID string
	toolName  string
	target    string
}

func (m *mockSignalEmitter) EmitToolCompleted(agentID, sessionID, toolName, target string) {
	m.mu.Lock()
	m.calls = append(m.calls, signalCall{
		agentID:   agentID,
		sessionID: sessionID,
		toolName:  toolName,
		target:    target,
	})
	m.mu.Unlock()
	m.callCount.Add(1)
}

func (m *mockSignalEmitter) getCalls() []signalCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]signalCall, len(m.calls))
	copy(result, m.calls)
	return result
}

func (m *mockSignalEmitter) getCallCount() int64 {
	return m.callCount.Load()
}

// =============================================================================
// Signal Emission Tests
// =============================================================================

func TestExecuteWithSignals_SuccessfulExecution(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	ctx := WithExecutorContext(context.Background(), "agent-1", "session-1")
	inv := ToolInvocation{
		Tool:    "echo",
		Command: "echo",
		Args:    []string{"hello"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.ExecuteWithSignals(ctx, inv)

	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, string(result.Stdout), "hello")

	calls := emitter.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "agent-1", calls[0].agentID)
	assert.Equal(t, "session-1", calls[0].sessionID)
	assert.Equal(t, "echo", calls[0].toolName)
	assert.Equal(t, "hello", calls[0].target)
}

func TestExecuteWithSignals_FailedExecution(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	ctx := WithExecutorContext(context.Background(), "agent-1", "session-1")
	inv := ToolInvocation{
		Tool:    "exit",
		Command: "sh",
		Args:    []string{"-c", "exit 1"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.ExecuteWithSignals(ctx, inv)

	require.NoError(t, err) // Execute returns no error, exit code in result
	assert.Equal(t, 1, result.ExitCode)

	// Signal should still be emitted on failure
	calls := emitter.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "agent-1", calls[0].agentID)
	assert.Equal(t, "session-1", calls[0].sessionID)
	assert.Equal(t, "exit", calls[0].toolName)
}

func TestExecuteWithSignals_NilEmitter(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	// Don't set emitter - should be no-op

	ctx := WithExecutorContext(context.Background(), "agent-1", "session-1")
	inv := ToolInvocation{
		Command: "echo",
		Args:    []string{"hello"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.ExecuteWithSignals(ctx, inv)

	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	// No panic, no error - silent no-op
}

func TestExecuteWithSignals_NilContext(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	// Context without IDs
	ctx := context.Background()
	inv := ToolInvocation{
		Tool:    "echo",
		Command: "echo",
		Args:    []string{"hello"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.ExecuteWithSignals(ctx, inv)

	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Signal should be emitted with empty IDs
	calls := emitter.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "", calls[0].agentID)
	assert.Equal(t, "", calls[0].sessionID)
}

// =============================================================================
// Context Key Tests
// =============================================================================

func TestWithAgentID(t *testing.T) {
	ctx := context.Background()
	ctx = WithAgentID(ctx, "test-agent")

	val := ctx.Value(AgentIDKey)
	require.NotNil(t, val)
	assert.Equal(t, "test-agent", val.(string))
}

func TestWithSessionID(t *testing.T) {
	ctx := context.Background()
	ctx = WithSessionID(ctx, "test-session")

	val := ctx.Value(SessionIDKey)
	require.NotNil(t, val)
	assert.Equal(t, "test-session", val.(string))
}

func TestWithExecutorContext(t *testing.T) {
	ctx := context.Background()
	ctx = WithExecutorContext(ctx, "test-agent", "test-session")

	agentID := ctx.Value(AgentIDKey)
	sessionID := ctx.Value(SessionIDKey)

	require.NotNil(t, agentID)
	require.NotNil(t, sessionID)
	assert.Equal(t, "test-agent", agentID.(string))
	assert.Equal(t, "test-session", sessionID.(string))
}

// =============================================================================
// Getter/Setter Tests
// =============================================================================

func TestSetSignalEmitter_MultipleSets(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter1 := &mockSignalEmitter{}
	emitter2 := &mockSignalEmitter{}

	executor.SetSignalEmitter(emitter1)
	assert.Equal(t, emitter1, executor.GetSignalEmitter())

	executor.SetSignalEmitter(emitter2)
	assert.Equal(t, emitter2, executor.GetSignalEmitter())
}

func TestGetSignalEmitter_NilHolder(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	// signalHolder is nil by default
	emitter := executor.GetSignalEmitter()
	assert.Nil(t, emitter)
}

func TestSetSignalEmitter_NilEmitter(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)
	executor.SetSignalEmitter(nil)

	assert.Nil(t, executor.GetSignalEmitter())
}

// =============================================================================
// Tool Info Extraction Tests
// =============================================================================

func TestExtractToolInfo_WithTool(t *testing.T) {
	inv := ToolInvocation{
		Tool:       "myTool",
		Command:    "command",
		Args:       []string{"arg1"},
		WorkingDir: "/work",
	}

	toolName, target := extractToolInfo(inv)

	assert.Equal(t, "myTool", toolName)
	assert.Equal(t, "/work", target)
}

func TestExtractToolInfo_FallbackToCommand(t *testing.T) {
	inv := ToolInvocation{
		Command:    "command",
		Args:       []string{"arg1"},
		WorkingDir: "/work",
	}

	toolName, target := extractToolInfo(inv)

	assert.Equal(t, "command", toolName)
	assert.Equal(t, "/work", target)
}

func TestExtractToolInfo_FallbackToArgs(t *testing.T) {
	inv := ToolInvocation{
		Command: "command",
		Args:    []string{"target-file"},
	}

	toolName, target := extractToolInfo(inv)

	assert.Equal(t, "command", toolName)
	assert.Equal(t, "target-file", target)
}

func TestExtractToolInfo_EmptyInvocation(t *testing.T) {
	inv := ToolInvocation{}

	toolName, target := extractToolInfo(inv)

	assert.Equal(t, "", toolName)
	assert.Equal(t, "", target)
}

// =============================================================================
// Context ID Extraction Tests
// =============================================================================

func TestExtractContextIDs_WithBothIDs(t *testing.T) {
	ctx := WithExecutorContext(context.Background(), "agent-1", "session-1")

	agentID, sessionID := extractContextIDs(ctx)

	assert.Equal(t, "agent-1", agentID)
	assert.Equal(t, "session-1", sessionID)
}

func TestExtractContextIDs_EmptyContext(t *testing.T) {
	ctx := context.Background()

	agentID, sessionID := extractContextIDs(ctx)

	assert.Equal(t, "", agentID)
	assert.Equal(t, "", sessionID)
}

func TestExtractContextIDs_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), AgentIDKey, 123) // int instead of string

	agentID, sessionID := extractContextIDs(ctx)

	assert.Equal(t, "", agentID)
	assert.Equal(t, "", sessionID)
}

// =============================================================================
// Concurrent Execution Tests
// =============================================================================

func TestExecuteWithSignals_ConcurrentExecution(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	const numGoroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := WithExecutorContext(context.Background(), "agent-"+string(rune('A'+idx)), "session-1")
			inv := ToolInvocation{
				Tool:    "echo",
				Command: "echo",
				Args:    []string{"hello"},
				Timeout: 5 * time.Second,
			}
			_, _ = executor.ExecuteWithSignals(ctx, inv)
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int64(numGoroutines), emitter.getCallCount())
}

func TestSetSignalEmitter_ConcurrentAccess(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	const numGoroutines = 50
	var wg sync.WaitGroup

	// Initialize the signal holder first
	executor.SetSignalEmitter(&mockSignalEmitter{})

	// Concurrently set and get
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Setter goroutine
		go func(idx int) {
			defer wg.Done()
			executor.SetSignalEmitter(&mockSignalEmitter{})
		}(i)

		// Getter goroutine
		go func() {
			defer wg.Done()
			_ = executor.GetSignalEmitter()
		}()
	}

	wg.Wait()
	// Test passes if no race is detected
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestEmitToolSignal_RaceWithSetEmitter(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	const numIterations = 100
	var wg sync.WaitGroup

	// Emit signals while changing emitter
	for i := 0; i < numIterations; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			ctx := WithExecutorContext(context.Background(), "agent-1", "session-1")
			inv := ToolInvocation{Tool: "test", Command: "true", Timeout: time.Second}
			executor.emitToolSignal(ctx, inv, nil)
		}()

		go func() {
			defer wg.Done()
			executor.SetSignalEmitter(&mockSignalEmitter{})
		}()
	}

	wg.Wait()
	// Test passes if no race is detected
}

func TestExtractContextIDs_ConcurrentAccess(t *testing.T) {
	ctx := WithExecutorContext(context.Background(), "agent-1", "session-1")

	const numGoroutines = 100
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agentID, sessionID := extractContextIDs(ctx)
			assert.Equal(t, "agent-1", agentID)
			assert.Equal(t, "session-1", sessionID)
		}()
	}

	wg.Wait()
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestExecuteWithSignals_TimeoutStillEmitsSignal(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	ctx := WithExecutorContext(context.Background(), "agent-1", "session-1")
	inv := ToolInvocation{
		Tool:    "sleep",
		Command: "sh",
		Args:    []string{"-c", "sleep 10"},
		Timeout: 100 * time.Millisecond,
	}

	result, err := executor.ExecuteWithSignals(ctx, inv)

	require.NoError(t, err)
	assert.True(t, result.Killed)

	// Signal should still be emitted after timeout
	calls := emitter.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "agent-1", calls[0].agentID)
	assert.Equal(t, "sleep", calls[0].toolName)
}

func TestExecuteWithSignals_ContextCancelStillEmitsSignal(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithAgentID(ctx, "agent-1")
	ctx = WithSessionID(ctx, "session-1")

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	inv := ToolInvocation{
		Tool:    "sleep",
		Command: "sh",
		Args:    []string{"-c", "sleep 10"},
		Timeout: 10 * time.Second,
	}

	result, err := executor.ExecuteWithSignals(ctx, inv)

	require.NoError(t, err)
	assert.True(t, result.Killed)

	// Signal should still be emitted after context cancel
	calls := emitter.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "agent-1", calls[0].agentID)
}

func TestExecuteWithSignals_MultipleExecutions(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	ctx := WithExecutorContext(context.Background(), "agent-1", "session-1")

	for i := 0; i < 5; i++ {
		inv := ToolInvocation{
			Tool:    "echo",
			Command: "echo",
			Args:    []string{"test"},
			Timeout: 5 * time.Second,
		}
		_, err := executor.ExecuteWithSignals(ctx, inv)
		require.NoError(t, err)
	}

	assert.Equal(t, int64(5), emitter.getCallCount())
}

func TestExecuteWithSignals_DifferentAgents(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	emitter := &mockSignalEmitter{}
	executor.SetSignalEmitter(emitter)

	agents := []string{"agent-1", "agent-2", "agent-3"}
	for _, agentID := range agents {
		ctx := WithExecutorContext(context.Background(), agentID, "session-1")
		inv := ToolInvocation{
			Tool:    "echo",
			Command: "echo",
			Args:    []string{"test"},
			Timeout: 5 * time.Second,
		}
		_, err := executor.ExecuteWithSignals(ctx, inv)
		require.NoError(t, err)
	}

	calls := emitter.getCalls()
	require.Len(t, calls, 3)

	agentsSeen := make(map[string]bool)
	for _, call := range calls {
		agentsSeen[call.agentID] = true
	}

	for _, agentID := range agents {
		assert.True(t, agentsSeen[agentID], "Expected signal for agent %s", agentID)
	}
}
