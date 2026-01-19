package hooks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Test Helpers
// =============================================================================

// MockWorkflowProvider implements WorkflowContextProvider for testing.
type MockWorkflowProvider struct {
	mu              sync.Mutex
	workflowContext *WorkflowContext
	workflowErr     error
	calls           []workflowCall
}

type workflowCall struct {
	sessionID string
}

func NewMockWorkflowProvider() *MockWorkflowProvider {
	return &MockWorkflowProvider{
		calls: make([]workflowCall, 0),
	}
}

func (m *MockWorkflowProvider) GetWorkflowContext(
	ctx context.Context,
	sessionID string,
) (*WorkflowContext, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, workflowCall{sessionID: sessionID})

	if m.workflowErr != nil {
		return nil, m.workflowErr
	}

	return m.workflowContext, nil
}

func (m *MockWorkflowProvider) SetWorkflowContext(wfCtx *WorkflowContext) {
	m.mu.Lock()
	m.workflowContext = wfCtx
	m.mu.Unlock()
}

func (m *MockWorkflowProvider) SetError(err error) {
	m.mu.Lock()
	m.workflowErr = err
	m.mu.Unlock()
}

func (m *MockWorkflowProvider) GetCalls() []workflowCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func createWorkflowTestPromptData(agentType string) *ctxpkg.PromptHookData {
	return &ctxpkg.PromptHookData{
		SessionID:     "test-session",
		AgentID:       "test-agent",
		AgentType:     agentType,
		TurnNumber:    1,
		Query:         "test query",
		Timestamp:     time.Now(),
	}
}

func createTestWorkflowState() *WorkflowState {
	return &WorkflowState{
		ID:        "wf-123",
		Name:      "Test Workflow",
		Phase:     "executing",
		Progress:  0.5,
		StartTime: time.Now().Add(-10 * time.Minute),
	}
}

func createTestTask(id, desc, status, assignedTo string) OrchestratorTask {
	return OrchestratorTask{
		ID:          id,
		Description: desc,
		AssignedTo:  assignedTo,
		Status:      status,
	}
}

func createTestPipeline(id, status string, agents []string) PipelineInfo {
	return PipelineInfo{
		ID:           id,
		Status:       status,
		ActiveAgents: agents,
	}
}

func createTestAgentState(id, agentType string, usage float64, task string) AgentStateSnapshot {
	return AgentStateSnapshot{
		AgentID:      id,
		AgentType:    agentType,
		ContextUsage: usage,
		LastActivity: time.Now(),
		CurrentTask:  task,
	}
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewWorkflowContextHook_DefaultConfig(t *testing.T) {
	hook := NewWorkflowContextHook(WorkflowContextHookConfig{})

	if hook.maxTasks != DefaultMaxTasks {
		t.Errorf("maxTasks = %d, want %d", hook.maxTasks, DefaultMaxTasks)
	}

	if hook.maxPipelines != DefaultMaxPipelines {
		t.Errorf("maxPipelines = %d, want %d", hook.maxPipelines, DefaultMaxPipelines)
	}

	if !hook.enabled {
		t.Error("Hook should be enabled by default")
	}
}

func TestNewWorkflowContextHook_CustomConfig(t *testing.T) {
	provider := NewMockWorkflowProvider()
	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
		MaxTasks:         15,
		MaxPipelines:     8,
	})

	if hook.workflowProvider != provider {
		t.Error("WorkflowProvider not set correctly")
	}

	if hook.maxTasks != 15 {
		t.Errorf("maxTasks = %d, want 15", hook.maxTasks)
	}

	if hook.maxPipelines != 8 {
		t.Errorf("maxPipelines = %d, want 8", hook.maxPipelines)
	}
}

// =============================================================================
// Hook Interface Tests
// =============================================================================

func TestWorkflowContextHook_Name(t *testing.T) {
	hook := NewWorkflowContextHook(WorkflowContextHookConfig{})

	if hook.Name() != WorkflowContextHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), WorkflowContextHookName)
	}
}

