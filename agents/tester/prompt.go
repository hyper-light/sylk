package tester

import "github.com/adalundhe/sylk/prompts"

var (
	DefaultSystemPrompt      = prompts.MustLoad("tester", "system")
	TestCategoryDescriptions = prompts.MustLoad("tester", "categories")
	PrioritizationFormula    = prompts.MustLoad("tester", "prioritization")
	QualityThresholds        = prompts.MustLoad("tester", "thresholds")
	TestSuggestionTemplate   = prompts.MustLoad("tester", "suggestion")
)
