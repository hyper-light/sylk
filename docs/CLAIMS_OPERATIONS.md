# Claims Operations

This document specifies the operational architecture of the Sylk claims
plane: how the system bootstraps, how it recovers from crashes, what
performance bounds it operates under, how cancellation propagates
through nested claim trees, how telemetry surfaces operational state,
and which invariants are enforceable at compile time or at runtime
versus which depend on developer discipline.

It complements the design-level documents (`CLAIMS.md`,
`CLAIMS_AND_DELTAS.md`, `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`,
`CLAIMS_VISIBILITY.md`, `CLAIMS_AND_INFRASTRUCTURE.md`,
`ARTIFACTS_AND_VALIDATIONS.md`) which specify the *what* and *why* of
the claims model. This document specifies the *how* of running it in
production.

## 1. Purpose

The design documents leave operational concerns underspecified. This
gap surfaces during real deployment as a class of questions the design
documents cannot answer alone:

1. How does the process come up from cold start? What's the order in
   which the identity registry, board, bus, system services, and agent
   participants initialize? What happens when one of those
   initialization claims fails?
2. What happens when a claimant orchestrator crashes mid-validation?
   What recovers the in-flight handler invocations, the pending
   continuations, the partially-bundled result testament?
3. What are the actual performance bounds? Queue capacities, delta
   throughput, WAL write latency, dispatcher concurrency, validation
   parallelism — what are the design budgets and what's the
   measurable behavior under load?
4. How does telemetry surface this state? Which counters drive
   alarms? What dashboards exist?
5. How does a user cancellation walk down a claim → consultation →
   child claim → grandchild claim tree without leaving orphan
   pending work or stuck UI rows?
6. Which invariants are enforceable by tooling (linters, runtime
   audits, static analysis) versus by developer discipline alone?

This document answers each of these.

## 2. Non-Negotiable Operational Invariants

These invariants govern operational behavior. They override any
implementation that conflicts with them and must hold under all
production conditions.

### 2.1 No Untracked Goroutines

Every goroutine spawned anywhere in the claims plane is owned by a
named `core/concurrency.GoroutineScope`. Bare `go func()` is forbidden.
Enforcement is via static lint (§9.1) and runtime scope inventory
(§9.3).

### 2.2 No Unbounded Queues

Every queue has a declared capacity derived from participant
registration metadata, claim-issuance-rate budgets, or runtime
configuration. No queue grows without bound. Overflow produces a
durable error artifact and an operational telemetry counter
increment.

### 2.3 No Silent Drops

When backpressure forces a drop, the drop is recorded as an
`ArtifactError` on the affected claim, an error artifact on the
testament, or a telemetry counter increment — at minimum one of these.
A drop that produces no observable signal is a bug.

### 2.4 Cancellation Propagates

User-initiated cancellation propagates through the claim graph from
root to leaves. Every cancelled claim transitions to an explicit
terminal state (`claim.progress_failed` with `interrupted` error
category) before its goroutine scope releases. No claim is killed
silently.

### 2.5 Replay Reconstructs Identical State

WAL replay reconstructs identical board state for the same input WAL.
Replay is deterministic: same WAL, same state, same delta sequence.
Non-deterministic handlers store their outputs as durable result
artifacts and are not re-executed on replay.

### 2.6 Bootstrap Is Idempotent

Process boot can be retried at any phase boundary. Re-running boot
after a partial failure produces the same final state as a clean cold
start.

### 2.7 Shutdown Drains Deterministically

Process shutdown drains the goroutine scope tree in a fixed order with
explicit per-scope deadlines. No goroutine outlives the process's
shutdown deadline. No in-flight commit is lost; either the commit
lands durably or the WAL records the abort.

### 2.8 Performance Bounds Are Declared, Not Hardcoded

Every queue capacity, every timeout default, every concurrency budget,
every backpressure threshold is declared in participant registration
metadata, runtime configuration, or computed via documented formulas
over declared inputs. No magic numbers anywhere in the operational
layer.

## 3. Bootstrap Sequencing

Process boot follows a strict phase ordering. Each phase is itself a
claim cycle per `docs/CLAIMS_AND_INFRASTRUCTURE.md` §14 (boot
sequencer service); this section pins down the inter-phase ordering.

### 3.1 Bootstrap Identity Allocation

The identity registry is a service participant per
`docs/CLAIMS_AND_INFRASTRUCTURE.md` §7.4. Every other participant
needs a canonical UID, which the registry allocates. The identity
registry itself needs a UID. This is the bootstrap circular
dependency.

The resolution:

