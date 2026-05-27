package architect

import (
	"context"
	"strings"
	"testing"

	shared "github.com/adalundhe/sylk/agents/shared"
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
		"Resolve missing context through your own carried-forward testaments/artifacts first, then through Guide-routed knowledge agents as the conversation evolves.",
		"When the user continues, approves, revises, or asks to formalize the same stable topic, call `recall_forward(topic=…)` before issuing another `consult_peer`.",
		"If recall returns `status=usable` with `usable=true` and answers the repository, historical, or design uncertainty, use it and skip the duplicate consult.",
		"Use consultation continuously during discussion and discovery, not only after you decide to create a plan.",
		"start building a consultation-backed evidence base immediately",
		"do not invoke `plan(action=start)` or `plan(action=analyze)` before that first targeted `consult_peer`",
		"Prefer targeted consults over one broad omnibus consult, but do not repeat a fresh target/query unless new information creates a material gap.",
		"Re-evaluate Academic research depth continuously based on the user's latest input",
		"Do not treat the Academic as a rare keyword-triggered escalation.",
		"`architect_forest_consult(purpose=get_plan_precedents, query=…)`",
		"`architect_forest_consult(purpose=compare_plan_branches, query=…)`",
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
		"call `recall_forward(topic=…)` before consulting a knowledge agent again.",
		"If recall returns `status=usable` with `usable=true` and answers the uncertainty, use that source-indexed continuity and skip the duplicate consult.",
		"start by consulting the most relevant knowledge agent with the narrowest question that can materially reduce the next uncertainty.",
		"do not invoke `plan(action=start)` or `plan(action=analyze)` before that first targeted `consult_peer`",
		"Continue consulting as the conversation unfolds whenever the user adds material new information, constraints, preferences, scope changes, or technical direction.",
		"Prefer targeted consults over one broad consult, but do not repeat a fresh target/query unless new information creates a material gap.",
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
		"use recall_forward(topic=...) first when the user is continuing, approving, revising, or asking to plan the same stable topic.",
		"If recall_forward returns status=usable with usable=true, use that source-indexed continuity and do not repeat a fresh consult_peer call.",
		"start with the most relevant knowledge agent and the narrowest question that can materially reduce the next uncertainty.",
		"do not invoke plan(action=start) or plan(action=analyze) before the first targeted consult_peer",
		"Prefer targeted consult_peer calls over one broad omnibus consult, but do not repeat a fresh target/query unless new information creates a material gap.",
		"Re-evaluate Academic depth as the user's constraints evolve and your own understanding improves:",
		"invoke recall_recent before claiming the earlier discussion is unavailable",
		"recent_context_summary or recent_context_focus are present",
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
		"recent_context_summary or recent_context_focus are present",
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
		"`architect_forest_consult(purpose=get_plan_precedents, query=…)`",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("conversation system prompt missing %q", want)
		}
	}
}

func TestArchitectPlannerStagePromptsIncludeFabricContinuity(t *testing.T) {
	for _, stage := range []string{"requirements", "design", "tasks"} {
		text := ArchitectPlannerPromptForStage(stage)
		for _, want := range []string{
			"# Activity Fabric Awareness",
			"`recall_forward(topic",
			"`carry_forward(topic",
			"Fresh carried-forward testaments/artifacts outrank duplicate live consults",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s stage prompt missing %q", stage, want)
			}
		}
	}
}

