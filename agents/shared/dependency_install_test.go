package shared

import "testing"

func TestParseDependencyInstallPlan_ExtractsFencedJSON(t *testing.T) {
	raw := "Plan:\n```json\n{\"summary\":\"Install pytest\",\"steps\":[{\"command\":\"python -m pip install pytest\"}]}\n```"
	plan, err := ParseDependencyInstallPlan(raw)
	if err != nil {
		t.Fatalf("ParseDependencyInstallPlan() error = %v", err)
	}
	if plan.Summary != "Install pytest" {
		t.Fatalf("summary = %q, want Install pytest", plan.Summary)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Command != "python -m pip install pytest" {
		t.Fatalf("steps = %#v", plan.Steps)
	}
}

func TestValidateDependencyInstallPlan_RejectsUnsafeCommand(t *testing.T) {
	err := ValidateDependencyInstallPlan(&DependencyInstallPlan{
		Summary: "Install tooling",
		Steps: []DependencyInstallStep{
			{Command: "npm install jest && npm install vitest"},
		},
	})
	if err == nil {
		t.Fatal("expected unsafe install command to be rejected")
	}
}
