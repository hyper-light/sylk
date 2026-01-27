// Package sylkdir manages the .sylk directory structure for Sylk knowledge graph storage.
// This package provides initialization, validation, and locking for the filesystem layout
// described in DB.md.
package sylkdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Directory and file constants for the .sylk layout.
const (
	// RootDir is the name of the .sylk directory.
	RootDir = ".sylk"
	// ConfigFile is the name of the configuration file.
	ConfigFile = "config.yaml"
	// LockFile is the name of the lock file for concurrent access prevention.
	LockFile = "lock"

	// KnowledgeDir contains all knowledge graph data.
	KnowledgeDir = "knowledge"
	// MetaFile is the global metadata file within knowledge directory.
	MetaFile = "meta.json"

	// NodesDir contains node storage.
	NodesDir = "nodes"
	// NodeBlocksDir contains node block files.
	NodeBlocksDir = "blocks"
	// NodeIndexDir contains node index files.
	NodeIndexDir = "index"

	// EdgesDir contains edge shard storage.
	EdgesDir = "edges"

	// VectorsDir contains vector storage.
	VectorsDir = "vectors"
	// VectorShardsDir contains vector shard files.
	VectorShardsDir = "shards"
	// VectorGraphDir contains the Vamana graph files.
	VectorGraphDir = "graph"
	// VectorPartitionsDir contains IVF partition data.
	VectorPartitionsDir = "partitions"

	// BleveDir contains Bleve full-text search index.
	BleveDir = "bleve"
	// BleveIndexDir contains the actual Bleve index.
	BleveIndexDir = "index"

	// SessionsDir contains per-session storage.
	SessionsDir = "sessions"
)

// ErrLocked is returned when attempting to lock an already locked directory.
var ErrLocked = errors.New("sylkdir: directory is locked by another process")

// ErrNotInitialized is returned when operations are performed on an uninitialized directory.
var ErrNotInitialized = errors.New("sylkdir: directory is not initialized")

// ValidationError represents a validation failure with details.
type ValidationError struct {
	Path   string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("sylkdir validation: %s: %s", e.Path, e.Reason)
}

// ValidationErrors is a collection of validation errors.
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no validation errors"
	}
	if len(e) == 1 {
		return e[0].Error()
	}
	return fmt.Sprintf("sylkdir validation: %d errors (first: %s)", len(e), e[0].Error())
}

// SylkDir manages the .sylk directory structure.
type SylkDir struct {
	// projectPath is the root project directory containing .sylk.
	projectPath string
	// lockFile holds the file descriptor for the lock file.
	lockFile *os.File
}

// New creates a new SylkDir instance without initializing or validating.
// Call Init() to create the directory structure or Validate() to check an existing one.
func New(projectPath string) *SylkDir {
	return &SylkDir{
		projectPath: projectPath,
	}
}

// Init creates the full .sylk directory structure.
// If the directory already exists, this is a no-op for existing directories
// but will create any missing subdirectories.
func (s *SylkDir) Init() error {
	dirs := []string{
		s.RootPath(),
		s.KnowledgePath(),
		s.NodesPath(),
		s.NodeBlocksPath(),
		s.NodeIndexPath(),
		s.EdgesPath(),
		s.VectorsPath(),
		s.VectorShardsPath(),
		s.VectorGraphPath(),
		s.VectorPartitionsPath(),
		s.BlevePath(),
		s.BleveIndexPath(),
		s.SessionsPath(),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("sylkdir: failed to create directory %s: %w", dir, err)
		}
	}

	// Create default config.yaml if it doesn't exist
	configPath := s.ConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := s.writeDefaultConfig(); err != nil {
			return fmt.Errorf("sylkdir: failed to create config: %w", err)
		}
	}

	// Create meta.json if it doesn't exist
	metaPath := s.MetaPath()
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		if err := s.writeDefaultMeta(); err != nil {
			return fmt.Errorf("sylkdir: failed to create meta: %w", err)
		}
	}

	return nil
}

