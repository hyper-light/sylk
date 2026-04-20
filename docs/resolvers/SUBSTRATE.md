# SUBSTRATE.md — Shared Resolver Primitives

This plan specifies the substrate-level primitives every adapter
depends on. Adapters are implementation-small because this substrate
is implementation-large. Every primitive here is used by at least
three adapters; most are used by all.

## 1. Overview

The substrate provides:

- The `Resolver` interface and `FrontierAwareResolver` extension
- A Go-native PubGrub implementation (`core/resolver/pubgrub`)
- The `Constraint` type system (version, platform, features,
  capabilities, runtime-version)
- The HTTP/2 transport layer with connection pooling, retry, auth
- The metadata cache (indexed SQLite, negative cache, Etag-aware)
- The content-addressed recipe store (materializer + reflink /
  hardlink primitives)
- The lockfile framework (parse, read-as-hint, write-canonical)
- The frontier-driven prefetch coordinator
- The multi-feed federation abstraction
- The authority / credentials manager for per-feed auth
- Observability hooks (structured logs, traces, metrics) every
  adapter can emit to

Adapters supply only the ecosystem-specific parts: metadata parsing,
registry protocol specifics, materialization post-processing (e.g.
Node's node_modules layout vs Cargo's target/), lockfile format.

**Design principle.** The substrate absorbs complexity so adapters
don't re-solve solved problems. An adapter's implementation should
be ~1–5K lines; the substrate can be ~20–30K lines and that's
acceptable because it's paid once.

## 2. Data Model

### 2.1 RecipeID and Coordinates

```go
// RecipeID is the substrate's canonical recipe identifier. Adapters
// encode ecosystem coordinates into this opaque string form; the
// substrate treats it as an opaque content-addressed key.
type RecipeID string

// EcosystemCoordinate is the ecosystem-native identity of a recipe,
// before it's flattened to a RecipeID. Adapters produce these from
// their own parsing and decode them back as needed.
type EcosystemCoordinate struct {
    Ecosystem string            // "python", "rust", "npm", etc.
    Namespace string            // e.g. groupId for Maven, @scope for npm
    Name      string            // package name
    Version   string            // exact version string (not range)
    Platform  PlatformTuple     // resolved platform
    Features  []string          // resolved feature set
    Classifier string           // optional: Maven classifier, Scala suffix, etc.
    Extra     map[string]string // ecosystem-specific: framework tag, etc.
}

func (c EcosystemCoordinate) ToRecipeID() RecipeID { ... }
func ParseRecipeID(id RecipeID) (EcosystemCoordinate, error) { ... }
```

RecipeID encoding is stable across versions: a hash of the
canonicalized coordinate so recipe-store lookups are O(1). The
encoding scheme is a separate doc concern; for implementation, use
blake3 of the canonicalized coordinate tuple.

### 2.2 Constraint

```go
// Constraint is the universal dependency-requirement shape. Adapters
// translate ecosystem-native constraints into this form before handing
// to the resolver.
type Constraint struct {
    // Target package identity (name + optional namespace).
    Target ConstraintTarget

    // VersionRange is the constraint on version. Can be a semver range
    // ("^1.2.0", ">=2.0, <3.0"), exact pin ("1.2.3"), or Any.
    VersionRange VersionRange

    // Attributes are key-value constraints for variant-aware resolution
    // (Gradle attributes, Python wheel platform tags, NuGet framework tags).
    // Empty map means "any variant acceptable."
    Attributes map[string]string

    // Features requires specific optional features (Cargo features,
    // Python extras). Empty means "no optional features."
    Features []string

    // Capabilities declared by this constraint — used for capability-
    // conflict detection (Gradle pattern).
    Capabilities []Capability

    // Scope restricts which classpath/build-phase the constraint applies to
    // (Maven scope, Cargo dev-dependency, npm devDependencies).
    Scope ConstraintScope

    // Optional marks this as an optional dependency (npm optionalDependencies,
    // Python extras-only deps).
    Optional bool
}

type ConstraintTarget struct {
    Ecosystem string
    Namespace string // optional
    Name      string
}

type VersionRange interface {
    Satisfies(v Version) bool
    Intersect(other VersionRange) VersionRange
    IsAny() bool
    IsEmpty() bool
    String() string
}

type ConstraintScope string

const (
    ScopeCompile  ConstraintScope = "compile"
    ScopeRuntime  ConstraintScope = "runtime"
    ScopeTest     ConstraintScope = "test"
    ScopeProvided ConstraintScope = "provided"
    ScopeDev      ConstraintScope = "dev"
    ScopeBuild    ConstraintScope = "build"
)

type Capability struct {
    Group   string
    Name    string
    Version string // optional
}
```