func TestToolsForConversationMode_ConverseIncludesPlanningProtocolTools(t *testing.T) {
	tools := toolsForConversationMode(plannerConversationModeConverse)
	for _, want := range []string{
		// Phase 1/2.K refactor: consult → consult_peer,
		// ask_user_question → ask_user_clarification,
		// route_requirements_research → academic_research,
		// start_planning → plan(action=start).
		"recall_forward",
		"carry_forward",
		"consult_peer",
		"forest",
		"ask_user_clarification",
		"academic_research",
		"plan",
	} {
		if !containsToolName(tools, want) {
			t.Fatalf("converse tools missing %q: %v", want, tools)
		}
	}
	// plan_acceptance is intentionally excluded — the dialog publish
	// in feedbackReadyDirective is now the canonical acceptance path.
	// Exposing the tool would let the LLM auto-dispatch ahead of the
	// user's clicked verdict.
	for _, blocked := range []string{
		"plan_acceptance",
		// Phase 2.K / CR-4 refactor: plan_workflow folded into plan.
		"plan_workflow",
		"start_planning",
		// Phase 2.4 refactor: pre_delegation_declare +
		// validate_pre_delegation folded into delegation.
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

func TestToolsForConversationModeWithContext_AddsValidateWorkForArchitectGlobalReviewChallenge(t *testing.T) {
	ctx := shared.WithGlobalReviewState(context.Background(), shared.NewGlobalReviewState(&shared.GlobalReviewSnapshot{
		ReviewID: "review-architect",
		PendingChallenge: &shared.GlobalReviewChallenge{
			ID:              "review-architect-challenge",
			RequestingAgent: shared.GlobalReviewAgentInspector,
			TargetAgent:     shared.GlobalReviewAgentArchitect,
			Request:         "Reconcile the design contract.",
		},
	}, nil))

	tools := toolsForConversationModeWithContext(ctx, plannerConversationModeConverse)
	if !containsToolName(tools, "validate_work") {
		t.Fatalf("challenge tools missing validate_work: %v", tools)
	}
}

func TestToolsForConversationModeWithContext_DoesNotExposeValidateWorkWithoutArchitectChallenge(t *testing.T) {
	tools := toolsForConversationModeWithContext(context.Background(), plannerConversationModeConverse)
	if containsToolName(tools, "validate_work") {
		t.Fatalf("unexpected validate_work in ordinary converse tools: %v", tools)
	}
}

func TestPlannerConversationModeConverse_ToolSurfaceMatchesProtocolInstructions(t *testing.T) {
	// Phase 1/2.K refactor: consult → consult_peer,
	// route_plan_acceptance → plan_acceptance(action=route),
	// start_planning → plan(action=start).
	text := compactPromptWhitespace(plannerConversationModeInstructions(plannerConversationModeConverse))
	for _, want := range []string{
		"first review attached planning evidence and make any required consult_peer call for concrete missing/stale/contradicted gaps that usable recall did not answer, then invoke plan(action=analyze), then plan(action=design), then plan(action=generate_tasks)",
		"emits the user-reviewable plan_markdown artifact",
		"Do NOT invoke plan_acceptance — wait for the user's next message.",
		"Frame it as plan review, not execution kickoff.",
		"Avoid phrases like \"kick it off\", \"start building\", \"start implementing\", \"get started\", or \"ship it\".",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("converse instructions missing %q", want)
		}
	}
	tools := toolsForConversationMode(plannerConversationModeConverse)
	for _, wantTool := range []string{"plan", "recall_forward", "carry_forward", "consult_peer", "forest"} {
		if !containsToolName(tools, wantTool) {
			t.Fatalf("converse tools missing protocol-required tool %q: %v", wantTool, tools)
		}
	}
	// plan_acceptance is intentionally absent — see
	// TestToolsForConversationMode_ConverseIncludesPlanningProtocolTools.
	if containsToolName(tools, "plan_acceptance") {
		t.Fatalf("converse tools must not expose plan_acceptance: %v", tools)
	}
}

func TestGenerateTasksNextAction_UsesPlanReviewNotExecutionKickoff(t *testing.T) {
	text := compactPromptWhitespace(generateTasksNextAction(false))
	for _, want := range []string{
		"persisted the user-reviewable plan_markdown artifact before this final text.",
		"Frame it as plan review, not execution kickoff.",
		"Avoid phrases like \"kick it off\", \"start building\", \"start implementing\", \"get started\", or \"ship it\".",
		"Do NOT invoke plan_acceptance — wait for the user's response.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generateTasksNextAction(false) missing %q", want)
		}
	}
}

func TestPlanningProtocolInstructions_GateEvidenceBeforeAnalyze(t *testing.T) {
	text := startPlanningProtocolInstructions(false)
	evidenceIdx := strings.Index(text, "1. Evidence review")
	analyzeIdx := strings.Index(text, "2. plan(action=analyze")
	if evidenceIdx < 0 || analyzeIdx < 0 {
		t.Fatalf("protocol instructions missing evidence/analyze ordering:\n%s", text)
	}
	if evidenceIdx > analyzeIdx {
		t.Fatalf("protocol instructions analyze before evidence:\n%s", text)
	}
	if !strings.Contains(text, "If plan(action=analyze) or plan(action=design) returns requires_consultation=true") {
		t.Fatalf("protocol instructions missing analyze/design consultation gate:\n%s", text)
	}
}

func TestPlanningProtocolPromptStartsWithEvidenceReview(t *testing.T) {
	runner := &planningProtocolRunner{
		architect: &Architect{},
		request:   &ArchitectRequest{Query: "Create a toy Python CLI."},
		plan:      &DesignPlan{ID: "plan-1", SessionID: "session-1"},
	}
	text := compactPromptWhitespace(runner.buildProtocolPrompt())
	if strings.Contains(text, "Begin by invoking plan with action=analyze") {
		t.Fatalf("protocol prompt still starts at analyze:\n%s", text)
	}
	for _, want := range []string{
		"Begin with evidence review.",
		"Call recall_forward",
		"issue one targeted consult_peer and wait for it to complete",
		"Only after fresh evidence is attached should you invoke plan(action=analyze).",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("protocol prompt missing %q:\n%s", want, text)
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
