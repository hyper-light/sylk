package versioning

import (
	"fmt"
	"sync"
	"time"
)

// ReplicaLifecycleState is the in-memory projection of an audit
// replica's lifecycle as recorded on the control WAL. Rebuilt on
// Open by replaying ControlKindReplicaSpawned/Decided/Crashed entries.
// The orchestrator consults this to decide which replicas need re-
// dispatch after session resume: any entry in InFlight state whose
// commit-queue entry is still Auditing is re-launched with the same
// deterministic ReplicaID.
type ReplicaLifecycleState string

const (
	// ReplicaLifecycleInFlight means Spawned was recorded but neither
	// Decided nor Crashed has landed. The replica was (or is) running.
	ReplicaLifecycleInFlight ReplicaLifecycleState = "in_flight"
	// ReplicaLifecycleDecided means the replica emitted a terminal
	// accept/reject verdict. No re-dispatch required.
	ReplicaLifecycleDecided ReplicaLifecycleState = "decided"
	// ReplicaLifecycleCrashed means the replica died before emitting
	// a decision. Re-dispatch required on resume.
	ReplicaLifecycleCrashed ReplicaLifecycleState = "crashed"
)

// ReplicaLifecycleEntry is the current known state for one audit
// replica keyed by its deterministic ReplicaID.
type ReplicaLifecycleEntry struct {
	ReplicaID     string
	AgentType     string
	MergedVersion SemanticVersion
	State         ReplicaLifecycleState
	// Decision is populated when State == Decided.
	Decision         ReplicaDecision
	DecisionText     string
	Concerns         []string
	// CrashReason is populated when State == Crashed.
	CrashReason string
	// SpawnedAt / ResolvedAt timestamps taken from WAL entry
	// timestamps.
	SpawnedAt  time.Time
	ResolvedAt time.Time
}

// ReplicaLifecycleLog tracks the current lifecycle state of audit
// replicas for a session. Every transition is write-ahead to the
// session's ControlWAL; the in-memory state is a projection
// rebuildable via ApplyControlEntry on Open replay.
//
// Concurrency: all public methods take the mutex. The log is safe
// for use from multiple goroutines (orchestrator dispatch, replica
// decision callbacks, observability readers) concurrently.
type ReplicaLifecycleLog struct {
	mu      sync.Mutex
	entries map[string]*ReplicaLifecycleEntry
	wal     *ControlWAL
}

// NewReplicaLifecycleLog returns a fresh log. The optional wal is
// used to persist every transition before applying it in memory.
// Tests may pass nil to run fully in-memory.
func NewReplicaLifecycleLog(wal *ControlWAL) *ReplicaLifecycleLog {
	return &ReplicaLifecycleLog{
		entries: make(map[string]*ReplicaLifecycleEntry),
		wal:     wal,
	}
}

// RecordSpawned writes a ControlKindReplicaSpawned entry and marks
// the replica as in-flight. Idempotent: spawning the same ReplicaID
// twice is a no-op on the in-memory state (the WAL still records
// both events, which is fine — replay will produce the same
// projection).
func (l *ReplicaLifecycleLog) RecordSpawned(replicaID, agentType string, mergedVersion SemanticVersion) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.walAppend(ControlKindReplicaSpawned, ControlEntryPayload{
		ReplicaMergeVersion: mergedVersion,
		ReplicaAgentType:    agentType,
		ReplicaID:           replicaID,
	}); err != nil {
		return err
	}
	l.entries[replicaID] = &ReplicaLifecycleEntry{
		ReplicaID:     replicaID,
		AgentType:     agentType,
		MergedVersion: mergedVersion,
		State:         ReplicaLifecycleInFlight,
		SpawnedAt:     time.Now().UTC(),
	}
	return nil
}

// RecordDecided writes a ControlKindReplicaDecided entry. Callable
// only on an in-flight replica; calling on decided/crashed returns
// an error.
func (l *ReplicaLifecycleLog) RecordDecided(replicaID string, decision ReplicaDecision, text string, concerns []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[replicaID]
	if entry == nil {
		return fmt.Errorf("replica lifecycle: RecordDecided: unknown replica %s", replicaID)
	}
	if entry.State != ReplicaLifecycleInFlight {
		return fmt.Errorf("replica lifecycle: RecordDecided: replica %s in state %s (expected in_flight)", replicaID, entry.State)
	}
	if err := l.walAppend(ControlKindReplicaDecided, ControlEntryPayload{
		ReplicaMergeVersion: entry.MergedVersion,
		ReplicaID:           replicaID,
		ReplicaDecision:     decision,
		ReplicaDecisionText: text,
		Concerns:            append([]string(nil), concerns...),
	}); err != nil {
		return err
	}
	entry.State = ReplicaLifecycleDecided
	entry.Decision = decision
	entry.DecisionText = text
	entry.Concerns = append([]string(nil), concerns...)
	entry.ResolvedAt = time.Now().UTC()
	return nil
}

