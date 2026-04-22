package versioning

import (
	"sync"
	"time"
)

// CopyRetention tracks active references on MergedVersions (Copies) so
// the cleanup pass knows which Copies are safe to release.
// See docs/PARALLEL_GLOBAL_VFS.md §5 for the lifetime rules.
//
// Reference holders (stage 4 scope):
//   - In-flight audit replica auditing a Copy
//   - Pending remediation dispatch targeting a Copy
//   - Downstream descriptor in lazy-materialization mode (deferred)
//
// Water line: the lowest MergedVersion still held. Advances as
// disk-commits resolve entries. Copies below the water line with
// zero refs are released.
type CopyRetention struct {
	mu         sync.Mutex
	refs       map[SemanticVersion]int
	holders    map[string]SemanticVersion // hold-id → version for debuggability
	waterLine  SemanticVersion
	waterAt    time.Time
	gcCallback func(released []SemanticVersion)
	wal        *ControlWAL
}

// CopyRetentionConfig configures a CopyRetention.
type CopyRetentionConfig struct {
	// OnRelease is called (if non-nil) with the list of MergedVersions
	// released by a GC pass. Used to flush observability events and
	// (in future stages) compact WAL segments.
	OnRelease func(released []SemanticVersion)
	// WAL, if set, persists every Retain / Release /
	// AdvanceWaterLine event durably before the in-memory state is
	// mutated. Replay on Open reconstructs the full retention
	// topology from the log, so a crash mid-transition cannot lose a
	// holder or skip a water-line advance.
	WAL *ControlWAL
}

// NewCopyRetention returns a fresh retention tracker.
func NewCopyRetention(cfg CopyRetentionConfig) *CopyRetention {
	return &CopyRetention{
		refs:       make(map[SemanticVersion]int),
		holders:    make(map[string]SemanticVersion),
		gcCallback: cfg.OnRelease,
		wal:        cfg.WAL,
	}
}

// Retain registers a reference on the given Copy version. holderID is a
// free-form identifier for debuggability (e.g., "replica-abc123",
// "remediation-dispatch-task_a_fix"). Duplicate retains with the same
// holderID and version are a no-op; duplicates with different versions
// release the prior hold implicitly before acquiring the new one.
// Write-ahead to ControlWAL before mutating in-memory state.
func (r *CopyRetention) Retain(ver SemanticVersion, holderID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.holders[holderID]; ok && existing == ver {
		return nil
	}
	if err := r.walAppend(ControlKindRetentionRetain, ControlEntryPayload{
		RetentionVersion:  ver,
		RetentionHolderID: holderID,
	}); err != nil {
		return err
	}
	if existing, ok := r.holders[holderID]; ok {
		if r.refs[existing] > 0 {
			r.refs[existing]--
			if r.refs[existing] == 0 {
				delete(r.refs, existing)
			}
		}
	}
	r.refs[ver]++
	r.holders[holderID] = ver
	return nil
}

// Release releases a reference by holderID. No-op if the holder isn't
// tracked. Write-ahead to ControlWAL before mutating in-memory state.
func (r *CopyRetention) Release(holderID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ver, ok := r.holders[holderID]
	if !ok {
		return nil
	}
	if err := r.walAppend(ControlKindRetentionRelease, ControlEntryPayload{
		RetentionVersion:  ver,
		RetentionHolderID: holderID,
	}); err != nil {
		return err
	}
	if n := r.refs[ver]; n > 0 {
		r.refs[ver] = n - 1
		if r.refs[ver] == 0 {
			delete(r.refs, ver)
		}
	}
	delete(r.holders, holderID)
	return nil
}

// RefCount returns the current number of references on a Copy version.
func (r *CopyRetention) RefCount(ver SemanticVersion) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refs[ver]
}

// WaterLine returns the current water line. MergedVersions strictly
// less than the water line are releasable (subject to refcount).
func (r *CopyRetention) WaterLine() SemanticVersion {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.waterLine
}

