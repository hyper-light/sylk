package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestRenderProgressBar_UsesUniformAnimatedSquares(t *testing.T) {
	th := theme.DefaultDark()
	grad := th.Palette.PipelineGradient()

	pending := renderProgressBar("pending", 0, grad, th)
	executing := renderProgressBar("executing", 0, grad, th)

	wantGlyphs := strings.Repeat(pipelineProgressGlyph, progressBarCells)
	if got := stripANSI(pending); got != wantGlyphs {
		t.Fatalf("pending progress bar glyphs = %q, want %q", got, wantGlyphs)
	}
	if got := stripANSI(executing); got != wantGlyphs {
		t.Fatalf("executing progress bar glyphs = %q, want %q", got, wantGlyphs)
	}
}

func TestFormatPipelineCounterLabel_RendersValidatedClaimsOverTotal(t *testing.T) {
	tests := []struct {
		name string
		pl   *PipelineState
		want string
	}{
		{name: "nil pipeline", pl: nil, want: "0/0"},
		{name: "no claims yet", pl: &PipelineState{Status: "defining_criteria"}, want: "0/0"},
		{name: "no claims yet ignores loops", pl: &PipelineState{Status: "executing", LoopCount: 3, MaxLoops: 4}, want: "0/0"},
		{name: "no claims yet ignores phase", pl: &PipelineState{Status: "validating"}, want: "0/0"},
		{name: "partial validation", pl: &PipelineState{ClaimsAccepted: 3, ClaimsTotalCount: 5, Status: "executing"}, want: "3/5"},
		{name: "all validated", pl: &PipelineState{ClaimsAccepted: 5, ClaimsTotalCount: 5, Status: "completed"}, want: "5/5"},
		{name: "negative inputs clamp to zero", pl: &PipelineState{ClaimsAccepted: -1, ClaimsTotalCount: -1}, want: "0/0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPipelineCounterLabel(tt.pl); got != tt.want {
				t.Fatalf("formatPipelineCounterLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderPipelineRow_KeepsProgressOnSingleLineWithRightAlignment(t *testing.T) {
	th := theme.DefaultDark()
	grad := th.Palette.PipelineGradient()
	pl := &PipelineState{
		ID:               "task_auth_checkout",
		TaskLabel:        "auth-checkout-extremely-long-title",
		Status:           "executing",
		LoopCount:        3,
		MaxLoops:         12,
		ClaimsAccepted:   3,
		ClaimsTotalCount: 12,
	}

	width := 40
	rendered := stripANSI(renderPipelineRow(pl, width, 0, grad, th, false, "", AnimState{}, false))
	if strings.Contains(rendered, "\n") {
		t.Fatalf("expected single-line pipeline row, got %q", rendered)
	}
	if !strings.HasPrefix(rendered, pipelineHeaderPrefix(false)) {
		t.Fatalf("rendered = %q, want prefix %q", rendered, pipelineHeaderPrefix(false))
	}
	bar := strings.Repeat(pipelineProgressGlyph, progressBarCells)
	if !strings.Contains(rendered, bar) {
		t.Fatalf("rendered = %q, want progress bar %q", rendered, bar)
	}
	suffix := bar + " 3/12"
	if !strings.HasSuffix(rendered, suffix) {
		t.Fatalf("rendered = %q, want right-aligned suffix %q", rendered, suffix)
	}
	// Title must have been truncated (ellipsis from `truncate`).
	if !strings.Contains(rendered, "\u2026") {
		t.Fatalf("expected task label or status to be truncated with an ellipsis, got %q", rendered)
	}
}

func TestRenderPipelineRow_FitsTitleWhenWidthAllows(t *testing.T) {
	th := theme.DefaultDark()
	grad := th.Palette.PipelineGradient()
	pl := &PipelineState{
		ID:               "task_short",
		TaskSlug:         "short-task",
		TaskLabel:        "Add structure",
		Status:           "executing",
		LoopCount:        1,
		MaxLoops:         4,
		ClaimsAccepted:   1,
		ClaimsTotalCount: 4,
	}

	rendered := stripANSI(renderPipelineRow(pl, 80, 0, grad, th, false, "", AnimState{}, false))
	if strings.Contains(rendered, "\n") {
		t.Fatalf("expected single-line row, got %q", rendered)
	}
	if !strings.Contains(rendered, "short-task") {
		t.Fatalf("expected task slug, got %q", rendered)
	}
	suffix := strings.Repeat(pipelineProgressGlyph, progressBarCells) + " 1/4"
	if !strings.HasSuffix(rendered, suffix) {
		t.Fatalf("rendered = %q, want right-aligned suffix %q", rendered, suffix)
	}
}

func TestModel_HandlePipelineState_RehomesRuntimePipelinePlaceholder(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetSize(80, 40)
	model.SetFocused(true)

	_, _ = model.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_runtime_pipeline",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "eng-1",
			Content:   "processing",
			Data: map[string]any{
				"agent_name":  "Engineer",
				"agent_type":  "engineer",
				"pipeline_id": "runtime-pipeline-123",
			},
		},
	})

	if model.pipelines["runtime-pipeline-123"] == nil {
		t.Fatal("expected runtime placeholder pipeline row")
	}

	model.Update(msg.PipelineStateMsg{
		PipelineID:        "task_auth_checkout",
		RuntimePipelineID: "runtime-pipeline-123",
		TaskID:            "task_auth_checkout",
		TaskLabel:         "auth-checkout",
		Status:            "executing",
		LoopCount:         2,
		MaxLoops:          5,
		WorkerType:        "engineer",
	})

	if model.pipelines["runtime-pipeline-123"] != nil {
		t.Fatal("expected runtime placeholder pipeline row to be rehomed")
	}

	pl := model.pipelines["task_auth_checkout"]
	if pl == nil {
		t.Fatal("expected canonical pipeline row")
	}
	if pl.TaskLabel != "auth-checkout" {
		t.Fatalf("TaskLabel = %q, want auth-checkout", pl.TaskLabel)
	}
	if pl.LoopCount != 2 || pl.MaxLoops != 5 {
		t.Fatalf("loop state = %d/%d, want 2/5", pl.LoopCount, pl.MaxLoops)
	}

	agent := model.agents["task_auth_checkout:engineer"]
	if agent == nil {
		t.Fatal("expected canonical engineer pipeline row to remain present")
	}
	if agent.PipelineID != "task_auth_checkout" {
		t.Fatalf("agent PipelineID = %q, want task_auth_checkout", agent.PipelineID)
	}
}
