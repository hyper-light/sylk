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

## 12. Phased Implementation Plan

This section is the executable implementation plan for turning the
operational contract above into production code. It is intentionally
phased so that each checkpoint produces a shippable improvement with a
clear rollback point, deterministic acceptance criteria, and tests that
exercise success, failure, edge, concurrency, deadlock, and simulated
usage paths.

The operational state vocabulary is shared with
`docs/ARTIFACTS_AND_VALIDATIONS.md`: `artifact.generated`,
`artifact.generation_failed`, `artifact.received`,
`artifact.receipt_failed`, `artifact.attached`,
`artifact.validating`, `artifact.validation_failed`,
`artifact.validated`, `validation.ready`, `validation.validating`,
`validation.validation_failed`,
`validation.validation_failed_not_required`, `validation.errored`,
`validation.errored_not_required`, `validation.validating_quality_bar`,
`validation.quality_bar_validation_failed`,
`validation.quality_bar_validation_failed_not_required`, and
`validation.validated`. Legacy validation statuses `pending`,
`in_progress`, `passed`, `incomplete`, `failed`, `errored`, and
`skipped` are compatibility projections during replay and rollback.

Plan-wide rules:

1. Every integration or e2e mock is generated with
   `github.com/vektra/mockery`; extend `.mockery.yaml` rather than
   introducing another mock generator. Unit tests may use local
   table-driven fakes only when the test stays inside one package and
   does not cross a package boundary.
2. Every production goroutine introduced by these phases is launched
   through `core/concurrency.GoroutineScope.Go` or an interface with the
   same signature, such as `core/claims.ScopeProvider` or
   `agents/shared.GoroutineScopeProxy`.
3. Every capacity, timeout, retry budget, batch limit, and deadline is
   sourced from registration metadata or runtime configuration. Tests
   may use named constants in test files, but implementation code must
   not hide operational values as anonymous literals.
4. Every durable mutation uses the existing WAL/outbox architecture in
   `core/claims/board_durable.go` and `core/claims/outbox.go`. SQLite
   remains relational-only; no SQLite extension is part of this plan.
5. Every phase must preserve compatibility with current canonical
   deltas in `core/claims/canonical_delta.go`, legacy-tolerant delta
   decoding in `core/claims/deltas.go`, and claims intake in
   `agents/shared/claims_intake.go`.

### 12.1 Phase 0 - Baseline Contract Inventory and Mock Harness

Goal: lock down the current operational surface before adding new
runtime behavior. This phase prevents later work from drifting away
from the existing board, WAL, inbox, boot, continuation, and UI bridge
APIs.

#### 12.1.1 Inventory the current claims operations surface

Description: Create a source-backed inventory that maps each
operational requirement in this document to the current implementation
surface or to a named gap. The inventory must distinguish existing
behavior from planned work. For example, durable board replay already
exists through `OpenDurableBoard` and `replayWAL`; boot claims exist for
phase 1 and phase 2 in `core/boot/operations.go`; later boot phases,
programmatic service dispatch, and cancellation tree propagation remain
gaps. This inventory becomes the checklist used by all later phases.

File references:

- `docs/CLAIMS_OPERATIONS.md`
- `docs/CLAIMS_AND_INFRASTRUCTURE.md`
- `docs/CLAIMS_AND_DELTAS.md`
- `docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`
- `core/claims/types.go`
- `core/claims/board.go`
- `core/claims/board_lifecycle.go`
- `core/claims/board_durable.go`
- `core/claims/canonical_delta.go`
- `core/claims/inbox.go`
- `agents/shared/claims_intake.go`
- `agents/shared/consult_continuations.go`
- `core/boot/operations.go`
- `ui/bridge/claims.go`

Existing APIs and integration points:

- `claims.ClaimsBoardConfig`, including `Scope`, `DeltaBus`,
  `AgentRefResolver`, `ClaimPostPolicy`, `Projectors`,
  `DisableOutbox`, `LegacySessionNoWAL`, and `Rollout`.
- `claims.OpenDurableBoard`, `DurableBoard.Board`,
  `DurableBoard.SaveSnapshot`, `DurableBoard.DrainOutbox`, and
  `DurableBoard.ProjectionHealth`.
- Board lifecycle APIs:
  `GenerateClaimAction`, `GenerateClaim`, `PostGeneratedClaim`,
  `PostGeneratedClaims`, `AcknowledgeClaimReceipt`,
  `GenerateTestamentAction`, `PostGeneratedTestament`,
  `AcknowledgeTestamentReceipt`, `BeginTestamentValidation`,
  `CompleteTestamentValidation`, `CompleteTestamentValidationError`,
  `EvaluateValidation`, and `RecordClaimLifecycleFailure`.
- Delta APIs: `claims.CanonicalDelta`, `claims.DeltaAction`,
  `claims.DeltaPublisher`, `claims.DeltaSubscriber`,
  `claims.ClaimsProjector`, `claims.InboxPatternsFor`, and
  `claims.NewClaimsInbox`.
- Intake and continuation APIs:
  `shared.WireClaimsIntake`, `shared.ClaimsIntakeConfig`,
  `shared.ContinuationStore`, `RecoverPendingContinuations`,
  `DeliverClaimResult`, `CancelContinuation`, and `Stop`.
- UI observer integration:
  `ClaimsBridge.startClaimsIntake`,
  `ClaimsBridge.processClaimsEntry`, and
  `ClaimsBridge.processBoardMutationDelta`.

Acceptance criteria:

- A traceability table exists in this document or a follow-up design
  note that maps each invariant in §2, §3, §4, §5, §6, §7, §8, and §9 to
  "implemented", "partially implemented", or "planned".
- Every "planned" entry names the package and API boundary where the
  work will land.
- The table explicitly marks boot phases 3-7, service handler dispatch,
  cancellation graph traversal, validator dispatch, recovery audits,
  and telemetry exporters as incomplete unless implementation has
  actually landed.
- The inventory includes no new storage technology and does not propose
  any SQLite extension.

Test cases:

- Unit: add table-driven tests next to the inventory helper, if a
  helper is introduced, that verify every known lifecycle action from
  `claims.KnownDeltaActions()` and every validation type from
  `claims.KnownValidationTypes()` has an inventory row.
- Unit negative path: verify the inventory test fails when a synthetic
  action or validation type is omitted.
- Unit edge case: verify legacy-only delta kinds from `deltas.go`
  remain explicitly listed as compatibility surfaces, not as new
  primary behavior.
- Integration with mockery: generate mocks for `claims.DeltaPublisher`,
  `claims.DeltaSubscriber`, `claims.ClaimsProjector`,
  `claims.AgentRefResolver`, `claims.ClaimPostPolicy`, and
  `claims.ScopeProvider`; assert the inventory's named integration
  points compile against generated mocks.
- E2E with mockery: wire a durable board, mocked delta bus, mocked
  projector, mocked identity resolver, and UI observer intake; simulate
  a claim lifecycle from generation through validation and assert every
  inventory surface receives the expected call.
- Race and deadlock: run the inventory integration test under
  `go test -race` while repeatedly creating and closing inboxes; the
  generated mock expectations must not require call order where the
  production API explicitly allows concurrency.
- Simulated usage: execute a boot phase 1 and phase 2 sequence through
  `boot.OperationsSequencer`, then query `ProjectionHealth` and the UI
  observer path to prove the documented surfaces are connected.

#### 12.1.2 Establish mockery-first test boundaries

Description: Extend the mock generation configuration so every
cross-package integration/e2e test can mock only interfaces, never
concrete claims board internals. Where a required seam does not exist,
introduce the narrowest possible interface at the package boundary
instead of mocking global state or private methods.

File references:

- `.mockery.yaml`
- `core/claims/bus_publisher.go`
- `core/claims/projectors.go`
- `core/claims/types.go`
- `core/claims/canonical_delta.go`
- `core/claims/board_lifecycle.go`
- `agents/shared/consult_continuations.go`
- `agents/shared/claims_intake.go`
- `ui/bridge/bridge.go`

Existing APIs and integration points:

- Existing interfaces already suitable for mockery:
  `DeltaPublisher`, `DeltaSubscriber`, `DeltaSubscription`,
  `DroppedCounter`, `DeltaBus`, `ClaimsProjector`, `ScopeProvider`,
  `AgentRefResolver`, `ClaimPostPolicy`, `ExpectedToolExecutor`,
  `ExpectedToolPolicy`, `ExpectedToolArgumentRedactor`,
  `ValidationExpectedToolRemediationPoster`, and
  `agents/shared.GoroutineScopeProxy`.
- UI bridge integration can mock `ui/bridge.TeaProgram` and
  `ui/bridge.Bridge` when e2e tests need to assert emitted messages
  without running a real Bubble Tea program.

Acceptance criteria:

- `.mockery.yaml` contains package entries for every interface used by
  integration/e2e tests in these phases.
- Generated mocks live under package-local `mocks` directories such as
  `core/claims/mocks`, `agents/shared/mocks`, or `ui/bridge/mocks`.
- No test imports a mock generated by another package's internal test
  directory.
- No integration/e2e test hand-writes a cross-package mock where a
  mockery-generated interface mock can be used.
- Mock generation is documented with the exact `mockery --config
  .mockery.yaml` command in the test package README or test helper
  comment.

