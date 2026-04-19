package shared

import (
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/adalundhe/sylk/prompts"
)

// Prompt variables loaded from embedded markdown files.
var (
	// Shared prompts used by both variants.
	DiagnosisMethodology   = prompts.MustLoad("tester", "diagnosis")
	TestPlanningStrategy   = prompts.MustLoad("tester", "test_planning")
	HarnessDesign          = prompts.MustLoad("tester", "harness")
	TesterSkillsPolicy     = prompts.MustLoad("tester", "system_skills")
	TesterGuardrails       = prompts.MustLoad("tester", "system_guardrails")
	TesterTaskSystem       = prompts.MustLoad("tester", "pipeline_task_system")
	TesterGlobalTaskSystem = prompts.MustLoad("tester", "global_task_system")

	// Existing shared prompts (kept for both variants).
	TestCategoryDescriptions = prompts.MustLoad("tester", "categories")
	PrioritizationFormula    = prompts.MustLoad("tester", "prioritization")
	QualityThresholds        = prompts.MustLoad("tester", "thresholds")
	TestSuggestionTemplate   = prompts.MustLoad("tester", "suggestion")

	// Pipeline Tester system prompt.
	PipelineSystemPrompt = prompts.MustLoad("tester", "pipeline_system")

	// Global Tester system prompt.
	GlobalSystemPrompt = prompts.MustLoad("tester", "system")

	// Design validation context for pipeline tester.
	pipelineDesignValidation = prompts.MustLoad("tester", "design_validation")

	// Conversation mode prompt for chat intents.
	TesterConversationPrompt = prompts.MustLoad("tester", "conversation")

	// Activity Fabric awareness — uniform section appended to every
	// tester variant's system prompt. Pipeline tester and global
	// tester both compose this in.
	TesterFabricAwareness = prompts.MustLoad("shared", "fabric_awareness")
)

// PipelineTesterSystemPrompt composes the full pipeline tester
// system prompt.
//
// IMPORTANT ORDERING: TesterFabricAwareness is now positioned RIGHT
// AFTER the agent identity prompt, BEFORE methodology and workflow.
// This is the structural fix per the screenshot review — when the
// fabric section was at the end, the LLM followed the workflow it
// read first and never reached for fabric tools. At the top, fabric
// awareness becomes framing for the work.
func PipelineTesterSystemPrompt() string {
	return PipelineSystemPrompt + "\n\n" +
		TesterFabricAwareness + "\n\n" +
		DiagnosisMethodology + "\n\n" +
		TestPlanningStrategy + "\n\n" +
		HarnessDesign + "\n\n" +
		TesterSkillsPolicy + "\n\n" +
		TesterGuardrails
}

// PipelineTesterSystemPromptForWorker composes the pipeline tester system prompt,
// appending design validation context when workerType is "designer".
func PipelineTesterSystemPromptForWorker(workerType string) string {
	base := PipelineTesterSystemPrompt()
	if workerType == "designer" {
		return base + "\n\n" + pipelineDesignValidation
	}
	return base
}

// PipelineTesterSystemPromptForWorkerAndContract composes the pipeline tester
// system prompt for task-scoped execution. When a structured task contract is
// present, the runtime-provided execution contract is the source of workflow
// obligations, so the prompt stays focused on role, quality, and testing
// principles instead of repeating a static phase machine.
func PipelineTesterSystemPromptForWorkerAndContract(workerType string, contract *agentshared.TaskExecutionContract) string {
	if contract == nil {
		return PipelineTesterSystemPromptForWorker(workerType)
	}
	base := joinPromptSections(
		TesterTaskSystem,
		TesterFabricAwareness, // promoted ahead of workflow
		DiagnosisMethodology,
		TestPlanningStrategy,
		HarnessDesign,
		TestCategoryDescriptions,
		TesterGuardrails,
	)
	if workerType == "designer" {
		return base + "\n\n" + pipelineDesignValidation
	}
	return base
}

// GlobalTesterSystemPrompt composes the full global tester system
// prompt. Same ordering rationale as PipelineTesterSystemPrompt:
// fabric awareness is framing, not footer.
func GlobalTesterSystemPrompt() string {
	return GlobalSystemPrompt + "\n\n" +
		TesterFabricAwareness + "\n\n" +
		DiagnosisMethodology + "\n\n" +
		TestPlanningStrategy + "\n\n" +
		HarnessDesign + "\n\n" +
		TesterSkillsPolicy + "\n\n" +
		TesterGuardrails + "\n\n" +
		agentshared.BuildWorkspaceViewContext(agentshared.WorkspacePromptOptions{
			DefaultView: versioning.WorkspaceViewGlobal,
		})
}

// GlobalTesterSystemPromptForContract composes the global tester prompt for
// structured global task requests. When a global execution contract is present,
// the runtime guidance drives workflow obligations while this prompt preserves
// role, testing standards, and workspace context.
func GlobalTesterSystemPromptForContract(contract *agentshared.GlobalExecutionContract) string {
	if contract == nil {
		return GlobalTesterSystemPrompt()
	}
	return joinPromptSections(
		TesterGlobalTaskSystem,
		TesterFabricAwareness, // promoted ahead of workflow
		DiagnosisMethodology,
		TestPlanningStrategy,
		HarnessDesign,
		TestCategoryDescriptions,
		TesterGuardrails,
		agentshared.BuildWorkspaceViewContext(agentshared.WorkspacePromptOptions{
			DefaultView: versioning.WorkspaceViewGlobal,
		}),
	)
}

// TesterConversationSystemPrompt returns the system prompt for conversation mode.
// Uses only the conversation persona. Task-mode workflow modules and gate
// requirements are excluded so the LLM responds to direct user chat without
// inheriting structured task obligations.
func TesterConversationSystemPrompt() string {
	return strings.TrimSpace(agentshared.AppendWorkspaceViewContext(
		TesterConversationPrompt,
		agentshared.WorkspacePromptOptions{DefaultView: versioning.WorkspaceViewGlobal},
	))
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
