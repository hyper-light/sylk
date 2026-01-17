package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPipelineRunner(t *testing.T) {
	config := PipelineRunnerConfig{
		ID: "test-pipeline",
	}

	runner := NewPipelineRunner(config)

	if runner.ID() != "test-pipeline" {
		t.Errorf("got ID %q, want %q", runner.ID(), "test-pipeline")
	}

	if runner.config.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("got ShutdownTimeout %v, want %v", runner.config.ShutdownTimeout, DefaultShutdownTimeout)
	}
}

func TestPipelineRunner_RegisterAndExecutePhases(t *testing.T) {
	runner, order, mu := setupPhaseRunner()

	_ = runner.Start(context.Background())
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	verifyPhaseOrder(t, order, DefaultPipelinePhases)

	if runner.Lifecycle().State() != StateStopped {
		t.Errorf("got state %v, want %v", runner.Lifecycle().State(), StateStopped)
	}
}

func setupPhaseRunner() (*PipelineRunner, *[]PipelinePhase, *sync.Mutex) {
	var order []PipelinePhase
	var mu sync.Mutex
	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})

	for _, phase := range DefaultPipelinePhases {
		p := phase
		runner.RegisterPhase(p, func(ctx context.Context) error {
			mu.Lock()
			order = append(order, p)
			mu.Unlock()
			return nil
		})
	}
	return runner, &order, &mu
}

func verifyPhaseOrder(t *testing.T, order *[]PipelinePhase, expected []PipelinePhase) {
	t.Helper()
	if len(*order) != len(expected) {
		t.Fatalf("got %d phases executed, want %d", len(*order), len(expected))
	}
	for i, phase := range expected {
		if (*order)[i] != phase {
			t.Errorf("phase %d: got %v, want %v", i, (*order)[i], phase)
		}
	}
}

func TestPipelineRunner_Start(t *testing.T) {
	var executed atomic.Bool
	runner := createBlockingPipelineRunner(&executed)

	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer runner.Stop()

	time.Sleep(50 * time.Millisecond)

	if !runner.IsRunning() {
		t.Error("expected runner to be running")
	}
	if !executed.Load() {
		t.Error("phase function not executed")
	}
}

func TestPipelineRunner_StartAndStop(t *testing.T) {
	var executed atomic.Bool
	runner := createBlockingPipelineRunner(&executed)

	_ = runner.Start(context.Background())
	time.Sleep(50 * time.Millisecond)

	if err := runner.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
	if runner.IsRunning() {
		t.Error("expected runner to not be running")
	}
}

func createBlockingPipelineRunner(executed *atomic.Bool) *PipelineRunner {
	runner := NewPipelineRunner(PipelineRunnerConfig{
		ID:              "test",
		ShutdownTimeout: time.Second,
	})
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		executed.Store(true)
		<-ctx.Done()
		return nil
	})
	return runner
}

func TestPipelineRunner_StartAlreadyStarted(t *testing.T) {
	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	_ = runner.Start(context.Background())
	defer runner.Stop()

	err := runner.Start(context.Background())
	if err != ErrPipelineAlreadyStarted {
		t.Errorf("got error %v, want %v", err, ErrPipelineAlreadyStarted)
	}
}

func TestPipelineRunner_StopNotRunning(t *testing.T) {
	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})

	err := runner.Stop()
	if err != ErrPipelineNotRunning {
		t.Errorf("got error %v, want %v", err, ErrPipelineNotRunning)
	}
}

func TestPipelineRunner_PhaseError(t *testing.T) {
	expectedErr := errors.New("phase error")

	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})
	runner.RegisterPhase(PhaseInspector, func(ctx context.Context) error {
		return expectedErr
	})

	_ = runner.Start(context.Background())
	time.Sleep(50 * time.Millisecond)

	if runner.Error() == nil {
		t.Error("expected error to be captured")
	}

	if runner.Lifecycle().State() != StateFailed {
		t.Errorf("got state %v, want %v", runner.Lifecycle().State(), StateFailed)
	}
}

