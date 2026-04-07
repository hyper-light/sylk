package guide

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// parsePlanAcceptanceResponse
// ---------------------------------------------------------------------------

func TestParsePlanAcceptanceResponse_Accept(t *testing.T) {
	input := planAcceptanceInput{
		Plan:         "Step 1: foo",
		PlanID:       "plan-1",
		PlanName:     "My Plan",
		UserResponse: "Looks great!",
	}
	content := `{"result":"accept","modifications":[]}`

	got, err := parsePlanAcceptanceResponse(content, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["result"] != "accept" {
		t.Errorf("result = %q, want %q", got["result"], "accept")
	}
	mods, ok := got["modifications"].([]string)
	if !ok {
		t.Fatalf("modifications type = %T, want []string", got["modifications"])
	}
	if len(mods) != 0 {
		t.Errorf("modifications len = %d, want 0", len(mods))
	}
	if got["plan_id"] != "plan-1" {
		t.Errorf("plan_id = %v, want %q", got["plan_id"], "plan-1")
	}
}

func TestParsePlanAcceptanceResponse_Modify(t *testing.T) {
	input := planAcceptanceInput{
		Plan:         "Step 1: foo",
		PlanID:       "plan-2",
		PlanName:     "Edit Plan",
		UserResponse: "Add error handling",
	}
	content := `{"result":"modify","modifications":["Add error handling to step 1"]}`

	got, err := parsePlanAcceptanceResponse(content, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["result"] != "modify" {
		t.Errorf("result = %q, want %q", got["result"], "modify")
	}
	mods := got["modifications"].([]string)
	if len(mods) != 1 || mods[0] != "Add error handling to step 1" {
		t.Errorf("modifications = %v, want [Add error handling to step 1]", mods)
	}
}

func TestParsePlanAcceptanceResponse_FencedJSON(t *testing.T) {
	input := planAcceptanceInput{
		Plan:         "Step 1: foo",
		PlanID:       "plan-2b",
		PlanName:     "Edit Plan",
		UserResponse: "Add error handling",
	}
	content := "```json\n{\"result\":\"modify\",\"modifications\":[\"Add error handling to step 1\"]}\n```"

	got, err := parsePlanAcceptanceResponse(content, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["result"] != "modify" {
		t.Errorf("result = %q, want %q", got["result"], "modify")
	}
}

func TestParsePlanAcceptanceResponse_Reject(t *testing.T) {
	input := planAcceptanceInput{
		Plan:         "Step 1: foo",
		PlanID:       "plan-3",
		PlanName:     "Bad Plan",
		UserResponse: "No, scrap it",
	}
	content := `{"result":"reject","modifications":["Complete rethink needed"]}`

	got, err := parsePlanAcceptanceResponse(content, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["result"] != "reject" {
		t.Errorf("result = %q, want %q", got["result"], "reject")
	}
}

func TestParsePlanAcceptanceResponse_InvalidResult(t *testing.T) {
	input := planAcceptanceInput{Plan: "x", PlanID: "p", PlanName: "n", UserResponse: "y"}
	content := `{"result":"maybe","modifications":[]}`

	_, err := parsePlanAcceptanceResponse(content, input)
	if err == nil {
		t.Fatal("expected error for invalid result")
	}
	if !strings.Contains(err.Error(), "invalid plan acceptance result") {
		t.Errorf("error = %v, want 'invalid plan acceptance result'", err)
	}
}

func TestParsePlanAcceptanceResponse_InvalidJSON(t *testing.T) {
	input := planAcceptanceInput{Plan: "x", PlanID: "p", PlanName: "n", UserResponse: "y"}

	_, err := parsePlanAcceptanceResponse("not json", input)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParsePlanAcceptanceResponse_EmptyContent(t *testing.T) {
	input := planAcceptanceInput{Plan: "x", PlanID: "p", PlanName: "n", UserResponse: "y"}

	_, err := parsePlanAcceptanceResponse("", input)
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("error = %v, want 'empty response'", err)
	}
}

func TestParsePlanAcceptanceResponse_NormalizesCase(t *testing.T) {
	input := planAcceptanceInput{Plan: "x", PlanID: "p", PlanName: "n", UserResponse: "y"}
	content := `{"result":"ACCEPT","modifications":[]}`

	got, err := parsePlanAcceptanceResponse(content, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["result"] != "accept" {
		t.Errorf("result = %q, want %q", got["result"], "accept")
	}
}

func TestParsePlanAcceptanceResponse_NullModifications(t *testing.T) {
	input := planAcceptanceInput{Plan: "x", PlanID: "p", PlanName: "n", UserResponse: "y"}
	content := `{"result":"accept","modifications":null}`

	got, err := parsePlanAcceptanceResponse(content, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mods, ok := got["modifications"].([]string)
	if !ok {
		t.Fatalf("modifications type = %T, want []string", got["modifications"])
	}
	if len(mods) != 0 {
		t.Errorf("modifications len = %d, want 0", len(mods))
	}
}

// ---------------------------------------------------------------------------
// loadPlanAcceptancePrompt
// ---------------------------------------------------------------------------

func TestLoadPlanAcceptancePrompt_ReturnsNonEmpty(t *testing.T) {
	if !planAcceptanceSkillFileExists() {
		t.Skip("SKILL.md not found on disk (binary build)")
	}
	resetPlanAcceptancePrompt()
	defer resetPlanAcceptancePrompt()

	body := loadPlanAcceptancePrompt()
	if body == "" {
		t.Fatal("expected non-empty SKILL.md body")
	}
	if !strings.Contains(body, "Evaluate Plan Acceptance") {
		t.Errorf("body does not contain expected heading, got: %s", body[:min(100, len(body))])
	}
}

// ---------------------------------------------------------------------------
// buildPlanAcceptanceUserPrompt
// ---------------------------------------------------------------------------

func TestBuildPlanAcceptanceUserPrompt_ContainsAllFields(t *testing.T) {
	input := planAcceptanceInput{
		Plan:         "Step 1: do the thing",
		PlanID:       "id-abc",
		PlanName:     "My Great Plan",
		UserResponse: "Sounds good to me!",
	}

	prompt := buildPlanAcceptanceUserPrompt(input)

	for _, want := range []string{"id-abc", "My Great Plan", "Step 1: do the thing", "Sounds good to me!"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// planAcceptanceResponseSchema
// ---------------------------------------------------------------------------

func TestPlanAcceptanceResponseSchema_Structure(t *testing.T) {
	schema := planAcceptanceResponseSchema()

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}

	resultProp, ok := props["result"].(map[string]any)
	if !ok {
		t.Fatal("schema missing result property")
	}
	enum, ok := resultProp["enum"].([]string)
	if !ok {
		t.Fatal("result property missing enum")
	}
	if len(enum) != 3 {
		t.Errorf("result enum len = %d, want 3", len(enum))
	}

	modsProp, ok := props["modifications"].(map[string]any)
	if !ok {
		t.Fatal("schema missing modifications property")
	}
	if modsProp["type"] != "array" {
		t.Errorf("modifications type = %v, want array", modsProp["type"])
	}

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("schema missing required")
	}
	if len(required) != 2 {
		t.Errorf("required len = %d, want 2", len(required))
	}
}

// ---------------------------------------------------------------------------
// tryDirectSkillInvocation
// ---------------------------------------------------------------------------

func TestTryDirectSkillInvocation_PrefixMatch(t *testing.T) {
	registry := skills.NewRegistry()
	registry.Register(
		skills.NewSkill("evaluate_plan_acceptance").
			Description("test").
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return map[string]any{"result": "accept", "modifications": []string{}}, nil
			}).
			Build(),
	)

	g := &Guide{skills: registry}
	payload := `{"plan":"p","plan_id":"id","plan_name":"n","user_response":"yes"}`
	data, ok, err := g.tryDirectSkillInvocation(context.Background(), "evaluate-plan-acceptance: "+payload)

	if !ok {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", data)
	}
	if m["result"] != "accept" {
		t.Errorf("result = %v, want accept", m["result"])
	}
}

func TestTryDirectSkillInvocation_NoMatch(t *testing.T) {
	g := &Guide{skills: skills.NewRegistry()}
	_, ok, err := g.tryDirectSkillInvocation(context.Background(), "some random input")
	if ok {
		t.Fatal("expected no match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTryDirectSkillInvocation_InvalidJSON(t *testing.T) {
	g := &Guide{skills: skills.NewRegistry()}
	_, ok, err := g.tryDirectSkillInvocation(context.Background(), "evaluate-plan-acceptance: not-json")
	if !ok {
		t.Fatal("expected prefix match for invalid JSON")
	}
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestTryDirectSkillInvocation_SkillError(t *testing.T) {
	registry := skills.NewRegistry()
	registry.Register(
		skills.NewSkill("evaluate_plan_acceptance").
			Description("test").
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return nil, json.Unmarshal([]byte("bad"), &struct{}{})
			}).
			Build(),
	)

	g := &Guide{skills: registry}
	payload := `{"plan":"p","plan_id":"id","plan_name":"n","user_response":"yes"}`
	_, ok, err := g.tryDirectSkillInvocation(context.Background(), "evaluate-plan-acceptance: "+payload)
	if !ok {
		t.Fatal("expected prefix match when skill returns error")
	}
	if err == nil {
		t.Fatal("expected skill error")
	}
}

func TestEvaluatePlanAcceptance_DeterministicApprovalWithoutProvider(t *testing.T) {
	g := &Guide{}
	got, err := g.evaluatePlanAcceptance(context.Background(), planAcceptanceInput{
		Plan:         "step 1",
		PlanID:       "plan-1",
		PlanName:     "Test Plan",
		UserResponse: "Kick it off!",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["result"] != "accept" {
		t.Fatalf("result = %v, want accept", got["result"])
	}
	mods, ok := got["modifications"].([]string)
	if !ok {
		t.Fatalf("modifications type = %T, want []string", got["modifications"])
	}
	if len(mods) != 0 {
		t.Fatalf("modifications = %v, want empty", mods)
	}
}

func TestTryDirectSkillInvocation_DeterministicApprovalUsesRegisteredSkillWithoutProvider(t *testing.T) {
	g := &Guide{skills: skills.NewRegistry()}
	g.skills.Register(evaluatePlanAcceptanceSkill(g))

	payload := `{"plan":"p","plan_id":"id","plan_name":"n","user_response":"Kick it off!"}`
	data, ok, err := g.tryDirectSkillInvocation(context.Background(), "evaluate-plan-acceptance: "+payload)
	if !ok {
		t.Fatal("expected direct-skill prefix match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", data)
	}
	if m["result"] != "accept" {
		t.Fatalf("result = %v, want accept", m["result"])
	}
}

// ---------------------------------------------------------------------------
// evaluatePlanAcceptanceSkill registration
// ---------------------------------------------------------------------------

func TestEvaluatePlanAcceptanceSkill_NilGuideReturnsError(t *testing.T) {
	skill := evaluatePlanAcceptanceSkill(nil)
	if skill == nil {
		t.Fatal("skill is nil")
	}
	if skill.Name != "evaluate_plan_acceptance" {
		t.Errorf("name = %q, want %q", skill.Name, "evaluate_plan_acceptance")
	}

	input, _ := json.Marshal(planAcceptanceInput{
		Plan:         "step 1",
		PlanID:       "id",
		PlanName:     "name",
		UserResponse: "ok",
	})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error when guide is nil")
	}
	if !strings.Contains(err.Error(), "guide instance required") {
		t.Errorf("error = %v, want 'guide instance required'", err)
	}
}

func TestEvaluatePlanAcceptanceSkill_MissingParams(t *testing.T) {
	skill := evaluatePlanAcceptanceSkill(nil)

	tests := []struct {
		name  string
		input planAcceptanceInput
		want  string
	}{
		{"missing plan", planAcceptanceInput{PlanID: "id", PlanName: "n", UserResponse: "ok"}, "plan"},
		{"missing user_response", planAcceptanceInput{Plan: "p", PlanID: "id", PlanName: "n"}, "user_response"},
		{"missing plan_id", planAcceptanceInput{Plan: "p", PlanName: "n", UserResponse: "ok"}, "plan_id"},
		{"missing plan_name", planAcceptanceInput{Plan: "p", PlanID: "id", UserResponse: "ok"}, "plan_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.input)
			_, err := skill.Handler(context.Background(), raw)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want to contain %q", err, tt.want)
			}
		})
	}
}
