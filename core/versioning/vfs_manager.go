package versioning

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrVFSNotFound          = errors.New("VFS not found")
	ErrVariantGroupExists   = errors.New("variant group already exists")
	ErrVariantGroupNotFound = errors.New("variant group not found")
	ErrVariantExists        = errors.New("variant already exists")
	ErrVariantNotFound      = errors.New("variant not found")
	ErrNoVariantsInGroup    = errors.New("no variants in group")
)

type VFSManagerConfig struct {
	BaseDir      string
	VersionStore VersionStore
	BlobStore    BlobStore
}

type VFSManager interface {
	CreatePipelineVFS(cfg VFSConfig) (*PipelineVFS, error)
	GetPipelineVFS(pipelineID string) (*PipelineVFS, error)
	ClosePipelineVFS(pipelineID string) error

	CreateVariantGroup(cfg VariantGroupConfig) (*VariantGroup, error)
	GetVariantGroup(groupID string) (*VariantGroup, error)
	AddVariantToGroup(groupID, variantID string, cfg VFSConfig) error
	SelectVariant(groupID, variantID string) error
	CancelVariantGroup(groupID string) error

	GetSessionVFSes(sessionID SessionID) []*PipelineVFS
	CleanupSession(sessionID SessionID) error
	CleanupStaging(pipelineID string) error

	Close() error
}

type VariantGroupConfig struct {
	GroupID     string
	SessionID   SessionID
	BasePath    string
	Description string
}

type VariantGroup struct {
	mu          sync.RWMutex
	ID          string
	SessionID   SessionID
	BasePath    string
	Description string
	Variants    map[string]*PipelineVFS
	Selected    string
	CreatedAt   time.Time
	Committed   bool
}

type MemoryVFSManager struct {
	mu            sync.RWMutex
	config        VFSManagerConfig
	vfses         map[string]*PipelineVFS
	variantGroups map[string]*VariantGroup
	sessionIndex  map[SessionID][]string
	closed        bool
}

func NewMemoryVFSManager(cfg VFSManagerConfig) *MemoryVFSManager {
	return &MemoryVFSManager{
		config:        cfg,
		vfses:         make(map[string]*PipelineVFS),
		variantGroups: make(map[string]*VariantGroup),
		sessionIndex:  make(map[SessionID][]string),
	}
}

func (m *MemoryVFSManager) CreatePipelineVFS(cfg VFSConfig) (*PipelineVFS, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, ErrVFSClosed
	}

	if cfg.StagingDir == "" {
		cfg.StagingDir = m.createStagingDir(cfg.PipelineID)
	}

	vfs := NewPipelineVFS(cfg, m.config.VersionStore, m.config.BlobStore)
	m.vfses[cfg.PipelineID] = vfs
	m.addToSessionIndex(cfg.SessionID, cfg.PipelineID)

	return vfs, nil
}

func (m *MemoryVFSManager) createStagingDir(pipelineID string) string {
	if m.config.BaseDir == "" {
		return ""
	}
	dir := filepath.Join(m.config.BaseDir, "staging", pipelineID)
	os.MkdirAll(dir, 0755)
	return dir
}

func (m *MemoryVFSManager) addToSessionIndex(sessionID SessionID, pipelineID string) {
	m.sessionIndex[sessionID] = append(m.sessionIndex[sessionID], pipelineID)
}

func (m *MemoryVFSManager) GetPipelineVFS(pipelineID string) (*PipelineVFS, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrVFSClosed
	}

	vfs, ok := m.vfses[pipelineID]
	if !ok {
		return nil, ErrVFSNotFound
	}
	return vfs, nil
}

func (m *MemoryVFSManager) ClosePipelineVFS(pipelineID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrVFSClosed
	}

	vfs, ok := m.vfses[pipelineID]
	if !ok {
		return ErrVFSNotFound
	}

	if err := vfs.Close(); err != nil {
		return err
	}

	delete(m.vfses, pipelineID)
	m.removeFromSessionIndex(vfs.config.SessionID, pipelineID)

	return nil
}

func (m *MemoryVFSManager) removeFromSessionIndex(sessionID SessionID, pipelineID string) {
	pipelineIDs := m.sessionIndex[sessionID]
	filtered := make([]string, 0, len(pipelineIDs))
	for _, id := range pipelineIDs {
		if id != pipelineID {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		delete(m.sessionIndex, sessionID)
	} else {
		m.sessionIndex[sessionID] = filtered
	}
}

func (m *MemoryVFSManager) CreateVariantGroup(cfg VariantGroupConfig) (*VariantGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, ErrVFSClosed
	}

	if _, exists := m.variantGroups[cfg.GroupID]; exists {
		return nil, ErrVariantGroupExists
	}

	group := &VariantGroup{
		ID:          cfg.GroupID,
		SessionID:   cfg.SessionID,
		BasePath:    cfg.BasePath,
		Description: cfg.Description,
		Variants:    make(map[string]*PipelineVFS),
		CreatedAt:   time.Now(),
	}

	m.variantGroups[cfg.GroupID] = group
	return group, nil
}

func (m *MemoryVFSManager) GetVariantGroup(groupID string) (*VariantGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrVFSClosed
	}

	group, ok := m.variantGroups[groupID]
	if !ok {
		return nil, ErrVariantGroupNotFound
	}
	return group, nil
}

func (m *MemoryVFSManager) AddVariantToGroup(groupID, variantID string, cfg VFSConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrVFSClosed
	}

	group, ok := m.variantGroups[groupID]
	if !ok {
		return ErrVariantGroupNotFound
	}

	return m.addVariantLocked(group, variantID, cfg)
}

