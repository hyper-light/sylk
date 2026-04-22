# Parallel Global VFS — Progressive Audit, Byte-Copy Remediation

**Status**: Design, pending implementation.
**Scope**: `core/versioning`, `agents/orchestrator`, `agents/shared` (pipeline and global review protocols), `agents/inspector` (global).
**Supersedes**: the candidate-map review flow (`ExtractReviewCandidate` → `s.reviews.candidates` → `AcceptActiveReviewCandidate`) and the implicit "release pod on OT accept / strand on OT reject" pod lifecycle.

## 1. The problem

Two failures, structurally connected.

**Failure 1 — remediation pipelines receive a blank workspace.**
When the global inspector rejects a candidate, the candidate's pipeline VFS has already been closed by `ExtractReviewCandidate` (`core/versioning/session_review.go:57`). The candidate's mods sit orphaned in `s.reviews.candidates`; they were never promoted to global VFS. When the architect dispatches a remediation DAG, each fix task's `BeginPipeline` opens a fresh pipeline VFS whose base reader reads from global VFS only — which still has the pre-rejection state. `workspace_read` returns empty for paths the original pipeline created. The rejected work is invisible to the fix that's supposed to fix it.

**Failure 2 — global-inspector audit is strictly serialized.**
Today the global review processes one candidate at a time, top-to-bottom. Multiple pipelines finishing concurrently queue behind a single slow audit. The inspector cannot encounter new work mid-audit, only at the start of each audit turn. Pipeline concurrency is present but audit concurrency is absent, so overall throughput is bottlenecked by the global inspector's serial cadence.

These two failures share a root: the current model has one "global state under review" and one "review candidate in flight," and both the rejection path and the parallelism story are limited by that singular structure.

## 2. Rejected alternatives and why

The following approaches were considered and rejected during design:

**Persistent rejected-overlay tier + remediation inheritance chain.** Each rejection's mods sit in a parallel overlay; remediation pipelines read through an overlay chain. Rejected because overlay chains multiply memory linearly with rejection depth and add per-read chain-walk cost. Correct but structurally wasteful.

**Pre-analysis layer (linters, AST probes, format checks).** Proposed as a deterministic filter reducing LLM load. Rejected because every concrete probe either (a) required per-language parser infrastructure that does not scale across the language matrix we support, or (b) duplicated work the pipeline inspector and role agents (architect, archivalist) are already doing, at additional arbitrary LLM cost. The global inspector already consults those agents on-demand; pre-querying them is brittle and redundant.

**Shadow inspector (second LLM running ahead of the real one).** Rejected because the pipeline inspector already performs the local review a shadow would synthesize, and its output is durable. Adding a shadow adds an LLM turn that re-derives information we already hold.

**Per-merge replicas on independent snapshots (plain Option B).** Rejected because each snapshot would audit in isolation, missing cross-candidate coherence issues that only emerge when two changesets are in view together. The progressive variant (below) resolves this by making each replica's context cumulative.

**Independence-set parallelism based on file-surface disjointness.** Rejected because big-picture coherence concerns (convention drift, interface duplication, plan divergence) cross file surfaces even when diffs are disjoint. Surface-disjointness is not a sufficient independence signal for a role whose purpose is emergent coherence.

**MVCC snapshot handles as the base for remediation pipeline VFS.** Rejected in favor of byte-for-byte materialization. Hard isolation trumps page sharing: materialized copies eliminate divergence tracking, give predictable memory and read cost, and make failure domains clean.

## 3. The model

### 3.1 Progressive Global VFS Copies

Every pipeline merge into the global state produces a new **Copy** — a logical handle at a specific monotonic `arrival_seq`.

```
Copy₀ = disk state (last committed checkpoint)
Copy₁ = Copy₀ + changeset A (from pipeline A)
Copy₂ = Copy₁ + changeset B (from pipeline B, transformed against A)
Copy₃ = Copy₂ + changeset C
...
Copyₙ = Copyₙ₋₁ + changeset N
```

