package agent

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestModel_SelectedAgentID(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetFocused(true)
	if got := model.SelectedAgentID(); got != "" {
		t.Fatalf("SelectedAgentID() = %q, want empty", got)
	}

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "architect", "architect")

	if got := model.SelectedAgentID(); got != "guide" {
		t.Fatalf("SelectedAgentID() = %q, want guide", got)
	}

	model.CycleNext()
	if got := model.SelectedAgentID(); got != "architect" {
		t.Fatalf("SelectedAgentID() after cycle = %q, want architect", got)
	}
}

func TestModel_RebuildRows(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	// Add standalone agents.
	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "orchestrator", "orchestrator")

	// Add knowledge agent.
	pushAgentActivity(model, "librarian", "librarian")

	model.ensureRows()

	// Expect: spacer + section "global" + 2 standalone + spacer + section "knowledge" + 1 knowledge = 7 rows.
	if len(model.rows) != 7 {
		t.Fatalf("rebuildRows() produced %d rows, want 7", len(model.rows))
	}

	// First row should be spacer.
	if model.rows[0].Kind != rowSpacer {
		t.Fatalf("rows[0] = %+v, want spacer", model.rows[0])
	}

	// Second row should be global section header.
	if model.rows[1].Kind != rowSection || model.rows[1].Label != "global" {
		t.Fatalf("rows[1] = %+v, want section 'global'", model.rows[1])
	}

	// Knowledge section at index 5.
	if model.rows[5].Kind != rowSection || model.rows[5].Label != "knowledge" {
		t.Fatalf("rows[5] = %+v, want section 'knowledge'", model.rows[5])
	}
}

func TestModel_DisplayOrder(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	// Add standalone agents in reverse canonical order.
	pushAgentActivity(model, "tester", "tester")
	pushAgentActivity(model, "inspector", "inspector")
	pushAgentActivity(model, "orchestrator", "orchestrator")
	pushAgentActivity(model, "architect", "architect")
	pushAgentActivity(model, "guide", "guide")

	model.ensureRows()

	// Expected canonical order: guide, architect, orchestrator, inspector, tester.
	want := []string{"guide", "architect", "orchestrator", "inspector", "tester"}
	var got []string
	for _, row := range model.rows {
		if row.Kind == rowAgent {
			got = append(got, row.ID)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("agent count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row[%d] = %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}
}

func TestModel_MoveSelectionSkipsSections(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "librarian", "librarian")

	model.ensureRows()

	// Rows: [spacer, section "global", guide, spacer, section "knowledge", librarian]
	// Selection starts at 0 (spacer). First CycleNext should skip spacer+section → guide.
	model.selected = 0
	model.moveSelection(1)

	// Should skip spacer and section, land on guide (index 2).
	if model.selected != 2 {
		t.Fatalf("moveSelection(1) from spacer = %d, want 2", model.selected)
	}

	// Move forward should skip spacer + knowledge section → librarian.
	model.moveSelection(1)
	if model.selected != 5 {
		t.Fatalf("moveSelection(1) from guide = %d, want 5", model.selected)
	}
}

func TestModel_HandlePipelineState(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "pl-1",
		TaskID:     "task-abc",
		Status:     "write_test",
		WorkerType: "engineer",
		LoopCount:  1,
		MaxLoops:   5,
	})

	if len(model.pipelines) != 1 {
		t.Fatalf("pipelines count = %d, want 1", len(model.pipelines))
	}

	pl := model.pipelines["pl-1"]
	if pl.TaskID != "task-abc" {
		t.Fatalf("pipeline TaskID = %q, want task-abc", pl.TaskID)
	}
	if pl.Status != "write_test" {
		t.Fatalf("pipeline Status = %q, want write_test", pl.Status)
	}

	if !model.rowsDirty {
		t.Fatal("rowsDirty should be true after pipeline state update")
	}
}

