# Emergent Agency

A follow-on to `docs/EMERGENT_FOREST.md` that operationalizes the forest
as the active map driving Sylk's Activity Fabric and Claims Board. The
forest is no longer a passive consequence of agent activity; it is the
ambient context that shapes every action, every causal trace, and every
claim.

## Premise

Three subsystems already coexist in Sylk; this design closes the loop
between them.

- **Forest** = the *map*. Where ideas live (named clusters), how they
  relate (typed edges, bridges), what stage they are at (Pioneer →
  Climax), what is notable about a region (PoI views: hot, brittle,
  boundary, frontier, keystone, bridge, underused-gold).
- **Fabric** = the *record*. The W3C-style trace context propagated with
  every cross-agent message. Captures causality and location. See
  `docs/FABRIC.md` and `docs/FOREST_FABRIC_INTEGRATION.md` for the
  current fabric → forest direction.
- **Claims** = the *obligation*. The accountability layer. Claims gate
  decisions; validations gate acceptance; testaments carry evidence.
  See `docs/CLAIMS.md`.

The current dataflow is one-way: **fabric → forest** (forest harvests
activity events to project structure). This design adds the reverse:
**forest → fabric** (forest position is ambient in every action) and
**forest → claims** (forest history shapes the obligations agents take
on). Outcomes from claims and fabric continue to flow back into the
forest, closing the loop.

The design discipline matches `EMERGENT_FOREST.md`'s: forest signals
are *suggestions*, never auto-permission. Agents override freely;
overrides are themselves signal.

## Forest → Fabric

### Cluster Cursor in Trace Baggage

Every fabric activity carries a forest cursor:

```
forest_cursor:
  cluster_ids:       [primary, ...secondary]   // soft membership, top-K
  focal_node_id:     uuid                      // which interaction-node this action lives at
  signature_readout: vec                       // predicted facet roles
  poi_markers:       [hot, boundary, ...]      // active markers in scope
  stage:             pioneer|sapling|mature|climax|critical
```

Computed at the agent boundary based on the agent's current task
context (what query is active, what evidence has been gathered, what
the Branch Packet returned). Stored in fabric trace baggage; rides
along with every downstream message; restored onto the recipient's
context.

Effect: forest position becomes ambient. An agent receiving a message
already knows where in the forest it is operating, without making a
separate `forest_recall` call. Every chokepoint instrumentation point
that the fabric already captures (per `FABRIC.md`) gets the cluster
cursor for free.

### PoI Markers as Trace Annotations

When the forest's projector identifies a Point of Interest covering
the focal region — a *hot zone* (interaction arrival spike), a
*boundary zone* (open contradiction), a *keystone* node (high
betweenness centrality), a *brittle* node (heavy downstream + decayed
support), a *frontier* (leading edge of recent arrival) — the marker
propagates as fabric baggage. Any downstream activity within that
region inherits the marker.

Concrete effect: an agent inspecting `ctx` sees `poi_markers: [boundary]`
and knows to surface the contradiction rather than push past it. The
marker is not a hard gate — it is awareness — but agents that ignore
boundary markers can be detected and audited.

### Bridge Crossings Emit Fabric Events

When an action transitions from one cluster's neighborhood into
another's via a bridge node, fabric records a `bridge_crossed` event
with the source and destination cluster IDs. Two effects:

1. **Forest reinforcement.** The bridge edge gains weight (validations,
   reuse). Bridges that get used grow; unused bridges decay. This is
   the forest's normal reinforcement, but the fabric event triggers it
   at the moment the crossing happens, instead of waiting for the
   action's downstream evidence to surface.
2. **Cross-cluster auditability.** Cross-cluster work is a known site
   for scope creep. The fabric trace makes every crossing observable
   without polling the forest projection.

### Critical-Period Reopening as Fabric Broadcast

