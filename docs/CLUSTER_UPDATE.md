# CLUSTER_UPDATE.md — Maximally Correct Sylk Substrate Implementation Plan

> Complete, no-concessions implementation plan covering all 30 phases of `docs/CLUSTER.md` and all 14 phases of `docs/CLAIMS.md`. No deferrals. No "realistic" framing. Every system gets the maximally correct, maximally robust, maximally performant, maximally agentic implementation. Dependency-ordered. Each item carries acceptance criteria, race-condition tests, and adversarial validation.

## 0. Stance and Invariants

**Stance.** The substrate is the system. Sylk's correctness is the substrate's correctness. Every micro-gap closes. Every adversarial scenario has a soundness theorem and a failing-then-passing test. There are no "good enough" components.

**Hard invariants** (any violation halts the relevant track until resolved):

1. **Causal soundness.** No claim, transaction, or workflow observes an effect whose cause is unknown to it. The Causal Merkle DAG is the ground truth; HLC is the projection.
2. **Single source of truth per subject.** A subject has exactly one Raft group at any term. Cross-namespace effects route through MultiNamespaceTx with two-level 2PC.
3. **Identity and frame pinning.** Every signature is tied to a (svid_serial, raft_term, hlc_window) triple. SVID rotation, term changes, and time leaps invalidate in-flight assumptions explicitly.
4. **Async by default.** Observational, replication, and best-effort writes never block agent hot paths. Tracked goroutines + bounded queues + drop counters.
5. **Pipeline lifecycle ⊥ disk commit.** A pipeline pod's VFS lives until commit-to-disk acknowledges; rejection or correction is not terminal.
6. **Architect authors corrective actions.** Orchestrator dispatches and monitors; the architect agent is the canonical author of remediation claims.
7. **Agents own their actions.** Forest, Fabric, Guardian advise; no non-agent system authors claims, evaluates validations, or gates transitions. Influence flows via prevalence + specificity + trust, not authority.
8. **Progressive context disclosure.** Every claim envelope is default-narrow. Wider contexts are explicitly composed.

---

## 1. Track Layout (dependency-ordered, fully parallel where independent)

The substrate has 30 CLUSTER phases and 14 CLAIMS phases. The tracks below execute the union. Phase IDs preserve original numbering. Tracks A–N run in dependency order; tracks marked `‖` may parallelize against the prior track once its acceptance gates clear.

| Track | Phases | Theme |
|---|---|---|
| A | C-0, C-1, C-2 | Identity, time, transport, wire format |
| B ‖ A | C-3, C-4 | Causal DAG, namespaces, Raft groups |
| C ‖ B | C-5, C-6 | Membership, federation control plane |
| D | C-7, C-8 | Storage, retention, deletion |
| E ‖ D | C-9, C-10 | State machines, replication, snapshots |
| F | C-11 | Sylk-native SQLite-compatible engine + `.sshm` |
| G ‖ F | C-12, C-13 | Pub/sub, KV, queues |
| H | C-14, C-15 | Object store, blobs, content-addressing |
| I ‖ H | C-16, C-17 | Quotas, multi-tenancy, isolation |
| J | C-18, C-19 | Audit, observability, tracing |
| K ‖ J | C-20, C-21 | Federation runtime, learners, freshness API |
| L | C-22, C-23, C-24 | Operator, topology, SM lifecycle |
| M | C-25, C-26, C-27 | Adaptive transport, SQLite envelope, SQL extensions |
| N | C-28, C-29 | Architectural micro-gaps, workflow + algebraic effects |
| Z (CLAIMS overlay) | CL-0..CL-13 | Claims system on substrate |

CLAIMS phases CL-0 through CL-12 layer onto Tracks A–L as the claims-specific systems mature. CL-13 (substrate integration) drops as the cap once L closes.

---

## 2. Track A — Identity, Time, Transport, Wire Format

### Phase 0 — Project foundations

**Items.**
- 0.1 Build system, repo layout, `pkg/substrate/`, `pkg/wire/`, `pkg/raft/`, `pkg/causal/`, `pkg/identity/`, `pkg/time/`, `pkg/storage/`, `pkg/state/`, `pkg/federation/`, `pkg/agents/`.
- 0.2 Determinism harness: race detector mandatory, chaos injection harness, deterministic simulator (Foundation-DB-style) with fault-injection oracle.
- 0.3 Property test framework (`gopter`-style) wired into every package.
- 0.4 BLAKE3 + Ed25519 + X25519 vendored crypto with constant-time guarantees + side-channel test harness.
- 0.5 Fuzzing infrastructure: native `go test -fuzz`, structured fuzzing with `go-fuzz-headers`, corpus seeding from production traces.

**Acceptance.** `go test -race -count=10 ./...` clean; deterministic sim reproduces every recorded trace bit-for-bit; fuzz corpora persist across runs; chaos harness can inject node death, network partition, clock skew, byzantine messages.

**Tests.**
- Unit: every public type has a property test.
- Race: `-race -count=100` across the matrix.
- Determinism: same seed → same trace, full closure.
- Adversarial: fuzz with malicious bytes; expected: no panic, no UB, no allocator misuse.

### Phase 1 — Identity (SPIFFE SVIDs, Ed25519, key custody)

**Items.**
- 1.1 SPIFFE Workload API client; SVID issuance with Ed25519 keys; trust bundle distribution.
- 1.2 SVID rotation protocol (CLUSTER §3.4) with cross-signed transition window; old + new serials co-valid for `2 * max_in_flight_rtt`; verifier checks both serials during overlap; signed transition message in operator group.
- 1.3 Key custody: hardware-backed where available (TPM/Secure Enclave/YubiKey via PIV), software-fallback with mlock + memzero on rotation.
- 1.4 Per-role identity hierarchy: node, pod, agent, session SVIDs; chained signatures with role attestation.
- 1.5 Revocation: short-lived SVIDs (default 1h), CRL distribution via operator-group Raft with strong-read consistency.
- 1.6 Frame pinning: every signed message carries `(svid_serial, raft_term, hlc_window_start, hlc_window_end)`; verifiers reject if any frame is stale beyond grace.

**Acceptance.** Rotation never drops in-flight messages; cross-sign window is provably safe (no message verifies under exactly-one of old/new); revocation propagates to all verifiers within `min(SVID_ttl/4, 5min)`; frame-pinned signatures detect every replay/relay we test.

