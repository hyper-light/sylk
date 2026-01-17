package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolExecutor_DirectExecution(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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

	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	// Create temp dir and resolve symlinks (macOS /tmp -> /private/tmp)
	tempDir := t.TempDir()
	resolvedTempDir, err := filepath.EvalSymlinks(tempDir)
	require.NoError(t, err)

	cfg := DefaultToolExecutorConfig()
	cfg.AllowedDirs = []string{resolvedTempDir}
	executor := NewToolExecutor(cfg)
	defer executor.Close()

	ctx := context.Background()

	// Test valid working directory
	inv := ToolInvocation{
		Command:    "pwd",
		WorkingDir: resolvedTempDir,
		Timeout:    5 * time.Second,
	}
	result, err := executor.Execute(ctx, inv)
	require.NoError(t, err)
	assert.Contains(t, string(result.Stdout), filepath.Base(resolvedTempDir))

	// Test working directory outside allowed boundaries
	inv.WorkingDir = "/usr"
	_, err = executor.Execute(ctx, inv)
	assert.ErrorIs(t, err, ErrWorkingDirOutside)

	// Test relative path error
	inv.WorkingDir = "relative/path"
	_, err = executor.Execute(ctx, inv)
	assert.ErrorIs(t, err, ErrWorkingDirNotAbsolute)
}

func TestToolExecutor_Timeout(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
	require.NoError(t, executor.Close())

	ctx := context.Background()
	_, err := executor.Execute(ctx, ToolInvocation{Command: "echo hello"})
	assert.ErrorIs(t, err, ErrExecutorClosed)

	err = executor.Close()
	assert.ErrorIs(t, err, ErrExecutorClosed)
}

func TestToolExecutor_Duration(t *testing.T) {
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
	executor := NewToolExecutor(DefaultToolExecutorConfig())
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
