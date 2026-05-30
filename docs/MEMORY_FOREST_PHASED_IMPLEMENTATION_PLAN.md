# Memory Forest Phased Implementation Plan

This plan converts the current branch-centric memory forest into a
claims-governed emergent forest with ecological substrate, hyper-heuristic
policy selection, memetic learning, and proposal-only generated skills.

The plan is intentionally replacement-oriented. Transitional code may exist
inside a phase for migration and test comparison, but a phase is not accepted
until the listed legacy runtime path has been removed from production use.

## Source Architecture

Primary architecture documents:

- `docs/MEMORY_FOREST.md`
- `docs/EMERGENT_FOREST.md`
- `docs/EMERGENT_AGENCY.md`
- `docs/ECOLOGY.md`
- `docs/CLAIMS.md`
- `docs/CLAIMS_AND_INFRASTRUCTURE.md`
- `docs/CLAIMS_AND_DELTAS.md`
- `docs/ARTIFACTS_AND_VALIDATIONS.md`

Current implementation anchors:

- `core/forest/types.go`
- `core/forest/schema.go`
- `core/forest/service.go`
- `core/forest/projector.go`
- `core/forest/query.go`
- `core/forest/substrate.go`
- `core/forest/claims_harvester.go`
- `core/forest/hyperparameter_tuner.go`
- `agents/shared/memory_forest.go`
- `agents/shared/forest_preload.go`
- `core/context/skills/forest_skills.go`
- `core/context/skills/forest_role_skills.go`
- `core/activity/activitystore/forest_subscriber.go`
- `core/fabriclog/forest_bridge.go`
- `core/claims/canonical_delta.go`
- `core/claims/bus_publisher.go`
- `core/claims/artifact_validation_board.go`
- `core/claims/validator_interfaces.go`
- `core/claims/mocks`

Hard constraints:

- Do not introduce SQLite extensions, including FTS3, FTS4, FTS5, R-Tree,
  JSON1-specific query behavior, sqlite-vec, sqlite-vss, spatialite, or any
  loadable extension.
- Use SQLite only for relational storage, Bleve for full-text search, and
  VectorDB/HNSW for semantic neighborhoods.
- Do not create untracked goroutines.
- Do not create unbounded queues, maps, caches, replay buffers, candidate
  populations, or search frontiers.
- Do not silently drop work. Overflow must produce artifacts, testaments, or
  explicit retry records.
- Avoid magic numbers. Coefficients must be derived from observed data,
  declared hyperparameters with provenance, or validation-backed policy
  candidates.
- Keep cyclomatic complexity below 4 by splitting projection, validation, and
  scoring steps into small typed functions.

## Testing Terms

Every phase uses these test categories.

- Unit tests exercise a single package with in-memory or temp-file storage,
  fake clocks, deterministic IDs, and no external services.
- Integration tests run real Sylk components together: durable claims board,
  Guide event bus adapter, forest SQLite database, Bleve temp index, and
  VectorDB temp store where required.
- E2E tests exercise a session-level flow using fake agents and
  Vektra/mockery mocks for interfaces that would otherwise call LLMs, remote
  services, GUI surfaces, or non-deterministic providers.
- Race tests run under `go test -race` for packages touched by the phase.
- Deadlock tests use bounded contexts, controlled blocked mocks, and shutdown
  assertions.
- Performance tests use Go benchmarks or bounded wall-clock assertions with
  fixture sizes large enough to expose quadratic behavior.

New interfaces added for integration or e2e boundaries must include
`//go:generate mockery --name=... --output=./mocks --outpkg=mocks`, following
the pattern already used by `core/claims/validator_interfaces.go` and
`core/claims/mocks`.

## Phase 1: Architectural Invariants

### Item 1.1: Replace ad hoc forest workers with a tracked runtime scope

Description and examples:

The current forest starts multiple background workers from `MemoryForest.New`
and helper methods such as `startBranchProjector`,
`startRetrievalAuditDrainer`, `startImplicitNegativeSweeper`,
`startAntiPatternPromoter`, and pruners. Some are already reasonably scoped,
but the runtime contract is spread across files. Replace this with one
forest-owned runtime scope that registers every worker, queue, ticker, lease,
and shutdown path.

Example: instead of `New` directly starting a projector and each maintenance
worker independently, `New` constructs `ForestRuntime`, registers named workers
with bounded inputs, then starts them through one lifecycle manager:

```text
forest.runtime.register("node_projector", queue=ProjectorQueue)
forest.runtime.register("policy_trials", queue=PolicyTrialQueue)
forest.runtime.register("retrieval_audit", queue=AuditQueue)
forest.runtime.start(ctx)
```

Implementation guide:

- Add `core/forest/runtime.go` with a `Runtime` type that owns a context,
  cancellation, worker registry, queue registry, panic recovery, shutdown
  timeout, and metrics snapshot.
- Replace direct worker starts in `core/forest/service.go` with registration.
- Move worker constants out of individual files into typed `RuntimeLimits`
  derived from `HyperParameters`, config, or observed startup workload.
- Make every worker return a terminal error or nil. Panic recovery must create
  a forest error artifact in later phases and must be visible through health in
  this phase.
- Replace fire-and-forget callbacks with runtime-submitted tasks. If a task
  cannot be queued, record a bounded overflow record.
- Keep shutdown idempotent. Multiple `Close` calls must not panic, block, or
  double-close channels.

Legacy path to remove:

- Production worker startup directly from `New` and `start*` methods without
  runtime registration.

Acceptance criteria:

- `MemoryForest` has exactly one owner for worker lifecycle.
- Every worker has a name, queue limit, start time, last success, last error,
  and shutdown state.
- Worker queue capacities are not literals hidden inside workers.
- Worker panics are recovered and visible in health state.
- Shutdown waits for every registered worker or records a timeout artifact in
  the runtime health projection.
- No production goroutine is started without going through the runtime.

Tests:

- Unit happy: register two workers, process tasks, close cleanly, assert all
  workers report stopped.
- Unit negative: worker returns an error; health reports the named error and no
  other worker is terminated unless policy says fatal.
- Unit edge: close before start, start twice, close twice, nil worker
  registration, zero-capacity queue rejected.
- Race: concurrently submit tasks and close runtime under `go test -race`.
- Deadlock: one worker blocks on a mock dependency; shutdown context expires
  and runtime exits with a named timeout instead of hanging.
- Performance: 100k lightweight tasks across bounded queues complete without
  unbounded heap growth.

### Item 1.2: Replace branch event storage with a canonical forest ledger

Description and examples:

`forest_events` currently stores forest-specific events with family, root, and
branch fields. That shape encodes the legacy ontology. Replace it with a
canonical forest ledger that stores immutable facts from claims deltas, fabric
records, retrieval observations, policy decisions, and maintenance outcomes.

Example ledger records:

```text
source_kind=claims_delta
source_id=delta_...
source_key=claim.posted:sess:board:seq:...
event_kind=claim.posted
subject_ref=claim:c123
actor_ref=agent:architect:uid
payload_ref=claims_delta_context_hash
```

Implementation guide:

- Add schema for `forest_ledger`, `forest_ledger_payloads`, and
  `forest_projection_offsets`.
- Make `source_key` unique per source partition. For claims deltas, use
  `claims.CanonicalDelta.DeltaKey()`. For fabric traversal events, use a
  stable record key. For internal forest maintenance, use policy or worker
  action IDs.
- Store payload bodies as canonical JSON text, but do not query JSON with
  SQLite JSON functions. Extract queryable fields into relational columns.
- Add append-only triggers for the new tables. Updates and deletes are blocked
  except for explicitly documented archive migration tables.
- Replace `AppendEvent` and `appendEventLedger` with `AppendLedgerRecord`.
  Callers must provide a typed source and stable idempotency key.
- Add a one-time migration tool that reads existing `forest_events` and writes
  corresponding legacy-import ledger records. This tool is not a runtime path.

Legacy path to remove:

- Runtime writes to `forest_events`.
- Runtime dependence on `Event.Family`, `Event.RootID`, and `Event.BranchID`
  as primary ontology.

Acceptance criteria:

- All new forest projection code reads from `forest_ledger`.
- `forest_events` is no longer written in production.
- Duplicate ledger appends with the same `source_key` are idempotent.
- Ledger records retain session, task, board, sequence, actor, refs, and
  source provenance where available.
- Ledger schema can be rebuilt from scratch and produce identical projection
  offsets from the same source stream.

Tests:

- Unit happy: append canonical record and read it back with exact refs.
- Unit negative: missing source key, missing event kind, malformed actor ref,
  or empty session is rejected.
- Unit edge: duplicate append returns existing record without double
  projecting.
- Integration: migrate a fixture `forest_events` database into the new ledger
  and assert node projection parity for supported fields.
- Race: concurrent duplicate appends produce one row and no unique constraint
  leak.
- Performance: append and scan 1M narrow ledger records with bounded memory and
  stable per-batch time.

### Item 1.3: Replace implicit schema compatibility with explicit schema gates

Description and examples:

`schema.go` currently owns a broad expected schema hash and table creation.
As the forest becomes an ecosystem, schema changes must be gated by explicit
versions and replacement milestones. The gate prevents old branch tables from
remaining active after their replacement phase.

Implementation guide:

- Add `forest_schema_meta` with `schema_version`, `replaces_version`,
  `applied_at`, `code_version`, and `migration_id`.
- Split schema creation into versioned migration functions in
  `core/forest/schema_migrations.go`.
- Add startup checks that reject mixed runtime states, for example new node
  projector enabled while old branch projector is still configured as active.
- Add a schema audit that fails if any prohibited SQLite extension table or
  virtual table is present.
- Keep migrations deterministic and idempotent.

Legacy path to remove:

- One monolithic schema bootstrap where new and legacy projections are all
  treated as permanent peers.

Acceptance criteria:

- Startup reports exact schema version and active projection version.
- Startup fails closed for unsupported mixed projection configurations.
- No virtual tables or extension-backed SQLite objects are created.
- Schema tests prove migrations can run twice without changing data.

Tests:

- Unit happy: migrate empty DB to latest version.
- Unit negative: DB with unsupported legacy active flag refuses startup.
- Unit edge: migration interrupted after metadata write is detected and
  repaired or rejected deterministically.
- Integration: open an existing forest DB, migrate, restart, and assert schema
  version stable.
- Performance: schema audit over all SQLite objects stays linear in object
  count.

## Phase 2: Claims-Native Ingestion

### Item 2.1: Replace `ClaimsHarvester` with canonical delta ingestion

Description and examples:

`core/forest/claims_harvester.go` maps activity payloads into forest events.
That loses canonical claim lifecycle semantics. Replace it with a
`DeltaIngestor` that subscribes to `claims.DeltaSubscriber` and appends
canonical `claims.CanonicalDelta` records directly to the forest ledger.

Examples:

- `claim.posted` becomes a claim node seed.
- `testament.posted` becomes a testament evidence seed.
- `artifact.generated` becomes an artifact evidence seed before any testament
  closes.
- `validation.validated` increases validation-backed confidence.
- `validation.validation_failed` creates contradiction or corruption pressure.

Implementation guide:

- Add `core/forest/delta_ingestor.go`.
- Define a narrow interface around `claims.DeltaSubscriber` if needed, then
  generate Vektra/mockery mocks for forest tests.
- Subscribe to canonical claim, artifact, validation, board, and agent topics
  using `claims.Canonical*ActionPattern` helpers.
- Use tolerant canonical decoding for observer ingestion, but reject malformed
  envelopes before appending.
- Deduplicate by `DeltaKey` plus sequence.
- Store all `DeltaRef` entries in relational `forest_ledger_refs`.
- Store `Actor`, `Delivery`, and participant refs in normalized relational
  columns.
- Route legacy `InboxDelta`, `TestamentDelta`, `ValidationDelta`, and
  `ClaimStatusDelta` through a compatibility decoder only in migration tests.
  Production ingestion must use canonical deltas.

Legacy path to remove:

- `ClaimsHarvester.Harvest` as the production claims path.
- Activity-payload heuristics for claim acceptance, rejection, testament, and
  validation meaning.

Acceptance criteria:

- Every known `claims.KnownDeltaActions()` value is either ingested with a
  typed mapping or explicitly ignored with a documented reason and audit
  record.
- No canonical claim lifecycle state is inferred from display strings.
- Delivery refs and participant refs are preserved.
- Duplicate bus delivery does not duplicate ledger records or projections.
- Subscriber failure produces a visible runtime health error.

Tests:

- Unit happy: one canonical delta per action maps to the expected ledger kind.
- Unit negative: malformed schema, empty refs, unknown required action, missing
  delivery on a delivery-required action.
- Unit edge: future action passes tolerant validation but is recorded as
  `unknown_observed_delta` with no projection side effect.
- Integration with mockery: use `core/claims/mocks.DeltaSubscriber` to capture
  handlers, deliver deltas, and assert exact ledger rows.
- E2E with mockery: post a real claim on a durable board with mocked bus
  subscriber and assert the forest receives canonical claim, artifact,
  testament, validation, and transition records.
- Race: deliver the same delta concurrently from multiple handlers.
- Deadlock: handler blocks on full ledger queue; bounded backpressure records
  overflow instead of blocking bus shutdown.
- Performance: ingest 100k canonical deltas with stable batching and bounded
  memory.

### Item 2.2: Replace fabric claim harvesting with traversal-only fabric ingestion

Description and examples:

`core/activity/activitystore/forest_subscriber.go` and
`core/fabriclog/forest_bridge.go` currently help claims enter the forest
through activity events. After canonical delta ingestion, fabric should record
traversal and context observations, not claim lifecycle truth.

Example:

- A fabric consume/resolve record becomes `traversal.observed`.
- A bridge crossing becomes `bridge.crossed`.
- A context suppression event becomes `context.suppressed`.
- A claim status change never enters from fabric if the claims delta exists.

Implementation guide:

- Replace forest eligibility rules in `forest_subscriber.go` with a strict
  traversal/context classifier.
- Update `forest_bridge.go` so it uses scoped context from the runtime, not
  `context.Background()`.
- Persist `candidate.Reason` as a ledger field.
- Stop forwarding claim lifecycle activity kinds to the forest.
- Add conflict detection: if both fabric and claims provide the same claim
  lifecycle fact, claims wins and fabric is recorded only as delivery evidence.

Legacy path to remove:

- Claim lifecycle ingestion from `ActivityCandidate`.
- Background-context harvesting in `forest_bridge.go`.

Acceptance criteria:

- Fabric cannot create claim, testament, artifact, or validation lifecycle
  facts.
- Fabric traversal records include source activity ID, reason, consume/resolve
  state, actor, and refs.
- Any fabric ingestion error is visible to runtime health.

Tests:

- Unit happy: traversal candidate maps to traversal ledger record.
- Unit negative: claim lifecycle candidate is rejected or downgraded to
  delivery evidence.
- Unit edge: candidate with missing reason stores an explicit empty reason and
  does not panic.
- Integration: activity store emits mixed activity stream; forest records only
  traversal/context records.
- Race: simultaneous fabric and claims observations for the same claim do not
  create duplicate lifecycle nodes.
- Performance: classifier handles large activity batches without allocating
  per-rule regex state.

## Phase 3: Artifact And Validation Model

### Item 3.1: Replace artifact-as-payload with artifact evidence records

Description and examples:

Artifacts currently reach the forest indirectly through payloads and branch
support. Replace this with first-class artifact evidence records linked to
claims, testaments, validations, agents, and content hashes.

Examples:

- A generated markdown plan is `artifact_node(kind=document, status=generated)`.
- A tool failure is `artifact_node(kind=error, status=generated)`.
- A validation result artifact is linked to both the validation and the target
  artifact.

Implementation guide:

- Add `forest_artifacts` with artifact ID, claim ID, testament ID, generator
  participant, artifact kind, data type, content hash, content ref, status,
  validation status, and last lifecycle sequence.
- Add `forest_artifact_edges` for generated_by, attached_to, validates,
  invalidates, supersedes, and remediates.
- Populate from canonical artifact deltas and validation lifecycle deltas.
- Derive content hashes in Go when inline content is available. Do not use
  SQLite JSON or hash extensions.
- Replace branch support/counter-evidence fields with artifact evidence edges.

Legacy path to remove:

- Treating artifacts only as opaque event payloads inside `Branch.Support` or
  `BranchPacket.Support`.

Acceptance criteria:

- Every artifact lifecycle delta updates exactly one artifact evidence record.
- Artifact lifecycle order is monotonic by board sequence.
- Error artifacts are queryable as artifacts, not logs.
- Artifact evidence can be retrieved without loading branch packets.

Tests:

- Unit happy: generated, received, attached, validating, validated lifecycle
  produces one artifact row and expected edges.
- Unit negative: lifecycle sequence regression is rejected and recorded.
- Unit edge: artifact with missing hash is accepted only if it has an external
  content ref or explicit unavailable reason.
- Integration with mockery: use `ArtifactLifecycleBusSink` mock or canonical
  delta mock to emit artifact transitions and assert records.
- E2E: agent returns a testament with one success artifact and one error
  artifact; forest records both and retrieval can show both.
- Race: artifact and testament deltas arriving out of order converge after
  replay.
- Performance: 100k artifact lifecycle records project in bounded batches.

### Item 3.2: Replace validation hints with validation evidence records

Description and examples:

Validations must become evidence with lifecycle, not a score adjustment hidden
inside retrieval. A failed validation should be usable by retrieval,
immunity, remediation, and skill generation.

Implementation guide:

- Add `forest_validations` with validation ID, claim ID, target artifact ID,
  evaluator participant, validation type, status, result artifact ID,
  failure reason, required flag, and sequence.
- Add `forest_validation_patterns` later in the phase to summarize repeated
  validation success by claim shape and artifact type.
- Populate from `validation.*` canonical deltas and the result artifact refs
  carried by the claims board.
- Replace `EventTypeValidation` branch projection as the primary validation
  signal.

Legacy path to remove:

- Using `EventTypeValidation` as the primary validation model.

Acceptance criteria:

- Each validation lifecycle transition is represented exactly once.
- Validations are bound to artifacts where the claims architecture requires
  one-to-one validation.
- Validation failures create evidence edges to target artifact, claim, and
  remediation claims.
- Validation records can answer: what was validated, by whom, with what
  result, and what artifact proves the result?

Tests:

- Unit happy: validation ready, validating, validated with result artifact.
- Unit negative: validation completed without required target artifact is
  rejected unless validation type is explicitly receipt-only.
- Unit edge: not-required failure status does not suppress the claim as hard
  failure, but still contributes weak corruption.
- Integration: durable board `CompleteValidationLifecycle` emits canonical
  validation and artifact deltas that project into forest records.
- E2E with mockery: mocked validator returns a result artifact; forest
  retrieval shows validation evidence.
- Race: validation result and artifact result deltas arrive in either order.
- Performance: validation pattern aggregation remains sublinear through
  incremental summaries.

## Phase 4: Emergent Node Graph

### Item 4.1: Replace branch ontology with interaction nodes

Description and examples:

The current forest primary model is `TreeFamily`, `Branch`, and
`BranchPacket`. Replace it with `ForestNode`, `ForestEdge`, and `ForestPacket`.
Families may be deleted from primary logic. If labels are still useful, they
become derived node tags.

Examples:

- Claim posted: node kind `claim`.
- Artifact generated: node kind `artifact`.
- Validation failed: node kind `validation`.
- Agent consult: node kind `interaction`.
- Contradiction: node kind `contradiction`.
- Policy trial: node kind `policy_trial`.
- Generated skill candidate: node kind `skill_candidate`.

Implementation guide:

- Add `core/forest/node_types.go`.
- Replace `TreeFamily`-driven logic in `types.go` with typed node kinds,
  edge kinds, and evidence grades.
- Add `forest_nodes` and `forest_node_edges`.
- Define stable node IDs as deterministic hashes over source partition, source
  key, node kind, and subject ref.
- Define edge IDs as deterministic hashes over source node, target node, edge
  kind, and source key.
- Move branch-only fields such as `RootID`, `BranchID`, and `Family` out of
  production ingestion.
- Delete or quarantine code paths that create branches from events once node
  projection is complete.

Legacy path to remove:

- `TreeFamily` as a primary storage or retrieval dimension.
- `Branch` as primary memory object.

Acceptance criteria:

- All projected facts create nodes and edges, not branches.
- Node and edge creation is idempotent.
- Node kinds are closed and documented.
- Edge kinds include causality, support, contradiction, validation, lineage,
  co-use, similarity, remediation, suppression, and traversal.
- No production query requires `forest_branches`.

Tests:

- Unit happy: each ledger kind maps to expected node and edge set.
- Unit negative: unsupported node kind rejected before database write.
- Unit edge: duplicate source facts create no duplicate nodes or edges.
- Integration: replay a mixed claim/artifact/validation/fabric ledger into
  nodes and compare deterministic snapshot.
- Race: concurrent projector batches produce the same final graph as serial
  replay.
- Performance: edge diff application is O(changed edges), not O(total graph).

### Item 4.2: Replace branch projector with node projector

Description and examples:

`core/forest/projector.go` projects `forest_events` into branches. Replace it
with a node projector that reads `forest_ledger`, writes nodes and edges, and
stores offsets in `forest_projection_offsets`.

Implementation guide:

- Add `core/forest/node_projector.go`.
- Use the Phase 1 runtime scope for lifecycle.
- Project in deterministic sequence order per source partition.
- Make projection functions pure where possible: ledger record in, node/edge
  operations out.
- Apply operations in one SQLite transaction per batch.
- Poison records must be isolated: after a bounded retry count, record a
  projection failure artifact and advance only if the failure policy says the
  record is non-critical.
- Remove production startup of `startBranchProjector`.

Legacy path to remove:

- `startBranchProjector` as production projector.
- `projectBranchTx`, `applyBranchEventTx`, and branch materialization as
  required runtime behavior.

Acceptance criteria:

- Projector can rebuild all node tables from an empty projection state.
- Projector offset advances only after transaction commit.
- Projection failures are durable and visible.
- Shutdown during a batch leaves either no batch effects or a fully committed
  batch.

Tests:

- Unit happy: pure projection rules for every ledger kind.
- Unit negative: poison record retries to limit and emits failure evidence.
- Unit edge: out-of-order source partitions are handled by partition offsets.
- Integration: delete projections, replay ledger, assert checksum identical.
- E2E: claim lifecycle through board produces graph nodes without any branch
  write.
- Race: concurrent projector wakeups do not double-project.
- Deadlock: database lock contention respects context and releases worker.
- Performance: projector throughput target defined from current branch
  projector baseline and must not regress by more than an accepted threshold.

## Phase 5: Cluster And Forest Topology

### Item 5.1: Replace family grouping with density clusters

Description and examples:

The emergent forest defines trees as density clusters, not declared memory
families. Replace family grouping with cluster membership derived from semantic
neighborhood, traversal co-use, validation support, and contradiction pressure.

Implementation guide:

- Add `forest_clusters`, `forest_cluster_membership`, and
  `forest_cluster_metrics`.
- Define a `NeighborIndex` interface over VectorDB/HNSW and generate mockery
  mocks for integration tests.
- Use Bleve only for text recall and seed expansion, not as cluster truth.
- Implement online clustering in bounded maintenance batches. Avoid global
  recompute except in explicit rebuild tools.
- Derive thresholds from observed neighborhood density distributions and
  policy parameters, not literals.

Legacy path to remove:

- Family canopy as the primary cluster proxy.
- Retrieval grouping by `TreeFamily`.

Acceptance criteria:

- Every active node belongs to zero or more clusters with bounded membership.
- Cluster metrics include cohesion, validation density, contradiction load,
  novelty, utility, and decay pressure.
- Cluster updates are replayable from node graph plus policy version.
- No SQLite extension is used for clustering.

Tests:

- Unit happy: small graph produces expected clusters and memberships.
- Unit negative: neighbor index failure records maintenance error and does not
  corrupt memberships.
- Unit edge: isolated nodes remain unclustered or in singleton clusters
  according to policy.
- Integration with mockery: mocked `NeighborIndex` returns deterministic
  neighborhoods; clusterer writes stable memberships.
- E2E: repeated related claims form a named candidate cluster after threshold.
- Race: cluster maintenance and node projection do not deadlock.
- Performance: incremental update cost bounded by changed node neighborhood.

### Item 5.2: Add bridge nodes, points of interest, and cluster lineage

Description and examples:

Bridge nodes connect clusters. Points of interest surface critical evidence,
brittle nodes, validation hubs, contradiction centers, and high-value
artifacts. Lineage records split, merge, dormancy, extinction, and speciation.

Implementation guide:

- Add `forest_cluster_lineage`, `forest_bridge_nodes`, and `forest_poi_cache`.
- Compute bridge score from cross-cluster edge diversity, traversal frequency,
  validation support, and contradiction risk.
- Compute PoI records as bounded cache entries with source metric snapshots.
- Require naming gates: a cluster cannot receive a stable name until it has
  enough age, evidence, and validation density.
- Store lineage operations as ledger-backed projection facts.

Legacy path to remove:

- Relay edges as the primary cross-topic structure.

Acceptance criteria:

- Bridge nodes are explainable by concrete graph edges.
- PoI entries have reason, source metrics, expiry, and invalidation sequence.
- Cluster split/merge/speciation lineage is replayable.
- Cluster names are not assigned from a single event or unvalidated label.

Tests:

- Unit happy: graph with two clusters and one connector identifies a bridge.
- Unit negative: bridge without cross-cluster evidence is rejected.
- Unit edge: rapid split/merge oscillation is damped by policy and recorded.
- Integration: cluster lineage replay produces identical active clusters.
- E2E: agent consult crossing clusters emits a bridge observation and PoI.
- Performance: PoI refresh uses bounded top-K per cluster.

## Phase 6: Ecological Substrate

### Item 6.1: Replace scalar substrate with multi-channel fields

Description and examples:

`substrate.go` currently computes conductance, flux, redundancy, inhibition,
and frontier state. Replace this with explicit channels such as attention,
confidence, contradiction, novelty, utility, validation energy, suppression,
and recovery.

Implementation guide:

- Add `forest_substrate_channels`, `forest_substrate_field`, and
  `forest_resource_accounting`.
- Replace `refreshSubstrateState` with channel update steps:
  source injection, diffusion, local normalization, resource accounting,
  suppression, recovery, and writeback.
- Derive diffusion and damping coefficients from channel policy records.
- Keep every update bounded by changed nodes, affected clusters, or explicit
  rebuild scope.
- Store policy version on every field snapshot.

Legacy path to remove:

- Scalar `forest_substrate_state` and `forest_substrate_edges` as production
  substrate truth.
- Fixed hidden coefficients in `refreshSubstrateState`.

Acceptance criteria:

- Every channel has a documented source, sink, bounds, and conservation rule.
- Substrate update is deterministic for same graph, same policy, same ledger.
- No channel can grow without bound.
- Suppression never deletes evidence. It only affects activation and routing.

Tests:

- Unit happy: channel injection and diffusion on a small graph match expected
  values.
- Unit negative: invalid policy with negative bounds or NaN coefficients is
  rejected.
