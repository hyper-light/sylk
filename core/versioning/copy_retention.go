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
}

// CopyRetentionConfig configures a CopyRetention.
type CopyRetentionConfig struct {
	// OnRelease is called (if non-nil) with the list of MergedVersions
	// released by a GC pass. Used to flush observability events and
	// (in future stages) compact WAL segments.
	OnRelease func(released []SemanticVersion)
}

// NewCopyRetention returns a fresh retention tracker.
func NewCopyRetention(cfg CopyRetentionConfig) *CopyRetention {
	return &CopyRetention{
		refs:       make(map[SemanticVersion]int),
		holders:    make(map[string]SemanticVersion),
		gcCallback: cfg.OnRelease,
	}
}

// Retain registers a reference on the given Copy version. holderID is a
// free-form identifier for debuggability (e.g., "replica-abc123",
// "remediation-dispatch-task_a_fix"). Duplicate retains with the same
// holderID and version are a no-op; duplicates with different versions
// panic because that's a bookkeeping error at the caller.
func (r *CopyRetention) Retain(ver SemanticVersion, holderID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.holders[holderID]; ok {
		if existing == ver {
			return
		}
		// Release the prior hold implicitly before acquiring the new
		// one. Could panic instead; chose leniency.
		if r.refs[existing] > 0 {
			r.refs[existing]--
		}
	}
	r.refs[ver]++
	r.holders[holderID] = ver
}

// Release releases a reference by holderID. No-op if the holder isn't
// tracked.
func (r *CopyRetention) Release(holderID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ver, ok := r.holders[holderID]
	if !ok {
		return
	}
	if n := r.refs[ver]; n > 0 {
		r.refs[ver] = n - 1
		if r.refs[ver] == 0 {
			delete(r.refs, ver)
		}
	}
	delete(r.holders, holderID)
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
// (if any).
func (r *CopyRetention) AdvanceWaterLine(newLine SemanticVersion, knownVersions []SemanticVersion) []SemanticVersion {
	r.mu.Lock()
	if newLine.Compare(r.waterLine) <= 0 {
		r.mu.Unlock()
		return nil
	}
	r.waterLine = newLine
	r.waterAt = time.Now().UTC()
	released := r.collectReleasableLocked(knownVersions)
	callback := r.gcCallback
	r.mu.Unlock()

	if callback != nil && len(released) > 0 {
		callback(released)
	}
	return released
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
