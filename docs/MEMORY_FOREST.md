# Memory Forest

## Goal

Sylk needs a predictive `Memory Forest` that helps every agent maximize user intent, not merely answer the literal prompt. The forest is a multi-tree, multi-timescale memory system that:

- preserves immutable evidence
- projects that evidence into typed trees specialized by intent facet
- maintains active canopies for the current user, session, and project
- learns cross-tree associations that improve future retrieval and planning
- returns agent-ready `BranchPacket`s instead of loose search hits
- uses memory to safely exceed user intent on quality without silently exceeding scope

The primary output of the system is:

`what most helps this agent advance the user’s intent right now`

## Core Principles

- `Evidence is immutable`
  Raw prompts, replies, tool results, code facts, citations, and outcomes are append-only.
- `Inference is versioned`
  Hypotheses, decisions, summaries, abstractions, and preferences are derived projections, never destructive rewrites.
- `Intent is first-class`
  Retrieval is conditioned on active intent and active branch state before generic lexical or semantic similarity.
- `Multi-tree beats single-tree`
  Different trees specialize in different facets of user intent and collaborate through relays.
- `Fast episodic + slow semantic`
  The system follows complementary learning systems rather than flattening all memory into one store.
- `Agent usability matters`
  Agents do not query raw graph primitives by default. They consume skills that return branch packets with provenance, confidence, conflicts, and next actions.
- `Fail open`
  If any forest subsystem degrades, Sylk falls back to today’s content and hybrid retrieval paths without losing evidence.

## Forest Layers

### Soil

The `Soil` layer is the immutable evidence substrate:

- `UniversalContentStore` content entries
- raw documents and code artifacts
- tool inputs and outputs
- user messages and agent messages
- validation outputs and workflow transitions

This layer is the source of truth for raw evidence.

### Ledger

The `Forest Ledger` is an append-only event stream derived from soil and explicit forest writes.

Each event records:

- event type
- session, agent, and turn identity
- tree family
- scope
- intent and branch identity
- confidence and salience
- provenance references
- supersession and contradiction links
- event payload

The ledger is the only write surface for the forest.

### Canonical Graph

The `Canonical Graph` is the typed relational projection over the ledger and current knowledge graph. It represents:

- intent roots and revisions
- constraints and success criteria
- evidence and claims
- questions and hypotheses
- decisions and outcomes
- preferences and capability episodes
- opportunities and conflict sets
- branch summaries and relays

The graph preserves provenance and contradiction state.

### Tree Projections

The forest is not one tree. It is a family of specialized trees materialized from the same graph:

- `Intent Forest`
  Explicit goals, intent revisions, latent intent hypotheses, active subgoals, unresolved questions.
- `Constraint Forest`
  Must-haves, prohibitions, authority boundaries, scope limits, performance and correctness requirements, success criteria.
- `Evidence Forest`
  Local code evidence from Librarian, external evidence from Academic, historical workflow evidence from Archivalist.
- `Decision Forest`
  Candidate choices, chosen decisions, supersessions, rationale, rejected branches.
- `Outcome Forest`
  Validation results, regressions, fixes, user reactions, empirical results, workflow outcomes.
- `Preference Forest`
  User preferences, explanation style, risk tolerance, scope tolerance, review strictness, favored tradeoffs.
- `Capability Forest`
  Which agents, skills, tools, and workflows succeed under which conditions.
- `Opportunity Forest`
  Adjacent-value branches, proactive upgrades, safe surplus quality, predicted upside.
- `Conflict Forest`
  Contradictions, stale assumptions, disputed claims, competing branches, unresolved forks.

### Canopy

The `Canopy` is the active root set for the current context. It is computed across multiple horizons:

- turn horizon
- session horizon
- user horizon
- project horizon

The canopy answers:

- which intent roots are active now
- which branches are hot
- which constraints and preferences currently dominate
- which failures should shape near-term planning

### Relay Graph

The `Relay Graph` is the cross-tree activation fabric. It links branches that repeatedly matter together across the framework.

Examples:

- an intent branch can activate a preferred agent or skill path
- a code evidence branch can activate a historical failure branch
- an opportunity branch can activate a scope risk branch
- a decision branch can activate the external evidence that justified it

The relay graph is where framework-wide informational cross-pollination happens.

### Substrate Network

The `Substrate Network` is the adaptive underlay beneath the explicit trees and relay graph.

It is inspired by fungal-growth mathematics, but in Sylk it should remain a technical systems layer with concrete responsibilities:

