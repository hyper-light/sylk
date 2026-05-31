package skills

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// genericForestSkillNames lists the forest query surface for every
// agent's visible catalog. The four read-side verbs collapsed into a
// single `forest(op=resolve_intent|recall|recall_recent|predict_next)`
// so the LLM sees one name and selects the op.
var genericForestSkillNames = []string{
	"forest.retrieve_evidence",
	"forest.suggest_validations",
	"forest.propose_claim",
	"forest.review_contradiction",
	"forest.review_bridge",
	"forest.review_skill_candidate",
	"forest.record_outcome",
}

// forestMutatingSkillNames lists the write-side forest skill.
// forest_record_outcome stays a distinct name — the write semantics
// deserve their own catalog entry rather than hiding inside the
// read verb's op enum.
var forestMutatingSkillNames = []string{
	"forest.propose_claim",
	"forest.record_outcome",
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
	// Consolidated read-side verb + the single write-side verb. The
	// per-op builders above stay exported for direct programmatic use
	// but are no longer registered on the LLM-visible catalog.
	for _, skill := range []*skills.Skill{
		NewForestRetrieveEvidenceSkill(deps),
		NewForestSuggestValidationsSkill(deps),
		NewForestProposeClaimSkill(deps),
		NewForestReviewSkill("forest.review_contradiction", "Review contradiction evidence and quarantine state for a claim, artifact, or cluster.", deps),
		NewForestReviewSkill("forest.review_bridge", "Review bridge-node risk and cross-cluster evidence before a context crossing.", deps),
		NewForestReviewSkill("forest.review_skill_candidate", "Review skill-candidate evidence, validation needs, and quarantine state.", deps),
		NewForestRecordOutcomeNamedSkill("forest.record_outcome", deps),
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
//
// Phase 2.K / 10.G refactor: the per-role skill-per-purpose pattern
// collapsed into a single `<role>_forest_consult(purpose=…)` skill.
// This helper now returns the collapsed name for each matching role
// domain rather than one entry per historical spec.
func ForestSkillNamesForAgent(agentType string) []string {
	normalizedType := NormalizeAdaptiveAgentType(agentType)
	names := append([]string(nil), genericForestSkillNames...)
	seenRoles := make(map[string]struct{})
	for _, spec := range forestRoleSkillSpecs {
		if !roleForestSpecMatchesAgent(spec, normalizedType) {
			continue
		}
		if _, ok := seenRoles[spec.Domain]; ok {
			continue
		}
		seenRoles[spec.Domain] = struct{}{}
		names = append(names, collapsedRoleForestSkillName(spec.Domain))
	}
	return names
}

// ForestMutatingSkillNames returns the forest-backed skills that mutate state.
func ForestMutatingSkillNames() []string {
	return append([]string(nil), forestMutatingSkillNames...)
}