Test cases:

- Unit: verify generated mocks compile with `go test` for packages that
  own the interfaces.
- Unit negative path: add a compile-time assertion test that fails if a
  generated mock no longer satisfies its source interface after an API
  change.
- Unit edge case: verify nil-safe interfaces such as `NoopDeltaBus` and
  nil projectors remain usable without mocks.
- Integration with mockery: use `MockDeltaBus` to subscribe an inbox,
  publish canonical deltas, return deterministic unsubscribe errors,
  and assert `ClaimsInbox.Close` reports the first error without leaking
  subscriptions.
- E2E with mockery: use mocked bus, scope, projector, and UI program to
  run a full claim-to-render path without a real LLM provider or a real
  terminal UI.
- Race and deadlock: configure mocks to block on a controllable channel
  and assert scope cancellation releases the blocked path before the
  test deadline.
- Simulated usage: run the same e2e with shuffled delivery order to
  prove mocks do not accidentally encode a stronger ordering contract
  than `DeltaSequence` and `DeltaKey`.

#### 12.1.3 Add baseline regression gates

Description: Establish a baseline test command set for claims
operations so every later phase can prove it did not regress current
behavior. This baseline must cover durable board replay, canonical
delta validation, inbox filtering, boot operation idempotency,
continuation recovery, and UI observer intake.

File references:

- `core/claims/board_durable_test.go`
- `core/claims/lifecycle_test.go`
- `core/claims/canonical_delta_test.go`
- `core/claims/inbox_test.go`
- `core/claims/outbox_projectors_test.go`
- `core/boot/operations_test.go`
- `agents/shared/claims_intake_test.go`
- `agents/shared/consult_continuations_test.go`
- `ui/bridge/claims_test.go` or a new UI bridge integration test file
- `cmd/sylk-lint/main.go`
- `cmd/nogo/main.go`

Existing APIs and integration points:

- `OpenDurableBoard` and WAL replay.
- `ValidateCanonicalDeltaStrict` and
  `ValidateCanonicalDeltaTolerant`.
- `ClaimsInbox.Start`, `ClaimsInbox.Expect`, `ClaimsInbox.Ingest`,
  `ClaimsInbox.Close`, `ClaimsInbox.DeliveredByClass`, and overflow
  accounting.
- `OperationsSequencer.CommitPhase1` and `CommitPhase2`.
- `WireClaimsIntake` and continuation delivery.
- Existing analyzers in `cmd/sylk-lint` and `cmd/nogo`.

Acceptance criteria:

- A documented baseline command exists for normal tests, race tests on
  claims-critical packages, and lint analyzers.
- The baseline is runnable on a developer machine without external
  services.
- Slow e2e tests are tagged or scoped so normal package tests remain
  usable during development.
- Test helpers use temp directories for WAL/outbox state and close all
  durable boards and inboxes.

Test cases:

- Unit: run existing tests for `core/claims`, `core/boot`, and
  `agents/shared` with no network.
- Unit negative path: verify corrupt WAL lines become notification
  errors and do not panic replay.
- Unit edge case: verify zero `ClaimsBoardConfig.SessionDir` uses
  in-memory durability behavior while still keeping outbox logic
  nil-safe.
- Integration with mockery: mock the delta bus and projector while a
  durable board writes to a temp WAL; assert mutations survive close and
  reopen.
- E2E with mockery: simulate Guide bus delivery through mocked
  `DeltaSubscriber` and UI bridge observer intake; assert rendered state
  follows canonical deltas.
- Race and deadlock: run `go test -race` on `core/claims`,
  `core/boot`, and `agents/shared` while repeatedly posting claims,
  closing inboxes, draining outbox, and cancelling continuation stores.
- Simulated usage: create a multi-claim action, submit testaments,
  evaluate validations, save a snapshot, reopen the board, drain the
  outbox, and assert the final projection is identical before and after
  replay.

### 12.2 Phase 1 - Boot Sequencer Completion

Goal: implement the complete boot phase sequence described in §3 while
preserving the currently implemented phase 1 and phase 2 behavior.

#### 12.2.1 Extend `OperationsSequencer` through boot phases 3-7

Description: Extend `core/boot/operations.go` from the currently
implemented durable substrate and system participant phases to the full
boot sequence: knowledge backends, infrastructure services, agent
activation, user-facing surfaces, and boot complete. Each phase must be
a claim cycle with idempotency keys, readiness artifacts, timing
artifacts, and receipt validation. The new phases must use the same
claim/testament lifecycle APIs as phase 1 and phase 2 so replay remains
ordinary board replay, not a special boot-only reducer.

File references:

- `core/boot/operations.go`
- `core/boot/operations_test.go`
- `core/claims/board_lifecycle.go`
- `core/claims/types.go`
- `docs/CLAIMS_AND_INFRASTRUCTURE.md`
- `docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`

Existing APIs and integration points:

- `OperationsSequencer.CommitPhase1` and `CommitPhase2` provide the
  pattern for phase claims, readiness artifacts, failure artifacts,
  idempotency keys, and receipt validation.
- `claims.ActionTypeBoot` and `claims.ActionTypeActivation` already
  exist.
- `GenerateClaimAction`, `PostGeneratedClaim`,
  `AcknowledgeClaimReceipt`, `UpdateClaimProgress`,
  `GenerateTestamentAction`, `PostGeneratedTestament`,
  `AcknowledgeTestamentReceipt`, `BeginTestamentValidation`,
  `EvaluateValidation`, and `CompleteTestamentValidation` already
  provide the lifecycle.
- `claims.ArtifactKindTiming`, `ArtifactKindReadiness`,
  `ArtifactKindStateHash`, and error artifact kinds already provide
  the artifact vocabulary needed for boot testimony.

Acceptance criteria:

- `OperationsSequencer` exposes explicit commit methods or a typed
  phase dispatcher for phases 3-7.
- Every phase has deterministic idempotency keys derived from
  `(boot_operation_prefix, phase, participant_or_step, outcome)`.
- A phase cannot run until all prerequisite phases are satisfied.
- Re-running a completed phase returns the existing claim and testament
  IDs without adding duplicate claims, testaments, validations, or
  outbox records.
- A failed phase posts a failure testament with a `boot_failure`
  artifact and prevents later phases from starting.
- Boot complete posts a terminal `boot.satisfied` testament summarizing
  phase outcomes.

Test cases:

- Unit happy path: each new `CommitPhaseN` posts exactly one phase
  claim, one completion testament, readiness artifacts for every
  declared participant, a timing artifact, and passed receipt
  validations.
- Unit negative path: a missing backend/service readiness entry causes
  the phase claim to receive a failure testament and failed validation.
- Unit edge case: duplicate participant IDs normalize deterministically
  and do not create duplicate participant claims.
- Unit race: concurrent retries of the same phase converge on the same
  claim/testament IDs and do not violate lifecycle transition rules.
- Unit deadlock: a phase commit with a cancelled context returns
  promptly and leaves the board in the last durable lifecycle boundary.
- Integration with mockery: mock `claims.ClaimPostPolicy`,
  `claims.AgentRefResolver`, `claims.DeltaPublisher`, and
  `claims.ClaimsProjector`; run phases 1-7 against a durable board and
  assert canonical deltas and outbox records are produced.
- Integration negative path: mocked claim post policy rejects one
  activation claim; assert the phase records `claim.post_failed` and a
  failure artifact without starting later phases.
- E2E with mockery: use mocked knowledge backend, VFS provisioner, DAG
  processor, tool runtime, provider gateway, guardian, agent activator,
  and UI bridge readiness reporters; simulate a full cold boot and
  assert the final board has `boot.satisfied`.
- E2E race/deadlock: inject a mocked participant that blocks until its
  context is cancelled; assert boot abort drains the scope and does not
  leave live workers.
- Simulated usage: close and reopen the durable board after every phase
  boundary, then retry boot from the top; assert the final projection is
  identical to a clean uninterrupted boot.

#### 12.2.2 Wire boot into process startup

Description: Connect the boot sequencer to the real process startup
path so boot claims are not only available in tests. Startup must open
the durable board, open the Guide event bus, replay WAL, register
system participants, activate always-hot agents, wire the UI observer
intake, and then commit boot complete. The startup path must remain
idempotent across process restarts.

File references:

- `cmd/tui.go`
- `cmd/root.go`
- `main.go`
- `core/boot/operations.go`
- `core/claims/session_registry.go`
- `core/claims/session_inbox_registry.go`
- `agents/*/*go` constructors that call `WireClaimsIntake`
- `ui/bridge/claims.go`

Existing APIs and integration points:

- `claims.DefaultSessionBoardRegistry().Register` and
  `ReplaceForReason`.
- `claims.DefaultSessionInboxRegistry().Register` and `Remove`.
- `shared.WireClaimsIntake` for agent and UI observer intake.
- Agent constructors that already build `ContinuationStore` and call
  `WireClaimsIntake`, including architect, engineer, orchestrator,
  guardian, inspector, tester, designer, librarian, archivalist, and
  academic agents.
- `ClaimsBridge.startClaimsIntake` for the UI bridge.

Acceptance criteria:

- Process startup creates exactly one durable board per active session
  and registers it before any agent intake starts.
