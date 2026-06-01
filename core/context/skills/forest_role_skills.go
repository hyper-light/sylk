package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/skills"
)

const defaultRoleForestLimit = 6

// ForestRoleInput configures a role-shaped forest query.
type ForestRoleInput struct {
	Query                  string `json:"query"`
	SessionID              string `json:"session_id,omitempty"`
	TaskID                 string `json:"task_id,omitempty"`
	AgentID                string `json:"agent_id,omitempty"`
	IntentID               string `json:"intent_id,omitempty"`
	CursorID               string `json:"cursor_id,omitempty"`
	Horizon                string `json:"horizon,omitempty"`
	Limit                  int    `json:"limit,omitempty"`
	IncludeCounterEvidence *bool  `json:"include_counter_evidence,omitempty"`
}

// ForestRoleOutput packages intent and retrieval results for role-specific skills.
type ForestRoleOutput struct {
	Role       string                       `json:"role"`
	Intent     *forest.IntentResolution     `json:"intent,omitempty"`
	Packets    []*forest.ForestPacket       `json:"packets,omitempty"`
	Cursor     *forest.ForestCursor         `json:"cursor,omitempty"`
	Projection *forest.ForestRoleProjection `json:"projection,omitempty"`
	Focus      []string                     `json:"focus,omitempty"`
	// ConsultActivityID is the fabric activity ID emitted by the
	// handler when the consult ran. Populated on every successful
	// return. Downstream activity emissions that want to declare
	// causation back to this consult can use the ID as a Caused
	// pointer — the forest bridge then joins outcome to consult
	// during harvest. See Tier 5 of
	// docs/FOREST_FABRIC_INTEGRATION.md.
	ConsultActivityID string `json:"consult_activity_id,omitempty"`
}

type forestRoleSkillSpec struct {
	Name                   string
	Domain                 string
	Description            string
	Keywords               []string
	QueryDescription       string
	Kinds                  []forest.ForestNodeKind
	DefaultLimit           int
	IncludeCounterEvidence bool
	Predict                bool
	Usage                  string
	BestPractices          []string
}

var (
	roleIntentNodeKinds = []forest.ForestNodeKind{
		forest.ForestNodeClaim,
		forest.ForestNodeInteraction,
		forest.ForestNodePolicyTrial,
		forest.ForestNodeSkillCandidate,
	}
	roleConstraintNodeKinds = []forest.ForestNodeKind{
		forest.ForestNodeValidation,
		forest.ForestNodePolicyTrial,
	}
	roleEvidenceNodeKinds = []forest.ForestNodeKind{
		forest.ForestNodeArtifact,
		forest.ForestNodeValidation,
		forest.ForestNodeTestament,
		forest.ForestNodeContent,
	}
	roleOutcomeNodeKinds = []forest.ForestNodeKind{
		forest.ForestNodeOutcome,
		forest.ForestNodeRemediation,
	}
	roleAntiPatternNodeKinds = []forest.ForestNodeKind{
		forest.ForestNodeContradiction,
	}
)

