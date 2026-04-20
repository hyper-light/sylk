# Resolver Engineering — Per-Ecosystem Case Studies

Companion to `TOOL_VFS.md` § *Resolvers (Per-Ecosystem Adapters)*. The
parent doc names the *algorithm* each ecosystem's resolver uses (Python
→ PubGrub, Cargo → PubGrub-derived, Go → MVS, etc.). This doc is about
**everything below the algorithm** — the network strategy, cache
architecture, parallelism model, and avoidance heuristics that
determine whether a resolver is fast or slow. Algorithm choice is
necessary but rarely sufficient: the fastest resolvers in production
beat naive implementations of the *same algorithm* by 10–100×.

The structure: one case study per resolver, plus a final synthesis
section that turns the recurring lessons into contracts the Sylk
adapter interface should support so that fast implementations can plug
in without re-architecting the substrate.

---

## Why algorithm choice underdetermines speed

Resolution is overwhelmingly an **I/O-bound** problem. Modern resolvers
spend most of their wall-clock time fetching package metadata, parsing
it, and waiting on disk. The algorithmic core (PubGrub, MVS, etc.) is
typically a small fraction of total runtime — well under 10% even for
large dependency closures.

The resolvers that win therefore optimize the I/O surface, not the
math. The algorithm is correct or it is not; the I/O strategy is
where 50× speedups live.

Five repeating engineering moves separate fast resolvers from naive
ones. Every case study below maps its design choices back to these
five:

1. **Minimum-bytes metadata fetching** — resolve from headers and
   manifests, not whole archives.
2. **Speculative prefetching interleaved with the solver** — start
   I/O on candidate packages as soon as the solver considers them,
   cancel when the solver backtracks.
3. **Content-addressed shared cache** with cheap materialization
   (reflinks, hardlinks, symlinks) so warm-cache resolves and
   environment construction are filesystem-metadata operations.
4. **Build-on-resolve avoidance** — never run a package's build
   system to learn its dependencies; demand a manifest.
5. **Connection multiplexing and concurrency** that saturates the
   wire — HTTP/2 or HTTP/3 with shared pools, async I/O, no
   GIL-equivalent.

A Sylk adapter that uses the doc-mandated algorithm but implements
none of these will be 50–100× slower than the production reference.
A Sylk adapter that implements all five but uses a sub-optimal
algorithm will be within ~2×. Optimize where the time is.

---

## Case study: uv (Python / PyPI)

uv (astral-sh/uv) is the reference implementation for "what a fast
Python resolver looks like." It is 10–100× faster than pip / Poetry /
PDM on equivalent workloads. The PubGrub algorithm it uses is the
same one Poetry uses; the speedup comes entirely from the layers
around it. What follows is a breakdown of the engineering moves uv
makes, in roughly descending order of impact.

### 1. Range-request metadata fetching from wheels

A Python wheel is a ZIP archive. The dependency manifest the
resolver needs (`METADATA`) is one entry inside it, typically <10 KB.
The wheel itself can be tens of MB (numpy is ~20 MB, torch is
hundreds of MB).

A naive resolver downloads the whole wheel to inspect dependencies.
uv issues two HTTP **Range requests** instead:

1. Range-fetch the last ~64 KB of the wheel — this contains the
   ZIP central directory.
2. Parse the central directory in-memory; locate the `METADATA`
   entry's byte range.
3. Range-fetch *just those bytes*.

Total bytes transferred per candidate version: usually <16 KB,
regardless of wheel size. The bandwidth difference vs naive is
**three orders of magnitude** for large packages. Resolvers consider
many candidates per package during backtracking; this dominates
end-to-end network time.

PyPI supports HTTP Range requests on its CDN; the central-directory
trick depends on the ZIP format putting its index at the *end* of
the file (which it does, by design — ZIP was meant to be appendable
on tape).

### 2. Speculative prefetching with cancellable in-flight fetches

PubGrub is, on paper, a synchronous algorithm: pick a candidate,
fetch its dependencies, propagate constraints, decide. A textbook
implementation calls `fetch_metadata(pkg, ver)` synchronously inside
the solver loop, blocking on each network round-trip.

uv runs PubGrub on a Tokio task that **exposes its decision frontier**.
As soon as the solver *considers* package `X` at version `Y` — even
before deciding — an I/O task is dispatched to fetch `X==Y`'s
metadata in parallel. By the time PubGrub gets around to needing
that metadata, it's almost always already in cache.

When PubGrub **backtracks** (a constraint failure invalidates a
branch), uv cancels the in-flight fetches that no longer matter.
Without cancellation, speculative prefetching is a bandwidth bomb;
with it, you saturate your bandwidth on the *useful* candidates
only.

This is not a property of "using PubGrub." It's a property of how
PubGrub is wired to the I/O layer. A naive Go port that does
`metadata, _ := fetch(pkg, ver)` synchronously inside the solver
loop *throws this away entirely.* The Sylk `Resolver` interface
therefore needs to expose the candidate-consideration event stream
as a first-class concept, not bury it behind a `Resolve(ctx,
request) -> result` blob.

### 3. Content-addressed global cache with reflink/hardlink venvs

uv stores everything in a single global cache rooted at
`$XDG_CACHE_HOME/uv` (defaulting to `~/.cache/uv`):

- **Wheels** are stored once per `(content_hash)`. Wheel bytes are
  shared across every venv on the machine.
- **Built sdists** are cached per `(sdist_hash, platform_tag)` so
  the second resolve in any project that depends on a sdist reuses
  the build.
- **Metadata** (the `METADATA` files extracted by the
  range-request trick) is indexed in a single SQLite database
  with composite key `(package, version, platform_tag)`. Lookup
  is one indexed query, sub-millisecond.
- **Negative cache**: "this package has no wheel for this
  platform" is remembered so we don't re-probe across resolves.

When uv materializes a venv from the cache, it uses **reflinks**
(copy-on-write at the filesystem level — supported on btrfs, APFS,
ZFS, XFS) when available, **hardlinks** as a fallback. Either way
the venv creation is *filesystem metadata only* — no bytes are
copied. A 100-package venv materializes in milliseconds.

Compare to pip's per-venv `site-packages` directory: every package
is unpacked from its wheel and copied bytewise. Creating five
venvs from the same dependency set costs five times the disk I/O.

The Sylk recipe-store and content-addressed substrate already
express this pattern (per `TOOL_VFS.md`); the lesson is that the
**resolver and the materializer must share the cache** so the
resolver's downloads serve the materializer's installs without a
second copy.

### 4. No build-during-resolve

A source distribution (`sdist`) is a tarball with a build script
(`setup.py` or `pyproject.toml`'s build backend). Running that
script can take *minutes* — C extensions compile, Cython
transpiles, `setup_requires` triggers transitive resolves.

A naive resolver invokes `pip download <pkg>` or equivalent and
accidentally builds sdists during resolution to learn their
dependencies. This single mistake is where 90% of slow-resolver
time goes for repos with C-extension deps.

uv's rule: **never build during resolve.** Specifically:

- If a wheel exists for the target platform, use it. Always.
- If only an sdist exists, parse the sdist's `PKG-INFO` (cheap,
  no build) to extract the static dependency list. PEP 517 /
  PEP 621 mandate this metadata is declared.
- If `PKG-INFO` is missing or incomplete (legacy sdists), fall
  back to a *minimal* PEP 517 metadata-only build (`prepare_metadata_for_build_wheel`)
  — still skips the actual extension compile.
- The full sdist build happens at materialization time, in
  parallel, never blocking the resolver.

The Sylk adapter equivalent: the resolver's `Resolve` step is for
metadata only. Recipe materialization (build, fetch full archives)
is a separate downstream phase that can parallelize and cache
independently.

### 5. HTTP/2 multiplexing and Tokio-based concurrency

uv opens a small number of long-lived HTTP/2 connections to PyPI
and multiplexes hundreds of concurrent requests over them. No
head-of-line blocking, no per-request TCP handshake, no TLS
re-negotiation. The entire transport layer is `reqwest` over
`hyper` over Tokio.

pip and Poetry historically use `urllib3` connection pools that
serialize requests on the same connection (HTTP/1.1 keep-alive).
Even with thread pools or async wrappers, the GIL serializes
the parsing work that follows each response.

For a Sylk Go-based adapter, this factor mostly disappears: Go's
`net/http` does HTTP/2 by default and goroutines have no GIL. The
adapter still needs to *use* a shared `http.Client` with HTTP/2
enabled (the default since Go 1.6) and bound concurrent fetches
with a `golang.org/x/sync/errgroup` or similar — not spawn a fresh
client per request.

### Speedup attribution

A rough decomposition of where uv's speedup over Poetry comes from,
from internal profiling and uv's own design notes:

| Source | Approximate share of speedup |
|---|---|
| Range-request metadata + cache | 40% |
| Speculative prefetching | 25% |
| Build avoidance (wheel-first, sdist metadata-only) | 20% |
| HTTP/2 + native concurrency (Rust vs Python+GIL) | 10% |
| Algorithm (PubGrub vs Poetry's older mixology) | <5% |

The Sylk adapter, written in Go with PubGrub, automatically gets
the algorithm bucket and most of the concurrency bucket. The other
65–85% requires explicit engineering investment.

### Lessons for the Sylk Python adapter

Concrete contracts the implementation must satisfy to come within
2× of uv:

- **Range-request wheel metadata fetcher.** Treat downloading a full
  wheel during resolution as a bug.
- **Solver-frontier event stream.** The PubGrub integration must
  expose "considering candidate `(pkg, ver)`" events to a sibling
  prefetcher task. Add this to the `Resolver` interface — see
  *Adapter contracts* below.
- **Cancellable fetches.** Every metadata fetch is bound to a
  `context.Context` derived from the candidate decision so
  backtracking cancels them.
- **Shared content-addressed metadata cache** keyed by `(package,
  version, platform_tuple)` — backed by the same recipe-store SQLite
  the rest of the substrate uses, not a per-resolve in-memory map.
- **Wheel-first, sdist-metadata-only.** Sdist *building* belongs in
  the materializer, not the resolver.
- **HTTP/2 client reuse.** One `http.Client` per resolver instance,
  not per request. Bounded by `errgroup` with a sane concurrency
  cap (uv defaults to 50; Sylk should benchmark and pick).

---

## Adapter contracts (lessons → interface obligations)

The current `Resolver` interface in `TOOL_VFS.md`:

```go
type Resolver interface {
    Resolve(ctx context.Context, request ResolveRequest) (ResolveResult, error)
    Ecosystem() string
}
```

This is sufficient for *correctness* but blocks the speculative-prefetching
optimization that drives most of uv's speedup. Adapters that want to
support frontier-driven prefetching need an extension:

```go
// FrontierAwareResolver is implemented by resolvers whose solver loop
// can stream candidate-consideration events to a sibling I/O task.
// The substrate's prefetch coordinator subscribes to Frontier and
// dispatches metadata fetches in parallel; on backtrack, the
// coordinator cancels orphaned fetches via the context returned
// alongside each event.
type FrontierAwareResolver interface {
    Resolver
    Frontier() <-chan FrontierEvent
}

type FrontierEvent struct {
    Recipe    RecipeID    // candidate under consideration
    Decided   bool        // false = considered, true = decided
    Reason    string      // optional: solver's reason for considering
    PrefetchCtx context.Context // cancelled when this branch is abandoned
}
```

Resolvers that *can't* expose a frontier (resolvers that wrap an
opaque external solver, e.g. Maven's resolver via JNI) implement
only the base interface and forfeit the prefetch optimization.
Resolvers that *can* (any in-process PubGrub or MVS implementation)
implement both and get the speedup for free.

The substrate's prefetch coordinator is ecosystem-agnostic — it
just sees `RecipeID`s and dispatches metadata fetches against the
appropriate backend. Each backend owns its own range-request /
manifest-fetch logic.

---

## Case study: Cargo (Rust / crates.io)

Cargo is the reference for **what happens when the registry
protocol does the optimization for you.** uv has to do clever
range-request gymnastics on PyPI because PyPI's protocol predates
the idea that resolvers should fetch dependency metadata cheaply.
crates.io was designed knowing what resolvers need; Cargo gets
uv-class speed almost for free, even though Cargo's own resolver
historically wasn't PubGrub.

The lesson here is dual: (a) when you control the registry,
protocol design beats client-side cleverness, and (b) when you
don't, client-side cleverness is what gets you to the same place.
Sylk's substrate-level recipe registry should learn from cargo;
Sylk's third-party-ecosystem adapters (PyPI, npm, etc.) need the
uv-style cleverness because they don't get to redesign the
upstream protocol.

### 1. The sparse index protocol — protocol-level metadata fetching

Until 2023, Cargo distributed crate metadata via a **git
repository** (`crates.io-index`). To resolve dependencies, Cargo
cloned (or pulled) the entire index — every published version of
every crate, ~500 MB and growing. The first run on a fresh machine
took minutes; every subsequent run paid a `git pull` round-trip.

The new **sparse index** protocol (Cargo 1.68, default since
1.70) replaces this with a static HTTPS endpoint:

```
https://index.crates.io/{prefix}/{prefix2}/{crate}
```

Each URL returns a JSON-Lines file with one line per published
version of `{crate}`. Each line is the full dependency manifest
for that version: name, version, dependency list, feature
declarations, checksum, yanked flag, links field. Cargo fetches
**only the index files for crates actually being resolved** —
typically dozens to hundreds, never the whole index.

This is structurally what uv approximates with its
range-request-on-wheel-METADATA trick, but cargo gets it
*natively*: each metadata blob is its own HTTP resource,
cacheable by the CDN, fetchable without parsing a container
format.

A typical sparse-index file is a few KB. Cargo's metadata
fetch for a 100-crate dependency tree is a few hundred small
HTTPS requests against a CDN — completes in <1s on a fast
connection.

The Sylk lesson here is for the substrate's own recipe-store
metadata API: **serve dependency metadata as separate small
HTTP resources, one per recipe-version, with stable cacheable
URLs.** Don't bundle metadata into the recipe archive itself
the way PyPI bundles METADATA into wheels.

### 2. Speculative prefetching with cancellable HTTP/2 fetches

Cargo runs all index fetches concurrently over a small number
of HTTP/2 connections (via `reqwest`/`hyper`, the same stack uv
uses). The resolver doesn't block waiting for one crate's
metadata to arrive before requesting the next; it dispatches
fetches as the dependency graph is discovered.

Cargo's resolver is somewhat less aggressive about *cancelling*
in-flight fetches than uv is. The historical Cargo resolver
was a hand-rolled SAT-like backtracker without an explicit
"frontier event stream" — fetches were issued breadth-first
from each newly-discovered direct dependency, not driven by
the solver's candidate-consideration order. This is generally
fine because index files are small, so over-fetching is
cheap; for large workspaces with many version constraints, the
new resolver (`resolver = "2"`, default for edition 2021)
narrows the prefetch surface considerably.

Cargo is mid-migration to `pubgrub-rs` — the same library uv
uses — for the algorithmic core. When that lands, Cargo will
get the same frontier-driven prefetching uv already has. The
substrate `FrontierAwareResolver` interface proposed above
generalizes over this: any in-process PubGrub-based adapter
implements it; a wrapper around the legacy Cargo resolver does
not.

### 3. Content-addressed shared cache (`~/.cargo`)

Cargo's cache layout, rooted at `$CARGO_HOME` (default
`~/.cargo`):

- **`registry/cache/{registry-host}/{name}-{version}.crate`** —
  the gzipped tarball of the crate's source. Stored once per
  `(name, version)` across every project on the machine.
- **`registry/src/{registry-host}/{name}-{version}/`** —
  unpacked source, materialized on demand the first time a
  crate is built. Subsequent builds in any project reuse it.
- **`registry/index/{registry-host}/`** — the cached metadata
  files from the sparse index, indexed by crate name.
- **`target/`** (per-workspace, not global) — compiled
  artifacts (`.rmeta`, `.rlib`). Cargo's incremental compilation
  is keyed off content hashes of source + dependency
  fingerprints.

Every project on a machine shares the registry cache and the
unpacked source trees. Two workspaces depending on `serde
1.0.193` use the *same* on-disk source — no duplication. This
is the same shared-immutable-cache pattern uv uses; cargo has
had it since day one (2014).

What cargo does **less** well than uv: there's no reflink
materialization at the venv-equivalent level. Each workspace's
`target/` directory is independent and compiled artifacts
aren't shared. `sccache` / `cachepot` exist as third-party
wrappers to fill this gap; the substrate-level equivalent in
Sylk should plan for build-artifact sharing from the start
(Cargo would do it differently if redesigned today).

### 4. Feature unification — Cargo's unique complication

Cargo crates declare **features**: optional bits of
functionality (compile-time flags, optional dependencies)
that consumers opt into. Two crates depending on the same
version of a third may request *different but overlapping*
feature sets. Cargo's resolver must pick a single version
*and* a unified feature set such that all consumers get at
least what they asked for.

This is unique among major package ecosystems — npm has
optional dependencies but not the union-of-features semantics;
Python has extras but they don't unify across consumers; Go
has no analogue at all.

Feature unification is **load-bearing** for Cargo's
performance because it determines how many resolver
backtracks happen. The old resolver (`resolver = "1"`, the
default for edition 2018) over-unified: features requested in
build-dependency or target-specific contexts got merged into
the regular dependency graph, sometimes pulling in code that
shouldn't have been compiled. This caused both correctness
issues and resolution thrash. The new resolver
(`resolver = "2"`) tracks feature contexts separately —
build, target, and normal dependencies get independent
feature sets — at the cost of a more complex unification
algorithm.

The Sylk lesson: any ecosystem that supports optional
dependencies needs an explicit model of how those options
unify across the dependency graph, *and* the resolver must
expose this in the lockfile so re-resolves are
deterministic. The doc-mandated `Constraint` type needs a
features field if it's going to support cargo (and an extras
field for Python).

### 5. Lockfile semantics and re-resolution

`Cargo.lock` is checked into version control for binaries
and ignored for libraries — a deliberate split because
binaries want reproducible builds while libraries want their
downstream users to pick fresh versions.

When `Cargo.lock` exists, `cargo build` does **lockfile-driven
resolution**: every version is fixed; cargo verifies the
lockfile satisfies the manifest's constraints and complains if
not. This makes warm-cache builds nearly free — no metadata
fetches, no resolution work, just verify-and-go.

When the manifest changes (new dep, version bump), cargo runs
**incremental resolution**: it tries to keep the existing
lockfile entries and only re-resolve what changed. This is a
concrete instance of the LockfileSnapshot hint in the
substrate's `ResolveRequest` — and it's where cargo's resolver
shines vs npm's, which historically did much worse at
preserving existing pins on partial re-resolve.

The substrate adapter should treat `LockfileHints` as a
*hard preference*, not a soft hint: prefer to leave existing
pins in place; only consider new versions when the existing
pin is now invalid.

### Speedup attribution

| Source | Approximate share of speedup vs naive |
|---|---|
| Sparse index protocol (per-crate metadata files) | 50% |
| Content-addressed shared cache | 20% |
| HTTP/2 + native concurrency (Rust + Tokio) | 15% |
| Lockfile-driven re-resolution preserving pins | 10% |
| Algorithm (resolver=2 vs resolver=1; future PubGrub) | 5% |

Cargo's win is heavily protocol-level: ~50% of the speedup
comes from "the registry serves what the resolver needs."
Compare to uv where ~40% comes from client-side range-request
cleverness *to extract* what the registry should have served.
Same end state, very different engineering investment.

### Lessons for the Sylk Cargo adapter

The Cargo case is the easiest of the bunch to adapt because
the upstream protocol is already well-suited:

- **Use the sparse index, not the git index.** The git index
  is being deprecated; the sparse index is the default for
  modern Cargo and is the only thing the substrate adapter
  should target.
- **Treat `Cargo.lock` as `LockfileHints`.** Pass it through
  to `ResolveRequest`; honor its pins as hard preferences.
- **Reuse the `~/.cargo/registry` layout.** Don't reinvent the
  cache structure; the substrate's recipe-store can mount it
  directly or mirror its structure for content-addressed
  storage.
- **Implement `FrontierAwareResolver`.** When the substrate
  uses `pubgrub-rs` (or a Go port) under the hood, the
  frontier-driven prefetch wins still apply — even though
  cargo itself doesn't yet use them.
- **Model features in the `Constraint` type.** Without this,
  cargo crates that depend on optional features can't be
  expressed as substrate constraints.

### Lessons for the Sylk recipe-store protocol

When designing the substrate's *own* recipe-metadata API
(distinct from third-party ecosystem adapters), copy the
sparse index design:

- **Per-recipe-version metadata as a separate HTTP resource.**
  Stable URL, JSON or JSON-Lines, cacheable by CDN.
- **No metadata embedded in recipe archives.** The archive is
  the build artifact; the metadata is its own thing.
- **Append-only, content-addressed.** Recipe metadata files
  never change; updates are new files at new URLs.
- **Index files for high-churn recipes** — JSON-Lines with one
  line per published version, fetched as a single request.

This makes the substrate's first-party adapter trivially fast
(no range-request hackery needed) and makes the substrate
self-mirroring: the registry can be served from any HTTPS
endpoint, including a local proxy or air-gapped mirror.

---

## Case study: Go modules (Go / GOPROXY + MVS)

Go modules is the case where **algorithmic simplicity does most
of the work.** Cargo wins on protocol design; uv wins on
client-side cleverness; Go wins on choosing an algorithm that
makes most of those problems vanish in the first place. Minimum
Version Selection (MVS) has no backtracking, no constraint
solving, no SAT-like search — the "algorithm" fits in a paragraph.
Combined with a metadata-first HTTP protocol (`GOPROXY`) and a
content-addressed cache, the result is a resolver that is
*structurally* fast: there's almost nothing to optimize because
there's almost nothing happening.

The lesson here is the inverse of the cargo lesson: cargo shows
that *protocol design* can absorb resolver complexity. Go shows
that *algorithm choice* can eliminate resolver complexity
altogether. Sylk gets to choose: when designing first-party
substrate ecosystems, MVS-style "least amount of work that
satisfies constraints" is worth considering even though it
costs flexibility, because the resolver implementation
collapses to a few hundred lines.

### 1. MVS — the algorithm that doesn't need a fast implementation

The full Minimum Version Selection algorithm:

1. Read the module's `go.mod`. Note its direct dependencies and
   the minimum version it requires for each.
2. For each direct dependency, recursively read *its* `go.mod`
   and accumulate its requirements.
3. For each module in the accumulated set, pick the **maximum
   of all minimum versions** anyone requested.
