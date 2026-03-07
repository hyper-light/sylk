package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/resources"
	"github.com/google/uuid"
)

// protocolMaxToolRuns is the tool-call budget for the agent-driven planning
// protocol. The protocol needs ~6-8 skill calls (analyze, consult, design,
// generate_tasks, plus potentially ask_user_question, route_plan_acceptance,
// and handle_plan_acceptance_result). 16 provides headroom.
const protocolMaxToolRuns = 16

func (a *Architect) runPlanningProtocol(ctx context.Context, req *ArchitectRequest) (*DesignPlan, error) {
	a.logInfo("runPlanningProtocol: entry",
		"query", truncateString(req.Query, 120),
		"session_id", req.SessionID,
		"intent", string(req.Intent),
		"ctx_deadline", contextDeadlineString(ctx))

	req = a.enrichPlanningRequest(req)
	plan := newProtocolPlan(req)
	a.planStore.Upsert(plan)

	corrID := correlationIDFromContext(ctx)
	shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventProtocolStarted,
		a.id, req.SessionID, corrID, "info",
		&agentlog.ProtocolPayload{PlanID: plan.ID, Phase: "started"})

	// Derive operation budget from model parameters (no magic numbers).
	// stages * budgetLevels * perRequestTimeout
	opTimeout := a.operationTimeout()
	a.logInfo("runPlanningProtocol: operation timeout derived",
		"plan_id", plan.ID,
		"op_timeout", opTimeout.String(),
		"llm_request_timeout", a.config.LLMRequestTimeout.String())
	propagator := &resources.DeadlinePropagator{CleanupBuffer: 5 * time.Second}
	opCtx, opCancel := propagator.Propagate(ctx, opTimeout)
	defer opCancel()

	a.logInfo("runPlanningProtocol: op context created",
		"plan_id", plan.ID,
		"op_ctx_deadline", contextDeadlineString(opCtx))

	// Reset per-operation state: budget for fraction computation, pressure
	// debounce flag so each operation gets at most one TimePressure signal.
	a.resetPlannerOperationState(opTimeout)

	plannerCtx := withPlannerThoughtCallback(opCtx, func(stage string, thought string) {
		a.publishPlanThought(opCtx, stage, thought)
	})
	plannerCtx = withStreamRetryResetEmitter(plannerCtx, func() {
		a.publishPlanStreamStart(opCtx)
	})
	diag := openProtocolDiagnostics(plan.ID, a.config.WorkingDirectory)
	defer diag.close()
	plannerAvailable := a.ensurePlanner(ctx) != nil
	diag.log("protocol start plan=%s query=%q planner_available=%v op_timeout=%v",
		plan.ID, req.Query, plannerAvailable, opTimeout)

	// When no LLM planner is available, run the deterministic multi-stage
	// path directly. This allows planning to proceed with structural
	// fallbacks instead of failing at the tool loop planner check.
	if !plannerAvailable {
		return a.runDeterministicProtocol(plannerCtx, req, plan, diag)
	}

	runner := &planningProtocolRunner{architect: a, ctx: plannerCtx, request: req, plan: plan, diag: diag}
	protocolStart := time.Now()
	if err := runner.run(); err != nil {
		elapsed := time.Since(protocolStart)
		diag.log("protocol FAILED after %v: %v", elapsed, err)
		a.logWarn("runPlanningProtocol: FAILED",
			"plan_id", plan.ID,
			"elapsed", elapsed.String(),
			"plan_status", plan.SM().State().String(),
			"err", err)
		// Context cancellation from user interrupt: supersedeStalledPlans
		// already moved the plan to Superseded via the CancelEntry hook.
		// Fall through to retryable check or fail.

		// Last resort: when the LLM protocol exhausted all retries with
		// a retryable error (e.g., API persistently overloaded), fall
		// back to the deterministic protocol so the plan reaches Ready
		// instead of being marked Failed. The retry infrastructure
		// (server-guided backoff, proper delays) handles transient
		// overload; this path only fires when the API is truly unreachable.
		if providers.IsRetryable(err) {
			diag.log("retryable failure after %v, falling back to deterministic protocol", elapsed)
			a.logInfo("runPlanningProtocol: retryable failure, deterministic fallback",
				"plan_id", plan.ID, "elapsed", elapsed.String())
			plan.sm = NewPlanStateMachine(plan.ID, PlanStatusPending)
			plan.Status = PlanStatusPending
			plan.Epoch = plan.SM().Epoch()
			plan.UpdatedAt = time.Now()
			return a.runDeterministicProtocol(plannerCtx, req, plan, diag)
		}
		return a.failAndPersistPlan(plan, err)
	}
	plan.CompletedAt = time.Now()
	a.planStore.Upsert(plan)
	elapsed := time.Since(protocolStart)
	diag.log("protocol complete elapsed=%v tasks=%d components=%d",
		elapsed, len(plan.Tasks), len(plan.Architecture.Components))
	a.logInfo("runPlanningProtocol: complete",
		"plan_id", plan.ID,
		"elapsed", elapsed.String(),
		"tasks", len(plan.Tasks),
		"status", plan.SM().State().String())

	shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventProtocolCompleted,
		a.id, req.SessionID, corrID, "info",
		&agentlog.ProtocolPayload{PlanID: plan.ID, Phase: "completed", DurNs: elapsed.Nanoseconds()})

	return plan, nil
}