// RecordCrashed writes a ControlKindReplicaCrashed entry. Used when
// a replica terminates without emitting a decision — the orchestrator
// re-dispatches a fresh replica with the same ID on next resume
// opportunity.
func (l *ReplicaLifecycleLog) RecordCrashed(replicaID, reason string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[replicaID]
	if entry == nil {
		return fmt.Errorf("replica lifecycle: RecordCrashed: unknown replica %s", replicaID)
	}
	if entry.State != ReplicaLifecycleInFlight {
		return fmt.Errorf("replica lifecycle: RecordCrashed: replica %s in state %s (expected in_flight)", replicaID, entry.State)
	}
	if err := l.walAppend(ControlKindReplicaCrashed, ControlEntryPayload{
		ReplicaMergeVersion: entry.MergedVersion,
		ReplicaID:           replicaID,
		RejectionReason:     reason,
	}); err != nil {
		return err
	}
	entry.State = ReplicaLifecycleCrashed
	entry.CrashReason = reason
	entry.ResolvedAt = time.Now().UTC()
	return nil
}

// Lookup returns the lifecycle entry for a ReplicaID or nil if
// unknown.
func (l *ReplicaLifecycleLog) Lookup(replicaID string) *ReplicaLifecycleEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[replicaID]
	if !ok {
		return nil
	}
	clone := *e
	clone.Concerns = append([]string(nil), e.Concerns...)
	return &clone
}

// InFlight returns the set of replicas currently in the InFlight
// state. Ordering is not guaranteed. On session Open the orchestrator
// consults this list to re-dispatch replicas whose commit-queue
// entry is still in Auditing.
func (l *ReplicaLifecycleLog) InFlight() []ReplicaLifecycleEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ReplicaLifecycleEntry, 0, len(l.entries))
	for _, e := range l.entries {
		if e.State == ReplicaLifecycleInFlight {
			clone := *e
			clone.Concerns = append([]string(nil), e.Concerns...)
			out = append(out, clone)
		}
	}
	return out
}

// ApplyControlEntry rehydrates a single durable transition on replay.
// Unknown kinds are ignored (caller routes them to their own
// subsystem). Called by SessionVFS.replayControlWAL.
func (l *ReplicaLifecycleLog) ApplyControlEntry(entry *ControlEntry) {
	if entry == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch entry.Kind {
	case ControlKindReplicaSpawned:
		l.entries[entry.Payload.ReplicaID] = &ReplicaLifecycleEntry{
			ReplicaID:     entry.Payload.ReplicaID,
			AgentType:     entry.Payload.ReplicaAgentType,
			MergedVersion: entry.Payload.ReplicaMergeVersion,
			State:         ReplicaLifecycleInFlight,
			SpawnedAt:     entry.Timestamp,
		}
	case ControlKindReplicaDecided:
		e := l.entries[entry.Payload.ReplicaID]
		if e == nil {
			e = &ReplicaLifecycleEntry{
				ReplicaID:     entry.Payload.ReplicaID,
				MergedVersion: entry.Payload.ReplicaMergeVersion,
				SpawnedAt:     entry.Timestamp,
			}
			l.entries[entry.Payload.ReplicaID] = e
		}
		e.State = ReplicaLifecycleDecided
		e.Decision = entry.Payload.ReplicaDecision
		e.DecisionText = entry.Payload.ReplicaDecisionText
		e.Concerns = append([]string(nil), entry.Payload.Concerns...)
		e.ResolvedAt = entry.Timestamp
	case ControlKindReplicaCrashed:
		e := l.entries[entry.Payload.ReplicaID]
		if e == nil {
			e = &ReplicaLifecycleEntry{
				ReplicaID:     entry.Payload.ReplicaID,
				MergedVersion: entry.Payload.ReplicaMergeVersion,
				SpawnedAt:     entry.Timestamp,
			}
			l.entries[entry.Payload.ReplicaID] = e
		}
		e.State = ReplicaLifecycleCrashed
		e.CrashReason = entry.Payload.RejectionReason
		e.ResolvedAt = entry.Timestamp
	default:
		// Not a replica-lifecycle kind.
	}
}

// walAppend persists a lifecycle transition to the ControlWAL.
// Caller holds l.mu. Returns the WAL failure (if any) so the
// transition aborts before mutating in-memory state.
func (l *ReplicaLifecycleLog) walAppend(kind ControlEntryKind, payload ControlEntryPayload) error {
	if l.wal == nil {
		return nil
	}
	_, err := l.wal.Append(kind, payload)
	return err
}