func roleNodeKinds(groups ...[]forest.ForestNodeKind) []forest.ForestNodeKind {
	seen := make(map[forest.ForestNodeKind]struct{})
	kinds := make([]forest.ForestNodeKind, 0)
	for _, group := range groups {
		for _, kind := range group {
			if _, ok := seen[kind]; ok {
				continue
			}
			seen[kind] = struct{}{}
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

var forestRoleSkillSpecs = []forestRoleSkillSpec{
	{
		Name:                   "architect_forest_get_plan_precedents",
		Domain:                 AgentTypeArchitect,
		Description:            "Recall prior plan nodes, constraints, evidence, and outcomes relevant to the current architectural direction.",
		Keywords:               []string{"architect", "plan", "precedent", "constraint", "outcome"},
		QueryDescription:       "Architectural plan, decomposition, or design direction to ground in precedent",
		Kinds:                  roleNodeKinds(roleIntentNodeKinds, roleOutcomeNodeKinds, roleEvidenceNodeKinds, roleConstraintNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
		Usage:                  "Use before finalizing a plan, major decomposition, or architectural trade-off so the architect sees prior successful and failed plan paths.",
		BestPractices: []string{
			"Prefer this when the plan could repeat a past failure or when the repo already has strong implementation precedent.",
		},
	},
	{
		Name:             "architect_forest_compare_plan_paths",
		Domain:           AgentTypeArchitect,
		Description:      "Predict low-risk adjacent plan paths that could improve the current architecture or decomposition.",
		Keywords:         []string{"architect", "compare", "plan", "path", "adjacent"},
		QueryDescription: "Current architectural problem or decomposition to compare against adjacent packet paths",
		DefaultLimit:     5,
		Predict:          true,
		Usage:            "Use when the architect wants to check whether a nearby path would better satisfy likely user intent without surprising scope expansion.",
	},
	{
		Name:                   "academic_forest_get_authority_bundle",
		Domain:                 AgentTypeAcademic,
		Description:            "Recall evidence, outcomes, and capability precedents that best support or bound the current research direction.",
		Keywords:               []string{"academic", "authority", "evidence", "precedent", "research"},
		QueryDescription:       "Research question, proposal, or external claim to ground in authority-weighted precedent",
		Kinds:                  roleNodeKinds(roleEvidenceNodeKinds, roleOutcomeNodeKinds, roleIntentNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "academic_forest_check_contradictions",
		Domain:                 AgentTypeAcademic,
		Description:            "Retrieve contradiction-heavy nodes and constraints so academic work does not overstate weak or conflicting evidence.",
		Keywords:               []string{"academic", "contradiction", "counterevidence", "constraint"},
		QueryDescription:       "Claim or proposal that needs contradiction and counterevidence review",
		Kinds:                  roleNodeKinds(roleAntiPatternNodeKinds, roleEvidenceNodeKinds, roleConstraintNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "librarian_forest_get_code_precedents",
		Domain:                 AgentTypeLibrarian,
		Description:            "Retrieve code-facing precedents, implementation nodes, and capability evidence for the current repo task.",
		Keywords:               []string{"librarian", "code", "precedent", "implementation", "pattern"},
		QueryDescription:       "Code problem, implementation pattern, or touched-file concern to ground in repo precedent",
		Kinds:                  roleNodeKinds(roleEvidenceNodeKinds, roleIntentNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "librarian_forest_get_implementation_risks",
		Domain:                 AgentTypeLibrarian,
		Description:            "Surface implementation risks, prior conflicts, and failure outcomes related to the current code path.",
		Keywords:               []string{"librarian", "risk", "implementation", "failure", "conflict"},
		QueryDescription:       "Implementation area that may have hidden repo-local risks or conflict precedent",
		Kinds:                  roleNodeKinds(roleAntiPatternNodeKinds, roleOutcomeNodeKinds, roleConstraintNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "archivalist_forest_get_decision_precedents",
		Domain:                 AgentTypeArchivalist,
		Description:            "Recall prior decisions, outcomes, and preference context relevant to the current problem.",
		Keywords:               []string{"archivalist", "decision", "precedent", "history", "outcome"},
		QueryDescription:       "Problem or decision area that should be grounded in prior historical choices",
		Kinds:                  roleNodeKinds(roleIntentNodeKinds, roleOutcomeNodeKinds, roleConstraintNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "archivalist_forest_get_failure_precedents",
		Domain:                 AgentTypeArchivalist,
		Description:            "Retrieve past failed or mixed paths so current work avoids repeating historical mistakes.",
		Keywords:               []string{"archivalist", "failure", "precedent", "history", "conflict"},
		QueryDescription:       "Failure mode, repeated issue, or risky area to compare against prior outcomes",
		Kinds:                  roleNodeKinds(roleOutcomeNodeKinds, roleAntiPatternNodeKinds, roleIntentNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "guide_forest_get_user_intent_history",
		Domain:                 AgentTypeGuide,
		Description:            "Resolve the user's active intent using prior intent, preference, and outcome nodes instead of only the literal current prompt.",
		Keywords:               []string{"guide", "intent", "history", "preference", "outcome"},
		QueryDescription:       "User request or confusion point that needs longitudinal intent context",
		Kinds:                  roleNodeKinds(roleIntentNodeKinds, roleConstraintNodeKinds, roleOutcomeNodeKinds),
		DefaultLimit:           5,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "guide_forest_get_teaching_precedents",
		Domain:                 AgentTypeGuide,
		Description:            "Retrieve evidence and successful explanation nodes that can improve onboarding or guidance quality.",
		Keywords:               []string{"guide", "teaching", "example", "onboarding", "precedent"},
		QueryDescription:       "Concept, tutorial need, or explanation path that should use prior successful teaching nodes",
		Kinds:                  roleNodeKinds(roleEvidenceNodeKinds, roleIntentNodeKinds, roleOutcomeNodeKinds),
		DefaultLimit:           5,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "orchestrator_forest_get_coordination_precedents",
		Domain:                 AgentTypeOrchestrator,
		Description:            "Recall prior coordination paths, outcomes, and capability precedents for the current multi-agent situation.",
		Keywords:               []string{"orchestrator", "coordination", "precedent", "handoff", "workflow"},
		QueryDescription:       "Coordination problem, workflow state, or multi-agent objective to ground in precedent",
		Kinds:                  roleNodeKinds(roleIntentNodeKinds, roleOutcomeNodeKinds, roleAntiPatternNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "orchestrator_forest_predict_handoff_path",
		Domain:           AgentTypeOrchestrator,
		Description:      "Predict low-risk adjacent paths that could improve coordination or handoff sequencing.",
		Keywords:         []string{"orchestrator", "predict", "handoff", "sequence", "adjacent"},
		QueryDescription: "Workflow or coordination problem that may have a better next handoff path",
		DefaultLimit:     5,
		Predict:          true,
	},
	{
		Name:                   "engineer_forest_select_implementation_node",
		Domain:                 AgentTypeEngineer,
		Description:            "Retrieve the strongest implementation nodes, constraints, and capability evidence for the current coding task.",
		Keywords:               []string{"engineer", "implementation", "node", "constraint", "capability"},
		QueryDescription:       "Implementation task or coding problem to ground in forest precedent",
		Kinds:                  roleNodeKinds(roleIntentNodeKinds, roleEvidenceNodeKinds, roleConstraintNodeKinds, roleOutcomeNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
		Usage:                  "Use before editing when there are multiple plausible implementations or when prior regressions matter.",
	},
	{
		Name:                   "engineer_forest_get_failure_precedents",
		Domain:                 AgentTypeEngineer,
		Description:            "Retrieve prior failures, constraints, and conflict-heavy nodes that could invalidate the current implementation path.",
		Keywords:               []string{"engineer", "failure", "regression", "constraint", "conflict"},
		QueryDescription:       "Implementation area that may have hidden regression or failure precedent",
		Kinds:                  roleNodeKinds(roleOutcomeNodeKinds, roleAntiPatternNodeKinds, roleConstraintNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "designer_forest_get_preference_prior",
		Domain:                 AgentTypeDesigner,
		Description:            "Recall user preference, intent, and outcome precedent relevant to the current design choice.",
		Keywords:               []string{"designer", "preference", "intent", "style", "outcome"},
		QueryDescription:       "Design problem or UX direction to ground in user preference precedent",
		Kinds:                  roleNodeKinds(roleConstraintNodeKinds, roleIntentNodeKinds, roleOutcomeNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "designer_forest_discover_adjacent_value",
		Domain:           AgentTypeDesigner,
		Description:      "Predict low-risk adjacent design paths that could improve the work beyond literal compliance.",
		Keywords:         []string{"designer", "adjacent", "value", "opportunity", "predict"},
		QueryDescription: "Design task or UX problem to explore for safe adjacent value",
		DefaultLimit:     5,
		Predict:          true,
	},
	{
		Name:                   "guardian_forest_evaluate_scope_risk",
		Domain:                 AgentTypeGuardian,
		Description:            "Retrieve constraints, conflicts, decisions, and outcomes that indicate scope or policy risk in the current path.",
		Keywords:               []string{"guardian", "scope", "risk", "policy", "constraint"},
		QueryDescription:       "Action or plan that should be checked for scope, policy, or authority risk",
		Kinds:                  roleNodeKinds(roleConstraintNodeKinds, roleAntiPatternNodeKinds, roleOutcomeNodeKinds, roleIntentNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "guardian_forest_get_approval_precedents",
		Domain:                 AgentTypeGuardian,
		Description:            "Recall prior approved or rejected outcomes relevant to the current governance decision.",
		Keywords:               []string{"guardian", "approval", "precedent", "governance", "decision"},
		QueryDescription:       "Governance or approval decision that should be checked against precedent",
		Kinds:                  roleNodeKinds(roleIntentNodeKinds, roleOutcomeNodeKinds, roleConstraintNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "scribe_forest_get_capture_targets",
		Domain:                 AgentTypeScribe,
		Description:            "Retrieve the intent, decisions, outcomes, conflicts, and preferences a scribe should preserve from the current work.",
		Keywords:               []string{"scribe", "capture", "handoff", "summary", "decision"},
		QueryDescription:       "Work in progress that the scribe should capture with the highest downstream value",
		Kinds:                  roleNodeKinds(roleIntentNodeKinds, roleOutcomeNodeKinds, roleAntiPatternNodeKinds, roleConstraintNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "scribe_forest_prepare_handoff_context",
		Domain:                 AgentTypeScribe,
		Description:            "Retrieve the forest context most important to preserve for future handoff and replay.",
		Keywords:               []string{"scribe", "handoff", "replay", "context", "preserve"},
		QueryDescription:       "Workstream or handoff target that needs compact high-signal forest context",
		Kinds:                  roleNodeKinds(roleIntentNodeKinds, roleOutcomeNodeKinds, roleConstraintNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "inspector_forest_get_regression_precedents",
		Domain:                 AgentTypeInspector,
		Description:            "Retrieve prior regressions, conflicts, and evidence bundles relevant to the current inspection target.",
		Keywords:               []string{"inspector", "regression", "precedent", "finding", "conflict"},
		QueryDescription:       "Inspection target, suspected regression, or review concern to ground in precedent",
		Kinds:                  roleNodeKinds(roleOutcomeNodeKinds, roleAntiPatternNodeKinds, roleEvidenceNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "inspector_forest_get_validation_targets",
		Domain:                 AgentTypeInspector,
		Description:            "Retrieve the constraints, decisions, outcomes, and evidence that should be validated by inspection.",
		Keywords:               []string{"inspector", "validation", "target", "constraint", "evidence"},
		QueryDescription:       "Implementation or change set that needs inspection targets and validation context",
		Kinds:                  roleNodeKinds(roleConstraintNodeKinds, roleOutcomeNodeKinds, roleIntentNodeKinds, roleEvidenceNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "tester_forest_get_test_targets",
		Domain:                 AgentTypeTester,
		Description:            "Retrieve constraints, capabilities, outcomes, and evidence that should shape the current testing target set.",
		Keywords:               []string{"tester", "test", "target", "coverage", "constraint"},
		QueryDescription:       "Implementation or behavior that needs a strong testing target set",
		Kinds:                  roleNodeKinds(roleConstraintNodeKinds, roleIntentNodeKinds, roleOutcomeNodeKinds, roleEvidenceNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:                   "tester_forest_get_failure_clusters",
		Domain:                 AgentTypeTester,
		Description:            "Retrieve clustered failure and conflict precedent to improve test selection and guard against repeated misses.",
		Keywords:               []string{"tester", "failure", "cluster", "regression", "conflict"},
		QueryDescription:       "Failure mode or risky behavior that should be checked against clustered precedent",
		Kinds:                  roleNodeKinds(roleOutcomeNodeKinds, roleAntiPatternNodeKinds, roleEvidenceNodeKinds),
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
}

// roleForestSkillNames returns the collapsed `<role>_forest_consult`
// skill names — one per role domain — that replace the historical
// per-purpose `<role>_forest_*` skills. Phase 2.K / 10.G refactor.
func roleForestSkillNames() []string {
	seen := make(map[string]struct{}, len(forestRoleSkillSpecs))
	names := make([]string, 0, len(forestRoleSkillSpecs))
	for _, spec := range forestRoleSkillSpecs {
		if _, ok := seen[spec.Domain]; ok {
			continue
		}
		seen[spec.Domain] = struct{}{}
		names = append(names, collapsedRoleForestSkillName(spec.Domain))
	}
	return names
}

func roleForestDomains() []string {
	seen := make(map[string]struct{}, len(forestRoleSkillSpecs))
	domains := make([]string, 0, len(forestRoleSkillSpecs))
	for _, spec := range forestRoleSkillSpecs {
		if _, ok := seen[spec.Domain]; ok {
			continue
		}
		seen[spec.Domain] = struct{}{}
		domains = append(domains, spec.Domain)
	}
	return domains
}

// RegisterRoleForestSkills registers the collapsed per-role
// `<role>_forest_consult(purpose=…)` skills for every role that has
// specs. Phase 2.K / 10.G: replaces the pre-collapse loop that
// registered each spec under its own name. Keeps the same entry point
// used by RegisterAdaptiveRetrievalSkills.
func RegisterRoleForestSkills(registry *skills.Registry, deps *RetrievalDependencies) error {
	if registry == nil || deps == nil || deps.Forest == nil {
		return nil
	}
	roleSpecs := make(map[string][]forestRoleSkillSpec)
	roleOrder := make([]string, 0)
	for _, spec := range forestRoleSkillSpecs {
		if _, ok := roleSpecs[spec.Domain]; !ok {
			roleOrder = append(roleOrder, spec.Domain)
		}
		roleSpecs[spec.Domain] = append(roleSpecs[spec.Domain], spec)
	}
	for _, role := range roleOrder {
		skill := NewRoleForestConsultSkill(deps, role, roleSpecs[role])
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register %s: %w", skill.Name, err)
		}
		registry.Load(skill.Name)
	}
	return nil
}

func registerRoleForestSkillsForAgentIntegration(
	registry *skills.Registry,
	agentType string,
	deps *RetrievalDependencies,
) error {
	if registry == nil || deps == nil || deps.Forest == nil {
		return nil
	}
	agentType = NormalizeAdaptiveAgentType(agentType)
	// Phase 2.K / 10.G refactor (docs/PIPELINE_SKILL_REFACTOR.md §10.G):
	// group the per-role specs by domain and emit ONE collapsed skill
	// per role — `<role>_forest_consult(purpose=…)`. The purpose enum
	// values are the spec name suffixes (e.g. "get_test_targets",
	// "get_failure_clusters") so semantic cueing is preserved. Handler
	// bodies for all specs were already identical (declarative over
	// forestRoleSkillSpec); the collapse simply shifts enum dispatch
	// from name-based to parameter-based.
	roleSpecs := make(map[string][]forestRoleSkillSpec)
	roleOrder := make([]string, 0)
	for _, spec := range forestRoleSkillSpecs {
		if !roleForestSpecMatchesAgent(spec, agentType) {
			continue
		}
		if _, ok := roleSpecs[spec.Domain]; !ok {
			roleOrder = append(roleOrder, spec.Domain)
		}
		roleSpecs[spec.Domain] = append(roleSpecs[spec.Domain], spec)
	}
	for _, role := range roleOrder {
		specs := roleSpecs[role]
		skill := NewRoleForestConsultSkill(deps, role, specs)
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register %s: %w", skill.Name, err)
		}
		registry.Load(skill.Name)
	}
	return nil
}

// collapsedRoleForestSkillName returns the unified `<role>_forest_consult`
// skill name for a given role family.
func collapsedRoleForestSkillName(role string) string {
	return role + "_forest_consult"
}

// forestConsultPurposeForSpec returns the purpose enum value for a given
// per-spec skill — the original skill name with the role prefix stripped.
// e.g. "tester_forest_get_test_targets" → "get_test_targets".
func forestConsultPurposeForSpec(spec forestRoleSkillSpec) string {
	prefix := spec.Domain + "_forest_"
	return strings.TrimPrefix(spec.Name, prefix)
}

func roleForestSpecMatchesAgent(spec forestRoleSkillSpec, agentType string) bool {
	agentType = NormalizeAdaptiveAgentType(agentType)
	if spec.Domain == agentType {
		return true
	}
	return spec.Domain == AgentTypeScribe && strings.HasPrefix(agentType, AgentTypeScribe)
}

// NewRoleForestConsultSkill builds the collapsed per-role
// `<role>_forest_consult(purpose=…)` skill that replaces the
// historical per-spec `<role>_forest_<action>` skills. Each spec's
// descriptive metadata becomes one enum value of `purpose`; the
// handler dispatches to the same underlying code path as the original
// NewRoleForestSkill handler (which was already declarative over
// forestRoleSkillSpec — see the pre-§10.G code).
func NewRoleForestConsultSkill(deps *RetrievalDependencies, role string, specs []forestRoleSkillSpec) *skills.Skill {
	name := collapsedRoleForestSkillName(role)
	purposeEnum := make([]string, 0, len(specs))
	specByPurpose := make(map[string]forestRoleSkillSpec, len(specs))
	var purposeLines strings.Builder
	keywordSet := map[string]struct{}{"forest": {}, "consult": {}, role: {}}
	keywordOrder := []string{"forest", "consult", role}
	for _, spec := range specs {
		purpose := forestConsultPurposeForSpec(spec)
		purposeEnum = append(purposeEnum, purpose)
		specByPurpose[purpose] = spec
		fmt.Fprintf(&purposeLines, "  - purpose=%q: %s\n", purpose, spec.Description)
		for _, kw := range spec.Keywords {
			if _, ok := keywordSet[kw]; !ok {
				keywordSet[kw] = struct{}{}
				keywordOrder = append(keywordOrder, kw)
			}
		}
	}
	description := fmt.Sprintf(
		"Memory Forest consultation for the %s role. Run a packet retrieval query against role-scoped forest node kinds, selected by the purpose enum.\n\nPurposes:\n%s",
		role, purposeLines.String(),
	)
	b := skills.NewSkill(name).
		Description(description).
		Domain(role).
		Keywords(keywordOrder...).
		Priority(95).
		EnumParam("purpose", "Which role-specific forest query to run.", purposeEnum, true).
		StringParam("query", "Natural language description of the current task or concern", true).
		StringParam("session_id", "Optional session identifier for session-scoped retrieval", false).
		StringParam("task_id", "Optional task identifier for task-scoped retrieval", false).
		StringParam("agent_id", "Optional concrete agent identifier", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		EnumParam("horizon", "Optional canopy horizon: turn, task, session, user, or project.", []string{
			string(forest.CanopyHorizonTurn),
			string(forest.CanopyHorizonSession),
			string(forest.CanopyHorizonTask),
			string(forest.CanopyHorizonUser),
			string(forest.CanopyHorizonProject),
		}, false).
		StringParam("cursor_id", "Optional forest cursor id from a prior turn or preload", false).
		IntParam("limit", "Maximum number of forest packets to return", false).
		BoolParam("include_counter_evidence", "Whether to include contradictory evidence in the returned packets (retrieval purposes only; ignored for predict purposes)", false)

	usageParts := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec.Usage != "" {
			usageParts = append(usageParts, fmt.Sprintf("purpose=%s: %s", forestConsultPurposeForSpec(spec), spec.Usage))
		}
	}
	if len(usageParts) > 0 {
		b = b.Usage(strings.Join(usageParts, " // "))
	}
	for _, spec := range specs {
		for _, practice := range spec.BestPractices {
			b = b.BestPractice(fmt.Sprintf("[purpose=%s] %s", forestConsultPurposeForSpec(spec), practice))
		}
	}

	return b.Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
		if deps == nil || deps.Forest == nil {
			return nil, fmt.Errorf("forest is not configured")
		}

		var params struct {
			Purpose                string `json:"purpose"`
			Query                  string `json:"query"`
			SessionID              string `json:"session_id,omitempty"`
			TaskID                 string `json:"task_id,omitempty"`
			AgentID                string `json:"agent_id,omitempty"`
			IntentID               string `json:"intent_id,omitempty"`
			CursorID               string `json:"cursor_id,omitempty"`
			Horizon                string `json:"horizon,omitempty"`
			Limit                  int    `json:"limit,omitempty"`
			IncludeCounterEvidence *bool  `json:"include_counter_evidence,omitempty"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}
		purpose := strings.TrimSpace(params.Purpose)
		if purpose == "" {
			return nil, fmt.Errorf("purpose is required (expected one of: %s)", strings.Join(purposeEnum, ", "))
		}
		spec, ok := specByPurpose[purpose]
		if !ok {
			return nil, fmt.Errorf("unknown purpose %q (expected one of: %s)", params.Purpose, strings.Join(purposeEnum, ", "))
		}
		if strings.TrimSpace(params.Query) == "" {
			return nil, fmt.Errorf("query is required")
		}
		sessionID, taskID := resolveForestSkillScope(ctx, params.SessionID, params.TaskID)

		horizon, err := resolveForestSkillHorizon(params.Horizon, sessionID, taskID)
		if err != nil {
			return nil, err
		}

		query := forest.Query{
			Query:                  strings.TrimSpace(params.Query),
			SessionID:              sessionID,
			TaskID:                 taskID,
			AgentID:                strings.TrimSpace(params.AgentID),
			AgentType:              spec.Domain,
			IntentID:               strings.TrimSpace(params.IntentID),
			Horizon:                horizon,
			Limit:                  resolveRoleForestLimit(params.Limit, spec.DefaultLimit),
			Kinds:                  append([]forest.ForestNodeKind(nil), spec.Kinds...),
			IncludeCounterEvidence: resolveRoleCounterEvidence(params.IncludeCounterEvidence, spec.IncludeCounterEvidence),
		}
		intentInput := forest.ResolveIntentInput{
			Query:     query.Query,
			SessionID: query.SessionID,
			TaskID:    query.TaskID,
			AgentID:   query.AgentID,
			AgentType: spec.Domain,
			IntentID:  query.IntentID,
			Limit:     query.Limit,
			Horizon:   query.Horizon,
		}

		intent, err := deps.Forest.ResolveIntent(ctx, intentInput)
		if err != nil {
			return nil, err
		}
		packets, err := deps.Forest.RetrieveForest(ctx, query)
		if err != nil {
			return nil, err
		}
		cursor, err := deps.Forest.CreateForestCursor(ctx, forest.ForestCursorInput{
			SessionID: query.SessionID,
			TaskID:    query.TaskID,
			AgentID:   query.AgentID,
			TurnID:    strings.TrimSpace(params.CursorID),
			Packets:   packets,
			Limit:     query.Limit,
		})
		if err != nil {
			return nil, err
		}
		projection, err := forest.BuildRoleForestProjection(spec.Domain, packets, cursor, query.Limit)
		if err != nil {
			return nil, err
		}

		// Tier 5: emit ActionForestConsultEmitted so downstream
		// activities within this call stack can declare causation
		// back to this consult. The forest bridge joins the
		// resulting outcomes to the consult record during harvest.
		consultID := emitForestConsultActivity(ctx, spec, purpose, query, intent, packets, cursor)

		return &ForestRoleOutput{
			Role:              spec.Domain,
			Intent:            intent,
			Packets:           packets,
			Cursor:            cursor,
			Projection:        projection,
			Focus:             buildRoleForestFocus(intent, packets),
			ConsultActivityID: consultID,
		}, nil
	}).
		Build()
}

// NewRoleForestSkill creates a role-appropriate forest skill from a declarative spec.
func NewRoleForestSkill(deps *RetrievalDependencies, spec forestRoleSkillSpec) *skills.Skill {
	b := skills.NewSkill(spec.Name).
		Description(spec.Description).
		Domain(spec.Domain).
		Keywords(spec.Keywords...).
		Priority(95).
		StringParam("query", firstNonEmpty(spec.QueryDescription, "Natural language description of the current task or concern"), true).
		StringParam("session_id", "Optional session identifier for session-scoped retrieval", false).
		StringParam("task_id", "Optional task identifier for task-scoped retrieval", false).
		StringParam("agent_id", "Optional concrete agent identifier", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		EnumParam("horizon", "Optional canopy horizon: turn, task, session, user, or project.", []string{
			string(forest.CanopyHorizonTurn),
			string(forest.CanopyHorizonSession),
			string(forest.CanopyHorizonTask),
			string(forest.CanopyHorizonUser),
			string(forest.CanopyHorizonProject),
		}, false).
		StringParam("cursor_id", "Optional forest cursor id from a prior turn or preload", false).
		IntParam("limit", "Maximum number of forest packets to return", false)
	if !spec.Predict {
		b = b.BoolParam("include_counter_evidence", "Whether to include contradictory evidence in the returned packets", false)
	}
	if spec.Usage != "" {
		b = b.Usage(spec.Usage)
	}
	for _, practice := range spec.BestPractices {
		b = b.BestPractice(practice)
	}
	return b.Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
		if deps == nil || deps.Forest == nil {
			return nil, fmt.Errorf("forest is not configured")
		}

		var params ForestRoleInput
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}
		if strings.TrimSpace(params.Query) == "" {
			return nil, fmt.Errorf("query is required")
		}
		sessionID, taskID := resolveForestSkillScope(ctx, params.SessionID, params.TaskID)

		horizon, err := resolveForestSkillHorizon(params.Horizon, sessionID, taskID)
		if err != nil {
			return nil, err
		}

		query := forest.Query{
			Query:                  strings.TrimSpace(params.Query),
			SessionID:              sessionID,
			TaskID:                 taskID,
			AgentID:                strings.TrimSpace(params.AgentID),
			AgentType:              spec.Domain,
			IntentID:               strings.TrimSpace(params.IntentID),
			Horizon:                horizon,
			Limit:                  resolveRoleForestLimit(params.Limit, spec.DefaultLimit),
			Kinds:                  append([]forest.ForestNodeKind(nil), spec.Kinds...),
			IncludeCounterEvidence: resolveRoleCounterEvidence(params.IncludeCounterEvidence, spec.IncludeCounterEvidence),
		}
		intentInput := forest.ResolveIntentInput{
			Query:     query.Query,
			SessionID: query.SessionID,
			TaskID:    query.TaskID,
			AgentID:   query.AgentID,
			AgentType: spec.Domain,
			IntentID:  query.IntentID,
			Limit:     query.Limit,
			Horizon:   query.Horizon,
		}

		intent, err := deps.Forest.ResolveIntent(ctx, intentInput)
		if err != nil {
			return nil, err
		}
		packets, err := deps.Forest.RetrieveForest(ctx, query)
		if err != nil {
			return nil, err
		}
		cursor, err := deps.Forest.CreateForestCursor(ctx, forest.ForestCursorInput{
			SessionID: query.SessionID,
			TaskID:    query.TaskID,
			AgentID:   query.AgentID,
			TurnID:    strings.TrimSpace(params.CursorID),
			Packets:   packets,
			Limit:     query.Limit,
		})
		if err != nil {
			return nil, err
		}
		projection, err := forest.BuildRoleForestProjection(spec.Domain, packets, cursor, query.Limit)
		if err != nil {
			return nil, err
		}

		// Tier 5: forest consult emission for causation threading.
		// See the collapsed-skill handler above for rationale.
		// The single-purpose per-role skill uses the spec's default
		// purpose string since the handler params don't carry one.
		consultID := emitForestConsultActivity(ctx, spec, defaultPurposeForSpec(spec), query, intent, packets, cursor)

		return &ForestRoleOutput{
			Role:              spec.Domain,
			Intent:            intent,
			Packets:           packets,
			Cursor:            cursor,
			Projection:        projection,
			Focus:             buildRoleForestFocus(intent, packets),
			ConsultActivityID: consultID,
		}, nil
	}).
		Build()
}

// defaultPurposeForSpec returns a stable "purpose" string for the
// per-role forest skill variant. Single-purpose skills don't accept
// a purpose parameter, so we derive one from the spec name to keep
// the emitted ActionForestConsultEmitted payload consistent with
// the collapsed-skill path.
func defaultPurposeForSpec(spec forestRoleSkillSpec) string {
	if idx := strings.LastIndex(spec.Name, "forest_"); idx >= 0 {
		trimmed := strings.TrimPrefix(spec.Name[idx:], "forest_")
		if trimmed != "" {
			return trimmed
		}
	}
	return spec.Name
}

// emitForestConsultActivity records the fact of a forest consult
// into the fabric. Payload captures purpose, query, node IDs, cursor, and
// intent confidence so the forest bridge's consume/resolve harvest
// can join subsequent outcomes back to the consult that shaped
// them. Returns the emitted activity ID so the handler can include
// it in the skill output.
func emitForestConsultActivity(
	ctx context.Context,
	spec forestRoleSkillSpec,
	purpose string,
	query forest.Query,
	intent *forest.IntentResolution,
	packets []*forest.ForestPacket,
	cursor *forest.ForestCursor,
) string {
	actorID := strings.TrimSpace(query.AgentID)
	if actorID == "" {
		actorID = activity.FabricContextFromContext(ctx).Baggage["agent_id"]
	}
	actor := activity.Actor{
		AgentID:   actorID,
		AgentType: spec.Domain,
	}

	nodeIDs := make([]string, 0, len(packets))
	for _, p := range packets {
		if p == nil || p.Node.ID == "" {
			continue
		}
		nodeIDs = append(nodeIDs, p.Node.ID)
	}

	payload := map[string]any{
		"purpose":  purpose,
		"query":    query.Query,
		"horizon":  query.Horizon,
		"limit":    query.Limit,
		"node_ids": nodeIDs,
	}
	if cursor != nil {
		payload["cursor_id"] = cursor.ID
		payload["active_cluster_ids"] = cursor.ActiveClusterIDs
		payload["bridge_refs"] = cursor.BridgeRefs
		payload["risk_flags"] = cursor.RiskFlags
	}
	if intent != nil {
		if intent.PrimaryIntent != "" {
			payload["primary_intent"] = intent.PrimaryIntent
		}
		if len(intent.ActiveRoots) > 0 {
			payload["active_roots"] = intent.ActiveRoots
		}
	}
	payloadJSON, _ := json.Marshal(payload)

	id := activity.ActivityID(activity.NewID())
	a := activity.AgentActivity{
		ID:         id,
		SessionID:  activity.SessionID(string(query.SessionID)),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionForestConsultEmitted),
		Actor:      actor,
		Action:     activity.ActionForestConsultEmitted,
		Subject: activity.Subject{
			Domain: spec.Domain,
			Coordinates: map[string]string{
				"purpose": purpose,
			},
		},
		Payload: payloadJSON,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, a)

	// Tier 5: register the consult in the process-wide tracker so
	// subsequent emissions by the same (sessionID, agentID) can
	// auto-populate their Caused pointer back to this consult. The
	// tracker has a bounded TTL; stale entries do not cross-link.
	if actor.AgentID != "" {
		activity.RecordForestConsult(string(query.SessionID), actor.AgentID, id)
	}
	return string(id)
}

func supplementalDomainsForAgent(agentType string) []string {
	agentType = NormalizeAdaptiveAgentType(agentType)
	var domains []string
	if strings.HasPrefix(agentType, AgentTypeScribe) {
		domains = append(domains, AgentTypeScribe)
	}
	if isPipelineScopedAgent(agentType) {
		domains = append(domains, AgentTypePipeline)
	}
	return domains
}

func isPipelineScopedAgent(agentType string) bool {
	agentType = NormalizeAdaptiveAgentType(agentType)
	switch agentType {
	case AgentTypePipeline, AgentTypeEngineer, AgentTypeDesigner, AgentTypeInspector, AgentTypeTester:
		return true
	default:
		return false
	}
}

func resolveRoleForestLimit(input, fallback int) int {
	if input > 0 {
		return input
	}
	if fallback > 0 {
		return fallback
	}
	return defaultRoleForestLimit
}

func resolveRoleCounterEvidence(input *bool, fallback bool) bool {
	if input != nil {
		return *input
	}
	return fallback
}

func buildRoleForestFocus(intent *forest.IntentResolution, packets []*forest.ForestPacket) []string {
	seen := map[string]struct{}{}
	focus := make([]string, 0, 5)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		focus = append(focus, value)
	}

	if intent != nil {
		add("Primary intent: " + intent.PrimaryIntent)
		if len(intent.Constraints) > 0 && intent.Constraints[0] != nil {
			add("Constraint: " + firstNonEmpty(intent.Constraints[0].Node.Summary, intent.Constraints[0].Node.Title))
		}
		if len(intent.Preferences) > 0 && intent.Preferences[0] != nil {
			add("Preference: " + firstNonEmpty(intent.Preferences[0].Node.Summary, intent.Preferences[0].Node.Title))
		}
	}
	if len(packets) > 0 {
		add("Top node: " + firstNonEmpty(packets[0].Node.Summary, packets[0].Node.Title))
		if len(packets[0].CounterEvidence) > 0 {
			add("Watch for: " + packets[0].CounterEvidence[0].Summary)
		}
		if len(packets[0].ProposedClaims) > 0 {
			add("Proposed claim: " + packets[0].ProposedClaims[0].Summary)
		}
	}
	if len(focus) > 5 {
		focus = focus[:5]
	}
	return focus
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
