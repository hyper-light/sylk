package shared

import (
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

// Prompt variables loaded from embedded markdown files.
var (
	// Shared prompts used by both variants.
	DiagnosisMethodology = prompts.MustLoad("tester", "diagnosis")
	TestPlanningStrategy = prompts.MustLoad("tester", "test_planning")
	HarnessDesign        = prompts.MustLoad("tester", "harness")
	TesterSkillsPolicy   = prompts.MustLoad("tester", "system_skills")
	TesterGuardrails     = prompts.MustLoad("tester", "system_guardrails")

	// Existing shared prompts (kept for both variants).
	TestCategoryDescriptions = prompts.MustLoad("tester", "categories")
	PrioritizationFormula    = prompts.MustLoad("tester", "prioritization")
	QualityThresholds        = prompts.MustLoad("tester", "thresholds")
	TestSuggestionTemplate   = prompts.MustLoad("tester", "suggestion")

	// Pipeline Tester system prompt.
	PipelineSystemPrompt = prompts.MustLoad("tester", "pipeline_system")

	// Global Tester system prompt.
	GlobalSystemPrompt = prompts.MustLoad("tester", "system")

	// Design validation context for pipeline tester.
	pipelineDesignValidation = prompts.MustLoad("tester", "design_validation")

	// Conversation mode prompt for chat intents.
	TesterConversationPrompt = prompts.MustLoad("tester", "conversation")
)

// PipelineTesterSystemPrompt composes the full pipeline tester system prompt.
func PipelineTesterSystemPrompt() string {
	return PipelineSystemPrompt + "\n\n" +
		DiagnosisMethodology + "\n\n" +
		TestPlanningStrategy + "\n\n" +
		TesterSkillsPolicy + "\n\n" +
		TesterGuardrails
}

// PipelineTesterSystemPromptForWorker composes the pipeline tester system prompt,
// appending design validation context when workerType is "designer".
func PipelineTesterSystemPromptForWorker(workerType string) string {
	base := PipelineTesterSystemPrompt()
	if workerType == "designer" {
		return base + "\n\n" + pipelineDesignValidation
	}
	return base
}

// GlobalTesterSystemPrompt composes the full global tester system prompt.
func GlobalTesterSystemPrompt() string {
	return GlobalSystemPrompt + "\n\n" +
		DiagnosisMethodology + "\n\n" +
		TestPlanningStrategy + "\n\n" +
		HarnessDesign + "\n\n" +
		TesterSkillsPolicy + "\n\n" +
		TesterGuardrails
}

// TesterConversationSystemPrompt returns the system prompt for conversation mode.
// Uses only the conversation persona — the pipeline-specific GlobalSystemPrompt
// (Inspector gate, 7-phase protocol, skill schemas) and TesterGuardrails
// (gate prerequisite) are excluded so the LLM responds to direct user chat
// without blocking on pipeline prerequisites.
func TesterConversationSystemPrompt() string {
	return strings.TrimSpace(TesterConversationPrompt)
}

