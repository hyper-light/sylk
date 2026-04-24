package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

func ingestPlanSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("ingest_plan").
		Description("Ingest a structured plan handoff from the architect. Parses the plan document, creates task records and workflow state, builds a DAG from the task graph, and submits it to the DAG scheduler for execution.").
		Domain("orchestration").
		Keywords("ingest", "plan", "handoff", "architect", "import", "receive").
		Priority(95).
		Usage("Use when the architect dispatches a plan for execution. The plan_json must be a valid JSON-serialized PlanHandoff produced by the architect's buildHandoffPayload. This skill replaces manual execute_dag calls — it handles the full plan-to-DAG pipeline. Do NOT use for partial plans or plans still being revised.").
		StringParam("plan_json", "JSON-serialized PlanHandoff from the architect", true).
		Example(`{"plan_json": "{\"plan_id\":\"plan_abc\",\"tasks\":[...],\"execution_layers\":[[\"task_1\"],[\"task_2\",\"task_3\"]]}"}`).
		BestPractice("After ingestion, use analyze_plan with the returned dag_id to understand the execution structure before monitoring.").
		BestPractice("If ingestion fails with a validation error, escalate to the architect — the plan may need revision.").
		BestPractice("When streaming the pre-kickoff receipt, use a short multi-line summary with labeled lines or bullets. Do not collapse the plan id, objective, and execution counts into one dense sentence.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				PlanJSON string `json:"plan_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return o.ingestPlan(ctx, params.PlanJSON)
		}).
		Build()
}

func analyzePlanSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("analyze_plan").
		Description("Analyze a plan or active DAG execution to understand its structure, critical path, parallelism, complexity distribution, and risk factors. Produces a structured breakdown suitable for reasoning about execution strategy.").
		Domain("orchestration").
		Keywords("analyze", "plan", "dag", "structure", "critical", "path", "parallelism").
		Priority(85).
		Usage("Use after ingesting a plan (via ingest_plan) or when you need to understand an active DAG's structure. Provide either a dag_id (for active DAGs) or plan_json (for pre-execution analysis). The response includes the execution layer breakdown, critical path, per-task summaries, and aggregate statistics.").
		StringParam("dag_id", "DAG execution ID to analyze (for active/completed DAGs)", false).
		StringParam("plan_json", "Raw PlanHandoff JSON to analyze (for pre-execution analysis)", false).
		Example(`{"dag_id": "dag_abc123"}`).
		Example(`{"plan_json": "{\"plan_id\":\"plan_abc\",\"tasks\":[...]}"}`).
		BestPractice("Prefer dag_id over plan_json when the plan has already been ingested — it includes live execution state.").
		BestPractice("Use the critical_path field to identify which tasks determine overall completion time.").
		BestPractice("Use the risk_summary to decide whether to pre-emptively modify the DAG before high-risk tasks execute.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				DAGID    string `json:"dag_id"`
				PlanJSON string `json:"plan_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			if params.DAGID != "" {
				return o.analyzeActiveDAG(params.DAGID)
			}
			if params.PlanJSON != "" {
				return analyzePlanJSON(params.PlanJSON)
			}
			return nil, fmt.Errorf("either dag_id or plan_json is required")
		}).
		Build()
}

// ingestPlan parses a PlanHandoff, creates orchestrator state, builds a DAG,
// and submits it for execution.
func (o *Orchestrator) ingestPlan(ctx context.Context, planJSON string) (any, error) {
	o.logInfo("ingestPlan: entry",
		"plan_json_bytes", len(planJSON))
	attempt, duplicate, err := o.preparePlanHandoffIngest(ctx, planJSON)
	if err != nil {
		o.logWarnMsg("ingestPlan: preparePlanHandoffIngest failed",
			"error", err.Error())
		o.logTrace("plan_handoff_ingest_prepare_failed", agentlog.EventError, map[string]any{
			"error": err.Error(),
		})
		o.orchestratorSubmitTestament(ctx, o.orchestratorTestament(
			"Plan ingestion failed: "+truncateOrchestrator(err.Error(), 60), "committed",
			[]*claims.Artifact{o.orchestratorArtifact("error", err.Error())},
		))
		return nil, err
	}
	if duplicate {
		o.logInfo("ingestPlan: DUPLICATE — returning existing receipt without re-running dispatch",
			"plan_id", attempt.handoff.PlanID,
			"session_id", attempt.handoff.SessionID,
			"receipt_id", attempt.receipt.ReceiptID,
			"receipt_status", string(attempt.receipt.Status),
			"receipt_dag_id", attempt.receipt.DAGID,
			"receipt_updated_at", attempt.receipt.UpdatedAt)
		o.logTrace("plan_handoff_ingest_duplicate", agentlog.EventTaskDispatched, map[string]any{
			"plan_id":            attempt.handoff.PlanID,
			"session_id":         attempt.handoff.SessionID,
			"receipt_id":         attempt.receipt.ReceiptID,
			"receipt_status":     string(attempt.receipt.Status),
			"receipt_dag_id":     attempt.receipt.DAGID,
			"receipt_updated_at": attempt.receipt.UpdatedAt,
		})
		return o.handoffResultFromReceipt(attempt.receipt, true), nil
	}
	o.logInfo("ingestPlan: fresh plan — beginning DAG build + submit",
		"plan_id", attempt.handoff.PlanID,
		"session_id", attempt.handoff.SessionID,
		"task_count", len(attempt.handoff.Tasks),
		"layer_count", len(attempt.handoff.ExecutionLayers))
	if err := attempt.ingest(ctx); err != nil {
		o.logWarnMsg("ingestPlan: attempt.ingest failed",
			"plan_id", attempt.handoff.PlanID,
			"error", err.Error())
		o.orchestratorSubmitTestament(ctx, o.orchestratorTestament(
			"Plan ingest failed: "+truncateOrchestrator(attempt.handoff.PlanID, 40), "committed",
			[]*claims.Artifact{
				o.orchestratorArtifact("plan_id", attempt.handoff.PlanID),
				o.orchestratorArtifact("error", err.Error()),
			},
		))
		return nil, err
	}
	o.logInfo("ingestPlan: OK — plan ingested and DAG submitted for execution",
		"plan_id", attempt.handoff.PlanID,
		"session_id", attempt.handoff.SessionID,
		"dag_id", attempt.dagID,
		"workflow_id", attempt.workflowID,
		"task_count", len(attempt.handoff.Tasks),
		"layer_count", len(attempt.handoff.ExecutionLayers))
	o.logTrace("plan_handoff_ingest_ok", agentlog.EventTaskDispatched, map[string]any{
		"plan_id":     attempt.handoff.PlanID,
		"session_id":  attempt.handoff.SessionID,
		"dag_id":      attempt.dagID,
		"workflow_id": attempt.workflowID,
		"task_count":  len(attempt.handoff.Tasks),
		"layer_count": len(attempt.handoff.ExecutionLayers),
	})
	o.orchestratorSubmitTestament(ctx, o.orchestratorTestament(
		"Plan ingested and DAG submitted", "committed",
		[]*claims.Artifact{
			o.orchestratorArtifact("plan_id", attempt.handoff.PlanID),
			o.orchestratorArtifact("dag_id", attempt.dagID),
			o.orchestratorArtifact("task_count", fmt.Sprintf("%d", len(attempt.handoff.Tasks))),
			o.orchestratorArtifact("layer_count", fmt.Sprintf("%d", len(attempt.handoff.ExecutionLayers))),
		},
	))
	return attempt.result(), nil
}

type planHandoffIngestAttempt struct {
	orchestrator *Orchestrator
	handoff      *architect.PlanHandoff
	workflowID   string
	receipt      *PlanHandoffReceiptRecord
	preflight    *guardianPlanPreflight
	dag          *dag.DAG
	dagID        string
	receiptText  string
}

func (o *Orchestrator) preparePlanHandoffIngest(
	ctx context.Context,
	planJSON string,
) (*planHandoffIngestAttempt, bool, error) {
	handoff, err := parseValidatedPlanHandoff(planJSON)
	if err != nil {
		return nil, false, err
	}
	attempt := newPlanHandoffIngestAttempt(o, handoff)
	receipt, duplicate, err := o.beginPlanHandoffReceiptForAttempt(ctx, attempt)
	if err != nil {
		return nil, false, err
	}
	attempt.receipt = receipt
	return attempt, duplicate, nil
}

func parseValidatedPlanHandoff(planJSON string) (*architect.PlanHandoff, error) {
	var handoff architect.PlanHandoff
	if err := json.Unmarshal([]byte(planJSON), &handoff); err != nil {
		return nil, fmt.Errorf("invalid plan handoff JSON: %w", err)
	}
	if err := validateHandoff(&handoff); err != nil {
		return nil, fmt.Errorf("plan handoff validation failed: %w", err)
	}
	return &handoff, nil
}

func newPlanHandoffIngestAttempt(
	o *Orchestrator,
	handoff *architect.PlanHandoff,
) *planHandoffIngestAttempt {
	return &planHandoffIngestAttempt{
		orchestrator: o,
		handoff:      handoff,
		workflowID:   "wf_" + handoff.PlanID,
	}
}

func (o *Orchestrator) beginPlanHandoffReceiptForAttempt(
	ctx context.Context,
	attempt *planHandoffIngestAttempt,
) (*PlanHandoffReceiptRecord, bool, error) {
	receipt, duplicate, err := o.store.BeginPlanHandoffReceipt(
		attempt.handoff.SessionID,
		attempt.handoff.PlanID,
		attempt.handoff.Revision,
		handoffCorrelationIDFromContext(ctx),
		handoffRequesterAgentIDFromContext(ctx),
	)
	if err != nil {
		return nil, false, fmt.Errorf("begin handoff receipt: %w", err)
	}
	if !duplicate {
		// A genuinely-fresh plan is taking over the session.
		// Prior-plan execution holds carry remediation context that
		// no longer applies, so supersede them atomically before any
		// DAG from this plan is submitted. Duplicates skip this step
		// — they re-use the existing receipt and (by definition)
		// belong to the same plan_id whose holds should remain.
		if o.store != nil {
			superseded, supErr := o.store.SupersedePriorPlans(ctx, attempt.handoff.SessionID, attempt.handoff.PlanID)
			if supErr != nil {
				o.logWarnMsg("supersede prior-plan holds failed",
					"session_id", attempt.handoff.SessionID,
					"new_plan_id", attempt.handoff.PlanID,
					"error", supErr.Error())
			} else if superseded > 0 {
				o.logInfo("superseded prior-plan execution holds",
					"session_id", attempt.handoff.SessionID,
					"new_plan_id", attempt.handoff.PlanID,
					"superseded", superseded)
			}
		}
		o.publishPlanHandoffReceiptUpdate(receipt, "accepted")
		return receipt, false, nil
	}
	normalized, normErr := o.normalizePlanHandoffReceipt(receipt)
	if normErr != nil {
		return nil, false, fmt.Errorf("normalize duplicate handoff receipt: %w", normErr)
	}
	return normalized, true, nil
}

func (a *planHandoffIngestAttempt) ingest(ctx context.Context) error {
	if err := a.prepareExecution(ctx); err != nil {
		return err
	}
	a.publishReceiptSummary(ctx)
	if err := a.submitExecution(ctx); err != nil {
		return err
	}
	a.publishIngestedEvent()
	return nil
}

func (a *planHandoffIngestAttempt) prepareExecution(ctx context.Context) error {
	preflight, err := a.orchestrator.guardianPreflightPlan(ctx, a.handoff)
	if err != nil {
		return a.fail("guardian_preflight", "guardian preflight failed", err)
	}
	a.preflight = preflight
	a.orchestrator.createWorkflowAndTasks(a.handoff, a.workflowID, preflight)
	built, err := buildDAGFromHandoff(a.handoff)
	if err != nil {
		return a.fail("dag_build", "dag construction failed", err)
	}
	a.dag = built
	a.recordDAGBuilt()
	return nil
}

func (a *planHandoffIngestAttempt) submitExecution(ctx context.Context) error {
	if a.orchestrator.dagBridge == nil {
		a.orchestrator.logWarnMsg("submitExecution: dag bridge unavailable",
			"plan_id", a.handoff.PlanID,
			"session_id", a.handoff.SessionID)
		return a.fail("dag_bridge_unavailable", "dag bridge unavailable (no project directory)", fmt.Errorf("dag bridge unavailable"))
	}
	a.orchestrator.logInfo("submitExecution: dispatching DAG to dagBridge.Execute",
		"plan_id", a.handoff.PlanID,
		"session_id", a.handoff.SessionID,
		"task_count", len(a.handoff.Tasks),
		"layer_count", len(a.handoff.ExecutionLayers))
	dagID, err := a.orchestrator.dagBridge.Execute(ctx, a.dag, a.handoff.PlanID, a.handoff.SessionID)
	if err != nil {
		a.orchestrator.logWarnMsg("submitExecution: dagBridge.Execute failed",
			"plan_id", a.handoff.PlanID,
			"session_id", a.handoff.SessionID,
			"error", err.Error())
		return a.fail("dag_execute", "dag execution failed", err)
	}
	a.dagID = dagID
	a.orchestrator.logInfo("submitExecution: DAG submitted to scheduler — first node should be dispatching now",
		"plan_id", a.handoff.PlanID,
		"session_id", a.handoff.SessionID,
		"dag_id", dagID)
	a.orchestrator.logTrace("plan_handoff_dag_submitted", agentlog.EventTaskDispatched, map[string]any{
		"plan_id":    a.handoff.PlanID,
		"session_id": a.handoff.SessionID,
		"dag_id":     dagID,
	})
	a.recordSubmitted()
	return nil
}

func (a *planHandoffIngestAttempt) publishReceiptSummary(ctx context.Context) {
	if a == nil || a.orchestrator == nil {
		return
	}
	a.receiptText = buildPreKickoffIngestionReceipt(a.handoff)
	if strings.TrimSpace(a.receiptText) == "" {
		return
	}
	a.orchestrator.publishStreamChunk(ctx, a.receiptText)
}

func (a *planHandoffIngestAttempt) fail(stage, message string, cause error) error {
	a.recordFailure(stage, cause)
	return fmt.Errorf("%s: %w", message, cause)
}

func (a *planHandoffIngestAttempt) recordFailure(stage string, stageErr error) {
	if stageErr == nil {
		return
	}
	receipt, err := a.orchestrator.store.UpdatePlanHandoffReceiptStage(
		a.handoff.SessionID,
		a.handoff.PlanID,
		a.handoff.Revision,
		agentshared.PlanHandoffReceiptStatusFailed,
		a.workflowID,
		"",
		len(a.handoff.Tasks),
		len(a.handoff.ExecutionLayers),
		fmt.Sprintf("%s: %s", stage, stageErr.Error()),
	)
	if err != nil {
		a.orchestrator.logTrace("plan_handoff_receipt_fail_update_failed", agentlog.EventError, map[string]any{
			"plan_id":     a.handoff.PlanID,
			"revision":    a.handoff.Revision,
			"stage":       stage,
			"stage_error": stageErr.Error(),
			"error":       err.Error(),
		})
		return
	}
	a.receipt = receipt
	a.orchestrator.publishPlanHandoffReceiptUpdate(receipt, "failed")
}

func (a *planHandoffIngestAttempt) recordDAGBuilt() {
	receipt, err := a.orchestrator.store.UpdatePlanHandoffReceiptStage(
		a.handoff.SessionID,
		a.handoff.PlanID,
		a.handoff.Revision,
		agentshared.PlanHandoffReceiptStatusDAGBuilt,
		a.workflowID,
		"",
		len(a.handoff.Tasks),
		len(a.handoff.ExecutionLayers),
		"",
	)
	if err != nil {
		a.orchestrator.logTrace("plan_handoff_receipt_dag_built_failed", agentlog.EventError, map[string]any{
			"plan_id":  a.handoff.PlanID,
			"revision": a.handoff.Revision,
			"error":    err.Error(),
		})
		return
	}
	a.receipt = receipt
	a.orchestrator.publishPlanHandoffReceiptUpdate(receipt, "dag_built")
}

func (a *planHandoffIngestAttempt) recordSubmitted() {
	receipt, err := a.orchestrator.store.UpdatePlanHandoffReceiptStage(
		a.handoff.SessionID,
		a.handoff.PlanID,
		a.handoff.Revision,
		agentshared.PlanHandoffReceiptStatusSubmitted,
		a.workflowID,
		a.dagID,
		len(a.handoff.Tasks),
		len(a.handoff.ExecutionLayers),
		"",
	)
	if err != nil {
		a.orchestrator.logTrace("plan_handoff_receipt_submitted_failed", agentlog.EventError, map[string]any{
			"plan_id":  a.handoff.PlanID,
			"revision": a.handoff.Revision,
			"dag_id":   a.dagID,
			"error":    err.Error(),
		})
		return
	}
	a.receipt = receipt
	a.orchestrator.publishPlanHandoffReceiptUpdate(receipt, "submitted")
}

func (a *planHandoffIngestAttempt) publishIngestedEvent() {
	a.orchestrator.pushEvent(&busEvent{
		Topic:     "plan.ingested",
		Timestamp: time.Now(),
		Severity:  severityInfo,
		Summary: fmt.Sprintf(
			"Plan %s ingested: %d tasks, %d layers, DAG %s",
			a.handoff.PlanID,
			len(a.handoff.Tasks),
			len(a.handoff.ExecutionLayers),
			a.dagID,
		),
		Data: map[string]any{
			"plan_id":     a.handoff.PlanID,
			"dag_id":      a.dagID,
			"task_count":  len(a.handoff.Tasks),
			"layer_count": len(a.handoff.ExecutionLayers),
			"trigger":     a.handoff.Trigger,
			"guardian":    a.preflight.summaryPayload(),
		},
	})
}

func (a *planHandoffIngestAttempt) result() map[string]any {
	return map[string]any{
		"ingested":         true,
		"duplicate":        false,
		"receipt_id":       receiptIDOf(a.receipt),
		"receipt_status":   receiptStatusOf(a.receipt),
		"receipt_streamed": strings.TrimSpace(a.receiptText) != "",
		"plan_id":          a.handoff.PlanID,
		"dag_id":           a.dagID,
		"workflow_id":      a.workflowID,
		"task_count":       len(a.handoff.Tasks),
		"layer_count":      len(a.handoff.ExecutionLayers),
		"guardian_gate":    a.preflight.summaryPayload(),
		"guardian_clean":   a.preflight.Clean(),
	}
}

func buildPreKickoffIngestionReceipt(h *architect.PlanHandoff) string {
	if h == nil {
		return ""
	}
	planID := strings.TrimSpace(h.PlanID)
	taskCount := len(h.Tasks)
	layerCount := len(h.ExecutionLayers)
	query := strings.TrimSpace(h.Query)

	lines := []string{
		fmt.Sprintf("Received plan `%s`", planID),
		"",
	}
	if query != "" {
		lines = append(lines, fmt.Sprintf("- Goal: %s", query))
	}
	lines = append(
		lines,
		fmt.Sprintf("- Execution: %d task%s across %d layer%s", taskCount, pluralSuffix(taskCount), layerCount, pluralSuffix(layerCount)),
		"",
		"Starting execution next.",
	)
	return strings.Join(lines, "\n")
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func receiptIDOf(receipt *PlanHandoffReceiptRecord) string {
	if receipt == nil {
		return ""
	}
	return receipt.ReceiptID
}

func receiptStatusOf(receipt *PlanHandoffReceiptRecord) string {
	if receipt == nil {
		return ""
	}
	return string(receipt.Status)
}

func validateHandoff(h *architect.PlanHandoff) error {
	if strings.TrimSpace(h.PlanID) == "" {
		return fmt.Errorf("plan_id is required")
	}
	if len(h.Tasks) == 0 {
		return fmt.Errorf("at least one task is required")
	}
	ids := make(map[string]struct{}, len(h.Tasks))
	for _, t := range h.Tasks {
		if strings.TrimSpace(t.ID) == "" {
			return fmt.Errorf("task id is required")
		}
		if _, exists := ids[t.ID]; exists {
			return fmt.Errorf("duplicate task id: %s", t.ID)
		}
		ids[t.ID] = struct{}{}
		if !pipelineExpandable(t.AgentType) {
			return fmt.Errorf("task %s has invalid agent_type %q: only %q and %q are pipeline-eligible",
				t.ID, t.AgentType, "engineer", "designer")
		}
	}
	return nil
}

// createWorkflowAndTasks populates orchestrator state with workflow and task records.
func (o *Orchestrator) createWorkflowAndTasks(h *architect.PlanHandoff, wfID string, preflight *guardianPlanPreflight) {
	o.mu.Lock()
	defer o.mu.Unlock()

	now := time.Now()
	taskIDs := make([]string, 0, len(h.Tasks))
	planSnapshot := buildGlobalInspectorPlanSnapshot(h)
	planFilePath := strings.TrimSpace(h.PlanFile)

	for _, ht := range h.Tasks {
		taskIDs = append(taskIDs, ht.ID)
		o.state.Tasks[ht.ID] = &TaskRecord{
			ID:          ht.ID,
			WorkflowID:  wfID,
			Name:        ht.Name,
			Description: ht.Description,
			Status:      TaskStatusPending,
			Priority:    ht.Priority,
			CreatedAt:   now,
			MaxAttempts: 2,
			SessionID:   h.SessionID,
			Metadata: map[string]any{
				"agent_type":             ht.AgentType,
				"task_slug":              ht.Slug,
				"complexity":             ht.Complexity,
				"estimated_tokens":       ht.EstimatedTokens,
				"plan_id":                h.PlanID,
				"plan_file_path":         planFilePath,
				"success_criteria":       ht.SuccessCriteria,
				"acceptance_criteria":    ht.AcceptanceCriteria,
				"guidelines":             ht.Guidelines,
				"implementation_guide":   ht.ImplementationGuide,
				"task_criteria_snapshot": buildGlobalInspectorTaskCriteriaSnapshot(ht),
				"affected_files":         ht.AffectedFiles,
				"test_requirements":      ht.TestRequirements,
				"risk_factors":           ht.RiskFactors,
			},
		}
		if preflight != nil {
			if taskPreflight, ok := preflight.Tasks[ht.ID]; ok {
				o.state.Tasks[ht.ID].Metadata["guardian_preflight"] = taskPreflight
			}
		}
	}

	o.state.Workflows[wfID] = &WorkflowState{
		ID:          wfID,
		Name:        fmt.Sprintf("Plan %s", h.PlanID),
		Description: h.Query,
		Status:      WorkflowStatusRunning,
		TaskIDs:     taskIDs,
		PendingIDs:  taskIDs,
		StartedAt:   now,
		UpdatedAt:   now,
		LeadAgentID: "architect",
		SessionID:   h.SessionID,
		Metadata: map[string]any{
			"plan_id":               h.PlanID,
			"plan_file_path":        planFilePath,
			workflowPlanSnapshotKey: planSnapshot,
			"revision":              h.Revision,
			"trigger":               h.Trigger,
			"total_tokens":          h.TotalTokens,
			"critical_path":         h.CriticalPath,
			"execution_layers":      h.ExecutionLayers,
		},
	}
	if preflight != nil {
		o.state.Workflows[wfID].Metadata["guardian_preflight"] = preflight.summaryPayload()
	}

	o.state.Stats.TotalWorkflows++
	o.state.Stats.ActiveWorkflows++
}

type guardianTaskPreflight struct {
	TaskID           string   `json:"task_id"`
	AgentType        string   `json:"agent_type"`
	Clean            bool     `json:"clean"`
	RequiresApproval bool     `json:"requires_approval"`
	Warnings         []string `json:"warnings,omitempty"`
	BlockingFindings []string `json:"blocking_findings,omitempty"`
	UserMessage      string   `json:"user_message,omitempty"`
}

type guardianPlanPreflight struct {
	PlanID string
	Tasks  map[string]*guardianTaskPreflight
}

func (g *guardianPlanPreflight) Clean() bool {
	if g == nil {
		return true
	}
	for _, task := range g.Tasks {
		if task == nil {
			continue
		}
		if !task.Clean || len(task.BlockingFindings) > 0 {
			return false
		}
	}
	return true
}

func (g *guardianPlanPreflight) summaryPayload() map[string]any {
	if g == nil {
		return map[string]any{"clean": true}
	}
	warnings := 0
	blocking := 0
	requiresApproval := false
	results := make([]map[string]any, 0, len(g.Tasks))
	for _, task := range g.Tasks {
		if task == nil {
			continue
		}
		warnings += len(task.Warnings)
		blocking += len(task.BlockingFindings)
		requiresApproval = requiresApproval || task.RequiresApproval
		results = append(results, map[string]any{
			"task_id":           task.TaskID,
			"agent_type":        task.AgentType,
			"clean":             task.Clean,
			"requires_approval": task.RequiresApproval,
			"warnings":          task.Warnings,
			"blocking_findings": task.BlockingFindings,
			"user_message":      task.UserMessage,
		})
	}
	return map[string]any{
		"plan_id":             g.PlanID,
		"clean":               g.Clean(),
		"requires_approval":   requiresApproval,
		"warning_count":       warnings,
		"blocking_count":      blocking,
		"task_preflight_runs": results,
	}
}

func (o *Orchestrator) guardianPreflightPlan(ctx context.Context, handoff *architect.PlanHandoff) (*guardianPlanPreflight, error) {
	if handoff == nil {
		return nil, fmt.Errorf("plan handoff is required")
	}

	preflight := &guardianPlanPreflight{
		PlanID: handoff.PlanID,
		Tasks:  make(map[string]*guardianTaskPreflight, len(handoff.Tasks)),
	}
	for _, task := range handoff.Tasks {
		payload := map[string]any{
			"action":               "preflight_task",
			"plan_id":              handoff.PlanID,
			"task_id":              task.ID,
			"agent_type":           task.AgentType,
			"complexity":           task.Complexity,
			"affected_files":       affectedFilePaths(task.AffectedFiles),
			"risk_factors":         task.RiskFactors,
			"test_requirements":    task.TestRequirements,
			"guidelines":           task.Guidelines,
			"implementation_guide": task.ImplementationGuide,
		}
		o.orchestratorPostClaim(ctx,
			claims.Action{AgentID: "orchestrator", Type: claims.ActionTypeTask},
			orchestratorTaskClaim(
				"Preflight gate: guardian review for "+task.ID,
				"Guardian preflight review of task before dispatch",
				"guardian",
				[]claims.ClaimScopeEntry{{Kind: "preflight", Key: task.ID}},
				[]*claims.Validation{
					orchestratorValidation(claims.ValidationTypeReceipt, true, "Guardian completes preflight review", "response.success"),
				},
			),
		)
		respMsg, err := o.requestRouteSync(ctx, "guardian", payload, map[string]any{
			"direct_skill": "review_gate",
			"plan_id":      handoff.PlanID,
			"task_id":      task.ID,
		})
		if err != nil {
			return nil, err
		}
		resp, ok := respMsg.GetRouteResponse()
		if !ok || resp == nil {
			return nil, fmt.Errorf("guardian preflight returned invalid response for task %s", task.ID)
		}
		if !resp.Success {
			return nil, fmt.Errorf("guardian preflight rejected task %s: %s", task.ID, resp.Error)
		}
		taskPreflight, err := decodeGuardianTaskPreflight(resp.Data)
		if err != nil {
			return nil, fmt.Errorf("decode guardian preflight for task %s: %w", task.ID, err)
		}
		preflight.Tasks[task.ID] = taskPreflight
		if taskPreflight.UserMessage != "" {
			level := "info"
			if len(taskPreflight.BlockingFindings) > 0 {
				level = "error"
			} else if len(taskPreflight.Warnings) > 0 || taskPreflight.RequiresApproval {
				level = "warning"
			}
			_ = o.publishStatusMessage(taskPreflight.UserMessage, level)
		}
	}
	return preflight, nil
}

func decodeGuardianTaskPreflight(data any) (*guardianTaskPreflight, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal guardian payload: %w", err)
	}
	var result guardianTaskPreflight
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal guardian payload: %w", err)
	}
	return &result, nil
}

func affectedFilePaths(files []architect.TaskFileTarget) []string {
	if len(files) == 0 {
		return nil
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.Path) == "" {
			continue
		}
		paths = append(paths, file.Path)
	}
	return paths
}

func (o *Orchestrator) publishStatusMessage(message, level string) error {
	if o.bus == nil || !o.running {
		return nil
	}
	msg := &guide.Message{
		ID:            generateMessageID(),
		Type:          guide.MessageTypeResponse,
		SourceAgentID: o.config.AgentID,
		Payload:       message,
		Metadata: map[string]any{
			"level":  level,
			"source": "orchestrator",
		},
		Timestamp: time.Now(),
	}
	return o.bus.Publish("orchestrator.status", msg)
}

// buildDAGFromHandoff constructs a dag.DAG from the PlanHandoff task graph.
func buildDAGFromHandoff(h *architect.PlanHandoff) (*dag.DAG, error) {
	builder := dag.NewBuilder(fmt.Sprintf("plan_%s", h.PlanID)).
		WithDescription(h.Query)

	if h.Constraints != nil {
		policy := dag.ExecutionPolicy{
			FailurePolicy:  dag.FailurePolicyContinue,
			MaxConcurrency: constraintConcurrency(h.Constraints),
			DefaultTimeout: constraintTimeout(h.Constraints),
			DefaultRetries: 0,
			RetryBackoff:   time.Second,
		}
		builder = builder.WithPolicy(policy)
	}

	builder = builder.WithMetadata("plan_id", h.PlanID)
	builder = builder.WithMetadata("session_id", h.SessionID)

	for _, ht := range h.Tasks {
		prompt := buildNodePrompt(ht)
		nodeCtx := buildNodeContext(h, ht)
		cfg := dag.NodeConfig{
			ID:           ht.ID,
			AgentType:    ht.AgentType,
			Prompt:       prompt,
			Context:      nodeCtx,
			Dependencies: ht.Dependencies,
			Priority:     ht.Priority,
			Metadata: map[string]any{
				"name":       ht.Name,
				"complexity": ht.Complexity,
			},
			CoAgents:          ht.CoAgents,
			CollaborationMode: parseHandoffCollaborationMode(ht.CollaborationMode),
			MaxReviewRounds:   ht.MaxReviewRounds,
		}
		if len(ht.AgentScopes) > 0 {
			scopedPrompts := buildScopedPrompts(ht)
			cfg.Context["agent_prompts"] = scopedPrompts
		}
		builder = builder.AddNode(cfg)
	}

	return builder.Build()
}

// buildNodePrompt composes the full task prompt from HandoffTask fields.
// Includes the description, implementation guide, acceptance criteria,
// guidelines, examples, affected files, and test requirements.
func buildNodePrompt(ht *architect.HandoffTask) string {
	var b strings.Builder

	b.WriteString("# Task: ")
	b.WriteString(ht.Name)
	b.WriteString("\n\n## Description\n")
	b.WriteString(ht.Description)

	if ht.ImplementationGuide != "" {
		b.WriteString("\n\n## Implementation Guide\n")
		b.WriteString(ht.ImplementationGuide)
	}

	if len(ht.AcceptanceCriteria) > 0 {
		b.WriteString("\n\n## Acceptance Criteria\n")
		for i, ac := range ht.AcceptanceCriteria {
			fmt.Fprintf(&b, "%d. [%s] Given %s, when %s, then %s\n",
				i+1, ac.Priority, ac.Given, ac.When, ac.Then)
		}
	}

	if len(ht.Guidelines) > 0 {
		b.WriteString("\n\n## Guidelines\n")
		for _, g := range ht.Guidelines {
			b.WriteString("- ")
			b.WriteString(g)
			b.WriteString("\n")
		}
	}

	if len(ht.Examples) > 0 {
		b.WriteString("\n\n## Examples\n")
		for _, ex := range ht.Examples {
			if strings.TrimSpace(ex.Label) != "" {
				fmt.Fprintf(&b, "### %s\n", strings.TrimSpace(ex.Label))
			}
			fence := "```"
			if lang := strings.TrimSpace(ex.Language); lang != "" {
				fence += lang
			}
			b.WriteString(fence)
			b.WriteString("\n")
			b.WriteString(strings.TrimSpace(ex.Code))
			b.WriteString("\n```\n")
			if explanation := strings.TrimSpace(ex.Explanation); explanation != "" {
				b.WriteString(explanation)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	if len(ht.AffectedFiles) > 0 {
		b.WriteString("\n\n## Affected Files\n")
		for _, af := range ht.AffectedFiles {
			fmt.Fprintf(&b, "- %s [%s]: %s\n", af.Path, af.Operation, af.Reason)
		}
	}

	if len(ht.TestRequirements) > 0 {
		b.WriteString("\n\n## Test Requirements\n")
		for _, tr := range ht.TestRequirements {
			b.WriteString("- ")
			b.WriteString(tr)
			b.WriteString("\n")
		}
	}

	if len(ht.SuccessCriteria) > 0 {
		b.WriteString("\n\n## Success Criteria\n")
		for _, sc := range ht.SuccessCriteria {
			b.WriteString("- ")
			b.WriteString(sc)
			b.WriteString("\n")
		}
	}

	if len(ht.RiskFactors) > 0 {
		b.WriteString("\n\n## Risk Factors\n")
		for _, rf := range ht.RiskFactors {
			b.WriteString("- ")
			b.WriteString(rf)
			b.WriteString("\n")
		}
	}

	return b.String()
}

// buildNodeContext creates the structured context map for a DAG node.
func buildNodeContext(h *architect.PlanHandoff, ht *architect.HandoffTask) map[string]any {
	ctx := map[string]any{
		"task_id":          ht.ID,
		"task_slug":        ht.Slug,
		"task_name":        ht.Name,
		"agent_type":       ht.AgentType,
		"complexity":       ht.Complexity,
		"estimated_tokens": ht.EstimatedTokens,
	}
	if len(ht.AffectedFiles) > 0 {
		ctx["affected_files"] = ht.AffectedFiles
	}
	if len(ht.AcceptanceCriteria) > 0 {
		ctx["acceptance_criteria"] = ht.AcceptanceCriteria
	}
	if len(ht.SuccessCriteria) > 0 {
		ctx["success_criteria"] = ht.SuccessCriteria
	}
	if len(ht.Guidelines) > 0 {
		ctx["guidelines"] = ht.Guidelines
	}
	if ht.ImplementationGuide != "" {
		ctx["implementation_guide"] = ht.ImplementationGuide
	}
	if len(ht.TestRequirements) > 0 {
		ctx["test_requirements"] = ht.TestRequirements
	}
	if len(ht.RiskFactors) > 0 {
		ctx["risk_factors"] = ht.RiskFactors
	}
	if ht.Workspace.BaseVersion != "" || len(ht.Workspace.ReadSet) > 0 || len(ht.Workspace.WriteSet) > 0 || len(ht.Workspace.TestSurface) > 0 || len(ht.Workspace.PrefetchPaths) > 0 {
		ctx["workspace"] = ht.Workspace
	}
	if len(ht.WorkerPackets) > 0 {
		ctx["worker_packets"] = ht.WorkerPackets
	}
	if len(ht.CoAgents) > 0 {
		ctx["co_agents"] = ht.CoAgents
		ctx["collaboration_mode"] = ht.CollaborationMode
	}
	if len(ht.AgentScopes) > 0 {
		ctx["agent_scopes"] = ht.AgentScopes
	}
	ctx["big_picture"] = buildBigPictureBrief(h)
	ctx["task_intent"] = buildTaskIntentContext(ht)
	return ctx
}

func buildBigPictureBrief(h *architect.PlanHandoff) map[string]any {
	if h == nil {
		return nil
	}
	brief := map[string]any{
		"goal":          strings.TrimSpace(h.Query),
		"risk_summary":  append([]string(nil), h.RiskSummary...),
		"assumptions":   append([]string(nil), h.Assumptions...),
		"critical_path": append([]string(nil), h.CriticalPath...),
	}
	if h.Requirements != nil {
		brief["requirements"] = append([]string(nil), h.Requirements.Goals...)
		brief["constraints"] = append([]string(nil), h.Requirements.Constraints...)
	}
	if h.Architecture != nil {
		brief["architecture_summary"] = summarizeArchitecture(h.Architecture)
	}
	return brief
}

func buildTaskIntentContext(ht *architect.HandoffTask) map[string]any {
	if ht == nil {
		return nil
	}
	return map[string]any{
		"task_name":            ht.Name,
		"why_this_task_exists": ht.Description,
		"user_visible_outcome": strings.Join(ht.SuccessCriteria, "; "),
		"architectural_role":   ht.AgentType,
		"upstream_inputs":      append([]string(nil), ht.Dependencies...),
	}
}

func summarizeArchitecture(arch *architect.SolutionArchitecture) string {
	if arch == nil {
		return ""
	}
	var parts []string
	if name := strings.TrimSpace(arch.Name); name != "" {
		parts = append(parts, name)
	}
	if desc := strings.TrimSpace(arch.Description); desc != "" {
		parts = append(parts, desc)
	}
	if len(arch.Patterns) > 0 {
		parts = append(parts, "patterns: "+strings.Join(arch.Patterns, ", "))
	}
	if len(arch.Components) > 0 {
		componentNames := make([]string, 0, len(arch.Components))
		for _, component := range arch.Components {
			if name := strings.TrimSpace(component.Name); name != "" {
				componentNames = append(componentNames, name)
			}
		}
		if len(componentNames) > 0 {
			parts = append(parts, "components: "+strings.Join(componentNames, ", "))
		}
	}
	return strings.Join(parts, " | ")
}

func constraintConcurrency(c *architect.PlanConstraints) int {
	if c.MaxConcurrency > 0 {
		return c.MaxConcurrency
	}
	return 8
}

func constraintTimeout(c *architect.PlanConstraints) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 5 * time.Minute
}

