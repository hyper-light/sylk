// Package fabriclog is the session-global observability log for the
// Activity Fabric.
//
// The fabric is a cross-agent coordination substrate: every agent
// publishes activities to it and reads from it. The fabric's core
// storage (core/activity + core/activity/activitystore) owns the
// source of truth — fabriclog does not replace it. What fabriclog
// captures is the interaction pattern: who publishes, who reads,
// which publications get consumed by which readers, how long publish
// → consume takes, which lifecycle resolutions close out which
// challenges and consults, and where the fabric dead-letters.
//
// The output is a single session-global append-only JSONL stream at
// .sylk/sessions/{sessionID}/fabric/fabric.{YYYY-MM-DD}.{nnn}.jsonl.
// Projections (unread, scope graph, lifecycles) are rebuilt on
// demand by `sylk trace fabric`, not maintained live — the hot path
// stays lean.
//
// Six record kinds model the fabric's interaction surface:
//
//   - fabric_publish — every activity.Append emission
//   - fabric_read    — every activity.Source lens call
//   - fabric_consume — one record per activity ID returned by a read,
//                      joining publisher to consumer
//   - fabric_ambient — every fabric.AppendAmbientContext attachment
//   - fabric_resolve — one record per activity that Resolves another,
//                      closing out a consult/challenge/claim lifecycle
//   - fabric_drop    — bounded-buffer overflow or aged-out activity
//
// Correctness guarantees:
//
//   - Per-session monotonic sequence (Seq) plus wall-clock and
//     monotonic-ns timestamps means cross-agent ordering is exact even
//     when two agents emit in the same nanosecond.
//   - Every record is self-contained (full activity payload on
//     publish; returned activity IDs on read); no external joins
//     required to reconstruct the session narrative.
//   - Redaction applies at the write boundary via the existing
//     agentlog.Redactor chain, so the log can be rendered safely.
//   - Writes are non-blocking (bounded async buffer); overflow
//     is visible via fabric_drop records so dead-letter is
//     observable rather than silent.
//
// Robustness guarantees:
//
//   - The fabric's core activity.Sink / activity.Source stays on the
//     fast path. fabriclog is a decorator — if the logger is nil,
//     behavior is unchanged.
//   - Close drains the buffer and flushes the writer before returning.
//   - Rotation is delegated to agentlog.StreamWriter (daily + 64 MiB).
package fabriclog

import (
	"encoding/json"
	"time"
)

// RecordKind names the six fabric observability record types.
type RecordKind string

const (
	// KindPublish records one activity.Append emission. Carries the
	// full (redacted) AgentActivity payload so the log is self-
	// contained and does not have to join against the per-agent event
	// stream.
	KindPublish RecordKind = "fabric_publish"

	// KindRead records one activity.Source lens call: which lens, what
	// filter, how many activities returned, elapsed time, error if any.
	KindRead RecordKind = "fabric_read"

	// KindConsume records one activity ID that flowed from a publisher
	// to a reader via a KindRead result. One KindConsume per returned
	// activity, keyed back to the KindRead via ReaderRecordSeq. This is
	// the join record: it's how you answer "who actually saw what I
	// published?".
	KindConsume RecordKind = "fabric_consume"

	// KindAmbient records one fabric.AppendAmbientContext attachment
	// on a tool result. Carries the activity IDs that were surfaced
	// to the LLM, distinct from explicit reads.
	KindAmbient RecordKind = "fabric_ambient"

	// KindResolve records one activity whose Resolves pointer closes
	// out a prior in-flight activity. Consult and challenge lifecycles
	// land here. When the resolved activity is known from the recent
	// publish cache, the record carries publish→resolve latency.
	KindResolve RecordKind = "fabric_resolve"

	// KindDrop records a lost record: async buffer overflow, logger
	// closed mid-emit, or aged-out activity never consumed. Drops are
	// counted continuously; a KindDrop record is emitted periodically
	// (not per drop) to avoid amplification loops.
	KindDrop RecordKind = "fabric_drop"
)

