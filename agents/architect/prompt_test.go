package architect

import (
	"strings"
	"testing"
)

func compactPromptWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func TestDefaultSystemPrompt_ComposesModulesOnceInOrder(t *testing.T) {
	modules := []string{
		"# THE ARCHITECT",
		"## Planning Protocol",
		"## Consultation Policy",
		"## Delegation and Handoff",
		"## Output Contract",
		"## Guardrails",
		"## Skill Use Policy",
	}

	lastIdx := -1
	for _, marker := range modules {
		count := strings.Count(DefaultSystemPrompt, marker)
		if count != 1 {
			t.Fatalf("marker %q appears %d times, want 1", marker, count)
		}
		idx := strings.Index(DefaultSystemPrompt, marker)
		if idx <= lastIdx {
			t.Fatalf("marker %q appears out of order", marker)
		}
		lastIdx = idx
	}
}

func TestArchitectSystemCorePrompt_DoesNotDuplicateModuleHeadings(t *testing.T) {
	disallowed := []string{
		"## Planning Protocol",
		"## Consultation Policy",
		"## Delegation and Handoff",
		"## Output Contract",
		"## Guardrails",
		"## Skill Use Policy",
	}

	for _, marker := range disallowed {
		if strings.Contains(ArchitectSystemCorePrompt, marker) {
			t.Fatalf("core prompt contains delegated module heading %q", marker)
		}
	}
}

func TestDefaultSystemPrompt_IncludesGlobalReviewChallengeGuidance(t *testing.T) {
	for _, want := range []string{
		"When the global inspector challenges your plan or rationale, treat that as a first-class design review.",
		"later workflow tasks may still be pending or in progress",
		"only call planned work \"missing\" during a checkpoint",
		"you may freely consult the orchestrator whenever that context helps you assess, defend, or revise the plan",
		"end the challenged turn with `validate_work`",
		"`validate_work`",
	} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Fatalf("default system prompt missing %q", want)
		}
	}
}

func TestDefaultSystemPrompt_IncludesDiscussionTimeConsultationGuidance(t *testing.T) {
	for _, want := range []string{
		"Resolve missing context through Guide-routed knowledge agents as the conversation evolves, not only once formal planning starts.",
		"Use consultation continuously during discussion and discovery, not only after you decide to create a plan.",
		"start building a consultation-backed evidence base immediately",
		"Prefer repeated, targeted consults over one broad omnibus consult.",
		"Re-evaluate Academic research depth continuously based on the user's latest input",
		"Do not treat the Academic as a rare keyword-triggered escalation.",
		"`architect_forest_get_plan_precedents`",
		"`architect_forest_compare_plan_branches`",
		"During discussion before planning:",
		"start with the most relevant knowledge agent and the narrowest question that can materially reduce the next uncertainty",
		"do not wait for keywords like \"research\" or \"benchmark\" to consult the Academic",
	} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Fatalf("default system prompt missing %q", want)
		}
	}
}

func TestArchitectConversationPrompt_UsesContinuousTargetedConsultation(t *testing.T) {
	for _, want := range []string{
		"start by consulting the most relevant knowledge agent with the narrowest question that can materially reduce the next uncertainty.",
		"Continue consulting as the conversation unfolds whenever the user adds material new information, constraints, preferences, scope changes, or technical direction.",
		"Prefer repeated targeted consults over one broad consult that tries to answer the whole problem at once.",
		"Re-evaluate Academic research depth as the conversation sharpens.",
		"Prefer consulting the knowledge agents over asking the user questions that you can resolve from codebase reality, historical precedent, or stronger architectural research.",
	} {
		if !strings.Contains(ArchitectConversationPrompt, want) {
			t.Fatalf("architect conversation prompt missing %q", want)
		}
	}
}

func TestPlannerConversationModeConverse_InsistsOnDiscussionTimeConsultation(t *testing.T) {
	text := compactPromptWhitespace(plannerConversationModeInstructions(plannerConversationModeConverse))
	for _, want := range []string{
		"start with the most relevant knowledge agent and the narrowest question that can materially reduce the next uncertainty.",
		"Prefer repeated targeted consults over one broad omnibus consult.",
		"Re-evaluate Academic depth as the user's constraints evolve and your own understanding improves:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("converse instructions missing %q", want)
		}
	}
}