func TestModel_HandleVariantState(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	// Create pipeline first.
	model.Update(msg.PipelineStateMsg{
		PipelineID: "pl-1",
		TaskID:     "task-abc",
		Status:     "implement",
	})

	model.Update(msg.VariantStateMsg{
		VariantID:  "var-a8f2",
		PipelineID: "pl-1",
		Name:       "explore",
		State:      "active",
		Message:    "exploring alternatives",
	})

	if len(model.variants) != 1 {
		t.Fatalf("variants count = %d, want 1", len(model.variants))
	}

	v := model.variants["var-a8f2"]
	if v.State != "active" {
		t.Fatalf("variant State = %q, want active", v.State)
	}
}

func TestModel_VariantBound(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "pl-1",
		TaskID:     "task",
		Status:     "write_test",
	})

	// Add maxVariantsPerPipeline variants.
	for i := range maxVariantsPerPipeline {
		model.Update(msg.VariantStateMsg{
			VariantID:  "var-" + string(rune('a'+i)),
			PipelineID: "pl-1",
			State:      "active",
		})
	}

	// Next one should be rejected.
	model.Update(msg.VariantStateMsg{
		VariantID:  "var-overflow",
		PipelineID: "pl-1",
		State:      "active",
	})

	if len(model.variants) != maxVariantsPerPipeline {
		t.Fatalf("variants count = %d, want %d", len(model.variants), maxVariantsPerPipeline)
	}
}

func TestModel_NeedsDecorTick(t *testing.T) {
	model := New(theme.DefaultDark())

	// No pipelines → no decor tick needed.
	if model.NeedsDecorTick() {
		t.Fatal("NeedsDecorTick() = true with no pipelines")
	}

	model.Update(msg.PipelineStateMsg{
		PipelineID: "pl-1",
		TaskID:     "task",
		Status:     "implement",
	})

	if !model.NeedsDecorTick() {
		t.Fatal("NeedsDecorTick() = false with active pipeline")
	}

	// Terminal status.
	model.Update(msg.PipelineStateMsg{
		PipelineID: "pl-1",
		TaskID:     "task",
		Status:     "completed",
	})

	if model.NeedsDecorTick() {
		t.Fatal("NeedsDecorTick() = true with completed pipeline")
	}
}

func TestModel_IsTerminalPipelineStatus(t *testing.T) {
	terminals := []string{"completed", "failed", "cancelled"}
	for _, s := range terminals {
		if !isTerminalPipelineStatus(s) {
			t.Errorf("isTerminalPipelineStatus(%q) = false, want true", s)
		}
	}

	nonTerminals := []string{"write_test", "implement", "validate", ""}
	for _, s := range nonTerminals {
		if isTerminalPipelineStatus(s) {
			t.Errorf("isTerminalPipelineStatus(%q) = true, want false", s)
		}
	}
}

func TestModel_PipelineRowsLayout(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	// Add pipeline.
	model.Update(msg.PipelineStateMsg{
		PipelineID: "pl-1",
		TaskID:     "task-abc",
		Status:     "implement",
		LoopCount:  2,
		MaxLoops:   5,
	})

	// Add pipeline agent (engineer with pipeline_id).
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_eng",
			EventType: events.EventTypeLLMRequest,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "implementing",
			Data: map[string]any{
				"agent_name":  "engineer",
				"agent_type":  "engineer",
				"pipeline_id": "pl-1",
			},
		},
	})

	// Add standalone agent.
	pushAgentActivity(model, "guide", "guide")

	// Add variant.
	model.Update(msg.VariantStateMsg{
		VariantID:  "var-a8f2",
		PipelineID: "pl-1",
		State:      "active",
		Message:    "exploring",
	})

	model.ensureRows()

	// Expected rows:
	// 0: spacer
	// 1: global section
	// 2: guide (standalone)
	// 3: spacer
	// 4: pl-1 pipeline header
	// 5: eng-1 agent
	// 6: var-a8f2 variant
	if len(model.rows) < 7 {
		t.Fatalf("rows count = %d, want >= 7", len(model.rows))
	}

	if model.rows[3].Kind != rowSpacer {
		t.Fatalf("rows[3] = %+v, want spacer", model.rows[3])
	}

	if model.rows[4].Kind != rowPipeline || model.rows[4].ID != "pl-1" {
		t.Fatalf("rows[4] = %+v, want pipeline pl-1", model.rows[4])
	}

	if model.rows[5].Kind != rowAgent || model.rows[5].ID != "eng-1" {
		t.Fatalf("rows[5] = %+v, want agent eng-1", model.rows[5])
	}

	if model.rows[6].Kind != rowVariant || model.rows[6].ID != "var-a8f2" {
		t.Fatalf("rows[6] = %+v, want variant var-a8f2", model.rows[6])
	}
}

