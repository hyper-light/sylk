package agent

import (
	"fmt"
	"regexp"
	"strings"
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

func TestModel_SelectedTargetAgentID_PrefersRuntimeWorkerID(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetFocused(true)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_pipeline_worker",
			EventType: events.EventTypeLLMRequest,
			Timestamp: time.Now(),
			AgentID:   "worker-1234",
			Content:   "active",
			Data: map[string]any{
				"agent_name":       "Inspector",
				"agent_type":       "inspector-pipeline",
				"pipeline_id":      "task_1",
				"task_id":          "task_1",
				"runtime_agent_id": "worker-1234",
			},
		},
	})

	model.ensureRows()
	for i, row := range model.rows {
		if row.Kind == rowAgent {
			model.selected = i
			break
		}
	}

	if got := model.SelectedAgentID(); got != "task_1:inspector-pipeline" {
		t.Fatalf("SelectedAgentID() = %q, want %q", got, "task_1:inspector-pipeline")
	}
	if got := model.SelectedTargetAgentID(); got != "worker-1234" {
		t.Fatalf("SelectedTargetAgentID() = %q, want %q", got, "worker-1234")
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

func TestModel_RenderListRow_SkipsNilStateEntries(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(48, 10)

	model.agents["ghost-agent"] = nil
	model.pipelines["ghost-pipeline"] = nil
	model.variants["ghost-variant"] = nil

	if got, ok, _ := model.renderListRow(listRow{Kind: rowAgent, ID: "ghost-agent"}, 0, 0, AnimState{}); ok || got != "" {
		t.Fatalf("rowAgent render = %q, ok=%v, want empty,false", got, ok)
	}
	if got, ok, _ := model.renderListRow(listRow{Kind: rowPipeline, ID: "ghost-pipeline"}, 0, 0, AnimState{}); ok || got != "" {
		t.Fatalf("rowPipeline render = %q, ok=%v, want empty,false", got, ok)
	}
	if got, ok, _ := model.renderListRow(listRow{Kind: rowVariant, ID: "ghost-variant"}, 0, 0, AnimState{}); ok || got != "" {
		t.Fatalf("rowVariant render = %q, ok=%v, want empty,false", got, ok)
	}
}

func TestModel_VariantsForPipeline_SkipsNilEntries(t *testing.T) {
	model := New(theme.DefaultDark())
	model.variants["ghost-variant"] = nil
	model.variants["real-variant"] = &VariantState{ID: "real-variant", PipelineID: "task_1"}

	variants := model.variantsForPipeline("task_1")
	if len(variants) != 1 {
		t.Fatalf("variantsForPipeline() len = %d, want 1", len(variants))
	}
	if variants[0] == nil || variants[0].ID != "real-variant" {
		t.Fatalf("variantsForPipeline()[0] = %#v, want real-variant", variants[0])
	}
}

func TestModel_RenderExpandedView_IgnoresNilExpandedEntries(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(48, 10)
	model.view = viewExpanded

	model.expanded = "ghost-pipeline"
	model.pipelines["ghost-pipeline"] = nil
	if got := model.renderExpandedView(); got != "" {
		t.Fatalf("renderExpandedView() for nil pipeline = %q, want empty", got)
	}

	model.expanded = "ghost-variant"
	model.variants["ghost-variant"] = nil
	if got := model.renderExpandedView(); got != "" {
		t.Fatalf("renderExpandedView() for nil variant = %q, want empty", got)
	}

	model.expanded = "ghost-agent"
	model.agents["ghost-agent"] = nil
	if got := model.renderExpandedView(); got != "" {
		t.Fatalf("renderExpandedView() for nil agent = %q, want empty", got)
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

func TestModel_PipelineStateAllowsLaterTaskSlugPromotion(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		Status:     "executing",
	})

	pl := model.pipelines["task_auth_checkout"]
	if pl == nil {
		t.Fatal("expected pipeline state")
	}
	if pl.TaskLabel != "task_auth_checkout" {
		t.Fatalf("pipeline TaskLabel = %q, want task_auth_checkout", pl.TaskLabel)
	}

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_eng_slug",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "implementing",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout",
			},
		},
	})

	if pl.TaskLabel != "auth-checkout" {
		t.Fatalf("pipeline TaskLabel = %q, want auth-checkout", pl.TaskLabel)
	}
}

func TestModel_PipelineActivityAllowsLaterTaskSlugPromotionAfterTaskIDFallback(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_eng_fallback",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "engineer registered",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "task_1",
				"task_id":     "task_1",
			},
		},
	})

	pl := model.pipelines["task_1"]
	if pl == nil {
		t.Fatal("expected pipeline state")
	}
	if pl.TaskLabel != "task_1" {
		t.Fatalf("pipeline TaskLabel = %q, want task_1", pl.TaskLabel)
	}

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_slug_upgrade",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "task_1:inspector-pipeline",
			Content:   "inspecting",
			Data: map[string]any{
				"agent_name":  "Inspector",
				"agent_type":  "inspector-pipeline",
				"pipeline_id": "task_1",
				"task_id":     "task_1",
				"task_slug":   "auth-checkout",
			},
		},
	})

	if pl.TaskLabel != "auth-checkout" {
		t.Fatalf("pipeline TaskLabel = %q, want auth-checkout", pl.TaskLabel)
	}
}

