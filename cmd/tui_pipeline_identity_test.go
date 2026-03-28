package cmd

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/orchestrator"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/handoff"
)

func TestPipelineWorkerAgentID_UsesSpecLabelWhenAvailable(t *testing.T) {
	ctx := container.WithCreationContext(context.Background(), container.ContainerSpec{
		Labels: map[string]string{
			"pipeline_worker_id": "abcd1234",
		},
	}, container.PodID("task-7"))

	if got := pipelineWorkerAgentID(ctx, "engineer"); got != "abcd1234" {
		t.Fatalf("pipelineWorkerAgentID() = %q, want %q", got, "abcd1234")
	}
}

func TestPipelineWorkerAgentID_FallsBackToStablePipelineWorkerID(t *testing.T) {
	ctx := container.WithCreationContext(context.Background(), container.ContainerSpec{}, container.PodID("task-7"))

	got := pipelineWorkerAgentID(ctx, "engineer")
	want := orchestrator.PipelineWorkerAgentID("task-7", "engineer")
	if got != want {
		t.Fatalf("pipelineWorkerAgentID() = %q, want %q", got, want)
	}
}

func TestRequirePipelineWorkerAgentID_RequiresTaskScopedContext(t *testing.T) {
	if _, err := requirePipelineWorkerAgentID(context.Background(), "tester-pipeline"); err == nil {
		t.Fatal("expected requirePipelineWorkerAgentID() to reject missing task-scoped context")
	}
}

func TestRequirePipelineWorkerAgentID_UsesTaskScopedIdentity(t *testing.T) {
	ctx := container.WithCreationContext(context.Background(), container.ContainerSpec{}, container.PodID("task-7"))

	got, err := requirePipelineWorkerAgentID(ctx, "tester-pipeline")
	if err != nil {
		t.Fatalf("requirePipelineWorkerAgentID() error = %v", err)
	}
	want := orchestrator.PipelineWorkerAgentID("task-7", "tester-pipeline")
	if got != want {
		t.Fatalf("requirePipelineWorkerAgentID() = %q, want %q", got, want)
	}
}

func TestApplyHandoffCreationContext_MapsFactoryMetadataToContainerContext(t *testing.T) {
	ctx := handoff.WithFactoryCreationMetadata(context.Background(), handoff.FactoryCreationMetadata{
		AgentType: "tester-pipeline",
		AgentID:   "4d6b407a",
		TaskID:    "task-auth-checkout",
		TaskSlug:  "auth-checkout",
	})

	ctx = applyHandoffCreationContext(ctx, "tester-pipeline")

	spec, ok := container.CreationSpecFromContext(ctx)
	if !ok {
		t.Fatal("expected container creation spec in context")
	}
	podID, ok := container.CreationPodIDFromContext(ctx)
	if !ok {
		t.Fatal("expected container pod id in context")
	}
	if spec.AgentType != "tester-pipeline" {
		t.Fatalf("spec.AgentType = %q, want tester-pipeline", spec.AgentType)
	}
	if spec.Labels["pipeline_worker_id"] != "4d6b407a" {
		t.Fatalf("pipeline_worker_id = %q, want 4d6b407a", spec.Labels["pipeline_worker_id"])
	}
	if spec.Labels["task_id"] != "task-auth-checkout" {
		t.Fatalf("task_id = %q, want task-auth-checkout", spec.Labels["task_id"])
	}
	if spec.Labels["task_slug"] != "auth-checkout" {
		t.Fatalf("task_slug = %q, want auth-checkout", spec.Labels["task_slug"])
	}
	if podID != container.PodID("task-auth-checkout") {
		t.Fatalf("podID = %q, want task-auth-checkout", podID)
	}
}

func TestTaskScopedWorkerRoutingInfo_StripsAmbiguousGlobalAliases(t *testing.T) {
	info := taskScopedWorkerRoutingInfo(&guide.AgentRoutingInfo{
		ID:      "worker-1234",
		Type:    "inspector-pipeline",
		Name:    "inspector-pipeline",
		Aliases: []string{"pipeline-inspector", "task-validator"},
		ActionShortcuts: []guide.ActionShortcut{{
			Name: "validate-task",
		}},
		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{"validate task"},
		},
		Registration: &guide.AgentRegistration{
			ID:      "worker-1234",
			Name:    "inspector-pipeline",
			Aliases: []string{"pipeline-inspector", "task-validator"},
		},
	}, "task-7", "inspector-pipeline")
	if info == nil {
		t.Fatal("taskScopedWorkerRoutingInfo() returned nil")
	}
	if info.Name != "inspector-pipeline" {
		t.Fatalf("Name = %q, want %q", info.Name, "inspector-pipeline")
	}
	if info.PodID != "task-7" {
		t.Fatalf("PodID = %q, want task-7", info.PodID)
	}
	if len(info.Aliases) != 1 || info.Aliases[0] != "task-7-inspector-pipeline" {
		t.Fatalf("Aliases = %v, want [task-7-inspector-pipeline]", info.Aliases)
	}
	if len(info.ActionShortcuts) != 0 {
		t.Fatalf("ActionShortcuts = %v, want empty", info.ActionShortcuts)
	}
	if len(info.Triggers.StrongTriggers) != 0 {
		t.Fatalf("Triggers = %+v, want empty", info.Triggers)
	}
	if info.Registration == nil {
		t.Fatal("Registration = nil")
	}
	if info.Registration.Name != "inspector-pipeline" {
		t.Fatalf("Registration.Name = %q, want %q", info.Registration.Name, "inspector-pipeline")
	}
	if len(info.Registration.Aliases) != 1 || info.Registration.Aliases[0] != "task-7-inspector-pipeline" {
		t.Fatalf("Registration.Aliases = %v, want [task-7-inspector-pipeline]", info.Registration.Aliases)
	}
}
