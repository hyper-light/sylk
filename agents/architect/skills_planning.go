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
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

type consultBeforePlanningParams struct {
	Query           string `json:"query"`
	Scope           string `json:"scope,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	IncludeAcademic bool   `json:"include_academic,omitempty"`
}

func consultBeforePlanningSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("consult_before_planning").
		Description("Perform mandatory knowledge consultations before creating or revising plans.").
		Domain("consultation").
		Keywords("consult", "before planning", "context", "evidence", "librarian", "archivalist").
		Priority(100).
		StringParam("query", "Planning request to consult for", true).
		StringParam("scope", "Scope of consultation", false).
		StringParam("session_id", "Session identifier", false).
		BoolParam("include_academic", "Whether to require Academic consultation", false).
		Usage("Use BEFORE creating any new plan or revising an existing one. This is the mandatory consultation gate — it queries Librarian and Archivalist (and optionally Academic) to gather evidence that informs the plan design. Do NOT skip this step; plans without consultation evidence will fail the pre-delegation validation gate.").
		Example(`{"query": "Implement WebSocket support for real-time notifications", "scope": "backend", "include_academic": true}`).
		BestPractice("Always provide a specific scope to narrow consultation queries — broad scopes produce unfocused evidence.").
		BestPractice("Set include_academic=true only for architecturally novel features — routine CRUD operations do not benefit from academic research.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseConsultBeforePlanningParams(input)
			if err != nil {
				return nil, err
			}
			plan := &DesignPlan{
				ID:            uuid.NewString(),
				Query:         params.Query,
				SessionID:     params.SessionID,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
				Constraints:   &PlanConstraints{Scope: params.Scope},
				Consultations: map[string]*ConsultationEvidence{},
			}
			req := &ArchitectRequest{
				ID:        uuid.NewString(),
				Intent:    IntentPlan,
				Query:     params.Query,
				SessionID: params.SessionID,
				Timestamp: time.Now(),
				Params: map[string]any{
					"include_academic": params.IncludeAcademic,
					"scope":            params.Scope,
				},
			}
			if err := a.enforceConsultationGate(ctx, plan, req); err != nil {
				return nil, err
			}
			return map[string]any{
				"ready":         true,
				"consultations": plan.Consultations,
				"required":      mandatoryConsultationTargets(req),
			}, nil
		}).
		Build()
}

func parseConsultBeforePlanningParams(input json.RawMessage) (*consultBeforePlanningParams, error) {
	var params consultBeforePlanningParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if strings.TrimSpace(params.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	return &params, nil
}

func consultLibrarianSkill(a *Architect) *skills.Skill {
	return directConsultSkill(a, "consult_librarian", "librarian", "Consult Librarian for codebase patterns and implementation context.")
}

func consultArchivalistSkill(a *Architect) *skills.Skill {
	return directConsultSkill(a, "consult_archivalist", "archivalist", "Consult Archivalist for historical failures and prior outcomes.")
}

func consultAcademicSkill(a *Architect) *skills.Skill {
	return directConsultSkill(a, "consult_academic", "academic", "Consult Academic for best practices and external research validation.")
}

func consultEngineerSkill(a *Architect) *skills.Skill {
	return directConsultSkill(a, "consult_engineer", "engineer", "Consult Engineer for implementation feasibility and execution detail risks.")
}

func consultDesignerSkill(a *Architect) *skills.Skill {
	return directConsultSkill(a, "consult_designer", "designer", "Consult Designer for UX/UI approach and component design constraints.")
}

func consultInspectorSkill(a *Architect) *skills.Skill {
	return directConsultSkill(a, "consult_inspector", "inspector", "Consult Inspector for quality constraints and review risks before delegation.")
}

func consultTesterSkill(a *Architect) *skills.Skill {
	return directConsultSkill(a, "consult_tester", "tester", "Consult Tester for test strategy and validation coverage before execution.")
}

type directConsultParams struct {
	Query     string `json:"query"`
	Scope     string `json:"scope,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

func directConsultSkill(a *Architect, name string, target string, description string) *skills.Skill {
	return skills.NewSkill(name).
		Description(description).
		Domain("consultation").
		Keywords("consult", target, "knowledge", "patterns", "history", "research").
		Priority(90).
		StringParam("query", "Consultation question", true).
		StringParam("scope", "Scope for consultation", false).
		StringParam("session_id", "Session identifier", false).
		Usage(fmt.Sprintf("Use to gather evidence from the %s before or during planning. The consultation result includes success status and evidence data that will be attached to the active plan's consultation record. Do NOT call after delegation — consultations are a planning-phase activity.", target)).
		Example(`{"query": "What patterns exist for WebSocket connections in this codebase?", "scope": "backend"}`).
		BestPractice("Consultation responses are cached on the plan — do not re-consult the same agent for the same query within a single planning cycle.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseDirectConsultParams(input)
			if err != nil {
				return nil, err
			}
			evidence, err := a.requestConsultation(ctx, target, params.Query, params.Scope, params.SessionID)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"target":   target,
				"success":  evidence.Success,
				"evidence": evidence,
				"data":     evidence.Data,
			}, nil
		}).
		Build()
}

func parseDirectConsultParams(input json.RawMessage) (*directConsultParams, error) {
	var params directConsultParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if strings.TrimSpace(params.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	return &params, nil
}

type preDelegationParams struct {
	PlanID            string   `json:"plan_id,omitempty"`
	TaskID            string   `json:"task_id,omitempty"`
	TargetAgent       string   `json:"target_agent"`
	Reasoning         string   `json:"reasoning"`
	RequiredSkills    []string `json:"required_skills"`
	ExpectedOutcome   string   `json:"expected_outcome"`
	FailureCriteria   string   `json:"failure_criteria"`
	UserClarification bool     `json:"user_clarification_needed,omitempty"`
	ChallengesRaised  []string `json:"challenges_raised,omitempty"`
}

func preDelegationDeclareSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("pre_delegation_declare").
		Description("Create and persist a formal pre-delegation declaration with consultation evidence.").
		Domain("delegation").
		Keywords("declare", "delegation", "handoff", "target agent", "evidence").
		Priority(100).
		StringParam("plan_id", "Plan identifier", false).
		StringParam("task_id", "Task identifier", false).
		StringParam("target_agent", "Target agent for delegation", true).
		StringParam("reasoning", "Reasoning for this delegation strategy", true).
		ArrayParam("required_skills", "Skills required for execution", "string", true).
		StringParam("expected_outcome", "Expected outcome for delegated task", true).
		StringParam("failure_criteria", "Failure criteria for delegated task", true).
		BoolParam("user_clarification_needed", "Whether unresolved ambiguity remains", false).
		ArrayParam("challenges_raised", "Concerns raised during planning", "string", false).
		Usage("Use after consultation is complete and before handing off to the Orchestrator. Creates a formal declaration recording the target agent, reasoning, required skills, expected outcome, and failure criteria. The declaration is validated against consultation evidence — missing or stale consultations will cause validation failure. Do NOT create declarations for tasks that lack clear success criteria.").
		Example(`{"plan_id": "plan_abc", "target_agent": "engineer", "reasoning": "Single-file change with clear scope", "required_skills": ["go", "websocket"], "expected_outcome": "WebSocket handler implemented and tests passing", "failure_criteria": "Compilation errors or test failures"}`).
		BestPractice("Always specify both expected_outcome and failure_criteria — these are used by the Orchestrator to determine task success or failure.").
		BestPractice("Set user_clarification_needed=true if any consultation raised unresolved ambiguity — the user will be prompted before delegation proceeds.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parsePreDelegationParams(input)
			if err != nil {
				return nil, err
			}
			plan, err := a.selectPlan(params.PlanID)
			if err != nil {
				return nil, err
			}
			declaration := buildPreDelegationDeclaration(plan, params)
			if err := a.validateDeclaration(declaration); err != nil {
				return nil, err
			}
			a.persistDeclaration(plan, declaration)
			a.publishDeclaration(declaration, plan.SessionID)
			return declaration, nil
		}).
		Build()
}