// FabricLogRecord is the single envelope shape written to the
// session-global JSONL stream. All records share the envelope fields
// (Kind, SessionID, Seq, Timestamp, MonoNano, Agent); at most one of
// the kind-specific bodies is populated.
type FabricLogRecord struct {
	Kind      RecordKind `json:"kind"`
	SessionID string     `json:"session_id,omitempty"`

	// Seq is the per-session monotonically-increasing sequence number.
	// Every record emitted through the same FabricLogger sees a
	// strictly-increasing Seq, so consumers can reconstruct total
	// ordering without wall-clock trust.
	Seq int64 `json:"seq"`

	// Timestamp is the wall-clock emission time (RFC3339 nanosecond).
	// Use for human-readable display and cross-session analysis; use
	// Seq (or MonoNano) for ordering within this session.
	Timestamp time.Time `json:"t"`

	// MonoNano is the process-monotonic nanosecond count at emission.
	// Immune to wall-clock adjustments. Mostly useful for latency math
	// within a single process.
	MonoNano int64 `json:"mono_ns,omitempty"`

	// Agent is the identity of the agent that produced the record.
	// For Publish: the activity's Actor. For Read/Consume/Ambient: the
	// caller (populated from context when available). For Resolve: the
	// resolver activity's Actor. For Drop: empty.
	Agent AgentRef `json:"agent"`

	Publish *PublishBody `json:"publish,omitempty"`
	Read    *ReadBody    `json:"read,omitempty"`
	Consume *ConsumeBody `json:"consume,omitempty"`
	Ambient *AmbientBody `json:"ambient,omitempty"`
	Resolve *ResolveBody `json:"resolve,omitempty"`
	Drop    *DropBody    `json:"drop,omitempty"`
}

// AgentRef identifies the agent attached to a record. Pipeline is
// optional (global agents leave it empty).
type AgentRef struct {
	AgentID    string `json:"agent_id,omitempty"`
	AgentType  string `json:"agent_type,omitempty"`
	PipelineID string `json:"pipeline_id,omitempty"`
}

// PublishBody carries the full activity payload. The nested Activity
// is the entire AgentActivity marshaled as JSON so the fabric log is
// self-contained — readers do not need to cross-reference the
// per-agent event stream or the SQLite activity store.
type PublishBody struct {
	ActivityID   string          `json:"activity_id"`
	ActionKind   string          `json:"action_kind"`
	State        string          `json:"state,omitempty"`
	Confidence   string          `json:"confidence,omitempty"`
	Scope        string          `json:"scope,omitempty"`
	TargetAgent  string          `json:"target_agent,omitempty"`
	Domain       string          `json:"domain,omitempty"`
	Caused       string          `json:"caused,omitempty"`
	Resolves     string          `json:"resolves,omitempty"`
	PayloadBytes int             `json:"payload_bytes"`
	Activity     json.RawMessage `json:"activity"`
}

// ReadBody records one lens call. ReturnedIDs is the bounded list of
// activity IDs the lens returned (one KindConsume will follow per
// ID). ReturnedCount is the total — the two can differ when the
// returned set is capped for log size.
type ReadBody struct {
	// Lens names the Source method invoked: "FilterActivities",
	// "GetActivity", or "LatestActivityID". When a higher-level lens
	// (peers.WhatAreTheyDoing, etc.) wraps multiple Source calls,
	// each underlying Source call produces its own ReadBody — the
	// lens name is optional context on top of that.
	Lens string `json:"lens"`

	// LensAlias, when set, names the semantic lens function (e.g.
	// "WhatAreTheyDoing", "ConflictsOpen") that called through to the
	// underlying Source method. Enables "how often is each semantic
	// lens actually used?" without reverse-engineering filter shapes.
	LensAlias string `json:"lens_alias,omitempty"`

	Filter        *FilterBody `json:"filter,omitempty"`
	ReturnedIDs   []string    `json:"returned_ids,omitempty"`
	ReturnedCount int         `json:"returned_count"`
	ElapsedMicros int64       `json:"elapsed_us"`
	Err           string      `json:"err,omitempty"`
}

