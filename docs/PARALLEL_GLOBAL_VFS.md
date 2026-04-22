# Parallel Global VFS — Progressive Audit, COW Replica Chain, Direct-Protocol Dispatch

**Status**: Design (canonical). Partial implementation in flight; see §11 for staging.
**Scope**: `core/versioning`, `agents/shared` (audit coordinator, protocol contracts, skills), `agents/inspector/global`, `agents/tester/global`, `agents/architect` (remediation). The **orchestrator has no role** in this system — see §0.
**Supersedes**: the candidate-map review flow (`ExtractReviewCandidate` → `s.reviews.candidates` → `AcceptActiveReviewCandidate`), the single-active-candidate review overlay (`reviewVFS`), the orchestrator-as-audit-dispatcher model, and the implicit "release pod on OT accept / strand on OT reject" pod lifecycle.

---

## 0. The orchestrator has no role

This point is fundamental and repeated verbatim in every section that might tempt someone to reach for orchestrator wiring:

**The orchestrator does not:**
- Subscribe to merge completions.
- Spawn audit replicas.
- Translate audit results into commit-queue transitions.
- Run the commit resolver.
- Own the `ReplicaVFS` chain.
- Broker the handoff from pipeline inspector to OT.
- Drive remediation dispatch (the architect does that directly).

The orchestrator's role is unchanged from its existing responsibilities — agent runtime lifecycles, request forwarding through the guide, session-level multiplexing. It is **not** in the per-merge audit loop, the commit-resolver loop, or the rejection → remediation cycle.

The flow is:

```
pipeline inspector handoff_to_green   (direct protocol message)
    ↓ direct method call
SessionVFS.MergePipelineIntoGreen     (MergePipe does OT, descriptor enqueued)
    ↓ synchronous callback (RegisterMergeCallback)
MergeAuditCoordinator.onMerge         (in agents/shared, not orchestrator)
    ↓ direct method call
Inspector.SpawnAuditReplica           (direct interface call, no bus)
Tester.SpawnAuditReplica              (direct interface call, no bus)
    ↓ LLM tool loop invokes
emit_audit_decision skill
    ↓ direct ctx-scoped callback
MergeAuditCoordinator.finalizeDecision (lifecycle + retention + commit queue)
    ↓ on accept, seal the ReplicaVFS
ReplicaVFS.Seal → MergePipe           (audit-addendum merge, OT-transformed)
    ↓ auto-accepted commit-queue entry
CommitResolver.flushToDisk            (owned by SessionVFS, in core/versioning)
    ↓ water line advance + retention GC
```

Every arrow is a direct method call or an in-process synchronous callback. There is no bus topic publish/subscribe in the critical path. (An optional observability publication happens on `AuditMergeResultTopic` AFTER internal state machines have advanced — consumed by the TUI and architect for rejection → remediation routing — but the core loop does not depend on it.)

---

## 1. The problem

Two failures, structurally connected.

**Failure 1 — remediation pipelines receive a blank workspace.**
When the global inspector rejects a candidate, the candidate's pipeline VFS has already been closed by `ExtractReviewCandidate`. The candidate's mods sit orphaned in `s.reviews.candidates`; they were never promoted to global VFS. When the architect dispatches a remediation DAG, each fix task's `BeginPipeline` opens a fresh pipeline VFS whose base reader sees only the pre-rejection green state. `workspace_read` returns empty for paths the original pipeline created. The rejected work is invisible to the fix that's supposed to fix it.

**Failure 2 — global-inspector audit is strictly serialized.**
The legacy global review processes one candidate at a time, top-to-bottom. Multiple pipelines finishing concurrently queue behind a single slow audit. The inspector cannot encounter new work mid-audit. Pipeline concurrency exists; audit concurrency doesn't; overall throughput is bottlenecked on the inspector's serial cadence.

These failures share a root: the legacy model has one "global state under review" and one "review candidate in flight." Both rejection and parallelism are limited by that singular structure.

## 2. Rejected alternatives

**Persistent rejected-overlay tier + remediation inheritance chain.** Rejected: memory grows linearly with rejection depth, per-read chain-walk cost adds up. The progressive COW chain (§3.6) gives the same benefit with bounded depth and bounded memory.

**Pre-analysis layer (linters, AST probes, format checks).** Rejected: every concrete probe either needs per-language parser infrastructure that doesn't scale across our language matrix, or duplicates work the pipeline inspector and role agents already do. Audit replicas can invoke linters on demand as part of their audit — no pre-analysis needed.

**Shadow inspector (second LLM running ahead).** Rejected: the pipeline inspector already performs local review. Shadowing re-derives information we hold.

**Per-merge replicas on independent snapshots (plain Option B).** Rejected: isolated snapshots miss cross-candidate coherence issues. The progressive variant (§3.3) makes each replica's context cumulative without eagerly copying.

**Independence-set parallelism by file-surface disjointness.** Rejected: coherence concerns cross surfaces.

**MVCC snapshot handles as the base for remediation pipeline VFS.** Rejected: replaced by the COW chain model (§3.6), which gives the same isolation with lazier materialization.

**Orchestrator-owned audit dispatch.** Rejected: the orchestrator has no semantic stake in the audit-replica state machine; coupling it there creates an artificial choke point and cross-layer entanglement. Dispatch belongs in `agents/shared` — the audit coordinator is a direct-callback observer on SessionVFS.

