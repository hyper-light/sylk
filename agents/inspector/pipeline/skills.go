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
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

func (pi *PipelineInspector) registerCoreSkills() {
	// Phase 2.K / CR-2: Inspector-pipeline is read-only — writeCfg
	// removed because it no longer registers workspace_write.
	// Phase 2.K / GI-C refactor: run_linter + run_type_checker +
	// run_formatter_check + run_security_scan + analyze_complexity +
	// detect_deadlocks + detect_memory_leaks collapsed into
	// run_analyzer(kind=…).
	pi.skills.Register(shared.RunAnalyzerSkill(pi.toolRunner))
	pi.skills.Register(bashSkill(pi))
	// read_file / glob / grep dropped — workspace_read covers all
	// three via op=read|glob|grep with explicit view selection.
	// Phase 2.K / CR-2 refactor: 12 workspace skills collapsed to 3
	// verb-dispatched primitives. Inspector-pipeline is read-only —
	// it gets workspace_read but NOT prepare_write_context or
	// workspace_write (its role is audit, not mutation).
	inspectorPipelineGetViews := func() versioning.WorkspaceViewAccess { return pi.workspaceViews }
	inspectorPipelineGetFA := func() versioning.FileAccess { return pi.fileAccess }
	inspectorPipelineDefaultPipelineID := func() string { return pi.pipelineID }
	pi.skills.Register(versioning.NewWorkspaceReadSkill(versioning.WorkspaceReadSkillConfig{
		GetViews:          inspectorPipelineGetViews,
		GetFileAccess:     inspectorPipelineGetFA,
		DefaultPipelineID: inspectorPipelineDefaultPipelineID,
	}))

	// Phase 2.K / 10.B refactor: validate_token_usage +
	// validate_accessibility + validate_component_api +
	// validate_design_consistency collapsed into
	// validate_ui_compliance(aspect=…).
	pi.skills.Register(shared.ValidateUIComplianceSkill(pi.toolRunner))

	// Pipeline-specific skills.
	pi.skills.Register(defineCriteriaSkill(pi))
	pi.skills.Register(validateCriteriaSkill(pi))
	pi.skills.Register(gradeTaskQualitySkill(pi))
	pi.skills.Register(requestOverrideSkill(pi))
	// Phase 1 refactor:
	//   - request_correction removed. Inspector uses challenge_peer
	//     with target_activity_id = offending artifact_published /
	//     validation_rejected fabric activity ID.
	//   - get_validation_status removed. Derived from
	//     query_peer_activity(kinds=["validation_started",
	//     "validation_accepted","validation_rejected"]) +
	//     query_pipeline_state.
	// Phase 2.K / GT-4 + GI-5 refactor: collapsed into dependency(action=…).
	pi.skills.Register(dependencySkill(pi))

	for _, skill := range agentShared.CoordinationSkills(agentShared.CoordinationSkillConfig{
		Client: agentShared.CoordinationClient{
			BusProvider:     func() guide.EventBus { return pi.bus },
			SourceAgentID:   func() string { return pi.id },
			SourceAgentType: func() string { return "inspector-pipeline" },
			SessionID:       func() string { return pi.config.SessionID },
			RegisterPending: func(correlationID string) <-chan *guide.Message {
				return pi.registerPendingWait(correlationID).Response
			},
			ClearPending: pi.clearPendingWait,
			Timeout:      routeSyncTimeout,
		},
		CurrentTaskID:   func() string { return pi.pipelineID },
		CurrentTaskName: func() string { return firstNonEmptyCoordinationName(pi.pipelineName, pi.pipelineSlug) },
		WorkerType:      func() string { return "inspector-pipeline" },
	}) {
		pi.skills.Register(skill)
	}
	// Activity Fabric: uniform awareness skills + cross-pipeline
	// primitives + audit-time inspect_open_activity. The audit skill
	// is inspector-only — it's how the inspector detects unresolved
	// cross-pipeline disputes blocking acceptance.
	for _, skill := range fabric.AwarenessSkills(fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return pi.config.SessionID },
		AgentID:        func() string { return pi.id },
		AgentType:      func() string { return "inspector-pipeline" },
	}) {
		pi.skills.Register(skill)
	}
	for _, skill := range agentShared.CrossPipelineSkills(agentShared.CrossPipelineSkillConfig{
		SessionID:  func() string { return pi.config.SessionID },
		AgentID:    func() string { return pi.id },
		AgentType:  func() string { return "inspector-pipeline" },
		PipelineID: func() string { return pi.pipelineID },
		RouteSync: agentShared.RouteSyncFromBus(
			func() guide.EventBus { return pi.bus },
			func() string {
				if pi.channels == nil {
					return ""
				}
				return pi.channels.Responses
			},
		),
	}) {
		pi.skills.Register(skill)
	}
	for _, skill := range fabric.InspectorAuditSkills(fabric.InspectorAuditSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return pi.config.SessionID },
		AgentID:        func() string { return pi.id },
		AgentType:      func() string { return "inspector-pipeline" },
	}) {
		pi.skills.Register(skill)
	}
	// Phase 5 of SCRIBE_FABRIC.md: recall_my_history.
	for _, skill := range fabric.RecallSkills(fabric.RecallSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return pi.config.SessionID },
		AgentID:        func() string { return pi.id },
		AgentType:      func() string { return "inspector-pipeline" },
	}) {
		pi.skills.Register(skill)
	}
	for _, skill := range agentShared.PipelineProtocolSkills(agentShared.PipelineProtocolSkillConfig{
		AgentType:      func() string { return "inspector-pipeline" },
		AgentID:        func() string { return pi.id },
		InspectorOT:    true,
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return pi.workspaceViews },
		// Inspector-owned VFS authority — handoff_to_ot and discard_pipeline
		// invoke this committer directly so the orchestrator no longer reacts
		// to "succeeded" / "failed" pipeline broadcasts to mutate VFS state.
		// Lazy because the wiring code (cmd/tui.go) calls SetPipelineCommitter
		// after agent construction.
		Committer: func() agentShared.PipelineCommitter { return pi.pipelineCommitter },
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

			// Phase 4 refactor: emit a charter-style decision so
			// tester/engineer see the quality contract in their
			// ambient context. Previously the criteria lived only in
			// inspector-local memory.
			summary := fmt.Sprintf("%d criteria, %d gates, %d constraints", len(criteria.SuccessCriteria), len(criteria.QualityGates), len(criteria.Constraints))
			agentShared.AutoPublishCommitted(ctx, agentShared.AutoPublishInput{
				SessionID:        pi.config.SessionID,
				AuthorAgentID:    pi.id,
				AuthorAgentType:  "inspector-pipeline",
				AuthorPipelineID: pi.pipelineID,
				TriggerSkill:     "define_criteria",
				Domain:           "success_criteria",
				Value:            summary,
				Scope:            taskID,
				Coordinates:      map[string]string{"task_id": taskID},
				Evidence:         []string{summary},
			})

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
			// Phase 4 refactor: wrap validation with fabric
			// activities so peer pipelines see the pass/fail outcome
			// in ambient context. Emit started before running, then
			// accepted/rejected based on result.Passed.
			startedID := agentShared.AutoPublishValidationStarted(ctx, agentShared.AutoPublishValidationInput{
				SessionID:       pi.config.SessionID,
				ActorAgentID:    pi.id,
				ActorAgentType:  "inspector-pipeline",
				ActorPipelineID: pi.pipelineID,
				TriggerSkill:    "validate_criteria",
				EpochID:         taskID,
				Scope:           taskID,
				Summary:         fmt.Sprintf("validating %d files against %d criteria", len(files), len(criteria.SuccessCriteria)),
			})
			result, err := pi.ValidateAgainstCriteria(ctx, taskID, files, workerType)
			if err != nil {
				return nil, err
			}
			validationSummary := fmt.Sprintf("passed=%t issues=%d met=%d failed=%d", result.Passed, len(result.Issues), len(result.CriteriaMet), len(result.CriteriaFailed))
			evidence := make([]string, 0, len(result.Issues))
			for _, issue := range result.Issues {
				evidence = append(evidence, issue.Message)
			}
			validationInput := agentShared.AutoPublishValidationInput{
				SessionID:       pi.config.SessionID,
				ActorAgentID:    pi.id,
				ActorAgentType:  "inspector-pipeline",
				ActorPipelineID: pi.pipelineID,
				TriggerSkill:    "validate_criteria",
				EpochID:         taskID,
				Scope:           taskID,
				Summary:         validationSummary,
				Evidence:        evidence,
				IssueCount:      len(result.Issues),
				FailureCount:    len(result.CriteriaFailed),
			}
			if result.Passed {
				agentShared.AutoPublishValidationAccepted(ctx, validationInput, startedID)
			} else {
				agentShared.AutoPublishValidationRejected(ctx, validationInput, startedID)
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

			// Phase 4 refactor: emit grade as a decision so peers
			// see the inspector's judgment in ambient context.
			overall := grade.OverallForDomain(shared.ValidationDomainFromWorkerType(workerType))
			agentShared.AutoPublishCommitted(ctx, agentShared.AutoPublishInput{
				SessionID:        pi.config.SessionID,
				AuthorAgentID:    pi.id,
				AuthorAgentType:  "inspector-pipeline",
				AuthorPipelineID: pi.pipelineID,
				TriggerSkill:     "grade_task_quality",
				Domain:           "quality_grade",
				Value:            fmt.Sprintf("%v", overall),
				Scope:            taskID,
				Coordinates:      map[string]string{"task_id": taskID},
				Evidence:         []string{fmt.Sprintf("issues=%d", len(result.Issues))},
			})

			response := map[string]any{
				"task_id":        taskID,
				"grade":          grade,
				"overall":        overall,
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

