package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mattnsqlite3 "github.com/mattn/go-sqlite3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
	modernsqlite "modernc.org/sqlite"
	modernsqlite3 "modernc.org/sqlite/lib"
)

// BunSQLiteConfig configures a shared Bun-backed SQLite database handle.
// Defaults are intentionally conservative for state stores where correctness
// under write contention matters more than maximizing local concurrency.
type BunSQLiteConfig struct {
	Path              string
	MaxOpen           int
	MaxIdle           int
	MaxLifetime       time.Duration
	BusyTimeout       time.Duration
	EnableWAL         bool
	ForeignKeys       bool
	CacheSize         int
	Synchronous       string
	WriteRetryMax     int
	WriteRetryBackoff time.Duration
}

// DefaultBunSQLiteConfig returns SQLite settings tuned for bounded, durable
// agent state stores. Callers that need more read concurrency can override.
func DefaultBunSQLiteConfig(path string) BunSQLiteConfig {
	return BunSQLiteConfig{
		Path:              path,
		MaxOpen:           1,
		MaxIdle:           1,
		MaxLifetime:       time.Hour,
		BusyTimeout:       5 * time.Second,
		EnableWAL:         true,
		ForeignKeys:       true,
		CacheSize:         -2000,
		Synchronous:       "normal",
		WriteRetryMax:     5,
		WriteRetryBackoff: 25 * time.Millisecond,
	}
}

// BunSQLiteDB owns a shared SQLite connection pool plus a Bun facade and a
// serialized write path for robust transactional updates under contention.
type BunSQLiteDB struct {
	sqlDB  *sql.DB
	bunDB  *bun.DB
	path   string
	config BunSQLiteConfig

	writeMu sync.Mutex
}

