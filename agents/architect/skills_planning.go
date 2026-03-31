package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// consult (consolidated: single, pre_planning, knowledge)
// ---------------------------------------------------------------------------

type consultInput struct {
	Mode      string `json:"mode"`
	Target    string `json:"target,omitempty"`
	Query     string `json:"query"`
	Scope     string `json:"scope,omitempty"`
	Depth     string `json:"depth,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
}

var consultAllTargets = map[string]bool{
	"librarian": true, "archivalist": true, "academic": true,
	"engineer": true, "designer": true, "inspector": true, "tester": true, "orchestrator": true,
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
					"target":  p.Target,
					"status":  "not_registered",
					"message": fmt.Sprintf("agent %q is not registered; skip or choose a different target", p.Target),
				}, nil
			}
			evidence, err := a.requestConsultationWithMetadata(
				ctx,
				p.Target,
				p.Query,
				p.Scope,
				p.SessionID,
				shared.ConsultationMetadataWithResearchDepth(nil, p.Depth),
			)
			if err != nil {
				if errors.Is(err, skills.ErrDelegatedRequested) {
					return nil, err
				}
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
			protoPlan, hasPlan := a.resolveProtocolPlan(p.PlanID)
			var plan *DesignPlan
			if hasPlan {
				plan = protoPlan
			} else {
				plan = &DesignPlan{
					ID:            uuid.NewString(),
					Query:         p.Query,
					SessionID:     p.SessionID,
					CreatedAt:     time.Now(),
					UpdatedAt:     time.Now(),
					Constraints:   &PlanConstraints{Scope: p.Scope},
					Consultations: map[string]*ConsultationEvidence{},
				}
			}
			if hasPlan {
				if consultationTargetsSatisfied(plan, a.config.ConsultationMaxAge) {
					plan.CodebasePatterns = extractLibrarianPatterns(plan)
					if err := a.persistPlanState(plan); err != nil {
						return nil, err
					}
					return map[string]any{
						"ready":                   true,
						"consultations":           plan.Consultations,
						"suggested_targets":       defaultDiscussionConsultationTargets(),
						"stale_or_failed_targets": staleOrFailedConsultationTargets(plan.Consultations, a.config.ConsultationMaxAge),
						"reused":                  true,
					}, nil
				}
				if !hasReachedPlanPhase(plan.SM().State(), PlanStatusConsulting) {
					if err := a.advancePlan(ctx, plan, PlanStatusConsulting, nil); err != nil {
						return nil, err
					}
				}
			}
			if hasPlan {
				plan.CodebasePatterns = extractLibrarianPatterns(plan)
				if err := a.persistPlanState(plan); err != nil {
					return nil, err
				}
			}
			return map[string]any{
				"ready":                   true,
				"consultations":           plan.Consultations,
				"suggested_targets":       defaultDiscussionConsultationTargets(),
				"stale_or_failed_targets": staleOrFailedConsultationTargets(plan.Consultations, a.config.ConsultationMaxAge),
				"message":                 "Synthesize the discussion-time consultation evidence, refresh only the material gaps, then move into design.",
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
			evidence, err := a.requestConsultationWithMetadata(
				ctx,
				p.Target,
				p.Query,
				p.Scope,
				"",
				shared.ConsultationMetadataWithResearchDepth(nil, p.Depth),
			)
			if err != nil {
				if errors.Is(err, skills.ErrDelegatedRequested) {
					return nil, err
				}
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
		Description("Consult agents for evidence during live discussion and during planning synthesis.\n\n"+
			"Modes:\n"+
			"- single: Consult one execution or coordination agent directly (params: target [required], query, scope, session_id)\n"+
			"- pre_planning: Consolidate and refresh discussion-time consultation evidence before design (params: query, scope, session_id)\n"+
			"- knowledge: Consult Librarian/Archivalist/Academic during discussion or planning for evidence (params: target [required], query, scope)").
		Domain("consultation").
		Keywords("consult", "before planning", "context", "evidence", "librarian",
			"archivalist", "academic", "engineer", "designer", "inspector", "tester",
			"knowledge", "patterns", "history", "research").
		Priority(100).
		TokenEstimate(500).
		EnumParam("mode", "Consultation mode", []string{"single", "pre_planning", "knowledge"}, true).
		EnumParam("target", "Agent to consult", []string{
			"librarian", "archivalist", "academic", "engineer", "designer", "inspector", "tester", "orchestrator",
		}, false).
		StringParam("query", "Question or topic to consult about", true).
		StringParam("scope", "Scope to limit the search", false).
		EnumParam("depth", "Research depth for Academic consultations", shared.ResearchDepthEnumValues(), false).
		StringParam("session_id", "Session identifier", false).
		StringParam("plan_id", "Plan identifier for protocol-driven consultation", false).
		Usage("Use during conversation as new material information arrives, and again during planning to consolidate what you learned. Prefer repeated targeted consults over one broad consult. Do not defer obvious codebase, historical, or Academic evidence gathering until formal plan creation. `consult(mode=pre_planning)` should synthesize and refresh the evidence already gathered during discussion, not begin from zero.").
		BestPractice("On the first substantive turn for a new implementation, planning, or architecture problem, start with the most relevant knowledge agent and the narrowest question that can materially reduce the next uncertainty. Add other knowledge agents only as concrete unresolved questions remain.").
		BestPractice("During live discussion, consult the Librarian when codebase-fit or local-pattern questions emerge, the Archivalist when historical decisions or preferences matter, and the Academic when architecture quality, correctness, performance, testing, infrastructure, or tradeoffs materially affect the outcome.").
		BestPractice("Do not wait for literal keywords like 'research' or 'benchmark' to consult the Academic. Use it whenever the conversation materially needs stronger alternatives, best practices, or external grounding.").
		BestPractice("Re-evaluate Academic depth every time the question changes. Use `minimal` or `quick` for narrow validation, `standard` for ordinary planning tradeoffs, `deep` for decision-critical architectural questions, and `comprehensive` only when broader corroboration could materially change a high-stakes or reusable conclusion.").
		BestPractice("Do not ask for `comprehensive` depth by default. Use it when the planning decision is materially expensive, irreversible, externally dependent, or likely to be reused as a formal research input.").
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
		Usage("Use before handing off to the Orchestrator. Creates a formal declaration recording the target agent, reasoning, required skills, expected outcome, failure criteria, and any consultation evidence already gathered. The declaration validation checks any attached consultation evidence for freshness and success, but it is not a fixed consultation checklist. Do NOT create declarations for tasks that lack clear success criteria.").
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
			a.publishDeclaration(ctx, declaration, plan.SessionID)
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
	plans := a.planStore.Snapshot()
	if len(plans) == 0 {
		return nil
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
	for target, evidence := range declaration.ConsultationChecks {
		if evidence == nil {
			return fmt.Errorf("invalid consultation evidence for %s", target)
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

func consultationTargetsSatisfied(plan *DesignPlan, maxAge time.Duration) bool {
	if plan == nil {
		return false
	}
	for _, evidence := range plan.Consultations {
		if evidence == nil || !evidence.Success || !isConsultationFresh(evidence, maxAge) {
			return false
		}
	}
	return true
}

func defaultDiscussionConsultationTargets() []string {
	return []string{"librarian", "archivalist", "academic"}
}

func staleOrFailedConsultationTargets(consultations map[string]*ConsultationEvidence, maxAge time.Duration) []string {
	if len(consultations) == 0 {
		return nil
	}
	targets := make([]string, 0, len(consultations))
	for target, evidence := range consultations {
		if evidence == nil || !evidence.Success || !isConsultationFresh(evidence, maxAge) {
			targets = append(targets, target)
		}
	}
	sort.Strings(targets)
	return targets
}

func (a *Architect) persistDeclaration(plan *DesignPlan, declaration *PreDelegationDeclaration) {
	if plan == nil || declaration == nil {
		return
	}
	existing := a.planStore.Get(plan.ID)
	if existing == nil {
		return
	}
	existing.Declarations = append(existing.Declarations, declaration)
	existing.UpdatedAt = time.Now()
	if err := a.planStore.Upsert(existing); err != nil {
		a.logger.Warn("failed to persist declaration plan snapshot", "plan_id", existing.ID, "error", err)
	}
}

func (a *Architect) publishDeclaration(ctx context.Context, declaration *PreDelegationDeclaration, sessionID string) {
	if declaration == nil || !a.running || a.bus == nil {
		return
	}
	input, err := guide.ArchivalistStoreRouteInput("stored pre-delegation declaration")
	if err != nil {
		a.logWarn("failed to encode archivalist declaration route",
			"declaration_id", declaration.ID,
			"session_id", sessionID,
			"error", err)
		return
	}
	branchCtx, branch := shared.BeginArchivalistStoreBranch(ctx, "stored pre-delegation declaration", map[string]any{
		"declaration_id": declaration.ID,
		"session_id":     sessionID,
	})
	metadata := branch.ApplyMetadata(branchCtx, map[string]any{"declaration": declaration})
	req := &guide.RouteRequest{
		CorrelationID: "decl_" + uuid.NewString(),
		Input:         input,
		SourceAgentID: a.id,
		FireAndForget: true,
		SessionID:     sessionID,
		Timestamp:     time.Now(),
		Metadata:      metadata,
	}
	if req.ParentCorrelationID == "" {
		if stream, ok := shared.StreamMetadataFromContext(branchCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	msg := guide.NewRequestMessage(a.generateMessageID(), req)
	msg.Metadata = metadata
	if err := a.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		branch.Complete(branchCtx, "", "", err)
		a.logWarn("failed to publish pre-delegation declaration",
			"declaration_id", declaration.ID,
			"session_id", sessionID,
			"error", err)
		return
	}
	branch.Complete(branchCtx, "stored pre-delegation declaration", "", nil)
}

type validatePreDelegationParams struct {
	PlanID        string `json:"plan_id,omitempty"`
	DeclarationID string `json:"declaration_id,omitempty"`
}

func validatePreDelegationSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("validate_pre_delegation").
		Description("Validate a pre-delegation declaration and sanity-check any attached consultation evidence.").
		Domain("delegation").
		Keywords("validate", "pre delegation", "declaration", "consultation").
		Priority(95).
		StringParam("plan_id", "Plan identifier", false).
		StringParam("declaration_id", "Declaration identifier", false).
		Usage("Use to validate an existing declaration before proceeding with handoff. Returns valid=true when the declaration is structurally sound and any attached consultation evidence is successful and within the configured max age. This is not a fixed consultation checklist; gather the consultations that materially matter for the task before `handoff_to_orchestrator`.").
		Example(`{"plan_id": "plan_abc", "declaration_id": "decl_xyz"}`).
		BestPractice("If validation fails due to stale consultation evidence, refresh the material consultations and then re-run validation rather than creating a new declaration from scratch.").
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
		PlanFile:        plan.PlanFile,
		Timestamp:       time.Now(),
		Architecture:    plan.Architecture,
		Requirements:    plan.Requirements,
		Assumptions:     plan.Assumptions,
	}
}

func atomicTaskToHandoff(t *AtomicTask) *HandoffTask {
	h := &HandoffTask{
		ID:                  t.ID,
		Slug:                taskSlugForTask(t, 0),
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
		Workspace:           t.Workspace,
		WorkerPackets:       t.WorkerPackets,
		ExecutionContracts:  t.ExecutionContracts,
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
				TargetAgentID: a.planHandoffTargetAgentID(plan),
				SessionID:     plan.SessionID,
			}
			if req.TargetAgentID == "" {
				return map[string]any{
					"status":      "local_fallback",
					"plan_id":     plan.ID,
					"plan_status": plan.Status.String(),
					"error":       "no registered orchestrator agent id is available",
				}, nil
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
	current := a.planStore.Get(plan.ID)
	if current == nil {
		return plan
	}
	priorPlanFile := strings.TrimSpace(current.PlanFile)
	nextVersion := normalizedPlanArtifactVersion(current).BumpMinor()
	nextPlanFile := nextVersionedPlanMarkdownPath(a.config.WorkingDirectory, current, nextVersion)
	if err := clonePlanMarkdownToVersion(strings.TrimSpace(current.PlanFile), nextPlanFile, current.Query, nextVersion); err != nil {
		a.logger.Warn("failed to rotate revised plan markdown", "plan_id", current.ID, "error", err)
	}
	current.Revision++
	current.ArtifactVersion = nextVersion
	current.PlanFile = nextPlanFile
	current.UpdatedAt = time.Now()
	current.RiskSummary = append(current.RiskSummary, reason)
	applyPlanUpdateFields(current, updates)
	a.syncPlanModePlanFile(current.SessionID, priorPlanFile, nextPlanFile)
	if err := a.planStore.Upsert(current); err != nil {
		a.logger.Warn("failed to persist revised plan snapshot", "plan_id", current.ID, "error", err)
	}
	return current
}

func (a *Architect) syncPlanModePlanFile(sessionID, oldPath, newPath string) {
	if a == nil || strings.TrimSpace(newPath) == "" {
		return
	}
	normalized := normalizeSessionID(sessionID)
	a.planModesMu.Lock()
	defer a.planModesMu.Unlock()
	mode := a.planModes[normalized]
	if mode == nil {
		return
	}
	if strings.TrimSpace(oldPath) != "" && !samePath(mode.PlanFile, oldPath) {
		return
	}
	mode.PlanFile = newPath
	mode.UpdatedAt = time.Now()
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
	current := a.planStore.Get(plan.ID)
	if current == nil {
		return ""
	}
	current.Workflow = workflow
	current.Tasks = tasks
	if smErr := current.SM().TransitionTo(PlanStatusReady, current); smErr != nil {
		a.logger.Warn("attachFixWorkflow: transition to Ready rejected",
			"plan_id", current.ID, "error", smErr)
		return current.ID
	}
	current.Status = current.SM().State()
	current.Epoch = current.SM().Epoch()
	current.UpdatedAt = time.Now()
	if lm := a.planStore.LeaseManager(); lm != nil {
		lm.GrantReadyLease(current)
	}
	if err := a.planStore.Upsert(current); err != nil {
		a.logger.Warn("failed to persist fix workflow snapshot", "plan_id", current.ID, "error", err)
	}
	return current.ID
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
	current := a.planStore.Get(plan.ID)
	if current == nil {
		return plan
	}
	target := interruptStatus(action, current.SM().State())
	if smErr := current.SM().TransitionTo(target, current); smErr != nil {
		a.logger.Warn("applyInterruptAction: transition rejected",
			"plan_id", current.ID, "action", action, "error", smErr)
		return current
	}
	current.Status = current.SM().State()
	if strings.TrimSpace(reason) != "" {
		current.RiskSummary = append(current.RiskSummary, reason)
	}
	current.UpdatedAt = time.Now()
	if err := a.planStore.Upsert(current); err != nil {
		a.logger.Warn("failed to persist interrupted plan snapshot", "plan_id", current.ID, "error", err)
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
	planID := uuid.NewString()
	resolvedFile := resolvePlanFile(a.config.WorkingDirectory, normalizedSession, taskDescription, planID, planFile)
	_ = ensurePlanMarkdownFileExists(resolvedFile, taskDescription, normalizedPlanArtifactVersion(&DesignPlan{Revision: 1}))
	mode := &PlanModeState{
		SessionID:        normalizedSession,
		PlanID:           planID,
		PlanName:         taskDescription,
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

func resolvePlanFile(workDir, sessionID, planName, planID, planFile string) string {
	if strings.TrimSpace(planFile) != "" {
		var candidate string
		if filepath.IsAbs(planFile) {
			candidate = planFile
		} else {
			candidate = filepath.Join(workDir, planFile)
		}
		return nextVersionedPlanMarkdownPath(workDir, &DesignPlan{
			ID:              planID,
			SessionID:       sessionID,
			Query:           planName,
			PlanFile:        candidate,
			Revision:        1,
			ArtifactVersion: normalizedPlanArtifactVersion(&DesignPlan{Revision: 1}),
		}, normalizedPlanArtifactVersion(&DesignPlan{Revision: 1}))
	}
	return defaultVersionedPlanMarkdownPath(workDir, sessionID, planName, planID, normalizedPlanArtifactVersion(&DesignPlan{Revision: 1}))
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
		Usage("Use as a last resort when the Architect cannot resolve one or two narrow ambiguities through consultation or plan analysis alone. Creates a structured clarification request with multiple-choice options. Do NOT use this for broad, underspecified requests that need exploratory clarification — use route_requirements_research for that.").
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
			// When invoked during the planning protocol tool loop,
			// attach the questions to the in-flight plan so the protocol
			// can detect that clarification was requested.
			if plan := a.latestConsultingPlan(sessionID); plan != nil {
				plan.ClarificationQuestions = extractQuestionTexts(params.Questions)
				if plan.SM().State() == PlanStatusConsulting {
					if err := plan.SM().TransitionTo(PlanStatusClarifying, plan); err == nil {
						plan.Status = plan.SM().State()
						plan.Epoch = plan.SM().Epoch()
					}
				}
				plan.UpdatedAt = time.Now().UTC()
				a.persistPlanStateBestEffort(plan, originalCIDFromContext(ctx), "clarification questions recorded")
			}
			userMessage := formatClarificationNotification(params.Questions)
			return map[string]any{
				"status":       "clarification_required",
				"session_id":   sessionID,
				"questions":    params.Questions,
				"user_message": userMessage,
			}, nil
		}).
		Build()
}

type routeRequirementsResearchParams struct {
	PlanID              string   `json:"plan_id,omitempty"`
	OriginalInput       string   `json:"original_input"`
	Reason              string   `json:"reason"`
	ResearchGoal        string   `json:"research_goal,omitempty"`
	KnownContext        []string `json:"known_context,omitempty"`
	MissingRequirements []string `json:"missing_requirements,omitempty"`
}

type requirementsResearchHandoffPayload struct {
	PlanID              string   `json:"plan_id,omitempty"`
	OriginalInput       string   `json:"original_input"`
	Reason              string   `json:"reason"`
	ResearchGoal        string   `json:"research_goal,omitempty"`
	KnownContext        []string `json:"known_context,omitempty"`
	MissingRequirements []string `json:"missing_requirements,omitempty"`
}

func routeRequirementsResearchSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("route_requirements_research").
		Description("Hand the conversation to the Academic when the request is too vague or underspecified for reliable planning.").
		Domain("coordination").
		Keywords("underspecified", "vague", "requirements", "research", "clarify", "academic").
		Priority(95).
		StringParam("plan_id", "Current plan identifier when this handoff occurs mid-protocol", false).
		StringParam("original_input", "The user's implementation request or latest relevant message", true).
		StringParam("reason", "Why the Architect cannot safely plan yet", true).
		StringParam("research_goal", "What the Academic should help clarify or research", false).
		ArrayParam("known_context", "Facts or constraints already established", "string", false).
		ArrayParam("missing_requirements", "Concrete gaps that must be clarified before planning", "string", false).
		Usage("Use when the implementation request is still too vague or underspecified to produce a responsible plan. This performs a Guide-routed user handoff to the Academic so the problem can be clarified before planning continues. Prefer this over ask_user_question when the user needs exploratory clarification or requirements-shaping, not just one narrow decision.").
		Example(`{"original_input":"Build a production-ready observability system for our platform.","reason":"The request does not define scope, target workloads, retention, compliance constraints, or success criteria.","research_goal":"Clarify scope, operational constraints, and the minimum viable rollout plan.","missing_requirements":["Target services and traffic profile","Required retention/compliance constraints","Primary success metrics"]}`).
		BestPractice("Use ask_user_question for one or two concrete decisions. Use this tool when the user first needs help defining the problem space well enough to plan.").
		BestPractice("Do NOT use this when the blocker is codebase or change-history evidence. In that case, consult the Librarian or Archivalist instead of handing the user away.").
		BestPractice("Pass the user's real request in original_input and summarize the missing requirements precisely — that context becomes the Academic's hidden handoff brief.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := parseRouteRequirementsResearchParams(input)
			if err != nil {
				return nil, err
			}
			return a.submitRequirementsResearchHandoff(ctx, params)
		}).
		Build()
}

func parseRouteRequirementsResearchParams(input json.RawMessage) (*routeRequirementsResearchParams, error) {
	var params routeRequirementsResearchParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	params.PlanID = strings.TrimSpace(params.PlanID)
	params.OriginalInput = strings.TrimSpace(params.OriginalInput)
	params.Reason = strings.TrimSpace(params.Reason)
	params.ResearchGoal = strings.TrimSpace(params.ResearchGoal)
	params.KnownContext = dedupeNonEmptyStrings(params.KnownContext)
	params.MissingRequirements = dedupeNonEmptyStrings(params.MissingRequirements)
	if params.Reason == "" {
		return nil, fmt.Errorf("reason is required")
	}
	return &params, nil
}

func (a *Architect) submitRequirementsResearchHandoff(
	ctx context.Context,
	params *routeRequirementsResearchParams,
) (map[string]any, error) {
	if a == nil || params == nil {
		return nil, fmt.Errorf("requirements research handoff is not configured")
	}
	if !a.running || a.bus == nil {
		return nil, fmt.Errorf("bus unavailable for academic handoff")
	}

	sessionID := normalizeSessionID(architectSessionIDFromContext(ctx))
	targetAgentID := a.knownAgentIDByType("academic", "academic")
	originalInput, handoffHistory := architectHandoffContext(ctx)
	if params.OriginalInput != "" {
		originalInput = params.OriginalInput
	}
	if strings.TrimSpace(originalInput) == "" {
		return nil, fmt.Errorf("original_input is required for academic handoff")
	}
	plan := a.resolveRequirementsResearchPlan(params.PlanID, sessionID)
	if plan != nil && plan.PendingWork != nil && plan.PendingWork.Kind == string(continuationKindAcademicHandoff) {
		message := strings.TrimSpace(plan.PendingWork.Message)
		if message == "" {
			message = "I'm already routing this request through the Academic so we can sharpen the requirements before planning."
		}
		return map[string]any{
			"status":         "requirements_research_pending",
			"plan_id":        plan.ID,
			"correlation_id": plan.PendingWork.CorrelationID,
			"target_agent":   plan.PendingWork.TargetAgentID,
			"user_message":   message,
		}, nil
	}

	payload := &requirementsResearchHandoffPayload{
		PlanID:              params.PlanID,
		OriginalInput:       originalInput,
		Reason:              params.Reason,
		ResearchGoal:        params.ResearchGoal,
		KnownContext:        append([]string(nil), params.KnownContext...),
		MissingRequirements: append([]string(nil), params.MissingRequirements...),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode academic handoff: %w", err)
	}
	rawArgs, _ := json.Marshal(params)

	correlationID := "academic_" + uuid.NewString()
	userMessage := "This request is still too vague for me to plan responsibly. I'm handing you to the Academic to sharpen the requirements and constraints before I continue."
	record := &ArchitectContinuation{
		ID:                      "cont_" + uuid.NewString(),
		Kind:                    continuationKindAcademicHandoff,
		State:                   continuationStatusPending,
		PlanID:                  planID(plan),
		SessionID:               sessionID,
		TargetAgentID:           targetAgentID,
		ResponseCorrelationID:   correlationID,
		InvocationCorrelationID: originalCIDFromContext(ctx),
		ToolName:                "route_requirements_research",
		RawArguments:            string(rawArgs),
		RequestJSON:             string(encoded),
		CreatedAt:               time.Now().UTC(),
		ExpiresAt:               time.Now().UTC().Add(routeSyncTimeout),
	}
	if err := a.recordPendingContinuation(plan, record, userMessage); err != nil {
		return nil, err
	}

	req := &guide.RouteRequest{
		Input:               originalInput,
		CorrelationID:       correlationID,
		ParentCorrelationID: originalCIDFromContext(ctx),
		TargetAgentID:       targetAgentID,
		SessionID:           sessionID,
		Metadata: map[string]any{
			"user_facing_handoff":       true,
			"handoff_kind":              "requirements_clarification",
			"handoff_from":              "architect",
			"handoff_reason":            params.Reason,
			"handoff_research_goal":     params.ResearchGoal,
			"handoff_known_context":     append([]string(nil), params.KnownContext...),
			"handoff_missing_questions": append([]string(nil), params.MissingRequirements...),
			"original_request_agent":    "architect",
		},
	}
	if plan != nil {
		req.Metadata["plan_id"] = plan.ID
	}
	if strings.TrimSpace(handoffHistory) != "" {
		req.Metadata["handoff_conversation_history"] = handoffHistory
	}
	if err := a.publishRouteRequest(req); err != nil {
		if plan != nil {
			a.clearPlanPendingContinuationBestEffort(plan, correlationID, "academic handoff publish failed")
		}
		a.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "academic handoff publish failed")
		return nil, fmt.Errorf("academic handoff failed: %w", err)
	}

	if plan != nil {
		a.markPlanClarifyingForResearch(plan, params.MissingRequirements)
	}
	if originalCID := originalCIDFromContext(ctx); originalCID != "" {
		a.publishHandoffReroute(ctx, targetAgentID, "requirements clarification handoff", originalCID, correlationID)
	}

	payloadMap := map[string]any{
		"status":         "requirements_research_pending",
		"plan_id":        planID(plan),
		"correlation_id": correlationID,
		"target_agent":   targetAgentID,
		"user_message":   userMessage,
	}
	return nil, skills.NewDelegatedError(payloadMap, userMessage)
}

func (a *Architect) resolveRequirementsResearchPlan(planID, sessionID string) *DesignPlan {
	if a == nil || a.planStore == nil {
		return nil
	}
	if trimmed := strings.TrimSpace(planID); trimmed != "" {
		return a.planStore.Get(trimmed)
	}
	if plan := a.latestConsultingPlan(sessionID); plan != nil {
		return plan
	}
	return a.latestClarifyingPlan(sessionID)
}

func (a *Architect) markPlanClarifyingForResearch(plan *DesignPlan, missing []string) {
	if a == nil || plan == nil {
		return
	}
	if len(missing) > 0 {
		plan.ClarificationQuestions = append([]string(nil), missing...)
	}
	if plan.SM().State() == PlanStatusConsulting && len(plan.ClarificationQuestions) > 0 {
		if err := plan.SM().TransitionTo(PlanStatusClarifying, plan); err == nil {
			plan.Status = plan.SM().State()
			plan.Epoch = plan.SM().Epoch()
		}
	}
	plan.UpdatedAt = time.Now().UTC()
	a.persistPlanStateBestEffort(plan, "", "plan clarification status refreshed")
}

func dedupeNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func planID(plan *DesignPlan) string {
	if plan == nil {
		return ""
	}
	return strings.TrimSpace(plan.ID)
}

func architectHandoffContext(ctx context.Context) (string, string) {
	if ctx == nil {
		return "", ""
	}
	payload, ok := architectConversationContextFromContext(ctx)
	if !ok {
		return "", ""
	}
	query := strings.TrimSpace(payload.UserQuery)
	history := formatConversationHistory(payload.ConversationHistory)
	return query, strings.TrimSpace(history)
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

func formatClarificationNotification(questions []map[string]any) string {
	if len(questions) == 0 {
		return "I need clarification before I can continue."
	}
	var b strings.Builder
	b.WriteString("I need clarification before I can continue:\n")
	for i, q := range questions {
		text := strings.TrimSpace(fmt.Sprint(q["question"]))
		if text == "" || text == "<nil>" {
			continue
		}
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, text))
	}
	return strings.TrimSpace(b.String())
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
	ResearchSlug        string   `json:"research_slug"`
	PaperPath           string   `json:"paper_path"`
	Version             int      `json:"version,omitempty"`
	Summary             string   `json:"summary,omitempty"`
	RecommendedOptionID string   `json:"recommended_option_id,omitempty"`
	PrototypeSketch     string   `json:"prototype_sketch,omitempty"`
	SystemDesignNotes   []string `json:"system_design_notes,omitempty"`
	SessionID           string   `json:"session_id,omitempty"`
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
		StringParam("recommended_option_id", "Optional preferred architecture option identifier", false).
		StringParam("prototype_sketch", "Optional proof-of-concept sketch from the research paper", false).
		ArrayParam("system_design_notes", "Optional system design implications carried from the research paper", "string", false).
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
	parts := []string{
		fmt.Sprintf("Convert research proposal '%s' into execution plan.", slug),
	}
	if summary != "" {
		parts = append(parts, "Summary: "+summary)
	}
	if optionID := strings.TrimSpace(params.RecommendedOptionID); optionID != "" {
		parts = append(parts, "Recommended option: "+optionID)
	}
	if prototype := strings.TrimSpace(params.PrototypeSketch); prototype != "" {
		parts = append(parts, "Prototype sketch: "+prototype)
	}
	if notes := filterNonEmptyStrings(params.SystemDesignNotes); len(notes) > 0 {
		parts = append(parts, "System design notes: "+strings.Join(notes, "; "))
	}
	return strings.Join(parts, " ")
}

func filterNonEmptyStrings(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// start_planning — transition from conversation to plan generation
// ---------------------------------------------------------------------------

type startPlanningInput struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id,omitempty"`
}

