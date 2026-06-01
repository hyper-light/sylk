package engineer

import (
	"strings"
	"testing"

	shared "github.com/adalundhe/sylk/agents/shared"
)

func TestDefaultEngineerSystemPrompt_ComposesModulesOnceInOrder(t *testing.T) {
	modules := []string{
		"# Engineer Agent — System",
		"# Engineer Agent — Implementation Guidance",
		"# Engineer Agent — Consultation Policy",
		"# Engineer Agent — Skill Usage Policy",
		"# Engineer Agent — Guardrails",
		"# Engineer Agent — Self-Audit Protocol",
		"# Engineer Agent — Collaboration Protocol",
	}

	lastIdx := -1
	for _, marker := range modules {
		count := strings.Count(DefaultEngineerSystemPrompt, marker)
		if count != 1 {
			t.Fatalf("marker %q appears %d times, want 1", marker, count)
		}
		idx := strings.Index(DefaultEngineerSystemPrompt, marker)
		if idx <= lastIdx {
			t.Fatalf("marker %q appears out of order", marker)
		}
		lastIdx = idx
	}
}

// TestDefaultEngineerSystemPrompt_IncludesLanguageToolingPreference
// locks the wiring of the shared language-tooling preference snippet.
// The snippet encodes the priority order — task spec → fabric →
// codebase — that engineer reads before reaching for write or edit
// skills. Pre-fix, agents drifted into the codebase's primary
// language even when the task explicitly asked for a different
// language; the snippet plus the harness's language_source signal is
// the corrective.
func TestDefaultEngineerSystemPrompt_IncludesLanguageToolingPreference(t *testing.T) {
	required := []string{
		"LANGUAGE, FRAMEWORK, AND TOOLING SELECTION",
		"task specification",
		"Activity Fabric",
		"existing codebase",
		"ask_user_clarification",
	}
	for _, want := range required {
		if !strings.Contains(DefaultEngineerSystemPrompt, want) {
			t.Errorf("DefaultEngineerSystemPrompt missing %q", want)
		}
	}
}

func TestEngineerSystemCorePrompt_DoesNotDuplicateModuleHeadings(t *testing.T) {
	disallowed := []string{
		"# Engineer Agent — Implementation Guidance",
		"# Engineer Agent — Consultation Policy",
		"# Engineer Agent — Skill Usage Policy",
		"# Engineer Agent — Guardrails",
		"# Engineer Agent — Self-Audit Protocol",
		"# Engineer Agent — Collaboration Protocol",
	}

	for _, marker := range disallowed {
		if strings.Contains(EngineerSystemCorePrompt, marker) {
			t.Fatalf("core prompt contains delegated module heading %q", marker)
		}
	}
}

func TestDefaultEngineerSystemPrompt_UsesCurrentSkillNames(t *testing.T) {
	required := []string{
		"`discover_project_tools`",
		"`discover_code_patterns`",
		// consult_peer is the single consultation entry point; the old
		// `consult` + `target:` surface was removed in the Phase 1/2.K
		// refactor and replaced with consult_peer + target_agent_type.
		"consult_peer",
		"target_agent_type",
		"`engineer_forest_consult(purpose=select_implementation_node, query=…)`",
		"`engineer_forest_consult(purpose=get_failure_precedents, query=…)`",
		"`audit`",
		"`bash`",
	}
	for _, marker := range required {
		if !strings.Contains(DefaultEngineerSystemPrompt, marker) {
			t.Fatalf("expected default engineer prompt to contain %q", marker)
		}
	}

	legacyAliases := []string{
		"consult_librarian",
		"consult_archivalist",
		"consult_academic",
		"audit_implementation",
		"run_tests",
		// The pre-refactor `consult` skill with a bare `target:` param
		// is no longer registered on the engineer's visible surface;
		// the prompt must not direct the LLM to call it.
		"consult` with `target",
		"`consult`",
	}
	for _, marker := range legacyAliases {
		if strings.Contains(DefaultEngineerSystemPrompt, marker) {
			t.Fatalf("default engineer prompt still contains legacy tool alias %q", marker)
		}
	}
}

func TestDefaultEngineerSystemPrompt_IncludesExactEditContract(t *testing.T) {
	// The per-scope write tools (write_pipeline_file / edit_pipeline_file)
	// were collapsed into workspace_write(op=write|edit, scope=pipeline),
	// and prepare_pipeline_write_context into workspace_read(op=prepare_write,
	// scope=pipeline). The edit contract now applies to workspace_write op=edit.
	required := []string{
		"`op=edit`",
		"`old_text`",
		"`new_text`",
		"`op=write`",
	}
	for _, want := range required {
		if !strings.Contains(DefaultEngineerSystemPrompt, want) {
			t.Fatalf("expected default engineer prompt to contain %q", want)
		}
	}
}

func TestEngineerSystemPromptForContract_OmitsStaticImplementationProtocol(t *testing.T) {
	prompt := EngineerSystemPromptForContract(&shared.TaskExecutionContract{TaskID: "task_1"})
	if strings.Contains(prompt, "# Engineer Agent — Implementation Guidance") {
		t.Fatal("task-scoped engineer prompt should not include the static implementation protocol")
	}
	for _, want := range []string{
		"# Engineer Agent — System",
		"# Engineer Agent — Consultation Policy",
		"# Engineer Agent — Skill Usage Policy",
		"# Engineer Agent — Guardrails",
		"# Engineer Agent — Self-Audit Protocol",
		"# Engineer Agent — Collaboration Protocol",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("task-scoped engineer prompt missing %q", want)
		}
	}
}

func TestEngineerSystemCollabPrompt_RoutesImplementationBackToInspector(t *testing.T) {
	required := []string{
		"`inspector-pipeline`",
		"`inspector -> tester -> engineer/designer -> inspector`",
		"Do not hand off directly to `tester-pipeline` after implementation",
		"Use `post_action(kind=task)` to route back to `inspector-pipeline` when you are handing off fresh top-level implementation evidence.",
		"Use `submit_testaments` whenever you are the subject of a delivered claim and have completion, refusal, blocker, or error evidence to return.",
		"Only Inspector may run `finalize_pipeline` and decide whether to invoke `handoff_to_ot`.",
		"Your first `post_action(kind=challenge)` call to Tester, Designer, or Inspector is allowed.",
		"Re-challenge Inspector only after Inspector answered your previous challenge and you then changed pipeline VFS state yourself based on that answer.",
		"Do not reinterpret a targeted challenge turn as permission to restart the broad top-level implementation flow.",
	}
	for _, want := range required {
		if !strings.Contains(EngineerSystemCollabPrompt, want) {
			t.Fatalf("collaboration prompt missing %q", want)
		}
	}

	disallowed := []string{
		"If tests pass: you're done",
		"the task is complete.",
	}
	for _, text := range disallowed {
		if strings.Contains(EngineerSystemCollabPrompt, text) {
			t.Fatalf("collaboration prompt still contains stale terminal guidance %q", text)
		}
	}
}
