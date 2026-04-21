package global

import "testing"

func TestGlobalTesterVisibleSkillsIncludeInstallRemediation(t *testing.T) {
	// Phase 2.K / GT-4 + GI-5 refactor: research_test_tool_install +
	// install_test_tooling collapsed into dependency(action=…, category=test).
	for _, want := range []string{"dependency", "bash"} {
		if !containsGlobalTesterName(globalTesterVisibleSkillNames(), want) {
			t.Fatalf("global tester visible skills missing %q", want)
		}
	}
	for _, want := range []string{"dependency", "bash"} {
		if !containsGlobalTesterName(globalTesterMutatingSkillNames(), want) {
			t.Fatalf("global tester mutating skills missing %q", want)
		}
	}
}

func containsGlobalTesterName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