Each `Copy` is **materialized on demand** from a base checkpoint plus replayable WAL deltas. Persistent representation is the sequence of changesets in the WAL; in-memory representations are reconstructed at dispatch or replica launch.

### 3.2 Pipeline-inspector-accept merges directly into green

The pipeline inspector's `handoff_to_ot` no longer performs `ExtractReviewCandidate` into a pending-candidate map. Instead, the pipeline's overlay mods merge via existing `MergePipe`/OT machinery directly into green. The merge produces a new `Copy` at a new `arrival_seq`. The pipeline VFS releases; the pipeline's pod releases.

This is the stage-1 fix for the blank-remediation-workspace bug. After this change:

- Remediation pipelines dispatched to read from green (current `Copy_n`) see all predecessor work.
- The `s.reviews.candidates` intermediate is eliminated.
- Pod lifecycle is locally determined by pipeline inspector accept, not coupled to global review outcomes.

### 3.3 Per-merge audit replicas with progressive context

Each merge launches one **global inspector replica** scoped to that specific changeset:

- **Audit target**: the changeset's diff (the mods produced by this pipeline's pipeline-accept).
- **Audit context (base)**: the `Copy` immediately preceding this changeset (= disk + all earlier changesets in `arrival_seq` order).

The replica reasons about "does this changeset cohere with its context?" Its context therefore **progressively accumulates**: replica N sees disk + changesets 1..N-1 + (optionally) the current changeset under review. Replica 1 sees only disk + changeset 1. Replica 2 sees disk + changeset 1 + changeset 2.

This placement is deliberate. Cross-changeset coherence concerns (duplicate interfaces, conflicting dependencies, convention drift) are caught by the replica that has both changesets in its view — always the later one. Earlier replicas are honest about what they can see at the time of their audit. Later replicas inherit the full picture.

Replicas run in parallel. They do not block each other. They emit accept / reject decisions independently.

### 3.4 Arrival-ordered FIFO commit queue

Audit decisions accumulate in parallel; disk commits linearize:

```
MergeDescriptor {
    arrival_seq       : monotonic
    source_task_id    : pipeline that produced the changeset
    base_copy_seq     : the Copy this merge applied against
    merged_copy_seq   : the Copy this merge produced
    paths             : set of paths touched
    diff              : the OT-transformed changeset
    pipeline_certificate : narrative from the pipeline inspector
    audit_replica_id  : which replica is auditing
    state             : auditing | accepted | rejected | superseded | committed | abandoned
    superseded_by     : arrival_seq of the superseding changeset (when state == superseded)
}
```

The commit resolver is a single goroutine processing the queue in `arrival_seq` order:

- Head `accepted`: flush the changeset's diff into the on-disk checkpoint, advance the water line, pop.
- Head `auditing`: wait.
- Head `rejected` without supersession: block, architect must dispatch remediation.
- Head `rejected` with `superseded_by` set: promote the supersedor's changeset into this slot's disk-commit order, pop both entries.

Later-accepted entries block behind unresolved earlier entries. Disk never receives a changeset whose predecessors are unresolved.

### 3.5 Rejection and remediation

When a replica rejects, the orchestrator records the rejection on the merge descriptor and notifies the architect. Architect composes a remediation DAG whose fix tasks target the failing `arrival_seq`.

**Copy selection for remediation dispatch** — the remediation's pipeline VFS is materialized from the **latest Copy that contains the failing work and all its dependencies**:

- Single-changeset rejection at seq K → target Copy_K.
- Combined rejection (replica saw both A and B together and flagged their combination) → target the later seq's Copy, which includes both.
- General rule: pick the highest `arrival_seq` among the Copies referenced in the rejection's reasoning.

### 3.6 Byte-for-byte materialization

At remediation dispatch (and at pipeline dispatch in general), the target Copy is materialized into the new pipeline's VFS **byte-for-byte**. The pipeline VFS owns its bytes; there is no read-through, no shared pages, no MVCC handle, no upstream reference.

