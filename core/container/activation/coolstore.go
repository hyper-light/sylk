package activation

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/handoff"
)

var (
	ErrCoolStoreNotFound = errors.New("cool store entry not found")
	ErrCoolStoreFull     = errors.New("cool store at capacity")
)

// CoolStore persists serialized container state on disk for the Cool tier.
// Each agent type is stored as a separate JSON file in the configured directory.
type CoolStore struct {
	mu       sync.Mutex
	dir      string
	entries  map[string]struct{} // tracks which agent types have files
	capacity int
	logger   *slog.Logger
}

// CoolEntry is the on-disk representation of a Cool-tier agent.
type CoolEntry struct {
	Spec  container.ContainerSpec  `json:"spec"`
	State *handoff.ArchivableState `json:"state,omitempty"`
}

// NewCoolStore creates a store backed by the given directory. The directory
// is created if it does not exist.
func NewCoolStore(dir string, capacity int) (*CoolStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &CoolStore{
		dir:      dir,
		entries:  make(map[string]struct{}, capacity),
		capacity: capacity,
		logger:   slog.Default(),
	}, nil
}

// SetLogger replaces the logger. Must be called before concurrent use.
func (cs *CoolStore) SetLogger(logger *slog.Logger) {
	cs.logger = logger
}

// Store serializes container state to disk. Returns ErrCoolStoreFull if
// the store is at capacity and the agent type is not already present.
func (cs *CoolStore) Store(agentType string, spec container.ContainerSpec, state *handoff.ArchivableState) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, exists := cs.entries[agentType]; !exists && len(cs.entries) >= cs.capacity {
		return ErrCoolStoreFull
	}

	entry := CoolEntry{Spec: spec, State: state}
	data, err := json.Marshal(&entry)
	if err != nil {
		return err
	}

	path := cs.pathFor(agentType)
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return err
	}

	cs.entries[agentType] = struct{}{}
	return nil
}

// Load deserializes container state from disk.
func (cs *CoolStore) Load(agentType string) (*CoolEntry, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, ok := cs.entries[agentType]; !ok {
		return nil, ErrCoolStoreNotFound
	}

	data, err := os.ReadFile(cs.pathFor(agentType))
	if err != nil {
		return nil, err
	}

	var entry CoolEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		// Corrupt file — remove tracking entry and attempt file cleanup.
		delete(cs.entries, agentType)
		if removeErr := os.Remove(cs.pathFor(agentType)); removeErr != nil {
			cs.logger.Warn("failed to remove corrupt cool store file",
				"agent_type", agentType,
				"error", removeErr,
			)
		}
		return nil, err
	}
	return &entry, nil
}

// Remove deletes stored state for the given agent type.
func (cs *CoolStore) Remove(agentType string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, ok := cs.entries[agentType]; !ok {
		return nil
	}
	delete(cs.entries, agentType)
	return os.Remove(cs.pathFor(agentType))
}

// Contains reports whether the store has an entry for the given type.
func (cs *CoolStore) Contains(agentType string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	_, ok := cs.entries[agentType]
	return ok
}

// Len returns the number of entries in the store.
func (cs *CoolStore) Len() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.entries)
}

func (cs *CoolStore) pathFor(agentType string) string {
	return filepath.Join(cs.dir, agentType+".json")
}
