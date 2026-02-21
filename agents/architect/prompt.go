package architect

import "github.com/adalundhe/sylk/prompts"

var (
	DefaultSystemPrompt        = prompts.MustLoad("architect", "system")
	RequirementsAnalysisPrompt = prompts.MustLoad("architect", "requirements")
	ArchitectureDesignPrompt   = prompts.MustLoad("architect", "design")
	TaskDecompositionPrompt    = prompts.MustLoad("architect", "decomposition")
	WorkflowCreationPrompt     = prompts.MustLoad("architect", "workflow")
)
