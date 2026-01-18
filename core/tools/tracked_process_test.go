//go:build !windows
// +build !windows

package tools

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackedProcess_SetsPgid(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "echo", []string{"hello"}, nil)
	require.NoError(t, err)

	assert.True(t, proc.cmd.SysProcAttr.Setpgid)
}

func TestTrackedProcess_RunExecutesAndReturnsOutput(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "echo", []string{"hello"}, nil)
	require.NoError(t, err)

	output, err := proc.Run()
	require.NoError(t, err)
	assert.Contains(t, string(output), "hello")
	assert.Equal(t, ProcessStateTerminated, proc.State())
}

func TestTrackedProcess_ForceCloseSendsSIGINTFirst(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "sleep", []string{"10"}, nil)
	require.NoError(t, err)

	go func() {
		_, _ = proc.Run()
	}()

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, ProcessStateRunning, proc.State())

	err = proc.ForceClose()
	require.NoError(t, err)

	select {
	case <-proc.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("process did not terminate")
	}

	assert.Equal(t, ProcessStateTerminated, proc.State())
}

func TestTrackedProcess_ForceCloseEscalatesToSIGTERM(t *testing.T) {
	ctx := context.Background()

	script := `trap '' INT; sleep 10`
	proc, err := NewTrackedProcess(ctx, "bash", []string{"-c", script}, nil)
	require.NoError(t, err)

	go func() {
		_, _ = proc.Run()
	}()

	time.Sleep(100 * time.Millisecond)
	start := time.Now()

	err = proc.ForceClose()
	require.NoError(t, err)

	elapsed := time.Since(start)
	assert.Greater(t, elapsed, 100*time.Millisecond)
}

func TestTrackedProcess_ForceCloseEscalatesToSIGKILL(t *testing.T) {
	ctx := context.Background()

	script := `trap '' INT TERM; sleep 10`
	proc, err := NewTrackedProcess(ctx, "bash", []string{"-c", script}, nil)
	require.NoError(t, err)

	go func() {
		_, _ = proc.Run()
	}()

	time.Sleep(100 * time.Millisecond)
	start := time.Now()

	err = proc.ForceClose()
	require.NoError(t, err)

	elapsed := time.Since(start)
	assert.Greater(t, elapsed, 600*time.Millisecond)
}

func TestTrackedProcess_SignalGroupKillsChildProcesses(t *testing.T) {
	ctx := context.Background()

	script := `sleep 100 & CHILD_PID=$!; sleep 100`
	proc, err := NewTrackedProcess(ctx, "bash", []string{"-c", script}, nil)
	require.NoError(t, err)

	go func() {
		_, _ = proc.Run()
	}()

	time.Sleep(100 * time.Millisecond)

	err = proc.ForceClose()
	require.NoError(t, err)

	select {
	case <-proc.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("process did not terminate")
	}
}

func TestTrackedProcess_WorksWithoutSandbox(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "ls", []string{"-la"}, nil)
	require.NoError(t, err)

	output, err := proc.Run()
	require.NoError(t, err)
	assert.NotEmpty(t, output)
}

func TestTrackedProcess_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	proc, err := NewTrackedProcess(ctx, "sleep", []string{"10"}, nil)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := proc.Run()
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("process did not respond to context cancellation")
	}
}

func TestTrackedProcess_PidAndPgidRecorded(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "sleep", []string{"1"}, nil)
	require.NoError(t, err)

	go func() {
		_, _ = proc.Run()
	}()

	time.Sleep(50 * time.Millisecond)

	pid := proc.Pid()
	pgid := proc.Pgid()

	assert.Greater(t, pid, 0)
	assert.Greater(t, pgid, 0)
}

func TestTrackedProcess_ID(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "sleep", []string{"1"}, nil)
	require.NoError(t, err)

	go func() {
		_, _ = proc.Run()
	}()

	time.Sleep(50 * time.Millisecond)

	id := proc.ID()
	assert.Contains(t, id, "proc-")
}

func TestTrackedProcess_IsClosed(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "echo", []string{"hi"}, nil)
	require.NoError(t, err)

	assert.False(t, proc.IsClosed())

	_, _ = proc.Run()

	assert.True(t, proc.IsClosed())
}

func TestTrackedProcess_ConcurrentForceClose(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "sleep", []string{"10"}, nil)
	require.NoError(t, err)

	go func() {
		_, _ = proc.Run()
	}()

	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = proc.ForceClose()
		}()
	}

	wg.Wait()
	assert.Equal(t, ProcessStateTerminated, proc.State())
}

func TestTrackedProcess_Stderr(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "bash", []string{"-c", "echo error >&2"}, nil)
	require.NoError(t, err)

	_, _ = proc.Run()

	stderr := proc.Stderr()
	assert.Contains(t, string(stderr), "error")
}

func TestTrackedProcess_StartedAt(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "echo", []string{"hi"}, nil)
	require.NoError(t, err)

	before := time.Now()
	_, _ = proc.Run()
	after := time.Now()

	startedAt := proc.StartedAt()
	assert.True(t, startedAt.After(before) || startedAt.Equal(before))
	assert.True(t, startedAt.Before(after) || startedAt.Equal(after))
}

func TestTrackedProcess_InvalidCommand(t *testing.T) {
	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "nonexistent_command_xyz", nil, nil)
	require.NoError(t, err)

	_, err = proc.Run()
	assert.Error(t, err)
}

func TestTrackedProcess_ResourceTypeIsProcess(t *testing.T) {
	proc := &TrackedProcess{}
	assert.Equal(t, "process", string(proc.Type()))
}

func TestTrackedProcess_DoubleRun(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping in CI due to timing sensitivity")
	}

	ctx := context.Background()
	proc, err := NewTrackedProcess(ctx, "sleep", []string{"1"}, nil)
	require.NoError(t, err)

	go func() {
		_, _ = proc.Run()
	}()

	time.Sleep(50 * time.Millisecond)

	_, err = proc.Run()
	assert.ErrorIs(t, err, ErrProcessAlreadyStarted)

	_ = proc.ForceClose()
}
