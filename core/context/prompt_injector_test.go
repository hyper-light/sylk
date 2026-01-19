package context

import (
	"strings"
	"testing"
)

// =============================================================================
// Injection Tests
// =============================================================================

func TestInjectAdaptiveRetrievalPrompt_Librarian(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are a librarian agent."
	result := InjectAdaptiveRetrievalPrompt("librarian", systemPrompt)

	if !strings.Contains(result, InjectionMarker) {
		t.Error("injection marker not found in result")
	}
	if !strings.Contains(result, "librarian_search_codebase") {
		t.Error("librarian-specific content not found")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Archivalist(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are an archivalist agent."
	result := InjectAdaptiveRetrievalPrompt("archivalist", systemPrompt)

	if !strings.Contains(result, "Failure Pattern Memory") {
		t.Error("archivalist-specific content not found")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Academic(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are an academic agent."
	result := InjectAdaptiveRetrievalPrompt("academic", systemPrompt)

	if !strings.Contains(result, "Research Search") {
		t.Error("academic-specific content not found")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Guide(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are a guide agent."
	result := InjectAdaptiveRetrievalPrompt("guide", systemPrompt)

	if !strings.Contains(result, "Prefetch Sharing") {
		t.Error("guide-specific content not found")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Engineer(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are an engineer agent."
	result := InjectAdaptiveRetrievalPrompt("engineer", systemPrompt)

	if !strings.Contains(result, "Limited Direct Retrieval") {
		t.Error("engineer-specific content not found")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Orchestrator(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are an orchestrator agent."
	result := InjectAdaptiveRetrievalPrompt("orchestrator", systemPrompt)

	if !strings.Contains(result, "Workflow Context") {
		t.Error("orchestrator-specific content not found")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Designer(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are a designer agent."
	result := InjectAdaptiveRetrievalPrompt("designer", systemPrompt)

	if !strings.Contains(result, "Pipeline Context") {
		t.Error("pipeline-specific content not found for designer")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Inspector(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are an inspector agent."
	result := InjectAdaptiveRetrievalPrompt("inspector", systemPrompt)

	if !strings.Contains(result, "Pipeline Context") {
		t.Error("pipeline-specific content not found for inspector")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Tester(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are a tester agent."
	result := InjectAdaptiveRetrievalPrompt("tester", systemPrompt)

	if !strings.Contains(result, "Pipeline Context") {
		t.Error("pipeline-specific content not found for tester")
	}
}

func TestInjectAdaptiveRetrievalPrompt_Unknown(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are an unknown agent."
	result := InjectAdaptiveRetrievalPrompt("unknown", systemPrompt)

	// Should get generic addition
	if !strings.Contains(result, InjectionMarker) {
		t.Error("generic injection marker not found")
	}
	if !strings.Contains(result, "Available Skills") {
		t.Error("generic content not found")
	}
}

func TestInjectAdaptiveRetrievalPrompt_CaseInsensitive(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are a librarian agent."

	tests := []string{"LIBRARIAN", "Librarian", "LiBrArIaN"}
	for _, agentType := range tests {
		t.Run(agentType, func(t *testing.T) {
			result := InjectAdaptiveRetrievalPrompt(agentType, systemPrompt)
			if !strings.Contains(result, "librarian_search_codebase") {
				t.Errorf("case-insensitive injection failed for %s", agentType)
			}
		})
	}
}

// =============================================================================
// Idempotency Tests
// =============================================================================

func TestInjectAdaptiveRetrievalPrompt_Idempotent(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are a librarian agent."

	// First injection
	result1 := InjectAdaptiveRetrievalPrompt("librarian", systemPrompt)

	// Second injection
	result2 := InjectAdaptiveRetrievalPrompt("librarian", result1)

	// Third injection
	result3 := InjectAdaptiveRetrievalPrompt("librarian", result2)

	// All should be identical
	if result1 != result2 {
		t.Error("second injection modified the result")
	}
	if result2 != result3 {
		t.Error("third injection modified the result")
	}

	// Count injection markers
	count := strings.Count(result3, InjectionMarker)
	if count != 1 {
		t.Errorf("expected 1 injection marker, got %d", count)
	}
}

func TestInjectAdaptiveRetrievalPrompt_IdempotentDifferentAgents(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are an agent."

	// Inject librarian prompt
	result1 := InjectAdaptiveRetrievalPrompt("librarian", systemPrompt)

	// Try to inject archivalist prompt - should be blocked by idempotency
	result2 := InjectAdaptiveRetrievalPrompt("archivalist", result1)

	// Should be unchanged
	if result1 != result2 {
		t.Error("second injection with different agent should be blocked")
	}
}

// =============================================================================
// HasAdaptiveRetrievalPrompt Tests
// =============================================================================

func TestHasAdaptiveRetrievalPrompt_True(t *testing.T) {
	t.Parallel()

	prompt := "Some content\n## ADAPTIVE RETRIEVAL INTEGRATION\nMore content"
	if !HasAdaptiveRetrievalPrompt(prompt) {
		t.Error("should detect injection marker")
	}
}

func TestHasAdaptiveRetrievalPrompt_False(t *testing.T) {
	t.Parallel()

	prompt := "Some content without the marker"
	if HasAdaptiveRetrievalPrompt(prompt) {
		t.Error("should not detect injection marker when absent")
	}
}

// =============================================================================
// RemoveAdaptiveRetrievalPrompt Tests
// =============================================================================

func TestRemoveAdaptiveRetrievalPrompt_Present(t *testing.T) {
	t.Parallel()

	original := "You are an agent."
	injected := InjectAdaptiveRetrievalPrompt("librarian", original)
	removed := RemoveAdaptiveRetrievalPrompt(injected)

	if strings.Contains(removed, InjectionMarker) {
		t.Error("marker should be removed")
	}
	if !strings.Contains(removed, "You are an agent") {
		t.Error("original content should be preserved")
	}
}

func TestRemoveAdaptiveRetrievalPrompt_NotPresent(t *testing.T) {
	t.Parallel()

	original := "You are an agent."
	result := RemoveAdaptiveRetrievalPrompt(original)

	if result != original {
		t.Error("should return original when no marker present")
	}
}

// =============================================================================
// GetPromptAdditionForAgent Tests
// =============================================================================

func TestGetPromptAdditionForAgent_AllAgents(t *testing.T) {
	t.Parallel()

	agents := GetSupportedAgentTypes()
	for _, agent := range agents {
		t.Run(agent, func(t *testing.T) {
			addition := GetPromptAdditionForAgent(agent)
			if addition == "" {
				t.Errorf("no addition for %s", agent)
			}
			if !strings.Contains(addition, InjectionMarker) {
				t.Errorf("addition for %s missing marker", agent)
			}
		})
	}
}

func TestGetPromptAdditionForAgent_UnknownReturnsGeneric(t *testing.T) {
	t.Parallel()

	addition := GetPromptAdditionForAgent("unknown_agent_type")
	if addition != GenericRetrievalPromptAddition {
		t.Error("unknown agent should get generic addition")
	}
}

// =============================================================================
// GetAllPromptAdditions Tests
// =============================================================================

func TestGetAllPromptAdditions(t *testing.T) {
	t.Parallel()

	additions := GetAllPromptAdditions()

	if len(additions) == 0 {
		t.Error("should return non-empty map")
	}

	// Verify librarian has specific content
	if _, ok := additions["librarian"]; !ok {
		t.Error("librarian should be in additions")
	}
}

func TestGetAllPromptAdditions_Immutable(t *testing.T) {
	t.Parallel()

	additions1 := GetAllPromptAdditions()
	additions1["test"] = "modified"

	additions2 := GetAllPromptAdditions()
	if _, ok := additions2["test"]; ok {
		t.Error("modifications should not affect original map")
	}
}

// =============================================================================
// GetSupportedAgentTypes Tests
// =============================================================================

func TestGetSupportedAgentTypes(t *testing.T) {
	t.Parallel()

	types := GetSupportedAgentTypes()

	expected := []string{
		"librarian", "archivalist", "academic", "guide",
		"engineer", "orchestrator", "designer", "inspector",
		"tester", "pipeline",
	}

	if len(types) != len(expected) {
		t.Errorf("expected %d types, got %d", len(expected), len(types))
	}

	typeSet := make(map[string]bool)
	for _, typ := range types {
		typeSet[typ] = true
	}

	for _, exp := range expected {
		if !typeSet[exp] {
			t.Errorf("missing expected type: %s", exp)
		}
	}
}

// =============================================================================
// Prompt Content Tests
// =============================================================================

func TestLibrarianPromptAddition_Content(t *testing.T) {
	t.Parallel()

	expected := []string{
		"CTX-REF",
		"AUTO-RETRIEVED",
		"librarian_search_codebase",
		"librarian_get_symbol_context",
		"Hot Cache Promotion",
		"promote_to_hot",
	}

	for _, exp := range expected {
		if !strings.Contains(LibrarianRetrievalPromptAddition, exp) {
			t.Errorf("LibrarianRetrievalPromptAddition missing: %s", exp)
		}
	}
}

func TestArchivalistPromptAddition_Content(t *testing.T) {
	t.Parallel()

	expected := []string{
		"Failure Pattern Memory",
		"FAILURE_PATTERN_WARNING",
		"recurrence_count",
		"Cross-Session Learning",
	}

	for _, exp := range expected {
		if !strings.Contains(ArchivalistRetrievalPromptAddition, exp) {
			t.Errorf("ArchivalistRetrievalPromptAddition missing: %s", exp)
		}
	}
}

func TestAcademicPromptAddition_Content(t *testing.T) {
	t.Parallel()

	expected := []string{
		"Research Search",
		"Applicability Analysis",
		"Source Citation",
		"Best Practices Lookup",
	}

	for _, exp := range expected {
		if !strings.Contains(AcademicRetrievalPromptAddition, exp) {
			t.Errorf("AcademicRetrievalPromptAddition missing: %s", exp)
		}
	}
}

func TestGuidePromptAddition_Content(t *testing.T) {
	t.Parallel()

	expected := []string{
		"Prefetch Sharing",
		"Routing History",
		"User Preferences",
	}

	for _, exp := range expected {
		if !strings.Contains(GuideRetrievalPromptAddition, exp) {
			t.Errorf("GuideRetrievalPromptAddition missing: %s", exp)
		}
	}
}

func TestEngineerPromptAddition_Content(t *testing.T) {
	t.Parallel()

	expected := []string{
		"Limited Direct Retrieval",
		"consult_librarian",
		"FAILURE_PATTERN_WARNING",
		"AUTO-RETRIEVED",
	}

	for _, exp := range expected {
		if !strings.Contains(EngineerRetrievalPromptAddition, exp) {
			t.Errorf("EngineerRetrievalPromptAddition missing: %s", exp)
		}
	}
}

func TestOrchestratorPromptAddition_Content(t *testing.T) {
	t.Parallel()

	expected := []string{
		"Workflow Context",
		"Similar Workflow Learning",
		"Context Handoff",
	}

	for _, exp := range expected {
		if !strings.Contains(OrchestratorRetrievalPromptAddition, exp) {
			t.Errorf("OrchestratorRetrievalPromptAddition missing: %s", exp)
		}
	}
}

func TestGenericPromptAddition_Content(t *testing.T) {
	t.Parallel()

	expected := []string{
		"CTX-REF",
		"AUTO-RETRIEVED",
		"retrieve_context",
		"search_history",
		"promote_to_hot",
	}

	for _, exp := range expected {
		if !strings.Contains(GenericRetrievalPromptAddition, exp) {
			t.Errorf("GenericRetrievalPromptAddition missing: %s", exp)
		}
	}
}
