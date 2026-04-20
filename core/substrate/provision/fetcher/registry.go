package fetcher

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Registry dispatches FetchSpecs to the Fetcher registered for their
// URL scheme. Safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	fetchers map[string]Fetcher
}

// NewRegistry returns a Registry with no fetchers installed. Callers
// register fetchers via Install. Use NewDefaultRegistry to get the
// substrate's stock fetcher set.
func NewRegistry() *Registry {
	return &Registry{fetchers: make(map[string]Fetcher)}
}

// NewDefaultRegistry returns a Registry preloaded with the substrate's
// default fetcher set: HTTPS and disk://.
//
// HTTP without TLS is intentionally absent — supply chain integrity
// would be undermined by allowing in-transit modification. Recipes that
// reference http:// URLs are refused at fetch time with ErrUnknownScheme.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	r.Install(NewHTTPSFetcher(DefaultHTTPSConfig()))
	r.Install(NewDiskFetcher())
	return r
}

// Install registers f. If a fetcher already exists for f.Scheme(), it
// is replaced.
func (r *Registry) Install(f Fetcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fetchers[f.Scheme()] = f
}

// Fetch resolves the FetchSpec by dispatching to the registered
// fetcher for the source's URL scheme. Source URL templates are
// substituted with the host platform tuple before dispatch.
func (r *Registry) Fetch(ctx context.Context, spec recipe.FetchSpec, opts FetchOptions) ([]byte, error) {
	resolved := resolveFetchSpec(spec, opts.Platform)
	scheme, err := schemeOf(resolved.Source)
	if err != nil {
		return nil, err
	}
	f, err := r.lookup(scheme)
	if err != nil {
		return nil, err
	}
	return f.Fetch(ctx, resolved, opts)
}

func (r *Registry) lookup(scheme string) (Fetcher, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.fetchers[scheme]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownScheme, scheme)
	}
	return f, nil
}

// resolveFetchSpec returns a copy of spec with template substitution
// applied to Source. Headers are not substituted here; they may
// reference env vars, which the HTTPS fetcher resolves at request time.
func resolveFetchSpec(spec recipe.FetchSpec, p recipe.Platform) recipe.FetchSpec {
	out := spec
	out.Source = SubstitutePlatform(spec.Source, p)
	return out
}

func schemeOf(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrInvalidSource, rawURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		return "", fmt.Errorf("%w: %s: missing scheme", ErrInvalidSource, rawURL)
	}
	return scheme, nil
}

// PickFetchSpec selects the best FetchSpec from a recipe's fetch list
// for the given host platform. The selection rule is:
//
//  1. Among FetchSpecs with PlatformPin set, pick the first whose pin
//     matches the host.
//  2. If no platform-pinned spec matches, pick the first FetchSpec with
//     no PlatformPin.
//  3. If neither, return ErrNoMatchingFetchSpec.
//
// The order of FetchSpecs in the recipe is therefore meaningful for
// disambiguation among equally-matching pins; recipe authors put the
// most specific or preferred source first.
func PickFetchSpec(specs []recipe.FetchSpec, host recipe.Platform) (recipe.FetchSpec, error) {
	if pinned, ok := pickPinned(specs, host); ok {
		return pinned, nil
	}
	if unpinned, ok := pickUnpinned(specs); ok {
		return unpinned, nil
	}
	return recipe.FetchSpec{}, ErrNoMatchingFetchSpec
}

func pickPinned(specs []recipe.FetchSpec, host recipe.Platform) (recipe.FetchSpec, bool) {
	for i := range specs {
		if matchesPin(specs[i], host) {
			return specs[i], true
		}
	}
	return recipe.FetchSpec{}, false
}

func matchesPin(spec recipe.FetchSpec, host recipe.Platform) bool {
	if spec.PlatformPin == nil {
		return false
	}
	return host.Matches(*spec.PlatformPin)
}

func pickUnpinned(specs []recipe.FetchSpec) (recipe.FetchSpec, bool) {
	for i := range specs {
		if specs[i].PlatformPin == nil {
			return specs[i], true
		}
	}
	return recipe.FetchSpec{}, false
}

// ErrNoMatchingFetchSpec is returned when none of a recipe's FetchSpecs
// match the host platform tuple.
var ErrNoMatchingFetchSpec = fmt.Errorf("fetcher: no fetch spec matches host platform")