- Startup fails loud if a production claims board is missing a
  `Scope`, `DeltaBus`, or required identity resolver.
- Always-hot agent activation is represented by activation claims and
  readiness testaments.
- UI observer intake starts only after the board and bus are available.
- Restart after partial boot retries from the first unsatisfied phase
  and does not overwrite an existing session board without
  `ReplaceForReason`.

Test cases:

- Unit: startup wiring helper rejects nil board, nil bus, nil scope, or
  empty session ID with typed errors.
- Unit negative path: duplicate session board registration returns
  `ErrSessionBoardAlreadyRegistered` unless the caller gives an
  explicit replacement reason.
- Unit edge case: legacy sessions marked `LegacySessionNoWAL` remain
  usable but report fallback continuity.
- Integration with mockery: mock the bus, scope, identity resolver, and
  readiness reporters; assert startup registers boards, inboxes, and
  phase claims in the required order.
- Integration negative path: mocked UI observer intake fails to start;
  assert the boot phase records degraded UI readiness and does not
  claim full boot satisfaction.
- E2E with mockery: run a headless startup sequence with mocked agents
  and providers; assert all always-hot agents have activation
  testaments and all on-demand agents remain cold.
- Race/deadlock: concurrently start and stop a session while boot is
  progressing; assert registry operations remain safe and all scopes
  drain.
- Simulated usage: start the process, interrupt between phase commits,
  reopen the durable board, and assert startup resumes at the correct
  unsatisfied phase.

#### 12.2.3 Add boot operator telemetry and health projection

Description: Surface boot state as structured telemetry and board
health, not only as phase testaments. The telemetry must be derived
from phase claims, testament artifacts, and `ProjectionHealth`, so it
stays consistent after replay.

File references:

- `core/boot/operations.go`
- `core/claims/outbox_health.go`
- `core/claims/skills_carry_forward.go`
- `agents/archivalist/skills_core.go`
- `docs/FABRIC_OBSERVABILITY.md`
- `docs/BUS_OBSERVABILITY.md`

Existing APIs and integration points:

- `claims.ProjectionHealth` and `ProjectionHealthHistory`.
- `claims.ProjectionHealthSkill`.
- Boot readiness artifacts in `core/boot/operations.go`.
- Fabric projector in `core/claims/projectors.go`.
- Canonical delta projector in `core/claims/canonical_delta_projector.go`.

Acceptance criteria:

- Boot health exposes phase, outcome, duration, replay sequence,
  readiness artifact counts, failure artifact counts, and outbox lag.
- Boot telemetry is replay-safe: reopening the board produces the same
  health facts from durable board state and outbox state.
- Operator skills can query boot health without reading logs.
- Failure telemetry includes the phase ID, claim ID, testament ID, and
  failing participant ID.

Test cases:

- Unit: phase artifact parsing produces stable health summaries for
  success and failure testaments.
- Unit negative path: malformed or missing readiness artifacts create a
  health warning, not a panic.
- Unit edge case: a boot phase with zero optional participants reports
  success only when required participants are satisfied.
- Integration with mockery: mock projectors to leave outbox records in
  pending, retryable, and terminal states; assert boot health includes
  lag and terminal failure details.
- E2E with mockery: simulate startup with one failing service and query
  `claims_projection_health`; assert the boot failure is discoverable
  from skills output.
- Race/deadlock: query boot health while phases commit and outbox drains
  concurrently under `go test -race`.
- Simulated usage: replay a boot WAL from disk, query boot health, and
  compare it to the pre-restart health snapshot.

### 12.3 Phase 2 - Universal Participant and Service Dispatch

Goal: make deterministic services first-class claims participants while
preserving the existing agent `ClaimsInbox` path.

#### 12.3.1 Introduce participant registration metadata

Description: Add a participant registration model that covers agents,
services, systems, and external participants. Registration must carry
canonical identity, category, scope keys, declared queues, timeouts,
concurrency budgets, determinism, and subscribed claim actions. This
metadata is the source for capacities and deadlines used by dispatchers
and audits.

File references:

- `core/claims/types.go`
- `core/claims/canonical_delta.go`
- `core/agents/identity/*`
- `core/container/identity_registry.go`
- `docs/CLAIMS_AND_INFRASTRUCTURE.md`
- new planned file: `core/claims/participants.go`
- new planned file: `core/claims/participants_test.go`

Existing APIs and integration points:

- `claims.AgentRef`, `AgentRefResolver`, `AgentRefFromIdentity`, and
  `DegradedAgentRef`.
- `claims.Relation`, including issuer, subject, evaluator, caused_by,
  depends_on, and reviews relationships.
- `ClaimsBoardConfig.AgentRefResolver` for canonical delta delivery.
- `core/agents/identity.Factory`, `AgentIdentity`, and `TaskRef`.

Acceptance criteria:

- Participant metadata has a stable UID derivation function for
  service/system participants based on service type and scope keys.
- Registration rejects empty category, empty route key, unbounded
  queue capacity, and missing concurrency budget.
- Existing agent identities convert losslessly into participant refs.
- Canonical deltas route by UID when resolver succeeds and by explicit
  degraded `agent_type` topic only when resolver fails.
- Registration metadata is immutable after activation except for
  explicit generation changes.

Test cases:

- Unit happy path: derive identical service UIDs from identical
  service type and scope keys across process restarts.
- Unit negative path: missing scope key, missing category, or unbounded
  queue capacity returns a typed validation error.
- Unit edge case: scope keys with different ordering normalize to the
  same UID only when the semantics say the keys are unordered.
- Unit race: concurrent registration attempts for the same participant
  converge on one record and do not create duplicate UIDs.
- Unit deadlock: participant registry callbacks must not call back into
  board mutation while holding registry locks.
- Integration with mockery: mock `AgentRefResolver` and `ClaimPostPolicy`
  to verify canonical delivery uses UID topics when available and
  degraded topics when not.
- E2E with mockery: wire mocked service participants and mocked agent
  participants in one session; post a mixed action and assert every
  directed claim reaches the right participant route.
- Simulated usage: restart the session with a new process UID and assert
  service UIDs remain stable while process-scoped boot UIDs rotate.

#### 12.3.2 Add deterministic service handler dispatch

Description: Add a service dispatch path parallel to agent
`ClaimsInbox`. A service handler consumes directed claims for a service
participant, performs deterministic work, and emits a testament with
artifacts. It must use the same board lifecycle transitions and
canonical deltas as agents. Long-running services may return a durable
pending marker, but the handler queue must remain bounded and owned by
a goroutine scope.

File references:

- `core/claims/inbox.go`
- `core/claims/board_lifecycle.go`
- `core/claims/expected_tool_execution.go`
- `agents/shared/claims_intake.go`
- `core/concurrency/goroutine_scope.go`
- new planned file: `core/claims/service_dispatch.go`
- new planned file: `core/claims/service_dispatch_test.go`

Existing APIs and integration points:

- `claims.DeltaSubscriber` and `DeltaPublisher` for claim delivery.
- `claims.InboxPatternsFor` and canonical topic helpers in
  `core/claims/topics.go`.
- Board lifecycle APIs for receipt, progress, testament generation,
  testament posting, validation start, and validation completion.
- `claims.ScopeProvider` for tracked dispatch workers.
- `claims.ArtifactKindErrorDiagnostic`, `ArtifactKindInterrupted`,
  `ArtifactKindToolTimeout`, and related error artifact kinds.

Acceptance criteria:

- Service handlers are registered by participant UID and action type.
- Dispatch subscribes only to narrow canonical claim-posted topics for
  the registered participant, never to a broad firehose.
- Every handler invocation records claim receipt before doing work.
- Handler success posts a testament with artifacts and advances receipt
  validation when appropriate.
- Handler failure posts an error testament or records a lifecycle
  failure; a Go error is never the only observable outcome.
- Handler panic is recovered, converted to an error artifact, and
  surfaced through telemetry.
- Queue capacity and worker concurrency are derived from participant
  metadata.

Test cases:

- Unit happy path: a service handler receives a claim, acknowledges
  receipt, posts a testament, and passes receipt validation.
- Unit negative path: handler returns an error; the board contains an
  error testament and the claim transitions to a failure lifecycle
  status.
- Unit edge case: duplicate delivery of the same `DeltaKey` is
  deduplicated and does not invoke the service handler twice.
- Unit race: multiple claims for the same service dispatch concurrently
  up to the declared concurrency budget and no further.
- Unit deadlock: a handler that attempts a nested board mutation cannot
  deadlock the dispatch lock; locks are not held across handler calls.
- Integration with mockery: mock `DeltaSubscriber`, `DeltaPublisher`,
  `ScopeProvider`, and a service handler interface; publish canonical
  claim deltas and assert dispatch outcomes.
- Integration negative path: mocked subscriber reports overflow; assert
  an error artifact or telemetry counter records the overflow.
- E2E with mockery: simulate VFS provisioner, DAG processor, tool
  runtime, provider gateway, and guardian service handlers responding
  to claims in a boot sequence.
- E2E race/deadlock: block one mocked handler and cancel its context
  while other handlers continue; assert cancellation artifacts are
  posted and the scope drains.
- Simulated usage: post service claims with mixed priorities and assert
  dispatch order follows priority only where the queue contract says it
  should.

#### 12.3.3 Add programmatic validator registry

