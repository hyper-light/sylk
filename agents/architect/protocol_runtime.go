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
	runner := &planningProtocolRunner{architect: a, ctx: plannerCtx, request: req, plan: plan}
	if err := runner.run(); err != nil {
		return a.failAndPersistPlan(plan, err)
	}
	plan.CompletedAt = time.Now()
	a.persistPlanState(plan)
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
	if shouldAutoHandoff(r.request) {
		if err := r.stepAutoHandoff(); err != nil {
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
	}
}

func shouldAutoHandoff(req *ArchitectRequest) bool {
	if req == nil || len(req.Params) == 0 {
		return false
	}
	value, ok := req.Params["auto_handoff"].(bool)
	return ok && value
}

func (r *planningProtocolRunner) stepAnalyze() error {
	if err := r.transition(PlanStatusAnalyzing); err != nil {
		return err
	}
	requirements, err := r.architect.analyzeRequirements(r.ctx, r.request.Query, r.request.Params)
	if err != nil {
		return err
	}
	r.plan.Requirements = requirements
	return r.architect.persistPlanState(r.plan)
}

func (r *planningProtocolRunner) stepConsult() error {
	if err := r.transition(PlanStatusConsulting); err != nil {
		return err
	}
	if err := r.architect.enforceConsultationGate(r.ctx, r.plan, r.request); err != nil {
		return err
	}
	if r.architect.running && r.architect.bus != nil {
		patterns, err := r.architect.consultLibrarian(r.ctx, r.plan.Requirements, r.request.SessionID)
		if err != nil {
			r.architect.logger.Warn("failed to consult librarian", "error", err)
		} else {
			r.plan.CodebasePatterns = patterns
		}
	}
	return r.architect.persistPlanState(r.plan)
}

func (r *planningProtocolRunner) stepDesign() error {
	if err := r.transition(PlanStatusDesigning); err != nil {
		return err
	}
	architecture, err := r.architect.designArchitecture(r.ctx, r.plan.Requirements, r.plan.CodebasePatterns)
	if err != nil {
		return err
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
		return err
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
	// Stream formatted plan inline as chat content.
	r.architect.publishPlanStreamChunk(r.ctx, formatPlanForChat(r.plan))
	// Stream LLM commentary + readiness footer token-by-token.
	r.plan.UserResponse = r.architect.readyUserResponseInline(r.ctx, r.request, r.plan)
	// Persist but do NOT show plan panel yet — it appears during execution.
	return r.architect.persistPlanState(r.plan)
}

func (r *planningProtocolRunner) stepAutoHandoff() error {
	if !r.architect.running || r.architect.bus == nil {
		return fmt.Errorf("auto_handoff requested but architect bus is unavailable")
	}
	if err := r.transition(PlanStatusExecuting); err != nil {
		return err
	}
	payload := buildHandoffPayload(r.plan, "auto handoff from planning protocol")
	request := &guide.RouteRequest{
		Input:         payload,
		TargetAgentID: "orchestrator",
		SessionID:     r.plan.SessionID,
	}
	response, err := r.architect.requestRouteSync(r.ctx, request)
	if err != nil {
		return err
	}
	r.plan.RiskSummary = append(r.plan.RiskSummary, summarizeAutoHandoffResponse(response))
	return r.architect.persistPlanState(r.plan)
}

func summarizeAutoHandoffResponse(msg *guide.Message) string {
	if msg == nil {
		return "auto handoff dispatched"
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		if resp.Success {
			return "auto handoff acknowledged by orchestrator"
		}
		return "auto handoff response error: " + strings.TrimSpace(resp.Error)
	}
	if errText, ok := msg.GetError(); ok {
		return "auto handoff error: " + strings.TrimSpace(errText)
	}
	return "auto handoff dispatched"
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

func (a *Architect) restorePersistedPlans() error {
	dir := a.planStoreDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := a.restorePlanFromFile(path); err != nil {
			a.logger.Warn("failed to restore plan", "path", path, "error", err)
		}
	}
	return nil
}

func (a *Architect) restorePlanFromFile(path string) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var plan DesignPlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return err
	}
	if strings.TrimSpace(plan.ID) == "" {
		return fmt.Errorf("restored plan missing id")
	}
	a.upsertActivePlan(&plan)
	return nil
}
