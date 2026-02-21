package inspector

import "github.com/adalundhe/sylk/prompts"

var (
	DefaultSystemPrompt        = prompts.MustLoad("inspector", "system")
	SeverityThresholds         = prompts.MustLoad("inspector", "severity")
	ValidationPromptTemplate   = prompts.MustLoad("inspector", "validation")
	CriteriaDefinitionTemplate = prompts.MustLoad("inspector", "criteria")
	FeedbackTemplate           = prompts.MustLoad("inspector", "feedback")
	OverrideRequestTemplate    = prompts.MustLoad("inspector", "override")
)