// OpenBunSQLite opens a Bun-backed SQLite database with a consistent
// connection, pragma, and write-retry policy.
func OpenBunSQLite(cfg BunSQLiteConfig) (*BunSQLiteDB, error) {
	cfg = normalizeBunSQLiteConfig(cfg)
	dsn := buildBunSQLiteDSN(cfg)

	sqlDB, err := sql.Open(sqliteshim.ShimName, dsn)
	if err != nil {
		return nil, fmt.Errorf("bun sqlite: open: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpen)
	sqlDB.SetMaxIdleConns(cfg.MaxIdle)
	sqlDB.SetConnMaxLifetime(cfg.MaxLifetime)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("bun sqlite: ping: %w", err)
	}

	return &BunSQLiteDB{
		sqlDB:  sqlDB,
		bunDB:  bun.NewDB(sqlDB, sqlitedialect.New()),
		path:   cfg.Path,
		config: cfg,
	}, nil
}

func normalizeBunSQLiteConfig(cfg BunSQLiteConfig) BunSQLiteConfig {
	if cfg.MaxOpen <= 0 {
		cfg.MaxOpen = 1
	}
	if cfg.MaxIdle <= 0 || cfg.MaxIdle > cfg.MaxOpen {
		cfg.MaxIdle = cfg.MaxOpen
	}
	if cfg.MaxLifetime <= 0 {
		cfg.MaxLifetime = time.Hour
	}
	if cfg.BusyTimeout <= 0 {
		cfg.BusyTimeout = 5 * time.Second
	}
	if cfg.CacheSize == 0 {
		cfg.CacheSize = -2000
	}
	if strings.TrimSpace(cfg.Synchronous) == "" {
		cfg.Synchronous = "normal"
	}
	if cfg.WriteRetryMax < 0 {
		cfg.WriteRetryMax = 0
	}
	if cfg.WriteRetryBackoff <= 0 {
		cfg.WriteRetryBackoff = 25 * time.Millisecond
	}
	return cfg
}

func buildBunSQLiteDSN(cfg BunSQLiteConfig) string {
	journalMode := "DELETE"
	if cfg.EnableWAL {
		journalMode = "WAL"
	}
	return fmt.Sprintf(
		"file:%s?_busy_timeout=%d&_journal_mode=%s&_foreign_keys=%d&_synchronous=%s&cache_size=%d",
		cfg.Path,
		int(cfg.BusyTimeout.Milliseconds()),
		journalMode,
		boolToInt(cfg.ForeignKeys),
		strings.ToUpper(strings.TrimSpace(cfg.Synchronous)),
		cfg.CacheSize,
	)
}

// Bun returns the Bun DB facade.
func (db *BunSQLiteDB) Bun() *bun.DB {
	if db == nil {
		return nil
	}
	return db.bunDB
}

// SQLDB returns the underlying database/sql handle.
func (db *BunSQLiteDB) SQLDB() *sql.DB {
	if db == nil {
		return nil
	}
	return db.sqlDB
}

// Path returns the opened DB path.
func (db *BunSQLiteDB) Path() string {
	if db == nil {
		return ""
	}
	return db.path
}

// Close closes the Bun DB and its underlying SQL connections.
func (db *BunSQLiteDB) Close() error {
	if db == nil || db.bunDB == nil {
		return nil
	}
	return db.bunDB.Close()
}

// ExecSchema executes a schema or migration SQL blob against the underlying DB.
func (db *BunSQLiteDB) ExecSchema(ctx context.Context, schema string) error {
	if db == nil || db.sqlDB == nil || strings.TrimSpace(schema) == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := db.sqlDB.ExecContext(ctx, schema)
	return err
}

// ExecContext forwards raw SQL execution to the underlying DB for callers
// that still use direct SQL while sharing the Bun-backed connection policy.
func (db *BunSQLiteDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return db.sqlDB.ExecContext(ctx, query, args...)
}

// Exec forwards raw SQL execution without an explicit context.
func (db *BunSQLiteDB) Exec(query string, args ...any) (sql.Result, error) {
	return db.sqlDB.Exec(query, args...)
}

// QueryContext forwards a query to the underlying DB.
func (db *BunSQLiteDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return db.sqlDB.QueryContext(ctx, query, args...)
}

// Query forwards a query without an explicit context.
func (db *BunSQLiteDB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.sqlDB.Query(query, args...)
}

// QueryRowContext forwards a single-row query to the underlying DB.
func (db *BunSQLiteDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if ctx == nil {
		ctx = context.Background()
	}
	return db.sqlDB.QueryRowContext(ctx, query, args...)
}

// QueryRow forwards a single-row query without an explicit context.
func (db *BunSQLiteDB) QueryRow(query string, args ...any) *sql.Row {
	return db.sqlDB.QueryRow(query, args...)
}

// Begin starts a raw SQL transaction on the underlying DB.
func (db *BunSQLiteDB) Begin() (*sql.Tx, error) {
	return db.sqlDB.Begin()
}

// RunInWriteTx serializes SQLite write transactions and retries on busy/locked
// failures so callers can safely compose multi-statement mutations.
func (db *BunSQLiteDB) RunInWriteTx(
	ctx context.Context,
	fn func(context.Context, bun.Tx) error,
) error {
	if db == nil || db.bunDB == nil || fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	attempts := db.config.WriteRetryMax + 1
	if attempts < 1 {
		attempts = 1
	}
	backoff := db.config.WriteRetryBackoff
	if backoff <= 0 {
		backoff = 25 * time.Millisecond
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		db.writeMu.Lock()
		err := db.bunDB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return fn(ctx, tx)
		})
		db.writeMu.Unlock()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil || !IsSQLiteBusy(err) || attempt == attempts {
			return err
		}
		timer := time.NewTimer(backoff * time.Duration(1<<(attempt-1)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return lastErr
}

// IsSQLiteBusy reports whether err represents SQLITE_BUSY/SQLITE_LOCKED for
// either modernc or mattn-backed SQLite drivers, with a string fallback.
func IsSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}

	var modernErr *modernsqlite.Error
	if errors.As(err, &modernErr) {
		code := modernErr.Code() & 0xff
		return code == modernsqlite3.SQLITE_BUSY || code == modernsqlite3.SQLITE_LOCKED
	}

	var mattnErr mattnsqlite3.Error
	if errors.As(err, &mattnErr) {
		return mattnErr.Code == mattnsqlite3.ErrBusy || mattnErr.Code == mattnsqlite3.ErrLocked
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked") ||
		strings.Contains(msg, "database is locked")
}
