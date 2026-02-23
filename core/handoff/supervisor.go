package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// SupervisorConfig configures the HandoffSupervisor.
type SupervisorConfig struct {
	WALDir         string `json:"wal_dir"`
	EnableWAL      bool   `json:"enable_wal"`
	EnableLearning bool   `json:"enable_learning"`
}

// DefaultSupervisorConfig returns a config with WAL and learning enabled.
func DefaultSupervisorConfig() *SupervisorConfig {
	return &SupervisorConfig{
		WALDir:         "",
		EnableWAL:      true,
		EnableLearning: true,
	}
}

// HandoffSupervisor manages shared handoff state across all agent bridges.
// It owns the prior hierarchy, profile learner, WAL, factory, and bridge registry.
// Each agent registers with the supervisor to get its own HandoffBridge.
type HandoffSupervisor struct {
	mu sync.RWMutex

	config           *SupervisorConfig
	priorHierarchy   *PriorHierarchy
	profileLearner   *ProfileLearner
	baselineRegistry *BaselineRegistry
	wal              *HandoffWAL
	factory          *AgentFactory
	factoryAdapter   *FactorySessionAdapter
	descriptors      *DescriptorRegistry
	bridges          map[string]*HandoffBridge

	onAgentReplaced func(oldID, newID string, newAgent HandoffableAgent) error

	started atomic.Bool
}

// NewHandoffSupervisor creates a new supervisor with the given config.
func NewHandoffSupervisor(config *SupervisorConfig) *HandoffSupervisor {
	if config == nil {
		config = DefaultSupervisorConfig()
	}

	descriptors := NewDescriptorRegistry()
	factory := NewAgentFactory(descriptors)
	adapter := NewFactorySessionAdapter(factory)
	hierarchy := NewPriorHierarchy()

	learnerCfg := DefaultProfileLearnerConfig()
	learnerCfg.EnableHierarchicalFallback = config.EnableLearning
	learner := NewProfileLearner(learnerCfg, hierarchy)

	// halfLife = gpDefaultMaxObservations / 2. EWMA tracks the same
	// temporal horizon as the GP observation window (100).
	const gpDefaultMaxObservations = 100
	halfLife := float64(gpDefaultMaxObservations) / 2.0
	baselineReg := NewBaselineRegistry(halfLife)

	return &HandoffSupervisor{
		config:           config,
		priorHierarchy:   hierarchy,
		profileLearner:   learner,
		baselineRegistry: baselineReg,
		factory:          factory,
		factoryAdapter:   adapter,
		descriptors:      descriptors,
		bridges:          make(map[string]*HandoffBridge),
	}
}

// Start initializes shared components. If WAL is enabled and the WAL
// directory exists, it opens the WAL and recovers persisted state.
func (s *HandoffSupervisor) Start() error {
	if s.started.Swap(true) {
		return nil
	}

	if s.config.EnableWAL && s.config.WALDir != "" {
		if err := os.MkdirAll(s.config.WALDir, 0755); err != nil {
			s.started.Store(false)
			return fmt.Errorf("create WAL dir: %w", err)
		}

		walPath := filepath.Join(s.config.WALDir, "handoff.wal")
		wal, err := NewHandoffWAL(walPath, nil)
		if err != nil {
			s.started.Store(false)
			return fmt.Errorf("open WAL: %w", err)
		}
		s.wal = wal

		// Recover state from WAL.
		if err := s.recoverFromWAL(); err != nil {
			_ = wal.Close()
			s.wal = nil
			s.started.Store(false)
			return fmt.Errorf("WAL recovery: %w", err)
		}
	}

	return nil
}

// Stop shuts down all bridges, persists final state to the WAL, and closes it.
func (s *HandoffSupervisor) Stop() error {
	if !s.started.Swap(false) {
		return nil
	}

	s.mu.Lock()
	bridges := make([]*HandoffBridge, 0, len(s.bridges))
	for _, b := range s.bridges {
		bridges = append(bridges, b)
	}
	s.mu.Unlock()

	// Stop all bridges.
	for _, b := range bridges {
		_ = b.Stop()
	}

	// Final checkpoint.
	if s.wal != nil {
		s.mu.RLock()
		for _, b := range s.bridges {
			_ = s.wal.WriteCheckpoint(b.profile, b.gp)
		}
		s.mu.RUnlock()
		return s.wal.Close()
	}

	return nil
}

