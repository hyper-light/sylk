// Package manifest is the substrate's typed bundle store.
//
// A Manifest names a content-addressed bundle of files — pytest 8.3.2's
// complete file list, or the Rust toolchain 1.83.0's complete file list,
// or zlib 1.3.1's complete build output. Manifests are tiny (KB-scale,
// even for huge packages — they are just hash lists pointing into the
// blob store).
//
// Manifests are themselves content-addressed by ManifestID, the BLAKE3
// hash of the manifest's canonicalized contents. Two builds of the same
// recipe with the same inputs produce the same ManifestID, which is what
// makes deterministic substrate caching meaningful.
package manifest

import (
	"time"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

// IDSize matches blob.HashSize for symmetry; both are 32-byte BLAKE3
// digests over canonicalized inputs.
const IDSize = blob.HashSize

// ID names a manifest by content. Equal IDs guarantee equal manifests.
type ID [IDSize]byte

// String returns the lowercase hex encoding.
func (i ID) String() string { return blob.Hash(i).String() }

// IsZero reports whether i is the zero ID (sentinel meaning "no manifest").
func (i ID) IsZero() bool { return i == ID{} }

// PackageID is the human-friendly handle for a manifest.
//
// (ecosystem, name, version) is the conventional package coordinate
// across every ecosystem the substrate supports. The substrate does not
// interpret the values — it just keys lookups on them — so any
// well-formed string triple is acceptable.
type PackageID struct {
	Ecosystem string
	Name      string
	Version   string
}

// String returns "ecosystem/name@version" for diagnostics.
func (p PackageID) String() string {
	return p.Ecosystem + "/" + p.Name + "@" + p.Version
}

// Mode is a unix mode bit pattern. The substrate stores mode bits per
// file so the FUSE projection can faithfully reproduce executable bits,
// readable bits, and (via projection synthesis) Windows ACL equivalents.
//
// Setuid/setgid bits are refused at provisioning time — they are
// security-relevant in ways the sandbox cannot fully model and have no
// legitimate substrate use case.
type Mode uint32

// FileEntry maps one logical path inside the manifest to its content.
type FileEntry struct {
	// Path is the substrate-relative POSIX path the file occupies in
	// the manifest's namespace, e.g. "/bin/pytest" or
	// "/python/site-packages/pytest/__init__.py". Always uses forward
	// slashes; the projection translates per platform.
	Path string

	// BlobHash names the file's content in the blob store. Symlinks
	// store their target as the blob content; the projection
	// synthesizes the symlink at FUSE time.
	BlobHash blob.Hash

	// Mode is the file's unix mode bits.
	Mode Mode

	// Symlink is true when this entry represents a symbolic link
	// rather than a regular file. The blob's content is the symlink
	// target string in that case.
	Symlink bool
}

// SourceMetadata records where a manifest's content originally came
// from — used for supply-chain auditing, lockfile pinning, and
// reproducible re-fetching when blobs are evicted from the cache.
type SourceMetadata struct {
	// RecipeID is the content hash of the recipe that produced this
	// manifest. Empty for manifests built outside the recipe runtime
	// (e.g. test fixtures).
	RecipeID string

	// FetchURL is the upstream URL bytes were originally fetched from.
	FetchURL string

	// FetchHash is the substrate-internal BLAKE3 of the fetched archive
	// before extraction. Lets us re-verify on re-fetch.
	FetchHash blob.Hash

	// EcosystemHash is the ecosystem-native hash the registry
	// publishes (PyPI's wheel SHA-256, npm's tarball shasum, Go's
	// h1: prefix, etc.). Stored so supply-chain verification can
	// match what the registry advertises.
	EcosystemHash string

	// EcosystemHashAlgorithm names the algorithm of EcosystemHash
	// (e.g. "sha256", "sha512", "blake3") so verifiers can choose
	// the correct primitive.
	EcosystemHashAlgorithm string

	// Attestation is the signed provenance bundle (PEP 740, Sigstore,
	// SLSA), opaque to the substrate. Empty when no attestation chain
	// is available.
	Attestation []byte
}

// Metadata holds informational fields. Not load-bearing — never used
// for identity, lookup, or correctness — but valuable for diagnostics
// and license audit reporting.
type Metadata struct {
	License     string // SPDX expression, free-form
	Ecosystem   string // e.g. "python", "rust", "system-binary"
	Homepage    string
	Description string
	Tags        []string
}

// Manifest is a content-addressed bundle of files describing one
// installable resource.
//
// Manifests are immutable once registered. The ID field is computed
// from the canonical encoding of (Package, sorted Files, Source); two
// identically-formed manifests have the same ID.
type Manifest struct {
	ID        ID
	Package   PackageID
	Files     []FileEntry
	Source    SourceMetadata
	Metadata  Metadata
	CreatedAt time.Time
}
