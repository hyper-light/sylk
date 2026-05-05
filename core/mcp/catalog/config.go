// Package catalog manages the layered MCP server configuration:
//
//	user-global (~/.sylk/mcp/servers.yaml)
//	    -> project-local (.sylk/local/mcp_servers.yaml)
//	    -> session-scoped overrides
//	    -> effective server view
//
// The catalog is hot-reloadable via fsnotify on each layer's source file;
// downstream consumers subscribe to a Watch channel for invalidations.
package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// TransportKind enumerates supported transports.
type TransportKind string

const (
	TransportStdio     TransportKind = "stdio"
	TransportHTTP      TransportKind = "http"
	TransportSSE       TransportKind = "sse"
	TransportInProcess TransportKind = "inprocess"
)

// ServerSpec is the on-disk shape of one MCP server definition.
type ServerSpec struct {
	ID                 string            `yaml:"id"`
	Disabled           bool              `yaml:"disabled,omitempty"`
	Transport          TransportKind     `yaml:"transport"`
	Command            string            `yaml:"command,omitempty"`
	Args               []string          `yaml:"args,omitempty"`
	Env                map[string]string `yaml:"env,omitempty"`
	WorkDir            string            `yaml:"work_dir,omitempty"`
	Endpoint           string            `yaml:"endpoint,omitempty"`
	Headers            map[string]string `yaml:"headers,omitempty"`
	CredentialRef      string            `yaml:"credential_ref,omitempty"`
	Domains            []string          `yaml:"domains,omitempty"`
	TrustedReadOnly    bool              `yaml:"trusted_read_only,omitempty"`
	WorkspaceAffinity  string            `yaml:"workspace_affinity,omitempty"`
	PriorityBoost      int               `yaml:"priority_boost,omitempty"`
	AllowTools         []string          `yaml:"allow_tools,omitempty"`
	DenyTools          []string          `yaml:"deny_tools,omitempty"`
	ServerInstructions string            `yaml:"server_instructions,omitempty"`
}

// File is the on-disk wrapper around a list of server specs.
type File struct {
	Servers []ServerSpec `yaml:"servers"`
}

// EffectiveCatalog is the merged view across layers. Indexed by server id
// so lookups are O(1).
type EffectiveCatalog struct {
	Servers map[string]ServerSpec
}

// Layer enumerates merge precedence (lowest first).
type Layer int

const (
	LayerGlobal Layer = iota
	LayerProject
	LayerSession
)

// Manager owns layered configuration, hot-reload, and notification fan-out.
type Manager struct {
	mu        sync.RWMutex
	layers    map[Layer]*File
	effective *EffectiveCatalog

	// fsnotify-equivalent: we re-read on demand and on a periodic
	// re-scan. Keeping the implementation in-process avoids a hard
	// fsnotify dependency for the catalog (the package already imports
	// fsnotify elsewhere; reusing it is safe but adds churn).
	pathByLayer map[Layer]string
	mtimeByPath map[string]time.Time

	subMu  sync.Mutex
	subs   []chan struct{}
}

// NewManager constructs a catalog manager. Paths may be empty: an empty
// path means that layer is not configured.
func NewManager() *Manager {
	return &Manager{
		layers:      map[Layer]*File{},
		pathByLayer: map[Layer]string{},
		mtimeByPath: map[string]time.Time{},
	}
}

// LoadGlobal reads the global servers file (or no-ops if path is empty / missing).
func (m *Manager) LoadGlobal(path string) error {
	return m.loadLayer(LayerGlobal, path)
}

// LoadProject reads the project-local servers file.
func (m *Manager) LoadProject(path string) error {
	return m.loadLayer(LayerProject, path)
}

// LoadSession sets the session-scoped overlay programmatically (no on-disk file).
func (m *Manager) LoadSession(file *File) {
	m.mu.Lock()
	m.layers[LayerSession] = file
	m.mu.Unlock()
	m.recompute()
	m.notify()
}

func (m *Manager) loadLayer(layer Layer, path string) error {
	if path == "" {
		m.mu.Lock()
		delete(m.layers, layer)
		delete(m.pathByLayer, layer)
		m.mu.Unlock()
		m.recompute()
		m.notify()
		return nil
	}
	file, err := readFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	m.mu.Lock()
	if file != nil {
		m.layers[layer] = file
	} else {
		delete(m.layers, layer)
	}
	m.pathByLayer[layer] = path
	m.recordMtime(path)
	m.mu.Unlock()
	m.recompute()
	m.notify()
	return nil
}

func (m *Manager) recordMtime(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	m.mtimeByPath[path] = info.ModTime()
}

func readFile(path string) (*File, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(bytes) == 0 {
		return &File{}, nil
	}
	var file File
	if err := yaml.Unmarshal(bytes, &file); err != nil {
		return nil, fmt.Errorf("mcp catalog: parse %s: %w", path, err)
	}
	return &file, nil
}

func (m *Manager) recompute() {
	m.mu.Lock()
	defer m.mu.Unlock()
	merged := map[string]ServerSpec{}
	for _, layer := range []Layer{LayerGlobal, LayerProject, LayerSession} {
		file, ok := m.layers[layer]
		if !ok || file == nil {
			continue
		}
		for _, spec := range file.Servers {
			id := strings.TrimSpace(spec.ID)
			if id == "" {
				continue
			}
			merged[id] = spec
		}
	}
	m.effective = &EffectiveCatalog{Servers: merged}
}

// Effective returns a snapshot of the merged server view.
func (m *Manager) Effective() *EffectiveCatalog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.effective == nil {
		return &EffectiveCatalog{Servers: map[string]ServerSpec{}}
	}
	out := make(map[string]ServerSpec, len(m.effective.Servers))
	for k, v := range m.effective.Servers {
		out[k] = v
	}
	return &EffectiveCatalog{Servers: out}
}

// EnabledIDs returns the sorted list of enabled server ids.
func (m *Manager) EnabledIDs() []string {
	cat := m.Effective()
	ids := make([]string, 0, len(cat.Servers))
	for id, spec := range cat.Servers {
		if spec.Disabled {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Watch returns a channel that receives a tick on every catalog change.
// The channel is buffered (1) and uses a non-blocking send so the publisher
// never stalls. Subscribers are responsible for draining promptly.
func (m *Manager) Watch() <-chan struct{} {
	ch := make(chan struct{}, 1)
	m.subMu.Lock()
	m.subs = append(m.subs, ch)
	m.subMu.Unlock()
	return ch
}

func (m *Manager) notify() {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for _, ch := range m.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Reload re-reads each on-disk layer if its mtime has changed. Returns true
// when the catalog actually changed.
func (m *Manager) Reload() (bool, error) {
	m.mu.RLock()
	paths := make(map[Layer]string, len(m.pathByLayer))
	for k, v := range m.pathByLayer {
		paths[k] = v
	}
	m.mu.RUnlock()
	changed := false
	for layer, path := range paths {
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return false, err
		}
		if !m.mtimeChanged(path, info.ModTime()) {
			continue
		}
		if err := m.loadLayer(layer, path); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func (m *Manager) mtimeChanged(path string, current time.Time) bool {
	m.mu.RLock()
	prev, ok := m.mtimeByPath[path]
	m.mu.RUnlock()
	return !ok || !prev.Equal(current)
}

// DefaultGlobalPath returns the conventional global catalog path.
func DefaultGlobalPath(homeDir string) string {
	return filepath.Join(homeDir, ".sylk", "mcp", "servers.yaml")
}

// DefaultProjectPath returns the conventional project-local catalog path.
func DefaultProjectPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".sylk", "local", "mcp_servers.yaml")
}
