// Package skills provides tests for AR.8.6: Orchestrator Retrieval Skills.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Mock Types
// =============================================================================

type mockWorkflowStateProvider struct {
	state *WorkflowState
	err   error
}

func (m *mockWorkflowStateProvider) GetWorkflowState(
	_ context.Context,
	_ string,
	_ *WorkflowStateOptions,
) (*WorkflowState, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.state, nil
}

type mockAgentCapabilityProvider struct {
	caps *AgentCapabilities
	err  error
}

func (m *mockAgentCapabilityProvider) GetAgentCapabilities(
	_ context.Context,
	_ string,
	_ *CapabilityOptions,
) (*AgentCapabilities, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.caps, nil
}

type mockCoordinationContextProvider struct {
	context *CoordinationContext
	err     error
}

func (m *mockCoordinationContextProvider) GetCoordinationContext(
	_ context.Context,
	_ string,
	_ *CoordinationOptions,
) (*CoordinationContext, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.context, nil
}

type mockOrchestratorContentStore struct {
	entries []*ctxpkg.ContentEntry
	err     error
}

func (m *mockOrchestratorContentStore) Search(
	_ string,
	_ *ctxpkg.SearchFilters,
	_ int,
) ([]*ctxpkg.ContentEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}

type mockOrchestratorSearcher struct {
	result *ctxpkg.TieredSearchResult
}

func (m *mockOrchestratorSearcher) SearchWithBudget(
	_ context.Context,
	_ string,
	_ time.Duration,
) *ctxpkg.TieredSearchResult {
	return m.result
}

// =============================================================================
// Test: orchestrator_get_workflow_state Skill
// =============================================================================

func TestNewGetWorkflowStateSkill(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{}
	skill := NewGetWorkflowStateSkill(deps)

	if skill.Name != "orchestrator_get_workflow_state" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
	if skill.Domain != OrchestratorDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}
}

func TestGetWorkflowState_RequiresWorkflowID(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{WorkflowStateProvider: &mockWorkflowStateProvider{}}
	skill := NewGetWorkflowStateSkill(deps)

	input, _ := json.Marshal(GetWorkflowStateInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "workflow_id is required" {
		t.Errorf("expected 'workflow_id is required' error, got: %v", err)
	}
}

func TestGetWorkflowState_NoProvider(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{}
	skill := NewGetWorkflowStateSkill(deps)

	input, _ := json.Marshal(GetWorkflowStateInput{WorkflowID: "wf-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no provider configured")
	}
}

func TestGetWorkflowState_WithProvider(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &OrchestratorDependencies{
		WorkflowStateProvider: &mockWorkflowStateProvider{
			state: &WorkflowState{
				WorkflowID:     "wf-123",
				Status:         "active",
				CurrentPhase:   "implementation",
				ActiveAgents:   []string{"engineer", "tester"},
				CompletedTasks: []string{"task-1"},
				PendingTasks:   []string{"task-2"},
				StartTime:      now.Add(-time.Hour),
				LastUpdate:     now,
			},
		},
	}
	skill := NewGetWorkflowStateSkill(deps)

	input, _ := json.Marshal(GetWorkflowStateInput{
		WorkflowID:     "wf-123",
		IncludeHistory: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetWorkflowStateOutput)
	if !output.Found {
		t.Error("expected workflow to be found")
	}
	if output.State.WorkflowID != "wf-123" {
		t.Errorf("unexpected workflow ID: %s", output.State.WorkflowID)
	}
	if output.State.Status != "active" {
		t.Errorf("unexpected status: %s", output.State.Status)
	}
}

func TestGetWorkflowState_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &OrchestratorDependencies{
		ContentStore: &mockOrchestratorContentStore{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:          "entry-1",
					ContentType: ctxpkg.ContentTypePlanWorkflow,
					Content:     "Implementation phase starting...",
					AgentType:   "engineer",
					Timestamp:   now,
				},
			},
		},
	}
	skill := NewGetWorkflowStateSkill(deps)

	input, _ := json.Marshal(GetWorkflowStateInput{WorkflowID: "wf-123"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetWorkflowStateOutput)
	if !output.Found {
		t.Error("expected workflow to be found")
	}
}

