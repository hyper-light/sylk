package shared

import "github.com/adalundhe/sylk/prompts"

// Shared prompts used by both inspector variants.
var (
	InspectorSkillsPolicy = prompts.MustLoad("inspector", "system_skills")
	InspectorGuardrails   = prompts.MustLoad("inspector", "system_guardrails")
)

// Pipeline inspector prompts.
var (
	pipelineSystem       = prompts.MustLoad("inspector-pipeline", "system")
	pipelineProtocol     = prompts.MustLoad("inspector-pipeline", "system_protocol")
	pipelineConversation = prompts.MustLoad("inspector-pipeline", "conversation")
	pipelineCorrection   = prompts.MustLoad("inspector-pipeline", "correction")
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
		InspectorGuardrails
}

// GlobalConversationPrompt returns the global conversation prompt.
func GlobalConversationPrompt() string {
	return globalConversation
}
