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
		"Use `handoff_next` for ordinary top-level design handoff back into the pipeline flow.",
		"Use `validate_work` only when you are directly answering an active challenge from Inspector, Tester, or Engineer.",
		"Do not reinterpret a targeted challenge turn as permission to restart the broad top-level design flow.",
		"## Completion Standards",
		"## COLLABORATION PROTOCOL",
		"## SAFETY CONSTRAINTS AND RULES",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("task-scoped designer prompt missing %q", want)
		}
	}
}