func TestGetWorkflowState_WithSearcher(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &OrchestratorDependencies{
		Searcher: &mockOrchestratorSearcher{
			result: &ctxpkg.TieredSearchResult{
				Results: []*ctxpkg.ContentEntry{
					{
						ID:          "entry-1",
						ContentType: ctxpkg.ContentTypePlanWorkflow,
						Content:     "Workflow state update",
						AgentType:   "orchestrator",
						Timestamp:   now,
					},
				},
			},
		},
	}
	skill := NewGetWorkflowStateSkill(deps)

	input, _ := json.Marshal(GetWorkflowStateInput{WorkflowID: "wf-123"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetWorkflowStateOutput)
	if !output.Found {
		t.Error("expected workflow to be found")
	}
}

func TestGetWorkflowState_NotFound(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{
		ContentStore: &mockOrchestratorContentStore{
			entries: []*ctxpkg.ContentEntry{},
		},
	}
	skill := NewGetWorkflowStateSkill(deps)

	input, _ := json.Marshal(GetWorkflowStateInput{WorkflowID: "nonexistent"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetWorkflowStateOutput)
	if output.Found {
		t.Error("expected workflow not to be found")
	}
}

// =============================================================================
// Test: orchestrator_get_agent_capabilities Skill
// =============================================================================

func TestNewGetAgentCapabilitiesSkill(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{}
	skill := NewGetAgentCapabilitiesSkill(deps)

	if skill.Name != "orchestrator_get_agent_capabilities" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestGetAgentCapabilities_RequiresAgentType(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{AgentCapabilityProvider: &mockAgentCapabilityProvider{}}
	skill := NewGetAgentCapabilitiesSkill(deps)

	input, _ := json.Marshal(GetAgentCapabilitiesInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "agent_type is required" {
		t.Errorf("expected 'agent_type is required' error, got: %v", err)
	}
}

func TestGetAgentCapabilities_WithProvider(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{
		AgentCapabilityProvider: &mockAgentCapabilityProvider{
			caps: &AgentCapabilities{
				AgentType:   "engineer",
				Description: "Code implementation",
				Skills:      []string{"implement", "refactor"},
				Domains:     []string{"code"},
				TokenLimit:  12000,
				CanHandoff:  true,
			},
		},
	}
	skill := NewGetAgentCapabilitiesSkill(deps)

	input, _ := json.Marshal(GetAgentCapabilitiesInput{
		AgentType:     "engineer",
		IncludeSkills: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetAgentCapabilitiesOutput)
	if !output.Found {
		t.Error("expected capabilities to be found")
	}
	if output.Capabilities.AgentType != "engineer" {
		t.Errorf("unexpected agent type: %s", output.Capabilities.AgentType)
	}
}

func TestGetAgentCapabilities_DefaultCapabilities(t *testing.T) {
	t.Parallel()

	// Test that known agent types return default capabilities
	deps := &OrchestratorDependencies{}
	skill := NewGetAgentCapabilitiesSkill(deps)

	agentTypes := []string{"librarian", "archivalist", "academic", "guide", "engineer", "orchestrator"}

	for _, agentType := range agentTypes {
		input, _ := json.Marshal(GetAgentCapabilitiesInput{AgentType: agentType})

		result, err := skill.Handler(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", agentType, err)
		}

		output := result.(*GetAgentCapabilitiesOutput)
		if !output.Found {
			t.Errorf("expected capabilities for %s to be found", agentType)
		}
	}
}

func TestGetAgentCapabilities_UnknownAgent(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{}
	skill := NewGetAgentCapabilitiesSkill(deps)

	input, _ := json.Marshal(GetAgentCapabilitiesInput{AgentType: "unknown_agent"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetAgentCapabilitiesOutput)
	if output.Found {
		t.Error("expected unknown agent not to be found")
	}
}

// =============================================================================
// Test: orchestrator_get_coordination_context Skill
// =============================================================================

func TestNewGetCoordinationContextSkill(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{}
	skill := NewGetCoordinationContextSkill(deps)

	if skill.Name != "orchestrator_get_coordination_context" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestGetCoordinationContext_RequiresWorkflowID(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{CoordinationContextProvider: &mockCoordinationContextProvider{}}
	skill := NewGetCoordinationContextSkill(deps)

	input, _ := json.Marshal(GetCoordinationContextInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "workflow_id is required" {
		t.Errorf("expected 'workflow_id is required' error, got: %v", err)
	}
}

func TestGetCoordinationContext_NoProvider(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{}
	skill := NewGetCoordinationContextSkill(deps)

	input, _ := json.Marshal(GetCoordinationContextInput{WorkflowID: "wf-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no provider configured")
	}
}

func TestGetCoordinationContext_WithProvider(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &OrchestratorDependencies{
		CoordinationContextProvider: &mockCoordinationContextProvider{
			context: &CoordinationContext{
				WorkflowID:       "wf-123",
				CurrentObjective: "Implement feature X",
				AgentStatus: []*AgentStatus{
					{AgentID: "agent-1", AgentType: "engineer", Status: "active", LastActive: now},
				},
				RecentMessages: []*AgentMessage{
					{MessageID: "msg-1", FromAgent: "agent-1", Content: "Working on task", Timestamp: now},
				},
			},
		},
	}
	skill := NewGetCoordinationContextSkill(deps)

	input, _ := json.Marshal(GetCoordinationContextInput{
		WorkflowID:      "wf-123",
		IncludeMessages: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetCoordinationContextOutput)
	if !output.Found {
		t.Error("expected context to be found")
	}
	if output.Context.WorkflowID != "wf-123" {
		t.Errorf("unexpected workflow ID: %s", output.Context.WorkflowID)
	}
}

func TestGetCoordinationContext_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &OrchestratorDependencies{
		ContentStore: &mockOrchestratorContentStore{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:        "entry-1",
					Content:   "Implement feature X\n\nDetails follow...",
					AgentID:   "agent-1",
					AgentType: "engineer",
					Timestamp: now,
				},
			},
		},
	}
	skill := NewGetCoordinationContextSkill(deps)

	input, _ := json.Marshal(GetCoordinationContextInput{
		WorkflowID:      "wf-123",
		IncludeMessages: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetCoordinationContextOutput)
	if !output.Found {
		t.Error("expected context to be found")
	}
}

func TestGetCoordinationContext_WithSearcher(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &OrchestratorDependencies{
		Searcher: &mockOrchestratorSearcher{
			result: &ctxpkg.TieredSearchResult{
				Results: []*ctxpkg.ContentEntry{
					{
						ID:        "entry-1",
						Content:   "Coordination message",
						AgentID:   "agent-1",
						AgentType: "orchestrator",
						Timestamp: now,
					},
				},
			},
		},
	}
	skill := NewGetCoordinationContextSkill(deps)

	input, _ := json.Marshal(GetCoordinationContextInput{WorkflowID: "wf-123"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetCoordinationContextOutput)
	if !output.Found {
		t.Error("expected context to be found")
	}
}

func TestGetCoordinationContext_NotFound(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{
		ContentStore: &mockOrchestratorContentStore{
			entries: []*ctxpkg.ContentEntry{},
		},
	}
	skill := NewGetCoordinationContextSkill(deps)

	input, _ := json.Marshal(GetCoordinationContextInput{WorkflowID: "nonexistent"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetCoordinationContextOutput)
	if output.Found {
		t.Error("expected context not to be found")
	}
}

// =============================================================================
// Test: Helper Functions
// =============================================================================

func TestExtractPhaseFromContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content  string
		expected string
	}{
		{"Starting planning phase", "planning"},
		{"Implementation in progress", "implementation"},
		{"Running testing suite", "testing"},
		{"Code review started", "review"},
		{"Task complete", "complete"},
		{"Some random content", "in_progress"},
	}

	for _, tc := range tests {
		result := extractPhaseFromContent(tc.content)
		if result != tc.expected {
			t.Errorf("extractPhaseFromContent(%q) = %q, want %q", tc.content, result, tc.expected)
		}
	}
}

func TestExtractObjective(t *testing.T) {
	t.Parallel()

	content := "Implement user authentication\n\nDetails:\n- Add login\n- Add logout"
	objective := extractObjective(content)

	if objective != "Implement user authentication" {
		t.Errorf("unexpected objective: %s", objective)
	}
}

func TestTruncateOrchestratorContent(t *testing.T) {
	t.Parallel()

	longContent := "This is a very long content that should be truncated at some point to fit within the maximum length allowed."
	truncated := truncateOrchestratorContent(longContent, 50)

	if len(truncated) > 55 { // 50 + "..."
		t.Errorf("content not truncated properly: len=%d", len(truncated))
	}
}

func TestGetKnownAgentCapabilities(t *testing.T) {
	t.Parallel()

	// Test librarian
	caps := getKnownAgentCapabilities("librarian")
	if caps == nil {
		t.Error("expected librarian capabilities")
	}
	if caps.AgentType != "librarian" {
		t.Errorf("unexpected agent type: %s", caps.AgentType)
	}
	if len(caps.Skills) == 0 {
		t.Error("expected non-empty skills")
	}

	// Test unknown agent
	unknown := getKnownAgentCapabilities("unknown")
	if unknown != nil {
		t.Error("expected nil for unknown agent")
	}
}

// =============================================================================
// Test: Skill Registration
// =============================================================================

func TestRegisterOrchestratorSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &OrchestratorDependencies{}

	err := RegisterOrchestratorSkills(registry, deps)
	if err != nil {
		t.Fatalf("failed to register skills: %v", err)
	}

	expectedSkills := []string{
		"orchestrator_get_workflow_state",
		"orchestrator_get_agent_capabilities",
		"orchestrator_get_coordination_context",
	}
	for _, name := range expectedSkills {
		if registry.Get(name) == nil {
			t.Errorf("skill %s not registered", name)
		}
	}
}

// =============================================================================
// Test: Error Handling
// =============================================================================

func TestGetWorkflowState_ProviderError(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{
		WorkflowStateProvider: &mockWorkflowStateProvider{err: fmt.Errorf("provider failed")},
	}
	skill := NewGetWorkflowStateSkill(deps)

	input, _ := json.Marshal(GetWorkflowStateInput{WorkflowID: "wf-123"})
	result, err := skill.Handler(context.Background(), input)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	output := result.(*GetWorkflowStateOutput)
	if output.Found {
		t.Error("expected found=false on provider error")
	}
}

func TestGetAgentCapabilities_ProviderError(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{
		AgentCapabilityProvider: &mockAgentCapabilityProvider{err: fmt.Errorf("provider failed")},
	}
	skill := NewGetAgentCapabilitiesSkill(deps)

	input, _ := json.Marshal(GetAgentCapabilitiesInput{AgentType: "engineer"})
	result, err := skill.Handler(context.Background(), input)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	output := result.(*GetAgentCapabilitiesOutput)
	if output.Found {
		t.Error("expected found=false on provider error")
	}
}

func TestGetCoordinationContext_ProviderError(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{
		CoordinationContextProvider: &mockCoordinationContextProvider{err: fmt.Errorf("provider failed")},
	}
	skill := NewGetCoordinationContextSkill(deps)

	input, _ := json.Marshal(GetCoordinationContextInput{WorkflowID: "wf-123"})
	result, err := skill.Handler(context.Background(), input)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	output := result.(*GetCoordinationContextOutput)
	if output.Found {
		t.Error("expected found=false on provider error")
	}
}

func TestGetWorkflowState_ContentStoreError(t *testing.T) {
	t.Parallel()

	deps := &OrchestratorDependencies{
		ContentStore: &mockOrchestratorContentStore{err: fmt.Errorf("store error")},
	}
	skill := NewGetWorkflowStateSkill(deps)

	input, _ := json.Marshal(GetWorkflowStateInput{WorkflowID: "wf-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from content store")
	}
}

// =============================================================================
// Test: Constants
// =============================================================================

func TestOrchestratorConstants(t *testing.T) {
	t.Parallel()

	if OrchestratorDomain != "orchestrator" {
		t.Errorf("unexpected domain: %s", OrchestratorDomain)
	}
	if DefaultAgentLimit != 10 {
		t.Errorf("unexpected agent limit: %d", DefaultAgentLimit)
	}
	if DefaultWorkflowDepth != 5 {
		t.Errorf("unexpected workflow depth: %d", DefaultWorkflowDepth)
	}
}
