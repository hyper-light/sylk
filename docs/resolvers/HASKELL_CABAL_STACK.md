# HASKELL_CABAL_STACK.md — Haskell / Cabal + Stack Adapter Implementation Plan

Tier 5 — most algorithmically demanding ecosystem in this doc.
Validates the substrate supports **dual-mode operation**: strict
constraint satisfaction (Cabal) AND curated-set consumption (Stack).
The "don't solve, curate" pattern is genuinely unique — the
adapter must know when to run a resolver and when to just apply a
snapshot.

## 1. Overview

The Haskell adapter resolves and materializes packages from:

- **[Hackage](https://hackage.haskell.org/)** (Cabal's default
  registry)
- **[Stackage](https://www.stackage.org/)** LTS and Nightly
  snapshots
- **Private Hackage mirrors** (hackage-server, artifactory)
- **Git dependencies** (via `source-repository-package` in
  cabal.project)
- **Source tarballs** (direct URL refs)

Operates in two modes depending on project configuration:

- **Cabal mode**: strict constraint solving via PubGrub; every
  transitive path must converge to the same version (Haskell's
  diamond-dep rule)
- **Stack mode**: no solving; read `stack.yaml`'s resolver,
  consume Stackage snapshot versions verbatim; skip the resolver
  entirely

Produces:

- A resolved package set (either solver-produced or snapshot-
  applied)
- `cabal.project.freeze` (Cabal mode) or `stack.yaml.lock` (Stack
  mode)
- A materialized package layout compatible with the user's build
  tool (`~/.cabal/store/` for Cabal or `~/.stack/snapshots/` for
  Stack)

User-visible behaviors (M3 target):

- `sylk resolve haskell ./my-project.cabal` → cabal.project.freeze
  (Cabal mode)
- `sylk resolve haskell ./stack.yaml` → stack.yaml.lock (Stack
  mode)
- `sylk install haskell` → packages in the appropriate store
- `sylk upgrade haskell` → re-solve (Cabal) or bump snapshot
  (Stack)
- `sylk why haskell <pkg>` → PubGrub explanation (Cabal) or "in
  snapshot lts-22.X" (Stack)

Non-goals:

- Running GHC (we resolve and materialize; compilation is the
  user's build tool)
- Building packages (Cabal's Setup.hs / Stack's compilation are
  out of scope)
- Managing GHC installations (ghcup/stack are outside adapter
  scope)

## 2. Data Model

### 2.1 Coordinates

```go
type HaskellCoordinate struct {
    Name    string              // "aeson", "text", "mtl"
    Version HaskellVersion      // Haskell PVP version
    Flags   map[string]bool     // cabal flags ("+fast", "-testing")
    GHC     string              // GHC version target (e.g. "9.4.7")
}

// Haskell uses PVP (Package Versioning Policy), a 4-part version:
// A.B.C.D where A.B is the "major" (breaking) and C.D is minor.
// This differs from SemVer — both A and B are breaking components.
type HaskellVersion struct {
    Components []int    // [1, 2, 3, 4] for "1.2.3.4"; trailing zeros preserved
    Raw        string
}

func (v HaskellVersion) Compare(other HaskellVersion) int {
    // Lexicographic on component-by-component.
}

// PVP major-minor extraction:
//   1.2.3.4 → major = "1.2", minor = "3.4"
//   A version range ">=1.2 && <1.3" locks both A and B (breaking-compat).
func (v HaskellVersion) PVPMajor() string { ... }
```

### 2.2 .cabal file

Each Haskell package has a `.cabal` file declaring metadata and
build configuration. Format is custom (vaguely YAML-like with
indentation-sensitive sections):

```cabal
name:             my-package
version:          1.0.0
synopsis:         Example package
build-type:       Simple
cabal-version:    >= 1.18

flag fast
  default: False
  description: Enable fast but less-safe code path

library
  exposed-modules:     My.Module
  build-depends:       base ^>= 4.17,
                       aeson >= 2.1 && < 2.3,
                       text >= 1.2 && < 3.0
  default-language:    Haskell2010

executable my-exe
  main-is:             Main.hs
  build-depends:       base, my-package
  if flag(fast)
    build-depends:     fastbench == 1.0
```

```go
type CabalFile struct {
    Name          string
    Version       HaskellVersion
    CabalVersion  VersionRange   // required cabal-install version
    BuildType     string         // "Simple", "Custom", etc.
    Flags         []CabalFlag
    Library       *CabalLibrary
    Executables   []CabalExecutable
    TestSuites    []CabalTestSuite
    Benchmarks    []CabalBenchmark
}

type CabalFlag struct {
    Name        string
    Default     bool
    Manual      bool           // if manual, user must explicitly set
    Description string
}

type CabalBuildDepends struct {
    Name          string
    VersionRange  VersionRange
    Conditional   *CabalCondition   // `if flag(fast)` etc.
}

type CabalCondition struct {
    Flag      string            // "flag(fast)"
    Negate    bool
    And       []CabalCondition
    Or        []CabalCondition
    OS        string            // "os(linux)"
    Arch      string            // "arch(x86_64)"
    Impl      string            // "impl(ghc)"
}
```

Custom parser (~1500 LOC; indentation-sensitive format).

### 2.3 cabal.project

Cabal's "project" file describes a multi-package build:

```
packages: .
          ./subpkg

source-repository-package
    type: git
    location: https://github.com/foo/bar.git
    tag: abc123

constraints: aeson < 3.0, mtl > 2.2

with-compiler: ghc-9.4.7

allow-newer: base

optional-packages: ./optional-pkg
```

```go
type CabalProject struct {
    Packages            []string                    // paths and globs
    SourceRepositoryPackages []SourceRepoPkg
    Constraints         []CabalConstraint           // solver hints
    Flags               map[string]map[string]bool  // pkg → flag → value
    WithCompiler        string                      // GHC version
    AllowNewer          []string
    AllowOlder          []string
    OptionalPackages    []string
    DocumentsPackages   []string
}
```

### 2.4 stack.yaml

Stack's project file:

```yaml
resolver: lts-22.13
packages:
- .
- subpkg
extra-deps:
- some-pkg-1.2.3@sha256:abc...
- github: foo/bar
  commit: abc123
flags:
  my-package:
    fast: true
```

```go
type StackYaml struct {
    Resolver      string                      // "lts-22.13" or URL
    Packages      []string
    ExtraDeps     []StackExtraDep
    Flags         map[string]map[string]bool
    AllowNewer    bool
}

type StackExtraDep struct {
    Name      string
    Version   HaskellVersion
    SHA256    string   // for Hackage deps
    GitURL    string
    Commit    string   // for Git deps
}
```

### 2.5 Stackage snapshot

```yaml
# https://raw.githubusercontent.com/commercialhaskell/stackage-snapshots/master/lts/22/13.yaml
resolver:
  name: lts-22.13
  compiler: ghc-9.6.3
packages:
- hackage: aeson-2.2.1.0@sha256:...,100
- hackage: text-2.0.2@sha256:...,50
- hackage: mtl-2.3.1@sha256:...,20
  ...
```

```go
type StackageSnapshot struct {
    Name         string
    Compiler     string                  // GHC version
    Packages     []SnapshotPackage
}

type SnapshotPackage struct {
    Name      string
    Version   HaskellVersion
    SHA256    string
    PantrySize int    // pantry cache size hint
}
```

## 3. HTTP Transport

### 3.1 Hackage endpoints

```
# Package index (incremental; served as a tarball):
GET https://hackage.haskell.org/01-index.tar.gz

# Per-package metadata:
GET https://hackage.haskell.org/package/{pkg}/{pkg}.cabal
GET https://hackage.haskell.org/package/{pkg}-{version}.tar.gz

# Revised .cabal (Hackage allows post-release metadata edits):
GET https://hackage.haskell.org/package/{pkg}/revision/{rev}.cabal
```

Hackage's "01-index" is a tarball containing every .cabal file in
the registry. Large (~300 MB uncompressed) but cacheable. Used via
hackage-security (TUF-based) for integrity.

Incremental updates: Hackage's HTTP serves range requests for
01-index.tar; clients track a byte offset and fetch only the
appended portion since last sync (append-only property).

### 3.2 Stackage endpoints

```
# Snapshot definition:
GET https://raw.githubusercontent.com/commercialhaskell/stackage-snapshots/master/{type}/{major}/{minor}.yaml

# Snapshot short URLs (used by Stack):
GET https://www.stackage.org/snapshot/lts-22.13
```

Stackage serves snapshots as YAML files. The snapshot is
content-addressed (its SHA is pinned in stack.yaml.lock).

### 3.3 Hackage-security (TUF)

Hackage uses [TUF (The Update Framework)](https://theupdateframework.io/)
for integrity and transparency. Verifies metadata signatures,
detects freeze/rollback attacks, supports offline verification.

The adapter implements a minimal TUF client sufficient for
Hackage:

- Root metadata (pinned keys)
- Snapshot metadata (per-release index manifest)
- Timestamp metadata (freshness)
- Target metadata (per-package)

## 4. Metadata Layer

### 4.1 Cabal file parser

Indentation-sensitive parser. Fields are case-insensitive. Build
sections (library/executable/test-suite/benchmark) have their own
dependency lists.

```go
func ParseCabalFile(data []byte) (*CabalFile, error) { ... }
```

Corpus for testing: vendor the Haskell community's cabal parser
test suite for round-trip validation.

### 4.2 Version range parser

Haskell version ranges are more expressive than SemVer:

- `== 1.2.3` — exact
- `>= 1.2 && < 2.0` — explicit range
- `^>= 1.2` — "caret" operator; ≥ 1.2 and < 1.3 (PVP-aware)
- `>= 1.2` — minimum
- `1.2.*` — wildcard
- Boolean combinations with `&&` and `||`

Parser is ~300 LOC PEG grammar.

### 4.3 Conditional dependency resolution

Cabal flags and conditionals affect the dep set:

```cabal
library
  build-depends: base, bytestring
  if flag(fast)
    build-depends: fastbench
  if os(linux) && impl(ghc >= 8.10)
    build-depends: linux-perf
```

Flag evaluation happens **at resolve time** with flag values from:

1. `cabal.project` explicit flag settings
2. Flag defaults from the .cabal file
3. Solver-chosen flags when `Manual: False`

The resolver treats manual flags as user input (constant) and
non-manual flags as variables it can choose (PubGrub picks flag
values to satisfy constraints).

### 4.4 Cache keys

```
(ecosystem="haskell", name=<pkg>, version=<ver>, platform_hash=<hash of (flags, GHC, OS, arch)>)
```

Flag selection produces different effective dependency sets, so
cache key incorporates flags.

## 5. Resolver

### 5.1 Cabal mode: strict consistency solver

PubGrub with an important constraint: **every path to package X
must agree on its version**. Haskell's type system makes
two-versions-of-same-package a compile error (diamond-dep rule).

```go
type haskellCabalProvider struct {
    fetcher       *hackageFetcher
    project       *CabalProject
    ghcVersion    string
    compilerInfo  CompilerInfo    // builtins like base, ghc-prim versions
    cache         *substrate.MetadataCache
}

func (p *haskellCabalProvider) AvailableVersions(ctx context.Context, pkg HaskellCoordinate) ([]HaskellVersion, error) {
    // 1. Fetch Hackage index entry for pkg.Name.
    // 2. Filter: ghc-version compat (from cabal-version requirements).
    // 3. Filter: cabal.project constraints (user-declared).
    // 4. Filter: allow-newer/allow-older overrides.
    // 5. Order: newest first, freeze-file preference.
}

func (p *haskellCabalProvider) Dependencies(ctx context.Context, pkg HaskellCoordinate, ver HaskellVersion) ([]pubgrub.Dependency, error) {
    // 1. Fetch the .cabal file for (pkg, ver).
    // 2. Evaluate flag conditions based on pkg.Flags.
    // 3. Apply os/arch/impl conditionals using compiler + target info.
    // 4. For library section: collect build-depends.
    // 5. For executables: optionally collect (if user wants to install).
    // 6. Translate to pubgrub.Dependency.
    //    STRICT: emit the dependency with its version range preserved; any
    //    downstream path to the same package must produce the same solution.
    //    PubGrub's standard semantics handle this naturally.
}
```

### 5.2 "Cabal hell" and its mitigations

Cabal's strictness means resolution can fail more often than in
npm or cargo ecosystems. Mitigations:

- **`allow-newer`**: loosen upper bounds for specific packages
- **`allow-older`**: loosen lower bounds
- **`constraints`**: inject additional version constraints
- **Freeze file** (`cabal.project.freeze`): pin every version

The adapter surfaces resolver failures with PubGrub's derivation
chain, making "cabal hell" debuggable.

### 5.3 Stack mode: snapshot consumption

```go
func (a *HaskellAdapter) resolveStackMode(ctx context.Context, stack *StackYaml) (substrate.ResolveResult, error) {
    // 1. Fetch the snapshot.
    // 2. Verify snapshot SHA if stack.yaml.lock specifies.
    // 3. Build the resolved set from snapshot.Packages.
    // 4. Apply extra-deps overrides (these replace or add beyond snapshot).
    // 5. Apply local packages (the project's own .cabal files).
    // 6. NO RESOLVER RUNS. The snapshot already guarantees consistency.
    // 7. Validate: every snapshot dep's transitive closure is in the snapshot.
    //    (Stackage curators guarantee this; we sanity-check.)
}
```

The "fastest resolver is no resolver." Stack mode is
milliseconds-fast for the resolution phase — just a snapshot
download and validation.

### 5.4 GHC as a package

GHC (the Haskell compiler) comes with "boot packages": `base`,
`ghc-prim`, `ghc-heap`, `template-haskell`, etc. These versions
are **fixed by the GHC version** and cannot be independently
resolved.

```go
type CompilerInfo struct {
    GHCVersion      string
    BootPackages    map[string]HaskellVersion  // base → 4.18.0.0, etc.
    Language        []string                    // supported language extensions
}

// The adapter seeds the resolver with boot-package constraints:
// "base is exactly <bootVersion>, ghc-prim is exactly <bootVersion>..."
// This forces the solver to find package versions compatible with the GHC-
// provided base library.
```

For Cabal mode, the user's GHC determines these. The adapter
detects the GHC via `ghc --numeric-version` or reads from
`cabal.project` / `stack.yaml`'s `with-compiler` directive.

### 5.5 Frontier

Cabal mode implements `FrontierAwareResolver` (PubGrub-based).
Stack mode **doesn't implement it** — no solver, no frontier.

Good example of the substrate's interface design handling
fundamentally different resolution shapes.

### 5.6 Revised .cabal handling

Hackage allows maintainers to edit `.cabal` files post-release for
metadata corrections (tightening version bounds, adding missing
deps). Revisions are numbered (revision 0 = original upload).

The adapter fetches the latest revision by default; users can pin
to a specific revision via cabal.project:

```
source-package-package-revision: aeson-2.2.1.0 revision: 2
```

## 6. Materializer

### 6.1 Cabal store layout

Cabal's `~/.cabal/store/ghc-{version}/` is content-addressed:

```
~/.cabal/store/
  ghc-9.4.7/
    package.db/                 # PackageDB (binary cache of installed pkgs)
    aeson-2.2.1.0-{hash}/       # extracted source, hash includes flags
    text-2.0.2-{hash}/
    ...
```

Hash includes the package's configuration (flags, deps resolved),
so different flag configurations get independent cache entries.

The substrate's recipe store mirrors this layout. Each entry is
keyed by (name, version, flag-set-hash) so cache hits are exact.

### 6.2 Stack snapshot layout

Stack's `~/.stack/snapshots/{platform}/{snapshot-name}/{ghc-version}/` layout:

```
~/.stack/snapshots/
  x86_64-linux/
    lts-22.13/
      9.4.7/
        pkgdb/
        lib/
          ...
```

Materialization creates this layout via hardlinks from the
substrate's content-addressed store.

### 6.3 Tarball extraction

Hackage tarballs are `.tar.gz` with the package contents under
`{name}-{version}/`. Extract with integrity verification against
hackage-security's target metadata SHA.

### 6.4 No build

The adapter doesn't compile. Cabal and Stack invoke GHC; the
adapter just ensures sources + metadata are in the right place.

## 7. Lockfile

### 7.1 cabal.project.freeze (Cabal mode)

```
constraints: aeson == 2.2.1.0,
             base == 4.18.0.0,
             text == 2.0.2,
             ...
```

Plus flag settings:

```
constraints: some-package +fast -testing
```

```go
type cabalFreezeCodec struct{}
func (c *cabalFreezeCodec) Filename() string { return "cabal.project.freeze" }
// Parse/write is mostly the cabal.project constraint line format.
```

### 7.2 stack.yaml.lock (Stack mode)

```yaml
packages: []
snapshots:
- completed:
    size: 712843
    url: https://raw.githubusercontent.com/commercialhaskell/stackage-snapshots/master/lts/22/13.yaml
    sha256: abc123...
  original: lts-22.13
```

Records the exact snapshot URL + SHA. Small — all other info is in
the snapshot itself.

```go
type stackLockfileCodec struct{}
func (c *stackLockfileCodec) Filename() string { return "stack.yaml.lock" }
```

## 8. Substrate Integration

| Substrate primitive | Cabal mode | Stack mode |
|---|---|---|
| `core/resolver/pubgrub` | Used | Not used |
| `core/substrate/http` | Used | Used |
| `core/substrate/cache/metadata` | Used | Used |
| `core/substrate/store/recipe` | Used | Used |
| `core/substrate/materializer` | Used | Used |
| `core/substrate/lockfile` | cabal.project.freeze codec | stack.yaml.lock codec |
| `core/substrate/feeds` | Hackage + mirrors | Stackage + Hackage |
| `core/substrate/frontier` | Used | Not used |
| `core/substrate/tuf` | Hackage-security | N/A |

Adapter modules under `adapters/haskell/`:

- `coordinate.go` — `HaskellCoordinate`
- `version.go` — `HaskellVersion` (PVP)
- `cabal_file.go` — .cabal parser
- `cabal_project.go` — cabal.project parser
- `stack_yaml.go` — stack.yaml parser
- `snapshot.go` — Stackage snapshot fetcher + parser
- `conditionals.go` — flag/os/arch/impl conditional evaluator
- `compiler.go` — GHC detection + boot-package info
- `hackage.go` — Hackage client
- `hackage_security.go` — TUF client
- `revisions.go` — revised .cabal handling
- `cabal_provider.go` — PubGrub DependencyProvider (Cabal mode)
- `stack_applier.go` — snapshot-as-resolution (Stack mode)
- `cabal_freeze.go` — cabal.project.freeze codec
- `stack_lock.go` — stack.yaml.lock codec
- `materializer.go` — both Cabal and Stack layouts
- `adapter.go` — top-level Resolver, mode dispatch

Estimated LOC: ~6,500. Complexity drivers:

- .cabal file parser (~1500 LOC; indentation-sensitive)
- TUF client (~800 LOC)
- Conditional evaluator with flag solving (~500 LOC)
- Two materializer layouts (~800 LOC)
- PVP version comparator (~300 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Cabal hell (unsolvable) | `ErrNoSatisfyingVersion` | PubGrub derivation shown |
| Transitive path version divergence | `ErrCapabilityConflict` | "Two paths require different aeson versions" |
| Snapshot not found on Stackage | `ErrNoSuchRecipe` | Snapshot URL + fallback suggestion |
| TUF signature invalid | `ErrSignatureFailed` | Fatal |
| Snapshot SHA mismatch | `ErrIntegrityMismatch` | Possibly tampered snapshot URL |
| GHC boot package version conflict | `ErrCapabilityConflict` | "base-4.17 required but GHC provides 4.18" |
| Extra-deps not in snapshot and not on Hackage | `ErrNoSuchRecipe` | Surface fallback path |
| Mix of cabal.project and stack.yaml detected | User error | Must pick one |

## 10. Security

### 10.1 Hackage-security (TUF)

Full TUF implementation. Signed metadata at every level (root →
snapshot → targets). Protects against:

- Malicious mirrors (keys pinned)
- Rollback attacks (timestamp verification)
- Freeze attacks (fresh timestamps required)
- Mix-and-match (snapshot → targets chain)

Reference: [hackage-security](https://github.com/haskell/hackage-security).

### 10.2 Snapshot integrity

Stack snapshots are content-addressed via SHA. The adapter records
the SHA in stack.yaml.lock; subsequent fetches verify.

### 10.3 Vulnerability advisories

[haskell-security-advisories](https://github.com/haskell/security-advisories)
provides CVE data. The adapter integrates as
`CapabilityConflict` with severity, similar to other adapters'
vulnerability integrations.

## 11. Testing

### 11.1 Unit tests

- PVP version parser + comparator
- .cabal parser on 100+ real files
- cabal.project parser
- stack.yaml parser
- Conditional evaluator (os/arch/impl/flag combinations)
- Stackage snapshot parser
- cabal.project.freeze round-trip
- stack.yaml.lock round-trip
- TUF verification (positive + negative test cases)

### 11.2 Integration tests

- Resolve IHP (web framework; large dep graph)
- Resolve a project using `allow-newer` to bypass strict bounds
- Resolve a project with custom cabal.project constraints
- Resolve a Stack project with lts-22.x
- Resolve a Stack project with extra-deps overrides
- Switch modes: same project resolved with both cabal.project
  and stack.yaml
- "Cabal hell" reproduction: deliberately unsolvable project;
  verify PubGrub explanation

### 11.3 Ecosystem compat

30 Haskell projects (smaller corpus; ecosystem is smaller).
Oracle: `cabal freeze` for Cabal mode, `stack ls dependencies`
for Stack mode.

### 11.4 Performance

- Cabal resolve, typical project: <5s cold, <1s warm (complex
  resolves can push this to 30s+ — we match Cabal's pace)
- Stack resolve: <500ms cold (fetch snapshot + validate), <100ms
  warm
- Hackage index sync: <10s first time (~300 MB), <1s incremental

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold Cabal resolve, typical project | <5s | <3s |
| Cold Cabal resolve, large project | <30s | <15s |
| Warm Cabal resolve | <1s | <500ms |
| Cold Stack resolve | <500ms | <250ms |
| Warm Stack resolve | <100ms | <50ms |
| Hackage index sync (incremental) | <1s | <500ms |
| Snapshot fetch | <300ms | <150ms |
| .cabal parse (typical) | <10ms | <5ms |
| Materialization, 50 pkgs (hardlink) | <1s | <500ms |
| Peak memory, Cabal resolve | <500MB | <300MB |

## 13. Phases

**M0.** Types, PVP version, .cabal parser; unit tests.

**M1.** Hackage client with TUF; Stackage client; single-package
fetch.

**M2.** Cabal-mode end-to-end with PubGrub + frontier. Stack-mode
end-to-end with snapshot consumption.

**M3.** Materializer for both modes. Lockfile codecs. Ecosystem
compat green on 30 projects.

**M4.** Vulnerability data integration. Revised .cabal handling.
allow-newer/allow-older full support. Production polish.

## 14. Open Questions

- **GHC discovery.** Assume user has GHC installed? ghcup / stack
  may manage it. Default: read from cabal.project `with-compiler`
  or stack.yaml's snapshot; prompt user when ambiguous.
- **Nix integration.** Some Haskell projects use Nix for reproducible
  builds. Out of scope for this adapter; compose with Sylk's Nix
  adapter (not implemented here) at a higher level.
- **Backpack modules.** GHC 8.2+ supports Backpack (parametric
  modules). .cabal has additional syntax. Edge case; defer
  full support to M4.
- **Stack project + Cabal mode conflict.** If a project has both
  stack.yaml and cabal.project, which wins? Proposal: detect;
  require user choice.
- **custom-setup.** Some packages have custom Setup.hs with their
  own build dependencies (`setup-depends` in .cabal). Handle
  similarly to regular deps but scoped to setup phase.

## 15. Dependencies

- Substrate M2 (PubGrub, frontier, multi-feed) → adapter M2 (Cabal mode)
- Substrate M3 (materializer, lockfile) → adapter M3
- Substrate TUF primitives for hackage-security

External Go dependencies:

- Custom .cabal parser (~1500 LOC)
- Custom Stackage YAML parser (`gopkg.in/yaml.v3` for most; some
  custom handling)
- TUF client (port of TUF spec; ~800 LOC)

No dependency on other adapters.