1. At process start, the boot sequencer hardcodes the identity
   registry's UID as `sys:identity_registry:proc/<process_uid>` where
   `<process_uid>` is derived from process-start-time entropy. This
   UID is computed in pure Go without consulting any external state.
2. The boot sequencer instantiates the identity registry with this
   hardcoded UID and the registry begins accepting allocation claims.
3. The boot sequencer's own UID is `sys:boot_sequencer:proc/<process_uid>`
   computed similarly.
4. All other participant UIDs are allocated through the registry via
   normal claim flow.

The hardcoded bootstrap UIDs are stable for the lifetime of the
process. They rotate on each process restart (different
`<process_uid>` derived from new entropy). Cross-restart continuity
for these participants relies on their service-type identity, not on
UID equality.

### 3.2 Boot Phase Order

Boot phases execute in this strict order. Each phase is a claim
against the boot sequencer; the claim must reach `claim.satisfied`
before the next phase starts.

```text
Phase 0: Process Identity (synchronous, no claims yet)
  - Compute <process_uid> from start-time entropy.
  - Instantiate boot sequencer with hardcoded UID.
  - Instantiate identity registry with hardcoded UID.
  - Open the in-memory board (no WAL yet).

Phase 1: Durable Substrate
  - Open WAL.
  - Open Guide event bus.
  - Replay WAL into board (idempotent — see §4).
  - Boot sequencer commits boot.phase_1_complete testament.

Phase 2: System Participants
  - Activate the bus administrator service.
  - Activate the session manager service.
  - Activate the fabric subscriber service.
  - Each activation is a claim against the activation controller.
  - Boot sequencer commits boot.phase_2_complete testament.

Phase 3: Knowledge Backends
  - Activate the knowledge graph reader/writer services.
  - Activate the document DB reader/writer services.
  - Activate the memory forest service.
  - Each readiness is a claim against the respective service.
  - Boot sequencer commits boot.phase_3_complete testament.

Phase 4: Infrastructure Services
  - Activate the VFS provisioners (pipeline, tool, global).
  - Activate the DAG processor.
  - Activate the tool runtime.
  - Activate the provider gateway.
  - Activate the guardian service component.
  - Boot sequencer commits boot.phase_4_complete testament.

Phase 5: Agent Activation
  - Activate the Guide agent (always hot).
  - Activate any other agents declared "always hot" in config.
  - On-demand agents remain cold until first claim arrives.
  - Boot sequencer commits boot.phase_5_complete testament.

Phase 6: User-Facing Surfaces
  - Activate the UI bridge.
  - Subscribe the bridge to canonical delta topics.
  - Boot sequencer commits boot.phase_6_complete testament.

Phase 7: Boot Complete
  - Boot sequencer commits boot.satisfied testament summarizing all
    phase outcomes.
  - System enters normal operation.
```

### 3.3 Boot Failure Handling

If any phase claim transitions to a failure state
(`claim.validation_failed`, `claim.validation_errored`, etc.) before
`claim.satisfied`:

1. The boot sequencer commits a `boot.phase_<n>_failed` testament with
   error artifacts.
2. Subsequent phases do not start.
3. Already-started services continue to function but no new
   participants activate.
4. The process logs the failure and enters a degraded state where it
   can accept administrator inspection but not user prompts.
5. The operator may retry the failed phase explicitly via the
   administrator API; the retry posts a new corrective claim per
   `docs/ARTIFACTS_AND_VALIDATIONS.md` §10.

### 3.4 Boot Idempotency

Boot is idempotent: a retried boot phase after a partial failure
produces identical final state. This is achieved by:

- Identity allocations using deterministic UID derivation: same input
  produces same UID, so re-running a phase's identity allocations is
  a no-op.
- Service activations checking for existing activation records before
  posting new ones.
- WAL replay being a pure function of the WAL contents.
- Idempotency keys on every claim posted during boot, keyed on
  (boot_phase, sub_step) so duplicate posts are ignored.

### 3.5 Boot Performance Budget

Boot performance budgets are declared per phase. The defaults:

| Phase | Default budget | Derivation |
|---|---|---|
| 0 | 50ms | Pure-Go computation, no I/O |
| 1 | 500ms × WAL size factor | WAL replay rate × declared WAL size |
| 2 | 200ms × system service count | Service activation latency × count |
| 3 | 2000ms × backend count | Knowledge backend init × count |
| 4 | 1000ms × service count | Infrastructure service init × count |
| 5 | 5000ms × always-hot agent count | LLM provider warm-up × count |
| 6 | 200ms | Bridge subscription attachment |

