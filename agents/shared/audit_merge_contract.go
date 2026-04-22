package shared

import (
	"encoding/json"
	"time"

	"github.com/adalundhe/sylk/core/versioning"
)

// jsonEncodeResult is the single encoder used for bus publication
// of results. Kept local so the JSON shape is consistent across the
// package.
func jsonEncodeResult(r *AuditMergeResult) ([]byte, error) {
	return json.Marshal(r)
}

// AuditMergeRequest is the payload orchestrator dispatches to the
// global inspector or global tester when a new merge enters the
// session's commit queue. Each merge gets exactly one inspector
// replica and one tester replica, each scoped to this descriptor.
//
// Per docs/PARALLEL_GLOBAL_VFS.md §3.3, the replica's audit target
// is the changeset (Descriptor.Paths + their diff), and the audit
// context is the Copy immediately preceding this merge
// (Descriptor.BaseVersion → materialized via SessionVFS.CopyAt).
// The replica reads both, plus the PipelineCertificate the pipeline
// inspector attached at handoff_to_green, and emits Accepted or
// Rejected.
type AuditMergeRequest struct {
	// SessionID identifies the session whose commit queue the merge
	// belongs to. Replica spawn / lifecycle / retention all key off
	// this + MergedVersion.
	SessionID string `json:"session_id"`

	// ReplicaID is the deterministic runtime agent ID the orchestrator
	// assigned (MergeReplicaAgentID output). The replica attaches its
	// steering ledger to this ID; resume on session Open re-dispatches
	// with the SAME ID so the ledger rehydrates correctly.
	ReplicaID string `json:"replica_id"`

	// AgentType is "inspector-global" or "tester-global". Replicas
	// use this to validate they were dispatched to the correct role
	// (defense against mis-routing).
	AgentType string `json:"agent_type"`

	// Descriptor is the MergeDescriptor this replica is auditing.
	// Contains BaseVersion (audit context), MergedVersion (target),
	// Paths touched, and the pipeline inspector's certificate.
	Descriptor versioning.MergeDescriptor `json:"descriptor"`

	// DispatchedAt is the wall-clock time the orchestrator issued
	// this audit. Used by SLA timers — audits that exceed the
	// configured SLA (in session-open wall time) trigger escalation.
	DispatchedAt time.Time `json:"dispatched_at"`

	// IsReAudit is true when the replica is being dispatched to
	// re-audit a descriptor whose OT transform changed substantively
	// after a supersession event. The replica inherits the same
	// ReplicaID as the original; its steering ledger carries the
	// prior audit history for context.
	IsReAudit bool `json:"is_re_audit,omitempty"`

	// ReplicaVFS is the per-merge copy-on-write VFS the audit
	// replicas operate on. Inspector and tester share this handle;
	// both read/write through it; it chains to the previous merge's
	// VFS (or disk) for ancestry. Excluded from JSON serialization
	// because the VFS is a live in-process object (not payload
	// data). Direct-protocol dispatch is in-process; bus observers
	// get the descriptor via the AuditMergeResult topic with no VFS
	// handle.
	ReplicaVFS *versioning.ReplicaVFS `json:"-"`

	// SteeringJournalDir, when non-empty, is the session-scoped
	// directory under which the replica should open its steering
	// journal. The replica's SteeringManager uses this path to
	// persist the ledger so that ResumeInFlightReplicas on session
	// restart can rehydrate in-flight audit state. Empty when the
	// session is in strict-RAM mode. Excluded from JSON because it's
	// an in-process detail (the bus doesn't carry in-process paths).
	SteeringJournalDir string `json:"-"`
}

// AuditMergeResult is the terminal verdict a replica emits. Exactly
// one of Accepted or Rejected is set per result. The orchestrator
// translates the result into CommitQueue.MarkAccepted /
// MarkRejected and ReplicaLifecycleLog.RecordDecided.
type AuditMergeResult struct {
	// SessionID echoes the request's session for routing validation.
	SessionID string `json:"session_id"`

	// ReplicaID echoes the request's replica ID for correlation.
	ReplicaID string `json:"replica_id"`

	// MergedVersion echoes the descriptor's merged version —
	// orchestrator uses it to locate the queue entry.
	MergedVersion versioning.SemanticVersion `json:"merged_version"`

	// Decision is Accepted or Rejected.
	Decision versioning.ReplicaDecision `json:"decision"`

	// Summary is the replica's prose summary of its audit — what it
	// checked, what it concluded. Stored on the lifecycle log entry
	// and surfaced in observability.
	Summary string `json:"summary"`

	// Concerns is the structured list of big-picture concerns the
	// replica flagged. Populated on both Accepted (soft warnings)
	// and Rejected (blocking reasons). Architect remediation reads
	// these on rejection to compose the fix DAG.
	Concerns []string `json:"concerns,omitempty"`

	// DecidedAt is the wall-clock time the replica emitted the
	// decision.
	DecidedAt time.Time `json:"decided_at"`
}