func TestModel_ActivityPipelinePlaceholderUsesCanonicalStatus(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_pipeline_status",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "task_auth_checkout:inspector-pipeline",
			Content:   "Pipeline agent registered: inspector-pipeline",
			Data: map[string]any{
				"agent_name":      "Inspector",
				"agent_type":      "inspector-pipeline",
				"pipeline_id":     "task_auth_checkout",
				"task_id":         "task_auth_checkout",
				"task_slug":       "auth-checkout",
				"pipeline_status": "defining_criteria",
			},
		},
	})

	pl := model.pipelines["task_auth_checkout"]
	if pl == nil {
		t.Fatal("expected pipeline state")
	}
	if pl.Status != "defining_criteria" {
		t.Fatalf("pipeline Status = %q, want defining_criteria", pl.Status)
	}
}

func TestModel_ActivityPipelineStatusUpdatesExistingPipelineOnHandoff(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	initial := time.Now()
	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		Status:     "defining_criteria",
		LoopCount:  1,
		MaxLoops:   5,
		Timestamp:  initial,
	})

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_tester_handoff",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: initial.Add(time.Second),
			AgentID:   "task_auth_checkout:tester-pipeline",
			Content:   "Pipeline agent registered: tester-pipeline",
			Data: map[string]any{
				"agent_name":      "Tester",
				"agent_type":      "tester-pipeline",
				"pipeline_id":     "task_auth_checkout",
				"task_id":         "task_auth_checkout",
				"task_slug":       "auth-checkout",
				"pipeline_status": "creating_tests",
			},
		},
	})

	pl := model.pipelines["task_auth_checkout"]
	if pl == nil {
		t.Fatal("expected pipeline state")
	}
	if pl.Status != "creating_tests" {
		t.Fatalf("pipeline Status = %q, want creating_tests", pl.Status)
	}
	if pl.LoopCount != 1 || pl.MaxLoops != 5 {
		t.Fatalf("loop state = %d/%d, want 1/5", pl.LoopCount, pl.MaxLoops)
	}
}

func TestModel_HandlePipelineStatePreservesLoopCounterAcrossStageHandoff(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	started := time.Now()
	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		Status:     "defining_criteria",
		WorkerType: "inspector-pipeline",
		LoopCount:  2,
		MaxLoops:   5,
		Timestamp:  started,
	})

	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		Status:     "creating_tests",
		WorkerType: "tester-pipeline",
		Timestamp:  started.Add(time.Second),
	})

	pl := model.pipelines["task_auth_checkout"]
	if pl == nil {
		t.Fatal("expected pipeline state")
	}
	if pl.Status != "creating_tests" {
		t.Fatalf("pipeline Status = %q, want creating_tests", pl.Status)
	}
	if pl.WorkerType != "tester-pipeline" {
		t.Fatalf("pipeline WorkerType = %q, want tester-pipeline", pl.WorkerType)
	}
	if pl.LoopCount != 2 || pl.MaxLoops != 5 {
		t.Fatalf("loop state = %d/%d, want 2/5", pl.LoopCount, pl.MaxLoops)
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

func TestModel_NeedsHighFrequencyDecorTick(t *testing.T) {
	model := New(theme.DefaultDark())

	if model.NeedsHighFrequencyDecorTick() {
		t.Fatal("NeedsHighFrequencyDecorTick() = true with no agents or pipelines")
	}

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
	if model.NeedsHighFrequencyDecorTick() {
		t.Fatal("NeedsHighFrequencyDecorTick() = true with only idle agents")
	}

	pushAgentActivity(model, "architect", "architect")
	if !model.NeedsHighFrequencyDecorTick() {
		t.Fatal("NeedsHighFrequencyDecorTick() = false with active agent")
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
		TaskLabel:  "auth-checkout",
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
	// 4: pipelines section
	// 5: pl-1 pipeline header
	// 6: pl-1:engineer agent
	// 7: var-a8f2 variant
	if len(model.rows) < 8 {
		t.Fatalf("rows count = %d, want >= 8", len(model.rows))
	}

	if model.rows[3].Kind != rowSpacer {
		t.Fatalf("rows[3] = %+v, want spacer", model.rows[3])
	}

	if model.rows[4].Kind != rowSection || model.rows[4].Label != "pipelines" {
		t.Fatalf("rows[4] = %+v, want section pipelines", model.rows[4])
	}

	if model.rows[5].Kind != rowPipeline || model.rows[5].ID != "pl-1" {
		t.Fatalf("rows[5] = %+v, want pipeline pl-1", model.rows[5])
	}

	if model.rows[6].Kind != rowAgent || model.rows[6].ID != "pl-1:engineer" {
		t.Fatalf("rows[6] = %+v, want agent pl-1:engineer", model.rows[6])
	}

	if model.rows[7].Kind != rowVariant || model.rows[7].ID != "var-a8f2" {
		t.Fatalf("rows[7] = %+v, want variant var-a8f2", model.rows[7])
	}

	pl := model.pipelines["pl-1"]
	if pl == nil {
		t.Fatal("expected pipeline state for pl-1")
	}
	if pl.TaskLabel != "auth-checkout" {
		t.Fatalf("pipeline TaskLabel = %q, want auth-checkout", pl.TaskLabel)
	}
}

func TestModel_PipelineAgentsAreScopedPerPipeline(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	for _, pipelineID := range []string{"task_auth_checkout", "task_payment_retry"} {
		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "evt_" + pipelineID,
				EventType: events.EventTypeAgentRegistered,
				Timestamp: time.Now(),
				AgentID:   pipelineID + ":engineer",
				Data: map[string]any{
					"agent_name":  "Engineer",
					"agent_type":  "engineer",
					"pipeline_id": pipelineID,
					"task_id":     pipelineID,
					"task_slug":   strings.ReplaceAll(strings.TrimPrefix(pipelineID, "task_"), "_", "-"),
				},
			},
		})
	}

	model.ensureRows()

	if len(model.pipelines) != 2 {
		t.Fatalf("pipeline count = %d, want 2", len(model.pipelines))
	}
	if got := len(model.agents); got != 2 {
		t.Fatalf("agent count = %d, want 2", got)
	}
	if model.findPipelineAgent("engineer", "task_auth_checkout") == nil {
		t.Fatal("expected engineer row for task_auth_checkout")
	}
	if model.findPipelineAgent("engineer", "task_payment_retry") == nil {
		t.Fatal("expected engineer row for task_payment_retry")
	}
}

