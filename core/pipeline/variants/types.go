// Package variants provides pipeline variant management for parallel execution
// of alternative pipeline paths with isolated file systems.
package variants

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrVariantNotFound       = errors.New("variant not found")
	ErrVariantAlreadyExists  = errors.New("variant already exists")
	ErrVariantInvalidState   = errors.New("variant in invalid state for operation")
	ErrVariantClosed         = errors.New("variant system is closed")
	ErrVariantConflict       = errors.New("variant merge conflict detected")
	ErrVariantNoBase         = errors.New("no base pipeline specified")
	ErrVariantResourceLimit  = errors.New("variant resource limit exceeded")
	ErrVariantSuspended      = errors.New("variant is suspended")
	ErrVariantMergeInProcess = errors.New("variant merge already in progress")
	ErrRegistryClosed        = errors.New("registry is closed")
)

// VariantID uniquely identifies a pipeline variant.
type VariantID string

// NewVariantID generates a new unique variant ID.
func NewVariantID() VariantID {
	return VariantID("var_" + uuid.New().String()[:8])
}

// ParseVariantID creates a VariantID from a string.
func ParseVariantID(s string) VariantID {
	return VariantID(s)
}

// String returns the string representation of the variant ID.
func (v VariantID) String() string {
	return string(v)
}

// IsZero returns true if the variant ID is empty.
func (v VariantID) IsZero() bool {
	return v == ""
}

// VariantState represents the current lifecycle state of a variant.
type VariantState int

const (
	// VariantStateCreated indicates the variant has been created but not started.
	VariantStateCreated VariantState = iota
	// VariantStateActive indicates the variant is actively executing.
	VariantStateActive
	// VariantStateSuspended indicates the variant execution is paused.
	VariantStateSuspended
	// VariantStateComplete indicates the variant execution completed successfully.
	VariantStateComplete
	// VariantStateFailed indicates the variant execution failed.
	VariantStateFailed
	// VariantStateMerging indicates the variant is being merged back to main.
	VariantStateMerging
	// VariantStateMerged indicates the variant has been merged to main.
	VariantStateMerged
	// VariantStateCancelled indicates the variant was cancelled before completion.
	VariantStateCancelled
)

var variantStateNames = map[VariantState]string{
	VariantStateCreated:   "created",
	VariantStateActive:    "active",
	VariantStateSuspended: "suspended",
	VariantStateComplete:  "complete",
	VariantStateFailed:    "failed",
	VariantStateMerging:   "merging",
	VariantStateMerged:    "merged",
	VariantStateCancelled: "cancelled",
}

// String returns the string representation of the variant state.
func (s VariantState) String() string {
	if name, ok := variantStateNames[s]; ok {
		return name
	}
	return "unknown"
}

// IsTerminal returns true if the state is a terminal state (no further transitions).
func (s VariantState) IsTerminal() bool {
	return s == VariantStateComplete ||
		s == VariantStateFailed ||
		s == VariantStateMerged ||
		s == VariantStateCancelled
}

// CanTransitionTo checks if a state transition is valid.
func (s VariantState) CanTransitionTo(target VariantState) bool {
	transitions := map[VariantState][]VariantState{
		VariantStateCreated:   {VariantStateActive, VariantStateCancelled},
		VariantStateActive:    {VariantStateSuspended, VariantStateComplete, VariantStateFailed, VariantStateCancelled},
		VariantStateSuspended: {VariantStateActive, VariantStateCancelled},
		VariantStateComplete:  {VariantStateMerging},
		VariantStateMerging:   {VariantStateMerged, VariantStateFailed},
	}

	allowed, ok := transitions[s]
	if !ok {
		return false
	}

	for _, t := range allowed {
		if t == target {
			return true
		}
	}
	return false
}

// VariantPriority defines execution priority for variants.
type VariantPriority int

const (
	VariantPriorityLow VariantPriority = iota
	VariantPriorityNormal
	VariantPriorityHigh
	VariantPriorityCritical
)

// VariantConfig contains configuration for creating a variant.
type VariantConfig struct {
	// ID is the unique identifier for this variant (auto-generated if empty).
	ID VariantID

	// Name is a human-readable name for the variant.
	Name string

	// Description describes the purpose of this variant.
	Description string

	// BasePipelineID is the pipeline this variant branches from.
	BasePipelineID string

	// BaseVersionID is the specific version to branch from (optional).
	BaseVersionID string

	// SessionID is the session this variant belongs to.
	SessionID string

	// AgentID is the agent that created/owns this variant.
	AgentID string

	// Priority determines execution priority.
	Priority VariantPriority

	// ResourceLimits defines resource constraints for this variant.
	ResourceLimits ResourceLimits

	// IsolationLevel determines VFS isolation behavior.
	IsolationLevel IsolationLevel

	// AutoMerge enables automatic merge when variant completes successfully.
	AutoMerge bool

	// Timeout is the maximum duration for variant execution.
	Timeout time.Duration

	// Tags for categorization and filtering.
	Tags []string

	// Metadata for custom variant data.
	Metadata map[string]any
}

