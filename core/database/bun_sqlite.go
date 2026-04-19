package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mattnsqlite3 "github.com/mattn/go-sqlite3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
	modernsqlite "modernc.org/sqlite"
	modernsqlite3 "modernc.org/sqlite/lib"

	"github.com/adalundhe/sylk/core/activity"
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
// connection, pragma, and write-retry policy. The DSN is generated for the
// active sqliteshim driver (modernc.org/sqlite or mattn/go-sqlite3) — they
// require different parameter syntax. After open, every requested pragma is
// probed back via PRAGMA queries to fail loudly if the driver silently
// dropped any setting (the historical failure mode that produced
// "database is locked" within sub-millisecond on the orchestrator's store).
func OpenBunSQLite(cfg BunSQLiteConfig) (*BunSQLiteDB, error) {
	cfg = normalizeBunSQLiteConfig(cfg)
	dsn, err := buildBunSQLiteDSN(cfg)
	if err != nil {
		return nil, fmt.Errorf("bun sqlite: dsn: %w", err)
	}

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

	if err := verifyBunSQLitePragmas(sqlDB, cfg); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("bun sqlite: pragma verify: %w", err)
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

// buildBunSQLiteDSN emits a DSN whose query parameters are honored by the
// active sqliteshim driver. modernc.org/sqlite reads only `_pragma=name(value)`
// (see modernc/sqlite applyQueryParams); mattn/go-sqlite3 reads its own set
// (`_busy_timeout`, `_journal_mode`, `_foreign_keys`, `_synchronous`,
// `_cache_size`). The two formats are mutually exclusive — passing one to the
// other driver is silently dropped, which previously left the orchestrator
// running without busy_timeout and without WAL.
func buildBunSQLiteDSN(cfg BunSQLiteConfig) (string, error) {
	journalMode := "DELETE"
	if cfg.EnableWAL {
		journalMode = "WAL"
	}
	syncMode := strings.ToUpper(strings.TrimSpace(cfg.Synchronous))
	busyMs := int(cfg.BusyTimeout.Milliseconds())
	fkInt := boolToInt(cfg.ForeignKeys)

	var params string
	switch driver := sqliteshim.DriverName(); driver {
	case "sqlite": // modernc.org/sqlite
		// Order matters cosmetically but not functionally; modernc applies
		// busy_timeout first regardless of order.
		params = fmt.Sprintf(
			"_pragma=busy_timeout(%d)&_pragma=journal_mode(%s)&_pragma=foreign_keys(%d)&_pragma=synchronous(%s)&_pragma=cache_size(%d)",
			busyMs, journalMode, fkInt, syncMode, cfg.CacheSize,
		)
	case "sqlite3": // mattn/go-sqlite3
		params = fmt.Sprintf(
			"_busy_timeout=%d&_journal_mode=%s&_foreign_keys=%d&_synchronous=%s&_cache_size=%d",
			busyMs, journalMode, fkInt, syncMode, cfg.CacheSize,
		)
	default:
		return "", fmt.Errorf("unsupported sqlite driver %q (sqliteshim has no backing driver — this build cannot use sqlite)", driver)
	}

	return fmt.Sprintf("file:%s?%s", cfg.Path, params), nil
}

// verifyBunSQLitePragmas probes the live database for each pragma the caller
// asked for and errors if any value didn't take effect. A silent driver-side
// drop is the historical failure mode that produced sub-millisecond
// "database is locked" errors on the orchestrator's coordination store
// (modernc.org/sqlite ignores mattn-style query params; the legacy DSN was
// mattn-style; every pragma was being dropped).
func verifyBunSQLitePragmas(db *sql.DB, cfg BunSQLiteConfig) error {
	if db == nil {
		return nil
	}

	wantJournal := "delete"
	if cfg.EnableWAL {
		wantJournal = "wal"
	}
	if got, err := readSQLitePragma(db, "journal_mode"); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	} else if !strings.EqualFold(got, wantJournal) {
		return fmt.Errorf("journal_mode = %q, want %q (driver %q likely ignored DSN params)", got, wantJournal, sqliteshim.DriverName())
	}

	wantBusyMs := int64(cfg.BusyTimeout.Milliseconds())
	if got, err := readSQLitePragma(db, "busy_timeout"); err != nil {
		return fmt.Errorf("read busy_timeout: %w", err)
	} else if gotMs, parseErr := strconv.ParseInt(strings.TrimSpace(got), 10, 64); parseErr != nil {
		return fmt.Errorf("busy_timeout pragma returned non-numeric %q", got)
	} else if gotMs != wantBusyMs {
		return fmt.Errorf("busy_timeout = %d ms, want %d ms (driver %q likely ignored DSN params)", gotMs, wantBusyMs, sqliteshim.DriverName())
	}

	wantFK := "0"
	if cfg.ForeignKeys {
		wantFK = "1"
	}
	if got, err := readSQLitePragma(db, "foreign_keys"); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	} else if strings.TrimSpace(got) != wantFK {
		return fmt.Errorf("foreign_keys = %q, want %q", got, wantFK)
	}

	wantSync := strings.ToLower(strings.TrimSpace(cfg.Synchronous))
	if wantSync != "" {
		got, err := readSQLitePragma(db, "synchronous")
		if err != nil {
			return fmt.Errorf("read synchronous: %w", err)
		}
		gotName := normalizeSQLiteSynchronous(strings.TrimSpace(got))
		if !strings.EqualFold(gotName, wantSync) {
			return fmt.Errorf("synchronous = %q, want %q", gotName, wantSync)
		}
	}

	return nil
}

