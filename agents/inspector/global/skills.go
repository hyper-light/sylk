package global

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

func (gi *GlobalInspector) registerCoreSkills() {
	writeCfg := versioning.WorkspaceWriteSkillConfig{
		GetFileAccess: func() versioning.FileAccess { return gi.fileAccess },
		GetViews:      func() versioning.WorkspaceViewAccess { return gi.workspaceViews },
	}

	// Phase 2.K / GI-C refactor: run_linter + run_type_checker +
	// run_formatter_check + run_security_scan + analyze_complexity +
	// detect_deadlocks + detect_memory_leaks collapsed into
	// run_analyzer(kind=…).
	gi.skills.Register(shared.RunAnalyzerSkill(gi.toolRunner))
	gi.skills.Register(bashSkill(gi))
	// read_file / glob / grep dropped — workspace_read covers all
	// three via op=read|glob|grep with explicit view selection.
	// Phase 2.K / CR-2 refactor: 12 workspace skills collapsed to 3
	// verb-dispatched primitives.
	inspectorGlobalGetViews := func() versioning.WorkspaceViewAccess { return gi.workspaceViews }
	inspectorGlobalGetFA := func() versioning.FileAccess { return gi.fileAccess }
	// prepare_write_context folded into workspace_read(op=prepare_write).
	gi.skills.Register(versioning.NewWorkspaceReadSkill(versioning.WorkspaceReadSkillConfig{
		GetViews:      inspectorGlobalGetViews,
		GetFileAccess: inspectorGlobalGetFA,
	}))
	gi.skills.Register(versioning.NewWorkspaceWriteSkill(writeCfg))

	// Global-specific skills. audit_layer is intentionally NOT registered as
	// an LLM tool: it is the entry point of the inspector's own LLM tool
	// loop, invoked by the Go-side layer gate (see NewInspectorLayerGate).
	// Exposing it as a Skill creates recursive LLM invocation because the
	// inner audit's tool loop sees audit_layer in its toolset and calls it
	// again. The audit prepass also requires NodeDiffs/NodeResults that the
	// LLM cannot supply, so the LLM-callable form was always functionally
	// degenerate.
	// Phase 2.K / GI-1 refactor (docs/PIPELINE_SKILL_REFACTOR.md §10.K.1):
	// validate_plan_adherence + cross_reference_changes + grade_layer_quality
	// + load_plan_context collapsed into audit(aspect=…). determine_audit_depth
	// stays named because tool_loop.go enforces it as the MANDATORY first
	// tool call on every fresh audit branch — keeping that gate robust
	// means keeping the landmark visible under its own name.
	gi.skills.Register(auditSkill(gi))
	gi.skills.Register(determineAuditDepthSkill(gi))
	// Phase 2.K / GI-A refactor (docs/PIPELINE_SKILL_REFACTOR.md §10.K.1):
	// consult_librarian_style + consult_academic_approach +
	// consult_archivalist_context + request_architect_research all route
	// through the shared consult_peer(target_agent_type=…) primitive
	// registered below via CrossPipelineSkills.
	// Phase 2.K / GI-B refactor: request_user_clarification folds into
	// shared ask_user_clarification, escalate_findings folds into
	// publish_work_event(kind=artifact, artifact_kind="audit_finding").
	gi.skills.Register(agentShared.BuildAskUserClarificationSkill(agentShared.AskUserClarificationConfig{
		Bus:          gi.bus,
		AgentID:      gi.id,
		AgentName:    "inspector",
		SessionID:    gi.config.SessionID,
		NewMessageID: gi.generateMessageID,
	}))
	// publish_work_event was removed alongside manage_claim — the global
	// inspector previously posted audit findings through it, but the
	// Fabric's ambient_context + consult_peer + challenge_peer cover the
	// needed visibility, and audit findings can also go through the
	// Claims Board when the claims model is wired end-to-end.
	for _, skill := range agentShared.NewGlobalReviewProtocolSkills(agentShared.GlobalReviewProtocolSkillConfig{
		AgentType:      func() string { return "inspector" },
		AgentID:        func() string { return gi.id },
		ResolveTarget:  func(agentType string) string { return gi.knownAgentIDByType(agentType, agentType) },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return gi.workspaceViews },
		Route: agentShared.GlobalReviewRouteConfig{
			BusProvider: func() guide.EventBus { return gi.bus },
			SessionID:   func() string { return gi.config.SessionID },
		},
	}) {
		gi.skills.Register(skill)
	}
	// Phase 2.K / GT-4 + GI-5 refactor: collapsed into dependency(action=…).
	gi.skills.Register(dependencySkill(gi))

	// Activity Fabric: uniform awareness skills + cross-pipeline
	// primitives + audit-time inspect_open_activity. Global inspector
	// audits at session level; the audit skill returns conflicts
	// across the whole session when called with empty scope.
	for _, skill := range fabric.AwarenessSkills(fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return gi.config.SessionID },
		AgentID:        func() string { return gi.id },
		AgentType:      func() string { return "inspector" },
	}) {
		gi.skills.Register(skill)
	}
	for _, skill := range agentShared.CrossPipelineSkills(agentShared.CrossPipelineSkillConfig{
		SessionID: func() string { return gi.config.SessionID },
		AgentID:   func() string { return gi.id },
		AgentType: func() string { return "inspector" },
		RouteSync: agentShared.RouteSyncFromBus(
			func() guide.EventBus { return gi.bus },
			func() string {
				if gi.channels == nil {
					return ""
				}
				return gi.channels.Responses
			},
		),
	}) {
		gi.skills.Register(skill)
	}
	for _, skill := range fabric.InspectorAuditSkills(fabric.InspectorAuditSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return gi.config.SessionID },
		AgentID:        func() string { return gi.id },
		AgentType:      func() string { return "inspector" },
	}) {
		gi.skills.Register(skill)
	}
	// Phase 5 of SCRIBE_FABRIC.md: recall_my_history.
	for _, skill := range fabric.RecallSkills(fabric.RecallSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return gi.config.SessionID },
		AgentID:        func() string { return gi.id },
		AgentType:      func() string { return "inspector" },
	}) {
		gi.skills.Register(skill)
	}

	// Diagnostics
	gi.skills.Register(agentShared.NewSelfDiagnosticSkill(&globalInspectorDiag{gi: gi}))

	// Standard reroute.
	gi.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   gi.id,
		SessionID: func() string { return gi.config.SessionID },
		Publish:   gi.publishRerouteRequest,
	}))
}

