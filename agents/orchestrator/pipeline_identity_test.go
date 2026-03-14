package orchestrator

import "testing"

func TestCanonicalPipelineTaskIdentity_PrefersDispatchTaskID(t *testing.T) {
	taskID, taskSlug := canonicalPipelineTaskIdentity(
		"task_plan_123",
		"",
		map[string]any{
			"task_id":   "task_1",
			"task_slug": "hello-cli",
		},
		"task_1:inspect",
	)

	if taskID != "task_plan_123" {
		t.Fatalf("taskID = %q, want task_plan_123", taskID)
	}
	if taskSlug != "hello-cli" {
		t.Fatalf("taskSlug = %q, want hello-cli", taskSlug)
	}
}

func TestCanonicalPipelineTaskIdentity_FallsBackThroughContextAndNodeID(t *testing.T) {
	taskID, taskSlug := canonicalPipelineTaskIdentity(
		"",
		"",
		map[string]any{
			"task_id":   "task_1",
			"task_slug": "hello-cli",
		},
		"task_1:inspect",
	)

	if taskID != "task_1" {
		t.Fatalf("taskID = %q, want task_1", taskID)
	}
	if taskSlug != "hello-cli" {
		t.Fatalf("taskSlug = %q, want hello-cli", taskSlug)
	}

	taskID, taskSlug = canonicalPipelineTaskIdentity("", "", nil, "task_1:inspect")
	if taskID != "task_1:inspect" {
		t.Fatalf("fallback taskID = %q, want task_1:inspect", taskID)
	}
	if taskSlug != "" {
		t.Fatalf("fallback taskSlug = %q, want empty", taskSlug)
	}
}

func TestPipelineWorkerAgentID_IsStableShortID(t *testing.T) {
	first := PipelineWorkerAgentID("task-7", "engineer")
	second := PipelineWorkerAgentID("task-7", "engineer")
	if first == "" {
		t.Fatal("expected non-empty pipeline worker agent ID")
	}
	if len(first) != 8 {
		t.Fatalf("len(PipelineWorkerAgentID()) = %d, want 8", len(first))
	}
	if first != second {
		t.Fatalf("PipelineWorkerAgentID() = %q then %q, want stable value", first, second)
	}
	if other := PipelineWorkerAgentID("task-7", "designer"); other == first {
		t.Fatalf("different worker roles should not share IDs: %q", first)
	}
}
