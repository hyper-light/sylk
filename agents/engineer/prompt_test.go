package engineer

import (
	"strings"
	"testing"

	shared "github.com/adalundhe/sylk/agents/shared"
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

func TestDefaultEngineerSystemPrompt_UsesCurrentSkillNames(t *testing.T) {
	required := []string{
		"`discover_project_tools`",
		"`discover_code_patterns`",
		"`consult`",
		"`audit`",
		"`run_command`",
	}
	for _, marker := range required {
		if !strings.Contains(DefaultEngineerSystemPrompt, marker) {
			t.Fatalf("expected default engineer prompt to contain %q", marker)
		}
	}

	legacyAliases := []string{
		"consult_librarian",
		"consult_archivalist",
		"consult_academic",
		"audit_implementation",
		"run_tests",
	}
	for _, marker := range legacyAliases {
		if strings.Contains(DefaultEngineerSystemPrompt, marker) {
			t.Fatalf("default engineer prompt still contains legacy tool alias %q", marker)
		}
	}
}

func TestEngineerSystemPromptForContract_OmitsStaticImplementationProtocol(t *testing.T) {
	prompt := EngineerSystemPromptForContract(&shared.TaskExecutionContract{TaskID: "task_1"})
	if strings.Contains(prompt, "# Engineer Agent — Implementation Protocol") {
		t.Fatal("task-scoped engineer prompt should not include the static implementation protocol")
	}
	for _, want := range []string{
		"# Engineer Agent — System",
		"# Engineer Agent — Consultation Policy",
		"# Engineer Agent — Skill Usage Policy",
		"# Engineer Agent — Guardrails",
		"# Engineer Agent — Self-Audit Protocol",
		"# Engineer Agent — Collaboration Protocol",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("task-scoped engineer prompt missing %q", want)
		}
	}
}
