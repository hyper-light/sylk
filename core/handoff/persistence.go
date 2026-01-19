package handoff

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// =============================================================================
// HO.9.1 WAL Entry Types - Persistence for Handoff State
// =============================================================================
//
// The Write-Ahead Log (WAL) provides durable persistence for handoff state.
// It uses NDJSON format (newline-delimited JSON) for efficient streaming
// and recovery. Each entry represents a single state change.

// WALEntryType identifies the type of a WAL entry.
type WALEntryType string

const (
	// EntryTypeObservation records a single observation event.
	// These are frequent and capture individual handoff decisions.
	EntryTypeObservation WALEntryType = "observation"

	// EntryTypeCheckpoint records a full state snapshot.
	// Checkpoints enable efficient recovery by limiting replay scope.
	EntryTypeCheckpoint WALEntryType = "checkpoint"

	// EntryTypePriorUpdate records changes to prior distributions.
	// These capture Bayesian updates to learned parameters.
	EntryTypePriorUpdate WALEntryType = "prior_update"

	// EntryTypeHandoffOutcome records the result of a handoff decision.
	// These are used for learning and improving future decisions.
	EntryTypeHandoffOutcome WALEntryType = "handoff_outcome"
)

// ProfileSnapshot is a serializable form of AgentHandoffProfile.
// It captures all learned parameters for persistence.
type ProfileSnapshot struct {
	// AgentType identifies the type of agent.
	AgentType string `json:"agent_type"`

	// ModelID identifies the LLM model.
	ModelID string `json:"model_id"`

	// InstanceID uniquely identifies the instance.
	InstanceID string `json:"instance_id"`

	// OptimalHandoffThreshold is the learned threshold.
	OptimalHandoffThreshold *LearnedWeight `json:"optimal_handoff_threshold,omitempty"`

	// OptimalPreparedSize is the learned optimal context size.
	OptimalPreparedSize *LearnedContextSize `json:"optimal_prepared_size,omitempty"`

	// QualityDegradationCurve tracks quality vs context.
	QualityDegradationCurve *LearnedWeight `json:"quality_degradation_curve,omitempty"`

	// PreserveRecent tracks how many recent turns to preserve.
	PreserveRecent *LearnedContextSize `json:"preserve_recent,omitempty"`

	// ContextUsageWeight tracks context importance.
	ContextUsageWeight *LearnedWeight `json:"context_usage_weight,omitempty"`

	// EffectiveSamples is the total observations incorporated.
	EffectiveSamples float64 `json:"effective_samples"`

	// LastUpdated is when the profile was last updated.
	LastUpdated time.Time `json:"last_updated"`

	// CreatedAt is when the profile was created.
	CreatedAt time.Time `json:"created_at"`
}

// NewProfileSnapshot creates a ProfileSnapshot from an AgentHandoffProfile.
func NewProfileSnapshot(profile *AgentHandoffProfile) *ProfileSnapshot {
	if profile == nil {
		return nil
	}

	snap := &ProfileSnapshot{
		AgentType:        profile.AgentType,
		ModelID:          profile.ModelID,
		InstanceID:       profile.InstanceID,
		EffectiveSamples: profile.EffectiveSamples,
		LastUpdated:      profile.LastUpdated,
		CreatedAt:        profile.CreatedAt,
	}

	// Deep copy learned parameters
	if profile.OptimalHandoffThreshold != nil {
		snap.OptimalHandoffThreshold = &LearnedWeight{
			Alpha:            profile.OptimalHandoffThreshold.Alpha,
			Beta:             profile.OptimalHandoffThreshold.Beta,
			EffectiveSamples: profile.OptimalHandoffThreshold.EffectiveSamples,
			PriorAlpha:       profile.OptimalHandoffThreshold.PriorAlpha,
			PriorBeta:        profile.OptimalHandoffThreshold.PriorBeta,
		}
	}

	if profile.OptimalPreparedSize != nil {
		snap.OptimalPreparedSize = &LearnedContextSize{
			Alpha:            profile.OptimalPreparedSize.Alpha,
			Beta:             profile.OptimalPreparedSize.Beta,
			EffectiveSamples: profile.OptimalPreparedSize.EffectiveSamples,
			PriorAlpha:       profile.OptimalPreparedSize.PriorAlpha,
			PriorBeta:        profile.OptimalPreparedSize.PriorBeta,
		}
	}

	if profile.QualityDegradationCurve != nil {
		snap.QualityDegradationCurve = &LearnedWeight{
			Alpha:            profile.QualityDegradationCurve.Alpha,
			Beta:             profile.QualityDegradationCurve.Beta,
			EffectiveSamples: profile.QualityDegradationCurve.EffectiveSamples,
			PriorAlpha:       profile.QualityDegradationCurve.PriorAlpha,
			PriorBeta:        profile.QualityDegradationCurve.PriorBeta,
		}
	}

	if profile.PreserveRecent != nil {
		snap.PreserveRecent = &LearnedContextSize{
			Alpha:            profile.PreserveRecent.Alpha,
			Beta:             profile.PreserveRecent.Beta,
			EffectiveSamples: profile.PreserveRecent.EffectiveSamples,
			PriorAlpha:       profile.PreserveRecent.PriorAlpha,
			PriorBeta:        profile.PreserveRecent.PriorBeta,
		}
	}

	if profile.ContextUsageWeight != nil {
		snap.ContextUsageWeight = &LearnedWeight{
			Alpha:            profile.ContextUsageWeight.Alpha,
			Beta:             profile.ContextUsageWeight.Beta,
			EffectiveSamples: profile.ContextUsageWeight.EffectiveSamples,
			PriorAlpha:       profile.ContextUsageWeight.PriorAlpha,
			PriorBeta:        profile.ContextUsageWeight.PriorBeta,
		}
	}

	return snap
}

