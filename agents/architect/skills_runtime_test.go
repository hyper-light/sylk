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
	// Phase 1 refactor: `consult` removed in favor of consult_peer.
	if !toolDefsContain(defs, "consult_peer") {
		t.Fatal("expected consult_peer to be loaded")
	}
	// Phase 2.4 refactor: pre_delegation_declare → delegation(action=declare).
	if !toolDefsContain(defs, "delegation") {
		t.Fatal("expected delegation skill to be loaded")
	}
	// Phase 2.6 refactor: read_research_paper → academic_research(action=read).
	// academic_research is visible-by-default (not lazy-loaded).
	if !toolDefsContain(defs, "academic_research") {
		t.Fatal("expected academic_research to be loaded")
	}
	if !toolDefsContain(defs, "recall_forward") {
		t.Fatal("expected recall_forward to be loaded")
	}
	if !toolDefsContain(defs, "carry_forward") {
		t.Fatal("expected carry_forward to be loaded")
	}
}

func TestArchitect_SkillCount(t *testing.T) {
	a := newTestArchitect(t, Config{AllowPlanningWithoutConsultation: true})
	allSkills := a.skills.GetAll()
	if got := len(allSkills); got < 15 {
		names := make([]string, len(allSkills))
		for i, s := range allSkills {
			names[i] = s.Name
		}
		t.Fatalf("expected at least 15 registered skills, got %d: %v", got, names)
	}
}

func TestArchitect_PlanningWithoutBusStillResponds(t *testing.T) {
	a := newTestArchitect(t, Config{})
	req := &ArchitectRequest{
		ID:        "req_gate",
		Intent:    IntentPlan,
		Query:     "plan a complex migration",
		Timestamp: time.Now(),
	}
	plan, err := a.executePlanningProtocol(context.Background(), req)
	if err != nil {
		t.Fatalf("expected architect to continue without bus consultation, got error: %v", err)
	}
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
}