**Tests.**
- Unit: signing, verification, rotation state machine.
- Integration: 100 rotation cycles with concurrent signing.
- Race: contention on rotation flip with 1000 signers.
- Adversarial: replay across rotation, malicious cross-sign, timing-attack against verification.
- Negative: expired SVID, revoked SVID, wrong-frame SVID, mismatched serial.

### Phase 2 — Time (HLC, bounded uncertainty, leap handling)

**Items.**
- 2.1 HLC with `(physical, logical, node_id)` triple; physical from monotonic clock; logical advances on tie.
- 2.2 NTP/PTP integration; uncertainty interval published with each HLC value.
- 2.3 Leap-second handling: smear (Google-style) at 1ms/s; never reverse logical.
- 2.4 Skew detection: each node tracks its skew vs operator-group consensus; >100ms skew triggers fence.
- 2.5 HLC fence: a node that exceeds skew bound stops issuing HLCs until re-synced; replication groups eject fenced members from quorum.
- 2.6 Cross-domain HLC algebra: `WriteToken{hlc, federation_frontier_id}`; `ObservedAfter(token)` tells a reader whether their view includes the write.
- 2.7 Per-tenant clock budgets to prevent clock-amplification attacks.

**Acceptance.** Clock skew injection (±10s) is contained — operations that would violate causality fail closed, never silently corrupt; smearing across leap seconds preserves monotonicity at logical layer; cross-domain reads are linearizable when WriteToken is present.

**Tests.**
- Unit: HLC arithmetic, monotonicity, tie-breaking.
- Integration: 5-node cluster with random clock skew up to 10s.
- Race: 100 concurrent HLC issuers per node.
- Adversarial: malicious time injection (future, past, NaN-equivalent), leap-second double-strike.
- Negative: unsynced node attempts replication, fence triggers correctly.

### Phase 3 — Transport (QUIC + datagrams + raw UDP+FEC + shmem rings)

**Items.**
- 3.1 QUIC streams (reliable, ordered) for control plane, large payloads, replication.
- 3.2 QUIC datagrams (RFC 9221, unreliable) for membership gossip, telemetry.
- 3.3 Raw UDP + Reed-Solomon FEC for ultra-low-latency claim broadcast (LAN + WAN), with adaptive code rate.
- 3.4 Shared-memory rings (single-node multi-process) — lock-free MPMC, NUMA-aware.
- 3.5 RDMA over RoCEv2 / InfiniBand where available; fallback to QUIC.
- 3.6 Adaptive transport selector: per-destination, per-message-class. Tracks RTT, loss, jitter, throughput, picks transport by EWMA scoring.
- 3.7 Connection multiplexing: one QUIC connection per node-pair; many streams; head-of-line blocking eliminated by datagram fallback.
- 3.8 Backpressure: per-stream credit, per-connection credit, per-node credit.
- 3.9 Path validation: address change triggers cryptographic path validation (no on-path injection).
- 3.10 Cross-cluster RAW: federation frames carry transport hints; receiver may upgrade/downgrade.

**Acceptance.** Single-node IPC < 5µs P50; LAN RPC < 200µs P50; WAN RPC < (RTT + 1ms) P50; transport selector converges to optimal within 10 RTTs of any topology change; FEC recovers from 30% loss with k=8/n=12 code rate.

**Tests.**
- Unit: each transport in isolation; codec correctness.
- Integration: cross-DC clusters, mixed transports, transport switch under load.
- Race: connection establishment under concurrent dial; multiplexed streams.
- Adversarial: malicious peer (QUIC fuzzing), packet injection, MTU exhaustion, amplification attempts.
- Negative: every transport class fails over; black-hole detection within 3 RTTs.

### Phase 4 — Sylk Wire Format (SWF)

**Items.**
- 4.1 SWF spec: 56-byte zero-copy header, body codec; codegen from schema definitions.
- 4.2 Schema-trained zstd dictionaries: per-schema corpus training pipeline, dictionary versioning, dictionary distribution via operator group.
- 4.3 Header CRC32C (hardware-accelerated where available).
- 4.4 Compression-bomb defense, 10-layer (CLUSTER §4.4):
  1. Pre-decompression: declared size bound check.
  2. Schema-derived max: per-message-class hard ceiling.
  3. Library-level limit (zstd `--max-window`).
  4. Streaming with running-size assertion at every chunk.
  5. Ratio sanity (compressed_size : decompressed_size ≥ 1:1000 default; per-class tunable).
  6. Dictionary integrity: BLAKE3 hash of dict matches expected ID.
  7. Decompression timeout (per-class, default 100ms).
  8. Per-tenant decompression budget (token-bucket).
  9. Post-decomp validation: schema validator runs before bytes touch business logic.
  10. Arena cap: decompressed bytes go into a fixed arena that's poisoned on overflow.
- 4.5 Three-layer dedupe: `event_id` (primary, BLAKE3 of content), `payload_fingerprint` (faster prefix match), `idem_key` (caller-supplied for retries).
- 4.6 Frame-pinned signatures embedded in header.
- 4.7 Codegen: from schema to Go struct + encoder + decoder + zero-copy view; generated code is deterministic and reviewable.
- 4.8 Versioning: schema versions are first-class; backwards/forwards-compatible field rules (additive only, never reorder, never repurpose).

**Acceptance.** Encode/decode roundtrip is identity; dictionary compression beats general zstd by ≥10x on representative corpus; bomb defense rejects every test bomb in <10ms; codegen output passes `go vet`, `staticcheck`, `gosec` clean.

**Tests.**
- Unit: codec roundtrip property test for every schema; CRC32C hardware-vs-software identity.
- Race: dictionary rotation under concurrent encode/decode.
- Adversarial: 1000+ bomb variants (zip-bombs adapted to zstd, dict poisoning, frame-pinning bypass).
- Negative: malformed headers, wrong CRC, mismatched dict ID, oversized declared size, undersized declared size with oversized actual.

---

## 3. Track B — Causal DAG, Namespaces, Raft Groups

### Phase 5 — Causal Merkle DAG (per-subject)

