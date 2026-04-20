// Package ecosystem is the substrate's plugin point for language-
// specific recipe construction.
//
// The substrate is language-agnostic by design: blob, manifest, recipe,
// provision, lock, sandbox, view — none of those packages name a
// language or a package manager. The piece that *does* know about pip,
// npm, cargo, gem, go modules, gradle, opam, hex, dotnet, and so on is
// localized here, behind a single interface and a registry.
//
// An Ecosystem implementation is responsible for two concerns:
//
//  1. Recognizing a free-form install command — `pip install foo`,
//     `npm install bar@1.2.3`, `cargo install ripgrep`, `gem install
//     bundler`, `go install golang.org/x/tools/...@latest` — and
//     translating it into structured Coordinates the substrate can act
//     on.
//
//  2. Building a substrate Recipe for a given Coordinates value by
//     consulting that ecosystem's registry (PyPI Warehouse, npm
//     registry, crates.io API, rubygems.org, the Go module proxy, the
//     Maven Central API, etc.) and producing the FetchSpec / VerifySpec
//     / ExtractSpec / Output triple that the substrate runtime needs to
//     materialize the artifact.
//
// Neither the substrate provisioner nor the install skill knows
// anything about any specific ecosystem. They look plugins up by name
// in the Registry and dispatch generically. Adding a new language is a
// matter of writing one Ecosystem implementation — never a change to
// substrate or skill code.
//
// This package intentionally ships zero ecosystem implementations.
// Concrete plugins live in their own subpackages so the substrate's
// language-neutral core has no transitive dependency on any registry
// client.
package ecosystem

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Coordinates names one resolvable artifact within an ecosystem. The
// shape is deliberately minimal — anything more elaborate (extras,
// markers, peer ranges, optional features, build profiles) is encoded
// in the ecosystem-native Constraint string and the plugin's own
// recipe builder.
type Coordinates struct {
	// Ecosystem is the canonical ecosystem name the plugin reports via
	// Ecosystem.Name(). Routing keys off this field; plugins are free
	// to accept aliases at parse time but should always emit the
	// canonical name in the Coordinates they return.
	Ecosystem string

	// Name is the package name within the ecosystem. The exact spelling
	// is whatever the registry uses (case-sensitive for npm, normalized
	// to PEP 503 for python, lower-kebab for cargo, etc.). Plugins are
	// responsible for any normalization their registry requires.
	Name string

	// Version is the concrete version to pin. Empty means "the
	// registry's latest stable" — the plugin's recipe builder is
	// expected to resolve it.
	Version string

	// Constraint is the ecosystem-native version constraint string
	// the original install command expressed (`^1.2`, `>=8,<9`,
	// `~> 3.5`, etc.). Informational at this layer — the substrate
	// records it on the lockfile witness so future resolutions can see
	// what range the original install asked for.
	Constraint string
}

// IsZero reports whether c is the zero Coordinates value.
func (c Coordinates) IsZero() bool {
	return c.Ecosystem == "" && c.Name == "" && c.Version == "" && c.Constraint == ""
}

// BuildRecipeRequest is the input to Ecosystem.BuildRecipe.
type BuildRecipeRequest struct {
	// Coords identifies the package to build a recipe for.
	Coords Coordinates

	// Platform is the host (or target) platform tuple. Plugins use it
	// to select per-platform artifacts (wheels, prebuilt zips, native
	// gems, etc.). When the plugin has no platform-specific selection,
	// Platform is ignored.
	Platform recipe.Platform
}

