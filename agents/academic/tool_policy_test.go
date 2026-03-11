package academic

import "testing"

func TestAcademicVisibleSkillsExcludeWorkspaceTools(t *testing.T) {
	for _, blocked := range []string{
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	} {
		for _, visible := range academicVisibleSkillNames() {
			if visible == blocked {
				t.Fatalf("academic visible skills unexpectedly include %q", blocked)
			}
		}
	}
}