Total cold-start budget for typical workload: ~10 seconds. Configurable
via the runtime configuration's `claims.boot.phase_budgets` map.

Exceeding a phase's budget does not fail the phase automatically; it
emits a `claims_boot_phase_duration_seconds_exceeded` telemetry
counter and the operator may choose to abort.

## 4. WAL and Replay

The board's WAL is the durable source of truth. Replay reconstructs
board state on restart and during audit sessions.

### 4.1 WAL Write Discipline

Every state transition commits to the WAL before the corresponding
delta emits to the Guide event bus. The ordering is:

1. Lock-free preparation of the state transition (compute idempotency
   key, validate transition legality).
2. WAL append (`fsync` to the appropriate persistence tier per
   `docs/CLAIMS.md` §4.10 mutability summary).
3. In-memory board state update (atomic with respect to readers).
4. Delta envelope construction.
5. Delta publication to the Guide event bus.

If any step fails:

- WAL append failure: the transition is aborted; the in-memory state
  is unchanged; the caller receives a typed error and may retry.
- In-memory update failure (e.g., consistency violation): the WAL
  entry is marked as aborted on the next WAL append batch; the
  in-memory state is unchanged.
- Delta publication failure: the WAL entry remains valid; the
  publication retries via the bus durability layer. WAL-side replay
  on a restart will re-emit any deltas that were not published.

### 4.2 Replay Determinism

Replay is a pure function of the WAL:

```text
replay(wal_contents) → board_state
```

Same WAL contents always produce the same board state. The replay
reducer:

1. Reads WAL entries in commit-sequence order.
2. For each entry, applies the corresponding state transition to the
   in-memory board.
3. Skips entries marked as aborted.
4. Reconstructs every claim, testament, artifact, validation, and
   their status histories.

Replay does NOT re-execute validator handlers, agent tool loops, or
service handlers. The committed results are loaded as-is from the
WAL.

### 4.3 Replay Performance

Replay throughput is bounded by WAL read rate plus in-memory state
update rate. The design budget:

```text
replay_throughput = min(disk_read_rate, in_memory_update_rate)
                  ≈ 100K entries/second (typical SSD + simple state)
                  ≈ 50K entries/second (worst case)
```

For a session with 100K board state transitions, replay completes in
1-2 seconds.

