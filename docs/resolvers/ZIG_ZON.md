# ZIG_ZON.md — Zig / build.zig.zon Adapter Implementation Plan

Tier 5 — the **modern minimalist** adapter. Validates the
substrate handles the **no-resolver-needed** case (URL+hash pairs,
no version ranges, no constraint solver) and **mandatory content-
addressed integrity** (every dep declared with its hash).

The simplest adapter in this doc. By design.

## 1. Overview

The Zig adapter resolves and materializes packages from:

- **Arbitrary HTTP(S) URLs** returning tarballs
- **Git tags via tarball archive URLs** (e.g., GitHub's
  `/archive/v1.0.0.tar.gz`)
- **Git repositories directly** (`git+https://...` URLs — less
  common, supported)
- **Local paths**

Produces:

- A resolved set (effectively: the set declared in `build.zig.zon`,
  transitively expanded)
- The build.zig.zon itself is both the manifest and the lockfile
  — no separate lockfile file
- A materialized content-addressed cache at
  `~/.cache/zig/p/{hash}/`

User-visible behaviors (M3 target):

- `sylk resolve zig ./build.zig.zon` → updated zon with any
  transitively-discovered deps
- `sylk install zig` → all deps fetched into cache
- `sylk add zig <url>` → `zig fetch --save <url>` equivalent;
  computes hash, updates zon
- `sylk why zig <pkg>` → simple dependency chain (no solver)

Non-goals:

- Running `zig build`
- Installing Zig toolchain
- Creating build.zig files

## 2. Data Model

### 2.1 Coordinates

```go
type ZigCoordinate struct {
    Name string   // dependency key in build.zig.zon
    URL  string   // fetch URL
    Hash string   // content hash (multihash format)
    Path string   // local path (alternative to URL)
}

// There's no "version" per se. The hash IS the identity.
// Two URLs serving identical content produce the same hash
// and are the same "version" as far as the cache is concerned.
```

### 2.2 build.zig.zon

Zig Object Notation — anonymous struct literal syntax:

```zig
.{
    .name = "myproject",
    .version = "0.1.0",
    .minimum_zig_version = "0.11.0",
    .dependencies = .{
        .raylib = .{
            .url = "https://github.com/raysan5/raylib/archive/5.0.tar.gz",
            .hash = "1220abc123def456...",
        },
        .zigimg = .{
            .url = "https://github.com/zigimg/zigimg/archive/abc123.tar.gz",
            .hash = "1220fedcba987654...",
        },
        .local_dep = .{
            .path = "../local_dep",
        },
        .lazy_dep = .{
            .url = "https://example.com/lazy.tar.gz",
            .hash = "1220...",
            .lazy = true,  // only fetched on demand
        },
    },
    .paths = .{ "build.zig", "build.zig.zon", "src" },
}
```

```go
type BuildZigZon struct {
    Name              string
    Version           string
    MinimumZigVersion string
    Dependencies      map[string]ZonDependency
    Paths             []string
}

type ZonDependency struct {
    URL   string
    Hash  string
    Path  string
    Lazy  bool
}
```

### 2.3 Hash format

Zig uses a custom multihash-like format:

```
1220<32 bytes hex>
```

- `1220` prefix = SHA-256 in multihash format (`12` = SHA-256 code,
  `20` = 32-byte length)
- Followed by hex-encoded hash

The hash is computed over the **unpacked directory contents** —
not the tarball bytes. Algorithm:

1. Unpack the tarball to a directory
2. Walk the directory in sorted order
3. For each file: compute SHA-256 of `{relative_path}:{permissions}:{content}`
4. SHA-256 of the concatenation

This matches Zig's reference implementation exactly. Any deviation
produces mismatched hashes and resolution failures.

```go
func ComputeZigPackageHash(unpackedDir string) (string, error) {
    // Walk dir in sorted order (alphabetically, deterministic).
    // Skip dirs; hash files only.
    // Concatenate: for each file, write path + perm + content hash.
    // Final hash = SHA-256 of concatenation.
    // Format: "1220" + hex(hash).
}
```

## 3. HTTP Transport

