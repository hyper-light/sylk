# SWIFT_SPM.md — Swift / SwiftPM Adapter Implementation Plan

Tier 5 — the **no-central-registry** case. Validates the substrate's
**git-URL-as-identity** primitive (modules identified by git URL,
not registry-issued name), and that the substrate's PubGrub
implementation matches SwiftPM's (the earliest commercial PubGrub
deployment).

## 1. Overview

The Swift adapter resolves and materializes packages from:

- **Git repositories at arbitrary URLs** (primary; GitHub dominant,
  GitLab/Bitbucket/corporate git also used)
- **Local path dependencies**
- **Swift Package Registry** (Apple's opt-in registry API; rare in
  open-source, common in enterprise)

Produces:

- A resolved dependency set with exact commit SHAs
- `Package.resolved` lockfile (SwiftPM's native format)
- A materialized checkouts directory (`.build/checkouts/`)
  compatible with SwiftPM's own tooling

User-visible behaviors (M3 target):

- `sylk resolve swift ./Package.swift` → Package.resolved
- `sylk install swift` → checkouts populated
- `sylk add swift <git-url>[@<range>]` → modifies Package.swift
- `sylk why swift <pkg>` → PubGrub explanation

Non-goals:

- Running `swift build` (resolver + materializer only)
- Xcode integration (Xcode invokes SwiftPM for resolution; Sylk
  is an alternative invocation path)
- Managing Swift toolchain installations

## 2. Data Model

### 2.1 Coordinates

```go
type SwiftCoordinate struct {
    Identity string         // canonical identity; typically last-path-component of git URL
    URL      string         // full git URL
    Version  SwiftVersion
    Source   SwiftSource    // version, branch, commit, local
    Products []string       // products (libs, exes) consumed
    Targets  []string       // targets consumed (narrower than products)
}

type SwiftVersion struct {
    // SemVer 2.0, strict. SwiftPM doesn't allow pre-release versions in
    // version-based dependencies (you must use revision: for pre-release).
    Major, Minor, Patch int
    Pre, Build          string
}

type SwiftSource struct {
    Kind     SwiftSourceKind   // version, branch, revision, localPath
    Version  SwiftVersion
    Branch   string
    Revision string             // exact commit SHA
    Path     string
}
```

### 2.2 Package.swift

The manifest file is Swift source code. SwiftPM evaluates it in a
sandboxed Swift interpreter. We need to **extract declarative
metadata** — dependencies, products, targets — without running
arbitrary Swift.

Two approaches:

**A. Parse the declarative Swift subset statically.** Works for
~95% of real projects which use a standard Package.swift template.
~1500 LOC Swift-lite parser.

**B. Invoke `swift package dump-package`.** Shells out to the
Swift toolchain; the output is JSON. Slower (Swift startup ~1s)
but fully accurate.

Hybrid: static parsing for the common case, fallback to `swift
package dump-package` when parsing fails or the manifest has
non-declarative logic.

```go
type SwiftManifest struct {
    Name           string
    DefaultLocalization string
    Platforms      []PlatformConstraint
    Products       []SwiftProduct
    Targets        []SwiftTarget
    Dependencies   []SwiftDependency
    SwiftLanguageVersions []string
    CLanguageStandard string
    CxxLanguageStandard string
}

type SwiftDependency struct {
    URL        string
    Name       string    // explicit name or derived from URL
    Requirement SwiftRequirement
    LocalPath  string
}

type SwiftRequirement struct {
    Kind     SwiftRequirementKind
    Exact    SwiftVersion         // .exact("1.2.3")
    Range    SwiftVersionRange    // .upToNextMinor(from: "1.2.0"), .upToNextMajor(from: "1.0.0")
    Branch   string               // .branch("main")
    Revision string               // .revision("abc123")
}
```

### 2.3 Package.resolved

SwiftPM's lockfile, JSON:

```json
{
  "pins": [
    {
      "identity": "swift-argument-parser",
      "kind": "remoteSourceControl",
      "location": "https://github.com/apple/swift-argument-parser.git",
      "state": {
        "revision": "8f4d2753f0e4778c76d5f05ad16c74f707390531",
        "version": "1.3.0"
      }
    },
    {
      "identity": "swift-crypto",
      "kind": "remoteSourceControl",
      "location": "https://github.com/apple/swift-crypto.git",
      "state": {
        "revision": "cc76b894169519f34f9d94fbe4bf5ec62bcd0594",
        "version": "3.1.0"
      }
    }
  ],
  "version": 2
}
```

The revision (commit SHA) is the integrity guarantee — moved tags
produce a revision mismatch and fail the build.

### 2.4 Identity derivation

Swift identity is derived from the git URL:

```
https://github.com/apple/swift-argument-parser.git
→ identity: swift-argument-parser
```

The last path component, stripped of `.git` suffix, lowercased.
Collisions between different hosts serving same-name packages can
occur — SwiftPM warns; Sylk treats as `ErrCapabilityConflict` by
default.

## 3. HTTP Transport

### 3.1 Git over HTTPS and SSH

Every dependency is a git clone. Substrate's git client handles
both HTTPS and SSH:

```bash
# HTTPS:
git clone https://github.com/apple/swift-argument-parser.git

# SSH:
git clone git@github.com:apple/swift-argument-parser.git
```

Auth:

- HTTPS: `~/.netrc` or OS keychain credentials
- SSH: `~/.ssh/` keys or SSH agent

Substrate AuthResolver handles all of this.

### 3.2 Shallow clones

For resolution, we only need:

- `git ls-remote` to enumerate tags (version candidates)
- Fetching a specific tag/commit's `Package.swift` (for dependency
  discovery)

