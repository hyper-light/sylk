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
	a.persistPlanState(plan)

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
	diag.log("protocol start plan=%s query=%q planner_available=%v op_timeout=%v",
		plan.ID, req.Query, a.ensurePlanner(ctx) != nil, opTimeout)
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
		// On context cancellation, preserve the plan's current state
		// instead of marking it Failed. This allows the next conversation
		// turn to detect and resume the in-progress plan.
		if ctx.Err() != nil && plan.SM().State() != PlanStatusPending {
			a.logInfo("runPlanningProtocol: interrupted, preserving plan state",
				"plan_id", plan.ID,
				"preserved_status", plan.SM().State().String())
			a.persistPlanState(plan)
			return plan, err
		}
		return a.failAndPersistPlan(plan, err)
	}
	plan.CompletedAt = time.Now()
	a.persistPlanState(plan)
	elapsed := time.Since(protocolStart)
	diag.log("protocol complete elapsed=%v tasks=%d components=%d",
		elapsed, len(plan.Tasks), len(plan.Architecture.Components))
	a.logInfo("runPlanningProtocol: complete",
		"plan_id", plan.ID,
		"elapsed", elapsed.String(),
		"tasks", len(plan.Tasks),
		"status", plan.SM().State().String())
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

	text, err := r.architect.executeToolLoop(
		loopCtx, req, "planning_protocol",
		func(chunk string) { r.architect.publishPlanStreamChunk(r.ctx, chunk) },
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
		return r.architect.persistPlanState(r.plan)
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
6. **Present and ask for approval**: The structured plan is automatically rendered in your
   response when generate_tasks completes. Do NOT repeat the plan structure or task list.
   Write a brief assessment (2-4 sentences): highlight the key architectural tradeoff,
   the primary risk, and why this decomposition is a good default.
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
	return r.architect.persistPlanState(r.plan)
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

func (a *Architect) persistPlanState(plan *DesignPlan) error {
	a.upsertActivePlan(plan)
	return a.persistPlanSnapshot(plan)
}

func (a *Architect) upsertActivePlan(plan *DesignPlan) {
	if a == nil || plan == nil || strings.TrimSpace(plan.ID) == "" {
		return
	}
	a.activePlansMu.Lock()
	defer a.activePlansMu.Unlock()
	a.activePlans[plan.ID] = plan
}

func (a *Architect) persistPlanSnapshot(plan *DesignPlan) error {
	if plan == nil {
		return nil
	}
	encoded, err := a.marshalPlanSnapshot(plan)
	if err != nil {
		return err
	}
	return a.persistEncodedPlanSnapshot(plan.ID, plan.SessionID, encoded)
}

func (a *Architect) marshalPlanSnapshot(plan *DesignPlan) ([]byte, error) {
	if a == nil || plan == nil || strings.TrimSpace(plan.ID) == "" {
		return nil, nil
	}
	return json.MarshalIndent(plan, "", "  ")
}

func (a *Architect) persistEncodedPlanSnapshot(planID, sessionID string, encoded []byte) error {
	if a == nil || strings.TrimSpace(planID) == "" || len(encoded) == 0 {
		return nil
	}
	dir := a.planStoreDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	trimmedID := strings.TrimSpace(planID)
	if trimmedID == "" {
		return nil
	}
	finalPath := filepath.Join(dir, trimmedID+".json")
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}

func (a *Architect) planStoreDir(sessionID string) string {
	base := a.config.WorkingDirectory
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = "default"
	}
	return filepath.Join(base, ".sylk", "sessions", sessionID, "agents", "architect", "plans")
}

// restoreMaxAge is the maximum age of a persisted plan eligible for restore.
// Plans older than this are stale artifacts from prior sessions.
const restoreMaxAge = 24 * time.Hour

// restoreMaxPlans caps the number of plans restored on startup to prevent
// unbounded memory growth from accumulated plan files.
const restoreMaxPlans = 32

func (a *Architect) restorePersistedPlans() error {
	base := a.config.WorkingDirectory
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	sessionsDir := filepath.Join(base, ".sylk", "sessions")
	sessionEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-restoreMaxAge)
	restored := 0
	skipped := 0
	for _, sessionEntry := range sessionEntries {
		if !sessionEntry.IsDir() {
			continue
		}
		planDir := filepath.Join(sessionsDir, sessionEntry.Name(), "agents", "architect", "plans")
		entries, readErr := os.ReadDir(planDir)
		if readErr != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			if restored >= restoreMaxPlans {
				_ = os.Remove(filepath.Join(planDir, entry.Name()))
				skipped++
				continue
			}
			path := filepath.Join(planDir, entry.Name())
			if ok, restoreErr := a.restorePlanFromFile(path, cutoff); restoreErr != nil {
				a.logger.Warn("failed to restore plan", "path", path, "error", restoreErr)
				continue
			} else if ok {
				restored++
			} else {
				skipped++
			}
		}
	}
	a.logInfo("restorePersistedPlans: done",
		"sessions_dir", sessionsDir, "restored", restored, "skipped", skipped)
	return nil
}

func (a *Architect) restorePlanFromFile(path string, cutoff time.Time) (bool, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var plan DesignPlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return false, err
	}
	if strings.TrimSpace(plan.ID) == "" {
		return false, fmt.Errorf("restored plan missing id")
	}
	// Skip terminal-state plans, actively executing plans, and stale ready/completed
	// plans. Ready plans from prior sessions are stale artifacts — the Guide's phase
	// gate (which arms plan approval) is in-memory-only and lost on restart.
	switch plan.Status {
	case PlanStatusFailed, PlanStatusExecuting, PlanStatusReady, PlanStatusCompleted:
		_ = os.Remove(path) // Proactively clean up terminal/stale plan files.
		return false, nil
	}
	if plan.UpdatedAt.Before(cutoff) {
		_ = os.Remove(path) // Proactively clean up old plan files.
		return false, nil
	}
	plan.sm = NewPlanStateMachineWithEpoch(plan.ID, plan.Status, plan.Epoch)
	a.logInfo("restorePlanFromFile",
		"plan_id", plan.ID,
		"status", plan.Status.String(),
		"query", truncateString(plan.Query, 80),
		"tasks", len(plan.Tasks),
		"created_at", plan.CreatedAt.String())
	a.upsertActivePlan(&plan)
	return true, nil
}
