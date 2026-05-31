package architect

import (
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

var (
	ArchitectSystemCorePrompt       = prompts.MustLoad("architect", "system")
	ArchitectSystemProtocolPrompt   = prompts.MustLoad("architect", "system_protocol")
	ArchitectSystemConsultPrompt    = prompts.MustLoad("architect", "system_consultation")
	ArchitectSystemDelegationPrompt = prompts.MustLoad("architect", "system_delegation")
	ArchitectSystemOutputPrompt     = prompts.MustLoad("architect", "system_output")
	ArchitectSystemGuardrailsPrompt = prompts.MustLoad("architect", "system_guardrails")
	ArchitectSystemSkillsPrompt     = prompts.MustLoad("architect", "system_skills")
	ArchitectConversationPrompt     = prompts.MustLoad("architect", "conversation")
	ArchitectFabricAwareness        = prompts.MustLoad("shared", "fabric_awareness")
	ArchitectClaimsNative           = prompts.MustLoad("shared", "claims_native")
	// IMPORTANT ORDERING: ArchitectFabricAwareness comes RIGHT
	// AFTER core identity, followed by the claims-native contract,
	// BEFORE protocol/skills. The screenshot review showed the LLM
	// follows the workflow it reads first; fabric and claims must be
	// framing, not footers.
	DefaultSystemPrompt = strings.Join([]string{
		ArchitectSystemCorePrompt,
		ArchitectFabricAwareness,
		ArchitectClaimsNative,
		ArchitectSystemProtocolPrompt,
		ArchitectSystemConsultPrompt,
		ArchitectSystemDelegationPrompt,
		ArchitectSystemOutputPrompt,
		ArchitectSystemGuardrailsPrompt,
		ArchitectSystemSkillsPrompt,
	}, "\n\n---\n\n")
	RequirementsAnalysisPrompt = prompts.MustLoad("architect", "requirements")
	ArchitectureDesignPrompt   = prompts.MustLoad("architect", "design")
	TaskDecompositionPrompt    = prompts.MustLoad("architect", "decomposition")
	WorkflowCreationPrompt     = prompts.MustLoad("architect", "workflow")
)

func ArchitectPlannerPromptForStage(stage string) string {
	modules := plannerPromptModules(stage)
	return strings.Join(modules, "\n\n---\n\n")
}

func plannerPromptModules(stage string) []string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "requirements":
		return []string{
			ArchitectSystemCorePrompt,
			ArchitectFabricAwareness,
			ArchitectClaimsNative,
			ArchitectSystemProtocolPrompt,
			ArchitectSystemConsultPrompt,
			ArchitectSystemGuardrailsPrompt,
			ArchitectSystemOutputPrompt,
		}
	case "design":
		return []string{
			ArchitectSystemCorePrompt,
			ArchitectFabricAwareness,
			ArchitectClaimsNative,
			ArchitectSystemProtocolPrompt,
			ArchitectSystemConsultPrompt,
			ArchitectSystemOutputPrompt,
			ArchitectSystemGuardrailsPrompt,
		}
	case "tasks":
		return []string{
			ArchitectSystemCorePrompt,
			ArchitectFabricAwareness,
			ArchitectClaimsNative,
			ArchitectSystemProtocolPrompt,
			ArchitectSystemDelegationPrompt,
			ArchitectSystemSkillsPrompt,
			ArchitectSystemOutputPrompt,
			ArchitectSystemGuardrailsPrompt,
		}
	default:
		return []string{
			ArchitectSystemCorePrompt,
			ArchitectFabricAwareness, // promoted ahead of workflow
			ArchitectClaimsNative,
			ArchitectSystemProtocolPrompt,
			ArchitectSystemConsultPrompt,
			ArchitectSystemDelegationPrompt,
			ArchitectSystemOutputPrompt,
			ArchitectSystemGuardrailsPrompt,
			ArchitectSystemSkillsPrompt,
		}
	}
}
