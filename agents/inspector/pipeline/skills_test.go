package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	coreskills "github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func TestDefineCriteriaSkill_AcceptsStringThresholdAndNormalizesGates(t *testing.T) {
	pi := &PipelineInspector{
		criteria: make(map[string]*shared.InspectorCriteria),
	}

	skill := defineCriteriaSkill(pi)
	if skill == nil || skill.Handler == nil {
		t.Fatal("expected define_criteria skill handler")
	}

	_, err := skill.Handler(context.Background(), json.RawMessage(`{
		"task_id":"task_1",
		"success_criteria":[{"description":"CLI prints expected greeting","verifiable":"true"}],
		"quality_gates":[{"metric":"coverage","threshold":"85","operator":"gte"}],
		"constraints":[{"description":"Use argparse only","required":"true"}]
	}`))
	if err != nil {
		t.Fatalf("define_criteria handler error = %v", err)
	}

	criteria := pi.criteria["task_1"]
	if criteria == nil {
		t.Fatal("expected criteria to be stored")
	}
	if len(criteria.SuccessCriteria) != 1 {
		t.Fatalf("expected 1 success criterion, got %d", len(criteria.SuccessCriteria))
	}
	if criteria.SuccessCriteria[0].ID != "criterion_1" {
		t.Fatalf("criterion ID = %q, want %q", criteria.SuccessCriteria[0].ID, "criterion_1")
	}
	if criteria.SuccessCriteria[0].VerificationMethod != "automated_check" {
		t.Fatalf("verification method = %q, want %q", criteria.SuccessCriteria[0].VerificationMethod, "automated_check")
	}
	if len(criteria.QualityGates) != 1 {
		t.Fatalf("expected 1 quality gate, got %d", len(criteria.QualityGates))
	}
	if criteria.QualityGates[0].Threshold != 85 {
		t.Fatalf("threshold = %v, want 85", criteria.QualityGates[0].Threshold)
	}
	if criteria.QualityGates[0].Operator != ">=" {
		t.Fatalf("operator = %q, want %q", criteria.QualityGates[0].Operator, ">=")
	}
	if criteria.QualityGates[0].Name != "coverage_1" {
		t.Fatalf("name = %q, want %q", criteria.QualityGates[0].Name, "coverage_1")
	}
	if len(criteria.Constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(criteria.Constraints))
	}
	if criteria.Constraints[0].Type != "requirement" {
		t.Fatalf("constraint type = %q, want %q", criteria.Constraints[0].Type, "requirement")
	}
}

func TestDefineCriteriaSkill_SchemaDeclaresNumericThreshold(t *testing.T) {
	skill := defineCriteriaSkill(&PipelineInspector{})
	if skill == nil || skill.InputSchema == nil {
		t.Fatal("expected define_criteria input schema")
	}

	gatesProp := skill.InputSchema.Properties["quality_gates"]
	if gatesProp == nil || gatesProp.Items == nil {
		t.Fatal("expected quality_gates array item schema")
	}
	thresholdProp := gatesProp.Items.Properties["threshold"]
	if thresholdProp == nil {
		t.Fatal("expected threshold property in quality_gates item schema")
	}
	if thresholdProp.Type != "number" {
		t.Fatalf("threshold type = %q, want %q", thresholdProp.Type, "number")
	}
}

func TestDefineCriteriaSkill_UsesCurrentTaskIDWhenRequestedIDIsPlaceholder(t *testing.T) {
	pi, err := New(shared.PipelineInspectorConfig{AgentID: "inspector-pipeline"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if pi.tools != nil {
			pi.tools.Close()
		}
	})

	pi.state.CurrentTaskID = "actual-task"
	pi.criteria["actual-task"] = &shared.InspectorCriteria{TaskID: "actual-task"}

	skill := defineCriteriaSkill(pi)
	resultAny, err := skill.Handler(context.Background(), json.RawMessage(`{
		"task_id":"task_1",
		"success_criteria":[{"description":"Validation stays attached to the active task"}]
	}`))
	if err != nil {
		t.Fatalf("define_criteria handler error = %v", err)
	}

	if pi.criteria["task_1"] != nil {
		t.Fatal("expected placeholder task criteria to be normalized to the active task")
	}
	criteria := pi.criteria["actual-task"]
	if criteria == nil {
		t.Fatal("expected criteria to be stored on the active task")
	}
	if criteria.TaskID != "actual-task" {
		t.Fatalf("criteria task ID = %q, want %q", criteria.TaskID, "actual-task")
	}

	result := resultAny.(map[string]any)
	if got := result["task_id"]; got != "actual-task" {
		t.Fatalf("task_id = %v, want actual-task", got)
	}
	if got := result["requested_task_id"]; got != "task_1" {
		t.Fatalf("requested_task_id = %v, want task_1", got)
	}
}

