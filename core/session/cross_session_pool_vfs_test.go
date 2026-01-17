package session

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestPoolVFS(t *testing.T) (*CrossSessionPoolVFS, *SignalDispatcherVFS, func()) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "pool_vfs_test_*")
	require.NoError(t, err)

	baseDispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   tempDir,
		SessionID: "test-session",
	})
	require.NoError(t, err)

	dispatcher := NewSignalDispatcherVFS(SignalDispatcherVFSConfig{
		BaseDispatcher: baseDispatcher,
	})

	calculator := NewFairShareCalculator(nil, DefaultFairShareConfig())
	basePool := NewCrossSessionPool(
		DefaultCrossSessionPoolConfig(),
		calculator,
		baseDispatcher,
		nil,
	)

	pool := NewCrossSessionPoolVFS(CrossSessionPoolVFSConfig{
		BasePool:        basePool,
		Dispatcher:      dispatcher,
		FileLockTimeout: 100 * time.Millisecond,
	})

	cleanup := func() {
		basePool.Close()
		baseDispatcher.Close()
		os.RemoveAll(tempDir)
	}

	return pool, dispatcher, cleanup
}

func TestNewCrossSessionPoolVFS(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	assert.NotNil(t, pool)
	assert.NotNil(t, pool.base)
	assert.NotNil(t, pool.dispatcher)
	assert.NotNil(t, pool.fileLocks)
	assert.NotNil(t, pool.waiters)
}

func TestCrossSessionPoolVFS_DefaultTimeout(t *testing.T) {
	pool := NewCrossSessionPoolVFS(CrossSessionPoolVFSConfig{})
	assert.Equal(t, DefaultFileLockTimeout, pool.fileLockTimeout)
}

func TestCrossSessionPoolVFS_AcquireFileAccess(t *testing.T) {
	t.Run("successful write lock", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		locks, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file1.go"}, FileAccessWrite)

		require.NoError(t, err)
		assert.Len(t, locks, 1)
		assert.Equal(t, "file1.go", locks[0].FilePath)
		assert.Equal(t, "session-1", locks[0].SessionID)
		assert.Equal(t, FileAccessWrite, locks[0].Mode)
	})

	t.Run("successful read lock", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		locks, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file1.go"}, FileAccessRead)

		require.NoError(t, err)
		assert.Len(t, locks, 1)
		assert.Equal(t, FileAccessRead, locks[0].Mode)
	})

	t.Run("multiple files", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		files := []string{"file1.go", "file2.go", "file3.go"}
		locks, err := pool.AcquireFileAccess(ctx, "session-1", files, FileAccessWrite)

		require.NoError(t, err)
		assert.Len(t, locks, 3)
	})

	t.Run("empty session ID", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		_, err := pool.AcquireFileAccess(ctx, "", []string{"file1.go"}, FileAccessWrite)

		assert.ErrorIs(t, err, ErrEmptySessionID)
	})

	t.Run("no files specified", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		_, err := pool.AcquireFileAccess(ctx, "session-1", []string{}, FileAccessWrite)

		assert.ErrorIs(t, err, ErrNoFilesSpecified)
	})

	t.Run("nil files", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		_, err := pool.AcquireFileAccess(ctx, "session-1", nil, FileAccessWrite)

		assert.ErrorIs(t, err, ErrNoFilesSpecified)
	})
}

func TestCrossSessionPoolVFS_ConcurrentReads(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	locks1, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessRead)
	require.NoError(t, err)
	assert.Len(t, locks1, 1)

	locks2, err := pool.AcquireFileAccess(ctx, "session-2", []string{"file.go"}, FileAccessRead)
	require.NoError(t, err)
	assert.Len(t, locks2, 1)

	locks3, err := pool.AcquireFileAccess(ctx, "session-3", []string{"file.go"}, FileAccessRead)
	require.NoError(t, err)
	assert.Len(t, locks3, 1)

	require.NoError(t, pool.ReleaseFileAccess(locks1))
	require.NoError(t, pool.ReleaseFileAccess(locks2))
	require.NoError(t, pool.ReleaseFileAccess(locks3))
}

