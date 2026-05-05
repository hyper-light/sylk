# Emergent Forest

A successor design to docs/MEMORY_FOREST.md that re-grounds the forest on
interaction-nodes, lets topical trees emerge from density clustering, and
upgrades the Substrate Network to a richer biological model.

## Status and Relation to MEMORY_FOREST.md

MEMORY_FOREST.md describes nine declared facet-trees (Intent, Constraint,
Evidence, Decision, Outcome, Preference, Capability, Opportunity, Conflict),
a scalar conductance substrate inspired by Physarum, pure Hebbian
reinforcement, and disuse-driven pruning. It is structurally sound but
coarse in three ways:

1. The tree taxonomy is declared, not discovered. Topics — what users
   actually think in — never appear as first-class structure.
2. The Substrate Network carries a single signal (conductance), so every
   form of attention pressure collapses onto one axis.
3. Reinforcement and pruning are biologically thin: pure Hebbian has no
   homeostat, and Physarum disuse-pruning has no notion of competition or
   developmental stage.

The Emergent Forest keeps the existing CQRS event-sourced runtime, the
ledger-as-source-of-truth invariant, the ACT-R warmth layer, the
agent-facing skill surface, the Branch Packet contract, and the storage
substrate (UniversalContentStore + Bleve + SQLite + VectorGraphDB). It
replaces the declared facet-tree schema with an emergent topical structure
grown from interaction-nodes, and it replaces scalar conductance with a
multi-channel substrate that uses BCM homeostasis, Turing-style lateral
inhibition, and developmental staging.

## Goal

The agent should see, through the substrate, where the user's interests
have actually grown — not where the schema says they should live. As more
interactions happen, more trees take root, and the agent gains a
human-decipherable map of what the user cares about and how those cares
relate.

The primary output is the same as MEMORY_FOREST.md:

> what most helps this agent advance the user's intent right now

What changes is *how* that surface is computed: structure is read off the
live interaction graph rather than maintained as a parallel projection of
declared types.

## Core Inversion: From Declared Schema to Emergent Structure

### What Changes

- **Interactions are nodes.** The unit of substance is the interaction
  itself: a user query, an agent reply, a tool result, a citation, a
  validation, a contradiction. Each is a node in the forest.
- **Trees are density clusters of interaction-nodes.** A tree is a
  population of nodes whose embeddings cluster, whose temporal ordering
  tells how the topic developed, and whose density determines vigor.
  Trees are not declared; they crystallize when density is sufficient.
- **Branches are paths through interaction-nodes inside a tree.** A
  branch is a coherent sub-trajectory — for instance, the chain of
  interactions in which a particular hypothesis was raised, refined,
  validated, and adopted.
- **Facets become interaction kinds.** The nine declared facet-trees in
  MEMORY_FOREST.md reduce to *kinds* of interaction: a question is an
  intent-kind interaction, a tool result is an evidence-kind interaction,
  a chosen-path response is a decision-kind interaction. Same node,
  intrinsic kind. No separate facet projection.
- **The Relay Graph collapses into bridge nodes.** A node density-reachable
  to two cluster cores *is* a relay. Cross-tree connections are the literal
  interactions in which two topics genuinely linked. No separate relay
  layer.

### What Stays

- The append-only event ledger remains the source of truth. Every
  interaction-node is materialized from a ledger event (or set of events).
- The CQRS projector model is unchanged in shape; what it projects expands
  from `forest_branches` to also include `forest_nodes`,
  `forest_clusters`, and substrate state. The lease, the watermark, the
  poison-pill counter, and the panic recovery all transfer directly.
- Branch Packets remain the primary retrieval product. Their contents now
  derive from interaction-graph neighborhoods rather than declared
  branch-rows, but the contract (identity, summary, evidence, conflicts,
  next actions, scoring) holds.
- The agent skill surface (`forest_resolve_intent`, `forest_recall`,
  `forest_predict_next_branches`, etc.) is preserved; only the
  implementation under each skill changes.
- Soil, Ledger, Warmth, Replay, and the learned reranker survive intact.

## The Forest as an Interaction Graph

### Interaction-Nodes

Each node carries:

- `node_id` — UUIDv4
- `event_id` — back-reference to the originating ledger event(s)
- `kind` — the interaction kind (intent, evidence, decision, validation,
  contradiction, query, refinement, fork, citation, outcome, preference,
  capability, …). Open-ended set; a small core is canonical, with
  extensions earning promotion only after sustained adoption.
- `valence` — signed scalar: validations positive, contradictions
  negative, queries neutral, refinements signed by the refinement's
  effect.
- `actor` — which agent or the user produced it.
- `embedding` — high-dimensional vector. Hierarchical where appropriate
  (a sentence-level vector for the gist, plus richer vectors for any
  code/citation content).
- `created_at` — ledger timestamp; used in decay and staging.
- `provenance` — pointers into UniversalContentStore for raw bytes.
- `interactions` — typed edges to other nodes (see below).
- `cluster_memberships` — soft membership weights over the top-K trees
  the node currently belongs to (K ≈ 3, weights renormalized to 1).
- `signature` — a learned vector over interaction-kind history that
  describes what role this node has played; readout, not a stored type.

### Edges

Edges are themselves first-class interactions and carry typed
relationships between nodes:

- `responds_to` — this answer-node responds to that question-node.
- `validates` — positive, supports.
- `contradicts` — negative, opposes.
- `refines` — modifies the target while preserving its identity.
- `forks_from` — branches off the target; both descendants persist.
- `cites` — references for evidence.
- `co_activated_with` — temporal/topical co-occurrence within a window.
- `defers_to` — authority handoff, e.g., a Guardian-class judgment.
- `supersedes` — replaces an earlier decision.

Edges carry weight, valence, and timestamp. Decay applies per-edge.

### Trees as Density Clusters

A tree is the runtime view of a high-density region in interaction space.
It is identified by:

- `cluster_id` — persistent ID assigned at crystallization.
- A representative **sample** of member nodes (centroid alone is
  unreliable in dense or multi-modal regions; sample preserves
  multimodality and lets soft membership remain meaningful).
- A **density profile** — how connected the cluster's interior is.
- A **boundary** — implicit, defined by where density-reachability stops.
- A **vigor metric** — derived from member-node stage distribution.
- A **name** — assigned by a curator agent on speciation; gate to
  promotion (see *Naming as Gate*).

Trees are not stored as authoritative fact; they are projected and
reprojected from the live node population. The projector keeps a
materialized table for fast retrieval, but its contents are
deterministically rebuildable from the node graph.

### Branches as Paths

A branch is a directed path or DAG through interaction-nodes within a
tree, with internal coherence: same actor, same sub-thread, same
hypothesis chain, etc. Branches are computed on demand from the
interaction graph rather than stored as primary structure. The Branch
Packet returned to an agent hydrates the path's nodes, summary, and
provenance from the underlying interaction-nodes.

### Bridges Replace the Relay Graph

A node whose density-reachability extends into two or more cluster cores
is a **bridge**. Bridges are the literal places where topics connected in
the actual conversation. The Relay Graph from MEMORY_FOREST.md
collapses into "the bridge nodes of the interaction graph" plus the
edges incident on them. Cross-tree activation, cross-tree reinforcement,
and cross-tree retrieval all flow through bridges.

## Mathematical Foundations

### Online Density Clustering

The clusterer is a streaming variant of HDBSCAN. The relevant primitives:

**Core distance.** For a node `p` and a parameter `m_pts`, the core
distance `core_d(p)` is the distance from `p` to its `m_pts`-th nearest
neighbor in node-embedding space. This is a local density estimate:
small `core_d` means `p` lives in a dense neighborhood.

**Mutual reachability distance.** For two nodes `a` and `b`:

```
d_mreach(a, b) = max( core_d(a), core_d(b), d(a, b) )
```

where `d(a, b)` is the embedding distance (cosine for normalized vectors,
or Euclidean for raw). This metric pulls dense points together while
keeping sparse points apart, which is what gives HDBSCAN its tolerance
for variable-density clusters.

**Density-reachability.** `b` is density-reachable from `a` if there is a
chain `a = x_0, x_1, …, x_k = b` such that every consecutive pair has
`d_mreach < ε` for some local `ε` derived from the cluster's density
profile. Two nodes in the same cluster are density-connected (each
density-reachable from a common third node).

**Streaming update.** On node arrival:

1. Embed the node and insert into the spatial index (Sylk's
   `core/vectorgraphdb/vamana` — a DiskANN-style k-NN graph — is the
   natural substrate; it's already persisted, mmap'd, and integrated
   with IVF partitioning).
2. Compute `core_d` from the `m_pts` nearest neighbors.
3. Determine cluster membership by density-connectedness to existing
   members. If unreachable from any cluster, mark the node as noise.
4. Periodic compaction: scan noise pool; if a noise region has reached
   density threshold, crystallize a new cluster.
5. Periodic cohesion check: for each cluster, verify density-connectedness
   of the interior; if a density valley has formed (decay weakened
   bridging nodes), split.
6. Periodic merge check: if two cluster cores have become
   density-connected via newly arrived bridge nodes, merge.

Speciation, merge, and split fall out of the algorithm; they are not
separate gates.

### Embedding and Spatial Index

- **Embedding.** Nodes are embedded at write time using the active model
  generation. Hierarchical: sentence-level for the gist (256–768-d),
  optionally augmented with a separate code embedding for nodes carrying
  code, and a citation embedding for nodes carrying external references.
  The clusterer operates on the gist vector by default; specialty
  embeddings are used in retrieval scoring and bridge identification.
- **Index.** Sylk's existing `core/vectorgraphdb/vamana` — a DiskANN
  Vamana graph (`RobustPrune` + `GreedySearch`) layered with IVF
  k-means partitioning (`core/vectorgraphdb/vamana/ivf`) and BBQ
  quantization. Updates are incremental on node arrival. Queries
  return k-NN for clustering and retrieval. The forest's clusterer
  is a thin layer over this substrate, not a separate index — see
  *Clustering Algorithm Choice* in Open Design Questions.
- **Generation.** Each node's embedding is tagged with the model
  generation that produced it. Nodes from prior generations remain
  immutable; if the active model rotates, prior-generation nodes are
  re-embedded only during an explicit, tracked re-clustering event (see
  *Open Design Questions*).

### Decay

Each edge and each node-kind contribution decays under a power law:

```
w(t) = w_0 · (1 + t / τ_k)^(-β_k)
```

where `t` is time since last event involving the edge, and `τ_k`, `β_k`
are kind-specific shape parameters. Power-law (vs exponential) preserves
recently-relevant items more aggressively while still flattening over
long horizons — the Acthar curve already used elsewhere in Sylk.

Per-kind shape parameters reflect epistemic asymmetry:

- Validations decay slowly. A validated claim stays validated.
- Queries / co-activations decay quickly. They are about *now*.
- Contradictions never fully decay. They are load-bearing for the
  contradicted node's history; they cap their target's activation
  asymptote rather than disappearing.
- Citations decay slowly while the source remains accessible.

### Resource Economy

The substrate carries multiple resource channels rather than a single
conductance. Channels are not biology metaphors imported wholesale; they
are what scoring already depends on. Reasonable starting set:

- **Carbon** — evidence/support density. Produced by evidence-kind nodes
  and citations.
- **Nitrogen** — correctness/constraint pressure. Produced by validations
  and Guardian-class constraints; consumed by claims under scrutiny.
- **Phosphorus** — intent salience. Produced by user queries and
  refinements; consumed by candidate branches under canopy resolution.
- **Water** — recency / accessibility. Decays continuously; replenished
  by any access.

Edges have asymmetric exchange ratios per channel. A validation edge
moves nitrogen from validator to validated and a small amount of carbon
from validated to validator (the validation acquired its own evidence
weight by attaching to a real claim). Contradictions drain nitrogen
sharply and may reroute it to a forked descendant. Co-activation edges
move water symmetrically.

A node *starves* when any essential channel drops below threshold; that
state contributes to staging transitions and pruning. Crucially,
"essential" is kind-dependent: an evidence-kind node starves on carbon
loss; an intent-kind node starves on phosphorus loss.

## Substrate Dynamics

### BCM with Sliding Threshold

Pure Hebbian (`Δw ∝ x_pre · x_post`) has no upper bound. Bienenstock–
Cooper–Munro fixes this with a sliding threshold:

```
Δw_ij  ∝  φ(y_j, θ_M(j)) · x_i
φ(y, θ_M)  =  y · (y - θ_M)
θ_M(j)  =  ⟨ y_j² ⟩_τ           (running average of post-synaptic activity)
```

`y_j` above `θ_M` produces LTP (strengthening); `y_j` below produces LTD
(weakening). `θ_M` slides with the post-synaptic node's recent activity,
so a node that *used to be* hot but stopped firing actively weakens its
incoming edges, rather than merely decaying. This gives targeted
forgetting and prevents runaway positive feedback.

For Sylk: `y_j` is the post-event activation of node `j` (a function of
incoming edge activity weighted by event valence and kind), and `θ_M(j)`
tracks `⟨y_j²⟩` over a configurable τ.

### Synaptic Scaling

Total inbound edge weight on any node is normalized to a soft budget:

```
∑_i w_ij  →  w_ij · ( B / ∑_i w_ij )       when ∑_i w_ij > B
```

This caps total reinforcement and forces the network to choose what to
invest in. Strengthening one input causes proportional weakening of the
others *only when over budget* — under budget, BCM operates freely.

### Reaction-Diffusion (Turing Patterns)

For each resource channel, pair an activator `A` and an inhibitor `I` on
the relay neighborhood graph:

```
∂A / ∂t  =  D_A · ∇²A  +  f(A, I)
∂I / ∂t  =  D_I · ∇²I  +  g(A, I)            with  D_I  ≫  D_A
```

`f` and `g` are local reaction terms; `∇²` is the graph Laplacian over
node neighborhoods. Short-range activator + long-range inhibitor produces
Turing patterns: neighborhoods of co-supported nodes with sharp boundaries
against weaker neighborhoods.

Effect on pruning: nodes don't merely die from disuse; they get
*outcompeted* by lateral inhibition emanating from a stronger neighbor in
the same niche. This is the structured pruning the prior design lacked.

In practice, Sylk does not run continuous PDE integration. The
maintenance loop performs periodic relaxation of the reaction-diffusion
field over the active subgraph (the canopy plus its k-hop neighborhood),
amortizing cost. Per-event updates do incremental local diffusion; the
full relaxation reconciles drift on a slower cadence.

### Allelopathy

Generic lateral inhibition is symmetric and topical. Allelopathy is
asymmetric and authority-driven: certain nodes (Guardian-class
constraints, hard policy boundaries) emit broad negative inhibition that
suppresses growth around them regardless of topical similarity. Same
substrate primitive (broadcast on the inhibitor channel), different
parameters: longer range, higher amplitude, asymmetric — only the source
emits, never receives.

Effect: Guardian governance, currently a ranking-time layer in
MEMORY_FOREST.md, partly descends into the substrate. Branches that
would violate constraints get suppressed *before* they grow, not just
filtered at retrieval.

### Hebbian Association With Valence

The original MEMORY_FOREST.md Hebbian rule strengthens edges that
co-fire. The valenced version:

```
Δw_ij  ∝  v_e · x_i · x_j
```

where `v_e ∈ {-1, 0, +1}` is the valence of the precipitating event.
Validations strengthen; contradictions weaken; queries leave existing
weights untouched. BCM and synaptic scaling apply on top of this base
rule.

## Lifecycle

### Node Stages

Each interaction-node passes through stages with different plasticity:

| Stage | Plasticity | Substrate role |
|---|---|---|
| **Pioneer** | High | Fresh; cheap to overwrite; counts toward candidate-cluster crystallization. |
| **Sapling** | Moderate | Validated once or referenced again; reconsolidation easy. |
| **Mature** | Low | Repeatedly validated; contributes durably to cluster cohesion. |
| **Climax** | Near-frozen | Long-stable, repeatedly re-validated. Effectively only Guardian-class events rewrite. |
| **Snag** | Inert | No longer referenced. Preserved (history is immutable) but contributes negligible density. |

Transitions are *derived*, not maintained: a query asks "is this node
above the Mature threshold on (validation count, kind diversity, recent
activation rate)?" and reads off the band.

A **critical period** reopens — moving a Climax node back to Mature
plasticity — when sustained contradiction load above threshold arrives,
when a major intent revision occurs in a containing tree, or under
explicit user-initiated review.

### Tree Succession (Emergent)

Tree-level staging is a function of the population's stage distribution,
not a separate state machine:

- **Pioneer-dominated tree** — most nodes are pioneers; topic actively
  growing.
- **Mature** — steady pioneer arrival on top of a mature/climax core;
  healthy active topic.
- **Climax** — mostly mature/climax nodes, low pioneer arrival rate;
  stable, well-explored topic.
- **Senescent** — pioneers stopped arriving, existing nodes aging;
  topic dying.
- **Dead** — substantially snags; tree persists for retrieval but no
  longer grows.

No tree-staging logic separate from node-staging. Read it off.

### Speciation, Merge, Split (Emergent)

- **Speciation.** Noise nodes accumulating density crystallize into a
  new cluster. The clusterer asks at compaction: does this noise region
  contain a density-connected subgraph above threshold? If yes,
  promote.
- **Merge.** Two cluster cores connected by a new bridge node that makes
  them density-connected. The merge preserves both former IDs as
  ancestors; the merged cluster carries a fresh ID.
- **Split.** Decay weakens bridging nodes inside a cluster. A density
  valley forms. The cluster fissions; daughter clusters carry fresh
  IDs and reference the parent.

All three are detected by periodic structural checks against the live
density field. Heavy operations; run at maintenance cadence, not
per-event.

### Naming as Gate

Speciation produces an unnamed candidate cluster. A curator agent
(typically Archivalist) samples representative nodes and proposes a 2–5
word topic name. **A cluster is not a first-class tree until naming
succeeds.**

This makes human-decipherability a substrate-level guarantee rather than
a hope. Unnamed candidate clusters remain in a holding pen, retrievable
by interaction proximity but not surfaced as named topics in any
agent-facing view.

Names drift. Re-naming on substantial composition change is a curator
responsibility. Old names retain as aliases for retrieval continuity.

## Points of Interest

Once interactions are first-class, the substrate exposes structural
queries that summarize what's happening in the forest. These are not
stored fields; they are queryable views computed from interaction
topology, edge valences, and decay state.

### View Catalog

- **Hot zones.** Regions where interaction arrival rate is spiking inside
  the current canopy. Detected by a sliding-window count of new nodes
  per cluster, normalized by the cluster's historical baseline.
- **Boundary zones.** Sub-regions where contradicting interactions are
  converging — open disagreement, unresolved fork. Detected by clusters
  of negative-valence edges with overlapping endpoints.
- **Keystones.** Nodes with disproportionate downstream reach: removing
  them would collapse identifiable neighborhoods. Detected by
  betweenness-centrality variants on the interaction graph.
- **Frontier.** The leading edge of recent node arrival around a cluster
  — where the topic is currently extending. Useful for predictive
  planning and proactive evidence gathering.
- **Brittle.** Nodes with heavy downstream dependence whose supporting
  interactions have decayed. Detected by mismatch between in-degree
  weight (high) and recent-activation rate (low). Replay-priority
  targets.
- **Bridges.** Nodes density-reachable to multiple cluster cores.
  Surfaced for cross-tree retrieval and relay reinforcement.
- **Underused gold.** Mature-or-Climax nodes with strong signature
  vectors but decayed recent rate. Replay-priority targets distinct from
  Brittle.

### Computation

PoI views compose with canopy resolution as a step zero in retrieval:
identify hot zones / boundaries / keystones / frontier / brittle / bridges
in the canopy region *before* gathering candidates. Interaction topology
is the cheapest first-pass filter for where to even look. Substrate
diffusion and reaction-diffusion patterns reinforce or suppress PoI
candidates as part of their normal operation; PoI computation reads the
field.

## Retrieval Reformulation

### From Branch Packets to Node Subgraphs

Branch Packets (the agent-facing contract) survive, but their internal
structure shifts. Where the prior design hydrated a Branch Packet from a
declared `forest_branches` row plus its provenance, the Emergent Forest
hydrates a Branch Packet from:

- A coherent path or sub-DAG of interaction-nodes.
- The cluster (named tree) containing the path.
- The PoI markers covering the region (hot / boundary / keystone /
  frontier / brittle / bridge).
- The signature readout giving the path's predicted facet roles.

### Steps

The retrieval pipeline becomes:

1. **Compute PoI views** for the current canopy region. Cheapest first
   filter: interaction-topology features.
2. **Resolve canopy** as in MEMORY_FOREST.md, but over named clusters
   rather than declared facet-trees.
3. **Gather candidate nodes** by spatial-index k-NN around active
   queries, augmented by canopy proximity and PoI markers.
4. **Build paths** through candidate nodes within their containing
   clusters.
5. **Score paths** with the existing two-stage SIMD base scorer + learned
   reranker. Feature set extends to include interaction-topology
   signals (PoI markers, signature, bridge-membership).
6. **Hydrate** top paths into Branch Packets including PoI annotations.
7. **Reinforce** returned Branch Packets in the warmth layer; emit
   recall events to the ledger.

### Returned Shape

Branch Packets carry the same contract as MEMORY_FOREST.md plus:

- `signature_readout` — predicted facet probabilities from the path's
  interaction history.
- `poi_markers` — which PoI views the path participates in.
- `bridge_neighbors` — for bridge paths, the other clusters reachable
  via this path.
- `cluster_lineage` — speciation/merge/split history of the containing
  cluster, for Archivalist-style provenance reasoning.

## Architecture

### Storage Layer

Tables added or extended (under the same SQLite database as the existing
forest schema):

| Table | Role |
|---|---|
| `forest_nodes` | Interaction-node projection. PK `node_id`. Holds embedding ref, kind, valence, actor, created_at, signature, last_applied_seq. |
| `forest_node_edges` | Typed, weighted, signed edges between nodes. PK `(src, dst, kind)`. Decay state per edge. |
| `forest_clusters` | Named tree projection. PK `cluster_id`. Holds representative samples, density profile, vigor, name, lineage, last_applied_seq. |
| `forest_cluster_membership` | Soft membership weights. PK `(node_id, cluster_id)`. Renormalized to ≤ K weights per node. |
| `forest_substrate_channels` | Per-edge resource channel state (carbon/nitrogen/phosphorus/water). Decayed in place by maintenance. |
| `forest_substrate_field` | Reaction-diffusion field samples per cluster region; periodically relaxed. |
| `forest_node_stage` | Optional materialization of derived stage; rebuildable from node + edge state. |
| `forest_poi_cache` | Optional materialization of PoI views; rebuildable. |
| `forest_cluster_lineage` | Speciation/merge/split events; append-only. |

Existing tables (`forest_events`, `forest_event_seq_log`,
`forest_projector_state`, `forest_branches`, `forest_relay_edges`,
`forest_canopies`, `forest_substrate_sessions`, `forest_branch_traces`,
`forest_replay_queue`, `forest_training_examples`, `forest_models`)
remain. `forest_branches` and `forest_relay_edges` continue to exist as
*derived caches* during the transition; once the interaction-node graph
is stable, they can be deprecated.

### CQRS Integration

The existing single-leader projector model extends cleanly. The branch
projector remains. Two new projectors join it:

- **Node projector.** Consumes ledger events that produce or modify
  interaction-nodes and edges. Single-leader, lease-coordinated, same
  watermark mechanics. Writes `forest_nodes`, `forest_node_edges`,
  `forest_substrate_channels`.
- **Cluster projector.** Consumes the node projector's downstream signal
  (a derived event stream emitted on cluster-affecting changes) to
  maintain `forest_clusters`, `forest_cluster_membership`,
  `forest_cluster_lineage`. Heavier than the node projector; runs at
  larger batch granularity.

Both inherit the existing concurrency invariants: source of truth never
read-modify-written, lease-gated watermark, idempotent replay, panic
isolation, bounded poison-pill retries, multi-process safety.

### Online Clusterer Placement

The clusterer is *not* in the projector hot path. It runs as a tracked
maintenance goroutine that reads the recent node arrivals queue and
performs:

- **Per-event work** (cheap, on arrival): embed; insert into the
  Vamana graph + IVF; compute `core_d` from Vamana's k-NN
  neighborhood; assign tentative membership by density-reachability
  to existing clusters.
- **Periodic compaction** (maintenance cadence): scan noise pool;
  crystallize new clusters from dense noise regions.
- **Periodic cohesion check** (maintenance cadence): split clusters
  with internal density valleys.
