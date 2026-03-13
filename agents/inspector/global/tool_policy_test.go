package global

import "testing"

func TestGlobalInspectorVisibleSkillsExcludeWorkspaceMutation(t *testing.T) {
	for _, blocked := range []string{
		"prepare_global_write_context",
		"write_global_file",
		"edit_global_file",
		"delete_global_file",
		"create_global_directory",
	} {
		if containsGlobalName(globalInspectorVisibleSkillNames(), blocked) {
			t.Fatalf("global inspector visible skills unexpectedly expose %q", blocked)
		}
	}
}

func TestGlobalInspectorMutatingSkillsExcludeWorkspaceMutation(t *testing.T) {
	for _, blocked := range []string{
		"write_global_file",
		"edit_global_file",
		"delete_global_file",
		"create_global_directory",
	} {
		if containsGlobalName(globalInspectorMutatingSkillNames(), blocked) {
			t.Fatalf("global inspector mutating skills unexpectedly expose %q", blocked)
		}
	}
}

func containsGlobalName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
