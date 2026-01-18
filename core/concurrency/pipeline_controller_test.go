package concurrency

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPipelineController_NewPipelineController(t *testing.T) {
	cfg := PipelineControllerConfig{}
	pc := NewPipelineController("pipeline-1", cfg)

	if pc.PipelineID() != "pipeline-1" {
		t.Errorf("expected pipeline-1, got %s", pc.PipelineID())
	}
	if pc.State() != ControllerStateRunning {
		t.Errorf("expected Running, got %v", pc.State())
	}
}

func TestPipelineController_RegisterUnregister(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{})

	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	s := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)

	pc.RegisterSupervisor("agent-1", s)
	if pc.SupervisorCount() != 1 {
		t.Errorf("expected 1 supervisor, got %d", pc.SupervisorCount())
	}

	pc.UnregisterSupervisor("agent-1")
	if pc.SupervisorCount() != 0 {
		t.Errorf("expected 0 supervisors, got %d", pc.SupervisorCount())
	}
}

func TestPipelineController_Stop_NoOperations(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{
		StopGracePeriod: 100 * time.Millisecond,
	})

	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	s := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	pc.RegisterSupervisor("agent-1", s)

	ctx := context.Background()
	err := pc.Stop(ctx)

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if pc.State() != ControllerStateStopped {
		t.Errorf("expected Stopped, got %v", pc.State())
	}
}

func TestPipelineController_Stop_WaitsForCompletion(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{
		StopGracePeriod: 500 * time.Millisecond,
	})

	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	s := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	pc.RegisterSupervisor("agent-1", s)

	op, err := s.BeginOperation(OpTypeLLMCall, "test-op", time.Second)
	if err != nil {
		t.Fatalf("failed to begin operation: %v", err)
	}

	done := make(chan error)
	go func() {
		done <- pc.Stop(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	s.EndOperation(op, nil, nil)

	err = <-done
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if pc.State() != ControllerStateStopped {
		t.Errorf("expected Stopped, got %v", pc.State())
	}
}

func TestPipelineController_Pause_Resume(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{
		PauseTimeout: 100 * time.Millisecond,
	})

	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	s := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	pc.RegisterSupervisor("agent-1", s)

	ctx := context.Background()
	err := pc.Pause(ctx)

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if pc.State() != ControllerStatePaused {
		t.Errorf("expected Paused, got %v", pc.State())
	}

	err = pc.Resume(ctx)
	if err != nil {
		t.Errorf("expected nil error on resume, got %v", err)
	}
	if pc.State() != ControllerStateRunning {
		t.Errorf("expected Running after resume, got %v", pc.State())
	}
}

func TestPipelineController_Kill_NoOperations(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{
		KillGracePeriod:  50 * time.Millisecond,
		KillHardDeadline: 100 * time.Millisecond,
	})

	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	s := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	pc.RegisterSupervisor("agent-1", s)

	ctx := context.Background()
	err := pc.Kill(ctx)

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if pc.State() != ControllerStateKilled {
		t.Errorf("expected Killed, got %v", pc.State())
	}
}

func TestPipelineController_Kill_CancelsOperations(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{
		KillGracePeriod:  100 * time.Millisecond,
		KillHardDeadline: 200 * time.Millisecond,
	})

	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	s := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	pc.RegisterSupervisor("agent-1", s)

	op, _ := s.BeginOperation(OpTypeLLMCall, "test-op", 5*time.Second)

	cancelled := make(chan struct{})
	go func() {
		<-op.Context().Done()
		close(cancelled)
	}()

	go func() {
		time.Sleep(50 * time.Millisecond)
		s.EndOperation(op, nil, nil)
	}()

	err := pc.Kill(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	select {
	case <-cancelled:
	case <-time.After(200 * time.Millisecond):
		t.Error("expected operation to be cancelled")
	}
}

func TestPipelineController_Kill_ReturnsOrphans(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{
		KillGracePeriod:  10 * time.Millisecond,
		KillHardDeadline: 20 * time.Millisecond,
	})

	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")

	s := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	pc.RegisterSupervisor("agent-1", s)

	_, _ = s.BeginOperation(OpTypeLLMCall, "stuck-op", 10*time.Second)

	err := pc.Kill(context.Background())

	if err == nil {
		t.Error("expected orphan error")
		return
	}

	orphanErr, ok := err.(*PipelineKillOrphansError)
	if !ok {
		t.Errorf("expected PipelineKillOrphansError, got %T", err)
		return
	}
	if len(orphanErr.Orphans) != 1 {
		t.Errorf("expected 1 orphan, got %d", len(orphanErr.Orphans))
	}
	if pc.State() != ControllerStateKilled {
		t.Errorf("expected Killed, got %v", pc.State())
	}
}

func TestPipelineController_DefaultConfig(t *testing.T) {
	cfg := DefaultPipelineControllerConfig()

	if cfg.StopGracePeriod != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.StopGracePeriod)
	}
	if cfg.PauseTimeout != 5*time.Second {
		t.Errorf("expected 5s, got %v", cfg.PauseTimeout)
	}
	if cfg.KillGracePeriod != 2*time.Second {
		t.Errorf("expected 2s, got %v", cfg.KillGracePeriod)
	}
	if cfg.KillHardDeadline != 5*time.Second {
		t.Errorf("expected 5s, got %v", cfg.KillHardDeadline)
	}
}

func TestPipelineController_MultipleSupervisors(t *testing.T) {
	pc := NewPipelineController("pipeline-1", PipelineControllerConfig{
		StopGracePeriod: 200 * time.Millisecond,
	})

	budget := NewGoroutineBudget(&atomic.Int32{})
	budget.RegisterAgent("agent-1", "engineer")
	budget.RegisterAgent("agent-2", "architect")

	s1 := NewAgentSupervisor(
		context.Background(),
		"agent-1", "engineer", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)
	s2 := NewAgentSupervisor(
		context.Background(),
		"agent-2", "architect", "pipeline-1",
		budget,
		AgentSupervisorConfig{},
	)

	pc.RegisterSupervisor("agent-1", s1)
	pc.RegisterSupervisor("agent-2", s2)

	if pc.SupervisorCount() != 2 {
		t.Errorf("expected 2 supervisors, got %d", pc.SupervisorCount())
	}

	err := pc.Stop(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	if s1.State() != SupervisorStateStopped {
		t.Errorf("expected s1 Stopped, got %v", s1.State())
	}
	if s2.State() != SupervisorStateStopped {
		t.Errorf("expected s2 Stopped, got %v", s2.State())
	}
}

func TestControllerState_String(t *testing.T) {
	tests := []struct {
		state    ControllerState
		expected string
	}{
		{ControllerStateRunning, "Running"},
		{ControllerStateStopping, "Stopping"},
		{ControllerStateStopped, "Stopped"},
		{ControllerStatePausing, "Pausing"},
		{ControllerStatePaused, "Paused"},
		{ControllerStateKilling, "Killing"},
		{ControllerStateKilled, "Killed"},
		{ControllerState(100), "Unknown"},
	}

	for _, tt := range tests {
		if tt.state.String() != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, tt.state.String())
		}
	}
}

func TestPipelineKillOrphansError_Error(t *testing.T) {
	err := &PipelineKillOrphansError{
		PipelineID: "pipeline-1",
		Orphans: []OrphanedOperation{
			{ID: "op-1"},
			{ID: "op-2"},
		},
	}

	expected := "pipeline pipeline-1 killed with 2 orphaned operations"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}
