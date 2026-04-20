# LUA_LUAROCKS.md — Lua / LuaRocks Adapter Implementation Plan

Tier 5 — smallest mainstream package-manager ecosystem. Validates
the substrate's **named-environment primitive** works at small
scale (LuaRocks trees = simpler OPAM switches), and that the
substrate can handle **Lua-script-as-metadata** (rockspec format is
Lua code evaluated to produce a metadata table).

## 1. Overview

The Lua adapter resolves and materializes rocks (Lua packages)
from:

- **[luarocks.org](https://luarocks.org/)** (default public rocks
  repository)
- **Private rock repositories** (luarocks --server URLs)
- **Git dependencies** (scm rocks: `scm-1` versions)
- **Path dependencies** (local rockspec files)

Produces:

- A resolved rock set compatible with the target Lua version
- A materialized rocks tree (LuaRocks' `rocks_tree` concept)
- Sylk-canonical lockfile (LuaRocks has limited native lockfile
  support)

User-visible behaviors (M3 target):

- `sylk resolve lua ./my-rock.rockspec` → resolved set
- `sylk install lua` → rocks into tree
- `sylk add lua <rock>` → updates rockspec, re-resolves

Non-goals:

- Running Lua itself
- Compiling native modules (handled by substrate subprocess +
  LuaRocks build commands)
- LuaRocks plugins

## 2. Data Model

### 2.1 Coordinates

```go
type LuaRockCoordinate struct {
    Name        string        // "luasocket", "luasec"
    Version     LuaRockVersion
    LuaVersion  string        // "5.1", "5.2", "5.3", "5.4", "luajit"
}

// LuaRocks versions are "modversion-rockrev" format.
// Examples: "3.0-1" (mod 3.0, rock revision 1), "0.9.3-2", "scm-1" for
// latest-from-source.
type LuaRockVersion struct {
    ModVersion  string   // "3.0", "0.9.3", "scm"
    RockRev     int      // -1 revision
    Raw         string
    IsSCM       bool     // true for scm-X (source-control-managed, latest)
}

func (v LuaRockVersion) Compare(other LuaRockVersion) int {
    // scm versions are "latest" — sort highest.
    // Otherwise compare modversion lexically, then rockrev numerically.
}
```

### 2.2 Rockspec

Rockspecs are Lua files evaluated to produce a metadata table:

```lua
package = "luasocket"
version = "3.0-1"
source = {
   url = "https://github.com/lunarmodules/luasocket/archive/v3.0.0.tar.gz",
   dir = "luasocket-3.0.0"
}
description = {
   summary = "Network support for Lua",
   detailed = [[...]],
   homepage = "...",
   license = "MIT"
}
dependencies = {
   "lua >= 5.1",
}
build = {
   type = "builtin",
   modules = {
      ["socket.core"] = {
         sources = {"src/luasocket.c", "src/timeout.c", ...},
         defines = {"LUASOCKET_DEBUG"},
      },
      -- etc.
   },
   platforms = {
      unix = {
         modules = { ... }
      },
      windows = {
         modules = { ... }
      }
   }
}
```

The adapter evaluates rockspec as Lua — NOT statically parses it.
Rockspecs can include conditionals, platform-specific build
configurations, and computed dependencies. Full Lua evaluation is
required.

```go
type Rockspec struct {
    Package      string
    Version      LuaRockVersion
    Source       RockSource
    Description  RockDescription
    Dependencies []RockDep
    BuildDependencies []RockDep
    TestDependencies []RockDep
    Supported    []string     // supported_platforms
    Build        RockBuild
}

type RockDep struct {
    Name         string          // "lua", "luasocket"
    VersionRange VersionRange
    Raw          string          // original constraint string
}
```

### 2.3 Rocks trees

LuaRocks supports multiple "trees" per user:

```
~/.luarocks/          # user tree (default)
/usr/local/           # system tree
./lua_modules/        # project-local tree
```

Each tree has:

```
{tree}/
  lib/luarocks/rocks-{lua-version}/
    {rock-name}/
      {version}/
        rock_manifest       # list of installed files
        ...
  lib/lua/{lua-version}/    # compiled modules
    socket.so
    socket/
      core.so
  share/lua/{lua-version}/  # pure-Lua modules
    socket.lua
    socket/
      http.lua
```

Multiple trees can coexist; LuaRocks searches them in a
configurable order.

## 3. HTTP Transport

### 3.1 LuaRocks repository protocol

```
# Manifest listing all rocks:
GET https://luarocks.org/manifest
GET https://luarocks.org/manifest-5.1   # per-Lua-version manifest
GET https://luarocks.org/manifest-5.4
```

Manifest is Lua source serializing a big table:

```lua
commands = {
  ...
}
modules = {
  ["socket.core"] = {
    "luasocket/3.0-1"
  },
  ...
}
repository = {
  luasocket = {
    ["3.0-1"] = {
      {
        arch = "rockspec"
      },
      {
        arch = "src"
      },
      {
        arch = "linux-x86_64"  -- binary rock for this platform
      }
    }
  }
}
```

The adapter evaluates this manifest with a minimal Lua interpreter
to extract the module/rock/version graph.

### 3.2 Per-rock fetch

```
GET https://luarocks.org/{rock}-{version}.rockspec
GET https://luarocks.org/{rock}-{version}-src.rock        # source archive
GET https://luarocks.org/{rock}-{version}-{platform}.rock # binary for platform
```

Rockspec is Lua source. `.rock` files are tar+zip archives (LuaRocks'
custom format).

### 3.3 Authentication

LuaRocks public repo has no auth. Private servers can use HTTP
Basic auth or per-URL tokens. Handled through substrate AuthResolver.

## 4. Metadata Layer

### 4.1 Rockspec evaluation

Embed a Lua interpreter (`github.com/yuin/gopher-lua`). Evaluate
the rockspec in a sandboxed Lua context — only expose safe globals
(no `os.execute`, no `io` writing, no `require` for non-standard
modules):

```go
func EvalRockspec(src string) (*Rockspec, error) {
    L := lua.NewState(lua.Options{
        CallStackSize:       120,
        RegistrySize:        256,
        SkipOpenLibs:        true,
    })
    defer L.Close()

    // Expose only safe globals: string, math, table operations.
    lua.OpenString(L)
    lua.OpenMath(L)
    lua.OpenTable(L)

    // Evaluate the rockspec source.
    if err := L.DoString(src); err != nil {
        return nil, err
    }

    // Extract the top-level variables into a Rockspec struct.
    return extractRockspec(L), nil
}
```

gopher-lua is pure Go, no cgo; sandboxed evaluation is safe.

### 4.2 Manifest evaluation

Same approach — evaluate the manifest Lua and extract the tables.
The manifest is large for full LuaRocks (~5 MB) but parses quickly.

### 4.3 Version range parsing

LuaRocks constraints:

- `>= 5.1` — minimum
- `>= 5.1, < 5.4` — explicit range
- `~> 5.1` — pessimistic, >= 5.1 and < 6.0
- `== 5.1` — exact
- `!= 5.1` — exclusion

~150 LOC parser.

### 4.4 Cache keys

```
(ecosystem="lua", name=<rock>, version=<ver>, platform_hash=<hash of (lua_version, os, arch)>)
```

## 5. Resolver

### 5.1 Simple backtracking solver

LuaRocks' native resolver is simple (newest-compatible-version with
basic conflict resolution). The ecosystem is small enough that
PubGrub's power is overkill. But PubGrub is already available from
the substrate, so use it for consistency and for conflict
explanations.

