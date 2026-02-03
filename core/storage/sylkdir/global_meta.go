package sylkdir

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// ErrMetaNotLoaded is returned when operations are performed before Load().
var ErrMetaNotLoaded = errors.New("sylkdir: global meta not loaded")

// CommittedSession records a session that has been committed to the global graph.
type CommittedSession struct {
	SessionID     uint32          `json:"session_id"`
	FinalVersion  SemanticVersion `json:"final_version"`  // Session's version at commit time
	GlobalVersion SemanticVersion `json:"global_version"` // Global version after this commit
	CommittedAt   time.Time       `json:"committed_at"`
}

// GlobalVersionManifest tracks the version DAG for the global knowledge graph.
// This mirrors the session VersionManifest structure for consistency.
type GlobalVersionManifest struct {
	Head     SemanticVersion `json:"head"`
	Versions []GlobalVersion `json:"versions"`
}

// GlobalVersion represents a checkpoint in the global knowledge graph history.
// Each commit from a session creates a new global version.
type GlobalVersion struct {
	ID         SemanticVersion    `json:"id"`
	ParentID   SemanticVersion    `json:"parent_id"`
	SessionID  uint32             `json:"session_id"`      // Which session caused this version
	SessionVer SemanticVersion    `json:"session_version"` // Session's version at commit time
	CreatedAt  time.Time          `json:"created_at"`
	Stats      GlobalVersionStats `json:"stats"`
}

// GlobalVersionStats tracks cumulative counts at a global version.
type GlobalVersionStats struct {
	TotalNodes     uint32 `json:"total_nodes"`
	TotalEdges     uint32 `json:"total_edges"`
	TotalVectors   uint32 `json:"total_vectors"`
	TotalDocs      uint32 `json:"total_docs"`
	TombstoneCount uint32 `json:"tombstone_count"` // Dead nodes tracked by tombstone bitmap
}

// GlobalMeta manages the global metadata stored in knowledge/meta.json.
// It provides atomic ID allocation and session commit registration.
type GlobalMeta struct {
	// SchemaVersion indicates the data format version for migrations.
	SchemaVersion int `json:"schema_version"`
	// Version is the current global knowledge graph version.
	Version SemanticVersion `json:"version"`
	// NextNodeID is the next available node ID (atomically incremented).
	NextNodeID uint32 `json:"next_node_id"`
	// NextSessionID is the next available session ID (atomically incremented).
	NextSessionID uint32 `json:"next_session_id"`
	// CommittedSessions lists all sessions merged into global state.
	CommittedSessions []CommittedSession `json:"committed_sessions"`
	// Manifest tracks the version DAG for cumulative snapshots.
	Manifest *GlobalVersionManifest `json:"manifest,omitempty"`
	// LastBleveIndexedVersion is the last global version whose documents were
	// fully indexed in Bleve. Used for crash recovery: if HEAD > this value,
	// the Bleve index must be rebuilt from the HEAD version's JSONL.
	LastBleveIndexedVersion *SemanticVersion `json:"last_bleve_indexed_version,omitempty"`

	// path is the filesystem path to meta.json.
	path string
	// mu protects all fields and file operations.
	mu sync.Mutex
	// loaded indicates whether Load() has been called successfully.
	loaded bool
}

// NewGlobalMeta creates a new GlobalMeta instance for the given path.
// Call Load() to read existing data or Save() to create new file.
func NewGlobalMeta(metaPath string) *GlobalMeta {
	return &GlobalMeta{
		path:              metaPath,
		SchemaVersion:     2, // v2 includes global versioning
		Version:           SemanticVersion{Major: 0, Minor: 0, Patch: 0}, // Initial version
		NextNodeID:        1,
		NextSessionID:     1,
		CommittedSessions: make([]CommittedSession, 0),
	}
}

// NewGlobalMetaFromSylkDir creates a GlobalMeta for the given SylkDir.
func NewGlobalMetaFromSylkDir(sd *SylkDir) *GlobalMeta {
	return NewGlobalMeta(sd.MetaPath())
}

// Load reads the meta.json file from disk.
// Returns an error if the file doesn't exist or is corrupt.
func (m *GlobalMeta) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to read meta: %w", err)
	}

	if err := json.Unmarshal(data, m); err != nil {
		return fmt.Errorf("sylkdir: failed to parse meta: %w", err)
	}

	// Ensure CommittedSessions is not nil
	if m.CommittedSessions == nil {
		m.CommittedSessions = make([]CommittedSession, 0)
	}

	// Ensure Manifest.Versions is not nil (for existing files without manifest)
	if m.Manifest != nil && m.Manifest.Versions == nil {
		m.Manifest.Versions = make([]GlobalVersion, 0)
	}

	m.loaded = true
	return nil
}

