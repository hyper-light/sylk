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

func TestPipelineWorkerRoutingTarget_UsesTaskScopedRoutingName(t *testing.T) {
	first := PipelineWorkerRoutingTarget("task-7", "engineer")
	second := PipelineWorkerRoutingTarget("task-7", "engineer")
	if first == "" {
		t.Fatal("expected non-empty pipeline worker routing target")
	}
	if first != "task-7-engineer" {
		t.Fatalf("PipelineWorkerRoutingTarget() = %q, want task-7-engineer", first)
	}
	if first != second {
		t.Fatalf("PipelineWorkerRoutingTarget() = %q then %q, want stable value", first, second)
	}
	if other := PipelineWorkerRoutingTarget("task-7", "designer"); other == first {
		t.Fatalf("different worker roles should not share routing targets: %q", first)
	}
}
