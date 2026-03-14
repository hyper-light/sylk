package librarian

import "testing"

func TestLibrarianVisibleSkillsRemainDiskOnly(t *testing.T) {
	for _, blocked := range []string{
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	} {
		for _, visible := range librarianVisibleSkillNames() {
			if visible == blocked {
				t.Fatalf("librarian visible skills unexpectedly include %q", blocked)
			}
		}
	}
}

func TestNew_AllowsLLMModeWithoutSearchSystem(t *testing.T) {
	agent, err := New(Config{
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