When the forest detects sustained contradiction load above threshold
in a region — triggering a Climax node to drop back to Mature
plasticity — fabric receives a `context_under_review(cluster_id,
node_id)` broadcast. Subsequent activity in that region carries the
annotation in baggage. Agents see "this region is in critical period"
and surface concerns rather than treating prior conclusions as settled.

### Allelopathy as Fabric Suppression

Guardian-class allelopathic fields project into fabric as `suppression`
annotations. An action attempting work in an allelopathic region
receives the suppression annotation in its context *before* any tool
runs. The annotation cites the source constraint. Effect: Guardian
governance partly descends from rank-time filtering (where it is
applied today) into pre-action awareness (where it can be heeded
proactively).

The descent is *light* by default — only hard policy boundaries emit
allelopathic broadcasts; topical Guardian work stays at rank time.
Promotion of specific constraints to allelopathic is operator opt-in.

## Forest → Claims

### Cluster Precedent → Validation Priors

A new derived view, `cluster_validation_patterns`, indexes the claims
ledger by cluster:

```
cluster_validation_patterns(cluster_id, claim_kind) →
  {
    typical_validation_types: [test, inspection, ...],
    typical_validator_agents: [tester, inspector, ...],
    success_rate_per_type:    {...},
    median_validation_count:  N,
  }
```

When an agent posts a claim via `PostAction`, the claims board:

1. Resolves the claim's cluster cursor (from the agent's fabric
   context, or by k-NN lookup against the claim subject's embedding).
2. Looks up the precedent for `(cluster_id, claim_kind)`.
3. Pre-fills the proposed `validations` and `target_validators` from
   the precedent, marking each as `(suggested, source: cluster_id,
   confidence: ...)`.
4. The agent reviews and posts. Overrides are first-class — every
   override is recorded, and a stream of overrides in one cluster is
   itself a forest signal that the cluster's precedent has drifted.

Effect: agents stop authoring validation-set boilerplate from scratch
for routine claims. Novel work still requires explicit validation
authoring; the forest only supplies priors.

### Node Stage → Claim Severity

The forest's developmental staging gates claim severity:

| Region stage | Claim discipline |
|---|---|
| **Pioneer-dominated cluster** | Light: one validator, receipt-class validation, ttl-bounded auto-accept on receipt |
| **Mature cluster** | Standard: validation set per cluster precedent; full testament discipline |
| **Climax cluster** | Heavy: standard discipline plus a Guardian validation requirement |
| **Critical period** (Climax dropping to Mature under contradiction load) | Reinforced: heavy + multi-evidence-kind requirement (test + inspection + receipt) |

These are enforced at `PostAction` time. A claim landing in a Pioneer
region gets a slimmer required-validation set; one landing in a
critical-period region gets the full discipline. The agent sees the
stage in their fabric context and knows what they're agreeing to
before posting.

### Bridge Claims Require Cross-Cluster Validators

If a claim's subject is a bridge node (density-reachable to multiple
cluster cores), the claims board auto-extends the
`target_validators` set to include the steward agent of each touched
cluster. Cross-tree decisions earn cross-tree accountability without
the issuing agent having to enumerate every steward.

### Brittle Nodes Auto-Spawn Maintenance Claims

The forest's replay scheduler already identifies *brittle* nodes (high
in-degree weight + low recent activation rate — heavy downstream
dependents on decayed support) and *underused gold* (Mature-or-Climax
nodes with strong signature vectors but decayed access rate).

These become low-priority maintenance claims, issued automatically by
the cluster's steward agent (Archivalist by default, overridable per
cluster):

```
PostAction({
  type:     "task",
  agent_id: "system:forest_replay_scheduler",
  claims: [{
    title:       "Refresh validation on brittle node {node_id}",
    description: "Heavy downstream dependence; supporting interactions decayed.",
    subject:     <cluster_steward>,
    scope:       [{kind: "cluster", key: cluster_id}, {kind: "node", key: node_id}],
    priority:    derived(brittleness_score, downstream_impact),
    validations: [{type: "receipt", description: "validation refreshed"}],
  }]
})
```

