package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	_ "github.com/mattn/go-sqlite3"
)

func newTestWALManager(t *testing.T) (*MultiSessionWALManager, string) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "wal-manager-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := WALManagerConfig{
		BaseDir:     filepath.Join(tmpDir, "sessions"),
		SharedDBDir: filepath.Join(tmpDir, "shared"),
		WALConfig:   concurrency.DefaultWALConfig(),
	}

	manager, err := NewMultiSessionWALManager(cfg)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create WAL manager: %v", err)
	}

	return manager, tmpDir
}

func TestNewMultiSessionWALManager(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	if manager == nil {
		t.Fatal("expected non-nil manager")
	}
	if manager.db == nil {
		t.Error("expected non-nil db")
	}
	if manager.sessionWALs == nil {
		t.Error("expected non-nil sessionWALs map")
	}
}

func TestMultiSessionWALManager_GetOrCreateWAL(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	wal, err := manager.GetOrCreateWAL("session-1")
	if err != nil {
		t.Fatalf("GetOrCreateWAL failed: %v", err)
	}
	if wal == nil {
		t.Fatal("expected non-nil WAL")
	}

	walDir := filepath.Join(tmpDir, "sessions", "session-1", "wal")
	if _, err := os.Stat(walDir); os.IsNotExist(err) {
		t.Error("expected WAL directory to exist")
	}
}

func TestMultiSessionWALManager_GetOrCreateWAL_Reuse(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	wal1, _ := manager.GetOrCreateWAL("session-1")
	wal2, _ := manager.GetOrCreateWAL("session-1")

	if wal1 != wal2 {
		t.Error("expected same WAL instance on second call")
	}
}

func TestMultiSessionWALManager_GetWAL_NotFound(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	_, err := manager.GetWAL("nonexistent")
	if err != ErrSessionWALMissing {
		t.Errorf("expected ErrSessionWALMissing, got %v", err)
	}
}

func TestMultiSessionWALManager_GetWAL_Exists(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.GetOrCreateWAL("session-1")

	wal, err := manager.GetWAL("session-1")
	if err != nil {
		t.Fatalf("GetWAL failed: %v", err)
	}
	if wal == nil {
		t.Error("expected non-nil WAL")
	}
}

func TestMultiSessionWALManager_MultipleSessions(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	for i := 0; i < 3; i++ {
		sessionID := "session-" + string(rune('A'+i))
		_, err := manager.GetOrCreateWAL(sessionID)
		if err != nil {
			t.Fatalf("GetOrCreateWAL failed for %s: %v", sessionID, err)
		}
	}

	if manager.ActiveSessionCount() != 3 {
		t.Errorf("expected 3 active sessions, got %d", manager.ActiveSessionCount())
	}
}

func TestMultiSessionWALManager_GetSessionInfo(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.GetOrCreateWAL("session-1")

	info, err := manager.GetSessionInfo("session-1")
	if err != nil {
		t.Fatalf("GetSessionInfo failed: %v", err)
	}
	if info.SessionID != "session-1" {
		t.Errorf("expected session-1, got %s", info.SessionID)
	}
	if info.WALDir == "" {
		t.Error("expected non-empty WAL dir")
	}
}

func TestMultiSessionWALManager_GetSessionInfo_NotFound(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	_, err := manager.GetSessionInfo("nonexistent")
	if err != ErrSessionWALMissing {
		t.Errorf("expected ErrSessionWALMissing, got %v", err)
	}
}

func TestMultiSessionWALManager_UpdateActivity(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.GetOrCreateWAL("session-1")

	infoBefore, _ := manager.GetSessionInfo("session-1")
	time.Sleep(10 * time.Millisecond)

	err := manager.UpdateActivity("session-1")
	if err != nil {
		t.Fatalf("UpdateActivity failed: %v", err)
	}

	infoAfter, _ := manager.GetSessionInfo("session-1")
	if !infoAfter.LastActive.After(infoBefore.LastActive) {
		t.Error("expected last active time to be updated")
	}
}

