# JVM_MAVEN.md — Maven Adapter Implementation Plan

Tier 3 — first of the JVM cluster. Validates the substrate
accommodates **two alternative resolution semantics** in one
adapter: Maven-compatible nearest-wins mediation (default) and
PubGrub-based strict constraint satisfaction (opt-in). The adapter
must produce identical output to `mvn dependency:tree` in
compatibility mode so existing Maven-built JARs behave the same
way Sylk-managed JARs do.

## 1. Overview

The Maven adapter resolves and materializes artifacts from:

- **[Maven Central](https://repo.maven.apache.org/maven2/)** (default)
- **Private Maven repositories** (Sonatype Nexus, JFrog Artifactory,
  Cloudsmith, AWS CodeArtifact, Google Artifact Registry, GitHub
  Packages)
- **Local Maven repository** (`~/.m2/repository`) as a fallback cache
- **Relocated and released-only artifacts** (honor
  `<relocation>` directives)

Produces:

- A resolved dependency graph matching `mvn dependency:tree` output
  in default mode
- An updated project `pom.xml` (optional; typically untouched)
- A materialized `~/.m2/repository`-style tree (substrate-managed,
  layout-identical so external `mvn` tools can consume)
- A `sylk-lockfile.xml` or similar (Maven has no canonical lockfile;
  we introduce one)

User-visible behaviors (M3 target):

- `sylk resolve maven ./pom.xml` → produces resolved tree + lockfile
- `sylk install maven` → materializes the local repository tree
- `sylk why maven <groupId:artifactId>` → explains via traversal
  order or PubGrub derivation
- Strict mode opt-in: `sylk resolve maven --strict ./pom.xml` →
  uses PubGrub, fails on unsatisfied ranges rather than silently
  picking

Non-goals:

- Running Maven plugins (pom.xml build/plugins sections are read
  for metadata but not executed)
- Producing or modifying `~/.m2/settings.xml`
- Dependency convergence analysis beyond what the resolver
  produces (users can run `mvn dependency:analyze` separately)

## 2. Data Model

### 2.1 Coordinates

```go
type MavenCoordinate struct {
    GroupID    string   // "org.apache.commons"
    ArtifactID string   // "commons-lang3"
    Version    MavenVersion
    Classifier string   // optional: "sources", "javadoc", "jdk8", ""
    Packaging  string   // "jar" (default), "pom", "war", "aar", "ear", "bundle"
}

// Maven's string form: "groupId:artifactId:packaging:classifier:version"
// With classifier omitted: "groupId:artifactId:packaging:version"
// Most common: "groupId:artifactId:version"
func (m MavenCoordinate) String() string { ... }
```

### 2.2 MavenVersion

Maven versions are **not** strict SemVer. They're
[Maven-specific ordering](https://maven.apache.org/ref/3.9.6/maven-artifact/apidocs/org/apache/maven/artifact/versioning/ComparableVersion.html)
with quirks:

- `1.0-alpha-1` → `1.0-alpha-2` → `1.0-beta-1` → `1.0-RC-1` → `1.0` → `1.0-1`
- String comparison for qualifiers (with known aliases: `ga`/`final`/`release` → stable)
- `SNAPSHOT` suffix: `1.2.0-SNAPSHOT` is pre-release of 1.2.0
- Dash-separated qualifier tokens have semantic ordering
- Case-insensitive qualifier names

```go
type MavenVersion struct {
    Items []MavenVersionItem  // parsed components
    Raw   string               // original string for round-trip
}

type MavenVersionItem struct {
    Kind MavenItemKind  // Integer, String, ListItem
    IntValue int
    StrValue string
    Qualifier Qualifier  // when Kind=String, maps known qualifiers
    SubItems []MavenVersionItem  // for nested [...]
}

type Qualifier int

const (
    QualifierAlpha Qualifier = iota
    QualifierBeta
    QualifierMilestone
    QualifierRC
    QualifierSnapshot
    QualifierStable   // "", "ga", "final", "release"
    QualifierSP       // "sp" — service pack, considered stable+
)

func (v MavenVersion) Compare(other MavenVersion) int { ... }
```

Implementation: port
[ComparableVersion.java](https://github.com/apache/maven/blob/master/maven-artifact/src/main/java/org/apache/maven/artifact/versioning/ComparableVersion.java).
~300 LOC; behavior-exact match with Maven's Java implementation is
required for ecosystem compat.

### 2.3 Version ranges

Maven version ranges use interval notation:

- `[1.0]` — exactly 1.0 (rare — usually just `1.0` means "prefer 1.0")
- `[1.0,)` — ≥1.0, unbounded
- `[1.0,2.0)` — ≥1.0, <2.0
- `(,1.5]` — ≤1.5
- `[1.0],[2.0],[3.0]` — set of alternatives (all allowed)
- `1.0` (bare) — "soft requirement" meaning "prefer 1.0 if not
  otherwise constrained"

The soft-requirement semantics are unique to Maven and critical for
compatibility mode (most real pom.xml use soft requirements, not
ranges).

### 2.4 pom.xml

```go
type POM struct {
    ModelVersion string           // always "4.0.0"
    Parent       *ParentPOM       // optional parent inheritance
    GroupID      string
    ArtifactID   string
    Version      string
    Packaging    string           // default "jar"

    Properties   map[string]string
    DependencyManagement *DependencyManagement  // version centralization
    Dependencies []POMDependency
    BuildConfig  *BuildConfig     // <build> — read for repositories, plugins
    Repositories []POMRepository
    PluginRepos  []POMRepository
    Profiles     []POMProfile
}

type ParentPOM struct {
    GroupID      string
    ArtifactID   string
    Version      string
    RelativePath string   // default "../pom.xml"
}

type POMDependency struct {
    GroupID    string
    ArtifactID string
    Version    string
    Classifier string
    Type       string    // default "jar"
    Scope      MavenScope  // compile, runtime, test, provided, system, import
    Optional   bool
    Exclusions []POMExclusion
    SystemPath string      // when scope=system
}

type MavenScope int

const (
    ScopeCompile  MavenScope = iota  // default; on all classpaths
    ScopeRuntime                      // runtime + test classpaths
    ScopeTest                          // test classpath only
    ScopeProvided                      // compile + test, provided externally at runtime
    ScopeSystem                        // deprecated: explicit system path
    ScopeImport                        // pom-type imports for BOM
)

type DependencyManagement struct {
    Dependencies []POMDependency  // version pins, not actual deps
}
```

### 2.5 Parent inheritance

Maven's `<parent>` directive inherits properties, dependencies,
dependency management, plugin configuration, repositories, etc. The
resolver must:

1. Read the project's pom.xml
2. Locate its parent (by `<parent>` coords + `<relativePath>` or
   repository lookup)
3. Recursively read parent pom.xml and its parents until
   reaching root
4. **Effective pom**: merge child + parent via Maven's
   inheritance rules (children override parents; properties
   interpolate)
5. Feed the effective pom to the resolver

Maven's inheritance rules are extensive (each element has its own
merge semantics — some override, some append, some are child-only).
Reference: [Maven Model Builder](https://maven.apache.org/ref/3.9.6/maven-model-builder/).

Port the rules as a deterministic builder. ~1500 LOC.

### 2.6 BOM imports

`<scope>import</scope>` + `<type>pom</type>` dependencies import
the target pom's `<dependencyManagement>` block. Used for curated
BOMs (Spring Boot BOM, Jackson BOM, etc.) that declare known-
compatible version sets.

```xml
<dependencyManagement>
    <dependencies>
        <dependency>
            <groupId>org.springframework.boot</groupId>
            <artifactId>spring-boot-dependencies</artifactId>
            <version>3.2.0</version>
            <type>pom</type>
            <scope>import</scope>
        </dependency>
    </dependencies>
</dependencyManagement>
```

Import POMs are resolved to fetch the target pom, extract its
`<dependencyManagement>`, and merge into the consuming project's
dependency management. Supports nested imports (BOM-of-BOMs).

## 3. HTTP Transport

### 3.1 Repository layout

Maven repositories are static file trees:

```
{repo}/{groupId with slashes}/{artifactId}/{version}/{artifactId}-{version}[-{classifier}].{ext}
{repo}/{groupId with slashes}/{artifactId}/{version}/{artifactId}-{version}.pom
{repo}/{groupId with slashes}/{artifactId}/{version}/{artifactId}-{version}.jar
{repo}/{groupId with slashes}/{artifactId}/{version}/{artifactId}-{version}.jar.sha1
{repo}/{groupId with slashes}/{artifactId}/{version}/{artifactId}-{version}.jar.sha256  # newer
{repo}/{groupId with slashes}/{artifactId}/{version}/{artifactId}-{version}.jar.sha512  # newer
{repo}/{groupId with slashes}/{artifactId}/{version}/{artifactId}-{version}.jar.md5      # legacy
{repo}/{groupId with slashes}/{artifactId}/{version}/{artifactId}-{version}.jar.asc      # PGP signature

{repo}/{groupId with slashes}/{artifactId}/maven-metadata.xml
# Lists available versions, release/latest markers
```

`maven-metadata.xml`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<metadata>
    <groupId>org.apache.commons</groupId>
    <artifactId>commons-lang3</artifactId>
    <versioning>
        <latest>3.14.0</latest>
        <release>3.14.0</release>
        <versions>
            <version>1.0</version>
            ...
            <version>3.14.0</version>
        </versions>
        <lastUpdated>20231228152000</lastUpdated>
    </versioning>
</metadata>
```

### 3.2 HEAD-first version discovery

Maven repositories don't have a modern metadata API. Version
discovery is:

1. `GET {repo}/{groupId}/{artifactId}/maven-metadata.xml`
2. Parse the `<versions>` list
3. HEAD individual `{version}/{artifactId}-{version}.pom` to confirm
   existence (metadata.xml can be stale)

For performance, the adapter:

- Caches metadata.xml with Etag/Last-Modified
- Validates HEAD with bounded concurrency
- Prefers metadata.xml's `<release>` for "latest stable" queries

### 3.3 Repository chain

Like GOPROXY, Maven supports multiple repositories with priority
order:

```xml
<repositories>
    <repository>
        <id>internal</id>
        <url>https://nexus.corp/repository/maven-releases/</url>
    </repository>
    <repository>
        <id>central</id>
        <url>https://repo.maven.apache.org/maven2/</url>
    </repository>
</repositories>
```

Resolution order: try each repo in order; first hit wins. `~/.m2/settings.xml`
can declare additional repositories and mirror configurations that
rewrite which repo serves which artifact.

The adapter models each repository as a `FeedReference`. Mirror
configurations are a substrate-level feed rewrite rule (implemented
in `core/substrate/feeds`).

### 3.4 Authentication

`~/.m2/settings.xml` is the canonical credentials store:

```xml
<settings>
    <servers>
        <server>
            <id>internal</id>
            <username>myuser</username>
            <password>...</password>
        </server>
    </servers>
</settings>
```

Encrypted passwords (`{encryptedValue}`) are decrypted using
`~/.m2/settings-security.xml`'s master key. The adapter handles
this via the substrate's AuthResolver with a
`MavenSettingsCredentialProvider`.

Authorization flows:

- HTTP Basic (username + password) — most common
- Bearer tokens — Sonatype Nexus Pro, Artifactory
- Cloud identity (AWS/GCP/Azure) — for cloud-provider registries

## 4. Metadata Layer

### 4.1 POM XML parsing

Maven POM XML is strict XML 1.0 with a known schema (maven-4.0.0.xsd).

```go
func ParsePOM(data []byte, baseURL string) (*POM, error) { ... }
```

Use Go's `encoding/xml` — sufficient and well-tested. For the POM
subset (mostly fixed schema), no streaming parser needed; POM files
are typically <100 KB.

### 4.2 Property interpolation

POM fields can reference properties:

```xml
<properties>
    <spring.version>6.1.0</spring.version>
</properties>
<dependencies>
    <dependency>
        <groupId>org.springframework</groupId>
        <artifactId>spring-core</artifactId>
        <version>${spring.version}</version>
    </dependency>
</dependencies>
```

Plus built-in properties:

- `${project.version}` — this project's version
- `${project.groupId}` — etc.
- `${project.basedir}` — project directory
- `${project.build.sourceEncoding}`
- `${env.JAVA_HOME}` — environment variables
- `${settings.localRepository}` — from settings.xml
- `${java.version}` — JVM properties

Interpolation is **recursive**: a property's value can itself
reference properties. Implementation:

```go
func Interpolate(s string, props map[string]string) (string, error) {
    // Substitute ${...} recursively with cycle detection.
    // Return error on cycle or unresolved reference.
}
```

Apply interpolation after parent inheritance merge (properties
inherit from parents).

### 4.3 Effective POM construction

Sequence:

1. Parse child POM
2. Locate and parse parent POM (recursively to root)
3. Merge parent → child using Maven inheritance rules:
   - Most fields: child overrides parent
   - `<dependencyManagement>`: merge; child's entries win on conflicts
   - `<properties>`: merge; child wins
   - `<repositories>`: accumulate; no dedup
   - `<build><plugins>`: merge with plugin-management override rules
4. Apply BOM imports: for each `<scope>import</scope>`
   dependency in `<dependencyManagement>`, fetch the target pom and
   merge its dependencyManagement
5. Interpolate all properties

The result is the "effective POM" the resolver operates on. Maven's
`mvn help:effective-pom` command produces this; our output should
match.

### 4.4 Dependency management

`<dependencyManagement>` entries are **version hints**, not
dependencies. When a direct or transitive `<dependency>` omits
`<version>`, the version comes from the dependencyManagement
lookup. Effectively: BOM imports + explicit pins centralize
versions.

The resolver uses dependencyManagement for version selection, not
as edges in the dependency graph. Edges come from `<dependencies>`.

## 5. Resolver

### 5.1 Compatibility mode (nearest wins + breadth-first)

Default. Matches `mvn dependency:tree` output exactly. Algorithm:

```go
func (r *mavenResolver) resolveCompatibility(ctx context.Context, root *POM) (*DependencyTree, error) {
    // Breadth-first traversal. Maven 4 default; more predictable
    // than Maven 3's depth-first.
    queue := []DependencyEntry{}
    // Seed with root's direct dependencies.
    for _, dep := range root.ResolvedDependencies() {
        queue = append(queue, DependencyEntry{
            Parent: nil,
            Dep: dep,
            Depth: 1,
        })
    }

    // Selected map: (groupId, artifactId, classifier) -> chosen version + depth.
    selected := map[ArtifactKey]SelectedEntry{}

    for len(queue) > 0 {
        entry := queue[0]
        queue = queue[1:]

        key := entry.Dep.Key()
        prev, exists := selected[key]
        if exists {
            // Nearest-wins: if we got here at shallower depth, keep existing.
            // If same depth, declared-first wins (queue ordering preserves).
            if entry.Depth >= prev.Depth {
                continue
            }
        }

        // Apply dependency management if version unset.
        version := entry.Dep.Version
        if version == "" {
            version = root.LookupDependencyManagement(key)
        }

        // Apply exclusions from ancestor path.
        if entry.IsExcluded() {
            continue
        }

        // Select this version.
        selected[key] = SelectedEntry{
            Coord: MavenCoordinate{GroupID: key.GroupID, ArtifactID: key.ArtifactID, Version: version, Classifier: key.Classifier},
            Depth: entry.Depth,
            Dep: entry.Dep,
        }

        // Fetch its POM and enqueue its dependencies (with depth + 1, scope-filtered).
        childPOM, err := r.fetchPOM(ctx, /* this version */)
        for _, childDep := range childPOM.ResolvedDependencies() {
            if !childDep.Scope.TransitiveFrom(entry.Dep.Scope) {
                continue  // e.g., test-scope deps don't transitively propagate
            }
            queue = append(queue, DependencyEntry{
                Parent: &entry,
                Dep: childDep,
                Depth: entry.Depth + 1,
            })
        }
    }

    return buildTreeFromSelected(selected), nil
}
```

Scope transitivity rules (Maven's table):

| Direct scope → | compile | provided | runtime | test |
|---|---|---|---|---|
| Transitive compile | compile | provided | runtime | (not propagated) |
| Transitive provided | (not) | (not) | (not) | (not) |
| Transitive runtime | runtime | provided | runtime | (not) |
| Transitive test | (not) | (not) | (not) | (not) |

I.e., test and provided scopes don't propagate.

### 5.2 Exclusions

Maven supports per-dependency exclusions:

```xml
<dependency>
    <groupId>A</groupId>
    <artifactId>X</artifactId>
    <exclusions>
        <exclusion>
            <groupId>B</groupId>
            <artifactId>Y</artifactId>
        </exclusion>
    </exclusions>
</dependency>
```

This excludes `B:Y` **only from the subtree rooted at A:X**.
Other paths to B:Y (via different parents) are still included.
Implementation: track an "excluded set" per dependency entry,
inherited from the parent chain.

Wildcard exclusions (`*` for groupId or artifactId) are legal and
must be supported.

### 5.3 Strict mode (PubGrub)

Opt-in. The resolver runs substrate's PubGrub with:

- `AvailableVersions` reads the maven-metadata.xml + version
  filtering
- `Dependencies` reads the POM + scope filtering + exclusion
  application
- Version range constraints are properly honored (ranges like
  `[1.0,2.0)` must actually constrain, not just suggest)
- Conflicting ranges fail loudly with a PubGrub-derived
  explanation

Strict mode produces **different resolutions** than compat mode
for any project whose transitive closure has declared version
ranges. The strict resolver might pick a newer version because
ranges allow it, while compat mode picks what's "nearest." Users
opt in knowing this.

### 5.4 Frontier integration

Compat mode: **no frontier** (breadth-first traversal is
deterministic; no backtracking). Do not implement
`FrontierAwareResolver` in compat mode.

Strict mode: **implements FrontierAwareResolver**. PubGrub's
candidate-consideration events drive the substrate's prefetch
coordinator, which fetches POM files speculatively.

This is the first adapter with two resolution shapes and
correspondingly different substrate integration per mode.

### 5.5 Relocation

A POM can declare that the artifact has moved:

```xml
<distributionManagement>
    <relocation>
        <groupId>new.group</groupId>
        <artifactId>new-artifact</artifactId>
        <version>1.0.0</version>
        <message>Moved to new.group due to renaming</message>
    </relocation>
</distributionManagement>
```

The resolver follows relocations transparently: when fetching A:B
produces a relocation POM, it substitutes the target and surfaces
the relocation message as a warning.

## 6. Materializer

### 6.1 Local repository layout

Produce `~/.m2/repository`-compatible structure:

```
{cache-root}/
  org/
    apache/
      commons/
        commons-lang3/
          3.14.0/
            commons-lang3-3.14.0.pom
            commons-lang3-3.14.0.jar
            commons-lang3-3.14.0.jar.sha1
            commons-lang3-3.14.0.jar.sha256
            _remote.repositories
```

`_remote.repositories` records which repository each artifact came
from (Maven's convention for cache provenance). The substrate
stores additional metadata in a sidecar SQLite database (integrity
records, first-fetch timestamp, usage count) without polluting the
Maven-compatible layout.

### 6.2 Artifact download + verification

For each resolved coordinate:

1. Fetch .pom (authoritative metadata; verified separately)
2. Fetch .jar (or .aar, .war, depending on packaging)
3. Fetch checksums (.sha1 mandatory; .sha256/.sha512 preferred if
   available)
4. Verify checksum — any mismatch aborts with
   `ErrIntegrityMismatch`
5. Fetch PGP signature (.asc) if feed requires signature
   verification
6. Verify signature against Maven Central's PGP keyring

### 6.3 Classifier and packaging handling

Classifiers (`sources`, `javadoc`, custom) produce additional
artifacts with the same (groupId, artifactId, version) but
different file names. The materializer fetches classifier
artifacts on demand — only when explicitly requested.

Packaging types determine the artifact file extension:

- `jar` (default) → `.jar`
- `pom` → `.pom` only (no binary artifact)
- `war` → `.war`
- `aar` → `.aar` (Android archives)
- `ear` → `.ear`
- `bundle` → `.jar` with OSGi manifest

The resolver handles `pom`-packaging specially: these are
metadata-only artifacts (typically BOMs); no jar fetch needed.

### 6.4 Link mode

Materializer creates reflink/hardlink from substrate recipe store
into the Maven-compatible cache layout. Same pattern as other
adapters.

## 7. Lockfile

### 7.1 The lockfile gap

Maven has no canonical lockfile. Sylk must introduce one. Options:

**Option A: `sylk-lockfile.xml`** — XML format matching Maven's
style. Pros: feels native; tooling-friendly. Cons: not Maven-
compatible out of the box.

**Option B: `maven-lockfile.json`** — matches the external
[maven-lockfile plugin](https://github.com/chains-project/maven-lockfile)
format. Pros: potential interop. Cons: third-party format, less
stable.

**Option C: Sylk's internal lockfile format** — substrate's own
format, same across all adapters. Pros: consistent. Cons: not
Maven-native.

**Recommendation: Option C for M3**, with Option B as opt-in
export format. Use substrate canonical lockfile for internal
round-trip; emit maven-lockfile-compatible JSON as a user-facing
artifact on demand.

### 7.2 Hard-preference semantics

Per substrate convention. Existing lockfile pins are honored
unless they no longer satisfy project pom.xml constraints. This is
a policy layer **on top of** Maven's native nearest-wins: the
lockfile provides reproducibility; Maven's own semantics provide
resolution.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Used in strict mode only |
| `core/substrate/http` | All POM / JAR / metadata.xml fetches |
| `core/substrate/cache/metadata` | Per-artifact maven-metadata.xml caching |
| `core/substrate/store/recipe` | Shared artifact storage with Maven-compat layout |
| `core/substrate/materializer` | Reflink/hardlink to project-local repo |
| `core/substrate/lockfile` | Substrate-canonical + maven-lockfile export |
| `core/substrate/feeds` | Multi-repository + mirror rewrites |
| `core/substrate/auth` | settings.xml credentials + encrypted password support |
| `core/substrate/frontier` | Used in strict mode only |
| `core/substrate/pgp` | .asc signature verification |

Adapter modules under `adapters/maven/`:

- `coordinate.go` — MavenCoordinate
- `version.go` — MavenVersion + ComparableVersion port
- `ranges.go` — version range parser
- `pom.go` — POM XML parser
- `inheritance.go` — effective POM builder (parent merging)
- `interpolate.go` — property interpolation
- `bom.go` — BOM import handling
- `metadata.go` — maven-metadata.xml parser
- `compat_resolver.go` — nearest-wins + breadth-first
- `strict_resolver.go` — PubGrub integration
- `exclusions.go` — per-dep exclusion tracking
- `scope.go` — scope transitivity rules
- `relocation.go` — relocation following
- `provider.go` — PubGrub DependencyProvider (strict mode)
- `materializer.go` — Maven-compatible local repo layout
- `checksums.go` — SHA-1/256/512 + MD5 verification
- `signatures.go` — PGP signature verification
- `settings.go` — settings.xml parser + encrypted password decrypt
- `lockfile.go` — both canonical and maven-lockfile codecs
- `adapter.go` — top-level Resolver

Estimated LOC: ~6,500. Complexity drivers:

- Parent inheritance builder (~1500 LOC)
- Property interpolation with cycle detection (~200 LOC)
- Two resolvers (compat + strict) (~2000 LOC)
- Maven version comparison (~300 LOC)
- settings.xml + encrypted passwords (~400 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| POM 404 on all repos | `ErrNoSuchRecipe` | List repos tried |
| Version listed in metadata.xml but POM missing | `ErrIntegrityMismatch` | Inconsistent mirror |
| Checksum mismatch | `ErrIntegrityMismatch` | Fatal |
| PGP signature invalid | `ErrSignatureFailed` | Fatal when signing required |
| Version range unsatisfiable (strict mode) | `ErrNoSatisfyingVersion` | PubGrub explanation |
| Parent POM recursion depth exceeded | `ErrCycleDetected` | Malformed inheritance |
| Property interpolation cycle | `ErrCycleDetected` | Bad POM |
| BOM import not found | `ErrNoSuchRecipe` | Surface which BOM |
| Relocation loop | `ErrCycleDetected` | A relocates to B relocates to A |
| Unknown packaging type | `ErrNoSuchRecipe` | Unsupported extension |
| Encrypted password with missing master key | Auth error | Clear settings-security.xml guidance |

## 10. Security

### 10.1 Checksum verification

- Prefer SHA-512 when available (newer repositories publish)
- Fall back to SHA-256, then SHA-1
- **MD5 is warning-only** (Maven Central still publishes but it's
  cryptographically broken; the adapter verifies but warns if MD5
  is the strongest available)

### 10.2 PGP signature verification

Maven Central artifacts are signed with PGP; `.asc` files
accompany each artifact. The adapter verifies signatures against a
local PGP keyring. Keys are fetched from
`https://keyserver.ubuntu.com/` or similar public keyservers on
demand.

Policy:

- **Default**: verify when `.asc` exists; warn if missing
- **Strict**: require `.asc` for every artifact; fail if missing
- **Disabled**: skip verification (not recommended)

### 10.3 Private repository auth

settings.xml passwords can be encrypted per Maven's scheme.
Implementation:

- Parse `~/.m2/settings-security.xml` for master password
- Decrypt `{encryptedValue}` entries using Maven's CBC-AES
  algorithm
- Never log decrypted passwords
- Scope tokens to the configured server host

### 10.4 Supply chain

- [OSS Index](https://ossindex.sonatype.org/) for vulnerability
  data
- [OSV](https://osv.dev/) as secondary source
- Surface known-vulnerable versions as `CapabilityConflict` with
  severity
- Check Maven Central for yanked-like state (rare in Maven, but
  some orgs publish "deprecated" metadata)

## 11. Testing

### 11.1 Unit tests

- MavenVersion comparison corpus from Apache Maven's own test
  suite (exported verbatim)
- Version range parser
- POM XML parser on 100+ real POMs
- Effective POM builder on 50+ multi-parent hierarchies
- Property interpolation with recursive references
- BOM import with nested BOMs
- Scope transitivity matrix
- Exclusion propagation

### 11.2 Integration tests

- Resolve Spring Boot 3.x (huge dependency tree, BOM-heavy)
- Resolve Jackson family (tight BOM coordination)
- Resolve a project with multi-level parent inheritance
- Resolve a project with mirror configuration rewriting central
- Resolve a project with encrypted settings.xml credentials
- PGP signature verification against real Maven Central
  signatures
- Strict mode: resolve a project with declared version ranges,
  compare to compat mode output

### 11.3 Ecosystem compatibility

Golden corpus of 50 Maven projects. `mvn dependency:tree` output
is the oracle. Our compat mode must match **byte-identical** tree
shape and version selection. Tolerance:

- Zero for tree shape / version selection
- Zero for scope assignments
- Low for output formatting (we don't emit mvn's exact text
  format; we emit our canonical lockfile + a human-readable tree
  view)

### 11.4 Performance

- Resolve Spring Boot starter (typical enterprise app): <5s cold,
  <1s warm
- Resolve Jackson BOM-based project: <3s cold
- Effective POM build with 5-level inheritance: <100ms
- PGP signature verification: <100ms per artifact (amortized;
  key fetch is the slow part)

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, typical Spring Boot app | <5s | <3s |
| Warm resolve, same | <1s | <500ms |
| POM fetch + parse (cache miss) | <100ms | <50ms |
| POM fetch (cache hit, 304) | <15ms | <5ms |
| Effective POM (5-level inheritance) | <100ms | <50ms |
| Materialization, 100 artifacts (reflink) | <1s | <500ms |
| Checksum verify (SHA-256, 10 MB JAR) | <50ms | <25ms |
| PGP verify (per artifact, warm keyring) | <100ms | <50ms |
| Peak memory, 500-artifact resolve | <250MB | <150MB |

## 13. Phases

**M0.** Types compile; MavenVersion + ComparableVersion port;
POM parser + effective POM builder; unit tests green.

**M1.** Repository client works against Maven Central. Single-
artifact fetch with checksum verification. maven-metadata.xml
parsing.

**M2.** Compat-mode resolver end-to-end for a small project.
Scope transitivity correctly filters. Exclusions work. BOM imports
integrate.

**M3.** Strict mode (PubGrub) also works. Full materializer with
Maven-compatible layout. Lockfile codec. 50 ecosystem-compat
projects green. PGP signature verification default-on. Performance
targets met.

**M4.** Vulnerability advisory integration. Mirror/proxy
configuration fully honored. Encrypted settings.xml passwords.
Production polish.

## 14. Open Questions

- **Maven 3 vs Maven 4 semantics.** Maven 4 shipped breadth-first
  collector as default; Maven 3 uses depth-first. Compat mode
  should match Maven 4 for forward-looking projects but this
  differs from ~20 years of Maven 3 behavior. Proposal: default
  to Maven 4 semantics; provide `--maven3-compat` opt-in.
- **Version range interpretation edge cases.** Maven's
  ComparableVersion has quirks around qualifier ordering that are
  under-specified. Port the exact Java code and accept the
  behavior, even when unintuitive.
- **BOM cycles.** BOMs can import other BOMs, up to arbitrary depth.
  Cycle detection required; hard-fail with explanation.
- **Settings-security master key discovery.** Default locations
  vary by OS. Proposal: check the documented default; allow
  override via env var.
- **Plugin dependencies.** Maven plugins (compiler, surefire, etc.)
  have their own dependency trees. The adapter ignores them by
  default (plugins are build tooling, not application deps).
  Users needing plugin-dep resolution: out of scope for M3;
  revisit later.
- **Maven timestamped snapshots.** SNAPSHOT versions are rewritten
  server-side to `1.0-20240101.120000-5` per deploy. The adapter
  resolves SNAPSHOT → timestamped version on first fetch and pins
  in lockfile. Handle Maven 3 vs 4 subtleties around TIMESTAMP
  handling.

## 15. Dependencies

- Substrate M1 → adapter M1
- Substrate M2 (multi-feed, frontier for strict mode) → adapter M2
- Substrate M3 (materializer, PGP verifier, lockfile framework) →
  adapter M3

External Go dependencies:

- Custom Maven version comparator (port of ComparableVersion.java;
  no off-the-shelf Go library matches semantics)
- `golang.org/x/crypto/openpgp` — PGP signature verification
- `encoding/xml` — stdlib POM parser; sufficient

No dependency on other adapters. Shares substrate primitives with
Gradle and Scala/Coursier adapters.