The forest becomes a planning input to the claim queue. Maintenance
work has a place to live; brittle-node decay no longer silently
erodes retrieval quality.

### Contradictions as Remediation Claims

Today, contradiction events update edge weights and decay edge support
(per the forest substrate). With claim-driven coupling, a sufficiently
dense contradiction event — multiple contradictions converging on the
same target node within a window — *also* triggers a remediation claim
addressed to the **architect** (the canonical author of corrective
actions in Sylk). The claim's scope is the contradicted region; its
description summarizes the contradicting paths; its
`replacement_claims` slot is left for the architect to populate.

The threshold for "sufficiently dense" is derived, not literal: when
the BCM threshold for a node has slid significantly downward in a
short window (post-synaptic activity dropping under repeated negative
events), the forest emits the remediation claim.

### Branch Packets as Claim Templates

A new skill, `forest_propose_claim(query) → ClaimTemplate`, lets an
agent pull a claim shape from forest precedent rather than authoring
it from scratch:

```
ClaimTemplate:
  proposed_subject:      <agent_id>
  proposed_scope:        [{kind, key}, ...]
  proposed_validations:  [{type, description, quality_bar}, ...]
  proposed_target_validators: [<agent_id>, ...]
  source_branch_packet:  <packet_id>      // for traceability
  confidence:            float            // how strong the precedent is
  override_count:        int              // how often agents have overridden this template's predecessors
```

The template is built by:

1. Resolving the query into a Branch Packet via the existing retrieval
   pipeline.
2. Reading the packet's `signature_readout` to predict the claim's
   facet roles.
3. Looking up `cluster_validation_patterns` for the packet's cluster
   and the predicted kinds.
4. Returning the assembly as a non-binding suggestion.

Agents fill in specifics; overrides flow back as signal. Routine work
goes faster; novel work is unaffected.

## Closing the Loop

Forest drives fabric and claims; their outcomes refine the forest.

### Claim Outcomes → Forest Signals

Every claim status transition emits a forest-shaped event with the
cluster cursor attached:

| Claim event | Forest interpretation |
|---|---|
| `claim_accepted` | validation event on the subject node + edges to the validating testament |
| `claim_rejected` | contradiction event on the subject node |
| `validation_passed` | strengthens (validator, validated) edge per BCM |
| `validation_failed` | weakens it; if persistent, contributes to BCM threshold drop |
| `remediation_posted` | forks_from edge from rejected to replacement |
| `claim_progressed` | activation tick on the subject node (warmth + recency) |

These are already the kinds of events the forest harvests via fabric
(`FOREST_FABRIC_INTEGRATION.md`); the change is that they now carry an
explicit cluster cursor, so the projection is precise rather than
inferred.

### Replay Scheduler → Claim Queue

The forest's replay scheduler (already considering: user correction,
success intensity, contradiction density, novelty, repeated reuse,
downstream impact, unresolved uncertainty) gains a new output channel:
the claim queue. Replay-priority items become maintenance claims.
Replay isn't a separate concept from work — it *is* work, with the
forest naming it.

The replay scheduler also gains a new input feature: **stage-transition
proximity**. A Sapling node that is one validation away from Mature
should outrank a Pioneer that has been validated twice. Naming this
explicitly closes one of the open questions from
`EMERGENT_FOREST.md`.

### Speciation → Guardian-Review Claims

When a new cluster crystallizes (noise nodes accumulating density →
candidate seed → curator-named tree, per `EMERGENT_FOREST.md`), the
naming event triggers an automatic claim issued by the curator
(Archivalist) to Guardian:

```
PostAction({
  type:     "challenge",
  agent_id: "system:archivalist",
  claims: [{
    title:       "Approve scope of new cluster: {name}",
    subject:     "guardian",
    scope:       [{kind: "cluster", key: cluster_id}],
    description: "New cluster crystallized; review scope and policy boundaries.",
    validations: [{type: "inspection", description: "scope and policy compatible with operator intent"}],
  }]
})
```

This catches scope creep at speciation time rather than at retrieval
time. Guardian's accept/reject feeds back as the cluster's first
formal validation event.

## Concrete Data Flow

### The Cursor

`forest_cursor` is computed by the agent's Forest skill surface
(`forest_resolve_intent`, `forest_recall`, `forest_predict_next_branches`),
attached to fabric trace baggage at the action boundary, and
restored onto the recipient's `ctx` by the fabric trace decoder.

The cursor's storage cost is tiny (top-K cluster IDs + node ID +
signature vector + marker list ≤ ~256 bytes), so propagating it on
every cross-agent message is cheap.

### Cluster Precedent Index

A new projection: `forest_cluster_validation_patterns`. Built by the
cluster projector (introduced in `EMERGENT_FOREST.md`'s CQRS
extension). Updates derived from:

- The claims ledger (every accepted claim's validation set).
- The forest cluster projection (cluster lineage, stage, vigor).

Indexed as `(cluster_id, claim_kind, action_type) → ValidationPattern`.

### Maintenance Claim Queue

The forest's maintenance loop (already running per
`EMERGENT_FOREST.md`) emits maintenance claims as ledger events. The
claims board consumes them like any other action. No special path —
the same single-leader, lease-coordinated, append-only discipline.

### Allelopathy Gate

`PostAction` consults the forest's allelopathy field at the claim's
subject region before committing. If the field exceeds the
suppression threshold, the claim is auto-rejected with the suppressing
constraint cited in the rejection reason. Allelopathy is a *hard*
gate; PoI markers and stage gates are *soft* (suggestions only).

## Failure Modes & Safeguards

### Forest Is Wrong

Cluster names go stale; precedent drifts; suggested validations no
longer match the agent's reality.

**Mitigation:** every forest-supplied suggestion is overridable. Every
override is recorded. A stream of overrides in one cluster *is* the
signal that the cluster needs renaming or precedent refresh. The
curator agent watches override rate per cluster as a primary input.

### Forest Is Unavailable

The cluster projection is eventually-consistent and could lag, or the
projector could be in panic recovery.

**Mitigation:** all forest-driven features degrade to opt-in
enhancements. Fabric drops the cluster cursor when the projection is
stale. Claims default to caller-specified validations when
`forest_cluster_validation_patterns` is unavailable. The system runs
correctly without the forest; the forest only makes it *better*.

### Loop Oscillation

Forest drives claim severity → claims feed forest → could amplify
positive feedback.

**Mitigation:** the BCM homeostasis described in
`EMERGENT_FOREST.md` caps positive feedback at the substrate level.
Synaptic scaling caps total inbound edge weight per node. Hysteresis
on stage transitions: a node moving from Mature to Climax must
satisfy thresholds for a sustained window, not just transiently;
the same applies in reverse for critical-period reopening.

### Cross-Cluster Contamination

Bridge crossings could spread incorrect signals from one cluster to
another (e.g., an allelopathic suppression in one cluster
inappropriately suppressing work in a connected cluster).

**Mitigation:** allelopathy fields are per-cluster, not propagated
across bridges. A bridge node carries *both* clusters' fields;
suppressions stack conjunctively (work suppressed only if both sides
suppress). Boundary conditions on the substrate diffusion (Turing
patterns, per `EMERGENT_FOREST.md`) damp at cluster boundaries.

### Curator Naming Cost

Forest-driven claim suggestions assume named clusters. Unnamed
candidates (the holding pen) can't drive precedent.