func (a *Architect) reusablePlanForRequest(sessionID, requestCorrelationID string) *DesignPlan {
	if strings.TrimSpace(requestCorrelationID) == "" {
		return nil
	}
	return a.planStore.LatestReusableForRequest(sessionID, requestCorrelationID)
}

func (a *Architect) supersedeDuplicateRequestPlans(sessionID, requestCorrelationID, keepPlanID string) {
	if strings.TrimSpace(requestCorrelationID) == "" {
		return
	}
	for _, candidate := range a.planStore.ActiveDuplicateRequestPlans(sessionID, requestCorrelationID) {
		if candidate == nil || strings.TrimSpace(candidate.ID) == strings.TrimSpace(keepPlanID) {
			continue
		}
		if candidate.SM().State() == PlanStatusSuperseded {
			continue
		}
		if err := candidate.SM().TransitionTo(PlanStatusSuperseded, candidate); err != nil {
			continue
		}
		candidate.Status = candidate.SM().State()
		candidate.Epoch = candidate.SM().Epoch()
		candidate.UpdatedAt = time.Now().UTC()
		candidate.Error = "superseded duplicate request plan"
		a.persistPlanStateBestEffort(candidate, "", "duplicate request plan superseded")
	}
}

func startPlanningSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("start_planning").
		Description("Create a new plan and return the plan_id. "+
			"After receiving the plan_id, drive the protocol by invoking "+
			"plan and consult skills directly with that plan_id.").
		Domain("planning").
		Keywords("start planning", "create plan", "formalize", "generate plan").
		Priority(100).
		TokenEstimate(300).
		StringParam("query", "Synthesized planning query capturing all requirements, constraints, and scope gathered from the conversation", true).
		StringParam("session_id", "Session identifier for plan tracking", false).
		Usage("Invoke to create a new plan once the user discussion and consultation work have produced enough evidence to plan responsibly. Returns plan_id and protocol instructions. Then invoke the planning skills yourself using the returned plan_id: plan(analyze) → consult(pre_planning) → plan(design) → plan(generate_tasks). Do NOT wait — drive the protocol immediately after receiving the plan_id.").
		BestPractice("Synthesize the full conversation context and the consultation evidence already gathered into the query — do not just repeat the user's last message.").
		BestPractice("Do not treat start_planning as the first moment to gather obvious Librarian, Archivalist, or Academic evidence. Enter planning with a strong discussion-time evidence base, then use consult(pre_planning) to consolidate and refresh it.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params startPlanningInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			query := strings.TrimSpace(params.Query)
			if query == "" {
				return nil, fmt.Errorf("query is required")
			}
			sessionID := architectSessionIDFromContext(ctx)
			if sessionID == "" {
				sessionID = normalizeSessionID(params.SessionID)
			}
			requestCorrelationID := originalCIDFromContext(ctx)
			if reusable := a.reusablePlanForRequest(sessionID, requestCorrelationID); reusable != nil {
				a.supersedeDuplicateRequestPlans(sessionID, requestCorrelationID, reusable.ID)
				a.logInfo("start_planning: reusing request-scoped plan",
					"plan_id", reusable.ID,
					"session_id", sessionID,
					"request_correlation_id", requestCorrelationID,
					"status", reusable.SM().State().String())
				return map[string]any{
					"plan_id":    reusable.ID,
					"session_id": sessionID,
					"status":     reusable.SM().State().String(),
					"protocol":   startPlanningProtocolInstructions(a.config.AutoApprove),
					"reused":     true,
				}, nil
			}
			req := &ArchitectRequest{
				ID:        uuid.NewString(),
				Intent:    IntentPlan,
				Query:     query,
				SessionID: sessionID,
				Timestamp: time.Now(),
			}
			req = a.enrichPlanningRequest(req)
			plan := newProtocolPlan(req, requestCorrelationID)
			if err := a.persistPlanState(plan); err != nil {
				return nil, err
			}
			a.supersedeDuplicateRequestPlans(sessionID, requestCorrelationID, plan.ID)
			a.logInfo("start_planning: plan created",
				"plan_id", plan.ID,
				"session_id", sessionID,
				"request_correlation_id", requestCorrelationID,
				"query", truncateString(query, 120))

			shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventPlanCreated,
				a.id, sessionID, "", "info",
				&agentlog.PlanPayload{PlanID: plan.ID, Status: plan.Status.String()})
			return map[string]any{
				"plan_id":    plan.ID,
				"session_id": sessionID,
				"status":     plan.Status.String(),
				"protocol":   startPlanningProtocolInstructions(a.config.AutoApprove),
				"reused":     false,
			}, nil
		}).
		Build()
}