### 2.3 Version

Versions are parsed into an ecosystem-aware form so comparison is
correct per-ecosystem semantics. SemVer is the default; ecosystems
with non-semver conventions (Python PEP 440, Go pseudo-versions,
Maven snapshot versions) implement their own `Version` type that
satisfies the substrate interface.

```go
type Version interface {
    Compare(other Version) int // -1, 0, 1
    String() string
    IsPreRelease() bool
    Ecosystem() string
}

// NewSemVer parses a strict SemVer 2.0 version.
func NewSemVer(s string) (Version, error) { ... }

// NewPEP440 parses a Python PEP 440 version (epochs, pre/post/dev releases).
func NewPEP440(s string) (Version, error) { ... }

// NewMavenVersion parses a Maven version with its quirky ordering rules
// (alpha/beta/rc handling, snapshot suffix, build qualifiers).
func NewMavenVersion(s string) (Version, error) { ... }

// NewGoPseudoVersion parses a Go module pseudo-version (v0.0.0-YYYYMMDDhhmmss-hash).
func NewGoPseudoVersion(s string) (Version, error) { ... }
```

Each ecosystem gets exactly one `Version` implementation; adapters
don't invent their own. Version comparison is the most bug-prone
part of any resolver — centralizing it is non-negotiable.

### 2.4 PlatformTuple

```go
// PlatformTuple identifies a materialization target. Adapters that
// produce platform-specific artifacts (Python wheels, Go binaries,
// native Cargo crates) use this to pick matching variants.
type PlatformTuple struct {
    OS        string // "linux", "darwin", "windows", "any"
    Arch      string // "amd64", "arm64", "x86", "wasm32", "any"
    ABI       string // "gnu", "musl", "msvc", "" (none)
    Runtime   string // "python3.11", "cpython3.11", "node18", "jvm11", ""
    Extra     map[string]string // ecosystem-specific: glibc version, etc.
}

// Compatible returns true if a recipe built for 'recipe' will run on 'host'.
// Handles fallback rules (netstandard2.0 is compatible with net8.0, py3 wheels
// compatible with cpython3.x, etc.) as registered per-ecosystem.
func (host PlatformTuple) Compatible(recipe PlatformTuple) bool { ... }
```

### 2.5 ResolveRequest / ResolveResult

```go
type ResolveRequest struct {
    RootConstraints []Constraint
    Platform        PlatformTuple
    LockfileHints   LockfileSnapshot    // existing pins to prefer
    Feeds           []FeedReference     // feeds to consult, in priority order
    Strategy        ConflictStrategy    // nearest-wins, strict, prefer, latest
    OptimizationCriteria string         // OPAM-style, passed through when solver supports
    MaxCandidatesPerSlot int            // bound solver search space
}

type ResolveResult struct {
    Closure             []ResolvedRecipe
    PinJustifications   map[RecipeID]string
    Conflicts           []ConstraintConflict
    CapabilityConflicts []CapabilityConflict
    FeedAttribution     map[RecipeID]FeedReference  // which feed served each
    ResolutionMetadata  map[string]any              // adapter-specific
}

type ResolvedRecipe struct {
    ID          RecipeID
    Coordinate  EcosystemCoordinate
    Dependencies []Constraint       // edges for downstream materialization
    IntegrityHash string            // hash the materializer must verify
    Capabilities []Capability
    FeedSource  FeedReference
}
```

### 2.6 LockfileSnapshot

```go
// LockfileSnapshot is the substrate-canonical lockfile representation.
// Adapters read ecosystem-native lockfiles (Cargo.lock, package-lock.json,
// Gemfile.lock, etc.) and produce this canonical form for the resolver to
// consume as hints.
type LockfileSnapshot struct {
    Version         int
    Ecosystem       string
    Pins            []LockfilePin
    Checksum        string   // content hash of the canonical form
    GeneratedBy     string   // "sylk 1.x" or adapter-native tool name
    GeneratedAt     time.Time
}

type LockfilePin struct {
    Coordinate    EcosystemCoordinate
    IntegrityHash string
    SourceFeed    FeedReference
    // Attributes, Features, etc. all captured at pin time.
}
```

The substrate provides the snapshot type; each adapter provides
`ReadLockfile([]byte) (LockfileSnapshot, error)` and
`WriteLockfile(LockfileSnapshot) ([]byte, error)` implementations
that round-trip the ecosystem's native format.