- Unit edge: disconnected component, zero-resource cluster, and all-suppressed
  cluster remain stable.
- Integration: validation success increases confidence and recovery while
  validation failure increases contradiction.
- Race: substrate maintenance concurrent with retrieval uses snapshot
  isolation.
- Performance: update time scales with affected subgraph, not total database.

### Item 6.2: Add adaptive thresholds and resource accounting

Description and examples:

The ecology requires synaptic scaling and BCM-like adaptive thresholds. High
use should not let one memory monopolize the forest. Low-quality repetition
should not inflate trust.

Implementation guide:

- Add per-node and per-cluster activation history.
- Compute adaptive thresholds from sliding windows with bounded retention.
- Add resource budgets per cluster and per agent interaction.
- Charge resources for retrieval exposure, claim proposal, policy trials, and
  generated skill proposals.
- Credit resources from validation success, durable artifact use, and resolved
  contradiction.

Legacy path to remove:

- Warmth-only reinforcement as the dominant activation memory.

Acceptance criteria:

- Repeated retrieval without validation eventually saturates or normalizes.
- Validated artifacts can recover activation.
- Resource balances are bounded and auditable.
- Thresholds are explainable from stored history and policy version.

Tests:

- Unit happy: repeated success lowers threshold within bounds; repeated weak
  exposure saturates.
- Unit negative: malformed history window rejected.
- Unit edge: newly created node has cold-start threshold derived from cluster
  priors.
- Integration: retrieval, validation, and contradiction events update resource
  accounting correctly.
- Performance: sliding window updates do not scan full history.

## Phase 7: Antigenic Field And Immunity

### Item 7.1: Add corruption and immunity vectors

Description and examples:

ECOLOGY describes an antigenic field. Implement corruption and immunity as
typed vectors attached to nodes and clusters.

Examples:

- Corruption dimensions: stale evidence, failed validation, contradiction,
  broken artifact, harmful precedent, bad prediction.
- Immunity dimensions: cited validation, repeated success, cross-agent
  agreement, durable artifact, resolved contradiction.

Implementation guide:

- Add `forest_antigenic_vectors` and `forest_immunity_vectors`.
- Update vectors from artifact and validation evidence, contradiction edges,
  retrieval outcomes, and remediation claims.
- Add vector decay and replay protection. Replaying the same validation cannot
  inflate immunity beyond idempotent contribution.
- Add quarantine factor to retrieval scoring and policy fitness.

Legacy path to remove:

- Treating contradiction as only counter-evidence in branch packets.

Acceptance criteria:

- Every vector update references source evidence.
- Duplicate evidence cannot double-count.
- Quarantine affects activation but preserves audit retrieval.
- Recovery requires new validation or remediation evidence.

Tests:

- Unit happy: failed validation increases corruption; later remediation
  validation increases recovery and immunity.
- Unit negative: duplicate validation delta does not increase immunity twice.
- Unit edge: contradictory strong evidence produces quarantine, not deletion.
- Integration: retrieval excludes quarantined node from normal answers but
  includes it when counter-evidence is requested.
- Race: simultaneous contradiction and remediation updates converge.
- Performance: vector update uses sparse dimensions and bounded fanout.

### Item 7.2: Add outbreak detection and remediation claims

Description and examples:

Fast-growing contradiction or validation failure in a cluster should produce a
forest-generated remediation claim proposal.

Implementation guide:

- Add `forest_outbreaks` with cluster ID, vector dimension, growth rate,
  evidence refs, status, and proposed claim ID.
- Compute outbreak candidates from vector deltas and cluster metrics.
- Emit a claim proposal artifact through governance, not a silent board
  mutation.
- Route severe bridge-node outbreaks to Guardian review.

Legacy path to remove:

- Passive contradiction recording with no remediation path.

Acceptance criteria:

- Outbreak records are reproducible from vector history.
- Remediation proposals include supporting evidence and counter-evidence.
- Guardian review is required for bridge-node or cross-cluster outbreaks.

Tests:

- Unit happy: repeated validation failures cross outbreak threshold and create
  one proposal.
- Unit negative: low-evidence outbreak candidate is rejected.
- Unit edge: outbreak resolves before proposal dispatch; proposal is
  superseded with reason.
- Integration with mockery: mocked claim proposal sink receives exact
  remediation claim artifact.
- E2E: failing skill candidate validations trigger quarantine and remediation
  proposal.
- Performance: outbreak scan operates on changed clusters only.

## Phase 8: Retrieval V2

### Item 8.1: Replace `BranchPacket` retrieval with evidence-backed `ForestPacket`

Description and examples:

`Retrieve` currently returns `[]*BranchPacket`. Replace this with
`[]*ForestPacket` containing nodes, paths, clusters, artifacts, validations,
counter-evidence, PoIs, bridge risk, quarantine state, and proposed claim
templates.

Implementation guide:

- Add `ForestPacket`, `ForestPath`, `ForestEvidence`, and `ForestCursor` to
  `core/forest`.
- Change `MemoryForestService` in `agents/shared/memory_forest.go` to return
  forest packets.
- Replace branch scoring in `query.go` with multi-stage retrieval:
  lexical seed, vector seed, graph expansion, cluster context, evidence
  binding, risk scoring, diversity, and packet assembly.
- Keep anti-precedent and counter-evidence behavior as first-class packet
  sections.
- Delete production branch packet assembly.

Legacy path to remove:

- `BranchPacket` as agent-facing retrieval response.
- Branch-first ranking in `query.go`.

Acceptance criteria:

- Agents receive evidence-backed packets with provenance.
- High-impact retrieval includes counter-evidence by default or records why it
  was skipped.
- Packets include artifact and validation refs, not copied opaque summaries
  only.
- Quarantined evidence is visible as risk, not silently hidden from audit
  queries.

Tests:

- Unit happy: query returns packet with node path, cluster, artifacts, and
  validations.
- Unit negative: missing evidence binding causes packet assembly failure, not
  unsupported empty support.
- Unit edge: no semantic hits falls back to lexical and recent validated
  evidence.
- Integration: real Bleve temp index plus mocked VectorDB neighborhoods.
- E2E with mockery: agent forest skill retrieves a packet and cites artifact
  refs in a generated testament.
- Race: retrieval snapshot remains consistent while projector updates nodes.
- Performance: retrieval p95 target defined against current branch retrieval
  baseline and must not regress beyond accepted threshold.

### Item 8.2: Add forest cursor propagation

Description and examples:

Emergent agency requires agents to carry forest state across work. A cursor
captures local clusters, active nodes, bridge crossings, risk, and validation
needs.

Implementation guide:

- Add `ForestCursor` with cursor ID, session, task, active cluster IDs,
  active node IDs, PoI refs, bridge refs, risk flags, and policy version.
- Store cursor snapshots in `forest_cursors`.
- Inject cursor into agent context and fabric baggage at agent boundary.
- Update role forest skills to accept cursor-aware queries.
- Emit `bridge.crossed`, `context.under_review`, and `suppression.active`
  ledger records from cursor transitions.