func TestModel_OrchestratorStaysGlobalEvenWithPipelineMetadata(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	pushAgentActivity(model, "orchestrator", "orchestrator")
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_orch_pipeline",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "orchestrator",
			Content:   "Coordinating pipeline",
			Data: map[string]any{
				"agent_name":  "Orchestrator",
				"agent_type":  "orchestrator",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout",
			},
		},
	})

	model.ensureRows()
	if model.findPipelineAgent("orchestrator", "task_auth_checkout") != nil {
		t.Fatal("did not expect orchestrator pipeline row")
	}
	orch := model.agents["orchestrator"]
	if orch == nil {
		t.Fatal("expected global orchestrator row")
	}
	if orch.PipelineID != "" {
		t.Fatalf("orchestrator PipelineID = %q, want empty", orch.PipelineID)
	}
}

func TestModel_ViewKeepsClosingFooterBelowPipelines(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 8)
	model.SetFocused(true)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "pl-1",
		TaskID:     "task-abc",
		TaskLabel:  "auth-checkout",
		Status:     "implement",
	})
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_eng_footer",
			EventType: events.EventTypeLLMRequest,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "implementing",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "pl-1",
			},
		},
	})
	pushAgentActivity(model, "guide", "guide")

	lines := strings.Split(model.View(), "\n")
	if len(lines) != 8 {
		t.Fatalf("rendered lines = %d, want 8", len(lines))
	}
	footerLine := -1
	for i, line := range lines {
		if strings.Contains(line, "╰") {
			footerLine = i
			break
		}
	}
	if footerLine < 0 {
		t.Fatalf("expected closing footer in view, got %q", strings.Join(lines, "\n"))
	}
}

func TestModel_ViewPinsFooterToBottomWhenContentIsSparse(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 14)
	model.SetFocused(true)

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "librarian", "librarian")
	pushAgentActivity(model, "academic", "academic")
	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		TaskLabel:  "auth-checkout",
		Status:     "executing",
	})

	lines := strings.Split(stripANSI(model.View()), "\n")
	if len(lines) != 14 {
		t.Fatalf("rendered lines = %d, want 14", len(lines))
	}
	footerLine := -1
	for i, line := range lines {
		if strings.Contains(line, "╰") {
			footerLine = i
			break
		}
	}
	if footerLine < 0 {
		t.Fatalf("expected closing footer in view, got %q", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "academic") {
		t.Fatalf("expected academic agent to remain visible, got %q", strings.Join(lines, "\n"))
	}
}

func TestModel_ViewHeightStaysFixedAsPipelinesAreAdded(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 14)
	model.SetFocused(true)

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "librarian", "librarian")
	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		TaskLabel:  "auth-checkout",
		Status:     "executing",
	})

	lineCount := func(view string) int {
		return len(strings.Split(stripANSI(view), "\n"))
	}

	before := lineCount(model.View())
	if before != 14 {
		t.Fatalf("view line count before adding pipelines = %d, want 14", before)
	}

	for _, slug := range []string{"payment-retry", "cli-packaging"} {
		pipelineID := "task_" + strings.ReplaceAll(slug, "-", "_")
		model.Update(msg.PipelineStateMsg{
			PipelineID: pipelineID,
			TaskID:     pipelineID,
			TaskLabel:  slug,
			Status:     "executing",
		})
	}

	after := lineCount(model.View())
	if after != 14 {
		t.Fatalf("view line count after adding pipelines = %d, want 14", after)
	}
}

func TestModel_ViewPinsFooterToBottomWhenPipelinesOverflow(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(32, 8)
	model.SetFocused(true)

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "librarian", "librarian")

	for _, slug := range []string{"auth-checkout", "payment-retry", "cli-packaging", "search-tuning"} {
		pipelineID := "task_" + strings.ReplaceAll(slug, "-", "_")
		model.Update(msg.PipelineStateMsg{
			PipelineID: pipelineID,
			TaskID:     pipelineID,
			TaskLabel:  slug,
			Status:     "executing",
			LoopCount:  1,
			MaxLoops:   4,
		})
		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "evt_" + pipelineID,
				EventType: events.EventTypeAgentRegistered,
				Timestamp: time.Now(),
				AgentID:   pipelineID + ":engineer",
				Data: map[string]any{
					"agent_name":  "Engineer",
					"agent_type":  "engineer",
					"pipeline_id": pipelineID,
					"task_id":     pipelineID,
					"task_slug":   slug,
				},
			},
		})
	}

	lines := strings.Split(stripANSI(model.View()), "\n")
	if len(lines) != 8 {
		t.Fatalf("rendered lines = %d, want 8", len(lines))
	}
	footerLine := footerLineIndex(lines)
	if footerLine != len(lines)-1 {
		t.Fatalf("footer line = %d, want %d in overflow view:\n%s", footerLine, len(lines)-1, strings.Join(lines, "\n"))
	}
}

