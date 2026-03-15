package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

func (pi *PipelineInspector) registerCoreSkills() {
	writeCfg := versioning.WorkspaceWriteSkillConfig{
		GetFileAccess:     func() versioning.FileAccess { return pi.fileAccess },
		GetViews:          func() versioning.WorkspaceViewAccess { return pi.workspaceViews },
		DefaultPipelineID: func() string { return pi.pipelineID },
	}

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
	pi.skills.Register(versioning.NewReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return pi.workspaceViews }, func() string { return pi.pipelineID }))
	pi.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return pi.workspaceViews }, func() string { return pi.pipelineID }))
	pi.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return pi.workspaceViews }, func() string { return pi.pipelineID }))
	pi.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return pi.workspaceViews }, func() string { return pi.pipelineID }))
	pi.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return pi.workspaceViews }, func() string { return pi.pipelineID }))
	pi.skills.Register(versioning.NewDiffWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return pi.workspaceViews }, func() string { return pi.pipelineID }, nil))
	pi.skills.Register(versioning.NewPreparePipelineWriteContextSkill(func() versioning.WorkspaceViewAccess { return pi.workspaceViews }, func() string { return pi.pipelineID }, nil))
	pi.skills.Register(versioning.NewListPipelineChangesSkill(func() versioning.FileAccess { return pi.fileAccess }))
	pi.skills.Register(versioning.NewWritePipelineFileSkill(writeCfg))
	pi.skills.Register(versioning.NewEditPipelineFileSkill(writeCfg))
	pi.skills.Register(versioning.NewDeletePipelineFileSkill(writeCfg))
	pi.skills.Register(versioning.NewCreatePipelineDirectorySkill(writeCfg))

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

	for _, skill := range agentShared.CoordinationSkills(agentShared.CoordinationSkillConfig{
		Client: agentShared.CoordinationClient{
			BusProvider:     func() guide.EventBus { return pi.bus },
			SourceAgentID:   func() string { return pi.id },
			SourceAgentType: func() string { return "inspector-pipeline" },
			SessionID:       func() string { return pi.config.SessionID },
			RegisterPending: pi.registerPendingWait,
			ClearPending:    pi.clearPendingWait,
			Timeout:         routeSyncTimeout,
		},
		CurrentTaskID:   func() string { return pi.pipelineID },
		CurrentTaskName: func() string { return firstNonEmptyCoordinationName(pi.pipelineName, pi.pipelineSlug) },
		WorkerType:      func() string { return "inspector-pipeline" },
	}) {
		pi.skills.Register(skill)
	}
	for _, skill := range agentShared.PipelineProtocolSkills(agentShared.PipelineProtocolSkillConfig{
		AgentType:   func() string { return "inspector-pipeline" },
		InspectorOT: true,
		Route: agentShared.PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return pi.bus },
			SessionID:   func() string { return pi.config.SessionID },
			PublishReroute: func(ctx context.Context, toAgentID, reason, newCorrelationID string) {
				agentShared.PublishPipelineHandoffReroute(pi.bus, pi.channels, ctx, "inspector-pipeline", toAgentID, reason, newCorrelationID)
			},
		},
	}) {
		pi.skills.Register(skill)
	}

	// Diagnostics
	pi.skills.Register(agentShared.NewSelfDiagnosticSkill(&pipelineInspectorDiag{pi: pi}))

	// Standard reroute.
	pi.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   pi.id,
		SessionID: func() string { return pi.config.SessionID },
		Publish:   pi.publishRerouteRequest,
	}))
}

