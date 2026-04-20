// Package fetcher resolves a recipe's FetchSpec into bytes in memory.
//
// Fetchers are pluggable per URL scheme. The substrate ships fetchers
// for "https://" and "disk://" by default; additional fetchers
// (git://, oci://, github-release://) can be registered by callers as
// they are implemented.
//
// Fetched bytes flow through the verifier (which validates against the
// recipe's expected hash) and into the extractor without ever touching
// host disk. A fetcher that allocated more memory than the substrate's
// per-fetch cap can be passed a Limiter to surface a typed error early.
package fetcher

import (
	"context"
	"errors"
	"fmt"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Fetcher resolves a FetchSpec into bytes.
type Fetcher interface {
	// Scheme returns the URL scheme (without "://") this fetcher
	// handles. Used by the Registry to dispatch.
	Scheme() string

	// Fetch reads the bytes named by the spec and returns them in
	// memory. The byte slice belongs to the caller after return.
	//
	// Implementations must respect ctx cancellation and any
	// MaxBytes constraint advisable for the source.
	Fetch(ctx context.Context, spec recipe.FetchSpec, opts FetchOptions) ([]byte, error)
}

// FetchOptions are runtime parameters that compose with the recipe's
// FetchSpec. They differ per fetch invocation (e.g. progress reporting,
// per-fetch byte caps from the substrate config).
type FetchOptions struct {
	// MaxBytes caps the number of bytes the fetcher will read. Zero
	// means unlimited. Fetchers that exceed this cap return
	// ErrFetchTooLarge.
	MaxBytes int64

	// Progress, when non-nil, receives byte-count updates during the
	// fetch. Used by the substrate's UI surfaces to show provision
	// progress for large downloads.
	Progress ProgressFunc

	// Platform is the host platform tuple used for substituting {os},
	// {arch}, {libc} in source URLs and for matching PlatformPin.
	// Fetchers that need template substitution call SubstitutePlatform.
	Platform recipe.Platform
}

// ProgressFunc receives byte-count updates during a fetch. Calls are
// monotonically non-decreasing; the final call after a successful fetch
// reports the total byte count.
type ProgressFunc func(bytesRead int64)

// SubstitutePlatform applies {os}, {arch}, {libc} replacements to s.
//
// Substitution is deliberately limited to the three primitive fields.
// Recipes that need ecosystem-specific triple forms (Rust's
// "x86_64-unknown-linux-gnu", Python wheels' platform tags, etc.)
// either use multiple FetchSpecs with PlatformPin to disambiguate, or
// assemble the triple from the primitives.
func SubstitutePlatform(s string, p recipe.Platform) string {
	out := s
	out = replaceAll(out, "{os}", p.OS)
	out = replaceAll(out, "{arch}", p.Arch)
	out = replaceAll(out, "{libc}", p.Libc)
	return out
}

func replaceAll(s, old, new string) string {
	if old == "" || s == "" {
		return s
	}
	return stringReplaceAll(s, old, new)
}

// Errors common across fetchers.
var (
	ErrUnknownScheme  = errors.New("fetcher: no fetcher registered for scheme")
	ErrFetchTooLarge  = errors.New("fetcher: response exceeds MaxBytes")
	ErrFetchFailed    = errors.New("fetcher: fetch failed")
	ErrInvalidSource  = errors.New("fetcher: invalid source URL")
	ErrEmptyResponse  = errors.New("fetcher: response was empty")
)

// errFetch wraps an underlying error with fetch context.
func errFetch(scheme, source string, cause error) error {
	return fmt.Errorf("%w: %s://%s: %w", ErrFetchFailed, scheme, source, cause)
}
