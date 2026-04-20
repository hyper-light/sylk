# PYTHON_UV.md — Python Adapter Implementation Plan

Tier 1 exemplar — aim: match [uv](https://github.com/astral-sh/uv)
feature set and within ~2× its performance on equivalent workloads,
using the substrate's shared primitives wherever possible.

## 1. Overview

The Python adapter resolves and materializes Python packages from:

- **PyPI** (primary) via the [Simple Repository API](https://peps.python.org/pep-0503/)
  (PEP 503) and [JSON API](https://warehouse.pypa.io/api-reference/json.html)
- **Private PyPI-compatible feeds** (devpi, pip-compatible indexes)
- **Direct URL dependencies** (git repositories, sdist/wheel URLs)
- **Path dependencies** (local source trees)

Produces:

- A resolved dependency tree satisfying PEP 508 requirement specifiers
  against PEP 440 version constraints
- A materialized virtual environment layout or equivalent (site-packages
  hierarchy)
- A `uv.lock`-compatible lockfile (which is itself a superset of
  `requirements.txt`-style pinning)

User-visible behaviors (M3 target):

- `sylk resolve python ./pyproject.toml` → produces `sylk.lock`
- `sylk install python` → materializes venv from `sylk.lock`
- `sylk add python <package> [--version <range>]` → updates
  `pyproject.toml`, re-resolves, updates lockfile
- `sylk upgrade python` → re-resolves ignoring pins, updates lockfile
- `sylk why python <package>` → PubGrub-driven explanation of why a
  package is in the resolved tree

## 2. Data Model

### 2.1 Ecosystem coordinates

```go
// PythonCoordinate is the ecosystem-specific flavor of
// EcosystemCoordinate for Python. Adapters encode these into
// substrate RecipeIDs.
type PythonCoordinate struct {
    Name    string          // canonical, lowercased, normalized per PEP 503
    Version PEP440Version   // parsed PEP 440 version
    Extras  []string        // requested extras (e.g. ["test", "dev"])
    Marker  PEP508Marker    // environment marker that must evaluate true
    URL     string          // optional: direct URL dependency
    Path    string          // optional: local path dependency
    WheelTags WheelTags     // when known, the selected wheel's platform tags
}

// WheelTags represents a wheel's platform compatibility tags per PEP 427.
// Example: cp311-cp311-manylinux_2_17_x86_64
type WheelTags struct {
    Python   string // "cp311", "py3", "any"
    ABI      string // "cp311", "abi3", "none"
    Platform string // "manylinux_2_17_x86_64", "macosx_11_0_arm64", "any"
}
```

### 2.2 Constraint mapping

PEP 508 requirements translate to substrate `Constraint`:

- PEP 508 `package[extras]==version ; marker` → `Constraint` with
  `Name=package`, `Features=extras`, `VersionRange=exact(version)`,
  `Attributes={marker_key: marker_value, ...}`
- `python_requires` from package metadata → `Constraint` on the
  synthetic `python` package, with the declared range

### 2.3 Project manifests

Four entry points the adapter accepts:

- `pyproject.toml` (PEP 621 project metadata + PEP 518 build-system)
- `requirements.txt` (pip-style pinned/ranged requirements)
- `setup.py` / `setup.cfg` (legacy — adapter invokes PEP 517's
  `prepare_metadata_for_build_wheel` to extract requirements without
  running the full build)
- `Pipfile` (pipenv — PEP 508 pinning + lockfile)

All four are parsed into the same internal `ProjectRequirements` type:

```go
type ProjectRequirements struct {
    RootName         string
    RootVersion      string
    MainRequirements []Constraint         // PEP 508
    Extras           map[string][]Constraint // optional feature groups
    PythonRequires   VersionRange         // python_requires constraint
    BuildRequires    []Constraint         // PEP 518 build-system.requires
    Indexes          []FeedReference      // extra-index-url + index-url
}
```

## 3. HTTP Transport

### 3.1 PyPI protocols

Two endpoints per package:

**Simple Repository API (PEP 503)** — HTML page listing all files for a
package:

```
GET https://pypi.org/simple/{package}/
Accept: application/vnd.pypi.simple.v1+json
```

With the JSON Accept header (PEP 691), PyPI returns JSON instead of
HTML:

```json
{
  "meta": { "api-version": "1.0" },
  "name": "requests",
  "files": [
    {
      "filename": "requests-2.31.0-py3-none-any.whl",
      "url": "https://files.pythonhosted.org/.../requests-2.31.0-py3-none-any.whl",
      "hashes": { "sha256": "..." },
      "requires-python": ">=3.7",
      "yanked": false,
      "core-metadata": true
    }
  ]
}
```

Prefer JSON where supported; fall back to HTML parsing for older
mirrors.

**JSON API (legacy)** — per-version metadata:

```
GET https://pypi.org/pypi/{package}/{version}/json
```

Largely redundant with Simple API + `core-metadata=true` (which allows
fetching the `METADATA` file separately), but needed for some
classifiers and legacy mirror compatibility.

### 3.2 Wheel METADATA range-request fetching

The signature optimization uv uses — **mandatory** for this adapter.

Wheels are ZIP archives with a standard layout: `<pkg>-<ver>.dist-info/METADATA`
inside. The METADATA file is typically <10KB; wheels can be tens of
MB. Fetching the whole wheel just to read dependency metadata is
unacceptable at resolve time.

Algorithm:

1. Issue `HEAD` to the wheel URL. Confirm `Accept-Ranges: bytes`.
2. Issue `GET` with `Range: bytes=-65536` — fetches the last 64KB.
   This contains the ZIP central directory (by ZIP format spec, the
   central directory is at the *end* of the archive).
3. Parse the central directory in-memory. Locate the `METADATA`
   entry; extract its byte offset and compressed size.
4. Issue second `GET` with `Range: bytes={offset}-{offset+size-1}`.
5. If the entry is `deflate` compressed (usual), inflate in-memory.
6. Parse the METADATA file (RFC 822-style headers) with the parser
   described in § 4.2.

PEP 658 (core-metadata in Simple API) provides a shortcut — when a
wheel's Simple API entry has `core-metadata: true`, the METADATA file
is available at `<wheel-url>.metadata` as a separate resource. Prefer
this when available (single GET, no ZIP parsing). Fall back to
range-request extraction when not.

```go
// FetchWheelMetadata retrieves a wheel's METADATA file using the
// cheapest available method: PEP 658 side-file if available, else
// range-request ZIP central-directory extraction.
func FetchWheelMetadata(ctx context.Context, client *substrate.HTTPClient, wheelURL string, pep658Available bool) (*PEP427Metadata, error)
```

Benchmarks from uv's design notes: range-request metadata fetch is ~16
KB total transfer for a typical wheel, vs ~20MB for a full numpy
wheel. **Three orders of magnitude bandwidth reduction per candidate**.
This is the single largest contributor to resolver speed for Python.

### 3.3 Authentication

PyPI and most PyPI-compatible feeds use:

- **HTTP Basic auth** (username:password) — common for corporate devpi
  mirrors
- **PyPI API tokens** (`__token__:pypi-...`) — the modern default for
  private packages on PyPI
- **AWS IAM** / **GCS identity** — for S3/GCS-backed private indexes

All route through the substrate's `AuthResolver`. The adapter's only
responsibility is:

- Read `~/.netrc` for per-feed credentials (historical pip convention)
- Honor `UV_INDEX_URL`, `PIP_INDEX_URL`, `PIP_EXTRA_INDEX_URL`
  environment variables
- Support `--index-url` / `--extra-index-url` CLI overrides
- Translate these into `FeedReference` entries with their credentials

## 4. Metadata Layer

### 4.1 Simple Repository API parsing

PEP 691 JSON format is straightforward. HTML fallback uses a strict
HTML parser extracting `<a>` tags per PEP 503:

```go
// SimpleIndexEntry describes one file listed by the Simple API.
type SimpleIndexEntry struct {
    Filename        string
    URL             string
    Hashes          map[string]string // typically {"sha256": "..."}
    RequiresPython  string            // optional python_requires
    Yanked          bool              // PEP 592
    YankedReason    string
    CoreMetadataURL string            // PEP 658 side-file URL if available
    HasMetadata     bool              // PEP 658 flag
}
```

Parse filenames per PEP 427 to extract (name, version, python, abi,
platform, build tag). This is the first filter for platform
compatibility — wheels with incompatible tags are pruned before any
metadata fetch.

### 4.2 METADATA parsing

Wheel METADATA is RFC 822-style but with specific conventions (PEP 566 → PEP 685):

```
Metadata-Version: 2.1
Name: requests
Version: 2.31.0
Summary: Python HTTP for Humans.
Requires-Python: >=3.7
Requires-Dist: charset-normalizer<4,>=2
Requires-Dist: idna<4,>=2.5
Requires-Dist: urllib3<3,>=1.21.1
Requires-Dist: certifi>=2017.4.17
Requires-Dist: PySocks!=1.5.7,>=1.5.6 ; extra == "socks"
Provides-Extra: socks
```

Parser must handle:

- **Repeated fields** (`Requires-Dist` can appear N times)
- **Continuation lines** (a field can span multiple lines)
- **Case-insensitive field names**
- **PEP 508 environment markers** in `Requires-Dist` (`; python_version < "3.11"`)
- **Extras syntax** (`; extra == "socks"`)

```go
type PEP427Metadata struct {
    MetadataVersion string
    Name            string
    Version         PEP440Version
    Summary         string
    RequiresPython  VersionRange
    RequiresDist    []PEP508Requirement
    ProvidesExtra   []string
    // ... other fields as needed
}

type PEP508Requirement struct {
    Name       string
    Extras     []string
    VersionSpec VersionRange
    Marker     PEP508Marker
    URL        string // direct URL dependency
}
```

### 4.3 Marker evaluation

PEP 508 markers evaluate against the target environment:

```
; python_version < "3.11" and sys_platform == "linux"
```

The adapter evaluates markers against the `PlatformTuple` + detected
Python version at resolve time. Markers that evaluate false prune the
requirement entirely.

```go
type PEP508Marker interface {
    Evaluate(env MarkerEnvironment) bool
}

type MarkerEnvironment struct {
    PythonVersion   string
    PythonFullVersion string
    SysPlatform     string
    OsName          string
    PlatformMachine string
    PlatformSystem  string
    ImplementationName string
    ImplementationVersion string
    Extras          []string
}
```

Marker parser is a small PEG grammar (~200 LOC). Reuse
[go-pypi/metadata](https://pkg.go.dev/go-pypi/metadata) or implement
from scratch — probably the latter for full control.

### 4.4 Sdist metadata extraction (PEP 517 metadata-only build)

When only a sdist is available (no wheel), the adapter runs PEP 517's
`prepare_metadata_for_build_wheel` hook, which returns only the
METADATA file without performing the full build. This is cheap (~100ms
per package for Python-only projects; still expensive for C-extension
builds that evaluate setup.py conditionally).

**Crucial rule from uv**: never invoke `setup.py install` or any full
build during resolution. Only extract metadata. Full builds happen at
materialization time.

The PEP 517 metadata build is invoked in a subprocess with the project's
declared `build-system.requires` (from pyproject.toml) pre-installed.
This requires bootstrapping a minimal Python environment — the adapter
delegates this to the substrate's subprocess + environment facilities
but retains control over the Python version used.

When `prepare_metadata_for_build_wheel` is unavailable (ancient sdists
with no pyproject.toml), fall back to running `setup.py egg_info` in a
subprocess to extract `PKG-INFO`. Increasingly rare; treat as a
compatibility fallback path.

## 5. Resolver

### 5.1 PubGrub integration

Use the substrate's `core/resolver/pubgrub` directly. Implement
`DependencyProvider[PythonCoordinate, PEP440Version]`:

```go
type pythonDepProvider struct {
    fetcher    *pypiFetcher
    markerEnv  MarkerEnvironment
    platform   PlatformTuple
    feeds      []FeedReference
    cache      *substrate.MetadataCache
}

func (p *pythonDepProvider) AvailableVersions(ctx context.Context, pkg PythonCoordinate) ([]PEP440Version, error) {
    // 1. Check cache.
    // 2. If miss, Simple API fetch across all feeds in parallel.
    // 3. Filter by requires_python (platform's Python version).
    // 4. Filter out yanked versions (per PEP 592 semantics).
    // 5. Order: newest-first, stable-before-pre-release.
    // 6. Apply lockfile preference (pinned versions first, per substrate
    //    LockfileHints contract).
}

func (p *pythonDepProvider) Dependencies(ctx context.Context, pkg PythonCoordinate, ver PEP440Version) ([]pubgrub.Dependency, error) {
    // 1. Fetch wheel METADATA (range-request or PEP 658 side-file).
    // 2. Parse Requires-Dist.
    // 3. Evaluate markers; drop requirements where marker == false.
    // 4. Expand extras: if this coordinate requested extras, include
    //    requirements gated by `extra == "..."`.
    // 5. Translate PEP 508 requirements → pubgrub.Dependency.
}

func (p *pythonDepProvider) IncompatibleVersions(ctx context.Context, pkg PythonCoordinate) ([]PEP440Version, error) {
    // Yanked versions with user-configurable policy:
    // - skip yanked (default)
    // - allow yanked if in lockfile (historical pin preservation)
    // - include yanked (opt-in, for reproducibility)
}

func (p *pythonDepProvider) Priority(pkg PythonCoordinate) int {
    // Depth-first priority heuristic: prefer packages already partially
    // resolved, prefer packages with fewer candidates. Default is fine
    // for Python.
    return 0
}
```

### 5.2 Frontier implementation

Adapter implements `FrontierAwareResolver`:

```go
func (a *PythonAdapter) ResolveWithFrontier(ctx context.Context, req substrate.ResolveRequest, frontier chan<- substrate.FrontierEvent) (substrate.ResolveResult, error) {
    // Construct pubgrub solver; wire frontier into solver's decision stream.
    // Run the prefetch coordinator in parallel, consuming the frontier.
    // On Considering events, the coordinator kicks off wheel METADATA
    // fetches for candidate versions. On Backtracked events, the
    // context attached to the event is cancelled, aborting in-flight
    // fetches for abandoned branches.
}
```

### 5.3 Extra-index precedence

PyPI-compatible feeds are priority-ordered. Within a single package's
version set, the adapter selects the highest-priority feed that serves
the selected version. If multiple feeds serve the same version with
different file hashes, it's a hard error (`ErrIntegrityMismatch`) —
the resolver surfaces both feeds and asks the user to configure
explicit precedence via feed mapping.

### 5.4 Direct URL / path dependencies

Entries like `foo @ git+https://github.com/foo/bar@1.0.0` or
`foo @ file:///path/to/sdist` bypass the registry for version
discovery but still produce a constraint the solver must satisfy.
The adapter:

1. Fetches the direct URL / path
2. Extracts METADATA via PEP 517 metadata-only build
3. Treats the result as a single synthetic version of the package,
   pinned
4. Proceeds with normal resolution for transitive deps

## 6. Materializer

### 6.1 Virtual environment layout

Produce a PEP 405 virtual environment:

```
{venv}/
  pyvenv.cfg                  # metadata: Python interpreter path, prompt, etc.
  bin/ (Linux/macOS) | Scripts/ (Windows)
    python -> /path/to/cpython-3.11.x/bin/python
    python3 -> python
    pip (stub)                # points at sylk materialization
    {package console scripts} # generated from entry_points
  lib/python3.11/site-packages/
    {package}/
    {package}-{version}.dist-info/
      METADATA
      RECORD
      WHEEL
      entry_points.txt
      LICENSE
      top_level.txt
      INSTALLER            # written "sylk" for provenance
```

### 6.2 Wheel installation

For each resolved wheel:

1. Fetch into substrate recipe store (cached globally by content hash).
2. Extract wheel contents. Wheels are ZIP archives; extract the
   `<name>-<version>.data/purelib/` or `<name>-<version>.data/platlib/`
   tree into `site-packages`.
3. Rewrite paths per the WHEEL file's `Root-Is-Purelib` flag.
4. Generate entry-point scripts in `{venv}/bin/` from `entry_points.txt`.
5. Compute RECORD file (hash of every installed file) and write.
6. Write INSTALLER file with "sylk" marker.

Materialization uses substrate's `LinkReflink` → `LinkHardlink` →
`LinkCopy` fallback chain. For wheels, the cached source is the
*extracted* wheel tree, not the .whl file — linking individual files
is faster than copying from an extracted-on-demand tarball.

### 6.3 Sdist materialization

When only a sdist is available for the resolved version:

1. Fetch sdist to recipe store.
2. Create a temporary build environment with PEP 517
   `build-system.requires` installed.
3. Run `build_wheel` to produce a wheel.
4. Cache the resulting wheel in the recipe store keyed by
   `(sdist_hash, target_platform_tag)` — so future resolves of the
   same sdist on the same platform reuse the build.
5. Install the wheel as in § 6.2.

The build step may take minutes for C-extension packages (numpy,
scipy, etc.); **this is acceptable at materialization time, never at
resolution time**.

### 6.4 Editable installs

PEP 660 editable installs (`pip install -e .`) produce a `.pth` file
in `site-packages` pointing at the source tree. The adapter supports
this via the substrate's path-link primitive. Editable installs are a
materializer concern, not a resolver concern; the resolver treats
them identically to non-editable path dependencies.

### 6.5 Console script generation

For each `entry_points.txt` `[console_scripts]` entry, generate a
`bin/` wrapper script that launches Python with the target module:

- On Linux/macOS: a small Python shebang script
- On Windows: a `.exe` launcher (generated via `distlib`'s launcher
  templates, vendored into the adapter)

## 7. Lockfile

### 7.1 Format

Emit `uv.lock`-compatible TOML. uv's lockfile format is well-designed
and becoming a de-facto Python standard; matching it ensures
interoperability with uv itself for users who want to run `uv sync`
on a Sylk-produced lockfile.

```toml
version = 1
requires-python = ">=3.11"

[[package]]
name = "requests"
version = "2.31.0"
source = { registry = "https://pypi.org/simple" }
dependencies = [
    { name = "charset-normalizer" },
    { name = "idna" },
    { name = "urllib3" },
    { name = "certifi" },
]

[[package.wheels]]
url = "https://files.pythonhosted.org/.../requests-2.31.0-py3-none-any.whl"
hash = "sha256:..."

[package.metadata]
requires-dist = [
    { name = "charset-normalizer", specifier = "<4,>=2" },
    { name = "idna", specifier = "<4,>=2.5" },
    { name = "urllib3", specifier = "<3,>=1.21.1" },
    { name = "certifi", specifier = ">=2017.4.17" },
]
```

### 7.2 LockfileCodec

```go
type pythonLockfileCodec struct{}

func (c *pythonLockfileCodec) Ecosystem() string { return "python" }
func (c *pythonLockfileCodec) Filename() string  { return "sylk.lock" } // or uv.lock-compatible
func (c *pythonLockfileCodec) ReadLockfile(data []byte) (substrate.LockfileSnapshot, error) { ... }
func (c *pythonLockfileCodec) WriteLockfile(snap substrate.LockfileSnapshot) ([]byte, error) { ... }
```

Round-trip tests: `WriteLockfile(ReadLockfile(data))` must produce
byte-identical output. Lockfile drift during re-resolves is a common
complaint; strict round-trip is the easiest way to prevent it.

### 7.3 Hard-preference semantics

Per substrate default: lockfile pins are honored unless they don't
satisfy current constraints. When the lockfile specifies
`requests==2.31.0` and the project's constraints require
`requests>=2.32`, the pin is invalidated and a fresh resolve picks a
new version for requests only — other packages' pins are preserved.

## 8. Substrate Integration

All of the following are substrate-provided:

- `core/resolver/pubgrub` — the solver
- `core/substrate/http` — the HTTP client
- `core/substrate/cache/metadata` — metadata cache
- `core/substrate/store/recipe` — content-addressed recipe store
- `core/substrate/materializer` — link/hardlink/reflink primitives
- `core/substrate/lockfile` — LockfileSnapshot type
- `core/substrate/feeds` — multi-feed federation
- `core/substrate/auth` — AuthResolver
- `core/substrate/telemetry` — structured logging + tracing
- `core/substrate/frontier` — prefetch coordinator

The adapter provides:

- `adapters/python/coordinate.go` — `PythonCoordinate`, encoding/decoding
- `adapters/python/version.go` — `PEP440Version` implementing substrate's `Version`
- `adapters/python/markers.go` — PEP 508 marker parser + evaluator
- `adapters/python/metadata.go` — METADATA parser
- `adapters/python/simple.go` — PEP 503/691 Simple Repository client
- `adapters/python/wheel.go` — wheel format parser + range-request extractor
- `adapters/python/sdist.go` — PEP 517 metadata extraction
- `adapters/python/provider.go` — `DependencyProvider` for PubGrub
- `adapters/python/materializer.go` — venv construction
- `adapters/python/lockfile.go` — `LockfileCodec`
- `adapters/python/adapter.go` — the top-level `Resolver` impl
- `adapters/python/manifest/*.go` — readers for pyproject.toml,
  requirements.txt, setup.py, Pipfile

Estimated total adapter LOC: ~4,000–5,000 lines.

## 9. Error Handling

Python-specific error cases beyond the substrate's taxonomy:

| Condition | Substrate ErrorKind | Notes |
|---|---|---|
| Wheel not compatible with platform | `ErrNoSatisfyingVersion` | Include wheel tags + host tags in explanation |
| All wheels yanked, no sdist | `ErrNoSatisfyingVersion` | Explain yanked reason |
| PEP 517 metadata build failed | `ErrNoSuchRecipe` | Surface subprocess stderr |
| METADATA file corrupted / missing from wheel | `ErrIntegrityMismatch` | Rare; typically a broken mirror |
| requires-python excludes host Python | `ErrNoSatisfyingVersion` | Include python version hint for user |
| Marker evaluation produces empty dep set | Not an error | Informational log |
| Direct URL dep: network failure fetching | `ErrNetworkTransient` | Retry per substrate policy |
| Name normalization collision | `ErrCapabilityConflict` | PEP 503 normalization should prevent, but surface if it occurs |

## 10. Security

### 10.1 Hash verification

Every file downloaded has its SHA-256 hash verified against the
Simple API's declared hash. Hashes come from:

1. Simple API's `hashes` field (present for all modern PyPI packages)
2. The lockfile
3. User-configured hash pinning (`--require-hashes` mode)

Missing hashes in the Simple API response (ancient mirrors) cause a
hard error in strict mode (default for production) and a warning in
loose mode (opt-in for development).

### 10.2 PEP 740 attestations

[PEP 740](https://peps.python.org/pep-0740/) defines attestations
(Sigstore-based) for PyPI packages. The adapter verifies attestations
when present; missing attestations are a warning (not yet widely
deployed) but will graduate to an error once adoption reaches a
threshold.

### 10.3 Yanked version handling

PEP 592 yanking: the Simple API marks versions as `yanked: true` with
an optional reason. Default policy: skip yanked versions during fresh
resolution; honor yanked versions only if explicitly pinned in the
lockfile (so an existing project's lockfile doesn't silently break
when a version is yanked).

### 10.4 Package name typo-squatting

Mitigate via:

- Normalization per PEP 503 (lowercase, hyphen/underscore/period
  unified) — caught collisions at fetch time
- `FeedMapping` to pin known corporate package prefixes to corporate
  feeds (e.g. `@mycompany_*` only from `https://pypi.mycompany.com/`)
- OSV / PyPI security advisory integration (surface known-malicious
  packages as `CapabilityConflict` with severity=critical)

## 11. Testing

### 11.1 Unit tests

- PEP 440 version parser + comparator (corpus: [pypa/packaging](https://github.com/pypa/packaging/blob/main/tests/)
  test vectors, imported verbatim)
- PEP 508 marker parser + evaluator (corpus: same source)
- Simple API HTML + JSON parsers (fixtures: captured pypi.org responses)
- Wheel METADATA parser (fixtures: wheels from top 100 PyPI packages)
- Wheel range-request extraction (synthetic + real wheel fixtures)
- PEP 517 metadata extractor (live subprocess invocation, mocked for fast CI)
- `DependencyProvider` for PubGrub (with mocked fetcher)
- Lockfile round-trip (corpus: `uv.lock` files from real projects)

### 11.2 Integration tests

Canonical projects resolved end-to-end against a [PyPI mirror test
server](https://github.com/pypa/bandersnatch) or recorded fixtures:

- Django 5.x with all dev dependencies
- NumPy+SciPy+Pandas (heavy C-extension ecosystem)
- Flask with `[async]` extras
- A project with direct URL dependencies (git+https)
- A project with conflicting transitive constraints (exercises
  backtracking)
- A project pinned by lockfile (exercises LockfileHints path)

### 11.3 Ecosystem compatibility

A corpus of ~50 real Python projects (top PyPI downloads, notable
open-source) with known-good lockfiles from uv. The adapter must
produce equivalent resolutions. Divergences from uv are flagged for
review and either fixed or documented as intentional differences.

### 11.4 Performance tests

Benchmarks:

- Cold-cache resolve of a 100-dep project (target: <5s, stretch: <3s)
- Warm-cache resolve of same project (target: <500ms)
- Cold-cache resolve of a 500-dep project (target: <15s)
- Full materialization of a 100-dep venv (target: <1s on reflink-capable FS)
- METADATA range-request vs full-wheel fetch comparison (documented
  per-package; target: >95% bandwidth savings on average)

## 12. Performance Targets

| Metric | Target | Stretch |
|---|---|---|
| Cold-cache resolve, 100 deps | <5s | <3s |
| Warm-cache resolve, 100 deps | <500ms | <200ms |
| Wheel METADATA fetch (per pkg) | <200ms | <100ms |
| Venv materialization, 100 deps | <1s | <500ms |
| Peak memory, 500-dep resolve | <250MB | <150MB |
| Lockfile read+validate | <50ms | <20ms |
| Lockfile write (canonical) | <50ms | <20ms |

## 13. Phases

**M0 — Scaffold.** PythonCoordinate / PEP440Version / PEP508Marker
types compile. Trivial tests pass. No network.

**M1 — Online fetch.** Simple API client works against real PyPI.
METADATA range-request extraction works against real wheels. PEP 517
sdist extractor works against local sdists.

**M2 — Resolution.** Full PubGrub integration. Frontier-driven
prefetch. End-to-end resolve of the canonical projects against live
PyPI succeeds. Ecosystem-compat tests green for top 20 projects.

**M3 — Production.** Materializer (venv + wheel install + sdist
build). Lockfile codec. Error surface complete. All ecosystem-compat
tests green. Performance targets met.

**M4 — Hardening.** PEP 740 attestation verification. Telemetry
integrated with substrate. Observability dashboards. Production-ready
for Sylk users.

## 14. Open Questions

- **Sdist build subprocess isolation.** PEP 517 builds invoke
  arbitrary Python code in a subprocess. Sandbox strategy: chroot?
  seccomp? Per-build cgroup? Or trust the user's existing Python
  install? Default: trust, but expose hooks so corporate deployments
  can plug in sandboxing.
- **Python interpreter discovery.** The adapter needs to know which
  Python the target venv uses. Options: `python3` on PATH, explicit
  `--python` flag, `pyproject.toml`'s `requires-python` driving
  discovery through [python-build-standalone](https://github.com/indygreg/python-build-standalone)
  downloads. Proposal: substrate provides a Python discovery module;
  adapter delegates.
- **C-extension cross-compilation.** When resolving on a platform that
  doesn't match the target (e.g. resolving x86_64 deps on ARM64 CI),
  the materializer can't always build sdists cross-platform. Options:
  refuse cross-platform materialization (safest), use cibuildwheel-
  style cross-builds (complex), use pre-built wheels exclusively
  (limiting). Proposal: reject by default, opt-in cross mode for
  advanced users.
- **uv.lock format stability.** uv's lockfile format is still
  evolving (uv 0.5+ has made breaking changes). Commit to a specific
  uv version's format and pin it, or follow uv's HEAD? Proposal: pin
  to a format version, document which uv versions round-trip
  compatibly.
- **Pipfile support priority.** Pipenv adoption is declining; new
  projects prefer uv / Poetry / Hatch. Is Pipfile support day-1 or
  deferred? Proposal: defer to M4; read-only (can consume existing
  Pipfiles but don't generate them).

## 15. Dependencies

- **Substrate must be at M3** before this adapter reaches M3.
- **Substrate PubGrub at M1** unblocks adapter M2.
- **Substrate HTTPClient + MetadataCache at M0** unblocks adapter M1.
- No dependencies on other adapters.

Python-specific Go dependencies (beyond substrate):

- `github.com/BurntSushi/toml` — TOML parsing (pyproject.toml,
  uv.lock)
- `github.com/pelletier/go-toml/v2` — alternative, possibly faster
- Custom wheel/ZIP parser (Go's `archive/zip` is sufficient but
  needs a thin wrapper for range-request ZIP central directory
  parsing)
- Custom PEG parser for PEP 440 + PEP 508 (no viable off-the-shelf
  Go libraries match the semantics strictly enough)