Shallow clone (`--depth 1`) for each resolved version minimizes
bandwidth. Full history only needed if the user explicitly requests
`revision` ref.

### 3.3 Package registry (opt-in alternative)

When the project configures a Swift Package Registry:

```
GET {registry}/{scope}/{name}/{version}
```

Same pattern as NuGet's registry — service-index style. The
adapter supports this in addition to git URLs.

Rare in OSS; enterprise deployments use it for private
packages with HTTP-style distribution.

## 4. Metadata Layer

### 4.1 Package.swift parsing

The declarative-subset parser handles:

```swift
// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "MyApp",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "MyLib", targets: ["MyLib"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-argument-parser",
                 from: "1.3.0"),
        .package(url: "https://github.com/apple/swift-crypto",
                 .upToNextMajor(from: "3.0.0")),
    ],
    targets: [
        .target(
            name: "MyLib",
            dependencies: [
                .product(name: "ArgumentParser", package: "swift-argument-parser"),
                "Crypto",
            ]
        ),
        .testTarget(name: "MyLibTests", dependencies: ["MyLib"]),
    ]
)
```

Static parser extracts the `Package(...)` literal's arguments:
name, platforms, products, dependencies, targets. Works when
Package.swift is a straightforward top-level declaration (most
common case).

For non-trivial Package.swift (with Swift logic, conditional
compilation, etc.), fall back to `swift package dump-package`:

```go
func (a *SwiftAdapter) ParseManifest(ctx context.Context, path string) (*SwiftManifest, error) {
    if manifest, err := a.parseStatic(path); err == nil {
        return manifest, nil
    }
    return a.parseViaSwiftToolchain(ctx, path)  // subprocess
}
```

### 4.2 Dependency requirement parsing

Six requirement forms in Package.swift:

- `.exact("1.2.3")` — pin exact version
- `.upToNextMajor(from: "1.0.0")` — >= 1.0.0, < 2.0.0
- `.upToNextMinor(from: "1.0.0")` — >= 1.0.0, < 1.1.0
- `"1.0.0"..<"2.0.0"` — explicit range
- `.branch("main")` — branch ref (not version-based)
- `.revision("abc123")` — specific commit (not version-based)

Version-based dependencies go through PubGrub; branch/revision
dependencies are **pre-pinned** and skip the resolver (they're
their own synthetic version).