- diffuse intent and uncertainty pressure through the active graph
- adapt edge conductance based on successful traffic and reuse
- raise or lower frontier scores for where exploration should grow next
- propagate guardian-style inhibition without mutating provenance or truth state

The substrate network should be persisted as:

- conductance edges
- context-scoped nutrient and inhibition state
- active frontiers for the current session, horizon, and agent type

Conceptually:

- the trees store explicit semantic structure
- the relay graph links related structure
- the substrate network decides where attention and exploration should flow next

### Warmth Layer

The `Warmth` layer is ACT-R-compatible retrieval pressure over branches and relays:

- repeated use strengthens recall
- recent use strengthens recall
- successful use reinforces a branch
- contradicted or unhelpful recall cools it down

Warmth is not truth. It is learned retrieval utility.

## Biological and Learning Dynamics

### Complementary Learning Systems

The forest must operate with dual memory systems:

- `episodic forests`
  Fast, session-local, high-fidelity, provisional.
- `semantic forests`
  Slow, consolidated, stable abstractions reused across sessions and projects.

Fast memory captures what just happened. Slow memory captures what reliably keeps helping.

### Reconsolidation

Every significant recall is a potential reconsolidation event:

- recalled and validated branches are refreshed and reinforced
- recalled and contradicted branches are weakened or split into contradiction sets
- partially validated branches can fork into valid and invalid descendants

The system never silently overwrites contradictory history.

### Hebbian Association

Branches, relays, agents, and skills that repeatedly succeed together should strengthen together.

Hebbian learning should reinforce:

- intent <-> evidence
- evidence <-> decision
- decision <-> outcome
- intent <-> capability path
- branch <-> branch relay pairs

This is the basis for emergent cross-tree assistance.

### Physarum Pruning and Regrowth

Demand should shape visibility:

- frequently useful branches gain canopy visibility and relay thickness
- stale, contradicted, or low-demand branches thin out
- dormant branches remain recoverable and can regrow when demand returns

Nothing important is destroyed at the evidence layer.

### Prioritized Replay

Background replay consolidates recent high-salience episodes into reusable semantic structure.

Replay priority should consider:

- user correction
- success or failure intensity
- contradiction density
- novelty
- repeated reuse
- downstream impact
- unresolved uncertainty

Replay transforms episodes into precedents, preference priors, capability priors, and caution rules.

## Storage Model

The forest should maximize Sylk’s existing infrastructure instead of replacing it:

- raw evidence remains in `UniversalContentStore`
- lexical retrieval remains in Bleve
- graph state remains compatible with `VectorGraphDB`
- ACT-R memory code remains the source for activation equations and decay priors
- forest projections persist in additional SQLite tables in the same database

The forest storage model is:

- `soil`
  content entries and source artifacts
- `ledger`
  append-only forest events
- `branches`
  materialized branch projections
- `canopies`
  active root sets by horizon
- `relays`
  Hebbian cross-tree links
- `replay queue`
  prioritized background consolidation jobs
- `warmth traces`
  ACT-R-compatible access history for branch packets

## Runtime Architecture: CQRS Projection

The forest's runtime is event-sourced with CQRS projection. The event ledger is the only source of truth; every queryable surface (Branch, Canopy, Relay edges, Substrate state) is a *derived projection* that can be rebuilt deterministically from the ledger. There is no read-modify-write on the source of truth.

This section documents the mechanics: how events are appended, how the projector consumes them, how lease coordination keeps multi-process correct, how failure is contained, and how every projection eventually catches up.

### Why CQRS

The naive design — read the current Branch row, mutate it in memory, write it back — is a classic lost-update hazard under concurrent writers. SQLite serializes writers at the file lock, but SELECT-then-UPDATE inside a transaction can still see a stale snapshot if the transaction was started before the most recent commit. Two AppendEvent calls against the same branch race; one increment is silently dropped. Every counter downstream (`SuccessCount`, `Utility`, `ConflictScore`) drifts; every retrieval-quality signal becomes suspect.

CQRS makes lost updates *unrepresentable*:

- AppendEvent is a pure INSERT — no read, no merge, no in-memory state.
- The projection (Branch row, Relay edges, Canopy, Substrate-dirty marker) is built by a single-leader projector consuming events from the ledger in seq order.
- Single-leader means no concurrent contention on any projection row; the read-modify-write window never has a competing writer.
- Multi-process safety is enforced by a lease in the database — only one process holds the lease at a time, even across crashes.

This is the maximally-correct shape: the source of truth never gets read-modify-written; projections are derived.

### Architecture Overview

