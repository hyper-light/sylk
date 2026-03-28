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

func TestFormatPipelineCounterLabel_UsesLoopsOrPhaseFallback(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		loopCount int
		maxLoops  int
		want      string
	}{
		{name: "known max", status: "executing", maxLoops: 5, want: "0/5"},
		{name: "phase fallback executing", status: "executing", want: "3/4"},
		{name: "phase fallback validating", status: "validating", want: "4/4"},
		{name: "phase fallback pending", status: "pending", want: "0/4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPipelineCounterLabel(tt.status, tt.loopCount, tt.maxLoops); got != tt.want {
				t.Fatalf("formatPipelineCounterLabel(%q, %d, %d) = %q, want %q", tt.status, tt.loopCount, tt.maxLoops, got, tt.want)
			}
		})
	}
}

func TestRenderPipelineRow_WrapsProgressWithContinuationPrefix(t *testing.T) {
	th := theme.DefaultDark()
	grad := th.Palette.PipelineGradient()
	pl := &PipelineState{
		ID:        "task_auth_checkout",
		TaskLabel: "auth-checkout-extremely-long-title",
		Status:    "executing",
		LoopCount: 3,
		MaxLoops:  12,
	}

	rendered := stripANSI(renderPipelineRow(pl, 26, 0, grad, th, false, "", AnimState{}, false))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected wrapped pipeline row to use 2 lines, got %d: %q", len(lines), rendered)
	}
	if !strings.HasPrefix(lines[0], pipelineHeaderPrefix(false)) {
		t.Fatalf("first line = %q, want prefix %q", lines[0], pipelineHeaderPrefix(false))
	}
	if !strings.HasPrefix(lines[1], pipelineContinuationPrefix()) {
		t.Fatalf("second line = %q, want continuation prefix %q", lines[1], pipelineContinuationPrefix())
	}
	if strings.Contains(lines[0], strings.Repeat(pipelineProgressGlyph, progressBarCells)) {
		t.Fatalf("expected progress bar to move off the first line, got %q", lines[0])
	}
	if !strings.Contains(lines[1], strings.Repeat(pipelineProgressGlyph, progressBarCells)) || !strings.Contains(lines[1], "3/12") {
		t.Fatalf("second line = %q, want progress bar and counter", lines[1])
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