// startPlanningProtocolInstructions returns the protocol field for the
// start_planning skill result. When auto-approve is enabled, the LLM is
// instructed to invoke route_plan_acceptance after generate_tasks. When
// approval is required, the LLM must wait for the user's response.
func startPlanningProtocolInstructions(autoApprove bool) string {
	const base = "Drive the planning protocol using the plan_id above. " +
		"Invoke these skills in order:\n" +
		"1. plan(action=analyze, plan_id=<plan_id>, query=<the query>)\n" +
		"2. consult(mode=pre_planning, plan_id=<plan_id>) — consolidate and refresh the consultation evidence already gathered during discussion.\n" +
		"3. If the request is still broadly vague or underspecified, invoke route_requirements_research and STOP. " +
		"Use ask_user_question only for one or two narrow decisions. If the blocker is codebase or history evidence, " +
		"consult the Librarian or Archivalist instead. If the blocker is architectural quality, correctness, performance, testing, infrastructure, deployment, or tradeoffs, consult the Academic instead of guessing.\n" +
		"4. plan(action=design, plan_id=<plan_id>)\n" +
		"5. plan(action=generate_tasks, plan_id=<plan_id>) — auto-creates workflow and validates.\n" +
		"6. The system renders the plan structure separately in the UI — the user already sees it.\n" +
		"   Do NOT repeat, re-render, or include plan structure, tasks, criteria, or guides in your text.\n" +
		"   Write ONLY a brief assessment — highlight the key tradeoff and risk."

	if autoApprove {
		return base + "\n" +
			"7. Invoke route_plan_acceptance with the plan_id and a brief summary as user_response.\n" +
			"8. After invoking route_plan_acceptance, STOP. The approval and orchestrator handoff continue asynchronously."
	}
	return base + "\n" +
		"   Invite the user to approve or request changes — use natural phrasing, not a template.\n" +
		"   Frame it as plan review, not execution kickoff. Do NOT imply that implementation\n" +
		"   is already starting or that their reply will immediately start work in this turn.\n" +
		"   Avoid phrases like \"kick it off\", \"start building\", \"start implementing\",\n" +
		"   \"get started\", or \"ship it\".\n" +
		"   Do NOT invoke route_plan_acceptance — wait for the user's response."
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
		Description("Queue a ready plan and user response for Guide acceptance evaluation after Guardian approval.").
		Domain("coordination").
		Keywords("plan", "acceptance", "evaluate", "approve", "reject", "feedback").
		Priority(100).
		StringParam("plan_id", "Plan identifier (uses latest ready plan if omitted)", false).
		StringParam("user_response", "The user's verbatim response to the plan", true).
		Usage("Use IMMEDIATELY after the user responds to a presented plan. Packages the plan text, plan ID, plan name, and user response into a structured payload and queues it for the Guide's evaluate-plan-acceptance skill. All four payload fields are derived by the handler — do NOT attempt to construct the evaluation payload manually. This tool returns a pending status and the Architect resumes automatically when the Guide responds.").
		Example(`{"plan_id": "plan_abc", "user_response": "Looks good, but swap the task order for steps 2 and 3."}`).
		BestPractice("Always call this skill for user responses to ready plans — do not classify acceptance yourself.").
		BestPractice("If the result is 'modify', read the modifications list and apply changes via plan action=revise before re-presenting.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			architectDebugLog().Info("route_plan_acceptance: ENTRY",
				"input", truncateString(string(input), 500),
				"ctx_err", ctx.Err())
			params, err := parseRoutePlanAcceptanceParams(input)
			if err != nil {
				architectDebugLog().Warn("route_plan_acceptance: PARSE_ERROR", "error", err.Error())
				return nil, err
			}
			sessionID := architectSessionIDFromContext(ctx)
			architectDebugLog().Info("route_plan_acceptance: RESOLVING_PLAN",
				"plan_id_param", params.PlanID,
				"session_id", sessionID,
				"user_response", truncateString(params.UserResponse, 200))
			plan, err := a.resolveReadyPlanForAcceptance(params.PlanID, sessionID)
			if err != nil {
				architectDebugLog().Warn("route_plan_acceptance: PLAN_RESOLVE_FAILED",
					"error", err.Error(),
					"plan_id_param", params.PlanID,
					"session_id", sessionID)
				return nil, err
			}
			architectDebugLog().Info("route_plan_acceptance: PLAN_RESOLVED",
				"plan_id", plan.ID,
				"plan_status", plan.Status.String(),
				"plan_session", plan.SessionID,
				"task_count", len(plan.Tasks))
			payload := buildPlanAcceptancePayload(plan, params.UserResponse)
			architectDebugLog().Info("route_plan_acceptance: ROUTING_TO_GUIDE",
				"plan_id", plan.ID,
				"payload_plan_len", len(payload.Plan),
				"payload_plan_name", payload.PlanName)
			result, err := a.submitPlanAcceptanceEvaluation(ctx, plan, payload)
			if err != nil {
				architectDebugLog().Warn("route_plan_acceptance: GUIDE_ERROR",
					"error", err.Error(),
					"plan_id", plan.ID)
			} else {
				architectDebugLog().Info("route_plan_acceptance: GUIDE_RESULT",
					"plan_id", plan.ID,
					"result_type", fmt.Sprintf("%T", result))
			}
			return result, err
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
	architectDebugLog().Info("resolveReadyPlanForAcceptance: ENTRY",
		"plan_id_param", planID,
		"session_id", sessionID)
	if id := strings.TrimSpace(planID); id != "" {
		plan, ok := a.GetActivePlan(id)
		if !ok {
			architectDebugLog().Warn("resolveReadyPlanForAcceptance: NOT_FOUND",
				"plan_id", id)
			return nil, fmt.Errorf("plan not found: %s", id)
		}
		if plan.Status != PlanStatusReady {
			architectDebugLog().Warn("resolveReadyPlanForAcceptance: NOT_READY",
				"plan_id", id,
				"status", plan.Status.String())
			return nil, fmt.Errorf("plan %s is not in ready status (current: %s)", id, plan.Status)
		}
		architectDebugLog().Info("resolveReadyPlanForAcceptance: FOUND_BY_ID",
			"plan_id", id,
			"status", plan.Status.String())
		return plan, nil
	}
	plan := a.latestReadyPlan(sessionID)
	if plan == nil {
		architectDebugLog().Warn("resolveReadyPlanForAcceptance: NO_READY_PLAN",
			"session_id", sessionID)
		return nil, fmt.Errorf("no ready plan available for acceptance evaluation")
	}
	architectDebugLog().Info("resolveReadyPlanForAcceptance: FOUND_BY_SESSION",
		"plan_id", plan.ID,
		"session_id", sessionID)
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

// submitPlanAcceptanceEvaluation serializes the payload, records a durable
// continuation, and publishes a Guide-routed evaluation request without
// blocking the Architect loop.
func (a *Architect) submitPlanAcceptanceEvaluation(
	ctx context.Context,
	plan *DesignPlan,
	payload *planAcceptancePayload,
) (map[string]any, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		architectDebugLog().Warn("routePlanAcceptanceToGuide: ENCODE_ERROR", "error", err.Error())
		return nil, fmt.Errorf("failed to encode acceptance payload: %w", err)
	}
	if a == nil || !a.running || a.bus == nil {
		return nil, fmt.Errorf("bus unavailable for acceptance evaluation")
	}
	if plan != nil && plan.PendingWork != nil && plan.PendingWork.Kind == string(continuationKindAcceptanceEval) {
		message := strings.TrimSpace(plan.PendingWork.Message)
		if message == "" {
			message = "I'm already reviewing your response against the current plan and will update you shortly."
		}
		return map[string]any{
			"status":         "acceptance_evaluation_pending",
			"plan_id":        plan.ID,
			"correlation_id": plan.PendingWork.CorrelationID,
			"target_agent":   plan.PendingWork.TargetAgentID,
			"user_message":   message,
		}, nil
	}

	sessionID := plan.SessionID
	correlationID := "accept_" + uuid.NewString()
	userMessage := "I'm reviewing your response against the current plan now. I'll update you shortly."
	record := &ArchitectContinuation{
		ID:                      "cont_" + uuid.NewString(),
		Kind:                    continuationKindAcceptanceEval,
		State:                   continuationStatusPending,
		PlanID:                  plan.ID,
		SessionID:               sessionID,
		TargetAgentID:           "guide",
		ResponseCorrelationID:   correlationID,
		InvocationCorrelationID: originalCIDFromContext(ctx),
		RequestJSON:             string(encoded),
		CreatedAt:               time.Now().UTC(),
		ExpiresAt:               time.Now().UTC().Add(routeSyncTimeout),
	}
	if err := a.recordPendingContinuation(plan, record, userMessage); err != nil {
		return nil, err
	}

	architectDebugLog().Info("routePlanAcceptanceToGuide: SENDING_TO_GUIDE",
		"session_id", sessionID,
		"payload_len", len(encoded),
		"plan_id", payload.PlanID,
		"plan_name", payload.PlanName,
		"user_response", truncateString(payload.UserResponse, 200),
		"ctx_err", ctx.Err(),
		"ctx_deadline", contextDeadlineString(ctx))

	req := &guide.RouteRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: originalCIDFromContext(ctx),
		Input:               "evaluate-plan-acceptance: " + string(encoded),
		TargetAgentID:       "guide",
		SessionID:           sessionID,
	}

	if err := a.publishRouteRequest(req); err != nil {
		architectDebugLog().Warn("routePlanAcceptanceToGuide: PUBLISH_ERROR",
			"error", err.Error(),
			"plan_id", payload.PlanID,
			"session_id", sessionID)
		a.clearPlanPendingContinuationBestEffort(plan, correlationID, "acceptance evaluation publish failed")
		a.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "acceptance evaluation publish failed")
		return nil, fmt.Errorf("guide acceptance evaluation failed: %w", err)
	}
	return map[string]any{
		"status":         "acceptance_evaluation_pending",
		"plan_id":        payload.PlanID,
		"correlation_id": correlationID,
		"target_agent":   "guide",
		"user_message":   userMessage,
	}, nil
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
		architectDebugLog().Warn("extractAcceptanceResult: NIL_MESSAGE")
		return nil, fmt.Errorf("empty response from guide acceptance evaluation")
	}

	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		architectDebugLog().Warn("extractAcceptanceResult: NO_ROUTE_RESPONSE",
			"msg_type", msg.Type)
		return nil, fmt.Errorf("unexpected response type from guide acceptance evaluation")
	}

	architectDebugLog().Info("extractAcceptanceResult: ROUTE_RESPONSE",
		"success", resp.Success,
		"error", resp.Error,
		"data_type", fmt.Sprintf("%T", resp.Data))

	if !resp.Success {
		architectDebugLog().Warn("extractAcceptanceResult: GUIDE_FAILURE",
			"error", resp.Error)
		return nil, fmt.Errorf("guide acceptance evaluation returned error: %s", resp.Error)
	}

	out := &planAcceptanceResult{
		Plan:         payload.Plan,
		PlanID:       payload.PlanID,
		PlanName:     payload.PlanName,
		UserResponse: payload.UserResponse,
	}

	data := acceptanceResultDataMap(resp.Data)
	if data == nil {
		architectDebugLog().Warn("extractAcceptanceResult: DATA_NOT_MAP",
			"data_type", fmt.Sprintf("%T", resp.Data))
		out.Result = inferAcceptanceVerdict(payload.UserResponse, nil)
		return out, nil
	}

	out.Modifications = acceptanceModifications(data)
	out.Result = acceptanceResultString(data)
	if out.Result == "" {
		out.Result = inferAcceptanceVerdict(payload.UserResponse, out.Modifications)
	}
	architectDebugLog().Info("extractAcceptanceResult: EXTRACTED",
		"result", out.Result,
		"modifications_count", len(out.Modifications),
		"plan_id", out.PlanID)
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

