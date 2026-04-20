# RUBY_BUNDLER.md — Ruby / RubyGems + Bundler Adapter Implementation Plan

Tier 4 — second of the registry-federation tier. Validates the
substrate handles **compact-index protocol** (Bundler's static-text
metadata format), produces **Bundler-2.4-compatible
PubGrub-resolved Gemfile.lock** output, and routes through Ruby's
native cache layout for ecosystem interoperability.

## 1. Overview

The Ruby adapter resolves and materializes gems from:

- **[rubygems.org](https://rubygems.org/)** via the
  [compact index protocol](https://github.com/rubygems/compact_index)
- **Private gem servers** (Gemfury, Geminabox, JFrog Artifactory,
  GitHub Packages with rubygems support)
- **Git repositories** (`gem 'foo', git: 'https://github.com/...'`)
- **Path dependencies** (`gem 'foo', path: '../foo'`)
- **Vendored gems** (`Gemfile.local` paths, GEM_HOME inspection)

Produces:

- A resolved dependency graph using the substrate's PubGrub (Bundler
  2.4+ semantics)
- An updated `Gemfile.lock` byte-identical to Bundler's output
- A materialized gem layout compatible with Bundler's
  per-Ruby-version cache (`~/.gem/ruby/{version}/gems/`)
- Centralized cross-Ruby-version cache (Sylk's improvement over
  Bundler's per-version isolation)

User-visible behaviors (M3 target):

- `sylk resolve ruby ./Gemfile` → updated Gemfile.lock
- `sylk install ruby` → gems installed into per-Ruby cache
- `sylk add ruby <gem> [--version <range>]` → updates Gemfile +
  Gemfile.lock
- `sylk upgrade ruby [<gem>]` → re-resolves
- `sylk why ruby <gem>` → PubGrub-driven explanation

Non-goals:

- Running Ruby itself (we resolve and materialize; user invokes
  Ruby via system `ruby`, rbenv, rvm, asdf, etc.)
- Building gem native extensions (the materializer extracts source;
  building is `gem install`'s job for gems with C extensions —
  delegated)
- CocoaPods support (separate adapter; CocoaPods uses Molinillo,
  not PubGrub)

## 2. Data Model

### 2.1 Coordinates

```go
type RubyGemCoordinate struct {
    Name     string         // gem name; lowercase, hyphen-separated
    Version  RubyGemsVersion
    Platform GemPlatform    // ruby (default), java, mingw, x86_64-linux, etc.
    Source   GemSource
}

type RubyGemsVersion struct {
    Segments  []string       // ["1", "2", "3"] for "1.2.3", ["1", "0", "0", "rc", "1"] for "1.0.0.rc.1"
    Raw       string         // original string
    Prerelease bool
}

func (v RubyGemsVersion) Compare(other RubyGemsVersion) int {
    // RubyGems' version comparison: segment-by-segment, with prereleases
    // sorting before stable. ".rc." < ".pre." < ".alpha." < (numeric only).
    // Reference: rubygems/rubygems lib/rubygems/version.rb.
}

type GemPlatform struct {
    CPU  string  // "x86_64", "arm64", "universal", "ruby"
    OS   string  // "linux", "darwin", "mingw32", ""
    Version string // OS version when applicable
}

func (p GemPlatform) String() string {
    // "ruby" for pure-Ruby gems
    // "x86_64-linux", "arm64-darwin", "x86_64-mingw32-ucrt"
    // "java" for JRuby
    // "universal-darwin" for OS X universal binaries
}

func (consumer GemPlatform) Compatible(recipe GemPlatform) bool {
    // ruby is universal — compatible with everything
    // platform-specific gems compatible only with matching platforms
    // universal-darwin compatible with arm64-darwin, x86_64-darwin
}
```

### 2.2 Gemfile

Ruby DSL — Turing-complete. We parse the **declarative subset**
(gem declarations, source declarations, group declarations,
platform conditionals). Programmatic logic in Gemfile (loops,
conditional declarations) requires Ruby evaluation.

```go
type Gemfile struct {
    RubyVersion  string         // ruby '3.2.0'
    Sources      []GemSource    // source 'https://rubygems.org'
    Gems         []GemfileEntry
    Groups       []Group        // group :development, :test do
    Plugins      []GemfilePlugin
    GitSources   map[string]GitSourceFn  // git_source declarations
}

type GemfileEntry struct {
    Name        string
    Version     VersionRange   // empty means "any"
    Source      *GemSource     // overrides default source
    Git         *GitDependency
    Path        string
    Group       []string       // membership in named groups
    Require     []string       // alternative require paths
    PlatformReq []GemPlatform  // platform conditionals
    Type        string         // "git", "path", "rubygems", or empty
}

type Group struct {
    Names []string
    Gems  []GemfileEntry
}
```

### 2.3 Gemfile.lock

Bundler's lockfile is custom plain-text:

```
GIT
  remote: https://github.com/rails/rails.git
  revision: abc123def456...
  branch: main
  specs:
    actionview (8.0.0)
      activesupport (= 8.0.0)
      ...

GEM
  remote: https://rubygems.org/
  specs:
    activesupport (8.0.0)
      base64
      bigdecimal
      ...

PLATFORMS
  ruby
  x86_64-linux

RUBY VERSION
   ruby 3.2.0p0

DEPENDENCIES
  rails (~> 8.0.0)
  pg (~> 1.5)

BUNDLED WITH
   2.4.10
```

```go
type Gemfilelock struct {
    Sources         []GemfilelockSource
    GitDeps         []GemfilelockGit
    PathDeps        []GemfilelockPath
    Specs           []GemfilelockSpec
    Platforms       []GemPlatform
    RubyVersion     string
    Dependencies    []GemfilelockDependency  // user-declared (with constraints)
    BundledWith     string                    // "2.4.10"
}

type GemfilelockSpec struct {
    Name         string
    Version      RubyGemsVersion
    Platform     GemPlatform
    Dependencies []GemfilelockSpecDep
}
```

### 2.4 Gemspec

Each gem ships with a `.gemspec` file (Ruby code) declaring its
metadata. The compact-index serves a normalized form; we don't
typically need to evaluate the gemspec itself unless materializing
from a non-compact-index source.

For path / git deps, evaluating the .gemspec is necessary. The
adapter:

- For path deps: read the .gemspec file, run a minimal Ruby
  evaluator OR shell out to `gem build` to extract metadata
- For git deps: clone repo, evaluate .gemspec at the resolved
  commit
- For rubygems-served deps: use compact-index data exclusively

## 3. HTTP Transport

### 3.1 Compact index protocol

The [compact index](https://github.com/rubygems/compact_index)
serves three endpoint types:

```
# Names: list of all gem names
GET https://index.rubygems.org/names
# Plain text, one name per line, alphabetical, append-only.

# Versions: every (name, version, platform, info_checksum) globally
GET https://index.rubygems.org/versions
# Plain text, append-only. Lines like:
# rails 8.0.0,8.0.1 abc123
# pg 1.5.0-x86_64-linux d4e5f6

# Per-gem metadata
GET https://index.rubygems.org/info/{gem}
# Plain text, one line per (version, platform), with deps.
# Format:
# 8.0.0 actionview:= 8.0.0&activesupport:= 8.0.0|checksum:abc...,ruby:>= 3.1.0
# 8.0.0-java actionview:= 8.0.0&activesupport:= 8.0.0|checksum:def...,ruby:>= 3.1.0
```

The adapter caches:

- `/names` with daily refresh
- `/versions` with hourly refresh + Etag (it grows append-only;
  conditional GETs are cheap)
- `/info/{gem}` per gem, with checksum from `/versions` driving
  cache invalidation

### 3.2 Compact-index parser

```go
func ParseCompactInfoFile(r io.Reader) iter.Seq2[CompactInfoEntry, error] {
    // Each line: "{version}[-{platform}] {dep:type {ver}&...}|{key:val,...}"
    // Stream-parse line-by-line (the file can be megabytes for old gems
    // like rails with thousands of versions).
}

type CompactInfoEntry struct {
    Version       RubyGemsVersion
    Platform      GemPlatform
    Dependencies  []CompactDep
    Checksum      string  // SHA-256 of the .gem file
    RubyVersion   string  // required ruby
    RubygemsVer   string  // required rubygems
}

type CompactDep struct {
    Name        string
    Type        string  // "=" (runtime), "<:" (development)
    VersionReq  string
}
```

### 3.3 .gem file fetch

Gems live at:

```
GET https://rubygems.org/gems/{gem}-{version}[-{platform}].gem
```

The .gem file is a tar archive (POSIX tar) containing:

- `metadata.gz` — gzipped YAML gemspec
- `data.tar.gz` — gzipped tar of the gem's source files
- `checksums.yaml.gz` — file checksums

### 3.4 Authentication

Gem API keys for private feeds:

- `~/.gem/credentials` file (Ruby YAML format)
- `BUNDLE_<HOST>__<PATH>` env vars (Bundler convention)
- `BUNDLE_RUBYGEMS__ORG` for rubygems.org

```yaml
# ~/.gem/credentials
:rubygems_api_key: <token-for-rubygems.org>
:gemfury: <token-for-gemfury.com>
:internal: <token-for-internal-feed>
```

The adapter reads this YAML and routes credentials to the
substrate's AuthResolver based on the gem source URL.

## 4. Metadata Layer

### 4.1 Compact-index synchronization

The full registry sync workflow:

1. On first use: fetch `/names` (~10MB compressed); cache.
2. On every resolve: conditional GET `/versions` (cheap incremental
   update; rubygems.org appends ~kilobytes per minute).
3. For each gem we need: check if `/versions` shows an updated
   checksum since our cached `/info/{gem}`; if so, re-fetch.
4. Use cached `/info/{gem}` data for resolution.

This minimizes network I/O per resolve. After initial sync, typical
warm-cache resolves issue ~3 HTTP requests total
(`/versions` 304, then maybe one `/info/{gem}` revalidation).

### 4.2 Version range syntax

RubyGems uses pessimistic-version operators:

- `>= 1.0` — minimum
- `>= 1.0, < 2.0` — explicit range (comma separator)
- `~> 1.5` — pessimistic; >= 1.5, < 2.0 (locks major)
- `~> 1.5.0` — >= 1.5.0, < 1.6.0 (locks major+minor)
- `= 1.5.0` — exact pin
- `1.5.0` — bare version, equivalent to `= 1.5.0`

Parser is small (~200 LOC). Multiple constraints are AND-ed.

### 4.3 Cache keys

```
(ecosystem="ruby", name=<gem>, version=<ver>, platform_hash=<hash of GemPlatform>)
```

Platform-hash distinguishes same-version gems for different
platforms (e.g., `pg-1.5.0` for ruby vs `pg-1.5.0-x86_64-linux`).

## 5. Resolver

### 5.1 PubGrub via shared substrate impl

```go
type rubyDepProvider struct {
    fetcher     *rubyFetcher
    project     *Gemfile
    rubyVer     string
    platforms   []GemPlatform   // platforms to resolve for
    cache       *substrate.MetadataCache
}

func (p *rubyDepProvider) AvailableVersions(ctx context.Context, pkg RubyGemCoordinate) ([]RubyGemsVersion, error) {
    // 1. Fetch compact /info/{name} entries.
    // 2. Filter by platform compatibility.
    // 3. Filter by required Ruby version (compact entry's "ruby:" field).
    // 4. Filter prereleases unless project allows.
    // 5. Order: newest first, lockfile pin priority.
}

func (p *rubyDepProvider) Dependencies(ctx context.Context, pkg RubyGemCoordinate, ver RubyGemsVersion) ([]pubgrub.Dependency, error) {
    // 1. Look up the entry's dependencies (already in compact-index entry).
    // 2. Skip development deps (type="<:") unless explicitly in test/development group.
    // 3. Translate to pubgrub.Dependency.
}
```

### 5.2 Group filtering

Bundler resolves the **union of all groups**, then filters at
materialization. Groups are user-controlled subsets; `bundle install
--without development:test` excludes those gems from the materialized
result but they're still in the lockfile.

The adapter:

- Resolves the union (lockfile is identical regardless of groups)
- Applies group filter at materialize time

### 5.3 Platform handling

Bundler resolves separately per platform listed in the `Gemfile.lock`'s
`PLATFORMS` block. A gem available as `pg-1.5.0-x86_64-linux` and
`pg-1.5.0-x86_64-darwin` produces different lockfile entries per
platform.

The adapter resolves all configured platforms in a single resolve
pass, producing a multi-platform `Gemfile.lock`. This matches
Bundler 2.x default behavior.

### 5.4 Git and path dependencies

Same pattern as other adapters:

- Git deps: clone (via substrate git client), evaluate `.gemspec`,
  pin commit SHA in lockfile
- Path deps: read `.gemspec` from local directory, treat as synthetic
  pinned version

For .gemspec evaluation (which requires Ruby):

- Option A: invoke `ruby -e 'load_gemspec_and_print_metadata.rb'` as
  subprocess
- Option B: implement a minimal Ruby DSL evaluator for the gemspec
  declarative subset (~600 LOC; covers ~95% of real .gemspec files)

Proposal: B for simple cases, A as fallback when B fails to
parse.

### 5.5 Frontier

PubGrub-based; implements `FrontierAwareResolver`. Frontier events
trigger compact-index `/info/{gem}` fetches for newly-considered
gems. Standard pattern.

## 6. Materializer

### 6.1 Gem cache layout

Bundler's per-Ruby-version layout:

```
~/.gem/ruby/3.2.0/
  gems/
    rails-8.0.0/
      lib/
      bin/
      Gemfile
      rails.gemspec
      ...
  cache/
    rails-8.0.0.gem
  specifications/
    rails-8.0.0.gemspec  # extracted gemspec
  build_info/
    rails-8.0.0.info
  bin/
    rails  # console script wrappers
```

The substrate's recipe store maintains a **Ruby-version-agnostic**
content-addressed cache. Materialization creates per-Ruby-version
hardlink trees:

```
{recipe-store}/ruby/
  gems/
    rails-8.0.0/  # extracted source, content-addressed
    activesupport-8.0.0/
    ...

# Materialize for Ruby 3.2.0:
~/.gem/ruby/3.2.0/gems/rails-8.0.0 → hardlinks to {recipe-store}/ruby/gems/rails-8.0.0/

# Materialize for Ruby 3.3.0 (same gem version):
~/.gem/ruby/3.3.0/gems/rails-8.0.0 → hardlinks to same source
```

This is the substrate's improvement over native Bundler — Bundler
re-downloads + re-installs gems per Ruby version. Sylk shares.

### 6.2 .gem extraction

```go
func extractGem(gemPath, dst string) error {
    // 1. Open .gem as POSIX tar.
    // 2. Locate metadata.gz, data.tar.gz, checksums.yaml.gz.
    // 3. Verify SHA-256 of the .gem file against compact-index checksum.
    // 4. Decompress data.tar.gz; extract to dst.
    // 5. Verify checksums.yaml entries against extracted files.
    // 6. Write metadata-extracted gemspec to specifications/ directory.
}
```

### 6.3 Native extension building

Many gems include C extensions (`ext/{ext_name}/extconf.rb`). Building
requires invoking the consumer's Ruby:

```bash
cd {gem-dir}/ext/{ext_name}
ruby extconf.rb
make
make install
```

The adapter:

- Detects `ext/` directories in extracted gems
- For each, runs `extconf.rb` + `make` in a substrate-managed
  build environment (subprocess with chdir, env scoped, output
  captured)
- Caches built extensions by `(gem name, version, platform, ruby
  major+minor, extension hash)` so same Ruby + same gem version =
  reused build

Native build failures are common (missing system headers, etc.) and
surface with full stderr. Configurable: skip native builds for
specified gems (`--no-native-extensions=mygem`).

### 6.4 Console script generation

Each gem's gemspec lists `executables`. The materializer creates
wrapper scripts in `~/.gem/ruby/{version}/bin/`:

```ruby
#!/usr/bin/env ruby
# frozen_string_literal: true
load Gem.bin_path('rails', 'rails', version = ">= 0")
```

(Standard Bundler/RubyGems pattern; copy verbatim.)

## 7. Lockfile

### 7.1 Gemfile.lock format

Plain text, structured but not standardized via spec. Bundler
itself is the reference implementation.

```go
type rubyLockfileCodec struct{}

func (c *rubyLockfileCodec) Ecosystem() string { return "ruby" }
func (c *rubyLockfileCodec) Filename() string  { return "Gemfile.lock" }

func (c *rubyLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) {
    // Parse the text format. Sections: GIT, PATH, GEM, PLATFORMS,
    // RUBY VERSION, DEPENDENCIES, BUNDLED WITH.
    // Translate to LockfileSnapshot.
}

func (c *rubyLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) {
    // Emit byte-identical to Bundler 2.4+:
    //   - Section ordering: GIT, PATH, GEM, PLATFORMS, RUBY VERSION, DEPENDENCIES, BUNDLED WITH.
    //   - Within each section, sort entries alphabetically.
    //   - Indent dependencies with 4 spaces; specs with 2.
    //   - "BUNDLED WITH" reflects the Bundler version semver-compatibility we target (e.g. "2.4.10").
}
```

Byte-identical output is critical — Ruby developers running `bundle
install` after `sylk install ruby` should see no Gemfile.lock diff.

## 8. Substrate Integration

| Substrate primitive | Adapter usage |
|---|---|
| `core/resolver/pubgrub` | Direct use |
| `core/substrate/http` | Compact index fetches, .gem downloads |
| `core/substrate/cache/metadata` | /names, /versions, /info/{gem} caching |
| `core/substrate/store/recipe` | Cross-Ruby-version content-addressed cache |
| `core/substrate/materializer` | Per-Ruby-version hardlink layout |
| `core/substrate/lockfile` | Gemfile.lock codec |
| `core/substrate/feeds` | Multi-source (rubygems.org + private) |
| `core/substrate/auth` | ~/.gem/credentials YAML + Bundler env vars |
| `core/substrate/frontier` | Standard PubGrub frontier |
| `core/substrate/git` | Git dep cloning |
| `core/substrate/subprocess` | Native extension build, .gemspec eval fallback |

Adapter modules under `adapters/ruby/`:

- `coordinate.go` — `RubyGemCoordinate`
- `version.go` — `RubyGemsVersion`
- `platform.go` — `GemPlatform` + compatibility
- `gemfile.go` — Gemfile DSL declarative parser
- `gemspec.go` — minimal .gemspec evaluator
- `compact_index.go` — compact-index client + parser
- `versions_sync.go` — incremental /versions sync
- `provider.go` — PubGrub DependencyProvider
- `groups.go` — group filtering at materialize time
- `gem_extract.go` — .gem tar extraction
- `native_ext.go` — native extension building
- `materializer.go` — per-Ruby-version hardlink layout
- `console_scripts.go` — wrapper script generation
- `credentials.go` — ~/.gem/credentials parser
- `lockfile.go` — Gemfile.lock codec (byte-identical)
- `adapter.go` — top-level Resolver

Estimated LOC: ~5,500. Complexity drivers:

- Gemfile DSL parser (~1500 LOC)
- Minimal .gemspec evaluator (~600 LOC)
- Gemfile.lock byte-identical codec (~800 LOC)
- Native extension building (~500 LOC)

## 9. Error Handling

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Gem not in compact index | `ErrNoSuchRecipe` | Surface compact-index URL |
| No platform-compatible version | `ErrNoSatisfyingVersion` | "X has versions for [java, x86_64-linux] but project targets ruby" |
| Required Ruby version mismatch | `ErrNoSatisfyingVersion` | "X requires Ruby >= 3.1 but project declares 2.7" |
| .gem checksum mismatch | `ErrIntegrityMismatch` | Fatal |
| .gemspec evaluation fails | User error | Fall back to subprocess Ruby |
| Native extension build fails | `ErrInternalBug` or user error | Surface make's stderr; suggest --no-native-extensions |
| Git dep ref doesn't resolve | `ErrNoSuchRecipe` | Branch deleted |
| Path dep .gemspec missing | User error | Local source missing required file |
| Gemfile.lock format unrecognized | User error | Old Bundler format; suggest `bundle update --bundler` |

## 10. Security

### 10.1 Checksum verification

Compact index entries include SHA-256 of the .gem file. Mandatory
verification on every download. RubyGems-side support has been
universal since 2018.

### 10.2 Gem signing

RubyGems supports gem signing via `.cert` files in the .gem
archive's `metadata.gz`. Adoption is sparse but exists for
security-conscious gems. The adapter verifies signatures when
present; missing signatures are not errors (most gems are unsigned).

### 10.3 RubyGems advisory database

[RubyAdvisoryDB](https://github.com/rubysec/ruby-advisory-db)
provides CVE data for known-vulnerable gem versions. The adapter
integrates as a `CapabilityConflict` with severity, similar to
RustSec for Cargo.

### 10.4 Bundler audit

`bundle audit` (the gem) does the same thing client-side. Sylk's
adapter performs equivalent checks during resolve and surfaces
findings.

## 11. Testing

### 11.1 Unit tests

- `RubyGemsVersion` parser + comparator (corpus from
  rubygems/rubygems test suite)
- `GemPlatform` parser + compatibility matrix
- Compact-index parser on real /info/{gem} files
- Version range parser
- Gemfile DSL parser on 50+ real Gemfiles
- Gemfile.lock byte-identical round-trip on 100+ real lockfiles
- Native extension build on a controlled set of gems

### 11.2 Integration tests

- Resolve a Rails 8.x project (large dep tree, exercises platforms)
- Resolve with mixed sources (rubygems + git + path)
- Resolve a project with platform-specific gems (nokogiri, pg)
- Resolve with group filtering (development/test groups)
- Resolve a project pinning Bundler version
- Native extension build for nokogiri (libxml2 dependency)

### 11.3 Ecosystem compat

Golden corpus of 50 Ruby projects, oracle: `bundle install` output.
Match Gemfile.lock byte-for-byte after our resolve.

### 11.4 Performance

- Resolve typical Rails app (~150 gems): <3s cold, <500ms warm
- Resolve large Ruby app (Gitlab-scale, ~400 gems): <8s cold
- Compact-index initial sync (rubygems.org full): <30s on a fast
  connection
- /versions incremental sync: <1s
- Native extension build (nokogiri): build time + cache; second
  build: <100ms

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold resolve, typical Rails app | <3s | <2s |
| Cold resolve, Gitlab-scale | <8s | <5s |
| Warm resolve | <500ms | <250ms |
| Compact /info/{gem} fetch | <100ms | <50ms |
| Incremental /versions sync | <1s | <500ms |
| .gem extraction (typical 1MB gem) | <50ms | <25ms |
| Materialization, 150 gems (hardlink) | <2s | <1s |
| Lockfile byte-identical write | <100ms | <50ms |
| Peak memory, 400-gem resolve | <300MB | <200MB |

## 13. Phases

**M0.** Types, parsers, version comparator unit tests pass.

**M1.** Compact-index client works against rubygems.org. .gem
extraction with checksum.

**M2.** PubGrub end-to-end. Gemfile DSL parser. Frontier prefetch.
Group filtering.

**M3.** Per-Ruby materializer with cross-version cache sharing.
Gemfile.lock byte-identical codec. Native extension building.
50 ecosystem-compat projects green.

**M4.** Advisory DB integration. CocoaPods adapter (separate from
this adapter; Molinillo-based — would be a Tier 6).

## 14. Open Questions

- **Bundler version compatibility.** "BUNDLED WITH" version
  influences some lockfile semantics. Target Bundler 2.4+ exactly.
  Document.
- **CocoaPods relationship.** The rubygems gem ships Molinillo;
  CocoaPods uses Molinillo. We don't run rubygems-the-gem; we
  resolve directly. CocoaPods integration is a separate adapter
  (`adapters/cocoapods/`) tracking Podfile.lock format. Not in
  scope here.
- **rbenv/rvm/asdf integration.** The materializer needs to know
  the Ruby version. Default: read from `.ruby-version` file or
  `Gemfile`'s `ruby` directive. Don't manage Ruby toolchain
  installations.
- **JRuby support.** JRuby uses Java platform gems. Test coverage
  required; ensure platform compatibility logic handles "java"
  correctly.
- **Subprocess gemspec evaluation safety.** Evaluating .gemspec
  via subprocess Ruby can execute arbitrary code. Sandbox?
  Proposal: same approach as Node lifecycle scripts — sandbox
  by default, allow opt-out per-gem.

## 15. Dependencies

- Substrate M2 (frontier, multi-feed) → adapter M2
- Substrate M3 (materializer, lockfile, subprocess) → adapter M3

External Go dependencies:

- Custom Ruby DSL parser for Gemfile + minimal .gemspec
  evaluator (~2000 LOC)
- `gopkg.in/yaml.v3` — for .gem metadata.gz parsing,
  ~/.gem/credentials
- `archive/tar` — stdlib for .gem extraction

No dependency on other adapters.