// parseHandoffCollaborationMode converts a string collaboration mode from a
// HandoffTask to the dag.CollaborationMode enum.
func parseHandoffCollaborationMode(s string) dag.CollaborationMode {
	if strings.EqualFold(s, "adversarial") {
		return dag.CollaborationAdversarial
	}
	return dag.CollaborationSequential
}

// buildScopedPrompts produces per-agent scoped prompts for compound tasks.
// Each scoped prompt includes shared context plus agent-specific instructions.
// Returns a map of agent_type → prompt string.
func buildScopedPrompts(ht *architect.HandoffTask) map[string]string {
	prompts := make(map[string]string, len(ht.AgentScopes))
	for _, scope := range ht.AgentScopes {
		var b strings.Builder

		// Shared context.
		fmt.Fprintf(&b, "# Task: %s\n\n## Description\n%s\n", ht.Name, ht.Description)

		if len(ht.RiskFactors) > 0 {
			b.WriteString("\n## Risk Factors\n")
			for _, rf := range ht.RiskFactors {
				fmt.Fprintf(&b, "- %s\n", rf)
			}
		}

		if len(ht.SuccessCriteria) > 0 {
			b.WriteString("\n## Success Criteria\n")
			for _, sc := range ht.SuccessCriteria {
				fmt.Fprintf(&b, "- %s\n", sc)
			}
		}

		// Agent-specific sections.
		b.WriteString("\n## Your Scope\n")
		fmt.Fprintf(&b, "Role: %s\n", scope.Role)

		if scope.ImplementationGuide != "" {
			b.WriteString("\n### Implementation Guide\n")
			b.WriteString(scope.ImplementationGuide)
			b.WriteString("\n")
		}

		if len(scope.AcceptanceCriteria) > 0 {
			b.WriteString("\n### Acceptance Criteria\n")
			for i, ac := range scope.AcceptanceCriteria {
				fmt.Fprintf(&b, "%d. [%s] Given %s, when %s, then %s\n",
					i+1, ac.Priority, ac.Given, ac.When, ac.Then)
			}
		}

		if len(scope.AffectedFiles) > 0 {
			b.WriteString("\n### Affected Files\n")
			for _, af := range scope.AffectedFiles {
				fmt.Fprintf(&b, "- %s [%s]: %s\n", af.Path, af.Operation, af.Reason)
			}
		}

		if len(scope.Guidelines) > 0 {
			b.WriteString("\n### Guidelines\n")
			for _, g := range scope.Guidelines {
				fmt.Fprintf(&b, "- %s\n", g)
			}
		}

		if len(scope.TestRequirements) > 0 {
			b.WriteString("\n### Test Requirements\n")
			for _, tr := range scope.TestRequirements {
				fmt.Fprintf(&b, "- %s\n", tr)
			}
		}

		if scope.Role == "co_agent" {
			b.WriteString("\n### Context\nYou will receive the primary agent's changed files as context. Build upon their work. Do not re-implement what they have already done.\n")
		}

		prompts[scope.AgentType] = b.String()
	}
	return prompts
}
func (o *Orchestrator) analyzeActiveDAG(dagID string) (any, error) {
	status, err := o.dagBridge.Status(dagID)
	if err != nil {
		// Fall back to store.
		row, storeErr := o.store.GetDAGExecution(dagID)
		if storeErr != nil || row == nil {
			return nil, fmt.Errorf("dag not found: %s", dagID)
		}
		return analyzeFromStore(row), nil
	}
	return analyzeFromScheduler(dagID, status, o), nil
}

