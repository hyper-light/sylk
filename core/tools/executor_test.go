package tools

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolExecutor_DirectExecution(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "echo",
		Args:    []string{"hello"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, string(result.Stdout), "hello")
	assert.False(t, result.Killed)
}

func TestToolExecutor_ShellExecution(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "echo hello && echo world",
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, string(result.Stdout), "hello")
	assert.Contains(t, string(result.Stdout), "world")
}

func TestToolExecutor_ShellDetection(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	tests := []struct {
		command    string
		needsShell bool
	}{
		{"echo hello", false},
		{"ls -la", false},
		{"echo hello | grep hello", true},
		{"echo hello && echo world", true},
		{"echo hello || echo world", true},
		{"echo hello; echo world", true},
		{"ls *.go", true},
		{"echo $HOME", true},
		{"echo `date`", true},
		{"(echo hello)", true},
		{"echo hello > /tmp/test", true},
	}

	for _, tc := range tests {
		t.Run(tc.command, func(t *testing.T) {
			assert.Equal(t, tc.needsShell, executor.NeedsShell(tc.command))
		})
	}
}

func TestToolExecutor_EnvBlocklist(t *testing.T) {
	os.Setenv("TEST_API_KEY", "secret")
	os.Setenv("TEST_NORMAL", "value")
	defer os.Unsetenv("TEST_API_KEY")
	defer os.Unsetenv("TEST_NORMAL")

	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "env",
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	output := string(result.Stdout)
	assert.NotContains(t, output, "TEST_API_KEY")
	assert.Contains(t, output, "TEST_NORMAL")
}

func TestToolExecutor_WorkingDirValidation(t *testing.T) {
	tempDir := t.TempDir()
	resolvedTempDir, err := filepath.EvalSymlinks(tempDir)
	require.NoError(t, err)

	cfg := DefaultToolExecutorConfig()
	cfg.AllowedDirs = []string{resolvedTempDir}
	executor := NewToolExecutor(cfg, nil)
	defer executor.Close()

	ctx := context.Background()

	inv := ToolInvocation{
		Command:    "pwd",
		WorkingDir: resolvedTempDir,
		Timeout:    5 * time.Second,
	}
	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)
	assert.Contains(t, string(result.Stdout), filepath.Base(resolvedTempDir))

	inv.WorkingDir = "/usr"
	_, err = executor.Execute(ctx, inv)
	assert.ErrorIs(t, err, ErrWorkingDirOutside)

	inv.WorkingDir = "relative/path"
	_, err = executor.Execute(ctx, inv)
	assert.ErrorIs(t, err, ErrWorkingDirNotAbsolute)
}

func TestToolExecutor_Timeout(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "sh",
		Args:    []string{"-c", "sleep 10"},
		Timeout: 100 * time.Millisecond,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.True(t, result.Killed)
	assert.Equal(t, "timeout", result.KillSignal)
	assert.True(t, result.Partial)
}

