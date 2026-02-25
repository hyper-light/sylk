package engineer

import (
	"strings"
	"testing"
)

func TestDefaultEngineerSystemPrompt_ComposesModulesOnceInOrder(t *testing.T) {
	modules := []string{
		"# Engineer Agent — System",
		"# Engineer Agent — Implementation Protocol",
		"# Engineer Agent — Consultation Policy",
		"# Engineer Agent — Skill Usage Policy",
		"# Engineer Agent — Guardrails",
		"# Engineer Agent — Self-Audit Protocol",
		"# Engineer Agent — Collaboration Protocol",
	}

	lastIdx := -1
	for _, marker := range modules {
		count := strings.Count(DefaultEngineerSystemPrompt, marker)
		if count != 1 {
			t.Fatalf("marker %q appears %d times, want 1", marker, count)
		}
		idx := strings.Index(DefaultEngineerSystemPrompt, marker)
		if idx <= lastIdx {
			t.Fatalf("marker %q appears out of order", marker)
		}
		lastIdx = idx
	}
}

func TestEngineerSystemCorePrompt_DoesNotDuplicateModuleHeadings(t *testing.T) {
	disallowed := []string{
		"# Engineer Agent — Implementation Protocol",
		"# Engineer Agent — Consultation Policy",
		"# Engineer Agent — Skill Usage Policy",
		"# Engineer Agent — Guardrails",
		"# Engineer Agent — Self-Audit Protocol",
		"# Engineer Agent — Collaboration Protocol",
	}

	for _, marker := range disallowed {
		if strings.Contains(EngineerSystemCorePrompt, marker) {
			t.Fatalf("core prompt contains delegated module heading %q", marker)
		}
	}
}
