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
	SessionID    uint32    `json:"session_id"`
	FinalVersion uint32    `json:"final_version"`
	CommittedAt  time.Time `json:"committed_at"`
}

// GlobalMeta manages the global metadata stored in knowledge/meta.json.
// It provides atomic ID allocation and session commit registration.
type GlobalMeta struct {
	// SchemaVersion indicates the data format version for migrations.
	SchemaVersion int `json:"schema_version"`
	// NextNodeID is the next available node ID (atomically incremented).
	NextNodeID uint32 `json:"next_node_id"`
	// NextSessionID is the next available session ID (atomically incremented).
	NextSessionID uint32 `json:"next_session_id"`
	// CommittedSessions lists all sessions merged into global state.
	CommittedSessions []CommittedSession `json:"committed_sessions"`

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
		SchemaVersion:     1,
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
// This is called after successfully merging session data.
func (m *GlobalMeta) RegisterCommit(sessionID uint32, finalVersion uint32) error {
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

	m.CommittedSessions = append(m.CommittedSessions, CommittedSession{
		SessionID:    sessionID,
		FinalVersion: finalVersion,
		CommittedAt:  time.Now().UTC(),
	})

	if err := m.saveUnlocked(); err != nil {
		// Rollback on save failure
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
