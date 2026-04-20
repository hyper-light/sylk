# SCALA_COURSIER.md — Scala / Coursier Adapter Implementation Plan

Tier 3 finish — completes the JVM cluster. Validates the substrate
handles **identity-suffix encoding** (Scala's binary version baked
into artifact names) and **dual metadata format support** (POM XML
and Apache Ivy XML). Architecturally parallels Coursier's
Scala-native resolver: same Maven Central protocol as Maven and
Gradle, much faster execution.

## 1. Overview

The Scala adapter resolves and materializes JARs from:

- **Maven Central** (primary; serves the bulk of Scala libraries)
- **[Sonatype OSS Snapshots](https://s01.oss.sonatype.org/content/repositories/snapshots/)**
- **Typesafe / Scala-tools.org Ivy repositories** (legacy, still
  active)
- **Bintray-derived JFrog mirrors** (post-Bintray-shutdown
  archives)
- **Private Maven/Ivy repositories** (Nexus, Artifactory)
- **Coursier-managed local cache** (`~/.cache/coursier`) as a
  read-through source

Produces:

- A resolved dependency tree honoring Scala-version-pinned artifact
  identities
- A `build.sbt`-friendly resolved set (sbt consumes via standard
  Maven/Ivy protocols)
- An Ivy-format lockfile (`build.sbt.lock`) for sbt-coursier-lock
  compatibility
- A materialized cache layout compatible with Coursier's own
  `~/.cache/coursier`

User-visible behaviors (M3 target):

- `sylk resolve scala ./build.sbt` (or `./project/Dependencies.scala`)
  → resolved tree
- `sylk install scala` → cache populated; sbt consumes via
  `useCoursier := true` (or its successor)
- `sylk add scala "org.typelevel" %% "cats-core" % "2.10.0"` →
  modifies build script
- `sylk why scala <coord>` → PubGrub explanation

Non-goals:

- Running sbt itself (we resolve and materialize; users continue to
  invoke sbt for compile/test/run)
- Generating SBT plugins or task definitions
- Cross-build matrix orchestration (sbt's `++scala-version` model;
  the adapter resolves for one Scala version per invocation)

## 2. Data Model

### 2.1 Coordinates

```go
type ScalaCoordinate struct {
    Organization  string         // groupId equivalent: "org.typelevel"
    Name          string         // bare module name: "cats-core" (no _2.13)
    Version       MavenVersion   // shared with Maven adapter
    ScalaVersion  ScalaBinaryVersion  // resolved Scala binary version
    Platform      ScalaPlatform   // JVM, JS, Native
    Classifier    string         // optional: "sources", "javadoc"
}

type ScalaBinaryVersion struct {
    Major int     // 2 or 3
    Minor int     // 13, 12, 11, ... for Scala 2; ignored for Scala 3
    // Scala 3 uses just "3" (no minor) per its forward-compat promise.
    String string // canonical: "2.13", "3"
}

type ScalaPlatform struct {
    Kind ScalaPlatformKind  // JVM, JS, Native
    JSVersion     string    // "1.x" for Scala.js
    NativeVersion string    // "0.4" for Scala Native
}

type ScalaPlatformKind int
const (
    PlatformJVM ScalaPlatformKind = iota
    PlatformJS
    PlatformNative
)

// EncodedArtifactName produces the Maven/Ivy artifact ID with
// platform + Scala version suffix:
//   cats-core             → cats-core (Java library, no suffix)
//   cats-core_2.13        → JVM Scala 2.13
//   cats-core_3           → JVM Scala 3
//   cats-core_sjs1_2.13   → Scala.js 1.x, Scala 2.13
//   cats-core_native0.4_2.13 → Scala Native 0.4, Scala 2.13
func (s ScalaCoordinate) EncodedArtifactName() string { ... }
```

The encoding-into-identity is the unique-to-Scala part. The build
script's `%%` operator (in sbt) and `%%%` operator (for
cross-platform) are syntactic sugar for "compute the encoded name
at resolve time using the project's Scala version + platform."

### 2.2 build.sbt parsing

