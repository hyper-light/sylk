package skills

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/forest"
	coreskills "github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

func TestRoleForestSkillEngineerImplementationBranch(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			resolveIntent: func(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
				if input.AgentType != AgentTypeEngineer {
					t.Fatalf("resolve agent_type = %q, want %q", input.AgentType, AgentTypeEngineer)
				}
				return &forest.IntentResolution{
					Query:         input.Query,
					PrimaryIntent: "ship the implementation safely",
					ActiveRoots:   []string{"root-1"},
				}, nil
			},
			retrieveForest: func(_ context.Context, query forest.Query) ([]*forest.ForestPacket, error) {
				if query.AgentType != AgentTypeEngineer {
					t.Fatalf("retrieve agent_type = %q, want %q", query.AgentType, AgentTypeEngineer)
				}
				// Issue #11 Phase 3: engineer's plan-precedent surface
				// dropped explicit Capability — agent affordances roll up
				// into Intent, alongside Decision (also collapsed). The
				// canonical engineer query must still constrain to the
				// merged Intent + Constraint axis.
				if !hasFamily(query.Families, forest.TreeFamilyIntent) || !hasFamily(query.Families, forest.TreeFamilyConstraint) {
					t.Fatalf("unexpected families: %#v", query.Families)
				}
				if !query.IncludeCounterEvidence {
					t.Fatal("expected counter evidence enabled")
				}
				packet := testSkillForestPacket("node-1", forest.ForestNodeClaim, "Reuse the existing retry worker path")
				packet.CounterEvidence = []forest.ForestEvidence{{
					RefType: "node",
					RefID:   "risk-node",
					NodeID:  "risk-node",
					Summary: "Prior node failed without timeout coverage",
				}}
				return []*forest.ForestPacket{packet}, nil
			},
		},
	}

	skill := NewRoleForestSkill(deps, findRoleForestSpec(t, "engineer_forest_select_implementation_branch"))
	input, _ := json.Marshal(ForestRoleInput{Query: "implement safer retries"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*ForestRoleOutput)
	if output.Role != AgentTypeEngineer {
		t.Fatalf("role = %q, want %q", output.Role, AgentTypeEngineer)
	}
	if output.Intent == nil || output.Intent.PrimaryIntent != "ship the implementation safely" {
		t.Fatalf("unexpected intent: %#v", output.Intent)
	}
	if len(output.Packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(output.Packets))
	}
	if len(output.Focus) == 0 {
		t.Fatal("expected focus hints")
	}
}

func TestRoleForestSkillDesignerPredictsAdjacentValue(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			resolveIntent: func(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
				return &forest.IntentResolution{
					Query:         input.Query,
					PrimaryIntent: "improve the dashboard UX without extra scope",
				}, nil
			},
			retrieveForest: func(_ context.Context, query forest.Query) ([]*forest.ForestPacket, error) {
				if query.AgentType != AgentTypeDesigner {
					t.Fatalf("predict agent_type = %q, want %q", query.AgentType, AgentTypeDesigner)
				}
				return []*forest.ForestPacket{testSkillForestPacket("node-2", forest.ForestNodeClaim, "Expose the primary status earlier in the flow")}, nil
			},
		},
	}

	skill := NewRoleForestSkill(deps, findRoleForestSpec(t, "designer_forest_discover_adjacent_value"))
	input, _ := json.Marshal(ForestRoleInput{
		Query:     "improve the dashboard UX",
		SessionID: "sess-1",
		Horizon:   "session",
	})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*ForestRoleOutput)
	if len(output.Packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(output.Packets))
	}
	if output.Intent == nil || output.Intent.PrimaryIntent == "" {
		t.Fatal("expected intent resolution in role output")
	}
}

func TestRoleForestSkillUsesRealTaskHorizon(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			resolveIntent: func(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
				if input.Horizon != forest.CanopyHorizonTask {
					t.Fatalf("resolve horizon = %q, want %q", input.Horizon, forest.CanopyHorizonTask)
				}
				if input.TaskID != "task-123" {
					t.Fatalf("resolve task_id = %q, want task-123", input.TaskID)
				}
				return &forest.IntentResolution{
					Query:         input.Query,
					PrimaryIntent: "choose the right task-scoped tests",
				}, nil
			},
			retrieveForest: func(_ context.Context, query forest.Query) ([]*forest.ForestPacket, error) {
				if query.Horizon != forest.CanopyHorizonTask {
					t.Fatalf("retrieve horizon = %q, want %q", query.Horizon, forest.CanopyHorizonTask)
				}
				if query.TaskID != "task-123" {
					t.Fatalf("retrieve task_id = %q, want task-123", query.TaskID)
				}
				return []*forest.ForestPacket{testSkillForestPacket("node-task", forest.ForestNodeValidation, "Prior task-scoped regressions centered on timeout handling")}, nil
			},
		},
	}

	skill := NewRoleForestSkill(deps, findRoleForestSpec(t, "tester_forest_get_test_targets"))
	input, _ := json.Marshal(ForestRoleInput{
		Query:     "identify the highest-value test targets for this task",
		SessionID: "sess-task",
		TaskID:    "task-123",
		Horizon:   "task",
	})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*ForestRoleOutput)
	if output.Intent == nil || output.Intent.PrimaryIntent == "" {
		t.Fatal("expected intent resolution in role output")
	}
	if len(output.Packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(output.Packets))
	}
}