func acceptanceResultDataMap(raw any) map[string]any {
	switch value := raw.(type) {
	case nil:
		return nil
	case map[string]any:
		return value
	case string:
		return decodeAcceptanceResultMapString(value)
	case []byte:
		return decodeAcceptanceResultMapJSON(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		return decodeAcceptanceResultMapJSON(encoded)
	}
}

func decodeAcceptanceResultMapString(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if verdict := normalizeAcceptanceVerdict(trimmed); verdict != "" {
		return map[string]any{"result": verdict}
	}
	return decodeAcceptanceResultMapJSON([]byte(trimmed))
}

func decodeAcceptanceResultMapJSON(raw []byte) map[string]any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(trimmed), &data); err != nil {
		return nil
	}
	return data
}

func inferAcceptanceVerdict(userResponse string, modifications []string) string {
	switch {
	case isApprovalSignal(userResponse) && len(modifications) == 0:
		return string(verdictAccept)
	case isRejectSignal(userResponse):
		return string(verdictReject)
	case strings.TrimSpace(userResponse) != "":
		return string(verdictModify)
	default:
		return ""
	}
}

func normalizeAcceptanceVerdict(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case string(verdictAccept):
		return string(verdictAccept)
	case string(verdictModify):
		return string(verdictModify)
	case string(verdictReject):
		return string(verdictReject)
	default:
		return ""
	}
}

