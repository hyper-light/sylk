package extractor

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// ZipExtractor handles zip archives.
//
// archive/zip requires an io.ReaderAt + size, so the extractor buffers
// the input bytes (the recipe runtime already holds them in memory).
// This is acceptable: zip archives in our use cases (Python wheels,
// Java jars, GitHub Releases zips) are bounded by the substrate's
// fetch byte cap.
type ZipExtractor struct{}

// NewZipExtractor constructs a ZipExtractor.
func NewZipExtractor() *ZipExtractor { return &ZipExtractor{} }

// Format returns "zip".
func (ZipExtractor) Format() recipe.ExtractFormat { return recipe.ExtractFormatZip }

// Extract reads zip entries and yields them through onEntry.
func (ZipExtractor) Extract(r io.Reader, spec recipe.ExtractSpec, onEntry func(Entry) error) error {
	buf, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("%w: zip buffer: %w", ErrCorruptArchive, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return fmt.Errorf("%w: zip header: %w", ErrCorruptArchive, err)
	}
	return iterateZipFiles(zr, spec, onEntry)
}

func iterateZipFiles(zr *zip.Reader, spec recipe.ExtractSpec, onEntry func(Entry) error) error {
	for _, f := range zr.File {
		if err := processZipEntry(f, spec, onEntry); err != nil {
			return err
		}
	}
	return nil
}

func processZipEntry(f *zip.File, spec recipe.ExtractSpec, onEntry func(Entry) error) error {
	entry, ok, err := materializeZipEntry(f, spec)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return onEntry(entry)
}

func materializeZipEntry(f *zip.File, spec recipe.ExtractSpec) (Entry, bool, error) {
	if isZipDirectory(f) {
		return Entry{}, false, nil
	}
	relPath, ok := applyStripPrefix(f.Name, spec.StripPrefix)
	if !ok || relPath == "" {
		return Entry{}, false, nil
	}
	if err := validateExtractedPath(relPath); err != nil {
		return Entry{}, false, err
	}
	if !passesFilter(relPath, spec.Filter) {
		return Entry{}, false, nil
	}
	return buildZipEntry(f, relPath, spec.PreserveMode)
}

func isZipDirectory(f *zip.File) bool {
	if len(f.Name) == 0 {
		return true
	}
	return f.Name[len(f.Name)-1] == '/'
}

func buildZipEntry(f *zip.File, relPath string, preserveMode bool) (Entry, bool, error) {
	rc, err := f.Open()
	if err != nil {
		return Entry{}, false, fmt.Errorf("%w: open zip entry %q: %w", ErrCorruptArchive, relPath, err)
	}
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		return Entry{}, false, fmt.Errorf("%w: read zip entry %q: %w", ErrCorruptArchive, relPath, err)
	}
	return Entry{
		Path:    relPath,
		Mode:    resolveZipMode(f, preserveMode),
		Content: content,
	}, true, nil
}

func resolveZipMode(f *zip.File, preserveMode bool) uint32 {
	if preserveMode {
		return uint32(f.Mode().Perm())
	}
	if isExecutableMode(uint32(f.Mode().Perm())) {
		return defaultExecutableMode
	}
	return defaultRegularMode
}
