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
		"end the challenged turn with `validate_global_review`",
		"`validate_global_review`",
	} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Fatalf("default system prompt missing %q", want)
		}
	}
}

func TestDefaultSystemPrompt_IncludesDiscussionTimeConsultationGuidance(t *testing.T) {
	for _, want := range []string{
		"Resolve missing context through Guide-routed knowledge agents as the conversation evolves, not only once formal planning starts.",
		"For the first substantive turn on a new implementation, design, or planning problem, default to the Librarian + Archivalist + Academic triad before you settle on your answer unless one is clearly irrelevant or already fresh.",
		"Use consultation continuously during discussion and discovery, not only after you decide to create a plan.",
		"For the first substantive turn on a new implementation, planning, or architecture problem, your default move is to build an evidence base from Librarian, Archivalist, and Academic unless one is clearly irrelevant or already fresh.",
		"default to consulting all three unless one is clearly irrelevant or you already hold fresh evidence from that source",
		"Treat the Librarian, Archivalist, and Academic together as the architect's normal discussion-time grounding loop for substantive work.",
		"When in doubt, consult rather than assume.",
		"Do not treat the Academic as a rare keyword-triggered escalation.",
		"During discussion before planning:",
		"default to consulting the full knowledge triad: Librarian + Archivalist + Academic",
		"do not wait for keywords like \"research\" or \"benchmark\" to consult the Academic",
	} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Fatalf("default system prompt missing %q", want)
		}
	}
}

func TestArchitectConversationPrompt_InsistsOnKnowledgeTriadDuringDiscussion(t *testing.T) {
	for _, want := range []string{
		"For the first substantive planning, design, or implementation turn on a new problem, default to consulting Librarian, Archivalist, and Academic before you settle on your answer unless one is clearly irrelevant or already fresh.",
		"Continue consulting Librarian, Archivalist, and Academic as the conversation unfolds whenever the user adds material new information, constraints, preferences, scope changes, or technical direction.",
		"Treat the knowledge triad as your normal discussion-time evidence base, not as a rare escalation path.",
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
		"For the first substantive implementation, planning, or architecture turn on a new problem, default to consulting all three before you settle on your answer unless one is clearly",
		"discussion-time evidence base, not as a rare escalation path.",
		"When in doubt, consult rather than assume.",
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
		"are actively grounding your answer in Librarian, Archivalist, and Academic evidence unless",
		"the tool-enabled path would normally consult the full knowledge triad before settling on an answer.",
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
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("conversation system prompt missing %q", want)
		}
	}
}

func TestToolsForConversationMode_ConverseNarrowsToDiscussionTools(t *testing.T) {
	tools := toolsForConversationMode(plannerConversationModeConverse)
	for _, want := range []string{
		"consult",
		"ask_user_question",
		"route_requirements_research",
		"start_planning",
	} {
		if !containsToolName(tools, want) {
			t.Fatalf("converse tools missing %q: %v", want, tools)
		}
	}
	for _, blocked := range []string{
		"plan",
		"plan_workflow",
		"pre_delegation_declare",
		"validate_pre_delegation",
		"monitor_execution",
		"route_plan_acceptance",
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

func TestToolsForConversationMode_ExistingReadyUsesDiscussionTools(t *testing.T) {
	tools := toolsForConversationMode(plannerConversationModeExistingReady)
	if len(tools) != len(discussionConversationTools) {
		t.Fatalf("existing-ready tools len = %d, want %d", len(tools), len(discussionConversationTools))
	}
	for _, want := range discussionConversationTools {
		if !containsToolName(tools, want) {
			t.Fatalf("existing-ready tools missing %q: %v", want, tools)
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