// ToProfile converts a ProfileSnapshot back to an AgentHandoffProfile.
func (ps *ProfileSnapshot) ToProfile() *AgentHandoffProfile {
	if ps == nil {
		return nil
	}

	profile := &AgentHandoffProfile{
		AgentType:        ps.AgentType,
		ModelID:          ps.ModelID,
		InstanceID:       ps.InstanceID,
		EffectiveSamples: ps.EffectiveSamples,
		LastUpdated:      ps.LastUpdated,
		CreatedAt:        ps.CreatedAt,
	}

	// Deep copy learned parameters
	if ps.OptimalHandoffThreshold != nil {
		profile.OptimalHandoffThreshold = &LearnedWeight{
			Alpha:            ps.OptimalHandoffThreshold.Alpha,
			Beta:             ps.OptimalHandoffThreshold.Beta,
			EffectiveSamples: ps.OptimalHandoffThreshold.EffectiveSamples,
			PriorAlpha:       ps.OptimalHandoffThreshold.PriorAlpha,
			PriorBeta:        ps.OptimalHandoffThreshold.PriorBeta,
		}
	}

	if ps.OptimalPreparedSize != nil {
		profile.OptimalPreparedSize = &LearnedContextSize{
			Alpha:            ps.OptimalPreparedSize.Alpha,
			Beta:             ps.OptimalPreparedSize.Beta,
			EffectiveSamples: ps.OptimalPreparedSize.EffectiveSamples,
			PriorAlpha:       ps.OptimalPreparedSize.PriorAlpha,
			PriorBeta:        ps.OptimalPreparedSize.PriorBeta,
		}
	}

	if ps.QualityDegradationCurve != nil {
		profile.QualityDegradationCurve = &LearnedWeight{
			Alpha:            ps.QualityDegradationCurve.Alpha,
			Beta:             ps.QualityDegradationCurve.Beta,
			EffectiveSamples: ps.QualityDegradationCurve.EffectiveSamples,
			PriorAlpha:       ps.QualityDegradationCurve.PriorAlpha,
			PriorBeta:        ps.QualityDegradationCurve.PriorBeta,
		}
	}

	if ps.PreserveRecent != nil {
		profile.PreserveRecent = &LearnedContextSize{
			Alpha:            ps.PreserveRecent.Alpha,
			Beta:             ps.PreserveRecent.Beta,
			EffectiveSamples: ps.PreserveRecent.EffectiveSamples,
			PriorAlpha:       ps.PreserveRecent.PriorAlpha,
			PriorBeta:        ps.PreserveRecent.PriorBeta,
		}
	}

	if ps.ContextUsageWeight != nil {
		profile.ContextUsageWeight = &LearnedWeight{
			Alpha:            ps.ContextUsageWeight.Alpha,
			Beta:             ps.ContextUsageWeight.Beta,
			EffectiveSamples: ps.ContextUsageWeight.EffectiveSamples,
			PriorAlpha:       ps.ContextUsageWeight.PriorAlpha,
			PriorBeta:        ps.ContextUsageWeight.PriorBeta,
		}
	}

	return profile
}