func TestToolExecutor_ContextCancellation(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	inv := ToolInvocation{
		Command: "sh",
		Args:    []string{"-c", "sleep 10"},
		Timeout: 10 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.True(t, result.Killed)
	assert.Equal(t, "context", result.KillSignal)
}

func TestToolExecutor_NonZeroExitCode(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "sh",
		Args:    []string{"-c", "exit 42"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.Equal(t, 42, result.ExitCode)
	assert.False(t, result.Killed)
}

func TestToolExecutor_Stderr(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "sh -c 'echo error >&2'",
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.Contains(t, string(result.Stderr), "error")
}

func TestToolExecutor_ExtraEnv(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "sh -c 'echo $MY_VAR'",
		Env:     map[string]string{"MY_VAR": "test_value"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.Contains(t, string(result.Stdout), "test_value")
}

func TestToolExecutor_ActiveCount(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	assert.Equal(t, 0, executor.ActiveCount())

	ctx := context.Background()
	done := make(chan struct{})

	go func() {
		inv := ToolInvocation{
			Command: "sh",
			Args:    []string{"-c", "sleep 5"},
			Timeout: 10 * time.Second,
		}
		executor.Execute(ctx, inv)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, executor.ActiveCount())

	executor.KillAll()
	<-done

	assert.Equal(t, 0, executor.ActiveCount())
}

func TestToolExecutor_ClosedOperations(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	require.NoError(t, executor.Close())

	ctx := context.Background()
	_, err := executor.Execute(ctx, ToolInvocation{Command: "echo hello"})
	assert.ErrorIs(t, err, ErrExecutorClosed)

	err = executor.Close()
	assert.ErrorIs(t, err, ErrExecutorClosed)
}

func TestToolExecutor_Duration(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "sh",
		Args:    []string{"-c", "sleep 0.1"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, result.Duration, 100*time.Millisecond)
	assert.Less(t, result.Duration, 1*time.Second)
}

func TestToolExecutor_StdinReader(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig(), nil)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "cat",
		Stdin:   strings.NewReader("input from stdin"),
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, string(result.Stdout), "input from stdin")
}

func TestToolExecutor_DefaultConfig(t *testing.T) {
	cfg := DefaultToolExecutorConfig()

	assert.Equal(t, DefaultShellPatterns, cfg.ShellPatterns)
	assert.Equal(t, DefaultEnvBlocklist, cfg.EnvBlocklist)
	assert.Equal(t, 60*time.Second, cfg.DefaultTimeout)
	assert.Equal(t, 1*time.Second, cfg.CheckInterval)
}

func TestToolExecutor_WithResourcePool(t *testing.T) {
	pool := &mockResourcePool{}

	deps := &ToolExecutorDeps{
		ResourcePool: pool,
	}

	executor := NewToolExecutor(DefaultToolExecutorConfig(), deps)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "echo",
		Args:    []string{"hello"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.True(t, pool.acquired)
	assert.True(t, pool.released)
}

func TestToolExecutor_PoolExhausted(t *testing.T) {
	pool := &mockResourcePool{shouldFail: true}
	deps := &ToolExecutorDeps{
		ResourcePool: pool,
	}

	executor := NewToolExecutor(DefaultToolExecutorConfig(), deps)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Command: "echo",
		Args:    []string{"hello"},
		Timeout: 5 * time.Second,
	}

	_, err := executor.Execute(ctx, inv)
	assert.ErrorIs(t, err, ErrPoolExhausted)
}

func TestToolExecutor_ToolField(t *testing.T) {
	cfg := DefaultToolExecutorConfig()
	cfg.ToolTimeouts = map[string]time.Duration{
		"slow_tool": 10 * time.Minute,
	}

	executor := NewToolExecutor(cfg, nil)
	defer executor.Close()

	timeout := executor.getTimeout(ToolInvocation{Tool: "slow_tool"})
	assert.Equal(t, 10*time.Minute, timeout)

	timeout = executor.getTimeout(ToolInvocation{Tool: "unknown"})
	assert.Equal(t, 60*time.Second, timeout)

	timeout = executor.getTimeout(ToolInvocation{Tool: "slow_tool", Timeout: 5 * time.Second})
	assert.Equal(t, 5*time.Second, timeout)
}

func TestToolExecutor_ParsedOutput(t *testing.T) {
	handler := &mockOutputStreamer{
		parsedResult: map[string]string{"key": "value"},
	}
	deps := &ToolExecutorDeps{
		OutputHandler: handler,
	}

	executor := NewToolExecutor(DefaultToolExecutorConfig(), deps)
	defer executor.Close()

	ctx := context.Background()
	inv := ToolInvocation{
		Tool:    "test_tool",
		Command: "echo",
		Args:    []string{"hello"},
		Timeout: 5 * time.Second,
	}

	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)
	assert.NotNil(t, result.ParsedOutput)
}

func TestToolExecutor_BoundaryValidation(t *testing.T) {
	tempDir := t.TempDir()
	resolvedTempDir, err := filepath.EvalSymlinks(tempDir)
	require.NoError(t, err)

	cfg := DefaultToolExecutorConfig()
	cfg.ProjectRoot = resolvedTempDir

	executor := NewToolExecutor(cfg, nil)
	defer executor.Close()

	ctx := context.Background()

	inv := ToolInvocation{
		Command:    "pwd",
		WorkingDir: resolvedTempDir,
		Timeout:    5 * time.Second,
	}
	_, err = executor.Execute(ctx, inv)
	require.NoError(t, err)

	inv.WorkingDir = "/usr"
	_, err = executor.Execute(ctx, inv)
	assert.ErrorIs(t, err, ErrWorkingDirOutside)
}

type mockResourcePool struct {
	shouldFail bool
	acquired   bool
	released   bool
	handles    chan *mockHandle
}

func (m *mockResourcePool) Acquire(ctx context.Context, sessionID string, priority int) (ResourceHandle, error) {
	if m.shouldFail {
		return nil, ErrPoolExhausted
	}
	m.acquired = true
	if m.handles != nil {
		return <-m.handles, nil
	}
	return &mockHandle{pool: m}, nil
}

type mockHandle struct {
	pool *mockResourcePool
}

func (m *mockHandle) Release() {
	if m.pool != nil {
		m.pool.released = true
	}
}

type mockOutputStreamer struct {
	parsedResult interface{}
}

func (m *mockOutputStreamer) CreateStreams(streamTo io.Writer) (stdout, stderr *StreamWriter) {
	return NewStreamWriter(nil, 1024*1024), NewStreamWriter(nil, 1024*1024)
}

func (m *mockOutputStreamer) ProcessOutput(tool string, stdout, stderr []byte) *ProcessedOutput {
	if m.parsedResult != nil {
		return &ProcessedOutput{
			Type:   OutputTypeParsed,
			Parsed: m.parsedResult,
		}
	}
	return nil
}
