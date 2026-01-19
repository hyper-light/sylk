package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHandoffTrigger implements HandoffTrigger for testing.
type mockHandoffTrigger struct {
	mu               sync.Mutex
	shouldHandoffFn  func(agentID string, contextUsage float64) bool
	triggerHandoffFn func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error)
	calls            []mockCall
}

type mockCall struct {
	method       string
	agentID      string
	contextUsage float64
	request      *HandoffRequest
}

func newMockHandoffTrigger() *mockHandoffTrigger {
	return &mockHandoffTrigger{
		calls: make([]mockCall, 0),
	}
}

func (m *mockHandoffTrigger) ShouldHandoff(agentID string, contextUsage float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{
		method:       "ShouldHandoff",
		agentID:      agentID,
		contextUsage: contextUsage,
	})
	if m.shouldHandoffFn != nil {
		return m.shouldHandoffFn(agentID, contextUsage)
	}
	return contextUsage >= DefaultContextThreshold
}

func (m *mockHandoffTrigger) TriggerHandoff(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{
		method:  "TriggerHandoff",
		request: req,
	})
	if m.triggerHandoffFn != nil {
		return m.triggerHandoffFn(ctx, req)
	}
	return &HandoffResult{Success: true, NewAgentID: "new-agent-123"}, nil
}

func (m *mockHandoffTrigger) getCalls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]mockCall, len(m.calls))
	copy(result, m.calls)
	return result
}

func (m *mockHandoffTrigger) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = make([]mockCall, 0)
}

// TestNewAgentContextCheckHook tests constructor.
func TestNewAgentContextCheckHook(t *testing.T) {
	mock := newMockHandoffTrigger()
	hook := NewAgentContextCheckHook(mock)

	assert.NotNil(t, hook)
	assert.Equal(t, "agent_context_check", hook.Name())
	assert.Equal(t, skills.HookPhasePostPrompt, hook.Phase())
	assert.Equal(t, skills.HookPriorityHigh, hook.Priority())
	assert.Equal(t, DefaultContextThreshold, hook.GetThreshold())
}

// TestNewAgentContextCheckHook_NilManager tests constructor with nil manager.
func TestNewAgentContextCheckHook_NilManager(t *testing.T) {
	hook := NewAgentContextCheckHook(nil)

	assert.NotNil(t, hook)
	assert.Equal(t, "agent_context_check", hook.Name())
}

// TestAgentContextCheckHook_ShouldRun tests agent type filtering.
func TestAgentContextCheckHook_ShouldRun(t *testing.T) {
	hook := NewAgentContextCheckHook(nil)

	tests := []struct {
		agentType string
		expected  bool
	}{
		{"engineer", true},
		{"designer", true},
		{"inspector", true},
		{"tester", true},
		{"orchestrator", true},
		{"guide", true},
		{"librarian", false},
		{"academic", false},
		{"archivalist", false},
		{"architect", false},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.agentType, func(t *testing.T) {
			assert.Equal(t, tt.expected, hook.ShouldRun(tt.agentType))
		})
	}
}

// TestAgentContextCheckHook_Execute_BelowThreshold tests no handoff below threshold.
func TestAgentContextCheckHook_Execute_BelowThreshold(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return contextUsage >= DefaultContextThreshold
	}

	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 50000, // ~39% of 128K
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	assert.Nil(t, result.Error)

	calls := mock.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "ShouldHandoff", calls[0].method)
}

// TestAgentContextCheckHook_Execute_AtThreshold tests handoff at exact threshold.
func TestAgentContextCheckHook_Execute_AtThreshold(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return contextUsage >= DefaultContextThreshold
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		return &HandoffResult{Success: true, NewAgentID: "new-agent"}, nil
	}

	hook := NewAgentContextCheckHook(mock)

	// 75% of 128000 = 96000
	data := &skills.PromptHookData{
		AgentID:        "agent-1",
		ConversationID: "session-1",
		TokensUsed:     96000,
		Metadata:       map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	assert.Nil(t, result.Error)

	calls := mock.getCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, "ShouldHandoff", calls[0].method)
	assert.Equal(t, "TriggerHandoff", calls[1].method)
	assert.Equal(t, "context_threshold", calls[1].request.TriggerReason)
	assert.Equal(t, "engineer", calls[1].request.AgentType)
}

