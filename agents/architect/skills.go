package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

func (a *Architect) registerCoreSkills() {
	a.skills.Register(planSkill(a))
	a.skills.Register(planWorkflowSkill(a))
	a.skills.Register(startPlanningSkill(a))
	a.skills.Register(consultSkill(a))
	a.skills.Register(planModeSkill(a))
	a.skills.Register(routePlanAcceptanceSkill(a))
	a.skills.Register(handlePlanAcceptanceResultSkill(a))
	a.skills.Register(presentPlanApprovalDialogSkill(a))
	a.skills.Register(preDelegationDeclareSkill(a))
	a.skills.Register(validatePreDelegationSkill(a))
	a.skills.Register(monitorExecutionSkill(a))
	a.skills.Register(interruptHandlerSkill(a))
	a.skills.Register(askUserQuestionSkill(a))
	a.skills.Register(routeRequirementsResearchSkill(a))
	a.skills.Register(readResearchPaperSkill(a))
	for _, skill := range shared.NewGlobalReviewProtocolSkills(shared.GlobalReviewProtocolSkillConfig{
		AgentType:     func() string { return "architect" },
		AgentID:       func() string { return a.id },
		ResolveTarget: func(agentType string) string { return a.knownAgentIDByType(agentType, agentType) },
		Route: shared.GlobalReviewRouteConfig{
			BusProvider: func() guide.EventBus { return a.bus },
		},
	}) {
		a.skills.Register(skill)
	}
	a.skills.Register(shared.NewSelfDiagnosticSkill(&architectDiag{a: a}))
	a.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   "architect",
		SessionID: func() string { return "" },
		Publish:   a.publishRerouteRequest,
	}))
}

// registerFabricSkills wires the uniform Activity Fabric awareness +
// recall skills onto the architect's registry. These skills must be
// available so the architect can orient itself against peer activity
// before issuing handoffs, recall its own prior planning decisions,
// and trace causal lineage of contested decisions.
func (a *Architect) registerFabricSkills() {
	sessionID := func() string { return strings.TrimSpace(a.config.SessionID) }
	agentID := func() string { return a.id }
	agentType := func() string { return "architect" }

	for _, skill := range fabric.AwarenessSkills(fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      sessionID,
		AgentID:        agentID,
		AgentType:      agentType,
	}) {
		a.skills.Register(skill)
	}
	for _, skill := range fabric.RecallSkills(fabric.RecallSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      sessionID,
		AgentID:        agentID,
		AgentType:      agentType,
	}) {
		a.skills.Register(skill)
	}
	for _, skill := range shared.CrossPipelineSkills(shared.CrossPipelineSkillConfig{
		SessionID:  sessionID,
		AgentID:    agentID,
		AgentType:  agentType,
		PipelineID: func() string { return "" },
	}) {
		a.skills.Register(skill)
	}
}

type architectDiag struct{ a *Architect }

func (d *architectDiag) AgentName() string { return "architect" }
func (d *architectDiag) SessionID() string { return "" }
func (d *architectDiag) LogsDir() string {
	return shared.LogsDirForAgent(d.a.steering.SessionDir(), "architect")
}
func (d *architectDiag) EventLogger() *agentlog.SessionEventLogger { return d.a.steering.EventLogger() }
func (d *architectDiag) PeerLogsDirs() map[string]string           { return nil }
func (d *architectDiag) RecoveryHints() []string                   { return nil }

func (d *architectDiag) AgentSpecificDiagnostics() map[string]any {
	plans := d.a.planStore.Snapshot()
	states := make(map[string]string, len(plans))
	for _, p := range plans {
		states[p.ID] = p.Status.String()
	}
	return map[string]any{
		"active_plan_count": len(plans),
		"plan_states":       states,
	}
}

func (a *Architect) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if a.bus == nil {
		return fmt.Errorf("architect bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   a.id,
		SuggestedTarget: suggestedTarget,
		ExcludeAgents:   []string{"architect"},
	}
	return a.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
}

// ---------------------------------------------------------------------------
// Plan-aware skill helpers
// ---------------------------------------------------------------------------