- **Periodic merge check** (maintenance cadence): merge cluster pairs
  newly density-connected via bridge nodes.
- **Substrate relaxation** (maintenance cadence): reaction-diffusion
  field relaxation over the canopy and its k-hop neighborhood.

All cluster-affecting operations emit events into the ledger; the cluster
projector consumes those events to update the projection.

### Maintenance Loop

```
                 ┌──────────────────────────┐
                 │  Maintenance Goroutine    │
                 │  (tracked on m.wg)        │
                 └────────────┬──────────────┘
                              │ ticks
                              ▼
       ┌──────────────────────────────────────────────┐
       │  cycle:                                      │
       │    decay sweep (per-kind power law)          │
       │    BCM threshold update (sliding average)    │
       │    synaptic scaling (over-budget normalize)  │
       │    substrate relaxation (Turing pairs)       │
       │    cluster compaction (noise → clusters)     │
       │    cluster cohesion (split check)            │
       │    cluster merge check                       │
       │    PoI view recompute                        │
       │    replay scheduler                          │
       │    ecology pruning (snag transition)         │
       └──────────────────────────────────────────────┘
```

Each subroutine emits ledger events for any state-changing decision.
Idempotency holds at every step: re-running a cycle produces the same
outcome up to numerical tolerance.

## Diagrams

### Layer Stack

```
┌─────────────────────────────────────────────────────────────┐
│  Agents (Academic / Librarian / Archivalist / Engineer /    │
│         Designer / Guardian / Scribes / Orchestrator)       │
└────────────────────────────┬────────────────────────────────┘
                             │ skill calls
┌────────────────────────────▼────────────────────────────────┐
│  Forest Skill Surface                                       │
│   forest_resolve_intent / forest_recall /                   │
│   forest_predict_next_branches / forest_record_outcome /    │
│   forest_get_constraints / forest_get_conflicts /           │
│   forest_get_preference_prior / forest_get_capability_prior │
│   forest_explain_recommendation                             │
└────────────────────────────┬────────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────────┐
│  Retrieval Pipeline                                         │
│   PoI views → canopy → candidates → paths → score → hydrate │
└────────────────────────────┬────────────────────────────────┘
                             │ reads from
┌────────────────────────────▼────────────────────────────────┐
│  Projections (CQRS, eventually consistent)                  │
│                                                             │
│   ┌──────────────┐  ┌──────────────┐  ┌────────────────┐    │
│   │ forest_nodes │  │forest_node_  │  │forest_clusters │    │
│   │              │  │   edges      │  │                │    │
│   └──────────────┘  └──────────────┘  └────────────────┘    │
│                                                             │
│   ┌──────────────┐  ┌──────────────┐  ┌────────────────┐    │
│   │ substrate_   │  │ substrate_   │  │ poi_cache      │    │
│   │  channels    │  │  field       │  │                │    │
│   └──────────────┘  └──────────────┘  └────────────────┘    │
└────────────────────────────┬────────────────────────────────┘
                             │ projected from
┌────────────────────────────▼────────────────────────────────┐
│  Ledger (append-only, source of truth)                      │
│   forest_events  +  forest_event_seq_log                    │
└────────────────────────────┬────────────────────────────────┘
                             │ records
┌────────────────────────────▼────────────────────────────────┐
│  Soil (UniversalContentStore: raw evidence, immutable)      │
└─────────────────────────────────────────────────────────────┘
```

### Data Flow on Interaction Arrival

```
event arrives
    │
    ▼
appendEventLedger ──► forest_events  +  forest_event_seq_log
    │                       │
    │                       ▼
    │               seqNotify.Advance
    │                       │
    └──► Node Projector consumes ──► embed node
                                          │
                                          ▼
                                  Vamana insert + core_d
                                          │
                                          ▼
                              tentative density-reach assignment
                                          │
                                          ▼
                              upsert forest_nodes / edges /
                                  substrate_channels
                                          │
                                          ▼
                              emit cluster-affecting event(s)
                                          │
                                          ▼
                       Cluster Projector consumes (batched) ──►
                       forest_clusters / membership / lineage

                                          │
                                          │  (asynchronous)
                                          ▼
                       Maintenance Loop:
                          decay sweep, BCM, scaling,
                          substrate relaxation, compaction,
                          cohesion, merge, PoI, replay
```

### Node Lifecycle

```
              (new event)
                   │
                   ▼
              ┌─────────┐
              │ Pioneer │
              └────┬────┘
                   │  validation / reuse
                   ▼
              ┌─────────┐
              │ Sapling │
              └────┬────┘
                   │  repeated validation
                   ▼
              ┌─────────┐
              │ Mature  │
              └────┬────┘
                   │  long stability + re-validation
                   ▼
              ┌─────────┐                  contradiction
              │ Climax  │ ─── critical ──► back to Mature
              └────┬────┘     period
                   │
                   │  ceased reference
                   ▼
              ┌─────────┐
              │  Snag   │  (preserved, inert)
              └─────────┘
```

### Cluster Lifecycle

```
              noise nodes
                   │  density threshold
                   ▼
              ┌──────────────────┐
              │ Candidate seed   │
              └────────┬─────────┘
                       │  curator names
                       ▼
              ┌──────────────────┐
              │ Tree (named)     │ ◄────┐
              └────────┬─────────┘      │
                       │                │  density-connected
       ┌───────────────┼────────────┐   │  via bridge node
       │               │            │   │
       │               ▼            │   │
       │        density valley      │   │
       │               │            │   │
       │               ▼            │   │
       │        ┌─────────┐         │   │
       │        │  Split  │ ──── two daughters
       │        └─────────┘         │
       │                            │
       │                            ▼
       │                       ┌─────────┐
       └────────────────────►  │  Merge  │
                               └─────────┘
                                    │
                                    ▼
                            (new tree, both
                             former IDs as
                             ancestors)
```

## Departures From MEMORY_FOREST.md

### What Compresses Away

- The nine declared facet-trees → interaction kinds on nodes.
- The Relay Graph → bridge nodes in the interaction graph.
- The branch projection as primary structure → branches as queryable
  paths in the interaction graph; `forest_branches` becomes a derived
  cache.
- Scalar conductance → multi-channel resource economy.
- Pure Hebbian → BCM with sliding threshold + synaptic scaling.
- Disuse-only pruning → competition + resource starvation + LTD +
  density-valley split.

### What Survives

- CQRS projector model with lease coordination and watermark mechanics.
- Event ledger as source of truth.
- ACT-R warmth as a learned retrieval-utility signal layered over the
  substrate.
- Branch Packets as the agent-facing retrieval contract.
- The two-stage scorer (deterministic SIMD base + learned reranker).
- The agent-facing skill surface.
- Replay scheduler and reconsolidation discipline.
- Guardian governance, with a portion descending into the substrate via
  allelopathy.

### What Gets Added

- Online density-clustering subsystem (HDBSCAN-family).
- Multi-channel substrate state and reaction-diffusion relaxation.
- BCM + synaptic scaling on edge weights.
- Developmental staging at node and tree levels (derived).
- PoI views as a step-zero retrieval primitive.
- Cluster lineage as first-class provenance.
- Curator-naming as a gate to first-class tree status.

## Open Design Questions

These are points where the design meaningfully forks. None are ripe for
unilateral resolution.

### Embedding Generation Stability

The clusterer's structure is a function of the embedding model. If the
model rotates, every node's position shifts and the entire forest's
cluster structure can rearrange. Two viable stances:

- **Generation immutability.** Old nodes keep old embeddings; new model
  = new generation = new fresh forest grown alongside, with manual or
  curated migration of climax-stage knowledge across the boundary.
  Cleaner, but creates a discontinuity.
- **Periodic full re-embedding + re-clustering.** A heavyweight
  maintenance event that re-embeds all nodes under the active model and
  re-runs the clusterer from scratch. Honest about how embedding tech
  evolves, but expensive and risks structural drift the user didn't
  request.

Recommendation: default to generation immutability with explicit
migration; reserve full re-clustering for explicit operator action.

### Density Bandwidth

The clusterer's distance threshold for density-reachability controls
everything. Too tight → micro-trees fragment every conversation; too
loose → mega-tree absorbs everything.

This probably needs to be:

- **Adaptive per region.** Technical-code regions tolerate tighter
  clustering than open-ended-discussion regions.
- **Adaptive per user.** A tightly-focused user produces denser
  populations than an exploratory one.

How to estimate? Local kth-neighbor distance is the standard signal in
HDBSCAN; the question is whether to compute it once and freeze
(stable but stale as the forest grows) or recompute periodically
(expensive but accurate).

### Cold Start Honesty

The first ~50 interactions are mostly noise. There isn't enough density
anywhere to crystallize trees yet. Options:

- **No fake trees.** Retrieval falls back to raw embedding-nearest-
  neighbor over the node pool until structure emerges. Honest but
  feature-thin during onboarding.
- **Pre-seed with coarse priors.** Heuristic clusters from agent type
  (code / research / design / etc.). Faster perceived value, but biases
  the forest's eventual structure.

Recommendation: bare-ground default; optional user-hint pre-seeding for
power users who explicitly want it.

### Clustering Algorithm Choice

This is largely settled by what's already in `core/vectorgraphdb`.
The clusterer is **density-reachability over Vamana, sub-partitioning
IVF cells**, not a separately-imported algorithm. The mapping:

| HDBSCAN-family primitive | Existing sylk substrate |
|---|---|
| k-NN spatial index | Vamana graph (DiskANN: `RobustPrune` + `GreedySearch`) |
| Coarse partitioning | IVF k-means (`core/vectorgraphdb/vamana/ivf`) |
| `core_d(p)` | Distance to the `m_pts`-th Vamana neighbor — read off the existing graph |
| `d_mreach(a, b)` | `max(core_d(a), core_d(b), edge_weight(a,b))` over Vamana edges |
| Density-connected component | BFS over Vamana edges where `d_mreach < ε`, traversing within and across IVF partitions |
| "Cluster needs re-evaluation" trigger | IVF's `DriftRatio` (`core/vectorgraphdb/vamana/ivf/maintenance.go`) |
| Cluster-imbalance signal | `PartitionSizeGini` + `PartitionSizeCV` |
| Substrate health for retrieval | `ConnectivityRatio` + `DegreeFillRate` |
| Memory headroom for many nodes | BBQ quantization (`bbq.go`) |
| Persistence | `storage.MmapRegion` + posting-list serialization |

**Forest cluster = a density-connected component**, typically a
sub-region of one IVF cell, but spanning multiple cells when bridge
nodes connect them. The crossing point *is* the bridge.

What we add on top of Vamana+IVF (small, ~500 LOC):

1. Per-node `core_d` cache, recomputed when the Vamana neighborhood
   changes around a node.
2. Density-reachability BFS — pure traversal over the existing
   adjacency lists, no new storage.
3. Connected-component IDs persisted as `forest_cluster_membership`
   (separate from IVF partition IDs — IVF is the coarse layer, the
   forest cluster is the fine-grained view).
