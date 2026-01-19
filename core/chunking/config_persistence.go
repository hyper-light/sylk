package chunking

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// =============================================================================
// CK.3.1 WAL Entry Type for Chunk Configuration Learning
// =============================================================================

var (
	// ErrWALClosed indicates the WAL has been closed and cannot accept operations.
	ErrWALClosed = errors.New("chunk config WAL is closed")

	// ErrWALCorrupted indicates the WAL file contains corrupted data.
	ErrWALCorrupted = errors.New("chunk config WAL entry corrupted")

	// ErrInvalidEntry indicates an entry failed validation.
	ErrInvalidEntry = errors.New("invalid WAL entry")
)

// ChunkConfigWALEntry represents a single entry in the chunk configuration WAL.
// Each entry captures a snapshot of learned parameters at a point in time.
// Entries are JSON-serializable for NDJSON (newline-delimited JSON) format.
type ChunkConfigWALEntry struct {
	// Timestamp records when this entry was written.
	Timestamp time.Time `json:"timestamp"`

	// SequenceID is a monotonically increasing identifier for ordering entries.
	SequenceID uint64 `json:"sequence_id"`

	// ConfigSnapshot contains the full chunk configuration state at this point.
	ConfigSnapshot *ChunkConfigSnapshot `json:"config_snapshot"`

	// LearnedParams contains the domain-specific learned parameters.
	LearnedParams map[string]*DomainLearnedParams `json:"learned_params"`

	// Observation is the observation that triggered this entry (optional).
	// This is nil for checkpoint entries that are not triggered by observations.
	Observation *ChunkUsageObservation `json:"observation,omitempty"`

	// EntryType indicates the type of WAL entry.
	EntryType ChunkConfigWALEntryType `json:"entry_type"`
}

// ChunkConfigWALEntryType represents different types of WAL entries.
type ChunkConfigWALEntryType uint8

const (
	// EntryTypeObservation indicates an entry triggered by a new observation.
	EntryTypeObservation ChunkConfigWALEntryType = iota

	// EntryTypeCheckpoint indicates a periodic checkpoint of the full state.
	EntryTypeCheckpoint

	// EntryTypeSnapshot indicates a full state snapshot for recovery.
	EntryTypeSnapshot
)

// String returns the string representation of the entry type.
func (t ChunkConfigWALEntryType) String() string {
	switch t {
	case EntryTypeObservation:
		return "observation"
	case EntryTypeCheckpoint:
		return "checkpoint"
	case EntryTypeSnapshot:
		return "snapshot"
	default:
		return "unknown"
	}
}

// ChunkConfigSnapshot captures the complete state of a ChunkConfig for persistence.
type ChunkConfigSnapshot struct {
	// MaxTokens is the fixed maximum token limit.
	MaxTokens int `json:"max_tokens"`

	// TargetTokens captures the learned target token size distribution.
	TargetTokens *LearnedContextSizeSnapshot `json:"target_tokens"`

	// MinTokens captures the learned minimum token size distribution.
	MinTokens *LearnedContextSizeSnapshot `json:"min_tokens"`

	// ContextTokensBefore captures the learned context before distribution.
	ContextTokensBefore *LearnedContextSizeSnapshot `json:"context_tokens_before"`

	// ContextTokensAfter captures the learned context after distribution.
	ContextTokensAfter *LearnedContextSizeSnapshot `json:"context_tokens_after"`

	// OverflowStrategyWeights captures the learned overflow strategy preferences.
	OverflowStrategyWeights *LearnedOverflowWeightsSnapshot `json:"overflow_strategy_weights"`
}

// LearnedContextSizeSnapshot captures the state of a LearnedContextSize for persistence.
type LearnedContextSizeSnapshot struct {
	Alpha            float64 `json:"alpha"`
	Beta             float64 `json:"beta"`
	EffectiveSamples float64 `json:"effective_samples"`
	PriorAlpha       float64 `json:"prior_alpha"`
	PriorBeta        float64 `json:"prior_beta"`
}

// LearnedOverflowWeightsSnapshot captures the state of overflow strategy weights.
type LearnedOverflowWeightsSnapshot struct {
	RecursiveCount float64 `json:"recursive_count"`
	SentenceCount  float64 `json:"sentence_count"`
	TruncateCount  float64 `json:"truncate_count"`
}

// DomainLearnedParams captures all learned parameters for a specific domain.
type DomainLearnedParams struct {
	Domain           string                          `json:"domain"`
	Config           *ChunkConfigSnapshot            `json:"config"`
	Confidence       float64                         `json:"confidence"`
	ObservationCount int                             `json:"observation_count"`
	OverflowWeights  *LearnedOverflowWeightsSnapshot `json:"overflow_weights,omitempty"`
}

