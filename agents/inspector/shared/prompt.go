package shared

import (
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/adalundhe/sylk/prompts"
)

// Shared prompts used by both inspector variants.
var (
	InspectorSkillsPolicy = prompts.MustLoad("inspector", "system_skills")
	InspectorGuardrails   = prompts.MustLoad("inspector", "system_guardrails")
)

// Pipeline inspector prompts.
var (
	pipelineSystem           = prompts.MustLoad("inspector-pipeline", "system")
	pipelineProtocol         = prompts.MustLoad("inspector-pipeline", "system_protocol")
	pipelineConversation     = prompts.MustLoad("inspector-pipeline", "conversation")
	pipelineCorrection       = prompts.MustLoad("inspector-pipeline", "correction")
	pipelineDesignValidation = prompts.MustLoad("inspector-pipeline", "design_validation")
)

// Global inspector prompts.
var (
	globalSystem       = prompts.MustLoad("inspector", "system")
	globalProtocol     = prompts.MustLoad("inspector", "system_protocol")
	globalAudit        = prompts.MustLoad("inspector", "system_audit")
	globalConversation = prompts.MustLoad("inspector", "conversation")
)

const promptSeparator = "\n\n---\n\n"

// PipelineInspectorSystemPrompt composes the full pipeline inspector system prompt.
func PipelineInspectorSystemPrompt() string {
	return pipelineSystem + promptSeparator +
		pipelineProtocol + promptSeparator +
		InspectorSkillsPolicy + promptSeparator +
		InspectorGuardrails
}

// PipelineInspectorSystemPromptForContract omits the implementation-validation
// protocol block when the task is still in pre-implementation contract
// synthesis mode.
func PipelineInspectorSystemPromptForContract(contract *agentshared.TaskExecutionContract) string {
	parts := []string{pipelineSystem}
	if contract == nil || !contract.PreImplementation {
		parts = append(parts, pipelineProtocol)
	}
	parts = append(parts, InspectorSkillsPolicy, InspectorGuardrails)
	return joinNonEmpty(parts, promptSeparator)
}

// PipelineInspectorSystemPromptForDomain composes the pipeline inspector system
// prompt, appending design validation context when domain is DomainDesign.
func PipelineInspectorSystemPromptForDomain(domain ValidationDomain) string {
	base := PipelineInspectorSystemPrompt()
	if domain == DomainDesign {
		return base + promptSeparator + pipelineDesignValidation
	}
	return base
}

// ValidationDomainFromWorkerType converts a workerType string to a ValidationDomain.
func ValidationDomainFromWorkerType(workerType string) ValidationDomain {
	if workerType == "designer" {
		return DomainDesign
	}
	return DomainCode
}

// PipelineConversationPrompt returns the pipeline conversation prompt.
func PipelineConversationPrompt() string {
	return pipelineConversation
}

// PipelineCorrectionPrompt returns the pipeline correction template.
func PipelineCorrectionPrompt() string {
	return pipelineCorrection
}

// GlobalInspectorSystemPrompt composes the full global inspector system prompt.
func GlobalInspectorSystemPrompt() string {
	return globalSystem + promptSeparator +
		globalProtocol + promptSeparator +
		globalAudit + promptSeparator +
		InspectorSkillsPolicy + promptSeparator +
		InspectorGuardrails + promptSeparator +
		agentshared.BuildWorkspaceViewContext(agentshared.WorkspacePromptOptions{
			DefaultView: versioning.WorkspaceViewGlobal,
		})
}

// GlobalInspectorSystemPromptForContract composes the global inspector system
// prompt for structured global task requests. When a global execution contract
// is present, the runtime guidance becomes the source of workflow obligations,
// so the static audit protocol is omitted.
func GlobalInspectorSystemPromptForContract(contract *agentshared.GlobalExecutionContract) string {
	if contract == nil {
		return GlobalInspectorSystemPrompt()
	}
	return joinNonEmpty([]string{
		globalSystem,
		globalAudit,
		InspectorSkillsPolicy,
		InspectorGuardrails,
		agentshared.BuildWorkspaceViewContext(agentshared.WorkspacePromptOptions{
			DefaultView: versioning.WorkspaceViewGlobal,
		}),
	}, promptSeparator)
}

// GlobalConversationPrompt returns the global conversation prompt.
func GlobalConversationPrompt() string {
	return globalConversation
}

// GlobalInspectorConversationSystemPrompt composes the system prompt for
// conversational interactions — identity + guardrails + conversation persona.
func GlobalInspectorConversationSystemPrompt() string {
	return joinNonEmpty([]string{
		globalSystem,
		InspectorGuardrails,
		globalConversation,
		agentshared.BuildWorkspaceViewContext(agentshared.WorkspacePromptOptions{
			DefaultView: versioning.WorkspaceViewGlobal,
		}),
	}, promptSeparator)
}

// joinNonEmpty joins non-empty trimmed strings with a separator.
func joinNonEmpty(parts []string, sep string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return strings.Join(filtered, sep)
}