func TestTextOnlyModeConverse_PreservesConsultationPosture(t *testing.T) {
	text := compactPromptWhitespace(textOnlyModeInstructions(plannerConversationModeConverse))
	for _, want := range []string{
		"During substantive implementation, planning, or architecture discussion, reason as if you",
		"are actively grounding your answer in Librarian, Archivalist, and Academic evidence, using the",
		"tool-enabled path would normally start with the most relevant knowledge agent and",
		"Prefer answers that reflect codebase reality, historical precedent, and stronger architectural",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text-only converse instructions missing %q", want)
		}
	}
}

func TestBuildPlannerConversationSystemPrompt_IncludesConsultationAndSkillsPolicy(t *testing.T) {
	text := buildPlannerConversationSystemPrompt(DefaultSystemPrompt)
	for _, want := range []string{
		"## Consultation Policy",
		"## Skill Use Policy",
		"`architect_forest_get_plan_precedents`",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("conversation system prompt missing %q", want)
		}
	}
}

func TestToolsForConversationMode_ConverseIncludesPlanningProtocolTools(t *testing.T) {
	tools := toolsForConversationMode(plannerConversationModeConverse)
	for _, want := range []string{
		"consult",
		"ask_user_question",
		"route_requirements_research",
		"start_planning",
		"plan",
		"route_plan_acceptance",
	} {
		if !containsToolName(tools, want) {
			t.Fatalf("converse tools missing %q: %v", want, tools)
		}
	}
	for _, blocked := range []string{
		"plan_workflow",
		"pre_delegation_declare",
		"validate_pre_delegation",
		"monitor_execution",
		"read_file",
		"glob",
		"grep",
		"git",
		"ast_grep_search",
		"lsp",
	} {
		if containsToolName(tools, blocked) {
			t.Fatalf("converse tools unexpectedly include %q: %v", blocked, tools)
		}
	}
}

func TestToolsForConversationMode_ExistingReadyIncludesAcceptanceAndReplanningTools(t *testing.T) {
	tools := toolsForConversationMode(plannerConversationModeExistingReady)
	if len(tools) != len(planningConversationTools) {
		t.Fatalf("existing-ready tools len = %d, want %d", len(tools), len(planningConversationTools))
	}
	for _, want := range planningConversationTools {
		if !containsToolName(tools, want) {
			t.Fatalf("existing-ready tools missing %q: %v", want, tools)
		}
	}
}

func TestPlannerConversationModeConverse_ToolSurfaceMatchesProtocolInstructions(t *testing.T) {
	text := compactPromptWhitespace(plannerConversationModeInstructions(plannerConversationModeConverse))
	for _, want := range []string{
		"invoke plan(analyze), consult(pre_planning), plan(design), plan(generate_tasks)",
		"Do NOT invoke route_plan_acceptance — wait for the user's next message.",
		"Frame it as plan review, not execution kickoff.",
		"Avoid phrases like \"kick it off\", \"start building\", \"start implementing\", \"get started\", or \"ship it\".",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("converse instructions missing %q", want)
		}
	}
	tools := toolsForConversationMode(plannerConversationModeConverse)
	for _, wantTool := range []string{"plan", "consult", "route_plan_acceptance"} {
		if !containsToolName(tools, wantTool) {
			t.Fatalf("converse tools missing protocol-required tool %q: %v", wantTool, tools)
		}
	}
}

func TestGenerateTasksNextAction_UsesPlanReviewNotExecutionKickoff(t *testing.T) {
	text := compactPromptWhitespace(generateTasksNextAction(false))
	for _, want := range []string{
		"Frame it as plan review, not execution kickoff.",
		"Avoid phrases like \"kick it off\", \"start building\", \"start implementing\", \"get started\", or \"ship it\".",
		"Do NOT invoke route_plan_acceptance — wait for the user's response.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generateTasksNextAction(false) missing %q", want)
		}
	}
}

func containsToolName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
