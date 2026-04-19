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
	paramBlender     *HierarchicalParamBlender
	baselineRegistry *BaselineRegistry
	wal              *HandoffWAL
	factory          *AgentFactory
	factoryAdapter   *FactorySessionAdapter
	descriptors      *DescriptorRegistry
	bridges          map[string]*HandoffBridge

	onAgentReplaced func(oldID, newID string, newAgent HandoffableAgent) error

	// Quality publisher — propagated to all bridges.
	qualityPublisher func(agentID string, quality, stdDev float64)

	// Brief source — propagated to all bridges so handoffs can request
	// Archivalist-backed summaries instead of falling back to raw snapshots.
	briefSource BriefSource

	// archivePublisher — HAND-05. Fire-and-forget sink for
	// ArchiveRequests the bridge emits at lifecycle transitions. When
	// unset, the bridge skips archival (cold-start / test paths).
	archivePublisher ArchivePublisher

	// Traffic shift callbacks — wired by bootstrap.
	onShiftBegin func(oldID string, newAgent HandoffableAgent)
	updateWeight func(agentID string, weight float64)

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
		paramBlender:     NewHierarchicalParamBlender(nil),
		baselineRegistry: baselineReg,
		factory:          factory,
		factoryAdapter:   adapter,
		descriptors:      descriptors,
		bridges:          make(map[string]*HandoffBridge),
	}
}

// HAND-02 parameter key constants. The blender stores *LearnedWeight
// values keyed by ParamName — we define canonical names so lookups
// and updates agree across the codebase.
const (
	// BlenderParamHandoffThreshold names the learned handoff threshold
	// posterior in the blender. Profile.OptimalHandoffThreshold is the
	// per-(agent,model) source; the blender maintains the hierarchical
	// view across instance / agent-model / model / global levels.
	BlenderParamHandoffThreshold = "handoff_threshold"
)

// Blender returns the supervisor's HierarchicalParamBlender.
// Nil is never returned — the supervisor always owns a live blender
// so callers can register and query parameters unconditionally.
func (s *HandoffSupervisor) Blender() *HierarchicalParamBlender {
	return s.paramBlender
}