func TestCrossSessionPoolVFS_ExclusiveWrite(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	locks1, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
	require.NoError(t, err)

	ctx2, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_, err = pool.AcquireFileAccess(ctx2, "session-2", []string{"file.go"}, FileAccessWrite)
	assert.Error(t, err)

	require.NoError(t, pool.ReleaseFileAccess(locks1))
}

func TestCrossSessionPoolVFS_WriteBlocksRead(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	locks1, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
	require.NoError(t, err)

	ctx2, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_, err = pool.AcquireFileAccess(ctx2, "session-2", []string{"file.go"}, FileAccessRead)
	assert.Error(t, err)

	require.NoError(t, pool.ReleaseFileAccess(locks1))
}

func TestCrossSessionPoolVFS_ReadBlocksWrite(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	locks1, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessRead)
	require.NoError(t, err)

	ctx2, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_, err = pool.AcquireFileAccess(ctx2, "session-2", []string{"file.go"}, FileAccessWrite)
	assert.Error(t, err)

	require.NoError(t, pool.ReleaseFileAccess(locks1))
}

func TestCrossSessionPoolVFS_ReleaseFileAccess(t *testing.T) {
	t.Run("release write lock", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		locks, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
		require.NoError(t, err)

		err = pool.ReleaseFileAccess(locks)
		assert.NoError(t, err)

		assert.False(t, pool.IsFileLocked("file.go"))
	})

	t.Run("release read lock", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		locks, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessRead)
		require.NoError(t, err)

		err = pool.ReleaseFileAccess(locks)
		assert.NoError(t, err)

		assert.False(t, pool.IsFileLocked("file.go"))
	})

	t.Run("release empty locks", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		err := pool.ReleaseFileAccess([]FileLock{})
		assert.NoError(t, err)
	})

	t.Run("release nil locks", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		err := pool.ReleaseFileAccess(nil)
		assert.NoError(t, err)
	})
}

func TestCrossSessionPoolVFS_GetFileOwners(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	_, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file1.go"}, FileAccessWrite)
	require.NoError(t, err)

	_, err = pool.AcquireFileAccess(ctx, "session-2", []string{"file2.go"}, FileAccessRead)
	require.NoError(t, err)

	owners := pool.GetFileOwners([]string{"file1.go", "file2.go", "file3.go"})

	assert.Equal(t, "session-1", owners["file1.go"])
	assert.Equal(t, "session-2", owners["file2.go"])
	_, exists := owners["file3.go"]
	assert.False(t, exists)
}

func TestCrossSessionPoolVFS_IsFileLocked(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	assert.False(t, pool.IsFileLocked("file.go"))

	locks, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
	require.NoError(t, err)

	assert.True(t, pool.IsFileLocked("file.go"))

	require.NoError(t, pool.ReleaseFileAccess(locks))

	assert.False(t, pool.IsFileLocked("file.go"))
}

func TestCrossSessionPoolVFS_GetFileLockInfo(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	_, exists := pool.GetFileLockInfo("file.go")
	assert.False(t, exists)

	locks, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
	require.NoError(t, err)

	info, exists := pool.GetFileLockInfo("file.go")
	assert.True(t, exists)
	assert.Equal(t, "file.go", info.FilePath)
	assert.Equal(t, "session-1", info.SessionID)
	assert.Equal(t, FileAccessWrite, info.Mode)

	require.NoError(t, pool.ReleaseFileAccess(locks))
}

func TestCrossSessionPoolVFS_LockWaiting(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	locks1, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
	require.NoError(t, err)

	var wg sync.WaitGroup
	var locks2 []FileLock
	var locks2Err error

	wg.Add(1)
	go func() {
		defer wg.Done()
		locks2, locks2Err = pool.AcquireFileAccess(ctx, "session-2", []string{"file.go"}, FileAccessWrite)
	}()

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, pool.ReleaseFileAccess(locks1))

	wg.Wait()

	assert.NoError(t, locks2Err)
	assert.Len(t, locks2, 1)
}