// Validate checks that the directory structure is complete and valid.
// Returns nil if valid, or ValidationErrors containing all issues found.
func (s *SylkDir) Validate() error {
	var errs ValidationErrors

	// Check required directories exist
	requiredDirs := map[string]string{
		s.RootPath():             "root .sylk directory",
		s.KnowledgePath():        "knowledge directory",
		s.NodesPath():            "nodes directory",
		s.NodeBlocksPath():       "node blocks directory",
		s.NodeIndexPath():        "node index directory",
		s.EdgesPath():            "edges directory",
		s.VectorsPath():          "vectors directory",
		s.VectorShardsPath():     "vector shards directory",
		s.VectorGraphPath():      "vector graph directory",
		s.VectorPartitionsPath(): "vector partitions directory",
		s.BlevePath():            "bleve directory",
		s.BleveIndexPath():       "bleve index directory",
		s.SessionsPath():         "sessions directory",
	}

	for path, desc := range requiredDirs {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			errs = append(errs, ValidationError{Path: path, Reason: desc + " missing"})
		} else if err != nil {
			errs = append(errs, ValidationError{Path: path, Reason: fmt.Sprintf("stat error: %v", err)})
		} else if !info.IsDir() {
			errs = append(errs, ValidationError{Path: path, Reason: desc + " is not a directory"})
		}
	}

	// Check required files exist
	requiredFiles := map[string]string{
		s.ConfigPath(): "config file",
		s.MetaPath():   "meta file",
	}

	for path, desc := range requiredFiles {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			errs = append(errs, ValidationError{Path: path, Reason: desc + " missing"})
		} else if err != nil {
			errs = append(errs, ValidationError{Path: path, Reason: fmt.Sprintf("stat error: %v", err)})
		} else if info.IsDir() {
			errs = append(errs, ValidationError{Path: path, Reason: desc + " is a directory, expected file"})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Lock acquires an exclusive lock on the .sylk directory.
// This prevents concurrent access from multiple processes.
// Returns ErrLocked if another process holds the lock.
// The lock is automatically released when Close() is called.
func (s *SylkDir) Lock() error {
	lockPath := s.LockPath()

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create lock directory: %w", err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to open lock file: %w", err)
	}

	// Try to acquire exclusive lock (non-blocking)
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLocked
		}
		return fmt.Errorf("sylkdir: failed to acquire lock: %w", err)
	}

	// Write PID to lock file for debugging
	if err := f.Truncate(0); err != nil {
		s.releaseLock(f)
		return fmt.Errorf("sylkdir: failed to truncate lock file: %w", err)
	}
	if _, err := f.WriteString(fmt.Sprintf("%d\n", os.Getpid())); err != nil {
		s.releaseLock(f)
		return fmt.Errorf("sylkdir: failed to write lock file: %w", err)
	}

	s.lockFile = f
	return nil
}

// Unlock releases the directory lock.
func (s *SylkDir) Unlock() error {
	if s.lockFile == nil {
		return nil
	}
	return s.releaseLock(s.lockFile)
}

func (s *SylkDir) releaseLock(f *os.File) error {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	err := f.Close()
	if f == s.lockFile {
		s.lockFile = nil
	}
	return err
}

// Close releases resources including any held lock.
func (s *SylkDir) Close() error {
	return s.Unlock()
}

// IsLocked returns true if this instance holds the lock.
func (s *SylkDir) IsLocked() bool {
	return s.lockFile != nil
}

// Exists returns true if the .sylk directory exists.
func (s *SylkDir) Exists() bool {
	info, err := os.Stat(s.RootPath())
	return err == nil && info.IsDir()
}

// Path accessors

// RootPath returns the path to the .sylk directory.
func (s *SylkDir) RootPath() string {
	return filepath.Join(s.projectPath, RootDir)
}

// ConfigPath returns the path to config.yaml.
func (s *SylkDir) ConfigPath() string {
	return filepath.Join(s.RootPath(), ConfigFile)
}

