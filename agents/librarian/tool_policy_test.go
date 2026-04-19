package librarian

import "testing"

// TestLibrarianVisibleSkillsExposeBothDiskAndWorkspaceReads pins the dual-read
// design: disk-only read_file/glob/grep (committed source of truth) live
// alongside the workspace-aware variants (in-flight global VFS overlay).
// Mixing the two paths into a single tool led to "open ... no such file"
// errors when the librarian tried to read files staged in the global VFS but
// not yet promoted to disk; keeping them as separate, attributed tools makes
// the LLM's view selection explicit.
//
// Pipeline-scoped views are intentionally excluded — librarian operates above
// any single pipeline's scope, so the authority profile only lists Disk and
// Global. The workspace skills still register, but pipeline reads are denied
// at the authority layer if attempted.
func TestLibrarianVisibleSkillsExposeBothDiskAndWorkspaceReads(t *testing.T) {
	required := []string{
		"read_file",
		"glob",
		"grep",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	}
	visible := map[string]struct{}{}
	for _, name := range librarianVisibleSkillNames() {
		visible[name] = struct{}{}
	}
	for _, name := range required {
		if _, ok := visible[name]; !ok {
			t.Errorf("librarian visible skills must include %q", name)
		}
	}
}

func TestNew_AllowsLLMModeWithoutSearchSystem(t *testing.T) {
	agent, err := New(Config{
		Factory: newTestFactory(t),
		ID:               "lib-test",
		EnableLLM:        true,
		Model:            "test-model",
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if agent == nil {
		t.Fatal("New() returned nil agent")
	}
}
