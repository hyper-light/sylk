package activity

import "time"

// Resolution determines an activity's storage tier and lifetime. The
// fabric uses tiered emission to deliver granular insight without
// storage explosion: high-volume infrastructural events live in memory
// with bounded retention; semantic work lives durably in SQLite and
// gets indexed for full-text and semantic search.
//
// Resolution is set by the chokepoint based on ActionKind — never by
// callers. See ResolutionFor(kind) for the canonical mapping.
type Resolution string

const (
	// ResolutionAtomic — extremely high-volume infrastructural events.
	// Examples: LLM chunk received, cache hit, file read, broker
	// stdout byte-batch.
	//
	// Storage: Ristretto-only ring buffer per session.
	// Retention: bounded by the ring buffer's size policy (last N
	// seconds or N MB per session).
	ResolutionAtomic Resolution = "atomic"

	// ResolutionFine — high-volume but individually meaningful events.
	// Examples: tool call complete, file write, command exec complete,
	// guardian decision, bus delivery complete, cache miss with fetch.
	//
	// Storage: Ristretto + ephemeral SQLite (partitioned by hour).
	// Retention: 24 hours, then drop.
	ResolutionFine Resolution = "fine"

	// ResolutionMedium — moderate-volume meaningful work. Examples:
	// skill invocation complete, LLM round-trip complete, bus topic
	// publish, store write transaction, cross-pipeline message.
	//
	// Storage: Ristretto + SQLite agent_activity (durable) + Bleve
	// full-text index.
	// Retention: session lifetime, then candidate for Memory Forest
	// harvest.
	ResolutionMedium Resolution = "medium"

	// ResolutionCoarse — semantic work the system reasons over.
	// Examples: decision declared, challenge issued, plan ratified,
	// validation accepted, charter narrowed, advisory emitted.
	//
	// Storage: Ristretto + SQLite (durable) + Bleve + vectorgraphdb
	// embed + Memory Forest precedent candidate.
	// Retention: permanent (subject to forest gardening).
	ResolutionCoarse Resolution = "coarse"
)

// IsDurable reports whether the resolution requires SQLite storage.
// Atomic activities live in memory only; everything else is durable.
func (r Resolution) IsDurable() bool {
	switch r {
	case ResolutionFine, ResolutionMedium, ResolutionCoarse:
		return true
	}
	return false
}

// ShouldIndexFullText reports whether the resolution should be indexed
// in Bleve for full-text search. Atomic and Fine activities are too
// high-volume; Medium and Coarse warrant the indexing cost.
func (r Resolution) ShouldIndexFullText() bool {
	switch r {
	case ResolutionMedium, ResolutionCoarse:
		return true
	}
	return false
}

// ShouldEmbed reports whether the resolution should be embedded in
// vectorgraphdb for semantic search. Only Coarse activities currently
// warrant the embedding cost.
func (r Resolution) ShouldEmbed() bool {
	return r == ResolutionCoarse
}

// ShouldHarvestForest reports whether the resolution is a candidate
// for Memory Forest cross-session precedent harvest.
func (r Resolution) ShouldHarvestForest() bool {
	return r == ResolutionCoarse
}

// EphemeralRetention returns the retention window for non-durable
// resolutions, or zero if the resolution is durable.
func (r Resolution) EphemeralRetention() time.Duration {
	switch r {
	case ResolutionAtomic:
		// In-memory ring buffer; the actual size policy is enforced by
		// the Ristretto sink, not by an absolute time bound. Returning
		// a reasonable upper-bound hint here keeps the contract
		// readable.
		return 5 * time.Minute
	case ResolutionFine:
		return 24 * time.Hour
	}
	return 0
}