// Validate checks if the configuration is valid.
func (c *VariantConfig) Validate() error {
	if c.BasePipelineID == "" {
		return ErrVariantNoBase
	}
	return nil
}

// ResourceLimits defines resource constraints for a variant.
type ResourceLimits struct {
	// MaxMemoryMB is the maximum memory in megabytes.
	MaxMemoryMB int64

	// MaxCPUPercent is the maximum CPU usage percentage.
	MaxCPUPercent float64

	// MaxDiskMB is the maximum disk space in megabytes.
	MaxDiskMB int64

	// MaxFiles is the maximum number of files that can be modified.
	MaxFiles int

	// MaxOperations is the maximum number of operations allowed.
	MaxOperations int64

	// MaxDuration is the maximum execution time.
	MaxDuration time.Duration
}

// DefaultResourceLimits returns sensible default resource limits.
func DefaultResourceLimits() ResourceLimits {
	return ResourceLimits{
		MaxMemoryMB:   512,
		MaxCPUPercent: 50.0,
		MaxDiskMB:     1024,
		MaxFiles:      1000,
		MaxOperations: 10000,
		MaxDuration:   30 * time.Minute,
	}
}

// IsolationLevel determines how isolated the variant's VFS is.
type IsolationLevel int

const (
	// IsolationLevelNone means no isolation (shared with base).
	IsolationLevelNone IsolationLevel = iota
	// IsolationLevelCopyOnWrite creates copies only when files are modified.
	IsolationLevelCopyOnWrite
	// IsolationLevelFull creates a complete isolated copy of the VFS.
	IsolationLevelFull
)

var isolationLevelNames = map[IsolationLevel]string{
	IsolationLevelNone:        "none",
	IsolationLevelCopyOnWrite: "copy_on_write",
	IsolationLevelFull:        "full",
}

// String returns the string representation of the isolation level.
func (l IsolationLevel) String() string {
	if name, ok := isolationLevelNames[l]; ok {
		return name
	}
	return "unknown"
}

// VariantMetrics tracks runtime metrics for a variant.
type VariantMetrics struct {
	// CreatedAt is when the variant was created.
	CreatedAt time.Time

	// StartedAt is when the variant started executing.
	StartedAt time.Time

	// CompletedAt is when the variant finished (success, fail, or cancel).
	CompletedAt time.Time

	// Duration is the total execution time.
	Duration time.Duration

	// OperationCount is the number of operations performed.
	OperationCount int64

	// FileReadCount is the number of file reads.
	FileReadCount int64

	// FileWriteCount is the number of file writes.
	FileWriteCount int64

	// FileDeleteCount is the number of file deletes.
	FileDeleteCount int64

	// MemoryUsedMB is the peak memory usage in MB.
	MemoryUsedMB int64

	// DiskUsedMB is the disk space used in MB.
	DiskUsedMB int64

	// ErrorCount is the number of errors encountered.
	ErrorCount int64

	// LastError is the most recent error message.
	LastError string

	// LastActivity is the timestamp of the last activity.
	LastActivity time.Time

	// MergeAttempts is the number of merge attempts.
	MergeAttempts int

	// ConflictsDetected is the number of conflicts found during merge.
	ConflictsDetected int

	// ConflictsResolved is the number of conflicts resolved.
	ConflictsResolved int
}

// NewVariantMetrics creates a new VariantMetrics with initial values.
func NewVariantMetrics() *VariantMetrics {
	now := time.Now()
	return &VariantMetrics{
		CreatedAt:    now,
		LastActivity: now,
	}
}

// RecordStart marks the variant as started.
func (m *VariantMetrics) RecordStart() {
	m.StartedAt = time.Now()
	m.LastActivity = m.StartedAt
}

// RecordCompletion marks the variant as completed.
func (m *VariantMetrics) RecordCompletion() {
	m.CompletedAt = time.Now()
	m.Duration = m.CompletedAt.Sub(m.StartedAt)
	m.LastActivity = m.CompletedAt
}

// RecordOperation increments the operation counter.
func (m *VariantMetrics) RecordOperation() {
	m.OperationCount++
	m.LastActivity = time.Now()
}

// RecordFileRead increments the file read counter.
func (m *VariantMetrics) RecordFileRead() {
	m.FileReadCount++
	m.LastActivity = time.Now()
}

// RecordFileWrite increments the file write counter.
func (m *VariantMetrics) RecordFileWrite() {
	m.FileWriteCount++
	m.LastActivity = time.Now()
}

// RecordFileDelete increments the file delete counter.
func (m *VariantMetrics) RecordFileDelete() {
	m.FileDeleteCount++
	m.LastActivity = time.Now()
}

