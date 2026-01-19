package context

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// HookPhase.String() Tests
// =============================================================================

func TestHookPhaseString(t *testing.T) {
	tests := []struct {
		name     string
		phase    HookPhase
		expected string
	}{
		{
			name:     "PrePrompt phase",
			phase:    HookPhasePrePrompt,
			expected: "pre_prompt",
		},
		{
			name:     "PostPrompt phase",
			phase:    HookPhasePostPrompt,
			expected: "post_prompt",
		},
		{
			name:     "PreTool phase",
			phase:    HookPhasePreTool,
			expected: "pre_tool",
		},
		{
			name:     "PostTool phase",
			phase:    HookPhasePostTool,
			expected: "post_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.phase.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHookPhaseStringInvalid(t *testing.T) {
	tests := []struct {
		name  string
		phase HookPhase
	}{
		{
			name:  "negative phase",
			phase: HookPhase(-1),
		},
		{
			name:  "undefined phase value 4",
			phase: HookPhase(4),
		},
		{
			name:  "large undefined phase",
			phase: HookPhase(100),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.phase.String()
			assert.Contains(t, result, "unknown_phase")
			assert.Contains(t, result, ")")
		})
	}
}

// =============================================================================
// HookPriority Constants Tests
// =============================================================================

func TestHookPriorityConstants(t *testing.T) {
	// Verify priority ordering: First < Early < Normal < Late < Last
	assert.Less(t, int(HookPriorityFirst), int(HookPriorityEarly))
	assert.Less(t, int(HookPriorityEarly), int(HookPriorityNormal))
	assert.Less(t, int(HookPriorityNormal), int(HookPriorityLate))
	assert.Less(t, int(HookPriorityLate), int(HookPriorityLast))

	// Verify specific values
	assert.Equal(t, HookPriority(0), HookPriorityFirst)
	assert.Equal(t, HookPriority(25), HookPriorityEarly)
	assert.Equal(t, HookPriority(50), HookPriorityNormal)
	assert.Equal(t, HookPriority(75), HookPriorityLate)
	assert.Equal(t, HookPriority(100), HookPriorityLast)
}

// =============================================================================
// NewPromptHook Tests
// =============================================================================

func TestNewPromptHook(t *testing.T) {
	handler := func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		return &HookResult{Modified: true}, nil
	}

	hook := NewPromptHook("test-hook", HookPhasePrePrompt, 50, handler)

	require.NotNil(t, hook)
	assert.Equal(t, "test-hook", hook.Name())
	assert.Equal(t, HookPhasePrePrompt, hook.Phase())
	assert.Equal(t, 50, hook.Priority())
	assert.True(t, hook.Enabled())
}

func TestNewPromptHookWithNilHandler(t *testing.T) {
	hook := NewPromptHook("nil-handler-hook", HookPhasePostPrompt, 75, nil)

	require.NotNil(t, hook)
	assert.Equal(t, "nil-handler-hook", hook.Name())
	assert.Equal(t, HookPhasePostPrompt, hook.Phase())
	assert.Equal(t, 75, hook.Priority())
	assert.True(t, hook.Enabled())
}

func TestNewPromptHookAllPhases(t *testing.T) {
	phases := []HookPhase{
		HookPhasePrePrompt,
		HookPhasePostPrompt,
		HookPhasePreTool,
		HookPhasePostTool,
	}

	for _, phase := range phases {
		t.Run(phase.String(), func(t *testing.T) {
			hook := NewPromptHook("phase-test", phase, int(HookPriorityNormal), nil)
			assert.Equal(t, phase, hook.Phase())
		})
	}
}

// =============================================================================
// NewToolCallHook Tests
// =============================================================================

func TestNewToolCallHook(t *testing.T) {
	handler := func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		return &HookResult{Modified: true}, nil
	}

	hook := NewToolCallHook("tool-hook", HookPhasePreTool, 25, handler)

	require.NotNil(t, hook)
	assert.Equal(t, "tool-hook", hook.Name())
	assert.Equal(t, HookPhasePreTool, hook.Phase())
	assert.Equal(t, 25, hook.Priority())
	assert.True(t, hook.Enabled())
}

