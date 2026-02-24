package shared

import "github.com/adalundhe/sylk/prompts"

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
)

// PipelineTesterSystemPrompt composes the full pipeline tester system prompt.
func PipelineTesterSystemPrompt() string {
	return PipelineSystemPrompt + "\n\n" +
		DiagnosisMethodology + "\n\n" +
		TestPlanningStrategy + "\n\n" +
		TesterSkillsPolicy + "\n\n" +
		TesterGuardrails
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