func TestWorkflowContextHook_Phase(t *testing.T) {
	hook := NewWorkflowContextHook(WorkflowContextHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("Phase() = %v, want HookPhasePrePrompt", hook.Phase())
	}
}

func TestWorkflowContextHook_Priority(t *testing.T) {
	hook := NewWorkflowContextHook(WorkflowContextHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityNormal) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityNormal)
	}
}

func TestWorkflowContextHook_Enabled(t *testing.T) {
	hook := NewWorkflowContextHook(WorkflowContextHookConfig{})

	if !hook.Enabled() {
		t.Error("Hook should be enabled by default")
	}

	hook.SetEnabled(false)

	if hook.Enabled() {
		t.Error("Hook should be disabled after SetEnabled(false)")
	}
}

// =============================================================================
// Execute Tests - Agent Type Filtering
// =============================================================================

func TestWorkflowContextHook_OnlyAppliesToOrchestrator(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: createTestWorkflowState(),
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("Should modify data for Orchestrator agent")
	}

	if data.PrefetchedContent == nil {
		t.Error("PrefetchedContent should be set")
	}
}

func TestWorkflowContextHook_SkipsNonOrchestratorAgents(t *testing.T) {
	nonOrchestratorAgents := []string{"librarian", "engineer", "guide", "architect"}

	for _, agent := range nonOrchestratorAgents {
		t.Run(agent, func(t *testing.T) {
			provider := NewMockWorkflowProvider()
			provider.SetWorkflowContext(&WorkflowContext{
				CurrentWorkflow: createTestWorkflowState(),
			})

			hook := NewWorkflowContextHook(WorkflowContextHookConfig{
				WorkflowProvider: provider,
			})

			ctx := context.Background()
			data := createWorkflowTestPromptData(agent)

			result, err := hook.Execute(ctx, data)

			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}

			if result.Modified {
				t.Errorf("Should not modify data for %s agent", agent)
			}
		})
	}
}

func TestWorkflowContextHook_HandlesOrchestratorCaseInsensitive(t *testing.T) {
	variants := []string{"orchestrator", "Orchestrator", "ORCHESTRATOR", "OrChEsTrAtOr"}

	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			provider := NewMockWorkflowProvider()
			provider.SetWorkflowContext(&WorkflowContext{
				CurrentWorkflow: createTestWorkflowState(),
			})

			hook := NewWorkflowContextHook(WorkflowContextHookConfig{
				WorkflowProvider: provider,
			})

			ctx := context.Background()
			data := createWorkflowTestPromptData(variant)

			result, err := hook.Execute(ctx, data)

			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}

			if !result.Modified {
				t.Errorf("Should modify data for %s agent", variant)
			}
		})
	}
}

// =============================================================================
// Execute Tests - Workflow Context Injection
// =============================================================================

func TestWorkflowContextHook_InjectsWorkflowContext(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: createTestWorkflowState(),
		PendingTasks: []OrchestratorTask{
			createTestTask("task-1", "Implement feature", "pending", "engineer"),
		},
		ActivePipelines: []PipelineInfo{
			createTestPipeline("pipeline-1", "executing", []string{"engineer", "tester"}),
		},
		AgentStates: map[string]AgentStateSnapshot{
			"agent-1": createTestAgentState("agent-1", "engineer", 0.45, "Implementing feature"),
		},
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("Should modify data")
	}

	if data.PrefetchedContent == nil {
		t.Fatal("PrefetchedContent should be set")
	}

	if len(data.PrefetchedContent.Excerpts) == 0 {
		t.Fatal("Should have at least one excerpt")
	}

	content := data.PrefetchedContent.Excerpts[0].Content

	// Verify content structure
	expectedParts := []string{
		"[WORKFLOW_CONTEXT]",
		"### Current Workflow",
		"Test Workflow",
		"executing",
		"50%", // Progress
		"### Pending Tasks",
		"task-1",
		"### Active Pipelines",
		"pipeline-1",
		"### Agent States",
		"agent-1",
	}

	for _, part := range expectedParts {
		if !containsString(content, part) {
			t.Errorf("Content missing expected part: %s", part)
		}
	}
}