**Items.**
- 5.1 Per-subject append-only DAG: each event has `(parents, content_hash, hlc, signer, signature)`; content addressed by BLAKE3.
- 5.2 Causal closure: an event is committed only when all parents are committed in the same group.
- 5.3 Merkle commitment: per-subject root advances atomically per Raft entry.
- 5.4 Frontier tracking: per-replica frontier set, gossiped, used for sync.
- 5.5 Anti-entropy: Merkle-tree diffing for catch-up; bloom + IBLT for delta computation.
- 5.6 Cycle prevention: parents must precede in HLC + must be in same group's history.
- 5.7 Dedup: by content hash; idempotent appends.

**Acceptance.** No replica ever observes an effect without its causes; anti-entropy converges in O(log n) rounds; Merkle root computation is O(amortized 1) per append.

**Tests.**
- Unit: DAG operations, frontier algebra, merge functions.
- Integration: 5-node group, 10K events, random partitions.
- Race: concurrent appenders; no cycles; Merkle root consistent.
- Adversarial: byzantine parent claims, fork attempts, hash collisions (forced by test).
- Negative: missing parent, future-timestamped event, mismatched signer.

### Phase 6 — Namespaces, MultiNamespaceTx

**Items.**
- 6.1 Namespace as the unit of multi-tenancy and Raft-group placement.
- 6.2 Per-namespace ACLs derived from SVIDs.
- 6.3 MultiNamespaceTx: cross-namespace 2PC with explicit prepare/commit/abort.
- 6.4 Two-level 2PC for cross-namespace + cross-engine (CLUSTER §6.6, §6.7): outer 2PC across namespaces, inner 2PC across engines (e.g., DAG + SQL). Coordinator state replicated in operator group.
- 6.5 Abort safety: on abort, every engine in every namespace cleans up; no partial visibility (CLUSTER §6.6).
- 6.6 Deadlock detection: per-coordinator wait-for graph; victim selection by transaction age + priority.
- 6.7 Recovery: coordinator failure handled by operator group; in-doubt transactions get authoritative resolution.

**Acceptance.** No partial commits ever observed by any reader; abort cleanup is total; coordinator failure during prepare/commit always resolves consistently.

**Tests.**
- Unit: 2PC state machine, abort cleanup.
- Integration: cross-namespace transaction across 5 nodes; coordinator failure injected at every state.
- Race: concurrent transactions on overlapping subjects.
- Adversarial: byzantine participant claims wrong vote, byzantine coordinator attempts double-commit.
- Negative: partition during prepare, partition during commit, abort during commit message.

### Phase 7 — Multi-Raft (operator, topology, per-namespace, learners)

**Items.**
- 7.1 Operator group: cluster-wide config, federation membership, SVID trust roots, Raft topology.
- 7.2 Topology group: namespace-to-Raft-group placement, learner assignments.
- 7.3 Per-namespace groups: data Raft groups, optionally split per subject prefix.
- 7.4 Learners: read-only replicas with bounded freshness; (CLUSTER §20.8) freshness API: `freshness_at(replica) → hlc_lag, raft_index_lag`.
- 7.5 Cross-group routing: requests routed by subject; cross-group ops via MultiNamespaceTx.
- 7.6 Group resharding: split/merge with online rebalance, no downtime.
- 7.7 Leader leases: time-bounded lease via HLC fence; prevents stale-leader reads.
- 7.8 Pre-vote and CheckQuorum (Raft optimizations) mandatory.
- 7.9 Term-bound signed Raft entries (CLUSTER §0 invariant): every entry signed with `(svid_serial, term)` to prevent leader replay across terms.

**Acceptance.** Linearizable reads under all leader changes; learner freshness is queryable and accurate; resharding never drops or duplicates entries.

**Tests.**
- Unit: Raft state machine (re-derive from existing libraries with our additions: term-bound sigs, HLC fences).
- Integration: 7-node group, kill-and-revive cycles, partitions, byzantine leader attempts.
- Race: leader election under concurrent log appends.
- Adversarial: malicious follower forges votes (must fail signature check), malicious leader tries cross-term replay.
- Negative: minority partition, majority partition, learner with stale view returns correct freshness.

---

## 4. Track C — Membership, Federation Control Plane

### Phase 8 — SWIM++ Membership

**Items.**
- 8.1 SWIM with hierarchical levels: node, pod, agent, session.
- 8.2 Indirect probes, suspect timeout, K-confirm.
- 8.3 Lifeguard improvements (jittered probes, dampen flapping).
- 8.4 Encrypted gossip with per-message authentication (Ed25519 + per-pair X25519 ECDH for confidentiality).
- 8.5 Membership integrated with operator group: SWIM detects, operator group ratifies (terminal removals require Raft).
- 8.6 Cross-DC gossip with bandwidth-aware fanout reduction.
- 8.7 Hierarchical liveness: a node is alive ⇒ its pods are reachable; a pod is alive ⇒ its agents are alive; etc.

**Acceptance.** Failure detection P99 < 3 RTTs; flapping rate < 0.1% under chaos; cross-DC bandwidth ≤ 5% of intra-DC.

**Tests.**
- Unit: SWIM state transitions, probe scheduling.
- Integration: 100-node cluster with random partitions, asymmetric failures.
- Race: concurrent membership events.
- Adversarial: byzantine node claims false-suspect of others; malicious gossip injection (must fail auth).
- Negative: split-brain detection, gray failure (latency without packet loss), message reordering.

### Phase 9 — Federation Control Plane (BFT)

**Items.**
- 9.1 Federation as a BFT-replicated control plane spanning multiple Sylk clusters.
- 9.2 BFT consensus (HotStuff or Tendermint-style) for federation membership and cross-cluster policy.
- 9.3 Per-cluster operator group reports its frontier to federation.
- 9.4 Cross-cluster routing: a federation-aware request is signed by source cluster, verified by destination cluster.
- 9.5 (CLUSTER §20.7) Federation backpressure cascade: DCTCP-style ECN; clusters report cross-cluster congestion to federation; federation propagates to upstream clusters; agents see backpressure as quota reduction with reason annotation.
- 9.6 Cross-domain HLC algebra (Phase 2.6) anchored in federation frontier.

**Acceptance.** BFT control plane survives `f` byzantine clusters out of `3f+1`; backpressure propagates within 1 RTT through federation; cross-cluster reads are linearizable when WriteToken is presented.

