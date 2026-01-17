package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/storage"
	_ "github.com/mattn/go-sqlite3"
)

type Manager struct {
	dirs  *storage.Dirs
	pools map[string]*Pool
	mu    sync.RWMutex
}

type Pool struct {
	db          *sql.DB
	path        string
	config      PoolConfig
	mu          sync.RWMutex
	connections int
}

type PoolConfig struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
	BusyTimeout time.Duration
	EnableWAL   bool
	ForeignKeys bool
	CacheSize   int
}

func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpen:     10,
		MaxIdle:     5,
		MaxLifetime: time.Hour,
		BusyTimeout: 30 * time.Second,
		EnableWAL:   true,
		ForeignKeys: true,
		CacheSize:   -2000,
	}
}

func NewManager(dirs *storage.Dirs) *Manager {
	return &Manager{
		dirs:  dirs,
		pools: make(map[string]*Pool),
	}
}

func (m *Manager) Open(name string, config PoolConfig) (*Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if pool, ok := m.pools[name]; ok {
		return pool, nil
	}

	path := m.resolvePath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=%d&_journal_mode=WAL&_foreign_keys=%d&cache_size=%d",
		path,
		int(config.BusyTimeout.Milliseconds()),
		boolToInt(config.ForeignKeys),
		config.CacheSize,
	)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	db.SetMaxOpenConns(config.MaxOpen)
	db.SetMaxIdleConns(config.MaxIdle)
	db.SetConnMaxLifetime(config.MaxLifetime)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	pool := &Pool{
		db:     db,
		path:   path,
		config: config,
	}

	m.pools[name] = pool
	return pool, nil
}

func (m *Manager) Get(name string) (*Pool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pool, ok := m.pools[name]
	return pool, ok
}

func (m *Manager) Close(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.pools[name]
	if !ok {
		return nil
	}

	delete(m.pools, name)
	return pool.Close()
}

func (m *Manager) CloseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for name, pool := range m.pools {
		if err := pool.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.pools, name)
	}
	return firstErr
}

func (m *Manager) resolvePath(name string) string {
	switch name {
	case "system":
		return m.dirs.DataDir("system.db")
	default:
		if filepath.IsAbs(name) {
			return name
		}
		return m.dirs.DataDir(name + ".db")
	}
}

func (p *Pool) DB() *sql.DB {
	return p.db
}

func (p *Pool) Path() string {
	return p.path
}

func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db == nil {
		return nil
	}

	err := p.db.Close()
	p.db = nil
	return err
}

func (p *Pool) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.db.ExecContext(ctx, query, args...)
}

func (p *Pool) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return p.db.QueryContext(ctx, query, args...)
}

func (p *Pool) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}

func (p *Pool) Begin(ctx context.Context) (*sql.Tx, error) {
	return p.db.BeginTx(ctx, nil)
}

func (p *Pool) Transaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (p *Pool) Version() (int, error) {
	var version int
	err := p.db.QueryRow("PRAGMA user_version").Scan(&version)
	return version, err
}

func (p *Pool) SetVersion(version int) error {
	_, err := p.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version))
	return err
}

func (p *Pool) IntegrityCheck() error {
	var result string
	err := p.db.QueryRow("PRAGMA integrity_check").Scan(&result)
	if err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	return nil
}

func (p *Pool) Stats() sql.DBStats {
	return p.db.Stats()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
