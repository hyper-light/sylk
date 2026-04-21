package shared

import (
	"strings"
	"testing"

	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func TestPipelineTesterSystemPromptForWorkerAndContract_UsesTaskModePrompt(t *testing.T) {
	prompt := PipelineTesterSystemPromptForWorkerAndContract("engineer", &agentshared.TaskExecutionContract{TaskID: "task_1"})
	for _, blocked := range []string{
		"## 6-PHASE TESTING PROTOCOL",
		"## AVAILABLE SKILLS",
		"### Phase 1: Gate on Inspector",
	} {
		if strings.Contains(prompt, blocked) {
			t.Fatalf("task-scoped tester prompt unexpectedly contains %q", blocked)
		}
	}
	for _, want := range []string{
		"# THE PIPELINE TESTER",
		"Use them as the source of workflow truth.",
		"## Core Testing Principles",
		"If Inspector's criteria are ambiguous or untestable on a normal top-level turn, use `pipeline_protocol(action=challenge)` to ask for clarification instead of guessing.",
		"Your first `pipeline_protocol(action=challenge)` call to Engineer, Designer, or Inspector is allowed.",
		"Re-challenge Inspector only after Inspector answered your prior challenge and you then changed pipeline VFS state yourself based on that answer.",
		"`pipeline_protocol(action=handoff)` is the normal top-level transport step",
		"`pipeline_protocol(action=validate)`",
		"`tester_forest_consult(purpose=get_test_targets, query=…)`",
		"`tester_forest_consult(purpose=get_failure_clusters, query=…)`",
		"Test Categories:",
		"## SAFETY CONSTRAINTS AND RULES",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("task-scoped tester prompt missing %q", want)
		}
	}
}

func TestGlobalTesterSystemPromptForContract_UsesTaskModePrompt(t *testing.T) {
	prompt := GlobalTesterSystemPromptForContract(&agentshared.GlobalExecutionContract{Role: "tester-global", Mode: agentshared.GlobalExecutionModeExecute})
	for _, blocked := range []string{
		"## 7-PHASE TESTING PROTOCOL",
		"## AVAILABLE SKILLS",
		"### Phase 1: Assemble Batch Context",
	} {
		if strings.Contains(prompt, blocked) {
			t.Fatalf("global task prompt unexpectedly contains %q", blocked)
		}
	}
	for _, want := range []string{
		"# THE GLOBAL TESTER",
		"Use it as the source of workflow truth.",
		"`tester_forest_consult(purpose=get_test_targets, query=…)`",
		"`tester_forest_consult(purpose=get_failure_clusters, query=…)`",
		"## Core Global Testing Principles",
		"## SAFETY CONSTRAINTS AND RULES",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("global task prompt missing %q", want)
		}
	}
}

// TestTesterSystemPrompts_IncludeLanguageToolingPreference locks the
// wiring of the shared language-tooling preference snippet into both
// pipeline and global tester prompt assemblies. The snippet encodes
// the priority order — task spec → fabric → codebase — that tester
// reads before reaching for write_test, edit_pipeline_file, or
// write_global_file. Pre-fix, tester drifted into the codebase's
// primary language even when the task's path-shaped spec implied a
// different framework (e.g., wrote pytest in a Go project).
func TestTesterSystemPrompts_IncludeLanguageToolingPreference(t *testing.T) {
	required := []string{
		"LANGUAGE, FRAMEWORK, AND TOOLING SELECTION",
		"task specification",
		"Activity Fabric",
		"existing codebase",
		"ask_user_clarification",
	}
	for name, prompt := range map[string]string{
		"PipelineTesterSystemPrompt":               PipelineTesterSystemPrompt(),
		"GlobalTesterSystemPrompt":                 GlobalTesterSystemPrompt(),
		"PipelineTesterSystemPromptForContract":    PipelineTesterSystemPromptForWorkerAndContract("engineer", &agentshared.TaskExecutionContract{TaskID: "task_x"}),
		"GlobalTesterSystemPromptForContract":      GlobalTesterSystemPromptForContract(&agentshared.GlobalExecutionContract{Role: "tester-global", Mode: agentshared.GlobalExecutionModeExecute}),
	} {
		for _, want := range required {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s missing %q", name, want)
			}
		}
	}
}

func TestGlobalTesterSystemPrompt_IncludesGlobalReviewProtocolGuidance(t *testing.T) {
	prompt := GlobalTesterSystemPrompt()
	for _, want := range []string{
		"## GLOBAL REVIEW PROTOCOL",
		"Treat normal top-level global testing turns as ordinary handoffs.",
		"Treat active challenge turns as narrower follow-up work. Answer those turns with `pipeline_protocol(action=validate)`",
		"`tester_forest_consult(purpose=get_test_targets, query=…)`",
		"### When Responding To The Global Inspector",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("global tester prompt missing %q", want)
		}
	}
}