// LockPath returns the path to the lock file.
func (s *SylkDir) LockPath() string {
	return filepath.Join(s.RootPath(), LockFile)
}

// KnowledgePath returns the path to the knowledge directory.
func (s *SylkDir) KnowledgePath() string {
	return filepath.Join(s.RootPath(), KnowledgeDir)
}

// MetaPath returns the path to the global meta.json file.
func (s *SylkDir) MetaPath() string {
	return filepath.Join(s.KnowledgePath(), MetaFile)
}

// NodesPath returns the path to the nodes directory.
func (s *SylkDir) NodesPath() string {
	return filepath.Join(s.KnowledgePath(), NodesDir)
}

// NodeBlocksPath returns the path to the node blocks directory.
func (s *SylkDir) NodeBlocksPath() string {
	return filepath.Join(s.NodesPath(), NodeBlocksDir)
}

// NodeIndexPath returns the path to the node index directory.
func (s *SylkDir) NodeIndexPath() string {
	return filepath.Join(s.NodesPath(), NodeIndexDir)
}

// EdgesPath returns the path to the edges directory.
func (s *SylkDir) EdgesPath() string {
	return filepath.Join(s.KnowledgePath(), EdgesDir)
}

// VectorsPath returns the path to the vectors directory.
func (s *SylkDir) VectorsPath() string {
	return filepath.Join(s.KnowledgePath(), VectorsDir)
}

// VectorShardsPath returns the path to the vector shards directory.
func (s *SylkDir) VectorShardsPath() string {
	return filepath.Join(s.VectorsPath(), VectorShardsDir)
}

// VectorGraphPath returns the path to the vector graph directory.
func (s *SylkDir) VectorGraphPath() string {
	return filepath.Join(s.VectorsPath(), VectorGraphDir)
}

// VectorPartitionsPath returns the path to the vector partitions directory.
func (s *SylkDir) VectorPartitionsPath() string {
	return filepath.Join(s.VectorsPath(), VectorPartitionsDir)
}

// BlevePath returns the path to the Bleve directory.
func (s *SylkDir) BlevePath() string {
	return filepath.Join(s.RootPath(), BleveDir)
}

// BleveIndexPath returns the path to the Bleve index directory.
func (s *SylkDir) BleveIndexPath() string {
	return filepath.Join(s.BlevePath(), BleveIndexDir)
}

// SessionsPath returns the path to the sessions directory.
func (s *SylkDir) SessionsPath() string {
	return filepath.Join(s.RootPath(), SessionsDir)
}

// SessionPath returns the path to a specific session directory.
func (s *SylkDir) SessionPath(sessionID string) string {
	return filepath.Join(s.SessionsPath(), sessionID)
}

// writeDefaultConfig creates an initial config.yaml with defaults.
func (s *SylkDir) writeDefaultConfig() error {
	defaultConfig := `# Sylk Knowledge Graph Configuration
# Generated automatically - modify as needed

version: 1

embedding:
  provider: "voyage"
  model: "voyage-code-3"
  batch_size: 64

indexing:
  include_patterns:
    - "**/*.go"
    - "**/*.py"
    - "**/*.ts"
    - "**/*.js"
    - "**/*.rs"
    - "**/*.md"
  exclude_patterns:
    - "**/vendor/**"
    - "**/node_modules/**"
    - "**/.git/**"
    - "**/testdata/**"
  concurrency: 4

storage:
  node_block_size: 4096
  edge_shard_size: 65536
  vector_shard_size: 65536
`
	return os.WriteFile(s.ConfigPath(), []byte(defaultConfig), 0644)
}

// writeDefaultMeta creates an initial meta.json with defaults.
func (s *SylkDir) writeDefaultMeta() error {
	defaultMeta := `{
  "schema_version": 1,
  "next_node_id": 1,
  "next_session_id": 1,
  "committed_sessions": []
}
`
	return os.WriteFile(s.MetaPath(), []byte(defaultMeta), 0644)
}
