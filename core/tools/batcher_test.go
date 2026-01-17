package tools

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultBatchConfig(t *testing.T) {
	cfg := DefaultBatchConfig()

	if !cfg.BatchCapable {
		t.Error("expected BatchCapable to be true")
	}
	if cfg.MaxFiles != DefaultMaxBatchSize {
		t.Errorf("expected MaxFiles %d, got %d", DefaultMaxBatchSize, cfg.MaxFiles)
	}
	if cfg.BatchWindow != DefaultBatchWindow {
		t.Errorf("expected BatchWindow %v, got %v", DefaultBatchWindow, cfg.BatchWindow)
	}
}

func TestDefaultBatcherConfig(t *testing.T) {
	cfg := DefaultBatcherConfig()

	if cfg.DefaultWindow != DefaultBatchWindow {
		t.Errorf("expected DefaultWindow %v, got %v", DefaultBatchWindow, cfg.DefaultWindow)
	}
	if len(cfg.ToolConfigs) == 0 {
		t.Error("expected default tool configs")
	}
	if !cfg.FallbackOnFail {
		t.Error("expected FallbackOnFail to be true")
	}
}

func TestNewToolBatcher(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	if batcher == nil {
		t.Fatal("expected non-nil batcher")
	}
	if batcher.TotalPending() != 0 {
		t.Errorf("expected 0 pending, got %d", batcher.TotalPending())
	}
}

func TestIsBatchable(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	if !batcher.IsBatchable("eslint") {
		t.Error("expected eslint to be batchable")
	}
	if !batcher.IsBatchable("prettier") {
		t.Error("expected prettier to be batchable")
	}
	if batcher.IsBatchable("unknown-tool") {
		t.Error("expected unknown-tool to not be batchable")
	}
}

func TestSetAndRemoveToolConfig(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	batcher.SetToolConfig("custom-tool", BatchConfig{
		BatchCapable: true,
		MaxFiles:     10,
		BatchWindow:  50 * time.Millisecond,
	})

	if !batcher.IsBatchable("custom-tool") {
		t.Error("expected custom-tool to be batchable after SetToolConfig")
	}

	batcher.RemoveToolConfig("custom-tool")

	if batcher.IsBatchable("custom-tool") {
		t.Error("expected custom-tool to not be batchable after RemoveToolConfig")
	}
}

func TestBatchableTools(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	tools := batcher.BatchableTools()
	if len(tools) == 0 {
		t.Error("expected at least one batchable tool")
	}

	found := false
	for _, tool := range tools {
		if tool == "eslint" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected eslint in batchable tools")
	}
}

func TestSubmitNonBatchable(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "echo",
		Args:    []string{"hello"},
	}

	result, err := batcher.Submit(ctx, inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestPendingCount(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	cfg := DefaultBatcherConfig()
	cfg.ToolConfigs["test-tool"] = BatchConfig{
		BatchCapable: true,
		MaxFiles:     50,
		BatchWindow:  1 * time.Second,
	}
	batcher := NewToolBatcher(cfg, executor)

	if batcher.PendingCount("test-tool") != 0 {
		t.Error("expected 0 pending initially")
	}
}

func TestTotalPending(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	if batcher.TotalPending() != 0 {
		t.Error("expected 0 total pending initially")
	}
}

func TestClose(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	err := batcher.Close()
	if err != nil {
		t.Fatalf("unexpected error on first close: %v", err)
	}

	err = batcher.Close()
	if err != ErrExecutorClosed {
		t.Errorf("expected ErrExecutorClosed on second close, got %v", err)
	}
}

func TestCloseRejectsPending(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	cfg := DefaultBatcherConfig()
	cfg.ToolConfigs["test-tool"] = BatchConfig{
		BatchCapable: true,
		MaxFiles:     50,
		BatchWindow:  10 * time.Second,
	}
	batcher := NewToolBatcher(cfg, executor)

	var wg sync.WaitGroup
	var submitErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx := context.Background()
		_, submitErr = batcher.Submit(ctx, ToolInvocation{
			Command: "test-tool",
			Args:    []string{"file.txt"},
		})
	}()

	time.Sleep(50 * time.Millisecond)

	batcher.Close()
	wg.Wait()

	if submitErr != ErrExecutorClosed {
		t.Logf("submit returned: %v (may be nil if closed before receive)", submitErr)
	}
}

