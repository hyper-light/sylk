package integrity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/backup"
	"github.com/adalundhe/sylk/core/database"
	"github.com/adalundhe/sylk/core/storage"
)

func TestMonitorBasic(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, err := dbMgr.Open("test", database.DefaultPoolConfig())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	backupMgr := backup.NewManager(dirs, backup.DefaultConfig())
	monitor := NewMonitor(backupMgr, DefaultConfig())

	monitor.Register("system", pool)

	ctx := context.Background()
	if err := monitor.Check(ctx, "system"); err != nil {
		t.Errorf("Check failed: %v", err)
	}

	lastCheck, ok := monitor.LastCheckTime("system")
	if !ok {
		t.Error("LastCheckTime should return true")
	}
	if lastCheck.IsZero() {
		t.Error("LastCheckTime should not be zero")
	}
}

func TestMonitorUnknownScope(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	backupMgr := backup.NewManager(dirs, backup.DefaultConfig())
	monitor := NewMonitor(backupMgr, DefaultConfig())

	ctx := context.Background()
	err := monitor.Check(ctx, "nonexistent")
	if err == nil {
		t.Error("Check should fail for unknown scope")
	}
}

func TestMonitorCorruptionCallback(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("callback", database.DefaultPoolConfig())

	backupMgr := backup.NewManager(dirs, backup.DefaultConfig())

	config := DefaultConfig()
	config.AutoRestore = false
	monitor := NewMonitor(backupMgr, config)
	monitor.Register("system", pool)

	callbackCalled := false
	monitor.OnCorruption(func(scope string, err error) {
		callbackCalled = true
	})

	ctx := context.Background()
	_ = monitor.Check(ctx, "system")

	if callbackCalled {
		t.Error("Callback should not be called for healthy database")
	}
}

func TestMonitorIsRebuildable(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	backupMgr := backup.NewManager(dirs, backup.DefaultConfig())
	monitor := NewMonitor(backupMgr, DefaultConfig())

	if !monitor.IsRebuildable("index") {
		t.Error("index should be rebuildable")
	}
	if !monitor.IsRebuildable("knowledge") {
		t.Error("knowledge should be rebuildable")
	}
	if monitor.IsRebuildable("system") {
		t.Error("system should not be rebuildable")
	}
}

func TestMonitorStartStop(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("startstop", database.DefaultPoolConfig())

	backupMgr := backup.NewManager(dirs, backup.DefaultConfig())

	config := DefaultConfig()
	config.CheckInterval = 50 * time.Millisecond
	config.StartupCheck = true
	monitor := NewMonitor(backupMgr, config)
	monitor.Register("system", pool)

	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	monitor.Stop()

	lastCheck, _ := monitor.LastCheckTime("system")
	if lastCheck.IsZero() {
		t.Error("Should have performed at least one check")
	}
}

func TestMonitorRegisterUnregister(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("regtest", database.DefaultPoolConfig())

	backupMgr := backup.NewManager(dirs, backup.DefaultConfig())
	monitor := NewMonitor(backupMgr, DefaultConfig())

	monitor.Register("test", pool)

	ctx := context.Background()
	err := monitor.Check(ctx, "test")
	if err != nil {
		t.Errorf("Check should succeed: %v", err)
	}

	monitor.Unregister("test")

	err = monitor.Check(ctx, "test")
	if err == nil {
		t.Error("Check should fail after unregister")
	}
}

func TestIsSuspiciousError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{errors.New("normal error"), false},
		{errors.New("database disk image is malformed"), true},
		{errors.New("SQLITE_CORRUPT"), true},
		{errors.New("disk I/O error"), true},
		{errors.New("file is corrupted"), true},
		{errors.New("SQLITE_NOTADB: not a database"), true},
	}

	for _, tt := range tests {
		result := isSuspiciousError(tt.err)
		if result != tt.expected {
			errMsg := "nil"
			if tt.err != nil {
				errMsg = tt.err.Error()
			}
			t.Errorf("isSuspiciousError(%q): got %v, want %v", errMsg, result, tt.expected)
		}
	}
}

func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		s, substr string
		expected  bool
	}{
		{"hello world", "world", true},
		{"Hello World", "WORLD", true},
		{"CORRUPT", "corrupt", true},
		{"hello", "xyz", false},
		{"", "a", false},
		{"a", "", true},
	}

	for _, tt := range tests {
		result := containsIgnoreCase(tt.s, tt.substr)
		if result != tt.expected {
			t.Errorf("containsIgnoreCase(%q, %q): got %v, want %v", tt.s, tt.substr, result, tt.expected)
		}
	}
}

func TestRebuildRequiredError(t *testing.T) {
	err := &RebuildRequired{Scope: "index"}
	if err.Error() != "database index requires rebuild" {
		t.Errorf("Error message: %s", err.Error())
	}
}

func TestNoBackupAvailableError(t *testing.T) {
	err := &NoBackupAvailable{Scope: "system"}
	if err.Error() != "no backup available for system" {
		t.Errorf("Error message: %s", err.Error())
	}
}
