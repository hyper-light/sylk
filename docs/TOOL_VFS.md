# Tool VFS — Substrate Design

A general, language-agnostic, hermetic, content-addressed virtual filesystem for tools, compilers, libraries, runtimes, and resources that agents need to do work. Strict no-disk policy. Install once, share across every pipeline. Fully self-contained on Linux, macOS, and Windows.

---

## Goal

Sylk's existing `core/purevfs` provides per-pipeline + global in-memory VFS for **agent-authored content** (drafts, edits, proposals). It does not provide a place to install external tooling — pytest, npm packages, Rust toolchains, language servers, formatters, system libraries, custom CLIs. Every pipeline that needs `pytest` must currently install it from scratch into its own VFS, and the strict no-disk execution backend rejects the installation outright. The result: tools cannot be installed at all, and agents fail to do work that depends on them.

The Tool VFS (the **substrate**) closes that gap. It is the shared place where every installable resource lives, content-addressed and refcount-deduplicated across all pipelines, projected into agent process environments through FUSE, with hermetic builds for anything that must be built from source.

The substrate is **not** a Python solution, an npm solution, or any single ecosystem's solution. It is a general mechanism. New languages, new package managers, new tools, and new artifact distribution channels are added by writing **recipes** — declarative content — not by changing substrate code.

---

## Core Principles