## 3. HTTP Transport

### 3.1 Shared HTTP client

```go
// HTTPClient is the substrate-managed transport for every outbound
// HTTP request from any adapter. Single instance per substrate init;
// adapters receive a reference, never construct their own.
type HTTPClient struct {
    // http.Client with HTTP/2 enabled (Go default since 1.6), tuned
    // transport: MaxConnsPerHost, IdleConnTimeout, TLSHandshakeTimeout.
    client *http.Client

    // Bounded concurrency per host. Prevents starving a slow registry
    // while a fast one has plenty of capacity. Default 50 (matches uv).
    perHostSemaphores map[string]*semaphore.Weighted

    // Authentication resolver. Matches requests to credentials based on
    // host + path prefix. Populated from substrate config.
    auth *AuthResolver

    // Telemetry emitter.
    telemetry *Telemetry
}

type RequestOptions struct {
    Timeout      time.Duration
    RetryPolicy  RetryPolicy
    AcceptHeader string
    // Range is an optional HTTP Range header for partial content fetches.
    // Used by the Python adapter for wheel METADATA range-requests; the
    // substrate exposes it so other adapters with similar needs can reuse.
    Range        *RangeSpec
}
```

Every adapter's network I/O goes through `HTTPClient`. This centralizes:

- HTTP/2 configuration (connection multiplexing, no per-request TCP
  handshake)
- Connection pooling (bounded per host, reused across requests)
- Retry with exponential backoff + jitter
- Authentication (per-feed credentials matched to request host)
- TLS configuration (minimum TLS 1.2, cert pinning if configured)
- Telemetry (one place to emit request spans)

### 3.2 Retry policy

```go
type RetryPolicy struct {
    MaxAttempts       int           // default 3
    InitialBackoff    time.Duration // default 250ms
    MaxBackoff        time.Duration // default 10s
    BackoffMultiplier float64       // default 2.0
    Jitter            time.Duration // default 100ms

    // RetryableStatuses defines which HTTP statuses trigger retry.
    // Default: 429, 502, 503, 504. Never retry: 4xx (except 408, 429).
    RetryableStatuses []int

    // RespectRetryAfter honors the server's Retry-After header when present.
    // Default true.
    RespectRetryAfter bool
}
```

Retry policy is per-request with sensible defaults. Adapters rarely
need to override; when they do (e.g. registry mirrors with known flaky
behavior) the override is per-adapter config.

### 3.3 AuthResolver

```go
type AuthResolver struct {
    // Configs is an ordered list of credential providers. First match wins.
    // Providers are checked in order; a provider returning nil falls through.
    configs []CredentialProvider
}

type CredentialProvider interface {
    Credentials(ctx context.Context, req *http.Request) (Credentials, bool)
}

type Credentials interface {
    Apply(req *http.Request)
}

// Implementations:
// - NetrcCredentialProvider: reads ~/.netrc
// - EnvCredentialProvider: reads ADAPTER_REGISTRY_TOKEN etc.
// - KeyringCredentialProvider: OS keychain integration
// - DockerConfigCredentialProvider: for OCI registries
// - AWSCredsCredentialProvider: for S3-backed private registries
// - AzureCredsCredentialProvider: for Azure Artifacts feeds
// - StaticCredentialProvider: explicit config-file credentials
```

Every adapter requiring auth routes through this — npm auth tokens,
Maven repository credentials, NuGet feed keys, PyPI tokens all come
through the same pipeline with the same credential-matching logic.

## 4. Metadata Layer

### 4.1 MetadataCache

```go
// MetadataCache is the substrate's indexed metadata store. Backed by
// a single SQLite database per Sylk install (or per-session in test
// mode). Shared across all adapters.
type MetadataCache struct {
    db          *sql.DB
    schemaVersion int
    // Per-adapter namespace isolation: cache keys include ecosystem prefix
    // so collisions across adapters are impossible.
}

// Get returns a cache entry if present and not expired, honoring Etag/
// Last-Modified freshness checks where supplied.
func (m *MetadataCache) Get(ctx context.Context, key CacheKey) (*CacheEntry, bool, error)

// Put stores or overwrites an entry. Entries are keyed by ecosystem +
// recipe-name + version + platform + attribute-hash so variant-aware
// lookups are exact-match.
func (m *MetadataCache) Put(ctx context.Context, key CacheKey, entry *CacheEntry) error

// Negative-cache support: "no version of X satisfies Y" is stored with
// short TTL so retries during one resolve don't re-probe the network.
func (m *MetadataCache) Negative(ctx context.Context, key CacheKey, reason string, ttl time.Duration) error

// Purge invalidates by pattern (useful for "this registry changed, invalidate everything from it").
func (m *MetadataCache) Purge(ctx context.Context, pattern CachePattern) error
```

