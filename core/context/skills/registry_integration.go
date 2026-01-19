// Package skills provides AR.9.2: Skill Registry Integration - bridges adaptive
// retrieval skills with the existing skill registry system.
package skills

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Agent Type Constants
// =============================================================================

const (
	AgentTypeLibrarian   = "librarian"
	AgentTypeArchivalist = "archivalist"
	AgentTypeAcademic    = "academic"
	AgentTypeGuide       = "guide"
	AgentTypeOrchestrator = "orchestrator"
	AgentTypePipeline    = "pipeline"
	AgentTypeEngineer    = "engineer"
	AgentTypeDesigner    = "designer"
	AgentTypeInspector   = "inspector"
	AgentTypeTester      = "tester"
)

// KnowledgeAgents are agents that use universal retrieval skills.
var KnowledgeAgents = []string{
	AgentTypeLibrarian,
	AgentTypeArchivalist,
	AgentTypeAcademic,
	AgentTypeGuide,
	AgentTypeOrchestrator,
}

// =============================================================================
// Unified Dependencies
// =============================================================================

// AdaptiveRetrievalDependencies contains all dependencies for adaptive retrieval
// skills across all agent types.
type AdaptiveRetrievalDependencies struct {
	// Universal retrieval dependencies (for all knowledge agents)
	Universal *RetrievalDependencies

	// Agent-specific dependencies
	Librarian   *LibrarianDependencies
	Archivalist *ArchivalistDependencies
	Academic    *AcademicDependencies
	Guide       *GuideDependencies
	Orchestrator *OrchestratorDependencies
	Pipeline    *PipelineDependencies
}

// =============================================================================
// Skill Registration
// =============================================================================

// RegisterAdaptiveRetrievalSkills registers all adaptive retrieval skills with
// the provided skill registry. All skills are classified as TIER 1 (always loaded).
func RegisterAdaptiveRetrievalSkills(
	registry *skills.Registry,
	deps *AdaptiveRetrievalDependencies,
) error {
	if registry == nil {
		return fmt.Errorf("registry is required")
	}

	// Register universal skills (available to all knowledge agents)
	if err := registerUniversalSkillsIntegration(registry, deps.Universal); err != nil {
		return fmt.Errorf("failed to register universal skills: %w", err)
	}

	// Register agent-specific skills
	if err := registerAgentSpecificSkillsIntegration(registry, deps); err != nil {
		return fmt.Errorf("failed to register agent-specific skills: %w", err)
	}

	return nil
}

func registerUniversalSkillsIntegration(
	registry *skills.Registry,
	deps *RetrievalDependencies,
) error {
	if deps == nil {
		return nil // No universal dependencies provided
	}
	return RegisterUniversalSkills(registry, deps)
}

func registerAgentSpecificSkillsIntegration(
	registry *skills.Registry,
	deps *AdaptiveRetrievalDependencies,
) error {
	// Register Librarian skills
	if err := registerLibrarianIfProvidedIntegration(registry, deps.Librarian); err != nil {
		return err
	}

	// Register Archivalist skills
	if err := registerArchivalistIfProvidedIntegration(registry, deps.Archivalist); err != nil {
		return err
	}

	// Register Academic skills
	if err := registerAcademicIfProvidedIntegration(registry, deps.Academic); err != nil {
		return err
	}

	// Register Guide skills
	if err := registerGuideIfProvidedIntegration(registry, deps.Guide); err != nil {
		return err
	}

	// Register Orchestrator skills
	if err := registerOrchestratorIfProvidedIntegration(registry, deps.Orchestrator); err != nil {
		return err
	}

	// Register Pipeline skills
	if err := registerPipelineIfProvidedIntegration(registry, deps.Pipeline); err != nil {
		return err
	}

	return nil
}

func registerLibrarianIfProvidedIntegration(
	registry *skills.Registry,
	deps *LibrarianDependencies,
) error {
	if deps == nil {
		return nil
	}
	return RegisterLibrarianSkills(registry, deps)
}

func registerArchivalistIfProvidedIntegration(
	registry *skills.Registry,
	deps *ArchivalistDependencies,
) error {
	if deps == nil {
		return nil
	}
	return RegisterArchivalistSkills(registry, deps)
}

func registerAcademicIfProvidedIntegration(
	registry *skills.Registry,
	deps *AcademicDependencies,
) error {
	if deps == nil {
		return nil
	}
	return RegisterAcademicSkills(registry, deps)
}

func registerGuideIfProvidedIntegration(
	registry *skills.Registry,
	deps *GuideDependencies,
) error {
	if deps == nil {
		return nil
	}
	return RegisterGuideSkills(registry, deps)
}

func registerOrchestratorIfProvidedIntegration(
	registry *skills.Registry,
	deps *OrchestratorDependencies,
) error {
	if deps == nil {
		return nil
	}
	return RegisterOrchestratorSkills(registry, deps)
}

func registerPipelineIfProvidedIntegration(
	registry *skills.Registry,
	deps *PipelineDependencies,
) error {
	if deps == nil {
		return nil
	}
	return RegisterPipelineSkills(registry, deps)
}

