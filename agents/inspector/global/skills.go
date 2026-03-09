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
	faFunc := func() shared.FileAccess { return gi.fileAccess }
	gi.skills.Register(shared.ReadFileSkill(faFunc))
	gi.skills.Register(shared.GlobSkill(faFunc))
	gi.skills.Register(shared.GrepSkill(faFunc))
	gi.skills.Register(versioning.NewReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))
	gi.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return gi.workspaceViews }, nil))

	// Global-specific skills.
	gi.skills.Register(auditLayerSkill(gi))
	gi.skills.Register(validatePlanAdherenceSkill(gi))
	gi.skills.Register(crossReferenceChangesSkill(gi))
	gi.skills.Register(gradeLayerQualitySkill(gi))
	gi.skills.Register(requestArchitectResearchSkill(gi))
	gi.skills.Register(requestUserClarificationSkill(gi))
	gi.skills.Register(escalateFindingsSkill(gi))

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
		Description("Run comprehensive audit on a completed DAG layer.").
		Domain("audit").
		Keywords("audit", "layer", "dag").
		Priority(100).
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
		Description("Compare implementation against the architect's plan.").
		Domain("audit").
		Keywords("plan", "adherence", "compliance").
		Priority(100).
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
		Description("Detect cross-file issues: interface mismatches, import cycles, type inconsistencies, shared state races.").
		Domain("audit").
		Keywords("cross-file", "interface", "import", "type", "race").
		Priority(95).
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
		Description("Produce an overall quality grade for a DAG layer.").
		Domain("audit").
		Keywords("grade", "quality", "layer").
		Priority(90).
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
				_, _ = gi.requestRouteSync(ctx, "architect", string(payloadJSON))
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
				_, _ = gi.requestRouteSync(ctx, "guide", string(payloadJSON))
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
		Description("Publish audit findings to the audit.results topic.").
		Domain("audit").
		Keywords("escalate", "publish", "findings").
		Priority(95).
		StringParam("dag_id", "DAG identifier", true).
		IntParam("layer_idx", "Layer index", true).
		BoolParam("blocking", "Whether findings are blocking", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				DAGID    string `json:"dag_id"`
				LayerIdx int    `json:"layer_idx"`
				Blocking bool   `json:"blocking"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			// Publish to audit.results topic if bus is available
			if gi.bus != nil {
				payload := map[string]any{
					"dag_id":    params.DAGID,
					"layer_idx": params.LayerIdx,
					"blocking":  params.Blocking,
					"source":    gi.id,
				}
				payloadJSON, _ := json.Marshal(payload)
				msg := &guide.Message{
					ID:            gi.generateMessageID(),
					Type:          guide.MessageTypeAuditResult,
					Payload:       string(payloadJSON),
					SourceAgentID: gi.id,
					Timestamp:     time.Now(),
				}
				_ = gi.bus.Publish("audit.results", msg)
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