Description: Add deterministic validator registration and dispatch for
mechanical validation. Programmatic validators must evaluate a
testament's artifacts against a validation and emit the same
`validation.evaluated` and claim lifecycle deltas as agentic
validators. The registry must require determinism metadata and explicit
timeout/concurrency budgets.

File references:

- `core/claims/types.go`
- `core/claims/board_lifecycle.go`
- `core/claims/expected_tool_execution.go`
- `docs/ARTIFACTS_AND_VALIDATIONS.md`
- new planned file: `core/claims/validator_registry.go`
- new planned file: `core/claims/validator_dispatch.go`
- new planned file: `core/claims/validator_dispatch_test.go`

Existing APIs and integration points:

- `claims.Validation`, `ValidationType`, `ValidationStatus`,
  `ValidationTypeSemanticsFor`, and `KnownValidationTypes`.
- `ClaimsBoard.EvaluateValidation`.
- `BeginTestamentValidation`, `CompleteTestamentValidation`, and
  `CompleteTestamentValidationError`.
- `claims.ExpectedToolExecutor` and related expected-tool policy
  interfaces.
- `shared.dispatchExpectedValidationTools` for expected tool execution
  from claims intake.

Acceptance criteria:

- Validator registration requires validation type, action type filter,
  determinism level, timeout, concurrency budget, and artifact target
  contract.
- Receipt validation remains mechanical and backwards-compatible.
- Programmatic validators never call an LLM provider.
- Validator panic, timeout, malformed input, missing artifact, and
  policy denial all become structured validation outcomes and/or error
  artifacts.
- Optional validation failure does not reject the claim unless the
  validation contract says it must.
- Replay audit can re-run pure/content validators without mutating the
  original WAL.

Test cases:

- Unit happy path: validator passes required validation and auto-satisfies
  a claim when all required validations pass.
- Unit negative path: validator returns failed, incomplete, and errored
  verdicts and each produces the correct claim lifecycle transition.
- Unit edge case: optional validation fails while required validations
  pass; claim satisfaction behavior matches the validation contract.
- Unit race: many validations dispatch concurrently but never exceed
  declared concurrency.
- Unit deadlock: validator dispatch does not hold board locks while
  executing handler code.
- Integration with mockery: mock validator handler, expected tool
  executor, policy, redactor, and remediation poster; assert outcomes
  are committed as board facts.
- Integration negative path: mocked validator blocks past deadline;
  assert timeout artifact, validation errored status, and scope drain.
- E2E with mockery: service handler posts artifacts, programmatic
  validator evaluates them, UI observer receives canonical deltas, and
  a continuation waiting on the claim resumes.
- E2E race/deadlock: start validators while a shutdown signal arrives;
  assert in-flight validations either commit or abort durably.
- Simulated usage: replay a WAL containing validator outcomes and assert
  no validator handler is re-executed outside explicit replay audit.

### 12.4 Phase 3 - Durable WAL, Outbox, Replay, and Recovery Closure

Goal: close the gaps between the current durable board and the
operational guarantees in §4 and §5.

#### 12.4.1 Harden WAL append and replay semantics

Description: The durable board already writes WAL events before
mutating in-memory state and replays JSONL events. This item hardens
that path for fsync discipline, replay error classification,
checkpoint compatibility, duplicate event collapse, and deterministic
derived-state rebuild.

File references:

- `core/claims/board_durable.go`
- `core/claims/board_durable_test.go`
- `core/claims/phase9_test.go`
- `core/claims/projection_rebuild.go`
- `core/claims/relations_index.go`

Existing APIs and integration points:

- `DurableBoard.appendEvent`, `appendCommittedEvent`, `replayWAL`,
  `applyEvent`, `SaveSnapshot`, `loadSnapshot`, and
  `walContentFingerprint`.
- `ClaimsBoard.rebuildDerivedState`.
- `ClaimsBoard.RecordNotificationError`.
- WAL event kinds such as `claim_action_generated`,
  `claim_lifecycle_transition`, `testament_action_generated`,
  `validation_evaluated`, `phase_transition`, and `board_complete`.

Acceptance criteria:

- WAL append explicitly syncs according to configured durability tier.
- Replay classifies malformed JSON, unknown event kind, missing
  referenced entity, duplicate event, and illegal transition
  separately in notification errors.
- Duplicate logical events are collapsed by fingerprint without
  duplicate board mutations or outbox records.
- Snapshot load validates board/session identity and derived indexes
  before accepting the checkpoint.
- Replay never panics on corrupt input; it surfaces corruption as board
  notification errors and continues where the contract allows.

Test cases:

- Unit happy path: append every known WAL event kind, reopen the board,
  and assert projection equality.
- Unit negative path: corrupt JSON line, illegal lifecycle transition,
  and missing referenced claim produce bounded notification errors.
- Unit edge case: empty WAL, empty snapshot, snapshot-only replay, and
  duplicate WAL content all produce deterministic state.
- Unit race: append events while saving snapshots under controlled
  scope scheduling; reopened board must choose a consistent boundary.
- Unit deadlock: replay cannot call subscriber callbacks while holding
  board mutation locks.
- Integration with mockery: mock projector and delta bus, write events,
  reopen, drain outbox, and assert projection calls are replayable.
- Integration negative path: mocked WAL fsync failure returns a typed
  append error and leaves in-memory state unchanged.
- E2E with mockery: simulate process crash after WAL append but before
  outbox projection; reopen and assert delta projection catches up.
- E2E race/deadlock: repeatedly crash/reopen in a loop while mocked
  outbox projector alternates success, retryable failure, and terminal
  failure; no goroutine leak is allowed.
- Simulated usage: create a session with many claims, snapshot at a
  sequence boundary, compact, reopen, and assert replay duration and
  final projection match the budget model.

#### 12.4.2 Make outbox projection fully observable and repairable

Description: The outbox already stores deterministic projection records
and exposes health. This item completes operational repair: dry-run
inspection, bounded replay, per-projector retry policy, terminal failure
surfacing, and report testaments when repair mutates projection state.

File references:

- `core/claims/outbox.go`
- `core/claims/outbox_health.go`
- `core/claims/projectors.go`
- `core/claims/canonical_delta_projector.go`
- `core/claims/skills_carry_forward.go`
- `agents/archivalist/skills_core.go`

Existing APIs and integration points:

- `ClaimsOutbox.InsertMany`, `Pending`, `Claim`, `MarkSucceeded`,
  `MarkFailed`, `Records`, `ProjectPending`, and `Health`.
- `DurableBoard.DrainOutbox`.
- `ClaimsBoard.ProjectionHealth` and `ProjectionHealthHistory`.
- `claims_projection_health` and projection repair skills.
- `ClaimsProjector` implementations: fabric, canonical delta, and
  knowledge mirror projectors.

Acceptance criteria:

- Outbox repair supports dry-run, bounded replay limit, projector
  filter, board/session filter, and report emission.
- Retryable failures include attempt count and last error.
- Terminal failures are bounded in health output and include record ID,
  sequence, entity type, entity ID, mutation kind, and last error.
- Expired leases are claimable by a later worker.
- Projection health never grows unbounded in memory.

Test cases:

- Unit happy path: pending record is claimed, projected, marked
  succeeded, and reflected in latency metrics.
- Unit negative path: projector returns error; outbox marks retryable
  failure and board records projection error.
- Unit edge case: expired lease is re-claimed; unexpired lease is not.
- Unit race: multiple workers attempt to claim the same record and only
  one succeeds.
- Unit deadlock: projector callback cannot deadlock by querying the
  board while outbox status mutation is in progress.
- Integration with mockery: mock two projectors with independent
  success/failure behavior; assert health separates per-projector lag.
- Integration negative path: mocked projector returns terminal error
  when configured by repair policy; assert terminal failure summary is
  bounded.
- E2E with mockery: simulate missing canonical bus delivery, run repair
  dry-run, then replay; assert inbox receives repaired canonical deltas.
- E2E race/deadlock: run repair while normal board mutations schedule
  projection; assert no duplicate records and no stuck leases.
- Simulated usage: operator queries projection health, repairs a
  specific session, and receives a repair report testament.

#### 12.4.3 Reconstruct continuations and in-flight work after crash

Description: The continuation store already supports pending
continuations and recovery. This item wires recovery into process
startup and reconciles in-flight validation, expected tool, service
handler, and testament accumulation boundaries after WAL replay.

File references:

- `agents/shared/consult_continuations.go`
- `agents/shared/claims_intake.go`
- `agents/shared/await_consults_skill.go`
- `core/claims/accumulator.go`
- `core/claims/expected_tool_execution.go`
- `core/boot/operations.go`

Existing APIs and integration points:

- `ContinuationStore.RecoverPendingContinuations`.
- `ContinuationStore.DeliverClaimResult`,
  `AwaitConsultsOrYield`, `AwaitClaimResults`,
  `CancelContinuation`, and `Stop`.
- `shared.deliverExpectedPeerResultToContinuation`.
- `claims.TestamentAccumulator`, including flush lifecycle.
- Board lifecycle APIs for validation and failure recording.

Acceptance criteria:

- Startup calls continuation recovery after WAL replay and before
  normal agent intake resumes new work.
- Continuations whose awaited claims are already terminal resume
  immediately from board state.