// runDeterministicProtocol executes the multi-stage planning path using
// deterministic fallbacks (analyzeRequirements → designArchitecture →
// generateAtomicTasks → createWorkflowDAG). Used when no LLM planner is
// configured so the planning protocol can still produce structural plans.
func (a *Architect) runDeterministicProtocol(
	ctx context.Context,
	req *ArchitectRequest,
	plan *DesignPlan,
	diag *protocolDiagnostics,
) (*DesignPlan, error) {
	protocolStart := time.Now()
	diag.log("deterministic protocol start plan=%s", plan.ID)

	transition := func(status PlanStatus) error {
		if err := plan.SM().TransitionTo(status, plan); err != nil {
			return err
		}
		plan.Status = plan.SM().State()
		plan.Epoch = plan.SM().Epoch()
		plan.UpdatedAt = time.Now()
		return a.planStore.Upsert(plan)
	}

	// 1. Analyze
	if err := transition(PlanStatusAnalyzing); err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	requirements, err := a.analyzeRequirements(ctx, req.Query, req.Params)
	if err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	plan.Requirements = requirements

	// 2. Consult (skip — no bus in deterministic mode)
	if err := transition(PlanStatusConsulting); err != nil {
		return a.failAndPersistPlan(plan, err)
	}

	// 3. Design
	if err := transition(PlanStatusDesigning); err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	architecture, err := a.designArchitecture(ctx, requirements, nil)
	if err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	plan.Architecture = architecture

	// 4. Generate tasks
	if err := transition(PlanStatusGenerating); err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	tasks, err := a.generateAtomicTasks(ctx, architecture, plan.Constraints)
	if err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	plan.Tasks = tasks

	// 5. Orchestrate
	if err := transition(PlanStatusOrchestrating); err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	workflow, err := a.createWorkflowDAG(ctx, tasks)
	if err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	plan.Workflow = workflow

	// 6. Ready
	if err := transition(PlanStatusReady); err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	plan.LeaseExpiry = time.Now().Add(ReadyPlanMaxAge)
	a.planStore.Upsert(plan)

	elapsed := time.Since(protocolStart)
	diag.log("deterministic protocol complete elapsed=%v tasks=%d", elapsed, len(tasks))
	a.logInfo("runDeterministicProtocol: complete",
		"plan_id", plan.ID, "elapsed", elapsed.String(), "tasks", len(tasks))
	return plan, nil
}

func (a *Architect) enrichPlanningRequest(req *ArchitectRequest) *ArchitectRequest {
	if req == nil {
		return nil
	}
	if a == nil {
		return req
	}
	clone := *req
	clone.Params = cloneParams(req.Params)
	if len(clone.ConversationHistory) > 0 {
		clone.Params["conversation_history"] = formatConversationHistory(clone.ConversationHistory)
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return &clone
	}
	context := a.priorSessionContext(req.SessionID)
	if len(context) == 0 {
		return &clone
	}
	clone.Params["session_context"] = context
	return &clone
}

