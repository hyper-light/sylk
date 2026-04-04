package pipeline

import (
	"strings"
	"testing"
)

func TestResearchDependencyInstallSkill_RoutesTestToolingToTester(t *testing.T) {
	skill := researchDependencyInstallSkill(&PipelineInspector{})
	if skill == nil {
		t.Fatal("expected skill")
	}
	if !strings.Contains(skill.Description, "non-test") {
		t.Fatalf("description = %q, want non-test guidance", skill.Description)
	}
	joinedAvoids := strings.Join(skill.Avoids, "\n")
	for _, want := range []string{"Tester", "research_test_tool_install", "install_test_tooling"} {
		if !strings.Contains(joinedAvoids, want) {
			t.Fatalf("avoids = %q, want %q", joinedAvoids, want)
		}
	}
}

func TestInstallDependencyToolingSkill_RoutesTestToolingToTester(t *testing.T) {
	skill := installDependencyToolingSkill(&PipelineInspector{})
	if skill == nil {
		t.Fatal("expected skill")
	}
	if !strings.Contains(skill.Description, "non-test") {
		t.Fatalf("description = %q, want non-test guidance", skill.Description)
	}
	joinedAvoids := strings.Join(skill.Avoids, "\n")
	for _, want := range []string{"Tester", "research_test_tool_install", "install_test_tooling"} {
		if !strings.Contains(joinedAvoids, want) {
			t.Fatalf("avoids = %q, want %q", joinedAvoids, want)
		}
	}
}