### 4.2 Schema

SQLite schema with indexed (ecosystem, name, version, platform_hash)
composite key. Negative-cache entries share the schema with a discriminator
column. Entries expire on Etag/Last-Modified change or explicit TTL;
positive entries default to 24h TTL, negative to 5min.

```sql
CREATE TABLE metadata_entries (
    ecosystem      TEXT NOT NULL,
    name           TEXT NOT NULL,
    version        TEXT NOT NULL,
    platform_hash  TEXT NOT NULL,      -- blake3 of platform tuple
    attribute_hash TEXT NOT NULL,      -- blake3 of attribute map
    is_negative    INTEGER NOT NULL,   -- 0=positive, 1=negative
    etag           TEXT,
    last_modified  TEXT,
    fetched_at     INTEGER NOT NULL,   -- unix epoch seconds
    expires_at     INTEGER NOT NULL,
    content_hash   TEXT,               -- blake3 of the cached payload
    content        BLOB NOT NULL,      -- the metadata bytes (gzipped)
    size_bytes     INTEGER NOT NULL,
    PRIMARY KEY (ecosystem, name, version, platform_hash, attribute_hash)
);

CREATE INDEX idx_metadata_expires ON metadata_entries(expires_at);
CREATE INDEX idx_metadata_eco_name ON metadata_entries(ecosystem, name);

CREATE TABLE feed_etags (
    feed_url    TEXT PRIMARY KEY,
    resource    TEXT NOT NULL,
    etag        TEXT,
    last_modified TEXT,
    fetched_at  INTEGER NOT NULL
);
```

### 4.3 Eviction

Background eviction every 10 min: entries past `expires_at`, entries
older than 30 days, and size-based eviction when total cache exceeds
configured budget (default 10 GiB). Eviction is LRU within age-eligible
sets. Negative entries always evict before positive when size-pressured.

## 5. Resolver

### 5.1 Interface

```go
// Resolver is the base resolver contract. All adapters implement this.
type Resolver interface {
    Ecosystem() string
    Resolve(ctx context.Context, request ResolveRequest) (ResolveResult, error)
}

// FrontierAwareResolver extends Resolver with decision-frontier event
// streaming. Adapters using PubGrub or similar backtracking solvers
// implement this; adapters using MVS or non-backtracking algorithms
// don't.
//
// Frontier events are consumed by the prefetch coordinator (section
// 5.3) to fetch candidate metadata speculatively. When the solver
// backtracks, events carry cancellation signals so in-flight fetches
// for abandoned branches stop.
type FrontierAwareResolver interface {
    Resolver
    ResolveWithFrontier(ctx context.Context, request ResolveRequest, frontier chan<- FrontierEvent) (ResolveResult, error)
}

type FrontierEvent struct {
    Kind          FrontierEventKind // Considering, Decided, Backtracked
    RecipeID      RecipeID
    Coordinate    EcosystemCoordinate
    DecisionCtx   context.Context   // cancelled when this branch is abandoned
    SolverDepth   int               // for logging / debug
    Reason        string            // human-readable explanation
}
```

### 5.2 PubGrub implementation (`core/resolver/pubgrub`)

The substrate ships a Go port of PubGrub, reusable across every
PubGrub-adopting adapter. Based on Natalie Weizenbaum's original Dart
paper and the Rust `pubgrub-rs` implementation.

Core types:

```go
package pubgrub

type Solver[P PackageID, V VersionOrdering] struct {
    // Pluggable dependency-provider and incompatibility-tracker.
    provider DependencyProvider[P, V]
    // Internal state: term set, derivation history, decision stack.
}

// DependencyProvider is the interface the solver uses to discover versions
// and dependencies. Adapters supply this by implementing registry queries.
type DependencyProvider[P PackageID, V VersionOrdering] interface {
    // AvailableVersions returns the set of versions for P, ordered by
    // preference (newer usually preferred; adapter can reorder based on
    // lockfile hints).
    AvailableVersions(ctx context.Context, pkg P) ([]V, error)

    // Dependencies returns the direct dependencies of P at version V.
    Dependencies(ctx context.Context, pkg P, ver V) ([]Dependency[P, V], error)

    // IncompatibleVersions returns versions known to have conflicts
    // the solver should prune upfront (yanked versions, security-failed
    // versions when user configured to skip them).
    IncompatibleVersions(ctx context.Context, pkg P) ([]V, error)

    // Priority returns a ranking for this package — used to decide which
    // unassigned variable to pick next. Higher = picked first. The
    // default is dependency-count-based; adapters can override.
    Priority(pkg P) int
}

type Dependency[P PackageID, V VersionOrdering] struct {
    Package P
    Range   VersionRange[V]
}

// Solve runs PubGrub. Returns the resolved assignment or an explanation
// of why no solution exists.
func (s *Solver[P, V]) Solve(ctx context.Context, root P, rootRange VersionRange[V]) (*Resolution[P, V], error)

// SolveWithFrontier runs PubGrub while streaming FrontierEvent values.
// Emits Considering when a version is first added to the term set,
// Decided when it's committed to the assignment, Backtracked when
// it's evicted.
func (s *Solver[P, V]) SolveWithFrontier(ctx context.Context, root P, rootRange VersionRange[V], frontier chan<- FrontierEvent) (*Resolution[P, V], error)
```

Key correctness points:

- **Unit propagation.** When a term is decided, propagate implications
  eagerly. If `A@1.0` requires `B >= 2.0` and `B` is already assigned
  1.5, fail immediately rather than backtracking later.
- **Conflict-driven clause learning.** When backtracking from a
  conflict, record the derivation (set of assignments that together
  caused the conflict) as an incompatibility. Future search prunes any
  superset.
- **Decision heuristic.** Pick the unassigned variable with the
  fewest viable versions (most constrained variable first). Ties broken
  by `Priority` from the provider.
- **Linear-in-derivation-size explanations.** When resolution fails,
  produce a human-readable explanation by walking the derivation
  history. PubGrub's core advantage over ad-hoc backtrackers.

### 5.3 Prefetch coordinator

```go
// PrefetchCoordinator consumes FrontierEvent streams and dispatches
// metadata fetches against the adapter's registry. On Backtracked
// events, it cancels in-flight fetches for abandoned branches via
// the event's DecisionCtx.
type PrefetchCoordinator struct {
    fetcher    MetadataFetcher
    cache      *MetadataCache
    concurrency *semaphore.Weighted // bound total in-flight fetches
    telemetry  *Telemetry
}

func (p *PrefetchCoordinator) Run(ctx context.Context, events <-chan FrontierEvent) error

type MetadataFetcher func(ctx context.Context, coord EcosystemCoordinate) error
```

The prefetch coordinator is adapter-agnostic. Each adapter constructs
one with its own `MetadataFetcher` closure and connects it to its
resolver's frontier stream. The coordinator guarantees:

- Bounded in-flight fetches (default 50 concurrent)
- Per-coordinate deduplication (two simultaneous Considering events for
  the same coordinate produce one fetch)
- Cancellation propagation on Backtracked
- Fetch failures don't block the resolver — errors are cached as
  negative entries, the resolver retries or gives up based on its
  own retry policy

### 5.4 Conflict resolution strategies

```go
type ConflictStrategy int

const (
    StrategyLatest ConflictStrategy = iota // newest version wins
    StrategyStrict                         // explicit pins force; conflicts fail
    StrategyPrefer                         // declared prefs influence, don't force
    StrategyForce                          // explicit override of any conflict
    StrategyNearestWins                    // Maven-compat mode
)
```

Passed in `ResolveRequest`. Adapters may support a subset of strategies
— Maven supports all including NearestWins; Cargo ignores NearestWins
because the crates.io semantics don't match.

## 6. Materializer

### 6.1 Interface

```go
// Materializer is the substrate-managed on-disk layout builder. Adapters
// invoke it with a ResolveResult; the materializer produces a physical
// layout the ecosystem's runtime can consume (node_modules/, venv/,
// target/, Gemfile.lock-validated gem tree, etc.).
type Materializer interface {
    Materialize(ctx context.Context, req MaterializeRequest) (MaterializeResult, error)
}

type MaterializeRequest struct {
    Resolution  ResolveResult
    Destination string                // path to produce the layout at
    Layout      LayoutStrategy        // Node-style, Cargo-style, venv-style, etc.
    LinkMode    LinkMode              // Reflink, Hardlink, Copy, Symlink
    Filters     []MaterializationFilter // e.g. skip devDependencies
}

type LinkMode int
const (
    LinkReflink  LinkMode = iota // copy-on-write on btrfs/APFS/ZFS/XFS
    LinkHardlink                 // hardlink where filesystems permit
    LinkSymlink                  // for pnpm-style layouts
    LinkCopy                     // byte-copy (fallback)
)
```

