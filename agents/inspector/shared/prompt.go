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

	// Activity Fabric awareness — uniform across all agents — plus the
	// inspector audit-clause extension. Pipeline inspector and global
	// inspector both compose both in.
	InspectorFabricAwareness = prompts.MustLoad("shared", "fabric_awareness")
	InspectorFabricAudit     = prompts.MustLoad("shared", "fabric_inspector_audit")
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
//
// IMPORTANT ORDERING: the fabric awareness + audit clauses come
// BEFORE the workflow protocol so the LLM sees fabric context as
// framing for its work, not as an afterthought. Without this
// ordering the LLM follows the workflow it reads first and never
// reaches for fabric skills.
func PipelineInspectorSystemPrompt() string {
	return pipelineSystem + promptSeparator +
		InspectorFabricAwareness + promptSeparator +
		InspectorFabricAudit + promptSeparator +
		pipelineProtocol + promptSeparator +
		InspectorSkillsPolicy + promptSeparator +
		InspectorGuardrails + promptSeparator +
		agentshared.BuildWorkspaceViewContext(agentshared.WorkspacePromptOptions{
			DefaultView:     versioning.WorkspaceViewPipeline,
			IncludePipeline: true,
		})
}

// PipelineInspectorSystemPromptForContract omits the implementation-validation
// protocol block when the task is still in pre-implementation contract
// synthesis mode.
func PipelineInspectorSystemPromptForContract(contract *agentshared.TaskExecutionContract) string {
	parts := []string{
		pipelineSystem,
		InspectorFabricAwareness, // promote ahead of workflow
		InspectorFabricAudit,
	}
	if contract == nil || !contract.PreImplementation {
		parts = append(parts, pipelineProtocol)
	}
	parts = append(parts, InspectorSkillsPolicy, InspectorGuardrails,
		agentshared.BuildWorkspaceViewContext(agentshared.WorkspacePromptOptions{
			DefaultView:     versioning.WorkspaceViewPipeline,
			IncludePipeline: true,
		}),
	)
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
//
// Same ordering rationale as PipelineInspectorSystemPrompt: fabric
// awareness + audit clauses before the workflow protocol so the LLM
// reaches for inspect_open_activity / query_peer_activity as
// orientation tools, not as bolted-on extras.
func GlobalInspectorSystemPrompt() string {
	return globalSystem + promptSeparator +
		InspectorFabricAwareness + promptSeparator +
		InspectorFabricAudit + promptSeparator +
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
// but the strict global-review protocol still applies and must remain visible.
func GlobalInspectorSystemPromptForContract(contract *agentshared.GlobalExecutionContract) string {
	if contract == nil {
		return GlobalInspectorSystemPrompt()
	}
	return joinNonEmpty([]string{
		globalSystem,
		InspectorFabricAwareness,
		InspectorFabricAudit,
		globalProtocol,
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