// Validate checks if the WAL entry has valid data.
func (e *ChunkConfigWALEntry) Validate() error {
	if e.Timestamp.IsZero() {
		return fmt.Errorf("%w: timestamp is zero", ErrInvalidEntry)
	}
	if e.ConfigSnapshot == nil && e.LearnedParams == nil {
		return fmt.Errorf("%w: both config snapshot and learned params are nil", ErrInvalidEntry)
	}
	return nil
}

// =============================================================================
// CK.3.2 ChunkConfigWAL - WAL for Chunk Configuration Learning
// =============================================================================

// ChunkConfigWAL provides append-only write-ahead logging for chunk configuration
// learning state. It uses NDJSON format for durability and human readability.
type ChunkConfigWAL struct {
	mu sync.RWMutex

	// filePath is the path to the WAL file.
	filePath string

	// file is the open file handle for appending entries.
	file *os.File

	// writer is a buffered writer for efficient appends.
	writer *bufio.Writer

	// sequenceID is the next sequence ID to assign.
	sequenceID uint64

	// closed indicates whether the WAL has been closed.
	closed bool

	// lastSyncTime tracks when we last synced to disk.
	lastSyncTime time.Time

	// syncInterval controls how often to fsync (0 means sync on every write).
	syncInterval time.Duration

	// pendingSync indicates if there are unsynced writes.
	pendingSync bool
}

// ChunkConfigWALConfig holds configuration options for the WAL.
type ChunkConfigWALConfig struct {
	// FilePath is the path where the WAL file will be stored.
	FilePath string

	// SyncInterval controls how often to fsync. Zero means sync on every write.
	SyncInterval time.Duration

	// CreateDir indicates whether to create parent directories if they don't exist.
	CreateDir bool
}

// DefaultChunkConfigWALConfig returns sensible default configuration.
func DefaultChunkConfigWALConfig(dir string) ChunkConfigWALConfig {
	return ChunkConfigWALConfig{
		FilePath:     filepath.Join(dir, "chunk_config.wal"),
		SyncInterval: 0, // Sync on every write for durability
		CreateDir:    true,
	}
}

// NewChunkConfigWAL creates a new WAL for chunk configuration persistence.
// If the WAL file already exists, it will be opened for appending.
func NewChunkConfigWAL(config ChunkConfigWALConfig) (*ChunkConfigWAL, error) {
	if config.FilePath == "" {
		return nil, fmt.Errorf("WAL file path cannot be empty")
	}

	// Create parent directories if needed
	if config.CreateDir {
		dir := filepath.Dir(config.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create WAL directory: %w", err)
		}
	}

	// Open file for append-only writes
	file, err := os.OpenFile(config.FilePath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL file: %w", err)
	}

	wal := &ChunkConfigWAL{
		filePath:     config.FilePath,
		file:         file,
		writer:       bufio.NewWriterSize(file, 64*1024),
		sequenceID:   0,
		closed:       false,
		lastSyncTime: time.Now(),
		syncInterval: config.SyncInterval,
		pendingSync:  false,
	}

	// Load existing sequence ID from file
	if err := wal.loadLastSequenceID(); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to load sequence ID: %w", err)
	}

	return wal, nil
}

// loadLastSequenceID reads the WAL file to find the last sequence ID.
func (w *ChunkConfigWAL) loadLastSequenceID() error {
	// Reset to beginning of file
	if _, err := w.file.Seek(0, 0); err != nil {
		return err
	}

	scanner := bufio.NewScanner(w.file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for large entries

	var lastSeq uint64
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry ChunkConfigWALEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip corrupted entries during initialization
			continue
		}
		if entry.SequenceID > lastSeq {
			lastSeq = entry.SequenceID
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to scan WAL: %w", err)
	}

	w.sequenceID = lastSeq

	// Seek to end for appending
	if _, err := w.file.Seek(0, 2); err != nil {
		return err
	}

	return nil
}

