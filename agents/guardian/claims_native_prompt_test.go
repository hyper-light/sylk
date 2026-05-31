package guardian

import (
	"strings"
	"testing"
)

func TestGuardianSystemPromptIncludesClaimsNativeContract(t *testing.T) {
	prompt := (&Guardian{}).buildSystemPrompt(IntentConverse)
	for _, want := range []string{
		"Claims, Testaments, And Artifacts Operating Contract",
		"Claims are first-class work inputs",
		"expected_tool_calls",
		"Errors are artifacts for testaments",
		"submit_testaments",
		"evaluate_validation",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("guardian prompt missing %q", want)
		}
	}
}
