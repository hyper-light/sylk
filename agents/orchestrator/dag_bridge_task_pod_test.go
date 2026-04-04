package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	containerpod "github.com/adalundhe/sylk/core/container/pod"
)

type taskPodTestAgent struct {
	id        string
	agentType string
}

func (a *taskPodTestAgent) AgentID() string                 { return a.id }
func (a *taskPodTestAgent) AgentType() string               { return a.agentType }
func (a *taskPodTestAgent) Terminate(context.Context) error { return nil }

func newTaskPodTestRuntime(quota *container.ResourceQuota) *container.DefaultRuntime {
	budget := concurrency.NewGoroutineBudget(new(atomic.Int32))
	reg := container.NewContainerRegistry()
	return container.NewDefaultRuntime(container.DefaultRuntimeConfig{
		Budget:   budget,
		Registry: reg,
		Quota:    quota,
		CreateAgent: func(_ context.Context, agentType string) (container.ContainerAgent, error) {
			return &taskPodTestAgent{id: agentType + "-1", agentType: agentType}, nil
		},
	})
}

func newTaskPodForTest(taskID string, runtime container.ContainerRuntime, idleToWarm, idleToCool, idleToCold time.Duration) *shared.AgentPod {
	members := append([]string(nil), PipelineAgentTypes[:]...)
	specs := make([]container.ContainerSpec, 0, len(members))
	for _, member := range members {
		specs = append(specs, container.ContainerSpec{
			Name:      taskID + "-" + member,
			AgentType: member,
			Resources: container.ResourceSpec{GoroutineLimit: 2},
		})
	}
	return shared.NewAgentPod(shared.AgentPodConfig{
		PodID:   taskID,
		Runtime: runtime,
		Policy: &containerpod.PodPolicy{
			PodID:       taskID,
			PodType:     containerpod.PodTypePipeline,
			MemberTypes: members,
			MinTier:     containerpod.TierCold,
			IdleToWarm:  idleToWarm,
			IdleToCool:  idleToCool,
			IdleToCold:  idleToCold,
		},
		Specs: specs,
	})
}

func TestDAGBridge_MaintainTaskPodsRelievesIdlePipelineContainerPressure(t *testing.T) {
	quota := container.NewResourceQuota(container.ResourceQuotaConfig{
		GoroutineLimit: 200,
		ContainerLimit: 4,
	})
	runtime := newTaskPodTestRuntime(quota)

	task1 := newTaskPodForTest("task-1", runtime, 5*time.Millisecond, 5*time.Millisecond, 25*time.Millisecond)
	task2 := newTaskPodForTest("task-2", runtime, 5*time.Millisecond, 5*time.Millisecond, 25*time.Millisecond)

	if err := task1.HoldForNode(context.Background(), "node-1", []string{"engineer"}); err != nil {
		t.Fatalf("task1.HoldForNode: %v", err)
	}
	task1.ReleaseForNode("node-1")
	time.Sleep(15 * time.Millisecond)

	if err := task2.HoldForNode(context.Background(), "node-2", []string{"engineer"}); err != nil {
		t.Fatalf("task2.HoldForNode: %v", err)
	}

	bridge := &DAGBridge{
		quota: quota,
		activeDAGs: map[string]*ActiveDAGMeta{
			"dag-1": {
				TaskPods: map[string]*shared.AgentPod{
					"task-1": task1,
					"task-2": task2,
				},
			},
		},
	}

	if usage := quota.Usage(); usage.ContainerCount != 8 {
		t.Fatalf("expected 8 containers before maintenance, got %+v", usage)
	}

	bridge.maintainTaskPods(context.Background())

	usage := quota.Usage()
	if usage.ContainerCount != 4 {
		t.Fatalf("expected maintenance to reclaim idle task pod containers, got %+v", usage)
	}
	if got := task1.LoadTier(); got != containerpod.TierCool {
		t.Fatalf("task1 tier = %v, want %v", got, containerpod.TierCool)
	}
	if got := task2.LoadTier(); got != containerpod.TierHot {
		t.Fatalf("task2 tier = %v, want %v", got, containerpod.TierHot)
	}

	task1.Release()
	task2.Release()
}

func TestDAGBridge_ReleaseCompletedTaskResources_WaitsForIdleThenReleases(t *testing.T) {
	quota := container.NewResourceQuota(container.ResourceQuotaConfig{
		GoroutineLimit: 200,
		ContainerLimit: 4,
	})
	runtime := newTaskPodTestRuntime(quota)

	taskPod := newTaskPodForTest("task-1", runtime, time.Minute, time.Minute, time.Minute)
	if err := taskPod.HoldForNode(context.Background(), "node-1", []string{"inspector-pipeline"}); err != nil {
		t.Fatalf("taskPod.HoldForNode: %v", err)
	}

	scope := testScope()
	buffers, err := NewBufferRegistry(DefaultBufferRegistryConfig(4), nil, scope)
	if err != nil {
		t.Fatalf("NewBufferRegistry: %v", err)
	}
	if err := buffers.Push(TaskUpdateEntry{
		ID:        "upd-1",
		TaskID:    "task-1",
		Status:    "running",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("buffers.Push: %v", err)
	}

	bridge := &DAGBridge{
		buffers: buffers,
		activeDAGs: map[string]*ActiveDAGMeta{
			"dag-1": {
				TaskPods: map[string]*shared.AgentPod{
					"task-1": taskPod,
				},
				TaskLabels:             map[string]string{"task-1": "auth-checkout"},
				ResetPendingTaskPods:   map[string]struct{}{"task-1": {}},
				ReleasePendingTaskPods: make(map[string]struct{}),
			},
		},
	}

	bridge.ReleaseCompletedTaskResources("dag-1", "task-1")

	meta := bridge.activeDAGs["dag-1"]
	if _, pending := meta.ReleasePendingTaskPods["task-1"]; !pending {
		t.Fatal("expected task pod release to defer while the task is still active")
	}
	if _, present := meta.TaskPods["task-1"]; !present {
		t.Fatal("task pod should remain tracked until the active request settles")
	}

	taskPod.ReleaseForNode("node-1")
	bridge.releasePendingCompletedTaskPods("dag-1")

	if !taskPod.IsReleased() {
		t.Fatal("expected task pod to be released after the task became idle")
	}
	if usage := quota.Usage(); usage.ContainerCount != 0 {
		t.Fatalf("expected accepted task cleanup to deallocate containers, got %+v", usage)
	}
	if _, present := meta.TaskPods["task-1"]; present {
		t.Fatal("expected released task pod to be removed from active DAG state")
	}
	if _, pending := meta.ReleasePendingTaskPods["task-1"]; pending {
		t.Fatal("expected pending release marker to clear after cleanup")
	}
	if _, pending := meta.ResetPendingTaskPods["task-1"]; pending {
		t.Fatal("expected reset-pending marker to clear after cleanup")
	}
	if got := buffers.ActiveBufferCount(); got != 0 {
		t.Fatalf("expected task buffer to be flushed on release, got %d active buffers", got)
	}
}