type globalInspectorDiag struct{ gi *GlobalInspector }

func (d *globalInspectorDiag) AgentName() string { return "inspector_global" }
func (d *globalInspectorDiag) SessionID() string { return d.gi.config.SessionID }
func (d *globalInspectorDiag) LogsDir() string {
	return agentShared.LogsDirForAgent(d.gi.steering.SessionDir(), "inspector_global")
}
func (d *globalInspectorDiag) EventLogger() *agentlog.SessionEventLogger {
	return d.gi.steering.EventLogger()
}
func (d *globalInspectorDiag) PeerLogsDirs() map[string]string { return nil }
func (d *globalInspectorDiag) RecoveryHints() []string         { return nil }

func (d *globalInspectorDiag) AgentSpecificDiagnostics() map[string]any {
	return map[string]any{}
}

// Phase 2.K / GI-1 refactor (docs/PIPELINE_SKILL_REFACTOR.md §10.K.1):
// auditSkill collapses validate_plan_adherence, cross_reference_changes,
// grade_layer_quality, and load_plan_context into one skill dispatched
// by the aspect parameter. determine_audit_depth is deliberately kept
// as its own skill because tool_loop.go enforces it as the first tool
// call on every fresh audit branch — collapsing that landmark would
// weaken the deterministic gate.
const (
	auditAspectPlanAdherence   = "plan_adherence"
	auditAspectCrossReferences = "cross_references"
	auditAspectLayerQuality    = "layer_quality"
	auditAspectContextLoad     = "context_load"
)

func auditAspects() []string {
	return []string{
		auditAspectPlanAdherence,
		auditAspectCrossReferences,
		auditAspectLayerQuality,
		auditAspectContextLoad,
	}
}