**Tests.**
- Unit: BFT state machine, view-change protocol.
- Integration: 4-cluster federation, byzantine cluster injection (1 of 4).
- Race: concurrent federation membership changes.
- Adversarial: byzantine cluster signs conflicting messages; partition isolates malicious cluster.
- Negative: cluster departure mid-transaction; federation quorum loss recovery.

---

## 5. Track D — Storage, Retention, Deletion

### Phase 10 — Storage Engine (segments, compaction, encryption)

**Items.**
- 10.1 Append-only segment files; per-segment Merkle root; immutable after seal.
- 10.2 Tiered compaction (size-tiered + leveled); compaction is verifiable (output Merkle covers input Merkle inputs).
- 10.3 Per-segment AEAD encryption (XChaCha20-Poly1305); per-namespace key, derived from cluster KEK.
- 10.4 Key rotation: rolling re-encryption without downtime; segments tagged with key generation.
- 10.5 Page cache: per-namespace, NUMA-aware, sized by quota.
- 10.6 Direct I/O for cold reads; mmap for hot.
- 10.7 Erasure coding (Reed-Solomon 10+4) for cold tier; transparent to readers.
- 10.8 Local SSD + remote object store tiers; auto-promotion/demotion by access pattern.

**Acceptance.** Compaction never loses data (Merkle audit passes); encryption rotation never blocks writes; tier transitions are invisible to readers.

**Tests.**
- Unit: segment format, compaction, encryption.
- Integration: TB-scale write/read/compact cycle.
- Race: compaction during writes; key rotation during compaction.
- Adversarial: bit-flip on disk (must detect via Merkle); malformed segment from peer; oracle attack on encryption.
- Negative: disk full, partial write (torn), corrupted segment, rotation failure mid-flight.

### Phase 11 — Retention and Deletion (3 levels)

**Items.**
- 11.1 (CLUSTER §7.6) Three-level deletion semantics:
  - **Retention**: TTL-based aging; subjects keep latest-N or latest-by-time; old data evicted from page cache + cold-tiered.
  - **Soft delete**: tombstone written; subject becomes invisible but data retained for audit window.
  - **Hard delete**: cryptographic erasure (key destruction) + segment rewrite to remove tombstoned entries.
- 11.2 Per-namespace retention policy.
- 11.3 Tombstone propagation through replication; learners must apply tombstones before serving reads.
- 11.4 Hard-delete cascade: cross-namespace dependencies tracked; hard delete blocked if other namespaces still reference.
- 11.5 GDPR/right-to-erasure compatible (cryptographic erasure is sufficient under most jurisdictions; users handle their own compliance).

**Acceptance.** Tombstones converge within bounded freshness; hard delete is provably irrecoverable (key zeroed, segments rewritten, audit logged).

**Tests.**
- Unit: tombstone semantics, key-erasure correctness.
- Integration: cross-namespace cascade with conflicting deletes.
- Race: concurrent soft-delete and re-add.
- Adversarial: attempt to read after hard delete (must fail); replay of pre-delete state (must fail).
- Negative: failed key-erase mid-flight (rollback); cascade conflict; partial replication.

---

## 6. Track E — State Machines, Replication, Snapshots

### Phase 12 — Tiered State Machines (DSL → Native Go → Optional WASM)

**Items.**
- 12.1 (CLUSTER §31.1) Tier 1: DSL codegen. Declarative SM specs compile to deterministic Go code; reviewed in PR; this is the default tier.
- 12.2 Tier 2: Native Go state machines. Hand-written for performance-critical SMs; reproducible build provenance (Go version, deps locked, BLAKE3 of binary checked in to operator group).
- 12.3 Tier 3 (opt-in, feature-flagged): WASM SMs for sandboxed third-party extensions. Disabled by default. WASM runtime is wasmtime with deterministic mode (no host clock, no PRNG without seed).
- 12.4 SM invocation context: read-only view of subject's DAG up to current Merkle root; per-call resource budget (CPU, memory, time).
- 12.5 SM determinism harness: every SM has a determinism test that runs the SM 1000 times against the same input and asserts identical output bytes.
- 12.6 (CLUSTER §24.7) SM coexistence: multiple SM versions can run side-by-side during upgrade; transactions pin SM version; long-running transactions hold the version pin until commit.

**Acceptance.** SMs are bit-deterministic; resource budgets are enforced; multiple versions coexist correctly.

**Tests.**
- Unit: each tier in isolation; determinism harness.
- Integration: rolling upgrade with active long transactions.
- Race: SM dispatch under concurrent invocations.
- Adversarial: malicious SM attempts non-determinism (clock, PRNG, network — all blocked); resource exhaustion (must hit budget).
- Negative: SM crashes mid-execution; SM returns malformed output; SM version mismatch.

### Phase 13 — Replication, Snapshots, Catch-up

**Items.**
- 13.1 Synchronous replication within Raft group; quorum acks before commit.
- 13.2 Asynchronous replication to learners with bounded lag.
- 13.3 Snapshot generation: incremental, content-addressed, deduplicated across snapshots.
- 13.4 Snapshot application: streaming, resumable, cryptographically verified.
- 13.5 Anti-entropy catch-up using DAG frontier diffing (Phase 5).
- 13.6 Cross-region replication via federation channels.

**Acceptance.** Catch-up completes for any join within bounded time given throughput; snapshot apply is streaming (no need to fit in memory); cross-region replication respects federation backpressure.

**Tests.**
- Unit: snapshot format, dedup, streaming apply.
- Integration: 100-node group, churn 30%, catch-up correctness.
- Race: snapshot during writes.
- Adversarial: corrupted snapshot from peer (rejected); malicious snapshot claims wrong Merkle root.
- Negative: snapshot apply interrupted, resume; partial snapshot.

---

## 7. Track F — Sylk-Native SQLite-Compatible Engine

### Phase 14 — Sylk SQL Engine (replacing SQLite, `.sshm` sidecar)