sbt build files are Scala source code, full Turing-complete. The
adapter parses the **declarative subset** — `libraryDependencies`,
`scalaVersion`, `crossScalaVersions`, `resolvers`,
`dependencyOverrides`, `excludeDependencies`. Non-trivial logic in
build.sbt (functions, conditionals, plugins) requires invoking sbt
itself.

```go
type SBTBuildScript struct {
    ScalaVersion        string                  // "2.13.12" or "3.3.1"
    CrossScalaVersions  []string                // for cross-builds
    LibraryDependencies []SBTDependency
    Resolvers           []SBTResolver
    DependencyOverrides []SBTDependency
    ExcludeDependencies []SBTExclude
    Settings            map[string]interface{}  // for known declarative keys
}

type SBTDependency struct {
    Organization        string
    Name                string
    Version             string
    Configuration       string         // "compile", "test", "provided"
    CrossVersion        SBTCrossVersion // %, %%, %%%, or explicit
    Classifier          string
    Excludes            []SBTExclude
}

type SBTCrossVersion int
const (
    CrossVersionDisabled SBTCrossVersion = iota  // % — no Scala suffix
    CrossVersionBinary                           // %% — append _scalaBinaryVersion
    CrossVersionFull                             // %%/full — append _scalaVersion
    CrossVersionPlatform                         // %%% — append _platform_scalaBinary
    CrossVersionFor3Use2_13                      // sbt-cross — Scala 3 falls back to 2.13 jars
)
```

For non-declarative build files, the adapter offers two fallbacks:

- **`--sbt-shell-api` mode**: invoke sbt as a subprocess to extract
  `dependencyTree` and `resolvers` via sbt's built-in commands.
  Slow first invocation (sbt JVM startup ~10s) but accurate.
- **`--mill` mode**: similar fallback for Mill build tool.

### 2.3 project/Dependencies.scala convention

Many projects centralize dependencies in a Scala source file under
`project/`. The adapter parses these via the same declarative-subset
parser used for build.sbt. Convention is well-established; static
parsing typically works.

## 3. HTTP Transport

### 3.1 Maven repository protocol

Same as Maven adapter — static file tree with predictable URLs from
coordinates. The adapter shares the substrate's HTTP transport with
the Maven and Gradle adapters. Coordinate-to-URL derivation:

```
{repo}/{org-slashes}/{name-with-suffix}/{version}/{name-with-suffix}-{version}[-{classifier}].{ext}
{repo}/{org-slashes}/{name-with-suffix}/{version}/{name-with-suffix}-{version}.pom
```

### 3.2 Apache Ivy XML format

Some Scala libraries (especially older ones) publish to Ivy-format
repositories with `ivy.xml` instead of POM. The path convention
differs:

```
{repo}/{org}/{name-with-suffix}/{version}/ivys/ivy.xml
{repo}/{org}/{name-with-suffix}/{version}/jars/{name-with-suffix}.jar
```

Ivy XML is structurally similar to POM but with different element
names:

```xml
<ivy-module version="2.0">
    <info organisation="org.typelevel" module="cats-core_2.13" revision="2.10.0">
        <license name="Apache-2.0"/>
    </info>
    <configurations>
        <conf name="compile" visibility="public"/>
        <conf name="runtime" extends="compile"/>
        <conf name="test" extends="runtime"/>
    </configurations>
    <publications>
        <artifact name="cats-core_2.13" type="jar" ext="jar" conf="compile"/>
    </publications>
    <dependencies>
        <dependency org="org.typelevel" name="cats-kernel_2.13" rev="2.10.0" conf="compile->compile"/>
    </dependencies>
</ivy-module>
```

The adapter parses both POM and Ivy XML; ~80% of Scala libraries
use POM, ~20% use Ivy or both.

### 3.3 Repository chain

```scala
resolvers ++= Seq(
    Resolver.mavenCentral,
    "Sonatype Snapshots" at "https://s01.oss.sonatype.org/content/repositories/snapshots/",
    "Internal" at "https://nexus.corp/repository/maven-public/",
    Resolver.ivyStylePatterns at "https://repo.example.com/ivy/"
)
```