// GPSnapshot is a serializable form of AgentGaussianProcess.
// It captures the GP state for persistence.
type GPSnapshot struct {
	// Observations collected by the GP.
	Observations []*GPObservation `json:"observations,omitempty"`

	// Hyperparams for the GP kernel.
	Hyperparams *GPHyperparams `json:"hyperparams,omitempty"`

	// PriorMean is the learned prior mean function.
	PriorMean *GPPriorMean `json:"prior_mean,omitempty"`

	// MaxObservations limits stored observations.
	MaxObservations int `json:"max_observations"`
}

// NewGPSnapshot creates a GPSnapshot from an AgentGaussianProcess.
func NewGPSnapshot(gp *AgentGaussianProcess) *GPSnapshot {
	if gp == nil {
		return nil
	}

	snap := &GPSnapshot{
		MaxObservations: gp.maxObservations,
	}

	// Copy observations
	gp.mu.RLock()
	defer gp.mu.RUnlock()

	if len(gp.observations) > 0 {
		snap.Observations = make([]*GPObservation, len(gp.observations))
		for i, obs := range gp.observations {
			snap.Observations[i] = obs.Clone()
		}
	}

	if gp.hyperparams != nil {
		snap.Hyperparams = gp.hyperparams.Clone()
	}

	if gp.priorMean != nil {
		snap.PriorMean = gp.priorMean.Clone()
	}

	return snap
}

// ToGP converts a GPSnapshot back to an AgentGaussianProcess.
func (gs *GPSnapshot) ToGP() *AgentGaussianProcess {
	if gs == nil {
		return NewAgentGaussianProcess(nil)
	}

	hyperparams := gs.Hyperparams
	if hyperparams == nil {
		hyperparams = DefaultGPHyperparams()
	}

	gp := NewAgentGaussianProcess(hyperparams)

	if gs.MaxObservations > 0 {
		gp.maxObservations = gs.MaxObservations
	}

	// Restore observations
	for _, obs := range gs.Observations {
		if obs != nil {
			gp.observations = append(gp.observations, obs.Clone())
		}
	}

	if gs.PriorMean != nil {
		gp.priorMean = gs.PriorMean.Clone()
	}

	return gp
}

// HandoffWALEntry represents a single entry in the WAL.
type HandoffWALEntry struct {
	// Timestamp is when this entry was created.
	Timestamp time.Time `json:"timestamp"`

	// EntryType identifies the type of this entry.
	EntryType WALEntryType `json:"entry_type"`

	// ProfileSnapshot captures profile state (for checkpoints and updates).
	ProfileSnapshot *ProfileSnapshot `json:"profile_snapshot,omitempty"`

	// GPSnapshot captures GP state (for checkpoints).
	GPSnapshot *GPSnapshot `json:"gp_snapshot,omitempty"`

	// Observation is a single handoff observation (for observation entries).
	Observation *HandoffObservation `json:"observation,omitempty"`

	// GPObservation is a single GP observation (for observation entries).
	GPObservation *GPObservation `json:"gp_observation,omitempty"`

	// Decision captures a handoff decision outcome.
	Decision *HandoffDecision `json:"decision,omitempty"`

	// WasSuccessful indicates if a handoff/operation was successful.
	WasSuccessful bool `json:"was_successful,omitempty"`

	// SequenceNumber is a monotonically increasing counter for ordering.
	SequenceNumber uint64 `json:"sequence_number"`
}

// NewObservationEntry creates a WAL entry for an observation.
func NewObservationEntry(obs *HandoffObservation, gpObs *GPObservation, seq uint64) *HandoffWALEntry {
	return &HandoffWALEntry{
		Timestamp:      time.Now(),
		EntryType:      EntryTypeObservation,
		Observation:    obs,
		GPObservation:  gpObs,
		SequenceNumber: seq,
	}
}

// NewCheckpointEntry creates a WAL entry for a full checkpoint.
func NewCheckpointEntry(profile *AgentHandoffProfile, gp *AgentGaussianProcess, seq uint64) *HandoffWALEntry {
	return &HandoffWALEntry{
		Timestamp:       time.Now(),
		EntryType:       EntryTypeCheckpoint,
		ProfileSnapshot: NewProfileSnapshot(profile),
		GPSnapshot:      NewGPSnapshot(gp),
		SequenceNumber:  seq,
	}
}

