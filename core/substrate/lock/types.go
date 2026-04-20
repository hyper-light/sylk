// Package lock implements the substrate's per-session lockfile.
//
// A Lockfile records, for one Sylk session, which substrate manifests
// have been pinned by which pipelines under which version constraints.
// It is the trust anchor for reproducibility within a pipeline (a
// pipeline's pins never silently move) and the coordination point for
// liveness across pipelines (new pipelines may pin newer versions
// without disturbing older ones).
//
// CRDT semantics: the lockfile is monotonic and append-only with
// respect to witnesses. Multiple pipelines may run constraint solvers
// concurrently and pin coordinates without coordination — each appends
// its witness; never deletes others'. The solver's output is
// deterministic given the witness set, so concurrent solves converge.
//
// Existence-by-witness: a pin exists while at least one witness
// references it. When the last witness for a pin expires (its pipeline
// terminates), the pin is removed and the substrate's manifest store is
// notified to detach the manifest, which decrements blob refcounts and
// makes refcount-zero blobs eligible for eviction.
//
// Side-by-side versions: distinct pipelines may pin different versions
// of the same coordinate. The lockfile holds multiple Pins per
// Coordinate; content-addressing and substrate dedup mean side-by-side
// versions are nearly free at the byte level (e.g. pytest 8.3.1 and
// 8.3.2 share ~95% of their blobs).
package lock

import (
	"time"

	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Coordinate is the (ecosystem, name) handle a Pin resolves under.
//
// (Version is intentionally not part of Coordinate — distinct versions
// of the same coordinate coexist in the lockfile as separate Pins.
// Resolving a constraint picks the best Pin for that constraint among
// the coordinate's pinned versions.)
type Coordinate struct {
	Ecosystem string
	Name      string
}

// String returns "ecosystem/name".
func (c Coordinate) String() string {
	return c.Ecosystem + "/" + c.Name
}

// IsZero reports whether c is the zero Coordinate.
func (c Coordinate) IsZero() bool {
	return c.Ecosystem == "" && c.Name == ""
}

// Pin is one substrate manifest pinned in the lockfile, with its
// witness set and transitive closure.
//
// A Pin is created when the first witness arrives for (Coordinate,
// Version) and is removed when the last witness expires. Between, the
// Pin is mutable only with respect to its Witnesses slice (append-only
// adds, single-witness removes); the manifest reference is fixed at
// creation time.
type Pin struct {
	Coordinate Coordinate
	Version    string
	ManifestID manifest.ID
	RecipeID   recipe.ID
	Source     manifest.SourceMetadata
	PinnedAt   time.Time
	Witnesses  []Witness
	Closure    []recipe.ID // transitive recipe closure that satisfies this Pin
}

// IsZero reports whether p is the zero Pin.
func (p *Pin) IsZero() bool {
	return p == nil || (p.Coordinate.IsZero() && p.Version == "")
}

// HasWitness reports whether the pin has any witness from pipelineID.
func (p *Pin) HasWitness(pipelineID string) bool {
	for i := range p.Witnesses {
		if p.Witnesses[i].PipelineID == pipelineID {
			return true
		}
	}
	return false
}

// WitnessCount returns the number of witnesses currently referencing
// this pin. A pin with WitnessCount == 0 is a logic error (it should
// have been removed); the lockfile enforces the invariant.
func (p *Pin) WitnessCount() int {
	return len(p.Witnesses)
}

// Witness records that one pipeline contributed one constraint to this
// pin. Witnesses are the lockfile's CRDT join element: appending a new
// witness is an idempotent operation on (PipelineID, Constraint), and
// the witness set converges across concurrent appenders.
type Witness struct {
	PipelineID string
	Constraint string // ecosystem-native version constraint string (opaque to the lockfile)
	AddedAt    time.Time
}

// PinRequest is the input to Lockfile.Pin.
type PinRequest struct {
	Coordinate Coordinate
	Version    string
	ManifestID manifest.ID
	RecipeID   recipe.ID
	Source     manifest.SourceMetadata
	Witness    Witness
	Closure    []recipe.ID
}

// PinOutcome describes what Pin did.
type PinOutcome struct {
	// Pin is the (possibly newly-created) Pin in the lockfile.
	Pin *Pin

	// Created is true when this call created the Pin (i.e. it was the
	// first witness for the (Coordinate, Version) pair).
	Created bool

	// WitnessAdded is true when this call appended a new witness to
	// the Pin's witness set (false if the witness was already present).
	WitnessAdded bool
}

// RemovalOutcome describes what RemoveWitness did.
type RemovalOutcome struct {
	// PinRemoved is true when removing this witness left the pin with
	// zero witnesses, causing the lockfile to drop the pin entirely.
	// When PinRemoved is true, the substrate's manifest store should
	// Detach the manifest to release blob refcounts.
	PinRemoved bool

	// ManifestID is the manifest the removed pin referenced. Useful
	// for the caller to feed into manifest.Store.Detach when
	// PinRemoved is true.
	ManifestID manifest.ID

	// RemainingWitnesses is the witness count after removal. Zero when
	// PinRemoved is true.
	RemainingWitnesses int
}

// Snapshot is a point-in-time copy of the lockfile suitable for
// serialization, inspection, or restoration.
type Snapshot struct {
	SessionID string
	CreatedAt time.Time
	Pins      []Pin
}
