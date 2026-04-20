# Resolver Implementation Plans

Per-adapter implementation plans for the resolvers specified in
`docs/RESOLVER.md`. Each adapter's plan is implementation-ready — hand it
to one implementer and they should have everything needed to ship.

Every plan follows the same 15-section template so readers comparing
adapters know where to look for each concern:

| # | Section | What it covers |
|---|---|---|
| 1 | Overview | Problem statement, ecosystem scope, user-visible behaviors |
| 2 | Data Model | Go types, interfaces, wire-format structs |
| 3 | HTTP Transport | Protocol specifics, connection pooling, retry policy, auth |
| 4 | Metadata Layer | Registry-specific parsing, caching, invalidation, Etag handling |
| 5 | Resolver | Algorithm (PubGrub, MVS, hand-rolled), frontier integration |
| 6 | Materializer | Disk layout, cache integration, hardlink/reflink strategy |
| 7 | Lockfile | Format, read/write, hard-preference semantics |
| 8 | Substrate Integration | How this adapter plugs into shared primitives |
| 9 | Error Handling | Error taxonomy, reporting, recovery paths |
| 10 | Security | Signature verification, hash checking, TLS, credential handling |
| 11 | Testing | Unit, integration, ecosystem-compat, perf, fuzz |
| 12 | Performance Targets | Concrete numbers (ops/sec, latency P50/P99, memory) |
| 13 | Phases | What ships in order, milestone definitions |
| 14 | Open Questions | Known unknowns, design decisions still to make |
| 15 | Dependencies | On substrate primitives, on other adapters |

## Implementation order

Adapters are ordered so each adapter's implementation validates and
hardens the substrate primitives in a way that benefits subsequent
adapters. The first adapter in each "tier" is the exemplar; subsequent
adapters in the tier reuse the tier's validated machinery.

**Tier 0 — Foundation** (`SUBSTRATE.md`)

Shared primitives every adapter depends on. Must ship before any
adapter can be started. Includes the Go PubGrub implementation, the
content-addressed cache, the HTTP/2 transport layer, the `Resolver`
interface, the `FrontierAwareResolver` extension, the `Constraint`
type, the lockfile framework, the materializer's hardlink/reflink
primitives, the frontier-driven prefetch coordinator.

**Tier 1 — PubGrub exemplars** (most-used ecosystems, validate
substrate generality)

1. **Python (uv)** — `PYTHON_UV.md` — PubGrub + range-request wheel
   metadata + comprehensive cache. The tier's exemplar; its
   implementation will surface most substrate refinements.
2. **Rust (Cargo)** — `RUST_CARGO.md` — PubGrub + sparse index
   (structurally simpler than Python's because crates.io serves
   metadata as per-version resources). Validates the substrate
   works for clean protocols as well as messy ones.
3. **PHP (Composer)** — `PHP_COMPOSER.md` — PubGrub + Packagist
   API v2 + metadata minification expansion. Validates the
   substrate handles delta-compressed metadata.

**Tier 2 — Non-PubGrub exemplars** (validate substrate accommodates
different solver shapes)

4. **Go modules** — `GO_MODULES.md` — MVS + GOPROXY. Validates
   substrate supports non-backtracking algorithms and that
   `FrontierAwareResolver` is correctly optional.
5. **Node (npm)** — `NODE_NPM.md` — custom peer-dep resolver
   (Arborist-compatible) + streaming JSON + hardlink materialization.
   The hardest adapter; validates substrate handles full-package
   mega-documents and non-convergent constraint models.

**Tier 3 — JVM cluster** (share Maven Central transport)

6. **Maven** — `JVM_MAVEN.md` — static-file-tree protocol, nearest-
   wins mediation (compatibility mode) + PubGrub (strict mode).
7. **Gradle** — `JVM_GRADLE.md` — variant-aware resolution, Module
   Metadata format, capability conflict detection, per-configuration
   lockfiles.
8. **Scala + Coursier** — `SCALA_COURSIER.md` — identity-suffix
   encoding for Scala version, both POM and Ivy XML metadata.

**Tier 4 — Registry-federation / lockfile-heavy** (validate multi-
feed abstractions)

9. **.NET (NuGet)** — `DOTNET_NUGET.md` — service-index protocol,
   multi-feed federation, framework targeting, CPM + lockfile.
10. **Ruby (Bundler)** — `RUBY_BUNDLER.md` — compact index, PubGrub
    via shared Go impl, `Gemfile.lock`.
11. **Elixir/Erlang (Hex)** — `ELIXIR_HEX.md` — signed protobuf
    metadata, aggregate sync endpoints, cross-language packages.

