package prompts

import (
	"strings"
	"testing"
)

func TestClaimsNativePromptIsLoadable(t *testing.T) {
	got, err := Load("shared", "claims_native")
	if err != nil {
		t.Fatalf("Load shared/claims_native: %v", err)
	}
	for _, want := range []string{
		"Claims, Testaments, And Artifacts Operating Contract",
		"Claims are first-class work inputs",
		"expected_tool_calls",
		"Errors are artifacts for testaments",
		"submit_testaments",
		"evaluate_validation",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("claims_native prompt missing required marker %q", want)
		}
	}
}