func TestNewToolCallHookWithNilHandler(t *testing.T) {
	hook := NewToolCallHook("nil-handler-tool", HookPhasePostTool, 100, nil)

	require.NotNil(t, hook)
	assert.Equal(t, "nil-handler-tool", hook.Name())
	assert.Equal(t, HookPhasePostTool, hook.Phase())
	assert.Equal(t, 100, hook.Priority())
	assert.True(t, hook.Enabled())
}

// =============================================================================
// Hook Interface Implementation Tests
// =============================================================================

func TestPromptHookImplementsHookInterface(t *testing.T) {
	hook := NewPromptHook("interface-test", HookPhasePrePrompt, 50, nil)

	// Verify PromptHook implements Hook interface
	var _ Hook = hook

	assert.Equal(t, "interface-test", hook.Name())
	assert.Equal(t, HookPhasePrePrompt, hook.Phase())
	assert.Equal(t, 50, hook.Priority())
	assert.True(t, hook.Enabled())
}

func TestToolCallHookImplementsHookInterface(t *testing.T) {
	hook := NewToolCallHook("interface-test", HookPhasePreTool, 75, nil)

	// Verify ToolCallHook implements Hook interface
	var _ Hook = hook

	assert.Equal(t, "interface-test", hook.Name())
	assert.Equal(t, HookPhasePreTool, hook.Phase())
	assert.Equal(t, 75, hook.Priority())
	assert.True(t, hook.Enabled())
}

func TestPromptHookSetEnabled(t *testing.T) {
	hook := NewPromptHook("enable-test", HookPhasePrePrompt, 50, nil)

	assert.True(t, hook.Enabled())

	hook.SetEnabled(false)
	assert.False(t, hook.Enabled())

	hook.SetEnabled(true)
	assert.True(t, hook.Enabled())
}

func TestToolCallHookSetEnabled(t *testing.T) {
	hook := NewToolCallHook("enable-test", HookPhasePreTool, 50, nil)

	assert.True(t, hook.Enabled())

	hook.SetEnabled(false)
	assert.False(t, hook.Enabled())

	hook.SetEnabled(true)
	assert.True(t, hook.Enabled())
}

// =============================================================================
// PromptHook.Execute() Tests
// =============================================================================

func TestPromptHookExecuteHappyPath(t *testing.T) {
	var executedWith *PromptHookData

	handler := func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		executedWith = data
		return &HookResult{
			Modified:        true,
			ModifiedContent: "modified query",
		}, nil
	}

	hook := NewPromptHook("execute-test", HookPhasePrePrompt, 50, handler)

	data := &PromptHookData{
		SessionID:  "session-123",
		AgentID:    "agent-456",
		AgentType:  "engineer",
		TurnNumber: 5,
		Query:      "test query",
		Timestamp:  time.Now(),
	}

	result, err := hook.Execute(context.Background(), data)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Modified)
	assert.Equal(t, "modified query", result.ModifiedContent)
	assert.Equal(t, data, executedWith)
}

func TestPromptHookExecuteWithNilHandler(t *testing.T) {
	hook := NewPromptHook("nil-handler", HookPhasePrePrompt, 50, nil)

	data := &PromptHookData{
		SessionID: "session-123",
		Query:     "test query",
	}

	result, err := hook.Execute(context.Background(), data)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Modified)
	assert.False(t, result.ShouldAbort)
}

func TestPromptHookExecuteWithNilContext(t *testing.T) {
	handler := func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		// Handler receives nil context - should still work
		return &HookResult{Modified: true}, nil
	}

	hook := NewPromptHook("nil-ctx", HookPhasePrePrompt, 50, handler)

	data := &PromptHookData{Query: "test"}

	//nolint:staticcheck // testing nil context deliberately
	result, err := hook.Execute(nil, data)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Modified)
}

func TestPromptHookExecuteWithNilData(t *testing.T) {
	var receivedData *PromptHookData

	handler := func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		receivedData = data
		return &HookResult{}, nil
	}

	hook := NewPromptHook("nil-data", HookPhasePrePrompt, 50, handler)

	result, err := hook.Execute(context.Background(), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, receivedData)
}