func TestMultiSessionWALManager_ListSessions(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	ctx := context.Background()

	manager.GetOrCreateWAL("session-1")
	manager.GetOrCreateWAL("session-2")

	sessions, err := manager.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestMultiSessionWALManager_RemoveSession(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.GetOrCreateWAL("session-1")

	err := manager.RemoveSession("session-1")
	if err != nil {
		t.Fatalf("RemoveSession failed: %v", err)
	}

	if manager.ActiveSessionCount() != 0 {
		t.Errorf("expected 0 active sessions, got %d", manager.ActiveSessionCount())
	}

	_, err = manager.GetSessionInfo("session-1")
	if err != ErrSessionWALMissing {
		t.Error("expected session to be removed from database")
	}
}

func TestMultiSessionWALManager_RemoveSession_NotFound(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	err := manager.RemoveSession("nonexistent")
	if err != nil {
		t.Errorf("expected no error for removing nonexistent session, got %v", err)
	}
}

func TestMultiSessionWALManager_RecoverAllSessions(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)

	manager.GetOrCreateWAL("session-1")
	manager.GetOrCreateWAL("session-2")
	manager.Close()

	manager2, err := NewMultiSessionWALManager(WALManagerConfig{
		BaseDir:     filepath.Join(tmpDir, "sessions"),
		SharedDBDir: filepath.Join(tmpDir, "shared"),
		WALConfig:   concurrency.DefaultWALConfig(),
	})
	if err != nil {
		t.Fatalf("failed to create second manager: %v", err)
	}
	defer manager2.Close()
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	err = manager2.RecoverAllSessions(ctx)
	if err != nil {
		t.Fatalf("RecoverAllSessions failed: %v", err)
	}

	if manager2.ActiveSessionCount() != 2 {
		t.Errorf("expected 2 recovered sessions, got %d", manager2.ActiveSessionCount())
	}
}

func TestMultiSessionWALManager_Close(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.GetOrCreateWAL("session-1")

	err := manager.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err = manager.GetOrCreateWAL("session-2")
	if err != ErrWALManagerClosed {
		t.Errorf("expected ErrWALManagerClosed, got %v", err)
	}
}

func TestMultiSessionWALManager_DoubleClose(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.Close()
	err := manager.Close()

	if err != ErrWALManagerClosed {
		t.Errorf("expected ErrWALManagerClosed on double close, got %v", err)
	}
}

func TestMultiSessionWALManager_ClosedOperations(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.Close()

	_, err := manager.GetWAL("session-1")
	if err != ErrWALManagerClosed {
		t.Errorf("GetWAL: expected ErrWALManagerClosed, got %v", err)
	}

	ctx := context.Background()
	err = manager.RecoverAllSessions(ctx)
	if err != ErrWALManagerClosed {
		t.Errorf("RecoverAllSessions: expected ErrWALManagerClosed, got %v", err)
	}

	err = manager.RemoveSession("session-1")
	if err != ErrWALManagerClosed {
		t.Errorf("RemoveSession: expected ErrWALManagerClosed, got %v", err)
	}
}

func TestMultiSessionWALManager_WALWrite(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	wal, err := manager.GetOrCreateWAL("session-1")
	if err != nil {
		t.Fatalf("GetOrCreateWAL failed: %v", err)
	}

	entry := &concurrency.WALEntry{
		Type:    concurrency.EntryStateChange,
		Payload: []byte("test data"),
	}

	seq, err := wal.Append(entry)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if seq == 0 {
		t.Error("expected non-zero sequence")
	}
}

// Race condition tests for W4H.7 TOCTOU fix

func TestMultiSessionWALManager_Race_UpdateActivityNormal(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.GetOrCreateWAL("session-1")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.UpdateActivity("session-1")
		}()
	}
	wg.Wait()
}

func TestMultiSessionWALManager_Race_UpdateActivityAfterClose(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.GetOrCreateWAL("session-1")
	manager.Close()

	err := manager.UpdateActivity("session-1")
	if err != ErrWALManagerClosed {
		t.Errorf("expected ErrWALManagerClosed, got %v", err)
	}
}

func TestMultiSessionWALManager_Race_ConcurrentCloseDuringUpdate(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.GetOrCreateWAL("session-1")

	var wg sync.WaitGroup
	started := make(chan struct{})

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			err := manager.UpdateActivity("session-1")
			if err != nil && err != ErrWALManagerClosed {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-started
		manager.Close()
	}()

	close(started)
	wg.Wait()
}

