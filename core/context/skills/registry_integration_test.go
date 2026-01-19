package skills

import (
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Registration Tests
// =============================================================================

func TestRegisterAdaptiveRetrievalSkills_NilRegistry(t *testing.T) {
	t.Parallel()

	err := RegisterAdaptiveRetrievalSkills(nil, &AdaptiveRetrievalDependencies{})
	if err == nil {
		t.Error("expected error for nil registry")
	}
}

func TestRegisterAdaptiveRetrievalSkills_NilDependencies(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	err := RegisterAdaptiveRetrievalSkills(registry, &AdaptiveRetrievalDependencies{})

	// Should succeed with no dependencies - just nothing registered
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegisterAdaptiveRetrievalSkills_WithUniversal(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal: &RetrievalDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check universal skills registered
	if registry.Get("retrieve_context") == nil {
		t.Error("retrieve_context should be registered")
	}
	if registry.Get("search_history") == nil {
		t.Error("search_history should be registered")
	}
	if registry.Get("promote_to_hot") == nil {
		t.Error("promote_to_hot should be registered")
	}
}

func TestRegisterAdaptiveRetrievalSkills_WithLibrarian(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Librarian: &LibrarianDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check librarian skills registered
	if registry.Get("librarian_search_codebase") == nil {
		t.Error("librarian_search_codebase should be registered")
	}
}

func TestRegisterAdaptiveRetrievalSkills_WithArchivalist(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Archivalist: &ArchivalistDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check archivalist skills registered
	if registry.Get("archivalist_search_sessions") == nil {
		t.Error("archivalist_search_sessions should be registered")
	}
}

func TestRegisterAdaptiveRetrievalSkills_WithAcademic(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Academic: &AcademicDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check academic skills registered
	if registry.Get("academic_search_research") == nil {
		t.Error("academic_search_research should be registered")
	}
}

func TestRegisterAdaptiveRetrievalSkills_WithGuide(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Guide: &GuideDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check guide skills registered
	if registry.Get("guide_get_onboarding") == nil {
		t.Error("guide_get_onboarding should be registered")
	}
}

func TestRegisterAdaptiveRetrievalSkills_WithOrchestrator(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Orchestrator: &OrchestratorDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check orchestrator skills registered
	if registry.Get("orchestrator_get_workflow_state") == nil {
		t.Error("orchestrator_get_workflow_state should be registered")
	}
}

func TestRegisterAdaptiveRetrievalSkills_WithPipeline(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Pipeline: &PipelineDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check pipeline skills registered
	if registry.Get("pipeline_get_implementation_context") == nil {
		t.Error("pipeline_get_implementation_context should be registered")
	}
}

func TestRegisterAdaptiveRetrievalSkills_AllSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal:    &RetrievalDependencies{},
		Librarian:    &LibrarianDependencies{},
		Archivalist:  &ArchivalistDependencies{},
		Academic:     &AcademicDependencies{},
		Guide:        &GuideDependencies{},
		Orchestrator: &OrchestratorDependencies{},
		Pipeline:     &PipelineDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify all skill names are registered
	allNames := GetAllAdaptiveRetrievalSkillNames()
	for _, name := range allNames {
		if registry.Get(name) == nil {
			t.Errorf("skill %s should be registered", name)
		}
	}
}

// =============================================================================
// Agent-Targeted Registration Tests
// =============================================================================

func TestRegisterSkillsForAgent_NilRegistry(t *testing.T) {
	t.Parallel()

	err := RegisterSkillsForAgent(nil, "librarian", &AdaptiveRetrievalDependencies{})
	if err == nil {
		t.Error("expected error for nil registry")
	}
}

func TestRegisterSkillsForAgent_Librarian(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal: &RetrievalDependencies{},
		Librarian: &LibrarianDependencies{},
	}

	err := RegisterSkillsForAgent(registry, "librarian", deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have universal skills (knowledge agent)
	if registry.Get("retrieve_context") == nil {
		t.Error("librarian should have universal skills")
	}

	// Should have librarian-specific skills
	if registry.Get("librarian_search_codebase") == nil {
		t.Error("librarian should have librarian skills")
	}
}

func TestRegisterSkillsForAgent_Engineer(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal: &RetrievalDependencies{},
	}

	err := RegisterSkillsForAgent(registry, "engineer", deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Engineer is not a knowledge agent, no universal skills
	if registry.Get("retrieve_context") != nil {
		t.Error("engineer should not have universal skills")
	}
}

func TestRegisterSkillsForAgent_CaseInsensitive(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal: &RetrievalDependencies{},
		Librarian: &LibrarianDependencies{},
	}

	err := RegisterSkillsForAgent(registry, "LIBRARIAN", deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if registry.Get("librarian_search_codebase") == nil {
		t.Error("should handle uppercase agent type")
	}
}

// =============================================================================
// Knowledge Agent Tests
// =============================================================================

func TestIsKnowledgeAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		agentType string
		expected  bool
	}{
		{"librarian", true},
		{"archivalist", true},
		{"academic", true},
		{"guide", true},
		{"orchestrator", true},
		{"engineer", false},
		{"designer", false},
		{"inspector", false},
		{"tester", false},
		{"unknown", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.agentType, func(t *testing.T) {
			t.Parallel()
			result := IsKnowledgeAgent(tc.agentType)
			if result != tc.expected {
				t.Errorf("IsKnowledgeAgent(%s) = %v, want %v",
					tc.agentType, result, tc.expected)
			}
		})
	}
}