func isRejectSignal(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	switch lower {
	case "no", "nope", "nah", "reject", "decline", "cancel":
		return true
	}
	for _, phrase := range rejectionPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

var rejectionPhrases = []string{
	"do not proceed", "don't proceed", "dont proceed",
	"do not do this", "don't do this", "dont do this",
	"not approved", "not acceptable", "bad idea",
	"wrong direction", "scrap this", "drop this plan",
	"hold off", "not yet", "stop this", "cancel this",
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
			architectDebugLog().Info("handlePlanAcceptanceResult: ENTRY",
				"input_len", len(input))
			params, err := parseAcceptanceResultParams(input)
			if err != nil {
				architectDebugLog().Warn("handlePlanAcceptanceResult: PARSE_ERROR",
					"error", err.Error())
				return nil, err
			}
			architectDebugLog().Info("handlePlanAcceptanceResult: PARAMS",
				"plan_id", params.PlanID,
				"result", params.Result,
				"user_response_len", len(params.UserResponse),
				"modifications", len(params.Modifications))
			sessionID := architectSessionIDFromContext(ctx)
			plan, err := a.resolveReadyPlanForAcceptance(params.PlanID, sessionID)
			if err != nil {
				architectDebugLog().Warn("handlePlanAcceptanceResult: PLAN_RESOLVE_FAILED",
					"plan_id", params.PlanID,
					"session_id", sessionID,
					"error", err.Error())
				return nil, err
			}
			verdict := acceptanceVerdict(params.Result)
			architectDebugLog().Info("handlePlanAcceptanceResult: DISPATCHING",
				"plan_id", plan.ID,
				"verdict", string(verdict))
			var result any
			switch verdict {
			case verdictAccept:
				result, err = a.actOnAccept(ctx, plan)
			case verdictModify:
				result, err = a.actOnModify(ctx, plan, params.UserResponse, params.Modifications)
			case verdictReject:
				result, err = a.actOnReject(ctx, plan, params.UserResponse, params.Modifications)
			default:
				return nil, fmt.Errorf("unknown verdict: %q", params.Result)
			}
			if err != nil {
				architectDebugLog().Warn("handlePlanAcceptanceResult: ACTION_ERROR",
					"plan_id", plan.ID,
					"verdict", string(verdict),
					"error", err.Error())
				return nil, err
			}
			architectDebugLog().Info("handlePlanAcceptanceResult: ACTION_COMPLETE",
				"plan_id", plan.ID,
				"verdict", string(verdict))
			return result, nil
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
	a.publishActivityState(events.EventTypeAgentAction, "Plan accepted for execution", events.AgentUIStatePlanAccepted)
	architectDebugLog().Info("actOnAccept: ENTRY",
		"plan_id", plan.ID,
		"plan_status", plan.SM().State().String(),
		"tasks", len(plan.Tasks),
		"session_id", plan.SessionID)

	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentExecute,
		Query:     "user-approved plan execution",
		SessionID: plan.SessionID,
		Timestamp: time.Now(),
	}
	architectDebugLog().Info("actOnAccept: CALLING_DISPATCH",
		"plan_id", plan.ID,
		"request_id", req.ID)
	result, _ := a.dispatchPlanExecution(ctx, req, plan)
	architectDebugLog().Info("actOnAccept: DISPATCH_RETURNED",
		"plan_id", plan.ID,
		"has_result", result != nil)
	return result, nil
}