Implementation:
1. Identify target Copy's `arrival_seq = K`.
2. Walk the Copy lineage from the latest checkpoint through changesets 1..K, materializing each file's final state into the new pipeline VFS's storage.
3. Pipeline VFS tracks subsequent writes in its write-journal.
4. At pipeline-accept, the write-journal is the changeset; submit to MergePipe for merge into green.

Memory cost per pipeline = materialized Copy bytes + per-pipeline overlay writes. Independent of chain depth, independent of concurrent pipeline count (beyond linear multiplication).

### 3.7 Supersession transforms

When remediation at seq M supersedes rejection at seq K:

1. M's changeset is extracted relative to Copy_K (M's base).
2. K's slot in the commit queue is marked `superseded`, `superseded_by: M`.
3. When the commit resolver reaches K's slot, it emits M's changeset to disk in K's place.
4. M's own slot in the queue (further down) is dropped; it was consumed by the supersession.

For later changesets (K+1, K+2, ..., M-1) whose audits ran in context that included K:

- If the superseding changeset M touches paths disjoint from their audits → their audits remain valid; they commit in order.
- If M touches paths that their audits depended on → MergePipe/OT re-transforms their diffs against M; re-audit is triggered for any substantively-changed diff.

Re-audit is the same mechanism as a fresh audit — spin a replica against the re-transformed diff. Bounded cost.

### 3.8 Mid-audit new information

A replica auditing changeset N sees Copy_N-1 + changeset N as its context. Meanwhile green continues advancing as other pipelines merge. The replica does not automatically see merges > N.

If the replica wants to incorporate later merges:

- `MergesAfter(N)` inspector tool returns the list of changesets with seq > N.
- Replica chooses: continue audit on current context, rebase to the latest (cancel and restart audit with fresh base), or emit rejection-with-hint referencing the later work.

Autonomy is preserved. No preemption. No silent context mutation.

## 4. Lifecycle

### 4.1 Pipeline dispatch → pipeline work → pipeline-inspector accept → merge

```
orchestrator.DispatchTask(task)
  → SessionVFS.BeginPipeline(cfg)
      cfg.BaseCopySeq = current_green_seq           // normal dispatch
      cfg.BaseCopySeq = failing_changeset_seq        // remediation dispatch
  → byte-for-byte materialize Copy_cfg.BaseCopySeq into new pipelineVFS
  → pipelineVFS is fully independent

pipeline agents run...
pipeline writes → pipelineVFS write-journal accumulates

pipeline inspector accepts (handoff_to_green replaces handoff_to_ot)
  → SessionVFS.MergePipelineIntoGreen(pipelineID)
      - extracts write-journal
      - submits to MergePipe → OT transform against any green advancement since dispatch
      - produces changeset at arrival_seq = monotonic_next
      - updates green to Copy_new
      - launches global inspector replica with MergeDescriptor
  → pipelineVFS released
  → pipeline pod released
```

### 4.2 Replica audit → commit queue → disk commit

```
replica audits (context = Copy_base_copy_seq, target = changeset.diff)
replica emits decision
  → commit queue updates descriptor.state

commit resolver loop:
  head := queue.peek()
  switch head.state:
    accepted   → diskFlusher.ApplyChangeset(head.diff) ; queue.pop ; advance water line
    auditing   → wait
    rejected without supersedor → block ; orchestrator notifies architect
    rejected with supersedor M →
        diskFlusher.ApplyChangeset(M.diff)
        queue.pop(head)
        queue.dropEntry(M.seq)   // M was consumed by supersession
        advance water line
```

### 4.3 Rejection → remediation dispatch