Operators set the upper-bound WAL size limit; exceeding it triggers
WAL compaction (separate process, doesn't block runtime).

### 4.4 WAL Compaction

WAL compaction snapshots the in-memory board state at a sequence
boundary and writes a checkpoint file that the next replay can use as
a starting point instead of reading from sequence 0.

Compaction triggers:

1. WAL size exceeds the configured threshold (default: 100MB,
   derivable from disk budget × retention window).
2. Session count exceeds the configured threshold (default: 1000
   sessions).
3. Operator-initiated.

Compaction does not pause the board. It runs in a background scope
with bounded resource use.

### 4.5 Replay Audit Mode

Replay audit is a special mode that re-executes pure and content-
deterministic validators on replay and asserts identical outputs.
Used for:

- Detecting validator nondeterminism that was declared as pure or
  content.
- Detecting validator bug fixes that would change historical verdicts.
- Compliance audits.

Replay audit:

1. Loads the WAL up to the configured audit boundary.
2. Reconstructs board state.
3. For each historical validation with determinism level `pure` or
   `content`, re-invokes the validator handler with the original
   inputs.
4. Compares the re-execution result to the stored result.
5. Records divergences as `validator_nondeterministic` error artifacts
   on a synthetic audit testament.

Replay audit does not modify the original WAL; the divergence
artifacts go into a separate audit board.

## 5. Crash Recovery

A process crash leaves the WAL durable but the in-memory state, the
goroutine scope tree, in-flight handler invocations, pending
continuations, and partially-bundled result testaments are all lost.
Recovery reconstructs everything reconstructible.

### 5.1 Recovery Sequence

On restart after a crash:

1. **Boot phases 0-2** run normally (identity, substrate, system
   services).
2. **WAL replay** reconstructs board state (per §4.2).
3. **Pending continuation reconstruction**: the continuation store
   reads its durable backing and re-registers every continuation
   that was pending at crash time. Continuations whose preconditions
   are already satisfied resume immediately; others wait for new
   deltas.
4. **Orphan validation reconciliation**: the claimant orchestrator
   inspects every claim in a non-terminal state. For each:
   - If the claim is at `claim.validating` and has validations at
     `validation.validating`, re-dispatch those validations from
     scratch. The dispatcher's idempotency key prevents
     double-execution where handlers are pure or content-deterministic.
   - If the claim is at `claim.testament_acknowledged` and has not
     yet committed `claim.validating`, the orchestrator continues
     from that boundary.
5. **In-flight artifact reconciliation**: artifacts at
   `artifact.validating` are re-dispatched following the same logic
   as orphan validations.
6. **Result testament reconstruction**: partially-bundled result
   testaments at crash time are re-bundled from the durable result
   artifacts on the board. If some result artifacts were committed
   but the testament was not yet posted, the orchestrator
   re-generates the testament containing the committed results.
7. **Subscription re-attachment**: all subscribers (UI bridge,
   continuation workers, validators) re-attach to their canonical
   topic patterns.

### 5.2 Recovery Determinism

Recovery produces identical post-recovery board state regardless of
when the crash occurred, provided:

- The WAL was synced before crash (the `fsync` on every state
  transition).
- The continuation store was synced before crash.
- The validator handlers are pure or content-deterministic (otherwise
  re-execution may produce different results).

For nondeterministic handlers, recovery uses the stored result rather
than re-executing.

### 5.3 Recovery Performance

Recovery time is bounded by:

```text
recovery_time = boot_phases_0_to_2 + wal_replay + continuation_reconstruction + orphan_reconciliation
```

For a session with 100K board transitions, 1000 pending continuations,
and 50 orphan validations:

- Boot phases 0-2: ~750ms
- WAL replay: 1-2 seconds
- Continuation reconstruction: 500ms (bounded by continuation count)
- Orphan reconciliation: 1-3 seconds (bounded by validation count
  and re-dispatch parallelism)

Total: ~4-7 seconds typical. Configurable budgets and parallelism per
operator.

### 5.4 Recovery Failure

If recovery itself fails (corrupt WAL, missing continuation store,
identity registry rejection of recovered UIDs), the process enters a
degraded state and surfaces a `recovery_failed` system testament.
Operator intervention is required.

The recovery process never silently discards state. If a piece of
state cannot be recovered, an error artifact is committed describing
exactly what was lost and why.

## 6. Cancellation Propagation

User cancellation (Esc keypress, explicit cancel command, session
shutdown) propagates through the claim graph. This section pins down
the propagation mechanics.

### 6.1 Cancellation Sources

The cancellation sources:

| Source | Trigger | Scope |
|---|---|---|
| User Esc (single) | UI interrupt key | Currently active root claim |
| User Esc (long-press) | UI extended interrupt | All active root claims in session |
| Session shutdown | Session close | All active claims in session |
| Process shutdown | SIGTERM, etc. | Everything |
| Deadline expiration | Claim or validation deadline reached | Affected claim subtree |

Each source posts a cancellation request that triggers the same
propagation mechanism.

### 6.2 Propagation Mechanism

Cancellation propagates via corrective claims with `caused_by`
relations:

1. The cancellation source posts a top-level cancellation claim
   targeting the orchestrator participant responsible for the
   affected root claim.
2. The orchestrator commits `claim.progress_failed` with `interrupted`
   error category on the affected claim and on its testament-in-progress.
3. The orchestrator traverses the claim's relation graph (via the
   `caused_by` field on child claims) and recursively commits
   `claim.progress_failed` on every descendant claim.
4. For each cancelled claim with in-flight goroutines (handler
   executions, validator dispatches), the orchestrator cancels the
   goroutine context. The handler's deferred cleanup commits any
   error artifacts and releases the scope.
5. Pending continuations waiting on cancelled claims commit
   `claim.progress_failed` with `interrupted` category before
   releasing.

### 6.3 Propagation Ordering

Cancellation propagates from root to leaves. The orchestrator's
traversal is:

```text
1. Identify the root claim of the cancelled work (the topmost claim
   the user is interacting with).
2. BFS the relation graph from root, finding all descendants linked
   via caused_by relations.
3. Cancel claims in reverse BFS order (leaves first, root last).
4. Wait for all descendants to reach a terminal state before
   committing the root's cancellation.
```

This order ensures no leaf claim is orphaned. If the root were
cancelled first, descendants might continue and produce committed
results that no consumer exists to receive.

### 6.4 Cancellation Latency

Cancellation completes within a bounded latency derived from:

```text
cancellation_latency = max(handler_deadline, validation_deadline) + propagation_overhead
```

For typical agentic claims (handlers up to 30s, validations up to
30s), cancellation completes within ~60-90 seconds. For service-only
claims (handlers in milliseconds), cancellation completes in
sub-second.

The cancellation deadline is itself a bound; if cancellation cannot
complete within the deadline, the process logs a stuck-cancellation
event and the operator may force-kill the orchestrator scope.

### 6.5 Cancellation Audit Trail

