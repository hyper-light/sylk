# Forest Ecology

A companion to `docs/EMERGENT_FOREST.md` and `docs/EMERGENT_AGENCY.md`
that grounds the forest's biological dynamics in the actual
mathematical frameworks computational immunology and theoretical
ecology use for the same problems — not metaphor, mechanism.

The motivating question this document answers: *given that the
forest needs to detect and contain propagating corruption (bugs,
hallucinations, stale assumptions, contradicted decisions), what
real biological mechanism scales to the high-density,
high-dimensional regime sylk operates in?*

## Why metaphor isn't enough

The original Emergent Forest borrowed Turing reaction-diffusion,
BCM homeostasis, allelopathy, and developmental staging. These are
real mathematical frameworks, not just biological window-dressing.
But the most-obvious add — disease as compartmental SIR/SEIR — is
exactly the kind of metaphor that falls apart under sylk's
operating conditions:

- **Categorical state collapses semantic gradient.** A node either
  Susceptible or Infected has no encoding of how-much,
  what-direction, or how-related-to-other-corruption.
- **Pathogens as discrete IDs don't compose.** Real corruption
  mutates: hallucinations get paraphrased; bug patterns get
  refined into adjacent variants. SEIR forces every variant into
  a fresh pathogen with fresh state.
- **Transmission probability is a global parameter.** Real
  transmission depends on which dimensions of the corruption
  matter for which downstream node. Two scalars (β, γ) can't
  represent this.
- **Doesn't compose with the substrate.** The reaction-diffusion
  field is already a continuous vector field over the graph;
  introducing a separate discrete state machine creates two
  parallel dynamics that can't reinforce.

Computational immunology stopped using compartmental models for
serious antigenic prediction in the 1990s. The framework that
replaced them — antigenic cartography on continuous antigenic
spaces — is what this document adapts to the forest.

## The Antigenic Field

### Pathogens as vectors, not IDs

A pathogen is a point in pathogen-embedding space (same
dimensionality as node embeddings, projected through a learned
head). Pathogens are points; mutations are paths in that space.

Pathogen vectors come from:

- **Contradiction collapse.** When BCM threshold collapses on a
  node under sustained contradiction load, the pathogen vector is
  the contradicted region's signature.
- **Guardian declaration.** The Guardian's declaration text is
  embedded into pathogen space.
- **Architect remediation.** When the architect (canonical author
  of corrective actions) declares a pathogen as part of an
  intentional remediation, the corrective claim's text seeds the
  pathogen vector.

Because pathogens are points in a metric space, **similarity is
graded automatically**: two pathogens close in the space are
related; far apart, unrelated. Cross-immunity emerges from this
metric, not from explicit declarations.

### Per-node corruption and immunity vector fields

Each node carries two vectors:

- `corruption ∈ ℝ^d` — weighted sum of pathogen vectors the node
  has been exposed to.
- `immunity ∈ ℝ^d` — weighted sum of pathogen vectors the node has
  been *cleared from* (via cited validation).

Effective infection at a node, given a query-time pathogen `P`:

```
infection(P, node) = max(0, ⟨P, corruption⟩ - ⟨P, immunity⟩) / ‖P‖
```

Continuous, graded, dimensional. **Cross-immunity emerges
automatically**: a node immune to `P₁` partially covers `P₂`
proportional to `⟨P₁, P₂⟩ / (‖P₁‖‖P₂‖)`.

### Diffusion as vector PDE

The same graph-Laplacian reaction-diffusion machinery that drives
the resource economy carries the corruption field — same
primitive, different kernel:

```
∂corruption(j)/∂t  =  α · Σ_i L_authority(i,j) · corruption(i)
                    - γ · corruption(j)
                    - β · proj_immunity(j)(corruption(j))
```

Where `L_authority` is the weighted Laplacian over authority
edges (`cites`, `refines`, `defers_to`, `responds_to`). Three
scalar parameters (α, γ, β) replace the per-pathogen-per-edge-kind
parameter sprawl of compartmental models.

### Mutation as drift in pathogen space

When corruption transmits through a `refines` or `responds_to`
edge — edges that paraphrase or transform — the pathogen vector
drifts toward the receiver's signature by a small factor δ:

```
pathogen_at_receiver = (1 - δ) · pathogen_at_sender + δ · receiver_signature
```

Mutation is first-class and continuous. The resulting topology in
pathogen space is a tree of variants: each variant is a point;
parent → child edges trace the drift; the original pathogen is
at the root. Immunity to the root partially covers descendants,
decaying with antigenic distance.

This is **exactly the structure flu antigenic-cartography papers
plot every year**: strain trajectories through HA antigenic space,
parent → child variants, vaccine strain selected as the centroid
of the predicted next year's cloud.

### Spectral outbreak detection

The Laplacian of the active subgraph **weighted by corruption**
shifts spectrally during outbreaks. Specifically, λ₂ of
`L_corruption_weighted` (the Fiedler value) drops as the graph
becomes "stuck" around the corrupted region — a known result from
network epidemiology (Pastor-Satorras & Vespignani 2001, Wang et
al. 2003).

```
outbreak_signal(cluster, t) =
    fiedler_baseline(cluster) - fiedler_value(L_corruption_weighted, t)
```

Computed via Lanczos iteration in O(|E|) per check, **independent
of pathogen count and dimensionality**. One spectral check tells
us whether *any* outbreak is forming.

### Quarantine as projection

Retrieval scoring multiplies a node's relevance by:

```
quarantine_factor(node, query) =
    1 - max(0, ⟨corruption(node), pathogen_aligned_with(query)⟩)
        / ‖corruption(node)‖
```

Continuous degradation — heavily corrupted nodes have near-zero
retrieval weight; barely-corrupted nodes have near-full weight.
**No hard cutoff; no S/I state to maintain.** Recovery is
observed: when the corruption vector decays under cited
validations, the quarantine factor naturally returns to 1.

### Recovery via cited validation

A validation testament that names the pathogen (cites the
corruption vector or a Guardian-declared pathogen) adds to the
node's immunity vector by:

```
immunity ←  immunity + λ · testament_confidence · pathogen_vector
```

Recovery is gradient descent in immunity space, accumulating with
each successful cure. **Nothing transitions categorically**; the
infection level continuously drops as immunity coverage grows.

### Why this scales

| Property | SIR/SEIR | Antigenic Field |
|---|---|---|
| State per node | O(pathogens) categorical | O(d) continuous, fixed |
| Cross-pathogen interactions | Manual declaration | Cosine similarity |
| Mutation | New pathogen ID per variant | Continuous drift |
| Outbreak detection cost | O(N × pathogens) | O(\|E\|), one spectral check |
| Substrate composition | Parallel state machine | Same reaction-diffusion primitive |
| Tunable parameters | β, γ, σ × pathogens | α, γ, β (three scalars) |
| High-dimensional handling | Brittle | Native |

## Enhanced Pruning

Existing pruning operates on a single axis — *how warm/active is
this node* — through decay, BCM-LTD, synaptic scaling, lateral
inhibition, density-valley split, and Snag transition. The
antigenic field adds an orthogonal axis — *how clean/poisoned is
this node* — and turns pruning two-dimensional.

### Two-axis pruning

```
prune_priority(n) =
    f_warmth(decayed_weight(n), activation_ema(n))               // existing axis
  - f_immunity(‖immunity(n)‖, mutation_coverage(n))              // protective
  + f_corruption(‖corruption(n)‖ - proj_immunity(corruption(n))) // accelerator
```

Each term is principled:

- **Warmth** stays as the existing decay/BCM signal.
- **Immunity protection** keeps a node alive against pure-decay
  pruning if it carries hard-won institutional immunity. A node
  that's *been through* an outbreak and developed coverage is
  *more* valuable than a fresh node, not less.
- **Corruption acceleration** prunes faster when a node has
  uncovered corruption load — it's dragging its neighborhood; the
  substrate should drop it before it spreads further.

Concrete edge cases this resolves that existing pruning gets wrong:

- **Heavy-but-poisoned**: many citations but high uncovered
  corruption. Currently survives because it's "warm". After:
  pruned because the corruption term dominates.
- **Cold-but-immune**: a Climax node with low recent activity but
  strong immunity vectors. Currently a Brittle/Snag candidate.
  After: protected because its immunity is institutional knowledge.
- **Repeatedly cured**: a node through many cure cycles carrying
  broad cross-coverage. Currently indistinguishable from any other
  Mature node. After: ranked at the top of the protected set —
  immunity diversity is a quality signal.

### Spectral pruning — surgical, structural

When a cluster's corruption-weighted Laplacian shows pathological
eigenmodes, the eigenvector entries with largest magnitude pick
out *which specific nodes* contribute most to the pathology.
These are surgical pruning targets:

```
modes := topKEigenmodes(L_corruption_weighted, K)
for mode := range modes {
    if mode.Eigenvalue > pathologyThreshold(cluster) {
        targets := topMagnitudeNodes(mode.Eigenvector, ratio)
        markForStructuralPrune(targets, "spectral mode", mode.Eigenvalue)
    }
}
```

This is qualitatively new: pruning that targets *load-bearing-but-
corrupted* structure, not just leaf-level decay. The difference
between removing dead branches (decay-driven) and removing a
diseased tree before its rot infects the canopy (structural).

### Cluster-level split-pruning

When the spectrum *bifurcates* — energy splitting between two
competing eigenmodes within a cluster — the cluster has formed
two competing sub-coherences. Currently this would resolve as a
slow density-valley split. With spectral detection it can be
triggered eagerly: the cluster fissions immediately, and if one
daughter is dominantly aligned with the pathological mode, the
*whole daughter* is pruned to Snag rather than its individual
nodes being chipped away.

One cluster-level decision replaces hundreds of per-node decisions
when corruption has structurally taken over a sub-region.

### Replay-protected immunity

The replay scheduler currently weighs by salience, contradiction
density, novelty, downstream impact. Add immunity:

- **Protect the immunized.** Replay-priority floors are *higher*
  for nodes with strong immunity vectors. The system actively
  rehearses its hard-won immune memory so it doesn't decay.
- **Targeted cross-immunization.** Identify nodes that carry
  corruption mutations close in pathogen space to neighbors'
  established immunities. Replay those neighbors near the
  corrupted node — *immunity transfers via co-activation*.
  This is the substrate's analog of nurse-cell-mediated
  immunization.
- **Pre-mortem replay.** A node about to be pruned by spectral or
  corruption signals gets one more replay cycle — a chance for
  cited validation to clear it. If the cycle produces a
  clearance testament, the node is rescued.

### Allelopathic-targeted clearance

Guardian's allelopathic field becomes a *vector*, not a scalar.
Guardian can declare "prune anything aligned with this corruption
vector":

```
prune_if:  cos(corruption(n), guardian_clear_vector) > threshold
       AND ‖immunity(n) projected onto guardian_clear_vector‖ < coverage_min
```

Targeted purge — Guardian removes a specific class of corruption
from the forest in one declaration, without enumerating every
individual node.

### Snag promotion via cure-failure history

A node replayed for cure attempts N times without a successful
clearance testament moves to Snag faster than pure decay would
suggest. The substrate gives up on healing nodes that have proven
unhealable. Per-node `cure_failure_count` is a small integer; the
threshold derives from the median successful-cure cycle count
across the active forest.

### Summary

| Existing pruning | What it catches | What it misses |
|---|---|---|
| Decay | Inactive nodes | Inactive-but-immune (institutional memory) |
| BCM-LTD | Edges incident on under-active targets | Targets that are over-active *with corruption* |
| Synaptic scaling | Over-reinforced edges | Edges carrying corruption-weighted reinforcement |
| Lateral inhibition | Locally-outcompeted nodes | Globally-pathological cluster modes |
| Density-valley split | Slow structural fission | Fast spectral bifurcation |
| Snag transition | Long-disused nodes | Heavy-but-cure-failed nodes |

The antigenic field closes every "what it misses" column.

## Inter-Cluster Competition

The Emergent Forest's competition mechanisms — BCM, synaptic
scaling, lateral inhibition — operate at the *node* and *edge*
level. They handle "this node is outcompeted by its neighbor".
They do not handle the case the user actually encounters most:
**two clusters competing for the same conceptual territory**.

When a user reframes a topic — "let me think about this as X
instead of as Y" — the new framing's cluster crystallizes
alongside the old one. With current dynamics, both persist
indefinitely, competing implicitly via retrieval. The substrate
has no explicit mechanism to drive the loser to extinction.

The framework that handles this is **Lotka-Volterra population
dynamics with niche overlap as the competition coefficient**.

### The Lotka-Volterra cluster system

Treat each named cluster as a species with a population (size in
nodes). Per cluster `i`:

```
dN_i/dt = r_i · N_i · (1 - (N_i + Σ_{j≠i} α_ij · N_j) / K_i)
```

where:

- `N_i` = size of cluster `i` (member count, decayed by stage —
  Climax nodes count fully; Pioneer nodes count fractionally).
- `r_i` = intrinsic growth rate, derived from the cluster's
  pioneer-arrival rate normalized by its current size.
- `K_i` = carrying capacity, derived from the cluster's share
  of total substrate budget. A cluster's `K_i` rises with its
  vigor (active engagement) and falls with disturbance pressure.
- `α_ij` = competition coefficient, the per-unit effect of
  cluster `j` on cluster `i`. **Computed from signature
  overlap**:

  ```
  α_ij = ⟨centroid(sample_i), centroid(sample_j)⟩
       / (‖centroid(sample_i)‖ · ‖centroid(sample_j)‖)
  ```

  A cluster competes more strongly with another to the extent
  their representative samples occupy the same region of
  embedding space. Orthogonal clusters have `α ≈ 0` (no
  competition); near-duplicate clusters have `α ≈ 1` (full
  competition).

`α_ij` is asymmetric in general — a small specialized cluster
within a larger generalist cluster's territory is heavily
suppressed by the generalist (`α_specialist,generalist` high)
while the generalist is barely affected by the specialist
(`α_generalist,specialist` low). This is exactly the asymmetry
real ecological literature studies (Connell 1980 on competitive
exclusion in barnacles is the canonical reference).

### Competitive exclusion and niche partitioning

The Lotka-Volterra system has three regimes:

- **Coexistence** (when `α_ij · α_ji < 1` for all pairs): both
  clusters occupy distinct niches; both persist at reduced
  carrying capacities. This is **niche partitioning** — clusters
  specialize to their unique territory.
- **Competitive exclusion** (when one `α_ij` is large enough that
  its negative carrying-capacity contribution drives `dN_j/dt`
  negative): the weaker cluster shrinks until extinct. The forest
  *prunes* the losing cluster — not by decay, by competition.
- **Bistability** (when both `α_ij` and `α_ji` are simultaneously
  high, depending on initial conditions): only one of the two
  clusters can dominate; which one depends on the trajectory.
  This is the **alternate-stable-states** regime that
  disturbance regimes can flip — fire, drought, or model rotation
  can push the system into a different basin of attraction.

### Mutualism — when α is negative

Two clusters that *consistently co-activate productively* (the
user's queries hit both, validations on one tend to predict
validations on the other) have negative competition coefficient
— the presence of one *helps* the other. This is mutualism, and
sylk has a clean operational signal for it: **conditional
co-activation rate** above the per-cluster baseline.

Mutualistic pairs are protected jointly: pruning one
disproportionately damages the other, so the forest treats them
as a unit during disturbance response. This is also the substrate
mechanism for **bridge stabilization** — bridges between
mutualistic clusters get reinforced.

### Apparent competition

Two clusters can compete *not because they overlap in
embedding space* but because they share a "predator" — typically
a Guardian-class allelopathic constraint that hits both. The
cluster that loses more of its members to the Guardian field
appears to be losing competition with the other, but the actual
mechanism is shared predation pressure. Holt 1977 introduced
this concept formally.

For sylk: this surfaces when an operator declares a cross-cutting
Guardian constraint (e.g., "no work using deprecated API X").
Clusters that depended heavily on X experience apparent
competition with clusters that didn't — even if they never
overlapped in subject matter.

### Storage and computation

```sql
-- Per-pair competition state. Sparse: only pairs with non-trivial
-- α are tracked. Maintained at maintenance cadence.
CREATE TABLE forest_cluster_competition (
    cluster_a            TEXT    NOT NULL,
    cluster_b            TEXT    NOT NULL,
    alpha_ab             REAL    NOT NULL,         -- effect of b on a
    alpha_ba             REAL    NOT NULL,         -- effect of a on b
    coactivation_rate    REAL    NOT NULL,         -- normalized; signed (negative = mutualistic)
    last_computed_at     INTEGER NOT NULL,
    PRIMARY KEY (cluster_a, cluster_b)
) STRICT;
```

The competition coefficient is recomputed at maintenance cadence
via `O(C²)` cosine evaluations on representative samples. With
realistic cluster counts (canonicalKindCount × handful per
canonical kind), this is well under one millisecond per cycle.

Under `O(C²)` pressure at very high cluster counts, the
computation becomes Lanczos-friendly: only the top-K most-similar
pairs need exact `α` values; the rest are guaranteed below the
competition threshold by triangle inequality on a single
HNSW-style nearest-neighbor lookup over cluster centroids.

## Cross-Pollination and Hybrid Vigor

Bridges in the Emergent Forest design are *passive structural
connectors* — nodes density-reachable to multiple cluster cores.
They are how the substrate registers that two topics overlap.
What they *don't* do today is generate anything. Real ecologies
treat bridges as active sites: pollination zones where cross-
fertilization between species produces hybrids that may carry
properties neither parent had.

Knowledge work is overwhelmingly *synthetic*. Novel insight is
typically a synthesis across regions, not pure within-region
deepening. The forest should treat cross-pollination as a
first-class generative mechanism.

### Hybrid detection

A new node `Y` is a *hybrid* when, at insertion time, its
spatial neighborhood places it density-reachable to nodes
belonging to two or more distinct clusters. The detector runs
in the node projector's tentative-membership step (A.3.5):

```
hybrid_score(Y) = entropy(Y.cluster_memberships)
```

Where the membership weights are the soft-membership values
already computed. A node with primary-cluster weight ≈ 1 has
hybrid score 0 (single-cluster); a node with weights split
across multiple clusters has hybrid score > 0. Above a threshold
(anchored on `1 / membershipK` — the uniform-share baseline),
the node is tagged as a hybrid.

### Inheritance from parents

Hybrid nodes inherit substrate state by **weighted combination**
of their parents' state, where parents are the cluster-distinct
nodes in the hybrid's spatial neighborhood weighted by mutual-
reachability:

```
For each parent P_k with weight w_k (sum to 1):
    Y.signature       = Σ w_k · P_k.signature
    Y.corruption      = Σ w_k · P_k.corruption
    Y.immunity        = ⊕ w_k · P_k.immunity      // see below
```

The signature and corruption combinations are weighted vector
sums — standard. The immunity combination uses the **vector OR
operator** ⊕ — the result covers any pathogen any parent was
immune to, not just the average. Concretely:

```
immunity_combined = parent_max_per_dimension(immunity_1, ..., immunity_K)
```

Effectively, the hybrid inherits *every parent's immune memory*.
This is the **hybrid vigor** signal — the hybrid is potentially
fitter than any parent because it carries the union of their
hard-won immunities.

### Hybrid vigor as quality signal

Hybrid nodes are flagged in retrieval scoring with a small bonus
multiplier proportional to `hybrid_score`. The bonus has two
justifications:

1. **Synthesis quality.** A node bridging multiple regions has,
   by construction, integrated knowledge across them — exactly
   the kind of result the agent wants to surface for
   cross-domain queries.
2. **Disturbance resistance.** A hybrid carries the immunity
   union of its parents, so it survives more pathogens. Its
   downstream contribution is more durable.

The bonus is bounded — a hybrid in a region where one parent
cluster has been declared diseased gets *no* bonus (and may be
pruned with the parent), since hybrid vigor doesn't survive
parental disease in real biology either.

### Outbreeding depression

Hybrids whose parents are *too dissimilar* are unfit. Real
biology calls this outbreeding depression — the hybrid offspring
fails to thrive because the parental gene pools are
incompatible. For sylk: a hybrid whose parental signature
distance exceeds an outbreeding threshold has its bonus
inverted to a penalty. The substrate flags it for review rather
than reinforcement.

This is the operational signal for "agents tried to synthesize
across regions that genuinely don't synthesize" — e.g., a
node attempting to bridge user-preference work and architectural
constraint work, where the two regions speak different
languages and the hybrid is incoherent.

The outbreeding threshold is derived: it's the q95 of intra-
hybrid signature distances across all hybrids in the forest's
history. Hybrids beyond q95 are statistically unusual and worth
flagging.

### Cluster merge driven by hybrid accumulation