func analyzeFromScheduler(dagID string, status *dag.DAGStatus, o *Orchestrator) map[string]any {
	o.mu.RLock()
	meta := o.activeDAGs()[dagID]
	o.mu.RUnlock()

	analysis := map[string]any{
		"dag_id":        dagID,
		"state":         status.State.String(),
		"current_layer": status.CurrentLayer,
		"total_layers":  status.TotalLayers,
		"progress":      status.Progress,
		"source":        "scheduler",
	}

	// Node state breakdown.
	nodeBreakdown := map[string]int{}
	for _, state := range status.NodeStates {
		nodeBreakdown[state.String()]++
	}
	analysis["node_breakdown"] = nodeBreakdown
	analysis["total_nodes"] = len(status.NodeStates)

	if meta != nil {
		analysis["plan_id"] = meta.PlanID
		analysis["duration"] = time.Since(meta.StartedAt).Truncate(time.Second).String()
	}

	return analysis
}

func (o *Orchestrator) activeDAGs() map[string]*ActiveDAGMeta {
	if o.dagBridge == nil {
		return nil
	}
	o.dagBridge.mu.RLock()
	defer o.dagBridge.mu.RUnlock()
	return o.dagBridge.activeDAGs
}

// analyzeFromStore creates an analysis from persisted DAG execution data.
func analyzeFromStore(row *DAGExecutionRow) map[string]any {
	return map[string]any{
		"dag_id":          row.ID,
		"plan_id":         row.PlanID,
		"state":           row.State,
		"current_layer":   row.CurrentLayer,
		"total_layers":    row.TotalLayers,
		"total_nodes":     row.NodesTotal,
		"nodes_succeeded": row.NodesSucceeded,
		"nodes_failed":    row.NodesFailed,
		"nodes_skipped":   row.NodesSkipped,
		"source":          "store",
	}
}