**Bus-topic broadcast for audit dispatch.** Rejected: the coordinator → replica handoff is a session-internal concern with no outside subscribers. Bus broadcast adds indirection, timing ambiguity, and failure modes (slow subscribers, dropped messages) for no semantic benefit. Direct method calls via the `AuditReplicaSpawner` interface and a ctx-scoped `AuditDecisionFinalizer` close the loop without bus.

**Eager byte-for-byte materialization of the audit-context Copy.** Rejected: for deep chains, eager materialization is wasteful. COW per-file (§3.6) materializes only what a given audit actually touches.

**Split per-role overlays within a single merge (one for inspector, one for tester).** Rejected: inspector and tester audit the SAME merge and produce complementary contributions. Sharing a single `ReplicaVFS` per merge is simpler, avoids a synthetic role-boundary OT at seal time, and aligns with the "both replicas see each other's in-flight writes" semantic that coordinated audits need (e.g. inspector runs formatter across tester-authored tests).

## 3. The model

### 3.1 Progressive replica chain

Every pipeline merge into the global state produces a new `ReplicaVFS_N` — a full in-RAM VFS scoped to merge N, with a parent pointer to `ReplicaVFS_{N-1}`. The chain captures progressive context without eager materialization.

```
Disk state (authoritative, committed merges only, updated by the commit resolver)
   ↑ read-through for uncached paths
ReplicaVFS_1   ← audits merge 1; in-RAM; parent = nil (direct read-through to disk)
   ↑
ReplicaVFS_2   ← audits merge 2; parent = ReplicaVFS_1
   ↑
ReplicaVFS_3   ← audits merge 3; parent = ReplicaVFS_2
   ⋮
ReplicaVFS_N   ← audits merge N; parent = ReplicaVFS_{N-1}
```

Chain depth is bounded by the queue-depth backpressure cap (§5). Water-line advancement (§3.4) prunes accepted merges off the bottom as they commit.

### 3.2 Pipeline-inspector-accept merges directly into green via direct protocol

The pipeline inspector's `handoff_to_green` (renamed from `handoff_to_ot`) is a **direct protocol message** — a method call through the `PipelineCommitter` interface, resolving to `SessionVFS.MergePipelineIntoGreen(ctx, pipelineID)`. No bus broadcast. No orchestrator hop.

`MergePipelineIntoGreen`:
1. Runs MergePipe to OT-transform the pipeline's overlay against current green.
2. Produces a new `MergeDescriptor` at a new `arrival_seq`, attaching the pipeline inspector's `PipelineInspectorCertificate` (declared-scope, open-concerns, summary, tester-verdict).
3. Enqueues the descriptor on the commit queue (write-ahead via ControlWAL; §3.4).
4. Fires synchronous merge callbacks registered via `SessionVFS.RegisterMergeCallback`.

The registered merge callback is the `MergeAuditCoordinator`'s hook. On fire:
- It materializes `ReplicaVFS_N` for the new merge, wiring the parent pointer from the merge log.
- It calls `Inspector.SpawnAuditReplica(ctx, req)` and `Tester.SpawnAuditReplica(ctx, req)` — direct interface calls. `ctx` carries both the `AuditMergeContext` (session, replica ID, versions) and the `AuditDecisionFinalizer` the replica invokes when it emits a verdict.
- It retains the parent `ReplicaVFS` (refcount) so it cannot be GC'd while the new replica is alive.
- It records `ReplicaSpawned` in the replica lifecycle log (durable via ControlWAL).

After this, `MergePipelineIntoGreen` returns to the pipeline inspector. The pipeline VFS releases; the pipeline pod releases. The audit runs asynchronously.

### 3.3 Per-merge audit replicas with progressive context

Each merge launches exactly **two replicas**: one inspector, one tester. Both operate on the same `ReplicaVFS_N`. Both can read. Both can write.

- **Audit target**: the changeset's diff (the mods produced by the pipeline's pipeline-accept; captured in `MergeDescriptor.Paths` + the merged overlay on `ReplicaVFS_N`).
- **Audit context**: everything readable through `ReplicaVFS_N` — its own in-RAM content + the parent chain + disk.

**Reads** by an audit replica through its `ReplicaVFS_N`:
1. `ReplicaVFS_N`'s own content store (writes + cached reads from ancestors).
2. If miss: walk parent chain — `ReplicaVFS_{N-1}`, `{N-2}`, ... — applying a bloom filter per node to skip nodes that don't own the path.
3. If still miss: read from disk. Disk is the authoritative committed state. Reading it during audit is normal — it's how a replica sees all the merges that have already been accepted and flushed. Optionally cache the resolved content into `ReplicaVFS_N` for subsequent hits.

**Writes** by either replica:
1. Serialize on `ReplicaVFS_N`'s mutex (inspector and tester both share the lock).
2. Apply to `ReplicaVFS_N`'s in-RAM content store.
3. Append `ControlKindReplicaOverlayWrite` to the session's ControlWAL with writer-ID + role for attribution.
4. **Never touches disk.** Only the commit resolver writes to disk.

Inspector + tester stomping the same path is a coordination failure at the agent level (tool loops should not step on each other). Last-write-wins in the shared overlay; WAL attribution makes it auditable. In practice, the two roles touch disjoint path sets (inspector: linter/formatter on source; tester: new test files) — conflict is rare and diagnosable.

Mid-audit, both replicas see each other's writes because they share the same `ReplicaVFS_N`. The inspector can format a test file the tester just authored. The tester can read the formatted source the inspector just produced.

