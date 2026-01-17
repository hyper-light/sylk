package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

type Migration struct {
	Version     int
	Description string
	Up          func(tx *sql.Tx) error
	Down        func(tx *sql.Tx) error
	Destructive bool
}

type Migrator struct {
	pool       *Pool
	migrations []Migration
	onBackup   func(pool *Pool) error
}

func NewMigrator(pool *Pool, migrations []Migration) *Migrator {
	sorted := make([]Migration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Version < sorted[j].Version
	})

	return &Migrator{
		pool:       pool,
		migrations: sorted,
	}
}

func (m *Migrator) OnBackup(fn func(pool *Pool) error) {
	m.onBackup = fn
}

func (m *Migrator) Migrate(ctx context.Context) error {
	currentVersion, err := m.pool.Version()
	if err != nil {
		return fmt.Errorf("get version: %w", err)
	}

	for _, migration := range m.migrations {
		if migration.Version <= currentVersion {
			continue
		}

		if migration.Destructive && m.onBackup != nil {
			if err := m.onBackup(m.pool); err != nil {
				return fmt.Errorf("backup before migration %d: %w", migration.Version, err)
			}
		}

		if err := m.applyMigration(ctx, migration); err != nil {
			return fmt.Errorf("migration %d (%s): %w", migration.Version, migration.Description, err)
		}
	}

	return nil
}

func (m *Migrator) applyMigration(ctx context.Context, migration Migration) error {
	return m.pool.Transaction(ctx, func(tx *sql.Tx) error {
		if err := migration.Up(tx); err != nil {
			return err
		}

		_, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", migration.Version))
		return err
	})
}

func (m *Migrator) Rollback(ctx context.Context, targetVersion int) error {
	currentVersion, err := m.pool.Version()
	if err != nil {
		return fmt.Errorf("get version: %w", err)
	}

	for i := len(m.migrations) - 1; i >= 0; i-- {
		migration := m.migrations[i]
		if migration.Version <= targetVersion || migration.Version > currentVersion {
			continue
		}

		if migration.Down == nil {
			return fmt.Errorf("migration %d has no down function", migration.Version)
		}

		if err := m.rollbackMigration(ctx, migration, i); err != nil {
			return fmt.Errorf("rollback %d: %w", migration.Version, err)
		}
	}

	return nil
}

func (m *Migrator) rollbackMigration(ctx context.Context, migration Migration, index int) error {
	prevVersion := 0
	if index > 0 {
		prevVersion = m.migrations[index-1].Version
	}

	return m.pool.Transaction(ctx, func(tx *sql.Tx) error {
		if err := migration.Down(tx); err != nil {
			return err
		}

		_, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", prevVersion))
		return err
	})
}

func (m *Migrator) CurrentVersion() (int, error) {
	return m.pool.Version()
}

func (m *Migrator) PendingMigrations() ([]Migration, error) {
	currentVersion, err := m.pool.Version()
	if err != nil {
		return nil, err
	}

	var pending []Migration
	for _, migration := range m.migrations {
		if migration.Version > currentVersion {
			pending = append(pending, migration)
		}
	}
	return pending, nil
}

func (m *Migrator) HasPendingMigrations() (bool, error) {
	pending, err := m.PendingMigrations()
	if err != nil {
		return false, err
	}
	return len(pending) > 0, nil
}

func (m *Migrator) HasDestructivePending() (bool, error) {
	pending, err := m.PendingMigrations()
	if err != nil {
		return false, err
	}

	for _, migration := range pending {
		if migration.Destructive {
			return true, nil
		}
	}
	return false, nil
}
