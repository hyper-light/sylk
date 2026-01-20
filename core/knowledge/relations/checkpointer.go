package relations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// TCP.1 - EntryType Constants
// =============================================================================

// EntryType identifies the type of checkpoint log entry
type EntryType uint8

const (
	// EntryTypeEdgeAdd logs adding an edge to the graph
	EntryTypeEdgeAdd EntryType = iota + 1

	// EntryTypeEdgeRemove logs removing an edge from the graph
	EntryTypeEdgeRemove

	// EntryTypeDelta logs a delta set of inferred edges
	EntryTypeDelta

	// EntryTypeStratum logs completion of a stratum
	EntryTypeStratum

	// EntryTypeCheckpoint logs a full checkpoint marker
	EntryTypeCheckpoint

	// EntryTypeComputationStart logs start of a new computation
	EntryTypeComputationStart

	// EntryTypeComputationEnd logs completion of a computation
	EntryTypeComputationEnd

	// EntryTypeComputationAbort logs an aborted computation
	EntryTypeComputationAbort
)

// entryTypeStrings maps entry types to their string representations
var entryTypeStrings = map[EntryType]string{
	EntryTypeEdgeAdd:           "EdgeAdd",
	EntryTypeEdgeRemove:        "EdgeRemove",
	EntryTypeDelta:             "Delta",
	EntryTypeStratum:           "Stratum",
	EntryTypeCheckpoint:        "Checkpoint",
	EntryTypeComputationStart:  "ComputationStart",
	EntryTypeComputationEnd:    "ComputationEnd",
	EntryTypeComputationAbort:  "ComputationAbort",
}

// String returns the string representation of an entry type
func (e EntryType) String() string {
	if s, ok := entryTypeStrings[e]; ok {
		return s
	}
	return "Unknown"
}

// IsValid returns true if the entry type is a known type
func (e EntryType) IsValid() bool {
	_, ok := entryTypeStrings[e]
	return ok
}

// AllEntryTypes returns all valid entry types
func AllEntryTypes() []EntryType {
	return []EntryType{
		EntryTypeEdgeAdd,
		EntryTypeEdgeRemove,
		EntryTypeDelta,
		EntryTypeStratum,
		EntryTypeCheckpoint,
		EntryTypeComputationStart,
		EntryTypeComputationEnd,
		EntryTypeComputationAbort,
	}
}

// =============================================================================
// TCP.2 - CheckpointEntry Struct
// =============================================================================

// Validation errors for checkpoint entries
var (
	ErrInvalidEntryType          = errors.New("invalid entry type")
	ErrMissingSubject            = errors.New("edge entry missing subject")
	ErrMissingPredicate          = errors.New("edge entry missing predicate")
	ErrMissingObject             = errors.New("edge entry missing object")
	ErrMissingDeltaEdges         = errors.New("delta entry missing edges")
	ErrInvalidStratum            = errors.New("stratum entry has invalid stratum value")
	ErrMissingCheckpointID       = errors.New("checkpoint entry missing checkpoint_id")
	ErrMissingComputationID      = errors.New("computation entry missing computation_id")
	ErrMissingRuleIDs            = errors.New("computation start entry missing rule_ids")
)

