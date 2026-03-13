package shared

import (
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestDetectToolCallDuplicate_AllowsStaleWriteBasisPrepareRetry(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: preparePipelineWriteContextTool, Arguments: `{"path":"main.go"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role:    providers.RoleAssistant,
			Content: "Trying the write.",
			ToolCalls: []providers.ToolCall{
				{ID: "write-1", Name: "write_pipeline_file", Arguments: `{"path":"main.go"}`},
			},
		},
		{
			Role:     providers.RoleTool,
			ToolName: "write_pipeline_file",
			IsError:  true,
			Content:  `{"error":"tool \"write_pipeline_file\" failed: pipeline write basis is stale: disk availability changed; rerun prepare_pipeline_write_context"}`,
		},
	}

	allDup, _ := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected stale-basis retry to bypass duplicate rejection")
	}
}

func TestDetectToolCallDuplicate_AllowsPrepareRetryAfterWriteSurfaceMutation(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "1", Name: preparePipelineWriteContextTool, Arguments: `{"path":"hello_cli/__init__.py"}`},
	}
	seen := make(map[ToolCallSignature]int)
	DetectToolCallDuplicate(calls, seen)

	history := []providers.Message{
		{
			Role: providers.RoleAssistant,
			ToolCalls: []providers.ToolCall{
				{ID: "mkdir-1", Name: "create_pipeline_directory", Arguments: `{"path":"hello_cli"}`},
			},
		},
		{
			Role:     providers.RoleTool,
			ToolName: "create_pipeline_directory",
			Content:  `{"path":"hello_cli","created":true}`,
		},
	}

	allDup, _ := DetectToolCallDuplicate(calls, seen, history)
	if allDup {
		t.Fatal("expected prepare retry after directory mutation to bypass duplicate rejection")
	}
}
