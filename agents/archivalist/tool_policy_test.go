package archivalist

import "testing"

func TestArchivalistVisibleSkillsExcludeWorkspaceTools(t *testing.T) {
	for _, blocked := range []string{
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	} {
		for _, visible := range archivalistVisibleSkillNames() {
			if visible == blocked {
				t.Fatalf("archivalist visible skills unexpectedly include %q", blocked)
			}
		}
	}
}