**Items.**
- 14.1 (CLUSTER §0, §11.8) Pure-Go re-implementation of SQLite-compatible engine (~25K LOC). Drawing from `../turso` learnings but Go-native, substrate-integrated.
- 14.2 SQL parser, planner, executor (full SQLite syntax + Sylk extensions).
- 14.3 MVCC: snapshot isolation by default; serializable on opt-in via predicate locks.
- 14.4 WAL: substrate-backed (subject-as-WAL); recovery is replay through Raft log.
- 14.5 (CLUSTER §11.8, §26.8) `.sshm` sidecar: the authoritative Sylk format; metadata, indexes, statistics, write tokens. `.tshm` (Turso) readable for migration compat.
- 14.6 (CLUSTER §27.8) CC tuning: three-tier regime — read-only, single-writer fast path, multi-writer with conflict detection. Closed-loop tuning monitors abort rate and shifts regime.
- 14.7 Crash safety: `fsync` discipline (or substrate equivalent); torn-page protection; recovery is total.
- 14.8 SQLite drop-in compatibility: same C API surface (cgo wrapper) for embedded use; native Go API for substrate-aware use.
- 14.9 (CLUSTER §6.7) Cross-engine transactions: SQL writes can participate in MultiNamespaceTx; inner 2PC with DAG.

**Acceptance.** Passes SQLite test suite (TCL tests adapted); sub-millisecond commits on local; fully participates in distributed transactions.

**Tests.**
- Unit: parser, planner, executor; MVCC correctness; WAL recovery.
- Integration: SQLite TCL suite; mixed workloads with cross-engine transactions.
- Race: concurrent writers; serializable conflict detection.
- Adversarial: torn pages (kill -9 mid-fsync), corrupted WAL, malicious SQL injection (must fail planner).
- Negative: disk full, crash recovery, schema migration mid-transaction.

### Phase 15 — SQL Extensions (CLUSTER §11.9, §27)

**Items.**
- 15.1 Vector type: native; ANN indexes (HNSW, IVF+PQ, BBQ — leveraging existing `core/vectorgraphdb/`).
- 15.2 JSON path expressions, JSONB storage.
- 15.3 Time-series optimizations: per-subject time-bucketed indexes; downsampling functions.
- 15.4 Graph queries: recursive CTE, transitive closure, shortest path.
- 15.5 Substrate-native tables: `_sylk.subjects`, `_sylk.events`, `_sylk.frontiers` queryable via SQL.
- 15.6 UDFs: tier-1 (DSL), tier-2 (native Go); same tiering as state machines.
- 15.7 (CLUSTER §27) Pushdown: predicates pushed to storage layer; vector pre-filters; substrate metadata indexes.

**Acceptance.** Vector queries beat external vector DBs at the same recall; JSON/graph queries are competitive with PostgreSQL.

**Tests.**
- Unit: each extension in isolation.
- Integration: mixed workloads with vector + JSON + graph.
- Race: index updates during queries.
- Adversarial: pathological queries (Cartesian explosions blocked by planner cost cap).
- Negative: malformed JSON, invalid vector dimensions, schema drift.

---

## 8. Track G — Pub/Sub, KV, Queues

### Phase 16 — Substrate-Native Pub/Sub

**Items.**
- 16.1 Subject = topic; subscribers register on Raft group hosting the subject.
- 16.2 Causal ordering: subscribers see events in causal order (DAG topo-sort).
- 16.3 Per-subscriber bounded buffer; slow subscriber backpressure.
- 16.4 Replay: subscribers can request from any HLC point; bounded by retention.
- 16.5 Filter pushdown: subscribers register filters; substrate evaluates pre-deliver.
- 16.6 (CLUSTER §31.7) Subject deletion semantics propagate to subscribers.

### Phase 17 — KV (substrate-backed)

**Items.**
- 17.1 KV as a subject family: each key is a subject with single-value SM.
- 17.2 Watches: subscribe to key changes (built on pub/sub).
- 17.3 Atomic compare-and-swap.
- 17.4 TTL via retention policy.
- 17.5 Range queries via SQL engine over KV namespace.

### Phase 18 — Queues (substrate-backed)

**Items.**
- 18.1 Queue as a subject with multi-consumer SM; messages have visibility timeout.
- 18.2 At-least-once delivery; exactly-once via idempotency keys (Phase 4.5).
- 18.3 Dead-letter queue support.
- 18.4 Priority queues with per-tenant fairness.
- 18.5 Long-poll receive.

**Acceptance (16-18).** Pub/sub P50 < 1ms LAN; KV CAS linearizable; queue delivery semantics provable under churn.

**Tests.** Unit per primitive; integration cross-primitive; race on concurrent access; adversarial on slow subscriber, redelivery, priority inversion; negative on subject deletion mid-subscribe.

---

## 9. Track H — Object Store, Blobs, Content-Addressing

### Phase 19 — Blob Store

**Items.**
- 19.1 Content-addressed: BLAKE3 of content is the key.
- 19.2 Chunked storage: large blobs split into content-addressed chunks; manifest is a Merkle tree.
- 19.3 Dedup across blobs (chunks shared).
- 19.4 (CLUSTER §19.6) Erasure coding for cold blobs (RS 10+4 or Cauchy-RS); local + remote tier; auto-promotion.
- 19.5 Blob references in SQL/KV/pub-sub are by hash; substrate fetches lazily.
- 19.6 Garbage collection: refcount via substrate; sweep uncollected after grace.

### Phase 20 — Lazy Storage and Streaming

**Items.**
- 20.1 Streaming uploads: chunks streamed as written, hashed online.
- 20.2 Streaming downloads: chunks pre-fetched, prefix-served.
- 20.3 Range requests: byte ranges served from cold tier without full fetch.
- 20.4 Content-defined chunking (FastCDC) for better dedup.

**Acceptance (19-20).** Blob upload P50 < (size/throughput + 1ms); cold-tier reads stream without first-byte-latency penalty > 50ms; dedup ratio measurable via test corpus.

**Tests.** Unit on chunking, dedup; integration on TB-scale; race on concurrent upload of same content; adversarial on hash collision (forced); negative on partial upload, GC race.

---

## 10. Track I — Quotas, Multi-Tenancy, Isolation

### Phase 21 — Hierarchical Quota System

**Items.**
- 21.1 Per-tenant token bucket; hierarchical (cluster → tenant → namespace → subject).
- 21.2 (CLUSTER §22.7) Earned-burst credits: tenants accumulate credits when under-utilizing; burst is bounded by credits.
- 21.3 (CLUSTER §31.24) Cross-DC borrow/lend: a tenant can borrow capacity from another DC's bucket if local is exhausted; bounded by global pool.
- 21.4 Resource classes: CPU, memory, storage, IOPS, network, decompression budget (Phase 4.4.8).
- 21.5 Quota enforcement at every layer: API gateway, Raft proposer, SM dispatcher, SQL planner.
- 21.6 Quota violation returns structured backpressure (not error); caller can wait or cancel.