func firstNonEmptyCoordinationName(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type pipelineInspectorDiag struct{ pi *PipelineInspector }

func (d *pipelineInspectorDiag) AgentName() string { return "inspector_pipeline" }
func (d *pipelineInspectorDiag) SessionID() string { return d.pi.config.SessionID }
func (d *pipelineInspectorDiag) LogsDir() string {
	return agentShared.LogsDirForAgent(d.pi.steering.SessionDir(), "inspector_pipeline")
}
func (d *pipelineInspectorDiag) EventLogger() *agentlog.SessionEventLogger {
	return d.pi.steering.EventLogger()
}
func (d *pipelineInspectorDiag) PeerLogsDirs() map[string]string { return nil }
func (d *pipelineInspectorDiag) RecoveryHints() []string         { return nil }

func (d *pipelineInspectorDiag) AgentSpecificDiagnostics() map[string]any {
	d.pi.mu.RLock()
	defer d.pi.mu.RUnlock()
	return map[string]any{
		"criteria_count": len(d.pi.criteria),
	}
}

func defineCriteriaSkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("define_criteria").
		Description("Define success criteria and quality gates for a task before downstream implementation and validation work.").
		Domain("validation").
		Keywords("criteria", "define", "quality", "gate").
		Usage("Use in contract-synthesis mode to turn the requested work into explicit success criteria, quality gates, and constraints before downstream implementation begins.").
		Satisfies("Creates the criteria contract that downstream Engineer, Tester, and Designer work should follow.").
		Avoid("Do not use to validate implementation quality after code exists; switch to validate_criteria for that.").
		BestPractice("Provide numeric quality gate thresholds as numbers when possible; the runtime also accepts numeric strings for robustness.").
		Example(`{"task_id":"task_1","success_criteria":[{"id":"criterion_1","description":"CLI prints Hello, world!","verifiable":true,"verification_method":"stdout_match"}],"quality_gates":[{"name":"coverage_min","metric":"coverage","threshold":80,"operator":">="}],"constraints":[{"type":"dependency","description":"Use argparse only","required":true}]}`).
		Priority(100).
		StringParam("task_id", "Unique identifier for the task", true).
		ArrayObjectParam("success_criteria", "List of success criteria", map[string]*skills.Property{
			"id":                  {Type: "string", Description: "Stable criterion identifier"},
			"description":         {Type: "string", Description: "What must be true for the task to pass"},
			"verifiable":          {Type: "boolean", Description: "Whether this criterion can be checked automatically"},
			"verification_method": {Type: "string", Description: "How the criterion should be verified"},
		}, []string{"description"}, false).
		ArrayObjectParam("quality_gates", "List of measurable quality gates", map[string]*skills.Property{
			"name":      {Type: "string", Description: "Stable quality gate identifier"},
			"metric":    {Type: "string", Description: "Metric being measured, such as coverage or blocking_issues"},
			"threshold": {Type: "number", Description: "Numeric threshold that must be met"},
			"operator":  {Type: "string", Description: "Comparison operator: >=, <=, >, <, ==, !="},
		}, []string{"metric", "threshold"}, false).
		ArrayObjectParam("constraints", "List of implementation constraints", map[string]*skills.Property{
			"type":        {Type: "string", Description: "Constraint category"},
			"description": {Type: "string", Description: "Constraint details"},
			"required":    {Type: "boolean", Description: "Whether the constraint is mandatory"},
		}, []string{"description"}, false).
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
			taskID, resolved := pi.resolveTaskID(params.TaskID)
			if taskID == "" {
				taskID = strings.TrimSpace(params.TaskID)
			}

			criteria := &shared.InspectorCriteria{
				TaskID:          taskID,
				SuccessCriteria: params.SuccessCriteria,
				QualityGates:    params.QualityGates,
				Constraints:     params.Constraints,
				CreatedAt:       time.Now(),
			}
			if err := normalizeDefinedCriteria(criteria); err != nil {
				return nil, err
			}

			pi.DefineCriteria(taskID, criteria)

			response := map[string]any{
				"task_id":           taskID,
				"criteria_defined":  true,
				"criteria_count":    len(criteria.SuccessCriteria),
				"gates_count":       len(criteria.QualityGates),
				"constraints_count": len(criteria.Constraints),
			}
			if resolved && strings.TrimSpace(params.TaskID) != "" && strings.TrimSpace(params.TaskID) != taskID {
				response["requested_task_id"] = strings.TrimSpace(params.TaskID)
			}
			return response, nil
		}).
		Build()
}