### 3.4 OT is the single conflict-resolution primitive

Every cross-merge or cross-replica conflict collapses into **MergePipe OT**. No new merge algorithm is introduced for audits.

**Pipeline merge into green (arrival time)**: MergePipe OT transforms the pipeline overlay against current green. Same as today.

**Audit-addendum merge on accept**: When `ReplicaVFS_N.Seal()` fires, the sealed diff (inspector + tester combined writes, relative to N's parent) is submitted to MergePipe as a pipeline-equivalent changeset. MergePipe OT-transforms it against the current green state (which may have advanced since N's original pipeline merged). Produces a new commit-queue entry `N.audit`, marked `audit_addendum=true` on the descriptor. The coordinator sees the flag and skips re-auditing it — it's auto-accepted, carrying the verdict of the audit that produced it.

**Supersession and re-audit (§3.7)**: When a remediation M supersedes rejected N, MergePipe OT checks whether M's transform substantively changes any intermediate merge K+1..M-1 that had depended on N's pre-rejection state. For each substantively-changed merge, a fresh audit replica is spawned (re-audit) against the re-transformed diff.

All conflict handling runs through the same OT serializer — the one MergePipe already runs on a single goroutine for authoritative ordering. No parallel conflict algorithms, no split brain.

### 3.5 Arrival-ordered FIFO commit queue

Audit decisions accumulate in parallel; disk commits linearize.

```
MergeDescriptor {
    arrival_seq          : monotonic
    source_task_id       : pipeline that produced the changeset
    base_copy_seq        : the seq this merge applied against
    merged_copy_seq      : the seq this merge produced
    paths                : set of paths touched
    diff                 : the OT-transformed changeset
    pipeline_certificate : PipelineInspectorCertificate
    audit_addendum       : bool — true if produced by a ReplicaVFS seal; skipped by audit dispatch
    audit_replica_ids    : which replicas are auditing this (both: inspector + tester)
    state                : auditing | accepted | rejected | superseded | committed | abandoned
    superseded_by        : arrival_seq of the supersedor (when state == superseded)
}
```

The commit resolver is owned by `SessionVFS` (not the orchestrator; see §6.2). It's a single goroutine polling the queue head:

- Head `accepted`: flush the descriptor's diff to disk, transition the queue entry to `committed`, pop, advance the water line, trigger retention GC.
- Head `auditing`: wait.
- Head `rejected` without supersedor: block. The architect must dispatch remediation.
- Head `rejected` with supersedor: when the supersedor is `accepted`, flush its diff in this slot's place. Pop the rejected slot and the supersedor's own slot atomically. Water line advances past both.

The `CommitQueue` is WAL-backed: every transition (`Enqueue`, `MarkAccepted`, `MarkRejected`, `MarkSuperseded`, `MarkCommitted`, `Abandon`) is write-ahead to the ControlWAL before the in-memory state mutates. Crash recovery: replay the WAL, reconstruct the queue, restart the resolver. See §8.

Commit-queue transitions also emit events via `CommitQueue.Subscribe()` — consumed optionally by observability surfaces (TUI, telemetry). The core dispatch loop does not consume these; they are purely informational.

### 3.6 ReplicaVFS: copy-on-write chain, disk-as-authoritative-base

`ReplicaVFS_N` is a full in-RAM VFS (built on the `PipelineVFS` foundation) with a parent pointer. Each replica has:

- A local content store (writes + read-cache from ancestors).
- A local `deletes` set.
- A bloom filter over owned paths (for fast "do I own this?" checks during chain walks).
- A mutex protecting concurrent inspector/tester access.
- A parent pointer to `ReplicaVFS_{N-1}` or nil.
- A sealed flag (set on accept; overlay becomes immutable).
- A retention reference on its parent (preventing parent GC).

**Memory cost per replica** = paths it wrote + paths it read-cached. Unread paths are pointer references; large codebases keep most files on disk and pay the read cost only where replicas look.

**Chain depth** is bounded by backpressure (queue depth cap; §5). Water-line advancement prunes the chain as merges commit.

**Sharing** between inspector and tester: both operate on the same `ReplicaVFS_N`. Writes are serialized by the VFS mutex. Both read through the same chain. The ReplicaVFS itself doesn't branch on role — attribution is per-WAL-entry.

**Disk** is the authoritative committed baseline. Reads on a chain miss read from disk, which reflects every merge that has been accepted and flushed. This is the "reading merged work" the audit agents rely on — it's the contract for what's real versus what's still in flight.

### 3.7 Accept, seal, and the audit-addendum merge

When both audit replicas (inspector + tester) emit Accepted:

1. Coordinator's `finalizeDecision` fires (both-accept semantics; §3.4). Lifecycle log records `ReplicaDecided` for each.
2. Coordinator calls `ReplicaVFS_N.Seal()`:
   - N's in-RAM overlay is frozen; further writes rejected.
   - The sealed diff (writes + deletes, relative to N's parent chain) is computed.
   - The diff is submitted to MergePipe as a pipeline-equivalent changeset with `audit_addendum=true`.
3. MergePipe OT-transforms the diff against current green; produces a new `MergeDescriptor` at a fresh `arrival_seq`.
4. The new descriptor enqueues on the commit queue. Its `audit_addendum=true` flag tells the `MergeAuditCoordinator` to skip audit dispatch for this entry and mark it accepted directly.
5. The original merge's commit-queue entry transitions from `auditing` to `accepted`.
6. Commit resolver eventually flushes both entries to disk in arrival order. Water line advances past both.
7. Retention refs to `ReplicaVFS_N` drop to zero; it's released and its memory reclaimed.

If both audits rejected, skip steps 2–7. Retention refs drop; `ReplicaVFS_N` is released. The rejected queue entry remains, blocking the resolver until the architect dispatches a remediation (§3.8).

### 3.8 Rejection, remediation, and chain bypass

When either replica emits Rejected, the first rejection short-circuits (the sibling's verdict is no longer consulted). The queue entry transitions to `rejected`. The commit resolver blocks at that slot.

The architect subscribes to `AuditMergeResultTopic` (observability bus — the only bus touchpoint in this design) for rejections and composes a remediation DAG. Each fix task declares `remediates_seq = K` (the rejected merge's arrival_seq). When the architect dispatches the fix pipeline:
- `BeginPipelineConfig.BaseCopyVersion = K` (byte-for-byte materialization from the rejected merge's Copy — which is the state the remediation fixes).

The remediation pipeline runs, writes its fixes, its pipeline inspector handoffs via `handoff_to_green`. MergePipe merges it as `M`. `M`'s `MergeDescriptor.SupersedesSeq = K`.

When `M`'s audit accepts, the coordinator calls `CommitQueue.MarkSuperseded(K, M)`. The commit resolver, on reaching K's slot, sees `state=superseded, superseded_by=M`. It flushes M's diff in K's slot and pops both entries atomically.

**Chain bypass on reject**: `ReplicaVFS_K` marked abandoned. Any `ReplicaVFS` that had been chained through K skips over it — its effective parent becomes K's parent. Reads that would have resolved through K's overlay now resolve through K's parent.

**Chain surgery on supersession**: M's `ReplicaVFS` is constructed with parent = K's parent (bypass). M is a fresh attempt at K's slot, not a continuation — avoids carrying forward the defect K was rejected for.

**Re-audit on substantive OT transform**: When M's audit accepts and MergePipe transforms later in-flight merges K+1..M-1 against M, if any merge's diff is substantively changed by the transform, a fresh audit replica is spawned for it against the re-transformed diff. "Substantive" heuristic: any conflicting mod, any audited-context path touched by M.

### 3.9 Mid-audit awareness

A replica auditing merge N sees its own `ReplicaVFS_N` plus the chain of ancestors. Meanwhile green may continue advancing as parallel pipelines merge. The replica does not automatically see merges with `arrival_seq > N`.

If a replica wants to incorporate later merges:
- `merges_after(seq)` skill returns the descriptors with `merged_seq > seq`.
- Replica chooses: continue audit on current context, rebase to the latest (cancel and restart audit with fresh chain base), or emit rejection-with-hint referencing the later work.

Autonomy is preserved. No preemption. No silent context mutation.

## 4. Lifecycle

### 4.1 Pipeline dispatch → pipeline work → pipeline-inspector accept → merge

```
architect composes DAG / orchestrator dispatches task
    ↓ (orchestrator here is in its normal role — agent runtime management; NOT the audit path)
SessionVFS.BeginPipeline(cfg)
    cfg.BaseCopyVersion = current_green_seq         [normal dispatch]
    cfg.BaseCopyVersion = failing_merge_seq          [remediation dispatch — from architect]
    pipelineVFS byte-materialized from Copy at cfg.BaseCopyVersion
pipeline runs, pipeline inspector audits locally
pipeline inspector calls handoff_to_green                 [DIRECT PROTOCOL MESSAGE]
    ↓ direct method (PipelineCommitter interface)
SessionVFS.MergePipelineIntoGreen(ctx, pipelineID)
    - MergePipe OT transforms overlay against current green
    - MergeDescriptor produced with PipelineInspectorCertificate attached
    - CommitQueue.Enqueue(desc) → ControlWAL write-ahead
    - fireMergeCallbacks(desc) — synchronous
        ↓
MergeAuditCoordinator.onMerge(desc)                       [in agents/shared — NOT orchestrator]
    - BeginAuditReplicaVFS(desc.MergedVersion) → ReplicaVFS_N
    - RetainParent(N)
    - RecordReplicaSpawned(inspector_id, N) → ControlWAL
    - RecordReplicaSpawned(tester_id, N) → ControlWAL
    - Inspector.SpawnAuditReplica(ctx, req)       [direct interface call]
    - Tester.SpawnAuditReplica(ctx, req)          [direct interface call]
MergePipelineIntoGreen returns; pipelineVFS released; pipeline pod released.
```

### 4.2 Replica audit → decision → commit-queue transition → seal → disk

```
Inspector / Tester replica runs its LLM tool loop under ctx:
    - AuditMergeContext (session, replica ID, versions) attached
    - AuditDecisionFinalizer attached (direct callback to coordinator)
    - FileAccess scoped to ReplicaVFS_N
  Replica reads: ReplicaVFS_N local → chain parent → disk
  Replica writes: ReplicaVFS_N local store + ControlWAL (write never touches disk)
  Replica may call merges_after(seq) for progressive awareness
  Replica terminates by calling emit_audit_decision(decision, summary, concerns)

emit_audit_decision skill:
    - Pulls AuditDecisionFinalizer from ctx
    - Invokes it directly with the result
    - No bus publish for this path

MergeAuditCoordinator.finalizeDecision(result):
    - ReplicaLifecycleLog.RecordDecided(replica_id, decision, summary, concerns) → ControlWAL
    - CopyRetention.Release(replica_id)                  [also WAL]
    - Combine verdicts:
        both accepted  → CommitQueue.MarkAccepted(N, replica_id)
        first rejection → CommitQueue.MarkRejected(N, replica_id, summary, concerns)
    - Optional: publish on AuditMergeResultTopic (observability; TUI + architect)

If accepted:
    ReplicaVFS_N.Seal() — freeze overlay, compute diff
    Submit diff to MergePipe as audit_addendum pipeline-equivalent
    MergePipe OT-transforms diff against green → new descriptor arrival_seq
    CommitQueue.Enqueue(audit_addendum_desc)           [coordinator auto-accepts]
    CommitQueue.MarkAccepted(audit_addendum_seq)
If rejected:
    ReplicaVFS_N.Abandon() — marked; chain bypass for downstream
    CommitQueue entry stays at `rejected`; blocks resolver
    Architect (via AuditMergeResultTopic observability) composes remediation

CommitResolver (owned by SessionVFS; not orchestrator):
    Polls queue head; on accepted → DiskFlusher.Flush(diff)
    MarkCommitted → pop → CopyRetention.AdvanceWaterLine → retention GC
    ReplicaVFS_N released when no downstream refs remain
```

### 4.3 Remediation → supersession → commit

```
Architect subscribes AuditMergeResultTopic; sees rejection for merge K
Architect composes remediation DAG (fix tasks declare remediates_seq = K)
Architect dispatches through its normal route    [orchestrator is in its normal role here]
    ↓ pipeline is dispatched with BeginPipelineConfig.BaseCopyVersion = K
Remediation pipeline materializes byte-for-byte from Copy at K
Remediation pipeline runs, pipeline inspector handoff_to_green
    ↓ SessionVFS.MergePipelineIntoGreen produces descriptor M with SupersedesSeq = K
    ↓ MergeAuditCoordinator.onMerge spawns replicas for M
    ↓ Both replicas audit M against its progressive context
If M's audit accepts:
    MergeAuditCoordinator.finalizeDecision → MarkAccepted(M)
    CommitQueue.MarkSuperseded(K, M)
    ReplicaVFS_M.Seal → audit_addendum merge M.audit
CommitResolver reaches K's slot:
    state = superseded, superseded_by = M
    Flush M.diff (and M.audit's diff if present) in K's slot
    Pop K, pop M, pop M.audit
    Water line advances past all three
```

### 4.4 DAG terminal / session close

```
DAG cancelled or failed:
    sweep all tasks in the DAG
    for each task:
        cancel pending replicas (RecordReplicaCrashed; retention released)
        mark queue descriptors as abandoned (CommitQueue.Abandon → WAL)
        release ReplicaVFS
    water line advances past abandoned seqs

Session close:
    MergeAuditCoordinator.Stop — halts further dispatches
    In-flight replicas run to their terminal decisions (ctx-scoped; quiesced)
    CommitResolver stops
    SessionVFS.Close:
        Semantic WAL fsync + close
        ControlWAL fsync + close (SessionCloseEpoch marker)
        All ReplicaVFS released
```

## 5. Cleanup

A `ReplicaVFS_N` is releasable when ALL of the following:

1. Its merge descriptor is **terminally resolved** — committed, superseded-and-supersedor-committed, or abandoned.
2. No **child ReplicaVFS** holds N as its parent.
3. No **pending remediation dispatch** targets N's Copy.

Implementation: refcount + water-line hybrid. Refcount tracks explicit holds (child chain links, pending remediation targets). Water line advances on disk-commit / supersession / abandonment events. A GC pass runs on each trigger, releasing ReplicaVFS below the water line whose refcount has reached zero.

Releasing a ReplicaVFS makes it unreconstructable in RAM. Any subsequent dispatch targeting a released ReplicaVFS fails loudly. No silent fallback — the commit resolver has already advanced past it.

### 5.1 Blocked queue backpressure

When a rejection blocks the queue head without a dispatched remediation, subsequent merges accumulate. Their ReplicaVFS instances are held (cannot be released). Chain grows.

Mitigations:

- **Architect SLA**: if a rejection has no remediation dispatched within a configured window (measured in session-open wall time — §8), the system emits a control-plane escalation event and a user notification.
- **Explicit abandon**: architect can declare "drop K, treat as abandoned." K's state → `abandoned`. Water line advances past K. Subsequent merges whose audit context depended on K's presence are re-audited.
- **Pipeline dispatch backpressure**: when the non-terminal queue depth exceeds a configured threshold, new pipeline dispatches block at the dispatch gate. The system stops accumulating more work than it can resolve.

### 5.2 Forensic retention

Policy knob:

- **Archive before release**: terminal merge diffs serialized to a forensic directory with TTL retention.
- **Reconstruct on demand**: no archive; semantic WAL replay reconstructs any terminal state until WAL entries are compacted.
- **None**: terminal state released immediately; no forensic capability.

Default: reconstruct-on-demand with WAL retention tied to `session_vfs` WAL retention settings.

## 6. Components

### 6.1 SessionVFS — authoritative

Owns:
- `MergePipe` (OT engine; the single conflict-resolution primitive).
- Semantic WAL (disk-backed `VersionedWAL`; records merge descriptors).
- Control WAL (disk-backed append-only log; records commit-queue transitions, retention events, replica lifecycle, session epochs, overlay writes/deletes).
- `CommitQueue` (WAL-backed state machine; subscribers for observability).
- `CopyRetention` (refcount + water line; WAL-backed).
- `ReplicaLifecycleLog` (WAL-backed; in-flight / decided / crashed).
- `CommitResolver` (goroutine; owned by SessionVFS, started in `Open`, stopped in `Close`).
- Merge callback registry: `RegisterMergeCallback(cb)` fires synchronously from `MergePipelineIntoGreen` after enqueue.
- ReplicaVFS factory: `BeginAuditReplicaVFS(mergedVersion) (*ReplicaVFS, error)` — constructs a new ReplicaVFS with the correct parent wired from the merge log.
- Disk-backed storage:
  - `semantic-wal/` — versioned WAL files for merge content.
  - `control-wal/log.bin` — append-only control WAL.

Exposes:
- `CommitQueue()`, `CopyRetention()`, `ReplicaLifecycleLog()` — accessors for coordinator.
- `MergeDescriptors()`, `FindMergeDescriptor(ver)`, `MergesAfter(ver)` — for observability + mid-audit awareness.
- `CommitQueueDepth()` — for backpressure gates.

Removes (from legacy):
- `s.reviews.candidates` map.
- `ExtractReviewCandidate`.
- `AcceptActiveReviewCandidate`.
- `DiscardReviewCandidate`.
- `ActivateReviewCandidate`.
- `reviewVFS` overlay.

### 6.2 CommitResolver — owned by SessionVFS

Not the orchestrator. Not the coordinator. The resolver belongs to the VFS layer because its inputs (`CommitQueue`, `mergeLog`) and outputs (`DiskFlusher.Flush`, `CopyRetention.AdvanceWaterLine`) are entirely VFS-layer.

- Constructed in `NewSessionVFS`.
- Started in `SessionVFS.Open` (or equivalent lifecycle entry).
- Stopped in `SessionVFS.Close`.
- Polls the queue head; flushes accepted / superseded / abandoned in arrival order; advances the water line.

### 6.3 ReplicaVFS — per-merge COW in-RAM VFS

New in `core/versioning/replica_vfs.go`.

- Built on `PipelineVFS` foundation (content store, modifications tracking, mutex).
- Parent pointer: `*ReplicaVFS` or nil (nil parent = direct read-through to disk).
- Local content store for writes + read-cache.
- Bloom filter over owned paths.
- `Read(path)`: local → chain parent (recursive with per-node bloom early-out) → disk.
- `Write(writerID, path, bytes)`: mutex; local store; `ControlKindReplicaOverlayWrite` WAL entry.
- `Delete(writerID, path)`: mutex; local deletes set; `ControlKindReplicaOverlayDelete` WAL entry.
- `Seal() (diff, error)`: freeze overlay; compute diff vs parent; return diff for MergePipe submission.
- `Abandon()`: mark abandoned; downstream chain walks bypass.

### 6.4 MergeAuditCoordinator — in `agents/shared`, not orchestrator

The coordinator is a direct-dispatch pump. It:

- `Start(ctx)` registers a merge callback on SessionVFS.
- On merge fire: calls `Inspector.SpawnAuditReplica(ctx, req)` and `Tester.SpawnAuditReplica(ctx, req)` directly. No bus.
- `finalizeDecision(result)` is invoked by the replica's `emit_audit_decision` skill via ctx-scoped callback. Handles lifecycle / retention / queue transition with AND-semantics (both-accept for accept, first-rejection-wins).
- On accept: calls `ReplicaVFS_N.Seal()`, submits the sealed diff to MergePipe. On reject: calls `ReplicaVFS_N.Abandon()`.
- Optionally publishes finalized results on `AuditMergeResultTopic` for observability.

The coordinator is NOT the orchestrator. The coordinator has NO relationship to the orchestrator. The coordinator is created by the session-lifecycle wiring (cmd/tui.go or equivalent) alongside SessionVFS and torn down with it.

### 6.5 Pipeline inspector

Changes from legacy:

- `handoff_to_ot` renamed to `handoff_to_green` (no alias). Semantic: "I have reviewed this pipeline's work and assert it is ready for global integration."
- Emits a `PipelineInspectorCertificate` alongside the handoff: declared-scope, open-concerns, summary, tester-verdict. Attached to the `MergeDescriptor`.
- The handoff itself is a direct protocol method call through `PipelineCommitter.MergePipelineIntoGreen`. No bus publish. No orchestrator route.

### 6.6 Global inspector

Changes:

- Implements `AuditReplicaSpawner.SpawnAuditReplica(ctx, req)` — the direct-dispatch entry point.
- Per-audit: LLM tool loop runs with ctx carrying `AuditMergeContext` + `AuditDecisionFinalizer`.
- `FileAccess` scoped to `ReplicaVFS_N`.
- Writes allowed (linter, formatter outputs).
- Registered skills:
  - `workspace_read` / `workspace_write` — scoped to `ReplicaVFS_N`.
  - `merges_after` — progressive awareness.
  - `emit_audit_decision` — terminal; invokes ctx-scoped finalizer.
  - No `accept_checkpoint` / `discard_checkpoint` (concept gone).
  - `commit_to_disk` — removed (commit resolver handles flushing).
- Deterministic replica identity: `inspector-global#replica-{session_id}:{merged_version}`.

### 6.7 Global tester

Changes:

- Symmetric with inspector. Implements `AuditReplicaSpawner.SpawnAuditReplica(ctx, req)`.
- `FileAccess` scoped to `ReplicaVFS_N` (same VFS the inspector uses).
- Writes allowed (new test files, harness adjustments) — part of the merge's committable work.
- Registered skills: the above set, minus anything inspector-specific.
- Deterministic replica identity: `tester-global#replica-{session_id}:{merged_version}`.

### 6.8 Architect remediation

Changes:

- Subscribes to `AuditMergeResultTopic` (observability) for rejections.
- Composes remediation DAG; fix task nodes declare `remediates_seq = K` (the rejected arrival_seq). Multi-predecessor remediations declare the highest referenced seq per §3.8.
- Dispatches through normal orchestrator routing; orchestrator translates `remediates_seq` into `BeginPipelineConfig.BaseCopyVersion`. Orchestrator's role here is the same as any other pipeline dispatch — it is NOT audit-loop-aware.

### 6.9 Orchestrator — unchanged role

The orchestrator is not modified by this design beyond:

- Honoring `BeginPipelineConfig.BaseCopyVersion` when dispatching pipelines (already implemented).
- Routing `remediates_seq` metadata from architect DAG tasks into `BeginPipelineConfig`.

The orchestrator does not subscribe to merge callbacks. Does not spawn audit replicas. Does not run the commit resolver. Does not handle audit results. Does not issue `AuditMergeRequest`. **Does not participate in the audit loop in any capacity.**

## 7. Correctness

- **Serializability**: MergePipe OT linearizes all merges (pipeline + audit-addendum) into green. Commit queue enforces FIFO arrival order on disk commits. Disk only receives accepted-and-unblocked changesets.
- **Audit context is honest**: each replica sees exactly its own ReplicaVFS_N + parent chain + disk. Never a state frozen before newer ancestors; never a state mutating under it.
- **Cross-changeset coherence**: caught by the replica that has both changesets in view — always the later one. Earlier replicas need not prophesy.
- **Conflict-free merges**: every conflict point routes through MergePipe OT — pipeline merge, audit-addendum merge, supersession re-transform. One serializer, authoritative ordering.
- **Supersession preserves invariants**: a rejected merge's slot is replaced by its supersedor on commit. Dependent audits are re-triggered if substantively re-transformed by OT.
- **No silent work loss**: rejected work remains in the chain (as an abandoned ReplicaVFS) until its slot is resolved (committed via supersession or abandoned). In-flight ReplicaVFS writes are durable via the ControlWAL; a crash mid-write replays on next open.
- **Audit writes are first-class**: inspector + tester writes seal into MergePipe as an audit-addendum merge. Commits to disk through the standard resolver path. No scratch state lost.

## 8. Robustness

- **Durable queue**: commit queue + retention + replica lifecycle + overlay writes all persist to ControlWAL per-transition (write-ahead discipline). Restart replays the WAL; in-flight replicas re-launch with deterministic IDs and rehydrated steering ledgers.
- **Replica crash**: descriptor state returns to `auditing` or `pending`; fresh replica relaunches against the same descriptor with the SAME deterministic runtime agent ID (the steering ledger rehydrates).
- **Cascade rejection**: bounded by architect SLA / explicit-abandon / pipeline-dispatch backpressure.
- **Architect stall**: backpressure kicks in; user-escalation on SLA breach (wall-clock measured in session-open epochs; closed session does not advance the timer).
- **Session close**: water line advances as far as possible; unresolved entries remain in-flight (not abandoned) so a later session-open can resume them.
- **Crash vs clean close — identical recovery path**: the WAL is the single source of truth. Clean close writes a `SessionCloseEpoch` marker, but recovery doesn't depend on it. Replay always produces a consistent state.

## 9. Scalability

- **Pipeline concurrency** unchanged from today (MergePipe already handles concurrent merges).
- **Audit concurrency**: N parallel merges → N parallel inspector+tester pairs. Bounded by LLM budget and the chain-depth backpressure cap.
- **Per-audit cost**: O(diff size) for reasoning; O(chain depth) for read resolution (with bloom filter early-out). Context is inherited through the chain, not re-derived.
- **Commit serialization**: the sole bottleneck is the resolver's disk-flush; delta I/O is small.
- **Memory**: bounded by queue depth × average ReplicaVFS footprint (paths actually touched per audit). Water-line advancement and backpressure cap depth.
- **ControlWAL growth**: compacted as the water line advances past a merge — entries referencing released Copies become eligible for compaction.

## 10. Performance

- **P50 accept**: short. A trivial changeset gets a fast audit (small diff, few reads, minimal writes) and commits quickly when queue head.
- **P50 reject**: short audit (issue visible in the diff); remediation dispatch runs in parallel with other commits.
- **P99 (rejected head blocks queue)**: shaped by architect remediation time; no algorithmic amplification.
- **Throughput ceiling**: `(replica-pair pool size) × (1 / avg audit latency)`. Independent of single-audit duration modulo head-of-line blocking.
- **ReplicaVFS read amplification**: bounded by chain depth × per-node bloom check (≈ O(depth) with tiny constants). Hot paths cache at the first level that resolves them.

## 11. Implementation plan

Stages ordered for maximum incremental correctness — each stage self-contained and shippable.

### Stage 1 — Pipeline-accept merges directly via direct protocol. Candidate map eliminated. Pods release on pipeline-accept.

Fixes the original blank-remediation-workspace bug.

Files:
- `core/versioning/session_vfs.go` — `MergePipelineIntoGreen` is the direct protocol endpoint.
- `core/versioning/session_review.go` — deprecate / remove `ExtractReviewCandidate`.
- `agents/shared/pipeline_committer.go` — rename path in the committer interface.
- `agents/shared/pipeline_protocol.go` — `handoff_to_green` skill invokes the new path.
- `agents/inspector/pipeline/**` — skill renamed from `handoff_to_ot` (no alias).

### Stage 2 — MergeDescriptor, Copy addressing, byte-for-byte pipeline dispatch materialization.

Data-layer Copy concepts. `BeginPipeline` honors `BaseCopyVersion`.

Files:
- `core/versioning/session_vfs.go` — `CopyAt`, `MaterializePipelineFromCopy`, `MergeDescriptor`, arrival_seq tracking.
- `core/versioning/merge_pipe*.go` — tag merges with arrival_seq; record in semantic WAL.
- Pipeline dispatch plumbing to thread `BaseCopyVersion` through.

### Stage 3 — Direct-protocol audit dispatch. ControlWAL durability. Commit queue + resolver wired.

Core audit loop lands.

Files:
- `core/versioning/control_entry.go` + `control_wal.go` — durability primitive.
- `core/versioning/commit_queue.go` — WAL-backed state machine + subscription.
- `core/versioning/copy_retention.go` — WAL-backed; ref + water-line.
- `core/versioning/replica_lifecycle.go` — Spawned/Decided/Crashed log.
- `core/versioning/commit_resolver.go` — owned by SessionVFS; started in Open; stopped in Close.
- `core/versioning/session_vfs.go` — merge-callback registry; `RegisterMergeCallback`, `fireMergeCallbacks` in `MergePipelineIntoGreen`; resolver lifecycle; session open/close epochs.
- `agents/shared/audit_merge_contract.go` — `AuditMergeRequest`, `AuditMergeResult`, `AuditMergeContext`, `AuditDecisionFinalizer` ctx key.
- `agents/shared/merge_replica_identity.go` — deterministic replica IDs.
- `agents/shared/merge_audit_coordinator.go` — the direct-dispatch pump.
- `agents/shared/emit_audit_decision_skill.go` — terminal skill, direct-finalizer.
- `agents/shared/merges_after_skill.go` — progressive awareness.
- `agents/inspector/global/audit_merge.go` — `SpawnAuditReplica` + ReplicaVFS-scoped tool loop.
- `agents/tester/global/audit_merge.go` — symmetric.

### Stage 4 — ReplicaVFS COW chain. Audit-addendum merge path. Supersession + re-audit.

Closes the correctness loop on progressive context and rejection recovery.

Files:
- `core/versioning/replica_vfs.go` — `ReplicaVFS` type; COW reads; mutex-protected writes; seal/abandon.
- `core/versioning/control_entry.go` — overlay write/delete/seal/abandon kinds.
- `core/versioning/session_vfs.go` — `BeginAuditReplicaVFS`; chain-parent wiring; seal integration with MergePipe; audit-addendum merge flag handling.
- `agents/shared/merge_audit_coordinator.go` — sealed diff submission; supersession orchestration.
- Re-audit trigger on substantive OT transform.

### Stage 5 — Resume protocol; SLA; TUI state-resync; mid-audit tooling; backpressure; forensic retention.

Operational layer.

Files:
- `core/versioning/session_vfs.go` — full `Open` replay: commit queue, retention, lifecycle, chain, overlays; relaunch in-flight replicas with deterministic IDs.
- `agents/shared/merge_audit_coordinator.go` — `ResumeInFlightReplicas`.
- Dispatch-gate backpressure keyed off `CommitQueue.Depth()`.
- SLA tracker keyed off session-open epochs (ControlWAL).
- TUI state-resync event on session Open.
- Optional forensic-archive hook at retention release.

## 12. Migration

Staged migration — not atomic:

- Stage 1 eliminates candidate-map usage from the pipeline-accept path. Any residual in-flight candidate state drains through the legacy path; new work flows through the new path.
- Stage 3 fully retires the candidate-map. `accept_checkpoint` / `discard_checkpoint` / `commit_to_disk` are deleted (no aliasing, no backcompat shims).
- Stage 4 enables the audit-addendum flow. Merges that landed before Stage 4 continue through the stage-3 path (audit with a zero-overlay ReplicaVFS) until they resolve.
- Single deploy boundary per stage. No mixed-generation state retained across stages.

## 13. Non-goals

- **Not replacing MergePipe or OT.** Both remain authoritative. OT is the single conflict-resolution primitive; this design leans on it rather than introducing new merge algorithms.
- **Not replacing the semantic WAL.** The ControlWAL is additive — a session-level decision log separate from the content-level semantic WAL.
- **Not removing per-pipeline local inspectors.** Pipeline-level review stays; this design adds global per-merge audits on top.
- **Not centralizing audit dispatch in the orchestrator.** The orchestrator has no role in this process (§0).

## 14. Open questions

- **Replica-pair pool size / cost tuning**: start unbounded; add caps based on observed LLM burn.
- **Architect SLA window**: policy knob; sensible default TBD post-deployment.
- **Forensic retention default**: reconstruct-on-demand proposed.
- **Re-audit substantive-change threshold**: initial heuristic — any conflicting mod, any audited-context path that changed. Refine post-deployment.
- **ControlWAL compaction cadence**: driven by water line; specifics TBD.

---

This document is the canonical specification. Implementation follows per §11. The orchestrator has no role in any of it (§0).
