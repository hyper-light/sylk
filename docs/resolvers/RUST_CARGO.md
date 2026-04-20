# RUST_CARGO.md — Rust / Cargo Adapter Implementation Plan

Tier 1 — validates that the substrate works cleanly for ecosystems
whose upstream protocol is already well-designed. Cargo's sparse
index is the "easy case" — per-version metadata as separate cheap
HTTP resources — so this adapter should be structurally smaller than
Python's and validate that the substrate doesn't impose unnecessary
complexity on well-behaved ecosystems.

## 1. Overview

The Rust adapter resolves and materializes crates from:

- **crates.io** via the [sparse index protocol](https://doc.rust-lang.org/cargo/reference/registry-index.html#sparse-index-format)
  (stable since 2023, default since Cargo 1.70)
- **Private registries** (Artifactory, CloudSmith, corporate
  Artifactory-hosted crates.io mirrors, sparse-index compatible)
- **Git dependencies** (`git = "https://..."` in Cargo.toml)
- **Path dependencies** (local crates in the same workspace or
  outside)

Produces:

- A resolved dependency tree satisfying Cargo's semver ranges and
  feature unification rules
- A `Cargo.lock` pinning every resolved crate
- A materialized `target/` (via a substrate-managed download cache;
  `cargo build` is *not* orchestrated by the adapter — the adapter
  ends at "crates are on disk with correct metadata")

User-visible behaviors (M3 target):

- `sylk resolve rust ./Cargo.toml` → updates `Cargo.lock`
- `sylk install rust` → populates `~/.cargo/registry` equivalent
  under substrate control, makes crates available for `cargo build`
- `sylk add rust <crate> [--version <range>] [--features <list>]` →
  modifies `Cargo.toml`, re-resolves, updates lockfile
- `sylk upgrade rust` → re-resolves ignoring lockfile, updates
  lockfile
- `sylk why rust <crate>` → PubGrub-driven explanation

Non-goals:

- Building crates (that's `cargo build`, not our adapter)
- Running `build.rs` scripts (the adapter only handles source
  distribution; build execution happens outside)

## 2. Data Model

### 2.1 Ecosystem coordinates

```go
type CargoCoordinate struct {
    Name     string            // crate name, per [a-zA-Z0-9_-]{1,64}
    Version  SemVer            // strict SemVer 2.0
    Features []string          // requested features
    Source   CargoSource       // registry, git, or path
}

type CargoSource struct {
    Kind     CargoSourceKind  // Registry, Git, Path
    Registry string           // registry URL when Kind=Registry
    Git      GitReference     // when Kind=Git
    Path     string           // when Kind=Path
}

type GitReference struct {
    URL       string
    Revision  string   // resolved commit SHA
    Ref       string   // user-requested: branch, tag, or commit
    Kind      string   // "branch", "tag", "rev", or "default"
    Submodules bool    // honor .gitmodules
}
```

### 2.2 Cargo.toml parsing

```go
type CargoManifest struct {
    Package      *CargoPackage
    Dependencies map[string]CargoDepSpec       // [dependencies]
    DevDeps      map[string]CargoDepSpec       // [dev-dependencies]
    BuildDeps    map[string]CargoDepSpec       // [build-dependencies]
    Target       map[string]CargoTargetDeps    // [target."cfg(unix)".dependencies]
    Features     map[string][]string           // [features]
    Workspace    *CargoWorkspace
    Patch        map[string]map[string]CargoDepSpec // [patch] overrides
    Replace      map[string]CargoDepSpec       // deprecated but still supported
}

type CargoDepSpec struct {
    Version         VersionRange   // may be empty if Git/Path only
    Path            string
    Git             string
    Branch, Tag, Rev string
    Features        []string
    DefaultFeatures bool            // default true
    Optional        bool
    Package         string          // rename: "foo" → depends on "bar" package
    Workspace       bool            // inherit from workspace table
}

type CargoWorkspace struct {
    Members         []string        // glob patterns
    Exclude         []string
    DefaultMembers  []string
    Resolver        string          // "1" or "2" (feature-unification semantics)
    Dependencies    map[string]CargoDepSpec   // workspace-wide shared deps
    Package         *CargoPackage   // workspace-wide metadata
}
```

### 2.3 Cargo.lock

Cargo's lockfile is TOML with a specific structure. The substrate's
LockfileCodec produces `Cargo.lock`-compatible output:

```toml
version = 3

[[package]]
name = "serde"
version = "1.0.193"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "..."
dependencies = [
    "serde_derive",
]

[[package]]
name = "serde_derive"
version = "1.0.193"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "..."
dependencies = [
    "proc-macro2",
    "quote",
    "syn",
]
```

Lockfile version 3 is current; versions 1 and 2 are legacy formats
the codec must read for backward compatibility but never emit.

### 2.4 Feature unification

Cargo's resolver unifies features across consumers. The `resolver = "2"`
(default for edition 2021+) separates features per context:

- `[dependencies]` and `[build-dependencies]` of the same crate don't
  share features
- `[target."cfg(x)".dependencies]` features apply only when the
  target's cfg matches

`resolver = "1"` unified features globally and had subtle correctness
issues with build-dependencies — the adapter must support both but
default to v2 for new workspaces.

## 3. HTTP Transport

### 3.1 Sparse index protocol

The sparse index is a static HTTP tree. Per-crate metadata lives at
a URL derived from the crate name:

```
# Crate name "serde":
https://index.crates.io/se/rd/serde

# Crate name "x" (short names get special paths):
https://index.crates.io/1/x

# Crate name "ab" (two-char names):
https://index.crates.io/2/ab

# Crate name "abc" (three-char names):
https://index.crates.io/3/a/abc
```

The response body is a JSON-Lines file — one line per published
version of the crate:

```json
{"name":"serde","vers":"1.0.193","deps":[...],"cksum":"...","features":{"default":["std"],"alloc":[],"std":[],"derive":["serde_derive"]},"yanked":false,"links":null}
{"name":"serde","vers":"1.0.194","deps":[...],"cksum":"...","features":{...},"yanked":false,"links":null}
```

Each line is a complete crate-version record. The full file is
append-only — old versions never change, new versions are appended.
This makes it trivially incrementally updatable via `If-Modified-Since`
or `Etag`.

### 3.2 Fetch strategy

```go
// FetchCrateIndex retrieves a crate's JSON-Lines metadata file.
// Uses Etag / Last-Modified for conditional fetches — on cache hit
// the server returns 304 and we use the cached content.
func (c *cargoAdapter) FetchCrateIndex(ctx context.Context, crateName string) (*CrateIndex, error)

type CrateIndex struct {
    Name     string
    Versions []CrateVersion
    Etag     string
    FetchedAt time.Time
}

type CrateVersion struct {
    Name       string
    Vers       SemVer
    Deps       []CrateIndexDep
    Cksum      string    // SHA-256 of the .crate tarball
    Features   map[string][]string
    Yanked     bool
    Links      string    // optional: links= metadata for native deps
    RustVersion string   // minimum rustc version; optional
}
```

### 3.3 .crate file download

Source tarballs live at predictable URLs:

```
https://static.crates.io/crates/{crate}/{crate}-{version}.crate
```

These are gzipped tar archives (`crate` extension is Cargo's
convention; the file is actually `.tar.gz`). Fetched only at
materialization time, never during resolution.

### 3.4 Authentication

crates.io itself doesn't require auth for read access. Private
registries do:

- **Artifactory / CloudSmith** — HTTP Basic auth or API tokens
- **Corporate GitHub-hosted mirrors** — GitHub Personal Access
  Tokens
- **`cargo login`-managed tokens** — stored in `~/.cargo/credentials.toml`

The adapter reads `~/.cargo/credentials.toml` to source tokens for
named registries. The `CARGO_REGISTRIES_<NAME>_TOKEN` env var is
also honored per Cargo's convention.

## 4. Metadata Layer

### 4.1 JSON-Lines parser

Streaming parser — each line is valid JSON, parse incrementally to
avoid buffering the entire file for big crates (some crates have
thousands of versions):

```go
func ParseSparseIndexFile(r io.Reader) iter.Seq2[CrateVersion, error] {
    return func(yield func(CrateVersion, error) bool) {
        scanner := bufio.NewScanner(r)
        scanner.Buffer(make([]byte, 64*1024), 1024*1024) // handle large lines
        for scanner.Scan() {
            var v CrateVersion
            if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
                if !yield(v, err) { return }
                continue
            }
            if !yield(v, nil) { return }
        }
    }
}
```

Use Go's native `encoding/json` (not fastjson) because per-line
documents are small and the dominant cost is network, not parsing.
Streaming is important: a crate like `windows-sys` has ~500 versions
and we want to filter yanked/incompatible as they arrive, not after
buffering.

### 4.2 Feature expansion

Feature declaration in the index:

```json
"features": {
    "default": ["std", "derive"],
    "std": [],
    "alloc": [],
    "derive": ["serde_derive"],
    "serde_derive": ["dep:serde_derive"]
}
```

Dependencies declared in `deps` with optional flag:

```json
{"name":"serde_derive","req":"=1.0.193","features":[],"optional":true,"default_features":true,"target":null,"kind":"normal","package":null}
```

Feature activation semantics (Cargo's rules):

1. A consumer requesting feature `X` on crate `C` activates every
   feature in `C.features["X"]` transitively
2. `dep:X` syntax activates `X` as an optional dependency
3. `X/Y` syntax activates feature `Y` on dependency `X`
4. `X?/Y` activates `Y` on `X` *only if* `X` is otherwise enabled
5. Feature unification: a single version of `C` in the resolved graph
   gets the union of all features activated by all consumers
   (modulo resolver v2's per-context separation)

This is the most error-prone part of the Cargo adapter. The
implementation strategy:

- Represent a resolved crate as `(CargoCoordinate, set[feature])`
- After initial resolution produces a tree, run a feature
  expansion pass that unifies features per-crate per-context
- Re-check dependency satisfaction after feature expansion —
  enabling a feature can add new deps that may conflict
- Re-resolve if conflicts emerge (rare but possible)

The feature expansion and re-check is a fixed-point iteration.
Convergence is guaranteed because feature sets are monotonic
(can only grow) and bounded by the crate's declared feature set.

### 4.3 Cache keys

Substrate metadata cache keys for Cargo:

- `(ecosystem="rust", name=<crate>, version=*, platform_hash=*)` for
  the per-crate index file
- `(ecosystem="rust", name=<crate>, version=<v>, platform_hash=*)` for
  per-version metadata (derived from the index file's line for `v`)

Per-version entries are populated lazily from the per-crate entry;
they share a single SQLite row but the lookup API lets either
granularity be queried.

## 5. Resolver

### 5.1 PubGrub integration

Reuse substrate's `core/resolver/pubgrub` directly. Cargo is the
archetypal PubGrub user — the original Rust `pubgrub-rs` library was
built for Cargo's migration (still in progress as of 2024; once
complete, Sylk's substrate port will be structurally similar).

```go
type cargoDepProvider struct {
    fetcher *cargoFetcher
    manifest *CargoManifest
    workspace *resolvedWorkspace
    target TargetTuple  // cfg evaluation context
    resolverVersion int // 1 or 2
    cache *substrate.MetadataCache
}

func (p *cargoDepProvider) AvailableVersions(ctx context.Context, pkg CargoCoordinate) ([]SemVer, error) {
    // 1. Fetch sparse index file for pkg.Name.
    // 2. Parse each version's line.
    // 3. Filter: yanked → skip (unless lockfile-pinned with preserve-yanked).
    // 4. Filter: links=<x> collisions (only one crate with a given `links`
    //    value can be in the resolved graph).
    // 5. Filter: rust-version compatibility against project's rust-version.
    // 6. Order: newest-first, stable-before-pre-release.
    // 7. Apply lockfile preference.
}

func (p *cargoDepProvider) Dependencies(ctx context.Context, pkg CargoCoordinate, ver SemVer) ([]pubgrub.Dependency, error) {
    // 1. Look up this version's deps from the index file.
    // 2. Apply target/cfg filtering (deps with target="cfg(unix)" only apply on Unix targets).
    // 3. Apply feature expansion: for each requested feature in pkg.Features,
    //    collect transitively-activated optional deps.
    // 4. Translate to pubgrub.Dependency.
}
```

### 5.2 Target / cfg evaluation

Cargo supports conditional dependencies via `[target."cfg(...)".dependencies]`:

```toml
[target.'cfg(unix)'.dependencies]
nix = "0.26"

[target.'cfg(target_arch = "x86_64")'.dependencies]
raw-cpuid = "10"

[target.'cfg(not(target_os = "wasi"))'.dependencies]
mio = "0.8"
```

The `cfg(...)` expression is evaluated against the target triple
and compile-time cfg flags. Implement a small cfg expression
evaluator:

```go
type CfgExpr interface {
    Evaluate(env CfgEnv) bool
}

type CfgEnv struct {
    TargetOS     string   // "linux", "macos", "windows"
    TargetFamily string   // "unix", "windows"
    TargetArch   string   // "x86_64", "aarch64"
    TargetEnv    string   // "gnu", "musl", "msvc"
    TargetVendor string   // "apple", "pc", "unknown"
    Flags        []string // user-passed --cfg flags
}
```

Full cfg expression grammar: `cfg(K)`, `cfg(K = "V")`,
`cfg(not(E))`, `cfg(any(E1, E2, ...))`, `cfg(all(E1, E2, ...))`.

### 5.3 Frontier implementation

Cargo's resolver benefits from frontier-driven prefetching — each
`Considering` event triggers a sparse-index fetch for the candidate
package. Since sparse-index files are small (typically a few KB,
rarely >100KB), many candidate fetches can proceed in parallel
with minimal bandwidth cost.

```go
func (a *CargoAdapter) ResolveWithFrontier(ctx context.Context, req substrate.ResolveRequest, frontier chan<- substrate.FrontierEvent) (substrate.ResolveResult, error) {
    // Build provider, run pubgrub with frontier wired to prefetch coordinator.
    // Prefetch fetches sparse-index entries for newly-Considered coordinates.
    // On Backtracked, cancel fetches for abandoned branches (rare in
    // practice for Cargo because sparse-index fetches are so cheap that
    // cancellation rarely saves work, but the pattern is uniform with
    // other adapters).
}
```

### 5.4 Git and path dependencies

Git deps resolve to a fixed commit SHA at resolve time. The adapter
shells out to substrate's git client to resolve `branch` / `tag` /
`rev` to a SHA, clones the crate, reads its Cargo.toml for transitive
deps, and treats the result as a single synthetic version in the
solver.

Path deps are similar — read the local crate's Cargo.toml, treat as
a synthetic version pinned to whatever the source tree currently
declares.

Workspace members are automatically path deps for each other. The
workspace declares `[workspace.members = ["crate-a", "crate-b"]]`,
and `crate-a`'s deps on `crate-b` resolve to the local path, not
the registry.

### 5.5 [patch] overrides

The `[patch]` table in Cargo.toml lets a project override any
crate in its dep tree with a local path or git version:

```toml
[patch.crates-io]
log = { path = "../my-log-fork" }
```

Patches are applied *before* resolution — the resolver sees the
patch target as the only available version for that crate, and the
normal sparse-index entries are ignored.

The adapter honors `[patch]` transparently; the substrate doesn't
need to know it's happening. Implementation: the
`AvailableVersions` method, when the crate is patched, returns only
the single synthetic patched version.

## 6. Materializer

### 6.1 Cache layout

Mimic Cargo's `~/.cargo/registry` layout inside the substrate's
recipe store:

```
{recipe-store}/rust/
  cache/
    {registry-hostname}/
      {crate}-{version}.crate      # the .tar.gz, content-addressed
  src/
    {registry-hostname}/
      {crate}-{version}/           # extracted source
  index/
    {registry-hostname}/
      .cache/
        {sparse-index-cache}       # Cargo's local cache layout
```

The `src/` directory stores extracted sources; this is what `cargo
build` reads. The substrate materializer creates reflinks/hardlinks
from `src/` into the project's chosen materialization path (or leaves
`CARGO_HOME` pointing at the store directly when feasible).

### 6.2 `.crate` extraction

`.crate` files are gzipped tarballs with a single top-level directory
`{crate}-{version}/`. Extract with Go's `archive/tar` + `compress/gzip`:

```go
func ExtractCrate(src string, dst string) error {
    // Open .crate as gzip-wrapped tar.
    // Verify SHA-256 checksum against the index's `cksum` during read.
    // Extract files; reject absolute paths or `..` path components
    // (security).
    // Preserve file modes (important for shell scripts in build.rs dirs).
}
```

### 6.3 Directory layout for cargo compat

To make `cargo build` work against the substrate-materialized tree,
set `CARGO_HOME` to point at the substrate's store, or use
`--config registries.crates-io.index=...` pointing at a local
sparse-index mirror the substrate also serves.

The adapter's M3 goal: a project with a `Cargo.lock` produced by
Sylk can be built with a single `cargo build --offline` invocation
after `sylk install rust`, no network required.

### 6.4 Linking and vendoring

Two materialization modes:

**Shared-cache mode** (default): `CARGO_HOME` points at the substrate
store. All projects on the machine share crate sources. Identical to
native Cargo behavior — saves disk, respects Cargo conventions.

**Vendored mode** (opt-in): sources are copied (via reflink where
possible) into a project-local `vendor/` directory and a
`.cargo/config.toml` is written telling Cargo to look there instead
of the network. Useful for air-gapped builds and reproducible CI.

## 7. Lockfile

### 7.1 LockfileCodec

```go
type cargoLockfileCodec struct{}

func (c *cargoLockfileCodec) Ecosystem() string { return "rust" }
func (c *cargoLockfileCodec) Filename() string  { return "Cargo.lock" }
func (c *cargoLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) {
    // Parse TOML.
    // Support versions 1, 2, 3.
    // Produce substrate.LockfileSnapshot with CargoCoordinate per package.
}
func (c *cargoLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit version 3.
    // Sort packages by (name, version, source) for deterministic output.
    // Include checksums.
    // Emit workspace metadata section if workspace.
}
```

Cargo.lock has subtle ordering rules — packages are sorted by name,
version, source, and internal dependencies within a package block
are sorted by name. The codec must produce byte-identical output
for the same resolution, matching Cargo's own format exactly.
Round-trip tests on 50+ real Cargo.lock files from top Rust
projects.

### 7.2 Lockfile-as-hard-preference

Per substrate semantics. When `Cargo.lock` pins `serde = "1.0.193"`
and the current `Cargo.toml` says `serde = "1.0"`, the pin is
honored. When the `Cargo.toml` changes to `serde = "2.0"`, the pin
is invalidated and a fresh resolve selects a new version — but
other packages' pins are preserved.

`cargo update -p <crate>` is the semantic Cargo users expect for
selectively re-resolving; `sylk upgrade rust <crate>` should match.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Direct use; adapter supplies `DependencyProvider` |
| `core/substrate/http` | All sparse-index and .crate fetches |
| `core/substrate/cache/metadata` | Per-crate index cache, Etag-aware |
| `core/substrate/store/recipe` | Shared `.crate` storage and extracted sources |
| `core/substrate/materializer` | Reflink/hardlink for project `vendor/` mode |
| `core/substrate/lockfile` | `cargoLockfileCodec` |
| `core/substrate/feeds` | Multi-registry federation (crates.io + private) |
| `core/substrate/auth` | Credentials sourcing from `~/.cargo/credentials.toml` |
| `core/substrate/frontier` | Prefetch coordinator receives frontier events |
| `core/substrate/git` | Git dependency cloning |

Adapter-specific modules (under `adapters/rust/`):

- `coordinate.go` — `CargoCoordinate`, encoding
- `version.go` — `SemVer` implementing substrate's `Version`
- `manifest.go` — Cargo.toml parser
- `index.go` — sparse-index client + JSON-Lines parser
- `features.go` — feature expansion + unification
- `cfg.go` — cfg expression parser + evaluator
- `deps.go` — dependency translation
- `provider.go` — PubGrub `DependencyProvider`
- `patch.go` — `[patch]` and `[replace]` handling
- `crate_file.go` — .crate tarball extraction + verification
- `materializer.go` — shared-cache and vendored modes
- `lockfile.go` — LockfileCodec
- `adapter.go` — top-level Resolver impl

Estimated LOC: ~3,500. Smaller than Python because the sparse-index
protocol is clean and PubGrub fits Cargo's semantics directly.

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Sparse index 404 | `ErrNoSuchRecipe` | Crate doesn't exist on this registry |
| Version yanked | (filtered, not an error) | Unless lockfile-pinned; then warn |
| `links=` collision | `ErrCapabilityConflict` | Two crates claim same native library |
| rust-version too high | `ErrNoSatisfyingVersion` | Surface: "requires rustc >= X but project declares Y" |
| Feature doesn't exist | `ErrNoSatisfyingVersion` | Surface: "crate C has no feature F" |
| Checksum mismatch on .crate | `ErrIntegrityMismatch` | Potential attack or corrupt mirror |
| Git dep: ref doesn't resolve | `ErrNoSuchRecipe` | Branch deleted, tag missing |
| Workspace member missing | `ErrNoSuchRecipe` | `[workspace.members]` points at non-existent path |
| Feature unification conflict | `ErrCapabilityConflict` | Rare: typically surfaces as pubgrub conflict, occasionally as post-unification incompatibility |

## 10. Security

### 10.1 Checksum verification

Every `.crate` download's SHA-256 is verified against the
`cksum` field from the sparse index. Mismatches abort
materialization and surface `ErrIntegrityMismatch`.

The cksum comes from a trusted (authenticated over TLS) index
fetch. This is the supply-chain integrity guarantee crates.io
provides — it's equivalent to Go's `go.sum` model, just delivered
per-crate-version rather than per-project.

### 10.2 Yanked versions and RustSec advisories

Yanked versions are skipped by default. Known-vulnerable versions
(from [RustSec](https://rustsec.org/)) are also skipped if the
substrate's advisory integration is enabled. CVE → `CapabilityConflict`
with severity in the conflict report.

### 10.3 Private registries and token scope

Tokens stored in `~/.cargo/credentials.toml` must be per-registry
and never sent to crates.io or other registries. The adapter's
`AuthResolver` integration uses host-exact matching to prevent
token leakage across registries.

### 10.4 Git dependency SHA pinning

Git deps resolve to a commit SHA at first resolve; the SHA is
recorded in `Cargo.lock`. Subsequent resolves verify the ref still
points at the same SHA (or honor the pin). Prevents ref-rewriting
attacks where a branch is moved to a malicious commit.

## 11. Testing

### 11.1 Unit tests

- SemVer parser + comparator — corpus from
  [semver.org](https://semver.org/) + Cargo's own test vectors
- Cargo.toml parser — 100+ real manifests from crates.io top 500
- Sparse-index JSON-Lines parser — synthetic fixtures + captured
  crates.io responses
- Feature expansion — corpus of feature graphs including edge
  cases (`dep:`, `pkg/feat`, `pkg?/feat`, default-features cascading)
- Cfg expression parser + evaluator — corpus from
  [rust-lang/cargo](https://github.com/rust-lang/cargo/tree/master/tests/testsuite)
- .crate extraction — valid and malicious tarballs (path traversal,
  absolute paths, huge files, zip bombs)

### 11.2 Integration tests

- Resolve and materialize `tokio` and its dev-dependencies (dense
  dependency graph, exercises feature unification)
- Resolve `serde` with every combination of its features (exercises
  feature expansion)
- Resolve a workspace with 20 members (exercises path resolution +
  feature propagation)
- Resolve with a `[patch]` override replacing a transitive dep
- Resolve with a git dep pinned by tag vs by SHA

### 11.3 Ecosystem compatibility

Golden corpus of ~50 Rust projects, resolution compared against
`cargo generate-lockfile` output. Accepted divergences:

- None. Cargo's resolution is deterministic and the substrate must
  match byte-for-byte. Any divergence is a bug.

### 11.4 Performance

- Resolve tokio + full deps: <3s cold, <500ms warm
- Resolve a 200-crate workspace: <10s cold
- Extract a 100-crate materialization: <2s (reflink)
- Sparse-index fetch per crate: <100ms P50, <500ms P99

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, 100-crate graph | <3s | <2s |
| Warm resolve, 100-crate graph | <300ms | <150ms |
| Sparse-index fetch (cache miss) | <100ms | <50ms |
| Sparse-index fetch (cache hit, 304) | <10ms | <5ms |
| .crate extraction (typical crate) | <50ms | <20ms |
| Materialization, 100 crates (reflink) | <1s | <500ms |
| Peak memory, 500-crate resolve | <200MB | <120MB |
| Lockfile read+validate | <30ms | <15ms |
| Lockfile write | <30ms | <15ms |

## 13. Phases

**M0.** Types compile; unit tests for SemVer, Cargo.toml parser,
cfg evaluator, feature expander pass.

**M1.** Sparse-index client works against live crates.io. .crate
download with checksum verification works.

**M2.** End-to-end resolve: `tokio` full graph, with features and
workspace support. Frontier prefetch wired. Ecosystem-compat on top
20 projects matches `cargo generate-lockfile`.

**M3.** Materializer (shared-cache and vendored modes). Cargo.lock
codec round-trips. Patch/replace support. `[target.'cfg(...)']`
support. All 50 ecosystem-compat projects green. Performance
targets met.

**M4.** Telemetry, advisory integration (RustSec), alternative
registries (not just crates.io), production polish.

## 14. Open Questions

- **Resolver v1 vs v2 as default.** Cargo edition 2021+ defaults to
  v2; older editions default to v1. Sylk respects the project's
  declared resolver version but v1 has known issues. Should Sylk
  warn when v1 is detected? Proposal: warn but honor.
- **`[workspace.inheritance]` support.** Workspaces can declare
  shared metadata (`workspace = true` in member Cargo.tomls) that
  inherits from the root. Full support is a substantial amount of
  code; start with common inheritance (dependencies, edition) and
  defer corner cases.
- **Git submodule handling.** Git deps with submodules: clone
  recursively? At what depth? Default: follow, unlimited depth.
  Bad actors could DOS via pathological submodule graphs; rate-limit
  recursion depth as a safety measure.
- **Rustup integration.** Cargo depends on a specific rustc version;
  rustup manages toolchains. Should Sylk's Rust adapter also resolve
  the toolchain version (via `rust-toolchain.toml`)? Proposal: read
  the file, validate the installed toolchain matches, but don't
  install toolchains — that's rustup's job.
- **Alternative registries with non-sparse-index formats.** Some
  older private registries still serve git-index-only. Sparse-index
  is now standard; falling back to git-index is a significant
  complication. Proposal: refuse; document requirement.

## 15. Dependencies

- **Substrate M1** (PubGrub + HTTP + cache): unblocks adapter M1
- **Substrate M2** (frontier, multi-feed): unblocks adapter M2
- **Substrate M3** (materializer, lockfile framework): unblocks adapter M3

No dependencies on other adapters.

External Go dependencies beyond substrate:

- `github.com/BurntSushi/toml` — TOML parsing
- `github.com/Masterminds/semver/v3` — possibly; evaluate against
  rolling our own (Cargo's SemVer has nuances)
- Custom cfg-expression parser (no off-the-shelf Go library)
- Git client reused from substrate (not an adapter concern)