```go
type luaDepProvider struct {
    fetcher     *luaRocksFetcher
    project     *Rockspec
    luaVersion  string
    platform    substrate.PlatformTuple
    cache       *substrate.MetadataCache
}

func (p *luaDepProvider) AvailableVersions(ctx context.Context, pkg LuaRockCoordinate) ([]LuaRockVersion, error) {
    // 1. Fetch manifest if not cached.
    // 2. Filter by Lua version compatibility.
    // 3. Filter by platform (binary rocks) or mark as "any" (src rocks).
    // 4. Order: newest first.
}

func (p *luaDepProvider) Dependencies(ctx context.Context, pkg LuaRockCoordinate, ver LuaRockVersion) ([]pubgrub.Dependency, error) {
    // 1. Fetch rockspec.
    // 2. Evaluate Lua to extract dependencies table.
    // 3. Translate to pubgrub.Dependency.
}
```

### 5.2 Virtual packages

LuaRocks supports **module-based resolution** — a rock may
"provide" a module name that other rocks depend on. Not the same
as the rock's package name.

Example: `luasocket` provides modules `socket` and `socket.http`.
A dependency on `socket` could be satisfied by any rock providing
it.

Substrate's `Capability` concept maps directly. Each rock's
`build.modules` table declares the modules it provides; dependencies
naming modules (not rocks) are resolved to rocks providing those
modules.