// RegisterAgent creates a per-agent HandoffBridge and starts it.
// The bridge is configured based on the agent's descriptor.
func (s *HandoffSupervisor) RegisterAgent(agent HandoffableAgent) (*HandoffBridge, error) {
	if !s.started.Load() {
		return nil, fmt.Errorf("supervisor not started")
	}

	desc, ok := s.descriptors.Get(agent.AgentType())
	if !ok {
		// Register with a reasonable default.
		desc = agent.Descriptor()
		s.descriptors.Register(desc)
	}

	cfg := BridgeConfigForAgent(desc)
	bridge := NewHandoffBridge(cfg, agent, s)

	if err := bridge.Start(); err != nil {
		return nil, fmt.Errorf("start bridge for %q: %w", agent.AgentID(), err)
	}

	s.mu.Lock()
	s.bridges[agent.AgentID()] = bridge
	s.mu.Unlock()

	return bridge, nil
}

// UnregisterAgent stops and removes the bridge for the given agent ID.
func (s *HandoffSupervisor) UnregisterAgent(agentID string) error {
	s.mu.Lock()
	bridge, ok := s.bridges[agentID]
	if ok {
		delete(s.bridges, agentID)
	}
	s.mu.Unlock()

	if !ok {
		return nil
	}

	return bridge.Stop()
}

// GetBridge returns the bridge for the given agent ID, or nil.
func (s *HandoffSupervisor) GetBridge(agentID string) *HandoffBridge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bridges[agentID]
}

// Factory returns the agent factory.
func (s *HandoffSupervisor) Factory() *AgentFactory {
	return s.factory
}

// Descriptors returns the descriptor registry.
func (s *HandoffSupervisor) Descriptors() *DescriptorRegistry {
	return s.descriptors
}

// SetAgentReplacedCallback sets the callback invoked when an agent is
// replaced during a handoff. The callback should unregister the old agent
// from the guide and register the new one.
func (s *HandoffSupervisor) SetAgentReplacedCallback(fn func(string, string, HandoffableAgent) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAgentReplaced = fn
}

// BridgeCount returns the number of active bridges.
func (s *HandoffSupervisor) BridgeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.bridges)
}

// recoverFromWAL replays the WAL to restore profile and GP state into
// the profile learner and prior hierarchy.
func (s *HandoffSupervisor) recoverFromWAL() error {
	if s.wal == nil {
		return nil
	}

	state, err := s.wal.Recover()
	if err != nil {
		return err
	}

	// Restore the recovered profile into the learner if it has meaningful data.
	if state.Profile != nil && state.Profile.EffectiveSamples > 0 {
		s.profileLearner.RestoreProfile(state.Profile)
	}

	s.recoverOrphanedOverlaps()

	return nil
}

// recoverOrphanedOverlaps checks for in-progress overlaps after WAL recovery
// and aborts them. A crash during overlap means the old agent survived but
// the new agent doesn't exist — the overlap must be abandoned.
func (s *HandoffSupervisor) recoverOrphanedOverlaps() {
	if s.wal == nil {
		return
	}

	entries, err := s.wal.LoadEntries()
	if err != nil {
		return
	}

	// Find unmatched overlap_begin entries (no corresponding complete/abort).
	// Track by old_agent_id to detect orphans.
	pending := make(map[string]*OverlapWALEntry)

	for i := range entries {
		entry := &entries[i]
		if entry.Overlap == nil {
			continue
		}

		switch entry.EntryType {
		case EntryTypeOverlapBegin:
			pending[entry.Overlap.OldAgentID] = entry.Overlap
		case EntryTypeOverlapComplete, EntryTypeOverlapAbort:
			delete(pending, entry.Overlap.OldAgentID)
		}
	}

	// Abort any orphaned overlaps.
	for _, overlap := range pending {
		_ = s.wal.WriteOverlapEvent(OverlapWALEntry{
			Phase:       OverlapAborted,
			OldAgentID:  overlap.OldAgentID,
			NewAgentID:  overlap.NewAgentID,
			SnapshotSeq: overlap.SnapshotSeq,
			Timestamp:   time.Now(),
			Error:       "recovered from crash — overlap abandoned",
		})
	}
}
