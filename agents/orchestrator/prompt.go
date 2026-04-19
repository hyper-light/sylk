package orchestrator

import (
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

var (
	OrchestratorSystemCorePrompt       = prompts.MustLoad("orchestrator", "system")
	OrchestratorSystemProtocolPrompt   = prompts.MustLoad("orchestrator", "system_protocol")
	OrchestratorSystemGuardrailsPrompt = prompts.MustLoad("orchestrator", "system_guardrails")
	OrchestratorSystemSkillsPrompt     = prompts.MustLoad("orchestrator", "system_skills")
	OrchestratorConversationPrompt     = prompts.MustLoad("orchestrator", "conversation")
	OrchestratorSelfResponsePrompt     = prompts.MustLoad("orchestrator", "self_response")
	OrchestratorFabricAwareness        = prompts.MustLoad("shared", "fabric_awareness")

	// DefaultSystemPrompt is the full system prompt for event processing (LLM loop).
	// Includes all modules: core identity, protocol, guardrails, and skill strategy.
	DefaultSystemPrompt = strings.Join(nonEmptyOrchestratorSections([]string{
		OrchestratorSystemCorePrompt,
		OrchestratorSystemProtocolPrompt,
		OrchestratorSystemGuardrailsPrompt,
		OrchestratorSystemSkillsPrompt,
		OrchestratorFabricAwareness,
	}), "\n\n---\n\n")

	// ConversationPrompt is the legacy alias used by existing callers.
	ConversationPrompt = OrchestratorConversationPrompt
)

// OrchestratorConversationSystemPrompt returns the system prompt for conversation mode.
// Core identity + guardrails + conversation persona + tool-use policy.
func OrchestratorConversationSystemPrompt() string {
	return strings.Join(nonEmptyOrchestratorSections([]string{
		OrchestratorSystemCorePrompt,
		OrchestratorSystemGuardrailsPrompt,
		OrchestratorConversationPrompt,
		OrchestratorSelfResponsePrompt,
	}), "\n\n---\n\n")
}

// nonEmptyOrchestratorSections filters out empty or whitespace-only sections.
func nonEmptyOrchestratorSections(sections []string) []string {
	result := make([]string, 0, len(sections))
	for _, section := range sections {
		trimmed := strings.TrimSpace(section)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