// resolveProtocolPlan looks up an active plan by ID. Returns nil, false
// when planID is empty or not found — callers should proceed without
// plan attachment in that case (backward compatibility).
func (a *Architect) resolveProtocolPlan(planID string) (*DesignPlan, bool) {
	if strings.TrimSpace(planID) == "" {
		return nil, false
	}
	return a.GetActivePlan(planID)
}

// advancePlan validates the state machine transition, runs the mutate
// function to attach results, transitions state, publishes progress,
// and persists. This is the single entry point for plan state changes
// from skill handlers.
func (a *Architect) advancePlan(
	ctx context.Context,
	plan *DesignPlan,
	targetStatus PlanStatus,
	mutate func(),
) error {
	if plan == nil {
		return fmt.Errorf("advancePlan: plan is nil")
	}
	currentStatus := plan.SM().State()
	if currentStatus != targetStatus {
		if err := plan.SM().TransitionTo(targetStatus, plan); err != nil {
			return fmt.Errorf("advancePlan: %w", err)
		}
	}
	plan.Status = plan.SM().State()
	plan.Epoch = plan.SM().Epoch()
	plan.UpdatedAt = time.Now()
	if targetStatus == PlanStatusReady {
		if lm := a.planStore.LeaseManager(); lm != nil {
			lm.GrantReadyLease(plan)
		}
	}
	if mutate != nil {
		mutate()
	}
	a.publishPlanStreamProgress(ctx, targetStatus)
	return a.persistPlanState(plan)
}

func planPhaseRank(status PlanStatus) int {
	switch status {
	case PlanStatusPending:
		return 0
	case PlanStatusAnalyzing:
		return 1
	case PlanStatusConsulting, PlanStatusClarifying:
		return 2
	case PlanStatusDesigning:
		return 3
	case PlanStatusGenerating:
		return 4
	case PlanStatusOrchestrating:
		return 5
	case PlanStatusReady, PlanStatusExecuting, PlanStatusCompleted:
		return 6
	default:
		return -1
	}
}

func hasReachedPlanPhase(current, target PlanStatus) bool {
	return planPhaseRank(current) >= planPhaseRank(target)
}

// ---------------------------------------------------------------------------
// plan (consolidated: analyze, design, generate_tasks, estimate, revise)
// ---------------------------------------------------------------------------

type planInput struct {
	Action           string                `json:"action"`
	Query            string                `json:"query,omitempty"`
	Scope            string                `json:"scope,omitempty"`
	Goals            []string              `json:"goals,omitempty"`
	Constraints      []string              `json:"constraints,omitempty"`
	Requirements     *Requirements         `json:"requirements,omitempty"`
	Patterns         []string              `json:"patterns,omitempty"`
	Architecture     *SolutionArchitecture `json:"architecture,omitempty"`
	MaxTasksPerAgent int                   `json:"max_tasks_per_agent,omitempty"`
	AllowParallel    bool                  `json:"allow_parallel,omitempty"`
	Description      string                `json:"description,omitempty"`
	Context          map[string]any        `json:"context,omitempty"`
	PlanID           string                `json:"plan_id,omitempty"`
	Reason           string                `json:"reason,omitempty"`
	Updates          map[string]any        `json:"updates,omitempty"`
}

func planSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("plan").
		Description("Plan operations for synthesizing discussion and consultation evidence into requirements, architecture, tasks, estimates, and revisions.\n\n"+
			"Actions:\n"+
			"- analyze: Synthesize project requirements from the user discussion, existing evidence, and known constraints (params: query [required], scope, goals, constraints)\n"+
			"- design: Synthesize system architecture from requirements and consultation evidence (params: requirements [required], patterns)\n"+
			"- generate_tasks: Synthesize atomic tasks from architecture (params: architecture [required], max_tasks_per_agent, allow_parallel)\n"+
			"- estimate: Estimate task complexity and token usage (params: description [required], context)\n"+
			"- revise: Revise an existing plan (params: plan_id, reason [required], updates)").
		Domain("planning").
		Keywords("analyze", "requirements", "understand", "goals", "constraints",
			"design", "architecture", "system", "components", "structure",
			"generate", "tasks", "atomic", "decompose", "breakdown",
			"estimate", "complexity", "tokens", "effort", "size",
			"revise", "replan", "update plan", "change workflow").
		Priority(100).
		TokenEstimate(600).
		EnumParam("action", "Planning action to execute", []string{
			"analyze", "design", "generate_tasks", "estimate", "revise",
		}, true).
		StringParam("query", "Requirement or task to analyze (for analyze)", false).
		StringParam("scope", "Scope of analysis (for analyze)", false).
		ArrayParam("goals", "Explicit goals (for analyze)", "string", false).
		ArrayParam("constraints", "Known constraints (for analyze)", "string", false).
		ObjectParam("requirements", "Requirements to design for (for design)", map[string]*skills.Property{
			"query": {Type: "string", Description: "Main requirement query"},
			"goals": {Type: "array", Items: &skills.Property{Type: "string"}, Description: "Goals to achieve"},
			"scope": {Type: "string", Description: "Scope of the design"},
		}, false).
		ArrayParam("patterns", "Existing patterns to incorporate (for design)", "string", false).
		ObjectParam("architecture", "Architecture to generate tasks from (for generate_tasks)", map[string]*skills.Property{
			"name":        {Type: "string", Description: "Architecture name"},
			"description": {Type: "string", Description: "Architecture description"},
			"components": {
				Type:        "array",
				Description: "Component specifications",
				Items: &skills.Property{
					Type: "object",
					Properties: map[string]*skills.Property{
						"name":        {Type: "string", Description: "Component name"},
						"type":        {Type: "string", Description: "Component type"},
						"description": {Type: "string", Description: "Component description"},
					},
				},
			},
		}, false).
		IntParam("max_tasks_per_agent", "Maximum tasks per agent (for generate_tasks, default 5)", false).
		BoolParam("allow_parallel", "Allow parallel execution (for generate_tasks, default true)", false).
		StringParam("description", "Task description to estimate (for estimate)", false).
		ObjectParam("context", "Additional context for estimation (for estimate)", map[string]*skills.Property{
			"has_dependencies":  {Type: "boolean", Description: "Whether task has dependencies"},
			"dependency_count":  {Type: "integer", Description: "Number of dependencies"},
			"scope":             {Type: "string", Description: "Task scope"},
			"involves_tests":    {Type: "boolean", Description: "Whether task includes testing"},
			"involves_refactor": {Type: "boolean", Description: "Whether task involves refactoring"},
		}, false).
		StringParam("plan_id", "Plan identifier for protocol-driven operations (analyze, design, generate_tasks, revise)", false).
		StringParam("reason", "Reason for revision (for revise)", false).
		ObjectParam("updates", "Optional update payload (for revise)", map[string]*skills.Property{}, false).
		Usage("Use this skill to formalize and synthesize what the architect has already learned from the user discussion and consultations. The goal is a high-quality planning artifact, not a replacement for live discovery. If material evidence is still missing, perform the targeted consultation first or immediately after the relevant planning step instead of guessing.").
		BestPractice("For action=analyze, synthesize the whole conversation and consultation evidence into the query and inputs — do not treat analyze as the first moment to discover obvious requirements.").
		BestPractice("For action=design and action=generate_tasks, preserve the current output shape while grounding the result in the strongest evidence already gathered. Additional targeted research is still allowed when a real gap remains.").
		BestPractice("For action=generate_tasks, prefer short markdown-ready examples when they materially reduce ambiguity. Use example languages like sh, json, go, ts, or mermaid, and do not include surrounding triple backticks in the example code field.").
		BestPractice("Use compact mermaid flow or sequence diagrams when task sequencing, ownership, or data flow is easier to understand visually than in prose.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params planInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := planSkillHandlers[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown plan action: %q", params.Action)
			}
			return fn(a, ctx, &params)
		}).
		Build()
}

type planSkillHandler func(*Architect, context.Context, *planInput) (any, error)