func TestModel_PipelineOverflowKeepsKnowledgeVisible(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 12)
	model.SetFocused(true)

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "architect", "architect")
	pushAgentActivity(model, "librarian", "librarian")
	pushAgentActivity(model, "academic", "academic")
	pushAgentActivity(model, "archivalist", "archivalist")

	for _, task := range []string{"auth-checkout", "payment-retry", "cli-packaging"} {
		pipelineID := "task_" + strings.ReplaceAll(task, "-", "_")
		model.Update(msg.PipelineStateMsg{
			PipelineID: pipelineID,
			TaskID:     pipelineID,
			TaskLabel:  task,
			Status:     "executing",
			LoopCount:  1,
			MaxLoops:   4,
		})
		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "evt_" + pipelineID,
				EventType: events.EventTypeAgentRegistered,
				Timestamp: time.Now(),
				AgentID:   pipelineID + ":engineer",
				Data: map[string]any{
					"agent_name":  "Engineer",
					"agent_type":  "engineer",
					"pipeline_id": pipelineID,
					"task_id":     pipelineID,
					"task_slug":   task,
				},
			},
		})
	}

	view := stripANSI(model.View())
	if !strings.Contains(view, "knowledge") {
		t.Fatalf("expected knowledge section to remain visible, got %q", view)
	}
	if !strings.Contains(view, "pipelines") {
		t.Fatalf("expected pipelines section to remain visible, got %q", view)
	}
}

func TestModel_ScrollDownMovesOnlyPipelineViewport(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 12)
	model.SetFocused(true)

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "librarian", "librarian")

	for idx := range 4 {
		pipelineID := "task_pipeline_" + string(rune('a'+idx))
		taskSlug := "pipeline-" + string(rune('a'+idx))
		model.Update(msg.PipelineStateMsg{
			PipelineID: pipelineID,
			TaskID:     pipelineID,
			TaskLabel:  taskSlug,
			Status:     "executing",
			LoopCount:  1,
			MaxLoops:   4,
		})
		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "evt_" + pipelineID,
				EventType: events.EventTypeAgentRegistered,
				Timestamp: time.Now(),
				AgentID:   pipelineID + ":engineer",
				Data: map[string]any{
					"agent_name":  "Engineer",
					"agent_type":  "engineer",
					"pipeline_id": pipelineID,
					"task_id":     pipelineID,
					"task_slug":   taskSlug,
				},
			},
		})
	}

	model.ensureRows()
	selectedBefore := model.selected
	scrollBefore := model.pipelineScroll
	if !model.ScrollDown() {
		t.Fatal("expected pipeline viewport scroll to be consumed")
	}
	if model.selected != selectedBefore {
		t.Fatalf("selection changed during viewport scroll: got %d want %d", model.selected, selectedBefore)
	}
	if model.pipelineScroll <= scrollBefore {
		t.Fatalf("pipelineScroll = %d, want > %d", model.pipelineScroll, scrollBefore)
	}
}

func TestModel_ScrollingOverflowingPipelinesKeepsFooterPinned(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(32, 8)
	model.SetFocused(true)

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "librarian", "librarian")

	for _, slug := range []string{"auth-checkout", "payment-retry", "cli-packaging", "search-tuning"} {
		pipelineID := "task_" + strings.ReplaceAll(slug, "-", "_")
		model.Update(msg.PipelineStateMsg{
			PipelineID: pipelineID,
			TaskID:     pipelineID,
			TaskLabel:  slug,
			Status:     "executing",
			LoopCount:  1,
			MaxLoops:   4,
		})
		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "evt_scroll_" + pipelineID,
				EventType: events.EventTypeAgentRegistered,
				Timestamp: time.Now(),
				AgentID:   pipelineID + ":engineer",
				Data: map[string]any{
					"agent_name":  "Engineer",
					"agent_type":  "engineer",
					"pipeline_id": pipelineID,
					"task_id":     pipelineID,
					"task_slug":   slug,
				},
			},
		})
	}

	seenLastPipeline := false
	for range 16 {
		lines := strings.Split(stripANSI(model.View()), "\n")
		if footerLine := footerLineIndex(lines); footerLine != len(lines)-1 {
			t.Fatalf("footer line = %d, want %d while scrolling:\n%s", footerLine, len(lines)-1, strings.Join(lines, "\n"))
		}
		if strings.Contains(strings.Join(lines, "\n"), "search-tuning") {
			seenLastPipeline = true
			break
		}
		if !model.ScrollDown() {
			break
		}
	}

	if !seenLastPipeline {
		t.Fatalf("expected to scroll the last pipeline into view, final scroll=%d", model.pipelineScroll)
	}
}

func TestModel_SpaceTogglesSelectedPipelineCollapse(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 12)
	model.SetFocused(true)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		TaskLabel:  "auth-checkout",
		Status:     "executing",
	})
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_collapse_engineer",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "task_auth_checkout:engineer",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout",
			},
		},
	})

	model.ensureRows()
	if !model.selectRow(rowPipeline, "task_auth_checkout") {
		t.Fatal("expected pipeline header row selection")
	}
	model.toggleSelectedPipelineCollapse()
	model.ensureRows()

	if !model.collapsedPipelines["task_auth_checkout"] {
		t.Fatal("expected pipeline to be collapsed")
	}
	for _, row := range model.rows {
		if row.Kind == rowAgent && row.PipelineID == "task_auth_checkout" {
			t.Fatalf("unexpected pipeline child row after collapse: %+v", row)
		}
	}

	view := stripANSI(model.View())
	if !strings.Contains(view, "│  + auth-checkout") {
		t.Fatalf("expected collapsed pipeline marker in view, got %q", view)
	}

	model.toggleSelectedPipelineCollapse()
	model.ensureRows()
	if model.collapsedPipelines["task_auth_checkout"] {
		t.Fatal("expected pipeline to be expanded")
	}
	foundChild := false
	for _, row := range model.rows {
		if row.Kind == rowAgent && row.ID == "task_auth_checkout:engineer" {
			foundChild = true
			break
		}
	}
	if !foundChild {
		t.Fatal("expected engineer row after expanding pipeline")
	}
}