### 6.2 Filesystem primitives

```go
package coreFS

// TryReflink attempts a copy-on-write reflink. Returns nil on success,
// ErrUnsupported when the filesystem doesn't support reflinks.
func TryReflink(src, dst string) error

// TryHardlink creates a hardlink. Returns nil on success,
// ErrCrossDevice when src and dst are on different filesystems.
func TryHardlink(src, dst string) error

// TrySymlink creates a symlink. Always succeeds except on Windows without
// developer mode (where symlinks require admin privs).
func TrySymlink(src, dst string) error

// FallbackCopy bytewise-copies src to dst. Used when all link modes fail.
func FallbackCopy(src, dst string) error

// MaterializeFile picks the best available strategy based on LinkMode,
// falling back if the preferred mode fails (reflink → hardlink → symlink →
// copy). Returns which mode was actually used.
func MaterializeFile(src, dst string, preferred LinkMode) (LinkMode, error)
```

### 6.3 Content-addressed recipe store

```go
// RecipeStore is the substrate's content-addressed recipe cache. Recipes
// are stored once per (name, version, platform_tuple, integrity_hash)
// globally; materialization creates reflinks/hardlinks from the store
// into project-local layouts.
type RecipeStore struct {
    root string // default $XDG_CACHE_HOME/sylk/recipes
    db   *sql.DB
}

// Fetch retrieves a recipe into the store, downloading if not already present.
// Returns the store-local path where the recipe's content lives.
func (r *RecipeStore) Fetch(ctx context.Context, coord EcosystemCoordinate, fetchURL string, expectedHash string) (string, error)

// Materialize creates links from the store-local path into dst.
// Uses the preferred LinkMode with fallback.
func (r *RecipeStore) Materialize(ctx context.Context, coord EcosystemCoordinate, dst string, mode LinkMode) error

// GC evicts entries not accessed in over N days, down to a size budget.
// Run as a background task or on explicit user command.
func (r *RecipeStore) GC(ctx context.Context, policy GCPolicy) error
```

## 7. Lockfile Framework

### 7.1 Interface

```go
// LockfileCodec is implemented per adapter to translate between the
// substrate's LockfileSnapshot and the ecosystem's native lockfile format.
type LockfileCodec interface {
    Ecosystem() string
    ReadLockfile(data []byte) (LockfileSnapshot, error)
    WriteLockfile(snapshot LockfileSnapshot) ([]byte, error)
    Filename() string // canonical filename for this ecosystem's lockfile
}
```

### 7.2 Semantics

The substrate enforces **lockfile-as-hard-preference** semantics:
during resolution, pinned versions from `LockfileHints` are honored
unless they don't satisfy the current root constraints. Versions that
satisfy constraints but aren't in the lockfile are considered only if
no lockfile pin satisfies.

This is cargo's and Go's model, stricter than npm's (which will
aggressively re-resolve). The trade-off: cargo-style produces more
stable builds; npm-style accepts more churn in exchange for
automatically picking newer compatible versions.

## 8. Multi-Feed Federation

### 8.1 FeedReference

```go
type FeedReference struct {
    Ecosystem string         // "npm", "maven", etc.
    URL       string         // feed root URL
    Name      string         // user-friendly name
    Priority  int            // lower is higher priority (1 = primary)
    Auth      AuthContext    // how to authenticate
    Features  FeedFeatures   // which capabilities this feed supports
}

type FeedFeatures struct {
    SupportsServiceIndex bool // NuGet-style capability discovery
    SupportsSparseIndex  bool // cargo-style per-version metadata
    SupportsSigning      bool // returns signed metadata (Hex, Maven)
    SupportsMirror       bool // can serve aggregate sync endpoints
}

// FeedMapping maps recipe prefixes/patterns to specific feeds, NuGet-style
// <packageSourceMapping>. Prevents typosquatting attacks where an attacker
// uploads a namespace-matching package to a public feed.
type FeedMapping struct {
    Pattern string        // e.g. "org.apache.*", "@myorg/*"
    Feed    FeedReference
}
```

### 8.2 Federated resolution

When resolving against multiple feeds:

1. For each candidate version discovery, all configured feeds are queried
   in parallel via the prefetch coordinator.
2. Responses are merged in feed-priority order — when two feeds serve the
   same `(name, version)`, the higher-priority feed wins.