func TestMultiSessionWALManager_Race_GetSessionInfoNormal(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.GetOrCreateWAL("session-1")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.GetSessionInfo("session-1")
		}()
	}
	wg.Wait()
}

func TestMultiSessionWALManager_Race_GetSessionInfoAfterClose(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.GetOrCreateWAL("session-1")
	manager.Close()

	_, err := manager.GetSessionInfo("session-1")
	if err != ErrWALManagerClosed {
		t.Errorf("expected ErrWALManagerClosed, got %v", err)
	}
}

func TestMultiSessionWALManager_Race_ConcurrentCloseDuringGetSessionInfo(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.GetOrCreateWAL("session-1")

	var wg sync.WaitGroup
	started := make(chan struct{})

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			_, err := manager.GetSessionInfo("session-1")
			if err != nil && err != ErrWALManagerClosed && err != ErrSessionWALMissing {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-started
		manager.Close()
	}()

	close(started)
	wg.Wait()
}

func TestMultiSessionWALManager_Race_ListSessionsNormal(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.GetOrCreateWAL("session-1")
	manager.GetOrCreateWAL("session-2")

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.ListSessions(ctx)
		}()
	}
	wg.Wait()
}

func TestMultiSessionWALManager_Race_ListSessionsAfterClose(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.GetOrCreateWAL("session-1")
	manager.Close()

	ctx := context.Background()
	_, err := manager.ListSessions(ctx)
	if err != ErrWALManagerClosed {
		t.Errorf("expected ErrWALManagerClosed, got %v", err)
	}
}

func TestMultiSessionWALManager_Race_ConcurrentCloseDuringListSessions(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.GetOrCreateWAL("session-1")
	manager.GetOrCreateWAL("session-2")

	var wg sync.WaitGroup
	started := make(chan struct{})
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			_, err := manager.ListSessions(ctx)
			if err != nil && err != ErrWALManagerClosed {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-started
		manager.Close()
	}()

	close(started)
	wg.Wait()
}

func TestMultiSessionWALManager_Race_RapidOpenCloseCycles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wal-manager-race-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for cycle := 0; cycle < 5; cycle++ {
		cfg := WALManagerConfig{
			BaseDir:     filepath.Join(tmpDir, "sessions"),
			SharedDBDir: filepath.Join(tmpDir, "shared"),
			WALConfig:   concurrency.DefaultWALConfig(),
		}

		manager, err := NewMultiSessionWALManager(cfg)
		if err != nil {
			t.Fatalf("cycle %d: failed to create manager: %v", cycle, err)
		}

		var wg sync.WaitGroup
		started := make(chan struct{})

		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-started
				sessionID := "session-" + string(rune('A'+idx))
				manager.GetOrCreateWAL(sessionID)
				manager.UpdateActivity(sessionID)
				manager.GetSessionInfo(sessionID)
			}(i)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			time.Sleep(time.Millisecond)
			manager.Close()
		}()

		close(started)
		wg.Wait()
	}
}

func TestMultiSessionWALManager_Race_MixedOperations(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	for i := 0; i < 5; i++ {
		sessionID := "session-" + string(rune('A'+i))
		manager.GetOrCreateWAL(sessionID)
	}

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('A'+(idx%5)))
			switch idx % 4 {
			case 0:
				manager.UpdateActivity(sessionID)
			case 1:
				manager.GetSessionInfo(sessionID)
			case 2:
				manager.ListSessions(ctx)
			case 3:
				manager.GetWAL(sessionID)
			}
		}(i)
	}
	wg.Wait()
}

func TestMultiSessionWALManager_Race_ConcurrentCloseMultipleTimes(t *testing.T) {
	manager, tmpDir := newTestWALManager(t)
	defer os.RemoveAll(tmpDir)

	manager.GetOrCreateWAL("session-1")

	var wg sync.WaitGroup
	started := make(chan struct{})
	closedCount := 0
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			err := manager.Close()
			if err == nil {
				mu.Lock()
				closedCount++
				mu.Unlock()
			} else if err != ErrWALManagerClosed {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	close(started)
	wg.Wait()

	if closedCount != 1 {
		t.Errorf("expected exactly 1 successful close, got %d", closedCount)
	}
}
