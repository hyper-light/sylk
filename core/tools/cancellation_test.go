package tools

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultCancellationConfig(t *testing.T) {
	cfg := DefaultCancellationConfig()

	if cfg.TotalBudget != DefaultTotalBudget {
		t.Errorf("expected TotalBudget %v, got %v", DefaultTotalBudget, cfg.TotalBudget)
	}
	if cfg.AgentBudget != DefaultAgentBudget {
		t.Errorf("expected AgentBudget %v, got %v", DefaultAgentBudget, cfg.AgentBudget)
	}
	if cfg.ToolBudget != DefaultToolBudget {
		t.Errorf("expected ToolBudget %v, got %v", DefaultToolBudget, cfg.ToolBudget)
	}
	if cfg.CleanupBudget != DefaultCleanupBudget {
		t.Errorf("expected CleanupBudget %v, got %v", DefaultCleanupBudget, cfg.CleanupBudget)
	}
}

func TestNormalizeCancellationConfig(t *testing.T) {
	cfg := normalizeCancellationConfig(CancellationConfig{})

	if cfg.TotalBudget != DefaultTotalBudget {
		t.Errorf("expected default TotalBudget")
	}
	if cfg.AgentBudget != DefaultAgentBudget {
		t.Errorf("expected default AgentBudget")
	}
}

func TestNewCancellationManager(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())

	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.Stage() != StageIdle {
		t.Errorf("expected initial stage idle, got %v", mgr.Stage())
	}
	if mgr.RunningToolCount() != 0 {
		t.Errorf("expected 0 running tools")
	}
}

func TestRegisterAndUnregisterTool(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())

	mgr.RegisterTool("tool-1", "eslint", nil)
	if mgr.RunningToolCount() != 1 {
		t.Errorf("expected 1 running tool, got %d", mgr.RunningToolCount())
	}

	mgr.RegisterTool("tool-2", "prettier", nil)
	if mgr.RunningToolCount() != 2 {
		t.Errorf("expected 2 running tools, got %d", mgr.RunningToolCount())
	}

	mgr.UnregisterTool("tool-1")
	if mgr.RunningToolCount() != 1 {
		t.Errorf("expected 1 running tool after unregister, got %d", mgr.RunningToolCount())
	}

	mgr.UnregisterTool("tool-2")
	if mgr.RunningToolCount() != 0 {
		t.Errorf("expected 0 running tools after unregister, got %d", mgr.RunningToolCount())
	}
}

func TestSetPartialOutput(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())
	mgr.RegisterTool("tool-1", "eslint", nil)

	stdout := []byte("partial stdout")
	stderr := []byte("partial stderr")
	mgr.SetPartialOutput("tool-1", stdout, stderr)

	tools := mgr.getRunningTools()
	if len(tools) != 1 {
		t.Fatal("expected 1 running tool")
	}

	if string(tools[0].PartialOut) != "partial stdout" {
		t.Errorf("expected partial stdout to be set")
	}
	if string(tools[0].PartialErr) != "partial stderr" {
		t.Errorf("expected partial stderr to be set")
	}
}

func TestCancelNoRunningTools(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())
	ctx := context.Background()

	result := mgr.Cancel(ctx)

	if result.ToolsCancelled != 0 {
		t.Errorf("expected 0 tools cancelled")
	}
	if result.Duration == 0 {
		t.Errorf("expected non-zero duration")
	}
	if mgr.Stage() != StageComplete {
		t.Errorf("expected stage complete")
	}
}

func TestProgressCallback(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())

	var progressMessages []CancellationProgress
	var mu sync.Mutex

	mgr.SetProgressCallback(func(p CancellationProgress) {
		mu.Lock()
		progressMessages = append(progressMessages, p)
		mu.Unlock()
	})

	ctx := context.Background()
	mgr.Cancel(ctx)

	mu.Lock()
	defer mu.Unlock()

	if len(progressMessages) == 0 {
		t.Errorf("expected at least one progress message")
	}
}

func TestCleanupHandler(t *testing.T) {
	mgr := NewCancellationManager(CancellationConfig{
		TotalBudget:   1 * time.Second,
		AgentBudget:   800 * time.Millisecond,
		ToolBudget:    500 * time.Millisecond,
		CleanupBudget: 200 * time.Millisecond,
	})

	var cleanupCalled atomic.Int32

	mgr.AddCleanupHandler(func(ctx context.Context, toolID string) error {
		cleanupCalled.Add(1)
		return nil
	})

	mgr.RegisterTool("tool-1", "eslint", nil)

	ctx := context.Background()
	result := mgr.Cancel(ctx)

	if cleanupCalled.Load() != 1 {
		t.Errorf("expected cleanup called once, got %d", cleanupCalled.Load())
	}
	if result.CleanupsDone != 1 {
		t.Errorf("expected 1 cleanup done, got %d", result.CleanupsDone)
	}
}