// RecordError records an error.
func (m *VariantMetrics) RecordError(err error) {
	m.ErrorCount++
	if err != nil {
		m.LastError = err.Error()
	}
	m.LastActivity = time.Now()
}

// Clone creates a copy of the metrics.
func (m *VariantMetrics) Clone() *VariantMetrics {
	if m == nil {
		return nil
	}
	clone := *m
	return &clone
}

// VariantInfo provides read-only information about a variant.
type VariantInfo struct {
	ID             VariantID
	Name           string
	Description    string
	State          VariantState
	BasePipelineID string
	SessionID      string
	AgentID        string
	Priority       VariantPriority
	IsolationLevel IsolationLevel
	Metrics        *VariantMetrics
	Tags           []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// VariantEvent represents an event in the variant lifecycle.
type VariantEvent struct {
	Type      VariantEventType
	VariantID VariantID
	Timestamp time.Time
	OldState  VariantState
	NewState  VariantState
	Error     error
	Metadata  map[string]any
}

// VariantEventType identifies the type of variant event.
type VariantEventType int

const (
	VariantEventCreated VariantEventType = iota
	VariantEventStarted
	VariantEventSuspended
	VariantEventResumed
	VariantEventCompleted
	VariantEventFailed
	VariantEventMergeStarted
	VariantEventMergeCompleted
	VariantEventMergeFailed
	VariantEventCancelled
	VariantEventResourceLimitReached
)

var variantEventTypeNames = map[VariantEventType]string{
	VariantEventCreated:              "created",
	VariantEventStarted:              "started",
	VariantEventSuspended:            "suspended",
	VariantEventResumed:              "resumed",
	VariantEventCompleted:            "completed",
	VariantEventFailed:               "failed",
	VariantEventMergeStarted:         "merge_started",
	VariantEventMergeCompleted:       "merge_completed",
	VariantEventMergeFailed:          "merge_failed",
	VariantEventCancelled:            "cancelled",
	VariantEventResourceLimitReached: "resource_limit_reached",
}

// String returns the string representation of the event type.
func (t VariantEventType) String() string {
	if name, ok := variantEventTypeNames[t]; ok {
		return name
	}
	return "unknown"
}

// VariantEventCallback is called when variant events occur.
type VariantEventCallback func(event VariantEvent)

// VariantFilter defines criteria for filtering variants.
type VariantFilter struct {
	IDs            []VariantID
	States         []VariantState
	SessionID      string
	AgentID        string
	BasePipelineID string
	Tags           []string
	CreatedAfter   *time.Time
	CreatedBefore  *time.Time
	Priority       *VariantPriority
}

// Matches checks if a variant matches the filter criteria.
func (f *VariantFilter) Matches(info *VariantInfo) bool {
	if info == nil {
		return false
	}

	if len(f.IDs) > 0 && !containsVariantID(f.IDs, info.ID) {
		return false
	}

	if len(f.States) > 0 && !containsState(f.States, info.State) {
		return false
	}

	if f.SessionID != "" && info.SessionID != f.SessionID {
		return false
	}

	if f.AgentID != "" && info.AgentID != f.AgentID {
		return false
	}

	if f.BasePipelineID != "" && info.BasePipelineID != f.BasePipelineID {
		return false
	}

	if len(f.Tags) > 0 && !hasAnyTag(info.Tags, f.Tags) {
		return false
	}

	if f.CreatedAfter != nil && info.CreatedAt.Before(*f.CreatedAfter) {
		return false
	}

	if f.CreatedBefore != nil && info.CreatedAt.After(*f.CreatedBefore) {
		return false
	}

	if f.Priority != nil && info.Priority != *f.Priority {
		return false
	}

	return true
}

// containsVariantID checks if a variant ID is in the slice.
func containsVariantID(ids []VariantID, id VariantID) bool {
	for _, i := range ids {
		if i == id {
			return true
		}
	}
	return false
}

// containsState checks if a state is in the slice.
func containsState(states []VariantState, state VariantState) bool {
	for _, s := range states {
		if s == state {
			return true
		}
	}
	return false
}

// hasAnyTag checks if any tag matches.
func hasAnyTag(variantTags, filterTags []string) bool {
	tagSet := make(map[string]struct{}, len(variantTags))
	for _, t := range variantTags {
		tagSet[t] = struct{}{}
	}
	for _, t := range filterTags {
		if _, ok := tagSet[t]; ok {
			return true
		}
	}
	return false
}

// RegistryStats provides statistics about the variant registry.
type RegistryStats struct {
	TotalVariants     int
	ActiveVariants    int
	SuspendedVariants int
	CompletedVariants int
	FailedVariants    int
	MergedVariants    int
	CancelledVariants int
}

// VariantSummary provides a brief summary of a variant.
type VariantSummary struct {
	ID        VariantID
	Name      string
	State     VariantState
	CreatedAt time.Time
	Duration  time.Duration
}