func parsePreDelegationParams(input json.RawMessage) (*preDelegationParams, error) {
	var params preDelegationParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if strings.TrimSpace(params.TargetAgent) == "" {
		return nil, fmt.Errorf("target_agent is required")
	}
	if strings.TrimSpace(params.Reasoning) == "" {
		return nil, fmt.Errorf("reasoning is required")
	}
	if strings.TrimSpace(params.ExpectedOutcome) == "" {
		return nil, fmt.Errorf("expected_outcome is required")
	}
	if strings.TrimSpace(params.FailureCriteria) == "" {
		return nil, fmt.Errorf("failure_criteria is required")
	}
	if len(params.RequiredSkills) == 0 {
		return nil, fmt.Errorf("required_skills is required")
	}
	return &params, nil
}

func (a *Architect) selectPlan(planID string) (*DesignPlan, error) {
	if strings.TrimSpace(planID) != "" {
		plan, ok := a.GetActivePlan(planID)
		if !ok {
			return nil, fmt.Errorf("plan not found: %s", planID)
		}
		return plan, nil
	}
	plan := a.latestPlan()
	if plan == nil {
		return nil, fmt.Errorf("no active plan available")
	}
	return plan, nil
}

func (a *Architect) latestPlan() *DesignPlan {
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	if len(a.activePlans) == 0 {
		return nil
	}
	plans := make([]*DesignPlan, 0, len(a.activePlans))
	for _, plan := range a.activePlans {
		plans = append(plans, plan)
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].UpdatedAt.After(plans[j].UpdatedAt)
	})
	return plans[0]
}

func buildPreDelegationDeclaration(plan *DesignPlan, params *preDelegationParams) *PreDelegationDeclaration {
	consultations := map[string]*ConsultationEvidence{}
	if plan != nil {
		for key, value := range plan.Consultations {
			consultations[key] = value
		}
	}
	return &PreDelegationDeclaration{
		ID:                 "decl_" + uuid.NewString(),
		PlanID:             safePlanID(plan),
		TaskID:             params.TaskID,
		TargetAgent:        params.TargetAgent,
		Reasoning:          params.Reasoning,
		RequiredSkills:     params.RequiredSkills,
		ExpectedOutcome:    params.ExpectedOutcome,
		FailureCriteria:    params.FailureCriteria,
		UserClarification:  params.UserClarification,
		ChallengesRaised:   params.ChallengesRaised,
		ConsultationChecks: consultations,
		CreatedAt:          time.Now(),
	}
}

func safePlanID(plan *DesignPlan) string {
	if plan == nil {
		return ""
	}
	return plan.ID
}

func (a *Architect) validateDeclaration(declaration *PreDelegationDeclaration) error {
	if declaration == nil {
		return fmt.Errorf("declaration is required")
	}
	if err := validateDeclarationConsultations(declaration, a.config.ConsultationMaxAge); err != nil {
		return err
	}
	return nil
}

func validateDeclarationConsultations(
	declaration *PreDelegationDeclaration,
	maxAge time.Duration,
) error {
	required := []string{"librarian", "archivalist"}
	for _, target := range required {
		evidence := declaration.ConsultationChecks[target]
		if evidence == nil {
			return fmt.Errorf("missing consultation evidence for %s", target)
		}
		if !evidence.Success {
			return fmt.Errorf("%s consultation failed: %s", target, evidence.Error)
		}
		if !isConsultationFresh(evidence, maxAge) {
			return fmt.Errorf("%s consultation is stale", target)
		}
	}
	return nil
}

func isConsultationFresh(evidence *ConsultationEvidence, maxAge time.Duration) bool {
	if evidence == nil {
		return false
	}
	if maxAge <= 0 {
		return true
	}
	if evidence.ReceivedAt.IsZero() {
		return false
	}
	return time.Since(evidence.ReceivedAt) <= maxAge
}

func (a *Architect) persistDeclaration(plan *DesignPlan, declaration *PreDelegationDeclaration) {
	if plan == nil || declaration == nil {
		return
	}
	var (
		snapshotID  string
		encoded     []byte
		encodeError error
	)
	a.activePlansMu.Lock()
	existing := a.activePlans[plan.ID]
	if existing != nil {
		existing.Declarations = append(existing.Declarations, declaration)
		existing.UpdatedAt = time.Now()
		snapshotID = existing.ID
		encoded, encodeError = a.marshalPlanSnapshot(existing)
	}
	a.activePlansMu.Unlock()
	if encodeError != nil {
		a.logger.Warn("failed to encode declaration plan snapshot", "plan_id", snapshotID, "error", encodeError)
		return
	}
	if err := a.persistEncodedPlanSnapshot(snapshotID, encoded); err != nil {
		a.logger.Warn("failed to persist declaration plan snapshot", "plan_id", snapshotID, "error", err)
	}
}

func (a *Architect) publishDeclaration(declaration *PreDelegationDeclaration, sessionID string) {
	if declaration == nil || !a.running || a.bus == nil {
		return
	}
	req := &guide.RouteRequest{
		CorrelationID: "decl_" + uuid.NewString(),
		Input:         "store pre-delegation declaration",
		SourceAgentID: "architect",
		TargetAgentID: "archivalist",
		FireAndForget: true,
		SessionID:     sessionID,
		Timestamp:     time.Now(),
	}
	msg := guide.NewRequestMessage(a.generateMessageID(), req)
	msg.Metadata = map[string]any{"declaration": declaration}
	if err := a.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		a.logWarn("failed to publish pre-delegation declaration",
			"declaration_id", declaration.ID,
			"session_id", sessionID,
			"error", err)
	}
}

type validatePreDelegationParams struct {
	PlanID        string `json:"plan_id,omitempty"`
	DeclarationID string `json:"declaration_id,omitempty"`
}

func validatePreDelegationSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("validate_pre_delegation").
		Description("Validate a pre-delegation declaration against required consultation evidence.").
		Domain("delegation").
		Keywords("validate", "pre delegation", "declaration", "consultation").
		Priority(95).
		StringParam("plan_id", "Plan identifier", false).
		StringParam("declaration_id", "Declaration identifier", false).
		Usage("Use to validate an existing declaration's consultation evidence before proceeding with handoff. Returns valid=true if all required consultations (Librarian, Archivalist) are present, successful, and within the configured max age. Use this as a preflight check before `handoff_to_orchestrator`.").
		Example(`{"plan_id": "plan_abc", "declaration_id": "decl_xyz"}`).
		BestPractice("If validation fails due to stale consultations, re-run `consult_before_planning` rather than creating a new declaration from scratch.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params validatePreDelegationParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			decl, err := a.findDeclaration(params.PlanID, params.DeclarationID)
			if err != nil {
				return nil, err
			}
			if err := a.validateDeclaration(decl); err != nil {
				return map[string]any{"valid": false, "error": err.Error(), "declaration_id": decl.ID}, nil
			}
			return map[string]any{"valid": true, "declaration_id": decl.ID}, nil
		}).
		Build()
}

