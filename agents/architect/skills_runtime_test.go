package architect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestArchitect_SkillsLoaded(t *testing.T) {
	a := newTestArchitect(t, Config{AllowPlanningWithoutConsultation: true})
	defs := a.GetLoadedSkillDefinitions()
	if len(defs) == 0 {
		t.Fatal("expected loaded skill definitions")
	}
	if !toolDefsContain(defs, "consult_before_planning") {
		t.Fatal("expected consult_before_planning to be loaded")
	}
	if !toolDefsContain(defs, "pre_delegation_declare") {
		t.Fatal("expected pre_delegation_declare to be loaded")
	}
	if toolDefsContain(defs, "read_research_paper") {
		t.Fatal("expected read_research_paper to be lazy-loaded, not core-loaded")
	}
}

func TestArchitect_ConsultationGateRequiresBus(t *testing.T) {
	a := newTestArchitect(t, Config{})
	req := &ArchitectRequest{
		ID:        "req_gate",
		Intent:    IntentPlan,
		Query:     "plan a complex migration",
		Timestamp: time.Now(),
	}
	_, err := a.Handle(context.Background(), req)
	if err == nil {
		t.Fatal("expected consultation gate error when bus is unavailable")
	}
}

func TestArchitect_ReadResearchPaperSkill(t *testing.T) {
	root := t.TempDir()
	paperPath := filepath.Join(root, "research.md")
	if err := os.WriteFile(paperPath, []byte("# Research\nUse cache-aside with redis."), 0644); err != nil {
		t.Fatalf("failed to create paper: %v", err)
	}

	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 root,
	})
	input := map[string]any{
		"research_slug": "redis-caching",
		"paper_path":    paperPath,
		"summary":       "Redis cache-aside recommendation",
		"session_id":    "s1",
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	result := a.InvokeSkill(context.Background(), "read_research_paper", payload)
	if result == nil || !result.Success {
		t.Fatalf("read_research_paper failed: %+v", result)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result data type: %T", result.Data)
	}
	if data["plan_id"] == "" {
		t.Fatal("expected plan_id in read_research_paper result")
	}
}

func TestArchitect_ProposalActionTriggersResearchRead(t *testing.T) {
	root := t.TempDir()
	paperPath := filepath.Join(root, "proposal.md")
	if err := os.WriteFile(paperPath, []byte("# Proposal\nShip oauth flow."), 0644); err != nil {
		t.Fatalf("failed to create proposal: %v", err)
	}
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 root,
	})
	action := &guide.ActionRequest{
		CorrelationID: "corr_proposal",
		SourceAgentID: "academic",
		TargetAgentID: "architect",
		Action:        "proposal",
		FireAndForget: true,
		Data: map[string]any{
			"research_slug": "oauth",
			"paper_path":    paperPath,
			"summary":       "oauth proposal",
			"session_id":    "s2",
		},
	}
	msg := guide.NewActionMessage("msg_proposal", action)
	if err := a.handleBusRequest(msg); err != nil {
		t.Fatalf("handleBusRequest failed: %v", err)
	}
	if len(a.GetAllActivePlans()) == 0 {
		t.Fatal("expected proposal action to create an active plan")
	}
}

func TestArchitect_PrepareToolDefinitionsForRequest_LazyLoadsResearchTools(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
	})
	req := &ArchitectRequest{
		ID:        "req_r",
		Intent:    IntentPlan,
		Query:     "read research paper and convert proposal to plan",
		Timestamp: time.Now(),
		Params: map[string]any{
			"include_academic": true,
		},
	}
	defs := a.PrepareToolDefinitionsForRequest(req)
	if len(defs) == 0 {
		t.Fatal("expected tool definitions after prepare")
	}
	if !toolDefsContain(defs, "read_research_paper") {
		t.Fatal("expected read_research_paper to be loaded for research request")
	}
}

func TestArchitect_TodoMarkCompleteSkill(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
	})

	enterPayload, err := json.Marshal(map[string]any{
		"task_description": "validate todo completion flow",
		"session_id":       "todo_s1",
	})
	if err != nil {
		t.Fatalf("enter payload marshal failed: %v", err)
	}
	enterResult := a.InvokeSkill(context.Background(), "enter_plan_mode", enterPayload)
	if enterResult == nil || !enterResult.Success {
		t.Fatalf("enter_plan_mode failed: %+v", enterResult)
	}

	writePayload, err := json.Marshal(map[string]any{
		"session_id": "todo_s1",
		"todos": []map[string]any{
			{"content": "step 1", "status": "in_progress", "active_form": "running step 1"},
			{"content": "step 2", "status": "pending", "active_form": "running step 2"},
		},
	})
	if err != nil {
		t.Fatalf("todo payload marshal failed: %v", err)
	}
	writeResult := a.InvokeSkill(context.Background(), "todo_write", writePayload)
	if writeResult == nil || !writeResult.Success {
		t.Fatalf("todo_write failed: %+v", writeResult)
	}

	completePayload, err := json.Marshal(map[string]any{
		"session_id": "todo_s1",
		"index":      0,
	})
	if err != nil {
		t.Fatalf("complete payload marshal failed: %v", err)
	}
	completeResult := a.InvokeSkill(context.Background(), "todo_mark_complete", completePayload)
	if completeResult == nil || !completeResult.Success {
		t.Fatalf("todo_mark_complete failed: %+v", completeResult)
	}
	data, ok := completeResult.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected complete result data: %T", completeResult.Data)
	}
	todo, ok := data["todo"].(PlanTodo)
	if !ok {
		t.Fatalf("unexpected todo payload type: %T", data["todo"])
	}
	if todo.Status != "completed" {
		t.Fatalf("todo status = %q, want completed", todo.Status)
	}
}

func newTestArchitect(t *testing.T, cfg Config) *Architect {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create architect: %v", err)
	}
	return a
}

func toolDefsContain(defs []map[string]any, name string) bool {
	for _, def := range defs {
		if def["name"] == name {
			return true
		}
	}
	return false
}