func TestWorkflowContextHook_InjectsOnlyWorkflowState(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: createTestWorkflowState(),
		// No tasks, pipelines, or agent states
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("Should modify data with workflow state")
	}

	content := data.PrefetchedContent.Excerpts[0].Content
	if !containsString(content, "Test Workflow") {
		t.Error("Should contain workflow name")
	}
}

func TestWorkflowContextHook_InjectsOnlyTasks(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		PendingTasks: []OrchestratorTask{
			createTestTask("task-1", "First task", "pending", ""),
		},
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("Should modify data with tasks")
	}

	content := data.PrefetchedContent.Excerpts[0].Content
	if !containsString(content, "task-1") {
		t.Error("Should contain task ID")
	}
}

// =============================================================================
// Execute Tests - Edge Cases
// =============================================================================

func TestWorkflowContextHook_SkipsWhenDisabled(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: createTestWorkflowState(),
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}
}

func TestWorkflowContextHook_SkipsNilProvider(t *testing.T) {
	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: nil,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil provider")
	}
}

func TestWorkflowContextHook_SkipsEmptyContext(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		// Empty context
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with empty workflow context")
	}
}

func TestWorkflowContextHook_SkipsNilContext(t *testing.T) {
	provider := NewMockWorkflowProvider()
	// No context set (nil)

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil workflow context")
	}
}

func TestWorkflowContextHook_HandlesProviderError(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetError(errors.New("workflow query failed"))

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := hook.Execute(ctx, data)

	// Hook should not return error to caller
	if err != nil {
		t.Fatalf("Execute should not return error, got %v", err)
	}

	// But result should contain the error
	if result.Error == nil {
		t.Error("Result.Error should contain the provider error")
	}

	if result.Modified {
		t.Error("Should not modify data on error")
	}
}

func TestWorkflowContextHook_PreservesExistingContent(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: createTestWorkflowState(),
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	// Pre-populate with existing content
	data.PrefetchedContent = &ctxpkg.AugmentedQuery{
		OriginalQuery: "test query",
		Excerpts: []ctxpkg.Excerpt{
			{Source: "existing", Content: "existing content", Confidence: 0.8},
		},
	}

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// Should have both workflow and existing excerpts
	if len(data.PrefetchedContent.Excerpts) < 2 {
		t.Errorf("Expected at least 2 excerpts, got %d", len(data.PrefetchedContent.Excerpts))
	}

	// Workflow should be first (prepended)
	if data.PrefetchedContent.Excerpts[0].Source != "workflow_context" {
		t.Error("Workflow context should be prepended as first excerpt")
	}

	// Existing content should still be present
	found := false
	for _, ex := range data.PrefetchedContent.Excerpts {
		if ex.Source == "existing" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Existing content should be preserved")
	}
}