```
replica rejects changeset at seq K
  → commit queue marks K.state = rejected ; K.rejection_reasons = ...
  → orchestrator publishes AuditRejected event to architect
  → architect composes remediation DAG with fix tasks carrying remediates_seq = K

orchestrator dispatches remediation task
  → SessionVFS.BeginPipeline(cfg.BaseCopySeq = K) [per 4.1]
  → remediation pipeline runs on byte-for-byte Copy_K

remediation pipeline inspector accepts
  → SessionVFS.MergePipelineIntoGreen with remediation flag + supersedes_seq = K
  → new changeset at seq M
  → commit queue descriptor for M: state=auditing, supersedes=K
  → replica audits M with progressive context (Copy_M-1 + M's diff)
  → on accept: K.superseded_by = M ; commit resolver resumes
```

### 4.4 DAG terminal

```
DAG cancelled or failed (no recovery path):
  → sweep all tasks in the DAG
  → for each task: 
      - cancel pending replicas
      - mark queue descriptors as abandoned
      - release pipeline VFS if still open
      - release pods
  → advance water line past abandoned seqs (they never hit disk)
```

## 5. Cleanup

A `Copy_N` is releasable when ALL of the following:

1. Its changeset is **terminally resolved** — disk-committed, superseded-and-supersedor-committed, or abandoned.
2. No **in-flight replica** holds Copy_N as audit context base.
3. No **pending remediation dispatch** targets Copy_N.
4. In lazy-materialization mode: Copy_N's bytes are not required to materialize a still-live downstream descriptor (i.e., there is no live Copy_M, M > N, that would replay through Copy_N's changeset to reconstruct).

Implementation: refcount + water-line hybrid. Refcount tracks explicit holds (replicas, pending remediation targets). Water line advances on disk-commit / supersession / abandonment events. A GC pass runs on each trigger, releasing Copies below the water line whose refcount has reached zero.

Release semantics under lazy materialization: drop the descriptor, compact WAL entries older than the new water line into the current checkpoint, archive or delete old WAL segments per retention policy.

Release semantics under eager materialization: delete the stored bytes.

Releasing a Copy makes it unreconstructable. Any subsequent dispatch targeting a released Copy fails loudly. No silent fallback.

### 5.1 Blocked queue backpressure

When a rejection blocks the queue head without a dispatched remediation, subsequent changesets accumulate in the queue without committing. Their Copies are held (cannot be released). Water line is stuck.

Mitigations:

- **Architect SLA**: if a rejection has no remediation dispatched within a configured window, the orchestrator escalates (control-plane event, user notification). The system forces queue resolution.
- **Explicit abandon**: architect can declare "drop K, treat as abandoned." K's state → `abandoned`. Water line advances past K. Subsequent changesets whose audit context depended on K's presence are re-audited.
- **Pipeline dispatch backpressure**: orchestrator halts new pipeline dispatches when the queue exceeds a configured depth. The system stops accumulating more work than it can resolve.

### 5.2 Forensic retention

Policy knob. Options:

- **Archive before release**: terminal Copies serialized to a forensic directory with TTL retention.
- **Reconstruct on demand**: no archive; WAL replay can reconstruct any terminal Copy until its WAL entries are compacted.
- **None**: terminal Copies are released immediately; no forensic capability.

Default is reconstruct-on-demand with WAL retention tied to `session_vfs` WAL retention settings.

## 6. Components

### 6.1 SessionVFS — authoritative

Adds:
- `GreenState()` → current green; effectively `Copy_latest`.
- `CopyAt(seq)` → materialize Copy at a specific arrival_seq. Byte-for-byte output.
- `MergePipelineIntoGreen(pipelineID, flags)` → replaces `ExtractReviewCandidate`; performs the OT merge and produces a new Copy + MergeDescriptor.
- `MaterializePipelineFromCopy(cfg, seq)` → called from `BeginPipeline(cfg)`; copies bytes from `CopyAt(seq)` into the new pipelineVFS.
- Commit queue: `CommitQueue` type with `Enqueue`, `Peek`, `Advance`, `MarkAccepted`, `MarkRejected`, `MarkSuperseded`, `Abandon`.
- Refcount / water-line GC: `RetainCopy`, `ReleaseCopy`, `RunCopyGC`.

