package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/storage"
)

func TestManagerOpenClose(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	mgr := NewManager(dirs)
	defer mgr.CloseAll()

	pool, err := mgr.Open("test", DefaultPoolConfig())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if pool.DB() == nil {
		t.Error("DB should not be nil")
	}

	pool2, ok := mgr.Get("test")
	if !ok || pool2 != pool {
		t.Error("Get should return same pool")
	}

	_, ok = mgr.Get("nonexistent")
	if ok {
		t.Error("Get should return false for nonexistent pool")
	}

	if err := mgr.Close("test"); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, ok = mgr.Get("test")
	if ok {
		t.Error("Pool should be removed after close")
	}
}

func TestPoolBasicOperations(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	mgr := NewManager(dirs)
	defer mgr.CloseAll()

	pool, err := mgr.Open("ops", DefaultPoolConfig())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	ctx := context.Background()

	_, err = pool.Exec(ctx, "CREATE TABLE test (id INTEGER PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = pool.Exec(ctx, "INSERT INTO test (value) VALUES (?)", "hello")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var value string
	err = pool.QueryRow(ctx, "SELECT value FROM test WHERE id = ?", 1).Scan(&value)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if value != "hello" {
		t.Errorf("value: got %s, want hello", value)
	}

	rows, err := pool.Query(ctx, "SELECT * FROM test")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Errorf("row count: got %d, want 1", count)
	}
}

func TestPoolTransaction(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	mgr := NewManager(dirs)
	defer mgr.CloseAll()

	pool, err := mgr.Open("tx", DefaultPoolConfig())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	ctx := context.Background()
	_, _ = pool.Exec(ctx, "CREATE TABLE tx_test (id INTEGER PRIMARY KEY, value INTEGER)")

	err = pool.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO tx_test (value) VALUES (?)", 100)
		return err
	})
	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}

	var value int
	err = pool.QueryRow(ctx, "SELECT value FROM tx_test WHERE id = 1").Scan(&value)
	if err != nil || value != 100 {
		t.Errorf("Transaction not committed: value=%d, err=%v", value, err)
	}

	err = pool.Transaction(ctx, func(tx *sql.Tx) error {
		_, _ = tx.Exec("INSERT INTO tx_test (value) VALUES (?)", 200)
		return sql.ErrNoRows
	})
	if err == nil {
		t.Error("Transaction should have failed")
	}

	var count int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM tx_test").Scan(&count)
	if count != 1 {
		t.Errorf("Rollback failed: count=%d, want 1", count)
	}
}

func TestPoolVersion(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	mgr := NewManager(dirs)
	defer mgr.CloseAll()

	pool, err := mgr.Open("version", DefaultPoolConfig())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	version, err := pool.Version()
	if err != nil {
		t.Fatalf("Version failed: %v", err)
	}
	if version != 0 {
		t.Errorf("Initial version: got %d, want 0", version)
	}

	if err := pool.SetVersion(5); err != nil {
		t.Fatalf("SetVersion failed: %v", err)
	}

	version, _ = pool.Version()
	if version != 5 {
		t.Errorf("Version: got %d, want 5", version)
	}
}

func TestPoolIntegrityCheck(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	mgr := NewManager(dirs)
	defer mgr.CloseAll()

	pool, err := mgr.Open("integrity", DefaultPoolConfig())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if err := pool.IntegrityCheck(); err != nil {
		t.Errorf("IntegrityCheck failed: %v", err)
	}
}

func TestMigrator(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	mgr := NewManager(dirs)
	defer mgr.CloseAll()

	pool, err := mgr.Open("migrate", DefaultPoolConfig())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	migrations := []Migration{
		{
			Version:     1,
			Description: "create users table",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
				return err
			},
			Down: func(tx *sql.Tx) error {
				_, err := tx.Exec("DROP TABLE users")
				return err
			},
		},
		{
			Version:     2,
			Description: "add email column",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("ALTER TABLE users ADD COLUMN email TEXT")
				return err
			},
			Down: func(tx *sql.Tx) error {
				return nil
			},
		},
	}

	migrator := NewMigrator(pool, migrations)
	ctx := context.Background()

	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	version, _ := migrator.CurrentVersion()
	if version != 2 {
		t.Errorf("Version: got %d, want 2", version)
	}

	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'").Scan(&count)
	if err != nil || count != 1 {
		t.Error("users table should exist")
	}
}

func TestMigratorPending(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Data: tmpDir,
	}

	mgr := NewManager(dirs)
	defer mgr.CloseAll()

	pool, _ := mgr.Open("pending", DefaultPoolConfig())
	_ = pool.SetVersion(1)

	migrations := []Migration{
		{Version: 1, Description: "first"},
		{Version: 2, Description: "second"},
		{Version: 3, Description: "third", Destructive: true},
	}

	migrator := NewMigrator(pool, migrations)

	pending, _ := migrator.PendingMigrations()
	if len(pending) != 2 {
		t.Errorf("Pending: got %d, want 2", len(pending))
	}

	has, _ := migrator.HasPendingMigrations()
	if !has {
		t.Error("HasPendingMigrations should be true")
	}

	destructive, _ := migrator.HasDestructivePending()
	if !destructive {
		t.Error("HasDestructivePending should be true")
	}
}

func TestAdvisoryLock(t *testing.T) {
	tmpDir := t.TempDir()

	lock, err := NewAdvisoryLock(tmpDir, "test")
	if err != nil {
		t.Fatalf("NewAdvisoryLock failed: %v", err)
	}

	ctx := context.Background()
	if err := lock.Acquire(ctx, 5*time.Second); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	if !lock.IsHeld() {
		t.Error("Lock should be held")
	}

	lock2, _ := NewAdvisoryLock(tmpDir, "test")
	acquired, _ := lock2.TryAcquire()
	if acquired {
		t.Error("Second lock should not acquire")
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	if lock.IsHeld() {
		t.Error("Lock should not be held after release")
	}

	acquired, _ = lock2.TryAcquire()
	if !acquired {
		t.Error("Second lock should acquire after release")
	}
	lock2.Release()
}
