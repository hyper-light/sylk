package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewAgentRunner(t *testing.T) {
	config := AgentRunnerConfig{
		ID:        "test-runner",
		AgentType: AgentGuide,
	}

	runner := NewAgentRunner(config, func(ctx context.Context) error {
		return nil
	})

	if runner.ID() != "test-runner" {
		t.Errorf("got ID %q, want %q", runner.ID(), "test-runner")
	}

	if runner.AgentType() != AgentGuide {
		t.Errorf("got AgentType %q, want %q", runner.AgentType(), AgentGuide)
	}

	if runner.config.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("got ShutdownTimeout %v, want %v", runner.config.ShutdownTimeout, DefaultShutdownTimeout)
	}
}

func TestAgentRunner_Start(t *testing.T) {
	var executed atomic.Bool

	runner := NewAgentRunner(AgentRunnerConfig{
		ID:              "test",
		ShutdownTimeout: time.Second,
	}, func(ctx context.Context) error {
		executed.Store(true)
		<-ctx.Done()
		return nil
	})

	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer runner.Stop()

	time.Sleep(50 * time.Millisecond)

	if !runner.IsRunning() {
		t.Error("expected runner to be running")
	}

	if !executed.Load() {
		t.Error("agent function not executed")
	}
}

func TestAgentRunner_Stop(t *testing.T) {
	runner := NewAgentRunner(AgentRunnerConfig{
		ID:              "test",
		ShutdownTimeout: time.Second,
	}, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	_ = runner.Start(context.Background())
	time.Sleep(50 * time.Millisecond)

	if err := runner.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	if runner.IsRunning() {
		t.Error("expected runner to not be running")
	}

	if runner.Lifecycle().State() != StateStopped {
		t.Errorf("got state %v, want %v", runner.Lifecycle().State(), StateStopped)
	}
}

func TestAgentRunner_StartAlreadyStarted(t *testing.T) {
	runner := NewAgentRunner(AgentRunnerConfig{ID: "test"}, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	ctx := context.Background()
	_ = runner.Start(ctx)
	defer runner.Stop()

	err := runner.Start(ctx)
	if err != ErrRunnerAlreadyStarted {
		t.Errorf("got error %v, want %v", err, ErrRunnerAlreadyStarted)
	}
}

func TestAgentRunner_StopNotRunning(t *testing.T) {
	runner := NewAgentRunner(AgentRunnerConfig{ID: "test"}, func(ctx context.Context) error {
		return nil
	})

	err := runner.Stop()
	if err != ErrRunnerNotRunning {
		t.Errorf("got error %v, want %v", err, ErrRunnerNotRunning)
	}
}

func TestAgentRunner_StopAlreadyStopped(t *testing.T) {
	runner := NewAgentRunner(AgentRunnerConfig{ID: "test"}, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	_ = runner.Start(context.Background())
	_ = runner.Stop()

	err := runner.Stop()
	if err != nil {
		t.Errorf("got error %v, want nil", err)
	}
}

func TestAgentRunner_AgentFunctionError(t *testing.T) {
	expectedErr := errors.New("agent error")

	runner := NewAgentRunner(AgentRunnerConfig{ID: "test"}, func(ctx context.Context) error {
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

func TestAgentRunner_PanicRecovery(t *testing.T) {
	var panicHandled atomic.Bool
	var panicValue atomic.Value

	runner := NewAgentRunner(AgentRunnerConfig{
		ID: "test",
		OnPanic: func(id string, recovered any) {
			panicHandled.Store(true)
			panicValue.Store(recovered)
		},
	}, func(ctx context.Context) error {
		panic("test panic")
	})

	_ = runner.Start(context.Background())
	time.Sleep(50 * time.Millisecond)

	if !panicHandled.Load() {
		t.Error("panic handler not called")
	}

	if panicValue.Load() != "test panic" {
		t.Errorf("got panic value %v, want %q", panicValue.Load(), "test panic")
	}

	if runner.Lifecycle().State() != StateFailed {
		t.Errorf("got state %v, want %v", runner.Lifecycle().State(), StateFailed)
	}
}

func TestAgentRunner_ContextCancellation(t *testing.T) {
	runner := NewAgentRunner(AgentRunnerConfig{ID: "test"}, func(ctx context.Context) error {
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

func TestAgentRunner_ConcurrentStartStop(t *testing.T) {
	runner := NewAgentRunner(AgentRunnerConfig{
		ID:              "test",
		ShutdownTimeout: time.Second,
	}, func(ctx context.Context) error {
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

func TestAgentRunner_WaitForState(t *testing.T) {
	runner := NewAgentRunner(AgentRunnerConfig{ID: "test"}, func(ctx context.Context) error {
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

func TestAgentRunner_ShutdownTimeout(t *testing.T) {
	runner := NewAgentRunner(AgentRunnerConfig{
		ID:              "test",
		ShutdownTimeout: 50 * time.Millisecond,
	}, func(ctx context.Context) error {
		time.Sleep(time.Second)
		return nil
	})

	_ = runner.Start(context.Background())
	time.Sleep(10 * time.Millisecond)

	err := runner.Stop()
	if err != ErrShutdownTimeout {
		t.Errorf("got error %v, want %v", err, ErrShutdownTimeout)
	}
}

func TestAgentTypes(t *testing.T) {
	types := []AgentType{
		AgentGuide,
		AgentArchitect,
		AgentOrchestrator,
		AgentLibrarian,
		AgentArchivalist,
		AgentAcademic,
	}

	for _, at := range types {
		if at == "" {
			t.Error("agent type should not be empty")
		}
	}
}