// AuditMergeResultTopic is the observability topic the audit
// coordinator publishes finalized results on AFTER the internal
// state machine advances. Subscribers are observers: the architect
// (for rejection → remediation) and the TUI (for state view). The
// coordinator → replica dispatch loop does NOT go through the bus
// — it's direct SessionVFS.RegisterMergeCallback +
// AuditReplicaSpawner.SpawnAuditReplica + AuditDecisionFinalizer
// on ctx.
const AuditMergeResultTopic = "audit.merge.result"

// AuditMergeAddendumTopic is the observability topic the audit
// coordinator publishes on when an audit-addendum merge lands in the
// commit queue. Addenda carry inspector + tester writes (linter /
// formatter output, new tests, harness adjustments) that accompany
// an accepted pipeline merge — they are already audited by virtue
// of how they were produced, so the coordinator skips fresh replica
// dispatch for them. But observers (TUI, architect) need to
// distinguish "the original pipeline merge was accepted" from
// "audit-authored content landed alongside it" — hence a dedicated
// topic rather than overloading AuditMergeResultTopic.
const AuditMergeAddendumTopic = "audit.merge.addendum"

// AuditMergeAddendumNotification is the payload published on
// AuditMergeAddendumTopic when a sealed ReplicaVFS has been
// submitted to the commit queue as an addendum descriptor. Carries
// enough context for observers to correlate the addendum with the
// original merge it decorates and surface its path list.
type AuditMergeAddendumNotification struct {
	// SessionID identifies the session whose commit queue received
	// the addendum.
	SessionID string `json:"session_id"`

	// AddendumVersion is the MergedVersion of the addendum
	// descriptor (i.e. the version the session's WAL assigned when
	// the sealed diff was appended).
	AddendumVersion versioning.SemanticVersion `json:"addendum_version"`

	// BaseVersion is the MergedVersion of the original pipeline
	// merge this addendum decorates. BaseVersion of the addendum
	// descriptor == MergedVersion of the merge whose audit produced
	// it.
	BaseVersion versioning.SemanticVersion `json:"base_version"`

	// PipelineID echoes the synthetic pipeline id the session
	// assigned (prefix "audit-addendum:"). Useful for observer
	// filtering.
	PipelineID string `json:"pipeline_id"`

	// Paths are the distinct file paths the addendum touches.
	Paths []string `json:"paths,omitempty"`

	// PathCount is len(Paths).
	PathCount int `json:"path_count"`

	// State is the commit-queue state observed at notification time.
	// Expected to be CommitStateAccepted (SubmitAuditAddendum flips
	// it atomically). A non-Accepted state indicates an upstream
	// protocol violation and is logged loudly by the coordinator.
	State string `json:"state"`

	// ObservedAt is the wall-clock time the coordinator saw the
	// addendum land.
	ObservedAt time.Time `json:"observed_at"`
}

// marshalAuditMergeResultForBus serializes a result for the
// observability bus topic. Kept in this file so the contract + the
// one-place JSON encoding stay co-located.
func marshalAuditMergeResultForBus(r *AuditMergeResult) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return jsonEncodeResult(r)
}

// marshalAuditMergeAddendumForBus serializes an addendum
// notification for the observability bus topic.
func marshalAuditMergeAddendumForBus(n *AuditMergeAddendumNotification) ([]byte, error) {
	if n == nil {
		return nil, nil
	}
	return json.Marshal(n)
}

// jsonMarshalSLABreach serializes an SLA-breach notification for
// observability publication.
var jsonMarshalSLABreach = func(n *AuditRejectionSLABreachNotification) ([]byte, error) {
	if n == nil {
		return nil, nil
	}
	return json.Marshal(n)
}
