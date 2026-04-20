package designer

import (
	"strings"

	shared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/prompts"
)

var (
	DefaultSystemPrompt     = prompts.MustLoad("designer", "system")
	TaskSystemPrompt        = prompts.MustLoad("designer", "task_system")
	ComponentAnalysisPrompt = prompts.MustLoad("designer", "component")
	DesignPlanPrompt        = prompts.MustLoad("designer", "plan")
	A11yAuditPrompt         = prompts.MustLoad("designer", "a11y")
	TokenValidationPrompt   = prompts.MustLoad("designer", "token")

	DesignerSkillsPolicy   = prompts.MustLoad("designer", "system_skills")
	DesignerGuardrails     = prompts.MustLoad("designer", "system_guardrails")
	DesignerCollaboration  = prompts.MustLoad("designer", "collaboration")
	DesignerFabricAwareness = prompts.MustLoad("shared", "fabric_awareness")

	// DesignerLanguageToolingPreference encodes the priority order —
	// task spec → fabric → codebase — that designer reads before
	// reaching for write or edit skills. Sits next to fabric
	// awareness so the LLM sees both signals as framing.
	DesignerLanguageToolingPreference = prompts.MustLoad("shared", "language_tooling_preference")
)

// DesignerSystemPrompt composes the full system prompt from all
// prompt sections.
//
// IMPORTANT ORDERING: DesignerFabricAwareness comes RIGHT AFTER the
// agent identity prompt, BEFORE skills/collaboration/guardrails.
// The screenshot review showed the LLM follows the workflow it
// reads first; fabric must be framing, not a footer.
func DesignerSystemPrompt() string {
	var b strings.Builder
	b.WriteString(DefaultSystemPrompt)
	b.WriteString("\n\n")
	b.WriteString(DesignerFabricAwareness)
	b.WriteString("\n\n")
	b.WriteString(DesignerLanguageToolingPreference)
	b.WriteString("\n\n")
	b.WriteString(DesignerSkillsPolicy)
	b.WriteString("\n\n")
	b.WriteString(DesignerCollaboration)
	b.WriteString("\n\n")
	b.WriteString(DesignerGuardrails)
	return b.String()
}

// DesignerSystemPromptForContract composes the stock designer prompt for
// task-scoped execution. In task mode, the shared execution contract remains
// the source of workflow obligations while this prompt preserves role,
// quality, and collaboration expectations.
func DesignerSystemPromptForContract(contract *shared.TaskExecutionContract) string {
	if contract == nil {
		return DesignerSystemPrompt()
	}
	return joinPromptSections(
		TaskSystemPrompt,
		DesignerFabricAwareness, // promoted ahead of workflow
		DesignerLanguageToolingPreference,
		DesignerCollaboration,
		DesignerGuardrails,
	)
}

func joinPromptSections(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return strings.Join(filtered, "\n\n")
}