func TestModel_ViewKeepsCriticalSectionsPinnedWhenUnfocused(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 12)
	model.SetFocused(false)

	pushAgentActivity(model, "guide", "guide")
	pushAgentActivity(model, "architect", "architect")
	pushAgentActivity(model, "orchestrator", "orchestrator")
	pushAgentActivity(model, "librarian", "librarian")
	pushAgentActivity(model, "academic", "academic")
	pushAgentActivity(model, "archivalist", "archivalist")

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_pipeline_worker",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "task_auth_checkout:inspector-pipeline",
			Content:   "Processing pipeline task: task_auth_checkout:inspect",
			Data: map[string]any{
				"agent_name":  "Inspector",
				"agent_type":  "inspector-pipeline",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout",
			},
		},
	})
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_pipeline_engineer",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "task_auth_checkout:engineer",
			Content:   "Pipeline agent registered: engineer",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout",
			},
		},
	})

	view := stripANSI(model.View())
	if !strings.Contains(view, "knowledge") {
		t.Fatalf("expected knowledge section in view, got %q", view)
	}
	if !strings.Contains(view, "pipelines") {
		t.Fatalf("expected pipelines section in view when height allows reserved viewport, got %q", view)
	}
	if !strings.Contains(view, "Inspector") && !strings.Contains(view, "Engineer") && !strings.Contains(view, "auth-checkout") {
		t.Fatalf("expected some pipeline content in reserved viewport, got %q", view)
	}
}

func TestModel_ViewDoesNotAutoFollowActivePipelinesWhenUnfocused(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(32, 8)
	model.SetFocused(false)

	for _, slug := range []string{"pipeline-a", "pipeline-b", "pipeline-c", "pipeline-d"} {
		pipelineID := "task_" + strings.ReplaceAll(slug, "-", "_")
		model.Update(msg.PipelineStateMsg{
			PipelineID: pipelineID,
			TaskID:     pipelineID,
			TaskLabel:  slug,
			Status:     "executing",
			LoopCount:  1,
			MaxLoops:   4,
		})
		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "evt_" + pipelineID,
				EventType: events.EventTypeAgentRegistered,
				Timestamp: time.Now(),
				AgentID:   pipelineID + ":engineer",
				Data: map[string]any{
					"agent_name":  "Engineer",
					"agent_type":  "engineer",
					"pipeline_id": pipelineID,
					"task_id":     pipelineID,
					"task_slug":   slug,
				},
			},
		})
	}

	view := stripANSI(model.View())
	if !strings.Contains(view, "pipeline-a") {
		t.Fatalf("expected unfocused view to preserve the top of the pipeline list, got %q", view)
	}
	if model.pipelineScroll != 0 {
		t.Fatalf("pipelineScroll = %d, want 0", model.pipelineScroll)
	}
}

func TestModel_PipelineViewportLayoutCountsWrappedHeaderLines(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(26, 8)
	model.SetFocused(true)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		TaskLabel:  "auth-checkout-extremely-long-title",
		Status:     "executing",
		LoopCount:  3,
		MaxLoops:   12,
	})
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_wrapped_pipeline",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "task_auth_checkout:engineer",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout-extremely-long-title",
			},
		},
	})

	layout, ok := model.pipelineViewportLayout()
	if !ok {
		t.Fatal("expected pipeline viewport layout")
	}
	if len(layout.rowHeights) < 2 {
		t.Fatalf("rowHeights len = %d, want at least 2", len(layout.rowHeights))
	}
	if layout.rowHeights[0] != 2 {
		t.Fatalf("pipeline header height = %d, want 2", layout.rowHeights[0])
	}
	if layout.rowHeights[1] != 1 {
		t.Fatalf("pipeline member height = %d, want 1", layout.rowHeights[1])
	}
	if layout.totalLines < 3 {
		t.Fatalf("totalLines = %d, want at least 3", layout.totalLines)
	}
}

func TestModel_HandleListClickTogglesPipelineHeader(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 12)
	model.SetFocused(true)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_payment_retry",
		TaskID:     "task_payment_retry",
		TaskLabel:  "payment-retry",
		Status:     "executing",
	})
	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_click_engineer",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "task_payment_retry:engineer",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "task_payment_retry",
				"task_id":     "task_payment_retry",
				"task_slug":   "payment-retry",
			},
		},
	})

	view := model.View()
	if view == "" {
		t.Fatal("expected rendered view")
	}

	rowLine := -1
	for lineIdx, rowIdx := range model.lineRowMap {
		if rowIdx >= 0 && rowIdx < len(model.rows) {
			row := model.rows[rowIdx]
			if row.Kind == rowPipeline && row.ID == "task_payment_retry" {
				rowLine = lineIdx
				break
			}
		}
	}
	if rowLine < 0 {
		t.Fatal("expected rendered pipeline header line")
	}

	model.HandleListClick(rowLine)
	if !model.collapsedPipelines["task_payment_retry"] {
		t.Fatal("expected click to collapse pipeline")
	}
	model.View()
	model.HandleListClick(rowLine)
	if model.collapsedPipelines["task_payment_retry"] {
		t.Fatal("expected second click to expand pipeline")
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func footerLineIndex(lines []string) int {
	for i, line := range lines {
		if strings.Contains(line, "╰") {
			return i
		}
	}
	return -1
}

func TestModel_PipelineActivityUsesCanonicalPipelineRowID(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_designer_pipeline",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "designer",
			Content:   "Processing design task",
			Data: map[string]any{
				"agent_name":  "Designer",
				"agent_type":  "designer",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout",
			},
		},
	})

	if _, exists := model.agents["designer"]; exists {
		t.Fatal("unexpected standalone designer row")
	}
	agent := model.agents["task_auth_checkout:designer"]
	if agent == nil {
		t.Fatal("expected canonical pipeline designer row")
	}
	if agent.PipelineID != "task_auth_checkout" {
		t.Fatalf("pipeline id = %q, want task_auth_checkout", agent.PipelineID)
	}
}

