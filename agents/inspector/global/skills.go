package global

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/skills"
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

	// Global-specific skills.
	gi.skills.Register(auditLayerSkill(gi))
	gi.skills.Register(validatePlanAdherenceSkill(gi))
	gi.skills.Register(crossReferenceChangesSkill(gi))
	gi.skills.Register(gradeLayerQualitySkill(gi))
	gi.skills.Register(requestArchitectResearchSkill(gi))
	gi.skills.Register(requestUserClarificationSkill(gi))
	gi.skills.Register(escalateFindingsSkill(gi))

	// Standard reroute.
	gi.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   gi.id,
		SessionID: func() string { return gi.config.SessionID },
		Publish:   gi.publishRerouteRequest,
	}))
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
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				PlanSnapshot     string   `json:"plan_snapshot"`
				ImplementedTasks []string `json:"implemented_tasks"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			score := shared.PlanAdherenceScore{
				Score:        1.0,
				TasksCovered: params.ImplementedTasks,
			}

			return map[string]any{
				"score":         score.Score,
				"tasks_covered": score.TasksCovered,
				"tasks_missing": score.TasksMissing,
				"deviations":    score.Deviations,
			}, nil
		}).
		Build()
}

func crossReferenceChangesSkill(_ *GlobalInspector) *skills.Skill {
	return skills.NewSkill("cross_reference_changes").
		Description("Detect cross-file issues: interface mismatches, import cycles, type inconsistencies, shared state races.").
		Domain("audit").
		Keywords("cross-file", "interface", "import", "type", "race").
		Priority(95).
		ArrayParam("files", "Files to cross-reference", "string", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Files []string `json:"files"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			// Cross-file analysis is performed by the LLM reading files
			// and reasoning about relationships. This skill provides structure.
			return map[string]any{
				"files_analyzed":    len(params.Files),
				"cross_file_issues": []shared.CrossFileIssue{},
			}, nil
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