Every cancellation produces durable audit-trail evidence:

- The originating cancellation request is a claim with an
  `error_diagnostic` artifact describing the trigger.
- Every cancelled descendant claim has its status history updated
  with an `interrupted` reason citing the originating cancellation
  claim's ID.
- The originating cancellation claim's eventual testament summarizes
  the cancelled subtree (count of cancelled claims, list of affected
  participants).

This audit trail makes "what was cancelled when and why" a board
query, not a log-scraping exercise.

## 7. Performance Bounds

This section pins down the design bounds for the claims plane's
runtime performance. Bounds are derived from declared inputs, not
hardcoded magic numbers.

### 7.1 Delta Throughput

The Guide event bus must sustain:

```text
peak_delta_rate = sum over claims (states_per_claim) / claim_duration
```

For a session with 100 active claims each emitting 20 deltas over
60 seconds: peak = 100 × 20 / 60 ≈ 33 deltas/second per session.
For 100 concurrent sessions: 3300 deltas/second process-wide.

The Guide bus default capacity sustains 10K deltas/second per
process. Operators may scale via per-topic subscription pools.

### 7.2 Board Mutation Throughput

The board must sustain mutation throughput equal to delta throughput:
every delta corresponds to one state transition.

WAL write rate (SSD with `fsync`):

```text
wal_write_rate ≈ 1K-10K writes/second per session
              (bounded by disk fsync latency, not raw bandwidth)
```

For 3300 deltas/second across 100 sessions: 33 writes/second per
session — well within budget. Burst capacity is higher; the design
sustains 10K writes/second/session for short bursts.

### 7.3 Validator Dispatch Throughput

The validator dispatcher capacity per process:

```text
dispatcher_capacity = sum over registered validators (declared_concurrency)
```

If a process registers 20 validators each declaring concurrency 10:
dispatcher capacity 200 concurrent invocations. Validations exceeding
this trigger backpressure per `docs/ARTIFACTS_AND_VALIDATIONS.md` §13.

### 7.4 Continuation Pool Capacity

Continuation worker pool capacity:

```text
continuation_pool_size = expected_concurrent_pending_continuations
                       × handler_p99_duration
                       / continuation_check_interval
```

Defaults: pool size 100, check interval 500ms. Sustains 100 concurrent
long-running consultations.

### 7.5 Memory Bounds

Process memory bounds per session:

| Component | Per-session bound | Eviction |
|---|---|---|
| Board in-memory state | ~10MB × claim count factor | LRU on session close |
| WAL buffer | 16MB | Flush on full |
| Delta dedup store | 1MB × subscriber count | TTL after 5 minutes |
| Continuation store buffer | 4MB | Flush on full |
| Type registry | ~1MB process-wide | None (read-mostly) |
| Validator registry | ~2MB process-wide | None (read-mostly) |
| UI bridge state | ~5MB × active sessions | On session close |

For 100 active sessions: ~1.5GB process memory budget. Operators may
adjust via configuration.

### 7.6 Latency Targets

| Operation | Target latency | Bound |
|---|---|---|
| Claim post | <10ms | WAL fsync |
| Artifact generation | <20ms | WAL fsync + payload size |
| Testament generation + attachment | <50ms | WAL fsync × N artifacts |
| Programmatic validator dispatch | <100ms | Handler execution |
| Validator quality-bar phase | <60s | LLM turn |
| Delta publication | <5ms | Bus capacity |
| WAL replay (per 1K entries) | <50ms | Disk read |
| Recovery (typical session) | <10s | Bounded by §5.3 |

These are design targets, not contractual guarantees. Telemetry counts
violations.

### 7.7 Backpressure Thresholds

Backpressure thresholds are declared per queue:

```text
queue_overflow_threshold = configured_capacity × 0.9
```

At 90% capacity, the queue emits a `claims_<queue>_near_capacity`
telemetry counter for early warning. At 100%, overflow produces error
artifacts per §2.3.

## 8. Telemetry and Observability

The claims plane emits a structured set of counters, gauges, and
events for production observability.

### 8.1 Counter Catalog

