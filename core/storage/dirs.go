// Package storage provides platform-native directory resolution with XDG support.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
)

// Dirs provides platform-native directory resolution with XDG support.
type Dirs struct {
	Config string // User configuration (settings, credentials)
	Data   string // Persistent data (sessions, databases, per-project knowledge)
	Cache  string // Regenerable cache (TTL-tiered: hot/warm/cold)
	State  string // Runtime state (logs, runtime, temp, signals)
}

// ProjectDirs returns project-local directories.
type ProjectDirs struct {
	Root   string // .sylk/
	Config string // .sylk/config.yaml (committed)
	Local  string // .sylk/local/ (gitignored)
}

var (
	globalDirs     *Dirs
	globalDirsOnce sync.Once
	globalDirsErr  error
)

// ResolveDirs returns platform-appropriate directories.
// Results are cached after first call.
func ResolveDirs() (*Dirs, error) {
	globalDirsOnce.Do(func() {
		globalDirs, globalDirsErr = resolveDirsImpl()
	})
	return globalDirs, globalDirsErr
}

func resolveDirsImpl() (*Dirs, error) {
	dirs := &Dirs{
		Config: resolveDir("XDG_CONFIG_HOME", platformConfigDefault()),
		Data:   resolveDir("XDG_DATA_HOME", platformDataDefault()),
		Cache:  resolveDir("XDG_CACHE_HOME", platformCacheDefault()),
		State:  resolveDir("XDG_STATE_HOME", platformStateDefault()),
	}
	return dirs, nil
}

func resolveDir(envVar, fallback string) string {
	if dir := os.Getenv(envVar); dir != "" {
		return filepath.Join(dir, "sylk")
	}
	return fallback
}

// ResolveProjectDirs returns project-local directories for the given project root.
func ResolveProjectDirs(projectRoot string) *ProjectDirs {
	sylkDir := filepath.Join(projectRoot, ".sylk")
	return &ProjectDirs{
		Root:   sylkDir,
		Config: filepath.Join(sylkDir, "config.yaml"),
		Local:  filepath.Join(sylkDir, "local"),
	}
}

// ProjectHash generates a consistent hash for a project path.
// Used for per-project database isolation.
func ProjectHash(projectRoot string) string {
	absPath, err := filepath.Abs(projectRoot)
	if err != nil {
		absPath = projectRoot
	}
	hash := sha256.Sum256([]byte(absPath))
	return hex.EncodeToString(hash[:8]) // 16 chars
}

// EnsureDir creates a directory with the specified permissions if it doesn't exist.
// Uses 0700 for sensitive directories by default.
func EnsureDir(path string, perm os.FileMode) error {
	if perm == 0 {
		perm = 0700
	}
	return os.MkdirAll(path, perm)
}

// EnsureSensitiveDir creates a directory with restricted permissions (0700).
func EnsureSensitiveDir(path string) error {
	return EnsureDir(path, 0700)
}

// EnsureStandardDir creates a directory with standard permissions (0755).
func EnsureStandardDir(path string) error {
	return EnsureDir(path, 0755)
}

// CleanupDir removes a directory after validating it's within expected boundaries.
func CleanupDir(path string, allowedPrefixes []string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	// Validate path is within allowed boundaries
	allowed := false
	for _, prefix := range allowedPrefixes {
		absPrefix, err := filepath.Abs(prefix)
		if err != nil {
			continue
		}
		if len(absPath) >= len(absPrefix) && absPath[:len(absPrefix)] == absPrefix {
			allowed = true
			break
		}
	}

	if !allowed {
		return &PathNotAllowedError{Path: absPath}
	}

	return os.RemoveAll(path)
}

// TempDir returns an OS-managed temp directory with sylk prefix.
func TempDir(pattern string) (string, error) {
	if pattern == "" {
		pattern = "sylk-*"
	} else if pattern[len(pattern)-1] != '*' {
		pattern = "sylk-" + pattern + "-*"
	} else {
		pattern = "sylk-" + pattern
	}
	return os.MkdirTemp("", pattern)
}

// PathNotAllowedError indicates a path operation was rejected.
type PathNotAllowedError struct {
	Path string
}

func (e *PathNotAllowedError) Error() string {
	return "path not allowed: " + e.Path
}

// ConfigDir returns the config subdirectory path.
func (d *Dirs) ConfigDir(subpath ...string) string {
	return filepath.Join(append([]string{d.Config}, subpath...)...)
}

// DataDir returns the data subdirectory path.
func (d *Dirs) DataDir(subpath ...string) string {
	return filepath.Join(append([]string{d.Data}, subpath...)...)
}

// CacheDir returns the cache subdirectory path.
func (d *Dirs) CacheDir(subpath ...string) string {
	return filepath.Join(append([]string{d.Cache}, subpath...)...)
}

// StateDir returns the state subdirectory path.
func (d *Dirs) StateDir(subpath ...string) string {
	return filepath.Join(append([]string{d.State}, subpath...)...)
}

// SessionDir returns the session-specific data directory.
func (d *Dirs) SessionDir(sessionID string) string {
	return d.DataDir("sessions", sessionID)
}

// ProjectDataDir returns the project-specific data directory.
func (d *Dirs) ProjectDataDir(projectRoot string) string {
	return d.DataDir("projects", ProjectHash(projectRoot))
}

// BackupDir returns the backup directory for a specific scope.
func (d *Dirs) BackupDir(scope string) string {
	return d.DataDir("backups", scope)
}

// LogDir returns the log directory.
func (d *Dirs) LogDir() string {
	return d.StateDir("logs")
}

// LockDir returns the lock directory for advisory locks.
func (d *Dirs) LockDir() string {
	return d.StateDir("locks")
}

// EnsureAll creates all standard directories with appropriate permissions.
func (d *Dirs) EnsureAll() error {
	// Sensitive directories (0700)
	sensitiveDirs := []string{
		d.Config,
		d.ConfigDir("credentials"),
	}

	// Standard directories (0755)
	standardDirs := []string{
		d.Data,
		d.DataDir("sessions"),
		d.DataDir("projects"),
		d.DataDir("backups"),
		d.Cache,
		d.CacheDir("hot"),
		d.CacheDir("warm"),
		d.CacheDir("cold"),
		d.State,
		d.LogDir(),
		d.LockDir(),
	}

	for _, dir := range sensitiveDirs {
		if err := EnsureSensitiveDir(dir); err != nil {
			return err
		}
	}

	for _, dir := range standardDirs {
		if err := EnsureStandardDir(dir); err != nil {
			return err
		}
	}

	return nil
}