func TestGradeTaskQualitySkill_AutoValidatesResolvedCurrentTask(t *testing.T) {
	pi := newStubValidationPipelineInspector(t, "actual-task")
	pi.criteria["actual-task"] = &shared.InspectorCriteria{
		TaskID:       "actual-task",
		QualityGates: defaultQualityGates("engineer", false),
	}

	skill := gradeTaskQualitySkill(pi)
	resultAny, err := skill.Handler(context.Background(), json.RawMessage(`{"task_id":"task_1"}`))
	if err != nil {
		t.Fatalf("grade_task_quality handler error = %v", err)
	}

	if pi.results["actual-task"] == nil {
		t.Fatal("expected grading to synthesize a validation result for the active task")
	}

	result := resultAny.(map[string]any)
	if got := result["task_id"]; got != "actual-task" {
		t.Fatalf("task_id = %v, want actual-task", got)
	}
	if got := result["requested_task_id"]; got != "task_1" {
		t.Fatalf("requested_task_id = %v, want task_1", got)
	}
	if got := result["validation_ran"]; got != true {
		t.Fatalf("validation_ran = %v, want true", got)
	}
}

func TestStageInstructions_InspectIncludesValidateCriteriaBeforeGrade(t *testing.T) {
	instructions := stageInstructions("inspect")
	validateIdx := strings.Index(instructions, "validate_criteria")
	gradeIdx := strings.Index(instructions, "grade_task_quality")

	if validateIdx < 0 {
		t.Fatal("expected inspect stage instructions to mention validate_criteria")
	}
	if gradeIdx < 0 {
		t.Fatal("expected inspect stage instructions to mention grade_task_quality")
	}
	if validateIdx > gradeIdx {
		t.Fatalf("expected validate_criteria to appear before grade_task_quality, got %q", instructions)
	}
}

func TestPipelineInspectorToolDefinitionsIncludeGradeAndStatus(t *testing.T) {
	pi, err := New(shared.PipelineInspectorConfig{AgentID: "inspector-pipeline"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if pi.tools != nil {
			pi.tools.Close()
		}
	})

	names := toolDefinitionNames(pi.buildToolDefinitions())
	for _, want := range []string{"search_skills", "validate_criteria", "grade_task_quality", "get_validation_status"} {
		if !containsName(names, want) {
			t.Fatalf("tool definitions missing %q: %v", want, names)
		}
	}
}

func TestPipelineInspectorToolDefinitionsIncludeCoordinationTools(t *testing.T) {
	pi, err := New(shared.PipelineInspectorConfig{AgentID: "inspector-pipeline"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if pi.tools != nil {
			pi.tools.Close()
		}
	})

	names := toolDefinitionNames(pi.buildToolDefinitions())
	for _, want := range []string{
		"coord_query_view",
		"coord_watch_updates",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
	} {
		if !containsName(names, want) {
			t.Fatalf("tool definitions missing %q: %v", want, names)
		}
	}
}

func TestPipelineInspectorSafetyHookAllowsSearchSkills(t *testing.T) {
	pi, err := New(shared.PipelineInspectorConfig{AgentID: "inspector-pipeline"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if pi.tools != nil {
			pi.tools.Close()
		}
	})

	_, err = pi.executeToolCall(context.Background(), providers.ToolCall{
		ID:        "tool_1",
		Name:      "search_skills",
		Arguments: `{"query":"grade"}`,
	})
	if err != nil {
		t.Fatalf("executeToolCall(search_skills) error = %v", err)
	}
}

func TestPipelineInspectorSafetyHookAllowsCoordinationTools(t *testing.T) {
	pi, err := New(shared.PipelineInspectorConfig{AgentID: "inspector-pipeline"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if pi.tools != nil {
			pi.tools.Close()
		}
	})

	_, err = pi.executeToolCall(context.Background(), providers.ToolCall{
		ID:        "tool_2",
		Name:      "coord_query_view",
		Arguments: `{"task_id":"task_1"}`,
	})
	if err == nil {
		t.Fatal("expected coord_query_view to fail without a configured bus")
	}
	if strings.Contains(err.Error(), "not permitted for pipeline inspector") {
		t.Fatalf("coord_query_view was blocked by the safety hook: %v", err)
	}
}

func newStubValidationPipelineInspector(t *testing.T, currentTaskID string) *PipelineInspector {
	t.Helper()

	pi := &PipelineInspector{
		id:         "inspector-pipeline",
		criteria:   make(map[string]*shared.InspectorCriteria),
		taskFiles:  make(map[string][]string),
		results:    make(map[string]*shared.InspectorResult),
		state:      &shared.InspectorState{CurrentTaskID: currentTaskID},
		workerType: "engineer",
		skills:     coreskills.NewRegistry(),
		hooks:      coreskills.NewHookRegistry(),
		steering:   agentShared.NewSteeringManager(),
	}

	for _, name := range []string{
		"run_linter",
		"run_type_checker",
		"run_formatter_check",
		"run_security_scan",
		"analyze_complexity",
	} {
		toolName := name
		pi.skills.Register(coreskills.NewSkill(toolName).
			Description("stub validation tool").
			Domain("validation").
			Handler(func(context.Context, json.RawMessage) (any, error) {
				return map[string]any{"issues": []shared.ValidationIssue{}}, nil
			}).
			Build())
	}

	runtime, err := toolruntime.New(toolruntime.Config{
		Registry: pi.skills,
		Hooks:    pi.hooks,
		Manifest: pipelineInspectorToolManifest(pi.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		t.Fatalf("toolruntime.New() error = %v", err)
	}
	pi.tools = runtime
	pi.registerSafetyHook()

	t.Cleanup(func() {
		if pi.tools != nil {
			pi.tools.Close()
		}
	})

	return pi
}

func toolDefinitionNames(tools []providers.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func containsName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