func TestPromptHookExecuteReturnsError(t *testing.T) {
	expectedErr := errors.New("handler error")

	handler := func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		return nil, expectedErr
	}

	hook := NewPromptHook("error-test", HookPhasePrePrompt, 50, handler)

	result, err := hook.Execute(context.Background(), &PromptHookData{})

	assert.ErrorIs(t, err, expectedErr)
	assert.Nil(t, result)
}

func TestPromptHookExecuteWithContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	handler := func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &HookResult{Modified: true}, nil
	}

	hook := NewPromptHook("cancel-test", HookPhasePrePrompt, 50, handler)

	result, err := hook.Execute(ctx, &PromptHookData{})

	assert.Error(t, err)
	assert.Nil(t, result)
}

// =============================================================================
// ToolCallHook.Execute() Tests
// =============================================================================

func TestToolCallHookExecuteHappyPath(t *testing.T) {
	var executedWith *ToolCallHookData

	handler := func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		executedWith = data
		return &HookResult{
			Modified:        true,
			ModifiedContent: map[string]any{"modified": true},
		}, nil
	}

	hook := NewToolCallHook("execute-test", HookPhasePreTool, 50, handler)

	data := &ToolCallHookData{
		SessionID:  "session-123",
		AgentID:    "agent-456",
		AgentType:  "librarian",
		TurnNumber: 3,
		ToolName:   "read_file",
		ToolInput:  map[string]any{"path": "/test/file.go"},
		Timestamp:  time.Now(),
	}

	result, err := hook.Execute(context.Background(), data)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Modified)
	assert.Equal(t, data, executedWith)
}

func TestToolCallHookExecuteWithNilHandler(t *testing.T) {
	hook := NewToolCallHook("nil-handler", HookPhasePreTool, 50, nil)

	data := &ToolCallHookData{
		SessionID: "session-123",
		ToolName:  "write_file",
	}

	result, err := hook.Execute(context.Background(), data)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Modified)
	assert.False(t, result.ShouldAbort)
}

func TestToolCallHookExecuteWithNilContext(t *testing.T) {
	handler := func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		return &HookResult{Modified: true}, nil
	}

	hook := NewToolCallHook("nil-ctx", HookPhasePreTool, 50, handler)

	data := &ToolCallHookData{ToolName: "test"}

	//nolint:staticcheck // testing nil context deliberately
	result, err := hook.Execute(nil, data)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Modified)
}

func TestToolCallHookExecuteWithNilData(t *testing.T) {
	var receivedData *ToolCallHookData

	handler := func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		receivedData = data
		return &HookResult{}, nil
	}

	hook := NewToolCallHook("nil-data", HookPhasePreTool, 50, handler)

	result, err := hook.Execute(context.Background(), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, receivedData)
}

func TestToolCallHookExecuteReturnsError(t *testing.T) {
	expectedErr := errors.New("tool handler error")

	handler := func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		return nil, expectedErr
	}

	hook := NewToolCallHook("error-test", HookPhasePreTool, 50, handler)

	result, err := hook.Execute(context.Background(), &ToolCallHookData{})

	assert.ErrorIs(t, err, expectedErr)
	assert.Nil(t, result)
}

func TestToolCallHookExecuteWithContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handler := func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &HookResult{Modified: true}, nil
	}

	hook := NewToolCallHook("cancel-test", HookPhasePreTool, 50, handler)

	result, err := hook.Execute(ctx, &ToolCallHookData{})

	assert.Error(t, err)
	assert.Nil(t, result)
}

// =============================================================================
// HookResult Tests
// =============================================================================

func TestHookResultModified(t *testing.T) {
	tests := []struct {
		name     string
		result   HookResult
		modified bool
	}{
		{
			name:     "not modified",
			result:   HookResult{Modified: false},
			modified: false,
		},
		{
			name:     "modified with content",
			result:   HookResult{Modified: true, ModifiedContent: "new content"},
			modified: true,
		},
		{
			name:     "modified without content",
			result:   HookResult{Modified: true, ModifiedContent: nil},
			modified: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.modified, tt.result.Modified)
		})
	}
}