// TestAgentContextCheckHook_Execute_AboveThreshold tests handoff above threshold.
func TestAgentContextCheckHook_Execute_AboveThreshold(t *testing.T) {
	mock := newMockHandoffTrigger()
	triggered := false
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return contextUsage >= DefaultContextThreshold
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		triggered = true
		return &HandoffResult{Success: true, NewAgentID: "new-agent"}, nil
	}

	hook := NewAgentContextCheckHook(mock)

	// 90% of 128000 = 115200
	data := &skills.PromptHookData{
		AgentID:        "agent-1",
		ConversationID: "session-1",
		TokensUsed:     115200,
		Metadata:       map[string]any{"agent_type": "designer"},
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	assert.True(t, triggered)
	assert.Equal(t, 1, hook.GetHandoffIndex("agent-1"))
}

// TestAgentContextCheckHook_Execute_NilData tests handling of nil data.
func TestAgentContextCheckHook_Execute_NilData(t *testing.T) {
	mock := newMockHandoffTrigger()
	hook := NewAgentContextCheckHook(mock)

	result := hook.Execute(context.Background(), nil)

	assert.True(t, result.Continue)
	assert.Empty(t, mock.getCalls())
}

// TestAgentContextCheckHook_Execute_NonPipelineAgent tests non-pipeline agents are skipped.
func TestAgentContextCheckHook_Execute_NonPipelineAgent(t *testing.T) {
	mock := newMockHandoffTrigger()
	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "librarian-1",
		TokensUsed: 115200, // Above threshold
		Metadata:   map[string]any{"agent_type": "librarian"},
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	assert.Empty(t, mock.getCalls())
}

// TestAgentContextCheckHook_Execute_NilManager tests behavior with nil manager.
func TestAgentContextCheckHook_Execute_NilManager(t *testing.T) {
	hook := NewAgentContextCheckHook(nil)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200, // Above threshold
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	assert.Nil(t, result.Error)
}

// TestAgentContextCheckHook_Execute_HandoffError tests error handling.
func TestAgentContextCheckHook_Execute_HandoffError(t *testing.T) {
	mock := newMockHandoffTrigger()
	expectedErr := errors.New("handoff failed")
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return true
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		return nil, expectedErr
	}

	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	assert.Equal(t, expectedErr, result.Error)
}

// TestAgentContextCheckHook_Execute_MultipleHandoffs tests multiple handoffs.
func TestAgentContextCheckHook_Execute_MultipleHandoffs(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return true
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		return &HandoffResult{Success: true}, nil
	}

	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	// First handoff
	hook.Execute(context.Background(), data)
	assert.Equal(t, 1, hook.GetHandoffIndex("agent-1"))

	// Second handoff
	hook.Execute(context.Background(), data)
	assert.Equal(t, 2, hook.GetHandoffIndex("agent-1"))

	// Third handoff
	hook.Execute(context.Background(), data)
	assert.Equal(t, 3, hook.GetHandoffIndex("agent-1"))

	// Verify handoff index is passed correctly
	calls := mock.getCalls()
	require.Len(t, calls, 6) // 3 ShouldHandoff + 3 TriggerHandoff
	assert.Equal(t, 0, calls[1].request.HandoffIndex)
	assert.Equal(t, 1, calls[3].request.HandoffIndex)
	assert.Equal(t, 2, calls[5].request.HandoffIndex)
}

// TestAgentContextCheckHook_GetSetThreshold tests threshold getter/setter.
func TestAgentContextCheckHook_GetSetThreshold(t *testing.T) {
	hook := NewAgentContextCheckHook(nil)

	assert.Equal(t, DefaultContextThreshold, hook.GetThreshold())

	hook.SetThreshold(0.80)
	assert.Equal(t, 0.80, hook.GetThreshold())

	hook.SetThreshold(0.50)
	assert.Equal(t, 0.50, hook.GetThreshold())
}

// TestAgentContextCheckHook_GetLastContextUsage tests context usage tracking.
func TestAgentContextCheckHook_GetLastContextUsage(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return false
	}

	hook := NewAgentContextCheckHook(mock)

	// Before execution - no data
	_, ok := hook.GetLastContextUsage("agent-1")
	assert.False(t, ok)

	// Execute with some token usage
	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 64000, // 50% of 128K
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	hook.Execute(context.Background(), data)

	usage, ok := hook.GetLastContextUsage("agent-1")
	assert.True(t, ok)
	assert.InDelta(t, 0.5, usage, 0.01)
}

