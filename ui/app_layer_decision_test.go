package ui

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/msg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestLayerDecisionLayout_IncludesFailuresAndOptions(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 90, Height: 28}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	app.layerDecision = &layerDecisionState{
		request: &msg.LayerDecisionMsg{
			DAGID:    "dag-1",
			LayerIdx: 0,
			FailedNodes: []msg.LayerFailedNode{
				{
					NodeName:  "Create src-layout package scaffold",
					AgentType: "engineer",
					Error:     "compile failed",
				},
			},
		},
		selected:  0,
		activated: -1,
	}

	layout := app.layerDecisionLayout(60)
	joined := ansi.Strip(strings.Join(layout.lines, "\n"))

	if !strings.Contains(joined, "DAG dag-1 layer 0 has blocking failures.") {
		t.Fatalf("layer decision prompt missing title:\n%s", joined)
	}
	if !strings.Contains(joined, "Create src-layout package scaffold [engineer]: compile") || !strings.Contains(joined, "failed") {
		t.Fatalf("layer decision prompt missing failure summary:\n%s", joined)
	}
	if !strings.Contains(joined, "• Retry Layer rerun failed nodes") {
		t.Fatalf("layer decision prompt missing retry option:\n%s", joined)
	}
	if !strings.Contains(joined, "• Abort DAG cancel this workflow") {
		t.Fatalf("layer decision prompt missing abort option:\n%s", joined)
	}
}

func TestLayerDecisionOptionAt_UsesTightHitboxes(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 90, Height: 28}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	app.layerDecision = &layerDecisionState{
		request: &msg.LayerDecisionMsg{
			DAGID:    "dag-1",
			LayerIdx: 0,
			FailedNodes: []msg.LayerFailedNode{
				{NodeName: "engineer-worker", AgentType: "engineer", Error: "compile failed"},
			},
		},
		selected:  0,
		activated: -1,
	}

	layout := app.layerDecisionLayout(60)
	if len(layout.hitboxes) == 0 {
		t.Fatal("expected option hitboxes")
	}
	first := layout.hitboxes[0]

	if idx, ok := app.layerDecisionOptionAt(first.x1-1, first.y); !ok || idx != first.option {
		t.Fatalf("layerDecisionOptionAt() = (%d, %v), want (%d, true)", idx, ok, first.option)
	}
	if idx, ok := app.layerDecisionOptionAt(59, first.y); ok {
		t.Fatalf("layerDecisionOptionAt() outside text span = (%d, true), want no hit", idx)
	}
}

func TestLayerDecisionLayout_TruncatesFailuresToKeepOptionsVisible(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 72, Height: 14}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	failures := make([]msg.LayerFailedNode, 0, 8)
	for i := 0; i < 8; i++ {
		failures = append(failures, msg.LayerFailedNode{
			NodeID:    "node-" + string(rune('a'+i)),
			AgentType: "tester-pipeline",
			Error:     "placeholder failure detail that should wrap in the overlay",
		})
	}
	app.layerDecision = &layerDecisionState{
		request: &msg.LayerDecisionMsg{
			DAGID:       "dag-2",
			LayerIdx:    1,
			FailedNodes: failures,
		},
		selected:  0,
		activated: -1,
	}

	layout := app.layerDecisionLayout(48)
	joined := ansi.Strip(strings.Join(layout.lines, "\n"))

	if !strings.Contains(joined, "more blocking failure(s) omitted") {
		t.Fatalf("expected overflow summary in compact layout:\n%s", joined)
	}
	if !strings.Contains(joined, "Retry Layer") || !strings.Contains(joined, "Abort DAG") {
		t.Fatalf("expected action options to remain visible after truncation:\n%s", joined)
	}
}
