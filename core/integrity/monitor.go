package integrity

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/backup"
	"github.com/adalundhe/sylk/core/database"
)

type Monitor struct {
	pools        map[string]*database.Pool
	backupMgr    *backup.Manager
	config       Config
	mu           sync.RWMutex
	stopCh       chan struct{}
	stoppedCh    chan struct{}
	running      bool
	lastCheck    map[string]time.Time
	onCorruption func(scope string, err error)
}

type Config struct {
	CheckInterval  time.Duration
	StartupCheck   bool
	CheckOnError   bool
	AutoRestore    bool
	RebuildableDBs map[string]bool
}

func DefaultConfig() Config {
	return Config{
		CheckInterval: 5 * time.Minute,
		StartupCheck:  true,
		CheckOnError:  true,
		AutoRestore:   true,
		RebuildableDBs: map[string]bool{
			"index":     true,
			"knowledge": true,
		},
	}
}

func NewMonitor(backupMgr *backup.Manager, config Config) *Monitor {
	return &Monitor{
		pools:     make(map[string]*database.Pool),
		backupMgr: backupMgr,
		config:    config,
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
		lastCheck: make(map[string]time.Time),
	}
}

func (m *Monitor) Register(scope string, pool *database.Pool) {
	m.mu.Lock()
	m.pools[scope] = pool
	m.mu.Unlock()
}

func (m *Monitor) Unregister(scope string) {
	m.mu.Lock()
	delete(m.pools, scope)
	delete(m.lastCheck, scope)
	m.mu.Unlock()
}

func (m *Monitor) OnCorruption(fn func(scope string, err error)) {
	m.mu.Lock()
	m.onCorruption = fn
	m.mu.Unlock()
}

func (m *Monitor) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = true
	m.mu.Unlock()

	if m.config.StartupCheck {
		if err := m.checkAll(ctx); err != nil {
			return fmt.Errorf("startup check: %w", err)
		}
	}

	go m.run()
	return nil
}

func (m *Monitor) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	m.mu.Unlock()

	close(m.stopCh)
	<-m.stoppedCh
}

func (m *Monitor) run() {
	defer close(m.stoppedCh)

	ticker := time.NewTicker(m.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			ctx := context.Background()
			_ = m.checkAll(ctx)
		}
	}
}

func (m *Monitor) checkAll(ctx context.Context) error {
	m.mu.RLock()
	pools := make(map[string]*database.Pool, len(m.pools))
	for k, v := range m.pools {
		pools[k] = v
	}
	m.mu.RUnlock()

	var firstErr error
	for scope, pool := range pools {
		if err := m.checkOne(ctx, scope, pool); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Monitor) checkOne(ctx context.Context, scope string, pool *database.Pool) error {
	err := pool.IntegrityCheck()

	m.mu.Lock()
	m.lastCheck[scope] = time.Now()
	m.mu.Unlock()

	if err == nil {
		return nil
	}

	m.notifyCorruption(scope, err)

	if m.config.AutoRestore {
		return m.attemptRecovery(ctx, scope, pool)
	}

	return err
}

func (m *Monitor) notifyCorruption(scope string, err error) {
	m.mu.RLock()
	fn := m.onCorruption
	m.mu.RUnlock()

	if fn != nil {
		fn(scope, err)
	}
}

func (m *Monitor) attemptRecovery(ctx context.Context, scope string, pool *database.Pool) error {
	latestBackup, err := m.backupMgr.LatestBackup(scope)
	if err != nil {
		return fmt.Errorf("find backup: %w", err)
	}

	if latestBackup != nil {
		if err := m.backupMgr.Restore(ctx, pool, latestBackup.Path); err != nil {
			return fmt.Errorf("restore: %w", err)
		}
		return nil
	}

	if m.config.RebuildableDBs[scope] {
		return &RebuildRequired{Scope: scope}
	}

	return &NoBackupAvailable{Scope: scope}
}

func (m *Monitor) Check(ctx context.Context, scope string) error {
	m.mu.RLock()
	pool, ok := m.pools[scope]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("unknown scope: %s", scope)
	}

	return m.checkOne(ctx, scope, pool)
}

func (m *Monitor) CheckOnSQLError(ctx context.Context, scope string, sqlErr error) {
	if !m.config.CheckOnError {
		return
	}

	if !isSuspiciousError(sqlErr) {
		return
	}

	_ = m.Check(ctx, scope)
}

func (m *Monitor) LastCheckTime(scope string) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.lastCheck[scope]
	return t, ok
}

func (m *Monitor) IsRebuildable(scope string) bool {
	return m.config.RebuildableDBs[scope]
}

func isSuspiciousError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	suspiciousPatterns := []string{
		"database disk image is malformed",
		"disk I/O error",
		"corrupt",
		"SQLITE_CORRUPT",
		"SQLITE_NOTADB",
	}
	for _, pattern := range suspiciousPatterns {
		if containsIgnoreCase(msg, pattern) {
			return true
		}
	}
	return false
}

func containsIgnoreCase(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if equalIgnoreCase(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

func equalIgnoreCase(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

type RebuildRequired struct {
	Scope string
}

func (e *RebuildRequired) Error() string {
	return fmt.Sprintf("database %s requires rebuild", e.Scope)
}

type NoBackupAvailable struct {
	Scope string
}

func (e *NoBackupAvailable) Error() string {
	return fmt.Sprintf("no backup available for %s", e.Scope)
}