### 3.1 URL fetching

The protocol is unambiguous: fetch the URL, expect a tarball (tar.gz
or tar.xz), extract, verify hash. No registry API, no metadata
format, no version discovery.

```go
func (a *ZigAdapter) fetchTarball(ctx context.Context, url string, expectedHash string) (unpackedDir string, err error) {
    // 1. GET url. Expect tarball response.
    // 2. Detect compression (gzip / xz).
    // 3. Extract to staging dir.
    // 4. Compute ZigPackageHash.
    // 5. Compare to expectedHash. Mismatch = fatal.
    // 6. Move to cache-addressed location: {store}/zig/p/{hash}/
    // 7. Return path.
}
```

### 3.2 GitHub / GitLab archive URLs

Most real Zig dependencies use GitHub-hosted archive URLs:

```
https://github.com/{user}/{repo}/archive/{ref}.tar.gz
```

These are standard git archive exports — the adapter treats them
as any other tarball URL. Integrity via the hash, not via git
commit SHA.

### 3.3 git+ URLs

For rare cases where a hash-stable tarball isn't available
(frequent force-pushes, etc.), Zig supports `git+https://...` URLs.
The adapter:

- Clones via substrate's git client
- Archives at the specified ref
- Proceeds as with tarball

### 3.4 Authentication

URLs can require HTTPS Basic auth (for private artifact hosting).
The adapter routes via substrate AuthResolver. Most public Zig
packages are anonymous-accessible.

## 4. Metadata Layer

### 4.1 ZON parser

ZON is a subset of Zig syntax: anonymous struct literals + basic
types (strings, numbers, bools, tuples). No functions, no
conditionals.

```go
func ParseZON(data []byte) (ZONValue, error) { ... }

type ZONValue interface{}  // string | int | float | bool | []ZONValue | map[string]ZONValue

func ParseBuildZigZon(data []byte) (*BuildZigZon, error) {
    value, err := ParseZON(data)
    if err != nil { return nil, err }
    // Extract known fields into typed BuildZigZon struct.
}
```

Parser is ~600 LOC (small grammar, deterministic). Output is
identical to `zig --zon-format` for round-trip fidelity.

### 4.2 No registry; no version discovery

The adapter has no "fetch available versions" step. Each
dependency in build.zig.zon is a single URL + hash pair. There's
no candidate set to consider.

### 4.3 Cache keys

```
(ecosystem="zig", name=<hash>, version=<hash>, platform_hash="")
```

The hash IS the identity. Two URLs serving identical content
cache-collide intentionally.

## 5. Resolver

### 5.1 No solver

There is no resolver in the constraint-satisfaction sense. The
"resolution" is:

```go
func (a *ZigAdapter) Resolve(ctx context.Context, req substrate.ResolveRequest) (substrate.ResolveResult, error) {
    // 1. Parse root build.zig.zon.
    // 2. For each declared dependency:
    //    - If URL+hash: note the pair.
    //    - If path: read its build.zig.zon; recurse.
    // 3. For each URL+hash dep:
    //    - Fetch tarball, verify hash.
    //    - Extract.
    //    - Read extracted build.zig.zon.
    //    - Transitive deps become additional entries (but NOT auto-
    //      added to root's build.zig.zon; users coordinate explicitly).
    // 4. Return the flat resolved set.
}
```

### 5.2 Transitive discovery is advisory

Zig's model pushes coordination to the user: if dep A and dep B
both have a transitive on dep X at different versions, the user
must explicitly add dep X to the root's build.zig.zon to
disambiguate.

The adapter:

- Reports transitive conflicts but doesn't auto-resolve
- Surfaces the conflict as a `CapabilityConflict` in the
  substrate's result
- Suggests the fix (add X to root)

### 5.3 Lazy dependencies

```zig
.lazy_dep = .{
    .url = "...",
    .hash = "...",
    .lazy = true,
},
```

Lazy deps are not fetched at resolve time — only when
`build.zig` explicitly requests them via
`b.dependency("lazy_dep", .{})`. The adapter records them in the
resolved set as "deferred" and materializes on demand.

