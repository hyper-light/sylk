package authority

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestProfileForArchitectHasNoFilesystem(t *testing.T) {
	profile := ProfileFor("architect")
	if profile.AllowsFileReads() {
		t.Fatal("architect should not be able to read files")
	}
	if profile.AllowsWorkspaceTools() {
		t.Fatal("architect should not have workspace view access")
	}
	if profile.ExecScope != ExecScopeNone {
		t.Fatalf("architect exec scope = %q, want none", profile.ExecScope)
	}
}

func TestRestrictFileAccessDeniesKnowledgeWrites(t *testing.T) {
	delegate := versioning.NewDiskFileAccess(t.TempDir(), false)
	restricted := RestrictFileAccess("academic", delegate)
	if restricted == nil {
		t.Fatal("restricted file access should not be nil")
	}
	if err := restricted.WriteFile(context.Background(), "note.txt", []byte("blocked")); err != versioning.ErrPermissionDenied {
		t.Fatalf("WriteFile error = %v, want %v", err, versioning.ErrPermissionDenied)
	}
}

// TestRestrictWorkspaceViewsLibrarianAllowsAllThreeViews pins the
// post-refactor authority surface: librarian gets read access to disk,
// global, AND pipeline. The original disk-only restriction was a hack to
// prevent the librarian from confusing "what actually exists" across
// layers; that confusion is now prevented structurally by requiring every
// read tool to name its layer via the `view` parameter and requiring every
// response to attribute its sources to a layer. With layer attribution in
// place, denying the librarian access to pipeline state needlessly cripples
// comparison queries ("is the engineer's draft consistent with the plan?")
// that the librarian is the natural agent to answer.
//
// Writes remain blocked (FileScope=FileScopeDiskRead) — librarian is
// read-only across all layers.
func TestRestrictWorkspaceViewsLibrarianAllowsAllThreeViews(t *testing.T) {
	root := t.TempDir()
	views := versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:  versioning.WorkspaceViewDisk,
		WorkingDir:   root,
		DiskFallback: versioning.NewDiskFileAccess(root, true),
	})
	restricted := RestrictWorkspaceViews("librarian", views)
	if restricted == nil {
		t.Fatal("restricted workspace views should not be nil")
	}
	// All three views must NOT short-circuit with permission-denied. They
	// reach the delegate, which may return a different error (such as "no
	// active session VFS" in this test fixture). The important thing is
	// that the authority layer no longer blocks the call.
	for _, view := range []versioning.WorkspaceView{
		versioning.WorkspaceViewDisk,
		versioning.WorkspaceViewGlobal,
		versioning.WorkspaceViewPipeline,
	} {
		pipelineID := ""
		if view == versioning.WorkspaceViewPipeline {
			pipelineID = "task_1"
		}
		_, err := restricted.ReadFile(context.Background(), view, "missing.txt", pipelineID)
		if err == versioning.ErrPermissionDenied {
			t.Errorf("view %q must be reachable for librarian (got permission-denied)", view)
		}
	}
}

func TestRestrictFileAccessBlocksPipelineDiskWrites(t *testing.T) {
	delegate := versioning.NewDiskFileAccess(t.TempDir(), false)
	restricted := RestrictFileAccess("engineer", delegate)
	if err := restricted.WriteFile(context.Background(), "draft.txt", []byte("blocked")); err != versioning.ErrPermissionDenied {
		t.Fatalf("WriteFile error = %v, want %v", err, versioning.ErrPermissionDenied)
	}
	if !restricted.IsReadOnly() {
		t.Fatal("pipeline agent disk fallback should be read-only")
	}
}

func TestRestrictWorkspaceViews_PreservesSessionResolution(t *testing.T) {
	root := t.TempDir()
	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	target := filepath.Join(root, "hello.txt")
	ctx := versioning.WithSessionID(context.Background(), "sess-1")
	if err := svfs.GlobalVFS().Write(ctx, target, []byte("global")); err != nil {
		t.Fatalf("seed global: %v", err)
	}

	views := versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:      versioning.WorkspaceViewGlobal,
		DefaultSessionID: "sess-1",
		SessionLookup: func(sessionID string) *versioning.SessionVFS {
			if sessionID == "sess-1" {
				return svfs
			}
			return nil
		},
		WorkingDir:   root,
		DiskFallback: versioning.NewDiskFileAccess(root, true),
	})
	restricted := RestrictWorkspaceViews("inspector", views)

	if resolved := versioning.SessionForWorkspaceViews(ctx, restricted); resolved != svfs {
		t.Fatalf("resolved session = %v, want %v", resolved, svfs)
	}
	content, err := restricted.ReadFile(ctx, versioning.WorkspaceViewGlobal, "hello.txt", "")
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if string(content) != "global" {
		t.Fatalf("global content = %q, want %q", content, "global")
	}
}
