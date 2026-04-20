# NODE_NPM.md — Node / npm Adapter Implementation Plan

Tier 2 finish — **the hardest adapter in this doc.** npm's constraint
model doesn't fit PubGrub or MVS — peer dependencies require a
custom resolver with multi-pass convergence. The protocol serves
mega-JSON registry responses (10 MB+ per package) that demand
streaming parsing. The materialization layout (hoisted or nested
`node_modules`) interacts with the constraint model in ways every
other ecosystem avoids.

This adapter aims to match Bun's performance characteristics (same
protocol, same constraints, ~10–30× faster than npm CLI) while
producing `package-lock.json` byte-identical to npm's output for
ecosystem compatibility.

## 1. Overview

The Node adapter resolves and materializes packages from:

- **[registry.npmjs.org](https://registry.npmjs.org/)** (default)
- **npm-compatible private registries** (Verdaccio, GitHub Packages,
  JFrog Artifactory, AWS CodeArtifact, corporate Nexus)
- **Scoped registries** (`@myorg:registry=https://npm.myorg.com/`)
- **Git URLs** (`foo: git+https://...#tag`)
- **Tarball URLs** (`foo: https://example.com/foo-1.0.0.tgz`)
- **Path dependencies** (`foo: file:../local/`)

Produces:

- A resolved dependency tree satisfying semver constraints **and**
  peer dependency requirements
- A `package-lock.json` v3 (npm 9+ format) pinning every package
- A materialized `node_modules/` tree (hoisted by default; optional
  isolated mode via symlinks, pnpm-style)
- Lifecycle script execution in dependency order (preinstall,
  install, postinstall)

User-visible behaviors (M3 target):

- `sylk resolve node ./package.json` → package-lock.json
- `sylk install node` → materialized node_modules, lifecycle scripts
  run
- `sylk add node <package>[@<range>]` → updates package.json,
  re-resolves
- `sylk upgrade node [<package>]` → re-resolves, updates lockfile
- `sylk why node <package>` → resolver-derived explanation

Non-goals:

- Running Node.js itself (adapter ends at "node_modules is on disk")
- Supporting pnpm's symlink layout as default (add as opt-in mode
  later)
