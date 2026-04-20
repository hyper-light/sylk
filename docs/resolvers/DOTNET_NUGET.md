# DOTNET_NUGET.md — .NET / NuGet Adapter Implementation Plan

Tier 4 — first of the **registry-federation / lockfile-heavy** tier.
Validates the substrate's **service-index capability discovery**
(NuGet pioneered this), **multi-feed federation as a first-class
concern** (NuGet was designed multi-feed from day one), **framework
targeting** (multi-axis constraint), and **Central Package Management
+ packages.lock.json** (opt-in lockfile pattern).

## 1. Overview

The .NET adapter resolves and materializes NuGet packages from:

- **[nuget.org](https://api.nuget.org/v3/index.json)** (default
  public feed)
- **Private NuGet feeds** (Azure Artifacts, AWS CodeArtifact,
  GitHub Packages, MyGet, ProGet, Sonatype Nexus with NuGet
  support, JFrog Artifactory)
- **Local feeds** (directory of .nupkg files; offline workflows)
- **Folder-based feeds** (V2-style file:// URLs; legacy)

Produces:

- A resolved dependency graph honoring framework targeting +
  package version + Central Package Management
- An updated project file (`.csproj` / `.fsproj` / `Directory.Packages.props`)
- An optional `packages.lock.json` (when
  `<RestorePackagesWithLockFile>true</RestorePackagesWithLockFile>`)
- A materialized package cache compatible with NuGet's `~/.nuget/packages/`

User-visible behaviors (M3 target):

- `sylk resolve dotnet ./MyProject.csproj` → resolved tree (+ optional
  lockfile)
- `sylk install dotnet` → packages restored to local cache
- `sylk add dotnet <PackageId> [--version <range>]` → modifies
  `.csproj` or `Directory.Packages.props` (CPM-aware)
- `sylk why dotnet <PackageId>` → resolution explanation including
  framework selection
- Strict mode (`--strict`) uses PubGrub for genuine constraint
  satisfaction

Non-goals:

- Running `dotnet build` (we resolve and materialize; runtime is
  the user's `dotnet` CLI)
- MSBuild evaluation (we parse declarative subsets of `.csproj`;
  full MSBuild evaluation requires invoking MSBuild)

## 2. Data Model

### 2.1 Coordinates

```go
type NuGetCoordinate struct {
    ID                string         // "Newtonsoft.Json" (case-insensitive lookup)
    Version           NuGetVersion   // SemVer 2.0 + .NET extensions
    TargetFramework   FrameworkMoniker
    Source            NuGetSource    // which feed served this
}

type NuGetVersion struct {
    Major, Minor, Patch, Revision int  // 4-part version (.NET-specific)
    PreRelease string  // "rc1", "beta.2"
    Metadata   string  // build metadata
}

func (v NuGetVersion) Compare(other NuGetVersion) int { ... }
func (v NuGetVersion) IsPreRelease() bool { return v.PreRelease != "" }
```

NuGet uses 4-part versions (`1.2.3.4`) which is a .NET
convention; SemVer 2.0 normally has 3 parts. The Revision field
is optional but common in older packages.

### 2.2 Framework targeting

[Target Framework Monikers](https://learn.microsoft.com/en-us/dotnet/standard/frameworks)
identify which .NET runtime + version a package supports:

```go
type FrameworkMoniker struct {
    Identifier string  // "net", "netstandard", "netframework", "netcoreapp"
    Version    NuGetVersion
    Profile    string  // optional: "client", "compact"
    Platform   string  // optional: "windows", "linux", "macos", "ios", "android"
    PlatformVersion string  // platform version when applicable
}

// String produces the canonical TFM:
//   net8.0
//   net8.0-windows7.0
//   netstandard2.0
//   net48 (== netframework 4.8)
//   netcoreapp3.1
func (f FrameworkMoniker) String() string { ... }

// Compatible returns true when consumer's target framework can use
// recipe's target framework. Implements .NET's compatibility table
// (netstandard fallbacks, net→netstandard fallbacks, etc.).
func (consumer FrameworkMoniker) Compatible(recipe FrameworkMoniker) bool { ... }
```

The compatibility table is non-trivial:

- `net8.0` consumes `net8.0`, `net7.0`, `net6.0`, `netstandard2.1`,
  `netstandard2.0`, `netstandard1.x`
- `netstandard2.0` consumes `netstandard1.x`
- `net48` consumes `netstandard2.0`, `net47`, `net46`, ...
- `netcoreapp3.1` consumes `netstandard2.1` and below

Reference: NuGet's [FrameworkConstants.cs](https://github.com/NuGet/NuGet.Client/blob/dev/src/NuGet.Core/NuGet.Frameworks/FrameworkConstants.cs).
Implementation: port the rules as a lookup table; ~500 LOC.

### 2.3 .csproj parsing

C# project files are XML, MSBuild format:

```xml
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
    <RestorePackagesWithLockFile>true</RestorePackagesWithLockFile>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.3" />
    <PackageReference Include="Microsoft.Extensions.Logging" />  <!-- version from CPM -->
    <PackageReference Include="System.Text.Json" Version="8.0.0" PrivateAssets="all" />
  </ItemGroup>
  <ItemGroup>
    <ProjectReference Include="..\OtherProject\OtherProject.csproj" />
  </ItemGroup>
</Project>
```

```go
type CSProj struct {
    SDK                   string                       // "Microsoft.NET.Sdk", etc.
    TargetFrameworks      []FrameworkMoniker           // could be plural
    Properties            map[string]string            // PropertyGroup contents
    PackageReferences     []PackageReference
    ProjectReferences     []ProjectReference
    PackageVersionsItems  []PackageVersion             // CPM Directory.Packages.props
    Imports               []ProjectImport              // <Import Project="..."/>
}

type PackageReference struct {
    ID            string
    Version       string  // empty if CPM-managed
    PrivateAssets AssetSet  // assets not propagated to consumers
    IncludeAssets AssetSet
    ExcludeAssets AssetSet
    NoWarn        []string
}

type AssetSet int
const (
    AssetCompile  AssetSet = 1 << iota
    AssetRuntime
    AssetContentFiles
    AssetBuild
    AssetBuildMultitargeting
    AssetBuildTransitive
    AssetAnalyzers
    AssetNative
    AssetAll = AssetCompile | AssetRuntime | AssetContentFiles | AssetBuild | AssetBuildMultitargeting | AssetBuildTransitive | AssetAnalyzers | AssetNative
)
```

### 2.4 Directory.Packages.props (Central Package Management)

When `<ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>`
is set, project files declare `<PackageReference>` without
versions; versions live in a single `Directory.Packages.props` at
the repo root:

```xml
<Project>
  <PropertyGroup>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
    <CentralPackageTransitivePinningEnabled>true</CentralPackageTransitivePinningEnabled>
  </PropertyGroup>
  <ItemGroup>
    <PackageVersion Include="Newtonsoft.Json" Version="13.0.3" />
    <PackageVersion Include="Microsoft.Extensions.Logging" Version="8.0.0" />
  </ItemGroup>
</Project>
```

Transitive pinning: a `<PackageVersion>` declared centrally without
a corresponding `<PackageReference>` in any project still
constrains transitive resolution — if a transitive dependency tries
to bring in a different version, the centrally-declared version
wins.

The adapter walks up the directory tree from each `.csproj` to
find `Directory.Packages.props` (closest wins for nested
hierarchies); merges with the project's own properties.

## 3. HTTP Transport

### 3.1 Service index protocol

Every NuGet feed starts with a service-index URL:

```
GET https://api.nuget.org/v3/index.json
```

Response declares the resources this feed serves:

```json
{
  "version": "3.0.0",
  "resources": [
    { "@id": "https://api.nuget.org/v3-flatcontainer/", "@type": "PackageBaseAddress/3.0.0" },
    { "@id": "https://api.nuget.org/v3/registration5-semver1/", "@type": "RegistrationsBaseUrl/3.6.0" },
    { "@id": "https://azuresearch-usnc.nuget.org/query", "@type": "SearchQueryService/3.5.0" },
    { "@id": "https://api.nuget.org/v3-flatcontainer/", "@type": "PackagePublish/2.0.0" },
    { "@id": "...", "@type": "Vulnerability" },
    { "@id": "...", "@type": "RepositorySignatures" }
  ]
}
```

The adapter caches the service index per feed (long TTL — feeds
rarely change capabilities). Resource lookup by `@type` with
version negotiation:

```go
func (n *NuGetAdapter) FetchServiceIndex(ctx context.Context, feedURL string) (*ServiceIndex, error) { ... }

func (s *ServiceIndex) ResourceURL(typeName string) (string, error) {
    // Find resource matching typeName. Multiple versions may exist; pick the
    // newest version we support.
}
```

### 3.2 PackageBaseAddress (flat container)

The primary metadata endpoint:

```
# Version listing:
GET {base}/{lower-id}/index.json
# → {"versions": ["1.0.0", "2.0.0", "13.0.3", ...]}

# Per-version manifest:
GET {base}/{lower-id}/{lower-version}/{lower-id}.nuspec
# → XML nuspec file

# Per-version package binary:
GET {base}/{lower-id}/{lower-version}/{lower-id}.{lower-version}.nupkg
# → .nupkg ZIP archive
```

`{lower-id}` is the package ID lowercased; `{lower-version}` is
the version normalized to lowercase. Predictable URLs from
coordinates — same design as cargo's sparse index.

### 3.3 RegistrationsBaseUrl (legacy / richer metadata)

Returns paginated JSON registration data with full per-version
metadata embedded:

```
GET {base}/{lower-id}/index.json
# → { "items": [ ... pages ... ] }
GET {base}/{lower-id}/page/{lower-low-version}/{lower-high-version}.json
# → { "items": [ ... per-version objects ... ] }
```

Used as a fallback when PackageBaseAddress is insufficient (e.g.,
listing dependencies for very large packages where the nuspec
fetch matters).

Modern adapters use PackageBaseAddress for version listing +
nuspec; Registrations is for backwards compat with V2 protocol
clients.

### 3.4 .nupkg format

A `.nupkg` is a ZIP archive (similar to wheels in Python). Layout:

```
my-package.1.0.0.nupkg
├── _rels/.rels
├── [Content_Types].xml
├── my-package.nuspec
├── lib/
│   ├── net8.0/
│   │   └── MyPackage.dll
│   ├── netstandard2.0/
│   │   └── MyPackage.dll
│   └── net48/
│       └── MyPackage.dll
├── runtimes/
│   ├── win-x64/native/myhelper.dll
│   ├── linux-x64/native/libmyhelper.so
│   └── osx-arm64/native/libmyhelper.dylib
├── build/
│   └── My.Package.targets
├── tools/
│   └── install.ps1
└── content/
```

Per-framework asset selection happens at materialization time —
the adapter picks the `lib/{tfm}/` directory whose TFM is
compatible with the consuming project's target framework.

### 3.5 Authentication

Per-feed credentials in:

- `~/.nuget/NuGet/NuGet.Config` (Linux/macOS) /
  `%APPDATA%\NuGet\NuGet.Config` (Windows)
- Project-local `NuGet.Config` (overrides user)
- Environment variables (`NUGET_<feedname>_API_KEY`,
  `NUGET_<feedname>_PASSWORD`)

```xml
<configuration>
  <packageSources>
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
    <add key="azure-artifacts" value="https://pkgs.dev.azure.com/myorg/_packaging/myfeed/nuget/v3/index.json" />
  </packageSources>
  <packageSourceCredentials>
    <azure-artifacts>
      <add key="Username" value="myuser" />
      <add key="ClearTextPassword" value="..." />
    </azure-artifacts>
  </packageSourceCredentials>
</configuration>
```

Cleartext or encrypted (Windows DPAPI; Linux/macOS uses Microsoft's
cross-platform credential helper). Encrypted credentials require
the same DPAPI/keychain integration; the adapter shells out to
`dotnet nuget` for credential resolution when DPAPI is involved
(no Go-native DPAPI library exists; subprocess fallback is
acceptable).

### 3.6 Package Source Mapping

NuGet 6.0+ supports package source mapping — pinning specific
package prefixes to specific feeds:

```xml
<packageSourceMapping>
  <packageSource key="nuget.org">
    <package pattern="*" />
  </packageSource>
  <packageSource key="azure-artifacts">
    <package pattern="MyOrg.*" />
    <package pattern="ContosoFork.*" />
  </packageSource>
</packageSourceMapping>
```

`MyOrg.*` packages are *only* fetched from `azure-artifacts`, even
if `nuget.org` has a package matching that name (typo-squatting
defense). The substrate's `FeedMapping` primitive models this
directly.

## 4. Metadata Layer

### 4.1 nuspec parsing

`.nuspec` is XML:

```xml
<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata>
    <id>Newtonsoft.Json</id>
    <version>13.0.3</version>
    <authors>James Newton-King</authors>
    <license type="expression">MIT</license>
    <projectUrl>https://www.newtonsoft.com/json</projectUrl>
    <description>Json.NET is a popular high-performance JSON framework for .NET</description>
    <dependencies>
      <group targetFramework=".NETStandard2.0">
        <!-- no deps -->
      </group>
      <group targetFramework=".NETFramework4.5">
        <!-- no deps -->
      </group>
      <group targetFramework=".NETFramework2.0">
        <!-- no deps -->
      </group>
    </dependencies>
  </metadata>
</package>
```

Notable: dependencies are **grouped by target framework**. Different
TFMs may declare different dependency sets. The resolver picks the
TFM group matching the consumer's target framework.

```go
func ParseNuspec(data []byte) (*Nuspec, error) { ... }

type Nuspec struct {
    ID           string
    Version      NuGetVersion
    Authors      string
    License      License
    Description  string
    Dependencies []DependencyGroup
    FrameworkAssemblies []FrameworkAssembly  // .NET FX assemblies referenced
    References   []NuspecReference  // explicit assembly references
    ContentFiles []ContentFile      // content files with build action metadata
    PackageTypes []PackageType      // "Dependency", "DotnetTool", "Template"
    Vulnerabilities []Vulnerability  // CVE entries (newer feeds)
}

type DependencyGroup struct {
    TargetFramework FrameworkMoniker
    Dependencies    []NuspecDependency
}

type NuspecDependency struct {
    ID      string
    Version string  // version range
    Exclude string  // comma-separated asset exclusions
    Include string  // comma-separated asset inclusions
}
```

### 4.2 Version range syntax

NuGet uses interval notation (similar to Maven's, slightly
different):

- `1.0` — soft requirement (minimum), no upper bound
- `[1.0]` — exactly 1.0
- `[1.0,)` — ≥1.0
- `[1.0,2.0)` — ≥1.0, <2.0
- `(,1.5]` — ≤1.5
- `*` — any
- `1.0.*` — wildcard match
- `1.*-*` — wildcard with pre-release allowed

Implementation: ~300 LOC PEG parser + interval evaluator.

### 4.3 Framework selection algorithm

Given a consumer's TFM (e.g., `net8.0`) and a package's
DependencyGroups, pick the **best** group:

```go
func SelectDependencyGroup(consumer FrameworkMoniker, groups []DependencyGroup) (*DependencyGroup, error) {
    // 1. Filter compatible groups (per FrameworkMoniker.Compatible).
    // 2. Of compatible, pick the most specific:
    //    a. Exact match preferred.
    //    b. Closest version match.
    //    c. Same identifier preferred over fallback identifier.
    // 3. If multiple equally-good matches, error.
    // 4. If no compatible group, fall back to the default group (no targetFramework).
}
```

Reference: NuGet's [FrameworkReducer.cs](https://github.com/NuGet/NuGet.Client/blob/dev/src/NuGet.Core/NuGet.Frameworks/FrameworkReducer.cs).
Port the algorithm precisely.

### 4.4 Cache keys

```
(ecosystem="nuget", name=<lowercased ID>, version=<lowercased version>, platform_hash=<hash of TFM>)
```

The platform_hash captures the consumer's target framework, since
the same package version has different *effective* dependency sets
per TFM. Cache entries are TFM-specific.

## 5. Resolver

### 5.1 NuGet's resolution model

The .NET 9 resolver rewrite produces a **flat set** with one node per
unique `(package, version, framework)`. Conflicts resolve eagerly
during graph construction. This is the substrate's PubGrub pattern
applied to NuGet specifically.

```go
type nugetDepProvider struct {
    fetcher    *nugetFetcher
    project    *CSProj
    cpm        *CentralPackageManagement  // when enabled
    feeds      []FeedReference
    targetFW   FrameworkMoniker
    cache      *substrate.MetadataCache
}

func (p *nugetDepProvider) AvailableVersions(ctx context.Context, pkg NuGetCoordinate) ([]NuGetVersion, error) {
    // 1. Check FeedMapping; restrict to mapped feeds for this package prefix.
    // 2. Fetch /index.json for each compatible feed in parallel.
    // 3. Collect versions; dedupe across feeds.
    // 4. Apply CPM transitive pinning if enabled (target version pinned by central).
    // 5. Filter pre-releases unless project requests pre-release.
    // 6. Order: newest first; lockfile pin priority.
}

func (p *nugetDepProvider) Dependencies(ctx context.Context, pkg NuGetCoordinate, ver NuGetVersion) ([]pubgrub.Dependency, error) {
    // 1. Fetch nuspec.
    // 2. Select dependency group matching p.targetFW.
    // 3. For each dep in group, translate to pubgrub.Dependency.
    // 4. Apply assets filtering: PackageReference's PrivateAssets/ExcludeAssets/IncludeAssets
    //    determine whether dep is propagated transitively or just consumed locally.
    // 5. Apply CPM transitive pinning: if pkg name is centrally pinned, override
    //    the dep's version to the centrally-pinned version.
}
```

### 5.2 Central Package Management semantics

CPM affects version resolution:

- Direct PackageReference with version → use that version
- Direct PackageReference without version → look up in CPM
  Directory.Packages.props
- Transitive dependency referenced by another package, with CPM
  transitive pinning enabled → CPM version overrides transitive's
  declared version

CPM check happens at `AvailableVersions` time:

```go
func (p *nugetDepProvider) effectiveVersionRange(pkgID string, declaredRange string) string {
    if p.cpm == nil || !p.cpm.TransitivePinningEnabled {
        return declaredRange
    }
    if pinned, ok := p.cpm.PackageVersions[pkgID]; ok {
        return pinned // CPM wins
    }
    return declaredRange
}
```

### 5.3 Asset propagation

PackageReference's `PrivateAssets="all"` means: this dependency is
used by my project but is **not propagated** to consumers of my
project. Important for testing tools, build tools, source
generators that shouldn't appear in downstream `bin/Release` outputs.

The resolver tracks asset propagation rules through the graph:

```go
type AssetPropagation struct {
    ConsumerProject string
    ToPackage       string
    IncludeAssets   AssetSet
    ExcludeAssets   AssetSet
    PrivateAssets   AssetSet
}
```

When emitting the resolved tree, transitive deps are filtered by
asset rules — a `PrivateAssets="all"` dependency's transitives are
not included in downstream consumers' resolution.

### 5.4 Strict mode

Like Maven's strict mode, opt-in PubGrub-driven satisfaction. NuGet's
default is "newest version satisfying all constraints" which often
silently picks newer transitive versions; strict mode requires every
declared range to be honored exactly.

### 5.5 Frontier

PubGrub-based; implements `FrontierAwareResolver`. Frontier events
drive prefetch of nuspec files for candidate versions. Particularly
valuable for NuGet because nuspec files are small (~5 KB) and
candidate consideration is highly speculative.

## 6. Materializer

### 6.1 NuGet cache layout

`~/.nuget/packages/` is the canonical user cache:

```
~/.nuget/packages/
  newtonsoft.json/
    13.0.3/
      newtonsoft.json.nuspec
      newtonsoft.json.13.0.3.nupkg
      newtonsoft.json.13.0.3.nupkg.sha512
      .nupkg.metadata
      lib/
        net8.0/
          Newtonsoft.Json.dll
          Newtonsoft.Json.xml  # XML doc comments
        netstandard2.0/
          ...
      ...
```

Files lowercased on disk; case-insensitive lookup. The substrate's
recipe store mirrors this layout.

### 6.2 Per-framework asset selection at materialization

The resolver picks one TFM per package; the materializer extracts
**only that TFM's assets**:

```go
func (m *nugetMaterializer) installPackage(ctx context.Context, pkg ResolvedNuGetPackage, dst string) error {
    // 1. Fetch nupkg, verify SHA-512 hash.
    // 2. Open as ZIP.
    // 3. Pick lib/{tfm}/ subdirectory matching pkg's resolved TFM.
    // 4. Extract that subdirectory's contents into dst (typically project's bin/).
    // 5. If pkg has runtimes/ entries, extract platform-specific runtime files
    //    matching the consumer's RuntimeIdentifier (RID).
    // 6. If pkg has analyzers/, extract Roslyn analyzer DLLs to a separate
    //    analyzer-specific path.
}
```

### 6.3 RuntimeIdentifier (RID) handling

`runtimes/{rid}/native/` directories contain platform-specific
native binaries. The consumer's RID (`win-x64`, `linux-x64`,
`linux-arm64`, `osx-arm64`, etc.) determines which native files are
copied.

RID compatibility is hierarchical:

- `linux-x64` is compatible with `linux`, `unix`, `any`
- `win-x64` is compatible with `win`, `any`

Reference: NuGet's [RuntimeGraph.cs](https://github.com/NuGet/NuGet.Client/blob/dev/src/NuGet.Core/NuGet.RuntimeModel/RuntimeGraph.cs).
Port the compatibility graph (~300 LOC + JSON data file).

### 6.4 Build assets

Packages with `build/` directories contain MSBuild `.targets` /
`.props` files automatically imported by consuming projects. The
materializer makes these available; the actual import happens at
MSBuild evaluation (out of scope for the resolver).

## 7. Lockfile

### 7.1 packages.lock.json format

```json
{
  "version": 2,
  "dependencies": {
    "net8.0": {
      "Newtonsoft.Json": {
        "type": "Direct",
        "requested": "[13.0.3, )",
        "resolved": "13.0.3",
        "contentHash": "..."
      },
      "Microsoft.Extensions.Logging.Abstractions": {
        "type": "Transitive",
        "resolved": "8.0.0",
        "contentHash": "...",
        "dependencies": {
          "System.Diagnostics.DiagnosticSource": "8.0.0"
        }
      }
    }
  }
}
```

The schema is documented at [NuGet.Client](https://github.com/NuGet/NuGet.Client/blob/dev/src/NuGet.Core/NuGet.ProjectModel/LockFile/PackagesLockFile.cs).

### 7.2 LockfileCodec

```go
type nugetLockfileCodec struct{}

func (c *nugetLockfileCodec) Ecosystem() string { return "nuget" }
func (c *nugetLockfileCodec) Filename() string  { return "packages.lock.json" }
func (c *nugetLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) { ... }
func (c *nugetLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Group entries by TFM (target framework).
    // Sort entries within each TFM alphabetically.
    // Emit JSON matching NuGet's format exactly.
}
```

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Direct use (NuGet's flat-set + eager-conflict semantics align) |
| `core/substrate/http` | All service-index, flat-container, nupkg fetches |
| `core/substrate/cache/metadata` | Service index (long TTL), version listings, nuspecs |
| `core/substrate/store/recipe` | Shared nupkg storage, extracted TFM assets |
| `core/substrate/materializer` | Reflink/hardlink to NuGet-compat cache |
| `core/substrate/lockfile` | packages.lock.json codec |
| `core/substrate/feeds` | Multi-feed federation, FeedMapping for source mapping |
| `core/substrate/auth` | NuGet.Config credential resolution + DPAPI fallback |
| `core/substrate/frontier` | Standard PubGrub frontier |
| `core/substrate/sigverify` | Authenticode signature verification on nupkg |

Adapter modules under `adapters/dotnet/`:

- `coordinate.go` — `NuGetCoordinate`
- `version.go` — `NuGetVersion` (4-part SemVer extension)
- `framework.go` — `FrameworkMoniker` + compatibility table
- `runtime.go` — RID compatibility graph
- `csproj.go` — .csproj/.fsproj parser (declarative subset)
- `cpm.go` — Directory.Packages.props discovery + transitive pinning
- `nuspec.go` — nuspec XML parser
- `serviceindex.go` — service index discovery
- `flatcontainer.go` — PackageBaseAddress client
- `registrations.go` — RegistrationsBaseUrl client (fallback)
- `nupkg.go` — .nupkg ZIP extraction
- `assets.go` — asset propagation rules
- `provider.go` — PubGrub DependencyProvider
- `framework_select.go` — best-match TFM selection
- `materializer.go` — NuGet cache layout
- `nuget_config.go` — NuGet.Config parser + credential extraction
- `lockfile.go` — packages.lock.json codec
- `signature.go` — Authenticode signature verification
- `adapter.go` — top-level Resolver

Estimated LOC: ~7,000. Complexity drivers:

- Framework compatibility table (~500 LOC + data)
- RID compatibility graph (~300 LOC + JSON)
- .csproj / Directory.Packages.props parsing (~1500 LOC)
- Asset propagation logic (~400 LOC)
- NuGet.Config XML schema (~500 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Package not on any feed | `ErrNoSuchRecipe` | Honor feed source mapping |
| No compatible TFM in package | `ErrNoSatisfyingVersion` | "X has TFMs [a,b,c] but project targets Y" |
| Version pinned by CPM but doesn't satisfy other constraints | `ErrCapabilityConflict` | Surface CPM file + conflicting requirement |
| RID not supported by package | `ErrNoSatisfyingVersion` | "Need linux-arm64 but X provides only [win-x64, osx-arm64]" |
| nupkg signature invalid | `ErrSignatureFailed` | When repository requires signing |
| nupkg SHA-512 mismatch | `ErrIntegrityMismatch` | Fatal |
| FeedMapping rejects this package on this feed | `ErrCapabilityConflict` | Mapping pattern violation |
| Service index fetch fails for all feeds | `ErrNetworkPermanent` | After substrate retry |

## 10. Security

### 10.1 Authenticode / Repository signatures

NuGet supports two signature types:

- **Author signatures** (Authenticode): the package author signs
  with their code-signing certificate
- **Repository signatures**: the feed (e.g., nuget.org) signs the
  package on receipt with the repository's certificate

The adapter verifies both when present. nuget.org requires
repository signatures since 2018; verifying these protects against
mirror tampering.

Authenticode verification on Linux/macOS uses a vendored Microsoft
Authenticode library or shells out to `dotnet nuget verify`. Pure-Go
Authenticode verification is non-trivial; subprocess fallback is
acceptable.

### 10.2 Vulnerability data

NuGet feeds can serve vulnerability data via the `Vulnerability`
service. The adapter queries this and surfaces known-vulnerable
versions as `CapabilityConflict` with severity.

GitHub Advisory Database integration provides additional CVE data
for packages whose feeds don't serve their own vulnerability
endpoint.

### 10.3 Package source mapping (typo-squatting defense)

`packageSourceMapping` is the canonical defense against typo-
squatting. The adapter enforces mappings strictly — a package
matching a mapping pattern that's served by a non-mapped feed is
rejected with `ErrCapabilityConflict`.

### 10.4 Credentials

NuGet.Config can store credentials in three forms:

- `<add key="ClearTextPassword" value="..."/>` — plain text (warns
  if file is world-readable)
- `<add key="Password" value="..."/>` — DPAPI-encrypted (Windows)
- `<add key="ValidAuthenticationTypes" value="basic,negotiate"/>`
  — credential helper integration

The adapter's auth resolution honors all three; encrypted password
handling shells out to `dotnet nuget` on Windows for DPAPI
decryption.

## 11. Testing

### 11.1 Unit tests

- `NuGetVersion` parser + comparator (4-part edge cases)
- Framework compatibility table on every TFM combination
- RID compatibility graph
- Version range parser
- nuspec XML parser on 50+ real nuspec files
- .csproj parser on 100+ real project files
- CPM transitive pinning logic
- Asset propagation rules

### 11.2 Integration tests

- Resolve ASP.NET Core 8 web app (typical project)
- Resolve Xamarin / MAUI app with multiple TFMs
- Resolve a project with CPM + transitive pinning
- Resolve a project with FeedMapping restricting feeds
- Resolve a multi-targeted library (`<TargetFrameworks>net8.0;net48;netstandard2.0</TargetFrameworks>`)
- Resolve with asset propagation (PrivateAssets="all" testing tools)
- Authenticode signature verification

### 11.3 Ecosystem compatibility

Golden corpus of 50 .NET projects, oracle: `dotnet restore`
output. Match resolution + lockfile output byte-identical.

### 11.4 Performance

- Resolve typical ASP.NET Core app: <5s cold, <500ms warm
- Resolve large enterprise repo (Microsoft 1ES Pipeline-scale): <2 min
  (Microsoft's own .NET 9 benchmark; we should land within 2× of
  their 2-minute target)
- nuspec fetch + parse: <50ms
- Framework selection (per package, 100 deps): <1ms

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, typical ASP.NET app | <5s | <3s |
| Cold resolve, multi-target library | <8s | <5s |
| Warm resolve | <500ms | <250ms |
| Service index fetch (cache miss) | <100ms | <50ms |
| Service index fetch (cache hit) | <5ms | <2ms |
| Version listing fetch (per pkg) | <100ms | <50ms |
| nuspec fetch + parse | <50ms | <25ms |
| Materialization, 100 packages (reflink) | <1s | <500ms |
| Authenticode verification (per pkg) | <100ms | <50ms |
| Peak memory, 500-package resolve | <300MB | <200MB |

## 13. Phases

**M0.** Types compile; FrameworkMoniker, NuGetVersion, .csproj
parser unit tests.

**M1.** Service index discovery + flat container client + nuspec
parsing. Single-package fetch.

**M2.** PubGrub end-to-end resolution. Framework selection. CPM.
Asset propagation.

**M3.** Multi-feed federation + FeedMapping. Lockfile codec.
Authenticode verification. Materializer with NuGet-compat layout.
Ecosystem compat green on 50 projects. Performance targets met.

**M4.** Vulnerability data integration. RuntimeGraph for native
asset selection. Multi-targeting. Production polish.

## 14. Open Questions

- **FrameworkReducer port.** NuGet's FrameworkReducer.cs is ~3000
  LOC of subtle compatibility logic. Port verbatim or reimplement?
  Proposal: port verbatim with comprehensive tests; correctness >
  cleanliness.
- **DPAPI decryption.** Pure Go implementation is impractical
  (proprietary algorithm). Shell out to `dotnet nuget` or accept
  Windows-only support? Proposal: subprocess fallback.
- **Multi-targeting resolution.** A project with three TFMs needs
  three resolutions. Run sequentially or in parallel? Proposal:
  parallel with shared metadata cache.
- **Tools packages.** `<PackageReference Include="X" PrivateAssets="all">`
  for tools packages (analyzers, source generators) needs special
  asset handling. Document the matrix; default to "tools assets
  not propagated, runtime assets propagated."
- **MAUI / Xamarin platform-specific assets.** These projects
  include many runtimes/{platform}/ assets. The materializer must
  handle correctly; might need per-platform staging.
- **Source-only packages.** Some packages are source-only (no
  compiled DLLs); the materializer must extract and integrate
  with the consumer's compilation. M4 concern.

## 15. Dependencies

- Substrate M2 (multi-feed, frontier) → adapter M2
- Substrate M3 (materializer, lockfile, signature verification)
  → adapter M3

External Go dependencies:

- Custom FrameworkMoniker compatibility (no off-the-shelf Go library)
- Custom RID compatibility graph (loaded from JSON data file)
- `encoding/xml` — stdlib for nuspec, csproj, NuGet.Config
- `archive/zip` — stdlib for .nupkg extraction

No dependency on other adapters.