func TestArchitect_ReadResearchPaperSkill(t *testing.T) {
	// Phase 2.6 refactor: read_research_paper folded into
	// academic_research(action=read).
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
		"action":        "read",
		"research_slug": "redis-caching",
		"paper_path":    paperPath,
		"summary":       "Redis cache-aside recommendation",
		"session_id":    "s1",
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	result := a.InvokeSkill(context.Background(), "academic_research", payload)
	if result == nil || !result.Success {
		t.Fatalf("academic_research(action=read) failed: %+v", result)
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
	// Phase 2.6 refactor: read_research_paper folded into academic_research.
	if !toolDefsContain(defs, "academic_research") {
		t.Fatal("expected academic_research to be loaded for research request")
	}
}

func TestArchitect_PlanModeTodoFlow(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
	})

	enterPayload, err := json.Marshal(map[string]any{
		"action":           "enter",
		"task_description": "validate todo completion flow",
		"session_id":       "todo_s1",
	})
	if err != nil {
		t.Fatalf("enter payload marshal failed: %v", err)
	}
	enterResult := a.InvokeSkill(context.Background(), "plan_mode", enterPayload)
	if enterResult == nil || !enterResult.Success {
		t.Fatalf("plan_mode enter failed: %+v", enterResult)
	}

	writePayload, err := json.Marshal(map[string]any{
		"action":     "todo_write",
		"session_id": "todo_s1",
		"todos": []map[string]any{
			{"content": "step 1", "status": "in_progress", "active_form": "running step 1"},
			{"content": "step 2", "status": "pending", "active_form": "running step 2"},
		},
	})
	if err != nil {
		t.Fatalf("todo payload marshal failed: %v", err)
	}
	writeResult := a.InvokeSkill(context.Background(), "plan_mode", writePayload)
	if writeResult == nil || !writeResult.Success {
		t.Fatalf("plan_mode todo_write failed: %+v", writeResult)
	}

	completePayload, err := json.Marshal(map[string]any{
		"action":     "todo_mark_complete",
		"session_id": "todo_s1",
		"index":      0,
	})
	if err != nil {
		t.Fatalf("complete payload marshal failed: %v", err)
	}
	completeResult := a.InvokeSkill(context.Background(), "plan_mode", completePayload)
	if completeResult == nil || !completeResult.Success {
		t.Fatalf("plan_mode todo_mark_complete failed: %+v", completeResult)
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

func TestArchitect_StartPlanning_ReusesRequestScopedPlan(t *testing.T) {
	a := newTestArchitect(t, Config{AllowPlanningWithoutConsultation: true})

	ctx := withArchitectSessionID(context.Background(), "sess-reuse")
	ctx = withArchitectStreamContext(ctx, "corr-reuse", "tui")
	// Phase 2.K / CR-4 refactor: start_planning → plan(action=start).
	payload, err := json.Marshal(map[string]any{"action": "start", "query": "Create a hello world CLI"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	first := a.InvokeSkill(ctx, "plan", payload)
	if first == nil || !first.Success {
		t.Fatalf("first plan(action=start) failed: %+v", first)
	}
	firstData, ok := first.Data.(map[string]any)
	if !ok {
		t.Fatalf("first result data type = %T", first.Data)
	}
	firstPlanID, _ := firstData["plan_id"].(string)
	if firstPlanID == "" {
		t.Fatal("first plan_id is empty")
	}

	second := a.InvokeSkill(ctx, "plan", payload)
	if second == nil || !second.Success {
		t.Fatalf("second plan(action=start) failed: %+v", second)
	}
	secondData, ok := second.Data.(map[string]any)
	if !ok {
		t.Fatalf("second result data type = %T", second.Data)
	}
	secondPlanID, _ := secondData["plan_id"].(string)
	if secondPlanID != firstPlanID {
		t.Fatalf("second plan_id = %q, want %q", secondPlanID, firstPlanID)
	}
	if reused, _ := secondData["reused"].(bool); !reused {
		t.Fatal("expected second plan(action=start) call to report reused=true")
	}
}

func TestArchitect_GenerateTasks_ReusesReadyPlanArtifacts(t *testing.T) {
	a := newTestArchitect(t, Config{AllowPlanningWithoutConsultation: true})

	plan := &DesignPlan{
		ID:        "plan-ready",
		SessionID: "sess-ready",
		Status:    PlanStatusReady,
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
		Architecture: &SolutionArchitecture{
			Name: "cli",
			Components: []ComponentSpec{
				{Name: "cli"},
			},
		},
		Tasks: []*AtomicTask{
			{ID: "task-1", Name: "create cli", Complexity: ComplexityLow, EstimatedTokens: 120},
		},
		Workflow: &WorkflowDAG{
			Status: WorkflowStatusPending,
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusReady)
	if err := a.persistPlanState(plan); err != nil {
		t.Fatalf("persist plan: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"action":  "generate_tasks",
		"plan_id": plan.ID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	result := a.InvokeSkill(context.Background(), "plan", payload)
	if result == nil || !result.Success {
		t.Fatalf("generate_tasks failed: %+v", result)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("result data type = %T", result.Data)
	}
	if reused, _ := data["reused"].(bool); !reused {
		t.Fatal("expected generate_tasks to reuse ready plan artifacts")
	}
}

func TestArchitect_PlanDesign_RequiresFreshConsultBeforeDesign(t *testing.T) {
	a := newTestArchitect(t, Config{AllowPlanningWithoutConsultation: true})

	// Phase 2.K / CR-4 refactor: start_planning → plan(action=start).
	startPayload, err := json.Marshal(map[string]any{
		"action": "start",
		"query":  "Create a minimal Python hello world CLI application.",
	})
	if err != nil {
		t.Fatalf("marshal start payload: %v", err)
	}
	startResult := a.InvokeSkill(context.Background(), "plan", startPayload)
	if startResult == nil || !startResult.Success {
		t.Fatalf("plan(action=start) failed: %+v", startResult)
	}
	startData, ok := startResult.Data.(map[string]any)
	if !ok {
		t.Fatalf("start result data type = %T", startResult.Data)
	}
	planID, _ := startData["plan_id"].(string)
	if planID == "" {
		t.Fatal("expected plan_id from start_planning")
	}

	analyzePayload, err := json.Marshal(map[string]any{
		"action":  "analyze",
		"plan_id": planID,
		"query":   "Create a minimal Python hello world CLI application using argparse.",
	})
	if err != nil {
		t.Fatalf("marshal analyze payload: %v", err)
	}
	analyzeResult := a.InvokeSkill(context.Background(), "plan", analyzePayload)
	if analyzeResult == nil || !analyzeResult.Success {
		t.Fatalf("plan analyze failed: %+v", analyzeResult)
	}

	designPayload, err := json.Marshal(map[string]any{
		"action":  "design",
		"plan_id": planID,
	})
	if err != nil {
		t.Fatalf("marshal design payload: %v", err)
	}
	designResult := a.InvokeSkill(context.Background(), "plan", designPayload)
	if designResult == nil || !designResult.Success {
		t.Fatalf("plan design without evidence = %+v, want structured consultation gate", designResult)
	}
	designGate, ok := designResult.Data.(map[string]any)
	if !ok {
		t.Fatalf("plan design without evidence data type = %T, want map[string]any", designResult.Data)
	}
	if designGate["requires_consultation"] != true || designGate["ready_for_design"] != false {
		t.Fatalf("plan design without evidence = %+v, want requires_consultation gate", designResult)
	}

	designPayload, err = json.Marshal(map[string]any{
		"action":  "design",
		"plan_id": planID,
		"evidence": []map[string]any{{
			"Kind":       EvidenceKindConsult,
			"Target":     "librarian",
			"Query":      "current project structure for a minimal Python CLI",
			"Success":    true,
			"Summary":    "No existing Python package structure; keep the CLI minimal.",
			"ReceivedAt": time.Now(),
		}},
	})
	if err != nil {
		t.Fatalf("marshal design payload with evidence: %v", err)
	}
	designResult = a.InvokeSkill(context.Background(), "plan", designPayload)
	if designResult == nil || !designResult.Success {
		t.Fatalf("plan design with evidence failed: %+v", designResult)
	}
	designData, ok := designResult.Data.(map[string]any)
	if !ok {
		t.Fatalf("design result data type = %T", designResult.Data)
	}
	if got, _ := designData["plan_status"].(string); got != "designing" {
		t.Fatalf("plan_status = %q, want %q", got, "designing")
	}
}

// ---------------------------------------------------------------------------
// Dispatch coverage tests for consolidated skills
// ---------------------------------------------------------------------------

func TestArchitect_GitSkillDispatch(t *testing.T) {
	root := t.TempDir()
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 root,
	})

	// git status should work in any directory (even non-git)
	for _, cmd := range []string{"status", "diff", "log", "ls_files", "branch_list"} {
		payload, _ := json.Marshal(map[string]any{"command": cmd})
		result := a.InvokeSkill(context.Background(), "git", payload)
		// These may fail in a non-git dir, but the dispatch should resolve correctly
		if result == nil {
			t.Fatalf("git command=%s returned nil result", cmd)
		}
	}

	// unknown command returns error
	payload, _ := json.Marshal(map[string]any{"command": "rebase"})
	result := a.InvokeSkill(context.Background(), "git", payload)
	if result != nil && result.Success {
		t.Fatal("expected git rebase to fail with unknown command error")
	}

	// blame without file returns validation error
	payload, _ = json.Marshal(map[string]any{"command": "blame"})
	result = a.InvokeSkill(context.Background(), "git", payload)
	if result != nil && result.Success {
		t.Fatal("expected git blame without file to fail")
	}
}

func TestArchitect_LspSkillDispatch(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
	})

	// unknown action returns error
	payload, _ := json.Marshal(map[string]any{"action": "rename"})
	result := a.InvokeSkill(context.Background(), "lsp", payload)
	if result != nil && result.Success {
		t.Fatal("expected lsp rename to fail with unknown action error")
	}

	// goto_definition without file returns validation error
	payload, _ = json.Marshal(map[string]any{"action": "goto_definition"})
	result = a.InvokeSkill(context.Background(), "lsp", payload)
	if result != nil && result.Success {
		t.Fatal("expected lsp goto_definition without file to fail")
	}
}

func TestArchitect_ConsultSkillDispatch(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
	})

	// single without target returns error
	payload, _ := json.Marshal(map[string]any{
		"mode":  "single",
		"query": "test query",
	})
	result := a.InvokeSkill(context.Background(), "consult", payload)
	if result != nil && result.Success {
		t.Fatal("expected consult single without target to fail")
	}

	// single with invalid target returns error
	payload, _ = json.Marshal(map[string]any{
		"mode":   "single",
		"target": "invalid_agent",
		"query":  "test query",
	})
	result = a.InvokeSkill(context.Background(), "consult", payload)
	if result != nil && result.Success {
		t.Fatal("expected consult single with invalid target to fail")
	}

	// knowledge with non-knowledge target returns error
	payload, _ = json.Marshal(map[string]any{
		"mode":   "knowledge",
		"target": "engineer",
		"query":  "test query",
	})
	result = a.InvokeSkill(context.Background(), "consult", payload)
	if result != nil && result.Success {
		t.Fatal("expected consult knowledge with engineer target to fail")
	}

	// unknown mode returns error
	payload, _ = json.Marshal(map[string]any{
		"mode":  "unknown",
		"query": "test query",
	})
	result = a.InvokeSkill(context.Background(), "consult", payload)
	if result != nil && result.Success {
		t.Fatal("expected consult unknown mode to fail")
	}
}

