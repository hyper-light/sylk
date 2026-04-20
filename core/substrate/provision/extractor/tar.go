package extractor

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/adalundhe/sylk/core/substrate/recipe"

	"github.com/klauspost/compress/zstd"
)

// TarExtractor handles tar archives, with optional gzip / xz / zstd
// decompression based on the format hint.
//
// xz support is intentionally unimplemented at this stage: pure-Go xz
// decoders exist but introduce a CGO-vs-pure-Go tradeoff the substrate
// hasn't yet decided. When a recipe declares tar.xz, the extractor
// returns ErrXzNotImplemented; recipes that need xz should be rewritten
// to fetch the gzip variant the upstream typically also publishes.
type TarExtractor struct {
	format recipe.ExtractFormat
}

// NewTarExtractor constructs a TarExtractor for the given format hint.
func NewTarExtractor(format recipe.ExtractFormat) *TarExtractor {
	return &TarExtractor{format: format}
}

// Format returns the extract format this extractor handles.
func (t *TarExtractor) Format() recipe.ExtractFormat { return t.format }

// Extract decompresses (per format), parses the tar stream, and yields
// each file/symlink entry through onEntry.
func (t *TarExtractor) Extract(r io.Reader, spec recipe.ExtractSpec, onEntry func(Entry) error) error {
	decompressed, closer, err := t.openDecompressor(r)
	if err != nil {
		return err
	}
	defer closer()
	return readTarEntries(tar.NewReader(decompressed), spec, onEntry)
}

func (t *TarExtractor) openDecompressor(r io.Reader) (io.Reader, func(), error) {
	switch t.format {
	case recipe.ExtractFormatTar:
		return r, func() {}, nil
	case recipe.ExtractFormatTarGz:
		return openGzip(r)
	case recipe.ExtractFormatTarZst:
		return openZstd(r)
	case recipe.ExtractFormatTarXz:
		return nil, nil, ErrXzNotImplemented
	}
	return nil, nil, fmt.Errorf("%w: %q", ErrUnknownFormat, t.format)
}

func openGzip(r io.Reader) (io.Reader, func(), error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: gzip: %w", ErrCorruptArchive, err)
	}
	return gz, func() { _ = gz.Close() }, nil
}

func openZstd(r io.Reader) (io.Reader, func(), error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: zstd: %w", ErrCorruptArchive, err)
	}
	return zr, zr.Close, nil
}

func readTarEntries(tr *tar.Reader, spec recipe.ExtractSpec, onEntry func(Entry) error) error {
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: tar header: %w", ErrCorruptArchive, err)
		}
		if err := processTarEntry(tr, header, spec, onEntry); err != nil {
			return err
		}
	}
}

func processTarEntry(tr *tar.Reader, header *tar.Header, spec recipe.ExtractSpec, onEntry func(Entry) error) error {
	entry, ok, err := materializeTarEntry(tr, header, spec)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return onEntry(entry)
}

func materializeTarEntry(tr *tar.Reader, header *tar.Header, spec recipe.ExtractSpec) (Entry, bool, error) {
	if !isExtractableTarEntry(header) {
		return Entry{}, false, nil
	}
	relPath, ok := applyStripPrefix(header.Name, spec.StripPrefix)
	if !ok || relPath == "" {
		return Entry{}, false, nil
	}
	if err := validateExtractedPath(relPath); err != nil {
		return Entry{}, false, err
	}
	if !passesFilter(relPath, spec.Filter) {
		return Entry{}, false, nil
	}
	return buildTarEntry(tr, header, relPath, spec.PreserveMode)
}

func isExtractableTarEntry(header *tar.Header) bool {
	switch header.Typeflag {
	case tar.TypeReg, tar.TypeRegA, tar.TypeSymlink:
		return true
	}
	return false
}

func buildTarEntry(tr *tar.Reader, header *tar.Header, relPath string, preserveMode bool) (Entry, bool, error) {
	if header.Typeflag == tar.TypeSymlink {
		return Entry{
			Path:    relPath,
			Mode:    resolveMode(header.Mode, preserveMode, true),
			Symlink: true,
			Content: []byte(header.Linkname),
		}, true, nil
	}
	content, err := io.ReadAll(tr)
	if err != nil {
		return Entry{}, false, fmt.Errorf("%w: read entry %q: %w", ErrCorruptArchive, relPath, err)
	}
	return Entry{
		Path:    relPath,
		Mode:    resolveMode(header.Mode, preserveMode, false),
		Content: content,
	}, true, nil
}

func resolveMode(archived int64, preserveMode, isSymlink bool) uint32 {
	if preserveMode {
		return uint32(archived) & modeMask
	}
	if isSymlink {
		return defaultSymlinkMode
	}
	if isExecutableMode(uint32(archived)) {
		return defaultExecutableMode
	}
	return defaultRegularMode
}

func isExecutableMode(m uint32) bool {
	return m&executableBitsMask != 0
}

const (
	// modeMask isolates the bottom 12 bits, which carry the unix mode
	// bits the substrate cares about. Higher bits in the int64 mode
	// field hold archive-format flags we deliberately discard.
	modeMask = 0o7777

	// executableBitsMask matches any of the user/group/other execute
	// bits — used to decide between defaultRegularMode and
	// defaultExecutableMode when preserveMode is false.
	executableBitsMask = 0o111

	defaultRegularMode    uint32 = 0o644
	defaultExecutableMode uint32 = 0o755
	defaultSymlinkMode    uint32 = 0o777
)

// ErrXzNotImplemented signals that the substrate has not yet wired an
// xz decoder. Recipes targeting tar.xz should be re-pinned to a gzip
// variant of the same upstream artifact when one is available.
var ErrXzNotImplemented = fmt.Errorf("extractor: xz decoding not yet implemented; use tar.gz")
