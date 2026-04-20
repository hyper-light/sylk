package manifest

import (
	"sort"

	"lukechampine.com/blake3"
)

// ComputeID derives the ManifestID from the load-bearing fields of m.
//
// The hash is computed over a canonical encoding of:
//   - Package coordinates (ecosystem, name, version)
//   - File entries sorted by path, each contributing path + blob hash +
//     mode + symlink flag
//   - SourceMetadata (recipe id, fetch url, fetch hash, ecosystem hash)
//
// The Metadata field is excluded from the hash because its fields are
// informational; cosmetic differences in license or description should
// not change a manifest's identity.
//
// Inputs are length-prefixed before hashing to prevent injection attacks
// where one field's content masquerades as another's boundary.
func ComputeID(m *Manifest) ID {
	h := blake3.New(IDSize, nil)
	hashStringField(h, m.Package.Ecosystem)
	hashStringField(h, m.Package.Name)
	hashStringField(h, m.Package.Version)
	hashFileEntries(h, m.Files)
	hashSourceMetadata(h, &m.Source)
	var out ID
	copy(out[:], h.Sum(nil))
	return out
}

func hashFileEntries(h interface {
	Write([]byte) (int, error)
}, files []FileEntry) {
	sorted := sortedCopy(files)
	hashIntField(h, len(sorted))
	for i := range sorted {
		hashFileEntry(h, &sorted[i])
	}
}

func hashFileEntry(h interface {
	Write([]byte) (int, error)
}, e *FileEntry) {
	hashStringField(h, e.Path)
	hashBytesField(h, e.BlobHash[:])
	hashUint32Field(h, uint32(e.Mode))
	hashBoolField(h, e.Symlink)
}

func hashSourceMetadata(h interface {
	Write([]byte) (int, error)
}, s *SourceMetadata) {
	hashStringField(h, s.RecipeID)
	hashStringField(h, s.FetchURL)
	hashBytesField(h, s.FetchHash[:])
	hashStringField(h, s.EcosystemHash)
	hashStringField(h, s.EcosystemHashAlgorithm)
	hashBytesField(h, s.Attestation)
}

// sortedCopy returns a deterministic ordering by path.
//
// Files in the same manifest with the same path are not allowed (caller
// should validate); when present here, the ordering is still
// deterministic by virtue of the stable sort, so the hash remains
// reproducible — but the manifest itself is malformed and will be
// rejected at registration time.
func sortedCopy(files []FileEntry) []FileEntry {
	out := make([]FileEntry, len(files))
	copy(out, files)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

// Length-prefixed field writers. Each writes a 4-byte big-endian length
// followed by the raw bytes. Strings are written as their UTF-8 bytes.

func hashStringField(h interface {
	Write([]byte) (int, error)
}, s string) {
	hashBytesField(h, []byte(s))
}

func hashBytesField(h interface {
	Write([]byte) (int, error)
}, b []byte) {
	writeBigEndianUint32(h, uint32(len(b)))
	_, _ = h.Write(b)
}

func hashIntField(h interface {
	Write([]byte) (int, error)
}, n int) {
	writeBigEndianUint32(h, uint32(n))
}

func hashUint32Field(h interface {
	Write([]byte) (int, error)
}, n uint32) {
	writeBigEndianUint32(h, n)
}

func hashBoolField(h interface {
	Write([]byte) (int, error)
}, b bool) {
	flag := byte(0)
	if b {
		flag = 1
	}
	_, _ = h.Write([]byte{flag})
}

func writeBigEndianUint32(h interface {
	Write([]byte) (int, error)
}, n uint32) {
	buf := []byte{
		byte(n >> 24),
		byte(n >> 16),
		byte(n >> 8),
		byte(n),
	}
	_, _ = h.Write(buf)
}
