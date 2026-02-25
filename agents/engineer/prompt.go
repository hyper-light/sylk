package engineer

import (
	"strings"

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

	// DefaultEngineerSystemPrompt composes all modules with separators.
	DefaultEngineerSystemPrompt = strings.Join([]string{
		EngineerSystemCorePrompt,
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

// Task-specific prompt templates (unchanged).
var (
	TaskAnalysisPrompt       = prompts.MustLoad("engineer", "analysis")
	ImplementationPlanPrompt = prompts.MustLoad("engineer", "implementation")
	CodeReviewPrompt         = prompts.MustLoad("engineer", "review")
	ErrorAnalysisPrompt      = prompts.MustLoad("engineer", "error")
)