func formatConversationHistory(turns []guide.ConversationTurn) string {
	var b strings.Builder
	for i, turn := range turns {
		fmt.Fprintf(&b, "Turn %d:\n  User: %s\n  Agent: %s\n", i+1, turn.UserInput, turn.AgentReply)
	}
	return b.String()
}

func cloneParams(values map[string]any) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (a *Architect) priorSessionContext(sessionID string) map[string]any {
	plan := a.latestHistoricalPlanForSession(sessionID)
	if plan == nil {
		return nil
	}
	context := map[string]any{
		"prior_plan_query":  plan.Query,
		"prior_plan_status": plan.Status.String(),
	}
	if plan.Requirements != nil {
		context["prior_scope"] = plan.Requirements.Scope
		context["prior_goals"] = append([]string(nil), plan.Requirements.Goals...)
		context["prior_constraints"] = append([]string(nil), plan.Requirements.Constraints...)
	}
	if len(plan.ClarificationQuestions) > 0 {
		context["prior_clarification_questions"] = append([]string(nil), plan.ClarificationQuestions...)
	}
	if len(plan.Assumptions) > 0 {
		context["prior_assumptions"] = append([]string(nil), plan.Assumptions...)
	}
	return context
}

func newProtocolPlan(req *ArchitectRequest) *DesignPlan {
	if req == nil {
		plan := &DesignPlan{ID: uuid.NewString(), Status: PlanStatusFailed, Error: "nil request"}
		plan.sm = NewPlanStateMachine(plan.ID, PlanStatusFailed)
		return plan
	}
	now := time.Now()
	plan := &DesignPlan{
		ID:            uuid.NewString(),
		SessionID:     req.SessionID,
		Query:         req.Query,
		Status:        PlanStatusPending,
		Revision:      1,
		Constraints:   extractConstraints(req.Params),
		Consultations: map[string]*ConsultationEvidence{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusPending)
	return plan
}

func (a *Architect) failAndPersistPlan(plan *DesignPlan, err error) (*DesignPlan, error) {
	failed, runErr := failPlan(plan, err)
	a.persistPlanState(failed)
	return failed, runErr
}

type planningProtocolRunner struct {
	architect *Architect
	ctx       context.Context
	request   *ArchitectRequest
	plan      *DesignPlan
	diag      *protocolDiagnostics
}

// protocolDiagnostics writes a structured log file per plan for post-mortem
// debugging. Safe to use as a zero-value (all methods are no-ops on nil file).
type protocolDiagnostics struct {
	file *os.File
}

func openProtocolDiagnostics(planID, baseDir string) *protocolDiagnostics {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	dir := filepath.Join(baseDir, ".sylk", "architect", "diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &protocolDiagnostics{}
	}
	name := filepath.Join(dir, "protocol_"+planID+".log")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return &protocolDiagnostics{}
	}
	return &protocolDiagnostics{file: f}
}

func (d *protocolDiagnostics) log(format string, args ...any) {
	if d == nil || d.file == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(d.file, "[%s] %s\n", ts, msg)
}

func (d *protocolDiagnostics) close() {
	if d != nil && d.file != nil {
		d.file.Close()
	}
}