Removes:
- `s.reviews.candidates` map.
- `ExtractReviewCandidate`.
- `AcceptActiveReviewCandidate` → replaced by commit resolver flushing diffs to disk.
- `DiscardReviewCandidate`.
- `ActivateReviewCandidate`.
- Review-overlay / reviewVFS concepts — green IS the under-audit state; disk is the authorized state.

### 6.2 Orchestrator

Adds:
- Audit replica dispatch hook: on each `MergePipelineIntoGreen` completion, launch a global inspector replica with the resulting MergeDescriptor.
- Commit resolver goroutine: processes queue in arrival order, calls SessionVFS disk-flush on accepted heads.
- Rejection notification path: publishes `AuditRejected` event to architect on replica reject.
- Remediation dispatch: reads `remediates_seq` from remediation DAG task nodes, passes to `BeginPipelineConfig.BaseCopySeq`.

Changes:
- `handlePipelineUpdate`: removes the OT-followup publish path on inspector success. Pipeline-inspector-accept now completes synchronously via `MergePipelineIntoGreen`. Pod release follows immediately.
- `failPendingCheckpointReview` / `completePendingCheckpointReview`: removed. Replaced by commit-resolver's disk-flush path driven by audit decisions, not by sending task responses.
- `handleTaskFailed`: does NOT call `rollbackTaskDraft`. Task failure is orthogonal to Copy lifecycle under the new model (the relevant event is replica rejection, not task failure).

### 6.3 Pipeline inspector

Changes:
- `handoff_to_ot` renamed to `handoff_to_green` (or equivalent). Semantic: "I've reviewed this pipeline's work and assert it is ready for global integration."
- Emits a `PipelineInspectorCertificate` alongside the handoff: the pipeline inspector's declared-scope, open-concerns, summary, tester-verdict. Carried on the MergeDescriptor.

### 6.4 Global inspector

Changes:
- No longer pulls from a single-active-candidate review overlay.
- Each invocation is scoped to a specific MergeDescriptor.
- Reads audit context from Copy_base_copy_seq materialized on demand.
- Reads target changeset diff directly from the MergeDescriptor.
- Has access to `MergesAfter(seq)` tool for mid-audit awareness.
- Emits accept/reject to the commit queue for the specific `arrival_seq`.

Removes:
- `accept_checkpoint` skill as currently structured (operates on `reviewVFS` overlay — concept gone).
- `discard_checkpoint` skill (same reason).

Keeps / restructures:
- `commit_to_disk`: now operates at the water-line / checkpoint-advancement level, not per-candidate. Likely invoked by the commit resolver on quiescence, not directly by the global inspector per-decision. Alternatively, disk-flush happens per-accepted-commit automatically and `commit_to_disk` becomes a no-op or an approval gate.

### 6.5 Architect remediation

Changes:
- Remediation DAG task nodes declare `remediates_seq` (the failing `arrival_seq`). Multi-predecessor remediations declare the latest `arrival_seq` per §3.5.
- Orchestrator reads `remediates_seq` and sets `BeginPipelineConfig.BaseCopySeq` accordingly.
- `RemediationResolutionCancelAndReplan` cancels the DAG → triggers per §4.4.

## 7. Correctness

- **Serializability**: OT linearizes merges into green. Commit queue enforces FIFO arrival order on disk commits. Disk only receives accepted-and-unblocked changesets.
- **Audit context is honest**: each replica sees exactly Copy_base + its own target. Never a state frozen before newer predecessors; never a state mutating under it.
- **Cross-changeset coherence**: caught by whichever replica has both changesets in its context — always the later one. Earlier replicas need not prophesy.
- **Supersession preserves invariants**: a rejected changeset's slot is replaced by its supersedor on disk-commit. Dependent audits are re-triggered if substantively re-transformed by OT.
- **No silent work loss**: rejected work remains in green until its slot is resolved (committed via supersession or abandoned). `workspace_read` from any dispatched pipeline sees all green state up to its base Copy.

