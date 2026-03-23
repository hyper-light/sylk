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
		"Your first `challenge_agent` call to Engineer, Designer, or Inspector is allowed.",
		"Re-challenge Inspector only after Inspector answered your prior challenge and you then changed pipeline VFS state yourself based on that answer.",
		"`handoff_next` is the only transport step.",
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
		"## Core Global Testing Principles",
		"## SAFETY CONSTRAINTS AND RULES",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("global task prompt missing %q", want)
		}
	}
}
