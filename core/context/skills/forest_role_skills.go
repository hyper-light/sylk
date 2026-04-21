package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

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
	Horizon                string `json:"horizon,omitempty"`
	Limit                  int    `json:"limit,omitempty"`
	IncludeCounterEvidence *bool  `json:"include_counter_evidence,omitempty"`
}

// ForestRoleOutput packages intent and retrieval results for role-specific skills.
type ForestRoleOutput struct {
	Role    string                   `json:"role"`
	Intent  *forest.IntentResolution `json:"intent,omitempty"`
	Packets []*forest.BranchPacket   `json:"packets,omitempty"`
	Focus   []string                 `json:"focus,omitempty"`
}

type forestRoleSkillSpec struct {
	Name                   string
	Domain                 string
	Description            string
	Keywords               []string
	QueryDescription       string
	Families               []forest.TreeFamily
	DefaultLimit           int
	IncludeCounterEvidence bool
	Predict                bool
	Usage                  string
	BestPractices          []string
}

var forestRoleSkillSpecs = []forestRoleSkillSpec{
	{
		Name:             "architect_forest_get_plan_precedents",
		Domain:           AgentTypeArchitect,
		Description:      "Recall prior plan branches, constraints, evidence, and outcomes relevant to the current architectural direction.",
		Keywords:         []string{"architect", "plan", "precedent", "constraint", "outcome"},
		QueryDescription: "Architectural plan, decomposition, or design direction to ground in precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyDecision,
			forest.TreeFamilyCapability,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyEvidence,
			forest.TreeFamilyConstraint,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
		Usage:                  "Use before finalizing a plan, major decomposition, or architectural trade-off so the architect sees prior successful and failed branches.",
		BestPractices: []string{
			"Prefer this when the plan could repeat a past failure or when the repo already has strong implementation precedent.",
		},
	},
	{
		Name:             "architect_forest_compare_plan_branches",
		Domain:           AgentTypeArchitect,
		Description:      "Predict low-risk adjacent plan branches that could improve the current architecture or decomposition.",
		Keywords:         []string{"architect", "compare", "plan", "branch", "adjacent"},
		QueryDescription: "Current architectural problem or decomposition to compare against adjacent branches",
		DefaultLimit:     5,
		Predict:          true,
		Usage:            "Use when the architect wants to check whether a nearby branch would better satisfy likely user intent without surprising scope expansion.",
	},
	{
		Name:             "academic_forest_get_authority_bundle",
		Domain:           AgentTypeAcademic,
		Description:      "Recall evidence, outcomes, and capability precedents that best support or bound the current research direction.",
		Keywords:         []string{"academic", "authority", "evidence", "precedent", "research"},
		QueryDescription: "Research question, proposal, or external claim to ground in authority-weighted precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyEvidence,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyCapability,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "academic_forest_check_contradictions",
		Domain:           AgentTypeAcademic,
		Description:      "Retrieve contradiction-heavy branches and constraints so academic work does not overstate weak or conflicting evidence.",
		Keywords:         []string{"academic", "contradiction", "counterevidence", "constraint"},
		QueryDescription: "Claim or proposal that needs contradiction and counterevidence review",
		Families: []forest.TreeFamily{
			forest.TreeFamilyConflict,
			forest.TreeFamilyEvidence,
			forest.TreeFamilyConstraint,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "librarian_forest_get_code_precedents",
		Domain:           AgentTypeLibrarian,
		Description:      "Retrieve code-facing precedents, implementation branches, and capability evidence for the current repo task.",
		Keywords:         []string{"librarian", "code", "precedent", "implementation", "pattern"},
		QueryDescription: "Code problem, implementation pattern, or touched-file concern to ground in repo precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyEvidence,
			forest.TreeFamilyDecision,
			forest.TreeFamilyCapability,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "librarian_forest_get_implementation_risks",
		Domain:           AgentTypeLibrarian,
		Description:      "Surface implementation risks, prior conflicts, and failure outcomes related to the current code path.",
		Keywords:         []string{"librarian", "risk", "implementation", "failure", "conflict"},
		QueryDescription: "Implementation area that may have hidden repo-local risks or conflict precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyConflict,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyConstraint,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "archivalist_forest_get_decision_precedents",
		Domain:           AgentTypeArchivalist,
		Description:      "Recall prior decisions, outcomes, and preference context relevant to the current problem.",
		Keywords:         []string{"archivalist", "decision", "precedent", "history", "outcome"},
		QueryDescription: "Problem or decision area that should be grounded in prior historical choices",
		Families: []forest.TreeFamily{
			forest.TreeFamilyDecision,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyPreference,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "archivalist_forest_get_failure_precedents",
		Domain:           AgentTypeArchivalist,
		Description:      "Retrieve past failed or mixed branches so current work avoids repeating historical mistakes.",
		Keywords:         []string{"archivalist", "failure", "precedent", "history", "conflict"},
		QueryDescription: "Failure mode, repeated issue, or risky area to compare against prior outcomes",
		Families: []forest.TreeFamily{
			forest.TreeFamilyOutcome,
			forest.TreeFamilyConflict,
			forest.TreeFamilyDecision,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "guide_forest_get_user_intent_history",
		Domain:           AgentTypeGuide,
		Description:      "Resolve the user’s active intent using prior intent, preference, and outcome branches instead of only the literal current prompt.",
		Keywords:         []string{"guide", "intent", "history", "preference", "outcome"},
		QueryDescription: "User request or confusion point that needs longitudinal intent context",
		Families: []forest.TreeFamily{
			forest.TreeFamilyIntent,
			forest.TreeFamilyPreference,
			forest.TreeFamilyOutcome,
		},
		DefaultLimit:           5,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "guide_forest_get_teaching_precedents",
		Domain:           AgentTypeGuide,
		Description:      "Retrieve evidence and successful explanation branches that can improve onboarding or guidance quality.",
		Keywords:         []string{"guide", "teaching", "example", "onboarding", "precedent"},
		QueryDescription: "Concept, tutorial need, or explanation path that should use prior successful teaching branches",
		Families: []forest.TreeFamily{
			forest.TreeFamilyEvidence,
			forest.TreeFamilyCapability,
			forest.TreeFamilyOutcome,
		},
		DefaultLimit:           5,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "orchestrator_forest_get_coordination_precedents",
		Domain:           AgentTypeOrchestrator,
		Description:      "Recall prior coordination branches, outcomes, and capability precedents for the current multi-agent situation.",
		Keywords:         []string{"orchestrator", "coordination", "precedent", "handoff", "workflow"},
		QueryDescription: "Coordination problem, workflow state, or multi-agent objective to ground in precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyCapability,
			forest.TreeFamilyDecision,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyConflict,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "orchestrator_forest_predict_handoff_path",
		Domain:           AgentTypeOrchestrator,
		Description:      "Predict low-risk adjacent branches that could improve coordination or handoff sequencing.",
		Keywords:         []string{"orchestrator", "predict", "handoff", "sequence", "adjacent"},
		QueryDescription: "Workflow or coordination problem that may have a better next handoff path",
		DefaultLimit:     5,
		Predict:          true,
	},
	{
		Name:             "engineer_forest_select_implementation_branch",
		Domain:           AgentTypeEngineer,
		Description:      "Retrieve the strongest implementation branches, constraints, and capability evidence for the current coding task.",
		Keywords:         []string{"engineer", "implementation", "branch", "constraint", "capability"},
		QueryDescription: "Implementation task or coding problem to ground in branch precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyDecision,
			forest.TreeFamilyCapability,
			forest.TreeFamilyEvidence,
			forest.TreeFamilyConstraint,
			forest.TreeFamilyOutcome,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
		Usage:                  "Use before editing when there are multiple plausible implementations or when prior regressions matter.",
	},
	{
		Name:             "engineer_forest_get_failure_precedents",
		Domain:           AgentTypeEngineer,
		Description:      "Retrieve prior failures, constraints, and conflict-heavy branches that could invalidate the current implementation path.",
		Keywords:         []string{"engineer", "failure", "regression", "constraint", "conflict"},
		QueryDescription: "Implementation area that may have hidden regression or failure precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyOutcome,
			forest.TreeFamilyConflict,
			forest.TreeFamilyConstraint,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "designer_forest_get_preference_prior",
		Domain:           AgentTypeDesigner,
		Description:      "Recall user preference, intent, and outcome precedent relevant to the current design choice.",
		Keywords:         []string{"designer", "preference", "intent", "style", "outcome"},
		QueryDescription: "Design problem or UX direction to ground in user preference precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyPreference,
			forest.TreeFamilyIntent,
			forest.TreeFamilyOutcome,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "designer_forest_discover_adjacent_value",
		Domain:           AgentTypeDesigner,
		Description:      "Predict low-risk adjacent design branches that could improve the work beyond literal compliance.",
		Keywords:         []string{"designer", "adjacent", "value", "opportunity", "predict"},
		QueryDescription: "Design task or UX problem to explore for safe adjacent value",
		DefaultLimit:     5,
		Predict:          true,
	},
	{
		Name:             "guardian_forest_evaluate_scope_risk",
		Domain:           AgentTypeGuardian,
		Description:      "Retrieve constraints, conflicts, decisions, and outcomes that indicate scope or policy risk in the current path.",
		Keywords:         []string{"guardian", "scope", "risk", "policy", "constraint"},
		QueryDescription: "Action or plan that should be checked for scope, policy, or authority risk",
		Families: []forest.TreeFamily{
			forest.TreeFamilyConstraint,
			forest.TreeFamilyConflict,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyDecision,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "guardian_forest_get_approval_precedents",
		Domain:           AgentTypeGuardian,
		Description:      "Recall prior approved or rejected branches relevant to the current governance decision.",
		Keywords:         []string{"guardian", "approval", "precedent", "governance", "decision"},
		QueryDescription: "Governance or approval decision that should be checked against precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyDecision,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyConstraint,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "scribe_forest_get_capture_targets",
		Domain:           AgentTypeScribe,
		Description:      "Retrieve the intent, decisions, outcomes, conflicts, and preferences a scribe should preserve from the current work.",
		Keywords:         []string{"scribe", "capture", "handoff", "summary", "decision"},
		QueryDescription: "Work in progress that the scribe should capture with the highest downstream value",
		Families: []forest.TreeFamily{
			forest.TreeFamilyIntent,
			forest.TreeFamilyDecision,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyConflict,
			forest.TreeFamilyPreference,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "scribe_forest_prepare_handoff_context",
		Domain:           AgentTypeScribe,
		Description:      "Retrieve the branch context most important to preserve for future handoff and replay.",
		Keywords:         []string{"scribe", "handoff", "replay", "context", "preserve"},
		QueryDescription: "Workstream or handoff target that needs compact high-signal forest context",
		Families: []forest.TreeFamily{
			forest.TreeFamilyDecision,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyPreference,
			forest.TreeFamilyCapability,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "inspector_forest_get_regression_precedents",
		Domain:           AgentTypeInspector,
		Description:      "Retrieve prior regressions, conflicts, and evidence bundles relevant to the current inspection target.",
		Keywords:         []string{"inspector", "regression", "precedent", "finding", "conflict"},
		QueryDescription: "Inspection target, suspected regression, or review concern to ground in precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyOutcome,
			forest.TreeFamilyConflict,
			forest.TreeFamilyEvidence,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "inspector_forest_get_validation_targets",
		Domain:           AgentTypeInspector,
		Description:      "Retrieve the constraints, decisions, outcomes, and evidence that should be validated by inspection.",
		Keywords:         []string{"inspector", "validation", "target", "constraint", "evidence"},
		QueryDescription: "Implementation or change set that needs inspection targets and validation context",
		Families: []forest.TreeFamily{
			forest.TreeFamilyConstraint,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyDecision,
			forest.TreeFamilyEvidence,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "tester_forest_get_test_targets",
		Domain:           AgentTypeTester,
		Description:      "Retrieve constraints, capabilities, outcomes, and evidence that should shape the current testing target set.",
		Keywords:         []string{"tester", "test", "target", "coverage", "constraint"},
		QueryDescription: "Implementation or behavior that needs a strong testing target set",
		Families: []forest.TreeFamily{
			forest.TreeFamilyConstraint,
			forest.TreeFamilyCapability,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyEvidence,
		},
		DefaultLimit:           6,
		IncludeCounterEvidence: true,
	},
	{
		Name:             "tester_forest_get_failure_clusters",
		Domain:           AgentTypeTester,
		Description:      "Retrieve clustered failure and conflict precedent to improve test selection and guard against repeated misses.",
		Keywords:         []string{"tester", "failure", "cluster", "regression", "conflict"},
		QueryDescription: "Failure mode or risky behavior that should be checked against clustered precedent",
		Families: []forest.TreeFamily{
			forest.TreeFamilyOutcome,
			forest.TreeFamilyConflict,
			forest.TreeFamilyEvidence,
		},
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
		"Memory Forest consultation for the %s role. Run a retrieval or prediction query against the per-role tree families, selected by the purpose enum. Collapses the historical per-purpose %s_forest_* skills into one primitive.\n\nPurposes:\n%s",
		role, role, purposeLines.String(),
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
		IntParam("limit", "Maximum number of branch packets to return", false).
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
			Families:               append([]forest.TreeFamily(nil), spec.Families...),
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

		var (
			intent   *forest.IntentResolution
			packets  []*forest.BranchPacket
			firstErr error
			mu       sync.Mutex
			wg       sync.WaitGroup
		)

		wg.Add(2)
		go func() {
			defer wg.Done()
			resolution, resolveErr := deps.Forest.ResolveIntent(ctx, intentInput)
			mu.Lock()
			defer mu.Unlock()
			if resolveErr != nil && firstErr == nil {
				firstErr = resolveErr
				return
			}
			intent = resolution
		}()
		go func() {
			defer wg.Done()
			var packetsErr error
			if spec.Predict {
				packets, packetsErr = deps.Forest.PredictNextBranches(ctx, query)
			} else {
				packets, packetsErr = deps.Forest.Retrieve(ctx, query)
			}
			mu.Lock()
			defer mu.Unlock()
			if packetsErr != nil && firstErr == nil {
				firstErr = packetsErr
			}
		}()
		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}

		return &ForestRoleOutput{
			Role:    spec.Domain,
			Intent:  intent,
			Packets: packets,
			Focus:   buildRoleForestFocus(intent, packets),
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
		IntParam("limit", "Maximum number of branch packets to return", false)
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
			Families:               append([]forest.TreeFamily(nil), spec.Families...),
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

		var (
			intent   *forest.IntentResolution
			packets  []*forest.BranchPacket
			firstErr error
			mu       sync.Mutex
			wg       sync.WaitGroup
		)

		wg.Add(2)
		go func() {
			defer wg.Done()
			resolution, resolveErr := deps.Forest.ResolveIntent(ctx, intentInput)
			mu.Lock()
			defer mu.Unlock()
			if resolveErr != nil && firstErr == nil {
				firstErr = resolveErr
				return
			}
			intent = resolution
		}()
		go func() {
			defer wg.Done()
			var packetsErr error
			if spec.Predict {
				packets, packetsErr = deps.Forest.PredictNextBranches(ctx, query)
			} else {
				packets, packetsErr = deps.Forest.Retrieve(ctx, query)
			}
			mu.Lock()
			defer mu.Unlock()
			if packetsErr != nil && firstErr == nil {
				firstErr = packetsErr
			}
		}()
		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}

		return &ForestRoleOutput{
			Role:    spec.Domain,
			Intent:  intent,
			Packets: packets,
			Focus:   buildRoleForestFocus(intent, packets),
		}, nil
	}).
		Build()
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

func buildRoleForestFocus(intent *forest.IntentResolution, packets []*forest.BranchPacket) []string {
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
		if len(intent.Constraints) > 0 && intent.Constraints[0].Branch != nil {
			add("Constraint: " + intent.Constraints[0].Branch.Summary)
		}
		if len(intent.Preferences) > 0 && intent.Preferences[0].Branch != nil {
			add("Preference: " + intent.Preferences[0].Branch.Summary)
		}
	}
	if len(packets) > 0 {
		if packets[0].Branch != nil {
			add("Top branch: " + packets[0].Branch.Summary)
		}
		if len(packets[0].Conflicts) > 0 {
			add("Watch for: " + packets[0].Conflicts[0].Summary)
		}
		if len(packets[0].NextActions) > 0 {
			add("Next action: " + packets[0].NextActions[0].Description)
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