### Phase 22 — Tenant Isolation

**Items.**
- 22.1 Per-tenant Raft groups (one or more) — no shared groups across tenants.
- 22.2 Per-tenant encryption keys (Phase 10.3).
- 22.3 Per-tenant SVIDs scoped to tenant namespace.
- 22.4 Per-tenant page cache, page tables, NUMA-aware.
- 22.5 Tenant isolation audit: deterministic tests prove no cross-tenant data flow.

**Acceptance.** Quota over-spend is bounded; tenant isolation is provable under chaos.

**Tests.** Unit on bucket algebra; integration on multi-tenant load with adversarial neighbor; race on bucket contention; adversarial on attempted cross-tenant read (must fail at every layer); negative on quota exhaustion and recovery.

---

## 11. Track J — Audit, Observability, Tracing

### Phase 23 — Audit Log

**Items.**
- 23.1 Append-only audit log per cluster + per tenant.
- 23.2 Every operation captured with `(actor_svid, hlc, raft_term, subject, op, result_hash)`.
- 23.3 Audit log is itself a substrate subject — full causal DAG, replicated.
- 23.4 Audit queries via SQL.
- 23.5 Tamper-evidence: audit Merkle root anchored periodically to operator group.

### Phase 24 — Observability

**Items.**
- 24.1 Metrics: every component exports Prometheus + OpenTelemetry; cardinality budgeted.
- 24.2 Tracing: OpenTelemetry trace context propagated through every RPC + Raft entry; trace samples follow workflows across federation.
- 24.3 Profiling: continuous profiling with `pprof` endpoints; per-tenant attribution.
- 24.4 Logs: structured (JSON), per-tenant attribution, retention policy.
- 24.5 Dashboards: prebuilt for operator, per-tenant, per-subject views.

**Acceptance.** Every claim/event/transaction is traceable end-to-end across federation; audit log is provably tamper-evident.

**Tests.** Unit on audit append, hash chain; integration on cross-federation trace propagation; race on concurrent audit writes; adversarial on attempted log tamper (must be detected by Merkle audit); negative on audit log full, retention pruning correctness.

---

## 12. Track K — Federation Runtime, Learners, Freshness API

### Phase 25 — Federation Runtime

**Items.**
- 25.1 Federation control plane (Phase 9) gets runtime: cross-cluster routing, federation-aware MultiNamespaceTx.
- 25.2 Per-cluster federation gateway: signed envelopes, frame-pinning.
- 25.3 Federation read/write paths: WriteToken issued by source cluster; ObservedAfter at destination.
- 25.4 (CLUSTER §20.7) Backpressure cascade live: ECN-marked envelopes flow upstream.
- 25.5 Federation membership changes: BFT consensus → operator-group propagation → local Raft policy update.

### Phase 26 — Learners and Freshness API

**Items.**
- 26.1 Learner replicas across federation.
- 26.2 (CLUSTER §20.8) Freshness API: every read returns `(value, hlc_at, raft_index_at)`; clients can require `hlc_at >= write_token.hlc`.
- 26.3 Read classification: linearizable (leader), bounded-staleness (learner with check), eventual (any replica). Default per call-site; explicit override.
- 26.4 Causal-consistent reads: pass session WriteToken; replica waits or redirects until token is satisfied.

**Acceptance.** Cross-federation reads are linearizable when WriteToken is presented; bounded-staleness reads have provable bounds; learners never serve reads ahead of their freshness claim.

**Tests.** Unit on freshness algebra; integration on cross-cluster RAW; race on learner promotion; adversarial on lying learner (must be detected by frame-pinning + Merkle); negative on federation partition during read.

---

## 13. Track L — Operator, Topology, SM Lifecycle

### Phase 27 — Operator (control plane)

**Items.**
- 27.1 Cluster bootstrap: SVID provisioning, operator group formation, initial topology.
- 27.2 Cluster scaling: add/remove nodes, rebalance Raft groups, online.
- 27.3 Configuration management: cluster-wide config in operator group; per-namespace overrides; rollout with canary.
- 27.4 Disaster recovery: backup operator-group state; restore procedure; RTO/RPO measurable.
- 27.5 Upgrade: rolling upgrade with version compatibility matrix; SM coexistence (Phase 12.6) live.

### Phase 28 — Topology Management

**Items.**
- 28.1 Topology group decides namespace-to-Raft-group placement.
- 28.2 Auto-rebalance on load skew.
- 28.3 Failure-domain awareness: replicas spread across racks/AZs/DCs.
- 28.4 Geo-policies: per-namespace allowed regions (data residency).

### Phase 29 — SM Lifecycle Management

**Items.**
- 29.1 SM versioning, deployment, canary, rollback.
- 29.2 (CLUSTER §24.7) Coexistence semantics live: long-running transactions hold version pins.
- 29.3 SM telemetry: per-version invocation count, latency, error rate.
- 29.4 SM permissions: which subjects can a given SM read/write; enforced by substrate.

**Acceptance.** Cluster lifecycle (bootstrap → scale → upgrade → DR) tested end-to-end; topology decisions converge under chaos; SM lifecycle preserves all invariants.

**Tests.** Unit per operator action; integration on full lifecycle; race on concurrent topology changes; adversarial on malicious upgrade attempt (signature check fails); negative on backup corruption, restore correctness.

---

## 14. Track M — Adaptive Transport, SQLite Envelope, SQL Extensions

### Phase 30 — Adaptive Transport (CLUSTER Phase 25)

**Items.**
- 30.1 Adaptive selector convergence under realistic conditions.
- 30.2 Per-tenant transport policies (e.g., latency-critical tenant may force RAW+FEC).
- 30.3 Cross-cluster RAW (CLUSTER §31.24): federation envelopes can use RAW where path supports it.
- 30.4 Transport upgrades online (e.g., HTTP/3 evolution); no protocol freeze.
- 30.5 NUMA-aware shmem rings; CPU pinning; lock-free queues with hazard pointers or epoch-based reclamation.