func readSQLitePragma(db *sql.DB, name string) (string, error) {
	var raw string
	row := db.QueryRow("PRAGMA " + name)
	if err := row.Scan(&raw); err != nil {
		return "", err
	}
	return raw, nil
}

// normalizeSQLiteSynchronous maps SQLite's numeric PRAGMA synchronous result
// (0/1/2/3) onto the symbolic name the caller used in config.
func normalizeSQLiteSynchronous(raw string) string {
	switch strings.TrimSpace(raw) {
	case "0":
		return "off"
	case "1":
		return "normal"
	case "2":
		return "full"
	case "3":
		return "extra"
	}
	return strings.ToLower(raw)
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
//
// Activity Fabric chokepoint: every successful transaction emits one
// store_write_committed activity (Fine resolution) carrying store
// identity and retry attempt count. Failed transactions emit
// store_write_failed (also Fine). The activity store's own writes
// bypass this path via SQLDB().ExecContext to avoid recursive
// emission.
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

	span := activity.StartSpan(ctx, activity.ActionStoreWriteCommitted, activity.Subject{
		TargetArtifact: filepath.Base(db.path),
	})
	span.SetAttribute("store_path", db.path)
	defer func() { span.End() }()
	ctx = span.Context()

	attempts := db.config.WriteRetryMax + 1
	if attempts < 1 {
		attempts = 1
	}
	backoff := db.config.WriteRetryBackoff
	if backoff <= 0 {
		backoff = 25 * time.Millisecond
	}

	var lastErr error
	retries := 0
	for attempt := 1; attempt <= attempts; attempt++ {
		db.writeMu.Lock()
		err := db.bunDB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return fn(ctx, tx)
		})
		db.writeMu.Unlock()
		if err == nil {
			span.SetAttribute("retries", retries)
			return nil
		}
		lastErr = err
		retries++
		if ctx.Err() != nil || !IsSQLiteBusy(err) || attempt == attempts {
			span.SetAttribute("retries", retries-1)
			span.EndWithError(err)
			return err
		}
		timer := time.NewTimer(backoff * time.Duration(1<<(attempt-1)))
		select {
		case <-ctx.Done():
			timer.Stop()
			span.SetAttribute("retries", retries-1)
			span.EndWithError(ctx.Err())
			return ctx.Err()
		case <-timer.C:
		}
	}

	span.SetAttribute("retries", retries-1)
	span.EndWithError(lastErr)
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
