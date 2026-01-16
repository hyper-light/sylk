package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// =============================================================================
// Persister Interface
// =============================================================================

// Persister handles session persistence
type Persister interface {
	// Save persists a session
	Save(session *Session) error

	// Load loads a session by ID
	Load(id string) (*Session, error)

	// LoadAll loads all persisted sessions
	LoadAll() ([]*Session, error)

	// Delete removes a persisted session
	Delete(id string) error

	// List returns all persisted session IDs
	List() ([]string, error)
}

// =============================================================================
// File Persister
// =============================================================================

// FilePersister persists sessions to the filesystem
type FilePersister struct {
	mu      sync.RWMutex
	baseDir string
}

// NewFilePersister creates a new file persister
func NewFilePersister(baseDir string) (*FilePersister, error) {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create persistence directory: %w", err)
	}

	return &FilePersister{
		baseDir: baseDir,
	}, nil
}

// sessionPath returns the path for a session file
func (p *FilePersister) sessionPath(id string) string {
	return filepath.Join(p.baseDir, id+".json")
}

// Save persists a session to disk
func (p *FilePersister) Save(session *Session) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := session.Serialize()
	if err != nil {
		return err
	}

	path := p.sessionPath(session.ID())

	// Write to temp file first, then rename (atomic)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename session file: %w", err)
	}

	return nil
}

// Load loads a session from disk
func (p *FilePersister) Load(id string) (*Session, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	path := p.sessionPath(id)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	return DeserializeSession(data)
}

// LoadAll loads all persisted sessions
func (p *FilePersister) LoadAll() ([]*Session, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	entries, err := p.readEntries()
	if err != nil {
		return nil, err
	}

	var sessions []*Session

	for _, entry := range entries {
		if p.shouldSkipEntry(entry) {
			continue
		}

		session, id, err := p.loadSessionEntry(entry)
		if err != nil {
			continue
		}

		if session.ID() != id {
			continue
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

func (p *FilePersister) readEntries() ([]os.DirEntry, error) {
	entries, err := os.ReadDir(p.baseDir)
	if err == nil {
		return entries, nil
	}
	if os.IsNotExist(err) {
		return nil, nil
	}
	return nil, fmt.Errorf("failed to read persistence directory: %w", err)
}

func (p *FilePersister) shouldSkipEntry(entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	return filepath.Ext(entry.Name()) != ".json"
}

func (p *FilePersister) loadSessionEntry(entry os.DirEntry) (*Session, string, error) {
	name := entry.Name()
	id := name[:len(name)-5]
	path := filepath.Join(p.baseDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	child, err := DeserializeSession(data)
	if err != nil {
		return nil, "", err
	}
	return child, id, nil
}

// Delete removes a persisted session
func (p *FilePersister) Delete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	path := p.sessionPath(id)

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete session file: %w", err)
	}

	return nil
}

// List returns all persisted session IDs
func (p *FilePersister) List() ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	entries, err := os.ReadDir(p.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read persistence directory: %w", err)
	}

	var ids []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}

		// Remove .json extension to get ID
		id := name[:len(name)-5]
		ids = append(ids, id)
	}

	return ids, nil
}

// =============================================================================
// Memory Persister (for testing)
// =============================================================================

// MemoryPersister stores sessions in memory (for testing)
type MemoryPersister struct {
	mu       sync.RWMutex
	sessions map[string][]byte
}

// NewMemoryPersister creates a new memory persister
func NewMemoryPersister() *MemoryPersister {
	return &MemoryPersister{
		sessions: make(map[string][]byte),
	}
}

// Save stores a session in memory
func (p *MemoryPersister) Save(session *Session) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := session.Serialize()
	if err != nil {
		return err
	}

	p.sessions[session.ID()] = data
	return nil
}

// Load loads a session from memory
func (p *MemoryPersister) Load(id string) (*Session, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	data, ok := p.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}

	return DeserializeSession(data)
}

// LoadAll loads all sessions from memory
func (p *MemoryPersister) LoadAll() ([]*Session, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var sessions []*Session

	for _, data := range p.sessions {
		session, err := DeserializeSession(data)
		if err != nil {
			continue
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}

// Delete removes a session from memory
func (p *MemoryPersister) Delete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.sessions, id)
	return nil
}

// List returns all session IDs in memory
func (p *MemoryPersister) List() ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ids := make([]string, 0, len(p.sessions))
	for id := range p.sessions {
		ids = append(ids, id)
	}

	return ids, nil
}