### 4.3 swift-tools-version

Package.swift's first line declares the minimum SwiftPM version:

```swift
// swift-tools-version: 5.9
```

The adapter respects this — features requiring newer tooling (e.g.,
package access modifiers, custom module access) aren't parsed.
Emits a warning when a package's tools version exceeds what we can
parse.

### 4.4 Cache keys

```
(ecosystem="swift", name=<git-URL-hash>, version=<SemVer or commit>, platform_hash=<hash of (swift-tools-version, platform)>)
```

git URL hash uniquely identifies the source; version includes
branches/commits when non-SemVer.

## 5. Resolver

### 5.1 PubGrub via substrate

```go
type swiftDepProvider struct {
    fetcher    *swiftGitFetcher
    project    *SwiftManifest
    platform   substrate.PlatformTuple
    cache      *substrate.MetadataCache
}

func (p *swiftDepProvider) AvailableVersions(ctx context.Context, pkg SwiftCoordinate) ([]SwiftVersion, error) {
    // 1. git ls-remote to get all tags.
    // 2. Parse tags as SemVer; skip non-SemVer tags.
    // 3. Filter by Package.swift's platform requirements (at resolve
    //    time, not just materialize).
    // 4. Order: newest first, Package.resolved pin priority.
}

func (p *swiftDepProvider) Dependencies(ctx context.Context, pkg SwiftCoordinate, ver SwiftVersion) ([]pubgrub.Dependency, error) {
    // 1. Check cache for this (URL, version) → deps mapping.
    // 2. If miss: shallow clone, read Package.swift at tag, parse deps.
    // 3. Filter by requested products (only deps needed by consumed products).
    // 4. Translate to pubgrub.Dependency.
}
```

### 5.2 Product-level dependency filtering

Swift has a unique feature: a package consumer depends on
**specific products** of the dependency, not the whole package. A
package can expose multiple products, and different consumers can
take different subsets.

```swift
.package(url: "...", from: "1.0.0"),
.product(name: "ArgumentParser", package: "swift-argument-parser"),
```

When resolving transitive deps, the adapter follows only the graph
reachable through consumed products. A product's target's
dependencies become transitive constraints; unused products don't
add constraints.

### 5.3 Platform constraints

Package.swift declares supported platforms:

```swift
platforms: [.iOS(.v17), .macOS(.v14), .tvOS(.v17)]
```

During resolution, transitive platforms must be compatible with
consumer's platforms. A dependency requiring iOS 18+ can't be used
by a consumer declaring iOS 17 as minimum.

### 5.4 Branch / revision dependencies

Non-version refs bypass PubGrub:

- Branch: resolve the branch to a SHA at resolve time; pin SHA in
  Package.resolved.
- Revision: already a SHA; no resolution needed.

These behave as single-version pins — the PubGrub provider returns
only the resolved SHA as the available version.

### 5.5 Frontier

PubGrub-based. `FrontierAwareResolver` implemented. Frontier events
trigger git ls-remote / shallow clone for candidate versions.

Less bandwidth-efficient than per-version metadata fetches (git
clone is heavier than fetching a single metadata file), but the
shared local clone cache (§ 6.1) makes this acceptable for warm
cache — the first clone dominates; subsequent resolves hit the
local cache.

## 6. Materializer

### 6.1 Local clone cache

Substrate's recipe store holds a single clone per unique git URL:

```
{recipe-store}/swift/clones/
  https/github.com/apple/swift-argument-parser.git/
    .git/
    Package.swift
    Sources/
    ...
```

For each resolved (URL, revision), materialize into project-local
`.build/checkouts/` via reflink/hardlink from the clone cache.
When a revision is requested that's not in the clone, fetch the
specific ref.

### 6.2 .build/checkouts/ layout

SwiftPM's checkout layout:

```
.build/
  checkouts/
    swift-argument-parser/
      .git/
      Package.swift
      Sources/
      ...
    swift-crypto/
      ...
  artifacts/
    <precompiled binaries for binary targets>
  workspace-state.json     # SwiftPM's internal state file
```