func (a *Architect) findDeclaration(planID string, declarationID string) (*PreDelegationDeclaration, error) {
	plan, err := a.selectPlan(planID)
	if err != nil {
		return nil, err
	}
	if len(plan.Declarations) == 0 {
		return nil, fmt.Errorf("no declarations found on plan %s", plan.ID)
	}
	if declarationID == "" {
		return plan.Declarations[len(plan.Declarations)-1], nil
	}
	for _, declaration := range plan.Declarations {
		if declaration != nil && declaration.ID == declarationID {
			return declaration, nil
		}
	}
	return nil, fmt.Errorf("declaration not found: %s", declarationID)
}

// buildHandoffPayload serializes the plan as a structured PlanHandoff JSON
// document. The orchestrator's ingest_plan skill parses this to create task
// records, build a DAG, and begin execution.
//
// Returns an empty string on nil plan or marshal failure — callers MUST
// validate the result via isPlanHandoffPayloadValid before sending.
func buildHandoffPayload(plan *DesignPlan, trigger string) string {
	if plan == nil {
		return ""
	}
	handoff := buildPlanHandoff(plan, trigger)
	data, err := json.Marshal(handoff)
	if err != nil {
		return ""
	}
	return string(data)
}

func buildPlanHandoff(plan *DesignPlan, trigger string) *PlanHandoff {
	tasks := make([]*HandoffTask, 0, len(plan.Tasks))
	totalTokens := 0
	for _, t := range plan.Tasks {
		ht := atomicTaskToHandoff(t)
		tasks = append(tasks, ht)
		totalTokens += t.EstimatedTokens
	}

	var layers [][]string
	var criticalPath []string
	if plan.Workflow != nil {
		layers = plan.Workflow.ExecutionLayers
		criticalPath = plan.Workflow.CriticalPath
	}

	return &PlanHandoff{
		PlanID:          plan.ID,
		SessionID:       plan.SessionID,
		Query:           plan.Query,
		Revision:        plan.Revision,
		Tasks:           tasks,
		ExecutionLayers: layers,
		CriticalPath:    criticalPath,
		Constraints:     plan.Constraints,
		TotalTokens:     totalTokens,
		RiskSummary:     plan.RiskSummary,
		Trigger:         trigger,
		Timestamp:       time.Now(),
		Architecture:    plan.Architecture,
		Requirements:    plan.Requirements,
		Assumptions:     plan.Assumptions,
	}
}

func atomicTaskToHandoff(t *AtomicTask) *HandoffTask {
	h := &HandoffTask{
		ID:                  t.ID,
		Name:                t.Name,
		Description:         t.Description,
		AgentType:           t.AgentType,
		Dependencies:        t.Dependencies,
		EstimatedTokens:     t.EstimatedTokens,
		Complexity:          t.Complexity.String(),
		Priority:            t.Priority,
		SuccessCriteria:     t.SuccessCriteria,
		AcceptanceCriteria:  t.AcceptanceCriteria,
		Guidelines:          t.Guidelines,
		ImplementationGuide: t.ImplementationGuide,
		Examples:            t.Examples,
		AffectedFiles:       t.AffectedFiles,
		TestRequirements:    t.TestRequirements,
		RiskFactors:         t.RiskFactors,
		CoAgents:            t.CoAgents,
		MaxReviewRounds:     t.MaxReviewRounds,
		AgentScopes:         t.AgentScopes,
	}
	if t.CollaborationMode != 0 {
		h.CollaborationMode = t.CollaborationMode.String()
	}
	return h
}

type monitorExecutionParams struct {
	PlanID string `json:"plan_id"`
	Query  string `json:"query,omitempty"`
}

func monitorExecutionSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("monitor_execution").
		Description("Query orchestrator execution status for a delegated plan.").
		Domain("coordination").
		Keywords("monitor", "execution", "status", "orchestrator").
		Priority(80).
		StringParam("plan_id", "Plan identifier", true).
		StringParam("query", "Optional status query", false).
		Usage("Use to check the Orchestrator's execution progress for a delegated plan. Queries the Orchestrator via the bus and returns the current plan status. Falls back to local plan state if the Orchestrator is unreachable. Do NOT use before handoff — the Orchestrator has no state for plans that have not been dispatched.").
		Example(`{"plan_id": "plan_abc", "query": "How many tasks have completed?"}`).
		BestPractice("If the response shows status=local_fallback, the Orchestrator may be overloaded or unrestarting — check orchestrator health.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params monitorExecutionParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			plan, err := a.selectPlan(params.PlanID)
			if err != nil {
				return nil, err
			}
			query := params.Query
			if strings.TrimSpace(query) == "" {
				query = fmt.Sprintf("status for plan %s", plan.ID)
			}
			req := &guide.RouteRequest{
				Input:         query,
				TargetAgentID: "orchestrator",
				SessionID:     plan.SessionID,
			}
			msg, err := a.requestRouteSync(ctx, req)
			if err != nil {
				return map[string]any{
					"status":      "local_fallback",
					"plan_id":     plan.ID,
					"plan_status": plan.Status.String(),
					"error":       err.Error(),
				}, nil
			}
			return map[string]any{
				"status":      "ok",
				"plan_id":     plan.ID,
				"plan_status": plan.Status.String(),
				"response":    msg.Payload,
			}, nil
		}).
		Build()
}

type revisePlanParams struct {
	PlanID  string         `json:"plan_id"`
	Reason  string         `json:"reason"`
	Updates map[string]any `json:"updates,omitempty"`
}

func revisePlanSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("revise_plan").
		Description("Revise an existing plan in response to runtime feedback or changed assumptions.").
		Domain("planning").
		Keywords("revise", "replan", "update plan", "change workflow").
		Priority(85).
		StringParam("plan_id", "Plan identifier to revise", true).
		StringParam("reason", "Reason for revision", true).
		ObjectParam("updates", "Optional update payload", map[string]*skills.Property{}, false).
		Usage("Use when runtime feedback, user input, or execution results require changes to an existing plan. Increments the plan revision, records the reason, and optionally updates status or scope. Always provide a meaningful reason — it is persisted in the plan's risk summary for audit. Do NOT use to create new plans — use `consult_before_planning` + the planning protocol instead.").
		Example(`{"plan_id": "plan_abc", "reason": "Engineer reported dependency conflict — adjusting task order", "updates": {"scope": "backend+infra"}}`).
		BestPractice("After revision, re-validate any existing declarations with `validate_pre_delegation` — the changed plan may invalidate prior consultation evidence.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params revisePlanParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Reason) == "" {
				return nil, fmt.Errorf("reason is required")
			}
			plan, err := a.selectPlan(params.PlanID)
			if err != nil {
				return nil, err
			}
			updated := a.applyPlanRevision(plan, params.Reason, params.Updates)
			return map[string]any{
				"plan_id":  updated.ID,
				"revision": updated.Revision,
				"status":   updated.Status.String(),
				"updated":  updated.UpdatedAt,
			}, nil
		}).
		Build()
}

