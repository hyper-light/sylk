package vectorgraphdb

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultDBConfig(t *testing.T) {
	path := "/tmp/test.db"
	cfg := DefaultDBConfig(path)

	if cfg.Path != path {
		t.Errorf("Path = %q, want %q", cfg.Path, path)
	}
	if cfg.MaxOpenConns != DefaultMaxOpenConns {
		t.Errorf("MaxOpenConns = %d, want %d", cfg.MaxOpenConns, DefaultMaxOpenConns)
	}
	if cfg.MaxIdleConns != DefaultMaxIdleConns {
		t.Errorf("MaxIdleConns = %d, want %d", cfg.MaxIdleConns, DefaultMaxIdleConns)
	}
	if cfg.ConnMaxLifetime != DefaultConnMaxLifetime {
		t.Errorf("ConnMaxLifetime = %v, want %v", cfg.ConnMaxLifetime, DefaultConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime != DefaultConnMaxIdleTime {
		t.Errorf("ConnMaxIdleTime = %v, want %v", cfg.ConnMaxIdleTime, DefaultConnMaxIdleTime)
	}
}

func TestLightDBConfig(t *testing.T) {
	path := "/tmp/test.db"
	cfg := LightDBConfig(path)

	if cfg.MaxOpenConns != 10 {
		t.Errorf("MaxOpenConns = %d, want 10", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 5 {
		t.Errorf("MaxIdleConns = %d, want 5", cfg.MaxIdleConns)
	}
}

func TestHeavyDBConfig(t *testing.T) {
	path := "/tmp/test.db"
	cfg := HeavyDBConfig(path)

	if cfg.MaxOpenConns != 50 {
		t.Errorf("MaxOpenConns = %d, want 50", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 25 {
		t.Errorf("MaxIdleConns = %d, want 25", cfg.MaxIdleConns)
	}
}

func TestDBConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  DBConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid default config",
			config:  DefaultDBConfig("/tmp/test.db"),
			wantErr: false,
		},
		{
			name:    "valid light config",
			config:  LightDBConfig("/tmp/test.db"),
			wantErr: false,
		},
		{
			name:    "valid heavy config",
			config:  HeavyDBConfig("/tmp/test.db"),
			wantErr: false,
		},
		{
			name: "empty path",
			config: DBConfig{
				Path:         "",
				MaxOpenConns: 10,
				MaxIdleConns: 5,
			},
			wantErr: true,
			errMsg:  "path is required",
		},
		{
			name: "zero MaxOpenConns",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: 0,
				MaxIdleConns: 0,
			},
			wantErr: true,
			errMsg:  "MaxOpenConns must be between",
		},
		{
			name: "negative MaxOpenConns",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: -1,
				MaxIdleConns: 0,
			},
			wantErr: true,
			errMsg:  "MaxOpenConns must be between",
		},
		{
			name: "MaxOpenConns exceeds limit",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: MaxOpenConnsLimit + 1,
				MaxIdleConns: 10,
			},
			wantErr: true,
			errMsg:  "MaxOpenConns must be between",
		},
		{
			name: "MaxOpenConns at limit",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: MaxOpenConnsLimit,
				MaxIdleConns: 10,
			},
			wantErr: false,
		},
		{
			name: "negative MaxIdleConns",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: 10,
				MaxIdleConns: -1,
			},
			wantErr: true,
			errMsg:  "MaxIdleConns must be at least",
		},
		{
			name: "MaxIdleConns exceeds MaxOpenConns",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: 10,
				MaxIdleConns: 20,
			},
			wantErr: true,
			errMsg:  "MaxIdleConns (20) cannot exceed MaxOpenConns (10)",
		},
		{
			name: "MaxIdleConns equals MaxOpenConns",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: 10,
				MaxIdleConns: 10,
			},
			wantErr: false,
		},
		{
			name: "zero MaxIdleConns is valid",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: 10,
				MaxIdleConns: 0,
			},
			wantErr: false,
		},
		{
			name: "minimum valid config",
			config: DBConfig{
				Path:         "/tmp/test.db",
				MaxOpenConns: MinOpenConns,
				MaxIdleConns: MinIdleConns,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errMsg != "" && !dbCfgContainsSubstr(err.Error(), tt.errMsg) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestFunctionalOptions(t *testing.T) {
	path := "/tmp/test.db"
	cfg := DefaultDBConfig(path)

	// Apply options
	WithMaxOpenConns(100)(&cfg)
	WithMaxIdleConns(50)(&cfg)
	WithConnMaxLifetime(2 * time.Hour)(&cfg)
	WithConnMaxIdleTime(time.Hour)(&cfg)

	if cfg.MaxOpenConns != 100 {
		t.Errorf("MaxOpenConns = %d, want 100", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 50 {
		t.Errorf("MaxIdleConns = %d, want 50", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 2*time.Hour {
		t.Errorf("ConnMaxLifetime = %v, want 2h", cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime != time.Hour {
		t.Errorf("ConnMaxIdleTime = %v, want 1h", cfg.ConnMaxIdleTime)
	}
}

func TestOpenWithOptions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := OpenWithOptions(dbPath,
		WithMaxOpenConns(30),
		WithMaxIdleConns(15),
	)
	if err != nil {
		t.Fatalf("OpenWithOptions: %v", err)
	}
	defer db.Close()

	// Verify the database is functional
	if db.db == nil {
		t.Error("db.db is nil")
	}
	if db.path != dbPath {
		t.Errorf("path = %q, want %q", db.path, dbPath)
	}

	// Verify connection pool settings via sql.DB.Stats()
	stats := db.db.Stats()
	if stats.MaxOpenConnections != 30 {
		t.Errorf("MaxOpenConnections = %d, want 30", stats.MaxOpenConnections)
	}
}

func TestOpenWithOptionsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// MaxIdleConns > MaxOpenConns should fail validation
	_, err := OpenWithOptions(dbPath,
		WithMaxOpenConns(10),
		WithMaxIdleConns(20),
	)
	if err == nil {
		t.Error("expected error for invalid config, got nil")
	}
}

func TestOpenWithConfigValidation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	tests := []struct {
		name    string
		config  DBConfig
		wantErr bool
	}{
		{
			name:    "valid config",
			config:  DefaultDBConfig(dbPath),
			wantErr: false,
		},
		{
			name: "invalid MaxOpenConns",
			config: DBConfig{
				Path:         dbPath,
				MaxOpenConns: 0,
				MaxIdleConns: 0,
			},
			wantErr: true,
		},
		{
			name: "MaxIdleConns exceeds MaxOpenConns",
			config: DBConfig{
				Path:         dbPath,
				MaxOpenConns: 5,
				MaxIdleConns: 10,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := OpenWithConfig(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
					if db != nil {
						db.Close()
					}
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				} else {
					db.Close()
				}
			}
		})
	}
}

func TestOpenWithConfigAppliesSettings(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	config := DBConfig{
		Path:            dbPath,
		MaxOpenConns:    42,
		MaxIdleConns:    21,
		ConnMaxLifetime: 45 * time.Minute,
		ConnMaxIdleTime: 15 * time.Minute,
	}

	db, err := OpenWithConfig(config)
	if err != nil {
		t.Fatalf("OpenWithConfig: %v", err)
	}
	defer db.Close()

	stats := db.db.Stats()
	if stats.MaxOpenConnections != 42 {
		t.Errorf("MaxOpenConnections = %d, want 42", stats.MaxOpenConnections)
	}
}

func TestConstantBounds(t *testing.T) {
	// Verify constants have sensible values
	if MinOpenConns < 1 {
		t.Errorf("MinOpenConns = %d, should be at least 1", MinOpenConns)
	}
	if MaxOpenConnsLimit < 100 {
		t.Errorf("MaxOpenConnsLimit = %d, should allow at least 100 connections", MaxOpenConnsLimit)
	}
	if MinIdleConns < 0 {
		t.Errorf("MinIdleConns = %d, should be non-negative", MinIdleConns)
	}
	if DefaultMaxIdleConns > DefaultMaxOpenConns {
		t.Errorf("DefaultMaxIdleConns (%d) > DefaultMaxOpenConns (%d)",
			DefaultMaxIdleConns, DefaultMaxOpenConns)
	}
}

// dbCfgContainsSubstr checks if s contains substr
func dbCfgContainsSubstr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && dbCfgFindSubstr(s, substr)))
}

func dbCfgFindSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
