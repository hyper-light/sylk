// Package direct implements a no-solving resolver that pins recipes
// the caller has already chosen.
//
// Direct is the right resolver for ecosystems that have no real
// constraint-solving need: GitHub Releases artifacts (the user pins a
// specific tag), custom HTTPS downloads (the user pins a specific URL
// and hash), disk-fallback (the user pins a host binary), arbitrary
// internal tools fetched from corporate artifact registries with no
// inter-package dependencies. The "constraint" in these cases is just
// "I want exactly this recipe."
//
// More structured ecosystems (Python wheels, Cargo crates, npm
// packages) have their own resolvers that use PubGrub, MVS,
// npm-arborist, etc. Direct is the floor — the simplest possible
// resolver that satisfies the Resolver interface.
package direct

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/adalundhe/sylk/core/substrate/recipe"
	"github.com/adalundhe/sylk/core/substrate/resolver"
)

// Resolver maps constraint names directly to pre-registered recipe
// IDs. The caller registers (name, version) → recipe.ID mappings
// up-front; Resolve then satisfies any matching Constraint by returning
// the registered recipe ID.
//
// "Constraint" semantics for Direct: the constraint string is treated
// as the version. A request for {Name: "ripgrep", Constraint: "14.1.0"}
// looks up the recipe ID registered for ("ripgrep", "14.1.0") and
// returns it as the closure. No partial matching, no version range
// satisfaction — exact name+version pinning only.
type Resolver struct {
	ecosystem string

	mu      sync.RWMutex
	recipes map[entryKey]recipe.ID
}

// entryKey indexes a registration.
type entryKey struct {
	name    string
	version string
}

// New constructs a Direct resolver for the named ecosystem.
//
// Common ecosystem names: "github-release", "custom-https",
// "disk-fallback". The resolver does not validate the ecosystem string
// — it is whatever the caller and the lockfile agree it should be.
func New(ecosystem string) *Resolver {
	return &Resolver{
		ecosystem: ecosystem,
		recipes:   make(map[entryKey]recipe.ID),
	}
}

// Ecosystem returns the resolver's ecosystem identifier.
func (r *Resolver) Ecosystem() string { return r.ecosystem }

// Register associates (name, version) with recipeID. Subsequent
// Resolve calls with a matching Constraint will return recipeID in the
// closure.
//
// Re-registering the same (name, version) replaces the previous recipe
// ID. This is intentional: an upstream artifact's bytes can change
// (republish, re-tag) and the Direct caller may need to update the
// pin without the full recipe-deletion ritual.
func (r *Resolver) Register(name, version string, recipeID recipe.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recipes[entryKey{name: name, version: version}] = recipeID
}

// Unregister removes the (name, version) → recipeID mapping. Returns
// true if a mapping existed.
func (r *Resolver) Unregister(name, version string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := entryKey{name: name, version: version}
	if _, ok := r.recipes[key]; !ok {
		return false
	}
	delete(r.recipes, key)
	return true
}

// Resolve satisfies each Constraint by exact (name, Constraint) lookup.
//
// Direct does not do dependency closure expansion (it has no concept
// of a recipe's transitive dependencies). The Closure returned is just
// the recipe IDs corresponding to the input constraints, in input order.
//
// Returns a Conflict for any constraint that has no matching registered
// recipe.
func (r *Resolver) Resolve(_ context.Context, req resolver.ResolveRequest) (resolver.ResolveResult, error) {
	if len(req.Constraints) == 0 {
		return resolver.ResolveResult{}, ErrNoConstraints
	}
	return r.resolveAll(req.Constraints), nil
}

func (r *Resolver) resolveAll(constraints []resolver.Constraint) resolver.ResolveResult {
	out := resolver.ResolveResult{
		PinJustifications: make(map[recipe.ID]string, len(constraints)),
	}
	for i := range constraints {
		r.resolveOne(constraints[i], &out)
	}
	return out
}

func (r *Resolver) resolveOne(c resolver.Constraint, out *resolver.ResolveResult) {
	id, found := r.lookup(c.Name, c.Constraint)
	if !found {
		out.Conflicts = append(out.Conflicts, resolver.ConstraintConflict{
			Conflicting: []resolver.Constraint{c},
			Reason:      fmt.Sprintf("no recipe registered for %q@%q", c.Name, c.Constraint),
		})
		return
	}
	out.Closure = append(out.Closure, id)
	out.PinJustifications[id] = directJustification(c)
}

func (r *Resolver) lookup(name, version string) (recipe.ID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.recipes[entryKey{name: name, version: version}]
	return id, ok
}

func directJustification(c resolver.Constraint) string {
	return fmt.Sprintf("direct pin: %s@%s", c.Name, c.Constraint)
}

// ErrNoConstraints is returned when Resolve is called with an empty
// constraint list. Resolvers cannot resolve nothing.
var ErrNoConstraints = errors.New("direct: resolve called with no constraints")
