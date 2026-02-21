package archivalist

import (
	"fmt"

	"github.com/adalundhe/sylk/prompts"
)

var (
	DefaultSystemPrompt              = prompts.MustLoad("archivalist", "system")
	SummaryPromptTemplate            = prompts.MustLoad("archivalist", "summary")
	MultiSourceSummaryPromptTemplate = prompts.MustLoad("archivalist", "multi_source_summary")
	AgentBriefingTemplate            = prompts.MustLoad("archivalist", "briefing")
	ClassificationSystemPrompt       = prompts.MustLoad("archivalist", "classification")
	ClassificationExamples           = prompts.MustLoad("archivalist", "classification_examples")
)

func FormatSummaryPrompt(content string) string {
	return fmt.Sprintf(SummaryPromptTemplate, content)
}

func FormatMultiSourcePrompt(count int, content string) string {
	return fmt.Sprintf(MultiSourceSummaryPromptTemplate, count, content)
}

func FormatAgentBriefingPrompt(context string) string {
	return fmt.Sprintf(AgentBriefingTemplate, context)
}

func FormatClassificationPrompt(learnedExamples string) string {
	if learnedExamples == "" {
		return ClassificationSystemPrompt
	}
	return ClassificationSystemPrompt + "\n" + fmt.Sprintf(ClassificationExamples, learnedExamples)
}
