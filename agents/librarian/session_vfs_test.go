package librarian

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

// TestLibrarian_SetSessionVFS_EnablesListChanges verifies the fix
// for the "file access is unavailable" librarian error. Before the
// fix SetSessionVFS was a deliberate no-op that discarded the VFS
// argument and left the librarian with a disk-only view. Now it
// wires both the workspace-view and file-access layers so reads
// traverse the session overlay and list_changes can interrogate
// modifications.
//
// The test stages a write to the session's global overlay, asks the
// librarian's workspace_read(op=list_changes) handler what's
// pending, and confirms the staged modification shows up.
func TestLibrarian_SetSessionVFS_EnablesListChanges(t *testing.T) {
	workDir := t.TempDir()

	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}

	// Stage a modification against the session global overlay so
	// list_changes has something to return.
	globalFA := svfs.NewGlobalFileAccess(false)
	if err := globalFA.WriteFile(context.Background(), "docs/resolvers/staged.md", []byte("# staged")); err != nil {
		t.Fatalf("stage write: %v", err)
	}

	l, err := New(Config{
		Factory:          newTestFactory(t),
		ID:               "librarian-listchanges",
		SessionID:        "sess-lc",
		WorkingDirectory: workDir,
		EnableLLM:        true,
	}, nil)
	if err != nil {
		t.Fatalf("new librarian: %v", err)
	}

	// Pre-condition: the librarian starts with no FileAccess — the
	// bootstrap path only knows about disk until SetSessionVFS runs.
	if l.fileAccess != nil {
		t.Fatal("pre-attach: fileAccess should be nil until SetSessionVFS wires it")
	}

	// Wire the SessionVFS. The new implementation plumbs it through
	// to both workspaceViews and fileAccess.
	l.SetSessionVFS(svfs)

	if l.fileAccess == nil {
		t.Fatal("post-attach: fileAccess should be populated")
	}
	if l.sessionVFS == nil {
		t.Fatal("post-attach: sessionVFS should be retained")
	}

	// Invoke the workspace_read skill with op=list_changes via the
	// registered skill handler. This is the exact path the LLM
	// uses from the tool loop.
	skill := l.skills.Get("workspace_read")
	if skill == nil {
		t.Fatal("workspace_read skill not registered")
	}
	result, err := skill.Handler(context.Background(), json.RawMessage(`{"op":"list_changes","scope":"global"}`))
	if err != nil {
		t.Fatalf("workspace_read op=list_changes failed: %v", err)
	}

	summary, ok := result.(*versioning.WorkspaceChangeSummary)
	if !ok {
		t.Fatalf("result = %T, want *WorkspaceChangeSummary", result)
	}
	if summary.Scope != versioning.WorkspaceWriteScopeGlobal {
		t.Errorf("Scope = %q, want global", summary.Scope)
	}
	// Rendered as either a list of paths or entries — the staged
	// write must appear somewhere in the summary.
	raw, _ := json.Marshal(summary)
	if !strings.Contains(string(raw), "docs/resolvers/staged.md") {
		t.Errorf("staged modification not surfaced; summary=%s", string(raw))
	}
}

// TestLibrarian_SetSessionVFS_NilClearsFileAccess verifies the
// teardown path: passing nil detaches the fileAccess and
// workspaceViews fall back to the disk-only bootstrap. Important
// because session close / test cleanup go through this path and
// must not leave a dangling FileAccess pointing at a torn-down VFS.
func TestLibrarian_SetSessionVFS_NilClearsFileAccess(t *testing.T) {
	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}

	l, err := New(Config{
		Factory:          newTestFactory(t),
		ID:               "librarian-tear",
		SessionID:        "sess-tear",
		WorkingDirectory: t.TempDir(),
		EnableLLM:        true,
	}, nil)
	if err != nil {
		t.Fatalf("new librarian: %v", err)
	}

	l.SetSessionVFS(svfs)
	if l.fileAccess == nil {
		t.Fatal("expected fileAccess populated after attach")
	}
	l.SetSessionVFS(nil)
	if l.fileAccess != nil {
		t.Error("fileAccess should be cleared after nil attach")
	}
	if l.sessionVFS != nil {
		t.Error("sessionVFS should be cleared after nil attach")
	}
}
