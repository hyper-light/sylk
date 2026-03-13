package versioning

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

type stubReadWorkspaceViews struct {
	content map[string][]byte
}

func (s *stubReadWorkspaceViews) ReadFile(_ context.Context, _ WorkspaceView, path string, _ string) ([]byte, error) {
	if content, ok := s.content[path]; ok {
		return content, nil
	}
	return nil, ErrFileNotFound
}

func (s *stubReadWorkspaceViews) Glob(context.Context, WorkspaceView, string, string, []string, string) ([]string, error) {
	return nil, nil
}

func (s *stubReadWorkspaceViews) Grep(context.Context, WorkspaceView, string, string, string, int, int, string) ([]GrepMatch, error) {
	return nil, nil
}

func (s *stubReadWorkspaceViews) InspectPath(context.Context, string, string) (*WorkspacePathState, error) {
	return nil, nil
}

func (s *stubReadWorkspaceViews) SummarizePaths(context.Context, []string, string) (*WorkspaceSummary, error) {
	return nil, nil
}

func (s *stubReadWorkspaceViews) DefaultView() WorkspaceView { return WorkspaceViewPipeline }

func TestReadWorkspaceFileSkill_MissingFileReturnsMetadata(t *testing.T) {
	registry := skills.NewRegistry()
	registry.Register(NewReadWorkspaceFileSkill(
		func() WorkspaceViewAccess {
			return &stubReadWorkspaceViews{content: map[string][]byte{}}
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
	if reason, _ := data["reason"].(string); reason == "" {
		t.Fatal("expected missing-file reason")
	}
}
