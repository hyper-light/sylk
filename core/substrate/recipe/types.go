// Package recipe defines the substrate's universal installation
// declaration format.
//
// A Recipe is the substrate's atomic input — a structured description
// of how to materialize one resource (a tool, library, runtime,
// language toolchain, language server, formatter, custom binary, system
// shared library, font, model checkpoint — any content-addressable
// artifact) into the substrate.
//
// Recipes are language-agnostic by design. The substrate runtime knows
// how to execute recipes; recipe authors describe ecosystems. Adding a
// new ecosystem is a matter of writing recipes, not changing substrate
// code.
//
// Recipes are themselves content-addressable: the RecipeID is the
// BLAKE3 hash of the recipe's canonical encoding, computed via
// ComputeID. Two recipes with the same load-bearing fields produce the
// same RecipeID, regardless of cosmetic metadata differences.
package recipe

import (
	"time"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

// IDSize matches blob.HashSize — recipes are hashed with the same
// substrate-internal primitive (BLAKE3-256) as blobs and manifests, so
// addressing widths are uniform.
const IDSize = blob.HashSize

// ID names a recipe by content. Equal IDs guarantee equal load-bearing
// recipe contents.
type ID [IDSize]byte

// String returns the lowercase hex encoding.
func (i ID) String() string { return blob.Hash(i).String() }

// IsZero reports whether i is the zero ID (sentinel meaning "no recipe").
func (i ID) IsZero() bool { return i == ID{} }

// SchemaVersion is the recipe schema this package implements. Recipes
// declare their schema version; the runtime uses it to dispatch to the
// correct decoder. Forward compatibility is achieved by adding
// per-version decoders, never by mutating older schemas.
const SchemaVersion = 1

// Recipe is the universal installation declaration.
type Recipe struct {
	// SchemaVersion identifies the recipe format. Currently 1.
	SchemaVersion int

	// ID is the content hash of the recipe. Computed via ComputeID;
	// callers should not set this directly — Register and the recipe
	// loader populate it.
	ID ID

	// Fetch declares one or more byte sources. The runtime tries each
	// in order, picking the first whose PlatformPin matches the host
	// (or the first FetchSpec with no PlatformPin if none match the
	// pinned ones). A successful fetch produces the bytes that flow
	// through Verify and Extract.
	Fetch []FetchSpec

	// Verify is the integrity policy applied to the fetched bytes
	// before extraction. Hash mismatches are fatal — the substrate
	// refuses to register manifests built from unverified content.
	Verify VerifySpec

	// Extract describes how to interpret the fetched bytes as files.
	// For pure data artifacts (a single binary, a single font), use
	// ExtractFormatRaw. For archives, use the matching format hint.
	Extract ExtractSpec

	// Requires lists other recipes whose outputs must be substrate-
	// mounted before this recipe's Build steps run. Empty for recipes
	// that are pure data extraction (no Build).
	//
	// The dependency graph across all recipes in a closure must be
	// acyclic; the runtime detects cycles and refuses to provision.
	Requires []ID

	// Build is an optional sequence of commands to run under the
	// substrate's hermetic sandbox after extraction, before output
	// capture. Empty Build means "extract bytes are the output" (pure
	// data recipe).
	Build []BuildStep

	// Determinism configures the environment under which Build steps
	// run, so substrate output hashes are reproducible across hosts.
	// Ignored for pure data recipes.
	Determinism DeterminismProfile

	// Output maps paths in the build tree (or extracted archive) to
	// the substrate paths the resulting manifest will claim. For pure
	// data recipes with a single OutputMapping{SubstratePath: "/",
	// SourcePath: "/"}, the entire extracted tree is captured.
	Output []OutputMapping

	// Metadata is informational. Excluded from ComputeID.
	Metadata RecipeMetadata

	// CreatedAt is set when the recipe is loaded; not part of the ID.
	// Used for diagnostics only.
	CreatedAt time.Time
}

// FetchSpec declares one source of bytes for the recipe.
type FetchSpec struct {
	// Source is the URL the runtime fetches from. Supported schemes are
	// determined by the registered fetchers. {os}, {arch}, {libc} are
	// substituted at fetch time from the host platform tuple.
	Source string

	// Hash is the expected hash of the fetched bytes. The substrate
	// computes the hash on fetch and refuses to proceed on mismatch.
	Hash Hash

	// Headers are additional HTTP headers (for HTTPS sources). Useful
	// for auth-protected mirrors. Values may reference environment
	// variables via ${VAR} substitution at fetch time.
	Headers map[string]string

	// PlatformPin restricts this FetchSpec to a matching host platform.
	// Nil PlatformPin means "any platform". When multiple FetchSpecs
	// have PlatformPin set, the runtime picks the first matching one.
	PlatformPin *Platform

	// Signature is an optional cryptographic signature attached to the
	// source URL (PEP 740 attestation, Sigstore bundle, GPG signature).
	// Verifier policy in VerifySpec decides whether signature presence
	// is required or optional.
	Signature *Signature
}

// Hash names a digest by algorithm + raw bytes.
type Hash struct {
	// Algorithm is one of "sha256", "sha512", "blake3". Lowercase.
	Algorithm string

	// Digest is the raw bytes of the hash. Length must match the
	// algorithm's output size; verifier rejects mismatched widths.
	Digest []byte
}

// IsEmpty reports whether the hash is unset.
func (h Hash) IsEmpty() bool {
	return h.Algorithm == "" && len(h.Digest) == 0
}

// Signature is an opaque signature payload paired with a format hint.
type Signature struct {
	// Format identifies the signature scheme: "pep740", "sigstore",
	// "gpg-detached", "minisign". The verifier dispatches on this.
	Format string

	// Payload is the raw signature bytes (or signature bundle bytes).
	Payload []byte
}

// VerifySpec configures how fetched bytes are validated before
// extraction.
type VerifySpec struct {
	// HashAlgorithm names the primary digest the verifier computes.
	// Defaults to "blake3" if empty.
	HashAlgorithm string

	// PolicyClass controls verification strictness:
	//   strict     — hash and any present signature must verify
	//   advisory   — verifier reports failures but does not block
	//   permissive — verifier checks hash only; signatures are skipped
	// Defaults to strict.
	PolicyClass VerifyPolicy
}

// VerifyPolicy enumerates the verification strictness levels.
type VerifyPolicy string

const (
	VerifyStrict     VerifyPolicy = "strict"
	VerifyAdvisory   VerifyPolicy = "advisory"
	VerifyPermissive VerifyPolicy = "permissive"
)

// ExtractSpec describes how to interpret fetched bytes as files.
type ExtractSpec struct {
	// Format identifies the archive format. See ExtractFormat constants.
	Format ExtractFormat

	// StripPrefix removes leading path components from extracted paths.
	// E.g. "rust-1.83.0/" turns "rust-1.83.0/bin/rustc" into "bin/rustc".
	// Empty disables stripping.
	StripPrefix string

	// PreserveMode honors mode bits from the archive. When false, all
	// files are extracted with mode 0644 (regular) or 0755 (executable
	// detected via filename heuristics or shebang).
	PreserveMode bool

	// Filter optionally restricts which entries are extracted.
	Filter *Filter
}

// ExtractFormat enumerates supported archive formats.
type ExtractFormat string

const (
	ExtractFormatTar     ExtractFormat = "tar"      // pure tar (no compression)
	ExtractFormatTarGz   ExtractFormat = "tar.gz"
	ExtractFormatTarXz   ExtractFormat = "tar.xz"
	ExtractFormatTarZst  ExtractFormat = "tar.zst"
	ExtractFormatZip     ExtractFormat = "zip"
	ExtractFormatRaw     ExtractFormat = "raw"      // single file; no archive parsing
	ExtractFormatGitTree ExtractFormat = "git-tree"
	ExtractFormatOCI     ExtractFormat = "oci-layer"
)

// Filter restricts extraction to a subset of archive entries.
type Filter struct {
	// Include is a list of glob patterns; an entry is included if it
	// matches any. Empty Include means "include all".
	Include []string

	// Exclude is a list of glob patterns; an entry is excluded if it
	// matches any. Excludes apply after Includes.
	Exclude []string
}

// BuildStep declares one command to run under the hermetic sandbox.
type BuildStep struct {
	// Cmd is the argv for the command. argv[0] is the binary; relative
	// names resolve against the sandbox PATH.
	Cmd []string

	// Env is additional environment variables. Composed with the base
	// env supplied by the determinism profile.
	Env map[string]string

	// Cwd is the working directory inside the writable build
	// projection. Defaults to "/sylk/build".
	Cwd string

	// Timeout caps wall-clock time for this step. Zero means "use the
	// runtime default".
	Timeout time.Duration

	// SandboxOverride lets a single step adjust capabilities (e.g.
	// allow a downloader-style first step that needs network — useful
	// for resolvers that do their own dependency download). Use
	// sparingly and document why.
	SandboxOverride *SandboxStepOverride
}

// SandboxStepOverride is a placeholder for per-step sandbox adjustments.
// Phase 2.5 wires the sandbox; this type pre-declares the field so the
// recipe schema is forward-compatible.
type SandboxStepOverride struct {
	// AllowNetwork enables network access for this step only.
	AllowNetwork bool
}

// DeterminismProfile clamps the build environment so output bytes are
// reproducible across hosts.
type DeterminismProfile struct {
	// SourceDateEpoch is the value of the SOURCE_DATE_EPOCH env var,
	// which most modern compilers and archivers respect. Default 0
	// (Unix epoch), the convention used by Reproducible Builds.
	SourceDateEpoch int64

	// Locale clamps LC_ALL. Default "C".
	Locale string

	// Hostname is what gethostname() returns inside the sandbox.
	// Default "substrate".
	Hostname string

	// User is what whoami / getlogin / $USER return. Default "builder".
	User string

	// Home is what $HOME points to. Default "/sylk/home".
	Home string

	// MtimePolicy controls how the writable FUSE projection assigns
	// mtime/ctime/atime to files written during the build:
	//   "clamp"    — set all to SourceDateEpoch
	//   "preserve" — pass through (non-deterministic; only for debugging)
	// Default "clamp".
	MtimePolicy MtimePolicy

	// CompilerFlags are appended to commonly-respected env vars
	// (CFLAGS, CXXFLAGS, RUSTFLAGS) when set, so the determinism
	// profile can inject -frandom-seed and -fdebug-prefix-map without
	// requiring every recipe to repeat them.
	CompilerFlags []string

	// LinkerFlags is the equivalent for LDFLAGS / RUSTFLAGS link args.
	LinkerFlags []string

	// EnvScrub names env vars to remove entirely from the sandbox env.
	// Use for ambient host pollution that would otherwise affect the
	// build (e.g. PYTHONSTARTUP, RUBYOPT).
	EnvScrub []string

	// EnvAdd are additional env clamps composed after the defaults.
	EnvAdd map[string]string
}

// MtimePolicy enumerates the file timestamp policies for build outputs.
type MtimePolicy string

const (
	MtimeClamp    MtimePolicy = "clamp"
	MtimePreserve MtimePolicy = "preserve"
)

// OutputMapping declares one substrate-relative path the manifest will
// claim, paired with the path inside the build tree (or extracted
// archive) to capture from.
type OutputMapping struct {
	// SubstratePath is the canonical path inside the manifest.
	// Always POSIX-style; the View projects it onto the host's path
	// conventions per platform.
	SubstratePath string

	// SourcePath is the path inside the build tree (after extraction
	// and any Build steps) to capture from. May be a directory or a
	// single file. Trailing slash on both SubstratePath and SourcePath
	// indicates "recursive directory copy"; otherwise a single file.
	SourcePath string

	// Mode is the unix mode bits assigned to the captured files.
	// PreserveMode (the zero value of OutputMode means "preserve from
	// source") respects the source's mode.
	Mode OutputMode

	// Symlink: when true, capture as a symbolic link rather than a
	// regular file. SourcePath is the symlink target string.
	Symlink bool
}

// OutputMode is the mode policy for an output mapping.
//
// The zero value (PreserveMode) tells the output capture to honor the
// source's mode bits. Any other value (a unix mode bit pattern) is
// applied verbatim, ignoring the source.
type OutputMode uint32

// PreserveMode is the sentinel "honor source" mode.
const PreserveMode OutputMode = 0

// IsPreserve reports whether m is the PreserveMode sentinel.
func (m OutputMode) IsPreserve() bool { return m == PreserveMode }

// RecipeMetadata holds informational fields. Excluded from ComputeID
// because cosmetic differences in license string or description should
// not affect a recipe's identity.
type RecipeMetadata struct {
	Name        string   // human-friendly name, e.g. "ripgrep"
	Version     string   // human-friendly version, e.g. "14.1.0"
	Ecosystem   string   // e.g. "rust", "python", "system-binary"
	License     string   // SPDX expression
	Homepage    string
	Description string
	Tags        []string
}

// Platform is the (os, arch, libc) triple identifying a host or target.
type Platform struct {
	// OS: "linux", "darwin", "windows", etc.
	OS string

	// Arch: "amd64", "arm64", "riscv64", etc.
	Arch string

	// Libc: "glibc", "musl" (linux); "system" (darwin); "msvc" (windows).
	Libc string
}

// IsZero reports whether the platform is fully unset.
func (p Platform) IsZero() bool {
	return p.OS == "" && p.Arch == "" && p.Libc == ""
}

// Matches reports whether p satisfies pin. A pin field that is empty
// matches any value; a non-empty pin field must equal the host's value.
func (p Platform) Matches(pin Platform) bool {
	return matchesField(p.OS, pin.OS) &&
		matchesField(p.Arch, pin.Arch) &&
		matchesField(p.Libc, pin.Libc)
}

func matchesField(host, pin string) bool {
	return pin == "" || host == pin
}

// String returns the conventional "os/arch/libc" form, e.g. "linux/amd64/glibc".
func (p Platform) String() string {
	return p.OS + "/" + p.Arch + "/" + p.Libc
}