// AppendEntry appends a new entry to the WAL with atomic write and fsync.
func (w *ChunkConfigWAL) AppendEntry(entry *ChunkConfigWALEntry) (uint64, error) {
	if w.closed {
		return 0, ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Assign sequence ID and timestamp
	w.sequenceID++
	entry.SequenceID = w.sequenceID
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Validate entry
	if err := entry.Validate(); err != nil {
		w.sequenceID-- // Rollback sequence ID
		return 0, err
	}

	// Serialize to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		w.sequenceID-- // Rollback sequence ID
		return 0, fmt.Errorf("failed to marshal entry: %w", err)
	}

	// Write with newline delimiter
	if _, err := w.writer.Write(data); err != nil {
		w.sequenceID-- // Rollback sequence ID
		return 0, fmt.Errorf("failed to write entry: %w", err)
	}
	if _, err := w.writer.Write([]byte{'\n'}); err != nil {
		w.sequenceID-- // Rollback sequence ID
		return 0, fmt.Errorf("failed to write newline: %w", err)
	}

	w.pendingSync = true

	// Sync to disk
	if err := w.syncIfNeeded(); err != nil {
		return 0, fmt.Errorf("failed to sync: %w", err)
	}

	return entry.SequenceID, nil
}

// syncIfNeeded flushes the buffer and fsyncs if needed.
func (w *ChunkConfigWAL) syncIfNeeded() error {
	if !w.pendingSync {
		return nil
	}

	// Always sync if interval is 0, otherwise check interval
	if w.syncInterval > 0 && time.Since(w.lastSyncTime) < w.syncInterval {
		return nil
	}

	return w.syncLocked()
}

// syncLocked performs the actual sync operation (must be called with lock held).
func (w *ChunkConfigWAL) syncLocked() error {
	// Flush buffered writer
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush buffer: %w", err)
	}

	// Fsync for durability
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("failed to fsync: %w", err)
	}

	w.pendingSync = false
	w.lastSyncTime = time.Now()
	return nil
}

// Sync forces a sync to disk.
func (w *ChunkConfigWAL) Sync() error {
	if w.closed {
		return ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	return w.syncLocked()
}

// LoadEntries reads all entries from the WAL file.
// Returns entries in order of sequence ID. Corrupted entries are skipped.
func (w *ChunkConfigWAL) LoadEntries() ([]*ChunkConfigWALEntry, error) {
	if w.closed {
		return nil, ErrWALClosed
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.loadEntriesFromFile()
}

// loadEntriesFromFile reads entries from the WAL file (internal, no locking).
func (w *ChunkConfigWAL) loadEntriesFromFile() ([]*ChunkConfigWALEntry, error) {
	// Open a separate file handle for reading
	file, err := os.Open(w.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to open WAL for reading: %w", err)
	}
	defer file.Close()

	var entries []*ChunkConfigWALEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry ChunkConfigWALEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip corrupted entries but continue processing
			continue
		}

		entries = append(entries, &entry)
	}

	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("error reading WAL: %w", err)
	}

	return entries, nil
}

// Recover loads all entries from the WAL and replays them to restore learned state.
// Returns a fully populated ChunkConfigLearner with the recovered state.
func (w *ChunkConfigWAL) Recover() (*ChunkConfigLearner, error) {
	entries, err := w.LoadEntries()
	if err != nil {
		return nil, fmt.Errorf("failed to load entries: %w", err)
	}

	// Create a new learner
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create learner: %w", err)
	}

	// If no entries, return fresh learner
	if len(entries) == 0 {
		return learner, nil
	}

	// Find the most recent snapshot or checkpoint entry
	var latestSnapshot *ChunkConfigWALEntry
	var observationsSinceSnapshot []*ChunkConfigWALEntry

	for _, entry := range entries {
		if entry.EntryType == EntryTypeSnapshot || entry.EntryType == EntryTypeCheckpoint {
			latestSnapshot = entry
			observationsSinceSnapshot = nil
		} else if entry.EntryType == EntryTypeObservation && entry.Observation != nil {
			observationsSinceSnapshot = append(observationsSinceSnapshot, entry)
		}
	}

	// Restore from snapshot if available
	if latestSnapshot != nil {
		if err := w.restoreFromSnapshot(learner, latestSnapshot); err != nil {
			return nil, fmt.Errorf("failed to restore from snapshot: %w", err)
		}
	}

	// Replay observations since the last snapshot
	for _, entry := range observationsSinceSnapshot {
		if entry.Observation != nil {
			if err := learner.RecordObservation(*entry.Observation); err != nil {
				// Log but continue - don't fail recovery for individual observation errors
				continue
			}
		}
	}

	return learner, nil
}