func TestModel_TaskIDOverridesRuntimePipelineIDForPipelineAgents(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_designer_runtime_pipeline",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "designer",
			Content:   "Processing design task",
			Data: map[string]any{
				"agent_name":  "Designer",
				"agent_type":  "designer",
				"pipeline_id": "runtime-pipeline-123",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout",
			},
		},
	})

	agent := model.agents["task_auth_checkout:designer"]
	if agent == nil {
		t.Fatal("expected canonical pipeline designer row")
	}
	if agent.PipelineID != "task_auth_checkout" {
		t.Fatalf("pipeline id = %q, want task_auth_checkout", agent.PipelineID)
	}
	if _, exists := model.agents["runtime-pipeline-123:designer"]; exists {
		t.Fatal("unexpected runtime pipeline keyed designer row")
	}
}

func TestModel_PipelineAliasAbsorbsMetadataPoorFollowOnEvents(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_pipeline_start",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "inspector-pipeline",
			Content:   "Validating implementation quality",
			Data: map[string]any{
				"agent_name":  "Inspector",
				"agent_type":  "inspector-pipeline",
				"pipeline_id": "task_payment_retry",
				"task_id":     "task_payment_retry",
				"task_slug":   "payment-retry",
			},
		},
	})

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_llm_follow_on",
			EventType: events.EventTypeLLMRequest,
			Timestamp: time.Now(),
			AgentID:   "inspector-pipeline",
			Content:   "thinking",
			Data: map[string]any{
				"model": "claude-opus-4-6",
			},
		},
	})

	if got := len(model.agents); got != 1 {
		t.Fatalf("agent count = %d, want 1", got)
	}
	agent := model.agents["task_payment_retry:inspector-pipeline"]
	if agent == nil {
		t.Fatal("expected canonical pipeline inspector row")
	}
	if agent.Status != StatusThinking {
		t.Fatalf("status = %v, want StatusThinking", agent.Status)
	}
}

func TestModel_PipelineMetadataRehomesEarlierBadRuntimeRow(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_bad_first",
			EventType: events.EventTypeLLMRequest,
			Timestamp: time.Now(),
			AgentID:   "designer",
			Content:   "thinking",
			Data: map[string]any{
				"model": "gemini-3.1-pro-preview",
			},
		},
	})

	if model.agents["designer"] != nil {
		t.Fatal("unexpected ambiguous global designer row")
	}

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_fixup",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "designer",
			Content:   "Processing design task",
			Data: map[string]any{
				"agent_name":  "Designer",
				"agent_type":  "designer",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
				"task_slug":   "auth-checkout",
			},
		},
	})

	if _, exists := model.agents["designer"]; exists {
		t.Fatal("unexpected ambiguous global designer row after fixup")
	}
	if model.agents["task_auth_checkout:designer"] == nil {
		t.Fatal("expected canonical pipeline row after rehome")
	}
}

func TestModel_PipelineWorkerFallbackAvoidsGlobalDuplicatesAfterEngineerFailure(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		TaskLabel:  "auth-checkout",
		Status:     "executing",
	})

	for _, agentType := range []string{"engineer", "inspector-pipeline", "tester-pipeline"} {
		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "seed_" + agentType,
				EventType: events.EventTypeAgentRegistered,
				Timestamp: time.Now(),
				AgentID:   "task_auth_checkout:" + agentType,
				Content:   "Pipeline agent registered",
				Data: map[string]any{
					"agent_name":  agentType,
					"agent_type":  agentType,
					"pipeline_id": "task_auth_checkout",
					"task_id":     "task_auth_checkout",
					"task_slug":   "auth-checkout",
				},
			},
		})
	}

	for _, tc := range []struct {
		agentType string
		eventType events.EventType
		content   string
	}{
		{agentType: "engineer", eventType: events.EventTypeAgentError, content: "Task failed: tool calls failed 2 consecutive turns"},
		{agentType: "inspector-pipeline", eventType: events.EventTypeAgentRegistered, content: "Pipeline agent registered"},
		{agentType: "tester-pipeline", eventType: events.EventTypeAgentRegistered, content: "Pipeline agent registered"},
	} {
		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "fallback_" + tc.agentType,
				EventType: tc.eventType,
				Timestamp: time.Now(),
				AgentID:   tc.agentType,
				Content:   tc.content,
				Data:      map[string]any{},
			},
		})
	}

	if got := len(model.agents); got != 3 {
		t.Fatalf("agent count = %d, want 3", got)
	}
	for _, agentID := range []string{"engineer", "inspector-pipeline", "tester-pipeline"} {
		if model.agents[agentID] != nil {
			t.Fatalf("unexpected global fallback row %q", agentID)
		}
	}
	for _, agentType := range []string{"engineer", "inspector-pipeline", "tester-pipeline"} {
		canonicalID := "task_auth_checkout:" + agentType
		agent := model.agents[canonicalID]
		if agent == nil {
			t.Fatalf("expected canonical pipeline row %q", canonicalID)
		}
		if agent.PipelineID != "task_auth_checkout" {
			t.Fatalf("%s PipelineID = %q, want task_auth_checkout", canonicalID, agent.PipelineID)
		}
	}

	model.ensureRows()
	for _, row := range model.rows {
		if row.Kind == rowSection && row.Label == "global" {
			t.Fatal("did not expect a global section after fallback pipeline worker events")
		}
	}
}