// FilterBody flattens the subset of QueryFilter fields we capture.
// We avoid a direct dependency on the activity package from the
// record struct so tests and trace tooling can deserialize without
// pulling in the full fabric storage stack.
type FilterBody struct {
	ActionKinds        []string  `json:"action_kinds,omitempty"`
	States             []string  `json:"states,omitempty"`
	ActorAgentID       string    `json:"actor_agent_id,omitempty"`
	ActorAgentType     string    `json:"actor_agent_type,omitempty"`
	ActorPipelineID    string    `json:"actor_pipeline_id,omitempty"`
	SubjectPathPrefix  string    `json:"subject_path_prefix,omitempty"`
	SubjectTargetAgent string    `json:"subject_target_agent,omitempty"`
	SubjectDomain      string    `json:"subject_domain,omitempty"`
	Since              time.Time `json:"since,omitempty"`
	Until              time.Time `json:"until,omitempty"`
	Caused             string    `json:"caused,omitempty"`
	Resolves           string    `json:"resolves,omitempty"`
	Limit              int       `json:"limit,omitempty"`
	// FilterHash is a stable 8-hex-digit fingerprint of the filter
	// shape so "same filter, different session" analyses are easy.
	FilterHash string `json:"filter_hash,omitempty"`
}

// ConsumeBody joins a reader to a publisher. ReaderRecordSeq points
// back to the KindRead that produced this consume; PublishSeq points
// to the KindPublish that produced the activity when it is in the
// recent-publish cache. When PublishSeq is populated, LatencyMicros
// is the publish→consume wall-clock latency.
type ConsumeBody struct {
	ReaderRecordSeq int64  `json:"reader_record_seq"`
	ActivityID      string `json:"activity_id"`
	ActionKind      string `json:"action_kind,omitempty"`
	ProducedByAgent string `json:"produced_by_agent,omitempty"`
	ProducedByType  string `json:"produced_by_type,omitempty"`
	PublishSeq      int64  `json:"publish_seq,omitempty"`
	LatencyMicros   int64  `json:"latency_us,omitempty"`
}

// AmbientBody records one ambient-envelope attachment. Each ambient
// call surfaces a bounded set of activities to the LLM via the tool
// result tail; the IDs captured here are the exact set that reached
// the model, distinct from what the agent would have seen via an
// explicit lens read.
type AmbientBody struct {
	ToolName      string   `json:"tool_name,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	ActivityIDs   []string `json:"activity_ids,omitempty"`
	ConflictCount int      `json:"conflict_count"`
	AdvisoryCount int      `json:"advisory_count"`
	Bytes         int      `json:"bytes"`
	ElapsedMicros int64    `json:"elapsed_us"`
	RateLimited   bool     `json:"rate_limited,omitempty"`
}

// ResolveBody records one lifecycle edge: the resolver activity
// closes out the resolved activity. When the resolved activity is
// found in the recent-publish cache, ResolvedKind and LatencyMicros
// are populated.
type ResolveBody struct {
	ResolverActivityID string `json:"resolver_activity_id"`
	ResolverKind       string `json:"resolver_kind,omitempty"`
	ResolvedActivityID string `json:"resolved_activity_id"`
	ResolvedKind       string `json:"resolved_kind,omitempty"`
	LatencyMicros      int64  `json:"latency_us,omitempty"`
}

// DropBody records lost records. Reason is one of "buffer_full",
// "closed", "aged_out_unread". DroppedCount is the cumulative drops
// at emission time; a single DropBody record is emitted periodically
// rather than once per drop so the log doesn't amplify pressure.
type DropBody struct {
	Reason       string `json:"reason"`
	ActivityID   string `json:"activity_id,omitempty"`
	DroppedCount int64  `json:"dropped_count"`
}
