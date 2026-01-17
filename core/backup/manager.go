package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/database"
	"github.com/adalundhe/sylk/core/storage"
)

type Manager struct {
	dirs      *storage.Dirs
	config    Config
	scheduler *Scheduler
	mu        sync.Mutex
}

type Config struct {
	RetentionCount         int
	MigrationRetentionDays int
	PeriodicInterval       time.Duration
	EnablePreSession       bool
	EnablePreMigration     bool
}

func DefaultConfig() Config {
	return Config{
		RetentionCount:         10,
		MigrationRetentionDays: 30,
		PeriodicInterval:       24 * time.Hour,
		EnablePreSession:       true,
		EnablePreMigration:     true,
	}
}

func NewManager(dirs *storage.Dirs, config Config) *Manager {
	m := &Manager{
		dirs:   dirs,
		config: config,
	}
	return m
}

func (m *Manager) Backup(ctx context.Context, pool *database.Pool, scope string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	backupDir := m.dirs.BackupDir(scope)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405.000")
	filename := fmt.Sprintf("backup-%s.db", timestamp)
	backupPath := filepath.Join(backupDir, filename)

	if err := m.performBackup(ctx, pool, backupPath); err != nil {
		return "", err
	}

	if err := m.enforceRetention(backupDir); err != nil {
		return backupPath, fmt.Errorf("retention cleanup: %w", err)
	}

	return backupPath, nil
}

func (m *Manager) performBackup(ctx context.Context, pool *database.Pool, destPath string) error {
	_, err := pool.Exec(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}

	srcPath := pool.Path()

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer dest.Close()

	if _, err := io.Copy(dest, src); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("copy: %w", err)
	}

	return nil
}

func (m *Manager) BackupToFile(pool *database.Pool, destPath string) error {
	srcPath := pool.Path()

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dest.Close()

	_, err = io.Copy(dest, src)
	return err
}

func (m *Manager) enforceRetention(backupDir string) error {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return err
	}

	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "backup-") {
			backups = append(backups, entry.Name())
		}
	}

	sort.Strings(backups)

	if len(backups) <= m.config.RetentionCount {
		return nil
	}

	toDelete := backups[:len(backups)-m.config.RetentionCount]
	for _, name := range toDelete {
		path := filepath.Join(backupDir, name)
		if err := os.Remove(path); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) ListBackups(scope string) ([]BackupInfo, error) {
	backupDir := m.dirs.BackupDir(scope)

	entries, err := os.ReadDir(backupDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var backups []BackupInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "backup-") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backups = append(backups, BackupInfo{
			Name:      entry.Name(),
			Path:      filepath.Join(backupDir, entry.Name()),
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})

	return backups, nil
}

func (m *Manager) Restore(ctx context.Context, pool *database.Pool, backupPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("backup not found: %w", err)
	}

	destPath := pool.Path()

	if err := pool.Close(); err != nil {
		return fmt.Errorf("close pool: %w", err)
	}

	src, err := os.Open(backupPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dest.Close()

	_, err = io.Copy(dest, src)
	return err
}

func (m *Manager) LatestBackup(scope string) (*BackupInfo, error) {
	backups, err := m.ListBackups(scope)
	if err != nil {
		return nil, err
	}
	if len(backups) == 0 {
		return nil, nil
	}
	return &backups[0], nil
}

func (m *Manager) DeleteBackup(path string) error {
	backupDir := m.dirs.BackupDir("")
	absBackupDir, _ := filepath.Abs(backupDir)
	absPath, _ := filepath.Abs(path)

	if !strings.HasPrefix(absPath, filepath.Dir(absBackupDir)) {
		return fmt.Errorf("path outside backup directory")
	}

	return os.Remove(path)
}

type BackupInfo struct {
	Name      string
	Path      string
	Size      int64
	CreatedAt time.Time
}