- TypeScript type resolution / @types/* matching (runtime concern,
  not resolver concern)

## 2. Data Model

### 2.1 Coordinates

```go
type NpmCoordinate struct {
    Scope      string     // "@myorg" or empty
    Name       string     // without scope prefix
    Version    SemVer     // exact resolved version (never a range)
    Source     NpmSource
    RequestedFeatures PackageFeatures // dev, optional, peer context
}

func (n NpmCoordinate) FullName() string {
    if n.Scope != "" {
        return n.Scope + "/" + n.Name
    }
    return n.Name
}

type NpmSource struct {
    Kind       NpmSourceKind // Registry, Git, Tarball, File, Workspace
    Registry   string        // when Kind=Registry
    Git        GitReference  // when Kind=Git
    TarballURL string        // when Kind=Tarball
    FilePath   string        // when Kind=File
    Integrity  string        // SRI-format hash (sha512-...)
}

type PackageFeatures struct {
    IsDev       bool
    IsOptional  bool
    IsPeer      bool
    IsWorkspace bool
}
```

### 2.2 package.json

```go
type PackageJSON struct {
    Name     string
    Version  string
    Private  bool

    Dependencies           map[string]string  // "^1.0.0"
    DevDependencies        map[string]string
    PeerDependencies       map[string]string
    PeerDependenciesMeta   map[string]PeerDepMeta
    OptionalDependencies   map[string]string
    BundleDependencies     []string
    OverridesJSON          json.RawMessage    // overrides block (complex)

    Workspaces             Workspaces

    Scripts                map[string]string

    Engines                map[string]string  // "node": ">=18"
    OS                     []string           // ["linux", "darwin"]
    CPU                    []string           // ["x64", "arm64"]

    Publish config, etc (not relevant to resolver)
}

type PeerDepMeta struct {
    Optional bool
}

type Workspaces struct {
    Packages []string  // glob patterns
    Nohoist  []string  // legacy
}
```

### 2.3 Registry response

The npm registry's packument is a single JSON document per package,
containing metadata for every published version:

```json
{
  "name": "react",
  "dist-tags": { "latest": "18.2.0", "next": "18.3.0-canary-...", "beta": "..." },
  "versions": {
    "0.0.1": { "name": "react", "version": "0.0.1", "dependencies": {...}, "dist": {...} },
    "0.0.2": {...},
    ...,
    "18.2.0": {...}
  },
  "time": { "0.0.1": "2011-08-04T...", "modified": "2024-01-15T..." },
  "maintainers": [...],
  "readme": "..."
}
```

For popular packages, the full packument can be 10 MB+. Each
version entry has the full dependency set, dist info, etc. The
`versions` object can contain thousands of entries.

**Streaming parse is mandatory.** `encoding/json.Unmarshal` into a
full struct buffers the whole document and allocates heavily.

### 2.4 package-lock.json v3

```go
type PackageLockJSON struct {
    Name             string
    Version          string
    LockfileVersion  int  // 3
    Requires         bool // true for v3
    Packages         map[string]LockPackage // keyed by path in node_modules, "" for root
    Dependencies     map[string]LockDep     // deprecated v1 compat, emitted for old-tool compat
}

type LockPackage struct {
    Version       string
    Resolved      string   // tarball URL
    Integrity     string   // "sha512-..." SRI
    Dev           bool
    Optional      bool
    DevOptional   bool
    PeerOptional  bool
    Inbundle      bool
    HasInstallScript bool
    HasShrinkwrap bool
    Engines       map[string]string
    OS            []string
    CPU           []string
    Bin           map[string]string
    License       string
    Dependencies  map[string]string  // constraint → range (preserved from package.json)
    DevDependencies map[string]string
    PeerDependencies map[string]string
    PeerDependenciesMeta map[string]PeerDepMeta
    OptionalDependencies map[string]string
}
```

Path keys in `Packages`:

- `""` — the root project
- `"node_modules/react"` — hoisted `react`
- `"node_modules/foo/node_modules/react"` — nested `react` (when
  hoisting conflict forced nesting)
- `"packages/workspace-a"` — workspace member (no `node_modules/`
  prefix)

This path-keyed structure is what lets npm recreate the exact
`node_modules` tree from the lockfile alone.

## 3. HTTP Transport

### 3.1 Registry protocol

The npm registry serves three endpoints per package:

```
# Full packument (historical, still primary):
GET https://registry.npmjs.org/{package}

# Abbreviated metadata (lighter, since npm 5):
GET https://registry.npmjs.org/{package}
Accept: application/vnd.npm.install-v1+json

# Single version:
GET https://registry.npmjs.org/{package}/{version}
```

The abbreviated metadata format (`install-v1`) strips fields the
resolver doesn't need (README, maintainer info, etc.) — typical 2–5×
size reduction. **Always use `install-v1` accept header** unless the
registry doesn't support it (fall back on 406).

For tarballs:

```
GET https://registry.npmjs.org/{package}/-/{package}-{version}.tgz
```

### 3.2 Streaming JSON parser

Naive `encoding/json.Unmarshal` of a 10 MB react packument takes
~200ms and allocates ~100 MB of intermediate objects. Bun's
SIMD-accelerated parser does the same work in ~20 ms. Go's ecosystem
has `github.com/valyala/fastjson` which streams without allocating
intermediate structs.

```go
// FetchPackument fetches and parses a packument, extracting only the
// fields the resolver needs. Never buffers the full response in
// memory — streams parse directly.
func (n *NpmAdapter) FetchPackument(ctx context.Context, name string) (*Packument, error) {
    resp, err := n.httpClient.Get(ctx, packumentURL(name), WithAccept("application/vnd.npm.install-v1+json"))
    if err != nil { return nil, err }
    defer resp.Body.Close()

    // Stream-parse the JSON. For each `versions.*.key`, extract only
    // needed fields: version, dependencies, devDependencies,
    // peerDependencies, peerDependenciesMeta, optionalDependencies,
    // dist, engines, os, cpu, deprecated.
    return streamParsePackument(resp.Body)
}

type Packument struct {
    Name     string
    DistTags map[string]string  // "latest", "next", etc.
    Versions map[string]*PackumentVersion  // version string → version metadata
    Modified time.Time  // "modified" from time block, for cache invalidation
}

type PackumentVersion struct {
    Version              SemVer
    Dependencies         map[string]string
    DevDependencies      map[string]string
    PeerDependencies     map[string]string
    PeerDependenciesMeta map[string]PeerDepMeta
    OptionalDependencies map[string]string
    BundleDependencies   []string
    Dist                 PackumentDist
    Engines              map[string]string
    OS                   []string
    CPU                  []string
    Deprecated           string  // non-empty if deprecated
    HasInstallScript     bool    // has preinstall/install/postinstall
    Bin                  map[string]string
}

type PackumentDist struct {
    Tarball   string
    Integrity string  // "sha512-..." (preferred)
    Shasum    string  // SHA-1 (legacy, must still verify)
}
```

Streaming approach using `fastjson`:

```go
func streamParsePackument(r io.Reader) (*Packument, error) {
    p := &fastjson.Parser{}
    scanner := &fastjson.Scanner{}
    scanner.InitReader(r)
    for scanner.Next() {
        v := scanner.Value()
        // Extract top-level fields (name, dist-tags, time, versions).
        // For versions, iterate each version key and extract needed fields only.
        // Never materialize the full object graph.
    }
    return pkg, nil
}
```

This reduces a 10 MB packument parse from ~200ms to ~25ms while
using <5 MB of peak memory.

### 3.3 ETag and conditional requests

npm registry honors `If-None-Match` with Etags. The adapter's
substrate MetadataCache stores the Etag and issues conditional
requests — 304 responses are free cache hits.

For the abbreviated endpoint specifically, the Etag changes
whenever any version is added or modified; updates are very
frequent for popular packages (react, lodash, etc.), so cache TTL
shouldn't be too aggressive. Proposal: 10-minute soft TTL with Etag
revalidation, 24-hour hard TTL.

### 3.4 Authentication

npm auth tokens:

- **Legacy**: `_authToken` in `.npmrc` — sent as
  `Authorization: Bearer <token>`
- **Modern (NPM_TOKEN env)**: same format
- **Per-registry**: `//registry.scope.example.com/:_authToken=...`
- **`_auth` (base64 user:pass)**: HTTP Basic; legacy

The adapter reads `~/.npmrc` and `./.npmrc` in that order (project
overrides user), honors `NPM_CONFIG_REGISTRY`, `NPM_TOKEN`, and
handles scoped registry auth: `@scope:registry=...` pairs a scope
with a specific registry and uses that registry's token.

## 4. Metadata Layer

### 4.1 Scoped registries

```
.npmrc:
@mycompany:registry=https://npm.mycompany.com/
@myorg:registry=https://registry.npmjs.org/

registry=https://registry.npmjs.org/
//registry.npmjs.org/:_authToken=<public-token>
//npm.mycompany.com/:_authToken=<private-token>
```

Behavior:

- `@mycompany/foo` → fetched from npm.mycompany.com
- `@myorg/bar` → fetched from registry.npmjs.org (explicit scope
  override)
- `baz` (unscoped) → fetched from the default registry

The adapter implements this via multi-feed federation — each scope
registry is a `FeedReference` with a scope-name filter. The feed
list is walked for each package lookup; the first feed whose scope
filter matches is used.

### 4.2 dist-tags resolution

```json
"dist-tags": {
    "latest": "18.2.0",
    "next": "18.3.0-canary-...",
    "beta": "18.3.0-beta.0"
}
```

Dependencies can reference dist-tags: `"react": "latest"` means
"whatever version `latest` currently points to." The adapter resolves
tags to concrete versions at dependency discovery time, pinning the
exact version into the resolver's candidate set.

The `latest` tag is special: when a constraint is a tag name, the
adapter treats the resolved version as a hard pin (no version range
semantics). This is npm's behavior.

### 4.3 Version ranges

npm uses semver ranges:

- `^1.2.3` — >=1.2.3, <2.0.0 (pre-1.0.0 behaves differently;
  caret-1.x locks the minor)
- `~1.2.3` — >=1.2.3, <1.3.0
- `>=1.2.3 <2.0.0` — explicit boolean range
- `1.2.x` — any patch of 1.2
- `*` or empty — any version
- `http://...` — URL dependency
- `git+https://...#tag` — git dependency
- `file:...` — path dependency

Parser is a small PEG grammar. Use
[Masterminds/semver](https://github.com/Masterminds/semver) or
implement natively; native is preferred because npm has subtle
pre-release handling that generic semver libraries often get wrong
(specifically: pre-release versions don't match caret/tilde ranges
unless the range itself includes a pre-release).

### 4.4 Cache keys

```
(ecosystem="npm", name="@scope/react", version=*, platform_hash="")
```

Scoped and unscoped packages live under the same ecosystem key; the
full scoped name is the `name` field. Platform hash is empty because
npm packages are typically platform-independent; packages with
`os`/`cpu` restrictions filter by platform compatibility at candidate
selection, not via separate cache entries.

## 5. Resolver

This is the non-trivial part. npm's constraint model requires a
**custom resolver** — neither PubGrub nor MVS fits.

### 5.1 Why not PubGrub

PubGrub models each constraint as a term (package → version range).
Peer dependencies don't fit cleanly because:

1. **Peer deps have context-dependent semantics.** A package A
   declares `peerDependencies: { react: ^17.0.0 }`. A consuming
   project must provide a `react` somewhere in scope; A doesn't
   install it. The "somewhere in scope" is determined by
   `node_modules` placement, which is a materialization concern, not
   a resolution concern — but peer satisfaction depends on where the
   peer ends up being materialized.

2. **Same package, different placements.** If two consumers of A
   provide different react versions satisfying A's peer range, A
   gets duplicated at both placements with different react contexts.
   PubGrub produces one version per package globally; npm produces
   multiple ("doppelgangers").

3. **Optional peer semantics.** `peerDependenciesMeta.X.optional:
   true` means "use X if it's there, fine if it isn't." PubGrub has
   no first-class model for "optional presence."

### 5.2 The Arborist algorithm (simplified)

The resolver runs in phases:

**Phase 1: Ideal tree construction.** Build a logical dependency
tree honoring `dependencies`, `devDependencies` (root only),
`optionalDependencies`. Use semver range satisfaction with
preference for newest satisfying version. Each node in the tree is
`(name, version, parent)`. Root is the project itself.

**Phase 2: Peer propagation.** For each node, resolve its
`peerDependencies`. A peer must be satisfied by a node reachable
from an ancestor (closer-to-root). If no satisfying ancestor exists,
insert the peer at the ancestor closest to root that would satisfy
(usually the peer gets hoisted to near-root).

**Phase 3: Hoisting.** Place each node in the physical layout as
close to the root as possible without conflict. A package at
`(parent=A)` hoists to `root` if no conflicting version of the same
name is already at `root`. Otherwise stays nested at `A/node_modules/`.

**Phase 4: Re-validate peers.** Hoisting can shift what's in scope
for each node. Re-check every peer dependency against the physical
layout. Peers that were satisfied before hoisting may no longer
be, requiring insertion of additional nodes or duplication of
existing nodes.

**Phase 5: Fixed-point iteration.** Re-run peer propagation +
hoisting until no changes. Convergence is guaranteed because the
tree can only grow (or stay the same), not shrink.

```go
type resolver struct {
    root *Node
    registry registryClient
    overrides *Overrides
    options ResolveOptions
}

type Node struct {
    Name       string
    Version    SemVer
    Parent     *Node
    Children   map[string]*Node  // name → node
    Package    *PackumentVersion
    IsPeer     bool
    IsOptional bool
    IsDev      bool
    IsDoppelganger bool  // multiple copies of same (name, version) in tree
    PlacementPath string  // final location in node_modules
}

func (r *resolver) Resolve(ctx context.Context) (*Tree, error) {
    // Phase 1: ideal tree
    r.buildIdealTree(ctx)

    // Phase 2-5: fixed-point
    for iter := 0; iter < maxPeerIterations; iter++ {
        changed := false

        // Phase 2: peer propagation
        changed = r.propagatePeers(ctx) || changed

        // Phase 3: hoisting
        changed = r.hoist(ctx) || changed

        // Phase 4: peer validation
        changed = r.validatePeers(ctx) || changed

        if !changed {
            return r.tree(), nil
        }
    }
    return nil, fmt.Errorf("peer dependency resolution did not converge after %d iterations", maxPeerIterations)
}
```

### 5.3 Peer dependency semantics (detail)

`peerDependencies` declares constraints the **consumer** must
satisfy. `peerDependenciesMeta.X.optional: true` makes the peer
optional (consumer doesn't have to provide; package degrades
gracefully).

Strict peer mode (default in npm 7+):

- Missing required peer → warning (install proceeds)
- Conflicting required peers → error (fail resolution)
- Optional peer not provided → silent

Non-strict mode (legacy):

- All peer issues → warnings only

Sylk defaults to strict; configurable via
`ResolveOptions.PeerDependencyMode`.

### 5.4 Overrides

npm 8+ supports `overrides` in package.json:

```json
"overrides": {
    "foo": "2.0.0",
    "bar": {
        ".": "1.5.0",
        "nested-pkg": "3.0.0"
    }
}
```

Override syntax:

- `"foo": "2.0.0"` — force all `foo` in the graph to 2.0.0
- `"foo": { ".": "2.0.0", "bar": "1.0" }` — force foo=2.0.0, and
  within foo's subtree, force bar=1.0
- Nested keys with `$` prefix reference parent scope

Overrides are applied during Phase 1 — before any version is
considered, if an override matches, the candidate set is restricted
to the override's version.

### 5.5 Workspaces

npm workspaces: a project with `workspaces: ["packages/*"]` in
package.json treats every matching directory as a linked workspace
member. Workspace members can depend on each other; these are
resolved as local path references, not fetched from the registry.

```go
func (r *resolver) resolveWorkspaces(ctx context.Context, ws Workspaces) ([]*WorkspaceMember, error) {
    // Expand glob patterns.
    // For each member: read its package.json.
    // Register the member as a synthetic "package" available for
    // resolution.
    // A consumer's `"my-utility": "^1.0"` can match either the
    // workspace member or a registry package; workspace member wins
    // when versions align.
}
```

### 5.6 No frontier

Arborist's multi-pass algorithm doesn't expose a candidate-
consideration frontier in the PubGrub sense. Candidates are
considered in waves (one per phase), not incrementally.

The adapter does NOT implement `FrontierAwareResolver`. Instead, it
does **bulk prefetching**: before Phase 1 starts, the adapter
fetches the packuments for every direct dependency declared in
package.json in parallel (typically 20-100 packages). During Phase 1,
as new transitive dependencies are discovered, their packuments are
fetched in batches (one batch per Phase 1 iteration).

This is less optimal than frontier-driven prefetching but fits
Arborist's algorithmic shape. In practice the bulk fetching covers
most of the network I/O cost because the dependency graph's
structure is discovered quickly.

## 6. Materializer

### 6.1 node_modules layout

Default: **hoisted layout** matching npm 9's output. Every package is
placed as close to the root as possible without conflict:

```
project/
  package.json
  package-lock.json
  node_modules/
    react/              # hoisted (depended on by multiple)
    react-dom/
    lodash/
    old-package/
      node_modules/
        react/          # nested because "old-package" needs an older react
                        # that conflicts with the root-hoisted react
```

Optional: **isolated layout** (pnpm-style) via symlinks. Packages
live in a content-addressed store; `node_modules/` is a set of
symlinks. Activated via `--isolated` flag. More correct than
hoisting (no accidental access to non-declared dependencies) but
breaks some tools that traverse `node_modules` directly.

### 6.2 Tarball extraction

Each resolved package's dist.tarball is a gzipped tar. Extract to
the substrate recipe store (content-addressed by integrity hash),
then link into node_modules:

```go
func (m *npmMaterializer) installPackage(ctx context.Context, pkg *ResolvedNode, dst string) error {
    // 1. Fetch tarball to recipe store (cached by SRI hash).
    // 2. Verify integrity: parse `sha512-...` SRI, compute hash, compare.
    // 3. Extract tarball. npm tarballs have a canonical prefix "package/";
    //    strip it, extract into dst.
    // 4. Create node_modules/{name}/.package.json preserving original metadata.
    //    npm writes this as node_modules/{name}/package.json verbatim.
}
```

### 6.3 Hardlink/reflink strategy

Content-addressed recipe store contains extracted packages once per
(name, version, integrity). For each materialization:

```go
func (m *npmMaterializer) linkFiles(ctx context.Context, srcPkgDir, dstPkgDir string, mode substrate.LinkMode) error {
    // Walk src tree.
    // For each file, TryReflink → TryHardlink → FallbackCopy.
    // For directories, create; don't try to link.
    // For symlinks in src, reproduce as symlinks in dst (not resolve then copy).
}
```

A 100-package `node_modules` hoisted layout with reflinks: <1s
materialization. Same with hardlinks: <2s. Byte-copy: ~15s (what npm
does natively).

### 6.4 Lifecycle scripts

npm packages can declare lifecycle scripts:

- `preinstall` — runs before package install
- `install` — runs during install (typically native builds)
- `postinstall` — runs after install
- `prepublish`, `prepare`, etc. (don't run during install)

Default: **run lifecycle scripts** matching npm's behavior.
Security-conscious mode: `--ignore-scripts` skips all lifecycle
execution.

Lifecycle scripts have full shell access. This is a supply-chain
risk; the adapter:

- Defaults to running (for npm compat)
- Exposes `--ignore-scripts` flag
- Logs every script execution with full command and stderr
- Supports script execution sandboxing via substrate's subprocess
  sandbox (seccomp on Linux, sandbox-exec on macOS)

Script execution order: **topological by dependency** — a package's
scripts run after all its dependencies' scripts have completed.
Parallel within a topological layer.

### 6.5 Bin installation

Packages with `bin` entries in package.json have wrapper scripts
created in `node_modules/.bin/`:

```go
func installBins(ctx context.Context, nodeModulesDir string, pkg *ResolvedNode) error {
    binDir := filepath.Join(nodeModulesDir, ".bin")
    for binName, binPath := range pkg.Bin {
        target := filepath.Join(nodeModulesDir, pkg.Name, binPath)
        link := filepath.Join(binDir, binName)
        // Linux/macOS: symlink
        // Windows: generate .cmd wrapper
        createBinLink(link, target)
    }
}
```

## 7. Lockfile

### 7.1 package-lock.json v3

```go
type npmLockfileCodec struct{}

func (c *npmLockfileCodec) Ecosystem() string { return "npm" }
func (c *npmLockfileCodec) Filename() string  { return "package-lock.json" }
func (c *npmLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) {
    // Parse JSON.
    // Handle lockfileVersion 1, 2, 3. v1 is pre-npm-7; v2 is npm 7-8 (dual
    // format: both packages and dependencies); v3 is npm 9+ (packages only).
    // Normalize to substrate.LockfileSnapshot.
}
func (c *npmLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit v3.
    // Key ordering matches npm's (packages object in insertion-order of paths).
    // Integrity field uses SRI format.
    // Dev/optional/peerOptional flags computed from the resolved tree.
}
```

### 7.2 Byte-identical output

Matching npm's exact output is important for ecosystem compat
(developers may run `npm install` after `sylk install node` and
expect no lockfile diff). Test corpus: generate lockfiles for 50
projects using our adapter, compare byte-for-byte to the npm CLI's
output. Accept:

- Order of keys within objects — npm uses insertion order; we can
  match
- Whitespace/indentation — npm uses 2-space, no trailing newline; we
  match
- "requires: true" marker — required for v3
- Integer vs float for lockfileVersion — npm uses int

Differences that are bugs:

- Different integrity format (sha1 vs sha512)
- Different dev/optional flag computation
- Different package paths (due to different hoisting choices)

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | **Not used** (custom Arborist-like resolver) |
| `core/substrate/http` | All registry and tarball fetches |
| `core/substrate/cache/metadata` | Packument caching with Etag revalidation |
| `core/substrate/store/recipe` | Content-addressed extracted packages |
| `core/substrate/materializer` | Reflink/hardlink for node_modules |
| `core/substrate/lockfile` | package-lock.json codec |
| `core/substrate/feeds` | Scoped registry routing |
| `core/substrate/auth` | npm token auth per registry |
| `core/substrate/frontier` | **Not used** (bulk prefetch instead) |
| `core/substrate/subprocess` | Lifecycle script execution with optional sandbox |

Adapter modules under `adapters/node/`:

- `coordinate.go` — NpmCoordinate, scope handling
- `version.go` — SemVer (with npm's pre-release quirks)
- `manifest.go` — package.json parser
- `registry.go` — npm registry client
- `packument.go` — streaming JSON parser
- `ranges.go` — semver range parser
- `overrides.go` — overrides block evaluator
- `resolver.go` — Arborist-style multi-phase resolver
- `peer.go` — peer dep propagation
- `hoist.go` — node_modules hoisting algorithm
- `workspace.go` — workspace member resolution
- `materializer.go` — node_modules construction
- `lifecycle.go` — lifecycle script runner
- `bins.go` — bin script generation
- `lockfile.go` — package-lock.json codec
- `npmrc.go` — .npmrc parser
- `adapter.go` — top-level Resolver

Estimated LOC: ~7,000–8,000. Largest adapter. Complexity drivers:

- Arborist-style multi-phase resolver (~1500 LOC)
- Streaming JSON parser with field extraction (~500 LOC)
- Hoisting algorithm with conflict detection (~1000 LOC)
- Lifecycle script runner with sandbox (~500 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Package not found on registry | `ErrNoSuchRecipe` | Include registry URL |
| Version not in packument | `ErrNoSatisfyingVersion` | List available versions |
| Peer dep unsatisfiable in any placement | `ErrCapabilityConflict` | "Package X requires peer Y@^1.0 but Y@^2.0 is required by Z" |
| Optional peer missing | (warning, not error) | Log; install proceeds |
| Override references non-existent pkg | User error | Clear message |
| Integrity mismatch (SRI hash) | `ErrIntegrityMismatch` | Possibly compromised mirror |
| Tarball unreachable (all feeds failed) | `ErrNetworkPermanent` | After substrate retry |
| Lifecycle script failed | `ErrInternalBug` or user error | Surface stderr; continue with `--ignore-scripts` possible |
| Workspace member missing package.json | User error | Glob matched non-package dir |
| Peer resolution didn't converge | `ErrInternalBug` | Pathological graph; report for debugging |

## 10. Security

### 10.1 Integrity (SRI)

npm 5+ uses [Subresource Integrity](https://www.w3.org/TR/SRI/)
format for tarball hashes: `sha512-<base64-hash>`. The adapter:

- Parses the SRI to extract algorithm and expected hash
- Verifies on tarball receipt
- Records in lockfile
- Rejects legacy `shasum` SHA-1 if both formats present (prefer
  sha512)

### 10.2 Supply-chain

- **Audit advisories**: integrate with
  [npm audit API](https://github.com/npm/npm-registry-fetch) and/or
  [OSV](https://osv.dev/) to surface known-vulnerable versions as
  `CapabilityConflict` with severity
- **Package signing**: npm added
  [Sigstore-based provenance attestations](https://docs.npmjs.com/generating-provenance-statements)
  in 2023. The adapter verifies attestations when present
- **FeedMapping** for scope-level trust: `@mycompany/*` must be
  from `https://npm.mycompany.com/` — cross-registry serving
  triggers `ErrIntegrityMismatch`
- **Typosquatting**: integrate [socket.dev](https://socket.dev/) or
  similar API for package risk scoring

### 10.3 Lifecycle script security

Lifecycle scripts are the single biggest supply-chain risk in
Node:

- **Default**: run scripts (npm compat)
- **Sandbox**: run scripts in substrate's subprocess sandbox
  (seccomp/sandbox-exec). Limits filesystem write scope to the
  package's own install directory. Limits network access.
- **Advisory mode**: `--ignore-scripts` skip all lifecycle
  execution; log warnings listing skipped scripts so user can
  review
- **Static analysis**: integrate with `npm-audit-package-trust` or
  equivalent to flag packages whose lifecycle scripts download
  binaries from non-npm URLs (common supply-chain attack vector)

### 10.4 Registry auth scope

Tokens in `.npmrc` are scoped to specific registries. The
substrate's AuthResolver ensures tokens never leak to other hosts.

## 11. Testing

### 11.1 Unit tests

- SemVer + npm quirks (pre-release range interaction, caret on 0.x)
- Range parser for every syntactic form
- Packument streaming parser (handles 10 MB+ fixtures)
- Overrides evaluator with nested patterns
- Peer dep propagation on hand-crafted graphs
- Hoisting with conflict scenarios
- `.npmrc` parser with scoped registries
- Lockfile round-trip for 50 real projects

### 11.2 Integration tests

- Resolve create-react-app + all deps (~1500 packages, exercises
  peer deps heavily)
- Resolve next.js + React 18 (peer dep convergence)
- Resolve workspace-monorepo with 10 workspaces
- Resolve project with overrides forcing transitive version
- Resolve project with deprecated packages (should warn, not fail)
- Resolve project with optional deps for platform-specific binaries
- Lifecycle scripts: dependency-order execution, sandbox escape
  attempts rejected

### 11.3 Ecosystem compat

Golden corpus of 100 real Node projects with npm CLI-generated
lockfiles. Our resolution must produce equivalent `package-lock.json`
output. Tolerance:

- **Zero tolerance** for package version divergence
- **Zero tolerance** for dependency tree shape divergence
- **Low tolerance** for lockfile key ordering differences (documented;
  tracks npm behavior)

### 11.4 Performance

Target: match Bun's performance characteristics within 2x:

- Resolve create-react-app cold-cache: <5s (Bun: ~2s, npm: ~30s)
- Resolve warm-cache: <1s
- Materialize node_modules via hardlinks, 1500 packages: <3s
- Streaming packument parse (10 MB): <30ms

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, 100 direct deps / 1500 total | <5s | <3s |
| Warm resolve, same | <1s | <500ms |
| Packument fetch + parse (mega, 10 MB) | <50ms | <25ms |
| Packument fetch (304 from cache) | <15ms | <5ms |
| Hoisting pass (1500 pkgs) | <500ms | <200ms |
| Peer propagation pass (1500 pkgs) | <500ms | <200ms |
| Materialization, 1500 pkgs (reflink) | <3s | <1.5s |
| Lifecycle script total (20 scripts, parallel) | <30s | (depends) |
| Peak memory, 5000-pkg resolve | <600MB | <400MB |
| Lockfile read+validate | <100ms | <50ms |
| Lockfile write (canonical) | <150ms | <75ms |

## 13. Phases

**M0.** Types, parsers for package.json/npm ranges/package-lock.json
structure. No network.

**M1.** npm registry client with streaming packument parser. Can
fetch and enumerate candidate versions. Tarball download with SRI
verification.

**M2.** Arborist-style resolver end-to-end for a small project.
Peer propagation working. Hoisting producing correct layouts.
Workspace support.

**M3.** Materializer with reflink/hardlink. Full lockfile codec
producing byte-identical-to-npm output for 50 ecosystem-compat
projects. Lifecycle scripts with optional sandbox.

**M4.** Advisory/provenance integration. Isolated (pnpm-style)
layout as opt-in. Telemetry. Production-ready.

## 14. Open Questions

- **Arborist upstream tracking.** npm's Arborist is JavaScript; our
  Go port should track its behavior closely. How to handle Arborist
  changes (npm 10, 11, ...)? Proposal: track the majority version
  in use; document the npm version whose behavior we match; freeze
  semantics except for bug fixes.
- **Streaming parser: fastjson vs custom.** fastjson is battle-
  tested but has subtle memory model quirks. Custom SAX-style
  parser for the specific packument shape may be faster and
  easier to control. Benchmark both, decide by M1.
- **Lifecycle script sandboxing.** Many real packages' install
  scripts legitimately download binaries (node-sass, etc.).
  Sandboxing breaks them. Proposal: sandbox *by default* (users
  who need network in scripts opt out per-package); fallback to
  `--ignore-scripts` with clear errors when sandbox rejects.
- **Doppelganger detection.** When peer deps force multiple copies
  of the same package at different placements, the resolver must
  decide whether they're truly independent (different peer contexts)
  or accidental duplicates. Default: trust the algorithm; warn but
  don't error.
- **Binary addons (native modules).** Packages like sqlite3, node-
  canvas compile C++ at install time. Our resolver must honor
  their declared `os`/`cpu` constraints; materializer must handle
  the install-time build. Proposal: at M3, basic support (run
  install script in appropriate environment); at M4, native build
  caching keyed by (sdist hash, platform).
- **pnpm lockfile compat.** pnpm uses `pnpm-lock.yaml` in an entirely
  different format. Should we support reading pnpm lockfiles as
  hints? Proposal: not in M3; revisit later.

## 15. Dependencies

- **Substrate M1** (HTTPClient, cache) → adapter M1
- **Substrate M2** (multi-feed federation) → adapter M2 (scoped
  registries)
- **Substrate M3** (materializer, lockfile framework, subprocess)
  → adapter M3

No dependency on other adapters.

External Go dependencies beyond substrate:

- `github.com/valyala/fastjson` — streaming JSON parse (or roll own)
- `github.com/Masterminds/semver/v3` — possibly; npm's semver has
  subtleties a generic lib often misses
- `github.com/mattn/go-isatty` — TTY detection for lifecycle script
  output
- Custom tar+gzip extractor wrapping `archive/tar`/`compress/gzip`
