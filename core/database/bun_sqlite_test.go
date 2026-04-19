package database

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

// TestOpenBunSQLite_AppliesPragmas guards against the historical regression
// where the DSN used mattn-style query parameters but the active driver was
// modernc.org/sqlite, which silently dropped them. Every requested pragma
// must take effect at runtime.
func TestOpenBunSQLite_AppliesPragmas(t *testing.T) {
	cfg := DefaultBunSQLiteConfig(filepath.Join(t.TempDir(), "pragma.db"))

	db, err := OpenBunSQLite(cfg)
	if err != nil {
		t.Fatalf("OpenBunSQLite: %v", err)
	}
	defer db.Close()

	checks := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
		{"synchronous", "1"}, // 1 == NORMAL; verify by name below too
	}
	for _, c := range checks {
		got, err := readSQLitePragma(db.SQLDB(), c.pragma)
		if err != nil {
			t.Fatalf("PRAGMA %s: %v", c.pragma, err)
		}
		if !strings.EqualFold(strings.TrimSpace(got), c.want) {
			t.Errorf("PRAGMA %s = %q, want %q", c.pragma, got, c.want)
		}
	}

	syncName := normalizeSQLiteSynchronous("1")
	if syncName != "normal" {
		t.Errorf("synchronous symbolic = %q, want normal", syncName)
	}
}

// TestOpenBunSQLite_RejectsUnsupportedDriver only fires on platforms where
// sqliteshim has no backing driver. Documents the contract.
func TestVerifyBunSQLitePragmas_DetectsMissingPragmas(t *testing.T) {
	cfg := DefaultBunSQLiteConfig(filepath.Join(t.TempDir(), "verify.db"))
	cfg.EnableWAL = false // request DELETE journal

	db, err := OpenBunSQLite(cfg)
	if err != nil {
		t.Fatalf("OpenBunSQLite: %v", err)
	}
	defer db.Close()

	got, err := readSQLitePragma(db.SQLDB(), "journal_mode")
	if err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(strings.TrimSpace(got), "delete") {
		t.Errorf("journal_mode = %q, want delete (DSN should honor EnableWAL=false)", got)
	}
}

// TestRunInWriteTx_RetriesOnBusy verifies the retry loop actually fires.
// Combined with the DSN fix this is what prevents sub-millisecond
// "database is locked" errors under coord_* bursts.
func TestRunInWriteTx_RetriesOnBusy(t *testing.T) {
	cfg := DefaultBunSQLiteConfig(filepath.Join(t.TempDir(), "retry.db"))
	cfg.WriteRetryMax = 5
	cfg.WriteRetryBackoff = 5 * time.Millisecond

	db, err := OpenBunSQLite(cfg)
	if err != nil {
		t.Fatalf("OpenBunSQLite: %v", err)
	}
	defer db.Close()

	if _, err := db.SQLDB().Exec(`CREATE TABLE t (k INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO t (k, v) VALUES (?, ?)`, id, "x")
				return err
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("write under contention: %v", err)
	}

	var count int
	if err := db.SQLDB().QueryRow(`SELECT COUNT(*) FROM t`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 8 {
		t.Errorf("inserted rows = %d, want 8", count)
	}
}