func (a *Architect) applyPlanRevision(plan *DesignPlan, reason string, updates map[string]any) *DesignPlan {
	var (
		current     *DesignPlan
		snapshotID  string
		encoded     []byte
		encodeError error
	)
	a.activePlansMu.Lock()
	current = a.activePlans[plan.ID]
	if current == nil {
		a.activePlansMu.Unlock()
		return plan
	}
	current.Revision++
	current.UpdatedAt = time.Now()
	current.RiskSummary = append(current.RiskSummary, reason)
	applyPlanUpdateFields(current, updates)
	snapshotID = current.ID
	encoded, encodeError = a.marshalPlanSnapshot(current)
	a.activePlansMu.Unlock()
	if encodeError != nil {
		a.logger.Warn("failed to encode revised plan snapshot", "plan_id", snapshotID, "error", encodeError)
		return current
	}
	if err := a.persistEncodedPlanSnapshot(snapshotID, encoded); err != nil {
		a.logger.Warn("failed to persist revised plan snapshot", "plan_id", snapshotID, "error", err)
	}
	return current
}

func applyPlanUpdateFields(plan *DesignPlan, updates map[string]any) {
	if plan == nil || updates == nil {
		return
	}
	if status, ok := updates["status"].(string); ok {
		plan.Status = parsePlanStatus(status, plan.Status)
	}
	if scope, ok := updates["scope"].(string); ok && plan.Constraints != nil {
		plan.Constraints.Scope = scope
	}
}

func parsePlanStatus(status string, fallback PlanStatus) PlanStatus {
	key := strings.ToLower(strings.TrimSpace(status))
	table := map[string]PlanStatus{
		"pending":       PlanStatusPending,
		"analyzing":     PlanStatusAnalyzing,
		"consulting":    PlanStatusConsulting,
		"designing":     PlanStatusDesigning,
		"generating":    PlanStatusGenerating,
		"orchestrating": PlanStatusOrchestrating,
		"ready":         PlanStatusReady,
		"executing":     PlanStatusExecuting,
		"completed":     PlanStatusCompleted,
		"failed":        PlanStatusFailed,
	}
	if parsed, ok := table[key]; ok {
		return parsed
	}
	return fallback
}

type createFixDAGParams struct {
	PlanID      string `json:"plan_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Corrections []any  `json:"corrections"`
}

func createFixDAGSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("create_fix_dag").
		Description("Build a remediation DAG structure from inspector/tester correction signals. Does NOT dispatch or execute the plan.").
		Domain("planning").
		Keywords("fix dag", "corrections", "repair structure", "remediation tasks").
		Priority(85).
		StringParam("plan_id", "Plan identifier to attach fix DAG to", false).
		StringParam("session_id", "Session identifier", false).
		ArrayParam("corrections", "Correction list from inspector/tester feedback", "object", true).
		Usage("Use when Inspector or Tester feedback produces correction signals that need a remediation workflow. Builds a fix DAG from the correction list and attaches it to the specified plan. Each correction becomes an engineer task. This is a planning-phase tool that builds data structures — it does NOT submit the plan to the Orchestrator or trigger execution. To execute a plan after user approval, use route_plan_acceptance followed by handle_plan_acceptance_result. Do NOT use for fresh feature work — this is strictly for remediation of existing plan failures.").
		Example(`{"plan_id": "plan_abc", "corrections": [{"description": "Missing nil check in handler", "issue": "panic on nil input"}], "session_id": "sess_abc"}`).
		BestPractice("NEVER use this skill as a substitute for plan execution. Execution requires: route_plan_acceptance → Guide verdict → handle_plan_acceptance_result.").
		BestPractice("Review the generated task count — if corrections produce more than 5 tasks, consider revising the original plan rather than layering fixes.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseCreateFixDAGParams(input)
			if err != nil {
				return nil, err
			}
			tasks := buildFixTasks(params.Corrections)
			workflow, err := a.createWorkflowDAG(ctx, tasks)
			if err != nil {
				return nil, err
			}
			linkedPlanID := a.attachFixWorkflow(params.PlanID, workflow, tasks)
			return map[string]any{
				"plan_id":      linkedPlanID,
				"session_id":   normalizeSessionID(params.SessionID),
				"workflow":     workflow,
				"task_count":   len(tasks),
				"corrections":  len(params.Corrections),
				"workflow_tag": "fix",
			}, nil
		}).
		Build()
}

func parseCreateFixDAGParams(input json.RawMessage) (*createFixDAGParams, error) {
	var params createFixDAGParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if len(params.Corrections) == 0 {
		return nil, fmt.Errorf("corrections are required")
	}
	return &params, nil
}

func buildFixTasks(corrections []any) []*AtomicTask {
	tasks := make([]*AtomicTask, 0, len(corrections))
	for idx, entry := range corrections {
		description := extractCorrectionText(entry, idx)
		task := &AtomicTask{
			ID:              fmt.Sprintf("fix_task_%d", idx+1),
			Name:            fmt.Sprintf("Apply fix %d", idx+1),
			Description:     description,
			AgentType:       "engineer",
			SuccessCriteria: []string{"Correction applied", "Regression risk addressed"},
			Dependencies:    nil,
			EstimatedTokens: 2000,
			Complexity:      ComplexityMedium,
			Status:          TaskStatusPending,
		}
		tasks = append(tasks, task)
	}
	return normalizeTaskGraph(tasks)
}

func extractCorrectionText(entry any, idx int) string {
	if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
		return text
	}
	if payload, ok := entry.(map[string]any); ok {
		if text := firstNonEmptyString(payload["description"], payload["message"], payload["issue"]); text != "" {
			return text
		}
	}
	return fmt.Sprintf("Resolve correction item %d", idx+1)
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		text, ok := value.(string)
		if ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func (a *Architect) attachFixWorkflow(planID string, workflow *WorkflowDAG, tasks []*AtomicTask) string {
	plan, err := a.selectPlan(planID)
	if err != nil || plan == nil {
		return ""
	}
	var (
		currentID   string
		encoded     []byte
		encodeError error
	)
	a.activePlansMu.Lock()
	current := a.activePlans[plan.ID]
	if current == nil {
		a.activePlansMu.Unlock()
		return ""
	}
	current.Workflow = workflow
	current.Tasks = tasks
	current.Status = PlanStatusReady
	current.UpdatedAt = time.Now()
	currentID = current.ID
	encoded, encodeError = a.marshalPlanSnapshot(current)
	a.activePlansMu.Unlock()
	if encodeError != nil {
		a.logger.Warn("failed to encode fix workflow snapshot", "plan_id", currentID, "error", encodeError)
		return currentID
	}
	if err := a.persistEncodedPlanSnapshot(currentID, encoded); err != nil {
		a.logger.Warn("failed to persist fix workflow snapshot", "plan_id", currentID, "error", err)
	}
	return currentID
}

type interruptHandlerParams struct {
	PlanID    string `json:"plan_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
}

func interruptHandlerSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("interrupt_handler").
		Description("Handle stop/pause/resume/cancel signals and update plan state safely.").
		Domain("coordination").
		Keywords("interrupt", "stop", "pause", "resume", "cancel").
		Priority(85).
		StringParam("plan_id", "Plan identifier", false).
		StringParam("session_id", "Session identifier", false).
		StringParam("action", "interrupt action: pause|resume|cancel|stop", true).
		StringParam("reason", "Optional reason for interruption", false).
		Usage("Use when the user or system signals a stop, pause, resume, or cancel for an active plan. Updates the plan status safely and records the reason. Valid actions: pause (→pending), resume (→executing), cancel/stop (→failed). Do NOT use for plan revision — use `revise_plan` instead.").
		Example(`{"plan_id": "plan_abc", "action": "pause", "reason": "User requested pause to review intermediate results"}`).
		BestPractice("After cancellation, broadcast a status update so downstream agents (Orchestrator, pipeline agents) can clean up.").
		BestPractice("Resume only after verifying the plan's consultation evidence is still fresh — stale evidence after a long pause invalidates the plan.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseInterruptHandlerParams(input)
			if err != nil {
				return nil, err
			}
			plan, err := a.selectPlan(params.PlanID)
			if err != nil {
				return nil, err
			}
			updated := a.applyInterruptAction(plan, params.Action, params.Reason)
			return map[string]any{
				"plan_id":      updated.ID,
				"action":       strings.ToLower(strings.TrimSpace(params.Action)),
				"status":       updated.Status.String(),
				"session_id":   normalizeSessionID(params.SessionID),
				"updated_at":   updated.UpdatedAt,
				"risk_summary": updated.RiskSummary,
			}, nil
		}).
		Build()
}

func parseInterruptHandlerParams(input json.RawMessage) (*interruptHandlerParams, error) {
	var params interruptHandlerParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(params.Action))
	if action != "pause" && action != "resume" && action != "cancel" && action != "stop" {
		return nil, fmt.Errorf("unsupported action: %s", params.Action)
	}
	return &params, nil
}

func (a *Architect) applyInterruptAction(plan *DesignPlan, action string, reason string) *DesignPlan {
	var (
		current     *DesignPlan
		snapshotID  string
		encoded     []byte
		encodeError error
	)
	a.activePlansMu.Lock()
	current = a.activePlans[plan.ID]
	if current == nil {
		a.activePlansMu.Unlock()
		return plan
	}
	current.Status = interruptStatus(action, current.Status)
	if strings.TrimSpace(reason) != "" {
		current.RiskSummary = append(current.RiskSummary, reason)
	}
	current.UpdatedAt = time.Now()
	snapshotID = current.ID
	encoded, encodeError = a.marshalPlanSnapshot(current)
	a.activePlansMu.Unlock()
	if encodeError != nil {
		a.logger.Warn("failed to encode interrupted plan snapshot", "plan_id", snapshotID, "error", encodeError)
		return current
	}
	if err := a.persistEncodedPlanSnapshot(snapshotID, encoded); err != nil {
		a.logger.Warn("failed to persist interrupted plan snapshot", "plan_id", snapshotID, "error", err)
	}
	return current
}

func interruptStatus(action string, fallback PlanStatus) PlanStatus {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "pause":
		return PlanStatusPending
	case "resume":
		return PlanStatusExecuting
	case "cancel", "stop":
		return PlanStatusFailed
	default:
		return fallback
	}
}