The adapter walks the resolver chain in declaration order; first
hit wins. Mirror configurations in `~/.coursier/config.json` or
`~/.sbt/repositories` can rewrite which resolver serves which
artifact.

### 3.4 Authentication

Credentials in:

- `~/.sbt/credentials` (sbt-style)
- `~/.coursier/credentials.properties` (Coursier-style)
- `~/.m2/settings.xml` (Maven-style; Coursier honors)
- env vars: `COURSIER_CREDENTIALS`

Format (sbt):

```
realm=Internal Repository
host=nexus.corp
user=myuser
password=mypassword
```

Same auth resolution as Maven adapter; reuse `core/substrate/auth`
with format-specific credential providers.

## 4. Metadata Layer

### 4.1 POM and Ivy parsers

POM parser is shared with Maven adapter (extracted into
`adapters/jvm_shared/pom/`). Ivy XML parser is Scala-adapter-
specific:

```go
func ParseIvyXML(data []byte) (*IvyModule, error) { ... }

type IvyModule struct {
    Info           IvyInfo
    Configurations []IvyConfiguration
    Publications   []IvyPublication
    Dependencies   []IvyDependency
}

type IvyDependency struct {
    Org      string
    Name     string
    Revision string
    Conf     string  // "compile->compile" — configuration mapping
    Excludes []IvyExclude
    Force    bool
}
```

The configuration mapping syntax (`compile->compile`,
`runtime->default`, `*->default`) is Ivy's way of declaring how
dependency configurations connect to consumer configurations. POM
has no equivalent; Ivy XML's expressiveness is why some Scala
libraries still use it.

### 4.2 Scala-suffix discovery

Given a bare module name + Scala version + platform, derive the
encoded name. But also: for any encoded module name in a POM
dependency, derive its bare components for cross-Scala-version
analysis.

```go
func EncodeForScala(bareName string, scalaVer ScalaBinaryVersion, plat ScalaPlatform) string { ... }
func DecodeFromArtifact(artifactID string) (bareName string, scalaVer *ScalaBinaryVersion, plat *ScalaPlatform, ok bool) { ... }
```

Decode is a regex match: `_(?:sjs1_|native0\.4_)?(\d+\.\d+|3)$`.
Some Java libraries that don't follow Scala's naming convention
have artifact IDs that look like Scala-suffixed versions
(`apache-commons-lang_3` is unfortunately ambiguous); the adapter
prefers the bare-name interpretation unless the suffix matches an
expected Scala binary version.

### 4.3 Cross-version compatibility (`for3Use2_13` / `for2_13Use3`)

Scala 3 can consume Scala 2.13 JARs (binary compat layer). sbt's
`for3Use2_13` directive tells the resolver: "when resolving for
Scala 3, allow falling back to `_2.13` JARs if no `_3` JAR exists."

The reverse `for2_13Use3` is rarer (Scala 2.13 consuming Scala 3
JARs requires the `-Ytasty-reader` flag).

```go
type CrossCompatPolicy struct {
    For3Use213 map[string]bool  // module names allowed to fall back
    For213Use3 map[string]bool
}

// During resolution, when encoded artifact for "name_3" 404s, the adapter
// retries "name_2.13" if For3Use213 is enabled for name (or globally).
```

### 4.4 Cache keys

```
(ecosystem="scala", name="org.typelevel:cats-core_2.13", version=*, platform_hash=<hash of (scalaver, platform)>)
```

The platform_hash distinguishes cache entries for the same bare name
across Scala versions and platforms — same bare cats-core might
exist as five separate cache entries (2.12, 2.13, 3 × JVM, JS,
Native).

## 5. Resolver

### 5.1 PubGrub with Scala extensions

The resolver is substrate PubGrub with Scala-aware adapter logic:

```go
type scalaDepProvider struct {
    fetcher       *scalaFetcher
    project       *SBTBuildScript
    scalaVersion  ScalaBinaryVersion
    platform      ScalaPlatform
    crossPolicy   CrossCompatPolicy
}

func (p *scalaDepProvider) AvailableVersions(ctx context.Context, pkg ScalaCoordinate) ([]MavenVersion, error) {
    // 1. Encode the artifact name using p.scalaVersion + p.platform.
    // 2. Fetch maven-metadata.xml (or Ivy equivalent) for the encoded artifact.
    // 3. Parse versions list.
    // 4. Filter by configured stability (snapshots only if explicitly allowed).
    // 5. Order: newest first, lockfile pin priority.
    // 6. If for3Use213 active and no Scala 3 versions found, retry with Scala 2.13.
}

func (p *scalaDepProvider) Dependencies(ctx context.Context, pkg ScalaCoordinate, ver MavenVersion) ([]pubgrub.Dependency, error) {
    // 1. Try fetching .pom; if not present, fall back to ivy.xml.
    // 2. Parse dependency declarations.
    // 3. For each dependency, decode its artifact ID to extract Scala version
    //    component (most should match p.scalaVersion).
    // 4. Apply scope filtering (compile, runtime — exclude test/provided
    //    typically; configurable).
    // 5. Translate to pubgrub.Dependency, re-encoding artifact names with
    //    p.scalaVersion when appropriate.
}
```

### 5.2 Scope semantics (Maven + Ivy)

POM scopes (compile/runtime/test/provided/system/import) carry
Maven semantics. Ivy configuration mappings translate:

- `compile->compile` → Maven compile scope
- `runtime->default` → Maven runtime
- `test->default` → Maven test
- `provided->default` → Maven provided
- `*->default` → all configurations consume the dependency's
  default

The adapter normalizes both to a unified scope model, then applies
substrate-standard scope-transitivity rules.

### 5.3 dependencyOverrides

sbt's `dependencyOverrides` setting forces a specific version of a
transitive dependency:

```scala
dependencyOverrides ++= Seq(
    "com.typesafe.akka" %% "akka-actor" % "2.6.20"
)
```

Override semantics: if `akka-actor` is in the resolved graph (from
any path), force version 2.6.20. Implemented as a PubGrub
constraint that overrides any other range during candidate
selection — equivalent to Cargo's `[patch]`.

### 5.4 Eviction warnings

Coursier (and sbt) report when a transitive's declared version is
"evicted" by a newer version (selected because something else
required ≥ that version). These are informational, not errors —
expose via the substrate's `ResolveResult.ResolutionMetadata` map:

```json
"evictions": [
    {"module": "akka-actor_2.13", "evictedFrom": "2.5.32", "evictedTo": "2.6.20", "by": "akka-stream_2.13"}
]
```

### 5.5 Frontier

PubGrub-based; implements `FrontierAwareResolver`. Frontier events
drive prefetching of POM / Ivy XML files for candidate versions.
Substrate's prefetch coordinator handles the rest.

## 6. Materializer

### 6.1 Coursier cache layout

Coursier's canonical cache at `~/.cache/coursier/v1/` is content-
addressed:

```
~/.cache/coursier/v1/
  https/
    repo1.maven.org/
      maven2/
        org/typelevel/
          cats-core_2.13/
            2.10.0/
              cats-core_2.13-2.10.0.pom
              cats-core_2.13-2.10.0.pom.sha1
              cats-core_2.13-2.10.0.jar
              cats-core_2.13-2.10.0.jar.sha1
```

The path mirrors the source URL (https + host + URL path). This
makes mirror provenance explicit.

The adapter populates this layout if Coursier is configured to
share with Sylk; otherwise materializes into substrate-managed
storage.

### 6.2 Artifact selection

