# Sylk Cluster Substrate

A unified durable coordination substrate for Sylk that replaces ad-hoc bus, WAL,
projection, and fabric plumbing with one principled stack. Designed to scale
from a single user running the TUI on a personal laptop up to a single user (or
many users) running TUIs against a multi-datacenter remote cluster, **without
forking the abstractions**.

This document is the canonical specification for that substrate. It describes
the layers, the wire format, the consensus and membership protocols, the
durability primitives, the higher-level coordination services that ride on
top, and the three deployment modes the substrate supports.

---

## 0. Goals

The substrate is governed by five rubric properties. Every design decision is
checked against these.

1. **Crash-safe end-to-end.** Survive node crashes, partial network partitions,
   GC pauses, clock skew, full-cluster restarts, and silent disk corruption,
   with no message loss for durable classes and no double-execution for
   idempotent operations.

2. **Causal ordering as a first-class invariant.** Preserve happens-before
   ordering across pipelines, sessions, agents, and DCs without requiring a
   global lock. Every published frame carries a Hybrid Logical Clock (HLC)
   stamp; every entry references its causal parents; replay is graph-walk over
   a causal DAG, not sequence-scan.

3. **Wire-level invariants for dedupe, schema, and authority.** These three
   properties are not optional headers callers may forget; they are mandatory
   fields enforced by the substrate at publish time.

4. **One substrate, many projections.** The durable log, the fabric activity
   store, the claims board, the forest event ledger, and the COW pipeline VFS
   commit queue collapse to *the same primitive* seen from different angles.
   Sovereign systems publish to subjects; lenses read subjects; projections
   are queries, not separate consumers.

5. **Time-travel by construction.** Because every entry has an HLC and the log
   is a Merkle DAG, debugging the system is a graph query, not a log-archaeology
   exercise. Any historical HLC frontier is reproducible.

### Non-goals

- This is not a hyperscale distributed-task-execution framework. The substrate
  borrows hyperscale's distributed primitives (SWIM, multi-Raft, ledger,
  reliability) but the application layer is Sylk's, not load-test workflows.

### What this *is* with respect to relational data

Sylk fully commits to its own extended SQL surface. We are *replacing
SQLite with a substrate-native, SQLite-compatible implementation* —
turso (`../turso`) is a reference engine we draw from, but the
production target is a Sylk-owned engine that ships SQLite wire- and
file-compatibility plus the extensions in §11.8, §11.9, §27. Concretely:

- Tables are substrate subjects (page-deltas + CDC dual-subject pattern,
  §11.8).
- The WAL discipline is the substrate's causal Merkle DAG (Layer 4).
- Replication is multi-Raft per namespace (Layer 3) — not single-master
  SQLite WAL replication.
- The SQL surface is extended with the §27 envelope-pushing features
  (CRDT tables, per-row consistency, causal foreign keys, continuous
  queries, vector+SQL, federated queries, multi-engine atomic
  transactions, etc.) — features no production SQL engine ships.
- The on-host coordination sidecar is `.sshm` (§26.8), not turso's
  `.tshm`; we read existing turso `.tshm` for compatibility but the
  authoritative format is ours.

Sylk's relational data lives in *this* — not in vanilla SQLite, not
in plain turso. Existing SQLite-compatible drivers (`go-sqlite3`-shape
APIs, application code that speaks SQLite) work unchanged because we
preserve the wire and file compatibility. Everything else — the
storage engine, the WAL, the replication, the transaction layer —
is substrate.

---

## 1. Three Operating Modes

The substrate runs in one of three deployment shapes. Same code, same
abstractions, different binding for transport and replica counts.

### 1.1 Mode summary

| Mode             | Process layout                                           | Transport       | Raft replicas | SWIM           | Use case                             |
|------------------|----------------------------------------------------------|-----------------|---------------|----------------|--------------------------------------|
| Embedded         | sylk binary = TUI + substrate + agents + knowledge stack | Go channel      | 1 per group   | no-op          | Single user, laptop, default         |
| Local Daemon     | sylkd background process; sylk TUI client                | Unix socket     | 1 per group   | no-op          | Power user, persistent agents        |
| Remote Multi-DC  | sylk TUI client + sylkd cluster across DCs               | QUIC + mTLS     | 3+ per group  | full hierarchy | Team / enterprise, long-lived agents |

### 1.2 Deployment shapes

```
┌─────────────────── EMBEDDED MODE (single laptop) ────────────────────┐
│                                                                       │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │                       sylk (one process)                         │ │
│  │                                                                  │ │
│  │  ┌──────┐    ┌────────────────┐    ┌───────────────────────┐   │ │
│  │  │ TUI  │◄──►│   Substrate    │◄──►│  Agents + Knowledge   │   │ │
│  │  │      │    │  (in-process,  │    │  + Forest + Fabric +  │   │ │
│  │  │      │    │   Go channels) │    │  VFS + Treesitter ... │   │ │
│  │  └──────┘    └───────┬────────┘    └───────────────────────┘   │ │
│  │                      │                                           │ │
│  │                      ▼                                           │ │
│  │              ~/.sylk/data/                                       │ │
│  │              (segments, indexes, snapshots)                      │ │
│  └─────────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────┘

┌─────────────────── LOCAL DAEMON MODE (one host) ─────────────────────┐
│                                                                       │
│  ┌──────────────┐    AF_UNIX     ┌────────────────────────────────┐  │
│  │  sylk (TUI)  │◄──────────────►│     sylkd (background)          │  │
│  │              │                 │  Substrate + Agents + Knowledge │  │
│  └──────────────┘                 └────────────┬───────────────────┘  │
│                                                │                       │
│  ┌──────────────┐    AF_UNIX                   ▼                       │
│  │  sylk (TUI 2)│◄────────────────────► ~/.sylk/data/                 │
│  └──────────────┘                                                     │
└───────────────────────────────────────────────────────────────────────┘

┌────────────────── REMOTE MULTI-DC MODE ──────────────────────────────┐
│                                                                       │
│  ┌──────────────┐                                                    │
│  │ sylk (TUI)   │                                                    │
│  │ + local      │                                                    │
│  │   cache      │                                                    │
│  └──────┬───────┘                                                    │
│         │ QUIC + mTLS (SPIFFE SVID)                                  │
│         ▼                                                            │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                      sylkd cluster                              │ │
│  │                                                                 │ │
│  │   ┌─────────────┐    ┌─────────────┐    ┌─────────────┐       │ │
│  │   │   DC: us-w  │    │  DC: us-e   │    │   DC: eu-w  │       │ │
│  │   │  ┌───────┐  │    │  ┌───────┐  │    │  ┌───────┐  │       │ │
│  │   │  │ sylkd │◄─┼────┼──┤ sylkd │◄─┼────┼──┤ sylkd │  │       │ │
│  │   │  │ sylkd │  │    │  │ sylkd │  │    │  │ sylkd │  │       │ │
│  │   │  │ sylkd │  │    │  │ sylkd │  │    │  │ sylkd │  │       │ │
│  │   │  └───────┘  │    │  └───────┘  │    │  └───────┘  │       │ │
│  │   │   SWIM ◄────┼────┼─►   SWIM   ◄┼────┼►    SWIM    │       │ │
│  │   │   Raft ◄────┼────┼─►   Raft   ◄┼────┼►    Raft    │       │ │
│  │   └─────────────┘    └─────────────┘    └─────────────┘       │ │
│  └────────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────┘
```

### 1.3 Why same code

The abstractions are *invariant* across modes. Identity, time, wire format,
authority enforcement, dedupe, causal DAG, content-addressed cursors, fabric
projection — all identical. What changes is binding:

- **Transport binding** swaps from in-process channels (embedded) to Unix
  sockets (local daemon) to QUIC streams (remote).
- **Membership binding** is no-op in embedded/local-daemon (one node) and
  full SWIM in remote.
- **Consensus binding** uses degenerate single-replica Raft groups in
  embedded/local-daemon and 3+ replica groups across DCs in remote.

Crucially: degenerate Raft groups still run the full Raft state machine. Every
"vote" is the leader voting for itself; every "fsync" is the only fsync. The
WAL discipline, snapshot cadence, truncation semantics, and recovery-on-startup
playback are *unchanged*. The laptop user inherits cluster-grade durability;
the cluster user inherits laptop-grade simplicity for everything below the
consensus layer.

This makes scaling honest. There is no "embedded mode bug" class — production
bug fixes flow into the laptop and vice versa. Local development is byte-for-
byte the same code as production.

---

## 2. Layer Architecture

The substrate is nine layers. Each layer presents a contract to the layer
above and depends only on the contracts below it.

```
┌─────────────────────────────────────────────────────────────────────┐
│ Layer 9: Observability                                              │
│   time-travel queries, causal cone, provable audit, metrics surface │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 8: Higher-level primitives                                    │
│   typed KV, object store, fabric activity, claims board, forest    │
│   ledger, VFS commit log, authority broadcast                       │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 7: Reliability                                                │
│   class-based backpressure, retry budgets, circuit breakers,        │
│   priority scheduling, best-effort tier                             │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 6: Idempotency / dedupe                                       │
│   three-layer dedupe (event_id + payload_fingerprint + idem_key)    │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 5: Delivery                                                   │
│   pull-first consumers, content-addressed cursors, ack/nack/term    │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 4: Durable log (Causal Merkle DAG)                            │
│   per-subject append-only DAG, sealed segments, content addressing  │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 3: Consensus (Multi-Raft)                                     │
│   per-namespace Raft groups, operator group, topology group         │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 2: Membership / failure detection (SWIM++)                    │
│   hierarchical liveness, sectioned coordinates, dual-channel        │
│   suspicion, Merkle reconciliation on rejoin                        │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 1: Wire format and transport                                  │
│   56-byte frame header, CBOR body, QUIC + mTLS, two-channel split   │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 0: Identity, time, addressing                                 │
│   SPIFFE SVIDs, HLC, typed subject URIs                             │
└─────────────────────────────────────────────────────────────────────┘
```

| Layer | Provides | Depends on |
|-------|----------|------------|
| 0 | identity, monotonic ordering, addressing | nothing |
| 1 | byte transport, frame validation | 0 |
| 2 | who is alive, where they are | 0, 1 |
| 3 | replicated state machines | 0, 1, 2 |
| 4 | durable causal log | 0, 1, 3 |
| 5 | reliable delivery to consumers | 0, 1, 4 |
| 6 | exactly-once-ish semantics | 0, 4, 5 |
| 7 | flow control, fairness, isolation | 1, 5, 6 |
| 8 | application-shape primitives | 4, 5, 6, 7 |
| 9 | introspection of all above | 4, 5 |

---

## 3. Layer 0 — Identity, Time, Addressing

### 3.1 Identity (SPIFFE SVIDs)

Every node, agent, pod, and session has a SPIFFE-style URI identity:

```
spiffe://sylk/<cluster>/<dc>/<node>/<agent-or-pod>/<session>
```

Examples:

```
spiffe://sylk/prod/us-west-2a/n-7f3a/architect/s-2025-04-26-abc123
spiffe://sylk/prod/us-west-2a/n-7f3a/runtime/-          // node-level
spiffe://sylk/prod/-/-/cluster-controller/-              // global control
spiffe://sylk/laptop/-/-/-/-                             // embedded mode
```

Each identity has an X.509 SVID issued by the cluster's CA (or by a local
trust root in embedded mode). SVIDs carry **authority bindings** — the
capabilities the identity holds — as X.509 extensions. The substrate
consults these bindings at publish time.

Identity scopes form a hierarchy:

```
cluster
  └── dc
        └── node
              └── agent / pod
                    └── session
```

A capability granted at level *N* applies to all entities below it unless
explicitly narrowed. An agent's `claim.issue` capability for session
`s-2025-04-26-abc123` is scoped to that session only; an operator's
`subject.register` capability is scoped to the cluster.

### 3.2 Time (Hybrid Logical Clocks)

Every node maintains a Hybrid Logical Clock:

```go
type HLC struct {
    PhysicalNs uint64  // wall clock in nanoseconds
    Logical    uint32  // monotonic counter, resets on physical advance
    NodeID     uint32  // tiebreaker, unique per node
}
```

**Update rule** (on every event, send, or receive):

```
local_phys = current_wall_clock_ns()
recv_phys, recv_logical = received.PhysicalNs, received.Logical (0 if local event)

new_phys = max(self.PhysicalNs, recv_phys, local_phys)

if new_phys == self.PhysicalNs and new_phys == recv_phys:
    new_logical = max(self.Logical, recv_logical) + 1
elif new_phys == self.PhysicalNs:
    new_logical = self.Logical + 1
elif new_phys == recv_phys:
    new_logical = recv_logical + 1
else:
    new_logical = 0

self.HLC = {new_phys, new_logical, self.NodeID}
```

**Properties guaranteed by HLCs**:

- Monotonic on each node, even across wall-clock backsteps and pauses.
- Total order across the cluster: `(PhysicalNs, Logical, NodeID)` lexicographic.
- Respects happens-before: if event A causally precedes B (A was observed
  before B was emitted), then `HLC(A) < HLC(B)`.
- Bounded skew from physical time (the physical component drifts at most by
  the network round-trip plus clock skew).

HLCs are stamped on every wire frame. Receivers update their HLC on every
receive; publishers stamp at send.

### 3.3 Addressing (typed subjects)

Subjects are not free-form strings. They are typed schemas registered with
the cluster's subject registry (a Raft-replicated KV store living in the
cluster-topology group).

**Subject URI structure**:

```
sylk://<namespace>/<kind>/<version>?partition=<partition_key>
```

Examples:

```
sylk://session/s-2025-04-26-abc123/claims/v3
sylk://session/s-2025-04-26-abc123/claims/v3?partition=task_42
sylk://session/s-2025-04-26-abc123/forest-events/v1
sylk://session/s-2025-04-26-abc123/vfs-commits/v2
sylk://fleet/architect/authority/v1
sylk://global/membership/v1
sylk://global/subject-registry/v1
```

| Component       | Purpose                                                                                  |
|-----------------|------------------------------------------------------------------------------------------|
| `<namespace>`   | Routes the subject to a particular Raft group (session, fleet, global)                   |
| `<kind>`        | Schema family — e.g., `claims`, `forest-events`, `vfs-commits`, `kv`, `object`, `view`   |
| `<version>`     | Schema version. v1, v2 are distinct subjects; subscribers declare which they accept       |
| `<partition_key>` | Optional. Within a subject, ordering is preserved per partition_key                    |

**Wire form**: subjects are resolved to a 64-bit subject ID via the registry.
Frames carry the subject ID, not the URI string. Registry updates are
cluster-wide events; nodes cache the URI ↔ ID map and invalidate on update.

**Schema evolution**: registering `sylk://.../claims/v2` does not modify
`v1`. Subjects are immortal once registered (deletion is a separate flow
gated by retention policy and operator authorization). Subscribers pick
which versions to accept; publishers pick which version to write. This
removes an entire class of "schema drift broke the consumer" bugs.

**Authority binding**: each subject has authority predicates registered at
creation time. Predicates are functions `(SVID, Subject, Frame) → Allow|Deny`
evaluated at publish time on the substrate side. Compromised callers cannot
publish to a subject they lack authority for, regardless of what their
client library says.

### 3.4 Live SVID rotation under active workload

SVIDs rotate periodically (per SPIRE config; typical interval 1-8h).
Rotation must not disrupt in-flight publishes, subscriptions, or
connections. The substrate handles rotation as an in-flight-safe
operation via cross-signed transition windows + frame-pinned
verification.

**Cross-signed transition window**: at rotation, the new SVID is
cross-signed against the old SVID's key for a configurable window
(default 30 min; configurable down to 5 min for high-security
clusters). During the window, both old and new SVIDs verify against
either trust path.

**Frame-level pinning**: each frame's signature is verified against
the SVID active at sign time. The frame doesn't carry an SVID
identifier; verifier maintains an LRU cache of `(svid_id,
validity_window)` keyed by SVID public-key fingerprint. Verifier
tries the most recently rotated SVID first; falls back to predecessor
within the cross-sign window.

**In-flight publish during rotation**: a publish initiated under
SVID-old whose frame goes on the wire mid-rotation completes
unchanged — the frame was signed with old key, verifier accepts via
cross-sign trust path. No retry, no observable user impact, no
allocation difference on the verify path.

**Subscription connections (QUIC)**: existing QUIC connection's mTLS
session was authenticated at handshake against the SVID active at
handshake. Connection lifetime can exceed SVID lifetime — the SVID
served as a *trust anchor at handshake*; subsequent traffic is
protected by QUIC's session keys (HKDF-derived at handshake). No
re-handshake required mid-rotation. Per QUIC RFC 9001 the handshake
identity is bound at handshake, not refreshed mid-connection.

**New connections post-rotation**: handshake uses the new SVID. New
connections expand the active-SVID set; existing connections continue
under their handshake-time SVID. Both coexist transparently.

**Capability binding continuity**: capabilities (§3.1) are bound to
the *identity URI* (e.g.,
`spiffe://sylk/.../engineer/s-abc`), not to a specific SVID instance
/ public-key fingerprint. Rotation produces a new SVID for the same
identity URI; capabilities preserved through rotation. Revocation
(§17.4 / §11.7) is a separate, distinct event from rotation.

**Term-bound keys (§17.1)** are orthogonal to SVIDs. Raft term keys
are ephemeral, generated at term start, destroyed at term end. SVID
rotation doesn't affect term keys; term-key rotation (which happens
at every leader election) doesn't affect SVIDs.

**Long-running batch operations**: a batch with embedded signatures
(snapshot install with Schnorr aggregate per §25.10; bulk replay)
verifies the aggregate against the SVID at batch-emission time. If
the operation spans rotation, verifier maintains historical SVID
public keys for verification. Historical keys cached in the
term-history subject `sylk://global/svid-history/v1` (signed,
content-addressed, retained per audit retention policy).

**Forward-secrecy at end of cross-sign window**: old SVID's *private*
key destroyed (overwritten + zeroed in HSM/keystore; verified via
post-destruction read that must fail). After window close, old SVID
can no longer sign new frames. The public key remains cached in the
SVID-history subject for verifying historical frames within retention
horizon. Past the §21.5 horizon, signatures verify against the
snapshot root rather than the original SVID public key.

**Audit invariants**:
- Every rotation event signed by the rotating-authority operator and
  recorded in `sylk://global/svid-rotations/v1`.
- Every revocation distinct from rotation, recorded in
  `sylk://global/svid-revocations/v1`.
- Anomaly detection: rotations outside operator-declared schedule
  trigger alert via §28.3 anomaly feed.

**Revocation overrides cross-sign**: if an SVID is revoked
mid-rotation (compromise detected), revocation publishes to the
revocations subject; verifiers refuse the SVID immediately, even
within the cross-sign window. Existing connections under the revoked
SVID are torn down on next heartbeat cycle (§5).

**Soundness theorem** (machine-checked alongside §17.1):

> For any frame F signed by SVID S at HLC `h`, F is verifiable by any
> honest replica R such that:
> (a) R's view of `sylk://global/svid-history/v1` covers HLC `h`, and
> (b) S was not revoked at HLC `h` per
> `sylk://global/svid-revocations/v1`.
>
> Verifiability is preserved through any number of rotations within
> the substrate's audit retention horizon.

---

## 4. Layer 1 — Wire Format and Transport

### 4.1 Frame format

Every wire frame is a 56-byte fixed-size header followed by an opaque CBOR body:

```
Bytes  Field                   Width   Description
─────  ─────                   ─────   ───────────
 0     ver                     1       protocol version (currently 1)
 1     flags                   1       bitfield: COMPRESSED, ENCRYPTED_BODY,
                                       URGENT, REPLY, SYSTEM
 2-3   msg_type                2       PUBLISH, ACK, NACK, TERM, PROBE,
                                       PROBE_ACK, PROBE_INDIRECT, JOIN,
                                       LEAVE, RAFT_*, CONTROL_*, CURSOR_*
 4-11  subject_id              8       64-bit subject ID resolved from registry
12-27  session_id              16      UUIDv7 of the originating session
28-35  hlc_phys                8       HLC physical component
36-39  hlc_log                 4       HLC logical component
40-43  hlc_node                4       HLC node ID
44-47  length                  4       body length in bytes
48-55  body_blake3_prefix      8       first 8 bytes of BLAKE3-256(body)

──── 56 byte header ────
| body (CBOR-encoded, length bytes)                                    |
──── variable length body ────
| trailer:                                                              |
|   authority_token (Ed25519 signature, 64 bytes) over header+body     |
|   full_body_blake3 (BLAKE3-256, 32 bytes) for content addressing     |
──── 96 byte trailer ────
```

The header is **append-only and zero-copy**: receivers slice it off mmap'd
buffers without parsing. The body's first-8-bytes BLAKE3 is in the header
for fast Bloom-filter checks; the full BLAKE3 in the trailer is the
authoritative content address used for dedupe and replay.

**Frame validation order** at receive time:

1. Decode header (no allocation).
2. Verify body length plausible (reject obviously truncated/malformed).
3. Verify HLC plausible (within drift bound of local HLC).
4. Look up `subject_id` in registry; resolve schema and authority predicates.
5. Verify authority token over `header || body` against publisher's SVID.
6. Verify body schema (CBOR-decode + schema check against the subject's
   registered schema for that version).
7. Compute full BLAKE3 over body; verify matches header prefix.
8. Run dedupe lookup (Layer 6); drop if duplicate.
9. Pass frame to delivery layer.

Steps 1-3 are zero-allocation; steps 4-9 may allocate. Bad frames are
rejected before any allocation, so a malicious peer cannot cause memory
pressure.

### 4.2 Transport: QUIC

The transport is QUIC over UDP, with TLS 1.3 mutual authentication via SPIFFE
SVIDs. QUIC is chosen over alternatives for these properties:

- Stream multiplexing without head-of-line blocking
- Connection migration (survives client IP change)
- 0-RTT resumption (fast reconnect)
- Built-in congestion control
- Mature library ecosystem (`quic-go` for Go)
- mTLS native, no separate TLS termination

**Fallback**: TLS-over-TCP for environments where QUIC is blocked. The
substrate supports both transports behind one interface; per-peer transport
selection is negotiated at connection time.

### 4.3 Two-channel split

Every connection carries **two QUIC stream pools**:

```
┌────────────────── connection (mTLS) ─────────────────────┐
│                                                            │
│   ┌─── Data plane stream pool ────────────────┐           │
│   │ PUBLISH, ACK, NACK, TERM, CURSOR_RESUME,  │           │
│   │ delivery push, replay reads                │           │
│   └────────────────────────────────────────────┘           │
│                                                            │
│   ┌─── Control plane stream ──────────────────┐           │
│   │ SWIM PROBE / PROBE_ACK / PROBE_INDIRECT,  │           │
│   │ RAFT_VOTE / APPEND / SNAPSHOT,             │           │
│   │ subject registry updates,                  │           │
│   │ capacity advertisements,                   │           │
│   │ Merkle reconciliation                      │           │
│   └────────────────────────────────────────────┘           │
└────────────────────────────────────────────────────────────┘
```

The data plane never blocks on control. A loaded data plane (large replay
read, bulk publish) cannot starve SWIM probes or leader heartbeats. This is
hyperscale's out-of-band channel concept made structural.

### 4.4 Hot-path discipline

Receive path is allocation-free. Send path is bounded-allocation.

**Per-connection arenas**: each connection holds an arena allocator for
parser scratch space. Recycled per frame batch. No per-frame heap allocation
on receive.

**Frame envelope free-list**: a sync.Pool of envelope structs. After a frame
is fully processed, the envelope returns to the pool.

**io_uring on Linux, kqueue on Darwin/BSD**: syscall batching. The transport
implementation is OS-aware; on Linux 5.10+, all reads/writes use io_uring.
On macOS, kqueue with EVFILT_READ batching.

**No reflection on hot paths**: schema validation uses precompiled CBOR
decoders generated at subject-registration time. New subjects trigger code
generation on first use; the generated decoder is cached.

### 4.5 Adaptive transport selection (multi-stack)

Hyperscale's TCP+UDP dual-stack solves the "ordered/reliable/large vs
fast/lossy/small" split. We extend to a four-path adaptive stack
chosen per-frame from `(class, body_size, dest_topology)`. Routing
happens substrate-side; callers don't pick transports.

```
┌─ Path                       ─┬─ Use                                    ─┐
│ QUIC datagrams (RFC 9221)   │ Critical, ≤1KB, single frame             │
│   - shares mTLS handshake    │   claim issued, ack frames               │
│ QUIC streams                │ Critical/Standard, multi-frame, ordered  │
│   - shared mTLS              │   Raft AppendEntries, replication        │
│ Raw UDP unreliable          │ Background/best-effort, gossip           │
│   - DTLS or none             │   SWIM probe, telemetry, clock tick      │
│ Raw UDP + Reed-Solomon FEC  │ Bulk medium frames (1-64KB)              │
│                             │   loss-tolerant view subjects, cold tier │
│ Shared-memory ring buffer    │ Same-host IPC                            │
│   - HLC fence in header      │   TUI ↔ daemon, knowledge ↔ agent        │
└──────────────────────────────┴───────────────────────────────────────────┘
```

QUIC datagrams give UDP-shape semantics over the QUIC connection,
sharing congestion control + auth with QUIC streams — one mTLS
handshake covers everything, unlike hyperscale's separate TLS-on-TCP
+ DTLS-on-UDP.

**Selection table** (default; per-subject override permitted):

| (class, size, dest)         | Path |
|------------------------------|------|
| Critical, < 1KB, intra-DC   | QUIC datagram |
| Critical, ≥ 1KB, any         | QUIC stream + sync fsync |
| Standard, intra-DC           | QUIC stream, group commit |
| Standard, cross-DC          | QUIC stream + zstd |
| Bulk, large, cross-DC        | QUIC stream + zstd-with-schema-dict |
| Bulk, medium, lossy WAN      | UDP + Reed-Solomon FEC |
| Background, small            | raw UDP best-effort |
| any, same host               | shared memory (skip kernel) |

**Multipath for Critical**: substrate sends Critical frames *both* over
QUIC stream AND QUIC datagram simultaneously; receiver accepts whichever
arrives first; §6 dedupe (event_id) drops the redundant copy. 2x bandwidth
for Critical (a tiny fraction of total traffic) buys substantially better
tail latency under loss.

**Per-class congestion control**: QUIC connection's CC selector is per
stream class. Critical streams use BBR with conservative ramp (preserve
latency over throughput); Bulk streams use CUBIC (throughput over
latency); Background uses LEDBAT-style scavenger (only fills idle
bandwidth). `quic-go`'s pluggable CC interface registers per-class
selectors.

**Stream-level priority isolation**: rather than relying on QUIC stream
priority hints, **separate QUIC connections per priority class** between
peer pairs — Critical class gets its own connection with its own packet
queue. Higher per-peer overhead (one extra mTLS context per class) but
eliminates HoL inversion entirely.

### 4.6 Bandwidth, compression, and verification

Wire-level optimizations exploiting our typed/structured frames.

**Pre-trained Zstd dictionaries per schema**: schema registration
triggers training of a per-schema dictionary on a corpus sample. Dict ID
shipped in the schema entry; receivers cache by `(schema_id, dict_version)`.
Compression of typed bodies hits 10-20x — dramatically better than
hyperscale's per-message zstd because we know body structure before
seeing bytes.

**Header-only fast path**: our 56-byte header (§4.1) is fixed-format
and zero-copy. Receive-side routing decisions (drop, ack-only,
forward-to-replica) don't need body parse:

- Dedupe lookup uses header `event_id` (bytes 12-27).
- Authority pre-rejection on `subject_id` without body parse.
- HLC validation uses header alone.

Bad frames are rejected at <1µs without allocation. DoS resistance is
structurally stronger than hyperscale's parse-then-decide approach.

**Forward error correction on UDP for medium frames**: for Bulk-class
in 1-64KB range over lossy WAN, naive UDP retransmits induce HoL
blocking; TCP/QUIC adds round-trip latency. Reed-Solomon FEC
(`klauspost/reedsolomon`) at the protocol layer:

- Split frame into k data + m parity shards.
- Send all k+m as separate UDP datagrams.
- Receiver decodes once any k arrive.
- Tolerates up to m simultaneous shard losses with **zero round trips**.

For cross-DC bulk replication: 2-5x effective throughput improvement
under lossy peering. Per-flow state minimal (k, m, frame ID).

**Bloom-filter interest broadcasts**: for massive fan-out (§27.4),
"send what changed" doesn't scale. Subscribers periodically broadcast a
Bloom filter of subject IDs they want; publishers only send to nodes
whose filter shows interest. For 100K subscribers across 1000 subjects,
avoids the O(subscribers × publish_rate) message floor that plain
pub/sub hits.

**Coalesced ack/credit/HLC piggybacking**: hyperscale piggybacks gossip
on probe acks. We extend: a single QUIC frame carries (ack-batch +
credit-advertisement + HLC-tick + Bloom-interest-update + skew-telemetry)
coalesced. Receivers process all in one allocation-free header read.
One frame replaces what hyperscale needs 4-5 separate frames for.

**Schnorr signature aggregation**: for batched delivery (snapshot
install with thousands of frames), per-frame Ed25519 signing is
wasteful. Schnorr signatures aggregate (`cloudflare/circl`): N
signatures → one combined signature verifiable in one operation.
Receive-side batch verify is O(1) instead of O(N).

**RDMA for intra-DC zero-copy** (where hardware permits): RoCEv2 NICs
with verbs API (`rdma-core` Go bindings) deliver zero-copy reads
between hosts in the same rack at ~500ns. Substrate transport gets a
fifth implementation; falls back to QUIC when RDMA unavailable. Same
wire format; storage layer reads pages directly into peer memory.
Particularly impactful for Raft replication of bulk-class subjects
within a DC.

**Network coding cross-DC**: linear codes (extended FEC) mix multiple
frames into encoded packets; receiver decodes when enough received.
Tolerates packet loss without retransmission; reduces tail latency by
removing HoL blocking on retransmits. Standard per-DC pair, opt-in
per subject for high-rate cross-DC subjects.

### 4.7 What we are not building

These are explicit non-goals for the protocol layer, to be precise:

- **Custom L4 protocol replacing QUIC**: QUIC is the right choice; we
  ride on it, we don't replace it.
- **Lossless TCP fallback**: QUIC over UDP works on every modern
  network; environments that block UDP get TLS-over-TCP. No custom
  TCP framing.
- **Application-level flow control above QUIC**: QUIC's per-stream and
  per-connection flow control suffices; per-class credit advertisement
  (§10.1) sits above QUIC, not under it.

---

## 5. Layer 2 — Membership and Failure Detection (SWIM++)

The membership layer answers: *who is alive, where are they, can I reach them?*
We borrow hyperscale's SWIM stack wholesale and extend it.

### 5.1 SWIM components borrowed from hyperscale

These are battle-tested in `../hyperscale/hyperscale/distributed/swim/` and are
ported as-is:

| Component | Source | Purpose |
|-----------|--------|---------|
| Incarnation numbers | `detection/incarnation_*.py` | Per-node monotonic counter; refutes stale rumors |
| Indirect probes | `detection/indirect_probe_manager.py` | Probe via K random witnesses when direct fails |
| Suspicion timer | `detection/suspicion_*.py` | Two-phase failure: suspect → confirmed |
| Timing wheel | `detection/timing_wheel.py` | O(1) scheduling of probe deadlines |
| Piggyback gossip | `gossip/piggyback_update.py` | State updates ride on probe acks |
| φ-accrual + LHM | `health/local_health_multiplier.py` | Confidence-based liveness, adapts to network jitter |
| Flapping detector | `leadership/flapping_detector.py` | Suppress nodes that oscillate up/down |
| Vivaldi coordinates | `swim/coordinates/coordinate_*.py` | Latency-aware peer selection |
| Federated health | `health/federated_health_monitor.py` | Cross-cluster health propagation |
| Out-of-band channel | `health/out_of_band_health_channel.py` | Non-SWIM liveness signal |
| Graceful degradation | `health/graceful_degradation.py` | Behavior under partial cluster loss |

### 5.2 Hierarchical liveness levels

SWIM tracks node liveness. We extend to **four orthogonal levels**, each with
its own probe cadence and failure semantics:

```
┌──────────────────────────────────────────────────────────────┐
│  Level             Probed by              Cadence    Action  │
│  ─────             ─────────              ───────    ──────  │
│  Node              Cross-DC SWIM peers    1s         Cluster │
│                                                       repart │
│  Pod               Intra-DC SWIM peers    250ms      Pod     │
│                                                       restart│
│  Agent             Local pod runtime      50ms       Agent   │
│                                                       restart│
│  Session           Owning agent           100ms      Session │
│                                                       failover│
└──────────────────────────────────────────────────────────────┘
```

Each level has its own φ score. A node with healthy pods can have one sick
agent; the substrate restarts the agent without affecting peers. Conversely,
a healthy agent on a sick node is migrated.

Decisions gated by which level failed:

| Failure level | Triggers |
|---------------|----------|
| Session | Session failover to standby pod; cursor handoff |
| Agent | Agent restart in same pod; in-flight claims requeued |
| Pod | Pod restart on same node; VFS replicas reattached |
| Node | Cluster repartition; namespace replicas promoted on other nodes |

### 5.3 Sectioned Vivaldi coordinates

Vivaldi gives latency-distance estimates. Hyperscale uses flat coordinates;
we use **sectioned coordinates** that respect topology.

Each node's coordinate is a triple `(intra-rack, intra-DC, cross-DC)`. The
section used for peer selection depends on the operation:

```
┌──────────────────────────────────────────────────────────────────┐
│ Operation                          Coordinate section            │
│ ─────────                          ─────────────────             │
│ Intra-pod control message          intra-rack                    │
│ Intra-DC SWIM probe                intra-rack or intra-DC        │
│ Namespace Raft replica selection   intra-DC                      │
│ Cross-DC replication               cross-DC                      │
│ TUI gateway selection              cross-DC (then intra-DC)      │
└──────────────────────────────────────────────────────────────────┘
```

Same node embedding, different distance metric per operation.

### 5.4 Merkle reconciliation on rejoin

When a node returns from a partition, it doesn't replay the full gossip
stream. Both sides exchange Merkle roots of their membership view; symmetric
difference is computed via Merkle search tree, and only divergent leaves are
exchanged.

```
┌─────────────────────── Rejoin sequence ────────────────────────┐
│                                                                 │
│ 1. Node A (rejoining)        Node B (cluster member)           │
│       │                              │                          │
│       │  PROBE_ACK  ───────────────► │                          │
│       │  (HLC, incarnation)          │                          │
│       │                              │                          │
│       │  ◄────────  RECONCILE_REQ    │                          │
│       │             (Merkle root)    │                          │
│       │                              │                          │
│       │  Compute symmetric diff      │                          │
│       │  via Merkle search tree      │                          │
│       │                              │                          │
│       │  RECONCILE_RESP ───────────► │                          │
│       │  (divergent leaves only)     │                          │
│       │                              │                          │
│       │  ◄──────  RECONCILE_RESP     │                          │
│       │           (their divergent   │                          │
│       │            leaves)            │                          │
│       │                              │                          │
│       │  Merge; both sides converge  │                          │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

Bounded by *what actually changed*, not by partition duration. A node away
for an hour where 5 peers changed state exchanges 5 leaves, not the full
gossip log.

### 5.5 Dual-channel suspicion

A node is **suspect** only when both the SWIM probe AND the QUIC keepalive
disagree with the node's self-claimed liveness. Single-channel suspicion is a
common false-positive (a flaky NIC on the SWIM path); dual-channel rejects
this class.

```
┌────────────────────────────────────────────────────────────────┐
│  Channel state           SWIM ok      SWIM fail                │
│  ───────────────         ───────      ─────────                │
│  QUIC ok                 alive        possibly suspect         │
│  QUIC fail               possibly     SUSPECT (dual-channel)   │
│                          suspect                                │
└────────────────────────────────────────────────────────────────┘
```

"Possibly suspect" triggers retry/indirect-probe; only "SUSPECT" promotes to
the suspicion timer. False-positive rate roughly squared.

### 5.6 Probe cycle diagram

```
┌──────────────── SWIM probe cycle (typical) ──────────────────┐
│                                                                │
│  Probe interval = 1s (cluster) / 250ms (intra-DC)             │
│                                                                │
│  t=0     Node A picks random peer B                           │
│           A → B   PROBE                                        │
│                                                                │
│  t=50ms  ┌─ B responds: A → alive, piggyback gossip applied   │
│          └─ B silent: A increments LHM                        │
│                                                                │
│  t=200ms If B silent: A → K random peers C1..Cn               │
│           A → Ci  PROBE_INDIRECT(B)                           │
│           Ci → B  PROBE                                        │
│                                                                │
│  t=400ms ┌─ Any Ci → B → Ci → A: alive (piggyback gossip)    │
│          └─ All Ci timeout: A marks B SUSPECT, gossips         │
│                                                                │
│  t=400ms Suspicion timer starts (default 5s)                   │
│   to     B can refute via piggybacked alive message            │
│  t=5.4s  with higher incarnation                               │
│                                                                │
│  t=5.4s  If unrefuted: A marks B FAULTY, gossips               │
│           Cluster removes B from membership view               │
└────────────────────────────────────────────────────────────────┘
```

---

## 6. Layer 3 — Consensus (Multi-Raft)

Single global Raft is a scaling cliff: throughput is leader-bound, all
operations contend on one log. We use **multi-Raft** with three group classes.

### 6.1 Group taxonomy

```
┌─────────────────────────────────────────────────────────────────┐
│                       Operator Group                             │
│             (3 replicas, manages multi-Raft itself)              │
│             - group creation / dissolution                       │
│             - member transitions                                 │
│             - namespace migrations                               │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                  Cluster-Topology Group                          │
│           (5-7 replicas, spread across DCs)                      │
│           - cluster membership canon                             │
│           - subject registry                                     │
│           - durability policies                                  │
│           - authority profiles                                   │
│           - Vivaldi seed coordinates                             │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│           Per-Namespace Control Groups (many)                    │
│  (3 replicas per group, members rendezvous-hashed)               │
│                                                                  │
│   ┌───────────────┐  ┌───────────────┐  ┌───────────────┐       │
│   │ session       │  │ session       │  │ session       │       │
│   │ s-abc123      │  │ s-def456      │  │ s-xyz789      │       │
│   └───────────────┘  └───────────────┘  └───────────────┘       │
│                                                                  │
│   ┌───────────────┐  ┌───────────────┐  ┌───────────────┐       │
│   │ fleet         │  │ fleet         │  │ subsystem     │       │
│   │ architect     │  │ engineer      │  │ forest-global │       │
│   └───────────────┘  └───────────────┘  └───────────────┘       │
└─────────────────────────────────────────────────────────────────┘
```

Routing: a subject's namespace component selects the namespace group; group
membership is rendezvous-hashed over the cluster's nodes (selecting 3 nodes
weighted by Vivaldi proximity within the same DC where possible).

### 6.2 Why multi-Raft

- Throughput scales with namespace count, not with cluster size.
- Fault domains are scoped: a failed namespace group doesn't affect others.
- Membership operations on one namespace don't interrupt others.
- Migration: a namespace can move (e.g., to balance load) by adding new
  replicas, syncing, removing old replicas — only that group is affected.

### 6.3 Refinements over a textbook Raft

| Refinement | What it does | Why |
|------------|--------------|-----|
| **Pre-vote always** | Candidates send pre-vote round before incrementing term | Prevents disruptive elections when a partitioned-then-rejoined node has stale high term |
| **Joint consensus** | Membership transitions go through a transitional config (Cold → Cold,New → New) | Handles concurrent membership changes correctly; the simpler one-at-a-time variant has known edge cases |
| **Read-index reads** | Linearizable reads via index handshake instead of leader leases | Resilient to clock skew; no false-positive leader failure |
| **Content-addressed log entries** | Log entry stores `(term, index, blake3, body_ptr)`; bodies dedup'd | Re-propose-after-leader-change doesn't double-store the same payload |
| **Streaming Merkle snapshots** | Snapshot install exchanges Merkle nodes, receiver requests only what it lacks | Stale-replica catchup proportional to actual divergence, not snapshot size |
| **Per-class fsync policy** | Critical entries fsync immediately; bulk uses group commit | Latency for hot paths, throughput for cold paths |
| **Quorum-aware backpressure** | Leader stops accepting proposals when follower backlog grows beyond threshold | Prevents follower OOM under extreme write load |

### 6.4 Cross-namespace operations

The common case is namespace-local: a session's claims, forest events, and
VFS commits are all in *that* session's namespace group. No cross-namespace
coordination needed.

For genuine cross-namespace operations:

**MultiNamespaceTx** — 2PC across at most N namespaces. The coordinator is
chosen deterministically (lexicographic min of namespace IDs) so no
coordination is needed to pick one. Implements full prepare/commit/abort with
participant timeout, rollback, and recovery on coordinator failure.

```
┌─────────── Cross-namespace 2PC ──────────────────────┐
│                                                       │
│ Coordinator (chosen by lex-min namespace ID)         │
│      │                                                │
│      ├─► Participant N1: PREPARE(tx_id, ops_for_N1) │
│      ├─► Participant N2: PREPARE(tx_id, ops_for_N2) │
│      ├─► Participant N3: PREPARE(tx_id, ops_for_N3) │
│      │                                                │
│      ◄──── PREPARE_OK / PREPARE_FAIL                 │
│                                                       │
│   ┌── all ok ──► COMMIT to all                       │
│   │                                                   │
│   └── any fail ─► ABORT to all                       │
│                                                       │
│   Each participant durably logs PREPARE in its own   │
│   Raft group before responding, so coordinator       │
│   failure is recoverable.                            │
└───────────────────────────────────────────────────────┘
```

For unbounded N (e.g., broadcast updates touching every namespace),
MultiNamespaceTx falls back to **escrow + compensation**: the coordinator
publishes a "reservation" to each participant; participants apply at-leisure;
if the coordinator declares the operation aborted, participants apply
compensating writes.

### 6.5 Membership transitions

```
┌────── Adding a replica via joint consensus ──────────┐
│                                                       │
│ Old config: {N1, N2, N3}                              │
│ Goal:       {N1, N2, N3, N4}                          │
│                                                       │
│ Phase 1: Joint config {N1, N2, N3, N1, N2, N3, N4}   │
│   - Quorum is majority of OLD (≥2 of 3)              │
│       AND majority of NEW (≥3 of 4)                  │
│   - N4 begins log replication                         │
│                                                       │
│ Phase 2: When N4 caught up, transition to NEW only   │
│   - New config: {N1, N2, N3, N4}                      │
│   - Quorum is majority of NEW (≥3 of 4)              │
│                                                       │
│ During Phase 1, both old and new majorities required │
│  → no possibility of split-brain                     │
└───────────────────────────────────────────────────────┘
```

### 6.6 MultiNamespaceTx abort cleanup, recovery, and telemetry

§6.4 specifies the happy path. Abort behavior, coordinator-failure
recovery, and observability are normative below.

**Abort decision sources**:

1. **Coordinator-decided abort**: any participant returns
   PREPARE_FAIL (constraint violation, quota exceeded, conflict
   detected). Coordinator commits ABORT to its Raft, then broadcasts
   ABORT to all participants.
2. **Participant-decided abort**: participant times out waiting for
   COMMIT after PREPARE-OK (default 30s for Critical, 5min for Bulk).
   Participant unilaterally aborts; reports to coordinator.
3. **Coordinator failure mid-2PC**: participants stuck in PREPARE
   state. Recovery protocol below.

**PREPARE-state isolation at participant**:

Each engine writes PREPARE state to a *staging keyspace*, not to the
live state. Staging is HLC-stamped, signed, content-addressed by
`tx_id`. Visible only to the transaction itself (other transactions
don't see staged state). Specifically:

- **SQLite/turso engine**: staged WAL frame held in
  `staging/<tx_id>/wal-frame` until COMMIT.
- **KV engine**: per-key locked + staged value held in
  `staging/<tx_id>/kv`.
- **Object engine**: blob written with `staging_id`; object index
  *not* yet updated.
- **CRDT engine**: PREPARE buffers the merge operation; not yet
  applied to live merged state.

**Cleanup at participant on ABORT**:
- Discard staging keyspace entries for `tx_id`.
- Release locks / scope claims.
- Emit `tx.aborted{tx_id, reason, hlc}` to the namespace's tx-log
  subject.
- If the participant emitted subscriber-visible side effects pre-
  abort (rare, requires opt-out of default isolation): run
  registered compensation hooks in reverse order.

**Cleanup at coordinator on ABORT**:
- Mark transaction in coordinator's Raft state as `ABORTED` with
  `(reason, hlc, participant_outcomes)`.
- Emit `tx.aborted` to coordinator's tx-log.
- GC after retention.

**Recovery from coordinator failure**:

Participants stuck in PREPARE poll the coordinator's namespace via
cursor every K seconds (default 1s):

1. If cursor shows `tx.committed` for `tx_id` → apply COMMIT.
2. If cursor shows `tx.aborted` → apply ABORT.
3. If neither and coordinator unreachable past
   `participant_recovery_timeout` (default 60s) → participants run
   quorum-based decision:
   - Poll all known peer participants for their state.
   - If quorum confirms PREPARE-OK and confirms having received the
     original PREPARE → presume-commit fallback (rare, requires
     operator policy enable for "presume-commit safe").
   - Otherwise → presume-abort (default).
4. Forensic event published regardless of outcome:
   `sylk://global/multitx-recovery/v1` records the recovery decision
   with full context.

**Idempotency**: `tx_id = BLAKE3(coordinator_id || sequence_in_session
|| logical_op_hash)`. Re-issuing the same logical operation produces
the same `tx_id`:
- Already committed → short-circuits; cached result returned.
- Already aborted → caller gets the original abort reason; can issue
  new transaction with new `tx_id`.

**Telemetry**:

- Per-tx span in §28.1 OTel: spans for BEGIN, PREPARE (per-
  participant), COMMIT, ABORT, recovery.
- Audit subject `sylk://global/multitx-audit/v1`: every commit/abort
  decision with reason, participant list, coordinator ID, HLC
  timestamps, blast-radius (how many subjects affected).
- Per-cluster Prometheus metrics:
  - `multitx_total{result}` — committed, aborted, recovered
  - `multitx_duration_seconds{result}` — histogram
  - `multitx_participants_count` — histogram
  - `multitx_abort_cause{cause}` — `prepare_fail`, `timeout`,
    `coordinator_failure`, `conflict`, `quota`, `force_abandoned`
  - `multitx_recovery_outcomes{outcome}` — `presume_commit`,
    `presume_abort`, `coordinator_recovered`
- Causal cone (§12.2) traversal: aborts queryable by cause. "Show me
  all aborts in the last hour caused by `prepare_fail` from
  participant X" is a cone walk.

**Compensation hooks** (rare; only for transactions that emit
subscriber events pre-abort): compensation registered at PREPARE
time. On ABORT, runs in reverse order. Saga-equivalent semantics for
the subset of cross-namespace transactions that need them.

**Soundness invariants**:
- **Atomicity**: COMMIT happens at all participants or none.
- **Durability**: a participant that returned PREPARE-OK can serve
  COMMIT or ABORT correctly across crashes.
- **Idempotent recovery**: replaying the abort cleanup after a crash
  is a no-op if already cleaned up.

### 6.7 Cross-substrate transactional semantics (cross-namespace + cross-engine)

§6.4 covers cross-namespace 2PC. §G.1 (and §11.9 multi-engine
transactions) covers cross-engine transactions within a namespace.
What about transactions spanning *both* — e.g., a SQLite write in
namespace A, a KV write in namespace B, an object put in namespace
C? Two-level 2PC.

**Transaction graph model**: model the transaction as a directed
graph of `(namespace_id, engine, operation)` tuples with optional
dependencies (FK constraints, expect-frontier dependencies).
Coordinator chosen as
`lex_min(namespace_id || engine_id)` deterministically.

**Two-level 2PC**:

```
┌── Level 1 (cross-namespace) ────────────────────────┐
│                                                       │
│  Coordinator (Raft group of lex-min namespace)       │
│      │                                                │
│      ├─► Participant ns_A: PREPARE (Level 2 ops)    │
│      │                                                │
│      ├─► Participant ns_B: PREPARE (Level 2 ops)    │
│      │                                                │
│      └─► Participant ns_C: PREPARE (Level 2 ops)    │
│                                                       │
│  Each participant runs its own Level 2 2PC:         │
│                                                       │
│      ┌── Level 2 (cross-engine within namespace) ──┐ │
│      │  ns_A.coordinator                            │ │
│      │      │                                        │ │
│      │      ├─► sqlite engine: PREPARE             │ │
│      │      ├─► kv engine: PREPARE                  │ │
│      │      └─► object engine: PREPARE              │ │
│      └─────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────┘
```

Both levels must commit. Either level's ABORT cascades up
(Level 2 abort → Level 1 abort) and down (Level 1 abort → all
Level 2 abort).

**Engine-specific PREPARE semantics**: identical to §6.6 staging
keyspace pattern.

**HLC-deterministic apply at COMMIT**:
- Level 1 coordinator chooses `commit_hlc` via Raft commit.
- Broadcasts COMMIT to Level 1 participants with `commit_hlc`.
- Each Level 1 participant's coordinator broadcasts to Level 2
  participants with the same `commit_hlc`.
- Each engine's apply path runs at `commit_hlc`, deterministically
  ordered. Same operations + same HLC → same final state across
  replicas.

**Cross-engine consistency invariants**:

- **Atomicity**: COMMIT happens in all participating engines or
  none.
- **Isolation**: PREPARE state invisible to non-participants under
  default isolation; configurable to weaker isolation only with
  explicit opt-in per-subject.
- **Determinism**: replicas converge to bit-equal state given the
  same transaction graph + same `commit_hlc`.
- **Causality**: writes within a transaction share `commit_hlc`;
  reads see snapshot at that HLC.

**Read-write set tracking**: each engine reports its read-set +
write-set at PREPARE. Coordinator aggregates for distributed BEGIN
CONCURRENT (§B.3) conflict detection across the full transaction
graph.

**Nested transactions / savepoints**: explicit nesting via
savepoints. Savepoint = sub-transaction with own staging. COMMIT of
outer requires all inner saves committed. ROLLBACK to savepoint
discards inner staging only.

**Recovery from crashes**:

- **Engine crash mid-PREPARE**: staging keyspace persisted via Raft;
  on restart, engine reads its staging keyspace; rolls back any
  incomplete PREPAREs (those without matching COMMIT).
- **Engine crash mid-COMMIT**: COMMIT idempotent (single Raft
  entry); re-applies on restart.
- **Coordinator crash**: same recovery as §6.6.
- **Cross-namespace partition**: Level 1 participants stuck in
  PREPARE; recovery via deterministic timeout + presume-abort with
  operator override available.

**Performance**:

- Single-namespace single-engine: degenerate, 1 RTT (no 2PC
  protocol; treated as a regular Publish).
- Single-namespace multi-engine: Level 2 only; 1 RTT for engine
  coordination.
- Multi-namespace single-engine per ns: Level 1 only; 2 RTT.
- Multi-namespace multi-engine: 2 RTT total (Level 2 PREPARE bundled
  with Level 1 PREPARE; Level 2 COMMIT bundled with Level 1 COMMIT).

**Saga decomposition for high participant counts**: configurable cap
(default 32 participants). Beyond cap, the transaction MUST
decompose into a saga (§26.1) with explicit compensations per step.
Loses atomicity, gains liveness; appropriate for long-running
cross-cluster operations.

**Cross-cluster (federation) transactional extensions**:
- Federation peers can participate via §20.1 federation control
  plane + §31.24 cross-domain causal algebra.
- Cross-cluster PREPARE has higher latency (federation RTT); opt-in
  per subject (`federation_transactional=true` schema flag).
- Default: no cross-cluster transactions; reach across federation
  via sagas + compensation.

**Telemetry**: per-tx-graph metrics include Level 1 participant
count, Level 2 participants per Level 1, engine mix, HLC commit
latency, cross-engine PREPARE / COMMIT / ABORT latency
distributions. Cross-engine causal cone via §12.2: every tx
queryable with full participant graph + outcome.

**Soundness theorem** (machine-checked alongside §6.4):

> For any committed transaction T spanning engines E_1...E_n across
> namespaces NS_1...NS_m: ∀ replica R of any participating
> namespace, R observes T's effects atomically at HLC
> `T.commit_hlc` with the same final state as every other replica
> of that namespace. The state at `HLC ≥ T.commit_hlc` reflects all
> of T's writes; at `HLC < T.commit_hlc` reflects none.

---

## 7. Layer 4 — The Durable Log (Causal Merkle DAG)

This is the substrate's central data structure. NATS streams are flat; Sylk
streams are causal DAGs.

### 7.1 Entry structure

```go
type Entry struct {
    HLC                HLC          // total order with happens-before
    ParentHLCs         []HLC        // direct causal parents observed by publisher
    BodyHash           [32]byte     // BLAKE3-256 of canonical body
    SubjectID          uint64
    SessionID          [16]byte     // UUIDv7
    AuthorityToken     [64]byte     // Ed25519 signature over header+body
    DedupeEventID      [16]byte     // UUIDv7 — mandatory
    DedupeFingerprint  [32]byte     // BLAKE3-256 over canonical body — mandatory
    DedupeIdemKey      []byte       // optional caller-supplied
    Body               []byte       // CBOR
}
```

Every entry references its causal parents — typically:
- The publisher's last entry on this subject (continuity)
- Any other entries the publisher had observed and reasoned about (causal
  influence)

This makes the log a directed acyclic graph, not a sequence.

### 7.2 The unification

| Today (scattered) | After substrate (unified subject) |
|-------------------|-----------------------------------|
| `core/claims/board_durable.go` | `sylk://session/<id>/claims/v3` |
| `core/forest/` event ledger | `sylk://session/<id>/forest-events/v1` |
| `core/activity/activitystore` | derived view over multiple subjects |
| `core/versioning/commit_queue.go` ControlWAL | `sylk://session/<id>/vfs-commits/v2` |
| `core/versioning/copy_retention.go` ControlWAL | `sylk://session/<id>/copy-retention/v1` |
| `core/agentlog/` | `sylk://session/<id>/agent-log/v1` |
| `core/buslog/` | derived view over all subjects |

The fabric (`core/fabric/`) becomes a **read-side projection** over those
subjects via lenses, exactly as `docs/CLAIMS.md` already prescribes for
sovereign-store + fabric projection.

### 7.3 Storage layout per subject

```
~/.sylk/data/<namespace>/<subject>/
  ├── active/
  │   ├── segment-0042.log      ← mmap, append-only, current segment
  │   └── segment-0042.idx      ← LSM index for active segment
  ├── sealed/
  │   ├── segment-0001.log      ← immutable, content-addressed
  │   ├── segment-0001.merkle   ← Merkle tree over entries
  │   ├── segment-0001.idx      ← LSM index by HLC, body_hash, dedupe_*
  │   ├── segment-0002.log
  │   ├── segment-0002.merkle
  │   └── ...
  ├── snapshots/
  │   ├── snapshot-hlc-X.bin    ← state-machine snapshot at HLC frontier
  │   └── snapshot-hlc-X.merkle
  ├── manifest.json             ← list of segments, sealed roots, retention
  └── tombstones.log            ← retention-policy-driven deletions, signed
```

**Active segment**: mmap, append-only. Writes batched per fsync window (default
2ms or 1MB, whichever first). On fsync, batch is durable; if the segment
exceeds size threshold (default 64MB), it's sealed.

**Sealed segments**: immutable. Content-addressed by Merkle root. The Merkle
root is the segment's canonical name. Indexes stored alongside.

**Indexes** (LSM-style, RocksDB-class implementation):

| Index | Maps | Used by |
|-------|------|---------|
| HLC index | HLC → entry offset | resume from cursor, time-travel |
| body_hash index | BLAKE3 → entry offset | content-addressed replay, dedupe |
| dedupe_event_id index | UUIDv7 → entry offset | dedupe layer 1 |
| dedupe_fingerprint index | BLAKE3 → entry offset | dedupe layer 2 |
| dedupe_idem_key index | string → entry offset | dedupe layer 3 |
| session_id index | UUIDv7 → entry list | session-scoped queries |
| parent index | HLC → child entries | causal cone queries |

**Snapshot**: per-subject state-machine snapshot at HLC frontier `F`. Used
for replay-from-snapshot; consumers can resume from a snapshot's HLC instead
of replaying from the beginning.

### 7.4 Compaction and retention

Compaction is **never destructive by default**. Sealed segments can move to
cold storage (S3, etc.) but the Merkle root is immortal.

Retention is per-subject policy:

| Profile | Retain duration | Retain trigger |
|---------|-----------------|----------------|
| forever | ∞ | (no deletion) |
| audit | 7 years | (compliance) |
| operational | 90 days | sealed segment age |
| session | session lifetime | session close |
| ephemeral | 24 hours | sealed segment age |
| viewport | 5 minutes | sealed segment age (TUI render projections) |

Operators can authorize segment deletion; deletion writes a signed tombstone
to `tombstones.log` recording the policy authorization. The history's audit
trail is preserved as "we deleted segment X at HLC Y per policy P with
authorization signature S."

### 7.5 Causal DAG diagram

```
┌────────────────── Causal DAG for one subject ──────────────────┐
│                                                                  │
│   Publisher A                Publisher B           Publisher C   │
│       │                          │                      │        │
│       ▼                          ▼                      ▼        │
│   ┌─[E1]─ HLC=10                                                 │
│   │ parents=∅                                                    │
│   └──┬───                                                        │
│      │                                                            │
│      │       ┌─[E2]─ HLC=15                                      │
│      │       │ parents={E1}                                       │
│      │       └──┬─                                                │
│      │          │                                                 │
│      ▼          │                ┌─[E3]─ HLC=18                  │
│      │          │                │ parents={E1}                   │
│      │          │                └──┬─                            │
│      │          │                   │                             │
│      ▼          ▼                   ▼                             │
│   ┌─[E4]─ HLC=22                                                 │
│   │ parents={E2, E3}    ◄── merge: A observed both B and C      │
│   └──┬───                                                        │
│      │                                                            │
│      ▼                                                            │
│      ...                                                          │
│                                                                   │
│  Entries form a DAG, not a sequence. Replay from cursor C        │
│  (HLC frontier {A:22, B:15, C:18}) walks forward in topological  │
│  order, returning entries causally after C.                      │
└──────────────────────────────────────────────────────────────────┘
```

### 7.6 Subject deletion semantics

§7.4 covers retention-policy expiry. Explicit subject deletion is a
distinct operation with three levels of strength.

**Three deletion levels**:

| Level             | Effect                                      | Reactivatable? | Audit trail |
|-------------------|---------------------------------------------|----------------|-------------|
| Retention expiry  | Data ages out per profile                   | N/A (data gone via policy) | Signed tombstone in `tombstones.log`; Merkle proofs preserved |
| Soft delete       | Subject marked `deleted` at HLC X; pubs/subs refused; data retained per retention | Yes, within retention window | `subject.soft_deleted` entry, signed by operator authority |
| Hard delete       | Per-subject DEK destroyed; data unrecoverable | No (terminal)             | `subject.hard_destroyed` entry with verifiable destruction proof |

**Soft delete flow**:

1. Operator publishes `subject.soft_deleted{subject_id, at_hlc,
   reason}` to `sylk://global/subject-lifecycle/v1` with operator
   authority.
2. Substrate accepts; new publishes refused with
   `ErrSubjectSoftDeleted`. New subscriptions refused.
3. Existing subscribers receive `subject.deletion_event` on their
   cursors at the delete-HLC.
4. Cursors past delete-HLC return `ErrSubjectSoftDeleted`.
5. Data retained per the subject's retention policy from delete-HLC.
6. Reactivation: operator publishes `subject.reactivated{subject_id,
   at_hlc, reason}` within the retention window. New publishes
   resume; subscribers receive reactivation event.

**Hard delete flow**:

1. Operator publishes `subject.hard_destroy_requested{subject_id,
   at_hlc, reason, witnesses}` requiring M-of-N operator signatures.
2. Substrate validates authority; emits the request entry.
3. Per-subject DEK destruction via KMS (§22.4):
   - Substrate calls `KMS.ScheduleKeyDeletion(dek_id, 7d)` (7-day
     soft-delete window in KMS).
   - Substrate publishes `subject.hard_destroyed{subject_id, hlc,
     destruction_proof}` where `destruction_proof =
     {kms_destruction_receipt, witness_signatures, hlc}`.
4. Sealed segments retained briefly (Merkle proofs still verify
   their subtrees); data unreadable post-destruction because DEK is
   gone.
5. Eventual physical deletion of segments per retention; segment
   metadata + Merkle roots remain forever (preserves historical
   audit chain).
6. Cursors past destroy-HLC return `ErrSubjectHardDestroyed`.

**Tombstone Merkle integrity**:

Tombstones (soft + hard) are themselves entries in the substrate's
causal Merkle DAG (§7). They're content-addressed, signed,
HLC-stamped. The fact-of-deletion is preserved permanently even when
data is gone.

**Cascade protocol**:

- **Subscribers** (Layer 5 cursors): deletion event delivered via
  existing cursor mechanism; subscribers get `Err...Deleted` on next
  read past delete-HLC. Cursor invalidation event emitted.
- **Backups (§21.3)**: deletion propagates as backup metadata
  update. Object-locked backup objects respect their compliance
  retention (which may exceed substrate retention); new backups
  don't include destroyed-subject data.
- **Federation peers (§20.1)**: deletion replicates as federation
  control entry. Peer policy declared at federation pairing time:
  - `accept_cascade`: peer hard-deletes locally too.
  - `retain_local_policy`: peer keeps replicated copy until peer's
    own retention expires; peer's audit shows "received hard-delete
    from cluster X at HLC h; retained per local policy until HLC y."
  - Per-pair sovereignty preserved.
- **Persistence consumers** (Forest, KG, Doc DB, Bleve): each
  receives delete event; updates indexes per per-consumer protocol:
  - Forest archives the chain as anti-precedent before removing
    semantic content.
  - KG removes nodes + edges referencing the deleted subject.
  - Doc DB tombstones documents.
  - Bleve removes index entries.

**Audit invariants**:

- Soft delete: data + audit both intact within retention window.
- Hard delete: data gone; audit chain preserves *that* the deletion
  happened, when, who, why, with destruction proof.
- Retention expiry: data gone per policy; signed policy reference in
  tombstone.

**GDPR alignment**: hard delete is right-to-erasure compliant.
Combines with §31.23 verifiable redaction for field-level erasure
within still-existing subjects (when only some rows need erasing).

**Federation conflict resolution**: peer A hard-deletes; peer B's
policy says retain. B keeps its replicated copy until B's retention
expires; B's audit shows the federation event clearly. No federation-
wide consistency for deletions; per-cluster sovereignty preserved.

**Race with in-flight publishes**: a publish initiated before delete
but landing after delete-HLC is rejected with the appropriate error;
publisher retries via §10.3 retry budget; eventual `ErrPermanent`
after retry exhaustion.

**Operator authority**:
- Soft delete: requires `subject.delete` capability.
- Hard delete: requires `subject.destroy` capability + M-of-N operator
  approval (per §30.5 cluster-wide identity-compromise patterns; same
  threshold model).
- Reactivation: requires `subject.reactivate` capability.

**Soundness invariants**:

- Once `subject.hard_destroyed` committed, no replica can produce
  the original data (DEK is destroyed).
- Once `subject.soft_deleted` committed, no new publishes succeed.
- Cursors past deletion HLC always observe deletion event before
  observing `Err...Deleted`.
- Audit chain integrity: every deletion is itself a substrate entry,
  Merkle-rooted, signed, content-addressed.

---

## 8. Layer 5 — Delivery

Pull-first consumers with content-addressed cursors.

### 8.1 Cursor structure

A cursor is **not** "I'm at offset 4731." It's a content-addressed witness
of the consumer's exact frontier:

```go
type Cursor struct {
    SubjectID    uint64
    HLCFrontier  HLC               // every entry with hlc <= this delivered
    CausalSet    BloomFilter       // entries known not to need redelivery
    ResumeProof  []MerkleHash      // proof the frontier matches a known segment
    Generation   uint64            // bumped on cursor reset (e.g., reposition)
}
```

Resume = "give me everything causally after `HLCFrontier`, excluding what's
in `CausalSet`." This survives:

- Log compaction (the Merkle proof identifies the segment regardless of
  offset rebasing).
- Snapshot installs.
- Cluster rewrites (cursor still validates against the new Merkle root if
  the entries are preserved).
- Cross-namespace migration (subject moved to a different Raft group; cursor
  re-resolves via subject registry).

### 8.2 Resume sequence

```
┌─────────────────── Consumer resume ─────────────────────────────┐
│                                                                  │
│  Consumer C                              Substrate S             │
│      │                                       │                   │
│      │  CURSOR_RESUME(cursor)                │                   │
│      ├──────────────────────────────────────►│                   │
│      │                                       │                   │
│      │                       Verify ResumeProof against current  │
│      │                       Merkle root for the subject.        │
│      │                       If valid, scan forward from         │
│      │                       HLCFrontier; filter out entries     │
│      │                       already in CausalSet (Bloom check). │
│      │                                       │                   │
│      │  DELIVERY(entry, ack_token)           │                   │
│      │◄──────────────────────────────────────┤                   │
│      │  ...                                  │                   │
│      │                                       │                   │
│      │  ACK(ack_token)                       │                   │
│      ├──────────────────────────────────────►│                   │
│      │                                                            │
│      │  (Bloom filter updated with delivered entries)            │
└──────────────────────────────────────────────────────────────────┘
```

If the cursor's `ResumeProof` doesn't match (rare — only after segment
deletion that retention authorized), the substrate returns
`CURSOR_INVALID` with a hint: "earliest available HLC for this subject is
H_min." Consumer can choose to reset its cursor to H_min and accept gap
acknowledgement.

### 8.3 Ack semantics

Three-state, mandatory:

| Ack | Meaning | Effect on inflight set |
|-----|---------|------------------------|
| `ACK` | Successfully processed | Removed |
| `NACK(reason, retry_hint)` | Failed; should retry | Re-queued with retry policy |
| `TERM(reason)` | Permanently fail; do not retry | Removed; dead-letter |

The inflight set lives in the consumer's Raft group state (consumers run on
substrate nodes, with their own namespace group). Loss of consumer node
means another instance picks up exactly the inflight set. There is no
"consumer crashed, messages were lost" mode.

Default policies:
- `ACK` deadline: 30 seconds (configurable per subject); missed deadline
  redelivers via NACK semantics.
- NACK retry: exponential backoff capped by retry budget.
- TERM: routed to per-consumer dead-letter subject.

### 8.4 Push as pull

Push delivery is implemented as a server-side pull loop with the consumer's
credit window. Push is *syntax* on top of pull. The failure model is the
same: every push has an implicit ack expectation; missed ack within deadline
redelivers.

### 8.5 Ordering rules

| Scope | Ordering guarantee |
|-------|---------------------|
| Within a subject | HLC order |
| Within a (subject, partition_key) | HLC order — same partition key, same publisher's actions are delivered in publish order |
| Across partition keys in one subject | Concurrent — different partition_keys can flow in parallel |
| Across subjects | No global ordering; consumers correlate via HLC if needed |

---

## 9. Layer 6 — Idempotency and Dedupe

### 9.1 Three layers (mandatory)

Every published frame carries three dedupe fields. None are optional.

| Layer | Field | Catches | Generated by |
|-------|-------|---------|--------------|
| 1 | `event_id` (UUIDv7) | Network retransmits of same physical send | Substrate client at publish time |
| 2 | `payload_fingerprint` (BLAKE3-256 of canonical body) | Semantic duplicates from independent sends with same content | Substrate client at publish time |
| 3 | `idempotency_key` (caller string) | Application-level duplicates ("retry of same logical operation across crash boundaries") | Application |

The third is optional in the *value* sense (caller may pass empty string),
but the field is always present in the wire form. Empty `idempotency_key`
just means "no application-level dedupe requested."

### 9.2 Lookup flow

```
┌───────────── Dedupe check on receive ─────────────┐
│                                                    │
│  1. Lookup event_id in event_id index              │
│       ─ hit → DROP (duplicate retransmit)          │
│                                                    │
│  2. Lookup fingerprint in fingerprint index        │
│       ─ hit → DROP (semantic duplicate)            │
│                                                    │
│  3. If idem_key non-empty:                         │
│     Lookup idem_key in idem_key index              │
│       ─ hit → DROP (application duplicate)         │
│                                                    │
│  4. Insert all three keys into respective indexes  │
│  5. Pass to delivery layer                         │
│                                                    │
│  Steps 1-3 are read-only and short-circuit on hit. │
│  Step 4 is durable (part of Raft commit).          │
└────────────────────────────────────────────────────┘
```

The dedupe table is part of the subject's Raft group state machine,
persisted in the LSM indexes, with TTL + windowed compaction.

### 9.3 TTL

Default per profile:

| Profile | Dedupe TTL |
|---------|------------|
| Critical (claims, VFS commits) | 30 days |
| Standard (forest events, fabric activities) | 24 hours |
| Bulk (knowledge ingestion) | 1 hour |
| Background (telemetry) | 5 minutes |

Compaction runs hourly per subject; expired entries are removed from the
dedupe indexes (but not from the log itself — the log retains entries per
its own retention policy).

### 9.4 Cross-DC dedupe (CRDT semantics)

Each DC owns the authoritative dedupe table for messages originated there.
Replicated to other DCs via gossip with **monotone-CRDT** semantics: the
dedupe table is a grow-only set per `(subject, key_type, key)`. Replication
is best-effort.

Worst case during partition: missed dedupe for retried payloads. This is
caught by the fingerprint layer because BLAKE3-256 collision probability is
negligible. So the system is exactly-once-or-near-enough across partitions
without coordination.

---

## 10. Layer 7 — Reliability

### 10.1 Wire-level credit advertisement

Receivers periodically piggyback **per-class credit** on acks and control
frames:

```
PiggybackCredit {
    Class:                MessageClass    // Critical, Standard, Bulk, Background
    CapacityUnits:        uint32          // available units this class
    QueueDepth:           uint32          // current backlog
    EstimatedDrainTimeMs: uint32          // model-derived estimate
}
```

Publishers enforce per-class flow control. NATS has connection-level
slow-consumer protection but not class-aware. The substrate's class-aware
flow control means a `Bulk` flood does not starve `Critical`.

### 10.2 Message classes

```
┌──────────────────────────────────────────────────────────────────┐
│ Class       Latency    Durability    Priority    Example         │
│ ─────       ───────    ──────────    ────────    ───────         │
│ Critical    sync       fsync per     1          claim issued,    │
│             <10ms      entry                    VFS commit       │
│                                                                   │
│ Standard    soft       group commit  2          forest event,    │
│             <100ms                              fabric activity  │
│                                                                   │
│ Bulk        eventual   group commit  3          knowledge        │
│             <10s                                ingestion        │
│                                                                   │
│ Background  best-      no fsync;     4          telemetry,       │
│             effort     periodic sync            rendered views   │
└──────────────────────────────────────────────────────────────────┘
```

Subjects declare their default class at registration; publishers may
override for individual frames within bounds (cannot promote Bulk to
Critical without authorization).

### 10.3 Retry budgets

Per `(publisher, subject)` pair: limit the *fraction* of traffic that can be
retries. Default 20%. Prevents retry storms during partial outages.

```
┌─────────────────────────────────────────────────────────────────┐
│ retry_ratio = retries / (originals + retries)                    │
│                                                                  │
│ if retry_ratio > budget_threshold:                               │
│     reject new retries with TEMPORARILY_OUT_OF_BUDGET           │
│     allow only original publishes through                        │
│                                                                  │
│ The retry rate falls; budget recovers; retries resume.          │
└──────────────────────────────────────────────────────────────────┘
```

Hyperscale's `retry_budget_manager` pattern ported.

### 10.4 Circuit breakers

Per `(subject, consumer)` pair. When NACK rate exceeds threshold, deliveries
to that consumer for that subject open the circuit:

```
┌────────────────── Circuit breaker states ──────────────────┐
│                                                              │
│  CLOSED ─── nack_rate > threshold ──► OPEN                  │
│    ▲                                    │                    │
│    │                                    │                    │
│    │   probe_succeeds (HALF_OPEN test) │  open_duration      │
│    │                                    │  elapsed           │
│    │                                    ▼                    │
│  HALF_OPEN ◄────────────────────── (await test probe)        │
│                                                              │
│  In OPEN: deliveries paused; queue grows up to bound;        │
│  load-shed by class when bound exceeded.                     │
└──────────────────────────────────────────────────────────────┘
```

### 10.5 Best-effort tier

Some messages are fire-and-forget (ambient envelope refresh, telemetry,
rendered viewport updates). They:

- Bypass durability (no fsync)
- Obey backpressure (still respect credit advertisement)
- Drop under load shed
- Have no ack expectation

Hyperscale's `best_effort_manager` pattern. Exists for performance, not
correctness — operations that must persist use Standard or higher.

### 10.6 Priority scheduling at every queue

| Queue | Sort key |
|-------|----------|
| Connection ingress | (class priority, HLC) |
| Replication outbound | (class priority, HLC) |
| Delivery outbound | (class priority, HLC, partition_key hash) |
| Ack queue | (class priority, HLC) |

A Bulk flood cannot starve Critical claims-board updates because every queue
is class-priority-ordered.

---

## 11. Layer 8 — Higher-level primitives

Built on the substrate, not bolted on. Each is a *kind* of subject.

### 11.1 Typed KV

Subjects with `kind=kv` give last-write-wins per key with HLC tiebreak.

```
sylk://session/<id>/kv/v1
  ├── key1: { value: ..., hlc: ..., publisher: ... }
  ├── key2: ...
  └── ...
```

Operations:
- `Put(key, value)` — publishes a `kv-put` entry
- `Get(key)` — reads current value (Raft read-index for linearizable read)
- `GetAt(key, hlc)` — reads value at historical HLC frontier
- `Watch(key)` — cursor-based subscription to changes for that key
- `Delete(key)` — publishes a `kv-tombstone` entry

### 11.2 Object Store

Subjects with `kind=object` chunk content into Merkle blobs:

```
sylk://session/<id>/object/v1
  ├── object_id: blake3_root
  │   ├── chunk[0]: blake3_hash, refcount: 3
  │   ├── chunk[1]: blake3_hash, refcount: 1
  │   └── ...
```

Operations:
- `Put(content) → object_id` — chunks via content-defined chunking, dedups
  chunks by hash, publishes `object-put` entry with manifest
- `Get(object_id) → content` — reassembles from chunks
- `Stream(object_id) → reader` — streamed read for large objects
- `Delete(object_id)` — decrements chunk refcounts; chunks GC'd when zero

### 11.3 Fabric activity store

The fabric (`core/fabric/`) becomes a read-side projection. Lenses
(`core/activity/lens.go`) are queries against substrate subjects.

```
                  ┌──────────────────────────┐
                  │  sylk://session/<id>/    │
                  │  claims/v3               │
                  │  forest-events/v1        │
                  │  vfs-commits/v2          │
                  │  ...                     │
                  └─────────┬────────────────┘
                            │
                            ▼
                  ┌──────────────────────────┐
                  │ Fabric lens (consumer)    │
                  │ projects subject deltas   │
                  │ into Activity records    │
                  └─────────┬────────────────┘
                            │
                            ▼
                  ┌──────────────────────────┐
                  │ AmbientEnvelope          │
                  │ (per-agent, bounded)      │
                  └──────────────────────────┘
                            │
                            ▼
                       to LLM tool result
```

The lens is a substrate consumer. It maintains its own cursor; resubscribes
on restart; survives all the substrate's failure modes.

### 11.4 Claims board

Already specified in `docs/CLAIMS.md`. The substrate replaces the durable
claims-board WAL with the substrate subject:

```
sylk://session/<id>/claims/v3
  - claim.issued
  - claim.accepted / claim.rejected
  - testament.submitted
  - artifact.published
  - claim.remediated
```

The board's Raft state machine reduces deliveries into the structured board
view. Lenses query the board.

### 11.5 Forest event ledger

```
sylk://session/<id>/forest-events/v1
  - branch.appended
  - branch.materialized
  - canopy.shifted
  - replay.consolidated
  - outcome.recorded
```

The forest's projector (`core/forest/projector.go`) becomes a substrate
consumer; canopy/branch/replay state lives in the namespace's Raft state
machine.

### 11.6 VFS pipeline commit log

```
sylk://session/<id>/vfs-commits/v2
  - pipeline.begun
  - pipeline.merged
  - commit.accepted (audit replica)
  - commit.rejected
  - commit.superseded
  - commit.flushed
```

The commit_resolver (`core/versioning/commit_resolver.go`) becomes a
substrate consumer; commit_queue state lives in the namespace's Raft state
machine.

### 11.7 Authority broadcast

```
sylk://global/authority/v1
  - authority.granted
  - authority.revoked
  - authority.profile_updated
```

Every node subscribes; deliveries are applied at substrate boundaries (the
publish-time authority predicate). Authority changes propagate cluster-wide
within seconds.

### 11.8 SQLite-compatible subjects (`kind=sqlite`)

> **We are replacing SQLite with our own substrate-native, SQLite-
> compatible implementation.** Turso (`../turso`) is a reference engine
> — we draw from it (page format, MVCC algorithm, CDC pragma shape,
> async I/O patterns, lazy storage) — but the production target is a
> *Sylk-owned* SQL engine that ships SQLite wire- and file-format
> compatibility plus the extensions in §11.9 / §27. Existing
> SQLite-compatible drivers and application code work unchanged. The
> storage engine, WAL discipline, replication, encryption, transaction
> layer — all substrate. The on-host coordination sidecar is `.sshm`
> (§26.8); we read turso's `.tshm` for compatibility but the
> authoritative format is ours.
>
> **What we draw from turso**: SQLite wire/file format compatibility,
> the page layout, the MVCC algorithm (Larson et al. main-memory MVCC),
> the CDC pragma shape, async I/O patterns, lazy storage. We re-
> implement these as Sylk-native code rather than embedding turso as
> a black box. This gives us first-class control of every layer +
> tight integration with substrate primitives.
>
> **What we are**: the SQL surface for Sylk's relational data. Period.
> No vanilla SQLite anywhere. No "primary engine + substrate around it"
> two-level story. One engine, one platform.

A `kind=sqlite` subject is a SQLite database whose durability,
replication, encryption, geo-fencing, and audit are all substrate-
governed.

**Two-subject pattern per database**:

```
sylk://tenant/<t>/sqlite/<db>/pages/v1   ← page deltas (large catch-up)
sylk://tenant/<t>/sqlite/<db>/cdc/v1     ← row-level change feed (live tail)
```

Both subjects are HLC-ordered; both reference the same logical revision.
Subscribers pick granularity: bootstrap via pages, switch to CDC at
caught-up frontier. Page subjects use turso's WAL frame format; CDC
subjects use turso's `capture_data_changes_conn` stream.

**Engine integration**:

- The SM apply surface is Sylk's tape (re-implementation of turso's
  `database_tape.rs` semantics in Go); each frame is a tape op.
- The substrate HLC frontier *is* the revision identifier — no separate
  string.
- `BEGIN CONCURRENT` MVCC (re-implemented in Go from turso's
  `core/mvcc/`) handles intra-replica concurrency; cross-replica goes
  through Raft.
- The CDC pragma is the canonical event source — apps see SQL, substrate
  sees CDC entries via the `cdc/v1` subject.

**Replication granularity** (improving over turso):

Turso ships pages (4KB units; conflate unrelated rows). We ship both:

- **Page deltas** for cold catch-up (large gaps; bandwidth ∝ pages-touched).
- **Row deltas** (CDC) for live tail (bandwidth ∝ rows-touched, often
  100× smaller).

Subscribers select per state. Substrate enforces both subjects converge
to identical state via deterministic SM apply.

**Schema-aware compression on the wire**: §4.6 zstd-with-schema-dict
reduces typical UPDATE wire size to 30-100 bytes vs turso's 4KB page —
50-100× bandwidth reduction for live replication.

**Predicate pushdown to replication**: subscribers declare interest as
predicates; substrate evaluates at the writer side; ships only matching
rows. Subscribers in eu-west don't pull us-west rows. Composes with
§31.19 geo-fenced CRDTs for residency at the wire.

**Sparse storage / lazy paging**: lazy page-fault path (re-implementation
of turso's `database_sync_lazy_storage.rs` in Go) serves as our
edge-tier (§20.2) and cold-tier (§21.1) page-fault path. Pages on
cold tier fetched on demand; pages on remote substrate fetched on
demand. Same code path, two backends.

**Multi-process WAL coordination**: Sylk's `.sshm` sidecar (the
authoritative on-host coordination format, §26.8) handles multiple
sylk processes on one host without a daemon. Embedded mode gets
multi-tab semantics for free. Format draws from turso's `.tshm` and
extends with HLC tail, participant SVID hashes, substrate epoch.
Turso's `.tshm` databases are readable for compat; sylk auto-promotes
to `.sshm` on first sylk write.

**Operations exposed via SQL**:

- `BACKUP DATABASE <name> TO <uri> WITH (continuous = true);` — wires
  to §21.3.
- `RESTORE DATABASE <name> FROM <uri> AT HLC '<h>';` — wires to §21.4.
- `SELECT * FROM users AS OF HLC '<h>';` — time-travel via §12.1.
- `ALTER TABLE users REDACT FIELD ssn WHERE id = 12345;` — wires to
  §31.23 verifiable redaction.

**What our engine ships that turso / SQLite do not**:

- Causally ordered (HLC) replication, not opaque revision strings.
- Multi-Raft replicated, not single-master server-pushed.
- Per-tenant encrypted via §21.2 envelope.
- Federated cross-cluster (§20.1).
- Audit-grade (§19.1 accountability + §31.16 anchoring).
- Time-travelable (§12.1) at the SQL surface (`SELECT ... AS OF HLC '...'`).
- Geo-fenced per row (§31.19).
- Tiered storage (§21.1) hot → warm → cold → archive.
- Erasure-coded WAL on cold tier (§21.6).
- Quorum-loss recoverable (§30.6).
- All of §11.9 / §27 SQL extensions (CRDT tables, per-row consistency,
  causal foreign keys, continuous queries, vector+SQL native, federated
  SQL, etc.).

**What we draw from turso (re-implemented in Go as Sylk-native code)**:
SQLite wire/file format compatibility, page layout, MVCC algorithm,
CDC pragma shape, async I/O patterns, lazy storage, deterministic-
simulation testing approach, BEGIN CONCURRENT semantics. Turso is a
reference, not a dependency.

### 11.9 SQLite envelope-pushing (beyond turso)

Features that *use* turso's engine but extend the SQL surface beyond
what turso (or any production SQL DB) ships. Each opt-in per table.

**Hybrid row + columnar dual representation**:

Same data, two layouts maintained automatically through one WAL apply:

- Primary B-tree (turso row-store) for OLTP point reads.
- Secondary columnar projection (Arrow / Parquet-shape) for OLAP scans.
- Query planner picks based on cost.

Replaces "have a separate analytics warehouse" with "same database
does both."

**Schema-aware page format**:

Custom on-disk page layout per registered schema:

- Column-grouped storage (skip irrelevant columns on scan).
- Dictionary encoding for low-cardinality columns.
- Bit-packed integers, frame-of-reference encoding.
- Backward-compat: a "compatibility view" exposes raw SQLite bytes
  when needed.

5-10× storage reduction, 10-100× scan speedup for analytical workloads,
at the cost of slightly more expensive point-write — exactly the right
tradeoff for substrate's tiered storage.

**Append-only / event-sourced tables**:

```sql
CREATE TABLE events (...) WITH (mutation = APPEND_ONLY);
```

- Substrate refuses UPDATE / DELETE.
- Compaction trivial (no tombstones).
- Time-travel queries are O(scan range), not O(rebuild from snapshot).
- Audit-grade by construction.

**CRDT tables in SQL**:

```sql
CREATE TABLE counters (
    id    TEXT PRIMARY KEY,
    value INTEGER WITH (crdt = 'pn-counter'),
    tags  TEXT    WITH (crdt = 'or-set')
);

UPDATE counters SET value = value + 1 WHERE id = 'foo';
-- multi-master writes converge without coordination
```

Maps to §26.2 typed CRDT subjects. Multi-region writes don't require
quorum. SQL interface is unchanged; semantics are CRDT under the hood.

**Per-row consistency choice**:

```sql
CREATE TABLE orders (
    id      INT PRIMARY KEY  WITH (consistency = 'linearizable'),
    metrics JSONB             WITH (consistency = 'eventual'),
    audit   TEXT              WITH (consistency = 'monotonic-read'),
    state   TEXT              WITH (consistency = 'causal')
);
```

Substrate enforces. Application picks correctness vs latency per
column. Genuinely novel: no production database does per-column
consistency.

**Distributed BEGIN CONCURRENT**:

Extends turso's single-node MVCC across the cluster:

- Transaction stamps every read with `(reader_hlc, read_set)`.
- Commit is `Publish(..., expect=read_set_frontier)` (§26.3).
- Conflict = write whose HLC is between `reader_hlc` and current HLC,
  on a key in `read_set`.
- No global lock; conflict is a local check at commit time.

Higher conflict rate than single-node MVCC but distributed multi-writer
with snapshot-isolation semantics.

**Causal foreign keys**:

```sql
CREATE TABLE testaments (
    claim_id TEXT REFERENCES claims(id) WITH (causality = 'happens-after'),
    ...
);
```

Stronger than referential integrity — *temporal* referential integrity.
A testament for claim X is invisible to a reader whose HLC frontier
doesn't include X's commit. Substrate enforces.

**Schemas as session types**:

DDL becomes substrate session types (§31.21):

```sql
CREATE PROTOCOL claim_lifecycle ON claims AS
    role architect: INSERT issued
    → role engineer: (UPDATE accepted | UPDATE rejected)
    → if accepted: role engineer*: INSERT testament
                 → role architect: INSERT artifact
    → if rejected: role architect: UPDATE remediated
                 → loop;
```

Substrate refuses writes that violate the protocol grammar at frame
boundary, not at app layer.

**Continuous queries (subscriptions returning streaming deltas)**:

```sql
SELECT customer_id, SUM(amount) FROM orders
GROUP BY customer_id
WITH (continuous = true, max_staleness_ms = 100);
```

Returns a stream of `(insert | update | delete)` deltas, maintained
incrementally via §31.4 differential dataflow. No polling. No
materialized-view refresh. No notification triggers.

**Vector + SQL native**:

```sql
CREATE TABLE docs (
    id INT PRIMARY KEY,
    text TEXT,
    embedding VECTOR(1536) WITH (index = 'hnsw', m = 16, ef = 200)
);

SELECT id, text FROM docs
WHERE embedding <-> :query_vec < 0.3
ORDER BY embedding <-> :query_vec
LIMIT 10;
```

HNSW index lives on substrate; replicates with the rest. Hybrid
retrieval (keyword + semantic) in one query plan.

**Federated SQL across federation peers**:

```sql
SELECT u.*, p.payment_status
FROM cluster('us-west').users u
JOIN cluster('eu-west').payments p ON u.id = p.user_id
WHERE p.amount > 1000;
```

Planner reasons about federation topology (§20.1), decomposes per-
cluster sub-queries, ships predicates, joins at coordinator. Authority
predicates enforce cross-cluster access. Vivaldi sections (§5.3) drive
join location.

**Topology-aware cost optimizer**:

Plan cost incorporates: network distance (Vivaldi sections), data tier
(hot/warm/cold/archive), per-class congestion budget, energy cost
(§31.10), tenant quota remaining (§22.1). Plan moves computation
toward data when cheap.

**Multi-engine atomic transactions**:

```sql
BEGIN;
  INSERT INTO orders (...);                            -- SQLite/turso engine
  KV.PUT('order_count_us-west', SQL.last_count() + 1); -- KV engine (§11.1)
  OBJECT.PUT('receipt_42', :receipt_pdf);              -- Object engine (§11.2)
COMMIT;
```

`MultiNamespaceTx` (§6.4) lets a transaction touch multiple engines
atomically. Distributed atomicity across heterogeneous storage. **No
WASM in the loop** — each engine's `Apply()` is native Go,
deterministic by §24.1, ordered by HLC.

**Probabilistic SQL columns**:

```sql
CREATE TABLE daily_metrics (
    date DATE PRIMARY KEY,
    unique_users HLL,           -- ~1KB regardless of cardinality
    request_latency TDIGEST,    -- p50/p99 in <1KB
    distinct_paths CMS          -- frequency estimates
);
```

Sketches (§26.6) as native column types; cross-DC merges are
associative. 1000× storage reduction for high-cardinality counts.

**Time-series + SQL**:

`WITH (kind = 'time-series')` triggers Gorilla compression + tag-based
indexing. Standard SQL surface; storage is time-series-optimized.
Replaces "we need a separate Influx/Prometheus."

**Privacy / compliance via SQL**:

- `ALTER TABLE users REDACT FIELD ssn WHERE id = X;` → §31.23 verifiable
  redaction.
- `CREATE TABLE users (...) WITH (residency = 'allowed_regions: [eu]');`
  → §31.19 geo-fenced rows.
- `SELECT count(*) WITH (differential_privacy = epsilon=1.0);` → DP-safe
  results with substrate budget tracking.
- `SELECT * WITH (proof = true);` → ZK proof of result correctness
  (§31.2).

**Online schema changes**:

ALTER TABLE doesn't rewrite. Old SM applies old schema; new SM applies
new (§24.2). Backward-compat reads via §31.17 transform registry. ADD
COLUMN is free; DROP COLUMN is metadata-only until compaction; ALTER
TYPE requires a declarative transform in registry. Migrations no
longer require downtime.

**Self-tuning indexes**:

Substrate observes query patterns (§28.3). Optimizer (§31.25) proposes
indexes; operator approves; substrate creates online (no table lock).

**Per-row TTL**:

```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    data JSONB,
    created_at TIMESTAMP DEFAULT NOW()
) WITH (ttl_column = 'created_at', ttl_interval = '24h', on_expire = 'redact');
```

Substrate auto-redacts (§31.23) or deletes after TTL.

**Per-row provenance via SQL**:

```sql
SELECT id, value, provenance(rowid) FROM claims;
-- → (id, value, {svid, hlc, merkle_path})
```

Provenance certificates (§31.9) accessible as a SQL function.

---

## 12. Layer 9 — Observability

Three native capabilities fall out of the design.

### 12.1 Time-travel queries

```go
// Reconstruct state of a subject at any historical HLC frontier
state, err := substrate.StateAt(SubjectURI("sylk://session/<id>/claims/v3"), hlc)
```

Implementation:
1. Find nearest snapshot at or before `hlc`.
2. Replay deliveries from snapshot to `hlc`.
3. Return materialized state.

Use cases:
- "Show me the claims board the user saw 10 minutes ago"
- "Replay the architect's reasoning at the time of this rejection"
- Time-scrubbing in the TUI for session debugging

### 12.2 Causal cone queries

```go
// What caused this entry?
ancestors, err := substrate.CausalCone(EntryRef{Subject, HLC}, MaxDepth)
// What did this entry cause?
descendants, err := substrate.CausalDescendants(EntryRef{Subject, HLC}, MaxDepth)
```

Implementation: graph walk over `parent` index. Stops when depth exceeded
or no more parents.

Use cases:
- "Why was this claim rejected?" → cone walks back to originating claim
  and the rejection's reasoning artifacts
- "What did this rejected claim trigger?" → descendants show remediation
  claims, retries, etc.

### 12.3 Provable audit

A node can prove to an auditor that an entry was durably committed at HLC
`H` by serving the Merkle path from the entry to a snapshot signed by the
Raft group's current term leader.

```
┌─────────────────── Audit proof ───────────────────────┐
│                                                         │
│  Entry E (HLC=H)                                       │
│      │                                                  │
│      └── BodyHash → segment leaf                       │
│                       │                                 │
│                       └── Merkle path → segment root   │
│                                            │            │
│                                            └── snapshot │
│                                                root     │
│                                                  │      │
│                                                  └─sig  │
│                                                    by   │
│                                                  leader │
│                                                                 │
│  Auditor verifies:                                              │
│   1. BodyHash matches entry body                               │
│   2. Merkle path valid up to segment root                      │
│   3. Segment root in snapshot manifest                          │
│   4. Snapshot signed by leader of term T                       │
│   5. Term T was a valid term in cluster history                │
└─────────────────────────────────────────────────────────────────┘
```

The auditor needs only the cluster's CA root and the term-history list to
verify the chain.

### 12.4 Metrics surface

Standard Prometheus-style counters per subject:

| Metric | Purpose |
|--------|---------|
| `substrate_publish_total{subject, class, result}` | Publish counts |
| `substrate_publish_latency_seconds{subject, class}` | Publish latency |
| `substrate_delivery_inflight{subject, consumer}` | Inflight set size |
| `substrate_delivery_lag_seconds{subject, consumer}` | Consumer lag |
| `substrate_dedupe_hits_total{subject, layer}` | Dedupe drops by layer |
| `substrate_circuit_breaker_state{subject, consumer}` | Open/closed/half |
| `substrate_raft_term{namespace}` | Current term |
| `substrate_raft_log_index{namespace}` | Log index |
| `substrate_swim_alive_peers` | Peer count |
| `substrate_swim_suspect_peers` | Suspect peer count |
| `substrate_segment_size_bytes{subject}` | Active segment size |
| `substrate_replication_lag_seconds{namespace}` | Follower lag |

---

## 13. Comparison vs NATS / JetStream

| NATS | Sylk substrate | Why it matters |
|---|---|---|
| Subjects are strings, validated nowhere | Subjects are typed schemas registered in Raft, validated at publish | Eliminates schema-drift consumer breakage; impossible to accidentally publish v2 payload to v1 subject |
| Stream sequence is linear | Stream is causal Merkle DAG | Replay survives compaction; happens-before is preserved across forks/merges; debugging is graph walk |
| Cursors are sequence numbers | Cursors are HLC frontiers + Merkle proofs | Survives log rebases, snapshot installs, namespace migrations |
| Dedupe is opt-in via `Nats-Msg-Id` header | Three-layer dedupe is mandatory in the wire format | Idempotency is a property of the substrate, not a thing callers might forget |
| Connection-level slow-consumer protection | Class-aware credit advertisement per (subject, class) | A Bulk flood doesn't starve Critical traffic |
| Authentication via account JWT, fixed scopes | SPIFFE identities + per-subject authority enforcement at publish time | Sylk's existing authority profile enforcement is unified with the wire |
| Push consumer can lose inflight on consumer crash | Inflight set lives in consumer's Raft group | Crash-safe consumers without external KV |
| KV/ObjectStore are layered on streams via convention | Substrate-native primitives sharing the same Merkle DAG | Time-travel queries against KV are free |
| Cluster gossip is JetStream-internal, no external introspection | SWIM with hierarchical liveness levels exposed as a subject | Membership becomes time-travelable like everything else |
| Single-tier durability (R=1, R=3, R=5) | Per-subject durability profile (Critical sync vs Bulk async vs Best-effort) | Latency for hot paths, throughput for cold paths |
| Subject schema is "whatever you publish" | Subject schema is a CBOR schema registered at registration | Validates at publish; runtime errors at the broker, not the consumer |
| Replication is master-replica | Replication is multi-Raft per namespace | Fault isolation across namespaces; throughput scales with namespace count |
| No native causal cone | Causal cone is a graph walk over parent index | "Why did this happen?" is a primitive, not a manual log archaeology |
| No native time-travel state queries | Time-travel via snapshot+replay | Debugging at any HLC frontier |
| Headers are ad-hoc strings | Header is a fixed-format struct + CBOR body | Zero-copy header parse; deterministic CPU cost |

---

## 14. Comparison vs Hyperscale's Distributed Protocol

Hyperscale's distributed primitives (SWIM, multi-Raft, ledger, reliability)
are world-class. The substrate borrows them. Deltas are:

| Hyperscale | Sylk substrate | Why it matters |
|---|---|---|
| Per-node WAL is the durability primitive | Causal Merkle DAG per subject is the primitive | Replay is graph-walk from frontier; no per-node coordination needed for projections |
| Raft log is sequence-of-entries | Raft log entries are content-addressed; bodies dedup'd | Re-propose-after-leader-change doesn't double-store payloads |
| Dedupe via `idempotency/manager_ledger` (in-process) | Three-layer dedupe at wire level, mandatory | Caller cannot publish without dedupe metadata; cross-DC CRDT replication of dedupe table |
| Vivaldi coordinates flat | Sectioned coordinates per (rack, DC, cross-DC) | Topology-aware peer selection respects realistic network shape |
| Suspicion single-channel (SWIM probe) | Suspicion dual-channel (SWIM + QUIC keepalive) | False-positive rate roughly squared down |
| Cluster topology in one global Raft | Multi-Raft with operator group + per-namespace groups + topology group | Namespace operations don't compete with global operations |
| Snapshot install via blob transfer | Streaming Merkle reconciliation | Stale-replica catchup proportional to actual divergence |
| WAL has fsync per entry or grouped per writer | Group commit per subject with per-class fsync policy | Critical-class messages get synchronous fsync; bulk gets group-commit |
| Activity / projection is in-process | Substrate IS the projection input; fabric reads subjects | One canonical store; lenses are queries, not consumers |
| HLCs not centrally enforced | Every wire frame carries HLC; happens-before is enforced at receive | Time-travel and causal cone queries are free |
| Failure detection is one level (node) | Hierarchical failure detection (node, pod, agent, session) | Right action for the right failure scope |
| Membership rejoin is full gossip catchup | Merkle reconciliation exchanges only divergent leaves | Rejoin cost proportional to changes, not absence duration |
| Cross-namespace ops via direct calls | MultiNamespaceTx with deterministic 2PC + escrow fallback | Composable across arbitrary namespace counts |
| Reliability is per-message-class in-process | Class-based credit advertisement at the wire | Wire-level fairness across publishers |

Hyperscale's strengths preserved as-is and ported (SWIM machinery, raft
algorithm, robust queue, retry budget, circuit breaker). Substrate adds
typed subjects, causal DAG, content-addressed cursors, three-layer dedupe,
multi-Raft namespacing, and the fabric-projection unification.

---

## 15. Sylk-Native Advantages

These come from what Sylk's domain is. They are not generalizable to NATS
or hyperscale because they assume Sylk's coordination model.

1. **Pipeline COW VFS commits ARE substrate entries.** The commit_queue,
   copy_retention, and merge_descriptor logs collapse into one subject with
   the existing ControlWAL semantics inherited. Pipeline-as-data-plane is
   the same primitive as activity-as-data-plane.

2. **Claims, testaments, artifacts ARE substrate entries.** No separate
   claims store. The "sovereign-store + fabric-projection" pattern in
   `docs/CLAIMS.md` becomes literal: every sovereign system publishes to
   its subject; the fabric is the projection over those subjects. The
   per-namespace Raft state machines ARE the sovereign stores.

3. **Forest event ledger ARE substrate entries.** The forest's append-only
   ledger collapses to a subject; the projector becomes a stream consumer;
   canopy/branch/replay state is the Raft state machine for that namespace.

4. **Authority enforcement is wire-level.** `core/authority/` policies
   become substrate-side publish predicates. An agent without `claim.issue`
   for `(subject=claims/v3, session=X)` simply cannot publish — the
   substrate refuses the frame at the QUIC stream entry.

5. **Tree-sitter content addressing for code.** Code chunks chunked by
   `core/chunking/` and fingerprinted by `core/treesitter/` get
   content-addressed substrate entries. Knowledge graph extractors become
   substrate consumers; embeddings reference the same content addresses;
   cache invalidation across the knowledge graph, the editor highlighter,
   and the agent skill surface is automatic — they all share the Merkle
   root.

6. **Concurrency-scope-aware delivery.** `core/concurrency/` already gates
   goroutines by scope; the substrate's consumer machinery uses the same
   scope tree, so consumer goroutines are accounted, deadlined, and
   cleanly shut down with the rest of the session.

7. **Pipeline inspector certificates as artifacts.** Already content-addressed
   in sylk; on the substrate they become first-class entries with
   substrate-level dedupe, replay, and provenance.

---

## 16. Embedded Mode (single user, single laptop)

This is the default. `sylk` is one binary; everything is in one process.

### 16.1 Layer collapses

| Layer | Behavior in embedded mode |
|-------|----------------------------|
| 0 | Identity = local user; HLC normal; subject registry local file |
| 1 | "Wire" = Go channel between in-process publisher and consumer; header struct passed by pointer; no serialization |
| 2 | SWIM no-op; `IsAlive(self) → true` hardcoded; no probes |
| 3 | Each Raft group has 1 replica; full Raft state machine runs but every "vote" is self-vote, every "fsync" is the only fsync |
| 4 | Same code; segments at `~/.sylk/data/<namespace>/` |
| 5 | Same code; same content-addressed cursors; TUI restarts cheap |
| 6 | Same code; three-layer dedupe runs |
| 7 | Same code; class-based backpressure even single-node (Bulk reindex shouldn't starve Critical claims) |
| 8 | Same code; all higher primitives present |
| 9 | Same code; time-travel and causal cone work locally |

### 16.2 Resource cost

- **Memory**: Every namespace has its own state machine + indexes. For a
  typical session, that's ~5-15 namespaces (session control, claims,
  forest, fabric, VFS, authority, subject registry, KV, object store,
  agent log, view projections). Each is a few MB. Total substrate
  overhead: ~50-100 MB at rest, mostly mmap'd indexes that the OS pages
  out under memory pressure.
- **Disk**: Default retention gives ~100MB-1GB per active session. Opt-down
  ("session only, no archive") brings under 50 MB per session.
- **CPU**: HLC bookkeeping is free. BLAKE3 fingerprints over message
  bodies are a few hundred ns each. Group-commit fsync (default 2ms
  window) batches dozens of writes per syscall. A laptop sustains 100K
  msg/sec on a single core; real workload is closer to 100-1000 msg/sec.
- **Boot time**: mmap segment open + Raft replay catches up the state
  machine. <500ms cold boot for a typical session, <100ms warm boot
  (snapshot reasonably current).

### 16.3 What you gain that you don't have today

- **Crash safety for everything.** Sylk currently has six different WAL
  discipline implementations (claims, forest, activity, commit_queue,
  copy_retention, agent log). Embedded mode means one — battle-tested for
  the cluster — used everywhere.
- **Time-travel debugging on a laptop.** "Show me state at HLC `H`" works
  locally; you can scrub a session like a video.
- **Same code as production.** No "embedded mode bug" class — when the
  user files a bug, you reproduce against the same code paths the cluster
  runs.
- **Sane offline-first behavior** when the user later upgrades to remote
  mode — the local cache layer is the embedded mode.

### 16.4 What you don't have

- Cross-machine collaboration. Single laptop.
- Cross-DC durability for long-lived sessions. A laptop SSD failure means
  session loss. Optional async backup-to-cloud-bucket via a
  sealed-segment uploader (a substrate consumer that pushes sealed
  segments to S3-compatible storage) is supported; off by default.

### 16.5 Boot sequence

```
┌───────── sylk embedded mode boot ──────────┐
│                                              │
│  t=0     User runs `sylk`                    │
│                                              │
│  t<10ms  Process start                       │
│          ├─ Open subject registry           │
│          ├─ Start substrate Raft groups     │
│          │  (single-replica, replay WAL)    │
│          └─ Restore in-memory state         │
│                                              │
│  t<200ms Substrate ready                     │
│          ├─ Start agent runtimes            │
│          ├─ Start knowledge stack           │
│          └─ Start TUI                        │
│                                              │
│  t<500ms TUI rendered with current state    │
│          (cold boot)                         │
│                                              │
│  t<100ms Same flow but with snapshot         │
│          reasonably current (warm boot)     │
└──────────────────────────────────────────────┘
```

---

## 17. Local Daemon Mode

`sylkd` is a long-running background process; `sylk` is the TUI that connects
via Unix socket.

### 17.1 Why this exists

- TUI restart doesn't kill agents or lose in-flight work. Crash the TUI,
  reconnect, see the same world.
- Multiple TUI windows can connect to the same daemon. Useful for
  split-screen workflows.
- The daemon runs at login; long-running agent work continues even when
  no TUI is open.
- Trivial migration path to remote: change the socket path to a QUIC URL.

### 17.2 Performance

Identical to embedded for human-scale interaction. Unix socket latency on
Linux/macOS is ~10-30 µs, indistinguishable from in-process.

### 17.3 Multi-TUI scenarios

```
┌─────────────────── Multi-TUI scenario ────────────────────┐
│                                                            │
│   sylk (TUI window 1: claims board view)                  │
│        │                                                   │
│        │ AF_UNIX                                           │
│        ▼                                                   │
│   ┌──────────────────────────────────────────┐            │
│   │             sylkd (daemon)                │            │
│   │                                            │            │
│   │   ┌─────────────────────────────────────┐│            │
│   │   │  Substrate                           ││            │
│   │   │  Subjects:                           ││            │
│   │   │    sylk://session/abc/claims/v3      ││            │
│   │   │    sylk://session/abc/forest-evts/v1 ││            │
│   │   │    ...                                ││            │
│   │   └─────────────────────────────────────┘│            │
│   │                                            │            │
│   │   ┌─────────────────────────────────────┐│            │
│   │   │  Agents (long-running)              ││            │
│   │   └─────────────────────────────────────┘│            │
│   └──────────────────────────────────────────┘            │
│        ▲                                                   │
│        │ AF_UNIX                                           │
│        │                                                   │
│   sylk (TUI window 2: forest/memory view)                 │
│                                                            │
│   Each TUI subscribes to the substrate subjects it needs. │
│   No coordination between TUIs; each is an independent    │
│   consumer with its own cursor.                           │
└────────────────────────────────────────────────────────────┘
```

---

## 18. Remote Multi-DC Mode

The user runs `sylk` locally; the cluster of `sylkd` instances runs across
DCs. The TUI is a thin client; the substrate is fully realized on the cluster.

### 18.1 Connection lifecycle

```
┌─────────────── TUI connection lifecycle ──────────────────────┐
│                                                                │
│ 1. Discovery                                                   │
│    sylk does DNS-SD or seed-node lookup against                │
│    sylk.example.com. Cluster's discovery service               │
│    (operator group) returns gateway endpoints with             │
│    Vivaldi sectioned coordinates.                              │
│                                                                │
│ 2. Gateway selection                                           │
│    sylk picks the closest gateway by sectioned coordinate.     │
│    Hyperscale's discovery/selection/ machinery, ported.        │
│                                                                │
│ 3. Authentication                                              │
│    sylk presents an SVID (issued via OIDC flow on first        │
│    login, cached locally). Gateway validates against           │
│    cluster CA; checks SVID's authority bindings against        │
│    the user's tenant.                                          │
│                                                                │
│ 4. Session admission                                           │
│    Gateway routes session to its namespace Raft group          │
│    (rendezvous-hashed on session ID). Session state lives      │
│    in 3 replicas, ideally one per DC. Gateway becomes the      │
│    user's read-replica.                                        │
│                                                                │
│ 5. Cursor sync                                                 │
│    sylk presents its HLC frontier from local cache. Gateway    │
│    streams delta from cache's frontier to current HLC.         │
│      Cold start: tens to hundreds of KB                        │
│      Warm restart: a few KB                                    │
│                                                                │
│ 6. Subscription                                                │
│    sylk subscribes to viewport-scoped projection subjects:     │
│      sylk://session/<id>/view/claims-board-summary             │
│      sylk://session/<id>/view/fabric-envelope                  │
│      sylk://session/<id>/view/forest-packets-recent            │
│      sylk://session/<id>/view/pipeline-status                  │
│      sylk://session/<id>/view/editor-diagnostics               │
│                                                                │
│    Server-side aggregators publish to these from raw subjects. │
│    Bandwidth bounded by viewport, not session activity.        │
└────────────────────────────────────────────────────────────────┘
```

### 18.2 Latency

| Operation | Path | Typical latency |
|-----------|------|-----------------|
| TUI render existing state | Local cache → TUI | <1 ms |
| User submits a prompt | TUI → gateway → leader → quorum fsync → ack | 5-50 ms (intra-DC) |
| Agent writes to claims board | Agent (in-cluster) → namespace leader → fsync → fabric projection | 2-20 ms (intra-DC) |
| TUI receives ambient envelope | Server-side rendered → gateway push to TUI | 1-10 ms after publish |
| Cross-DC write | Coordinator DC → quorum across DCs | 50-200 ms |
| Reconnect after gateway death | Detect → re-discover → new gateway → cursor sync | 200ms-1s |
| Local cache read during disconnect | Local cache → TUI | <1 ms |

The critical observation: **agents run in the cluster, near the data**
(knowledge graph, forest, document DB). The TUI sees rendered events, not
raw computations. The TUI's bandwidth scales with what the user can read,
not with what the cluster does.

### 18.3 Bandwidth shaping (rendered projections)

```
┌────────────── Server-side viewport rendering ─────────────┐
│                                                            │
│ Raw subjects                                               │
│   sylk://session/<id>/claims/v3   (high traffic)          │
│   sylk://session/<id>/forest-events/v1                    │
│   sylk://session/<id>/agent-log/v1                        │
│   ...                                                      │
│         │                                                  │
│         ▼                                                  │
│ Server-side aggregator (substrate consumer)                │
│   reads raw subjects                                       │
│   computes rendered viewport state                         │
│   publishes deltas to view subject                         │
│         │                                                  │
│         ▼                                                  │
│ View subjects (low-traffic, delta-encoded)                 │
│   sylk://session/<id>/view/claims-board-summary            │
│   sylk://session/<id>/view/fabric-envelope                 │
│   ...                                                      │
│         │                                                  │
│         ▼                                                  │
│ TUI subscribes only to active viewport's view subjects     │
│   bandwidth: KB/s typical, MB/s during heavy activity      │
└────────────────────────────────────────────────────────────┘
```

When the user opens the claims-board panel, the TUI subscribes to
`view/claims-board-summary`; closing the panel unsubscribes. Bandwidth
is bounded by viewport.

### 18.4 Failover scenarios

```
┌─────────────── Gateway failover ──────────────────────┐
│                                                        │
│ 1. sylk's QUIC keepalive to gateway times out (~1s)   │
│ 2. sylk reads local cache to keep rendering           │
│ 3. sylk runs discovery again                          │
│ 4. sylk picks next-closest healthy gateway            │
│ 5. sylk presents cursor to new gateway                │
│ 6. New gateway streams delta from cursor              │
│ 7. sylk catches up; UI refreshes                      │
│                                                        │
│ User-visible: brief "reconnecting" indicator (<2s)    │
└────────────────────────────────────────────────────────┘

┌─────────────── Namespace leader failover ─────────────┐
│                                                        │
│ 1. Namespace leader dies                              │
│ 2. Followers detect via heartbeat timeout (~500ms)    │
│ 3. New election (pre-vote then vote, ~250-500ms)      │
│ 4. New leader committed; gateway re-routes writes     │
│ 5. Reads continue via read-index (no stall for reads) │
│                                                        │
│ User-visible: brief stall on writes only              │
└────────────────────────────────────────────────────────┘

┌─────────────── DC partition ──────────────────────────┐
│                                                        │
│ Cluster: 3 DCs × 3 nodes each, namespace replicated   │
│ across DCs.                                            │
│                                                        │
│ DC-A partitioned from DC-B, DC-C.                     │
│                                                        │
│ Case 1: User in DC-A                                  │
│   sylk's gateway in DC-A is reachable but cluster     │
│   has lost quorum (DC-A is minority)                  │
│   Writes fail; reads still work from DC-A replicas    │
│   sylk's outbox queues local writes                   │
│   On heal: outbox replays with three-layer dedupe     │
│                                                        │
│ Case 2: User in DC-B (with DC-C)                      │
│   Cluster operates with DC-B + DC-C quorum (4/6 nodes)│
│   sylk continues normally                             │
│   On heal: DC-A replicas catch up via Merkle reconcile│
│                                                        │
│ Three-DC deployment survives any single-DC partition. │
└────────────────────────────────────────────────────────┘

┌─────────────── TUI process dies ──────────────────────┐
│                                                        │
│ 1. TUI process killed                                 │
│ 2. User restarts sylk                                 │
│ 3. sylk reads local cache, renders cached state       │
│ 4. sylk reconnects to gateway with cursor             │
│ 5. Gateway streams delta since cursor                 │
│ 6. TUI catches up                                     │
│                                                        │
│ Session state unaffected; agents continue in cluster. │
└────────────────────────────────────────────────────────┘

┌─────────────── Bad network ───────────────────────────┐
│                                                        │
│ 1. sylk's connection is flaky (intermittent loss)     │
│ 2. sylk falls back to local cache for reads           │
│ 3. User writes go to local outbox subject             │
│ 4. On reconnect, outbox replays to cluster            │
│ 5. Three-layer dedupe ensures exactly-once            │
│                                                        │
│ User can keep working through flaky network.          │
└────────────────────────────────────────────────────────┘
```

### 18.5 Authority and isolation

- Every published frame from the TUI is signed by the user's SVID. The
  gateway forwards the frame; the namespace's authority predicate verifies
  the SVID has the capability for that subject. Compromised gateway
  *cannot* forge user actions because it doesn't have the user's signing
  key.
- Multi-tenant: namespace IDs are tenant-scoped; the rendezvous hash
  includes the tenant. Cross-tenant subjects don't exist; cross-tenant
  routing is impossible.
- Audit: every committed entry is signed and Merkle-rooted. The user can
  request a provable audit log of their session at any time; the gateway
  returns Merkle paths the user verifies against the cluster's published
  roots. End-to-end auditable.

### 18.6 Local cache (the embedded substrate, bounded)

The remote-mode TUI keeps a local cache that is *the same storage layer as
embedded mode*, just with much tighter retention.

```
~/.sylk/cache/<cluster>/<session>/
  ├── segments/                 ← bounded sealed segments
  ├── indexes/
  ├── snapshot-current.bin
  ├── cursor.json               ← persisted across restarts
  └── outbox/                   ← local writes pending replay
      └── outbox.log
```

- Retention default: 24h or 100MB, whichever first.
- Cursor stored here; reconnect is delta-only.
- During disconnection: TUI renders from cache. Fabric envelope, claims
  board, recent forest packets — readable.
- Local edits during disconnection go to outbox; on reconnect, replayed
  with caller-supplied idempotency keys ensuring exactly-once.

This is the same code as embedded mode's storage layer. *No fork.*

### 18.7 Cold-start UX

```
┌──────── Remote-mode cold start ────────────┐
│                                              │
│ t=0     User runs `sylk --connect ...`      │
│                                              │
│ t<100ms TUI renders local cache             │
│         (offline-first, prior-session view) │
│                                              │
│ t<500ms QUIC connection established         │
│                                              │
│ t<1s    Gateway authenticated; cursor sent  │
│                                              │
│ t<2s    Delta arrives; TUI repaints         │
│                                              │
│ User never sees a blank loading screen.     │
│ They see yesterday's view becoming today's. │
└──────────────────────────────────────────────┘
```

---

## 19. Trust Models and Adversarial Robustness

The substrate as specified in §1-§18 is crash-fault-tolerant (Raft), backed
by mTLS-mutual-authenticated SVIDs and dual-channel suspicion. Crash faults
are the right model for laptop, single-team, and intra-org clusters. Cross-
tenant SaaS, federated meshes, and adversarial deployments need three more
layers of defense — designed into the substrate, not bolted onto it.

### 19.1 Cryptographic accountability layer

Every committed Raft entry carries a leader-term-bound Ed25519 signature
over `(term, index, parent_index, body_hash)`. Every snapshot carries a
leader signature over its Merkle root. Every gossip frame is signed by
originator and chained by HLC.

A divergent or lying leader produces a *cryptographic proof of misbehavior*
that any honest replica can broadcast. The cluster runs with crash-fault
quorum cost but Byzantine-grade post-hoc forensics. An attacker who
compromises a leader cannot rewrite history — the chain breaks visibly.
This is "accountable Raft": no quorum size increase, all the audit value
of BFT, structurally on-by-default.

The accountability layer is also the precondition for federation (§20)
because peer clusters can verify the chain without trusting the local
cluster's gossip path.

### 19.2 Optional BFT subjects

Subject registration accepts `consensus = raft | hotstuff | tendermint`.
BFT consensus runs 3f+1 replicas with threshold signatures; same wire
format, same cursors, same Merkle DAG, different consensus binding.

Use cases:

- `sylk://global/authority/v1` — cluster-wide capability grants, must be
  Byzantine-safe across federated peers.
- `sylk://federation/<id>/topology/v1` — federation control plane.
- `sylk://tenant/<t>/audit/v1` — tenant-facing audit subjects where the
  substrate operator is itself untrusted.

Non-goal: BFT for everything. Per-session claims and forest events run
Raft because they're scoped to a single trust boundary. BFT is per-subject
opt-in for subjects spanning trust boundaries.

### 19.3 Provable non-equivocation for SWIM gossip

A Byzantine peer can split its view by gossiping different states to
different recipients. Hyperscale's incarnation refutation handles stale
rumors but not equivocation.

Extension: every gossip update is signed by originator and tagged
`(node, hlc, incarnation)`. Receivers index updates by `(node, hlc)`.
Two updates with the same `(node, hlc)` and different content is a
*cryptographic proof of equivocation*; the offending node is immediately
marked FAULTY across the cluster regardless of SWIM state. The proof
itself becomes a substrate entry on `sylk://global/security/equivocation/v1`.

### 19.4 Trusted execution attestation

For "process running on someone else's metal" — regulated industry,
cross-org federation, BYOC — substrate state machines run inside attested
enclaves: Intel SGX, AMD SEV-SNP, AWS Nitro, Azure Confidential Computing.

- SVIDs include attestation evidence as an X.509 extension.
- Cluster join protocol includes remote attestation: joining node proves
  "I am running this code, this config, in this enclave."
- SVID issuance gated on attestation verification.
- Per-replica attestation refresh on SVID rotation.

The substrate doesn't *require* enclaves; it *supports* them as a
deployment-time policy choice. Bare-metal, K8s, and enclave-backed
deployments use the same code paths.

---

## 20. Federation, Edge, and Witness Topology

Multi-Raft across 3 DCs is mid-scale. Million-user / global / cross-org
demands three more topology axes simultaneously.

### 20.1 Federation as a first-class primitive

A *federation* is a set of clusters with shared trust roots (federated
SPIFFE trust domain) but independent control planes and data planes.

```
┌─── Federation ──────────────────────────────────────────┐
│                                                          │
│   ┌─ Cluster A ─┐    ┌─ Cluster B ─┐    ┌─ Cluster C ─┐│
│   │ ops-grp     │    │ ops-grp     │    │ ops-grp     ││
│   │ topo-grp    │    │ topo-grp    │    │ topo-grp    ││
│   │ ns groups…  │    │ ns groups…  │    │ ns groups…  ││
│   └──────┬──────┘    └──────┬──────┘    └──────┬──────┘│
│          │                   │                   │      │
│          └───────────────────┴───────────────────┘      │
│                              │                          │
│             ┌────────────────┴───────────────┐          │
│             │ Federation control subject     │          │
│             │   sylk://federation/<id>/...   │          │
│             │   (BFT-replicated across       │          │
│             │   cluster representatives)     │          │
│             └────────────────────────────────┘          │
└──────────────────────────────────────────────────────────┘
```

- **Federated subject namespace**: subject IDs prefixed by cluster origin;
  lookup falls back across federation members via the federation control
  subject.
- **Cross-cluster subscription**: a consumer in cluster A subscribes to
  a subject in cluster B. Federation gateway in B verifies authority via
  the shared trust root, streams entries with HLC + cryptographic
  accountability proofs (§19.1) so A can verify integrity without trusting
  B's gossip.
- **Federation policy**: per-pair allowlist of subjects, rate limits, and
  authority predicates. Compromised federation peer cannot exfiltrate
  beyond its allowlist.
- **Federation control plane is BFT** (§19.2). Federation membership
  changes (add peer, remove peer, rotate trust root) are BFT-committed.

The federation isn't another cluster — it's a *coordination subject* with
cross-cluster delivery. Each cluster owns its data; federation is read-
replication + cross-publish + capability sharing.

### 20.2 Edge / PoP tier

Between TUI client and home cluster, add a third deployment shape:
stateless or weakly-stateful Points of Presence at ISP / region / ASN
granularity.

```
┌─ Edge Topology ─────────────────────────────────────────┐
│                                                          │
│   sylk (TUI, anywhere) ─── anycast/geo-DNS ───┐         │
│                                                ▼         │
│   ┌────────────── Edge / PoP tier ─────────────────┐   │
│   │                                                  │   │
│   │  PoP-narita     PoP-cdg      PoP-iad     PoP-syd│   │
│   │  ┌─────────┐   ┌─────────┐  ┌─────────┐ ┌──────┐│   │
│   │  │view cache│  │view cache│  │view cache│ │view ││   │
│   │  │outbox    │  │outbox    │  │outbox    │ │cache ││   │
│   │  │SVID term │  │SVID term │  │SVID term │ │outbox││   │
│   │  └────┬────┘   └────┬────┘  └────┬────┘  └──┬───┘│   │
│   │       │             │            │          │    │   │
│   └───────┼─────────────┼────────────┼──────────┼────┘   │
│           │             │            │          │        │
│           └─────────────┴────────────┴──────────┘        │
│                          │ QUIC + accountability proofs  │
│                          ▼                               │
│   ┌──────── Home cluster (multi-DC) ───────────────┐    │
│   └──────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────┘
```

Edge responsibilities (no consensus participation):

- **View cache**: read-through cache of view subjects; HLC-frontier
  validated against home cluster periodically + on miss.
- **Outbox aggregation**: batch and forward writes; same three-layer
  dedupe; survives PoP restart via local segment storage.
- **mTLS termination, user SVID passthrough**: edge holds a per-PoP SVID
  with `gateway` capability; user SVID is *carried through, not replaced*
  — the user's signature on each frame validates end-to-end at the home
  cluster. Compromised edge cannot forge user actions.
- **Anycast / geo-DNS** for selection at the network layer.

Failure modes:

- PoP failure → client falls over to next PoP (DNS or anycast reconvergence).
- Home cluster partitioned from PoP → PoP serves cached reads, queues
  writes, surfaces "stale-by-X-seconds" to TUI.
- PoP compromise → cannot forge user actions; can only suppress/delay;
  detected by home cluster via missing-heartbeat + gossip.

### 20.3 Witness and learner replica classes

Voting Raft replicas are expensive (quorum participation, full log,
snapshots). Two non-voter classes round out the picture.

| Class    | Vote? | Log replication?      | Snapshot install? | Use case |
|----------|-------|-----------------------|--------------------|----------|
| Voter    | yes   | full                  | yes                | Default; quorum participant |
| Witness  | yes (vote-only) | only commit indices | no | 2-DC stretched cluster needs odd-quorum witness in third location; minimal storage / bandwidth |
| Learner  | no    | full                  | yes                | Read replicas, audit replicas, archival sinks, ML training feeds; join-via-learner before voter promotion |

Learner replicas also solve the "joining a hot group destabilizes it"
problem: new replicas join as learner, catch up at their leisure, only
promote to voter when caught up to within configurable lag.

### 20.4 Coalesced heartbeat protocol

A million sessions = a million per-session Raft groups = O(replicas ×
groups × heartbeat-rate) of pure overhead even when idle. At 50ms
heartbeat × 1M groups × 3 replicas = 60M heartbeats / sec of pointless
traffic.

Coalesced heartbeat protocol:

- One physical heartbeat frame per node-pair on the control channel,
  regardless of group count.
- Frame body carries a delta-encoded vector of `(group_id → leader_state,
  term, commit_index)` for every group sharing that connection.
- Followers ack with their own commit indices in the same coalesced
  fashion.
- Group-level heartbeat timeouts derive from the coalesced frame; if the
  coalesced frame stops, *every* group on that pair detects leader
  failure simultaneously.

Result: heartbeat load is O(node-pairs × heartbeat-rate), independent of
group count.

### 20.5 Hierarchical Raft

For very large clusters, the topology group becomes a hot spot (every
namespace creation hits it).

```
Global root group
  │
  ├── Region meta-group (us-west) ── owns: namespaces in us-west DCs
  ├── Region meta-group (us-east) ── owns: namespaces in us-east DCs
  ├── Region meta-group (eu-west) ── owns: namespaces in eu-west DCs
  └── Region meta-group (ap-east) ── owns: namespaces in ap-east DCs
```

- Namespace placement decisions happen at the regional meta-group; only
  cross-region migrations escalate to root.
- Region-local subject registry caches global registry; pulls updates
  from root.
- Tenant assignment to regions is itself a subject;
  `sylk://global/tenant-region/v1`.
- Failure of a region meta-group degrades only that region's placement
  decisions; existing namespaces continue serving.

The substrate handles "one-region cluster" through "20-region global
mesh" with the same code; hierarchy is opt-in based on observed scale.

### 20.6 Shared-log Raft for many small groups

Per-group physical log = per-group fsync = wasted IOPS at high group
density. Optional optimization: many small groups share one physical log
on disk, indexed by `(group_id, raft_index)`. Adopted from CockroachDB's
experience with tens of thousands of ranges per node.

Trade-off: a physical-log corruption affects multiple groups. Mitigated
by per-group Merkle roots — corruption in group X's entries is detected
without affecting group Y.

### 20.7 Backpressure propagation across federation boundaries

§10 defines per-class credit advertisement within a cluster. §20.1
establishes federation. The two compose: federation gateways propagate
backpressure between clusters via hierarchical credit federation +
hop-by-hop ECN.

**Hierarchical credit federation**: each federation gateway advertises
*aggregate per-(class, subject)* credit to peer gateways. Advertisement
piggybacks on the existing coalesced piggyback frame (§4.6) extended
with a `federation_credit` block — no new round-trip protocol.

**Cascade protocol**:

```
Cluster A overload → A's gateway reduces credit advert to peer B
                  → B's gateway propagates reduced credit through
                    B's local credit advertisement (§10.1)
                  → B's publishers slow per existing local mechanism
```

Backpressure propagation is hop-by-hop; cluster-internal mechanisms
(§10) handle local enforcement. No global coordinator required.

**ECN-style explicit congestion notification**: when a federation
gateway's ingress queue exceeds a threshold (default 50% capacity), it
sets an ECN bit in subsequent forwarded frames. Sender's gateway sees
ECN, reduces send rate proportional to fraction-of-marked-frames
(DCTCP-style: `α := (1-g)·α + g·F` where `F` = fraction marked, `g` =
0.0625). Standard convergence properties from the DCTCP literature.

**Per-pair federation quota** (§22.1 extension): cross-cluster
bandwidth subject to per-`(source, destination, class)` quota.
Saturation triggers explicit `ErrFederationQuotaExceeded` at sender's
gateway — backpressure surfaces to sender's publishers immediately,
not just via implicit slowdown.

**Class-specific behavior at federation boundary**:

- **Critical**: never dropped at federation boundary; backpressure
  cascades to publisher; gateway buffer reserved.
- **Standard**: queued at gateway with bounded buffer; backpressure
  cascades when buffer > 75%.
- **Bulk**: dropped at gateway when buffer > 90%; metric emitted; sender's retry
  budget (§10.3) handles.
- **Background**: never crosses federation boundary if best-effort
  flag set. If it does, drop-on-pressure with no retry.

**Federation gateway as Raft / BFT group**: gateway is replicated
(3+ replicas via §17.2 BFT, or §6 Raft for non-Byzantine); credit
state is part of the replicated state machine. Single-gateway crash
doesn't lose credit ledger or produce inconsistent backpressure.

**Operational guarantees**:
- Backpressure delay propagation bounded by
  `2 × max_intra_cluster_propagation_delay + federation_RTT`.
  Typical: 50ms intra-cluster + 100ms federation RTT → backpressure
  visible to publishers within ~250ms.
- Federation overload is graceful, not catastrophic: drops shift from
  Background → Bulk → (rarely) Standard; Critical preserved.

**Telemetry**:
- `federation_credit{class, peer}` — outbound credit advertised.
- `federation_ecn_marked_total{peer}` — ECN-marked frame count.
- `federation_dropped_total{peer, class, reason}` — per-class drops at
  federation boundary.
- Causal cone (§12.2): federation backpressure events queryable.

**Soundness invariants**:
- Backpressure is monotonic: once a cluster signals reduced credit,
  remote senders cannot ignore it (their gateway enforces).
- No starvation: the credit advertisement protocol is fair across
  peers; no peer can be starved of cross-federation bandwidth by
  another peer's burst.
- Correctness under partition: if federation peers can't communicate,
  each cluster's local backpressure is unchanged; cross-federation
  publishes time out per existing mechanisms (§10.3 retry budget).

### 20.8 Learner-replica freshness guarantees for served reads

§20.3 introduces learner replicas. Reads served from learners need
explicit freshness semantics — when can a learner serve a read, and
under what guarantee.

**Per-learner advertised freshness**: each learner publishes
`(commit_index_lag_bytes, hlc_lag_ms, last_advert_hlc)` to its parent
namespace's leader and to subscribed clients via §4.6 piggyback
frames. Updated every 100ms.

**Read API classification**:

```go
type ReadOpts struct {
    // exactly one of:
    Linearizable     *bool                          // leader-only; strictest
    BoundedStaleness *time.Duration                 // any learner with lag ≤ duration
    Eventual         *bool                          // any learner; no guarantee
    Monotonic        *HLC                           // learner with frontier ≥ given HLC
    ReadYourWrites   *WriteToken                    // observed-after token
}
```

**Per-class default isolation level** (configurable):

- Critical → `Linearizable`
- Standard → `BoundedStaleness{100ms}`
- Bulk → `Eventual`
- Background → `Eventual`

**Linearizable read served from learner** (optional, off by default):
learner does a read-index handshake with leader (~1 RTT), gets
confirmed `commit_index`, serves at that index. Higher latency than
direct leader read but offloads serving cost.

**Monotonic-read enforcement**: client maintains `last_observed_hlc`
cookie; each read includes it in request header. Server (learner)
checks its frontier ≥ cookie; if not, fails over (different learner
or leader). Cookie advances with successful reads.

**ReadYourWrites**: write returns `WriteToken{hlc, uncertainty}`.
Subsequent read with this token only served by replicas whose frontier
≥ `token.hlc + token.uncertainty`. Otherwise routed elsewhere or
returns `ErrFrontierNotReached`.

**Per-subject staleness budget**: each subject declares max acceptable
staleness for default reads via `SubjectPolicy` (§25.1). Clients
without explicit `ReadOpts` get the subject's default.

**Lag-bounded learner exclusion**: operator configures per-cluster
hard lag bound (e.g., `learner_excluded_if_lag > 10s`). Learner
exceeding bound is automatically excluded from read serving until
caught up. Excluded learners don't count toward any subject's read
availability. Re-included after sustained recovery (sliding-window
check; default 5min stable).

**Selection algorithm at gateway**:

```
RouteRead(subject, opts) →
  if opts.Linearizable:
      route to leader (via §7 read-index)
  else if opts.BoundedStaleness:
      pick learner with min(lag) such that lag ≤ opts.duration
      if none qualifies: ErrStale
  else if opts.Monotonic:
      pick learner with frontier ≥ opts.hlc
      if none: route to leader
  else if opts.ReadYourWrites:
      pick learner with frontier ≥ token.hlc + token.uncertainty
      if none and within wait budget: wait for next learner advert
      else: ErrFrontierNotReached
  else if opts.Eventual:
      pick learner with min(load), no freshness check
```

**Telemetry**:
- `learner_lag_seconds{learner, subject}` — lag distribution.
- `learner_excluded_total{learner, reason}` — exclusion events.
- `read_route{class, target}` — leader vs learner distribution.
- `read_freshness_violation_total{client}` — observed staleness >
  declared bound (a bug if non-zero).

**Soundness**:
- Under stable network + Raft progress, BoundedStaleness reads return
  values committed within `duration` of read time. No qualifying
  learner → `ErrStale` (fail-closed; no older data served).
- Linearizable reads from learner observe a commit index ≥ leader's
  commit at handshake time; identical correctness guarantee as direct
  leader read.
- ReadYourWrites is an instance of cross-domain observed-after
  (§31.24) within a single domain.

---

## 21. Extended Storage Architecture

§7 covers active / sealed / snapshot / manifest. Production at scale
demands four more dimensions.

### 21.1 Native multi-tier storage

Per-subject storage policy: `(hot=local-NVMe, warm=local-SSD,
cold=S3-compatible, archive=glacier)`.

| Tier    | Read latency       | Storage cost | Use |
|---------|--------------------|--------------|-----|
| Hot     | µs (mmap, NVMe)    | $$$$         | Active segment + recent sealed (last 24h typical) |
| Warm    | ms (SSD, no mmap)  | $$$          | Sealed segments aged 24h-30d |
| Cold    | 100ms-seconds (S3 GET) | $$       | Sealed segments 30d-7y; replay-on-demand |
| Archive | minutes (Glacier restore) | $     | Compliance retention; full restore needed before access |

Auto-tiering rules per subject:

- Sealed segments demote based on access age + frequency (LFU with time
  decay).
- Promotion on first cold access (full segment fetched into warm;
  subsequent reads are warm-hot).
- Cold reads bandwidth-budgeted (own message class; cannot starve hot
  reads).
- Tier transitions Merkle-verified; the segment's content hash is the
  same across tiers.

Time-travel queries transparently fault in cold/archive segments via a
streaming reader. Archive reads return `ErrArchiveRestoreInProgress`
with an HLC ETA so callers can decide.

### 21.2 Encryption at rest with envelope hierarchy

```
HSM/KMS (cluster-rooted master keys, M-of-N quorum to access)
   │
   ▼
KEKs (per-cluster, rotated quarterly)
   │
   ▼
DEKs (per-tenant, per-subject-class — rotated annually or on demand)
   │
   ▼
AEAD-encrypted segment data + per-segment nonce
```

- Sealed-segment AEAD encryption with the DEK; per-segment unique nonce;
  segment trailer includes encrypted-DEK envelope.
- Key rotation rotates only KEKs (re-wrap envelopes; no segment rewrite);
  DEK rotation is per-tenant policy, lazy on rotation event.
- Field-level encryption for PII fields declared in subject schema
  (additional inner AEAD layer with separate field-encryption keys).
- Forward-secrecy variant: ephemeral session keys for short-lived
  subjects; historical disk theft reveals nothing about already-compacted
  sessions.

### 21.3 Continuous backup as a first-class subject

Backup is a substrate consumer that:

1. Subscribes to all subjects with `backup=continuous` policy.
2. Writes encrypted, content-addressed sealed segments to immutable object
   storage (S3 with object-lock + bucket versioning, GCS with retention
   policy, Azure WORM).
3. Publishes `backup-progress` entries to `sylk://global/backup/v1` so the
   cluster's own audit trail tracks backup completeness.
4. Verifies post-write Merkle root against source.

Restore = "replay this archive into a new namespace" with full Merkle
verification before any entry is applied. Untrusted-backup attacks (§30.3)
blocked by out-of-band signed root list verification.

Cross-cluster DR replication uses the same primitive in stream form: a
learner replica in the DR cluster subscribes to the backup feed.

### 21.4 Point-in-time restore at HLC frontiers

Time-travel queries are O(snapshot lookup + replay-from-snapshot). For
*restore*:

```go
// rebuild this namespace at HLC H into a sibling namespace
opGroup.Restore(SourceNamespace, hlc, NewNamespaceName)
```

- Operator group atomically creates `NewNamespaceName`, replays from
  nearest snapshot ≤ H, stops at H, marks new namespace ready.
- Powers undo, fork-for-debug, per-tenant rollback, incident response
  ("show me what the cluster looked like before the bug deployed").
- Sibling namespace is a full first-class namespace; can serve traffic,
  can be diff'd against live, can be promoted to replace live via
  authority cutover.

### 21.5 Causal DAG horizon compaction

The parent-edge index in §7.3 grows in proportion to total entries ever
published, even past retention. Causal cone queries past a year are
accidentally O(year-of-entries).

Solution: a *causal horizon*, an HLC frontier maintained per subject.
Parent edges crossing the horizon collapse into a single "horizon
parent" pointing at the most recent snapshot covering pre-horizon state.
Causal cone queries past the horizon return `(horizon_parent,
ErrHorizonTruncated)`; auditors can still verify Merkle paths back to
the snapshot, but cone walks terminate at the horizon.

Horizon is per-subject policy (default: snapshot age × 4). A compactor
service prunes the parent index in the background.

### 21.6 Erasure-coded cold tier

Full 3x replication is wasteful at petabyte cold tier. Reed-Solomon
(k=10, m=4) gives 1.4x storage cost with tolerance for any 4 simultaneous
shard failures.

- Hot/warm tiers stay 3x replicated for read latency.
- Cold tier sealed segments erasure-coded across object storage shards or
  across DCs.
- Recovery: any 10 of 14 shards rebuild the segment; verified by Merkle
  root.

---

## 22. Multi-Tenancy and Resource Isolation

§18.5 mentions tenant scoping. Million-user SaaS is its own architecture
problem.

### 22.1 Per-tenant quota subject

`sylk://tenant/<id>/quota/v1` carries:

- Storage GB (hot, warm, cold per tier)
- Messages/sec per class (Critical, Standard, Bulk, Background)
- Namespace count
- Replication bandwidth
- Compaction CPU-seconds / hour
- Federation cross-publish bandwidth

Authority predicate at publish time consults the quota state machine;
over-quota publishes return `ErrQuotaExceeded` cheaply (read-side check,
no replication round-trip).

Quota is itself a substrate subject — observable, time-travelable, audit-
loggable, BFT-replicated for tenant-tenant fairness.

### 22.2 Tenant-isolated Raft groups

Hard rule enforced at namespace creation: no namespace group contains
data from more than one tenant. Per-tenant quota for "namespaces
consumed" caps the blast radius from any one tenant. Eliminates noisy-
neighbor at the consensus layer.

### 22.3 Per-tenant LSM compaction isolation

Each tenant's LSM indexes run in their own compaction queue with per-
tenant CPU/IO accounting. Compaction scheduler is priority-aware:

- Hot compactions (active subject, pending writes) preempt cold.
- Per-tenant compaction CPU-seconds tracked against quota.
- Tenants over compaction quota → compaction stalls (writes still
  accepted; backlog grows; eventually backpressure to publisher).

### 22.4 Per-tenant key material

§21.2's DEK hierarchy with strict tenant boundaries:

- Per-tenant KEK; cross-tenant DEK access cryptographically impossible
  without KEK access.
- KEK access in HSM with per-operator approval workflow for emergency
  restores.
- Tenant offboarding: KEK destroyed → all tenant data cryptographically
  inaccessible regardless of disk recovery.

### 22.5 Cost accounting subject

`sylk://tenant/<id>/usage/v1` mirrors §22.1 with *observed* values:

- Bytes published per class
- Bytes stored per tier
- Replication bytes egressed
- Compaction CPU-seconds consumed
- Federation cross-publish bytes

Powers chargeback, capacity planning, abuse detection. Tenant-visible
(self-service capacity dashboards). Cluster-operator-visible (revenue
reporting).

### 22.6 Tenant lifecycle subject

`sylk://global/tenant-lifecycle/v1`:

- `tenant.created`
- `tenant.suspended` (publish/subscribe disabled, data retained)
- `tenant.frozen` (read-only, no writes)
- `tenant.offboarded` (KEK destroyed, key material gone, residual data
  inaccessible)
- `tenant.exported` (full bundle of tenant subjects exported per data-
  portability obligations)

BFT-replicated; cluster operator + tenant operator both sign tenant
lifecycle transitions.

### 22.7 Quota burst dynamics

§22.1 describes per-tenant quota subjects. Burst behavior — how a
tenant's bursty workload interacts with steady-rate quota
enforcement — is normative below.

**Algorithm: hierarchical token bucket + sliding-window observation +
earned-burst credits.**

**Per-quota-dimension token bucket**: each
`(tenant, class, dimension)` triple — e.g.,
`(tenant_x, Critical, msg_per_sec)` — has a token bucket with:

- `rate`: refill rate (tokens/sec) = published quota.
- `capacity`: max tokens = `rate × burst_seconds` where
  `burst_seconds` is class-specific:
  - Critical: 10s burst (low; preserves predictability)
  - Standard: 60s burst
  - Bulk: 300s burst (5min; high tolerance)
  - Background: 600s burst
- `tokens`: current bucket level.

**Hot-path enforcement**:

```go
publish(class, size):
    cost := computeCost(class, size)
    for {
        cur := atomic.Load(&bucket.tokens)
        if cur < cost {
            return ErrQuotaExceeded { RetryAfter: refillETA(bucket, cost) }
        }
        if atomic.CAS(&bucket.tokens, cur, cur - cost) {
            return nil
        }
    }
```

Single CAS; no locks. Hot path ~10ns. Token generation is a separate
background goroutine that adds tokens at refill rate (rate-limited to
avoid overshoot).

**Hierarchical buckets**: tenant has a global bucket; per-subject
sub-buckets borrow from tenant. Sub-bucket exhaustion lets sibling
subjects continue (until tenant bucket also exhausted).

```
tenant.global_bucket
    ├── subject_A.bucket (borrows from global)
    ├── subject_B.bucket
    └── subject_C.bucket
```

**Sliding-window observation alongside**: 5-second sliding window of
actual rate, exposed via §22.5 cost accounting. Used for billing
accuracy + abuse detection (anomalous spike beyond bucket capacity in
short window). Sliding window doesn't gate publishes — token bucket
does. Window observed independently for forensic analysis.

**Earned-burst credits**: every minute under utilization < 50% earns
1 minute-of-burst-credit. Credits stored in a separate bucket
(capacity 60 = 1 hour bonus burst). Credits consumed first when
traffic exceeds steady-state rate. Encourages bursty workloads while
preventing starvation.

**Distributed accounting**: per-tenant bucket state in BFT-replicated
quota subject (§22.1). Updates via atomic counter increments
piggybacked on publish path. Leader holds canonical counter;
followers see eventually-consistent view, but enforcement is local-
first with reconciliation:

- Local enforcement: each gateway / replica enforces against its
  local share of tokens.
- Reconciliation: every 1s, gateways reconcile via the BFT quota
  subject. Over-spend at one gateway compensated by reduced share
  next interval.
- Drift bound: typical ≤ 1% over-spend per reconciliation interval.

**Cross-DC borrow/lend**: tenant's quota split per-DC by configured
ratio. If DC-A is idle and DC-B is busy, A's unused tokens lend to B
up to a cap (default 25% of A's allocation). Implemented via the
federation control plane: tenant's quota subject is BFT-replicated;
per-DC sub-buckets coordinate via BFT entries.

**Choking precedence under tenant exhaustion**:

1. Background drops first (no retry).
2. Bulk drops next (with retry budget).
3. Standard delays (queued with bounded wait).
4. Critical preserved up to a hard mini-quota (10% of total
   reserved; never raided by lower-priority drops).
5. If Critical mini-quota exhausted: hard refuse with
   `ErrCriticalQuotaExhausted`; alerts operator.

**Backpressure protocol**: `ErrQuotaExceeded` returned with
`RetryAfter` header indicating refill ETA. Client uses §10.3 retry
budget logic. Repeated quota errors trigger §10.4 circuit breaker.

**Telemetry**:
- `quota_tokens_remaining{tenant, class, dimension}` — gauge.
- `quota_consumed_total{tenant, class, dimension, result}` —
  counter (consumed vs rejected).
- `quota_borrow_lend_bytes{src_dc, dst_dc, tenant}` — cross-DC
  borrow.
- `quota_burst_credits_balance{tenant}` — earned credits.

**Soundness invariants**:
- No tenant can exceed its declared rate over any 1-hour window
  (sliding-window enforcement plus token bucket).
- Burst capacity bounded by `rate × burst_seconds + earned_credits`.
- Cross-DC borrow respects total tenant quota (DCs cannot
  collectively over-spend).
- Critical class never starved by Bulk/Background under exhaustion.

---

## 23. Time and Clocks at Global Scale

HLC drift bound of 500ms is fine intra-DC, tight cross-DC, insufficient
across continents under adversarial clocks.

### 23.1 Bounded clock service (TrueTime-style)

Operator can configure cluster-wide bounded clocks: GPS/atomic-fed time
servers per DC, NTP/PTP discipline with measured uncertainty bound.
Substrate consumes bound from a `BoundedClock` interface.

Frame HLCs become `(physical, logical, node, uncertainty_ns)`. Uncertainty
is the maximum clock skew the substrate can guarantee at frame stamp time.

Cross-DC linearizable reads can wait out the uncertainty: "I want to
read with linearizability across DCs" → wait for uncertainty bound to
elapse before serving. Uncertainty under TrueTime-class hardware is
microseconds; under software NTP, milliseconds; under no clock service,
the §3.2 default 500ms.

The substrate doesn't *require* TrueTime; it *supports* it as a policy
choice that tightens cross-DC linearizability latency.

### 23.2 HLC fencing primitive

Explicit substrate-level operations:

- `WaitUntil(hlc)` — block until local HLC ≥ hlc.
- `ObservedAfter(hlc)` — guard a read so it reflects all entries with
  HLC ≤ argument.
- `FenceWrite(hlc)` — write that won't commit before HLC argument.

Necessary for "I want this read to reflect any write completed before
wall-clock T." Currently every subsystem hand-rolls this.

### 23.3 Skew telemetry per node

Continuous measurement of `HLC.PhysicalNs - max(observed_peer_HLC)` per
peer. Skew above threshold:

- Substrate-level health signal (separate from SWIM/φ-accrual).
- Drives gateway selection away from skewed nodes.
- Triggers `sylk://global/security/clock-skew/v1` event for operator
  alerting.
- Beyond extreme threshold (e.g., 5s), node partitioned from write
  quorum participation until clock recovers.

### 23.4 Logical-only mode for adversarial clocks

If the cluster operator declares clock infrastructure compromised,
substrate switches to logical-only mode: HLC physical component frozen,
only logical counter advances. Total order preserved; happens-before
preserved; freshness lost (cannot wait out uncertainty for cross-DC
linearizable reads).

Recovery: external attestation that clocks are honest again; gradual
physical-clock reincorporation with bounded slew rate.

---

## 24. State Machine Safety and Determinism

§3 commits to Raft + state machines but assumes state machines are
deterministic. At scale, this assumption needs structural enforcement.

### 24.1 Determinism harness

A test infrastructure that:

1. Captures committed Raft logs in test/canary cluster.
2. Replays into N parallel state machine instances on different machines,
   OS versions, Go versions.
3. Bit-compares state at every committed index.

Run on every state-machine code change. Detect: map iteration ordering,
pointer-equality dependence, `time.Now()` leakage, `math/rand` without
seed, goroutine-scheduling-dependent code.

Static analysis (lints) supplements: forbid `time.Now`, `math/rand`
(use seeded variants), `range map` without key sort, in state-machine
packages.

### 24.2 State-machine code versioning

Each state machine has a stable `(name, version, code_hash)` triple. Raft
entries record which SM version applied them. Replicas refuse to apply
an entry committed under a SM version they don't have:

```
follower receives entry committed under SM v17
follower has SM v16 only
→ follower stalls, alerts operator
→ operator deploys v17 to follower
→ replication resumes
```

Prevents silent divergence where two replicas run subtly different SM
code. Slow rollout requires *all* replicas to have *all* versions in the
rollout window before any one becomes the active SM.

### 24.3 Poison-pill quarantine

A bug in the SM that crashes on a specific entry would crash every
replica in sequence as that entry replicates. Defense:

```
SM apply boundary {
    recover from panic →
        log entry as quarantined (full evidence: input, stack, version)
        publish event to sylk://global/sm-quarantine/v1
        skip entry, continue with next
}
```

Quarantined entries are *not* dropped from the log; they're marked
unsafe-to-apply. Operator alert fires. A subsequent SM version (with
bugfix) can re-apply quarantined entries via a retry directive.

Better failure mode than uniform crash + cluster bricking.

### 24.4 Crash-loop bounded

Three SM crashes in 60 seconds → replica enters "safe mode": refuses new
entries until operator clears. Prevents replica thrashing under malformed-
entry attack.

### 24.5 Shadow build / dual-version verification

Every SM version is dual-built:

- Primary version applies entries and serves reads.
- Shadow version applies the same entries in a separate goroutine; states
  are compared periodically.
- Divergence between primary and shadow → cluster-wide alert; primary
  halts before commit.

Powers safe rollouts of state-machine refactors and bugfixes — the new
version proves equivalent on real production traffic before it cuts over.

### 24.6 Reproducible builds and provenance

Every deployment is content-addressed: `(binary_blake3, config_blake3,
schema_set_blake3)`. Cluster join protocol verifies the joining node's
hash against operator-approved hashes published to
`sylk://global/deployment/v1`. Mismatched hashes → join refused.

End-to-end provenance: source code → reproducible build (Bazel/Nix) →
binary hash → deployment manifest → cluster admission. Forensic
reconstruction of "what code was running at HLC H" is a substrate query.

### 24.7 Multi-version SM coexistence during long-running transactions

§24.2 specifies SM versioning at the entry level: replicas refuse
entries from unknown SM versions. Long-running transactions add a
wrinkle: a transaction that BEGAN under v17 may need to COMMIT under
v17 semantics even after the cluster has rolled to v18.

**Transaction-version pinning**: every transaction stamps at BEGIN
with `(sm_name, sm_version, code_hash)` for every namespace it
touches. The pin is part of the transaction's durable record (in the
coordinator's Raft log per §6.4 and in each participant's tx-log
subject).

**Version-stable apply**: each engine's apply path uses the
transaction's pinned SM version, not the cluster's "current" version.
As long as the pinned version is loaded on the replica, apply
succeeds. Replicas hold N versions concurrently during rollouts
(default 3 prior versions; configurable per cluster).

**Dual-version coexistence window**:

```
operator deploys v18 → cluster runs both v17 and v18 simultaneously
new transactions:    pin to "active version" (operator-declared)
in-flight v17 txns:  continue under v17 pin
window closes when:  (a) all in-flight v17 txns completed, OR
                     (b) operator force-aborts remaining v17 txns
```

**Force-upgrade protocol** (when window must close before natural
completion): operator publishes `tx.force_abort{tx_id, reason}` for
each in-flight transaction pinned to the version being retired.
Their state rolls back; clients see `ErrSMVersionForceAbandoned`;
clients re-issue under the new version. Used when operator must
purge an old version cluster-wide (e.g., security fix mandates
immediate rollover).

**Read-side version pinning**: long-running reads (snapshot reads,
time-travel queries §12.1) pin similarly. A read started at HLC X
reads with the SM version active at X for that subject. Per-version
state preserved in snapshots (§7.4).

**Cross-namespace-tx interaction (§6.4)**: coordinator's pinned
version applies to all participants. If any participant doesn't have
that version loaded, the transaction is rejected at PREPARE with
`ErrSMVersionMissing`. Coordinator handles by aborting the
transaction; client retries under whatever version is universally
available.

**BEGIN CONCURRENT (§B.3) interaction**: a v17 transaction's read-set
+ version pin defines what conflicts with a v18 commit. v18 commit
modifying a key in v17's read-set conflicts iff the v18 *semantics*
affect v17's read interpretation. Default: cross-version commits
*always* conflict (force serial); operator can declare per-subject
"v17/v18 read-set-disjoint" via the version-compat table to relax
the constraint when known safe.

**Bounded transaction lifetime per class**:

| Class      | Max lifetime |
|------------|--------------|
| Critical   | 1 hour       |
| Standard   | 1 hour       |
| Bulk       | 24 hours     |
| Saga       | unlimited (version-decomposed per step) |

Beyond limit: transaction auto-aborted with
`ErrSMVersionLifetimeExceeded`.

**Saga step-level versioning** (§26.1): each saga step pins its own
SM version. A long-running saga can survive multiple SM upgrades by
upgrading versions step-by-step (each step pinned at step start).
Compensation steps pin to the version that committed the original
step.

**Storage**: per-version SM state stored in tagged keyspaces. v17's
state lives in `keyspace_v17/`; v18's in `keyspace_v18/`; reads
dispatch by transaction's pin. After v17's coexistence window closes
+ retention period (typically 24h-7d), v17's keyspace is GC'd.

**Operator workflow for SM upgrade**:

1. Stage v18 deployment (§24.6 reproducible build registered).
2. Operator publishes "active version: v18" to
   `sylk://global/sm-active/v1`. New transactions pin v18.
3. In-flight v17 transactions continue under v17.
4. Monitor: `multitx_pinned_version{name, version}` shows count of
   in-flight transactions per pinned version.
5. When count for v17 reaches 0 → coexistence window closes; v17
   keyspace eligible for GC.
6. If count doesn't reach 0 within bounded time (e.g., 24h after
   operator-declared cutoff) → operator issues force-upgrade.

**Soundness invariants**:
- Every transaction commits under exactly the SM version pinned at
  its BEGIN.
- Cross-version conflicts handled deterministically per the
  version-compat table.
- Replicas converge: replica with both v17 and v18 loaded produces
  identical state to any other replica with both versions, given the
  same transaction graph + same HLC ordering.
- Force-aborted transactions roll back atomically; no partial state
  left across versions.

---

## 25. Operational Model: Declarative and Cloud-Native

The operator surface itself should be declarative and cloud-native.

### 25.1 Cluster CRDs

First-class declarative resources reconciled by an operator service
(K8s Custom Resources where applicable; equivalent file-based format
elsewhere):

| CRD                  | Purpose |
|----------------------|---------|
| `SubjectPolicy`      | Schema, retention, durability class, authority predicates, encryption policy, tier policy |
| `NamespacePlacement` | DC affinity, replica count, witness allocation, learner allocation, geo-fencing |
| `TenantQuota`        | Per-tenant limits (storage, throughput, namespaces, federation bandwidth) |
| `BackupPolicy`       | Backup target, frequency, retention, encryption, verification cadence |
| `FederationPeer`     | Peer cluster, trust root, allowed subjects, rate limits |
| `OperatorAuthority`  | Cluster-operator capability bindings (separate from user authority) |
| `EnvelopeKeyPolicy`  | KEK rotation cadence, DEK rotation cadence, escrow policy |
| `ClockServicePolicy` | Bounded-clock provider, uncertainty thresholds, skew alerts |

### 25.2 Operator pattern

`sylkd-operator` is a control-loop service:

1. Watches CRDs.
2. Compares to substrate state (via operator-group reads).
3. Issues operator-group writes to reconcile (create namespace, change
   retention, rotate keys).
4. Drift detection: substrate state diverging from CRDs raises a
   reconciliation event.

GitOps-friendly: cluster spec lives in a repo; Argo/Flux applies CRD
changes; operator reconciles; substrate state matches Git within seconds.

### 25.3 Node bootstrap via SPIRE

Stop hand-rolling SVID issuance. SPIRE agents per node, attestation by
node-type (K8s service account, AWS IID, GCP MDS, bare-metal hardware
attestation), automatic SVID rotation, integration with cluster CA.

### 25.4 Pod disruption budgets per Raft group

K8s-native: tag namespace group replicas with their group ID; PDB
enforces "at most floor((replicas-1)/2) replicas of any group can be
evicted simultaneously." K8s rolling updates physically cannot kill
quorum.

### 25.5 Topology-aware scheduling

Substrate publishes topology hints (Vivaldi sections, DC labels, rack
labels) to K8s scheduler via node labels and pod topology spread
constraints. Replicas land in different failure domains by construction.

### 25.6 Capacity / autoscaling primitives

- **Stateful node autoscaling**: substrate publishes per-node load (CPU,
  IO, group count); operator group decides whether to provision additional
  voters or learners and where.
- **Gateway autoscaling**: stateless gateways scale with HPA on connection
  count + bandwidth.
- **Tenant-driven scaling**: per-tenant quota approaching → emit
  `tenant.quota.high-water` event; tenant operator can auto-bump quota
  or alert humans.

### 25.7 Upgrade orchestration

Substrate-managed rolling upgrade:

1. Operator publishes target binary hash to `sylk://global/deployment/v1`.
2. Reconciler picks one node per failure domain; drains, upgrades, re-
   attests, re-joins.
3. Quorum-aware: never drains more than `floor((n-1)/2)` voters of any
   group simultaneously.
4. Per-version compatibility check: §24.2 SM version table enforced.
5. Canary cohort: 5% of nodes upgraded first; cluster-wide divergence
   detector watches; rollback if anomaly.

### 25.8 Multi-cluster control plane

For federation: an *upper-tier control plane* manages multiple clusters
as a fleet. `Cluster` is itself a CRD; deploying a new cluster is a
declarative operation; federation membership is a CRD.

---

## 26. Application Primitives (Extended)

§11 (Layer 8) covers KV, object, claims, forest, fabric, VFS, authority.
Six more primitives generalize patterns Sylk hand-rolls today.

### 26.1 Saga primitive

Long-running, multi-step business workflows with explicit compensations.
Each step is a substrate entry with a `compensation_ref` linking to its
undo.

```
sylk://session/<id>/saga/v1
  - saga.started      { saga_id, steps[] }
  - step.completed    { saga_id, step_id, compensation_ref }
  - step.failed       { saga_id, step_id, reason }
  - compensation.applied { saga_id, step_id }
  - saga.committed    { saga_id }
  - saga.rolled-back  { saga_id }
```

Saga coordinator is a state machine. The agents-claims-testaments-
artifacts flow IS a saga; making sagas first-class generalizes claim
remediation to arbitrary multi-agent workflows (multi-step refactors,
incremental migrations, knowledge-graph reindexes).

Recovery: a coordinator crash mid-saga recovers from the saga subject;
in-progress steps either complete or compensate per their durable state.

### 26.2 Typed CRDT subjects

KV LWW is one resolution rule. Add CRDT types as subject kinds:

| Kind           | Type                          | Use |
|----------------|-------------------------------|-----|
| `g-counter`    | grow-only counter             | Monotonic counts (hits, claims-issued, errors) |
| `pn-counter`   | +/- counter                   | Reversible counters (active sessions) |
| `or-set`       | observed-remove set           | Tag sets, member sets |
| `lww-map`      | LWW map                       | Current `kv` (renamed) |
| `mv-register`  | multi-value register          | Concurrent updates exposed for app resolution |
| `rga`          | replicated growable array     | Collaborative text (multi-agent code edits) |
| `crdt-graph`   | 2P-graph                      | Knowledge graph fragments converging across DCs |

CRDT subjects merge across cross-DC partitions without 2PC. Powers multi-
agent collaborative editing without locking. State machine encodes the
merge function; substrate replicates entries; replicas converge.

### 26.3 Optimistic concurrency primitive

`Publish(..., expect=hlc_frontier)` — publish only if subject's current
HLC frontier matches `hlc_frontier`. Atomic CAS at substrate level.
Currently each subsystem hand-rolls (claim resolution, artifact
publication, commit ordering). Promoting it makes conflict semantics
explicit and uniform.

`expect` can also reference content-addressed subject root (Merkle root
at frontier). CAS-on-content.

### 26.4 Workflow composition primitives

- **Lease**: time-bound exclusive grant on a resource; re-issued or
  expired via substrate timer.
- **Barrier**: "wait until N participants reach barrier"; HLC-frontier-
  based.
- **Counter (limit)**: distributed counter with cap; publishes blocked
  when cap reached.
- **Voting / quorum**: subject schema declares "approve threshold";
  participants publish votes; substrate emits resolution entry on
  threshold.

### 26.5 Capability macaroons

Beyond static SVID-bound capabilities, support per-operation macaroons
with caveats:

- Time bounds ("expires at HLC H").
- Predicate caveats ("only if subject = X").
- Third-party caveats (delegated capability requiring third-party
  attestation).
- One-shot caveats (single use, tracked in dedupe).

Authority predicate evaluates SVID + macaroon stack. Powers delegation
without round-trip to authority issuer; useful for "this user grants
this agent this scoped capability for this session."

### 26.6 Probabilistic data structures as subject kinds

For unbounded-cardinality analytics with bounded memory:

| Kind      | Type                    | Use |
|-----------|-------------------------|-----|
| `hll`     | HyperLogLog             | Distinct counts (unique users, unique sessions) |
| `cms`     | Count-Min-Sketch        | Frequency estimation |
| `tdigest` | t-digest                | Latency / size percentiles |
| `bloom`   | Bloom filter            | Membership tests over large sets |

State machine merges sketches across replicas (HLL union, CMS sum,
t-digest merge are all associative). Cross-DC merge is free.

### 26.7 Algebraic effect handlers (resume + restart)

Workflow composition needs more than chains, fanouts, and conditionals.
It needs first-class control-flow primitives for retries, early returns,
typed errors, recursion, timeouts, and resource lifecycle. Implementing
each separately gives you N hand-rolled mechanisms; implementing them
all on top of *algebraic effect handlers* gives you one mechanism.

Sylk adopts the pattern from Barnum (`../barnum`): two effect styles —
**resume** and **restart** — built into the substrate as workflow
primitives.

#### Resume-style effects

A `ResumeHandle` wraps a body. When a `ResumePerform(id)` fires inside
the body, control transfers to the handler *inline* at the perform
site. The handler returns `[value, new_state]`. The engine delivers
`value` to the perform's parent (so the body continues with that
value); `new_state` becomes the new state of the handle frame
(persisting across multiple performs into the same handler).

Use cases:
- Iterators: `next()` performs into a handler that yields the next
  element + advances the iterator state.
- Counters: a perform increments + returns the value.
- Generators: stateful resumable computation.

Wire form:

```
ResumeHandle {
    resume_handler_id:  uint16
    body:               <action subtree>
    handler:            <action subtree>     // produces [value, new_state]
    initial_state:      <Value>              // initial state of the frame
}

ResumePerform {
    resume_handler_id:  uint16              // matches enclosing handle
    perform_payload:    <Value>             // input to handler
}
```

#### Restart-style effects

A `RestartHandle` wraps a body. When a `RestartPerform(id, payload)`
fires, the body is *torn down*; the handler runs with `payload`; the
handler's output becomes the new input to the body, which re-advances
from scratch.

Use cases (these all encode as restart effects, *not* as separate
primitives):

| Higher-level primitive | Encoding |
|---|---|
| `loop((recur) => body)` | RestartHandle wraps body; recur is `RestartPerform` with the new iteration's input |
| `tryCatch(body, recovery)` | RestartHandle wraps body; throw is `RestartPerform` tagging the error; restart routes to `Branch({ Continue: body, Break: recovery })` |
| `earlyReturn` / `break` | RestartPerform without a Continue branch; restart drops the body |
| `withTimeout(body, ms)` | RestartHandle + a timer that fires `RestartPerform` after `ms` |
| `retry(body, maxAttempts)` | RestartHandle with a counter; failure performs; handler re-issues body if counter > 0 |
| `withResource(setup, body, teardown)` | Setup runs first; restart on body failure runs teardown then re-restarts |

Wire form:

```
RestartHandle {
    restart_handler_id: uint16
    body:               <action subtree>
    handler:            <action subtree>     // produces new body input
    initial_input:      <Value>
}

RestartPerform {
    restart_handler_id: uint16
    perform_payload:    <Value>
}
```

#### Substrate semantics

Effects are substrate-replicated. The handle frame's state lives in
the namespace's Raft state machine. A `RestartPerform` from a replica
is a substrate entry; the corresponding handle's tear-down + re-advance
is the SM's response. Bit-equal across replicas (per §24.1).

Effect handler scope is bounded: a `Perform` targets the *nearest
enclosing* handle with matching ID, walking the workflow's frame stack.
The lookup is deterministic (no ambiguity); cross-effect interference
impossible because handler IDs are typed (resume IDs distinct from
restart IDs at the type level).

#### Why this matters

Today, Sylk has corrective claims (CLAIMS.md §14.11 #9d), saga
compensation (§26.1), retry budgets (§10.3), circuit breakers (§10.4)
— each a separate hand-rolled mechanism for a specific failure mode.
With algebraic effects, all of these collapse into one substrate
primitive with type-safe composition. New failure-handling patterns
become libraries on top, not new substrate features.

#### Soundness

- Effects compose: `tryCatch(loop(body))` works as you'd expect.
- Effect handler IDs are typed (resume ≠ restart) — cross-binding
  errors caught at compile time.
- A `Perform` with no matching handle is a structural error caught
  at workflow registration (compile-time).
- State transitions through resume-handler frames are deterministic
  given the input sequence; replicas converge.

### 26.8 Compositional workflow combinators

Effects (§26.7) provide the underlying mechanism. The user-facing
language is a set of higher-order combinators that compose claims
and pure data transforms into workflows.

```
sylk://session/<id>/workflow/v1     // workflow definitions as subject
sylk://session/<id>/workflow-runs/v1 // workflow execution traces
```

#### Combinator surface

| Combinator | Type | Semantics |
|---|---|---|
| `invoke(handler)` | `Action<TIn, TOut>` | Leaf node. Calls a claim or builtin. |
| `pipe(a, b, c, ...)` | sequential composition | `a` → `b` → `c`. Output of one feeds input of next. |
| `forEach(handler)` | `Action<[]TIn, []TOut>` | Parallel map over array input. |
| `all(a, b, c, ...)` | fanout | Same input to all; collects results as tuple. |
| `branch({k1: a, k2: b, ...})` | conditional | Routes by `kind` field of discriminated-union input. |
| `loop((recur) => body)` | recursion | Encoded via restart effects. `recur(value)` re-enters loop with new value. |
| `tryCatch(body, recovery)` | typed error handling | Encoded via restart effects. |
| `withTimeout(body, ms)` | bounded execution | Encoded via restart effects + substrate timer. |
| `withResource(setup, body, teardown)` | resource lifecycle | Setup-body-teardown with guaranteed cleanup on body failure. |
| `race(a, b, c, ...)` | first-to-complete | Returns first result; cancels others. |

#### Built-in pure transforms

The substrate ships a registry of pure-data transforms — these compile
to native Go at workflow registration; no runtime VM:

```
Constant(v), Identity, Drop, Merge, Flatten, GetField(name),
GetIndex(i), CollectSome, SplitFirst, SplitLast, WrapInField(name),
ExtractPrefix, BoolToOption, Sleep(ms), Tag(label),
allObject, asOption, panic, pick, range, splitFirst, splitLast,
withResource
```

Same registry as Barnum's `builtins/`; codegen'd to native Go;
deterministic; substrate-replicated.

#### Type safety

Each combinator is `TypedAction<TIn, TOut>`. Composition typechecks
at workflow registration time:

```go
// Type error caught at registration:
pipe(
    listFiles,           // returns []string
    refactorOneFile,     // expects string, not []string
)
// → ErrWorkflowTypeMismatch: pipe stage 2 expects string, got []string

// Correct version:
listFiles.forEach(refactorOneFile)
// → forEach maps refactorOneFile over each element. Type check passes.
```

Schema validation (SWF-derived) verifies handler I/O at runtime
boundaries.

#### Workflow as substrate plan

A composed workflow compiles to a substrate execution plan via §31.29
substrate-as-IR. Each handler is a claim with progressive context
disclosure (§26.10). Effects map to corrective-claim chains via
Relations.

#### Example

```go
refactorWithRetry := substrate.Workflow.Pipe(
    refactor,
    evaluate,
    substrate.Workflow.Loop(func(recur substrate.Action) substrate.Action {
        return substrate.Workflow.Pipe(
            typeCheck, classifyErrors,
        ).Branch(map[string]substrate.Action{
            "HasErrors": substrate.Workflow.ForEach(fix).Drop().Then(recur),
            "Clean":     substrate.Workflow.Drop(),
        })
    }),
    commit,
    createPR,
)

substrate.Workflow.Run(
    listFiles.ForEach(refactorWithRetry),
)
```

Compiles to a substrate workflow subject; each step is a claim;
fan-out per file is parallel publish; loop is a restart-effect handle;
branch dispatches by validated input shape. Persisted, replayable,
time-travelable, audited.

### 26.9 Workflow-as-substrate-subject

Workflows themselves are first-class substrate subjects, not just
plumbing.

#### Subject shape

```
sylk://session/<id>/workflow/v1
  - workflow.submitted     { workflow_id, ast, schema_set, signed_hash }
  - workflow.executed      { workflow_id, run_id, input, hlc }
  - workflow.step_dispatched  { run_id, frame_id, handler_id, input }
  - workflow.step_completed   { run_id, frame_id, output, hlc }
  - workflow.step_failed      { run_id, frame_id, error, hlc }
  - workflow.effect_performed { run_id, frame_id, effect_kind, payload }
  - workflow.completed     { run_id, output, hlc }
  - workflow.aborted       { run_id, reason, hlc }
```

#### Operations

```go
// Submit a workflow definition. Returns content-addressed ID.
id := substrate.Workflow.Submit(ast, signing_svid)

// Execute a workflow against an input. Returns run_id.
run_id := substrate.Workflow.Execute(id, input)

// Replay a workflow at a historical HLC frontier (deterministic).
state := substrate.Workflow.Replay(run_id, hlc)

// Diff two workflow ASTs.
diff := substrate.Workflow.Diff(id_a, id_b)

// Causal cone of a workflow run (§12.2 graph walk).
cone := substrate.Workflow.CausalCone(run_id)

// Cancel an in-flight workflow run.
substrate.Workflow.Cancel(run_id, reason)
```

#### Properties

- **Content-addressed**: workflow ID is BLAKE3 of the AST. Same AST →
  same ID. Workflow registry deduplicates.
- **Signed**: workflow submission carries the submitter's SVID
  signature.
- **Persisted**: AST stored on substrate; survives crashes, replicates
  across federation.
- **Replayable**: given input + same handler implementations,
  re-running produces the same output (modulo external side effects
  which are themselves claim-mediated).
- **Time-travelable** (§12.1): "what was the state of this workflow
  run at HLC X?"
- **Diff-able**: workflow versioning is content-hash-driven; AST
  diffs visualize what changed.
- **Causal**: every step's parent edges link via Relations, queryable
  via §12.2 cone.
- **Audited**: every step dispatch + completion is a signed substrate
  entry; complete provenance trail.

#### Composition with §27 SQL surface

Workflows are queryable via SQL:

```sql
SELECT workflow_id, COUNT(*) AS run_count, AVG(duration_ms) AS avg_duration
FROM substrate.workflow_runs
WHERE submitted_after > NOW() - INTERVAL '7d'
GROUP BY workflow_id
ORDER BY avg_duration DESC;
```

Dashboards, regression detection, performance tracking — all built
on the substrate's existing analytical surface.

### 26.10 Progressive context disclosure (claims-level)

This extends CLAIMS.md §3 (sovereign-store + fabric-projection) with a
default change: per-claim, the agent receives *only* the claim itself
(description + scope + validations) plus the immediately required
testament target. Wider context (board state, peer progress, ambient
envelope, cross-pipeline activity, fabric lenses) is *opt-in* via
explicit declaration on the claim.

#### Why default-narrow

Empirically, LLM agents perform better with focused context. A
20K-token ambient envelope with 8K of unrelated peer progress + 5K of
historical narrations + 3K of forest precedents leaves 4K for actual
task work. Same agent with 4K of focused task context performs
substantially better.

Barnum's "progressive disclosure" is the same principle: each handler
sees only its declared input. Sylk's claim system has a richer multi-
agent visibility model, but the *default* should be narrow; opt-in
should be explicit.

#### Mechanism

Claim's schema carries a `context_envelope` field declaring what
ambient context to include:

```
ClaimContextEnvelope {
    include_board_state:     bool        // default false
    include_peer_progress:   bool        // default false
    include_ambient_fabric:  bool        // default false
    include_recent_testaments: int       // 0 = none; default 0
    include_forest_precedents: int       // 0 = none; default 0
    include_kg_neighbors:    int         // 0 = none; default 0
    custom_lens:             []LensSpec  // explicit lens queries
}
```

Default: all false / 0. Agents receive: claim description, scope,
validations, testament target. Nothing else.

Architects authoring claims explicitly opt into wider context per
claim. Inspector / Tester / Engineer claims for typical implementation
work stay narrow. Architect / Strategist claims that genuinely require
cross-pipeline awareness opt in.

#### Saga / workflow context inheritance

Within a saga or workflow, context flows by *explicit* output → input
mapping (§26.8 combinators). A handler's input is the previous step's
output; ambient context isn't inherited. This matches Barnum's pattern
exactly.

#### Compatibility

Existing Sylk agents continue working: their claim schemas declare
the wider context they currently consume. Migration is *opt-out*
context narrowing per claim, not a breaking change.

#### Soundness

- Per-claim context is bounded by the schema-declared envelope.
- Substrate enforces: agents cannot read ambient state not declared in
  their claim's envelope (the authority predicate refuses cross-claim
  reads beyond declared scope).
- Audit: the envelope itself is part of the claim record. "What
  context did this agent see?" is a substrate query.

#### Performance

Token-cost reduction is empirical: typical implementation claims
narrow from ~20K tokens of ambient + ~4K of task to ~4K of task.
5x token cost reduction per agent step at no quality cost (typically
better quality, given the LLM-context literature).

---

## 27. Hot-Path Scaling Extensions

§3-§7 scales linearly in namespace count. A few specific cliffs at
extreme scale.

### 27.1 Sub-subject sharding

A single hot subject (one massive session, one global broadcast) is
bottlenecked by its single Raft group's leader. Add sub-sharding by
partition_key hash:

```
sylk://session/<id>/claims/v3
  ├── shard 0:  partition_keys hashing to [0, 2^16)     ← Raft sub-group
  ├── shard 1:  partition_keys hashing to [2^16, 2^17)  ← Raft sub-group
  ├── ...
  └── shard 15: ...
```

- Each shard is its own Raft sub-log with HLC ordering preserved within.
- Cursor resume crosses shards via HLC frontier vector (one HLC per
  shard).
- Cross-shard ordering relies on HLC alone (no global Raft order across
  shards).

Effectively "Multi-Raft inside a namespace." Caller-transparent: the
subject API is unchanged; partitioning happens substrate-side.

### 27.2 Replication topology adaptation

Static 3-replicas-per-group is wrong for both small and large scale.

- **Pipelined replication for write-heavy paths**: leader → follower 1
  → follower 2 instead of leader → all followers; pipeline overlap
  reduces commit latency at the cost of one extra hop on the slow path.
- **Gossip-augmented replication for read-mostly subjects**: followers
  gossip recent log entries with each other; reduces leader fan-out load.
- **Topology selected per subject** based on observed write/read ratio;
  reconfiguration is online (joint consensus).

### 27.3 Adaptive group commit window

Static 2ms / 1MB is wrong for laptop (overkill latency) and global
cluster (too tight under cross-DC RTT).

Adaptive policy: target a configurable latency percentile (default p99
of write_latency ≤ 50ms); window adjusts dynamically based on observed
write rate, fsync latency, and replication latency. Bounded between
configurable min/max (default 100µs to 50ms).

### 27.4 Zero-copy subscriber fan-out

When 10K consumers subscribe to one view subject, currently each delivery
is a separate frame. Optimization:

- Storage layer returns content-addressed body hashes to delivery layer.
- Delivery sends `(entry_header, body_hash)` to consumers; bodies fetched
  from a node-local content-addressed cache on demand.
- Subscribers on the same node share cache → body fetched once per node,
  not per consumer.
- For massive fan-out (10K+ consumers in one DC), substrate-internal
  multicast tree (Raft group leader → designated forwarders → consumers)
  further reduces leader bandwidth.

### 27.5 Erasure-coded Raft logs

For very large entries, full body replication is wasteful. Optional:
per-entry Reed-Solomon (k+m) replication where m parity shards span
DCs. Read requires k of (k+m) shards. Tolerates m simultaneous shard
losses.

Trade-off: increased read complexity, slower single-replica fast-path.
Used selectively for bulk-class subjects with large bodies.

### 27.6 Persistent memory tier (CXL / PMEM)

Where hardware permits, substrate exposes a PMEM tier between DRAM and
NVMe:

- Sealed segments live in PMEM until cold-tier migration.
- Recovery time: microseconds (no disk replay), not milliseconds.
- mmap'd PMEM for true byte-addressable persistence.

Falls back gracefully to NVMe-only on machines without PMEM. Same code
path; storage abstraction layer handles backing.

### 27.7 Network coding cross-DC

Cross-DC bandwidth is the dominant cost at global scale. Linear network
coding mixes multiple frames into encoded packets; receiver decodes when
enough received.

- Tolerates packet loss without retransmission (R = original / encoded
  ratio configurable).
- Reduces effective tail latency (no head-of-line blocking on
  retransmits).
- Standard per-DC pair, opt-in per subject for high-rate cross-DC
  subjects.

### 27.8 Per-class congestion-control parameter tuning policy

§25.3 selects CC algorithm per class. §27.3 has adaptive group
commit. The tuning *policy* — when and how parameters get retuned —
is normative below. Three-tier regime: static defaults +
operator overrides + closed-loop optimizer with rollback gating.

**Tier 1 — Static class defaults** (ship with sylk):

| Class      | Algorithm | Parameters                                         |
|------------|-----------|----------------------------------------------------|
| Critical   | BBRv2     | gain=1.25, ProbeRTT every 10s, min_rtt window=10s |
| Standard   | CUBIC     | β=0.7, C=0.4, max_cwnd=16MB                       |
| Bulk       | CUBIC     | β=0.5, init_cwnd=32                               |
| Background | LEDBAT    | target_delay=100ms, gain=1.0                       |

Defaults reviewed quarterly against the §15.2 benchmark suite.
Changes shipped as patch releases.

**Tier 2 — Operator overrides via CRD (§25.1)**: per-cluster,
per-class, per-CC-parameter overrides. Version-pinned (§24.2);
rollout via §25.7 upgrade orchestration. Override schema explicit
about which parameters are tunable; substrate refuses unknown
parameters.

**Tier 3 — Adaptive optimizer** (§31.25 + closed-loop): substrate
observes performance and *proposes* parameter changes; operator
approves before apply.

**Tuning trigger conditions**:

- **Sustained latency divergence**: rolling p99 of a class deviates
  from the operator-declared target by > 50% over 1 hour →
  optimizer proposes change.
- **Buffer-bloat detection**: per-flow average queueing delay >
  `target_delay × 2` for 10 min → propose more conservative CC.
- **Loss-rate change**: 7-day rolling loss-rate change > 50% →
  reconsider CC algorithm choice. Loss correlated with bandwidth
  probing (BBR sign) vs congestion (CUBIC sign) drives the
  recommendation.
- **Throughput shortfall**: actual throughput < 70% of expected for
  a sustained period → propose more aggressive parameters.

**Proposal protocol**:

1. Optimizer publishes proposal to
   `sylk://global/cc-tuning-proposals/v1` with rationale, projected
   impact, target metric, fallback plan.
2. Operator reviews via UI / CLI; signs + approves OR rejects.
3. Approval triggers staged rollout (per §25.7):
   - 5% canary fleet receives the change.
   - 24-hour canary observation window.
   - Auto-promote (if metrics improve / hold) or auto-rollback (if
     regress).
4. Audit recorded in `sylk://global/cc-tuning-history/v1`.

**Closed-loop validation**: each parameter change tracked by a
"tuning epoch" identifier. Pre-change vs post-change metrics
compared via paired statistical test (paired t-test on SLO
indicators: latency p99, throughput p50, error rate, loss rate).
Significance threshold: `p < 0.01`. If post-change metrics regress
significantly → automatic rollback within minutes.

**Per-deployment-shape baselines**:

- **Embedded**: trivial CC; in-process channels mostly. CC parameters
  near-degenerate.
- **Single-host daemon**: localhost-optimized; extreme low RTT.
- **Single-DC cluster**: standard parameters.
- **Multi-DC cluster**: cross-DC RTT-aware; bigger windows; Critical
  class uses BBR exclusively.
- **Federation**: per-pair overrides for known-degraded peer paths.

**Per-link tuning** (extreme cases): one peer with persistently poor
connectivity gets per-`(peer, class)` overrides. Substrate maintains
a small per-peer config map. Optimizer proposes per-peer overrides
when one peer's metrics persistently diverge from cluster average by
2+ standard deviations.

**Audit trail**: every parameter change records: who approved, when,
rationale, before/after metrics, rollback events. Queryable via
§12.2 causal cone for forensic analysis.

**Soundness invariants**:
- Tier 1 defaults always available; absence of overrides → defaults
  apply.
- Tier 2 overrides additive; partial overrides supported.
- Tier 3 changes always operator-approved; never auto-applied
  cluster-wide.
- Rollback path always reachable: prior parameter values retained
  in `cc-tuning-history`; rollback is a single approval.

**Telemetry**:
- `cc_active_params{class, algorithm, parameter}` — current values.
- `cc_proposal_total{result}` — proposals approved / rejected /
  rolled-back.
- `cc_observation_metric{class, metric}` — metrics feeding the
  optimizer.

---

## 28. Observability Extensions

§12 (Layer 9) covers time-travel, causal cone, audit, metrics. Three
more close the loop.

### 28.1 Native OpenTelemetry trace integration

Every entry has HLC and parent edges. Map directly to OTel:

- Entry → span.
- Parent edges → span links / `parent_span_id`.
- HLC → span timestamp (with HLC-aware ordering for span reconstruction).
- Subject → `service.name`.

Substrate becomes the largest tracing system the user has — without a
separate trace pipeline. Sampling policy at substrate level: sample
entire causal cones, not independent spans.

Bridge to existing OTel collectors via a substrate consumer that
translates entries to OTLP and forwards.

### 28.2 Counterfactual queries

"If this entry hadn't been published, what state would the subject be in
now?" Useful for debugging regressions:

1. Substrate forks the namespace at the entry's HLC.
2. Replays without the suspect entry.
3. Diffs against actual current state.
4. Returns the diff.

Falls out from time-travel + causal cone but needs an explicit API.
Powers "what did this rejected claim cause downstream?" answered as a
query, not a manual trace.

### 28.3 Anomaly detection feed

Substrate publishes its own behavioral metrics to
`sylk://global/observability/v1`:

- Slow replicas (φ score quartiles per group).
- Suspect peers.
- Quota approaches per tenant.
- Retry-budget approaches per (publisher, subject).
- Skew telemetry (§23.3).
- Compaction backlogs.
- Cold-tier read rates.

Operator dashboards consume the subject. Eats own dog food and time-
travels its own observability. Anomaly detectors (substrate-internal or
external ML) consume the same feed.

### 28.4 Causal blame analysis

Given an entry E with `outcome=failure`, automatic upstream walk: find
ancestors with high error correlation, return ranked candidates.
Statistical analysis over the causal cone informed by domain-specific
outcome labels.

### 28.5 Production tracing of state-machine internals

Optional flag on a subject: "trace SM execution." Each entry's SM
application emits an internal trace (function-call graph, execution
time per branch). Stored as a sibling subject. Powers "why did this
entry take 300ms to apply?" without instrumenting every SM by hand.

---

## 29. Embedded-Mode Specific Architecture

§16 mostly says "everything runs but smaller." Real laptop deployments
have constraints worth designing for.

### 29.1 Memory-pressure backpressure

OS memory pressure signal (`memory.high` cgroup on Linux, `vm.pressure`
on macOS, available memory on Windows) → substrate responds:

1. Pause Bulk + Background classes.
2. Shrink in-memory body cache.
3. Increase `madvise(DONTNEED)` for cold segments.
4. Defer compaction.
5. Surface "system under pressure" to TUI.

Today, OS would just OOM-kill. With this, substrate degrades gracefully
to "Critical only" before OOM.

### 29.2 Disk-full graceful degradation

Substrate owns disk; what happens when user fills it? Per-subject
reservation:

- Each subject reserves N MB at registration.
- Hit reservation → reject Critical with `ErrDiskFull`.
- Sealed segments queued for cold-tier upload (if configured) or wait.
- Active segment writes refused before corruption is possible.
- Surfaced as `sylk://global/storage-pressure/v1` events.

Background reaper: as cold-tier uploads complete, local sealed segments
freed.

### 29.3 Battery / thermal awareness

On laptop with battery / thermal pressure (OS power signals):

- Throttle background compaction.
- Defer snapshotting.
- Skip non-essential dedupe-table maintenance.
- Reduce HLC stamping rate for Background-class subjects.
- Defer cold-tier migration.

Cooperate with OS power signals (Linux `org.freedesktop.UPower`; macOS
`IOPMrootDomain`; Windows `SystemPowerStatus`).

### 29.4 Shared-memory transport for super-local IPC

Embedded uses Go channels (perfect for in-process). Multi-process on the
same host (TUI + sylkd) uses Unix sockets (~10-30µs).

For high-throughput IPC (knowledge stack ↔ substrate ↔ agents in
separate processes), shared-memory ring buffer is a fourth transport
shape:

- Lockfree SPSC/MPSC ring with HLC fences.
- Byte-addressable; no syscall per send.
- ~100ns latency.
- Used opportunistically when both ends are on the same NUMA node.

Same wire format; transport layer abstraction hides the difference.

### 29.5 Single-process multi-tenant safety

Even in embedded mode, a single user can run multiple sessions or a CI
runner with multiple agents. Tenant isolation primitives (§22) apply at
the goroutine boundary:

- Per-session resource quota (memory, file handles, goroutines).
- Per-session compaction queue.
- Per-session disk reservation.
- Cooperative deadline propagation.

Embedded user with N parallel sessions doesn't see one runaway session
monopolize the laptop.

---

## 30. Catastrophic Recovery

§18.4 covers single-DC partitions. Modes of total cluster failure deserve
explicit architecture.

### 30.1 State-machine bug bricking

An SM bug that crashes on a specific entry crashes every replica in
sequence. Defenses (composed):

1. §24.3 quarantine — replicas don't crash, they quarantine the entry.
2. §24.4 crash-loop bounded — replicas stop applying after thrashing.
3. §24.5 shadow build / dual-version — divergence detected pre-commit.
4. SM rollback subject. `sylk://global/sm-rollback/v1` carries operator-
   issued "halt SM v17, restore v16" commands. Replicas roll back state,
   replay from last-snapshot-with-v16 forward.

### 30.2 Encryption key loss

§21.2's per-tenant DEKs in HSM/KMS. Loss = data loss.

Defense: every DEK escrowed via Shamir's secret sharing, M-of-N
reconstruction. M-of-N parties can reconstruct in catastrophic recovery;
no single party can. KEK loss is recoverable; full-cluster compromise
of M parties is not (and that's appropriate).

KEK rotation re-issues escrow shares; old shares cryptographically tied
to specific KEK epoch.

### 30.3 Untrusted backup recovery

Restoring from backup: validate every Merkle root in the backup against
an out-of-band signed root list before applying. The signed root list
is published to:

- Multiple immutable object stores (cross-cloud).
- Out-of-band notarization (e.g., Sigstore Rekor transparency log).
- Hardware-tokens (operator-held YubiKey-signed roots).

Restore protocol: fetch Merkle root from K independent sources; require
unanimous match; reject backup if any source disagrees. Tampered backup
→ restore aborts; cluster operates on prior state.

### 30.4 Geo-fenced data residency violation recovery

Tenant declares "no replica may live in region X." Operational accident
causes replica placement in X. Recovery:

1. Detection: substrate self-audit verifies replica placement vs
   `NamespacePlacement` CRD geo-fences.
2. Containment: replica in X marked sealed (no further writes); reads
   disabled.
3. Migration: operator group moves replica out of X via joint consensus;
   old replica destroyed; KEK re-encrypts data.
4. Forensic: full audit trail of "data in X from HLC H1 to HLC H2;
   potentially read by …".

Compliance-grade incident response, mechanically driven.

### 30.5 Cluster-wide identity compromise

Cluster CA private key compromise = catastrophic. Defenses:

1. CA private key in HSM, M-of-N quorum to access.
2. Short-lived intermediate CAs for SVID issuance; roots stay air-gapped.
3. Cluster-wide CA rotation primitive: emit new root via federation
   channel; existing SVIDs cross-signed with old + new during transition;
   old root revoked after window.
4. Compromised SVIDs revoked via authority broadcast (§11.7).
5. End-state trust anchors stored in tamper-evident hardware (HSMs,
   YubiKeys, secure enclaves) per operator.

### 30.6 Quorum loss recovery

A namespace group with majority dead beyond recovery (e.g., DC permanent
loss without backup):

1. Operator explicitly authorizes "force-elect" with cryptographic
   operator quorum (M-of-N).
2. Substrate creates new replica from last available snapshot + cold
   backup.
3. Force-elected leader's first action: publish forensic event with
   operator authorization to `sylk://global/quorum-recovery/v1`.
4. Auditable: any future audit can verify "this namespace was force-
   recovered at HLC H by operators X, Y, Z."

Better than data loss; visible than pretending nothing happened.

---

## 31. Pushing the Envelope

The previous sections take us from "very strong substrate" to "production-
grade global SaaS." Below are the architectural moves that take the
substrate beyond that — into territory most distributed systems either
hand-wave away or treat as research projects. They are listed in order
of how much of the substrate they re-shape, not in order of priority.

### 31.1 Tiered programmable state machines

State machine code is Go, compiled into the binary, versioned via §24.2.
For "deploy new SM logic without rebuilding sylk" the cost/value
tradeoff varies sharply by use case. We use a tiered approach rather
than reaching for a runtime VM uniformly.

**Tier 1 — Declarative DSLs (95% of cases, near-zero overhead):**

For the vast majority of "extension" needs, declarative DSLs that
compile to native Go at registration time win on every axis:

- **Stored procedures**: PL/pgSQL-shape language; compiled to substrate
  IR (§31.29) and emitted as native Go via codegen at registration.
- **Wire transforms**: a registry of vetted transforms (compress,
  encrypt, redact, schema-migrate, mask). Operators *select* from the
  registry rather than authoring.
- **Projection / view definitions**: SQL-shape; compile to differential
  dataflow ops (§31.4).
- **Authority predicates**: policy DSL (Rego-like) compiled to native
  decision trees at policy load.
- **Wire validators**: schema-driven (§3.3); validators codegen'd at
  schema registration.

These cover the lion's share of "deploy without recompiling," compile
to native Go, and pay zero hot-path overhead.

**Tier 2 — Native Go SMs with reproducible build provenance (4% of
cases, native speed):**

For trusted operator-deployed extensions beyond what DSLs cover:

- Native Go SMs registered via the existing interface, deployed via
  §24.6 reproducible-build provenance.
- SM versioning (§24.2) pins entries to specific SM versions.
- Determinism enforced statically (lints) and dynamically (§24.1
  replay harness); no runtime sandbox required.
- Trust model: operator-signed, hash-pinned, reproducible-built.
  Cluster admission refuses unapproved hashes (§24.6).

Same time-travel reproducibility WASM was claimed to deliver, with
native performance and native debugging.

**Tier 3 — Optional WASM as a narrow escape hatch (1% of cases):**

Genuinely irreducible use cases:

- Truly untrusted multi-tenant runtime code (Cloudflare-Workers-style
  scenarios that Sylk doesn't actually have in scope today).
- Cross-language extension authors who can't or won't build via the
  Sylk Go toolchain.

WASM is offered as an optional, feature-flagged subsystem
(`extensions=wasm`) — never on the critical path of core SM apply.
Hot paths (claim apply, KV put, page apply, CDC emit) stay native.
Memory-bounds checks, WASM linear memory copies, and JIT/AOT
overhead are paid only by callers who explicitly opt in.

**What this preserves of the original "WASM SM" goal:**

- Behavior is content-addressed (via §24.6 binary hash matching).
- Time-travel reproducibility (replay through the pinned SM version).
- Hot-reloadable SMs (operator-signed binary updates, §25.7).
- Capability-scoped APIs (§22.5 macaroons).
- Determinism guarantees (§24.1 + §24.2 + §24.5).

**What it loses:**

- Runtime sandboxing of arbitrary user code on the apply hot path.
  This is a real loss only when running untrusted code as a substrate
  primitive — not Sylk's threat model. When it becomes scope, Tier 3
  is there.

### 31.2 Verifiable computation for audit-critical operations

For operations that must be auditable beyond "this Merkle root was
signed by leader," support **succinct non-interactive arguments of
knowledge** (SNARKs):

- A subject can declare `audit=zk-proof`.
- Each entry includes a proof that "applying this entry to the state at
  HLC H_prev produces the state with root R_new."
- Audit verification is a single proof check independent of state size.
- Powers tenant-facing audit where the operator is itself untrusted ("I
  can prove your data was processed correctly without revealing the
  data").

Selective application — full zk-Raft is research-grade, but per-subject
opt-in for sensitive workloads is feasible today (gnark, halo2, plonky3).

### 31.3 Causal isolation levels (CIL)

Beyond linearizable / serializable, subjects declare an isolation level:

| Level                  | Guarantee |
|------------------------|-----------|
| `strict-serializable`  | Default for Critical-class subjects |
| `linearizable`         | Reads see all writes ordered before them globally |
| `monotonic-read`       | Reads never go backward per consumer |
| `causal`               | Happens-before respected; concurrent writes may be observed in different orders |
| `eventual-merge`       | CRDT subjects; convergent, no ordering guarantee |
| `read-your-writes`     | A publisher always sees its own writes |

Substrate enforces. Lets applications pick the cheapest correct level.
Powers cross-DC active-active for subjects that don't need
linearizability (most of them).

### 31.4 Differential dataflow for projections

Lenses today re-derive on each delivery. Differential dataflow makes
projections *deltas all the way down*: updates propagate as differences
across an arbitrarily complex projection graph.

- Subject deltas → projection deltas → view subject deltas.
- Incremental view maintenance: complex queries (joins, aggregations,
  recursive walks) updated in O(input delta), not O(state size).
- Cross-subject queries (joins of `claims × forest-events × fabric-
  activity`) are first-class.

Powers analytical workloads (dashboards, knowledge-graph reasoning) on
the same substrate as transactional workloads.

### 31.5 Self-issuing decentralized identity (DIDs)

Beyond cluster-CA SVIDs, support W3C Decentralized Identifiers for
federation:

- SVIDs as DIDs anchored in substrate trust roots.
- Cross-organizational trust without shared CA.
- Identity proofs verifiable across federations without round-trip to
  issuer.
- Compatible with existing SVID flow (`did:web:`, `did:key:`,
  `did:spiffe:`).

Necessary for "agent A in org X collaborates with agent B in org Y"
without forcing a global PKI.

### 31.6 Substrate-as-database (subject-oriented SQL)

The "fabric is a projection over subjects" idea, generalized: any SQL-
shape query is a projection over a set of subjects.

```sql
SELECT claim_id, COUNT(*) AS rejection_count
FROM sylk://session/{id}/claims/v3
WHERE outcome = 'rejected'
  AND hlc > '2026-04-25T00:00:00Z'
GROUP BY claim_id
HAVING rejection_count > 1
```

- Query planner over subject schemas.
- Incrementally maintained via differential dataflow (§31.4).
- Linearizability per-subject; cross-subject reads at HLC frontier.
- Time-travel: `AS OF HLC '...'` clause.

Substrate becomes a unified transactional + analytical store; no separate
OLAP pipeline.

### 31.7 Continuous-replication training data

LLM agent outputs, evaluation rewards, RLHF feedback are all substrate
subjects. Time-travel becomes "training data audit." Causal cone becomes
"what input caused this output."

A *training feed* is a learner-replica subscription to designated
subjects across the federation. Powers:

- Per-tenant model fine-tuning on tenant data (with tenant key
  attestation that data didn't leave the cluster).
- Per-org private training across federation peers (via federated
  learning protocol).
- Provenance-tracked datasets: every training datapoint references its
  substrate origin entry.

Compliance, reproducibility, and auditability in ML come for free.

### 31.8 Causal-precision schedulers

Most schedulers operate on wall clock + dependencies. Substrate's HLC +
causal DAG offers a richer model: schedule a job at "HLC frontier ≥ F
+ dependencies satisfied + capability available."

- Cron-on-HLC: jobs that fire when a subject reaches a frontier, not
  when wall clock advances.
- Causal idempotence: a scheduled job's effect is keyed by HLC frontier;
  replays at the same frontier are no-ops.
- Cross-region scheduling: jobs can name "the first replica to observe
  frontier F" rather than a specific node.

Powers maintenance jobs that are reproducible, idempotent, and naturally
distributed.

### 31.9 Memory-of-trust: provenance-by-construction

Every byte that leaves the substrate carries a provenance certificate:

- Origin entry's BLAKE3 + signing SVID + HLC + Merkle path to a signed
  root.
- Receiving systems (fabric, knowledge graph, agent context) preserve
  provenance through transformations.
- Surface-level tools (TUI, audit reports) display provenance UI on
  hover.

A claim, an agent message, a fabric envelope — every byte traces back
to a signed source. The substrate isn't just durable; it's *attributable*.

### 31.10 Energy-aware operation

For sustainability and cost: substrate exposes per-operation energy
estimates (joules per fsync, per replication round-trip, per cross-DC
publish). Operator policies can:

- Defer non-urgent work to off-peak / renewable-heavy hours.
- Bias replica placement to low-carbon DCs.
- Surface per-tenant energy consumption (chargeback by carbon, not just
  $$).

Hooks into grid-aware infrastructure (Google's load-shifting, AWS
sustainability metrics).

### 31.11 Self-healing with closed-loop autonomy

Substrate observes its own health (§28.3) and proposes remediations
(subject placement changes, quota adjustments, replica replacements).
Operators approve via signed authority.

Closed loop:

1. Substrate detects hot spot (e.g., one node serving 80% of writes for
   a namespace).
2. Substrate computes alternative placement (move N namespaces to peer
   nodes).
3. Substrate publishes a *proposal* to
   `sylk://global/auto-remediation/v1`.
4. Operator (human or policy bot) reviews and approves; signed approval
   triggers reconciler.
5. Reconciler executes via existing operator-group APIs.

Closed loop with human checkpoint; never autonomously destructive
without approval.

### 31.12 Simulation harness as a first-class subsystem

Beyond chaos tests, a deterministic simulator that replays *every layer*
in single-process simulation:

- Network: lossy, delayed, partitioned, reordered.
- Disk: flaky fsync, slow IO, full disk.
- Clock: skewed, frozen, jumping.
- CPU: pauses, scheduling delays.

Run the entire substrate stack in simulated time, deterministically.
Reproduce any production bug as a simulation seed. Exhaustive search
over partial-order schedules for safety-property verification.

Inspired by FoundationDB's simulation framework. The substrate becomes
verifiable, not just tested.

### 31.13 Post-quantum cryptography

Long-lived audit chains and federation trust roots will outlive RSA /
elliptic-curve security:

- Dilithium signatures alongside Ed25519 (dual-sign during transition).
- Kyber KEM for QUIC handshake (hybrid: X25519 + Kyber).
- SPHINCS+ for high-assurance signatures (slower but minimal assumptions).
- Substrate operations remain unchanged; algorithm choice is per-cluster
  policy.

Without PQC migration path, today's signed Merkle roots become
forgeable in 10-20 years; audit value erodes. Build the migration in.

### 31.14 Operationally-verifiable formal methods

TLA+ specs for the consensus and cursor invariants are not enough — they
verify the model, not the running code. Bind specs to runtime:

- TLA+ specs compiled to runtime invariant checks.
- Substrate continuously verifies its own safety properties during
  operation (sample of committed entries, sample of cursor advances).
- Property violation: emit alert; halt the affected group; gather
  evidence.

The formal model becomes a living constraint, not a one-time proof.

### 31.15 Cross-substrate interop adapters

Drop-in compatibility shims so existing systems migrate without code
changes:

- NATS protocol front-end: speak NATS wire; translate to substrate
  subjects under the hood.
- Kafka protocol front-end: same idea, Kafka topics map to substrate
  subjects with partition_key.
- Postgres logical-replication front-end: substrate as a logical-
  replication target for change-data-capture.

The substrate replaces NATS / Kafka / CDC pipelines without forcing a
client rewrite. Adoption goes from "rewrite your stack" to "swap your
broker."

### 31.16 Public blockchain anchoring (timestamping)

Periodically anchor cluster Merkle root to a public blockchain (Bitcoin,
Ethereum, or a permissioned chain). Side effect:

- Audit trail public-verifiable: anyone with the block height can verify
  "this Merkle root existed at this point in time, and so did everything
  it commits to."
- Immune to operator collusion: even if cluster operator + tenant
  collude to rewrite history, public chain disagrees.
- Cost trivial (one transaction per hour or per day, not per entry).

Useful for high-assurance audit (regulated industries, contractual SLA
proof, cryptographic timestamping).

### 31.17 Built-in wire transform registry

Mid-stream transforms declared at subject schema level. To avoid
runtime sandboxing overhead on every frame, transforms are drawn from
a *registry of vetted, native-Go implementations* — operators
*select and configure*, not author.

Registry covers the operationally-relevant set:

- **Compression / decompression**: zstd (with §31.13 pre-trained
  per-schema dictionaries), lz4, snappy.
- **Encryption / decryption**: AES-GCM-256, ChaCha20-Poly1305, with
  per-tenant DEK envelope (§21.2).
- **Redaction**: field-level (declared in schema), TTL-driven (auto-
  erase past retention via §31.23 verifiable redaction).
- **Schema migration**: v1 → v2 transforms via declarative mapping
  rules, codegen'd to native Go at registration.
- **Sanitization**: PII masking, value-pinning, format normalization
  for cross-tenant subjects.

Each registry entry is:
- Native Go code in the sylk binary (zero runtime overhead).
- Versioned (§24.2 SM versioning rules apply).
- Verified by §24.1 determinism harness.
- Auditable: the transform's identity (registry entry + parameters)
  is logged per use.

A transform pipeline is a sequence of registry entries with
parameters: `[zstd(dict=schema_v3), aes-gcm(kek=tenant_kek)]`. Pipelines
are subjects themselves — observable, revisable, time-travelable.

User-authored transforms beyond the registry fall under §31.1 Tier 3
(optional WASM); not the default path.

### 31.18 Substrate-managed agent runtime

Agents themselves become substrate primitives:

- Agent definitions stored as substrate objects (native Go binary +
  manifest + capability bindings + reproducible-build provenance per
  §24.6).
- Agent lifecycle (spawn, schedule, retire) is operator-group managed
  via `sylk://global/agents/v1` state machine.
- Agent execution is constrained by capability bindings (§22.5
  macaroons + SVID); agent can only publish to subjects its SVID is
  authorized for.
- Agent restart is "replay from last applied HLC frontier" via the
  existing pull-first cursor mechanism.

Trust model: agents are *Sylk-first-party code*, authored in Go,
hash-pinned via §24.6, deployed by operator authority. No runtime
sandbox is needed because agent code is trusted equivalently to the
substrate binary itself. Capability scoping (§22.5) is enforced at
publish time via authority predicates — not via sandboxing the
process.

The Sylk runtime collapses into "substrate + native-Go SMs + agents-
as-substrate-objects with native execution." One mental model, no
WASM tax on the agent hot path.

### 31.19 Geo-fenced CRDTs for residency-compliant collaboration

GDPR / HIPAA / data-residency: personal data stays in-region, but
metadata can converge globally.

- CRDT subject schema declares per-field residency: `(field, allowed_
  regions)`.
- Substrate enforces: replicas in disallowed regions hold tombstones /
  hashes only, not values.
- Cross-region merges respect field-level residency: fields that can't
  leave their region merge only intra-region; metadata fields merge
  globally.

Powers global collaboration on regulated workloads without legal risk
or hand-rolled per-feature residency code.

### 31.20 Self-replicating cluster (autonomous fleet management)

For very large fleets: substrate manages its own cluster topology
declaratively.

- Cluster spec includes desired capacity (min/max nodes per DC, target
  utilization).
- Substrate observes load over time (§28.3 anomaly feed).
- When sustained over-utilization detected, substrate proposes capacity
  expansion.
- Operator approves; substrate provisions new node via cloud API
  (stateless cloud-init); attests; joins cluster as voter or learner.
- Symmetric for decommission.

Closed-loop fleet management with human-approved control. Ops effort
scales sub-linearly with fleet size.

---

### 31.21 Causality-as-type-system (session-typed subjects)

§31.3 makes isolation a per-subject choice. Push further: make the
*interaction protocol* itself a type. Each subject's schema declares a
session type (à la pi-calculus, Honda et al.) describing the legal
sequences of `(role, message)` pairs:

```
session ClaimsBoard =
    role architect: claim.issued
  → role engineer: (claim.accepted | claim.rejected)
  → if accepted: role engineer*: testament.submitted
              → role architect: artifact.published
  → if rejected: role architect: claim.remediated
              → loop ClaimsBoard
```

The substrate type-checks publish/subscribe interactions at the wire:

- A frame violating the session type is rejected at the substrate
  boundary, not by the consumer.
- Static guarantee: consumers cannot observe orderings publishers cannot
  produce.
- Refinement types: `claim.rejected` carries `reason: NonEmpty<String>`,
  enforced structurally.

The substrate becomes a *typed protocol enforcer*, not just a typed
data store. Bug categories like "agent published a testament before
claim was accepted" become compile-time impossible — they are wire-
level rejected.

### 31.22 Proof-carrying state machines

§24 enforces SM determinism + dual-version verification. Push further:
each SM ships with a machine-checked proof (Coq, Lean, Dafny, F*) that
it preserves declared invariants. Operator deployment of a new SM
version *requires the proof to validate* against the declared invariants.

```
SM v17 of claims-board declares invariants:
  I1: ∀ claim. claim.accepted ⇒ claim.testament.submitted_count > 0
  I2: ∀ claim. claim.outcome ∈ {accepted, rejected, remediated}
  I3: monotonicity of HLC frontier within partition_key
proof-of-correctness.lean → invariants hold under all reachable states
deployed-hash → cluster admits SM v17
```

A malformed SM that violates invariants cannot be deployed. Bugs of the
form "this code path produces inconsistent state" are caught
mathematically before reaching production. Non-trivial — but for the
state machines that matter (claims, VFS commits, authority), the
invariants are small enough that proof effort is bounded.

### 31.23 Verifiable history redaction (GDPR-compliant immutable logs)

Append-only Merkle DAGs and "right-to-erasure" appear contradictory. They
aren't. Use *redaction-aware Merkle trees* (KEMs over Merkle leaves):

- Each leaf is encrypted with a per-leaf key; key escrowed to substrate.
- Erasure of leaf L: destroy L's key + replace plaintext with hash
  commitment.
- Merkle proof structure unchanged; auditors verify "the tree's shape
  and inclusion is identical to what it was, but leaf L's content is
  now hash-only."
- Original cryptographic chain *still verifies* — only the redacted
  content is gone.

The substrate offers:
- Immutable audit guarantees: prior commitments still verify.
- GDPR / CCPA compliance: personal data destroyable on demand.
- Forensic transparency: redaction itself is logged (when, who, why).

Resolves a 10-year-old tension between immutable logs and right-to-
erasure regulations.

### 31.24 Cross-domain causal algebra

HLCs work within one substrate. Across federated substrates with
different time bases, causal ordering is hand-rolled today. The
substrate exposes a formal *cross-domain HLC algebra*:

- Per-federation `(domain_id, hlc, uncertainty)` tuples.
- Cross-domain happens-before is a partial order; the algebra computes
  upper/lower bounds and uncertainty propagation.
- A federated read with linearizability requirement waits out the
  *combined* uncertainty across involved domains.
- Soundness theorem (machine-checked alongside §31.22): "no observed
  ordering violates happens-before across domains."

Lets federations reason about causality without designating one cluster
as time master, and without resorting to vector clocks of unbounded
size.

**Concrete protocol — write tokens + observed-after reads**:

Cross-cluster read-after-write semantics are realized via *write
tokens* and bounded-wait *observed-after* reads.

**WriteToken**: every cross-cluster write returns a token:

```go
type WriteToken struct {
    DomainID    uint64       // origin cluster's domain ID
    HLC         HLC          // commit HLC at origin
    Uncertainty Duration     // origin's clock uncertainty at commit (§23.1)
    ParentChain []TokenLink  // dependency chain from prior reads/writes
}

type TokenLink struct {
    DomainID uint64
    HLC      HLC
}
```

The token is opaque to the application; passed back to the substrate
on subsequent reads to enforce causality.

**ObservedAfter read API**:

```go
read(subject, ObservedAfter(token))
```

Substrate computes the minimum HLC frontier needed across all domains
in `token.ParentChain` and the `token.DomainID` for the read to be
guaranteed to include the write's effects:

```
target_per_domain[d] = max over chain entries with DomainID==d of
                        (entry.HLC + token.Uncertainty)
```

Substrate blocks until the local cluster's `FederationFrontier[d]`
covers `target_per_domain[d]` for every `d`, OR a configurable
timeout fires (returning `ErrFrontierNotReached`).

**Frontier propagation protocol**: federation gateways exchange
per-domain commit-HLC via a dedicated BFT subject
`sylk://federation/<id>/frontier/v1`. Updates published every 100ms
(configurable). Each cluster maintains a `FederationFrontier` view:

```go
type FederationFrontier map[domain_id]struct {
    HLC                 HLC       // last advertised commit HLC
    Uncertainty         Duration  // last advertised uncertainty
    LastAdvertReceived  HLC       // when this advert was received locally
}
```

Stale entries (advert age > threshold) treated as unknown.

**Wait protocol**:

```
ObservedAfter(token):
    deadline = now() + timeout
    for each link in token.ParentChain ∪ {(token.DomainID, token.HLC)}:
        target = link.HLC + token.Uncertainty
        for {
            current = local.FederationFrontier[link.DomainID]
            if current.HLC ≥ target:
                break  // this domain is satisfied
            if now() > deadline:
                return ErrFrontierNotReached
            wait_for_frontier_advance(link.DomainID)
        }
    return read at local snapshot HLC
```

**Causal token chains**: when a read returns value `V` derived from
token `T`, the response carries a *new* token
`T' = T ⊕ {local_domain, hlc_at_read}`. Subsequent reads using `T'`
enforce the full causal chain.

**Token compression**: chain length bounded; oldest entries collapse
into "horizon" entries (per §21.5). Local reads against horizoned
tokens skip waits for fully-saturated domains.

**Soundness theorem** (machine-checked):

> For any read R returning value V with token T, V causally-includes
> every write W such that `W.token ∈ T.parent_chain`. Equivalently:
> ¬∃ R, W : `(W.token ∈ R.token.parent_chain)` ∧ `(R.value omits
> W.effect)`.

**Operational guarantee**: under bounded clock skew (§23.1) and
bounded federation RTT, observed-after reads complete within
`max_chain_uncertainty + max_federation_propagation_delay`. Typical
budget: 100-500ms for chain depth ≤ 5; longer chains require either
federation RTT improvement or token compression.

**Telemetry**:
- `xdomain_observed_after_wait_seconds{domain_chain_depth}` —
  distribution.
- `xdomain_frontier_lag_seconds{domain}` — per-domain frontier age.
- `xdomain_observed_after_timeouts_total` — bounded-wait timeouts.

### 31.25 Substrate-internal optimization passes

The substrate observes its own behavior (subject access patterns,
projection costs, hot spots) and applies optimization passes like a
compiler:

- **Hot view denormalization**: high-fan-out projections automatically
  promoted from "compute on read" to "materialize on write."
- **Replication topology rewrite**: read-heavy subjects re-shaped from
  3-voter Raft to 1-voter + 4-learner pipelined replication.
- **Subject auto-sharding**: hot subjects automatically sub-sharded
  (§27.1) when partition_key skew exceeds threshold.
- **Cross-subject join precomputation**: frequent multi-subject queries
  precomputed and stored as derived view subjects.

Each optimization is itself a substrate operation (operator-group write)
with full audit trail. The substrate reasons about its own performance
the way a compiler reasons about generated code — with profile-guided
feedback.

### 31.26 Speculative consensus with verifiable rollback

For latency-critical paths, optimistically commit assuming no
Byzantine faults; verify post-hoc; rollback if wrong.

- HotStuff-style 1-RTT commit on the optimistic path.
- 2-RTT verification path runs in parallel.
- If verification disagrees: rollback (substrate has full audit trail of
  speculatively-committed entries).
- Guarantee: no externally-visible state advances past the verified
  commit point; rollback is invisible to non-speculative readers.
- Speculative readers (those willing to accept potential rollback)
  observe lower latency.

A latency / safety knob exposed at the read API. Critical-class
operations get strict; latency-critical reads can opt in to speculative.

### 31.27 Topology-aware routing beyond Vivaldi

Vivaldi gives latency distance. Production network topology is richer:
BGP peerings, ASN boundaries, peering economics, jurisdictional
boundaries.

- Substrate consumes BGP feeds (RouteViews, RIPE) at edge tier.
- Routing decisions consider: ASN distance, transit cost, peering
  agreements, jurisdictional residency.
- Cross-DC replication routes via known peering links rather than transit.
- Failover routes pre-computed for common BGP withdrawal patterns.

Bandwidth cost reduction at scale (transit-vs-peering can be 10x).
Resilience against ISP-level outages (Cloudflare 2022, Facebook 2021
patterns).

### 31.28 Multi-level cache coherence as a primitive

Edge cache + DC cache + replica cache + consumer cache. Currently each
hand-rolls invalidation. The substrate makes coherence a primitive:

- Each cache level subscribes to invalidation events keyed by
  `(subject, partition_key, hlc_frontier)`.
- Coherence protocol modeled formally (akin to MESI cache coherence at
  the distributed scale).
- Stale read → cache fetches latest from upstream; only published if
  upstream's HLC > stale.
- Operations: `coherent_read(consistency_level, max_staleness_ms)`.

Powers "stale-bounded reads at edge" without each consumer reinventing
TTL + invalidation.

### 31.29 Substrate as compiler IR

The substrate's typed subjects + session types + projection DSL +
dataflow operators is, structurally, a programming language. Make it
explicit:

- Sylk fabric / agents / knowledge graph compile to a substrate
  *execution plan*.
- The compiler emits subject definitions, projection programs, session
  types, durability profiles.
- The substrate is the runtime; the user-facing surface (Sylk skills,
  agent composition) is the source language.
- Optimization passes (§31.25) operate on the IR.

This reframes Sylk: not "an application using a substrate" but "a
domain-specific language compiled to a substrate runtime." Other
applications (workflow engines, data pipelines) can target the same
substrate with their own front-ends.

### 31.30 Honest assessment: what we still don't know how to do

Even with §31.1-§31.29, several frontiers remain genuinely open. They
are listed not as items to implement but as boundaries acknowledged:

- **Fully homomorphic compute on substrate state**: aggregations over
  encrypted tenant data without operator decryption. Possible for
  specific operators (sums, counts) via partially homomorphic schemes;
  general FHE remains too slow.
- **Quantum-secure causal anchoring with preserved interactivity**:
  PQC signatures (§31.13) cover authentication; lattice-based
  timestamping that works under quantum adversary while preserving
  HLC-style interactivity is unsolved.
- **Causal compression with provable bounds**: identifying when two
  causal subtrees are observationally equivalent and collapsing them
  is undecidable in general; bounded heuristics exist but no formal
  storage-vs-fidelity trade-off curve.
- **Adversarial reasoning across federations of arbitrary depth**:
  trust composition in deep federation hierarchies (federation of
  federations) lacks a clean algebraic framework.
- **Cross-substrate live migration with zero downtime**: moving a live
  cluster between heterogeneous substrate implementations (different
  consensus algorithms, different storage engines) while preserving
  cursors and happens-before. Possible in principle with §17.1
  accountability but engineering effort substantial.
- **Provable lower bound on cluster operational complexity**: no formal
  framework yet for proving "this cluster cannot be operated by fewer
  than N humans of skill level S sustainably." Empirical only.

Acknowledging these is not defeatism; it's accurate scoping. The
substrate as designed solves what is solvable now. The frontier moves
forward with the field.

---

## 32. Mode Migration

Users can move between modes without re-learning anything because the
abstractions don't change.

### 19.1 Embedded → Local Daemon

```
1. Stop sylk.
2. Install sylkd; configure to use ~/.sylk/data (existing data path).
3. Start sylkd (systemd / launchd unit).
4. Start sylk; configure to connect via Unix socket.
5. State preserved; cursor preserved.
```

### 19.2 Local Daemon → Remote

```
1. Set up cluster (sylkd nodes in target DCs).
2. Migrate session state:
   - sylk export-session <session-id> > session-bundle.tar
     (exports sealed segments + manifest + cursor as a Merkle bundle)
   - Upload bundle to cluster's session-import endpoint
   - Cluster ingests as a new namespace with Merkle verification
3. Stop local sylkd.
4. Start sylk in remote mode; connect to cluster.
5. Cursor still valid; nothing lost.
```

### 19.3 Embedded → Remote (skip daemon)

Same as 19.2 but starting from `~/.sylk/data` directly.

---

## 33. Implementation Plan

The implementation is broken into 16 phases, executed in order. Each phase
contains items; each item has explicit acceptance criteria and a complete
test ladder (unit, integration, end-to-end). Phases are independently
shippable — landing phase N never destabilizes phase N-1's behavior.

**Test convention** throughout this section:

- **Unit tests** verify a single function or struct in isolation. Run in the
  package's `_test.go` files. Must run in <100ms each.
- **Integration tests** verify multiple components composing correctly.
  Run in `core/substrate/integration_test/`. May spin up real disk, real
  goroutines, real Raft groups (in-process). Run in <10s each.
- **End-to-end tests** verify cross-process or cross-node behavior. Run in
  `core/substrate/e2e_test/`. May spin up multiple `sylkd` processes,
  inject network faults, kill processes. Run in <60s each.
- **Chaos tests** are a special class of e2e tests that introduce random
  faults under load and verify invariants. Run separately via
  `make chaos`; not part of normal CI.

Every item below also includes invariants the implementation must preserve
under all conditions (crash, partition, restart, concurrent writes). These
are testable via property-based tests (`testing/quick`).

---

### Phase 0 — Foundation and Tooling

Cross-cutting infrastructure used by all later phases.

#### 0.1 — Repository layout

**Description**: Establish `core/substrate/` package layout with subpackages
for each layer. Update build system, lints, and test scaffolding.

**Layout**:
```
core/substrate/
  identity/        Layer 0 (SVID, HLC)
  subject/         Layer 0 (subject URI, registry types)
  wire/            Layer 1 (frame format, codec)
  transport/       Layer 1 (QUIC, channel, AF_UNIX)
  membership/      Layer 2 (SWIM++)
  consensus/       Layer 3 (multi-Raft)
  storage/         Layer 4 (causal Merkle DAG)
  delivery/        Layer 5 (cursor, ack)
  dedupe/          Layer 6
  reliability/     Layer 7
  primitives/      Layer 8 (KV, object, claims, forest, fabric, vfs)
  observability/   Layer 9
  internal/
    blake3/        crypto wrapper
    bloom/         bloom filter
    merkle/        merkle tree primitives
    cbor/          CBOR codec generator
    fsync/         group-commit fsync
  testutil/        shared test fixtures
  integration_test/
  e2e_test/
```

**Acceptance criteria**:
- All listed directories exist and compile.
- `go vet ./core/substrate/...` clean.
- `golangci-lint run ./core/substrate/...` clean (existing config).
- Cyclomatic complexity ≤ 4 per function (existing CLAUDE.md rule).
- Build with Go 1.25+; CI verifies.

**Unit tests**: N/A (structural).

**Integration tests**: N/A.

**End-to-end tests**: `make build` succeeds end-to-end with new packages.

---

#### 0.2 — BLAKE3 wrapper

**Description**: Wrap `github.com/zeebo/blake3` (or equivalent) in
`internal/blake3` with Sylk-specific helpers: `Hash256`, `HashStream`,
`KeyedHash`, `Verify`.

**Acceptance criteria**:
- `Hash256(data) → [32]byte` for fixed-size return (no allocation in hot
  path beyond input copy).
- `HashStream(io.Reader) → [32]byte` for large bodies.
- `KeyedHash(key, data) → [32]byte` for HMAC-style use (authority tokens
  use Ed25519, not keyed BLAKE3, but other code paths need keyed hashing).
- `Verify(data, expected [32]byte) → bool` constant-time compare.
- All functions allocation-free for inputs ≤4KB.

**Unit tests**:
- `TestBlake3Hash256_KAT` — Known-answer-test against RFC test vectors.
- `TestBlake3Hash256_NoAlloc` — `testing.AllocsPerRun` reports 0 for ≤4KB
  inputs.
- `TestBlake3HashStream_LargeInput` — 1GB stream produces same hash as
  `Hash256` over the same content (constructed via batches).
- `TestBlake3Verify_ConstantTime` — Timing-stability test (within
  configured variance) across matching and mismatched inputs.

**Integration tests**: N/A (utility).

**End-to-end tests**: N/A.

---

#### 0.3 — Ed25519 signing

**Description**: Wrap `crypto/ed25519` with substrate conventions: SVID
keypair generation, signing of frame header+body, verification with caching
of parsed public keys.

**Acceptance criteria**:
- `Sign(privKey, data) → [64]byte` allocation-free.
- `Verify(pubKey, data, sig) → bool` allocation-free.
- Public-key parse cache: parsing an SVID's public key happens once per
  SVID, then is reused. Cache is bounded (default 10K entries, LRU).
- Cache thread-safe under concurrent verify calls.

**Unit tests**:
- `TestEd25519Sign_KAT` — Known-answer-test against RFC 8032.
- `TestEd25519Verify_Tamper` — Modified data, modified sig, modified key
  all fail verification.
- `TestEd25519PubKeyCache_Bounded` — Inserting >10K entries evicts LRU.
- `TestEd25519PubKeyCache_Concurrent` — `go test -race` clean under 1000
  concurrent verifies.

**Integration tests**: N/A.

**End-to-end tests**: N/A.

---

#### 0.4 — SPIFFE SVID parsing

**Description**: Parse X.509 SVIDs with the SPIFFE URI extension. Extract
identity URI and authority bindings from X.509 extensions.

**Acceptance criteria**:
- `ParseSVID(certDER) → SVID` succeeds for well-formed SVIDs.
- `ParseSVID` rejects: expired certs, unknown CAs, missing SPIFFE URI,
  malformed authority extension.
- `SVID.Identity() → URI` returns the canonical SPIFFE URI.
- `SVID.AuthorityBindings() → []Capability` returns parsed capabilities.
- Returns wrapped error with the rejection reason; never panics.

**Unit tests**:
- `TestParseSVID_Valid` — Round-trip a generated SVID; identity matches.
- `TestParseSVID_Expired` — Expired cert rejected with `ErrSVIDExpired`.
- `TestParseSVID_UnknownCA` — Cert signed by unknown CA rejected.
- `TestParseSVID_MissingURI` — Cert without SPIFFE URI rejected.
- `TestParseSVID_MalformedAuthorityExt` — Malformed extension rejected.
- `TestSVID_AuthorityBindings_Parse` — Full set of capability strings
  parses correctly.

**Integration tests**:
- `TestSVID_RoundTripSignVerify` — Generated SVID; sign frame; verify
  with parsed SVID; succeeds.

**End-to-end tests**: deferred to phase 5.

---

#### 0.5 — HLC implementation

**Description**: Implement `HLC{PhysicalNs, Logical, NodeID}` with the
update rule from §3.2.

**Acceptance criteria**:
- `New(nodeID) → *HLC` instantiates with current wall clock, logical=0.
- `Now()` returns the current HLC, advancing the logical counter.
- `Update(received HLC)` advances per the rule in §3.2.
- Total order: `HLC.Compare(a, b) < 0` iff `a` lexicographically precedes
  `b` on `(PhysicalNs, Logical, NodeID)`.
- Monotonic across wall-clock backsteps (verified by injecting a backwards
  clock in tests).
- Drift bound: `HLC.PhysicalNs - wall_clock_ns()` ≤ MaxDrift (default
  500ms); `Update` rejects HLCs beyond the bound with `ErrHLCSkew`.
- Concurrent-safe: `Now`, `Update` thread-safe under any number of
  concurrent calls.

**Unit tests**:
- `TestHLC_NowAdvances` — Successive `Now()` calls produce strictly
  increasing HLCs.
- `TestHLC_UpdateMonotonic` — `Update` with received HLC < local does not
  decrease local HLC.
- `TestHLC_UpdateAdvancesOnReceived` — `Update` with received > local
  advances local to received+1.
- `TestHLC_BackwardsClock` — Injected wall-clock backsteps don't decrease
  HLC.
- `TestHLC_Compare` — All ordering pairs.
- `TestHLC_DriftBound` — HLC > wall_clock + MaxDrift returns
  `ErrHLCSkew` from `Update`.
- `TestHLC_Concurrent` — `go test -race`; 100 goroutines, 10K Now+Update
  ops each; final HLC > start HLC by at least op count.
- `TestHLC_HappensBefore` (property test) — For any sequence of local
  events and received messages, the resulting HLCs respect the
  happens-before partial order.

**Integration tests**:
- `TestHLC_TwoNodeExchange` — Two HLC instances exchange messages; HLCs
  remain consistent and observe each other's events.

**End-to-end tests**: deferred to phase 4.

---

#### 0.6 — Substrate test harness

**Description**: Shared fixtures in `testutil/`: in-memory transport,
in-memory storage, faked SVIDs, deterministic HLC, Raft group factory.

**Acceptance criteria**:
- `NewInMemoryTransport()` returns a transport that echoes between
  registered endpoints; supports configurable latency, packet loss, drop
  patterns.
- `NewFakeSVID(identity, capabilities)` returns a usable SVID without a
  real CA.
- `NewDeterministicHLC(seed)` returns an HLC with a controllable physical
  clock.
- `NewRaftGroup(t, replicas)` spins up an in-process Raft group with N
  replicas and returns handles.
- `NewSubstrate(t, opts)` boots a single-node embedded substrate for tests.
- All fixtures `t.Cleanup`-safe.

**Unit tests**: N/A (test utility).

**Integration tests**:
- `TestHarness_InMemoryTransportLossy` — Transport configured with 10%
  loss; `n` sends produce ~`0.9n` deliveries.
- `TestHarness_DeterministicHLC` — Seeded HLC produces reproducible
  sequences across two test runs.

**End-to-end tests**: N/A.

---

### Phase 1 — Wire Format and Local Identity

Frame format, codec, validation pipeline. Single-process for now.

#### 1.1 — Frame header struct

**Description**: Define `wire.Header` matching §4.1 byte layout. Define
constants for `MsgType`, `Flags`. Provide `EncodeHeader(buf, hdr)` and
`DecodeHeader(buf) (hdr, err)`.

**Acceptance criteria**:
- Header is exactly 56 bytes; verified by `unsafe.Sizeof` test.
- `EncodeHeader` writes 56 bytes; allocation-free.
- `DecodeHeader` reads 56 bytes; allocation-free.
- Round-trip equality: `DecodeHeader(EncodeHeader(h)) == h`.
- All fields little-endian (chosen for arch-native; documented).
- Reserved fields zeroed; validators reject non-zero reserved.

**Unit tests**:
- `TestWireHeader_Size` — `unsafe.Sizeof(Header{}) == 56`.
- `TestWireHeader_RoundTrip` — Encode then decode produces equal struct.
- `TestWireHeader_NoAlloc` — `testing.AllocsPerRun` reports 0 for both
  encode and decode.
- `TestWireHeader_Truncated` — `DecodeHeader` on <56 bytes returns
  `ErrTruncated`.
- `TestWireHeader_BadVersion` — Version != 1 returns `ErrUnsupportedVersion`.
- `TestWireHeader_FieldEndianness` — Fields decoded match expected
  little-endian values.

**Integration tests**: N/A.

**End-to-end tests**: deferred.

---

#### 1.2 — Frame body codec (CBOR)

**Description**: Wrap `github.com/fxamacker/cbor/v2` for body encoding and
decoding with strict mode (no map ordering ambiguity, no infinite-length
items).

**Acceptance criteria**:
- `EncodeBody(v) → []byte` produces canonical CBOR (deterministic; same
  input always produces same bytes).
- `DecodeBody(data, &v)` strict-mode decode.
- Canonical encoding requires sorted map keys, smallest int encoding, no
  indefinite-length items.
- Reject non-canonical inputs with `ErrNonCanonical`.

**Unit tests**:
- `TestCBOREncode_Canonical` — Same input produces same bytes across
  encodes.
- `TestCBORDecode_Strict_RejectsNonCanonical` — Map with unsorted keys
  rejected.
- `TestCBOR_RoundTripStruct` — Encode/decode arbitrary structs with
  `testing/quick`.
- `TestCBOR_LargeBody` — 16MB body encodes and decodes.

**Integration tests**: N/A.

**End-to-end tests**: N/A.

---

#### 1.3 — Frame trailer (signature + content hash)

**Description**: Define `wire.Trailer{AuthoritySig [64]byte, BodyHash [32]byte}`.
Provide `SignFrame(privKey, header, body) → trailer` and
`VerifyFrame(pubKey, header, body, trailer) → error`.

**Acceptance criteria**:
- `SignFrame` produces signature over `header || body`.
- `VerifyFrame` validates signature; checks `BodyHash` matches
  `BLAKE3-256(body)`.
- Tampering with header, body, signature, or hash fails verification with
  specific error.
- Allocation-free on both sign and verify hot paths (apart from signing
  algorithm internals).

**Unit tests**:
- `TestFrameSign_Verify` — Sign then verify succeeds.
- `TestFrameVerify_TamperedHeader` — Modified header byte fails.
- `TestFrameVerify_TamperedBody` — Modified body byte fails.
- `TestFrameVerify_TamperedSig` — Modified sig fails.
- `TestFrameVerify_TamperedHash` — Modified hash fails.
- `TestFrameVerify_BodyHashMismatch` — Trailer hash != computed body hash
  fails before signature check (cheap check first).

**Integration tests**: N/A.

**End-to-end tests**: N/A.

---

#### 1.4 — Subject URI and registry types

**Description**: Define `subject.URI{Namespace, Kind, Version, PartitionKey}`,
`subject.ID uint64`, `subject.Schema`, and the in-memory `subject.Registry`
interface with `Register`, `Lookup`, `LookupByID`, `Schemas`. Persistence
deferred to phase 3 (Raft-backed registry).

**Acceptance criteria**:
- `URI.Parse(string) → URI, error` accepts canonical form; rejects
  malformed.
- `URI.String()` round-trips with `Parse`.
- `Registry.Register(uri, schema, authority) → ID` returns deterministic
  ID for the URI (FNV-1a 64 hash, collision-checked against existing
  entries).
- `Registry.Lookup(uri) → (ID, Schema, error)`.
- `Registry.LookupByID(id) → (URI, Schema, error)`.
- Registry thread-safe under concurrent register/lookup.
- Versions are immutable: re-registering same URI returns existing ID.
- Different URIs hashing to same ID (collision) returns
  `ErrSubjectIDCollision` with both URIs.

**Unit tests**:
- `TestSubjectURI_Parse_RoundTrip` — `URI.Parse(URI.String())` equals
  original (property test over generated URIs).
- `TestSubjectURI_RejectMalformed` — Suite of bad URIs all rejected.
- `TestRegistry_RegisterLookup` — Round-trip register and lookup.
- `TestRegistry_DeterministicID` — Same URI produces same ID across
  registry instances.
- `TestRegistry_ConcurrentRegister` — `go test -race` clean.
- `TestRegistry_VersionImmutable` — Re-register returns same ID.
- `TestRegistry_IDCollisionDetection` — Forced collision returns error.

**Integration tests**: N/A.

**End-to-end tests**: deferred to phase 3.

---

#### 1.5 — Schema validation

**Description**: Schema is a CBOR schema document (e.g., a CDDL schema or
custom format). Provide `schema.Validate(data, schema) → error`. Generate
fast decoders at registration time and cache.

**Acceptance criteria**:
- Schema language supports: required/optional fields, primitive types,
  arrays, maps, fixed-size byte arrays, integer ranges, string regexes,
  recursive types.
- `Validate` rejects: missing required, type mismatch, out-of-range int,
  malformed string.
- Generated decoders 5x faster than reflection-based decoders for typical
  schemas (benchmarked).
- Registration triggers code-gen on first use; cached thereafter.
- Allocation-free `Validate` for valid inputs (no error return).

**Unit tests**:
- `TestSchema_Validate_Required` — Missing required field rejected.
- `TestSchema_Validate_TypeMismatch` — Wrong type rejected.
- `TestSchema_Validate_IntRange` — Out-of-range rejected.
- `TestSchema_Validate_StringRegex` — Non-matching string rejected.
- `TestSchema_Validate_Recursive` — Recursive types validate (nested up
  to depth 100).
- `TestSchema_Decoder_Codegen` — Cached decoder used on second call.
- `BenchmarkSchema_Validate_Generated` vs `BenchmarkSchema_Validate_Reflection`
  — generated is ≥5x faster.

**Integration tests**:
- `TestSchema_ManyTypesIntegration` — Realistic Sylk-shape schemas
  (claim, testament, forest event) all validate end-to-end.

**End-to-end tests**: N/A.

---

#### 1.6 — In-process channel transport

**Description**: First transport implementation. `transport.Channel{Send,
Recv}` over Go channels. Used in embedded mode and tests.

**Acceptance criteria**:
- `Send(frame) error` enqueues to receiver's channel; non-blocking up to
  buffer size, blocking with backpressure thereafter.
- `Recv() (frame, error)` dequeues; blocks if empty.
- `Close()` drains in-flight, then returns `ErrClosed` on subsequent ops.
- Backpressure-aware: when receiver's buffer is full, sender blocks (not
  drops) by default. Optional drop mode for best-effort frames.
- No goroutines leaked on `Close`.

**Unit tests**:
- `TestChannelTransport_SendRecv` — Round-trip a frame.
- `TestChannelTransport_Backpressure` — Send to full buffer blocks until
  Recv frees space.
- `TestChannelTransport_Close` — Operations after close return
  `ErrClosed`.
- `TestChannelTransport_NoGoroutineLeak` — `goleak.VerifyNone(t)` after
  close (use uber-go/goleak).
- `TestChannelTransport_Concurrent` — `go test -race` with 1000
  concurrent Send/Recv pairs.

**Integration tests**:
- `TestChannelTransport_FrameValidation` — Invalid frames rejected at
  receiver per §4.1 validation order.

**End-to-end tests**: deferred to phase 4.

---

#### 1.7 — Frame validation pipeline

**Description**: `wire.Validator` runs the 9-step validation from §4.1
(decode header, body length plausible, HLC plausible, subject lookup,
authority verify, schema validate, body hash compute, dedupe lookup,
deliver). Phase 1 implements steps 1-3 and 6-7; steps 4-5 and 8-9 wire in
later.

**Acceptance criteria**:
- Validator returns specific error per failed step.
- Order strictly enforced: bad header is rejected before authority is
  even checked.
- Each step's cost amortized (allocation-free for steps 1-3, cached for
  step 4-5).
- Pluggable: future phases add steps 4 and 8 without changing existing
  step impls.

**Unit tests**:
- `TestValidator_BadHeader_RejectedFirst` — Bad header doesn't reach
  authority check.
- `TestValidator_HLCSkew_RejectedThird` — HLC out of bound rejected
  before subject lookup.
- `TestValidator_BadSchema_RejectedSixth` — Body fails schema validation
  rejected after authority OK.
- `TestValidator_BodyHashMismatch_RejectedSeventh` — Trailer hash mismatch
  rejected after schema OK.
- `TestValidator_AllStepsPass` — Valid frame passes all steps and
  delivers.

**Integration tests**:
- `TestValidator_ChannelTransport_FullPipeline` — Channel transport with
  validator; valid frames flow, invalid drop with errors.

**End-to-end tests**: deferred.

---

### Phase 2 — Local Storage (Causal Merkle DAG)

#### 2.1 — Active segment writer

**Description**: `storage.ActiveSegment` mmap-backed append-only log file.
Writes append; group-commit fsync flushes batched writes.

**Acceptance criteria**:
- `Append(entry) → offset` writes entry bytes to mmap region; returns
  offset.
- `Sync()` issues fsync; returns when durable.
- `Size()` returns current write offset.
- Crash safety: after `Sync`, all entries before sync's offset are
  durable; after process kill, entries after last sync may or may not be
  present, but never partial (length-prefix + CRC catches truncation).
- mmap region grows by configurable chunk (default 64MB) when needed.
- No torn writes: writes ≤4KB are atomic on POSIX-compliant FS; larger
  writes use length-prefix + trailer-CRC to detect truncation.

**Unit tests**:
- `TestActiveSegment_AppendSync` — Round-trip an entry.
- `TestActiveSegment_GrowMmap` — Append past initial size triggers grow;
  writes still succeed.
- `TestActiveSegment_TruncationDetected` — Manually truncate file mid-entry;
  `Open` rejects last partial entry without losing earlier entries.
- `TestActiveSegment_CRC_DetectsCorruption` — Flip a byte mid-entry;
  reader returns `ErrCorruption` on that entry, prior entries intact.
- `TestActiveSegment_ConcurrentAppend_Serializes` — Concurrent Append calls
  serialize correctly (offsets monotonic).
- `TestActiveSegment_NoLeaks` — `Close` closes mmap and file; no fd leak.

**Integration tests**:
- `TestActiveSegment_CrashRecovery` — Spawn subprocess that appends N
  entries with periodic Sync; kill subprocess at random offset; on
  recovery, all synced entries readable, post-sync entries handled
  cleanly.

**End-to-end tests**: deferred.

---

#### 2.2 — Group-commit fsync

**Description**: `storage.GroupCommit` batches Sync requests within a window;
single fsync covers all batched writes; callers get notified on completion.

**Acceptance criteria**:
- Configurable window (default 2ms or 1MB pending, whichever first).
- N concurrent `Submit` calls within window result in 1 fsync syscall.
- Each Submit returns when its entry is durable (its sync covered it).
- Critical-class submits force immediate fsync (bypass batching).
- No deadlock: if no submitter, no fsync issued (idle).
- Statistics: tracks batch sizes, fsync count, latency histogram.

**Unit tests**:
- `TestGroupCommit_BatchOne` — Single submit triggers single fsync.
- `TestGroupCommit_BatchMany` — 100 concurrent submits trigger 1 fsync;
  all submits return success.
- `TestGroupCommit_WindowExpiry` — Submit then wait > window; fsync
  triggers without further submits.
- `TestGroupCommit_CriticalBypass` — Critical submit triggers fsync
  without waiting.
- `TestGroupCommit_NoLeaks_Idle` — No fsyncs issued when idle.
- `TestGroupCommit_Concurrent` — `go test -race` with 1000 submitters.

**Integration tests**:
- `TestGroupCommit_WithActiveSegment` — Active segment + group commit;
  durability verified by crashing and recovering.

**End-to-end tests**: deferred.

---

#### 2.3 — Segment sealing

**Description**: When active segment hits size threshold (default 64MB) or
age threshold (default 1h), seal it: compute Merkle tree over entries,
write Merkle file, rename to sealed naming, open new active segment.

**Acceptance criteria**:
- Sealing is atomic from observer perspective: either the segment is
  sealed-and-Merkle-rooted, or it's not; never half-sealed.
- Active segment swap happens only after sealed segment is durable.
- Merkle tree built incrementally during writes (so sealing is fast):
  every entry's hash inserted into a streaming Merkle builder; sealing
  finalizes the root.
- Sealed segment is read-only; open file descriptor can be closed.
- Merkle root committed to manifest before active rename.

**Unit tests**:
- `TestSegmentSealing_Atomic` — Crash mid-seal; recovery either sees
  fully sealed or fully active, never partial.
- `TestSegmentSealing_MerkleRoot` — Computed root matches root computed
  from re-reading all entries.
- `TestSegmentSealing_StreamingBuilder` — Streaming Merkle builder
  produces same root as batch builder over same input.
- `TestSegmentSealing_ManifestUpdate` — Manifest contains sealed segment's
  root after seal completes.

**Integration tests**:
- `TestSegmentSealing_RotateUnderLoad` — Sustained writes trigger
  multiple seals; all sealed segments verifiable; no entry loss.

**End-to-end tests**: deferred.

---

#### 2.4 — LSM indexes

**Description**: Per-subject LSM indexes for HLC, body_hash, dedupe_event_id,
dedupe_fingerprint, dedupe_idem_key, session_id, and parent (causal). Use
existing battle-tested LSM library (e.g., `cockroachdb/pebble`).

**Acceptance criteria**:
- `Put(key, offset)`, `Get(key) → offset`, `Range(start, end)`,
  `PrefixScan(prefix)`.
- Concurrent reads non-blocking; concurrent writes serialized per index.
- Crash-safe: WAL'd; recovers consistently.
- Index opens fast: O(log n) for Get, O(log n + k) for range with k matches.
- Compaction tunable; default leveled compaction.

**Unit tests**:
- `TestLSMIndex_PutGet` — Round-trip 10K entries.
- `TestLSMIndex_Range` — Range scan returns ordered subset.
- `TestLSMIndex_PrefixScan` — Prefix returns matching subset.
- `TestLSMIndex_Concurrent` — `go test -race` with 1K read+write
  goroutines.
- `TestLSMIndex_Crash` — Crash mid-write; recovery preserves committed
  writes, rejects uncommitted.
- `BenchmarkLSMIndex_Get` — < 1µs per Get on warm index.

**Integration tests**:
- `TestLSMIndex_AllSeven` — All seven index types coexist; cross-type
  consistency under writes.
- `TestLSMIndex_Compaction` — Long-running test with continuous writes;
  no unbounded growth.

**End-to-end tests**: deferred.

---

#### 2.5 — Manifest

**Description**: Per-subject `manifest.json` lists segments (active, sealed),
their Merkle roots, sealed timestamps, retention status. Updates atomic via
write-temp + rename.

**Acceptance criteria**:
- Manifest survives crash: half-written manifest reverts to prior version.
- Atomic update via `os.Rename` after `fsync` of temp.
- Manifest version incremented on each update.
- Reader returns latest committed version.
- All manifest reads/writes go through one mutex per subject.

**Unit tests**:
- `TestManifest_AtomicUpdate` — Crash mid-write; recovery sees prior
  version.
- `TestManifest_VersionMonotonic` — Each update increments version.
- `TestManifest_ConcurrentReaders` — Concurrent reads see consistent
  snapshot during write.

**Integration tests**:
- `TestManifest_WithSegmentSealing` — Seal triggers manifest update;
  state coherent.

**End-to-end tests**: deferred.

---

#### 2.6 — Snapshot writer

**Description**: Periodic per-subject snapshot of state-machine state at an
HLC frontier. Snapshot is content-addressed; manifest records snapshot
roots.

**Acceptance criteria**:
- Snapshot frequency configurable (default every 100K entries or 1h).
- Snapshot taken without blocking writes (uses Raft's snapshot semantics
  in clustered mode; in embedded mode uses a snapshot serializer
  protected by a read-lock).
- Snapshot contains: state-machine state at HLC `F`, list of segment
  roots covering `[0, F]`, signature.
- Crash during snapshot: snapshot file is written to temp, fsync'd,
  renamed; partial snapshot doesn't replace existing.

**Unit tests**:
- `TestSnapshot_RoundTrip` — Write snapshot; read back; state equal.
- `TestSnapshot_AtomicWrite` — Crash mid-write; existing snapshot
  preserved.
- `TestSnapshot_NonBlocking` — Snapshot in progress; concurrent writes
  succeed.

**Integration tests**:
- `TestSnapshot_RestoreFromSnapshot` — Restore state machine from
  snapshot; verify state equal to live state at snapshot HLC.

**End-to-end tests**: deferred.

---

#### 2.7 — Crash recovery

**Description**: `storage.Open(subject)` reads manifest, opens active
segment, replays from last snapshot's HLC forward, reconstructs in-memory
state.

**Acceptance criteria**:
- Recovery time bounded: O(entries since last snapshot).
- Replays only entries with valid CRC; truncates partial trailing entries.
- After Open, state machine matches what was durable before crash.
- Idempotent: opening already-open subject returns existing handle.

**Unit tests**:
- `TestStorageOpen_FromEmpty` — Open new subject; state empty.
- `TestStorageOpen_FromSnapshot` — Open subject with snapshot; state
  matches snapshot.
- `TestStorageOpen_FromSnapshotPlusReplay` — Open with snapshot + N post-
  snapshot entries; state matches snapshot+N.
- `TestStorageOpen_TrailingTruncation` — Manually corrupt last entry;
  open succeeds with truncation.

**Integration tests**:
- `TestStorage_CrashRecoveryUnderLoad` — Subprocess writes 100K entries;
  killed at random points; recovery preserves all synced entries.
- `TestStorage_RestartIdempotent` — 100 sequential open/close cycles
  produce stable state.

**End-to-end tests**:
- `TestStorageE2E_CrashChaos` — Long-running subprocess with random
  kills; runs for 1 minute; final state consistent and recoverable.

---

#### 2.8 — Sealed segment reader

**Description**: `storage.SealedReader(segment) → Reader` provides random-
access read of sealed segments. Verifies Merkle proofs on demand.

**Acceptance criteria**:
- `Read(offset) → entry` returns entry at offset.
- `Verify(offset) → bool` verifies the entry's Merkle path against the
  segment root.
- Random-access reads use mmap; no syscall per read.
- Verification is cheap: O(log n) Merkle path traversal.

**Unit tests**:
- `TestSealedReader_Read` — Read each entry; matches what was written.
- `TestSealedReader_Verify` — Each entry verifies.
- `TestSealedReader_VerifyTampered` — Tampered entry fails verify.
- `TestSealedReader_RandomAccess` — Read entries in arbitrary order; all
  succeed.

**Integration tests**:
- `TestSealedReader_AfterSeal` — Seal segment; reader opens; all entries
  readable.

**End-to-end tests**: deferred.

---

### Phase 3 — Single-Node Raft (Subject Registry)

Implement Raft as the consensus primitive. Single group for the subject
registry.

#### 3.1 — Raft node core

**Description**: `consensus.RaftNode` implements Raft per the 2014 paper,
plus pre-vote, joint consensus, and read-index.

**Acceptance criteria**:
- States: Follower, Candidate, Leader. Plus PreVote sub-state.
- Leader election with pre-vote: candidate sends pre-vote round before
  incrementing term; only proceeds if majority would grant.
- Log append: leader replicates entries to followers; commit on majority
  ack.
- Log compaction via snapshot.
- Crash safety: term and votedFor durable before any RPC.
- Liveness: in stable network with majority alive, leader elected within
  bounded time (10× heartbeat interval).
- Safety: at most one leader per term (verified by election rules).

**Unit tests**:
- `TestRaft_ElectLeader_Stable` — 3 nodes; leader elected within 10
  heartbeats.
- `TestRaft_LogReplication` — Leader appends; followers receive in
  order.
- `TestRaft_PreVote_PreventsDisruption` — Partitioned-then-rejoined node
  with stale high term doesn't disrupt; pre-vote rejects it.
- `TestRaft_CrashRecovery_TermDurable` — Crash after vote; recovery
  preserves vote.
- `TestRaft_AtMostOneLeaderPerTerm` (property test) — Random partition
  patterns; never 2 leaders in same term.
- `TestRaft_LogConsistency` (property test) — At all times, all
  committed entries identical across replicas.

**Integration tests**:
- `TestRaft_PartitionRecovery` — Partition leader from majority; new
  leader elected; on heal, old leader steps down.
- `TestRaft_SlowFollower` — One follower stalls; leader continues with
  remaining quorum; stalled follower catches up on resume.

**End-to-end tests**: deferred to phase 7.

---

#### 3.2 — Joint consensus membership change

**Description**: Membership changes go through a transitional config (Cold,
Cold,New, New) per Raft 2014 §6.

**Acceptance criteria**:
- `AddVoter(id)`, `RemoveVoter(id)` initiate joint config.
- During joint config, quorum is majority of OLD AND majority of NEW.
- Transition completes when joint-config entry committed.
- Concurrent membership changes serialized (one at a time).
- Crash mid-change: recovery completes the change or aborts cleanly.

**Unit tests**:
- `TestRaftJointConsensus_AddVoter` — 3-node group; add 4th; quorum
  doubles correctly during transition.
- `TestRaftJointConsensus_RemoveVoter` — 3-node group; remove 1; quorum
  drops to 1 of 2.
- `TestRaftJointConsensus_NoSplitBrain` (property test) — Random
  add/remove sequences; never split-brain.
- `TestRaftJointConsensus_CrashMidChange` — Crash during joint config;
  recovery completes change.

**Integration tests**:
- `TestRaftJointConsensus_3DCAddDC` — Initial 3 replicas in 1 DC; add
  3 in second DC; quorum requires majority of both.

**End-to-end tests**: deferred to phase 7.

---

#### 3.3 — Read-index reads

**Description**: Linearizable reads without leader leases. Leader records
current commit index, confirms leadership via heartbeat round, then serves
reads against that index.

**Acceptance criteria**:
- `ReadIndex() → index` returns a commit index after confirming leadership.
- Subsequent local reads at that index are linearizable.
- Stale leader (deposed during read) returns `ErrNotLeader`.
- Bounded wait: read-index returns within RPC round-trip.

**Unit tests**:
- `TestRaftReadIndex_Linearizable` — Read after write; sees write.
- `TestRaftReadIndex_StaleLeaderRejected` — Network-isolated leader;
  read-index times out.
- `TestRaftReadIndex_Concurrent` — Many concurrent reads; all linearizable.

**Integration tests**:
- `TestRaftReadIndex_VsLease_NoFalseLinearizability` — Inject clock skew;
  read-index correct, would-have-been-leader-lease incorrect.

**End-to-end tests**: deferred.

---

#### 3.4 — Content-addressed log entries

**Description**: Raft log entry stores `(term, index, BLAKE3 hash, body
pointer)`. Bodies dedup'd across entries.

**Acceptance criteria**:
- `Append(body)` hashes body, dedups, stores `(term, index, hash, ptr)`.
- Re-propose of same body (e.g., after leader change) reuses existing
  storage.
- Replication sends just `(term, index, hash)` if follower already has
  the body; sends body otherwise.
- Body store is reference-counted; bodies removed when no entry refers
  to them.

**Unit tests**:
- `TestRaftLog_DedupBody` — Same body appended twice stores once.
- `TestRaftLog_ReproposalReusesBody` — After leader change, re-propose
  same body; no new storage.
- `TestRaftLog_ReplicationOptimization` — Follower with body receives
  only header; without body receives full.
- `TestRaftLog_BodyGCOnTruncation` — Truncate log; orphaned bodies GC'd.

**Integration tests**:
- `TestRaftLog_DedupAcrossLeaderChanges` — Re-elections + re-proposes;
  body count bounded.

**End-to-end tests**: deferred.

---

#### 3.5 — Single-replica degenerate mode

**Description**: Raft group of size 1. Full state machine runs but quorum
is trivially 1. Used in embedded mode.

**Acceptance criteria**:
- 1-replica group elects itself as leader without any RPCs.
- Each "fsync" is a single fsync (no replication).
- No spurious elections.
- Same code path as multi-replica; only count differs.
- Membership transitions still work (can grow from 1 to 3).

**Unit tests**:
- `TestRaft_SingleReplica_AutoLeader` — Single-node group; leader
  elected on start.
- `TestRaft_SingleReplica_AppendCommit` — Append succeeds; committed on
  fsync.
- `TestRaft_SingleReplica_GrowToThree` — 1→3 membership transition
  succeeds.

**Integration tests**:
- `TestRaft_SingleReplica_WithStorage` — Single-node group + storage;
  durable across restarts.

**End-to-end tests**: deferred to phase 4.

---

#### 3.6 — Streaming Merkle snapshots

**Description**: Snapshot install via incremental Merkle reconciliation.
Receiver fetches only Merkle nodes it lacks.

**Acceptance criteria**:
- `RequestSnapshot()` from follower triggers exchange.
- Receiver sends its current Merkle root; sender computes diff and sends
  only divergent leaves.
- Snapshot installation is verifiable: receiver verifies each leaf
  against the sender's root before applying.
- Resumable: interrupted snapshot can resume from last verified leaf.

**Unit tests**:
- `TestSnapshot_StreamingFullDivergence` — Receiver has nothing; full
  snapshot streamed.
- `TestSnapshot_StreamingPartialDivergence` — Receiver has 90%; only 10%
  streamed.
- `TestSnapshot_StreamingResume` — Interrupt mid-stream; resume completes.
- `TestSnapshot_StreamingTamperDetected` — Tampered leaf rejected; whole
  snapshot rejected.

**Integration tests**:
- `TestSnapshot_StreamingWithRaft` — Stale follower; snapshot install
  catches it up.

**End-to-end tests**: deferred to phase 7.

---

#### 3.7 — Subject registry state machine

**Description**: Raft state machine for subject registry. Operations:
`Register(uri, schema, authority)`, `Deprecate(uri)`, `LookupSnapshot()`.

**Acceptance criteria**:
- All registry operations go through Raft.
- State machine deterministic: same log → same state.
- Snapshot serializes full registry.
- Restore from snapshot reconstructs equivalent state.
- All replicas have identical state at any committed index.

**Unit tests**:
- `TestRegistrySM_Register` — Apply Register; state contains entry.
- `TestRegistrySM_RegisterDuplicate` — Duplicate registration is no-op
  (returns existing).
- `TestRegistrySM_Deprecate` — Deprecate marks entry; not deletion.
- `TestRegistrySM_DeterministicReplay` — Replay same log produces
  identical state across N runs.
- `TestRegistrySM_SnapshotRestore` — Snapshot then restore; state equal.

**Integration tests**:
- `TestRegistrySM_WithRaft_3Replicas` — 3-replica Raft group; all
  replicas converge.
- `TestRegistrySM_WithRaft_LeaderChange` — Mid-operation leader change;
  no operation lost.

**End-to-end tests**: deferred.

---

### Phase 4 — Embedded Mode End-to-End

First runnable substrate. All in-process. Validates the design end to end.

#### 4.1 — Substrate boot sequence

**Description**: `substrate.Open(config) → *Substrate` performs the embedded
mode boot per §16.5: open subject registry, start Raft groups, restore
state, ready for use.

**Acceptance criteria**:
- Cold boot completes within 500ms for typical session (5-15 namespaces,
  each with snapshots).
- Warm boot (recent snapshot) completes within 100ms.
- Boot is atomic: either fully ready or returns error; no partial-ready
  state.
- Concurrent Open calls on same data dir return same handle (singleton).
- Close cleanly terminates all goroutines (`goleak` clean).

**Unit tests**:
- `TestSubstrate_BootEmpty` — Open empty data dir; ready in <100ms.
- `TestSubstrate_BootWithExisting` — Open data dir with prior state;
  state restored.
- `TestSubstrate_ConcurrentOpen` — Concurrent Opens return same handle.
- `TestSubstrate_CloseGoroutines` — `goleak.VerifyNone` after Close.

**Integration tests**:
- `TestSubstrate_BootTime_ColdWarm` — Measured cold/warm boot times
  meet criteria.

**End-to-end tests**:
- `TestSubstrateE2E_BootRunCloseRestart` — Open, publish 1K messages,
  close, reopen, all messages present.

---

#### 4.2 — Publish API

**Description**: `Publish(ctx, subject, body, opts) → (entryID, error)`.
Stamps HLC, computes hashes, runs validation pipeline, appends to storage,
triggers replication (single replica in embedded).

**Acceptance criteria**:
- Allocation cost per Publish: ≤ 2 allocations (entry envelope, body
  copy).
- Authority check happens before storage write.
- Schema validation happens before storage write.
- Three-layer dedupe checked before storage write.
- Returns committed entry ID after fsync (Critical class) or after
  group-commit window (Standard, Bulk).
- Returns immediately (best-effort) for Background class.
- Concurrent publishes serialized per subject (HLC monotonicity).

**Unit tests**:
- `TestPublish_Critical_DurableBeforeReturn` — Crash immediately after
  Publish returns; entry present on recovery.
- `TestPublish_Bulk_GroupCommitDelays` — Bulk publish returns after
  group-commit window.
- `TestPublish_Background_NoFsync` — Background publish doesn't fsync.
- `TestPublish_AuthorityRejection` — Publish without capability rejected
  with `ErrUnauthorized`.
- `TestPublish_SchemaRejection` — Bad body rejected with `ErrSchema`.
- `TestPublish_DuplicateRejected` — Duplicate event_id, fingerprint, or
  idem_key dropped.
- `TestPublish_HLCMonotonic` (property test) — N concurrent publishes;
  HLCs are monotonic on the receiver side.

**Integration tests**:
- `TestPublish_AllocationsBounded` — `testing.AllocsPerRun` ≤ 2 for hot
  path.
- `TestPublish_CrashAfterReturn` — Subprocess crashes after publish
  returns; recovery shows entry present.

**End-to-end tests**:
- `TestPublishE2E_HighThroughput` — 100K msg/sec sustained for 60s on
  single laptop; no errors, no leaks.

---

#### 4.3 — Subscribe API

**Description**: `Subscribe(ctx, subject, cursor, opts) → Subscription`.
Returns a stream of entries from cursor's HLC frontier forward.

**Acceptance criteria**:
- Subscription delivers entries in HLC order per subject.
- Within a partition_key, entries delivered in publisher order.
- Across partition_keys, parallel delivery allowed.
- Subscription survives substrate Close+Reopen if cursor persisted.
- `Cancel()` cleanly terminates; no goroutine leak.

**Unit tests**:
- `TestSubscribe_FromBeginning` — Subscribe with empty cursor; receives
  all entries.
- `TestSubscribe_FromMidpoint` — Subscribe with cursor at HLC X;
  receives only entries after X.
- `TestSubscribe_OrderingPerPartition` — Multiple partition_keys;
  per-partition order maintained.
- `TestSubscribe_CancelClean` — Cancel; `goleak` clean.

**Integration tests**:
- `TestSubscribe_LiveDelivery` — Subscriber subscribes; publisher
  publishes; subscriber receives in <10ms.
- `TestSubscribe_BackpressureRespected` — Subscriber slow; publisher
  slows for that class.

**End-to-end tests**:
- `TestSubscribeE2E_PubSub_OneMillionMessages` — 1M messages published;
  subscriber receives all; no loss, correct order.

---

#### 4.4 — Cursor implementation

**Description**: `Cursor{HLCFrontier, CausalSet, ResumeProof, Generation}`
per §8.1. Persisted via consumer's storage.

**Acceptance criteria**:
- Cursor serializes deterministically.
- Bloom filter false-positive rate ≤ 1% at expected size.
- ResumeProof verifies against current Merkle root.
- Cursor invalidation returns clear error.
- Generation increments on cursor reset (e.g., reposition to HLC X).

**Unit tests**:
- `TestCursor_SerializeDeterministic` — Same cursor → same bytes.
- `TestCursor_BloomFalsePositive` — At 10K entries, FP rate ≤ 1%.
- `TestCursor_ResumeProofValid` — Cursor against current root verifies.
- `TestCursor_ResumeProofInvalid` — Cursor against rebased root rejected.
- `TestCursor_GenerationIncrement` — Reset increments generation.

**Integration tests**:
- `TestCursor_AfterSnapshot` — Cursor across snapshot still valid.
- `TestCursor_AfterSegmentSeal` — Cursor across segment seal still
  valid.

**End-to-end tests**:
- `TestCursorE2E_RestartConsumerResume` — Consumer restarts; cursor
  resume delivers exactly the missing entries.

---

#### 4.5 — Three-layer dedupe (full)

**Description**: All three layers active. event_id, fingerprint, idem_key
indexed in LSM. TTL compaction.

**Acceptance criteria**:
- All three layers checked on every receive.
- TTL respected: expired dedupe entries removed during compaction.
- Dedupe table size bounded (~ entries within TTL window).
- No false negatives within TTL: a dedupe within TTL always fires.
- Cross-replica consistency: replicas have same dedupe state.

**Unit tests**:
- `TestDedupe_Layer1_EventID` — Same event_id rejected.
- `TestDedupe_Layer2_Fingerprint` — Same fingerprint, different event_id
  rejected.
- `TestDedupe_Layer3_IdemKey` — Same idem_key, different event_id and
  body rejected.
- `TestDedupe_TTLExpiry` — Entry past TTL no longer dedup'd.
- `TestDedupe_Concurrent` — `go test -race` 1K concurrent publishes,
  some duplicates.

**Integration tests**:
- `TestDedupe_TTLCompaction` — Long-running test; dedupe table size
  bounded.
- `TestDedupe_AfterRestart` — Restart; dedupe state preserved per TTL.

**End-to-end tests**:
- `TestDedupeE2E_NetworkRetransmit` — Simulate retransmit at every
  layer; exactly once delivery verified.

---

#### 4.6 — Recovery from crash

**Description**: After kill -9 of substrate process, recovery restores all
durable state.

**Acceptance criteria**:
- All Critical-class messages durable before Publish returned: present
  after recovery.
- Standard-class messages within group-commit window may be lost (per
  contract).
- No corrupted state: every recovery produces a valid state machine.
- Recovery time bounded: O(entries since last snapshot).

**Unit tests**: covered in 2.7, 4.1, 4.2.

**Integration tests**:
- `TestRecovery_AfterKillCritical` — Publish 1000 Critical; kill mid-flight; all 1000 present.
- `TestRecovery_AfterKillStandard` — Publish 1000 Standard; kill mid-flight; ≥ N present where N = published-before-last-group-commit.

**End-to-end tests**:
- `TestRecoveryE2E_RandomKillsUnderLoad` — 60-second test; random kill
  every 1-5s; on each restart, durability invariants hold.

---

### Phase 5 — QUIC Transport

Cross-process and cross-network communication.

#### 5.1 — QUIC server

**Description**: `transport.QUICServer` accepts QUIC connections with mTLS.
Validates SVIDs against cluster CA.

**Acceptance criteria**:
- Listens on configured UDP port.
- mTLS required; clients without valid SVID rejected.
- Per-connection arena allocator.
- Connection limit enforced (default 10K concurrent connections).
- Graceful shutdown drains active connections.

**Unit tests**:
- `TestQUICServer_AcceptsValidSVID` — Client with valid SVID connects.
- `TestQUICServer_RejectsInvalidSVID` — Client with expired/unknown
  SVID rejected.
- `TestQUICServer_ConnectionLimit` — N+1 connection beyond limit
  rejected.
- `TestQUICServer_GracefulShutdown` — Shutdown drains; active conns
  finish; no goroutine leak.

**Integration tests**:
- `TestQUICServer_ConcurrentConnections` — 1K concurrent clients;
  all succeed.

**End-to-end tests**:
- `TestQUICE2E_RealNetwork` — Two real processes over loopback;
  bidirectional traffic.

---

#### 5.2 — QUIC client

**Description**: `transport.QUICClient` connects with mTLS, presents SVID,
multiplexes streams.

**Acceptance criteria**:
- 0-RTT resumption when prior session valid.
- Connection migration: if local IP changes, connection survives.
- Backoff on connection failures (exponential, capped).
- Auto-reconnect on connection loss.

**Unit tests**:
- `TestQUICClient_ZeroRTT` — Reconnect after prior session uses 0-RTT.
- `TestQUICClient_Migration` — Local IP change; connection survives.
- `TestQUICClient_BackoffOnFailure` — Failure pattern; backoff
  observed.
- `TestQUICClient_AutoReconnect` — Server restart; client reconnects.

**Integration tests**:
- `TestQUICClient_AgainstServer` — Client+server pair; round-trip
  traffic.

**End-to-end tests**:
- `TestQUICClientE2E_LongRunning` — 1h continuous connection with
  random server restarts; client maintains availability.

---

#### 5.3 — Two-channel split

**Description**: Per QUIC connection: data plane stream pool + dedicated
control plane stream.

**Acceptance criteria**:
- Control stream never blocks on data plane.
- Data plane backpressure doesn't affect control delivery.
- Heartbeats / SWIM probes flow on control even when data plane saturated.

**Unit tests**:
- `TestTwoChannel_ControlNotBlocked` — Saturate data plane; control
  message delivers in <10ms.

**Integration tests**:
- `TestTwoChannel_UnderLoad` — 100MB/s on data plane; SWIM probes
  on control still meet timing budget.

**End-to-end tests**:
- `TestTwoChannelE2E_SWIMUnderLoad` — Cluster with high data load;
  SWIM probes succeed; no false suspicions.

---

#### 5.4 — Frame multiplexing over QUIC streams

**Description**: Multiple subjects' frames flow over one connection
multiplexed across QUIC streams. Each subject gets its own stream pool.

**Acceptance criteria**:
- Per-subject stream pool sized by class (Critical reserved streams).
- No head-of-line blocking across subjects.
- Stream allocation bounded (no unbounded growth).
- Idle streams reaped after configured timeout.

**Unit tests**:
- `TestQUICMux_PerSubjectStreams` — Two subjects don't HoL-block.
- `TestQUICMux_StreamReaping` — Idle streams reaped.

**Integration tests**:
- `TestQUICMux_HighSubjectCount` — 1K subjects on one connection;
  fairness maintained.

**End-to-end tests**:
- `TestQUICMuxE2E_MultiPubSub` — Many publishers + subscribers; all
  meet latency budgets.

---

### Phase 6 — SWIM Membership

Port hyperscale's SWIM stack to Go and extend.

#### 6.1 — SWIM core port

**Description**: Port `hyperscale/distributed/swim/core/`,
`detection/`, `gossip/` to Go.

**Acceptance criteria**:
- Port produces equivalent behavior in Go: same probe cadence, same
  suspicion rules, same incarnation semantics.
- Behavioral equivalence verified by replay tests against captured
  hyperscale traces.
- Allocation-bounded: probe loop allocation-free; gossip bounded.

**Unit tests**:
- `TestSWIM_ProbeCycleStable` — 5-node simulated; all alive throughout.
- `TestSWIM_ProbeCycleFailure` — Kill 1 node; SWIM detects within
  configured time bound.
- `TestSWIM_IndirectProbe` — Direct probe blocked; indirect succeeds.
- `TestSWIM_IncarnationRefutation` — Stale rumor refuted by higher
  incarnation.
- `TestSWIM_PiggybackGossip` — State updates propagate via probe acks.
- `TestSWIM_TimingWheelAccuracy` — Scheduled probes fire within ±5ms of
  scheduled time.
- `TestSWIM_PhiAccrual` — φ score monotonic with elapsed silent time.
- `TestSWIM_LHM` — Local health multiplier adapts to network jitter.

**Integration tests**:
- `TestSWIM_50NodeStableCluster` — 50-node cluster; all alive after 1
  minute; memory stable.
- `TestSWIM_50NodeRollingFailure` — 5 nodes fail at random times; all
  detected; no false positives.

**End-to-end tests**:
- `TestSWIME2E_AcrossDC` — 9 nodes across 3 DCs (3+3+3); all alive
  status converges; cross-DC failures detected.

---

#### 6.2 — Hierarchical liveness levels

**Description**: Track (node, pod, agent, session) levels with separate
probe cadences and failure semantics per §5.2.

**Acceptance criteria**:
- Each level has its own φ score.
- Failure at one level doesn't propagate up unless the upper level also
  fails.
- Probe cadences per level configurable.
- Recovery decisions gated by level.

**Unit tests**:
- `TestHierarchical_AgentFailureNotPodFailure` — Kill agent; pod still
  alive; only agent restarted.
- `TestHierarchical_PodFailurePropagatesAgents` — Kill pod; all its
  agents marked failed.
- `TestHierarchical_NodeFailurePropagatesPods` — Kill node; all its pods
  marked failed.

**Integration tests**:
- `TestHierarchical_FullStack` — Realistic Sylk session; injected
  failures at each level; correct recovery action triggered.

**End-to-end tests**:
- `TestHierarchicalE2E_AgentRestart` — Agent crash; pod restarts agent;
  session continues.

---

#### 6.3 — Sectioned Vivaldi coordinates

**Description**: Coordinates per (intra-rack, intra-DC, cross-DC) per §5.3.

**Acceptance criteria**:
- Each node maintains 3 coordinate vectors.
- Distance query returns appropriate section's distance.
- Coordinates converge under varying network conditions.
- Selection: peer picker uses correct section for operation type.

**Unit tests**:
- `TestVivaldi_CoordinateConvergence` — Synthetic latency matrix;
  coordinates converge to predicted distances.
- `TestVivaldi_SectionSelection` — Per operation type, correct
  section's distance returned.

**Integration tests**:
- `TestVivaldi_CrossDCSelection` — 3 DCs; cross-DC peer selection
  picks closer DC.

**End-to-end tests**:
- `TestVivaldiE2E_RealLatencies` — Real cross-DC test (or simulated
  with realistic latencies); selections measurably better than random.

---

#### 6.4 — Dual-channel suspicion

**Description**: Suspect requires both SWIM and QUIC keepalive failure per
§5.5.

**Acceptance criteria**:
- Single-channel failure: "possibly suspect" — retry/indirect-probe,
  no suspicion timer started.
- Dual-channel failure: SUSPECT, suspicion timer starts.
- False-positive rate measurably reduced vs single-channel.

**Unit tests**:
- `TestDualChannel_SWIMOnlyFails_NoSuspicion` — SWIM fails, QUIC ok;
  no suspicion.
- `TestDualChannel_QUICOnlyFails_NoSuspicion` — QUIC fails, SWIM ok;
  no suspicion.
- `TestDualChannel_BothFail_Suspicion` — Both fail; SUSPECT.

**Integration tests**:
- `TestDualChannel_FlakyNIC` — Inject SWIM-only flakiness; no false
  positives over 5 minutes.

**End-to-end tests**:
- `TestDualChannelE2E_NetworkPartialFailure` — Partial network failure
  affecting SWIM port only; no false suspicions.

---

#### 6.5 — Merkle reconciliation on rejoin

**Description**: Rejoining node exchanges Merkle roots; only divergent
leaves transferred per §5.4.

**Acceptance criteria**:
- Bandwidth proportional to changes during absence, not absence duration.
- Both sides converge to common state.
- Concurrent changes during reconcile handled (CRDT-like).

**Unit tests**:
- `TestMerkleReconcile_NoDivergence` — Both sides identical; no leaves
  transferred.
- `TestMerkleReconcile_FullDivergence` — Receiver empty; full state
  transferred.
- `TestMerkleReconcile_PartialDivergence` — 10% divergence; ~10%
  transfer.
- `TestMerkleReconcile_Convergence` — After reconcile, both sides
  identical.

**Integration tests**:
- `TestMerkleReconcile_5NodeRejoin` — Node away for 1h; rejoins; state
  converges.

**End-to-end tests**:
- `TestMerkleReconcileE2E_LongPartition` — Multi-hour partition;
  rejoin completes within bandwidth budget.

---

### Phase 7 — Multi-Raft

Multiple Raft groups for namespace partitioning.

#### 7.1 — Operator group bootstrap

**Description**: 3-replica Raft group that manages multi-Raft itself.
Bootstrap at cluster genesis.

**Acceptance criteria**:
- Cluster genesis: first 3 nodes form operator group via deterministic
  rule (lowest 3 node IDs).
- Operator group manages: group creation, member transitions, namespace
  migrations.
- Operator group itself is a Raft group; loss of majority pauses
  cluster operations.

**Unit tests**:
- `TestOperator_GenesisBootstrap` — 3 nodes start; operator group
  formed deterministically.
- `TestOperator_LossOfMajorityPauses` — Kill 2 of 3 operators; cluster
  ops fail with `ErrOperatorUnavailable`.

**Integration tests**:
- `TestOperator_CreateNamespaceGroup` — Operator creates namespace
  group; group active.

**End-to-end tests**:
- `TestOperatorE2E_FullClusterBootstrap` — 9 nodes; operator group
  formed; topology group formed; ready for namespace creation.

---

#### 7.2 — Topology group

**Description**: 5-7 replica group for cluster membership canon, subject
registry, durability policies, authority profiles.

**Acceptance criteria**:
- Replicas spread across DCs.
- Operations on topology don't compete with namespace operations.
- Subject registry now lives in topology group.

**Unit tests**:
- `TestTopology_SpreadAcrossDCs` — 7 replicas across 3 DCs; no DC has
  majority alone.
- `TestTopology_SubjectRegistry_HA` — Lose 1 DC's replicas; registry
  still operational.

**Integration tests**:
- `TestTopology_RegistryConsistency` — Concurrent registrations;
  consistency preserved.

**End-to-end tests**:
- `TestTopologyE2E_DCFailover` — Kill DC; topology group survives;
  cluster operational.

---

#### 7.3 — Namespace group lifecycle

**Description**: Create, place, migrate, retire namespace groups.

**Acceptance criteria**:
- `CreateNamespace(name)` — operator allocates group, picks 3 replicas
  via rendezvous hashing weighted by Vivaldi proximity.
- `MigrateNamespace(name, newReplicas)` — joint consensus moves replicas.
- `RetireNamespace(name)` — group dissolved; data archived per retention.

**Unit tests**:
- `TestNamespace_Create` — Create succeeds; group has 3 replicas.
- `TestNamespace_Migrate` — Migrate adds new replicas, syncs, removes
  old; no downtime.
- `TestNamespace_Retire` — Retire archives; group gone.

**Integration tests**:
- `TestNamespace_PlacementHonorsVivaldi` — Created groups have replicas
  in close DCs.

**End-to-end tests**:
- `TestNamespaceE2E_LifecycleUnderLoad` — Continuous traffic; create,
  migrate, retire operations succeed.

---

#### 7.4 — Rendezvous hashing for placement

**Description**: Deterministic replica placement: for namespace `N`, pick
nodes with highest hash of `(node_id, namespace_id)` weighted by Vivaldi
proximity.

**Acceptance criteria**:
- Same input → same placement (deterministic).
- Adding/removing nodes minimally redistributes namespaces.
- Weighted by Vivaldi proximity within target DC where possible.

**Unit tests**:
- `TestRendezvous_Deterministic` — Same input → same output.
- `TestRendezvous_MinimalRedistribution` — Add 1 node to 9; ~10% of
  namespaces move.
- `TestRendezvous_VivaldiWeighted` — Closer nodes preferred.

**Integration tests**:
- `TestRendezvous_NodeAddRemove` — Realistic add/remove sequence;
  cluster rebalances.

**End-to-end tests**:
- `TestRendezvousE2E_BalancedLoad` — 100 namespaces, 10 nodes; load
  within ±20% across nodes.

---

#### 7.5 — Group routing

**Description**: Subject's namespace component selects the namespace group;
client routes operations there.

**Acceptance criteria**:
- Subject URI → namespace group → leader address.
- Routing cache: warm cache for active subjects.
- Cache invalidation on leader change.
- Fallback: any replica answers, redirects to leader if needed.

**Unit tests**:
- `TestRouting_SubjectToGroup` — URI resolves to correct group.
- `TestRouting_LeaderRedirect` — Wrong replica redirects to leader.
- `TestRouting_CacheInvalidation` — Leader change invalidates cache.

**Integration tests**:
- `TestRouting_LiveLeaderChange` — Mid-routing leader change; client
  succeeds via redirect.

**End-to-end tests**:
- `TestRoutingE2E_HighSubjectCount` — 10K subjects across many groups;
  routing latency < 1ms median.

---

#### 7.6 — MultiNamespaceTx (2PC)

**Description**: Cross-namespace transactions per §6.4.

**Acceptance criteria**:
- Coordinator picked deterministically (lex-min namespace ID).
- All participants log PREPARE durably before responding.
- Coordinator failure recoverable: any participant can recover by
  consulting coordinator's namespace group's log.
- Atomic: either all participants commit or all abort.

**Unit tests**:
- `TestMultiNamespaceTx_HappyPath` — All prepare, all commit.
- `TestMultiNamespaceTx_PrepareFailAborts` — One prepare fails; all
  abort.
- `TestMultiNamespaceTx_CoordinatorCrash` — Coordinator crashes mid-tx;
  recovery completes (abort or commit per state).
- `TestMultiNamespaceTx_ParticipantCrash` — Participant crashes after
  PREPARE; recovery applies COMMIT or ABORT.

**Integration tests**:
- `TestMultiNamespaceTx_3NamespaceCommit` — 3 namespaces; commit
  preserves invariants.

**End-to-end tests**:
- `TestMultiNamespaceTxE2E_RandomFailures` — Inject random crashes
  during tx; invariant preserved.

---

#### 7.7 — Escrow / compensation

**Description**: For unbounded N participants, use escrow + compensation
fallback per §6.4.

**Acceptance criteria**:
- `Escrow(participants, op)` distributes reservations.
- `Apply()` makes escrow visible.
- `Compensate()` issues compensating writes if abort.
- Idempotent compensations.

**Unit tests**:
- `TestEscrow_DistributedReservation` — All participants escrow.
- `TestEscrow_ApplyOrCompensate` — Apply visible; compensate reverses.
- `TestEscrow_CompensationIdempotent` — Re-apply compensation no-op.

**Integration tests**:
- `TestEscrow_LargeFanout` — 50 participants; all escrow; then apply.

**End-to-end tests**:
- `TestEscrowE2E_PartialFailures` — 50 participants; some fail to
  escrow; compensations issued for those that did.

---

### Phase 8 — Replicated Storage

Subject storage replicated across Raft replicas of the namespace group.

#### 8.1 — Storage replication

**Description**: Subject's storage is part of the namespace group's
replicated state machine. Every write goes through Raft.

**Acceptance criteria**:
- Writes durable on majority before Publish returns (Critical class).
- Reads served from any replica (with read-index for linearizable).
- Replicas have byte-identical sealed segments.

**Unit tests**:
- `TestStorageReplication_AllReplicasIdentical` — After N writes, all
  replicas have same Merkle root.
- `TestStorageReplication_FollowerCatchUp` — Stale follower catches up.

**Integration tests**:
- `TestStorageReplication_3Replica_LeaderChange` — Mid-write leader
  change; no entry lost; final state consistent.

**End-to-end tests**:
- `TestStorageReplicationE2E_DCFailover` — DC partition; writes continue
  on remaining DCs; on heal, partitioned DC catches up.

---

#### 8.2 — Streaming Merkle snapshot install

**Description**: Use streaming Merkle reconciliation for snapshot install.

**Acceptance criteria**:
- Catch-up cost proportional to actual divergence.
- Resumable.
- Verifiable.

**Unit tests**: covered in 3.6.

**Integration tests**:
- `TestStreamingSnapshot_LargeStateLittleDivergence` — 10GB state, 1MB
  divergence; transfer ~1MB.

**End-to-end tests**:
- `TestStreamingSnapshotE2E_NodeRejoinAfterDay` — Node away 24h; rejoin
  catches up in seconds, not hours.

---

#### 8.3 — Quorum-aware backpressure

**Description**: Leader stops accepting proposals when follower backlog
grows beyond threshold.

**Acceptance criteria**:
- Threshold configurable per group.
- Backpressure triggers `ErrFollowerBacklog` to publishers.
- Backpressure releases when followers catch up.

**Unit tests**:
- `TestQuorumBackpressure_TriggersOnBacklog` — Slow follower; leader
  rejects new writes.
- `TestQuorumBackpressure_Releases` — Follower catches up; writes
  resume.

**Integration tests**:
- `TestQuorumBackpressure_UnderHighLoad` — Sustained high load with one
  slow follower; no OOM, no crash.

**End-to-end tests**:
- `TestQuorumBackpressureE2E_RecoveryAfterStall` — Follower stalls 1
  minute; recovers; backlog drains.

---

### Phase 9 — Delivery (Pull-First)

#### 9.1 — Pull-first consumer

**Description**: `delivery.Consumer` requests batches via cursor; substrate
serves entries from cursor's HLC frontier forward.

**Acceptance criteria**:
- Consumer never receives unrequested entries.
- Credit window: consumer specifies max in-flight; substrate respects.
- Inflight set persisted in consumer's namespace group.

**Unit tests**:
- `TestPullConsumer_ExplicitCredit` — Consumer requests 10; receives
  ≤10.
- `TestPullConsumer_InflightTracked` — Inflight visible via API.

**Integration tests**:
- `TestPullConsumer_LeaderChange` — Mid-pull leader change; pull
  continues seamlessly.

**End-to-end tests**:
- `TestPullConsumerE2E_HighThroughput` — 100K msg/sec sustained.

---

#### 9.2 — Three-state acks

**Description**: ACK / NACK / TERM per §8.3.

**Acceptance criteria**:
- ACK removes from inflight; commits cursor advance.
- NACK redelivers per retry policy.
- TERM removes; routes to dead-letter.
- Missed ack within deadline behaves as NACK.

**Unit tests**:
- `TestAck_AdvancesCursor` — Cursor moves past ACKed entry.
- `TestNack_Redelivers` — NACK; entry redelivered.
- `TestTerm_DeadLetters` — TERM; entry in dead-letter subject.
- `TestAck_MissedDeadline_Redelivers` — No ack in deadline; redelivered.

**Integration tests**:
- `TestAck_ConsumerCrashRecovery` — Consumer crashes between recv and
  ack; on restart, entry redelivered.

**End-to-end tests**:
- `TestAckE2E_RandomNackPattern` — Random NACK pattern; eventual
  delivery succeeds.

---

#### 9.3 — Push as pull (sugar)

**Description**: Push API implemented over pull machinery.

**Acceptance criteria**:
- Same failure model as pull.
- Push is convenience; pull is foundation.

**Unit tests**:
- `TestPush_BehavesAsPull` — Push consumer sees same behavior under
  failure as pull.

**Integration tests**:
- `TestPush_AckSemantics` — All ack/nack/term semantics work via push.

**End-to-end tests**: covered by general delivery E2E.

---

### Phase 10 — Reliability

#### 10.1 — Message classes

**Description**: Critical, Standard, Bulk, Background per §10.2.

**Acceptance criteria**:
- Each class has its own queue, fsync policy, and priority.
- Bulk cannot starve Critical.
- Class declared at subject registration; per-frame override allowed
  with authority.

**Unit tests**:
- `TestMessageClass_Priority` — Critical messages preempt Bulk in
  scheduler.
- `TestMessageClass_FsyncPolicy` — Critical fsyncs synchronously;
  Background never fsyncs.

**Integration tests**:
- `TestMessageClass_Fairness` — Bulk flood with concurrent Critical;
  Critical latency bounded.

**End-to-end tests**:
- `TestMessageClassE2E_RealisticMix` — 99% Standard, 1% Critical;
  Critical p99 <50ms.

---

#### 10.2 — Wire-level credit advertisement

**Description**: Receivers advertise per-class capacity on acks and probes.

**Acceptance criteria**:
- Credit piggybacked on ACK frames.
- Publishers respect credit per class.
- Credit recovers as receiver drains.

**Unit tests**:
- `TestCredit_AdvertisedOnAck` — ACK contains credit info.
- `TestCredit_PublisherRespects` — Publisher slows when credit low.

**Integration tests**:
- `TestCredit_DynamicAdaptation` — Receiver capacity changes; publisher
  adapts.

**End-to-end tests**:
- `TestCreditE2E_NoSlowConsumerOOM` — Slow consumer + fast publisher;
  no OOM.

---

#### 10.3 — Retry budgets

**Description**: Per (publisher, subject) retry ratio limit per §10.3.

**Acceptance criteria**:
- Retry ratio computed over sliding window.
- Exceeded budget → `ErrRetryBudgetExceeded` for new retries.
- Originals still flow.
- Budget resets as ratio falls.

**Unit tests**:
- `TestRetryBudget_BlocksAtThreshold` — Retry ratio > 20% blocks.
- `TestRetryBudget_RecoversAsRatioDrops` — Ratio falls; retries
  resume.

**Integration tests**:
- `TestRetryBudget_PreventsStorm` — Simulated outage; retry storm
  prevented.

**End-to-end tests**:
- `TestRetryBudgetE2E_PartialOutage` — DC partial outage; retries
  bounded; cluster stable.

---

#### 10.4 — Circuit breakers

**Description**: Per (subject, consumer) NACK-rate-driven circuit per §10.4.

**Acceptance criteria**:
- States: CLOSED, OPEN, HALF_OPEN.
- Transition triggers per thresholds.
- HALF_OPEN test probe before close.

**Unit tests**:
- `TestCircuitBreaker_OpensAtThreshold` — NACK rate > threshold opens.
- `TestCircuitBreaker_HalfOpenProbes` — Test probe after duration.
- `TestCircuitBreaker_ClosesOnSuccess` — Successful test closes.

**Integration tests**:
- `TestCircuitBreaker_PreventsCascade` — Failing consumer doesn't
  starve queue indefinitely.

**End-to-end tests**:
- `TestCircuitBreakerE2E_ConsumerRecovery` — Consumer fails then
  recovers; circuit cycles correctly.

---

#### 10.5 — Best-effort tier

**Description**: Background class messages: no fsync, drop under load.

**Acceptance criteria**:
- No fsync on publish.
- Dropped first under load shed.
- No ack expectation.

**Unit tests**:
- `TestBestEffort_NoFsync` — Publish background; no fsync syscall.
- `TestBestEffort_DroppedFirst` — Under load shed, background dropped
  before standard.

**Integration tests**:
- `TestBestEffort_HighThroughput` — Sustained 1M msg/sec on
  background; no system stress.

**End-to-end tests**: covered by generic load tests.

---

### Phase 11 — Higher-Level Primitives

#### 11.1 — KV state machine

**Description**: Subjects with `kind=kv` per §11.1.

**Acceptance criteria**:
- `Put`, `Get`, `GetAt(hlc)`, `Watch`, `Delete`.
- LWW with HLC tiebreak.
- Historical reads at any HLC frontier.
- Watch is cursor-based subscription.

**Unit tests**:
- `TestKV_PutGet` — Round-trip.
- `TestKV_LWW` — Conflicting writes resolve by HLC.
- `TestKV_GetAt_Historical` — Historical reads return correct values.
- `TestKV_Watch_Delivers` — Watch delivers updates.
- `TestKV_Delete` — Tombstone.

**Integration tests**:
- `TestKV_ReplicaConsistency` — All replicas have same KV state.

**End-to-end tests**:
- `TestKVE2E_HighWriteRate` — Sustained KV writes; consistency
  preserved.

---

#### 11.2 — Object store

**Description**: Subjects with `kind=object` per §11.2.

**Acceptance criteria**:
- Content-defined chunking.
- Chunks dedup'd by hash with refcount.
- Object_id is BLAKE3 of manifest.
- Streamed read for large objects.
- GC: chunks with refcount 0 removed.

**Unit tests**:
- `TestObject_PutGet` — Round-trip.
- `TestObject_DedupChunks` — Same content stored once.
- `TestObject_Streaming` — Large object streamed.
- `TestObject_GC` — Deleted object's chunks GC'd.

**Integration tests**:
- `TestObject_PartialRefcounting` — Two objects share chunks; delete
  one; shared chunks remain.

**End-to-end tests**:
- `TestObjectE2E_GBLargeObject` — 1GB object stored and retrieved.

---

#### 11.3 — Claims board state machine

**Description**: Replace `core/claims/board_durable.go` with substrate
subject + state machine.

**Acceptance criteria**:
- Operations: claim.issued, claim.accepted, claim.rejected,
  testament.submitted, artifact.published, claim.remediated.
- All board operations go through substrate.
- Existing `docs/CLAIMS.md` semantics preserved.
- Existing claims-board tests pass against substrate-backed implementation.

**Unit tests**: existing `core/claims/*_test.go` re-targeted.

**Integration tests**:
- `TestClaimsBoard_PostActionFlow` — Full action lifecycle.
- `TestClaimsBoard_RemediationFlow` — Rejection + remediation.

**End-to-end tests**:
- `TestClaimsBoardE2E_FullSession` — Session with realistic claim
  flows; correct outcomes.

---

#### 11.4 — Forest event ledger projector

**Description**: `core/forest/projector.go` becomes substrate consumer.

**Acceptance criteria**:
- Forest events flow through substrate.
- Projector consumes and updates forest state.
- Branch packet retrieval semantics unchanged.
- Existing `core/forest/*_test.go` pass.

**Unit tests**: existing forest tests re-targeted.

**Integration tests**:
- `TestForest_ProjectorOnSubstrate` — Realistic forest workload;
  branch state coherent.

**End-to-end tests**:
- `TestForestE2E_SessionLifecycle` — Forest events across session;
  retrieval correct.

---

#### 11.5 — Fabric lens consumer

**Description**: Fabric lenses become substrate consumers reading multiple
subjects.

**Acceptance criteria**:
- Existing `core/fabric/` skill behavior preserved.
- Ambient envelope rendering unchanged.
- Performance: envelope render time within current budget.

**Unit tests**: existing fabric tests re-targeted.

**Integration tests**:
- `TestFabric_AmbientEnvelopeOnSubstrate` — Realistic agent activity;
  envelope contents correct.

**End-to-end tests**:
- `TestFabricE2E_MultiAgentSession` — Multi-agent session; envelopes
  reflect cross-agent activity.

---

#### 11.6 — VFS commit log

**Description**: Replace `core/versioning/commit_queue.go` ControlWAL
with substrate subject.

**Acceptance criteria**:
- All operations: pipeline.begun, pipeline.merged, commit.accepted,
  commit.rejected, commit.superseded, commit.flushed.
- Existing `core/versioning/*_test.go` pass.
- Commit resolver becomes substrate consumer.

**Unit tests**: existing versioning tests re-targeted.

**Integration tests**:
- `TestVFSCommitLog_FullPipelineLifecycle` — Begin, merge, accept,
  flush.

**End-to-end tests**:
- `TestVFSCommitLogE2E_ConcurrentPipelines` — Many concurrent pipelines;
  commit log coherent.

---

#### 11.7 — Authority broadcast

**Description**: `sylk://global/authority/v1` for cluster-wide authority
updates.

**Acceptance criteria**:
- All nodes subscribe.
- Updates propagate within seconds.
- Authority predicates use latest received.
- Conflicts resolved by HLC.

**Unit tests**:
- `TestAuthorityBroadcast_Propagation` — Update; all nodes apply.
- `TestAuthorityBroadcast_RevocationApplied` — Revoke; future publishes
  rejected.

**Integration tests**:
- `TestAuthority_RevokeWhileInflight` — Revoke during publish; either
  succeeds or fails cleanly.

**End-to-end tests**:
- `TestAuthorityE2E_ClusterWideRevocation` — Revoke across cluster;
  no node accepts revoked publishes.

---

#### 11.8 — View projections (server-side rendering)

**Description**: Aggregator consumes raw subjects, publishes rendered view
subjects per §18.3.

**Acceptance criteria**:
- View subjects delta-encoded.
- View bandwidth ~10x lower than raw subjects.
- Subscribe/unsubscribe respects viewport.
- Aggregator restart resumes seamlessly.

**Unit tests**:
- `TestViewProjection_DeltaEncoded` — Subsequent views are diffs.
- `TestViewProjection_SubscribeBandwidth` — View bandwidth bounded.

**Integration tests**:
- `TestViewProjection_AggregatorRestart` — Aggregator restarts; views
  continue.

**End-to-end tests**:
- `TestViewProjectionE2E_TUIBandwidth` — Realistic TUI subscription;
  bandwidth meets target.

---

### Phase 12 — Observability

#### 12.1 — Time-travel state API

**Description**: `Substrate.StateAt(subject, hlc) → state` per §12.1.

**Acceptance criteria**:
- Returns state at any HLC frontier within retention.
- Cost: O(entries since nearest snapshot).
- Same state machine code path as live.

**Unit tests**:
- `TestStateAt_AtSnapshot` — Equal to snapshot's state.
- `TestStateAt_BetweenSnapshots` — Equal to live state at that HLC.
- `TestStateAt_BeforeRetentionLimit` — Pre-retention HLC returns
  `ErrOutsideRetention`.

**Integration tests**:
- `TestStateAt_LiveAndHistorical` — State at live HLC matches current
  live state.

**End-to-end tests**:
- `TestStateAtE2E_DebugScenario` — Reproduce historical bug via
  StateAt.

---

#### 12.2 — Causal cone query

**Description**: `CausalCone(entry, depth)` and `CausalDescendants(entry,
depth)` per §12.2.

**Acceptance criteria**:
- Walks parent index; terminates at depth or no parents.
- Bounded memory: depth × fanout limited.
- Returns DAG, not tree (preserves shared ancestors).

**Unit tests**:
- `TestCausalCone_Linear` — Linear chain; full ancestry returned.
- `TestCausalCone_Diamond` — Diamond pattern; shared ancestor returned
  once.
- `TestCausalCone_DepthLimited` — Depth cap respected.

**Integration tests**:
- `TestCausalCone_RealClaimRejection` — Cone walk from rejection to
  originating claim returns expected ancestors.

**End-to-end tests**:
- `TestCausalConeE2E_DebugQuery` — Realistic debug query returns
  meaningful causal ancestry.

---

#### 12.3 — Provable audit

**Description**: Merkle path from entry to leader-signed snapshot per §12.3.

**Acceptance criteria**:
- `AuditProof(entry) → MerklePath, LeaderSig` returns proof.
- Auditor verifies given cluster CA + term history.
- Compromise of leader cannot forge past entries (signature is term-bound).

**Unit tests**:
- `TestAudit_ValidProof` — Proof verifies.
- `TestAudit_TamperedEntryRejected` — Tampered entry's proof fails.
- `TestAudit_ExpiredTermDoubleCheck` — Term verified against history.

**Integration tests**:
- `TestAudit_AcrossLeaderChange` — Proof from term T verified after
  leader change.

**End-to-end tests**:
- `TestAuditE2E_FullHistory` — 1M entries; sample 1K; all verify.

---

#### 12.4 — Prometheus metrics

**Description**: Per §12.4.

**Acceptance criteria**:
- All listed metrics exported.
- Cardinality bounded (no per-message labels).
- Scrape latency <100ms for typical cluster.

**Unit tests**:
- `TestMetrics_AllExported` — Endpoint exposes all listed metrics.
- `TestMetrics_CardinalityBounded` — No metric has >10K series.

**Integration tests**:
- `TestMetrics_UnderLoad` — Metrics scraping doesn't impact cluster
  performance.

**End-to-end tests**:
- `TestMetricsE2E_Dashboard` — Dashboard queries return expected values.

---

### Phase 13 — Migration

Convert existing scattered logs to substrate subjects. Riskiest phase.

#### 13.1 — ControlWAL → VFS commit subject

**Description**: Existing `commit_queue.go` and `copy_retention.go` ControlWAL
back-fills into substrate subjects via dual-write.

**Acceptance criteria**:
- Dual-write window: writes go to both old WAL and substrate.
- Shadow read verification: every old-WAL read also performed against
  substrate; mismatch logged for investigation.
- Cutover gated by zero mismatches over verification window (default 7
  days).
- Rollback path: revert to old WAL if mismatches detected.

**Unit tests**:
- `TestVFSCommitMigration_DualWriteAgreement` — Both stores have same
  state.
- `TestVFSCommitMigration_ShadowReadDetectsMismatch` — Injected mismatch
  detected.

**Integration tests**:
- `TestVFSCommitMigration_LongRunningParity` — 24h dual-write; zero
  mismatches.

**End-to-end tests**:
- `TestVFSCommitMigrationE2E_Cutover` — Full cutover sequence; existing
  tests pass against substrate-only.

---

#### 13.2 — Claims board → substrate

**Description**: Per 11.3.

**Acceptance criteria**: as 13.1 but for claims.

**Tests**: corresponding to 13.1.

---

#### 13.3 — Forest ledger → substrate

**Description**: Per 11.4.

**Acceptance criteria**: as 13.1.

**Tests**: corresponding to 13.1.

---

#### 13.4 — Activity store → derived view

**Description**: `core/activity/activitystore` becomes a derived view over
substrate subjects.

**Acceptance criteria**:
- All existing lens queries return same results as before.
- Performance: queries within 2x of current.
- No data loss during migration.

**Unit tests**: existing activity tests retargeted.

**Integration tests**:
- `TestActivityMigration_LensParity` — All lens queries match across
  old and new.

**End-to-end tests**:
- `TestActivityMigrationE2E_Cutover` — Full session under
  substrate-backed activity.

---

#### 13.5 — Agent log → substrate

**Description**: `core/agentlog/` log becomes substrate subject.

**Acceptance criteria**: as 13.1.

**Tests**: corresponding to 13.1.

---

#### 13.6 — Cutover gates

**Description**: Operational gates before cutover.

**Acceptance criteria**:
- 7 days zero mismatches.
- All existing tests pass against substrate-backed.
- Performance regression ≤ 10% for any operation.
- Rollback path tested.

**Tests**: ops-driven, not unit-test-driven.

---

### Phase 14 — Remote Mode Production

#### 14.1 — Cluster bootstrap

**Description**: Cluster-genesis flow: deterministic operator group
formation; subsequent node joins.

**Acceptance criteria**:
- 3 nodes started simultaneously bootstrap successfully.
- 4th node joins existing cluster.
- Genesis is deterministic: same node IDs → same bootstrap outcome.

**Unit tests**:
- `TestBootstrap_GenesisDeterministic` — Same node IDs → same operator
  group.
- `TestBootstrap_LateJoin` — 4th node joins; topology updates.

**Integration tests**:
- `TestBootstrap_3DCGenesis` — 3 DCs start; cluster forms across DCs.

**End-to-end tests**:
- `TestBootstrapE2E_RealCluster` — 9-node real-process cluster
  bootstraps successfully.

---

#### 14.2 — DNS-SD discovery

**Description**: Cluster discoverable via DNS-SD; sylk client uses it.

**Acceptance criteria**:
- TXT records expose gateway endpoints.
- Coordinates published via TXT.
- Rotation: stale records expire.

**Unit tests**:
- `TestDNSSD_RecordFormat` — TXT records well-formed.

**Integration tests**:
- `TestDNSSD_DiscoverGateways` — Mock DNS server returns records;
  client discovers.

**End-to-end tests**:
- `TestDNSSDE2E_RealDNS` — Real DNS server with real records;
  end-to-end discovery.

---

#### 14.3 — Gateway selection

**Description**: Client picks closest gateway by Vivaldi.

**Acceptance criteria**:
- Closest gateway selected.
- Failover to next-closest on failure.

**Unit tests**:
- `TestGatewaySelection_ClosestPicked` — Synthetic coords; closest
  picked.
- `TestGatewaySelection_FailoverToNext` — Closest fails; next picked.

**Integration tests**:
- `TestGatewaySelection_AcrossDC` — Multi-DC; appropriate gateway
  chosen.

**End-to-end tests**:
- `TestGatewaySelectionE2E_LatencyMeasured` — Measured latency to
  selected gateway < 90th percentile of all gateways.

---

#### 14.4 — OIDC SVID issuance

**Description**: First-login flow: OIDC → cluster issues SVID → cached
locally.

**Acceptance criteria**:
- OIDC providers configurable.
- SVID cached securely (OS keychain).
- Refresh on expiry.
- Revocation respected.

**Unit tests**:
- `TestSVIDIssuance_OIDCFlow` — Mock OIDC; SVID issued with correct
  claims.
- `TestSVIDIssuance_RefreshOnExpiry` — Near-expiry triggers refresh.

**Integration tests**:
- `TestSVIDIssuance_KeychainStorage` — SVID stored and retrieved from
  OS keychain.

**End-to-end tests**:
- `TestSVIDIssuanceE2E_LoginToConnect` — Full first-login → SVID →
  connect → publish flow.

---

#### 14.5 — Local cache layer

**Description**: TUI-side bounded substrate per §18.6.

**Acceptance criteria**:
- Same code as embedded mode storage.
- Bounded retention per config.
- Cursor preserved across restarts.
- Outbox queues offline writes.

**Unit tests**:
- `TestLocalCache_BoundedRetention` — Cache size bounded.
- `TestLocalCache_CursorPersisted` — Restart preserves cursor.
- `TestLocalCache_OutboxReplay` — Offline writes queued; replay on
  reconnect.

**Integration tests**:
- `TestLocalCache_DisconnectReconnect` — Disconnect; cache renders;
  reconnect catches up.

**End-to-end tests**:
- `TestLocalCacheE2E_FlakyNetwork` — TUI under flaky network; user
  sees no errors.

---

#### 14.6 — Cursor sync at scale

**Description**: Cold-start cursor sync delivers delta efficiently.

**Acceptance criteria**:
- Cold start: <500ms for typical session delta.
- Warm restart: <100ms.
- Delta size proportional to actual changes.

**Unit tests**:
- `TestCursorSync_DeltaSize` — Delta proportional to changes since
  cursor.

**Integration tests**:
- `TestCursorSync_ColdWarm` — Cold and warm paths meet timing budgets.

**End-to-end tests**:
- `TestCursorSyncE2E_DailyUserPattern` — Daily disconnect/reconnect
  cycles; delta consistently small.

---

### Phase 15 — Hardening

#### 15.1 — Chaos testing

**Description**: Continuous chaos: random kills, partitions, slow nodes,
GC pauses, clock skew, disk corruption.

**Acceptance criteria**:
- Run continuously for 1 week.
- All invariants hold (no data loss for Critical, no double-execution
  for idempotent, no split-brain).
- Cluster always converges to consistent state on heal.

**Tests**:
- `ChaosKillRandomNode` — Random node killed every 1-5min.
- `ChaosPartitionRandom` — Random partition every 5-30min.
- `ChaosSlowNode` — Random node slowed 100-1000x for 1-5min.
- `ChaosGCPause` — Inject 1-10s GC pauses.
- `ChaosClockSkew` — Inject ±500ms clock skew.
- `ChaosDiskCorruption` — Flip random bit in segment file.
- `ChaosNetworkDelay` — Random per-link delay 0-200ms.

Each chaos test runs ≥1 hour and verifies invariants throughout.

---

#### 15.2 — Performance benchmarks

**Description**: Performance regression detection.

**Acceptance criteria** (initial targets, refined as system matures):
- Embedded publish (Critical): p50 <2ms, p99 <10ms.
- Embedded publish (Standard): p50 <500µs, p99 <5ms.
- Cluster publish (Critical, intra-DC): p50 <10ms, p99 <50ms.
- Cluster publish (Critical, cross-DC): p50 <100ms, p99 <500ms.
- Subscribe throughput: >100K msg/sec per consumer.
- Boot time: cold <500ms, warm <100ms (embedded).
- Reconnect time: <2s 95th percentile.

**Tests**:
- `BenchmarkEmbedded_PublishCritical`
- `BenchmarkEmbedded_PublishStandard`
- `BenchmarkCluster_PublishCriticalIntraDC`
- `BenchmarkCluster_PublishCriticalCrossDC`
- `BenchmarkSubscribe_Throughput`
- `BenchmarkBoot_Cold`
- `BenchmarkBoot_Warm`
- `BenchmarkReconnect_GatewayDeath`

CI fails if any regression >10% from baseline.

---

#### 15.3 — Long-running stability

**Description**: Multi-day continuous run.

**Acceptance criteria**:
- 1-week run with realistic load: no crash, no goroutine leak, no
  unbounded growth.
- Memory stable.
- Disk usage matches retention policy.

**Tests**:
- `LongRunning_OneWeek` — Continuous 7-day run.
- Periodic snapshots of memory profile, goroutine counts, disk usage.
- Verifies no metric grows unboundedly.

---

#### 15.4 — Security audit

**Description**: External / internal security audit.

**Acceptance criteria**:
- mTLS verified end-to-end.
- SVID validation prevents spoofing.
- Authority predicates enforced (negative tests for every capability
  type).
- No path-traversal in subject URIs.
- No DoS amplification via SWIM/Raft.
- Crypto choices reviewed.

**Tests**:
- `TestSecurity_SVIDSpoof_Rejected` — Forged SVID rejected.
- `TestSecurity_AuthorityBypass_Rejected` — Operations without
  authority rejected.
- `TestSecurity_PathTraversal_Rejected` — `../`-style URIs rejected.
- `TestSecurity_SWIMAmplification_Bounded` — SWIM probe traffic
  bounded.
- `TestSecurity_RaftRPCFlooding_Bounded` — RPC rate limiting effective.

---

#### 15.5 — Operational runbooks

**Description**: Documented procedures for operators.

**Acceptance criteria**:
- Runbook for each common scenario: DC outage, node replacement,
  capacity expansion, rollback, retention change.
- Each runbook tested in staging.
- Each runbook has expected duration and success criteria.

**Procedures documented**:
- `docs/RUNBOOK_DC_OUTAGE.md`
- `docs/RUNBOOK_NODE_REPLACEMENT.md`
- `docs/RUNBOOK_CAPACITY_EXPANSION.md`
- `docs/RUNBOOK_ROLLBACK.md`
- `docs/RUNBOOK_RETENTION_CHANGE.md`

---

### Phase 16 — Continuous Operation

Post-launch ongoing operation.

#### 16.1 — Continuous chaos in production

Periodic chaos injection in non-critical regions to keep failure handling
sharp.

#### 16.2 — Performance regression prevention

CI gating on benchmark suite. PRs that regress >10% blocked.

#### 16.3 — Capacity planning

Quarterly review: trend analysis on growth, headroom for next quarter.

#### 16.4 — Schema evolution

Process for registering new subject schemas: review, register, deprecate
old, remove after retention.

---

### Phase 17 — Trust and Adversarial Robustness

Cryptographic accountability, optional BFT, equivocation detection,
attested execution. Per §19.

**Phase implementation overview**: Phase 17 introduces *signed-everything*
across the existing Raft + SWIM substrate. Signing keys are held in two
tiers: per-node static SVID keys (already provisioned by Phase 0) and
per-Raft-term ephemeral keys (new). The phase adds optional BFT
consensus as a second engine alongside Raft, swappable per subject.
Common dependencies: `crypto/ed25519`, `cloudflare/circl` (BLS for
threshold signatures), `zeebo/blake3`, SPIFFE / SPIRE attestation
plugins, platform-specific attestation libraries.

#### 17.1 — Cryptographic accountability layer

**Description**: Per §19.1. Every Raft entry signed; every snapshot
signed; every gossip frame signed and HLC-chained.

**Implementation approach**:
- Library: `crypto/ed25519` for sign/verify; `zeebo/blake3` for hashes.
- Per-leader-term keypair: at term start, leader generates ephemeral
  Ed25519 keypair; commits the public key as the first entry of the
  term ("term-genesis" entry). Term-genesis itself is signed by the
  leader's static SVID key, so the chain bootstraps from cluster CA.
- Sign over `(term, index, parent_hash, body_hash)` — 64-byte fixed
  input; Ed25519 sign ≈50µs on commodity hardware.
- Storage: extend `consensus/raft/types.go` Entry struct with
  `LeaderSig [64]byte`. Term pubkey cached per-term in an LRU
  (capacity 10K terms) keyed by `(group_id, term)`.
- Snapshot signing: Ed25519 sign over snapshot Merkle root, key from
  current term.
- Gossip frame signing: per-node static SVID key; chain is
  `(prev_frame_hash, hlc, body_hash)` signed; tail of chain stored
  per-peer with bounded retention.
- Divergence proof: tuple `(entry_a, sig_a, entry_b, sig_b)` where
  `entry_a.term == entry_b.term && entry_a.index == entry_b.index &&
  entry_a.body_hash != entry_b.body_hash` and both sigs verify under
  the same term pubkey. Portable: any node holding the term pubkey
  validates without trusting either source.
- Forward secrecy: term key destroyed at term end (overwrite + zero).
  A compromised current-term key cannot forge past terms.
- Code path: `core/substrate/consensus/accountability.go` (~700 LOC) +
  modifications to entry/snapshot apply paths.
- Hard parts: key-rotation race during leader transition. Solved by
  treating term-genesis as a special Raft entry that itself goes
  through the existing pre-vote → vote → first-AppendEntries flow,
  with the static SVID key as the bootstrap signer.

**Acceptance criteria**:
- Leader signs `(term, index, parent_index, body_hash)` over Ed25519
  with term-bound key for every committed entry.
- Term-genesis entry committed before any leader-issued entries; its
  signature uses the leader's static SVID key.
- Snapshot signed over Merkle root with current-term leader key.
- Gossip frame signed by originator's SVID key, chained to prior frame
  by HLC.
- Verification cost: <100µs per entry on commodity hardware.
- Batch verification used for snapshot install and bulk replay (≥5x
  speedup vs sequential).
- Any honest replica can verify any entry's signature chain back to
  cluster genesis without external dependencies.
- Divergence between two signed entries with same `(term, index)`
  produces a portable proof of misbehavior verifiable without trusting
  any specific replica.
- Term key destroyed at term end (forward-secrecy: no past-term forgery
  possible from current-term key compromise).
- Memory bound: term pubkey cache ≤ 10K entries (LRU evicts oldest
  terms).
- Allocation cost: zero allocations on Sign / Verify hot paths beyond
  what `crypto/ed25519` requires internally.
- All accountability operations are bounded-CPU: a malicious peer
  cannot induce O(n²) verification cost by replaying conflicting
  proofs.

**Unit tests**:
- `TestAccountability_EntrySignatureValidates` — Signed entries verify
  with parsed term pubkey.
- `TestAccountability_DivergenceProof` — Two conflicting signed entries
  generate a portable proof; proof verifies independently.
- `TestAccountability_SnapshotSig` — Snapshot signed with current-term
  key; verifies against root.
- `TestAccountability_GossipChainLinks` — Gossip frames chain correctly;
  HLC monotonicity preserved across chain.
- `TestAccountability_TermBoundKey` — Key from term T fails to verify
  entry from term T+1.
- `TestAccountability_BatchVerify` — Batch of N=1024 signatures verifies
  in ≤ 1.5x time of a single verify (using Ed25519 batch verify).
- `TestAccountability_TermKeyDestroyed` — After term end, term key
  bytes are zeroed in memory (introspect via `unsafe.Pointer` test).
- `TestAccountability_KeyCacheLRUEviction` — Inserting > 10K terms
  evicts oldest LRU; cache size bounded.
- `TestAccountability_TermGenesisSignedByStaticSVID` — Term-genesis
  signature verifies under leader's SVID key, not term key.
- `TestAccountability_AllocsPerSign` — `testing.AllocsPerRun` ≤ 1 for
  Sign hot path.

**Integration tests**:
- `TestAccountability_LeaderForgeryDetected` — Faulty leader producing
  divergent log; honest replica generates and propagates proof within
  one heartbeat cycle (≤ 50ms).
- `TestAccountability_AcrossSnapshot` — Chain remains verifiable across
  snapshot install on a new follower; auditor can verify entries from
  before the snapshot via the snapshot's signed root.
- `TestAccountability_LeaderTransition` — Mid-term leader transition;
  new term's signatures accepted; old term's signatures still verify
  for old entries; no signature-validity gap.
- `TestAccountability_KeyRotationRace` — Term-genesis arrives at a
  follower before regular entries from that term; follower buffers
  and validates in correct order.
- `TestAccountability_MultiGroupCoexistence` — 100 Raft groups; each
  with independent term keys; no cross-group key confusion.

**End-to-end tests**:
- `TestAccountabilityE2E_AuditTrail` — Realistic 9-node cluster running
  for 1 hour with mixed-class load; held-out auditor verifies complete
  cryptographic chain end-to-end.
- `TestAccountabilityE2E_ByzantineLeader` — Inject Byzantine leader
  behavior (lying about commits, equivocating on `(term, index)`,
  forging body hashes); honest replicas detect and isolate within 5s;
  proof artifact persists across cluster restarts.
- `TestAccountabilityE2E_LongRunningChain` — 7-day continuous run;
  chain integrity verified hourly via held-out auditor; no false
  positives, no skipped verifications.

**Race condition tests**:
- `TestAccountability_ConcurrentSign` — `go test -race` with 100
  concurrent Sign calls; no race; signatures all valid.
- `TestAccountability_ConcurrentVerify` — `go test -race` with 1K
  concurrent Verify calls against shared cache; no race; all results
  consistent.
- `TestAccountability_KeyCacheConcurrent` — `go test -race` with mixed
  insert/lookup/evict at 10K ops/sec; cache invariants preserved
  (size bound, no double-free, no torn reads).
- `TestAccountability_ChainExtensionConcurrent` — Multiple goroutines
  extending the gossip chain with HLC tiebreak; chain remains a valid
  linear chain when serialized.
- `TestAccountability_TermSwitchRace` — Term ends concurrently with
  Sign/Verify calls; pending operations either complete cleanly under
  old key or fail with `ErrTermEnded`; no torn writes to chain.
- `TestAccountability_MemoryOrderingTermGenesis` — Term-key publish +
  first-entry apply ordering; happens-before preserved across CPU
  memory model reorderings (verified with `sync/atomic` discipline
  on x86 and ARM64 builders).

**Negative / non-happy path tests**:
- `TestAccountability_TamperedHeader_Rejected` — Modified entry header
  field fails verification; specific error returned (`ErrSigInvalid`).
- `TestAccountability_TamperedBody_Rejected` — Body bit flip fails
  verification (because body_hash mismatch).
- `TestAccountability_TamperedSig_Rejected` — Modified signature fails
  with `ErrSigInvalid`.
- `TestAccountability_WrongTermKey_Rejected` — Entry signed with term T
  key but stamped as term T+1 fails with `ErrTermMismatch`.
- `TestAccountability_ExpiredTermKey_NoVerify` — After term key
  destroyed, attempting to sign returns `ErrTermEnded`; verifying
  past-term entries with cached pubkey still works.
- `TestAccountability_ReplayedTermGenesis_Rejected` — Replayed
  term-genesis from a prior partition rejected via pre-vote (term
  number too low).
- `TestAccountability_MalformedDivergenceProof_Rejected` — Proof with
  one valid + one forged signature rejected; proof with non-matching
  `(term, index)` rejected; proof with same body_hash returns
  `ErrNotDivergent`.
- `TestAccountability_TruncatedSig_Rejected` — Signature shorter than
  64 bytes returns `ErrSigMalformed`, not panic.
- `TestAccountability_GossipChainGap_Rejected` — Missing intermediate
  frame in gossip chain detected; receiver requests gap-fill rather
  than accepting.
- `TestAccountability_SignKeyRevoked_Rejected` — Static SVID key
  revoked mid-term; new term-genesis signing fails; cluster handles
  gracefully via authority broadcast.
- `TestAccountability_ClockSkewedTermGenesis_Rejected` — Term-genesis
  with HLC beyond drift bound rejected by HLC validator before
  signature check (cheap rejection first).
- `TestAccountability_ZeroLengthBody_HandledCorrectly` — Empty-body
  entries sign / verify correctly (body_hash = blake3 of empty).
- `TestAccountability_PubkeyCacheUnderMemoryPressure` — Cache evicts
  under memory pressure signal (§29.1) without losing in-flight
  verifications.
- `TestAccountability_BatchWithSomeInvalid` — Batch verify of N
  signatures where M are invalid; returns N-M valid + M errors;
  doesn't fail-fast on first invalid.

---

#### 17.2 — Optional BFT subjects (HotStuff)

**Description**: Per §19.2. Subjects with `consensus=hotstuff` use BFT
state machine.

**Implementation approach**:
- Algorithm: HotStuff (Yin et al., PODC'19) — leader-based BFT with
  3-phase chained commit and linear authenticator complexity.
- Library: `cloudflare/circl` for BLS12-381 threshold signatures
  (n=3f+1 replicas, t=2f+1 threshold).
- New consensus engine: `core/substrate/consensus/hotstuff/` parallel
  to `core/substrate/consensus/raft/`. Both implement `consensus.Engine`
  interface (`Propose`, `Apply`, `Snapshot`, `Subscribe`, `Status`).
- Subject registration: `consensus = raft | hotstuff` field in
  `subject.Schema`; selected at namespace-group creation time.
- Wire format: existing 56-byte header; new `MsgType` values
  `BFT_PREPARE`, `BFT_PRECOMMIT`, `BFT_COMMIT`, `BFT_VIEWCHANGE`,
  `BFT_NEWVIEW`. Body carries signed messages with threshold-sig
  partial shares.
- Storage: HotStuff blocks stored in same causal Merkle DAG as Raft
  entries; one HotStuff block = one substrate entry.
- View change: timeout-driven; new leader collects 2f+1 view-change
  votes, broadcasts NEWVIEW with highest QC, resumes from there.
- Code path: `core/substrate/consensus/hotstuff/` (~3500 LOC).
- Hard parts: ensuring causal DAG semantics (§7) map cleanly onto
  HotStuff's chained block view; solved by treating each HotStuff
  block as a substrate entry whose parent edges are the QC chain.

**Acceptance criteria**:
- 3f+1 replicas required for BFT subjects; configurable f.
- BLS12-381 threshold signatures: 2f+1 partial shares aggregate into
  one full signature.
- Same wire format / cursor / Merkle DAG abstractions as Raft subjects.
- Performance: BFT commit latency 2-3x Raft equivalent (4 message
  delays vs 2).
- Throughput: BFT subject sustains ≥ 5K msg/sec/group on commodity
  hardware (lower than Raft, expected).
- BFT and Raft subjects coexist on same cluster, same node, same
  network connection.
- View change completes within bounded time (≤ 3 × view timeout).
- Safety: no two committed blocks at same height across Byzantine
  faults up to f.
- Liveness: with 2f+1 honest replicas + bounded async network,
  progress eventually made.
- Threshold key generation: distributed (no trusted dealer), verifiable
  via `Pedersen DKG` or equivalent.
- BFT engine respects same authority predicates, same dedupe, same
  delivery semantics as Raft engine.

**Unit tests**:
- `TestBFT_LivenessTolerates_f` — n=4, f=1; one Byzantine; cluster
  makes progress.
- `TestBFT_LivenessTolerates_2fHonest` — n=7, f=2; 5 honest required
  for liveness.
- `TestBFT_AgreementUnderEquivocation` — Equivocating leader detected;
  view-changed within timeout.
- `TestBFT_ThresholdSig_AggregateValid` — 2f+1 partial shares aggregate
  to a valid full signature.
- `TestBFT_ThresholdSig_FewerThanThresholdInvalid` — < 2f+1 shares
  aggregate to invalid signature.
- `TestBFT_SafetyProperty` (property test) — Random schedules with
  random Byzantine subset of size ≤ f; no two committed blocks at
  same height.
- `TestBFT_ChainedCommit_PrecommitAfterPrepare` — PRECOMMIT only after
  2f+1 PREPARE votes.
- `TestBFT_ViewChangeOnTimeout` — Leader silent; view change initiated
  after timeout; new leader proposes.
- `TestBFT_DKG_NoTrustedDealer` — Distributed key generation produces
  correct threshold key shares without any single party knowing the
  full key.

**Integration tests**:
- `TestBFT_AuthoritySubjectE2E` — Authority broadcast on BFT;
  surviving f Byzantine replicas; capabilities propagate correctly.
- `TestBFT_RaftCoexistence` — Same cluster runs Raft and BFT subjects;
  no interference; throughput targets for each preserved.
- `TestBFT_LargeReplicaCount` — n=10, f=3; commit latency within
  budget.
- `TestBFT_ViewChangeUnderLoad` — Continuous traffic during view
  change; no committed entries lost.
- `TestBFT_KeyResharing` — Member transition (add/remove replica);
  threshold keys reshared without downtime.

**End-to-end tests**:
- `TestBFTE2E_FederatedPolicy` — Federation control plane (§20.1) on
  BFT; federation peer compromised; honest peers maintain consistent
  view.
- `TestBFTE2E_LongRunningStability` — 24-hour BFT subject under
  realistic load; no safety violations; throughput meets target.

**Race condition tests**:
- `TestBFT_ConcurrentProposals` — Multiple replicas proposing
  concurrently; pacemaker serializes; no double-commit.
- `TestBFT_PartialShareCollection_Concurrent` — `go test -race`
  with concurrent share collection from multiple peers; aggregate
  signature valid.
- `TestBFT_ViewChangeRace` — View change initiated by multiple
  replicas simultaneously; deterministic resolution by leader-rotation
  rule.
- `TestBFT_ApplyConcurrentReadIndex` — Concurrent commits and
  read-index queries; reads observe consistent state.
- `TestBFT_NetworkReorder` — Random message reordering; safety holds
  under property-based testing.

**Negative / non-happy path tests**:
- `TestBFT_FByzantineUnderLimit_LivenessHolds` — Inject f Byzantine;
  cluster makes progress.
- `TestBFT_F1ByzantineOverLimit_NoProgress` — Inject f+1 Byzantine;
  cluster does not make progress (expected); no safety violation.
- `TestBFT_MalformedThresholdShare_Rejected` — Replica submits
  malformed BLS share; rejected; replica counted as faulty.
- `TestBFT_ReplayedShare_Rejected` — Replayed PRECOMMIT share from
  prior view rejected.
- `TestBFT_LeaderEquivocation_Detected` — Leader proposes two
  conflicting blocks; view change triggered; equivocation proof
  recorded.
- `TestBFT_NetworkPartition_NoProgressNoSafetyViolation` —
  Symmetric partition; minority side cannot commit; majority side
  continues; on heal, minority catches up cleanly.
- `TestBFT_ViewChangeStorm_Bounded` — Repeated view changes from
  flapping leader; cluster falls back to extended timeout; no
  unbounded growth.
- `TestBFT_KeyShareCorruption_DetectedDuringDKG` — One participant's
  share corrupted during DKG; detected; DKG restarts.
- `TestBFT_QuorumLossUnderfLimits` — n=4, f=1, lose 2 honest replicas;
  liveness lost (expected); safety preserved.
- `TestBFT_RaftToBFTMigration_NotSupported` — Subject created as
  Raft cannot mid-life migrate to BFT; returns
  `ErrConsensusEngineImmutable`.
- `TestBFT_OversizedBlock_Rejected` — Block exceeding configured
  size limit rejected by replicas.
- `TestBFT_ClockSkewBeyondLimit_ReplicaExcluded` — Replica with
  excessive clock skew (§23.3) excluded from threshold-share quorum.

---

#### 17.3 — Provable non-equivocation

**Description**: Per §19.3. Signed gossip frames keyed by `(node, hlc)`;
duplicate `(node, hlc)` with different bodies → equivocation proof.

**Implementation approach**:
- Storage: pebble-backed gossip frame index keyed
  `(node_id_uint64, hlc.physical_uint64, hlc.logical_uint32) →
  (body_hash, sig)`. Bounded retention (default 1 hour) to cap
  storage.
- Detection: on receive of gossip frame, lookup; if existing entry
  with different body_hash and both sigs verify → equivocation proof.
- Proof = both signed frames as a tuple, broadcast to
  `sylk://global/security/equivocation/v1` (BFT subject from §17.2).
- Cluster-wide propagation: equivocating node added to a "FAULTY-by-
  proof" set in operator-group state machine; authority broadcast
  (§11.7) revokes all capabilities.
- Code path: `core/substrate/membership/equivocation.go` (~400 LOC).

**Acceptance criteria**:
- Gossip frames signed (§17.1) and indexed by `(node, hlc)`.
- Equivocation detected within one gossip cycle (≤ 1s typical).
- Equivocating node marked FAULTY cluster-wide regardless of SWIM φ
  score; SWIM cannot resurrect.
- Proof published to `sylk://global/security/equivocation/v1`; portable
  (verifiable by any holder of the offending node's pubkey).
- Index TTL ≤ 1 hour; older entries reaped.
- Index size bounded: O(unique (node, hlc) pairs in TTL window) with
  cap.
- False positive rate: zero (proof requires two valid signatures from
  same key).

**Unit tests**:
- `TestEquivocation_Detected` — Forced equivocation detected and
  proof generated.
- `TestEquivocation_ProofVerifies` — Proof verifies independently;
  any node with offending pubkey can validate.
- `TestEquivocation_FaultyMarking` — Faulty marking propagates via
  authority broadcast.
- `TestEquivocation_IndexTTLRespected` — Entries past TTL reaped.
- `TestEquivocation_IndexSizeBounded` — Cap enforced; oldest evicted.
- `TestEquivocation_SameBodyHashNotEquivocation` — Two frames with
  same `(node, hlc, body_hash)` is *not* equivocation (idempotent
  resend).
- `TestEquivocation_DifferentNodesSameHLC_NotEquivocation` — Different
  nodes with same HLC is normal (HLC tiebreak via node ID).

**Integration tests**:
- `TestEquivocation_FaultyNodeIsolated` — Equivocating node loses
  all capabilities cluster-wide; subsequent publishes rejected.
- `TestEquivocation_RecoveryAfterRevocation` — Equivocating node's
  SVID rotation creates new identity; fresh identity can rejoin.

**End-to-end tests**:
- `TestEquivocationE2E_ByzantinePeer` — Live cluster, injected
  equivocating peer; isolated within 1 gossip cycle.
- `TestEquivocationE2E_LongRunning` — 24-hour run; periodic injected
  equivocations; all detected.

**Race condition tests**:
- `TestEquivocation_ConcurrentLookup` — `go test -race` with 1K
  concurrent gossip-frame lookups; no race.
- `TestEquivocation_ConcurrentInsertSameKey` — Concurrent insert
  of same `(node, hlc)`; deterministic outcome (first wins or
  equivocation detected).
- `TestEquivocation_TTLReapDuringInsert` — Reaper running during
  insert; no torn writes; consistent state.
- `TestEquivocation_ProofPropagationConcurrent` — Multiple replicas
  detect same equivocation; only one proof published (dedup via
  proof content hash).

**Negative / non-happy path tests**:
- `TestEquivocation_ForgedProof_Rejected` — Proof with one forged
  signature rejected; both must verify.
- `TestEquivocation_MismatchedKey_Rejected` — Proof claims sig from
  node X but verifies under key Y; rejected.
- `TestEquivocation_DifferentHLC_Rejected` — Proof must have
  identical `(node, hlc)`; rejected otherwise.
- `TestEquivocation_IndexCorruption_RecoverGracefully` — Pebble
  index corruption; recover by rebuilding from gossip log; no
  panic.
- `TestEquivocation_DuplicateProofPropagation_Bounded` — Proof
  flooded by malicious node; rate-limited; cluster doesn't OOM.
- `TestEquivocation_RevocationRaceWithLegitimateOp` — Equivocation
  detected mid-publish; pending publish either completes (committed)
  or fails cleanly with `ErrIdentityRevoked`.
- `TestEquivocation_HLCSkewedFrame_RejectedBeforeIndexLookup` —
  Skewed-HLC frame rejected (§23.3) before reaching equivocation
  check (cheap rejection first).

---

#### 17.4 — Trusted execution attestation

**Description**: Per §19.4. SVID extensions carry attestation evidence;
join protocol verifies.

**Implementation approach**:
- Per-platform shim: SGX via `intel-sgx-dcap` (DCAP quote verification);
  AMD SEV-SNP via `psp-sev` driver and `go-sev-guest`; AWS Nitro via
  `aws-nitro-enclaves-sdk-go` (PCR-based attestation); Azure CC via
  Azure SDK.
- SVID X.509 extension OID: `1.3.6.1.4.1.<sylk_oid>.attestation`
  carries platform-specific evidence as DER-encoded structure.
- Verifier interface: `Verify(evidence, expected_pcrs) error`. SPIRE's
  existing `attestor.Plugin` framework extended with substrate-aware
  verifier.
- Join handshake: cluster join handshake includes attestation; operator
  group validates against approved measurement set.
- Refresh on SVID rotation: attestation re-verified each rotation.
- Code path: per-platform shim ~500 LOC each + unified verifier
  interface ~400 LOC; SPIRE plugin ~300 LOC.

**Acceptance criteria**:
- Optional cluster join policy: `attestation_required = true` enforced.
- Supports Intel SGX (DCAP), AMD SEV-SNP, AWS Nitro Enclaves, Azure
  Confidential Computing platform attestation formats.
- SVID X.509 extension carries attestation evidence as DER.
- Verification on cluster join; re-verification on SVID rotation.
- Mismatched / invalid / replayed attestation → join refused with
  specific error per failure mode.
- Verification cost ≤ 50ms per platform.
- Approved measurement set (PCRs / launch measurements) stored in
  operator group; updates require operator authority.
- Per-platform plugin loaded via Go plugin or compile-time selection;
  cluster supports heterogeneous platforms (some nodes SGX, some
  Nitro).

**Unit tests**:
- `TestAttestation_EvidenceParsed_SGX` — SGX evidence format parses.
- `TestAttestation_EvidenceParsed_SEV` — SEV-SNP evidence format
  parses.
- `TestAttestation_EvidenceParsed_Nitro` — Nitro evidence format
  parses.
- `TestAttestation_EvidenceParsed_Azure` — Azure CC evidence format
  parses.
- `TestAttestation_VerificationFlow_Mock` — Mock attestation verifies
  per platform.
- `TestAttestation_PCRWhitelistMatch` — Approved PCRs accepted.
- `TestAttestation_PCRWhitelistMiss` — Non-approved PCRs rejected.
- `TestAttestation_VerifyCostBounded` — Verification ≤ 50ms across
  platforms.

**Integration tests**:
- `TestAttestation_JoinRejectsUnattested` — Non-attested node rejected
  when policy requires.
- `TestAttestation_JoinAllowsUnattested_WhenPolicyOff` — Without
  policy, unattested nodes join.
- `TestAttestation_RotationRefreshes` — Periodic SVID rotation
  re-verifies attestation; rotation succeeds.
- `TestAttestation_HeterogeneousPlatforms` — Cluster with SGX + Nitro
  + bare-metal nodes; all join with platform-appropriate evidence.

**End-to-end tests**:
- `TestAttestationE2E_RealEnclave` — Real SGX or Nitro deployment
  joins cluster, publishes, gets revoked on attestation failure
  (e.g., simulated kernel measurement change).
- `TestAttestationE2E_AttestationOutage` — Platform attestation
  service down; new joins blocked; existing nodes continue; service
  restored; new joins resume.

**Race condition tests**:
- `TestAttestation_ConcurrentJoins` — `go test -race` with 100
  concurrent join attempts; verifier handles correctly.
- `TestAttestation_RotationDuringPublish` — SVID rotation
  concurrent with active publishes; either old SVID accepted (until
  expiry) or new SVID accepted; no torn-state rejection.
- `TestAttestation_ApprovedSetUpdateRace` — Approved measurement set
  updated concurrently with new joins; deterministic outcome based
  on operator-group commit order.

**Negative / non-happy path tests**:
- `TestAttestation_ForgedEvidence_Rejected` — Forged attestation
  evidence rejected with `ErrAttestationInvalid`.
- `TestAttestation_ReplayedEvidence_Rejected` — Replay of past
  evidence rejected (nonce check).
- `TestAttestation_ExpiredEvidence_Rejected` — Evidence past its
  validity window rejected.
- `TestAttestation_WrongPlatformEvidence_Rejected` — SGX evidence
  presented to Nitro verifier rejected with platform-mismatch error.
- `TestAttestation_MalformedSVIDExtension_Rejected` — SVID with
  truncated / malformed attestation extension rejected.
- `TestAttestation_PlatformVerifierUnavailable_GracefulFallback` —
  Verifier unavailable (e.g., SGX QE down); join request queued or
  retried per policy; no crash.
- `TestAttestation_RevokedDueToAttestationFailure` — Existing node's
  re-attestation fails; node revoked via authority broadcast;
  capabilities removed.
- `TestAttestation_PCRMismatchAfterKernelUpdate_Detected` — Node
  reboots with new kernel; PCRs change; re-attestation fails; node
  re-joins only if new PCRs added to approved set.
- `TestAttestation_DowngradeAttack_Rejected` — Older / weaker
  attestation format presented when stronger required; rejected.
- `TestAttestation_ClockSkewedNonce_Rejected` — Attestation nonce
  outside acceptable time window rejected.

---

### Phase 18 — Federation, Edge, Witness

Cross-cluster federation, edge tier, witness/learner replicas, coalesced
heartbeats, hierarchical Raft. Per §20.

**Phase implementation overview**: Phase 18 extends Phase 14's remote-
production substrate from "one cluster, multi-DC" to "many clusters
federated, edge-distributed, hierarchical." Federation rides on §17.2
BFT subjects; edge tier reuses §16 embedded storage; witness/learner
replicas modify the existing Raft engine. Common dependencies: BGP feed
clients (`pmacct` stream / RIPE RIS), cloud anycast (Cloudflare /
AWS Global Accelerator), DNS-SD, and the §17 accountability layer.

#### 18.1 — Federation control plane

**Description**: Per §20.1. BFT-replicated control subject across
cluster representatives.

**Implementation approach**:
- Federation control subject `sylk://federation/<id>/control/v1` is a
  BFT subject (§17.2) hosted across designated cluster representatives.
- Cluster representative = a node holding the `federation_rep`
  capability, nominated by local operator group write.
- Federation gateway: separate substrate role binary or co-located,
  listens on configurable QUIC port, performs cross-cluster ingress /
  egress with cryptographic verification (§17.1).
- Subject ID disambiguation: 128-bit composite
  `(cluster_origin_uint64, subject_local_uint64)` to prevent collision.
- Federation member set stored in BFT control subject; admission +
  removal require BFT commit.
- Code path: `core/substrate/federation/` (~2500 LOC).
- Hard part: representative re-election under partial cluster failures —
  solved by deterministic representative-rotation rule based on node
  ID + capability + φ score.

**Acceptance criteria**:
- Federation control subject committed via §17.2 BFT (3f+1 cluster
  representatives across federation members).
- Cross-cluster subject lookup via federation registry returns
  `(cluster_origin, subject_uri, schema_hash)`.
- Per-pair subject allowlist enforced at federation gateway egress
  and ingress.
- Compromise of one cluster cannot exfiltrate beyond allowlist (verified
  by gateway-side filter on every cross-cluster frame).
- Membership changes (add / remove / rotate trust root) BFT-committed
  with M-of-N operator signatures from each affected cluster.
- Representative election deterministic; ties broken by node ID.
- Federation control subject latency: p99 < 500ms for typical
  cross-DC committee.
- Federation gateway throughput: ≥ 10K cross-cluster frames/sec with
  verification.

**Unit tests**:
- `TestFederation_RegistryLookup_LocalCache` — Cross-cluster lookup hits
  cached entry.
- `TestFederation_RegistryLookup_FallsBackToBFT` — Cache miss queries
  BFT control subject.
- `TestFederation_AllowlistPositive` — Allowed subject passes through.
- `TestFederation_AllowlistNegative` — Disallowed rejected at gateway.
- `TestFederation_PolicyChange_BFTCommitted` — Allowlist change requires
  BFT commit.
- `TestFederation_RepresentativeElection_Deterministic` — Same input →
  same outcome.
- `TestFederation_SubjectIDComposite` — Composite IDs disambiguate
  origin clusters.
- `TestFederation_OperatorMSignature` — Membership change requires
  M-of-N signatures from each cluster.

**Integration tests**:
- `TestFederation_TwoClusterE2E` — Two-cluster federation; cross-publish
  works; verification end-to-end.
- `TestFederation_PartialCompromise` — Compromised peer cannot exceed
  allowlist; gateway filters.
- `TestFederation_RepresentativeFailover` — Representative dies;
  re-election within timeout; cluster continues.
- `TestFederation_ControlSubjectViewChange` — BFT view change in control
  subject; allowlist still enforced.
- `TestFederation_HeterogeneousVersions` — Federation peers running
  different substrate versions (within compat matrix).

**End-to-end tests**:
- `TestFederationE2E_ThreeClusterMesh` — Three clusters; full mesh;
  subscriber in A receives from B and C with verified provenance.
- `TestFederationE2E_LongRunning` — 7-day federation run; no drift; no
  unauthorized cross-publishes.

**Race condition tests**:
- `TestFederation_ConcurrentAllowlistRead` — `go test -race` 1K
  concurrent allowlist checks; consistent.
- `TestFederation_ConcurrentRepresentativeNomination` — Race in
  representative election; deterministic resolution.
- `TestFederation_LookupCacheRace` — Concurrent cache lookup +
  invalidation; no torn reads.
- `TestFederation_GatewayConcurrentFrameRouting` — `go test -race` with
  10K concurrent frames in flight; no race; correct routing.
- `TestFederation_PolicyUpdateDuringActiveStream` — Policy tightened
  mid-stream; in-flight frames either complete (committed before
  policy tightening) or fail with `ErrPolicyChanged`.

**Negative / non-happy path tests**:
- `TestFederation_UnknownPeer_Rejected` — Frame from unknown federation
  peer rejected.
- `TestFederation_RevokedTrustRoot_Rejected` — Frame signed by revoked
  trust root rejected.
- `TestFederation_AllowlistMissing_DefaultDeny` — No allowlist
  declared → all subjects denied (fail-closed).
- `TestFederation_RepresentativeQuorumLost_NoProgress` — Lose majority
  of representatives; control subject reads stall; cluster operates
  on cached allowlist with bounded staleness.
- `TestFederation_MalformedSubjectID_Rejected` — Composite subject ID
  with garbage cluster_origin rejected.
- `TestFederation_CrossClusterReplay_Rejected` — Frame replay across
  federation rejected by §6 dedupe (event_id index).
- `TestFederation_AsymmetricAllowlist` — Cluster A allows subject X
  to B; cluster B doesn't allow X from A; rejected at A's egress
  (egress check is fail-fast).
- `TestFederation_TrustRootRotation_DowntimeBounded` — Trust-root
  rotation; bounded brief window where new + old roots both valid.
- `TestFederation_GatewayOOM_Bounded` — Gateway under memory pressure
  (§29.1); sheds Bulk frames; preserves Critical.
- `TestFederation_BadOperatorSignature_Rejected` — Membership change
  with insufficient operator signatures rejected.

---

#### 18.2 — Cross-cluster subscription with verification

**Description**: Subscriber in A receives entries from B with §17.1
accountability proofs.

**Implementation approach**:
- Federation gateway streams entries: each frame includes
  `(entry, leader_sig, merkle_path_to_signed_root)`.
- Subscriber verifies at receive time: leader_sig under term key, Merkle
  path to current snapshot's signed root.
- Failed verification → frame dropped, peer reputation degraded,
  `sylk://global/security/cross-cluster-failure/v1` event emitted.
- Cursor extended with `cluster_origin` so each cluster's HLC frontier
  tracked independently.
- Code path: `core/substrate/federation/subscription.go` (~1200 LOC).

**Acceptance criteria**:
- Streaming delivery via federation gateway with bounded buffer per
  subscription.
- Each entry includes Merkle path proof + leader signature.
- Subscriber verifies before delivering to consumer; verification cost
  <1ms per entry on commodity hardware.
- Tampered or unsigned entries rejected; dropped peer marked.
- Cursor frontier tracks per `(cluster_origin, subject)`; no drift.
- Bandwidth overhead from proofs ≤ 30% of raw entry bandwidth.
- Verification failures bounded by per-peer rate limit; abusive peer
  paused.

**Unit tests**:
- `TestCrossCluster_DeliveryWithProof` — Delivery includes proof.
- `TestCrossCluster_VerifyMerklePath` — Proof verifies against signed
  root.
- `TestCrossCluster_TamperedDelivery_Rejected` — Tampered entry caught.
- `TestCrossCluster_CursorPerOrigin` — Cursor tracks per cluster.
- `TestCrossCluster_VerificationCost_Bounded` — < 1ms per entry.

**Integration tests**:
- `TestCrossCluster_HighThroughput` — Sustained delivery; throughput
  meets target with verification overhead bounded.
- `TestCrossCluster_BandwidthOverhead` — Bandwidth ≤ 30% over raw.
- `TestCrossCluster_PeerReputationDegrades` — Persistent verification
  failures from peer → peer paused.

**End-to-end tests**:
- `TestCrossClusterE2E_AdversarialPeer` — Federation peer attempts
  forgery; subscriber detects and isolates peer.
- `TestCrossClusterE2E_CursorRecovery` — Subscriber restart; cursor
  resumes per-origin frontier; no duplicates, no gaps.

**Race condition tests**:
- `TestCrossCluster_ConcurrentVerify` — `go test -race` 1K concurrent
  verifications; no race.
- `TestCrossCluster_CursorAdvanceRace` — Concurrent ack + delivery;
  cursor advances atomically.
- `TestCrossCluster_PeerPauseRace` — Reputation degrade race with
  delivery; pause takes effect cleanly.

**Negative / non-happy path tests**:
- `TestCrossCluster_TamperedSig_Rejected` — Signature tamper detected.
- `TestCrossCluster_TamperedMerklePath_Rejected` — Merkle path tamper
  detected.
- `TestCrossCluster_MissingProof_Rejected` — Frame without proof
  rejected when proof required.
- `TestCrossCluster_StaleSnapshotRoot_RejectedWithRefresh` — Frame
  proves against snapshot root older than retention; subscriber
  fetches newer root.
- `TestCrossCluster_PartialFailure_DropsBadFrames` — Mixed valid +
  invalid stream; valid delivered, invalid dropped, no full-stream
  abort.
- `TestCrossCluster_GatewayDeath_ResubscribeNewGateway` — Gateway
  dies; subscriber falls over; cursor resumes correctly.
- `TestCrossCluster_UpstreamRestart_NoDuplicates` — Source cluster
  restarts; subscriber sees no duplicate entries (dedupe).

---

#### 18.3 — Edge / PoP tier

**Description**: Per §20.2. Stateless gateway with view cache, outbox,
SVID passthrough.

**Implementation approach**:
- PoP binary: substrate role with restricted capability set;
  `core/substrate/edge/pop.go`.
- Local cache: reuses §16 embedded storage with tighter retention
  (default 1h or 100MB).
- Outbox: dedicated subject `sylk://edge/<pop_id>/outbox/v1` with
  short retention; on PoP restart, outbox replays via existing
  three-layer dedupe (§9).
- mTLS termination at PoP; PoP signs additional `gateway_forward`
  signature, but user SVID passes through unchanged. Home cluster
  authority predicate validates user signature.
- Anycast / geo-DNS handled via deployment (Cloudflare, AWS Global
  Accelerator, Equinix); not code.
- Code path: `core/substrate/edge/` (~2000 LOC).

**Acceptance criteria**:
- View cache: read-through; HLC-frontier validated against home cluster.
- Outbox: queued during disconnection; replayed on reconnect with
  three-layer dedupe (no duplicates).
- mTLS termination at PoP; user SVID carried through; home cluster
  validates user signature end-to-end.
- Anycast / geo-DNS for selection (deployment integration).
- PoP failure → next-PoP failover transparent to user (≤ 2s).
- PoP storage bounded per retention policy.
- Compromise of PoP cannot forge user actions (verified by signature
  passthrough).
- PoP itself runs §17.4 attested execution where supported.

**Unit tests**:
- `TestEdge_ViewCacheReadThrough` — Cache miss fetches from home.
- `TestEdge_ViewCacheHit` — Cache hit served locally.
- `TestEdge_OutboxQueuesWrites` — Disconnected writes queued.
- `TestEdge_OutboxReplay` — Reconnect replays with dedupe.
- `TestEdge_OutboxDedupe` — Duplicate outbox entries dropped via §9.
- `TestEdge_SVIDPassthrough` — User signature validated at home, not
  PoP.
- `TestEdge_PoPSigSeparate` — PoP gateway-forward signature separate
  from user signature.
- `TestEdge_RetentionBound` — Cache size respects retention.

**Integration tests**:
- `TestEdge_FailoverToNextPoP` — PoP failure; client falls over within
  budget.
- `TestEdge_CompromisedPoP` — Compromised PoP cannot forge user
  actions; home cluster rejects.
- `TestEdge_HLCFrontierValidation` — Cache invalidates when HLC
  frontier advances.
- `TestEdge_LongDisconnection` — 1h disconnection; outbox preserved;
  reconnect replays.

**End-to-end tests**:
- `TestEdgeE2E_GlobalLatency` — TUI in Tokyo, cluster in us-west; PoP in
  narita; latency meets target.
- `TestEdgeE2E_DisconnectedOperation` — Long disconnection; outbox
  preserved; reconnect catches up.
- `TestEdgeE2E_AnycastFailover` — Real DNS / anycast; PoP outage;
  reroute transparent.

**Race condition tests**:
- `TestEdge_ConcurrentOutboxWrites` — `go test -race` 1K concurrent
  outbox writes; no race.
- `TestEdge_OutboxReplayDuringNewWrites` — Replay concurrent with new
  writes; ordering preserved.
- `TestEdge_CacheInvalidationDuringRead` — Invalidation race with
  in-flight read; reader sees consistent snapshot.
- `TestEdge_ReconnectionRace` — Multiple reconnect attempts; only
  one succeeds.

**Negative / non-happy path tests**:
- `TestEdge_PoPDiskFull_OutboxBackpressure` — Outbox storage full;
  client gets `ErrOutboxFull`; reconnects to home directly.
- `TestEdge_HomeClusterUnreachable_CacheServes` — Home cluster down;
  cache serves stale reads with explicit staleness indication.
- `TestEdge_StaleCacheBoundExceeded_ServesError` — Cache staleness
  beyond bound; reader gets `ErrCacheStale`.
- `TestEdge_OutboxOverflow_RejectsCriticalFirst` — Outbox at cap;
  Bulk dropped, Critical rejected with `ErrOutboxFull`.
- `TestEdge_PoPCertExpired_ClientReconnects` — PoP cert expires;
  client reconnects to next PoP.
- `TestEdge_TamperedFrameAtPoP_Detected` — Frame tampered at PoP;
  home cluster rejects (signature mismatch).
- `TestEdge_PoPMaliciousReplay_DroppedAtHome` — PoP replays user
  frame; dedupe at home drops.
- `TestEdge_ClockSkewAtPoP_HomeRejects` — PoP clock skewed beyond
  bound; HLC validation at home rejects forwarded frames.

---

#### 18.4 — Witness replicas

**Description**: Per §20.3. Vote-only replica with no log replication.

**Implementation approach**:
- Modify Raft replicator: witness role flag; replicator sends only
  `(commit_index, term)` heartbeats, not log bodies.
- Storage: witness has no log on disk; only term + voted_for state.
- Voting path unchanged from voter.
- Recovery: witness reboot restores commit index from voters via
  RPC; no replay.
- Promotion to voter: existing joint consensus.
- Code path: `core/substrate/consensus/raft/witness.go` (~400 LOC).

**Acceptance criteria**:
- Witness votes but doesn't store log on disk.
- 2-voter + 1-witness quorum operates correctly.
- Witness storage cost: O(1), independent of log size.
- Witness bandwidth: only commit indices, ≤ 1KB/s typical.
- Witness recovery after partition: resyncs commit indices via
  voters; no full log replay.
- Witness can be promoted to voter via joint consensus.
- Cannot have all-witness quorum (must include ≥ 1 voter).

**Unit tests**:
- `TestWitness_VotesNoStorage` — Vote without log.
- `TestWitness_QuorumCorrectness` — 2-voter + 1-witness quorum
  commits correctly.
- `TestWitness_PromoteToVoter` — Promotion via joint consensus.
- `TestWitness_StorageBound` — O(1) storage regardless of log size.
- `TestWitness_BandwidthBound` — ≤ 1KB/s steady-state.
- `TestWitness_AllWitnessConfig_Rejected` — Configuration with no
  voters refused.

**Integration tests**:
- `TestWitness_StretchedCluster` — 2-DC + witness DC; partition
  tolerance.
- `TestWitness_RecoveryAfterPartition` — Witness rejoins; resyncs
  fast.
- `TestWitness_VoterFailoverWithWitness` — Voter dies; witness
  preserves quorum.

**End-to-end tests**:
- `TestWitnessE2E_DCFailoverWithWitness` — Lose 1 of 2 main DCs;
  witness preserves quorum; cluster continues.
- `TestWitnessE2E_LongPartition` — Multi-hour partition; witness
  resync on heal proportional to elapsed commits.

**Race condition tests**:
- `TestWitness_ConcurrentVotes` — Multiple election rounds;
  witness vote state correct.
- `TestWitness_PromotionRace` — Promotion concurrent with voter
  failure; deterministic resolution.
- `TestWitness_RecoveryRace` — Witness recovery while leader changes;
  consistent commit index reached.

**Negative / non-happy path tests**:
- `TestWitness_AttemptLogAppend_Refused` — Attempting to send log
  body to witness rejected with `ErrWitnessNoLog`.
- `TestWitness_AttemptDirectRead_Redirects` — Read on witness
  redirects to voter.
- `TestWitness_LosesMajorityOfVoters_NoProgress` — Witness alone
  cannot make progress (no log to commit against).
- `TestWitness_ConfigChangeRemovesAllVoters_Rejected` — Refuse
  config that would leave only witnesses.
- `TestWitness_PromotionWhilePartitioned_Stalls` — Promotion attempted
  during partition; stalls until heal.

---

#### 18.5 — Learner replicas

**Description**: Per §20.3. Non-voting replica with full log.

**Implementation approach**:
- Existing Raft libraries (`etcd/raft`, `hashicorp/raft`) support
  learners; extend with substrate-specific promotion criteria.
- Learner: `voter=false`; full log replication; full snapshot install.
- Promotion criteria: lag ≤ configured threshold (default 1s of HLC).
- Read-as-learner: read-index supported; reads served from learner
  with linearizability via leader read-index handshake.
- Code path: `core/substrate/consensus/raft/learner.go` (~300 LOC) +
  promotion logic ~200 LOC.

**Acceptance criteria**:
- Learner doesn't count toward quorum.
- Full log replication; full snapshot install.
- Promotion to voter via joint consensus when caught up to within
  lag threshold.
- Learner-as-read-replica supported with read-index for linearizable
  reads.
- Many learners supported per group (≥ 100 learners feasible).
- Learner addition doesn't slow leader (rate-limited replication).

**Unit tests**:
- `TestLearner_NoVote` — Doesn't count toward quorum.
- `TestLearner_PromotionWhenCaughtUp` — Promotion succeeds.
- `TestLearner_PromotionRefusedIfLag` — Refused if lag > threshold.
- `TestLearner_ReadIndex` — Read-index served from learner.
- `TestLearner_ManyLearners` — 100 learners; leader unaffected.

**Integration tests**:
- `TestLearner_CatchUpNonDisruptive` — Adding learner doesn't slow
  leader (rate-limit verified).
- `TestLearner_AsReadReplica` — Reads via learner; linearizable.
- `TestLearner_SnapshotInstall` — Far-behind learner installs
  snapshot.

**End-to-end tests**:
- `TestLearnerE2E_AuditReplica` — Audit replica continuously
  snapshots; never affects voter performance.
- `TestLearnerE2E_TrainingFeed` — ML training feed via learner; data
  never leaves cluster boundary.

**Race condition tests**:
- `TestLearner_ConcurrentReadIndex` — `go test -race` 1K concurrent
  reads via learner; correct.
- `TestLearner_PromotionConcurrentWithReads` — Promotion mid-read;
  read either completes or migrates to voter cleanly.
- `TestLearner_AdditionRace` — Multiple learner additions concurrent;
  serialized correctly.

**Negative / non-happy path tests**:
- `TestLearner_VoteAttempt_Rejected` — Learner vote attempt rejected.
- `TestLearner_PromotionWithoutQuorum_Stalls` — Promotion requires
  voter quorum; stalls if no quorum.
- `TestLearner_LagBeyondThreshold_PromotionRefused` — Far-behind
  learner refused promotion until caught up.
- `TestLearner_ReadIndexBeforeCaughtUp_StallsUntilSync` — Read on
  uncaught learner stalls or returns `ErrLearnerStale`.
- `TestLearner_CrashDuringPromotion_RecoversToOldRole` — Crash
  mid-promotion; recovery preserves prior role consistency.

---

#### 18.6 — Coalesced heartbeat protocol

**Description**: Per §20.4. One physical heartbeat per node-pair carrying
state for many groups.

**Implementation approach**:
- Per-node-pair heartbeat manager: aggregates all groups' state into
  single CBOR frame.
- Frame body: `BaseFull{groups: []GroupState}` periodic + delta-encoded
  `Delta{changed: []GroupChange}` between bases.
- Loss of coalesced frame triggers per-group failure timer reset
  (every group on the pair detected simultaneously).
- Replaces existing per-group heartbeat path entirely.
- Code path: `core/substrate/transport/heartbeat_coalesce.go`
  (~1200 LOC).

**Acceptance criteria**:
- Heartbeat traffic O(node-pairs), not O(groups).
- 1M groups per node-pair sustained.
- Failure detection latency unchanged from per-group heartbeat
  (within ±10%).
- Loss of coalesced heartbeat triggers detection for all groups
  simultaneously.
- Frame size bounded: full base ≤ 1MB; delta ≤ 100KB typical.
- Compression effective: delta typically 10-20x smaller than base.

**Unit tests**:
- `TestCoalescedHB_TrafficScaling` — Traffic constant in group count.
- `TestCoalescedHB_FailureDetection` — Failure detected within budget.
- `TestCoalescedHB_DeltaEncoding` — Delta compresses correctly.
- `TestCoalescedHB_BaseFrameRecovery` — Periodic base re-sync after
  many deltas.
- `TestCoalescedHB_FrameSizeBounded` — Bounds enforced.

**Integration tests**:
- `TestCoalescedHB_HighGroupCount` — 100K groups; heartbeat overhead
  bounded.
- `TestCoalescedHB_LossDetection` — Frame loss triggers all groups'
  detection simultaneously.

**End-to-end tests**:
- `TestCoalescedHBE2E_MillionSessions` — Million-session simulated
  cluster; heartbeat is not bottleneck.

**Race condition tests**:
- `TestCoalescedHB_ConcurrentGroupUpdates` — `go test -race` with
  many groups updating; coalesced frame consistent.
- `TestCoalescedHB_BasePlusDeltaInterleaving` — Base and delta
  arriving out of order; receiver reorders correctly.
- `TestCoalescedHB_ConcurrentSenders` — Multiple concurrent flushes
  to wire; serialized correctly.

**Negative / non-happy path tests**:
- `TestCoalescedHB_FrameOversize_Split` — Frame exceeding QUIC stream
  size split into multiple sub-frames with consistent semantics.
- `TestCoalescedHB_DeltaWithoutBase_RequestsBase` — Receiver gets
  delta without prior base; requests base.
- `TestCoalescedHB_OldDelta_Discarded` — Stale delta (older than
  current state) discarded.
- `TestCoalescedHB_PeerDeath_AllGroupsTimeout` — Peer dies; all groups
  on the pair detect simultaneously; no group-level false negatives.
- `TestCoalescedHB_NetworkReorder_Tolerated` — Reordered delivery;
  no spurious failure detections.
- `TestCoalescedHB_FrameTampered_Rejected` — Tampered frame rejected
  (sig mismatch); peer reputation degraded.

---

#### 18.7 — Hierarchical Raft

**Description**: Per §20.5. Region meta-groups for namespace placement.

**Implementation approach**:
- Region meta-group: Raft group with state machine "namespace
  placements within region X."
- Root group: SM owns "tenant→region" + cross-region migration.
- Routing layer: client lookups go region→namespace; root only
  consulted on cross-region operations.
- Code path: `core/substrate/topology/hierarchical/` (~1000 LOC
  routing + ~400 LOC SMs).

**Acceptance criteria**:
- Region meta-groups own regional namespace placement.
- Cross-region migrations escalate to root.
- Region failure isolated to that region's placement decisions
  (existing namespaces unaffected).
- Root group only consulted for global / cross-region operations.
- Region-local subject registry caches global registry; pulls
  updates.
- Up to 20 regions tested.

**Unit tests**:
- `TestHierarchical_RegionLocalCreate` — Creation in region doesn't
  hit root.
- `TestHierarchical_CrossRegionMigrate` — Migration via root.
- `TestHierarchical_RegionFailureIsolated` — Region meta-group dies;
  other regions unaffected.
- `TestHierarchical_RootGroupConsultedOnlyForCrossRegion` — Routing
  verified.
- `TestHierarchical_RegistryCacheRefresh` — Region cache pulls from
  root.

**Integration tests**:
- `TestHierarchical_MultiRegionLifecycle` — 4 regions; create
  namespaces in each; migrate cross-region.
- `TestHierarchical_RootFailoverDoesNotBlockRegional` — Root group
  failover; regional ops continue with cached state.

**End-to-end tests**:
- `TestHierarchicalE2E_GlobalCluster` — 20 regions; namespace lifecycle
  scales.

**Race condition tests**:
- `TestHierarchical_ConcurrentRegionalCreates` — `go test -race`
  multi-region creates; no cross-region collisions.
- `TestHierarchical_MigrationRace` — Concurrent cross-region
  migrations of same namespace; deterministic resolution.
- `TestHierarchical_CacheInvalidationRace` — Cache invalidation race
  with new lookup; eventual consistency reached.

**Negative / non-happy path tests**:
- `TestHierarchical_RootUnavailable_RegionalContinues` — Root group
  unavailable; regional ops continue from cached state with bounded
  staleness.
- `TestHierarchical_RegionPartitioned_OtherRegionsUnaffected` — Region
  fully partitioned; rest of cluster operates normally.
- `TestHierarchical_CrossRegionMigrationDuringPartition_DefersUntilHeal`
  — Migration deferred; resumes on heal.
- `TestHierarchical_RegionDecommission_NamespacesEvacuated` —
  Decommission flow safely evacuates before region removal.
- `TestHierarchical_StaleRegionCache_RefreshOnConflict` — Stale cache
  causes routing failure; auto-refresh + retry.

---

#### 18.8 — Shared-log Raft optimization

**Description**: Per §20.6. Many small groups share one physical log per
node.

**Implementation approach**:
- Pebble DB per node; key prefix `(group_id_be64, raft_index_be64)`.
- Per-group Merkle root indexed separately.
- Per-group crash recovery: read group's index range, verify Merkle
  root.
- Compaction respects group boundaries (no reads cross groups).
- Per-group physical log option preserved for groups that need
  isolation.
- Code path: `core/substrate/storage/shared_log.go` (~2500 LOC).
- Hard part: corruption isolation. Per-group Merkle roots detect
  corruption in group X without affecting Y.

**Acceptance criteria**:
- Configurable: per-group physical log vs shared log.
- Per-group Merkle roots preserved.
- Per-group crash recovery preserved (corruption in group X doesn't
  affect Y).
- IOPS reduced N-fold for N groups sharing a log (verified empirically).
- 10K groups per node sustained.
- Compaction per-group; doesn't blocking writes to other groups.

**Unit tests**:
- `TestSharedLog_MultiGroupAppend` — Multiple groups append to one log.
- `TestSharedLog_PerGroupRecovery` — Crash recovery isolated per group.
- `TestSharedLog_CompactionBoundary` — Compaction respects boundaries.
- `TestSharedLog_PerGroupMerkleRoot` — Per-group root verifies
  independently.
- `TestSharedLog_GroupIsolation` — Corruption in group X doesn't
  affect group Y's reads.

**Integration tests**:
- `TestSharedLog_HighDensityWrites` — 10K groups per node; throughput
  meets target.
- `TestSharedLog_IOPSReduction` — Measured IOPS reduction vs
  per-group config.

**End-to-end tests**:
- `TestSharedLogE2E_LongRunningStability` — 24-hour run with
  high-density groups; no fragmentation.

**Race condition tests**:
- `TestSharedLog_ConcurrentMultiGroupWrites` — `go test -race` writes
  from many groups; no race; correct ordering per-group.
- `TestSharedLog_CompactionDuringActiveWrites` — Per-group compaction
  concurrent with writes from other groups; no blocking.
- `TestSharedLog_RecoveryRace` — Recovery of multiple groups after
  crash; deterministic order.

**Negative / non-happy path tests**:
- `TestSharedLog_DiskFullPartialWrite_RecoveryConsistent` — Disk
  fills mid-write; recovery preserves committed entries from all
  groups, drops partial.
- `TestSharedLog_BitrotInGroupX_DetectedNotPropagated` — Single bit
  flip in group X's data; group X's Merkle root mismatch detected;
  group Y unaffected.
- `TestSharedLog_GroupRetirementCleanup` — Retired group's data
  reclaimed without affecting others.
- `TestSharedLog_LSMCorruption_FailoverToReplica` — Local pebble
  corruption; replica catches up via §3.6 streaming snapshot.
- `TestSharedLog_SchemaBackwardCompat` — Old per-group log readable
  after migration to shared log.

---

### Phase 19 — Extended Storage Architecture

Multi-tier storage, encryption-at-rest, continuous backup, PITR, horizon
compaction, erasure coding. Per §21.

**Phase implementation overview**: Phase 19 extends Phase 2's local
storage with hot/warm/cold/archive tiering, envelope encryption, backup
+ DR primitives, point-in-time restore, and DAG horizon compaction.
Storage abstraction layer `core/substrate/storage/tier/` is the
integration point for all tier backends. Common dependencies:
`aws-sdk-go-v2/service/s3`, `cloud.google.com/go/storage`,
`Azure/azure-sdk-for-go/sdk/storage/azblob`, KMS adapters, AEAD ciphers,
`klauspost/reedsolomon`.

#### 19.1 — Multi-tier storage policy

**Description**: Per §21.1.

**Implementation approach**:
- Storage abstraction with backends:
  - Hot: existing mmap on local NVMe.
  - Warm: file IO on local SSD (no mmap; less wired memory).
  - Cold: object store via `aws-sdk-go-v2/service/s3` (or GCS/Azure
    SDK); content-addressed key = blake3 hex.
  - Archive: same SDK with Glacier / Archive storage class.
- Per-subject tier policy stored in operator group as part of
  `SubjectPolicy` CRD.
- Demoter: background goroutine driven by access tracking + age;
  scans sealed segments; uploads + verifies + removes local.
- Promoter: on read miss, fetch full segment to warm; LRU eviction
  caps warm-tier storage.
- Cold-tier reads: own message class with dedicated bandwidth budget;
  cannot starve hot reads.
- Time-travel reads transparently fault in cold/archive segments via
  streaming reader; archive returns
  `ErrArchiveRestoreInProgress` with HLC ETA.
- Code path: `core/substrate/storage/tier/` (~2500 LOC).
- Hard part: tier transition consistency under concurrent reads.
  Solved by Merkle-verified handoff: segment present in target tier
  + Merkle root match before source tier removed.

**Acceptance criteria**:
- Per-subject tier policy declared in `SubjectPolicy` CRD.
- Auto-tiering: LFU with time decay + age-driven demotion (configurable
  weights).
- Cold reads bandwidth-budgeted (own message class with priority below
  Standard).
- Time-travel transparent across tiers; archive reads return ETA.
- Promotion on first cold access; LRU eviction at warm-tier cap.
- Tier transitions Merkle-verified before source tier removal.
- Demotion never blocks active writes.
- Same content hash across tiers (verified end-to-end).

**Unit tests**:
- `TestTier_DemotionPolicy_AgeBased` — Aged segments demote.
- `TestTier_DemotionPolicy_LFUWeighted` — Cold-by-frequency segments
  demote.
- `TestTier_PromotionOnRead` — Cold read promotes to warm.
- `TestTier_BandwidthBudget` — Cold reads don't starve hot reads.
- `TestTier_MerkleVerifyAcrossTier` — Same content hash across tiers.
- `TestTier_LRUEvictionAtCap` — Warm tier evicts LRU at cap.
- `TestTier_TransitionMerkleVerified` — Source removed only after
  target verified.
- `TestTier_ArchiveETA` — Archive read returns
  `ErrArchiveRestoreInProgress` with HLC ETA.

**Integration tests**:
- `TestTier_ColdReadE2E` — Subject with cold tier; query reaches it.
- `TestTier_ArchiveRestore` — Archive segment restored; subsequent
  read succeeds.
- `TestTier_DemotionConcurrentWithReads` — Demotion + reads coexist;
  consistency.

**End-to-end tests**:
- `TestTierE2E_LongHistorySession` — 1-year session; navigate full
  history; tiers used appropriately; bandwidth budgets respected.
- `TestTierE2E_MultiCloudTier` — Hot local; warm local; cold S3;
  archive Glacier; full read path validated.

**Race condition tests**:
- `TestTier_ConcurrentDemoteAndPromote` — `go test -race` demotion
  and promotion of same segment; deterministic outcome.
- `TestTier_ReaderDuringTierTransition` — Reader holds reference to
  segment being transitioned; read completes from old tier; next
  read uses new tier.
- `TestTier_LRUEvictionRace` — Concurrent eviction + promotion;
  no double-free.
- `TestTier_BandwidthCounterRace` — Concurrent cold reads; bandwidth
  budget enforced atomically.

**Negative / non-happy path tests**:
- `TestTier_S3Unreachable_FallsBackToReplica` — Cold tier object
  store unreachable; substrate fetches from voter replica's local
  copy if available.
- `TestTier_SegmentMissingFromCold_AlertsAndRecovers` — Cold object
  missing (operator deleted, expired); alerts + recovers via
  cross-replica fetch.
- `TestTier_ArchiveRestoreFails_CallerNotified` — Archive restore
  fails; query returns `ErrArchiveRestoreFailed`.
- `TestTier_TamperedColdSegment_Rejected` — Tampered cold segment
  detected via Merkle root mismatch; rejected; alert fires.
- `TestTier_DemotionDuringDiskFull_QueuedDeferred` — Disk full;
  demotion queued; deferred until space available.
- `TestTier_PromotionWhenWarmAtCap_TriggersEviction` — Warm tier at
  cap; promotion triggers eviction of LRU; verified.
- `TestTier_PolicyChangeApplyToExisting` — Policy change affects
  existing segments per re-evaluation cycle.
- `TestTier_BandwidthBudgetExceeded_QueuedNotDropped` — Cold-read
  bandwidth budget hit; reads queued, not dropped.

---

#### 19.2 — Encryption-at-rest envelope

**Description**: Per §21.2. KEK / DEK envelope hierarchy with per-segment
AEAD.

**Implementation approach**:
- AEAD library: `crypto/aes` (AES-GCM-256) for x86 with AES-NI;
  `golang.org/x/crypto/chacha20poly1305` for ARM.
- KMS adapter interface; implementations: AWS KMS, GCP Cloud KMS,
  HashiCorp Vault, PKCS#11 HSM via `miekg/pkcs11`.
- KEK in KMS; DEK envelope per `(tenant, subject_class)` rotated
  annually or on demand.
- Per-segment trailer: `kek_id || nonce_96bit || encrypted_dek_wrap`.
- KEK rotation: re-wrap envelopes (KMS Decrypt + Encrypt with new
  KEK); segment data unchanged.
- Field-level: subject schema flags `pii=true`; SM applies inner
  AEAD with field-encryption-key derived via HKDF from DEK + field
  name.
- Forward secrecy: ephemeral session keys for short-lived subjects;
  destroyed after compaction.
- Code path: `core/substrate/storage/envelope/` (~1800 LOC) + ~400
  LOC per KMS provider.

**Acceptance criteria**:
- KEK / DEK separation enforced; KEK never leaves KMS / HSM.
- AEAD per-segment with 96-bit unique nonce; nonce-reuse impossible
  via deterministic generation.
- KEK rotation re-wraps envelopes only (no segment rewrite); rotation
  duration: O(envelope count), not O(data size).
- Field-level encryption for declared PII fields; field-encryption-
  keys not stored anywhere except derived ephemerally.
- Forward-secrecy variant: ephemeral keys for short-lived subjects.
- Tampering detected via AEAD authentication tag.
- KMS unavailability: cluster degrades to read-only; existing keys
  cached for cache TTL.

**Unit tests**:
- `TestEnvelope_KEKRotation` — Old KEK destroyed; data still readable
  via new envelope.
- `TestEnvelope_AEADIntegrity` — Tampered ciphertext rejected.
- `TestEnvelope_FieldLevel` — PII fields independently keyed.
- `TestEnvelope_NonceUniqueness` (property test, 1M iterations) —
  Nonces never reused.
- `TestEnvelope_HKDFFieldKey` — Field-encryption-key derivation
  deterministic from DEK.
- `TestEnvelope_ForwardSecrecyDestruction` — Ephemeral key zeroed
  in memory after compaction.
- `TestEnvelope_KMSAdapterInterface` — Each provider impl
  conforms to interface.

**Integration tests**:
- `TestEnvelope_HSMIntegration` — HSM-backed key access works.
- `TestEnvelope_RotationLive` — Rotation during live traffic; no
  read/write errors.
- `TestEnvelope_KMSCacheTTL` — KMS unavailable; cached keys serve
  reads until TTL expires.
- `TestEnvelope_FieldLevelE2E` — End-to-end PII roundtrip.

**End-to-end tests**:
- `TestEnvelopeE2E_CrossClusterRotation` — KEK rotation across
  federated cluster; uninterrupted.
- `TestEnvelopeE2E_OnDemandRotationAfterCompromise` — Simulated KEK
  compromise; rotation completes within minutes; old KEK destroyed.

**Race condition tests**:
- `TestEnvelope_ConcurrentEncryptDecrypt` — `go test -race` 1K
  concurrent; AEAD correctness preserved.
- `TestEnvelope_RotationDuringRead` — Rotation concurrent with
  read; reader uses old or new KEK consistently.
- `TestEnvelope_NonceCounterRace` — Nonce counter race; uniqueness
  preserved (CAS-based atomic).
- `TestEnvelope_KMSCacheConcurrent` — Concurrent KMS operations;
  cache consistent.

**Negative / non-happy path tests**:
- `TestEnvelope_KMSUnavailable_DegradesToReadOnly` — KMS down; new
  writes refused; reads served from cache.
- `TestEnvelope_TamperedAEADTag_Rejected` — Bit flip in tag detected.
- `TestEnvelope_TamperedNonce_Rejected` — Nonce tamper detected.
- `TestEnvelope_KEKDeletedFromKMS_DataInaccessible` — KEK explicitly
  deleted; data unreadable; specific error returned.
- `TestEnvelope_ForgedKEKID_Rejected` — Frame claims unknown KEK ID;
  rejected.
- `TestEnvelope_DEKWrapTampered_Rejected` — Wrapped DEK tampered;
  unwrap fails.
- `TestEnvelope_RotationFailsHalfway_Recoverable` — KMS error during
  rotation; partial rotation rolls back; data still readable via
  old KEK.
- `TestEnvelope_FieldKeyDerivationConsistency` — HKDF derivation
  deterministic across runs and platforms.
- `TestEnvelope_AEADCipherVersionMigration` — Migration from one AEAD
  cipher to another (e.g., AES-GCM to ChaCha20-Poly1305) preserves
  data.
- `TestEnvelope_NonceCounterOverflow_Rotated` — Nonce counter
  approaching 2^64 limit triggers DEK rotation before exhaustion.

---

#### 19.3 — Continuous backup

**Description**: Per §21.3. Backup consumer streams sealed segments to
immutable storage.

**Implementation approach**:
- Backup is a substrate consumer (learner-mode subscription) running
  per cluster.
- Bucket configured with object-lock (S3 COMPLIANCE mode), bucket
  versioning, MFA delete.
- Post-write GET to verify hash matches source.
- Backup progress published to `sylk://global/backup/v1` for cluster
  audit.
- Restore protocol: validate Merkle roots against §30.3 multi-source
  list; apply only after verification.
- Code path: `core/substrate/backup/` (~1000 LOC).

**Acceptance criteria**:
- Backup writes encrypted, content-addressed segments to immutable
  storage.
- Post-write Merkle verification; mismatch alerts.
- Backup-progress published to substrate audit subject.
- Restore verifies multi-source Merkle root before applying.
- Cross-cluster DR replication via same primitive in stream form.
- Backup lag bounded; alert on lag > threshold.
- Bucket retention enforced via object-lock.

**Unit tests**:
- `TestBackup_StreamingWrite` — Sealed segments written.
- `TestBackup_MerkleVerifyPostWrite` — Post-write verification.
- `TestBackup_RestoreVerifyFirst` — Tampered backup rejected before
  apply.
- `TestBackup_ProgressPublished` — Progress entries to audit subject.
- `TestBackup_LagAlerted` — Lag > threshold triggers alert.

**Integration tests**:
- `TestBackup_LongRunningParity` — Live cluster vs backup; parity
  maintained over 24h.
- `TestBackup_ObjectLockEnforced` — Backup objects unmodifiable per
  retention.

**End-to-end tests**:
- `TestBackupE2E_DRFailover` — Primary cluster lost; DR cluster
  activates from continuous backup; cursor-correct.
- `TestBackupE2E_CrossCloudReplication` — Backup to S3 + GCS + Azure
  simultaneously; cross-cloud restore verified.

**Race condition tests**:
- `TestBackup_ConcurrentSegmentSeal` — `go test -race` multiple
  segments sealing concurrently; backup picks up all.
- `TestBackup_RestoreRace` — Restore concurrent with backup writes;
  consistent.

**Negative / non-happy path tests**:
- `TestBackup_BucketUnreachable_LagAccumulatesBounded` — Bucket
  unreachable; lag bounded; resumes on heal.
- `TestBackup_PostWriteHashMismatch_Retries` — Verification fails;
  retry; persistent failure alerts.
- `TestBackup_ObjectLockViolation_Rejected` — Attempt to delete
  locked object rejected.
- `TestBackup_RestoreFromIncompleteBackup_Refused` — Backup missing
  segments; restore refuses with `ErrBackupIncomplete`.
- `TestBackup_TamperedBackup_Rejected` — Tampered backup detected
  via Merkle mismatch.
- `TestBackup_KEKRotationDuringBackup_Coherent` — KEK rotation
  mid-backup; backup uses consistent KEK envelope per segment.
- `TestBackup_OutOfOrderSegmentArrival_Reordered` — Backup writes
  segments out of order (parallel uploads); restore reorders by HLC.

---

#### 19.4 — Point-in-time restore

**Description**: Per §21.4.

**Implementation approach**:
- Operator API call → operator-group write spawning new namespace.
- Reuses existing snapshot/replay machinery; replays from nearest
  snapshot ≤ target HLC; stops at target HLC.
- Sibling namespace has full first-class status; can serve traffic,
  diff'd against live, promoted via authority transfer + gateway
  routing update.
- Forced snapshot creation if none ≤ target HLC.
- Code path: `core/substrate/restore/pitr.go` (~600 LOC).

**Acceptance criteria**:
- Operator API: `Restore(source_ns, hlc, target_ns)`.
- Atomic creation; namespace ready when complete; partial state
  invisible.
- No interference with live namespace (live continues unaffected).
- Sibling can be promoted to replace live via authority cutover.
- Restore time: O(entries from snapshot to target HLC).
- Promoted sibling can be reverted.

**Unit tests**:
- `TestPITR_RestoresState` — State at HLC matches live state at HLC.
- `TestPITR_AtomicCreate` — Sibling namespace atomic.
- `TestPITR_ForcedSnapshot` — No prior snapshot; one created on
  demand.

**Integration tests**:
- `TestPITR_LiveNamespaceUnaffected` — Concurrent traffic to live;
  restore doesn't affect.
- `TestPITR_SiblingPromotion` — Promote sibling; cutover atomic.
- `TestPITR_RevertPromotion` — Revert promotion; original live
  restored.

**End-to-end tests**:
- `TestPITRE2E_IncidentRecovery` — Realistic scenario: bug caused bad
  state at HLC H; restore at H-ε; promote sibling; resume normal
  operation.

**Race condition tests**:
- `TestPITR_ConcurrentRestoresDifferentNamespaces` — Multiple
  restores concurrent; isolated.
- `TestPITR_ReplayConcurrentWithLiveTraffic` — Replay from snapshot
  while live takes traffic; no interference.

**Negative / non-happy path tests**:
- `TestPITR_HLCBeyondRetention_Refused` — Target HLC pre-retention;
  refused with `ErrOutsideRetention`.
- `TestPITR_TargetNamespaceExists_Refused` — Target namespace exists;
  refused unless `--overwrite` flag with operator authority.
- `TestPITR_RestoreFails_PartialNamespaceCleanedUp` — Restore fails
  midway; partial namespace cleaned up; no orphan state.
- `TestPITR_PromotionDuringActiveLiveWrites_AtomicCutover` — Promote
  while writes in flight; cutover is atomic; in-flight writes either
  land in old or new, never both.
- `TestPITR_DowngradedSchema_Rejected` — Target HLC requires schema
  version no longer present; rejected.
- `TestPITR_QuorumLossDuringRestore_Resumes` — Restore in progress;
  voter death; restore resumes after recovery.

---

#### 19.5 — Causal DAG horizon compaction

**Description**: Per §21.5.

**Implementation approach**:
- Per-subject horizon HLC stored in manifest.
- Background compactor walks parent index; for entries with hlc <
  horizon, replaces parent edges with single horizon-parent pointer
  (to most-recent snapshot at horizon).
- Pebble batch update for atomicity.
- Snapshot at horizon must exist + be signed (§17.1) before
  compaction proceeds.
- Causal cone queries past horizon return
  `(horizon_parent, ErrHorizonTruncated)` — auditor verifies Merkle
  path to snapshot.
- Code path: `core/substrate/storage/horizon.go` (~700 LOC).

**Acceptance criteria**:
- Per-subject horizon HLC tracked in manifest.
- Parent edges past horizon collapsed to "horizon parent."
- Causal cone queries terminate at horizon with explicit indication.
- Audit Merkle paths still verify past horizon (snapshot is
  authoritative anchor).
- Compaction never blocks active queries (background, batched).
- Horizon snapshot signed with current term key (§17.1).
- Compaction reversible if horizon advanced too aggressively (within
  retention window).

**Unit tests**:
- `TestHorizon_EdgeCollapse` — Edges past horizon collapsed.
- `TestHorizon_QueryTermination` — Cone walk terminates with
  `ErrHorizonTruncated`.
- `TestHorizon_CompactorBackground` — Compactor runs without blocking
  queries.
- `TestHorizon_SnapshotSigned` — Horizon snapshot signed.
- `TestHorizon_AuditPathPreserved` — Audit verification uses snapshot.

**Integration tests**:
- `TestHorizon_LongHistoryGrowthBounded` — 10-year subject; parent
  index doesn't grow without bound.
- `TestHorizon_ConcurrentCompactionAndCone` — Cone queries during
  compaction.

**End-to-end tests**:
- `TestHorizonE2E_AuditPreserved` — Audit Merkle path still verifies
  past horizon via snapshot.

**Race condition tests**:
- `TestHorizon_CompactorConcurrentReads` — `go test -race` cone
  queries during compaction; consistent.
- `TestHorizon_HorizonAdvanceRace` — Horizon advances concurrent
  with new entries crossing horizon; deterministic resolution.
- `TestHorizon_ParentIndexUpdateAtomic` — Pebble batch atomicity
  preserved.

**Negative / non-happy path tests**:
- `TestHorizon_CompactionWithoutSnapshot_Refused` — No snapshot at
  horizon; compaction refused.
- `TestHorizon_ConePastHorizon_ReturnsTruncated` — Cone walk past
  horizon returns specific error.
- `TestHorizon_SnapshotCorruption_AuditFails` — Snapshot at horizon
  corrupted; audit verification fails with specific error.
- `TestHorizon_HorizonTooAggressive_RestoredFromBackup` — Horizon
  advanced beyond useful auditability; restore from backup adjusts.
- `TestHorizon_CompactionCrash_RecoveryConsistent` — Compactor
  crashes mid-batch; recovery preserves consistent state.

---

#### 19.6 — Erasure-coded cold tier

**Description**: Per §21.6.

**Implementation approach**:
- Library: `klauspost/reedsolomon` (k=10, m=4 default).
- Encoding at demote-to-cold: split sealed segment into k+m shards;
  upload each to distinct prefix / bucket / region per policy.
- Read: parallel GET on k of (k+m) shards; first to return decodes;
  Merkle root verifies decoded segment.
- Reconstruction on shard loss: fetch k surviving shards, regenerate
  missing.
- Code path: `core/substrate/storage/erasure.go` (~500 LOC).

**Acceptance criteria**:
- Reed-Solomon (k+m) encoding for cold sealed segments.
- Recovery from any k of (k+m) shards.
- Storage cost: ≤ 1.5x (vs 3x replication) at default k=10, m=4.
- Encoding done at demote-to-cold time; decode on read.
- Shards distributed across distinct failure domains (prefix, bucket,
  region).
- Reconstruction triggered on detected shard loss; runs in background.
- Read latency degrades gracefully with shard losses.

**Unit tests**:
- `TestErasure_EncodeDecode` — Round-trip preserves bytes.
- `TestErasure_ShardLossRecovery_AllPatterns` — Every (m choose) shard
  loss pattern recoverable.
- `TestErasure_StorageCostBound` — Effective storage cost ≤ 1.5x.
- `TestErasure_ParallelDecode` — Parallel GET; first k decodes.
- `TestErasure_MerkleVerifyPostDecode` — Decoded segment matches
  Merkle root.

**Integration tests**:
- `TestErasure_DemotionPath` — Hot → warm → cold (erasure-coded)
  pipeline.
- `TestErasure_ReconstructionTriggered` — Shard loss detected;
  reconstruction queued.
- `TestErasure_DistributedAcrossFailureDomains` — Shards in distinct
  domains.

**End-to-end tests**:
- `TestErasureE2E_LargeColdTier` — 1TB cold tier; recovery from
  shard losses.
- `TestErasureE2E_RegionalOutage` — One region's shards unavailable;
  reads succeed via remaining regions.

**Race condition tests**:
- `TestErasure_ConcurrentEncode` — `go test -race` parallel
  encodes of different segments.
- `TestErasure_DecodeRaceWithReconstruction` — Decode and
  reconstruction race; deterministic outcome.
- `TestErasure_ShardLossRaceWithRead` — Shard becomes unavailable
  mid-read; reader switches to alternate.

**Negative / non-happy path tests**:
- `TestErasure_TooManyShardsLost_DataLoss` — More than m shards lost;
  decode fails with `ErrInsufficientShards`; alert.
- `TestErasure_TamperedShard_DetectedViaMerkle` — Tampered shard;
  decoded segment fails Merkle check; rejected.
- `TestErasure_PartialUploadFailure_Retried` — Some shards fail to
  upload; retry; eventual completion.
- `TestErasure_ReconstructionLagBounded` — Many shards lost
  simultaneously; reconstruction queued; no thundering herd.
- `TestErasure_KMismatchedShards_DecodeFails` — k shards with
  different segment hashes; decode rejects.
- `TestErasure_ShardSizeMismatch_Rejected` — Shard with unexpected
  size rejected.
- `TestErasure_ParameterMismatchOnRead_Rejected` — Encoded with
  k=10 m=4; read attempts with k=8 m=2 rejected.

---

### Phase 20 — Multi-Tenancy and Resource Isolation

Per-tenant quotas, group isolation, compaction isolation, lifecycle, cost
accounting. Per §22.

**Phase implementation overview**: Phase 20 adds the multi-tenant
abstractions on top of Phase 11's primitives + Phase 17's BFT subjects.
Quotas, lifecycle, and cost accounting all flow through BFT subjects so
that no operator and no tenant can unilaterally rewrite enforcement
state. Per-tenant LSM isolation uses Linux cgroup v2 for hard CPU/IO
isolation; macOS and Windows use cooperative quota enforcement.
Dependencies: pebble, cgroup v2, KMS adapters from §19.2, BFT engine
from §17.2.

#### 20.1 — Tenant quota subject

**Description**: Per §22.1.

**Implementation approach**:
- BFT subject `sylk://tenant/<id>/quota/v1` with state machine
  tracking quota state.
- Authority predicate extension consults quota state via cached LSM
  read (~10µs hot path).
- Cache invalidation: HLC-tagged; cache miss on stale tag forces
  refresh from BFT subject.
- Quota fields: storage GB per tier, msg/sec per class, namespace
  count, replication bandwidth, compaction CPU-sec/hour, federation
  cross-publish bandwidth.
- Code path: `core/substrate/tenancy/quota/` (~700 LOC).

**Acceptance criteria**:
- Per-tenant quota subject; BFT-replicated (§17.2) for tenant-tenant
  fairness.
- Authority predicate consults quota at publish time; cached lookup
  ≤ 10µs hot path.
- Over-quota: `ErrQuotaExceeded` returned cheaply (read-side check, no
  replication round-trip).
- Quota historical: time-travel queries supported (§12.1).
- Quota updates require operator + tenant double-signature.
- Cache invalidation HLC-tagged; bounded staleness.
- All quota fields enforceable independently.

**Unit tests**:
- `TestQuota_OverThresholdRejected` — Over-quota publish rejected.
- `TestQuota_AuthorityIntegration` — Predicate evaluates quota
  correctly across all fields.
- `TestQuota_HistoricalQuery` — Past quota state queryable.
- `TestQuota_CacheLookupCost` — Lookup ≤ 10µs.
- `TestQuota_HLCInvalidation` — Update invalidates cache.
- `TestQuota_DoubleSignatureRequired` — Single signature rejected.

**Integration tests**:
- `TestQuota_EnforcementUnderLoad` — Bursty publisher hits quota;
  bounded.
- `TestQuota_BFTReplicated` — Quota changes BFT-committed.
- `TestQuota_AllFieldsEnforced` — Each quota field independently
  triggers `ErrQuotaExceeded` when exceeded.

**End-to-end tests**:
- `TestQuotaE2E_NoisyTenantIsolated` — Tenant flooding doesn't impact
  others.
- `TestQuotaE2E_QuotaIncrease_RealTime` — Operator increases quota;
  tenant unblocked within seconds.

**Race condition tests**:
- `TestQuota_ConcurrentPublishes_QuotaEnforced` — `go test -race`
  100 concurrent publishes near quota limit; quota enforced
  consistently.
- `TestQuota_UpdateDuringActivePublish` — Quota tightened during
  active publish; in-flight either succeeds or fails cleanly.
- `TestQuota_CacheRefreshRace` — Concurrent cache miss + invalidation;
  consistent state.

**Negative / non-happy path tests**:
- `TestQuota_BFTQuorumLost_FailsClosed` — BFT subject quorum lost;
  authority predicate fails closed (rejects publishes); cluster
  doesn't operate without quota verification.
- `TestQuota_NegativeQuota_Rejected` — Setting negative quota
  rejected.
- `TestQuota_ZeroQuota_AllPublishesRejected` — Zero quota → all
  publishes rejected.
- `TestQuota_QuotaForUnknownTenant_Rejected` — Quota update for
  non-existent tenant rejected.
- `TestQuota_QuotaDecreaseBelowUsage_NewWritesBlocked` — Decrease
  quota below current usage; existing data preserved; new writes
  blocked.
- `TestQuota_StaleCacheBoundExceeded_Refresh` — Cache age > bound;
  forces refresh.
- `TestQuota_OperatorSignatureRevoked_RejectsFurtherUpdates` —
  Revoked operator signature; no more updates accepted.

---

#### 20.2 — Tenant-isolated namespace groups

**Description**: Per §22.2.

**Implementation approach**:
- Operator-group invariant on `CreateNamespace`: refuse if proposed
  members would mix tenants.
- Group ID schema:
  `tenant_id_uint64 || namespace_local_id_uint64`.
- Per-tenant namespace count capped via quota (§20.1).
- Code path: `core/substrate/tenancy/isolation.go` (~300 LOC).

**Acceptance criteria**:
- No cross-tenant Raft groups (verified at creation).
- Namespace count per-tenant capped via quota.
- Group ID schema prevents accidental cross-tenant mixing.
- Verified at namespace creation; refuse with specific error.

**Unit tests**:
- `TestTenantIsolation_NoCrossTenantGroups` — Creation enforces
  isolation.
- `TestTenantIsolation_QuotaExceeded` — Over namespace count rejected.
- `TestTenantIsolation_GroupIDSchema` — Group ID composite respected.

**Integration tests**:
- `TestTenantIsolation_LoadIsolation` — Tenant load doesn't propagate.
- `TestTenantIsolation_GroupCreationAtScale` — 1K namespaces per
  tenant; isolation preserved.

**End-to-end tests**:
- `TestTenantIsolationE2E_AdversarialTenant` — Adversarial tenant
  can't degrade others.

**Race condition tests**:
- `TestTenantIsolation_ConcurrentCreations` — `go test -race`
  multi-tenant concurrent creations; no cross-tenant race.
- `TestTenantIsolation_QuotaCheckRace` — Concurrent creation near
  quota limit; deterministic enforcement.

**Negative / non-happy path tests**:
- `TestTenantIsolation_AttemptCrossTenantGroup_Rejected` — Operator
  attempting cross-tenant group rejected.
- `TestTenantIsolation_TenantSuspended_NoNewGroups` — Suspended
  tenant cannot create new namespaces.
- `TestTenantIsolation_TenantFrozen_NoWrites` — Frozen tenant can
  read but not write.
- `TestTenantIsolation_GroupIDCollision_Rejected` — Forced collision
  rejected.

---

#### 20.3 — Per-tenant LSM compaction

**Description**: Per §22.3.

**Implementation approach**:
- Per-tenant pebble.DB instance up to ~1K tenants/node; beyond, shared
  DB with custom compaction queue.
- Linux: cgroup v2 hierarchy: `/sys/fs/cgroup/sylk/tenant-<id>`;
  CPU + IO controllers. Cooperative on macOS / Windows.
- Custom compaction scheduler: priority queue, hot preempts cold,
  per-tenant CPU-second accounting against quota.
- Code path: `core/substrate/tenancy/compaction.go` (~1200 LOC).

**Acceptance criteria**:
- Per-tenant compaction queue.
- CPU/IO accounting per tenant.
- Priority-aware scheduler (hot subject's pending writes preempt
  cold tier compaction).
- Over compaction quota → compaction stalls (writes still accepted;
  backlog grows; eventually backpressure to publisher).
- Linux cgroup v2 hard limits enforced.
- Compaction in tenant X cannot starve tenant Y.

**Unit tests**:
- `TestCompaction_PerTenantQueue` — Tenants don't share queue.
- `TestCompaction_QuotaEnforced` — Over CPU quota → compaction stalls.
- `TestCompaction_PrioritySchedule` — Hot compaction preempts cold.
- `TestCompaction_CGroupV2_HardLimits` — Linux cgroup limits enforced.
- `TestCompaction_CooperativeOnNonLinux` — macOS / Windows
  cooperative quota enforced.

**Integration tests**:
- `TestCompaction_FairnessUnderLoad` — N tenants; each gets fair
  share.
- `TestCompaction_HotColdInterleaving` — Hot compactions interleave
  with cold; latency budgets respected.

**End-to-end tests**:
- `TestCompactionE2E_NoCompactionInterference` — Heavy compaction in
  one tenant doesn't slow others.
- `TestCompactionE2E_QuotaThrottlesGracefully` — Tenant exceeding
  quota; compaction throttles, writes backpressure.

**Race condition tests**:
- `TestCompaction_ConcurrentCompactionAcrossTenants` — `go test -race`
  multiple tenant compactions; no interference.
- `TestCompaction_PreemptionRace` — Hot compaction preempts cold
  mid-execution; clean handoff.
- `TestCompaction_CGroupAttachRace` — New goroutine joining cgroup
  during compaction; correct accounting.

**Negative / non-happy path tests**:
- `TestCompaction_TenantSuspended_QueuePaused` — Suspended tenant's
  compaction paused.
- `TestCompaction_CGroupOOM_TenantOnlyAffected` — Tenant cgroup OOM;
  only that tenant's compaction killed; others unaffected.
- `TestCompaction_DiskFullForTenant_Stalls` — Tenant's storage full;
  compaction stalls (no work to do); doesn't affect others.
- `TestCompaction_QuotaIncrease_ResumesCompaction` — Quota raised;
  stalled compaction resumes within seconds.
- `TestCompaction_RaftGroupMigration_CompactionTransfers` —
  Namespace migrates; compaction state transfers cleanly.
- `TestCompaction_HighPriorityStarvesLow_ResolvedByAging` — Sustained
  hot work; cold compactions eventually run via aging policy.

---

#### 20.4 — Per-tenant key material

**Description**: Per §22.4.

**Implementation approach**:
- Per-tenant KEK in KMS namespace; cross-tenant DEK access
  cryptographically impossible without KEK.
- KEK access: HSM with per-operator approval workflow for emergency
  restore (M-of-N quorum).
- Tenant offboarding: `KMS.ScheduleKeyDeletion(tenant_kek, 7d)`;
  substrate verifies key gone before declaring offboarded.
- Code path: `core/substrate/tenancy/keys.go` (~400 LOC) on top of
  §19.2.

**Acceptance criteria**:
- Per-tenant KEK isolation.
- Cross-tenant access cryptographically impossible without KEK.
- Tenant offboarding destroys KEK; data inaccessible regardless of
  disk recovery.
- KEK access requires M-of-N operator quorum.
- Offboarding flow: 7-day soft delete window before hard destruction.

**Unit tests**:
- `TestTenantKey_Isolation` — Cross-tenant decryption fails.
- `TestTenantKey_OffboardDestruction` — KEK destroyed; data
  inaccessible.
- `TestTenantKey_MOfNApproval` — Recovery requires M signatures.
- `TestTenantKey_SoftDeleteWindow` — 7-day window; key recoverable
  during; destroyed after.

**Integration tests**:
- `TestTenantKey_HSMIntegration` — HSM-backed per-tenant KEKs.
- `TestTenantKey_KMSProviderIntegration` — Each provider (AWS, GCP,
  Azure, Vault) works.

**End-to-end tests**:
- `TestTenantKeyE2E_OffboardingFlow` — Full offboarding flow; data
  cryptographically gone; verifiable via failed decrypt attempts.
- `TestTenantKeyE2E_EmergencyRestore` — M-of-N restore; KEK recovered;
  data accessible.

**Race condition tests**:
- `TestTenantKey_ConcurrentEncryptDifferentTenants` — `go test -race`
  parallel encrypt across tenants; no cross-key bleed.
- `TestTenantKey_DestructionRaceWithRead` — Read in flight while
  KEK destruction; read either completes (cached DEK) or fails
  cleanly with `ErrKEKDestroyed`.

**Negative / non-happy path tests**:
- `TestTenantKey_RogueOperator_CannotAccessAlone` — Single operator
  cannot access KEK without M-of-N.
- `TestTenantKey_KMSAdapterMisconfigured_RefusesPublish` — Bad KMS
  config; cluster refuses publishes for affected tenant.
- `TestTenantKey_TenantSuspendedKEKReachable_NoDataAccess` — KEK
  reachable but tenant suspended; data still inaccessible (auth
  layer blocks).
- `TestTenantKey_OffboardingPartialFailure_StateConsistent` —
  Offboarding fails midway; state rolls back; tenant marked
  partially-offboarded; recovery continues.
- `TestTenantKey_KMSAuditLogVerified` — All key access logged;
  audit cross-checks against operator-group signatures.

---

#### 20.5 — Cost accounting subject

**Description**: Per §22.5.

**Implementation approach**:
- Atomic counters in publish / storage / replication paths;
  periodic flush to usage subject (every 1s, coalesced).
- Per-tenant usage subject `sylk://tenant/<id>/usage/v1`.
- Time-travelable; tenant + operator visible.
- Code path: `core/substrate/tenancy/cost.go` (~500 LOC).

**Acceptance criteria**:
- Per-tenant usage subject populated continuously.
- Includes: bytes published / stored / replicated, compaction CPU,
  federation bytes, plus disk used per tier.
- Time-travelable (§12.1).
- Tenant-visible (self-service) + operator-visible (revenue).
- Recording overhead ≤ 1% of operation cost.
- Accuracy: ≤ 5% drift from ground truth over 24h.

**Unit tests**:
- `TestCostAccounting_RecordedPerTenant` — Each operation records.
- `TestCostAccounting_HistoricalQuery` — Past usage queryable.
- `TestCostAccounting_AtomicCounters` — Concurrent updates atomic.
- `TestCostAccounting_FlushPeriod` — Periodic flush ≤ 1s.

**Integration tests**:
- `TestCostAccounting_AccuracyUnderLoad` — Recorded usage matches
  actual within tolerance over 24h.
- `TestCostAccounting_LowOverhead` — Recording adds ≤ 1% overhead.

**End-to-end tests**:
- `TestCostAccountingE2E_ChargebackReport` — Realistic month; report
  matches.

**Race condition tests**:
- `TestCostAccounting_ConcurrentIncrements` — `go test -race` 10K
  concurrent increments; final count exact.
- `TestCostAccounting_FlushDuringIncrement` — Flush concurrent with
  increment; no lost updates.

**Negative / non-happy path tests**:
- `TestCostAccounting_FlushFailure_RetriedNoLoss` — Flush to subject
  fails; counter buffered; retry; no lost data.
- `TestCostAccounting_CounterOverflow_BoundedRollover` — 64-bit
  counter approaches max; rolled over with explicit overflow event.
- `TestCostAccounting_TenantOffboarded_HistoricalPreserved` —
  Tenant offboarded; historical usage subject retained per
  retention policy.
- `TestCostAccounting_RaftGroupMigration_ContinuesAfterMove` —
  Subject migrated; counters preserved.

---

#### 20.6 — Tenant lifecycle subject

**Description**: Per §22.6.

**Implementation approach**:
- BFT state machine over `sylk://global/tenant-lifecycle/v1`.
- Operator + tenant double-signature: each transition requires two
  signatures from designated authority hierarchies.
- Export bundle: streamed sealed segments + manifest, blake3-rooted.
- Offboarding: soft delete + KEK destruction (§20.4).
- Code path: `core/substrate/tenancy/lifecycle.go` (~1000 LOC).

**Acceptance criteria**:
- Lifecycle subject; operator + tenant double-signed transitions.
- States: created, suspended, frozen, offboarded, exported.
- KEK destruction on offboard.
- Export bundle Merkle-verified; portable to another cluster.
- Lifecycle transitions auditable (full history).
- Offboarding compliant with data-portability obligations
  (GDPR / CCPA).

**Unit tests**:
- `TestTenantLifecycle_AllStates` — Each transition.
- `TestTenantLifecycle_OffboardKeyDestroy` — KEK destroyed; data
  inaccessible.
- `TestTenantLifecycle_ExportBundleVerify` — Export Merkle-verifiable.
- `TestTenantLifecycle_DoubleSignatureRequired` — Each transition.
- `TestTenantLifecycle_SuspendBlocksPublishes` — Suspended state
  blocks pub/sub.
- `TestTenantLifecycle_FreezeBlocksWrites` — Frozen state blocks
  writes only.

**Integration tests**:
- `TestTenantLifecycle_FullFlow` — Create → use → suspend → freeze →
  export → offboard.
- `TestTenantLifecycle_ImportFromExport` — Export from cluster A;
  import to cluster B; data integrity preserved.

**End-to-end tests**:
- `TestTenantLifecycleE2E_RegulatoryCompliance` — Export complies
  with data-portability obligations.

**Race condition tests**:
- `TestTenantLifecycle_ConcurrentTransitions_Serialized` — Concurrent
  transition attempts; deterministic resolution.
- `TestTenantLifecycle_PublishDuringSuspend` — Pub in flight when
  suspend committed; pub fails with `ErrTenantSuspended`.
- `TestTenantLifecycle_ExportDuringActiveTraffic` — Export while
  traffic active; consistent snapshot.

**Negative / non-happy path tests**:
- `TestTenantLifecycle_InvalidTransition_Rejected` — Disallowed
  transition (e.g., offboard → active) rejected.
- `TestTenantLifecycle_PartialOffboarding_RecoverableUntilFinalized`
  — Offboarding interrupted before finalization; recoverable.
- `TestTenantLifecycle_ExportInsufficientPermissions_Rejected` —
  Export without authority rejected.
- `TestTenantLifecycle_SuspendedTenantSubscribe_Rejected` —
  Subscriber in suspended tenant rejected.
- `TestTenantLifecycle_FrozenTenantReads_Allowed` — Frozen tenant
  reads succeed.
- `TestTenantLifecycle_ImportTamperedBundle_Rejected` — Tampered
  export bundle detected on import.
- `TestTenantLifecycle_LegalHold_BlocksOffboarding` — Tenant under
  legal hold; offboarding refused with specific error.

---

### Phase 21 — Time, Determinism, Operational Model

Bounded clocks, HLC fences, skew telemetry, determinism harness, SM
versioning, quarantine, shadow build, CRDs, SPIRE, topology scheduler.
Per §23, §24, §25.

**Phase implementation overview**: Phase 21 grafts three orthogonal
correctness layers onto the substrate: tightened time semantics (bounded
clocks + fences + skew), state-machine safety (determinism harness,
versioning, quarantine, shadow), and declarative cloud-native operations
(CRDs, SPIRE, topology, upgrade). The time and SM layers are pure
substrate code; the operational layer is mostly K8s integration via
`controller-runtime` and `kubebuilder`. Common dependencies: `chrony`
socket, `gpsd`, PTP drivers, `golangci-lint` plugin API,
`controller-runtime`, SPIRE Workload API client (`spiffe/go-spiffe`).

#### 21.1 — Bounded clock service

**Description**: Per §23.1.

**Implementation approach**:
- `BoundedClock` interface with implementations:
  - `NTPBoundedClock` — reads `chrony` socket; uncertainty derived
    from `Root delay/2 + Root dispersion`.
  - `PTPBoundedClock` — reads `/dev/ptp*` via `golang.org/x/sys/unix`
    ioctl; sub-microsecond uncertainty in well-disciplined deployments.
  - `GPSBoundedClock` — reads `gpsd` JSON socket.
  - `DefaultBoundedClock` — hardcoded 500ms (§3.2 default behavior).
- HLC frame extension: 4 extra bytes for `uncertainty_log2_ns`
  (compact base-2 log encoding).
- Cross-DC linearizable read API: `WaitForCommit(ctx)` waits the
  combined uncertainty before serving.
- Code path: `core/substrate/identity/clock/` (~1000 LOC + per-source
  adapter).

**Acceptance criteria**:
- `BoundedClock` interface with documented contract.
- Implementations: NTP (chrony), PTP, GPS (gpsd), default fallback.
- HLC frame extension: 4 extra bytes carry log2 uncertainty.
- Cross-DC linearizable reads wait out uncertainty deterministically.
- Falls back to default 500ms when no clock service configured.
- Uncertainty monotonically tracked over time (no false-shortening).
- Source switch (NTP → PTP) without HLC monotonicity violation.

**Unit tests**:
- `TestBoundedClock_UncertaintyPropagated` — Uncertainty in frame.
- `TestBoundedClock_LinearizableWait` — Wait at least uncertainty
  amount.
- `TestBoundedClock_Fallback` — No service → default.
- `TestBoundedClock_LogScaleEncoding` — Round-trip log2 encoding.
- `TestBoundedClock_MonotonicUncertainty` — Non-decreasing under
  worsening conditions.
- `TestBoundedClock_SourceSwitchNoMonotonicityViolation` — Switching
  sources doesn't decrease HLC.

**Integration tests**:
- `TestBoundedClock_NTPDiscipline` — Real NTP source; uncertainty
  bounded.
- `TestBoundedClock_PTPDiscipline` — Real PTP source; sub-µs
  uncertainty.
- `TestBoundedClock_LinearizableWaitVsLeaderLease` — Wait correct
  vs leader lease (correct under skew).

**End-to-end tests**:
- `TestBoundedClockE2E_TrueTimeSimulation` — Simulated TrueTime;
  cross-DC reads correct.
- `TestBoundedClockE2E_DCWideClockOutage` — DC-wide clock service
  outage; falls back; cluster continues with degraded uncertainty.

**Race condition tests**:
- `TestBoundedClock_ConcurrentNowAndUpdate` — `go test -race`
  concurrent `Now` + clock update; consistent.
- `TestBoundedClock_SourceSwitchRace` — Source switch race with
  in-flight reads; reads observe consistent uncertainty.
- `TestBoundedClock_UncertaintyReadDuringDiscipline` — Discipline
  cycle running; concurrent `Now` calls; consistent uncertainty
  reported.

**Negative / non-happy path tests**:
- `TestBoundedClock_NTPDaemonDown_FallbackToDefault` — NTP daemon
  unavailable; falls back; alerts.
- `TestBoundedClock_GPSAntennaLoss_UncertaintyGrows` — GPS antenna
  disconnected; uncertainty grows; cluster operations adjust.
- `TestBoundedClock_NegativeOffset_ClampedToZero` — Pathological
  negative-offset reading clamped, not negative-propagated.
- `TestBoundedClock_SuddenJump_DetectedAndRejected` — 10s sudden
  jump; rejected as drift bound exceeded; HLC continues advancing
  monotonically.
- `TestBoundedClock_PTPHardwareFailure_Reported` — PTP hardware
  failure; reported via observability subject.
- `TestBoundedClock_InconsistentClockSources_HighestUncertaintyTaken`
  — Multiple sources disagree; substrate takes max uncertainty.

---

#### 21.2 — HLC fencing primitives

**Description**: Per §23.2.

**Implementation approach**:
- `WaitUntil(hlc)`: select on local HLC ticker; wakes when local
  HLC ≥ target.
- `ObservedAfter(hlc)`: read with read-index; blocks until commit
  index covers entries with hlc ≤ argument.
- `FenceWrite(hlc)`: leader holds entry until local HLC ≥ argument
  before committing.
- Composable: e.g., `WaitUntil(hlc1)` then `Publish(...,
  Expect(hlc2))` enables ordering across subjects.
- Code path: `core/substrate/identity/fence.go` (~500 LOC).

**Acceptance criteria**:
- `WaitUntil`, `ObservedAfter`, `FenceWrite` primitives exposed.
- Composable across subjects.
- Allocation-free on hot paths (waiter wakeup uses condition variable
  + atomic signaling).
- Cancellation via `context.Context`.
- Bounded wait: `ObservedAfter(future_hlc)` returns
  `ErrFutureHLC` rather than blocking forever.

**Unit tests**:
- `TestHLCFence_WaitUntil` — Blocks correctly until target HLC.
- `TestHLCFence_ObservedAfter` — Read sees all entries up to HLC.
- `TestHLCFence_FenceWrite` — Write commits at or after specified HLC.
- `TestHLCFence_ContextCancellation` — Cancel via context.
- `TestHLCFence_FutureHLCReturnsErr` — Far-future HLC returns
  `ErrFutureHLC`.
- `TestHLCFence_ZeroAlloc` — `testing.AllocsPerRun` zero on hot
  paths.

**Integration tests**:
- `TestHLCFence_CrossSubjectFence` — Fencing across multiple subjects.
- `TestHLCFence_ChainedComposition` — `WaitUntil` → `Expect` →
  `FenceWrite` chain works.

**End-to-end tests**:
- `TestHLCFenceE2E_RealisticFlow` — Real workflow uses fences.

**Race condition tests**:
- `TestHLCFence_ManyWaitersRace` — `go test -race` 1K waiters on
  same HLC; all wake exactly once.
- `TestHLCFence_HLCAdvanceRace` — Concurrent advance + wait;
  waiters wake correctly.
- `TestHLCFence_CancelDuringWake` — Cancel races with wake; cleanup
  no double-fire.

**Negative / non-happy path tests**:
- `TestHLCFence_HLCBeyondHorizon_ErrTruncated` — Wait for HLC past
  retention horizon (§21.5); returns `ErrHorizonTruncated`.
- `TestHLCFence_FenceWriteOnSubjectWithDifferentHLCSpace_Rejected` —
  HLC fencing only valid within a single causal space.
- `TestHLCFence_DeadlineExceeded_ReturnsTimeout` — Bounded wait
  with deadline; returns timeout error.
- `TestHLCFence_LeaderChangeDuringFenceWrite_Reproposed` — Leader
  change mid-fenced-write; new leader re-proposes; fence preserved.

---

#### 21.3 — Skew telemetry

**Description**: Per §23.3.

**Implementation approach**:
- Per-peer running stat: `max(received_hlc.physical) -
  local_wall_clock`.
- Periodic publish to `sylk://global/security/clock-skew/v1`.
- Threshold-based exclusion: peer with skew > threshold removed
  from quorum participation; can rejoin when skew recovers.
- Logical-only mode: HLC physical frozen; only logical advances.
- Code path: `core/substrate/identity/skew.go` (~400 LOC).

**Acceptance criteria**:
- Continuous skew measurement per peer.
- Skew event subject populated periodically.
- Quorum participation gated by skew threshold (default 5s).
- Logical-only mode reachable on adversarial-clock declaration.
- Per-peer skew exposed via observability.
- Skew recovery: peer rejoins quorum when skew falls under
  threshold.

**Unit tests**:
- `TestSkew_Measured` — Continuous measurement.
- `TestSkew_GatesParticipation` — Skewed node excluded from quorum.
- `TestSkew_LogicalOnlyMode` — Logical-only mode preserves order.
- `TestSkew_PerPeerStat` — Per-peer skew tracked.
- `TestSkew_Recovery` — Skew falls; peer rejoins.
- `TestSkew_LogicalModeRecovery` — Logical-only mode recoverable.

**Integration tests**:
- `TestSkew_RealClockSkew` — Inject skew; telemetry detects.
- `TestSkew_SkewedNodeExcluded` — Excluded from quorum during skew.
- `TestSkew_LogicalModeDuringAttack` — Switch to logical-only on
  declaration; preserves order.

**End-to-end tests**:
- `TestSkewE2E_ClockSkewIncident` — Realistic skew; cluster degrades
  gracefully.

**Race condition tests**:
- `TestSkew_ConcurrentMeasurement` — `go test -race` 100 concurrent
  measurements; counters consistent.
- `TestSkew_ExclusionRaceWithVote` — Exclusion mid-vote; deterministic
  outcome.
- `TestSkew_LogicalModeTransitionRace` — Mode transition during
  active publishes; consistent state.

**Negative / non-happy path tests**:
- `TestSkew_PeerSkewedBeyondLimit_ExcludedNotCrashed` — Extreme skew
  excludes peer; no crash.
- `TestSkew_AllPeersSkewed_ClusterDegradedNotPartitioned` — All peers
  skewed; cluster runs degraded; no premature partition declaration.
- `TestSkew_SkewAttackVector_LogicalModeFallback` — Adversarial
  clock signals; cluster falls back to logical-only.
- `TestSkew_ClockJumpForward_DriftBoundEnforced` — Jump beyond
  drift bound; rejected.
- `TestSkew_NegativeSkew_ClampedToZero` — Local clock ahead of
  peer; clamped to zero (no negative skew).

---

#### 21.4 — Determinism harness

**Description**: Per §24.1.

**Implementation approach**:
- Test infrastructure (not production code).
- Capture: Raft committed-entry stream tap on canary cluster;
  serialized to disk.
- Replay: spawn N goroutines running independent SM instances
  (different machines, OS versions, Go versions in CI matrix);
  hash state at every committed index; fail on divergence.
- Lints: custom `golangci-lint` rule banning `time.Now`, `math/rand`
  (without seed), `range map`, `runtime.Gosched`, `select on time`,
  in `core/substrate/sm/...`.
- Code path: `testutil/determinism/` (~1200 LOC) + `lints/` (~300
  LOC plugin).

**Acceptance criteria**:
- Captures real Raft logs from canary cluster.
- Replays in N parallel SMs across OS / Go-version matrix.
- Compares state at every committed index.
- Lints catch all forbidden constructs in SM packages.
- Bit-equal state required.
- Reports divergence with specific entry and minimal diff.
- Runs as nightly CI job.

**Unit tests**:
- `TestDeterminism_LintsForbiddenConstructs` — `time.Now` /
  `range map` / `math/rand` (unseeded) in SM packages caught.
- `TestDeterminism_ParallelReplayBitEqual` — N replicas bit-equal.
- `TestDeterminism_DivergenceDiagnostics` — Divergence reports
  specific entry + state diff.
- `TestDeterminism_LintAllowsSeededRand` — `math/rand` with seed
  in SM allowed.

**Integration tests**:
- `TestDeterminism_RealLogReplay` — Production log; replays
  bit-equal.
- `TestDeterminism_CrossPlatformReplay` — Linux + macOS replay
  bit-equal.

**End-to-end tests**:
- `TestDeterminismE2E_NightlyVerification` — Nightly job replays
  prod logs; alerts on divergence.
- `TestDeterminismE2E_RegressionDetection` — Introduce non-
  deterministic SM change; harness catches.

**Race condition tests**:
- `TestDeterminism_ParallelReplayRace` — `go test -race` parallel
  SM replays; no race in harness itself.

**Negative / non-happy path tests**:
- `TestDeterminism_GoroutineSchedulingNonDet_Detected` — SM uses
  non-cooperative goroutine; flagged.
- `TestDeterminism_FloatingPointReorderingTolerance` — Float
  ordering across platforms; if SM uses floats, divergence flagged.
- `TestDeterminism_PointerEqualityBug_Caught` — Pointer-equality
  dependence in SM caught (lint).
- `TestDeterminism_LintFalsePositive_Configurable` — False
  positive overridable via comment annotation.
- `TestDeterminism_LogTruncatedMidReplay_GracefulAbort` — Replay
  log truncated; harness aborts cleanly with specific error.

---

#### 21.5 — SM versioning

**Description**: Per §24.2.

**Implementation approach**:
- SM interface: `Apply(entry) error` + `Version() (name string,
  version uint32, code_hash [32]byte)`.
- Raft entry header: 4 bytes for SM version.
- Compatibility table is a substrate subject `sylk://global/sm-
  compat/v1`.
- Replica refuses unknown SM version → alert; pauses replication
  until operator deploys.
- Code path: `core/substrate/sm/version.go` (~700 LOC).

**Acceptance criteria**:
- Each SM has stable `(name, version, code_hash)` triple.
- Entries record SM version.
- Replicas refuse entries from unknown SM versions; alert published.
- Compatibility matrix enforced during rollout (subject-published).
- Slow rollout requires *all* replicas to have *all* versions
  in the rollout window.
- Replica refresh policy: pull new versions before applying entries
  produced under them.

**Unit tests**:
- `TestSMVersion_RefusesUnknown` — Unknown version refused with
  alert.
- `TestSMVersion_RolloutCompatibility` — Compatibility table
  enforced.
- `TestSMVersion_HashStable` — Code hash stable across builds.
- `TestSMVersion_HeaderEncoding` — 4-byte version field round-trips.

**Integration tests**:
- `TestSMVersion_RollingUpgrade` — Upgrade pauses if not all
  replicas have new version.
- `TestSMVersion_VersionPushBeforeUseDuringRollout` — Operator
  can pre-publish version before any entry uses it.

**End-to-end tests**:
- `TestSMVersionE2E_PartialDeploy` — Partial deploy detected;
  upgrade paused.

**Race condition tests**:
- `TestSMVersion_ConcurrentApplyDifferentVersions` — `go test -race`
  apply N versions concurrently; correct dispatch.
- `TestSMVersion_VersionTablePropagationRace` — Version added to
  compat table; race with apply; deterministic outcome.

**Negative / non-happy path tests**:
- `TestSMVersion_DowngradeRejected` — Replica with only newer
  version refuses older entry (downgrade attack).
- `TestSMVersion_HashMismatch_BinaryCorruption_Detected` — Local
  binary's code hash doesn't match deployed hash; refuses to
  start.
- `TestSMVersion_CompatTableUnreachable_FailsClosed` — Compat
  table unavailable; replica refuses unknown versions (fails
  closed, not open).
- `TestSMVersion_VersionRetiredButLogStillHasIt_RefusesApply` —
  Old version retired before all entries replayed; refuses;
  operator must restore.
- `TestSMVersion_HotPatchSameVersionBumpNotAllowed` — Same version
  with different code hash rejected — must bump version.

---

#### 21.6 — Poison-pill quarantine

**Description**: Per §24.3.

**Implementation approach**:
- SM apply wrapped in `defer func() { if r := recover(); r != nil
  { ... } }()`.
- On panic: log entry to `sylk://global/sm-quarantine/v1` with full
  evidence; mark entry quarantined; SM continues.
- Crash-loop bounded (§24.4): 3 panics in 60s → safe mode.
- Code path: `core/substrate/sm/quarantine.go` (~500 LOC) + safe
  mode (~250 LOC).

**Acceptance criteria**:
- SM apply panics caught.
- Entry quarantined; alert published.
- SM continues with next entry.
- Quarantine + crash-loop bound composed.
- Quarantined entry retained in log; can be re-applied by future
  fixed SM version.
- Safe mode: refuses Apply, serves reads; reset via authority
  broadcast.

**Unit tests**:
- `TestQuarantine_PanicCaught` — Panic doesn't crash.
- `TestQuarantine_EntryMarked` — Entry marked unsafe-to-apply.
- `TestQuarantine_CrashLoopSafeMode` — Three crashes → safe mode.
- `TestQuarantine_SafeModeReadsServed` — Safe mode serves reads.
- `TestQuarantine_SafeModeRefusesWrites` — Refuses applies.
- `TestQuarantine_RetryDirectiveReapplies` — Future SM version
  re-applies quarantined entries.

**Integration tests**:
- `TestQuarantine_ClusterContinues` — Cluster continues despite
  quarantined entry.
- `TestQuarantine_ReplicaConsistency` — All replicas quarantine
  same entry deterministically.

**End-to-end tests**:
- `TestQuarantineE2E_BugReplica` — Realistic bug; cluster doesn't
  brick.

**Race condition tests**:
- `TestQuarantine_PanicDuringConcurrentApply` — `go test -race`
  panic in one apply; concurrent applies unaffected.
- `TestQuarantine_SafeModeTransitionRace` — Multiple panics
  concurrent; safe mode transition deterministic.

**Negative / non-happy path tests**:
- `TestQuarantine_StackOverflowDetected` — Stack overflow caught.
- `TestQuarantine_OOMInSM_ProcessSurvives` — SM OOM; recovered;
  entry quarantined.
- `TestQuarantine_PanicAfterPartialStateMutation_Rolled Back` —
  Panic after partial mutation; SM state rolled back to pre-apply.
- `TestQuarantine_SafeModeWithoutOperatorAuthorityReset_Stays` —
  Safe mode without authority reset stays safe; doesn't auto-clear.
- `TestQuarantine_ReplayingQuarantinedAfterFix_ConsistentState` —
  Re-applying quarantined entries after SM fix produces consistent
  state.

---

#### 21.7 — Shadow build verification

**Description**: Per §24.5.

**Implementation approach**:
- Two SM goroutines per replica: primary (committed, read-serving)
  and shadow (parallel apply, no commit).
- Periodic state-hash compare (every 1000 entries default).
- Divergence: alert + halt commits; primary continues serving reads.
- Code path: `core/substrate/sm/shadow.go` (~700 LOC).

**Acceptance criteria**:
- New SM version runs as shadow.
- Periodic state diff vs primary (configurable interval).
- Divergence halts before commit.
- Shadow doesn't slow primary (rate-limited).
- Verifiable rollout: shadow proves equivalence on real production
  traffic before cutover.

**Unit tests**:
- `TestShadow_DivergenceDetected` — Forced divergence detected.
- `TestShadow_NoOpOnEqual` — Equal state, no halt.
- `TestShadow_RateLimitedNoSlowdown` — Shadow apply rate-limited.
- `TestShadow_StateHashAlgorithm` — Hash deterministic and
  collision-resistant.

**Integration tests**:
- `TestShadow_RolloutValidation` — Real rollout validated by
  shadow.
- `TestShadow_PrimaryServesReadsDuringHalt` — Halt for divergence;
  reads continue.

**End-to-end tests**:
- `TestShadowE2E_BuggyVersionCaught` — Buggy version diverges from
  primary; cluster halts before commit.

**Race condition tests**:
- `TestShadow_ConcurrentApplyPrimaryShadow` — `go test -race`
  primary + shadow concurrent; consistent.
- `TestShadow_HashCompareRaceWithApply` — Hash compare during
  apply; consistent snapshot.

**Negative / non-happy path tests**:
- `TestShadow_ShadowCrash_PrimaryUnaffected` — Shadow crashes;
  primary continues.
- `TestShadow_ShadowSlower_BackpressureNotPropagatedToPrimary` —
  Shadow slow; primary not blocked.
- `TestShadow_VersionMismatchedShadow_Refuses` — Shadow version
  not in compat matrix; refused.
- `TestShadow_DivergenceAtSnapshotBoundary_FlaggedClearly` —
  Divergence at snapshot; reported with full diff.

---

#### 21.8 — Reproducible build provenance

**Description**: Per §24.6.

**Implementation approach**:
- Build system: Bazel WORKSPACE or Nix flake; reproducible bit-for-
  bit binaries.
- Hash = blake3(binary || config || schema_set).
- Cluster admission: join handshake includes hash; operator group
  validates against `sylk://global/deployment/v1` approved set.
- Code path: `core/substrate/provenance/` (~500 LOC) + Bazel/Nix
  setup.

**Acceptance criteria**:
- Deployment hash = `(binary_blake3, config_blake3,
  schema_set_blake3)`.
- Cluster join verifies hash against operator-approved set.
- Approved set published to `sylk://global/deployment/v1`.
- Reproducible build verified across CI environments.
- Hash mismatch → join refused with specific error.

**Unit tests**:
- `TestProvenance_HashComputation` — Reproducible hash.
- `TestProvenance_JoinVerify` — Mismatched hash rejected.
- `TestProvenance_BazelReproducibility` — Bazel build bit-for-bit.
- `TestProvenance_NixReproducibility` — Nix build bit-for-bit.

**Integration tests**:
- `TestProvenance_Bazel` — Real reproducible Bazel build round-
  trips.
- `TestProvenance_AcrossArchitectures` — x86 + ARM64 hashes
  separately approved.

**End-to-end tests**:
- `TestProvenanceE2E_FleetWide` — Fleet of nodes; all hashes match.

**Race condition tests**:
- `TestProvenance_ConcurrentJoinAttempts` — `go test -race` many
  joins; verifier consistent.
- `TestProvenance_ApprovedSetUpdateRace` — Approved set updated
  concurrently with new joins; deterministic outcome via operator-
  group commit order.

**Negative / non-happy path tests**:
- `TestProvenance_TamperedBinary_JoinRefused` — Modified binary
  hash mismatches.
- `TestProvenance_ConfigDrift_JoinRefused` — Config changed
  out-of-band; refused.
- `TestProvenance_SchemaSetDrift_JoinRefused` — Schema set drift
  refused.
- `TestProvenance_UnapprovedHash_JoinRefused` — Hash not in
  approved set refused.
- `TestProvenance_ApprovedSetEmpty_FailsClosed` — Empty approved
  set rejects all joins (fail-closed default).
- `TestProvenance_BazelWithSloppyDeps_NonReproducible` —
  Non-reproducible build (e.g., embedded timestamp) flagged.

---

#### 21.9 — Cluster CRDs and operator

**Description**: Per §25.1, §25.2.

**Implementation approach**:
- `kubebuilder` for CRD generation.
- Reconciler in `controller-runtime` style: watch CRDs, diff vs
  operator-group state, apply.
- CRDs: SubjectPolicy, NamespacePlacement, TenantQuota, BackupPolicy,
  FederationPeer, OperatorAuthority, EnvelopeKeyPolicy,
  ClockServicePolicy.
- Drift events to anomaly subject.
- Code path: `cmd/sylkd-operator/` + `apis/v1/` (~3500 LOC).

**Acceptance criteria**:
- Operator service watches CRDs.
- Reconciles to substrate state via operator group.
- Drift events published.
- GitOps-friendly (Argo / Flux compatible).
- Each CRD validated via OpenAPI schema.
- Reconciliation idempotent.
- Reconciliation observable via standard K8s status fields.

**Unit tests**:
- `TestCRD_ReconcileSubjectPolicy` — CRD update propagates.
- `TestCRD_DriftDetection` — Drift detected and reported.
- `TestCRD_OpenAPIValidation` — Invalid CRD rejected by API server.
- `TestCRD_ReconciliationIdempotent` — Same CRD applied twice no-op.
- `TestCRD_StatusReflection` — Status fields reflect actual state.

**Integration tests**:
- `TestCRD_GitOpsWorkflow` — Argo applies CRD; substrate updated.
- `TestCRD_AllCRDTypesEnd2End` — Each CRD type round-trips.

**End-to-end tests**:
- `TestCRDE2E_FullClusterSpec` — Cluster fully managed via CRDs.

**Race condition tests**:
- `TestCRD_ConcurrentReconciliations` — Multiple reconciler
  goroutines; deterministic outcome.
- `TestCRD_CRDUpdatesDuringReconciliation` — CRD updated mid-
  reconcile; eventual consistency.

**Negative / non-happy path tests**:
- `TestCRD_InvalidCRD_RejectedAtAPIServer` — Schema-invalid CRD
  rejected by K8s API server (kubebuilder validation).
- `TestCRD_ReconciliationFailure_Retried` — Reconciliation fails;
  retry with backoff.
- `TestCRD_OperatorGroupUnreachable_DriftEvent` — Operator group
  unavailable; reconciliation fails; drift event emitted.
- `TestCRD_PartialSpecApplication_RolledBack` — Reconcile fails
  midway; partial changes rolled back.
- `TestCRD_ConflictingCRDs_DeterministicResolution` — Two CRDs
  conflict; resolution deterministic by CRD priority.

---

#### 21.10 — SPIRE integration

**Description**: Per §25.3.

**Implementation approach**:
- Deploy SPIRE Server cluster + SPIRE Agent DaemonSet (K8s) or
  systemd units (bare-metal).
- Substrate consumes Workload API via gRPC (`spiffe/go-spiffe`
  library).
- Node attestor varies: K8s SAT, AWS IID, GCP MDS, hardware
  attestation per platform.
- SVID rotation: SPIRE Agent refreshes; substrate consumes via
  Workload API.
- Code path: `core/substrate/identity/spire/` (~400 LOC client
  + deployment work).

**Acceptance criteria**:
- SPIRE agents per node.
- Per-node-type attestation.
- Auto SVID rotation.
- Substrate Workload API consumer.
- SVIDs refreshed before expiry.
- Attestation evidence integrated with §17.4.

**Unit tests**:
- `TestSPIRE_AttestationFlow` — Each node type attests.
- `TestSPIRE_AutoRotate` — SVID rotated.
- `TestSPIRE_WorkloadAPIClient` — Client consumes correctly.

**Integration tests**:
- `TestSPIRE_K8sIntegration` — K8s service-account attestation.
- `TestSPIRE_BareMetalIntegration` — Bare-metal hardware attestor.
- `TestSPIRE_RotationLive` — Live rotation; no traffic interruption.

**End-to-end tests**:
- `TestSPIREE2E_RealCluster` — Real K8s cluster with SPIRE; SVIDs
  issued and rotated.

**Race condition tests**:
- `TestSPIRE_RotationDuringActivePublish` — `go test -race` rotation
  concurrent with publishes; consistent.
- `TestSPIRE_AgentRestartRace` — SPIRE Agent restart concurrent
  with substrate; reconnects cleanly.

**Negative / non-happy path tests**:
- `TestSPIRE_AgentDown_GracefulDegrade` — SPIRE Agent down;
  substrate continues with cached SVID until expiry; alerts.
- `TestSPIRE_SVIDExpiredNoRotation_OperationsHalt` — SVID expired
  without rotation; substrate halts (no unauthenticated ops).
- `TestSPIRE_AttestationFailure_NodeQuarantined` — Attestation
  fails; node refused continued operation; revoke via authority.
- `TestSPIRE_TrustBundleRotation_Coherent` — Trust bundle rotated;
  no interruption.

---

#### 21.11 — PDB and topology scheduling

**Description**: Per §25.4, §25.5.

**Implementation approach**:
- Operator generates K8s `PodDisruptionBudget` per Raft group;
  selector by `sylk.io/raft-group` label.
- `maxUnavailable: floor((replicas-1)/2)` per group.
- Topology hints: substrate publishes Vivaldi sections + DC labels;
  operator updates pod node affinity / topology spread constraints.
- Code path: `cmd/sylkd-operator/pdb/` (~300 LOC) + topology
  generator (~400 LOC).

**Acceptance criteria**:
- Per-Raft-group PDB.
- Topology hints to scheduler.
- Spread constraints honored.
- PDB enforces quorum safety: K8s rolling updates physically cannot
  kill quorum.
- Replicas land in distinct failure domains.

**Unit tests**:
- `TestPDB_QuorumSafe` — PDB prevents quorum loss during eviction.
- `TestPDB_GeneratedFromGroupMembership` — Generation correct.
- `TestScheduler_TopologySpread` — Replicas spread.
- `TestScheduler_VivaldiHintsAppliedToLabels` — Vivaldi → labels
  mapping.

**Integration tests**:
- `TestScheduler_K8sLabels` — K8s labels reflect topology.
- `TestPDB_K8sEnforcement` — K8s respects PDB during cordon/drain.

**End-to-end tests**:
- `TestSchedulerE2E_FaultDomainPlacement` — Real K8s; replicas in
  different zones.

**Race condition tests**:
- `TestPDB_ConcurrentEvictions` — Multiple eviction attempts;
  PDB serializes.
- `TestScheduler_GroupMembershipChangeRace` — Group membership
  change during PDB regeneration; eventually consistent.

**Negative / non-happy path tests**:
- `TestPDB_ForcedEviction_PDBOverride_Logged` — Operator force-
  override PDB; logged with audit trail.
- `TestScheduler_TopologyConstraintViolation_Refused` — Pod that
  violates spread constraints not scheduled.
- `TestPDB_DeleteRaftGroup_PDBCleanedUp` — Group retired; PDB
  garbage-collected.
- `TestScheduler_TopologyHintStale_RegeneratesOnDrift` — Stale
  topology hints; regenerated on drift detection.

---

#### 21.12 — Upgrade orchestration

**Description**: Per §25.7.

**Implementation approach**:
- Substrate-managed rolling upgrade reconciler.
- Picks one node per failure domain (uses §21.11 topology); cordons
  (K8s) or quiesces (bare-metal); upgrades; re-attests; rejoins.
- Quorum check via §20.4 coalesced heartbeats — never drains beyond
  `floor((n-1)/2)` per group.
- Per-version compatibility: §21.5 SM version table enforced.
- Canary cohort: 5% of nodes get new version first; anomaly
  detector watches §28.3 feed; auto-rollback on canary anomaly.
- Code path: `cmd/sylkd-operator/upgrade/` (~1800 LOC).

**Acceptance criteria**:
- Quorum-aware rolling upgrade (never drains beyond
  `floor((n-1)/2)` voters).
- Canary cohort 5%; anomaly detection during canary.
- Rollback on canary failure.
- Per-version compatibility verified before upgrade.
- Live traffic unaffected during upgrade.
- Total upgrade time bounded.
- Auditable upgrade trail in substrate.

**Unit tests**:
- `TestUpgrade_QuorumAware` — Never drains beyond
  `floor((n-1)/2)`.
- `TestUpgrade_CanaryCohort` — Canary 5%; rollback on anomaly.
- `TestUpgrade_VersionCompatibility` — §21.5 enforcement.
- `TestUpgrade_PerFailureDomainPicker` — One per domain at a
  time.
- `TestUpgrade_AuditTrail` — Substrate records upgrade events.

**Integration tests**:
- `TestUpgrade_LiveTrafficUnaffected` — Live traffic during
  upgrade; no errors.
- `TestUpgrade_BFTSubjectsCoexist` — Mixed Raft + BFT subjects
  upgrade correctly.
- `TestUpgrade_RollbackPath` — Manual rollback works.

**End-to-end tests**:
- `TestUpgradeE2E_FullClusterRollout` — Realistic rollout with
  cross-version compatibility.
- `TestUpgradeE2E_CanaryAnomalyAutoRollback` — Inject anomaly
  during canary; auto-rollback fires.

**Race condition tests**:
- `TestUpgrade_ConcurrentNodeUpgrades` — `go test -race` reconciler
  concurrent with cluster ops; consistent.
- `TestUpgrade_FailoverDuringDrain` — Leader failover concurrent
  with drain; quorum preserved.
- `TestUpgrade_CanaryRollbackRace` — Anomaly detected mid-canary;
  rollback consistent.

**Negative / non-happy path tests**:
- `TestUpgrade_VersionIncompatibility_Refused` — Incompatible
  version refused before any node upgraded.
- `TestUpgrade_NodeFailsToReJoin_Reverts` — Upgraded node fails
  to re-attest / re-join; reconciler reverts to old binary on
  that node.
- `TestUpgrade_QuorumViolationAttempt_Blocked` — Reconciler
  attempts to drain beyond quorum; PDB blocks.
- `TestUpgrade_OperatorAuthorityRevoked_PausesUpgrade` —
  Operator authority revoked mid-upgrade; pauses.
- `TestUpgrade_DiskCorruptionDuringUpgrade_RecoveredViaPeer` —
  Disk corruption mid-upgrade; recovered via §3.6 streaming
  snapshot from peer.
- `TestUpgrade_NetworkPartitionDuringDrain_DefersUntilHeal` —
  Partition during drain; defers; resumes on heal.
- `TestUpgrade_ConcurrentUpgradeAttempts_Serialized` — Two
  operators initiate upgrade; serialized; only one runs.

---

### Phase 22 — Application Primitives (Extended) and Observability

Sagas, CRDTs, optimistic concurrency, workflow primitives, macaroons,
probabilistic structures, sub-subject sharding, adaptive windows,
zero-copy fan-out, OTel traces, counterfactual queries, anomaly feed.
Per §26, §27, §28.

**Phase implementation overview**: Phase 22 is application surface +
performance + observability. Most items are SMs implementing well-known
patterns (sagas, CRDTs, macaroons, sketches) on top of substrate
primitives — substantial code volume but algorithmic risk is low. The
performance items (sub-shard, adaptive window, zero-copy fan-out)
materially change throughput characteristics. OTel + counterfactual +
anomaly feed close the observability loop. Common dependencies:
`go-macaroon`, `axiomhq/hyperloglog`, `caio/go-tdigest`,
`bits-and-blooms/bloom`, OTel SDK, ML libraries (optional for anomaly).

#### 22.1 — Saga primitive

**Description**: Per §26.1.

**Implementation approach**:
- Saga subject `sylk://session/<id>/saga/v1` with state machine.
- Saga ID = `(session_id, hlc)` for uniqueness.
- Coordinator is just an SM transition triggered by step entries;
  no separate coordinator process.
- Steps reference compensation entries via `compensation_ref` field.
- Recovery: SM replay reconstructs in-flight sagas from log.
- Long-running: HLC-keyed timeouts; bounded retries.
- Code path: `core/substrate/primitives/saga/` (~1700 LOC).

**Acceptance criteria**:
- Saga subject + state machine.
- Compensations on step failure (idempotent).
- Coordinator crash recovery: SM replay reconstructs.
- Long-running (hours/days) supported.
- Nested sagas (saga of sagas) supported.
- Saga step failures bounded by retry budget.
- Compensation order: reverse of step order.

**Unit tests**:
- `TestSaga_HappyPath` — All steps complete.
- `TestSaga_FailureCompensates` — Step fails; compensations applied
  in reverse.
- `TestSaga_CoordinatorCrash` — Coordinator dies; recovery completes.
- `TestSaga_IdempotentCompensation` — Re-applied compensation no-op.
- `TestSaga_RetryBudget` — Step retries bounded.
- `TestSaga_HLCTimeout` — Timeout fires at HLC deadline.

**Integration tests**:
- `TestSaga_NestedSagas` — Saga of sagas converges.
- `TestSaga_LongRunning` — 1-hour saga completes correctly.
- `TestSaga_PartitionRecovery` — Saga survives partition.

**End-to-end tests**:
- `TestSagaE2E_AgentWorkflow` — Multi-agent saga; outcome correct.

**Race condition tests**:
- `TestSaga_ConcurrentStepsSameStage` — Multiple steps same stage
  completing concurrently; deterministic resolution.
- `TestSaga_CrashDuringCompensation` — Crash mid-compensation;
  resumes correctly.
- `TestSaga_NestedSagasConcurrent` — `go test -race` nested sagas
  concurrent; no deadlock.

**Negative / non-happy path tests**:
- `TestSaga_StepFailedAfterCompensation_NoDoubleCompensate` —
  Compensation already applied; further failure no-ops.
- `TestSaga_AllStepsFail_FullRollback` — All steps fail;
  compensations roll everything back.
- `TestSaga_CompensationFails_AlertsHumanIntervention` —
  Compensation also fails; saga marked needs-intervention; alerts.
- `TestSaga_DeadlinedSaga_Aborted` — Saga past deadline; aborted +
  compensated.
- `TestSaga_OrphanedSaga_Reaped` — Saga whose session ended;
  reaped after retention.
- `TestSaga_CircularCompensation_DetectedAndAborted` — Saga
  schema-validated to forbid circular compensation refs.

---

#### 22.2 — Typed CRDT subjects

**Description**: Per §26.2.

**Implementation approach**:
- Per CRDT type, separate state machine implementing merge function:
  - G-counter: `map[node_id]uint64`; merge = max per node.
  - PN-counter: pair of G-counters (incs, decs).
  - OR-set: `map[element]set[unique_tag]`; merge = union of tag sets;
    remove erases observed tags only.
  - LWW-map: existing kv with HLC tiebreak.
  - MV-register: keep set of concurrent values.
  - RGA: tree with `(node_id, sequence)` IDs (`automerge`-style).
  - 2P-graph: pair of OR-sets (vertices, edges).
- Code path: `core/substrate/primitives/crdt/` (~5500 LOC across
  types).

**Acceptance criteria**:
- All listed CRDT kinds supported.
- Convergence verified property-test-style under random partitions.
- Cross-DC merges without 2PC.
- Merge functions associative + commutative + idempotent (verified
  with property tests).
- Memory usage bounded per type.
- RGA / 2P-graph: no anomalies under concurrent edits.

**Unit tests**:
- `TestCRDT_GCounter` — Grow-only counter merges.
- `TestCRDT_GCounter_AssociativeMerge` (property) — Associative.
- `TestCRDT_PNCounter` — +/- counter merges.
- `TestCRDT_ORSet_AddRemove` — Observed-remove semantics.
- `TestCRDT_ORSet_RemoveBeforeAddNoop` — Remove of unobserved
  element no-op.
- `TestCRDT_LWWMap` — LWW map converges.
- `TestCRDT_MVRegister` — MV register exposes all concurrent.
- `TestCRDT_RGA_InsertOrdering` — Concurrent inserts converge.
- `TestCRDT_RGA_DeleteWithConcurrentInsert` — Delete + insert
  converge.
- `TestCRDT_Graph_VerticesAndEdges` — 2P-graph converges.
- `TestCRDT_Graph_NoEdgeWithoutVertex` — Invariant.
- `TestCRDT_Convergence_AllKinds` (property) — All kinds converge
  under random partitions.

**Integration tests**:
- `TestCRDT_CrossDCConvergence` — 3 DCs; partition; heal; converge.
- `TestCRDT_LongRunningMerges` — Sustained merge load; bounded
  memory.

**End-to-end tests**:
- `TestCRDTE2E_CollaborativeEdit` — Multi-agent editing; consistency.

**Race condition tests**:
- `TestCRDT_ConcurrentMerges` — `go test -race` concurrent merges
  on same subject; consistent.
- `TestCRDT_RGAConcurrentTreeOps` — Concurrent insert + delete in
  RGA tree; tree invariants preserved.
- `TestCRDT_CrossDCMergeRace` — Cross-DC merges arriving in random
  order; final state same.

**Negative / non-happy path tests**:
- `TestCRDT_TombstoneGrowthBounded` — OR-set tombstone growth
  bounded by GC cycle.
- `TestCRDT_RGAOrphanNodes_Pruned` — RGA orphan tree nodes pruned.
- `TestCRDT_CounterOverflow_BoundedRollover` — Counter near max;
  rolled over with explicit overflow event.
- `TestCRDT_MalformedMerge_Rejected` — Malformed merge input
  rejected; SM continues.
- `TestCRDT_ByzantineFakeIncrement_DetectedViaSig` — Counter
  increment without valid signature rejected.
- `TestCRDT_2PGraphRemoveVertexWithEdges_RemovesEdgesFirst` —
  Vertex removal cascades to edge removal.

---

#### 22.3 — Optimistic concurrency primitive

**Description**: Per §26.3.

**Implementation approach**:
- Publish API extension: `Publish(..., Expect(hlc_frontier))` or
  `Expect(content_hash)`.
- Leader checks current frontier or content; rejects if mismatch
  with `ErrFrontierMismatch`.
- Atomic: check + commit in same Raft entry.
- Code path: `core/substrate/delivery/optimistic.go` (~350 LOC).

**Acceptance criteria**:
- `Publish(..., expect=hlc_frontier)`.
- Conflict returns `ErrFrontierMismatch`.
- Content-addressed expect supported.
- Atomic check + commit.
- No starvation: high-contention publishers eventually succeed
  (Raft fairness preserved).

**Unit tests**:
- `TestOptimistic_HappyPath` — Match succeeds.
- `TestOptimistic_ConflictDetected` — Mismatch rejected.
- `TestOptimistic_ContentExpect` — Content hash expect.
- `TestOptimistic_AtomicCommit` — Check + commit atomic.

**Integration tests**:
- `TestOptimistic_HighContention` — Many concurrent; correct
  serialization.
- `TestOptimistic_NoStarvation` — Fair scheduling.

**End-to-end tests**:
- `TestOptimisticE2E_RealisticContendedSubject` — Hot subject;
  correctness preserved.

**Race condition tests**:
- `TestOptimistic_ConcurrentExpect_OneWins` — Many concurrent
  same-frontier publishes; one wins, others get conflict.
- `TestOptimistic_FrontierAdvanceRace` — Frontier advances during
  check; race resolved deterministically.

**Negative / non-happy path tests**:
- `TestOptimistic_StaleExpect_Rejected` — Frontier already
  advanced; rejected.
- `TestOptimistic_FutureExpect_Rejected` — Frontier in future;
  rejected with specific error.
- `TestOptimistic_ContentExpectMismatch_Rejected` — Content hash
  expect mismatch rejected.
- `TestOptimistic_ExpectOnNonExistentSubject_Rejected` — Subject
  doesn't exist; rejected.
- `TestOptimistic_LeaderChangeDuringExpect_Reproposed` — Leader
  change; new leader re-checks expect; correct outcome.

---

#### 22.4 — Workflow composition primitives

**Description**: Per §26.4. Lease, barrier, counter-with-cap, voting.

**Implementation approach**:
- Each primitive is a small SM:
  - Lease: `map[resource](holder, expiry_hlc)`; substrate timer fires
    expiry.
  - Barrier: `map[barrier_id]set[participant_hlc]`; emits resolution
    at threshold.
  - Counter-with-cap: `map[counter_id]int64`; cap check.
  - Voting: `map[ballot_id]map[voter]vote`; emits resolution at
    threshold.
- Code path: `core/substrate/primitives/workflow/` (~1500 LOC).

**Acceptance criteria**:
- Each primitive correct under partition.
- Lease auto-expires via substrate timer.
- Barrier synchronizes N participants.
- Counter caps publishing.
- Voting emits resolution at threshold.
- All primitives idempotent.

**Unit tests**:
- `TestLease_Acquire` — Lease acquired.
- `TestLease_AutoExpire` — Lease expires at deadline.
- `TestLease_RenewExtends` — Renew extends lease.
- `TestLease_HolderOnlyCanRenew` — Non-holder renew rejected.
- `TestBarrier_SynchronizesNParticipants` — N reach.
- `TestBarrier_PartialReached_NoResolution` — < N reached; no
  resolution.
- `TestCounter_CapsPublishing` — Cap respected.
- `TestCounter_DecrementUnblocks` — Decrement unblocks publishing.
- `TestVoting_Threshold` — Resolution emitted at threshold.
- `TestVoting_DuplicateVoteIgnored` — Idempotent.

**Integration tests**:
- `TestWorkflow_PartitionTolerance` — All primitives correct under
  partition.
- `TestWorkflow_CompositionExample` — Lease + barrier + counter
  composed.

**End-to-end tests**:
- `TestWorkflowE2E_AgentCoordination` — Agents coordinating via
  workflow primitives.

**Race condition tests**:
- `TestLease_ConcurrentAcquire` — `go test -race` many acquires;
  one winner.
- `TestBarrier_ConcurrentArrivals` — Concurrent participant
  arrivals at threshold; resolution emitted exactly once.
- `TestCounter_ConcurrentIncrement` — Concurrent inc/dec; final
  count correct.

**Negative / non-happy path tests**:
- `TestLease_AcquireDuringExpiry_AtomicHandoff` — Acquire during
  expiry; deterministic owner.
- `TestLease_NetworkPartitionDuringHold_ExpiresAfterPartition` —
  Holder partitioned; lease expires; new holder acquires.
- `TestBarrier_MoreThanNParticipants_Rejected` — N+1 participant
  rejected.
- `TestBarrier_TimeoutBeforeThreshold_Aborted` — Barrier timeout;
  aborted.
- `TestCounter_NegativeIncrement_Rejected` — Increment must be
  positive.
- `TestVoting_VoterNotInRoster_Rejected` — Unknown voter rejected.
- `TestWorkflow_RaftGroupRetiredWithActivePrimitives_Cleaned` —
  Retiring group cleans up active primitives.

---

#### 22.5 — Capability macaroons

**Description**: Per §26.5.

**Implementation approach**:
- Library: `gopkg.in/macaroon.v2` (libmacaroons port).
- Embed macaroon in frame trailer (extension).
- Authority predicate validates SVID + macaroon caveat stack.
- Third-party caveats: discharge-fetching service over QUIC.
- One-shot caveats tracked in §6 dedupe (idempotency_key reused).
- Code path: `core/substrate/identity/macaroon.go` (~800 LOC).

**Acceptance criteria**:
- Macaroons with caveats supported (time, predicate, third-party,
  one-shot).
- Authority predicate evaluates SVID + macaroon stack.
- Third-party caveats supported with discharge fetching.
- One-shot caveats tracked in dedupe (single use).
- Delegation works without round-trip to authority issuer.
- Macaroon size bounded (≤ 4KB).

**Unit tests**:
- `TestMacaroon_TimeBoundCaveat` — Time-bound expires.
- `TestMacaroon_PredicateCaveat` — Predicate enforced.
- `TestMacaroon_ThirdPartyCaveat` — Discharge required.
- `TestMacaroon_OneShot` — Single use.
- `TestMacaroon_Composition` — Multiple caveats compose.
- `TestMacaroon_SizeBound` — Bounded ≤ 4KB.

**Integration tests**:
- `TestMacaroon_Delegation` — Delegated capability works without
  round-trip.
- `TestMacaroon_DischargeService` — Real discharge service.

**End-to-end tests**:
- `TestMacaroonE2E_AgentScopedCap` — User grants agent scoped
  capability; enforced cluster-wide.

**Race condition tests**:
- `TestMacaroon_ConcurrentValidation` — `go test -race` 1K
  concurrent; consistent.
- `TestMacaroon_OneShotRace` — Concurrent use of one-shot;
  exactly one succeeds (dedupe wins).

**Negative / non-happy path tests**:
- `TestMacaroon_ExpiredCaveat_Rejected` — Expired time caveat
  rejected.
- `TestMacaroon_TamperedSig_Rejected` — Tampered macaroon detected.
- `TestMacaroon_DischargeFails_Rejected` — Discharge service down;
  caveat fails.
- `TestMacaroon_CaveatPredicateFalse_Rejected` — Predicate evaluates
  false; rejected.
- `TestMacaroon_OversizedMacaroon_Rejected` — > 4KB rejected.
- `TestMacaroon_OneShotReuse_Rejected` — Already-used one-shot
  rejected by dedupe.
- `TestMacaroon_SVIDRevoked_AnyMacaroonInvalid` — SVID revoked;
  macaroon issued by it invalid.

---

#### 22.6 — Probabilistic data structures

**Description**: Per §26.6. HLL, CMS, t-digest, Bloom.

**Implementation approach**:
- HLL: `axiomhq/hyperloglog`.
- CMS: `seiflotfy/cuckoofilter` or custom (stable HLL-style sketch).
- t-digest: `caio/go-tdigest`.
- Bloom: `bits-and-blooms/bloom`.
- Each as substrate subject kind with merge function.
- Code path: `core/substrate/primitives/sketch/` (~600 LOC).

**Acceptance criteria**:
- Each kind supported as subject schema.
- Merge functions associative.
- Cross-DC merge correct (HLL union, CMS sum, t-digest merge,
  Bloom union).
- Bounded memory per subject regardless of cardinality.
- Accuracy within published bounds for each sketch.

**Unit tests**:
- `TestHLL_DistinctCount` — Within accuracy bound.
- `TestHLL_Union_Associative` (property) — Associative merge.
- `TestCMS_FrequencyEstimate` — Within accuracy bound.
- `TestCMS_Merge_Sum` — Sum semantics.
- `TestTDigest_Percentile` — Within accuracy bound.
- `TestTDigest_Merge_Convergence` — Merge converges to ground
  truth.
- `TestBloom_Membership` — Within FP rate.
- `TestBloom_Union` — Set union semantics.

**Integration tests**:
- `TestProbabilistic_CrossDCMerge` — Merges across DCs converge.
- `TestProbabilistic_BoundedMemory` — Memory bounded across
  high-cardinality input.

**End-to-end tests**:
- `TestProbabilisticE2E_LargeCardinalityAnalytics` — Realistic
  high-cardinality analytics.

**Race condition tests**:
- `TestProbabilistic_ConcurrentMerges` — `go test -race` many
  merges; consistent.
- `TestProbabilistic_RaceWithSnapshot` — Snapshot during merge;
  consistent.

**Negative / non-happy path tests**:
- `TestProbabilistic_AccuracyDegradesGracefully` — Cardinality far
  exceeds expected; accuracy degrades but no crash.
- `TestProbabilistic_MalformedSketch_Rejected` — Malformed sketch
  bytes rejected.
- `TestProbabilistic_VersionMismatchedMerge_Rejected` — Different
  parameter sets cannot merge.
- `TestProbabilistic_ParameterMigration_Rebuilt` — Parameter change
  rebuilds sketch from source.

---

#### 22.7 — Sub-subject sharding

**Description**: Per §27.1.

**Implementation approach**:
- Subject schema declares `shards: N` (power of 2).
- Each shard is its own Raft group, parented under the namespace
  group.
- Cursor extended: `HLCFrontier []HLC` (one per shard).
- Caller-transparent via routing layer; routing hashes
  partition_key into shard ID.
- Code path: `core/substrate/storage/subshard.go` (~2000 LOC).

**Acceptance criteria**:
- Hash-shard partition_key into N sub-Rafts (power of 2).
- Cursor frontier vector across shards.
- Caller transparency: subject API unchanged.
- Throughput scales linearly with shard count up to expected
  hardware limit.
- Shard count immutable post-creation (rebalance via subject
  migration).
- Partition_key skew detected and reported.

**Unit tests**:
- `TestSubShard_PartitionDistribution` — Balanced for uniform
  partition_key.
- `TestSubShard_CursorVector` — Frontier correctly tracks per
  shard.
- `TestSubShard_CallerTransparency` — Caller code unchanged.
- `TestSubShard_HashStable` — Same partition_key → same shard.

**Integration tests**:
- `TestSubShard_HighThroughput` — Throughput scales with shard
  count.
- `TestSubShard_CursorResumeAcrossShards` — Cursor resume
  consistent.

**End-to-end tests**:
- `TestSubShardE2E_HotSubject` — Massively hot subject; throughput
  meets target.

**Race condition tests**:
- `TestSubShard_ConcurrentPublishesAcrossShards` — `go test -race`
  parallel publishes to different shards; no cross-shard race.
- `TestSubShard_CursorConcurrentAdvance` — Concurrent cursor
  advances per shard; vector consistent.

**Negative / non-happy path tests**:
- `TestSubShard_PartitionKeySkew_Reported` — Skew detected;
  reported via anomaly subject.
- `TestSubShard_ShardLeaderDeath_OnlyAffectsThatShard` — One
  shard's leader dies; only that shard's writes pause.
- `TestSubShard_NonPowerOfTwoShardCount_Rejected` — N not power
  of 2 rejected at creation.
- `TestSubShard_CrossShardOrderingNotGuaranteed` — Documented
  behavior; cross-shard order via HLC only.

---

#### 22.8 — Adaptive group commit

**Description**: Per §27.3.

**Implementation approach**:
- PID controller (k_p, k_i, k_d tuned offline) targeting p99
  latency.
- Observation: rolling p99 over 1s; output: commit window in
  [100µs, 50ms].
- Adjust every 100ms.
- Code path: `core/substrate/storage/adaptive_commit.go` (~500
  LOC).

**Acceptance criteria**:
- Adaptive window targets configurable latency p99.
- Bounded between min/max (default 100µs to 50ms).
- Stable under load swings (no oscillation).
- Adjustment cadence ≤ 100ms.
- PID parameters tunable per subject class.

**Unit tests**:
- `TestAdaptive_ConvergesToTarget` — Settles on window.
- `TestAdaptive_BoundedOscillation` — No flapping.
- `TestAdaptive_WindowBounded` — Min/max enforced.
- `TestAdaptive_PIDStability` — Step input → stable output.

**Integration tests**:
- `TestAdaptive_LoadSwings` — Sudden load change; settles fast.
- `TestAdaptive_PerClassPolicy` — Different classes have different
  windows.

**End-to-end tests**:
- `TestAdaptiveE2E_VariedWorkload` — Realistic varied workload;
  latency met.

**Race condition tests**:
- `TestAdaptive_ConcurrentObservation` — `go test -race`
  observation + adjustment; consistent.

**Negative / non-happy path tests**:
- `TestAdaptive_PathologicalLatency_FallsBackToMax` — Pathological
  latency; window saturates at max; no oscillation.
- `TestAdaptive_ZeroTraffic_NoAdjustment` — Zero traffic; no
  observation; no adjustment.
- `TestAdaptive_BadPIDConfig_RejectedAtSetup` — Unstable PID
  parameters rejected at config.

---

#### 22.9 — Zero-copy fan-out

**Description**: Per §27.4.

**Implementation approach**:
- Per-node content-addressed cache: blake3 → mmap region.
- Delivery sends `(header, body_hash)` + body via separate stream;
  consumers fetch body from cache.
- Multicast tree for massive fan-out: leader designates per-DC
  forwarder via topology hint; forwarder propagates to local
  consumers.
- Code path: `core/substrate/delivery/fanout.go` (~1800 LOC).

**Acceptance criteria**:
- Body fetched once per node (regardless of consumers per node).
- Multicast tree for massive fan-out.
- Bandwidth saving measurable (≥ 10x for 100+ consumers per node).
- Cache LRU-evicts; bounded memory.
- Tree resilient to forwarder failure.

**Unit tests**:
- `TestFanOut_NodeLocalCache` — Body fetched once.
- `TestFanOut_MulticastTree` — Tree structure correct.
- `TestFanOut_CacheLRU` — Eviction under memory pressure.
- `TestFanOut_BandwidthSavings` — Measurable saving.

**Integration tests**:
- `TestFanOut_HighSubscriberCount` — 10K subscribers; bandwidth
  bounded.
- `TestFanOut_ForwarderFailover` — Forwarder dies; new forwarder
  picked.

**End-to-end tests**:
- `TestFanOutE2E_GlobalBroadcast` — Massive fan-out; leader
  bandwidth bounded.

**Race condition tests**:
- `TestFanOut_ConcurrentFetch` — `go test -race` many concurrent
  fetches of same body; one fetch + N reads.
- `TestFanOut_CacheEvictionDuringFetch` — Eviction concurrent
  with fetch; reader either gets cached or refetches; no double-
  free.
- `TestFanOut_TreeReorganizationRace` — Tree change during
  delivery; consistent.

**Negative / non-happy path tests**:
- `TestFanOut_BodyMissingFromCache_Refetched` — Body not in cache;
  refetched from leader.
- `TestFanOut_TamperedBodyAtNode_Rejected` — Cache returns body
  with mismatched hash; rejected; refetched.
- `TestFanOut_ForwarderTreeCycle_DetectedAndBroken` — Cycle
  detection; tree rebuilt.
- `TestFanOut_MultipleForwardersSameDC_Deterministic` —
  Deterministic forwarder selection; tied selection by node ID.
- `TestFanOut_ConsumerCrashDuringFanOut_OnlyThatConsumerAffected`
  — One consumer dies; tree continues; other consumers unaffected.

---

#### 22.10 — OpenTelemetry trace integration

**Description**: Per §28.1.

**Implementation approach**:
- Substrate consumer subscribed to subjects (operator-configurable).
- Translates entry → OTLP span via `go.opentelemetry.io/otel`.
- Span links from causal parent edges.
- Sampling: head-based at causal-cone level (hash root cone ID
  modulo sample rate).
- Bridges to OTel collectors.
- Code path: `core/substrate/observability/otel/` (~900 LOC).

**Acceptance criteria**:
- Substrate consumer translates entries to OTLP.
- Span links from causal parents.
- HLC-aware ordering.
- Sampling at causal-cone level.
- Bridges to standard OTel collectors.
- Bounded overhead: ≤ 5% on publish path.

**Unit tests**:
- `TestOTel_SpanFromEntry` — Entry to span.
- `TestOTel_LinksFromParents` — Causal parents to span links.
- `TestOTel_CausalConeSampling` — Sampling preserves cones.
- `TestOTel_HLCAwareOrdering` — Span timestamps respect HLC.

**Integration tests**:
- `TestOTel_CollectorIntegration` — Real OTel collector receives
  spans.
- `TestOTel_OverheadBounded` — Publish overhead ≤ 5%.

**End-to-end tests**:
- `TestOTelE2E_RealSession` — Real session; trace explores
  correctly.

**Race condition tests**:
- `TestOTel_ConcurrentSpanEmission` — `go test -race` concurrent
  span emissions; consistent.

**Negative / non-happy path tests**:
- `TestOTel_CollectorUnreachable_BackpressureNotPropagated` —
  Collector down; OTel exporter buffers; no impact on publish path.
- `TestOTel_OversizedSpan_Truncated` — Very large span; truncated
  with marker.
- `TestOTel_SamplingZero_NoSpansEmitted` — 0% sampling; no spans.
- `TestOTel_HLCSkewBetweenSpans_Tolerated` — HLC skew across
  spans; sorted correctly.

---

#### 22.11 — Counterfactual queries

**Description**: Per §28.2.

**Implementation approach**:
- API: `WhatIfWithout(entry_ref, depth)`.
- Spawn temp namespace as fork at entry's HLC; replay forward
  skipping the entry; diff state at horizon; destroy temp.
- Reuses §19.4 PITR machinery.
- Code path: `core/substrate/observability/counterfactual.go` (~500
  LOC).

**Acceptance criteria**:
- Fork at HLC; replay without entry; diff.
- Cost: O(replay-from-snapshot).
- Temp namespace cleanup automatic.
- Multiple counterfactuals can run concurrently.

**Unit tests**:
- `TestCounterfactual_Diff` — Excluded entry; diff visible.
- `TestCounterfactual_BoundedCost` — Cost bounded by snapshot
  frequency.
- `TestCounterfactual_TempCleanup` — Temp namespace cleaned up.

**Integration tests**:
- `TestCounterfactual_RealisticDebug` — Real bug scenario.
- `TestCounterfactual_ConcurrentQueries` — Multiple counterfactuals.

**End-to-end tests**:
- `TestCounterfactualE2E_DebugSession` — Session debug via
  counterfactual.

**Race condition tests**:
- `TestCounterfactual_TempCleanupRace` — Cleanup race with
  concurrent query; no use-after-free.

**Negative / non-happy path tests**:
- `TestCounterfactual_EntryBeforeRetention_Refused` — Entry past
  retention; refused.
- `TestCounterfactual_DepthTooLarge_Refused` — Depth exceeds limit;
  refused.
- `TestCounterfactual_TempNamespaceCreationFails_Cleanup` —
  Creation fails; partial state cleaned up.
- `TestCounterfactual_ConcurrentSiblingClash_Resolved` — Two
  counterfactuals try same temp name; deterministic.

---

#### 22.12 — Anomaly detection feed

**Description**: Per §28.3.

**Implementation approach**:
- Counters in each substrate subsystem (φ score, retry budget,
  quota usage, compaction backlog, skew, cold-tier rate).
- Aggregator publishes to `sylk://global/observability/v1`.
- Cardinality bounded; no per-entry labels.
- Time-travelable.
- Code path: `core/substrate/observability/anomaly.go` (~500 LOC).

**Acceptance criteria**:
- Substrate publishes self-metrics to subject.
- Cardinality bounded (no per-entry labels).
- Time-travelable.
- Operator-friendly schema.
- Bounded overhead: ≤ 1% on hot paths.

**Unit tests**:
- `TestAnomaly_MetricPublished` — Each metric published.
- `TestAnomaly_CardinalityBounded` — No unbounded labels.
- `TestAnomaly_TimeTravelable` — Past metric values queryable.

**Integration tests**:
- `TestAnomaly_DashboardConsumes` — Dashboard reads subject.
- `TestAnomaly_OverheadBounded` — ≤ 1% overhead.

**End-to-end tests**:
- `TestAnomalyE2E_RealCluster` — Real cluster; dashboard reflects.

**Race condition tests**:
- `TestAnomaly_ConcurrentMetricUpdates` — `go test -race`
  concurrent counter updates; final values exact.

**Negative / non-happy path tests**:
- `TestAnomaly_PublishFailure_BufferedRetry` — Publish fails;
  buffered + retried.
- `TestAnomaly_HighFrequencyDoesntOverwhelm` — High-frequency
  metric updates coalesced into bounded publishes.
- `TestAnomaly_SubjectRetentionAffectsHistoricalQuery` — Retention
  expiry; old metrics gone; expected behavior.

---

### Phase 23 — Embedded Hardening, Catastrophic Recovery, Envelope-Pushing

Memory pressure, disk-full, battery, shared memory, SM rollback, key
escrow, backup verification, geo-fence recovery, quorum-loss recovery,
tiered programmable SMs (DSL → native Go → optional WASM), ZK proofs,
causal isolation levels, differential dataflow, DIDs, substrate-as-
database, PQC, formal methods, interop, blockchain anchoring, wire
transform registry, native agent runtime, geo-fenced CRDTs,
self-replicating cluster, simulation harness. Per §29, §30, §31.

**Phase implementation overview**: Phase 23 is the longest and most
heterogeneous. It bundles three thematic clusters: (a) embedded-mode
robustness (§29), (b) catastrophic recovery (§30), and (c) envelope-
pushing primitives (§31.1-§31.20). Items are grouped here because
they're independent of each other and don't fit cleanly into earlier
phases. Each item lands behind its own feature flag. Many items pull
in heavyweight external dependencies (DSL compilers, gnark, blockchain
SDKs, optional WASM runtimes); items can ship in any order subject to
local prerequisites.

#### 23.1 — Memory pressure backpressure

**Description**: Per §29.1.

**Implementation approach**:
- Linux: read `/proc/pressure/memory` (PSI) periodically (250ms);
  threshold-based response.
- macOS: `kern_event` notification on memory pressure (or sysctl
  `kern.memorystatus_*`).
- Windows: `GlobalMemoryStatusEx` polling.
- Response: pause Bulk + Background classes (block on quota); shrink
  in-memory body cache; `madvise(DONTNEED)` for cold segments;
  defer compaction.
- Code path: `core/substrate/embedded/memory_pressure.go` (~700 LOC
  + per-platform shim).

**Acceptance criteria**:
- OS pressure signal observed every 250ms.
- Bulk + Background paused at threshold.
- Cache shrunk proportionally to pressure level.
- `madvise(DONTNEED)` applied to cold segments.
- Compaction deferred during pressure.
- Critical class preserved.
- Surfaces "system under pressure" event to TUI / observability.
- Recovery: pressure clears → classes resume; cache regrows
  gradually.

**Unit tests**:
- `TestPressure_BulkPaused` — Pause on signal.
- `TestPressure_CacheShrinks` — Cache shrinks under pressure.
- `TestPressure_CriticalUnaffected` — Critical class continues.
- `TestPressure_MadviseApplied` — `madvise` syscall fired.
- `TestPressure_RecoveryResumes` — Pressure clears; classes
  resume.

**Integration tests**:
- `TestPressure_LinuxCgroup` — Linux cgroup signal consumed.
- `TestPressure_DarwinPressure` — macOS pressure signal consumed.
- `TestPressure_WindowsMemStatus` — Windows polling.

**End-to-end tests**:
- `TestPressureE2E_LowMemoryLaptop` — Constrained memory; substrate
  degrades gracefully.

**Race condition tests**:
- `TestPressure_PauseDuringActivePublish` — `go test -race` pause
  during in-flight publishes; clean handoff.
- `TestPressure_CacheShrinkConcurrentReads` — Cache shrunk
  concurrent with reads; readers either hit cache or refetch.
- `TestPressure_RapidPressureChange` — Rapid pressure on/off; no
  flapping.

**Negative / non-happy path tests**:
- `TestPressure_PSIUnavailable_FallsBackToPolling` — PSI not
  available on older kernels; falls back to polling.
- `TestPressure_ExtremeMemoryStarvation_GracefulOOMAvoidance` —
  Memory near OOM; substrate sheds aggressively, doesn't crash.
- `TestPressure_FalsePositive_BoundedDisruption` — False
  pressure signal; brief disruption bounded by re-evaluation
  interval.
- `TestPressure_PressureRefuseCriticalDecline` — Even at extreme
  pressure, Critical not refused (only delayed slightly).
- `TestPressure_PauseAlreadyPausedClass_NoOp` — Pause re-applied
  to already-paused class no-ops.

---

#### 23.2 — Disk-full graceful degradation

**Description**: Per §29.2.

**Implementation approach**:
- Per-subject reservation tracked in operator group.
- `statfs()` polled every 10s + pre-write reservation check.
- Reservation exhaustion → `ErrDiskFull`.
- Cold-tier reaper frees space async.
- Code path: `core/substrate/embedded/disk_full.go` (~500 LOC).

**Acceptance criteria**:
- Per-subject reservations.
- Reservation hit → publish rejected with `ErrDiskFull` (not
  silent corruption).
- Cold-tier upload reaper frees local space asynchronously.
- Active segment writes refused before disk corruption is possible.
- Surfaced as `sylk://global/storage-pressure/v1` events.
- Recovery: space freed → publishes resume.
- Reservation enforcement applies to both single-subject and
  cluster-wide pressure.

**Unit tests**:
- `TestDiskFull_ReservationEnforced` — Reservation blocks publish.
- `TestDiskFull_ColdReaper` — Reaper frees space.
- `TestDiskFull_ErrReturnedNotSilentCorruption` — Returns specific
  error.
- `TestDiskFull_ReservationGrace` — Small headroom for in-flight
  writes.

**Integration tests**:
- `TestDiskFull_RealisticFill` — Disk fills; substrate degrades.
- `TestDiskFull_ReaperConcurrentWithWrites` — Reaper concurrent with
  writes; bounded behavior.

**End-to-end tests**:
- `TestDiskFullE2E_LongSession` — Long session; disk pressure;
  substrate recovers.

**Race condition tests**:
- `TestDiskFull_ConcurrentReservationCheck` — `go test -race` many
  concurrent reservation checks; consistent.
- `TestDiskFull_ReaperAndPublishRace` — Reaper frees space race
  with new publishes; deterministic outcome.

**Negative / non-happy path tests**:
- `TestDiskFull_ReaperUnableToFree_BackpressureContinues` — Reaper
  can't free (e.g., no cold tier configured); backpressure
  sustained; cluster-wide alert.
- `TestDiskFull_StatfsFails_ConservativeRefuse` — `statfs` syscall
  fails; conservative behavior (refuse new writes); not panic.
- `TestDiskFull_DiskQuotaPerTenantHit_OnlyTenantAffected` —
  Tenant disk quota (§22.1) hit; only that tenant blocked.
- `TestDiskFull_ReservationOverflow_Refused` — Reservation request
  beyond available; refused at registration.
- `TestDiskFull_PartialWriteCorruption_DetectedOnRecovery` — Crash
  mid-write near full; recovery detects partial; truncates.

---

#### 23.3 — Battery / thermal awareness

**Description**: Per §29.3.

**Implementation approach**:
- Linux: D-Bus `org.freedesktop.UPower` via `godbus/dbus`.
- macOS: `IOPMrootDomain` via cgo (or `pmset` shell-out).
- Windows: `SystemPowerStatus` via `golang.org/x/sys/windows`.
- Response: throttle background compaction, defer snapshotting,
  skip non-essential dedupe maintenance, reduce HLC stamping rate
  for Background class, defer cold-tier migration.
- Code path: `core/substrate/embedded/power.go` (~600 LOC + per-
  platform shim).

**Acceptance criteria**:
- OS power signals consumed.
- Background work throttled when on battery / thermally throttled.
- Critical work unaffected.
- Cooperative with OS power signals (each platform).
- Recovery: AC + cool → resume normal cadence.

**Unit tests**:
- `TestPower_BackgroundThrottled` — Background reduced.
- `TestPower_CriticalUnaffected` — Critical normal.
- `TestPower_ThermalSignalConsumed` — Thermal pressure detected.
- `TestPower_ACReturnedResumes` — AC restored; resumes.

**Integration tests**:
- `TestPower_LinuxUPower` — Linux UPower D-Bus integration.
- `TestPower_MacOSPowerEvents` — macOS power events.

**End-to-end tests**:
- `TestPowerE2E_BatteryWorkflow` — On battery; substrate behaves
  appropriately.

**Race condition tests**:
- `TestPower_RapidACToggling` — AC plug/unplug rapidly; no
  oscillation in throttle level.
- `TestPower_ConcurrentBatteryEvents` — `go test -race` event
  consumption.

**Negative / non-happy path tests**:
- `TestPower_DBusUnavailable_FallsBackToFile` — D-Bus down; falls
  back to `/sys/class/power_supply` reads.
- `TestPower_PMSetUnavailable_DefaultsToAC` — macOS `pmset` fails;
  defaults to AC mode.
- `TestPower_NoPowerSignal_DefaultsToAC` — No signal source; AC mode.
- `TestPower_LowBatteryDoesntKillCritical` — Even at critical
  battery, Critical-class operations continue.

---

#### 23.4 — Shared-memory transport

**Description**: Per §29.4.

**Implementation approach**:
- `tmpfs`-backed mmap region (Linux: `/dev/shm`).
- Lockfree MPSC ring buffer (Vyukov algorithm; port via custom or
  `lni/goutils/ringbuffer`).
- HLC fence in ring header: writer publishes entry then RMB +
  release-store on tail; reader acquires-load + RMB before read.
- Same wire format as channel transport.
- Used opportunistically when both ends on same NUMA node;
  fallback to Unix socket.
- Code path: `core/substrate/transport/shm.go` (~900 LOC).

**Acceptance criteria**:
- SPSC / MPSC ring buffer with lockfree semantics.
- ~100ns latency p50.
- Same wire format as other transports.
- Falls back to Unix socket gracefully.
- Bounded memory; ring slot count fixed at creation.
- HLC fences preserve happens-before.

**Unit tests**:
- `TestSharedMem_RoundTrip` — Round-trip.
- `TestSharedMem_LatencyTarget` — < 100ns p50.
- `TestSharedMem_HLCFenceCorrectness` — Happens-before preserved.
- `TestSharedMem_MPSCCorrectness` — Multi-producer single-consumer
  no torn writes.
- `TestSharedMem_FullBufferBackpressure` — Full ring blocks
  writer.

**Integration tests**:
- `TestSharedMem_BetweenProcesses` — Two processes communicate.
- `TestSharedMem_FallbackToUnixSocket` — When SHM unavailable,
  falls back.

**End-to-end tests**:
- `TestSharedMemE2E_KnowledgeAgent` — Knowledge stack to agent via
  shared memory.

**Race condition tests**:
- `TestSharedMem_ManyProducersOneConsumer` — `go test -race` 100
  producers + 1 consumer; correct ordering.
- `TestSharedMem_MemoryOrderingARM64` — Run on ARM64; barriers
  correct under weaker memory model.
- `TestSharedMem_ProducerCrashMidWrite_ReaderRecovers` — Producer
  crashes mid-frame-write; reader detects torn frame; skips.

**Negative / non-happy path tests**:
- `TestSharedMem_TmpfsUnavailable_FallsBack` — `/dev/shm` not
  mounted; fallback.
- `TestSharedMem_RingCorruption_DetectedAndAborted` — Ring
  corruption (e.g., header tampered); detected; transport
  aborted.
- `TestSharedMem_ProducerStalled_NoConsumerStall` — Producer
  blocks on full ring; consumer continues with available frames.
- `TestSharedMem_DifferentNUMANode_NoOptimization` — Producers /
  consumer on different NUMA; falls back to other transport.
- `TestSharedMem_ProcessBoundary_SecurityCheck` — Other process
  with mmap permission tries to write; blocked by SVID check at
  receive.

---

#### 23.5 — SM rollback subject

**Description**: Per §30.1 fourth defense.

**Implementation approach**:
- Operator-issued via authority broadcast (§11.7).
- Replicas roll back to last snapshot at prior version (snapshot
  index from §21.5 SM versioning); replay forward applying entries
  through prior SM.
- Quarantined entries from §21.6 flagged for re-apply.
- Code path: `core/substrate/sm/rollback.go` (~700 LOC; overlaps
  with §21.5).

**Acceptance criteria**:
- Operator-issued rollback signal.
- Replicas roll back state; replay from last-snapshot-with-prior-
  version.
- Quarantined entries flagged for re-apply on next version.
- Rollback auditable in substrate.
- Multi-replica consistency: all replicas roll back to same
  snapshot deterministically.

**Unit tests**:
- `TestSMRollback_StateReverted` — State reverted.
- `TestSMRollback_Replay` — Replay produces correct state.
- `TestSMRollback_QuarantinedFlagged` — Quarantined entries
  flagged.
- `TestSMRollback_AuditTrail` — Rollback events recorded.

**Integration tests**:
- `TestSMRollback_LiveCluster` — Live cluster rolled back.
- `TestSMRollback_AllReplicasConsistent` — Multi-replica rollback
  consistent.

**End-to-end tests**:
- `TestSMRollbackE2E_BugIncident` — Realistic bug rolled back;
  cluster recovers.

**Race condition tests**:
- `TestSMRollback_RollbackDuringActiveApply` — Rollback signal
  during active SM apply; clean abort.
- `TestSMRollback_ConcurrentRollbackProposals` — Multiple operators
  propose rollback; deterministic resolution.

**Negative / non-happy path tests**:
- `TestSMRollback_NoSnapshotAtPriorVersion_Refused` — No snapshot
  at prior SM version; rollback refused.
- `TestSMRollback_BeyondRetention_Refused` — Target before
  retention; refused.
- `TestSMRollback_OperatorAuthorityRevoked_PartiallyExecuted` —
  Authority revoked mid-rollback; pauses; recoverable on
  re-authority.
- `TestSMRollback_PartialRollback_AllReplicasResume` — Partial
  failure; all replicas reconcile to consistent state.

---

#### 23.6 — Shamir-escrowed key recovery

**Description**: Per §30.2.

**Implementation approach**:
- Library: `hashicorp/vault/shamir`.
- KEK split M-of-N at creation; shares distributed to operator
  parties out-of-band.
- Recovery workflow: operator UI collects M shares; reconstruct
  KEK; re-encrypt envelopes.
- KEK rotation re-issues escrow shares tied to specific KEK epoch.
- Code path: `core/substrate/recovery/shamir.go` (~500 LOC) + UI.

**Acceptance criteria**:
- DEK escrow via Shamir M-of-N.
- KEK rotation re-issues escrow.
- Recovery requires M parties; M-1 shares insufficient.
- Old shares cryptographically tied to specific KEK epoch.
- Recovery workflow auditable.
- M-of-N quorum verifiable (signatures from authorized operators).

**Unit tests**:
- `TestShamir_MofNRecovery` — Recovery succeeds with M.
- `TestShamir_LessThanMFails` — Fewer than M fails.
- `TestShamir_RotationReissues` — Rotation re-issues correctly.
- `TestShamir_OldEpochSharesInvalid` — Old shares fail after
  rotation.
- `TestShamir_ShareCorruption_DetectedDuringRecovery` — Bad share
  detected.

**Integration tests**:
- `TestShamir_RealHSMs` — Multiple HSMs; recovery flow.
- `TestShamir_OperatorSignatureCollected` — Operator signatures
  collected via authority.

**End-to-end tests**:
- `TestShamirE2E_DRDrill` — Practiced DR drill.

**Race condition tests**:
- `TestShamir_ConcurrentShareSubmission` — `go test -race`
  concurrent submissions; correct combination.
- `TestShamir_RotationRaceWithRecovery` — Recovery in progress
  when rotation fires; deterministic.

**Negative / non-happy path tests**:
- `TestShamir_ForgedShare_Rejected` — Forged share detected via
  signature.
- `TestShamir_DuplicateShare_TreatedAsOne` — Same operator
  submits twice; counts once.
- `TestShamir_ShareLeak_RotationInvalidates` — Suspected leak;
  rotation invalidates leaked epoch.
- `TestShamir_RecoverySigVerificationFails_Aborted` — Operator
  signature verification fails; abort.
- `TestShamir_HSMDown_FallbackToOfflineRecovery` — HSM
  unavailable; offline recovery procedure documented.

---

#### 23.7 — Backup verification before restore

**Description**: Per §30.3.

**Implementation approach**:
- Multi-source root list: cross-cloud immutable storage (S3 +
  GCS + Azure) + Sigstore Rekor (`sigstore/rekor-cli` or
  `sigstore-go`) + hardware-token signed roots.
- Restore protocol: fetch K independent roots, require unanimous
  match, abort if any disagrees.
- Code path: `core/substrate/recovery/backup_verify.go` (~700 LOC).

**Acceptance criteria**:
- Multiple independent root sources.
- Unanimous match required.
- Disagreement aborts restore with specific error.
- Sigstore Rekor integration optional.
- Hardware-token signature verification optional.
- Restore audit logged.

**Unit tests**:
- `TestBackupVerify_UnanimousMatch` — Match → proceed.
- `TestBackupVerify_DisagreementAborts` — Disagreement → abort.
- `TestBackupVerify_SigstoreIntegration` — Rekor lookup correct.
- `TestBackupVerify_HardwareTokenSig` — Hardware token sig
  validation.

**Integration tests**:
- `TestBackupVerify_TamperedDetected` — Tampered backup detected.
- `TestBackupVerify_PartialSourceUnavailable_FallsBackToRequired` —
  Some sources down; if required count met, proceeds.

**End-to-end tests**:
- `TestBackupVerifyE2E_RealRestore` — Real restore flow.

**Race condition tests**:
- `TestBackupVerify_ConcurrentRootFetch` — `go test -race` parallel
  root fetches; consistent comparison.

**Negative / non-happy path tests**:
- `TestBackupVerify_AllSourcesDown_RestoreRefused` — All sources
  down; restore refused (fail-closed).
- `TestBackupVerify_SilentMajority_DetectedAsTamper` — Most sources
  return same wrong root; detected via independent verifier.
- `TestBackupVerify_RekorEntryMissing_FallsBackToOtherSources` —
  Rekor entry not yet propagated; other sources used.
- `TestBackupVerify_PartialBackupRestored_FailsConsistencyCheck` —
  Backup missing entries; restore consistency check fails;
  refused.
- `TestBackupVerify_SourcesAgreeButTamperedAtRest` — All sources
  agree on tampered root; secondary check (e.g., chain to known
  good HLC) catches.

---

#### 23.8 — Geo-fence violation recovery

**Description**: Per §30.4.

**Implementation approach**:
- Self-audit: periodic operator-group check of replica placement
  vs `NamespacePlacement` CRD (§25.1) geo-fences.
- Containment: seal replica (ACL flip to read-only).
- Migration: joint consensus to compliant region; old replica
  destroyed; KEK rotation.
- Forensic event mandatory.
- Code path: `core/substrate/recovery/geofence.go` (~900 LOC).

**Acceptance criteria**:
- Self-audit detects placement violations periodically.
- Containment + migration automatic upon detection.
- Forensic audit trail (when, what data, potentially read by whom).
- Migration without data loss; KEK rotation post-migration.
- Operator notified.

**Unit tests**:
- `TestGeoFence_SelfAuditDetects` — Detection.
- `TestGeoFence_ContainmentSeals` — Replica sealed.
- `TestGeoFence_MigrationFlow` — Move to compliant region.
- `TestGeoFence_KEKRotationPostMigration` — KEK rotated.
- `TestGeoFence_ForensicEventMandatory` — Forensic event recorded.

**Integration tests**:
- `TestGeoFence_LiveMigration` — Migration without service
  interruption.
- `TestGeoFence_OperatorNotification` — Notification sent.

**End-to-end tests**:
- `TestGeoFenceE2E_ComplianceIncident` — Realistic compliance
  incident resolved.

**Race condition tests**:
- `TestGeoFence_ViolationDuringMigration_HandledSequentially` —
  Concurrent violations during migration; serialized handling.
- `TestGeoFence_MigrationRaceWithCRDPolicyChange` — Policy
  changed mid-migration; restarts with new policy.

**Negative / non-happy path tests**:
- `TestGeoFence_NoCompliantRegion_BlocksWithAlert` — No region
  satisfies policy; alert; substrate refuses to operate that
  namespace.
- `TestGeoFence_MigrationFailsHalfway_PartialState` — Migration
  fails; partial state; eventual consistency reached on retry.
- `TestGeoFence_FalsePositiveAudit_ReversibleContainment` — False
  positive; containment reversible without data loss.
- `TestGeoFence_PolicyChangeMakesEverythingNonCompliant_OperatorOverride`
  — Policy change requires global migration; operator must
  authorize.

---

#### 23.9 — Quorum-loss force-recovery

**Description**: Per §30.6.

**Implementation approach**:
- Operator M-of-N authorization: collect M signed approvals via
  authority broadcast.
- Force-elect: bypass quorum invariant via operator authority
  capability.
- New replica from snapshot + cold backup.
- Force-elected leader's first action: publish forensic event with
  operator authorization to `sylk://global/quorum-recovery/v1`.
- Code path: `core/substrate/recovery/quorum_loss.go` (~700 LOC).

**Acceptance criteria**:
- Operator M-of-N quorum required.
- Recovery from snapshot + cold backup.
- Forensic event mandatory.
- Force-elected leader auditable.
- Recovery preserves identity continuity (term ID monotonic).

**Unit tests**:
- `TestQuorumLoss_RequiresMofN` — Without M-of-N, refused.
- `TestQuorumLoss_Forensic` — Forensic event published.
- `TestQuorumLoss_TermMonotonic` — New term > all previous.
- `TestQuorumLoss_AuthorityVerified` — M signatures verified.

**Integration tests**:
- `TestQuorumLoss_FromBackup` — Recover from cold backup.
- `TestQuorumLoss_PostRecoveryReplication` — New replicas catch up
  cleanly.

**End-to-end tests**:
- `TestQuorumLossE2E_DCPermanentLoss` — Simulated DC permanent
  loss; recovery flow.

**Race condition tests**:
- `TestQuorumLoss_ConcurrentForceElectAttempts` — Multiple
  operators initiate; deterministic resolution.
- `TestQuorumLoss_OldReplicaResurfaces_DetectedNoSplit` — Long-
  lost replica returns post-recovery; detected; not allowed to
  split-brain.

**Negative / non-happy path tests**:
- `TestQuorumLoss_FewerThanMSigs_Refused` — < M signatures
  refused.
- `TestQuorumLoss_BackupCorruption_RecoveryAborts` — Backup
  verification fails; abort.
- `TestQuorumLoss_PartialRecoveryStuck_OperatorIntervention` —
  Stuck; operator can manually override.
- `TestQuorumLoss_ForgedOperatorSig_Rejected` — Forged signature
  detected.
- `TestQuorumLoss_RecoveryFromStaleSnapshot_DataLossDocumented` —
  Recover from stale snapshot; data-loss window documented in
  forensic event.

---

#### 23.10 — Tiered programmable state machines

**Description**: Per §31.1. Three-tier model: declarative DSLs
(default), native Go SMs with reproducible-build provenance (trusted
extensions), optional WASM (escape hatch behind feature flag).

**Implementation approach**:
- **Tier 1 — DSL codegen**: stored procedure / projection / authority
  / wire-validator DSLs. Parser + typechecker + native-Go emitter.
  Compilation happens at registration time; result cached to disk.
  No runtime VM; emitted Go is linked + loaded via §24.6 binary
  hash matching.
- **Tier 2 — Native Go SMs**: existing SM interface (§24.2 `Apply`,
  `Version`); registered via reproducible-build hash; cluster-
  admitted via §24.6 approved-set check.
- **Tier 3 — Optional WASM**: gated behind `extensions=wasm` feature
  flag and explicit per-subject opt-in. Library: `wasmtime-go`.
  Module deployed as substrate object (§11.2); activation pivot at
  HLC; host functions whitelisted (storage R/W, HLC, seeded RNG,
  structured logging); determinism enforced by WASM constraints +
  §24.1 harness. **Never on the critical path of core SM apply** —
  WASM SMs are an extension point, not a foundational mechanism.
- Code path: `core/substrate/sm/dsl/` (~2500 LOC for Tier 1
  compilers); `core/substrate/sm/version/` reuses §24.2 (~0 net
  Tier 2 code); `core/substrate/sm/wasm/` (~2500 LOC for Tier 3,
  feature-flagged off by default).

**Acceptance criteria**:
- DSL → native Go codegen at registration time; ≤ 1ms hot-path
  overhead per apply over hand-written Go.
- Native-Go Tier 2 SMs deployable via §24.6 reproducible build;
  cluster-admitted only with approved hash.
- WASM Tier 3 gated behind feature flag; disabled by default; if
  enabled, sandboxed apply with whitelisted host functions, ≤ 5ms
  apply latency budget, module size ≤ 8MB.
- All three tiers respect §24.2 SM versioning, §24.3 quarantine,
  §24.5 shadow build verification.
- Determinism: bit-equal across replicas regardless of tier
  (verified by §24.1 harness).

**Unit tests**:
- `TestSMTier1_DSLCodegen_StoredProc` — Stored procedure DSL
  compiles to native Go; round-trip produces equivalent state.
- `TestSMTier1_DSLCodegen_Projection` — Projection DSL → diff-flow
  ops.
- `TestSMTier1_DSLCodegen_AuthorityPolicy` — Policy DSL → decision
  tree.
- `TestSMTier1_DSLCodegen_HotPathOverhead` — ≤ 1ms over native.
- `TestSMTier2_NativeReproducibleBuild` — Hash pin enforced.
- `TestSMTier2_RejectsUnapprovedHash` — Unapproved binary refused.
- `TestSMTier3_WASMFeatureFlag_OffByDefault` — Default config
  rejects WASM modules.
- `TestSMTier3_WASMFeatureFlag_OnAcceptsModule` — Enabled config
  accepts.
- `TestSMTier3_WASMSandboxed` — Forbidden ops rejected.
- `TestSMTier3_WASMDeterministic` — Bit-equal across replicas.
- `TestSMTier3_WASMPivotAtHLC` — Pivot applies correctly.
- `TestSMTier3_WASMHostFunctionWhitelist` — Only whitelisted
  callable.

**Integration tests**:
- `TestSMTier1_LiveDSLDeploy` — DSL deployment without binary
  upgrade; version pinned.
- `TestSMTier2_OperatorSignedRollout` — Tier 2 deploy via §25.7
  upgrade orchestration.
- `TestSMTier3_LiveSwap` — WASM live swap when feature flag on.
- `TestSMAllTiers_DeterminismHarness` — All three tiers pass
  §24.1 harness.

**End-to-end tests**:
- `TestSMTiersE2E_DSLProcedureRealCluster` — Real cluster deploys
  PL/pgSQL stored proc; behavior correct.
- `TestSMTiersE2E_NativeExtensionRealCluster` — Tier 2 native SM
  on real cluster.
- `TestSMTiersE2E_WASMOptionalEscapeHatch` — Tier 3 WASM behind
  flag; verifies isolation from default path.

**Race condition tests**:
- `TestSMTier1_ConcurrentDSLCompilations` — `go test -race`
  multiple DSL registrations; cache consistent.
- `TestSMTier2_HotReloadRace` — Tier 2 hot-reload race with active
  apply; clean handoff.
- `TestSMTier3_WASMConcurrentApply` — `go test -race` parallel
  WASM applies; deterministic.
- `TestSMTier3_WASMPivotDuringActiveApply` — Pivot mid-apply;
  clean handoff.

**Negative / non-happy path tests**:
- `TestSMTier1_DSLSyntaxError_RejectedAtRegistration` — Bad DSL
  refused at registration with line-pointing error.
- `TestSMTier1_DSLForbiddenConstructUsed_Rejected` — DSL using
  forbidden non-deterministic op rejected.
- `TestSMTier1_CodegenOutputFailsHarness_DeploymentBlocked` —
  Generated code that fails §24.1 harness blocked.
- `TestSMTier2_HashCollisionAttempt_Detected` — Forced hash
  collision detected via signed-set check.
- `TestSMTier2_BinaryArchMismatch_Rejected` — Wrong-arch binary
  rejected.
- `TestSMTier3_WASMModuleSyscallAttempt_Killed` — Module attempts
  syscall; killed; alert.
- `TestSMTier3_WASMInfiniteLoop_TimedOutAndQuarantined` — Module
  loops forever; timeout; quarantined per §24.3.
- `TestSMTier3_WASMOOMInModule_RecoveredAndQuarantined` — Module
  OOM; quarantined; substrate continues.
- `TestSMTier3_WASMOversizedModule_RejectedAtDeploy` — > size
  limit rejected.
- `TestSMTier3_WASMNonDeterministicViolation_Detected` — Module
  exhibits non-determinism; harness catches; rejected.
- `TestSMTier3_WASMBadHostFunctionCall_GracefulError` — Bad
  arguments to host function; specific error returned to module.
- `TestSMTier3_WASMFeatureFlagOff_DeploymentRejected` — WASM
  module deployment refused with `ErrWASMDisabled`.

---

#### 23.11 — Verifiable computation (zk-proof subjects)

**Description**: Per §31.2.

**Implementation approach**:
- Library: `gnark` (Go-native zk-SNARK; BN254 / BLS12-381).
- Per-SM circuits authored in gnark's frontend DSL.
- Prover runs alongside SM; produces proof per entry.
- Verifier embedded in audit tooling; O(1) verification.
- Selective application: per-subject opt-in.
- Code path: `core/substrate/zk/` (~2500 LOC infra) + per-SM
  circuit (1000-5000 LOC each in gnark DSL).

**Acceptance criteria**:
- Per-subject opt-in zk-proof generation.
- Audit verification independent of state size.
- Proof size bounded (≤ 200 bytes typical for Groth16).
- Proof generation time bounded.
- Verification cost bounded (~10ms).
- Tampered state fails verification.

**Unit tests**:
- `TestZK_ProofVerifies` — Generated proof verifies.
- `TestZK_TamperingRejected` — Tampered state fails verification.
- `TestZK_ProofSizeBounded` — ≤ 200 bytes.
- `TestZK_VerificationCost` — ≤ 10ms.

**Integration tests**:
- `TestZK_RealStateMachine` — Realistic SM (claims-board accept);
  proofs generated and verified.
- `TestZK_BatchVerification` — Multiple proofs verified in batch.

**End-to-end tests**:
- `TestZKE2E_TenantFacingAudit` — Tenant audits without trusting
  operator.

**Race condition tests**:
- `TestZK_ConcurrentProofGen` — `go test -race` parallel proof
  generation; correct outputs.
- `TestZK_ProofGenerationDuringStateUpdate` — Proof gen race
  with state update; consistent.

**Negative / non-happy path tests**:
- `TestZK_ForgedProof_Rejected` — Forged proof rejected.
- `TestZK_CircuitMismatch_Rejected` — Proof from different
  circuit version rejected.
- `TestZK_TrustedSetupRequired_FailsWithoutSetup` — Without
  trusted setup parameters, prover fails; specific error.
- `TestZK_LongRunningGeneration_TimeoutDetected` — Proof gen
  takes too long; alert.
- `TestZK_StateInconsistentWithProof_Detected` — State machine
  state diverges from proof claim; detected at audit.

---

#### 23.12 — Causal isolation levels

**Description**: Per §31.3.

**Implementation approach**:
- Per-subject schema declaration: `isolation: strict-serializable
  | linearizable | monotonic-read | causal | eventual-merge |
  read-your-writes`.
- Read API parameter selects level.
- Linearizable: existing read-index path.
- Causal: HLC-frontier read.
- Monotonic-read: per-cursor floor; read never goes backward.
- Read-your-writes: publisher's HLCs added to cursor.
- Eventual-merge: CRDT subjects, no wait.
- Code path: `core/substrate/delivery/cil.go` (~1200 LOC).

**Acceptance criteria**:
- All listed isolation levels supported.
- Per-subject declaration + per-read override.
- Substrate enforces.
- Each level has measurable latency profile.
- No silent downgrade (declared linearizable always linearizable).

**Unit tests**:
- `TestCIL_Linearizable` — Linearizability.
- `TestCIL_Causal` — Causal only; concurrent writes can be
  observed in different orders.
- `TestCIL_MonotonicRead` — Per-consumer monotonic.
- `TestCIL_ReadYourWrites` — Publisher sees own writes.
- `TestCIL_EventualMergeForCRDT` — No wait for CRDT subject.

**Integration tests**:
- `TestCIL_CrossDCActiveActive` — Active-active for causal
  subjects.
- `TestCIL_PerSubjectLevel` — Different subjects with different
  levels coexist.

**End-to-end tests**:
- `TestCILE2E_GlobalDeployment` — Global deployment; correctness.

**Race condition tests**:
- `TestCIL_ConcurrentReadsDifferentLevels` — `go test -race`
  same subject, different levels; each correct.
- `TestCIL_HLCFrontierAdvanceDuringRead` — Frontier advances
  during causal read; consistent snapshot.

**Negative / non-happy path tests**:
- `TestCIL_LinearizableWaitTimeout_ReturnsTimeout` — Linearizable
  read times out; specific error.
- `TestCIL_MonotonicReadBackwardAttempt_DetectedAndCorrected` —
  Apparent backward read; corrected.
- `TestCIL_DowngradeAttempt_Refused` — Subject declared
  linearizable; read with weaker level refused (no silent
  downgrade).
- `TestCIL_UnknownLevel_Rejected` — Unknown isolation level
  rejected at registration.

---

#### 23.13 — Differential dataflow projections

**Description**: Per §31.4.

**Implementation approach**:
- Port `frankmcsherry/differential-dataflow` (Rust) or build
  minimal Go version.
- Operators: map, filter, join, reduce, iterate.
- Subjects → dataflow inputs; deltas propagate downstream.
- Computation graph declared via subject schema or projection DSL.
- Code path: `core/substrate/projection/dataflow/` (~6000 LOC for
  minimal differential runtime + ~1500 LOC per integration).

**Acceptance criteria**:
- Projection deltas propagate correctly.
- Cross-subject joins.
- Incremental view maintenance: O(input delta), not O(state).
- Memory bounded per projection.
- Restart resumes from last consistent frontier.

**Unit tests**:
- `TestDataflow_Delta` — Delta propagates.
- `TestDataflow_Join` — Cross-subject join.
- `TestDataflow_Incremental` — Update is O(input delta).
- `TestDataflow_Iterate` — Recursive operator works.
- `TestDataflow_Restart` — Resume from last frontier.

**Integration tests**:
- `TestDataflow_ComplexQuery` — Realistic complex query.
- `TestDataflow_MemoryBounded` — Memory bounded per projection.

**End-to-end tests**:
- `TestDataflowE2E_AnalyticsDashboard` — Dashboard with complex
  query; delta-driven updates.

**Race condition tests**:
- `TestDataflow_ConcurrentDeltas` — `go test -race` concurrent
  delta application; consistent state.
- `TestDataflow_OperatorRescheduling` — Dataflow operator
  rescheduled mid-update; consistent.

**Negative / non-happy path tests**:
- `TestDataflow_OperatorFails_RecoveredViaReplay` — Operator
  crashes; recovered via input replay.
- `TestDataflow_OutOfOrderDeltas_Reordered` — Out-of-order delta
  arrival; reordered by HLC.
- `TestDataflow_CycleDetected_Refused` — Dataflow with cycle
  refused at registration.
- `TestDataflow_MemoryGrowthBounded` — Pathological input;
  memory bounded; sheds via aging.

---

#### 23.14 — Decentralized identity (DIDs)

**Description**: Per §31.5.

**Implementation approach**:
- Library: `hyperledger/aries-framework-go` for DID resolution.
- Resolvers: `did:web` (HTTPS+well-known), `did:key` (in-band
  pubkey), `did:spiffe` (SPIFFE-aware).
- SVID extension carries DID alongside SPIFFE URI.
- Federation control plane (§20.1) uses DIDs for cross-org trust.
- Code path: `core/substrate/identity/did/` (~1700 LOC).

**Acceptance criteria**:
- W3C DID document format supported.
- `did:web:`, `did:key:`, `did:spiffe:` resolvers.
- Cross-org trust without shared CA.
- DID resolution cached with bounded staleness.
- Identity proofs verifiable across federations.

**Unit tests**:
- `TestDID_DocumentParse` — DID document parses.
- `TestDID_Resolution_Web` — `did:web` resolves.
- `TestDID_Resolution_Key` — `did:key` resolves.
- `TestDID_Resolution_SPIFFE` — `did:spiffe` resolves.
- `TestDID_CacheBounded` — Resolution cache bounded.

**Integration tests**:
- `TestDID_FederationTrust` — Cross-org federation via DIDs.
- `TestDID_RotationFlow` — DID document rotation propagates.

**End-to-end tests**:
- `TestDIDE2E_AgentCollaboration` — Agents in different orgs
  collaborate via DIDs.

**Race condition tests**:
- `TestDID_ConcurrentResolution` — `go test -race` parallel
  resolutions; cache consistent.

**Negative / non-happy path tests**:
- `TestDID_MalformedDID_Rejected` — Malformed DID rejected.
- `TestDID_ResolverUnreachable_FallsBackToCache` — Resolver
  network down; cached doc used; bounded staleness.
- `TestDID_RevokedDID_Rejected` — Revoked DID rejected.
- `TestDID_HijackedDIDWebDomain_DetectedViaSig` — Hijacked
  domain; old key fails sig check.

---

#### 23.15 — Substrate-as-database

**Description**: Per §31.6.

**Implementation approach**:
- Parser: `auxten/postgresql-parser` or `pg_query_go`.
- Planner maps SQL to projection plan over §23.13 differential
  dataflow.
- `AS OF HLC '...'` clause maps to time-travel.
- Reasonable subset of PostgreSQL supported (SELECT, JOIN, GROUP
  BY, aggregations); not full PostgreSQL.
- Code path: `core/substrate/sql/` (~6000 LOC).

**Acceptance criteria**:
- SQL-shape query planner over subjects.
- Incremental maintenance via §23.13.
- `AS OF HLC` time-travel clause.
- SELECT, JOIN, GROUP BY, aggregations supported.
- Linearizability per-subject; cross-subject reads at HLC frontier.
- Query latency for typical analytical workload bounded.

**Unit tests**:
- `TestSQL_QueryPlan_Generated` — Plan generated.
- `TestSQL_AsOf_HLC` — Time-travel clause works.
- `TestSQL_Joins` — Cross-subject join.
- `TestSQL_Aggregations` — GROUP BY + aggregates.
- `TestSQL_IncrementalView` — Maintenance via dataflow.

**Integration tests**:
- `TestSQL_CrossSubjectJoin` — Multi-subject join.
- `TestSQL_DashboardQuery` — Realistic dashboard query.

**End-to-end tests**:
- `TestSQLE2E_AnalyticsWorkload` — Realistic OLAP workload on
  substrate.

**Race condition tests**:
- `TestSQL_ConcurrentQueries` — `go test -race` parallel queries;
  consistent.
- `TestSQL_QueryDuringSchemaChange` — Schema change race; query
  either uses old or new schema, no torn.

**Negative / non-happy path tests**:
- `TestSQL_MalformedQuery_Rejected` — Parse error returns
  specific error.
- `TestSQL_UnsupportedFeature_RejectedWithMessage` — Use of
  unsupported SQL feature; specific error.
- `TestSQL_AsOfBeforeRetention_Refused` — Time-travel before
  retention; refused.
- `TestSQL_QueryTimeout` — Bounded execution time; timeout
  returns partial or error.
- `TestSQL_LargeResultSet_Streamed` — Result set large; streamed,
  not buffered.

---

#### 23.16 — Post-quantum cryptography

**Description**: Per §31.13.

**Implementation approach**:
- Library: `cloudflare/circl` (Dilithium, Kyber); reference impl
  for SPHINCS+.
- Hybrid signatures: Ed25519 + Dilithium dual-sign during
  transition.
- QUIC handshake hybrid: X25519 + Kyber via TLS 1.3 KEM extension
  (post-quantum draft); requires `quic-go` extension.
- Code path: `core/substrate/identity/pqc/` (~1700 LOC).

**Acceptance criteria**:
- Dilithium signatures alongside Ed25519 (dual-sign during
  transition).
- Kyber KEM for QUIC handshake (hybrid).
- SPHINCS+ for high-assurance (slow but minimal assumptions).
- Algorithm choice per-cluster policy.
- Migration path: dual-sign window; old keys destroyed after
  cluster fully migrated.

**Unit tests**:
- `TestPQC_DilithiumKAT` — Known-answer-test against NIST vectors.
- `TestPQC_KyberKAT` — KAT.
- `TestPQC_SPHINCSKAT` — KAT.
- `TestPQC_DualSign` — Dual-sign verifies under both algorithms.
- `TestPQC_KeySize` — Within expected bounds.

**Integration tests**:
- `TestPQC_QUICHybrid` — Hybrid QUIC handshake works.
- `TestPQC_SVIDWithDilithium` — SVID carries Dilithium pubkey.

**End-to-end tests**:
- `TestPQCE2E_ClusterMigration` — Cluster migrates from classical
  to PQC without downtime.

**Race condition tests**:
- `TestPQC_ConcurrentDualSign` — `go test -race` concurrent
  dual-signing.

**Negative / non-happy path tests**:
- `TestPQC_OnlyClassicalSig_RejectedAfterMigration` — After full
  migration, classical-only sig rejected.
- `TestPQC_DilithiumSigForged_Detected` — Forged Dilithium
  detected.
- `TestPQC_HybridHandshakeFailoverToClassical_Configurable` —
  Operator policy selects whether to fallback.
- `TestPQC_LargeKeysOversizeFrames_Handled` — Larger PQC keys
  fit in frame format extensions; not breaking wire compat.

---

#### 23.17 — Operationally-verifiable formal methods

**Description**: Per §31.14.

**Implementation approach**:
- TLA+ specs in `tlaplus`; export safety properties to Go-readable
  form.
- TLC for offline model checking.
- Runtime sampler validates predicates against committed entries
  on configurable percentage.
- Property violation halts affected component.
- Code path: `core/substrate/verify/` (~2200 LOC) + TLA+ specs.

**Acceptance criteria**:
- TLA+ specs compile to runtime invariants.
- Continuous in-prod sampling check.
- Violations halt affected group.
- Sampling overhead bounded (≤ 1%).
- Predicate library extensible.

**Unit tests**:
- `TestTLA_InvariantCompile` — Invariant compiles to checker.
- `TestTLA_ViolationHalts` — Violation halts affected component.
- `TestTLA_SamplingOverhead` — ≤ 1%.

**Integration tests**:
- `TestTLA_LiveSampling` — Production-style sampling without
  overhead.

**End-to-end tests**:
- `TestTLAE2E_InjectedViolation` — Inject violation; detected;
  halted.

**Race condition tests**:
- `TestTLA_ConcurrentSampling` — `go test -race` parallel
  predicate checks.

**Negative / non-happy path tests**:
- `TestTLA_PredicateBugCausesFalsePositive_OperatorOverride` —
  Bug in predicate; operator can override halt.
- `TestTLA_MalformedSpec_RejectedAtCompile` — Invalid spec
  rejected.
- `TestTLA_ViolationDuringHighLoad_HaltStillEffective` — Halt
  works under load.

---

#### 23.18 — Cross-substrate interop adapters

**Description**: Per §31.15.

**Implementation approach**:
- NATS: parse NATS protocol via `nats.go`'s wire helpers; map
  subjects to substrate; translate ack semantics.
- Kafka: implement Kafka wire protocol (or use `franz-go`'s
  framing); map topics to substrate.
- Postgres logical-replication: `jackc/pglogrepl` to consume
  replication stream into substrate.
- Code path: `core/substrate/interop/{nats,kafka,postgres}/`
  (~3500 LOC per adapter).

**Acceptance criteria**:
- NATS protocol front-end.
- Kafka protocol front-end.
- Postgres logical-replication front-end.
- Each uses substrate semantics under the hood (cursors, dedupe,
  HLC).
- Drop-in: existing NATS / Kafka clients run unchanged.
- Latency overhead from translation bounded (≤ 1ms).

**Unit tests**:
- `TestNATS_FrontEnd` — NATS clients work.
- `TestKafka_FrontEnd` — Kafka clients work.
- `TestPostgres_LogicalRep` — Postgres replicates to substrate.
- `TestNATS_AckSemantics` — Ack maps to substrate ack.
- `TestKafka_OffsetSemantics` — Offset maps to HLC frontier.

**Integration tests**:
- `TestInterop_DropInReplacement` — Existing NATS / Kafka apps
  run unchanged.
- `TestInterop_HighThroughput` — Throughput with adapter overhead
  meets target.

**End-to-end tests**:
- `TestInteropE2E_Migration` — Live system migrated from NATS to
  substrate without app changes.

**Race condition tests**:
- `TestInterop_ConcurrentClients` — `go test -race` many
  concurrent adapter clients.

**Negative / non-happy path tests**:
- `TestNATS_UnsupportedFeature_RejectedWithMessage` — NATS
  feature not supported; specific error.
- `TestKafka_ProtocolVersionMismatch_Negotiated` — Kafka client
  with newer / older protocol; negotiated version.
- `TestPostgres_LogicalRepDecodingFailure_PausedNotLost` —
  Decoding failure pauses stream; does not silently drop.
- `TestInterop_ClientCrash_NoSubstrateLeakage` — Client crash
  cleaned up; no orphan substrate state.

---

#### 23.19 — Public blockchain anchoring

**Description**: Per §31.16.

**Implementation approach**:
- Library: `go-ethereum` for Ethereum, `btcsuite/btcd` for Bitcoin.
- Daily transaction submitting Merkle root (configurable cadence).
- Verification: any party fetches block, extracts root, compares.
- Cost bounded (one tx per cadence period).
- Code path: `core/substrate/anchoring/` (~700 LOC).

**Acceptance criteria**:
- Periodic Merkle root anchoring (default daily).
- Configurable target chain.
- Cost bounded.
- Public verifiable.
- Audit immune to operator collusion.

**Unit tests**:
- `TestAnchor_RootSubmit` — Root submitted.
- `TestAnchor_Verify` — Public verification.
- `TestAnchor_CostBounded` — One tx per cadence.

**Integration tests**:
- `TestAnchor_RealChain` — Real test chain (Bitcoin testnet,
  Ethereum Sepolia).
- `TestAnchor_Mempool` — Tx propagates through mempool.

**End-to-end tests**:
- `TestAnchorE2E_AuditScenario` — Audit verified using public
  chain.

**Race condition tests**:
- `TestAnchor_ConcurrentSubmit_Idempotent` — Concurrent
  submission attempts; idempotent.

**Negative / non-happy path tests**:
- `TestAnchor_ChainUnreachable_QueueRetry` — Chain RPC down;
  queued; retried.
- `TestAnchor_TxStuckInMempool_BumpedFee` — Stuck tx; fee bumped
  + replaced.
- `TestAnchor_InvalidRootSubmitted_OperatorAlert` — Submission
  with corrupt root; alerts.
- `TestAnchor_ChainReorgInvalidatesAnchor_Resubmits` — Chain
  reorganization; anchor invalidated; resubmitted.

---

#### 23.20 — Built-in wire transform registry

**Description**: Per §31.17. Native-Go transform registry — operators
*select and configure*, not author. WASM-authored transforms are an
optional Tier 3 escape hatch (§23.10), not the default.

**Implementation approach**:
- Registry of vetted, native-Go transform implementations:
  - **Compression**: zstd (with §31.13 schema-trained dicts), lz4,
    snappy.
  - **Encryption**: AES-GCM-256, ChaCha20-Poly1305 with envelope
    (§21.2).
  - **Redaction**: field-level (declared in schema), TTL-driven
    (auto-erase per §31.23).
  - **Schema migration**: v1→v2 via declarative mapping rules,
    codegen'd to native Go at registration.
  - **Sanitization**: PII masking, value-pinning, format
    normalization for cross-tenant subjects.
- Registry entry: `(name, version, params_schema, native_apply_fn,
  reverse_fn?)`. Versioned per §24.2; verified by §24.1 harness.
- Pipelines are sequences of registry entries with parameters,
  declared in subject schema.
- Pipeline itself published to a substrate subject (audit trail
  of which transform was applied to which entry).
- Code path: `core/substrate/wire_transform/registry/` (~1500 LOC
  for builtin registry + ~600 LOC for pipeline machinery).

**Acceptance criteria**:
- Registry holds all listed builtin transforms.
- Each transform native-Go, zero runtime VM overhead.
- Schema declares pipeline; substrate validates entries before
  registration.
- Deterministic (same input + same params → same output) across
  replicas.
- Reversible where applicable (schema migrations, encryption).
- Auditable: pipeline applications logged to audit subject.
- New registry entries added via §24.6 reproducible-build process.
- User-authored transforms outside registry require Tier 3 WASM
  (§23.10), feature-flagged off by default.

**Unit tests**:
- `TestTransform_RegistryListsAllBuiltins` — All listed transforms
  present and callable.
- `TestTransform_DeterministicOutput` — Same input + params →
  same output across runs / platforms.
- `TestTransform_ReversibleWhereApplicable` — Encryption /
  schema-migration reversible round-trip.
- `TestTransform_PipelineComposition` — Multiple transforms in
  pipeline compose correctly.
- `TestTransform_PipelineAudited` — Pipeline application logged.
- `TestTransform_VersionedPerSM` — §24.2 version pinning works.
- `TestTransform_HarnessValidates` — §24.1 harness validates
  registry entries.

**Integration tests**:
- `TestTransform_PIIRedaction` — PII redacted past TTL.
- `TestTransform_CompressionPolicy` — Compression applied
  end-to-end with schema-trained dicts.
- `TestTransform_EncryptionEnvelopeE2E` — AES-GCM with KEK rotation.
- `TestTransform_ParamsValidation` — Bad params rejected at config.

**End-to-end tests**:
- `TestTransformE2E_SchemaMigration` — v1 → v2 schema migration via
  declarative transform on real cluster.
- `TestTransformE2E_PipelineCompose` — `[zstd_dict, aes_gcm,
  redact_pii]` pipeline applied; verifiable end-to-end.

**Race condition tests**:
- `TestTransform_ConcurrentApply` — `go test -race` parallel
  transform invocations; consistent state.
- `TestTransform_RegistryUpdateRace` — Registry entry update
  concurrent with active uses; deterministic version pinning.
- `TestTransform_PipelineConfigChangeRace` — Config change race;
  in-flight either old or new; no torn state.

**Negative / non-happy path tests**:
- `TestTransform_NonDeterministicAttempt_Rejected` — Transform
  using non-deterministic op rejected.
- `TestTransform_InfiniteLoop_TimedOut` — Transform loops; timeout.
- `TestTransform_OversizedOutput_BoundedOrRejected` — Output
  larger than configured limit; rejected.
- `TestTransform_VersionMismatch_RefusesApply` — Different
  transform versions; explicit migration required.

---

#### 23.21 — Substrate-managed agent runtime

**Description**: Per §31.18.

**Implementation approach**:
- Agent definitions stored as substrate objects (native Go binary +
  manifest + capability bindings + reproducible-build hash per
  §24.6). No WASM runtime; agents run as native goroutines.
- Lifecycle SM (`sylk://global/agents/v1`): spawn, schedule, retire.
- Capability scoping via SVID + macaroon caveats (§22.5); enforced
  at publish time by authority predicates.
- Restart = HLC-frontier replay (existing pull-first cursor).
- Code path: `core/substrate/agents/` (~2200 LOC).

**Acceptance criteria**:
- Agent definitions in substrate object store.
- Lifecycle managed by operator group.
- Agent restart via HLC-frontier replay.
- Capability scoping enforced (cannot publish to unauthorized
  subjects).
- Bounded agent count per node.

**Unit tests**:
- `TestAgentRuntime_LifecycleOps` — Spawn / schedule / retire.
- `TestAgentRuntime_Replay` — Restart from frontier.
- `TestAgentRuntime_CapabilityScoping` — Unauthorized publish
  rejected.
- `TestAgentRuntime_BoundedCount` — Per-node cap.

**Integration tests**:
- `TestAgentRuntime_FullLifecycle` — End-to-end lifecycle.
- `TestAgentRuntime_RestartIdempotent` — Restart produces same
  effect as continuation.

**End-to-end tests**:
- `TestAgentRuntimeE2E_FullSession` — Full session under managed
  runtime.

**Race condition tests**:
- `TestAgentRuntime_ConcurrentSpawnRetire` — `go test -race`
  spawn + retire concurrent; deterministic.
- `TestAgentRuntime_RestartDuringActiveOp` — Restart during
  in-flight op; clean handoff.

**Negative / non-happy path tests**:
- `TestAgentRuntime_AgentCrash_LoggedAndRespawned` — Crash;
  logged; respawned per policy.
- `TestAgentRuntime_AgentExceedsResourceQuota_Killed` — Over
  quota; killed.
- `TestAgentRuntime_RevokedCapability_AgentSuspended` — Cap
  revoked mid-session; agent suspended.
- `TestAgentRuntime_NodeDown_AgentMigratesToOtherNode` — Node
  failure; agent migrated.
- `TestAgentRuntime_BinaryHashMismatch_Refused` — Deployed binary
  doesn't match manifest hash; refused.

---

#### 23.22 — Geo-fenced CRDTs

**Description**: Per §31.19.

**Implementation approach**:
- Schema declares per-field residency: `(field, allowed_regions)`.
- Replication filter at federation gateway: drop fields not
  allowed in destination region (replace with hash).
- Merge respects fences: out-of-region replicas hold tombstones /
  hashes.
- Code path: `core/substrate/crdt/geofence.go` (~1100 LOC).

**Acceptance criteria**:
- Per-field residency in CRDT schemas.
- Cross-region merges respect residency.
- Tombstones / hashes outside region.
- GDPR-compliant: PII never replicated outside allowed region.
- Field-level enforcement (some fields global, some local).

**Unit tests**:
- `TestGeoCRDT_FieldResidency` — Field stays in region.
- `TestGeoCRDT_MergeCorrect` — Merges respect fences.
- `TestGeoCRDT_GlobalFieldsReplicate` — Non-fenced fields
  replicate.
- `TestGeoCRDT_HashedReplacement` — Out-of-region replicas hold
  hashes.

**Integration tests**:
- `TestGeoCRDT_GDPR` — GDPR scenario; data residency preserved.
- `TestGeoCRDT_FederationFilter` — Federation gateway filters.

**End-to-end tests**:
- `TestGeoCRDTE2E_GlobalCollab` — Global collab; regulated data
  in-region.

**Race condition tests**:
- `TestGeoCRDT_ConcurrentMergesDifferentRegions` — `go test -race`
  parallel merges; correct fences.

**Negative / non-happy path tests**:
- `TestGeoCRDT_AttemptCrossRegionFieldRead_Rejected` — Reading
  fenced field from non-allowed region rejected.
- `TestGeoCRDT_PolicyChangeEvictsExistingData` — Residency policy
  changed; data evacuated from non-allowed regions.
- `TestGeoCRDT_HashCollisionInReplacement_BoundedImpact` —
  Hash collision in replacement field; doesn't cascade.
- `TestGeoCRDT_ReplicaInUnauthorizedRegion_RefusesAttach` — Try
  to attach replica in disallowed region; refused.

---

#### 23.23 — Self-replicating cluster

**Description**: Per §31.20.

**Implementation approach**:
- Cloud SDKs: AWS EC2 (`aws-sdk-go-v2`), GCP Compute, Azure SDK.
- Provisioning: substrate proposes; operator approves via signed
  authority; reconciler invokes cloud API.
- Cloud-init with SPIRE bootstrap; VM boots, attests, joins as
  learner; promotes to voter when caught up.
- Symmetric for decommission.
- Code path: `cmd/sylkd-operator/scaling/` (~1700 LOC + per-cloud
  adapter).

**Acceptance criteria**:
- Cluster spec includes capacity targets.
- Substrate proposes scaling actions.
- Operator approval required for execution.
- Closed-loop fleet management.
- Provisioning + attest + join automated.
- Symmetric decommission flow.

**Unit tests**:
- `TestSelfReplicate_Proposal` — Proposal generated.
- `TestSelfReplicate_RequiresApproval` — Refuses without signed
  approval.
- `TestSelfReplicate_DecommissionFlow` — Decommission proposal +
  execution.
- `TestSelfReplicate_LearnerPromotion` — Joins as learner;
  promotes after sync.

**Integration tests**:
- `TestSelfReplicate_Provisioning` — Real cloud provisioning +
  attest + join.
- `TestSelfReplicate_AcrossClouds` — Multi-cloud fleet.

**End-to-end tests**:
- `TestSelfReplicateE2E_ClosedLoop` — Sustained load; cluster
  scales up; sustained underutilization; cluster scales down.

**Race condition tests**:
- `TestSelfReplicate_ConcurrentProposalsCoalesced` — Multiple
  proposals coalesced.

**Negative / non-happy path tests**:
- `TestSelfReplicate_CloudAPIDown_QueuedRetried` — Cloud API
  down; queued + retried.
- `TestSelfReplicate_CloudInitFailureBlocksJoin` — Cloud-init
  fails; node doesn't join; alerted.
- `TestSelfReplicate_AttestationFailure_NodeDestroyed` — Failed
  attestation; node destroyed before joining.
- `TestSelfReplicate_OperatorRejectionPath` — Operator rejects
  proposal; substrate respects.
- `TestSelfReplicate_FleetSpecChangedMidProvision_UsesLatest` —
  Spec changed mid-provision; reconciler uses latest.

---

#### 23.24 — Simulation harness

**Description**: Per §31.12.

**Implementation approach**:
- Hardest item; deterministic simulation of all layers.
- Replace `time.Now`, `runtime.NumGoroutine`, channel scheduling,
  network with simulated equivalents.
- For Go: `runtime.GOMAXPROCS(1)` + cooperative yield discipline
  is workable approximation; full goroutine determinism not
  feasible.
- Inspired by FoundationDB Flow library (C++).
- Reproducible by seed; partial-order schedule exhaustion via
  configurable explorer.
- Code path: `testutil/sim/` (~6000 LOC).

**Acceptance criteria**:
- All layers run in deterministic single-process simulation.
- Network / disk / clock / CPU faults injected as configured.
- Reproducible by seed: same seed → same final state.
- Partial-order schedule exhaustion for safety properties.
- Simulation time vs wall time decoupled.

**Unit tests**:
- `TestSim_DeterministicReplay` — Same seed → same result.
- `TestSim_FaultInjection` — Faults injected as configured.
- `TestSim_TimeDecoupling` — Sim time advances independent of
  wall.
- `TestSim_PartialOrderSchedule` — Partial-order exhaustion finds
  edge cases.

**Integration tests**:
- `TestSim_FullStack` — Full substrate stack runs in simulation.
- `TestSim_NetworkFaultPropagation` — Network faults propagate.
- `TestSim_DiskFaultPropagation` — Disk faults propagate.
- `TestSim_ClockSkewPropagation` — Clock faults propagate.

**End-to-end tests**:
- `TestSimE2E_BugReproduction` — Production bug reproduced from
  seed.
- `TestSimE2E_ExhaustiveSafety` — Partial-order exhaustion finds
  invariant violations.

**Race condition tests**:
- `TestSim_DeterminismUnderRace` — `go test -race`; harness
  itself race-free; sim outcome stable.
- `TestSim_GoroutineSchedulingNonDetTolerated` — Best-effort
  determinism; documented limits.

**Negative / non-happy path tests**:
- `TestSim_FlakyTestExposed_DeterministicallyReproduced` — Flaky
  test reproduced via seed.
- `TestSim_NonDeterministicSubstrateCode_FlaggedByDivergence` —
  Substrate code with non-determinism flagged.
- `TestSim_LongSimulation_BoundedMemory` — Multi-day sim; memory
  bounded.
- `TestSim_FaultBeyondConfigured_NoSilentSuccess` — Test asserts
  fault should occur; if not occurring, test fails.
- `TestSim_GoroutineLeak_Detected` — Sim detects goroutine leaks
  in substrate code.

---

### Phase 24 — Research Frontier

Items from §31.21-§31.29 — genuinely novel territory. Each ships
independently behind feature flags; none block prior phases. Each
involves theory work (formal models, machine-checked proofs) alongside
implementation.

**Phase implementation overview**: Phase 24 is the only phase where
implementation is bottlenecked by theory work, not engineering. Each
item involves a formal model (TLA+, Coq, or Lean) plus a runtime
verifier or enforcer. Effort estimates are dominated by proof
construction, not Go code. Common dependencies: Lean 4 toolchain (for
24.1, 24.2, 24.4), TLA+ + TLC (for 24.8, 24.4), `auxten/postgresql-
parser` or custom DSL parser (for 24.9), HotStuff lit (24.6), BGP feed
sources (24.7).

#### 24.1 — Session-typed subjects

**Description**: Per §31.21.

**Implementation approach**:
- Schema language extended with session type DSL (multiparty session
  types, Honda et al.); custom or port of `scribble.org`.
- Compile session type to deterministic state-machine checker at
  registration; per-frame check is constant-time lookup.
- Refinement types via predicate language (subset of CDDL or custom).
- Frame validation: substrate consults session-type-checker; out-of-
  order frame rejected with `ErrSessionTypeViolation`.
- Soundness theorem machine-checked in Lean 4: "no observable
  execution violates declared session type."
- Code path: `core/substrate/types/session/` (~3500 LOC) + theory.

**Acceptance criteria**:
- Schema language extended with session type grammar.
- Substrate type-checks publish/subscribe at frame boundary.
- Type-violating frames rejected with `ErrSessionTypeViolation`.
- Refinement types enforced (non-empty, range, regex).
- Formal soundness theorem proven in Lean 4 (no observable execution
  violates declared session type).
- Per-frame check ≤ 1µs.
- Existing subjects can opt-in without code changes (declare types
  at registration).

**Unit tests**:
- `TestSessionType_GrammarParse` — Grammar parses valid types.
- `TestSessionType_RejectsViolation` — Violating frame rejected.
- `TestSessionType_Refinement` — Refinement types enforced
  (non-empty, range, regex).
- `TestSessionType_Soundness` (property test, model-based) —
  Generated traces respect type.
- `TestSessionType_PerFrameCheckCost` — ≤ 1µs.
- `TestSessionType_RecursiveTypes` — Recursive types validate.
- `TestSessionType_BranchingChoice` — Branching session types work.

**Integration tests**:
- `TestSessionType_RealClaimsBoard` — Claims board session type
  enforced; existing semantics preserved.
- `TestSessionType_OptInWithoutCodeChange` — Existing subject opts
  in; no code change.

**End-to-end tests**:
- `TestSessionTypeE2E_AdversarialPublisher` — Publisher attempting
  out-of-order published rejected at substrate.
- `TestSessionTypeE2E_FormalSoundnessOnRealLogs` — Replay real logs;
  type checker validates; no false negatives.

**Race condition tests**:
- `TestSessionType_ConcurrentPublishesSameSession` — `go test -race`
  concurrent publishes; type state consistent.
- `TestSessionType_TypeRegistrationRace` — Type registration race
  with active subscriptions; consumers re-validate.

**Negative / non-happy path tests**:
- `TestSessionType_OutOfOrderMessage_Rejected` — Out-of-order in
  declared session rejected.
- `TestSessionType_WrongRoleSends_Rejected` — Wrong role for
  message rejected.
- `TestSessionType_TypeMismatchViolation_Rejected` — Refinement
  type violation rejected with specific error.
- `TestSessionType_TypeUpgradeIncompatible_RejectedAtRegister` —
  Type upgrade not backward-compatible; rejected.
- `TestSessionType_LeanTheoremProofDrift_DetectedInCI` — Theorem
  proof drift caught in CI before merge.
- `TestSessionType_DegenerateGrammar_Rejected` — Empty / unbounded-
  recursion grammar rejected.

---

#### 24.2 — Proof-carrying state machines

**Description**: Per §31.22. SM ships with machine-checked invariant
proof.

**Implementation approach**:
- Proof assistant: Lean 4 (most modern; Mathlib ecosystem).
- SM author writes proof of `Apply preserves invariants` in Lean.
- Export proof to substrate-readable format (Lean's `.olean` or
  extracted Coq + Coq Native).
- Substrate ships small kernel proof checker (Lean 4's kernel ~5000
  LOC; bind via cgo).
- Deployment refuses SM without valid proof.
- Code path: `core/substrate/sm/proof_carrying/` (~5000 LOC including
  kernel binding) + per-SM proof (2000-10000 LOC in Lean).

**Acceptance criteria**:
- Proof carrier format defined (Lean / Coq / Dafny export).
- Substrate verifies proof against declared invariants on deployment.
- Invalid proof → SM deployment rejected.
- Audit trail of proof verification.
- Verified kernel size minimal (small TCB).
- Proof checking time bounded.
- Per-SM proof maintainable (ideally proof refactor cheap when SM
  refactors).

**Unit tests**:
- `TestProof_VerifiesValidProof` — Valid proof accepted.
- `TestProof_RejectsInvalid` — Invalid proof rejected.
- `TestProof_InvariantStatement` — Invariants stated correctly.
- `TestProof_KernelDeterministic` — Same proof → same verification
  result.
- `TestProof_VerificationCostBounded` — ≤ configurable budget.

**Integration tests**:
- `TestProof_RealSMDeployment` — Realistic SM (KV) ships with proof
  of LWW invariant; deployment succeeds.
- `TestProof_BuggySMRejected` — Buggy SM rejected at proof check.

**End-to-end tests**:
- `TestProofE2E_BugCaughtAtDeploy` — Buggy SM violating invariant
  rejected at deployment, never reaches production.
- `TestProofE2E_ProofMaintenance` — SM refactor; proof maintained;
  re-deploys.

**Race condition tests**:
- `TestProof_ConcurrentVerification` — `go test -race` parallel
  proof checks; consistent.

**Negative / non-happy path tests**:
- `TestProof_TamperedProof_Rejected` — Tampered proof rejected.
- `TestProof_ProofForDifferentSM_Rejected` — Proof claims to verify
  SM A but submitted with SM B; rejected.
- `TestProof_AxiomBypass_Detected` — Proof relying on suspicious
  axioms detected and rejected.
- `TestProof_NonTerminating_TimeoutRejected` — Pathological proof
  doesn't terminate; timeout; rejected.
- `TestProof_KernelBugDetected_DeploymentBlocked` — Kernel
  vulnerability disclosed; corresponding proofs flagged for re-check.
- `TestProof_StaleProofForUpdatedSM_Rejected` — SM updated; proof
  stale; deployment rejected; new proof required.

---

#### 24.3 — Verifiable history redaction

**Description**: Per §31.23.

**Implementation approach**:
- Per-leaf AEAD with separate keys; keys in cluster escrow keystore.
- Redaction: KMS destroys leaf key; substrate replaces leaf
  plaintext with `Hash(plaintext)` (already known from Merkle leaf
  hash).
- Merkle structure unchanged; outer commitments verify.
- Auditor sees redacted leaves but cannot recover content.
- Field-level redaction within an entry: separate AEAD per declared
  PII field.
- Code path: `core/substrate/redaction/` (~1700 LOC).

**Acceptance criteria**:
- Per-leaf encryption key managed by substrate.
- Redaction destroys leaf key; replaces plaintext with hash.
- Merkle proof structure preserved.
- Pre-redaction commitments still verify.
- Redaction itself logged to audit subject.
- Field-level redaction supported.
- GDPR / CCPA compliant: tenant-driven erasure.
- Forensic: redaction event records when, who authorized, why
  (no PII).

**Unit tests**:
- `TestRedaction_ProofStructurePreserved` — Tree shape unchanged.
- `TestRedaction_PreRedactionVerifies` — Old commitments still
  verify.
- `TestRedaction_PlaintextGone` — Plaintext irrecoverable (key
  destroyed).
- `TestRedaction_AuditLogged` — Redaction event published.
- `TestRedaction_FieldLevel` — Single field redacted; others
  intact.
- `TestRedaction_KeyDestructionInKMS` — KMS confirms key destroyed.

**Integration tests**:
- `TestRedaction_GDPRFlow` — End-to-end GDPR right-to-erasure
  flow.
- `TestRedaction_FieldLevelPII` — PII field redacted; non-PII
  preserved.

**End-to-end tests**:
- `TestRedactionE2E_RegulatoryAudit` — Auditor verifies post-
  redaction cluster integrity.
- `TestRedactionE2E_TenantInitiatedErasure` — Tenant requests
  erasure; substrate executes; auditor confirms compliance.

**Race condition tests**:
- `TestRedaction_RedactionDuringActiveRead` — Redaction concurrent
  with read; reader either gets plaintext or hash, no torn state.
- `TestRedaction_ConcurrentRedactions` — Multiple redactions of
  different leaves; consistent.

**Negative / non-happy path tests**:
- `TestRedaction_KeyNotDestroyedInKMS_RetryUntilConfirmed` — KMS
  fails to destroy; retried; until confirmed gone.
- `TestRedaction_AttemptToRecoverPlaintext_FailsCryptographically` —
  Attempt to read post-redaction plaintext fails.
- `TestRedaction_HashCollisionInLeafReplacement_Negligible` —
  Hash collision probability negligible (BLAKE3).
- `TestRedaction_RedactedFieldStillReferenceableByAudit` — Audit
  references redacted leaf via hash; no resurrection of plaintext.
- `TestRedaction_IncompleteRedactionDetected` — Plaintext copy
  exists in some replica; redaction protocol propagates until all
  replicas confirm.
- `TestRedaction_RestoreFromBackupReintroducesPlaintext_Detected` —
  Backup restore would reintroduce plaintext; redaction-aware
  restore filter.

---

#### 24.4 — Cross-domain causal algebra

**Description**: Per §31.24.

**Implementation approach**:
- Formal model in Lean 4: `(domain_id, hlc, uncertainty)` tuples;
  cross-domain happens-before partial order.
- Implementation: extended HLC carrying domain ID; runtime computes
  upper/lower bounds at federation boundaries.
- Linearizable read: waits combined uncertainty across involved
  domains.
- Soundness theorem proves "no observed ordering violates HB
  across domains."
- Code path: `core/substrate/identity/cross_domain_hlc.go` (~2500
  LOC) + Lean theory.

**Acceptance criteria**:
- Federated HLC tuples `(domain_id, hlc, uncertainty)`.
- Cross-domain happens-before computed soundly.
- Federated linearizable reads wait combined uncertainty.
- Soundness theorem machine-checked (Lean 4).
- Wait time bounded by max uncertainty across domains.
- No domain-master designation; symmetric.

**Unit tests**:
- `TestCrossDomainHLC_PartialOrder` — Cross-domain order is partial.
- `TestCrossDomainHLC_UncertaintyPropagation` — Uncertainty
  propagates correctly.
- `TestCrossDomainHLC_LinearizableWait` — Wait correct.
- `TestCrossDomainHLC_SymmetricNoDomainMaster` — No domain treated
  as master.
- `TestCrossDomainHLC_TheoremProofDrift_CIDetects` — Proof drift
  caught in CI.

**Integration tests**:
- `TestCrossDomainHLC_FederationE2E` — Two-federation read;
  correctness.
- `TestCrossDomainHLC_HighUncertaintyBoundedWait` — High
  uncertainty bounds wait time.

**End-to-end tests**:
- `TestCrossDomainHLCE2E_GlobalLinearizableRead` — Realistic global
  read; meets correctness and latency budgets.

**Race condition tests**:
- `TestCrossDomainHLC_ConcurrentReadsAcrossDomains` — `go test
  -race` cross-domain reads; consistent.
- `TestCrossDomainHLC_DomainBoundaryRace` — Read crossing domain
  boundary; consistent.

**Negative / non-happy path tests**:
- `TestCrossDomainHLC_DomainUnreachable_StalesUntilHeal` — Domain
  unreachable; reads in other domain bounded staleness; no false
  linearizability.
- `TestCrossDomainHLC_AdversarialDomainSkew_Detected` — Adversarial
  skewed domain; cross-domain participation excluded.
- `TestCrossDomainHLC_MalformedDomainID_Rejected` — Malformed ID
  rejected.
- `TestCrossDomainHLC_UncertaintyOverflow_Saturated` — Uncertainty
  overflow saturated, not wraparound.

---

#### 24.5 — Substrate-internal optimization passes

**Description**: Per §31.25.

**Implementation approach**:
- Pass framework: each pass = `(matcher, transformer)` pair.
- Profile collector consumes §28.3 anomaly feed.
- Passes:
  - Hot-view materialization (matcher: high read rate on
    projection).
  - Replication topology rewrite (read-heavy → 1-voter + learners).
  - Sub-subject sharding (matcher: partition_key skew).
  - Cross-subject join precomputation (matcher: query frequency).
- Operator approval gate per transformation; reversible.
- Code path: `core/substrate/optimizer/` (~3500 LOC).

**Acceptance criteria**:
- Each optimization pass declared with predicate + transformation.
- Profile-guided: trigger on observed access patterns.
- Operator-approved: each proposed transformation requires sign-off
  (or auto-approve policy).
- Reversible: every optimization can be rolled back.
- Auditable: each transformation recorded.
- Bounded runtime overhead: ≤ 5%.
- Passes composable; ordering deterministic.

**Unit tests**:
- `TestOptimizer_HotViewMaterialize` — Hot projection materialized.
- `TestOptimizer_ReplicationRewrite` — Read-heavy → 1-voter +
  learners.
- `TestOptimizer_Reversal` — Each optimization reversible.
- `TestOptimizer_PassComposition` — Multiple passes compose.
- `TestOptimizer_OverheadBounded` — ≤ 5% overhead.

**Integration tests**:
- `TestOptimizer_LiveCluster` — Real workload; observed
  improvement.
- `TestOptimizer_OperatorApprovalRequired` — Without approval, no
  application.

**End-to-end tests**:
- `TestOptimizerE2E_LongRunning` — Multi-day run; optimizer
  settles to near-optimal config.

**Race condition tests**:
- `TestOptimizer_ConcurrentProposals` — Multiple passes proposing
  concurrently; serialized application.

**Negative / non-happy path tests**:
- `TestOptimizer_BadPassPredicate_DoesntMatch` — Buggy predicate;
  doesn't match; no spurious optimization.
- `TestOptimizer_TransformationFails_ReverseClean` — Apply fails;
  reverse to prior state cleanly.
- `TestOptimizer_AutoApprovePolicyRevoked_StopsAuto` — Auto-
  approve revoked; stops auto-apply.
- `TestOptimizer_AppliedAndOperatorRejects_Reverted` — Operator
  rejects post-apply; reverted.
- `TestOptimizer_PathologicalProfile_BoundedActivity` —
  Pathological profile (cardinality explosion); pass activity
  bounded.

---

#### 24.6 — Speculative consensus

**Description**: Per §31.26.

**Implementation approach**:
- HotStuff variant: 1-RTT optimistic commit assuming honesty;
  2-RTT verification in parallel.
- Speculative readers explicitly opt in via API.
- Substrate retains audit log of speculatively-committed entries.
- Rollback: emit "rollback" event; consumers reading speculatively
  must replay.
- Code path: `core/substrate/consensus/speculative.go` (~3500 LOC).

**Acceptance criteria**:
- 1-RTT optimistic commit; 2-RTT verification.
- Speculative readers explicitly opt in.
- Rollback invisible to non-speculative readers.
- Latency improvement measurable on read-heavy paths.
- Rollback rate under no-Byzantine reality ~0.
- Speculative state never reaches non-spec readers.

**Unit tests**:
- `TestSpeculative_OptimisticCommit` — 1-RTT path.
- `TestSpeculative_Rollback` — Rollback works.
- `TestSpeculative_NonSpecUnaffected` — Non-spec readers unaffected.
- `TestSpeculative_LatencyBetterThanStrict` — Measurable
  improvement.

**Integration tests**:
- `TestSpeculative_NoByzantineReality` — Under no-Byzantine
  reality, rollback rate ~0.
- `TestSpeculative_RollbackEventEmission` — Rollback event reaches
  speculative consumers.

**End-to-end tests**:
- `TestSpeculativeE2E_LatencyImprovement` — Measured latency
  improvement vs strict path.
- `TestSpeculativeE2E_RollbackUnderByzantine` — Inject Byzantine
  fault; rollback fires; speculative consumers replay.

**Race condition tests**:
- `TestSpeculative_ConcurrentSpecReadsDuringRollback` —
  Speculative reads during rollback; deterministic outcome.
- `TestSpeculative_VerificationCompletesAfterCommit` — Verification
  late but eventual; consistent.

**Negative / non-happy path tests**:
- `TestSpeculative_VerificationDisagrees_RollbackTriggered` —
  Verification disagrees; rollback.
- `TestSpeculative_NonSpecReaderNeverSeesRollback` — Strict reader
  blind to spec rollback.
- `TestSpeculative_RepeatedRollbacks_BackoffToStrict` — Repeated
  rollbacks; system falls back to strict for hot subject.
- `TestSpeculative_OptInOnNonSpecSubject_Rejected` — Opt-in only
  available on subjects with `consensus=speculative` declared.
- `TestSpeculative_AuditTrailComplete` — Audit log of speculative
  + rollback events complete.

---

#### 24.7 — BGP/ASN-aware routing

**Description**: Per §31.27.

**Implementation approach**:
- Ingest BGP feeds: RouteViews via `pmacct` stream or RIPE RIS via
  WebSocket.
- ASN distance via `whois` + ASRank API (CAIDA).
- Routing decisions weighted by ASN distance + peering economics
  (Cloudflare's published transit/peering data).
- Failover routes pre-computed for top-100 ASN failure scenarios.
- Code path: `core/substrate/edge/bgp/` (~1700 LOC).

**Acceptance criteria**:
- Edge tier consumes BGP feeds.
- Routing decisions weighted by ASN distance + peering economics.
- Failover routes pre-computed.
- Bandwidth cost measurably reduced (transit vs peering).
- Resilient to BGP-level outages (Cloudflare 2022, Facebook 2021
  patterns).
- Per-route SLAs configurable.

**Unit tests**:
- `TestBGP_FeedConsumption` — BGP feed consumed.
- `TestBGP_RouteSelection` — Selection respects ASN distance.
- `TestBGP_PeeringEconomicsWeighting` — Cost weighting applied.
- `TestBGP_FailoverPrecomputed` — Top-100 ASN failures
  pre-computed.

**Integration tests**:
- `TestBGP_FailoverPattern` — Common BGP withdrawal patterns
  handled.
- `TestBGP_PeeringPreferred` — Peering preferred over transit
  where possible.

**End-to-end tests**:
- `TestBGPE2E_RealNetwork` — Real cross-cloud deployment; transit
  cost measurably reduced.
- `TestBGPE2E_BGPOutageRecovery` — Simulated BGP outage;
  pre-computed failover route taken.

**Race condition tests**:
- `TestBGP_ConcurrentFeedUpdates` — `go test -race` BGP feed
  updates concurrent with route lookup; consistent.

**Negative / non-happy path tests**:
- `TestBGP_FeedSourceUnreachable_FallsBackToCache` — Feed source
  unreachable; cached topology used; bounded staleness.
- `TestBGP_BogusFeedData_DetectedAndIgnored` — Garbage feed
  rejected (sanity checks).
- `TestBGP_HijackedASN_DetectedViaSig` — BGP hijack detected via
  RPKI ROV (if configured).
- `TestBGP_NoPathToDestination_FailsClearWithError` — No path;
  specific error; doesn't blackhole.
- `TestBGP_RoutingTableExplosion_BoundedMemory` — Massive routing
  table; bounded memory; LRU eviction.

---

#### 24.8 — Multi-level cache coherence

**Description**: Per §31.28.

**Implementation approach**:
- Coherence protocol modeled in TLA+ (akin to MESI but distributed).
- Each cache level subscribes to invalidation events keyed
  `(subject, partition_key, hlc_frontier)`.
- API: `coherent_read(level, max_staleness_ms)` enforces bound.
- Cache invalidation via §11.7 authority broadcast for writes.
- Code path: `core/substrate/cache/coherence/` (~2500 LOC + TLA+
  spec).

**Acceptance criteria**:
- Coherence protocol modeled formally in TLA+.
- Each cache level subscribes to invalidation events.
- `coherent_read(level, max_staleness_ms)` API.
- No stale read past max_staleness.
- Stale → cache fetches latest from upstream.
- Multi-level: edge → DC → replica → consumer.
- TLA+ model checked via TLC.

**Unit tests**:
- `TestCoherence_Invalidation` — Invalidation propagates.
- `TestCoherence_StaleBoundedRead` — Bound respected.
- `TestCoherence_TLCModelCheck` — TLA+ model passes TLC.
- `TestCoherence_LevelSpecificStaleness` — Each level has its own
  staleness bound.
- `TestCoherence_FetchOnStale` — Stale → fetch.

**Integration tests**:
- `TestCoherence_MultiLevelE2E` — Edge → DC → replica → consumer
  coherence preserved.
- `TestCoherence_HighWriteLowRead` — Many writes; cache invalidates
  correctly.

**End-to-end tests**:
- `TestCoherenceE2E_GlobalDeployment` — Global deployment; bounded
  staleness preserved cross-region.

**Race condition tests**:
- `TestCoherence_ConcurrentWritesAndReads` — `go test -race`
  concurrent; consistent.
- `TestCoherence_InvalidationDuringRead` — Invalidation race with
  read; bounded outcome.
- `TestCoherence_LevelTransitionRace` — Read crosses level mid-
  invalidation; consistent.

**Negative / non-happy path tests**:
- `TestCoherence_UpstreamUnreachable_StalesBounded` — Upstream
  down; cache stales bounded; subsequent stale-bounded reads
  return `ErrCacheStale`.
- `TestCoherence_InvalidationLost_DetectedViaHLCAdvance` — Lost
  invalidation event; staleness check detects via HLC frontier
  advancement.
- `TestCoherence_StalenessExceedsLevel_RejectsRead` — Cache stale
  past level's bound; read returns `ErrCacheStaleness`.
- `TestCoherence_TLAModelDrift_CICatches` — TLA+ spec drifts from
  implementation; CI catches.
- `TestCoherence_MaliciousCacheReturnsStale_Detected` —
  Compromised cache returns stale data; detected via HLC frontier
  check at consumer.

---

#### 24.9 — Substrate as compiler IR

**Description**: Per §31.29.

**Implementation approach**:
- Frontend: type-checker + parser for fabric DSL or skill
  definitions (existing fabric structures + extension).
- IR: substrate primitives (subjects, projections, session types,
  durability profiles).
- Optimizer: passes from §24.5 operate on IR.
- Backend: emits subject definitions + projection programs +
  lifecycle bindings.
- Other front-ends (workflow engine, data pipeline) target same
  backend.
- Code path: `core/substrate/compiler/` (~5500 LOC).

**Acceptance criteria**:
- Sylk fabric / agents compile to substrate execution plans.
- Compiler emits subject definitions, projections, session types,
  durability profiles.
- Optimization passes (§24.5) operate on IR.
- Front-end / back-end split allows alternate front-ends.
- Compiler errors actionable.
- Generated plans reproducible (same input → same output).

**Unit tests**:
- `TestCompiler_PlanEmission` — Plans correctly emitted.
- `TestCompiler_OptimizationApplies` — IR-level optimizations
  applied.
- `TestCompiler_ReproduciblePlan` — Same input → same plan.
- `TestCompiler_TypeCheckerErrors` — Errors actionable.
- `TestCompiler_BackendCodeGen` — Backend produces valid substrate
  ops.

**Integration tests**:
- `TestCompiler_SylkFrontEnd` — Sylk front-end compiles to
  substrate successfully.
- `TestCompiler_AlternateFrontEnd` — Workflow-engine front-end
  targets same substrate.
- `TestCompiler_OptimizerOnIR` — Profile-guided optimization on
  compiled IR.

**End-to-end tests**:
- `TestCompilerE2E_AlternateFrontEnd` — Workflow-engine front-end
  targets same substrate; runs end-to-end.
- `TestCompilerE2E_FullSylkCompile` — Full Sylk fabric → substrate;
  parity with hand-written substrate code.

**Race condition tests**:
- `TestCompiler_ConcurrentCompilations` — `go test -race` parallel
  compilations of different inputs; no race.

**Negative / non-happy path tests**:
- `TestCompiler_TypeError_ActionableMessage` — Type error in
  source; specific error pointing to line.
- `TestCompiler_UnknownPrimitive_RejectedAtCompile` — Unknown
  primitive in front-end; rejected.
- `TestCompiler_UnoptimizableInput_GeneratesValidIR` — Input the
  optimizer can't improve; still valid IR.
- `TestCompiler_BadBackendIR_RejectedBeforeDeploy` — Backend
  generates invalid plan; rejected before deploy.
- `TestCompiler_FrontendLanguageVersionMismatch_Rejected` —
  Front-end language version not supported; rejected.
- `TestCompiler_GeneratedSchemaDriftFromSource_DetectedInCI` —
  Generated schema drifts from source; CI catches.

---

### Phase 25 — Adaptive Transport Layer

Multi-stack transport, multipath for Critical, per-class congestion
control, pre-trained Zstd dicts, header fast path, Reed-Solomon FEC,
Bloom interest, coalesced piggyback, Schnorr aggregation, RDMA
intra-DC, network coding. Per §4.5, §4.6.

**Phase implementation overview**: Phase 25 rebuilds Layer 1 with
adaptive routing and bandwidth-aware shaping while preserving wire
format compatibility (§4.1). The substrate selects per-frame transport
from `(class, body_size, dest_topology)`. New transports plug into the
existing `transport.Engine` interface alongside Phase 5 QUIC. Common
dependencies: `quic-go` (with datagram + multipath extensions),
`klauspost/reedsolomon`, `cloudflare/circl` (Schnorr), `klauspost/
compress/zstd`, `bits-and-blooms/bloom`, optional `rdma-core` Go
bindings.

#### 25.1 — Multi-stack transport selection

**Description**: Per §4.5. Substrate selects per-frame from QUIC
streams, QUIC datagrams, raw UDP, raw UDP+FEC, or shared memory based
on `(class, body_size, dest_topology)`.

**Implementation approach**:
- New router `core/substrate/transport/router.go` evaluates selection
  table at publish time; selects path; dispatches via existing
  `transport.Engine` interface.
- QUIC datagrams via `quic-go` RFC 9221 extension; share connection
  context with QUIC streams (one mTLS handshake covers both).
- Raw UDP path uses unencrypted UDP only for Background-class
  intra-cluster traffic; cross-cluster always rides QUIC.
- Shared-memory path reuses §29.4 ring; selected when peer's
  `same_host=true` flag set.
- Selection cached per `(peer, subject_class)` to avoid per-frame
  decision overhead.
- Code path: `core/substrate/transport/router.go` (~1200 LOC) +
  per-path drivers (~400 LOC each).

**Acceptance criteria**:
- Selection happens substrate-side; caller code unchanged.
- Default selection table per §4.5 implemented.
- Per-subject override via schema metadata permitted.
- One mTLS handshake for QUIC streams + datagrams.
- Transport failure of one path falls over to next-priority path
  for that class.
- Selection cost ≤ 100ns hot-path (cached).
- Wire format identical across all paths (§4.1 56-byte header
  + body).

**Unit tests**:
- `TestRouter_SelectsQUICDatagramForCriticalSmall` — Critical < 1KB
  → datagram.
- `TestRouter_SelectsQUICStreamForLargeFrame` — > 1KB → stream.
- `TestRouter_SelectsRawUDPForBackground` — Background class →
  raw UDP.
- `TestRouter_SelectsSHMForSameHost` — Same-host peer → shared
  memory.
- `TestRouter_PerSubjectOverride` — Schema override respected.
- `TestRouter_SelectionCached` — Repeated select is cache hit.
- `TestRouter_SelectionCostBounded` — ≤ 100ns hot path.

**Integration tests**:
- `TestRouter_QUICDatagramOverConnection` — Datagram + stream share
  one mTLS context; both deliver.
- `TestRouter_PathFailover` — Primary path failure → next-priority.
- `TestRouter_AcrossDCDispatch` — Cross-DC frames use QUIC; intra-
  rack uses appropriate path.

**End-to-end tests**:
- `TestRouterE2E_RealNetworkSelection` — Real network; selection
  matches expected per topology.
- `TestRouterE2E_HeterogeneousTraffic` — Mixed class workload;
  each class uses correct path.

**Race condition tests**:
- `TestRouter_ConcurrentSelection` — `go test -race` 1K concurrent
  selections; cache consistent.
- `TestRouter_PathFailoverDuringActiveDispatch` — Failover race
  with in-flight; clean handoff.
- `TestRouter_CacheInvalidationDuringSelect` — Cache invalidation
  race; reader either gets cached or fresh, no torn.

**Negative / non-happy path tests**:
- `TestRouter_UnknownClass_DefaultsToStandard` — Unknown class
  defaults safely.
- `TestRouter_AllPathsDown_Backpressure` — All paths unavailable;
  backpressure to caller, no silent drop.
- `TestRouter_SHMUnavailableSameHost_FallsBackToUnix` — Shared
  memory unavailable; falls back to Unix socket.
- `TestRouter_QUICDatagramTooLarge_StreamUsed` — Frame exceeds
  datagram MTU; stream used instead.
- `TestRouter_PerSubjectOverrideMisconfigured_RejectedAtRegister`
  — Bad override (e.g., shared-memory for cross-DC) rejected.

---

#### 25.2 — Multipath for Critical

**Description**: Per §4.5. Critical frames sent over both QUIC stream
and QUIC datagram simultaneously; receiver accepts first; dedupe drops
the second.

**Implementation approach**:
- Multipath wrapper at publish: dispatch frame on both stream and
  datagram in parallel goroutines.
- Receiver: §6 dedupe (event_id index) drops redundant copies
  cheaply (~10ns LSM lookup).
- Per-frame multipath flag in header `flags` byte (already reserved
  bits in §4.1).
- Bandwidth budget: multipath capped at Critical-class allocation
  (operator-tunable; default 5% of total).
- Code path: `core/substrate/transport/multipath.go` (~600 LOC).

**Acceptance criteria**:
- Critical frames flagged for multipath dispatch.
- Both paths attempted in parallel; first arrival accepted.
- Redundant copy dropped via dedupe; no double-apply.
- Bandwidth overhead bounded by Critical-class allocation.
- Latency improvement measurable under packet loss.
- Operator can disable per-cluster.

**Unit tests**:
- `TestMultipath_DispatchOnBothPaths` — Frame dispatched twice.
- `TestMultipath_DedupeDropsSecond` — Second arrival dropped.
- `TestMultipath_BandwidthCapEnforced` — Budget respected.
- `TestMultipath_FlagOnCritical_OnlyByDefault` — Only Critical
  multipathed by default.

**Integration tests**:
- `TestMultipath_LatencyUnderLoss` — Inject 5% loss; multipath
  median latency improves vs single-path.
- `TestMultipath_NoExtraStateAtReceiver` — Dedupe handles cleanly.

**End-to-end tests**:
- `TestMultipathE2E_CrossDCCritical` — Real cross-DC; Critical
  tail-latency p99 reduced under loss.

**Race condition tests**:
- `TestMultipath_ConcurrentArrivalRace` — `go test -race` both
  paths arrive same nanosecond; dedupe handles atomically.
- `TestMultipath_CancellationRace` — Caller cancels during
  multipath dispatch; both paths cleanly aborted.

**Negative / non-happy path tests**:
- `TestMultipath_BothPathsFail_BackpressureToCaller` — Both fail;
  publish errs; no infinite retry.
- `TestMultipath_DedupeFailureCausesDoubleApply_NotPossible` —
  Even with deliberate dedupe-bypass attempt, double apply
  prevented at SM layer.
- `TestMultipath_BudgetExhausted_DropsToSinglePath` — Budget hit;
  reverts to single-path; no failures.
- `TestMultipath_DisabledByOperator_NoMultipath` — Per-cluster
  disable respected.

---

#### 25.3 — Per-class congestion control

**Description**: Per §4.5. QUIC stream class selects CC algorithm:
BBR for Critical, CUBIC for Bulk, LEDBAT-style for Background.

**Implementation approach**:
- `quic-go`'s pluggable congestion controller interface; register
  per-class CC selector at connection setup.
- BBR: `lucas-clemente/quic-go/internal/congestion` BBR variant or
  `francoispqt/gobbr`.
- CUBIC: `quic-go` default.
- LEDBAT: scavenger-class CC implementation; only fills idle
  bandwidth via `target_delay` parameter.
- Code path: `core/substrate/transport/cc/` (~1500 LOC + per-CC
  algorithm).

**Acceptance criteria**:
- Per-class CC selectable at QUIC stream creation.
- BBR for Critical: low latency under contention.
- CUBIC for Bulk: high throughput.
- LEDBAT for Background: scavenger; backs off under congestion.
- CC parameters tunable per cluster policy.

**Unit tests**:
- `TestCC_BBRForCritical` — Critical streams use BBR.
- `TestCC_CUBICForBulk` — Bulk streams use CUBIC.
- `TestCC_LEDBATForBackground` — Background streams use LEDBAT.
- `TestCC_BBRLatencyUnderContention` — BBR maintains latency
  target under simulated contention.
- `TestCC_LEDBATBacksOff` — LEDBAT throttles when foreground
  traffic present.

**Integration tests**:
- `TestCC_CriticalUnaffectedByBulk` — Bulk flood doesn't degrade
  Critical p99.
- `TestCC_LEDBATIdleFill` — Background fills idle; vacates on
  foreground arrival.

**End-to-end tests**:
- `TestCCE2E_RealisticMix` — Realistic class mix; each class meets
  its latency / throughput target.

**Race condition tests**:
- `TestCC_ConcurrentClassedStreams` — `go test -race` mixed-class
  parallel streams; per-class CC state isolated.

**Negative / non-happy path tests**:
- `TestCC_UnknownAlgorithm_FallsBackToCUBIC` — Misconfigured CC
  falls back safely.
- `TestCC_BBRPathologicalRTT_FallsBackToCUBIC` — BBR pathological
  case (RTT measurement broken); falls back.
- `TestCC_LEDBATStarvedByBackground_BoundedRecovery` — Background
  starves; bounded recovery on heal.

---

#### 25.4 — Stream-level priority isolation (per-class connections)

**Description**: Per §4.5. Critical class gets its own QUIC connection
between peer pairs to eliminate HoL inversion.

**Implementation approach**:
- Per-`(peer, class)` connection pool: separate mTLS context per
  class.
- Critical/Standard/Bulk each get connections; Background shares
  Standard or has its own.
- Connection pool sized per cluster config; default 4 connections
  per peer pair.
- Idle connections reaped after timeout.
- Code path: `core/substrate/transport/conn_pool.go` (~800 LOC).

**Acceptance criteria**:
- Per-class connection pool keyed by `(peer, class)`.
- Critical class never shares connection with Bulk.
- HoL inversion impossible across class boundaries.
- Per-peer overhead bounded (~4 connections × peer count).
- Idle connection reaping prevents unbounded growth.

**Unit tests**:
- `TestConnPool_PerClassConnection` — Each class has own conn.
- `TestConnPool_NoCriticalBulkSharing` — Verified.
- `TestConnPool_IdleReaping` — Idle conns reaped.
- `TestConnPool_BoundedSize` — Pool size bounded.

**Integration tests**:
- `TestConnPool_HoLInversionEliminated` — Bulk packet HoL doesn't
  block Critical.
- `TestConnPool_ConnectionFailureScopedToClass` — One conn fails;
  other class conns unaffected.

**End-to-end tests**:
- `TestConnPoolE2E_HighDensityPeers` — Many peers; pool bounded;
  isolation preserved.

**Race condition tests**:
- `TestConnPool_ConcurrentDispatchAcrossClasses` — `go test -race`
  parallel dispatch; class isolation preserved.
- `TestConnPool_ReapingDuringActive` — Reaper concurrent with
  active streams; doesn't kill in-use.

**Negative / non-happy path tests**:
- `TestConnPool_AllConnsExhausted_QueuedNotFailed` — Pool
  exhausted; queued; not silently dropped.
- `TestConnPool_PeerDeath_ClassPoolCleanedUp` — Peer dies; all
  class conns cleaned up.
- `TestConnPool_SVIDRotationMidStream_Reconnects` — SVID
  rotation; new connection created cleanly.

---

#### 25.5 — Pre-trained Zstd dictionaries per schema

**Description**: Per §4.6. Per-schema pre-trained zstd dict shipped at
schema registration; receivers cache by `(schema_id, dict_version)`.

**Implementation approach**:
- Dict training: at schema registration, sample N representative
  bodies (configurable); train zstd dict via `klauspost/compress/zstd
  /dict`.
- Dict ID stored in schema entry; receivers fetch by ID; cache
  bounded.
- Compression at publish, decompression at receive — both use cached
  dict for that schema's frames.
- Re-train trigger: corpus drift detection (compression ratio drops
  below threshold) → re-train + new dict version.
- Code path: `core/substrate/wire/zstd_dict.go` (~700 LOC).

**Acceptance criteria**:
- Per-schema dict trained at registration.
- Compression ratio 10-20× on typed bodies vs raw zstd.
- Dict size bounded (≤ 100KB typical).
- Receivers cache dicts; bounded cache.
- Dict version handled for migration.
- Re-train on drift detection.

**Unit tests**:
- `TestZstdDict_TrainAtRegistration` — Dict trained.
- `TestZstdDict_CompressionRatio` — 10-20× on typed corpus.
- `TestZstdDict_DictSizeBounded` — ≤ 100KB.
- `TestZstdDict_VersionMigration` — Old dict version still
  decompresses.
- `TestZstdDict_DriftTriggersRetrain` — Drift triggers new version.

**Integration tests**:
- `TestZstdDict_RealSchemaCorpus` — Realistic Sylk schemas; ratio
  meets target.
- `TestZstdDict_BandwidthSavingMeasurable` — Substantial bandwidth
  reduction vs no-dict.

**End-to-end tests**:
- `TestZstdDictE2E_LiveTrafficCompression` — Live traffic;
  compression ratio sustained.

**Race condition tests**:
- `TestZstdDict_ConcurrentCompress` — `go test -race` parallel
  compress with shared dict; thread-safe.
- `TestZstdDict_RetrainRaceWithCompress` — Re-train concurrent
  with active compress; old dict still valid.

**Negative / non-happy path tests**:
- `TestZstdDict_DictMissingAtReceiver_FetchedOnDemand` — Receiver
  doesn't have dict; fetched.
- `TestZstdDict_DictCorruption_DetectedAndRefetched` — Corruption
  detected; refetched.
- `TestZstdDict_TrainingCorpusEmpty_FallsBackToRawZstd` — No
  corpus; falls back.
- `TestZstdDict_DictTooLargeForSchema_RejectedAtRegister` —
  Pathological; refused.

---

#### 25.6 — Header-only fast path

**Description**: Per §4.6. Receive-side routing decisions made on
header alone, without body parse.

**Implementation approach**:
- Existing 56-byte header (§4.1) already zero-copy.
- Receive pipeline reorders: header-decode → dedupe → authority →
  HLC → body-decode (only if previous steps pass).
- Bad frames rejected at <1µs without allocation.
- Code path: refactor `core/substrate/wire/validator.go` (~400 LOC
  net change).

**Acceptance criteria**:
- Bad-header frames rejected at <1µs.
- Body parse only attempted for valid-header frames.
- Allocation-free for rejected frames.
- DoS resistance: malicious peer can't induce body-parse memory
  pressure.

**Unit tests**:
- `TestFastPath_BadHeaderRejectedNoAlloc` — Bad header; no alloc.
- `TestFastPath_DedupeBeforeBodyParse` — Dedupe checked before body.
- `TestFastPath_HLCBeforeBodyParse` — HLC checked before body.
- `TestFastPath_AuthorityBeforeBodyParse` — Authority before body.
- `TestFastPath_RejectionLatencyBounded` — <1µs rejection.

**Integration tests**:
- `TestFastPath_DoSResistance` — Malicious flood; bounded memory.
- `TestFastPath_ValidFrameStillFullyValidated` — Good frames
  reach all checks.

**End-to-end tests**:
- `TestFastPathE2E_UnderFloodAttack` — Floor of bad frames;
  cluster operations unaffected.

**Race condition tests**:
- `TestFastPath_ConcurrentValidations` — `go test -race` 10K
  parallel validations; no race.

**Negative / non-happy path tests**:
- `TestFastPath_TruncatedHeader_RejectedNoAlloc` — Truncated;
  rejected without alloc.
- `TestFastPath_GarbageHeader_NoCrash` — Random bytes; no crash.
- `TestFastPath_HeaderValidBodyBad_RejectedAtBodyStep` — Valid
  header but bad body; rejected at body parse step.

---

#### 25.7 — Reed-Solomon FEC on UDP

**Description**: Per §4.6. Bulk-class medium frames split into k+m
shards; tolerate up to m losses with zero round trips.

**Implementation approach**:
- Library: `klauspost/reedsolomon` (k=8, m=2 default; configurable).
- Per-frame split: header + body chunked into k shards; m parity
  shards computed; all k+m shipped as UDP datagrams.
- Receiver: any k decode the frame; verify Merkle root.
- Per-frame state: `(frame_id, shard_idx, k, m, total_frame_size)`.
- Code path: `core/substrate/transport/udp_fec.go` (~1000 LOC).

**Acceptance criteria**:
- Bulk medium frames (1-64KB) eligible for FEC.
- k=8, m=2 default; tolerates 2 losses.
- Decode possible from any k of (k+m) shards.
- Merkle root verifies decoded frame.
- Per-frame state bounded.
- Throughput improvement under loss measurable.

**Unit tests**:
- `TestUDPFEC_EncodeDecodeRoundTrip` — Round-trip preserves bytes.
- `TestUDPFEC_DecodeFromKShards` — Any k shards decode.
- `TestUDPFEC_TolerateMLosses` — m losses recoverable.
- `TestUDPFEC_MerkleVerifyDecoded` — Decoded frame matches root.
- `TestUDPFEC_PerFrameStateBounded` — State bounded.

**Integration tests**:
- `TestUDPFEC_LossyLink_ThroughputImprovement` — 5% loss;
  throughput vs naive UDP measurably better.
- `TestUDPFEC_NoExtraTrips` — No retransmits required.

**End-to-end tests**:
- `TestUDPFECE2E_CrossDCBulk` — Real cross-DC; bulk replication
  throughput improved under simulated loss.

**Race condition tests**:
- `TestUDPFEC_ConcurrentEncode` — `go test -race` parallel encode.
- `TestUDPFEC_ShardArrivalReorder` — Shards arrive in random
  order; decode succeeds.

**Negative / non-happy path tests**:
- `TestUDPFEC_TooManyLosses_FrameDropped` — > m losses; frame
  dropped; reported.
- `TestUDPFEC_TamperedShard_DetectedViaMerkle` — Tampered shard;
  decoded frame fails check; rejected.
- `TestUDPFEC_ShardSizeMismatch_Rejected` — Bad shard size;
  rejected.
- `TestUDPFEC_DuplicateShard_Idempotent` — Duplicate shard;
  no double-decode.
- `TestUDPFEC_StaleShardFromOldFrame_Discarded` — Late shard from
  prior frame; discarded.

---

#### 25.8 — Bloom-filter interest broadcasts

**Description**: Per §4.6. Subscribers broadcast Bloom of subject IDs
they want; publishers filter destinations by interest.

**Implementation approach**:
- Subscriber: maintain Bloom filter of subscribed subject IDs;
  broadcast every N seconds + on subscription change.
- Publisher: maintain per-peer Bloom; on publish, check peer's
  Bloom; skip non-interested peers.
- Bloom parameters: target FP rate ≤ 1%, sized for max active
  subjects.
- Code path: `core/substrate/delivery/bloom_interest.go` (~700 LOC).

**Acceptance criteria**:
- Subscribers broadcast Bloom; publishers consume.
- FP rate ≤ 1% at expected scale.
- Bloom size bounded.
- Subscription churn handled (Bloom rebuild + broadcast).
- Bandwidth saving measurable for high-fan-out subjects.

**Unit tests**:
- `TestBloom_FilterCorrectness` — Subscribed subjects pass; others
  blocked.
- `TestBloom_FPRateBounded` — ≤ 1%.
- `TestBloom_SizeBounded` — Bounded.
- `TestBloom_RebuildOnChurn` — Subscription change triggers rebuild.

**Integration tests**:
- `TestBloom_HighFanOut_BandwidthSaving` — 10K subscribers;
  bandwidth measurably saved.
- `TestBloom_PerPeerInterestPropagation` — Per-peer Blooms updated.

**End-to-end tests**:
- `TestBloomE2E_GlobalBroadcast` — Global broadcast; only
  interested nodes receive.

**Race condition tests**:
- `TestBloom_ConcurrentSubscribeUnsubscribe` — `go test -race`;
  Bloom consistent.
- `TestBloom_BroadcastDuringChurn` — Broadcast race with churn;
  eventual consistency.

**Negative / non-happy path tests**:
- `TestBloom_FPCausesUnnecessaryDelivery_DropsAtSubscriber` —
  False positive at publisher; subscriber drops.
- `TestBloom_StaleBloom_BoundedStaleness` — Bloom stale;
  bounded; refreshed on next broadcast.
- `TestBloom_BloomCorruption_DetectedViaSig` — Tampered Bloom;
  detected via §17.1 sig.
- `TestBloom_OversizedBloom_Rejected` — Bloom exceeding size
  limit rejected.

---

#### 25.9 — Coalesced piggyback frames

**Description**: Per §4.6. One frame carries (ack-batch +
credit-advertisement + HLC-tick + Bloom-update + skew-telemetry).

**Implementation approach**:
- New piggyback frame format: variant of existing 56-byte header
  with `MsgType = PIGGYBACK_COALESCED`; body is CBOR struct with
  optional fields per category.
- Receiver dispatches each section to appropriate subsystem.
- Sender coalesces opportunistically; flushes on size threshold or
  time threshold.
- Code path: `core/substrate/transport/piggyback.go` (~800 LOC).

**Acceptance criteria**:
- Single frame carries multiple piggyback categories.
- Allocation-free header read.
- Sections optional; only present when populated.
- Bandwidth saving measurable vs separate frames.
- Backward-compatible (peers without coalescing still work).

**Unit tests**:
- `TestPiggyback_AllSectionsPresent` — Frame with all carries
  delivers all.
- `TestPiggyback_OptionalSections` — Missing sections don't
  break.
- `TestPiggyback_AllocFreeHeader` — Zero allocs on header.
- `TestPiggyback_BandwidthSaving` — Saving measured.

**Integration tests**:
- `TestPiggyback_DispatchPerSection` — Each section reaches
  handler.
- `TestPiggyback_BackwardCompatNonCoalescing` — Old peer still
  works.

**End-to-end tests**:
- `TestPiggybackE2E_RealClusterTraffic` — Live traffic; bandwidth
  reduced vs uncoalesced.

**Race condition tests**:
- `TestPiggyback_ConcurrentCoalescing` — `go test -race` multiple
  goroutines coalescing.
- `TestPiggyback_FlushDuringAdd` — Flush race with add; consistent.

**Negative / non-happy path tests**:
- `TestPiggyback_MalformedSection_Skipped` — One section bad;
  others delivered.
- `TestPiggyback_OversizedFrame_Split` — Exceeds size; split into
  multiple piggyback frames.
- `TestPiggyback_SectionVersionMismatch_BackwardCompat` — Mixed
  versions handled.

---

#### 25.10 — Schnorr signature aggregation

**Description**: Per §4.6. Batched delivery uses Schnorr aggregate
signatures (one verify per batch instead of per frame).

**Implementation approach**:
- Library: `cloudflare/circl/sign/schnorr` (BIP340-style or BLS
  alternative).
- Sender: when batching frames (snapshot install, replay), sign each
  per §17.1, then aggregate signatures.
- Receiver: batch verify in O(1) instead of O(N).
- Per-batch state: aggregate signature, list of frame headers.
- Code path: `core/substrate/identity/schnorr_agg.go` (~600 LOC).

**Acceptance criteria**:
- Aggregate signature scheme implemented.
- Per-frame sigs aggregable; aggregate verifies in one operation.
- Compatibility: per-frame sigs still valid individually.
- Verification cost: ~constant regardless of batch size.

**Unit tests**:
- `TestSchnorrAgg_SingleAggregate` — N sigs aggregate to one.
- `TestSchnorrAgg_AggregateVerifies` — Aggregate valid.
- `TestSchnorrAgg_PerFrameSigStillValid` — Individual sigs valid.
- `TestSchnorrAgg_ConstantTimeVerify` — Verify time constant.

**Integration tests**:
- `TestSchnorrAgg_SnapshotInstallBatch` — Snapshot install batch;
  one verify.
- `TestSchnorrAgg_BulkReplayBatch` — Bulk replay; verify cost
  bounded.

**End-to-end tests**:
- `TestSchnorrAggE2E_LargeSnapshotInstall` — Real large snapshot;
  install time improved.

**Race condition tests**:
- `TestSchnorrAgg_ConcurrentAggregation` — `go test -race`
  parallel aggregation; consistent.

**Negative / non-happy path tests**:
- `TestSchnorrAgg_TamperedSig_DetectedAtVerify` — One frame's sig
  tampered; aggregate fails; offending frame identified via
  fallback per-frame check.
- `TestSchnorrAgg_PartialBatch_FallsBackToIndividual` — Some
  frames missing; falls back to per-frame verify.
- `TestSchnorrAgg_KeyMismatchInBatch_AggregateRejected` — Mixed
  keys in batch; aggregate fails.

---

#### 25.11 — RDMA intra-DC zero-copy

**Description**: Per §4.6. RoCEv2 verbs API for zero-copy delivery
between same-rack hosts.

**Implementation approach**:
- Library: `rdma-core` Go bindings (cgo) for RoCEv2 verbs.
- Per-peer detection: if RDMA-capable + same-rack (Vivaldi
  intra-rack section), advertise RDMA endpoint.
- Storage layer reads pages directly into peer memory via
  RDMA write.
- Falls back to QUIC when RDMA unavailable.
- Code path: `core/substrate/transport/rdma.go` (~1500 LOC + cgo).

**Acceptance criteria**:
- RoCEv2 detected; RDMA endpoints advertised when available.
- Same-rack peers use RDMA for Bulk-class transfers.
- Read latency ~500ns (vs QUIC ~50µs).
- Falls back to QUIC when hardware unavailable.
- Same wire format (page format unchanged).

**Unit tests**:
- `TestRDMA_DetectionRoCEv2` — RoCE detected.
- `TestRDMA_FallbackToQUIC` — Without RDMA, QUIC used.
- `TestRDMA_PerPeerSelection` — Same-rack uses RDMA; cross-rack
  doesn't.
- `TestRDMA_LatencyMeasured` — < 1µs measurement.

**Integration tests**:
- `TestRDMA_BulkReplicationViaRDMA` — Bulk replication uses RDMA.
- `TestRDMA_RaftAppendEntriesViaRDMA` — Raft replication via RDMA
  on same-rack peers.

**End-to-end tests**:
- `TestRDMAE2E_RealHardware` — Real RoCEv2 hardware; throughput +
  latency targets met.

**Race condition tests**:
- `TestRDMA_ConcurrentReadsTargetingSamePage` — `go test -race`
  parallel RDMA reads of same page; consistent.
- `TestRDMA_PathFailoverDuringRead` — RDMA path fails mid-read;
  failover to QUIC.

**Negative / non-happy path tests**:
- `TestRDMA_HardwareFailure_FallsBackToQUIC` — RDMA NIC failure;
  falls back; alert.
- `TestRDMA_PermissionDenied_NoSilentFailure` — Memory region
  ACL blocks; explicit error.
- `TestRDMA_IBDeviceLost_RecoveredViaQUIC` — IB device lost;
  recovery via QUIC.
- `TestRDMA_MTUMismatch_HandledGracefully` — MTU mismatch on
  fabric; handled.

---

#### 25.12 — Network coding cross-DC

**Description**: Per §4.6. Linear network coding extends FEC to
multi-frame mixing for cross-DC bulk subjects.

**Implementation approach**:
- Per-flow encoder: combines N consecutive frames via linear
  combination over GF(256); ships N+m encoded packets.
- Receiver decodes once N+ encoded packets received.
- Tolerates packet loss without HoL retransmits.
- Per-subject opt-in via schema.
- Code path: `core/substrate/transport/netcoding.go` (~800 LOC).

**Acceptance criteria**:
- Per-flow linear coding over GF(256).
- Tolerates random packet loss without retransmission.
- Tail latency reduction measurable on lossy WAN.
- Per-subject opt-in.
- Bounded per-flow state.

**Unit tests**:
- `TestNetCoding_EncodeDecode` — Round-trip.
- `TestNetCoding_LossTolerance` — Random loss tolerated.
- `TestNetCoding_TailLatencyImproved` — Reduces tail.
- `TestNetCoding_StateBounded` — Bounded per flow.

**Integration tests**:
- `TestNetCoding_LossyCrossDC` — Simulated lossy WAN; tail
  latency measurably better.
- `TestNetCoding_OptInPerSubject` — Per-subject opt-in respected.

**End-to-end tests**:
- `TestNetCodingE2E_RealCrossDC` — Real cross-DC; bandwidth +
  latency benefit on lossy peering.

**Race condition tests**:
- `TestNetCoding_ConcurrentFlows` — `go test -race` parallel
  flows; isolated.

**Negative / non-happy path tests**:
- `TestNetCoding_TooMuchLoss_FrameDropped` — Loss exceeds budget;
  frame lost; reported.
- `TestNetCoding_TamperedEncodedPacket_DetectedViaMerkle` —
  Tampered; decoded frame fails check.
- `TestNetCoding_FlowStateOverflow_Reset` — State overflows;
  flow reset cleanly.

---

### Phase 26 — SQLite-Compatible Subjects (Foundation)

Sylk-native SQLite-compatible engine, dual-granularity replication,
MVCC, lazy paging, multi-process WAL coordination, SQL operations
exposed to substrate primitives. Per §11.8.

**Phase implementation overview**: Phase 26 builds Sylk's own
SQLite-compatible engine for `kind=sqlite` subjects. Turso (`../turso`)
is a reference implementation we draw from (page format, MVCC,
CDC pragma, async I/O, lazy storage, deterministic-simulation
testing) but the production target is Sylk-native Go code with
direct substrate integration. The substrate provides causal Merkle
DAG durability, multi-Raft replication, encryption envelope, tiered
storage, and audit; the engine provides the SQLite surface, MVCC,
WAL, and CDC. Common dependencies: SQLite file/wire format reference
(public spec); `mattn/go-sqlite3` style driver shim for compatibility
testing; turso source as architectural reference (Rust → Go port of
specific subsystems where prudent).

#### 26.1 — SQLite-compatible engine (Sylk-native)

**Description**: Per §11.8. Sylk-native SQLite-compatible engine in Go;
draws from turso reference for page format, MVCC algorithm, CDC pragma
shape, lazy storage; integrated directly with substrate primitives.

**Implementation approach**:
- Pure-Go implementation; no cgo, no Rust dependency at runtime.
- Page format: SQLite 3.x file format (preserves wire/file
  compatibility); page layout matches turso's improvements where
  applicable.
- WAL: integrated with substrate's causal Merkle DAG; WAL frames
  ARE substrate entries for the `pages/v1` subject (§26.2).
- Connection: one substrate connection multiplexes many SQL
  connections; per-conn MVCC via in-memory transaction store.
- B-tree, pager, virtual machine layers are first-class Sylk code
  with extension points for §27 features (CRDT tables, columnar
  projections, etc.).
- Driver: implements `database/sql/driver` interface; `mattn/go-
  sqlite3` test suite passes.
- Code path: `core/substrate/engines/sqlite/` (~25K LOC for engine
  proper).

**Acceptance criteria**:
- SQLite 3.x file format read + write.
- SQL surface compatible with SQLite syntax (CREATE, INSERT, UPDATE,
  DELETE, SELECT, CREATE INDEX, ALTER TABLE, JOIN, GROUP BY, etc.).
- `database/sql/driver` interface; existing apps work unchanged.
- WAL discipline integrated with substrate Layer 4 (causal Merkle DAG).
- MVCC for `BEGIN CONCURRENT`.
- CDC pragma emits row-level deltas to substrate `cdc/v1` subject.
- Existing turso `.tshm` databases readable; sylk auto-promotes to
  `.sshm` on first write.
- SQLite test corpus (sqlite-tcl tests adapted) passes.
- Determinism harness (§24.1) validates: same input → same on-disk
  bytes across replicas.

**Unit tests**:
- `TestSQLiteEngine_BasicCRUD` — CRUD operations work.
- `TestSQLiteEngine_TransactionCommit` — BEGIN/COMMIT works.
- `TestSQLiteEngine_TransactionRollback` — ROLLBACK works.
- `TestSQLiteEngine_WALWrite` — WAL writes are substrate entries.
- `TestSQLiteEngine_MultiConnection` — Multiple conns isolated.
- `TestSQLiteEngine_FileFormatCompat` — File compatible with vanilla
  SQLite + turso readers.
- `TestSQLiteEngine_BTreeOperations` — B-tree split/merge correct.
- `TestSQLiteEngine_QueryPlanner` — Cost-based plans correct.
- `TestSQLiteEngine_DeterministicState` — Bit-equal across replicas.

**Integration tests**:
- `TestSQLiteEngine_GoSQLDriverCompat` — `database/sql` driver suite.
- `TestSQLiteEngine_VanillaSQLiteRead` — Read SQLite-produced files.
- `TestSQLiteEngine_TursoFileRead` — Read turso-produced files;
  auto-promote `.tshm` → `.sshm`.
- `TestSQLiteEngine_SQLiteTestCorpus` — Adapted SQLite-tcl tests pass.
- `TestSQLiteEngine_TursoTestCorpus` — Adapted turso compat tests pass.

**End-to-end tests**:
- `TestSQLiteEngineE2E_RealisticApp` — Real app (e.g., session
  storage) works on Sylk engine + substrate.
- `TestSQLiteEngineE2E_MigrationFromVanillaSQLite` — Drop in Sylk
  engine for app currently running on vanilla SQLite; behavior
  identical except for substrate features.

**Race condition tests**:
- `TestSQLiteEngine_ConcurrentConnections` — `go test -race`
  parallel conns; correct results.
- `TestSQLiteEngine_WALConcurrentWrites` — Concurrent writes
  serialized via WAL.
- `TestSQLiteEngine_BTreeConcurrentReadWrite` — Tree ops safe.
- `TestSQLiteEngine_PagerConcurrentEvict` — Page eviction race.

**Negative / non-happy path tests**:
- `TestSQLiteEngine_MalformedSQL_RejectedWithError` — Bad SQL;
  parse error returned.
- `TestSQLiteEngine_ConstraintViolation_TransactionRolledBack` —
  Constraint violation; rollback.
- `TestSQLiteEngine_DiskFullDuringCommit_TransactionAborted` —
  Disk full; abort.
- `TestSQLiteEngine_CorruptedFile_DetectedAtOpen` — File corruption
  detected via Merkle (substrate) + checksums (engine).
- `TestSQLiteEngine_VersionMismatchedFile_Rejected` — File from
  incompatible engine version detected.
- `TestSQLiteEngine_DeterminismViolation_FlaggedByHarness` —
  Non-determinism flagged at §24.1 harness.

---

#### 26.2 — Two-subject pattern (pages + CDC)

**Description**: Per §11.8. Each SQLite database backed by two
substrate subjects: page-delta and CDC.

**Implementation approach**:
- `pages` subject: WAL frames mapped to page-update entries.
- `cdc` subject: turso CDC pragma stream; row-level deltas.
- Both subjects HLC-ordered; both reference same turso revision.
- Subscribers select granularity: pages for catch-up, CDC for
  live tail.
- SM apply guarantees both subjects converge.
- Code path: `core/substrate/engines/sqlite/dual_subject.go`
  (~1500 LOC).

**Acceptance criteria**:
- Both subjects automatically created on `CREATE TABLE`.
- Each WAL frame produces one `pages` entry.
- Each CDC row change produces one `cdc` entry.
- HLC-ordered.
- Subscribers can subscribe to either or both.
- Bit-equal state from either subject's replay.

**Unit tests**:
- `TestDualSubject_PagesEntryPerWALFrame` — One per frame.
- `TestDualSubject_CDCEntryPerRowChange` — One per CDC row.
- `TestDualSubject_HLCOrdering` — Ordered by HLC.
- `TestDualSubject_BitEqualReplayFromEither` — Either subject
  replays to same state.

**Integration tests**:
- `TestDualSubject_LiveReplicationViaCDC` — CDC for live tail.
- `TestDualSubject_CatchupViaPages` — Pages for cold catch-up.
- `TestDualSubject_GranularitySwitch` — Switch from pages to CDC
  at frontier.

**End-to-end tests**:
- `TestDualSubjectE2E_GlobalReplication` — Real cross-DC; live
  replication via CDC; catch-up via pages.

**Race condition tests**:
- `TestDualSubject_ConcurrentWritesBothSubjects` — `go test -race`
  concurrent writes; both subjects consistent.
- `TestDualSubject_GranularitySwitchRace` — Switch race;
  no gaps, no duplicates.

**Negative / non-happy path tests**:
- `TestDualSubject_PagesSubjectAheadOfCDC_BackpressureCDC` —
  Pages ahead; CDC catches up; no inconsistency.
- `TestDualSubject_CDCDisabled_PagesStillWork` — CDC pragma
  disabled; pages still functional.
- `TestDualSubject_DivergenceDetected_FailoverToCanonical` —
  Subjects diverge (bug); detected; canonical (pages) wins;
  alert.

---

#### 26.3 — BEGIN CONCURRENT MVCC integration

**Description**: Per §11.8. Turso's MVCC for intra-replica multi-
writer concurrency.

**Implementation approach**:
- Expose `BEGIN CONCURRENT` via SQL; backend uses turso's
  `core/mvcc/`.
- Conflict detection at commit time (write-write conflicts).
- Cross-replica conflicts handled at Raft layer.
- Code path: `core/substrate/engines/sqlite/mvcc.go` (~1000 LOC).

**Acceptance criteria**:
- `BEGIN CONCURRENT` accepted as SQL statement.
- Multiple writers commit if write sets don't overlap.
- Conflict returns specific error; caller retries.
- Read snapshot isolation correct.

**Unit tests**:
- `TestMVCC_NonOverlappingWritesBothCommit` — Two non-overlapping
  writes commit.
- `TestMVCC_OverlappingWritesOneAborts` — Overlap; one wins.
- `TestMVCC_SnapshotIsolation` — Read sees consistent snapshot.
- `TestMVCC_ConflictRetryable` — Conflict; retry works.

**Integration tests**:
- `TestMVCC_HighConcurrencyWorkload` — Realistic high concurrency.

**End-to-end tests**:
- `TestMVCCE2E_TursoCompatibility` — Turso's MVCC test suite
  (`hermitage_tests`) passes.

**Race condition tests**:
- `TestMVCC_ConcurrentBeginCommit` — `go test -race` concurrent
  txns; correct serialization.

**Negative / non-happy path tests**:
- `TestMVCC_TransactionAbortDueToConflict_StateIntact` — Abort;
  no partial state.
- `TestMVCC_LongRunningTxnGCStarvation_ResolvedByYielding` —
  Long txn doesn't starve GC.
- `TestMVCC_VersionGarbageCollectionBoundedMemory` — GC runs;
  memory bounded.

---

#### 26.4 — Page-level + row-level dual-granularity replication

**Description**: Per §11.8. Substrate subscribers select replication
granularity (pages for catch-up, rows for live tail).

**Implementation approach**:
- Subscriber declares preference at subscription time.
- Substrate routes corresponding subject(s).
- Catch-up flow: page subscription until frontier reached, then
  switch to CDC.
- Code path: `core/substrate/engines/sqlite/granularity.go`
  (~500 LOC).

**Acceptance criteria**:
- Subscriber selects pages or CDC or both.
- Catch-up flow automatic.
- Bandwidth saving for live tail (CDC) measurable.

**Unit tests**:
- `TestGranularity_PagesOnlySubscription` — Only pages delivered.
- `TestGranularity_CDCOnlySubscription` — Only CDC delivered.
- `TestGranularity_BothDelivered` — Both delivered.
- `TestGranularity_AutoSwitchAtFrontier` — Switch happens.

**Integration tests**:
- `TestGranularity_BandwidthCDCLessThanPages` — CDC bandwidth <
  pages for live updates.

**End-to-end tests**:
- `TestGranularityE2E_BootstrapThenLiveTail` — Bootstrap via
  pages, live via CDC, bandwidth profile matches expectation.

**Race condition tests**:
- `TestGranularity_SwitchRace` — Switch concurrent with writes;
  no gaps.

**Negative / non-happy path tests**:
- `TestGranularity_CDCFallsBehindPages_Reswitch` — CDC backlog;
  re-switch to pages.
- `TestGranularity_BadSubscriptionPreference_Rejected` — Invalid
  preference rejected.

---

#### 26.5 — Schema-aware row diffs

**Description**: Per §11.8. Row-level diffs encoded with schema
awareness — column mask + new values, not full rows.

**Implementation approach**:
- CDC entry encoder: `(rowid, column_mask, [new_values])`; uses
  schema's pre-trained zstd dict (§25.5).
- Receiver decodes column mask; applies only changed columns.
- Wire size 30-100 bytes typical for UPDATE.
- Code path: `core/substrate/engines/sqlite/row_diff.go` (~700 LOC).

**Acceptance criteria**:
- UPDATE encodes as column mask + values.
- INSERT encodes full row (efficient).
- DELETE encodes as rowid only.
- Wire size 50-100x smaller than turso's 4KB page for typical
  UPDATE.
- Round-trip lossless.

**Unit tests**:
- `TestRowDiff_UpdateColumnMask` — UPDATE → column mask.
- `TestRowDiff_InsertFullRow` — INSERT → full row.
- `TestRowDiff_DeleteRowidOnly` — DELETE → rowid.
- `TestRowDiff_RoundTrip` — Encode + decode → original state.
- `TestRowDiff_WireSizeBound` — UPDATE ≤ 100 bytes typical.

**Integration tests**:
- `TestRowDiff_BandwidthSavingMeasurable` — vs page deltas.

**End-to-end tests**:
- `TestRowDiffE2E_LiveReplicationBandwidth` — Live replication
  bandwidth meets target.

**Race condition tests**:
- `TestRowDiff_ConcurrentEncodeDecode` — `go test -race`.

**Negative / non-happy path tests**:
- `TestRowDiff_SchemaMismatchAtReceiver_Rejected` — Schema
  mismatch; rejected with specific error.
- `TestRowDiff_MalformedColumnMask_Rejected` — Bad mask rejected.
- `TestRowDiff_ColumnRemovedAtReceiver_Migrated` — Schema
  evolution handled via §31.17.

---

#### 26.6 — Predicate pushdown to replication

**Description**: Per §11.8. Subscribers declare predicate; substrate
filters at writer side.

**Implementation approach**:
- Subscription includes optional predicate (subset of SQL WHERE).
- Predicate compiled to native Go via §31.1 Tier 1 DSL.
- Writer-side evaluation: per CDC entry, check predicate; ship only
  matching.
- Combined with §25.8 Bloom interest for coarse-grained pre-filter.
- Code path: `core/substrate/engines/sqlite/predicate_push.go`
  (~1000 LOC).

**Acceptance criteria**:
- Subscription accepts predicate.
- Predicate compiles to native Go.
- Writer-side filtering reduces bandwidth proportional to
  selectivity.
- Composes with geo-fenced rows (§31.19).

**Unit tests**:
- `TestPredicatePush_CompileToNative` — Compiles cleanly.
- `TestPredicatePush_FilterAtWriter` — Only matching rows shipped.
- `TestPredicatePush_BandwidthProportional` — Saving proportional
  to selectivity.

**Integration tests**:
- `TestPredicatePush_ComposesWithGeoFence` — Geo-fence + predicate
  composes.
- `TestPredicatePush_HighSelectivityWorkload` — High selectivity
  (1% match); 99% bandwidth saved.

**End-to-end tests**:
- `TestPredicatePushE2E_RegionalSubscription` — Regional
  subscription `WHERE region = 'us-west'`; only matching rows
  delivered cross-region.

**Race condition tests**:
- `TestPredicatePush_ConcurrentEvaluation` — `go test -race`.

**Negative / non-happy path tests**:
- `TestPredicatePush_BadSyntax_RejectedAtSubscribe` — Bad
  predicate syntax rejected.
- `TestPredicatePush_PredicatePerformanceRegression_FallsBackToFullStream`
  — Pathological predicate; substrate falls back to full stream
  + receiver-side filter.
- `TestPredicatePush_PredicateOnEvolvedSchema_HandledViaTransform`
  — Schema evolution; predicate adapted.

---

#### 26.7 — Sparse storage / lazy paging

**Description**: Per §11.8. Pages on cold tier or remote substrate
faulted in on demand.

**Implementation approach**:
- Reuse turso's `database_sync_lazy_storage.rs` as a backend for
  §21.1 multi-tier storage.
- Sparse SQLite file: pages allocated on first access; missing pages
  faulted from cold tier or remote.
- Page fault → §21.1 tier read → install in hot tier → satisfy read.
- Edge tier (§20.2) uses same path with home-cluster as backend.
- Code path: `core/substrate/engines/sqlite/sparse.go` (~800 LOC,
  reusing turso machinery).

**Acceptance criteria**:
- DB starts as sparse file.
- Page fault triggers tier read.
- First-access latency proportional to tier distance; subsequent
  accesses hot.
- Combined with §21.1 tier policy.

**Unit tests**:
- `TestSparse_DBStartsAsSparse` — Sparse file confirmed.
- `TestSparse_PageFaultTriggersTier` — Fault → tier read.
- `TestSparse_HotAfterFirstAccess` — Subsequent fast.

**Integration tests**:
- `TestSparse_ColdTierFault` — Real cold tier; fault works.
- `TestSparse_EdgeTierFault` — Edge PoP; fault from home cluster.

**End-to-end tests**:
- `TestSparseE2E_LargeColdTierQuery` — Query against year-old
  data; lazy fault completes; correct result.

**Race condition tests**:
- `TestSparse_ConcurrentFaults` — `go test -race` parallel faults
  of same page; one fetch.
- `TestSparse_FaultDuringWrite` — Concurrent fault + write to
  same page; consistent.

**Negative / non-happy path tests**:
- `TestSparse_TierUnreachableDuringFault_RetryOrFail` — Tier
  unreachable; retry; eventual fail with specific error.
- `TestSparse_DiskFullDuringFaultIn_BackpressureNoCorruption` —
  No disk; backpressure; no corruption.
- `TestSparse_PageMissingFromAllTiers_AlertsOperator` — Missing
  everywhere; data loss alert.

---

#### 26.8 — Multi-process WAL coordination (`.sshm` sidecar)

**Description**: Per §11.8. Multiple sylk processes on one host
coordinate via sylk's `.sshm` sidecar — modeled after turso's `.tshm`
but owned and extended by sylk with substrate-specific coordination
(HLC stamps for cross-process happens-before, capability fences,
substrate-aware reaper).

**Implementation approach**:
- `.sshm` file per database; sylk processes participate as WAL
  readers/writers.
- Format: turso `.tshm` layout extended with sylk header (HLC tail,
  participant SVID hashes, substrate epoch).
- Substrate ensures `.sshm` semantics preserved through replication
  (sidecar is local-host coordination only; not shipped over the
  wire).
- Embedded mode (§16): multiple TUI / agent processes coexist.
- Daemon mode (§17): daemon owns one writer; TUIs are readers.
- Code path: `core/substrate/engines/sqlite/sshm.go` (~600 LOC).

**Acceptance criteria**:
- `.sshm` sidecar created per DB.
- Multiple processes read concurrently.
- Single-writer semantics preserved across processes.
- Process death cleaned up via `.sshm` heartbeat.
- Sylk-extended header (HLC tail, participant SVID hashes) populated
  by every joining process.
- Backward-compat read path: substrate can read databases with raw
  turso `.tshm` (auto-promotes to `.sshm` on first sylk write).

**Unit tests**:
- `TestSSHM_MultiProcessRead` — Multiple processes read.
- `TestSSHM_SingleWriter` — Only one writer at a time.
- `TestSSHM_ProcessDeathCleanup` — Dead process cleaned up.
- `TestSSHM_HeartbeatPerProcess` — Heartbeat preserved.
- `TestSSHM_HLCTailUpdated` — HLC tail advances per write.
- `TestSSHM_ParticipantSVIDsRecorded` — Joining processes
  recorded.
- `TestSSHM_AutoPromoteFromTSHM` — Reading turso-only DB
  auto-creates `.sshm` on first sylk write.

**Integration tests**:
- `TestSSHM_EmbeddedMultiTUI` — Embedded mode; multiple TUIs.
- `TestSSHM_DaemonModeReaders` — Daemon writer + TUI readers.
- `TestSSHM_HLCAcrossProcesses` — HLC monotonic across processes
  on same host.

**End-to-end tests**:
- `TestSSHME2E_RealMultiProcess` — Real multiple processes;
  no corruption.
- `TestSSHME2E_TursoCompatRead` — sylk reads existing turso DB
  (with `.tshm`) and operates correctly.

**Race condition tests**:
- `TestSSHM_ConcurrentProcessJoin` — `go test -race` processes
  joining; coordination correct.
- `TestSSHM_WriterDeathHandover` — Writer dies; new writer
  acquires cleanly.
- `TestSSHM_HeaderUpdateRace` — Concurrent header updates;
  serialized correctly.

**Negative / non-happy path tests**:
- `TestSSHM_StaleSidecar_Reaped` — Stale `.sshm` from crashed
  cluster; reaped at boot.
- `TestSSHM_PermissionDeniedSidecar_FailsClearly` — Permission
  issue; explicit error.
- `TestSSHM_FilesystemDoesntSupportLocking_FailsAtBoot` — FS
  without locking; fail-fast at boot with explicit message.
- `TestSSHM_HeaderCorrupted_RecoveryViaTSHMFallback` — `.sshm`
  header corrupt; falls back to underlying `.tshm` semantics;
  rebuilds sylk header on next write.
- `TestSSHM_VersionMismatchedHeader_Rejected` — `.sshm` written
  by incompatible sylk version detected; refuses to open until
  upgrade.
- `TestSSHM_ParticipantSVIDRevoked_HeartbeatRejected` — Revoked
  SVID's heartbeat rejected; participant evicted.

---

#### 26.9 — SQL operations exposed to substrate primitives

**Description**: Per §11.8. SQL syntax for backup, restore,
time-travel, redaction.

**Implementation approach**:
- New SQL statements parsed; compiled to substrate operator-group
  calls.
- `BACKUP DATABASE ... TO ... WITH (continuous=true)` → §21.3.
- `RESTORE DATABASE ... FROM ... AT HLC '<h>'` → §21.4.
- `SELECT ... AS OF HLC '<h>'` → §12.1.
- `ALTER TABLE ... REDACT FIELD ... WHERE ...` → §31.23.
- Code path: `core/substrate/engines/sqlite/sql_ops.go` (~1500
  LOC).

**Acceptance criteria**:
- Each SQL statement parsed correctly.
- Compiles to corresponding substrate API call.
- Authority predicate enforced (operator vs user).
- Errors actionable.

**Unit tests**:
- `TestSQLOps_BackupParsed` — Statement parsed.
- `TestSQLOps_RestoreParsed` — Statement parsed.
- `TestSQLOps_AsOfParsed` — Time-travel parsed.
- `TestSQLOps_RedactParsed` — Redaction parsed.
- `TestSQLOps_AuthorityEnforced` — Without permission, rejected.

**Integration tests**:
- `TestSQLOps_BackupContinuous` — Continuous backup wired.
- `TestSQLOps_PITR` — Point-in-time restore wired.
- `TestSQLOps_AsOfReturnsHistorical` — Historical state.
- `TestSQLOps_RedactionActuallyDestroysKey` — KMS confirms
  destruction.

**End-to-end tests**:
- `TestSQLOpsE2E_FullSQLAdminFlow` — Backup + restore + time-
  travel + redact via SQL on real cluster.

**Race condition tests**:
- `TestSQLOps_ConcurrentBackupAndWrites` — `go test -race`
  backup running with writes; consistent.

**Negative / non-happy path tests**:
- `TestSQLOps_RestoreWithoutAuthority_Rejected` — Restore without
  permission rejected.
- `TestSQLOps_AsOfBeforeRetention_ErrOutsideRetention` — Outside
  retention; specific error.
- `TestSQLOps_RedactNonexistentRow_GracefulNoOp` — No matching
  row; no-op.
- `TestSQLOps_BackupTargetUnreachable_Queued` — Target down;
  queued; retried.

---

### Phase 27 — SQLite Beyond Turso

Hybrid row+columnar, schema-aware page format, append-only tables,
CRDT tables, per-row consistency, distributed BEGIN CONCURRENT,
causal FKs, schemas-as-session-types, continuous queries, vector+SQL,
federated SQL, topology-aware optimizer, multi-engine txns,
probabilistic columns, time-series, privacy/compliance, online schema
changes, self-tuning indexes, per-row TTL, per-row provenance. Per
§11.9.

**Phase implementation overview**: Phase 27 extends turso with
substrate-native primitives that no production SQL engine ships. Each
item opt-in per table via `WITH (...)` clause; existing tables behave
as turso-vanilla. Per-feature feature flags allow operator gating.
Common dependencies: §31.4 differential dataflow runtime, §26.2 typed
CRDTs, §31.21 session types, HNSW (`coder/hnsw`), Gorilla compression
(`facebookarchive/gorilla`), `gnark` for ZK, Paillier for partial
homomorphic, `tensorflow/privacy` for DP.

#### 27.1 — Hybrid row + columnar dual representation

**Description**: Per §11.9. Same data, two layouts updated
atomically.

**Implementation approach**:
- Primary: turso's row B-tree.
- Secondary: Arrow/Parquet-shape columnar projection per table.
- Both updated atomically via single WAL apply; columnar lags by
  bounded amount (configurable).
- Query planner picks based on cost (scan ratio, projection size).
- Code path: `core/substrate/engines/sqlite/columnar/` (~3000
  LOC).

**Acceptance criteria**:
- Per-table opt-in via `WITH (storage = 'row+columnar')`.
- Both layouts maintained; bit-equal.
- Planner picks cheaper for each query.
- Storage cost ≤ 2x row-only.
- Scan speedup ≥ 10x for analytical queries.

**Unit tests**:
- `TestColumnar_BothLayoutsUpdated` — Atomic update verified.
- `TestColumnar_PlannerChoosesCheaper` — Cost-based selection.
- `TestColumnar_ScanSpeedup` — Measured.
- `TestColumnar_StorageCostBound` — ≤ 2x.

**Integration tests**:
- `TestColumnar_RealAnalyticalQuery` — OLAP query; columnar used.
- `TestColumnar_RealOLTPQuery` — Point query; row used.

**End-to-end tests**:
- `TestColumnarE2E_HTAPWorkload` — Mixed workload; both layouts
  serve appropriate queries.

**Race condition tests**:
- `TestColumnar_ConcurrentReadsAcrossLayouts` — `go test -race`.
- `TestColumnar_ColumnarLagDuringWrite` — Lag bounded.

**Negative / non-happy path tests**:
- `TestColumnar_DivergenceDetected_Repaired` — Divergence detected
  via hash; repaired from row-store.
- `TestColumnar_DiskBudgetExceeded_DropsColumnar` — Disk budget;
  columnar dropped per policy.
- `TestColumnar_LayoutMigrationFails_FallsBackToRowOnly` —
  Migration fails; falls back.

---

#### 27.2 — Schema-aware page format

**Description**: Per §11.9. Custom on-disk page layout per schema.

**Implementation approach**:
- Schema → page-layout codegen at registration: column-grouped,
  dict-encoded, bit-packed integers, frame-of-reference.
- Compatibility view exposes raw SQLite bytes when needed.
- Per-table opt-in via `WITH (page_format = 'schema-aware')`.
- Code path: `core/substrate/engines/sqlite/page_format/` (~2500
  LOC).

**Acceptance criteria**:
- Custom layout compiled at schema registration.
- 5-10x storage reduction.
- 10-100x scan speedup.
- Compatibility view available.
- Backward-compat with vanilla SQLite via export.

**Unit tests**:
- `TestPageFormat_StorageReduction` — 5-10x measured.
- `TestPageFormat_ScanSpeedup` — 10-100x measured.
- `TestPageFormat_CompatibilityView` — Raw bytes accessible.

**Integration tests**:
- `TestPageFormat_RealisticSchemas` — Real schema benchmarks.

**End-to-end tests**:
- `TestPageFormatE2E_WorkloadComparison` — Vanilla SQLite vs
  schema-aware; performance gains measurable.

**Race condition tests**:
- `TestPageFormat_ConcurrentReadsCustomLayout` — `go test -race`.

**Negative / non-happy path tests**:
- `TestPageFormat_SchemaEvolution_PageFormatMigrated` — Schema
  ALTER; page format regenerated; migration online.
- `TestPageFormat_PathologicalSchema_FallsBackToVanilla` — Schema
  doesn't benefit; falls back; warning.
- `TestPageFormat_BackwardCompatExport` — Export to vanilla
  SQLite works.

---

#### 27.3 — Append-only / event-sourced tables

**Description**: Per §11.9. `WITH (mutation = APPEND_ONLY)` refuses
UPDATE/DELETE.

**Implementation approach**:
- DDL flag stored in schema; SM rejects UPDATE/DELETE.
- Compaction skips tombstone reasoning (none exist).
- Time-travel queries O(scan range).
- Code path: `core/substrate/engines/sqlite/append_only.go`
  (~400 LOC).

**Acceptance criteria**:
- `WITH (mutation = APPEND_ONLY)` accepted.
- UPDATE/DELETE rejected with `ErrAppendOnly`.
- Compaction efficient.
- Time-travel queries fast.

**Unit tests**:
- `TestAppendOnly_FlagAccepted` — DDL accepted.
- `TestAppendOnly_UpdateRejected` — UPDATE → ErrAppendOnly.
- `TestAppendOnly_DeleteRejected` — DELETE → ErrAppendOnly.
- `TestAppendOnly_TimeTravelFast` — O(scan range).

**Integration tests**:
- `TestAppendOnly_CompactionEfficient` — No tombstones.

**End-to-end tests**:
- `TestAppendOnlyE2E_AuditTable` — Audit table workload; correct.

**Race condition tests**:
- `TestAppendOnly_ConcurrentInserts` — `go test -race`.

**Negative / non-happy path tests**:
- `TestAppendOnly_AlterTableMutationFlagChange_Refused` — Cannot
  flip flag mid-life.
- `TestAppendOnly_TruncateRefused` — TRUNCATE blocked.

---

#### 27.4 — CRDT tables in SQL

**Description**: Per §11.9. `WITH (crdt = '...')` columns; multi-
master converges.

**Implementation approach**:
- DDL parses CRDT type per column; maps to §26.2 typed CRDT subject.
- UPDATE on CRDT column dispatches to CRDT op (e.g.,
  `value = value + 1` → G-counter increment).
- Multi-master writes converge without coordination.
- Code path: `core/substrate/engines/sqlite/crdt_tables.go`
  (~1500 LOC, on top of §26.2).

**Acceptance criteria**:
- CRDT types: g-counter, pn-counter, or-set, lww-map, mv-register,
  rga, 2p-graph (per §26.2).
- DDL parses CRDT specification.
- UPDATE compiles to CRDT op.
- Multi-master writes converge under partition.
- Same SQL surface for callers.

**Unit tests**:
- `TestCRDTSQL_GCounterIncrement` — `value = value + 1` →
  G-counter op.
- `TestCRDTSQL_ORSetAddRemove` — Set ops.
- `TestCRDTSQL_MultiMasterConvergence` — Two masters; same final
  state.
- `TestCRDTSQL_DDLParse` — DDL accepted.

**Integration tests**:
- `TestCRDTSQL_CrossDCConvergence` — Cross-DC partition heals.
- `TestCRDTSQL_RealisticWorkload` — Real workload converges.

**End-to-end tests**:
- `TestCRDTSQLE2E_GlobalCounter` — Global counter; multi-region
  increments; converges.

**Race condition tests**:
- `TestCRDTSQL_ConcurrentMultiMasterWrites` — `go test -race`.

**Negative / non-happy path tests**:
- `TestCRDTSQL_NonCRDTOpOnCRDTColumn_Rejected` — Direct SET on
  G-counter rejected.
- `TestCRDTSQL_UnknownCRDTType_Rejected` — Bad type rejected.
- `TestCRDTSQL_CRDTTypeChangeRefused` — Type cannot change.

---

#### 27.5 — Per-row consistency choice

**Description**: Per §11.9. Each column declares `WITH (consistency
= '...')`; substrate enforces.

**Implementation approach**:
- DDL parses per-column consistency flag.
- Consistency mapped to §31.3 causal isolation levels per column
  on read.
- Replication path varies: linearizable → Raft; eventual → CRDT;
  causal → §31.3 causal.
- Code path: `core/substrate/engines/sqlite/per_row_consistency.go`
  (~1200 LOC).

**Acceptance criteria**:
- Per-column consistency: linearizable, eventual, monotonic-read,
  causal, read-your-writes.
- DDL parses correctly.
- Reads enforce per-column level.
- Different columns coexist in same row.

**Unit tests**:
- `TestPerRow_LinearizableColumn` — Linearizable read enforced.
- `TestPerRow_EventualColumn` — Eventual respected.
- `TestPerRow_MonotonicReadColumn` — Monotonic preserved.
- `TestPerRow_CausalColumn` — Causal enforced.
- `TestPerRow_ReadYourWrites` — RYW enforced.
- `TestPerRow_MixedColumnsCoexist` — Same row, different
  consistencies.

**Integration tests**:
- `TestPerRow_LatencyDifferences` — Eventual reads faster than
  linearizable.

**End-to-end tests**:
- `TestPerRowE2E_RealWorkload` — Mixed workload across columns.

**Race condition tests**:
- `TestPerRow_ConcurrentReadsDifferentLevels` — `go test -race`.

**Negative / non-happy path tests**:
- `TestPerRow_DowngradeAttempted_Rejected` — Read at weaker than
  declared rejected.
- `TestPerRow_UnknownLevel_Rejected` — Unknown rejected.
- `TestPerRow_RaftFailureLinearizableUnavailable_EventualStillWorks`
  — Linearizable column unavailable; eventual unaffected.

---

#### 27.6 — Distributed BEGIN CONCURRENT

**Description**: Per §11.9. Cross-cluster MVCC via `Publish(...,
expect=read_set_frontier)`.

**Implementation approach**:
- Transaction tracks `(reader_hlc, read_set)` per BEGIN CONCURRENT.
- Commit translates to substrate optimistic publish (§26.3).
- Conflict = write whose HLC ∈ (reader_hlc, current_hlc] on read_set
  key.
- Distributed via existing Raft replication.
- Code path: `core/substrate/engines/sqlite/dist_concurrent.go`
  (~1500 LOC).

**Acceptance criteria**:
- BEGIN CONCURRENT works across replicas.
- Conflict detected at commit.
- Caller retries on conflict.
- Snapshot isolation across cluster.

**Unit tests**:
- `TestDistConcurrent_NonOverlappingCommit` — Both commit.
- `TestDistConcurrent_OverlappingConflict` — Conflict; one wins.
- `TestDistConcurrent_SnapshotIsolation` — Snapshot consistent.
- `TestDistConcurrent_RetryAfterConflict` — Retry works.

**Integration tests**:
- `TestDistConcurrent_HighConcurrencyAcrossDCs` — Multi-DC
  workload.

**End-to-end tests**:
- `TestDistConcurrentE2E_RealisticContendedSubject` — Hot subject;
  correctness preserved across cluster.

**Race condition tests**:
- `TestDistConcurrent_ConcurrentTxnsAcrossReplicas` — `go test -race`.

**Negative / non-happy path tests**:
- `TestDistConcurrent_HighContentionAbortRate_RetriedSuccessfully`
  — High contention; retries succeed eventually.
- `TestDistConcurrent_PartitionDuringTxn_Aborted` — Partition;
  txn aborts; no split-brain.
- `TestDistConcurrent_ReadSetTooLarge_Refused` — Read set beyond
  bound; refused.

---

#### 27.7 — Causal foreign keys

**Description**: Per §11.9. `WITH (causality = 'happens-after')` —
rows invisible until referenced row's HLC committed.

**Implementation approach**:
- DDL parses causality clause.
- Read-time check: row B's HLC frontier must include referenced
  row A's commit HLC; otherwise row B invisible.
- Substrate enforces on every read.
- Code path: `core/substrate/engines/sqlite/causal_fk.go`
  (~1000 LOC).

**Acceptance criteria**:
- DDL accepts `WITH (causality = 'happens-after')`.
- Row visibility gated by causal HB.
- Reader's HLC frontier propagates correctly.
- Cross-region reads correctly delayed.

**Unit tests**:
- `TestCausalFK_DDLAccepted` — DDL parsed.
- `TestCausalFK_RowInvisibleUntilHLCCommitted` — Visibility gated.
- `TestCausalFK_RowVisibleOnceAncestorCommitted` — Visible after.
- `TestCausalFK_ReaderFrontierPropagates` — Frontier correct.

**Integration tests**:
- `TestCausalFK_CrossRegionDelay` — Cross-region reader sees row
  only after ancestor cross-region committed.

**End-to-end tests**:
- `TestCausalFKE2E_TestamentBeforeClaim` — Testament invisible
  until claim committed in reader's region.

**Race condition tests**:
- `TestCausalFK_HLCFrontierAdvanceRace` — `go test -race`.

**Negative / non-happy path tests**:
- `TestCausalFK_ReferencedRowDeleted_OrphanHandled` — Reference
  deleted; row marked orphan; configurable behavior.
- `TestCausalFK_CycleDetection` — Cyclic causality refused at
  DDL.
- `TestCausalFK_DeepChainScalingLatency` — Deep chain; latency
  bounded.

---

#### 27.8 — Schemas as session types

**Description**: Per §11.9. `CREATE PROTOCOL` DDL → §31.21 session
type enforcement.

**Implementation approach**:
- New DDL: `CREATE PROTOCOL <name> ON <table> AS <session_type>`.
- Compile to session type machine; deploy as substrate primitive.
- Each write checked against current state; protocol-violating
  writes rejected.
- Code path: `core/substrate/engines/sqlite/protocol_ddl.go`
  (~1500 LOC, on top of §31.21).

**Acceptance criteria**:
- DDL accepted.
- Session type compiled.
- Protocol-violating writes rejected with specific error.
- Per-row state machine state tracked.

**Unit tests**:
- `TestProtocolDDL_Parse` — DDL parsed.
- `TestProtocolDDL_ViolatingWriteRejected` — Out-of-order rejected.
- `TestProtocolDDL_ValidWriteAccepted` — In-order accepted.
- `TestProtocolDDL_PerRowState` — Per-row state tracked.

**Integration tests**:
- `TestProtocolDDL_RealClaimsBoardProtocol` — Full claims
  lifecycle protocol.

**End-to-end tests**:
- `TestProtocolDDLE2E_AdversarialPublisher` — Out-of-order writes
  rejected.

**Race condition tests**:
- `TestProtocolDDL_ConcurrentWritesSameRow` — `go test -race`.

**Negative / non-happy path tests**:
- `TestProtocolDDL_BadProtocolGrammar_RejectedAtDDL` — Grammar
  error.
- `TestProtocolDDL_UnknownRoleInProtocol_Rejected` — Unknown role.
- `TestProtocolDDL_ProtocolUpgradeIncompatible_Refused` — Upgrade
  not backward-compat refused.

---

#### 27.9 — Continuous queries

**Description**: Per §11.9. `SELECT ... WITH (continuous = true)` —
streaming delta.

**Implementation approach**:
- SQL parser extension; compile to §31.4 differential dataflow.
- Returns subscription handle; deltas pushed.
- Maintain per-subscription state; bounded memory.
- Code path: `core/substrate/engines/sqlite/continuous_query.go`
  (~2000 LOC, on §31.4).

**Acceptance criteria**:
- DDL extension parsed.
- Compiles to dataflow plan.
- Returns delta stream.
- Memory bounded per subscription.
- Max staleness configurable.

**Unit tests**:
- `TestContinuousQuery_Parse` — Parse extension.
- `TestContinuousQuery_DeltaStream` — Returns deltas.
- `TestContinuousQuery_MemoryBounded` — Bounded.
- `TestContinuousQuery_Aggregations` — GROUP BY works.

**Integration tests**:
- `TestContinuousQuery_RealDashboard` — Realistic dashboard query.

**End-to-end tests**:
- `TestContinuousQueryE2E_LiveAnalytics` — Live dashboard updates.

**Race condition tests**:
- `TestContinuousQuery_ConcurrentSubscriptions` — `go test -race`.

**Negative / non-happy path tests**:
- `TestContinuousQuery_PathologicalQueryUnboundedState_Refused` —
  Unbounded state query refused.
- `TestContinuousQuery_SubscriberDeath_StateCleanedUp` —
  Subscriber dies; state freed.
- `TestContinuousQuery_StalenessExceeded_Notified` — Staleness
  beyond bound; subscriber notified.

---

#### 27.10 — Vector + SQL native

**Description**: Per §11.9. `VECTOR(N) WITH (index = 'hnsw')` column.

**Implementation approach**:
- Column type `VECTOR(N)`; HNSW index via `coder/hnsw` or similar.
- `<->` operator for distance.
- Index replicates as substrate object.
- Hybrid retrieval via planner.
- Code path: `core/substrate/engines/sqlite/vector.go` (~2000 LOC).

**Acceptance criteria**:
- VECTOR type supported.
- HNSW index created/maintained.
- Distance queries efficient (~constant time).
- Hybrid keyword + vector queries work.

**Unit tests**:
- `TestVector_TypeSupported` — Type accepted.
- `TestVector_HNSWIndex` — Index created.
- `TestVector_DistanceQuery` — `<->` works.
- `TestVector_ReplicationCorrect` — Index replicates.

**Integration tests**:
- `TestVector_HybridRetrieval` — Keyword + vector combined.

**End-to-end tests**:
- `TestVectorE2E_KnowledgeGraphRetrieval` — Realistic retrieval
  workload.

**Race condition tests**:
- `TestVector_ConcurrentInsertSearch` — `go test -race`.

**Negative / non-happy path tests**:
- `TestVector_DimensionMismatch_Rejected` — Dim mismatch rejected.
- `TestVector_HNSWParameterChange_Rebuilt` — Param change requires
  rebuild.
- `TestVector_OutOfMemoryDuringIndexBuild_GracefulFallback` — OOM;
  fallback to brute-force search.

---

#### 27.11 — Federated SQL queries

**Description**: Per §11.9. Cross-cluster joins via `cluster('...').`
prefix.

**Implementation approach**:
- Parser extension for cluster prefix.
- Planner decomposes per-cluster sub-queries; ships predicates.
- Federation gateway routes; results joined at coordinator.
- Authority enforced cross-cluster.
- Code path: `core/substrate/engines/sqlite/federated.go`
  (~2500 LOC).

**Acceptance criteria**:
- Cluster prefix parsed.
- Planner generates federated plan.
- Sub-queries shipped to remote clusters.
- Joins at coordinator.
- Authority predicates verify cross-cluster.

**Unit tests**:
- `TestFederatedSQL_Parse` — Prefix parsed.
- `TestFederatedSQL_PlanGenerated` — Plan correct.
- `TestFederatedSQL_PredicatePushdown` — Predicates ship.
- `TestFederatedSQL_AuthorityEnforced` — Auth checked.

**Integration tests**:
- `TestFederatedSQL_TwoClusterJoin` — Real two-cluster join.

**End-to-end tests**:
- `TestFederatedSQLE2E_GlobalReport` — Global cross-cluster
  reporting query.

**Race condition tests**:
- `TestFederatedSQL_ConcurrentFederatedQueries` — `go test -race`.

**Negative / non-happy path tests**:
- `TestFederatedSQL_RemoteClusterUnreachable_PartialResultOrAbort`
  — Configurable.
- `TestFederatedSQL_AuthorityRejected_Aborted` — Auth fails;
  abort.
- `TestFederatedSQL_SchemaMismatchAcrossClusters_Detected` —
  Mismatch detected.

---

#### 27.12 — Topology-aware cost optimizer

**Description**: Per §11.9. Plan cost includes network distance,
tier, congestion, energy, quota.

**Implementation approach**:
- Cost model extended with topology factors.
- Per-cost component pulled from substrate (Vivaldi sections,
  §28.3 anomaly feed for congestion, §31.10 for energy, §22.1
  for quota).
- Plan considers move-compute-to-data when cheap.
- Code path: `core/substrate/engines/sqlite/topology_cost.go`
  (~1500 LOC).

**Acceptance criteria**:
- Cost model integrates topology.
- Plan moves computation appropriately.
- Cost reflects energy / quota / congestion.

**Unit tests**:
- `TestTopologyCost_VivaldiUsed` — Distance influences cost.
- `TestTopologyCost_TierUsed` — Tier influences cost.
- `TestTopologyCost_EnergyUsed` — Energy factor included.
- `TestTopologyCost_QuotaInfluencesPlan` — Quota considered.

**Integration tests**:
- `TestTopologyCost_PlanChangesWithTopology` — Topology change;
  plan adapts.

**End-to-end tests**:
- `TestTopologyCostE2E_RealisticWorkload` — Real workload; plans
  measurably better.

**Race condition tests**:
- `TestTopologyCost_ConcurrentPlanning` — `go test -race`.

**Negative / non-happy path tests**:
- `TestTopologyCost_StaleTopologyData_BoundedStaleness` — Stale
  data; bounded; refreshed.
- `TestTopologyCost_PathologicalCostFunction_FallsBackToDefault` —
  Cost function broken; fallback.

---

#### 27.13 — Multi-engine atomic transactions

**Description**: Per §11.9. Distributed atomicity across SQLite + KV
+ object store via §6.4 MultiNamespaceTx.

**Implementation approach**:
- BEGIN/COMMIT block can include multi-engine ops.
- Compiles to MultiNamespaceTx (§6.4) plan.
- Each engine's apply native Go (no WASM).
- Deterministic via §24.1 harness.
- Code path: `core/substrate/engines/multi_engine_tx.go`
  (~2000 LOC).

**Acceptance criteria**:
- BEGIN supports cross-engine ops.
- 2PC atomic.
- Each engine apply deterministic.
- Replicas converge to bit-equal state.

**Unit tests**:
- `TestMultiEngineTx_AcrossSQLiteKV` — SQLite + KV.
- `TestMultiEngineTx_AcrossSQLiteObject` — SQLite + object.
- `TestMultiEngineTx_AllThree` — SQLite + KV + object.
- `TestMultiEngineTx_AtomicCommit` — All commit or none.
- `TestMultiEngineTx_AtomicAbort` — All abort.

**Integration tests**:
- `TestMultiEngineTx_DeterministicReplay` — Replay produces
  same state.

**End-to-end tests**:
- `TestMultiEngineTxE2E_RealisticWorkflow` — Order placement
  across engines.

**Race condition tests**:
- `TestMultiEngineTx_ConcurrentTxns` — `go test -race`.

**Negative / non-happy path tests**:
- `TestMultiEngineTx_OneEngineFailsCommit_AllAbort` — Failure
  → abort.
- `TestMultiEngineTx_CoordinatorCrashRecovery` — Crash recovery.
- `TestMultiEngineTx_EngineVersionMismatch_Rejected` — Mismatch
  rejected.

---

#### 27.14 — Probabilistic SQL columns

**Description**: Per §11.9. `HLL`, `CMS`, `TDIGEST`, `BLOOM` types.

**Implementation approach**:
- New column types; backed by §26.6 sketches.
- SQL functions: `HLL.add`, `HLL.cardinality`, etc.
- Cross-DC merge via associative ops.
- Code path: `core/substrate/engines/sqlite/probabilistic.go`
  (~1200 LOC).

**Acceptance criteria**:
- Types accepted: HLL, CMS, TDIGEST, BLOOM.
- Functions work.
- Cross-DC merge correct.
- Bounded memory regardless of cardinality.

**Unit tests**:
- `TestProbSQL_HLLDistinct` — HLL accuracy.
- `TestProbSQL_CMSFreq` — CMS accuracy.
- `TestProbSQL_TDigestPercentile` — Percentile accuracy.
- `TestProbSQL_BloomMembership` — Bloom accuracy.
- `TestProbSQL_CrossDCMerge` — Merge correct.

**Integration tests**:
- `TestProbSQL_RealAnalyticsWorkload` — Analytics workload.

**End-to-end tests**:
- `TestProbSQLE2E_LargeCardinality` — Realistic cardinality;
  storage 1000x reduction.

**Race condition tests**:
- `TestProbSQL_ConcurrentAdd` — `go test -race`.

**Negative / non-happy path tests**:
- `TestProbSQL_AccuracyDegradesGracefully` — Beyond expected
  cardinality; degrades; no crash.
- `TestProbSQL_ParameterMigration_Rebuilt` — Parameter change;
  rebuilt.
- `TestProbSQL_VersionMismatchedMerge_Rejected` — Version
  mismatch.

---

#### 27.15 — Time-series + SQL

**Description**: Per §11.9. `WITH (kind = 'time-series')` triggers
Gorilla compression + tag indexing.

**Implementation approach**:
- DDL flag triggers time-series storage.
- Gorilla compression for floats; delta-of-delta for timestamps.
- Tag-based indexing.
- Standard SQL surface.
- Code path: `core/substrate/engines/sqlite/timeseries.go`
  (~2000 LOC).

**Acceptance criteria**:
- DDL flag accepted.
- Gorilla compression on floats; ratio meets target.
- Tag index works.
- Standard SQL queries work.

**Unit tests**:
- `TestTimeseries_GorillaCompression` — Ratio meets target.
- `TestTimeseries_TagIndex` — Tag queries fast.
- `TestTimeseries_StandardSQL` — Standard SELECT works.

**Integration tests**:
- `TestTimeseries_RealMetricsWorkload` — Real metrics; storage
  measurably reduced.

**End-to-end tests**:
- `TestTimeseriesE2E_PrometheusReplacement` — Prometheus-style
  workload.

**Race condition tests**:
- `TestTimeseries_ConcurrentInsert` — `go test -race`.

**Negative / non-happy path tests**:
- `TestTimeseries_NonNumericValueRejected` — Non-numeric in float
  column rejected.
- `TestTimeseries_OutOfOrderTimestamp_Handled` — Out-of-order
  handled.
- `TestTimeseries_TagCardinalityExplosion_Bounded` — Tag
  cardinality bounded.

---

#### 27.16 — Privacy / compliance via SQL

**Description**: Per §11.9. SQL syntax for redaction, residency, DP,
ZK proofs.

**Implementation approach**:
- DDL/SQL extensions for each capability.
- Wires to underlying substrate primitives (§31.23, §31.19, DP,
  §31.2 ZK).
- Per-tenant DP budget tracked.
- Code path: `core/substrate/engines/sqlite/privacy_sql.go`
  (~2500 LOC).

**Acceptance criteria**:
- `ALTER TABLE ... REDACT FIELD ...` works (§31.23).
- `WITH (residency = '...')` works (§31.19).
- `WITH (differential_privacy = epsilon=...)` works.
- `WITH (proof = true)` returns ZK proof (§31.2).
- DP budget tracked per tenant.

**Unit tests**:
- `TestPrivacySQL_RedactFieldDestroyKey` — Field key destroyed.
- `TestPrivacySQL_ResidencyEnforced` — Residency respected.
- `TestPrivacySQL_DPNoiseAdded` — Noise correct.
- `TestPrivacySQL_ZKProofGenerated` — Proof verifiable.
- `TestPrivacySQL_DPBudgetTracked` — Budget tracked.

**Integration tests**:
- `TestPrivacySQL_GDPRFlow` — GDPR right-to-erasure end-to-end.
- `TestPrivacySQL_TenantDPBudgetExhausted` — Exhausted; queries
  fail until reset.

**End-to-end tests**:
- `TestPrivacySQLE2E_TenantAuditScenario` — Tenant audits via
  ZK proofs.

**Race condition tests**:
- `TestPrivacySQL_ConcurrentRedactReadRace` — `go test -race`.

**Negative / non-happy path tests**:
- `TestPrivacySQL_ReadRedactedField_ReturnsNullOrError` — Per
  policy.
- `TestPrivacySQL_DPEpsilonNegative_Rejected` — Bad epsilon.
- `TestPrivacySQL_ResidencyViolatingInsert_Rejected` — Insert
  violating fence rejected.
- `TestPrivacySQL_ZKProofTampered_Rejected` — Tampered proof.

---

#### 27.17 — Online schema changes

**Description**: Per §11.9. ALTER TABLE without rewrite via §24.2 SM
versioning + §31.17 transforms.

**Implementation approach**:
- ALTER TABLE bumps SM version.
- Old SM applies old schema; new SM applies new.
- Backward-compat reads via transform registry (§31.17).
- Migrations background; eventually compaction rewrites.
- Code path: `core/substrate/engines/sqlite/online_alter.go`
  (~1800 LOC).

**Acceptance criteria**:
- ADD COLUMN free.
- DROP COLUMN metadata-only.
- ALTER TYPE requires registered transform.
- No table lock during ALTER.
- Old reads still work.

**Unit tests**:
- `TestOnlineAlter_AddColumnFree` — No rewrite.
- `TestOnlineAlter_DropColumnMetadata` — Metadata-only.
- `TestOnlineAlter_AlterTypeWithTransform` — Transform applied.
- `TestOnlineAlter_NoLock` — No table lock.

**Integration tests**:
- `TestOnlineAlter_LiveTrafficUnaffected` — Live traffic during
  ALTER.

**End-to-end tests**:
- `TestOnlineAlterE2E_RealMigration` — Production-style migration.

**Race condition tests**:
- `TestOnlineAlter_AlterDuringWrites` — `go test -race`.

**Negative / non-happy path tests**:
- `TestOnlineAlter_ChangeRequiringTransformWithoutRegistered_Refused`
  — Refused.
- `TestOnlineAlter_AlterFailsHalfway_Rolled Back` — Rollback.
- `TestOnlineAlter_BackwardCompatBroken_Detected` — Breaking
  change detected; refused or warned.

---

#### 27.18 — Self-tuning indexes

**Description**: Per §11.9. Optimizer (§31.25) proposes indexes
based on query patterns.

**Implementation approach**:
- §28.3 anomaly feed → query pattern observer.
- Optimizer proposes new index; operator approves; substrate
  creates online (no lock).
- Auto-approve policy configurable.
- Code path: `core/substrate/engines/sqlite/auto_index.go`
  (~1200 LOC, on top of §31.25).

**Acceptance criteria**:
- Query patterns observed.
- Index proposals generated.
- Operator approval flow.
- Online creation.
- Per-tenant policy.

**Unit tests**:
- `TestAutoIndex_ProposalGeneration` — Proposals generated.
- `TestAutoIndex_OperatorApprovalRequired` — Without auto-approve,
  manual.
- `TestAutoIndex_AutoApproveLowCost` — Low-cost auto-approved.
- `TestAutoIndex_OnlineCreation` — No lock.

**Integration tests**:
- `TestAutoIndex_RealQueryPatternImproved` — Real workload;
  improvement measurable.

**End-to-end tests**:
- `TestAutoIndexE2E_LongRunning` — Long-running; optimizer
  settles.

**Race condition tests**:
- `TestAutoIndex_ProposalDuringWrites` — `go test -race`.

**Negative / non-happy path tests**:
- `TestAutoIndex_ProposalRejected_NoChange` — Reject; no change.
- `TestAutoIndex_BadProposalCausesRegression_Reverted` —
  Regression; revert.
- `TestAutoIndex_DiskBudgetExceeded_ProposalsThrottled` — Budget
  hit; throttled.

---

#### 27.19 — Per-row TTL

**Description**: Per §11.9. `WITH (ttl_column = '...', ttl_interval
= '...', on_expire = 'redact'|'delete')`.

**Implementation approach**:
- DDL parses TTL clause.
- Background scanner: rows past TTL → redact (§31.23) or delete.
- Operator-configurable cadence.
- Code path: `core/substrate/engines/sqlite/ttl.go` (~800 LOC).

**Acceptance criteria**:
- DDL accepted.
- Scanner runs at configured cadence.
- Redact / delete actions correct.
- Bounded scanner cost.

**Unit tests**:
- `TestTTL_DDLAccepted` — Parsed.
- `TestTTL_RowExpiresRedact` — Redacted.
- `TestTTL_RowExpiresDelete` — Deleted.
- `TestTTL_ScannerCadence` — Cadence respected.

**Integration tests**:
- `TestTTL_LargeTableScanBounded` — Bounded cost.

**End-to-end tests**:
- `TestTTLE2E_SessionsTable` — Session expiry workflow.

**Race condition tests**:
- `TestTTL_ScannerDuringWrites` — `go test -race`.

**Negative / non-happy path tests**:
- `TestTTL_ColumnDoesntExist_Rejected` — Bad column.
- `TestTTL_IntervalNegative_Rejected` — Bad interval.
- `TestTTL_ScanFailsHalfway_RetriedNoCorruption` — Partial fail
  retried.

---

#### 27.20 — Per-row provenance via SQL

**Description**: Per §11.9. `provenance(rowid)` SQL function.

**Implementation approach**:
- New SQL function `provenance(rowid)`; returns
  `(svid, hlc, merkle_path)` JSONB.
- Function reads from §31.9 provenance certificate associated with
  row's HLC.
- Read-only; cheap.
- Code path: `core/substrate/engines/sqlite/provenance_fn.go`
  (~600 LOC).

**Acceptance criteria**:
- Function callable from any SELECT.
- Returns correct provenance.
- Cost ≤ 1ms.
- Available cluster-wide.

**Unit tests**:
- `TestProvenanceFn_Callable` — Function works.
- `TestProvenanceFn_CorrectData` — Data matches.
- `TestProvenanceFn_CostBounded` — ≤ 1ms.

**Integration tests**:
- `TestProvenanceFn_AuditQuery` — Realistic audit query.

**End-to-end tests**:
- `TestProvenanceFnE2E_TenantAudit` — Tenant audit via SQL.

**Race condition tests**:
- `TestProvenanceFn_ConcurrentCalls` — `go test -race`.

**Negative / non-happy path tests**:
- `TestProvenanceFn_RowidNotExist_ReturnsNull` — Null for
  non-existent.
- `TestProvenanceFn_ProvenancePastRetention_ReturnsTruncated` —
  Past retention; truncated provenance returned.
- `TestProvenanceFn_TamperedProvenance_DetectedAtVerify` —
  Tampered; verification fails on caller side.

---

### Phase 28 — Architectural Refinement (closing the substrate-design micro-gaps)

Cross-cluster, cross-engine, multi-version, lifecycle-edge cases that
need explicit primitives. Per §3.4, §6.6, §6.7, §7.6, §20.7, §20.8,
§22.7, §24.7, §27.8, §31.24-extension.

**Phase implementation overview**: Phase 28 closes ten substrate-
design micro-gaps surfaced after the broader architecture stabilized.
Each item lands as a self-contained primitive but composes with the
existing Layers 0-9 + extensions §17-§27. None of these block the
phase 0-14 critical path; each can ship in any order subject to its
local prerequisites. Common dependencies: existing primitives
(crypto, Raft, BFT, federation, KMS), formal-methods toolchain (Lean
4 / Coq) for the soundness theorems where applicable.

#### 28.1 — Live SVID rotation under active workload

**Description**: Per §3.4. Cross-signed transition window; frame-pinned
verification; connection lifetime > SVID lifetime; forward-secret
key destruction at window close.

**Implementation approach**:
- Cross-sign window default 30 min, configurable down to 5 min.
- Verifier maintains LRU cache of `(svid_pubkey_hash → expiry_hlc)`
  with bounded capacity (10K entries).
- `sylk://global/svid-history/v1` content-addressed; entries persist
  for audit retention horizon.
- `sylk://global/svid-rotations/v1` records each rotation event.
- `sylk://global/svid-revocations/v1` distinct from rotations;
  revocation overrides cross-sign trust within the window.
- Old SVID private key destruction is verified via post-destruction
  read (must fail) before logging completion.
- Code path: `core/substrate/identity/rotation/` (~1200 LOC).

**Acceptance criteria**:
- In-flight publishes during rotation complete without error.
- Existing QUIC connections survive rotation without re-handshake.
- New connections post-rotation use new SVID.
- Capabilities preserved through rotation (identity URI stable).
- Forward-secret destruction at window close (verified).
- Revocation overrides cross-sign within window.
- Anomaly alert on out-of-schedule rotation.
- Verification cost unchanged in steady-state (LRU cache hit).
- Rotation fully audited.

**Unit tests**:
- `TestSVIDRotation_CrossSignWindowVerifies` — Both old & new accepted.
- `TestSVIDRotation_FramePinnedToSignTime` — Frame from old period
  verifies via predecessor pubkey.
- `TestSVIDRotation_LRUCacheBounded` — Cache size bounded.
- `TestSVIDRotation_ForwardSecretDestruction` — Old key zeroed +
  verification read fails.
- `TestSVIDRotation_RevocationOverridesCrossSign` — Revoked SVID
  rejected even mid-window.
- `TestSVIDRotation_CapabilityContinuity` — Caps preserved.
- `TestSVIDRotation_HistoryEntryRecorded` — Rotation logged.

**Integration tests**:
- `TestSVIDRotation_InFlightPublishCompletes` — Real publish across
  rotation; succeeds.
- `TestSVIDRotation_QUICConnectionSurvives` — Long-lived connection
  unaffected.
- `TestSVIDRotation_NewConnectionUsesNewSVID` — New handshake.
- `TestSVIDRotation_BatchedSchnorrAcrossRotation` — §25.10 batches
  verify against historical SVID.

**End-to-end tests**:
- `TestSVIDRotationE2E_RealCluster` — Cluster-wide rotation; no
  observable disruption.
- `TestSVIDRotationE2E_SecurityIncidentRevocation` — Compromise
  detected mid-rotation; revocation propagates within budget.

**Race condition tests**:
- `TestSVIDRotation_ConcurrentVerificationDuringRotation` — `go test
  -race` parallel verifies; LRU consistent.
- `TestSVIDRotation_KeyDestructionRaceWithVerify` — Late-arriving
  frame after destruction; either verifies via cache or fails clearly.
- `TestSVIDRotation_RevocationDuringActiveVerify` — Revocation race;
  verify result deterministic.

**Negative / non-happy path tests**:
- `TestSVIDRotation_RevokedSVIDNotAccepted` — Revoked rejected.
- `TestSVIDRotation_ExpiredSVIDPostWindow_Rejected` — Past window,
  old fails.
- `TestSVIDRotation_KMSDestructionFails_RotationStallsAndAlerts` —
  Key destruction failure → operator alert; stalls.
- `TestSVIDRotation_OutOfScheduleRotation_AnomalyAlert` — Unsigned
  rotation alerts.
- `TestSVIDRotation_HistoryGapDetected_AuditFails` — Gap in
  rotation history detected.
- `TestSVIDRotation_PostHorizonFrameSig_VerifiesViaSnapshot` —
  Post-§21.5 horizon, signature verifies via snapshot root.

---

#### 28.2 — MultiNamespaceTx abort cleanup, recovery, and telemetry

**Description**: Per §6.6. Explicit staging keyspace per engine;
deterministic recovery from coordinator failure; full causal-cone
observability; idempotent re-issue.

**Implementation approach**:
- Per-engine staging keyspace: `staging/<tx_id>/...` namespaced.
- Coordinator's Raft log carries tx state machine: PREPARING,
  PREPARED, COMMITTED, ABORTED.
- Recovery loop: participant polls coordinator's tx-log subject
  every 1s when stuck in PREPARED.
- Quorum-based decision after `participant_recovery_timeout` (60s
  default): poll peer participants, presume-abort default,
  presume-commit only with operator-policy enable.
- `sylk://global/multitx-audit/v1` records every commit / abort /
  recovery decision with full context.
- Per-tx OTel span (§28.1) covers BEGIN, PREPARE per-participant,
  COMMIT, ABORT, recovery.
- `tx_id = BLAKE3(coordinator_id || sequence_in_session ||
  logical_op_hash)`.
- Code path: `core/substrate/consensus/multitx/` (~3000 LOC).

**Acceptance criteria**:
- Per-engine staging isolates PREPARE state from live state.
- Coordinator failure recoverable within bounded time.
- Idempotent re-issue: same logical op → same tx_id → cached result.
- Telemetry covers all paths: commit / abort / recovery.
- Compensation hooks runnable on ABORT for visible side-effects.
- Soundness invariants from §6.6 verified by property tests.

**Unit tests**:
- `TestMultiTx_PerEngineStaging` — Engines isolate staging.
- `TestMultiTx_PrepareOKThenCommit` — Happy path.
- `TestMultiTx_PrepareFailAbort` — One participant fails.
- `TestMultiTx_TimeoutAtParticipant` — Participant-decided abort.
- `TestMultiTx_TXIDIdempotency` — Same logical op → same tx_id.
- `TestMultiTx_CachedResultOnRetry` — Already-committed returns
  cached result.
- `TestMultiTx_CompensationHookRuns` — Compensation invoked on abort.

**Integration tests**:
- `TestMultiTx_CrossNamespaceFullLifecycle` — End-to-end.
- `TestMultiTx_CoordinatorRecoveryLoop` — Participants poll;
  decision applied.
- `TestMultiTx_QuorumPresumeAbort` — Coordinator unreachable;
  default presume-abort.
- `TestMultiTx_QuorumPresumeCommit_PolicyEnabled` — Policy override.
- `TestMultiTx_TelemetryCompleteness` — All metrics present.

**End-to-end tests**:
- `TestMultiTxE2E_RealClusterWithRecovery` — Coordinator killed
  mid-PREPARE; cluster recovers.
- `TestMultiTxE2E_HighConcurrencyAbortRate` — Realistic high-conflict
  workload.

**Race condition tests**:
- `TestMultiTx_ConcurrentCommitAbort` — `go test -race` parallel
  decisions; consistent.
- `TestMultiTx_RecoveryRaceWithCoordinatorRecovery` — Recovery
  while coordinator returns; deterministic.
- `TestMultiTx_StagingCleanupRaceWithRetry` — Cleanup during retry;
  consistent.

**Negative / non-happy path tests**:
- `TestMultiTx_OrphanedStaging_GCAfterTimeout` — Orphan reaped.
- `TestMultiTx_MalformedPrepareResponse_TreatedAsFailure` — Safe.
- `TestMultiTx_ParticipantDoubleAbort_Idempotent` — Idempotent.
- `TestMultiTx_CompensationHookFails_LoggedNotPanic` — Safe.
- `TestMultiTx_ForceAbortByOperator` — Operator-issued abort works.
- `TestMultiTx_CoordinatorPermanentLoss_DataLossDocumented` —
  Documented edge case.
- `TestMultiTx_StagingKeyspaceCorruption_DetectedAndQuarantined` —
  Detection.

---

#### 28.3 — Cross-substrate transactional semantics (cross-namespace + cross-engine)

**Description**: Per §6.7. Two-level 2PC; engine-specific staging;
HLC-deterministic apply; nested transactions / savepoints.

**Implementation approach**:
- Transaction graph compiler: parses transaction definition →
  graph; chooses Level 1 coordinator deterministically; assigns
  Level 2 coordinator per namespace.
- Engine adapters: `core/substrate/engines/<engine>/staging.go` for
  each engine (sqlite, kv, object, crdt).
- Savepoint support: nested staging keyspaces.
- Cross-cluster opt-in via subject schema flag.
- Saga decomposition triggered when participant count > configured
  cap.
- Code path: `core/substrate/consensus/multitx/two_level/` (~4000
  LOC) + per-engine staging adapters (~600 LOC each).

**Acceptance criteria**:
- Two-level 2PC works for any combination of engines + namespaces
  (within participant cap).
- Nested transactions / savepoints supported.
- HLC-deterministic apply across replicas.
- Cross-cluster path opt-in; defaults off.
- Saga decomposition automatic above cap.
- Performance: 2 RTT for typical multi-engine multi-namespace.

**Unit tests**:
- `TestCrossSubstrate_TransactionGraphCompiler`.
- `TestCrossSubstrate_DeterministicCoordinatorChoice`.
- `TestCrossSubstrate_EngineSpecificStaging` (per engine).
- `TestCrossSubstrate_SavepointCreation`.
- `TestCrossSubstrate_SavepointRollback`.
- `TestCrossSubstrate_NestedTransactionCommit`.
- `TestCrossSubstrate_NestedTransactionAbort`.
- `TestCrossSubstrate_ParticipantCountCapEnforced`.
- `TestCrossSubstrate_SagaDecompositionAboveCap`.

**Integration tests**:
- `TestCrossSubstrate_FullTwoLevelHappyPath`.
- `TestCrossSubstrate_HLCDeterministicAcrossReplicas`.
- `TestCrossSubstrate_SQLiteKVObjectAtomic`.
- `TestCrossSubstrate_CRDTNonCRDTMix` — CRDT subject + KV in same
  tx.
- `TestCrossSubstrate_CrossClusterOptIn`.

**End-to-end tests**:
- `TestCrossSubstrateE2E_RealisticMultiEngineWorkload`.
- `TestCrossSubstrateE2E_LongRunningConsistency` — 24h workload;
  no anomalies.

**Race condition tests**:
- `TestCrossSubstrate_ConcurrentTransactions` — `go test -race`.
- `TestCrossSubstrate_NestedSavepointConcurrency`.
- `TestCrossSubstrate_CrossEngineCommitOrderRace` — Apply order
  deterministic under concurrent commits.

**Negative / non-happy path tests**:
- `TestCrossSubstrate_OneEngineFailsPrepare_AllAbort`.
- `TestCrossSubstrate_NestedTxnOuterFailsInner_AllRollback`.
- `TestCrossSubstrate_SavepointAfterCommit_Refused`.
- `TestCrossSubstrate_PartialEngineCrashDuringCommit_Recovered`.
- `TestCrossSubstrate_CrossClusterPartitionMidPrepare_Aborted`.
- `TestCrossSubstrate_OversizedTransactionGraph_Rejected`.
- `TestCrossSubstrate_DeterminismHarnessValidation` — §24.1 catches
  any non-determinism.

---

#### 28.4 — Subject deletion semantics

**Description**: Per §7.6. Three-level deletion (retention expiry,
soft, hard); cascade protocol; verifiable destruction proof; audit
chain integrity.

**Implementation approach**:
- New subject `sylk://global/subject-lifecycle/v1` records
  soft-delete / hard-destroy / reactivate events.
- Per-subject DEK destruction: `KMS.ScheduleKeyDeletion(dek, 7d)`
  with M-of-N operator approval requirement.
- Cascade adapters per consumer kind: subscribers, backup, federation
  peers, persistence systems (Forest, KG, Doc DB, Bleve).
- Authority capabilities: `subject.delete` (soft),
  `subject.destroy` (hard), `subject.reactivate`.
- Code path: `core/substrate/lifecycle/deletion/` (~2000 LOC) +
  cascade adapters (~600 LOC each).

**Acceptance criteria**:
- Three deletion levels work as documented.
- Cascade reaches all subscribers within bounded time.
- Federation peer policies respected per pair.
- Hard delete produces verifiable destruction proof.
- Retention expiry, soft delete, hard delete distinguishable in
  audit.
- Reactivation works within retention window for soft-deleted.
- Audit chain integrity preserved across all three.

**Unit tests**:
- `TestSubjectDeletion_SoftDelete_NewPubsRefused`.
- `TestSubjectDeletion_SoftDelete_ReactivationWithinWindow`.
- `TestSubjectDeletion_HardDelete_DEKDestroyed`.
- `TestSubjectDeletion_HardDelete_DestructionProof`.
- `TestSubjectDeletion_HardDelete_MOfNRequired`.
- `TestSubjectDeletion_RetentionExpiry_SignedTombstone`.
- `TestSubjectDeletion_TombstoneInMerkleDAG`.
- `TestSubjectDeletion_AuthorityCapabilities`.

**Integration tests**:
- `TestSubjectDeletion_SubscriberCascade` — Subscribers receive
  deletion event.
- `TestSubjectDeletion_BackupCascade` — Backup metadata updated.
- `TestSubjectDeletion_FederationCascade_AcceptCascade` — Peer
  hard-deletes too.
- `TestSubjectDeletion_FederationCascade_RetainLocalPolicy` — Peer
  retains.
- `TestSubjectDeletion_PersistenceCascade_AllConsumers` — Forest, KG,
  Doc DB, Bleve all updated.

**End-to-end tests**:
- `TestSubjectDeletionE2E_GDPRRightToErasure` — Realistic erasure
  flow.
- `TestSubjectDeletionE2E_SoftThenHard` — Soft followed by hard.

**Race condition tests**:
- `TestSubjectDeletion_DeleteRaceWithInFlightPublish` — `go test
  -race`.
- `TestSubjectDeletion_CascadeRaceWithReactivation` — Reactivation
  race.
- `TestSubjectDeletion_FederationConflictDeleteVsRetain` — Race.

**Negative / non-happy path tests**:
- `TestSubjectDeletion_HardDeleteWithoutMOfN_Refused`.
- `TestSubjectDeletion_KMSDestructionFails_StallsWithAlert`.
- `TestSubjectDeletion_ReactivateAfterRetention_Refused`.
- `TestSubjectDeletion_ReactivateHardDestroyed_Refused`.
- `TestSubjectDeletion_DataReadable_AfterHardDelete_FailsClearly` —
  Data unrecoverable.
- `TestSubjectDeletion_PartialCascadeFailure_RetriedThenAlerted`.
- `TestSubjectDeletion_AuditChainBrokenByDeletion_Detected` —
  Detection.

---

#### 28.5 — Federation backpressure propagation

**Description**: Per §20.7. Hierarchical credit federation; ECN-style
marking; per-pair quota; class-specific behavior.

**Implementation approach**:
- Extend §4.6 piggyback frame format with `federation_credit` block.
- DCTCP-style ECN: `α := (1-g)·α + g·F` with `g = 0.0625`.
- Per-`(source, destination, class)` quota in BFT-replicated
  federation control plane (§20.1).
- Federation gateway as Raft group (or §17.2 BFT) with replicated
  credit state.
- Code path: `core/substrate/federation/backpressure/` (~1800 LOC).

**Acceptance criteria**:
- Credit advertisement piggybacks; no new round-trip protocol.
- ECN marking + DCTCP-style adaptation.
- Per-pair quota enforced.
- Class-specific behavior at federation boundary.
- Backpressure propagation latency ≤ 250ms typical.
- Critical class never dropped at federation boundary.

**Unit tests**:
- `TestFederationBackpressure_CreditPiggyback`.
- `TestFederationBackpressure_DCTCPECNAdaptation`.
- `TestFederationBackpressure_PerPairQuota`.
- `TestFederationBackpressure_ClassSpecificBehavior` (4 classes).
- `TestFederationBackpressure_LatencyBounded`.
- `TestFederationBackpressure_GatewayReplicatedCreditState`.

**Integration tests**:
- `TestFederationBackpressure_TwoClusterCascade`.
- `TestFederationBackpressure_LoadShedHierarchy` — Background drops
  first.
- `TestFederationBackpressure_GatewayCrashRecovery` — State
  preserved.

**End-to-end tests**:
- `TestFederationBackpressureE2E_RealCrossDCLoad`.
- `TestFederationBackpressureE2E_OverloadGracefulDegrade`.

**Race condition tests**:
- `TestFederationBackpressure_ConcurrentCreditUpdates` — `go test
  -race`.
- `TestFederationBackpressure_ECNMarkingRaceWithSend` — Race.
- `TestFederationBackpressure_QuotaBorrowRace` — Cross-DC borrow.

**Negative / non-happy path tests**:
- `TestFederationBackpressure_PeerUnreachable_LocalEnforcementStands`.
- `TestFederationBackpressure_QuotaExhaustedReturnsError`.
- `TestFederationBackpressure_CriticalNeverDropped`.
- `TestFederationBackpressure_BackgroundFloodRateLimited`.
- `TestFederationBackpressure_StaleCreditAdvert_Bounded`.
- `TestFederationBackpressure_NoStarvationAcrossPeers` (property
  test).

---

#### 28.6 — Learner-replica freshness guarantees

**Description**: Per §20.8. Per-learner advertised lag; read API
classification; ReadYourWrites tokens; lag-bounded exclusion.

**Implementation approach**:
- Lag advert via §4.6 piggyback every 100ms.
- Read routing layer with selection algorithm per §20.8.
- WriteToken returned from every cross-cluster write (§31.24
  extension).
- Lag-bounded exclusion via local dispatcher; reinclusion after
  sustained recovery.
- Code path: `core/substrate/delivery/learner_freshness/` (~1500
  LOC).

**Acceptance criteria**:
- Per-class default isolation respected.
- Linearizable from learner via read-index.
- Monotonic, ReadYourWrites, BoundedStaleness all functional.
- Excluded learner reinclusion after sustained recovery.
- ErrStale on no-qualifying-learner.

**Unit tests**:
- `TestLearnerFreshness_LagAdvertised` (every 100ms).
- `TestLearnerFreshness_LinearizableFromLearner_ReadIndex`.
- `TestLearnerFreshness_BoundedStalenessSelection`.
- `TestLearnerFreshness_MonotonicCookieAdvances`.
- `TestLearnerFreshness_ReadYourWritesEnforced`.
- `TestLearnerFreshness_LagExcludedBeyondBound`.
- `TestLearnerFreshness_ReinclusionAfterRecovery`.

**Integration tests**:
- `TestLearnerFreshness_HighReadFanout` — 1K subscribers via
  learners.
- `TestLearnerFreshness_LeaderUnchangedDespiteLearnerRouting`.
- `TestLearnerFreshness_MixedClassRouting` — Critical to leader,
  Bulk to learner.

**End-to-end tests**:
- `TestLearnerFreshnessE2E_RealCluster`.
- `TestLearnerFreshnessE2E_PerformanceBudget` — Throughput improvement
  from learner offload.

**Race condition tests**:
- `TestLearnerFreshness_ConcurrentReadRouting` — `go test -race`.
- `TestLearnerFreshness_LagAdvertRaceWithRoutingDecision` — Race.
- `TestLearnerFreshness_ExclusionRaceWithRecovery` — Race.

**Negative / non-happy path tests**:
- `TestLearnerFreshness_AllLearnersExcluded_ErrStale`.
- `TestLearnerFreshness_NoQualifyingLearner_ErrStale`.
- `TestLearnerFreshness_FrontierNotReachedTimeout`.
- `TestLearnerFreshness_StaleAdvertIgnoredInSelection`.
- `TestLearnerFreshness_LearnerCrash_ExcludedThenReinclusion`.
- `TestLearnerFreshness_FreshnessViolationDetected_Alert`.

---

#### 28.7 — Quota burst dynamics

**Description**: Per §22.7. Hierarchical token bucket + sliding-
window observation + earned-burst credits.

**Implementation approach**:
- Per-`(tenant, class, dimension)` token bucket; CAS-based hot path.
- Hierarchical: tenant global → per-subject sub-buckets.
- Sliding-window 5s alongside bucket for billing accuracy.
- Earned burst credits: minute-of-burst per minute of <50%
  utilization, capped at 60 credits.
- Cross-DC borrow/lend via federation control plane.
- Code path: `core/substrate/tenancy/quota/burst/` (~1600 LOC).

**Acceptance criteria**:
- Hot-path quota check ~10ns.
- Hierarchical bucket inheritance correct.
- Earned-burst credits accumulate + consume correctly.
- Cross-DC borrow respects total tenant quota.
- Choking precedence: Background → Bulk → Standard → Critical mini.
- Critical mini-quota never raided.

**Unit tests**:
- `TestQuotaBurst_TokenBucketHotPathCost` — < 50ns.
- `TestQuotaBurst_HierarchicalInheritance`.
- `TestQuotaBurst_EarnedCreditAccumulation`.
- `TestQuotaBurst_EarnedCreditConsumption`.
- `TestQuotaBurst_SlidingWindowAccuracy`.
- `TestQuotaBurst_CrossDCBorrowCap`.
- `TestQuotaBurst_ChokePrecedence` (4 classes).
- `TestQuotaBurst_CriticalMiniNeverRaided`.

**Integration tests**:
- `TestQuotaBurst_RealisticBurstyWorkload`.
- `TestQuotaBurst_DistributedAccountingDriftBound` — < 1%.

**End-to-end tests**:
- `TestQuotaBurstE2E_LongRunningTenant`.
- `TestQuotaBurstE2E_AbusiveTenantContained`.

**Race condition tests**:
- `TestQuotaBurst_ConcurrentPublishesCAS` — `go test -race`.
- `TestQuotaBurst_BorrowLendRace` — Cross-DC.
- `TestQuotaBurst_BucketRefillRaceWithConsume`.

**Negative / non-happy path tests**:
- `TestQuotaBurst_OverConsumed_RetryAfterHeader`.
- `TestQuotaBurst_BurstCreditExpired_StandardRateOnly`.
- `TestQuotaBurst_DistributedReconciliationCorrects`.
- `TestQuotaBurst_TenantOffboarded_BucketsCleanedUp`.
- `TestQuotaBurst_PathologicalSpike_BoundedDegradation`.
- `TestQuotaBurst_NegativeQuotaConfig_RejectedAtRegister`.

---

#### 28.8 — Multi-version SM coexistence during long-running transactions

**Description**: Per §24.7. Transaction-version pinning; bounded
coexistence window; force-upgrade protocol; saga step-level
versioning.

**Implementation approach**:
- Add `(sm_name, sm_version, code_hash)` triple to transaction
  metadata at BEGIN.
- Replicas hold N versions concurrently (default 3); per-version
  apply path.
- `sylk://global/sm-active/v1` declares active version per SM.
- `multitx_pinned_version{name, version}` metric tracks in-flight.
- Force-abort protocol: operator publishes `tx.force_abort{tx_id,
  reason}`.
- Code path: `core/substrate/sm/version_pinning/` (~1500 LOC).

**Acceptance criteria**:
- Transaction-version pin recorded at BEGIN.
- Apply uses pinned version; replicas refuse if version unloaded.
- Coexistence window respects N concurrent versions.
- Force-upgrade rolls back v_old transactions atomically.
- Bounded transaction lifetime per class enforced.
- Saga step-level versioning works through multiple SM upgrades.

**Unit tests**:
- `TestSMVersionPin_PinAtBegin`.
- `TestSMVersionPin_ApplyUsesPin`.
- `TestSMVersionPin_ReplicaRefusesUnloadedVersion`.
- `TestSMVersionPin_NCoexistentVersions`.
- `TestSMVersionPin_ForceAbortRollback`.
- `TestSMVersionPin_TxnLifetimeEnforced`.
- `TestSMVersionPin_SagaStepLevelPin`.

**Integration tests**:
- `TestSMVersionPin_RollingUpgradeMidTransaction`.
- `TestSMVersionPin_LongSagaAcrossMultipleUpgrades`.
- `TestSMVersionPin_VersionCompatTableEnforced`.

**End-to-end tests**:
- `TestSMVersionPinE2E_RealUpgradeFlow`.
- `TestSMVersionPinE2E_ForceUpgradeIncident`.

**Race condition tests**:
- `TestSMVersionPin_ConcurrentTxnsAcrossVersions` — `go test -race`.
- `TestSMVersionPin_ForceAbortRaceWithCommit`.
- `TestSMVersionPin_VersionLoadRaceWithApply`.

**Negative / non-happy path tests**:
- `TestSMVersionPin_VersionMissing_PrepareFails`.
- `TestSMVersionPin_LifetimeExceeded_AutoAbort`.
- `TestSMVersionPin_VersionCompatViolation_TxnRejected`.
- `TestSMVersionPin_ForceAbortDuringActiveTxn_Rolled Back`.
- `TestSMVersionPin_OldKeyspaceGCRespectsActiveTxns`.
- `TestSMVersionPin_PathologicalVersionExplosion_Bounded`.

---

#### 28.9 — Cross-cluster read-after-write (concrete protocol)

**Description**: Per §31.24 extension. WriteToken; ObservedAfter
read API; FederationFrontier propagation; bounded-wait protocol.

**Implementation approach**:
- WriteToken struct in subject schemas; carried in publish response
  + read request headers.
- `sylk://federation/<id>/frontier/v1` BFT subject for cross-domain
  frontier; updates every 100ms.
- Local FederationFrontier cache per cluster.
- Wait protocol with configurable timeout; default 5s for
  Standard, 30s for Bulk.
- Token compression at horizon (§21.5).
- Soundness theorem proof in Lean.
- Code path: `core/substrate/federation/raw/` (~2200 LOC) + Lean
  theory.

**Acceptance criteria**:
- WriteToken returned from every cross-cluster write.
- ObservedAfter blocks until frontier covers token.
- FederationFrontier advertised within 100ms intervals.
- Token chain length bounded; horizon compaction works.
- Bounded-wait timeout deterministic.
- Soundness theorem machine-checked.

**Unit tests**:
- `TestRAW_WriteTokenReturned`.
- `TestRAW_ObservedAfterBlocks`.
- `TestRAW_FrontierAdvertCadence`.
- `TestRAW_TokenChainBounded`.
- `TestRAW_HorizonCompaction`.
- `TestRAW_BoundedWaitTimeout`.
- `TestRAW_SoundnessTheoremProofValidates`.

**Integration tests**:
- `TestRAW_TwoClusterRoundTrip`.
- `TestRAW_DeepChainPerformance` — chain depth 5; latency budget.
- `TestRAW_StaleFrontierTreatedAsUnknown`.

**End-to-end tests**:
- `TestRAWE2E_GlobalReadAfterWrite`.
- `TestRAWE2E_LongLivedSession_PreservesCausality`.

**Race condition tests**:
- `TestRAW_ConcurrentReadsWithDifferentTokens` — `go test -race`.
- `TestRAW_FrontierUpdateRaceWithRead`.
- `TestRAW_TokenChainGrowthRace`.

**Negative / non-happy path tests**:
- `TestRAW_TimeoutBeforeFrontierReached_ErrFrontierNotReached`.
- `TestRAW_DomainDownDuringWait_ErrFederationPartition`.
- `TestRAW_MalformedTokenRejected`.
- `TestRAW_TokenForUnknownDomain_Rejected`.
- `TestRAW_ChainDepthBeyondBound_HorizonCompacted`.
- `TestRAW_TokenForRevokedSVID_Rejected`.

---

#### 28.10 — Per-class CC parameter tuning policy

**Description**: Per §27.8. Three-tier regime: static defaults +
operator overrides + closed-loop optimizer with rollback gating.

**Implementation approach**:
- Tier 1 defaults shipped; reviewed quarterly.
- Tier 2 overrides via CRD (§25.1) + version-pinned (§24.2).
- Tier 3 optimizer subscribes to performance metrics (§28.3 anomaly
  feed); proposes via `sylk://global/cc-tuning-proposals/v1`.
- Operator approval workflow + canary rollout (§25.7).
- Closed-loop validation: paired statistical test on SLO metrics;
  significance `p < 0.01`; auto-rollback on regression.
- `sylk://global/cc-tuning-history/v1` audits all changes.
- Code path: `core/substrate/transport/cc_tuning/` (~2000 LOC).

**Acceptance criteria**:
- Tier 1 / 2 / 3 distinct + composable.
- Optimizer proposes only with statistical significance.
- Canary cohort 5%; 24h observation.
- Auto-rollback on regression.
- Per-link overrides for known-degraded peers.
- Audit trail complete.

**Unit tests**:
- `TestCCTuning_Tier1Defaults`.
- `TestCCTuning_Tier2Overrides`.
- `TestCCTuning_OptimizerTriggers` (4 conditions).
- `TestCCTuning_StatisticalSignificance`.
- `TestCCTuning_CanaryThenPromote`.
- `TestCCTuning_AutoRollback`.
- `TestCCTuning_PerLinkOverride`.
- `TestCCTuning_AuditEntry`.

**Integration tests**:
- `TestCCTuning_RealisticLatencyDivergenceTriggers`.
- `TestCCTuning_BufferBloatTriggers`.
- `TestCCTuning_LossRateTriggers`.
- `TestCCTuning_RolloutFlowEndToEnd`.

**End-to-end tests**:
- `TestCCTuningE2E_SustainedLatencyImprovement`.
- `TestCCTuningE2E_RegressionAutoRolledBack`.

**Race condition tests**:
- `TestCCTuning_ConcurrentProposals_Serialized` — `go test -race`.
- `TestCCTuning_RolloutRaceWithRollback`.

**Negative / non-happy path tests**:
- `TestCCTuning_UnknownParameter_Rejected`.
- `TestCCTuning_OperatorRejectionPath`.
- `TestCCTuning_ProposalWithoutSignificance_Skipped`.
- `TestCCTuning_RollbackPathTested_StagingOnly`.
- `TestCCTuning_PathologicalMetric_NoFlapping`.
- `TestCCTuning_TierConflict_TierPriorityRespected`.

---

### Phase 29 — Compositional Workflow + Algebraic Effects

Algebraic effect handlers, compositional workflow combinators,
workflow-as-substrate-subject, progressive context disclosure. Per
§26.7, §26.8, §26.9, §26.10. Borrowed from `../barnum`'s pattern;
adapted to Sylk's substrate.

**Phase implementation overview**: Phase 29 introduces a
*compositional workflow algebra* on top of the substrate. Each item
ships independently behind a feature flag; together they give Sylk
Barnum-grade workflow expressiveness on substrate-grade durability /
audit / federation. Common dependencies: §26.1 saga, §26.4 workflow
primitives, §31.4 differential dataflow, §31.29 substrate-as-IR,
§24.1 determinism harness.

#### 29.1 — Algebraic effect handler primitives (resume + restart)

**Description**: Per §26.7. Resume + restart effect handlers as
substrate primitives; tear-down + re-advance for restart, inline
handler + state mutation for resume.

**Implementation approach**:
- Two new substrate primitives in `core/substrate/primitives/effects/`:
  - `ResumeHandle` / `ResumePerform`
  - `RestartHandle` / `RestartPerform`
- Effect handler IDs typed at compile time (`ResumeHandlerId` ≠
  `RestartHandlerId`); cross-binding errors caught at codegen.
- Handle frame state lives in namespace's Raft state machine; resume
  state mutations are SM transitions.
- Restart tear-down implemented via frame-stack pop + cleanup.
- Lookup: effect performs target nearest-enclosing handle by walking
  the substrate-replicated frame stack; deterministic.
- Code path: `core/substrate/primitives/effects/` (~3000 LOC) +
  Phase 28 SM machinery.

**Acceptance criteria**:
- Resume + restart primitives implemented as substrate operations.
- Type-safe handler IDs; cross-binding caught at compile time.
- Frame state Raft-replicated; bit-equal across replicas (§24.1
  harness).
- Restart tears down body deterministically; re-advances from new
  input.
- Resume runs handler inline at perform site; delivers value to
  parent; persists new state.
- A Perform with no matching handle is a structural error caught at
  workflow registration.

**Unit tests**:
- `TestEffects_ResumeHandle_Inline` — Handler runs at perform site.
- `TestEffects_ResumeHandle_StateMutation` — State updated correctly.
- `TestEffects_RestartHandle_BodyTeardown` — Body torn down on perform.
- `TestEffects_RestartHandle_HandlerOutputBecomesInput` — Restart
  semantics.
- `TestEffects_TypedHandlerIDs` — Cross-binding caught at compile time.
- `TestEffects_NestedHandlersResolveByDistance` — Nearest enclosing
  wins.
- `TestEffects_FrameStackReplicated` — Bit-equal across replicas.
- `TestEffects_PerformWithoutHandle_StructuralError` — Caught at
  registration.

**Integration tests**:
- `TestEffects_ComposedTryCatchOverLoop` — `tryCatch(loop(body))`
  works.
- `TestEffects_DeepNesting` — 16-level nesting; correct dispatch.
- `TestEffects_RestartAcrossReplicas` — Restart propagates;
  consistent.

**End-to-end tests**:
- `TestEffectsE2E_RealisticTryCatchScenario`.
- `TestEffectsE2E_LoopWithEarlyReturn`.

**Race condition tests**:
- `TestEffects_ConcurrentPerforms` — `go test -race` parallel performs;
  serialized correctly.
- `TestEffects_HandlerStateRaceWithPerform` — State mutation race;
  consistent.
- `TestEffects_RestartDuringActivePerform` — Race resolved
  deterministically.

**Negative / non-happy path tests**:
- `TestEffects_PerformOutsideHandle_Refused` — No matching handle.
- `TestEffects_HandlerDivergesAcrossReplicas_FlaggedByHarness` —
  §24.1 catches.
- `TestEffects_RestartLoopUnbounded_BoundedViaCounter` — Loop bound
  enforced.
- `TestEffects_MalformedEffectID_Rejected` — Bad ID at compile.
- `TestEffects_PathologicalNesting_Bounded` — Depth limit.
- `TestEffects_HandlerCrashes_ErrorArtifact` — Crash → corrective
  action.

---

#### 29.2 — Compositional workflow combinators

**Description**: Per §26.8. `pipe / forEach / all / branch / loop /
tryCatch / withTimeout / race / withResource` as first-class
substrate workflow primitives. Plus the registry of pure-data
builtins.

**Implementation approach**:
- Combinators in `core/substrate/workflow/combinators/`.
- Each combinator emits substrate plan nodes (§31.29 IR):
  - `pipe(a, b)` → Chain plan node
  - `forEach(h)` → ForEach plan node (parallel claim publish)
  - `all(a, b, c)` → fanout plan node
  - `branch({...})` → discriminated dispatch plan node
  - `loop(...)` → restart-effect handle (uses §29.1)
  - `tryCatch(...)` → restart-effect handle with branch
  - `withTimeout` → restart-effect + substrate timer
  - `withResource` → setup-body-teardown with cleanup-on-restart
  - `race(...)` → fanout + first-completion-wins + cancel-others
- Builtin registry: native-Go pure transforms (no VM); see §26.8
  list. Codegen at workflow registration.
- Type system: `TypedAction<TIn, TOut>` Go generics; composition
  type-checks at registration.
- Code path: `core/substrate/workflow/combinators/` (~4000 LOC) +
  builtins (~1200 LOC).

**Acceptance criteria**:
- All listed combinators implemented.
- Type-safe composition (compile-time + registration-time).
- Built-in pure transforms in the registry; codegen'd to native Go.
- Workflows compile to substrate execution plans.
- Each handler step is a claim with progressive context disclosure
  (§29.4).
- Effects integrate cleanly: `loop` / `tryCatch` / `withTimeout` use
  §29.1 restart effects.

**Unit tests**:
- `TestCombinator_Pipe` — Sequential composition.
- `TestCombinator_ForEach_ParallelDispatch` — Parallel claims.
- `TestCombinator_All_Fanout` — Fanout + collect.
- `TestCombinator_Branch_Dispatch` — Conditional routing.
- `TestCombinator_Loop_Recursion` — Loop terminates.
- `TestCombinator_TryCatch_Recovery` — Error recovery.
- `TestCombinator_WithTimeout_Bounded` — Timeout fires.
- `TestCombinator_WithResource_CleanupOnFailure`.
- `TestCombinator_Race_FirstWins`.
- `TestCombinator_TypeSafeComposition` — Type errors caught.
- `TestCombinator_BuiltinRegistry_AllPresent`.

**Integration tests**:
- `TestCombinator_ComplexWorkflow` — Realistic deeply-composed flow.
- `TestCombinator_CompiledToSubstrateIR` — IR emission correct.
- `TestCombinator_DeterministicAcrossReplicas` — §24.1 validates.

**End-to-end tests**:
- `TestCombinatorE2E_BarnumLikeRefactorWorkflow` — Implements the
  refactor-with-retry workflow from Barnum's README; all features
  exercised.

**Race condition tests**:
- `TestCombinator_ConcurrentForEach` — `go test -race` parallel
  fanout; consistent.
- `TestCombinator_RaceCancellationCleanup`.
- `TestCombinator_TimeoutRaceWithCompletion`.

**Negative / non-happy path tests**:
- `TestCombinator_TypeMismatchAtRegistration_Rejected`.
- `TestCombinator_InfiniteLoopWithoutBaseCase_Detected` (max-iter cap).
- `TestCombinator_BranchOnUnknownKind_DefaultBranchOrError`.
- `TestCombinator_OversizedFanout_Bounded` — Fanout cap.
- `TestCombinator_BuiltinPanics_ErrorArtifact`.
- `TestCombinator_CycleInWorkflow_DetectedAtRegistration`.

---

#### 29.3 — Workflow-as-substrate-subject

**Description**: Per §26.9. Workflows themselves are first-class
substrate subjects: submit / execute / replay / diff / causal-cone /
cancel.

**Implementation approach**:
- New subject `sylk://session/<id>/workflow/v1` for workflow
  definitions.
- New subject `sylk://session/<id>/workflow-runs/v1` for execution
  traces.
- Workflow ID = BLAKE3 of canonical AST → content-addressed.
- Submission carries SVID signature; substrate verifies + stores.
- `Execute(workflow_id, input)` allocates a `run_id`; SM begins
  dispatching steps.
- Step dispatch / completion / failure are entries on workflow-runs
  subject.
- Replay: replay execution by re-running step dispatches in HLC
  order against current handler implementations.
- Diff: AST diff over canonical SWF-encoded ASTs.
- Cancel: §6.6 abort cleanup applied to in-flight workflow runs.
- Code path: `core/substrate/workflow/subjects/` (~2500 LOC).

**Acceptance criteria**:
- Workflow definitions stored as substrate subjects.
- Execution traces stored as run-subject entries.
- Content-addressed workflow IDs.
- Submission requires SVID signature; substrate verifies.
- Replay reproduces same output given same input + same handler
  versions.
- Diff over canonical AST representation.
- Cancel cleans up via §6.6 mechanism.
- Federation-replicable; cross-cluster workflow visibility supported.

**Unit tests**:
- `TestWorkflowSubject_SubmitContentAddressed` — Same AST → same ID.
- `TestWorkflowSubject_SignatureVerified`.
- `TestWorkflowSubject_RunIDAllocated`.
- `TestWorkflowSubject_StepEntriesEmitted`.
- `TestWorkflowSubject_Replay` — Re-run produces same trace.
- `TestWorkflowSubject_DiffAcrossVersions`.
- `TestWorkflowSubject_CancelMidRun` — Cleanup correct.

**Integration tests**:
- `TestWorkflowSubject_FullLifecycle` — Submit → execute → complete.
- `TestWorkflowSubject_CrossSessionPersistence`.
- `TestWorkflowSubject_FederationReplication`.

**End-to-end tests**:
- `TestWorkflowSubjectE2E_RealClusterWorkflow`.
- `TestWorkflowSubjectE2E_TimeTravelDebugSession` — Inspect
  historical state.
- `TestWorkflowSubjectE2E_SQLAnalytics` — `SELECT` over
  workflow-runs.

**Race condition tests**:
- `TestWorkflowSubject_ConcurrentSubmits_Idempotent` — Same AST,
  one ID.
- `TestWorkflowSubject_ConcurrentRunsDifferentIDs`.
- `TestWorkflowSubject_CancelDuringStepDispatch`.

**Negative / non-happy path tests**:
- `TestWorkflowSubject_TamperedASTSig_Rejected`.
- `TestWorkflowSubject_HandlerVersionMissingAtReplay_ErrorReturned`.
- `TestWorkflowSubject_OversizedAST_Rejected`.
- `TestWorkflowSubject_RunIDCollision_Detected`.
- `TestWorkflowSubject_DiffAcrossIncompatibleSchemas_Bounded`.
- `TestWorkflowSubject_OrphanedRuns_GCAfterRetention`.
- `TestWorkflowSubject_PathologicalDeepAST_Bounded`.

---

#### 29.4 — Progressive context disclosure (claims-level default)

**Description**: Per §26.10. Per-claim context defaults to *narrow*
(claim itself + validations + testament target only). Wider context
is opt-in per claim via `ClaimContextEnvelope`.

**Implementation approach**:
- New field on `Claim` struct: `ContextEnvelope ClaimContextEnvelope`.
- All envelope flags default to false / 0.
- Substrate authority predicate enforces: agent's read access for
  ambient state is bounded by claim's envelope declaration.
- Architect's claim-generation prompt updated to *not* request wide
  context unless task warrants.
- Existing claims migrate via opt-out: existing schemas declare
  current wide envelope explicitly; new claims default to narrow.
- Code path: claim type (~200 LOC), authority predicate
  enforcement (~400 LOC), agent context assembly path (~1500 LOC),
  architect prompt update.

**Acceptance criteria**:
- New claims default to narrow context (claim + validations only).
- Existing claims unchanged (envelope declared explicitly).
- Substrate enforces: out-of-envelope reads refused.
- Token-cost reduction measurable (typical 5x reduction on
  implementation claims).
- Quality preserved or improved (per LLM-context literature
  expectations + benchmarks).
- Audit: envelope is part of claim record; queryable.

**Unit tests**:
- `TestProgressive_NarrowDefault` — New claim has all-false envelope.
- `TestProgressive_OptInWideContext` — Architect can declare wider.
- `TestProgressive_AuthorityEnforced` — Out-of-envelope reads
  refused.
- `TestProgressive_ContextAssemblyBounded` — Agent receives only
  declared.
- `TestProgressive_LegacyClaimsCompatible` — Old wide-envelope
  preserved.

**Integration tests**:
- `TestProgressive_TokenCostReduction` — Measurably 5x.
- `TestProgressive_QualityPreservedOrImproved` — Quality benchmark.
- `TestProgressive_AuditTrailComplete`.

**End-to-end tests**:
- `TestProgressiveE2E_RealisticImplementationFlow`.
- `TestProgressiveE2E_ArchitectExplicitlyWiderForStrategy`.

**Race condition tests**:
- `TestProgressive_EnvelopeChangeRaceWithRead`.
- `TestProgressive_ConcurrentClaimsDifferentEnvelopes`.

**Negative / non-happy path tests**:
- `TestProgressive_OutOfScopeReadAttempt_Refused`.
- `TestProgressive_OversizedEnvelope_Bounded`.
- `TestProgressive_LegacyMigrationFlag` — Migration during cutover.
- `TestProgressive_AmbientLensNotDeclared_Refused`.
- `TestProgressive_QualityRegressionDetected_Alert` — Regression
  alert if narrowing degrades quality.

---

#### 29.5 — Compile workflow AST to substrate IR

**Description**: Workflows submitted via §29.3 compile to substrate
execution plans via §31.29 substrate-as-IR. Compile happens at
submission time; runtime is plan execution, not interpretation.

**Implementation approach**:
- Workflow compiler in `core/substrate/workflow/compile/`.
- Front end: parse the AST (Action enum from §26.7 / §26.8).
- Mid end: type check (TypedAction) + canonicalize.
- Back end: emit substrate IR nodes (§31.29):
  - `Action.Invoke` → claim-publish IR
  - `Action.Chain` → sequence IR
  - `Action.ForEach` → parallel-publish IR
  - `Action.RestartHandle` → restart-frame IR
  - `Action.ResumeHandle` → resume-frame IR
- Optimizer (§31.25) operates on IR before emission.
- Code path: `core/substrate/workflow/compile/` (~2500 LOC).

**Acceptance criteria**:
- AST compiles to substrate IR at submission.
- Compilation fails on type errors with actionable messages.
- Optimization passes (§31.25) operate on IR before emission.
- IR is canonical; same AST → same IR (deterministic).
- Compile time bounded.

**Unit tests**:
- `TestWorkflowCompile_AllNodeKinds` — Each AST node compiles.
- `TestWorkflowCompile_TypeChecking`.
- `TestWorkflowCompile_DeterministicIR`.
- `TestWorkflowCompile_OptimizationApplies`.
- `TestWorkflowCompile_CompileBounded`.

**Integration tests**:
- `TestWorkflowCompile_ComplexWorkflow`.
- `TestWorkflowCompile_OptimizerHotViewMaterialize` — §31.25 pass.

**End-to-end tests**:
- `TestWorkflowCompileE2E_RealisticWorkflow`.

**Race condition tests**:
- `TestWorkflowCompile_ConcurrentSubmissions`.

**Negative / non-happy path tests**:
- `TestWorkflowCompile_TypeError_ActionableMessage`.
- `TestWorkflowCompile_PathologicalAST_Bounded`.
- `TestWorkflowCompile_UnknownNodeKind_Rejected`.
- `TestWorkflowCompile_UnreachableHandler_DetectedAndRefused`.
- `TestWorkflowCompile_BackwardCompatLegacyAST` — Older AST format
  handled.

---

#### 29.6 — Effect-handler-based saga decomposition

**Description**: Existing saga primitive (§26.1) refactored to ride on
algebraic effects. Saga steps become effect-handled claims;
compensations become restart-effect responses.

**Implementation approach**:
- Saga as `RestartHandle` wrapping the step sequence.
- Step failure is a `RestartPerform` carrying error.
- Restart handler routes to compensation chain (reverse order of
  completed steps).
- Compensations are themselves effect-handled (nested).
- Migration: existing `core/substrate/primitives/saga/` updates to
  use effects internally; external API unchanged.
- Code path: refactor `core/substrate/primitives/saga/` (~1500 LOC
  modified).

**Acceptance criteria**:
- Saga primitive backed by algebraic effects.
- External API unchanged; existing tests pass.
- Compensation runs on step failure via restart effect.
- Nested sagas compose via nested effect handles.
- Recovery from coordinator failure preserved (per §6.6).

**Unit tests**:
- `TestSagaEffects_BackedByRestartHandle`.
- `TestSagaEffects_ExistingTestsPass`.
- `TestSagaEffects_CompensationOnFailure`.
- `TestSagaEffects_NestedComposes`.

**Integration tests**:
- `TestSagaEffects_RealClaimsWorkflow`.
- `TestSagaEffects_RecoveryFromCoordinatorCrash`.

**End-to-end tests**:
- `TestSagaEffectsE2E_AgentWorkflow`.

**Race condition tests**:
- `TestSagaEffects_ConcurrentSagasDifferentEffects`.

**Negative / non-happy path tests**:
- `TestSagaEffects_AllStepsFailCompensateAll`.
- `TestSagaEffects_CompensationFailsAlertOperator`.
- `TestSagaEffects_OrphanedSagaGC`.

---

#### 29.7 — Workflow analytics SQL surface

**Description**: Workflows + workflow runs queryable via §27 SQL.
Run analytics, regression detection, performance tracking.

**Implementation approach**:
- Workflow + workflow-runs subjects exposed as SQL tables (per
  §27.1 / §31.6).
- Standard SQL queries supported.
- Continuous queries (§27.9) for live dashboards.
- Code path: `core/substrate/workflow/sql/` (~600 LOC, mostly
  schema mapping).

**Acceptance criteria**:
- `SELECT * FROM substrate.workflows`,
  `... FROM substrate.workflow_runs` work.
- JOIN across workflow + workflow-runs supported.
- Continuous queries on workflow-runs (§27.9) for live dashboards.
- Time-travel: `SELECT ... AS OF HLC '...'`.

**Unit tests**:
- `TestWorkflowSQL_BasicSelect`.
- `TestWorkflowSQL_JoinAcrossSubjects`.
- `TestWorkflowSQL_AsOfHLC`.

**Integration tests**:
- `TestWorkflowSQL_RealDashboardQuery`.
- `TestWorkflowSQL_ContinuousQueryUpdates`.

**End-to-end tests**:
- `TestWorkflowSQLE2E_AnalyticsDashboard`.

**Negative / non-happy path tests**:
- `TestWorkflowSQL_OversizedResultPaginated`.
- `TestWorkflowSQL_PathologicalQueryTimeout`.

---

#### 29.8 — Resource-lifecycle primitive (`withResource`)

**Description**: `withResource(setup, body, teardown)` guarantees
teardown on body completion or failure. Encoded via restart effects.

**Implementation approach**:
- Combinator: `withResource(setup, body, teardown)`.
- Compiles to: `pipe(setup, restartHandle(body, teardown))`.
- Teardown runs deterministically on body completion (success) or
  body restart-perform (failure).
- Resource handle returned by setup is passed through to teardown
  via frame state.
- Code path: included in §29.2 combinators.

**Acceptance criteria**:
- Teardown always runs (on success or failure).
- Resource handle propagates correctly.
- Composes with `pipe`, `loop`, `tryCatch`.
- Determinism preserved.

**Unit tests**:
- `TestWithResource_TeardownOnSuccess`.
- `TestWithResource_TeardownOnFailure`.
- `TestWithResource_ResourceHandlePropagation`.
- `TestWithResource_ComposesWithLoop`.

**Integration tests**:
- `TestWithResource_FileLockExample` — Acquire / use / release.
- `TestWithResource_DBConnExample`.

**End-to-end tests**:
- `TestWithResourceE2E_WorktreeIsolation` — Per-claim git
  worktree with cleanup.

**Negative / non-happy path tests**:
- `TestWithResource_TeardownPanicsLogged`.
- `TestWithResource_SetupFailsBodySkipped`.
- `TestWithResource_NestedResourceCleanupOrder`.

---

#### 29.9 — Bounded-execution primitive (`withTimeout` + `race`)

**Description**: `withTimeout(body, ms)` and `race(a, b, c, ...)` as
bounded-execution primitives via restart effects + substrate timer.

**Implementation approach**:
- `withTimeout`: RestartHandle wrapping body + substrate timer (HLC
  +ms); timer fire is `RestartPerform`; restart drops body, returns
  timeout error.
- `race`: All subactions submitted in parallel (substrate fanout);
  first completion wins; cancellation propagates to others (via
  restart effect on the losing subtree).
- Substrate timer integrates with §28.1 OTel for trace.
- Code path: §29.2 combinators (~600 LOC additional).

**Acceptance criteria**:
- Timeout fires deterministically at HLC.
- Race winner returned; losers cancelled cleanly.
- No goroutine leaks on timeout / race.
- Composes with other combinators.

**Unit tests**:
- `TestTimeout_FiresAtHLC`.
- `TestTimeout_BodyCompletesBeforeTimeout`.
- `TestRace_FirstWins`.
- `TestRace_LosersCancelled`.
- `TestRace_NoGoroutineLeaks`.

**Integration tests**:
- `TestTimeoutAcrossReplicas` — HLC-deterministic timeout.
- `TestRaceComposedWithLoop`.

**End-to-end tests**:
- `TestTimeoutE2E_LLMCallTimeout`.
- `TestRaceE2E_FirstResponderWins`.

**Race condition tests**:
- `TestTimeout_FireRaceWithCompletion`.
- `TestRace_CancellationRaceWithLastCompletion`.

**Negative / non-happy path tests**:
- `TestTimeout_NegativeBound_RejectedAtCompile`.
- `TestRace_AllFail_AllErrorsAggregated`.
- `TestTimeout_PathologicalLongBody_Cancelled`.
- `TestRace_RacingHandlersWithDifferentReturnTypes_TypeError`.

---

#### 29.10 — Migration tooling: existing claims → workflow definitions

**Description**: Migration shim for existing CLAIMS.md flows
(pipeline phase choreography, saga compensation, retry budgets) into
workflow definitions where appropriate. Where existing flows already
work well, no migration; where they'd benefit, mechanical translation.

**Implementation approach**:
- Migration tool `tools/migrate_claims_to_workflow/` analyzes
  existing claim flows and emits equivalent workflow definitions.
- Manual review + opt-in: each flow's migration is operator-
  authorized; not automatic.
- Backward compat: claims-as-they-stand continue working unchanged;
  workflows are an additional way to express coordination.
- Code path: `tools/migrate_claims_to_workflow/` (~1500 LOC).

**Acceptance criteria**:
- Tool analyzes existing claim flows.
- Emits canonical workflow ASTs equivalent to those flows.
- Operator review + opt-in flow.
- Backward compatibility preserved.

**Unit tests**:
- `TestMigrate_PipelineToWorkflow`.
- `TestMigrate_SagaToWorkflow`.
- `TestMigrate_OperatorReview`.

**Integration tests**:
- `TestMigrate_RealisticPipelineFlow`.
- `TestMigrate_BackwardCompatPreserved`.

**End-to-end tests**:
- `TestMigrateE2E_FullSessionMigration`.

**Negative / non-happy path tests**:
- `TestMigrate_NonMigratableFlow_Documented`.
- `TestMigrate_PartialMigration_Resumable`.
- `TestMigrate_OperatorRejection_NoChange`.

---

### Implementation summary table

| Phase | Items | Approximate effort | Dependencies |
|-------|-------|--------------------|--------------|
| 0 — Foundation | 6 | 2 weeks | none |
| 1 — Wire format | 7 | 3 weeks | 0 |
| 2 — Local storage | 8 | 4 weeks | 0, 1 |
| 3 — Single-Raft | 7 | 5 weeks | 0, 1, 2 |
| 4 — Embedded E2E | 6 | 2 weeks | 0-3 |
| 5 — QUIC transport | 4 | 3 weeks | 0, 1 |
| 6 — SWIM | 5 | 5 weeks | 0, 1, 5 |
| 7 — Multi-Raft | 7 | 6 weeks | 3, 5, 6 |
| 8 — Replicated storage | 3 | 3 weeks | 2, 7 |
| 9 — Delivery | 3 | 3 weeks | 4, 7, 8 |
| 10 — Reliability | 5 | 4 weeks | 9 |
| 11 — Higher primitives | 8 | 8 weeks | 9, 10 |
| 12 — Observability | 4 | 3 weeks | 4, 11 |
| 13 — Migration | 6 | 12 weeks (long dual-write) | 11 |
| 14 — Remote production | 6 | 5 weeks | 11, 12 |
| 15 — Hardening | 5 | ongoing | all |
| 16 — Continuous | 4 | ongoing | all |
| 17 — Trust & adversarial | 4 | 6 weeks | 6, 7 |
| 18 — Federation, edge, witness | 8 | 12 weeks | 7, 14, 17 |
| 19 — Storage extended | 6 | 8 weeks | 2, 8, 14 |
| 20 — Multi-tenancy | 6 | 6 weeks | 11, 17 |
| 21 — Time, determinism, ops | 12 | 10 weeks | 7, 14 |
| 22 — Apps & observability extended | 12 | 10 weeks | 11, 12 |
| 23 — Hardening, catastrophic, envelope | 24 | 16 weeks | all prior |
| 24 — Research frontier | 9 | 24 weeks (theory + impl) | all prior, plus formal-methods toolchain |
| 25 — Adaptive transport | 12 | 14 weeks | 5, 6 |
| 26 — SQLite-compatible subjects (foundation) | 9 | 12 weeks | 7, 11, 14 + turso integration |
| 27 — SQLite beyond turso | 20 | 24 weeks | 26, plus §31.4 dataflow, §31.21 session types, §31.23 redaction, §31.19 geo-fence |
| 28 — Architectural refinement | 10 | 12 weeks | 17, 18, 22, 24, 25 |
| 29 — Compositional workflow + algebraic effects | 10 | 14 weeks | 26, 31, plus §24.1 harness |

**Total greenfield effort (phases 0-14)**: ~75 weeks of focused work across
the design, plus dual-write windows that parallelize with other work.

**Total effort (phases 0-29)**: ~241 weeks of focused engineering effort.
Phases 17-29 are independently shippable on top of the phase 0-14
foundation; each is gated behind its own feature flag and can land in
any order subject to its dependencies. The critical-path through phase
14 remains unchanged. Phase 24 specifically requires investment in a
formal-methods toolchain (Coq / Lean / Dafny + a proof-carrying-code
runtime) before its items become tractable. Phases 26-27 require Sylk-
native SQLite-compatible engine implementation. Phase 28 closes
substrate-design micro-gaps. Phase 29 brings Barnum-style compositional
workflow + algebraic effects to the substrate.

**Critical path**: 0 → 1 → 2 → 3 → 4 (embedded, ~4 months) is the minimum
for a runnable substrate. Remote mode requires phases 5-7 (additional ~3
months). Full migration (phase 13) is the longest tail because it requires
extended dual-write windows for safety. Phases 17-29 add cross-cutting
robustness, scale-out, envelope-pushing, transport, SQL capabilities,
architectural refinement, and compositional workflow on top of the
production-grade phase 14 cluster — none block the initial substrate
from shipping.

Each phase ships behind a feature flag; phase N's feature flag is removed
once phase N+1 (or later) depends on it irreversibly. This makes rollback
of any single phase possible.

---

## 34. Why This Scales

It's the same property that makes the upper-bound design work: **everything
is a subject, and subjects don't care about deployment shape.** A subject's
storage layer is a Merkle DAG; a subject's consumers track HLC frontiers; a
subject's authority is a predicate; a subject's dedupe is mandatory at the
wire. None of that depends on whether there's one Raft replica or twenty-one,
whether SWIM has zero peers or two thousand, whether the transport is a Go
channel or a multi-DC QUIC mesh.

What you avoid is the typical fate of distributed systems: a thick
"production" mode and a tissue-thin "embedded" mode that diverges from
production behavior the first time anyone needs a feature, leaving local
development perpetually different from prod. Same code, same WAL discipline,
same recovery semantics, same correctness guarantees. The laptop user gets
cluster-grade durability; the cluster user gets laptop-grade simplicity for
everything below the consensus layer.

The honest tradeoff: the laptop pays a small overhead (every namespace is a
degenerate Raft group; every publish is HLC-stamped, BLAKE3-fingerprinted,
schema-validated) that a hand-rolled embedded substrate could skip. In
exchange, the laptop user inherits everything the cluster has — including
the ability to graduate seamlessly when the use case demands it, with zero
behavior changes and a working migration path.

---

## 35. Bottom Line

The substrate is the recognition that Sylk already has six different durable
logs (claims board, forest, activity store, ControlWAL, copy retention,
agent log), three different gossip-ish coordination paths (fabric envelope,
bus, direct dispatch), and two different consensus moments (commit_resolver
water-line, pipeline merge), and they all want to be *one substrate* with
three properties: causal Merkle DAG storage, multi-Raft consensus, and
SWIM++ membership — with everything else as projections.

Building it that way unifies what's currently scattered, lets time-travel
debugging fall out for free, and makes the agent-to-agent coordination
story a first-class data model rather than nine subsystems pretending
they're independent. It scales down to a single laptop without forking the
abstractions, and scales up to a multi-DC cluster without changing the user
experience beyond latency. Same code, top to bottom.