- Continuations whose awaited claims failed receive structured
  interrupted/error results, not silent timeouts.
- Partially accumulated testaments are either reconstructed from
  durable artifacts or failed with an error artifact.
- Recovery is bounded by pending continuation count and configured
  worker budgets.

Test cases:

- Unit happy path: a pending continuation recovers after its awaited
  claim already has a posted testament.
- Unit negative path: awaited claim is rejected; recovered continuation
  receives an error result and does not resume as success.
- Unit edge case: orphan response arrives before expectation
  registration and is replayed when expectation is restored.
- Unit race: recovery and live delivery of the same result happen
  concurrently and only one resume fires.
- Unit deadlock: `RecoverPendingContinuations` does not block forever
  when resume function is slow and scope cancellation fires.
- Integration with mockery: mock `GoroutineScopeProxy`, board-facing
  expected tool executor, and resume function; assert recovery
  schedules tracked work and closes it.
- Integration negative path: mocked scope rejects `Go`; assert
  continuation remains visible with error artifact or retry state.
- E2E with mockery: simulate an agent yielding to peer consults, crash
  before peer testament delivery, reopen board, deliver peer result,
  and assert the original agent resumes exactly once.
- E2E race/deadlock: concurrently cancel a continuation while recovery
  tries to resume it; assert cancellation wins deterministically or the
  result wins deterministically based on sequence order.
- Simulated usage: run a multi-agent consult chain with child and
  grandchild claims, crash at each lifecycle boundary, and assert
  recovery reconstructs pending work consistently.

### 12.5 Phase 4 - Cancellation Propagation and Shutdown Drain

Goal: implement cancellation as a claims-plane operation that walks the
claim graph, emits durable failure evidence, and drains scopes
deterministically.

#### 12.5.1 Add cancellation graph traversal

Description: Implement cancellation traversal over claim relationships.
The traversal starts from an active root claim, discovers descendants
through `RelationshipCausedBy`, `RelationshipDependsOn`, and any
documented child-claim relationship used by consultations and
continuations, and commits failure lifecycle states in reverse BFS order
so leaves finish before roots.

File references:

- `core/claims/types.go`
- `core/claims/relations_index.go`
- `core/claims/board_context.go`
- `core/claims/skills_traverse.go`
- `core/claims/board_lifecycle.go`
- new planned file: `core/claims/cancellation.go`
- new planned file: `core/claims/cancellation_test.go`

Existing APIs and integration points:

- `Relation`, `RelationshipCausedBy`, `RelationshipDependsOn`,
  `RelationshipClaim`, and related relationship constants.
- Relation index query helpers in `core/claims/relations_index.go`.
- `RecordClaimProgressFailure` and `RecordClaimLifecycleFailure`.
- Error artifact kind `ArtifactKindInterrupted`.
- Canonical lifecycle action `DeltaActionClaimProgressFailed`.

Acceptance criteria:

- Cancellation can target one claim, all active root claims in a
  session, or all active claims during shutdown.
- Traversal is deterministic for the same board state.
- Descendants transition before ancestors.
- Every cancelled claim receives an interrupted reason that cites the
  originating cancellation claim.
- Already-terminal claims are skipped and recorded in the cancellation
  summary.
- Cycles in relations are detected and do not cause infinite traversal.

Test cases:

- Unit happy path: root, child, and grandchild claims are cancelled in
  reverse BFS order and all receive interrupted failure artifacts.
- Unit negative path: cancellation target does not exist; cancellation
  claim receives an error testament and no unrelated claim changes.
- Unit edge case: relation cycle, duplicate relation, and already
  terminal descendant are handled deterministically.
- Unit race: new child claim appears while cancellation traversal is in
  progress; sequence ordering defines whether it is included or must be
  cancelled by a follow-up sweep.
- Unit deadlock: traversal must not hold board locks while invoking
  cancellation callbacks or scope cancellation.
- Integration with mockery: mock delta bus and scope provider; assert
  canonical `claim.progress_failed` deltas are emitted for every
  cancelled claim.
- Integration negative path: mocked board mutation fails on one child;
  assert the cancellation summary records partial failure and later
  descendants are not silently dropped.
- E2E with mockery: simulate user Esc through UI bridge, orchestrator
  cancellation claim, child consult claims, and continuation waits;
  assert UI rows close and continuation waiters release.
- E2E race/deadlock: hold one child handler blocked on a mocked
  channel, cancel root, then release or time out the handler; assert
  shutdown still drains before the configured hard deadline.
- Simulated usage: long-press interrupt cancels all active root claims
  in a session and leaves unrelated sessions untouched.

#### 12.5.2 Wire cancellation into agent and service execution

Description: Ensure every agent intake, service handler, validator
handler, expected tool execution, and continuation wait receives a
context that is cancelled when its claim is cancelled. This connects
board-level cancellation to actual work cancellation.

File references:

- `agents/shared/claims_intake.go`
- `agents/shared/consult_continuations.go`
- `core/claims/service_dispatch.go` (planned)
- `core/claims/validator_dispatch.go` (planned)
- `core/claims/expected_tool_execution.go`
- `core/concurrency/goroutine_scope.go`
- `agents/*/claims_testimony.go`

Existing APIs and integration points:

- `shared.WireClaimsIntake` dispatches `ProcessEntry` through
  `Scope.Go("process_claim", ...)`.
- `ContinuationStore.CancelContinuation` and `Stop`.
- `claims.ExecuteValidationExpectedTools`.
- `concurrency.GoroutineScope.SignalShutdown` and `Shutdown`.

Acceptance criteria:

- Every claim execution has a claim-scoped context derived from the
  parent scope and cancel registry.
- Cancelling a claim cancels agent `processClaimsEntry`, service
  handler, validator, expected tool execution, and continuation waits
  associated with that claim.
- Handler cleanup posts interrupted artifacts when work stops because
  of cancellation.
- A cancelled context is checked before starting expensive work and
  after each durable boundary.
- No path starts untracked goroutines during cancellation.

Test cases:

- Unit happy path: cancelling a claim cancels registered contexts and
  calls cleanup hooks exactly once.
- Unit negative path: handler ignores context; scope hard deadline
  reports `GoroutineLeakError` and cancellation summary marks stuck
  work.
- Unit edge case: cancellation arrives before handler starts, during
  handler execution, during testament posting, and after terminal
  state.
- Unit race: cancellation and successful testament posting happen
  concurrently; the lower sequence wins and the other path records a
  no-op or superseded outcome.
- Unit deadlock: cancellation callback must not wait on a scope worker
  while holding the registry lock that the worker needs for cleanup.
- Integration with mockery: mock `ScopeProvider`, service handler,
  validator handler, and expected tool executor; assert contexts are
  cancelled and artifacts are posted.
- Integration negative path: mocked executor blocks until context
  cancellation; assert it returns interrupted and no further tool calls
  run.
- E2E with mockery: simulate an LLM-agent claim, a service child claim,
  a validator, and a continuation wait; cancel root and assert all
  paths stop.
- E2E race/deadlock: cancel during process shutdown while outbox
  projection is also draining; assert no blocked goroutines remain.
- Simulated usage: repeated user Esc events coalesce into one
  cancellation operation per root claim.

#### 12.5.3 Implement deterministic shutdown ordering

Description: Codify process shutdown order for claims operations:
stop new intake, signal all scopes, cancel active claims, drain
continuations, drain outbox, save snapshots, close WAL/outbox files,
and remove registries. Shutdown must be idempotent and safe to call
after partial startup.

File references:

- `core/concurrency/goroutine_scope.go`
- `core/claims/session_registry.go`
- `core/claims/session_inbox_registry.go`
- `core/claims/board_durable.go`
- `agents/shared/consult_continuations.go`
- `cmd/tui.go`
- `ui/bridge/claims.go`
- new planned file: `core/boot/shutdown.go`

Existing APIs and integration points:

- `GoroutineScope.SignalShutdown`, `Shutdown`, `WorkerCount`, and
  `GoroutineLeakError`.
- `ClaimsInbox.Close`.
- `ContinuationStore.Stop`.
- `DurableBoard.DrainOutbox`, `SaveSnapshot`, and `Close`.
- Session registry `Remove` APIs.

Acceptance criteria:

- Shutdown refuses new inbox subscriptions and new service dispatch.
- Active work receives cancellation before hard drain starts.
- Outbox drain runs after claim cancellation commits, so cancellation
  deltas can project.
- Snapshot save happens after final board mutation and before WAL close.
- Shutdown reports leaked workers with descriptions and stack dump.
- Repeated shutdown calls are safe and return the same final state.

Test cases:

- Unit happy path: shutdown drains workers, closes inboxes, drains
  outbox, saves snapshot, closes durable board, and removes registries.
- Unit negative path: one worker ignores cancellation; shutdown returns
  `GoroutineLeakError`.
- Unit edge case: shutdown before boot, during boot, after boot
  failure, and after normal completion.
- Unit race: concurrent shutdown calls share the same drain and do not
  double-close resources.
- Unit deadlock: a worker waiting on outbox projection is cancelled
  before `DurableBoard.Close`.
- Integration with mockery: mock durable board wrapper, inboxes,
  continuation stores, and scopes; assert shutdown order.