// SyncBlenderFromProfile registers the supplied profile's learned
// handoff threshold into the blender at both the instance level
// (keyed on instanceUID) and the agent-model level (keyed on
// agentType + modelID). Called after every RecordObservation so the
// blender's posterior stays in step with the profile learner.
//
// Callers that bypass the bridge (tests, recovery paths) invoke this
// directly to populate the blender from a restored profile; the
// bridge runtime calls it automatically on each observation.
func (s *HandoffSupervisor) SyncBlenderFromProfile(agentType, modelID, instanceUID string, profile *AgentHandoffProfile) {
	if s == nil || s.paramBlender == nil || profile == nil {
		return
	}
	if profile.OptimalHandoffThreshold == nil {
		return
	}
	// Register at agent-model level always — every profile belongs to
	// exactly one (agentType, modelID) pair. Copy the learned weight by
	// value so later blender mutations never leak back into the profile
	// (LearnedWeight is a plain struct with no nested pointers).
	agentModelCopy := *profile.OptimalHandoffThreshold
	s.paramBlender.RegisterAgentModel(agentType, modelID, BlenderParamHandoffThreshold, &agentModelCopy)

	// Register at instance level when the caller has an identity —
	// bridge.go passes the agent's UID, dispatcher code can pass an
	// empty string when the concept of "instance" collapses to the
	// (agent, model) pair.
	if instanceUID != "" {
		instanceCopy := *profile.OptimalHandoffThreshold
		s.paramBlender.RegisterInstance(agentType, modelID, instanceUID, BlenderParamHandoffThreshold, &instanceCopy)
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

	s.mu.RLock()
	if existing, ok := s.bridges[agent.AgentID()]; ok && existing != nil {
		s.mu.RUnlock()
		return existing, nil
	}
	publisher := s.qualityPublisher
	briefSource := s.briefSource
	s.mu.RUnlock()

	desc, ok := s.descriptors.Get(agent.AgentType())
	if !ok {
		// Register with a reasonable default.
		desc = agent.Descriptor()
		s.descriptors.Register(desc)
	}

	cfg := BridgeConfigForAgent(desc)
	if cfg.BriefSource == nil {
		cfg.BriefSource = briefSource
	}
	bridge := NewHandoffBridge(cfg, agent, s)

	// Propagate quality publisher to the new bridge.
	if publisher != nil {
		bridge.SetQualityPublisher(publisher)
	}

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
	bridge, ok := s.bridges[agentID]
	if !ok {
		return nil
	}
	return bridge
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

// SetBriefSource sets the brief source bridges use to generate handoff
// summaries. Propagated to all existing and future bridges.
func (s *HandoffSupervisor) SetBriefSource(source BriefSource) {
	s.mu.Lock()
	s.briefSource = source
	bridges := make([]*HandoffBridge, 0, len(s.bridges))
	for _, b := range s.bridges {
		bridges = append(bridges, b)
	}
	s.mu.Unlock()

	for _, b := range bridges {
		b.mu.Lock()
		b.config.BriefSource = source
		if source == nil {
			b.briefGen = nil
		} else {
			b.briefGen = NewBriefGenerator(source)
		}
		b.mu.Unlock()
	}
}

// SetArchivePublisher wires the ArchivePublisher that bridges use to
// fire ArchiveRequests at handoff / eviction / terminate transitions.
// Propagated to all existing and future bridges. Passing nil clears
// the publisher — subsequent lifecycle transitions will not emit
// archive requests (the Archivalist retains any already-published
// state, so this is a clean "disable going forward" switch, not a
// history wipe).
//
// HAND-05 entry point — supervisor-level so bootstrap code can flip a
// single switch during startup rather than threading the publisher
// through every BridgeConfig.
func (s *HandoffSupervisor) SetArchivePublisher(pub ArchivePublisher) {
	s.mu.Lock()
	s.archivePublisher = pub
	bridges := make([]*HandoffBridge, 0, len(s.bridges))
	for _, b := range s.bridges {
		bridges = append(bridges, b)
	}
	s.mu.Unlock()

	for _, b := range bridges {
		b.mu.Lock()
		b.archivePublisher = pub
		b.mu.Unlock()
	}
}

// ArchivePublisher returns the supervisor's currently-installed
// archive publisher, or nil when none is set. Intended for
// tests and diagnostics only — the bridge reads the publisher
// through its own field, not this accessor.
func (s *HandoffSupervisor) ArchivePublisher() ArchivePublisher {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.archivePublisher
}

// MaybeCheckpointProfiles writes a WAL checkpoint for every
// registered bridge's profile + GP snapshot if the profile learner's
// persistence interval has elapsed. HAND-07: prior behavior only
// wrote checkpoints at Stop(), which meant a crash mid-session lost
// all profile learning since the last observation replay boundary.
// Periodic checkpointing caps that loss at PersistenceInterval
// without spamming fsyncs on every turn.
//
// Returns the number of checkpoints written — useful for tests.
// The caller is expected to invoke this from the bridge's per-turn
// hook; cost is amortized by ShouldPersist's time gate.
func (s *HandoffSupervisor) MaybeCheckpointProfiles() int {
	if s == nil || s.wal == nil || s.profileLearner == nil {
		return 0
	}
	if !s.profileLearner.ShouldPersist() {
		return 0
	}
	s.mu.RLock()
	bridges := make([]*HandoffBridge, 0, len(s.bridges))
	for _, b := range s.bridges {
		bridges = append(bridges, b)
	}
	s.mu.RUnlock()

	written := 0
	for _, b := range bridges {
		if err := s.wal.WriteCheckpoint(b.profile, b.gp); err == nil {
			written++
		}
	}
	if written > 0 {
		s.profileLearner.MarkPersisted()
	}
	return written
}

// SetQualityPublisher sets the callback that bridges use to publish
// GP quality predictions. Propagated to all existing and future bridges.
func (s *HandoffSupervisor) SetQualityPublisher(fn func(agentID string, quality, stdDev float64)) {
	s.mu.Lock()
	s.qualityPublisher = fn
	bridges := make([]*HandoffBridge, 0, len(s.bridges))
	for _, b := range s.bridges {
		bridges = append(bridges, b)
	}
	s.mu.Unlock()

	for _, b := range bridges {
		b.SetQualityPublisher(fn)
	}
}

// SetTrafficShiftCallbacks sets the callbacks used during gradual traffic
// shifts. onBegin registers the new agent alongside the old in the Guide.
// updateWeight updates the routing weight in the service registry.
func (s *HandoffSupervisor) SetTrafficShiftCallbacks(
	onBegin func(oldID string, newAgent HandoffableAgent),
	updateWeight func(agentID string, weight float64),
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onShiftBegin = onBegin
	s.updateWeight = updateWeight
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

	// Find unmatched shift_begin entries (no corresponding converge/abort).
	pendingShifts := make(map[string]*ShiftWALEntry)
	for i := range entries {
		entry := &entries[i]
		if entry.Shift == nil {
			continue
		}
		switch entry.EntryType {
		case EntryTypeShiftBegin:
			pendingShifts[entry.Shift.OldID] = entry.Shift
		case EntryTypeShiftConverge, EntryTypeShiftAbort:
			delete(pendingShifts, entry.Shift.OldID)
		}
	}

	// Reset weights for orphaned shifts — old agent gets full weight.
	for _, shift := range pendingShifts {
		_ = s.wal.WriteShiftEvent(ShiftWALEntry{
			Phase:     ShiftAborted,
			OldID:     shift.OldID,
			NewID:     shift.NewID,
			OldWeight: 1.0,
			NewWeight: 0,
			Timestamp: time.Now(),
			Error:     "recovered from crash — shift abandoned",
		})
	}
}