## 8. Robustness

- **Durable queue**: commit queue persists to WAL. Restart replays the queue; in-flight replicas re-launch; unresolved entries resume.
- **Replica crash**: descriptor state returns to `auditing` or `pending`; fresh replica can relaunch against the same descriptor.
- **Cascade rejection**: bounded by architect SLA / explicit-abandon / pipeline-dispatch backpressure.
- **Architect stall**: backpressure kicks in; user-escalation on SLA breach.
- **Session close**: water line advances as far as possible; unresolved entries abandoned; Copies released.

## 9. Scalability

- **Pipeline concurrency** unchanged from today (MergePipe already handles concurrent merges).
- **Audit concurrency** new: N parallel merges → N parallel replicas. Replicas bounded by LLM budget / replica pool cap.
- **Per-audit cost**: O(diff size) for reasoning — context is inherited, not re-derived per audit.
- **Commit serialization** is the sole bottleneck; disk-flush is small-delta I/O.
- **Memory**: bounded by queue depth × average Copy materialization cost per held Copy. Water-line advancement and backpressure cap depth.

## 10. Performance

- **P50 accept**: short. A trivial changeset gets a fast audit (small context) and commits quickly when queue head.
- **P50 reject**: short audit (the issue is usually visible in the diff); remediation dispatch proceeds in parallel with other commits.
- **P99 (rejected head blocks queue)**: shaped by architect remediation time; no algorithmic amplification.
- **Throughput ceiling**: `(replica pool size) × (1 / avg audit latency)`. Independent of how long any single audit takes, assuming no head-of-line blocking.

## 11. Implementation plan

Stages are ordered for maximum incremental correctness — each stage is self-contained and ships something usable.

### Stage 1 — Pipeline-accept merges directly into green. Candidate map eliminated. Pipeline pods release on pipeline-accept.

This fixes the original bug. Remediation pipelines dispatched after this stage read from green, which now contains all predecessor work.

Files touched:
- `core/versioning/session_vfs.go` — add `MergePipelineIntoGreen`. Remove the candidate-map intermediate where possible (the remaining global-review accept path can keep using candidates temporarily for backward compat during transition, but pipeline-accept goes directly to green).
- `core/versioning/session_review.go` — deprecate `ExtractReviewCandidate` or rewire it to invoke `MergePipelineIntoGreen`.
- `agents/shared/pipeline_committer.go` — rename `ExtractReviewCandidate` to `MergePipelineIntoGreen` in the committer interface.
- `agents/shared/pipeline_protocol.go` — `handoff_to_ot` skill invokes the new path.
- `agents/orchestrator/pipeline_runtime.go` — `finalizePipelineUpdateCtx` on inspector success no longer publishes OT-followup; pod release follows directly.
- `agents/orchestrator/orchestrator.go` — remove `rollbackTaskDraft` from `handleTaskFailed`.
- `agents/orchestrator/dag_bridge.go` — remove unconditional `markTaskPodResetPending` from `EventNodeFailed`.

Tests:
- Regression: remediation pipeline reads predecessor's work via green.
- Pipeline-accept produces a new green version.
- Pod release after pipeline-accept.

### Stage 2 — Merge descriptors, Copy addressing, byte-for-byte pipeline dispatch materialization.

Introduce the `MergeDescriptor` / `arrival_seq` / `Copy` concepts at the data layer. `BeginPipeline` accepts `BaseCopySeq` and materializes byte-for-byte from the addressed Copy.

Files touched:
- `core/versioning/session_vfs.go` — add `CopyAt`, `MaterializePipelineFromCopy`, `MergeDescriptor`, `arrival_seq` tracking.
- `core/versioning/merge_pipe.go` (or equivalent) — tag every merge with `arrival_seq`, record in WAL.
- `core/versioning/wal.go` — extend semantic WAL to record MergeDescriptors.
- `agents/orchestrator/pipeline_runtime.go` / task dispatch — plumb `BaseCopySeq` through `BeginPipelineConfig`.

