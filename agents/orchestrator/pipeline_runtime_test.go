package orchestrator

import (
	"testing"
	"time"
)

func TestExtractPipelineUpdate_ParsesStageAttemptAndTimestampFromMap(t *testing.T) {
	now := time.Now().UTC().Round(0)
	update := extractPipelineUpdate(map[string]any{
		"dag_id":     "dag-1",
		"node_id":    "node-1",
		"task_id":    "task-1",
		"agent_id":   "worker-1",
		"agent_type": "tester-pipeline",
		"status":     "running",
		"stage":      "test",
		"progress":   0.4,
		"message":    "testing current state",
		"attempt":    2,
		"timestamp":  now.Format(time.RFC3339Nano),
	})
	if update == nil {
		t.Fatal("expected pipeline update")
	}
	if update.Stage != "test" {
		t.Fatalf("stage = %q, want test", update.Stage)
	}
	if update.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", update.Attempt)
	}
	if !update.Timestamp.Equal(now) {
		t.Fatalf("timestamp = %s, want %s", update.Timestamp.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	}
}