// =============================================================================
// Agent-Targeted Registration
// =============================================================================

// RegisterSkillsForAgent registers skills appropriate for a specific agent type.
// This provides fine-grained control over which skills each agent receives.
func RegisterSkillsForAgent(
	registry *skills.Registry,
	agentType string,
	deps *AdaptiveRetrievalDependencies,
) error {
	if registry == nil {
		return fmt.Errorf("registry is required")
	}

	normalizedType := strings.ToLower(agentType)

	// All knowledge agents get universal skills
	if IsKnowledgeAgent(normalizedType) {
		if err := registerUniversalSkillsIntegration(registry, deps.Universal); err != nil {
			return err
		}
	}

	// Register agent-specific skills
	return registerAgentTypeSkillsIntegration(registry, normalizedType, deps)
}

// IsKnowledgeAgent checks if an agent type is a knowledge agent.
func IsKnowledgeAgent(agentType string) bool {
	for _, ka := range KnowledgeAgents {
		if ka == agentType {
			return true
		}
	}
	return false
}

func registerAgentTypeSkillsIntegration(
	registry *skills.Registry,
	agentType string,
	deps *AdaptiveRetrievalDependencies,
) error {
	switch agentType {
	case AgentTypeLibrarian:
		return registerLibrarianIfProvidedIntegration(registry, deps.Librarian)
	case AgentTypeArchivalist:
		return registerArchivalistIfProvidedIntegration(registry, deps.Archivalist)
	case AgentTypeAcademic:
		return registerAcademicIfProvidedIntegration(registry, deps.Academic)
	case AgentTypeGuide:
		return registerGuideIfProvidedIntegration(registry, deps.Guide)
	case AgentTypeOrchestrator:
		return registerOrchestratorIfProvidedIntegration(registry, deps.Orchestrator)
	case AgentTypePipeline:
		return registerPipelineIfProvidedIntegration(registry, deps.Pipeline)
	default:
		// Other agent types (engineer, designer, etc.) don't get specific skills
		return nil
	}
}

// =============================================================================
// Skill Loading (TIER 1)
// =============================================================================

// LoadAllAdaptiveRetrievalSkills loads all adaptive retrieval skills as TIER 1.
// This should be called after registration to ensure all skills are available
// in the LLM context.
func LoadAllAdaptiveRetrievalSkills(registry *skills.Registry) {
	// Load by domain - all AR skills use the "retrieval" domain
	registry.LoadDomain(RetrievalDomain)

	// Also load any skills that might use agent-specific domains
	registry.LoadDomain("librarian")
	registry.LoadDomain("archivalist")
	registry.LoadDomain("academic")
	registry.LoadDomain("guide")
	registry.LoadDomain("orchestrator")
	registry.LoadDomain("pipeline")
}

// =============================================================================
// Skill Queries
// =============================================================================

// GetSkillsForAgent returns all skills appropriate for an agent type.
func GetSkillsForAgent(
	registry *skills.Registry,
	agentType string,
) []*skills.Skill {
	normalizedType := strings.ToLower(agentType)

	var result []*skills.Skill

	// All knowledge agents get universal skills
	if IsKnowledgeAgent(normalizedType) {
		result = append(result, registry.GetByDomain(RetrievalDomain)...)
	}

	// Get agent-specific skills
	agentSkills := registry.GetByDomain(normalizedType)
	result = append(result, agentSkills...)

	return DeduplicateSkills(result)
}

// DeduplicateSkills removes duplicate skills from a slice.
func DeduplicateSkills(skillList []*skills.Skill) []*skills.Skill {
	if skillList == nil {
		return nil
	}

	seen := make(map[string]bool)
	var result []*skills.Skill

	for _, skill := range skillList {
		if !seen[skill.Name] {
			seen[skill.Name] = true
			result = append(result, skill)
		}
	}

	return result
}

// GetAllAdaptiveRetrievalSkillNames returns the names of all AR skills.
func GetAllAdaptiveRetrievalSkillNames() []string {
	return []string{
		// Universal skills
		"retrieve_context",
		"search_history",
		"promote_to_hot",

		// Librarian skills
		"librarian_search_codebase",
		"librarian_get_symbol_context",
		"librarian_get_pattern_examples",

		// Archivalist skills
		"archivalist_search_sessions",
		"archivalist_get_session_summary",
		"archivalist_find_similar_solutions",

		// Academic skills
		"academic_search_research",
		"academic_get_citations",
		"academic_summarize_paper",

		// Guide skills
		"guide_get_onboarding",
		"guide_find_examples",
		"guide_explain_concept",

		// Orchestrator skills
		"orchestrator_get_workflow_state",
		"orchestrator_get_agent_capabilities",
		"orchestrator_get_coordination_context",

		// Pipeline skills
		"pipeline_get_implementation_context",
		"pipeline_get_test_results",
		"pipeline_get_inspection_findings",
	}
}

// IsAdaptiveRetrievalSkill checks if a skill name is an AR skill.
func IsAdaptiveRetrievalSkill(name string) bool {
	for _, arName := range GetAllAdaptiveRetrievalSkillNames() {
		if arName == name {
			return true
		}
	}
	return false
}