```text
# Board mutations
claims_board_transitions_total{entity_type, action}
claims_board_transition_duration_seconds{entity_type, action}
claims_board_wal_write_total{result}
claims_board_wal_write_duration_seconds
claims_board_wal_replay_entries_total
claims_board_wal_replay_duration_seconds

# Delta emission
claims_delta_emitted_total{action, participant_category}
claims_delta_emission_duration_seconds
claims_delta_publish_failure_total{reason}
claims_delta_subscriber_overflow_total{topic}

# Claim lifecycle
claims_claim_generated_total{action_type, issuer_category}
claims_claim_posted_total{action_type, target_category}
claims_claim_received_total{target_category}
claims_claim_satisfied_total{action_type}
claims_claim_validation_failed_total{action_type, error_category}
claims_claim_validation_incomplete_total{action_type}
claims_claim_validation_errored_total{action_type, error_category}
claims_claim_duration_seconds{action_type, outcome}

# Artifact lifecycle
claims_artifact_generated_total{participant_category, artifact_kind}
claims_artifact_received_total{claimant_category}
claims_artifact_attached_total{participant_category}
claims_artifact_validated_total{claimant_category}
claims_artifact_validation_failed_total{claimant_category, error_category}
claims_artifact_receipt_failed_total{claimant_category, error_category}

# Validation lifecycle
claims_validation_dispatched_total{validator_id}
claims_validation_validated_total{validator_id}
claims_validation_failed_total{validator_id, required, error_category}
claims_validation_errored_total{validator_id, required, error_category}
claims_validation_quality_bar_started_total{validator_id}
claims_validation_quality_bar_validated_total{validator_id}
claims_validation_quality_bar_failed_total{validator_id, required}
claims_validation_handler_duration_seconds{validator_id, determinism}
claims_validation_handler_timeout_total{validator_id}
claims_validation_handler_panic_total{validator_id}

# Dispatcher
claims_dispatcher_queue_depth{queue}
claims_dispatcher_queue_near_capacity_total{queue}
claims_dispatcher_queue_overflow_total{queue}
claims_dispatcher_handler_invocations_total{participant_type, outcome}

# Continuation
claims_continuation_registered_total{handler_type}
claims_continuation_resumed_total{handler_type, outcome}
claims_continuation_pending_count{handler_type}
claims_continuation_timeout_total{handler_type}

# Cancellation
claims_cancellation_initiated_total{source}
claims_cancellation_propagated_claims_total{source}
claims_cancellation_completion_duration_seconds{source}
claims_cancellation_stuck_total{source}

# Recovery
claims_recovery_initiated_total
claims_recovery_completion_duration_seconds
claims_recovery_orphan_validations_total
claims_recovery_orphan_continuations_total
claims_recovery_failed_total{reason}

# Boot
claims_boot_phase_completed_total{phase, outcome}
claims_boot_phase_duration_seconds{phase}
claims_boot_phase_duration_seconds_exceeded{phase}

# Memory and resource
claims_session_count
claims_active_claim_count{participant_category}
claims_pending_continuation_count
claims_in_flight_handler_count{participant_type}
claims_goroutine_count{scope_type}
```

### 8.2 Alarm Threshold Declaration

Alarm thresholds are declared per participant in registration metadata
and per system in runtime configuration. Defaults:

| Counter | Threshold | Severity |
|---|---|---|
| `claims_board_wal_write_failure_total` rate >0.1/sec | Page | Critical |
| `claims_delta_publish_failure_total` rate >1/sec | Page | High |
| `claims_dispatcher_queue_overflow_total` rate >0.1/sec | Page | High |
| `claims_validation_handler_panic_total` rate >0.01/sec | Page | High |
| `claims_cancellation_stuck_total` rate >0/sec | Page | Critical |
| `claims_recovery_failed_total` rate >0/sec | Page | Critical |
| `claims_dispatcher_queue_near_capacity_total` rate >5/sec | Warn | Medium |
| `claims_validation_handler_timeout_total` rate >1/sec | Warn | Medium |
| `claims_claim_validation_errored_total` rate >5/sec | Warn | Medium |

Operators override per environment. The defaults are sized for typical
small-to-medium production deployments.

### 8.3 Trace Correlation

Every delta carries the trace context per the existing `core/activity`
trace propagation. Operators can correlate deltas across the system
by trace ID, claim ID, or session ID. Dashboards link telemetry to
specific delta sequences for incident investigation.

### 8.4 Dashboard Requirements

Production deployments require at minimum:

1. **Session overview dashboard**: per-session active claim count,
   delta rate, error rate, oldest in-flight claim.
2. **Per-participant dashboard**: per-participant type validator
   dispatch rate, handler latency p99, failure rate.
3. **Queue health dashboard**: per-queue depth, near-capacity events,
   overflow events.
4. **Recovery dashboard**: recovery initiation rate, duration, success
   rate, orphan counts.
5. **Cancellation dashboard**: cancellation initiation rate per
   source, propagation duration, stuck cancellations.

Dashboards are operator-provisioned; this document specifies the
required panels but not the dashboard implementation.

## 9. Enforceable Invariants