POM-based artifacts produce the `.jar`. Ivy-based artifacts have
explicit publication declarations specifying which artifacts to
fetch (the IvyModule's `<publications>` section).

Special handling for **POM-packaging** modules (used as BOMs in
Maven, sometimes in Scala): metadata only, no JAR.

### 6.3 Incremental compilation cache

Out of scope. sbt's incremental compilation cache
(`~/.sbt/.../target/`) is sbt's concern, not the adapter's.

## 7. Lockfile

### 7.1 sbt-coursier-lock format

`build.sbt.lock` (sbt-dependency-lock plugin) is JSON:

```json
{
    "version": "1",
    "scalaVersion": "2.13.12",
    "configurations": {
        "compile": {
            "dependencies": [
                {"organization": "org.typelevel", "name": "cats-core_2.13", "version": "2.10.0", "checksum": "..."},
                ...
            ]
        }
    }
}
```

The substrate's LockfileSnapshot adapts naturally; one entry per
(organization, name, version), with checksum.

### 7.2 LockfileCodec

```go
type scalaLockfileCodec struct{}

func (c *scalaLockfileCodec) Ecosystem() string { return "scala" }
func (c *scalaLockfileCodec) Filename() string  { return "build.sbt.lock" }
func (c *scalaLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) { ... }
func (c *scalaLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Sort entries deterministically; emit JSON matching sbt-dependency-lock format.
}
```

For projects not using sbt-dependency-lock, the substrate's
canonical lockfile format is used and materialized as
`sylk.lock`.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Direct use |
| `core/substrate/http` | All POM / Ivy XML / JAR fetches |
| `core/substrate/cache/metadata` | Per-artifact metadata caching |
| `core/substrate/store/recipe` | Shared JAR storage |
| `core/substrate/materializer` | Coursier-compat cache layout |
| `core/substrate/lockfile` | sbt-coursier-lock + canonical formats |
| `core/substrate/feeds` | Multi-resolver chain |
| `core/substrate/auth` | sbt + Coursier credential providers |
| `core/substrate/frontier` | PubGrub frontier |
| `core/substrate/pgp` | When resolvers require signature verification |
| `adapters/jvm_shared/pom` | Shared POM parser |

Adapter modules under `adapters/scala/`:

- `coordinate.go` — `ScalaCoordinate`, suffix encoding/decoding
- `binary_version.go` — `ScalaBinaryVersion` + Scala 3 forward-compat
- `platform.go` — JVM / JS / Native platforms
- `sbt_buildscript.go` — build.sbt declarative parser
- `dependencies_scala.go` — project/Dependencies.scala parser
- `mill.go` — Mill fallback parser (M4)
- `ivy.go` — Ivy XML parser
- `cross_compat.go` — for3Use2_13 + for2_13Use3 logic
- `provider.go` — PubGrub DependencyProvider with re-encoding
- `overrides.go` — dependencyOverrides, excludeDependencies
- `materializer.go` — Coursier-compat cache layout
- `lockfile.go` — sbt-coursier-lock codec
- `credentials.go` — sbt + Coursier credential parsers
- `adapter.go` — top-level Resolver

Estimated LOC: ~5,500. Complexity drivers:

- build.sbt declarative parser (~1500 LOC; Scala syntax subset)
- Suffix encoding/decoding with cross-compat fallback (~500 LOC)
- Ivy XML parser + configuration mapping (~700 LOC)
- POM parser shared with Maven (no additional LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Encoded artifact 404, no cross-compat fallback | `ErrNoSuchRecipe` | "cats-core_3 not found; for3Use2_13 not enabled" |
| Cross-compat fallback also missing | `ErrNoSuchRecipe` | "Tried _3 then _2.13; both 404" |
| Both POM and Ivy XML missing | `ErrNoSuchRecipe` | Artifact in metadata.xml but no descriptor |
| Configuration mapping invalid | `ErrInternalBug` | Malformed Ivy XML |
| Snapshot version with caching disabled | (warning) | Coursier-style 24h TTL by default |
| build.sbt non-declarative content detected | User error | Suggest `--sbt-shell-api` fallback |
| Scala version mismatch in transitive | `ErrCapabilityConflict` | Two paths require incompatible Scala versions |

## 10. Security

### 10.1 Checksum verification

Same as Maven — SHA-1/256/512 verification on every fetch. PGP
signatures verified when present (Sonatype publishes signed
artifacts to Maven Central).

### 10.2 Cross-Scala-version supply-chain risks

The encoding-into-identity model means a malicious
`cats-core_2.13` and a legitimate `cats-core_3` are different
artifacts — typo-squatters can publish under variant suffixes.
Mitigations:

- FeedMapping for known organizations (`org.typelevel.*` only from
  Maven Central or Sonatype Snapshots)
- Cross-reference signed artifacts against known publisher PGP keys
- Surface "this is a new publisher you haven't trusted before"
  warnings

### 10.3 Snapshot TTL

Snapshot versions (`-SNAPSHOT` suffix) update on every server
deploy. Coursier defaults to 24-hour TTL to avoid network-bound
re-resolves; the substrate's adapter inherits this default and
exposes `--no-snapshot-cache` to force re-fetch.

## 11. Testing

### 11.1 Unit tests

- ScalaBinaryVersion parsing + comparison
- Suffix encoding/decoding round-trip for ~100 known artifacts
- Cross-compat fallback semantics
- Ivy XML parser on real-world Ivy modules (Typesafe artifacts)
- build.sbt declarative parser on 50 real projects
- Configuration mapping translation

### 11.2 Integration tests

- Resolve cats + cats-effect + cats-effect-std (Typelevel core)
- Resolve Akka full ecosystem
- Resolve a Scala 3 project using `for3Use2_13` for legacy deps
- Resolve a Scala.js project (cross-platform variants)
- Resolve a sbt-managed Scala 2/3 cross-build (two passes)
- Resolve a project with Ivy XML metadata

### 11.3 Ecosystem compat

50 real Scala projects, oracle is `coursier resolve` output. Match:
zero tolerance for tree shape, low tolerance for transitive
ordering.

### 11.4 Performance

Match Coursier's own benchmarks (which themselves clobber Ivy by
~4×):

- Resolve spark-sql full deps: <15s cold (Coursier: ~13s; Ivy: ~51s)
- Resolve typical Scala app: <3s cold, <300ms warm
- POM/Ivy parse: <50ms

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, typical Scala app | <3s | <2s |
| Cold resolve, spark-sql | <15s | <10s |
| Warm resolve | <300ms | <150ms |
| POM fetch + parse | <50ms | <25ms |
| Ivy XML fetch + parse | <70ms | <40ms |
| Materialization (reflink), 100 JARs | <1s | <500ms |
| Suffix encode/decode | <1μs | <500ns |
| Lockfile read+validate | <50ms | <25ms |

## 13. Phases

**M0.** Types, ScalaBinaryVersion, suffix encoder/decoder, build.sbt
declarative parser; unit tests pass.

**M1.** POM + Ivy XML parsing; metadata fetch from Maven Central
+ Sonatype.

**M2.** Full PubGrub resolution. Cross-compat fallback. Frontier
prefetch.

**M3.** Coursier-compat cache layout. sbt-coursier-lock codec.
Ecosystem compat green on 50 projects.

**M4.** Mill build tool support, sbt-shell-API fallback for
non-declarative build files, KotlinScript build files (build.sbt.kts
once Scala 3 sbt 2.x lands).

## 14. Open Questions

- **Scala 3 transition state.** Many libraries are mid-migration to
  Scala 3 with cross-builds. Some publish only Scala 2.13 and
  expect for3Use2_13. Default policy: try Scala 3 first, fall back
  to 2.13 with a warning.
- **Mill vs sbt parsing precedence.** Mill projects use
  `build.mill` with different syntax. Default: detect by presence
  of build.sbt vs build.mill; refuse if both present without
  explicit choice.
- **Cross-build matrix.** sbt's `++scala-version` recompiles
  against multiple Scala versions. Sylk resolves for one Scala
  version per invocation; cross-build is a higher-level orchestration
  concern (multiple `sylk resolve scala --scala-version=X` runs).
- **Bloop integration.** Bloop is a "compile server" that some
  Scala teams use for fast incremental builds. Outside resolver
  scope but the materializer's layout matters; ensure Bloop can
  consume.

## 15. Dependencies

- Substrate M2 (frontier, multi-feed) → adapter M2
- Substrate M3 (materializer, lockfile) → adapter M3
- `adapters/jvm_shared/pom` (extracted with Maven adapter) → M1

External Go dependencies:

- Custom Scala-syntax declarative parser (~1500 LOC)
- Custom Ivy XML parser (uses encoding/xml; ~500 LOC)
- POM parser shared with Maven adapter