func TestKnowledgeAgents(t *testing.T) {
	t.Parallel()

	expected := []string{"librarian", "archivalist", "academic", "guide", "orchestrator"}
	if len(KnowledgeAgents) != len(expected) {
		t.Errorf("KnowledgeAgents length = %d, want %d",
			len(KnowledgeAgents), len(expected))
	}

	for i, agent := range expected {
		if KnowledgeAgents[i] != agent {
			t.Errorf("KnowledgeAgents[%d] = %s, want %s",
				i, KnowledgeAgents[i], agent)
		}
	}
}

// =============================================================================
// Skill Loading Tests
// =============================================================================

func TestLoadAllAdaptiveRetrievalSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal: &RetrievalDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	LoadAllAdaptiveRetrievalSkills(registry)

	// Verify skills are loaded
	loaded := registry.GetLoaded()
	if len(loaded) == 0 {
		t.Error("no skills loaded")
	}
}

// =============================================================================
// Skill Query Tests
// =============================================================================

func TestGetSkillsForAgent(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal: &RetrievalDependencies{},
		Librarian: &LibrarianDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	agentSkills := GetSkillsForAgent(registry, "librarian")
	if len(agentSkills) == 0 {
		t.Error("librarian should have skills")
	}
}

func TestGetSkillsForAgent_Deduplication(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal: &RetrievalDependencies{},
		Librarian: &LibrarianDependencies{},
	}

	err := RegisterAdaptiveRetrievalSkills(registry, deps)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	agentSkills := GetSkillsForAgent(registry, "librarian")

	// Check for duplicates
	seen := make(map[string]bool)
	for _, skill := range agentSkills {
		if seen[skill.Name] {
			t.Errorf("duplicate skill: %s", skill.Name)
		}
		seen[skill.Name] = true
	}
}

func TestGetAllAdaptiveRetrievalSkillNames(t *testing.T) {
	t.Parallel()

	names := GetAllAdaptiveRetrievalSkillNames()

	// Should have 21 skills total:
	// 3 universal + 3 librarian + 3 archivalist + 3 academic +
	// 3 guide + 3 orchestrator + 3 pipeline
	expected := 21
	if len(names) != expected {
		t.Errorf("GetAllAdaptiveRetrievalSkillNames() returned %d names, want %d",
			len(names), expected)
	}

	// Check uniqueness
	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate skill name: %s", name)
		}
		seen[name] = true
	}
}

func TestIsAdaptiveRetrievalSkill(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected bool
	}{
		{"retrieve_context", true},
		{"librarian_search_codebase", true},
		{"archivalist_search_sessions", true},
		{"academic_search_research", true},
		{"guide_get_onboarding", true},
		{"orchestrator_get_workflow_state", true},
		{"pipeline_get_implementation_context", true},
		{"unknown_skill", false},
		{"", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := IsAdaptiveRetrievalSkill(tc.name)
			if result != tc.expected {
				t.Errorf("IsAdaptiveRetrievalSkill(%s) = %v, want %v",
					tc.name, result, tc.expected)
			}
		})
	}
}

// =============================================================================
// Agent Type Constants Tests
// =============================================================================

func TestAgentTypeConstants(t *testing.T) {
	t.Parallel()

	expectedConstants := map[string]string{
		"AgentTypeLibrarian":   "librarian",
		"AgentTypeArchivalist": "archivalist",
		"AgentTypeAcademic":    "academic",
		"AgentTypeGuide":       "guide",
		"AgentTypeOrchestrator": "orchestrator",
		"AgentTypePipeline":    "pipeline",
		"AgentTypeEngineer":    "engineer",
		"AgentTypeDesigner":    "designer",
		"AgentTypeInspector":   "inspector",
		"AgentTypeTester":      "tester",
	}

	actualValues := map[string]string{
		"AgentTypeLibrarian":   AgentTypeLibrarian,
		"AgentTypeArchivalist": AgentTypeArchivalist,
		"AgentTypeAcademic":    AgentTypeAcademic,
		"AgentTypeGuide":       AgentTypeGuide,
		"AgentTypeOrchestrator": AgentTypeOrchestrator,
		"AgentTypePipeline":    AgentTypePipeline,
		"AgentTypeEngineer":    AgentTypeEngineer,
		"AgentTypeDesigner":    AgentTypeDesigner,
		"AgentTypeInspector":   AgentTypeInspector,
		"AgentTypeTester":      AgentTypeTester,
	}

	for name, expected := range expectedConstants {
		actual := actualValues[name]
		if actual != expected {
			t.Errorf("%s = %s, want %s", name, actual, expected)
		}
	}
}

// =============================================================================
// Deduplication Helper Test
// =============================================================================

func TestDeduplicateSkills(t *testing.T) {
	t.Parallel()

	skill1 := &skills.Skill{Name: "skill_a"}
	skill2 := &skills.Skill{Name: "skill_b"}
	skill3 := &skills.Skill{Name: "skill_a"} // Duplicate

	input := []*skills.Skill{skill1, skill2, skill3}
	result := DeduplicateSkills(input)

	if len(result) != 2 {
		t.Errorf("DeduplicateSkills returned %d skills, want 2", len(result))
	}
}

func TestDeduplicateSkills_Empty(t *testing.T) {
	t.Parallel()

	result := DeduplicateSkills(nil)
	if result != nil {
		t.Error("DeduplicateSkills(nil) should return nil")
	}
}

func TestDeduplicateSkills_NoChanges(t *testing.T) {
	t.Parallel()

	skill1 := &skills.Skill{Name: "skill_a"}
	skill2 := &skills.Skill{Name: "skill_b"}

	input := []*skills.Skill{skill1, skill2}
	result := DeduplicateSkills(input)

	if len(result) != 2 {
		t.Errorf("DeduplicateSkills returned %d skills, want 2", len(result))
	}
}
