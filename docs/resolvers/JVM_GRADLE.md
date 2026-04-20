# JVM_GRADLE.md — Gradle Adapter Implementation Plan

Tier 3 — second JVM adapter, architecturally distinct from Maven.
Gradle consumes the same Maven Central protocol at the transport
layer but uses a **variant-aware resolver** with attribute matching,
**Gradle Module Metadata** (JSON extending POM), **capability
conflict detection**, and **per-configuration lockfiles**. This
adapter must serve Android projects (largest JVM consumer ecosystem),
Kotlin Multiplatform projects, and modern Java projects using
Gradle.

## 1. Overview

The Gradle adapter resolves and materializes artifacts from:

- **Maven Central** and **[Gradle Plugin Portal](https://plugins.gradle.org/)** (default)
- **Private Maven-compatible repositories** (same as Maven adapter)
- **Google's Maven repository** (for Android artifacts)
- **Included builds** (composite build substitution)
- **Flat directory repositories** (`flatDir { dirs 'libs' }`)

Produces:

- A resolved dependency graph selected by attribute-based variant
  matching
- An updated project `build.gradle` or `build.gradle.kts` (optional)
- Per-configuration `gradle.lockfile` (one per classpath
  configuration)
- A materialized artifact cache layout compatible with Gradle's own
  (`~/.gradle/caches/modules-2`)

User-visible behaviors (M3 target):

- `sylk resolve gradle ./build.gradle[.kts]` → lockfiles per
  configuration
- `sylk install gradle` → materialized artifact cache
- `sylk add gradle implementation <coord>` → modifies build script
- `sylk upgrade gradle` → re-resolves all configurations
- `sylk why gradle <coord>` → dependency insight + variant match
  explanation

Non-goals:

- Running Gradle tasks (compile, test, etc.)
- Evaluating the full Gradle DSL (we parse declarative subsets;
  full evaluation requires invoking Gradle itself)
- Gradle Wrapper (`gradlew`) management

## 2. Data Model

### 2.1 Coordinates

```go
type GradleCoordinate struct {
    Group     string   // Maven groupId equivalent
    Module    string   // Maven artifactId equivalent
    Version   MavenVersion  // shared with Maven adapter
    Variant   *VariantSelection  // which variant was chosen
    Classifier string  // still supported for pom-based artifacts
}

// VariantSelection is Gradle's variant-aware concept. Each coordinate
// can have multiple variants (e.g., JVM 8 API, JVM 11 runtime, JS,
// Android); the resolver picks one based on attribute matching.
type VariantSelection struct {
    Name       string              // variant name ("apiElements", "runtimeElements", ...)
    Attributes AttributeSet
}

type AttributeSet struct {
    Values map[string]AttributeValue  // key like "org.gradle.jvm.version"
}

type AttributeValue struct {
    StringValue string
    IntValue    *int  // some attributes are integers
}
```

### 2.2 Gradle Module Metadata

Gradle's JSON metadata format (`{module}-{version}.module`) is
served alongside POMs. When present, it's the **authoritative** source
(more expressive than POM). The adapter reads both; Module Metadata
wins for variant information.

```go
type GradleModuleMetadata struct {
    FormatVersion string           // "1.1"
    Component     ModuleComponent
    CreatedBy     ModuleCreatorInfo
    Variants      []ModuleVariant
}

type ModuleComponent struct {
    Group  string
    Module string
    Version string
    URL    string  // usually the pom URL; for consistency checks
}

type ModuleVariant struct {
    Name         string
    Attributes   AttributeSet
    Dependencies []ModuleVariantDependency
    DependencyConstraints []ModuleVariantConstraint
    Files        []ModuleVariantFile
    Capabilities []ModuleVariantCapability
    AvailableAt  *ModuleAvailableAt  // redirect to another variant
}

type ModuleVariantDependency struct {
    Group      string
    Module     string
    Version    VersionRequirement
    Attributes AttributeSet  // variant-scoped attribute overrides
    Excludes   []ModuleExclude
    Reason     string   // human-readable justification
    Requested  bool     // true if directly requested; false if transitive
    Endorse    bool     // inherit variant constraints
}

type ModuleVariantConstraint struct {
    Group   string
    Module  string
    Version VersionRequirement
    Reason  string
}

type VersionRequirement struct {
    Requires string  // hard requirement (range or pin)
    Prefers  string  // soft preference
    Strictly string  // strict version; conflicts with higher elsewhere cause failure
    Rejects  []string // version ranges to reject
}

type ModuleVariantFile struct {
    Name   string
    URL    string
    Size   int64
    SHA1   string
    SHA256 string
    SHA512 string
    MD5    string
}

type ModuleVariantCapability struct {
    Group   string
    Name    string
    Version string  // optional
}
```

### 2.3 Gradle build script

Two flavors: Groovy DSL (`build.gradle`) and Kotlin DSL
(`build.gradle.kts`). Full evaluation requires the Gradle tooling
API; the adapter reads a **declarative subset** statically:

```go
type GradleBuildScript struct {
    Plugins          []GradlePlugin
    Repositories     []GradleRepository
    Dependencies     GradleDependencies
    DependencyConstraints []GradleDependencyConstraint
    JavaVersion      int  // from java { sourceCompatibility }
    KotlinVersion    string
    AndroidConfig    *AndroidBlock  // for Android projects
}

type GradleDependencies struct {
    // Keyed by configuration name (implementation, api, testImplementation, etc.)
    Configurations map[string][]GradleDependency
}

type GradleDependency struct {
    Group      string
    Module     string
    Version    VersionRequirement
    Classifier string
    Excludes   []GradleExclude
    Capabilities []GradleCapabilityRequest
    Attributes map[string]string  // attribute constraints
    Force      bool  // legacy: force this version
}
```

Static parsing handles ~90% of real projects. The remaining 10%
(programmatic dependency declarations, dynamic version selection,
plugin-provided deps) require invoking Gradle. Proposal: static
parsing for M3; optional `--gradle-tooling-api` fallback for
complex projects in M4.

### 2.4 Configurations

Gradle classpaths are organized into named **configurations**:

- `implementation` — compile + runtime, not transitive to consumers
- `api` — compile + runtime, transitive to consumers
- `compileOnly` — compile only, not runtime
- `runtimeOnly` — runtime only, not compile
- `testImplementation` — test compile + runtime
- ... (many more, configurable)

Each configuration has its own resolution. The adapter must
produce one resolved graph per configuration. Configurations
inherit from each other (`testImplementation` extends
`implementation`), so often graphs overlap significantly.

## 3. HTTP Transport

### 3.1 Repository layout

Same as Maven — static file tree. Additionally, Gradle Module
Metadata lives at:

```
{repo}/{group-with-slashes}/{module}/{version}/{module}-{version}.module
```

(JSON file next to the POM.)

### 3.2 Repository types

Gradle supports more repository types than Maven:

- **`mavenCentral()`** — Maven Central
- **`google()`** — Google's Maven repository
  (https://maven.google.com/) for Android
- **`gradlePluginPortal()`** — plugins.gradle.org
- **`maven { url "..." }`** — arbitrary Maven-compatible repo
- **`ivy { url "..." }`** — Ivy-format repositories
- **`flatDir { dirs "libs" }`** — directory of JARs without
  metadata
- **`mavenLocal()`** — `~/.m2/repository` as a source

Ivy repositories use a different path convention:
`[organisation]/[module]/[revision]/[artifact]-[revision](-[classifier]).[ext]`.
The adapter must support Ivy layouts when the project uses them.

### 3.3 Metadata source declaration

Gradle lets projects declare which metadata formats to accept:

```kotlin
repositories {
    mavenCentral {
        metadataSources {
            gradleMetadata()
            mavenPom()
            artifact()
            ignoreGradleMetadataRedirection()
        }
    }
}
```

The adapter honors this declaration:

- Prefer Gradle Module Metadata when both are available
- Fall back to POM when only POM exists
- Fall back to "artifact only" (no metadata) when both are absent
- Follow `.module` files' `formatVersion` field strictly

### 3.4 Authentication

Same as Maven — `~/.m2/settings.xml` credentials + per-repo auth
in Gradle build scripts:

```kotlin
repositories {
    maven {
        url = uri("https://nexus.corp/repository/maven-releases/")
        credentials {
            username = System.getenv("NEXUS_USERNAME")
            password = System.getenv("NEXUS_PASSWORD")
        }
    }
}
```

Credentials can come from:

- env vars
- `~/.gradle/gradle.properties` (`nexusUsername=...`,
  `nexusPassword=...`)
- project-local `gradle.properties`
- keyring integrations (macOS Keychain, GNOME Keyring)

## 4. Metadata Layer

### 4.1 Gradle Module Metadata parsing

```go
func ParseModuleMetadata(data []byte) (*GradleModuleMetadata, error) {
    var m GradleModuleMetadata
    if err := json.Unmarshal(data, &m); err != nil {
        return nil, fmt.Errorf("invalid .module file: %w", err)
    }
    // Validate format version.
    if m.FormatVersion != "1.1" && m.FormatVersion != "1.0" {
        return nil, fmt.Errorf("unsupported format version: %s", m.FormatVersion)
    }
    return &m, nil
}
```

Well-defined JSON schema; parsing is straightforward. Size: typically
10–50 KB per artifact.

### 4.2 POM fallback

When Module Metadata is absent, the adapter falls back to POM.
POM-based artifacts are treated as having a single default variant
with implicit attributes:

```go
func POMToVariants(pom *POM) []ModuleVariant {
    return []ModuleVariant{
        {
            Name: "compile",
            Attributes: AttributeSet{Values: map[string]AttributeValue{
                "org.gradle.category": {StringValue: "library"},
                "org.gradle.usage": {StringValue: "java-api"},
                "org.gradle.libraryelements": {StringValue: "jar"},
            }},
            Dependencies: translateCompileDeps(pom),
            Files: []ModuleVariantFile{{Name: fmt.Sprintf("%s-%s.jar", pom.ArtifactID, pom.Version), URL: ...}},
        },
        {
            Name: "runtime",
            Attributes: AttributeSet{Values: map[string]AttributeValue{
                "org.gradle.category": {StringValue: "library"},
                "org.gradle.usage": {StringValue: "java-runtime"},
                "org.gradle.libraryelements": {StringValue: "jar"},
            }},
            Dependencies: translateRuntimeDeps(pom),
            Files: []ModuleVariantFile{...},
        },
    }
}
```

This makes POM-only artifacts consumable by Gradle's variant
matcher as if they had declared minimal Module Metadata.

### 4.3 Attribute schema

Gradle's core attribute schema:

| Attribute | Type | Values |
|---|---|---|
| `org.gradle.category` | String | `library`, `platform`, `regular-platform`, `enforced-platform`, `documentation`, `verification` |
| `org.gradle.usage` | String | `java-api`, `java-runtime`, `native-link`, `native-runtime`, ... |
| `org.gradle.libraryelements` | String | `jar`, `classes`, `resources`, `classes-and-resources`, ... |
| `org.gradle.dependency.bundling` | String | `external`, `embedded`, `shadowed` |
| `org.gradle.jvm.version` | Integer | `8`, `11`, `17`, `21`, ... |
| `org.gradle.jvm.environment` | String | `standard-jvm`, `android` |
| `org.gradle.docstype` | String | `javadoc`, `sources` |

Android and Kotlin Multiplatform add their own:

- `com.android.build.api.attributes.BuildTypeAttr`
- `com.android.build.api.attributes.ProductFlavor:XXX`
- `org.jetbrains.kotlin.platform.type` — `jvm`, `js`, `native`,
  `common`, `androidJvm`
- `org.jetbrains.kotlin.native.target` — `ios_arm64`, etc.

The adapter supports arbitrary custom attributes (ecosystems can
extend the schema); matching/disambiguation rules come from the
Module Metadata itself when declared.

## 5. Resolver

### 5.1 Variant-aware resolution

The resolver is conceptually a constraint solver over two
interacting domains:

1. **Version resolution** — pick a version for each module
   satisfying constraints
2. **Variant selection** — pick a variant per module satisfying
   attribute matching

These interact: a variant declares dependencies and constraints
that affect version resolution; version resolution determines which
module instances are candidates for variant matching.

```go
type GradleResolver struct {
    root          *GradleProject
    configuration string  // which configuration we're resolving
    fetcher       *gradleFetcher
    conflictStrategy ConflictStrategy
}

func (r *GradleResolver) Resolve(ctx context.Context, req substrate.ResolveRequest) (substrate.ResolveResult, error) {
    // 1. Parse root project's dependencies for the target configuration.
    // 2. For each dependency, fetch metadata (prefer .module; fallback POM).
    // 3. Run PubGrub-like version resolution with Gradle-specific extensions:
    //    - Version requirement semantics (requires/prefers/strictly/rejects)
    //    - Conflict strategies (failOnVersionConflict, latest, strict)
    // 4. For each resolved module, select a variant via attribute matching.
    // 5. Detect capability conflicts across selected variants.
    // 6. Apply dependency constraints (influence without requiring).
}
```

### 5.2 Attribute matching algorithm

Given a consumer's requested attributes and a producer's available
variants, pick the variant whose attributes are
**compatible** and **most specific**.

```go
func MatchVariant(consumer AttributeSet, variants []ModuleVariant, schema AttributeSchema) (*ModuleVariant, error) {
    // 1. Filter variants that are compatible with consumer attrs.
    //    Compatibility rules per attribute type (exact, exact-or-undeclared,
    //    custom compatibility rules registered in schema).
    // 2. Of compatible variants, pick the "best" using disambiguation rules:
    //    - Exact match preferred over compatible-but-not-exact.
    //    - Consumer attributes declared > not declared.
    //    - Tie-break via attribute-specific disambiguation rules.
    // 3. If tied after disambiguation, error with
    //    "Multiple variants match; add explicit attribute X to disambiguate."
}
```

Schema-based disambiguation rules (from `.module` metadata or
project config):

```json
{
  "attributes": {
    "org.gradle.jvm.version": {
      "compatibility": "smaller-or-equal",  // consumer asks for 17; producer with 11 compatible
      "disambiguation": "closest-match"      // of compatible, pick largest ≤ consumer
    }
  }
}
```

### 5.3 Version requirement semantics

Gradle version requirements have four semantic modes:

- **`requires`** — hard requirement; resolution fails if not met
- **`prefers`** — soft preference; used to break ties but not
  enforced
- **`strictly`** — strict requirement; conflicts with higher
  versions elsewhere cause failure
- **`rejects`** — veto list; versions matching these ranges are
  excluded

```go
func (vr VersionRequirement) Satisfies(v MavenVersion) bool {
    if len(vr.Rejects) > 0 {
        for _, rejectRange := range vr.Rejects {
            if matchesRange(v, rejectRange) { return false }
        }
    }
    if vr.Strictly != "" {
        return matchesRange(v, vr.Strictly)
    }
    if vr.Requires != "" {
        return matchesRange(v, vr.Requires)
    }
    return true  // only prefers = soft; any version allowed
}
```

### 5.4 Capability conflict detection

Capabilities are a separate identity dimension:

```json
"variants": [{
    "name": "runtimeElements",
    "capabilities": [
        { "group": "commons-logging", "name": "commons-logging", "version": "1.0" }
    ],
    ...
}]
```

Multiple modules can declare the same capability (e.g.,
`jcl-over-slf4j` declares `commons-logging:commons-logging`
capability; original `commons-logging` also declares it). Only one
can be in the resolved graph.

```go
func (r *GradleResolver) detectCapabilityConflicts(resolved []ResolvedVariant) []CapabilityConflict {
    caps := map[CapabilityKey][]ResolvedVariant{}
    for _, rv := range resolved {
        for _, cap := range rv.Variant.Capabilities {
            key := CapabilityKey{Group: cap.Group, Name: cap.Name}
            caps[key] = append(caps[key], rv)
        }
    }
    conflicts := []CapabilityConflict{}
    for key, providers := range caps {
        if len(providers) > 1 {
            conflicts = append(conflicts, CapabilityConflict{
                Capability: key,
                Providers: providers,
                Resolution: "none",  // user must pick via capabilitiesResolution block
            })
        }
    }
    return conflicts
}
```

Capability conflicts are **fatal by default**. Users resolve via
explicit capability resolution in build.gradle:

```kotlin
dependencies {
    modules {
        module("commons-logging:commons-logging") {
            replacedBy("org.slf4j:jcl-over-slf4j", "Use SLF4J bridge instead")
        }
    }
}
```

### 5.5 Conflict resolution strategies

Per-configuration conflict strategies:

```go
type ConflictStrategy int

const (
    StrategyLatest ConflictStrategy = iota  // newest wins (default)
    StrategyFailOnConflict                   // any conflict fails
    StrategyPreferProjectModules             // local workspace wins
)
```

Per-dependency strict versions + rejects provide finer control
without changing the global strategy.

### 5.6 Dependency constraints

Constraints are separate from dependencies — they influence
version selection without adding to the graph:

```kotlin
dependencies {
    constraints {
        implementation("org.apache.logging.log4j:log4j-core:2.17.0+") {
            because("CVE-2021-44228")
        }
    }
}
```

If log4j-core is pulled transitively, its version must satisfy
`2.17.0+`. If nothing pulls log4j-core, the constraint has no
effect.

Substrate's `Constraint` type already supports this via a separate
flag; the adapter adds constraint entries to the PubGrub problem
without including them as edges.

### 5.7 Frontier integration

Gradle's resolver is PubGrub-with-variant-layer. The version
resolution phase exposes a frontier; the variant-selection phase
doesn't (it's a post-hoc pick over resolved modules).

Implement `FrontierAwareResolver` with the PubGrub phase's frontier
driving the substrate's prefetch coordinator. Variant selection
happens after resolution converges, so no frontier is needed there.

## 6. Materializer

### 6.1 Gradle cache layout

Gradle's canonical cache layout at `~/.gradle/caches/modules-2/`:

```
~/.gradle/caches/modules-2/
  files-2.1/
    {group}/
      {module}/
        {version}/
          {sha1}/
            {module}-{version}.jar
          {sha1}/
            {module}-{version}.pom
          {sha1}/
            {module}-{version}.module
  metadata-2.X/
    descriptors/                 # Gradle's internal metadata cache
```

Files are stored under SHA-1 directories keyed by the content hash.
This is content-addressing at the filesystem level — Gradle's own
design, predating (though not identical to) uv/cargo/Bun's content-
addressed caches.

The substrate's recipe store maps naturally: extracted artifacts
live in the store; materialization creates the Gradle-compatible
directory layout pointing at store contents via hardlinks.

### 6.2 Artifact selection per variant

A variant's `files` array lists what files belong to that variant.
Most variants have a single file (the JAR); some have multiple
(e.g., native variants with multiple platform-specific binaries).

```go
func materializeVariant(ctx context.Context, rv ResolvedVariant, dst string) error {
    for _, file := range rv.Variant.Files {
        srcHash := firstNonEmpty(file.SHA256, file.SHA1)
        if err := fetchWithHashVerify(ctx, file.URL, file.Size, srcHash); err != nil {
            return err
        }
        // Link into Gradle-compatible layout.
    }
}
```

### 6.3 Per-configuration classpaths

Unlike npm (one node_modules per project) or cargo (one target/),
Gradle has **multiple classpaths** per project. The adapter
produces one resolved set per configuration:

- `compileClasspath`
- `runtimeClasspath`
- `testCompileClasspath`
- `testRuntimeClasspath`
- Android: `releaseRuntimeClasspath`, `debugRuntimeClasspath`, etc.

These overlap significantly (test extends main), so the materializer
deduplicates — each unique artifact is on disk once, with
classpath metadata (which artifacts are in which classpath) stored
separately.

## 7. Lockfile

### 7.1 Per-configuration lockfiles

Gradle writes one lockfile per configuration:

```
gradle/dependency-locks/
  compileClasspath.lockfile
  runtimeClasspath.lockfile
  testCompileClasspath.lockfile
  ...
```

Each file lists `group:module:version` one per line:

```
com.fasterxml.jackson.core:jackson-core:2.15.3=compileClasspath,runtimeClasspath
com.fasterxml.jackson.core:jackson-databind:2.15.3=compileClasspath,runtimeClasspath
org.slf4j:slf4j-api:2.0.9=compileClasspath,runtimeClasspath,testRuntimeClasspath
```

Dependencies shared across configurations appear in one line with
a comma-separated configuration list.

### 7.2 LockfileCodec

```go
type gradleLockfileCodec struct{}

func (c *gradleLockfileCodec) Ecosystem() string { return "gradle" }
func (c *gradleLockfileCodec) Filename() string  { return "gradle.lockfile" }
func (c *gradleLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) {
    // Parse the entire gradle/dependency-locks/ directory as a single snapshot.
    // LockfilePin.Attributes includes the configuration list.
}
func (c *gradleLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit one file per unique configuration (since configurations typically
    // overlap, this is where substrate's single-file model meets Gradle's
    // multi-file expectation).
}
```

The substrate's LockfileSnapshot is per-project; Gradle's lockfile
is per-configuration. The codec translates between them by encoding
configuration affiliation into each pin's attributes.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Core version resolution |
| `core/substrate/http` | Maven-format repository fetches |
| `core/substrate/cache/metadata` | POM + .module file caching |
| `core/substrate/store/recipe` | Shared artifact storage |
| `core/substrate/materializer` | Reflink/hardlink for Gradle cache layout |
| `core/substrate/lockfile` | Per-configuration lockfile codec |
| `core/substrate/feeds` | Multi-repository |
| `core/substrate/auth` | gradle.properties + env var credentials |
| `core/substrate/frontier` | Version-resolution phase frontier events |
| `core/substrate/pgp` | PGP signature verification |

Adapter modules under `adapters/gradle/`:

- `coordinate.go` — `GradleCoordinate`, variant encoding
- `buildscript.go` — build.gradle / build.gradle.kts parser (subset)
- `module_metadata.go` — .module JSON parser
- `attributes.go` — attribute schema + matching algorithm
- `capabilities.go` — capability conflict detection
- `version_req.go` — requires/prefers/strictly/rejects semantics
- `variant_resolver.go` — variant selection post-PubGrub
- `constraints.go` — dependency constraints
- `configurations.go` — per-configuration resolution
- `provider.go` — PubGrub DependencyProvider with Gradle semantics
- `resolver.go` — top-level orchestration
- `pom_fallback.go` — POM → synthetic variants for POM-only modules
- `ivy.go` — Ivy-layout repository support
- `materializer.go` — Gradle cache layout producer
- `lockfile.go` — per-configuration lockfile codec
- `gradle_properties.go` — gradle.properties parser (credentials)
- `adapter.go` — top-level Resolver

Estimated LOC: ~7,500. Complexity drivers:

- Attribute matching + disambiguation (~1500 LOC)
- Variant selection (~800 LOC)
- Build script parser (~1500 LOC for Groovy + Kotlin DSL subsets)
- Capability conflict detection (~500 LOC)
- Per-configuration lockfile + management (~500 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| No variant matches consumer attributes | `ErrNoSatisfyingVersion` | List available variants + attributes |
| Multiple variants match ambiguously | `ErrCapabilityConflict` | Ask user for disambiguating attribute |
| Capability conflict | `ErrCapabilityConflict` | List all providers + suggestion |
| Strictly version conflict | `ErrCapabilityConflict` | Show stricts from different parts of graph |
| Module Metadata format version unsupported | `ErrInternalBug` | Future-proofing |
| Ivy repo layout but artifact missing | `ErrNoSuchRecipe` | Path-derivation explanation |
| Build script parse error (declarative subset) | User error | Point at line/column; suggest Gradle tooling fallback |
| Android attribute unknown | `ErrInternalBug` | Android schema extension issue |

## 10. Security

### 10.1 Signature verification

Gradle supports
[dependency verification](https://docs.gradle.org/current/userguide/dependency_verification.html)
via `gradle/verification-metadata.xml`:

```xml
<verification-metadata>
    <configuration>
        <verify-metadata>true</verify-metadata>
        <verify-signatures>true</verify-signatures>
    </configuration>
    <components>
        <component group="com.google.guava" name="guava" version="31.1-jre">
            <artifact name="guava-31.1-jre.jar">
                <sha256 value="..." origin="..."/>
            </artifact>
            <artifact name="guava-31.1-jre.pom">
                <sha256 value="..." origin="..."/>
            </artifact>
        </component>
    </components>
</verification-metadata>
```

Adapter honors this file when present: verify SHA-256 (or configured
algorithm) against every downloaded artifact; verify PGP signature
when `verify-signatures` is enabled.

### 10.2 Plugin trust

Plugins downloaded from the Gradle Plugin Portal have their own
signature verification. We don't *execute* plugins but may fetch
their metadata for dependency resolution; apply same signature
policies as project dependencies.

### 10.3 Capability conflicts as supply-chain signal

Capability conflicts often indicate typo-squatting (a malicious
package claiming a known capability). Elevate capability conflicts
involving known-legitimate capabilities to `severity=critical`
suggestions.

## 11. Testing

### 11.1 Unit tests

- Attribute compatibility + disambiguation rules
- Variant selection on hand-crafted metadata
- VersionRequirement semantics (requires/prefers/strictly/rejects)
- Capability conflict detection
- Per-configuration lockfile format round-trip
- Build script parser (Groovy + Kotlin DSL) on 100+ real build
  files

### 11.2 Integration tests

- Resolve a Spring Boot Gradle project
- Resolve an Android Gradle project (variant-aware with build
  types)
- Resolve a Kotlin Multiplatform project (JVM + JS + iOS
  variants)
- Resolve a project with dependency constraints forcing CVE-
  patched versions
- Resolve a project with included builds (composite)
- Capability conflict test (commons-logging vs jcl-over-slf4j)

### 11.3 Ecosystem compat

50 Gradle projects with `gradle dependencies` output as oracle. Our
resolution must produce equivalent trees. Acceptable divergences:

- Very small (our variant matcher may differ from Gradle's in
  edge cases)
- Document any divergence; fix over time

### 11.4 Performance

- Resolve a typical Spring Boot Gradle project: <5s cold, <1s warm
- Resolve Android app with ~500 deps: <10s cold
- Attribute matching on 100 variants: <10ms

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, typical Spring Boot app | <5s | <3s |
| Cold resolve, Android app | <10s | <6s |
| Warm resolve | <1s | <500ms |
| .module fetch + parse | <50ms | <25ms |
| Attribute match (100 variants) | <10ms | <5ms |
| Materialization, 100 artifacts (reflink) | <1s | <500ms |
| Per-configuration lockfile write | <100ms | <50ms |
| Peak memory, 500-artifact resolve | <350MB | <200MB |

## 13. Phases

**M0.** Types, .module JSON parser, attribute schema, build script
parser (declarative subset).

**M1.** Module Metadata + POM fallback works. Attribute matching
implemented. Single-configuration resolution passes on simple
cases.

**M2.** Full per-configuration resolution. Capability conflicts
detected. Dependency constraints. Version requirement semantics.
Frontier prefetch.

**M3.** Gradle-compat lockfiles. Dependency verification
integration. Ecosystem compat green on 50 projects. Performance
targets met.

**M4.** Ivy repository layout support. Kotlin Multiplatform
variants. Android build variants. Gradle Tooling API fallback for
complex build scripts. Production polish.

## 14. Open Questions

- **Build script evaluation.** Gradle DSL is Turing-complete;
  static parsing catches ~90%. For the rest, options: (a) invoke
  Gradle tooling API as subprocess (slow but accurate), (b) refuse
  (force users to a declarative config), (c) reject with clear
  error and manual override. Proposal: (a) as opt-in fallback.
- **Attribute schema registration.** Gradle lets build scripts
  register custom attributes and compatibility rules. The adapter
  must read these from build scripts when present. Full support
  requires build script evaluation (see above).
- **Kotlin Multiplatform metadata.** KMP projects produce many
  variants; full support requires understanding Kotlin-specific
  attributes. Start with core (JVM, JS, common) in M3; add native
  targets in M4.
- **Android manifest merging.** Android Gradle Plugin does more
  than dependency resolution — it manifests, resources, etc. The
  adapter stays scoped to dependency resolution only; other
  concerns are out of scope.
- **Module replacement rules.** Gradle's `modules { module("X") {
  replacedBy("Y") } }` is one way to resolve capability conflicts.
  The adapter supports it statically.
- **`strictly` version precedence.** When two parts of the graph
  use `strictly` with incompatible versions, what's the right
  user message? Proposal: surface both sites with their `reason`
  fields; no automatic resolution.

## 15. Dependencies

- Substrate M2 (frontier, multi-feed) → adapter M2
- Substrate M3 (materializer, lockfile, PGP) → adapter M3
- Maven adapter's POM parser can be shared — proposal: extract into
  `adapters/jvm_shared/` usable by Maven, Gradle, Scala

External Go dependencies:

- Custom Groovy/Kotlin DSL parser (~1500 LOC; we parse a
  declarative subset, not full grammar)
- `encoding/json` — stdlib sufficient for .module parsing
- `encoding/xml` — stdlib for POM (shared with Maven adapter)
- Custom Ivy XML parser when needed
