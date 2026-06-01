# Memory Forest

This is the implementation-facing Memory Forest guide. The active architecture
is `docs/EMERGENT_FOREST.md`; this document pins the runtime contract that
contributors should implement against.

## Active Model

The Memory Forest is an append-only evidence system with typed emergent
projections. Its source of truth is `forest_ledger` plus ledger payload/ref
tables. Queryable surfaces are projections from that ledger:

- `forest_nodes` and `forest_node_edges`
- `forest_clusters`, `forest_cluster_members`, and `forest_bridge_nodes`
- `forest_artifact_evidence` and `forest_validation_evidence`
- `forest_substrate_channels`, `forest_substrate_nodes`,
  `forest_substrate_edges`, and `forest_substrate_sessions`
- `forest_antigens`, `forest_outbreaks`, and governance quarantine rows
- `forest_policy_candidates`, policy trials, memes, and skill candidates
- `forest_cursors` and retrieval audit/accounting tables

Agents consume `ForestPacket` values. A packet contains a typed node, evidence,
counter-evidence, artifacts, validations, paths, cluster IDs, bridge risks,
quarantine state, proposed claims, scores, policy version, and cursor context.
Agents do not consume primary branch projections.

## Ingestion

All durable evidence enters through one of these paths:

- `AppendCanonicalDelta` for claims, testaments, artifacts, validations, and
  lifecycle deltas.
- `AppendLedgerRecord` for explicit forest maintenance, fabric observation,
  governance, policy, meme, and generated-skill records.
- `AppendEvent` only as the historical forest event compatibility importer; it
  writes canonical ledger rows and then projects the node graph.

The ledger is append-only. Duplicate source keys are idempotent. Unsupported
partial migrations fail closed at startup.

## Projection

The node projector consumes `forest_ledger` in sequence and writes the node
graph. Downstream projectors and maintenance jobs derive clusters, bridge
risks, substrate channel state, antigenic state, policy trials, memes, and skill
candidates from nodes and ledger records.

Runtime workers are tracked by the forest `Runtime`: every queue has bounded
capacity, every worker has a cancellation path, every lease-coordinated worker
records health, and shutdown waits for registered workers to stop.

Read-your-writes is node-projection based. Tests that need deterministic
visibility use synchronous projection; production uses the tracked node
projector.

## Retrieval

`Retrieve` and `RetrieveForest` return `[]*ForestPacket`. Retrieval filters on
`ForestNodeKind` through `Query.Kinds`, not branch families. The ranking surface
combines:

- text/semantic candidates from the node graph
- graph paths and cluster membership
- artifact and validation support
- contradiction and quarantine penalties
- bridge risk and policy constraints
- substrate channel state
- cursor context and role-specific query shape

Generic skills use `forest_recall`, `recall_recent`, and
`forest_predict_next_packets`. Role skills expose `<role>_forest_consult` and
dispatch by purpose while returning `ForestPacket`s plus a cursor.

## Governance

Trusted forest changes are proposal-backed. Policy promotion, remediation,
quarantine, pruning, cluster speciation, and generated-skill promotion must
produce governance proposal rows and proposal artifacts with:

- evidence refs
- rollback path
- permission diff when authority changes
- guardian review requirement when risk warrants it
- accepted claim or scoped approval before activation

Rejected proposals become quarantine/negative evidence. Proposal IDs are stable
and idempotent so retrying after a sink failure does not fork trusted state.

Generated skills are proposal-only until validation evidence and approval are
present. Permission expansion fails closed unless an approval claim records the
actor, scope, expiry, and exact permission diff.

## Fabric And Claims

Claims lifecycle truth enters via canonical claims deltas. Fabric observation is
a context signal path for activity consumption/resolution, consult linkage,
tool completions, and LLM completions. Fabric observation never replaces claims
delta ingestion for claim, testament, artifact, or validation lifecycle truth.

See `docs/FOREST_FABRIC_INTEGRATION.md` for the active fabric observer path.

## Health

`Health()` reports subsystem state, schema hash integrity, node-projection spot
checks, runtime queues/workers, substrate state, page-rank cache state, and
retrieval latency percentiles. Spot checks sample `forest_nodes` and verify
node ontology plus ledger source linkage for projected rows.

## Legacy Removal

The active runtime does not depend on primary branch projections, relay-edge
projection tables, scalar substrate truth, claims activity harvesting, or
branch-family skills. Historical adapters may exist only for import, migration,
or archived tests; new behavior must target the ledger, node graph,
`ForestPacket`, `ForestNodeKind`, governance proposals, and typed projections
listed above.