4. Done.

That's it. There is no version range to satisfy, no backtracking
when constraints conflict, no candidate consideration. A module
either gets included at exactly the highest minimum-version
anyone asked for, or it doesn't get included at all. Conflicts
are *impossible*: the union of "minimum X required by anyone" is
always well-defined.

Compare to PubGrub: PubGrub solves the NP-hard problem of finding
*any* satisfying assignment to a constraint system, then proves
optimality. MVS solves a trivial problem (compute a maximum) by
construction. The "resolver" in MVS is barely an algorithm — it's
a graph traversal with a max-reduce at each node.

The trade-off is **conservatism**: MVS picks older versions than
necessary. If module A requires `B >= 1.2.0` and B is at 2.0.0,
MVS picks 1.2.0 unless something else explicitly bumps it. The
philosophy is "add a constraint or accept the conservative pick" —
no resolver-driven version inflation.

This conservatism is itself a performance win: MVS produces the
same answer every time given the same inputs, so re-resolution is
incremental by construction. There's no "the resolver explored
candidate space differently this time" class of problem.

### 2. The GOPROXY protocol — metadata as separate cacheable resources

`GOPROXY` is the HTTPS module proxy protocol. The default proxy
(`proxy.golang.org`) and any conforming mirror serves four URLs
per module version:

```
GET /{module}/@v/list           # all available versions, plain text
GET /{module}/@v/{version}.info # JSON metadata (timestamp, etc.)
GET /{module}/@v/{version}.mod  # the go.mod file — dependency manifest
GET /{module}/@v/{version}.zip  # the source archive
```

The critical resource is **`.mod`**: a few-line file listing this
version's direct dependencies. Resolving a Go project requires
fetching one `.mod` per module-version in the closure. For a
typical project, that's tens to a few hundred small files,
fetched concurrently against a CDN.

This is structurally identical to cargo's sparse index — and like
cargo, the protocol design means a naive resolver is already
fast. There's no metadata to extract from a container format, no
range-request cleverness needed. The dependency manifest is its
own resource.

`.info` and `.list` are similarly tiny and cacheable. The `.zip`
(the actual source) is fetched only at materialization time —
the resolver itself never needs the source.

### 3. `go.sum` — content-addressed integrity baked into the protocol

Every module version has a SHA-256 hash recorded in
`go.sum`. The hash covers both `.mod` and `.zip`. When a module
is fetched, Go verifies the hash before using it; mismatches are
hard errors. The proxy itself can be untrusted because the hash
is checked at the edge.

This collapses several concerns into one mechanism:

- **Lockfile semantics** — `go.sum` pins exact bytes, not just
  versions. A `go.sum` plus a `go.mod` is sufficient to
  reproduce a build byte-for-byte across machines.
- **Cache integrity** — `$GOPATH/pkg/mod/cache/download` stores
  fetched archives keyed by `(module, version)`; the hash
  provides verification at every read.
- **Mirror trust** — alternative GOPROXY hosts (corporate
  mirrors, air-gapped proxies) are safe to use because the
  hash check happens locally. The proxy can serve anything;
  the build fails loudly if it lies.
- **Sumdb checkpoint** — `sum.golang.org` is a transparent log
  of all known module hashes. Builds verify against the log
  to detect retroactive tampering.

This is a content-addressed-everything design. Sylk's substrate
already has this pattern (per `TOOL_VFS.md`'s recipe-store);
Go's `go.sum` is the cleanest small-scale example of integrating
verification into the protocol rather than bolting it on.

### 4. No backtracking → trivial prefetch, no cancellation needed

Because MVS doesn't backtrack, there's no concept of "the
resolver considered this candidate and then abandoned it."
Every module the resolver fetches metadata for is *definitely*
in the final closure. Prefetch is therefore degenerate:

```
For each direct dependency in go.mod:
  Fetch its .mod file.
For each new module discovered in any fetched .mod:
  Fetch its .mod file.
Repeat until quiescent.
```

No cancellation, no orphaned fetches, no decision frontier to
expose. Go's module loader uses a goroutine pool to fan out
fetches breadth-first; a typical resolution is a few hundred
parallel HTTPS GETs against a CDN, completed in seconds even
for large dependency trees from cold cache.

The `FrontierAwareResolver` interface proposed for the substrate
is **unnecessary** for MVS-based adapters. They implement only
the base `Resolver` interface, and they don't lose anything by
doing so — there's nothing to be aware of in the first place.
This is a design strength, not a weakness: the interface
extension is opt-in for resolvers that need it; resolvers that
don't need it pay no overhead.

### 5. `GOPATH/pkg/mod` cache + `$GOCACHE` build artifact cache

Go's cache hierarchy:

- **`$GOPATH/pkg/mod/{module}@{version}/`** — extracted source
  trees, immutable, content-addressed. Shared across every
  project on the machine. The `vendor/` directory in a project
  is opt-in and copies from this cache.
- **`$GOPATH/pkg/mod/cache/download/{module}/@v/`** — the
  raw downloaded files (`.mod`, `.info`, `.zip`) before
  extraction.
- **`$GOCACHE`** (default `~/.cache/go-build`) — per-target
  build artifacts (`.a` files, executables) keyed by content
  hash of source + compiler version + build flags. Fully
  deterministic; identical inputs produce identical artifacts.

The build cache is the unsung hero of Go's "fast builds"
reputation. A build is a series of cache lookups: for each
package, hash its source + dependencies' artifact hashes +
build configuration; check `$GOCACHE`; if hit, link the
existing artifact. Most builds in a working developer's flow
are 90% cache hits.

What Go does *less* well than uv: there's no reflink-based
materialization at the project level. If a project uses
`vendor/`, the cache is *copied* into the project tree.
Reflinks would make this free; Go doesn't use them. (This is
fixable but historically hasn't been a priority because
vendoring is a minority workflow.)

### Speedup attribution

Comparing Go's resolver to a hypothetical naive implementation
of the same algorithm (i.e. one that fetches `.zip` files to
extract `.mod`, runs serially, ignores the cache):

| Source | Approximate share of speedup vs naive |
|---|---|
| MVS algorithm (no backtracking) | 35% |
| GOPROXY protocol (`.mod` as dedicated resource) | 30% |
| Content-addressed cache + `go.sum` integrity | 20% |
| Native concurrency (goroutines) | 10% |
| Build artifact cache (`$GOCACHE`) | 5% |

This decomposition is unique among the case studies because
the **algorithm itself** is a major contributor. uv and cargo
are constrained to PubGrub-like solvers because their
ecosystems require version-range satisfaction; Go traded that
expressiveness for an algorithm that's nearly free to run.
The resulting protocol and cache choices fall out naturally —
it's hard to over-engineer something that's already trivially
cheap.

### MVS's failure mode: the diamond dependency problem

MVS's conservatism produces a known failure mode: if module A
needs `B >= 1.2.0` and module C needs `B >= 1.5.0` *and B
1.5.0 has breaking changes from 1.2.0*, MVS picks 1.5.0 and A
silently breaks at runtime. There is no resolver feedback —
MVS's worldview is "the maximum of minimums is always
correct."

Go's solution is **Semantic Import Versioning (SIV)**: major
versions are part of the import path. `github.com/foo/bar/v2`
is a *different module* from `github.com/foo/bar`. A project
can depend on both simultaneously; the resolver treats them as
independent.

This works but pushes complexity onto the ecosystem: every
breaking change requires renaming the import path. Library
authors don't always do this correctly; Go's tooling enforces
it imperfectly. The trade-off is intentional — Go's designers
chose to make breaking changes *socially expensive* rather
than make the resolver smarter.

The Sylk lesson is: the algorithm/protocol/social-norm
trade-offs are linked. A choice to use MVS implies a commitment
to SIV-style breaking-change discipline. A choice to allow
arbitrary version ranges implies a commitment to PubGrub-class
resolver complexity. Pick the trade-off, then the rest of the
system falls out.

### Lessons for the Sylk Go adapter

The Go case is the easiest of the bunch by a wide margin:

- **Use the GOPROXY protocol.** The substrate's Go adapter is
  fundamentally a GOPROXY client. Reuse `golang.org/x/mod/module`
  for parsing module paths; reuse `golang.org/x/mod/modfile`
  for parsing `.mod` files.
- **Treat `go.sum` as the lockfile hint.** Pass through to
  `LockfileHints`; verify hashes before honoring.
- **Don't implement the substrate's `FrontierAwareResolver`
  interface.** MVS doesn't need it; the base `Resolver`
  interface is sufficient.
- **Mount `$GOPATH/pkg/mod` directly into the recipe store.**
  Don't copy; the layout is already content-addressed and
  immutable.
- **Honor `$GOPROXY` and `$GOPRIVATE` env vars.** Air-gapped
  Sylk deployments will need to point at internal mirrors;
  Go's env-var convention is the obvious surface for this.

### Lessons for the Sylk substrate protocol

Two lessons specifically from Go's design that the substrate
should consider:

- **Hash verification at the edge.** The substrate's recipe
  store should record content hashes alongside version
  metadata, and the materializer should verify before use.
  `go.sum`-style transparent logs (`sum.golang.org`) are a
  template for catching retroactive tampering; the substrate
  may or may not need this depending on threat model.
- **Algorithm choice as a load-bearing decision.** The
  substrate's first-party recipe ecosystems get to pick the
  algorithm. MVS is a strong default for *internal* recipes
  where breaking changes can be coordinated. PubGrub is
  necessary only when consumers can't coordinate (third-party
  ecosystems). Don't reach for PubGrub by reflex when MVS
  would do.

---

## Case study: npm + Arborist vs `bun install` (Node / npm registry)

The npm case is unique in this doc: it's two case studies in
one. **npm + Arborist** is the pathology — the slowest mainstream
resolver in active use, slow not because anyone's incompetent
but because the protocol, the constraint model, and the runtime
all conspire against it. **`bun install`** is the response —
proof of how much speedup is recoverable when you fix
implementation quality alone, holding the protocol and constraint
model fixed.

The lesson here is the inverse of every prior case study. uv
extracted speed from a bad protocol via client cleverness. Cargo
got speed from protocol design. Go got speed from algorithm
choice. **Bun gets speed from implementation quality** — same
protocol as npm, same constraint model, same lockfile semantics
roughly, 10–30× faster. When you can't change the protocol or
the algorithm, the implementation *is* the resolver.

This matters for Sylk because Sylk's npm adapter is in the same
position as Bun: locked into the npm registry, locked into peer-
dependency semantics, locked into producing a `node_modules`
layout that Node's resolver can find. The only lever is
implementation quality — and the lever is huge.

### 1. The npm registry protocol — the original sin

The npm registry serves a single JSON document per package
containing the entire publish history: every version ever, every
dependency declaration, every metadata field. `express`'s full
registry response is ~10 MB. To resolve `express`, a client
fetches all 10 MB even though it cares about a handful of
versions.

There is **no per-version metadata endpoint** like cargo's
sparse index (`/{prefix}/{prefix2}/{crate}`) or Go's `.mod` URLs
(`/{module}/@v/{version}.mod`). The protocol was designed in
2010 for a much smaller ecosystem and has since ossified — too
many tools depend on the existing shape to change it.

This is the structural constraint everything else flows from. uv
works around the wheel-METADATA problem with HTTP Range
requests because PyPI also predates "resolvers should fetch
metadata cheaply" — but PyPI's failure mode is "metadata is
buried inside an archive" which range requests can extract. npm's
failure mode is "metadata is bundled with every other version's
metadata in one mega-document" which no client trick can
disentangle. You parse the whole document or you parse nothing.

### 2. Peer dependencies — the constraint model from hell

npm has the most complex constraint model of any major
ecosystem. A package can declare:

- `dependencies` — installed, child of this package
- `devDependencies` — installed during dev only
- `peerDependencies` — must be present *somewhere in scope*; the
  consuming app is expected to provide them
- `peerDependenciesMeta.X.optional: true` — peer is optional
- `optionalDependencies` — try to install, ignore failure

Peer-dep semantics mean two consumers of the same package may
need *different copies* of a transitively-shared dependency to
satisfy *their* peer constraints. This produces "doppelgangers" —
multiple physical copies of the same `(name, version)` at
different positions in the `node_modules` tree.

Then **hoisting** has to decide where to physically place each
package. To minimize duplication and keep Node's CommonJS
resolver able to find everything, npm tries to put each package
as close to the root of `node_modules` as possible without
conflicting. This is NP-hard in the general case; npm uses
heuristics with multiple convergence passes.

PubGrub doesn't model peer dependencies. MVS doesn't model
optional dependencies. The npm resolver is its own beast: a
hand-rolled constraint solver designed around npm's particular
pathologies. Sylk cannot reuse a generic algorithm here; the
adapter must implement npm's semantics natively.

### 3. Arborist — npm's resolver since npm 7

Arborist, introduced in npm 7 (2020), replaced the older
"ideal tree / actual tree" reconciler. It runs in JavaScript on
Node.js. Its job is to:

1. Read `package.json` and existing `package-lock.json`
2. Resolve all version ranges across the dependency graph
3. Propagate peer dependency constraints across the tree
4. Compute the hoisting placement
5. Detect cycles, handle workspaces, model overrides
6. Emit a unified tree to the materializer

The algorithm is **multi-pass** because peer-dep propagation
doesn't converge in one go: a placement decision for package A
may invalidate a peer constraint for package B, requiring B's
placement to change, which may invalidate A's, and so on.
Arborist iterates until quiescent.

Time decomposition for a typical large-project `npm install`
cold-cache (30s–2min total):

- ~40% parsing 10 MB+ JSON registry responses with V8's
  `JSON.parse`
- ~20% Arborist's multi-pass peer-dep + hoisting algorithm
- ~20% byte-copying packages into `node_modules` (with
  doppelgangers, the same files copied multiple times)
- ~10% V8 startup, JIT warmup, GC pauses
- ~10% re-parsing `package-lock.json` (often megabytes)

Notice: the algorithm is one of five contributors. The biggest
single line item is JSON parsing of registry responses. The
second is byte-copy materialization. Both are implementation
concerns, not algorithm concerns.

### 4. `bun install` — the implementation-quality response

Bun (oven-sh/bun) is a Node-compatible runtime + package
manager + bundler written in Zig. The package-manager component
treats npm as a fixed protocol target: same registry, same
peer-dep semantics, structurally compatible lockfile. It
optimizes the implementation aggressively and lands 10–30×
faster than npm.

Bun's five engineering moves:

**Binary lockfile (`bun.lockb`).** Custom binary format,
mmap-able, parseable in microseconds. Unlike `package-lock.json`
(text JSON, often megabytes), there's no per-install JSON parse
cost. Trade-off: diffs are unreadable in code review; mitigated
by `bun pm` inspection tools. The semantic content is
equivalent to `package-lock.json` — Bun can convert between the
two formats.

**SIMD-accelerated JSON parser** hand-tuned in Zig for the
npm registry's response shape (deeply-nested version maps with
many repeated keys, predictable string-heavy structure).
Roughly 5–10× faster than V8's `JSON.parse` on these
documents. This is probably the single largest contributor to
Bun's win — registry parsing was the biggest line item in the
npm decomposition.

**Native HTTP/2 client** with connection pooling. Custom Zig
stack, multiplexed connections to `registry.npmjs.org`. No
Node.js overhead.

**Hardlinked global cache.** Packages live in
`~/.bun/install/cache/{name}/{version}/` once globally.
Materializing `node_modules` is a `link()` syscall per file —
filesystem metadata only, no byte copy. Same pattern pnpm
pioneered. A 100-package install is milliseconds rather than
seconds. On btrfs/APFS/ZFS/XFS, Bun uses reflinks (CoW) for
the same speed without the inode-sharing semantics of
hardlinks.

**Concurrent everything.** Registry fetches, downloads,
extraction, and materialization fan out across CPUs. Zig has
no GIL and no GC pauses; the saturation of CPU and network is
near-optimal.

What Bun keeps from npm: the protocol (registry API), the
algorithm (peer-dep + hoisting semantics, including npm 9's
exact placement rules), the on-disk `node_modules` layout
(doppelgangers and all). What it changes: only the
implementation. The result is the same dependency tree
Arborist would produce, materialized on disk identically,
computed 10–30× faster.

### Speedup attribution

Bun's speedup over npm, decomposed:

| Source | Approximate share of speedup vs npm |
|---|---|
| SIMD JSON parser tuned for npm registry shape | 30% |
| Binary lockfile (mmap, no parse) | 20% |
| Hardlink/reflink materialization (vs byte copy) | 20% |
| Native code (no V8 startup, no GC pauses, no GIL) | 20% |
| HTTP/2 with shared connection pool | 5% |
| Algorithm (essentially unchanged) | <5% |

The algorithm contributes nothing. The protocol contributes
nothing. Implementation is the entire game.

### Lessons for the Sylk npm adapter

Sylk's npm adapter is constrained the same way Bun is: locked
into the npm registry, locked into peer-dep semantics, locked
into producing a `node_modules` layout Node's resolver can
find. The only lever is implementation quality. Five layers,
in order of build priority (highest leverage first):

#### Layer 1 — Registry client (highest-leverage win)

- **HTTP/2 connection pool** to `registry.npmjs.org`. Go's
  `net/http` does HTTP/2 by default; ensure one shared
  `http.Client` per adapter instance, not per request.
- **Streaming JSON parser** for registry responses.
  `encoding/json` buffers the whole document, which is fatal
  for 10 MB responses with thousands of versions. Use
  `github.com/valyala/fastjson` or hand-rolled SAX-style
  parsing that pulls only the version keys needed and skips
  the rest. A 10 MB document parsed at ~20ms instead of
  ~200ms is the single largest win available without writing
  custom SIMD code.
- **Per-package metadata cache** in the substrate's
  recipe-store SQLite, keyed by `(package, etag)`. The npm
  registry sends `Etag` headers; respect them. Cache hits
  skip the network entirely.
- **Negative cache** — "no version of X satisfies Y" cached
  per-resolve so Arborist-style backtracks don't repeat work.

This layer alone gets Sylk most of the way to Bun.

#### Layer 2 — Resolver (don't get clever; copy Arborist)

- **Match npm 9 peer-dep semantics exactly.** This is not a
  place to invent something simpler — many npm packages
  depend on the exact placement and resolution rules npm
  uses. Get this wrong and packages fail at runtime in
  subtle ways the user can't debug.
- **Implement in Go for native concurrency.** Arborist's
  multi-pass nature parallelizes well: each pass over the
  tree is data-parallel.
- **Do NOT use PubGrub for npm.** PubGrub doesn't model peer
  dependencies; the constraint shape is wrong. The
  substrate's generic `Resolver` interface accommodates this
  — the npm adapter's resolver is a custom solver
  implementing the interface, just like cargo's adapter
  wraps `pubgrub-rs` and Go's adapter does MVS.
- **Skip `FrontierAwareResolver`.** Arborist's multi-pass
  algorithm doesn't have a clean PubGrub-style frontier. You
  can wire prefetch off "package version decided" events as
  a partial substitute, but the speedup is smaller than for
  PubGrub-based adapters. The base `Resolver` interface is
  sufficient.

#### Layer 3 — Materializer (second-highest-leverage win)

- **Single global cache** under the substrate's recipe-store,
  keyed by `(package@version, integrity-hash)`. Identical
  pattern to cargo's `~/.cargo/registry`, Bun's
  `~/.bun/install/cache`, uv's content-addressed wheel cache.
  Every project on the machine shares the same package
  bytes.
- **Hardlink materialization** for `node_modules`. Linux and
  macOS native; Windows falls back to copy or NTFS junctions.
  The result: `node_modules` directories are
  filesystem-metadata-only operations.
- **Reflinks (CoW) on btrfs/APFS/ZFS/XFS** when available.
  Bonus efficiency on supported filesystems; falls back
  gracefully to hardlinks elsewhere.
- **Never byte-copy** unless materializing across
  filesystems. Cross-FS materialization is a fallback path,
  not the default.

#### Layer 4 — Lockfile (keep it portable)

- **Output `package-lock.json`** (npm-compatible) so the
  substrate's npm recipes integrate with non-Sylk Node
  tooling. Developers running `npm ci` outside Sylk should
  get the same tree.
- **Internally**, store a pre-parsed binary representation in
  the substrate's recipe-store (analogous to `bun.lockb` but
  as substrate metadata, not a file in the project). Avoids
  re-parsing on every operation while keeping the on-disk
  lockfile in the canonical format.
- **Honor existing `package-lock.json` as a hard preference**
  in `LockfileHints` — same pattern as cargo and Go.

#### Layer 5 — Things to explicitly NOT do

- **Don't ship pnpm's symlinked `node_modules` layout.** It's
  clever but breaks packages that traverse `node_modules`
  looking for files (some Webpack plugins, older bundlers,
  native modules with custom build steps). Hardlinks are
  safer because the on-disk shape is identical to npm's.
- **Don't ship a Bun-style runtime.** That's a different
  scope entirely. The substrate's npm adapter resolves and
  materializes; the actual Node.js runtime is the user's
  choice (system Node, nvm-managed Node, Bun, Deno).
- **Don't write a SIMD JSON parser unless benchmarks
  demand it.** Most resolves are I/O-bound; CPU JSON parsing
  matters only for the largest registry responses. fastjson's
  streaming approach is usually sufficient.
- **Don't try to deduplicate registry data across packages**
  beyond what the cache key already gives you. The
  registry's data is per-package; cross-package
  deduplication is over-engineering.

### Expected position

If Sylk's npm adapter implements layers 1–4 correctly, it
should land **5–15× faster than npm, 1.5–3× slower than Bun**.
The remaining gap to Bun comes from:

- Go's GC vs Zig's manual memory management (small, ~10–20%
  on hot paths)
- No SIMD JSON parser by default (~10–20% on parse-heavy
  workloads)
- Substrate cache indirection (small win for
  cross-ecosystem locality, small cost for npm-specific
  access)

That's the right place to land. Beating Bun isn't the goal;
the goal is that npm package installs don't bottleneck Sylk
pipelines, and that the npm adapter plugs into the substrate's
content-addressed cache so Python recipes and Node recipes
share locality benefits.

### Lessons for the Sylk substrate protocol

The npm case offers exactly one lesson for substrate-level
design, and it's a *negative* one:

- **Never design the substrate's first-party recipe metadata
  API the way npm did.** Per-package mega-documents containing
  full publish history are the worst possible shape for
  resolvers. Design like cargo's sparse index (per-version
  metadata at stable URLs) or Go's GOPROXY (`.mod` files as
  dedicated dependency manifests) instead.