```
                 ┌─────────────────────────────────┐
                 │   forest_events  (the ledger)   │
                 │   append-only, immutable        │
                 │   ★ SOURCE OF TRUTH ★            │
                 └──────────────┬──────────────────┘
                                │ joined with
                 ┌──────────────▼──────────────────┐
                 │  forest_event_seq_log           │
                 │  AUTOINCREMENT seq, UNIQUE eid  │
                 │  ★ MONOTONIC ORDERING ★          │
                 └──────────────┬──────────────────┘
                                │
                  reads in seq order, batch-wise
                                │
                 ┌──────────────▼──────────────────┐
                 │  Branch Projector (single       │
                 │  tracked goroutine, lease-      │
                 │  coordinated leader)            │
                 │                                 │
                 │  applyBranchProjectorEvent:     │
                 │    BeginTx                      │
                 │      projectBranchTx (apply)    │
                 │      setProjectorWatermark...   │
                 │        (gated on lease holder)  │
                 │    Commit                       │
                 │    seqNotify.Advance            │
                 │    runProjectorPostCommit       │
                 └──────────────┬──────────────────┘
                                │ writes to
        ┌───────────────┬───────┴───────┬───────────────┐
        ▼               ▼               ▼               ▼
   forest_branches  forest_relay_   forest_canopies  forest_
   (projection)     edges                            substrate_*
   (projection)     (projection)    (projection)     (dirty markers)
```

Reads against any projection table are eventually consistent. Callers needing read-your-writes use `WaitForBranchSeq` (event-driven).

### Schema Layout

| Table | Role | Key fields |
|---|---|---|
| `forest_events` | Ledger — append-only via MEM-03 trigger; immutable | `id` PRIMARY KEY (UUIDv4) |
| `forest_event_seq_log` | Monotonic sequence + dedup index | `seq` AUTOINCREMENT PK; `event_id` UNIQUE |
| `forest_projector_state` | Per-projector lease + watermark + health | `projector_name` PK, `last_applied_seq`, `leader_holder`, `leader_lease_until`, `health_status`, `last_error` |
| `forest_branches` | Branch projection | `id` PK; `last_applied_seq` watermark |
| `forest_relay_edges` | Relay graph projection | `(source, target, relation)` PK; `last_applied_seq` |
| `forest_canopies` | Active root sets per horizon | `canopy_key` PK; `last_applied_seq` |
| `forest_substrate_sessions` | Per-session substrate-dirty marker | `session_id` PK; `last_applied_seq` |
| `forest_substrate_*` | Substrate state derived by maintenance loop | session-scoped |
| `forest_branch_traces` | ACT-R warmth trace | `(branch_id, accessed_at, access_type)` PK |
| `forest_replay_queue` | Background replay work | AUTOINCREMENT id |
| `forest_training_examples` | Labeled retrieval examples | AUTOINCREMENT id |
| `forest_models` | Serialized booster models | `(model_key, version)` PK |

The MEM-03 append-only trigger on `forest_events` raises ABORT on any UPDATE or DELETE — the ledger cannot be mutated in place. The seq log is a sibling table (separate from the ledger) so AUTOINCREMENT works without violating the trigger's invariant.

### Sequence Allocation

Every event carries a monotonic `Seq int64` assigned at append time:

1. `INSERT INTO forest_events (...) ON CONFLICT (id) DO NOTHING` — idempotent. Returns `inserted=true` if a new row was written, `false` if a duplicate event ID was already present.
2. If `inserted == true`: `INSERT INTO forest_event_seq_log (event_id, appended_at)` — AUTOINCREMENT supplies the next seq atomically. Returns the just-allocated seq via `LastInsertId()`.
3. If `inserted == false`: `SELECT seq FROM forest_event_seq_log WHERE event_id = ?` — returns the canonical seq the original append got.

Both writes happen inside a single transaction. SQLite serializes writers at the file lock, so concurrent appends of distinct event IDs each get a unique seq via AUTOINCREMENT, and concurrent appends of the same event ID converge to the same seq (one inserts, the other reads).

`event.Seq` on the in-memory struct is set **only after** `tx.Commit()` succeeds. If commit fails, the in-memory struct retains its prior Seq (typically zero), so callers never observe a seq that doesn't correspond to durable state.

### Idempotency and Replay

Re-appending an event with an already-present `id` is a no-op: the INSERT does nothing, the seq log lookup returns the existing seq, the function returns success with `event.Seq` populated to the canonical value. This makes the write path safe for:

- Bus-driven harvest replay (the claims integration delivers deltas at-least-once)
- WAL recovery (process restart re-emits any in-flight writes)
- Manual replay during operator-driven schema migration (reset projector watermark to 0; projector replays from seq 1 forward)