func TestArchitect_PlanSkillDispatch(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
	})

	// estimate action
	payload, _ := json.Marshal(map[string]any{
		"action":      "estimate",
		"description": "implement a caching layer with Redis and local fallback",
	})
	result := a.InvokeSkill(context.Background(), "plan", payload)
	if result == nil || !result.Success {
		t.Fatalf("plan estimate failed: %+v", result)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected estimate result type: %T", result.Data)
	}
	if data["estimate"] == nil {
		t.Fatal("expected estimate in result")
	}

	// analyze without query returns error
	payload, _ = json.Marshal(map[string]any{"action": "analyze"})
	result = a.InvokeSkill(context.Background(), "plan", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan analyze without query to fail")
	}

	// revise without reason returns error
	payload, _ = json.Marshal(map[string]any{"action": "revise", "plan_id": "nonexistent"})
	result = a.InvokeSkill(context.Background(), "plan", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan revise without reason to fail")
	}

	// unknown action returns error
	payload, _ = json.Marshal(map[string]any{"action": "destroy"})
	result = a.InvokeSkill(context.Background(), "plan", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan unknown action to fail")
	}
}

func TestArchitect_PlanWorkflowSkillDispatch(t *testing.T) {
	// Phase 2.K / CR-4 refactor: plan_workflow folded into
	// plan(action=workflow, workflow_type=…).
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
	})

	// standard without tasks returns error
	payload, _ := json.Marshal(map[string]any{"action": "workflow", "workflow_type": "standard"})
	result := a.InvokeSkill(context.Background(), "plan", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan(action=workflow, workflow_type=standard) without tasks to fail")
	}

	// fix without corrections returns error
	payload, _ = json.Marshal(map[string]any{"action": "workflow", "workflow_type": "fix"})
	result = a.InvokeSkill(context.Background(), "plan", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan(action=workflow, workflow_type=fix) without corrections to fail")
	}

	// unknown type returns error
	payload, _ = json.Marshal(map[string]any{"action": "workflow", "workflow_type": "unknown"})
	result = a.InvokeSkill(context.Background(), "plan", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan(action=workflow) with unknown workflow_type to fail")
	}
}

func TestArchitect_PlanModeSkillDispatch(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
	})

	// enter without task_description returns error
	payload, _ := json.Marshal(map[string]any{"action": "enter"})
	result := a.InvokeSkill(context.Background(), "plan_mode", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan_mode enter without task_description to fail")
	}

	// update_file without plan mode returns error
	payload, _ = json.Marshal(map[string]any{
		"action":  "update_file",
		"content": "test",
	})
	result = a.InvokeSkill(context.Background(), "plan_mode", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan_mode update_file without plan mode to fail")
	}

	// unknown action returns error
	payload, _ = json.Marshal(map[string]any{"action": "destroy"})
	result = a.InvokeSkill(context.Background(), "plan_mode", payload)
	if result != nil && result.Success {
		t.Fatal("expected plan_mode unknown action to fail")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestArchitect(t *testing.T, cfg Config) *Architect {
	t.Helper()
	if cfg.WorkingDirectory == "" {
		cfg.WorkingDirectory = t.TempDir()
	}
	if cfg.Factory == nil {
		cfg.Factory = newTestFactory(t)
	}
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to create architect: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
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