3. Integrity hashes are checked even for high-priority feeds; a hash
   mismatch between feeds for the same version is surfaced as a
   `CapabilityConflict` with high severity.
4. `FeedMapping` constrains which feed can serve which recipe: a recipe
   matching a mapping pattern is only considered if served by the
   mapped feed.

## 9. Error Handling

### 9.1 Error taxonomy

```go
type ResolverError interface {
    error
    Kind() ErrorKind
    Recipe() EcosystemCoordinate
    Feed() FeedReference
    Retriable() bool
}

type ErrorKind string

const (
    ErrNetworkTransient     ErrorKind = "network_transient"       // retry
    ErrNetworkPermanent     ErrorKind = "network_permanent"       // abort
    ErrAuthenticationFailed ErrorKind = "auth_failed"             // abort
    ErrIntegrityMismatch    ErrorKind = "integrity_mismatch"      // abort, surface
    ErrSignatureFailed      ErrorKind = "signature_failed"        // abort, surface
    ErrNoSuchRecipe         ErrorKind = "no_such_recipe"          // abort
    ErrNoSatisfyingVersion  ErrorKind = "no_satisfying_version"   // abort, explain
    ErrVersionYanked        ErrorKind = "version_yanked"          // skip, try next
    ErrCapabilityConflict   ErrorKind = "capability_conflict"     // abort, explain
    ErrCycleDetected        ErrorKind = "cycle_detected"          // abort, explain
    ErrLockfileStale        ErrorKind = "lockfile_stale"          // abort
    ErrInternalBug          ErrorKind = "internal_bug"            // abort, telemetry
)
```

### 9.2 Error explanations

PubGrub-based adapters use the derivation history to produce
explanations. Non-PubGrub adapters (Arborist-like npm, OPAM's CUDF
solver) produce explanations from their own internal state. The
`ResolveResult.Conflicts` field carries the explanations; the
substrate's UI layer formats them for display.

## 10. Security

### 10.1 Integrity verification

**Every** materialized artifact must have its integrity hash verified
before being linked into a project. Hashes come from:

1. The lockfile, if one exists for this recipe
2. The feed's metadata, if the feed supplies it (Hex signed, NuGet
   registration, cargo index, Go `go.sum`)
3. A trust-on-first-use hash recorded on first fetch and stored in the
   recipe store's database

Mismatches are always fatal — never silently replace.

### 10.2 Signature verification

Feeds that support signing (Hex, Maven Central via PGP, NuGet via
Authenticode) have their signatures verified by the adapter. The
substrate provides:

```go
package coreSig

type Verifier interface {
    Verify(ctx context.Context, payload []byte, signature []byte) error
}

// Built-in verifiers: OpenPGP (for Maven .asc), Authenticode (for NuGet
// .nupkg with Microsoft code signing), Hex's custom ECDSA format.
```

### 10.3 TLS configuration

Minimum TLS 1.2. TLS 1.3 preferred. Certificate pinning for known-
stable feeds (nuget.org, crates.io, pypi.org, registry.npmjs.org) is
configurable but off by default. Per-feed override via config.

### 10.4 Supply-chain mitigations

- **Typosquatting** — `FeedMapping` prevents cross-feed recipe
  identity collisions for known namespaces.
- **Malicious mirror** — integrity hashes cached from the first
  trusted fetch; subsequent fetches from mirrors must match.
- **Vulnerability scanning** — optional feed property: the adapter
  queries the feed's vulnerability API (GitHub Advisory, OSV) and
  surfaces CVEs as `CapabilityConflict` entries with `severity: high`.
- **Package signing transparency log** — Hex has `sumdb`-style
  transparency; the substrate records observed signatures in a local
  append-only log and compares to the upstream transparency log when
  available.

## 11. Testing Strategy

### 11.1 Substrate tests

- **Unit tests** for every public primitive: PubGrub solver, version
  types (PEP 440, SemVer, Maven, Go pseudo), constraint arithmetic,
  cache CRUD, HTTP retry, link/hardlink/reflink fallback.
- **PubGrub conformance** — a corpus of canonical PubGrub test cases
  (from Dart's pub, Rust's pubgrub-rs, and manually crafted pathological
  cases) with expected outputs. The Go implementation must match.
- **Property-based tests** (`testing/quick` or `gopter`) for the
  constraint type: intersection is commutative, union includes both
  operands, etc.
- **Concurrency tests** — race detector runs for all cache, HTTP, and
  recipe-store code. Chaos tests: random cancellations of frontier
  contexts to validate prefetch cancellation.
- **Fuzz tests** for every parser (version strings, lockfile formats,
  metadata formats).

