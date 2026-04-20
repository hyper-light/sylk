// Package blob is the substrate's content-addressed blob store.
//
// A Blob is an immutable sequence of bytes keyed by its BLAKE3 hash. The
// store deduplicates identical content across all packages, manifests, and
// pipelines: the same bytes produce the same Hash and live as a single
// entry, refcounted by callers.
//
// BLAKE3 is chosen over SHA-256 for substrate-internal hashing because it
// is roughly 5× faster on commodity CPUs and the SLSA provenance ecosystem
// is moving toward it. External hashes published by registries (PyPI's
// wheel SHA-256, npm's tarball SHA-512, Go's h1: prefix) are stored
// alongside in manifest source metadata for supply-chain verification but
// are not used for substrate-internal dedup.
package blob

import (
	"encoding/hex"
	"errors"
	"fmt"

	"lukechampine.com/blake3"
)

// HashSize is the byte length of a blob hash. BLAKE3's default output
// width matches SHA-256 at 32 bytes, which keeps wire formats and storage
// records the same width as legacy SHA-256 hashes elsewhere in the tree.
const HashSize = 32

// Hash is the 32-byte BLAKE3 digest that names a blob.
//
// Hashes are values, not pointers: comparable, copyable, safe to use as
// map keys without aliasing concerns.
type Hash [HashSize]byte

// Sum returns the BLAKE3 hash of the given bytes. The returned Hash
// uniquely names the bytes for the substrate's deduplication purposes.
func Sum(data []byte) Hash {
	return blake3.Sum256(data)
}

// String returns the lowercase hex encoding of the hash.
//
// Hex is chosen over base64 to match the textual conventions of the
// surrounding codebase (Git, Sigstore, PyPI all surface hex digests).
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// ErrInvalidHashLength is returned when ParseHash receives input that
// does not decode to exactly HashSize bytes.
var ErrInvalidHashLength = errors.New("blob: hex string does not decode to a HashSize-byte digest")

// ParseHash decodes a hex-encoded hash string. It is the inverse of
// Hash.String.
func ParseHash(s string) (Hash, error) {
	var h Hash
	decoded, err := hex.DecodeString(s)
	if err != nil {
		return h, fmt.Errorf("blob: parse hash: %w", err)
	}
	if len(decoded) != HashSize {
		return h, ErrInvalidHashLength
	}
	copy(h[:], decoded)
	return h, nil
}

// IsZero reports whether h is the zero Hash. The zero Hash is reserved
// as a sentinel meaning "no hash"; it can never collide with a real
// BLAKE3 digest of any input (the BLAKE3 of the empty string is not
// zero — verifiable by inspection).
func (h Hash) IsZero() bool {
	return h == Hash{}
}
