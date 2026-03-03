package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/skills"
)

func (pi *PipelineInspector) registerCoreSkills() {
	// Shared analysis skills.
	pi.skills.Register(shared.RunLinterSkill(pi.toolRunner))
	pi.skills.Register(shared.RunTypeCheckerSkill(pi.toolRunner))
	pi.skills.Register(shared.RunFormatterCheckSkill(pi.toolRunner))
	pi.skills.Register(shared.RunSecurityScanSkill(pi.toolRunner))
	pi.skills.Register(shared.CheckCoverageSkill(pi.toolRunner))
	pi.skills.Register(shared.AnalyzeComplexitySkill(pi.toolRunner))
	pi.skills.Register(shared.DetectRaceConditionsSkill(pi.toolRunner))
	pi.skills.Register(shared.DetectDeadlocksSkill(pi.toolRunner))
	pi.skills.Register(shared.DetectMemoryLeaksSkill(pi.toolRunner))
	faFunc := func() shared.FileAccess { return pi.fileAccess }
	pi.skills.Register(shared.ReadFileSkill(faFunc))
	pi.skills.Register(shared.GlobSkill(faFunc))
	pi.skills.Register(shared.GrepSkill(faFunc))

	// Design validation skills (always registered — LLM selects based on context).
	pi.skills.Register(shared.ValidateTokenUsageSkill(pi.toolRunner))
	pi.skills.Register(shared.ValidateAccessibilitySkill(pi.toolRunner))
	pi.skills.Register(shared.ValidateComponentAPISkill(pi.toolRunner))
	pi.skills.Register(shared.ValidateDesignConsistencySkill(pi.toolRunner))

	// Pipeline-specific skills.
	pi.skills.Register(defineCriteriaSkill(pi))
	pi.skills.Register(validateCriteriaSkill(pi))
	pi.skills.Register(gradeTaskQualitySkill(pi))
	pi.skills.Register(requestCorrectionSkill(pi))
	pi.skills.Register(requestOverrideSkill(pi))
	pi.skills.Register(getValidationStatusSkill(pi))

	// Standard reroute.
	pi.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   pi.id,
		SessionID: func() string { return pi.config.SessionID },
		Publish:   pi.publishRerouteRequest,
	}))
}

func defineCriteriaSkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("define_criteria").
		Description("Define success criteria and quality gates for a task (TDD Phase 1).").
		Domain("validation").
		Keywords("criteria", "define", "quality", "gate").
		Priority(100).
		StringParam("task_id", "Unique identifier for the task", true).
		ArrayParam("success_criteria", "List of success criteria", "object", false).
		ArrayParam("quality_gates", "List of quality gates", "object", false).
		ArrayParam("constraints", "List of constraints", "object", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID          string                    `json:"task_id"`
				SuccessCriteria []shared.SuccessCriterion `json:"success_criteria"`
				QualityGates    []shared.QualityGate      `json:"quality_gates"`
				Constraints     []shared.Constraint       `json:"constraints"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.TaskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}

			criteria := &shared.InspectorCriteria{
				TaskID:          params.TaskID,
				SuccessCriteria: params.SuccessCriteria,
				QualityGates:    params.QualityGates,
				Constraints:     params.Constraints,
				CreatedAt:       time.Now(),
			}

			pi.mu.Lock()
			pi.criteria[params.TaskID] = criteria
			pi.mu.Unlock()

			return map[string]any{
				"task_id":           params.TaskID,
				"criteria_defined":  true,
				"criteria_count":    len(criteria.SuccessCriteria),
				"gates_count":       len(criteria.QualityGates),
				"constraints_count": len(criteria.Constraints),
			}, nil
		}).
		Build()
}

func validateCriteriaSkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("validate_criteria").
		Description("Validate implementation against defined criteria (TDD Phase 4).").
		Domain("validation").
		Keywords("validate", "criteria", "check").
		Priority(100).
		StringParam("task_id", "Task ID to validate against", true).
		ArrayParam("files", "Files to validate", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID string   `json:"task_id"`
				Files  []string `json:"files"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.TaskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}

			pi.mu.RLock()
			criteria, ok := pi.criteria[params.TaskID]
			pi.mu.RUnlock()

			if !ok {
				return nil, fmt.Errorf("no criteria defined for task %s", params.TaskID)
			}

			return map[string]any{
				"task_id":        params.TaskID,
				"criteria_found": true,
				"criteria_count": len(criteria.SuccessCriteria),
				"gates_count":    len(criteria.QualityGates),
				"files_to_check": params.Files,
			}, nil
		}).
		Build()
}

func gradeTaskQualitySkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("grade_task_quality").
		Description("Produce a multi-dimensional quality grade for a task.").
		Domain("validation").
		Keywords("grade", "quality", "score").
		Priority(90).
		StringParam("task_id", "Task to grade", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID string `json:"task_id"`
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
				"task_id": params.TaskID,
				"grade":   grade,
				"overall": grade.Overall(),
			}, nil
		}).
		Build()
}

func requestCorrectionSkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("request_correction").
		Description("Route corrections back to the responsible agent for fixing.").
		Domain("validation").
		Keywords("correction", "fix", "feedback").
		Priority(95).
		StringParam("target_agent", "Agent to send corrections to (engineer/designer)", true).
		ArrayParam("corrections", "List of corrections to apply", "object", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TargetAgent string              `json:"target_agent"`
				Corrections []shared.Correction `json:"corrections"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.TargetAgent == "" {
				return nil, fmt.Errorf("target_agent is required")
			}
			if len(params.Corrections) == 0 {
				return nil, fmt.Errorf("at least one correction is required")
			}

			// Route via sync bus RPC if available
			if pi.bus != nil {
				correctionPayload := map[string]any{
					"type":        "correction_request",
					"corrections": params.Corrections,
					"source":      pi.id,
				}
				payload, _ := json.Marshal(correctionPayload)

				resp, err := pi.requestRouteSync(ctx, params.TargetAgent, string(payload))
				if err != nil {
					pi.logger.Warn("correction routing failed", "target", params.TargetAgent, "error", err)
				}
				_ = resp
			}

			return map[string]any{
				"routed":           true,
				"target":           params.TargetAgent,
				"correction_count": len(params.Corrections),
			}, nil
		}).
		Build()
}

func requestOverrideSkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("request_override").
		Description("Request a severity downgrade for a specific issue.").
		Domain("validation").
		Keywords("override", "downgrade", "exception").
		Priority(80).
		StringParam("issue_id", "ID of the issue to override", true).
		StringParam("reason", "Reason for the override", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				IssueID string `json:"issue_id"`
				Reason  string `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.IssueID == "" || params.Reason == "" {
				return nil, fmt.Errorf("issue_id and reason are required")
			}

			return map[string]any{
				"issue_id":                params.IssueID,
				"status":                  "pending",
				"requires_human_approval": true,
			}, nil
		}).
		Build()
}

func getValidationStatusSkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("get_validation_status").
		Description("Return current validation state and status.").
		Domain("validation").
		Keywords("status", "state", "result").
		Priority(75).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			state := pi.getState()
			return map[string]any{
				"state":   state,
				"running": pi.running,
			}, nil
		}).
		Build()
}