func TestModel_DropsAmbiguousPipelineActivityWithoutCreatingGlobalGhost(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		TaskLabel:  "auth-checkout",
		Status:     "executing",
	})
	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_profile",
		TaskID:     "task_auth_profile",
		TaskLabel:  "auth-profile",
		Status:     "executing",
	})

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "ambiguous_engineer",
			EventType: events.EventTypeAgentError,
			Timestamp: time.Now(),
			AgentID:   "engineer",
			Content:   "Task failed: tool calls failed 2 consecutive turns",
			Data: map[string]any{
				"agent_type": "engineer",
			},
		},
	})

	if model.agents["engineer"] != nil {
		t.Fatal("unexpected global engineer ghost row")
	}
	if got := len(model.agents); got != 0 {
		t.Fatalf("agent count = %d, want 0", got)
	}
}

func TestModel_StreamProgressResolvesRuntimeAliasToPipelineRow(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_wrapped",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "task_auth_checkout:engineer",
			Content:   "Processing implementation task",
			Data: map[string]any{
				"agent_name":       "Engineer",
				"agent_type":       "engineer",
				"pipeline_id":      "task_auth_checkout",
				"task_id":          "task_auth_checkout",
				"task_slug":        "auth-checkout",
				"runtime_agent_id": "engineer",
			},
		},
	})

	_, _ = model.Update(msg.StreamProgressMsg{
		AgentID:    "engineer",
		Message:    "Applying patch",
		Visibility: events.VisibilityUser,
	})

	agent := model.agents["task_auth_checkout:engineer"]
	if agent == nil {
		t.Fatal("expected canonical pipeline engineer row")
	}
	if agent.Status != StatusThinking {
		t.Fatalf("status = %v, want StatusThinking", agent.Status)
	}
	if agent.TaskSummary != "Applying patch" {
		t.Fatalf("task summary = %q, want Applying patch", agent.TaskSummary)
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

func TestModel_StreamProgressCanonicalizesTaskScopedPipelineRuntimeIDs(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	for _, tc := range []struct {
		agentType string
		message   string
	}{
		{agentType: "engineer", message: "Applying patch"},
		{agentType: "designer", message: "Refining interaction flow"},
		{agentType: "inspector-pipeline", message: "Evaluating acceptance criteria"},
		{agentType: "tester-pipeline", message: "Running regression suite"},
	} {
		canonicalID := "task_auth_checkout:" + tc.agentType
		runtimeID := "task_auth_checkout__" + tc.agentType

		_, _ = model.Update(msg.ActivityEventMsg{
			Event: &events.ActivityEvent{
				ID:        "evt_" + tc.agentType,
				EventType: events.EventTypeAgentRegistered,
				Timestamp: time.Now(),
				AgentID:   canonicalID,
				Content:   "Pipeline agent registered",
				Data: map[string]any{
					"agent_name":  tc.agentType,
					"agent_type":  tc.agentType,
					"pipeline_id": "task_auth_checkout",
					"task_id":     "task_auth_checkout",
					"task_slug":   "auth-checkout",
				},
			},
		})

		_, _ = model.Update(msg.StreamProgressMsg{
			AgentID:    runtimeID,
			AgentType:  tc.agentType,
			PipelineID: "task_auth_checkout",
			TaskID:     "task_auth_checkout",
			Message:    tc.message,
			Visibility: events.VisibilityAgent,
		})

		agent := model.agents[canonicalID]
		if agent == nil {
			t.Fatalf("expected canonical pipeline row for %s", tc.agentType)
		}
		if agent.Status != StatusThinking {
			t.Fatalf("%s status = %v, want StatusThinking", tc.agentType, agent.Status)
		}
		if agent.TaskSummary != tc.message {
			t.Fatalf("%s task summary = %q, want %q", tc.agentType, agent.TaskSummary, tc.message)
		}
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

func TestModel_AdvanceDecorCoalescesIdlePhase(t *testing.T) {
	model := New(theme.DefaultDark())

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

	base := model.shimmerStart.Add(2 * time.Second)
	if !model.AdvanceDecor(base) {
		t.Fatal("AdvanceDecor() = false for first idle bucket")
	}
	if model.AdvanceDecor(base.Add(200 * time.Millisecond)) {
		t.Fatal("AdvanceDecor() = true within same idle bucket")
	}
	if !model.AdvanceDecor(base.Add(idleDecorPhaseStep)) {
		t.Fatal("AdvanceDecor() = false after idle bucket boundary")
	}
}

func TestModel_AdvanceDecorKeepsActiveDotsAnimatingWhilePipelinesRun(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	pushAgentActivity(model, "architect", "architect")
	model.Update(msg.PipelineStateMsg{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		TaskLabel:  "auth-checkout",
		Status:     "executing",
	})

	before := model.dotFrame
	if !model.AdvanceDecor(model.shimmerStart.Add(100 * time.Millisecond)) {
		t.Fatal("AdvanceDecor() = false with an active agent and active pipeline")
	}
	if model.dotFrame == before {
		t.Fatalf("dotFrame = %d, want it to advance beyond %d while pipelines are active", model.dotFrame, before)
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

func TestModel_AllowsPipelineAgentsBeyondInitialAgentCapacity(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	for i := 0; i < initialAgentCapacity; i++ {
		id := fmt.Sprintf("seed-%02d", i)
		model.SeedAgent(id, id, id, nil, "", "")
	}

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_pipeline_overflow_engineer",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "task_overflow:engineer",
			Content:   "Pipeline agent registered: engineer",
			Data: map[string]any{
				"agent_name":      "Engineer",
				"agent_type":      "engineer",
				"pipeline_id":     "task_overflow",
				"task_id":         "task_overflow",
				"task_slug":       "overflow",
				"pipeline_status": "executing",
			},
		},
	})

	agent := model.agents["task_overflow:engineer"]
	if agent == nil {
		t.Fatal("expected pipeline engineer to be added after initial agent capacity")
	}
	if agent.PipelineID != "task_overflow" {
		t.Fatalf("agent PipelineID = %q, want task_overflow", agent.PipelineID)
	}
	if got := len(model.agents); got != initialAgentCapacity+1 {
		t.Fatalf("agents count = %d, want %d", got, initialAgentCapacity+1)
	}

	pl := model.pipelines["task_overflow"]
	if pl == nil {
		t.Fatal("expected overflow pipeline state to be created")
	}
	found := false
	for _, member := range pl.Members {
		if member == "task_overflow:engineer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected overflow pipeline members to include task_overflow:engineer, got %v", pl.Members)
	}
}

func TestModel_AllowsPipelinesBeyondInitialPipelineCapacity(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	for i := 0; i < initialPipelineCapacity+1; i++ {
		pipelineID := fmt.Sprintf("task_%02d", i)
		model.Update(msg.PipelineStateMsg{
			PipelineID: pipelineID,
			TaskID:     pipelineID,
			TaskLabel:  fmt.Sprintf("task-%02d", i),
			Status:     "executing",
		})
	}

	if got := len(model.pipelines); got != initialPipelineCapacity+1 {
		t.Fatalf("pipeline count = %d, want %d", got, initialPipelineCapacity+1)
	}
	if model.pipelines["task_08"] == nil {
		t.Fatal("expected pipeline beyond initial capacity to remain visible")
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

	agent := model.agents["dag-1:engineer"]
	if agent == nil {
		t.Fatal("agent dag-1:engineer not created on registration event")
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

func TestModel_HandoffActivityUpdatesStatusAndContextUsage(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_reg_handoff",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "4d6b407a",
			Content:   "Pipeline agent registered: tester-pipeline",
			Data: map[string]any{
				"agent_name":  "Pipeline Tester",
				"agent_type":  "tester-pipeline",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
			},
		},
	})

	agent := model.agents["task_auth_checkout:tester-pipeline"]
	if agent == nil {
		t.Fatal("expected pipeline tester row to be created")
	}

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_handoff_triggered",
			EventType: events.EventTypeAgentDecision,
			Timestamp: time.Now(),
			AgentID:   "4d6b407a",
			Content:   "Context handoff triggered",
			Data: map[string]any{
				"agent_name":    "Pipeline Tester",
				"agent_type":    "tester-pipeline",
				"pipeline_id":   "task_auth_checkout",
				"task_id":       "task_auth_checkout",
				"handoff_state": "triggered",
				"context_usage": 0.91,
			},
		},
	})

	if agent.Status != StatusHandoff {
		t.Fatalf("status after handoff trigger = %v, want StatusHandoff", agent.Status)
	}
	if agent.ContextUsage != 0.91 {
		t.Fatalf("context usage after handoff trigger = %.2f, want 0.91", agent.ContextUsage)
	}

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_handoff_complete",
			EventType: events.EventTypeSuccess,
			Timestamp: time.Now(),
			AgentID:   "4d6b407a",
			Content:   "Context handoff complete",
			Data: map[string]any{
				"agent_name":     "Pipeline Tester",
				"agent_type":     "tester-pipeline",
				"pipeline_id":    "task_auth_checkout",
				"task_id":        "task_auth_checkout",
				"handoff_state":  "completed",
				"context_tokens": 0,
			},
		},
	})

	if agent.Status != StatusWaiting {
		t.Fatalf("status after handoff completion = %v, want StatusWaiting", agent.Status)
	}
	if agent.ContextUsage != 0 {
		t.Fatalf("context usage after handoff completion = %.2f, want 0", agent.ContextUsage)
	}
}

func TestModel_GenericActivityDoesNotOverrideContextUsage(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_reg_generic_ctx",
			EventType: events.EventTypeAgentRegistered,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "Engineer registered",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "task_auth_checkout",
				"task_id":     "task_auth_checkout",
			},
		},
	})

	agent := model.agents["task_auth_checkout:engineer"]
	if agent == nil {
		t.Fatal("expected pipeline engineer row to be created")
	}
	agent.ContextUsage = 0.18

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_llm_generic_ctx",
			EventType: events.EventTypeLLMResponse,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "done",
			Data: map[string]any{
				"agent_name":    "Engineer",
				"agent_type":    "engineer",
				"pipeline_id":   "task_auth_checkout",
				"task_id":       "task_auth_checkout",
				"context_usage": 0.91,
			},
		},
	})

	if agent.ContextUsage != 0.18 {
		t.Fatalf("context usage after generic activity = %.2f, want unchanged 0.18", agent.ContextUsage)
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