func TestCleanupHandlerError(t *testing.T) {
	mgr := NewCancellationManager(CancellationConfig{
		TotalBudget:   1 * time.Second,
		AgentBudget:   800 * time.Millisecond,
		ToolBudget:    500 * time.Millisecond,
		CleanupBudget: 200 * time.Millisecond,
	})

	expectedErr := errors.New("cleanup failed")
	mgr.AddCleanupHandler(func(ctx context.Context, toolID string) error {
		return expectedErr
	})

	mgr.RegisterTool("tool-1", "eslint", nil)

	ctx := context.Background()
	result := mgr.Cancel(ctx)

	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
	if result.Errors[0] != expectedErr {
		t.Errorf("expected cleanup error to be recorded")
	}
}

func TestIsIdle(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())

	if !mgr.IsIdle() {
		t.Errorf("expected manager to be idle initially")
	}

	ctx := context.Background()
	mgr.Cancel(ctx)

	if !mgr.IsIdle() {
		t.Errorf("expected manager to be idle after cancel completes")
	}
}

func TestConfigAccessor(t *testing.T) {
	cfg := CancellationConfig{
		TotalBudget:   10 * time.Second,
		AgentBudget:   8 * time.Second,
		ToolBudget:    6 * time.Second,
		CleanupBudget: 2 * time.Second,
	}
	mgr := NewCancellationManager(cfg)

	returnedCfg := mgr.Config()
	if returnedCfg.TotalBudget != 10*time.Second {
		t.Errorf("expected TotalBudget 10s, got %v", returnedCfg.TotalBudget)
	}
}

func TestCancellationStageConstants(t *testing.T) {
	stages := []CancellationStage{
		StageIdle,
		StageCancelling,
		StageStopping,
		StageForceStopping,
		StageKilling,
		StageCleanup,
		StageComplete,
	}

	for _, stage := range stages {
		if stage == "" {
			t.Errorf("stage should not be empty")
		}
	}
}

func TestConcurrentRegistration(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			toolID := "tool-" + string(rune('0'+id%10))
			mgr.RegisterTool(toolID, "test", nil)
			mgr.UnregisterTool(toolID)
		}(i)
	}
	wg.Wait()
}

func TestMultipleCleanupHandlers(t *testing.T) {
	mgr := NewCancellationManager(CancellationConfig{
		TotalBudget:   1 * time.Second,
		AgentBudget:   800 * time.Millisecond,
		ToolBudget:    500 * time.Millisecond,
		CleanupBudget: 500 * time.Millisecond,
	})

	var calls atomic.Int32

	mgr.AddCleanupHandler(func(ctx context.Context, toolID string) error {
		calls.Add(1)
		return nil
	})
	mgr.AddCleanupHandler(func(ctx context.Context, toolID string) error {
		calls.Add(1)
		return nil
	})

	mgr.RegisterTool("tool-1", "eslint", nil)

	ctx := context.Background()
	result := mgr.Cancel(ctx)

	if calls.Load() != 2 {
		t.Errorf("expected 2 cleanup calls, got %d", calls.Load())
	}
	if result.CleanupsDone != 2 {
		t.Errorf("expected 2 cleanups done, got %d", result.CleanupsDone)
	}
}

func TestPartialResultsPreserved(t *testing.T) {
	mgr := NewCancellationManager(CancellationConfig{
		TotalBudget:   1 * time.Second,
		AgentBudget:   800 * time.Millisecond,
		ToolBudget:    500 * time.Millisecond,
		CleanupBudget: 200 * time.Millisecond,
	})

	mgr.RegisterTool("tool-1", "eslint", nil)
	mgr.SetPartialOutput("tool-1", []byte("stdout data"), []byte("stderr data"))

	ctx := context.Background()
	result := mgr.Cancel(ctx)

	if len(result.PartialResults) != 1 {
		t.Errorf("expected 1 partial result, got %d", len(result.PartialResults))
	}

	partial, ok := result.PartialResults["tool-1"]
	if !ok {
		t.Fatal("expected partial result for tool-1")
	}

	if !partial.Partial {
		t.Errorf("expected Partial flag to be true")
	}
	if string(partial.Stdout) != "stdout data" {
		t.Errorf("expected stdout to be preserved")
	}
	if string(partial.Stderr) != "stderr data" {
		t.Errorf("expected stderr to be preserved")
	}
}

func TestCancelContextTimeout(t *testing.T) {
	mgr := NewCancellationManager(CancellationConfig{
		TotalBudget:   5 * time.Second,
		AgentBudget:   4 * time.Second,
		ToolBudget:    3 * time.Second,
		CleanupBudget: 1 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := mgr.Cancel(ctx)

	if result.Duration == 0 {
		t.Errorf("expected non-zero duration")
	}
}

func TestUnregisterNonexistentTool(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())

	mgr.UnregisterTool("nonexistent")

	if mgr.RunningToolCount() != 0 {
		t.Errorf("expected 0 running tools")
	}
}

func TestSetPartialOutputNonexistentTool(t *testing.T) {
	mgr := NewCancellationManager(DefaultCancellationConfig())

	mgr.SetPartialOutput("nonexistent", []byte("stdout"), []byte("stderr"))
}

func TestDefaultCancelDuration(t *testing.T) {
	result := defaultCancelDuration(0, 5*time.Second)
	if result != 5*time.Second {
		t.Errorf("expected default value when zero")
	}

	result = defaultCancelDuration(10*time.Second, 5*time.Second)
	if result != 10*time.Second {
		t.Errorf("expected provided value when non-zero")
	}
}
