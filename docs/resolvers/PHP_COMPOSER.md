# PHP_COMPOSER.md — PHP / Composer Adapter Implementation Plan

Tier 1 — completes the PubGrub-exemplar tier. Validates the
substrate handles **delta-compressed metadata** (Packagist's
"minified" v2 format) and the **stability-flag** filtering pattern
(stable/RC/beta/alpha/dev), neither of which appear in Python or
Rust.

## 1. Overview

The PHP adapter resolves and materializes packages from:

- **[Packagist](https://packagist.org/)** via the
  [v2 metadata API](https://blog.packagist.com/packagist-metadata-v2/)
- **Private Composer repositories** (Satis, Toran Proxy,
  Repman, JFrog Artifactory, GitHub Packages)
- **VCS repositories** (`type: vcs` git/hg/svn references)
- **Path repositories** (`type: path` for monorepo siblings)

Produces:

- A resolved dependency tree satisfying `composer.json` constraints
- A `composer.lock` pinning every package
- A `vendor/` directory layout matching Composer's conventions,
  including PSR-0/PSR-4/classmap autoloader generation

User-visible behaviors (M3 target):

- `sylk resolve php ./composer.json` → `composer.lock`
- `sylk install php` → vendored `vendor/` tree with autoloader
- `sylk add php <vendor/package>` → updates composer.json + lockfile
- `sylk upgrade php [<package>]` → re-resolve, update lockfile
- `sylk why php <vendor/package>` → PubGrub explanation

Non-goals:

- Running PHP itself (we resolve and materialize; runtime is the
  user's choice — system php, Docker container, etc.)
- Composer plugins (the adapter doesn't load PHP plugin code; if a
  project depends on a plugin's behavior, Sylk produces a vendor/
  tree that the user can post-process by running `composer
  run-script post-install-cmd` themselves)

## 2. Data Model

### 2.1 Coordinates

```go
type ComposerCoordinate struct {
    Vendor  string         // "symfony", "laravel", etc.
    Package string         // "console", "framework"
    Version ComposerVer    // Composer's version model
    Source  ComposerSource // dist (preferred) or source
    DevMode bool           // dev branch suffix (e.g. "dev-main", "1.x-dev")
}

func (c ComposerCoordinate) Name() string { return c.Vendor + "/" + c.Package }

type ComposerSource struct {
    Type    string  // "git", "hg", "svn", "path"
    URL     string
    Reference string // commit hash for VCS
    Shasum   string // SHA-1 of the dist tarball when known
}
```

### 2.2 ComposerVer

Composer's version model is **SemVer with extensions** —
specifically:

- Standard SemVer: `1.2.3`, `1.2.3-rc.1+build.5`
- Composer pre-release suffixes: `1.2.3-RC1`, `1.2.3-alpha2`,
  `1.2.3-patch.1`
- Branch versions: `dev-main`, `dev-feature/x`, `1.x-dev`,
  `2.0.x-dev`
- Stability flags applied via constraint syntax: `^1.0@beta`

```go
type ComposerVer struct {
    Major     int
    Minor     int
    Patch     int
    Pre       string         // "RC1", "alpha2", etc.
    Build     string         // build metadata
    Branch    string         // "main", "feature/x" if branch version
    IsDev     bool           // dev-* or X.Y.x-dev
    Stability StabilityLevel // stable, RC, beta, alpha, dev
}

type StabilityLevel int

const (
    StabilityStable StabilityLevel = iota
    StabilityRC
    StabilityBeta
    StabilityAlpha
    StabilityDev
)
```

Comparison rules: numeric tuple first, then pre-release ordering
(stable > RC > beta > alpha > dev), with branch versions ordered by
git timestamp (informational; resolver never picks a branch version
unless explicitly asked).

### 2.3 composer.json

```go
type ComposerManifest struct {
    Name        string
    Description string
    Type        string          // "library", "project", "metapackage", "composer-plugin"
    Version     string          // optional; package's own version
    License     string

    Require        map[string]string          // "^1.0", ">=2.0,<3.0", "1.5.*"
    RequireDev     map[string]string
    Suggest        map[string]string
    Conflict       map[string]string
    Replace        map[string]string
    Provide        map[string]string

    MinimumStability  StabilityLevel
    PreferStable      bool
    StabilityFlags    map[string]StabilityLevel  // per-package overrides

    Repositories      []ComposerRepository
    Platform          map[string]string          // platform requirements
    PlatformDev       map[string]string

    Autoload    AutoloadSpec
    AutoloadDev AutoloadSpec

    Scripts     map[string]any                   // we read but don't execute
}

type ComposerRepository struct {
    Type    string                 // "composer", "vcs", "path", "package"
    URL     string
    Options map[string]any
}

type AutoloadSpec struct {
    PSR4      map[string][]string  // namespace → directories
    PSR0      map[string][]string
    Classmap  []string             // directories to scan
    Files     []string             // files to require_once
    ExcludeFromClassmap []string
}
```

### 2.4 Stability flags

Composer's stability flags are unique among the case studies and
need first-class modeling.

`MinimumStability` filters which versions the resolver can
consider for *any* package. Default `stable`. `dev` allows
everything; `alpha` allows alpha and above.

`PreferStable` (default `true`): when stability levels are equal,
prefer stable. Affects ordering, not eligibility.

Per-package stability flags via constraint syntax:

```json
"require": {
    "vendor/foo": "^1.0@beta",
    "vendor/bar": "dev-main"
}
```

`@beta` allows beta versions of `vendor/foo` even if global
`MinimumStability` is `stable`. `dev-main` requires the `main`
branch's dev version.

The adapter encodes stability into the `Constraint` type via the
`Attributes` map (`stability=beta`) and applies the filter in
`AvailableVersions`.

## 3. HTTP Transport

### 3.1 Packagist API v2

```
GET https://repo.packagist.org/p2/{vendor}/{package}.json
GET https://repo.packagist.org/p2/{vendor}/{package}~dev.json
```

Tagged releases live in the first URL; dev branches in the second.
Both use the same JSON shape; the dev URL is fetched only when the
project's stability allows dev versions or pins a `dev-*` branch.

Response shape (after expanding minification):

```json
{
  "minified": "composer/2.0",
  "packages": {
    "symfony/console": [
      {
        "name": "symfony/console",
        "version": "v6.4.0",
        "version_normalized": "6.4.0.0",
        "source": { "type": "git", "url": "...", "reference": "..." },
        "dist": { "type": "zip", "url": "...", "shasum": "...", "reference": "..." },
        "require": { "php": ">=8.1", "psr/log": "^1|^2|^3" },
        "require-dev": { "..." },
        "autoload": { "psr-4": { "Symfony\\Component\\Console\\": "" } },
        "type": "library",
        "time": "2023-11-29T08:32:23+00:00"
      },
      // ... earlier versions, with delta-compressed fields
    ]
  }
}
```

### 3.2 Minification expansion

Per Packagist's [minification spec](https://github.com/composer/metadata-minifier):

- Each version object is **delta-compressed** against the previous
  in the array.
- Fields not present in a version inherit from the previous version.
- A field set to `null` explicitly clears it.

Expansion algorithm:

```go
func ExpandMinifiedVersions(versions []json.RawMessage) ([]ComposerVersionRecord, error) {
    // Walk in order. For each version:
    //   - Parse as a partial record.
    //   - Merge with the accumulated "previous" record.
    //     - Set fields override accumulated.
    //     - null fields clear accumulated.
    //     - Absent fields inherit accumulated.
    //   - Emit the fully-expanded record.
    //   - Update accumulated for the next iteration.
}

type ComposerVersionRecord struct {
    Name             string
    Version          string
    VersionNormalized string
    Source, Dist     ComposerSource
    Require          map[string]string
    RequireDev       map[string]string
    Conflict         map[string]string
    Replace          map[string]string
    Provide          map[string]string
    Autoload         AutoloadSpec
    Type             string
    Time             time.Time
}
```

The substrate's `MetadataCache` stores the **fully expanded**
records, not the minified form — expansion is cheap (~10μs per
version) and avoids re-expanding on every cache lookup.

### 3.3 Authentication

Composer supports several auth mechanisms:

- **HTTP Basic** for private Composer repositories
- **GitHub OAuth tokens** for github.com VCS repositories
- **GitLab tokens** for gitlab.com
- **Bitbucket OAuth** for bitbucket.org
- **Custom token-* schemes** for private registries

Credentials live in `~/.composer/auth.json` and `auth.json` in the
project root. The adapter reads both and passes them to the
substrate's `AuthResolver` with host-prefix matching.

## 4. Metadata Layer

### 4.1 Repository types

`composer.json` can declare multiple repositories. The adapter
handles each type:

**`composer` type** (Packagist-compatible): the v2 endpoint
described above.

**`vcs` type**: Composer treats a VCS URL as a "virtual registry"
— the adapter clones the repo (shallow), reads tagged versions
from `composer.json` at each tag, builds a synthetic version list.
Supports git, hg, svn, perforce.

**`path` type**: local directory; treat as a single synthetic
version pinned to whatever the local `composer.json` declares.
Useful for monorepos where multiple packages live in subdirectories.

**`package` type** (legacy): the repository declaration *itself*
contains the package metadata inline. Used for non-Composer-aware
sources (binaries, archives without composer.json). Fully supported
but rare in modern projects.

**`artifact` type**: a directory of zip files; the adapter scans
for ZIP archives matching `*-{version}.zip`, extracts metadata
from the embedded `composer.json`. Useful for offline installs.

### 4.2 Platform packages

Composer treats PHP version and extensions as **virtual packages**
in the dependency graph:

- `php` — the PHP interpreter version
- `php-64bit` — 64-bit-only requirement
- `ext-mbstring`, `ext-pdo`, etc. — PHP extensions
- `lib-curl`, `lib-libxml`, etc. — library versions linked into
  PHP

These are populated from the host environment at resolve time. The
adapter detects:

```go
type PlatformInfo struct {
    PHPVersion       string                 // detected from `php -v` or `--platform-php` flag
    PHPInt           int                    // 32 or 64
    Extensions       map[string]string      // "mbstring" -> "1.0.0"
    Libraries        map[string]string
    HHVMVersion      string                 // legacy HHVM support
}
```

`composer.json` `platform` and `platform-dev` blocks override
detected values for cross-platform CI:

```json
"platform": {
    "php": "8.2.0",
    "ext-redis": "5.0.0"
}
```

Per substrate convention, platform packages enter the resolver as
real `Constraint` entries. Resolution fails if any required
platform package is unsatisfied — the user gets a clear "your PHP
is 8.0 but X requires PHP >= 8.1" message.

### 4.3 Conflict, replace, provide

Three relationships beyond `require`:

**`conflict`**: declares this package conflicts with another at
specific versions. The resolver fails if both end up in the graph.
Modeled as negative constraints in PubGrub: `if A is in graph, B
must NOT match X`.

**`replace`**: declares this package *replaces* another. If
package P has `replace: {"old/pkg": "*"}`, then `old/pkg` is
considered unavailable for separate resolution; any reference to
`old/pkg` resolves to P. Used for forks and renames.

**`provide`**: declares this package *implements* a virtual
package. Multiple packages can provide the same virtual package
without conflict. Used for interface packages (e.g.
`psr/log-implementation`) where many concrete logger libraries
declare `provide: {"psr/log-implementation": "1.0|2.0"}`.

PubGrub handles `replace` natively (treat the replacement as
substitution at provider lookup). `conflict` requires custom
incompatibility injection. `provide` is more nuanced — adapter
maintains a "virtual package registry" mapping virtual names to
provider candidates; `Dependencies()` returns the provider as a
candidate.

## 5. Resolver

### 5.1 Stability filter

```go
func (p *composerDepProvider) AvailableVersions(ctx context.Context, pkg ComposerCoordinate) ([]ComposerVer, error) {
    // 1. Fetch v2 metadata (both tagged and ~dev when stability allows dev).
    // 2. Expand minification.
    // 3. Filter by minimum stability:
    //    a. Project-wide MinimumStability sets the floor.
    //    b. Per-package stability flags from composer.json overrides.
    //    c. Constraint @stability suffix overrides further.
    // 4. Filter by platform requirements (require.php, require.ext-*).
    // 5. Order:
    //    a. If PreferStable: stable first, then RC, beta, alpha, dev.
    //    b. Within stability tier: newest first.
    //    c. Lockfile pin gets max priority.
}
```

### 5.2 Conflict / replace / provide handling

The PubGrub provider implementation needs three additional code
paths beyond standard package lookup:

```go
func (p *composerDepProvider) Dependencies(ctx context.Context, pkg ComposerCoordinate, ver ComposerVer) ([]pubgrub.Dependency, error) {
    record := p.fetchRecord(pkg, ver)

    deps := []pubgrub.Dependency{}

    // Standard requires
    for name, constraint := range record.Require {
        deps = append(deps, p.translateRequire(name, constraint))
    }

    // Conflicts: negative constraints
    for name, constraint := range record.Conflict {
        deps = append(deps, p.translateConflict(name, constraint))
    }

    // Replace: register that this package satisfies the named packages.
    // The substrate's pubgrub solver supports virtual replacement via the
    // DependencyProvider's Aliases() method.
    for name, constraint := range record.Replace {
        p.registerReplacement(name, constraint, pkg, ver)
    }

    // Provide: same as replace, but doesn't preclude other providers.
    for name, constraint := range record.Provide {
        p.registerVirtualProvider(name, constraint, pkg, ver)
    }

    return deps
}
```

### 5.3 Frontier integration

Standard. `Considering` events trigger v2 metadata fetches; the
prefetch coordinator deduplicates and bounds concurrency.

Composer 2's parallel fetching (curl_multi) gets us ~50 concurrent
requests against Packagist for free. The substrate's HTTP client
already does HTTP/2 multiplexing, so single-connection parallelism
is automatic; the per-host semaphore caps total in-flight at 50.

## 6. Materializer

### 6.1 vendor/ layout

Composer's standard layout:

```
vendor/
  autoload.php                    # the entry point
  composer/
    autoload_real.php
    autoload_static.php
    autoload_classmap.php
    autoload_psr0.php
    autoload_psr4.php
    autoload_namespaces.php
    autoload_files.php
    ClassLoader.php
    InstalledVersions.php
    installed.json
    installed.php
    LICENSE
    platform_check.php
  symfony/
    console/
      composer.json
      src/
      ...
  ...
```

### 6.2 Distribution download

Each resolved package has a `dist` URL pointing at a ZIP archive.
Download via substrate HTTP, verify SHA-1 (Composer's checksum
algorithm; not blake3 or SHA-256), extract to recipe store, link
into `vendor/{vendor}/{package}/`.

```go
func (m *composerMaterializer) installPackage(ctx context.Context, pkg ResolvedComposerPackage, dst string) error {
    // 1. Fetch dist tarball into recipe store, content-addressed.
    // 2. Verify shasum (SHA-1 against the dist.shasum from metadata).
    // 3. Extract via substrate's ZIP extraction (with path-traversal protection).
    // 4. Link extracted source from recipe store into dst via reflink/hardlink/copy.
    // 5. Write composer.json into the package directory (Composer convention).
}
```

### 6.3 Autoloader generation

This is the unique-to-Composer piece. After all packages are
materialized, the adapter generates the autoloader files Composer
runtime expects. PHP's `require 'vendor/autoload.php'` triggers
class loading according to PSR-0, PSR-4, classmap, and files
conventions.

Generation steps:

```go
func (m *composerMaterializer) generateAutoloader(ctx context.Context, vendorDir string, packages []ResolvedComposerPackage) error {
    // 1. Aggregate autoload sections from all packages + the root project.
    // 2. Write autoload_psr4.php: namespace → directory map.
    // 3. Write autoload_namespaces.php: PSR-0 namespace → directory map.
    // 4. Write autoload_classmap.php: explicit class → file map (scan classmap directories).
    // 5. Write autoload_files.php: list of files to require_once at autoload startup.
    // 6. Write autoload_static.php: optimized static array form for production (--optimize-autoloader equivalent).
    // 7. Write autoload_real.php: the orchestrator that registers everything with SPL.
    // 8. Write the Composer ClassLoader.php (vendored from Composer source).
    // 9. Write installed.json: machine-readable record of all installed packages.
    // 10. Write installed.php: PHP form of installed.json for InstalledVersions API.
    // 11. Write platform_check.php: runtime PHP version + extension assertions.
}
```

The autoloader files are generated PHP source. The adapter ships
templates for each (extracted from upstream Composer's source, kept
in sync via a vendoring update process). A future direction:
distribute these templates as a substrate library since Hex and
Bundler also have autoload-like generation needs.

### 6.4 Classmap scanning

Classmap autoloading requires scanning specified directories for
`class` / `interface` / `trait` declarations and producing a
`class_name → file_path` map.

The scanner is a small PHP-tokenizer port. Use `tokenizer/php` or
implement directly:

```go
func ScanForClasses(rootDir string, exclude []string) (map[string]string, error) {
    // Walk rootDir recursively.
    // For each .php file:
    //   - Tokenize using a minimal PHP lexer.
    //   - Track namespace declarations.
    //   - Emit (namespace + classname → file) for class/interface/trait/enum.
    // Skip dirs in exclude.
}
```

Performance-critical: large vendor/ trees can have tens of
thousands of PHP files. Parallelize directory walks; skip
known-irrelevant dirs (`tests/`, `docs/`, `examples/` if listed in
`exclude-from-classmap`).

## 7. Lockfile

### 7.1 Format

`composer.lock` is JSON with a specific structure:

```json
{
  "_readme": [...],
  "content-hash": "blake3-of-composer.json-relevant-fields",
  "packages": [
    {
      "name": "symfony/console",
      "version": "v6.4.0",
      "source": { "type": "git", "url": "...", "reference": "..." },
      "dist": { "type": "zip", "url": "...", "shasum": "...", "reference": "..." },
      "require": { ... },
      "type": "library",
      "autoload": { ... },
      "time": "..."
    }
  ],
  "packages-dev": [...],
  "aliases": [],
  "minimum-stability": "stable",
  "stability-flags": [],
  "prefer-stable": true,
  "prefer-lowest": false,
  "platform": {},
  "platform-dev": {},
  "platform-overrides": {},
  "plugin-api-version": "2.6.0"
}
```

### 7.2 Codec

```go
type composerLockfileCodec struct{}

func (c *composerLockfileCodec) Ecosystem() string { return "php" }
func (c *composerLockfileCodec) Filename() string  { return "composer.lock" }
func (c *composerLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) { ... }
func (c *composerLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit JSON with stable key ordering matching Composer's format.
    // Include _readme matching Composer's exactly (for diff cleanliness).
    // Compute content-hash from the canonical composer.json fields.
}
```

The `content-hash` field is interesting: it's a hash of the
*relevant* composer.json fields, not the whole file. Used by
Composer to detect when composer.json has changed and the lockfile
is stale. The adapter computes this hash matching Composer's
algorithm exactly so external Composer can validate the lockfile.

Composer's hash algorithm (per Composer source):

```go
func ComputeContentHash(manifest *ComposerManifest) string {
    // Extract the lockfile-relevant fields from composer.json:
    //   name, version, require, require-dev, conflict, replace,
    //   provide, minimum-stability, prefer-stable, repositories,
    //   platform, platform-dev, platform-overrides
    // Serialize to JSON with sorted keys.
    // MD5 (yes, MD5 — Composer's choice for backward compat).
}
```

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Standard PubGrub with custom alias/conflict/provide handling |
| `core/substrate/http` | All v2 metadata fetches, dist downloads |
| `core/substrate/cache/metadata` | Per-package expanded records cached |
| `core/substrate/store/recipe` | Dist tarballs and extracted source storage |
| `core/substrate/materializer` | vendor/ tree linking |
| `core/substrate/lockfile` | composer.lock codec |
| `core/substrate/feeds` | Multi-repository (composer + vcs + path + package) |
| `core/substrate/auth` | auth.json credentials sourcing |
| `core/substrate/frontier` | Standard prefetch |
| `core/substrate/git` | VCS repository cloning |

Adapter modules under `adapters/php/`:

- `coordinate.go` — `ComposerCoordinate`
- `version.go` — `ComposerVer` with stability ordering
- `manifest.go` — composer.json parser
- `metadata.go` — v2 metadata fetcher
- `minifier.go` — minification expansion
- `repository.go` — repository type handlers
- `platform.go` — platform package detection
- `provider.go` — PubGrub provider with conflict/replace/provide
- `materializer.go` — vendor/ tree construction
- `autoload.go` — autoloader generation
- `classmap.go` — PHP class scanner
- `lockfile.go` — composer.lock codec
- `content_hash.go` — composer's hash algorithm
- `adapter.go` — top-level Resolver

Estimated LOC: ~5,000. Bigger than Cargo because of:

- Autoloader generation (~800 LOC of code + templates)
- Classmap scanner / PHP tokenizer (~500 LOC)
- Multiple repository types
- Conflict/replace/provide modeling

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Package not found on any repo | `ErrNoSuchRecipe` | List all repos checked |
| Stability filter rejects all versions | `ErrNoSatisfyingVersion` | "X has only beta versions but minimum-stability is stable; use @beta or lower minimum-stability" |
| Platform requirement unmet | `ErrNoSatisfyingVersion` | "X requires PHP 8.2 but project declares 8.1" |
| Conflict declaration violated | `ErrCapabilityConflict` | Surface both packages and the conflict declaration |
| dist shasum mismatch | `ErrIntegrityMismatch` | Possibly compromised mirror |
| VCS repo: ref doesn't exist | `ErrNoSuchRecipe` | Branch deleted or tag missing |
| Autoloader generation: invalid PSR-4 spec | `ErrInternalBug` | Malformed package — surface |
| Multiple packages provide non-mutually-exclusive virtual | (informational) | Resolver picks per priority |
| Replace cycle | `ErrCycleDetected` | Two packages mutually replace |

## 10. Security

### 10.1 dist shasum verification

SHA-1. Yes, Composer uses SHA-1; this is a known weakness but
ecosystem-mandated. The adapter verifies SHA-1 as Composer does, but
*also* records SHA-256 of every fetched dist in the substrate cache
for our own integrity guarantees. If two fetches of the same
declared SHA-1 produce different SHA-256, that's suspicious and
surfaces as `ErrIntegrityMismatch`.

### 10.2 Composer plugin sandboxing

Composer plugins are PHP code that runs during `composer install`.
The adapter does NOT run them (we're a resolver, not a runner).
Users who depend on plugins for autoloader extensions get a clear
warning: "package X declares Composer plugin Y; Sylk does not
execute Composer plugins. Run `composer run-script post-install-cmd`
manually if needed."

### 10.3 PHP class autoloading is code execution

Loading classes runs static initializers — a malicious package's
classes can execute arbitrary code at autoload time. This is a PHP
runtime risk; the adapter doesn't mitigate it directly but
recommends users review dist tarballs before installing
(future: integrate with [PHP Roave/SecurityAdvisories](https://github.com/Roave/SecurityAdvisories)
or similar).

### 10.4 auth.json security

Tokens in `auth.json` are sensitive. The adapter:

- Never logs token values (substrate's `AuthResolver` strips them
  from log lines)
- Honors `auth.json` file permissions; warns if `auth.json` is
  world-readable
- Never sends tokens to repositories other than their declared
  hosts

## 11. Testing

### 11.1 Unit tests

- `ComposerVer` parser + comparator: corpus from
  [composer/semver](https://github.com/composer/semver/tree/main/tests)
- composer.json parser: real manifests from top 200 Packagist
  packages
- v2 metadata expansion (minification): synthetic + captured
  Packagist responses
- Stability filter: matrix of minimum-stability × per-package flags
  × constraint suffixes
- Autoloader generation: golden output for known package sets
- Classmap scanner: edge cases (namespaced classes, traits,
  conditional class definitions, anonymous classes)
- composer.lock codec: round-trip 100+ real lockfiles

### 11.2 Integration tests

- Resolve and materialize Symfony framework + Doctrine + Twig
  (large dependency tree, exercises feature combinations)
- Resolve a project using `replace` for symfony/polyfill packages
- Resolve a project with platform requirements (php 8.2, ext-redis)
- Resolve from multiple repositories (Packagist + private +
  git VCS)
- Resolve a project using `path` repos for monorepo siblings

### 11.3 Ecosystem compatibility

50 real PHP projects. Compare our resolution to `composer update`
output. Acceptable divergences:

- **Tie-breaking order**: when two versions are equally good, the
  substrate may pick differently than Composer. Document and
  warn.
- **Platform check codepath**: the adapter resolves against
  declared `platform` overrides; Composer also tries to detect
  platform automatically. Document the difference.

### 11.4 Performance

- Resolve symfony/symfony full deps: <5s cold, <500ms warm
- Materialize 100-package vendor/: <2s on reflink FS
- Autoloader generation, 500-package classmap: <3s
- Classmap scan, 10K PHP files: <2s

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, 100 packages | <5s | <3s |
| Warm resolve, 100 packages | <500ms | <250ms |
| v2 metadata fetch (per pkg) | <100ms | <50ms |
| Minification expansion (per pkg) | <1ms | <500μs |
| Materialization, 100 pkgs (reflink) | <2s | <1s |
| Autoloader generation | <3s | <1.5s |
| Classmap scan, 10K files | <2s | <1s |
| Lockfile read+validate | <50ms | <25ms |
| Lockfile write (canonical) | <50ms | <25ms |

## 13. Phases

**M0.** Types compile; ComposerVer/ComposerCoordinate/ComposerManifest
unit tests pass.

**M1.** v2 metadata client fetches Packagist; minification expansion
works; dist downloads with shasum verification.

**M2.** PubGrub end-to-end; stability filter; conflict/replace/provide
in resolver; frontier prefetch wired; ecosystem-compat green for top
20 PHP projects.

**M3.** vendor/ materializer; full autoloader generation including
classmap scanning; composer.lock codec round-trips byte-identically;
all 50 ecosystem-compat projects green; performance targets met.

**M4.** PHP plugin advisory mode (warn but don't run), security
advisory integration, observability dashboards.

## 14. Open Questions

- **PHP runtime detection vs. user-specified platform.** Default
  detect (`php -v`)? Or always require explicit `--platform-php`?
  Docker workflows often want explicit; local dev wants detect.
  Proposal: detect with override; warn when detection differs from
  declared `platform`.
- **Autoloader optimization levels.** Composer has `--optimize`,
  `--classmap-authoritative`, `--apcu`. Authoritative classmaps
  prevent runtime class loading checks (faster but dangerous —
  classes added at runtime are invisible). Default to optimize but
  not authoritative; expose flags for advanced users.
- **Scripts handling.** composer.json `scripts` blocks define
  hooks (`post-install-cmd`, `post-update-cmd`). The adapter
  doesn't execute them but should it warn? Proposal: log info "X
  scripts declared, not executed; run `composer run-script post-
  install-cmd` if needed."
- **Composer plugins.** Some plugins extend the resolver itself
  (composer-asset-plugin, etc.). Refusing to execute them means
  not all real-world projects work with the substrate. Proposal:
  document common plugins and provide native equivalents over
  time; refuse in M3, build native equivalents in M4+.
- **Packagist repositories vs. composer repositories.** A
  `repositories` entry of type `composer` could point at any v2-
  compatible feed. The adapter must not assume Packagist
  specifically. Test with Toran Proxy and Repman explicitly.
- **PSR-0 deprecation.** PSR-0 is deprecated in favor of PSR-4
  but still widely used. Full PSR-0 support is required for
  compatibility but should we deprecate it in our autoloader and
  provide migration tooling? Proposal: support both indefinitely;
  no special migration tooling.

## 15. Dependencies

- Substrate M2 (PubGrub + frontier + multi-feed) → adapter M2
- Substrate M3 (materializer + lockfile) → adapter M3

External Go dependencies beyond substrate:

- `github.com/Masterminds/semver/v3` — possibly; or roll own for
  Composer's quirks
- Custom PHP tokenizer (~500 LOC; no viable Go library matches
  Composer's classmap semantics)
- Composer-format ClassLoader.php template (vendored from
  Composer source, kept in sync)

No dependencies on other adapters.