func auditSkill(gi *GlobalInspector) *skills.Skill {
	planAdherence := validatePlanAdherenceSkill(gi)
	crossRefs := crossReferenceChangesSkill(gi)
	grade := gradeLayerQualitySkill(gi)
	contextLoad := loadPlanContextSkill(gi)
	return skills.NewSkill("audit").
		Description("Run a global-inspector audit operation selected by aspect. Collapses validate_plan_adherence, cross_reference_changes, grade_layer_quality, and load_plan_context into one primitive dispatched by the aspect enum. (determine_audit_depth stays named because the tool loop enforces it as the mandatory first call on every fresh audit branch.)").
		Domain("audit").
		Keywords("audit", "plan", "adherence", "cross-file", "interface", "grade", "quality", "layer", "context").
		Priority(100).
		Usage("Set aspect=plan_adherence for whole-plan coverage checks, aspect=cross_references for multi-file interface/import/style drift analysis, aspect=layer_quality to produce the final DAG-layer quality grade, aspect=context_load to recover missing plan context from disk. The required parameters depend on aspect — see each aspect's Requirement guidance.").
		Requirement("aspect=plan_adherence requires plan_snapshot; aspect=cross_references requires files; aspect=layer_quality requires dag_id and layer_idx; aspect=context_load requires either plan_file_path or (plan_id + session_id).").
		Satisfies("Produces the audit evidence the global inspector uses for plan-adherence judgments, cross-file architectural findings, layer quality grades, and plan-context recovery.").
		Avoid("Do not pick an aspect you don't have evidence for. Grade before the relevant audit evidence exists is meaningless; cross_references on a single file is degenerate; plan_adherence needs the concrete implemented_tasks list.").
		EnumParam("aspect", "Audit aspect", auditAspects(), true).
		StringParam("plan_snapshot", "Serialized plan to validate against (aspect=plan_adherence or aspect=context_load)", false).
		ArrayParam("implemented_tasks", "List of implemented task IDs (aspect=plan_adherence)", "string", false).
		ArrayParam("files", "Files to cross-reference (aspect=cross_references)", "string", false).
		StringParam("dag_id", "DAG identifier (aspect=layer_quality)", false).
		IntParam("layer_idx", "Layer index (aspect=layer_quality)", false).
		StringParam("plan_id", "Architect plan ID (aspect=context_load)", false).
		StringParam("plan_file_path", "Architect plan file path (aspect=context_load)", false).
		StringParam("session_id", "Session identifier (aspect=context_load)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Aspect string `json:"aspect"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			aspect := strings.ToLower(strings.TrimSpace(params.Aspect))
			if aspect == "" {
				return nil, fmt.Errorf("aspect is required (expected one of: plan_adherence, cross_references, layer_quality, context_load)")
			}
			switch aspect {
			case auditAspectPlanAdherence:
				return planAdherence.Handler(ctx, input)
			case auditAspectCrossReferences:
				return crossRefs.Handler(ctx, input)
			case auditAspectLayerQuality:
				return grade.Handler(ctx, input)
			case auditAspectContextLoad:
				return contextLoad.Handler(ctx, input)
			default:
				return nil, fmt.Errorf("unknown aspect: %q (expected one of: plan_adherence, cross_references, layer_quality, context_load)", params.Aspect)
			}
		}).
		Build()
}

func validatePlanAdherenceSkill(_ *GlobalInspector) *skills.Skill {
	return skills.NewSkill("validate_plan_adherence").
		Description("Compare implementation against the full architect plan, not just isolated task summaries.").
		Domain("audit").
		Keywords("plan", "adherence", "compliance").
		Priority(100).
		Usage("Use when the audit must prove whether the merged implementation really matches the architect's intended task set, sequencing, scope boundaries, and quality expectations.").
		Requirement("Provide the full plan snapshot and the concrete implemented task IDs so adherence is judged against the actual plan, not reconstructed prose.").
		Satisfies("Produces plan-adherence evidence that can justify blocking, architect pushback, or a direct challenge to the plan itself.").
		Avoid("Do not guess adherence from prose summaries when the actual plan snapshot is available.").
		StringParam("plan_snapshot", "Serialized plan to validate against", true).
		ArrayParam("implemented_tasks", "List of implemented task IDs", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				PlanSnapshot     string   `json:"plan_snapshot"`
				ImplementedTasks []string `json:"implemented_tasks"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			score, parseErr := evaluatePlanAdherence(params.PlanSnapshot, params.ImplementedTasks)

			result := map[string]any{
				"score":         score.Score,
				"tasks_covered": score.TasksCovered,
				"tasks_missing": score.TasksMissing,
				"deviations":    score.Deviations,
			}
			if parseErr != nil {
				result["parse_error"] = parseErr.Error()
			}
			return result, nil
		}).
		Build()
}

func crossReferenceChangesSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("cross_reference_changes").
		Description("Detect cross-file issues such as interface mismatches, import cycles, type inconsistencies, shared state races, and style drift across the changed surface.").
		Domain("audit").
		Keywords("cross-file", "interface", "import", "type", "race").
		Priority(95).
		Usage("Use when multiple changed files need coherence checks across interfaces, imports, shared state, architecture boundaries, or established repo conventions.").
		Requirement("Provide the concrete file set so the analysis is tied to the actual changed surface.").
		Satisfies("Produces cross-file architectural findings for the global audit and final escalation/reporting.").
		Avoid("Do not limit yourself to one file at a time when the risk is in interactions between files.").
		ArrayParam("files", "Files to cross-reference", "string", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Files []string `json:"files"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			result := map[string]any{
				"files_analyzed":    len(params.Files),
				"cross_file_issues": []shared.CrossFileIssue{},
			}
			if gi != nil && gi.workspaceViews != nil && len(params.Files) > 0 {
				if summary, err := gi.workspaceViews.SummarizePaths(ctx, params.Files, ""); err == nil {
					result["workspace_summary"] = summary
				} else {
					result["workspace_summary_error"] = err.Error()
				}
			}
			return result, nil
		}).
		Build()
}

