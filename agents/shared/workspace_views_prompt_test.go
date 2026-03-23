package shared

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestBuildTaskWorkspaceRuntimeContext_IncludesSummarySections(t *testing.T) {
	dir := t.TempDir()
	if err := versioning.NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := versioning.WithSessionID(context.Background(), "sess-1")
	target := filepath.Join(dir, "hello.txt")
	if err := svfs.GlobalVFS().Write(ctx, target, []byte("global")); err != nil {
		t.Fatalf("seed global: %v", err)
	}

	views := versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:       versioning.WorkspaceViewPipeline,
		DefaultPipelineID: "task-1",
		Session:           svfs,
		WorkingDir:        dir,
		DiskFallback:      versioning.NewDiskFileAccess(dir, true),
	})
	task := &PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "engineer",
		Context: map[string]any{
			"affected_files": []any{"hello.txt"},
			"workspace": map[string]any{
				"write_set": []any{"hello.txt"},
			},
		},
	}

	got := BuildTaskWorkspaceRuntimeContext(ctx, views, task)
	for _, needle := range []string{
		"## Workspace Snapshot",
		"Paths Considered",
		"Views Available",
		"Global Changed vs Disk",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected runtime workspace context to contain %q\n%s", needle, got)
		}
	}
}

func TestBuildWorkspaceViewContext_ExplainsBrokeredCommandAccessToVFS(t *testing.T) {
	got := BuildWorkspaceViewContext(WorkspacePromptOptions{
		DefaultView:     versioning.WorkspaceViewPipeline,
		IncludePipeline: true,
	})

	for _, needle := range []string{
		"Default read/write tools for this agent operate on the `pipeline` view unless a tool explicitly says otherwise.",
		"`run_command` and `run_shell_script` execute against that same layered workspace view",
		"they can read VFS-backed files before those changes are committed to disk",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected workspace context to contain %q\n%s", needle, got)
		}
	}
}
