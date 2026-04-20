# ELIXIR_HEX.md — Elixir / Erlang / Hex Adapter Implementation Plan

Tier 4 finish. Validates the substrate handles **signed protobuf
metadata** (Hex's defense-in-depth integrity model), **aggregate
sync endpoints distinct from per-package endpoints** (Hex's
clean separation), and **cross-language ecosystems**
(Mix/Elixir + Rebar3/Erlang from the same registry).

## 1. Overview

The Hex adapter resolves and materializes packages from:

- **[hex.pm](https://hex.pm/)** (default; serves both Elixir and
  Erlang packages)
- **Private Hex repositories** (self-hosted Hex servers,
  organization-scoped feeds via `mix hex.organization`)
- **Git dependencies** (`{:foo, git: "https://github.com/..."}`)
- **Path dependencies** (`{:foo, path: "../foo"}`)
- **Umbrella project siblings** (Elixir's monorepo pattern)

Produces:

- A resolved dependency graph using PubGrub (matches Hex 2.0+
  semantics)
- An updated `mix.lock` (Elixir) or `rebar.lock` (Erlang)
  byte-identical to native tool output
- A materialized package layout compatible with Mix's
  `deps/` directory or Rebar3's `_build/`

User-visible behaviors (M3 target):

- `sylk resolve hex ./mix.exs` → mix.lock
- `sylk resolve hex ./rebar.config` → rebar.lock
- `sylk install hex` → deps materialized
- `sylk add hex <pkg>[@<range>]` → modifies mix.exs/rebar.config
- `sylk why hex <pkg>` → PubGrub explanation

Non-goals:

- Running Mix tasks (compile, test, etc.)
- Building Erlang/Elixir releases
- Hex.pm publishing (`mix hex.publish`)

## 2. Data Model

### 2.1 Coordinates

```go
type HexCoordinate struct {
    Repo    string         // "hexpm" (default), or named private repo
    Name    string         // package name; lowercase, underscore-separated
    Version SemVer         // strict SemVer 2.0
}

// Package names are namespaced by repo. The same name "foo" in two
// different repos is two different packages. The substrate's
// FeedMapping prevents typo-squatting across repos.
```

### 2.2 mix.exs

Elixir build files are Elixir code (Turing-complete). Parse the
declarative subset:

```go
type MixExs struct {
    Project  MixProject
    Deps     []MixDep
    Aliases  map[string][]string  // not relevant for resolution
    Releases []MixRelease         // not relevant for resolution
}

type MixProject struct {
    App           string
    Version       string
    ElixirVersion string  // "~> 1.16"
    Deps          func() []MixDep  // typically a function call
}

type MixDep struct {
    Name        string
    Version     string         // "~> 1.0"
    Source      *MixDepSource
    Optional    bool
    Only        []string       // [:dev, :test] — environment restriction
    Targets     []string       // [:host, :target] — Nerves-style multi-target
    Override    bool           // override transitive's version
    Manager     string         // "mix" (default), "rebar3", "make"
    Repo        string         // for private feeds
    OrganizationSrc string     // for hex organizations
    SystemEnv   map[string]string  // env vars for compilation
}

type MixDepSource struct {
    Kind   string  // "hex", "git", "path"
    URL    string
    Branch, Tag, Ref string
    Subdir string
}
```

### 2.3 rebar.config

Erlang build files are Erlang terms (more declarative than Elixir
DSL but still Turing-complete via `erl_eval`). Parse the
declarative subset:

```go
type RebarConfig struct {
    AppDir       string
    Deps         []RebarDep
    Profiles     map[string][]RebarDep  // {profiles, [{prod, [...]}, {test, [...]}]}.
    PluginDeps   []RebarDep
    Repos        []RebarRepo
}

type RebarDep struct {
    Name    string
    Version string  // "1.5.0" or {git, "...", {branch, "main"}}
    Source  *RebarDepSource
}
```

### 2.4 mix.lock and rebar.lock

`mix.lock`:

```elixir
%{
  "phoenix": {:hex, :phoenix, "1.7.10", "02189140a61b2ce85bb633a9b6fd02aff2c4ce3b50584c6e6ecff0c6e8803e9", [:mix], [{:phoenix_pubsub, "~> 2.0", [hex: :phoenix_pubsub, repo: "hexpm", optional: false]}, ...], "hexpm", "f6651f7a04feaa15c1c0d4c7afaca0d4f2c7e0b6dba89bd7..."},
  "phoenix_pubsub": {:hex, :phoenix_pubsub, "2.1.3", "...", [:mix], [], "hexpm", "..."},
}
```

A single Elixir map literal where keys are package names and values
are 7-tuples carrying source kind, name, version, hash (inner),
build managers, deps, repo, hash (outer).

`rebar.lock`:

```erlang
{"1.2.0",
[{<<"cowboy">>,{pkg,<<"cowboy">>,<<"2.10.0">>},0},
 {<<"cowlib">>,{pkg,<<"cowlib">>,<<"2.12.1">>},1},
 {<<"ranch">>,{pkg,<<"ranch">>,<<"2.1.0">>},1}]}.
{pkg_hash,[
 {<<"cowboy">>, <<"...">>},
 ...]}.
```

Two Erlang term files. Versioned format (lockfile version `1.2.0` or
`1.2.1`).

The adapter must read and write both formats byte-identically.

## 3. HTTP Transport

### 3.1 Hex registry endpoints

```
# Aggregate sync endpoints (used for mirror infra, not resolution):
GET https://repo.hex.pm/names         # all package names
GET https://repo.hex.pm/versions      # all (name, versions, retirement)

# Per-package metadata:
GET https://repo.hex.pm/packages/{pkg}

# Tarballs:
GET https://repo.hex.pm/tarballs/{pkg}-{version}.tar
```

All responses are **signed protobuf**. Each response is wrapped in
a signature envelope:

```protobuf
message Signed {
  bytes payload = 1;        // serialized inner message
  bytes signature = 2;      // signature over payload
}
```

The adapter:

1. Fetches the response
2. Verifies the signature against the repo's public key
3. Parses the inner protobuf payload

### 3.2 Public key verification

Each repo declares its public key. For hex.pm, the key is bundled
with Hex itself (and pinned in our adapter). For private repos,
the key is fetched at repo registration time and stored in the
substrate's trust store.

Signature algorithm: RSA-PSS-SHA512 (per Hex's spec). Verification
is per-response — every response must be signed.

### 3.3 Protobuf parsing

Schema definitions from
[hexpm/specifications](https://github.com/hexpm/specifications/tree/main/registry/v2):

```protobuf
message Names {
  repeated string packages = 1;
  string repository = 2;
}

message Versions {
  message Package {
    string name = 1;
    repeated string versions = 2;
    repeated string retired = 3;  // versions marked retired
  }
  repeated Package packages = 1;
  string repository = 2;
}

message Package {
  message Release {
    string version = 1;
    bytes inner_checksum = 2;       // hash of unpacked tarball contents
    bytes outer_checksum = 3;       // hash of the .tar file itself
    repeated Dependency dependencies = 4;
    repeated string build_tools = 5;  // ["mix"], ["rebar3"], etc.
    optional RetirementStatus retirement = 6;
  }
  message Dependency {
    string package = 1;
    string requirement = 2;
    bool optional = 3;
    string app = 4;       // OTP app name, may differ from package name
    string repository = 5;
  }
  message RetirementStatus {
    enum Reason {
      OTHER = 0;
      INVALID = 1;
      SECURITY = 2;
      DEPRECATED = 3;
      RENAMED = 4;
    }
    Reason reason = 1;
    string message = 2;
  }
  string name = 1;
  string repository = 2;
  repeated Release releases = 3;
}
```

Use `google.golang.org/protobuf` for codegen. Schema is small and
stable.

### 3.4 Authentication

`mix hex.organization auth` configures organization tokens:

```
~/.hex/hex.config → Erlang term file with auth tokens per repo.
```

```erlang
[
  {"hexpm:my_org", [
    {auth_key, <<"...">>},
    {api_url, <<"https://hex.pm/api/repos/my_org">>},
    {url, <<"https://repo.hex.pm/repos/my_org">>}
  ]},
  ...
].
```

The adapter parses this Erlang term file (small parser; Erlang
terms are simpler than full Erlang) and routes credentials to the
substrate AuthResolver.

## 4. Metadata Layer

### 4.1 Versions sync

Aggregate `/versions` endpoint provides every package's version
list in a single signed response (~5 MB compressed for full
hex.pm). The adapter:

1. On first use: fetch `/versions`, parse, populate cache
2. On subsequent use: conditional GET with Etag; sync incrementally
   when registry has updates

The compact representation (one Package message per package, with
all versions in a flat list) makes incremental sync efficient even
when many packages are updated.

### 4.2 Per-package fetch

For each package the resolver considers:

```
GET /packages/{pkg}
```

Returns a signed Package message with all versions + their
dependencies + checksums. Cache by package name; invalidate when
`/versions` shows a new version for the package.

### 4.3 Cache keys

```
(ecosystem="hex", name="hexpm:phoenix", version=*, platform_hash="")
```

Repo + name composite identity. Platform is empty (Hex packages
are platform-independent).

## 5. Resolver

### 5.1 PubGrub via shared substrate impl

```go
type hexDepProvider struct {
    fetcher       *hexFetcher
    project       *MixExs  // or *RebarConfig
    elixirVer     string
    targets       []string  // for multi-target Nerves projects
    cache         *substrate.MetadataCache
}

func (p *hexDepProvider) AvailableVersions(ctx context.Context, pkg HexCoordinate) ([]SemVer, error) {
    // 1. Fetch /packages/{pkg}.
    // 2. Filter by retirement status (skip retired by default).
    // 3. Filter by Elixir/OTP version compatibility (declared in package's
    //    application key, fetched from inner_metadata when needed).
    // 4. Order: newest first, lockfile pin priority.
}

func (p *hexDepProvider) Dependencies(ctx context.Context, pkg HexCoordinate, ver SemVer) ([]pubgrub.Dependency, error) {
    // 1. Look up the release in the cached Package message.
    // 2. Translate Hex Dependency entries to pubgrub.Dependency.
    // 3. Apply optional dependency semantics (skip optional unless
    //    transitively required).
    // 4. Apply Mix's only:[:dev, :test] filter (don't include test deps in
    //    production resolution).
}
```

### 5.2 Override directive

Mix's `override: true` is a Mix-specific directive forcing a
transitive's version:

```elixir
{:phoenix_pubsub, "~> 2.1", override: true}
```

If `phoenix_pubsub` appears as a transitive of `phoenix` with a
different version, the consuming project's `override: true` wins.

Implemented as a high-priority constraint in the PubGrub provider —
the override version becomes the only candidate during resolution.

### 5.3 Optional dependencies

Hex's `optional: true` deps are not included unless something else
in the graph also requires them (in which case the resolver picks a
mutually-satisfying version). Substrate's `Constraint.Optional` flag
models this directly.

### 5.4 Build tool routing

Each Hex package declares its build tools (`mix`, `rebar3`, `make`).
The adapter doesn't *build* but the lockfile records build tools
so the consumer's tool can invoke the right builder during compile.

### 5.5 Frontier

PubGrub-based; implements `FrontierAwareResolver`. Frontier events
trigger `/packages/{pkg}` fetches via prefetch coordinator.

## 6. Materializer

### 6.1 deps/ layout (Mix)

Mix's canonical layout per project:

```
deps/
  phoenix/
    .hex                  # Hex metadata sidecar
    .fetch                # fetch timestamp
    mix.exs
    lib/
    ...
  phoenix_pubsub/
    ...
```

Each dep is a flat directory under deps/. Hardlinks from the
substrate's recipe store make this nearly free.

### 6.2 _build/lib/ layout (Rebar3)

Rebar3 uses a different layout:

```
_build/
  default/
    lib/
      cowboy/
        ebin/    # compiled .beam (out of scope; rebar3 builds)
        src/
        ...
      cowlib/
        ...
```

The adapter creates the source-tree layout; Rebar3 itself runs
the compiler.

### 6.3 Tarball extraction

Hex tarballs (`*.tar`) have a specific format:

```
phoenix-1.7.10.tar:
  VERSION                  # protocol version
  CHECKSUM                 # SHA-256 of metadata.config + contents.tar.gz
  metadata.config          # Erlang term file with package metadata
  contents.tar.gz          # gzipped tar of the actual sources
```

```go
func extractHexTarball(tarPath, dst string) error {
    // 1. Open as POSIX tar.
    // 2. Read VERSION; reject if unsupported (currently 3).
    // 3. Read CHECKSUM; verify against metadata.config + contents.tar.gz hash.
    //    This is the OUTER checksum from the registry's Package message.
    // 4. Extract metadata.config; parse Erlang terms for sanity check.
    // 5. Extract contents.tar.gz; gunzip + untar to dst.
    // 6. Compute INNER checksum (hash of unpacked contents); verify.
}
```

Both inner and outer checksums must be verified (hex protocol
defense-in-depth).

## 7. Lockfile

### 7.1 mix.lock codec

```go
type hexMixLockCodec struct{}

func (c *hexMixLockCodec) Ecosystem() string { return "hex-mix" }
func (c *hexMixLockCodec) Filename() string  { return "mix.lock" }

func (c *hexMixLockCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) {
    // Parse Elixir map literal. Use a small Elixir term parser
    // (subset: maps, atoms, strings, tuples, lists, integers).
    // Each entry: {atom_key, {source_kind, name, version, inner_hash, build_tools, deps, repo, outer_hash}}.
}

func (c *hexMixLockCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit:
    //   %{
    //     "package_a": {:hex, :package_a, "1.0.0", "<inner>", [:mix], [...deps...], "hexpm", "<outer>"},
    //     ...
    //   }
    // Sort entries alphabetically by package name. Format dependencies as Elixir-style
    // keyword lists: [{:other_pkg, "~> 1.0", [hex: :other_pkg, repo: "hexpm", optional: false]}, ...].
    // Indent with 2 spaces. Match Mix's formatter exactly.
}
```

### 7.2 rebar.lock codec

```go
type hexRebarLockCodec struct{}

func (c *hexRebarLockCodec) Filename() string { return "rebar.lock" }

func (c *hexRebarLockCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit two Erlang term file entries:
    //   {"<lockfile-version>", [{<<"name">>, {pkg, <<"name">>, <<"version">>}, depth}, ...]}.
    //   {pkg_hash, [{<<"name">>, <<"hash">>}, ...]}.
    // Lockfile version 1.2.0 or 1.2.1 depending on Rebar3 capability.
}
```

Byte-identical output to `mix deps.get` / `rebar3 lock` for both
formats.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Direct use |
| `core/substrate/http` | All Hex registry fetches |
| `core/substrate/cache/metadata` | Per-package + /versions caching |
| `core/substrate/store/recipe` | Shared tarball + extracted source storage |
| `core/substrate/materializer` | Hardlink to deps/ or _build/lib/ |
| `core/substrate/lockfile` | mix.lock + rebar.lock codecs |
| `core/substrate/feeds` | Multi-repo (hex.pm + organization repos + private) |
| `core/substrate/auth` | hex.config Erlang term parsing for tokens |
| `core/substrate/sigverify` | RSA-PSS signature verification on every response |
| `core/substrate/frontier` | Standard PubGrub frontier |
| `core/substrate/git` | Git dep cloning |

Adapter modules under `adapters/hex/`:

- `coordinate.go` — `HexCoordinate`
- `mix_exs.go` — Elixir DSL declarative parser
- `rebar_config.go` — Erlang term declarative parser
- `protobuf.go` — Hex registry protobuf schema (codegen via protoc)
- `signature.go` — RSA-PSS signature verification
- `versions_sync.go` — /versions incremental sync
- `provider.go` — PubGrub DependencyProvider
- `tarball.go` — Hex tar format extractor with inner/outer checksum
  verification
- `materializer.go` — deps/ + _build/lib/ layouts
- `mix_lockfile.go` — mix.lock byte-identical codec
- `rebar_lockfile.go` — rebar.lock byte-identical codec
- `hex_config.go` — ~/.hex/hex.config parser
- `erlang_terms.go` — small Erlang term parser/printer (shared)
- `adapter.go` — top-level Resolver

Estimated LOC: ~5,000. Complexity drivers:

- Elixir DSL parser (~1500 LOC; declarative subset)
- Erlang term parser/printer (~800 LOC)
- Protobuf integration (~200 LOC + generated code)
- Signature verification (~200 LOC)
- Lockfile codecs for both formats (~1200 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Signature verification fails | `ErrSignatureFailed` | **Always fatal** — possible MITM or compromised registry |
| Inner / outer checksum mismatch | `ErrIntegrityMismatch` | Fatal |
| Package retired (security) | `ErrNoSatisfyingVersion` | Surface retirement reason; allow opt-in via flag |
| Required Elixir version unmet | `ErrNoSatisfyingVersion` | "X requires Elixir ~> 1.16 but project declares ~> 1.14" |
| Org auth token missing | Auth error | Suggest `mix hex.organization auth ORGNAME` |
| Mix.exs has non-declarative deps function | User error | Suggest --mix-shell-eval fallback |

## 10. Security

### 10.1 Signed metadata at protocol layer

Every Hex registry response is verified via RSA-PSS. The public
key is pinned per repo. This is the **defense-in-depth** model
that distinguishes Hex from JSON-protocol ecosystems — tampering
by intermediaries is detected immediately, before parsing.

### 10.2 Inner + outer checksums

Tarballs verified twice:

- **Outer**: SHA-256 of the .tar file contents (metadata.config +
  contents.tar.gz)
- **Inner**: SHA-256 of the unpacked source tree

Both must match the registry's recorded values. Catches both
tarball tampering and source-tree tampering after a successful
unpack.

### 10.3 Retirement

Hex maintainers can mark versions as retired with a reason
(SECURITY, DEPRECATED, RENAMED, INVALID). The adapter respects
retirement by default; opt-in via `--allow-retired` for users
needing to use a retired version.

### 10.4 Organization scoping

Private organization repos require auth; the adapter ensures
tokens never leak to other repos. FeedMapping for org-prefixed
package names prevents typosquatting (e.g.,
`hexpm:my_org/...` only from the org's repo).

## 11. Testing

### 11.1 Unit tests

- SemVer parser (Hex uses strict SemVer 2.0)
- Mix.exs + rebar.config declarative parsing
- Erlang term parser/printer (round-trip test corpus)
- Hex protobuf schema integration (golden message fixtures)
- Signature verification (positive + negative test cases)
- Tarball extraction with both checksum verifications

### 11.2 Integration tests

- Resolve a Phoenix project (Elixir; ~50 deps)
- Resolve a Cowboy project (Erlang; smaller dep tree)
- Resolve with mix override forcing transitive version
- Resolve with optional deps
- Resolve from a private organization repo
- Tarball extraction with both checksums verified

### 11.3 Ecosystem compat

50 Hex projects (mix of Mix and Rebar3). Compare lockfile output
to `mix deps.get` and `rebar3 lock` respectively. Byte-identical
match required.

### 11.4 Performance

- Resolve typical Phoenix app: <2s cold, <300ms warm
- Resolve large Elixir umbrella: <5s cold
- Hex /versions sync (full): <5s
- Per-package fetch: <50ms
- Signature verification: <1ms

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, typical Phoenix app | <2s | <1s |
| Cold resolve, umbrella project | <5s | <3s |
| Warm resolve | <300ms | <150ms |
| /packages/{pkg} fetch | <50ms | <25ms |
| /versions full fetch | <5s | <3s |
| /versions incremental sync | <500ms | <200ms |
| Signature verification (per response) | <1ms | <500μs |
| Tarball extraction with both checksums | <50ms | <25ms |
| Materialization, 50 deps (hardlink) | <1s | <500ms |
| Lockfile byte-identical write | <50ms | <25ms |

## 13. Phases

**M0.** Types, parsers, protobuf schema integration; unit tests.

**M1.** Hex registry client with signature verification. Tarball
extraction with checksums.

**M2.** PubGrub end-to-end. Frontier prefetch. Mix.exs + rebar.config
parsing.

**M3.** Materializer with deps/ + _build/lib/ layouts. Both lockfile
codecs byte-identical. 50 ecosystem-compat projects green.

**M4.** Organization repo auth. Private hex server support.
Production polish.

## 14. Open Questions

- **Multi-target (Nerves) resolution.** Nerves projects target
  multiple hardware platforms (rpi3, bbb, etc.). Resolution is
  per-target. Mix's targets handling is well-defined; we honor
  it.
- **Rebar3 vs Mix in same project.** Mix can declare deps with
  `:manager => :rebar3`. We resolve them via Hex registry but
  the lockfile entry indicates the build tool. The materializer
  doesn't build; the user's tool (Mix or Rebar3) drives compile.
- **Umbrella projects.** Elixir umbrella convention has multiple
  apps under `apps/`. Each has its own mix.exs; the umbrella's
  mix.exs aggregates. Resolve with the umbrella as root, treat
  each app's deps as path deps on its sibling.
- **Hex.pm rate limits.** Public hex.pm has rate limits; respect
  them via substrate's HTTP retry policy with backoff.

## 15. Dependencies

- Substrate M2 (frontier, multi-feed) → adapter M2
- Substrate M3 (materializer, lockfile, signature verifier) → adapter M3

External Go dependencies:

- `google.golang.org/protobuf` — Hex registry protobuf
- Custom Elixir DSL parser (~1500 LOC; declarative subset)
- Custom Erlang term parser/printer (~800 LOC)
- `crypto/rsa` — stdlib for RSA-PSS verification
- `crypto/sha256`, `crypto/sha512` — stdlib

No dependency on other adapters.
