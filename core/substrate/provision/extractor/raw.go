package extractor

import (
	"fmt"
	"io"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// RawExtractor "extracts" a single file: the entire fetched payload is
// emitted as one entry at path "/data" by default, with the executable
// bit set. The recipe's OutputMapping is responsible for renaming this
// canonical "/data" path into the substrate path the manifest claims
// (e.g. "/bin/ripgrep").
//
// Use raw for single-binary fetches: a precompiled CLI from GitHub
// Releases that wasn't shipped inside an archive, a font, a model
// checkpoint, an image — anything that's a single content blob.
type RawExtractor struct{}

// NewRawExtractor constructs a RawExtractor.
func NewRawExtractor() *RawExtractor { return &RawExtractor{} }

// Format returns "raw".
func (RawExtractor) Format() recipe.ExtractFormat { return recipe.ExtractFormatRaw }

// Extract emits a single Entry containing all of r's bytes.
func (RawExtractor) Extract(r io.Reader, spec recipe.ExtractSpec, onEntry func(Entry) error) error {
	content, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("%w: raw read: %w", ErrCorruptArchive, err)
	}
	return onEntry(Entry{
		Path:    rawCanonicalPath,
		Mode:    rawDefaultMode(spec),
		Content: content,
	})
}

func rawDefaultMode(spec recipe.ExtractSpec) uint32 {
	if spec.PreserveMode {
		return defaultExecutableMode
	}
	return defaultExecutableMode
}

// rawCanonicalPath is the path raw-extracted blobs occupy in the
// extractor output. The OutputMapping renames it into a substrate-meaningful
// path. Kept as a constant so recipe authors and tests can reference
// it by symbol rather than by string literal.
const rawCanonicalPath = "/data"
