package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
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
	var handoff architect.PlanHandoff
	if err := json.Unmarshal([]byte(planJSON), &handoff); err != nil {
		return nil, fmt.Errorf("invalid plan handoff JSON: %w", err)
	}

	if err := validateHandoff(&handoff); err != nil {
		return nil, fmt.Errorf("plan handoff validation failed: %w", err)
	}

	// Build orchestrator state from the handoff.
	wfID := "wf_" + handoff.PlanID
	o.createWorkflowAndTasks(&handoff, wfID)

	// Build DAG from task graph.
	d, err := buildDAGFromHandoff(&handoff)
	if err != nil {
		return nil, fmt.Errorf("dag construction failed: %w", err)
	}

	// Submit to DAG bridge for execution.
	if o.dagBridge == nil {
		return nil, fmt.Errorf("dag bridge unavailable (no project directory)")
	}
	dagID, err := o.dagBridge.Execute(ctx, d, handoff.PlanID, handoff.SessionID)
	if err != nil {
		return nil, fmt.Errorf("dag execution failed: %w", err)
	}

	// Notify the LLM loop about the plan ingestion so it can analyze the DAG.
	o.pushEvent(&busEvent{
		Topic:     "plan.ingested",
		Timestamp: time.Now(),
		Severity:  severityInfo,
		Summary:   fmt.Sprintf("Plan %s ingested: %d tasks, %d layers, DAG %s", handoff.PlanID, len(handoff.Tasks), len(handoff.ExecutionLayers), dagID),
		Data: map[string]any{
			"plan_id":     handoff.PlanID,
			"dag_id":      dagID,
			"task_count":  len(handoff.Tasks),
			"layer_count": len(handoff.ExecutionLayers),
			"trigger":     handoff.Trigger,
		},
	})

	return map[string]any{
		"ingested":    true,
		"plan_id":     handoff.PlanID,
		"dag_id":      dagID,
		"workflow_id":  wfID,
		"task_count":  len(handoff.Tasks),
		"layer_count": len(handoff.ExecutionLayers),
	}, nil
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
	}
	return nil
}

// createWorkflowAndTasks populates orchestrator state with workflow and task records.
func (o *Orchestrator) createWorkflowAndTasks(h *architect.PlanHandoff, wfID string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	now := time.Now()
	taskIDs := make([]string, 0, len(h.Tasks))

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
				"agent_type":           ht.AgentType,
				"complexity":           ht.Complexity,
				"estimated_tokens":     ht.EstimatedTokens,
				"acceptance_criteria":  ht.AcceptanceCriteria,
				"guidelines":           ht.Guidelines,
				"implementation_guide": ht.ImplementationGuide,
				"affected_files":       ht.AffectedFiles,
				"test_requirements":    ht.TestRequirements,
				"risk_factors":         ht.RiskFactors,
			},
		}
	}

	o.state.Workflows[wfID] = &WorkflowState{
		ID:             wfID,
		Name:           fmt.Sprintf("Plan %s", h.PlanID),
		Description:    h.Query,
		Status:         WorkflowStatusRunning,
		TaskIDs:        taskIDs,
		PendingIDs:     taskIDs,
		StartedAt:      now,
		UpdatedAt:      now,
		LeadAgentID:    "architect",
		SessionID:      h.SessionID,
		Metadata: map[string]any{
			"plan_id":          h.PlanID,
			"revision":         h.Revision,
			"trigger":          h.Trigger,
			"total_tokens":     h.TotalTokens,
			"critical_path":    h.CriticalPath,
			"execution_layers": h.ExecutionLayers,
		},
	}

	o.state.Stats.TotalWorkflows++
	o.state.Stats.ActiveWorkflows++
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
			DefaultRetries: 3,
			RetryBackoff:   time.Second,
		}
		builder = builder.WithPolicy(policy)
	}

	builder = builder.WithMetadata("plan_id", h.PlanID)
	builder = builder.WithMetadata("session_id", h.SessionID)

	for _, ht := range h.Tasks {
		prompt := buildNodePrompt(ht)
		nodeCtx := buildNodeContext(ht)
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
			fmt.Fprintf(&b, "### %s\n```\n%s\n```\n%s\n\n", ex.Label, ex.Code, ex.Explanation)
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
func buildNodeContext(ht *architect.HandoffTask) map[string]any {
	ctx := map[string]any{
		"task_id":          ht.ID,
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
	if len(ht.CoAgents) > 0 {
		ctx["co_agents"] = ht.CoAgents
		ctx["collaboration_mode"] = ht.CollaborationMode
	}
	if len(ht.AgentScopes) > 0 {
		ctx["agent_scopes"] = ht.AgentScopes
	}
	return ctx
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
			"id":          t.ID,
			"name":        t.Name,
			"agent_type":  t.AgentType,
			"complexity":  t.Complexity,
			"deps":        len(t.Dependencies),
			"files":       len(t.AffectedFiles),
			"tests":       len(t.TestRequirements),
			"risks":       len(t.RiskFactors),
			"acceptance":  len(t.AcceptanceCriteria),
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
		"plan_id":             handoff.PlanID,
		"query":               handoff.Query,
		"task_count":          len(handoff.Tasks),
		"layer_count":         len(handoff.ExecutionLayers),
		"execution_layers":    handoff.ExecutionLayers,
		"critical_path":       handoff.CriticalPath,
		"total_tokens":        totalTokens,
		"complexity_distribution": complexityDist,
		"agent_distribution":  agentDist,
		"tasks":               taskSummaries,
		"risk_summary":        handoff.RiskSummary,
		"all_risk_factors":    allRisks,
		"source":              "plan_json",
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
