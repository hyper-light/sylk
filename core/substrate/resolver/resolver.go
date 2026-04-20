// Package resolver defines the per-ecosystem adapter that translates
// ecosystem-native version constraints into substrate recipe IDs.
//
// Resolvers are the only ecosystem-specific code in the substrate's
// dependency-resolution path. They wrap each ecosystem's canonical
// solver — PubGrub for Python/Cargo/Hex, npm-arborist for npm with
// peer-dep semantics, MVS for Go, Maven's resolver, OPAM's solver,
// Bundler's, etc. — and emit a topologically-sorted closure of recipe
// IDs the substrate runtime then provisions.
//
// The lockfile stores the resolved closure; the substrate runtime
// doesn't care which solver picked the recipe IDs.
//
// Phase 3 ships the Resolver interface and a Direct resolver (no
// constraint solving — used for GitHub Releases, custom HTTPS,
// disk-fallback, and any other "I know which recipe I want" path).
// Subsequent phases wire ecosystem-specific resolvers as their
// implementations stabilize.
package resolver

import (
	"context"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Resolver translates ecosystem-native constraints into a substrate
// closure of recipe IDs.
type Resolver interface {
	// Ecosystem returns the resolver's ecosystem identifier. Used by
	// the dispatch Registry to route Resolve requests.
	Ecosystem() string

	// Resolve takes a list of constraints (e.g. for npm: [(react,
	// "^18.3"), (react-dom, "^18.3")]) and produces the closure of
	// recipe IDs that satisfy them, in topological order
	// (dependencies before dependents).
	Resolve(ctx context.Context, request ResolveRequest) (ResolveResult, error)
}

// ResolveRequest carries the inputs to a single resolution.
type ResolveRequest struct {
	// Constraints is the set of ecosystem-native constraints to
	// satisfy. The resolver decides how to interpret the strings
	// (semver, MVS, Maven version range, etc.).
	Constraints []Constraint

	// Platform is the host (or target) platform tuple. Resolvers
	// that emit platform-specific recipes use this to pick variants
	// (e.g. wheels for cp312-cp312-manylinux_x86_64).
	Platform recipe.Platform

	// LockfileSnapshot supplies the existing CRDT pin set as resolver
	// hints. A well-behaved resolver prefers compatible existing pins
	// over fresh resolutions, satisfying the lockfile's "monotonic
	// within a pipeline" invariant.
	//
	// Opaque: resolvers receive it as bytes/struct depending on their
	// internal representation; the lockfile package owns the conversion.
	LockfileSnapshot any
}

// Constraint is one (name, version_constraint) pair.
type Constraint struct {
	Name       string
	Constraint string // ecosystem-native: ">=8.0,<9.0", "^1.2", "~> 3.5", etc.
}

// ResolveResult is the resolver's output.
type ResolveResult struct {
	// Closure is the topologically-sorted closure of recipe IDs that
	// satisfy the request. Dependencies appear before their dependents.
	Closure []recipe.ID

	// PinJustifications maps each recipe ID in the closure to a
	// human-readable explanation of why this version was chosen
	// (PubGrub explanation traces, MVS minimum-selection reasoning,
	// etc.). Stored verbatim in the lockfile so "why is this pinned?"
	// always has a structured answer.
	PinJustifications map[recipe.ID]string

	// Conflicts reports any constraints the resolver could not satisfy.
	// Empty on success; non-empty when the resolver had to surrender
	// (e.g. peer-dep loops in npm, version-range incompatibilities in
	// PubGrub). The caller decides whether to surface or escalate.
	Conflicts []ConstraintConflict
}

// ConstraintConflict describes one unsatisfiable constraint situation.
type ConstraintConflict struct {
	// Conflicting names the constraints that could not be jointly
	// satisfied.
	Conflicting []Constraint

	// Reason is the resolver's human-readable explanation.
	Reason string
}

// HasConflicts is a convenience predicate.
func (r ResolveResult) HasConflicts() bool {
	return len(r.Conflicts) > 0
}