// AdvanceWaterLine updates the water line to a newer MergedVersion.
// Only advances if the new version is strictly greater; no-op
// otherwise. Returns the list of versions released as a side effect
// (if any). Write-ahead to ControlWAL before mutating in-memory state.
func (r *CopyRetention) AdvanceWaterLine(newLine SemanticVersion, knownVersions []SemanticVersion) ([]SemanticVersion, error) {
	r.mu.Lock()
	if newLine.Compare(r.waterLine) <= 0 {
		r.mu.Unlock()
		return nil, nil
	}
	if err := r.walAppend(ControlKindRetentionAdvanceWaterLine, ControlEntryPayload{
		WaterLine: newLine,
	}); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.waterLine = newLine
	r.waterAt = time.Now().UTC()
	released := r.collectReleasableLocked(knownVersions)
	callback := r.gcCallback
	r.mu.Unlock()

	if callback != nil && len(released) > 0 {
		callback(released)
	}
	return released, nil
}

// collectReleasableLocked returns versions that are (a) strictly below
// the water line and (b) have zero refcount. Caller must hold r.mu.
func (r *CopyRetention) collectReleasableLocked(knownVersions []SemanticVersion) []SemanticVersion {
	var released []SemanticVersion
	for _, ver := range knownVersions {
		if ver.Compare(r.waterLine) >= 0 {
			continue
		}
		if r.refs[ver] > 0 {
			continue
		}
		released = append(released, ver)
	}
	return released
}

// Snapshot returns a defensive copy of the current retention state for
// observability.
type CopyRetentionSnapshot struct {
	WaterLine     SemanticVersion
	WaterLineAt   time.Time
	TrackedCopies int
	Holders       int
	RefsByVersion map[SemanticVersion]int
}

func (r *CopyRetention) Snapshot() CopyRetentionSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	refs := make(map[SemanticVersion]int, len(r.refs))
	for k, v := range r.refs {
		refs[k] = v
	}
	return CopyRetentionSnapshot{
		WaterLine:     r.waterLine,
		WaterLineAt:   r.waterAt,
		TrackedCopies: len(r.refs),
		Holders:       len(r.holders),
		RefsByVersion: refs,
	}
}

// walAppend persists a retention transition to the ControlWAL. Caller
// holds r.mu when invoking. Returns the WAL failure (if any) so the
// transition aborts before mutating in-memory state.
func (r *CopyRetention) walAppend(kind ControlEntryKind, payload ControlEntryPayload) error {
	if r.wal == nil {
		return nil
	}
	_, err := r.wal.Append(kind, payload)
	return err
}

// ApplyControlEntry rehydrates a single durable retention event into
// in-memory state without re-writing it to the WAL. Used by
// SessionVFS.Open replay. Unknown kinds are ignored (the dispatcher
// routes them to the correct subsystem).
func (r *CopyRetention) ApplyControlEntry(entry *ControlEntry) {
	if entry == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch entry.Kind {
	case ControlKindRetentionRetain:
		holderID := entry.Payload.RetentionHolderID
		ver := entry.Payload.RetentionVersion
		if existing, ok := r.holders[holderID]; ok && existing != ver {
			if r.refs[existing] > 0 {
				r.refs[existing]--
				if r.refs[existing] == 0 {
					delete(r.refs, existing)
				}
			}
		}
		if existing, ok := r.holders[holderID]; !ok || existing != ver {
			r.refs[ver]++
			r.holders[holderID] = ver
		}
	case ControlKindRetentionRelease:
		holderID := entry.Payload.RetentionHolderID
		if ver, ok := r.holders[holderID]; ok {
			if n := r.refs[ver]; n > 0 {
				r.refs[ver] = n - 1
				if r.refs[ver] == 0 {
					delete(r.refs, ver)
				}
			}
			delete(r.holders, holderID)
		}
	case ControlKindRetentionAdvanceWaterLine:
		if entry.Payload.WaterLine.Compare(r.waterLine) > 0 {
			r.waterLine = entry.Payload.WaterLine
			r.waterAt = entry.Timestamp
		}
	default:
		// Not a retention kind — caller dispatches elsewhere.
	}
}
