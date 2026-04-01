package designer

import (
	"strings"
	"testing"

	shared "github.com/adalundhe/sylk/agents/shared"
)

func TestDesignerSystemPromptForContract_UsesTaskModePrompt(t *testing.T) {
	prompt := DesignerSystemPromptForContract(&shared.TaskExecutionContract{TaskID: "task_1"})
	if strings.Contains(prompt, "## 6-PHASE LLM-DRIVEN PROTOCOL") {
		t.Fatal("task-scoped designer prompt should not include the static phase protocol")
	}
	for _, want := range []string{
		"# THE DESIGNER",
		"Use them as the source of workflow truth.",
		"`designer_forest_get_preference_prior`",
		"`designer_forest_discover_adjacent_value`",
		"Your first `challenge_agent` call to Tester, Engineer, or Inspector is allowed.",
		"Re-challenge Inspector only after Inspector answered your prior challenge and you then changed pipeline VFS state yourself based on that answer.",
		"## Completion Standards",
		"## COLLABORATION PROTOCOL",
		"## SAFETY CONSTRAINTS AND RULES",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("task-scoped designer prompt missing %q", want)
		}
	}
}