- Integration negative path: mocked snapshot save fails; assert WAL
  close still runs and the failure is reported.
- E2E with mockery: simulate a session with active claims, pending
  continuations, and outbox lag, then send process shutdown; assert
  final board contains shutdown/cancellation evidence.
- E2E race/deadlock: send shutdown while UI bridge is processing a
  delta; assert observer intake closes without panic.
- Simulated usage: SIGTERM during provider gateway warm-up drains
  within configured deadlines and marks boot incomplete.

### 12.6 Phase 5 - Backpressure, Queue Budgets, and Resource Bounds

Goal: prove every queue and in-memory structure in the claims plane is
bounded, observable, and tied to participant/runtime configuration.

#### 12.6.1 Centralize queue and concurrency budgets

Description: Replace scattered queue and timeout defaults in the claims
operational layer with named budget structs derived from participant
metadata and runtime config. Existing constants may remain only as
documented defaults behind a normalized configuration path.

File references:

- `core/claims/inbox.go`
- `core/claims/outbox.go`
- `core/claims/board_durable.go`
- `core/concurrency/adaptive_channel.go`
- `core/concurrency/bounded_overflow.go`
- `agents/shared/consult_continuations.go`
- planned file: `core/claims/operations_config.go`

Existing APIs and integration points:

- `InboxConfig.BusSubscriptionQueueCap`.
- `computeBusSubscriptionQueueCap`.
- `durableOutboxProjectBatchLimit`.
- `defaultOutboxLease`.
- Continuation orphan limit and deadline watcher logic.
- `AdaptiveChannelConfig` and `BoundedOverflow`.

Acceptance criteria:

- Claims operation budgets are represented in one normalized config
  struct with documented derivation.
- Inboxes, service dispatch queues, validator queues, continuation
  orphan buffers, outbox projection batches, and shutdown deadlines all
  receive budgets from config or participant metadata.
- Zero or negative config values normalize to documented derived
  defaults; they never mean unbounded growth.
- Runtime projection exposes the effective budget values used by a
  board/session.

Test cases:

- Unit happy path: config normalization derives all budgets from input
  metadata and host capacity.
- Unit negative path: explicit unbounded values are rejected or
  normalized to bounded defaults with warnings.
- Unit edge case: low host capacity, high participant fan-in, and zero
  optional participants all derive valid budgets.
- Unit race: budget reads during config replacement produce either old
  or new immutable snapshots, never partial values.
- Unit deadlock: budget normalization cannot call into board mutation
  while board config locks are held.
- Integration with mockery: mock participant registry and scope; assert
  inbox and service dispatcher receive expected queue caps.
- E2E with mockery: simulate many participants publishing deltas and
  assert queue caps, overflow counters, and backpressure behavior match
  effective config.
- Simulated usage: load a small-memory profile and assert all queue
  capacities shrink according to formula without dropping required
  directed work silently.

#### 12.6.2 Implement observable overflow behavior

Description: Ensure every overflow path produces an observable signal:
error artifact, board notification error, outbox health warning, or
telemetry counter. Directed work must never disappear silently.

File references:

- `core/claims/inbox.go`
- `core/claims/bus_publisher.go`
- `core/claims/outbox_health.go`
- `agents/shared/claims_intake.go`
- `core/concurrency/bounded_overflow.go`
- `core/concurrency/adaptive_channel.go`
- planned file: `core/claims/backpressure.go`

Existing APIs and integration points:

- `DroppedCounter` and `ClaimsInbox.OverflowCount`.
- `ClaimsInbox.DeliveredByClass`.
- `DeltaClass` and `InboxClass`.
- `ClaimsBoard.RecordNotificationError`.
- Error artifact kinds for projection, timeout, interrupted, policy
  denied, and missing evidence.

Acceptance criteria:

- Subscriber overflow increments class-specific counters.
- Directed and consultation traffic overflow produces durable evidence
  or a retry path; observation traffic may be summarized as a coverage
  gap but not hidden.
- Near-capacity and overflow telemetry labels include queue name,
  participant ID, session ID, and inbox class.
- Overflow summaries are bounded and drained so they do not become an
  unbounded memory leak.

Test cases:

- Unit happy path: queue below threshold delivers all messages and no
  overflow signal is emitted.
- Unit negative path: directed work overflows; error artifact or board
  notification is recorded.
- Unit edge case: observation overflow produces coverage gap summary
  without rejecting high-priority directed work.
- Unit race: overflow while inbox closes does not panic and does not
  increment counters after close.
- Unit deadlock: overflow reporting cannot block the queue's send path
  indefinitely.
- Integration with mockery: mock `DroppedCounter` subscriptions and
  delta bus delivery; assert inbox overflow accounting is correct.
- E2E with mockery: simulate peer saturation through
  `DefaultSessionInboxRegistry` and assert publisher receives
  `ErrPeerSaturated` and posts recoverable evidence.
- Simulated usage: burst many consultation claims at one peer, then
  drain; assert high-priority consult responses are protected from
  broad observer traffic.

#### 12.6.3 Add resource audits for leaks and unbounded growth

Description: Add runtime audits that periodically scan goroutine
scope inventory, inbox registries, outbox queue health, continuation
orphan buffers, and board notification errors. Audits must be bounded,
scope-owned, and produce claims-plane evidence or telemetry.

File references:

- `core/concurrency/goroutine_scope.go`
- `core/claims/session_registry.go`
- `core/claims/session_inbox_registry.go`
- `core/claims/outbox_health.go`
- `agents/shared/consult_continuations.go`
- planned file: `core/claims/operations_audit.go`

Existing APIs and integration points:

- `GoroutineScope.WorkerCount`.
- `SessionBoardRegistry.Snapshot` and `SessionIDs`.
- `SessionInboxRegistry.Lookup` and `Remove`.
- `ProjectionHealth` and `ProjectionHealthHistory`.
- Continuation store pending/orphan state.

Acceptance criteria:

- Audits run under a named scope with configured interval and deadline.
- Audit output is bounded by configured result limits.
- Detected leak, stuck cancellation, outbox terminal failure, oversized
  orphan buffer, and stale inbox registry entry produce observable
  evidence.
- Audits can be disabled per environment without deleting the code path.

Test cases:

- Unit happy path: audit over healthy registries emits no findings.
- Unit negative path: stale inbox, leaked worker, outbox terminal
  failure, and oversized orphan buffer produce separate findings.
- Unit edge case: nil registries, empty sessions, and boards without
  durable outbox produce warnings but no panic.
- Unit race: audit runs while sessions are added and removed; no data
  races under `go test -race`.
- Unit deadlock: audit never holds registry locks while calling into
  board projection or continuation store.
- Integration with mockery: mock scope and telemetry sink; assert audit
  scheduling, cancellation, and bounded result emission.
- E2E with mockery: simulate a long-running session with controlled
  leaks and assert operator-facing health surfaces them.
- Simulated usage: run audits during a stress test with many short-lived
  sessions and assert memory does not grow after sessions close.

### 12.7 Phase 6 - Telemetry, Skills, and Operator Runbooks

Goal: make operational state queryable through metrics, skills, and
board artifacts without log scraping.

#### 12.7.1 Implement the telemetry catalog

Description: Add a metrics abstraction and instrument board mutations,
delta emission, inbox delivery, validator dispatch, service dispatch,
continuation recovery, cancellation, boot, recovery, outbox projection,
and resource audits according to §8. The abstraction must be optional
and nil-safe so tests and local tools do not require a metrics backend.

File references:

- `core/claims/board.go`
- `core/claims/board_lifecycle.go`
- `core/claims/board_durable.go`
- `core/claims/inbox.go`
- `core/claims/outbox.go`
- `core/boot/operations.go`
- `agents/shared/claims_intake.go`
- `agents/shared/consult_continuations.go`
- planned file: `core/claims/telemetry.go`

Existing APIs and integration points:

- Board mutation sites and notification errors.
- `DeltaPublisher.PublishDelta`.
- `ClaimsInbox.DeliveredByClass` and overflow counters.
- `ProjectionHealth`.
- Boot phase commit methods.
- Continuation store recovery and cancellation methods.

Acceptance criteria:

- Every counter/gauge/histogram listed in §8.1 either exists or is
  explicitly mapped to a consolidated replacement with the same
  information.
- Metrics labels are bounded cardinality and do not include raw
  descriptions, artifact content, or unbounded error strings.
- Metrics are emitted after durable state changes, not before.
- Metrics emission failure cannot fail a board mutation.
- Tests can replace telemetry with a mockery-generated sink.

Test cases:

- Unit happy path: board lifecycle emits transition counters and
  duration histograms.
- Unit negative path: telemetry sink returns error; board mutation still
  commits and records a bounded notification error only if configured.
- Unit edge case: empty participant category and unknown action type
  normalize to bounded labels.
- Unit race: many goroutines emit telemetry concurrently with no data
  races.
- Unit deadlock: telemetry sink callbacks cannot call back into board
  mutation under held locks.
- Integration with mockery: mock telemetry sink, delta bus, and
  projector; assert metrics match actual committed sequence counts.
- E2E with mockery: simulate boot failure, validation failure,
  cancellation, recovery, and outbox lag; assert operator dashboards
  can be built from emitted metrics.
- Simulated usage: run a multi-session workload and verify per-session
  gauges converge to zero after shutdown.