func TestHookResultShouldAbort(t *testing.T) {
	tests := []struct {
		name        string
		result      HookResult
		shouldAbort bool
		abortReason string
	}{
		{
			name:        "no abort",
			result:      HookResult{ShouldAbort: false},
			shouldAbort: false,
			abortReason: "",
		},
		{
			name:        "abort with reason",
			result:      HookResult{ShouldAbort: true, AbortReason: "validation failed"},
			shouldAbort: true,
			abortReason: "validation failed",
		},
		{
			name:        "abort without reason",
			result:      HookResult{ShouldAbort: true},
			shouldAbort: true,
			abortReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.shouldAbort, tt.result.ShouldAbort)
			assert.Equal(t, tt.abortReason, tt.result.AbortReason)
		})
	}
}

func TestHookResultWithError(t *testing.T) {
	expectedErr := errors.New("hook execution failed")

	result := HookResult{
		Modified: false,
		Error:    expectedErr,
	}

	assert.ErrorIs(t, result.Error, expectedErr)
	assert.False(t, result.Modified)
	assert.False(t, result.ShouldAbort)
}

func TestHookResultCombinedFlags(t *testing.T) {
	result := HookResult{
		Modified:        true,
		ModifiedContent: "updated content",
		ShouldAbort:     true,
		AbortReason:     "critical error",
		Error:           errors.New("underlying error"),
	}

	assert.True(t, result.Modified)
	assert.Equal(t, "updated content", result.ModifiedContent)
	assert.True(t, result.ShouldAbort)
	assert.Equal(t, "critical error", result.AbortReason)
	assert.Error(t, result.Error)
}

// =============================================================================
// PromptHookData Tests
// =============================================================================

func TestPromptHookDataFields(t *testing.T) {
	now := time.Now()

	augQuery := &AugmentedQuery{
		OriginalQuery: "original",
		TokensUsed:    500,
		BudgetMax:     1000,
	}

	data := PromptHookData{
		SessionID:           "session-abc",
		AgentID:             "agent-xyz",
		AgentType:           "engineer",
		TurnNumber:          10,
		ContextTokens:       8000,
		ContextUsagePercent: 0.75,
		Query:               "how to implement X",
		PrefetchedContent:   augQuery,
		PressureLevel:       2,
		Timestamp:           now,
	}

	assert.Equal(t, "session-abc", data.SessionID)
	assert.Equal(t, "agent-xyz", data.AgentID)
	assert.Equal(t, "engineer", data.AgentType)
	assert.Equal(t, 10, data.TurnNumber)
	assert.Equal(t, 8000, data.ContextTokens)
	assert.Equal(t, 0.75, data.ContextUsagePercent)
	assert.Equal(t, "how to implement X", data.Query)
	assert.Equal(t, augQuery, data.PrefetchedContent)
	assert.Equal(t, 2, data.PressureLevel)
	assert.Equal(t, now, data.Timestamp)
}

func TestPromptHookDataEmpty(t *testing.T) {
	data := PromptHookData{}

	assert.Empty(t, data.SessionID)
	assert.Empty(t, data.AgentID)
	assert.Empty(t, data.AgentType)
	assert.Zero(t, data.TurnNumber)
	assert.Zero(t, data.ContextTokens)
	assert.Zero(t, data.ContextUsagePercent)
	assert.Empty(t, data.Query)
	assert.Nil(t, data.PrefetchedContent)
	assert.Zero(t, data.PressureLevel)
	assert.True(t, data.Timestamp.IsZero())
}

// =============================================================================
// ToolCallHookData Tests
// =============================================================================

