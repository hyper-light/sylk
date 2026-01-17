package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/versioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDispatcherVFS(t *testing.T) (*SignalDispatcherVFS, func()) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "signal_vfs_test_*")
	require.NoError(t, err)

	baseDispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   tempDir,
		SessionID: "test-session",
	})
	require.NoError(t, err)

	dispatcher := NewSignalDispatcherVFS(SignalDispatcherVFSConfig{
		BaseDispatcher: baseDispatcher,
	})

	cleanup := func() {
		baseDispatcher.Close()
		os.RemoveAll(tempDir)
	}

	return dispatcher, cleanup
}

func TestNewSignalDispatcherVFS(t *testing.T) {
	dispatcher, cleanup := setupTestDispatcherVFS(t)
	defer cleanup()

	assert.NotNil(t, dispatcher)
	assert.NotNil(t, dispatcher.base)
	assert.NotNil(t, dispatcher.subscriptions)
	assert.Equal(t, "test-session", dispatcher.SessionID())
}

func TestSignalDispatcherVFS_SubscribeFileChanges(t *testing.T) {
	dispatcher, cleanup := setupTestDispatcherVFS(t)
	defer cleanup()

	t.Run("successful subscription", func(t *testing.T) {
		err := dispatcher.SubscribeFileChanges("session-1", []string{"*.go", "src/**/*.ts"})
		assert.NoError(t, err)

		sub, ok := dispatcher.GetSubscription("session-1")
		assert.True(t, ok)
		assert.Equal(t, "session-1", sub.SessionID)
		assert.Equal(t, []string{"*.go", "src/**/*.ts"}, sub.Patterns)
		assert.Len(t, sub.globs, 2)
	})

	t.Run("empty session ID", func(t *testing.T) {
		err := dispatcher.SubscribeFileChanges("", []string{"*.go"})
		assert.ErrorIs(t, err, ErrEmptySessionID)
	})

	t.Run("no patterns", func(t *testing.T) {
		err := dispatcher.SubscribeFileChanges("session-2", []string{})
		assert.ErrorIs(t, err, ErrNoPatterns)
	})

	t.Run("nil patterns", func(t *testing.T) {
		err := dispatcher.SubscribeFileChanges("session-3", nil)
		assert.ErrorIs(t, err, ErrNoPatterns)
	})

	t.Run("invalid pattern", func(t *testing.T) {
		err := dispatcher.SubscribeFileChanges("session-4", []string{"["})
		assert.Error(t, err)
		var patternErr *PatternCompileError
		assert.ErrorAs(t, err, &patternErr)
		assert.Equal(t, "[", patternErr.Pattern)
	})

	t.Run("overwrite existing subscription", func(t *testing.T) {
		err := dispatcher.SubscribeFileChanges("session-5", []string{"*.go"})
		require.NoError(t, err)

		err = dispatcher.SubscribeFileChanges("session-5", []string{"*.ts", "*.js"})
		assert.NoError(t, err)

		sub, ok := dispatcher.GetSubscription("session-5")
		assert.True(t, ok)
		assert.Equal(t, []string{"*.ts", "*.js"}, sub.Patterns)
	})
}

func TestSignalDispatcherVFS_UnsubscribeFileChanges(t *testing.T) {
	dispatcher, cleanup := setupTestDispatcherVFS(t)
	defer cleanup()

	t.Run("successful unsubscription", func(t *testing.T) {
		err := dispatcher.SubscribeFileChanges("session-1", []string{"*.go"})
		require.NoError(t, err)

		err = dispatcher.UnsubscribeFileChanges("session-1")
		assert.NoError(t, err)

		_, ok := dispatcher.GetSubscription("session-1")
		assert.False(t, ok)
	})

	t.Run("unsubscribe non-existent session", func(t *testing.T) {
		err := dispatcher.UnsubscribeFileChanges("non-existent")
		assert.NoError(t, err)
	})

	t.Run("empty session ID", func(t *testing.T) {
		err := dispatcher.UnsubscribeFileChanges("")
		assert.ErrorIs(t, err, ErrEmptySessionID)
	})
}