Many invariants in the claims plane depend on developer discipline.
This section catalogs which invariants are enforceable by tooling
versus which depend on convention, and specifies the tooling for
enforceable ones.

### 9.1 Static Lints

Implemented as `go vet` plugins or custom `analysis.Analyzer`
instances in `tools/claimslint/`:

| Lint | Catches |
|---|---|
| `no-bare-go` | Bare `go func()` outside `core/concurrency.GoroutineScope.Go` |
| `no-magic-queue-capacity` | Unboxed integer literals passed to queue constructors |
| `no-magic-timeout` | Unboxed `time.Duration` literals as timeout values |
| `participantref-not-agentref` | New code constructing `AgentRef` instead of `ParticipantRef` |
| `validator-must-declare-determinism` | `RegisterValidator` without explicit determinism level |
| `artifact-must-declare-datatype` | Artifact construction with empty `DataType` |
| `validation-must-have-target-artifact-name` | Validation construction with empty `TargetArtifactName` |
| `quality-bar-requires-agentic-claimant` | Validation declaration with non-empty `QualityBar` paired with non-agent claimant |
| `no-direct-error-return-for-infra-outcome` | Subsystem-level functions that mutate board state returning Go errors instead of producing artifacts |
| `no-go-error-from-validator-handler` | Validator handlers returning errors that don't pair with structured `ArtifactError` |

The lints run in CI on every PR. Violations block merge unless
explicitly waived with a comment-attached justification.

### 9.2 Runtime Audits

Implemented as periodic background scans:

| Audit | Detects |
|---|---|
| Goroutine inventory | Goroutines outside registered scopes |
| Queue capacity audit | Queues constructed without declared capacity |
| Validator determinism audit | Pure/content validators producing divergent outputs on replay |
| Receipt timing audit | Artifacts at `attached` without prior `received` AND with claimant having had ample observation time |
| Cancellation stuck audit | Cancellations not completing within their declared deadline |
| WAL integrity audit | WAL entries failing checksum verification |
| Boot replay audit | Boot phases failing to produce identical state across runs |

Audits run every 30 seconds (configurable) and emit telemetry
counters on detection.

### 9.3 Convention-Only Invariants

Invariants the tooling cannot enforce; depend on developer discipline:

| Invariant | Discipline |
|---|---|
| Validator handlers are truly deterministic when declared pure | Code review + replay-audit detection (lag indicator) |
| Handler functions are non-blocking observers | Code review |
| Result artifacts are ephemeral when not user-visible | Code review |
| Quality bar text is testable and unambiguous | Code review |
| Claim descriptions are atomic and specific | Code review + LLM-assisted review |
| Validation `TargetArtifactName` matches an artifact the testifier will actually produce | Code review + integration tests |
| Service identity scope keys are stable across restarts | Code review |
| Participant categories are correctly declared | Code review |

For these, the codebase relies on review discipline. The lints and
audits catch most concrete misuses; convention catches the rest.

### 9.4 Lint Suppression Discipline

Lint violations may be suppressed only with a comment attached at the
suppression site explaining the justification. Suppressions are
recorded in a central registry (`docs/lint-suppressions.md`) for
audit. PR review explicitly checks suppression justifications.

Bulk suppressions or category-wide disables are prohibited. Each
suppression is per-file, per-line.

## 10. Production Deployment Checklist

Before deploying a new participant or migrating a subsystem to the
claims plane, the following checklist must be satisfied:

### 10.1 Per-Participant Checklist

- [ ] Canonical UID derivation specified per `docs/CLAIMS_AND_INFRASTRUCTURE.md` §7.1
- [ ] Participant category declared (`agent`, `service`, `system`, `external`)
- [ ] Handler registration includes determinism level
- [ ] Handler bounded queue capacity declared
- [ ] Handler default timeout declared
- [ ] Telemetry counters added to the catalog
- [ ] Goroutine ownership scope identified
- [ ] Shutdown drain behavior specified
- [ ] Crash recovery behavior tested
- [ ] WAL replay re-execution behavior tested (for pure/content handlers)
- [ ] Lint suppression review (zero suppressions for production code)
- [ ] Integration test exercising the full lifecycle
- [ ] E2E test exercising failure paths
- [ ] Documentation cross-references updated

### 10.2 Per-Validator Checklist

- [ ] Typed handler signature declared
- [ ] `ArtifactDataType` registered with type registry
- [ ] `ResultDataType` registered with type registry
- [ ] Determinism level declared
- [ ] Expected concurrent invocations declared
- [ ] Timeout default declared (or explicit 0 for no timeout)
- [ ] Unit tests cover pass/fail/incomplete/errored paths
- [ ] Replay-audit-safe (pure/content handlers must round-trip)
- [ ] Quality bar text (if applicable) reviewed by domain expert

