package toolruntime

import "testing"

func TestApplyAuthorityProfilePrunesArchitectFilesystemTools(t *testing.T) {
	manifest := ApplyAuthorityProfile("architect", NewManifest("architect", "architect.default",
		NewToolPolicy("read_file", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("git", EffectReadOnly, DomainSystem, ExecutionModeLocal),
		NewToolPolicy("read_workspace_file", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("plan", EffectMutating, DomainPlanning, ExecutionModeLocalWorker),
	))
	if _, ok := manifest.Tools["read_file"]; ok {
		t.Fatal("read_file should be removed for architect")
	}
	if _, ok := manifest.Tools["git"]; ok {
		t.Fatal("git should be removed for architect")
	}
	if _, ok := manifest.Tools["read_workspace_file"]; ok {
		t.Fatal("read_workspace_file should be removed for architect")
	}
	if _, ok := manifest.Tools["plan"]; !ok {
		t.Fatal("plan should remain for architect")
	}
}

// TestApplyAuthorityProfileKeepsLibrarianReadSurface pins the dual-read
// design: librarian retains both disk-only read_file (committed source of
// truth) and the workspace-aware read_workspace_file (in-flight global VFS
// overlay). Previously read_workspace_file was pruned at the authority
// filter, which forced the librarian to use bare read_file even when files
// existed only in the global VFS, producing phantom "no such file" errors.
func TestApplyAuthorityProfileKeepsLibrarianReadSurface(t *testing.T) {
	manifest := ApplyAuthorityProfile("librarian", NewManifest("librarian", "librarian.default",
		NewToolPolicy("read_file", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("git", EffectReadOnly, DomainSystem, ExecutionModeLocal),
		NewToolPolicy("read_workspace_file", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("workspace_glob", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
	))
	for _, name := range []string{"read_file", "git", "read_workspace_file", "workspace_glob"} {
		if _, ok := manifest.Tools[name]; !ok {
			t.Fatalf("%s should remain for librarian", name)
		}
	}
}