// run launches the agent-driven tool loop where the LLM drives the planning
// protocol by invoking plan-aware skills. The state machine validates
// transitions as a guardrail rather than controlling the flow.
func (r *planningProtocolRunner) run() error {
	prompt := r.buildProtocolPrompt()
	system := r.architect.buildProtocolSystemPrompt()
	r.architect.ensureToolLoopSkillsLoaded()
	tools := r.architect.buildToolDefinitions()
	if len(tools) == 0 {
		return fmt.Errorf("no tool definitions available for protocol")
	}

	// Emit stream start so the TUI shows an active thinking indicator.
	r.architect.publishPlanStreamStart(r.ctx)

	loopCtx := withToolRunsOverride(r.ctx, protocolMaxToolRuns)
	req := &providers.Request{
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: prompt}},
		MaxTokens:    r.architect.config.MaxOutputTokens,
		SystemPrompt: system,
		Tools:        tools,
	}
	r.architect.applyProtocolRuntimeProfile(req)

	text, err := r.architect.executeToolLoop(
		loopCtx, req, "planning_protocol",
		func(chunk string) { r.architect.publishPlanStreamChunk(r.ctx, chunk) },
		shared.SteeringLedgerFromContext(loopCtx),
	)
	if err != nil {
		return err
	}
	// If the LLM invoked ask_user_question, the plan is now Clarifying.
	if r.plan.IsClarifying() {
		return nil
	}
	// The tool loop's final text turn is the plan presentation.
	if r.plan.SM().State() == PlanStatusReady && strings.TrimSpace(text) != "" {
		r.plan.UserResponse = sanitizePlannerConversationResponse(text)
		return r.architect.planStore.Upsert(r.plan)
	}
	return nil
}

// protocolPhaseInstructions is the embedded constant that drives the LLM's
// planning protocol behavior within the tool loop.
const protocolPhaseInstructions = `You drive the planning protocol by invoking skills. Execute these phases in order:

1. **Analyze**: Invoke plan with action=analyze, plan_id, and the user's query.
2. **Consult**: Invoke consult with mode=pre_planning and plan_id.
3. **Clarify** (conditional): Review the analysis results. If critical ambiguities
   would lead to a wrong plan, invoke ask_user_question. If you do, STOP and do
   not invoke further skills. If requirements are clear, proceed to Design.
4. **Design**: Invoke plan with action=design and plan_id.
5. **Generate**: Invoke plan with action=generate_tasks and plan_id.
   This automatically creates the workflow and validates the plan.
6. **Present and ask for approval**: The system renders the structured plan separately
   in the UI — the user already sees it. Do NOT repeat, re-render, or include the plan
   structure, task list, acceptance criteria, file lists, or implementation guides in
   your text. Write ONLY a brief assessment (2-4 sentences): highlight the key
   architectural tradeoff, the primary risk, and why this decomposition is a good default.
   Sound like a principal engineer, not a workflow bot.
   End by inviting the user to approve or request changes. Use your own natural phrasing —
   do NOT use a scripted template. Vary your wording each time.
   Do NOT invoke route_plan_acceptance — wait for the user to respond.

Always pass plan_id to plan and consult skills. Do not skip phases 1, 2, 4, or 5.

Exception — auto-approve (when approval_required is false):
Skip the approval cue in step 6. Instead, after the brief assessment, invoke
route_plan_acceptance with the plan_id and a brief user_response summary. When
route_plan_acceptance returns the Guide's verdict, invoke handle_plan_acceptance_result
with the verdict. On "accept", this dispatches the plan to the orchestrator automatically.`

// buildProtocolPrompt constructs the user message that seeds the tool loop.
func (r *planningProtocolRunner) buildProtocolPrompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan ID: %s\n", r.plan.ID)
	fmt.Fprintf(&b, "Session ID: %s\n", r.plan.SessionID)
	fmt.Fprintf(&b, "User query: %s\n", r.request.Query)
	fmt.Fprintf(&b, "Approval required: %v\n", !r.architect.config.AutoApprove)
	if sessionCtx, ok := r.request.Params["session_context"]; ok {
		contextJSON, _ := json.Marshal(sessionCtx)
		fmt.Fprintf(&b, "\nSession context:\n%s\n", string(contextJSON))
	}
	if history, ok := r.request.Params["conversation_history"].(string); ok && history != "" {
		fmt.Fprintf(&b, "\nConversation history:\n%s\n", history)
	}
	b.WriteString("\nBegin by invoking plan with action=analyze.")
	return b.String()
}