### 5.4 No frontier

Not implementing `FrontierAwareResolver`. There's no backtracking,
no candidate consideration, no events to stream. Standard
`Resolver` interface only.

## 6. Materializer

### 6.1 Content-addressed cache

```
~/.cache/zig/p/
  1220abc123.../
    build.zig
    build.zig.zon
    src/
    ...
  1220def456.../
    ...
```

Each dep is stored once globally by hash. Different projects
depending on the same hash share the extracted tree — the cache
is both manifest and materialization.

The substrate's recipe store maps directly; layout-compatible
with Zig's native cache so `zig build` can consume without
further materialization.

### 6.2 Linking

For project-local materialization (rare — Zig prefers reading
from the global cache directly), reflink/hardlink from cache to
project-local dir. Matches the substrate's standard
materialization.

### 6.3 No build-time behavior

The adapter ends at "tarball extracted, hash verified, cache
populated." `zig build` reads from the cache directly.

## 7. Lockfile

### 7.1 build.zig.zon IS the lockfile

Zig's design conflates manifest and lockfile. Users see one file.

The adapter treats build.zig.zon as both:

- Input constraints (declared URLs + hashes)
- Output state (updated URLs + hashes after fetches)

Lockfile round-trip is byte-identical ZON emission.

```go
type zigLockfileCodec struct{}

func (c *zigLockfileCodec) Ecosystem() string { return "zig" }
func (c *zigLockfileCodec) Filename() string  { return "build.zig.zon" }

func (c *zigLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) {
    zon, err := ParseBuildZigZon(data)
    if err != nil { return substrate.LockfileSnapshot{}, err }
    // Translate deps into substrate.LockfilePin entries.
}

func (c *zigLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit ZON with deterministic key ordering (alphabetical by dep name).
    // Match Zig's canonical formatter (4-space indent, trailing commas).
}
```

### 7.2 Hash computation on `sylk add`

When a user adds a new dep:

```bash
sylk add zig https://github.com/raysan5/raylib/archive/5.0.tar.gz
```

The adapter:

1. Fetches the URL
2. Extracts, computes hash
3. Updates build.zig.zon with (URL, computed hash)
4. Caches extracted content

Matches Zig's own `zig fetch --save` behavior.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | **Not used** |
| `core/substrate/http` | Tarball fetches |
| `core/substrate/cache/metadata` | (Minimally used — no metadata to cache) |
| `core/substrate/store/recipe` | Content-addressed package storage |
| `core/substrate/materializer` | Project-local linking (rare) |
| `core/substrate/lockfile` | build.zig.zon codec |
| `core/substrate/feeds` | (Trivially used — "feeds" are URLs) |
| `core/substrate/auth` | URL-level HTTPS auth |
| `core/substrate/frontier` | **Not used** |
| `core/substrate/git` | For git+ URLs |

Adapter modules under `adapters/zig/`:

- `coordinate.go` — `ZigCoordinate`
- `zon_parser.go` — ZON format parser
- `zon_emitter.go` — canonical ZON formatter
- `manifest.go` — build.zig.zon typed wrapper
- `hash.go` — Zig package hash algorithm
- `fetcher.go` — tarball fetch + extract + hash verify
- `resolver.go` — transitive discovery (no solver)
- `materializer.go` — cache layout
- `lockfile.go` — build.zig.zon as lockfile codec
- `adapter.go` — top-level Resolver

Estimated LOC: ~2,500. **Smallest adapter in this doc.** Complexity
drivers:

- ZON parser/emitter (~800 LOC)
- Zig package hash algorithm (~200 LOC; specific byte-layout
  requirements)