func TestToolCallHookDataFields(t *testing.T) {
	now := time.Now()
	toolErr := errors.New("tool execution error")

	data := ToolCallHookData{
		SessionID:  "session-def",
		AgentID:    "agent-123",
		AgentType:  "librarian",
		TurnNumber: 7,
		ToolName:   "search_code",
		ToolInput:  map[string]any{"query": "function definition", "limit": 10},
		ToolOutput: []string{"result1", "result2"},
		Duration:   150 * time.Millisecond,
		Error:      toolErr,
		Timestamp:  now,
	}

	assert.Equal(t, "session-def", data.SessionID)
	assert.Equal(t, "agent-123", data.AgentID)
	assert.Equal(t, "librarian", data.AgentType)
	assert.Equal(t, 7, data.TurnNumber)
	assert.Equal(t, "search_code", data.ToolName)
	assert.Equal(t, "function definition", data.ToolInput["query"])
	assert.Equal(t, 10, data.ToolInput["limit"])
	assert.Equal(t, []string{"result1", "result2"}, data.ToolOutput)
	assert.Equal(t, 150*time.Millisecond, data.Duration)
	assert.ErrorIs(t, data.Error, toolErr)
	assert.Equal(t, now, data.Timestamp)
}

func TestToolCallHookDataEmpty(t *testing.T) {
	data := ToolCallHookData{}

	assert.Empty(t, data.SessionID)
	assert.Empty(t, data.AgentID)
	assert.Empty(t, data.AgentType)
	assert.Zero(t, data.TurnNumber)
	assert.Empty(t, data.ToolName)
	assert.Nil(t, data.ToolInput)
	assert.Nil(t, data.ToolOutput)
	assert.Zero(t, data.Duration)
	assert.NoError(t, data.Error)
	assert.True(t, data.Timestamp.IsZero())
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestPromptHookPipelineExecution(t *testing.T) {
	// Simulate a pipeline with multiple hooks modifying data
	var executionOrder []string

	hook1 := NewPromptHook("hook1", HookPhasePrePrompt, int(HookPriorityFirst), func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		executionOrder = append(executionOrder, "hook1")
		data.Query = data.Query + " [hook1]"
		return &HookResult{Modified: true, ModifiedContent: data.Query}, nil
	})

	hook2 := NewPromptHook("hook2", HookPhasePrePrompt, int(HookPriorityNormal), func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		executionOrder = append(executionOrder, "hook2")
		data.Query = data.Query + " [hook2]"
		return &HookResult{Modified: true, ModifiedContent: data.Query}, nil
	})

	hook3 := NewPromptHook("hook3", HookPhasePrePrompt, int(HookPriorityLast), func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		executionOrder = append(executionOrder, "hook3")
		return &HookResult{Modified: false}, nil
	})

	data := &PromptHookData{Query: "initial"}

	// Execute hooks in priority order
	hooks := []*PromptHook{hook1, hook2, hook3}
	for _, h := range hooks {
		if h.Enabled() {
			_, err := h.Execute(context.Background(), data)
			require.NoError(t, err)
		}
	}

	assert.Equal(t, []string{"hook1", "hook2", "hook3"}, executionOrder)
	assert.Equal(t, "initial [hook1] [hook2]", data.Query)
}

func TestToolCallHookWithAbort(t *testing.T) {
	hook := NewToolCallHook("abort-hook", HookPhasePreTool, 50, func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		if data.ToolName == "dangerous_tool" {
			return &HookResult{
				ShouldAbort: true,
				AbortReason: "tool is blocked",
			}, nil
		}
		return &HookResult{}, nil
	})

	// Test with dangerous tool
	dangerousData := &ToolCallHookData{ToolName: "dangerous_tool"}
	result, err := hook.Execute(context.Background(), dangerousData)

	require.NoError(t, err)
	assert.True(t, result.ShouldAbort)
	assert.Equal(t, "tool is blocked", result.AbortReason)

	// Test with safe tool
	safeData := &ToolCallHookData{ToolName: "read_file"}
	result, err = hook.Execute(context.Background(), safeData)

	require.NoError(t, err)
	assert.False(t, result.ShouldAbort)
}

