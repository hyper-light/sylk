// Phase 2.4 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// pre_delegation_declare and validate_pre_delegation collapse into one
// `delegation` skill with action ∈ {declare, validate}. The underlying
// handler bodies come from the original skills unchanged.
//
// Note: the original plan doc suggested folding these into the `plan`
// skill's action enum, but that would have bloated `planInput` with
// delegation-specific fields (target_agent, required_skills, etc.)
// that only apply to two of the existing seven plan actions. A
// dedicated `delegation` skill is a cleaner collapse.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

type delegationInput struct {
	Action string `json:"action"`
	PlanID string `json:"plan_id,omitempty"`

	// declare fields
	TaskID                   string   `json:"task_id,omitempty"`
	TargetAgent              string   `json:"target_agent,omitempty"`
	Reasoning                string   `json:"reasoning,omitempty"`
	RequiredSkills           []string `json:"required_skills,omitempty"`
	ExpectedOutcome          string   `json:"expected_outcome,omitempty"`
	FailureCriteria          string   `json:"failure_criteria,omitempty"`
	UserClarificationNeeded  bool     `json:"user_clarification_needed,omitempty"`
	ChallengesRaised         []string `json:"challenges_raised,omitempty"`

	// validate fields
	DeclarationID string `json:"declaration_id,omitempty"`
}

// delegationSkill is the consolidated delegation primitive. Replaces
// pre_delegation_declare and validate_pre_delegation.
func delegationSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("delegation").
		Description("Create or validate a pre-delegation declaration before handoff. Actions:\n" +
			"- declare: Create and persist a formal declaration with consultation evidence (params: target_agent, reasoning, required_skills, expected_outcome, failure_criteria [all required]; plan_id, task_id, user_clarification_needed, challenges_raised).\n" +
			"- validate: Validate an existing declaration and sanity-check attached consultation evidence (params: plan_id, declaration_id).").
		Domain("delegation").
		Keywords("declare", "delegation", "handoff", "target agent", "evidence", "validate", "declaration").
		Priority(100).
		Usage("Call delegation(action=declare,…) before handing off to the Orchestrator. Later, optionally call delegation(action=validate,…) to confirm the declaration is still sound (e.g., consultation evidence fresh).").
		Example(`{"action": "declare", "plan_id": "plan_abc", "target_agent": "engineer", "reasoning": "Single-file change", "required_skills": ["go"], "expected_outcome": "WebSocket handler implemented", "failure_criteria": "Compilation errors"}`).
		Example(`{"action": "validate", "plan_id": "plan_abc", "declaration_id": "decl_xyz"}`).
		BestPractice("Always specify both expected_outcome and failure_criteria on declare — the Orchestrator uses them to evaluate task success.").
		EnumParam("action", "Delegation action", []string{"declare", "validate"}, true).
		StringParam("plan_id", "Plan identifier", false).
		StringParam("task_id", "Task identifier (declare)", false).
		StringParam("target_agent", "Target agent for delegation (declare)", false).
		StringParam("reasoning", "Reasoning for this delegation strategy (declare)", false).
		ArrayParam("required_skills", "Skills required for execution (declare)", "string", false).
		StringParam("expected_outcome", "Expected outcome for delegated task (declare)", false).
		StringParam("failure_criteria", "Failure criteria for delegated task (declare)", false).
		BoolParam("user_clarification_needed", "Whether unresolved ambiguity remains (declare)", false).
		ArrayParam("challenges_raised", "Concerns raised during planning (declare)", "string", false).
		StringParam("declaration_id", "Declaration identifier (validate)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params delegationInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(params.Action) {
			case "declare":
				return delegationDeclare(ctx, a, &params)
			case "validate":
				return delegationValidate(a, &params)
			default:
				return nil, fmt.Errorf("unknown delegation action: %q (expected declare|validate)", params.Action)
			}
		}).
		Build()
}

func delegationDeclare(ctx context.Context, a *Architect, p *delegationInput) (any, error) {
	if strings.TrimSpace(p.TargetAgent) == "" {
		return nil, fmt.Errorf("target_agent is required for action=declare")
	}
	if strings.TrimSpace(p.Reasoning) == "" {
		return nil, fmt.Errorf("reasoning is required for action=declare")
	}
	if strings.TrimSpace(p.ExpectedOutcome) == "" {
		return nil, fmt.Errorf("expected_outcome is required for action=declare")
	}
	if strings.TrimSpace(p.FailureCriteria) == "" {
		return nil, fmt.Errorf("failure_criteria is required for action=declare")
	}
	if len(p.RequiredSkills) == 0 {
		return nil, fmt.Errorf("required_skills is required for action=declare")
	}
	plan, err := a.selectPlan(p.PlanID)
	if err != nil {
		return nil, err
	}
	declaration := buildPreDelegationDeclaration(plan, &preDelegationParams{
		PlanID:                  p.PlanID,
		TaskID:                  p.TaskID,
		TargetAgent:             p.TargetAgent,
		Reasoning:               p.Reasoning,
		RequiredSkills:          p.RequiredSkills,
		ExpectedOutcome:         p.ExpectedOutcome,
		FailureCriteria:         p.FailureCriteria,
		UserClarification: p.UserClarificationNeeded,
		ChallengesRaised:        p.ChallengesRaised,
	})
	if err := a.validateDeclaration(declaration); err != nil {
		return nil, err
	}
	a.persistDeclaration(plan, declaration)
	a.publishDeclaration(ctx, declaration, plan.SessionID)
	return declaration, nil
}

func delegationValidate(a *Architect, p *delegationInput) (any, error) {
	decl, err := a.findDeclaration(p.PlanID, p.DeclarationID)
	if err != nil {
		return nil, err
	}
	if err := a.validateDeclaration(decl); err != nil {
		return map[string]any{"valid": false, "error": err.Error(), "declaration_id": decl.ID}, nil
	}
	return map[string]any{"valid": true, "declaration_id": decl.ID}, nil
}
