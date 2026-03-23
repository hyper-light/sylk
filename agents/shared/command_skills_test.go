package shared

import (
	"strings"
	"testing"
)

func TestDetectShellControlOperator_RespectsQuotedText(t *testing.T) {
	if issue, ok := DetectShellControlOperator(`printf "%s" "a && b"`); ok {
		t.Fatalf("expected quoted shell syntax to be ignored, got %q", issue)
	}
}

func TestDetectShellControlOperator_FindsCompoundShellSyntax(t *testing.T) {
	issue, ok := DetectShellControlOperator("cd ui && pnpm test")
	if !ok {
		t.Fatal("expected shell control operator to be detected")
	}
	if issue != "&&" {
		t.Fatalf("issue = %q, want %q", issue, "&&")
	}
}

func TestSingleCommandOnlyError_GuidesTowardShellScript(t *testing.T) {
	err := SingleCommandOnlyError("run_command", "&&", "cd ui && pnpm test")
	if err == nil {
		t.Fatal("expected error")
	}
	text := err.Error()
	for _, want := range []string{"working_dir", "run_shell_script"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q missing %q", text, want)
		}
	}
}