func TestModel_RenderCard_WithPrefix(t *testing.T) {
	th := theme.DefaultDark()
	agent := AgentState{
		ID:        "test",
		Name:      "test",
		AgentType: "guide",
		Status:    StatusIdle,
	}

	// Without prefix.
	card := RenderCard(agent, 60, th, false, false, "", AnimState{})
	if card == "" {
		t.Fatal("RenderCard with empty prefix returned empty string")
	}

	// With prefix.
	cardWithPrefix := RenderCard(agent, 60, th, false, false, " │ ", AnimState{})
	if cardWithPrefix == "" {
		t.Fatal("RenderCard with prefix returned empty string")
	}
}

func TestModel_HasActiveAgent(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	// No agents → not active.
	if model.HasActiveAgent() {
		t.Fatal("HasActiveAgent() = true with no agents")
	}

	// LLMRequest sets agent to Thinking (active).
	pushAgentActivity(model, "guide", "guide")
	if !model.HasActiveAgent() {
		t.Fatal("HasActiveAgent() = false after LLMRequest event")
	}

	// ToolResult sets agent to Idle (not active).
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_guide_idle",
			EventType: events.EventTypeToolResult,
			Timestamp: time.Now(),
			AgentID:   "guide",
			Content:   "done",
			Data:      map[string]any{"agent_name": "guide", "agent_type": "guide"},
		},
	})
	if model.HasActiveAgent() {
		t.Fatal("HasActiveAgent() = true after ToolResult (should be idle)")
	}
}

func TestModel_NeedsDecorTick_RequiresActiveAgentOrAgents(t *testing.T) {
	model := New(theme.DefaultDark())

	// No agents, no pipelines → no decor tick.
	if model.NeedsDecorTick() {
		t.Fatal("NeedsDecorTick() = true with nothing")
	}

	// Add idle agent → needs decor tick (idle shimmer still animates).
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_idle",
			EventType: events.EventTypeToolResult,
			Timestamp: time.Now(),
			AgentID:   "guide",
			Content:   "idle",
			Data:      map[string]any{"agent_name": "guide", "agent_type": "guide"},
		},
	})
	if !model.NeedsDecorTick() {
		t.Fatal("NeedsDecorTick() = false with idle agent (idle shimmer still needed)")
	}

	// Active agent also needs decor tick.
	pushAgentActivity(model, "architect", "architect")
	if !model.NeedsDecorTick() {
		t.Fatal("NeedsDecorTick() = false with active agent")
	}
}

func TestModel_SeedAgent(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	model.SeedAgent("architect-001", "architect", "Architect", nil, "", "")
	model.SeedAgent("inspector-001", "inspector", "Inspector", nil, "", "")
	model.SeedAgent("tester-001", "tester", "Tester", nil, "", "")

	// All three should be present and idle.
	if len(model.agents) != 3 {
		t.Fatalf("agents count = %d, want 3", len(model.agents))
	}
	for _, id := range []string{"architect-001", "inspector-001", "tester-001"} {
		agent := model.agents[id]
		if agent == nil {
			t.Fatalf("agent %q not found", id)
		}
		if agent.Status != StatusIdle {
			t.Fatalf("agent %q status = %v, want Idle", id, agent.Status)
		}
	}

	// Rows should contain a spacer + global section + spacer + 3 agents = 6.
	model.ensureRows()
	if len(model.rows) != 5 {
		t.Fatalf("rows count = %d, want 5", len(model.rows))
	}

	// Duplicate seed is a no-op.
	model.SeedAgent("architect-001", "architect", "Architect", nil, "", "")
	if len(model.agents) != 3 {
		t.Fatalf("duplicate seed changed count: %d, want 3", len(model.agents))
	}

	// Activity event on a seeded agent should update it, not create a duplicate.
	pushAgentActivity(model, "architect-001", "architect")
	if len(model.agents) != 3 {
		t.Fatalf("activity on seeded agent changed count: %d, want 3", len(model.agents))
	}
	if model.agents["architect-001"].Status != StatusThinking {
		t.Fatalf("status after LLMRequest = %v, want Thinking", model.agents["architect-001"].Status)
	}
}

