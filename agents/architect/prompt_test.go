package architect

import (
	"strings"
	"testing"
)

func TestDefaultSystemPrompt_ComposesModulesOnceInOrder(t *testing.T) {
	modules := []string{
		"# THE ARCHITECT",
		"## Planning Protocol",
		"## Consultation Policy",
		"## Delegation and Handoff",
		"## Output Contract",
		"## Guardrails",
		"## Skill Use Policy",
	}

	lastIdx := -1
	for _, marker := range modules {
		count := strings.Count(DefaultSystemPrompt, marker)
		if count != 1 {
			t.Fatalf("marker %q appears %d times, want 1", marker, count)
		}
		idx := strings.Index(DefaultSystemPrompt, marker)
		if idx <= lastIdx {
			t.Fatalf("marker %q appears out of order", marker)
		}
		lastIdx = idx
	}
}

func TestArchitectSystemCorePrompt_DoesNotDuplicateModuleHeadings(t *testing.T) {
	disallowed := []string{
		"## Planning Protocol",
		"## Consultation Policy",
		"## Delegation and Handoff",
		"## Output Contract",
		"## Guardrails",
		"## Skill Use Policy",
	}

	for _, marker := range disallowed {
		if strings.Contains(ArchitectSystemCorePrompt, marker) {
			t.Fatalf("core prompt contains delegated module heading %q", marker)
		}
	}
}