func TestSignalDispatcherVFS_GetSubscribedSessions(t *testing.T) {
	dispatcher, cleanup := setupTestDispatcherVFS(t)
	defer cleanup()

	t.Run("no subscriptions", func(t *testing.T) {
		sessions := dispatcher.GetSubscribedSessions()
		assert.Empty(t, sessions)
	})

	t.Run("multiple subscriptions", func(t *testing.T) {
		require.NoError(t, dispatcher.SubscribeFileChanges("session-1", []string{"*.go"}))
		require.NoError(t, dispatcher.SubscribeFileChanges("session-2", []string{"*.ts"}))
		require.NoError(t, dispatcher.SubscribeFileChanges("session-3", []string{"*.js"}))

		sessions := dispatcher.GetSubscribedSessions()
		assert.Len(t, sessions, 3)
		assert.Contains(t, sessions, "session-1")
		assert.Contains(t, sessions, "session-2")
		assert.Contains(t, sessions, "session-3")
	})
}

func TestSignalDispatcherVFS_BroadcastFileChange(t *testing.T) {
	t.Run("nil signal", func(t *testing.T) {
		dispatcher, cleanup := setupTestDispatcherVFS(t)
		defer cleanup()

		err := dispatcher.BroadcastFileChange(nil)
		assert.ErrorIs(t, err, ErrNilSignal)
	})

	t.Run("empty file path", func(t *testing.T) {
		dispatcher, cleanup := setupTestDispatcherVFS(t)
		defer cleanup()

		signal := &FileChangeSignal{
			FilePath:   "",
			ChangeType: FileChangeModified,
		}
		err := dispatcher.BroadcastFileChange(signal)
		assert.ErrorIs(t, err, ErrEmptyFilePath)
	})

	t.Run("broadcast to interested sessions", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "signal_vfs_broadcast_*")
		require.NoError(t, err)
		defer os.RemoveAll(tempDir)

		senderBase, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   tempDir,
			SessionID: "sender",
		})
		require.NoError(t, err)
		defer senderBase.Close()

		sender := NewSignalDispatcherVFS(SignalDispatcherVFSConfig{
			BaseDispatcher: senderBase,
		})

		receiverBase, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   tempDir,
			SessionID: "receiver",
		})
		require.NoError(t, err)
		defer receiverBase.Close()

		err = sender.SubscribeFileChanges("receiver", []string{"*.go"})
		require.NoError(t, err)

		signal := &FileChangeSignal{
			FilePath:   "main.go",
			ChangeType: FileChangeModified,
			OldVersion: versioning.VersionID{1, 2, 3},
			NewVersion: versioning.VersionID{4, 5, 6},
			ChangedBy:  "sender",
		}

		err = sender.BroadcastFileChange(signal)
		assert.NoError(t, err)

		files, err := filepath.Glob(filepath.Join(tempDir, "receiver", "*.signal"))
		require.NoError(t, err)
		assert.Len(t, files, 1)
	})

	t.Run("no broadcast to non-matching sessions", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "signal_vfs_nomatch_*")
		require.NoError(t, err)
		defer os.RemoveAll(tempDir)

		senderBase, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   tempDir,
			SessionID: "sender",
		})
		require.NoError(t, err)
		defer senderBase.Close()

		sender := NewSignalDispatcherVFS(SignalDispatcherVFSConfig{
			BaseDispatcher: senderBase,
		})

		err = sender.SubscribeFileChanges("receiver", []string{"*.ts"})
		require.NoError(t, err)

		signal := &FileChangeSignal{
			FilePath:   "main.go",
			ChangeType: FileChangeModified,
		}

		err = sender.BroadcastFileChange(signal)
		assert.NoError(t, err)

		receiverDir := filepath.Join(tempDir, "receiver")
		os.MkdirAll(receiverDir, 0755)

		files, err := filepath.Glob(filepath.Join(receiverDir, "*.signal"))
		require.NoError(t, err)
		assert.Len(t, files, 0)
	})
}