// restoreFromSnapshot restores the learner state from a snapshot entry.
func (w *ChunkConfigWAL) restoreFromSnapshot(learner *ChunkConfigLearner, entry *ChunkConfigWALEntry) error {
	if entry.ConfigSnapshot != nil {
		globalConfig := snapshotToConfig(entry.ConfigSnapshot)
		learner.GlobalPriors = globalConfig
	}

	if entry.LearnedParams != nil {
		for domainStr, params := range entry.LearnedParams {
			domain := parseDomain(domainStr)
			if params.Config != nil {
				learner.DomainConfigs[domain] = snapshotToConfig(params.Config)
			}
			learner.DomainConfidence[domain] = params.Confidence
			learner.observationCounts[domain] = params.ObservationCount
		}
	}

	return nil
}

// parseDomain converts a string domain name to Domain type.
func parseDomain(s string) Domain {
	switch s {
	case "code":
		return DomainCode
	case "academic":
		return DomainAcademic
	case "history":
		return DomainHistory
	default:
		return DomainGeneral
	}
}

// snapshotToConfig converts a snapshot back to a ChunkConfig.
func snapshotToConfig(snapshot *ChunkConfigSnapshot) *ChunkConfig {
	if snapshot == nil {
		return nil
	}

	config := &ChunkConfig{
		MaxTokens: snapshot.MaxTokens,
	}

	if snapshot.TargetTokens != nil {
		config.TargetTokens = snapshotToLearnedContextSize(snapshot.TargetTokens)
	}
	if snapshot.MinTokens != nil {
		config.MinTokens = snapshotToLearnedContextSize(snapshot.MinTokens)
	}
	if snapshot.ContextTokensBefore != nil {
		config.ContextTokensBefore = snapshotToLearnedContextSize(snapshot.ContextTokensBefore)
	}
	if snapshot.ContextTokensAfter != nil {
		config.ContextTokensAfter = snapshotToLearnedContextSize(snapshot.ContextTokensAfter)
	}
	if snapshot.OverflowStrategyWeights != nil {
		config.OverflowStrategyWeights = snapshotToOverflowWeights(snapshot.OverflowStrategyWeights)
	}

	return config
}

// snapshotToLearnedContextSize converts a snapshot to LearnedContextSize.
func snapshotToLearnedContextSize(snapshot *LearnedContextSizeSnapshot) *LearnedContextSize {
	return &LearnedContextSize{
		Alpha:            snapshot.Alpha,
		Beta:             snapshot.Beta,
		EffectiveSamples: snapshot.EffectiveSamples,
		PriorAlpha:       snapshot.PriorAlpha,
		PriorBeta:        snapshot.PriorBeta,
	}
}

// snapshotToOverflowWeights converts a snapshot to LearnedOverflowWeights.
func snapshotToOverflowWeights(snapshot *LearnedOverflowWeightsSnapshot) *LearnedOverflowWeights {
	return &LearnedOverflowWeights{
		RecursiveCount: snapshot.RecursiveCount,
		SentenceCount:  snapshot.SentenceCount,
		TruncateCount:  snapshot.TruncateCount,
	}
}

// CreateSnapshot creates a full snapshot entry from the current learner state.
func (w *ChunkConfigWAL) CreateSnapshot(learner *ChunkConfigLearner) (*ChunkConfigWALEntry, error) {
	if learner == nil {
		return nil, fmt.Errorf("learner cannot be nil")
	}

	learner.mu.RLock()
	defer learner.mu.RUnlock()

	entry := &ChunkConfigWALEntry{
		Timestamp:     time.Now(),
		EntryType:     EntryTypeSnapshot,
		ConfigSnapshot: configToSnapshot(learner.GlobalPriors),
		LearnedParams: make(map[string]*DomainLearnedParams),
	}

	for domain, config := range learner.DomainConfigs {
		entry.LearnedParams[domain.String()] = &DomainLearnedParams{
			Domain:           domain.String(),
			Config:           configToSnapshot(config),
			Confidence:       learner.DomainConfidence[domain],
			ObservationCount: learner.observationCounts[domain],
		}
	}

	return entry, nil
}