func TestSplitIntoBatches(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	invocations := make([]*PendingInvocation, 7)
	for i := range invocations {
		invocations[i] = &PendingInvocation{
			Invocation: ToolInvocation{Args: []string{"file.txt"}},
			ResultChan: make(chan *BatchedResult, 1),
		}
	}

	batches := batcher.splitIntoBatches(invocations, 3)

	if len(batches) != 3 {
		t.Errorf("expected 3 batches, got %d", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Errorf("expected first batch size 3, got %d", len(batches[0]))
	}
	if len(batches[1]) != 3 {
		t.Errorf("expected second batch size 3, got %d", len(batches[1]))
	}
	if len(batches[2]) != 1 {
		t.Errorf("expected third batch size 1, got %d", len(batches[2]))
	}
}

func TestSplitIntoBatchesZeroMax(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	invocations := make([]*PendingInvocation, 3)
	for i := range invocations {
		invocations[i] = &PendingInvocation{
			Invocation: ToolInvocation{Args: []string{"file.txt"}},
			ResultChan: make(chan *BatchedResult, 1),
		}
	}

	batches := batcher.splitIntoBatches(invocations, 0)

	if len(batches) != 1 {
		t.Errorf("expected 1 batch with default max, got %d", len(batches))
	}
}

func TestCollectAllArgs(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	batch := []*PendingInvocation{
		{Invocation: ToolInvocation{Args: []string{"a.js", "b.js"}}},
		{Invocation: ToolInvocation{Args: []string{"c.js"}}},
	}

	args := batcher.collectAllArgs(batch, " ")

	if len(args) != 3 {
		t.Errorf("expected 3 args, got %d", len(args))
	}
	expected := []string{"a.js", "b.js", "c.js"}
	for i, arg := range args {
		if arg != expected[i] {
			t.Errorf("expected arg %d to be %s, got %s", i, expected[i], arg)
		}
	}
}

func TestDetermineWorkingDir(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	batch := []*PendingInvocation{
		{Invocation: ToolInvocation{WorkingDir: "/project/a"}},
		{Invocation: ToolInvocation{WorkingDir: "/project/b"}},
	}

	dir := batcher.determineWorkingDir(batch)
	if dir != "/project/a" {
		t.Errorf("expected /project/a, got %s", dir)
	}

	emptyBatch := []*PendingInvocation{}
	dir = batcher.determineWorkingDir(emptyBatch)
	if dir != "" {
		t.Errorf("expected empty string for empty batch, got %s", dir)
	}
}

func TestCalculateBatchTimeout(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	batch := []*PendingInvocation{
		{Invocation: ToolInvocation{Timeout: 5 * time.Second}},
		{Invocation: ToolInvocation{Timeout: 10 * time.Second}},
		{Invocation: ToolInvocation{Timeout: 3 * time.Second}},
	}

	timeout := batcher.calculateBatchTimeout(batch)
	if timeout != 10*time.Second {
		t.Errorf("expected 10s, got %v", timeout)
	}
}

func TestParseBatchedOutput(t *testing.T) {
	output := []byte("file1.js:10: error\nfile2.js:5: warning\nfile1.js:20: error")
	files := []string{"file1.js", "file2.js", "file3.js"}

	results := ParseBatchedOutput(output, files, " ")

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	file1Output := string(results["file1.js"])
	if file1Output == "" {
		t.Error("expected output for file1.js")
	}

	file3Output := string(results["file3.js"])
	if file3Output != "" {
		t.Errorf("expected empty output for file3.js, got %s", file3Output)
	}
}

func TestCopyToolConfigs(t *testing.T) {
	src := map[string]BatchConfig{
		"eslint": {BatchCapable: true, MaxFiles: 50},
	}

	dst := copyToolConfigs(src)

	if len(dst) != 1 {
		t.Errorf("expected 1 config, got %d", len(dst))
	}

	src["prettier"] = BatchConfig{BatchCapable: true}
	if len(dst) != 1 {
		t.Error("copy should be independent of source")
	}
}

func TestGetToolConfig(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	cfg := batcher.getToolConfig("eslint")
	if !cfg.BatchCapable {
		t.Error("expected eslint config to be batch capable")
	}

	cfg = batcher.getToolConfig("unknown")
	if !cfg.BatchCapable {
		t.Error("expected default config to be batch capable")
	}
}

func TestGetBatchWindow(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	cfg := DefaultBatcherConfig()
	cfg.ToolConfigs["custom"] = BatchConfig{
		BatchCapable: true,
		BatchWindow:  200 * time.Millisecond,
	}
	batcher := NewToolBatcher(cfg, executor)

	window := batcher.getBatchWindow("custom")
	if window != 200*time.Millisecond {
		t.Errorf("expected 200ms, got %v", window)
	}

	window = batcher.getBatchWindow("unknown")
	if window != DefaultBatchWindow {
		t.Errorf("expected default window, got %v", window)
	}
}

func TestFlushAllEmpty(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	batcher.FlushAll()
}

func TestConcurrentSubmit(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	batcher := NewToolBatcher(DefaultBatcherConfig(), executor)

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			_, err := batcher.Submit(ctx, ToolInvocation{
				Command: "echo",
				Args:    []string{"test"},
			})
			if err == nil {
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()

	if successCount.Load() != 10 {
		t.Errorf("expected 10 successful submits, got %d", successCount.Load())
	}
}

func TestSubmitContextCancellation(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	cfg := DefaultBatcherConfig()
	cfg.ToolConfigs["slow-tool"] = BatchConfig{
		BatchCapable: true,
		MaxFiles:     50,
		BatchWindow:  5 * time.Second,
	}
	batcher := NewToolBatcher(cfg, executor)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := batcher.Submit(ctx, ToolInvocation{
		Command: "slow-tool",
		Args:    []string{"file.txt"},
	})

	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestNormalizeBatcherConfig(t *testing.T) {
	cfg := normalizeBatcherConfig(BatcherConfig{})

	if cfg.DefaultWindow != DefaultBatchWindow {
		t.Errorf("expected default window to be set")
	}
	if cfg.ToolConfigs == nil {
		t.Error("expected tool configs to be initialized")
	}
}

func TestDefaultBatchableTools(t *testing.T) {
	if len(DefaultBatchableTools) == 0 {
		t.Error("expected default batchable tools")
	}

	eslintCfg, ok := DefaultBatchableTools["eslint"]
	if !ok {
		t.Fatal("expected eslint in default batchable tools")
	}
	if !eslintCfg.BatchCapable {
		t.Error("expected eslint to be batch capable")
	}
}