The projector likewise dedups: `applyBranchProjectorEvent` checks `event.Seq <= state.lastAppliedSeq` and short-circuits.

### The Branch Projector

The projector is a single tracked goroutine started in `MemoryForest.New` after `startMaintenance`. Lifecycle:

```
New()
  → service.startBranchProjector()
      → m.wg.Add(1)
      → go runBranchProjectorLoop()
          → defer recoverProjectorPanic()
          → for {
              acquireBranchProjectorLease()
              runProjectorSession(state, backoff)
                → for { processBranchProjectorBatch(state) }
            }

Close()
  → runCancel()                  // cancel runCtx
  → close(stopCh)
  → wg.Wait()                    // drain projector + maintenance
```

The outer loop acquires or renews the lease via `UPDATE forest_projector_state ... WHERE (leader_lease_until < now OR leader_holder = me)`. Atomic UPDATE with WHERE clause — only one process succeeds at a time. The lease is renewed every 10 seconds against a 30-second TTL, leaving 20 seconds of slack for transient pauses.

#### Apply Step

Per event, `applyBranchProjectorEvent` does:

```
event.Seq <= state.lastAppliedSeq → skip (defensive idempotency)

BeginTx
  projectBranchTx(tx, event):
    base = getBranchTx(tx, event.BranchID)
    branch = applyEvent(base, event)
    branch.LastAppliedSeq = event.Seq
    upsertBranchTx(tx, branch)
    upsertRelayEdgesTx(tx, event)
    refreshCanopiesTx(tx, event.SessionID, event.TaskID, event.IntentID)
    enqueueReplayTx(tx, branch, event)
    markSubstrateDirtyTx(tx, event.SessionID, event.Timestamp)
  setProjectorWatermarkUnderLeaseTx(tx, name, m.projectorID, seq, ts)
    → returns (updated bool, err)
    → updated=false if leader_holder != m.projectorID (lease lost)
Commit

state.lastAppliedSeq = event.Seq
seqNotify.Advance(event.Seq)              // wake WaitForBranchSeq callers
runProjectorPostCommit(event, branch, ...)
  → recordProjectorWarmth (best-effort)
  → recordProjectorLabels (best-effort)
  → scheduleSubstrateRefresh / scheduleReplayAt / scheduleTraining
```

`projectBranchTx` is the same code path as the legacy `projectEventTx` *minus* the event INSERT (the event is already in the ledger). It updates Branch + Relay + Canopy + replay queue + substrate-dirty marker atomically.

#### Lease-Gated Watermark (Split-Brain Prevention)

The watermark UPDATE is gated on `leader_holder = m.projectorID`. If our process pauses past the lease window, another process acquires the lease and starts processing. If we then resume mid-transaction, our `setProjectorWatermarkUnderLeaseTx` UPDATE affects 0 rows (the new leader took our row), and we abort the transaction without committing. The new leader picks up cleanly without overlap.

Without this gate, a long GC pause could put two projectors in concurrent-apply territory. With the gate, the resumed process discards its in-flight transaction; only the active leader writes.

#### Watermark Persistence

The watermark (`forest_projector_state.last_applied_seq`) is updated *inside the same transaction* as the projection mutation. Either:

- The projection commits and the watermark advances, or
- The transaction rolls back and neither moves.

This makes crash recovery deterministic: `state.lastAppliedSeq` always reflects the highest seq whose projection actually committed. On restart, the projector loads its state and resumes from `last_applied_seq + 1`.

### Failure Modes and Containment

#### Transient vs Fatal Errors

The projector classifies apply errors:

- **Transient**: `database is locked`, `SQLITE_BUSY`, `context.Canceled`, network errors. Retried via the outer loop's exponential backoff (100ms → 30s cap).
- **Fatal**: `no such column`, `no such table`, `constraint failed`, `datatype mismatch`, `syntax error`. Wrapped in `errProjectorHalt`; the projector marks itself halted and stops processing.

Default for unrecognized errors is **transient** — the cost of a false retry is a brief delay; the cost of a false halt is operator wakeup.

#### Poison Pill Protection

If the same event seq fails repeatedly (e.g., a corrupted event payload that triggers a non-classified deterministic error), `projectorState.recordPoisonHit` increments a counter scoped to the event seq. After `projectorPoisonPillThreshold = 8` consecutive failures of the same seq, the projector escalates to halted with a `poison pill` cause. Subsequent seq advances reset the counter. This bounds infinite-retry storms on uncategorized deterministic errors.

#### Panic Recovery