// analyzePlanJSON analyzes a PlanHandoff JSON without executing it.
func analyzePlanJSON(planJSON string) (any, error) {
	var handoff architect.PlanHandoff
	if err := json.Unmarshal([]byte(planJSON), &handoff); err != nil {
		return nil, fmt.Errorf("invalid plan handoff JSON: %w", err)
	}

	taskSummaries := make([]map[string]any, 0, len(handoff.Tasks))
	complexityDist := map[string]int{}
	agentDist := map[string]int{}
	totalTokens := 0
	totalRisks := 0

	for _, t := range handoff.Tasks {
		summary := map[string]any{
			"id":         t.ID,
			"name":       t.Name,
			"agent_type": t.AgentType,
			"complexity": t.Complexity,
			"deps":       len(t.Dependencies),
			"files":      len(t.AffectedFiles),
			"tests":      len(t.TestRequirements),
			"risks":      len(t.RiskFactors),
			"acceptance": len(t.AcceptanceCriteria),
		}
		taskSummaries = append(taskSummaries, summary)
		complexityDist[t.Complexity]++
		agentDist[t.AgentType]++
		totalTokens += t.EstimatedTokens
		totalRisks += len(t.RiskFactors)
	}

	// Collect all risk factors.
	allRisks := make([]string, 0, totalRisks)
	for _, t := range handoff.Tasks {
		for _, rf := range t.RiskFactors {
			allRisks = append(allRisks, fmt.Sprintf("[%s] %s", t.ID, rf))
		}
	}

	return map[string]any{
		"plan_id":                 handoff.PlanID,
		"query":                   handoff.Query,
		"task_count":              len(handoff.Tasks),
		"layer_count":             len(handoff.ExecutionLayers),
		"execution_layers":        handoff.ExecutionLayers,
		"critical_path":           handoff.CriticalPath,
		"total_tokens":            totalTokens,
		"complexity_distribution": complexityDist,
		"agent_distribution":      agentDist,
		"tasks":                   taskSummaries,
		"risk_summary":            handoff.RiskSummary,
		"all_risk_factors":        allRisks,
		"source":                  "plan_json",
	}, nil
}

