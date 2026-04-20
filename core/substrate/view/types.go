// Package view composes substrate manifests into a per-pipeline View
// — the integration unit between the substrate's content-addressed
// storage and the agent's spawned process environment.
//
// A View is the answer to the question "given this pipeline's
// resolved set of pinned manifests, what does the agent's process
// actually see at runtime?" It composes:
//
//  1. A read-only substrate projection (via projection.Namespace)
//  2. A 5-layer environment stack (PATH and friends; library paths;
//     per-language env vars; canonical-path mount plan; auxiliary
//     state directories)
//  3. A platform-specific Layer 4 mount plan that the execution backend
//     applies to make hardcoded canonical paths (e.g. /usr/bin/python3
//     shebangs) resolve through substrate content
//
// The View is content-addressable: identical inputs (resolved manifest
// set, mount root, ecosystem contributions, platform tuple) produce
// identical View IDs. Re-spawning into the same View skips composition.
package view

import (
	"github.com/adalundhe/sylk/core/substrate/blob"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// IDSize matches blob.HashSize so View IDs share the same width
// convention as blobs, manifests, and recipes.
const IDSize = blob.HashSize

// ID names a View by content. Equal IDs guarantee equal load-bearing
// composition (manifests, mount paths, env exports, layer 4 plan).
type ID [IDSize]byte

// String returns the lowercase hex encoding.
func (i ID) String() string { return blob.Hash(i).String() }

// IsZero reports whether i is the zero ID.
func (i ID) IsZero() bool { return i == ID{} }

// View is one composed per-pipeline projection of substrate manifests.
type View struct {
	// ID is the content hash of the View's load-bearing fields.
	ID ID

	// PipelineID is the owning pipeline. Per-pipeline scoping means
	// the View's lifecycle is tied to the pipeline's, and its mount
	// root is per-pipeline by default.
	PipelineID string

	// MountRoot is the substrate-relative path the View is rooted at,
	// e.g. "/sylk/views/pipe-7". The execution backend mounts the View
	// at this path so the spawned process sees it as a real directory.
	MountRoot string

	// Layout is the composed (path → blob) map across all attached
	// manifests, with conflicts already resolved at composition time.
	Layout Layout

	// Env is the composed 5-layer environment stack the execution
	// backend exports into the spawned process's environment.
	Env Environment

	// Layer4 is the platform-specific mount plan that exposes substrate
	// content at canonical host paths (e.g. /usr, /opt, /etc on Linux)
	// for tools that hardcode absolute paths.
	Layer4 Layer4Plan

	// Platform records the host platform tuple this View was composed
	// for. Re-spawning on a different platform requires re-composition.
	Platform recipe.Platform
}

// ManifestSpec is one input to the composer: a manifest plus its
// position and ecosystem semantics within the View.
type ManifestSpec struct {
	// Manifest to attach.
	Manifest *manifest.Manifest

	// MountPath is the View-relative directory this manifest occupies.
	// E.g. "/python-3.13", "/python-3.13/site-packages/pytest", "/bin",
	// "/lib/x86_64-linux-gnu".
	MountPath string

	// Ecosystems names the env-var contribution sets this manifest
	// participates in. A pytest manifest would typically contribute to
	// "python" (PYTHONPATH) and "binary" (PATH for its executable).
	// Looked up against the substrate's ecosystem catalog.
	Ecosystems []string
}

// ComposeRequest is the input to Compose.
type ComposeRequest struct {
	// PipelineID owns the resulting View.
	PipelineID string

	// MountRoot is where the View will be rooted. Defaults to
	// "/sylk/views/{pipeline_id}" if empty.
	MountRoot string

	// Manifests is the closure of manifests to attach, in
	// dependency-topological order (dependencies first). The composer
	// processes them in this order so conflict detection flags the
	// right offender (the second one to claim a path is rejected).
	Manifests []ManifestSpec

	// Platform is the host platform tuple. Required.
	Platform recipe.Platform

	// EcosystemCatalog optionally overrides the default ecosystem
	// → env-contribution mapping. Empty uses DefaultEcosystemCatalog.
	EcosystemCatalog Catalog
}

// Layout is the composed file layout of the View — the union of all
// attached manifests' file entries, keyed by View-relative path.
type Layout struct {
	// Manifests is the set of attached manifest IDs in deterministic
	// order. Used to compute the View ID.
	Manifests []manifest.ID

	// Paths maps View-relative absolute paths to their resolved
	// (blob, mode, source) tuple. Empty when the View has no
	// attachments (e.g. for a pipeline that hasn't provisioned any
	// substrate tools yet).
	Paths map[string]ResolvedPath
}

// ResolvedPath is one composed entry in the View's layout.
type ResolvedPath struct {
	BlobHash       blob.Hash
	Mode           uint32
	Symlink        bool
	SourceManifest manifest.ID
}

// Environment is the 5-layer environment stack the View exports.
//
// Each EnvVar field carries the substrate-side prepend value. The
// execution backend composes these with the host's existing env
// (substrate first, host second) so substrate-supplied tools shadow
// host versions where they exist, and host tools remain visible where
// substrate has nothing to offer.
type Environment struct {
	// Layer 1 — PATH and friends.
	PATH            []string
	MANPATH         []string
	PKGConfigPath   []string
	AclocalPath     []string
	InfoPath        []string

	// Layer 2 — library lookup paths.
	LDLibraryPath     []string // Linux
	DYLDLibraryPath   []string // macOS
	DYLDFrameworkPath []string // macOS

	// Layer 3 — per-language env vars. Map key is env var name,
	// value is the prepend list. Generic so adding a new ecosystem is
	// "add an entry to a recipe's metadata" not "modify substrate code".
	LanguagePaths map[string][]string

	// Layer 5 — auxiliary state directories. Map key is env var name
	// (HOME, XDG_CONFIG_HOME, TMPDIR, etc.), value is the View-relative
	// directory path. Single-value (not a list) because these are
	// roots, not search paths.
	AuxiliaryDirs map[string]string
}

// Layer4Plan is the per-platform mount plan that exposes substrate
// content at canonical host paths.
//
// Only one of the per-platform fields is populated — the one matching
// View.Platform.OS. The execution backend's platform-specific code
// applies the matching plan.
type Layer4Plan struct {
	Linux   *LinuxLayer4
	Darwin  *DarwinLayer4
	Windows *WindowsLayer4
}

// LinuxLayer4 describes the bind-mount plan inside a Linux mount
// namespace. The execution backend applies these as ro-bind mounts
// (overlays canonical paths with View content; only visible inside
// the spawned process subtree).
type LinuxLayer4 struct {
	BindMounts []BindMount
}

// DarwinLayer4 captures the macOS-specific projection plan: shebang
// rewrites done at substrate-extract time and DYLD interposer entries.
type DarwinLayer4 struct {
	// InterposedPaths lists canonical host paths the substrate's DYLD
	// interposer should redirect to View content.
	InterposedPaths []DarwinInterposition
}

// DarwinInterposition is one (canonical_path → view_path) redirect the
// macOS DYLD interposer dylib applies on open()/stat() syscalls.
type DarwinInterposition struct {
	CanonicalPath string
	ViewPath      string
}

// WindowsLayer4 captures the per-process drive mappings and KnownFolder
// redirections the substrate uses on Windows.
type WindowsLayer4 struct {
	// DriveMappings lists per-process drive-letter mappings to apply
	// via DefineDosDevice with DDD_NO_BROADCAST_SYSTEM.
	DriveMappings []WindowsDriveMapping
}

// WindowsDriveMapping is one (drive_letter → target_path) per-process
// mapping.
type WindowsDriveMapping struct {
	DriveLetter rune   // e.g. 'S'
	TargetPath  string // e.g. \\?\sylk-view-pipe-7
}

// BindMount describes one mount-namespace bind operation.
type BindMount struct {
	// Source is the View-relative path inside the substrate.
	Source string

	// Target is the canonical host path the source overlays
	// (e.g. /usr, /opt).
	Target string

	// ReadOnly: if true the mount is RO; the substrate's substrate
	// content is always RO so this is virtually always true.
	ReadOnly bool
}