### Phase 31 — SQLite Envelope-Pushing (CLUSTER §11.9, Phase 27)

**Items.**
- 31.1 Native vector indexes integrated with SQL planner cost model.
- 31.2 Substrate-aware query optimization: planner sees cluster topology, chooses best replica.
- 31.3 Streaming SQL: long-running queries that yield rows as substrate events arrive.
- 31.4 Continuous queries: SQL queries that subscribe to subject updates and re-evaluate incrementally.
- 31.5 SQL over federation: cross-cluster queries with federation-aware planner.
- 31.6 (CLUSTER §27.8) CC tuning closed-loop validation.

**Acceptance.** SQL performance competitive with native single-node DBs for OLTP; superior for analytics that benefit from substrate-native indexes.

**Tests.** Unit per optimization; integration on cross-cluster queries; race on continuous query updates; adversarial on planner-cost manipulation; negative on slow query with proper cancellation.

---

## 15. Track N — Architectural Micro-Gaps, Workflow + Algebraic Effects

### Phase 32 — Architectural Refinements (CLUSTER Phase 28)

**Items (10 micro-gaps, all addressed earlier in tracks A-M; this phase is integration testing).**
- 32.1 SVID rotation under live traffic — full chaos run.
- 32.2 MultiNamespaceTx abort cleanup — adversarial run.
- 32.3 Cross-engine transactions — full DAG + SQL + KV.
- 32.4 Subject deletion cascade — multi-namespace dependency.
- 32.5 Federation backpressure cascade — multi-cluster ECN run.
- 32.6 Learner freshness — adversarial lying-learner detection.
- 32.7 Quota burst — cross-DC borrow/lend.
- 32.8 SM coexistence — long-transaction-spanning upgrade.
- 32.9 CC tuning — closed-loop tuning validation.
- 32.10 Cross-cluster RAW — federation envelope on RAW transport.

**Acceptance.** All 10 micro-gaps pass adversarial validation simultaneously under chaos.

### Phase 33 — Compositional Workflow + Algebraic Effects (CLUSTER Phase 29)

**Items.**
- 33.1 (CLUSTER §26.7) Algebraic effect handlers: resume style (continuation captured) and restart style (computation re-run from a checkpoint with new context). Effects are first-class substrate primitives.
- 33.2 (CLUSTER §26.8) Compositional workflow combinators borrowed from `../barnum`:
  - `pipe(a, b, c)` — sequential
  - `forEach(a)` — fan-out
  - `all(a, b, c)` — parallel join
  - `branch(predicate, a, b)` — conditional
  - `loop(recur)` — bounded fixed point
  - `tryCatch(a, handler)` — error recovery via restart effect
  - `withTimeout(a, duration)` — time-bounded
  - `race(a, b)` — first-wins
  - `withResource(acquire, release, body)` — bracket
- 33.3 (CLUSTER §26.9) Workflow-as-substrate-subject: a workflow execution is a subject; its events are the steps; reads of intermediate results are causally consistent.
- 33.4 (CLUSTER §26.10) Progressive context disclosure: each handler receives only the context required by its claim envelope; broader contexts are explicitly composed via `withResource` or claim escalation.
- 33.5 Determinism guarantee: workflows are deterministic given the same inputs and the same effect-handler bindings; replays are bit-identical.
- 33.6 Effect-handler binding: at workflow start, every effect declared by the workflow must have a handler bound; unbound effects fail at admission, never at runtime.
- 33.7 Workflow versioning + SM coexistence: long-running workflows pin their handler set + SM versions for the workflow's lifetime.
- 33.8 Architect-authored corrective actions (per `project_corrective_action_authority`): on workflow failure, the architect agent is invoked; its remediation claims author the recovery path.

**Acceptance.** Workflows compose without leaking context; effects resume/restart correctly under chaos; long-running workflows survive cluster upgrades; replay is bit-identical.

**Tests.**
- Unit per combinator and effect.
- Integration on Barnum-style demos: simple-workflow, retry-on-error, convert-folder-to-ts, identify-and-address-refactors.
- Race on concurrent workflow steps.
- Adversarial on byzantine handler (returns different result on replay — detected and re-run with fresh handler binding); on attempted context leak (fails admission).
- Negative on cancelled workflow, timed-out step, resource-exhausted handler, corrupted checkpoint (restart from previous checkpoint).

---

## 16. Track Z — CLAIMS Overlay (14 phases)

CLAIMS phases overlay the substrate. Each phase pulls from the substrate primitives built in Tracks A–N.

### CLAIMS Phase 0 — Foundations

- Z.0.1 Claim type system: structured claim envelopes (subject, action, evidence, signer, frame).
- Z.0.2 Claim authoring API.
- Z.0.3 Claim store as substrate subject.

### CLAIMS Phase 1 — Authoring and Validation

- Z.1.1 Claim validation pipeline: schema → policy → evidence check.
- Z.1.2 Validation results are themselves claims.

### CLAIMS Phase 2 — Architect, Orchestrator, Worker Roles

- Z.2.1 Architect agent: authors corrective actions.
- Z.2.2 Orchestrator: dispatches and monitors (no authoring).
- Z.2.3 Worker agents: execute and emit claims.

### CLAIMS Phase 3 — Substrate Integration (CLAIMS §3)

- Z.3.1 Claim envelopes use SWF wire format.
- Z.3.2 Claim subjects are substrate subjects.
- Z.3.3 Claim Raft groups per namespace.

### CLAIMS Phase 4 — Wire Encoding (SWF)

- Z.4.1 Claim schemas codegen to SWF.
- Z.4.2 Schema-trained zstd dicts for claims.

### CLAIMS Phase 5 — Causal Claim Graph

- Z.5.1 Claims form a causal DAG (parent claims = evidence/dependency).
- Z.5.2 Causal cone observability for any claim.

### CLAIMS Phase 6 — Red/Green/Refactor Loop

- Z.6.1 Red phase: failing test → claim.
- Z.6.2 Green phase: passing implementation → claim.
- Z.6.3 Refactor phase: improvement claim.
- Z.6.4 Loop closure verified by Architect.

### CLAIMS Phase 7 — Pipeline Lifecycle

- Z.7.1 Pipeline pod lifetime ≥ disk-commit ack (per `feedback_pipeline_lifecycle_disk_commit`).
- Z.7.2 Rejection/correction is not terminal for the VFS.