type enterPlanModeParams struct {
	TaskDescription string `json:"task_description"`
	PlanFile        string `json:"plan_file,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
}

func enterPlanModeSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("enter_plan_mode").
		Description("Enter plan mode for complex work requiring approval and revision tracking.").
		Domain("planning").
		Keywords("plan mode", "complex", "design", "architecture", "approval").
		Priority(85).
		StringParam("task_description", "Task to plan", true).
		StringParam("plan_file", "Optional plan markdown file path", false).
		StringParam("session_id", "Session identifier", false).
		Usage("Use when the user's request requires a structured planning workflow with approval gates — typically for complex multi-step features, architectural changes, or tasks spanning multiple agents. Creates a plan markdown file and enables plan mode for the session. Do NOT enter plan mode for simple single-agent tasks.").
		Example(`{"task_description": "Implement user authentication with OAuth2 and JWT tokens", "plan_file": "auth_plan.md", "session_id": "sess_abc"}`).
		BestPractice("Plan mode persists a markdown file — always provide a descriptive task_description as it becomes the file header.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params enterPlanModeParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.TaskDescription) == "" {
				return nil, fmt.Errorf("task_description is required")
			}
			mode := a.enterPlanMode(params.SessionID, params.PlanFile, params.TaskDescription)
			return mode, nil
		}).
		Build()
}

func (a *Architect) enterPlanMode(sessionID string, planFile string, taskDescription string) *PlanModeState {
	normalizedSession := normalizeSessionID(sessionID)
	resolvedFile := resolvePlanFile(a.config.WorkingDirectory, normalizedSession, planFile)
	_ = ensurePlanFileExists(resolvedFile, taskDescription)
	mode := &PlanModeState{
		SessionID:        normalizedSession,
		Enabled:          true,
		AwaitingApproval: false,
		PlanFile:         resolvedFile,
		UpdatedAt:        time.Now(),
	}
	a.planModesMu.Lock()
	a.planModes[normalizedSession] = mode
	a.planModesMu.Unlock()
	return mode
}

func normalizeSessionID(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return "default"
	}
	return sessionID
}

func resolvePlanFile(workDir string, sessionID string, planFile string) string {
	if strings.TrimSpace(planFile) != "" {
		if filepath.IsAbs(planFile) {
			return planFile
		}
		return filepath.Join(workDir, planFile)
	}
	name := fmt.Sprintf("%s_plan.md", sessionID)
	return filepath.Join(workDir, ".sylk", "plans", name)
}

func ensurePlanFileExists(path string, taskDescription string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	content := fmt.Sprintf("# Plan\n\n## Task\n%s\n", taskDescription)
	return os.WriteFile(path, []byte(content), 0644)
}

type updatePlanFileParams struct {
	SessionID string `json:"session_id,omitempty"`
	Content   string `json:"content"`
	Append    bool   `json:"append,omitempty"`
}

func updatePlanFileSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("update_plan_file").
		Description("Update the active plan-mode markdown file.").
		Domain("planning").
		Keywords("update plan file", "plan markdown", "revise document").
		Priority(80).
		StringParam("session_id", "Session identifier", false).
		StringParam("content", "Content to write", true).
		BoolParam("append", "Append content instead of overwrite", false).
		Usage("Use to write or append content to the active plan-mode markdown file. Requires plan mode to be enabled for the session. Set append=true to add content without overwriting existing plan text. Do NOT use outside plan mode — the handler will return an error if plan mode is not enabled.").
		Example(`{"content": "## Implementation\n\n1. Create WebSocket handler\n2. Add message routing\n", "append": true, "session_id": "sess_abc"}`).
		BestPractice("Use append=true for incremental plan building (adding sections) and append=false only for complete plan rewrites.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params updatePlanFileParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			mode, err := a.getPlanMode(params.SessionID)
			if err != nil {
				return nil, err
			}
			if err := writePlanFile(mode.PlanFile, params.Content, params.Append); err != nil {
				return nil, err
			}
			mode.UpdatedAt = time.Now()
			return map[string]any{"plan_file": mode.PlanFile, "updated": mode.UpdatedAt}, nil
		}).
		Build()
}

func writePlanFile(path string, content string, appendMode bool) error {
	if !appendMode {
		return os.WriteFile(path, []byte(content), 0644)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content)
	return err
}

type todoWriteParams struct {
	SessionID string     `json:"session_id,omitempty"`
	Todos     []PlanTodo `json:"todos"`
}

func todoWriteSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("todo_write").
		Description("Write or replace the plan-mode todo list.").
		Domain("planning").
		Keywords("todo", "tasks", "tracking", "progress").
		Priority(75).
		StringParam("session_id", "Session identifier", false).
		ArrayParam("todos", "Todo objects with content/status/active_form", "object", true).
		Usage("Use to create or replace the plan-mode todo list for tracking implementation progress. Each todo should have content, status, and an active_form (present-tense description shown during execution). Requires plan mode to be enabled.").
		Example(`{"session_id": "sess_abc", "todos": [{"content": "Implement WebSocket handler", "status": "pending", "active_form": "Implementing WebSocket handler"}, {"content": "Write integration tests", "status": "pending", "active_form": "Writing integration tests"}]}`).
		BestPractice("Keep todo items at the task level (not sub-task) — each should map to a single agent delegation.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params todoWriteParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			mode, err := a.getPlanMode(params.SessionID)
			if err != nil {
				return nil, err
			}
			mode.Todos = params.Todos
			mode.UpdatedAt = time.Now()
			return map[string]any{"todos": mode.Todos, "count": len(mode.Todos)}, nil
		}).
		Build()
}

type todoMarkCompleteParams struct {
	SessionID string `json:"session_id,omitempty"`
	Index     int    `json:"index"`
}

func todoMarkCompleteSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("todo_mark_complete").
		Description("Mark a plan-mode todo as completed by index.").
		Domain("planning").
		Keywords("todo complete", "mark done", "finish step").
		Priority(75).
		StringParam("session_id", "Session identifier", false).
		IntParam("index", "0-based todo index", true).
		Usage("Use to mark a specific plan-mode todo as completed by its 0-based index. Requires plan mode to be enabled and the index to be within the todo list bounds.").
		Example(`{"session_id": "sess_abc", "index": 0}`).
		BestPractice("Mark todos complete only after receiving confirmation from the executing agent — do not mark complete optimistically.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params todoMarkCompleteParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			mode, err := a.getPlanMode(params.SessionID)
			if err != nil {
				return nil, err
			}
			if params.Index < 0 || params.Index >= len(mode.Todos) {
				return nil, fmt.Errorf("todo index out of range")
			}
			mode.Todos[params.Index].Status = "completed"
			mode.UpdatedAt = time.Now()
			return map[string]any{
				"index":      params.Index,
				"todo":       mode.Todos[params.Index],
				"todo_count": len(mode.Todos),
				"updated_at": mode.UpdatedAt,
			}, nil
		}).
		Build()
}

type exitPlanModeParams struct {
	SessionID      string   `json:"session_id,omitempty"`
	AllowedPrompts []string `json:"allowed_prompts,omitempty"`
}

func exitPlanModeSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("exit_plan_mode").
		Description("Exit plan mode and mark plan as awaiting user approval.").
		Domain("planning").
		Keywords("exit plan mode", "approval", "review", "ready").
		Priority(80).
		StringParam("session_id", "Session identifier", false).
		ArrayParam("allowed_prompts", "Optional permitted command prompts", "string", false).
		Usage("Use when the plan is complete and ready for user review. Marks the plan as awaiting approval and optionally specifies allowed command prompts that the user can execute. The plan file remains on disk for the user to review. Do NOT exit plan mode before the plan is fully formed — premature exit wastes the user's review effort.").
		Example(`{"session_id": "sess_abc", "allowed_prompts": ["run tests", "install dependencies"]}`).
		BestPractice("Always populate allowed_prompts with the specific actions needed to implement the plan — this gives the user a clear next-step menu.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params exitPlanModeParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			mode, err := a.getPlanMode(params.SessionID)
			if err != nil {
				return nil, err
			}
			mode.AwaitingApproval = true
			mode.AllowedPrompts = params.AllowedPrompts
			mode.UpdatedAt = time.Now()
			return map[string]any{
				"session_id":        mode.SessionID,
				"awaiting_approval": mode.AwaitingApproval,
				"allowed_prompts":   mode.AllowedPrompts,
			}, nil
		}).
		Build()
}

type askUserQuestionParams struct {
	SessionID string           `json:"session_id,omitempty"`
	Questions []map[string]any `json:"questions"`
}

func askUserQuestionSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("ask_user_question").
		Description("Create an explicit user clarification request payload as a last resort.").
		Domain("coordination").
		Keywords("ask user", "clarification", "decision", "question").
		Priority(70).
		StringParam("session_id", "Session identifier", false).
		ArrayParam("questions", "Question objects with options", "object", true).
		Usage("Use as a last resort when the Architect cannot resolve ambiguity through consultation or plan analysis alone. Creates a structured clarification request with multiple-choice options. Do NOT use for routine planning decisions — exhaust consultation evidence first.").
		Example(`{"session_id": "sess_abc", "questions": [{"question": "Which authentication method should we use?", "options": [{"label": "OAuth2", "description": "External provider flow"}, {"label": "JWT", "description": "Self-issued tokens"}]}]}`).
		BestPractice("Provide concrete options whenever possible — open-ended questions slow the workflow significantly.").
		BestPractice("Batch related questions into a single call rather than asking one at a time.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params askUserQuestionParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if len(params.Questions) == 0 {
				return nil, fmt.Errorf("questions are required")
			}
			return map[string]any{
				"status":     "clarification_required",
				"session_id": normalizeSessionID(params.SessionID),
				"questions":  params.Questions,
			}, nil
		}).
		Build()
}

func (a *Architect) getPlanMode(sessionID string) (*PlanModeState, error) {
	normalized := normalizeSessionID(sessionID)
	a.planModesMu.RLock()
	mode := a.planModes[normalized]
	a.planModesMu.RUnlock()
	if mode == nil || !mode.Enabled {
		return nil, fmt.Errorf("plan mode not enabled for session %s", normalized)
	}
	return mode, nil
}

type readResearchPaperParams struct {
	ResearchSlug string `json:"research_slug"`
	PaperPath    string `json:"paper_path"`
	Version      int    `json:"version,omitempty"`
	Summary      string `json:"summary,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
}

func readResearchPaperSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("read_research_paper").
		Description("Read a research paper artifact and generate a planning-ready architecture plan.").
		Domain("research").
		Keywords("research paper", "proposal", "academic", "implementation plan").
		Priority(90).
		StringParam("research_slug", "Research identifier slug", false).
		StringParam("paper_path", "Path to research paper markdown", true).
		IntParam("version", "Research version number", false).
		StringParam("summary", "Optional proposal summary", false).
		StringParam("session_id", "Session identifier", false).
		Usage("Use when the user provides a research paper or proposal that should be converted into an executable architecture plan. Reads the paper content, builds a planning query from it, and executes the full planning protocol (including Academic consultation). Do NOT use for short notes or feature requests — this is for substantial research artifacts.").
		Example(`{"research_slug": "distributed-cache-v2", "paper_path": "docs/proposals/distributed_cache.md", "version": 1, "summary": "Proposes a two-tier caching layer with Redis L1 and disk L2"}`).
		BestPractice("Provide a summary if the paper is long — it is used to scope the planning query and prevents the full paper from overwhelming the LLM context.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseReadResearchPaperParams(input)
			if err != nil {
				return nil, err
			}
			content, err := os.ReadFile(params.PaperPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read research paper: %w", err)
			}
			query := buildResearchPlanningQuery(params, string(content))
			req := &ArchitectRequest{
				ID:        uuid.NewString(),
				Intent:    IntentPlan,
				Query:     query,
				SessionID: params.SessionID,
				Timestamp: time.Now(),
				Params: map[string]any{
					"scope":            "research",
					"include_academic": true,
					"research_slug":    params.ResearchSlug,
					"paper_path":       params.PaperPath,
					"version":          params.Version,
				},
			}
			plan, err := a.executePlanningProtocol(ctx, req)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"research_slug": params.ResearchSlug,
				"paper_path":    params.PaperPath,
				"plan_id":       plan.ID,
				"status":        plan.Status.String(),
			}, nil
		}).
		Build()
}