func (m *MemoryVFSManager) addVariantLocked(group *VariantGroup, variantID string, cfg VFSConfig) error {
	group.mu.Lock()
	defer group.mu.Unlock()

	if _, exists := group.Variants[variantID]; exists {
		return ErrVariantExists
	}

	cfg.StagingDir = m.createVariantStagingDir(group.ID, variantID)
	vfs := NewPipelineVFS(cfg, m.config.VersionStore, m.config.BlobStore)

	group.Variants[variantID] = vfs
	m.vfses[cfg.PipelineID] = vfs
	m.addToSessionIndex(cfg.SessionID, cfg.PipelineID)

	return nil
}

func (m *MemoryVFSManager) createVariantStagingDir(groupID, variantID string) string {
	if m.config.BaseDir == "" {
		return ""
	}
	dir := filepath.Join(m.config.BaseDir, "variants", groupID, variantID)
	os.MkdirAll(dir, 0755)
	return dir
}

func (m *MemoryVFSManager) SelectVariant(groupID, variantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrVFSClosed
	}

	group, ok := m.variantGroups[groupID]
	if !ok {
		return ErrVariantGroupNotFound
	}

	return m.selectVariantLocked(group, variantID)
}

func (m *MemoryVFSManager) selectVariantLocked(group *VariantGroup, variantID string) error {
	group.mu.Lock()
	defer group.mu.Unlock()

	if _, exists := group.Variants[variantID]; !exists {
		return ErrVariantNotFound
	}

	group.Selected = variantID
	group.Committed = true

	return m.cleanupUnselectedVariants(group, variantID)
}

func (m *MemoryVFSManager) cleanupUnselectedVariants(group *VariantGroup, selectedID string) error {
	for id, vfs := range group.Variants {
		if id == selectedID {
			continue
		}
		vfs.Close()
		delete(m.vfses, vfs.config.PipelineID)
	}
	return nil
}

func (m *MemoryVFSManager) CancelVariantGroup(groupID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrVFSClosed
	}

	group, ok := m.variantGroups[groupID]
	if !ok {
		return ErrVariantGroupNotFound
	}

	return m.cancelGroupLocked(group, groupID)
}

func (m *MemoryVFSManager) cancelGroupLocked(group *VariantGroup, groupID string) error {
	group.mu.Lock()
	defer group.mu.Unlock()

	for _, vfs := range group.Variants {
		vfs.Close()
		delete(m.vfses, vfs.config.PipelineID)
	}

	delete(m.variantGroups, groupID)
	return nil
}

func (m *MemoryVFSManager) GetSessionVFSes(sessionID SessionID) []*PipelineVFS {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pipelineIDs := m.sessionIndex[sessionID]
	vfses := make([]*PipelineVFS, 0, len(pipelineIDs))

	for _, id := range pipelineIDs {
		if vfs, ok := m.vfses[id]; ok {
			vfses = append(vfses, vfs)
		}
	}
	return vfses
}

func (m *MemoryVFSManager) CleanupSession(sessionID SessionID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrVFSClosed
	}

	pipelineIDs := m.sessionIndex[sessionID]
	return m.cleanupPipelines(pipelineIDs)
}

func (m *MemoryVFSManager) cleanupPipelines(pipelineIDs []string) error {
	for _, id := range pipelineIDs {
		if vfs, ok := m.vfses[id]; ok {
			vfs.Close()
			delete(m.vfses, id)
		}
	}
	return nil
}

func (m *MemoryVFSManager) CleanupStaging(pipelineID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return ErrVFSClosed
	}

	vfs, ok := m.vfses[pipelineID]
	if !ok {
		return ErrVFSNotFound
	}

	stagingDir := vfs.config.StagingDir
	if stagingDir != "" {
		return os.RemoveAll(stagingDir)
	}
	return nil
}

func (m *MemoryVFSManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}

	m.closeAllVFSes()
	m.closeAllVariantGroups()

	m.closed = true
	return nil
}

func (m *MemoryVFSManager) closeAllVFSes() {
	for _, vfs := range m.vfses {
		vfs.Close()
	}
	m.vfses = make(map[string]*PipelineVFS)
}

func (m *MemoryVFSManager) closeAllVariantGroups() {
	for _, group := range m.variantGroups {
		group.mu.Lock()
		for _, vfs := range group.Variants {
			vfs.Close()
		}
		group.mu.Unlock()
	}
	m.variantGroups = make(map[string]*VariantGroup)
}

func (m *MemoryVFSManager) Stats() VFSManagerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return VFSManagerStats{
		ActiveVFSes:    len(m.vfses),
		VariantGroups:  len(m.variantGroups),
		ActiveSessions: len(m.sessionIndex),
		TotalPipelines: m.countTotalPipelines(),
	}
}

func (m *MemoryVFSManager) countTotalPipelines() int {
	count := 0
	for _, ids := range m.sessionIndex {
		count += len(ids)
	}
	return count
}

type VFSManagerStats struct {
	ActiveVFSes    int
	VariantGroups  int
	ActiveSessions int
	TotalPipelines int
}

func (g *VariantGroup) GetVariant(variantID string) (*PipelineVFS, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	vfs, ok := g.Variants[variantID]
	if !ok {
		return nil, ErrVariantNotFound
	}
	return vfs, nil
}

func (g *VariantGroup) ListVariants() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ids := make([]string, 0, len(g.Variants))
	for id := range g.Variants {
		ids = append(ids, id)
	}
	return ids
}

func (g *VariantGroup) IsCommitted() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.Committed
}

func (g *VariantGroup) GetSelected() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.Selected
}

func (g *VariantGroup) VariantCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.Variants)
}