// actOnModify applies the user's modifications to the plan, then re-routes
// the updated plan + user response back through the Guide for re-approval.
func (a *Architect) actOnModify(
	_ context.Context,
	plan *DesignPlan,
	_ string,
	modifications []string,
) (any, error) {
	a.logInfo("actOnModify: applying modifications",
		"plan_id", plan.ID,
		"modification_count", len(modifications))

	reason := formatModificationReason(modifications)
	a.applyPlanRevision(plan, reason, nil)

	return map[string]any{
		"action":           "modify",
		"plan_id":          plan.ID,
		"revision":         plan.Revision,
		"artifact_version": plan.ArtifactVersion.String(),
		"plan_file":        plan.PlanFile,
		"modifications":    modifications,
		"directive":        "re_approval_requested",
		"awaiting_user":    true,
		"response_to_user": formatModifyResponse(modifications),
	}, nil
}

// actOnReject records the rejection, then re-routes the plan + user response
// back through the Guide so the user can provide clarification or correction.
func (a *Architect) actOnReject(
	_ context.Context,
	plan *DesignPlan,
	_ string,
	modifications []string,
) (any, error) {
	a.logInfo("actOnReject: plan rejected",
		"plan_id", plan.ID,
		"modification_count", len(modifications))
	a.publishActivityState(events.EventTypeAgentError, "Plan rejected", events.AgentUIStatePlanRejected)

	reason := "plan rejected by user"
	if len(modifications) > 0 {
		reason = "plan rejected: " + strings.Join(modifications, "; ")
	}
	a.applyPlanRevision(plan, reason, nil)

	return map[string]any{
		"action":           "reject",
		"plan_id":          plan.ID,
		"revision":         plan.Revision,
		"artifact_version": plan.ArtifactVersion.String(),
		"plan_file":        plan.PlanFile,
		"modifications":    modifications,
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
	b.WriteString("\nWant to proceed with these changes, or would you like further adjustments?")
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
