package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

func (a *Architect) registerCoreSkills() {
	a.skills.Register(analyzeRequirementsSkill(a))
	a.skills.Register(designArchitectureSkill(a))
	a.skills.Register(generateTasksSkill(a))
	a.skills.Register(createWorkflowDAGSkill(a))
	a.skills.Register(estimateComplexitySkill(a))
	a.skills.Register(consultKnowledgeSkill(a))
}

type analyzeRequirementsParams struct {
	Query       string   `json:"query"`
	Scope       string   `json:"scope,omitempty"`
	Goals       []string `json:"goals,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
}

func analyzeRequirementsSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("analyze_requirements").
		Description("Analyze project requirements and extract goals, constraints, and dependencies.").
		Domain("planning").
		Keywords("analyze", "requirements", "understand", "goals", "constraints").
		Priority(100).
		StringParam("query", "The requirement or task to analyze", true).
		StringParam("scope", "Scope of analysis (e.g., file path, module name)", false).
		ArrayParam("goals", "Explicit goals if known", "string", false).
		ArrayParam("constraints", "Known constraints to consider", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params analyzeRequirementsParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Query == "" {
				return nil, fmt.Errorf("query is required")
			}

			reqParams := map[string]any{}
			if params.Scope != "" {
				reqParams["scope"] = params.Scope
			}
			if len(params.Goals) > 0 {
				reqParams["goals"] = params.Goals
			}
			if len(params.Constraints) > 0 {
				reqParams["constraints"] = params.Constraints
			}

			requirements, err := a.analyzeRequirements(ctx, params.Query, reqParams)
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
		}).
		Build()
}

type designArchitectureParams struct {
	Requirements *Requirements `json:"requirements"`
	Patterns     []string      `json:"patterns,omitempty"`
}

func designArchitectureSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("design_architecture").
		Description("Design system architecture based on requirements and existing patterns.").
		Domain("design").
		Keywords("design", "architecture", "system", "components", "structure").
		Priority(95).
		ObjectParam("requirements", "Requirements to design for", map[string]*skills.Property{
			"query": {Type: "string", Description: "Main requirement query"},
			"goals": {Type: "array", Items: &skills.Property{Type: "string"}, Description: "Goals to achieve"},
			"scope": {Type: "string", Description: "Scope of the design"},
		}, true).
		ArrayParam("patterns", "Existing patterns to incorporate", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params designArchitectureParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Requirements == nil {
				return nil, fmt.Errorf("requirements is required")
			}

			var codebasePatterns *CodebasePatterns
			if len(params.Patterns) > 0 {
				codebasePatterns = &CodebasePatterns{
					Patterns: make([]PatternInfo, len(params.Patterns)),
				}
				for i, p := range params.Patterns {
					codebasePatterns.Patterns[i] = PatternInfo{Name: p}
				}
			}

			architecture, err := a.designArchitecture(ctx, params.Requirements, codebasePatterns)
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
		}).
		Build()
}

type generateTasksParams struct {
	Architecture     *SolutionArchitecture `json:"architecture"`
	MaxTasksPerAgent int                   `json:"max_tasks_per_agent,omitempty"`
	AllowParallel    bool                  `json:"allow_parallel,omitempty"`
}

func generateTasksSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("generate_tasks").
		Description("Generate atomic tasks from system architecture.").
		Domain("planning").
		Keywords("generate", "tasks", "atomic", "decompose", "breakdown").
		Priority(90).
		ObjectParam("architecture", "Architecture to generate tasks from", map[string]*skills.Property{
			"name":        {Type: "string", Description: "Architecture name"},
			"description": {Type: "string", Description: "Architecture description"},
			"components":  {Type: "array", Description: "Component specifications"},
		}, true).
		IntParam("max_tasks_per_agent", "Maximum tasks per agent (default: 5)", false).
		BoolParam("allow_parallel", "Allow parallel task execution (default: true)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params generateTasksParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Architecture == nil {
				return nil, fmt.Errorf("architecture is required")
			}

			constraints := &PlanConstraints{
				MaxTasksPerAgent: params.MaxTasksPerAgent,
				AllowParallel:    params.AllowParallel,
			}
			if constraints.MaxTasksPerAgent == 0 {
				constraints.MaxTasksPerAgent = 5
			}

			tasks, err := a.generateAtomicTasks(ctx, params.Architecture, constraints)
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
		}).
		Build()
}

type createWorkflowDAGParams struct {
	Tasks          []*AtomicTask `json:"tasks"`
	Policy         string        `json:"policy,omitempty"`
	MaxConcurrency int           `json:"max_concurrency,omitempty"`
}

func createWorkflowDAGSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("create_workflow_dag").
		Description("Create a workflow DAG for task orchestration.").
		Domain("planning").
		Keywords("workflow", "dag", "orchestration", "execution", "order").
		Priority(85).
		ArrayParam("tasks", "Tasks to create workflow from", "object", true).
		EnumParam("policy", "Execution policy", []string{"fail_fast", "continue"}, false).
		IntParam("max_concurrency", "Maximum concurrent tasks (default: 10)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params createWorkflowDAGParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Tasks) == 0 {
				return nil, fmt.Errorf("tasks are required")
			}

			workflow, err := a.createWorkflowDAG(ctx, params.Tasks)
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
		}).
		Build()
}

type estimateComplexityParams struct {
	Description string         `json:"description"`
	Context     map[string]any `json:"context,omitempty"`
}

func estimateComplexitySkill(a *Architect) *skills.Skill {
	return skills.NewSkill("estimate_complexity").
		Description("Estimate the complexity and token usage for a task.").
		Domain("planning").
		Keywords("estimate", "complexity", "tokens", "effort", "size").
		Priority(80).
		StringParam("description", "Task description to estimate", true).
		ObjectParam("context", "Additional context for estimation", map[string]*skills.Property{
			"has_dependencies":  {Type: "boolean", Description: "Whether task has dependencies"},
			"dependency_count":  {Type: "integer", Description: "Number of dependencies"},
			"scope":             {Type: "string", Description: "Task scope"},
			"involves_tests":    {Type: "boolean", Description: "Whether task includes testing"},
			"involves_refactor": {Type: "boolean", Description: "Whether task involves refactoring"},
		}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params estimateComplexityParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Description == "" {
				return nil, fmt.Errorf("description is required")
			}

			estimate := estimateTaskComplexity(params.Description, params.Context)

			return map[string]any{
				"estimate": estimate,
			}, nil
		}).
		Build()
}

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

type consultKnowledgeParams struct {
	Target string `json:"target"`
	Query  string `json:"query"`
	Scope  string `json:"scope,omitempty"`
}

func consultKnowledgeSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("consult_knowledge").
		Description("Consult Librarian or Academic for codebase patterns and knowledge.").
		Domain("planning").
		Keywords("consult", "librarian", "academic", "patterns", "knowledge").
		Priority(75).
		EnumParam("target", "Agent to consult", []string{"librarian", "academic"}, true).
		StringParam("query", "Question or topic to consult about", true).
		StringParam("scope", "Scope to limit the search", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params consultKnowledgeParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Target == "" {
				return nil, fmt.Errorf("target is required")
			}
			if params.Query == "" {
				return nil, fmt.Errorf("query is required")
			}

			if !a.running || a.bus == nil {
				return map[string]any{
					"status":  "unavailable",
					"message": "Event bus not available for consultation",
				}, nil
			}

			req := &ArchitectRequest{
				ID:        uuid.New().String(),
				Intent:    IntentConsult,
				Query:     params.Query,
				Timestamp: time.Now(),
				Params: map[string]any{
					"target": params.Target,
					"scope":  params.Scope,
				},
			}

			if params.Target == "librarian" {
				requirements := &Requirements{Query: params.Query, Scope: params.Scope}
				patterns, err := a.consultLibrarian(ctx, requirements)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"target":   params.Target,
					"patterns": patterns,
					"query":    req.Query,
				}, nil
			}

			return map[string]any{
				"target": params.Target,
				"status": "consultation_requested",
				"query":  req.Query,
			}, nil
		}).
		Build()
}
