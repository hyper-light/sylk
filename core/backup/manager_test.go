package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/database"
	"github.com/adalundhe/sylk/core/storage"
)

func TestBackupManager(t *testing.T) {
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

	ctx := context.Background()
	_, _ = pool.Exec(ctx, "CREATE TABLE test (id INTEGER PRIMARY KEY, value TEXT)")
	_, _ = pool.Exec(ctx, "INSERT INTO test (value) VALUES (?)", "testdata")

	backupMgr := NewManager(dirs, DefaultConfig())
	backupPath, err := backupMgr.Backup(ctx, pool, "system")
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	if _, err := os.Stat(backupPath); err != nil {
		t.Errorf("Backup file not created: %v", err)
	}
}

func TestBackupListAndLatest(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("list", database.DefaultPoolConfig())
	ctx := context.Background()

	backupMgr := NewManager(dirs, DefaultConfig())

	_, _ = backupMgr.Backup(ctx, pool, "system")
	time.Sleep(1100 * time.Millisecond)
	_, _ = backupMgr.Backup(ctx, pool, "system")

	backups, err := backupMgr.ListBackups("system")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}

	if len(backups) < 1 {
		t.Errorf("Backup count: got %d, want at least 1", len(backups))
	}

	latest, err := backupMgr.LatestBackup("system")
	if err != nil {
		t.Fatalf("LatestBackup failed: %v", err)
	}

	if latest == nil {
		t.Error("Latest backup should not be nil")
	}
}

func TestBackupRetention(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("retention", database.DefaultPoolConfig())
	ctx := context.Background()

	config := DefaultConfig()
	config.RetentionCount = 3
	backupMgr := NewManager(dirs, config)

	for i := 0; i < 5; i++ {
		_, _ = backupMgr.Backup(ctx, pool, "system")
		time.Sleep(1100 * time.Millisecond)
	}

	backups, _ := backupMgr.ListBackups("system")
	if len(backups) > config.RetentionCount {
		t.Errorf("Retention: got %d backups, want at most %d", len(backups), config.RetentionCount)
	}
}

func TestBackupDelete(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("delete", database.DefaultPoolConfig())
	ctx := context.Background()

	backupMgr := NewManager(dirs, DefaultConfig())
	backupPath, _ := backupMgr.Backup(ctx, pool, "system")

	if err := backupMgr.DeleteBackup(backupPath); err != nil {
		t.Fatalf("DeleteBackup failed: %v", err)
	}

	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Error("Backup should be deleted")
	}
}

func TestBackupDeleteOutsidePath(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	backupMgr := NewManager(dirs, DefaultConfig())

	err := backupMgr.DeleteBackup("/etc/passwd")
	if err == nil {
		t.Error("Should reject paths outside backup directory")
	}
}

func TestScheduler(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("sched", database.DefaultPoolConfig())

	backupMgr := NewManager(dirs, DefaultConfig())
	scheduler := NewScheduler(backupMgr, 100*time.Millisecond)

	scheduler.Register("system", pool)
	scheduler.Start()

	time.Sleep(250 * time.Millisecond)
	scheduler.Stop()

	backups, _ := backupMgr.ListBackups("system")
	if len(backups) < 1 {
		t.Error("Scheduler should have created at least one backup")
	}
}

func TestSchedulerTriggerNow(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("trigger", database.DefaultPoolConfig())

	backupMgr := NewManager(dirs, DefaultConfig())
	scheduler := NewScheduler(backupMgr, time.Hour)

	scheduler.Register("system", pool)
	scheduler.TriggerNow()

	time.Sleep(100 * time.Millisecond)

	backups, _ := backupMgr.ListBackups("system")
	if len(backups) != 1 {
		t.Errorf("TriggerNow: got %d backups, want 1", len(backups))
	}
}

func TestBackupInfo(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	dbMgr := database.NewManager(dirs)
	defer dbMgr.CloseAll()

	pool, _ := dbMgr.Open("info", database.DefaultPoolConfig())
	ctx := context.Background()

	backupMgr := NewManager(dirs, DefaultConfig())
	backupPath, _ := backupMgr.Backup(ctx, pool, "system")

	backups, _ := backupMgr.ListBackups("system")
	if len(backups) != 1 {
		t.Fatal("Expected one backup")
	}

	info := backups[0]
	if info.Path != backupPath {
		t.Errorf("Path: got %s, want %s", info.Path, backupPath)
	}
	if info.Size == 0 {
		t.Error("Size should not be zero")
	}
	if info.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if !filepath.IsAbs(info.Path) {
		t.Error("Path should be absolute")
	}
}