### 5.3 Frontier

PubGrub-based. Implements `FrontierAwareResolver`. Standard
pattern.

## 6. Materializer

### 6.1 Tree layout

Produce LuaRocks-compatible tree:

```
{tree}/
  lib/luarocks/rocks-{lua-version}/
    {rock}/
      {version}/
        rock_manifest
        {rock}-{version}-rockspec
  lib/lua/{lua-version}/
    <compiled modules>
  share/lua/{lua-version}/
    <pure Lua modules>
  bin/
    <console scripts>
```

### 6.2 .rock extraction

.rock files are tar+gzip. Extract per the archive's internal
layout (rockspec tells us which modules go where).

### 6.3 Native compilation

Rockspecs with `build.type = "builtin"` or `build.type = "cmake"`
require running a compiler. The adapter:

- Fetches source (from `source.url`)
- Extracts to build dir
- Invokes build commands from the rockspec (via substrate subprocess)
- Copies produced artifacts into the tree per rockspec's
  `build.modules` mapping

Uses the user's configured C compiler (or LuaRocks' own config).
Failures surface stderr; opt-in `--no-native` to skip native rocks.

## 7. Lockfile

LuaRocks has a `luarocks.lock` project-level format (since 3.9) but
it's limited. Sylk emits:

- `sylk.lock` (canonical substrate format)
- `luarocks.lock` (optionally, LuaRocks-compatible export)

```go
type luaLockfileCodec struct{}
func (c *luaLockfileCodec) Ecosystem() string { return "lua" }
func (c *luaLockfileCodec) Filename() string  { return "luarocks.lock" }
```

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Direct use |
| `core/substrate/http` | Manifest + rockspec + .rock fetches |
| `core/substrate/cache/metadata` | Manifest + rockspec caching |
| `core/substrate/store/recipe` | Shared rock storage + extracted source |
| `core/substrate/materializer` | Tree layouts |
| `core/substrate/lockfile` | luarocks.lock codec |
| `core/substrate/feeds` | Multi-rock-server chain |
| `core/substrate/env` | Named-environment primitive for trees |
| `core/substrate/subprocess` | Native rock building |
| `core/substrate/frontier` | Standard PubGrub frontier |

Adapter modules under `adapters/lua/`:

- `coordinate.go` — `LuaRockCoordinate`
- `version.go` — `LuaRockVersion`
- `rockspec_eval.go` — sandboxed Lua evaluator for rockspecs
- `manifest.go` — manifest Lua evaluator
- `ranges.go` — version range parser
- `provider.go` — PubGrub DependencyProvider
- `capabilities.go` — module → rock mapping
- `rock_file.go` — .rock archive extraction
- `build.go` — native rock building
- `tree.go` — tree layout
- `lockfile.go` — luarocks.lock codec
- `adapter.go` — top-level Resolver

