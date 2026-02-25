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
	"github.com/google/uuid"
)

func (a *Architect) runPlanningProtocol(ctx context.Context, req *ArchitectRequest) (*DesignPlan, error) {
	req = a.enrichPlanningRequest(req)
	plan := newProtocolPlan(req)
	a.persistPlanState(plan)
	plannerCtx := withPlannerThoughtCallback(ctx, func(stage string, thought string) {
		a.publishPlanThought(ctx, stage, thought)
	})
	plannerCtx = withStreamRetryResetEmitter(plannerCtx, func() {
		a.publishPlanStreamStart(ctx)
	})
	diag := openProtocolDiagnostics(plan.ID, a.config.WorkingDirectory)
	defer diag.close()
	diag.log("protocol start plan=%s query=%q planner_available=%v",
		plan.ID, req.Query, a.ensurePlanner() != nil)
	runner := &planningProtocolRunner{architect: a, ctx: plannerCtx, request: req, plan: plan, diag: diag}
	if err := runner.run(); err != nil {
		diag.log("protocol FAILED: %v", err)
		return a.failAndPersistPlan(plan, err)
	}
	plan.CompletedAt = time.Now()
	a.persistPlanState(plan)
	diag.log("protocol complete tasks=%d components=%d", len(plan.Tasks), len(plan.Architecture.Components))
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
		return &DesignPlan{ID: uuid.NewString(), Status: PlanStatusFailed, Error: "nil request"}
	}
	now := time.Now()
	return &DesignPlan{
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

func (r *planningProtocolRunner) run() error {
	if err := r.stepAnalyze(); err != nil {
		return err
	}
	if err := r.stepConsult(); err != nil {
		return err
	}
	needsClarification, err := r.stepClarify()
	if err != nil {
		return err
	}
	if needsClarification {
		return nil
	}
	for _, step := range r.remainingExecutionSteps() {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (r *planningProtocolRunner) remainingExecutionSteps() []func() error {
	return []func() error{
		r.stepDesign,
		r.stepGenerate,
		r.stepWorkflow,
		r.stepValidate,
		r.stepReady,
		r.stepAutoHandoff,
	}
}

func (r *planningProtocolRunner) stepAnalyze() error {
	if err := r.transition(PlanStatusAnalyzing); err != nil {
		return err
	}
	requirements, err := r.architect.analyzeRequirements(r.ctx, r.request.Query, r.request.Params)
	if err != nil {
		r.diag.log("stepAnalyze FAILED: %v", err)
		return err
	}
	r.diag.log("stepAnalyze goals=%d scope=%q constraints=%d",
		len(requirements.Goals), requirements.Scope, len(requirements.Constraints))
	for i, g := range requirements.Goals {
		r.diag.log("  goal[%d]: %q", i, g)
	}
	r.plan.Requirements = requirements
	return r.architect.persistPlanState(r.plan)
}

func (r *planningProtocolRunner) stepConsult() error {
	if err := r.transition(PlanStatusConsulting); err != nil {
		return err
	}
	r.diag.log("stepConsult: enforcing consultation gate")
	if err := r.architect.enforceConsultationGate(r.ctx, r.plan, r.request); err != nil {
		r.diag.log("stepConsult: consultation gate FAILED: %v", err)
		return err
	}
	r.diag.log("stepConsult: consultation gate passed consultations=%d",
		len(r.plan.Consultations))
	// Extract librarian patterns from the gate evidence rather than
	// issuing a redundant second consultation request.
	r.plan.CodebasePatterns = extractLibrarianPatterns(r.plan)
	return r.architect.persistPlanState(r.plan)
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

func (r *planningProtocolRunner) stepDesign() error {
	if err := r.transition(PlanStatusDesigning); err != nil {
		return err
	}
	architecture, err := r.architect.designArchitecture(r.ctx, r.plan.Requirements, r.plan.CodebasePatterns)
	if err != nil {
		r.diag.log("stepDesign FAILED: %v", err)
		return err
	}
	r.diag.log("stepDesign name=%q components=%d patterns=%d",
		architecture.Name, len(architecture.Components), len(architecture.Patterns))
	for i, c := range architecture.Components {
		r.diag.log("  component[%d]: name=%q type=%q desc=%q",
			i, c.Name, c.Type, truncateString(c.Description, 80))
	}
	r.plan.Architecture = architecture
	return r.architect.persistPlanState(r.plan)
}

func (r *planningProtocolRunner) stepGenerate() error {
	if err := r.transition(PlanStatusGenerating); err != nil {
		return err
	}
	tasks, err := r.architect.generateAtomicTasks(r.ctx, r.plan.Architecture, r.plan.Constraints)
	if err != nil {
		r.diag.log("stepGenerate FAILED: %v", err)
		return err
	}
	r.diag.log("stepGenerate tasks=%d", len(tasks))
	for i, t := range tasks {
		r.diag.log("  task[%d]: id=%q name=%q agent=%q deps=%v ac=%d files=%d guide_len=%d",
			i, t.ID, t.Name, t.AgentType, t.Dependencies,
			len(t.AcceptanceCriteria), len(t.AffectedFiles), len(t.ImplementationGuide))
	}
	r.plan.Tasks = tasks
	return r.architect.persistPlanState(r.plan)
}

func (r *planningProtocolRunner) stepWorkflow() error {
	if err := r.transition(PlanStatusOrchestrating); err != nil {
		return err
	}
	workflow, err := r.architect.createWorkflowDAG(r.ctx, r.plan.Tasks)
	if err != nil {
		return err
	}
	r.plan.Workflow = workflow
	return r.architect.persistPlanState(r.plan)
}

func (r *planningProtocolRunner) stepValidate() error {
	if err := validatePlanForExecution(r.plan); err != nil {
		return err
	}
	declaration := buildAutoDeclaration(r.plan)
	if err := r.validateDeclarationForPolicy(declaration); err != nil {
		return err
	}
	r.plan.Declarations = append(r.plan.Declarations, declaration)
	r.architect.publishDeclaration(declaration, r.plan.SessionID)
	return r.architect.persistPlanState(r.plan)
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

func (r *planningProtocolRunner) stepReady() error {
	if err := r.transition(PlanStatusReady); err != nil {
		return err
	}
	// Publish full plan snapshot for the inline chat widget.
	r.architect.publishPlanSnapshot(r.ctx, r.plan)
	// Stream LLM commentary + readiness footer token-by-token.
	r.plan.UserResponse = r.architect.readyUserResponseInline(r.ctx, r.request, r.plan)
	// Set the response directive so the Guide enters plan-approval phase.
	r.plan.ReadyDirective = &guide.ResponseDirective{
		Phase:    guide.PhasePlanApproval,
		AgentID:  "architect",
		Metadata: map[string]any{"plan_id": r.plan.ID},
		TTL:      readyPlanMaxAge,
	}
	return r.architect.persistPlanState(r.plan)
}

func (r *planningProtocolRunner) stepAutoHandoff() error {
	if !r.architect.running || r.architect.bus == nil {
		r.diag.log("stepAutoHandoff: bus unavailable, plan stays Ready")
		r.architect.publishPlanStreamChunk(r.ctx,
			"\n\nOrchestrator bus is unavailable — plan stays ready. Say **go ahead** to retry.")
		return nil
	}

	payload := buildHandoffPayload(r.plan, "auto handoff from planning protocol")
	if !isPlanHandoffPayloadValid(payload) {
		r.diag.log("stepAutoHandoff: invalid payload, plan stays Ready")
		r.architect.logWarn("stepAutoHandoff: handoff payload is invalid (not plan JSON)",
			"plan_id", r.plan.ID)
		r.architect.publishPlanStreamChunk(r.ctx,
			"\n\nPlan serialization failed — say **go ahead** to retry.")
		return nil
	}

	// Generate the orchestrator's CID and emit reroute BEFORE the sync call
	// so the TUI switches to "orchestrator" while the orchestrator processes.
	orchCID := "corr_" + uuid.NewString()
	originalCID := originalCIDFromContext(r.ctx)
	r.architect.publishHandoffReroute(r.ctx, "orchestrator", originalCID, orchCID)

	request := &guide.RouteRequest{
		Input:         payload,
		CorrelationID: orchCID,
		TargetAgentID: "orchestrator",
		SessionID:     r.plan.SessionID,
	}

	response, err := r.architect.requestRouteSync(r.ctx, request)
	if err != nil {
		r.architect.logWarn("stepAutoHandoff: route failed", "plan_id", r.plan.ID, "error", err)
		r.diag.log("stepAutoHandoff: route failed: %v", err)
		r.architect.publishPlanStreamChunk(r.ctx,
			"\n\nHandoff to orchestrator failed: "+err.Error()+"\nSay **go ahead** to retry.")
		return nil
	}

	if !isHandoffSuccess(response) {
		summary := summarizeAutoHandoffResponse(response)
		r.architect.logWarn("stepAutoHandoff: orchestrator rejected",
			"plan_id", r.plan.ID, "summary", summary)
		r.diag.log("stepAutoHandoff: rejected: %s", summary)
		r.architect.publishPlanStreamChunk(r.ctx,
			"\n\nOrchestrator rejected the handoff: "+summary+"\nSay **go ahead** to retry.")
		return nil
	}

	summary := summarizeAutoHandoffResponse(response)
	r.diag.log("stepAutoHandoff: success: %s", summary)

	// Clear the ReadyDirective so the guide does not enter PhasePlanApproval
	// for a plan that is already executing. Without this, the next user
	// message would be phase-classified as plan approval/feedback instead of
	// flowing through normal classification.
	r.plan.ReadyDirective = nil

	if err := r.transition(PlanStatusExecuting); err != nil {
		return err
	}
	r.plan.RiskSummary = append(r.plan.RiskSummary, summary)
	r.architect.publishPlanStreamChunk(r.ctx, "\n\n"+summary)
	return r.architect.persistPlanState(r.plan)
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
	r.plan.Status = status
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
	return a.persistEncodedPlanSnapshot(plan.ID, encoded)
}

func (a *Architect) marshalPlanSnapshot(plan *DesignPlan) ([]byte, error) {
	if a == nil || plan == nil || strings.TrimSpace(plan.ID) == "" {
		return nil, nil
	}
	return json.MarshalIndent(plan, "", "  ")
}

func (a *Architect) persistEncodedPlanSnapshot(planID string, encoded []byte) error {
	if a == nil || strings.TrimSpace(planID) == "" || len(encoded) == 0 {
		return nil
	}
	dir := a.planStoreDir()
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

func (a *Architect) planStoreDir() string {
	base := a.config.WorkingDirectory
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	return filepath.Join(base, ".sylk", "architect", "plans")
}

// restoreMaxAge is the maximum age of a persisted plan eligible for restore.
// Plans older than this are stale artifacts from prior sessions.
const restoreMaxAge = 24 * time.Hour

// restoreMaxPlans caps the number of plans restored on startup to prevent
// unbounded memory growth from accumulated plan files.
const restoreMaxPlans = 32

func (a *Architect) restorePersistedPlans() error {
	dir := a.planStoreDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-restoreMaxAge)
	restored := 0
	skipped := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if restored >= restoreMaxPlans {
			skipped++
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if ok, restoreErr := a.restorePlanFromFile(path, cutoff); restoreErr != nil {
			a.logger.Warn("failed to restore plan", "path", path, "error", restoreErr)
			continue
		} else if ok {
			restored++
		} else {
			skipped++
		}
	}
	a.logInfo("restorePersistedPlans: done",
		"dir", dir, "restored", restored, "skipped", skipped)
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
	// Skip terminal-state plans and plans older than cutoff.
	if plan.Status == PlanStatusFailed || plan.Status == PlanStatusExecuting {
		return false, nil
	}
	if plan.UpdatedAt.Before(cutoff) {
		return false, nil
	}
	a.logInfo("restorePlanFromFile",
		"plan_id", plan.ID,
		"status", plan.Status.String(),
		"query", truncateString(plan.Query, 80),
		"tasks", len(plan.Tasks),
		"created_at", plan.CreatedAt.String())
	a.upsertActivePlan(&plan)
	return true, nil
}
