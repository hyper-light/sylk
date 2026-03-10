package shared

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type stubWorkspaceViews struct {
	content map[string][]byte
}

func (s *stubWorkspaceViews) ReadFile(_ context.Context, _ versioning.WorkspaceView, path string, _ string) ([]byte, error) {
	if content, ok := s.content[path]; ok {
		return content, nil
	}
	return nil, versioning.ErrFileNotFound
}

func (s *stubWorkspaceViews) Glob(context.Context, versioning.WorkspaceView, string, string, []string, string) ([]string, error) {
	return nil, nil
}

func (s *stubWorkspaceViews) Grep(context.Context, versioning.WorkspaceView, string, string, string, int, int, string) ([]versioning.GrepMatch, error) {
	return nil, nil
}

func (s *stubWorkspaceViews) InspectPath(context.Context, string, string) (*versioning.WorkspacePathState, error) {
	return nil, nil
}

func (s *stubWorkspaceViews) SummarizePaths(context.Context, []string, string) (*versioning.WorkspaceSummary, error) {
	return nil, nil
}

func (s *stubWorkspaceViews) DefaultView() versioning.WorkspaceView { return versioning.WorkspaceViewPipeline }

func TestTesterReadWorkspaceFileSkill_MissingFileReturnsMetadata(t *testing.T) {
	registry := skills.NewRegistry()
	registry.Register(NewTesterReadWorkspaceFileSkill(
		func() versioning.WorkspaceViewAccess {
			return &stubWorkspaceViews{content: map[string][]byte{}}
		},
		func() string { return "task-1" },
	))

	result := registry.Invoke(context.Background(), "read_workspace_file", json.RawMessage(`{"view":"pipeline","path":"missing.go"}`))
	if !result.Success {
		t.Fatalf("Invoke success = false, error = %s", result.Error)
	}

	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("result data type = %T, want map[string]any", result.Data)
	}
	if exists, _ := data["exists"].(bool); exists {
		t.Fatalf("exists = true, want false")
	}
	if missing, _ := data["missing"].(bool); !missing {
		t.Fatalf("missing = false, want true")
	}
	if content, _ := data["content"].(string); content != "" {
		t.Fatalf("content = %q, want empty", content)
	}
}
