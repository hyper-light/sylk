package designer

import "testing"

func TestDesignerVisibleSkillsIncludeDependencyInstallRemediation(t *testing.T) {
	// Phase 2.K / GT-4 + GI-5 refactor: research_dependency_install +
	// install_dependency_tooling collapsed into dependency(action=…).
	for _, want := range []string{"dependency", "bash"} {
		if !containsDesignerName(designerVisibleSkillNames(), want) {
			t.Fatalf("designer visible skills missing %q", want)
		}
	}
	if !containsDesignerName(designerMutatingSkillNames(), "dependency") {
		t.Fatal("designer mutating skills missing dependency")
	}
	if !containsDesignerName(designerMutatingSkillNames(), "bash") {
		t.Fatal("designer mutating skills missing bash")
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
