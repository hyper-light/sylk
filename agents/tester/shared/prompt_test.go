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
		"If Inspector's criteria are ambiguous or untestable on a normal top-level turn, use `challenge_agent` to ask for clarification instead of guessing.",
		"Your first `challenge_agent` call to Engineer, Designer, or Inspector is allowed.",
		"Re-challenge Inspector only after Inspector answered your prior challenge and you then changed pipeline VFS state yourself based on that answer.",
		"`handoff_next` is the normal top-level transport step.",
		"`validate_work` is the challenge-response transport step.",
		"`tester_forest_get_test_targets`",
		"`tester_forest_get_failure_clusters`",
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
		"`tester_forest_get_test_targets`",
		"`tester_forest_get_failure_clusters`",
		"## Core Global Testing Principles",
		"## SAFETY CONSTRAINTS AND RULES",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("global task prompt missing %q", want)
		}
	}
}

func TestGlobalTesterSystemPrompt_IncludesGlobalReviewProtocolGuidance(t *testing.T) {
	prompt := GlobalTesterSystemPrompt()
	for _, want := range []string{
		"## GLOBAL REVIEW PROTOCOL",
		"Treat normal top-level global testing turns as ordinary handoffs.",
		"Treat active challenge turns as narrower follow-up work. Answer those turns with `validate_work`",
		"`tester_forest_get_test_targets`",
		"### When Responding To The Global Inspector",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("global tester prompt missing %q", want)
		}
	}
}
