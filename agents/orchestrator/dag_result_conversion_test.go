package orchestrator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

func TestConvertPipelineToNodeResult_UsesMessageWhenErrorEmpty(t *testing.T) {
	update := &PipelineUpdate{
		NodeID:    "node-1",
		Status:    "failed",
		Message:   "tests failed in layer 0",
		Timestamp: time.Now(),
	}

	result := convertPipelineToNodeResult(update)
	if result == nil {
		t.Fatal("expected node result")
	}
	if result.State != dag.NodeStateFailed {
		t.Fatalf("state = %v, want failed", result.State)
	}
	if result.Error == nil || result.Error.Error() != "tests failed in layer 0" {
		t.Fatalf("error = %v, want pipeline message fallback", result.Error)
	}
}

func TestConvertTaskFailedToNodeResult_UsesExplicitFallback(t *testing.T) {
	task := &TaskRecord{ID: "task-1"}

	result := convertTaskFailedToNodeResult(task)
	if result == nil {
		t.Fatal("expected node result")
	}
	if result.State != dag.NodeStateFailed {
		t.Fatalf("state = %v, want failed", result.State)
	}
	if result.Error == nil || result.Error.Error() != "task failed without error details" {
		t.Fatalf("error = %v, want explicit fallback", result.Error)
	}
}