Legacy path to remove:

- Stateless forest consults that ignore prior traversal context.

Acceptance criteria:

- Every agent turn can access a cursor or an explicit no-cursor reason.
- Cursor IDs are stable for a turn and immutable after creation.
- Cursor propagation does not wake agents or mutate claims by itself.
- Bridge crossings are recorded with source and target clusters.

Tests:

- Unit happy: cursor assembled from packet and serialized into baggage.
- Unit negative: cursor with nonexistent node ID rejected.
- Unit edge: cursor too large is compacted using deterministic top-K policy.
- Integration: fabric message carries cursor and downstream agent retrieves it.
- E2E: multi-agent consult crosses clusters and records bridge event.
- Race: cursor snapshot remains immutable during concurrent retrieval.

## Phase 9: Agent And Fabric Integration

### Item 9.1: Replace limited preloads with role-specific forest projections

Description and examples:

`agents/shared/forest_preload.go` currently preloads only selected projections.
Replace this with role-specific projections for every relevant agent type.

Implementation guide:

- Add projection builders for architect, engineer, tester, guardian,
  inspector, librarian, academic, designer, orchestrator, scribe, archivalist,
  and guide.
- Each projection consumes `ForestPacket` and `ForestCursor`, not branches.
- Keep projection size bounded by role budget and context budget.
- Include explicit evidence refs in preload text.
- Update all agent entry points that currently use old memory forest
  projections.

Legacy path to remove:

- Architect/librarian-only preload path.
- Branch-family preload formatting.

Acceptance criteria:

- Every role has a documented forest projection or explicit reason for none.
- Projection content includes evidence refs and risk flags.
- Projection generation is deterministic for same packets and cursor.
- Projection size respects context budget.

Tests:

- Unit happy: each role projection renders expected sections.
- Unit negative: missing required evidence ref fails projection build.
- Unit edge: over-budget packet set compacts deterministically.
- Integration: agent runtime receives role projection during turn setup.
- E2E with mockery: mocked LLM provider receives prompt containing forest
  cursor and evidence refs.
- Performance: projection rendering linear in packet count.

### Item 9.2: Replace passive memory skills with agency-aware forest skills

Description and examples:

Current forest skills expose recall and prediction. Replace them with skills
that support claim proposal, validation suggestion, contradiction review,
remediation lookup, bridge review, and skill-candidate review.

Implementation guide:

- Update `core/context/skills/forest_skills.go` and
  `core/context/skills/forest_role_skills.go`.
- Remove branch/family input parameters from public skills.
- Add operations:
  - `forest.retrieve_evidence`
  - `forest.suggest_validations`
  - `forest.propose_claim`
  - `forest.review_contradiction`
  - `forest.review_bridge`
  - `forest.review_skill_candidate`
  - `forest.record_outcome`
- Each mutating operation creates a claim proposal artifact or ledger record,
  not direct trusted infrastructure mutation.

Legacy path to remove:

- Generic branch recall skills as the main agent contract.

Acceptance criteria:

- Skill schemas expose node, cluster, artifact, validation, and cursor refs.
- Skills cannot install generated skills or alter permissions.
- Skill outputs include evidence and validation requirements.

Tests:

- Unit happy: skill schema validates representative requests.
- Unit negative: request to install generated skill is rejected.
- Unit edge: no cursor falls back to session/task query with audit reason.
- Integration: skill call creates claim proposal artifact via mocked poster.
- E2E: agent uses forest validation suggestion before submitting testament.

## Phase 10: Hyper-Heuristic Engine

### Item 10.1: Replace perturbation tuner with bounded policy engine

Description and examples:

`HyperParameterTuner.proposePerturbation` currently nudges a small set of
numbers. Replace it with a policy engine that selects strategies for
retrieval, pruning, replay, clustering, substrate update, validation routing,
contradiction handling, and skill generation.

Implementation guide:

- Add `forest_policies`, `forest_policy_candidates`,
  `forest_policy_trials`, and `forest_policy_outcomes`.
- Define policy genomes as typed structs with bounded fields and provenance.
- Move existing `HyperParameters` under policy versioning.
- Champion/challenger trials must be deterministic, bounded, and reversible.
- Policy promotion creates a policy artifact and claim proposal.
- Local optimization can reuse existing calibration and regret code, but the
  production decision must go through the policy engine.

Legacy path to remove:

- Direct runtime promotion from simple hyperparameter perturbation.

Acceptance criteria:

- Every active policy has a version, source, validation evidence, and rollback
  policy.
- Candidate population is bounded.
- Policy trials cannot change permissions or install skills.
- Promotion requires validation-backed claim acceptance.

Tests:

- Unit happy: candidate generated, trial assigned, outcome recorded, promotion
  proposal created.
- Unit negative: invalid genome field outside bounds rejected.
- Unit edge: challenger improves one metric but harms validation pass rate;
  promotion rejected.
- Integration with mockery: mocked policy outcome source produces deterministic
  champion/challenger result.
- E2E: retrieval policy trial changes packet ranking for a bounded cohort and
  records outcome artifacts.
- Race: concurrent outcome writes do not promote two champions.
- Performance: trial assignment O(1) per request after snapshot load.

### Item 10.2: Add policy fitness tied to claims and ecology

Description and examples:

Policy quality must be measured by claim outcomes, validation pass rate,
artifact quality, contradiction reduction, latency, cost, and ecological
health.

Implementation guide:

- Add `PolicyFitness` computation over claim deltas, validation records,
  retrieval outcomes, substrate metrics, and runtime metrics.
- Use confidence intervals before promotion.
- Prevent metric gaming by requiring multiple independent signals.
- Store fitness artifacts for audits.

Legacy path to remove:

- Retrieval-only regret as sufficient policy fitness.

Acceptance criteria:

- Fitness includes at least one correctness signal, one safety signal, one cost
  signal, and one ecology signal.
- Promotion cannot occur on latency improvement alone.
- Fitness computation is replayable.

Tests:

- Unit happy: aggregate fixture outcomes into expected fitness.
- Unit negative: missing validation signal blocks promotion.
- Unit edge: small sample size keeps challenger in trial.
- Integration: real claim lifecycle fixture drives policy fitness.
- Performance: incremental aggregation avoids full history scans.

## Phase 11: Memetic Layer

### Item 11.1: Add meme extraction from successful and failed patterns

Description and examples:

Memes are reusable learned patterns with provenance. They can represent
validation bundles, remediation recipes, retrieval paths, agent collaboration
patterns, artifact templates, and negative patterns.

Implementation guide:

- Add `forest_memes`, `forest_meme_sources`, `forest_meme_lineage`, and
  `forest_meme_fitness`.
- Extract memes from validated repeated paths, recurring remediation success,
  repeated validation bundles, and repeated failures.
- Require source evidence thresholds before a meme is active.
- Store negative memes for patterns that repeatedly fail.

