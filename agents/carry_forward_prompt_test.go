package agents

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/academic"
	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/guide"
	inspectorprompts "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/librarian"
	testerprompts "github.com/adalundhe/sylk/agents/tester/shared"
)

func TestAgentPromptsIncludeCarryForwardContinuityContract(t *testing.T) {
	prompts := map[string]string{
		"academic":                     academic.DefaultSystemPrompt,
		"architect/default":            architect.DefaultSystemPrompt,
		"architect/requirements_stage": architect.ArchitectPlannerPromptForStage("requirements"),
		"architect/design_stage":       architect.ArchitectPlannerPromptForStage("design"),
		"architect/tasks_stage":        architect.ArchitectPlannerPromptForStage("tasks"),
		"designer/default":             designer.DesignerSystemPrompt(),
		"designer/task":                designer.DesignerSystemPromptForContract(nil),
		"engineer":                     engineer.DefaultEngineerSystemPrompt,
		"guide":                        guide.GuideSystemPrompt,
		"inspector/global":             inspectorprompts.GlobalInspectorSystemPrompt(),
		"inspector/pipeline":           inspectorprompts.PipelineInspectorSystemPrompt(),
		"librarian":                    librarian.DefaultSystemPrompt,
		"tester/global":                testerprompts.GlobalTesterSystemPrompt(),
		"tester/pipeline":              testerprompts.PipelineTesterSystemPrompt(),
	}
	for name, prompt := range prompts {
		compact := strings.Join(strings.Fields(prompt), " ")
		for _, want := range []string{"recall_forward", "carry_forward"} {
			if !strings.Contains(compact, want) {
				t.Fatalf("%s prompt missing %q", name, want)
			}
		}
		if !strings.Contains(compact, "testaments and artifacts") &&
			!strings.Contains(compact, "testaments/artifacts") {
			t.Fatalf("%s prompt does not distinguish carried testaments/artifacts:\n%s", name, compact)
		}
		if strings.Contains(compact, "carry claims as evidence") {
			t.Fatalf("%s prompt tells the agent to carry claims as evidence:\n%s", name, compact)
		}
	}
}
