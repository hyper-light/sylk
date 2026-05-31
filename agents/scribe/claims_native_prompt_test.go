package scribe

import (
	"strings"
	"testing"
)

func TestScribeSystemPromptIncludesClaimsNativeContract(t *testing.T) {
	prompt := scribeSystemPrompt("architect", true)
	for _, want := range []string{
		"Claims, Testaments, And Artifacts Operating Contract",
		"Claims are first-class work inputs",
		"expected_tool_calls",
		"Errors are artifacts for testaments",
		"submit_testaments",
		"evaluate_validation",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("scribe prompt missing %q", want)
		}
	}
}
