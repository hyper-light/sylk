package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegration_StopLifecycle(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	controller := NewPipelineController("pipeline-1", PipelineControllerConfig{
		StopGracePeriod: 500 * time.Millisecond,
	})

	supervisor := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	controller.RegisterSupervisor("agent-1", supervisor)

	op, err := supervisor.BeginOperation(OpTypeLLMCall, "test-op", 5*time.Second)
	if err != nil {
		t.Fatalf("failed to begin operation: %v", err)
	}

	done := make(chan error)
	go func() {
		done <- controller.Stop(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	supervisor.EndOperation(op, "result", nil)

	err = <-done
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	if controller.State() != ControllerStateStopped {
		t.Errorf("expected Stopped, got %v", controller.State())
	}
	if supervisor.OperationCount() != 0 {
		t.Errorf("expected 0 operations, got %d", supervisor.OperationCount())
	}
}

func TestIntegration_PauseResumeLifecycle(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	controller := NewPipelineController("pipeline-1", PipelineControllerConfig{
		PauseTimeout: 200 * time.Millisecond,
	})

	supervisor := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	controller.RegisterSupervisor("agent-1", supervisor)

	err := controller.Pause(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if controller.State() != ControllerStatePaused {
		t.Errorf("expected Paused, got %v", controller.State())
	}

	err = controller.Resume(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if controller.State() != ControllerStateRunning {
		t.Errorf("expected Running, got %v", controller.State())
	}

	_, err = supervisor.BeginOperation(OpTypeLLMCall, "post-resume-op", time.Second)
	if err != nil {
		t.Errorf("failed to begin operation after resume: %v", err)
	}
}

func TestIntegration_KillLifecycle(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	controller := NewPipelineController("pipeline-1", PipelineControllerConfig{
		KillGracePeriod:  50 * time.Millisecond,
		KillHardDeadline: 100 * time.Millisecond,
	})

	supervisor := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	controller.RegisterSupervisor("agent-1", supervisor)

	op, _ := supervisor.BeginOperation(OpTypeLLMCall, "test-op", 5*time.Second)

	cancelled := make(chan struct{})
	go func() {
		<-op.Context().Done()
		close(cancelled)
	}()

	go func() {
		time.Sleep(30 * time.Millisecond)
		supervisor.EndOperation(op, nil, nil)
	}()

	err := controller.Kill(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	select {
	case <-cancelled:
	case <-time.After(150 * time.Millisecond):
		t.Error("expected operation to be cancelled")
	}

	if controller.State() != ControllerStateKilled {
		t.Errorf("expected Killed, got %v", controller.State())
	}
}

func TestIntegration_GoroutineBudgetPressure(t *testing.T) {
	pressureLevel := &atomic.Int32{}
	budget := NewGoroutineBudget(pressureLevel)
	budget.RegisterAgent("agent-1", "engineer")

	active, _, softLimit, hardLimit, ok := budget.GetStats("agent-1")
	if !ok {
		t.Fatal("agent not found")
	}
	if active != 0 {
		t.Errorf("expected 0 active, got %d", active)
	}

	initialSoft := softLimit
	initialHard := hardLimit

	budget.OnPressureChange(PressureElevated)
	_, _, softLimit, hardLimit, _ = budget.GetStats("agent-1")

	if softLimit >= initialSoft {
		t.Errorf("expected soft limit to decrease, got %d >= %d", softLimit, initialSoft)
	}
	if hardLimit >= initialHard {
		t.Errorf("expected hard limit to decrease, got %d >= %d", hardLimit, initialHard)
	}

	budget.OnPressureChange(PressureCritical)
	_, _, softLimit, _, _ = budget.GetStats("agent-1")

	if float64(softLimit) > float64(initialSoft)*0.30 {
		t.Errorf("expected critical limit ~25%%, got %d vs initial %d", softLimit, initialSoft)
	}
}

func TestIntegration_CascadeKill(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")
	budget.RegisterAgent("agent-2", "architect")
	budget.RegisterAgent("agent-3", "librarian")

	controller := NewPipelineController("pipeline-1", PipelineControllerConfig{
		KillGracePeriod:  50 * time.Millisecond,
		KillHardDeadline: 100 * time.Millisecond,
	})

	supervisors := make([]*AgentSupervisor, 3)
	for i := 0; i < 3; i++ {
		agentID := []string{"agent-1", "agent-2", "agent-3"}[i]
		agentType := []string{"engineer", "architect", "librarian"}[i]
		supervisors[i] = NewAgentSupervisor(
			context.Background(),
			agentID, agentType, "pipeline-1",
			budget,
			AgentSupervisorConfig{},
		)
		controller.RegisterSupervisor(agentID, supervisors[i])
	}

	var ops []*Operation
	for _, s := range supervisors {
		op, _ := s.BeginOperation(OpTypeLLMCall, "test-op", 5*time.Second)
		ops = append(ops, op)
	}

	go func() {
		time.Sleep(30 * time.Millisecond)
		for i, s := range supervisors {
			s.EndOperation(ops[i], nil, nil)
		}
	}()

	err := controller.Kill(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	for i, s := range supervisors {
		if s.OperationCount() != 0 {
			t.Errorf("supervisor %d: expected 0 operations, got %d", i, s.OperationCount())
		}
	}
}

func TestIntegration_StressKill(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	controller := NewPipelineController("pipeline-1", PipelineControllerConfig{
		KillGracePeriod:  100 * time.Millisecond,
		KillHardDeadline: 200 * time.Millisecond,
	})

	supervisor := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{MaxConcurrentOps: 100},
	)
	controller.RegisterSupervisor("agent-1", supervisor)

	const numOps = 50
	var wg sync.WaitGroup
	ops := make([]*Operation, numOps)

	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			op, err := supervisor.BeginOperation(OpTypeLLMCall, "stress-op", time.Second)
			if err == nil {
				ops[idx] = op
			}
		}(i)
	}
	wg.Wait()

	go func() {
		time.Sleep(50 * time.Millisecond)
		for _, op := range ops {
			if op != nil {
				supervisor.EndOperation(op, nil, nil)
			}
		}
	}()

	err := controller.Kill(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	if supervisor.OperationCount() != 0 {
		t.Errorf("expected 0 operations after kill, got %d", supervisor.OperationCount())
	}
}

func TestIntegration_GoroutineScope(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scope := NewGoroutineScope(ctx, "agent-1", budget)

	var completed atomic.Int32
	const numWorkers = 5

	for i := 0; i < numWorkers; i++ {
		err := scope.Go("test-worker", 100*time.Millisecond, func(ctx context.Context) error {
			time.Sleep(20 * time.Millisecond)
			completed.Add(1)
			return nil
		})
		if err != nil {
			t.Errorf("failed to spawn worker: %v", err)
		}
	}

	err := scope.Shutdown(50*time.Millisecond, 100*time.Millisecond)
	if err != nil {
		t.Errorf("shutdown error: %v", err)
	}

	if completed.Load() != numWorkers {
		t.Errorf("expected %d completed, got %d", numWorkers, completed.Load())
	}
}

func TestIntegration_ResourceTracking(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	supervisor := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)

	op, _ := supervisor.BeginOperation(OpTypeToolExecution, "test-op", time.Second)

	resource := &testTrackedResource{id: "res-1"}
	supervisor.TrackResource(op, resource)

	if supervisor.Resources().Count() != 1 {
		t.Errorf("expected 1 resource, got %d", supervisor.Resources().Count())
	}

	errs := supervisor.ForceCloseResources()
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}

	if !resource.closed.Load() {
		t.Error("expected resource to be force-closed")
	}
}

func TestIntegration_OperationTimeout(t *testing.T) {
	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	supervisor := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)

	op, _ := supervisor.BeginOperation(OpTypeLLMCall, "test-op", 50*time.Millisecond)

	select {
	case <-op.Context().Done():
	case <-time.After(100 * time.Millisecond):
		t.Error("expected operation to timeout")
	}

	supervisor.EndOperation(op, nil, op.Context().Err())
}