// NewPriorUpdateEntry creates a WAL entry for a prior update.
func NewPriorUpdateEntry(profile *AgentHandoffProfile, seq uint64) *HandoffWALEntry {
	return &HandoffWALEntry{
		Timestamp:       time.Now(),
		EntryType:       EntryTypePriorUpdate,
		ProfileSnapshot: NewProfileSnapshot(profile),
		SequenceNumber:  seq,
	}
}

// NewHandoffOutcomeEntry creates a WAL entry for a handoff outcome.
func NewHandoffOutcomeEntry(decision *HandoffDecision, wasSuccessful bool, seq uint64) *HandoffWALEntry {
	return &HandoffWALEntry{
		Timestamp:      time.Now(),
		EntryType:      EntryTypeHandoffOutcome,
		Decision:       decision,
		WasSuccessful:  wasSuccessful,
		SequenceNumber: seq,
	}
}

// =============================================================================
// HO.9.2 WAL Recovery - HandoffWAL Management
// =============================================================================

// HandoffState represents the complete recoverable state from a WAL.
type HandoffState struct {
	// Profile is the recovered AgentHandoffProfile.
	Profile *AgentHandoffProfile

	// GP is the recovered AgentGaussianProcess.
	GP *AgentGaussianProcess

	// LastSequence is the last sequence number seen.
	LastSequence uint64

	// RecoveryTimestamp is when recovery completed.
	RecoveryTimestamp time.Time

	// EntriesReplayed is the number of entries replayed.
	EntriesReplayed int

	// CorruptedEntries is the count of entries skipped due to corruption.
	CorruptedEntries int
}

// HandoffWAL manages the Write-Ahead Log for handoff state persistence.
type HandoffWAL struct {
	mu sync.Mutex

	// filePath is the path to the WAL file.
	filePath string

	// file is the open file handle.
	file *os.File

	// sequence is the next sequence number to assign.
	sequence uint64

	// entriesSinceCheckpoint counts entries since last checkpoint.
	entriesSinceCheckpoint int

	// checkpointInterval is how many entries between checkpoints.
	checkpointInterval int

	// lastCheckpointSeq is the sequence number of the last checkpoint.
	lastCheckpointSeq uint64

	// closed indicates if the WAL has been closed.
	closed bool
}

// WALConfig configures the HandoffWAL behavior.
type WALConfig struct {
	// CheckpointInterval is how many entries between automatic checkpoints.
	// Default: 100
	CheckpointInterval int
}

// DefaultWALConfig returns sensible defaults for WAL configuration.
func DefaultWALConfig() *WALConfig {
	return &WALConfig{
		CheckpointInterval: 100,
	}
}

// NewHandoffWAL creates a new WAL at the specified path.
// If the file exists, it will be opened for appending.
// If it doesn't exist, it will be created.
func NewHandoffWAL(filePath string, config *WALConfig) (*HandoffWAL, error) {
	if config == nil {
		config = DefaultWALConfig()
	}

	wal := &HandoffWAL{
		filePath:           filePath,
		sequence:           0,
		checkpointInterval: config.CheckpointInterval,
		closed:             false,
	}

	// Open file for append (create if not exists)
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL file: %w", err)
	}
	wal.file = file

	// Scan to find the last sequence number
	if err := wal.scanForLastSequence(); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to scan WAL: %w", err)
	}

	return wal, nil
}

// scanForLastSequence scans the WAL to find the last sequence number.
func (wal *HandoffWAL) scanForLastSequence() error {
	// Seek to beginning
	if _, err := wal.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	scanner := bufio.NewScanner(wal.file)
	// Increase buffer size for large entries
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry HandoffWALEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip corrupted entries during scan
			continue
		}

		if entry.SequenceNumber > wal.sequence {
			wal.sequence = entry.SequenceNumber
		}

		if entry.EntryType == EntryTypeCheckpoint {
			wal.lastCheckpointSeq = entry.SequenceNumber
			wal.entriesSinceCheckpoint = 0
		} else {
			wal.entriesSinceCheckpoint++
		}
	}

	// Seek back to end for appending
	if _, err := wal.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	return scanner.Err()
}