// configToSnapshot converts a ChunkConfig to a snapshot.
func configToSnapshot(config *ChunkConfig) *ChunkConfigSnapshot {
	if config == nil {
		return nil
	}

	snapshot := &ChunkConfigSnapshot{
		MaxTokens: config.MaxTokens,
	}

	if config.TargetTokens != nil {
		snapshot.TargetTokens = &LearnedContextSizeSnapshot{
			Alpha:            config.TargetTokens.Alpha,
			Beta:             config.TargetTokens.Beta,
			EffectiveSamples: config.TargetTokens.EffectiveSamples,
			PriorAlpha:       config.TargetTokens.PriorAlpha,
			PriorBeta:        config.TargetTokens.PriorBeta,
		}
	}
	if config.MinTokens != nil {
		snapshot.MinTokens = &LearnedContextSizeSnapshot{
			Alpha:            config.MinTokens.Alpha,
			Beta:             config.MinTokens.Beta,
			EffectiveSamples: config.MinTokens.EffectiveSamples,
			PriorAlpha:       config.MinTokens.PriorAlpha,
			PriorBeta:        config.MinTokens.PriorBeta,
		}
	}
	if config.ContextTokensBefore != nil {
		snapshot.ContextTokensBefore = &LearnedContextSizeSnapshot{
			Alpha:            config.ContextTokensBefore.Alpha,
			Beta:             config.ContextTokensBefore.Beta,
			EffectiveSamples: config.ContextTokensBefore.EffectiveSamples,
			PriorAlpha:       config.ContextTokensBefore.PriorAlpha,
			PriorBeta:        config.ContextTokensBefore.PriorBeta,
		}
	}
	if config.ContextTokensAfter != nil {
		snapshot.ContextTokensAfter = &LearnedContextSizeSnapshot{
			Alpha:            config.ContextTokensAfter.Alpha,
			Beta:             config.ContextTokensAfter.Beta,
			EffectiveSamples: config.ContextTokensAfter.EffectiveSamples,
			PriorAlpha:       config.ContextTokensAfter.PriorAlpha,
			PriorBeta:        config.ContextTokensAfter.PriorBeta,
		}
	}
	if config.OverflowStrategyWeights != nil {
		snapshot.OverflowStrategyWeights = &LearnedOverflowWeightsSnapshot{
			RecursiveCount: config.OverflowStrategyWeights.RecursiveCount,
			SentenceCount:  config.OverflowStrategyWeights.SentenceCount,
			TruncateCount:  config.OverflowStrategyWeights.TruncateCount,
		}
	}

	return snapshot
}

// CreateObservationEntry creates a WAL entry for recording an observation.
func (w *ChunkConfigWAL) CreateObservationEntry(obs ChunkUsageObservation, learner *ChunkConfigLearner) (*ChunkConfigWALEntry, error) {
	if learner == nil {
		return nil, fmt.Errorf("learner cannot be nil")
	}

	learner.mu.RLock()
	defer learner.mu.RUnlock()

	entry := &ChunkConfigWALEntry{
		Timestamp:      time.Now(),
		EntryType:      EntryTypeObservation,
		ConfigSnapshot: configToSnapshot(learner.GlobalPriors),
		LearnedParams:  make(map[string]*DomainLearnedParams),
		Observation:    &obs,
	}

	// Include only the domain that was updated
	if config, exists := learner.DomainConfigs[obs.Domain]; exists {
		entry.LearnedParams[obs.Domain.String()] = &DomainLearnedParams{
			Domain:           obs.Domain.String(),
			Config:           configToSnapshot(config),
			Confidence:       learner.DomainConfidence[obs.Domain],
			ObservationCount: learner.observationCounts[obs.Domain],
		}
	}

	return entry, nil
}

// LastSequenceID returns the last sequence ID that was written.
func (w *ChunkConfigWAL) LastSequenceID() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.sequenceID
}

// FilePath returns the path to the WAL file.
func (w *ChunkConfigWAL) FilePath() string {
	return w.filePath
}

// Close closes the WAL, flushing any pending writes.
func (w *ChunkConfigWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrWALClosed
	}

	w.closed = true

	// Flush and sync before closing
	if w.pendingSync {
		if err := w.writer.Flush(); err != nil {
			return fmt.Errorf("failed to flush on close: %w", err)
		}
		if err := w.file.Sync(); err != nil {
			return fmt.Errorf("failed to sync on close: %w", err)
		}
	}

	return w.file.Close()
}

// IsClosed returns whether the WAL has been closed.
func (w *ChunkConfigWAL) IsClosed() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.closed
}

// Truncate removes the WAL file and creates a fresh one.
// This is useful for compaction after taking a full snapshot.
func (w *ChunkConfigWAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrWALClosed
	}

	// Close current file
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush before truncate: %w", err)
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("failed to close before truncate: %w", err)
	}

	// Remove the file
	if err := os.Remove(w.filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove WAL file: %w", err)
	}

	// Reopen fresh file
	file, err := os.OpenFile(w.filePath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to create new WAL file: %w", err)
	}

	w.file = file
	w.writer = bufio.NewWriterSize(file, 64*1024)
	w.sequenceID = 0
	w.pendingSync = false
	w.lastSyncTime = time.Now()

	return nil
}