// TestAgentContextCheckHook_ResetAgent tests agent state reset.
func TestAgentContextCheckHook_ResetAgent(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return true
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		return &HandoffResult{Success: true}, nil
	}

	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	// Execute multiple times
	hook.Execute(context.Background(), data)
	hook.Execute(context.Background(), data)
	assert.Equal(t, 2, hook.GetHandoffIndex("agent-1"))

	_, ok := hook.GetLastContextUsage("agent-1")
	assert.True(t, ok)

	// Reset
	hook.ResetAgent("agent-1")

	assert.Equal(t, 0, hook.GetHandoffIndex("agent-1"))
	_, ok = hook.GetLastContextUsage("agent-1")
	assert.False(t, ok)
}

// TestAgentContextCheckHook_Execute_ZeroTokens tests handling of zero tokens.
func TestAgentContextCheckHook_Execute_ZeroTokens(t *testing.T) {
	mock := newMockHandoffTrigger()
	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 0,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	usage, ok := hook.GetLastContextUsage("agent-1")
	assert.True(t, ok)
	assert.Equal(t, 0.0, usage)
}

// TestAgentContextCheckHook_Execute_NegativeTokens tests handling of negative tokens.
func TestAgentContextCheckHook_Execute_NegativeTokens(t *testing.T) {
	mock := newMockHandoffTrigger()
	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: -100,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	usage, ok := hook.GetLastContextUsage("agent-1")
	assert.True(t, ok)
	assert.Equal(t, 0.0, usage)
}

// TestAgentContextCheckHook_Execute_NoMetadata tests handling of missing metadata.
func TestAgentContextCheckHook_Execute_NoMetadata(t *testing.T) {
	mock := newMockHandoffTrigger()
	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200,
		Metadata:   nil,
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	// Should not trigger handoff due to missing agent_type
	assert.Empty(t, mock.getCalls())
}

// TestAgentContextCheckHook_Execute_WrongMetadataType tests wrong metadata type.
func TestAgentContextCheckHook_Execute_WrongMetadataType(t *testing.T) {
	mock := newMockHandoffTrigger()
	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200,
		Metadata:   map[string]any{"agent_type": 123}, // wrong type
	}

	result := hook.Execute(context.Background(), data)

	assert.True(t, result.Continue)
	assert.Empty(t, mock.getCalls())
}

// TestAgentContextCheckHook_Concurrency tests concurrent access safety.
func TestAgentContextCheckHook_Concurrency(t *testing.T) {
	mock := newMockHandoffTrigger()
	var handoffCount atomic.Int32
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return contextUsage >= 0.5
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		handoffCount.Add(1)
		return &HandoffResult{Success: true}, nil
	}

	hook := NewAgentContextCheckHook(mock)

	const goroutines = 100
	const iterations = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				agentID := "agent-" + string(rune('A'+workerID%26))
				data := &skills.PromptHookData{
					AgentID:    agentID,
					TokensUsed: 80000, // Above 50% threshold
					Metadata:   map[string]any{"agent_type": PipelineAgentTypes[workerID%len(PipelineAgentTypes)]},
				}

				result := hook.Execute(context.Background(), data)
				assert.True(t, result.Continue)
			}
		}(i)
	}

	wg.Wait()

	// Verify no panics occurred and handoffs were triggered
	assert.Greater(t, handoffCount.Load(), int32(0))
}

// TestAgentContextCheckHook_ConcurrentThresholdModification tests concurrent threshold changes.
func TestAgentContextCheckHook_ConcurrentThresholdModification(t *testing.T) {
	hook := NewAgentContextCheckHook(nil)

	const goroutines = 50
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			threshold := float64(id) / float64(goroutines)
			hook.SetThreshold(threshold)
			_ = hook.GetThreshold()
		}(i)
	}

	wg.Wait()

	// Verify threshold is a valid value
	threshold := hook.GetThreshold()
	assert.GreaterOrEqual(t, threshold, 0.0)
	assert.LessOrEqual(t, threshold, 1.0)
}

