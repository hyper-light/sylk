# System Improvements: Containers, Pods, Volumes, Replicas

Substantive improvements to the agent runtime infrastructure. Hard constraints
that bound every proposal:

1. **Goroutine-native, in-process.** No process-per-pod, no IPC, no CRIU. Lean
   on what only an in-process Go scheduler can do.
2. **Cross-platform.** macOS, Linux, Windows. Platform-specific layers are
   permitted as enhancements, never as foundations.
3. **PureVFS stays in RAM.** No spill-to-disk, no on-disk snapshots, no fsync.
   Memory pressure is handled by compression, dedup, eviction of reconstructible
   state, snapshot pruning.
4. **Don't clone Kubernetes.** Push the unique abstractions (HAMT lineage,
   cross-pod content addressing, the activation tier ladder, claims/testimony,
   the architect's plan DAG) further. Generic orchestrator patterns are the
   wrong rubric.

The current architecture is captured in the audit at the top of this branch;
this doc is the forward plan.

---

## 1. Goroutine isolation at Go's actual abstraction layer

The unit of isolation is the goroutine scope, not the OS. Strengthen what we
already have rather than reach outside it.

- **Per-pod goroutine pool** with a small set of `runtime.LockOSThread` workers.
  Cache locality and CPU-share reasoning come from worker counts, not cgroups.
- **`runtime/metrics`-driven admission**: read `gc/pauses`, `sched/goroutines`,
  `gc/heap/allocs:bytes` live; admission is a function of *current* pressure,
  not a pre-allocated quota. Replaces the single-mutex `ResourceQuota` with a
  lock-free signal pipeline.
- **Capability tokens as unforgeable Go types**: private constructor, type-
  erased identity in `atomic.Pointer`. An agent holding a `ReadOnlyFileAccess`
  cannot upcast to a writable variant because the writable value is never given
  to it. Stronger than any in-process check, costs nothing, cross-platform.
- **Per-pod `debug.SetGCPercent` calibration**: shared controller adjusts based
  on observed latency. Hot-tier pods get tighter GC; cold pools get looser.
- **Cooperative cancellation trees**: per-pod context root with deadline
  propagation. Teardown is "cancel root, await scope drain with deadline." No
  `Goexit`-from-outside hacks.

## 2. PureVFS as a true memory-resident substrate

All pressure responses without touching disk.

- **Compressed cool tier in-RAM.** zstd with a dictionary trained per agent
  type. 5–10× density on cold chunks; lazy decompression on read.
- **Eviction by reconstruction cost, not LRU.** Drop cached treesitter parses,
  LSP responses, embedding vectors first — they can be rebuilt. Primary chunks
  evict last.
- **Snapshot lineage GC** via epoch-based reclamation. Today HAMT structural
  sharing keeps every snapshot alive forever if anyone held a reference. Add
  per-snapshot refcounts with epoch reclaim — frees chunks the moment the last
  reader retires.
- **Inline small files in the inode** (ZFS-style embedded data). A 200-byte
  file shouldn't take a chunk-store roundtrip. Wins on the small-file long tail.
- **Anonymous mmap arenas with `MADV_FREE`** (Unix) / `DiscardVirtualMemory`
  (Windows). Still RAM, but the OS can reclaim under pressure without crashing
  us. The arena abstraction already exists in `core/purevfs/chunk_arena_*.go`.
- **Sharded `ChunkStore`**: 256-way stripe by `hash[0]`. RCU-style reads via
  `atomic.Pointer[immutable.Map]`. Today's single mutex is the throughput
  ceiling.
- **First-class typed accessors on volumes.** Volumes don't just expose
  `ReadFile`; they expose `ResolvedAST(path)`, `LSPGraph(symbol)`,
  `ClaimsAt(generation)`. Chunk store stays bytes underneath, agents work in
  the right semantic units.

## 3. Cross-pod chunk visibility

The killer feature of in-memory + content-addressed + in-process. Two pipeline
pods working on the same repo currently dedup at the store level but each
pod's `VolumeManager` re-injects file access independently. Push it further:

- **Global content-addressed read plane.** Every pod sees every chunk by hash,
  gated by capability. Engineer reading a file the librarian already loaded is
  a hash lookup, not a re-read.
- **Capability-gated handoff = pointer transfer**, not state copy. Predecessor's
  snapshot becomes a read-only mount in the successor's namespace; only
  divergent writes cost memory.

K8s + a network filesystem cannot match this. Make it a first-class primitive.

## 4. Replicas as a tier-ladder pool, driven by the plan DAG

The orchestrator already knows the plan. Stop creating pods on-demand;
*promote* them.

- **Permanent Cold pool** of pre-initialized agents (LLM client wired, skills
  loaded, state zeroed). Cold pods cost ~bytes of header.
- **Predictive promotion**: when the architect's plan dialog yields a step
  list, the activation predictor drives Cold→Cool→Warm transitions *before*
  the request lands. Cold-start latency goes from "wire up an agent" to
  "atomic pointer swap."
