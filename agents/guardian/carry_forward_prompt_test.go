package guardian

import (
	"strings"
	"testing"
)

func TestGuardianPromptIncludesCarryForwardContinuityContract(t *testing.T) {
	prompt := (&Guardian{}).buildSystemPrompt(IntentConverse)
	compact := strings.Join(strings.Fields(prompt), " ")
	for _, want := range []string{
		"recall_forward",
		"carry_forward",
		"testaments and artifacts",
		"without consulting peers",
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("guardian prompt missing %q:\n%s", want, compact)
		}
	}
	if strings.Contains(compact, "carry claims as evidence") {
		t.Fatalf("guardian prompt tells the agent to carry claims as evidence:\n%s", compact)
	}
}