### 11.2 Integration tests

- **Real registry fetches** against recorded HTTP fixtures (using
  `httptest.Server` or VCR-style recordings). Substrate provides a
  fixture registry adapters can extend.
- **End-to-end resolves** for a canonical project per ecosystem —
  `pyproject.toml` with known deps, `Cargo.toml`, `package.json`,
  etc. Expected resolution tree is committed; changes require explicit
  approval.
- **Multi-feed tests** — resolves against two feeds serving
  overlapping recipes, verifying priority and mapping rules.

### 11.3 Performance tests

Benchmarks in `go test -bench`. Tracked per-commit; regressions >10% fail
CI. Covered:

- PubGrub solve time for 100, 1K, 10K-recipe dep graphs
- Cache hit/miss latency
- HTTP fetch throughput (mocked registry)
- Materializer throughput (reflink vs hardlink vs copy, 1K-file layout)

### 11.4 Ecosystem compatibility

Each adapter ships with a "golden" test corpus of real projects
from its ecosystem (top 20 GitHub repos or similar). The adapter
must produce identical resolution results to the native tool on
that corpus. Ecosystem-compat is a ship gate for M3.

## 12. Performance Targets

The substrate aims to be negligible overhead vs the native tool at
each ecosystem's hot path:

| Operation | Target |
|---|---|
| PubGrub solve, 100-pkg graph, warm cache | <10ms |
| PubGrub solve, 1K-pkg graph, warm cache | <100ms |
| PubGrub solve, 10K-pkg graph, warm cache | <2s |
| Metadata cache lookup (hit) | <1ms |
| Metadata cache lookup (miss → fetch → parse) | <100ms |
| Reflink materialization, 1K files | <50ms |
| Hardlink materialization, 1K files | <100ms |
| HTTP/2 stream open (warm conn) | <5ms |

Memory: resolver peak <500MB for 10K-pkg graphs. Cache: configurable
budget; default 10GiB.

## 13. Phases / Milestones

**M0 — Scaffold.** Types defined, interfaces compile, trivial unit
tests pass. No working resolver yet.

**M1 — PubGrub core.** Go PubGrub ships, passes the canonical test
corpus. Constraint type, version types, cache, HTTP client all
minimally functional. No frontier yet.

**M2 — Frontier integration.** `FrontierAwareResolver` extension
ships. Prefetch coordinator works. First adapter (Python) wired
through end-to-end against a live registry.

**M3 — Multi-feed, materializer, lockfile.** Full substrate with
all primitives. Python + Rust + Go adapters functional against
live registries. Performance targets met.

**M4 — Production hardening.** Error handling, security, telemetry,
ecosystem-compat tests green. Ready for adapter proliferation.

## 14. Open Questions

- **Blake3 vs SHA-256 for integrity hashing.** Blake3 is ~10× faster;
  SHA-256 is what most ecosystems use natively. Proposal: blake3 for
  substrate-internal caching, SHA-256 at the adapter↔ecosystem
  boundary. Benchmark and decide before M1.
- **SQLite WAL mode on Windows.** WAL mode is default on Linux/macOS
  for concurrent readers; Windows has subtle issues with cross-process
  WAL. May need to fall back to rollback-journal on Windows.
- **Recipe store eviction policy.** LRU vs LFU vs "never evict
  lockfile-pinned" vs hybrid. Needs empirical data from real usage.
- **Frontier event buffering.** Channel capacity for frontier events
  — small (backpressure the solver on slow prefetch) vs large (let
  solver race ahead, more speculative fetches). Current plan: 256
  event buffer; adjust after profiling.
- **Go `encoding/json` vs streaming.** For registries with mega-JSON
  responses (npm). Streaming is mandatory for npm but may be overkill
  for small-response ecosystems. Decide per-adapter.

## 15. Dependencies

Substrate has no dependencies on adapters. Every adapter depends on
every substrate primitive. Adapter milestones cannot reach M2 until
substrate M2 is complete; cannot reach M3 until substrate M3 is
complete.

External Go dependencies:

- `golang.org/x/sync/semaphore` — bounded concurrency
- `github.com/cespare/xxhash/v2` — fast hashing where crypto isn't
  needed
- `lukechampine.com/blake3` — blake3 hashing
- `modernc.org/sqlite` — pure-Go SQLite (avoid cgo)
- `github.com/valyala/fastjson` — streaming JSON parsing (used by npm
  adapter via substrate-exposed helpers)
- `golang.org/x/crypto/openpgp` — signature verification
