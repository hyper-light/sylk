package versioning

import (
	"sync"
	"time"
)

// MergeDescriptor is the per-merge event record produced when a pipeline's
// work is merged into green. Each successful MergePipelineIntoGreen (and,
// for backward compatibility, each CommitPipeline) produces exactly one
// descriptor. Descriptors are retained in the session's merge log until
// their corresponding Copy is terminally released.
//
// Descriptors are the input to per-merge audit replicas (stage 3) and the
// lookup path for remediation dispatch to find the target Copy (stage 2 —
// specifically, the BaseCopyVersion that BeginPipelineConfig takes).
//
// See docs/PARALLEL_GLOBAL_VFS.md for the architectural role.
type MergeDescriptor struct {
	// PipelineID is the source pipeline whose mods were merged.
	PipelineID string

	// BaseVersion is the green version this pipeline was built against.
	// For the default pipeline-dispatch path, this is the green version
	// at dispatch time. Under MergePipe's OT transform, the actual merge
	// may apply on top of a later green version; see MergedVersion for
	// the post-merge green version.
	BaseVersion SemanticVersion

	// MergedVersion is the green version AFTER this merge applied. This
	// is the arrival-ordered monotonic identifier used as the "Copy"
	// reference downstream: "Copy at MergedVersion" = "green's state
	// immediately after this merge."
	MergedVersion SemanticVersion

	// Paths is the set of distinct file paths touched by this merge.
	Paths []string

	// PathCount is the number of distinct paths touched.
	PathCount int

	// MergedAt is the wall-clock timestamp of merge completion.
	MergedAt time.Time

	// PipelineCertificate is the pipeline inspector's declaration of
	// its local review result — what scope the pipeline accepted,
	// which concerns it surfaced, its summary, and the tester
	// verdict it relied on. The global audit replica reads this
	// alongside the diff to decide whether the pipeline's local
	// attestation aligns with the cross-pipeline coherence picture.
	// Empty when the pipeline inspector did not emit a certificate
	// (legacy paths; not expected under the handoff_to_ot flow).
	PipelineCertificate PipelineInspectorCertificate

	// AuditAddendum is true when this descriptor was produced by a
	// ReplicaVFS seal, not by a pipeline inspector handoff. Per
	// docs/PARALLEL_GLOBAL_VFS.md §3.7, audit-addendum merges
	// carry the inspector + tester writes that accompany a merge.
	// The audit coordinator checks this flag and does NOT spawn
	// replicas for addenda (they're already audited — the verdict
	// that produced the addendum IS the audit decision).
	AuditAddendum bool

	// SupersedesVersion is the MergedVersion of a previously-rejected
	// merge whose slot this descriptor claims. Populated when the
	// producing pipeline was dispatched as a remediation: its
	// BeginPipelineConfig.BaseCopyVersion pinned to the rejected
	// merge's Copy and the commit queue still tracks that entry in
	// Rejected state. When this descriptor's audit accepts, the
	// coordinator calls CommitQueue.MarkSuperseded(SupersedesVersion,
	// MergedVersion) to transition the rejected slot and advance the
	// commit resolver. See docs/PARALLEL_GLOBAL_VFS.md §3.8.
	//
	// Zero-value when this descriptor is not a remediation (the
	// normal-dispatch path).
	SupersedesVersion SemanticVersion
}

// PipelineInspectorCertificate is the pipeline inspector's structured
// attestation accompanying handoff_to_ot. Design §6.3.
type PipelineInspectorCertificate struct {
	// DeclaredScope summarizes what the pipeline accepts as "done"
	// (e.g. "implemented fn A in pkg/x", "added tests for case Y").
	DeclaredScope string
	// OpenConcerns are issues the pipeline inspector flagged but did
	// not treat as blocking (e.g. "minor lint warnings in pkg/y").
	OpenConcerns []string
	// Summary is the pipeline inspector's prose review summary.
	Summary string
	// TesterVerdict records the pipeline-level tester's outcome (if
	// one ran). Values: "pass", "skip", "fail" (fail shouldn't reach
	// handoff; included for completeness).
	TesterVerdict string
}

// mergeLog is the in-memory ordered log of MergeDescriptors for a
// session. Append-only for the session's lifetime; cleanup via explicit
// truncation when Copies are released (stage 4).
type mergeLog struct {
	mu          sync.Mutex
	descriptors []MergeDescriptor
}

func newMergeLog() *mergeLog {
	return &mergeLog{}
}

// record appends a descriptor to the log. Safe for concurrent callers.
func (m *mergeLog) record(d MergeDescriptor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.descriptors = append(m.descriptors, d)
}

// snapshot returns a defensive copy of all currently-retained descriptors
// in merge order.
func (m *mergeLog) snapshot() []MergeDescriptor {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.descriptors) == 0 {
		return nil
	}
	out := make([]MergeDescriptor, len(m.descriptors))
	copy(out, m.descriptors)
	return out
}

// latest returns the most recent descriptor. ok=false when the log is
// empty (no merges have happened in this session yet).
func (m *mergeLog) latest() (MergeDescriptor, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.descriptors) == 0 {
		return MergeDescriptor{}, false
	}
	return m.descriptors[len(m.descriptors)-1], true
}

// findByMergedVersion returns the descriptor whose MergedVersion matches
// the given version. ok=false if none is found. Used by remediation
// dispatch to locate the Copy corresponding to a rejected merge.
func (m *mergeLog) findByMergedVersion(ver SemanticVersion) (MergeDescriptor, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.descriptors {
		if d.MergedVersion == ver {
			return d, true
		}
	}
	return MergeDescriptor{}, false
}

// count returns the number of retained descriptors.
func (m *mergeLog) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.descriptors)
}

// since returns descriptors whose MergedVersion is strictly greater
// than the given version. Used by mid-audit replicas (docs/PARALLEL_GLOBAL_VFS.md §3.8)
// to query merges that landed after their context was cut.
func (m *mergeLog) since(ver SemanticVersion) []MergeDescriptor {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.descriptors) == 0 {
		return nil
	}
	var out []MergeDescriptor
	for _, d := range m.descriptors {
		if d.MergedVersion.Compare(ver) > 0 {
			out = append(out, d)
		}
	}
	return out
}

// pathsFromMods extracts the unique path list from a modification slice
// in merge order, preserving first-occurrence order for deterministic
// descriptor snapshots. Uses OriginalPath as the canonical identity
// (StagingPath is an implementation detail of the mutation path).
func pathsFromMods(mods []FileModification) []string {
	if len(mods) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(mods))
	out := make([]string, 0, len(mods))
	for _, mod := range mods {
		path := mod.OriginalPath
		if path == "" {
			path = mod.StagingPath
		}
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
