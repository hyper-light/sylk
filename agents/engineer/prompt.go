package engineer

import "github.com/adalundhe/sylk/prompts"

var (
	DefaultSystemPrompt      = prompts.MustLoad("engineer", "system")
	TaskAnalysisPrompt       = prompts.MustLoad("engineer", "analysis")
	ImplementationPlanPrompt = prompts.MustLoad("engineer", "implementation")
	CodeReviewPrompt         = prompts.MustLoad("engineer", "review")
	ErrorAnalysisPrompt      = prompts.MustLoad("engineer", "error")
)