**Tier 5 — Curated / minimalist / research** (distinct architectural
models)

12. **Haskell (Cabal + Stack)** — `HASKELL_CABAL_STACK.md` — dual-
    mode: strict Cabal solver + Stackage snapshot consumption
    (no-solve path).
13. **OCaml (OPAM)** — `OCAML_OPAM.md` — CUDF + pluggable solvers,
    compiler-as-primary-constraint, switches as first-class
    environments.
14. **Lua (LuaRocks)** — `LUA_LUAROCKS.md` — reuses OPAM's
    environment primitive at smaller scale, rockspec-as-Lua-script
    parsing.
15. **Swift (SwiftPM)** — `SWIFT_SPM.md` — git-URL-as-identity,
    local clone cache, PubGrub.
16. **Zig (zon)** — `ZIG_ZON.md` — no-resolver-needed, URL+hash
    pairs, content-addressed materialization.

## Shared-code reuse map

This table identifies what each adapter reuses from the substrate
vs. what it must implement natively. The goal is maximum substrate
sharing so adapters stay small and the substrate bears the
engineering weight.

| Adapter | Algorithm | Protocol client | Metadata format | Materializer |
|---|---|---|---|---|
| Python (uv) | Substrate PubGrub | Custom (PyPI simple+JSON) | Custom (wheel METADATA extractor) | Substrate |
| Rust (Cargo) | Substrate PubGrub | Substrate HTTPS | Custom (sparse-index JSON-Lines) | Substrate |
| PHP (Composer) | Substrate PubGrub | Substrate HTTPS | Custom (v2 minified JSON) | Substrate |
| Go modules | Native MVS | Substrate HTTPS | Custom (GOPROXY .mod files) | Substrate |
| Node (npm) | Native Arborist-like | Substrate HTTPS | Custom (npm registry JSON, streaming) | Substrate |
| Maven | Substrate PubGrub + native nearest-wins | Substrate HTTPS | Custom (POM XML) | Substrate |
| Gradle | Substrate PubGrub + variant matcher | Substrate HTTPS | Custom (Module Metadata JSON + POM) | Substrate |
| Scala (Coursier) | Substrate PubGrub | Substrate HTTPS | Custom (POM + Ivy XML) | Substrate |
| .NET (NuGet) | Substrate PubGrub | Substrate HTTPS (service-index abstraction) | Custom (Registrations JSON + nuspec) | Substrate |
| Ruby (Bundler) | Substrate PubGrub | Substrate HTTPS | Custom (compact index text) | Substrate |
| Hex | Substrate PubGrub | Substrate HTTPS | Custom (protobuf + signature verification) | Substrate |
| Haskell (Cabal mode) | Substrate PubGrub + strict consistency | Substrate HTTPS | Custom (.cabal parser) | Substrate |
| Haskell (Stack mode) | None (snapshot consumer) | Substrate HTTPS | Custom (snapshot YAML) | Substrate |
| OCaml (OPAM) | Native CUDF + pluggable solver | Substrate HTTPS | Custom (.opam parser) | Substrate |
| Lua (LuaRocks) | Native simple solver | Substrate HTTPS | Custom (rockspec Lua evaluator) | Substrate |
| Swift (SwiftPM) | Substrate PubGrub | Substrate git client + HTTPS | Custom (Package.swift parser) | Substrate |
| Zig (zon) | None (no solver) | Substrate HTTPS | Custom (zon parser) | Substrate |

## Milestone definition (per adapter)

Each adapter's phases map to one of four milestones:

- **M0 — Scaffold**: types defined, interface compiles, trivial unit tests pass.
- **M1 — Online fetch**: adapter can fetch real registry data, parse
  metadata, produce candidate versions. Not yet integrated with
  resolver.
- **M2 — Resolution**: adapter resolves a real dependency tree end-to-
  end against the live registry. Correctness validated against a
  canonical ecosystem test corpus.
- **M3 — Production**: materialization, lockfile, integrity
  verification, error handling, observability, performance
  targets met. Ready to ship to Sylk users.

An adapter is considered "shipped" when M3 is reached and the
adapter's ecosystem-compatibility test suite passes. Below M3, the
adapter may be merged but is gated behind a feature flag.

## Continuation

This README is the index. Individual adapter plans follow the 15-
section template. As of this writing:

- **Tier 0 (Substrate)**: `SUBSTRATE.md` — drafted
- **Tier 1 (PubGrub exemplars)**: Python drafted; Rust / Composer
  pending
- **Tiers 2–5**: pending

Plans are added in the implementation order documented above.
