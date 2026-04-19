package archivalist

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

var (
	archivalistBaseSystem            = prompts.MustLoad("archivalist", "system")
	archivalistFabricAwareness       = prompts.MustLoad("shared", "fabric_awareness")
	archivalistFabricAdvisory        = prompts.MustLoad("shared", "fabric_knowledge_advisory")
	SummaryPromptTemplate            = prompts.MustLoad("archivalist", "summary")
	MultiSourceSummaryPromptTemplate = prompts.MustLoad("archivalist", "multi_source_summary")
	AgentBriefingTemplate            = prompts.MustLoad("archivalist", "briefing")
	ClassificationSystemPrompt       = prompts.MustLoad("archivalist", "classification")
	ClassificationExamples           = prompts.MustLoad("archivalist", "classification_examples")

	// DefaultSystemPrompt composes the archivalist's base persona with
	// the uniform fabric awareness + knowledge-agent advisory clause.
	DefaultSystemPrompt = strings.Join([]string{
		archivalistBaseSystem,
		archivalistFabricAwareness,
		archivalistFabricAdvisory,
	}, "\n\n---\n\n")
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
