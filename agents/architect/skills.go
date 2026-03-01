package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		SourceAgentID:   "architect",
		SuggestedTarget: suggestedTarget,
		ExcludeAgents:   []string{"architect"},
	}
	return a.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
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
			return map[string]any{
				"requirements": requirements,
				"analysis": map[string]any{
					"goal_count":       len(requirements.Goals),
					"constraint_count": len(requirements.Constraints),
					"scope":            requirements.Scope,
				},
			}, nil
		},
		"design": func(ctx context.Context, p *planInput) (any, error) {
			if p.Requirements == nil {
				return nil, fmt.Errorf("requirements is required for action=design")
			}
			var codebasePatterns *CodebasePatterns
			if len(p.Patterns) > 0 {
				codebasePatterns = &CodebasePatterns{
					Patterns: make([]PatternInfo, len(p.Patterns)),
				}
				for i, pat := range p.Patterns {
					codebasePatterns.Patterns[i] = PatternInfo{Name: pat}
				}
			}
			architecture, err := a.designArchitecture(ctx, p.Requirements, codebasePatterns)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"architecture": architecture,
				"summary": map[string]any{
					"component_count": len(architecture.Components),
					"interface_count": len(architecture.Interfaces),
					"pattern_count":   len(architecture.Patterns),
				},
			}, nil
		},
		"generate_tasks": func(ctx context.Context, p *planInput) (any, error) {
			if p.Architecture == nil {
				return nil, fmt.Errorf("architecture is required for action=generate_tasks")
			}
			constraints := &PlanConstraints{
				MaxTasksPerAgent: p.MaxTasksPerAgent,
				AllowParallel:    p.AllowParallel,
			}
			if constraints.MaxTasksPerAgent == 0 {
				constraints.MaxTasksPerAgent = 5
			}
			tasks, err := a.generateAtomicTasks(ctx, p.Architecture, constraints)
			if err != nil {
				return nil, err
			}
			totalTokens := 0
			complexityCounts := map[string]int{}
			for _, task := range tasks {
				totalTokens += task.EstimatedTokens
				complexityCounts[task.Complexity.String()]++
			}
			return map[string]any{
				"tasks": tasks,
				"summary": map[string]any{
					"task_count":        len(tasks),
					"total_tokens":      totalTokens,
					"complexity_counts": complexityCounts,
				},
			}, nil
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
			"components":  {Type: "array", Description: "Component specifications"},
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
		StringParam("plan_id", "Plan identifier to revise (for revise)", false).
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