func TestWorkflowContextHook_RespectsMaxTasks(t *testing.T) {
	provider := NewMockWorkflowProvider()

	// Create more tasks than maxTasks
	tasks := make([]OrchestratorTask, 15)
	for i := 0; i < 15; i++ {
		tasks[i] = createTestTask(
			fmt.Sprintf("task-%d", i),
			fmt.Sprintf("Task %d description", i),
			"pending",
			"",
		)
	}

	provider.SetWorkflowContext(&WorkflowContext{
		PendingTasks: tasks,
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
		MaxTasks:         5,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	content := data.PrefetchedContent.Excerpts[0].Content

	// Should mention there are more tasks
	if !containsString(content, "and 10 more tasks") {
		t.Error("Should indicate there are more tasks")
	}
}

func TestWorkflowContextHook_RespectsMaxPipelines(t *testing.T) {
	provider := NewMockWorkflowProvider()

	// Create more pipelines than maxPipelines
	pipelines := make([]PipelineInfo, 10)
	for i := 0; i < 10; i++ {
		pipelines[i] = createTestPipeline(
			fmt.Sprintf("pipeline-%d", i),
			"executing",
			[]string{"engineer"},
		)
	}

	provider.SetWorkflowContext(&WorkflowContext{
		ActivePipelines: pipelines,
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
		MaxPipelines:     3,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	content := data.PrefetchedContent.Excerpts[0].Content

	// Should mention there are more pipelines
	if !containsString(content, "and 7 more pipelines") {
		t.Error("Should indicate there are more pipelines")
	}
}

// =============================================================================
// ToPromptHook Tests
// =============================================================================

func TestWorkflowContextHook_ToPromptHook_ReturnsValidHook(t *testing.T) {
	provider := NewMockWorkflowProvider()
	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != WorkflowContextHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), WorkflowContextHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePrePrompt", promptHook.Phase())
	}

	if promptHook.Priority() != int(ctxpkg.HookPriorityNormal) {
		t.Errorf("PromptHook Priority = %d, want %d", promptHook.Priority(), ctxpkg.HookPriorityNormal)
	}
}

func TestWorkflowContextHook_ToPromptHook_ExecutesCorrectly(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: createTestWorkflowState(),
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	promptHook := hook.ToPromptHook()

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	result, err := promptHook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("PromptHook Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("PromptHook should modify data")
	}

	if data.PrefetchedContent == nil {
		t.Error("PromptHook should set PrefetchedContent")
	}
}

// =============================================================================
// Content Format Tests
// =============================================================================

func TestWorkflowContextHook_ContentFormat(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: &WorkflowState{
			ID:        "wf-456",
			Name:      "Feature Implementation",
			Phase:     "validating",
			Progress:  0.75,
			StartTime: time.Now().Add(-30 * time.Minute),
		},
		PendingTasks: []OrchestratorTask{
			{
				ID:          "task-abc",
				Description: "Run integration tests",
				Status:      "in_progress",
				AssignedTo:  "tester",
			},
		},
		ActivePipelines: []PipelineInfo{
			{
				ID:           "pipe-xyz",
				Status:       "testing",
				ActiveAgents: []string{"tester", "inspector"},
			},
		},
		AgentStates: map[string]AgentStateSnapshot{
			"agent-tester": {
				AgentID:      "agent-tester",
				AgentType:    "tester",
				ContextUsage: 0.65,
				CurrentTask:  "Running tests",
			},
		},
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	_, _ = hook.Execute(ctx, data)

	content := data.PrefetchedContent.Excerpts[0].Content

	// Verify format elements
	expectedParts := []string{
		"[WORKFLOW_CONTEXT]",
		"Current workflow state for coordination",
		"**Name**: Feature Implementation (ID: wf-456)",
		"**Phase**: validating",
		"**Progress**: 75%",
		"**Duration**:",
		"Run integration tests",
		"[in_progress]",
		"→ tester",
		"**pipe-xyz**: testing",
		"agents: tester, inspector",
		"65% context",
		"working on: Running tests",
		"Use this context to coordinate",
	}

	for _, part := range expectedParts {
		if !containsString(content, part) {
			t.Errorf("Content missing expected part: %s", part)
		}
	}
}

func TestWorkflowContextHook_ContentWithoutStartTime(t *testing.T) {
	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: &WorkflowState{
			ID:       "wf-123",
			Name:     "Test",
			Phase:    "planning",
			Progress: 0.1,
			// No StartTime
		},
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	ctx := context.Background()
	data := createWorkflowTestPromptData("orchestrator")

	_, _ = hook.Execute(ctx, data)

	content := data.PrefetchedContent.Excerpts[0].Content

	// Should not show duration when StartTime is zero
	if containsString(content, "**Duration**:") {
		t.Error("Should not show duration when StartTime is zero")
	}
}