The substrate's *adapters* for third-party ecosystems must
work around protocol limitations imposed by upstream. The
substrate's *own* protocols should never impose those
limitations on its adapters.

---

## Case study: RubyGems + Bundler (Ruby / rubygems.org)

This is the **ecosystem-learns** case study. Bundler is the only
mainstream resolver where we can observe a complete before/after
of a protocol migration — the same resolver, the same algorithm,
the same runtime, fronted by three successive metadata protocols
over a decade. Each transition produced a measurable speedup;
the largest was an order of magnitude. The lesson isn't about
any single technique — it's about what happens when you ship the
wrong protocol and have to migrate the entire ecosystem to fix
it.

The corollary lesson is about **algorithm-replacement timing.**
Bundler ran on Molinillo, a hand-rolled backtracking solver, from
2014 through 2022 — the same algorithmic core for nine years
while the protocol underneath went through three generations.
The biggest performance wins came from the protocol changes, not
algorithm work. Then in January 2023 (Bundler 2.4), the team
[migrated to PubGrub](https://bundler.io/blog/2023/01/31/bundler-v2-4.html)
via the [jhawthorn/pub_grub](https://github.com/jhawthorn/pub_grub)
Ruby implementation. The migration produced a meaningful
single-digit-percent speedup on typical resolves and a much
larger improvement on backtrack-heavy projects, *plus*
human-readable conflict explanations that Molinillo couldn't
produce.

The takeaway isn't "PubGrub is better, always migrate" — it's
that algorithm work pays off mostly when other bottlenecks have
been cleared. Bundler spent a decade fixing the protocol and
runtime issues before algorithm replacement was the
highest-value remaining lever. Sylk's adapters can choose
algorithms freely from day one *because* the substrate's
protocol and cache architecture are right; the trade-off
calculus is different from an ecosystem inheriting decades of
legacy decisions.

For Sylk: the RubyGems adapter is structurally easy (the
modern protocol is good, the algorithm is well-understood), but
the *meta-lesson* — protocol migration is painful, ship the
right one from day one — should shape how the substrate's
first-party recipe-store API is designed.

### 1. The original Marshal-index protocol — what not to do

When RubyGems launched in 2003, the registry distributed its
entire index as a single Marshal-serialized file (Ruby's binary
serialization format). To resolve dependencies, a client
downloaded the *entire* index — every gem, every version, every
dependency relationship — as one blob. The 2010-era index was
~50 MB; by 2014 it was approaching 200 MB.

Three failure modes compounded:

- **Cold-start was minutes**, not seconds. Downloading 200 MB
  before resolution could begin made `bundle install` on a
  fresh machine a coffee-break operation.
- **The index couldn't be cached effectively** at the CDN edge
  because it was a single mutable resource that updated on
  every gem publish. A daily-pulled index meant every CI run
  paid the full download.
- **Marshal-format parsing was Ruby-only.** The registry was
  effectively unusable from non-Ruby tooling without
  reimplementing Ruby's serialization format. Cross-language
  ecosystem participation was zero.

The protocol's defining mistake was the same one npm made:
treating the *registry* as the unit of distribution rather than
treating *each package version* as its own resource. uv's
range-request hackery, Bun's SIMD JSON parser, every other
case study's optimizations are downstream consequences of bad
protocol decisions like this one. Avoid the mistake at protocol
design time and the optimizations become unnecessary.

### 2. The dependency API — partial fix, new pathology

In 2010, RubyGems added a JSON dependency API:

```
GET /api/v1/dependencies?gems=foo,bar,baz
```

This returned the dependency lists for the requested gems — a
significant improvement over downloading the entire index, but
with its own failure modes:

- **Round-trips per N gems.** Resolving a 200-gem project
  required dozens of round-trips, each ~100ms over a typical
  connection. Total ~5–10s of pure latency.
- **Server-computed responses.** The endpoint was *not* a
  static resource; the server computed dependency closures on
  every request. CDN caching was minimal because the response
  varied with the query string.
- **No incremental updates.** A resolve of 199 cached gems
  plus 1 new gem still hit the server; there was no way to
  ask "what's changed since I last resolved?"

Bundler used this API by default for ~5 years. It was faster
than the Marshal index for first-time resolves but slower for
warm-cache resolves because every invocation re-hit the server.
The pathology: bandwidth saved, latency added.

The substrate-design lesson here is subtler than "static
resources are good." It's that **server-computed metadata APIs
trade one bottleneck for another.** They feel modern (JSON,
REST-ish, parameterized) but they break the cache hierarchy
and serialize on the server. cargo's sparse index avoids this
by serving fully-precomputed per-crate metadata files.

### 3. Compact index — the fix that worked

The compact index protocol (2015, shipped in Bundler 1.10)
solved the metadata problem properly:

```
GET /info/{gem}            # static text file with all versions
                           # of {gem} and their dependency lists
GET /versions              # newline-delimited list of all gems +
                           # their version histories
```

Each per-gem `/info/{gem}` file is a few KB, fully static, CDN-
cacheable forever (gem versions are immutable; new versions
append to the file). The `/versions` endpoint is a single
incrementally-appendable file that Bundler uses to detect
whether anything changed since its last resolve.

The structure is essentially identical to cargo's sparse index
(which shipped seven years later, in 2022) and Go's GOPROXY
`.list` + `.mod` URLs (2018). Bundler shipped it first.

The performance impact was dramatic. On typical projects:

- Cold-cache `bundle install`: 60s+ → 10s
- Warm-cache `bundle install`: 30s → 3s
- Backtrack-heavy resolves (where Molinillo explores many
  candidate combinations): 5min+ → 30s

The migration to compact index took roughly two years
(2015–2017) for the Ruby ecosystem to fully adopt. Bundler had
to retain support for the old API as a fallback because not
every gem mirror implemented compact index initially. The
fallback path was quietly removed in Bundler 2.x once
rubygems.org and the major mirrors had converged.

This is the only case study in the doc where the speedup
attribution can be *measured* against the same resolver
implementation:

| Bundler version | Protocol | Typical project install time |
|---|---|---|
| Bundler 1.0 (2010) | Marshal index | 2-5 min |
| Bundler 1.5 (2014) | Dependency API | 30-60s |
| Bundler 1.10+ (2015) | Compact index | 10-15s |
| Bundler 2.x (current) | Compact index, refined | 5-10s |

The protocol change accounts for ~70% of the speedup. The
remaining 30% comes from a decade of resolver constant-factor
improvements, Ruby runtime improvements (YJIT, faster GC), and
network stack improvements. The algorithm itself never changed.

### 4. Molinillo (2014–2022) → PubGrub (2023–present)

Bundler's resolver history splits cleanly across the 2023
migration. Both halves are useful for different lessons.

**Molinillo era (Bundler 1.x through 2.3, 2014–2022).**
Molinillo, [extracted from Bundler in 2014](https://github.com/rubygems/rubygems/pull/1189)
by Samuel Giddins, is a hand-rolled conflict-driven
backtracking resolver. It's not PubGrub — it lacks formal
unit propagation and derivation-based conflict learning — but
implements the same broad shape: greedy candidate selection,
backtracking on constraint failure, lockfile-aware ordering.
For nine years it was Bundler's algorithmic core through three
protocol generations.

The Molinillo era's lesson is that **a well-implemented
hand-rolled backtracker is competitive with a formally-optimal
solver for typical dependency trees** — single-digit percent
performance gap on realistic Ruby projects. The algorithm
wasn't where Bundler's time went, so replacing it wasn't the
biggest available win. Compact-index protocol adoption (2015)
delivered ~10× more speedup than the eventual PubGrub
migration would.

Molinillo is still in active use elsewhere: the **rubygems
gem itself** (the lower-level package installer that Bundler
sits on top of) ships Molinillo vendored at
`lib/bundler/vendor/molinillo`, and **CocoaPods** uses it for
iOS/macOS dependency resolution. Both ship it because
Molinillo is small (~2K LOC), well-tested, and the projects
using it haven't hit the backtrack-heavy pathology that drove
Bundler to migrate.

**PubGrub era (Bundler 2.4+, January 2023–present).**
[Bundler 2.4 migrated to PubGrub](https://bundler.io/blog/2023/01/31/bundler-v2-4.html)
via [jhawthorn/pub_grub](https://github.com/jhawthorn/pub_grub),
a Ruby port of the CDCL-based version solver originally
designed for Dart's `pub` package manager. The migration was
multi-year work tracked in
[rubygems/rubygems#5960](https://github.com/rubygems/rubygems/pull/5960).
Two motivations:

- **Backtrack performance.** Molinillo's conflict learning was
  weaker than PubGrub's derivation rules, so pathological
  resolves (where many constraint combinations had to be
  explored) were dramatically slower. PubGrub's unit
  propagation often cuts these resolves from minutes to
  seconds.
- **Diagnostic quality.** PubGrub's derivation history
  produces human-readable conflict explanations
  ("`gem A` requires `gem B >= 2.0` because... but `gem C`
  requires `gem B < 1.5` because..."). Molinillo's
  conflict messages were notoriously cryptic — a frequent
  source of Ruby community complaint.

For *typical* (non-backtrack-heavy) projects, the migration
produced a single-digit-percent speedup. The big wins are on
the long tail of resolves that previously took minutes; those
are now seconds. The diagnostic improvement is universal.

This puts Bundler in the same algorithm-family as Cargo
(in-progress migration to `pubgrub-rs`), uv, Swift Package
Manager, Hex, and pub itself. PubGrub has become the
de-facto standard for backtracking-class resolvers across
ecosystems.

**What this means for the substrate `FrontierAwareResolver`
extension.** Bundler's PubGrub adoption is recent enough that
the integration doesn't yet expose a candidate-consideration
event stream — pub_grub runs synchronously inside Bundler's
loop. Future work could wire this up, the same way uv does
with its Rust pubgrub-rs integration. For Sylk: a Go port of
PubGrub (or a wrapper around `pubgrub-rs` via FFI/cgo) should
expose the frontier from day one rather than retrofit it
later.

### 5. Ruby runtime constraints — the constant-factor floor

Bundler runs in Ruby. Ruby has structural performance
constraints that no resolver-level optimization can overcome:

- **GVL (Global VM Lock).** Like Python's GIL, Ruby's GVL
  serializes Ruby code across threads. I/O can run in
  parallel (network fetches release the GVL); pure-Ruby
  resolution work cannot.
- **Startup time.** Loading Bundler + dependencies takes
  300–500ms before any user code runs. Most CLI invocations
  pay this cost. YJIT (since Ruby 3.1) helps for long-
  running operations but doesn't reduce startup.
- **Verbose lockfile.** `Gemfile.lock` is YAML-like text. For
  large projects it's 10s of KB and re-parsed on every
  Bundler invocation. There's no binary fast-path the way
  Bun has.
- **Per-Ruby-version cache fragmentation.** Gems install into
  `~/.gem/ruby/{version}/gems/`. Switching between Ruby
  versions (rbenv, rvm, asdf) means re-downloading every
  gem because there's no global content-addressed cache.

These are the floor that Bundler can't push below without
either rewriting in a faster language (the Bun-style move) or
accepting the constraints. The Ruby ecosystem has chosen to
accept them — Bundler's "good enough" is acceptable to most
Ruby developers because the alternative would fragment the
ecosystem.

For Sylk, the lesson is that **runtime choice is a hard
performance ceiling**. Sylk's Go adapters automatically clear
the Bundler-style ceilings: no GVL, faster startup, native
binary serialization available, content-addressed caches
shared across language versions. Most of the constant-factor
work Bundler had to invest in is free for a Go-based
adapter.

### Speedup attribution (Bundler 2.4 vs Bundler 1.0)

| Source | Approximate share of speedup |
|---|---|
| Compact index protocol | 60% |
| Molinillo iterative improvements (2014–2022) | 10% |
| PubGrub migration (2023, biggest on backtrack-heavy projects) | 10% |
| Ruby runtime improvements (YJIT, GC, JIT) | 10% |
| Network stack improvements (HTTP/2, keepalive) | 5% |
| Lockfile format (no change) | 0% |

The protocol change still dominates — it's the largest single
contributor by a wide margin. The algorithm change in 2023 is
the second-largest *recent* contribution, but came after a
decade of work on the protocol and runtime. This sequencing is
the lesson: protocol and cache architecture before algorithm.

For the resolves where PubGrub matters most — the long tail
of backtrack-heavy projects with many conflicting version
constraints — the Bundler 2.3 → 2.4 transition produced order-
of-magnitude speedups. The 10% averaged share understates the
distributional impact.

### Lessons for the Sylk RubyGems adapter

The Ruby case is structurally easy. The hard work was done by
the ecosystem (compact index in 2015, PubGrub adoption in
2023). The adapter should:

- **Use the compact index protocol always.** The old API and
  Marshal index are deprecated; the substrate's adapter has no
  reason to support them.
- **Honor `Gemfile.lock` as the lockfile hint.** Pass through
  to `LockfileHints`; honor existing pins as hard preferences,
  same pattern as cargo and Go.
- **Use a Go PubGrub implementation** for the resolver, not
  Molinillo. Bundler's 2023 migration confirms PubGrub is the
  right choice for new RubyGems-shaped resolvers. Sylk's
  PubGrub adapter (also targeted at the Python case) is
  reusable here — the gem dependency model fits PubGrub's
  constraint shape cleanly. Implement
  `FrontierAwareResolver` from day one.
- **Don't shell out to Bundler.** Some adapters might be
  tempted to invoke `bundle install` as a subprocess. Don't —
  it pulls in the Ruby runtime, the GVL, the lockfile-parse
  cost, and per-Ruby-version cache fragmentation. The
  substrate's adapter implements gem resolution natively in
  Go and produces a `Gemfile.lock`-compatible lockfile for
  external Ruby tooling to consume.
- **Centralize the cache across Ruby versions.** Bundler
  doesn't do this; Sylk should. Store gems content-addressed
  in the substrate's recipe-store, materialize per-Ruby-
  version via hardlinks. Solves the cache-fragmentation
  problem Bundler can't fix without breaking compatibility.
- **Don't ship a Ruby runtime.** Same scope-discipline as the
  npm adapter: resolve and materialize, not run. The user's
  Ruby version is their choice (rbenv, rvm, asdf, system).
- **Note: if targeting CocoaPods recipes specifically, you
  still need Molinillo-compatible resolution semantics**
  because CocoaPods uses Molinillo, not PubGrub. The
  substrate's CocoaPods adapter (if shipped) is a separate
  concern from the RubyGems adapter even though both consume
  Ruby gems underneath.

### Lessons for the Sylk substrate protocol design

This is where the case study earns its place — Bundler's
protocol-migration history is the cleanest object lesson in
the doc:

- **Ship the right metadata protocol on day one.** The
  cost of a protocol migration is years of ecosystem-wide
  pain plus permanent compatibility shims. Bundler had to
  carry the dependency API as a fallback for a decade. npm
  has never managed to migrate at all. The substrate's
  first-party recipe-store API will be similarly hard to
  change once it has consumers — design it like the compact
  index from the start.
- **Per-resource immutability is the design crux.** A metadata
  API where each resource never changes (per-version
  dependency manifest at a stable URL) cleanly enables CDN
  caching, append-only updates, and incremental resolution.
  An API where the registry maintains aggregate state
  (full-index dump, computed query response) breaks all
  three.
- **Plan for migration anyway.** Even a well-designed
  protocol may need to evolve — new metadata fields, new
  integrity-hash algorithms, new platform classifiers.
  Version the metadata API explicitly (`/v1/info/{recipe}`,
  `/v2/info/{recipe}`) and budget for parallel deployment of
  old and new during transition. Bundler's painful migration
  was painful in part because the original protocol had no
  version negotiation; clients had to fall back via
  out-of-band signals.
- **Static text formats over binary or JSON.** The compact
  index uses a simple line-oriented text format because it's
  easy to append to (atomic file writes), trivial to parse
  in any language, and naturally streamable. Sylk's first-
  party metadata API should default to similar formats —
  protobuf or JSON only when the format complexity demands
  it.

The Ruby ecosystem paid millions of cumulative developer-hours
for the original Marshal-index decision. Sylk gets to make
that decision once and live with it for the substrate's
lifetime. Make it correctly.

---

## Case study: Composer (PHP / Packagist)

Composer is the **second ecosystem-migration case study** — the
PHP world's parallel to Bundler's compact-index story. Like
Bundler, Composer's first generation worked but was slow; like
Bundler, the project shipped a major version (Composer 2, October
2020) that combined a resolver rewrite with a new metadata
protocol; unlike Bundler's gradual two-year migration, Composer
2 shipped both changes in a single release and cut typical
operations 2–10× overnight.

For Sylk this is a useful parallel reference: Composer 2's
[Packagist API v2](https://blog.packagist.com/packagist-metadata-v2/)
is structurally very close to NuGet's PackageBaseAddress and
cargo's sparse index, and Composer's [`composer.lock`](https://getcomposer.org/doc/01-basic-usage.md#commit-your-composer-lock-file-to-version-control)
is the lockfile semantics every modern resolver converges on.
The case study earns its place by demonstrating that the
"compact index" pattern works equally well for ecosystems
adopting it a decade later — there's nothing PHP-specific about
the design.

### 1. Composer 1's metadata pathology — the v1 protocol

Pre-2020 Composer used Packagist's v1 metadata API, which served
**a single JSON document per package containing every published
version's full metadata**. Same fundamental shape as the npm
registry's per-package mega-documents — when resolving a popular
package like `symfony/console`, the client downloaded several
hundred KB of metadata covering every historical version, even
though only a few candidates were ever considered.

Two failure modes compounded:

- **Per-resolve bandwidth.** A typical Symfony app's
  `composer install` from cold cache fetched 50–100 MB of
  metadata across all dependencies. On constrained CI bandwidth
  this dominated wall-clock time.
- **Update detection.** Detecting whether any dependency had
  been updated required re-fetching every metadata blob. The v1
  endpoints were updated once per minute server-side, so even
  when nothing had changed the client paid the bandwidth.

Composer 1's resolver itself was a hand-rolled SAT-like solver
that worked correctly but had no conflict learning — backtracks
re-explored the same combinations. For projects with many
overlapping version constraints (typical for Symfony bundles or
Laravel package ecosystems), pathological resolves took
minutes.

### 2. Composer 2 — resolver rewrite + Packagist API v2

[Composer 2.0 (October 2020)](https://blog.packagist.com/composer-2-0-is-now-available/)
shipped both changes simultaneously. The ecosystem migrated
faster than Bundler's because Packagist serves both protocols
during transition (v1 still works for legacy clients) and
Composer 2 was opt-in initially before becoming default. As of
2024, [more than 95% of Composer updates use v2](https://blog.packagist.com/deprecating-composer-1-support/),
and Packagist is shutting down v1 support entirely.

The two changes:

**Packagist API v2** ([endpoint shape](https://packagist.org/apidoc)):

```
GET https://repo.packagist.org/p2/{vendor}/{package}.json
GET https://repo.packagist.org/p2/{vendor}/{package}~dev.json
```

Each URL serves the metadata for **one package**, with versions
listed as a JSON array — dev branches in the `~dev.json` file,
tagged releases in the `.json` file. The metadata is
**minified** ([composer/metadata-minifier](https://github.com/composer/metadata-minifier))
— since most fields don't change between adjacent versions, only
the deltas are encoded; the client expands them locally.

This is structurally identical to NuGet's PackageBaseAddress and
cargo's sparse index: per-package, static, CDN-cacheable,
incrementally fetchable. Combined with minification, payload
sizes dropped 70–80% vs v1 for typical packages.

**Resolver rewrite.** Composer 2's resolver added conflict
learning (similar to PubGrub's derivation rules without being
labeled as such), parallel metadata fetches via
[curl_multi](https://www.php.net/manual/en/book.curl.php), and
HTTP/2 connection reuse. The old resolver was sequential; the
new resolver fans out fetches across all dependencies
simultaneously.

**Speedup**: typical `composer install` 2–5× faster cold-cache,
3–10× faster warm-cache, and pathological backtracking resolves
went from minutes to seconds. The combined protocol+resolver
change is the largest single performance jump any mainstream
package manager has shipped in one release.

### 3. Platform requirements — runtime as primary constraint

Composer's `composer.json` supports a `platform` key declaring
runtime requirements:

```json
{
    "require": {
        "php": ">=8.1",
        "ext-mbstring": "*",
        "ext-pdo": "*",
        "vendor/package": "^2.0"
    }
}
```

`php` and `ext-*` (PHP extensions) are first-class entries in
the constraint graph. The resolver rejects package versions
whose own `require.php` doesn't satisfy the consuming project's
PHP version. This is the same pattern as OCaml's
`ocaml-base-compiler` constraint and Python's `python_requires`
filter, but more aggressive — Composer treats the runtime as a
real package in the resolution graph, not a precondition filter.

The Sylk lesson reinforces what OPAM's case study already made:
**runtime/compiler/extension versions belong in the constraint
graph, not as precondition filters.** Composer's
`platform-overrides` mechanism (lets you override the detected
PHP version for cross-platform CI builds) is also useful — the
substrate's adapter should support analogous override patterns
for adapters whose runtime version normally comes from the host.

### 4. `composer.lock` and `--prefer-stable` semantics

Composer requires a [`composer.lock`](https://getcomposer.org/doc/01-basic-usage.md#commit-your-composer-lock-file-to-version-control)
for every project — committed to version control for
applications, optionally not for libraries. `composer install`
honors the lockfile exactly; `composer update` re-resolves and
updates the lockfile. The semantics match cargo's `Cargo.lock`
and Bundler's `Gemfile.lock` discipline: lockfile-driven warm-
cache restores are the fast path.

Composer also has **stability flags** — `stable`, `RC`, `beta`,
`alpha`, `dev` — that filter which versions the resolver
considers. `--prefer-stable` is the default, excluding `dev`
versions unless explicitly requested. This is a useful pattern
for ecosystems where pre-release versions are common but should
require explicit opt-in.

### Speedup attribution (Composer 2 vs Composer 1)

| Source | Approximate share of speedup |
|---|---|
| Packagist API v2 (per-package metadata + minification) | 50% |
| Parallel fetches (curl_multi + HTTP/2) | 25% |
| Resolver rewrite (conflict learning) | 15% |
| Lockfile-driven re-resolution preserving pins | 10% |

Same shape as Bundler's attribution: protocol modernization
dominates, with the algorithm change as a secondary contributor.
The Composer 2 release is the cleanest single-release example of
this pattern in the doc — Bundler took two years to fully
migrate; Composer flipped overnight.

### Lessons for the Sylk Composer adapter

The PHP case is structurally easy because Packagist's modern
protocol is well-designed:

- **Use Packagist API v2 always.** v1 is being shut down; the
  substrate adapter has no reason to support it.
- **Implement metadata minification expansion.** v2 responses
  are delta-compressed; the substrate's adapter must apply the
  expansion algorithm or use the
  [composer/metadata-minifier](https://github.com/composer/metadata-minifier)
  reference. (Reimplementing in Go is ~50 LOC.)
- **Use a Go PubGrub implementation** for the resolver — same
  shared one targeted at Python, RubyGems, Hex, NuGet. The
  Composer constraint shape (semver ranges with stability flags)
  fits PubGrub cleanly.
- **Honor `composer.lock` as `LockfileHints`** — same pattern as
  every other adapter. Output `composer.lock`-compatible
  lockfiles for external Composer tooling to consume.
- **Treat `php` and `ext-*` as primary constraints** in the
  resolution graph, not as precondition filters. Support
  `platform-overrides` config for CI/cross-platform builds.
- **Implement `FrontierAwareResolver`.** PubGrub-based; the
  frontier is natural; expose it for substrate-level
  prefetching.
- **Honor stability flags.** Default to `stable`-only
  resolution; allow per-dependency or global stability override
  via the substrate's constraint type.

### Lessons for the Sylk substrate protocol

Composer adds one new substrate-level lesson on top of the
already-collected ones:

- **Metadata minification is worth the ~50 LOC of complexity**
  for ecosystems where adjacent-version metadata is mostly
  identical (most ecosystems — typical between-version diffs are
  one or two fields). The bandwidth savings are substantial
  (70–80% for Packagist) and the encoding/decoding is simple
  delta-compression. The substrate's first-party recipe-store
  metadata API should ship minification from day one.

---

## Case study: NuGet (.NET / nuget.org + private feeds)

The NuGet case study is about **multi-feed federation as a
first-class concern**. Every prior case study assumes one
canonical public registry: PyPI for Python, crates.io for Rust,
proxy.golang.org for Go, registry.npmjs.org for Node,
rubygems.org for Ruby. NuGet was designed from day one knowing
that .NET teams routinely combine the public nuget.org feed with
private corporate feeds, internal artifact servers, and local
package directories. The resolver consults all of them. The
protocol abstracts which feed serves what. The trust hierarchy
is built into the design.

This is uniquely relevant to Sylk because the substrate's own
recipe-store is, structurally, a private feed. Sylk users will
also have private feeds (corporate npm registries, internal
PyPI mirrors, GHE-hosted gem servers). The substrate must
abstract over which feed serves a recipe — and NuGet is the
only mainstream resolver that has solved this problem
properly.

There's a second, more recent angle: in November 2024, .NET 9
shipped a [completely rewritten NuGet resolver](https://devblogs.microsoft.com/dotnet/dotnet-9-nuget-resolver/)
that took restore times for Microsoft's largest internal
repository from 16 minutes to 2 minutes. The rewrite is a
clean object lesson in how representation choice (flat vs
graph) and conflict-resolution timing (eager vs deferred) can
produce 8× speedups without changing the protocol or the
algorithm's externally-observable behavior.

### 1. The service-index meta-protocol

NuGet's [v3 protocol](https://learn.microsoft.com/en-us/nuget/api/service-index)
starts with a service index — a JSON document at a feed's root
URL that declares which resources the feed implements:

```
GET https://api.nuget.org/v3/index.json
→ {
    "version": "3.0.0",
    "resources": [
      { "@id": "...", "@type": "PackageBaseAddress/3.0.0" },
      { "@id": "...", "@type": "RegistrationsBaseUrl/3.6.0" },
      { "@id": "...", "@type": "SearchQueryService/3.5.0" },
      ...
    ]
  }
```

The service index is a **capability declaration**, not the
metadata itself. Clients fetch it once per feed, learn which
resource URLs to use for each operation, and then use those
URLs directly for subsequent requests. New resource types
(SymbolPackagePublish, RepositorySignatures, Vulnerabilities)
can be added without breaking older clients — they just don't
use the new resource.

This is uniquely well-designed among the ecosystems in this
doc. Cargo, Go, RubyGems all hardcode their URL shapes into
the client. NuGet treats the feed itself as the source of
truth for what URL serves what resource. Adding a new feed
type (an internal corporate feed, a CI artifact server, an
S3-backed mirror) requires only that it serve a service index
— the resolver figures out the rest.

The Sylk substrate's recipe-store should adopt this pattern.
A single `/index.json` per feed declares capabilities; future
substrate versions can introduce new resource types without
breaking existing adapters.

### 2. Flat container (PackageBaseAddress) — per-version metadata at predictable URLs

The most-used resource is the [PackageBaseAddress](https://learn.microsoft.com/en-us/nuget/api/package-base-address-resource),
a.k.a. the "flat container":

```
GET {base}/{lower-id}/index.json
→ { "versions": ["1.0.0", "1.1.0", "2.0.0", ...] }

GET {base}/{lower-id}/{version}/{lower-id}.nuspec
→ <package metadata XML>

GET {base}/{lower-id}/{version}/{lower-id}.{version}.nupkg
→ <package binary>
```

Three observations:

- **Version listing is a single small static JSON file** per
  package. Same design pattern as Bundler's compact index
  (2015), Go's GOPROXY `.list` endpoint (2018), cargo's
  sparse index (2022). NuGet shipped this in v3 around 2015,
  contemporary with Bundler.
- **Per-version `.nuspec` metadata** is a separately
  addressable resource. Resolvers fetch only what they need;
  the `.nupkg` (the actual package binary) is downloaded only
  at install time, not during resolution.
- **Predictable URLs from package ID + version** mean any
  static-file mirror can serve as a feed. Internal corporate
  feeds are often just S3 buckets or Azure Blob containers
  serving these URLs.

The combination — service index + flat container — is the
template Sylk's substrate metadata API should follow. It's
the same pattern crates.io adopted seven years later.

### 3. Multi-feed resolution — the unique architectural concern

Every NuGet client (CLI, Visual Studio, dotnet restore) ships
configured with `nuget.org` as the default feed but expects
projects to add additional feeds. A typical .NET project's
`NuGet.config`:

```xml
<configuration>
  <packageSources>
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
    <add key="corporate" value="https://nuget.corp.example.com/v3/index.json" />
    <add key="ci-artifacts" value="https://ci.example.com/nuget/v3/index.json" />
  </packageSources>
</configuration>
```

When resolving, NuGet consults *all* configured feeds in
priority order. A package might be served by `corporate`
(internal fork) and `nuget.org` (upstream); the resolver picks
based on feed precedence and `<packageSourceMapping>` rules
that map package prefixes to specific feeds.

This shapes the resolver in three ways the single-registry
ecosystems don't have to consider:

- **Parallel multi-feed metadata fetches.** When resolving
  package `X`, the client queries the version-listing
  endpoint on every configured feed simultaneously. The
  resolver waits for all responses before making a decision
  (or short-circuits if a higher-priority feed returns
  the requested version).
- **Feed precedence and conflict resolution.** If two feeds
  serve the same version of the same package with different
  contents, NuGet picks the higher-priority feed. Hash
  verification catches mismatches; package signing
  (`packageSourceMapping`) prevents typosquatting attacks
  against private package names.
- **Per-feed authentication.** Different feeds may require
  different credentials. The resolver must thread auth
  context through every metadata fetch and download.

For Sylk: **the `Resolver` interface should accept a list of
feeds, not a single registry URL.** The substrate's own
recipe-store is one feed; a user's corporate npm mirror is
another; the public npm registry is a third. The same
adapter implementation handles all of them by walking the
feed list in precedence order.

### 4. Framework targeting — multi-axis constraint resolution

NuGet packages can declare multiple target frameworks
(`net6.0`, `net7.0`, `net8.0`, `netstandard2.0`,
`netframework472`, etc.) inside a single package. The package's
`.nuspec` lists which framework versions are supported and
which dependencies apply per framework.

The resolver picks the most-specific compatible framework for
the consuming project. A `net8.0` project consuming a package
that supports `net6.0`, `net7.0`, and `net8.0` gets the
`net8.0` assets. A `net6.0` project gets the `net6.0` assets.
A package supporting only `netstandard2.0` is compatible with
*all* modern .NET versions via fallback rules.

This is a **multi-axis constraint**: version satisfies semver
ranges, framework satisfies compatibility rules. The two axes
are independent — a package may have version `1.5.0` available
for `net6.0` but not `net8.0`. The resolver must consider
both.

The only other case study with anything similar is Python's
wheel platform tags (`cp310-cp310-manylinux2014_x86_64`).
PyPI handles these via separate uploaded wheels per platform;
NuGet handles them via per-framework asset groups inside one
package. NuGet's approach is more compact (one package per
version) but adds a constraint dimension to the resolver.

For Sylk: **the substrate's `Constraint` type needs to model
target-platform compatibility**, not just version ranges.
Anything resolving native binaries or framework-targeted code
will need this. Python wheels need it (`platform_tag`); .NET
needs it (`framework_tag`); cross-platform Sylk recipes
will need it (`os/arch/abi` triples).

### 5. The .NET 9 resolver rewrite — flat representation + eager conflicts

[The November 2024 rewrite](https://devblogs.microsoft.com/dotnet/dotnet-9-nuget-resolver/)
is the case study's recent surprise. The old resolver built
"a massive dependency graph with millions of nodes,
representing every possible relationship between
dependencies" and then ran "repetitive passes" to resolve
conflicts. For Microsoft's largest internal repository,
restore times exceeded 30 minutes; incremental fixes brought
it to 16 minutes; that was still unacceptable.

The new resolver makes two structural changes:

- **Flat set instead of graph.** Every unique
  `(package, version, framework)` tuple is a single node,
  reached by deduplication during construction. The
  documented benchmark: 1.6M nodes → 1,200 nodes for the
  same project. Memory footprint and cache locality both
  improve dramatically.
- **Eager conflict resolution.** Conflicts are detected and
  resolved during graph construction, not in post-hoc
  reconciliation passes. When package A needs `B >= 2.0` and
  package C needs `B < 1.5`, the conflict surfaces as soon as
  both constraints arrive, not after building out the entire
  candidate space.

The result: 16 minutes → 2 minutes for the same repository.
**8× speedup with no protocol change, no algorithm change in
the formal sense, no caching change.** Pure representation
and timing improvements.

This is structurally what PubGrub does — flat representation,
unit propagation that resolves conflicts at decision time
rather than after. The .NET 9 rewrite isn't documented as
PubGrub specifically (the post calls it "the new resolver"
without a name), but the design principles align. The
substrate's `FrontierAwareResolver` extension applies here
too: a resolver that resolves conflicts eagerly during
construction has a natural decision-frontier event stream
that prefetching can hook into.

The lesson for Sylk: **representation and conflict timing
can deliver order-of-magnitude wins independently of
algorithm choice.** Even a "PubGrub" implementation that
defers conflict checking until candidate-set materialization
gives up most of PubGrub's advantage. The substrate's
adapter implementations should resolve conflicts as
constraints arrive — eager, not deferred.

### 6. Central Package Management + packages.lock.json — opt-in centralization

Two relatively recent NuGet features that together provide
deterministic, auditable builds:

[**Central Package Management (CPM)**](https://learn.microsoft.com/en-us/nuget/consume-packages/central-package-management),
introduced in 2022, lets a multi-project solution declare
package versions in a single `Directory.Packages.props` file
at the repository root:

```xml
<Project>
  <ItemGroup>
    <PackageVersion Include="Newtonsoft.Json" Version="13.0.3" />
    <PackageVersion Include="Microsoft.Extensions.Logging" Version="8.0.0" />
  </ItemGroup>
</Project>
```

Individual `.csproj` files reference packages without
versions (`<PackageReference Include="Newtonsoft.Json" />`).
The version comes from the root file. **Transitive pinning**
(opt-in via `CentralPackageTransitivePinningEnabled`) lets
you patch a vulnerable transitive dependency by adding it to
`Directory.Packages.props` without making it a direct
dependency in any `.csproj`.

**`packages.lock.json`** is NuGet's lockfile format,
introduced in 2018 and opt-in via
`<RestorePackagesWithLockFile>true</RestorePackagesWithLockFile>`.
It contains the full transitive dependency graph plus content
hashes for every package. When present, `dotnet restore` is
deterministic — re-resolution is a verify-and-go operation.

The combination — CPM for centralization, lockfile for
reproducibility — is unique among the case studies in being
**explicitly opt-in**. cargo's `Cargo.lock` is mandatory for
binaries; npm's `package-lock.json` is on by default since
npm 5; Bundler's `Gemfile.lock` is committed for
applications. NuGet treats both features as reproducibility
tools you adopt deliberately when you need them.

This is an interesting design choice. The trade-off:
defaults-on lockfiles produce deterministic builds at the
cost of merge conflicts and stale-lockfile surprises;
opt-in lockfiles let casual users skip the friction. For
substrate design, Sylk should **default to lockfiles for
reproducibility** but make the opt-out path well-documented
for true library recipes that benefit from
floating-dependency semantics.

### Speedup attribution

Two attributions to consider — long-term (NuGet today vs
NuGet at v3 launch) and recent (.NET 9 vs .NET 8):

**Long-term (modern NuGet vs early v3):**

| Source | Approximate share of speedup |
|---|---|
| Service index + flat container protocol | 35% |
| New resolver (flat graph + eager conflicts, .NET 9) | 25% |
| Native code performance (.NET runtime + GC improvements) | 15% |
| Central Package Management + lockfile (when used) | 15% |
| HTTP/2, parallel feed queries | 10% |

**.NET 9 resolver rewrite specifically:**

| Source | Approximate share of speedup |
|---|---|
| Flat representation (1.6M → 1.2K nodes) | 50% |
| Eager conflict resolution (no post-hoc passes) | 35% |
| Improved framework-graph dedup | 10% |
| Memory locality / cache hit rate | 5% |

The .NET 9 numbers are particularly instructive. The new
resolver isn't faster because of a better algorithm in any
formal sense — it's faster because it makes better
representation choices. Same problem, same constraints,
different data structure → 8× speedup.

### Lessons for the Sylk NuGet adapter

NuGet is one of the easier ecosystems to adapt because the
protocol is well-designed and the implementation choices are
well-documented:

- **Speak v3 protocol natively.** The service index +
  PackageBaseAddress + Registrations resources cover 95% of
  what an adapter needs. Skip v2 (legacy ASP.NET-based
  protocol) entirely.
- **Multi-feed first.** The `Resolver` interface for the
  NuGet adapter must accept a list of feed URLs, not a
  single registry. Honor `<packageSourceMapping>` rules from
  `NuGet.config`. Query feeds in parallel with priority-
  based result selection.
- **Implement framework targeting.** Adapter must accept the
  consuming project's target framework as a constraint
  dimension and pick the most-specific compatible asset
  group from each package.
- **Use a flat-representation resolver.** Don't build a node
  per dependency-edge; build a node per
  `(package, version, framework)` and dedup during
  construction. Resolve conflicts eagerly. This is what
  .NET 9 does and what the substrate's PubGrub-class
  resolvers do.
- **Implement `FrontierAwareResolver`.** Eager-conflict
  resolvers have a natural decision-frontier event stream;
  expose it for substrate-level prefetching.
- **Honor `packages.lock.json`.** Treat as `LockfileHints`
  hard preference. Generate it as default output when the
  project has `<RestorePackagesWithLockFile>true</RestorePackagesWithLockFile>`.
- **Support Central Package Management.** Read
  `Directory.Packages.props` if present; treat
  centrally-declared versions as the version source for
  every project in the solution.
- **Verify package signatures and hashes.** NuGet supports
  package signing; the substrate adapter must verify
  signatures when feeds require them and always verify
  hashes from `packages.lock.json`.

### Lessons for the Sylk substrate protocol

Three structural lessons specifically from NuGet's design:

- **Service-index pattern for capability discovery.** The
  substrate's recipe-store API should start with a single
  `/index.json` declaring which resources the feed
  implements. This is the single most forward-compatible
  design decision available — adding new substrate
  capabilities (vulnerability scanning, signature stores,
  symbol packages) doesn't require breaking older
  adapters. Cargo's sparse index would have benefited from
  this; Go's GOPROXY would have benefited from this.
- **Multi-feed federation as a first-class concern.** The
  substrate must support multiple feeds per ecosystem.
  Sylk's first-party recipe-store is one feed; users will
  add corporate mirrors, internal CI artifact servers,
  air-gapped local proxies. Don't bake "the registry" into
  the resolver interface.
- **Default to lockfiles, allow opt-out.** NuGet's
  opt-in default leaves casual users without
  reproducibility; cargo's mandatory-for-binaries
  approach has fewer footguns. Sylk should default to
  lockfile generation but document the opt-out for true
  library recipes.

---

## Case study: Hex (Elixir + Erlang / hex.pm)

Hex is the **small ecosystem with deliberate design** case
study. Hex.pm hosts ~14K packages — three orders of magnitude
smaller than npm — which means resolution performance was never
in the same crisis territory as npm or pre-2015 RubyGems. The
team used that breathing room to make protocol decisions more
carefully than the larger ecosystems did. Two distinctive
choices emerged: **signed protobuf** as the metadata transport,
and **single-registry cross-language support** for both Elixir
and Erlang.

The 2023 [Hex 2.0 release shipped hex_solver](https://hex.pm/blog/hex-v20-released-with-new-version-solver),
a [PubGrub-based version solver](https://github.com/hexpm/hex_solver),
replacing the original 2014 hand-rolled algorithm. The timing
mirrors Bundler's January 2023 PubGrub adoption — both
ecosystems independently arrived at the same conclusion in the
same year, after watching uv (Python) and the in-progress
cargo migration. PubGrub is now the de facto cross-ecosystem
default for backtracking resolvers.

For Sylk, Hex is a useful case study less for resolver
performance — the ecosystem's small scale means even naive
implementations are fast enough — and more for two specific
protocol design decisions Sylk should consider adopting.

### 1. Signed protobuf as the metadata transport

The [Hex registry protocol](https://github.com/hexpm/specifications/blob/main/endpoints.md)
serves all metadata as **signed Protocol Buffers**, not JSON
or text. Every response from the registry carries:

- The metadata payload itself (protobuf-encoded)
- A signature over the payload, signed by the registry's
  private key

Clients verify the signature before trusting the data. Tampering
by intermediaries — proxies, CDNs, on-path attackers — is
detected at the edge. This is the same threat model Go's
`go.sum` + `sum.golang.org` transparent log addresses, but
solved differently: Hex signs at the protocol layer; Go
verifies content hashes after the fact.

Two trade-offs to call out:

- **Protobuf vs JSON.** Protobuf is more compact (often 2–3×
  smaller for the same data) and has a well-defined schema
  that prevents the "is this a string or a list?" parse
  ambiguity JSON-based ecosystems suffer. Trade-off: protobuf
  is binary and not human-readable. Debugging requires the
  schema. For a small ecosystem with a controlled tooling
  surface (Hex serves Mix and Rebar3, no third parties), this
  is the right choice. For npm-scale ecosystems with
  thousands of third-party tools, JSON's universality wins
  even at the cost of size.
- **Signing at the protocol layer vs hashing in the lockfile.**
  Hex's approach catches tampering immediately, before the
  data is processed. Go's approach catches tampering at use
  time via the lockfile hash. Both work; Hex's is more
  defensive (errors surface earlier) but assumes the registry
  itself is uncompromised.

The Sylk lesson: **signed metadata at the protocol layer is
worth considering for the substrate's first-party recipe
store.** It costs implementation complexity (key management,
signature verification on every read) but provides
defense-in-depth that hash-only lockfiles can't match. The
substrate's threat model should drive the decision — air-
gapped enterprise deployments benefit more than open-source
single-machine use.

### 2. Per-package endpoint + aggregate sync endpoints

The Hex registry exposes both granular and bulk metadata:

```
GET /packages/{PACKAGE}            # per-package: releases, deps, checksums
GET /tarballs/{PACKAGE}-{VER}.tar  # the package itself
GET /names                         # all package names (full sync)
GET /versions                      # all versions of all packages (full sync)
GET /docs/{PACKAGE}-{VER}.tar.gz   # optional documentation archive
```

The `/packages/{PACKAGE}` endpoint is the cargo-sparse-index
equivalent — small, per-package, cacheable. Resolvers fetch
only the packages they need.

The `/names` and `/versions` endpoints serve a different
purpose: **full registry mirror sync**. A corporate mirror
(e.g. an air-gapped Hex proxy) fetches `/versions` once to
learn what exists, then incrementally fetches per-package
metadata as needed. The aggregate endpoints aren't used by
resolvers; they're used by *infrastructure* that wants to
mirror or audit the entire registry.

This is a useful design split. cargo, Go, and Bundler all
serve per-package metadata but don't have a clean "give me
everything" endpoint — mirroring tools have to scrape or
crawl. NuGet's service index gestures in this direction
without fully solving it. Hex's `/names` + `/versions` is
the cleanest minimal answer: one cheap endpoint per concern
(name discovery, version discovery), used by infrastructure
not by resolvers.

The Sylk lesson: **the substrate's recipe-store should serve
both per-recipe metadata AND aggregate sync endpoints.**
Resolvers use the per-recipe endpoints (small, frequent,
fast). Mirror infrastructure uses the aggregate endpoints
(large, infrequent, designed for incremental updates). Don't
conflate the two — clients of each are completely different.

### 3. Hex 2.0 + hex_solver — the 2023 PubGrub migration

[hex_solver](https://github.com/hexpm/hex_solver) replaced the
hand-rolled 2014 resolver wholesale in [Hex 2.0](https://hex.pm/blog/hex-v20-released-with-new-version-solver).
The motivations match Bundler 2.4's almost exactly:

- **Backtrack performance.** The original solver had no
  conflict learning — it would re-explore the same failed
  combinations on each backtrack. Pathological resolves were
  slow. PubGrub's derivation rules cut these dramatically.
- **Diagnostic quality.** Conflict explanations matter to
  developers more than the underlying algorithm does. PubGrub
  produces step-by-step reasoning ("package A requires B >= 2.0
  because... but package C requires B < 1.5 because..."); the
  old solver produced cryptic "no solution found" messages.

This is the third major ecosystem to migrate to PubGrub in the
2023–2024 window (Bundler 2.4, Hex 2.0, in-progress cargo).
PubGrub has won the backtracking-resolver category. New
resolvers should default to it; existing Molinillo-style
hand-rolled solvers should plan migrations.

Hex's small ecosystem made the migration smoother than
Bundler's. There were fewer pathological dependency trees in
the wild that exposed edge cases between the old and new
solvers. The lesson for Sylk: **algorithm migrations are
easier in small ecosystems** — adapter implementations can
move faster than user-facing tools can.

### 4. Cross-language ecosystem — one registry, two compilers

Hex serves **both Elixir and Erlang** packages from a single
registry. Mix (Elixir's build tool) and Rebar3 (Erlang's
build tool) both consume hex.pm. A package can be written in
either language and is consumable by either consumer (with
the natural caveat that Erlang code can be called from
Elixir trivially while the reverse requires more work).

This is unique among the case studies. Cargo serves only
Rust crates. PyPI serves only Python (with C extensions
treated as opaque binaries). RubyGems serves only Ruby. The
Hex protocol is **language-agnostic at the registry layer** —
it serves opaque package archives plus metadata; the
build tool decides what to do with them.

The substrate-design lesson is direct: **Sylk's recipe-store
protocol should treat recipes as opaque archives plus typed
metadata, not as language-specific things.** Different
ecosystem adapters consume the same recipes for different
purposes. A Python recipe and a Node recipe are both just
recipes — the metadata declares which adapter knows how to
materialize them. This generalizes Hex's two-language model
to Sylk's many-ecosystem model.

### Speedup attribution

Hex doesn't have the long performance-optimization history
of cargo or Bundler — the ecosystem stayed small enough that
the original 2014 solver was acceptable for a decade. The
2023 hex_solver migration is the only major resolver
change. Approximate breakdown of why modern Hex is fast:

| Source | Approximate share of speedup vs hypothetical naive |
|---|---|
| Per-package metadata endpoint design | 30% |
| Protobuf vs JSON (compactness, parse speed) | 25% |
| hex_solver (PubGrub migration, 2023) | 20% |
| Small ecosystem (less metadata to parse overall) | 15% |
| Erlang VM concurrency for parallel fetches | 10% |

The "small ecosystem" line is real but not transferable —
Sylk can't choose to have fewer packages. The other four
contributors are all design decisions Sylk can adopt.

### Lessons for the Sylk Hex adapter

- **Use the v2 protobuf protocol** — there's no v1 worth
  supporting. Use [hex_core](https://github.com/hexpm/hex_core)
  (Erlang reference implementation) as the schema source of
  truth; reimplement the small subset Sylk needs in Go.
- **Verify signatures.** Don't skip this even if it's
  tempting. The protocol's threat model assumes signed
  responses; un-verified clients are a security regression
  vs Hex's own tooling.
- **Use a Go PubGrub implementation** — same shared one
  targeted at Python and RubyGems. Hex's package
  dependency model fits PubGrub's constraint shape cleanly.
- **Honor `mix.lock`** as `LockfileHints` — same pattern as
  every other adapter. Output `mix.lock`-compatible
  lockfiles for external Mix tooling to consume.
- **Implement `FrontierAwareResolver`** — PubGrub-based, the
  frontier is natural; expose it for substrate-level
  prefetching.

### Lessons for the Sylk substrate protocol

Two specific design lessons to consider for the substrate's
own metadata API:

- **Signed-metadata-at-the-protocol-layer is worth the
  cost** for trust-sensitive deployments. The substrate
  should support it as an optional feed property — feeds
  that opt in serve signed responses, clients verify; feeds
  that don't are still usable but provide hash-based
  integrity only via the lockfile. Default depends on Sylk's
  deployment context.
- **Aggregate sync endpoints distinct from per-recipe
  endpoints.** `/recipes/{recipe}` for resolvers;
  `/names` + `/versions` for mirror infrastructure. Don't
  let one client class drive the design that breaks the
  other's use case. Hex got this right by separating them
  cleanly.

---

## Case study: Maven (JVM / Maven Central + corporate Nexus/Artifactory)

Maven is the **shadow case study** of this doc — the example of
what every other modern resolver consciously chose *not* to do.
Where uv, cargo, Go, Bundler, and Hex all ultimately settled on
some form of constraint satisfaction (PubGrub, MVS, derivation-
based backtracking), Maven uses **"nearest wins" mediation** — a
deterministic graph traversal that picks a version based on
*depth in the dependency tree* and *declaration order* in the
POM. Conflicts don't fail resolution; they pick a winner
silently, with rules the user can't easily reason about.

The mediation algorithm has been one of the most-criticized
parts of Maven for a decade ([MNG-7852](https://issues.apache.org/jira/browse/MNG-7852)
is a 2024 thread arguing the rules should be removed entirely).
But Maven also gets two things very right that earn it a place
in this doc: a **predictable repository URL layout** that any
HTTP server can implement, and a **multi-feed model that just
works** — every JVM artifact server (Nexus, Artifactory, GitHub
Packages, AWS CodeArtifact, Google Artifact Registry, JitPack)
speaks Maven protocol because the protocol is small enough to
implement correctly.

For Sylk, Maven is a useful contrast on two axes: what to copy
(repository layout, multi-feed support) and what to avoid
(depth/order-based mediation as a substitute for real constraint
solving). The Maven adapter is also non-optional for any Sylk
deployment that touches the JVM ecosystem — Maven Central is
the de facto registry for ~10M JARs.

### 1. The repository layout — predictable URLs from groupId × artifactId × version

A [Maven repository](https://maven.apache.org/repository/layout.html)
is, structurally, a static HTTP file tree. The URL for any
artifact is determined entirely by its coordinates:

```
{repository-root}/{groupId-with-slashes}/{artifactId}/{version}/{artifactId}-{version}.{ext}

# Example:
https://repo.maven.apache.org/maven2/org/apache/commons/commons-lang3/3.14.0/commons-lang3-3.14.0.jar
https://repo.maven.apache.org/maven2/org/apache/commons/commons-lang3/3.14.0/commons-lang3-3.14.0.pom
```

Each version directory contains the artifact (`.jar`, `.war`,
`.aar`, etc.), the POM (`.pom`), checksums (`.sha1`, `.md5`,
`.sha256`, `.sha512`), and optional signatures (`.asc`).
Per-artifact metadata (`maven-metadata.xml`) lists available
versions for the artifact.

This layout has three properties that make it dominate the JVM
artifact-server market:

- **Any HTTP server can host it.** A Maven repository is a
  static file tree — no API server required, no protocol
  negotiation, no service index. Drop a directory tree on S3,
  Azure Blob, or Apache HTTPD; you have a Maven repository.
  Internal artifact servers (Nexus, Artifactory) are
  glorified caching proxies over this layout.
- **Predictable from coordinates.** The URL for
  `commons-lang3:3.14.0` is mechanically derivable from the
  coordinates. No metadata index lookup needed to find the
  artifact URL — clients construct it directly. Mirroring is
  trivial: `rsync` the path and you have a working mirror.
- **Browsable in any HTTP client.** A user with `curl` can
  inspect any Maven repository. Debugging is direct — no
  binary protocols, no protobuf decoders, no JSON parsing.

The Sylk lesson: **the substrate's recipe-store should expose
this layout option.** A Sylk feed served as a static file tree
under `{recipe-namespace}/{recipe}/{version}/...` is trivially
mirror-able to any HTTP server, browsable for debugging,
implementable in 50 lines of code by anyone who needs a private
Sylk feed. Service-index-style capability discovery (à la
NuGet) is a *complement* to this, not a replacement — the
substrate should support both modes.

### 2. POM as metadata — XML, inheritance, BOM imports

Each artifact ships with a Project Object Model (`.pom`) — an
XML document declaring metadata, dependencies, build configuration,
and inheritance relationships. Two features matter for the
resolver:

- **Parent POMs.** A POM can declare a `<parent>` element
  pointing at another POM whose properties and configuration
  are inherited. Dependency versions can be declared in a
  parent POM and inherited across many child artifacts. This
  is structurally similar to NuGet's Central Package
  Management but predates it by a decade.
- **BOM (Bill of Materials) imports.** A POM can `<import>`
  another POM's `<dependencyManagement>` section, pulling in
  a curated set of compatible version pins. The Spring Boot
  BOM, for example, declares versions of every Spring-related
  dependency known to work together. Consumers depend on the
  BOM and inherit the version coordination automatically.

BOM imports are a **socially-coordinated alternative to
resolver-driven version coordination**. Rather than asking the
resolver to find a satisfying version assignment, the
ecosystem publishes curated BOMs that pre-coordinate them. The
resolver just consumes the BOM's pinned versions.

This is a useful design pattern Sylk should support: substrate
recipes should be able to declare BOM-style "tested-together"
version groups that downstream recipes can import wholesale.
Avoids the resolver having to discover compatibility from
scratch on every resolve.

### 3. "Nearest wins" mediation — the design choice everyone else avoided

Maven's [conflict resolution](https://maven.apache.org/guides/introduction/introduction-to-dependency-mechanism.html)
is *not* constraint satisfaction. When two transitive
dependencies declare different versions of the same artifact,
Maven applies two rules in order:

1. **Nearest wins.** The version closest to the project root
   in the dependency tree is chosen. If `myapp` directly
   declares `lib-x:2.0.0` and a transitive dependency
   declares `lib-x:1.0.0`, the direct declaration (depth 1)
   beats the transitive one (depth 2+).
2. **Declared first wins (tiebreaker).** If two declarations
   are at the same depth, the one declared earlier in the POM
   wins.

What this is *not*:

- It's not range satisfaction. A transitive dependency
  requiring `lib-x >= 3.0.0` is silently overridden if the
  root POM pins `lib-x:1.0.0`. The build proceeds; the
  transitive dependency may fail at runtime with a
  `NoSuchMethodError`.
- It's not deterministic across POM edits. Adding an
  unrelated dependency may shift the depth of an existing
  transitive, changing which version of a third dependency
  wins. This is the "reorder a dependency to fix the
  resolver" anti-pattern that Maven users have suffered
  for years.
- It's not auditable. The user can't easily explain *why* a
  particular version was chosen without running `mvn
  dependency:tree` and visually tracing depths.

The pathology surfaces in the [MNG-7852 thread](https://issues.apache.org/jira/browse/MNG-7852):
the suggestion is that Maven should use *all* version
declarations for resolution (i.e. constraint satisfaction)
rather than "random variables like dependency depth or
dependency order." The thread is from 2024. The mediation
algorithm has been the same since 2003.

The Sylk lesson is direct: **never use depth-based or
order-based mediation as a substitute for constraint
satisfaction.** Modern resolvers use real constraint solvers
(PubGrub, MVS, hand-rolled with conflict learning) for a
reason — the alternative produces brittle, surprising,
hard-to-debug builds. Sylk's adapters must use real solvers
even when wrapping ecosystems whose native tools don't.

But also: **the Maven adapter has a compatibility constraint
the others don't.** A Sylk Maven adapter that uses PubGrub
will sometimes produce *different* version selections than
`mvn` would. For some users that's an improvement; for
others it's a regression that breaks their build. The
adapter should default to Maven-compatible nearest-wins
mediation and offer a `--strict` opt-in mode that uses
PubGrub-style constraint satisfaction.

### 4. Scopes and classifiers — dimensions without multi-axis constraints

Maven dependencies have two extra metadata dimensions beyond
`(groupId, artifactId, version)`:

- **Scope** — `compile` (default), `runtime`, `test`,
  `provided`, `system`, `import`. Determines which classpaths
  the dependency appears on. `compile` deps go everywhere;
  `test` deps only into the test classpath; `provided` deps
  are expected to be supplied by the runtime container (e.g.
  servlet APIs).
- **Classifier** — an optional string distinguishing variants
  of the same artifact. `commons-lang3-3.14.0-sources.jar`
  carries the source jar; `commons-lang3-3.14.0-javadoc.jar`
  the API docs; some libraries publish JDK-specific variants
  like `lib-1.0.0-jdk8.jar`.

These are *dimensions* but not *constraints* in the resolver
sense. A `test` dependency doesn't conflict with a `compile`
dependency on the same artifact at a different version —
they live on different classpaths. Classifiers similarly
multiplex variants without forcing the resolver to choose
one.

Compare to NuGet's framework targeting, which is a real
multi-axis constraint (the resolver must pick one
framework variant per package). Maven's scopes and
classifiers are simpler: they expand the artifact identity
beyond `(groupId, artifactId, version)` to
`(groupId, artifactId, version, classifier, scope)`, but
each tuple is independently resolvable.

The substrate `Constraint` type already accommodates this if
the recipe identity carries enough fields. The Sylk lesson
is naming-discipline more than algorithm-design: **recipe
identifiers must include all dimensions that affect
resolution**, not just version.

### 5. Maven 4's breadth-first collector — a 2024 protocol-neutral fix

Maven 3 used a depth-first dependency collector — the
algorithm that walks the transitive tree to discover all
required artifacts. Depth-first traversal interacts badly
with nearest-wins mediation: a single deep branch can pin
versions that a shallower branch would have overridden,
just because the deep branch was visited first.

Maven 4 (2024) switches to a [breadth-first collector](https://innovation.ebayinc.com/stories/open-source-contribution-new-maven-dependency-resolution-algorithm/)
contributed by eBay's open-source team. Breadth-first
guarantees that nearer-depth declarations are always
considered before deeper ones, making the mediation
algorithm match its documented semantics in cases where
DFS got it wrong.

This is a small algorithmic change with measurable
correctness improvements. It doesn't fix the underlying
"nearest wins is a bad design" problem — it just makes
nearest wins work the way it always claimed to. The
Sylk lesson here is narrow: **traversal order matters for
non-constraint-satisfaction algorithms.** Sylk's adapters
that wrap such algorithms (the Maven adapter being the
likely sole example) should explicitly specify traversal
order rather than letting it depend on data structure
implementation details.

### 6. The lockfile gap — Maven's longest-standing weakness

Maven had **no lockfile mechanism** for ~20 years. The
official position was that POMs should declare versions
explicitly enough that no lockfile was needed; in practice,
transitive version drift broke builds frequently.

The ecosystem worked around this with conventions:

- **Pinned versions everywhere.** Aggressive use of
  `<dependencyManagement>` to pin transitive versions
  explicitly.
- **`mvn versions:lock-snapshots`** plugin to convert
  `1.0-SNAPSHOT` to `1.0-20240101.123456-1` (timestamped
  snapshot version).
- **Build-time caches** that captured the resolved tree as
  a side effect of the first successful build.
- **Gradle, where lockfiles exist.** Gradle (which uses the
  same Maven repository protocol) shipped `gradle.lockfile`
  in 6.0 (2019). Many JVM teams use Gradle specifically for
  the lockfile support.

Recent Maven plugins ([Maven Lockfile](https://github.com/chains-project/maven-lockfile),
maintained externally) add lockfile capability, but it's
still not in the Maven core distribution. Maven 4 has
discussed first-class lockfile support but hasn't shipped
it as of the 4.0.0-rc releases.

The Sylk lesson is the second appearance of the same point
made in the RubyGems study: **ship lockfiles from day one.**
Retrofitting them onto a 20-year-old ecosystem is harder than
shipping them initially. Cargo got this right; npm got it
right (eventually); Maven still hasn't. Sylk's substrate
should default to lockfiles and treat the absence of one as a
warning condition.

### Speedup attribution

Maven is the slowest resolver in this doc. There are no
order-of-magnitude speedups to attribute — Maven's
performance has been "acceptable, not great" for two decades.
The breakdown of why Maven is *not* much slower than it
appears, given its choices:

| Source | Approximate impact vs naive baseline |
|---|---|
| Repository layout (predictable URLs, no API lookup) | 35% |
| Local repository cache (`~/.m2/repository`) | 25% |
| Nearest-wins is computationally cheap (no SAT) | 15% |
| Parallel artifact downloads (Maven 3.x+) | 15% |
| BOM imports avoiding resolver work | 10% |

The cache contribution is significant because Maven's
`~/.m2/repository` mirrors the registry layout exactly. Once
an artifact has been downloaded, subsequent resolves use the
local file directly with no protocol overhead. This is the
same content-addressed-cache pattern uv and cargo use, but
predates them by a decade.

The "nearest-wins is computationally cheap" line is a
backhanded compliment: Maven's algorithm is fast precisely
because it doesn't try to solve the constraint satisfaction
problem. The trade-off is correctness, not speed.

### Lessons for the Sylk Maven adapter

The Maven adapter has two design modes the substrate must
support:

**Compatibility mode (default).** Behave like `mvn`:
- Use nearest-wins mediation with breadth-first traversal
  (matching Maven 4 default behavior)
- Honor `<dependencyManagement>`, parent POMs, and BOM
  imports per Maven semantics
- Produce the same version selections `mvn` would
- This mode is required for users whose JARs were built
  against Maven's resolution semantics

**Strict mode (opt-in).** Use real constraint satisfaction:
- Run a PubGrub-style solver over the version range
  declarations encoded in POMs
- Fail loudly on unsatisfiable constraints rather than
  silently picking a winner
- Produce more predictable resolution at the cost of
  potentially differing from `mvn`'s output
- Document clearly that switching modes can change the
  resolved tree

Other adapter requirements:

- **Speak the standard Maven repository protocol** —
  static file tree under `{repo}/{groupId}/{artifactId}/{version}/`.
  No bespoke client.
- **Multi-feed first.** `<repositories>` in POM and
  `settings.xml` declare ordered feed lists; the adapter
  must consult them in order. Same multi-feed lessons as
  the NuGet adapter.
- **Verify checksums (`.sha1` minimum, prefer `.sha256`).**
  Maven Central provides multiple hash algorithms; use the
  strongest available.
- **Honor PGP signatures (`.asc`)** when feeds require them.
  Maven Central signs all artifacts; corporate feeds may
  enforce signature verification.
- **Cache in a Maven-compatible layout.** Sylk should write
  its cache as `{cache-root}/{groupId}/{artifactId}/{version}/...`
  so users can point their existing `mvn` at the cache for
  out-of-band debugging. Same content-addressed-cache
  benefits as cargo's `~/.cargo/registry`.
- **Generate a lockfile** even though Maven core doesn't
  require one. Use the substrate's standard lockfile format;
  optionally also emit a `maven-lockfile`-compatible JSON
  for users who want external `mvn` to verify the same tree.
- **Implement `FrontierAwareResolver`** in strict mode
  (PubGrub has a natural frontier); skip in compatibility
  mode (nearest-wins doesn't have one).

### Lessons for the Sylk substrate protocol

Three structural lessons from Maven, mostly negative:

- **Static-file-tree layout for the substrate's recipe-store
  is worth supporting** as one option. Maven proves that
  predictable URLs from coordinates dominate for mirroring,
  debugging, and trivial implementation. Sylk should support
  this layout in addition to NuGet-style service-index
  feeds.
- **Never use traversal-order or depth-based mediation.**
  Maven's experience demonstrates conclusively that this is
  a footgun. The substrate's `Resolver` interface should not
  even allow expressing "pick based on tree depth" as a
  resolution strategy — make the wrong thing impossible.
- **Lockfiles are not optional.** Two decades of Maven users
  shipping unreproducible builds is the conclusive case
  study. The substrate must default to lockfile generation
  and require explicit opt-out for true library recipes.

---

## Case study: Gradle (Java / Kotlin / Android — Maven Central + Gradle plugins)

Gradle is the JVM ecosystem's answer to "what would Maven look
like if you redesigned it knowing what we know now?" Same
underlying repository protocol (Maven Central / Nexus /
Artifactory). Same JAR artifact format. Same `groupId:artifactId`
identity scheme. But everything above the bytes is different:
the metadata format extends Maven's POM with a richer variant
graph, the resolver is variant-aware rather than nearest-wins,
conflicts are explicit failures rather than silent winners,
lockfiles are first-class (Gradle 6.0+), and the build cache
contributes to "install" performance in a way Maven doesn't try
to compete on.

For Sylk this matters because **the JVM ecosystem is not a single
ecosystem**. Maven projects expect Maven semantics; Gradle
projects expect Gradle semantics. Android — the largest single
JVM consumer ecosystem — uses Gradle exclusively. Kotlin
Multiplatform projects depend critically on variant-aware
resolution that Maven can't express. The substrate cannot ship
"a JVM adapter" and expect it to serve both audiences; it must
ship Maven and Gradle adapters that share the underlying HTTP
transport and content-addressed cache but diverge at the
resolution and metadata layers.

### 1. Gradle Module Metadata — extending POM with variants

[Gradle Module Metadata](https://docs.gradle.org/current/userguide/publishing_gradle_module_metadata.html)
(`.module` files) is Gradle's own metadata format, published
alongside the standard `.pom` file:

```
commons-lang3-3.14.0.jar
commons-lang3-3.14.0.pom        # for Maven consumers
commons-lang3-3.14.0.module     # for Gradle consumers
```

The `.module` file is JSON and carries **variant information**
the POM can't express:

```json
{
  "formatVersion": "1.1",
  "component": { "group": "org.apache.commons", "module": "commons-lang3", "version": "3.14.0" },
  "variants": [
    {
      "name": "apiElementsJvm",
      "attributes": {
        "org.gradle.category": "library",
        "org.gradle.usage": "java-api",
        "org.gradle.jvm.version": 8,
        "org.gradle.libraryelements": "jar"
      },
      "dependencies": [...],
      "files": [{ "name": "commons-lang3-3.14.0.jar", "...": "..." }]
    },
    { "name": "runtimeElementsJvm", "attributes": { ... }, ... },
    { "name": "sourcesElements", "attributes": { ... }, ... },
    { "name": "javadocElements", "attributes": { ... }, ... }
  ]
}
```

Each variant declares a set of **attributes** describing when
it should be selected (`jvm.version=8`, `usage=java-api`, etc.),
its dependencies, and its files. Different variants can declare
*different* dependencies — a JVM 8 variant might depend on a
backport library that's not needed for JVM 11+.

For libraries with platform-specific code (Kotlin Multiplatform
projects in particular), this is load-bearing. A Kotlin library
publishes JVM, JS, iOS-arm64, iOS-x64, Android, and macOS
variants from the same coordinates. The Gradle resolver picks
the correct variant per target — no separate `groupId` per
platform like Maven would require.

The Sylk lesson: **the substrate's metadata format should
support variant-aware identity from day one**. Variants are
not a Java-specific concern — Python wheels' platform tags are
the same problem under a different name, NuGet's framework
targeting is the same problem with a single attribute. Gradle's
attribute-based formulation is the most general; the substrate
should adopt the same pattern.

### 2. Variant-aware resolution — attribute matching

When a Gradle consumer requests `commons-lang3:3.14.0`, the
resolver picks a variant by [matching attributes](https://docs.gradle.org/current/userguide/variant_aware_resolution.html):

- The consumer declares its own attributes
  (`org.gradle.jvm.version=11`, `org.gradle.usage=java-runtime`)
- The producer declares each variant's attributes
- Gradle picks the variant whose attributes most specifically
  match the consumer's

Attribute matching has [explicit disambiguation rules](https://docs.gradle.org/current/userguide/variant_attributes.html)
when multiple variants match equally — exact match beats
compatible match, more-specific beats less-specific. Custom
attributes can be declared with custom matching rules, allowing
ecosystems built on Gradle (Android, Kotlin Multiplatform) to
extend the model without modifying Gradle itself.

This is structurally **multi-axis constraint resolution**, more
general than NuGet's framework targeting (which has one axis)
and more general than Python wheels' platform tags (which have
~5 fixed axes). Gradle's attributes are an arbitrary-dimensional
constraint space; the resolver picks the variant that's a
maximal compatible point in that space.

### 3. Capability conflict detection — what nearest-wins misses

A [capability](https://docs.gradle.org/current/userguide/dependency_capability_conflict.html)
in Gradle is a logical role a component fills — "the SLF4J API,"
"the JSON parser." Each component declares which capabilities it
provides. Gradle's resolver detects when two different components
in the same dependency graph claim the same capability and
**fails resolution** unless the user explicitly resolves the
conflict.

Concrete example: an old library depends on `commons-logging`;
a newer library depends on `jcl-over-slf4j` (the SLF4J shim that
implements the commons-logging API on top of SLF4J). Both
provide the "commons-logging implementation" capability. If both
are on the classpath, applications get unpredictable logging
behavior depending on classloader order.

Maven's nearest-wins picks one silently; the user discovers
the bug at runtime. Gradle's capability detection surfaces the
conflict at resolve time with an explicit error message and
guidance on how to resolve it (force one variant, exclude the
other, or use a `dependency-substitution` rule).

This catches a **real class of bug** that Maven users live with.
The Sylk substrate's recipe model should support capability
declarations on recipes — it's a small addition that closes a
real correctness gap.

### 4. Configurable conflict resolution — not one strategy

Gradle exposes [conflict resolution strategies](https://docs.gradle.org/current/userguide/dependency_resolution.html)
as configuration:

- `latest` (default) — newest version wins
- `strict` — declared version pins force resolution; conflicts fail
- `prefer` — declared preferences influence selection but don't force
- `force` — explicit override of any conflicting requirement
- `reject` — exclude specific versions from consideration

These are per-dependency, per-configuration. A project can use
`strict` for security-critical dependencies (lock to known-good
versions) and `latest` for everything else.

Compare to Maven's single hardcoded "nearest wins" rule. Compare
to npm's single "first-encountered wins" rule. Gradle treats
conflict resolution policy as **user-configurable**, not as a
property of the resolver implementation. This is closer to OPAM's
optimization-criteria pattern (different ecosystem, same idea:
expose policy as configuration).

The Sylk substrate's `Resolver` interface should accept a
**conflict resolution strategy** parameter, defaulting to a
sensible policy but allowing override per-dependency for
constraint-sensitive use cases.

### 5. `gradle.lockfile` and dependency constraints

[`gradle.lockfile`](https://docs.gradle.org/current/userguide/dependency_locking.html)
is Gradle's lockfile format, opt-in via
`dependencyLocking { lockAllConfigurations() }`. Lockfiles are
**per-configuration** (separate lockfile for compile classpath,
runtime classpath, test classpath) — important because the same
dependency can resolve to different variants in different
configurations.

Gradle 6.0 (2019) added [dependency constraints](https://docs.gradle.org/current/userguide/dependency_constraints.html):

```kotlin
dependencies {
    constraints {
        // Apply a version constraint to lib-x without making it a direct dep.
        // If lib-x ends up in the resolved graph transitively, it must be 2.0+.
        implementation("group:lib-x:2.0+")
    }
}
```

Constraints are separate from dependencies — they don't *add*
the constraint target to the graph, they only *influence* the
version selected if the target is added by something else. This
is closest to NuGet's transitive pinning, but Gradle shipped it
two years earlier and the model is more general (constraints can
declare any version range, not just exact pins).

For Sylk: the `Constraint` type should distinguish between
"add this dependency" and "if this dependency exists, constrain
its version." These are different semantic operations and
modeling them as the same constraint forces awkward workarounds.

### 6. Composite builds and the build cache

Two Gradle features that aren't strictly resolver concerns but
affect the end-to-end "install" experience:

[**Composite builds**](https://docs.gradle.org/current/userguide/composite_builds.html)
let multiple Gradle builds substitute dependency coordinates for
sibling project source includes:

```kotlin
includeBuild("../my-lib") {
    dependencySubstitution {
        substitute(module("com.example:my-lib")).using(project(":"))
    }
}
```

The consuming project depends on `com.example:my-lib` as a
normal coordinate; the composite build substitutes the coordinate
with a source build of the sibling project. Useful for
monorepos and active library development without manual
`mvn install` steps.

[**Build cache**](https://docs.gradle.org/current/userguide/build_cache.html)
(local + remote) caches **build outputs** (compiled classes,
processed resources) keyed by content hash of inputs. A team
with a shared remote cache effectively shares compiled artifacts
across machines — CI, developer laptops, deploy hosts all reuse
the same outputs. Gradle has poured engineering into this; the
build cache often dominates "install" time for large projects.

The Sylk substrate's recipe-store can support analogous build
caching for materializer outputs (post-build artifacts), keyed
by content hash of the recipe + build inputs. Maven doesn't
compete on this axis; Gradle does. Sylk should plan for it.

### Speedup attribution

Gradle vs Maven on equivalent projects, approximate
breakdown of why Gradle is *not slower than Maven* despite its
significantly more complex resolution model:

| Source | Approximate impact |
|---|---|
| Build cache (local + remote) for compiled outputs | 35% |
| Variant-aware resolution avoids over-fetching wrong variants | 20% |
| Parallel artifact downloads (concurrent fetches) | 15% |
| Module Metadata format efficiency vs POM XML parsing | 10% |
| Dependency constraints reducing graph search space | 10% |
| Configurable conflict resolution avoiding pathological cases | 10% |

Gradle isn't dramatically faster than Maven on pure metadata
fetching — it consumes the same Maven Central protocol. The
performance story is downstream: the build cache turns most
"install" operations into cache lookups, and variant-aware
resolution avoids the "download all framework variants, pick
one" pattern that NuGet has to live with.

### Lessons for the Sylk Gradle adapter

The Gradle adapter is more complex than the Maven adapter, but
the complexity is necessary — Gradle projects expect Gradle
semantics:

- **Speak Maven Central protocol** for the artifact transport
  layer. Gradle doesn't have its own registry; it consumes
  Maven repositories.
- **Parse both `.pom` and `.module` files** for each artifact.
  When `.module` is present, prefer it (richer information);
  when only `.pom` exists, fall back to Maven semantics.
- **Implement attribute-based variant selection.** This is the
  hard part. The substrate's `Constraint` type must support
  arbitrary attribute maps, and the resolver must implement
  Gradle's matching/disambiguation rules. Reuse Gradle's
  attribute schema as the canonical source of truth — don't
  invent a parallel attribute namespace.
- **Detect capability conflicts.** Build a capabilities map
  during resolution; fail loudly when two components claim the
  same capability without an explicit resolution.
- **Honor configurable conflict resolution.** Default to
  `latest` (Gradle's default); accept `strict`, `prefer`,
  `force`, `reject` overrides per-dependency from the recipe.
- **Honor `gradle.lockfile`** per-configuration. The
  `LockfileHints` model needs per-configuration scope, not just
  per-project scope.
- **Distinguish dependencies from constraints** in the
  `Resolver` interface. They have different semantics.
- **Read `build.gradle` / `build.gradle.kts`** for project
  metadata. This is Groovy/Kotlin DSL, not declarative config —
  the adapter may need to invoke Gradle itself for the initial
  parse rather than reimplement the DSL evaluator. Acceptable
  cost for compatibility.
- **Implement `FrontierAwareResolver`** if using a PubGrub-based
  underlying solver. The variant-attribute-matching layer sits
  on top of (not inside) the version-resolution layer; both can
  expose frontiers.

### Lessons for the Sylk substrate protocol

Gradle contributes three substrate-design ideas:

- **Variant-aware metadata format.** Recipes should declare
  variants with attributes; the substrate's `Constraint` type
  should support attribute-based matching as a first-class
  operation. Cargo's features, Python's extras, NuGet's
  framework targeting are all special cases of variant
  attributes; Gradle's general formulation accommodates all of
  them.
- **Capabilities as a separate identity dimension.** Components
  declare capabilities they provide; conflicts are detected at
  resolve time. This is a small addition that catches a real
  class of correctness bug.
- **User-configurable conflict resolution.** The substrate's
  `Resolver` interface should accept a strategy parameter
  (default `latest`, override `strict`/`prefer`/etc.) per-
  dependency or per-resolve. Different ecosystems and different
  use cases need different policies.

---

## Case study: Scala + Coursier (Scala / Maven Central + Ivy)

Scala's case study is a clean **"new resolver replaces old
resolver in same protocol"** parallel to Bun's relationship with
npm. sbt (Scala's build tool) shipped with [Apache Ivy](https://ant.apache.org/ivy/)
as its dependency resolver from inception in 2008 through 2019.
Ivy was correct but slow — typical resolves of mid-sized Scala
projects took 30–90s, and dependency-heavy projects routinely
hit minutes. [Coursier](https://get-coursier.io/), an
alternative Scala resolver written by Alexandre Archambault
starting in 2014, became dramatically popular as a faster
drop-in replacement and was adopted as [sbt 1.3's default
resolver in October 2019](https://www.scala-sbt.org/1.x/docs/sbt-1.3-Release-Notes.html).

The benchmarked speedup is meaningful: an [informal benchmark](https://eed3si9n.com/dependency-resolver-semantics/)
showed Coursier resolving spark-sql in 13s vs Ivy's 51s — about
4× on a representative real-world project. The Coursier story is
the JVM equivalent of Bun replacing npm-the-CLI: same protocol
(Maven Central / Ivy repositories), same artifact format, much
better implementation.

The case study also surfaces a Scala-specific constraint that
shapes the resolver: **the Scala compiler version is encoded
into artifact names**, not as a separate axis. `cats-core_2.13`
and `cats-core_3.x` are different artifact IDs even though
they're "the same library" — the Scala major version is part of
the identity. This produces some unique resolution patterns
worth understanding before implementing a Scala adapter.

### 1. Why Ivy was slow

[Apache Ivy](https://ant.apache.org/ivy/) was designed in 2004,
inherited Ant's XML-everywhere conventions, and was never
optimized for the access patterns sbt produces. Three structural
issues:

- **Sequential metadata fetching.** Ivy's resolver fetched POMs
  serially in dependency-tree order. A 200-dependency project
  required 200 sequential round-trips to Maven Central. With
  ~50ms latency each, that's 10s of pure wait time before any
  resolution work begins.
- **Re-resolution on every command.** sbt invokes Ivy on every
  command requiring dependencies. Without aggressive caching,
  this re-fetched metadata that hadn't changed.
- **In-process JVM overhead.** Ivy ran in sbt's JVM; resolution
  competed with sbt's own work for heap and CPU. The garbage
  collector saw heavy churn from POM XML parsing.

### 2. Coursier's design — pure Scala, parallel, cached

[Coursier](https://github.com/coursier/coursier) was designed
with three explicit goals:

- **Parallel by default.** All metadata fetches fan out
  concurrently across HTTP/2 connections. A 200-dependency
  resolve issues all 200 metadata fetches in parallel and
  processes responses as they arrive.
- **Aggressive local cache.** [`~/.cache/coursier`](https://get-coursier.io/docs/cache)
  is a content-addressed cache shared across every Coursier
  consumer on the machine. Cached artifacts are served from
  disk with no network roundtrip; the cache is keyed by
  artifact coordinates + checksum.
- **Pure-Scala implementation** with no JVM-startup cost (when
  used standalone via the `cs` CLI) and no contention with
  sbt's own work (when used as sbt's resolver, runs in sbt's
  JVM but with much smaller object graphs than Ivy).

For sbt 1.3, Coursier is the default; users who need
Ivy-specific features can opt back to Ivy via `useCoursier :=
false`. The opt-out exists because [Coursier's resolution
semantics differ from Ivy's in some edge cases](https://www.scala-sbt.org/1.x/docs/sbt-1.3-Release-Notes.html)
— most notably SNAPSHOT TTL handling (Coursier caches SNAPSHOTs
for 24h by default; Ivy re-fetched on every resolve).

The lesson here is the same as Bun's: **when the protocol is
fixed, implementation quality is the entire game.** Coursier
doesn't innovate at the protocol layer — it fetches the same
Maven Central POMs Ivy did. It wins by parallelizing,
caching, and avoiding the GC churn Ivy paid.

### 3. Scala-version-in-artifact-name — the unique constraint

Scala's binary compatibility model forces a unique artifact-
naming convention. Code compiled with Scala 2.12 is not
binary-compatible with code compiled with Scala 2.13 or 3.x.
Libraries therefore publish *separate artifacts* per Scala
major version:

```
org.typelevel:cats-core_2.12:2.10.0
org.typelevel:cats-core_2.13:2.10.0
org.typelevel:cats-core_3:2.10.0
```

The `_X.Y` suffix is part of the artifact ID. From a Maven /
Gradle resolver's perspective these are three distinct
artifacts that happen to share a version number.

sbt papers over this with the `%%` operator in build files:

```scala
libraryDependencies += "org.typelevel" %% "cats-core" % "2.10.0"
// Expands to: "org.typelevel:cats-core_2.13:2.10.0" if scalaVersion is 2.13
```

The build tool injects the current Scala version into the
artifact ID at resolve time. Cross-Scala-version projects
(libraries that publish for multiple Scala versions) require
running the resolver multiple times, once per target Scala
version, with different artifact IDs at each pass.

This is **structurally similar to OCaml's compiler-as-package
pattern**, but encoded into artifact identity rather than a
constraint dimension. Both achieve the same correctness
property (incompatible compiler versions never link); the
encoding is different. OCaml's is cleaner conceptually
(compiler is a package); Scala's is operationally simpler
(no special constraint logic in the resolver).

The Sylk lesson: **artifact identity can be a substitute for
runtime-version constraints when binary compatibility is
brittle.** For ecosystems where compiler/runtime versions
produce incompatible artifacts, encoding the version into the
identity may be cleaner than modeling it as a constraint
dimension. The substrate should accommodate both patterns.

### 4. Multi-axis identity — Scala version, Java version, JS/Native targets

Scala 3 expanded the identity-suffix pattern beyond Scala
version. Scala.js (Scala compiled to JavaScript) and
Scala Native (Scala compiled to native binaries) introduce
additional dimensions:

```
cats-core_2.13          # JVM, Scala 2.13
cats-core_3             # JVM, Scala 3
cats-core_sjs1_2.13     # Scala.js 1.x, Scala 2.13
cats-core_sjs1_3        # Scala.js 1.x, Scala 3
cats-core_native0.4_2.13  # Scala Native 0.4, Scala 2.13
```

Each suffix combination is a separate artifact ID. sbt's
`%%%` operator (three percents) is the cross-platform
equivalent of `%%` — it injects both the Scala version *and*
the target platform suffix.

This is the **artifact-identity-encoding** answer to what
Gradle solves with variant attributes. Where Gradle would
declare attributes (`scala.version=2.13`, `target=js`) and let
the resolver pick a variant, Scala encodes the same dimensions
into separate artifact IDs and picks them at build-config time.
Both work; the trade-off is where complexity lives — Scala
pushes it to the ecosystem (every library author must publish
N×M×K artifact variants); Gradle pushes it to the resolver
(every resolver must implement attribute matching).

For Sylk: the substrate should support both patterns, but
**variant-attribute-based identity (Gradle's pattern) is the
preferred default** because it scales better to combinations
the original library author didn't anticipate. Identity-suffix
encoding (Scala's pattern) is the fallback for ecosystems where
binary compatibility is brittle enough that explicit
identification is safer than computed selection.

### Speedup attribution (Coursier vs Ivy on equivalent projects)

| Source | Approximate share of speedup |
|---|---|
| Parallel metadata fetching (HTTP/2 multiplexing) | 45% |
| Aggressive content-addressed cache (`~/.cache/coursier`) | 25% |
| Pure-Scala resolver (less GC, smaller object graphs) | 15% |
| Lockfile-driven re-resolution (when used) | 10% |
| Algorithm refinements (conflict learning) | 5% |

The Coursier story is structurally identical to Bun's: same
protocol, much better implementation, parallelism + cache
dominate.

### Lessons for the Sylk Scala adapter

The Scala adapter shares most infrastructure with the Maven
and Gradle adapters (same Maven Central protocol, same JAR
artifacts). The Scala-specific concerns are:

- **Speak Maven Central protocol** — same as Maven and Gradle
  adapters. Reuse the underlying HTTP transport.
- **Parse Ivy XML metadata** in addition to Maven POMs. Some
  Scala libraries publish to Ivy-only repositories; the
  adapter must handle both formats. (Ivy XML is small enough
  that this is ~200 LOC of parsing.)
- **Encode Scala version + target platform into artifact
  identity** before resolution. The adapter accepts a Scala
  version (and optional Scala.js / Scala Native target) as a
  resolver parameter and rewrites artifact IDs accordingly.
- **Use a Go PubGrub implementation** for the underlying
  version solver — same shared one targeted at Python, Hex,
  RubyGems, NuGet, Composer.
- **Honor the Coursier cache layout** at `~/.cache/coursier`
  if present. Users who already have a populated Coursier
  cache should benefit from it; the substrate can mount it
  read-only for cache hits.
- **Honor [`build.sbt` / `project/Dependencies.scala`](https://www.scala-sbt.org/1.x/docs/Library-Management.html)**
  for project metadata. Like Gradle, this is a Scala DSL —
  the adapter may need to invoke sbt for the initial parse.
- **Honor `build.sbt`-managed lockfiles** when present
  ([sbt-coursier-lock](https://github.com/coursier/sbt-coursier)
  or sbt-dependency-lock). Lockfile-driven re-resolution is
  the warm-cache fast path.
- **Implement `FrontierAwareResolver`** — PubGrub-based; the
  frontier is natural.

### Lessons for the Sylk substrate protocol

Coursier/Scala adds two substrate-design lessons:

- **Identity-suffix encoding** is a valid pattern for
  ecosystems with brittle binary compatibility. The
  substrate's recipe identity should support optional
  identity suffixes so adapters can encode runtime version
  or target platform in the recipe ID when variant attributes
  aren't appropriate.
- **Per-language cache compatibility.** Like the Maven case,
  the substrate's adapter should mount the ecosystem's native
  cache where present (`~/.cache/coursier`,
  `~/.cargo/registry`, `~/.m2/repository`). Pre-populated
  ecosystem caches are free wins.

---

## Case study: OPAM (OCaml / opam-repository)

OPAM is the **research-grade** case study. While every other
ecosystem in this doc settled on a specialized algorithm (PubGrub,
MVS, hand-rolled backtracking with conflict learning), OPAM
expresses its problem in [CUDF](https://www.mancoosi.org/cudf/)
— Common Upgradeability Description Format, a research interchange
format from the [Mancoosi project](https://www.mancoosi.org/) — and
delegates resolution to a general-purpose SAT solver. OPAM is
where dependency resolution meets academic upgradeability
research: clean theory, formal optimization criteria, support
for arbitrary external solvers including SAT-competition
contestants.

OPAM also has a feature no other case study has: **the
language compiler is itself a primary constraint dimension**.
Every OPAM package declares which OCaml compiler versions it's
compatible with, and the OCaml compiler is itself a package
(`ocaml-base-compiler`). The resolver coordinates compiler
version with all package versions across the entire dependency
graph as a single optimization problem. Other ecosystems
gesture at this — Python's `python_requires`, cargo's
`rust-version` — but only as informational filters, not
primary constraints. OPAM is the proof point that
compiler-as-primary-constraint works at scale.

For Sylk, OPAM is mostly an **idea source**: most of its
distinctive design choices are too sophisticated for direct
adoption (general SAT is overkill for the typical resolve),
but the underlying ideas — first-class environments, runtime
version as primary constraint, user-specifiable optimization
criteria — generalize well. The substrate should support these
patterns even if the OPAM adapter itself is conservative.

### 1. CUDF + external SAT solvers — the research-grade architecture

OPAM's [solver architecture](https://opam.ocaml.org/doc/External_solvers.html)
expresses the resolution problem in CUDF format and ships it
to a solver:

```
package: foo
version: 2
depends: bar (>= 1), baz | qux

package: bar
version: 1

# ... etc

request:
install: foo
```

The CUDF format came out of the [Mancoosi International Solver
Competition (MISC)](https://www.mancoosi.org/misc-2012/) run
2010–2012, where research teams competed to build the fastest
correct dependency solver. OPAM is the surviving production
consumer of that research output.

Multiple solvers can answer CUDF problems:

- **mccs** — built into OPAM 2.0+ as the default. A SAT/PB
  (pseudo-boolean) solver written in C++, compact enough to
  embed.
- **aspcud** — historically the recommended external solver,
  built on [ASP (Answer Set Programming)](https://en.wikipedia.org/wiki/Answer_set_programming)
  via the clingo solver. More powerful than mccs for complex
  optimization criteria but heavier to install.
- **packup** — alternative SAT-based solver from the MISC era.
- **opam-0install-solver** — a pure-OCaml alternative
  ([opam-0install-cudf](https://opam.ocaml.org/packages/opam-0install-cudf/))
  using the [0install](https://0install.net/) project's
  solver engine. Faster than SAT for typical resolves
  (especially CI clean-environment installs); uses a custom
  algorithm rather than general SAT.

This is the only ecosystem in the doc with **pluggable
solvers**. The user can choose a different solver per resolve,
trading off speed vs optimality vs feature coverage. For most
workflows the built-in mccs is fine; for pathological resolves
or specific optimization needs, switching to aspcud or
0install-cudf is a one-line config change.

The trade-off: SAT is **overkill for typical resolves**.
Specialized algorithms like PubGrub exploit the structure of
dependency satisfaction (conflicts between version
constraints) that general SAT can't see. For typical OPAM
resolves with a few hundred packages, both mccs and aspcud
finish in milliseconds — the SAT generality isn't a
performance win, just an architectural one. The win shows
when you need optimization beyond "find any solution":
"find the smallest solution," "find the most up-to-date
solution," "find the solution that minimizes packages
removed."

The Sylk lesson is **not** "use SAT solvers." It's **the
solver should be pluggable behind the `Resolver` interface**.
Different ecosystems benefit from different algorithms;
different deployments benefit from different solvers within
an ecosystem. The substrate should make solver choice a
runtime config, not a compile-time architectural choice.

### 2. OCaml compiler version as a primary constraint

Every OPAM package declares which OCaml versions it's
compatible with via the `available:` field:

```
opam-version: "2.0"
name: "lwt"
version: "5.7.0"
depends: [
  "ocaml" {>= "4.08"}
  "dune" {>= "3.0"}
  ...
]
available: arch != "ppc32"
```

The `ocaml` package itself (the compiler) appears in the
dependency graph just like any other package. The resolver
must pick an OCaml version that's compatible with every
selected package's `depends: ocaml` constraint, and changing
the OCaml version requires re-resolving every other package
to find versions compatible with the new compiler.

This is structurally a **multi-axis constraint problem**
where one axis (compiler version) influences the candidate
sets on every other axis. PyPI doesn't do this:
`python_requires` filters which package versions are eligible
but isn't itself a resolution dimension. cargo's
`rust-version` is purely informational. NuGet's framework
targeting comes closest — multi-axis with framework
compatibility — but the framework is a build-time decision,
not a resolved package.

OCaml's choice to make the compiler a first-class
package is a deep design commitment: the compiler can be
upgraded by the resolver, downgraded by the resolver,
swapped between major versions by the resolver. Combined
with switches (next section), this makes "what version of
OCaml am I using?" a property of the resolved environment,
not a precondition.

The Sylk lesson generalizes: **runtime/compiler/toolchain
versions should be modeled as primary resolution
constraints, not as preconditions filtered out before
resolution.** This applies to Python (Python version),
Node (Node version), JVM (JDK version), Go (toolchain
version). The substrate's `Constraint` type should
accommodate "compiler/runtime version" as a constraint
dimension equivalent to package version. OPAM proves this
scales.

### 3. Switches — first-class isolated environments

A [switch](https://opam.ocaml.org/doc/man/opam-switch.html)
is OPAM's name for a named, isolated package environment
with its own OCaml compiler version. Switches are
first-class concept, not a workaround:

- `opam switch create 4.14.1 ocaml-base-compiler.4.14.1`
  creates an environment using OCaml 4.14.1
- `opam switch create 5.1.0 ocaml-base-compiler.5.1.0`
  creates a parallel environment using OCaml 5.1.0
- `opam switch` lists existing switches; `opam switch set
  4.14.1` activates a specific one
- Each switch has its own `~/.opam/{switch}/lib/`,
  `~/.opam/{switch}/bin/`, etc. Packages installed in one
  switch are invisible to another.

This is more first-class than virtualenvs (Python),
gemsets (Ruby), or per-project `node_modules` (Node).
Switches are:

- **Named globally**, not tied to a project directory
- **Reusable across projects** — multiple projects can
  share the same switch
- **Resolver-coherent** — every package in a switch was
  picked by the resolver to be compatible with every
  other package and the switch's compiler
- **Cheap to create** (relatively): a switch shares
  immutable files across switches via hardlinks where
  possible

Switches map naturally to Sylk's substrate concepts. A
"switch" is structurally a **named environment** carved
from the recipe-store, materializing a specific resolved
closure. The substrate's recipe-store already supports
this pattern via the materializer; OPAM is the proof
point that named global environments are a more useful
primitive than per-project virtualenvs.

The Sylk lesson: **the substrate should treat environments
as first-class, named, globally-addressable objects** —
not as side effects of project-local materializations.
A user should be able to create an environment, install
recipes into it, share it across projects, and tear it
down independently of any specific project.

### 4. opam-repository as a git tree

OPAM's canonical registry is [opam-repository](https://github.com/ocaml/opam-repository),
a git repository on GitHub. Each package version is a
directory:

```
opam-repository/
  packages/
    lwt/
      lwt.5.7.0/
        opam              # declarative metadata
        url               # source URL + checksum
      lwt.5.6.0/
        opam
        url
      ...
```

Clients clone the repository and `opam update` runs `git
pull` to sync. The git history provides natural versioning
and auditability — every change to every package's
metadata is a git commit, attributable and reverible.

This is a **versioned-source-tree-as-registry** pattern
unique to OPAM (and to Nixpkgs in spirit, though Nix's
model is broader). Two trade-offs:

- **Auditable, rebaseable, fork-able.** Anyone can fork
  opam-repository, propose changes via PR, or maintain a
  parallel namespace. The registry is just a git tree;
  governance is git workflow. Compare to opaque
  centralized registries (npm, PyPI) where forking
  requires running your own service.
- **Heavyweight to clone.** opam-repository is ~2GB
  uncompressed and growing. Initial clone is slow; users
  on slow connections suffer. OPAM has worked around this
  with shallow clones and incremental updates, but it's
  fundamentally a git-clone-at-scale problem. Cargo's
  sparse index solved the analogous problem better by
  fetching only what's needed.

The Sylk lesson is mixed: **registry-as-git provides
governance and auditability that opaque registries don't,
but at significant scale cost.** The substrate's
recipe-store should not default to git-tree distribution
(too heavy), but should support a git-backed feed
*option* for users who want the git workflow's auditability
benefits.

### 5. Optimization criteria — user-specified resolver preferences

OPAM exposes the SAT solver's [optimization criteria](https://ocaml.github.io/platform-dev/doc/Specifying_Solver_Preferences.html)
to the user via `--criteria`:

```
opam install foo --criteria '-count(removed),-count(down),-sum(solution,installedsize)'
```

The string above asks the solver to minimize (in priority
order): packages removed, packages downgraded, total
installed size. The defaults are sensible for most uses;
the explicit form is an escape hatch for users with
specific needs (CI bandwidth limits, security-conscious
"prefer up-to-date" preference, archive deployments
preferring small footprint).

This is unique among the case studies — every other
resolver hardcodes its optimization criteria. Cargo
optimizes for "newest satisfying version" implicitly. npm
optimizes for "minimum install set with peer-dep
satisfaction." Go's MVS optimizes for "lowest common
maximum."

The OPAM lesson is that **optimization criteria are policy,
not implementation**, and exposing them lets users encode
domain-specific preferences without forking the resolver.
The trade-off: the criteria language is opaque to most
users, and the "wrong" defaults can produce surprising
selections.

For Sylk: **the substrate's `Resolver` interface should
support optimization-criteria pass-through where the
underlying solver supports it.** Most adapters won't expose
this (PubGrub has fixed criteria; MVS has no criteria).
The OPAM adapter and any future SAT-based adapters should.

### 6. opam-0install-solver — when SAT is overkill

The [opam-0install-solver](https://github.com/ocaml-opam/opam-0install-solver)
is the alternative to mccs/aspcud for cases where SAT
generality isn't needed. Built on the
[0install](https://0install.net/) project's solver, it's a
pure OCaml implementation using a specialized algorithm
rather than general SAT.

The pitch: in CI clean-environment builds and similar
workflows where there are no existing-package upgrade
constraints to consider, the 0install algorithm produces
solutions faster than SAT. The trade-off: it doesn't
support OPAM's full optimization criteria language.

This is a useful pattern: **provide a fast specialized
solver for common cases, fall back to a general SAT solver
for complex cases.** OPAM lets the user choose; in
practice most workflows use mccs (built-in default), CI
workflows use 0install for speed, and pathological
resolves use aspcud for the optimization power.

The Sylk lesson is the same as the pluggable-solvers
lesson from section 1: **the substrate's `Resolver`
interface should accommodate multiple solver
implementations per ecosystem**, with the user able to
select among them. Don't lock the OPAM adapter (or any
adapter) into one solver; expose the choice.

### Speedup attribution

OPAM is hard to attribute speedups to in the same way as
the other case studies because OPAM has prioritized
correctness and flexibility over raw speed throughout its
history. The architecture is research-grade; the
performance is "good enough." The breakdown of why OPAM
is *acceptable*-fast despite the SAT overhead:

| Source | Approximate impact |
|---|---|
| Small ecosystem (~5K packages) — less metadata to chew | 30% |
| Built-in mccs solver (no external process) since 2.0 | 25% |
| 0install-solver alternative for CI workflows | 20% |
| Local repository cache + git-incremental updates | 15% |
| Switch-shared file deduplication | 10% |

The "small ecosystem" line dominates honestly. OPAM at
PyPI scale would be slower because SAT is asymptotically
worse than PubGrub. The OCaml ecosystem's smaller size
keeps the SAT overhead invisible.

### Sidebar: LuaRocks (Lua / luarocks.org)

[LuaRocks](https://luarocks.org/) is the smallest mainstream
ecosystem in this doc (~5K rocks vs OCaml's ~5K packages, npm's
~3M). It's worth a sidebar rather than a full case study because
its design closely mirrors OPAM's switches at smaller scale, and
the resolver itself is hand-rolled and unremarkable. The
LuaRocks-specific lessons fit comfortably under the OPAM section.

The interesting parallel to OPAM: LuaRocks supports
[multiple installation trees](https://github.com/luarocks/luarocks/wiki/Rocks-trees-and-the-rocks-trees-list)
— `system`, `user`, and arbitrary user-defined trees per project
or per Lua version. Each tree is independently materialized; a
project can pin to a specific tree or compose multiple. This is
structurally what OPAM switches are: named, isolated package
environments. LuaRocks just doesn't elevate "switch" to the same
first-class status — they're configured via `--tree` flags
rather than top-level commands.

The other LuaRocks-specific point: **rockspecs are Lua files,
not declarative metadata**. A `.rockspec` is a Lua script that
returns a table of metadata when evaluated. This makes them
expressive (any Lua computation can produce metadata) but
opaque to non-Lua tooling. The substrate's LuaRocks adapter
must either embed a Lua interpreter or shell out to `luarocks`
itself for metadata extraction — the same compatibility cost
the Gradle adapter pays for build.gradle.kts.

For Sylk:

- The substrate's named-environment primitive (designed for
  OPAM switches) covers LuaRocks trees natively. No
  LuaRocks-specific work needed at the substrate layer.
- The LuaRocks adapter is small (the resolver is simple,
  ecosystem is small) but pays the Lua-script-evaluation cost
  for rockspec parsing. Embed a minimal Lua interpreter
  ([gopher-lua](https://github.com/yuin/gopher-lua) or
  similar) rather than shelling out for performance.
- LuaRocks supports per-Lua-version installation paths —
  the adapter must accept Lua version as a constraint and
  resolve into the appropriate tree.

### Lessons for the Sylk OPAM adapter

The OPAM ecosystem is small enough that adapter performance
isn't a critical concern. The adapter's design priorities
are correctness and ecosystem compatibility, not speed:

- **Speak CUDF natively.** The substrate's OPAM adapter
  should encode resolution problems in CUDF and consume
  results in the same format. This makes solver choice a
  config option rather than an architectural commitment.
- **Default to the built-in mccs equivalent**. If Sylk
  ships a Go CUDF/SAT solver, use it as default. If not,
  shell out to mccs (small enough to bundle) or to OPAM's
  own solver via subprocess.
- **Support 0install-style fast path** for CI/clean-env
  installs. The substrate's resolver chain can detect "no
  existing installation to preserve" and route to the
  faster algorithm.
- **Honor `opam` files literally.** OPAM's metadata
  semantics are subtle (variables, filters, conditional
  dependencies); reimplementing them incorrectly produces
  different resolution results from upstream OPAM, which
  breaks ecosystem compatibility.
- **Treat `ocaml` as a primary constraint package**, not a
  precondition filter. Substrate `Constraint` modeling
  must accommodate this — same model as for cross-runtime
  Python/Node/JVM/etc. version constraints.
- **Mirror opam-repository locally**. Don't fetch from
  GitHub on every resolve; clone once, `git pull`
  incrementally. The substrate's recipe-store can host
  the mirror as a git-backed feed.
- **Implement OPAM switches as substrate environments.**
  The substrate's named-environment primitive maps
  directly to OPAM switches; the adapter should expose
  switch creation/management through that primitive.

### Lessons for the Sylk substrate protocol

OPAM contributes three substrate-design ideas worth
adopting even though OPAM itself is a small-ecosystem
case:

- **Pluggable solvers behind the `Resolver` interface.**
  Different ecosystems and different deployments benefit
  from different solver implementations. The substrate's
  interface should allow runtime solver selection rather
  than baking one algorithm in. The `FrontierAwareResolver`
  extension already gestures at this; OPAM provides the
  full pluggable-solver model worth generalizing.
- **Runtime/compiler version as a primary constraint
  dimension.** OCaml proves this works. Sylk recipes that
  declare runtime requirements (Python version, Node
  version, JVM version) should treat those as primary
  constraints to be resolved coherently, not as
  preconditions filtered out before resolution starts.
- **First-class named environments.** Switches are more
  useful than per-project virtualenvs because they're
  globally addressable, sharable across projects, and
  resolver-coherent. The substrate's materializer should
  treat named environments as the primary primitive,
  with project-local materializations as a special case.

---

## Case study: Haskell — Cabal vs Stack (Hackage + Stackage)

Haskell is the **most algorithmically demanding** ecosystem in
this doc, and its response is unique: rather than build a
better resolver, the community built a **second tooling stack
that doesn't resolve at all**. Cabal (the original) is a
constraint-satisfaction resolver running against the Hackage
registry. Stack (introduced 2015) refuses to do constraint
satisfaction — instead it consumes
[Stackage LTS](https://www.stackage.org/) snapshots, hand-
curated package sets where every included version is known to
compile together. This is the "don't solve, curate"
architectural pattern, and it's the most genuinely-different
idea in the missing case studies.

The reason Haskell needed this is the strictness of its type
system. The "diamond dependency problem" — two paths through
the dep graph requesting different versions of the same
package — is a **hard compile error in Haskell**, not a
runtime warning. GHC requires every type to come from one
specific package version; if the resolver picks two versions of
the same package along different paths, the build fails with
incomprehensible type errors. Cabal's solver has to be much
more aggressive about transitive consistency than other
ecosystems, and even with that aggression, "cabal hell"
(unresolvable graphs after every dep update) was a defining
ecosystem complaint for years.

Stack's response was to delegate the consistency problem to
human curators at the [Stackage project](https://www.stackage.org/),
who pick package versions known to mutually compile and publish
the resulting snapshots as named LTS releases. Users pick a
snapshot (`resolver: lts-22.13`) and Stack uses exactly the
versions in that snapshot — no constraint satisfaction
required.

For Sylk, both halves are useful: Cabal as the proof point
that strict-consistency resolvers are possible at scale, and
Stack as the proof point that **you don't always need a solver
— sometimes a curated set is the right answer.**

### 1. Cabal — the strict-consistency solver

[Cabal-install](https://www.haskell.org/cabal/) is the
traditional Haskell resolver, fetching from
[Hackage](https://hackage.haskell.org/) (the central registry,
~16K packages). Its solver is hand-rolled, written in Haskell,
and considerably more sophisticated than most:

- **Transitive consistency required.** The solver guarantees
  that every package in the resolved graph has consistent
  versions of all transitive deps. Two paths through the graph
  requesting `bytestring` must converge on the same
  `bytestring` version, or resolution fails.
- **Conflict learning.** Cabal's solver records which version
  combinations have failed to satisfy constraints and avoids
  re-exploring them. PubGrub-class behavior without being
  PubGrub.
- **Backtracking with priority heuristics.** When backtracking,
  Cabal prefers to demote less-constrained packages first,
  reducing the search space.

Despite this, the solver was historically slow — large dep
graphs with many version constraints could take minutes to
resolve, and "cabal hell" (unresolvable graphs after a Hackage
update) was common. Recent versions (Cabal 3.0+) have improved
substantially via algorithm refinements and better caching.

The Hackage protocol itself is unremarkable — it's a static
file tree similar to Maven Central, with `00-index.tar.gz`
serving as the metadata index (a single tar containing every
package's `.cabal` file at every published version). This is
structurally similar to opam-repository's git-tree-as-registry
but uses tar-over-HTTP rather than git, with `hackage-security`
([TUF](https://theupdateframework.io/)-based) providing
integrity verification.

### 2. Stack — refusing to solve

[Stack](https://docs.haskellstack.org/en/stable/) (Stack the
build tool, not stack as a data structure) was introduced in
2015 by FP Complete as a response to cabal hell. Stack's
core architectural insight: **most users don't need
constraint satisfaction; they need a known-working set of
versions.**

Stack consumes [Stackage](https://www.stackage.org/)
snapshots:

- A **snapshot** is a JSON file listing exact versions of
  every included package. LTS-22.13 (current as of late 2024)
  pins ~3,000 packages at specific versions known to compile
  together.
- Snapshots are **curated by humans**. The Stackage team runs
  builds across all included packages and updates the
  snapshot only when everything still compiles.
- New snapshots are released regularly:
  [LTS](https://www.stackage.org/lts) (long-term support,
  every ~3 months) for stability; [Nightly](https://www.stackage.org/nightly)
  (rolling) for early adopters.

A `stack.yaml` declares which snapshot the project uses:

```yaml
resolver: lts-22.13
packages:
  - .
extra-deps:
  - some-pkg-1.2.3@sha256:abc...
```

Stack uses the snapshot's pinned versions for every package
also in the snapshot; `extra-deps` declares versions for
packages outside the snapshot (with mandatory content hashes).
**Resolution doesn't happen** — there are no version ranges to
satisfy. Stack just downloads the listed versions.

The trade-offs are real:

- **Bounded ecosystem.** A Stackage snapshot covers ~3K
  packages out of ~16K on Hackage. If your project needs a
  package that isn't in the current LTS, you list it as
  `extra-deps` with a specific pin.
- **Snapshot lag.** New package versions land in Hackage
  immediately but take days-to-weeks to reach a snapshot
  (and may never reach LTS if they break the snapshot's
  consistency). Bleeding-edge libraries are a Stack
  weakness.
- **Curation overhead.** Stackage requires humans
  ([Stackage Curators](https://github.com/commercialhaskell/stackage/wiki/Stackage-Curators))
  to maintain the snapshots. The model only works because
  enough volunteers care.

### 3. The architectural lesson — when to solve, when to curate

Cabal and Stack are not "which one is better" — they're
different answers to different operating points:

- **Constraint satisfaction (Cabal-style)** is right when
  consumers can specify their needs precisely and the
  ecosystem is too large or too fast-moving for human
  curation. Most package ecosystems use this model because
  curation doesn't scale to npm-sized registries.
- **Curation (Stackage-style)** is right when correctness
  is brittle (Haskell's type system makes incompatibility a
  hard error) and the ecosystem is small enough to
  hand-coordinate. The trade-off is breadth and freshness for
  guaranteed consistency.

The Sylk lesson generalizes well beyond Haskell: **the
substrate should support both modes.** First-party recipe
ecosystems where Sylk maintains the recipes can publish
"compatibility sets" analogous to Stackage snapshots, and
recipes can reference a set without needing the resolver to
satisfy individual constraints. Third-party ecosystem
adapters (Python, Node, Ruby) keep using PubGrub-class
solvers because curated sets aren't available there.

This is also a useful pattern for **enterprise Sylk
deployments**: a corporate Sylk feed can publish a "blessed
set" of recipe versions that have been tested together,
analogous to a corporate Maven BOM or a Stackage snapshot.
Internal projects pull from the blessed set; resolution is
trivial; consistency is guaranteed.

### 4. `cabal.project.freeze` and `stack.yaml.lock`

Both tools support lockfiles, with different semantics:

- **`cabal.project.freeze`** (Cabal) — generated by
  `cabal freeze`. Pins exact versions of every package in
  the resolved graph. Optional but strongly encouraged for
  applications.
- **`stack.yaml.lock`** (Stack, since v2.5) — generated
  automatically by Stack. Pins the snapshot version, the
  exact resolver URL, and any extra-deps with content
  hashes. Mandatory in modern Stack workflows.

Stack's lockfile is interesting because it pins a *snapshot
identifier*, not individual package versions. The snapshot
itself defines the package versions. This separation is
elegant: the lockfile records *which curated set was used*,
not the resolved tree itself, and snapshots are themselves
content-addressed.

The Sylk lesson: **for ecosystems with curated sets, the
lockfile should pin the set, not the resolved versions.**
This makes lockfiles smaller, more readable, and more
informative (the user can see which curated set was used,
not just a tree of pins).

### Speedup attribution (modern Cabal/Stack vs Cabal circa 2014)

| Source | Approximate share of speedup |
|---|---|
| Cabal solver algorithm refinements (Cabal 3.0+) | 30% |
| Stack's "don't solve, just download" path | 25% |
| Hackage tarball index (single fetch vs per-package) | 20% |
| Local store deduplication and caching | 15% |
| Parallel downloads (Cabal 3.x+) | 10% |

Stack's contribution is unique in this doc: 25% of the
speedup comes from **not running the resolver at all** for
projects that pin a snapshot. The fastest resolution is no
resolution.

### Lessons for the Sylk Haskell adapter

The Haskell adapter has the most architectural choice of any
in the doc — it must support both the Cabal and Stack models:

- **Implement Cabal-compatible constraint satisfaction.** Use
  the substrate's shared Go PubGrub for projects that don't
  use Stackage snapshots. Honor `.cabal` file dependency
  declarations literally.
- **Implement Stack-compatible snapshot consumption.** When
  the project has a `stack.yaml` with a `resolver:` field,
  fetch the snapshot, use its pins as the resolved set,
  apply any `extra-deps` overrides. Skip the resolver
  entirely.
- **Honor the strictness contract.** Two transitive paths to
  the same package must produce the same version. The
  resolver must surface this as a hard error (Cabal mode);
  Stack mode gets it for free from the snapshot's
  pre-coordination.
- **Speak Hackage protocol.** Static file tree + index tarball
  + hackage-security TUF verification. Reuse the substrate's
  HTTP transport.
- **Honor `cabal.project.freeze` and `stack.yaml.lock`** as
  `LockfileHints` — mode-dependent (Cabal-style pins or
  Stack-style snapshot ID).
- **Implement `FrontierAwareResolver`** for Cabal mode. Skip
  for Stack mode (no resolution).

### Lessons for the Sylk substrate protocol

Haskell contributes one substrate-level idea worth adopting:

- **Curated sets as a first-class alternative to resolver-
  driven coordination.** The substrate should support
  publishing named "compatibility sets" of recipe versions
  that have been validated to work together. Recipes can
  reference a set ID instead of individual constraints; the
  resolver short-circuits to "use the set's pins." Both
  Stackage and corporate Maven BOMs are concrete examples of
  the same pattern at different scales.

---

## Case study: Swift Package Manager (Swift / git-as-registry)

Swift Package Manager (SwiftPM) is the **no-central-registry**
case study in its purest form. Where Go modules use git URLs
as identity but proxy them through `proxy.golang.org` for
caching and integrity, SwiftPM goes further: every dependency
is a **git URL fetched directly from its source repository**.
There is no SwiftPM-equivalent of GOPROXY for the open
ecosystem; Apple's [package registry standard](https://github.com/swiftlang/swift-package-manager/blob/main/Documentation/PackageRegistry/PackageRegistryUsage.md)
exists but is opt-in for private/enterprise feeds, not the
default for open-source dependencies.

SwiftPM was also one of the **earliest PubGrub adopters**
(2018–2019), before uv, Bundler, Hex, or Cargo's adoption.
Apple's [PubGrub implementation](https://github.com/swiftlang/swift-package-manager/blob/main/Sources/PackageGraph/Resolution/PubGrub/PubGrubDependencyResolver.swift)
is the production reference for PubGrub-in-Swift and was
the first major commercial deployment of the algorithm
outside its original Dart implementation.

For Sylk, SwiftPM is most useful as the **counter-example to
having a registry at all**: the design proves you don't need
one if your ecosystem accepts git URLs as identity, but the
costs (no metadata caching, no signing, no transparency log,
no usage analytics) are real.

### 1. Git URLs as package identity

A SwiftPM `Package.swift` declares dependencies as git URLs:

```swift
let package = Package(
    name: "MyApp",
    dependencies: [
        .package(url: "https://github.com/apple/swift-argument-parser",
                 from: "1.3.0"),
        .package(url: "https://github.com/pointfreeco/swift-snapshot-testing",
                 from: "1.15.0"),
    ],
    ...
)
```

The package's identity is its git URL. The version is a git tag
(SemVer). To resolve dependencies, SwiftPM:

1. Clones each dependency's git repo (shallow if possible)
2. Reads `Package.swift` from the cloned repo at the tagged
   version
3. Recurses into transitive dependencies
4. Runs PubGrub against the version constraints discovered

There is no metadata API to query, no central index, no
sparse-index protocol. Every metadata fetch is a `git ls-remote`
or `git clone` against the source repository. For projects
hosted on GitHub (the overwhelming majority), this means every
SwiftPM resolve hits GitHub's git endpoints — which has its
own rate limiting and performance characteristics.

The trade-offs are stark:

- **Trivial to publish.** Anyone with a git repository can
  publish a Swift package. No registration with Apple, no
  account on a registry, no review process.
- **Trivial to fork/mirror.** Forking a package is forking its
  git repo. Mirroring is `git mirror`. No centralized control
  point.
- **No metadata caching.** Every resolve hits the source
  repos. Slow, bandwidth-intensive, susceptible to upstream
  outages (if GitHub is down, no Swift project can resolve).
- **No signing or integrity.** Git provides commit hashes but
  no signing of the package contents themselves. The model
  trusts the source repository.
- **No usage analytics.** No registry means no aggregate
  download stats, no popularity metrics, no security
  vulnerability dataset.

Apple's [package registry standard](https://github.com/swiftlang/swift-package-manager/blob/main/Documentation/PackageRegistry/PackageRegistryUsage.md)
exists as an opt-in alternative — feeds like
[CocoaPods Trunk](https://cocoapods.org/), private corporate
feeds, or [Cloudsmith](https://docs.cloudsmith.com/formats/swift-registry)
serve packages via a defined HTTP API rather than git URLs.
Adoption is mostly in enterprise contexts; the open-source
community remains git-URL-based.

### 2. PubGrub adoption — early production reference

SwiftPM's [PubGrub implementation](https://github.com/swiftlang/swift-package-manager/blob/main/Sources/PackageGraph/Resolution/PubGrub/PubGrubDependencyResolver.swift)
shipped in [Swift 5.0 (March 2019)](https://www.swift.org/blog/swift-5-released/),
making SwiftPM one of the earliest commercial deployments of
PubGrub outside Dart's reference implementation. The
motivations matched the later wave (Bundler 2.4, Hex 2.0,
Cargo's in-progress migration):

- **Backtrack performance** on complex dependency graphs.
  SwiftPM's previous resolver was hand-rolled and had
  pathological cases that took minutes.
- **Diagnostic quality.** PubGrub's derivation history
  produces explainable conflict messages, important for
  Apple's "everything should just work" UX bar.

The implementation has been stable since 2019 with
incremental improvements. It informed the
[pubgrub-rs](https://github.com/pubgrub-rs/pubgrub) Rust
implementation that uv and (in-progress) Cargo use, by being
the first proof point that PubGrub scales to production
ecosystem sizes.

### 3. `Package.resolved` — the lockfile

[`Package.resolved`](https://www.polpiella.dev/safely-pinning-spm-depedencies-to-exact-versions/)
is SwiftPM's lockfile, generated automatically when
dependencies are resolved. It pins each dependency to a
specific version + git revision + content hash:

```json
{
  "pins": [
    {
      "identity": "swift-argument-parser",
      "kind": "remoteSourceControl",
      "location": "https://github.com/apple/swift-argument-parser",
      "state": {
        "revision": "8f4d2753f0e4778c76d5f05ad16c74f707390531",
        "version": "1.3.0"
      }
    }
  ]
}
```

The git revision is the integrity guarantee — if the upstream
tag is moved or rewritten, the SHA mismatch surfaces at
resolve time. This is the same model as Go's `go.sum` with
git revisions standing in for content hashes.

Lockfile is committed to version control for applications,
typically not committed for libraries (so downstream consumers
can pick fresh versions). Modern SwiftPM treats the lockfile
as authoritative for `swift build` (no re-resolution); only
`swift package update` re-resolves and refreshes the lockfile.

### Speedup attribution

SwiftPM is hard to attribute speedups to because it doesn't
have a "fast" reference to compare against — every resolve
hits real git endpoints, and there's no protocol-level
optimization available short of switching to the package
registry standard. The breakdown of why it's *not slower than
it appears*:

| Source | Approximate impact |
|---|---|
| Local clone cache (resolve once, reuse across projects) | 35% |
| PubGrub algorithm efficiency (vs old hand-rolled) | 25% |
| Shallow git clones (avoid full history) | 20% |
| Lockfile-driven re-resolution (warm cache) | 15% |
| Concurrent dependency discovery | 5% |

The local clone cache is doing most of the work. Without it,
every resolve would re-clone every dependency. With it, warm
resolves are nearly instant.

### Lessons for the Sylk Swift adapter

- **Speak both git URLs and the package registry standard.**
  Open-source Swift projects use git URLs; enterprise
  deployments use the registry standard. The adapter needs
  both code paths.
- **Use shallow clones aggressively.** `git clone --depth 1
  --branch v1.2.3` is faster than full-history clone and
  sufficient for resolution. Reuse clones across projects via
  the substrate's content-addressed cache.
- **Use a Go PubGrub implementation** — same shared one
  across the substrate.
- **Honor `Package.resolved`** as `LockfileHints`. Treat git
  revisions as integrity guarantees; verify SHAs before
  using cached content.
- **Implement `FrontierAwareResolver`** — PubGrub-based; the
  frontier is natural.
- **Mount `~/Library/Caches/org.swift.swiftpm/`** if present
  for cache hits from native SwiftPM use.

### Lessons for the Sylk substrate protocol

SwiftPM contributes one negative lesson — what *not* to do
unless the trade-offs make sense:

- **No-central-registry has real costs.** It's a tempting
  design choice (no infrastructure to maintain, no
  governance overhead, trivial to publish), but it forfeits
  metadata caching, integrity guarantees beyond git
  revisions, and the entire visibility/analytics layer.
  Sylk's substrate should support git-URL-based recipes as
  an option (similar to Go's git-as-source pattern) but
  default to a registry-based model where the substrate has
  a metadata API for caching, signing, and visibility.

---

## Case study: Zig — `build.zig.zon` (Zig / no registry, content-hash mandatory)

Zig is the **modern minimalist** case study. Released in 2023
with [Zig 0.11](https://ziglang.org/download/0.11.0/release-notes.html),
Zig's package manager adopts the most austere design in this
doc: **no central registry, mandatory content hashes for every
dependency, no version ranges, no resolver in the constraint-
satisfaction sense.** A `build.zig.zon` file declares
dependencies as URL+hash pairs; the build system fetches them
and verifies the hashes. That's it.

Zig is what you get when you start from scratch in 2023 with
the lessons of the prior 20 years and zero compatibility
constraints. The result is closer to Go modules' design than
anything else — content-addressed, integrity-mandatory,
git-URL-based — but stripped further: no `GOPROXY`-equivalent,
no semver ranges, no metadata API. Every dependency is
literally a URL + the SHA-256 hash of what's expected to be at
that URL.

For Sylk, Zig is a useful endpoint for thinking about how
minimal a package manager can be while still serving its
purpose. The answer is "very minimal" — but the ecosystem is
small enough (Zig 0.11 had ~hundreds of packages, growing) that
the minimalism hasn't yet hit the scaling problems npm or PyPI
forced larger ecosystems to confront.

### 1. `build.zig.zon` — declarative dependencies

A [`build.zig.zon`](https://github.com/ziglang/zig/blob/master/doc/build.zig.zon.md)
is a Zig Object Notation file (Zig's S-expression-like config
format) declaring the project's dependencies:

```zig
.{
    .name = "myproject",
    .version = "0.1.0",
    .dependencies = .{
        .raylib = .{
            .url = "https://github.com/raysan5/raylib/archive/5.0.tar.gz",
            .hash = "1220abcdef0123456789abcdef...",
        },
        .zigimg = .{
            .url = "https://github.com/zigimg/zigimg/archive/abc123.tar.gz",
            .hash = "1220fedcba9876543210fedcba...",
        },
    },
    .paths = .{ "build.zig", "build.zig.zon", "src" },
}
```

Each dependency is a `.url` + `.hash` pair. The URL is fetched
once; the hash is verified against the fetched content; the
result is cached in `~/.cache/zig/p/{hash}/`. There are no
version ranges, no semver, no transitive constraint resolution
— each dependency is pinned to exactly what the URL serves at
the moment the hash was computed.

### 2. `zig fetch --save` — the discovery workflow

Computing the hash by hand is impractical, so Zig provides
[`zig fetch --save {url}`](https://www.bradcypert.com/adding-dependencies-to-your-zig-project-with-zig-fetch/):

```bash
$ zig fetch --save https://github.com/raysan5/raylib/archive/5.0.tar.gz
```

This downloads the URL, computes the hash, and updates the
project's `build.zig.zon` with the URL+hash pair. The user
never types a hash manually.

The model is: dependencies are added by URL; the tooling
records the URL+hash for reproducibility. Updates are
explicit (`zig fetch --save` re-runs against a new URL); there
is no "compatible automatic update" the way semver-aware
ecosystems support.

### 3. Transitive resolution — flat, no version ranges

Each dependency's own `build.zig.zon` is fetched and processed
when the dependency is materialized. **Transitive dependencies
are added as separate top-level entries** in the consuming
project's `build.zig.zon` — there is no automatic transitive
resolution where the consumer's tooling decides which version
of a transitively-required dependency to use.

This pushes coordination to the **consumer**: if two
dependencies both transitively need `lib-x` at different
URLs/hashes, the consuming project must explicitly add `lib-x`
as a direct dependency to disambiguate. There's no "nearest
wins" or "highest version wins" or PubGrub-style satisfaction
— the consumer makes the call by adding an explicit pin.

This is structurally **MVS pushed to its logical extreme**: not
even minimum-version selection, just "the user pins exactly the
versions they want, the tooling enforces the pins, no
inference."

The ecosystem trade-off: **Zig requires direct human
coordination for any transitive conflict**. This works at small
scale (the consuming project has a manageable number of
direct deps); it would not work at npm scale (where transitive
trees are thousands of packages deep). Zig is betting on the
ecosystem staying small enough that explicit coordination is
tractable.

### 4. Cache architecture — content-addressed, single global

Zig's cache at `~/.cache/zig/p/{hash}/` is purely content-
addressed: each fetched dependency lives in a directory named
by its hash. Multiple projects on the same machine sharing
identical dependencies share the same on-disk content. The
cache is immutable — once written, never modified.

Materialization is direct: Zig's build system reads from the
cache directory; there's no separate "install" step that copies
content into a project-local node_modules-equivalent. The
build system just compiles against the cached source.

This is the cleanest expression of the content-addressed cache
pattern in any case study — no separate cache+store layers,
no per-project materialization, no `vendor/` equivalent. The
cache *is* the materialization.

### Speedup attribution

Zig's package manager is fast in absolute terms because it
does so little. The breakdown isn't really speedup-vs-naive —
it's "why doing very little works":

| Design choice | Contribution to "fast" |
|---|---|
| No transitive resolution (consumer coordinates) | 40% |
| No version ranges (no constraint satisfaction) | 25% |
| Content-addressed single-global cache (no per-project install) | 20% |
| Mandatory hashes (no integrity verification round-trips) | 10% |
| Direct fetch from source URLs (no metadata API to query) | 5% |

Zig's "speed" comes from removing problems other ecosystems
have to solve. The cost is that consumers do the coordination
work (transitively conflicting deps) the resolver would do
elsewhere. For a small ecosystem this is acceptable; the model
will need to evolve as the Zig ecosystem grows.

### Lessons for the Sylk Zig adapter

The Zig adapter is the simplest in this doc:

- **No constraint solver needed.** The adapter accepts
  URL+hash pairs from `build.zig.zon`, fetches each URL,
  verifies each hash, caches the result. No PubGrub, no MVS,
  no resolver loop.
- **Content-addressed cache** in the substrate's recipe-store,
  keyed by Zig's hash format (`1220` prefix + multihash).
- **Mandatory hash verification** — never serve cached content
  without verifying the hash matches the recorded value.
- **Honor `build.zig.zon` as both manifest and lockfile.** The
  manifest *is* the lockfile in Zig's model; no separate
  lockfile file.
- **`FrontierAwareResolver` not applicable.** No frontier; no
  candidates. The adapter just fetches what's listed.
- **Use `zig fetch`-equivalent logic for hash computation**
  when adding new dependencies. The substrate's recipe author
  workflow should automate hash computation rather than
  forcing manual entry.

### Lessons for the Sylk substrate protocol

Zig contributes one substrate-level lesson:

- **Mandatory content hashes are tractable for first-party
  recipes**, especially when paired with content-addressed
  caching. The substrate should consider making integrity
  hashes a required field on first-party recipe references
  rather than optional. Third-party ecosystem adapters
  (Python, Node, Ruby) can't enforce this because their
  source ecosystems don't, but Sylk's first-party recipes
  benefit from the Zig discipline: every reference is a
  URL+hash pair, every fetch is verified, no integrity
  ambiguity.

---

## Synthesis: cross-cutting lessons for Sylk substrate design

Across the eight case studies, the same lessons recur in
different shapes. This section consolidates them into a
single set of contracts the substrate should satisfy and
adapters should implement.

### Protocol design

The substrate's first-party recipe-store metadata API
should adopt the **best ideas from cargo, NuGet, and Hex**:

- **Per-recipe-version metadata at stable cacheable URLs**
  (cargo's sparse index, NuGet's PackageBaseAddress,
  Hex's `/packages/{name}`). Never bundle metadata across
  versions or across recipes. Static files; one resource
  per `(recipe, version)`.
- **Service-index capability discovery** (NuGet pattern).
  A single `/index.json` per feed declares which
  resources the feed implements; clients learn URL
  shapes from the feed. Forward-compatible against new
  capabilities.
- **Aggregate sync endpoints distinct from per-recipe
  endpoints** (Hex's `/names` + `/versions`). Per-recipe
  for resolvers; aggregate for mirror infrastructure.
  Don't conflate.
- **Optionally signed metadata at the protocol layer**
  (Hex's signed protobuf). Defense-in-depth beyond
  hash-only lockfile verification. Opt-in per feed.
- **Static-file-tree layout as one supported mode**
  (Maven). Predictable URLs from coordinates make
  mirroring trivial, debugging direct, implementation
  cost minimal.

The substrate's protocol should never:

- **Bundle metadata across versions** (npm registry's
  fatal flaw). Forces clients to download megabytes to
  read a few KB.
- **Use server-computed query endpoints** (RubyGems'
  intermediate dependency API). Breaks CDN caching and
  serializes on the server.
- **Rely on git tree distribution as the default**
  (OPAM's heavyweight registry). Supports the
  governance use case but penalizes everyone else.

### Resolver design

The substrate's `Resolver` interface should:

- **Accept a list of feeds, not a single registry URL**
  (NuGet, Maven). Multi-feed federation is non-optional
  for real-world deployments.
- **Expose an extension for frontier-aware decision-event
  streaming** (`FrontierAwareResolver`). Resolvers that
  can implement it (PubGrub, MVS, eager-conflict
  resolvers) get speculative-prefetching wins; resolvers
  that can't (Arborist's multi-pass, Maven's nearest-wins)
  forfeit the optimization.
- **Allow pluggable solver implementations per ecosystem**
  (OPAM). Don't bake one algorithm into the interface.
- **Model multi-axis constraints natively** (NuGet's
  framework targeting, OPAM's compiler version, Python's
  platform tags). The substrate's `Constraint` type
  needs at minimum: version range, platform tuple,
  feature set, runtime version.
- **Resolve conflicts eagerly during graph construction,
  not in post-hoc passes** (.NET 9's resolver rewrite).
  Representation choice — flat set vs graph — matters
  more than algorithm choice.
- **Use real constraint satisfaction, not depth/order-based
  mediation** (the negative Maven lesson). Make the wrong
  thing impossible to express in the interface.

### Materialization

Across uv, cargo, Bun, NuGet, and Maven, the same
materialization pattern recurs:

- **Single content-addressed cache** keyed by
  `(name, version, platform, hash)`, shared across
  projects and (when meaningful) across runtime versions.
- **Reflinks (CoW) on btrfs/APFS/ZFS/XFS** for free
  materialization.
- **Hardlinks as fallback** on filesystems without
  reflinks (Linux ext4, macOS HFS+, Windows NTFS).
- **Byte-copy only as last-resort fallback** for
  cross-filesystem materialization.
- **Cache layout compatible with native ecosystem tools**
  where possible (Maven's `~/.m2`-shaped cache, cargo's
  `~/.cargo/registry`-shaped cache) so users can
  out-of-band debug with native tooling.
- **No build-during-resolve** (uv's hard rule). Sdist
  building, native compilation, anything that takes
  more than metadata extraction belongs in
  materialization, not resolution.

### Lockfiles

The substrate should:

- **Default to lockfile generation** (cargo, npm, Bundler
  patterns). RubyGems and Maven's experience demonstrates
  conclusively that retrofitting lockfiles is harder than
  shipping them initially.
- **Allow opt-out for true library recipes** (NuGet's
  pattern). Library authors want their downstream
  consumers to pick fresh versions; lockfile presence
  forces stale pins.
- **Treat existing lockfiles as hard preferences, not
  soft hints** (cargo, Go patterns). Re-resolution
  should preserve pins unless they're invalid; only
  consider new versions when the pin no longer
  satisfies a constraint.
- **Include integrity hashes in the lockfile** (Go's
  `go.sum`, npm's `package-lock.json`,
  Cargo.lock's `checksum` field). Lockfile + hashes is
  the cheapest defense against supply-chain attacks.
- **Generate ecosystem-native lockfile formats** for
  compatibility (`Cargo.lock`, `package-lock.json`,
  `Gemfile.lock`, `mix.lock`, `packages.lock.json`,
  `go.sum`) so users' external tooling sees the same
  resolved tree.

### Caching

The substrate's caching architecture should:

- **Cache metadata in indexed SQLite**, not per-resolve
  in-memory maps (uv pattern). Subsequent resolves of
  the same recipe should be O(1) lookups.
- **Negative cache "no version of X satisfies Y"**
  per-resolve so backtracks don't repeat work.
- **Honor `Etag` and `Last-Modified` headers** for
  freshness checks (NuGet, npm registry patterns).
  Conditional GETs save bandwidth.
- **Share metadata cache across ecosystems** where
  practical. Python, Node, and Ruby recipes resolved in
  the same Sylk session should share the underlying
  HTTP-fetch cache even though their resolvers are
  different.

### Implementation language

The substrate is Go. Adapters should:

- **Use shared `http.Client` with HTTP/2** per adapter
  instance, not per request. Go's HTTP/2 default makes
  this free.
- **Bound concurrency with `errgroup`** rather than
  unbounded fan-out. uv defaults to 50 concurrent
  fetches; Sylk should benchmark per ecosystem.
- **Use `encoding/json` with caution for large
  documents.** For npm-scale registry responses, prefer
  streaming parsers (`fastjson`) or hand-rolled
  SAX-style parsing.
- **Use Go's content-addressable file primitives**
  (`os.Link` for hardlinks, `unix.Ioctl` with
  `FICLONE` for reflinks) rather than shelling out to
  `cp` or `ln`.

### Final synthesis

The case studies converge on a single architectural truth:
**resolver speed comes from below the algorithm.** The
algorithm is necessary but rarely sufficient. The
performance hierarchy, in descending order of impact:

1. **Protocol design** (cargo's sparse index, Hex's
   per-package endpoints, NuGet's service index) — gates
   how much metadata clients fetch and how cacheable it
   is. The largest single lever.
2. **Cache architecture** (uv's content-addressed cache,
   cargo's `~/.cargo/registry`, hardlink/reflink
   materialization) — gates re-resolve and
   materialization speed.
3. **Build-on-resolve avoidance** (uv's wheel-first rule,
   universal pattern) — eliminates the worst-case
   pathology where naive resolvers compile sdists during
   resolution.
4. **Implementation quality** (Bun vs npm) — when
   protocol and algorithm are fixed, native code with
   appropriate concurrency and parsing strategy is the
   only remaining lever.
5. **Algorithm choice** (PubGrub vs hand-rolled, MVS vs
   PubGrub) — usually contributes <10% of speedup. Worth
   doing for new resolvers; rarely worth a rewrite for
   working ones.

The Sylk substrate's per-ecosystem adapters should be
designed in this priority order. Get the protocol
contract right first; the cache architecture second;
the build-on-resolve discipline third; the
implementation quality fourth; and only then think
about the algorithm. This is the inverse of how most
resolver projects start — and it's why most resolver
projects are slower than they should be.