// Save writes the meta.json file to disk atomically.
// Uses write-to-temp + rename to prevent corruption.
func (m *GlobalMeta) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.saveUnlocked()
}

// saveUnlocked performs the save without acquiring the mutex.
// Caller must hold m.mu.
func (m *GlobalMeta) saveUnlocked() error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("sylkdir: failed to marshal meta: %w", err)
	}

	// Write to temp file first
	dir := filepath.Dir(m.path)
	tmpFile, err := os.CreateTemp(dir, "meta.*.tmp")
	if err != nil {
		return fmt.Errorf("sylkdir: failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on error
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	// Write data
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("sylkdir: failed to write temp file: %w", err)
	}

	// Sync to disk
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("sylkdir: failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("sylkdir: failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, m.path); err != nil {
		return fmt.Errorf("sylkdir: failed to rename temp file: %w", err)
	}

	success = true
	return nil
}

// initWithManifest initializes a new GlobalMeta file with the given initial version.
// This creates the meta.json file from scratch with proper manifest structure.
func (m *GlobalMeta) initWithManifest(initialVersion SemanticVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.SchemaVersion = 2
	m.Version = initialVersion
	m.NextNodeID = 1
	m.NextSessionID = 1
	m.CommittedSessions = make([]CommittedSession, 0)
	m.Manifest = &GlobalVersionManifest{
		Head: initialVersion,
		Versions: []GlobalVersion{
			{
				ID:        initialVersion,
				ParentID:  SemanticVersion{}, // Zero = no parent
				SessionID: 0,                 // System-created
				CreatedAt: time.Now().UTC(),
				Stats:     GlobalVersionStats{},
			},
		},
	}
	m.loaded = true

	return m.saveUnlocked()
}

// AllocateNodeID atomically increments NextNodeID and returns the allocated ID.
// The change is immediately persisted to disk.
func (m *GlobalMeta) AllocateNodeID() (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return 0, ErrMetaNotLoaded
	}

	id := m.NextNodeID
	m.NextNodeID++

	if err := m.saveUnlocked(); err != nil {
		// Rollback on save failure
		m.NextNodeID--
		return 0, fmt.Errorf("sylkdir: failed to persist node ID allocation: %w", err)
	}

	return id, nil
}

// AllocateNodeIDs atomically allocates n consecutive node IDs.
// Returns the first ID in the range. IDs are [firstID, firstID+n).
func (m *GlobalMeta) AllocateNodeIDs(n uint32) (uint32, error) {
	if n == 0 {
		return 0, fmt.Errorf("sylkdir: cannot allocate 0 node IDs")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return 0, ErrMetaNotLoaded
	}

	firstID := m.NextNodeID
	m.NextNodeID += n

	if err := m.saveUnlocked(); err != nil {
		// Rollback on save failure
		m.NextNodeID = firstID
		return 0, fmt.Errorf("sylkdir: failed to persist node ID allocation: %w", err)
	}

	return firstID, nil
}

// AllocateSessionID atomically increments NextSessionID and returns the allocated ID.
// The change is immediately persisted to disk.
func (m *GlobalMeta) AllocateSessionID() (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return 0, ErrMetaNotLoaded
	}

	id := m.NextSessionID
	m.NextSessionID++

	if err := m.saveUnlocked(); err != nil {
		// Rollback on save failure
		m.NextSessionID--
		return 0, fmt.Errorf("sylkdir: failed to persist session ID allocation: %w", err)
	}

	return id, nil
}

// RegisterCommit records a session as committed to the global graph.
// This performs a MAJOR version bump on the global KG (session commit = major milestone).
// finalVersion is the session's version at commit time (typically after a minor pre-commit bump).
func (m *GlobalMeta) RegisterCommit(sessionID uint32, finalVersion SemanticVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return ErrMetaNotLoaded
	}

	// Check for duplicate registration
	for _, cs := range m.CommittedSessions {
		if cs.SessionID == sessionID {
			return fmt.Errorf("sylkdir: session %d already committed", sessionID)
		}
	}

	// MAJOR bump on global version (session commit = major milestone)
	oldVersion := m.Version
	m.Version = m.Version.BumpMajor()

	m.CommittedSessions = append(m.CommittedSessions, CommittedSession{
		SessionID:     sessionID,
		FinalVersion:  finalVersion,
		GlobalVersion: m.Version,
		CommittedAt:   time.Now().UTC(),
	})

	if err := m.saveUnlocked(); err != nil {
		// Rollback on save failure
		m.Version = oldVersion
		m.CommittedSessions = m.CommittedSessions[:len(m.CommittedSessions)-1]
		return fmt.Errorf("sylkdir: failed to persist commit registration: %w", err)
	}

	return nil
}