- Tarball extraction with both gzip and xz (~400 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| URL unreachable | `ErrNetworkPermanent` | After substrate retry |
| Hash mismatch | `ErrIntegrityMismatch` | **Fatal always.** No fallback |
| Tarball format unrecognized | `ErrNoSuchRecipe` | Only gzip and xz supported |
| ZON parse error | User error | Line/column info |
| Path dependency doesn't exist | User error | Local path missing |
| Transitive conflict (two deps require same third at different hashes) | `ErrCapabilityConflict` | Suggest adding root-level dep |
| Hash format invalid (not multihash prefix) | User error | Suggest regenerating via `zig fetch` |

## 10. Security

### 10.1 Mandatory hash verification

Every dep must have a hash. The adapter refuses to fetch URLs
without hashes (except when computing a new hash via `sylk add`).
This is the ecosystem's integrity contract.

### 10.2 No registry; no signing

Zig has no package registry, no signing, no transparency log. The
hash IS the integrity. Supply-chain mitigations rely on:

- Source repositories' own security (GitHub, GitLab, etc.)
- Users vetting dependencies before adding
- Hash pinning preventing retroactive tampering

The substrate's optional vulnerability data integration is
effectively disabled — no package database to cross-reference.

### 10.3 Tarball extraction safety

Tar archives can contain malicious paths (`../` escapes,
absolute paths). The adapter sanitizes during extraction; paths
that escape the staging directory trigger fatal errors.

## 11. Testing

### 11.1 Unit tests

- ZON parser/emitter round-trip on 50+ real build.zig.zon files
- Zig package hash algorithm — golden test vectors from Zig's
  own tests
- Tarball extraction with path-traversal malicious inputs
- Hash mismatch produces ErrIntegrityMismatch

### 11.2 Integration tests

- Resolve and materialize raylib (canonical Zig example)
- Resolve a project with 10+ deps, some with transitive chains
- Verify `zig build` succeeds against our materialized cache
- Hash mismatch scenarios (tampered tarball)

### 11.3 Ecosystem compat

Zig is new (0.11 shipped zon in 2023). Corpus: ~20 real projects
using zon. Match `zig build`'s cache behavior — same hashes
produced for same inputs, same cache layout.

### 11.4 Performance

- Resolve + fetch a typical Zig project: <2s cold, <100ms warm
- Hash a small package (~100 files): <50ms
- Tarball extraction: bandwidth-bound (~100 MB/s on gzip)

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve + fetch (10 deps) | <2s | <1s |
| Warm resolve | <50ms | <25ms |
| ZON parse (typical build.zig.zon) | <1ms | <500μs |
| Package hash computation (1MB pkg) | <20ms | <10ms |
| Tarball extract (10MB) | <100ms | <50ms |
| Materialization (hardlink, 20 pkgs) | <200ms | <100ms |
| Peak memory | <100MB | <50MB |

## 13. Phases

**M0.** ZON parser/emitter, hash algorithm; unit tests.

**M1.** Tarball fetch + extract + hash verify against real URLs.

**M2.** Transitive resolution. build.zig.zon codec round-trip.

**M3.** Materializer + cache layout. `sylk add` workflow. 20
ecosystem-compat projects green.

**M4.** git+ URLs. xz compression support (if not already). Zig
0.12+ zon evolution tracking.

## 14. Open Questions

- **Zig stability.** Zig itself is pre-1.0 and zon format may
  evolve. Track Zig's ZON spec closely; version our codec for
  compatibility.
- **Transitive conflict auto-resolution.** Zig's design says
  "user coordinates manually." Sylk could offer auto-resolution
  (pick highest hash by timestamp, etc.) but this diverges from
  the ecosystem's intent. Proposal: surface conflicts, refuse to
  auto-resolve.
- **Lazy dependencies.** How does the substrate model "deferred"
  deps? Proposal: a LockfilePin with a `lazy: true` flag; the
  materializer skips unless explicitly invoked.
- **`zig fetch --save` subprocess fallback.** For edge cases
  (unusual compression, weird URLs), shell out to the Zig
  toolchain for hash computation. Default to native; subprocess
  only when native fails.

## 15. Dependencies

- Substrate M1 (HTTP, cache) → adapter M1
- Substrate M3 (materializer, lockfile) → adapter M3

External Go dependencies:

- Custom ZON parser/emitter (~800 LOC)
- `archive/tar`, `compress/gzip` — stdlib
- `github.com/ulikunitz/xz` — for xz compression (if adopted)

No substrate PubGrub dependency. No dependency on other adapters.