4. Hierarchy / lineage tracking (HDBSCAN's tree of clusters)
   recorded in `forest_cluster_lineage`.
5. Speciation/merge/split detection driven by the existing IVF
   maintenance signals: `DriftRatio` rising → re-evaluate cohesion;
   `PartitionSizeGini` rising → check for splits; new bridge
   density-connecting two clusters → check for merge.

This collapses the original "Online HDBSCAN vs DBSTREAM vs CluStream
vs custom hybrid" decision: the substrate is Vamana+IVF, the
clusterer is the density layer on top, and the maintenance signals
are the existing IVF telemetry.

### Interaction Kind Taxonomy

Open-ended is faithful but operationally messy: signature space drifts,
classifiers degrade, the projector's serialization grows.

- **Closed core (~10 kinds).** Stable signature space, easier to
  reason about, easier to score. Risks coercing genuinely-new kinds
  into wrong buckets.
- **Closed core + extensions earning promotion.** A kind starts as a
  free-form string; once enough nodes share it and signatures stabilize,
  it earns first-class status. Operationally harder, more honest.

Recommendation: closed core with extensions. The promotion rule needs
specification.

### Per-Kind Decay Parameters

`τ_k` and `β_k` per kind are tunable. Reasonable starting values:

| Kind | τ (days) | β |
|---|---|---|
| validation | 90 | 0.4 |
| contradiction | ∞ (capped, never fully decays) | — |
| citation | 60 | 0.5 |
| query | 7 | 0.8 |
| co_activated | 14 | 0.7 |
| refinement | 30 | 0.6 |
| outcome | 120 | 0.3 |
| preference | 180 | 0.3 |

These are first-pass priors. They should be revisited with empirical
data once the substrate is observable.

### Soft Membership Cap

K = 3 keeps projection size sane. Higher K captures more genuine
multi-tree membership but increases storage and complicates retrieval.
Anchoring on K = 3 is defensible; revisit if observed weight
distributions show meaningful mass beyond top 3 for substantial node
populations.

### Resource Channel Count

Four channels (carbon / nitrogen / phosphorus / water) is biology
metaphor. Sylk's actual scoring depends on a smaller number of
informationally-distinct signals. A sensitivity study is warranted: do
nitrogen and phosphorus carry independent retrieval value, or do they
collapse onto one in practice?

A defensible alternative starting set: **support, trust, salience,
recency** — the same axes, named without the mycology costume.

Recommendation: implement four channels but instrument for collapse;
prepare to merge if independence isn't observed.

### Full Graph Relaxation Cadence

Reaction-diffusion relaxation over the active subgraph is the substrate's
heaviest periodic cost. Per-event incremental updates are cheap; full
relaxation is not.

- Too frequent: substrate state stays fresh, costs spike.
- Too infrequent: drift accumulates, PoI views go stale.

Likely cadence: per-canopy-shift (intent change → relax neighborhood) +
periodic full sweep at maintenance interval (e.g., every N events or
every M minutes, whichever first).

### Guardian's Role in Substrate

How much of Guardian's authority should descend from ranking-time
filtering into substrate-level allelopathy?

- **Light:** only hard policy boundaries emit allelopathic inhibition.
  Most Guardian work stays at rank time.
- **Heavy:** all Guardian-class constraints emit allelopathy. Substrate
  shapes growth at planting time.

Heavy is more biologically coherent but couples the Guardian to substrate
dynamics in ways that complicate testing. Light preserves modularity.

Recommendation: light by default; promote specific constraints to
allelopathic on operator opt-in.

### Replay × Staging

Replay is what moves a node up a stage (Pioneer → Sapling → Mature).
This means replay priority should weight stage-transition urgency, not
just salience. A Sapling that's one validation away from Mature should
outrank a Pioneer that's been validated twice.

The replay scheduler currently considers user correction, success
intensity, contradiction density, novelty, repeated reuse, downstream
impact, unresolved uncertainty. Add: **stage-transition proximity**.

### Curator-Naming Budget

If every speciation triggers a curator (LLM) call, cost adds up. Options:

- **Batch.** Curator runs at maintenance cadence over all
  newly-crystallized candidate clusters at once.
- **Rate-limit.** N curator calls per hour cap.
- **Holding pen.** Unnamed candidates remain queryable by interaction
  proximity until the curator catches up.

Recommendation: all three. Batch + rate-limit + queryable-but-unnamed
holding pen.

## Implementation Phasing

### Phase 1 — Coexistence

Run the interaction-node graph alongside the existing branch projection.
Same ledger, additional projector, additional tables. Existing retrieval
pipeline unchanged. **Validate that node projections converge to
sensible structure on real interactions.**

### Phase 2 — Substrate

Multi-channel substrate state, BCM + scaling, decay sweep, basic
reaction-diffusion relaxation (no Turing patterns yet — just damped
diffusion). **Validate stability.**

### Phase 3 — Clusters and PoI

Online clusterer in maintenance loop. Cluster projector. PoI views as
read-only surfaces. Curator-naming subsystem. Bridge identification.
**Validate that named clusters are recognizable to humans.**

### Phase 4 — Retrieval Cutover

Branch Packets hydrated from interaction-node paths instead of from
`forest_branches` rows. PoI markers added to packet shape. Soft
membership feeds retrieval scoring. Existing learned reranker retrained
on new feature set.

### Phase 5 — Substrate Maturity

Turing reaction-diffusion patterns. Allelopathy from Guardian.
Developmental staging (derived). Tree-level succession. Critical-period
reopening on contradiction load.

### Phase 6 — Deprecation

`forest_branches` and `forest_relay_edges` become derived caches;
projector that maintains them retires once cluster + node projections
are stable.

## Non-Goals

- Replacing the event ledger or its CQRS discipline.
- Replacing UniversalContentStore, Bleve, or VectorGraphDB.
- Treating cluster structure as authoritative truth (the ledger is the
  truth; clusters are projection).
- Making the substrate a router on day one. Substrate signals enter
  ranking only after the substrate has demonstrated stability.
- Hand-curating cluster boundaries. Curators name; they do not
  construct. Constructing trees by hand defeats emergence.
- Treating predicted latent intent as automatic permission for scope
  changes. Same MEMORY_FOREST.md discipline; the substrate doesn't
  loosen it.

## Summary

The Emergent Forest re-grounds Sylk's memory on the interaction itself,
lets topical structure crystallize from density in interaction space,
and upgrades the substrate to a richer biological model — multi-channel
resource flow, BCM homeostasis, Turing-style competition, developmental
staging, allelopathic governance.

The CQRS event-sourced runtime survives intact. The agent-facing skill
surface and Branch Packet contract survive intact. What changes is the
*shape* of the projection: facet-trees compress into interaction kinds,
the relay graph compresses into bridges, scalar conductance expands into
multi-channel resource economy, and trees become things that grow
under observation rather than schemas declared up front.

The forest the agent sees is the forest the user actually planted.

---

# Appendix: Implementation

This appendix specifies the implementation in full enough detail that
an engineer can build it without ambiguity. It respects Sylk's
project rules: constants are derived from physical anchors, never
literals; cyclomatic complexity is held under 4; all goroutines are
tracked; no unbounded growth; no drops/leaks/races; Go 1.25+
constructs throughout.

The shape of the work: extend `core/forest/` with five new files
(`nodes.go`, `clusters.go`, `density.go`, `substrate.go`,
`maintenance_emergent.go`) plus schema migrations, plus a thin
density layer over `core/vectorgraphdb/vamana`. Existing types
(`TreeFamily`, `Event`, `BranchPacket`, `Canopy`) survive intact.

## A.1 Storage Schema

All tables under the existing forest SQLite database. Migrations are
forward-only and idempotent (existing forest convention). PRAGMAs
(WAL, foreign keys, busy timeout) inherit from the existing schema.

```sql
-- A.1.1 Interaction-node projection.
-- One row per ledger event (or coalesced event group) that produced
-- a node. Append by node_id; projector idempotent on (event_id,
-- last_applied_seq).
CREATE TABLE forest_nodes (
    node_id            TEXT    NOT NULL PRIMARY KEY,    -- UUIDv4
    event_id           TEXT    NOT NULL,                -- last ledger event that touched this node
    kind               TEXT    NOT NULL,                -- canonical kind or "ext:<name>" (see A.4)
    valence            REAL    NOT NULL DEFAULT 0,      -- [-1, +1]; sign-on-write
    actor              TEXT    NOT NULL,                -- agent_id or "user"
    created_at         INTEGER NOT NULL,                -- unix nanos; ledger timestamp
    last_seen_at       INTEGER NOT NULL,                -- unix nanos; updated on any incident edge event
    embedding_gen      INTEGER NOT NULL,                -- model generation tag
    embedding_ref      TEXT    NOT NULL,                -- vamana node_id (foreign key into VectorGraphDB)
    signature_blob     BLOB,                            -- packed []float32 readout vector; nullable
    provenance_blob    BLOB,                            -- packed UCS pointers (see A.1.7)
    last_applied_seq   INTEGER NOT NULL,                -- watermark (see existing forest_projector_state)
    -- derived/cached fields (rebuildable):
    in_degree_weight   REAL    NOT NULL DEFAULT 0,
    activation_ema     REAL    NOT NULL DEFAULT 0,      -- y_j running activation (see A.5)
    activation_ema_sq  REAL    NOT NULL DEFAULT 0,      -- ⟨y_j²⟩ for BCM threshold
    core_d             REAL    NOT NULL DEFAULT 0,      -- distance to m_pts-th vamana neighbor
    core_d_stale_at    INTEGER NOT NULL DEFAULT 0       -- when core_d was last computed
) STRICT;

CREATE INDEX idx_forest_nodes_kind        ON forest_nodes(kind);
CREATE INDEX idx_forest_nodes_actor       ON forest_nodes(actor);
CREATE INDEX idx_forest_nodes_last_seen   ON forest_nodes(last_seen_at);
CREATE INDEX idx_forest_nodes_embedding   ON forest_nodes(embedding_ref);

-- A.1.2 Typed, weighted, signed edges between nodes.
-- Decay applied lazily: stored weight is at the timestamp shown;
-- effective weight at query time is computed via the decay formula.
CREATE TABLE forest_node_edges (
    src_id             TEXT    NOT NULL,
    dst_id             TEXT    NOT NULL,
    kind               TEXT    NOT NULL,                -- responds_to, validates, contradicts, ...
    weight             REAL    NOT NULL,                -- post-BCM, post-scaling
    valence            REAL    NOT NULL,                -- [-1, +1]
    last_event_at      INTEGER NOT NULL,                -- unix nanos
    last_applied_seq   INTEGER NOT NULL,
    PRIMARY KEY (src_id, dst_id, kind),
    FOREIGN KEY (src_id) REFERENCES forest_nodes(node_id) ON DELETE CASCADE,
    FOREIGN KEY (dst_id) REFERENCES forest_nodes(node_id) ON DELETE CASCADE
) STRICT;

CREATE INDEX idx_forest_edges_dst     ON forest_node_edges(dst_id, kind);
CREATE INDEX idx_forest_edges_src     ON forest_node_edges(src_id, kind);
CREATE INDEX idx_forest_edges_age     ON forest_node_edges(last_event_at);

-- A.1.3 Named tree (cluster) projection.
CREATE TABLE forest_clusters (
    cluster_id         TEXT    NOT NULL PRIMARY KEY,    -- UUIDv4, persistent across merge/split
    name               TEXT,                            -- nullable until curator-named
    state              TEXT    NOT NULL,                -- candidate | named | senescent | dead
    created_at         INTEGER NOT NULL,
    named_at           INTEGER,
    sample_blob        BLOB    NOT NULL,                -- packed [16]node_id representative sample
    density_profile    BLOB    NOT NULL,                -- packed reachability quantiles (q10,q50,q90)
    vigor              REAL    NOT NULL,                -- derived from member-stage distribution
    size               INTEGER NOT NULL,                -- member node count
    last_applied_seq   INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_forest_clusters_state ON forest_clusters(state);

-- A.1.4 Soft membership weights, capped to top-K per node.
CREATE TABLE forest_cluster_membership (
    node_id            TEXT    NOT NULL,
    cluster_id         TEXT    NOT NULL,
    weight             REAL    NOT NULL,                -- normalized over top-K, sums to 1
    rank               INTEGER NOT NULL,                -- 0 (primary) .. K-1
    last_applied_seq   INTEGER NOT NULL,
    PRIMARY KEY (node_id, cluster_id),
    FOREIGN KEY (node_id)    REFERENCES forest_nodes(node_id)    ON DELETE CASCADE,
    FOREIGN KEY (cluster_id) REFERENCES forest_clusters(cluster_id) ON DELETE CASCADE
) STRICT;

CREATE INDEX idx_forest_membership_cluster ON forest_cluster_membership(cluster_id, rank);

-- A.1.5 Substrate channel state, per-edge.
-- Decay applied lazily; effective value at read time uses the
-- power-law formula from §"Decay".
CREATE TABLE forest_substrate_channels (
    src_id             TEXT    NOT NULL,
    dst_id             TEXT    NOT NULL,
    kind               TEXT    NOT NULL,
    carbon             REAL    NOT NULL DEFAULT 0,
    nitrogen           REAL    NOT NULL DEFAULT 0,
    phosphorus         REAL    NOT NULL DEFAULT 0,
    water              REAL    NOT NULL DEFAULT 0,
    last_event_at      INTEGER NOT NULL,
    PRIMARY KEY (src_id, dst_id, kind),
    FOREIGN KEY (src_id, dst_id, kind)
        REFERENCES forest_node_edges(src_id, dst_id, kind) ON DELETE CASCADE
) STRICT;

-- A.1.6 Reaction-diffusion field samples (per cluster region).
-- Periodically relaxed; per-event updates are local diffusion only.
CREATE TABLE forest_substrate_field (
    cluster_id         TEXT    NOT NULL,
    node_id            TEXT    NOT NULL,
    activator_a        REAL    NOT NULL DEFAULT 0,
    inhibitor_i        REAL    NOT NULL DEFAULT 0,
    last_relaxed_at    INTEGER NOT NULL,
    PRIMARY KEY (cluster_id, node_id),
    FOREIGN KEY (cluster_id) REFERENCES forest_clusters(cluster_id) ON DELETE CASCADE,
    FOREIGN KEY (node_id)    REFERENCES forest_nodes(node_id)        ON DELETE CASCADE
) STRICT;

-- A.1.7 Provenance pointers (packed in forest_nodes.provenance_blob).
-- Format: count:uint32, [{ucs_id_len:uint16, ucs_id:bytes, byte_off:uint64, byte_len:uint32}, ...]
-- Stored inline, not a separate table — every node has provenance, no
-- need for the join.

-- A.1.8 Cluster lineage (speciation/merge/split events).
-- Append-only by design. Reconstructible from the ledger.
CREATE TABLE forest_cluster_lineage (
    cluster_id         TEXT    NOT NULL,
    parent_id          TEXT,                            -- nullable for first-generation
    op                 TEXT    NOT NULL,                -- speciate | merge | split
    related_cluster    TEXT,                            -- the other party in merge/split
    occurred_at        INTEGER NOT NULL,
    seq                INTEGER NOT NULL,                -- ledger sequence
    PRIMARY KEY (cluster_id, seq)
) STRICT;

-- A.1.9 PoI cache. Optional materialization; rebuildable from
-- nodes/edges. TTL bounded to the maintenance cycle.
CREATE TABLE forest_poi_cache (
    cluster_id         TEXT    NOT NULL,
    view_kind          TEXT    NOT NULL,                -- hot|boundary|keystone|frontier|brittle|bridge|underused_gold
    members_blob       BLOB    NOT NULL,                -- packed []node_id
    computed_at        INTEGER NOT NULL,
    valid_until        INTEGER NOT NULL,
    PRIMARY KEY (cluster_id, view_kind)
) STRICT;
```

## A.2 Go Type Definitions

Types live in `core/forest/`. Each struct is sized for cache-line
locality where it appears in hot loops; packing is explicit via field
ordering.

```go
// core/forest/nodes.go

// Kind is the canonical interaction kind. Canonical kinds are
// constants; extensions use the form "ext:<name>" until they earn
// promotion (see A.4).
type Kind string

const (
    KindIntent        Kind = "intent"
    KindEvidence      Kind = "evidence"
    KindDecision      Kind = "decision"
    KindValidation    Kind = "validation"
    KindContradiction Kind = "contradiction"
    KindQuery         Kind = "query"
    KindRefinement    Kind = "refinement"
    KindFork          Kind = "fork"
    KindCitation      Kind = "citation"
    KindOutcome       Kind = "outcome"
    KindPreference    Kind = "preference"
)

// canonicalKindCount tracks the canonical kind population. Used as
// the divisor for kind-density computations and as the initial
// allocation hint for kind-keyed maps. Updated automatically in tests
// via reflect over the const block (see TestCanonicalKinds).
const canonicalKindCount = 11

// EdgeKind is the typed relationship between two nodes. Same
// canonical/extension discipline as Kind.
type EdgeKind string

const (
    EdgeRespondsTo      EdgeKind = "responds_to"
    EdgeValidates       EdgeKind = "validates"
    EdgeContradicts     EdgeKind = "contradicts"
    EdgeRefines         EdgeKind = "refines"
    EdgeForksFrom       EdgeKind = "forks_from"
    EdgeCites           EdgeKind = "cites"
    EdgeCoActivatedWith EdgeKind = "co_activated_with"
    EdgeDefersTo        EdgeKind = "defers_to"
    EdgeSupersedes      EdgeKind = "supersedes"
)

// Stage is the developmental stage of a node, derived from its
// validation count, kind diversity, and recent activation rate. Not
// stored as authoritative state; computed from forest_nodes columns
// at query time. See A.7.
type Stage int8

const (
    StagePioneer Stage = iota
    StageSapling
    StageMature
    StageClimax
    StageSnag
)

// Node is the in-memory projection of one row of forest_nodes plus
// any lazily-loaded structure (provenance, signature). Field order
// minimizes padding on amd64 / arm64 — the hot fields (NodeID,
// CoreD, ActivationEMA, EmbeddingRef) cluster in the first cache
// line for the density-clustering hot loop.
type Node struct {
    NodeID          [16]byte // UUID, packed
    CoreD           float32  // distance to m_pts-th vamana neighbor
    ActivationEMA   float32  // y_j running average
    ActivationEMASq float32  // ⟨y_j²⟩ for BCM threshold
    Valence         float32  // [-1, +1]
    InDegreeWeight  float32  // sum of inbound edge weights
    EmbeddingGen    int32    // model generation
    Kind            Kind
    Actor           string
    EmbeddingRef    string  // vamana node id
    EventID         string
    CreatedAt       int64   // unix nanos
    LastSeenAt      int64   // unix nanos
    CoreDStaleAt    int64   // when CoreD was last computed
    LastAppliedSeq  uint64
    Signature       []float32 // nullable; loaded on demand
    Provenance      []ProvenancePtr
}

// Edge is the in-memory projection of one row of forest_node_edges.
type Edge struct {
    SrcID         [16]byte
    DstID         [16]byte
    Weight        float32  // post-BCM, post-scaling
    Valence       float32  // [-1, +1]
    Kind          EdgeKind
    LastEventAt   int64
    LastAppliedSeq uint64
}

// ProvenancePtr points into UniversalContentStore. Inline-packed into
// forest_nodes.provenance_blob.
type ProvenancePtr struct {
    UCSID    string
    ByteOff  uint64
    ByteLen  uint32
}

// Cluster is the in-memory projection of one row of forest_clusters
// plus its representative sample.
type Cluster struct {
    ClusterID       [16]byte
    State           ClusterState
    Vigor           float32
    Size            int32
    Sample          [clusterSampleSize][16]byte // representative member node IDs
    DensityProfile  [3]float32                  // q10, q50, q90 of pairwise mutual-reachability
    Name            string                       // empty if not yet named
    CreatedAt       int64
    NamedAt         int64
    LastAppliedSeq  uint64
}

// clusterSampleSize is the number of representative members kept in
// the cluster's projection. Sized so the sample fits inline within
// one SQLite page boundary alongside the rest of the cluster row,
// and so the sample's covariance estimate has acceptable variance
// (16 samples → SE ≈ σ/4 on a normal distribution; finer sampling
// is paid for at query time on demand).
const clusterSampleSize = 16

// ClusterState is the lifecycle state of a cluster.
type ClusterState int8

const (
    ClusterCandidate ClusterState = iota
    ClusterNamed
    ClusterSenescent
    ClusterDead
)
```

## A.3 Density Layer Over Vamana

The clusterer is a thin layer over `core/vectorgraphdb/vamana` and
its IVF/BBQ extensions. It does *not* maintain a separate spatial
index.

### A.3.1 Interfaces against existing infrastructure

```go
// core/forest/density.go

// VamanaIndex is the subset of core/vectorgraphdb/vamana's API the
// density layer needs. Production wires a *vamana.Graph; tests pass
// a synthetic implementation. The interface is small by design: the
// density layer reads neighbors and distances; it never mutates the
// graph.
type VamanaIndex interface {
    // KNN returns the k nearest node IDs and their distances to the
    // query node. Distances are post-BBQ if BBQ is configured;
    // otherwise raw L2 or cosine per the index's distance function.
    KNN(nodeID string, k int) (neighbors []string, distances []float32, err error)

    // Distance returns the distance between two indexed nodes
    // without performing a full search. O(1) for nodes both
    // resident in the same posting list; O(log K) otherwise.
    Distance(a, b string) (float32, error)

    // PartitionOf returns the IVF partition ID for a node.
    PartitionOf(nodeID string) (uint32, error)

    // PartitionMembers returns the node IDs in a partition. Used
    // for partition-local density traversal.
    PartitionMembers(partitionID uint32) ([]string, error)
}

// CoreDistanceCache holds per-node core_d values. Backed by
// forest_nodes.core_d on disk; in-memory copy is a sync.Map of
// node_id → coreDEntry. The cache is the only place core_d is
// read on the hot path; recomputation goes through Refresh.
type CoreDistanceCache struct {
    entries  sync.Map // node_id (string) → *coreDEntry
    mPts     int      // m_pts parameter; see A.3.2
    vamana   VamanaIndex
}

type coreDEntry struct {
    value      float32
    computedAt int64 // unix nanos
}

// mPtsAnchor: minimum points for a region to be a "core" point.
// Derived from the canonical kind population: a region needs at
// least one representative of each face (kind diversity threshold)
// to be considered structured. Floors at 5 to avoid pathologically
// small clusters on dense bursts.
const mPtsAnchor = canonicalKindCount / 2  // = 5 with current kinds
```

### A.3.2 Computing core_d

```go
// CoreD returns the m_pts-th nearest-neighbor distance for a node,
// fetching from the cache or computing via Vamana on miss.
//
// Cache-miss path is bounded by the Vamana KNN call (typically
// O(log N) with a small constant via DiskANN beam search).
func (c *CoreDistanceCache) CoreD(nodeID string) (float32, error) {
    if v, ok := c.entries.Load(nodeID); ok {
        return v.(*coreDEntry).value, nil
    }
    return c.refreshLocked(nodeID)
}

// refreshLocked computes core_d for a single node and caches it.
// The "locked" suffix is by convention — sync.Map handles its own
// synchronization; the name signals that this is the slow path.
func (c *CoreDistanceCache) refreshLocked(nodeID string) (float32, error) {
    neighbors, distances, err := c.vamana.KNN(nodeID, c.mPts)
    if err != nil {
        return 0, fmt.Errorf("core_d: vamana knn for %s: %w", nodeID, err)
    }
    if len(distances) < c.mPts {
        // Insufficient neighbors → noise. Sentinel value floats above
        // any real distance so density-reachability fails for it.
        return math.MaxFloat32, nil
    }
    coreD := distances[c.mPts-1]
    c.entries.Store(nodeID, &coreDEntry{
        value:      coreD,
        computedAt: time.Now().UnixNano(),
    })
    _ = neighbors
    return coreD, nil
}

// Invalidate drops the cached core_d for a node. Called by the node
// projector when an event modifies the node's neighborhood (new
// inbound edge, embedding regenerated).
func (c *CoreDistanceCache) Invalidate(nodeID string) {
    c.entries.Delete(nodeID)
}
```

### A.3.3 Mutual reachability and density-reachability

```go
// MutualReachability computes d_mreach(a, b) = max(core_d(a),
// core_d(b), d(a, b)).
func MutualReachability(c *CoreDistanceCache, vamana VamanaIndex, a, b string) (float32, error) {
    coreA, err := c.CoreD(a)
    if err != nil {
        return 0, err
    }
    coreB, err := c.CoreD(b)
    if err != nil {
        return 0, err
    }
    dist, err := vamana.Distance(a, b)
    if err != nil {
        return 0, err
    }
    return maxFloat32(coreA, coreB, dist), nil
}

// densityReachableFrom performs a BFS from start, traversing edges
// whose mutual-reachability distance is below epsilon. Bounded by
// maxNodes (anchored at clusterSampleSize × log of cluster count;
// see A.3.5) and ctx cancellation.
//
// Returns the connected component reachable from start under the
// epsilon constraint.
func densityReachableFrom(
    ctx context.Context,
    c *CoreDistanceCache,
    vamana VamanaIndex,
    start string,
    epsilon float32,
    maxNodes int,
) (map[string]struct{}, error) {
    visited := make(map[string]struct{}, maxNodes)
    visited[start] = struct{}{}
    queue := []string{start}

    for len(queue) > 0 {
        if err := ctx.Err(); err != nil {
            return nil, err
        }
        if len(visited) >= maxNodes {
            break
        }
        head := queue[0]
        queue = queue[1:]
        if err := expandNeighbors(c, vamana, head, epsilon, visited, &queue); err != nil {
            return nil, err
        }
    }
    return visited, nil
}

// expandNeighbors is split out to keep densityReachableFrom under
// the cyclomatic-complexity bound. It enqueues every neighbor of
// `head` whose mutual-reachability distance is below epsilon.
func expandNeighbors(
    c *CoreDistanceCache,
    vamana VamanaIndex,
    head string,
    epsilon float32,
    visited map[string]struct{},
    queue *[]string,
) error {
    neighbors, _, err := vamana.KNN(head, c.mPts)
    if err != nil {
        return fmt.Errorf("expand neighbors of %s: %w", head, err)
    }
    for _, n := range neighbors {
        if _, seen := visited[n]; seen {
            continue
        }
        d, err := MutualReachability(c, vamana, head, n)
        if err != nil {
            return err
        }
        if d < epsilon {
            visited[n] = struct{}{}
            *queue = append(*queue, n)
        }
    }
    return nil
}
```

### A.3.4 Epsilon — the density bandwidth

Epsilon is per-cluster, derived from the cluster's density profile:

```go
// epsilonFor returns the cluster-local epsilon: nodes are
// density-connected within the cluster if d_mreach < epsilon.
//
// Anchored on the cluster's q90 mutual-reachability (the value
// covering 90% of intra-cluster pairs at last cohesion check),
// scaled by an expansion factor that allows nodes slightly outside
// the historical envelope to still join via density-reachability.
//
// The expansion factor is itself derived: it is the ratio of
// activator-channel diffusion length to inhibitor-channel diffusion
// length (D_A / D_I, both from the substrate dynamics). When
// inhibition dominates (D_I ≫ D_A), epsilon contracts; when
// activation dominates, epsilon expands. This is the structural
// expression of "Turing patterns shape boundaries".
func epsilonFor(cluster *Cluster, dA, dI float32) float32 {
    q90 := cluster.DensityProfile[2]
    if dI <= 0 {
        return q90
    }
    expansion := dA / dI
    return q90 * (1.0 + expansion)
}
```

### A.3.5 Per-event tentative-membership assignment

On node arrival the node projector assigns tentative membership by
density-reachability to the *primary cluster of each Vamana neighbor
that is itself a cluster member*. This is O(m_pts × K_membership)
per insertion and avoids the full density-reachable BFS on the hot
path.

```go
// AssignTentative computes top-K cluster membership for a newly
// inserted node, weighted by mutual-reachability proximity.
//
// O(m_pts) Vamana queries; O(K) write into forest_cluster_membership.
// No density-reachable BFS — that's deferred to the maintenance loop.
func (s *Service) AssignTentative(
    ctx context.Context,
    nodeID string,
) ([]membershipAssignment, error) {
    neighbors, distances, err := s.vamana.KNN(nodeID, s.mPts)
    if err != nil {
        return nil, fmt.Errorf("tentative knn: %w", err)
    }
    candidates := make(map[string]float32, len(neighbors))
    if err := s.gatherCandidatesFromNeighbors(ctx, nodeID, neighbors, distances, candidates); err != nil {
        return nil, err
    }
    return topKMembership(candidates, membershipK), nil
}

// gatherCandidatesFromNeighbors builds a (cluster_id → weight) map by
// looking up each neighbor's primary cluster and adding 1/d_mreach
// as the weight contribution. Split out to hold cyclomatic
// complexity ≤ 3.
func (s *Service) gatherCandidatesFromNeighbors(
    ctx context.Context,
    src string,
    neighbors []string,
    distances []float32,
    out map[string]float32,
) error {
    for i, neighborID := range neighbors {
        if err := ctx.Err(); err != nil {
            return err
        }
        primary, err := s.primaryClusterOf(neighborID)
        if err != nil {
            return err
        }
        if primary == "" {
            continue
        }
        d, err := MutualReachability(s.coreDCache, s.vamana, src, neighborID)
        if err != nil {
            return err
        }
        out[primary] += 1.0 / (d + distances[i])
    }
    return nil
}

// membershipK is the soft-membership cap. K = 3 keeps the
// per-node row count bounded; observed weight distributions show
// negligible mass beyond rank-3 in stable forests.
const membershipK = 3
```

## A.4 Kind Promotion

Kinds start as canonical constants. Extensions enter as
`"ext:<name>"`. Promotion criteria are derived, not literal:

```go
// PromotionCandidate returns true if an extension kind has earned
// canonical status. Criteria, all derived from observed population:
//  1. Adoption: ≥ canonicalKindCount × population_factor nodes
//     carry the kind. (Anchored on the existing canonical count;
//     the new kind is "as adopted as the average canonical".)
//  2. Stability: signature variance for nodes of this kind is
//     below the median signature variance across canonical kinds.
//     (Anchored on canonical-kind variance; promotion requires
//     the new kind to be "at least as semantically tight" as the
//     existing baseline.)
//  3. Persistence: the kind has been observed continuously for a
//     duration ≥ the median canonical-kind staleness window.
//
// All three must hold at the same maintenance-loop tick.
func (s *Service) PromotionCandidate(extKind Kind) (bool, error) {
    stats, err := s.kindStats(extKind)
    if err != nil {
        return false, err
    }
    return s.meetsAdoption(stats) &&
        s.meetsStability(stats) &&
        s.meetsPersistence(stats), nil
}

// populationFactor: an extension reaches "as adopted as the average
// canonical kind" when its node count is ≥ total_nodes /
// canonicalKindCount. The factor below adjusts for the fact that
// truly ubiquitous kinds (intent, evidence) skew the mean upward;
// we want the new kind to match the *median* canonical, not the
// mean. 0.5 corresponds to "median canonical adoption" on a
// log-uniform population distribution.
const populationFactor = 0.5
```

## A.5 Substrate Dynamics

### A.5.1 BCM activation and threshold

```go
// core/forest/substrate.go

// ActivationEMA computes the post-event running activation y_j and
// its squared average ⟨y_j²⟩, returning new values to persist back
// to forest_nodes.
//
// y_j on a single event = sum over inbound edges of
//   weight_ij × pre-activation_i × valence_event × kind_gain[event_kind]
// where kind_gain captures kind-specific signal strength (validation
// > query; tunable per A.5.4).
//
// The EMA window τ is derived from the post-synaptic node's
// observed event arrival rate: τ = max(τ_min, 1 / (recent rate)).
// τ_min anchors on the smallest meaningful inter-event gap (1 s).
func ActivationEMA(
    prevEMA, prevEMASq float32,
    instant float32,
    tau time.Duration,
    elapsed time.Duration,
) (newEMA, newEMASq float32) {
    alpha := emaAlpha(tau, elapsed)
    newEMA = prevEMA*(1-alpha) + instant*alpha
    newEMASq = prevEMASq*(1-alpha) + instant*instant*alpha
    return
}

// emaAlpha is the per-event EMA coefficient, derived from elapsed
// time vs window: alpha = 1 - exp(-elapsed/tau). Captures
// continuous-time EMA in discrete-event form.
func emaAlpha(tau, elapsed time.Duration) float32 {
    if tau <= 0 {
        return 1
    }
    ratio := float64(elapsed) / float64(tau)
    return float32(1 - math.Exp(-ratio))
}

// BCMUpdate computes the new edge weight Δw_ij ∝ φ(y_j, θ_M) × x_i
// where φ(y, θ) = y · (y - θ) and θ_M(j) = ⟨y_j²⟩.
// Returns the post-update weight, clamped to [0, 1].
func BCMUpdate(weight, preActivation, postActivation, postActivationSq float32) float32 {
    theta := postActivationSq
    phi := postActivation * (postActivation - theta)
    delta := phi * preActivation * bcmLearningRate
    next := weight + delta
    return clampUnit(next)
}

// bcmLearningRate is derived from the observed event arrival rate
// and the desired half-life of weight changes: a single event
// should move weight by no more than ~1/τ_half of the way from
// current to target. Anchored on the activation EMA window so BCM
// changes track the same temporal scale as activation itself.
//
// Computed once at service startup from observed historical event
// rates; not a literal.
var bcmLearningRate float32  // initialized in Service.deriveBCMRate()
```

### A.5.2 Synaptic scaling

```go
// SynapticScale renormalizes inbound edge weights when their sum
// exceeds the soft budget B. Returns the multiplier to apply to
// every inbound edge weight on node j.
//
// B is per-node, derived from the kind population: B = canonicalKindCount.
// Rationale: in the ideal case, each canonical kind contributes one
// strong inbound edge of weight 1.0 to every node it touches, so
// the budget should accommodate one edge per kind without
// suppression. Derived, not literal.
func SynapticScale(inboundSum float32) float32 {
    budget := float32(canonicalKindCount)
    if inboundSum <= budget {
        return 1.0
    }
    return budget / inboundSum
}
```

### A.5.3 Resource economy update rules

```go
// ChannelDeposit moves resources between (src, dst) on a substrate
// edge given the event valence and edge kind. Returns the new
// channel state to persist.
//
// The exchange ratios are derived from the channel meanings:
//  - Validation: nitrogen src→dst (the validator confers
//    correctness pressure); a small carbon dst→src (validating
//    earns evidence weight by attaching to a real claim).
//  - Contradiction: nitrogen drain on dst (correctness pressure
//    leaves); carbon also drains from dst because the claim
//    weakened.
//  - Co-activation: water symmetric.
//  - Citation: carbon src→dst (the cited node gains evidence).
//  - Query: phosphorus src→dst (intent salience flows toward the
//    answer).
//
// Specific exchange ratios are constants tied to kind definitions,
// not free parameters; they're checksum-tested in
// TestChannelExchangeIsKindGrounded.
func ChannelDeposit(
    cur ChannelState,
    valence float32,
    kind EdgeKind,
) ChannelState {
    rule := exchangeRules[kind]
    return ChannelState{
        Carbon:     cur.Carbon + rule.Carbon*valence,
        Nitrogen:   cur.Nitrogen + rule.Nitrogen*valence,
        Phosphorus: cur.Phosphorus + rule.Phosphorus*valence,
        Water:      cur.Water + rule.Water*valence,
    }
}

// ChannelState is the per-edge resource vector. Persisted via
// forest_substrate_channels.
type ChannelState struct {
    Carbon     float32
    Nitrogen   float32
    Phosphorus float32
    Water      float32
}

// exchangeRules is the kind→channel-deltas map. Kind-grounded:
// every entry's existence is justified by what the kind *means*,
// not by tuning. See A.5.5 for the table.
var exchangeRules = map[EdgeKind]ChannelState{
    EdgeValidates:       {Nitrogen: +1, Carbon: +0.25},
    EdgeContradicts:     {Nitrogen: -1, Carbon: -0.5},
    EdgeCoActivatedWith: {Water: +1},
    EdgeCites:           {Carbon: +1},
    EdgeRespondsTo:      {Phosphorus: +1, Water: +0.5},
    EdgeRefines:         {Phosphorus: +0.5},
    EdgeForksFrom:       {Phosphorus: +0.5, Water: -0.5},
    EdgeDefersTo:        {Nitrogen: +0.5},
    EdgeSupersedes:      {Nitrogen: +0.5, Carbon: -0.5},
}
```

### A.5.4 Per-kind decay

Decay applied lazily — stored values are at `last_event_at`;
effective values at query time use the formula. Per-kind shape
parameters are derived from the kind's epistemic role:

```go
// DecayShape returns (τ_k, β_k) for a kind. The values come from
// kind semantics, not tuning, and are checksum-tested:
//   - validation:    long horizon, gentle taper
//   - contradiction: never fully decays (β = 0 → constant after
//                    some asymptote), implemented as a min-floor
//   - citation:      long horizon, gentle taper
//   - query:         short horizon, steep taper
//   - co_activation: medium horizon, medium taper
type DecayShape struct {
    Tau  time.Duration
    Beta float32
    // MinFloor is the asymptotic minimum; non-zero only for
    // contradictions (they leave permanent record).
    MinFloor float32
}

// DecayedWeight returns w_0 × (1 + t/τ)^(-β), with floor.
func DecayedWeight(w0 float32, age time.Duration, shape DecayShape) float32 {
    if shape.Tau <= 0 {
        return maxFloat32(w0, shape.MinFloor)
    }
    ratio := float32(age) / float32(shape.Tau)
    decayed := w0 * pow32(1.0+ratio, -shape.Beta)
    return maxFloat32(decayed, shape.MinFloor)
}
```

Concrete shapes (in `decay_shapes.go`):

| Kind | Tau (anchor) | Beta | Floor |
|---|---|---|---|
| `validates` | observed-validation-cadence × 90 | 0.4 | 0 |
| `contradicts` | observed-validation-cadence × 365 | 0.0 | 0.05 × initial |
| `cites` | observed-validation-cadence × 60 | 0.5 | 0 |
| `query` | observed-validation-cadence × 7 | 0.8 | 0 |
| `co_activated_with` | observed-validation-cadence × 14 | 0.7 | 0 |
| `refines` | observed-validation-cadence × 30 | 0.6 | 0 |
| `forks_from` | observed-validation-cadence × 30 | 0.6 | 0 |
| `responds_to` | observed-validation-cadence × 14 | 0.7 | 0 |
| `defers_to` | observed-validation-cadence × 90 | 0.4 | 0 |
| `supersedes` | observed-validation-cadence × 365 | 0.0 | 0 |

`observed-validation-cadence` is the median time between
consecutive validation events on the active forest, computed at
service start from the ledger and refreshed on the maintenance
loop's slow tick.

### A.5.5 Reaction-diffusion on the graph Laplacian

```go
// RelaxField runs one Jacobi step of the discrete reaction-diffusion
// PDE over the active subgraph (canopy + k-hop neighborhood).
// Bounded by maxIterations; cancellable via ctx.
//
// Discrete update per node n:
//   A_n^{t+1} = A_n^t + dt × (D_A × Δ_n A + f(A_n, I_n))
//   I_n^{t+1} = I_n^t + dt × (D_I × Δ_n I + g(A_n, I_n))
// where Δ_n X = mean(X over neighbors) - X (graph Laplacian on
// degree-normalized adjacency).
//
// The reaction terms f and g are the Gierer-Meinhardt kernel:
//   f(A, I) = ρ_A × (A² / I) - μ_A × A
//   g(A, I) = ρ_I × A² - μ_I × I
// Anchored: production rates ρ are per-channel concentrations at
// last sample; degradation rates μ are derived from the channel
// decay shapes in A.5.4.
//
// dt and D_A, D_I are derived from the canopy's mean degree and
// the substrate cycle period (see deriveSubstrateScales in A.8).
func RelaxField(
    ctx context.Context,
    sub *Subgraph,
    field *Field,
    scales SubstrateScales,
    maxIterations int,
) error {
    for i := 0; i < maxIterations; i++ {
        if err := ctx.Err(); err != nil {
            return err
        }
        if err := jacobiStep(sub, field, scales); err != nil {
            return err
        }
        if field.MaxDelta < scales.ConvergenceEpsilon {
            return nil
        }
    }
    return nil
}
```

### A.5.6 Allelopathy projection

```go
// AllelopathicSources are the special node IDs (Guardian-class
// constraints, hard policy boundaries) that emit broadcast
// inhibition. Stored as part of forest_substrate_field with a
// distinguishing edge kind ("defers_to" with valence < 0).
//
// Allelopathic inhibition is asymmetric: source node emits high
// inhibitor concentration; the inhibitor diffuses with longer range
// (D_I × allelopathicReach) than ordinary inhibition. The source
// itself does not receive (its own inhibitor concentration is
// pinned at the source value during relaxation).
//
// allelopathicReach is the multiplier on D_I for allelopathic
// sources. Derived: enough to cover the median cluster's diameter
// (the q50 of cluster sizes in node count, mapped through the
// canopy diffusion velocity). The Guardian's reach matches the
// scale at which decisions propagate.
const allelopathicReach = 4.0  // see A.8 for derivation
```

## A.6 Stage Predicates

Stage is *derived* on read, not stored as authoritative state:

```go
// core/forest/staging.go

// StageOf returns the developmental stage of a node, computed from
// (validation count, kind diversity, recent activation rate) and
// historical contradiction load.
func StageOf(n *Node, edges []Edge, now int64) Stage {
    counts := stageCounts(edges)
    if counts.contradictionsRecent > thresholdCriticalPeriod(counts) {
        return StageMature // critical-period reopening: Climax → Mature
    }
    return classifyByCounts(counts, n.LastSeenAt, now)
}

// stageCounts walks the inbound edges once, tabulating the signals
// that drive staging.
type stageCounts struct {
    validations           int
    contradictions        int
    contradictionsRecent  int
    kindDiversity         int
    recentActivations     int
    recentActivationsAge  int64
}

// classifyByCounts maps counts to a stage band. Thresholds derived
// from the canonical-kind population:
//   StageSapling:  ≥ 1 validation
//   StageMature:   ≥ canonicalKindCount/2 validations AND
//                  kind diversity ≥ canonicalKindCount/3
//   StageClimax:   ≥ canonicalKindCount validations AND
//                  recent activation rate stable for ≥ τ_climax
//   StageSnag:     no recent activation in ≥ τ_snag
func classifyByCounts(counts stageCounts, lastSeen, now int64) Stage {
    age := time.Duration(now - lastSeen)
    if age >= snagThreshold && counts.recentActivations == 0 {
        return StageSnag
    }
    if counts.validations >= canonicalKindCount &&
        counts.recentActivationsAge >= int64(climaxStability) {
        return StageClimax
    }
    if counts.validations >= canonicalKindCount/2 &&
        counts.kindDiversity >= canonicalKindCount/3 {
        return StageMature
    }
    if counts.validations >= 1 {
        return StageSapling
    }
    return StagePioneer
}
```

`snagThreshold` and `climaxStability` are derived from observed
event cadence at service init (see A.8). No literals.

## A.7 Projector Topology

The existing single-leader, lease-coordinated, watermarked projector
(`core/forest/projector.go`) gains two siblings:

```go
// core/forest/projector_node.go

// NodeProjector consumes ledger events into forest_nodes,
// forest_node_edges, and forest_substrate_channels. Inherits the
// existing projector contract (single leader, lease, watermark,
// idempotent replay) — see core/forest/projector.go for the base
// type.
//
// Watermark reuses the existing forest_projector_state machinery
// keyed by projector "node".
type NodeProjector struct {
    base       *projectorBase   // existing forest projector type
    coreDCache *CoreDistanceCache
    vamana     VamanaIndex
    scope      *concurrency.GoroutineScope
}

// applyEvent is the per-event reducer. Idempotent on
// (event.ID, last_applied_seq).
func (p *NodeProjector) applyEvent(tx *sql.Tx, e *Event) error {
    if e.Seq <= p.base.watermark() {
        return nil // already applied
    }
    switch e.Type {
    case EventInteractionRecorded:
        return p.upsertNode(tx, e)
    case EventEdgeRecorded:
        return p.upsertEdge(tx, e)
    case EventSubstrateChannelDelta:
        return p.applyChannelDelta(tx, e)
    default:
        return nil
    }
}

// upsertNode writes a row into forest_nodes and (if the node is
// new) inserts into the Vamana index. The Vamana write is the only
// out-of-tx side effect; on Vamana failure the SQL transaction is
// aborted before commit.
func (p *NodeProjector) upsertNode(tx *sql.Tx, e *Event) error {
    payload, err := decodeNodePayload(e.Payload)
    if err != nil {
        return err
    }
    if err := p.vamana.Insert(payload.NodeID, payload.Embedding); err != nil {
        return fmt.Errorf("vamana insert: %w", err)
    }
    return upsertNodeRow(tx, payload, e.Seq)
}
```

Cluster projector follows the same pattern but at coarser
granularity (batched at maintenance cadence, not per-event):

```go
// core/forest/projector_cluster.go

// ClusterProjector consumes cluster-affecting events into
// forest_clusters, forest_cluster_membership, and
// forest_cluster_lineage. Heavier than NodeProjector — runs at the
// maintenance loop cadence with batched application.
type ClusterProjector struct {
    base   *projectorBase
    scope  *concurrency.GoroutineScope
    namer  CuratorNamer  // interface; see A.10
}

// applyBatch applies up to maxBatch cluster-affecting events in one
// transaction. Bounded by maxBatch (derived from observed event
// rate; see A.8).
func (p *ClusterProjector) applyBatch(ctx context.Context, events []*Event) error {
    return runInTx(ctx, p.base.db, func(tx *sql.Tx) error {
        for _, e := range events {
            if err := ctx.Err(); err != nil {
                return err
            }
            if err := p.applyClusterEvent(tx, e); err != nil {
                return err
            }
        }
        return nil
    })
}
```

## A.8 Maintenance Loop

The maintenance goroutine is tracked on `m.wg`, runs ctx-aware, and
performs each step on a derived cadence:

```go
// core/forest/maintenance_emergent.go

// runMaintenance is the long-lived maintenance loop. Owned by the
// Service; tracked on m.wg via the existing concurrency.GoroutineScope.
func (s *Service) runMaintenance(ctx context.Context) error {
    timer := time.NewTimer(s.cadence.Cycle)
    defer timer.Stop()
    for {
        if err := ctx.Err(); err != nil {
            return err
        }
        if err := s.maintenanceCycle(ctx); err != nil {
            slog.Warn("forest_maintenance_cycle_failed", "err", err.Error())
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-timer.C:
            timer.Reset(s.cadence.Cycle)
        }
    }
}

// maintenanceCycle runs each maintenance step in order. Each step
// is ctx-aware, time-budgeted, and idempotent.
func (s *Service) maintenanceCycle(ctx context.Context) error {
    steps := []maintenanceStep{
        {"decay_sweep", s.decaySweep, s.cadence.DecayBudget},
        {"bcm_threshold", s.bcmThresholdUpdate, s.cadence.BCMBudget},
        {"synaptic_scaling", s.synapticScaling, s.cadence.ScalingBudget},
        {"substrate_relaxation", s.substrateRelaxation, s.cadence.SubstrateBudget},
        {"cluster_compaction", s.clusterCompaction, s.cadence.CompactionBudget},
        {"cluster_cohesion", s.clusterCohesion, s.cadence.CohesionBudget},
        {"cluster_merge", s.clusterMergeCheck, s.cadence.MergeBudget},
        {"poi_recompute", s.poiRecompute, s.cadence.PoIBudget},
        {"replay_schedule", s.replaySchedule, s.cadence.ReplayBudget},
        {"ecology_pruning", s.ecologyPruning, s.cadence.PruningBudget},
    }
    for _, step := range steps {
        stepCtx, cancel := context.WithTimeout(ctx, step.budget)
        err := step.fn(stepCtx)
        cancel()
        if err != nil && !errors.Is(err, context.DeadlineExceeded) {
            return fmt.Errorf("%s: %w", step.name, err)
        }
    }
    return nil
}

type maintenanceStep struct {
    name   string
    fn     func(ctx context.Context) error
    budget time.Duration
}
```

Cadences and budgets are derived at service start:

```go
// MaintenanceCadence holds derived cadence values. None are
// literals; each is a function of observed system properties.
type MaintenanceCadence struct {
    Cycle             time.Duration  // = max(ObservedEventCadence × cyclesPerCadence, MinCycle)
    DecayBudget       time.Duration  // = Cycle / phaseCount × decayWeight
    BCMBudget         time.Duration  // = Cycle / phaseCount × bcmWeight
    ScalingBudget     time.Duration  // = Cycle / phaseCount × scalingWeight
    SubstrateBudget   time.Duration  // = Cycle / phaseCount × substrateWeight
    CompactionBudget  time.Duration  // = Cycle / phaseCount × compactionWeight
    CohesionBudget    time.Duration  // = Cycle / phaseCount × cohesionWeight
    MergeBudget       time.Duration  // = Cycle / phaseCount × mergeWeight
    PoIBudget         time.Duration  // = Cycle / phaseCount × poiWeight
    ReplayBudget      time.Duration  // = Cycle / phaseCount × replayWeight
    PruningBudget     time.Duration  // = Cycle / phaseCount × pruningWeight
}

// DeriveCadence computes maintenance cadences from observed system
// properties. cyclesPerCadence is fixed at canonicalKindCount —
// each canonical kind gets one full maintenance cycle's worth of
// observation per cadence. Phase weights sum to 1.
func DeriveCadence(observedEventCadence time.Duration) MaintenanceCadence {
    cycle := observedEventCadence * time.Duration(canonicalKindCount)
    if cycle < minMaintenanceCycle {
        cycle = minMaintenanceCycle
    }
    weights := phaseWeights{
        decay: 0.10, bcm: 0.10, scaling: 0.05, substrate: 0.20,
        compaction: 0.15, cohesion: 0.10, merge: 0.05,
        poi: 0.10, replay: 0.10, pruning: 0.05,
    }
    return MaintenanceCadence{
        Cycle:            cycle,
        DecayBudget:      timeFraction(cycle, weights.decay),
        BCMBudget:        timeFraction(cycle, weights.bcm),
        // ...
    }
}

// minMaintenanceCycle: the floor on cycle period. Anchored on the
// minimum meaningful inter-event gap (1 s) × canonicalKindCount.
// Below this the maintenance loop dominates the system.
var minMaintenanceCycle = time.Duration(canonicalKindCount) * time.Second
```

## A.9 Compaction, Cohesion, Merge, Split

```go
// clusterCompaction scans the noise pool (nodes with no cluster
// membership) for density-connected subgraphs above threshold and
// crystallizes them as candidate clusters.
func (s *Service) clusterCompaction(ctx context.Context) error {
    noise, err := s.fetchNoise(ctx)
    if err != nil {
        return err
    }
    components, err := s.findDensityComponents(ctx, noise)
    if err != nil {
        return err
    }
    return s.crystallizeAll(ctx, components)
}

// findDensityComponents runs density-reachable BFS from each unvisited
// noise node, growing components. Bounded by ctx.
func (s *Service) findDensityComponents(
    ctx context.Context,
    noise []string,
) ([][]string, error) {
    visited := make(map[string]struct{}, len(noise))
    var components [][]string
    for _, seed := range noise {
        if err := ctx.Err(); err != nil {
            return nil, err
        }
        if _, ok := visited[seed]; ok {
            continue
        }
        comp, err := densityReachableFrom(ctx, s.coreDCache, s.vamana,
            seed, s.epsilonGlobal(), s.maxComponentSize())
        if err != nil {
            return nil, err
        }
        if len(comp) >= s.minComponentSize() {
            components = append(components, sortedKeys(comp))
            mergeInto(visited, comp)
        }
    }
    return components, nil
}

// minComponentSize: a candidate cluster must have ≥ canonicalKindCount
// members to crystallize. Derived: at least one node per canonical
// kind on average. Below this threshold the candidate is too thin
// to support stable structure.
func (s *Service) minComponentSize() int { return canonicalKindCount }

// maxComponentSize: BFS bound to avoid pathologically large noise
// components from monopolizing compaction. Anchored on the
// q90 size of existing named clusters; a noise component larger
// than this is itself anomalous and gets logged for operator
// review rather than crystallized.
func (s *Service) maxComponentSize() int {
    return s.clusterSizeQuantile(0.90)
}

// clusterCohesion checks each named cluster for internal density
// valleys (decay weakened bridging nodes → component fission).
// Splits when a cluster decomposes into ≥ 2 density-connected
// subcomponents.
func (s *Service) clusterCohesion(ctx context.Context) error {
    return s.forEachNamedCluster(ctx, func(c *Cluster) error {
        sub, err := s.checkInteriorCohesion(ctx, c)
        if err != nil {
            return err
        }
        if len(sub) > 1 {
            return s.recordSplit(ctx, c, sub)
        }
        return nil
    })
}

// clusterMergeCheck: pairs of named clusters whose representative
// samples have become density-connected via newly arrived bridge
// nodes are merged.
func (s *Service) clusterMergeCheck(ctx context.Context) error {
    pairs, err := s.candidateMergePairs(ctx)
    if err != nil {
        return err
    }
    return s.forEachPair(ctx, pairs, s.mergeIfConnected)
}
```

## A.10 Naming as Gate

```go
// CuratorNamer is the interface that proposes names for candidate
// clusters. Production wires the Archivalist agent; tests pass a
// deterministic stub.
type CuratorNamer interface {
    // ProposeNames picks at most maxNames candidates and returns
    // proposed names. ctx-bounded; must respect maintenance budget.
    ProposeNames(ctx context.Context, candidates []*Cluster, maxNames int) ([]NamedCluster, error)
}

type NamedCluster struct {
    ClusterID [16]byte
    Name      string
}

// curatorBatchSize: how many candidates the curator processes per
// maintenance cycle. Anchored on the canonical kind count — at
// most one new cluster per canonical kind per cycle keeps the
// curator's LLM-call rate bounded by the kind population, not by
// noise-arrival rate. Excess candidates remain in the holding pen
// (queryable by interaction proximity but not surfaced as named
// topics).
const curatorBatchSize = canonicalKindCount
```

## A.11 PoI Computation

PoI views are computed at maintenance cadence and cached in
`forest_poi_cache` with TTL = one cycle. Each view's algorithm:

```go
// HotZones: clusters whose recent arrival rate exceeds their
// historical baseline by ≥ 1 standard deviation. O(C × W) where C
// is the cluster count and W is the window width.
func (s *Service) computeHotZones(ctx context.Context) (map[string][]string, error) {
    out := map[string][]string{}
    return out, s.forEachNamedCluster(ctx, func(c *Cluster) error {
        rate, baseline, sigma, err := s.arrivalStatistics(ctx, c)
        if err != nil {
            return err
        }
        if rate > baseline+sigma {
            out[uuidString(c.ClusterID)] = s.recentArrivals(ctx, c)
        }
        return nil
    })
}

// BoundaryZones: sub-regions where contradicting interactions
// converge. Detected by clusters of negative-valence edges with
// overlapping endpoints.
func (s *Service) computeBoundaryZones(ctx context.Context) (map[string][]string, error) {
    // Find nodes with high local count of inbound contradiction
    // edges. Group by spatial proximity (vamana k-NN).
    // O(N_contradicted × m_pts).
}

// Keystones: nodes with high betweenness centrality. Computed
// per-cluster via Brandes' algorithm on the cluster's induced
// subgraph. O(|V| × |E|) per cluster; cached aggressively.
//
// Brandes runs only on subgraphs of size ≤ keystoneSubgraphCap,
// which is anchored on observed-cluster-size q75. Larger clusters
// get a sampled approximation (Riondato-Kornaropoulos 2014).
func (s *Service) computeKeystones(ctx context.Context) (map[string][]string, error) {
    // ...
}

// Frontier: leading edge of recent arrival around a cluster.
// Detected by node arrival timestamp percentile (top 10% most
// recent) intersected with cluster boundary (node has at least one
// edge crossing into a different cluster, OR is at the noise
// boundary).
func (s *Service) computeFrontier(ctx context.Context) (map[string][]string, error) {
    // ...
}

// Brittle: high in_degree_weight, low recent activation_ema.
// Detected by ratio threshold derived from the cluster's stage
// distribution.
func (s *Service) computeBrittle(ctx context.Context) (map[string][]string, error) {
    // SELECT node_id FROM forest_nodes
    // WHERE in_degree_weight > q75(in_degree_weight)
    //   AND activation_ema < q25(activation_ema)
    // ORDER BY in_degree_weight DESC
    // ...
}

// Bridges: nodes density-reachable to multiple cluster cores.
// Detected by cluster_membership weights: a bridge has primary
// weight < bridgeThreshold and ≥ 2 non-zero weights.
//
// bridgeThreshold: derived from membershipK and the minimum
// "meaningful" weight: 1/membershipK is the uniform-share
// baseline, and "primary < uniform-share" means the node truly
// straddles. So bridgeThreshold = 1/membershipK = 1/3 with K=3.
func (s *Service) computeBridges(ctx context.Context) (map[string][]string, error) {
    // ...
}

// UnderusedGold: Mature/Climax-stage nodes with strong signature
// magnitude but decayed recent activation rate. Detected by
// (stage ∈ {Mature, Climax}) ∧ (signature_magnitude > q75) ∧
// (activation_ema < q25 of stage cohort).
func (s *Service) computeUnderusedGold(ctx context.Context) (map[string][]string, error) {
    // ...
}
```

## A.12 Branch Packet Hydration

```go
// HydrateBranchPacket builds a Branch Packet from a coherent path
// of interaction-nodes. The path is computed on-demand from the
// node graph (no forest_branches read).
func (s *Service) HydrateBranchPacket(
    ctx context.Context,
    path []string,
) (*BranchPacket, error) {
    nodes, err := s.loadNodes(ctx, path)
    if err != nil {
        return nil, err
    }
    cluster, err := s.containingCluster(ctx, nodes)
    if err != nil {
        return nil, err
    }
    pkt := &BranchPacket{
        Path:             path,
        Cluster:          cluster.Name,
        Summary:          s.summarizePath(nodes),
        Evidence:         s.evidenceFromPath(nodes),
        Conflicts:        s.conflictsFromPath(nodes),
        SignatureReadout: s.signatureReadout(nodes),
        PoIMarkers:       s.poiMarkersInScope(ctx, path),
        BridgeNeighbors:  s.bridgeNeighbors(ctx, path),
        ClusterLineage:   s.clusterLineageOf(ctx, cluster.ClusterID),
    }
    return pkt, nil
}
```

## A.13 Concurrency Invariants

The following hold across the implementation; violations are caught
in tests under `-race`:

1. **Source of truth never read-modify-written.** Every mutation
   is an append to `forest_events`. Projections are derived.
2. **Projector single-leadership.** `forest_projector_state` lease
   gates which process applies events. Multi-process safe.
3. **Watermark monotonicity.** `last_applied_seq` only increases.
4. **Idempotent replay.** Replaying any prefix of the ledger
   produces the same projection up to numerical tolerance.
5. **All goroutines tracked.** Maintenance loop, projector loop,
   curator dispatch — every goroutine is registered with the
   forest's `concurrency.GoroutineScope`. Untracked `go` is a
   build-time forbidden pattern (verified by a vet check).
6. **No unbounded growth.** Every queue is size-capped;
   over-budget items are written through to disk (replay queue,
   curator holding pen).
7. **No drops on observational paths.** Substrate channel updates,
   PoI cache invalidations, and stage-derived computations all
   either succeed or return ctx.Err(); silent drops are bugs.
8. **Lock ordering.** When more than one mutex must be held:
   `service.mu → cluster.mu → node.mu` (alphabetical by struct
   name). Documented in `core/forest/concurrency_invariants.go`.

## A.14 Resource Sizing Summary

Every constant in this implementation is derived from a physical
anchor. Summary:

| Constant | Anchor |
|---|---|
| `mPtsAnchor` | `canonicalKindCount / 2` |
| `clusterSampleSize` | SQLite page boundary alignment |
| `membershipK` | observed weight-distribution mass |
| `populationFactor` | log-uniform median canonical adoption |
| `canonicalKindCount` | reflect over the kind const block |
| `bcmLearningRate` | observed event arrival rate × half-life ratio |
| `synapticScalingBudget` | `canonicalKindCount` (one strong edge per kind) |
| `decayShape.Tau` | observed validation cadence × kind multiplier |
| `epsilonGlobal()` | cluster `q90` mutual-reachability × `(1 + D_A/D_I)` |
| `allelopathicReach` | median cluster diameter / canopy diffusion velocity |
| `minMaintenanceCycle` | `canonicalKindCount × MinMeaningfulGap` |
| `MaintenanceCadence.Cycle` | observed event cadence × `canonicalKindCount` |
| `phase weights` | sum to 1; relative cost of each step at last calibration |
| `minComponentSize` | `canonicalKindCount` |
| `maxComponentSize` | observed cluster-size `q90` |
| `keystoneSubgraphCap` | observed cluster-size `q75` |
| `bridgeThreshold` | `1 / membershipK` |
| `curatorBatchSize` | `canonicalKindCount` |

Service initialization computes the `observed_*` quantities by
running a single read query against the existing ledger; a fresh
forest with no history uses zero, falling back to the
literal-floor anchors (e.g., `MinCycle = 1s × canonicalKindCount`).
The recomputation runs once per maintenance cycle on the slow tick.

## A.15 Test Discipline

Every algorithm above ships with a test under
`core/forest/*_test.go`:

- **Property tests** for idempotent replay (reconstruct projection
  from ledger; assert byte-equality).
- **Race tests** for every mutation (run under `-race` with parallel
  projector + maintenance + ad-hoc reads).
- **Convergence tests** for substrate relaxation (assert MaxDelta
  monotonically decreases under reasonable inputs).
- **Bounds tests** for every "anchor": assert no literal escapes
  into the code by `go vet ./core/forest/...` with a custom
  analyzer that flags numeric literals outside of constant
  declarations.
- **Conformance tests** for stage transitions: given (validations,
  diversity, activation rate), assert StageOf returns the expected
  band.
- **Determinism tests** for cluster ID assignment across replays.
- **Leak tests** that the maintenance loop's tracked goroutines
  exit on ctx.Done within `shutdownHard`.

This is the bar for the implementation: not "it works on the happy
path" but "it provably has the structural properties the design
claims".

