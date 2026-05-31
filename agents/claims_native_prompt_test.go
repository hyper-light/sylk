package agents

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/academic"
	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/archivalist"
	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/guide"
	inspectorprompts "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/librarian"
	"github.com/adalundhe/sylk/agents/orchestrator"
	testerprompts "github.com/adalundhe/sylk/agents/tester/shared"
)

func TestAgentPromptsIncludeClaimsNativeOperatingContract(t *testing.T) {
	prompts := map[string]string{
		"academic":                     academic.DefaultSystemPrompt,
		"architect/default":            architect.DefaultSystemPrompt,
		"architect/requirements_stage": architect.ArchitectPlannerPromptForStage("requirements"),
		"architect/design_stage":       architect.ArchitectPlannerPromptForStage("design"),
		"architect/tasks_stage":        architect.ArchitectPlannerPromptForStage("tasks"),
		"archivalist":                  archivalist.DefaultSystemPrompt,
		"designer/default":             designer.DesignerSystemPrompt(),
		"designer/task":                designer.DesignerSystemPromptForContract(nil),
		"engineer/default":             engineer.DefaultEngineerSystemPrompt,
		"engineer/task":                engineer.EngineerSystemPromptForContract(nil),
		"guide":                        guide.GuideSystemPrompt,
		"inspector/global":             inspectorprompts.GlobalInspectorSystemPrompt(),
		"inspector/pipeline":           inspectorprompts.PipelineInspectorSystemPrompt(),
		"librarian":                    librarian.DefaultSystemPrompt,
		"orchestrator/default":         orchestrator.DefaultSystemPrompt,
		"orchestrator/conversation":    orchestrator.OrchestratorConversationSystemPrompt(),
		"tester/global":                testerprompts.GlobalTesterSystemPrompt(),
		"tester/pipeline":              testerprompts.PipelineTesterSystemPrompt(),
		"tester/conversation":          testerprompts.TesterConversationSystemPrompt(),
	}
	for name, prompt := range prompts {
		compact := strings.Join(strings.Fields(prompt), " ")
		for _, want := range []string{
			"Claims, Testaments, And Artifacts Operating Contract",
			"Claims are first-class work inputs",
			"expected_tool_calls",
			"Errors are artifacts for testaments",
			"submit_testaments",
			"evaluate_validation",
		} {
			if !strings.Contains(compact, want) {
				t.Fatalf("%s prompt missing %q", name, want)
			}
		}
	}
}
