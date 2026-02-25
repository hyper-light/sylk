package designer

import (
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

var (
	DefaultSystemPrompt     = prompts.MustLoad("designer", "system")
	ComponentAnalysisPrompt = prompts.MustLoad("designer", "component")
	DesignPlanPrompt        = prompts.MustLoad("designer", "plan")
	A11yAuditPrompt         = prompts.MustLoad("designer", "a11y")
	TokenValidationPrompt   = prompts.MustLoad("designer", "token")

	DesignerSkillsPolicy  = prompts.MustLoad("designer", "system_skills")
	DesignerGuardrails    = prompts.MustLoad("designer", "system_guardrails")
	DesignerCollaboration = prompts.MustLoad("designer", "collaboration")
)

// DesignerSystemPrompt composes the full system prompt from all prompt sections.
func DesignerSystemPrompt() string {
	var b strings.Builder
	b.WriteString(DefaultSystemPrompt)
	b.WriteString("\n\n")
	b.WriteString(DesignerSkillsPolicy)
	b.WriteString("\n\n")
	b.WriteString(DesignerCollaboration)
	b.WriteString("\n\n")
	b.WriteString(DesignerGuardrails)
	return b.String()
}
