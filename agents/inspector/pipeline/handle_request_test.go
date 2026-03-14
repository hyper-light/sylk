package pipeline

import (
	"context"
	"testing"

	inspectorshared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
)

func TestInspectTask_SeedsCriteriaWithoutProvider(t *testing.T) {
	pi, err := New(inspectorshared.PipelineInspectorConfig{AgentID: "inspector-pipeline"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pi.Close()

	criteria, err := pi.InspectTask(context.Background(), &agentShared.PipelineTaskInput{
		TaskID:    "task_1",
		AgentType: "inspector-pipeline",
		Prompt:    "Build a hello world CLI",
		Context: map[string]any{
			"agent_type":       "engineer",
			"pipeline_stage":   "inspect",
			"success_criteria": []any{"CLI prints hello world"},
		},
	})
	if err != nil {
		t.Fatalf("InspectTask: %v", err)
	}
	if criteria == nil {
		t.Fatal("expected criteria")
	}
	if criteria.TaskID != "task_1" {
		t.Fatalf("criteria task = %q, want %q", criteria.TaskID, "task_1")
	}
	if len(criteria.SuccessCriteria) == 0 {
		t.Fatal("expected success criteria to be seeded")
	}
}
