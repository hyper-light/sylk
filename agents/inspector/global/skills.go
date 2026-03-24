package global

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

func (gi *GlobalInspector) registerCoreSkills() {
	writeCfg := versioning.WorkspaceWriteSkillConfig{
		GetFileAccess: func() versioning.FileAccess { return gi.fileAccess },
		GetViews:      func() versioning.WorkspaceViewAccess { return gi.workspaceViews },
	}

	// Shared analysis skills.
	gi.skills.Register(shared.RunLinterSkill(gi.toolRunner))
	gi.skills.Register(shared.RunTypeCheckerSkill(gi.toolRunner))
	gi.skills.Register(shared.RunFormatterCheckSkill(gi.toolRunner))
	gi.skills.Register(shared.RunSecurityScanSkill(gi.toolRunner))
	gi.skills.Register(shared.CheckCoverageSkill(gi.toolRunner))
	gi.skills.Register(shared.AnalyzeComplexitySkill(gi.toolRunner))
	gi.skills.Register(shared.DetectRaceConditionsSkill(gi.toolRunner))
	gi.skills.Register(shared.DetectDeadlocksSkill(gi.toolRunner))
	gi.skills.Register(shared.DetectMemoryLeaksSkill(gi.toolRunner))
	gi.skills.Register(runCommandSkill(gi))
	gi.skills.Register(runShellScriptSkill(gi))
	faFunc := func() shared.FileAccess { return gi.fileAccess }
	gi.skills.Register(shared.ReadFileSkill(faFunc))
	gi.skills.Register(shared.GlobSkill(faFunc))
	gi.skills.Register(shared.GrepSkill(faFunc))
	gi.skills.Register(versioning.NewReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewDiffWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil, nil))
	gi.skills.Register(versioning.NewPrepareGlobalWriteContextSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewListGlobalChangesSkill(func() versioning.FileAccess { return gi.fileAccess }))
	gi.skills.Register(versioning.NewWriteGlobalFileSkill(writeCfg))
	gi.skills.Register(versioning.NewEditGlobalFileSkill(writeCfg))
	gi.skills.Register(versioning.NewDeleteGlobalFileSkill(writeCfg))
	gi.skills.Register(versioning.NewCreateGlobalDirectorySkill(writeCfg))

	// Global-specific skills.
	gi.skills.Register(auditLayerSkill(gi))
	gi.skills.Register(validatePlanAdherenceSkill(gi))
	gi.skills.Register(crossReferenceChangesSkill(gi))
	gi.skills.Register(gradeLayerQualitySkill(gi))
	gi.skills.Register(loadPlanContextSkill(gi))
	gi.skills.Register(consultLibrarianStyleSkill(gi))
	gi.skills.Register(consultAcademicApproachSkill(gi))
	gi.skills.Register(consultArchivalistContextSkill(gi))
	gi.skills.Register(requestArchitectResearchSkill(gi))
	gi.skills.Register(requestUserClarificationSkill(gi))
	gi.skills.Register(escalateFindingsSkill(gi))
	gi.skills.Register(researchDependencyInstallSkill(gi))
	gi.skills.Register(installDependencyToolingSkill(gi))

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

func auditLayerSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("audit_layer").
		Description("Run an adversarial, whole-plan audit on a completed DAG layer.").
		Domain("audit").
		Keywords("audit", "layer", "dag").
		Priority(100).
		Usage("Use when a completed DAG layer needs a hard, cross-file quality gate against the entire architect plan, the codebase's existing style, and the user's preserved intent.").
		Requirement("Provide the DAG, layer, and the full architect plan snapshot when available. If the plan is missing or partial, call `load_plan_context` before concluding.").
		Satisfies("Produces the whole-layer audit evidence that drives global inspection, blocking decisions, architect pushback, and escalation.").
		Avoid("Do not use for narrow single-file inspection when a scoped pipeline inspector pass is the correct tool.").
		StringParam("dag_id", "DAG identifier", true).
		IntParam("layer_idx", "Layer index to audit", true).
		StringParam("plan_snapshot", "Architect plan snapshot", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				DAGID        string `json:"dag_id"`
				LayerIdx     int    `json:"layer_idx"`
				PlanSnapshot string `json:"plan_snapshot"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.DAGID == "" {
				return nil, fmt.Errorf("dag_id is required")
			}

			req := &shared.LayerAuditRequest{
				DAGID:        params.DAGID,
				LayerIdx:     params.LayerIdx,
				PlanSnapshot: params.PlanSnapshot,
			}

			result, err := gi.AuditLayer(ctx, req)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"dag_id":           result.DAGID,
				"layer_idx":        result.LayerIdx,
				"passed":           result.Passed,
				"critical_count":   result.CriticalCount,
				"high_count":       result.HighCount,
				"issue_count":      len(result.Issues),
				"cross_file_count": len(result.CrossFileIssues),
				"plan_adherence":   result.PlanAdherence.Score,
			}, nil
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

func requestArchitectResearchSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("request_architect_research").
		Description("Request the architect to perform additional research on an issue.").
		Domain("audit").
		Keywords("architect", "research", "escalate").
		Priority(90).
		Usage("Use when a global inspection finding requires deeper architectural research or plan-level clarification before a confident verdict.").
		Requirement("Provide a concrete research question and the context that made the issue ambiguous or risky.").
		Satisfies("Opens an architect-side research loop that can unblock the global audit with better architectural evidence.").
		Avoid("Do not use for questions you can answer directly from the available plan, code, or audit evidence.").
		StringParam("description", "What to research", true).
		StringParam("context", "Relevant context for the research", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Description string `json:"description"`
				Context     string `json:"context"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if gi.bus != nil {
				payload := map[string]any{
					"type":        "architect_research",
					"description": params.Description,
					"context":     params.Context,
					"source":      gi.id,
				}
				payloadJSON, _ := json.Marshal(payload)
				_, _ = gi.requestRouteSync(ctx, "architect", string(payloadJSON), map[string]any{
					"consultation_kind": "architect_research",
				})
			}

			return map[string]any{
				"requested":   true,
				"target":      "architect",
				"description": params.Description,
			}, nil
		}).
		Build()
}

func requestUserClarificationSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("request_user_clarification").
		Description("Route a clarification request to the user via the guide.").
		Domain("audit").
		Keywords("clarification", "user", "question").
		Priority(85).
		Usage("Use when the audit is blocked on missing product intent or a user decision that cannot be responsibly inferred from the existing evidence.").
		Requirement("Ask a concrete, decision-relevant question that explains what ambiguity is blocking the audit.").
		Satisfies("Creates a user clarification request that can unblock the audit without guessing.").
		Avoid("Do not use when the answer is already available in the plan, diffs, or existing task context.").
		StringParam("question", "Question for the user", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Question string `json:"question"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if gi.bus != nil {
				payload := map[string]any{
					"type":     "user_clarification",
					"question": params.Question,
					"source":   gi.id,
				}
				payloadJSON, _ := json.Marshal(payload)
				_, _ = gi.requestRouteSync(ctx, "guide", string(payloadJSON), map[string]any{
					"clarification_request": true,
				})
			}

			return map[string]any{
				"requested": true,
				"question":  params.Question,
			}, nil
		}).
		Build()
}

func escalateFindingsSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("escalate_findings").
		Description("Submit blocking or corrective audit findings to the Orchestrator validation control plane.").
		Domain("audit").
		Keywords("escalate", "publish", "findings").
		Priority(95).
		Usage("Use when the global audit has reached a concrete verdict that needs orchestration-level validation handling or remediation routing.").
		Requirement("Requires a real summary, blocking flag, DAG scope, and enough details for downstream remediation to act on the findings.").
		Satisfies("Publishes the global inspection verdict into the orchestration validation control plane.").
		Avoid("Do not escalate vague suspicions or unresolved research questions as final findings.").
		StringParam("dag_id", "DAG identifier", true).
		IntParam("layer_idx", "Layer index", true).
		BoolParam("blocking", "Whether findings are blocking", true).
		StringParam("summary", "Optional finding summary", false).
		StringParam("details", "Optional additional details", false).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				DAGID    string `json:"dag_id"`
				LayerIdx int    `json:"layer_idx"`
				Blocking bool   `json:"blocking"`
				Summary  string `json:"summary"`
				Details  string `json:"details"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if gi.bus != nil {
				kind := agentShared.ValidationVerdictPassWithFindings
				severity := agentShared.ValidationSeverityWarning
				if params.Blocking {
					kind = agentShared.ValidationVerdictNeedsArchitectRemediation
					severity = agentShared.ValidationSeverityHigh
				}
				summary := strings.TrimSpace(params.Summary)
				if summary == "" {
					if params.Blocking {
						summary = fmt.Sprintf("Global inspector found blocking issues in DAG %s layer %d.", params.DAGID, params.LayerIdx)
					} else {
						summary = fmt.Sprintf("Global inspector reported follow-up findings for DAG %s layer %d.", params.DAGID, params.LayerIdx)
					}
				}
				payload := &agentShared.ValidationVerdictPayload{
					Kind:               kind,
					Severity:           severity,
					ValidatorAgentID:   gi.id,
					ValidatorType:      "global-inspector",
					SessionID:          gi.config.SessionID,
					DAGIDs:             []string{params.DAGID},
					ShouldPause:        params.Blocking,
					Summary:            summary,
					Details:            strings.TrimSpace(params.Details),
					RecommendedActions: []string{"architect_remediation"},
					CreatedAt:          time.Now().UTC(),
				}
				body, _ := json.Marshal(payload)
				req := &guide.RouteRequest{
					Input:           string(body),
					SourceAgentID:   gi.id,
					SourceAgentName: "inspector",
					TargetAgentID:   "orchestrator",
					ExplicitTarget:  true,
					FireAndForget:   true,
					SessionID:       gi.config.SessionID,
					Timestamp:       time.Now(),
					Metadata: map[string]any{
						"control_plane_kind": agentShared.ControlPlaneKindValidationVerdict,
						"layer_idx":          params.LayerIdx,
					},
				}
				_ = gi.bus.Publish(guide.TopicGuideRequests, guide.NewRequestMessage(gi.generateMessageID(), req))
			}

			return map[string]any{
				"escalated": true,
				"dag_id":    params.DAGID,
				"layer_idx": params.LayerIdx,
				"blocking":  params.Blocking,
			}, nil
		}).
		Build()
}