// CheckpointEntry represents a single entry in the checkpoint log.
// Entries are written sequentially and can be replayed to recover state.
type CheckpointEntry struct {
	Type        EntryType `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	SequenceNum uint64    `json:"sequence_num"`

	// EdgeAdd/EdgeRemove payload
	Subject   string `json:"subject,omitempty"`
	Predicate string `json:"predicate,omitempty"`
	Object    string `json:"object,omitempty"`

	// Delta payload - edges inferred in this delta
	DeltaEdges []EdgeKey `json:"delta_edges,omitempty"`

	// Stratum payload
	Stratum int `json:"stratum,omitempty"`

	// Checkpoint payload
	CheckpointID string `json:"checkpoint_id,omitempty"`

	// Computation payload
	ComputationID string   `json:"computation_id,omitempty"`
	RuleIDs       []string `json:"rule_ids,omitempty"`

	// Phase for checkpoint entries (used in recovery)
	Phase    string  `json:"phase,omitempty"`
	Progress float64 `json:"progress,omitempty"`

	// Metadata for additional information (e.g., abort reason)
	Metadata map[string]string `json:"metadata,omitempty"`
}

// NewCheckpointEntry creates a new checkpoint entry with the given type
func NewCheckpointEntry(entryType EntryType) *CheckpointEntry {
	return &CheckpointEntry{
		Type:      entryType,
		Timestamp: time.Now(),
	}
}

// NewEdgeAddEntry creates an entry for adding an edge
func NewEdgeAddEntry(subject, predicate, object string) *CheckpointEntry {
	return &CheckpointEntry{
		Type:      EntryTypeEdgeAdd,
		Timestamp: time.Now(),
		Subject:   subject,
		Predicate: predicate,
		Object:    object,
	}
}

// NewEdgeRemoveEntry creates an entry for removing an edge
func NewEdgeRemoveEntry(subject, predicate, object string) *CheckpointEntry {
	return &CheckpointEntry{
		Type:      EntryTypeEdgeRemove,
		Timestamp: time.Now(),
		Subject:   subject,
		Predicate: predicate,
		Object:    object,
	}
}

// NewDeltaEntry creates an entry for a delta set
func NewDeltaEntry(edges []EdgeKey) *CheckpointEntry {
	// Make a copy of the edges slice to avoid external mutation
	edgesCopy := make([]EdgeKey, len(edges))
	copy(edgesCopy, edges)

	return &CheckpointEntry{
		Type:       EntryTypeDelta,
		Timestamp:  time.Now(),
		DeltaEdges: edgesCopy,
	}
}

// NewStratumEntry creates an entry for stratum completion
func NewStratumEntry(stratum int) *CheckpointEntry {
	return &CheckpointEntry{
		Type:      EntryTypeStratum,
		Timestamp: time.Now(),
		Stratum:   stratum,
	}
}

// NewCheckpointMarker creates a checkpoint marker entry
func NewCheckpointMarker(checkpointID string) *CheckpointEntry {
	return &CheckpointEntry{
		Type:         EntryTypeCheckpoint,
		Timestamp:    time.Now(),
		CheckpointID: checkpointID,
	}
}

// NewComputationStartEntry creates an entry for computation start
func NewComputationStartEntry(computationID string, ruleIDs []string) *CheckpointEntry {
	// Make a copy of the ruleIDs slice to avoid external mutation
	ruleIDsCopy := make([]string, len(ruleIDs))
	copy(ruleIDsCopy, ruleIDs)

	return &CheckpointEntry{
		Type:          EntryTypeComputationStart,
		Timestamp:     time.Now(),
		ComputationID: computationID,
		RuleIDs:       ruleIDsCopy,
	}
}

// NewComputationEndEntry creates an entry for computation end
func NewComputationEndEntry(computationID string) *CheckpointEntry {
	return &CheckpointEntry{
		Type:          EntryTypeComputationEnd,
		Timestamp:     time.Now(),
		ComputationID: computationID,
	}
}

// Validate checks that the entry is well-formed for its type
func (e *CheckpointEntry) Validate() error {
	if !e.Type.IsValid() {
		return ErrInvalidEntryType
	}

	switch e.Type {
	case EntryTypeEdgeAdd, EntryTypeEdgeRemove:
		if e.Subject == "" {
			return ErrMissingSubject
		}
		if e.Predicate == "" {
			return ErrMissingPredicate
		}
		if e.Object == "" {
			return ErrMissingObject
		}

	case EntryTypeDelta:
		if e.DeltaEdges == nil || len(e.DeltaEdges) == 0 {
			return ErrMissingDeltaEdges
		}

	case EntryTypeStratum:
		if e.Stratum < 0 {
			return ErrInvalidStratum
		}

	case EntryTypeCheckpoint:
		if e.CheckpointID == "" {
			return ErrMissingCheckpointID
		}

	case EntryTypeComputationStart:
		if e.ComputationID == "" {
			return ErrMissingComputationID
		}
		if e.RuleIDs == nil || len(e.RuleIDs) == 0 {
			return ErrMissingRuleIDs
		}

	case EntryTypeComputationEnd:
		if e.ComputationID == "" {
			return ErrMissingComputationID
		}

	case EntryTypeComputationAbort:
		if e.ComputationID == "" {
			return ErrMissingComputationID
		}
	}

	return nil
}

// Size returns the approximate serialized size of the entry in bytes.
// This is an estimate based on JSON serialization overhead.
func (e *CheckpointEntry) Size() int {
	// Base overhead: type (1), timestamp (~30), sequence_num (~10), JSON structure (~50)
	size := 91

	// Add string field sizes
	size += len(e.Subject)
	size += len(e.Predicate)
	size += len(e.Object)
	size += len(e.CheckpointID)
	size += len(e.ComputationID)

	// Add delta edges size (each edge has 3 strings + JSON overhead)
	for _, edge := range e.DeltaEdges {
		size += len(edge.Subject) + len(edge.Predicate) + len(edge.Object) + 30
	}

	// Add rule IDs size
	for _, ruleID := range e.RuleIDs {
		size += len(ruleID) + 3 // quotes and comma
	}

	return size
}

// Clone creates a deep copy of the entry
func (e *CheckpointEntry) Clone() *CheckpointEntry {
	if e == nil {
		return nil
	}

	clone := &CheckpointEntry{
		Type:          e.Type,
		Timestamp:     e.Timestamp,
		SequenceNum:   e.SequenceNum,
		Subject:       e.Subject,
		Predicate:     e.Predicate,
		Object:        e.Object,
		Stratum:       e.Stratum,
		CheckpointID:  e.CheckpointID,
		ComputationID: e.ComputationID,
		Phase:         e.Phase,
		Progress:      e.Progress,
	}

	// Deep copy DeltaEdges slice
	if e.DeltaEdges != nil {
		clone.DeltaEdges = make([]EdgeKey, len(e.DeltaEdges))
		copy(clone.DeltaEdges, e.DeltaEdges)
	}

	// Deep copy RuleIDs slice
	if e.RuleIDs != nil {
		clone.RuleIDs = make([]string, len(e.RuleIDs))
		copy(clone.RuleIDs, e.RuleIDs)
	}

	// Deep copy Metadata map
	if e.Metadata != nil {
		clone.Metadata = make(map[string]string, len(e.Metadata))
		for k, v := range e.Metadata {
			clone.Metadata[k] = v
		}
	}

	return clone
}

// MarshalJSON implements json.Marshaler for CheckpointEntry
func (e *CheckpointEntry) MarshalJSON() ([]byte, error) {
	type Alias CheckpointEntry
	return json.Marshal(&struct {
		Type string `json:"type"`
		*Alias
	}{
		Type:  e.Type.String(),
		Alias: (*Alias)(e),
	})
}

// UnmarshalJSON implements json.Unmarshaler for CheckpointEntry
func (e *CheckpointEntry) UnmarshalJSON(data []byte) error {
	type Alias CheckpointEntry
	aux := &struct {
		Type string `json:"type"`
		*Alias
	}{
		Alias: (*Alias)(e),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Parse the type string back to EntryType
	for entryType, name := range entryTypeStrings {
		if name == aux.Type {
			e.Type = entryType
			return nil
		}
	}

	return ErrInvalidEntryType
}

// =============================================================================
// TCP.3 - ComputationState Struct
// =============================================================================

// ComputationState tracks the state of an in-progress transitive closure computation.
// This is used during recovery to resume computations.
type ComputationState struct {
	ComputationID  string              // Unique identifier for this computation
	RuleIDs        []string            // Rules being evaluated
	CurrentStratum int                 // Current stratum being processed
	TotalStrata    int                 // Total number of strata
	DeltasSeen     int                 // Number of delta entries processed
	EdgeCount      int                 // Total edges added so far
	StartedAt      time.Time           // When computation started
	LastUpdate     time.Time           // Last progress update
	Completed      bool                // Whether computation finished
	Phase            string              // Current phase of computation (for recovery)
	ProgressValue    float64             // Progress percentage (0.0 - 1.0) for recovery
	PendingEntries   []*CheckpointEntry  // Entries to replay during recovery
}

// NewComputationState creates a new computation state
func NewComputationState(computationID string, ruleIDs []string) *ComputationState {
	// Make a copy of ruleIDs to avoid external mutation
	ruleIDsCopy := make([]string, len(ruleIDs))
	copy(ruleIDsCopy, ruleIDs)

	now := time.Now()
	return &ComputationState{
		ComputationID:  computationID,
		RuleIDs:        ruleIDsCopy,
		CurrentStratum: 0,
		TotalStrata:    0,
		DeltasSeen:     0,
		EdgeCount:      0,
		StartedAt:      now,
		LastUpdate:     now,
		Completed:      false,
	}
}

// AdvanceStratum marks the current stratum as complete and moves to the next
func (cs *ComputationState) AdvanceStratum() {
	cs.CurrentStratum++
	cs.LastUpdate = time.Now()
}

// AddDelta records that a delta entry was processed
func (cs *ComputationState) AddDelta(edgeCount int) {
	cs.DeltasSeen++
	cs.EdgeCount += edgeCount
	cs.LastUpdate = time.Now()
}

// MarkCompleted marks the computation as finished
func (cs *ComputationState) MarkCompleted() {
	cs.Completed = true
	cs.LastUpdate = time.Now()
}

// Duration returns the time elapsed since start
func (cs *ComputationState) Duration() time.Duration {
	if cs.Completed {
		return cs.LastUpdate.Sub(cs.StartedAt)
	}
	return time.Since(cs.StartedAt)
}

// Progress returns completion percentage (0.0 - 1.0)
func (cs *ComputationState) Progress() float64 {
	if cs.Completed {
		return 1.0
	}
	if cs.TotalStrata == 0 {
		return 0.0
	}
	return float64(cs.CurrentStratum) / float64(cs.TotalStrata)
}

// Clone creates a deep copy of the computation state
func (cs *ComputationState) Clone() *ComputationState {
	if cs == nil {
		return nil
	}

	// Make a deep copy of RuleIDs
	ruleIDsCopy := make([]string, len(cs.RuleIDs))
	copy(ruleIDsCopy, cs.RuleIDs)

	// Make a deep copy of PendingEntries
	var pendingEntriesCopy []*CheckpointEntry
	if cs.PendingEntries != nil {
		pendingEntriesCopy = make([]*CheckpointEntry, len(cs.PendingEntries))
		for i, entry := range cs.PendingEntries {
			pendingEntriesCopy[i] = entry.Clone()
		}
	}

	return &ComputationState{
		ComputationID:  cs.ComputationID,
		RuleIDs:        ruleIDsCopy,
		CurrentStratum: cs.CurrentStratum,
		TotalStrata:    cs.TotalStrata,
		DeltasSeen:     cs.DeltasSeen,
		EdgeCount:      cs.EdgeCount,
		StartedAt:      cs.StartedAt,
		LastUpdate:     cs.LastUpdate,
		Completed:      cs.Completed,
		Phase:          cs.Phase,
		ProgressValue:  cs.ProgressValue,
		PendingEntries: pendingEntriesCopy,
	}
}

// =============================================================================
// TCP.4 - TCCheckpointer Struct
// =============================================================================

// Default configuration values for TCCheckpointer
const (
	DefaultCheckpointInterval = 5 * time.Minute
	DefaultBufferSize         = 100
)

// Errors for TCCheckpointer operations
var (
	ErrCheckpointerClosed      = errors.New("checkpointer is closed")
	ErrCheckpointerNotOpen     = errors.New("checkpointer is not open")
	ErrCheckpointerAlreadyOpen = errors.New("checkpointer is already open")
	ErrInvalidLogPath          = errors.New("invalid log path")
	ErrComputationInProgress   = errors.New("computation already in progress")
)

// TCCheckpointer provides write-ahead logging for transitive closure computations.
// It allows computations to be recovered and resumed after crashes.
type TCCheckpointer struct {
	mu                 sync.Mutex
	logFile            *os.File             // WAL file handle
	logPath            string               // Path to WAL file
	encoder            *json.Encoder        // JSON encoder for entries
	sequenceNum        atomic.Uint64        // Entry sequence counter
	currentComp        *ComputationState    // Current in-progress computation
	lastCheckpoint     time.Time            // Time of last checkpoint
	lastFlush          time.Time            // Time of last buffer flush
	checkpointInterval time.Duration        // How often to create checkpoints
	bufferSize         int                  // Entries to buffer before flush
	buffer             []*CheckpointEntry   // Buffered entries
	closed             bool                 // Whether checkpointer is closed
	activeComputation  *ComputationHandle   // Active computation handle
}

// NewTCCheckpointer creates a new checkpointer writing to the given path
func NewTCCheckpointer(logPath string) (*TCCheckpointer, error) {
	if logPath == "" {
		return nil, ErrInvalidLogPath
	}

	return &TCCheckpointer{
		logPath:            logPath,
		checkpointInterval: DefaultCheckpointInterval,
		bufferSize:         DefaultBufferSize,
		buffer:             make([]*CheckpointEntry, 0, DefaultBufferSize),
		closed:             true, // Not yet opened
	}, nil
}

// Open opens or creates the WAL file
func (c *TCCheckpointer) Open() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		return ErrCheckpointerAlreadyOpen
	}

	// Open file for append (create if not exists)
	file, err := os.OpenFile(c.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	c.logFile = file
	c.encoder = json.NewEncoder(file)
	c.lastCheckpoint = time.Now()
	c.closed = false

	return nil
}

// Close flushes and closes the WAL file.
// If there is an active computation, it will be aborted with reason "checkpointer closed".
func (c *TCCheckpointer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil // Already closed, idempotent
	}

	// End any active computation
	if c.activeComputation != nil {
		entry := &CheckpointEntry{
			Type:          EntryTypeComputationAbort,
			SequenceNum:   c.sequenceNum.Add(1),
			Timestamp:     time.Now(),
			ComputationID: c.activeComputation.ID,
			Metadata:      map[string]string{"reason": "checkpointer closed"},
		}
		c.buffer = append(c.buffer, entry)
		c.activeComputation = nil
		c.currentComp = nil
	}

	// Flush remaining buffer
	if err := c.flushLocked(); err != nil {
		return fmt.Errorf("final flush: %w", err)
	}

	// Close the file
	if c.logFile != nil {
		if err := c.logFile.Close(); err != nil {
			return fmt.Errorf("close file: %w", err)
		}
		c.logFile = nil
	}

	c.encoder = nil
	c.closed = true
	c.buffer = make([]*CheckpointEntry, 0, c.bufferSize)

	return nil
}

// IsOpen returns true if the checkpointer is open
func (c *TCCheckpointer) IsOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed
}

// GetLogPath returns the WAL file path
func (c *TCCheckpointer) GetLogPath() string {
	return c.logPath
}

// GetCurrentComputation returns the current computation state
func (c *TCCheckpointer) GetCurrentComputation() *ComputationState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentComp.Clone()
}

// GetSequenceNum returns the current sequence number
func (c *TCCheckpointer) GetSequenceNum() uint64 {
	return c.sequenceNum.Load()
}

// SetCheckpointInterval sets how often checkpoints are created
func (c *TCCheckpointer) SetCheckpointInterval(interval time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkpointInterval = interval
}

// SetBufferSize sets the entry buffer size
func (c *TCCheckpointer) SetBufferSize(size int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if size < 1 {
		size = 1
	}

	c.bufferSize = size

	// Resize buffer if needed (preserve existing entries up to new capacity)
	if len(c.buffer) > size {
		c.buffer = c.buffer[:size]
	}
}

// =============================================================================
// TCP.5 - writeEntry Implementation
// =============================================================================

// writeEntry writes a single entry to the WAL
// Acquires lock, validates entry, assigns sequence number, writes to file
func (c *TCCheckpointer) writeEntry(entry *CheckpointEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCheckpointerClosed
	}
	if c.logFile == nil {
		return ErrCheckpointerNotOpen
	}

	// Validate entry
	if err := entry.Validate(); err != nil {
		return fmt.Errorf("invalid entry: %w", err)
	}

	// Assign sequence number and timestamp
	entry.SequenceNum = c.sequenceNum.Add(1)
	entry.Timestamp = time.Now()

	// Add to buffer
	c.buffer = append(c.buffer, entry)

	// Flush if buffer is full
	if len(c.buffer) >= c.bufferSize {
		return c.flushLocked()
	}

	return nil
}

// flushLocked writes all buffered entries to disk.
// Caller must hold mutex.
func (c *TCCheckpointer) flushLocked() error {
	if len(c.buffer) == 0 {
		return nil
	}

	// Encode and write each entry
	for _, entry := range c.buffer {
		if err := c.encoder.Encode(entry); err != nil {
			return fmt.Errorf("encode entry: %w", err)
		}
	}

	// Clear buffer before sync to avoid re-encoding on retry
	c.buffer = c.buffer[:0]

	// Sync to disk
	if c.logFile != nil {
		if err := c.logFile.Sync(); err != nil {
			return fmt.Errorf("sync file: %w", err)
		}
	}

	c.lastFlush = time.Now()
	return nil
}

// Flush flushes buffered entries to disk
func (c *TCCheckpointer) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCheckpointerClosed
	}
	if c.logFile == nil {
		return ErrCheckpointerNotOpen
	}

	return c.flushLocked()
}

// =============================================================================
// TCP.6 - Log Methods
// =============================================================================

// LogEdgeAdd logs an edge addition
func (c *TCCheckpointer) LogEdgeAdd(subject, predicate, object string) error {
	entry := NewEdgeAddEntry(subject, predicate, object)
	return c.writeEntry(entry)
}

// LogEdgeRemove logs an edge removal
func (c *TCCheckpointer) LogEdgeRemove(subject, predicate, object string) error {
	entry := NewEdgeRemoveEntry(subject, predicate, object)
	return c.writeEntry(entry)
}

// LogDelta logs a delta set of inferred edges
func (c *TCCheckpointer) LogDelta(edges []EdgeKey) error {
	entry := NewDeltaEntry(edges)

	// Update computation state
	c.mu.Lock()
	if c.currentComp != nil {
		c.currentComp.AddDelta(len(edges))
	}
	c.mu.Unlock()

	return c.writeEntry(entry)
}

// LogStratum logs completion of a stratum
func (c *TCCheckpointer) LogStratum(stratum int) error {
	entry := NewStratumEntry(stratum)

	// Update computation state
	c.mu.Lock()
	if c.currentComp != nil {
		c.currentComp.AdvanceStratum()
	}
	c.mu.Unlock()

	return c.writeEntry(entry)
}

// LogCheckpoint logs a checkpoint marker
func (c *TCCheckpointer) LogCheckpoint() error {
	checkpointID := fmt.Sprintf("ckpt-%d", time.Now().UnixNano())
	entry := NewCheckpointMarker(checkpointID)

	c.mu.Lock()
	c.lastCheckpoint = time.Now()
	c.mu.Unlock()

	// Force flush on checkpoint
	if err := c.writeEntry(entry); err != nil {
		return err
	}
	return c.Flush()
}

// StartComputation logs start of a new computation
func (c *TCCheckpointer) StartComputation(computationID string, ruleIDs []string) error {
	entry := NewComputationStartEntry(computationID, ruleIDs)

	// Initialize computation state
	c.mu.Lock()
	c.currentComp = NewComputationState(computationID, ruleIDs)
	c.mu.Unlock()

	return c.writeEntry(entry)
}

// EndComputation logs end of a computation
func (c *TCCheckpointer) EndComputation(computationID string) error {
	entry := NewComputationEndEntry(computationID)

	// Mark computation complete
	c.mu.Lock()
	if c.currentComp != nil && c.currentComp.ComputationID == computationID {
		c.currentComp.MarkCompleted()
	}
	c.mu.Unlock()

	// Force flush
	if err := c.writeEntry(entry); err != nil {
		return err
	}
	return c.Flush()
}

// ShouldCheckpoint returns true if a checkpoint is due
func (c *TCCheckpointer) ShouldCheckpoint() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return time.Since(c.lastCheckpoint) >= c.checkpointInterval
}

// =============================================================================
// TCP.7 - recover Implementation
// =============================================================================

// recover replays WAL entries from a reader to restore computation state.
// Returns the ComputationState if a computation was in progress, nil otherwise.
func (c *TCCheckpointer) recover(reader io.Reader) (*ComputationState, error) {
	decoder := json.NewDecoder(reader)

	var lastCheckpoint *ComputationState
	var pendingEntries []*CheckpointEntry

	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode entry: %w", err)
		}

		switch entry.Type {
		case EntryTypeCheckpoint:
			// Checkpoint marks a consistent state
			lastCheckpoint = &ComputationState{
				ComputationID: entry.ComputationID,
				Phase:         entry.Phase,
				ProgressValue: entry.Progress,
				StartedAt:     entry.Timestamp,
				LastUpdate:    entry.Timestamp,
			}
			pendingEntries = nil // Clear pending entries after checkpoint

		case EntryTypeComputationStart:
			// New computation started
			lastCheckpoint = &ComputationState{
				ComputationID: entry.ComputationID,
				RuleIDs:       entry.RuleIDs,
				Phase:         "started",
				ProgressValue: 0,
				StartedAt:     entry.Timestamp,
				LastUpdate:    entry.Timestamp,
			}
			pendingEntries = nil

		case EntryTypeComputationEnd:
			// Computation completed - nothing to recover
			lastCheckpoint = nil
			pendingEntries = nil

		case EntryTypeComputationAbort:
			// Computation aborted - nothing to recover
			lastCheckpoint = nil
			pendingEntries = nil

		case EntryTypeEdgeAdd, EntryTypeEdgeRemove, EntryTypeDelta, EntryTypeStratum:
			// Queue pending entries to replay if needed
			entryCopy := entry.Clone()
			pendingEntries = append(pendingEntries, entryCopy)
		}
	}

	// If we have a checkpoint with pending entries, return state for replay
	if lastCheckpoint != nil {
		lastCheckpoint.PendingEntries = pendingEntries
		return lastCheckpoint, nil
	}

	return nil, nil // Nothing to recover
}

// RecoverFromFile opens a WAL file and recovers state.
// Returns nil, nil if no WAL file exists or nothing to recover.
func (c *TCCheckpointer) RecoverFromFile(path string) (*ComputationState, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No WAL file, nothing to recover
		}
		return nil, fmt.Errorf("open WAL: %w", err)
	}
	defer file.Close()

	return c.recover(file)
}

// =============================================================================
// TCP.8 - StartComputation with Handle Implementation
// =============================================================================

// ComputationHandle allows tracking and updating computation progress.
type ComputationHandle struct {
	ID           string
	State        *ComputationState
	checkpointer *TCCheckpointer
	ctx          context.Context
}

// UpdateProgress updates the computation progress.
// Checkpoints if the threshold is reached.
func (h *ComputationHandle) UpdateProgress(phase string, progress float64) error {
	h.State.Phase = phase
	h.State.ProgressValue = progress
	h.State.LastUpdate = time.Now()

	// Checkpoint if threshold reached
	if h.checkpointer.ShouldCheckpoint() {
		return h.checkpointer.LogCheckpointWithProgress(h.ID, phase, progress)
	}
	return nil
}

// Complete marks the computation as finished.
func (h *ComputationHandle) Complete() error {
	return h.checkpointer.EndComputation(h.ID)
}

// Abort cancels the computation with a reason.
func (h *ComputationHandle) Abort(reason string) error {
	return h.checkpointer.AbortComputation(h.ID, reason)
}

// Context returns the context associated with this computation.
func (h *ComputationHandle) Context() context.Context {
	return h.ctx
}

// StartComputationWithHandle begins a new transitive closure computation.
// Logs the start event and returns a ComputationHandle for tracking.
func (c *TCCheckpointer) StartComputationWithHandle(ctx context.Context, computationID string, ruleIDs []string) (*ComputationHandle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.activeComputation != nil {
		return nil, ErrComputationInProgress
	}

	if c.closed {
		return nil, ErrCheckpointerClosed
	}
	if c.logFile == nil {
		return nil, ErrCheckpointerNotOpen
	}

	// Create entry
	entry := NewComputationStartEntry(computationID, ruleIDs)
	entry.SequenceNum = c.sequenceNum.Add(1)
	entry.Timestamp = time.Now()

	// Add to buffer
	c.buffer = append(c.buffer, entry)

	// Flush if buffer is full
	if len(c.buffer) >= c.bufferSize {
		if err := c.flushLocked(); err != nil {
			return nil, fmt.Errorf("log computation start: %w", err)
		}
	}

	state := &ComputationState{
		ComputationID: computationID,
		RuleIDs:       ruleIDs,
		Phase:         "init",
		ProgressValue: 0,
		StartedAt:     entry.Timestamp,
		LastUpdate:    entry.Timestamp,
	}

	handle := &ComputationHandle{
		ID:           computationID,
		State:        state,
		checkpointer: c,
		ctx:          ctx,
	}

	c.activeComputation = handle
	c.currentComp = state

	return handle, nil
}

// AbortComputation marks a computation as aborted.
func (c *TCCheckpointer) AbortComputation(computationID, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCheckpointerClosed
	}
	if c.logFile == nil {
		return ErrCheckpointerNotOpen
	}

	entry := &CheckpointEntry{
		Type:          EntryTypeComputationAbort,
		SequenceNum:   c.sequenceNum.Add(1),
		Timestamp:     time.Now(),
		ComputationID: computationID,
		Metadata:      map[string]string{"reason": reason},
	}

	// Add to buffer
	c.buffer = append(c.buffer, entry)

	// Flush immediately on abort
	if err := c.flushLocked(); err != nil {
		return err
	}

	c.activeComputation = nil
	c.currentComp = nil

	return nil
}

// ResumeComputation continues a recovered computation.
// Returns a ComputationHandle for the resumed computation.
func (c *TCCheckpointer) ResumeComputation(ctx context.Context, state *ComputationState) (*ComputationHandle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.activeComputation != nil {
		return nil, ErrComputationInProgress
	}

	handle := &ComputationHandle{
		ID:           state.ComputationID,
		State:        state,
		checkpointer: c,
		ctx:          ctx,
	}

	c.activeComputation = handle
	c.currentComp = state

	return handle, nil
}

// LogCheckpointWithProgress logs a checkpoint with phase and progress info.
func (c *TCCheckpointer) LogCheckpointWithProgress(computationID, phase string, progress float64) error {
	checkpointID := fmt.Sprintf("ckpt-%d", time.Now().UnixNano())
	entry := &CheckpointEntry{
		Type:          EntryTypeCheckpoint,
		Timestamp:     time.Now(),
		CheckpointID:  checkpointID,
		ComputationID: computationID,
		Phase:         phase,
		Progress:      progress,
	}

	c.mu.Lock()
	c.lastCheckpoint = time.Now()
	c.mu.Unlock()

	// Force flush on checkpoint
	if err := c.writeEntry(entry); err != nil {
		return err
	}
	return c.Flush()
}

// GetActiveComputation returns the current active computation handle.
func (c *TCCheckpointer) GetActiveComputation() *ComputationHandle {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeComputation
}

// ClearActiveComputation clears the active computation state.
// This is useful after recovery when the computation has been fully processed.
func (c *TCCheckpointer) ClearActiveComputation() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activeComputation = nil
	c.currentComp = nil
}

// LogStratumWithComputation logs stratum completion with computation context.
func (c *TCCheckpointer) LogStratumWithComputation(computationID string, stratum int, metadata map[string]string) error {
	entry := &CheckpointEntry{
		Type:          EntryTypeStratum,
		Timestamp:     time.Now(),
		Stratum:       stratum,
		ComputationID: computationID,
		Metadata:      metadata,
	}

	// Update computation state
	c.mu.Lock()
	if c.currentComp != nil && c.currentComp.ComputationID == computationID {
		c.currentComp.AdvanceStratum()
	}
	c.mu.Unlock()

	return c.writeEntry(entry)
}

// LogEdgeAddWithComputation logs an edge addition with computation context.
func (c *TCCheckpointer) LogEdgeAddWithComputation(computationID string, edge EdgeKey) error {
	entry := &CheckpointEntry{
		Type:          EntryTypeEdgeAdd,
		Timestamp:     time.Now(),
		Subject:       edge.Subject,
		Predicate:     edge.Predicate,
		Object:        edge.Object,
		ComputationID: computationID,
	}
	return c.writeEntry(entry)
}

// =============================================================================
// TCP.10 - CheckpointedEvaluator Implementation
// =============================================================================

// CheckpointedEvaluator wraps SemiNaiveEvaluator with checkpointing support.
// It provides crash recovery capabilities by logging computation progress.
type CheckpointedEvaluator struct {
	evaluator    *SemiNaiveEvaluator
	checkpointer *TCCheckpointer
}

// NewCheckpointedEvaluator creates an evaluator with checkpoint support.
func NewCheckpointedEvaluator(
	evaluator *SemiNaiveEvaluator,
	checkpointer *TCCheckpointer,
) *CheckpointedEvaluator {
	return &CheckpointedEvaluator{
		evaluator:    evaluator,
		checkpointer: checkpointer,
	}
}

// GetEvaluator returns the underlying SemiNaiveEvaluator.
func (ce *CheckpointedEvaluator) GetEvaluator() *SemiNaiveEvaluator {
	return ce.evaluator
}

// GetCheckpointer returns the underlying TCCheckpointer.
func (ce *CheckpointedEvaluator) GetCheckpointer() *TCCheckpointer {
	return ce.checkpointer
}

// getRuleIDs extracts rule IDs from the evaluator's rules.
func (ce *CheckpointedEvaluator) getRuleIDs() []string {
	rules := ce.evaluator.rules
	ruleIDs := make([]string, len(rules))
	for i, rule := range rules {
		ruleIDs[i] = rule.ID
	}
	return ruleIDs
}

// generateComputationID creates a unique computation identifier.
func generateComputationID() string {
	return fmt.Sprintf("comp-%d", time.Now().UnixNano())
}

// Evaluate performs evaluation with checkpoint logging.
// It logs the computation start, progress through strata, and completion.
func (ce *CheckpointedEvaluator) Evaluate(ctx context.Context, db Database) (int, error) {
	// Generate computation ID
	computationID := generateComputationID()

	// Start computation with checkpointing
	handle, err := ce.checkpointer.StartComputationWithHandle(ctx, computationID, ce.getRuleIDs())
	if err != nil {
		return 0, fmt.Errorf("start computation: %w", err)
	}

	// Set up panic recovery
	defer func() {
		if r := recover(); r != nil {
			handle.Abort(fmt.Sprintf("panic: %v", r))
			panic(r) // Re-panic after logging
		}
	}()

	totalDerived := 0

	// Run evaluation with progress updates
	maxStratum := ce.evaluator.stratifier.MaxStratum()
	for stratum := 0; stratum <= maxStratum; stratum++ {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			handle.Abort(fmt.Sprintf("context cancelled: %v", ctx.Err()))
			return totalDerived, ctx.Err()
		default:
		}

		// Log stratum start
		if err := ce.checkpointer.LogStratumWithComputation(computationID, stratum, nil); err != nil {
			handle.Abort(fmt.Sprintf("log stratum: %v", err))
			return totalDerived, fmt.Errorf("log stratum %d: %w", stratum, err)
		}

		// Evaluate stratum
		derived, err := ce.evaluator.evaluateStratum(stratum, db)
		if err != nil {
			handle.Abort(err.Error())
			return totalDerived, fmt.Errorf("evaluate stratum %d: %w", stratum, err)
		}
		totalDerived += derived

		// Update progress
		progress := float64(stratum+1) / float64(maxStratum+1)
		if err := handle.UpdateProgress(fmt.Sprintf("stratum_%d", stratum), progress); err != nil {
			// Non-fatal error for progress update
			// Log but continue evaluation
		}
	}

	// Complete computation
	if err := handle.Complete(); err != nil {
		return totalDerived, fmt.Errorf("complete computation: %w", err)
	}

	return totalDerived, nil
}

// EvaluateWithEdgeLogging performs evaluation with detailed edge logging.
// This variant logs each derived edge individually, which is useful for recovery
// but has higher overhead.
func (ce *CheckpointedEvaluator) EvaluateWithEdgeLogging(ctx context.Context, db Database) (int, error) {
	// Generate computation ID
	computationID := generateComputationID()

	// Start computation with checkpointing
	handle, err := ce.checkpointer.StartComputationWithHandle(ctx, computationID, ce.getRuleIDs())
	if err != nil {
		return 0, fmt.Errorf("start computation: %w", err)
	}

	// Set up panic recovery
	defer func() {
		if r := recover(); r != nil {
			handle.Abort(fmt.Sprintf("panic: %v", r))
			panic(r)
		}
	}()

	totalDerived := 0

	// Run evaluation with edge logging
	maxStratum := ce.evaluator.stratifier.MaxStratum()
	for stratum := 0; stratum <= maxStratum; stratum++ {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			handle.Abort(fmt.Sprintf("context cancelled: %v", ctx.Err()))
			return totalDerived, ctx.Err()
		default:
		}

		// Log stratum start
		if err := ce.checkpointer.LogStratumWithComputation(computationID, stratum, nil); err != nil {
			handle.Abort(fmt.Sprintf("log stratum: %v", err))
			return totalDerived, fmt.Errorf("log stratum %d: %w", stratum, err)
		}

		// Evaluate stratum and log each new edge
		derived, err := ce.evaluateStratumWithLogging(ctx, stratum, db, computationID)
		if err != nil {
			handle.Abort(err.Error())
			return totalDerived, fmt.Errorf("evaluate stratum %d: %w", stratum, err)
		}
		totalDerived += derived

		// Update progress
		progress := float64(stratum+1) / float64(maxStratum+1)
		if err := handle.UpdateProgress(fmt.Sprintf("stratum_%d", stratum), progress); err != nil {
			// Non-fatal
		}
	}

	// Complete computation
	if err := handle.Complete(); err != nil {
		return totalDerived, fmt.Errorf("complete computation: %w", err)
	}

	return totalDerived, nil
}

// evaluateStratumWithLogging evaluates a stratum and logs each derived edge.
func (ce *CheckpointedEvaluator) evaluateStratumWithLogging(
	ctx context.Context,
	stratum int,
	db Database,
	computationID string,
) (int, error) {
	rulesByStratum := ce.evaluator.stratifier.GetRulesByStratum()
	rules := rulesByStratum[stratum]
	if len(rules) == 0 {
		return 0, nil
	}

	totalDerived := 0

	// Initialize deltas for each predicate produced by rules in this stratum
	deltas := make(map[string]*DeltaTable)
	for _, rule := range rules {
		if _, exists := deltas[rule.HeadPredicate]; !exists {
			deltas[rule.HeadPredicate] = NewDeltaTable()
		}
	}

	// Initial application
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		newDelta, err := ce.evaluator.applyRuleInitial(rule, db)
		if err != nil {
			return totalDerived, err
		}
		for _, edge := range newDelta.Edges() {
			if db.AddEdge(edge) {
				deltas[rule.HeadPredicate].Add(edge)
				totalDerived++
				// Log the derived edge
				if err := ce.checkpointer.LogEdgeAddWithComputation(computationID, edge); err != nil {
					return totalDerived, fmt.Errorf("log edge: %w", err)
				}
			}
		}
	}

	// Iterative semi-naive evaluation
	for iteration := 0; iteration < ce.evaluator.maxIterations; iteration++ {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return totalDerived, ctx.Err()
		default:
		}

		// Check if any delta is non-empty
		anyNonEmpty := false
		for _, delta := range deltas {
			if !delta.IsEmpty() {
				anyNonEmpty = true
				break
			}
		}
		if !anyNonEmpty {
			break // Fixpoint reached
		}

		// Compute new deltas
		newDeltas := make(map[string]*DeltaTable)
		for _, rule := range rules {
			if _, exists := newDeltas[rule.HeadPredicate]; !exists {
				newDeltas[rule.HeadPredicate] = NewDeltaTable()
			}
		}

		for _, rule := range rules {
			if !rule.Enabled {
				continue
			}
			newDelta, err := ce.evaluator.applyGeneralRuleSemiNaive(rule, deltas, db)
			if err != nil {
				return totalDerived, err
			}
			for _, edge := range newDelta.Edges() {
				if db.AddEdge(edge) {
					newDeltas[rule.HeadPredicate].Add(edge)
					totalDerived++
					// Log the derived edge
					if err := ce.checkpointer.LogEdgeAddWithComputation(computationID, edge); err != nil {
						return totalDerived, fmt.Errorf("log edge: %w", err)
					}
				}
			}
		}

		deltas = newDeltas
	}

	return totalDerived, nil
}

// ResumeFromCheckpoint continues a computation from a recovered checkpoint state.
// It replays pending entries and continues evaluation from the last completed stratum.
func (ce *CheckpointedEvaluator) ResumeFromCheckpoint(
	ctx context.Context,
	db Database,
	state *ComputationState,
) (int, error) {
	if state == nil {
		return 0, errors.New("nil computation state")
	}

	// Resume the computation handle
	handle, err := ce.checkpointer.ResumeComputation(ctx, state)
	if err != nil {
		return 0, fmt.Errorf("resume computation: %w", err)
	}

	// Set up panic recovery
	defer func() {
		if r := recover(); r != nil {
			handle.Abort(fmt.Sprintf("panic during recovery: %v", r))
			panic(r)
		}
	}()

	// Replay pending entries to restore database state
	for _, entry := range state.PendingEntries {
		switch entry.Type {
		case EntryTypeEdgeAdd:
			edge := EdgeKey{
				Subject:   entry.Subject,
				Predicate: entry.Predicate,
				Object:    entry.Object,
			}
			db.AddEdge(edge)
		case EntryTypeEdgeRemove:
			// For databases that support removal
			if imdb, ok := db.(*InMemoryDatabase); ok {
				edge := EdgeKey{
					Subject:   entry.Subject,
					Predicate: entry.Predicate,
					Object:    entry.Object,
				}
				imdb.RemoveEdge(edge)
			}
		}
	}

	totalDerived := 0

	// Continue from the last completed stratum
	startStratum := state.CurrentStratum
	maxStratum := ce.evaluator.stratifier.MaxStratum()

	for stratum := startStratum; stratum <= maxStratum; stratum++ {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			handle.Abort(fmt.Sprintf("context cancelled during recovery: %v", ctx.Err()))
			return totalDerived, ctx.Err()
		default:
		}

		// Log stratum start
		if err := ce.checkpointer.LogStratumWithComputation(state.ComputationID, stratum, nil); err != nil {
			handle.Abort(fmt.Sprintf("log stratum: %v", err))
			return totalDerived, fmt.Errorf("log stratum %d: %w", stratum, err)
		}

		// Evaluate stratum
		derived, err := ce.evaluator.evaluateStratum(stratum, db)
		if err != nil {
			handle.Abort(err.Error())
			return totalDerived, fmt.Errorf("evaluate stratum %d: %w", stratum, err)
		}
		totalDerived += derived

		// Update progress
		progress := float64(stratum+1) / float64(maxStratum+1)
		if err := handle.UpdateProgress(fmt.Sprintf("stratum_%d_recovery", stratum), progress); err != nil {
			// Non-fatal
		}
	}

	// Complete computation
	if err := handle.Complete(); err != nil {
		return totalDerived, fmt.Errorf("complete computation: %w", err)
	}

	return totalDerived, nil
}

// RecoverAndEvaluate recovers from a WAL file and continues evaluation.
// Returns the total number of new edges derived (including replayed ones).
func (ce *CheckpointedEvaluator) RecoverAndEvaluate(
	ctx context.Context,
	db Database,
	walPath string,
) (int, bool, error) {
	// Try to recover from WAL
	state, err := ce.checkpointer.RecoverFromFile(walPath)
	if err != nil {
		return 0, false, fmt.Errorf("recover from WAL: %w", err)
	}

	// If no recovery needed, start fresh evaluation
	if state == nil {
		derived, err := ce.Evaluate(ctx, db)
		return derived, false, err
	}

	// Resume from recovered state
	derived, err := ce.ResumeFromCheckpoint(ctx, db, state)
	return derived, true, err
}