When the *count* of hybrids on a bridge exceeds a threshold
(anchored on the smaller of the two clusters' sizes), the
forest interprets it as a sign that the two clusters have
genuinely merged in the user's working — they are no longer
distinct topics. A merge is triggered without waiting for the
slower density-connectivity check.

This makes cluster merging *user-pace responsive*: the substrate
registers conceptual fusion as it actually happens, via the
hybrid nodes that the user/agents create at the bridge.

## Climate, Microclimate, and Photosynthesis

The forest doesn't operate in vacuum. Resources don't appear
spontaneously; they are produced from the user's interactions
with the substrate. The original Forest design treats this
implicitly — events arrive, they trigger updates. This section
makes the energy flow explicit, in line with how real
ecosystems are studied.

### Photosynthesis — resources from input

Every interaction the user has with sylk is a **photon** —
energy entering the system. The substrate's resource channels
are the **organic compounds** synthesized from that energy:

| Energy input | Substrate channel produced | Mechanism |
|---|---|---|
| User query / refinement | Phosphorus (intent) | Direct from query event |
| Tool result / external citation | Carbon (evidence) | Direct from retrieval event |
| Validation / Guardian constraint | Nitrogen (correctness) | Direct from validation event |
| Any access (read, recall) | Water (recency) | Direct from access event |

The total budget of substrate resources at any moment is
*conserved over photosynthesis*: it cannot exceed the integral
of input events times their conversion rates, minus losses to
decay. Without input, resource pools shrink; the forest
contracts; eventually growth halts.

This is not a metaphor — it's a literal accounting invariant
the maintenance loop must respect. The current design has no
such invariant; resources can drift up arbitrarily through
edge-deposit rules. Making photosynthesis explicit forces the
substrate to be **energy-conservative** under decay, which
makes the dynamics much more stable across long time horizons.

### Photosynthetic agent profiles

Different agents differ in which resources they produce
efficiently:

| Agent | Carbon | Nitrogen | Phosphorus | Water | Ecological analog |
|---|---|---|---|---|---|
| Academic | High | Low | — | Medium | Coniferous tree (slow but durable carbon) |
| Librarian | Low | — | — | High | Succulent (water storage) |
| Guardian | — | High | — | — | Nitrogen-fixing legume |
| Engineer/Designer | — | — | Consumes | — | Pioneer species (fast growth, high consumption) |
| Tester | Medium | Medium | — | — | Decomposer (recycles via validation) |
| Architect | — | High | High | — | Apex predator (concentrates nitrogen, consumes intent) |
| Inspector | — | Medium | — | Medium | Detritivore (cleans up via tests, refreshes water) |

These profiles are not arbitrary — each agent's natural work
produces the channel its actions semantically signal. Academic
work is evidence-rich (carbon); Guardian work is correctness-
declarative (nitrogen). The mapping is mechanical, not tunable.

### Climate — the ambient gain control

Climate is the global multiplier on photosynthesis and decay.
It tracks the **user activity rate** at multiple time scales:

```
climate.sun_intensity = activity_rate(window_short) / activity_rate(window_long)
```

Where `window_short` is the past hour and `window_long` is the
past week. A `sun_intensity` of 1 means the user is engaging at
their typical pace; > 1 means they're in a hot window; < 1 means
they're cooling off.

Climate gates substrate dynamics:

- **Decay rate** scales inversely with `sun_intensity` — hot
  periods preserve nodes longer (the user keeps re-validating);
  cold periods accelerate decay (the user has moved on).
- **Speciation threshold** scales with `sun_intensity` — hot
  periods make new clusters easier to crystallize; cold periods
  keep noise in noise.
- **Maintenance cadence** scales inversely — hot periods run the
  loop more often (more events to process); cold periods relax.

Climate is a single derived scalar; computing it costs a
constant per maintenance cycle.

### Microclimate — per-cluster local conditions

Different topics have different working rhythms. Code work has
fast turnover (tropical microclimate); architecture decisions
are slow and stable (alpine microclimate). The substrate should
respect this — applying tropical decay rates to alpine clusters
is wrong.

Per-cluster microclimate is the cluster-local equivalent of
global climate: activity rate within the cluster vs the
cluster's own historical baseline. Microclimate gates apply
locally, layered on top of global climate:

```
effective_decay(cluster) = global.decay_base × climate.sun_intensity^(-1) × microclimate(cluster)
```

This is straightforward gain control — no new substrate
primitives, just per-cluster modulation of existing ones.

### Carbon sinks — Climax as long-term storage

Climax-stage nodes accumulate carbon (evidence weight) over
time and release it slowly. They are the forest's **carbon
sinks** — long-term storage of past photosynthesis. During
drought, carbon sinks subsidize the rest of the forest:
maintenance cycles draw from sink reserves before letting
growth-stage nodes starve.

This makes Climax nodes structurally valuable beyond their
direct retrieval contribution. They are the forest's
**reserves**.

### Seasonality — predictable cycles

User activity has predictable rhythms: daily (morning vs
evening), weekly (workday vs weekend), monthly or quarterly
(project phase). The forest should anticipate these rather
than treat them as noise.

A simple Fourier decomposition of the activity time series
reveals dominant cycles. The maintenance loop reads the current
seasonal phase and adjusts:

- **Predicted high-activity window approaching**: pre-warm
  Climax nodes near recent active regions; raise the speciation
  threshold floor (don't crystallize candidates that won't
  survive past the window).
- **Predicted low-activity window approaching**: drop the
  decay-priority floor (let stragglers go); harvest carbon into
  Climax sinks; pre-pause non-essential maintenance.

Seasonal adaptation is **anticipatory** — the forest acts
before the climate change, not after. This is a property real
forests have through phenological adaptation; sylk gets it
because it can read the activity time series directly.

## Disturbance Regimes

Real ecosystems are punctuated by disturbances — events that
disrupt steady-state operation. Each disturbance type has
characteristic dynamics; each has a recovery curve. The forest
needs distinct response profiles for each.

The four disturbance types relevant to sylk:

### Drought

**Trigger**: sustained low photosynthesis. Operationally, the
user has been engaged at < q10 of their historical activity
rate for ≥ a derived window (anchored on the median session
duration).

**Forest response**:
- Tighten maintenance cadence (less work; less consumption).
- Pause speciation entirely (no new clusters during drought).
- Promote Climax nodes to **carbon sink** mode — their reserves
  subsidize Mature/Sapling nodes.
- Suspend non-essential PoI computation (hot zones, frontier).
- Retain only essential PoI (boundary, brittle, bridges).

**Recovery**: when activity returns, drought lifts gradually
(hysteresis — don't unlock all dynamics at once). The substrate
spends one full maintenance cycle in "convalescence" — partial
return to normal — before resuming full operation.

### Hard winter

**Trigger**: sudden total resource constraint. The substrate's
budget drops by > 50% in a single cycle (e.g., container
restart wipes warm caches; model rotation invalidates
embeddings; explicit operator throttle).

**Forest response**:
- All non-Climax nodes enter **dormancy**: state preserved, but
  not loaded into the active subgraph for retrieval scoring.
- Only **cold-hardy seeds** stay loaded — a small set of nodes
  with both high vigor and high immunity diversity, anchored at
  `canonicalKindCount` per cluster.
- Maintenance loop runs at minimum cadence (one cycle per
  hour, maintenance work limited to dormancy preservation).

**Recovery**: when the constraint lifts, **spring** — dormant
nodes rehydrate over a derived window; the forest's full
dynamic surface returns gradually.

### Fire

**Trigger**: explicit operator action (e.g., "forget everything
about cluster X") OR a major architectural rewrite that
invalidates a cluster's foundational evidence (e.g., a Guardian
declaration deprecating an entire region's premise).

**Forest response**:
- The fire region transitions all member nodes to **Snag**
  immediately — no slow decay, immediate burn.
- Before the burn, harvest a **seed bank**: a representative
  sample of high-immunity, high-signature-magnitude nodes is
  preserved as Climax-marked Snags. They survive the fire and
  can re-activate during regrowth if their content is
  re-encountered.
- Fire is **disease-quarantined**: it must not spread to
  neighbor clusters. The substrate explicitly blocks bridge
  edges from the fire region during the burn cycle.

**Recovery**: **succession** — pioneer nodes regrow at the
edges of the burn region; some of them connect to seed-bank
Snags and revive that lineage. The recovered region passes
through Pioneer → Sapling → Mature stages over time, just like
natural post-fire succession.

### Flood

**Trigger**: input rate exceeds the projector's processing
capacity for > a derived burst window. Operationally: ledger
event arrival rate exceeds the projector's effective batch
rate.

**Forest response**:
- **Buffer** to the ledger — events queue, but the projector
  doesn't drop them.
- Maintenance loop pauses entirely during flood; all cycles
  serve projection catch-up.
- **Bounded queue with backpressure**: if the ledger queue
  exceeds its derived cap (anchored on `canonicalKindCount ×
  events per second per channel × derived burst window`), the
  flood signal propagates upstream so producers can throttle.
- **Triage**: validations and Guardian declarations have priority
  over incidental edges (`co_activated_with`, etc.). The
  projector applies high-priority events first; lower-priority
  events catch up after.

**Recovery**: flood subsides; the projector catches up to the
watermark; maintenance loop resumes.

### Critical slowing down — the early warning

Across all four disturbance types, a single signal warns of
approach: **critical slowing down**. As a system approaches a
tipping point — a regime shift between alternate stable states
— its variance and autocorrelation rise; perturbations take
longer to relax (Scheffer 2009 *Nature*, "Early-warning signals
for critical transitions"; Dakos et al. 2008 PNAS).

Per cluster, the substrate tracks:

```
slowdown_signal(cluster, t) =
    autocorr(spectral_signature(cluster), lag) × variance(spectral_signature(cluster))
```

When `slowdown_signal` exceeds the cluster's historical baseline
by > 1 σ, the substrate flags the cluster as **at risk**. The
flag is consumed by:

- **Replay scheduler**: prioritize at-risk clusters for replay.
  Correction is cheaper before a regime shift than after.
- **Maintenance loop**: extend maintenance budget for at-risk
  clusters. Whatever's brewing, address it now.
- **Guardian / Architect alert**: at-risk clusters surface in
  the architect's claim queue as preventive remediation
  candidates.

This is the substrate's mechanism for **anticipatory
intervention**. Real ecosystems have analogues — coral reefs
show critical slowing down before bleaching; lake ecosystems
show it before eutrophication. The mathematical framework is
the same; the forest borrows it.

### Resilience versus resistance

Two distinct properties matter for disturbance handling:

- **Resistance**: the substrate's ability to absorb a
  disturbance without changing structure. High-resistance
  forests have strong Climax cores, broad immunity vectors,
  and many nutrient-rich Carbon sinks.
- **Resilience**: the substrate's ability to recover after a
  disturbance has caused change. High-resilience forests have
  strong seed banks, fast pioneer regrowth, and effective
  succession dynamics.

The two are tradeable. A forest hyper-optimized for resistance
(everything Climax, no Pioneer) can't recover from disturbances
that overwhelm its resistance. A forest hyper-optimized for
resilience (lots of Pioneer, light Climax) recovers fast but
gets disrupted often. Healthy forests balance both.

The substrate's **resilience metric** (a derived value combining
seed-bank size, pioneer arrival rate, and recovery-curve speed
from past disturbances) is published as a forest-level
diagnostic. Operators can read it; agents can read it; the
architect can prioritize work on regions whose resilience is
declining.

### Tipping points and regime shifts

Some disturbances do not lead to recovery — instead, the system
shifts to an alternate stable state. The Lotka-Volterra
bistability regime is one example: a fire that destroys the
weaker of two competing clusters allows the other to capture
the territory permanently. The forest does not recover the
prior state; it stabilizes in a new configuration.

Tipping points are detected ex post by **regime-shift
analysis**: a sudden, persistent change in cluster composition
that doesn't unwind via normal recovery dynamics. The forest
records these as **regime events** in `forest_cluster_lineage`
— first-class history. They are the substrate's record of
"this is the moment the user's understanding fundamentally
changed".

Regime events drive Architect remediation: the architect is
informed of the shift and can post claims to update affected
infrastructure (renames, deprecations, migration of historical
references).

## The Forest as Participant

The substrate dynamics covered above describe what the forest
*does*. This section describes how the forest *speaks* — how its
signals reach the agent ecosystem, how its decisions get
ratified, and how each agent consumes its outputs differently.

The premise: the forest is itself a participant in the agent
ecosystem, not just substrate the agents use. It uses the same
protocol — testaments with artifacts, claims with validations —
that every other agent does. There is no special signaling
mechanism. The forest is an agent (the **Substrate Agent**, with
agent ID `system:substrate`) that posts testaments, issues claims,
and receives validations like any peer.

This framing has three operational consequences:

1. **The forest is auditable** — every consequential decision
   leaves a testament with provenance.
2. **The forest is accountable** — substrate-level decisions go
   through agent ratification before taking effect.
3. **Each agent consumes the forest differently** — subscriptions
   are role-typed; not every agent sees every signal. Each agent
   is different.

### The Substrate Agent

The Substrate Agent posts testaments at maintenance-loop cadence,
with artifacts batched per cycle. Fabric (per `docs/FABRIC.md`)
carries them like any other testament — visible to subscribers,
observable by chokepoint instrumentation, durable to the activity
ledger. No new infrastructure: forest signals ride the existing
testament-and-artifact pipeline.

Three subscription tiers govern fanout, derived from the project
rule that observational paths must not drop signals while remaining
within bounded queues:

- **Trace baggage tier** — a small subset of signals so universally
  relevant they ride in fabric trace baggage on every cross-agent
  message. Sub-256-byte cost; effectively zero overhead per
  message; received by every agent automatically.
- **Topic-subscription tier** — testament-and-artifact, fetched on
  demand. Agents register interest in specific artifact kinds; only
  matching signals dispatch to them.
- **Authority-stream tier** — substrate-issued claims, addressed to
  specific subject agents. Recipient determined by the claim's
  `subject` field; flows through the existing claims pipeline.

### Forest-Emitted Artifacts

Every forest event becomes a `claims.Artifact` whose `kind`
identifies the event type and whose `reference` carries the
structured payload. Variable-shape payloads are JSON-encoded;
hot-path payloads (cursor, baggage) are binary-packed.

The catalog:

| Artifact kind | Emitted on | Reference content |
|---|---|---|
| `forest_cursor` | Every cross-agent message (carried in fabric baggage) | `{cluster_ids[], focal_node_id, signature_readout, poi_markers[], stage}` |
| `cluster_speciated` | New cluster crystallizes from noise | `{cluster_id, sample_nodes[], named_at, density_profile}` |
| `cluster_renamed` | Curator updates cluster name | `{cluster_id, old_name, new_name, rationale}` |
| `cluster_merged` | Two clusters fuse | `{merged_id, parent_a, parent_b, bridge_count}` |
| `cluster_split` | Cluster fissions on density valley or spectral bifurcation | `{parent_id, daughter_a, daughter_b, split_signal}` |
| `bridge_crossed` | Action transitions a bridge node | `{source_cluster, target_cluster, bridge_node_id}` |
| `stage_transitioned` | Node moves between stages | `{node_id, from_stage, to_stage, signals}` |
| `critical_period_opened` | Climax→Mature reopening on contradiction load | `{node_id, contradiction_load, expected_duration}` |
| `pathogen_declared` | BCM threshold collapse or operator declaration | `{pathogen_id, source_node_id, embedding, declared_by}` |
| `pathogen_mutated` | Drift through `refines`/`responds_to` | `{parent_pathogen_id, child_pathogen_id, drift_distance}` |
| `node_exposed` | Node enters Exposed state | `{node_id, pathogen_id, exposure_intensity, source}` |
| `node_quarantined` | Node Infected; retrieval suppressed | `{node_id, pathogen_id, infection_level}` |
| `node_recovered` | Cited-validation testament cleared infection | `{node_id, pathogen_id, immunity_gain}` |
| `outbreak_detected` | Spectral signature deviation | `{cluster_id, fiedler_drop, top_modes[], magnitude}` |
| `slowdown_warning` | Critical slowing down detected | `{cluster_id, autocorrelation, lead_time_estimate}` |
| `disturbance_declared` | Drought / winter / fire / flood detected or declared | `{type, region, severity, recovery_phase}` |
| `seed_bank_preserved` | Pre-fire harvest of high-value nodes | `{fire_event_id, preserved_nodes[], signature_summary}` |
| `succession_progressed` | Post-disturbance stage advancement | `{cluster_id, new_stage, recovery_completion}` |
| `regime_shift_recorded` | Lotka-Volterra basin transition completed | `{from_state, to_state, affected_clusters[], ts_irreversible}` |
| `competitive_exclusion` | Cluster size driven below extinction threshold | `{exiled_cluster, dominant_cluster, alpha_matrix}` |
| `hybrid_emerged` | New node with multi-cluster parentage | `{hybrid_node_id, parent_clusters[], hybrid_score, parental_distance}` |
| `climate_shifted` | Sun intensity or seasonal phase crossed threshold | `{old_state, new_state, anticipated_effect}` |
| `replay_priority_elevated` | Brittle / underused-gold / immune-protected node surfaced | `{node_id, priority_score, signal_components}` |
| `allelopathy_extended` | Guardian declares broader scope | `{pathogen_vector, scope, declared_by}` |
| `vaccination_applied` | Region pre-emptively immunized | `{cluster_id, pathogen_vector, scope}` |

Each entry is a regular `claims.Artifact` — no separate event
format. The artifact's `metadata` field carries any structured
secondary content (e.g., the spectral mode eigenvectors for
`outbreak_detected`, packed as a binary blob).

### Trace Baggage Tier

A subset rides in fabric trace baggage on every cross-agent
message rather than as separate testaments. These are the signals
agents need *constantly*, not on demand:

- `forest_cursor` — current cluster + focal node + signature.
- `poi_markers` for the focal region.
- `stage` of the focal node.
- `disturbance_state` if active (drought / winter / fire / flood).
- `infection_level` of the focal region.

Total baggage size is bounded by the trace-context budget — packed
binary representation keeps it well under 256 bytes per message.
Cost on every chokepoint is negligible relative to the rest of
the trace context already carried.

Everything else is testament-and-artifact, fetched on demand by
agents whose role requires it.

### Forest-Issued Claims

The substrate doesn't act unilaterally on consequential decisions.
Speciation, naming, merge, split, regime-shift, pathogen-
declaration, fire-trigger, vaccination-extension — each is a
*proposal* that the substrate posts as a claim addressed to the
appropriate authority. The decision applies only after the
authority's testament arrives.

This is the substrate **submitting to agent governance**.

| Claim posted by | Subject (validator) | Action requested | Validation accepts on |
|---|---|---|---|
| Substrate (curator hook) | Archivalist | "Name new cluster: candidate_id={...}, sample={...}" | Testament with proposed name |
| Substrate (speciation gate) | Guardian | "Approve scope of new cluster X (potential policy implications)" | Inspection testament |
| Substrate (BCM-collapse hook) | Architect | "Resolve sustained contradiction at node Y: {paths}" | Replacement claims posted (corrective action) |
| Substrate (outbreak detector) | Architect | "Pathogen P propagating in cluster C; remediate" | Quarantine + cure declarations |
| Substrate (replay scheduler) | Cluster steward (Archivalist by default) | "Refresh validation on brittle node N" | Receipt validation |
| Substrate (replay scheduler) | Cluster steward | "Re-cite underused-gold node N from cluster C" | Receipt validation |
| Substrate (cohesion check) | Guardian | "Cluster C is splitting; approve daughters' scopes" | Inspection testament |
| Substrate (merge check) | Archivalist | "Cluster A and B are merging; propose unified name" | Naming testament |
| Substrate (slowdown detector) | Architect | "Cluster C approaching regime shift; review preventive options" | Architect's choice (intervene or accept) |
| Substrate (disturbance detector) | Orchestrator | "Drought detected; reduce dispatch rate to non-essential agents" | Receipt validation |
| Substrate (fire-trigger hook) | Guardian | "Operator-requested forget on cluster C; confirm scope of burn" | Inspection + receipt |
| Substrate (post-fire) | Archivalist | "Approve seed-bank preservation list for fire on cluster C" | Inspection testament |
| Substrate (hybrid emergence) | Both parent clusters' stewards | "New hybrid node N bridges your clusters; validate" | Joint validation |
| Substrate (vaccine selection) | Guardian | "Pathogen P drift exceeds coverage; propose new vaccination scope" | Vaccination claim posted |

Each claim flows through the existing claims pipeline. The
substrate has no special path; it posts via `PostAction`, the
validator submits a testament, the decision is recorded in the
ledger, projections update on the next cycle.

The substrate's own work continues asynchronously while waiting
for ratification. It does not block; the unratified state is the
*current* state until the testament arrives.

### The Substrate as Claim Subject

The substrate is also a SUBJECT of claims — agents post claims
TO the substrate. These are the operations agents request the
substrate to perform on their behalf:

| Claim posted by | Subject | Action requested |
|---|---|---|
| Architect | substrate | "Declare pathogen P with embedding {...}; quarantine matching nodes" |
| Architect | substrate | "Mark cluster C as fire candidate; harvest seed bank" |
| Guardian | substrate | "Vaccinate region R against pathogen P; allelopathic broadcast" |
| Operator (via Guide) | substrate | "Pin node N; protect from pruning" |
| Operator (via Guide) | substrate | "Merge clusters A and B; user has reframed" |
| Operator (via Guide) | substrate | "Forget cluster C; trigger fire" |
| Archivalist | substrate | "Promote candidate cluster K to named cluster with name N" |
| Tester | substrate | "Vouch for node V against pathogen P; immunity gain claimed" |
| Inspector | substrate | "Declare this finding as pathogen P (declaration_event=...)" |
| Academic | substrate | "External research suggests pathogen P; declare prophylactically" |

The substrate validates these by checking authority — does the
claimant have permission for the requested action? Authority is
codified per-claim-type in the authority table (next subsection).
Unauthorized claims are rejected with the constraint cited.

### Authority Table

The authority table maps `(agent_type, claim_action)` pairs to
permission. Codified in `core/forest/claim_authority.go`:

```go
// authorityMatrix declares per-agent-type permission to post each
// substrate-directed claim action. Built once at service start
// from agentProfiles.OutboundClaimKinds.
var authorityMatrix map[string]map[string]bool
```

Default matrix (entries are claim actions an agent may post; all
others rejected):

| Agent | Authorized substrate-directed actions |
|---|---|
| Architect | declare_pathogen, mark_fire, request_vaccination, propose_remediation, request_critical_period, override_quarantine |
| Guardian | declare_allelopathy, vaccinate_region, approve_speciation, reject_speciation, declare_policy_pathogen |
| Archivalist | propose_cluster_name, promote_candidate, deprecate_cluster, curate_seed_bank |
| Inspector | declare_finding_as_pathogen, vouch_for_node |
| Tester | vouch_for_node, post_immunity_gain |
| Academic | propose_external_pathogen, propose_evidence_gap |
| Operator (via Guide) | pin_node, suppress_node, merge_clusters, split_cluster, rename_cluster, mark_fire, declare_user_pathogen |
| Engineer | (none — Engineer affects substrate via testaments, not direct claims) |
| Designer | (none) |
| Orchestrator | acknowledge_disturbance |
| Scribe | (none) |
| Librarian | (none) |

Authority is **per-claim-type, not per-agent globally**. This
keeps powerful operations gated to the agents whose role
warrants them. Operators (via Guide) have broad authority because
the user is the system's principal; ordinary worker agents have
narrow scope.

Authority changes flow through ledger events like everything else:
an operator can grant or revoke specific authorities by posting a
`grant_substrate_authority` claim that Guardian validates.

### Per-Agent Forest Profiles

Each agent has a distinct forest profile — what artifact kinds it
subscribes to, what it produces, how it participates in substrate
dynamics, what claims it can issue. Twelve agents, twelve profiles.

#### Engineer

- **Subscribes to**: `forest_cursor` (always), `poi_markers` for
  *boundary* and *brittle* in implementation regions,
  `pathogen_declared` events with embeddings near code-pattern
  space (deprecated APIs, anti-pattern bug shapes),
  `cluster_renamed` for clusters covering active codebases.
- **Produces**: implementation artifacts (`code_reference`,
  `diff`, `test_output`); `responds_to` and `refines` edges to
  prior implementation nodes.
- **Substrate dynamics**: high phosphorus consumption (intent →
  work). Pioneer species in succession terms — fast growth, high
  consumption, recovers fast after fire.
- **Disease role**: vector. `cites` and `refines` edges *transmit*
  pathogens (a buggy pattern propagates through code reuse).
  Engineer testaments must explicitly cite pathogens to confer
  immunity; implicit reuse spreads.
- **Forest drives by**: pre-filled cluster precedent for
  implementation patterns; warns about pathogens (deprecated
  APIs); pulls in cross-cluster steward when work spans clusters;
  suggests validations from cluster history; surfaces brittle
  dependencies the implementation will rest on.
- **Substrate-claims interaction**: receives "refresh validation
  on brittle implementation node N" claims; responds with
  receipt + revalidation testaments. Receives "this node is in a
  critical-period region; revalidate before reuse" warnings.
- **Disturbance role**: pioneer recolonization — first to regrow
  after fires (reimplementation in burned regions). Heat-tolerant
  (thrives in hot working windows).
- **Skill additions**: `forest_query_cluster_precedent`,
  `forest_query_brittle_dependents`.

#### Designer

- **Subscribes to**: `forest_cursor`, `poi_markers` for *boundary*
  (UI patterns frequently have competing options),
  `cluster_renamed` for design-pattern clusters,
  `competitive_exclusion` events (when one design framing
  dominates another).
- **Produces**: design artifacts (`code_reference`, `diff`,
  `note`); `forks_from` edges (alternative proposals); `refines`
  edges to prior designs.
- **Substrate dynamics**: high phosphorus consumption; moderate
  carbon production; pioneer in design exploration, mature in
  established systems.
- **Disease role**: vector through aesthetic/pattern propagation.
  Stale design assumptions are pathogens that propagate through
  `refines`.
- **Forest drives by**: surfaces existing UI patterns from cluster
  precedent; flags boundary contradictions ("design language X
  vs Y conflict here"); pulls preference cluster signals; alerts
  to cluster competitive dynamics ("this framing is being
  outcompeted by alternative Z").
- **Substrate-claims interaction**: receives critical-period
  notices when prior design decisions are being reopened;
  receives "validate this hybrid design that bridges cluster A
  and B" requests when cross-domain.
- **Disturbance role**: similar to Engineer — pioneer regrowth,
  heat-tolerant.
- **Skill additions**: `forest_query_cluster_precedent`,
  `forest_query_competing_framings`.

#### Tester

- **Subscribes to**: `forest_cursor`; validation patterns from
  cluster precedent; `outbreak_detected` (high priority —
  outbreaks need test coverage); `pathogen_declared` (need to
  test for the pathogen).
- **Produces**: testaments with `test_output` artifacts (nitrogen
  — correctness production); `validates` edges that contribute
  to immunity vectors when cited explicitly.
- **Substrate dynamics**: high nitrogen production; moderate
  carbon; water release on access.
- **Disease role**: **immune system**. Tester testaments are the
  substrate's primary mechanism for clearing infections. A
  Tester testament that explicitly cites a pathogen *immunizes*
  the validated node against it.
- **Forest drives by**: surfaces cluster's typical validation set
  ("here's what tests look like in this cluster"); flags
  incomplete validation ("this validation set is missing a
  kind"); triggers re-validation of brittle nodes; highlights
  outbreak zones for priority test coverage.
- **Substrate-claims interaction**: receives the heaviest stream
  of "validate against pathogen P" claims; posts cure
  testaments; receives "this cluster's validation precedent has
  drifted; please update" notices when override rates accumulate.
- **Disturbance role**: decomposer — recycles correctness
  pressure through validation. Active during recovery (validates
  regrowth).
- **Skill additions**: `forest_validate_against_pathogen`,
  `forest_query_validation_set`, `forest_immunize_node`.

#### Inspector

- **Subscribes to**: `forest_cursor`; *all* PoI markers (Inspector
  cares about all structural irregularities); `pathogen_declared`;
  `cluster_split`; `outbreak_detected`; `slowdown_warning`;
  `node_exposed`.
- **Produces**: testaments with `inspection`,
  `error_diagnostic`, `error_trace` artifacts (nitrogen +
  diagnostic); `contradicts` edges (the immune-system corrective
  edge).
- **Substrate dynamics**: nitrogen + water; cleans up via
  inspection, refreshes recency.
- **Disease role**: **immune system + immune-memory recorder**.
  Inspectors document *which pathogens corrupted which nodes*,
  building the mutation tree's annotation. Inspector testaments
  seed pathogen embeddings.
- **Forest drives by**: surfaces cluster's anti-patterns
  (history of contradictions / past failures); flags
  Exposed/Infected nodes for active inspection; raises priority
  on critical-slowdown clusters; pulls Inspector into outbreak
  zones.
- **Substrate-claims interaction**: receives "inspect this
  potential pathogen and confirm" claims; *posts* "declare this
  finding as pathogen P" claims to substrate; receives "verify
  this cluster's spectral signature" requests.
- **Disturbance role**: detritivore — cleans up dead/diseased
  nodes during recovery; first responder for outbreaks.
- **Skill additions**: `forest_declare_finding_as_pathogen`,
  `forest_query_anti_patterns`, `forest_verify_spectral_signature`.

#### Architect

The most deeply forest-coupled agent. The architect is the
canonical author of corrective actions; the forest's primary
steering signals flow to the architect.

- **Subscribes to**: full forest macro stream —
  `outbreak_detected`, `slowdown_warning`, `regime_shift_recorded`,
  `pathogen_declared`, `competitive_exclusion`,
  `disturbance_declared`, `cluster_split`, `cluster_merged`,
  `critical_period_opened`. Subscribes more aggressively than any
  other agent.
- **Produces**: corrective claims (with `replacement_claims` slot
  populated); pathogen declarations (architect can declare
  pathogens prophylactically); fire signals (for major rewrites);
  rename/merge proposals; *meta* claims (claims about the
  substrate's behavior itself).
- **Substrate dynamics**: nitrogen + phosphorus (correctness
  pressure + intent direction); apex predator in trophic terms.
- **Disease role**: **vaccine designer**. Architect declares
  pathogens proactively; defines the corruption-vector that
  Guardian then broadcasts as allelopathy.
- **Forest drives by**: this is the deepest connection in the
  entire design. Forest *streams* critical signals to architect;
  architect's claim queue is *populated by* substrate emissions.
  The architect's primary work loop is:
  1. Read forest critical-stream from fabric.
  2. Triage: outbreak / regime-shift / contradiction-collapse /
     brittle-cluster / drift.
  3. For each: post claim with proposed remediation.
  4. Validate when testaments return; commit corrective action.
- **Substrate-claims interaction**: most active issuer of
  substrate-directed claims; most active recipient of
  substrate-issued claims. Architect *is* the steering layer.
- **Disturbance role**: regime-change coordinator — issues fire
  claims for major rewrites; coordinates seed-bank preservation;
  declares drought/winter when system-wide pause is needed;
  manages the recovery curve.
- **Skill additions**: `forest_review_critical_stream`,
  `forest_propose_remediation`, `forest_declare_pathogen`,
  `forest_request_fire`, `forest_coordinate_recovery`.

#### Guardian

- **Subscribes to**: `cluster_speciated` (review new scope);
  `pathogen_declared` (decide whether to vaccinate broadly);
  `bridge_crossed` for cross-policy clusters;
  `regime_shift_recorded` (policy may need to update);
  `vaccination_applied` (audit own actions).
- **Produces**: allelopathic declarations (vector-form on a
  pathogen embedding); vaccination claims (region + pathogen
  scope); hard-policy boundaries (which become substrate's
  allelopathic broadcasts).
- **Substrate dynamics**: high nitrogen production; allelopathic
  broadcast emitter (asymmetric, longer-range).
- **Disease role**: **vaccinator**. Guardian declares immunity
  scope before pathogens emerge; pre-empts categories of
  corruption.
- **Forest drives by**: speciation events trigger Guardian-review
  claims (does this new cluster's scope conflict with policy?);
  outbreak zones surface for vaccination consideration;
  cross-cluster bridge crossings get Guardian visibility.
- **Substrate-claims interaction**: posts most vaccination claims;
  posts allelopathy-extension claims; receives "your boundary is
  causing apparent competition between clusters X and Y, do you
  want to refine the scope?" notices.
- **Disturbance role**: emergency declarer — can declare fires
  for policy breaches; immune to most disturbances (Guardian's
  policy is steady-state, not weather-dependent).
- **Skill additions**: `forest_declare_allelopathy`,
  `forest_vaccinate_region`, `forest_review_speciation`,
  `forest_extend_pathogen_scope`.

#### Librarian

- **Subscribes to**: `forest_cursor` (for retrieval scope);
  `cluster_renamed` (re-index); `pathogen_declared` (filter
  polluted citations); `node_quarantined` (suppress in retrieval).
- **Produces**: retrieval results (water — recency/accessibility
  refresh on retrieved nodes); `cites` edges (carbon
  propagation).
- **Substrate dynamics**: high water retention (succulent
  metaphor — stores and slow-releases); citations propagate
  carbon to cited nodes.
- **Disease role**: vector — citations CAN transmit pathogens.
  Librarian's retrieval must respect immunity vectors and
  quarantine factors; otherwise clean queries get poisoned
  results.
- **Forest drives by**: cluster cursor narrows retrieval scope;
  PoI markers prioritize candidates (frontier > underused-gold
  > general); immunity vectors filter polluted citations;
  quarantine factors suppress diseased nodes from retrieval.
- **Substrate-claims interaction**: receives "refresh retrieval
  cache for cluster C" claims after merge/rename; receives
  drought-mode claims to throttle non-essential retrievals.
- **Disturbance role**: drought-resistant — water reserves help
  during low-activity; can keep historical access alive even
  when fresh photosynthesis is low.
- **Skill additions**: `forest_query_cluster_scope` (extended to
  filter by quarantine), `forest_query_immune_citations`.

#### Academic

- **Subscribes to**: `forest_cursor`; evidence-cluster signals;
  citation network state; low-density regions of carbon
  (research opportunities); `pathogen_declared` for
  citation-based pathogens.
- **Produces**: synthesized evidence (high carbon production);
  validations of evidence-grounding; *external* citation
  artifacts (carbon from outside the forest).
- **Substrate dynamics**: durable carbon producer (coniferous
  tree analogy — slow but persistent).
- **Disease role**: evidence-grounded contribution; resistant to
  most pathogens because outputs cite external sources; can be
  a vaccinator when external research finds existing forest
  assumptions outdated.
- **Forest drives by**: forest pulls Academic into citation gaps
  ("low-density evidence here, research needed"); flags
  evidence regions of high uncertainty (for confirmatory work);
  academic outputs become high-trust additions to cluster
  precedent.
- **Substrate-claims interaction**: receives "evidence gap
  detected; investigate" claims (low-priority but persistent);
  posts "external research suggests pathogen P; declare in
  substrate" claims when research finds outdated assumptions.
- **Disturbance role**: drought-resistant — carbon reserves
  help. Climax-stage Academic outputs are major carbon sinks
  (long-stable evidence).
- **Skill additions**: `forest_query_evidence_gap`,
  `forest_propose_external_pathogen`,
  `forest_query_confirmatory_targets`.

#### Archivalist

The cluster steward agent — the natural recipient of most
substrate-issued maintenance claims.

- **Subscribes to**: `cluster_speciated` (must name);
  `cluster_renamed` (record); `cluster_merged` and
  `cluster_split` (lineage); `replay_priority_elevated` (brittle
  / underused-gold notifications); `regime_shift_recorded`;
  `seed_bank_preserved`.
- **Produces**: cluster names (curatorial); lineage records;
  deprecation announcements; seed-bank preservation testaments.
- **Substrate dynamics**: keeper of history — reads more than
  writes; long water retention.
- **Disease role**: **institutional memory** — preserves
  immunity through dormancy; maintains pathogen registry;
  tracks mutation trees.
- **Forest drives by**: speciation events route to Archivalist
  for naming; brittle-node maintenance claims default to
  Archivalist as cluster steward; lineage queries answered by
  Archivalist; pre-fire seed-bank curation request goes to
  Archivalist.
- **Substrate-claims interaction**: receives the second-most
  claims after Architect; usually receipt-class validation work
  (acknowledge, record, surface); occasionally posts "rename
  cluster X based on drift" claims.
- **Disturbance role**: seed bank guardian — keeps Climax
  preserved through fires; steward of recovery (curates which
  seeds reactivate).
- **Skill additions**: `forest_propose_cluster_name`,
  `forest_query_lineage`, `forest_curate_seed_bank`,
  `forest_promote_candidate`.

#### Orchestrator

- **Subscribes to**: full forest health (macro-state);
  `disturbance_declared` (adjust dispatch); `cluster_speciated`
  / `cluster_merged` (re-tune routing); `climate_shifted`
  (gate pace).
- **Produces**: dispatches with forest cursor in context; rate
  decisions.
- **Substrate dynamics**: doesn't directly produce or consume;
  reads state.
- **Disease role**: doesn't transmit; can route around diseased
  clusters.
- **Forest drives by**: forest tells Orchestrator which clusters
  are hot/cold; which agents to spin up; whether to delay
  non-urgent dispatch during drought; whether to triage during
  flood.
- **Substrate-claims interaction**: receives "drought; throttle
  non-essential" claims; receives "outbreak in cluster C;
  prioritize Inspector dispatch there"; rarely posts
  substrate-directed claims.
- **Disturbance role**: traffic controller — adjusts dispatch
  rate based on disturbance state; activates flood-triage
  protocols.
- **Skill additions**: `forest_query_climate_state`,
  `forest_acknowledge_disturbance`.

#### Scribe

- **Subscribes to**: `forest_cursor` for summary scope;
  `poi_markers` for what to highlight; agent activity in active
  clusters.
- **Produces**: summaries; recording artifacts (water —
  preserves accessibility).
- **Substrate dynamics**: water producer (preserves
  accessibility through summarization).
- **Disease role**: doesn't propagate; summaries can carry
  pathogen markers if they cite diseased sources, but Scribe is
  downstream of retrieval which already filters.
- **Forest drives by**: forest cursor shapes summary scope; PoI
  markers indicate what to highlight; cluster lineage informs
  the framing of summaries.
- **Substrate-claims interaction**: receives "summarize this
  cluster's recent evolution" claims; rarely posts
  substrate-directed claims.
- **Disturbance role**: archivist during disturbance — captures
  state at moment of fire/winter for later recovery.
- **Skill additions**: `forest_query_summary_scope`.

#### Guide

- **Subscribes to**: forest map at high level; user activity
  patterns; `climate_shifted` (sense user's working state);
  `disturbance_declared` (route accordingly).
- **Produces**: routing decisions with forest cursor attached;
  user-facing forest narration ("we're working in cluster X").
- **Substrate dynamics**: indirect — reads to route.
- **Disease role**: routes through diseased regions appropriately
  (avoids when possible); user-facing forest health is partly
  Guide's responsibility.
- **Forest drives by**: cluster cursor in routing decisions;
  PoI markers shape which agent to route to (boundary →
  Designer; outbreak → Inspector + Architect; brittle →
  Archivalist); climate state shapes urgency cues to user.
- **Substrate-claims interaction**: posts "operator wants to
  merge clusters A and B" claims based on user input; posts
  "operator wants to forget cluster C; declare fire" claims;
  receives drought notices to surface to user.
- **Disturbance role**: senses the user's climate; surfaces
  relevant forest state to user; mediates user-issued substrate
  claims.
- **Skill additions**: `forest_narrate_state` (user-facing),
  `forest_relay_operator_action`.

### The Per-Agent Profile Registry

Profiles are codified in `core/forest/agent_profiles.go`:

```go
// AgentForestProfile is the per-agent declaration of which forest
// signals the agent consumes, what it produces, and what
// substrate-directed claims it can post.
type AgentForestProfile struct {
    AgentType string

    // Artifact kinds this agent subscribes to.
    SubscribedKinds []string

    // Substrate-claim types this agent can be a SUBJECT of.
    InboundClaimKinds []string

    // Substrate-claim types this agent can POST.
    OutboundClaimKinds []string

    // Photosynthesis profile (per Climate, Microclimate section).
    PhotosynthesisProfile Profile

    // Disease role: vector | immune | vaccinator | resistant.
    DiseaseRole DiseaseRole

    // Disturbance role: pioneer | decomposer | detritivore |
    // apex_predator | seed_bank | traffic_controller | mediator.
    DisturbanceRole DisturbanceRole
}

// agentProfiles is the canonical registry. Read at service start;
// drives subscription dispatch and authority enforcement.
var agentProfiles = map[string]AgentForestProfile{
    "engineer":     { /* per-spec above */ },
    "designer":     { /* ... */ },
    "tester":       { /* ... */ },
    "inspector":    { /* ... */ },
    "architect":    { /* ... */ },
    "guardian":     { /* ... */ },
    "librarian":    { /* ... */ },
    "academic":     { /* ... */ },
    "archivalist":  { /* ... */ },
    "orchestrator": { /* ... */ },
    "scribe":       { /* ... */ },
    "guide":        { /* ... */ },
}
```

The registry drives three runtime structures:

1. **Subscription dispatch** — when the substrate emits a
   testament with a given artifact kind, the dispatcher looks up
   which agents subscribe to that kind and routes the testament
   only to them. No blanket fanout.
2. **Authority enforcement** — when an agent posts a
   substrate-directed claim, the substrate looks up whether the
   agent's `OutboundClaimKinds` includes the claim's action.
   Unauthorized claims are rejected.
3. **Skill registration** — at agent construction, the per-agent
   `SkillAdditions` are registered with the agent's skill
   manager, surfacing the forest interaction at the skill layer.

### Phasing

The forest-as-participant features roll out in stages, each
preserving the existing system's correctness:

1. **Phase 1: Trace baggage tier.** `forest_cursor`,
   `poi_markers`, `stage` ride in fabric baggage. Read-only;
   agents can inspect but no behavior changes. Validates that
   the cursor is computed correctly and propagated reliably.
2. **Phase 2: Critical-stream subscriptions.** Architect,
   Guardian, and Inspector subscribe to their respective
   critical artifact kinds. They observe; they don't yet act on
   substrate-issued claims.
3. **Phase 3: Substrate-issued maintenance claims.** Brittle-node
   refresh and underused-gold replay claims start flowing to
   Archivalist as cluster steward. Low-stakes; failure mode is
   "maintenance work doesn't get done", not corruption.
4. **Phase 4: Speciation review.** Speciation events trigger
   curator + Guardian claims. Naming becomes claim-mediated;
   unnamed candidates remain in the holding pen.
5. **Phase 5: Pathogen declarations.** BCM-collapse hooks emit
   pathogen-declaration claims to Architect. Architect's queue
   is now populated by substrate signals. Quarantine activates
   on Architect's confirmation.
6. **Phase 6: Disturbance declarations.** Drought / winter /
   fire / flood detection emits disturbance claims.
   Orchestrator + Architect coordinate response. Recovery
   curves activate.
7. **Phase 7: User-mediated substrate operations.** Pin /
   suppress / merge / split / forget surfaces in the Guide as
   user-controllable operations. Operator authority extends
   substrate operations to the user.
8. **Phase 8: Full per-agent skill surface.** All twelve agents
   have their forest-interaction skills registered and
   exercised. Cross-agent forest coordination emerges as a
   first-class operational mode.

Each phase is gated on the previous phase being stable in
production. No phase introduces a hard dependency on a future
phase — the system is correct at each phase boundary.

### Non-goals

- Replacing existing claims pipeline. The substrate adds new
  claim types; it does not own the claim lifecycle.
- Replacing existing fabric. The substrate adds new artifact
  kinds; it does not redefine the trace format.
- Auto-permission. Substrate-supplied signals are never
  substitutes for explicit agent decisions. Every override is
  first-class.
- Synchronous coupling. The substrate posts claims and continues;
  agents validate asynchronously; everything is eventually
  consistent through the existing CQRS plumbing.
- Treating the substrate as authority. Authority resides with
  agents (Guardian for policy, Architect for remediation,
  Operator for user override). The substrate proposes; agents
  ratify.

## Real Diseases This Models

Compartmental (SIR/SEIR) works for diseases with stable antigens
and small variant counts: measles, polio, smallpox. Sylk's
forest-corruption problem doesn't have those properties — it has
continuous mutation, variant clouds, and graded cross-immunity.
The diseases that genuinely require continuous antigenic-space
modeling, and which match the forest's regime:

### Influenza — the canonical case

Hemagglutinin (HA) and neuraminidase (NA) are the antigens.
Antigenic drift — gradual point mutations in HA — produces a
continuous trajectory through antigenic space that escapes prior
immunity. Smith, Lapedes et al. 2004 (*Science*, "Mapping the
antigenic and genetic evolution of influenza virus") introduced
**antigenic cartography**: hemagglutination-inhibition assay data
mapped onto a 2-D continuous antigenic space, with strains as
points and population immunity as a vector field. This is
literally the data structure used to choose each year's flu
vaccine. The forest's pathogen-as-vector / immunity-as-vector
design mirrors this directly.

### HIV — within-host evolution

Within a single host, the gp120 envelope protein mutates fast
enough that the antibody repertoire chases an ever-drifting
target. The immune response is graded — antibodies trained on one
variant cover related variants by similarity. *Broadly
Neutralizing Antibodies* (bNAbs) are the "diversified immunity
vectors" that cover much of the antigenic space; HIV vaccine
research is essentially the search for the right immunity-vector
starting points to drive the immune system toward bNAb-like
coverage.

### SARS-CoV-2 variants — the recent demonstration

Alpha, Delta, Omicron and sub-variants form a tree in
spike-protein antigenic space. Cross-immunity from one variant is
graded by antigenic distance to the next. Bivalent and multivalent
boosters are explicitly *multiple immunity vectors*. The mRNA
platform makes vaccine design literally an act of choosing a
point in antigenic space.

### Plasmodium falciparum — malaria

The *var* gene family encodes ~60 surface protein variants per
parasite genome; switching expression evades immunity. Antigenic
space is high-dimensional with each *var* gene a coordinate.
There is no clean SIR boundary; immunity is graded coverage of
the var-space.

### Trypanosoma brucei — sleeping sickness

VSG (Variant Surface Glycoprotein) switching — same mechanism as
Plasmodium, even more extreme antigenic variation, ~1000 VSG
genes per parasite. The textbook example of antigenic variation
exhausting compartmental modeling.

### Borrelia burgdorferi — Lyme disease

VlsE protein antigenic variation through a recombination cassette
system. Immunity development is graded; SIR models cannot capture
the ongoing escape dynamics.

### Streptococcus pneumoniae — pneumococcal disease

~100 capsular serotypes; PCV vaccines target the most prevalent
~13–20 (a discrete sample of the continuous space). The selection
pressure from vaccines drives serotype replacement — which is
exactly the dynamic the forest's antigenic-field model
captures: when one region is well-immunized, corruption migrates
to adjacent uncovered regions.

### Cancer — the immunotherapy framing

Tumor neoantigens form a high-dimensional landscape. Checkpoint
inhibitors (anti-PD-1, anti-CTLA-4) raise the gain on existing
immunity vectors; CAR-T constructs explicit immunity vectors
against tumor antigens; tumor escape is antigenic drift away from
those vectors. The forest's spectral pruning is the structural
analog of CAR-T: explicit, targeted clearance of a specific
corruption mode rather than diffuse immune response.

**Pattern across all of these**: discrete states (S/E/I/R)
collapse the structure that matters. Continuous antigenic space +
graded cross-immunity + drift trajectories is what computational
immunology *actually* uses for vaccine design and outbreak
prediction. The forest is borrowing the framework that works.

## Natural Phenomena Being Mirrored

The deeper structures the design borrows from extend beyond
disease into the broader mathematical biology and physics
literature:

### Antigenic cartography and affinity maturation

Smith et al. 2004 (cited above) is the direct biological
precedent for the antigenic-vector design. **Affinity maturation**
— the process where B cells in germinal centers mutate their
antibody-coding genes and are selected for tighter antigen
binding — is the biological analog of the forest's cited-
validation immunity-vector update. Each cited validation is an
*affinity-matured antibody* against the specified pathogen.

### Quasispecies theory

Eigen's 1971 *Naturwissenschaften* paper (and later work) describes
RNA viruses not as single sequences but as a **cloud of related
sequences** around a master sequence, distributed by mutation rate
and fitness. Drug resistance and vaccine escape emerge from
selection within the cloud. The forest's pathogen field —
corruption represented as a vector with mutation drifting it
through related variants — is structurally a quasispecies model
applied to knowledge corruption.

### Spectral epidemiology

Pastor-Satorras & Vespignani 2001 (*Phys. Rev. Lett.*, "Epidemic
spreading in scale-free networks") showed that on power-law
networks, the **dominant eigenvalue of the adjacency matrix sets
the epidemic threshold** — outbreaks take off when transmission ×
λ_max > 1. Newman 2002 and Wang et al. 2003 extended this. The
forest detecting outbreaks via Fiedler-value drop in the
corruption-weighted Laplacian is the dual of the same theorem: as
λ₂ collapses, the network becomes "stuck" — paths through the
network are no longer typical, the corruption is concentrating.

The forest is built on a knowledge graph that follows power-law
degree distributions — the exact regime where spectral
epidemiology is most informative.

### Original Antigenic Sin

Francis 1960 documented (and now mainstream immunology confirms):
the immune system is biased toward the *first* strain it
encountered. New strains close in antigenic space are detected
easily; far ones are detected poorly. This is exactly the
asymmetry the forest's cosine-similarity-graded immunity gives:
established immunities help with neighboring corruptions and
hinder novel ones. The forest inherits this strength and
limitation deliberately.

### Coalescent theory

In population genetics, Kingman's 1982 coalescent describes how
genetic lineages converge backward in time to a common ancestor.
Mutations form a tree; tree topology reveals selection pressure.
The forest's pathogen-mutation tree (corruption drifting through
`refines` / `responds_to` edges) is structurally a coalescent
tree in pathogen-embedding space — and the same statistical tools
that detect selection in coalescent trees can detect *adversarial
mutation* in the pathogen tree (deliberate paraphrasing to escape
detection).

### Multi-component reaction-diffusion

Turing 1952 introduced the framework. The Emergent Forest already
uses Turing reaction-diffusion for the resource economy. Adding
the corruption field means the forest has *two coupled
reaction-diffusion systems* — resource and pathogen — competing
for spatial structure on the same graph. This is multi-component
reaction-diffusion, well-studied in developmental biology
(Murray's *Mathematical Biology* textbook; Kondo & Miura 2010
*Science* on animal pigment patterns). Cross-coupling between the
systems generates richer pattern formation than either alone —
exactly what makes the design behavior not reducible to either
system in isolation.

### Persistent homology

Carlsson 2009 (*Bull. AMS*, "Topology and data") established TDA
as a discipline. Edelsbrunner-Harer's textbook is the canonical
reference. Persistent homology in materials science (Lee et al.
2017 and others) detects dislocations and defects in crystal
structures via homology signatures. The forest's topological
pathology detection is this primitive applied to interaction
graphs: Betti number changes signal structural corruption that
spectral analysis misses.

### Renormalization group flow

The spectral approach (low-pass filtering, multi-scale
eigendecomposition) is structurally analogous to the
**renormalization group** in physics: integrate out high-frequency
modes to reveal macroscopic behavior. Each maintenance cycle, the
spectral filter coarse-grains the cluster's state. Pathological
perturbations show up as departures from the RG fixed point (the
cluster's resting spectrum). RG is *the* mathematical framework
for separating local fluctuation from systemic structure; the
forest borrows it directly.

### Kuramoto synchronization

In coupled oscillator networks, spectral properties of the
Laplacian determine synchronization regimes. Strogatz 2000
(*Physica D*) is the canonical reference. Healthy clusters in the
forest are *synchronized* — their nodes' substrate states evolve
coherently. Outbreaks are *desynchronization* — pathological modes
break the coherent regime. The forest inherits Kuramoto-style
spectral analysis for free.

### Graph signal processing

Shuman et al. 2013 (*IEEE Sig. Proc. Mag.*) and Ortega et al. 2018
(*Proc. IEEE*) established graph signal processing as a discipline.
The forest's spectral pathology detection is GSP applied to a
multi-channel graph signal (valence, activation, substrate
channels). Operations like graph Fourier transform, graph
convolution, and graph wavelets all become available — Lanczos
iteration makes them tractable on the maintenance-loop budget.

### Lotka-Volterra population dynamics

Lotka 1925 (*Elements of Physical Biology*) and Volterra 1926
(*Variazioni e fluttuazioni del numero d'individui in specie
animali conviventi*) independently derived the predator-prey and
competition equations that bear their names. The competition
form — `dN_i/dt = r_i N_i (1 - (N_i + Σ α_ij N_j)/K_i)` — is
the canonical model for two-species competition and underlies
nearly all subsequent niche theory.

The forest's inter-cluster competition borrows this directly,
with clusters as species and signature overlap as `α`. The same
mathematical framework that explains red-vs-grey squirrel
displacement in the UK explains cluster reframing in sylk.

### Hutchinson's niche concept

Hutchinson 1957 (*Concluding Remarks*, Cold Spring Harbor)
defined the ecological niche as a multi-dimensional hypervolume
of conditions and resources that a species can occupy. Two
species occupying overlapping niches compete; with sufficient
overlap, competitive exclusion drives one to extinction
(Gause 1934 — the experimental foundation).

The forest's signature space is exactly Hutchinson's
hypervolume, with each dimension a learned feature of the
embedding. Cluster representative samples mark the niche
each cluster occupies; overlap drives competition.

### Apparent competition (Holt 1977)

Holt 1977 (*Theoretical Population Biology*) showed that two
species can compete *not because they share resources* but
because they share a predator. The forest's apparent
competition arises from shared Guardian-class allelopathic
constraints: clusters under the same predation pressure compete
even without niche overlap.

### Connell on competitive exclusion in nature

Connell 1980 (*The American Naturalist*, "Diversity and the
coevolution of competitors, or the ghost of competition past")
documented the asymmetry of competition coefficients in real
ecosystems: smaller specialists are heavily suppressed by
larger generalists; the reverse is rarely true. The forest
inherits this asymmetry — `α_specialist,generalist` is high
while `α_generalist,specialist` is low — without explicit
declaration.

### Photosynthesis stoichiometry (Redfield)

Redfield 1958 (*American Scientist*, "The biological control of
chemical factors in the environment") established the canonical
C:N:P ratio (106:16:1) of marine plankton, demonstrating that
biological systems operate under fixed stoichiometric
constraints between resource channels. Nitrogen scarcity limits
growth even when carbon is plentiful; phosphorus limits even
when both nitrogen and carbon abound.

The forest's resource channels (carbon = evidence, nitrogen =
correctness, phosphorus = intent, water = recency) inherit this
stoichiometric thinking: a cluster cannot grow on carbon alone;
it needs the full nutrient suite to develop. Disease and
disturbance shift the stoichiometry; recovery depends on
restoring the balance.

### Hybrid vigor — heterosis

Charlesworth & Charlesworth 1987 (*Annual Review of Ecology and
Systematics*) review the genetics of inbreeding depression and
heterosis (hybrid vigor). The result is canonical: hybrids of
sufficiently distinct parents often outperform either parent on
fitness measures, because deleterious recessive alleles from
each lineage are masked. Beyond a distance threshold, however,
**outbreeding depression** sets in — hybrids become unfit
because the parental gene pools are too divergent to integrate.

The forest's hybrid-vigor logic mirrors this exactly: hybrids
of moderately-distant clusters are bonus-scored; hybrids of
extremely-distant clusters are penalty-scored.

### Succession theory — recovery dynamics

Clements 1916 introduced the concept of ecological succession:
predictable progressions from pioneer to climax community after
disturbance. Connell & Slatyer 1977 (*The American Naturalist*)
provided the modern framework — facilitation, tolerance, and
inhibition models for how pioneer species condition the
substrate for later arrivals.

The forest's post-disturbance recovery (fire and drought
recovery in particular) follows the **facilitation model**:
seed-bank Snag nodes condition the substrate; pioneer
regrowth establishes; mature nodes accumulate; climax stage is
reached over time. This is not metaphor — it's the same
mathematical model as natural succession, applied to interaction
nodes.

### Disturbance ecology and resilience

Holling 1973 (*Annual Review of Ecology and Systematics*,
"Resilience and stability of ecological systems") introduced
the modern distinction between **resistance** (capacity to
absorb disturbance without change) and **resilience** (capacity
to return to equilibrium after change). Holling 1996 extended
this to the **adaptive cycle** framework: ecosystems pass
through phases of growth, conservation, release, and
reorganization.

The forest's disturbance regimes (drought, hard winter, fire,
flood) borrow Holling's vocabulary directly. The
resilience/resistance distinction is built into the substrate's
diagnostics; the adaptive cycle phases map cleanly onto
cluster-level developmental staging.

### Critical slowing down (Scheffer)

Scheffer et al. 2009 (*Nature*, "Early-warning signals for
critical transitions") showed that approaching a regime shift
in any complex dynamical system produces measurable signatures:
rising autocorrelation, rising variance, slower recovery from
small perturbations. Dakos et al. 2008 (*PNAS*) gave the
practical statistical machinery.

The forest's `slowdown_signal` is this exact measurement
applied to the spectral signature of each cluster. The
literature's verdict: critical slowing down is a *generic*
signal — it applies to ecosystems, climate systems, financial
markets, neural networks. The forest inherits it for free
because its substrate is a dynamical system.

### Alternative stable states

Beisner et al. 2003 (*Frontiers in Ecology and the Environment*,
"Alternative stable states in ecology") catalogues the empirical
literature on systems with multiple attractors — shallow lakes
that flip between clear-water and turbid states; coral reefs
between coral-dominated and algae-dominated states; savannas
between grass-dominated and shrub-dominated states.

The Lotka-Volterra bistability regime in the forest is the same
phenomenon: when both inter-cluster competition coefficients are
simultaneously high, the system has two basins of attraction;
disturbance can flip the substrate from one to the other.
Recovery to the prior state is not guaranteed — the substrate
can settle into a permanently different configuration.

### Phenology and anticipatory adaptation

Phenology is the science of recurring biological cycles —
flowering times, migration timing, leaf-out dates. Real plants
don't wait for spring to arrive; they read environmental
signals and prepare in advance. Chuine 2010 (*Phil. Trans. R.
Soc. B*) is the canonical review.

The forest's seasonal adaptation borrows this directly: read
the activity time series, predict upcoming high or low
windows, prepare the substrate accordingly. Anticipatory
intervention beats reactive adjustment.

### The integrative point

The design isn't borrowing one biological metaphor. It's
borrowing the *mathematical framework that real immunology, real
epidemiology, and real ecology actually use*. Computational
immunology stopped using SIR for influenza prediction in the 90s;
antigenic cartography is the real machinery. Spectral methods for
outbreak prediction are 25 years old. Persistent homology for
structural anomaly detection is mainstream in materials science.
Reaction-diffusion for pattern formation has been canonical since
Turing.

What's novel in the forest design isn't any *one* of these — it's
the *composition*: a single coherent substrate where the antigenic
field, the spectral analysis, the topological signatures, the
reaction-diffusion economy, and the BCM/synaptic-scaling all run
on the same graph with the same maintenance loop, mutually
reinforcing each other's signals. Pruning becomes the joint
readout — a node's fate is determined by warmth × cleanliness ×
structural-load × topological-coherence × spectral-conformance,
with each axis contributing the same kind of signal real biology
contributes to cell-fate decisions.

## Implementation

This section specifies the implementation deltas relative to
`docs/EMERGENT_FOREST.md` Appendix A. Same project rules apply —
constants derived from physical anchors, cyclomatic complexity ≤
3, all goroutines tracked, no unbounded growth.

### Storage schema additions

```sql
-- Corruption + immunity vector fields per node. Vectors are packed
-- as []float32 in BLOB form; embedding generation is tagged so
-- vector ops operate on consistent representations.
ALTER TABLE forest_nodes ADD COLUMN corruption_blob       BLOB;
ALTER TABLE forest_nodes ADD COLUMN immunity_blob         BLOB;
ALTER TABLE forest_nodes ADD COLUMN corruption_norm       REAL NOT NULL DEFAULT 0;
ALTER TABLE forest_nodes ADD COLUMN immunity_norm         REAL NOT NULL DEFAULT 0;
ALTER TABLE forest_nodes ADD COLUMN cure_failure_count    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE forest_nodes ADD COLUMN last_cure_attempt_at  INTEGER NOT NULL DEFAULT 0;

-- Pathogen registry. Pathogens persist; queries against the
-- pathogen tree use this as the canonical source.
CREATE TABLE forest_pathogens (
    pathogen_id        TEXT    NOT NULL PRIMARY KEY,    -- UUIDv4
    embedding_blob     BLOB    NOT NULL,                -- packed []float32
    embedding_gen      INTEGER NOT NULL,
    declared_by        TEXT    NOT NULL,                -- agent_id or "system:bcm_collapse"
    declaration_event  TEXT    NOT NULL,                -- ledger event_id
    parent_pathogen_id TEXT,                            -- nullable; for mutation tree
    drift_distance     REAL    NOT NULL DEFAULT 0,      -- cosine distance from parent
    declared_at        INTEGER NOT NULL,
    last_updated_at    INTEGER NOT NULL,
    FOREIGN KEY (parent_pathogen_id) REFERENCES forest_pathogens(pathogen_id)
) STRICT;

CREATE INDEX idx_forest_pathogens_parent ON forest_pathogens(parent_pathogen_id);

-- Spectral signature cache per cluster. Recomputed on the
-- substrate-relaxation cadence; consumed by outbreak detection.
CREATE TABLE forest_cluster_spectrum (
    cluster_id          TEXT    NOT NULL PRIMARY KEY,
    fiedler_baseline    REAL    NOT NULL,
    fiedler_current     REAL    NOT NULL,
    top_k_eigenvalues   BLOB    NOT NULL,                -- packed [topK]float32
    top_k_eigenvectors  BLOB    NOT NULL,                -- packed [topK × cluster_size]float32
    last_computed_at    INTEGER NOT NULL,
    FOREIGN KEY (cluster_id) REFERENCES forest_clusters(cluster_id) ON DELETE CASCADE
) STRICT;

-- Topological signature per cluster (Betti numbers across
-- decay-filtered scales).
CREATE TABLE forest_cluster_topology (
    cluster_id        TEXT    NOT NULL PRIMARY KEY,
    betti_blob        BLOB    NOT NULL,                  -- packed Betti numbers across filter levels
    persistence_blob  BLOB    NOT NULL,                  -- packed persistence diagram
    last_computed_at  INTEGER NOT NULL,
    FOREIGN KEY (cluster_id) REFERENCES forest_clusters(cluster_id) ON DELETE CASCADE
) STRICT;
```

### Type definitions

```go
// core/forest/ecology.go

// PathogenVector is a point in pathogen-embedding space.
// Dimension matches node-embedding generation.
type PathogenVector []float32

// Pathogen carries metadata around a pathogen vector and its
// position in the mutation tree.
type Pathogen struct {
    ID                [16]byte      // UUID
    Embedding         PathogenVector
    EmbeddingGen      int32
    DeclaredBy        string
    DeclarationEvent  string
    ParentID          [16]byte      // zero for root pathogens
    DriftDistance     float32       // cosine distance from parent
    DeclaredAt        int64
}

// CorruptionState carries a node's per-step corruption and
// immunity vectors plus derived metrics.
type CorruptionState struct {
    Corruption        PathogenVector
    Immunity          PathogenVector
    CorruptionNorm    float32
    ImmunityNorm      float32
    CureFailureCount  int32
    LastCureAttemptAt int64
}

// Infection computes infection level for a query pathogen P
// against a node's corruption/immunity state. Returns a value in
// [0, 1] where 0 means fully covered, 1 means fully exposed.
func Infection(state CorruptionState, P PathogenVector) float32 {
    corruptDot := dot(P, state.Corruption)
    immunityDot := dot(P, state.Immunity)
    pNorm := norm(P)
    if pNorm == 0 {
        return 0
    }
    excess := corruptDot - immunityDot
    if excess < 0 {
        excess = 0
    }
    return excess / pNorm
}

// QuarantineFactor returns the retrieval-weight multiplier for a
// node given a query-aligned pathogen vector. In [0, 1].
func QuarantineFactor(state CorruptionState, queryPathogen PathogenVector) float32 {
    inf := Infection(state, queryPathogen)
    return clampUnit(1 - inf)
}
```

### Diffusion update on the corruption field

```go
// AdvanceCorruptionField performs one step of the corruption
// reaction-diffusion equation over a subgraph. Uses the same
// graph Laplacian as RelaxField (A.5.5); kernel differs by
// substituting corruption gradient for activator/inhibitor
// reaction terms.
//
// Bounded by ctx, idempotent on (subgraph_hash, step_index).
func (s *Service) AdvanceCorruptionField(
    ctx context.Context,
    sub *Subgraph,
    scales SubstrateScales,
) error {
    return s.forEachNode(ctx, sub, func(n *Node) error {
        return s.applyCorruptionStep(n, sub, scales)
    })
}

func (s *Service) applyCorruptionStep(
    n *Node,
    sub *Subgraph,
    scales SubstrateScales,
) error {
    laplacian := authorityLaplacianAt(sub, n)
    decay := scales.CorruptionDecayRate
    immune := scales.ImmunityClearanceRate
    proj := projectOnto(n.Corruption, n.Immunity)
    delta := vecScale(laplacian, scales.CorruptionDiffusionRate)
    delta = vecAdd(delta, vecScale(n.Corruption, -decay))
    delta = vecAdd(delta, vecScale(proj, -immune))
    n.Corruption = vecAdd(n.Corruption, vecScale(delta, scales.Dt))
    n.CorruptionNorm = norm(n.Corruption)
    return nil
}
```

### Spectral outbreak detection

```go
// FiedlerValue computes λ₂ of the corruption-weighted Laplacian
// over a cluster's induced subgraph via Lanczos iteration. O(|E|)
// per call.
//
// Returns NaN if the cluster has fewer than minSpectralClusterSize
// nodes (insufficient signal for spectral analysis).
func (s *Service) FiedlerValue(
    ctx context.Context,
    cluster *Cluster,
) (float32, error) {
    sub, err := s.inducedSubgraph(ctx, cluster.ClusterID)
    if err != nil {
        return 0, err
    }
    if len(sub.Nodes) < minSpectralClusterSize {
        return float32(math.NaN()), nil
    }
    L := corruptionWeightedLaplacian(sub)
    return lanczosLambda2(ctx, L, lanczosMaxIterations)
}

// minSpectralClusterSize: the smallest cluster for which spectral
// analysis is meaningful. Anchored on canonicalKindCount — a
// cluster needs at least one node per canonical kind for the
// spectrum to reflect kind-diverse interaction structure.
const minSpectralClusterSize = canonicalKindCount

// lanczosMaxIterations: bounded by Lanczos's well-known
// convergence guarantee — for a sparse symmetric matrix, the
// extreme eigenvalues converge in O(sqrt(condition_number))
// iterations. canonicalKindCount × 2 is a generous bound for the
// forest's regime.
const lanczosMaxIterations = canonicalKindCount * 2
```

### Pathogen mutation tracking

```go
// MutatePathogen creates a child pathogen by drifting the parent
// toward the receiver node's signature. Drift factor is anchored
// on the kind's susceptibility weight (refines = high drift,
// responds_to = lower drift).
func (s *Service) MutatePathogen(
    ctx context.Context,
    parent *Pathogen,
    receiver *Node,
    edgeKind EdgeKind,
) (*Pathogen, error) {
    drift := kindMutationRate[edgeKind]
    if drift <= 0 {
        return parent, nil // edge kind doesn't transmit / drift
    }
    childEmbedding := vecLerp(parent.Embedding, receiver.Signature, drift)
    return s.recordChildPathogen(ctx, parent, childEmbedding, drift)
}

// kindMutationRate: drift factor per authority edge kind.
// Anchored on kind semantics:
//   - refines: paraphrases the antecedent, high drift
//   - responds_to: shaped by the antecedent, medium drift
//   - cites: reproduces the antecedent verbatim, very low drift
//   - defers_to: explicit handoff, no semantic transformation
var kindMutationRate = map[EdgeKind]float32{
    EdgeRefines:    refinesDriftAnchor,
    EdgeRespondsTo: respondsToDriftAnchor,
    EdgeCites:      citesDriftAnchor,
    EdgeDefersTo:   0,
}
```

The drift anchors derive from observed edit distances on
representative pairs at service startup — they are not literals.

### Persistent homology over the cluster

Cluster-level topology uses sparse persistent homology over a
distance-weighted Vietoris-Rips complex. We track only Betti₀
(connected components) and Betti₁ (loops); Betti₂+ is
prohibitively expensive and contributes little signal in
practice.

```go
// ClusterBettiSignature returns (β₀, β₁) for the cluster's
// induced complex filtered by edge weight. Used by the topology
// PoI view to detect structural pathology.
func (s *Service) ClusterBettiSignature(
    ctx context.Context,
    cluster *Cluster,
    filterLevels []float32,
) ([]int32, []int32, error) {
    complex, err := s.buildVRComplex(ctx, cluster)
    if err != nil {
        return nil, nil, err
    }
    return persistentBetti(ctx, complex, filterLevels)
}

// numFilterLevels: how many filtration thresholds to sample.
// Anchored on canonicalKindCount — each canonical kind gets one
// resolution level, providing kind-grained topological coverage.
const numFilterLevels = canonicalKindCount
```

### Maintenance loop integration

The maintenance loop gains four new steps, slotted into the
existing ordering from `EMERGENT_FOREST.md` A.8:

```
existing:                                    becomes:
    decay_sweep                                  decay_sweep
    bcm_threshold                                bcm_threshold
    synaptic_scaling                             synaptic_scaling
                                                 corruption_diffusion        // new
    substrate_relaxation                         substrate_relaxation
                                                 spectral_outbreak_check     // new
    cluster_compaction                           cluster_compaction
    cluster_cohesion                             cluster_cohesion
    cluster_merge                                cluster_merge
                                                 topological_signature       // new
    poi_recompute                                poi_recompute
    replay_schedule                              replay_schedule
                                                 cure_failure_promotion      // new
    ecology_pruning                              ecology_pruning
```

Each new step is budgeted from the same phase-weight pool as the
existing steps; weights reallocate uniformly. The corruption
diffusion shares the substrate-relaxation budget envelope (it's
the same primitive, different field).

### Resource sizing additions

| Constant | Anchor |
|---|---|
| `minSpectralClusterSize` | `canonicalKindCount` |
| `lanczosMaxIterations` | `canonicalKindCount × 2` |
| `numFilterLevels` | `canonicalKindCount` |
| `kindMutationRate` | observed edit-distance percentile per edge kind at service start |
| corruption diffusion α | observed substrate cycle period × `pathogenAdvectionFraction` |
| corruption decay γ | observed validation cadence × `pathogenClearanceMultiplier` |
| immunity clearance β | observed cited-validation rate × `immunityGainMultiplier` |
| `pathologyThreshold` per cluster | cluster's historical `q90` Fiedler-drop |
| `pruneThreshold` (corruption axis) | cluster's `q90` of `‖corruption - proj_immunity(corruption)‖` |

All `*Multiplier` and `*Fraction` constants are derived from log-
of-event-rate ratios at service startup; none are literals.

### Test discipline additions

Every algorithm in this appendix ships with tests that go beyond
the existing forest test discipline:

- **Spectral convergence tests.** Lanczos with synthetic graphs of
  known eigenvalues; assert convergence to known λ₂ within
  tolerance.
- **Mutation tree fidelity.** Generate synthetic mutation events;
  assert the recorded `parent_pathogen_id` chain reconstructs the
  generative tree.
- **Quarantine-factor monotonicity.** As corruption grows,
  quarantine factor monotonically decreases; as immunity grows
  toward the corruption, quarantine factor monotonically
  increases back.
- **Cross-immunity tests.** A node immune to P₁ has graded
  protection against P₂ proportional to `cos(P₁, P₂)`. Assert
  partial coverage at known cosine angles.
- **Pruning two-axis correctness.** Synthetic nodes at the
  cardinal corners of the (warmth × cleanliness) plane —
  warm-clean, warm-poisoned, cold-immune, cold-poisoned — must
  be ranked in the expected order.
- **Spectral pruning surgery.** Construct a cluster with a known
  pathological subset (signature embeds the pathology); assert
  spectral pruning identifies exactly that subset, not its
  neighbors.

### Inter-cluster competition (Lotka-Volterra)

```sql
-- Per-pair competition state (already shown above; repeated here
-- in the Implementation context for completeness).
CREATE TABLE forest_cluster_competition (
    cluster_a            TEXT    NOT NULL,
    cluster_b            TEXT    NOT NULL,
    alpha_ab             REAL    NOT NULL,
    alpha_ba             REAL    NOT NULL,
    coactivation_rate    REAL    NOT NULL,
    last_computed_at     INTEGER NOT NULL,
    PRIMARY KEY (cluster_a, cluster_b)
) STRICT;

CREATE INDEX idx_cluster_competition_b ON forest_cluster_competition(cluster_b);
```

```go
// CompetitionStep applies one Lotka-Volterra integration step
// over all clusters. Reads forest_cluster_competition; updates
// forest_clusters.size accordingly.
//
// Bounded by ctx; idempotent on (last_computed_at, cycle_index).
func (s *Service) CompetitionStep(ctx context.Context) error {
    pairs, err := s.fetchCompetitionPairs(ctx)
    if err != nil {
        return err
    }
    return s.forEachCluster(ctx, func(c *Cluster) error {
        return s.applyLotkaVolterraStep(c, pairs)
    })
}

// applyLotkaVolterraStep computes dN_i/dt for one cluster and
// commits the integration result. Cyclomatic-complexity-bounded
// by extracting the term computation.
func (s *Service) applyLotkaVolterraStep(c *Cluster, pairs []CompetitionPair) error {
    r, K := c.GrowthRate, c.CarryingCapacity
    competitiveBurden := competitiveBurdenSum(c.ClusterID, pairs)
    instantaneous := r * float64(c.Size) *
        (1 - (float64(c.Size)+competitiveBurden)/K)
    return s.commitSizeDelta(c, instantaneous*s.cadence.CompetitionStepSeconds())
}

// competitiveBurdenSum is Σ_{j≠i} α_ij · N_j. Split out for
// complexity bound.
func competitiveBurdenSum(target [16]byte, pairs []CompetitionPair) float64 {
    var burden float64
    for _, p := range pairs {
        if p.A == target {
            burden += float64(p.AlphaAB) * p.SizeB
        } else if p.B == target {
            burden += float64(p.AlphaBA) * p.SizeA
        }
    }
    return burden
}

// RecomputeCompetitionMatrix runs at maintenance cadence.
// Uses a centroid-NN structure to bound the O(C²) baseline:
// only pairs whose centroids are within a derived radius are
// computed exactly; the rest are zeroed out by triangle
// inequality.
func (s *Service) RecomputeCompetitionMatrix(ctx context.Context) error {
    centroids, err := s.fetchClusterCentroids(ctx)
    if err != nil {
        return err
    }
    candidates := centroidNearestPairs(centroids, competitionDegreeAnchor)
    return s.forEachPair(ctx, candidates, s.upsertCompetitionPair)
}

// competitionDegreeAnchor: the maximum number of competitor
// neighbors per cluster the matrix tracks. Anchored on
// canonicalKindCount — a cluster's "competitive neighborhood"
// is at most one peer per kind. Beyond that, additional
// competitors are below threshold and don't materially affect
// dynamics.
const competitionDegreeAnchor = canonicalKindCount
```

### Hybrid vigor

```sql
ALTER TABLE forest_nodes ADD COLUMN hybrid_score        REAL NOT NULL DEFAULT 0;
ALTER TABLE forest_nodes ADD COLUMN parent_clusters_blob BLOB;          -- packed []cluster_id
ALTER TABLE forest_nodes ADD COLUMN parental_distance   REAL NOT NULL DEFAULT 0;
```

```go
// HybridScore is the entropy of a node's cluster memberships,
// normalized to [0, 1]. Hybrid score 0 = single-cluster (one
// rank-0 with weight ≈ 1); 1 = uniform over membershipK clusters.
func HybridScore(memberships []membershipAssignment) float32 {
    if len(memberships) <= 1 {
        return 0
    }
    var H float64
    for _, m := range memberships {
        if m.Weight <= 0 {
            continue
        }
        w := float64(m.Weight)
        H -= w * math.Log(w)
    }
    maxH := math.Log(float64(len(memberships)))
    if maxH <= 0 {
        return 0
    }
    return float32(H / maxH)
}

// CombineParentSubstrate computes the substrate inheritance for
// a hybrid node from its parents. Signature and corruption are
// weighted vector sums; immunity is the per-dimension max
// (vector OR), so the hybrid covers any pathogen any parent was
// immune to.
func CombineParentSubstrate(parents []ParentReference) HybridInheritance {
    weights := normalize(extractWeights(parents))
    sig := weightedVecSum(parents, weights, parentSignature)
    cor := weightedVecSum(parents, weights, parentCorruption)
    imm := vecMax(parents, parentImmunity) // OR semantics
    return HybridInheritance{
        Signature:  sig,
        Corruption: cor,
        Immunity:   imm,
    }
}

// HybridVigorBonus is the retrieval-score multiplier applied to
// hybrid nodes. Returns a value in [outbreedingPenalty, 1+vigorBonus].
//
// Below an outbreeding threshold, returns the bonus.
// Beyond the threshold, returns the penalty.
func HybridVigorBonus(score float32, parentalDistance float32, threshold float32) float32 {
    if parentalDistance > threshold {
        return outbreedingPenalty
    }
    return 1 + vigorBonusScale*score
}

// outbreedingPenalty: scaled below 1; anchored on
// 1/canonicalKindCount — outbred hybrids are treated as if
// they're contributing 1/canonicalKindCount of a normal node's
// signal.
const outbreedingPenalty = 1.0 / float32(canonicalKindCount)

// vigorBonusScale: maximum bonus an in-range hybrid can receive.
// Anchored on 1/canonicalKindCount — a perfectly-positioned
// hybrid contributes one extra "kind's worth" of signal.
const vigorBonusScale = 1.0 / float32(canonicalKindCount)
```

### Climate, microclimate, photosynthesis

```sql
-- Global climate state, single row, periodically updated.
CREATE TABLE forest_climate_global (
    id                   INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    sun_intensity        REAL    NOT NULL,
    activity_short       REAL    NOT NULL,
    activity_long        REAL    NOT NULL,
    seasonal_phase       REAL    NOT NULL,         -- radians [0, 2π)
    seasonal_amplitude   REAL    NOT NULL,
    last_updated_at      INTEGER NOT NULL
) STRICT;

-- Per-cluster microclimate state.
CREATE TABLE forest_climate_microclimate (
    cluster_id           TEXT    NOT NULL PRIMARY KEY,
    local_sun_intensity  REAL    NOT NULL,
    activity_short       REAL    NOT NULL,
    activity_long        REAL    NOT NULL,
    last_updated_at      INTEGER NOT NULL,
    FOREIGN KEY (cluster_id) REFERENCES forest_clusters(cluster_id) ON DELETE CASCADE
) STRICT;

-- Photosynthesis log: per-cycle resource production attribution
-- to (agent, channel). Sized small; rotated on the long-window
-- maintenance cadence.
CREATE TABLE forest_photosynthesis_log (
    cycle_id             INTEGER NOT NULL,
    agent_id             TEXT    NOT NULL,
    channel              TEXT    NOT NULL,         -- carbon|nitrogen|phosphorus|water
    produced             REAL    NOT NULL,
    cycle_at             INTEGER NOT NULL,
    PRIMARY KEY (cycle_id, agent_id, channel)
) STRICT;
```

```go
// PhotosynthesisRate returns the per-event resource production
// for an event of the given kind, scaled by the producing
// agent's photosynthetic profile.
func PhotosynthesisRate(eventKind EventKind, agentType string) ChannelState {
    base := basePhotosynthesisRate[eventKind]
    profile := agentPhotosyntheticProfile[agentType]
    return ChannelState{
        Carbon:     base.Carbon * profile.CarbonGain,
        Nitrogen:   base.Nitrogen * profile.NitrogenGain,
        Phosphorus: base.Phosphorus * profile.PhosphorusGain,
        Water:      base.Water * profile.WaterGain,
    }
}

// agentPhotosyntheticProfile codifies the per-agent gains shown
// in the ECOLOGY.md profile table. Kind-grounded: each gain
// reflects what the agent's actions semantically produce.
var agentPhotosyntheticProfile = map[string]Profile{
    "academic":   {CarbonGain: 1.0, WaterGain: 0.5},
    "librarian":  {CarbonGain: 0.3, WaterGain: 1.0},
    "guardian":   {NitrogenGain: 1.0},
    "engineer":   {PhosphorusGain: -1.0}, // consumer
    "designer":   {PhosphorusGain: -0.8},
    "tester":    {CarbonGain: 0.5, NitrogenGain: 0.5},
    "architect":  {NitrogenGain: 0.7, PhosphorusGain: -0.5},
    "inspector":  {NitrogenGain: 0.5, WaterGain: 0.5},
}

// ClimateGain returns the global climate multiplier applied to
// substrate dynamics (decay, growth, speciation threshold).
// Anchored on the ratio of short-window to long-window activity.
func ClimateGain(global *GlobalClimateState, t time.Time) float32 {
    seasonal := 1 + global.SeasonalAmplitude*float32(math.Cos(float64(global.SeasonalPhase)))
    return global.SunIntensity * seasonal
}

// MicroclimateGain layers on top of global climate per cluster.
func MicroclimateGain(local *MicroclimateState, global *GlobalClimateState) float32 {
    return ClimateGain(global, time.Now()) * local.LocalSunIntensity
}
```

### Disturbance regimes

```sql
-- Disturbance event log: append-only.
CREATE TABLE forest_disturbance_events (
    event_id             TEXT    NOT NULL PRIMARY KEY,    -- UUID
    type                 TEXT    NOT NULL,                -- drought|winter|fire|flood
    region_blob          BLOB    NOT NULL,                -- packed [cluster_id] affected
    severity             REAL    NOT NULL,                -- [0, 1]
    declared_at          INTEGER NOT NULL,
    declared_by          TEXT    NOT NULL,                -- agent_id or "system:detector"
    recovery_started_at  INTEGER,
    recovery_completed_at INTEGER
) STRICT;

CREATE INDEX idx_disturbance_type_at ON forest_disturbance_events(type, declared_at);

-- Seed bank: nodes preserved across fires.
CREATE TABLE forest_seed_bank (
    node_id              TEXT    NOT NULL,
    fire_event_id        TEXT    NOT NULL,
    immunity_diversity   REAL    NOT NULL,
    signature_magnitude  REAL    NOT NULL,
    preserved_at         INTEGER NOT NULL,
    PRIMARY KEY (node_id, fire_event_id),
    FOREIGN KEY (node_id)        REFERENCES forest_nodes(node_id)             ON DELETE CASCADE,
    FOREIGN KEY (fire_event_id)  REFERENCES forest_disturbance_events(event_id) ON DELETE CASCADE
) STRICT;
```

```go
// DisturbanceDetector runs every maintenance cycle and emits
// disturbance events when triggers fire.
type DisturbanceDetector struct {
    base *Service
}

// Detect runs all four detectors. Cyclomatic complexity bounded
// by dispatching to per-type checks.
func (d *DisturbanceDetector) Detect(ctx context.Context) ([]DisturbanceEvent, error) {
    events := []DisturbanceEvent{}
    detectors := []func(context.Context) (*DisturbanceEvent, error){
        d.detectDrought,
        d.detectHardWinter,
        d.detectFlood,
        // fires are operator-initiated; no auto-detector
    }
    for _, detect := range detectors {
        e, err := detect(ctx)
        if err != nil {
            return nil, err
        }
        if e != nil {
            events = append(events, *e)
        }
    }
    return events, nil
}

// detectDrought fires when global activity_short / activity_long
// falls below droughtThreshold for ≥ droughtMinDuration.
func (d *DisturbanceDetector) detectDrought(ctx context.Context) (*DisturbanceEvent, error) {
    climate := d.base.LoadGlobalClimate(ctx)
    if climate.ActivityShort/climate.ActivityLong > droughtThreshold {
        return nil, nil
    }
    if d.base.timeSinceLastNonDrought() < droughtMinDuration {
        return nil, nil
    }
    return d.base.NewDisturbanceEvent("drought", climate.AffectedRegion(), 1-climate.SunIntensity), nil
}

// droughtThreshold and droughtMinDuration are derived:
//   droughtThreshold = q10 of historical activity_short/activity_long ratio
//   droughtMinDuration = canonicalKindCount × MinMeaningfulGap
// — a drought is "low for at least one canonical-kind worth of cycles".
var (
    droughtThreshold     float32        // initialized at service start
    droughtMinDuration   time.Duration  // initialized at service start
)
```

```go
// CriticalSlowdownSignal computes the early-warning indicator for
// approaching tipping points per cluster, per Scheffer 2009.
//
// Returns a value > 1 when the cluster is at risk of regime
// shift. Used by the maintenance loop to prioritize replay and
// alert the architect.
func (s *Service) CriticalSlowdownSignal(
    ctx context.Context,
    cluster *Cluster,
) (float32, error) {
    samples, err := s.fetchSpectralHistory(ctx, cluster, slowdownWindow)
    if err != nil {
        return 0, err
    }
    if len(samples) < slowdownMinSamples {
        return 0, nil
    }
    autocorr := lag1Autocorrelation(samples)
    variance := sampleVariance(samples)
    baseline := s.slowdownBaseline(cluster)
    return float32(autocorr*variance) / baseline, nil
}

// slowdownWindow: how many maintenance cycles to look back.
// Anchored on canonicalKindCount × 4 — enough samples to
// detect autocorrelation reliably without weighting old
// regime states.
const slowdownWindow = canonicalKindCount * 4

// slowdownMinSamples: the minimum sample count for the
// autocorrelation estimate to be statistically meaningful.
// Anchored on canonicalKindCount.
const slowdownMinSamples = canonicalKindCount
```

### Maintenance loop integration

The four new mechanisms slot into the maintenance loop ordering
established in `EMERGENT_FOREST.md` A.8, in the natural
positions:

```
existing + ECOLOGY.md additions:                 becomes:
    decay_sweep                                      decay_sweep
    bcm_threshold                                    bcm_threshold
    synaptic_scaling                                 synaptic_scaling
    corruption_diffusion                             corruption_diffusion
    substrate_relaxation                             substrate_relaxation
    spectral_outbreak_check                          spectral_outbreak_check
                                                     climate_update                 // new
    cluster_compaction                               cluster_compaction
    cluster_cohesion                                 cluster_cohesion
    cluster_merge                                    cluster_merge
                                                     competition_step               // new
                                                     hybrid_score_recompute         // new
    topological_signature                            topological_signature
    poi_recompute                                    poi_recompute
                                                     critical_slowdown_check        // new
                                                     disturbance_detection          // new
    replay_schedule                                  replay_schedule
    cure_failure_promotion                           cure_failure_promotion
    ecology_pruning                                  ecology_pruning
                                                     disturbance_response           // new
```

Each new step takes its budget from the same phase-weight pool,
reallocated uniformly. The competition_step is `O(C × |pairs|)`
which is bounded by `competitionDegreeAnchor × C`, well within
the maintenance cycle. The disturbance detection runs every
cycle but only triggers responses when a detector fires.

### Resource sizing additions

| Constant | Anchor |
|---|---|
| `competitionDegreeAnchor` | `canonicalKindCount` |
| `outbreedingPenalty` | `1 / canonicalKindCount` |
| `vigorBonusScale` | `1 / canonicalKindCount` |
| `droughtThreshold` | `q10` of historical activity ratio |
| `droughtMinDuration` | `canonicalKindCount × MinMeaningfulGap` |
| `hardWinterDropFraction` | `0.5` × historical median resource budget |
| `floodBurstWindow` | `canonicalKindCount × MinMeaningfulGap` |
| `slowdownWindow` | `canonicalKindCount × 4` |
| `slowdownMinSamples` | `canonicalKindCount` |
| seed-bank size per cluster | `canonicalKindCount` |
| seasonal Fourier components | `canonicalKindCount` (fundamental + harmonics up to kind count) |

All `q10` / `q50` / `q90` quantities are recomputed at the
slow maintenance tick from the active forest's history; on a
fresh forest they fall back to the literal-floor anchors.

### Test discipline additions

- **Lotka-Volterra equilibrium tests.** Synthetic two-cluster
  systems with known `α` matrix; assert long-run population
  ratios match the analytic equilibrium.
- **Competitive exclusion tests.** When `α_ij × α_ji > 1` and
  initial conditions favor `i`, `j` decays to zero in a derived
  number of cycles.
- **Hybrid vigor bonus monotonicity.** Bonus increases with
  `hybrid_score` up to the outbreeding threshold; falls
  discontinuously at the threshold.
- **Climate gain bounded.** ClimateGain returns a value within
  `[seasonal_amplitude_min, seasonal_amplitude_max]` × historical
  bounds for any input.
- **Photosynthesis stoichiometry.** Aggregate channel production
  per cycle respects the agent profiles' relative weights within
  tolerance.
- **Disturbance recovery curves.** Inject a synthetic fire;
  assert pioneer regrowth, seed-bank reactivation, and
  full-recovery time match expected succession dynamics within
  derived bounds.
- **Critical slowdown precedes regime shift.** Inject a slow
  drift toward bistability; assert `slowdownSignal` exceeds
  threshold *before* the regime shift, with lead time of at
  least `slowdownWindow / 2` cycles.

### The Substrate Agent — emission and subscription

```go
// core/forest/substrate_agent.go

// SubstrateAgent is the forest's agent identity. Posts testaments
// at maintenance-loop cadence; receives validations like any
// other agent.
type SubstrateAgent struct {
    agentID  string                    // "system:substrate"
    board    *claims.ClaimsBoard
    fabric   activity.Publisher
    profiles map[string]AgentForestProfile
    scope    *concurrency.GoroutineScope
}

// EmitTestament posts a testament with one or more forest
// artifacts. Bounded by ctx; idempotent on
// (testament_seq, last_applied_seq).
func (s *SubstrateAgent) EmitTestament(
    ctx context.Context,
    artifacts []claims.Artifact,
) error {
    if len(artifacts) == 0 {
        return nil
    }
    if err := ctx.Err(); err != nil {
        return err
    }
    testament := claims.Testament{
        AgentID:    s.agentID,
        Summary:    summarizeArtifacts(artifacts),
        Artifacts:  byPointers(artifacts),
        Confidence: "high",
    }
    return s.board.SubmitTestaments(
        ctx,
        claims.Action{Type: claims.ActionTypeTestament, AgentID: s.agentID},
        []claims.Testament{testament},
    )
}

// EmitClaim posts a substrate-issued claim addressed to a
// specific agent. The substrate uses this for maintenance work,
// speciation review, contradiction remediation, etc.
func (s *SubstrateAgent) EmitClaim(
    ctx context.Context,
    subject string,
    action claims.ActionType,
    claim claims.Claim,
) error {
    if !s.profiles[subject].acceptsInbound(claim.Action) {
        return fmt.Errorf("substrate: agent %s does not accept claim action %s",
            subject, claim.Action)
    }
    claim.Relations = append(claim.Relations, claims.Relation{
        Related:      subject,
        RelatedType:  claims.RelatedTypeAgent,
        Relationship: claims.RelationshipSubject,
    })
    return s.board.PostAction(ctx,
        claims.Action{Type: action, AgentID: s.agentID},
        []claims.Claim{claim},
    )
}
```

### Subscription dispatch

```go
// core/forest/subscription_dispatch.go

// DispatchTestament delivers a testament to every agent whose
// profile subscribes to any of its artifact kinds. Bounded;
// runs through the existing fabric pipeline (no new transport).
//
// Cyclomatic complexity ≤ 3 by extracting the per-agent send.
func (d *SubscriptionDispatcher) DispatchTestament(
    ctx context.Context,
    t *claims.Testament,
) error {
    kinds := artifactKinds(t)
    if len(kinds) == 0 {
        return nil
    }
    targets := d.agentsSubscribingTo(kinds)
    return d.deliverToTargets(ctx, t, targets)
}

// agentsSubscribingTo returns the set of agents whose profile
// includes any of the given artifact kinds in SubscribedKinds.
// O(agent_count × kind_count); bounded.
func (d *SubscriptionDispatcher) agentsSubscribingTo(kinds []string) []string {
    seen := make(map[string]struct{}, len(d.profiles))
    out := make([]string, 0, len(d.profiles))
    for agentType, profile := range d.profiles {
        if !subscriberMatchesAny(profile.SubscribedKinds, kinds) {
            continue
        }
        if _, dup := seen[agentType]; dup {
            continue
        }
        seen[agentType] = struct{}{}
        out = append(out, agentType)
    }
    return out
}
```

### Authority enforcement

```go
// core/forest/claim_authority.go

// CanPost returns whether the given agent type is authorized to
// post the given substrate-directed claim action.
//
// Read at the substrate's PostAction inbound path. Unauthorized
// claims are rejected with the constraint cited as rejection
// reason.
func CanPost(agentType, claimAction string) bool {
    profile, ok := agentProfiles[agentType]
    if !ok {
        return false
    }
    return slices.Contains(profile.OutboundClaimKinds, claimAction)
}

// ValidateInboundSubstrateClaim runs at the substrate's PostAction
// path. Returns nil if the claim is authorized; otherwise an
// error explaining the rejection.
func ValidateInboundSubstrateClaim(
    issuer claims.Agent,
    claim *claims.Claim,
) error {
    if claim.Action == "" {
        return fmt.Errorf("substrate: claim missing action")
    }
    if !CanPost(issuer.Type, claim.Action) {
        return fmt.Errorf(
            "substrate: agent type %s not authorized to post claim action %q",
            issuer.Type, claim.Action,
        )
    }
    return nil
}
```

### Per-agent skill registration

Each agent's construction registers its forest-specific skills
into its skill manager. Skills are thin wrappers — they marshal
arguments, delegate to the substrate, and return structured
results.

```go
// core/forest/agent_skills.go

// RegisterAgentForestSkills attaches the per-agent forest skill
// surface to the agent's skill manager. Called from agent
// construction. The skills registered are the union of the
// agent's profile's SkillAdditions plus the universal skills
// (forest_resolve_intent, forest_recall, etc.) every agent has.
func RegisterAgentForestSkills(
    agentType string,
    sm *skills.Manager,
    sa *SubstrateAgent,
) error {
    profile, ok := agentProfiles[agentType]
    if !ok {
        return fmt.Errorf("forest: unknown agent type %s", agentType)
    }
    for _, skillName := range profile.SkillAdditions {
        impl, err := buildSkill(skillName, sa)
        if err != nil {
            return fmt.Errorf("forest: build skill %s: %w", skillName, err)
        }
        if err := sm.Register(impl); err != nil {
            return fmt.Errorf("forest: register skill %s: %w", skillName, err)
        }
    }
    return nil
}
```

### Resource sizing additions

| Constant | Anchor |
|---|---|
| Trace baggage size budget | `4 × canonicalKindCount` bytes (binary-packed cursor + markers + stage + disturbance + infection) |
| Subscription dispatch fanout cap | `agent_count × subscribed_kinds_per_agent` (bounded by table) |
| Substrate testament batch size | `canonicalKindCount` artifacts per testament |
| Substrate testament emission cadence | maintenance loop cadence (already derived) |
| Claim authority table size | bounded by enumerated authorized actions per agent |

All entries are derived from existing structural constants
(`canonicalKindCount`, agent count, table-bounded enumerations);
none are literals.

### Test discipline additions

- **Subscription correctness.** For each artifact kind, assert
  the set of agents the dispatcher routes to matches the
  declared `SubscribedKinds` in the profile registry.
- **Authority enforcement.** For each (agent_type, claim_action)
  pair, assert that authorized actions succeed and unauthorized
  actions are rejected with the constraint cited.
- **Profile completeness.** Reflect-based test that every entry
  in `agentProfiles` has `OutboundClaimKinds`,
  `SubscribedKinds`, `PhotosynthesisProfile`, `DiseaseRole`,
  `DisturbanceRole`, and `SkillAdditions` populated. Empty is
  permitted; missing is not.
- **Trace baggage size bound.** Synthetic forest cursor with
  worst-case top-K cluster IDs + max PoI markers + stage +
  disturbance + infection serializes to ≤ baggage budget.
- **Substrate-issued claim flow.** End-to-end test: substrate
  emits a brittle-node refresh claim → archivalist receives →
  archivalist posts testament → substrate's projection updates
  the brittle node's last-validated timestamp.
- **Substrate-as-subject claim flow.** End-to-end test: architect
  posts pathogen-declaration claim → substrate validates
  authority → pathogen registered → outbreak detector reflects
  the new pathogen on next cycle.
- **Cross-phase regression.** For each phase boundary in the
  rollout, snapshot the system state and assert correctness with
  the next phase's features both enabled and disabled. The
  system is correct at each phase boundary.

## Open Design Questions

These are the points where the antigenic field design is still
undetermined.

### Pathogen embedding stability

If the embedding generation rotates (per `EMERGENT_FOREST.md`),
pathogen vectors from prior generations are in a different metric
space. Three viable stances:

- **Generation-immutable pathogens**: keep old pathogen vectors;
  cross-generation immunity becomes uncertain.
- **Pathogen migration**: re-embed all active pathogens under the
  new generation; preserve mutation tree topology.
- **Generation-aware similarity**: define cross-generation cosine
  via a learned alignment head between the generations' spaces.

Recommendation: pathogen migration with explicit ledger event,
preserving lineage. Same discipline as node migration.

### Bridge-spanning outbreaks

Spectral analysis is per-cluster. A pathology spreading via
bridges spans cluster boundaries. Computing the spectrum of the
union (cluster + bridge neighborhood) detects this — but at the
cost of more expensive Lanczos runs. The tradeoff: detect
cross-cluster outbreaks earlier vs spend more on the maintenance
loop.

Recommendation: include bridge neighbors in the spectral check
for clusters whose Fiedler value has dropped below baseline; skip
otherwise. Adaptive cost.

### Topology depth

Persistent homology beyond Betti₁ is expensive but informative
for very high-dimensional cluster geometry. Whether to compute
Betti₂ at all is a cost/value decision that should be revisited
once the substrate is operating.

Recommendation: ship Betti₀ and Betti₁ in Phase 1; instrument
cluster-size distributions to determine whether Betti₂ is
warranted in any cluster.

### Drift rate calibration

The kind-specific drift anchors are derived from observed edit
distances at service start, but the calibration cohort matters.
Drift on technical-content edges differs from drift on
discussion edges. Per-cluster drift calibration is more accurate
but more expensive to maintain.

Recommendation: global drift anchors for the first phase; revisit
once outbreak data shows per-cluster variance.

### Competition coefficient asymmetry

The Lotka-Volterra `α_ij` derived from cosine similarity is
inherently symmetric — `α_ij = α_ji`. Real ecological data
shows asymmetry (Connell 1980); generalists suppress specialists
much more than specialists suppress generalists.

To get the asymmetry, `α_ij` should be weighted by the receiver's
*niche width* (how broad cluster i's signature distribution is).
A narrow specialist sees a wide generalist as overwhelming
competition; a wide generalist sees a narrow specialist as
negligible.

Recommendation: implement asymmetric `α` from the start, with
the niche-width weighting derived from cluster-sample variance.

### Hybrid vigor and corruption inheritance

The current design has hybrids inherit the *vector OR* of their
parents' immunity (cover any pathogen any parent covered). The
corruption inheritance is the *weighted sum* (average exposure).
This is asymmetric in the agent's favor — hybrids are protected
generously, exposed conservatively.

Whether this is the right asymmetry depends on whether
adversarial pathogen-mutation is a real concern. If an attacker
can craft a hybrid path that combines two clean parents into a
poisoned child, the OR-immunity assumption is too generous.

Recommendation: ship the optimistic hybrid-vigor design; monitor
corruption-vs-immunity histograms across hybrids; revisit if
adversarial drift becomes observable.

### Photosynthesis stoichiometry

The agent profile table is kind-grounded but the absolute
production rates (carbon gain × event count = total carbon) are
free parameters. They should follow Redfield-style stoichiometry
— canonical relative ratios between channels, with absolute
levels driven by user activity rate.

Recommendation: bake in Redfield-equivalent ratios as
relative anchors; keep absolute levels driven by activity.
Revisit if per-channel deficiencies emerge as growth-limiting in
practice.

### Climate vs season vs disturbance disambiguation

The detector boundaries between "low climate", "off-season", and
"drought" are not crisp. A weekend is climate; a vacation is
seasonality; a sustained stoppage is drought. The substrate
needs to disambiguate to apply the right response.

Recommendation: use **multi-scale activity statistics** — the
short-window vs long-window ratio is climate; the seasonal
Fourier components are seasonality; the deviation from
seasonality-adjusted long-window expectation is drought.

### Tipping-point intervention vs autonomy

Critical slowing down lets the substrate predict regime shifts
before they happen. *Whether* to intervene (preempting a shift
that the user actually wants) is a policy question, not a
technical one. The substrate should *signal* the at-risk
condition; intervention should be agent-mediated, with the
architect deciding whether to author preventive remediation
or let the shift complete.

Recommendation: substrate emits at-risk signals; never
auto-intervenes; the architect's claim queue is the gate.

### Substrate emission cadence vs latency

The substrate emits testaments at maintenance-loop cadence —
this is the natural rate, but means agents see forest signals
with up to one full cadence of latency. For some signals
(critical-period reopening, outbreak detection) the latency is
acceptable; for others (cluster cursor in baggage) it must be
real-time.

The trace-baggage tier handles the real-time slice; testament
tier handles batched. The boundary between the two is not yet
sharply specified — which signals belong in which tier should
be revisited once production data shows agent latency
sensitivity.

### Profile evolution

The per-agent profiles are static configuration in the design —
each agent has fixed `SubscribedKinds`, `OutboundClaimKinds`,
and `SkillAdditions`. In practice agents may evolve roles: a
Tester might effectively become an Inspector for a particular
cluster's domain, or vice versa.

Two viable stances:
- **Static profiles**: profiles are configuration, edited by
  operators when agent roles formally change. Keeps the system
  predictable.
- **Adaptive profiles**: profiles drift based on observed
  behavior — if an agent consistently posts a claim action it's
  not authorized for, the substrate proposes adding it (subject
  to operator review). More flexible; harder to audit.

Recommendation: static profiles for the first phase; revisit
once observed behavior shows whether drift is genuine or
merely noise.

### Authority delegation

Operator-mediated authority (via Guide) is broad — an operator
can do almost any substrate operation. Should the operator's
authority be delegable? An agent acting "with operator
authority" temporarily would let, say, a long-running Architect
issue fire claims without human-in-the-loop on each one.

This is essentially OAuth scopes for the substrate. Powerful;
risky. Operators should be able to grant time-bounded,
scope-bounded delegations to specific agent types, with
explicit revocation.

Recommendation: defer until concrete use cases emerge in
production; the static authority table is sufficient for most
intended workflows.

### Cross-cluster claim subjects

A claim that subjects a bridge node naturally requires both
clusters' stewards to validate. The current design specifies
joint validation but not the *order* — does it matter whether
cluster A's steward validates before cluster B's? Sequential
might give finer-grained provenance; parallel is faster.

Recommendation: parallel by default; the testaments combine
when both arrive. Sequential is achievable by the issuing agent
chaining claims if order matters for a specific case.

### Non-goals

- Replacing existing pruning. The antigenic field augments; it
  does not displace. Decay, BCM, scaling, lateral inhibition all
  remain primary mechanisms.
- Treating pathogens as authoritative. Pathogens are projections
  from contradiction events and Guardian/architect declarations;
  the ledger is truth, the pathogen registry is derived.
- Auto-prune without observation. Pruning decisions emit events
  to the ledger and propagate through the existing CQRS
  discipline; nothing happens without observability.
- Abandoning compartmental thinking entirely. For cases with
  small variant counts and stable antigens (a single declared
  Guardian boundary, say), a degenerate antigenic field with one
  pathogen recovers the SEIR dynamics. The framework subsumes
  rather than rejects.

## Phased Implementation Plan

This section is the build manifest. Twelve phases, each consisting
of atomic implementation items. Every item carries its own
acceptance criteria, test matrix (unit, integration, E2E,
negative path, race, leak, edge case), and a usage example.

The phases are buildable in order: each phase's correctness does
not depend on a future phase. Each phase is independently
deployable; the system is correct at every phase boundary. Within
a phase, items are mostly independent but ordered for clarity.

Test categories are abbreviated:
- **U** unit
- **I** integration
- **E** end-to-end
- **N** negative path
- **R** race detector
- **L** memory-leak
- **G** edge-case (boundary / generative)

Where a category is `n/a`, the item's nature makes that test
class non-applicable (e.g., a pure schema migration has no
goroutines, so `R` and `L` are n/a).

---

### Phase 1 — Foundation: Core Types, Schemas, Projectors

**Goal.** Land the type system, storage schemas, and projector
plumbing that every subsequent phase builds on. No dynamics yet;
just persistence + identity.

#### 1.1 Type definitions in `core/forest/nodes.go`

**Spec.** Define `Kind`, `EdgeKind`, `Stage`, `Node`, `Edge`,
`ProvenancePtr`, `Cluster`, `ClusterState`, `ChannelState`,
`PathogenVector`, `Pathogen`, `CorruptionState` per
`EMERGENT_FOREST.md` Appendix A.2 and `ECOLOGY.md` Appendix.

**Acceptance criteria.**
- All eleven types compile under Go 1.25+.
- Field ordering minimizes padding on amd64 + arm64 (verified by
  `unsafe.Sizeof` test).
- Hot fields (`NodeID`, `CoreD`, `ActivationEMA`, `EmbeddingRef`)
  in first cache line of `Node`.
- `[16]byte` UUIDs throughout; no string UUID storage on hot
  structs.

**Tests.**
- **U**: `TestNodeStructLayout` — `unsafe.Sizeof(Node{}) ≤ targetSize`
  derived from cache-line size × ceil(field count / 8).
- **U**: `TestKindCanonicalCount` — reflect over const block;
  assert `canonicalKindCount` matches actual constant count.
- **N**: `TestKindCanonicalNoEmpty` — assert no canonical kind has
  empty string.
- **G**: `TestStageBoundaryValues` — every `Stage` constant is a
  unique `int8` value within `[0, 4]`.
- **R/L**: n/a (pure type definitions).

**Usage.**
```go
n := Node{
    NodeID:        uuid.New(),
    Kind:          KindEvidence,
    Valence:       0.7,
    EmbeddingGen:  generation,
    EmbeddingRef:  vamanaID,
    CreatedAt:     time.Now().UnixNano(),
}
```

#### 1.2 Schema migrations

**Spec.** Apply the SQL DDL from `EMERGENT_FOREST.md` Appendix
A.1 + `ECOLOGY.md` Implementation, as forward-only idempotent
migrations under the existing forest migration framework.
Tables: `forest_nodes`, `forest_node_edges`, `forest_clusters`,
`forest_cluster_membership`, `forest_substrate_channels`,
`forest_substrate_field`, `forest_cluster_lineage`,
`forest_poi_cache`, `forest_pathogens`,
`forest_cluster_spectrum`, `forest_cluster_topology`,
`forest_cluster_competition`, `forest_climate_global`,
`forest_climate_microclimate`, `forest_photosynthesis_log`,
`forest_disturbance_events`, `forest_seed_bank`.

**Acceptance criteria.**
- All tables created with `STRICT` mode.
- Foreign keys enforced (`PRAGMA foreign_keys=ON`).
- Indexes match the documented patterns; no superfluous indexes.
- Migrations idempotent: applying twice produces no changes on
  the second pass.
- Schema version recorded in the forest migration ledger.

**Tests.**
- **U**: `TestMigrationApplyIdempotent` — apply migrations,
  apply again, assert no schema change between the two states.
- **I**: `TestMigrationFreshDatabase` — apply to empty DB; assert
  every table reachable via `sqlite_master`.
- **N**: `TestMigrationOnPartialState` — manually pre-create
  some tables; assert migration doesn't error and converges.
- **R/L/G**: n/a (DDL-only; no goroutines).

**Usage.**
```go
err := forest.RunMigrations(ctx, db)
```

#### 1.3 NodeProjector

**Spec.** Single-leader, lease-coordinated projector that
consumes ledger events into `forest_nodes`,
`forest_node_edges`, and `forest_substrate_channels`. Inherits
the existing forest projector contract (watermark, idempotent
replay, panic isolation). Implements `applyEvent` for
`EventInteractionRecorded`, `EventEdgeRecorded`,
`EventSubstrateChannelDelta`.

**Acceptance criteria.**
- Watermark monotonic: `last_applied_seq` only increases.
- Idempotent on `(event.ID, last_applied_seq)`.
- Vamana write attempted before the SQL transaction commits;
  Vamana failure aborts the transaction.
- Lease acquired/released with the existing forest lease type;
  no double-leadership.
- Panic recovery: a single panic in `applyEvent` does not
  corrupt watermark; next event retries.
- Tracked goroutine: the projector's main loop runs under
  `phase1.scope`; `SignalShutdown` propagates.

**Tests.**
- **U**: `TestNodeProjectorApplyIdempotent` — apply same event
  twice; assert single row write.
- **U**: `TestNodeProjectorWatermarkMonotonic` — synthesize 100
  events; assert watermark strictly increases.
- **I**: `TestNodeProjectorVamanaIntegration` — ledger event →
  projector → Vamana index contains the new node.
- **E**: `TestNodeProjectorEndToEnd` — append events to ledger;
  start projector; verify projection state matches expected.
- **N**: `TestNodeProjectorVamanaFailureAbortsTx` — mock Vamana
  to error; assert SQL transaction is rolled back.
- **R**: `TestNodeProjectorConcurrentAppendApply` — N concurrent
  appenders, projector running; assert all events eventually
  applied without duplicates under `-race`.
- **L**: `TestNodeProjectorShutdownDrains` — start projector,
  apply events, shutdown via ctx cancel; assert all goroutines
  exit within `shutdownHard`.
- **G**: `TestNodeProjectorEventOutOfOrder` — apply event with
  seq < watermark; assert no-op (no error, no duplicate).

**Usage.**
```go
proj := forest.NewNodeProjector(db, vamanaIndex, scope)
if err := proj.Start(ctx); err != nil { return err }
```

#### 1.4 ClusterProjector

**Spec.** Single-leader projector consuming the node projector's
downstream signal (cluster-affecting events) into
`forest_clusters`, `forest_cluster_membership`, and
`forest_cluster_lineage`. Batched: runs at maintenance cadence,
not per-event. Same lease/watermark/idempotency contract as
NodeProjector.

**Acceptance criteria.**
- Batched application: `applyBatch` consumes ≤ `maxBatch` events
  per transaction.
- `maxBatch` derived from observed event rate, not literal.
- Cluster ID stable across replays; same input produces same IDs.
- Lineage events append-only; no rewrites.
- Cyclomatic complexity ≤ 3 per function.

**Tests.**
- **U**: `TestClusterProjectorBatchSize` — assert batch size ≤
  `maxBatch`.
- **U**: `TestClusterProjectorLineageAppendOnly` — attempt to
  modify a lineage row; assert the projector never issues
  UPDATE or DELETE.
- **I**: `TestClusterProjectorWithNodeProjector` — run both
  projectors against a synthetic event stream; assert
  consistency between `forest_nodes` and
  `forest_cluster_membership`.
- **E**: `TestClusterProjectorEndToEnd` — append events
  representing speciation/merge/split; verify cluster state.
- **N**: `TestClusterProjectorBadEvent` — inject malformed
  cluster event; assert the batch's other events still apply.
- **R**: `TestClusterProjectorConcurrentLeaders` — start two
  projector instances under contention; assert single leader
  via lease.
- **L**: `TestClusterProjectorShutdownClean` — verify all
  goroutines exit on `SignalShutdown`.
- **G**: `TestClusterProjectorEmptyBatch` — apply empty event
  list; assert no SQL writes, no error.

**Usage.**
```go
clusterProj := forest.NewClusterProjector(db, scope, archivalistNamer)
clusterProj.Start(ctx)
```

---

### Phase 2 — Density Layer Over Vamana

**Goal.** Build the thin density-clustering layer that subsumes
HDBSCAN's primitives via Vamana + IVF.

#### 2.1 `CoreDistanceCache`

**Spec.** Per-node `core_d` cache backed by `sync.Map`,
populated on miss via `vamana.KNN`. Invalidated on neighborhood
change. Persistent backing in `forest_nodes.core_d`; in-memory
copy for hot reads.

**Acceptance criteria.**
- O(1) hit-path read.
- Miss-path bounded by Vamana KNN cost (typically O(log N)).
- `Invalidate(nodeID)` removes the cached entry; next read
  refreshes.
- `mPts` derived from `canonicalKindCount / 2` (no literal).
- Unbounded growth prevented: cache size capped at active node
  count via maintenance-loop trim.

**Tests.**
- **U**: `TestCoreDistanceCacheHitMiss` — hit returns cached;
  miss triggers Vamana call and stores.
- **U**: `TestCoreDistanceCacheInvalidate` — `Invalidate` →
  next read is a miss.
- **I**: `TestCoreDistanceCacheVamanaIntegration` — real Vamana
  index; insert nodes; assert `CoreD` returns the m_pts-th
  nearest distance.
- **N**: `TestCoreDistanceCacheVamanaError` — Vamana returns
  error; cache returns the error, doesn't store.
- **R**: `TestCoreDistanceCacheConcurrent` — N goroutines
  reading/invalidating same node; assert no torn reads under
  `-race`.
- **L**: `TestCoreDistanceCacheBounded` — populate to `2× active
  count`; trigger trim; assert size ≤ active count after.
- **G**: `TestCoreDistanceCacheInsufficientNeighbors` — node has
  < `m_pts` neighbors; returns `math.MaxFloat32`.

**Usage.**
```go
cache := forest.NewCoreDistanceCache(vamanaIndex, mPtsAnchor)
d, err := cache.CoreD(nodeID)
```

#### 2.2 `MutualReachability` and `densityReachableFrom`

**Spec.** `MutualReachability(a,b) = max(core_d(a), core_d(b),
d(a,b))`. `densityReachableFrom` performs ctx-aware BFS over
Vamana edges, traversing where `d_mreach < epsilon`. Bounded by
`maxNodes`; cyclomatic complexity ≤ 3 via `expandNeighbors`
extraction.

**Acceptance criteria.**
- BFS is ctx-cancellable; returns `ctx.Err()` on cancel.
- `maxNodes` bound respected; never exceeds.
- Returns the connected component; no spurious nodes.
- `MutualReachability` is symmetric in its arguments.

**Tests.**
- **U**: `TestMutualReachabilitySymmetric` — `MR(a,b) == MR(b,a)`.
- **U**: `TestDensityReachableFromBounded` — synthetic graph
  with > `maxNodes` reachable; assert exactly `maxNodes` returned.
- **I**: `TestDensityReachableFromVamana` — real Vamana index;
  insert dense cluster; assert all members density-reachable.
- **E**: `TestDensityClusterCrystallization` — populate noise
  pool; trigger compaction; dense subgraphs become candidate
  clusters.
- **N**: `TestDensityReachableFromCancelled` — cancel ctx
  mid-BFS; assert returns `context.Canceled`.
- **R**: `TestDensityReachableFromConcurrent` — N concurrent
  BFS calls; assert correctness under `-race`.
- **L**: n/a (no goroutines spawned).
- **G**: `TestDensityReachableFromIsolatedSeed` — seed has no
  reachable neighbors; returns `{seed}`.

**Usage.**
```go
component, err := densityReachableFrom(
    ctx, cache, vamana, seedID, epsilon, maxNodes)
```

#### 2.3 Tentative membership assignment

**Spec.** `AssignTentative` assigns top-K cluster membership for
a newly inserted node. O(m_pts) Vamana queries per insertion;
no density-reachable BFS on the hot path. Membership weights
normalized to sum to 1.

**Acceptance criteria.**
- Returns at most `membershipK` (= 3) memberships per node.
- Weights sum to 1 (within float epsilon).
- Weights ranked descending; rank 0 is primary cluster.
- ctx-aware; cancellable.

**Tests.**
- **U**: `TestAssignTentativeWeightSumOne`.
- **U**: `TestAssignTentativeRankOrder` — rank 0 has highest
  weight.
- **I**: `TestAssignTentativeBridge` — node between two
  clusters; assigned to both with weights reflecting proximity.
- **N**: `TestAssignTentativeNoNeighbors` — empty assignment,
  no error.
- **R**: `TestAssignTentativeConcurrent` — N concurrent
  insertions; assert no torn membership writes.
- **G**: `TestAssignTentativeKSaturation` — node reachable to
  > membershipK clusters; only top-K stored.

**Usage.**
```go
memberships, err := s.AssignTentative(ctx, nodeID)
```

#### 2.4 Compaction, cohesion, merge, split detection

**Spec.** Maintenance-loop subroutines: `clusterCompaction`
(noise → cluster), `clusterCohesion` (split detection),
`clusterMergeCheck` (merge detection). Each emits ledger
events; no direct projection writes.

**Acceptance criteria.**
- All three are ctx-bounded.
- Compaction respects `minComponentSize = canonicalKindCount`
  and `maxComponentSize = q90 cluster size`.
- Cohesion check fires only when interior density valley
  exceeds threshold.
- Merge check requires bridge nodes density-connecting two
  cluster cores.
- All decisions emit ledger events; cluster projector consumes.
- Idempotent: re-running on the same state produces no new
  events.

**Tests.**
- **U**: `TestCompactionMinSizeRespected` — noise pool just
  below `minComponentSize` does not crystallize.
- **U**: `TestCohesionSplitOnValley` — synthetic cluster with
  density valley → split event emitted.
- **I**: `TestCompactionEndToEnd` — accumulate noise; run
  compaction; assert ledger event + cluster projection update.
- **E**: `TestClusterLifecycleEnd2End` — cluster crystallizes,
  grows, splits via decay, merges via new bridges; assert
  lineage records the entire history.
- **N**: `TestCompactionOversizeNoise` — noise component > q90;
  logged but not crystallized.
- **R**: `TestMaintenanceConcurrentDetectors` — all three
  detectors run on the same maintenance cycle under `-race`.
- **L**: `TestCompactionShutdown` — long-running compaction;
  ctx cancel exits within budget.
- **G**: `TestMergeOscillation` — bridge nodes that flicker
  in/out of density-connection: assert no merge/split flapping
  via hysteresis.

**Usage.**
```go
err := s.maintenanceCycle(ctx) // includes all three detectors
```

#### 2.5 `epsilonFor` derivation

**Spec.** Per-cluster epsilon = `cluster.density_profile.q90 ×
(1 + D_A/D_I)`. Anchored on cluster history × substrate
diffusion ratio. No literals.

**Acceptance criteria.**
- Returns the cluster's q90 reachability when `D_A == D_I`.
- Expansion factor scales linearly with `D_A / D_I`.
- Bounded: returns `q90` (not negative, not infinite) when
  `D_I ≤ 0`.

**Tests.**
- **U**: `TestEpsilonForBaseline` — `D_A = D_I` → returns q90.
- **U**: `TestEpsilonForExpansion` — `D_A > D_I` → returns
  `> q90`.
- **G**: `TestEpsilonForZeroInhibitor` — `D_I = 0` → returns
  q90.
- Other categories n/a (pure function).

**Usage.**
```go
eps := epsilonFor(cluster, scales.DA, scales.DI)
```

---

### Phase 3 — Substrate Dynamics

**Goal.** BCM, synaptic scaling, resource economy, decay,
reaction-diffusion.

#### 3.1 `ActivationEMA` and `BCMUpdate`

**Spec.** Continuous-time EMA of post-synaptic activity
(`y_j` and `⟨y_j²⟩`) computed in discrete events via
`alpha = 1 - exp(-elapsed/tau)`. `BCMUpdate` returns the
post-update edge weight using the BCM φ.

**Acceptance criteria.**
- EMA convergence: under constant input, EMA converges to that
  value within tolerance after τ.
- BCM produces LTP when `y > θ_M`; LTD when `y < θ_M`.
- Weight clamped to `[0, 1]`.
- `bcmLearningRate` derived from observed event rate, not
  literal.

**Tests.**
- **U**: `TestActivationEMAConvergence`.
- **U**: `TestBCMLTPLTDDirection`.
- **U**: `TestBCMWeightClamped` — extreme inputs in `[0, 1]`.
- **I**: `TestBCMOnNodeProjection`.
- **N**: `TestBCMNegativeInputs` — LTD direction preserved.
- **G**: `TestBCMZeroTau` — `tau = 0` → alpha = 1.

**Usage.**
```go
emaNew, sqNew := ActivationEMA(prev, prevSq, instant, tau, elapsed)
weight = BCMUpdate(weight, preAct, emaNew, sqNew)
```

#### 3.2 `SynapticScale` and inbound-weight enforcement

**Spec.** Multiplier when `inboundSum > B = canonicalKindCount`.
Applied per-cycle to inbound edges of over-budget nodes.

**Acceptance criteria.**
- Returns 1.0 below budget.
- Returns `B / inboundSum` above budget.
- After scaling, sum equals `B` (within float epsilon).

**Tests.**
- **U**: `TestSynapticScaleUnderBudget`.
- **U**: `TestSynapticScaleOverBudget` — scale × original sum =
  B.
- **I**: `TestSynapticScalingMaintenanceStep`.
- **N**: `TestSynapticScaleZeroSum` — no division by zero.
- **R**: `TestSynapticScalingConcurrent`.
- **G**: `TestSynapticScaleOverflowGuard`.

**Usage.**
```go
scale := SynapticScale(inboundSum)
```

#### 3.3 Channel exchange rules and `ChannelDeposit`

**Spec.** Per-edge resource updates per the `exchangeRules`
table. Kind-grounded: every entry justified by edge semantics.
Checksum-tested.

**Acceptance criteria.**
- Validation moves nitrogen + small carbon by documented amounts.
- Contradiction drains nitrogen.
- Co-activation moves water symmetrically.
- Updates emit ledger events for `forest_substrate_channels`.

**Tests.**
- **U**: `TestChannelDepositValidation` — nitrogen +1, carbon +0.25.
- **U**: `TestChannelDepositContradiction` — nitrogen -1.
- **U**: `TestChannelExchangeIsKindGrounded` — checksum.
- **I**: `TestChannelDepositPersisted`.
- **N**: `TestChannelDepositUnknownKind` — zero-delta.
- **R**: `TestChannelDepositConcurrent`.
- **G**: `TestChannelDepositAccumulates`.

**Usage.**
```go
nextState := ChannelDeposit(curState, valence, edgeKind)
```

#### 3.4 Decay sweep

**Spec.** Per-kind power-law `w(t) = w_0 (1 + t/τ_k)^(-β_k)`
with `MinFloor`. Lazy: stored values are at `last_event_at`;
effective values computed at read time.

**Acceptance criteria.**
- `DecayedWeight` matches the analytic formula within tolerance.
- Contradiction edges respect `MinFloor`.
- Decay sweep within maintenance budget.

**Tests.**
- **U**: `TestDecayedWeightFormula`.
- **U**: `TestDecayedWeightFloor` — contradiction asymptotes
  at `MinFloor`.
- **I**: `TestDecaySweepEndToEnd`.
- **N**: `TestDecaySweepStaleClock` — clock skew handled.
- **R**: `TestDecaySweepConcurrent`.
- **L**: `TestDecaySweepShutdown`.
- **G**: `TestDecaySweepZeroAge` — returns w₀.

**Usage.**
```go
effective := DecayedWeight(stored, time.Since(lastEvent), shape)
```

#### 3.5 Reaction-diffusion via `RelaxField`

**Spec.** Jacobi iteration of the discrete Gierer-Meinhardt PDE
on the active subgraph. Per-cycle local diffusion; full
relaxation at slower cadence.

**Acceptance criteria.**
- Convergence: `MaxDelta` monotonically decreases.
- Iterations capped.
- ctx-cancellable.
- Per-channel separation.

**Tests.**
- **U**: `TestRelaxFieldConvergence`.
- **U**: `TestRelaxFieldMaxIterations`.
- **I**: `TestRelaxFieldOnSubgraph`.
- **E**: `TestSubstrateRelaxationE2E`.
- **N**: `TestRelaxFieldNanInput` — NaN guards.
- **R**: `TestRelaxFieldConcurrent`.
- **L**: `TestRelaxFieldShutdown`.
- **G**: `TestRelaxFieldEmptySubgraph`.

**Usage.**
```go
err := RelaxField(ctx, subgraph, field, scales, maxIterations)
```

---

### Phase 4 — Maintenance Loop & Lifecycle

**Goal.** Orchestrating loop, derived cadences, stage
predicates, naming gate.

#### 4.1 `runMaintenance` loop

**Spec.** Long-lived ctx-aware tracked goroutine running
`maintenanceCycle` at `cadence.Cycle`. Each step is
budget-bounded by phase weight. All steps emit ledger events;
idempotent under replay.

**Acceptance criteria.**
- Tracked via `concurrency.GoroutineScope`.
- ctx cancellation exits within `shutdownHard`.
- Each step's `context.WithTimeout` honors phase budget.
- Cycle ordering matches the spec.
- Idempotent: re-running a cycle produces same outcome.

**Tests.**
- **U**: `TestMaintenanceCyclePhaseOrder`.
- **I**: `TestMaintenanceCyclePhaseBudgets`.
- **E**: `TestMaintenanceLoopOverNCycles`.
- **N**: `TestMaintenanceCycleStepError` — cycle continues to
  next step.
- **R**: `TestMaintenanceLoopShutdownUnderLoad`.
- **L**: `TestMaintenanceLoopGoroutineCount` — returns to
  baseline after shutdown.
- **G**: `TestMaintenanceCycleDeadlineExceeded`.

**Usage.**
```go
go s.runMaintenance(ctx) // tracked on m.wg
```

#### 4.2 `DeriveCadence`

**Spec.** Compute `MaintenanceCadence` from observed event
cadence × `canonicalKindCount`. Phase weights sum to 1; budgets
proportional. `minMaintenanceCycle` = `canonicalKindCount ×
MinMeaningfulGap`.

**Acceptance criteria.**
- Returns positive durations for all fields.
- Phase weights sum to 1 ± float epsilon.
- Cycle ≥ `minMaintenanceCycle`.

**Tests.**
- **U**: `TestDeriveCadencePhaseWeightsSum`.
- **U**: `TestDeriveCadenceMinFloor`.
- **G**: `TestDeriveCadenceMonotonic`.

**Usage.**
```go
cadence := DeriveCadence(observedCadence)
```

#### 4.3 Stage predicates

**Spec.** `StageOf(node, edges, now)` returns developmental
stage. Critical-period detection runs first. Cyclomatic ≤ 3 via
`classifyByCounts` extraction.

**Acceptance criteria.**
- Stage transitions are derivative; no maintained state.
- Thresholds anchored on `canonicalKindCount`.
- Critical-period reopening triggers when contradiction load >
  threshold.

**Tests.**
- **U**: `TestStageOfPioneer`.
- **U**: `TestStageOfClimaxThreshold`.
- **U**: `TestStageOfCriticalPeriod`.
- **U**: `TestStageOfSnag`.
- **G**: `TestStageOfBoundary` — inclusive boundaries.

**Usage.**
```go
stage := StageOf(node, edges, time.Now().UnixNano())
```

#### 4.4 Curator-naming subsystem

**Spec.** `CuratorNamer` interface; `curatorBatchSize =
canonicalKindCount` rate-limit. Holding pen for unnamed
candidates queryable by interaction proximity.

**Acceptance criteria.**
- ≤ `curatorBatchSize` candidates per cycle.
- Unnamed candidates remain in `forest_clusters` with state =
  `Candidate`.
- Renaming preserves old name as alias.

**Tests.**
- **U**: `TestCuratorBatchSizeRespected`.
- **I**: `TestCuratorNamingPersisted`.
- **E**: `TestCuratorEndToEndCandidateToNamed`.
- **N**: `TestCuratorNamingFailure` — candidate remains in
  holding pen.
- **R**: `TestCuratorConcurrentNaming`.
- **L**: `TestCuratorShutdown`.
- **G**: `TestCuratorEmptyCandidateSet`.

**Usage.**
```go
namer := archivalist.NewCuratorNamer(...)
proj := forest.NewClusterProjector(db, scope, namer)
```

#### 4.5 Replay scheduler

**Spec.** Maintenance-loop subroutine emitting replay-priority
items for brittle, underused-gold, and stage-transition-proximate
nodes.

**Acceptance criteria.**
- Brittle detection via in-degree-weight × low-activation ratio.
- Underused-gold via stage ∈ {Mature, Climax} ∧ signature
  magnitude > q75 ∧ activation_ema < q25 of cohort.
- Stage-transition proximity factored into priority.

**Tests.**
- **U**: `TestReplayScheduleBrittleDetection`.
- **U**: `TestReplayScheduleUnderusedGold`.
- **I**: `TestReplayScheduleEndToEnd`.
- **N**: `TestReplayScheduleNoCandidates`.
- **G**: `TestReplaySchedulePriorityOrdering`.

**Usage.**
```go
err := s.replaySchedule(ctx)
```

---

### Phase 5 — PoI Views

**Goal.** Step-zero retrieval primitives. Each view computed at
maintenance cadence; cached in `forest_poi_cache` with TTL = one
cycle.

#### 5.1 Hot zones, boundary zones, frontier

**Tests.**
- **U**: `TestHotZonesSpike`, `TestBoundaryZoneDetection`,
  `TestFrontierEdge`.
- **I**: `TestPoICacheTTL` — entries expire after one cycle.
- **G**: `TestPoIEmptyCluster` — no detection on empty inputs.

#### 5.2 Keystones via Brandes

**Spec.** Betweenness centrality per cluster. Brandes on
subgraphs ≤ `keystoneSubgraphCap`; sampled approximation above.

**Tests.**
- **U**: `TestKeystoneBrandesSmall` — known graph; matches
  analytic.
- **I**: `TestKeystoneApproximation` — large cluster within
  tolerance.
- **L**: `TestKeystoneLargeClusterBudget` — sampling kicks in.

#### 5.3 Brittle, bridges, underused-gold

**Tests.** Per-detector positive and negative cases.

#### 5.4 PoI cache invalidation

**Tests.**
- **U**: `TestPoICacheTTL`.
- **I**: `TestPoICacheInvalidationOnDisturbance`.
- **R**: `TestPoICacheConcurrentInvalidation`.

#### 5.5 PoI integration with retrieval

**Spec.** PoI views compose as step-zero in retrieval pipeline.

**Tests.**
- **E**: `TestRetrievalUsesPoI` — query → PoI consulted →
  candidates filtered before scoring.
- **G**: `TestRetrievalNoPoI` — fallback when cache empty.

---

### Phase 6 — Antigenic Field

**Goal.** Pathogens as vectors, per-node corruption/immunity,
spectral outbreak detection, recovery via cited validation.

#### 6.1 Pathogen registry & embedding

**Acceptance criteria.**
- Pathogen IDs are UUIDv4, persistent across replay.
- Mutation tree maintained via `parent_pathogen_id`.

**Tests.**
- **U**: `TestPathogenIDStability`.
- **I**: `TestPathogenMutationTree`.
- **E**: `TestPathogenLineageEnd2End`.
- **N**: `TestPathogenInvalidParent`.
- **R**: `TestPathogenConcurrentDeclaration`.
- **G**: `TestPathogenSelfReference` — rejected.

#### 6.2 Per-node corruption/immunity vectors

**Acceptance criteria.**
- Infection monotonic in corruption magnitude.
- Cross-immunity: cosine-graded coverage.
- `QuarantineFactor` ∈ `[0, 1]`.

**Tests.**
- **U**: `TestInfectionMonotonic`.
- **U**: `TestCrossImmunity`.
- **U**: `TestQuarantineFactorBounded`.
- **I**: `TestQuarantineFactorRetrieval`.
- **E**: `TestRecoveryViaCitedValidation`.
- **R**: `TestCorruptionVectorConcurrent`.
- **G**: `TestZeroNormPathogen` — no division by zero.

#### 6.3 Diffusion field update

**Tests.**
- **U**: `TestCorruptionDiffusionConservation`.
- **I**: `TestCorruptionDiffusionSubgraph`.
- **R**: `TestCorruptionDiffusionConcurrent`.
- **L**: `TestCorruptionDiffusionShutdown`.
- **G**: `TestCorruptionDiffusionEmptySubgraph`.

#### 6.4 Spectral outbreak detection

**Tests.**
- **U**: `TestLanczosConvergenceOnKnownEigenvalues`.
- **I**: `TestSpectralOutbreakOnSyntheticCluster`.
- **N**: `TestSpectralBelowMinSize` — returns NaN.
- **L**: `TestSpectralLargeCluster` — bounded iterations.
- **G**: `TestSpectralDisconnectedCluster`.

#### 6.5 Recovery via cited validation

**Tests.**
- **U**: `TestImmunityUpdateOnCitedValidation`.
- **I**: `TestRecoveryEndToEnd`.
- **N**: `TestUncitedValidation` — immunity unchanged.

---

### Phase 7 — Enhanced Pruning

**Goal.** Two-axis pruning, spectral pruning, cluster-level
split-pruning, replay-protected immunity, snag-via-cure-failure.

#### 7.1 Two-axis prune scoring

**Tests.**
- **U**: `TestPrunePriorityFourCorners` — heavy-clean,
  warm-poisoned, cold-immune, cold-poisoned.
- **I**: `TestPrunePriorityIntegration`.
- **G**: `TestPrunePriorityEqualWeights`.

#### 7.2 Spectral pruning targets

**Tests.**
- **U**: `TestSpectralPrunSurgery` — exact target set.
- **I**: `TestSpectralPrunIntegration`.
- **N**: `TestSpectralPrunNoPathology`.

#### 7.3 Cluster-level split-pruning

**Tests.**
- **I**: `TestSplitPruningOnBifurcation`.
- **G**: `TestSplitPruningPreservesHealthyDaughter`.

#### 7.4 Replay-protected immunity

**Tests.**
- **U**: `TestReplayProtectsImmunity`.
- **I**: `TestReplayCrossImmunization`.

#### 7.5 Snag via cure failure

**Tests.**
- **U**: `TestSnagFromCureFailure`.
- **G**: `TestSnagThresholdDerivation` — median cure cycle count.

---

### Phase 8 — Competition & Hybrid Vigor

**Goal.** Inter-cluster Lotka-Volterra; hybrid nodes with
parental inheritance and outbreeding depression.

#### 8.1 `forest_cluster_competition` recompute

**Tests.**
- **U**: `TestCompetitionMatrixSparse`.
- **I**: `TestCompetitionMatrixUpdate`.
- **R**: `TestCompetitionMatrixConcurrent`.

#### 8.2 Lotka-Volterra integration step

**Tests.**
- **U**: `TestLotkaVolterraEquilibrium`.
- **U**: `TestCompetitiveExclusion`.
- **I**: `TestCompetitionEndToEnd`.
- **G**: `TestLotkaVolterraExtinction`.

#### 8.3 Hybrid detection

**Tests.**
- **U**: `TestHybridScoreEntropy`.
- **I**: `TestHybridDetectionAtInsertion`.

#### 8.4 Parent inheritance

**Tests.**
- **U**: `TestImmunityVectorOR` — OR semantics.
- **U**: `TestParentSignatureWeighted`.
- **I**: `TestHybridInheritanceAtInsertion`.

#### 8.5 Outbreeding depression

**Tests.**
- **U**: `TestOutbreedingPenalty`.
- **G**: `TestOutbreedingThresholdBoundary` — inclusive.

---

### Phase 9 — Climate, Photosynthesis, Disturbance

**Goal.** Energy accounting, ambient gain control, disturbance
response.

#### 9.1 Photosynthesis attribution

**Tests.**
- **U**: `TestPhotosynthesisProfileAttribution`.
- **I**: `TestPhotosynthesisEndToEnd`.
- **G**: `TestPhotosynthesisUnknownAgent` — zero contribution.

#### 9.2 Climate computation

**Tests.**
- **U**: `TestClimateGainBounded`.
- **U**: `TestSeasonalFourierFundamental`.
- **I**: `TestClimateMicroclimateLayer`.

#### 9.3 Disturbance detection (drought/winter/flood)

**Tests.**
- **U**: `TestDroughtDetectionThreshold`.
- **U**: `TestHardWinterDetectionDrop`.
- **U**: `TestFloodDetectionRate`.
- **I**: `TestDisturbanceResponseEnd2End`.
- **N**: `TestDisturbanceFalsePositive`.
- **G**: `TestMultipleDisturbancesSimultaneous`.

#### 9.4 Fire (operator-initiated)

**Tests.**
- **U**: `TestFireSeedBankPreservation`.
- **I**: `TestFireQuarantine` — bridges blocked during burn.
- **E**: `TestFireRecoveryEnd2End` — succession dynamics.

#### 9.5 Critical slowing down

**Tests.**
- **U**: `TestSlowdownSignalAtBaseline` — stable cluster ≈ 1.
- **I**: `TestSlowdownLeadsRegimeShift` — ≥ window/2 cycles
  lead time.
- **G**: `TestSlowdownInsufficientSamples` — returns 0.

#### 9.6 Resilience metric

**Tests.**
- **U**: `TestResilienceMetricComponents`.
- **I**: `TestResilienceMetricChangesAfterDisturbance`.

---

### Phase 10 — Forest as Participant

**Goal.** Substrate Agent emission; trace baggage; subscription
dispatch; authority enforcement.

#### 10.1 Substrate Agent emission

**Tests.**
- **U**: `TestEmitTestamentBatched`.
- **I**: `TestEmitTestamentReachesAgents`.
- **N**: `TestEmitClaimUnauthorizedSubject`.
- **R**: `TestEmitConcurrent`.
- **L**: `TestEmitShutdown`.

**Usage.**
```go
err := substrateAgent.EmitTestament(ctx, []claims.Artifact{
    {Kind: "outbreak_detected", Reference: "..."},
})
```

#### 10.2 Trace baggage tier

**Tests.**
- **U**: `TestBaggageSizeBudget` — ≤ 4 × `canonicalKindCount`
  bytes.
- **U**: `TestBaggageRoundTrip`.
- **I**: `TestBaggagePropagation` — multi-hop without drift.
- **G**: `TestBaggageEmptyCursor`.

#### 10.3 Subscription dispatch

**Tests.**
- **U**: `TestSubscriptionDispatchMatching`.
- **U**: `TestSubscriptionDispatchNonMatching` — no fanout.
- **I**: `TestSubscriptionDispatchPersistedSubscribers`.
- **R**: `TestSubscriptionDispatchConcurrent`.
- **L**: `TestSubscriptionDispatchShutdown`.

#### 10.4 Authority enforcement

**Tests.**
- **U**: `TestAuthorityArchitectCanDeclarePathogen`.
- **U**: `TestAuthorityEngineerCannotDeclareFire`.
- **U**: `TestAuthorityOperatorBroad`.
- **I**: `TestAuthorityRejectionSurfaces` — constraint cited.
- **N**: `TestAuthorityNoProfile` — unknown agent rejected.

---

### Phase 11 — Per-Agent Specialization

**Goal.** Twelve agent profiles wired; per-agent skills
registered.

#### 11.1 `agentProfiles` registry

**Tests.**
- **U**: `TestAgentProfilesComplete` — all twelve agents.
- **U**: `TestAgentProfileFieldsPopulated`.
- **G**: `TestNoOverlappingAuthority` — intentional overlaps
  documented.

#### 11.2 Per-agent skill registration

**Tests.**
- **U**: `TestSkillRegistrationPerProfile` — Engineer registers
  exactly its profile's `SkillAdditions`.
- **I**: `TestSkillCallReachesSubstrate`.
- **N**: `TestSkillUnauthorizedAction` — call fails.

#### 11.3 Cross-agent forest coordination

**Tests.**
- **E**: `TestOutbreakRemediationFlow` — outbreak → Architect
  → remediation → Tester validation → cure.
- **E**: `TestFireRecoveryFlow` — operator fire → Guardian
  validation → Archivalist seed bank → succession.
- **E**: `TestSpeciationToNamedCluster` — noise →
  crystallization → naming → Guardian approval → skill
  surface.

---

### Phase 12 — Retrieval Cutover & Deprecation

**Goal.** Branch Packets hydrated from interaction-node paths;
old derived caches deprecated.

#### 12.1 Branch Packet hydration from paths

**Tests.**
- **U**: `TestHydrateBranchPacketFields` — required fields.
- **I**: `TestHydrateBranchPacketEnd2End`.
- **R**: `TestHydrateBranchPacketConcurrent`.
- **L**: `TestHydrateBranchPacketBoundedAlloc`.

#### 12.2 Retrieval pipeline updated

**Tests.**
- **U**: `TestRetrievalPipelineSteps`.
- **I**: `TestRerankerWithNewFeatures` — improves rank quality
  vs baseline.
- **E**: `TestRetrievalEnd2End`.

#### 12.3 Deprecation of `forest_branches` and `forest_relay_edges`

**Tests.**
- **I**: `TestForestBranchesDerivedFromNodes` — derived view
  matches original.
- **E**: `TestRetirementOfBranchProjector`.
- **G**: `TestBackwardCompatibilityWindow` — legacy APIs
  equivalent results.

---

### Phase Summary Table

| Phase | Theme | Items | Critical Tests |
|---|---|---|---|
| 1 | Foundation | 4 | Idempotent replay, watermark monotonicity |
| 2 | Density Layer | 5 | Compaction correctness, cohesion split |
| 3 | Substrate Dynamics | 5 | BCM directionality, decay floor, R-D convergence |
| 4 | Maintenance & Lifecycle | 5 | Phase budgets, stage derivation, naming gate |
| 5 | PoI Views | 5 | Cache TTL, detector accuracy, large-cluster bounds |
| 6 | Antigenic Field | 5 | Cross-immunity grading, spectral outbreak |
| 7 | Enhanced Pruning | 5 | Two-axis four-corners, spectral surgery |
| 8 | Competition & Hybrid | 5 | LV equilibrium, hybrid OR-immunity, outbreeding |
| 9 | Climate & Disturbance | 6 | Disturbance recovery, slowdown lead time |
| 10 | Forest as Participant | 4 | Authority enforcement, baggage size bound |
| 11 | Per-Agent Specialization | 3 | Profile completeness, cross-agent flows |
| 12 | Retrieval Cutover | 3 | Branch packet shape, deprecation safety |

Each phase ships independently behind a feature gate; the system
is correct at every phase boundary. The full sequence is a
~6-month engineering arc with ~60 atomic items, each
individually mergeable and shippable.

## Summary

The Emergent Forest's substrate-level dynamics — BCM, reaction-
diffusion, lateral inhibition, developmental staging — are the
forest's *cellular* biology. This document extends the forest
into its *ecosystem* biology:

- **The Antigenic Field** replaces SIR/SEIR disease modeling with
  pathogens as vectors in continuous embedding space, immunity
  as vector quantities, mutation as drift, and outbreak
  detection via spectral signatures. This is what computational
  immunology actually uses for influenza, HIV, SARS-CoV-2 — the
  framework that scales to the chronic-evolving regime.
- **Enhanced Pruning** turns pruning from one-dimensional
  (warmth) to multi-dimensional (warmth × cleanliness ×
  structural-load × topological-coherence). Every existing
  pruning mechanism's "missed" case is closed.
- **Inter-Cluster Competition** (Lotka-Volterra) gives the
  substrate explicit cluster-vs-cluster dynamics when topics
  overlap. Competitive exclusion drives reframing; mutualism
  protects co-activated pairs; apparent competition surfaces
  shared Guardian pressure.
- **Cross-Pollination and Hybrid Vigor** turns bridges from
  passive structural connectors into active sites of synthesis.
  Hybrid nodes inherit the immunity-union of their parents,
  carrying coverage neither parent had alone.
- **Climate, Microclimate, Photosynthesis** makes the substrate's
  coupling to user input explicit. User activity is the sun;
  agents are photosynthetic species with characteristic
  resource profiles; climate gates dynamics globally; per-cluster
  microclimate gates locally; Climax nodes act as carbon sinks.
- **Disturbance Regimes** (drought, hard winter, fire, flood)
  give the substrate distinct response profiles for distinct
  stressors, with recovery curves drawn from real ecological
  succession theory. Critical slowing down provides anticipatory
  warning before tipping points.

The forest is no longer just a memory store with biological
metaphors. It is a working ecosystem model — borrowing
mathematical frameworks from computational immunology
(antigenic cartography, quasispecies theory), spectral
epidemiology (Pastor-Satorras), persistent homology (Carlsson,
Edelsbrunner-Harer), Lotka-Volterra population dynamics
(canonical), photosynthesis stoichiometry (Redfield),
succession theory (Connell-Slatyer), and resilience ecology
(Holling, Scheffer). Each mechanism is a known biological
phenomenon translated into the forest's substrate vocabulary,
not a metaphor stretched to fit.

The composition is the novelty: a single coherent substrate
where cellular biology (BCM, reaction-diffusion) and ecosystem
biology (competition, succession, photosynthesis, disturbance)
run on the same graph through the same maintenance loop,
mutually reinforcing each other's signals.

The substrate is also a **participant** in the agent ecosystem,
not just a substrate beneath it. The Substrate Agent emits
testaments through fabric, issues claims to the agents whose
authority each substrate decision requires, and accepts claims
from the agents authorized to direct its operation. Twelve agents
each have a distinct forest profile — what artifact kinds they
subscribe to, what claims they can post, what their role is in
disease propagation, disturbance recovery, and resource
production. The agent ecosystem shapes the forest, the forest
shapes agent operation, the user shapes both, and the ledger is
truth across all of it.

The forest the agent sees is the forest the user planted, kept
healthy by the agents collectively, shaped by the climate of
their interaction, resilient against the disturbances of their
working life, and responsive to every agent's role in maintaining
its health. Each agent is different, and the forest knows it.
