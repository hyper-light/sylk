package activity

import (
	"context"
	"time"
)

// Lens is a typed projection over the activity stream. Lenses filter,
// shape, and aggregate the underlying records into the answer an agent
// (or audit tool) actually wants. New lenses can be added without
// touching the storage; deprecated lenses can be removed without
// affecting the source of truth.
//
// Lenses are stateless functions over the fabric. Implementations must
// be safe for concurrent callers and should memoize results in
// Ristretto keyed by (lens_name, query_hash, last_known_activity_id)
// where appropriate.
//
// The lens interface is intentionally small — Query takes a typed
// query value, returns a typed result value, and is execution-shape
// agnostic. Concrete lens packages (manifest, coord, peers, session)
// expose their typed Query/Result types directly; this generic Lens
// interface exists for cross-cutting tooling (metrics, lens registry,
// debug introspection).
type Lens interface {
	// Name returns the lens's stable identifier (e.g.,
	// "manifest.Query", "peers.WhatAreTheyDoing").
	Name() string

	// Query executes the lens against the given query value and
	// returns a typed result. Implementations panic-recover and
	// return an error when the query cannot be served.
	Query(ctx context.Context, query any) (any, error)
}

// Source is the read interface lenses use to pull activities from
// storage. Implementations: SQLite-backed (the production source);
// in-memory (test source); composite (cache-then-disk).
//
// Source is intentionally narrow. Lenses encode their predicates as
// QueryFilter values; the source applies them at the storage layer
// (using the indexed columns) so the lens code stays simple.
type Source interface {
	// FilterActivities returns activities matching the filter, ordered
	// by Timestamp ascending. The result set is bounded by
	// filter.Limit; callers that need everything must paginate.
	FilterActivities(ctx context.Context, filter QueryFilter) ([]AgentActivity, error)

	// GetActivity returns the activity with the given ID, or nil if
	// not present. Used for causal-trace walks.
	GetActivity(ctx context.Context, id ActivityID) (*AgentActivity, error)

	// LatestActivityID returns the ID of the most recently appended
	// activity matching the filter. Used by lens-result memoization
	// to invalidate cached results when new relevant activities
	// arrive.
	LatestActivityID(ctx context.Context, filter QueryFilter) (ActivityID, error)
}

// QueryFilter is the typed predicate set lenses pass to Source. The
// fabric's storage layer maps each field onto an indexed column where
// possible.
type QueryFilter struct {
	// SessionID restricts the result set to one session. Required for
	// production queries; tests sometimes leave it empty.
	SessionID SessionID

	// ActionKinds restricts to one or more typed kinds. Empty matches
	// any kind.
	ActionKinds []ActionKind

	// States restricts to specific lifecycle states. Empty matches any.
	States []ActivityState

	// ActorAgentID restricts to activities emitted by a specific
	// agent.
	ActorAgentID string

	// ActorAgentType restricts to activities emitted by agents of a
	// specific type.
	ActorAgentType string

	// ActorPipelineID restricts to activities emitted by a specific
	// pipeline.
	ActorPipelineID string

	// SubjectPathPrefix restricts to activities whose subject's
	// PathPrefix is a prefix of (or equal to) the given path. Enables
	// scope-prefix queries like "what's happened in services/billing/?".
	SubjectPathPrefix string

	// SubjectTargetAgent restricts to activities targeting a specific
	// agent (e.g., inbound cross-pipeline disputes / consults).
	SubjectTargetAgent string

	// SubjectTargetArtifact restricts to activities targeting a
	// specific artifact.
	SubjectTargetArtifact string

	// SubjectDomain restricts to activities in a specific decision
	// domain (e.g., "test_framework", "build_backend").
	SubjectDomain string

	// Since restricts to activities with Timestamp >= Since. Zero
	// value matches any time.
	Since time.Time

	// Until restricts to activities with Timestamp <= Until. Zero
	// value matches any time.
	Until time.Time

	// Confidences restricts to specific confidence levels. Empty
	// matches any.
	Confidences []Confidence

	// Caused restricts to activities whose Caused matches the given
	// activity ID. Used for causal-tree walks.
	Caused *ActivityID

	// Resolves restricts to activities resolving the given activity
	// ID. Used to find terminal activities for in-flight items.
	Resolves *ActivityID

	// Limit caps the result set size. Zero means "no limit"; callers
	// that pass zero and want bounded results must enforce their own
	// cap.
	Limit int
}