**Mitigation:** suggestions degrade gracefully. A claim landing in
an unnamed candidate cluster gets only generic precedent (across the
agent's entire interaction history) rather than cluster-specific
precedent. The agent loses a precision benefit but loses no
correctness.

## Phasing

### Phase 1 — Read-Only Cursor in Fabric

Propagate `forest_cursor` in fabric baggage. No behavior change.
Observe how often actions cross cluster boundaries, how stable the
cursor is across a multi-agent thread, where cluster membership is
ambiguous. Validate the cursor's usefulness without acting on it.

### Phase 2 — Cluster Precedent Lookups (Read-Only)

Add `forest_cluster_validation_patterns` as a derived view. Add a
new agent skill `forest_suggest_validations(cluster_id, claim_kind)`
that returns the precedent. Agents can opt to call it; nothing in the
claims path consults it yet. Observe whether the suggestions match
what agents post anyway.

### Phase 3 — Maintenance Claim Queue from Replay

Forest's replay scheduler emits maintenance claims for brittle nodes
and underused gold. Claims flow as low-priority background work to
cluster stewards. Existing claims pipeline consumes them without
modification.

### Phase 4 — Allelopathy Gate on PostAction

Hard suppression for Guardian-class boundaries: `PostAction`
consults the forest's allelopathy field; suppressed claims auto-reject
with the constraint cited.

### Phase 5 — Stage Severity Gates

`PostAction` adjusts required validation set based on the claim's
cluster stage. Pioneer → light; Climax → heavy; critical-period →
reinforced. Includes the corresponding gates on auto-acceptance.

### Phase 6 — Speciation Review Claims

Cluster projector emits `cluster_speciated` events; Archivalist
auto-issues a Guardian-review claim per speciation.

### Phase 7 — PoI Baggage in Fabric

PoI markers (hot, boundary, keystone, brittle, frontier, bridge) ride
in fabric baggage. Agents inspecting `ctx` see active markers. No
hard gates — awareness only.

### Phase 8 — Bridge Crossing Events + Cross-Cluster Validators

Fabric records `bridge_crossed` events; claims subjecting bridge
nodes auto-extend `target_validators` to include each touched
cluster's steward.

### Phase 9 — Branch Packets as Claim Templates

`forest_propose_claim` skill returns a non-binding template. Agents
opt to use it. Override rates feed back as signal.

### Phase 10 — Critical-Period Reopening Broadcast + Contradiction Remediation

When BCM thresholds slide significantly under contradiction load, a
`context_under_review` broadcast lands in fabric baggage; sufficiently
dense contradictions auto-issue remediation claims to the architect.

## Non-Goals

- Replacing the existing claims pipeline. The forest provides
  suggestions, priors, and gates; it does not own the claim lifecycle.
- Replacing the fabric trace context. The forest cursor is one piece
  of baggage among many, not a redesign of the trace format.
- Auto-permission. Forest-supplied suggestions are never substitutes
  for explicit agent decisions. Every override is first-class.
- Treating the forest as a ranking authority over claims. Claim
  acceptance is determined by validations, per the existing claims
  discipline. The forest informs validation set composition; it does
  not vote.
- Synchronous coupling. Every coupling described here is via
  ledger events and eventually-consistent projections, preserving the
  CQRS discipline of `EMERGENT_FOREST.md` and `CLAIMS.md`.

## Summary

The Emergent Forest gave Sylk a map that grows from interaction
density. Emergent Agency makes that map *operational*: every action
carries its forest position in fabric baggage, every claim is shaped
by the forest's history of similar work, and every outcome flows back
as ledger events that refine the map.

Forest drives fabric. Forest drives claims. Outcomes drive forest. The
discipline that the substrate is suggestion (not authority) is
preserved at every coupling — agents act, the forest informs, the
ledger is truth, overrides are first-class, and the loop closes
through the same eventually-consistent CQRS plumbing already in
place.

The forest the agent sees is the forest the user planted. The agency
the agent exerts is grown from that planting, audited by claims, and
recorded in fabric — ambient, accountable, and self-organizing.