// buildProtocolSystemPrompt constructs the system prompt for the agent-driven
// planning loop, combining the core architect identity with protocol phase
// instructions.
func (a *Architect) buildProtocolSystemPrompt() string {
	modules := []string{
		ArchitectSystemCorePrompt,
		protocolPhaseInstructions,
		ArchitectSystemGuardrailsPrompt,
	}
	return strings.Join(nonEmptyPlannerSections(modules), "\n\n---\n\n")
}

// extractLibrarianPatterns builds CodebasePatterns from the consultation
// evidence already collected by enforceConsultationGate.
func extractLibrarianPatterns(plan *DesignPlan) *CodebasePatterns {
	if plan == nil || plan.Consultations == nil {
		return emptyCodebasePatterns()
	}
	evidence, ok := plan.Consultations["librarian"]
	if !ok || evidence == nil || !evidence.Success {
		return emptyCodebasePatterns()
	}
	return codebasePatternsFromEvidence(evidence)
}

func (r *planningProtocolRunner) validateDeclarationForPolicy(declaration *PreDelegationDeclaration) error {
	if r.architect == nil {
		return fmt.Errorf("architect is nil")
	}
	if declaration == nil || len(declaration.ConsultationChecks) == 0 {
		return nil
	}
	if err := r.architect.validateDeclaration(declaration); err != nil {
		r.plan.RiskSummary = append(r.plan.RiskSummary, "declaration validation warning: "+err.Error())
	}
	return nil
}

func buildAutoDeclaration(plan *DesignPlan) *PreDelegationDeclaration {
	requiredSkills := collectPlanRequiredSkills(plan)
	return &PreDelegationDeclaration{
		ID:                 "decl_" + uuid.NewString(),
		PlanID:             safePlanID(plan),
		TargetAgent:        "orchestrator",
		Reasoning:          "Auto-generated declaration for validated planning handoff.",
		RequiredSkills:     requiredSkills,
		ExpectedOutcome:    "Workflow executes tasks in dependency order with traceable status.",
		FailureCriteria:    "Any task failure, unresolved dependency, or execution timeout.",
		ConsultationChecks: cloneConsultationChecks(plan),
		CreatedAt:          time.Now(),
	}
}