The projector goroutine is wrapped in `defer recoverProjectorPanic()`. A panic anywhere in the call tree (apply, post-commit, lease ops) is captured: stack trace truncated to `projectorErrTruncate` bytes (rune-aware), logged via `slog.Error`, and persisted on `forest_projector_state.last_error` with `health_status='halted'`. The goroutine exits cleanly via `wg.Done`. Operator queries `ProjectorStatus(...)` to see what panicked.

#### Lease Loss

If the projector loses the lease (process pause beyond the 30s TTL, another process acquires), `renewLease` returns `errLeaseHeldByOther`. The projector exits its session, backs off, and re-attempts acquisition. If the lease has been taken, it sleeps `projectorWaitForLease = 5s` and retries. The losing process becomes a follower and will resume leading if the active leader's lease expires.

#### Crash Recovery

On process restart, `New()` runs `ensureSchema` (idempotent) and `ensureForestEventSeqBackfilled` (populates seq log for any events that lack a seq — only relevant during initial migration; idempotent via `INSERT OR IGNORE`). The projector starts, acquires the lease, loads `last_applied_seq` from `forest_projector_state`, and resumes processing from there.

Events appended-but-not-yet-projected when the previous process died are still in the ledger; the new leader picks them up. No event is lost. No projection is double-applied (seq dedup at apply time).

### WaitForBranchSeq — Event-Driven Read-Your-Writes

Callers needing strict read-after-write semantics call `WaitForBranchSeq(ctx, seq, timeout)`. Implementation is event-driven via the `seqNotifier`:

- The notifier holds `currentSeq` (highest seq applied) and a list of waiters with target seqs.
- After every successful apply, the projector calls `seqNotify.Advance(seq)`, which closes the done-channel of every waiter whose target is now satisfied.
- Waiters block on a select over: the done channel, a timer, the caller's context, and `runCtx`.
- Timer firing rechecks state — if Advance fired in the same instant, returns success rather than a false timeout.
- Halt fires ALL waiter channels with a flag check — waiters re-check halt state on wake and return the halt error rather than success.
- On `Close()`, `runCtx` cancellation wakes pending waiters; they return `forest closed`.

No polling. No 250ms wake delay. Tests assert event-driven semantics via a tight 100ms budget.

### Synchronous Mode (Tests)

`Config.SynchronousProjection = true` runs the projection inline in `AppendEvent` instead of starting the projector goroutine:

```go
AppendEvent(ctx, event):
    appendEventLedger(ctx, event)   // ledger + seq alloc + commit
    if synchronousProjection:
        projectInlineForTests(ctx, event)
            // same body as applyBranchProjectorEvent minus the
            // lease check (no leader to contend with)
```

This preserves read-your-writes semantics without `WaitForBranchSeq` polling and avoids the SQLite shared-cache lock contention that the in-memory test driver doesn't tolerate. Production never sets this flag — the async projector is what makes CQRS valuable.

The inline path also calls `seqNotify.Advance` so any cross-goroutine `WaitForBranchSeq` callers wake correctly under sync mode.

### Goroutine Accounting

All goroutines registered on `m.wg`:

| Goroutine | Lifecycle |
|---|---|
| Maintenance loop | `startMaintenance` in `New`; exits on `runCtx.Done` or `stopCh` close |
| Branch projector | `startBranchProjector` in `New` (only when `!synchronousProjection`); exits on `runCtx.Done` or `stopCh` close, recovered from panic |

`Close()` cancels `runCtx`, closes `stopCh`, waits on `wg`. Both goroutines exit deterministically. No untracked spawns elsewhere — `notifyProjector`, lease ops, post-commit side effects, `WaitForBranchSeq` are all synchronous on their callers' goroutines.

### Concurrency Invariants

Documented invariants the implementation preserves:

1. **Source of truth is never read-modify-written.** `forest_events` is append-only by trigger; appends are pure INSERT.
2. **Sequence allocation is atomic.** AUTOINCREMENT on `forest_event_seq_log` serializes at the SQLite file lock; concurrent appends of distinct event IDs cannot get the same seq.
3. **Single-leader projection.** Only the leaseholder writes the projection. Lease is enforced atomically via UPDATE-with-WHERE on `leader_lease_until`.
4. **Lease-gated watermark.** Watermark UPDATE is gated on `leader_holder` — if our hold is taken mid-transaction, the UPDATE affects 0 rows, the apply aborts, no projection commits.
5. **Idempotent replay.** `event.Seq <= state.lastAppliedSeq` short-circuits in the apply path; ON CONFLICT DO NOTHING short-circuits in the append path.
6. **Crash atomicity.** Every projection mutation + watermark update is in one transaction. Either both commit or neither does.
7. **Tracked goroutines only.** Every goroutine is on `m.wg`; `Close` drains.
8. **Panic isolation.** `defer recoverProjectorPanic()` keeps a panic from killing the process; halt state is persisted for operator triage.
9. **Bounded poison-pill retries.** A persistently-failing event halts after 8 consecutive failures; doesn't retry forever.