#### 12.7.2 Add operator skills and repair commands

Description: Expand operator-facing skills so boot health,
cancellation state, recovery state, queue health, and audit findings
are queryable and repairable through the same skills framework that
already exposes projection health.

File references:

- `core/claims/skills.go`
- `core/claims/skills_carry_forward.go`
- `core/claims/skills_context_queries.go`
- `agents/archivalist/skills_core.go`
- `agents/engineer/tool_policy.go`
- `agents/archivalist/tool_policy.go`
- planned file: `core/claims/skills_operations.go`

Existing APIs and integration points:

- `claims.ProjectionHealthSkill`.
- Existing board provider skill integration.
- `query_claims_board`, traversal skills, and context query skills.
- Tool policy allowlists for agents that may use claims introspection
  and repair.

Acceptance criteria:

- Operators can query boot phase status, projection health, cancellation
  status, recovery status, queue health, and resource audit findings.
- Repair commands support dry-run by default and require explicit
  non-dry-run selection for state-changing repairs.
- Repair results are committed as report testaments when requested.
- Tool policies allow read-only inspection broadly but restrict mutating
  repair operations.

Test cases:

- Unit happy path: each skill validates parameters and returns a bounded
  structured result.
- Unit negative path: unknown session, missing board, invalid phase, and
  repair without dry-run authorization return explicit errors.
- Unit edge case: legacy no-WAL session returns fallback guidance rather
  than pretending exact replay is available.
- Unit race: skill query runs while board mutates and returns a
  consistent projection snapshot.
- Unit deadlock: skill execution does not hold board locks while
  invoking projectors or repair routines.
- Integration with mockery: mock board provider, telemetry sink, and
  projector; assert dry-run and apply modes call the correct surfaces.
- E2E with mockery: simulate an operator detecting projection lag,
  running dry-run repair, applying repair, and receiving a report
  testament.
- Simulated usage: issue claims, force failures, query operations
  skills from archivalist policy, and verify outputs include file/claim
  references needed by the runbooks.

#### 12.7.3 Validate runbooks against simulated incidents

Description: Turn the runbooks in §11 into executable scenario tests.
Each runbook must have at least one test that creates the symptom,
performs the documented investigation query, applies the documented
recovery path, and asserts final board state.

File references:

- `docs/CLAIMS_OPERATIONS.md`
- `core/boot/operations_test.go`
- `core/claims/outbox_projectors_test.go`
- `core/claims/cancellation_test.go` (planned)
- `agents/shared/consult_continuations_test.go`
- planned file: `core/claims/operations_runbook_test.go`

Existing APIs and integration points:

- Boot phase commit APIs.
- `ProjectionHealth` and projection repair skills.
- Cancellation APIs from phase 4.
- Continuation recovery APIs.
- Inboxes and mocked delta bus for subscription overflow.

Acceptance criteria:

- Every runbook symptom has a matching test scenario.
- Each scenario asserts the operator can discover the affected claim ID,
  participant ID, queue, phase, or projection record without logs.
- Recovery actions leave durable evidence.
- Scenario tests avoid real external services by using mockery
  integration/e2e doubles.

Test cases:

- Unit: parse runbook scenario descriptors and verify every runbook has
  an executable scenario.
- Unit negative path: missing runbook scenario fails the test.
- Integration with mockery: boot failure, stuck cancellation, validator
  backpressure, crash recovery, and delta subscription overflow each run
  with mocked dependencies.
- E2E with mockery: complete the full incident loop for each runbook:
  symptom, investigation, recovery, final verification.
- Race/deadlock: run scenarios in parallel sessions and assert registry
  isolation and no cross-session contamination.
- Simulated usage: randomly interleave incident scenarios with normal
  claim traffic and verify operator queries remain bounded and accurate.

### 12.8 Phase 7 - Static Enforcement and CI Gates

Goal: make the non-negotiable invariants enforceable where possible.

#### 12.8.1 Add claims operations analyzers

Description: Extend the in-tree analyzer suite with claims-specific
checks for bare goroutines, magic queue capacities, missing
determinism declarations, missing artifact datatypes, broad topic
subscriptions, and direct infrastructure outcomes that bypass claims
evidence.

File references:

- `cmd/sylk-lint/main.go`
- `cmd/nogo/main.go`
- `core/ci/analyzers/nodirectexec/analyzer.go`
- new planned files under `core/ci/analyzers/claimsops/`
- new planned testdata under
  `core/ci/analyzers/claimsops/testdata/src/`

Existing APIs and integration points:

- `go/analysis` multichecker in `cmd/sylk-lint`.
- Existing `nogo` analyzer for raw goroutines.
- Existing `nodirectexec` analyzer pattern and testdata layout.
- Claims APIs that must be detected:
  `ScopeProvider.Go`, `RegisterValidator` (planned),
  `NewClaimsInbox`, topic helper calls, queue constructors, and
  artifact construction.

Acceptance criteria:

- `cmd/sylk-lint` includes claims operations analyzers.
- Analyzer diagnostics include a specific fix direction and file
  position.
- Tests cover clean and leaky examples for every analyzer.
- Analyzer allowlists are narrow and documented in code comments.
- Suppression requires a local comment and central registry entry.

Test cases:

- Unit happy path: clean testdata with scoped goroutines, named budgets,
  registered determinism, and narrow topics passes.
- Unit negative path: raw `go`, anonymous queue literal, broad
  `claims.*` subscription, missing determinism, missing artifact data
  type, and direct Go-error-only infrastructure outcome fail.
- Unit edge case: tests and approved low-level packages are exempt only
  where documented.
- Integration with mockery: not required for analyzer unit tests, but
  integration tests that verify analyzer output against generated mocks
  must use mockery-generated mocks to avoid hand-written false
  positives.
- E2E with mockery: run `go run ./cmd/sylk-lint ./...` after mock
  generation to ensure generated mocks do not trigger claimsops
  diagnostics.
- Race/deadlock: analyzers are pure compile-time passes and must not
  spawn goroutines or block on external state.
- Simulated usage: introduce a temporary test fixture for each banned
  pattern and assert CI would fail.

#### 12.8.2 Enforce lifecycle and delta partitions

Description: Keep action types, lifecycle statuses, canonical delta
actions, inbox activation classes, and system-internal actions in
closed, tested partitions. New action types must explicitly decide
whether they wake agents, remain system-internal, require delivery,
and may complete expectations.

File references:

- `core/claims/types.go`
- `core/claims/canonical_delta.go`
- `core/claims/inbox.go`
- `core/claims/system_internal_action_test.go`
- `core/claims/canonical_delta_test.go`
- `core/claims/topics_test.go`

Existing APIs and integration points:

- `IsSystemInternalAction`.
- `AgentActivationActionTypes`.
- `KnownDeltaActions`.
- `DeltaActionRequiresDelivery`.
- `DeltaActionMayCompleteExpectedWork`.
- `DeltaClass`.
- `InboxPatternsFor`.

Acceptance criteria:

- Every `ActionType` is either activation-bearing or system-internal.
- Every claim lifecycle status has exactly one canonical delta action.
- Every testament lifecycle status has exactly one canonical delta
  action.
- Every canonical action has an explicit delivery and expectation
  completion classification.
- Inbox role subscriptions do not wake agents for system-internal
  actions.

Test cases:

- Unit happy path: partitions cover all known action types and lifecycle
  statuses.
- Unit negative path: adding a synthetic action type without partition
  classification fails tests.
- Unit edge case: observer role receives display-only context topics
  without waking agent inference.
- Unit race: concurrent inbox matching of canonical deltas cannot mutate
  shared partition tables.
- Integration with mockery: mock delta bus and publish all known
  actions; assert only activation-bearing actions reach subject
  `ProcessEntry`.
- E2E with mockery: post boot, activation, checkpoint, testament,
  consultation, task, corrective, and guardian check actions; assert
  agent wake behavior matches the partition table.
- Simulated usage: replay a WAL containing old legacy deltas and assert
  tolerant observers accept them without reclassifying them as agent
  wake events.

#### 12.8.3 Add CI commands and documentation gates

Description: Add CI-visible commands that run claims operation tests,
race tests for claims-critical packages, mock generation checks, and
docs traceability checks.

File references:

- `Makefile`
- `.mockery.yaml`
- `cmd/sylk-lint/main.go`
- `docs/CLAIMS_OPERATIONS.md`
- planned CI scripts under `scripts/ci/`

Existing APIs and integration points:

- Existing `make test`.
- Existing `go test ./...`.
- Existing `go run ./cmd/sylk-lint ./...`.
- Mockery configuration.

Acceptance criteria:

- CI has a command for normal tests, claims-critical race tests,
  mockery drift checks, and claims operations lint: `make
  claims-infra-ci`.
- Mockery drift check fails if generated mocks are stale.
- Documentation traceability check fails if a required phase lacks
  acceptance criteria or tests.
- CI commands do not require network once dependencies are present.

Test cases:

- Unit: documentation checker validates phase headings, item headings,
  file references, acceptance criteria, and test cases.
- Unit negative path: fixture missing integration/e2e mockery language
  fails.
- Integration with mockery: regenerate mocks and run tests that import
  them.
- E2E with mockery: run the complete claims operations CI command set
  against a temp workspace copy with generated mocks.
