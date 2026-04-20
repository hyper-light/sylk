package engineer

import (
	"strings"

	shared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/prompts"
)

// Modular system prompt modules — composed into DefaultEngineerSystemPrompt.
var (
	EngineerSystemCorePrompt       = prompts.MustLoad("engineer", "system")
	EngineerSystemProtocolPrompt   = prompts.MustLoad("engineer", "system_protocol")
	EngineerSystemConsultPrompt    = prompts.MustLoad("engineer", "system_consultation")
	EngineerSystemSkillsPrompt     = prompts.MustLoad("engineer", "system_skills")
	EngineerSystemGuardrailsPrompt = prompts.MustLoad("engineer", "system_guardrails")
	EngineerSystemAuditPrompt      = prompts.MustLoad("engineer", "system_audit")
	EngineerSystemCollabPrompt     = prompts.MustLoad("engineer", "system_collaboration")
	EngineerFabricAwarenessPrompt  = prompts.MustLoad("shared", "fabric_awareness")

	// EngineerLanguageToolingPreferencePrompt encodes the priority
	// order — task spec → fabric → codebase — that engineer reads
	// before reaching for write or edit skills. Sits next to fabric
	// awareness so the LLM sees both signals as framing.
	EngineerLanguageToolingPreferencePrompt = prompts.MustLoad("shared", "language_tooling_preference")

	// DefaultEngineerSystemPrompt composes all modules with separators.
	//
	// IMPORTANT ORDERING: EngineerFabricAwarenessPrompt comes RIGHT
	// AFTER core identity, BEFORE protocol/skills. The screenshot
	// review showed the LLM follows the workflow it reads first;
	// fabric must be framing, not a footer.
	DefaultEngineerSystemPrompt = strings.Join([]string{
		EngineerSystemCorePrompt,
		EngineerFabricAwarenessPrompt,
		EngineerLanguageToolingPreferencePrompt,
		EngineerSystemProtocolPrompt,
		EngineerSystemConsultPrompt,
		EngineerSystemSkillsPrompt,
		EngineerSystemGuardrailsPrompt,
		EngineerSystemAuditPrompt,
		EngineerSystemCollabPrompt,
	}, "\n\n---\n\n")

	// DefaultSystemPrompt is the composed system prompt (alias for backward compat).
	DefaultSystemPrompt = DefaultEngineerSystemPrompt
)

// EngineerSystemPromptForContract composes the stock engineer prompt for
// task-scoped execution. When a structured task contract is present, the
// runtime-supplied contract guidance replaces the static implementation
// protocol section so there is a single source of task workflow truth.
func EngineerSystemPromptForContract(contract *shared.TaskExecutionContract) string {
	if contract == nil {
		return DefaultEngineerSystemPrompt
	}
	return strings.Join([]string{
		EngineerSystemCorePrompt,
		EngineerFabricAwarenessPrompt, // promoted ahead of workflow
		EngineerLanguageToolingPreferencePrompt,
		EngineerSystemConsultPrompt,
		EngineerSystemSkillsPrompt,
		EngineerSystemGuardrailsPrompt,
		EngineerSystemAuditPrompt,
		EngineerSystemCollabPrompt,
	}, "\n\n---\n\n")
}

// Task-specific prompt templates (unchanged).
var (
	TaskAnalysisPrompt       = prompts.MustLoad("engineer", "analysis")
	ImplementationPlanPrompt = prompts.MustLoad("engineer", "implementation")
	CodeReviewPrompt         = prompts.MustLoad("engineer", "review")
	ErrorAnalysisPrompt      = prompts.MustLoad("engineer", "error")
)