func TestRoleForestSkillSchemaIncludesTaskHorizon(t *testing.T) {
	t.Parallel()

	skill := NewRoleForestSkill(&RetrievalDependencies{Forest: &mockForestService{}}, findRoleForestSpec(t, "tester_forest_get_test_targets"))
	if skill.InputSchema == nil {
		t.Fatal("expected input schema")
	}
	prop := skill.InputSchema.Properties["horizon"]
	if prop == nil {
		t.Fatal("expected horizon property")
	}
	found := false
	for _, value := range prop.Enum {
		if value == string(forest.CanopyHorizonTask) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected horizon enum to include %q, got %#v", forest.CanopyHorizonTask, prop.Enum)
	}
}

func TestRoleForestSkillDefaultsScopeFromContext(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			resolveIntent: func(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
				if input.SessionID != "sess-ctx" {
					t.Fatalf("resolve session_id = %q, want sess-ctx", input.SessionID)
				}
				if input.TaskID != "task-ctx" {
					t.Fatalf("resolve task_id = %q, want task-ctx", input.TaskID)
				}
				if input.Horizon != forest.CanopyHorizonTask {
					t.Fatalf("resolve horizon = %q, want %q", input.Horizon, forest.CanopyHorizonTask)
				}
				return &forest.IntentResolution{PrimaryIntent: "ctx-driven retrieval"}, nil
			},
			retrieveForest: func(_ context.Context, query forest.Query) ([]*forest.ForestPacket, error) {
				if query.SessionID != "sess-ctx" {
					t.Fatalf("retrieve session_id = %q, want sess-ctx", query.SessionID)
				}
				if query.TaskID != "task-ctx" {
					t.Fatalf("retrieve task_id = %q, want task-ctx", query.TaskID)
				}
				if query.Horizon != forest.CanopyHorizonTask {
					t.Fatalf("retrieve horizon = %q, want %q", query.Horizon, forest.CanopyHorizonTask)
				}
				return []*forest.ForestPacket{}, nil
			},
		},
	}

	ctx := versioning.WithTaskID(versioning.WithSessionID(context.Background(), "sess-ctx"), "task-ctx")
	skill := NewRoleForestSkill(deps, findRoleForestSpec(t, "tester_forest_get_test_targets"))
	input, _ := json.Marshal(ForestRoleInput{Query: "ctx default scope"})
	if _, err := skill.Handler(ctx, input); err != nil {
		t.Fatalf("handler error: %v", err)
	}
}

func TestGetSkillsForAgentIncludesSupplementalDomains(t *testing.T) {
	t.Parallel()

	registry := coreskills.NewRegistry()
	deps := &AdaptiveRetrievalDependencies{
		Universal: &RetrievalDependencies{Forest: &mockForestService{}},
		Pipeline:  &PipelineDependencies{},
	}

	if err := RegisterAdaptiveRetrievalSkills(registry, deps); err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	engineerSkills := GetSkillsForAgent(registry, AgentTypeEngineer)
	if !containsSkill(engineerSkills, "pipeline_get_implementation_context") {
		t.Fatal("engineer should inherit pipeline skills via supplemental domains")
	}
	if !containsSkill(engineerSkills, "engineer_forest_consult") {
		t.Fatal("engineer should receive collapsed engineer_forest_consult skill")
	}

	scribeSkills := GetSkillsForAgent(registry, "scribe-engineer")
	if !containsSkill(scribeSkills, "scribe_forest_consult") {
		t.Fatal("scribe-engineer should inherit collapsed scribe_forest_consult skill")
	}
}

func findRoleForestSpec(t *testing.T, name string) forestRoleSkillSpec {
	t.Helper()
	for _, spec := range forestRoleSkillSpecs {
		if spec.Name == name {
			return spec
		}
	}
	t.Fatalf("role forest spec %q not found", name)
	return forestRoleSkillSpec{}
}

func hasFamily(families []forest.TreeFamily, target forest.TreeFamily) bool {
	for _, family := range families {
		if family == target {
			return true
		}
	}
	return false
}

func containsSkill(skillList []*coreskills.Skill, name string) bool {
	for _, skill := range skillList {
		if skill.Name == name {
			return true
		}
	}
	return false
}
