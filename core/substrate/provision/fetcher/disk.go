package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// DiskFetcher reads bytes from the host filesystem.
//
// disk:// is the substrate's degraded-mode fetcher. It is the only
// fetcher that intentionally touches host disk, used when substrate
// provisioning genuinely cannot proceed (network down, no published
// artifact for the host platform tuple, build broken). Guardian's
// disk_fallback_gate decides whether disk:// is allowed at all per
// pipeline; if Guardian denies, the registry never invokes this fetcher.
//
// disk:// reads, but never writes. The substrate's no-disk-write
// invariant is preserved — disk fallback is a controlled read of host
// content, not a substrate-side disk write.
type DiskFetcher struct{}

// NewDiskFetcher returns a DiskFetcher.
func NewDiskFetcher() *DiskFetcher { return &DiskFetcher{} }

// Scheme returns "disk".
func (DiskFetcher) Scheme() string { return "disk" }

// Fetch reads the file at the path component of spec.Source.
func (DiskFetcher) Fetch(ctx context.Context, spec recipe.FetchSpec, opts FetchOptions) ([]byte, error) {
	path, err := pathFromDiskURL(spec.Source)
	if err != nil {
		return nil, err
	}
	return readFileBounded(ctx, path, opts)
}

func pathFromDiskURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidSource, err)
	}
	if u.Scheme != "disk" {
		return "", fmt.Errorf("%w: expected disk:// scheme, got %q", ErrInvalidSource, u.Scheme)
	}
	return u.Path, nil
}

func readFileBounded(ctx context.Context, path string, opts FetchOptions) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errFetch("disk", path, err)
	}
	defer f.Close()
	return readAllBounded(ctx, f, opts, "disk", path)
}

func readAllBounded(ctx context.Context, r io.Reader, opts FetchOptions, scheme, source string) ([]byte, error) {
	limited := wrapLimit(r, opts.MaxBytes)
	bufrr, err := readWithProgress(ctx, limited, opts.Progress)
	if err != nil {
		return nil, classifyReadError(err, scheme, source)
	}
	if len(bufrr) == 0 {
		return nil, fmt.Errorf("%w: %s://%s", ErrEmptyResponse, scheme, source)
	}
	return bufrr, nil
}

func classifyReadError(err error, scheme, source string) error {
	if errors.Is(err, errLimitExceeded) {
		return fmt.Errorf("%w: %s://%s", ErrFetchTooLarge, scheme, source)
	}
	return errFetch(scheme, source, err)
}