// isPlanHandoffJSON returns true if the input looks like a PlanHandoff JSON payload.
func isPlanHandoffJSON(input string) bool {
	trimmed := strings.TrimSpace(input)
	return strings.HasPrefix(trimmed, `{"plan_id":`) || strings.HasPrefix(trimmed, `{"plan_id" :`)
}

// tryIngestPlanFromInput attempts to parse and ingest a plan handoff from
// an incoming bus request input. Returns (result, true) if the input was
// a plan handoff, (nil, false) otherwise.
func (o *Orchestrator) tryIngestPlanFromInput(ctx context.Context, input string) (any, bool) {
	if !isPlanHandoffJSON(input) {
		// Log if it looks like it should have been a handoff (contains plan-like keys).
		if looksLikeMalformedHandoff(input) {
			slog.Warn("input looks like a malformed plan handoff — treating as conversation",
				"prefix", truncateForLog(input, 120))
		}
		return nil, false
	}
	result, err := o.ingestPlan(ctx, input)
	if err != nil {
		slog.Warn("plan ingestion failed", "error", err)
		return map[string]any{"error": err.Error(), "ingested": false}, true
	}
	return result, true
}

// looksLikeMalformedHandoff returns true if the input contains plan-related
// keys but failed the isPlanHandoffJSON prefix check. Used for diagnostics.
func looksLikeMalformedHandoff(input string) bool {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return false
	}
	return strings.Contains(trimmed, `"plan_id"`) ||
		strings.Contains(trimmed, `"tasks"`) ||
		strings.Contains(trimmed, `"execution_layers"`)
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// generateMessageIDForIngest generates a unique message ID for ingestion events.
func generateMessageIDForIngest() string {
	return "ingest_" + uuid.New().String()
}
