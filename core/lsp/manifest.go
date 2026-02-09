package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// InstalledServer records metadata about one installed LSP server.
type InstalledServer struct {
	ServerID    ServerID  `json:"server_id"`
	Version     string    `json:"version"`
	Source      string    `json:"source"`
	BinaryPath  string    `json:"binary_path"`
	InstalledAt time.Time `json:"installed_at"`
	Checksum    string    `json:"checksum"`
}

// Manifest tracks all LSP servers installed by Sylk.
// Persisted to manifest.json in the LSP base directory.
type Manifest struct {
	Servers map[ServerID]*InstalledServer `json:"servers"`

	mu   sync.RWMutex `json:"-"`
	path string       `json:"-"`
}

// NewManifest creates a Manifest that persists to dir/manifest.json.
func NewManifest(dir string) *Manifest {
	return &Manifest{
		Servers: make(map[ServerID]*InstalledServer),
		path:    filepath.Join(dir, "manifest.json"),
	}
}

// Load reads the manifest from disk. Returns nil if the file does not exist.
func (m *Manifest) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	var loaded Manifest
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}

	if loaded.Servers != nil {
		m.Servers = loaded.Servers
	}
	return nil
}

// Save writes the manifest to disk atomically via tmp+rename.
func (m *Manifest) Save() error {
	m.mu.RLock()
	data, err := json.MarshalIndent(m, "", "  ")
	m.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write manifest tmp: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}

// Get retrieves an installed server entry by ID. Returns nil if not found.
func (m *Manifest) Get(id ServerID) *InstalledServer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Servers[id]
}

// Set adds or updates an installed server entry.
func (m *Manifest) Set(srv *InstalledServer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Servers[srv.ServerID] = srv
}

// Remove deletes an installed server entry.
func (m *Manifest) Remove(id ServerID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Servers, id)
}