func parseReadResearchPaperParams(input json.RawMessage) (*readResearchPaperParams, error) {
	var params readResearchPaperParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if strings.TrimSpace(params.PaperPath) == "" {
		return nil, fmt.Errorf("paper_path is required")
	}
	return &params, nil
}

func buildResearchPlanningQuery(params *readResearchPaperParams, content string) string {
	slug := strings.TrimSpace(params.ResearchSlug)
	if slug == "" {
		slug = "research_proposal"
	}
	summary := strings.TrimSpace(params.Summary)
	if summary == "" {
		summary = truncateString(content, 600)
	}
	return fmt.Sprintf("Convert research proposal '%s' into execution plan. Summary: %s", slug, summary)
}

// ---------------------------------------------------------------------------
// route_plan_acceptance — route plan + user response to Guide for evaluation
// ---------------------------------------------------------------------------

type routePlanAcceptanceParams struct {
	PlanID       string `json:"plan_id"`
	UserResponse string `json:"user_response"`
}

func routePlanAcceptanceSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("route_plan_acceptance").
		Description("Route a ready plan and user response to the Guide for acceptance evaluation.").
		Domain("coordination").
		Keywords("plan", "acceptance", "evaluate", "approve", "reject", "feedback").
		Priority(100).
		StringParam("plan_id", "Plan identifier (uses latest ready plan if omitted)", false).
		StringParam("user_response", "The user's verbatim response to the plan", true).
		Usage("Use IMMEDIATELY after the user responds to a presented plan. Packages the plan text, plan ID, plan name, and user response into a structured payload and routes it to the Guide's evaluate-plan-acceptance skill. All four payload fields are derived by the handler — do NOT attempt to construct the evaluation payload manually. The Guide returns accept/modify/reject with optional modification notes.").
		Example(`{"plan_id": "plan_abc", "user_response": "Looks good, but swap the task order for steps 2 and 3."}`).
		BestPractice("Always call this skill for user responses to ready plans — do not classify acceptance yourself.").
		BestPractice("If the result is 'modify', read the modifications list and apply changes via revise_plan before re-presenting.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseRoutePlanAcceptanceParams(input)
			if err != nil {
				return nil, err
			}
			plan, err := a.resolveReadyPlanForAcceptance(params.PlanID)
			if err != nil {
				return nil, err
			}
			payload := buildPlanAcceptancePayload(plan, params.UserResponse)
			return a.routePlanAcceptanceToGuide(ctx, plan.SessionID, payload)
		}).
		Build()
}

func parseRoutePlanAcceptanceParams(input json.RawMessage) (*routePlanAcceptanceParams, error) {
	var params routePlanAcceptanceParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if strings.TrimSpace(params.UserResponse) == "" {
		return nil, fmt.Errorf("user_response is required")
	}
	return &params, nil
}

// resolveReadyPlanForAcceptance locates the plan by ID or falls back to the
// latest ready plan. Returns an error if no eligible plan exists.
func (a *Architect) resolveReadyPlanForAcceptance(planID string) (*DesignPlan, error) {
	if id := strings.TrimSpace(planID); id != "" {
		plan, ok := a.GetActivePlan(id)
		if !ok {
			return nil, fmt.Errorf("plan not found: %s", id)
		}
		if plan.Status != PlanStatusReady {
			return nil, fmt.Errorf("plan %s is not in ready status (current: %s)", id, plan.Status)
		}
		return plan, nil
	}
	plan := a.latestReadyPlan()
	if plan == nil {
		return nil, fmt.Errorf("no ready plan available for acceptance evaluation")
	}
	return plan, nil
}

// planAcceptancePayload is the exact structure the Guide's
// evaluate-plan-acceptance skill requires. All fields are mandatory.
type planAcceptancePayload struct {
	Plan         string `json:"plan"`
	PlanID       string `json:"plan_id"`
	PlanName     string `json:"plan_name"`
	UserResponse string `json:"user_response"`
}

// planAcceptanceResult is the full output contract matching the Guide's
// evaluate-plan-acceptance SKILL.md output spec. Echoes all input fields
// plus the classification result and modification notes.
type planAcceptanceResult struct {
	Plan          string   `json:"plan"`
	PlanID        string   `json:"plan_id"`
	PlanName      string   `json:"plan_name"`
	UserResponse  string   `json:"user_response"`
	Result        string   `json:"result"`
	Modifications []string `json:"modifications"`
}

// buildPlanAcceptancePayload constructs the payload from plan data. Every field
// is derived from the plan — the caller only provides the user's response text.
func buildPlanAcceptancePayload(plan *DesignPlan, userResponse string) *planAcceptancePayload {
	planText := formatPlanForChat(plan)
	if planText == "" {
		planText = strings.TrimSpace(plan.Query)
	}
	planName := derivePlanName(plan)
	return &planAcceptancePayload{
		Plan:         planText,
		PlanID:       plan.ID,
		PlanName:     planName,
		UserResponse: userResponse,
	}
}

// derivePlanName extracts a human-readable name from the plan's query or ID.
func derivePlanName(plan *DesignPlan) string {
	if plan == nil {
		return ""
	}
	query := strings.TrimSpace(plan.Query)
	if query != "" {
		return truncateString(query, 120)
	}
	return plan.ID
}

// routePlanAcceptanceToGuide serializes the payload and sends it to the Guide
// for evaluation via the bus. Returns the Guide's structured response.
func (a *Architect) routePlanAcceptanceToGuide(
	ctx context.Context,
	sessionID string,
	payload *planAcceptancePayload,
) (any, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode acceptance payload: %w", err)
	}

	req := &guide.RouteRequest{
		Input:         "evaluate-plan-acceptance: " + string(encoded),
		TargetAgentID: "guide",
		SessionID:     sessionID,
	}

	response, err := a.requestRouteSync(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("guide acceptance evaluation failed: %w", err)
	}

	return extractAcceptanceResult(response, payload)
}

// extractAcceptanceResult unwraps the Guide's response into a
// planAcceptanceResult that satisfies the full SKILL.md output contract.
// The four input fields are always populated from the original payload;
// result and modifications are extracted from the Guide's response data.
func extractAcceptanceResult(
	msg *guide.Message,
	payload *planAcceptancePayload,
) (*planAcceptanceResult, error) {
	if msg == nil {
		return nil, fmt.Errorf("empty response from guide acceptance evaluation")
	}

	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		return nil, fmt.Errorf("unexpected response type from guide acceptance evaluation")
	}

	if !resp.Success {
		return nil, fmt.Errorf("guide acceptance evaluation returned error: %s", resp.Error)
	}

	out := &planAcceptanceResult{
		Plan:         payload.Plan,
		PlanID:       payload.PlanID,
		PlanName:     payload.PlanName,
		UserResponse: payload.UserResponse,
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		return out, nil
	}

	out.Result = acceptanceResultString(data)
	out.Modifications = acceptanceModifications(data)
	return out, nil
}

