package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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
	a.skills.Register(preDelegationDeclareSkill(a))
	a.skills.Register(validatePreDelegationSkill(a))
	a.skills.Register(monitorExecutionSkill(a))
	a.skills.Register(interruptHandlerSkill(a))
	a.skills.Register(askUserQuestionSkill(a))
	a.skills.Register(readResearchPaperSkill(a))
	a.skills.Register(readFileSkill(a))
	a.skills.Register(globSkill(a))
	a.skills.Register(grepSkill(a))
	a.skills.Register(gitSkill(a))
	a.skills.Register(lspSkill(a))
	a.skills.Register(astGrepSearchSkill(a))
	a.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   "architect",
		SessionID: func() string { return "" },
		Publish:   a.publishRerouteRequest,
	}))
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
	if err := plan.SM().TransitionTo(targetStatus, plan); err != nil {
		return fmt.Errorf("advancePlan: %w", err)
	}
	plan.Status = plan.SM().State()
	plan.Epoch = plan.SM().Epoch()
	plan.UpdatedAt = time.Now()
	if targetStatus == PlanStatusReady && a.leaseManager != nil {
		a.leaseManager.GrantReadyLease(plan)
	}
	if mutate != nil {
		mutate()
	}
	a.publishPlanStreamProgress(ctx, targetStatus)
	return a.persistPlanState(plan)
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
	type handler = func(context.Context, *planInput) (any, error)
	dispatch := map[string]handler{
		"analyze": func(ctx context.Context, p *planInput) (any, error) {
			if strings.TrimSpace(p.Query) == "" {
				return nil, fmt.Errorf("query is required for action=analyze")
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
				"analysis": map[string]any{
					"goal_count":       len(requirements.Goals),
					"constraint_count": len(requirements.Constraints),
					"scope":            requirements.Scope,
				},
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
		},
		"design": func(ctx context.Context, p *planInput) (any, error) {
			plan, hasPlan := a.resolveProtocolPlan(p.PlanID)
			requirements := p.Requirements
			var codebasePatterns *CodebasePatterns
			if hasPlan {
				// In protocol mode, use the plan's accumulated state.
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
				"summary": map[string]any{
					"component_count": len(architecture.Components),
					"interface_count": len(architecture.Interfaces),
					"pattern_count":   len(architecture.Patterns),
				},
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
		},
		"generate_tasks": func(ctx context.Context, p *planInput) (any, error) {
			plan, hasPlan := a.resolveProtocolPlan(p.PlanID)
			architecture := p.Architecture
			constraints := &PlanConstraints{
				MaxTasksPerAgent: p.MaxTasksPerAgent,
				AllowParallel:    p.AllowParallel,
			}
			if hasPlan {
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
			totalTokens := 0
			complexityCounts := map[string]int{}
			for _, task := range tasks {
				totalTokens += task.EstimatedTokens
				complexityCounts[task.Complexity.String()]++
			}
			result := map[string]any{
				"tasks": tasks,
				"summary": map[string]any{
					"task_count":        len(tasks),
					"total_tokens":      totalTokens,
					"complexity_counts": complexityCounts,
				},
			}
			if hasPlan {
				// Generating → attach tasks
				if err := a.advancePlan(ctx, plan, PlanStatusGenerating, func() {
					plan.Tasks = tasks
				}); err != nil {
					return nil, err
				}
				// Auto-chain: workflow DAG
				workflow, wErr := a.createWorkflowDAG(ctx, plan.Tasks)
				if wErr != nil {
					return nil, wErr
				}
				if err := a.advancePlan(ctx, plan, PlanStatusOrchestrating, func() {
					plan.Workflow = workflow
				}); err != nil {
					return nil, err
				}
				// Auto-chain: validate + declaration
				if vErr := validatePlanForExecution(plan); vErr != nil {
					return nil, vErr
				}
				declaration := buildAutoDeclaration(plan)
				if dErr := a.validateDeclaration(declaration); dErr != nil {
					plan.RiskSummary = append(plan.RiskSummary, "declaration validation warning: "+dErr.Error())
				}
				plan.Declarations = append(plan.Declarations, declaration)
				a.publishDeclaration(declaration, plan.SessionID)
				// Transition to Ready
				if err := a.advancePlan(ctx, plan, PlanStatusReady, nil); err != nil {
					return nil, err
				}
				// Grant ready lease
				if a.leaseManager != nil {
					a.leaseManager.GrantReadyLease(plan)
				}
				a.publishPlanSnapshot(ctx, plan)
				layers := planLayerCount(plan)
				result["plan_status"] = plan.SM().State().String()
				result["layer_count"] = layers
				result["task_summary"] = firstTaskName(plan)
				result["next_action"] = generateTasksNextAction(a.config.AutoApprove)
			}
			return result, nil
		},
		"estimate": func(_ context.Context, p *planInput) (any, error) {
			if strings.TrimSpace(p.Description) == "" {
				return nil, fmt.Errorf("description is required for action=estimate")
			}
			estimate := estimateTaskComplexity(p.Description, p.Context)
			return map[string]any{"estimate": estimate}, nil
		},
		"revise": func(_ context.Context, p *planInput) (any, error) {
			if strings.TrimSpace(p.Reason) == "" {
				return nil, fmt.Errorf("reason is required for action=revise")
			}
			plan, err := a.selectPlan(p.PlanID)
			if err != nil {
				return nil, err
			}
			updated := a.applyPlanRevision(plan, p.Reason, p.Updates)
			return map[string]any{
				"plan_id":  updated.ID,
				"revision": updated.Revision,
				"status":   updated.Status.String(),
				"updated":  updated.UpdatedAt,
			}, nil
		},
	}

	return skills.NewSkill("plan").
		Description("Plan operations for analyzing requirements, designing architecture, generating tasks, estimating complexity, and revising plans.\n\n"+
			"Actions:\n"+
			"- analyze: Analyze project requirements (params: query [required], scope, goals, constraints)\n"+
			"- design: Design system architecture (params: requirements [required], patterns)\n"+
			"- generate_tasks: Generate atomic tasks from architecture (params: architecture [required], max_tasks_per_agent, allow_parallel)\n"+
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
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params planInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown plan action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
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
		Usage("Use during plan formulation to organize atomic tasks into a dependency graph. This is a planning-phase tool that builds data structures — it does NOT submit the plan to the Orchestrator or trigger execution. To execute a plan after user approval, use route_plan_acceptance followed by handle_plan_acceptance_result.").
		BestPractice("NEVER use this skill as a substitute for plan execution. Execution requires: route_plan_acceptance → Guide verdict → handle_plan_acceptance_result.").
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
			"and a brief summary as user_response. When it returns the Guide's " +
			"verdict, invoke handle_plan_acceptance_result with the verdict details. " +
			"On accept, the plan dispatches to the orchestrator automatically."
	}
	return base + " Then invite the user to approve or request changes — " +
		"use your own natural phrasing, not a scripted template. " +
		"Do NOT invoke route_plan_acceptance — wait for the user's response."
}