func TestModel_SeedAgent_PromotePlaceholder(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	// Seed with placeholder ID == AgentType (activation failed, no real ID).
	model.SeedAgent("tester", "tester", "Tester", nil, "", "")
	model.SeedAgent("inspector", "inspector", "Inspector", nil, "", "")

	if len(model.agents) != 2 {
		t.Fatalf("agents count = %d, want 2", len(model.agents))
	}

	// Simulate real agent activation: activity event arrives with UUID-based ID.
	pushAgentActivity(model, "a8f2c1d0", "tester")

	// Should promote the placeholder, not create a duplicate.
	if len(model.agents) != 2 {
		t.Fatalf("agents count after promotion = %d, want 2", len(model.agents))
	}

	// Old placeholder key should be gone; new key should exist.
	if _, exists := model.agents["tester"]; exists {
		t.Fatal("placeholder key 'tester' still present after promotion")
	}
	promoted := model.agents["a8f2c1d0"]
	if promoted == nil {
		t.Fatal("promoted agent 'a8f2c1d0' not found")
	}
	if promoted.Name != "Tester" {
		t.Fatalf("promoted Name = %q, want 'Tester'", promoted.Name)
	}
	if promoted.AgentType != "tester" {
		t.Fatalf("promoted AgentType = %q, want 'tester'", promoted.AgentType)
	}
	if promoted.Category != "standalone" {
		t.Fatalf("promoted Category = %q, want 'standalone'", promoted.Category)
	}

	// Order should contain the new ID, not the old one.
	model.ensureRows()
	foundOld, foundNew := false, false
	for _, id := range model.order {
		if id == "tester" {
			foundOld = true
		}
		if id == "a8f2c1d0" {
			foundNew = true
		}
	}
	if foundOld {
		t.Fatal("order still contains old placeholder ID 'tester'")
	}
	if !foundNew {
		t.Fatal("order missing promoted ID 'a8f2c1d0'")
	}

	// Inspector placeholder should be untouched.
	if model.agents["inspector"] == nil {
		t.Fatal("inspector placeholder was incorrectly removed")
	}
}

func TestModel_RegisteredTransition(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	// Phase 1: AgentRegistered → StatusWaiting.
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_reg",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "Pipeline agent registered: engineer",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "dag-1",
			},
		},
	})

	agent := model.agents["eng-1"]
	if agent == nil {
		t.Fatal("agent eng-1 not created on registration event")
	}
	if agent.Status != StatusWaiting {
		t.Fatalf("status after registration = %v, want StatusWaiting", agent.Status)
	}

	// Phase 2: LLMRequest → StatusThinking (active work begins).
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_llm",
			EventType: events.EventTypeLLMRequest,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "thinking",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "dag-1",
			},
		},
	})

	if agent.Status != StatusThinking {
		t.Fatalf("status after LLMRequest = %v, want StatusThinking", agent.Status)
	}

	// Phase 3: LLMResponse → StatusIdle (work done, never returns to Waiting).
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_resp",
			EventType: events.EventTypeLLMResponse,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "done",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "dag-1",
			},
		},
	})

	if agent.Status != StatusIdle {
		t.Fatalf("status after LLMResponse = %v, want StatusIdle", agent.Status)
	}
}

func pushAgentActivity(model *Model, agentID, agentType string) {
	if model == nil {
		return
	}
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_" + agentID,
			EventType: events.EventTypeLLMRequest,
			Timestamp: time.Now(),
			AgentID:   agentID,
			Content:   "active",
			Data: map[string]any{
				"agent_name":     agentID,
				"agent_type":     agentType,
				"agent_category": agentCategoryByType[agentType],
			},
		},
	})
}
