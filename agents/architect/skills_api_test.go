package architect

import "testing"

func TestArchitectManifestExcludesFilesystemTools(t *testing.T) {
	manifest := architectToolManifest()
	for _, name := range []string{
		"read_file",
		"glob",
		"grep",
		"git",
		"lsp",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"ast_grep_search",
	} {
		if _, ok := manifest.Policy(name); ok {
			t.Fatalf("architect manifest unexpectedly allows %q", name)
		}
	}
}

func TestArchitectManifestExposesCarryForwardContinuityTools(t *testing.T) {
	manifest := architectToolManifest()
	for _, name := range []string{"recall_forward", "carry_forward"} {
		policy, ok := manifest.Policy(name)
		if !ok {
			t.Fatalf("architect manifest missing %q", name)
		}
		if !policy.VisibleByDefault {
			t.Fatalf("%s must be visible by default", name)
		}
	}
}