// IsSessionCommitted returns true if the session has been committed.
func (m *GlobalMeta) IsSessionCommitted(sessionID uint32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cs := range m.CommittedSessions {
		if cs.SessionID == sessionID {
			return true
		}
	}
	return false
}

// GetVersion returns the current global version.
func (m *GlobalMeta) GetVersion() SemanticVersion {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Version
}

// BumpMinorVersion increments the minor version and persists.
// Use for incremental updates (future feature).
func (m *GlobalMeta) BumpMinorVersion() (SemanticVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return SemanticVersion{}, ErrMetaNotLoaded
	}

	oldVersion := m.Version
	m.Version = m.Version.BumpMinor()

	if err := m.saveUnlocked(); err != nil {
		m.Version = oldVersion
		return SemanticVersion{}, fmt.Errorf("sylkdir: failed to persist version bump: %w", err)
	}

	return m.Version, nil
}

// BumpPatchVersion increments the patch version and persists.
// Use for index rebuilds or data repairs.
func (m *GlobalMeta) BumpPatchVersion() (SemanticVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return SemanticVersion{}, ErrMetaNotLoaded
	}

	oldVersion := m.Version
	m.Version = m.Version.BumpPatch()

	if err := m.saveUnlocked(); err != nil {
		m.Version = oldVersion
		return SemanticVersion{}, fmt.Errorf("sylkdir: failed to persist version bump: %w", err)
	}

	return m.Version, nil
}

// GetCommittedSessions returns a copy of the committed sessions list.
func (m *GlobalMeta) GetCommittedSessions() []CommittedSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]CommittedSession, len(m.CommittedSessions))
	copy(result, m.CommittedSessions)
	return result
}

// GetCurrentNodeID returns the next node ID without incrementing.
// Useful for visibility calculations.
func (m *GlobalMeta) GetCurrentNodeID() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.NextNodeID
}

// GetCurrentSessionID returns the next session ID without incrementing.
func (m *GlobalMeta) GetCurrentSessionID() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.NextSessionID
}

// Path returns the filesystem path to the meta file.
func (m *GlobalMeta) Path() string {
	return m.path
}

// IsLoaded returns true if Load() has been called successfully.
func (m *GlobalMeta) IsLoaded() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loaded
}

// GetHead returns the current HEAD version from the manifest.
// Returns zero version if manifest is not initialized.
func (m *GlobalMeta) GetHead() SemanticVersion {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Manifest == nil {
		return SemanticVersion{}
	}
	return m.Manifest.Head
}

// SetHead updates the HEAD version in the manifest and persists.
func (m *GlobalMeta) SetHead(version SemanticVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return ErrMetaNotLoaded
	}

	if m.Manifest == nil {
		return fmt.Errorf("sylkdir: manifest not initialized")
	}

	oldHead := m.Manifest.Head
	m.Manifest.Head = version

	if err := m.saveUnlocked(); err != nil {
		m.Manifest.Head = oldHead
		return fmt.Errorf("sylkdir: failed to persist head update: %w", err)
	}

	return nil
}

// GetAncestorChain returns the version chain from HEAD to root.
func (m *GlobalMeta) GetAncestorChain() []SemanticVersion {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Manifest == nil {
		return nil
	}

	versionMap := make(map[string]*GlobalVersion)
	for i := range m.Manifest.Versions {
		versionMap[m.Manifest.Versions[i].ID.String()] = &m.Manifest.Versions[i]
	}

	var chain []SemanticVersion
	current := m.Manifest.Head

	for !current.IsZero() {
		chain = append(chain, current)
		if v, ok := versionMap[current.String()]; ok {
			current = v.ParentID
		} else {
			break
		}
	}

	return chain
}

// AddVersion adds a new version to the manifest and persists.
// This does NOT update HEAD — call SetHead separately.
func (m *GlobalMeta) AddVersion(v GlobalVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return ErrMetaNotLoaded
	}

	if m.Manifest == nil {
		return fmt.Errorf("sylkdir: manifest not initialized")
	}

	// Check for duplicate version
	for _, existing := range m.Manifest.Versions {
		if existing.ID.Equal(v.ID) {
			return fmt.Errorf("sylkdir: version %s already exists", v.ID.String())
		}
	}

	m.Manifest.Versions = append(m.Manifest.Versions, v)

	if err := m.saveUnlocked(); err != nil {
		m.Manifest.Versions = m.Manifest.Versions[:len(m.Manifest.Versions)-1]
		return fmt.Errorf("sylkdir: failed to persist version addition: %w", err)
	}

	return nil
}