Tests:
- `CopyAt(N)` materializes the expected state.
- Concurrent merges produce distinct `arrival_seq`s.
- WAL replay reconstructs any Copy.

### Stage 3 — Per-merge audit replicas. Rejection path writes to commit queue. Architect dispatch reads `remediates_seq`.

Replace the single-global-inspector-turn model. Each merge launches a replica. Rejections feed the architect.

Files touched:
- `agents/inspector/global/**` — replica launcher; audit skill scoped to a MergeDescriptor.
- `agents/orchestrator/audit_replica_dispatch.go` (new) — spawn a replica per merge.
- `agents/orchestrator/commit_queue.go` (new) — durable queue.
- `agents/orchestrator/commit_resolver.go` (new) — FIFO resolver goroutine.
- `agents/orchestrator/checkpoint_review.go` — retire `completePendingCheckpointReview` / `failPendingCheckpointReview`, replace with commit-queue-driven flow.
- `agents/architect/remediation_control.go` — read `remediates_seq` on remediation result; pass to orchestrator dispatch.

Tests:
- N concurrent merges → N concurrent audits.
- FIFO commit: later-accepted waits for earlier-unresolved.
- Rejection blocks queue; remediation supersedes; disk-commit proceeds.

### Stage 4 — Supersession transforms; re-audit on substantive OT change; cleanup / refcount / water line.

Close the correctness loop on supersession. Release Copies aggressively.

Files touched:
- `core/versioning/session_vfs.go` — refcount map, water line, GC pass.
- `core/versioning/merge_pipe.go` — detect substantive transform change; flag for re-audit.
- `agents/orchestrator/commit_resolver.go` — re-audit trigger on supersession.

Tests:
- Supersession with non-conflicting later changeset: commits proceed normally.
- Supersession with conflicting later changeset: triggers re-audit of affected later entries.
- Water line advances on each resolved changeset.
- DAG terminal releases all held Copies for the DAG.

### Stage 5 — Mid-audit awareness tooling, backpressure, forensic retention policy.

Quality-of-life and operational:

- `MergesAfter(seq)` inspector tool.
- Pipeline dispatch backpressure when queue depth > threshold.
- Forensic archive on release (policy-gated).
- Architect SLA escalation on blocked queue head.

Files touched:
- `agents/shared/global_review_protocol.go` — add `MergesAfter` skill.
- `agents/orchestrator/dispatch_gate.go` — depth-based gating.
- `core/versioning/session_vfs.go` — archive-on-release hook.

## 12. Migration

The transition from the candidate-map model to the progressive-Copy model is not atomic. Staged migration:

- Stage 1 eliminates candidate-map usage from the pipeline-accept path. Global review's accept-candidate path continues to exist temporarily, invoked only for any residual state in flight at deploy time.
- Stage 3 fully retires the candidate-map. The global-review `accept_checkpoint` / `discard_checkpoint` / `commit_to_disk` skills are restructured around commit-queue events.
- Migration-time in-flight candidates: drained via the old path; new work flows through the new path. A single deploy boundary.

## 13. Non-goals

- This design does not replace MergePipe or OT. Both remain authoritative for merge transform.
- This design does not replace the semantic WAL. The commit queue is additive — semantic-WAL entries per merge are the canonical history.
- This design does not remove per-pipeline local inspectors. Pipeline-level review stays.

## 14. Open questions

- **Replica pool size / cost tuning**: start unbounded, add cap based on observed LLM budget burn.
- **Architect SLA window**: policy knob; sensible default TBD post-deployment.
- **Forensic retention default**: reconstruct-on-demand proposed; TBD based on debugging needs.
- **Re-audit substantive-change threshold**: what counts as "substantively changed by OT" for re-audit triggering. Initial heuristic: any file with conflicting mods, any audited-context path that changed. Refine post-deployment.

---

This document is the specification. Implementation follows in the staging above.