func TestPipelineRunner_PhasePanic(t *testing.T) {
	var panicHandled atomic.Bool

	runner := NewPipelineRunner(PipelineRunnerConfig{
		ID: "test",
		OnPanic: func(id string, recovered any) {
			panicHandled.Store(true)
		},
	})

	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		panic("test panic")
	})

	_ = runner.Start(context.Background())
	time.Sleep(50 * time.Millisecond)

	if !panicHandled.Load() {
		t.Error("panic handler not called")
	}

	if runner.Lifecycle().State() != StateFailed {
		t.Errorf("got state %v, want %v", runner.Lifecycle().State(), StateFailed)
	}
}

func TestPipelineRunner_ContextCancellation(t *testing.T) {
	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	_ = runner.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	if runner.Error() != nil {
		t.Errorf("context.Canceled should not be stored as error: %v", runner.Error())
	}
}

func TestPipelineRunner_CurrentPhase(t *testing.T) {
	phaseStarted := make(chan struct{})
	phaseComplete := make(chan struct{})

	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		close(phaseStarted)
		<-phaseComplete
		return nil
	})

	_ = runner.Start(context.Background())

	<-phaseStarted
	if runner.CurrentPhase() != PhaseWorker {
		t.Errorf("got current phase %v, want %v", runner.CurrentPhase(), PhaseWorker)
	}

	close(phaseComplete)
	time.Sleep(50 * time.Millisecond)
}

func TestPipelineRunner_CustomPhaseOrder(t *testing.T) {
	var order []PipelinePhase
	var mu sync.Mutex

	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})
	runner.SetPhaseOrder([]PipelinePhase{PhaseVerify, PhaseWorker})

	runner.RegisterPhase(PhaseVerify, func(ctx context.Context) error {
		mu.Lock()
		order = append(order, PhaseVerify)
		mu.Unlock()
		return nil
	})

	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		mu.Lock()
		order = append(order, PhaseWorker)
		mu.Unlock()
		return nil
	})

	_ = runner.Start(context.Background())
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 2 {
		t.Fatalf("got %d phases, want 2", len(order))
	}

	if order[0] != PhaseVerify || order[1] != PhaseWorker {
		t.Errorf("got order %v, want [verify, worker]", order)
	}
}

func TestPipelineRunner_SkipUnregisteredPhases(t *testing.T) {
	var executed atomic.Bool

	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		executed.Store(true)
		return nil
	})

	_ = runner.Start(context.Background())
	time.Sleep(100 * time.Millisecond)

	if !executed.Load() {
		t.Error("worker phase should have executed")
	}

	if runner.Lifecycle().State() != StateStopped {
		t.Errorf("got state %v, want %v", runner.Lifecycle().State(), StateStopped)
	}
}

func TestPipelineRunner_ConcurrentStartStop(t *testing.T) {
	runner := NewPipelineRunner(PipelineRunnerConfig{
		ID:              "test",
		ShutdownTimeout: time.Second,
	})

	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			runner.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
			runner.Stop()
		}()
	}

	wg.Wait()

	state := runner.Lifecycle().State()
	if state != StateStopped && state != StateFailed {
		t.Errorf("got state %v, want stopped or failed", state)
	}
}

func TestPipelinePhases(t *testing.T) {
	phases := []PipelinePhase{
		PhaseInspector,
		PhaseTester,
		PhaseWorker,
		PhaseVerify,
	}

	for _, p := range phases {
		if p == "" {
			t.Error("phase should not be empty")
		}
	}

	if len(DefaultPipelinePhases) != 4 {
		t.Errorf("got %d default phases, want 4", len(DefaultPipelinePhases))
	}
}

func TestPipelineRunner_WaitForState(t *testing.T) {
	runner := NewPipelineRunner(PipelineRunnerConfig{ID: "test"})
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	done := make(chan error, 1)
	go func() {
		done <- runner.WaitForState(StateRunning, time.Second)
	}()

	time.Sleep(10 * time.Millisecond)
	_ = runner.Start(context.Background())

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("WaitForState did not return")
	}

	runner.Stop()
}
