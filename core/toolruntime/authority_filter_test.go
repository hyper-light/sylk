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

func TestApplyAuthorityProfileKeepsLibrarianDiskExecution(t *testing.T) {
	manifest := ApplyAuthorityProfile("librarian", NewManifest("librarian", "librarian.default",
		NewToolPolicy("read_file", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy("git", EffectReadOnly, DomainSystem, ExecutionModeLocal),
		NewToolPolicy("read_workspace_file", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
	))
	if _, ok := manifest.Tools["read_file"]; !ok {
		t.Fatal("read_file should remain for librarian")
	}
	if _, ok := manifest.Tools["git"]; !ok {
		t.Fatal("git should remain for librarian")
	}
	if _, ok := manifest.Tools["read_workspace_file"]; ok {
		t.Fatal("read_workspace_file should be removed for librarian")
	}
}