// acceptanceResultString extracts the "result" field from the Guide's
// response, defaulting to empty string if absent or non-string.
func acceptanceResultString(data map[string]any) string {
	v, ok := data["result"]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// acceptanceModifications extracts the "modifications" list from the Guide's
// response. Returns nil if absent or not a string slice.
func acceptanceModifications(data map[string]any) []string {
	v, ok := data["modifications"]
	if !ok {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	mods := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			continue
		}
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			mods = append(mods, trimmed)
		}
	}
	return mods
}

// =============================================================================
// handle_plan_acceptance_result — Act on the Guide's plan evaluation verdict
// =============================================================================

// acceptanceVerdict enumerates the three outcomes from the Guide's
// evaluate-plan-acceptance skill.
type acceptanceVerdict string

const (
	verdictAccept acceptanceVerdict = "accept"
	verdictModify acceptanceVerdict = "modify"
	verdictReject acceptanceVerdict = "reject"
)

type handlePlanAcceptanceResultParams struct {
	PlanID        string   `json:"plan_id"`
	Result        string   `json:"result"`
	UserResponse  string   `json:"user_response"`
	Modifications []string `json:"modifications"`
}

func handlePlanAcceptanceResultSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("handle_plan_acceptance_result").
		Description("Act on the Guide plan acceptance verdict to dispatch, revise, or request clarification.").
		Domain("coordination").
		Keywords("acceptance", "result", "dispatch", "modify", "reject", "verdict").
		Priority(100).
		StringParam("plan_id", "Plan identifier (uses latest ready plan if omitted)", false).
		EnumParam("result", "The Guide's verdict", []string{"accept", "modify", "reject"}, true).
		StringParam("user_response", "The user's original response that produced this verdict", true).
		ArrayParam("modifications", "Modification notes from the Guide (required when result is modify or reject)", "string", false).
		Usage("Use IMMEDIATELY after receiving the output from route_plan_acceptance. This skill acts on the Guide's verdict: accept dispatches to the orchestrator, modify applies revisions and re-routes for approval, reject asks the user for clarification and re-routes. Do NOT call this skill without first calling route_plan_acceptance — the inputs come directly from its output.").
		Example(`{"plan_id": "plan_abc", "result": "modify", "user_response": "Swap steps 2 and 3", "modifications": ["Reorder task 2 and task 3"]}`).
		BestPractice("Never skip this skill after route_plan_acceptance — the Guide's verdict must be acted on to close the feedback loop.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseAcceptanceResultParams(input)
			if err != nil {
				return nil, err
			}
			plan, err := a.resolveReadyPlanForAcceptance(params.PlanID)
			if err != nil {
				return nil, err
			}
			verdict := acceptanceVerdict(params.Result)
			switch verdict {
			case verdictAccept:
				return a.actOnAccept(ctx, plan)
			case verdictModify:
				return a.actOnModify(ctx, plan, params.UserResponse, params.Modifications)
			case verdictReject:
				return a.actOnReject(ctx, plan, params.UserResponse, params.Modifications)
			default:
				return nil, fmt.Errorf("unknown verdict: %q", params.Result)
			}
		}).
		Build()
}

func parseAcceptanceResultParams(input json.RawMessage) (*handlePlanAcceptanceResultParams, error) {
	var params handlePlanAcceptanceResultParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	result := strings.TrimSpace(strings.ToLower(params.Result))
	if result == "" {
		return nil, fmt.Errorf("result is required")
	}
	params.Result = result
	if strings.TrimSpace(params.UserResponse) == "" {
		return nil, fmt.Errorf("user_response is required")
	}
	return &params, nil
}

// actOnAccept dispatches the plan to the orchestrator for execution.
func (a *Architect) actOnAccept(ctx context.Context, plan *DesignPlan) (any, error) {
	a.logInfo("actOnAccept: dispatching plan", "plan_id", plan.ID)

	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentExecute,
		Query:     "user-approved plan execution",
		SessionID: plan.SessionID,
		Timestamp: time.Now(),
	}
	result, _ := a.dispatchPlanExecution(ctx, req, plan)
	return result, nil
}

// actOnModify applies the user's modifications to the plan, then re-routes
// the updated plan + user response back through the Guide for re-approval.
func (a *Architect) actOnModify(
	ctx context.Context,
	plan *DesignPlan,
	userResponse string,
	modifications []string,
) (any, error) {
	a.logInfo("actOnModify: applying modifications",
		"plan_id", plan.ID,
		"modification_count", len(modifications))

	reason := formatModificationReason(modifications)
	a.applyPlanRevision(plan, reason, nil)

	payload := buildPlanAcceptancePayload(plan, userResponse)
	guideResult, err := a.routePlanAcceptanceToGuide(ctx, plan.SessionID, payload)
	if err != nil {
		return nil, fmt.Errorf("re-evaluation after modify failed: %w", err)
	}

	return map[string]any{
		"action":           "modify",
		"plan_id":          plan.ID,
		"revision":         plan.Revision,
		"modifications":    modifications,
		"re_evaluation":    guideResult,
		"directive":        "re_approval_requested",
		"awaiting_user":    true,
		"response_to_user": formatModifyResponse(modifications),
	}, nil
}

// actOnReject records the rejection, then re-routes the plan + user response
// back through the Guide so the user can provide clarification or correction.
func (a *Architect) actOnReject(
	ctx context.Context,
	plan *DesignPlan,
	userResponse string,
	modifications []string,
) (any, error) {
	a.logInfo("actOnReject: plan rejected",
		"plan_id", plan.ID,
		"modification_count", len(modifications))

	reason := "plan rejected by user"
	if len(modifications) > 0 {
		reason = "plan rejected: " + strings.Join(modifications, "; ")
	}
	a.applyPlanRevision(plan, reason, nil)

	payload := buildPlanAcceptancePayload(plan, userResponse)
	guideResult, err := a.routePlanAcceptanceToGuide(ctx, plan.SessionID, payload)
	if err != nil {
		return nil, fmt.Errorf("re-evaluation after reject failed: %w", err)
	}

	return map[string]any{
		"action":           "reject",
		"plan_id":          plan.ID,
		"revision":         plan.Revision,
		"modifications":    modifications,
		"re_evaluation":    guideResult,
		"directive":        "clarification_requested",
		"awaiting_user":    true,
		"response_to_user": formatRejectResponse(modifications),
	}, nil
}

// formatModificationReason builds a revision reason string from modification
// notes returned by the Guide's acceptance evaluation.
func formatModificationReason(modifications []string) string {
	if len(modifications) == 0 {
		return "user requested modifications"
	}
	return "user modifications: " + strings.Join(modifications, "; ")
}

// formatModifyResponse builds the user-facing message after modifications are
// applied, prompting for re-approval.
func formatModifyResponse(modifications []string) string {
	var b strings.Builder
	b.WriteString("I've updated the plan with your requested changes")
	if len(modifications) > 0 {
		b.WriteString(":\n")
		for i, mod := range modifications {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, mod))
		}
	} else {
		b.WriteString(".\n")
	}
	b.WriteString("\nSay **go ahead** to proceed, or let me know what else to adjust.")
	return b.String()
}

// formatRejectResponse builds the user-facing message after a rejection,
// asking for clarification or a new direction.
func formatRejectResponse(modifications []string) string {
	var b strings.Builder
	b.WriteString("I understand this plan doesn't work as-is.")
	if len(modifications) > 0 {
		b.WriteString(" Here's what I noted:\n")
		for i, mod := range modifications {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, mod))
		}
	}
	b.WriteString("\nCould you clarify what direction you'd prefer, or what specific changes would make this work?")
	return b.String()
}
