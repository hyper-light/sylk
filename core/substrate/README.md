# core/substrate — Tool VFS

In-memory, content-addressed, hermetic, language-agnostic substrate where
tools, compilers, libraries, runtimes, and resources are installed for
agent use. Strict no-disk policy.

See `docs/TOOL_VFS.md` for the full design.

## Subpackages

- `recipe/`     — Recipe schema, canonicalization, validation, shipped library
- `blob/`       — Sharded in-memory BLAKE3-keyed blob store with refcount
- `manifest/`   — Package manifests (path → blob_hash bundles)
- `cache/`      — Tiered representation (Hot/Warm/Cold) + S3-FIFO + TinyLFU + zstd dicts
- `lock/`       — Per-session CRDT lockfile
- `resolver/`   — Per-ecosystem constraint solvers (PubGrub/MVS/npm-arborist/...)
- `provision/`  — Recipe runtime: fetch → verify → extract → register
- `build/`      — Hermetic source builds: writable FUSE projection + output capture
- `sandbox/`    — Three-platform hermetic execution environment
- `projection/` — Read-only substrate projection through FUSE (composes pipeline ⊕ global ⊕ disk ⊕ substrate)
- `view/`       — Per-pipeline view composition + 5-layer environment generator
- `exec/`       — Substrate-aware execution backend
- `guardian/`   — Policy gates (provision, sandbox capability, disk fallback)
- `fabric/`     — Activity Fabric event emission + ambient lens extensions
- `skills/`     — Agent-facing skills (provision_tool_dependency, attach_tool_to_view, query_view)

## Status

In development. See `docs/TOOL_VFS.md` for phase plan.