var planSkillHandlers = map[string]planSkillHandler{
	"analyze":        handlePlanSkillAnalyze,
	"design":         handlePlanSkillDesign,
	"generate_tasks": handlePlanSkillGenerateTasks,
	"estimate":       handlePlanSkillEstimate,
	"revise":         handlePlanSkillRevise,
}

func handlePlanSkillAnalyze(a *Architect, ctx context.Context, p *planInput) (any, error) {
	if strings.TrimSpace(p.Query) == "" {
		return nil, fmt.Errorf("query is required for action=analyze")
	}
	if plan, ok := a.resolveProtocolPlan(p.PlanID); ok &&
		hasReachedPlanPhase(plan.SM().State(), PlanStatusAnalyzing) &&
		plan.Requirements != nil {
		return map[string]any{
			"requirements": plan.Requirements,
			"analysis":     requirementsAnalysisSummary(plan.Requirements),
			"plan_status":  plan.SM().State().String(),
			"reused":       true,
		}, nil
	}
	reqParams := map[string]any{}
	if p.Scope != "" {
		reqParams["scope"] = p.Scope
	}
	if len(p.Goals) > 0 {
		reqParams["goals"] = p.Goals
	}
	if len(p.Constraints) > 0 {
		reqParams["constraints"] = p.Constraints
	}
	requirements, err := a.analyzeRequirements(ctx, p.Query, reqParams)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"requirements": requirements,
		"analysis":     requirementsAnalysisSummary(requirements),
	}
	if plan, ok := a.resolveProtocolPlan(p.PlanID); ok {
		if err := a.advancePlan(ctx, plan, PlanStatusAnalyzing, func() {
			plan.Requirements = requirements
		}); err != nil {
			return nil, err
		}
		result["plan_status"] = plan.SM().State().String()
	}
	return result, nil
}

func handlePlanSkillDesign(a *Architect, ctx context.Context, p *planInput) (any, error) {
	plan, hasPlan := a.resolveProtocolPlan(p.PlanID)
	requirements := p.Requirements
	var codebasePatterns *CodebasePatterns
	if hasPlan {
		if hasReachedPlanPhase(plan.SM().State(), PlanStatusDesigning) &&
			plan.Architecture != nil {
			return map[string]any{
				"architecture": plan.Architecture,
				"summary":      architectureSummary(plan.Architecture),
				"plan_status":  plan.SM().State().String(),
				"reused":       true,
			}, nil
		}
		requirements = plan.Requirements
		codebasePatterns = plan.CodebasePatterns
	}
	if requirements == nil {
		return nil, fmt.Errorf("requirements is required for action=design")
	}
	if codebasePatterns == nil && len(p.Patterns) > 0 {
		codebasePatterns = &CodebasePatterns{
			Patterns: make([]PatternInfo, len(p.Patterns)),
		}
		for i, pat := range p.Patterns {
			codebasePatterns.Patterns[i] = PatternInfo{Name: pat}
		}
	}
	architecture, err := a.designArchitecture(ctx, requirements, codebasePatterns)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"architecture": architecture,
		"summary":      architectureSummary(architecture),
	}
	if hasPlan {
		if err := a.advancePlan(ctx, plan, PlanStatusDesigning, func() {
			plan.Architecture = architecture
		}); err != nil {
			return nil, err
		}
		result["plan_status"] = plan.SM().State().String()
	}
	return result, nil
}

