package orchestrator

import (
	"testing"

	"github.com/adalundhe/sylk/core/dag"
)

func TestFilterPipelineTaskPodDefs_ExcludesManagedPipelineTasks(t *testing.T) {
	d := dag.NewDAG("managed-pipeline", dag.ExecutionPolicy{})
	if err := d.AddNode(dag.NodeConfig{
		ID:        "task_1",
		AgentType: "engineer",
		Prompt:    "implement hello cli",
		Context: map[string]any{
			"task_id":        "task_1",
			"task_slug":      "implement-hello-cli",
			"pipeline_stage": "execute",
			"affected_files": []string{"hello.py"},
		},
	}); err != nil {
		t.Fatalf("add managed node: %v", err)
	}
	if err := d.AddNode(dag.NodeConfig{
		ID:        "task_2:test",
		AgentType: "tester-pipeline",
		Prompt:    "test hello cli",
		Context: map[string]any{
			"task_id":        "task_2",
			"task_slug":      "implement-hello-tests",
			"pipeline_stage": "test",
			"affected_files": []string{"test_hello.py"},
		},
	}); err != nil {
		t.Fatalf("add non-managed node: %v", err)
	}

	managed := collectManagedPipelineTaskIDs(d)
	if _, ok := managed["task_1"]; !ok {
		t.Fatal("expected execute worker task to be treated as managed")
	}
	if _, ok := managed["task_2"]; ok {
		t.Fatal("did not expect tester-stage task to be treated as managed")
	}

	defs := collectPipelineTaskPods(d)
	filtered := filterPipelineTaskPodDefs(defs, managed)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 task pod def after filtering, got %d", len(filtered))
	}
	if filtered[0].TaskID != "task_2" {
		t.Fatalf("filtered task pod def = %q, want %q", filtered[0].TaskID, "task_2")
	}
}