- **Strict no-disk for substrate content.** Substrate bytes never persist to host disk. All content lives in process memory, served through FUSE, replayable from registries on restart. The only host-disk concession is a single empty mount-point directory per platform (`/sylk/` on Linux/macOS, `S:\sylk\` on Windows) — flagged explicitly, content-free, one-time.
- **Install once, share many.** Content-addressing dedupes blobs across packages, versions, and ecosystems. Manifest mounting dedupes views across pipelines. Memory cost is `O(unique_bytes)`, not `O(pipelines × packages)`.
- **Recipes are content, the runtime is universal.** Ecosystem knowledge lives in declarative recipes. The substrate runtime knows how to fetch, verify, extract, build, sandbox, project, cache, and dedupe — it does not know what any artifact is for. Adding a new ecosystem = writing recipes.
- **Hermetic by construction on every platform.** The substrate's sandbox is first-class on Linux, macOS, and Windows from day one — different platform primitives, same hermeticity guarantees (no network, no host filesystem, no privilege escalation, deterministic environment).
- **Guardian owns policy.** The substrate is mechanism. Every gate — provisioning a new package, granting sandbox capabilities, allowing disk fallback — routes through Guardian's existing decision pipeline. Org policy lives in Guardian, not in substrate code.
- **Activity Fabric is the observability spine.** Provisioning, attaching, building, evicting, and supply-chain violations all emit fabric activities at appropriate resolution. Other agents see substrate evolution in their ambient context.
- **Disk fallback when provisioning fails.** When the substrate cannot provision a tool (network down, no compatible artifact, build broken), the system can degrade to using the user's host-installed version with explicit user consent and Guardian gating. The no-disk invariant is about us not writing; reading host disk in degraded mode is not the same operation.

---

## High-Level Architecture

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  AGENT PROCESS (engineer / designer / tester / inspector / ...)              │
│   spawn_command("pytest") → execution backend                                │
└──────────────────────────────┬───────────────────────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  EXECUTION BACKEND (substrate-aware)                                         │
│   1. Resolve required tools from pipeline lockfile                           │
│   2. Compose View (Layer 1-5 environment stack)                              │
│   3. Spawn under platform sandbox + FUSE projection                          │
│   4. Capture stdout/stderr/exit                                              │
└──────────────────┬─────────────────────────────────────┬─────────────────────┘
                   │                                     │
                   ▼                                     ▼
┌──────────────────────────────┐   ┌──────────────────────────────────────────┐
│  VIEW                        │   │  SANDBOX (per platform)                  │
│   per-pipeline composition   │   │   Linux:   namespaces + seccomp          │
│   of attached manifests      │   │   macOS:   sandbox-exec + Endpoint Sec   │
│   layout: path → blob_hash   │   │   Windows: AppContainer + WFP            │
│   env exports (5 layers)     │   │   network: kernel-enforced isolation     │
└──────────────────┬───────────┘   └──────────────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  PROJECTION (FUSE)                                                           │
│   read-only:  composes pipeline ⊕ global ⊕ disk ⊕ substrate views           │
│   writable:   per-build scratch projections (in-memory, captured to substrate│
│                on success, discarded on failure)                             │
│   backends:   hanwen/go-fuse (Linux), cgofuse (macOS via macFUSE/FUSE-T,     │
│               Windows via WinFsp)                                            │
└──────────────────┬───────────────────────────────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  SUBSTRATE STORAGE LAYER (process-global, read-only from views)              │
│                                                                              │
│   ┌──────────────────┐    ┌─────────────────────────────────────────┐       │
│   │  ManifestStore   │───▶│  PackageManifest:                       │       │
│   │  PackageID →     │    │    - PackageID (ecosystem,name,version) │       │
│   │  Manifest        │    │    - []FileEntry{path, blob_hash, mode} │       │
│   └──────────────────┘    │    - SourceMetadata, recipe_id          │       │
│                           └──────────────┬──────────────────────────┘       │
│                                          │                                  │
│                                          ▼                                  │
│   ┌──────────────────────────────────────────────────────────────────┐      │
│   │  BlobStore (sharded, refcounted, content-addressed)              │      │
│   │   BlobHash (BLAKE3) → []byte | zstd-compressed | manifest-only   │      │
│   │   tiered:   Hot (uncompressed) ↔ Warm (zstd) ↔ Cold (manifest)   │      │
│   │   eviction: S3-FIFO across tiers + TinyLFU admission control     │      │
│   │   GC:       refcount-zero blobs dropped deterministically        │      │
│   └──────────────────────────────────────────────────────────────────┘      │
└──────────────────┬───────────────────────────────────────────────────────────┘
                   │
                   ▲ provisioning path (network → memory → substrate)
                   │
┌──────────────────┴───────────────────────────────────────────────────────────┐
│  RECIPE RUNTIME                                                              │
│   - Recipe schema (universal, ecosystem-agnostic)                            │
│   - Fetch (HTTPS, git, OCI registry, custom URL schemes, disk:// fallback)   │
│   - Verify (SHA, BLAKE3, signatures, PEP 740 / Sigstore attestations)        │
│   - Extract (zip, tar.*, raw, oci-layer, git-tree, custom extractor plugin)  │
│   - Build (run under sandbox + writable FUSE, capture outputs deterministic) │
│   - Register (compute manifest, register with substrate)                     │
└──────────────────┬───────────────────────────────────────────────────────────┘
                   │
                   ▲ called via
                   │
┌──────────────────┴───────────────────────────────────────────────────────────┐
│  RESOLVERS (per-ecosystem adapters)                                          │
│   (ecosystem, name, constraint) → [recipe_id]                                │
│   PubGrub for Python/Cargo/Hex; npm-arborist for npm; MVS for Go;            │
│   Maven/Gradle resolvers; OPAM for OCaml; Bundler for Ruby; etc.             │
│   Adapters; not load-bearing — substrate doesn't care which ecosystem        │
└──────────────────────────────────────────────────────────────────────────────┘

   ┌──────────────────────────────────────────────────────────────────────┐
   │  LOCKFILE (per-session, CRDT-evolving, fabric-anchored)              │
   │   pinned recipe IDs + resolved closure + witnesses + source hashes   │
   │   monotonic: append-only witnesses; never silent upgrades            │
   │   fabric: tool_pinned / tool_upgraded / supply_chain_violation       │
   └──────────────────────────────────────────────────────────────────────┘

   ┌──────────────────────────────────────────────────────────────────────┐
   │  GUARDIAN POLICY GATES                                               │
   │   provision_substrate_gate: should this package enter the substrate? │
   │   sandbox_capability_gate:  what capabilities does this exec get?    │
   │   disk_fallback_gate:       allow degraded host-disk fallback?       │
   └──────────────────────────────────────────────────────────────────────┘
```

---

## The Five Primitives

The substrate is built from five orthogonal primitives. Every higher-level capability composes them.

### 1. Blob

A content-addressed sequence of bytes, keyed by BLAKE3 hash.

- **Identity = content.** Two blobs with the same bytes are the same blob. Two blobs with different bytes can never have the same hash.
- **Immutable.** Blobs are written once; never modified.
- **Refcounted.** Every manifest reference increments the count; detachment decrements. Refcount zero means no manifest claims this blob — eligible for deterministic drop.
- **Tiered representation.** Hot (uncompressed `[]byte`), Warm (zstd-compressed in RAM with per-ecosystem trained dictionary), Cold (manifest-only — bytes evicted, source URL retained for re-fetch). Tiering is governed by S3-FIFO over Hot/Warm/Cold transitions; existence is governed by refcount.

Hash algorithm: **BLAKE3** (`lukechampine.com/blake3` — pure Go). BLAKE3 is ~5× faster than SHA-256 on commodity CPUs and is the direction the SLSA provenance ecosystem is moving. External hashes (e.g. `wheel_sha256` published by PyPI) are stored alongside for supply-chain verification but the substrate's internal dedup uses BLAKE3.

### 2. Manifest

A typed bundle of file entries describing one installable resource.

```
Manifest {
    id:           ManifestID            // BLAKE3 over the manifest contents
    package_id:   PackageID {ecosystem, name, version}
    files:        []FileEntry {path, blob_hash, mode}
    source:       SourceMetadata {recipe_id, fetch_url, hash, attestation}
    metadata:     {license, ecosystem, original_url, ...}
}
```

- A manifest names a content-addressed *bundle* — pytest 8.3.2's complete file list, or Rust toolchain 1.83.0's complete file list, or zlib 1.3.1's complete build output.
- Manifests are tiny (KB-scale, even for huge packages — they're just hash lists).
- Manifests are themselves content-addressed by `ManifestID`. Two builds of the same recipe with the same inputs produce the same `ManifestID` (this is what makes determinism worth caching).
- The package_id is the human-friendly handle; `(ecosystem, name, version)` looks up the manifest.

### 3. Recipe

A declarative description of how to materialize one resource into the substrate.

```
Recipe {
    id:           RecipeID         // BLAKE3 over the recipe contents
    fetch:        []FetchSpec      // [(source_url, expected_hash, signature?)]
    verify:       VerifySpec       // hash algorithm, attestation chain
    extract:      ExtractSpec      // archive format hint
    requires:     []RecipeID       // other manifests that must be mounted before build
    build:        []BuildStep      // optional; absent = pure data extraction
    determinism:  DeterminismProfile
    output:       []OutputMapping  // (substrate_path, source_path_in_build_tree, mode)
    metadata:     RecipeMetadata
}
```

The recipe is the **only place ecosystem knowledge lives.** The substrate runtime executes recipes; recipe authors describe ecosystems. The substrate ships with a starter library of recipes for common cases and is open-ended forever after.

See [Recipe Examples](#recipe-examples) for concrete cross-language recipes.

### 4. Sandbox

A platform-abstracted execution environment that runs a build (or any command) hermetically:

- **No network** (kernel-enforced on all platforms)
- **No host filesystem** (mount/sandbox isolation on all platforms)
- **No host processes** (PID namespace on Linux; equivalents elsewhere)
- **No privilege escalation** (user namespace on Linux; AppSandbox on macOS; AppContainer on Windows)
- **Only substrate-mounted resources visible**
- **Deterministic environment** (clamped time, locale, hostname, user, etc.)
- **Optional syscall filter** (seccomp-bpf on Linux; equivalent on macOS via Endpoint Security; restricted token + Job Object on Windows)

Three platform backends, one interface. See [Sandbox](#sandbox) for details.

### 5. View

A per-pipeline named composition of substrate manifests, materialized into the agent's spawned process environment.

```
View {
    id:           ViewID            // stable per-pipeline
    mount_root:   /sylk/views/{id}/
    manifests:    []RecipeID        // resolved closure from the lockfile
    layout:       map[Path]BlobHash // composed across all manifests; conflicts surface as errors
    env_exports:  EnvironmentSet    // PATH, lib paths, language env vars, $HOME, etc.
    layer_4:      MountPlan         // canonical-path projections (per platform)
}
```

The View is what an agent's spawned process actually sees. Substrate manifests are content; Views are the assembly. See [View Composition and the Five-Layer Environment Stack](#view-composition-and-the-five-layer-environment-stack) for the integration mechanics.

---

## Recipe Schema (Detailed)

The recipe is the schema-versioned, content-addressable artifact that carries all ecosystem knowledge. The runtime is forward-compatible across recipe versions; recipes declare their schema version.

### Schema (`recipe_v1.go`)

```go
type Recipe struct {
    SchemaVersion int             // currently 1
    ID            RecipeID        // BLAKE3 over the canonicalized recipe
    Fetch         []FetchSpec
    Verify        VerifySpec
    Extract       ExtractSpec
    Requires      []RecipeID
    Build         []BuildStep     // empty for pure data recipes
    Determinism   DeterminismProfile
    Output        []OutputMapping
    Metadata      RecipeMetadata
}

type FetchSpec struct {
    Source      string            // https://, git://, oci://, github-release://, disk:// (fallback)
    Hash        Hash              // expected hash (algorithm + digest)
    Signature   *Signature        // optional: PEP 740, Sigstore, plain GPG
    Headers     map[string]string // optional: auth headers, etc.
    PlatformPin *PlatformTuple    // optional: only fetch on this (os, arch, libc)
}

type VerifySpec struct {
    HashAlgorithm   string         // "sha256" | "sha512" | "blake3"
    AttestationChain []Attestation // optional: SLSA / Sigstore / PEP 740 chain
    PolicyClass     string         // "strict" | "advisory" | "permissive"
}

type ExtractSpec struct {
    Format       string  // "zip" | "tar.gz" | "tar.xz" | "tar.zst" | "raw" | "oci-layer" | "git-tree" | "custom:<id>"
    StripPrefix  string  // optional: drop leading path component(s) (e.g. "rust-1.83.0/")
    PreserveMode bool    // honor mode bits from archive
    Filter       *Filter // optional: include/exclude patterns
}

type BuildStep struct {
    Cmd           []string                 // argv
    Env           map[string]string        // additional env (composes with sandbox base env)
    Cwd           string                   // working directory inside writable projection
    SandboxConfig *SandboxStepOverride     // optional: override default sandbox capabilities for this step
    Timeout       time.Duration
}

type DeterminismProfile struct {
    SourceDateEpoch  int64             // default 0
    Locale           string            // default "C"
    Hostname         string            // default "substrate"
    User             string            // default "builder"
    Home             string            // default "/sylk/home"
    MtimePolicy      string            // "clamp" (set all to SourceDateEpoch) | "preserve"
    CompilerFlags    []string          // additional flags injected (-frandom-seed, -fdebug-prefix-map, etc.)
    LinkerFlags      []string
    EnvScrub         []string          // env vars to remove entirely
    EnvAdd           map[string]string // additional env clamps
}

type OutputMapping struct {
    SubstratePath  string // canonical path the manifest claims (e.g. /bin/rg, /lib/libz.so.1)
    SourcePath     string // path in the writable projection (build tree)
    Mode           Mode   // explicit mode | "preserve" (preserve from build tree)
    Symlink        bool   // if true, capture as symlink rather than blob ref
}

type RecipeMetadata struct {
    Name        string            // human-friendly name (e.g. "ripgrep")
    Version     string            // human-friendly version (e.g. "14.1.0")
    Ecosystem   string            // "rust" | "python" | "npm" | "system-binary" | "custom" | ...
    License     string            // SPDX expression
    Homepage    string
    Description string
    Tags        []string
}
```

### Recipe Examples

Examples spanning ecosystems intentionally so no single language drives the design.

**Prebuilt language toolchain (Rust, no build):**

```yaml
schema_version: 1
fetch:
  - source: https://static.rust-lang.org/dist/rust-1.83.0-{platform_triple}.tar.gz
    hash: { algorithm: sha256, digest: a1b2c3... }
verify:
  hash_algorithm: sha256
  policy_class: strict
extract:
  format: tar.gz
  strip_prefix: rust-1.83.0-{platform_triple}/
  preserve_mode: true
requires: []
build: []
output:
  - { substrate_path: "/", source_path: "/", mode: preserve }
metadata:
  name: rust
  version: 1.83.0
  ecosystem: rust
  license: "MIT OR Apache-2.0"
```

**Prebuilt CLI from GitHub Releases (ripgrep):**

```yaml
schema_version: 1
fetch:
  - source: https://github.com/BurntSushi/ripgrep/releases/download/14.1.0/ripgrep-14.1.0-{platform_triple}.tar.gz
    hash: { algorithm: sha256, digest: <from SHASUMS> }
extract:
  format: tar.gz
  strip_prefix: ripgrep-14.1.0-{platform_triple}/
output:
  - { substrate_path: "/bin/rg", source_path: "/rg", mode: 0755 }
  - { substrate_path: "/share/man/man1/rg.1", source_path: "/doc/rg.1", mode: 0644 }
metadata: { name: ripgrep, version: "14.1.0", ecosystem: system-binary, license: "Unlicense OR MIT" }
```

**C library built from source (zlib — same shape for any C/C++ project):**

```yaml
schema_version: 1
fetch:
  - source: https://zlib.net/zlib-1.3.1.tar.gz
    hash: { algorithm: sha256, digest: <pinned> }
extract:
  format: tar.gz
  strip_prefix: zlib-1.3.1/
requires:
  - recipe_id: <gcc-bootstrap-{platform_triple}>
  - recipe_id: <make-4.4>
  - recipe_id: <posix-shell>
build:
  - cmd: ["./configure", "--prefix=/output"]
    env: { CC: gcc, SOURCE_DATE_EPOCH: "0" }
  - cmd: ["make", "-j1"]
  - cmd: ["make", "install", "DESTDIR=/output"]
determinism:
  source_date_epoch: 0
  locale: C
  hostname: substrate
  user: builder
  mtime_policy: clamp
  compiler_flags: ["-frandom-seed=zlib-1.3.1", "-fdebug-prefix-map=/sylk/build=/sylk/build"]
output:
  - { substrate_path: "/lib/libz.so.1.3.1", source_path: "/output/lib/libz.so.1.3.1", mode: 0755 }
  - { substrate_path: "/lib/libz.so.1",     source_path: "/output/lib/libz.so.1",     mode: 0755, symlink: true }
  - { substrate_path: "/include/zlib.h",    source_path: "/output/include/zlib.h",    mode: 0644 }
metadata: { name: zlib, version: "1.3.1", ecosystem: c-library, license: Zlib }
```

**OCaml library via dune:**

```yaml
schema_version: 1
fetch:
  - source: https://github.com/janestreet/base/archive/refs/tags/v0.17.0.tar.gz
    hash: { algorithm: sha256, digest: <pinned> }
extract:
  format: tar.gz
  strip_prefix: base-0.17.0/
requires:
  - <ocaml-5.2.0>
  - <dune-3.16.0>
  - <ocaml-stdio-v0.17.0>
build:
  - cmd: ["dune", "build", "--release"]
output:
  - { substrate_path: "/lib/ocaml/base/", source_path: "/_build/install/default/lib/base/", mode: preserve }
metadata: { name: base, version: "v0.17.0", ecosystem: ocaml, license: MIT }
```

**Python package built from sdist (any ecosystem with build backends — same shape):**

```yaml
schema_version: 1
fetch:
  - source: https://files.pythonhosted.org/packages/.../pytest-8.3.2.tar.gz
    hash: { algorithm: sha256, digest: <pinned> }
extract:
  format: tar.gz
  strip_prefix: pytest-8.3.2/
requires:
  - <cpython-3.13.0>
  - <python-setuptools-75.0.0>
  - <python-wheel-0.45.0>
build:
  - cmd: ["python", "-m", "build", "--wheel", "--no-isolation"]
output:
  - { substrate_path: "/python/site-packages/pytest/", source_path: "/dist/extracted/pytest/", mode: preserve }
  - { substrate_path: "/bin/pytest",                   source_path: "/dist/extracted/pytest-bin", mode: 0755 }
metadata: { name: pytest, version: "8.3.2", ecosystem: python, license: MIT }
```

**Custom internal artifact:**

```yaml
schema_version: 1
fetch:
  - source: https://artifacts.acme.internal/cli/2.4.1/cli-{platform_triple}.tar.zst
    hash: { algorithm: sha512, digest: <pinned> }
    headers: { Authorization: "Bearer ${ACME_TOKEN}" }
extract:
  format: tar.zst
output:
  - { substrate_path: "/bin/acme-cli", source_path: "/cli", mode: 0755 }
metadata: { name: acme-cli, version: "2.4.1", ecosystem: custom, license: Proprietary }
```

**Disk fallback (when substrate provisioning fails):**

```yaml
schema_version: 1
fetch:
  - source: disk:///usr/bin/python3
    hash: { algorithm: blake3, digest: <computed at fetch time> }
extract:
  format: raw
output:
  - { substrate_path: "/bin/python3", source_path: "/", mode: preserve }
metadata: { name: python3, version: "host-disk-fallback", ecosystem: disk-fallback, license: unknown }
```

The `disk://` fetcher computes the hash at fetch time, registers it in the lockfile as the host-fallback pin, and emits a `disk_fallback_used` activity. Guardian gates whether `disk://` is allowed at all per pipeline.

### Recipe Identity and Reproducibility

```
RecipeID := BLAKE3(canonicalize(recipe_yaml_excluding_metadata_description))
```

Two recipes with identical fetch + verify + extract + requires + build + determinism + output produce identical `RecipeID`s, regardless of cosmetic metadata differences. This makes recipes the unit of cache lookup: `RecipeID → ManifestID` is a function under the determinism profile.

---

## Resolvers (Per-Ecosystem Adapters)

Resolvers translate ecosystem-native version constraints into recipe ID lists. Every resolver implements:

```go
type Resolver interface {
    // Resolve takes ecosystem-native constraints and returns the closure of recipe IDs
    // that satisfy them, in topological dependency order.
    Resolve(ctx context.Context, request ResolveRequest) (ResolveResult, error)

    // Ecosystem returns the resolver's ecosystem identifier (e.g. "python", "npm", "cargo").
    Ecosystem() string
}

type ResolveRequest struct {
    Constraints []Constraint     // (name, version_constraint) pairs
    PlatformTuple PlatformTuple  // (os, arch, libc)
    LockfileHints LockfileSnapshot // existing pins (CRDT context)
}

type ResolveResult struct {
    Closure        []RecipeID          // topologically sorted: dependencies first
    PinJustifications map[RecipeID]string // why this version (PubGrub explanation, etc.)
    Conflicts      []ConstraintConflict // any unresolvable constraints
}
```

Resolvers shipped with the substrate:

| Resolver | Ecosystem | Backing Algorithm |
|---|---|---|
| `python` | PyPI | PubGrub |
| `cargo` | crates.io | PubGrub-derived |
| `npm` | npm registry | npm-arborist (peer-dep semantics) |
| `go` | GOPROXY | MVS |
| `gem` | RubyGems | Bundler's |
| `maven` | Maven Central | Maven's resolver |
| `nuget` | NuGet.org | NuGet's resolver |
| `hex` | Hex.pm | Hex's resolver |
| `opam` | OPAM | OPAM's solver |
| `github-release` | GitHub Releases | direct (single pinned version) |
| `oci` | OCI registry | direct |
| `custom-https` | arbitrary HTTPS | direct |
| `disk-fallback` | host disk | direct |

**Resolvers are adapters, not the load-bearing primitive.** The substrate doesn't care which solver picked the recipe IDs. The lockfile stores the resolved closure; resolvers can be swapped or extended without touching substrate code.

---

## Sandbox

The sandbox abstracts platform-specific isolation primitives behind a uniform interface.

```go
type Sandbox interface {
    Spawn(ctx context.Context, cfg SandboxConfig) (*Process, error)
}

type SandboxConfig struct {
    Mounts        []Mount        // FUSE projections (read-only substrate views, writable scratch)
    Env           map[string]string
    Network       NetworkPolicy  // None | LimitedToSubstrate
    Capabilities  CapabilitySet  // file system, syscalls, etc. — Guardian-gated
    Determinism   DeterminismProfile
    Cmd           []string
    Cwd           string
    Stdin         io.Reader
    Stdout, Stderr io.Writer
    Timeout       time.Duration
    MemoryCap     uint64         // bytes
    CPUCap        float64        // cores
}

type Process struct {
    PID      int
    Wait     func() (ExitStatus, error)
    Cancel   func() error
}
```

### Linux Backend (`core/substrate/sandbox/linux.go`)

Production target — full kernel-enforced hermeticity.

- `unshare(CLONE_NEWNS | CLONE_NEWNET | CLONE_NEWPID | CLONE_NEWUSER | CLONE_NEWUTS | CLONE_NEWIPC)`
- Mount-namespace-private FUSE projection mounted at View root
- Bind-mount canonical paths from View into `/usr`, `/opt`, `/etc` inside the namespace (host disk unaffected)
- Empty network namespace (no interfaces) when `Network = None`
- `seccomp-bpf` filter restricting syscalls to allow-set
- User namespace for unprivileged operation
- Resource limits via cgroups v2 (`memory.max`, `cpu.max`)

Established stack — bubblewrap, Firejail, Docker rootless all use this exact pattern.

### macOS Backend (`core/substrate/sandbox/darwin.go`)

Apple-platform hermeticity using native primitives.

- `sandbox-exec` profile restricting filesystem and network access
- FUSE mount via cgofuse provides projection at View root
- Per-process socket filter via Network Extension framework or `pf`-based per-PID rule injection
- Endpoint Security framework for syscall-level monitoring
- Restricted entitlements
- `posix_spawn` with restricted spawn attributes

For native code that hardcodes library paths: `DYLD_INSERT_LIBRARIES` interposer dylib (the substrate ships a tiny dylib that intercepts `open`/`stat` on canonical paths and redirects to the View; respects hardened-runtime restrictions).

### Windows Backend (`core/substrate/sandbox/windows.go`)

Windows hermeticity via AppContainer + Job Objects.

- AppContainer with explicit capability set (Win10+) — capability-based security
- Job Object with `JOB_OBJECT_LIMIT_NETWORK_RATE_CONTROL = 0` for network kill, plus WFP filter for hard block
- WinFsp mount provides projection
- Restricted token (`SE_GROUP_USE_FOR_DENY_ONLY`) for privilege drop
- AppContainer's filesystem virtualization keeps the process from seeing host files
- Per-process drive mappings via `DefineDosDevice` with `DDD_NO_BROADCAST_SYSTEM`

### Network Isolation

| Platform | Mechanism | Hardness |
|---|---|---|
| Linux | Empty network namespace | Kernel-enforced; namespace has no interfaces |
| macOS | Network Extension content filter or `pf` per-PID rule | Kernel-enforced; in network stack, not user-bypassable |
| Windows | WFP per-process filter + Job Object rate-limit-zero | Kernel-enforced; WFP is in kernel network stack |

All three are kernel-enforced. None depend on env vars or proxy settings. All three result in `connect(2)` (or Win32 equivalent) failing for any address.

### Determinism Across Platforms

| Control | Linux | macOS | Windows |
|---|---|---|---|
| `SOURCE_DATE_EPOCH` | env var, GCC/Clang/etc. respect | same | same |
| FUSE mtime clamping | our code, identical | identical | identical |
| `LC_ALL=C` | standard | standard | `LANG=C.UTF-8`, equivalent |
| Compiler `-frandom-seed` | native | native | MSVC `/experimental:deterministic` |
| Linker symbol ordering | `ld.lld --sort-section=name` | same (lld works on macOS) | `link.exe /Brepro` |
| Path normalization | `-fdebug-prefix-map=$build=/sylk` | same | MSVC `/PATHMAP` |
| PE timestamp zeroing | n/a | n/a | `dotnet build /Deterministic` style post-process |

Cross-host deterministic caching: cache key includes target tuple `(target_os, target_arch, target_libc)`, not host tuple. Same `(source_manifest, build_inputs, target_platform)` → same `output_manifest` regardless of which host built it.

---

## Writable FUSE Projection

For build steps. The substrate is read-only from agent processes; build steps need a writable scratch space that *also* respects the no-disk invariant.

The build runs against a **composed view** of two FUSE projections:

```
read-only substrate layer:
  /sylk/src/        ← source tarball extracted from substrate manifest
  /sylk/toolchain/  ← compiler + linker + make + sh from substrate manifests
  /sylk/deps/       ← dependency closure manifests
  /sylk/include/    ← system headers from libc-headers manifest
  /sylk/lib/        ← system libraries (libc, libpthread, etc.)

writable in-memory FUSE layer (per-build, scoped to invocation):
  /sylk/build/      ← intermediate object files, build artifacts
  /sylk/output/     ← final outputs the build is expected to produce
  /sylk/tmp/        ← scratch tmpfs replacement (POSIX TMPDIR)
  /sylk/home/       ← deterministic HOME for tools that probe it
```

The build (`./configure && make && make install DESTDIR=/output`) sees a normal POSIX filesystem. Writes to `/sylk/build`, `/sylk/output`, `/sylk/tmp`, `/sylk/home` go to RAM-backed FUSE state inside our Go process. Writes to substrate paths return `EROFS`.

**On build success:** walk the captured `/sylk/output` tree, BLAKE3-hash every file, register them as substrate blobs, emit a new manifest from the recipe's `output` mapping. Discard the entire build-scratch state. The new manifest is now substrate-resident and indistinguishable from a prebuilt one.

**On build failure:** discard scratch wholesale. Substrate is unchanged. Agent receives the build's stdout/stderr + exit code as a normal command failure.

Memory cost: peak scratch size for a typical build (e.g. CPython) is ~2GB. Mitigations:

- Cap per-build scratch size; reject builds exceeding cap with structured error
- Spill cold-but-needed scratch to the substrate's Warm tier (zstd-compressed in RAM)
- Stream linker output extraction directly into substrate as the linker writes (instead of holding both the writable FUSE copy and the substrate copy)

---

## View Composition and the Five-Layer Environment Stack

A View is the integration unit between substrate manifests and a spawned process environment. Without it, the substrate is invisible to anything that does the universal "look up `pytest` on `PATH`" or "`dlopen("libssl.so.3")`" call.

### Layer 1 — `PATH` and Friends

```
PATH            = /sylk/views/{view_id}/bin:$ORIGINAL_PATH
MANPATH         = /sylk/views/{view_id}/share/man:$ORIGINAL_MANPATH
PKG_CONFIG_PATH = /sylk/views/{view_id}/lib/pkgconfig:$ORIGINAL_PKG_CONFIG_PATH
ACLOCAL_PATH    = /sylk/views/{view_id}/share/aclocal:$ORIGINAL_ACLOCAL_PATH
INFOPATH        = /sylk/views/{view_id}/share/info:$ORIGINAL_INFOPATH
```

Substrate first; original last. Substrate shadows host tools when present; host remains as fallback when not provisioned.

Handles: tools that resolve binaries through `PATH` — the vast majority of modern tooling.

### Layer 2 — Library Lookup Paths

```
Linux:   LD_LIBRARY_PATH     = /sylk/views/{view_id}/lib:$ORIG
macOS:   DYLD_LIBRARY_PATH   = /sylk/views/{view_id}/lib:$ORIG
         DYLD_FRAMEWORK_PATH = /sylk/views/{view_id}/Frameworks:$ORIG
Windows: PATH (already covered) + per-binary "DLL search order"
```

Handles: dynamic linking of shared libraries.

### Layer 3 — Per-Language Environment Variables

Each language ecosystem has its own search path env var. The View's layout knows which manifests target which ecosystem and exports accordingly:

```
PYTHONPATH       = /sylk/views/{view_id}/python/site-packages
NODE_PATH        = /sylk/views/{view_id}/node_modules
GEM_PATH         = /sylk/views/{view_id}/ruby/gems
RUBYLIB          = /sylk/views/{view_id}/ruby/lib
JAVA_HOME        = /sylk/views/{view_id}/jdk
CLASSPATH        = /sylk/views/{view_id}/jvm-jars:$ORIG
GOPATH           = /sylk/views/{view_id}/go
GOMODCACHE       = /sylk/views/{view_id}/go/mod-cache
CARGO_HOME       = /sylk/views/{view_id}/cargo
RUSTUP_HOME      = /sylk/views/{view_id}/rustup
OCAMLPATH        = /sylk/views/{view_id}/ocaml
ERL_LIBS         = /sylk/views/{view_id}/erlang
MIX_PATH         = /sylk/views/{view_id}/elixir
HEX_HOME         = /sylk/views/{view_id}/hex
KOTLIN_HOME      = /sylk/views/{view_id}/kotlin
HASKELL_GHC_PATH = /sylk/views/{view_id}/ghc
... etc
```

This list is **content** (recipe-declared), not substrate code. Each recipe declares which env var its outputs participate in. Adding a new ecosystem = adding env-var declarations to its recipes.

### Layer 4 — Mount Namespace Projection at Canonical Paths

For tools that hardcode absolute paths (`#!/usr/bin/python3` shebangs, `dlopen("/usr/lib/.../libssl.so.3")`, build scripts that grep `/usr/include`).

**Linux** (full namespace projection):
- `unshare(CLONE_NEWNS)` — private mount namespace
- `mount --make-rprivate /` — mount events don't propagate to host
- FUSE-mount the View at `/sylk/views/{view_id}/`
- Bind-mount `/sylk/views/{view_id}/usr → /usr`, `/sylk/views/{view_id}/opt → /opt`, `/sylk/views/{view_id}/etc → /etc` — all read-only, in-namespace only
- Host disk unaffected; mounts only exist in this process subtree's namespace
- `#!/usr/bin/python3` resolves to the View's substrate-projected python3

**macOS** (no mount namespaces — two compensating mechanisms):
- **Shebang rewriting at substrate extraction:** when a recipe's output contains a script with a hardcoded shebang to a system path, the substrate detects and rewrites to `#!/usr/bin/env <interpreter>` at extraction. Documented; reproducible (rewrite is part of the manifest's content-addressing).
- **`DYLD_INSERT_LIBRARIES` interposer dylib:** for native code hardcoding library paths, substrate ships a tiny interposer dylib that intercepts `open()`/`stat()` on canonical paths and redirects to the View. Works on Apple-signed and ad-hoc-signed binaries; fails gracefully on hardened-runtime/notarized binaries (those refuse `DYLD_INSERT_LIBRARIES`).

**Windows** (per-process drive mappings):
- Mount the View as per-process drive letter via WinFsp (`S:\sylk\views\{view_id}\`)
- Per-process drive mappings via `DefineDosDevice` with `DDD_NO_BROADCAST_SYSTEM` flag — mapping exists only for processes spawned from this session
- For tools hardcoding `C:\Program Files\...`: per-process `KnownFolder` redirection via `IKnownFolderManager` (Windows 7+); or AppContainer's filesystem virtualization

### Layer 5 — Auxiliary State Directories

Tools look for config and state in conventional locations:

```
HOME             = /sylk/views/{view_id}/home
XDG_CONFIG_HOME  = /sylk/views/{view_id}/config
XDG_CACHE_HOME   = /sylk/views/{view_id}/cache  (writable FUSE)
XDG_DATA_HOME    = /sylk/views/{view_id}/data   (writable FUSE)
TMPDIR           = /sylk/views/{view_id}/tmp    (writable FUSE — replaces /tmp)
TEMP, TMP        = (Windows equivalents)
```

Writable layers use the same writable-FUSE-projection mechanism as build sandboxes.

### Discovery

A new skill: `query_view(view_id)` returns:

```json
{
    "view_id": "pipe-7",
    "manifests_attached": [
        {"recipe_id": "...", "ecosystem": "python", "version": "3.13.0", "claimed_paths": [...]},
        ...
    ],
    "available_binaries":    [{"name": "pytest", "path": "/bin/pytest", "source_recipe": "..."}],
    "available_libraries":   [{"soname": "libssl.so.3", "path": "/lib/...", "source_recipe": "..."}],
    "available_python_pkgs": [{"name": "pytest", "version": "8.3.2", "path": "/python/site-packages/pytest"}],
    "env_exports":           {"PATH": "...", "LD_LIBRARY_PATH": "...", ...}
}
```

Agents can also just run `which X` or `command -v X` under the spawned env — they work because Layer 1 ensured `PATH` includes the View's `bin/`.

### View Composition Hashing

```
view_hash = BLAKE3(
    sorted(manifest_recipe_ids) ||
    composed_layout ||
    env_export_set ||
    layer_4_mount_plan
)
```

Two pipelines with the same lockfile resolve produce the same `view_hash`. View construction is cached; re-spawning into the same View skips composition.

---

## Lockfile

Per-session canonical pin file, persisted in the substrate WAL (not on disk). CRDT-evolving, fabric-anchored, content-addressed.

### Schema

```json
{
    "schema_version": 1,
    "session_id": "...",
    "ecosystems": {
        "python": {
            "pytest": {
                "version": "8.3.2",
                "manifest_id": "...",
                "recipe_id": "...",
                "blob_root": "blake3:...",
                "source": {
                    "url": "https://files.pythonhosted.org/.../pytest-8.3.2-py3-none-any.whl",
                    "fetch_hash": "sha256:...",
                    "wheel_hash": "sha256:...",
                    "pep740_attestation": "..."
                },
                "pinned_at": "2026-04-19T16:23:18Z",
                "pinned_by_pipelines": ["pipe-7", "pipe-12"],
                "constraint_witnesses": [
                    { "pipeline": "pipe-7",  "constraint": ">=8.0,<9.0", "added_at": "..." },
                    { "pipeline": "pipe-12", "constraint": ">=8.3",       "added_at": "..." }
                ],
                "transitive_closure": ["pluggy@1.5.0", "iniconfig@2.0.0", ...]
            },
            "pluggy": { ... }
        },
        "rust": { ... },
        "system-binary": { ... }
    }
}
```

### CRDT Semantics

- **Monotonic and append-only** with respect to witnesses. Multiple pipelines run the constraint solver concurrently; each appends its witness; never deletes others'.
- **Solver output is deterministic** given the witness set, so concurrent solves converge.
- **Witnesses expire** when their pipeline terminates. When all witnesses for a manifest expire, refcount drops, substrate GC reclaims.
- **No silent upgrades.** Once a witness exists for `pytest 8.3.2`, that pin stays for the lifetime of any pipeline witnessing it. New pipelines may pin newer versions; old pipelines keep theirs.
- **Side-by-side versions are nearly free** because content-addressed dedup means pytest 8.3.1 and 8.3.2 share ~95% of their blobs.

### Constraint Resolution

PubGrub for ecosystems where it fits (Python, Cargo, Hex). Native ecosystem resolvers for those where it doesn't (npm peer-deps, Go MVS, Maven, NuGet, OPAM, Bundler). All produce the same shape: a list of recipe IDs in topological order. The lockfile stores the resolved closure; explanations from the solver are captured verbatim in `pin_justifications` so "why this version?" always has a structured answer.

### Supply Chain Integrity

- Every artifact pinned by `fetch_hash` and (when available) ecosystem-specific hash (`wheel_hash`, `npm-shasum`, `gosum`).
- Every artifact verified against pinned hash on every fetch — registry serving different bytes triggers `supply_chain_violation` activity and refusal.
- PEP 740 attestations / Sigstore / SLSA provenance captured where available, validated against trust roots configured in Guardian.
- The lockfile pin is the trust anchor — the registry cannot silently substitute different bytes.

### Fabric Anchoring

Every solver output emits an Activity Fabric event at Fine resolution:

- `tool_pinned`        — first-time pin of `(ecosystem, name, version)` in this session
- `tool_upgraded`      — version pin updated (witness compatibility allowed it)
- `tool_witness_added` — existing pin gained a new witness pipeline
- `supply_chain_violation` — fetch hash mismatch, attestation failure, etc.

Scoped to `tooling/{ecosystem}/{name}` for clean scope-based fabric queries. Other agents see substrate evolution in their next ambient context envelope. Designer pins react@18.3 → engineer's `query_peer_activity` surfaces it next turn → engineer adapts before issuing a divergent install.

---

## Tiered Cache

Three orthogonal mechanisms govern blob storage cost.

### Mechanism A — Refcount-driven existence

A blob's existence is governed by manifest references. When the last manifest referencing a blob is detached (last pipeline finished, last lockfile witness expired), refcount drops to zero, blob is **deterministically dropped**. No eviction algorithm involved. Provably correct because content-addressing means a re-fetch produces bit-identical bytes.

### Mechanism B — S3-FIFO-driven representation tiering

While refcount > 0, blob exists in one of three forms:

| Tier | Form | Read cost | Default cap |
|---|---|---|---|
| Hot | uncompressed `[]byte`, served via FUSE passthrough | nanoseconds | 25% sysmem |
| Warm | zstd-compressed in RAM (per-ecosystem trained dict) | microseconds (decompress on read) | 35% sysmem |
| Cold | manifest-only; blob hash + source URL retained | seconds (re-fetch from registry) | unbounded (KB/blob) |

S3-FIFO governs Hot ↔ Warm ↔ Cold transitions. Small/main/ghost queues map onto: small = recently-promoted (probationary), main = stable-hot, ghost = recently-demoted (so an immediate re-read cheaply re-promotes from Warm rather than from network).

S3-FIFO chosen over SIEVE for one specific reason: workload is bursty + scan-heavy (test runs touch many blobs in fast succession, then idle). S3-FIFO's small/ghost queues are explicitly designed to resist scan pollution; SIEVE is more vulnerable to one-shot scans. Both beat W-TinyLFU on production traces; S3-FIFO has the edge on scan resistance for our read pattern.

### Mechanism C — TinyLFU admission control

A 4-bit count-min sketch governs Cold → Warm promotions. Touched <2 times in last 1000 reads → don't promote, serve from re-fetch. Kills the failure mode where an agent does `glob('**/*.py')` over the substrate and trashes the working set.

### Per-Ecosystem zstd Dictionaries

Train one zstd dictionary per `(ecosystem, major-version)` from a corpus sample. The dictionary itself is ~64KB. Python source files compress 5-8× with a Python-trained dict vs ~3× with no dict. Dict cost amortized across thousands of blobs. Dictionaries persist in substrate WAL.

---

## Guardian Policy Gates

The substrate is mechanism; Guardian is policy. Every consequential decision routes through Guardian's existing gating pipeline.

### Gate 1 — Provisioning Gate

Before fetching any package into the substrate:

```
substrate.provision(ecosystem=python, name=pytest, constraint=">=8") →
  resolves to pytest 8.3.2, wheel SHA256 = X, MIT license, transitive = [...]
                ↓
Guardian skill: provision_substrate_gate({
    ecosystem: python,
    package: pytest@8.3.2,
    source_url: "https://files.pythonhosted.org/.../...whl",
    artifact_sha256: X,
    license: MIT,
    transitive_closure: [...],
    requires_build_scripts: false,
    requires_install_scripts: false,
    pep740_attestation: <provenance bundle if present>,
})
                ↓
Guardian evaluates against org policy:
  - Allowed registry? Blocked package list? License acceptable?
  - Hash matches what registry advertises? Attestation valid?
  - Transitive closure clean? (any blocked deps?)
                ↓
Verdict: APPROVED | DENIED | ESCALATE_TO_USER
```

### Gate 2 — Sandbox Capability Gate

Every sandbox spawn calls Guardian for capability grants:

```
substrate.exec.Run(cmd=["python", "-m", "pytest"], pipeline_view=pipe-7)
                ↓
substrate proposes SandboxConfig:
    mounts: [/sylk/python-3.13/, /sylk/pipeline-7/]
    network: NONE
    syscall_set: STANDARD
    memory_cap: 4GB
    timeout: 300s
    purpose: "test_execution"
                ↓
Guardian skill: sandbox_capability_gate({proposal})
                ↓
Guardian evaluates:
  - Is pipeline-7 allowed to execute Python? (per-pipeline authority profile)
  - Are the mounts within pipeline-7's authority scope?
  - Does NETWORK=NONE match the policy for this purpose?
  - Resource caps match org limits?
                ↓
Verdict: APPROVED | APPROVED_WITH_CAVEATS | DENIED | ESCALATE
```

### Gate 3 — Disk Fallback Gate

When the substrate cannot provision a tool and disk fallback is requested:

```
substrate.fallback_to_disk(tool=python3, reason="no compatible build for platform")
                ↓
Guardian skill: disk_fallback_gate({
    tool: python3,
    host_path: /usr/bin/python3,
    host_hash: blake3:...,
    reason: "no compatible build for platform"
})
                ↓
Guardian evaluates:
  - Is disk fallback allowed for this pipeline?
  - Does the host binary's hash match an approved list?
  - Has the user explicitly consented to disk fallback this session?
                ↓
Verdict: APPROVED | DENIED | ASK_USER
```

Org policy lives in Guardian configuration, not in substrate code. The substrate enforces whatever Guardian decides.

---

## Activity Fabric Integration

Substrate evolution is observable through the existing Activity Fabric.

### Activities Emitted

| Activity | Resolution | Scope | Fired When |
|---|---|---|---|
| `tool_pinned` | Fine | `tooling/{ecosystem}/{name}` | First-time pin in session |
| `tool_upgraded` | Fine | `tooling/{ecosystem}/{name}` | Version pin updated |
| `tool_witness_added` | Atomic | `tooling/{ecosystem}/{name}` | Existing pin gained new witness |
| `tool_attached` | Atomic | `pipeline/{pipeline_id}` | Manifest attached to View |
| `tool_detached` | Atomic | `pipeline/{pipeline_id}` | Manifest detached from View |
| `provisioning_started` | Fine | `tooling/{ecosystem}/{name}` | Recipe runtime began work |
| `provisioning_completed` | Fine | `tooling/{ecosystem}/{name}` | Recipe runtime registered manifest |
| `provisioning_failed` | Medium | `tooling/{ecosystem}/{name}` | Recipe runtime failed |
| `build_started` | Fine | `tooling/{ecosystem}/{name}` | Sandbox build began |
| `build_completed` | Fine | `tooling/{ecosystem}/{name}` | Sandbox build succeeded |
| `build_failed` | Medium | `tooling/{ecosystem}/{name}` | Sandbox build failed |
| `supply_chain_violation` | Coarse | `tooling/{ecosystem}/{name}` | Hash mismatch / attestation failure |
| `disk_fallback_used` | Medium | `pipeline/{pipeline_id}` | Disk fallback invoked |
| `substrate_eviction_pressure` | Atomic | `substrate/cache` | Tier cap exceeded |

### Lens Extensions

`AmbientFor`: surfaces `tool_pinned` / `tool_upgraded` / `supply_chain_violation` events in the agent's ambient context envelope when scoped to the agent's active ecosystem(s).

`inspect_open_conflicts`: surfaces version-pin conflicts ("pipeline-A pinned react@18, pipeline-B pinned react@17 — divergent across ecosystem 'npm'").

---

## Disk Fallback (Degraded Mode)

When the substrate cannot provision a tool — network down, no published artifact for the host's platform tuple, build script broken, all retries exhausted — the system can degrade to using the user's host-installed version with explicit consent.

```
1. Agent calls provision_tool_dependency(rust, rustc)
2. Substrate's resolver finds no satisfying recipe (no network, no artifact)
3. Substrate calls Guardian's disk_fallback_gate with proposed host path
4. Guardian evaluates org policy + asks user if needed
5. If APPROVED:
   - Substrate creates a disk:// fetch recipe, hashes the host binary, registers a fallback manifest
   - The manifest is tagged "ecosystem: disk-fallback" so it's visible in lockfile diffs
   - disk_fallback_used activity emitted
6. View composition treats the fallback manifest like any other; agent can use the tool
7. Lockfile pins the host hash; if the host binary changes, hash mismatch → re-evaluate fallback
```

This preserves the no-disk **write** invariant — substrate still doesn't write to disk. Reading host disk in degraded mode is not the same operation. Disk fallback is per-pipeline-scoped and never promotable to substrate-canonical (the manifest carries the fallback tag forever).

---

## Mount-Point Bootstrapping (The One Disk Concession)

FUSE on Linux/macOS requires the mount point to exist as a directory. `/sylk/views/pipe-7/` must exist before we can mount a View there.

**Linux:** mount inside an unshared mount namespace. `unshare(CLONE_NEWNS)` first, `mkdir -p /sylk/views/pipe-7` second — the directory only exists in the namespace, never on host disk. **Zero host-disk presence.**

**macOS / Windows:** no equivalent of mount namespaces. Accept one root directory (`/sylk/` and `S:\sylk\`) as the cost. **One empty, content-free root directory at agent install time, not per-pipeline.** Flagged for explicit user sign-off.

This is the only host-disk concession in the entire design. Everything else is in-memory.

---

## Implementation Phases

Walking-skeleton-first; each phase ships behind a feature flag and coexists with current install paths until cutover.

### Phase 0 — Prep & Decisions

- Package layout: `core/substrate/{blob,manifest,lock,provision,build,cache,projection,view,exec,sandbox,recipe,resolver}`
- Dependencies: `lukechampine.com/blake3`, `klauspost/compress/zstd`, `hanwen/go-fuse/v2`
- Pick PubGrub Go implementation
- Extend existing purevfs WAL infrastructure for substrate WAL (don't add WAL #5)

### Phase 1 — Blob Store + Manifest Store

- Sharded in-memory `BlobStore` (16 shards, sync.Map-style)
- BLAKE3-keyed; `Put` / `Get` / `Has` / `Refcount` / `Stat`
- `ManifestStore`: insert/lookup by `PackageID` and `ManifestID`
- Refcount semantics with concurrent attach/detach race tests
- WAL append on every manifest insert; WAL replay on process start
- Tests: refcount correctness under concurrency, WAL replay determinism, hash collision refusal

### Phase 2 — Recipe Runtime

- Recipe schema (`recipe_v1.go`) + canonicalization + content-hash
- Fetcher: HTTPS, git, OCI registry, custom URL schemes, disk:// fallback
- Verifier: SHA / BLAKE3 / signatures / attestations
- Extractor: zip, tar.* family, raw, oci-layer, git-tree, custom plugin slot
- Recipe executor: orchestrates fetch → verify → extract → (build) → register
- Initial recipe library: a handful of recipes spanning fetch-only, fetch+extract, and fetch+extract+build cases drawn from diverse ecosystems

### Phase 2.5 — Sandbox

- Three platform backends (Linux, macOS, Windows) developed against shared `Sandbox` interface
- Network isolation: kernel-enforced on all three
- Mount/filesystem isolation: namespace on Linux, sandbox-exec on macOS, AppContainer on Windows
- Determinism enforcement: env clamping, locale, time, hostname
- Resource limits: cgroups v2 / Job Object / similar
- Writable FUSE projection for build scratch (in-memory, captured on success)
- Tests: hermeticity (no network leakage, no host fs visibility), determinism (same inputs → same output bytes)

### Phase 3 — Lockfile

- `Lockfile` type matching schema
- CRDT semantics: append-only witnesses, monotonic, deterministic solve
- Per-ecosystem resolver adapter interface
- Initial resolver implementations: PubGrub for Python/Cargo, GitHub-release for direct pins
- WAL persistence; replay on process start
- Fabric activity emission on every state change
- Tests: CRDT convergence (property-based), supply-chain violation handling, witness lifecycle

### Phase 4 — FUSE Library Migration (Linux)

- Migrate `process_broker_linux.go` from `bazil.org/fuse` to `hanwen/go-fuse/v2/fs`
- Side-by-side via build tag (`substrate_fuse_v2`) for one release
- Run existing purevfs test suite against both backends
- Benchmark comparison
- Default flip after green run; bazil dropped one release later
- macOS/Windows continue with cgofuse

### Phase 5 — Substrate Projection

- Extend `projectedRoot` to compose pipeline ⊕ global ⊕ disk ⊕ substrate layers
- `SubstrateView`: `(manifest_id, mount_path)` attachments per view
- Read path: FUSE `read` → BlobStore.Get → serve bytes (zero-copy where backend supports)
- Write path: substrate paths return `EROFS`; upper-layer paths work normally
- Tests: composed-view integrity, write refusal, no-disk-fallthrough invariant

### Phase 5.5 — View Composition

- View type with content-hashed identity
- Layout composer with conflict detection
- Five-layer environment generator (PATH, lib paths, language env vars, mount plan, aux state)
- Layer 4 per-platform implementations (mount namespace on Linux, shebang rewrite + DYLD interposer on macOS, drive mappings on Windows)
- View construction caching by `view_hash`

### Phase 6 — Execution Backend

- `SubstrateBackend` implementing existing `ExecutionBackend` interface
- Pre-exec: lockfile lookup → manifest set → ensure attached → compute View → spawn under sandbox + projection
- Refusal mode: structured error pointing at `provision_tool_dependency` when tool unavailable
- Tests: end-to-end — provision tool, attach to view, spawn, exit 0

### Phase 7 — Tiered Cache

- Three blob representations (Hot, Warm, Cold)
- S3-FIFO governing transitions
- TinyLFU 4-bit count-min sketch for admission control
- Per-ecosystem zstd dictionary training (offline tool)
- Refcount-driven existence (orthogonal to tiering)
- Metrics emission

### Phase 8 — Skill Surface + Retirement

- New skills: `provision_tool_dependency`, `attach_tool_to_view`, `query_view`
- Update pipeline-agent prompts (engineer, designer, tester) to use them
- Rewire `research_test_tool_install`'s install half through `provision_tool_dependency`; keep research half
- Deprecate (with logged warning) existing install skills
- Remove deprecated skills one release cycle later

### Phase 9 — Fabric Integration & Observability

- Activity emission on all substrate state changes
- Lens extensions for `tool_pinned` / `tool_upgraded` / `supply_chain_violation`
- Telemetry: blob count, manifest count, tier sizes, hit rates
- Substrate WAL replay status surfaced in `self_diagnostic`

---

## Walking Skeleton

The minimum slice that proves architecture before committing all 9 phases:

**Phase 1 → Phase 2 (recipe runtime + 3 diverse recipes) → Phase 4 (FUSE migration) → Phase 5 (substrate projection) → Phase 5.5 (View composition) → Phase 6 (execution backend)**

Skeleton goal: **execute one recipe of each shape end-to-end, drawn from different ecosystems**, with the spawned process locating the recipe's output via ordinary `PATH` lookup and no agent-side path manipulation.

Three recipes:
1. **Pure data fetch** — a small data-only artifact (font, corpus)
2. **Prebuilt binary** — a CLI from any ecosystem (e.g., ripgrep)
3. **Source build** — a small C library (zlib) requiring a bootstrap toolchain recipe

If those three work end-to-end across all three sandbox backends, the substrate is general. Subsequent ecosystem support is content (recipes), not code.

---

## Parallelization

After Phase 0:

- **Track A**: Phases 1 → 2 → 3 (storage spine, recipe runtime, lockfile)
- **Track B**: Phase 4 (FUSE migration) — independent
- **Track C**: Phase 2.5 (sandbox) — independent of A/B until integration
- **Track D**: Phase 7 prep (zstd dict training, S3-FIFO sketch) — independent

Phases 5, 5.5, 6, 8, 9 must be sequential after dependencies land.

---

## Risk Table

| Risk | Likelihood | Mitigation |
|---|---|---|
| Registry rate limits (varies wildly per ecosystem) | Medium-High | Per-ecosystem rate limiter; substrate-level dedup means we mostly fetch once per `(name, version)` per process lifetime; mirror caching in WAL |
| Native artifact / linker dependency on host syslibs | High | Substrate-host curated syslibs as their own pseudo-ecosystem; refuse packages linking to non-substrate sysroots |
| Install-time / build-time scripts (npm postinstall, cargo build.rs, gem extconf, sdists) | High | Run under hermetic sandbox with substrate-mounted runtime + dependency closure; capability gates via Guardian; never refused — contained |
| Per-ecosystem resolver semantics (npm peer-deps, Go MVS, Maven conflict resolution) | High | Embed/wrap each ecosystem's canonical resolver; uniform lockfile schema, per-ecosystem solver field |
| Per-platform artifact selection | Medium | Provisioner takes (os, arch, libc) tuple; rejects mismatched artifacts at fetch time |
| Runtime version skew | Low (substrate-hosted) | Substrate-host runtimes via prebuilt distributions; runtimes are first-class manifests |
| Bootstrapping cycles | Low | Bootstrap toolchain is just a recipe; provision the prebuilt toolchain for the host's `(os, arch, libc)` triple at process start |
| Lockfile divergence across ecosystem solvers | Medium | Cross-ecosystem syslib unification via a shared `syslib` ecosystem that both ecosystems' packages depend on |
| Hash algorithm mismatch | Low | Per-ecosystem provisioner owns hash verification; lockfile stores both ecosystem-native hash AND internal BLAKE3 |
| Substrate execution backend rejects binary because dynamic linker not in substrate | High initially | Substrate-host the dynamic linker and core libc as part of the bootstrap toolchain |
| Permission/ownership semantics across ecosystems | Medium | Substrate stores Unix mode bits in `FileEntry`; FUSE projection synthesizes ACLs on Windows; refuse setuid/setgid bits at provisioning time |
| FUSE projection's executable-bit support varies by backend | Low | Confirm `mode & 0111` round-trips through projection on each platform during Phase 5 validation |
| One ecosystem's provisioner becomes substrate bottleneck | Medium | Per-ecosystem concurrency caps; substrate-level total provisioning concurrency cap; backpressure surfaced via fabric |
| Hanwen FUSE migration regresses an edge case bazil quietly handled | Medium | Side-by-side build-tag coexistence for one release; both run against full purevfs test suite before default flip |
| Memory cost of writable FUSE during builds | Medium | Per-build scratch caps; spill cold scratch to Warm tier; stream linker output extraction directly into substrate |
| Build determinism breaks on some upstream packages | Medium | Maintain substrate-internal patch set for known offenders; refuse + document for the rest |
| Substrate becomes trust root for compilers (compromised bootstrap = everything compromised) | Low | Hash-pin bootstrap; transparency log; ideally Sigstore/SLSA attestations on bootstrap; document trust model |
| Some builds genuinely require host-specific resources (GPU, kernel modules) | Low | Refuse + document. Rare in practice. |
| First build of anything is slow | Medium | Substrate cache hit on subsequent builds; agents should be aware bootstrap-then-build is a non-trivial latency event |
| Mount point creation pollutes user namespace | Low (Linux), Medium (macOS/Windows) | Linux: mount-namespace-private dirs (zero host disk). macOS/Windows: one empty `/sylk/` root dir, flagged for explicit user consent at install time |

---

## Out of Scope

Deferred deliberately:

- **Multi-node substrate sharing** (cross-process / cross-machine). Single-process substrate is enough for now; the design extends naturally if a shared substrate daemon is wanted later.
- **Manifest-level GC** (compacting WAL). Phase 7 covers blob eviction; manifest-level cleanup is a follow-up once we see how lockfiles age.
- **Full source bootstrap** (Guix-style reduced binary seed). The design accommodates it (recipes can recursively bootstrap), but Option A (curated bootstrap tarball) is the default. Switching is policy, not architecture.
- **Cross-platform deterministic builds for every ecosystem** (some Windows-targeting builds have residual non-determinism). Per-platform cache entries for the residual cases; converge over time as toolchains improve.

---

## File Layout

```
core/substrate/
  README.md
  recipe/
    schema.go               // Recipe, FetchSpec, ExtractSpec, BuildStep, OutputMapping types
    canonicalize.go         // canonical YAML/JSON for content-hashing
    validate.go             // schema validation
    library/                // shipped recipe library
      rust/, python/, node/, go/, ruby/, java/, dotnet/, ocaml/, ...
      system/, c-libraries/, github-releases/, ...
  blob/
    store.go                // sharded in-memory BlobStore
    refcount.go             // refcount tracking
    hash.go                 // BLAKE3 wrapper
    sharding.go             // 16-shard hash distribution
  manifest/
    store.go                // ManifestStore
    types.go                // Manifest, FileEntry, PackageID
    wal.go                  // WAL append + replay
  cache/
    tiers.go                // Hot/Warm/Cold representation
    s3fifo.go               // S3-FIFO eviction
    tinylfu.go              // 4-bit count-min sketch
    zstd_dict.go            // per-ecosystem dictionary management
  lock/
    lockfile.go             // Lockfile type + CRDT semantics
    solve.go                // resolver dispatch
    witnesses.go            // witness lifecycle
    wal.go                  // WAL persistence
  resolver/
    interface.go            // Resolver interface
    pubgrub/                // PubGrub-backed resolvers
      python/
      cargo/
      hex/
    npm/                    // npm-arborist wrapper
    go/                     // MVS implementation
    maven/, nuget/, ...
    direct/                 // GitHub-release, OCI, custom-https, disk-fallback
  provision/
    runtime.go              // Recipe runtime: fetch → verify → extract → build → register
    fetcher/
      https.go
      git.go
      oci.go
      github_release.go
      disk.go                // disk:// fallback
    extractor/
      zip.go, tar.go, raw.go, oci_layer.go, git_tree.go
      registry.go            // custom extractor plugin registration
    builder/
      orchestrator.go        // runs build steps under sandbox
      determinism.go         // applies determinism profile
  build/
    fuse_writable.go         // writable FUSE projection for build scratch
    output_capture.go        // walk output tree, hash, register
  sandbox/
    interface.go             // Sandbox interface
    linux.go                 // namespaces + seccomp + cgroups
    darwin.go                // sandbox-exec + Endpoint Security
    windows.go               // AppContainer + Job Object + WFP
    determinism_env.go       // platform-agnostic env clamping
    network.go               // platform-agnostic network policy enforcement
  projection/
    composer.go              // pipeline ⊕ global ⊕ disk ⊕ substrate composition
    substrate_layer.go       // substrate as projection source
  view/
    view.go                  // View type + composition
    env.go                   // five-layer environment generator
    layer4_linux.go          // mount namespace + bind mounts
    layer4_darwin.go         // shebang rewrite + DYLD interposer
    layer4_windows.go        // per-process drive mappings + KnownFolder redirect
    discovery.go             // query_view structured introspection
  exec/
    backend.go               // SubstrateBackend (ExecutionBackend impl)
    spawn.go                 // platform-agnostic spawn under view + sandbox
  guardian/
    gates.go                 // provision_substrate_gate, sandbox_capability_gate, disk_fallback_gate
  fabric/
    activities.go            // Activity Fabric event emission
    lenses.go                // ambient context lens extensions
  skills/
    provision.go             // provision_tool_dependency skill
    attach.go                // attach_tool_to_view skill
    query.go                 // query_view skill
```

---

## Glossary

- **Substrate** — the Tool VFS as a whole; the system described in this document.
- **Blob** — content-addressed sequence of bytes (BLAKE3-keyed).
- **Manifest** — bundle of file entries describing one installable resource.
- **Recipe** — declarative description of how to materialize a resource into the substrate.
- **Resolver** — per-ecosystem adapter translating constraints into recipe IDs.
- **Sandbox** — platform-abstracted hermetic execution environment.
- **View** — per-pipeline composition of substrate manifests, materialized into spawned process environment.
- **Lockfile** — per-session pinned set of recipes + transitive closures + witnesses.
- **Witness** — a pipeline that has pinned a specific version of a recipe; CRDT-append-only.
- **Hot / Warm / Cold** — blob representation tiers (uncompressed / zstd-compressed / manifest-only).

---

*Document version: 1*
*Last updated: 2026-04-19*