func handlePlanSkillGenerateTasks(a *Architect, ctx context.Context, p *planInput) (any, error) {
	plan, hasPlan := a.resolveProtocolPlan(p.PlanID)
	architecture := p.Architecture
	constraints := &PlanConstraints{
		MaxTasksPerAgent: p.MaxTasksPerAgent,
		AllowParallel:    p.AllowParallel,
	}
	if hasPlan {
		if hasReachedPlanPhase(plan.SM().State(), PlanStatusGenerating) &&
			len(plan.Tasks) > 0 && plan.Workflow != nil {
			return map[string]any{
				"tasks":        plan.Tasks,
				"summary":      taskGenerationSummary(plan.Tasks),
				"plan_status":  plan.SM().State().String(),
				"layer_count":  planLayerCount(plan),
				"task_summary": firstTaskName(plan),
				"next_action":  generateTasksNextAction(a.config.AutoApprove),
				"reused":       true,
			}, nil
		}
		architecture = plan.Architecture
		if plan.Constraints != nil {
			constraints = plan.Constraints
		}
	}
	if architecture == nil {
		return nil, fmt.Errorf("architecture is required for action=generate_tasks")
	}
	if constraints.MaxTasksPerAgent == 0 {
		constraints.MaxTasksPerAgent = 5
	}
	tasks, err := a.generateAtomicTasks(ctx, architecture, constraints)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"tasks":   tasks,
		"summary": taskGenerationSummary(tasks),
	}
	if !hasPlan {
		return result, nil
	}
	if err := a.advancePlan(ctx, plan, PlanStatusGenerating, func() {
		plan.Tasks = tasks
	}); err != nil {
		return nil, err
	}
	workflow, wErr := a.createWorkflowDAG(ctx, plan.Tasks)
	if wErr != nil {
		return nil, wErr
	}
	if err := a.advancePlan(ctx, plan, PlanStatusOrchestrating, func() {
		plan.Workflow = workflow
	}); err != nil {
		return nil, err
	}
	if vErr := validatePlanForExecution(plan); vErr != nil {
		return nil, vErr
	}
	declaration := buildAutoDeclaration(plan)
	if dErr := a.validateDeclaration(declaration); dErr != nil {
		plan.RiskSummary = append(plan.RiskSummary, "declaration validation warning: "+dErr.Error())
	}
	plan.Declarations = append(plan.Declarations, declaration)
	a.publishDeclaration(ctx, declaration, plan.SessionID)
	if err := a.advancePlan(ctx, plan, PlanStatusReady, nil); err != nil {
		return nil, err
	}
	if lm := a.planStore.LeaseManager(); lm != nil {
		lm.GrantReadyLease(plan)
	}
	a.publishPlanSnapshot(ctx, plan)
	result["plan_status"] = plan.SM().State().String()
	result["layer_count"] = planLayerCount(plan)
	result["task_summary"] = firstTaskName(plan)
	result["next_action"] = generateTasksNextAction(a.config.AutoApprove)
	return result, nil
}

func handlePlanSkillEstimate(_ *Architect, _ context.Context, p *planInput) (any, error) {
	if strings.TrimSpace(p.Description) == "" {
		return nil, fmt.Errorf("description is required for action=estimate")
	}
	estimate := estimateTaskComplexity(p.Description, p.Context)
	return map[string]any{"estimate": estimate}, nil
}

func handlePlanSkillRevise(a *Architect, _ context.Context, p *planInput) (any, error) {
	if strings.TrimSpace(p.Reason) == "" {
		return nil, fmt.Errorf("reason is required for action=revise")
	}
	plan, err := a.selectPlan(p.PlanID)
	if err != nil {
		return nil, err
	}
	updated := a.applyPlanRevision(plan, p.Reason, p.Updates)
	return map[string]any{
		"plan_id":          updated.ID,
		"revision":         updated.Revision,
		"artifact_version": updated.ArtifactVersion.String(),
		"plan_file":        updated.PlanFile,
		"status":           updated.Status.String(),
		"updated":          updated.UpdatedAt,
	}, nil
}

func requirementsAnalysisSummary(requirements *Requirements) map[string]any {
	return map[string]any{
		"goal_count":       len(requirements.Goals),
		"constraint_count": len(requirements.Constraints),
		"scope":            requirements.Scope,
	}
}

func architectureSummary(architecture *SolutionArchitecture) map[string]any {
	return map[string]any{
		"component_count": len(architecture.Components),
		"interface_count": len(architecture.Interfaces),
		"pattern_count":   len(architecture.Patterns),
	}
}

func taskGenerationSummary(tasks []*AtomicTask) map[string]any {
	totalTokens := 0
	complexityCounts := map[string]int{}
	for _, task := range tasks {
		totalTokens += task.EstimatedTokens
		complexityCounts[task.Complexity.String()]++
	}
	return map[string]any{
		"task_count":        len(tasks),
		"total_tokens":      totalTokens,
		"complexity_counts": complexityCounts,
	}
}

