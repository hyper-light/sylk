package designer

import "testing"

func TestDesignerVisibleSkillsIncludeDependencyInstallRemediation(t *testing.T) {
	for _, want := range []string{
		"research_dependency_install",
		"install_dependency_tooling",
		"run_command",
		"run_shell_script",
	} {
		if !containsDesignerName(designerVisibleSkillNames(), want) {
			t.Fatalf("designer visible skills missing %q", want)
		}
	}
	if !containsDesignerName(designerMutatingSkillNames(), "install_dependency_tooling") {
		t.Fatal("designer mutating skills missing install_dependency_tooling")
	}
	for _, want := range []string{"run_command", "run_shell_script"} {
		if !containsDesignerName(designerMutatingSkillNames(), want) {
			t.Fatalf("designer mutating skills missing %q", want)
		}
	}
}

func containsDesignerName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
