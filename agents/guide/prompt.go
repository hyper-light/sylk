package guide

import (
	"fmt"

	"github.com/adalundhe/sylk/prompts"
)

var (
	ClassificationSystemPrompt     = prompts.MustLoad("guide", "classification")
	ClassificationExamplesTemplate = prompts.MustLoad("guide", "classification_examples")
	GuideSystemPrompt              = prompts.MustLoad("guide", "system")
	HelpDSLSyntax                  = prompts.MustLoad("guide", "help_dsl")
	HelpAgents                     = prompts.MustLoad("guide", "help_agents")
)

// FormatClassificationPrompt formats the classification prompt with optional corrections
func FormatClassificationPrompt(corrections string) string {
	if corrections == "" {
		return ClassificationSystemPrompt
	}
	return ClassificationSystemPrompt + "\n" + fmt.Sprintf(ClassificationExamplesTemplate, corrections)
}
