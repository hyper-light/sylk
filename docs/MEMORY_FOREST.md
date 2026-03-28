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