// Ecosystem is one language- or package-manager-specific plugin.
//
// Implementations are expected to be safe for concurrent use: the
// Registry will call Parse and BuildRecipe from any goroutine.
type Ecosystem interface {
	// Name returns the canonical ecosystem identifier — "python",
	// "npm", "cargo", "gem", "go", "maven", "hex", "opam", "nuget",
	// "composer", and so on. The Registry routes on this string.
	Name() string

	// Parse tries to recognize command as an install instruction this
	// ecosystem knows how to handle. Returns the corresponding
	// Coordinates and true on a match. Plugins return false (with no
	// error) when command is not theirs to handle — the registry will
	// try the next plugin.
	//
	// Implementations should be conservative: only return true when
	// the command is unambiguously this ecosystem's. False positives
	// route real work to the wrong builder; false negatives just mean
	// the install skill falls back to the legacy broker path.
	Parse(command string) (Coordinates, bool)

	// BuildRecipe constructs a substrate Recipe for the requested
	// coordinates. Implementations consult their registry, select the
	// appropriate artifact for req.Platform, and emit a Recipe whose
	// Fetch / Verify / Extract / Output triple the substrate runtime
	// can execute.
	//
	// Plugins must populate Recipe.Metadata.Ecosystem with their Name()
	// — the manifest store uses it as part of the canonical PackageID.
	BuildRecipe(ctx context.Context, req BuildRecipeRequest) (*recipe.Recipe, error)
}

// Registry dispatches Parse and BuildRecipe across registered
// ecosystems. Safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	byName  map[string]Ecosystem
	ordered []Ecosystem
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Ecosystem)}
}

// Install registers eco. Install order is preserved; Parse iterates
// ecosystems in installation order, so earlier registrations win on
// ambiguous matches.
//
// Re-installing a plugin under the same Name() replaces the previous
// registration in byName but keeps its position in the ordered slice.
// This lets composition roots swap implementations (e.g. fixture
// plugins in tests) without reshuffling Parse precedence.
func (r *Registry) Install(eco Ecosystem) {
	if eco == nil {
		return
	}
	name := canonicalName(eco.Name())
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, existed := r.byName[name]; !existed {
		r.ordered = append(r.ordered, eco)
	} else {
		for i, existing := range r.ordered {
			if canonicalName(existing.Name()) == name {
				r.ordered[i] = eco
				break
			}
		}
	}
	r.byName[name] = eco
}

// Lookup returns the plugin registered under name, or false if none is
// registered.
func (r *Registry) Lookup(name string) (Ecosystem, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	eco, ok := r.byName[canonicalName(name)]
	return eco, ok
}

// Parse iterates registered ecosystems in installation order and
// returns the first plugin's match. Returns the matching Coordinates
// (with Ecosystem set to the plugin's canonical Name()) and true on
// match. Returns the zero Coordinates and false when no plugin
// recognized the command.
//
// Parse never returns an error: a free-form command that no plugin
// recognizes is a routing miss, not a substrate failure. Callers
// (typically the install skill) decide how to handle the miss —
// usually by falling back to the legacy broker path.
func (r *Registry) Parse(command string) (Coordinates, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return Coordinates{}, false
	}
	for _, eco := range r.snapshot() {
		coords, ok := eco.Parse(trimmed)
		if !ok {
			continue
		}
		coords.Ecosystem = canonicalName(eco.Name())
		return coords, true
	}
	return Coordinates{}, false
}

// BuildRecipe dispatches req to the plugin registered for
// req.Coords.Ecosystem. Returns ErrUnknownEcosystem when no plugin
// matches.
func (r *Registry) BuildRecipe(ctx context.Context, req BuildRecipeRequest) (*recipe.Recipe, error) {
	eco, ok := r.Lookup(req.Coords.Ecosystem)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEcosystem, req.Coords.Ecosystem)
	}
	rec, err := eco.BuildRecipe(ctx, req)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("ecosystem %q: build returned nil recipe", req.Coords.Ecosystem)
	}
	return rec, nil
}

// Names returns the canonical names of registered ecosystems in
// installation order.
func (r *Registry) Names() []string {
	snap := r.snapshot()
	out := make([]string, 0, len(snap))
	for _, eco := range snap {
		out = append(out, canonicalName(eco.Name()))
	}
	return out
}

// Len reports the number of registered ecosystems.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.ordered)
}

func (r *Registry) snapshot() []Ecosystem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Ecosystem, len(r.ordered))
	copy(out, r.ordered)
	return out
}

func canonicalName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ErrUnknownEcosystem is returned by BuildRecipe when no plugin is
// registered for the requested ecosystem.
var ErrUnknownEcosystem = errors.New("ecosystem: no plugin registered")
