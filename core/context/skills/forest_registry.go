package skills

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

var genericForestSkillNames = []string{
	"forest_resolve_intent",
	"forest_recall",
	"forest_predict_next_branches",
}

var forestMutatingSkillNames = []string{
	"forest_record_outcome",
}

// NormalizeAdaptiveAgentType maps runtime agent identifiers to the role family
// used by adaptive retrieval and Memory Forest skills.
func NormalizeAdaptiveAgentType(agentType string) string {
	normalized := strings.ToLower(strings.TrimSpace(agentType))
	switch {
	case normalized == "":
		return ""
	case strings.HasPrefix(normalized, AgentTypeScribe):
		return AgentTypeScribe
	case normalized == "inspector-pipeline":
		return AgentTypeInspector
	case normalized == "tester-pipeline":
		return AgentTypeTester
	default:
		return normalized
	}
}

func registerGenericForestSkills(registry *skills.Registry, deps *RetrievalDependencies) error {
	if registry == nil || deps == nil || deps.Forest == nil {
		return nil
	}
	for _, skill := range []*skills.Skill{
		NewForestResolveIntentSkill(deps),
		NewForestRecallSkill(deps),
		NewForestPredictNextSkill(deps),
		NewForestRecordOutcomeSkill(deps),
	} {
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register %s: %w", skill.Name, err)
		}
		registry.Load(skill.Name)
	}
	return nil
}

// RegisterForestSkillsForAgent registers only the Memory Forest-backed skills
// relevant to the given agent role.
func RegisterForestSkillsForAgent(
	registry *skills.Registry,
	agentType string,
	deps *RetrievalDependencies,
) error {
	if registry == nil {
		return fmt.Errorf("registry is required")
	}
	if deps == nil || deps.Forest == nil {
		return nil
	}
	normalizedType := NormalizeAdaptiveAgentType(agentType)
	if err := registerGenericForestSkills(registry, deps); err != nil {
		return err
	}
	return registerRoleForestSkillsForAgentIntegration(registry, normalizedType, deps)
}

// ForestSkillNamesForAgent returns the Memory Forest skills that should be
// exposed to an agent with the given runtime type.
func ForestSkillNamesForAgent(agentType string) []string {
	normalizedType := NormalizeAdaptiveAgentType(agentType)
	names := append([]string(nil), genericForestSkillNames...)
	for _, spec := range forestRoleSkillSpecs {
		if roleForestSpecMatchesAgent(spec, normalizedType) {
			names = append(names, spec.Name)
		}
	}
	return names
}

// ForestMutatingSkillNames returns the forest-backed skills that mutate state.
func ForestMutatingSkillNames() []string {
	return append([]string(nil), forestMutatingSkillNames...)
}
