// Package activity is the Agent Activity Fabric — a unified coordination
// and observation substrate for sylk's multi-agent system.
//
// The fabric is the ambient intelligence plane over sylk's existing
// sovereign storage systems: it captures every action at appropriate
// resolution, surfaces peer activity to every agent without ever blocking
// primary work, and lets agents collaborate or challenge across pipelines
// via uniform primitives.
//
// See docs/FABRIC.md for the full design.
//
// Sovereign systems own their own data — Decision Manifest still owns
// decisions; coordination service still owns claims; pipeline-protocol
// still owns the durable event log. The fabric receives projections of
// their state changes via instrumentation chokepoints, never replaces
// their storage. Failure mode of the fabric = lose cross-cutting lenses;
// the sovereign systems keep working unchanged.
package activity

import (
	"encoding/json"
	"time"
)

// ActivityID identifies a single AgentActivity record. Implemented as a
// ULID-like sortable identifier so chronological order is encoded in the
// ID itself; emitters generate them locally without coordination.
type ActivityID string

// SessionID identifies the user-facing session a fabric activity belongs
// to. Mirrors versioning.SessionID so the fabric and the existing
// session-scoping infrastructure use the same key type.
type SessionID string

// AgentActivity is the single typed primitive the fabric records for
// every observable action in sylk. Each chokepoint and each per-system
// amplifier emits exactly one AgentActivity per invocation; lenses
// project filtered, shaped views over the resulting stream.
//
// Append-only by design — state transitions emit new activities (e.g.,
// a decision_promoted activity links to the original decision_declared
// via Resolves). This eliminates the entire class of "did this row get
// mutated correctly" bugs.
type AgentActivity struct {
	// ID is the activity's globally-unique sortable identifier.
	ID ActivityID `json:"id"`

	// SessionID scopes the activity to a single user session.
	SessionID SessionID `json:"session_id"`

	// Timestamp is the wall-clock time at which the activity was emitted.
	Timestamp time.Time `json:"timestamp"`

	// Resolution determines the activity's storage tier and lifetime.
	Resolution Resolution `json:"resolution"`

	// Actor records who emitted the activity.
	Actor Actor `json:"actor"`

	// Action is the typed kind of work this activity represents.
	Action ActionKind `json:"action"`

	// Subject is the typed coordinate bag identifying what the activity
	// is about (path, target agent, target artifact, scope).
	Subject Subject `json:"subject"`

	// Payload carries kind-specific typed data.
	Payload json.RawMessage `json:"payload,omitempty"`

	// Caused points to the activity that immediately caused this one.
	// Nil for root activities (typically user requests entering the system).
	Caused *ActivityID `json:"caused,omitempty"`

	// Resolves points to the activity this one terminates. Nil unless
	// this activity ends a previously in_flight activity (e.g., a
	// challenge_response Resolves a challenge_emitted).
	Resolves *ActivityID `json:"resolves,omitempty"`

	// CausalChain is the denormalized list of ancestor activity IDs from
	// the root to (but excluding) this activity. Enables O(1) DAG walks
	// without recursive joins.
	CausalChain []ActivityID `json:"causal_chain,omitempty"`

	// State is the current lifecycle state.
	State ActivityState `json:"state"`

	// Confidence applies to decision-shaped activities (Hint, Tentative,
	// Committed, Consensus). Empty for activities without confidence
	// semantics.
	Confidence Confidence `json:"confidence,omitempty"`

	// Evidence carries pointers to artifacts, files, decisions, or other
	// activities that substantiate this activity.
	Evidence []EvidenceRef `json:"evidence,omitempty"`

	// SourceTable, when set, names the sovereign store that authored
	// this activity (e.g., "decision_manifest", "coordination_claims").
	// SourceID is the row's primary key in that table. The pair forms
	// a back-reference from the fabric projection to its canonical row.
	SourceTable string `json:"source_table,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
}

// Actor identifies the agent that emitted an activity.
type Actor struct {
	AgentID    string `json:"agent_id"`
	AgentType  string `json:"agent_type"`
	PipelineID string `json:"pipeline_id,omitempty"`
}

// Subject is the typed coordinate bag identifying what an activity is
// about. The fabric promotes specific keys into denormalized indexed
// columns (PathPrefix, TargetAgent, TargetArtifact) for hot lookups.
type Subject struct {
	// PathPrefix is the workspace-relative directory or file path the
	// activity touches. Used for scope-prefix matching across activities
	// (e.g., "what's happened in services/billing/?").
	PathPrefix string `json:"path_prefix,omitempty"`

	// TargetAgent identifies a specific peer agent that is the subject
	// of this activity (e.g., the addressee of a cross-pipeline consult
	// or challenge).
	TargetAgent string `json:"target_agent,omitempty"`

	// TargetArtifact identifies a specific artifact (file, decision,
	// claim, etc.) the activity is about.
	TargetArtifact string `json:"target_artifact,omitempty"`

	// Domain is an optional logical domain (e.g., "test_framework",
	// "build_backend", "ui_framework") for decision-shaped activities.
	Domain string `json:"domain,omitempty"`

	// Coordinates carries any additional typed scope keys (language,
	// surface, etc.) without forcing them into the indexed columns.
	Coordinates map[string]string `json:"coordinates,omitempty"`
}

// EvidenceRef points to an artifact, file, decision, or peer activity
// that substantiates an activity. Distinguished by Kind so consumers can
// route the reference to the appropriate store.
type EvidenceRef struct {
	Kind EvidenceKind `json:"kind"`
	Ref  string       `json:"ref"`
	Note string       `json:"note,omitempty"`
}

// EvidenceKind classifies an EvidenceRef so consumers know how to
// interpret Ref.
type EvidenceKind string

const (
	EvidenceFile     EvidenceKind = "file"
	EvidenceArtifact EvidenceKind = "artifact"
	EvidenceDecision EvidenceKind = "decision"
	EvidenceActivity EvidenceKind = "activity"
	EvidenceURL      EvidenceKind = "url"
	EvidenceMessage  EvidenceKind = "message"
)

// ActivityState is the lifecycle state of an activity.
type ActivityState string

const (
	// StatePoint marks an activity that completes the moment it's
	// emitted (no in-flight phase).
	StatePoint ActivityState = "point"

	// StateInFlight marks an activity awaiting a resolution (e.g., an
	// emitted challenge waiting for response, an in-flight claim).
	StateInFlight ActivityState = "in_flight"

	// StateResolved marks an in_flight activity that has been resolved
	// by a downstream activity (linked via Resolves).
	StateResolved ActivityState = "resolved"

	// StateCancelled marks an activity that was withdrawn before
	// completion.
	StateCancelled ActivityState = "cancelled"

	// StateSuperseded marks an activity replaced by a newer activity
	// (e.g., a decision_declared superseded by a narrower scope split).
	StateSuperseded ActivityState = "superseded"
)

// Confidence is the four-level confidence model for decision-shaped
// activities. Carried over from the existing Decision Manifest model so
// the fabric and the manifest interpret confidence identically.
type Confidence string

const (
	// ConfidenceHint represents an observation: the agent observed a
	// pre-existing fact in the codebase, not a commitment.
	ConfidenceHint Confidence = "hint"

	// ConfidenceTentative represents an intent: the agent has chosen
	// a direction but not yet acted on it.
	ConfidenceTentative Confidence = "tentative"

	// ConfidenceCommitted represents a commitment: the agent has acted
	// on the choice (mutation has occurred, work has happened).
	ConfidenceCommitted Confidence = "committed"

	// ConfidenceConsensus represents ratification: the choice has been
	// validated by an authority (inspector accepted artifact, plan
	// completed, charter ratified) or corroborated by peer pipelines.
	ConfidenceConsensus Confidence = "consensus"
)