// AppendEntry appends a new entry to the WAL.
// The entry's sequence number will be set automatically.
// This performs atomic write with fsync for durability.
func (wal *HandoffWAL) AppendEntry(entry *HandoffWALEntry) error {
	wal.mu.Lock()
	defer wal.mu.Unlock()

	if wal.closed {
		return fmt.Errorf("WAL is closed")
	}

	if entry == nil {
		return fmt.Errorf("entry cannot be nil")
	}

	// Assign sequence number
	wal.sequence++
	entry.SequenceNumber = wal.sequence
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Marshal to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	// Write with newline
	data = append(data, '\n')
	if _, err := wal.file.Write(data); err != nil {
		return fmt.Errorf("failed to write entry: %w", err)
	}

	// Fsync for durability
	if err := wal.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync entry: %w", err)
	}

	// Track checkpoint timing
	if entry.EntryType == EntryTypeCheckpoint {
		wal.lastCheckpointSeq = entry.SequenceNumber
		wal.entriesSinceCheckpoint = 0
	} else {
		wal.entriesSinceCheckpoint++
	}

	return nil
}

// LoadEntries loads all entries from the WAL file.
// Returns entries in sequence order, skipping corrupted entries.
func (wal *HandoffWAL) LoadEntries() ([]HandoffWALEntry, error) {
	wal.mu.Lock()
	defer wal.mu.Unlock()

	if wal.closed {
		return nil, fmt.Errorf("WAL is closed")
	}

	// Seek to beginning
	if _, err := wal.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek: %w", err)
	}

	var entries []HandoffWALEntry
	scanner := bufio.NewScanner(wal.file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry HandoffWALEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip corrupted entries
			continue
		}

		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	// Seek back to end
	if _, err := wal.file.Seek(0, io.SeekEnd); err != nil {
		return nil, fmt.Errorf("failed to seek to end: %w", err)
	}

	return entries, nil
}

// Recover restores the full handoff state from the WAL.
// It finds the latest checkpoint and replays subsequent entries.
// Corrupted entries are skipped with a count in the result.
func (wal *HandoffWAL) Recover() (*HandoffState, error) {
	entries, err := wal.LoadEntries()
	if err != nil {
		return nil, fmt.Errorf("failed to load entries: %w", err)
	}

	state := &HandoffState{
		RecoveryTimestamp: time.Now(),
	}

	if len(entries) == 0 {
		// Empty WAL - return default state
		state.Profile = NewAgentHandoffProfile("default", "default", "")
		state.GP = NewAgentGaussianProcess(nil)
		return state, nil
	}

	// Find the latest checkpoint
	var latestCheckpoint *HandoffWALEntry
	var checkpointIdx int = -1

	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].EntryType == EntryTypeCheckpoint {
			latestCheckpoint = &entries[i]
			checkpointIdx = i
			break
		}
	}

	// Initialize state from checkpoint or defaults
	if latestCheckpoint != nil && latestCheckpoint.ProfileSnapshot != nil {
		state.Profile = latestCheckpoint.ProfileSnapshot.ToProfile()
	} else {
		state.Profile = NewAgentHandoffProfile("default", "default", "")
	}

	if latestCheckpoint != nil && latestCheckpoint.GPSnapshot != nil {
		state.GP = latestCheckpoint.GPSnapshot.ToGP()
	} else {
		state.GP = NewAgentGaussianProcess(nil)
	}

	// Replay entries after checkpoint
	startIdx := 0
	if checkpointIdx >= 0 {
		startIdx = checkpointIdx + 1
	}

	for i := startIdx; i < len(entries); i++ {
		entry := &entries[i]
		state.EntriesReplayed++

		switch entry.EntryType {
		case EntryTypeObservation:
			if entry.Observation != nil && state.Profile != nil {
				state.Profile.Update(entry.Observation)
			}
			if entry.GPObservation != nil && state.GP != nil {
				state.GP.AddObservation(entry.GPObservation)
			}

		case EntryTypePriorUpdate:
			if entry.ProfileSnapshot != nil {
				state.Profile = entry.ProfileSnapshot.ToProfile()
			}

		case EntryTypeHandoffOutcome:
			// Outcome entries are informational - no state update needed
			// They can be used for analytics

		case EntryTypeCheckpoint:
			// This shouldn't happen since we started after the last checkpoint,
			// but handle it gracefully
			if entry.ProfileSnapshot != nil {
				state.Profile = entry.ProfileSnapshot.ToProfile()
			}
			if entry.GPSnapshot != nil {
				state.GP = entry.GPSnapshot.ToGP()
			}
		}

		if entry.SequenceNumber > state.LastSequence {
			state.LastSequence = entry.SequenceNumber
		}
	}

	return state, nil
}

