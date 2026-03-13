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
		"## Completion Standards",
		"## COLLABORATION PROTOCOL",
		"## SAFETY CONSTRAINTS AND RULES",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("task-scoped designer prompt missing %q", want)
		}
	}
}