- Race/deadlock: race tests run on `core/claims`, `core/boot`,
  `agents/shared`, and UI bridge claims tests.
- Simulated usage: pre-submit script runs the same commands a developer
  would run before merging a claims operations change.

### 12.9 Phase 8 - UI, Agent, and Rollout Completion

Goal: finish migration from design-level claims operations to a
production rollout with UI visibility, agent behavior, compatibility
switches, and rollback safety.

#### 12.9.1 Make UI operations state claims-driven

Description: Ensure the UI displays boot, recovery, cancellation,
projection health, queue overflow, and participant readiness from
claims-derived deltas and board projections, not ad hoc status writes.
The UI bridge already observes claims through `WireClaimsIntake`; this
phase completes operational surfaces and removes redundant non-claims
paths where they overlap.

File references:

- `ui/bridge/claims.go`
- `ui/agent/model.go`
- `docs/CLAIMS_UI.md`
- `core/claims/deltas.go`
- `core/claims/canonical_delta.go`
- `core/claims/topics.go`

Existing APIs and integration points:

- `ClaimsBridge.startClaimsIntake`.
- `ClaimsBridge.processClaimsEntry`.
- `ClaimsBridge.processBoardMutationDelta`.
- `claims.RoleObserver | claims.RoleAuditor`.
- `ClaimContextDelta`, `TestamentContextDelta`, and canonical lifecycle
  deltas.

Acceptance criteria:

- UI has a claims-derived representation for boot phase, participant
  readiness, cancellation progress, recovery state, and projection
  health warnings.
- UI observer does not wake agent inference.
- UI rows update deterministically by claim ID, testament ID,
  accumulator ID, and delta sequence.
- Free-floating presentation testaments remain visible through the
  board delta watch until canonical lifecycle fully covers them.

Test cases:

- Unit happy path: UI bridge handles canonical claim/testament,
  validation, context, boot, and cancellation deltas into stable
  messages.
- Unit negative path: stale session delta is ignored and does not mutate
  active UI state.
- Unit edge case: testament context arrives before testament ID and is
  rebound by accumulator ID after flush.
- Unit race: UI receives context, testament, validation, and terminal
  claim deltas out of order; sequence handling produces stable final
  state.
- Unit deadlock: UI bridge callback never blocks the claims bus while
  holding bridge locks.
- Integration with mockery: mock bus, scope, and Tea program; assert UI
  messages emitted for boot failure, stuck cancellation, and projection
  lag.
- E2E with mockery: run a headless TUI bridge against mocked claims
  traffic and assert visible operational state without a real terminal.
- Simulated usage: user interrupts a multi-agent operation and watches
  all claim rows close from cancellation deltas.

#### 12.9.2 Complete agent adoption and compatibility cleanup

Description: Ensure every agent uses claims intake, continuation store,
expected tool validation, and claims-derived operational state
consistently. Remove or gate legacy fire-and-forget paths only after
the claims path has e2e coverage.

File references:

- `agents/architect/*`
- `agents/engineer/*`
- `agents/orchestrator/*`
- `agents/guardian/*`
- `agents/inspector/*`
- `agents/tester/*`
- `agents/designer/*`
- `agents/librarian/*`
- `agents/archivalist/*`
- `agents/academic/*`
- `agents/shared/claims_intake.go`
- `agents/shared/cross_pipeline_skills.go`
- `agents/shared/consult_continuations.go`

Existing APIs and integration points:

- Agent constructors that call `shared.WireClaimsIntake`.
- Per-agent `processClaimsEntry` implementations.
- `shared.NewClaimsEntryAccumulator`.
- `shared.WithContinuationStore` and
  `ContinuationStoreFromContext`.
- `consult_peer`, `challenge_peer`, and `await_consults` skill paths.

Acceptance criteria:

- Every always-hot and on-demand agent registers claims intake with
  identity and scope when it can invoke LLM provider dispatch.
- Peer consultation and challenge responses route through
  continuation store rather than fresh inference.
- Expected tool validation runs through claims expected-tool execution
  and posts evidence artifacts.
- Legacy direct bus paths are either removed, explicitly marked
  compatibility-only, or gated behind rollout flags.
- Agent shutdown stops continuation stores and removes inbox registry
  entries.

Test cases:

- Unit happy path: each agent constructor wires inbox, continuation
  store, identity, factory, and scope.
- Unit negative path: missing identity or scope causes
  `WireClaimsIntake` to return nil for non-observer agent roles.
- Unit edge case: observer-only role may omit identity without blocking
  UI intake.
- Unit race: peer response and expectation registration can happen in
  either order and still resume exactly once.
- Unit deadlock: agent `processClaimsEntry` cannot block bus delivery
  because it always dispatches through scope.
- Integration with mockery: mock bus, scope, provider gateway, and
  continuation resume function; assert consult/challenge flows use
  claims deltas end to end.
- E2E with mockery: simulate architect -> librarian -> engineer ->
  tester consultation chain and verify all handoffs, testaments, and
  validations are board-visible.
- Simulated usage: run a user prompt decomposition through mocked
  agents and assert no non-claims display writes are needed to explain
  progress.

#### 12.9.3 Roll out with feature gates, shadow mode, and rollback

Description: Release claims operations incrementally. Use rollout flags
for durable outbox, knowledge mirror, service dispatch, programmatic
validators, cancellation propagation, telemetry exporters, and UI
operational panels. Shadow mode must compare new projections against
existing behavior where possible without changing user-visible state.

File references:

- `core/claims/rollout.go`
- `core/claims/rollout_test.go`
- `core/claims/outbox_health.go`
- `core/claims/projectors.go`
- `cmd/tui.go`
- planned file: `core/claims/operations_rollout.go`

Existing APIs and integration points:

- `claims.RolloutConfig` and `CurrentRolloutConfig`.
- `ClaimsBoard.RolloutConfig`.
- `DurableBoard.ProjectionHealth`, including feature flags and shadow
  diffs.
- `DisableOutbox` and projector rollout flags.

Acceptance criteria:

- Every new operational subsystem has an enable, disable, and shadow
  mode where shadow mode is meaningful.
- Rollback disables new projection/dispatch behavior without deleting
  WAL, outbox, or board evidence.
- Feature flag state appears in projection health.
- Shadow diffs are bounded and actionable.
- Rollout documentation names the default state for local dev, CI,
  staging, and production.

Test cases:

- Unit happy path: rollout config normalizes defaults and exposes
  feature flags in projection health.
- Unit negative path: invalid rollout value returns a typed config
  error or falls back according to documented policy.
- Unit edge case: disabled outbox preserves board mutations but reports
  warning in projection health.
- Unit race: rollout snapshot is immutable per board and cannot change
  halfway through a mutation.
- Unit deadlock: shadow projectors cannot block primary lifecycle
  commits.
- Integration with mockery: mock primary and shadow projectors with
  divergent outputs; assert shadow diffs are recorded and bounded.
- E2E with mockery: run the same scenario with feature disabled,
  shadow, and enabled; assert rollback returns to disabled behavior
  without losing durable evidence.
- Simulated usage: staging workload runs with service dispatch shadowed,
  compares service testaments to legacy outcomes, then enables service
  dispatch after zero critical diffs.

#### 12.9.4 Final production readiness review

Description: Treat production readiness as an evidence-bearing claims
cycle. The final review claim must attach test reports, race reports,
lint reports, runbook scenario reports, performance measurements, and
rollout/rollback evidence. The claim is not satisfied until required
validations pass.

File references:

- `docs/CLAIMS_OPERATIONS.md`
- `docs/PERFORMANCE.md`
- `docs/BUS_OBSERVABILITY.md`
- `docs/FABRIC_OBSERVABILITY.md`
- `core/claims/*_test.go`
- `core/boot/*_test.go`
- `agents/shared/*_test.go`
- `ui/bridge/*_test.go`
- `scripts/ci/*`

Existing APIs and integration points:

- `ClaimsBoard.GenerateClaimAction`, `GenerateTestamentAction`, and
  validation lifecycle APIs for recording the readiness review itself.
- Operator skills for projection health and repair.
- CI commands from phase 7.
- Telemetry and runbook scenarios from phase 6.

Acceptance criteria:

- All phase acceptance criteria are linked to passing evidence.
- Required unit, integration, e2e, race, lint, runbook, and performance
  tests pass.
- Performance budgets in §7 are measured and deviations are explained
  with corrective claims.
- Open risks are represented as claims with owners and deadlines.
- Rollback is tested from enabled state to disabled state without data
  loss.

Test cases:

- Unit: readiness report builder rejects missing phase evidence.
- Unit negative path: failed race/lint/runbook evidence prevents
  satisfaction.
- Unit edge case: waived optional validation must include a documented
  waiver artifact and cannot hide required failures.
- Integration with mockery: mock CI evidence providers and board
  submission; assert readiness claim carries all required artifacts.
- E2E with mockery: run the complete simulated production scenario,
  collect evidence, submit readiness testament, and validate it.
- Race/deadlock: readiness review runs while projection health is being
  queried and outbox repair is idle; no lock inversions.
- Simulated usage: operator reads the final readiness claim, follows
  rollback instructions in a mock deployment, and confirms board state
  remains replayable.

## 13. Final Operational Statement

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