### Multi-Process Safety

The forest can be opened from multiple processes pointing at the same SQLite file (e.g., during deployment overlap, or a multi-process server topology). Safety properties:

- Only one process is the active branch projector at any instant — enforced by lease.
- Concurrent `AppendEvent` from any number of processes is safe — SQLite serializes writers at the file lock; AUTOINCREMENT supplies unique seqs; idempotent INSERTs collapse duplicates.
- Projection reads see eventually-consistent state; readers in non-leader processes get the same projection rows the leader produced, just with normal SQLite read-snapshot semantics.
- Process A's projector pauses → its lease expires → process B acquires → process A resumes and discovers its lease is taken, becomes follower without writing. The lease-gated watermark prevents process A's in-flight transaction from committing.

### Health and Observability

`forest.ProjectorStatus(name)` returns the current state of a named projector (default `"branch"`):

- `lastAppliedSeq` — last seq successfully projected
- `lastAppliedAt` — wall-clock of last apply
- `leaderHolder` — process ID currently holding the lease
- `leaderLeaseUntil` — when the current lease expires
- `healthStatus` — `idle | running | halted`
- `lastError` — most recent error message (truncated to 4096 bytes, rune-safe)
- `lastErrorAt` — wall-clock of most recent error
- `poisonSeq`, `poisonCount` — current poison-pill counter (in-memory only; not persisted)

Logging:

- `slog.Error("forest_append_event_failed", ...)` on every AppendEvent failure
- `slog.Error("forest_projector_halted", ...)` when the projector marks itself halted
- `slog.Error("forest_projector_panic", ...)` on goroutine panic (with stack)
- `slog.Warn("forest_projector_error", ...)` on transient errors that retry
- `slog.Warn("forest_projector_lease_failed", ...)` on lease acquisition failures (suppressed for normal `errLeaseHeldByOther` waits)
- `slog.Debug("forest_projector_warmth_failed" / "forest_projector_label_failed", ...)` on best-effort post-commit side-effect failures

Errors during shutdown (`runCtx.Err() != nil`) are not persisted to `forest_projector_state` to avoid noise on the closing DB.

### Operational Procedures

**Resetting the projector** (e.g., after schema migration that changes `applyEvent` semantics):

```sql
UPDATE forest_projector_state
SET    last_applied_seq = 0,
       schema_version = schema_version + 1,
       health_status = 'idle',
       last_error = ''
WHERE  projector_name = 'branch';

-- Optionally rename the old projection for verification:
ALTER TABLE forest_branches RENAME TO forest_branches_v1;
-- then re-run ensureSchema to recreate forest_branches; restart projector.
```

The projector replays from seq 1 forward, applying the new logic.

**Backup**: dump `forest_events` + `forest_event_seq_log`. That's the source of truth. Projections regenerate by replaying the event log against the projector.

**Restoring from backup**: load `forest_events` + `forest_event_seq_log`. On `New()`, the projector starts with `last_applied_seq=0`, replays from seq 1 forward, projections rebuild.

**Diagnosing a halted projector**:

```sql
SELECT projector_name, health_status, last_error, last_error_at,
       last_applied_seq, leader_holder, leader_lease_until
FROM   forest_projector_state
WHERE  projector_name = 'branch';
```

Inspect `last_error` for the failure reason. If recoverable (e.g., a one-off corrupt event), quarantine the offending event and reset `health_status = 'idle'`. The projector resumes from where it halted.

### Future Projections

The architecture admits multiple projectors over the same event log, each maintaining its own watermark in `forest_projector_state`. Reserved column `last_applied_seq` is already present on `forest_canopies`, `forest_relay_edges`, `forest_substrate_sessions` for future per-projection projectors:

- `substrate` projector: rebuilds substrate state on dirty session events
- `canopy` projector: maintains active root sets per horizon
- `retrieval-cache` projector: precomputes top-K retrievals for hot intents
- `claims-harvester` projector: bridges to the bus-published claims deltas (per `docs/CLAIMS_BUS.md`)

Each runs as an independent tracked goroutine with its own lease entry. The branch projector is the first; others are additive.

---

## Ontology

### Branch Identity

Every branch has:

- `root_id`
- `branch_id`
- `parent_id`
- `intent_id`
- `family`
- `scope`
- `state`

### Scopes

Scopes are:

- `working`
- `episodic`
- `semantic`
- `contradiction`
- `dormant`

### States

Branch states are:

- `active`
- `candidate`
- `validated`
- `contradicted`
- `superseded`
- `dormant`

### Event Types

Core event types include:

- content indexed
- decision recorded
- outcome recorded
- preference recorded
- hypothesis recorded
- recall
- validation
- contradiction
- replay promotion
- replay consolidation
- ecology pruning
- ecology regrowth

## Branch Packets

Agents should retrieve `BranchPacket`s, not raw hits.

Each packet includes:

- branch identity
- tree family and scope
- title and summary
- support evidence
- counterevidence
- provenance
- confidence
- predicted utility
- scope risk
- conflicts
- suggested next actions
- scoring breakdown

Branch packets are the primary retrieval product for:

- Academic
- Librarian
- Archivalist
- Architect
- Orchestrator
- Inspector
- Tester

## Retrieval Model

Retrieval is `intent-conditioned first` and `query-conditioned second`.

The system should answer:

1. what is the active intent frontier
2. what constraints and preferences govern it
3. which branches already exist around it
4. which evidence best reduces uncertainty or advances completion
5. which next branches are likely to create value without violating scope

### Retrieval Steps

1. resolve the canopy
2. gather candidate branches across trees
3. gather supporting evidence from indexed content
4. spread activation over relays
5. score branch candidates
6. build normalized float32 feature vectors for each candidate
7. apply the learned reranker to top candidate packets
8. hydrate top candidates into branch packets
9. reinforce returned packets in the warmth layer

### Scoring Features

Candidate scoring should blend:

- query match
- evidence support
- canopy proximity
- substrate potential
- frontier score
- confidence
- recency
- ACT-R-compatible warmth
- success utility
- salience
- conflict penalty
- scope safety
- inhibition safety

This scoring path should use SIMD-friendly float32 feature vectors and concurrent fanout for candidate hydration.

### Learned Reranker

The forest should keep a two-stage ranking path:

- deterministic SIMD base scorer first
- learned reranker second

The base scorer remains the fail-open path and should continue to use `vek32` dot products over normalized float32 feature vectors.

The learned layer should be a native gradient-boosted stump ensemble with:

- SQLite-backed training example capture
- versioned active model storage
- global models and agent-specific models
- utility prediction
- risk prediction
- branch packet feature signals for explanation

This is the correct place for XGBoost-like behavior in Sylk. It should not replace:

- the forest graph
- ACT-R warmth
- replay and reconsolidation
- relay propagation
- deterministic governance checks

The learned reranker should consume features such as:

- base score
- query match
- evidence support
- canopy proximity
- confidence
- recency
- warmth
- utility and success balance
- conflict and scope safety
- support density and counter density
- relay mass
- substrate potential
- frontier score
- inhibition safety
- session affinity
- caller-agent and tree-family affinity
- scope and family one-hot features
- source-agent one-hot features

The reranker should return:

- utility probability
- risk probability
- replay-friendly salience hint
- clarification pressure
- model confidence
- salient feature signals

Final ranking should remain conservative:

- deterministic base score stays dominant
- learned utility blends in proportion to model confidence
- learned risk can only penalize, not silently override hard constraints
- the entire learned path fails open to the deterministic base scorer

## Predictive Planning

The forest should help agents produce stronger work than literal compliance while staying inside user authority.

That means:

- exceed on quality
- do not silently exceed on scope
- treat latent intent as a hypothesis with confidence

Predictive planning should generate and rank:

- strict satisfy branches
- safe surplus quality branches
- high-risk opportunity branches that require user approval

The planner should auto-prefer high-confidence, low-scope expansions and escalate when a branch crosses scope or authority boundaries.

## Agent Roles

### Academic

Academic contributes:

- external authority
- freshness-sensitive evidence
- contradiction checks against outside knowledge
- best-practice and research priors

### Librarian

Librarian contributes:

- code and repository evidence
- local implementation precedents
- touched-file and symbol context
- implementation pattern recall

### Archivalist

Archivalist contributes:

- decision history
- failures and lessons
- workflow outcomes
- reuseable historical precedent

All three write into the same forest with different provenance, trust, and family labels.

### Engineer

Engineer contributes:

- implementation precedent
- code change branches
- outcome-producing execution history
- capability priors for tool and workflow choice

Engineer should be a major producer of `evidence`, `decision`, `outcome`, and `capability` branches.

### Designer

Designer contributes:

- UX intent refinement
- visual and interaction constraints
- style and product preference priors
- design-risk and opportunity branches

Designer should be a major producer of `intent`, `constraint`, `preference`, `opportunity`, and `outcome` branches.

### Guardian

Guardian is not a normal evidence producer. Guardian contributes:

- high-authority safety and policy constraints
- conflict and scope-risk branches
- approval and veto signals
- governance relays that suppress unsafe opportunity branches

Guardian should be treated as a hard governance source in ranking and planning.

### Scribes

Scribes are sidecars, not primary deciders. Scribes contribute:

- dense episodic capture
- low-cost rationale preservation
- branch summaries
- replay-friendly observational traces

Scribes should feed the ledger and replay scheduler at high volume with lower authority than the parent decision-making agent.

## Skills

Agents should not have to manually reconstruct forest state. The core skill surface should include:

- `forest_resolve_intent`
- `forest_recall`
- `forest_predict_next_branches`
- `forest_record_outcome`
- `forest_get_constraints`
- `forest_get_conflicts`
- `forest_get_preference_prior`
- `forest_get_capability_prior`
- `forest_explain_recommendation`

The first four are the minimum universal skill surface. Additional specialist skills should be layered for Academic, Librarian, and Archivalist.

Engineer, Designer, Guardian, and Scribe-prefixed agents should also receive the universal forest skills. The forest is not only for knowledge-specialist agents; it is a framework-wide planning and recall substrate.

## Correctness Rules

- evidence is append-only
- contradiction creates sets or forks, not destructive mutation
- provenance is mandatory for every derived summary
- confidence is calibrated by outcomes, not model self-report alone
- session-local learning is isolated until replay or explicit consolidation
- dormant does not mean deleted
- relay strength does not override conflict or trust checks
- warmth is a ranking signal, not a truth signal
- the learned reranker is a utility estimator, not an authority source
- deterministic guardian and conflict constraints outrank learned optimism

## Performance Strategy

- use concurrent branch search, evidence search, and canopy resolution
- precompute and store branch summaries
- keep hot session state in SQL indexes and in-memory caches
- use SIMD float32 scoring for candidate batches
- keep active learned models cached in memory
- persist training examples in SQLite and train models in background maintenance cycles
- refresh substrate state and frontiers in background maintenance cycles
- bound branch hydration and evidence expansion
- replay and ecology run in background workers
- fail open to existing retrieval paths if forest workers are degraded

## Implementation Plan

### Phase 1

- extend content metadata and content types so evidence can carry forest identity
- add forest tables and the append-only ledger
- auto-ingest content entries into the ledger
- project initial branches and canopies

### Phase 2

- add branch packet retrieval
- add canopy resolution
- add relay reinforcement
- add substrate conductance edges, state, and frontiers
- add ACT-R-compatible branch warmth
- add SIMD batch scoring

### Phase 3

- add replay scheduler and consolidation
- add reconsolidation on recall and validation
- add ecology pruning and regrowth
- add substrate diffusion and inhibition refresh in maintenance
- expose intent-first skills to all knowledge agents

### Phase 4

- deepen agent-specialized skills for Academic, Librarian, and Archivalist
- add stronger capability and opportunity prediction
- add learned reranking from captured branch outcomes
- add agent-specific utility and risk models for engineer, designer, guardian, and scribes
- use forest signals as routing hints only after retrieval and planning are already stable

## Current Implementation Shape

The reference implementation should live in:

- `core/forest/`
  forest runtime, storage, projection, scoring, learning, substrate, replay, warmth, retrieval
- `core/context/`
  evidence metadata and observer integration
- `core/context/skills/`
  agent-facing forest skills

The implementation should reuse:

- `UniversalContentStore`
- `TieredSearcher`
- Bleve and SQLite
- `VectorGraphDB`
- `core/knowledge/memory` ACT-R types
- existing concurrency patterns and worker management style
- `vek` or `vek32` for scoring and float32 math
- SQLite model persistence rather than a separate model store

## Non-Goals

- replacing Sylk’s existing content store
- making the forest the primary router on day one
- treating predicted latent intent as automatic permission for scope changes
- collapsing all memory into one undifferentiated graph

## Summary

The Memory Forest is a predictive, multi-tree memory system layered on Sylk’s current stores. It is:

- evidence-grounded
- intent-conditioned
- multi-timescale
- cross-pollinating
- ACT-R-compatible
- skill-first for agents
- safe under contradiction
- optimized for helping agents advance user intent with higher quality and stronger foresight
- capable of learning branch utility and risk from explicit outcomes without replacing the symbolic forest