func TestSignalDispatcherVFS_BroadcastFileLocked(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "signal_vfs_lock_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	senderBase, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   tempDir,
		SessionID: "sender",
	})
	require.NoError(t, err)
	defer senderBase.Close()

	dispatcher := NewSignalDispatcherVFS(SignalDispatcherVFSConfig{
		BaseDispatcher: senderBase,
	})

	err = dispatcher.SubscribeFileChanges("receiver", []string{"handler.go"})
	require.NoError(t, err)

	t.Run("broadcast lock signal", func(t *testing.T) {
		err := dispatcher.BroadcastFileLocked("handler.go", "sender")
		assert.NoError(t, err)
	})

	t.Run("empty file path", func(t *testing.T) {
		err := dispatcher.BroadcastFileLocked("", "sender")
		assert.ErrorIs(t, err, ErrEmptyFilePath)
	})
}

func TestSignalDispatcherVFS_BroadcastFileUnlocked(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "signal_vfs_unlock_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	senderBase, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   tempDir,
		SessionID: "sender",
	})
	require.NoError(t, err)
	defer senderBase.Close()

	dispatcher := NewSignalDispatcherVFS(SignalDispatcherVFSConfig{
		BaseDispatcher: senderBase,
	})

	err = dispatcher.SubscribeFileChanges("receiver", []string{"*.go"})
	require.NoError(t, err)

	t.Run("broadcast unlock signal", func(t *testing.T) {
		err := dispatcher.BroadcastFileUnlocked("handler.go", "sender")
		assert.NoError(t, err)
	})
}

func TestSignalDispatcherVFS_BroadcastMergeConflict(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "signal_vfs_conflict_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	senderBase, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   tempDir,
		SessionID: "sender",
	})
	require.NoError(t, err)
	defer senderBase.Close()

	dispatcher := NewSignalDispatcherVFS(SignalDispatcherVFSConfig{
		BaseDispatcher: senderBase,
	})

	err = dispatcher.SubscribeFileChanges("receiver", []string{"*.go"})
	require.NoError(t, err)

	t.Run("broadcast merge conflict", func(t *testing.T) {
		baseVer := versioning.VersionID{1, 1, 1}
		ourVer := versioning.VersionID{2, 2, 2}
		theirVer := versioning.VersionID{3, 3, 3}

		err := dispatcher.BroadcastMergeConflict("main.go", baseVer, ourVer, theirVer, "other-session")
		assert.NoError(t, err)
	})
}

func TestSignalDispatcherVFS_PatternMatching(t *testing.T) {
	dispatcher, cleanup := setupTestDispatcherVFS(t)
	defer cleanup()

	testCases := []struct {
		name     string
		patterns []string
		filePath string
		matches  bool
	}{
		{"simple glob", []string{"*.go"}, "main.go", true},
		{"simple glob no match", []string{"*.go"}, "main.ts", false},
		{"double star", []string{"src/**/*.go"}, "src/pkg/main.go", true},
		{"double star no match", []string{"src/**/*.go"}, "test/main.go", false},
		{"multiple patterns match first", []string{"*.go", "*.ts"}, "main.go", true},
		{"multiple patterns match second", []string{"*.go", "*.ts"}, "main.ts", true},
		{"multiple patterns no match", []string{"*.go", "*.ts"}, "main.py", false},
		{"exact match", []string{"handler.go"}, "handler.go", true},
		{"exact match no match", []string{"handler.go"}, "main.go", false},
		{"basename match", []string{"*.go"}, "src/pkg/handler.go", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sessionID := "test-" + tc.name

			err := dispatcher.SubscribeFileChanges(sessionID, tc.patterns)
			require.NoError(t, err)

			sessions := dispatcher.findInterestedSessions(tc.filePath)
			if tc.matches {
				assert.Contains(t, sessions, sessionID)
			} else {
				assert.NotContains(t, sessions, sessionID)
			}

			dispatcher.UnsubscribeFileChanges(sessionID)
		})
	}
}

