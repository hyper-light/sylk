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

// TestApplyAuthorityProfileKeepsLibrarianLayeredReadSurface pins the
// post-refactor invariant: the workspace-aware read tools survive the
// authority filter for the librarian. The bare read_file / glob / grep
// skills are no longer registered for the librarian (see
// agents/librarian/skills.go), so this test asserts only that the
// layer-explicit family is preserved by the filter — it does NOT include
// the bare names because they should never appear in a librarian manifest
// to begin with.
func TestApplyAuthorityProfileKeepsLibrarianLayeredReadSurface(t *testing.T) {
	manifest := ApplyAuthorityProfile("librarian", NewManifest("librarian", "librarian.default",
		NewToolPolicy("git", EffectReadOnly, DomainSystem, ExecutionModeLocal),
		NewToolPolicy("read_workspace_file", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("workspace_glob", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("workspace_grep", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("inspect_workspace_state", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("summarize_workspace_state", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
	))
	for _, name := range []string{
		"git",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	} {
		if _, ok := manifest.Tools[name]; !ok {
			t.Fatalf("%s should remain for librarian", name)
		}
	}
}