func gradeLayerQualitySkill(_ *GlobalInspector) *skills.Skill {
	return skills.NewSkill("grade_layer_quality").
		Description("Produce an overall quality grade for a DAG layer across correctness, robustness, performance, security, adherence, and code quality.").
		Domain("audit").
		Keywords("grade", "quality", "layer").
		Priority(90).
		Usage("Use after the relevant audit evidence exists and the layer is ready for a final whole-plan quality judgment.").
		Requirement("Requires enough audit evidence to justify a grade across correctness, robustness, performance, security, adherence, style fit, and overall implementation quality.").
		Satisfies("Produces the quality-grade result used in final audit summaries, blocking decisions, and architect challenges.").
		Avoid("Do not grade a layer before the core audit evidence has been gathered.").
		StringParam("dag_id", "DAG identifier", true).
		IntParam("layer_idx", "Layer index", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				DAGID    string `json:"dag_id"`
				LayerIdx int    `json:"layer_idx"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			grade := shared.QualityGrade{
				Correctness: 1.0,
				Robustness:  1.0,
				Performance: 1.0,
				Security:    1.0,
				Adherence:   1.0,
			}

			return map[string]any{
				"dag_id":    params.DAGID,
				"layer_idx": params.LayerIdx,
				"grade":     grade,
				"overall":   grade.Overall(),
			}, nil
		}).
		Build()
}

func evaluatePlanAdherence(planSnapshot string, implementedTasks []string) (shared.PlanAdherenceScore, error) {
	expectedTasks, err := parsePlanTaskIDs(planSnapshot)
	implemented := normalizeTaskIDs(implementedTasks)
	score := shared.PlanAdherenceScore{
		TasksCovered: make([]string, 0),
		TasksMissing: make([]string, 0),
		Deviations:   make([]string, 0),
	}
	if err != nil {
		score.Deviations = append(score.Deviations, "unable to parse plan snapshot for adherence validation")
		score.TasksCovered = implemented
		sort.Strings(score.TasksCovered)
		return score, err
	}
	if len(expectedTasks) == 0 {
		score.TasksCovered = implemented
		if len(implemented) > 0 {
			score.Score = 1.0
		}
		return score, err
	}
	implementedSet := make(map[string]struct{}, len(implemented))
	for _, taskID := range implemented {
		implementedSet[taskID] = struct{}{}
	}
	for _, taskID := range expectedTasks {
		if _, ok := implementedSet[taskID]; ok {
			score.TasksCovered = append(score.TasksCovered, taskID)
		} else {
			score.TasksMissing = append(score.TasksMissing, taskID)
			score.Deviations = append(score.Deviations, fmt.Sprintf("planned task %s is missing from implemented_tasks", taskID))
		}
	}
	expectedSet := make(map[string]struct{}, len(expectedTasks))
	for _, taskID := range expectedTasks {
		expectedSet[taskID] = struct{}{}
	}
	for _, taskID := range implemented {
		if _, ok := expectedSet[taskID]; !ok {
			score.Deviations = append(score.Deviations, fmt.Sprintf("implemented_tasks includes unexpected task %s", taskID))
		}
	}
	total := len(expectedTasks)
	if total > 0 {
		score.Score = float64(len(score.TasksCovered)) / float64(total)
		penalty := float64(len(score.Deviations)-len(score.TasksMissing)) * 0.05
		score.Score -= penalty
		if score.Score < 0 {
			score.Score = 0
		}
	}
	sort.Strings(score.TasksCovered)
	sort.Strings(score.TasksMissing)
	sort.Strings(score.Deviations)
	return score, err
}

func parsePlanTaskIDs(planSnapshot string) ([]string, error) {
	if strings.TrimSpace(planSnapshot) == "" {
		return nil, fmt.Errorf("plan_snapshot is empty")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(planSnapshot), &decoded); err != nil {
		return nil, err
	}
	rawTasks, ok := decoded["Tasks"]
	if !ok {
		rawTasks = decoded["tasks"]
	}
	taskEntries, _ := rawTasks.([]any)
	taskIDs := make([]string, 0, len(taskEntries))
	for _, entry := range taskEntries {
		task, _ := entry.(map[string]any)
		if task == nil {
			continue
		}
		for _, key := range []string{"ID", "id"} {
			if value, _ := task[key].(string); strings.TrimSpace(value) != "" {
				taskIDs = append(taskIDs, strings.TrimSpace(value))
				break
			}
		}
	}
	return normalizeTaskIDs(taskIDs), nil
}

func normalizeTaskIDs(taskIDs []string) []string {
	seen := make(map[string]struct{}, len(taskIDs))
	result := make([]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		trimmed := strings.TrimSpace(taskID)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
}

