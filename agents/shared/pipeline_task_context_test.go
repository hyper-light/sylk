package shared

import (
	"strings"
	"testing"
)

func TestBuildPipelineSystemContext_IncludesWorkspaceLayerContract(t *testing.T) {
	task := &PipelineTaskInput{
		NodeID:    "task_1:test",
		TaskID:    "task_1",
		AgentType: "engineer",
		Context: map[string]any{
			"workspace": map[string]any{
				"read_set":  []any{"a.go"},
				"write_set": []any{"b.go"},
			},
		},
	}

	got := BuildPipelineSystemContext(task)
	for _, needle := range []string{
		"# Workspace Layers",
		"Disk: committed repository state on disk",
		"Global VFS: session-scoped merged but uncommitted work",
		"Pipeline VFS: task-scoped unmerged in-progress work",
		"read_workspace_file",
		"inspect_workspace_state",
		"summarize_workspace_state",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected context to contain %q\n%s", needle, got)
		}
	}
}
