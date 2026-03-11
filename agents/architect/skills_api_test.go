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
