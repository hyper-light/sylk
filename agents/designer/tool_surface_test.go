package designer

import (
	"testing"

	"github.com/adalundhe/sylk/agents/shared"
)

func TestDesignerTaskToolSurfaceIncludesPipelineWriteTools(t *testing.T) {
	d, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if d.tools != nil {
			d.tools.Close()
		}
	})

	contract := shared.BuildTaskExecutionContract(&shared.PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "designer",
		Context: map[string]any{
			"affected_files": []map[string]any{
				{"path": "src/ui/Button.tsx", "operation": "create"},
			},
		},
	})
	surface, err := shared.TaskToolSurface(d.toolRuntime(), contract, "designer")
	if err != nil {
		t.Fatalf("TaskToolSurface() error = %v", err)
	}

	names := map[string]struct{}{}
	for _, tool := range d.buildToolDefinitionsWithSurface(surface) {
		names[tool.Name] = struct{}{}
	}
	for _, want := range []string{"prepare_pipeline_write_context", "write_pipeline_file", "component_create"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("task surface missing %q: %v", want, names)
		}
	}
}