// Truncate removes all entries from the WAL.
// This is typically called after writing a compacted checkpoint.
func (wal *HandoffWAL) Truncate() error {
	wal.mu.Lock()
	defer wal.mu.Unlock()

	if wal.closed {
		return fmt.Errorf("WAL is closed")
	}

	// Truncate file to zero
	if err := wal.file.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate: %w", err)
	}

	// Seek to beginning
	if _, err := wal.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	// Sync
	if err := wal.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync: %w", err)
	}

	// Reset state
	wal.entriesSinceCheckpoint = 0
	wal.lastCheckpointSeq = 0

	return nil
}

// Compact performs compaction by truncating and writing a fresh checkpoint.
// This reduces WAL size and improves recovery time.
func (wal *HandoffWAL) Compact(profile *AgentHandoffProfile, gp *AgentGaussianProcess) error {
	// Truncate first
	if err := wal.Truncate(); err != nil {
		return fmt.Errorf("failed to truncate during compaction: %w", err)
	}

	// Write a fresh checkpoint
	entry := NewCheckpointEntry(profile, gp, 0)
	if err := wal.AppendEntry(entry); err != nil {
		return fmt.Errorf("failed to write checkpoint during compaction: %w", err)
	}

	return nil
}

// NeedsCheckpoint returns true if enough entries have accumulated
// since the last checkpoint to warrant a new one.
func (wal *HandoffWAL) NeedsCheckpoint() bool {
	wal.mu.Lock()
	defer wal.mu.Unlock()

	return wal.entriesSinceCheckpoint >= wal.checkpointInterval
}

// GetSequence returns the current sequence number.
func (wal *HandoffWAL) GetSequence() uint64 {
	wal.mu.Lock()
	defer wal.mu.Unlock()
	return wal.sequence
}

// GetEntriesSinceCheckpoint returns the count of entries since last checkpoint.
func (wal *HandoffWAL) GetEntriesSinceCheckpoint() int {
	wal.mu.Lock()
	defer wal.mu.Unlock()
	return wal.entriesSinceCheckpoint
}

// Close closes the WAL file.
func (wal *HandoffWAL) Close() error {
	wal.mu.Lock()
	defer wal.mu.Unlock()

	if wal.closed {
		return nil
	}

	wal.closed = true

	if wal.file != nil {
		return wal.file.Close()
	}

	return nil
}

// FilePath returns the path to the WAL file.
func (wal *HandoffWAL) FilePath() string {
	return wal.filePath
}

// =============================================================================
// Helper Functions for WAL Operations
// =============================================================================

// WriteObservation is a convenience method to write an observation entry.
func (wal *HandoffWAL) WriteObservation(obs *HandoffObservation, gpObs *GPObservation) error {
	entry := NewObservationEntry(obs, gpObs, 0)
	return wal.AppendEntry(entry)
}

// WriteCheckpoint is a convenience method to write a checkpoint entry.
func (wal *HandoffWAL) WriteCheckpoint(profile *AgentHandoffProfile, gp *AgentGaussianProcess) error {
	entry := NewCheckpointEntry(profile, gp, 0)
	return wal.AppendEntry(entry)
}

// WritePriorUpdate is a convenience method to write a prior update entry.
func (wal *HandoffWAL) WritePriorUpdate(profile *AgentHandoffProfile) error {
	entry := NewPriorUpdateEntry(profile, 0)
	return wal.AppendEntry(entry)
}

// WriteHandoffOutcome is a convenience method to write a handoff outcome entry.
func (wal *HandoffWAL) WriteHandoffOutcome(decision *HandoffDecision, wasSuccessful bool) error {
	entry := NewHandoffOutcomeEntry(decision, wasSuccessful, 0)
	return wal.AppendEntry(entry)
}

// MaybeCheckpoint writes a checkpoint if enough entries have accumulated.
// Returns true if a checkpoint was written.
func (wal *HandoffWAL) MaybeCheckpoint(profile *AgentHandoffProfile, gp *AgentGaussianProcess) (bool, error) {
	if !wal.NeedsCheckpoint() {
		return false, nil
	}

	if err := wal.WriteCheckpoint(profile, gp); err != nil {
		return false, err
	}

	return true, nil
}
