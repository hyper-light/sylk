package authority

import (
	"context"
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

func TestRestrictWorkspaceViewsDeniesLibrarianVFSReads(t *testing.T) {
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
	if _, err := restricted.ReadFile(context.Background(), versioning.WorkspaceViewGlobal, "missing.txt", ""); err != versioning.ErrPermissionDenied {
		t.Fatalf("ReadFile error = %v, want %v", err, versioning.ErrPermissionDenied)
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