func TestSignalDispatcherVFS_SignalTypeMapping(t *testing.T) {
	dispatcher, cleanup := setupTestDispatcherVFS(t)
	defer cleanup()

	testCases := []struct {
		changeType   FileChangeType
		expectedType SignalType
	}{
		{FileChangeCreated, SignalFileCreated},
		{FileChangeModified, SignalFileModified},
		{FileChangeDeleted, SignalFileDeleted},
		{"unknown", SignalFileModified},
	}

	for _, tc := range testCases {
		t.Run(string(tc.changeType), func(t *testing.T) {
			signalType := dispatcher.signalTypeFromChangeType(tc.changeType)
			assert.Equal(t, tc.expectedType, signalType)
		})
	}
}

func TestSignalDispatcherVFS_ConcurrentAccess(t *testing.T) {
	dispatcher, cleanup := setupTestDispatcherVFS(t)
	defer cleanup()

	var wg sync.WaitGroup
	numGoroutines := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('A'+idx%26)) + string(rune('0'+idx%10))
			_ = dispatcher.SubscribeFileChanges(sessionID, []string{"*.go"})
		}(i)
	}
	wg.Wait()

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_ = dispatcher.GetSubscribedSessions()
			_ = dispatcher.findInterestedSessions("main.go")
		}()
	}
	wg.Wait()

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('A'+idx%26)) + string(rune('0'+idx%10))
			_ = dispatcher.UnsubscribeFileChanges(sessionID)
		}(i)
	}
	wg.Wait()

	sessions := dispatcher.GetSubscribedSessions()
	assert.Empty(t, sessions)
}

func TestSignalDispatcherVFS_Base(t *testing.T) {
	dispatcher, cleanup := setupTestDispatcherVFS(t)
	defer cleanup()

	base := dispatcher.Base()
	assert.NotNil(t, base)
	assert.Equal(t, "test-session", base.SessionID())
}

func TestFileChangeSignal_Fields(t *testing.T) {
	signal := &FileChangeSignal{
		CrossSessionSignal: CrossSessionSignal{
			Type:        SignalFileModified,
			FromSession: "session-a",
			ToSession:   "session-b",
			Timestamp:   time.Now(),
			Payload:     "test",
		},
		FilePath:   "src/handler.go",
		OldVersion: versioning.ComputeVersionID([]byte("old"), []byte("meta")),
		NewVersion: versioning.ComputeVersionID([]byte("new"), []byte("meta")),
		ChangeType: FileChangeModified,
		ChangedBy:  "session-a",
		PipelineID: "pipeline-123",
	}

	assert.Equal(t, SignalFileModified, signal.Type)
	assert.Equal(t, "src/handler.go", signal.FilePath)
	assert.Equal(t, FileChangeModified, signal.ChangeType)
	assert.Equal(t, "session-a", signal.ChangedBy)
	assert.Equal(t, "pipeline-123", signal.PipelineID)
	assert.False(t, signal.OldVersion.IsZero())
	assert.False(t, signal.NewVersion.IsZero())
}

func TestPatternCompileError(t *testing.T) {
	err := &PatternCompileError{
		Pattern: "[invalid",
		Err:     assert.AnError,
	}

	assert.Contains(t, err.Error(), "[invalid")
	assert.Equal(t, assert.AnError, err.Unwrap())
}

func TestFileSubscription(t *testing.T) {
	sub := &FileSubscription{
		SessionID: "test-session",
		Patterns:  []string{"*.go", "*.ts"},
	}

	assert.Equal(t, "test-session", sub.SessionID)
	assert.Len(t, sub.Patterns, 2)
}