// InitManifest initializes the version manifest with an initial version.
// This is called during SylkDir.Init() for fresh installs.
func (m *GlobalMeta) InitManifest(initialVersion SemanticVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return ErrMetaNotLoaded
	}

	if m.Manifest != nil {
		return nil // Already initialized
	}

	m.Manifest = &GlobalVersionManifest{
		Head: initialVersion,
		Versions: []GlobalVersion{
			{
				ID:        initialVersion,
				ParentID:  SemanticVersion{}, // Zero = no parent
				SessionID: 0,                 // System-created
				CreatedAt: time.Now().UTC(),
				Stats:     GlobalVersionStats{},
			},
		},
	}

	// Also set the Version field to match
	m.Version = initialVersion

	return m.saveUnlocked()
}

// HasManifest returns true if the version manifest is initialized.
func (m *GlobalMeta) HasManifest() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Manifest != nil
}

// GetManifest returns a copy of the version manifest.
func (m *GlobalMeta) GetManifest() *GlobalVersionManifest {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Manifest == nil {
		return nil
	}

	// Return a copy
	versions := make([]GlobalVersion, len(m.Manifest.Versions))
	copy(versions, m.Manifest.Versions)
	return &GlobalVersionManifest{
		Head:     m.Manifest.Head,
		Versions: versions,
	}
}

// LockedMeta wraps GlobalMeta with an additional file lock for
// cross-process synchronization during critical operations.
type LockedMeta struct {
	*GlobalMeta
	lockFile *os.File
}

// WithFileLock acquires an exclusive file lock on the meta file.
// This provides cross-process synchronization for critical operations
// like bulk ID allocation or commit registration.
// Call Release() when done.
func (m *GlobalMeta) WithFileLock() (*LockedMeta, error) {
	// Open lock file adjacent to meta file
	lockPath := m.path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: failed to open meta lock: %w", err)
	}

	// Blocking exclusive lock
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("sylkdir: failed to acquire meta lock: %w", err)
	}

	// Reload data while holding lock (another process may have changed it)
	if err := m.Load(); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("sylkdir: failed to reload meta under lock: %w", err)
	}

	return &LockedMeta{
		GlobalMeta: m,
		lockFile:   f,
	}, nil
}

// Release releases the file lock.
func (lm *LockedMeta) Release() error {
	if lm.lockFile == nil {
		return nil
	}
	syscall.Flock(int(lm.lockFile.Fd()), syscall.LOCK_UN)
	err := lm.lockFile.Close()
	lm.lockFile = nil
	return err
}

// ErrVersionNotFound is returned when checkout targets a nonexistent version.
var ErrVersionNotFound = errors.New("sylkdir: version not found")

// Checkout switches the global HEAD to the specified version.
// The version must exist in the manifest. This only updates the HEAD pointer;
// the caller is responsible for reopening any stores (like Bleve) at the new HEAD.
func (m *GlobalMeta) Checkout(version SemanticVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return ErrMetaNotLoaded
	}

	if m.Manifest == nil {
		return fmt.Errorf("sylkdir: manifest not initialized")
	}

	// Verify the version exists in the manifest
	found := false
	for _, v := range m.Manifest.Versions {
		if v.ID.Equal(version) {
			found = true
			break
		}
	}
	if !found {
		return ErrVersionNotFound
	}

	// Update HEAD
	oldHead := m.Manifest.Head
	m.Manifest.Head = version
	m.Version = version

	if err := m.saveUnlocked(); err != nil {
		m.Manifest.Head = oldHead
		m.Version = oldHead
		return fmt.Errorf("sylkdir: failed to persist checkout: %w", err)
	}

	return nil
}

// GetVersionInfo returns the GlobalVersion for a specific version ID.
// Returns nil if not found.
func (m *GlobalMeta) GetVersionInfo(version SemanticVersion) *GlobalVersion {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Manifest == nil {
		return nil
	}

	for _, v := range m.Manifest.Versions {
		if v.ID.Equal(version) {
			copy := v // Make a copy
			return &copy
		}
	}
	return nil
}

// GetLastBleveIndexed returns the last version whose docs were fully indexed in Bleve.
// Returns the zero SemanticVersion if not yet set.
func (m *GlobalMeta) GetLastBleveIndexed() SemanticVersion {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.LastBleveIndexedVersion == nil {
		return SemanticVersion{}
	}
	return *m.LastBleveIndexedVersion
}

// SetLastBleveIndexed records the version whose Bleve indexing completed and persists.
func (m *GlobalMeta) SetLastBleveIndexed(v SemanticVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return ErrMetaNotLoaded
	}

	old := m.LastBleveIndexedVersion
	m.LastBleveIndexedVersion = &v

	if err := m.saveUnlocked(); err != nil {
		m.LastBleveIndexedVersion = old
		return fmt.Errorf("sylkdir: failed to persist last bleve indexed version: %w", err)
	}
	return nil
}
