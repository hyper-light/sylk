# GO_MODULES.md — Go Modules Adapter Implementation Plan

Tier 2 — validates the substrate handles non-PubGrub algorithms.
MVS (Minimum Version Selection) has no backtracking, no constraint
satisfaction, no candidate-consideration frontier in the
PubGrub sense. The substrate's `Resolver` interface was designed so
this is a first-class case, not a special case; this adapter proves
it.

## 1. Overview

The Go adapter resolves and materializes Go modules from:

- **[proxy.golang.org](https://proxy.golang.org/)** (default)
- **Alternative GOPROXY endpoints** (private corporate proxies,
  Athens, JFrog Artifactory's Go support)
- **`direct`** fallback (git clone + parse from source when proxy is
  unavailable)
- **Local module replacements** via `replace` directives
- **Vendored dependencies** when `vendor/` is present

Produces:

- A resolved dependency graph (MVS-selected) satisfying the
  project's `go.mod` requirements
- An updated `go.mod` reflecting resolved versions
- A `go.sum` with hashes for every selected module version
- A materialized `$GOPATH/pkg/mod`-compatible module cache
  (substrate-managed but layout-compatible with Go's native tools)

User-visible behaviors (M3 target):

- `sylk resolve go ./go.mod` → updated go.mod + go.sum
- `sylk install go` → modules materialized, ready for `go build`
- `sylk add go <module>@<version>` → modifies go.mod, re-resolves
- `sylk upgrade go [<module>]` → re-runs MVS with higher minimums
- `sylk why go <module>` → minimum-version justification chain

Non-goals:

- Executing `go build` (adapter ends at "modules on disk")
- Handling Go toolchain versions (separate substrate concern —
  toolchain is one of the "compiler-as-constraint" patterns that
  substrate primitives address)

## 2. Data Model

### 2.1 Coordinates

```go
// GoCoordinate is a module at a specific version.
type GoCoordinate struct {
    Path    string     // canonical module path, e.g. "github.com/gorilla/mux"
    Version GoVersion  // resolved version (never a range)
}

type GoVersion struct {
    Major     int
    Minor     int
    Patch     int
    Pre       string      // pre-release identifier
    Build     string      // build metadata
    PseudoVer *GoPseudoVersion // for v0.0.0-YYYYMMDDhhmmss-SHA
    Plus      bool        // +incompatible suffix
}

type GoPseudoVersion struct {
    BaseMajor, BaseMinor, BasePatch int // the semver "base" the pseudo version extends
    BasePre                         string // pre-release of base (e.g. pre.0.X)
    Timestamp                       time.Time
    CommitSHA                       string // 12-char abbreviated SHA
}
```

Pseudo-versions encode commits that aren't on tagged versions:

- `v0.0.0-20240101120000-abc123def456` — no base tag
- `v1.2.3-0.20240101120000-abc123def456` — extending v1.2.2
- `v1.2.4-0.20240101120000-abc123def456` — post-v1.2.3 pre-release

These are valid Go versions with full semver comparison semantics.

### 2.2 Major version in path

Go modules encode major version ≥ 2 into the module path:

- `github.com/gorilla/mux` — v0.x or v1.x
- `github.com/gorilla/mux/v2` — v2.x
- `github.com/gorilla/mux/v3` — v3.x

Semantically, `github.com/gorilla/mux` and
`github.com/gorilla/mux/v2` are **different modules**. A project can
depend on both simultaneously. The resolver treats them as
independent.

The `+incompatible` suffix exists for pre-modules Go code with
major versions ≥ 2 not following the path convention. E.g.
`github.com/foo/bar` at `v2.0.0+incompatible` means "this package
has a v2.0.0 git tag but doesn't use /v2 in its path." Handled
by the version parser and surfaced in error messages.

### 2.3 go.mod

```go
type GoMod struct {
    Module  string                 // module path of this project
    Go      string                 // minimum Go version (optional)
    Toolchain string                // toolchain directive (optional)
    Require []GoRequire
    Exclude []GoExclude
    Replace []GoReplace
    Retract []GoRetract
}

type GoRequire struct {
    Path    string
    Version GoVersion
    Indirect bool    // // indirect comment
}

type GoExclude struct {
    Path    string
    Version GoVersion  // excluded version
}

type GoReplace struct {
    OldPath string
    OldVersion GoVersion // zero-value means "any version"
    NewPath string       // might be same as OldPath for version replacement
    NewVersion GoVersion // zero-value means local path replacement
    NewPathIsLocal bool  // NewPath is a local directory, not a module path
}

type GoRetract struct {
    Low     GoVersion
    High    GoVersion
    Rationale string
}
```

### 2.4 go.sum

A two-hash record per module version:

```
github.com/gorilla/mux v1.8.1 h1:TuBL49tXwgrFYWhqrNgrUNEY92u81SPhu7sTdzQEiWY=
github.com/gorilla/mux v1.8.1/go.mod h1:AKf9I4AEqPTmMytcMc0KkNouC66V3BtZ4qD5fmWSiMQ=
```

Two hashes per (module, version):

- `h1:<base64-sha256>` — hash of the module tree (source files)
- `h1:<base64-sha256>` suffixed with `/go.mod` — hash of just
  the `go.mod` file

Both are verified: the first when materializing, the second when
reading module metadata during resolution.

## 3. HTTP Transport

### 3.1 GOPROXY protocol

Four endpoints per module:

```
GET {proxy}/{module-path}/@latest
# Returns: { "Version": "v1.8.1", "Time": "2023-11-20T..." }

GET {proxy}/{module-path}/@v/list
# Plain text, newline-separated list of available versions

GET {proxy}/{module-path}/@v/{version}.info
# JSON: { "Version": "v1.8.1", "Time": "2023-11-20T..." }

GET {proxy}/{module-path}/@v/{version}.mod
# Plain text: the module's go.mod file

GET {proxy}/{module-path}/@v/{version}.zip
# The module's source as a ZIP archive
```

Module paths are escaped per [Go module spec](https://go.dev/ref/mod#module-path)
— uppercase chars escaped as `!x`, e.g.
`github.com/BurntSushi/toml` → `github.com/!burnt!sushi/toml`.

### 3.2 GOPROXY chain

The `GOPROXY` environment variable is a comma- or pipe-separated list:

```
GOPROXY=https://proxy.golang.org,https://corp-proxy.example.com,direct
```

- `,` separator: fall back to next proxy on 404 or 410
- `|` separator: fall back on any non-200 error

`direct` means "clone the source repo directly" (for modules not
proxy-served). `off` means "disable all proxies" (only use
cache/replacements).

The substrate's `FeedReference` list models this directly; the Go
adapter's config translates `GOPROXY` env to an ordered
`[]FeedReference`.

### 3.3 GONOSUMCHECK / GONOVERIFY / GOSUMDB

`GOSUMDB=sum.golang.org` (default) enables transparency-log
verification: hashes observed during fetch are cross-checked against
the [Go checksum database](https://sum.golang.org/). Mismatches are
fatal — they indicate either a compromised proxy or source
tampering.

The adapter implements this as an optional post-fetch verification
step. Disabled via `GONOSUMCHECK=1`.

### 3.4 Authentication

Private modules are typically served via:

- **Private GOPROXY** with HTTP Basic or bearer auth
- **GOPRIVATE** pattern matching — modules matching the pattern
  skip the public proxy and go direct (for corporate
  internal modules served from private VCS):
  ```
  GOPRIVATE=*.corp.example.com,github.com/mycorp/*
  ```

Credentials for `direct` mode come from the user's git config
(typically `~/.gitconfig`, `~/.netrc`, SSH agent). The substrate's
git client handles this.

## 4. Metadata Layer

### 4.1 `.mod` file parsing

The `.mod` URL returns a `go.mod` file; parse with the same parser
used for the local `go.mod`:

```go
func ParseGoMod(data []byte) (*GoMod, error) { ... }
```

Go's `golang.org/x/mod/modfile` package has the reference
implementation. Vendor it or reimplement; reimplementation is
~500 LOC for the grammar and another ~500 for the AST + edit
support.

### 4.2 Version listing

`/@v/list` returns one version per line. Parse and filter:

```go
func ListVersions(ctx context.Context, adapter *GoAdapter, modulePath string) ([]GoVersion, error) {
    // 1. Fetch /@v/list from first proxy in chain.
    // 2. Parse each line as a GoVersion.
    // 3. Filter retracted versions (from the /@v/{latest}.mod).
    // 4. Sort in descending version order.
}
```

Retracted versions: if the latest tagged version's `go.mod`
declares `retract v1.2.3`, v1.2.3 is excluded from MVS
consideration (unless it's already in the current project's
go.mod, in which case it's honored with a warning — MVS never
changes existing pins involuntarily).

### 4.3 Cache keys

```
(ecosystem="go", name="github.com/gorilla/mux", version="v1.8.1", platform_hash="")
```

Platform is always empty for Go — the module's source is platform-
independent. Build-time specialization happens in `go build` via
build tags, not at module resolution.

## 5. Resolver

### 5.1 MVS algorithm

Minimum Version Selection, implemented directly. No substrate
PubGrub involvement.

```go
// ResolveMVS runs MVS starting from the project's go.mod.
//
// 1. Seed the "requirement map" from go.mod's require block.
// 2. For each module in the map, fetch its go.mod.
// 3. For each transitive require, update the map:
//    newMap[path] = max(existingMap[path], transitiveVer)
// 4. Iterate until no changes.
// 5. The map is the resolved set.
func ResolveMVS(ctx context.Context, adapter *GoAdapter, root *GoMod) (map[string]GoVersion, error) {
    selected := map[string]GoVersion{}
    queue := []GoRequire{}

    // Seed with root requires.
    for _, r := range root.Require {
        if !r.Indirect || adapter.treatIndirectAsSeed {
            queue = append(queue, r)
        }
    }

    for len(queue) > 0 {
        req := queue[0]
        queue = queue[1:]

        // Apply exclude: skip if this exact version is excluded.
        if root.IsExcluded(req.Path, req.Version) {
            continue
        }

        // Apply replace: rewrite the path/version if a replace rule matches.
        path, version := root.ApplyReplace(req.Path, req.Version)

        // Update selected: take the max of existing and this.
        if existing, ok := selected[path]; ok {
            if version.LessThan(existing) {
                continue // existing is already >= this; nothing new
            }
        }
        selected[path] = version

        // Fetch this module's go.mod, enqueue its requires.
        subMod, err := adapter.FetchModFile(ctx, path, version)
        if err != nil {
            return nil, err
        }
        for _, subReq := range subMod.Require {
            queue = append(queue, subReq)
        }
    }

    return selected, nil
}
```

Correctness points:

- **Max-of-minimums.** Selected version for each module is the
  maximum of all declared minimum versions.
- **No backtracking.** If a transitive declares `mod@v1.5` and
  another declares `mod@v1.3`, v1.5 is selected; v1.3 is ignored.
  This is the full extent of "conflict resolution."
- **Deterministic.** Given the same go.mod files, MVS always
  produces the same output regardless of traversal order.
- **`replace` is applied at every edge**, not just the root. A
  transitive's `replace` doesn't affect the root's resolution —
  only the root's `replace` directives apply.
- **`exclude` is applied at the root only** for the same reason.

### 5.2 No frontier

```go
func (a *GoAdapter) Ecosystem() string { return "go" }

func (a *GoAdapter) Resolve(ctx context.Context, req substrate.ResolveRequest) (substrate.ResolveResult, error) {
    // MVS runs synchronously. Fetching go.mod files can run in parallel
    // per-iteration, but there's no backtracking, no frontier events.
}

// Deliberately does NOT implement FrontierAwareResolver.
// MVS has no candidate-consideration frontier; the prefetch coordinator
// has nothing to do.
```

This is the canonical case for why `FrontierAwareResolver` is an
**optional extension** and not the base interface — MVS adapters
would be implementing `Frontier() <-chan FrontierEvent { return
nil }` otherwise, which is noise.

### 5.3 Parallel go.mod fetching

Within each MVS iteration, all newly-discovered module paths' `.mod`
files can be fetched concurrently. Use the substrate's HTTP client
with bounded concurrency (50, same as other adapters).

```go
func (a *GoAdapter) fetchModFilesParallel(ctx context.Context, modules []GoCoordinate) (map[string]*GoMod, error) {
    // Use errgroup with bounded concurrency.
    // Each goroutine fetches one .mod file.
    // Return map keyed by module path.
}
```

For typical projects, MVS converges in 3–5 iterations; each
iteration has tens to hundreds of modules to fetch in parallel.
Total resolve time is dominated by network latency, which
parallelism largely hides.

### 5.4 `replace` directive handling

Three forms of `replace`:

```
replace github.com/foo/bar => github.com/foo/bar-fork v1.2.3
# Redirect a module to another module path at a specific version.

replace github.com/foo/bar v1.0.0 => github.com/foo/bar v1.0.1
# Replace only a specific version; v1.0.0 is redirected to v1.0.1.

replace github.com/foo/bar => ../local-bar
# Local path replacement (commonly used in workspaces).
```

The adapter applies these rewrites **at every MVS edge traversal**:
when enqueueing a require, check if the root's replace directives
match, and if so, substitute.

Local path replacements short-circuit the proxy entirely — the
adapter reads `go.mod` from the local directory.

### 5.5 go.work (workspace mode)

Go 1.18+ supports multi-module workspaces via `go.work`:

```
go 1.22

use (
    ./module-a
    ./module-b
)

replace github.com/foo/bar => ./vendored-bar
```

When `go.work` exists, MVS considers the union of all listed
modules' dependency requirements. Workspace-level replace
directives override individual modules'. The adapter:

- Reads `go.work` in preference to the current directory's `go.mod`
  (workspace mode takes precedence)
- Uses all `use` modules as resolution roots simultaneously
- Applies workspace-level replaces as an additional layer

## 6. Materializer

### 6.1 Module cache layout

The substrate's recipe store mirrors Go's canonical cache layout so
`go build` can consume it:

```
{recipe-store}/go/
  pkg/
    mod/
      cache/
        download/
          {module-path}/
            @v/
              {version}.info
              {version}.mod
              {version}.zip
              {version}.ziphash
      {module-path}@{version}/   # extracted module tree
```

When the adapter is the only module manager (fresh install), we
create this layout directly. When an existing GOPATH is in use,
we populate missing entries and leave existing ones alone.

### 6.2 Zip extraction

Each `.zip` extracts to `{module-path}@{version}/`:

```go
func ExtractModuleZip(zipPath, destDir string) error {
    // 1. Open zip.
    // 2. Verify content hash against the ziphash file.
    // 3. Extract files. Module zips have a canonical format:
    //    - Top-level directory name is {module-path}@{version}.
    //    - All paths are prefixed with that directory name.
    //    - Strip the prefix; extract into destDir.
    // 4. Set read-only permissions (Go's convention — the module cache is
    //    immutable).
}
```

### 6.3 Verified fetch

Materialization always verifies hashes:

1. Fetch `.zip`
2. Compute SHA-256; compare to `h1:...` hash in go.sum
3. Mismatch → abort, report as `ErrIntegrityMismatch`
4. Extract zip
5. Compute module tree hash (per [`golang.org/x/mod/sumdb/dirhash`](https://pkg.go.dev/golang.org/x/mod/sumdb/dirhash))
6. Compare to go.sum's module tree hash
7. Update go.sum with hashes for modules not yet listed

The module tree hash is a specific algorithm:
`H1:base64(sha256(file1:hash1 file2:hash2 ...))` where files are
in sorted order. Vendor from `golang.org/x/mod/sumdb/dirhash`.

### 6.4 Sumdb verification

When `GOSUMDB=sum.golang.org` (default), additionally verify every
new hash against the transparency log:

```go
func VerifyAgainstSumdb(ctx context.Context, sumdb string, module GoCoordinate, hashes Hashes) error {
    // 1. Look up (module, version) in the sumdb HTTP API.
    // 2. Retrieve the tree-head + inclusion proof.
    // 3. Verify the proof against our locally-cached sumdb state.
    // 4. Compare the sumdb's hashes to what we observed.
    // 5. Mismatch → ErrIntegrityMismatch.
}
```

The sumdb is an append-only Merkle tree; the adapter caches the
tree head and periodically re-verifies a subset of known entries
to catch retroactive tampering.

### 6.5 Vendoring

`go mod vendor` produces a `vendor/` directory containing every
dependency's source, for offline / reproducible builds. The
adapter supports this as an opt-in materialization mode:

```go
func (m *goMaterializer) VendorAll(ctx context.Context, dst string, resolution substrate.ResolveResult) error {
    // For each resolved module:
    //   - Copy (reflink/hardlink) its source tree into vendor/{module-path}/
    //   - Generate vendor/modules.txt listing all vendored modules + versions
    // Emit the exact format `go mod vendor` produces — verified against real projects.
}
```

## 7. Lockfile

### 7.1 go.sum as lockfile

Go's lockfile is `go.sum` — content hashes for every resolved
module. Combined with `go.mod`'s explicit version requirements,
this is effectively a full lockfile.

The substrate's `LockfileCodec`:

```go
type goLockfileCodec struct{}

func (c *goLockfileCodec) Ecosystem() string { return "go" }
func (c *goLockfileCodec) Filename() string  { return "go.sum" }
func (c *goLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) {
    // Parse go.sum: "module version [/go.mod] h1:base64hash"
    // Produce LockfileSnapshot; each entry has both the module-tree and go.mod hashes.
}
func (c *goLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Sort entries: first by module path, then by version, then the
    // /go.mod variant after the bare module entry.
    // Emit newline-separated lines matching Go's format exactly.
}
```

### 7.2 go.mod also carries version information

`go.mod`'s `require` directives pin versions. The adapter treats
this as *both* input (constraints for MVS) and output (updated
after resolution). `go.mod` round-trip must preserve:

- Comments (especially the `// indirect` flags on transitive deps)
- Block structure (grouped requires vs. individual requires)
- User-facing formatting (whitespace, alignment)

This is more involved than typical lockfile round-trips. Use
`golang.org/x/mod/modfile`'s AST-preserving editor or implement
equivalent functionality (~500 LOC including edit operations).

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | **Not used** (this adapter has native MVS) |
| `core/substrate/http` | GOPROXY fetches |
| `core/substrate/cache/metadata` | go.mod and .info caching |
| `core/substrate/store/recipe` | Module .zip storage and extracted sources |
| `core/substrate/materializer` | Vendor mode linking |
| `core/substrate/lockfile` | go.sum codec |
| `core/substrate/feeds` | GOPROXY chain |
| `core/substrate/auth` | Private proxy credentials; git credentials for direct mode |
| `core/substrate/frontier` | **Not used** |
| `core/substrate/git` | `direct` fallback; `replace` to local paths |

Adapter modules under `adapters/go/`:

- `coordinate.go` — `GoCoordinate`
- `version.go` — `GoVersion` including pseudo-versions
- `modfile.go` — go.mod parser (port `golang.org/x/mod/modfile`)
- `sumfile.go` — go.sum parser/writer
- `goproxy.go` — GOPROXY client (all four endpoints)
- `resolver.go` — MVS implementation
- `replace.go` — replace/exclude/retract handling
- `workspace.go` — go.work support
- `dirhash.go` — module tree hash (port `golang.org/x/mod/sumdb/dirhash`)
- `sumdb.go` — checksum database verification
- `zipfile.go` — module zip extraction
- `vendor.go` — vendor/ generation
- `materializer.go` — module cache construction
- `adapter.go` — top-level Resolver

Estimated LOC: ~4,000, substantial fraction of which is parser code
(go.mod, go.sum) that closely mirrors `golang.org/x/mod`.

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Module not found on any proxy | `ErrNoSuchRecipe` | List all proxies tried |
| Version not in listing | `ErrNoSatisfyingVersion` | "Module X has versions [A, B, C]; requested D not found" |
| h1 hash mismatch | `ErrIntegrityMismatch` | **Always fatal.** Possible attack or corrupt mirror |
| sumdb verification fails | `ErrIntegrityMismatch` | Possibly retroactive tampering |
| `replace` to missing path | `ErrNoSuchRecipe` | Local path doesn't exist |
| `replace` target incompatible module path | `ErrCapabilityConflict` | Replacement's go.mod declares a different module path |
| Retracted version in use | (warning, not error) | Honor existing pin but warn |
| go.mod parse error | `ErrInternalBug` or user error | Detailed line/column error messages |
| Pseudo-version commit not in repo | `ErrNoSuchRecipe` | Git commit gone (force-push) |
| go.work / go.mod precedence ambiguous | User error | Clear message about workspace mode |

## 10. Security

### 10.1 Hash verification

Every module fetch verifies two hashes:

1. **Module tree hash** (`h1:...`) — computed over the extracted
   source tree, verified against go.sum.
2. **go.mod hash** (`h1:...` suffixed with `/go.mod`) — computed
   over just the go.mod file.

Both are required. Both must match go.sum entries when present. A
missing entry (new dependency not yet in go.sum) triggers
`GOSUMCHECK=0` mode — allowed on first use, then the hash is
recorded.

### 10.2 sumdb transparency log

`sum.golang.org` maintains a Merkle tree of every hash Go's ecosystem
has observed. The adapter verifies new hashes against the log and
periodically audits existing hashes (re-fetches the proof and
verifies tree head consistency).

Corporate deployments can point `GOSUMDB` at a private transparency
log or set `GOSUMDB=off` (discouraged — forfeits the tamper-
detection guarantee).

### 10.3 GOPRIVATE pattern

Modules matching `GOPRIVATE` patterns skip:

- The public proxy (never fetched from proxy.golang.org)
- The sumdb (hashes never submitted to sum.golang.org)

This is essential for private corporate modules. The adapter
respects `GOPRIVATE` semantics identically to Go's native tooling.

### 10.4 direct mode git client security

`direct` mode clones source repositories. Security concerns:

- **TLS verification** for HTTPS — enforced, no insecure override
- **SSH host key verification** — substrate's git client validates
  against user's `known_hosts`
- **Git path traversal** — the module zip extractor sanitizes paths
  to prevent `../` escapes

### 10.5 Retract and vulnerability advisories

Module authors can declare retractions:

```
retract v1.5.0 // has CVE-2024-XXXX
retract [v1.0.0, v1.4.9] // pre-retraction versions superseded by v1.5.1
```

Retractions are advisory — the resolver honors them by skipping
retracted versions during MVS but preserves existing go.mod pins
with a warning ("your go.mod requires v1.5.0 which has been
retracted: see CVE-2024-XXXX").

Integration with [Go vulnerability database](https://pkg.go.dev/vuln/)
as a separate security layer: known-vulnerable versions surface as
`CapabilityConflict` with severity=high.

## 11. Testing

### 11.1 Unit tests

- `GoVersion` parser + comparator — corpus from
  [golang.org/x/mod](https://pkg.go.dev/golang.org/x/mod)
- `go.mod` parser — corpus from real projects (hundreds of
  go.mod files)
- `go.sum` parser/writer — round-trip 50+ real go.sum files
- Module path escaping — Unicode edge cases, uppercase handling
- Pseudo-version parsing — every canonical form + error cases
- Dirhash computation — golden values from Go's own tests
- MVS algorithm — handcrafted test graphs with expected outputs

### 11.2 Integration tests

- Resolve kubernetes/kubernetes (enormous dep graph, ~2000 modules)
- Resolve a project with `replace` to a local path
- Resolve with GOPROXY chain (primary unavailable, fall back to
  secondary)
- Resolve using a go.work workspace with 5 modules
- Resolve a project with `+incompatible` dependency
- Resolve a project with pseudo-version dependencies
- sumdb verification end-to-end (against sum.golang.org or a
  test-injected transparency log)

### 11.3 Ecosystem compatibility

Golden corpus of ~100 Go projects. Our MVS result must byte-match
`go mod tidy` output. Any divergence is a bug. This is the highest-
accuracy compat requirement in this doc because MVS is
deterministic — divergence always indicates an implementation
error.

### 11.4 Performance

- Resolve kubernetes/kubernetes: <15s cold, <3s warm
- Resolve a 100-module project: <2s cold, <200ms warm
- Materialize a 100-module cache: <1s
- dirhash of a 1MB module tree: <50ms
- sumdb verification of a new hash: <200ms

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, 100 modules | <2s | <1s |
| Warm resolve, 100 modules | <200ms | <100ms |
| Cold resolve, 2000 modules (k8s) | <15s | <10s |
| `.mod` fetch (cache miss) | <100ms | <50ms |
| `.mod` fetch (cache hit, 304) | <10ms | <5ms |
| Module zip extraction (typical) | <100ms | <50ms |
| Dirhash, 10MB module | <200ms | <100ms |
| sumdb verification (per hash) | <200ms | <50ms |
| go.mod round-trip | <20ms | <10ms |
| go.sum round-trip | <50ms | <20ms |
| Peak memory, 2000-module resolve | <400MB | <200MB |

## 13. Phases

**M0.** Types compile; `GoVersion`, `GoMod`, `GoSum` parsers have
unit tests passing.

**M1.** GOPROXY client works against proxy.golang.org. Module zip
extraction with hash verification works. Can fetch and parse
arbitrary module metadata.

**M2.** MVS end-to-end for a canonical project (e.g. stdlib
adjacent module). Replace/exclude/retract handled. Parallel
metadata fetching within iterations.

**M3.** sumdb verification. Full vendoring support. go.work support.
Ecosystem-compat green on top 100 projects. Performance targets
met.

**M4.** direct mode (git fallback). Vulnerability database
integration. Telemetry. Production polish.

## 14. Open Questions

- **go.mod round-trip fidelity.** `golang.org/x/mod/modfile` is the
  reference. Re-implementing to avoid cgo and version drift has
  cost; vendoring it may be simpler. Proposal: vendor the reference
  as a substrate-level dependency so all go-adjacent work shares.
- **sumdb caching strategy.** The transparency log is append-only
  with a 20MB tree head file. Cache aggressively (hours) but
  periodically re-verify. Exact refresh intervals need empirical
  data.
- **Workspace + replace interactions.** go.work and go.mod both
  have replace directives; precedence is workspace > module > nil.
  Edge cases around conflicting replaces need explicit tests.
- **Toolchain directive.** Go 1.21+ added `toolchain go1.22` in
  go.mod to request a specific Go version. The adapter reads this
  as informational; the substrate's toolchain-management layer
  (separate concern) acts on it.
- **Incompatible support.** `+incompatible` is a compatibility
  shim for pre-modules Go code. Increasingly rare but still exists.
  Test coverage required; no special resolver logic needed.
- **Proxying vs direct fallback semantics.** When GOPROXY chain
  ends in `direct`, fallback to git. When it ends in `off`, refuse.
  This is well-specified but error-prone to implement correctly
  around partial failures (proxy 502 vs 404 vs connection refused).

## 15. Dependencies

- **Substrate M1** (HTTPClient, cache) → adapter M1
- **Substrate M2** (multi-feed federation) → adapter M2 (for
  GOPROXY chain)
- **Substrate M3** (materializer, lockfile, git) → adapter M3

No substrate PubGrub dependency (MVS is native).

External Go dependencies beyond substrate:

- `golang.org/x/mod/modfile` — vendored; go.mod AST + parser
- `golang.org/x/mod/module` — vendored; module path parsing +
  escaping
- `golang.org/x/mod/sumdb/dirhash` — vendored; module tree hash
  algorithm
- `golang.org/x/mod/sumdb` — vendored; sumdb client

Vendoring `golang.org/x/mod` is cleanest; it's a small well-tested
library and matches Go tool chain semantics exactly. Worth the
vendoring cost vs. reimplementation risk.
