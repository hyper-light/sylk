package guide

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

var (
	ClassificationSystemPrompt     = prompts.MustLoad("guide", "classification")
	ClassificationExamplesTemplate = prompts.MustLoad("guide", "classification_examples")
	ClassificationDomainPrompt     = prompts.MustLoad("guide", "domain")
	GuideSystemPrompt              = prompts.MustLoad("guide", "system")
	HelpDSLSyntax                  = prompts.MustLoad("guide", "help_dsl")
	HelpAgents                     = prompts.MustLoad("guide", "help_agents")
)

// BuildClassificationPrompt composes prompt modules based on request content.
func BuildClassificationPrompt(input string) string {
	sections := []string{ClassificationSystemPrompt}
	if shouldIncludeClassificationDomainModule(input) {
		sections = append(sections, ClassificationDomainPrompt)
	}
	return strings.Join(sections, "\n\n")
}

// FormatClassificationPrompt formats the classification prompt with optional corrections
func FormatClassificationPrompt(corrections string) string {
	base := BuildClassificationPrompt("")
	if corrections == "" {
		return base
	}
	return base + "\n" + fmt.Sprintf(ClassificationExamplesTemplate, corrections)
}

func shouldIncludeClassificationDomainModule(input string) bool {
	query := strings.ToLower(strings.TrimSpace(input))
	if query == "" {
		return true
	}
	keywords := []string{"domain", "route", "agent", "classify", "taxonomy"}
	for _, keyword := range keywords {
		if strings.Contains(query, keyword) {
			return true
		}
	}
	return false
}
