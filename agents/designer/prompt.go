package designer

import "github.com/adalundhe/sylk/prompts"

var (
	DefaultSystemPrompt     = prompts.MustLoad("designer", "system")
	ComponentAnalysisPrompt = prompts.MustLoad("designer", "component")
	DesignPlanPrompt        = prompts.MustLoad("designer", "plan")
	A11yAuditPrompt         = prompts.MustLoad("designer", "a11y")
	TokenValidationPrompt   = prompts.MustLoad("designer", "token")
)
