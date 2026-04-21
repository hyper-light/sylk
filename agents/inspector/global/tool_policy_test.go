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

func TestGlobalInspectorVisibleSkillsIncludeDependencyInstallRemediation(t *testing.T) {
	// Phase 2.K / GT-4 + GI-5 refactor: research_dependency_install +
	// install_dependency_tooling collapsed into dependency(action=…).
	for _, want := range []string{"dependency", "bash"} {
		if !containsGlobalName(globalInspectorVisibleSkillNames(), want) {
			t.Fatalf("global inspector visible skills missing %q", want)
		}
	}
	for _, want := range []string{"dependency", "bash"} {
		if !containsGlobalName(globalInspectorMutatingSkillNames(), want) {
			t.Fatalf("global inspector mutating skills missing %q", want)
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