func normalizeDefinedCriteria(criteria *shared.InspectorCriteria) error {
	if criteria == nil {
		return fmt.Errorf("criteria are required")
	}
	for i := range criteria.SuccessCriteria {
		criterion := &criteria.SuccessCriteria[i]
		criterion.ID = strings.TrimSpace(criterion.ID)
		criterion.Description = strings.TrimSpace(criterion.Description)
		criterion.VerificationMethod = strings.TrimSpace(criterion.VerificationMethod)
		if criterion.Description == "" {
			return fmt.Errorf("success_criteria[%d].description is required", i)
		}
		if criterion.ID == "" {
			criterion.ID = fmt.Sprintf("criterion_%d", i+1)
		}
		if criterion.VerificationMethod == "" {
			if criterion.Verifiable {
				criterion.VerificationMethod = "automated_check"
			} else {
				criterion.VerificationMethod = "manual_review"
			}
		}
	}

	for i := range criteria.QualityGates {
		gate := &criteria.QualityGates[i]
		gate.Name = strings.TrimSpace(gate.Name)
		gate.Metric = strings.TrimSpace(gate.Metric)
		gate.Operator = normalizeQualityGateOperator(gate.Operator, gate.Metric, gate.Threshold)

		if gate.Metric == "" {
			return fmt.Errorf("quality_gates[%d].metric is required", i)
		}
		if gate.Operator == "" {
			return fmt.Errorf("quality_gates[%d].operator is invalid", i)
		}
		if gate.Name == "" {
			gate.Name = fmt.Sprintf("%s_%d", sanitizeCriterionID(gate.Metric), i+1)
		}
	}

	for i := range criteria.Constraints {
		constraint := &criteria.Constraints[i]
		constraint.Type = strings.TrimSpace(constraint.Type)
		constraint.Description = strings.TrimSpace(constraint.Description)
		if constraint.Description == "" {
			return fmt.Errorf("constraints[%d].description is required", i)
		}
		if constraint.Type == "" {
			constraint.Type = "requirement"
		}
	}

	return nil
}

func normalizeQualityGateOperator(operator, metric string, threshold float64) string {
	switch strings.TrimSpace(strings.ToLower(operator)) {
	case ">=", "gte":
		return ">="
	case "<=", "lte":
		return "<="
	case ">":
		return ">"
	case "<":
		return "<"
	case "==", "=", "eq":
		return "=="
	case "!=", "<>", "neq":
		return "!="
	case "":
		lowerMetric := strings.ToLower(strings.TrimSpace(metric))
		if threshold == 0 && (strings.Contains(lowerMetric, "issue") ||
			strings.Contains(lowerMetric, "error") ||
			strings.Contains(lowerMetric, "warning") ||
			strings.Contains(lowerMetric, "failure") ||
			strings.Contains(lowerMetric, "violation") ||
			strings.Contains(lowerMetric, "defect") ||
			strings.Contains(lowerMetric, "bug")) {
			return "=="
		}
		return ">="
	default:
		return ""
	}
}

func validateCriteriaSkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("validate_criteria").
		Description("Validate implementation against the defined criteria and quality gates.").
		Domain("validation").
		Keywords("validate", "criteria", "check").
		Priority(100).
		Usage("Use in implementation-validation mode after criteria are defined and implementation evidence exists in the workspace or upstream results.").
		Requirement("Requires an existing criteria contract and real implementation evidence for the requested task.").
		Satisfies("Produces criteria evaluation evidence and concrete findings for validation reporting.").
		Avoid("Do not use during pre-implementation contract synthesis.").
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
			taskID, resolved := pi.resolveTaskID(params.TaskID)
			if taskID == "" {
				taskID = strings.TrimSpace(params.TaskID)
			}

			pi.mu.RLock()
			criteria, ok := pi.criteria[taskID]
			files := append([]string(nil), pi.taskFiles[taskID]...)
			workerType := pi.workerType
			pi.mu.RUnlock()

			if !ok {
				return nil, fmt.Errorf("no criteria defined for task %s", taskID)
			}
			if len(params.Files) > 0 {
				files = params.Files
			}
			result, err := pi.ValidateAgainstCriteria(ctx, taskID, files, workerType)
			if err != nil {
				return nil, err
			}
			response := map[string]any{
				"task_id":              taskID,
				"criteria_found":       true,
				"criteria_count":       len(criteria.SuccessCriteria),
				"gates_count":          len(criteria.QualityGates),
				"files_to_check":       files,
				"passed":               result.Passed,
				"issue_count":          len(result.Issues),
				"criteria_met":         result.CriteriaMet,
				"criteria_failed":      result.CriteriaFailed,
				"quality_gate_results": result.QualityGateResults,
				"issues":               result.Issues,
			}
			if resolved && strings.TrimSpace(params.TaskID) != "" && strings.TrimSpace(params.TaskID) != taskID {
				response["requested_task_id"] = strings.TrimSpace(params.TaskID)
			}
			return response, nil
		}).
		Build()
}

func gradeTaskQualitySkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("grade_task_quality").
		Description("Produce a multi-dimensional quality grade for a task.").
		Domain("validation").
		Keywords("grade", "quality", "score").
		Priority(90).
		Usage("Use after criteria validation and supporting checks when you need an overall quality judgment for the task.").
		Requirement("Requires either an existing validation result or enough implementation evidence to run validation first.").
		Satisfies("Produces the final quality grade used in validation artifacts and escalation decisions.").
		Avoid("Do not grade work that has not been validated yet.").
		StringParam("task_id", "Task to grade", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			taskID, resolved := pi.resolveTaskID(params.TaskID)
			if taskID == "" {
				taskID = strings.TrimSpace(params.TaskID)
			}
			if taskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			pi.mu.RLock()
			result := pi.results[taskID]
			criteria := pi.criteria[taskID]
			files := append([]string(nil), pi.taskFiles[taskID]...)
			workerType := pi.workerType
			pi.mu.RUnlock()
			validationRan := false
			if result == nil {
				if criteria == nil {
					return nil, fmt.Errorf("no validation result available for task %s", taskID)
				}
				validated, err := pi.ValidateAgainstCriteria(ctx, taskID, files, workerType)
				if err != nil {
					return nil, fmt.Errorf("validate task %s before grading: %w", taskID, err)
				}
				result = validated
				validationRan = true
			}

			grade := qualityGradeForResult(result, workerType)

			response := map[string]any{
				"task_id":        taskID,
				"grade":          grade,
				"overall":        grade.OverallForDomain(shared.ValidationDomainFromWorkerType(workerType)),
				"issue_count":    len(result.Issues),
				"validation_ran": validationRan,
			}
			if resolved && strings.TrimSpace(params.TaskID) != "" && strings.TrimSpace(params.TaskID) != taskID {
				response["requested_task_id"] = strings.TrimSpace(params.TaskID)
			}
			return response, nil
		}).
		Build()
}

func requestCorrectionSkill(pi *PipelineInspector) *skills.Skill {
	return skills.NewSkill("request_correction").
		Description("Route corrections back to the responsible agent for fixing.").
		Domain("validation").
		Keywords("correction", "fix", "feedback").
		Priority(95).
		Usage("Use after you have concrete validation findings that require Engineer or Designer follow-up.").
		Requirement("Requires specific corrections grounded in criteria failures or validation findings.").
		Satisfies("Creates a downstream correction request for the responsible agent.").
		Avoid("Do not use for vague concerns that have not been turned into explicit corrections.").
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
		Usage("Use only when a concrete finding appears overstated and you need an explicit human-reviewed exception path.").
		Requirement("Requires the exact issue identifier and a clear reason for the proposed override.").
		Satisfies("Records a pending override request for human review.").
		Avoid("Do not use to silently suppress or downgrade findings on your own.").
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
		Description("Return the current task validation status, including whether validation is still pending or implementation evidence is available.").
		Domain("validation").
		Keywords("status", "state", "result").
		Priority(75).
		Usage("Use to make the current inspection mode explicit: pending contract synthesis versus implementation validation with real evidence.").
		Satisfies("Provides reusable pending-validation or implementation-evidence state for coordination artifacts and final reporting.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			taskID, _ := pi.resolveTaskID("")
			criteriaDefined := false
			validationResultAvailable := false
			if taskID != "" {
				criteriaDefined = pi.hasCriteria(taskID)
				validationResultAvailable = pi.hasValidationResult(taskID)
			}
			contract := agentShared.TaskExecutionContractFromContext(ctx)
			pendingValidation := !validationResultAvailable
			hasImplementationEvidence := false
			if contract != nil {
				pendingValidation = contract.PreImplementation
				hasImplementationEvidence = contract.HasImplementationEvidence
				if taskID == "" {
					criteriaDefined = contract.CriteriaDefined
					validationResultAvailable = contract.ValidationResultAvailable
				}
			}
			state := pi.getState()
			return map[string]any{
				"task_id":                     taskID,
				"state":                       state,
				"running":                     pi.running,
				"criteria_defined":            criteriaDefined,
				"validation_result_available": validationResultAvailable,
				"pending_validation":          pendingValidation,
				"has_implementation_evidence": hasImplementationEvidence,
			}, nil
		}).
		Build()
}