Estimated LOC: ~3,500. Smaller than most adapters because the
ecosystem is small and the resolver machinery is shared with
substrate.

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Rock not in manifest | `ErrNoSuchRecipe` | Suggest `luarocks install` fallback |
| No compatible version for Lua target | `ErrNoSatisfyingVersion` | "X supports Lua [5.3,5.4]; project uses 5.1" |
| Platform binary unavailable, no src | `ErrNoSatisfyingVersion` | "No linux-x86_64 rock; only windows-x86" |
| Rockspec evaluation fails (sandbox) | `ErrInternalBug` or user | Surface Lua error |
| Native build fails | `ErrInternalBug` or user | Surface compiler stderr |
| .rock archive corrupted | `ErrIntegrityMismatch` | Checksum mismatch if available |

## 10. Security

### 10.1 Sandboxed Lua evaluation

Rockspecs can contain arbitrary Lua. Always evaluate in a
restricted sandbox: no `os`, no `io.open` with write mode, no
`require` for third-party modules, no network I/O. Only pure
computation allowed.

gopher-lua's `SkipOpenLibs: true` option disables default
libraries; we re-enable only `string`, `math`, `table`.

### 10.2 Checksum verification

.rock and source tarballs have checksums in the rockspec's
`source.md5` / `source.sha256` fields. Verify on fetch.

### 10.3 Native build sandboxing

Native builds run make / cmake / gcc. Substrate's subprocess
sandbox restricts filesystem scope. No network by default.

## 11. Testing

### 11.1 Unit tests

- Version parser + comparator
- Rockspec evaluator on 100+ real rockspecs from luarocks.org
- Manifest evaluator
- Range parser
- .rock extraction

### 11.2 Integration tests

- Resolve luasocket + luasec (typical Lua project)
- Resolve a rock requiring native build (luafilesystem)
- Resolve with multiple Lua version constraints
- Resolve using module-based (capability) deps

### 11.3 Ecosystem compat

20 Lua projects. Oracle: `luarocks install --dry-run`. Match set.

### 11.4 Performance

- Resolve typical Lua project: <1s cold, <100ms warm
- Manifest evaluation: <100ms (parsing 5MB Lua source)
- Rockspec evaluation: <10ms per rockspec

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve | <1s | <500ms |
| Warm resolve | <100ms | <50ms |
| Manifest parse | <100ms | <50ms |
| Rockspec eval (Lua sandbox) | <10ms | <5ms |
| .rock extraction | <50ms | <25ms |
| Materialization, 20 rocks (hardlink) | <200ms | <100ms |
| Peak memory | <100MB | <50MB |

## 13. Phases

**M0.** Types, Rockspec evaluator, version comparator; unit tests.

**M1.** Manifest client. Rockspec + .rock fetching.

**M2.** PubGrub end-to-end. Frontier. Capability resolution for
module-based deps.

**M3.** Tree materializer. Native build support. luarocks.lock codec.
20 ecosystem-compat projects green.

**M4.** Multiple-tree workflows (project + user + system).
Production polish.

## 14. Open Questions

- **Multiple trees.** LuaRocks' tree-search order is
  configurable. The adapter materializes into one specified tree
  by default; the substrate's env primitive supports multiple
  named environments for users who want to express this.
- **LuaJIT vs Lua.** LuaJIT is ABI-compatible with Lua 5.1 for most
  rocks. Treat as a separate `LuaVersion` but with compatibility
  rules letting it consume `lua 5.1` rocks. Some rocks target LuaJIT
  specifically (`luajit` version in dependencies) — distinguish.
- **Sandbox strength.** gopher-lua's sandboxing is good but not
  perfect. For defense in depth, consider also running rockspec
  evaluation in a subprocess with seccomp.

## 15. Dependencies

- Substrate M1 (HTTP, cache) → adapter M1
- Substrate M2 (PubGrub, frontier) → adapter M2
- Substrate M3 (materializer, lockfile, env, subprocess) → adapter M3

External Go dependencies:

- `github.com/yuin/gopher-lua` — embedded Lua interpreter
- Custom version comparator + range parser (~200 LOC)

No dependency on other adapters.
