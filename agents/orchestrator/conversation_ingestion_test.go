package orchestrator

import (
	"testing"

	"github.com/adalundhe/sylk/agents/architect"
)

func TestBuildPreKickoffIngestionReceipt_IncludesQueryAndCounts(t *testing.T) {
	handoff := &architect.PlanHandoff{
		PlanID:          "plan-123",
		Query:           "build a tiny hello world cli",
		Tasks:           []*architect.HandoffTask{{ID: "task-1"}, {ID: "task-2"}},
		ExecutionLayers: [][]string{{"task-1"}, {"task-2"}},
	}

	got := buildPreKickoffIngestionReceipt(handoff)
	want := "Received plan plan-123 for build a tiny hello world cli. Prepared 2 tasks across 2 layers. Starting execution next."
	if got != want {
		t.Fatalf("buildPreKickoffIngestionReceipt() = %q, want %q", got, want)
	}
}

func TestBuildPreKickoffIngestionReceipt_FallsBackWithoutQuery(t *testing.T) {
	handoff := &architect.PlanHandoff{
		PlanID:          "plan-456",
		Tasks:           []*architect.HandoffTask{{ID: "task-1"}},
		ExecutionLayers: [][]string{{"task-1"}},
	}

	got := buildPreKickoffIngestionReceipt(handoff)
	want := "Received plan plan-456. Prepared 1 tasks across 1 layers. Starting execution next."
	if got != want {
		t.Fatalf("buildPreKickoffIngestionReceipt() = %q, want %q", got, want)
	}
}

func TestIngestionReceiptAlreadyStreamed(t *testing.T) {
	if !ingestionReceiptAlreadyStreamed(map[string]any{"receipt_streamed": true}) {
		t.Fatal("expected receipt_streamed marker to be recognized")
	}
	if ingestionReceiptAlreadyStreamed(map[string]any{"receipt_streamed": false}) {
		t.Fatal("expected false receipt_streamed marker to be ignored")
	}
	if ingestionReceiptAlreadyStreamed(map[string]any{"plan_id": "plan-1"}) {
		t.Fatal("expected missing receipt_streamed marker to be ignored")
	}
}
