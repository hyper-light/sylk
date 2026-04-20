# OCAML_OPAM.md — OCaml / OPAM Adapter Implementation Plan

Tier 5 — the **research-grade adapter**. Validates the substrate
supports **pluggable solvers per adapter** (OPAM's CUDF +
multiple solver backends), **compiler-as-primary-constraint**
(OCaml compiler is a package in the graph), and **first-class
named environments** (OPAM switches).

## 1. Overview

The OCaml adapter resolves and materializes packages from:

- **[opam-repository](https://github.com/ocaml/opam-repository)**
  (default; git-tree registry)
- **Private OPAM repositories** (can be git, HTTPS file tree,
  or rsync)
- **Pinned packages** (`opam pin`; local paths or explicit URLs)

Solver strategies (pluggable):

- **Built-in mccs** (default; substrate's Go port of OPAM's
  default SAT/pseudo-boolean solver)
- **0install-style** fast path (substrate's alternative for
  clean-environment resolves)
- **External SAT** (aspcud via subprocess; for users needing
  advanced optimization criteria)

Produces:

- A resolved package set consistent with OCaml compiler constraints
- An OPAM-compatible switch layout (`~/.opam/{switch-name}/`)
- Lockfile (`sylk.lock` with OPAM-aware structure;
  OPAM's native lockfile support is partial — Sylk extends)

User-visible behaviors (M3 target):

- `sylk resolve ocaml ./myproject.opam` → resolved set
- `sylk install ocaml` → populated switch
- `sylk switch ocaml create <name> <ocaml-version>` → new switch
- `sylk add ocaml <pkg>` → updates .opam file, re-resolves
- `sylk why ocaml <pkg>` → CUDF explanation via solver backend

Non-goals:

- Building OCaml from source (we select an ocaml-base-compiler
  version; system provides it via opam's own mechanisms or
  ocaml-variants)
- Running Dune (OCaml's build tool)
- Opam plugins

## 2. Data Model

### 2.1 Coordinates

```go
type OpamCoordinate struct {
    Name    string              // "dune", "cmdliner", "ocaml-base-compiler"
    Version OpamVersion
    // OPAM doesn't have classifiers or variants per se.
}

// OPAM versions are similar to Debian versions. Parsed via a custom
// parser that handles epochs, release tags, and odd suffixes.
type OpamVersion struct {
    Epoch    int        // "1:2.0.0" has epoch=1
    Upstream string     // "2.0.0" part
    Revision string     // "-rc1" or similar
    Raw      string
}

func (v OpamVersion) Compare(other OpamVersion) int {
    // OPAM version ordering: epoch, then upstream-version component-by-component,
    // then revision. Implements DPKG-style "policy version" comparison.
}
```

### 2.2 .opam files

Each OPAM package version has an `opam` file declaring metadata:

```
opam-version: "2.0"
name: "lwt"
version: "5.7.0"
maintainer: "..."
homepage: "..."
dev-repo: "..."

depends: [
  "ocaml" {>= "4.08"}
  "dune" {>= "3.0"}
  "cppo" {build & >= "1.1.0"}
  "mmap"
  "ocplib-endian"
]

depopts: [
  "conf-libev"
]

conflicts: [
  "ocaml" {< "4.08"}
]

build: [
  ["dune" "build" "-p" name "-j" jobs]
]

install: [
  ["dune" "install" "-p" name]
]

available: os != "win32"

synopsis: "Promises and event-driven I/O"
```

```go
type OpamFile struct {
    OpamVersion  string
    Name         string
    Version      OpamVersion
    Maintainer   string
    Homepage     string
    DevRepo      string

    Depends      []OpamDep
    Depopts      []OpamDep       // optional dependencies
    Conflicts    []OpamDep       // cannot coexist
    ConflictClass []string       // class-based conflicts
    Build        []OpamCommand
    Install      []OpamCommand
    Remove       []OpamCommand
    Available    OpamFilter      // predicate on host environment

    Synopsis     string
    Description  string
    Bug          string
    License      string
}

type OpamDep struct {
    Name    string
    Filter  OpamFilter           // version range + build-only + test-only etc.
}

type OpamFilter struct {
    // Tree of conditions: version constraints, variable references,
    // logical operators. Evaluated with a variable context at resolve time.
    Kind       FilterKind  // Version, Variable, BinaryOp, Not, Group
    VersionOp  string      // ">=", "<=", "=", "!=", ">", "<"
    Version    OpamVersion
    Variable   string      // "os", "build", "post", "with-test", "dev"
    VarValue   string
    BinaryOp   string      // "and", "or"
    Left, Right *OpamFilter
    Inner       *OpamFilter // for Not/Group
}
```

OPAM filters are a mini-language. They encode version constraints
AND evaluation context AND build-phase flags. The parser is
~800 LOC PEG grammar.

### 2.3 CUDF representation

For the solver, package data is translated to CUDF format:

```
package: dune
version: 1
depends: cppo >= 1, ocaml >= 2

package: cppo
version: 1

package: ocaml
version: 2

request:
install: dune
```

```go
type CUDFDoc struct {
    Preamble CUDFPreamble  // property declarations
    Packages []CUDFPackage
    Request  CUDFRequest
}

type CUDFPackage struct {
    Name        string
    Version     int          // CUDF uses integer versions
    Depends     []CUDFDepList
    Conflicts   []CUDFDep
    Installed   bool
    Keep        string       // "none", "version", "package"
    Properties  map[string]interface{}
}

type CUDFDep struct {
    Name    string
    VersionOp string    // ">=", "=", etc.
    Version int
}
```

Version translation: OPAM versions map to monotonic integers for CUDF.
The adapter maintains a (pkg, opam-version) → cudf-integer mapping
per resolve; after solving, the inverse maps solution-ints back to
OPAM versions.

### 2.4 Switches

An OPAM switch is a named, isolated environment with its own
compiler + package set:

```go
type OpamSwitch struct {
    Name         string           // "4.14.1", "5.1.0", "my-project"
    CompilerPkg  string           // "ocaml-base-compiler"
    CompilerVer  OpamVersion
    InstalledPkgs []OpamCoordinate
    Root          string           // "~/.opam/my-project"
}
```

Sylk's substrate named-environment primitive maps directly to OPAM
switches.

## 3. HTTP Transport

### 3.1 opam-repository as a git tree

The canonical OPAM repository is a GitHub repo. Each package version
lives at:

```
opam-repository/
  packages/
    dune/
      dune.3.12.1/
        opam              # metadata
        url               # source tarball URL + checksum
      dune.3.12.0/
        opam
        url
```

The adapter fetches via git clone (shallow; substrate's git client).
Updates via `git pull` (incremental, fast).

### 3.2 Mirror / HTTPS tree support

OPAM also supports HTTPS-served repositories where the git tree is
served as a static file hierarchy. Faster for clients but less
flexible than full git.

```
https://opam.ocaml.org/packages/dune/dune.3.12.1/opam
https://opam.ocaml.org/packages/dune/dune.3.12.1/url
```

The adapter supports both modes. git is the authoritative source
(supports arbitrary history); HTTPS mirrors are faster for pure
reads.

### 3.3 Source tarball fetch

`url` file declares where to fetch the package's source:

```
src: "https://github.com/ocaml/dune/releases/download/3.12.1/dune-3.12.1.tbz"
checksum: [
  "sha256=abc123..."
  "sha512=def456..."
]
```

Fetch via substrate HTTP; verify both checksums.

### 3.4 Authentication

OPAM doesn't typically need auth (opam-repository is public,
opam.ocaml.org is public). Private repositories use git
auth (SSH key / HTTPS basic) handled by substrate's git
client.

## 4. Metadata Layer

### 4.1 OPAM file parser

Custom format; ~800 LOC recursive-descent parser. Key challenges:

- Multi-line strings
- Nested lists and options
- Filter expressions with operator precedence
- Variable interpolation in strings (`%{var}%`)

### 4.2 Filter evaluation

Filters evaluate to boolean or string-or-boolean values given a
variable context:

```go
type FilterContext struct {
    OS       string     // "linux", "macos", ...
    Arch     string     // "x86_64", "arm64"
    Build    bool       // true during build phase
    Post     bool
    Test     bool
    Dev      bool
    Jobs     int
    // Many more: os-family, os-distribution, sys-ocaml-version,
    // variant flags, etc.
}

func (f OpamFilter) Eval(ctx FilterContext) bool { ... }
```

### 4.3 Version range extraction

For the solver, filters that encode version constraints are
extracted as pure version predicates. The remaining filter logic
becomes "is this dep active?" at resolve time.

```go
func ExtractVersionRange(f *OpamFilter, ctx FilterContext) (VersionRange, bool /* dep is active */) { ... }
```

### 4.4 Cache keys

```
(ecosystem="ocaml", name=<pkg>, version=<ver>, platform_hash=<hash of FilterContext>)
```

Filter context affects effective dep set, so cache per-context.

## 5. Resolver

### 5.1 CUDF translation

```go
func (a *OpamAdapter) translateToCUDF(request substrate.ResolveRequest, allPackages []*OpamFile) (*CUDFDoc, error) {
    // 1. Build version-integer map:
    //    for each (pkg, version) in allPackages, assign monotonic integer.
    // 2. For each package, emit CUDFPackage:
    //    - Translate OPAM deps to CUDFDepList (OR-groups of alternatives).
    //    - Translate conflicts.
    //    - Filter by Available (packages whose Available evaluates false are omitted).
    // 3. Build request:
    //    - install: root package
    //    - Honor user-specified install constraints
}
```

### 5.2 Solver backends

```go
type CUDFSolver interface {
    Solve(ctx context.Context, doc *CUDFDoc, criteria string) (*CUDFSolution, error)
}

// Built-in solvers:
// - mccsSolver: Go port of OPAM's mccs (SAT/PB)
// - zeroInstallSolver: substrate's pure-algorithm fast path
// - aspcudSolver: subprocess to external `aspcud` binary
type CUDFSolution struct {
    Install []CUDFSolutionPackage  // installed (pkg, version)
    Remove  []CUDFSolutionPackage  // to be removed from existing state
    Upgrade []CUDFSolutionPackage
    Downgrade []CUDFSolutionPackage
}
```

The adapter picks the solver based on config:

- Default: mccsSolver (no external deps, reasonable for typical resolves)
- CI / clean-env: zeroInstallSolver (faster when there's no
  existing install state to preserve)
- Complex optimization: aspcudSolver (requires aspcud installed)

### 5.3 Solve-then-translate

```go
func (a *OpamAdapter) Resolve(ctx context.Context, req substrate.ResolveRequest) (substrate.ResolveResult, error) {
    // 1. Load all candidate package .opam files (possibly filtered by Available).
    // 2. Translate to CUDF.
    // 3. Pick solver backend.
    // 4. Invoke solver.
    // 5. Translate solution back to substrate ResolveResult.
    // 6. Surface solver errors with explanations when possible.
}
```

### 5.4 OCaml compiler as primary constraint

The `ocaml` package is treated as any other package in the graph.
A typical resolution selects:

- `ocaml-base-compiler.4.14.1` (or similar)
- Then packages compatible with that compiler version

Users specify compiler version via:

- Existing switch's compiler
- `.opam` file's `depends: "ocaml" {>= "X"}` constraint
- Explicit `--compiler=4.14.1` flag

### 5.5 Frontier

OPAM's mccs solver is synchronous. Not implementing
`FrontierAwareResolver` by default.

However, the CUDF document can be built eagerly from cached metadata,
and **metadata fetches** (per-package .opam files) can happen in
parallel during the document-build phase. This is a different kind
of prefetching than PubGrub's frontier — it's pre-translation bulk
fetch.

## 6. Materializer

### 6.1 Switch layout

```
~/.opam/my-switch/
  .opam-switch/
    switch-config
    install/
      installed
      installed-roots
    install-files/
      lwt.install     # per-package install manifest
      ...
  bin/
    ocaml, ocamlfind, dune, ...
  lib/
    stublibs/
    ocaml/            # stdlib
    lwt/              # installed package
      ...
  share/
    ...
  man/
    ...
```

### 6.2 Package installation

Unlike most adapters, OPAM packages require **running build
commands** at install time (dune build, make, etc.). The adapter:

- Fetches source tarball to recipe store
- Extracts to switch-local build directory
- Invokes build commands from the .opam file (with sandboxing
  via substrate's subprocess runner)
- Invokes install commands (copies produced artifacts into switch)
- Updates the switch's install registry

This is expensive but unavoidable — OPAM doesn't ship precompiled
binaries for most packages (they're compiled against the specific
OCaml compiler version in the switch).

### 6.3 Substrate recipe store

For M3, per-switch compilation. For M4+, cache built packages
by (pkg, version, OCaml version, platform, flag-set) hash so
re-installs into a compatible switch reuse previously-built
artifacts. Analogous to Cargo's target/ cache but per-OCaml-version.

## 7. Lockfile

### 7.1 OPAM native lockfile (optional)

OPAM 2.1+ supports an opt-in lockfile format:

```
opam-version: "2.0"
name: "myproject"
version: "1.0.0"

depends: [
  "ocaml" {= "4.14.1"}
  "dune" {= "3.12.1"}
  "lwt" {= "5.7.0"}
  ...
]
```

Same `.opam` file format, but with pins instead of ranges. Stored
as `{project}.opam.locked`.

### 7.2 LockfileCodec

```go
type opamLockfileCodec struct{}
func (c *opamLockfileCodec) Ecosystem() string { return "ocaml" }
func (c *opamLockfileCodec) Filename() string { return "opam.locked" }
```

Sylk's substrate canonical lockfile is used internally; exported
as `.opam.locked` format for interop with OPAM 2.1+ tooling.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | **Not used** (CUDF-based, different algorithm) |
| `core/substrate/http` | Tarball downloads |
| `core/substrate/cache/metadata` | Per-package .opam caching |
| `core/substrate/store/recipe` | Source tarball + built-binary caches |
| `core/substrate/materializer` | Switch layouts |
| `core/substrate/lockfile` | opam.locked codec |
| `core/substrate/feeds` | Multiple opam repositories |
| `core/substrate/env` | Named-environment primitive for switches |
| `core/substrate/subprocess` | Build command execution with sandboxing |
| `core/substrate/git` | opam-repository cloning |

Adapter modules under `adapters/ocaml/`:

- `coordinate.go` — `OpamCoordinate`
- `version.go` — `OpamVersion` with DPKG-style comparison
- `opam_file.go` — .opam parser
- `filter.go` — filter parser + evaluator
- `url_file.go` — url file parser
- `repo.go` — git + HTTPS tree repo clients
- `cudf.go` — CUDF translation + document builder
- `mccs.go` — built-in mccs solver
- `zero_install.go` — 0install-style fast solver
- `aspcud.go` — aspcud subprocess solver
- `switches.go` — switch lifecycle
- `builder.go` — subprocess-based build command execution
- `lockfile.go` — opam.locked codec
- `adapter.go` — top-level Resolver

Estimated LOC: ~6,000. Complexity drivers:

- .opam parser + filter (~1500 LOC)
- CUDF translation (~500 LOC)
- mccs port (~2000 LOC; SAT/PB solver is non-trivial)
- 0install-style solver (~1000 LOC)
- Switch layouts + build orchestration (~800 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| No CUDF solution exists | `ErrNoSatisfyingVersion` | Surface solver's explanation (mccs gives partial; aspcud richer) |
| .opam file parse error | User error | Line-column info |
| Filter evaluation fails | `ErrInternalBug` | Unknown variable in filter |
| OCaml compiler constraint unsatisfiable | `ErrNoSatisfyingVersion` | "No compiler version satisfies X & Y constraints" |
| Build command fails | `ErrInternalBug` or user | Surface stderr |
| Checksum mismatch on tarball | `ErrIntegrityMismatch` | Fatal |
| External aspcud not installed | `ErrInternalBug` | Suggest `apt install aspcud` or fallback to mccs |

## 10. Security

### 10.1 Checksums

`url` files declare multiple checksums (SHA-256, SHA-512, MD5
legacy). The adapter verifies all declared algorithms on tarball
fetch.

### 10.2 git repo trust

opam-repository is a git repo; commits are implicitly trusted by
their SHA. For private repos, HTTPS TLS or SSH host key
verification protects the transport. No additional signing layer.

### 10.3 Build command security

Build commands are arbitrary shell. Run in substrate's subprocess
sandbox (filesystem scoped to switch dir; no network by default;
no sudo). Users needing network in build commands opt in per-package.

## 11. Testing

### 11.1 Unit tests

- OpamVersion parser + comparator (corpus from OPAM test suite)
- .opam parser on 100+ real .opam files from opam-repository
- Filter evaluator (matrix of FilterContext vs filter expressions)
- CUDF translation (golden outputs for known-small graphs)
- mccs solver (subset of MISC competition test cases)

### 11.2 Integration tests

- Resolve a project depending on dune + lwt + cmdliner
- Resolve requiring specific OCaml compiler
- Resolve with opam pin to local path
- Switch creation + use
- Build command execution (lwt's dune build)

### 11.3 Ecosystem compat

20 OCaml projects. Oracle: `opam install --show-actions`. Match the
set (not necessarily the order — multiple valid solutions can exist).

### 11.4 Performance

OCaml is a small ecosystem (~5000 packages); SAT overhead is
invisible for typical resolves.

- Resolve typical OCaml project: <2s cold, <300ms warm
- opam-repository incremental sync: <500ms
- mccs solve on 500-package graph: <200ms

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, typical project | <2s | <1s |
| Warm resolve | <300ms | <150ms |
| opam-repository git pull | <500ms | <250ms |
| .opam parse + filter eval | <5ms | <2ms |
| CUDF translation (500 packages) | <100ms | <50ms |
| mccs solve (500 packages) | <200ms | <100ms |
| Build + install (small package) | varies | varies |
| Peak memory, typical resolve | <300MB | <200MB |

## 13. Phases

**M0.** Types, .opam parser, filter evaluator; unit tests.

**M1.** opam-repository client (git + HTTPS). Per-package fetch.
Filter-aware filtering.

**M2.** CUDF translation. mccs solver port. End-to-end resolution.

**M3.** Switch materializer. Build command execution with
sandboxing. opam.locked codec. 20 ecosystem-compat projects green.

**M4.** 0install-style fast solver. Aspcud integration. Cross-switch
cache sharing for built artifacts. Vulnerability data.

## 14. Open Questions

- **mccs port complexity.** SAT solvers are hairy. Options: port
  mccs verbatim, use an existing Go SAT library (`gini`?), or
  implement only the subset OPAM actually uses. Proposal: port
  mccs core; reuse its optimization-criteria pattern.
- **Build artifact caching across switches.** A built lwt for OCaml
  4.14.1 on Linux x86_64 should be reusable across switches with
  the same parameters. OPAM itself doesn't do this; Sylk can.
  Proposal: cache by (pkg, version, ocaml version, platform, flag
  hash); materialize via hardlinks.
- **Subprocess sandbox for build commands.** Some packages need
  specific build environments (ocamlfind paths, env vars). Sandbox
  must allow these. Proposal: allow-list of common OPAM build
  variables; deny by default otherwise.
- **opam-repository as an append-only log.** Could use the git
  hash as the "snapshot version" analogous to Stackage for
  reproducibility. Proposal: pin opam-repository commit SHA in
  lockfile.

## 15. Dependencies

- Substrate M1 (HTTP, cache) → adapter M1
- Substrate M3 (materializer, subprocess, env primitive) → adapter M3

External Go dependencies:

- Custom .opam parser (~1500 LOC)
- mccs port (~2000 LOC)
- Possibly `github.com/crillab/gophersat` or `github.com/irfansharif/solver`
  for SAT inspiration; target: no external SAT dependency
- `encoding/json` — stdlib for CUDF document representation during
  debugging

No dependency on other adapters.
