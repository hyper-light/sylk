// Package extractor turns fetched archive bytes into a stream of
// (path, content, mode) entries the substrate runtime can register as
// blobs and assemble into a manifest.
//
// Extractors operate entirely in memory — input is a []byte (or
// io.Reader over the bytes), output is an Entry stream. No temporary
// files are written to disk; the no-disk-write invariant is preserved.
//
// The package supports the substrate's core archive formats:
//
//   - tar (raw, gzip, xz, zstd compression variants)
//   - zip
//   - raw (single-file artifacts: a binary, a font, a corpus)
//   - git-tree and oci-layer are placeholder stubs for Phase 2c
package extractor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Entry is one extracted file or symlink.
type Entry struct {
	// Path is the entry's path, after StripPrefix has been applied.
	// Always POSIX-style (forward slashes); leading slash is stripped
	// so paths are substrate-relative.
	Path string

	// Mode is the entry's unix mode bits as recorded in the archive.
	// May be replaced by the OutputMapping at manifest construction
	// time; the extractor faithfully reports what the archive said.
	Mode uint32

	// Symlink indicates whether the entry is a symbolic link. When
	// true, Content is the symlink target (a path string), not file
	// data.
	Symlink bool

	// Content is the entry's bytes (for files) or the symlink target
	// (for symlinks). Always non-nil; empty files are []byte{}, not
	// nil.
	Content []byte
}

// Extractor reads archive bytes and yields Entries via a callback.
//
// The callback approach (rather than returning a slice) keeps memory
// bounded: a 50MB tarball expands to whatever its file count is, but
// the extractor only holds one Entry at a time. The callback can fan
// out to the blob store directly without materializing the full file
// list at once.
//
// The callback's error halts extraction immediately; the caller surfaces
// it. Returning a nil error continues to the next entry.
type Extractor interface {
	// Format identifies the archive format this extractor handles.
	Format() recipe.ExtractFormat

	// Extract reads from r, applying spec-driven filtering and prefix
	// stripping, and invokes onEntry for each entry that survives the
	// filter.
	Extract(r io.Reader, spec recipe.ExtractSpec, onEntry func(Entry) error) error
}

// Errors common across extractors.
var (
	ErrUnknownFormat   = errors.New("extractor: unknown archive format")
	ErrCorruptArchive  = errors.New("extractor: archive is corrupt or truncated")
	ErrPathEscape      = errors.New("extractor: archive entry escapes target via .. or absolute path")
	ErrUnsupportedKind = errors.New("extractor: unsupported entry kind (e.g. device, fifo, hardlink)")
)

// Registry dispatches extraction by archive format.
type Registry struct {
	extractors map[recipe.ExtractFormat]Extractor
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{extractors: make(map[recipe.ExtractFormat]Extractor)}
}

// NewDefaultRegistry returns a registry preloaded with the substrate's
// core extractors: tar (with gzip/xz/zstd compression variants), zip,
// and raw.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	r.Install(NewTarExtractor(recipe.ExtractFormatTar))
	r.Install(NewTarExtractor(recipe.ExtractFormatTarGz))
	r.Install(NewTarExtractor(recipe.ExtractFormatTarXz))
	r.Install(NewTarExtractor(recipe.ExtractFormatTarZst))
	r.Install(NewZipExtractor())
	r.Install(NewRawExtractor())
	return r
}

// Install registers e under e.Format(), replacing any prior registration.
func (r *Registry) Install(e Extractor) {
	r.extractors[e.Format()] = e
}

// Extract dispatches by format.
func (r *Registry) Extract(data []byte, spec recipe.ExtractSpec, onEntry func(Entry) error) error {
	e, ok := r.extractors[spec.Format]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownFormat, spec.Format)
	}
	return e.Extract(bytesReader(data), spec, onEntry)
}

// applyStripPrefix removes the configured prefix from p. If p does not
// start with the prefix, the entry is dropped (returns "", false). The
// returned path is leading-slash-stripped so it is substrate-relative.
func applyStripPrefix(p, prefix string) (string, bool) {
	stripped := strings.TrimPrefix(p, prefix)
	if prefix != "" && stripped == p {
		return "", false
	}
	return strings.TrimPrefix(stripped, "/"), true
}

// validateExtractedPath rejects paths that try to escape the manifest's
// namespace via ".." segments. Path traversal in archives is a known
// supply-chain attack vector ("zip slip"); any segment equal to ".."
// is refused regardless of where it appears in the path.
//
// Absolute-path inputs (leading slash) are already stripped by
// applyStripPrefix, so by the time this is called p is substrate-relative
// and the only escape vector left is `..` segments.
func validateExtractedPath(p string) error {
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return fmt.Errorf("%w: %q", ErrPathEscape, p)
		}
	}
	return nil
}

// passesFilter reports whether p passes the recipe's include/exclude
// filter. Empty filter accepts everything.
func passesFilter(p string, filter *recipe.Filter) bool {
	if filter == nil {
		return true
	}
	if !matchesAnyOrEmpty(p, filter.Include) {
		return false
	}
	if matchesAny(p, filter.Exclude) {
		return false
	}
	return true
}

func matchesAnyOrEmpty(p string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	return matchesAny(p, patterns)
}

func matchesAny(p string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchedPattern(pattern, p) {
			return true
		}
	}
	return false
}

func matchedPattern(pattern, p string) bool {
	matched, err := path.Match(pattern, p)
	if err != nil {
		return false
	}
	return matched
}

// bytesReader returns an io.Reader over data without copying.
func bytesReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}