// TestAgentContextCheckHook_ConcurrentResetAndExecute tests concurrent reset and execute.
func TestAgentContextCheckHook_ConcurrentResetAndExecute(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return false
	}

	hook := NewAgentContextCheckHook(mock)

	const goroutines = 50
	var wg sync.WaitGroup

	ctx := context.Background()

	for i := 0; i < goroutines; i++ {
		wg.Add(2)

		// Executor goroutine
		go func() {
			defer wg.Done()
			data := &skills.PromptHookData{
				AgentID:    "agent-1",
				TokensUsed: 64000,
				Metadata:   map[string]any{"agent_type": "engineer"},
			}
			hook.Execute(ctx, data)
		}()

		// Reset goroutine
		go func() {
			defer wg.Done()
			hook.ResetAgent("agent-1")
		}()
	}

	wg.Wait()
	// Test passes if no race conditions or panics occurred
}

// TestAgentContextCheckHook_ContextCancellation tests context cancellation handling.
func TestAgentContextCheckHook_ContextCancellation(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return true
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return &HandoffResult{Success: true}, nil
		}
	}

	hook := NewAgentContextCheckHook(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(ctx, data)

	// Hook should handle cancelled context gracefully
	assert.True(t, result.Continue)
}

// TestAgentContextCheckHook_HandoffFailure tests unsuccessful handoff result.
func TestAgentContextCheckHook_HandoffFailure(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return true
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		return &HandoffResult{Success: false, Error: errors.New("resource unavailable")}, nil
	}

	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(context.Background(), data)

	assert.False(t, result.Continue) // Should stop if handoff failed
	assert.Equal(t, 1, hook.GetHandoffIndex("agent-1"))
}

// TestPipelineAgentTypes verifies all expected agent types are defined.
func TestPipelineAgentTypes(t *testing.T) {
	expected := []string{"engineer", "designer", "inspector", "tester", "orchestrator", "guide"}
	assert.Equal(t, expected, PipelineAgentTypes)
}

// TestDefaultContextThreshold verifies the default threshold constant.
func TestDefaultContextThreshold(t *testing.T) {
	assert.Equal(t, 0.75, DefaultContextThreshold)
}

// TestAgentContextCheckHook_AllAgentTypes tests all pipeline agent types.
func TestAgentContextCheckHook_AllAgentTypes(t *testing.T) {
	mock := newMockHandoffTrigger()
	handoffs := make(map[string]bool)
	var mu sync.Mutex

	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return true
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		mu.Lock()
		handoffs[req.AgentType] = true
		mu.Unlock()
		return &HandoffResult{Success: true}, nil
	}

	hook := NewAgentContextCheckHook(mock)

	for _, agentType := range PipelineAgentTypes {
		data := &skills.PromptHookData{
			AgentID:    "agent-" + agentType,
			TokensUsed: 115200,
			Metadata:   map[string]any{"agent_type": agentType},
		}

		result := hook.Execute(context.Background(), data)
		assert.True(t, result.Continue)
	}

	// Verify all agent types triggered handoffs
	for _, agentType := range PipelineAgentTypes {
		assert.True(t, handoffs[agentType], "Expected handoff for %s", agentType)
	}
}

// TestAgentContextCheckHook_Execute_ContextTimeout tests timeout during handoff.
func TestAgentContextCheckHook_Execute_ContextTimeout(t *testing.T) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return true
	}
	mock.triggerHandoffFn = func(ctx context.Context, req *HandoffRequest) (*HandoffResult, error) {
		// Simulate slow operation
		select {
		case <-time.After(100 * time.Millisecond):
			return &HandoffResult{Success: true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	hook := NewAgentContextCheckHook(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 115200,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	result := hook.Execute(ctx, data)

	// Should return with timeout error
	assert.True(t, result.Continue)
	assert.ErrorIs(t, result.Error, context.DeadlineExceeded)
}

// BenchmarkAgentContextCheckHook_Execute benchmarks hook execution.
func BenchmarkAgentContextCheckHook_Execute(b *testing.B) {
	mock := newMockHandoffTrigger()
	mock.shouldHandoffFn = func(agentID string, contextUsage float64) bool {
		return false
	}

	hook := NewAgentContextCheckHook(mock)

	data := &skills.PromptHookData{
		AgentID:    "agent-1",
		TokensUsed: 64000,
		Metadata:   map[string]any{"agent_type": "engineer"},
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hook.Execute(ctx, data)
	}
}

// BenchmarkAgentContextCheckHook_ShouldRun benchmarks ShouldRun check.
func BenchmarkAgentContextCheckHook_ShouldRun(b *testing.B) {
	hook := NewAgentContextCheckHook(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hook.ShouldRun("engineer")
	}
}