func TestCrossSessionPoolVFS_ContextCancellation(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	locks1, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
	require.NoError(t, err)
	defer pool.ReleaseFileAccess(locks1)

	ctx2, cancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err = pool.AcquireFileAccess(ctx2, "session-2", []string{"file.go"}, FileAccessWrite)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestCoordinatedFileOperation(t *testing.T) {
	t.Run("complete operation", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		op, err := pool.BeginCoordinatedOp(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
		require.NoError(t, err)

		assert.False(t, op.IsCompleted())
		assert.False(t, op.IsAborted())
		assert.Equal(t, "session-1", op.SessionID())
		assert.Len(t, op.Locks(), 1)

		err = op.Complete()
		assert.NoError(t, err)
		assert.True(t, op.IsCompleted())
		assert.False(t, pool.IsFileLocked("file.go"))
	})

	t.Run("abort operation", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		op, err := pool.BeginCoordinatedOp(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
		require.NoError(t, err)

		err = op.Abort()
		assert.NoError(t, err)
		assert.True(t, op.IsAborted())
		assert.False(t, pool.IsFileLocked("file.go"))
	})

	t.Run("double complete", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		op, err := pool.BeginCoordinatedOp(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
		require.NoError(t, err)

		require.NoError(t, op.Complete())
		err = op.Complete()
		assert.ErrorIs(t, err, ErrOperationCompleted)
	})

	t.Run("complete after abort", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		op, err := pool.BeginCoordinatedOp(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
		require.NoError(t, err)

		require.NoError(t, op.Abort())
		err = op.Complete()
		assert.ErrorIs(t, err, ErrOperationAborted)
	})

	t.Run("abort after complete", func(t *testing.T) {
		pool, _, cleanup := setupTestPoolVFS(t)
		defer cleanup()

		ctx := context.Background()
		op, err := pool.BeginCoordinatedOp(ctx, "session-1", []string{"file.go"}, FileAccessWrite)
		require.NoError(t, err)

		require.NoError(t, op.Complete())
		err = op.Abort()
		assert.ErrorIs(t, err, ErrOperationCompleted)
	})
}

func TestCrossSessionPoolVFS_ConcurrentAccess(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()
	numGoroutines := 50
	var wg sync.WaitGroup

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('A'+idx%26))
			filePath := "file" + string(rune('0'+idx%10)) + ".go"

			locks, err := pool.AcquireFileAccess(ctx, sessionID, []string{filePath}, FileAccessRead)
			if err == nil {
				time.Sleep(time.Millisecond)
				pool.ReleaseFileAccess(locks)
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < 10; i++ {
		filePath := "file" + string(rune('0'+i)) + ".go"
		assert.False(t, pool.IsFileLocked(filePath))
	}
}

func TestCrossSessionPoolVFS_Base(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	assert.NotNil(t, pool.Base())
}

func TestCrossSessionPoolVFS_Dispatcher(t *testing.T) {
	pool, dispatcher, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	assert.Equal(t, dispatcher, pool.Dispatcher())
}

func TestFileLock_Fields(t *testing.T) {
	now := time.Now()
	lock := FileLock{
		FilePath:   "test.go",
		SessionID:  "session-1",
		Mode:       FileAccessWrite,
		AcquiredAt: now,
	}

	assert.Equal(t, "test.go", lock.FilePath)
	assert.Equal(t, "session-1", lock.SessionID)
	assert.Equal(t, FileAccessWrite, lock.Mode)
	assert.Equal(t, now, lock.AcquiredAt)
}

func TestCrossSessionPoolVFS_PartialAcquireRollback(t *testing.T) {
	pool, _, cleanup := setupTestPoolVFS(t)
	defer cleanup()

	ctx := context.Background()

	locks1, err := pool.AcquireFileAccess(ctx, "session-1", []string{"file2.go"}, FileAccessWrite)
	require.NoError(t, err)
	defer pool.ReleaseFileAccess(locks1)

	ctx2, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_, err = pool.AcquireFileAccess(ctx2, "session-2", []string{"file1.go", "file2.go", "file3.go"}, FileAccessWrite)
	assert.Error(t, err)

	assert.False(t, pool.IsFileLocked("file1.go"))
	assert.True(t, pool.IsFileLocked("file2.go"))
	assert.False(t, pool.IsFileLocked("file3.go"))
}