### CLAIMS Phase 8 — Memory Forest Integration

- Z.8.1 Forest advisory observations as claims.
- Z.8.2 Forest read-only on agent claims.

### CLAIMS Phase 9 — Fabric Integration (CLAIMS §9)

- Z.9.1 Fabric advisory feed.
- Z.9.2 No Fabric authoring of claims.

### CLAIMS Phase 10 — Guardian Integration

- Z.10.1 Manual-first approval flows in TUI.
- Z.10.2 Guardian skills_*: command, plan, fetch, gate, safety.
- Z.10.3 Guardian advisory; user authorizes.

### CLAIMS Phase 11 — VectorGraph + Bleve + Tree-sitter + LSP

- Z.11.1 IVF + Vamana + BBQ for code embedding retrieval.
- Z.11.2 Sharded BBQ for scale.
- Z.11.3 Bleve doc DB for unstructured.
- Z.11.4 Tree-sitter for parsing/validation.
- Z.11.5 LSP for navigation and refactor support.
- Z.11.6 All accessible to agents as substrate subjects.

### CLAIMS Phase 12 — Phased Plan (per CLAIMS §12)

- Z.12.1 Each claims subsystem has its own phased ladder (described in CLAIMS.md).

### CLAIMS Phase 13 — Substrate Integration Cap

- Z.13.1 All claims systems consume substrate primitives natively.
- Z.13.2 No bespoke transport, persistence, or transaction logic outside substrate.
- Z.13.3 Architect-authored corrective actions integrated with workflow combinators (Phase 33).
- Z.13.4 Progressive context disclosure (CLUSTER §26.10) applied to every claim envelope.
- Z.13.5 Workflow-as-subject (CLUSTER §26.9) — claim sequences are workflow subjects.

**Acceptance (CLAIMS overlay).** Every claims invariant from CLAIMS.md holds against the substrate; the architect-orchestrator-worker dynamic is preserved; agents own all authoring decisions.

**Tests.** Unit per claim type; integration on red/green/refactor end-to-end; race on concurrent claim authoring across agents; adversarial on attempted non-agent authoring (must fail); negative on pipeline teardown before commit (must be impossible).

---

## 17. Acceptance Gates (per track, integrated)

Each track must clear these gates before its successors begin (parallel tracks may proceed once their dependencies clear, not the entire prior track):

1. **Static gate.** All packages: `go vet`, `staticcheck`, `gosec`, custom lints clean.
2. **Unit gate.** 100% of public surface covered; property tests for invariants.
3. **Race gate.** `go test -race -count=100` clean.
4. **Determinism gate.** Deterministic simulator: same seed → bit-identical trace, 10K runs.
5. **Integration gate.** Cross-component scenarios pass.
6. **Chaos gate.** Random fault injection (node death, partition, clock skew, byzantine messages) for 24h continuous; no invariant violation.
7. **Adversarial gate.** Targeted attacks per track's threat model; all blocked at the correct layer with correct telemetry.
8. **Performance gate.** Per-track latency/throughput targets met at P50/P99/P99.9.
9. **Correctness audit.** Every soundness theorem in CLUSTER.md proven (or formally verified where TLA+ specs exist).

---

## 18. Cross-Cutting Concerns

- **Reproducible builds.** Every Sylk binary has BLAKE3-verified build provenance checked into operator group.
- **Continuous fuzzing.** Every codec, parser, planner, and protocol parser has a fuzz target with persistent corpus.
- **Continuous chaos.** Production clusters run a chaos schedule; staging runs continuous chaos.
- **Telemetry-driven validation.** Every gate's success criterion has a metric; metric must hold for the run window, not just at gate time.
- **Documentation.** Every public API, every wire format, every protocol has a spec doc + a working example. Generated reference docs from code.
- **Interoperability tests.** Sylk talks to Sylk across versions during upgrade (SM coexistence, federation across versions).

---

## 19. Soundness Theorems (must be proven before each track's correctness audit gate)

The following theorems are stated in CLUSTER.md. Each track's correctness audit gate requires the relevant theorems be proven (informal proof acceptable for v1; TLA+ formal where applicable).

- **T1 (Causal soundness):** No replica observes effect-without-cause. (Phase 5)
- **T2 (Linearizable cross-domain reads with WriteToken):** ObservedAfter(token) ⇒ caller's view ⊇ token's prefix. (Phase 26)
- **T3 (Bomb defense soundness):** No bomb passes all 10 layers given correct schemas. (Phase 4)
- **T4 (Tenant isolation):** No data flows across tenants given correct SVID scopes. (Phase 22)
- **T5 (SM determinism):** Tier-1 and Tier-2 SMs are bit-deterministic given equal inputs. (Phase 12)
- **T6 (MultiNamespaceTx atomicity):** No partial visibility across namespaces or engines. (Phase 6)
- **T7 (Frame-pinning soundness):** No replay or relay verifies under stale frame. (Phase 1)
- **T8 (Federation BFT safety):** With ≤f byzantine clusters out of 3f+1, federation control plane is safe. (Phase 9)
- **T9 (Workflow determinism + replay):** Replays under bound handlers are bit-identical. (Phase 33)
- **T10 (Hard-delete irrecoverability):** After hard-delete cascade, content is cryptographically irrecoverable. (Phase 11)
- **T11 (Quota over-spend bound):** Hierarchical bucket + earned credits guarantee bounded over-spend. (Phase 21)
- **T12 (Architect authority):** No claim authoring path bypasses agent ownership. (Track Z)

---

## 20. Why "maximally everything" — restated invariants

- **Correctness > velocity.** Every soundness theorem proven before that surface is exposed.
- **Robustness = no surprise modes.** Every adversarial scenario in the threat model has a test that fails the system before mitigation, then passes after.
- **Performance = within constant factors of physics.** Single-node IPC at shmem-ring speed; LAN at NIC line rate; WAN at link RTT + minimal overhead; storage at media bandwidth.
- **Agentic = agents own decisions.** No system component authors claims, gates transitions, or evaluates validations. Agents do. Forest/Fabric/Guardian advise.

This is the substrate. No phase is optional. No phase is "deferred." The order is dependency-ordered with the maximum parallelism the dependencies allow. The acceptance gates are absolute. The soundness theorems are the contracts.