Legacy path to remove:

- Anti-pattern promotion as a separate special-case system.

Acceptance criteria:

- Every meme has source nodes, source artifacts or validations, fitness, and
  lineage.
- Negative memes can suppress proposals without deleting evidence.
- Meme extraction is deterministic for a fixed policy and evidence set.

Tests:

- Unit happy: repeated successful validation path creates a meme.
- Unit negative: unsupported source evidence cannot create active meme.
- Unit edge: conflicting memes remain separate until validation supports merge.
- Integration: claim outcomes over multiple sessions generate stable memes.
- Performance: extraction bounded by changed clusters and recent windows.

### Item 11.2: Add recombination, mutation, and local refinement

Description and examples:

Memetic algorithms combine high-performing memes and refine them locally.
Generated candidates may be policies, validation recipes, remediation plans,
or skill candidates.

Implementation guide:

- Add bounded recombination queues.
- Define mutation operators per meme kind.
- Define crossover operators only where schemas are compatible.
- Run local refinement through policy engine validation, not direct
  activation.
- Record rejected variants as negative evidence.

Legacy path to remove:

- One-off manual anti-pattern or replay heuristics as the only adaptation.

Acceptance criteria:

- Mutation and crossover preserve schema validity.
- Population size and generation count are bounded by policy.
- Every candidate has parent lineage and rejection or promotion reason.

Tests:

- Unit happy: two compatible validation memes recombine into a candidate.
- Unit negative: incompatible meme schemas cannot cross over.
- Unit edge: mutation produces no behavioral change and is deduplicated.
- Integration with mockery: mocked fitness evaluator accepts/rejects
  candidates deterministically.
- Race: concurrent candidate evaluation cannot create duplicate lineage.
- Performance: generation step bounded by configured population size.

## Phase 12: Skill Foundry

### Item 12.1: Add proposal-only generated skill artifacts

Description and examples:

The forest may propose new skills, but it must not install or activate them
directly. A generated skill is a typed artifact containing `SKILL.md`,
examples, fixtures, validators, safety notes, source memes, and lineage.

Implementation guide:

- Add `forest_skill_candidates`, `forest_skill_candidate_files`, and
  `forest_skill_candidate_validations`.
- Define generated artifact type `generated_skill_candidate`.
- Produce candidate file sets in a temp artifact location, not directly in
  the active skills directory.
- Attach source nodes, memes, claims, validations, rejected variants, and
  promotion rationale.
- Generate claims about utility, safety, correctness, permission behavior, and
  regression risk.

Legacy path to remove:

- Any direct skill file generation path that bypasses claims and validation.

Acceptance criteria:

- Candidate skill artifacts are inert until approved.
- Candidate includes complete lineage and validation harness.
- Candidate cannot request new permissions without explicit approval claim.
- Candidate cannot install itself.

Tests:

- Unit happy: meme set generates a valid candidate artifact manifest.
- Unit negative: candidate with missing trigger, missing validation harness, or
  permission expansion is rejected.
- Unit edge: candidate duplicates existing skill and is marked supersession or
  duplicate, not installed.
- Integration with mockery: mocked artifact writer receives file set and
  mocked claim poster receives validation claims.
- E2E: repeated successful workflow produces a proposed skill artifact and
  Guardian review claim, with no active skill installation.
- Race: two foundry workers propose same skill; deterministic candidate key
  deduplicates.
- Performance: generation bounded by source packet and meme limits.

### Item 12.2: Add skill validation harnesses and promotion gates

Description and examples:

A generated skill must prove it helps. Validation includes static safety,
permission behavior, fixture correctness, regression benchmarks, and task
quality comparison.

Implementation guide:

- Add validators for `SKILL.md` structure, trigger specificity, tool
  authority, fixture execution, and regression comparison.
- Use mockery mocks for external tool interfaces and LLM providers in e2e
  tests.
- Promotion creates a claim transition and activation request. Activation is a
  separate explicit operation controlled by Guardian or human approval.
- Track activated skill lineage and later degradation.

Legacy path to remove:

- Manual trust in generated skill text without typed validations.

Acceptance criteria:

- No skill candidate can be promoted without passing required validators.
- Permission expansion requires explicit authorization.
- Failed validation creates negative meme evidence.
- Later degradation can quarantine or retire the skill.

Tests:

- Unit happy: valid candidate passes static validators.
- Unit negative: hidden permission expansion, vague trigger, missing examples,
  or unsafe tool instruction fails.
- Unit edge: candidate useful for one role but harmful for another is
  role-scoped.
- Integration: candidate validation creates result artifacts and validation
  lifecycle records.
- E2E: approved candidate moves from proposed to accepted but still requires
  separate activation.
- Performance: validator suite has bounded fixture count and timeout.

## Phase 13: Governance

### Item 13.1: Replace implicit forest decisions with claim-backed proposals

Description and examples:

The forest may propose quarantine, pruning, policy promotion, remediation, or
skill activation. It must not silently make trusted infrastructure changes.

Implementation guide:

- Add `ForestProposalSink` interface backed by claims board generation APIs.
- Generate mockery mocks.
- Define proposal artifact types:
  - `forest_policy_promotion`
  - `forest_quarantine`
  - `forest_remediation`
  - `forest_pruning`
  - `generated_skill_candidate`
  - `forest_cluster_speciation`
- Route Guardian review based on severity, bridge risk, and permission risk.

Legacy path to remove:

- Direct promotion or suppression without claim/testament/artifact trail.

Acceptance criteria:

- Every trusted state change has a proposal artifact or accepted claim.
- Rejected proposals become negative evidence.
- Proposal idempotency prevents duplicate claims.
- Rollback path is recorded for each accepted proposal.

Tests:

- Unit happy: policy promotion proposal includes evidence refs and rollback.
- Unit negative: proposal without validation evidence rejected.
- Unit edge: duplicate proposal returns existing claim ID.
- Integration with mockery: mocked claim generator receives exact action,
  claim, validations, and artifact refs.
- E2E: outbreak triggers remediation proposal and Guardian review path.
- Race: simultaneous proposal attempts deduplicate.

### Item 13.2: Add permission and activation safety boundaries

Description and examples:

Generated policies and skills cannot expand authority by accident. The forest
must detect and block attempts to add tools, widen sandbox behavior, or alter
approval requirements.

Implementation guide:

- Add permission diffing for skill candidates and policy candidates.
- Require explicit approval claims for any authority expansion.
- Treat missing permission metadata as unsafe.
- Add quarantine for candidates that try to bypass claims, validations, or
  approvals.

Legacy path to remove:

- Trusting generated text to self-describe safety.

Acceptance criteria:

- Permission expansion is detected structurally.
- Unsafe candidate cannot enter accepted state.
- Approval records include actor, scope, expiry, and exact permission diff.

Tests:

- Unit happy: no-permission-change candidate passes safety boundary.
- Unit negative: added tool, wider filesystem, or bypass instruction fails.
- Unit edge: ambiguous permission field fails closed.
- Integration: Guardian approval claim with explicit scope allows promotion but
  not activation beyond scope.
- E2E: malicious generated skill fixture is rejected and becomes negative
  meme evidence.

## Phase 14: Validation And Test Program

### Item 14.1: Add package-level test harnesses and mock contracts

Description and examples:

The migration touches claims, forest, agents, activity store, fabric, Bleve,
VectorDB, and generated skills. Tests need shared fixtures and generated mocks.

Implementation guide:

- Add `core/forest/testfixtures` for canonical deltas, artifacts, validation
  lifecycles, node graphs, clusters, substrate fields, policies, memes, and
  skill candidates.
- Add mockery interfaces for:
  - `NeighborIndex`
  - `TextSearcher`
  - `ForestProposalSink`
  - `SkillArtifactWriter`
  - `PolicyOutcomeSource`
  - `AgentTurnProvider`
  - `GuardianApprover`
- Add compile-time mock conformance tests like
  `core/claims/mocks/operations_interfaces_test.go`.
- Add deterministic clocks and ID generators.

Legacy path to remove:

- Tests relying on broad branch fixtures as the main forest correctness proof.

Acceptance criteria:

- Every new external boundary has a mockery mock.
- Every mock has compile-time interface conformance.
- Fixtures are deterministic and reusable.
- Tests can run offline.

Tests:

- Unit: fixture builders validate required fields.
- Integration: mocks satisfy interfaces and can be used in forest integration
  tests.
- E2E: fake session harness runs without network or real LLM.
- Race: shared fixtures are immutable or cloned per test.
- Performance: fixture setup does not dominate benchmark runtime.

### Item 14.2: Add migration-wide correctness, race, deadlock, and performance gates

Description and examples:

This phase creates the test gates that every other phase must pass before
merge.

Implementation guide:

- Add replay determinism tests from ledger to graph to retrieval packet.
- Add idempotency tests for every source key type.
- Add bounded queue tests for every runtime queue.
- Add shutdown tests for every worker.
- Add integration tests with real durable board and mocked bus subscribers.
- Add e2e tests for:
  - claim to artifact to validation to forest packet
  - contradiction to outbreak to remediation proposal
  - repeated workflow to meme to skill candidate proposal
  - policy trial to promotion proposal
- Add benchmark thresholds with documented fixture sizes.

Legacy path to remove:

- Treating unit tests alone as sufficient for forest correctness.

Acceptance criteria:

- `go test ./core/forest ./core/claims ./agents/shared` passes.
- Race-tested packages touched by runtime and projection pass under
  `go test -race`.
- Every bounded queue has overflow behavior test.
- Every worker has shutdown test.
- Every projection has replay determinism test.
- E2E harness proves no generated skill is activated without approval.

Tests:

- This item is itself the test suite. It is accepted when all phase-specific
  test types exist, run in CI or documented local gates, and fail on intentional
  fixture corruption.

## Phase 15: Rollout Order And Legacy Removal

### Item 15.1: Execute replacement in strict migration slices

Description and examples:

The migration must avoid permanent dual systems. Each slice may temporarily
build the replacement beside the legacy path for comparison, but the slice is
not complete until call sites move and legacy runtime use is deleted.

Implementation guide:

Recommended slice order:

1. Runtime scope and schema gates.
2. Forest ledger.
3. Canonical delta ingestion.
4. Artifact and validation evidence records.
5. Node graph and node projector.
6. Delete branch projector runtime path.
7. Cluster topology and bridge nodes.
8. Multi-channel substrate.
9. Antigenic field and quarantine.
10. ForestPacket retrieval and agent service API replacement.
11. Cursor propagation and role projections.
12. Agency-aware forest skills.
13. Policy engine.
14. Memetic layer.
15. Skill Foundry.
16. Governance hard gates.
17. Full legacy schema archival or removal.

Legacy path to remove:

- At the end of rollout, production code must not depend on:
  - `BranchPacket`
  - `Branch` as primary memory
  - `TreeFamily` as primary ontology
  - `forest_branches`
  - `forest_relay_edges`
  - branch projector startup
  - scalar substrate truth
  - claims activity harvester
  - branch-family forest skills

Acceptance criteria:

- Each slice has a migration PR with schema, code, tests, and removal diff.
- No slice leaves a permanent compatibility fork.
- Existing user data is migrated or archived with an explicit access path.
- Rollback is by schema version and policy version, not by keeping old runtime
  code alive.

Tests:

- Integration: upgrade from current schema to latest and replay current
  fixtures.
- Integration negative: unsupported partial upgrade fails closed.
- E2E: existing session memory migrates and agent retrieval uses
  `ForestPacket`.
- Race/deadlock: upgrade and startup do not race projector startup.
- Performance: migration time and memory are bounded and documented.

### Item 15.2: Define final deletion and documentation gates

Description and examples:

The final phase removes legacy docs and code paths or marks them historical.
Architecture docs must match runtime behavior.

Implementation guide:

- Update `docs/MEMORY_FOREST.md` to point at `docs/EMERGENT_FOREST.md` as the
  active architecture or rewrite it to the new model.
- Update `docs/FOREST_FABRIC_INTEGRATION.md` to remove claims-harvester
  semantics.
- Update agent skill docs to describe cursor and packet behavior.
- Delete or archive tests that assert branch-family behavior as primary truth.
- Keep migration tests for imported legacy databases.

Legacy path to remove:

- Any documentation that tells implementers to add new behavior to branches,
  relay edges, scalar substrate, or activity claims harvesting.

Acceptance criteria:

- Docs and code agree on source of truth.
- New contributors can follow docs without discovering hidden branch runtime
  paths.
- Audit search for legacy symbols shows only migration, archive, or historical
  references.

Tests:

- Unit/docs: doc cross-reference tests similar to existing claims docs tests.
- Integration: schema audit confirms no legacy active tables are written.
- E2E: generated skill proposal flow cites current docs and validates.

## Definition Of Done For The Whole Program

- Claims deltas, artifacts, validations, fabric traversal, policies, memes,
  and skill candidates enter one append-only forest ledger.
- Node graph, clusters, substrate, immunity, retrieval packets, policies, and
  memes are projections from that ledger.
- Branches and families are no longer production ontology.
- Agents receive forest cursor and role-specific evidence packets.
- Retrieval is path, cluster, artifact, validation, contradiction, and
  quarantine aware.
- Hyper-heuristics select bounded policies through champion/challenger trials.
- Memetic algorithms extract and recombine reusable patterns with lineage.
- Skill Foundry creates proposal-only skill artifacts with validation harnesses.
- Governance controls every promotion, activation, quarantine, pruning, and
  permission expansion.
- Every async path is tracked, bounded, cancellable, and observable.
- Every phase has unit, integration, e2e, race, deadlock, negative, edge, and
  performance tests.
- No SQLite extensions are introduced.