The adapter produces compatible layout so `swift build` can
proceed using our materialization.

### 6.3 Binary targets

Swift 5.6+ supports binary targets via `.binaryTarget(...)`.
Fetch the binary archive (XCFramework or similar), verify
checksum, extract to `.build/artifacts/`.

### 6.4 Resource bundles

Some targets declare resources (images, localization files, etc.).
The materializer handles resource collection per SwiftPM's rules
(out of scope for resolver; reuse substrate materialize primitives).

## 7. Lockfile

### 7.1 Package.resolved

```go
type swiftLockfileCodec struct{}

func (c *swiftLockfileCodec) Ecosystem() string { return "swift" }
func (c *swiftLockfileCodec) Filename() string  { return "Package.resolved" }

func (c *swiftLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) {
    var pkg struct {
        Version int
        Pins    []swiftPin
    }
    if err := json.Unmarshal(data, &pkg); err != nil {
        return substrate.LockfileSnapshot{}, err
    }
    // Translate to LockfileSnapshot.
}

func (c *swiftLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit JSON matching SwiftPM's format:
    // - Version 2 (current)
    // - Pins sorted by identity
    // - 2-space indentation
    // - Trailing newline
}
```

Byte-identical to `swift package resolve` output for ecosystem compat.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Direct use |
| `core/substrate/http` | Registry API fallback (rare) |
| `core/substrate/cache/metadata` | (URL, version) → deps cache |
| `core/substrate/store/recipe` | Shared git clone cache |
| `core/substrate/materializer` | .build/checkouts/ layout |
| `core/substrate/lockfile` | Package.resolved codec |
| `core/substrate/feeds` | Git URLs as feeds |
| `core/substrate/auth` | HTTPS + SSH git auth |
| `core/substrate/git` | All git operations |
| `core/substrate/frontier` | Standard PubGrub frontier |
| `core/substrate/subprocess` | Swift toolchain fallback for manifest parsing |

Adapter modules under `adapters/swift/`:

- `coordinate.go` — `SwiftCoordinate`
- `version.go` — `SwiftVersion`
- `identity.go` — git-URL → identity derivation
- `manifest_static.go` — declarative Swift subset parser
- `manifest_dump.go` — `swift package dump-package` fallback
- `requirements.go` — requirement kinds + version range translation
- `platforms.go` — platform compatibility
- `products.go` — product-level dependency filtering
- `provider.go` — PubGrub DependencyProvider
- `git_cache.go` — shared clone cache
- `registry.go` — Package Registry API client (optional)
- `materializer.go` — .build/checkouts/ layout
- `lockfile.go` — Package.resolved codec (byte-identical)
- `adapter.go` — top-level Resolver

Estimated LOC: ~4,000. Complexity drivers:

- Swift declarative parser (~1500 LOC)
- Product-level dep filtering (~400 LOC)
- Git operations + clone cache coordination (~600 LOC)
- Lockfile byte-identical codec (~300 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Git URL unreachable | `ErrNetworkPermanent` | After substrate retry |
| No version tags on repo | `ErrNoSatisfyingVersion` | "Expected semver tags like 1.0.0 but repo has [main, develop]" |
| Branch/revision ref not found | `ErrNoSuchRecipe` | Branch deleted or tag missing |
| Platform constraint unsatisfiable | `ErrNoSatisfyingVersion` | "Dependency requires iOS 18, project targets iOS 17" |
| Identity collision (same name, different URLs) | `ErrCapabilityConflict` | Surface both URLs |
| Package.swift parse failure | User error | Suggest `swift package dump-package` fallback |
| Manifest's swift-tools-version exceeds our parser | (warning) | Fall back to dump-package |
| Binary target checksum mismatch | `ErrIntegrityMismatch` | Fatal |

## 10. Security

### 10.1 Commit SHA pinning

Integrity for Swift deps comes from git commit SHAs. Every pin in
Package.resolved records an exact SHA. Tag rewrite attacks
(repointing v1.0.0 to a different commit) produce SHA mismatches
at resolve time — Sylk surfaces as `ErrIntegrityMismatch`.

### 10.2 Binary target checksums

Binary targets declare SHA-256 checksums:

```swift
.binaryTarget(
    name: "SomeBinary",
    url: "https://example.com/binary.xcframework.zip",
    checksum: "abc123..."
)
```

Verified on fetch. Mismatch is fatal.

### 10.3 Git SSH host verification

Substrate git client validates host keys against user's
known_hosts. Prevents MITM on SSH transport.

### 10.4 HTTPS certificate pinning (optional)

For corporate deployments using private git hosts, the adapter
supports optional cert pinning via substrate config.

## 11. Testing

### 11.1 Unit tests

- `SwiftVersion` parser + comparator (SemVer strict)
- Identity derivation from various URL shapes (GitHub, GitLab,
  SCP-style SSH)
- Static Package.swift parser on 100+ real manifests
- Requirement parser for all six forms
- Product/target reachability analysis
- Package.resolved round-trip byte-identical on 30+ real files

### 11.2 Integration tests

- Resolve Vapor (large Swift web framework dep tree)
- Resolve a project mixing version, branch, and revision
  dependencies
- Resolve with multi-product dependencies (apple/swift-nio)
- Resolve a project with binary targets
- Identity collision detection
- Platform constraint propagation

### 11.3 Ecosystem compat

30 Swift projects. Oracle: `swift package resolve` output. Match
Package.resolved byte-for-byte.

### 11.4 Performance

Swift PM's native resolution is slow (typically seconds due to
git operations). We should match or slightly beat via better
clone caching:

- Resolve Vapor: <5s cold (after clone cache warm), <1s warm
- Resolve typical Swift app: <3s cold, <500ms warm
- First-time clone of a large repo: dominated by git fetch

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, typical app | <3s | <2s |
| Cold resolve, Vapor-scale | <10s | <6s |
| Warm resolve | <500ms | <250ms |
| Static Package.swift parse | <10ms | <5ms |
| `swift package dump-package` fallback | ~1s (dominated by Swift startup) | — |
| Shallow clone (typical repo) | <500ms | <250ms |
| Materialization, 20 deps (reflink) | <500ms | <250ms |
| Lockfile byte-identical write | <20ms | <10ms |

## 13. Phases

**M0.** Types, static Package.swift parser; unit tests.

**M1.** Git client integration for ls-remote + shallow clone.
Tag enumeration; Package.swift parsing on tagged versions.

**M2.** PubGrub end-to-end. Product/target filtering. Frontier.
Handle branch/revision refs.

**M3.** .build/checkouts/ materializer. Package.resolved byte-
identical. 30 ecosystem-compat projects green. Binary target
support.

**M4.** Package Registry API (opt-in). swift-tools-version ≥6 parser
updates. Production polish.

## 14. Open Questions

- **Static parser vs dump-package fallback.** Aim for static
  parsing as primary (no Swift toolchain required); fallback only
  when static parse fails. Measure: what fraction of real projects
  require fallback?
- **git clone strategy.** Shallow by default; fetch history only
  when a `revision` ref is requested and the commit isn't in the
  shallow clone. Test with force-pushed branches.
- **Identity collision resolution.** When two deps have colliding
  identities (derived from URL), error or pick one? SwiftPM
  warns; Sylk should fail by default with an opt-in override.
- **Swift Package Registry adoption.** As registry adoption grows
  in enterprise, the adapter should prefer registry over git when
  both are configured. Config knob: `--prefer-registry`.

## 15. Dependencies

- Substrate M2 (frontier, git client) → adapter M2
- Substrate M3 (materializer, lockfile) → adapter M3

External Go dependencies:

- Custom Swift declarative parser (~1500 LOC)
- `encoding/json` — stdlib for Package.resolved
- Git operations via substrate client (no external git library)

No dependency on other adapters.