// ---------------------------------------------------------------------------
// plan_workflow (consolidated: standard, fix)
// ---------------------------------------------------------------------------

type planWorkflowInput struct {
	Type           string        `json:"type"`
	Tasks          []*AtomicTask `json:"tasks,omitempty"`
	Policy         string        `json:"policy,omitempty"`
	MaxConcurrency int           `json:"max_concurrency,omitempty"`
	PlanID         string        `json:"plan_id,omitempty"`
	SessionID      string        `json:"session_id,omitempty"`
	Corrections    []any         `json:"corrections,omitempty"`
}

func planWorkflowSkill(a *Architect) *skills.Skill {
	type handler = func(context.Context, *planWorkflowInput) (any, error)
	dispatch := map[string]handler{
		"standard": func(ctx context.Context, p *planWorkflowInput) (any, error) {
			if len(p.Tasks) == 0 {
				return nil, fmt.Errorf("tasks are required for type=standard")
			}
			workflow, err := a.createWorkflowDAG(ctx, p.Tasks)
			if err != nil {
				return nil, err
			}
			executionOrder := [][]string{}
			if workflow.DAG != nil {
				executionOrder = workflow.DAG.ExecutionOrder()
			}
			return map[string]any{
				"workflow": map[string]any{
					"dag_id":           workflow.DAG.ID(),
					"total_tasks":      workflow.TotalTasks,
					"estimated_tokens": workflow.EstimatedTokens,
					"execution_order":  executionOrder,
					"layer_count":      len(executionOrder),
				},
			}, nil
		},
		"fix": func(ctx context.Context, p *planWorkflowInput) (any, error) {
			if len(p.Corrections) == 0 {
				return nil, fmt.Errorf("corrections are required for type=fix")
			}
			tasks := buildFixTasks(p.Corrections)
			workflow, err := a.createWorkflowDAG(ctx, tasks)
			if err != nil {
				return nil, err
			}
			linkedPlanID := a.attachFixWorkflow(p.PlanID, workflow, tasks)
			return map[string]any{
				"plan_id":      linkedPlanID,
				"session_id":   normalizeSessionID(p.SessionID),
				"workflow":     workflow,
				"task_count":   len(tasks),
				"corrections":  len(p.Corrections),
				"workflow_tag": "fix",
			}, nil
		},
	}

	return skills.NewSkill("plan_workflow").
		Description("Build workflow DAG structures from tasks or corrections. Does NOT dispatch or execute the plan.\n\n"+
			"Types:\n"+
			"- standard: Build DAG from atomic tasks (params: tasks [required], policy, max_concurrency)\n"+
			"- fix: Build remediation DAG from corrections (params: corrections [required], plan_id, session_id)").
		Domain("planning").
		Keywords("workflow", "dag", "dependency graph", "task order", "plan structure",
			"fix dag", "corrections", "repair structure", "remediation tasks").
		Priority(85).
		TokenEstimate(350).
		EnumParam("type", "Workflow type to build", []string{"standard", "fix"}, true).
		ArrayParam("tasks", "Atomic tasks for standard workflow", "object", false).
		EnumParam("policy", "Execution policy (for standard)", []string{"fail_fast", "continue"}, false).
		IntParam("max_concurrency", "Maximum concurrent tasks (for standard, default 10)", false).
		StringParam("plan_id", "Plan identifier to attach fix DAG to (for fix)", false).
		StringParam("session_id", "Session identifier (for fix)", false).
		ArrayParam("corrections", "Correction list from inspector/tester feedback (for fix)", "object", false).
		Usage("Use during plan formulation to organize atomic tasks into a dependency graph. This is a planning-phase tool that builds data structures — it does NOT submit the plan to the Orchestrator or trigger execution. To execute a plan after user approval, use route_plan_acceptance and let the Architect resume asynchronously when approval completes.").
		BestPractice("NEVER use this skill as a substitute for plan execution. Execution requires route_plan_acceptance and an asynchronous handoff to the orchestrator.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params planWorkflowInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Type]
			if !ok {
				return nil, fmt.Errorf("unknown plan_workflow type: %q", params.Type)
			}
			return fn(ctx, &params)
		}).
		Build()
}