### 10.3 Per-Claim-Action Checklist

- [ ] Claim action type added to the action type enum (if new)
- [ ] Claim posting site includes idempotency key
- [ ] Validations declared with required/optional flags
- [ ] Quality bars (if any) reviewed for agentic-only use
- [ ] Remediation path specified per `docs/ARTIFACTS_AND_VALIDATIONS.md` §10
- [ ] Documentation updated

## 11. Operator Runbook

### 11.1 Process Won't Boot

Symptom: `claims_boot_phase_completed_total{outcome="failed"}` counter increments.

Investigation:

1. Inspect the failed phase's testament for error artifacts.
2. Check the bootstrap UID derivation (rare; entropy source failure).
3. Check the WAL — is it corrupt? Check `claims_board_wal_replay_failure_total`.
4. Check the dependency services — is the substrate accessible?

Recovery:

1. Fix the underlying issue.
2. Restart the process — boot is idempotent.
3. If boot still fails after the underlying fix, restore from
   checkpoint and replay forward.

### 11.2 Cancellations Get Stuck

Symptom: `claims_cancellation_stuck_total` counter increments.

Investigation:

1. Identify the stuck cancellation's root claim ID.
2. Inspect the claim's descendants — which one is not transitioning?
3. Check whether the descendant's handler is honoring context
   cancellation. Handlers that block on uncancellable operations
   (CGO calls, certain provider APIs) cause this.

Recovery:

1. If the stuck handler is non-cancellable: operator may force-kill
   the goroutine scope. This produces error artifacts on the
   descendant claim.
2. The parent cancellation completes after the descendant transitions
   to a terminal state.
3. File a fix to make the offending handler cancellable.

### 11.3 Validator Backpressure

Symptom: `claims_dispatcher_queue_overflow_total` counter increments
for a specific validator.

Investigation:

1. Check the validator's declared concurrency. Is it too low for the
   workload?
2. Check the handler's p99 latency. Is it spiking?
3. Check the handler's external dependencies.

Recovery:

1. Increase the validator's declared concurrency (requires restart
   to take effect because registration is at boot).
2. If latency is the cause, fix the handler or scale its dependency.
3. Short-term: operators may temporarily disable the validator,
   forcing affected claims into `validation.errored`. Operators must
   then post corrective claims to resume work once the validator is
   back.

### 11.4 Recovery After Crash

Symptom: process restart after `SIGKILL` or panic.

Investigation:

1. Check `claims_recovery_initiated_total` increment.
2. Monitor `claims_recovery_completion_duration_seconds` against the
   budget from §5.3.
3. Check `claims_recovery_orphan_validations_total` and
   `claims_recovery_orphan_continuations_total` for the recovery's
   workload size.

Recovery:

- Normal recovery completes within budget; no action needed.
- Recovery exceeding budget: investigate WAL size, continuation count,
  validator re-dispatch parallelism. Consider WAL compaction.
- Recovery failure (`claims_recovery_failed_total`): inspect the
  failure artifact, apply manual remediation, restart.

### 11.5 Delta Subscription Overflow

Symptom: `claims_delta_subscriber_overflow_total` counter increments
for a specific subscriber.

Investigation:

1. Identify the subscriber (UI bridge, validator dispatcher,
   continuation worker).
2. Check the subscriber's processing latency.
3. Check the topic's delta rate.

Recovery:

1. Scale the subscriber's processing concurrency.
2. If the topic is unusually busy (burst of activity), the bus's
   durability layer ensures no deltas are lost — the subscriber
   catches up via replay.
3. If the subscriber is fundamentally too slow, redesign or shard.

## 12. Final Operational Statement

The claims plane's operational robustness depends on three things:

1. **The design contracts being honored**: the invariants in §2 are
   non-negotiable. Code that violates them is broken regardless of
   whether it passes tests.
2. **The tooling catching violations**: the lints and audits in §9
   detect most concrete misuses. They run in CI and at runtime to
   provide continuous enforcement.
3. **The runbooks being usable**: the operational guidance in §11
   gives operators concrete steps for incident response. The
   telemetry catalog in §8 surfaces the signals the runbooks rely on.

Together, the design documents, the implementation, the tooling, and
this operations doc form a complete production-readiness picture. The
claims plane is a coherent architectural commitment with the
operational discipline to back it up. Where one of the four layers is
missing or incomplete, the system's robustness degrades to the weakest
layer.
