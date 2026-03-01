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

// ---------------------------------------------------------------------------
// consult (consolidated: single, pre_planning, knowledge)
// ---------------------------------------------------------------------------

type consultInput struct {
	Mode            string `json:"mode"`
	Target          string `json:"target,omitempty"`
	Query           string `json:"query"`
	Scope           string `json:"scope,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	IncludeAcademic bool   `json:"include_academic,omitempty"`
}

var consultAllTargets = map[string]bool{
	"librarian": true, "archivalist": true, "academic": true,
	"engineer": true, "designer": true, "inspector": true, "tester": true,
}

var consultKnowledgeTargets = map[string]bool{
	"librarian": true, "archivalist": true, "academic": true,
}

func consultSkill(a *Architect) *skills.Skill {
	type handler = func(context.Context, *consultInput) (any, error)
	dispatch := map[string]handler{
		"single": func(ctx context.Context, p *consultInput) (any, error) {
			if strings.TrimSpace(p.Target) == "" {
				return nil, fmt.Errorf("target is required for mode=single")
			}
			if !consultAllTargets[p.Target] {
				return nil, fmt.Errorf("unknown consultation target: %q", p.Target)
			}
			if !a.isAgentRegistered(p.Target) {
				return map[string]any{
					"target": p.Target,
					"status": "not_registered",
					"message": fmt.Sprintf("agent %q is not registered; skip or choose a different target", p.Target),
				}, nil
			}
			evidence, err := a.requestConsultation(ctx, p.Target, p.Query, p.Scope, p.SessionID)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"target":   p.Target,
				"success":  evidence.Success,
				"evidence": evidence,
				"data":     evidence.Data,
			}, nil
		},
		"pre_planning": func(ctx context.Context, p *consultInput) (any, error) {
			plan := &DesignPlan{
				ID:            uuid.NewString(),
				Query:         p.Query,
				SessionID:     p.SessionID,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
				Constraints:   &PlanConstraints{Scope: p.Scope},
				Consultations: map[string]*ConsultationEvidence{},
			}
			req := &ArchitectRequest{
				ID:        uuid.NewString(),
				Intent:    IntentPlan,
				Query:     p.Query,
				SessionID: p.SessionID,
				Timestamp: time.Now(),
				Params: map[string]any{
					"include_academic": p.IncludeAcademic,
					"scope":            p.Scope,
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
		},
		"knowledge": func(ctx context.Context, p *consultInput) (any, error) {
			if strings.TrimSpace(p.Target) == "" {
				return nil, fmt.Errorf("target is required for mode=knowledge")
			}
			if !consultKnowledgeTargets[p.Target] {
				return nil, fmt.Errorf("knowledge target must be librarian, archivalist, or academic; got %q", p.Target)
			}
			if !a.running || a.bus == nil {
				return map[string]any{
					"status":  "unavailable",
					"message": "Event bus not available for consultation",
				}, nil
			}
			if !a.isAgentRegistered(p.Target) {
				return map[string]any{
					"target":  p.Target,
					"status":  "not_registered",
					"message": fmt.Sprintf("agent %q is not registered; skip or choose a different target", p.Target),
				}, nil
			}
			evidence, err := a.requestConsultation(ctx, p.Target, p.Query, p.Scope, "")
			if err != nil {
				return map[string]any{
					"target": p.Target,
					"status": "failed",
					"query":  p.Query,
					"error":  err.Error(),
				}, nil
			}
			return map[string]any{
				"target":   p.Target,
				"status":   "ok",
				"query":    p.Query,
				"evidence": evidence,
				"data":     evidence.Data,
			}, nil
		},
	}

	return skills.NewSkill("consult").
		Description("Consult agents for evidence before or during planning.\n\n"+
			"Modes:\n"+
			"- single: Consult one agent directly (params: target [required], query, scope, session_id)\n"+
			"- pre_planning: Mandatory consultation gate before plan creation (params: query, scope, session_id, include_academic)\n"+
			"- knowledge: Consult Librarian/Archivalist/Academic for evidence (params: target [required], query, scope)").
		Domain("consultation").
		Keywords("consult", "before planning", "context", "evidence", "librarian",
			"archivalist", "academic", "engineer", "designer", "inspector", "tester",
			"knowledge", "patterns", "history", "research").
		Priority(100).
		TokenEstimate(500).
		EnumParam("mode", "Consultation mode", []string{"single", "pre_planning", "knowledge"}, true).
		EnumParam("target", "Agent to consult", []string{
			"librarian", "archivalist", "academic", "engineer", "designer", "inspector", "tester",
		}, false).
		StringParam("query", "Question or topic to consult about", true).
		StringParam("scope", "Scope to limit the search", false).
		StringParam("session_id", "Session identifier", false).
		BoolParam("include_academic", "Whether to require Academic consultation (pre_planning mode)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params consultInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Query) == "" {
				return nil, fmt.Errorf("query is required")
			}
			fn, ok := dispatch[params.Mode]
			if !ok {
				return nil, fmt.Errorf("unknown consult mode: %q", params.Mode)
			}
			return fn(ctx, &params)
		}).
		Build()
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
		BestPractice("If validation fails due to stale consultations, re-run `consult` with mode=pre_planning rather than creating a new declaration from scratch.").
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
		target := parsePlanStatus(status, plan.Status)
		if err := plan.SM().TransitionTo(target, plan); err == nil {
			plan.Status = plan.SM().State()
		}
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
		"clarifying":    PlanStatusClarifying,
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
	if smErr := current.SM().TransitionTo(PlanStatusReady, current); smErr != nil {
		a.logger.Warn("attachFixWorkflow: transition to Ready rejected",
			"plan_id", current.ID, "error", smErr)
		a.activePlansMu.Unlock()
		return current.ID
	}
	current.Status = current.SM().State()
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
		Usage("Use when the user or system signals a stop, pause, resume, or cancel for an active plan. Updates the plan status safely and records the reason. Valid actions: pause (→pending), resume (→executing), cancel/stop (→failed). Do NOT use for plan revision — use `plan` with action=revise instead.").
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
	target := interruptStatus(action, current.SM().State())
	if smErr := current.SM().TransitionTo(target, current); smErr != nil {
		a.logger.Warn("applyInterruptAction: transition rejected",
			"plan_id", current.ID, "action", action, "error", smErr)
		a.activePlansMu.Unlock()
		return current
	}
	current.Status = current.SM().State()
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

// ---------------------------------------------------------------------------
// plan_mode (consolidated: enter, update_file, exit, todo_write, todo_mark_complete)
// ---------------------------------------------------------------------------

type planModeInput struct {
	Action          string     `json:"action"`
	SessionID       string     `json:"session_id,omitempty"`
	TaskDescription string     `json:"task_description,omitempty"`
	PlanFile        string     `json:"plan_file,omitempty"`
	Content         string     `json:"content,omitempty"`
	Append          bool       `json:"append,omitempty"`
	Todos           []PlanTodo `json:"todos,omitempty"`
	Index           int        `json:"index,omitempty"`
	AllowedPrompts  []string   `json:"allowed_prompts,omitempty"`
}

func planModeSkill(a *Architect) *skills.Skill {
	type handler = func(context.Context, *planModeInput) (any, error)
	dispatch := map[string]handler{
		"enter": func(_ context.Context, p *planModeInput) (any, error) {
			if strings.TrimSpace(p.TaskDescription) == "" {
				return nil, fmt.Errorf("task_description is required for action=enter")
			}
			mode := a.enterPlanMode(p.SessionID, p.PlanFile, p.TaskDescription)
			return mode, nil
		},
		"update_file": func(_ context.Context, p *planModeInput) (any, error) {
			mode, err := a.getPlanMode(p.SessionID)
			if err != nil {
				return nil, err
			}
			if err := writePlanFile(mode.PlanFile, p.Content, p.Append); err != nil {
				return nil, err
			}
			mode.UpdatedAt = time.Now()
			return map[string]any{"plan_file": mode.PlanFile, "updated": mode.UpdatedAt}, nil
		},
		"exit": func(_ context.Context, p *planModeInput) (any, error) {
			mode, err := a.getPlanMode(p.SessionID)
			if err != nil {
				return nil, err
			}
			mode.AwaitingApproval = true
			mode.AllowedPrompts = p.AllowedPrompts
			mode.UpdatedAt = time.Now()
			return map[string]any{
				"session_id":        mode.SessionID,
				"awaiting_approval": mode.AwaitingApproval,
				"allowed_prompts":   mode.AllowedPrompts,
			}, nil
		},
		"todo_write": func(_ context.Context, p *planModeInput) (any, error) {
			mode, err := a.getPlanMode(p.SessionID)
			if err != nil {
				return nil, err
			}
			mode.Todos = p.Todos
			mode.UpdatedAt = time.Now()
			return map[string]any{"todos": mode.Todos, "count": len(mode.Todos)}, nil
		},
		"todo_mark_complete": func(_ context.Context, p *planModeInput) (any, error) {
			mode, err := a.getPlanMode(p.SessionID)
			if err != nil {
				return nil, err
			}
			if p.Index < 0 || p.Index >= len(mode.Todos) {
				return nil, fmt.Errorf("todo index out of range")
			}
			mode.Todos[p.Index].Status = "completed"
			mode.UpdatedAt = time.Now()
			return map[string]any{
				"index":      p.Index,
				"todo":       mode.Todos[p.Index],
				"todo_count": len(mode.Todos),
				"updated_at": mode.UpdatedAt,
			}, nil
		},
	}

	return skills.NewSkill("plan_mode").
		Description("Manage plan mode lifecycle for structured planning with approval gates.\n\n"+
			"Actions:\n"+
			"- enter: Enter plan mode (params: task_description [required], plan_file, session_id)\n"+
			"- update_file: Write/append to plan markdown file (params: content, append, session_id)\n"+
			"- exit: Mark plan as awaiting user approval (params: allowed_prompts, session_id)\n"+
			"- todo_write: Create/replace todo list (params: todos [required], session_id)\n"+
			"- todo_mark_complete: Mark a todo as completed (params: index [required], session_id)").
		Domain("planning").
		Keywords("plan mode", "complex", "design", "architecture", "approval",
			"update plan file", "plan markdown", "revise document",
			"exit plan mode", "review", "ready",
			"todo", "tasks", "tracking", "progress", "complete", "mark done").
		Priority(80).
		TokenEstimate(450).
		EnumParam("action", "Plan mode action", []string{
			"enter", "update_file", "exit", "todo_write", "todo_mark_complete",
		}, true).
		StringParam("session_id", "Session identifier", false).
		StringParam("task_description", "Task to plan (required for enter)", false).
		StringParam("plan_file", "Optional plan markdown file path (for enter)", false).
		StringParam("content", "Content to write (for update_file)", false).
		BoolParam("append", "Append instead of overwrite (for update_file)", false).
		ArrayParam("todos", "Todo objects with content/status/active_form (for todo_write)", "object", false).
		IntParam("index", "0-based todo index (for todo_mark_complete)", false).
		ArrayParam("allowed_prompts", "Permitted command prompts (for exit)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params planModeInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown plan_mode action: %q", params.Action)
			}
			return fn(ctx, &params)
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
			sessionID := normalizeSessionID(params.SessionID)
			// When invoked during the planning protocol's clarify step,
			// attach the questions to the in-flight plan so stepClarify
			// can detect that clarification was requested.
			if plan := a.latestConsultingPlan(sessionID); plan != nil {
				plan.ClarificationQuestions = extractQuestionTexts(params.Questions)
			}
			return map[string]any{
				"status":     "clarification_required",
				"session_id": sessionID,
				"questions":  params.Questions,
			}, nil
		}).
		Build()
}

// extractQuestionTexts pulls the "question" string from each question
// object map, filtering empty entries.
func extractQuestionTexts(questions []map[string]any) []string {
	texts := make([]string, 0, len(questions))
	for _, q := range questions {
		text := strings.TrimSpace(fmt.Sprint(q["question"]))
		if text == "" || text == "<nil>" {
			continue
		}
		texts = append(texts, text)
	}
	return texts
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
// start_planning — transition from conversation to plan generation
// ---------------------------------------------------------------------------

type startPlanningInput struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id,omitempty"`
}

func startPlanningSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("start_planning").
		Description("Transition from conversation to plan generation. "+
			"Synthesizes the conversation into a planning query and executes the full planning protocol.").
		Domain("planning").
		Keywords("start planning", "create plan", "formalize", "generate plan").
		Priority(100).
		TokenEstimate(300).
		StringParam("query", "Synthesized planning query capturing all requirements, constraints, and scope gathered from the conversation", true).
		StringParam("session_id", "Session identifier for plan tracking", false).
		Usage("Invoke when the conversation has reached sufficient clarity to produce an actionable plan. "+
			"The query must synthesize all requirements, constraints, technology choices, and scope from the conversation — "+
			"do not just repeat the user's last message.").
		BestPractice("Synthesize the full conversation context into the query — do not just repeat the user's last message.").
		BestPractice("Before invoking, confirm with the user that they are ready to proceed to planning.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params startPlanningInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			query := strings.TrimSpace(params.Query)
			if query == "" {
				return nil, fmt.Errorf("query is required")
			}
			sessionID := normalizeSessionID(params.SessionID)
			if sessionID == "default" {
				if ctxSession := architectSessionIDFromContext(ctx); ctxSession != "" {
					sessionID = ctxSession
				}
			}
			req := &ArchitectRequest{
				ID:        uuid.NewString(),
				Intent:    IntentPlan,
				Query:     query,
				SessionID: sessionID,
				Timestamp: time.Now(),
			}
			plan, err := a.executePlanningProtocol(ctx, req)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"plan_id": plan.ID,
				"status":  plan.Status.String(),
				"tasks":   len(plan.Tasks),
				"summary": truncateString(formatPlanForChat(plan), 500),
			}, nil
		}).
		Build()
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
		BestPractice("If the result is 'modify', read the modifications list and apply changes via plan action=revise before re-presenting.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseRoutePlanAcceptanceParams(input)
			if err != nil {
				return nil, err
			}
			sessionID := architectSessionIDFromContext(ctx)
			plan, err := a.resolveReadyPlanForAcceptance(params.PlanID, sessionID)
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
// latest ready plan for the given session. Returns an error if no eligible plan exists.
func (a *Architect) resolveReadyPlanForAcceptance(planID, sessionID string) (*DesignPlan, error) {
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
	plan := a.latestReadyPlan(sessionID)
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
			plan, err := a.resolveReadyPlanForAcceptance(params.PlanID, "")
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
