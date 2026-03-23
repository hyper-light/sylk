package global

import "testing"

func TestGlobalTesterVisibleSkillsIncludeInstallRemediation(t *testing.T) {
	for _, want := range []string{
		"research_test_tool_install",
		"install_test_tooling",
		"run_command",
		"run_shell_script",
	} {
		if !containsGlobalTesterName(globalTesterVisibleSkillNames(), want) {
			t.Fatalf("global tester visible skills missing %q", want)
		}
	}
	for _, want := range []string{"install_test_tooling", "run_command", "run_shell_script"} {
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