func collectPlanRequiredSkills(plan *DesignPlan) []string {
	if plan == nil || len(plan.Tasks) == 0 {
		return []string{"planning", "orchestration"}
	}
	set := map[string]struct{}{"planning": {}, "orchestration": {}}
	for _, task := range plan.Tasks {
		if task == nil {
			continue
		}
		agentType := strings.TrimSpace(task.AgentType)
		if agentType == "" {
			continue
		}
		set[agentType] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func cloneConsultationChecks(plan *DesignPlan) map[string]*ConsultationEvidence {
	checks := map[string]*ConsultationEvidence{}
	if plan == nil {
		return checks
	}
	for key, evidence := range plan.Consultations {
		checks[key] = evidence
	}
	return checks
}

func isHandoffSuccess(msg *guide.Message) bool {
	if msg == nil {
		return false
	}
	resp, ok := msg.GetRouteResponse()
	return ok && resp != nil && resp.Success
}

func summarizeAutoHandoffResponse(msg *guide.Message) string {
	if msg == nil {
		return "Plan dispatched to orchestrator."
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		if errText, ok := msg.GetError(); ok {
			return "Handoff error: " + strings.TrimSpace(errText)
		}
		return "Plan dispatched to orchestrator."
	}
	if !resp.Success {
		return "Handoff response error: " + strings.TrimSpace(resp.Error)
	}
	// Extract the orchestrator's user-facing response if available.
	if text := extractHandoffUserResponse(resp.Data); text != "" {
		return text
	}
	return "Plan accepted by orchestrator — DAG execution started."
}

// extractHandoffUserResponse extracts the user-facing text from the
// orchestrator's ingestion response. The bus is in-memory so the data
// arrives as a struct pointer (not a map). We try map assertion first
// (cheap), then JSON round-trip to handle cross-package struct types
// that the architect cannot import directly.
func extractHandoffUserResponse(data any) string {
	if data == nil {
		return ""
	}
	// Fast path: already a map (e.g. from deserialized payloads).
	if m, ok := data.(map[string]any); ok {
		if resp, ok := m["response"].(string); ok && strings.TrimSpace(resp) != "" {
			return strings.TrimSpace(resp)
		}
	}
	// Struct path: JSON round-trip for cross-package types (e.g.
	// *orchestrator.ConversationResult) that arrive as struct pointers
	// through the in-memory bus.
	encoded, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(encoded, &m) != nil {
		return ""
	}
	if resp, ok := m["response"].(string); ok && strings.TrimSpace(resp) != "" {
		return strings.TrimSpace(resp)
	}
	return ""
}

func (r *planningProtocolRunner) transition(status PlanStatus) error {
	if err := r.plan.SM().TransitionTo(status, r.plan); err != nil {
		return err
	}
	r.plan.Status = r.plan.SM().State() // sync cache for JSON
	r.plan.Epoch = r.plan.SM().Epoch()  // sync epoch for JSON
	r.plan.UpdatedAt = time.Now()
	r.architect.publishPlanStreamProgress(r.ctx, status)
	return r.architect.planStore.Upsert(r.plan)
}

func validatePlanForExecution(plan *DesignPlan) error {
	checks := []func(*DesignPlan) error{
		validatePlanCoreFields,
		validatePlanTasks,
		validatePlanWorkflow,
	}
	for _, check := range checks {
		if err := check(plan); err != nil {
			return err
		}
	}
	return nil
}

func validatePlanCoreFields(plan *DesignPlan) error {
	if plan == nil {
		return fmt.Errorf("plan is required")
	}
	if plan.Requirements == nil {
		return fmt.Errorf("requirements are required")
	}
	if plan.Architecture == nil {
		return fmt.Errorf("architecture is required")
	}
	if plan.Constraints == nil {
		return fmt.Errorf("constraints are required")
	}
	return nil
}

func validatePlanTasks(plan *DesignPlan) error {
	if plan == nil || len(plan.Tasks) == 0 {
		return fmt.Errorf("at least one task is required")
	}
	ids := make(map[string]struct{}, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if err := validateTaskContract(task); err != nil {
			return err
		}
		if _, exists := ids[task.ID]; exists {
			return fmt.Errorf("duplicate task id: %s", task.ID)
		}
		ids[task.ID] = struct{}{}
	}
	for _, task := range plan.Tasks {
		for _, dependency := range task.Dependencies {
			if _, ok := ids[dependency]; ok {
				continue
			}
			return fmt.Errorf("task %s has unknown dependency %s", task.ID, dependency)
		}
	}
	return nil
}

func validateTaskContract(task *AtomicTask) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if strings.TrimSpace(task.ID) == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(task.Description) == "" {
		return fmt.Errorf("task %s description is required", task.ID)
	}
	if len(task.SuccessCriteria) == 0 {
		return fmt.Errorf("task %s success criteria are required", task.ID)
	}
	if len(task.AcceptanceCriteria) == 0 {
		return fmt.Errorf("task %s acceptance criteria are required", task.ID)
	}
	if strings.TrimSpace(task.ImplementationGuide) == "" {
		return fmt.Errorf("task %s implementation guide is required", task.ID)
	}
	if len(task.AffectedFiles) == 0 {
		return fmt.Errorf("task %s affected files are required", task.ID)
	}
	return nil
}

func validatePlanWorkflow(plan *DesignPlan) error {
	if plan == nil || plan.Workflow == nil {
		return fmt.Errorf("workflow is required")
	}
	if plan.Workflow.DAG == nil {
		return fmt.Errorf("workflow dag is required")
	}
	if len(plan.Workflow.Tasks) != len(plan.Tasks) {
		return fmt.Errorf("workflow task count mismatch")
	}
	if plan.Workflow.TotalTasks != len(plan.Tasks) {
		return fmt.Errorf("workflow total task metadata mismatch")
	}
	return nil
}

// persistPlanState atomically upserts the plan to the PlanStore (in-memory + disk).
// Kept as a convenience wrapper for call sites that need an error return.
func (a *Architect) persistPlanState(plan *DesignPlan) error {
	return a.planStore.Upsert(plan)
}