- **Adaptive pool depth**: `min_warm = ceil(λ × p95_handoff_latency)` from
  observed traffic — Little's Law derived from data, not a literal.
- **Tier transition is the replica creation primitive.** Don't `New()` an
  Engineer; pull a Warm one and promote it. Don't destroy; demote.
- **Adaptive tier controller** reading `runtime/metrics` for pressure: under
  heap pressure, demote the lowest-trust idle pod to Cool, drop reconstructible
  state, compress its snapshot.

## 5. Speculative deliberation

LLMs are non-deterministic. Lean into it.

- For high-stakes tasks, fork two architects/engineers on the same input via
  shared CoW snapshot. First validated result wins; loser's chunks drop via
  refcount. Designer/inspector pick the winner via existing claims/testimony.
- Cost is bounded: forks share chunks at the byte level; goroutines spawn in
  microseconds; only divergent writes consume new memory.
- Structurally infeasible on K8s. Natural here. First-class scheduling
  primitive: `SpeculativePolicy{N: 2, JudgedBy: ConsensusOf(designer, inspector)}`.

## 6. Lock contention removed via Go-idiomatic patterns

Every hot subsystem (`GoroutineBudget`, `ResourceQuota`, `ContainerRegistry`,
`VolumeManager`, `ChunkStore`) has one mutex protecting everything.

- **`atomic.Pointer[immutable.Map]` snapshots** for registry reads. Mutations
  go through a single-writer goroutine fed by a channel — single-writer/multi-
  reader, no lock on the read path. Reuses the HAMT primitive already in PureVFS.
- **Per-agent semaphores** via buffered channels (`make(chan struct{}, n)`)
  instead of `sync.Cond.Signal()` on a shared budget. No thundering herd,
  idiomatic Go, cross-platform by definition.
- **Sharded `atomic.Int64` counters** for budget/quota — sum-on-read,
  contention-free writes.

## 7. Fenced two-phase handoff

The unmount-before-pause race in `core/container/pod/volume_manager.go` and the
"never tear down before commit" rule are both symptoms of an undefined
ordering protocol. Define it.

- **Generation-fenced seal**: predecessor calls `Seal(gen_n)` → workspace
  journal HEAD pinned, generation atomically bumped. Any subsequent write at
  `gen_n` returns `ErrStaleGeneration` at the Workspace API. Pure Go, no kernel.
- **Successor opens at `gen_n+1`** with predecessor's snapshot mounted
  read-only.
- **Bounded drain on demote**: per-volume semaphore drained with deadline; on
  expiry, deterministic error rather than the current silent-swallow.
- **Pod-coordinated restart**: today restart is per-container backoff. For
  pipeline pods, coordinate — if engineer fails 3× back-to-back, demote the
  whole pod and let the architect re-author, rather than restarting one agent
  into a broken peer state.

## 8. Probes that understand the workload

K8s probes ask "is the process up." Useless for LLM agents.

- **Token-progress probe**: alive iff the LLM client emitted ≥N tokens in
  last T seconds.
- **Claim-coherence probe**: ready iff latest emitted claim is still consistent
  with the active task signature.
- **Context-fit probe**: healthy iff projected working set fits remaining
  context envelope.

Failures route to the architect for corrective action (canonical authority),
not to a generic restart loop.

## 9. Time-travel debugging via the journal

The VFS journal already exists. Make it a debug primitive.

- "Replay this agent's view at handoff `gen_n`" — re-mount snapshot read-only,
  no LLM re-run.
- "Diff two pods' worldviews at same generation" — set difference over inode
  maps.
- "Rewind to before the corrupting write" — pop journal entries until a
  predicate fails.

A unique capability of in-memory + immutable lineage.

## 10. Token budget as the primary scarce resource

Heap and goroutines are real costs but secondary. For LLM workloads, **tokens
× model price** dominates economics, and **context window size** dominates the
working-set ceiling.

- Per-pod / per-session / per-user token bucket enforced at the **provider
  edge**, not pre-admission. Today the quota is fiction once a request enters
  the LLM client.
- **Pod-level context envelope** with intra-pod borrowing: if engineer is idle,
  designer can draw from the pod's shared context budget. Closer to how the
  user thinks about cost than per-agent caps.

---

## Sequencing

1. **Lock-sharding + RCU snapshots (#6)** — pays off immediately, makes
   everything else cheaper to evolve.
2. **Capability tokens as Go types (#1) and fenced two-phase handoff (#7)** —
   small surface, high blast-radius, deletes the standing "watch out for VFS
   teardown" rule.
3. **Tier-ladder pool driven by plan DAG (#4)** — turns cold-start from a
   latency hit into a control-loop tuning problem.
4. **Cross-pod chunk visibility + typed volume accessors (#2, #3)** — the
   unique-moat work; biggest leap in what the system can do that nothing else
   can.
5. **Speculative deliberation (#5), semantic probes (#8), time-travel
   debug (#9), token budget as primary (#10)** — first-class new capabilities
   once the foundations are in.