// ---------------------------------------------------------------------------
// estimateTaskComplexity — shared helper used by planSkill action=estimate
// ---------------------------------------------------------------------------

func estimateTaskComplexity(description string, context map[string]any) *ComplexityEstimate {
	baseTokens := 2000
	complexity := ComplexityLow
	factors := []ComplexityFactor{}

	descLen := len(description)
	if descLen > 200 {
		baseTokens += 2000
		complexity = ComplexityMedium
		factors = append(factors, ComplexityFactor{
			Name:        "description_length",
			Impact:      "medium",
			Description: "Long description suggests complex task",
		})
	}

	if context != nil {
		if hasDeps, ok := context["has_dependencies"].(bool); ok && hasDeps {
			baseTokens += 1000
			factors = append(factors, ComplexityFactor{
				Name:        "has_dependencies",
				Impact:      "low",
				Description: "Task depends on other work",
			})
		}

		if depCount, ok := context["dependency_count"].(float64); ok && depCount > 2 {
			baseTokens += int(depCount) * 500
			complexity = ComplexityHigh
			factors = append(factors, ComplexityFactor{
				Name:        "dependency_count",
				Impact:      "high",
				Description: fmt.Sprintf("Has %d dependencies", int(depCount)),
			})
		}

		if involvesTests, ok := context["involves_tests"].(bool); ok && involvesTests {
			baseTokens += 2000
			factors = append(factors, ComplexityFactor{
				Name:        "involves_tests",
				Impact:      "medium",
				Description: "Task includes test writing",
			})
		}

		if involvesRefactor, ok := context["involves_refactor"].(bool); ok && involvesRefactor {
			baseTokens += 3000
			if complexity < ComplexityHigh {
				complexity = ComplexityHigh
			}
			factors = append(factors, ComplexityFactor{
				Name:        "involves_refactor",
				Impact:      "high",
				Description: "Task involves refactoring",
			})
		}
	}

	durationMinutes := baseTokens / 200

	riskLevel := "low"
	if complexity >= ComplexityHigh {
		riskLevel = "high"
	} else if complexity >= ComplexityMedium {
		riskLevel = "medium"
	}

	return &ComplexityEstimate{
		Overall:         complexity,
		TokenEstimate:   baseTokens,
		DurationMinutes: durationMinutes,
		RiskLevel:       riskLevel,
		Factors:         factors,
	}
}

// generateTasksNextAction returns the LLM instruction for what to do after
// generate_tasks completes and the plan reaches Ready. When auto-approve is
// enabled, the LLM must invoke route_plan_acceptance immediately. When
// approval is required, it must wait for the user's response.
func generateTasksNextAction(autoApprove bool) string {
	const base = "PROTOCOL COMPLETE. The plan is ready. The system renders " +
		"the plan structure separately in the UI — the user already sees it. " +
		"Do NOT repeat, re-render, or include the plan structure, task list, " +
		"acceptance criteria, file lists, or implementation guides in your text. " +
		"Write ONLY a brief assessment (2-4 sentences): highlight the key " +
		"architectural tradeoff and the primary risk. Sound like a principal engineer."

	if autoApprove {
		return base + " Then invoke route_plan_acceptance with the plan_id " +
			"and a brief summary as user_response. After invoking it, STOP. " +
			"The approval and orchestrator handoff continue asynchronously."
	}
	return base + " Then invite the user to approve or request changes — " +
		"use your own natural phrasing, not a scripted template. " +
		"Frame it as plan review, not execution kickoff. Do NOT imply that " +
		"implementation is already starting or that their reply will immediately " +
		"start work in this turn. Avoid phrases like \"kick it off\", " +
		"\"start building\", \"start implementing\", \"get started\", or \"ship it\". " +
		"Do NOT invoke route_plan_acceptance — wait for the user's response."
}