func TestHookWithPrefetchedContent(t *testing.T) {
	prefetched := &AugmentedQuery{
		OriginalQuery: "test query",
		Excerpts: []Excerpt{
			{ID: "e1", Content: "code snippet", Source: "file.go", Confidence: 0.9},
		},
		Summaries: []Summary{
			{ID: "s1", Text: "Function does X", Source: "util.go", Confidence: 0.7},
		},
		TokensUsed: 250,
		BudgetMax:  500,
	}

	hook := NewPromptHook("prefetch-inject", HookPhasePrePrompt, int(HookPriorityFirst), func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		if data.PrefetchedContent != nil {
			// Inject prefetched content into the query
			return &HookResult{
				Modified:        true,
				ModifiedContent: data.Query + "\n\nContext:\n" + data.PrefetchedContent.Excerpts[0].Content,
			}, nil
		}
		return &HookResult{}, nil
	})

	data := &PromptHookData{
		Query:             "how to implement X",
		PrefetchedContent: prefetched,
	}

	result, err := hook.Execute(context.Background(), data)

	require.NoError(t, err)
	assert.True(t, result.Modified)
	assert.Contains(t, result.ModifiedContent.(string), "code snippet")
}

func TestDisabledHookSkipped(t *testing.T) {
	executed := false

	hook := NewPromptHook("disabled-hook", HookPhasePrePrompt, 50, func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		executed = true
		return &HookResult{Modified: true}, nil
	})

	hook.SetEnabled(false)

	// Simulate pipeline that checks Enabled()
	if hook.Enabled() {
		_, _ = hook.Execute(context.Background(), &PromptHookData{})
	}

	assert.False(t, executed)
	assert.False(t, hook.Enabled())
}

func TestHookPhaseValidation(t *testing.T) {
	// Test that hooks can be created with any phase
	promptHook := NewPromptHook("prompt-hook", HookPhasePrePrompt, 50, nil)
	toolHook := NewToolCallHook("tool-hook", HookPhasePreTool, 50, nil)

	assert.Equal(t, HookPhasePrePrompt, promptHook.Phase())
	assert.Equal(t, HookPhasePreTool, toolHook.Phase())

	// While unusual, hooks can be assigned to any phase
	mixedPromptHook := NewPromptHook("mixed-prompt", HookPhasePostTool, 50, nil)
	mixedToolHook := NewToolCallHook("mixed-tool", HookPhasePostPrompt, 50, nil)

	assert.Equal(t, HookPhasePostTool, mixedPromptHook.Phase())
	assert.Equal(t, HookPhasePostPrompt, mixedToolHook.Phase())
}

func TestToolCallHookDataWithNilToolInput(t *testing.T) {
	data := ToolCallHookData{
		ToolName:  "test_tool",
		ToolInput: nil,
	}

	hook := NewToolCallHook("nil-input", HookPhasePreTool, 50, func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		if data.ToolInput == nil {
			data.ToolInput = make(map[string]any)
		}
		data.ToolInput["injected"] = true
		return &HookResult{Modified: true, ModifiedContent: data.ToolInput}, nil
	})

	result, err := hook.Execute(context.Background(), &data)

	require.NoError(t, err)
	assert.True(t, result.Modified)
	assert.Equal(t, true, data.ToolInput["injected"])
}

func TestPromptHookDataWithZeroPressure(t *testing.T) {
	data := PromptHookData{
		PressureLevel: 0, // Normal pressure
	}

	hook := NewPromptHook("pressure-check", HookPhasePrePrompt, 50, func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		if data.PressureLevel >= 3 {
			return &HookResult{
				ShouldAbort: true,
				AbortReason: "critical pressure",
			}, nil
		}
		return &HookResult{}, nil
	})

	result, err := hook.Execute(context.Background(), &data)

	require.NoError(t, err)
	assert.False(t, result.ShouldAbort)
}

func TestPromptHookDataWithCriticalPressure(t *testing.T) {
	data := PromptHookData{
		PressureLevel: 3, // Critical pressure
	}

	hook := NewPromptHook("pressure-check", HookPhasePrePrompt, 50, func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		if data.PressureLevel >= 3 {
			return &HookResult{
				ShouldAbort: true,
				AbortReason: "critical pressure",
			}, nil
		}
		return &HookResult{}, nil
	})

	result, err := hook.Execute(context.Background(), &data)

	require.NoError(t, err)
	assert.True(t, result.ShouldAbort)
	assert.Equal(t, "critical pressure", result.AbortReason)
}
