package librarian

import "github.com/adalundhe/sylk/prompts"

var (
	DefaultSystemPrompt       = prompts.MustLoad("librarian", "system")
	HealthAssessmentPrompt    = prompts.MustLoad("librarian", "health")
	PatternDetectionPrompt    = prompts.MustLoad("librarian", "patterns")
	QueryClassificationPrompt = prompts.MustLoad("librarian", "query_classification")
)
