# Agent Activity Fabric

A unified coordination + observation substrate for sylk's multi-agent system.
The fabric is the *ambient intelligence plane* over sylk's existing sovereign
storage systems: it captures every action at appropriate resolution, surfaces
peer activity to every agent without ever blocking primary work, and lets
agents collaborate or challenge across pipelines via uniform primitives.

This document is the consolidated design after four rounds of refinement:

1. **Sovereign systems + ambient plane** — existing stores stay authoritative;
   the fabric is additive.
2. **No-gates, auto-publish, ambient awareness** — decisions are emergent
   from work, not prerequisites for it.
3. **Cross-pipeline collaboration** — challenges and consults span pipeline
   boundaries.
4. **Chokepoint instrumentation for total granular capture** — every action
   in sylk is captured at appropriate resolution via instrumented funnel
   points and CI-enforced invariants.

---

## Why the original "central substrate" framing was wrong

The first cut of this design proposed replacing the Decision Manifest, the
coordination service tables, validation_epochs / execution_holds /
remediation_cases, the pipeline-protocol durable log, and the bus tool-call
events with views over a single `agent_activity` table. The intent was right
— these surfaces *are* structurally similar. But each one carries semantics
that don't live in its schema:

- **Where work happens.** Manifest auto-promotion runs *inside* `RunInWriteTx`
  scanning every existing decision in the domain — recreating it as a write
  hook on activity append loses the same-transaction atomicity that makes it
  race-free.
- **When state is computed.** Coord lease renewal mutates `lease_expires_at`
  *in place*; an append-only model has to express renewal as supersession
  chains, turning O(1) updates into O(chain) reads.
- **What stays deliberately transient.** Pipeline-protocol obligations are
  *not* journaled by design. Bus tool-call events are fire-and-forget by
  design. Forcing them into durable storage changes their cost model.
- **Atomic per-task isolation.** Pipeline-protocol's WAL journal is
  per-task; collapsing into a global table eliminates the boundary that
  makes per-task replay/recovery clean.

So we keep every working system intact and let the fabric be **net-additive**:
sovereign stores stay sovereign; the fabric observes them.

## Why the central design's strength still matters

The original design had one virtue we cannot afford to lose: it captured
*every action* in sylk because every operation flowed through one substrate.
Without that, the fabric is useful but partial — it sees intentional
collaboration but misses infrastructural events (bus deliveries, LLM round-
trips, file reads, cache hits, retries, tier transitions, errors).

The reconciliation: keep central capture but distribute it through
**chokepoint instrumentation**. Sylk's codebase already funnels every action
through ~10 well-known abstractions. Instrument each one. CI lints enforce
that no new code escapes capture. Result: brute-force totalism without
central storage; sovereign systems keep their semantics; every action becomes
a fabric activity at appropriate resolution.

---

## Part 1 — Sovereign systems, ambient plane

### The diagnosis (recap)

| Surface                  | What it captures              | Where it lives                        | How agents read it today        |
|--------------------------|-------------------------------|---------------------------------------|---------------------------------|
| Bus messages             | Routing + transport           | Channel/event bus                     | Subscriptions, RPC waits        |
| Coordination service     | Claims, artifacts, reviews    | SQLite + Ristretto in orchestrator    | coord_query_view, coord_publish_artifact |
| Decision Manifest        | Typed pre-commitment decisions| SQLite + Ristretto in orchestrator    | query_decisions, declare_decision |
| Challenges / validate_work | Peer disagreement + responses | Pipeline-protocol durable log        | Implicit in pipeline state      |
| Consultations            | RPC to knowledge agents       | Ephemeral request/response            | consult_librarian_style, etc.   |
| Tool call events         | Tool start/complete metadata  | Bus, transient                        | Chat panel only                 |
| Pipeline state / DAG state | Task progress, validation epochs | SQLite tables in orchestrator      | Per-feature query methods       |
| Sidecar scribe           | Conversational transcripts    | Per-agent transcript log              | Read indirectly via memory-forest |
| Memory Forest            | Cross-session precedent       | vectorgraphdb                         | tester_forest_get_test_targets, etc. |
| Knowledge graph / document DB | Code/document corpus     | bleve + vectorgraphdb                 | Knowledge-agent consultations   |

Each one works individually. The fabric leaves each one sovereign.

### The principle

**Existing stores own their own data.** Decision Manifest still owns
decisions; coordination service still owns claims/artifacts/reviews;
pipeline-protocol durable log still owns epochs. Their reconciliation,
schemas, locking, and recovery — untouched.

**The fabric is a separate plane.** It captures state changes via emission
chokepoints (in the same `RunInWriteTx` as the source write — no consistency
window) and exposes cross-cutting capabilities none of the stores can have
alone (causal trace, peer awareness, semantic search, contention prediction,
cross-session reasoning chains).

**Failure mode of the fabric = lose cross-cutting lenses.** The sovereign
systems keep working unchanged. The fabric is genuinely additive: turn off
emission and every working system continues.

### The primitive

```go
type AgentActivity struct {
    ID            ActivityID         // ULID; sortable, globally unique
    SessionID     SessionID
    Timestamp     time.Time
    Resolution    Resolution         // Atomic | Fine | Medium | Coarse
    Actor         Actor              // who emitted (agent type, agent id, pipeline id)
    Action        ActionKind         // typed kind (see taxonomy)
    Subject       Subject            // typed coordinate bag (path, target_agent, target_artifact, scope)
    Payload       json.RawMessage    // kind-specific typed payload
    Caused        *ActivityID        // immediate cause; null if root
    Resolves      *ActivityID        // null unless this terminates an in_flight activity
    CausalChain   []ActivityID       // denormalized ancestor list for O(1) walks
    State         ActivityState      // point | in_flight | resolved | cancelled | superseded
    Confidence    Confidence         // hint | tentative | committed | consensus | n/a
    Evidence      []EvidenceRef      // pointers to artifacts, files, decisions, etc.

    // Source-of-truth back-reference (when emitted from a sovereign store)
    SourceTable   string             // empty if not store-emitted
    SourceID      string             // PK in source table

    // Denormalized columns for hot indexed lookups (extracted from Subject)
    SubjectPathPrefix    string
    SubjectTargetAgent   string
    SubjectTargetArtifact string
}
```

### Storage architecture

| Tier              | Job                                          | Activity Fabric uses it for                                                                                                                                |
|-------------------|----------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Ristretto (hot)   | Sub-µs reads of recent state                 | (a) most recent N activities by ID; (b) memoized lens results, invalidated when a new activity of relevant kind appears; (c) Atomic-tier firehose ring buffer |
| SQLite (warm)     | Indexed relational reads, durable writes     | The `agent_activity` table for Medium + Coarse activities; lens implementations push predicates here when they miss Ristretto                              |
| Bleve (full-text) | "find me activities mentioning this string"  | Indexed on payload + evidence + serialized subject; powers `peers.SearchActivity(query)`                                                                  |
| vectorgraphdb     | "semantically similar" + "walk the cause graph" | Embeds activity payloads (Medium/Coarse) at write time; cause/caused are first-class graph edges; powers `peers.SimilarActivity` and `peers.CausalContext` |
| Memory Forest     | Long-term cross-session precedent            | Subscribes to `precedent_emitted` activities (typically promoted from Consensus + accepted artifacts); harvests them into the forest tables                |
| Sidecar scribe    | Conversational verbatim per agent            | Subscribes to `tool_call_*` and `consult_*` activities; correlates with the agent's stream chunks; replaces bespoke `agentPod.FeedScribe` plumbing         |

Each tier sees its slice of the same stream. None of them duplicates the others' jobs.

### Schema (SQLite, on the existing orchestrator BunSQLite)

```sql
CREATE TABLE agent_activity (
    id                       TEXT PRIMARY KEY,        -- ULID
    session_id               TEXT NOT NULL,
    timestamp                INTEGER NOT NULL,        -- epoch ns
    resolution               TEXT NOT NULL,           -- atomic | fine | medium | coarse
    actor_agent_id           TEXT NOT NULL,
    actor_agent_type         TEXT NOT NULL,
    actor_pipeline_id        TEXT,
    action_kind              TEXT NOT NULL,
    subject_json             TEXT NOT NULL,
    payload_json             TEXT,
    caused                   TEXT,                    -- activity ID
    resolves                 TEXT,                    -- activity ID
    causal_chain_json        TEXT,                    -- denormalized ancestor list
    state                    TEXT NOT NULL DEFAULT 'point',
    confidence               TEXT,
    evidence_json            TEXT,
    source_table             TEXT,
    source_id                TEXT,
    -- Denormalized hot-lookup columns
    subject_path_prefix      TEXT,
    subject_target_agent     TEXT,
    subject_target_artifact  TEXT
) STRICT;

CREATE INDEX idx_aa_session_kind_time
    ON agent_activity(session_id, action_kind, timestamp);
CREATE INDEX idx_aa_actor_time
    ON agent_activity(actor_agent_id, timestamp);
CREATE INDEX idx_aa_caused          ON agent_activity(caused);
CREATE INDEX idx_aa_resolves        ON agent_activity(resolves);
CREATE INDEX idx_aa_inflight        ON agent_activity(state) WHERE state = 'in_flight';
CREATE INDEX idx_aa_subject_path    ON agent_activity(session_id, subject_path_prefix);
CREATE INDEX idx_aa_subject_target_agent ON agent_activity(session_id, subject_target_agent);
CREATE INDEX idx_aa_source          ON agent_activity(source_table, source_id);
```

Append-only, write-once. State transitions emit *new* activities (e.g.,
`decision_promoted` linked to the original `decision_declared` via
`Resolves`). This eliminates the entire class of "did this row get mutated
correctly" bugs.

### Write path

```
Sovereign store invokes RunInWriteTx(...)
   │
   ▼
   Inside the transaction:
     ├── source write happens (existing logic, untouched)
     └── fabric.Emit(activity) writes one agent_activity row in the SAME tx
   │
   ▼ (commit succeeds OR both rows roll back)
   Async fan-out to subscribers (channel-based):
     ├── Ristretto put (hot cache)
     ├── Bleve index batch (Medium/Coarse only)
     ├── vectorgraphdb embed + edge add (Medium/Coarse only)
     ├── Memory Forest (only for precedent_emitted)
     └── Sidecar scribe notification (only tool_call_* / consult_*)
```

Hot path cost: one extra SQLite row inside an existing transaction. Async
subscribers never block the source write. Crash recovery: subscribers can
replay from `(session_id, timestamp > last_seen)` because the SQLite row is
the source of truth.

### Read path: lenses

Agents never read `agent_activity` directly. They use **typed lenses** —
thin packages that filter, project, and shape the underlying records into
the answer the agent actually wants. Existing surfaces become lenses; new
cross-cutting lenses become possible.

```go
// All existing surfaces become lenses (subset shown):
manifest.Query(domain, scope) Result               // filters action_kind=decision_*, applies resolution policy
coord.QueryView(taskID) View                       // filters action_kind=claim_*|artifact_*|review_*
challenges.Pending(agentRef) []Challenge           // action_kind=challenge_issued, state=in_flight
consults.HistoryFor(agentRef) []ConsultRoundtrip   // action_kind=consult_*, paired by Resolves edges
tools.RecentCallsFor(agentRef, since) []ToolCall   // action_kind=tool_call_*, scribe-shaped projection

// Cross-cutting lenses that DON'T exist today:
peers.WhatAreTheyDoing(scope, since) []Activity    // any action_kind, any peer in scope, recent
peers.SearchActivity(query, scope) []Activity      // bleve full-text lens
peers.SimilarActivity(activity_id) []Activity      // vectorgraphdb semantic lens
peers.CausalContext(scope) DAG                     // cause/caused walk for "what led to the current state"
peers.ConflictsOpen() []Conflict                   // open challenges + decision incompatibilities
session.Timeline(filter) []Activity                // chronological projection
session.WorkSurfaceFor(path) []Activity            // every activity touching path
session.IngestionContext(scope) IngestionView      // for knowledge agents
session.AmbientFor(agent, scope, time) Envelope    // the ambient context envelope (see Part 2)
```

Lens results are memoized in Ristretto keyed by `(lens_name, query_hash,
last_known_activity_id)`. Each new activity bumps the lens's relevant
last-known-id, so the next call recomputes; intervening calls hit cache.
Sub-µs lens reads for repeated queries.

### Causality graph — the killer cross-cutting feature

`Caused + Resolves + CausalChain` form a DAG of agent work. Today this
graph is implicit and reconstructed by humans reading logs. Make it
explicit and queryable, and:

- **Inspectors audit causality.** "Does this artifact actually trace back
  to the criteria the architect handed off, or did the tester silently
  invent its own scope?" → `peers.CausalContext(artifact_id)` returns the
  chain.
- **Knowledge agents become live-aware.** A consult to the librarian today
  gets a static answer. With the fabric, the librarian can answer "what
  should I know about pipeline 3's recent work in this scope" by traversing
  causal context — using the same primitives it uses for cross-session
  knowledge.
- **Memory Forest learns from causal chains.** Future sessions get to
  query "what's the typical reasoning chain that leads to a successful
  pytest setup for a Python web service?"
- **Chat panel timeline becomes intrinsically coherent.** Renders sub-trees
  of the causal DAG natively; inter-agent rows, tool calls, consultations,
  decisions appear in proper causal nesting because that's what the DAG says.

---

## Part 2 — No-gates, auto-publish, ambient awareness

### The principle

**Decisions are emergent from work, not prerequisites for it.**

The Decision Manifest's broken JIT gate (`requireTestFrameworkDecision`)
violated this. It treated the manifest as a contract the agent had to
satisfy *before* acting. The redesign treats the manifest — and every other
fabric surface — as a **coordination surface the agent populates by
acting** and an **invitation surface the agent uses to collaborate or
challenge**.

This generalizes to a constitutional rule: **no skill in the codebase may
inspect fabric state as a precondition for executing its primary work.**
CI lints enforce this at the package level.

### The unified pattern that applies to every system

Every existing skill across the whole codebase falls into one of five
execution shapes. The fabric maps each to a fixed projection contract.

| Skill shape   | Examples                                                      | Auto-emits                            | Confidence | Gates? |
|---------------|---------------------------------------------------------------|---------------------------------------|-----------|--------|
| Discovery     | discover_project_tools, component_search, detect_test_harness, librarian's find_pattern, archivalist's query_graph | typed *observation* projection | Hint      | Never  |
| Planning      | plan_tests, architect plan-creation steps, orchestrator DAG ingestion, define_criteria pre-write | typed *intent* projection      | Tentative | Never  |
| Mutation      | write_test, write_pipeline_file, format, lint, component_create, coord_publish_artifact, commit_to_disk, claim_scope | typed *commitment* projection (promotes prior Tentative if equivalent) | Committed | Never  |
| Acceptance    | finalize_pipeline, finalize_global_review, accept_checkpoint, plan-execute completion, validate_work success | promotes Committed → Consensus | Consensus | Never  |
| Consumption   | query_decisions, coord_query_view, knowledge-agent consults, validate_criteria, grade_task_quality, awareness skills | *queries* — emit a lightweight `consulted` activity for causal traceability | n/a       | Never  |

**No system has a precondition skill.** No skill ever blocks because of
fabric state. Every emission is fire-and-forget at the storage layer.

### Per-system auto-publish map

#### Pipeline tester

| Existing skill        | What it already infers                              | Auto-declares as                                                                                |
|-----------------------|-----------------------------------------------------|-------------------------------------------------------------------------------------------------|
| detect_test_harness   | harness.FrameworkID, harness.RecommendedOutputs    | `test_framework`, `test_layout` at `{language, path}`, Tentative                                |
| plan_tests            | structured plan with fixture pattern, mock usage   | `fixture_strategy`, `mock_library` at planned-output scope, Tentative                           |
| write_test            | the file actually got written                       | promotes existing `test_framework` declaration to Committed                                     |
| finalize_pipeline     | the verification artifact ratifies the framework   | promotes to Consensus when artifact accepts (or surfaces failure on divergence)                 |

No gate. `write_test` always succeeds. The manifest just gets richer as the
tester does its job.

#### Engineer

| Existing skill            | What it already infers                              | Auto-declares as                                                                          |
|---------------------------|-----------------------------------------------------|-------------------------------------------------------------------------------------------|
| discover_project_tools    | existing build backend, package manager, type system | `build_backend`, `package_manager`, `type_system` at `{language, path}`, Hint            |
| discover_code_patterns    | module layout and import strategy                   | `module_layout`, `import_strategy` at `{path}`, Hint                                      |
| format (when first applied) | the formatter that ran                             | `code_style` at `{language, path}`, Committed                                             |
| lint                      | the linter backend invoked                          | `linter_backend`, Committed                                                                |
| write_pipeline_file       | file's location encodes module layout               | promotes any prior `module_layout` Hint to Committed                                       |
| handoff/finalize          | implementation artifact ratifies all of the above   | promotes to Consensus at acceptance                                                        |

#### Designer

| Existing skill              | Auto-declares as                                                                       |
|-----------------------------|----------------------------------------------------------------------------------------|
| component_search            | discovered `ui_framework`, `component_library` at `{path}`, Hint                       |
| component_create            | `ui_framework`, `state_management`, `design_token_source`, `component_structure` at `{path}`, Committed |
| token_validate / token_suggest | confirms or promotes `design_token_source`                                          |
| a11y_audit / contrast_check | `accessibility_baseline`, Committed                                                     |

#### Pipeline inspector

| Existing skill        | Auto-declares as                                                                            |
|-----------------------|--------------------------------------------------------------------------------------------|
| define_criteria       | `validation_strategy`, `acceptance_criteria_format` at `{task scope}`, Committed (or Consensus if architect-chartered) |
| validate_criteria     | (consumes — queries `test_framework`, `module_layout`, etc., to check that what was built matches what was declared) |
| grade_task_quality    | (consumes — checks for unresolved Tentative decisions blocking acceptance)                  |

Inspector also gets one new audit-time skill: **`inspect_open_activity(scope)`**
(generalizes the original `inspect_decision_conflicts`). Returns *all*
in-flight activity in scope: open challenges, hot scopes, conflicting tools
running in parallel, knowledge-agent advisories, validation holds.

#### Architect

| Existing skill        | Auto-declares as                                                                            |
|-----------------------|--------------------------------------------------------------------------------------------|
| propose_plan          | Hint at `{project_scope, plan_id}`                                                          |
| commit_plan           | Tentative becomes Committed at plan acceptance                                               |
| recoverStalledPlan    | emits `recovery_attempted` activity (operational signal)                                    |
| Plan acceptance       | auto-publishes `charter_ratified` activity carrying high-level decisions inside the plan as Consensus-confidence Charter entries — pipeline agents see Charter in ambient context with elevated weight, no need for a separate Charter table |

#### Orchestrator

| Existing skill           | Auto-declares as                                                            |
|--------------------------|----------------------------------------------------------------------------|
| ingest_plan              | `dag_ingested` activity at Tentative                                        |
| execute_dag (per layer)  | `layer_dispatched` Committed                                                |
| DAG completion           | `dag_accepted` Consensus                                                    |
| coord_* skills           | emit projections automatically (claim_acquired, artifact_published, review_requested) — coord internals stay sovereign; projections emit in the same RunInWriteTx as the coord write |
| Validation epoch transitions | `validation_*` projections                                              |
| Hold acquire/release     | `hold_*` projections                                                        |
| Remediation case create/resolve | `remediation_*` projections                                          |

#### Global tester / global inspector

`challenge_global_tester`, `accept_checkpoint`, `commit_to_disk`,
`finalize_global_review` all emit projections at the right confidence per
the table above. Global inspector's audit role mirrors pipeline inspector's,
scoped to whole-session activity.

#### Librarian / Academic / Archivalist (knowledge agents)

This is where "fundamentally aware" gets the most leverage:

- **Emit advisory projections.** Every consult response auto-publishes an
  `advisory_emitted` activity scoped to the requester's task with the
  substance of the advice. Peer pipelines tackling adjacent work see the
  advisory in their ambient context — they didn't have to consult, the
  librarian's wisdom is now *ambient*.
- **Subscribe for proactive notification.** Knowledge agents tail the
  fabric. When they observe a pipeline agent operating in a scope that
  matches a known precedent (success or anti-pattern), they push a
  `proactive_advisory` activity — which surfaces in that pipeline agent's
  next tool-call response envelope as ambient context.
- **Become routers, not just oracles.** A consult response can include
  *both* static knowledge *and* a routing hint pointing at peer specialists
  actively working in adjacent scope ("tester-pipeline-2 has been working
  on this exact scope for 3 minutes; consider consulting them too").
- **Consult interfaces unchanged** — same `consult_librarian_style` etc.
  Pipeline agents don't have to know the librarian became smarter.

#### Guardian

Every command-approval decision auto-publishes `command_approved` or
`command_denied` with rationale + scope. Peers about to run similar commands
see the denial proactively in ambient context — "guardian denied a similar
command in this scope 12s ago because of <reason>" — and can adapt without
re-tripping the guardian.

#### Guide

Routing classifications auto-publish `route_classified` activities (low
signal but useful for cross-pipeline causal trace). Steering actions
auto-publish `steering_emitted`.

### The five awareness vectors

The user's requirement is that awareness is baked in at multiple levels.
One mechanism isn't enough; we layer:

#### Vector 1 — System prompt (uniform across every agent)

One short section, identical structure for every agent type:

```
You live in a shared fabric with peer agents working in parallel.
Their work is visible to you; your work is visible to them. The
fabric is never a precondition — it cannot block what you do — but
ignoring it is how parallel pipelines silently diverge.

Awareness arrives in three ways:
  • Ambient context on every tool result shows recent peer activity,
    open conflicts, and advisories in your scope. Read it.
  • query_peer_activity / causal_trace / find_related_activity /
    inspect_open_conflicts let you dig actively when ambient
    context surfaces something you need to understand.
  • Knowledge agents (librarian, academic, archivalist) push
    proactive advisories when your scope matches known patterns or
    anti-patterns. Treat these as evidence, not commands.

Your peers in other pipelines are addressable, not just visible. When
ambient context shows a peer working in adjacent or overlapping scope,
you can:
  • consult_peer(pipeline_id=…) — ask them how they're handling
    something, request their evidence on a shared concern.
  • challenge_peer(activity_id=…) — dispute a specific commitment of
    theirs with concrete evidence. They will defend, yield,
    scope-split, or escalate.

Your responsibilities:
  • Collaborate. When peer activity in your scope is compatible
    with your task, adopt it. Adoption is cheap; divergence has
    integration cost.
  • Challenge. When you genuinely disagree with a peer's commitment,
    use challenge_peer against the activity's author. Carry the
    activity_id and your concrete evidence. Don't go silent and
    diverge.

Your routine work auto-publishes typed projections to the fabric as
side effects of the skills you already use. You don't broadcast
separately. The fabric simply gets richer as you do your job.
```

Inspector adds one audit clause; knowledge agents add one advisory clause.
The uniformity is the point — every agent reasons about the fabric the same
way.

#### Vector 2 — Skill descriptions carry the auto-publish contract

Every skill that auto-emits has a one-line note in its description:
*"Auto-publishes `test_framework` Tentative when called with a detected
harness"*. The LLM sees this in the tool list and learns the model
declaratively, without prose teaching.

#### Vector 3 — Ambient context envelope on every tool result

The most powerful mechanism. Every tool result returned to the agent
includes a small, bounded `<ambient_context>` block:

```
<ambient_context>
  scope: services/billing/tests/
  in_flight_activities: 2
    • tester-pipeline-3 challenged your test_framework choice 18s ago
      (activity_id=ch_71f2, evidence: sandbox-pytest-incompatibility)
    • coord scope tests/auth/ has 3 active challenges in 8 minutes
      (hotness=high)
  recent_peer_commitments: 1
    • engineer-pipeline-2 committed module_layout=src-layout 2m ago
      (compatible: yes)
  inbound_disputes: 1
    • engineer-pipeline-3 challenges your build_backend=poetry choice
      (activity_id=ch_88f1, target=dec_44a2, evidence: pyproject.toml
       already declares hatchling at root, your scope is a child)
  inbound_consults: 1
    • tester-pipeline-2 asks: "how are you handling fixtures for shared
      models in services/billing/?" (consult_id=co_22a1, deadline=180s)
  outbound_pending:
    • your consult to designer-pipeline-1 from 40s ago (no response yet)
  advisories: 1
    • librarian: this scope matches precedent "pytest-fixtures-pattern-A"
      (proactive, activity_id=adv_22c1)
</ambient_context>
```

The agent doesn't need to query. The fabric surfaces what's relevant *with
the response*. Computed by one differential lens (`session.AmbientFor`),
capped in size, ordered by `relevance × recency × hotness`. Sub-µs in the
common case (memoized).

#### Vector 4 — Active awareness skills (pulled)

Four uniform skills replace the per-domain query proliferation:

- `query_peer_activity(scope, kinds, since)` — "what have peer pipelines been doing in this scope recently"
- `causal_trace(activity_id)` — "what led to this"
- `find_related_activity(query)` — full-text + semantic search across the activity stream
- `inspect_open_conflicts(scope)` — "what's contested right now in my area"

Domain-specific convenience lenses (`query_decisions`, `coord_query_view`,
etc.) stay as ergonomic shortcuts.

#### Vector 5 — Proactive notifications (pushed)

Knowledge agents and the inspector can publish `notification_emitted`
activities targeted at a specific peer + scope. The fabric promotes those
into the target's next ambient_context envelope automatically. This is how
knowledge agents go from "external advisors" to "informed participants."

### Generalized dispute augmentation

Every dispute/coordination primitive gains the same optional fabric-context
fields:

| Primitive             | Existing fields                              | Adds                                                          |
|-----------------------|----------------------------------------------|---------------------------------------------------------------|
| challenge_agent       | reason, request, required_output, references | `targeting_activity_id`, `target_kind`                        |
| validate_work         | verdict, response                            | `activity_resolution` ∈ {defend, yield, scope-split, escalate, irrelevant} |
| request_correction    | criteria refs                                | `targeting_activity_ids[]`                                    |
| request_override (architect escalation) | rationale                       | `contested_activity_ids[]`                                    |
| consult_*             | query, scope                                 | `causal_context_activity_ids[]` (knowledge agents see *why* you're asking) |
| coord_request_review  | artifact_id                                  | `causal_context_activity_ids[]`                               |

Disputes become structured negotiation about typed activities — never vague
back-and-forth. The challenged agent reads the activity, examines its
causal chain, decides to defend / yield / scope-split / escalate.

### Gates that disappear (explicit kill-list)

| Gate                                | Location                                                                | Disposition                                                                 |
|-------------------------------------|-------------------------------------------------------------------------|-----------------------------------------------------------------------------|
| `requireTestFrameworkDecision`      | `agents/tester/pipeline/decision_manifest_gate.go`                      | **Delete entirely.** This is the inciting bug.                              |
| `write_test` precheck against manifest | tester pipeline tool loop                                            | **Delete.** Auto-publish on success replaces it.                            |
| Tester prompt's "query first / declare second / write third" ritual | `prompts/tester/pipeline_task_system.md` | **Replace** with the ambient/collaborate/challenge teaching.        |
| Future temptation: "engineer must check build_backend before write_pipeline_file" | (does not exist yet) | **Pre-empt.** Documented anti-pattern.                            |
| Future temptation: "any skill must check ambient_context before acting" | (does not exist) | **Pre-empt.** Ambient context is *informative*, never *prescriptive*.       |

The constitutional rule: **no skill in the codebase may inspect fabric state
as a precondition for executing its primary work.** CI lints enforce this.

---

## Part 3 — Cross-pipeline collaboration

### The principle

Agents in concurrent pipelines should be able to **see, consult, and
challenge** each other's work. The same primitives that let an agent see
peer activity should let it act on that visibility — and there's no good
reason for the action surface to stop at pipeline boundaries when the
awareness surface doesn't.

### Two unified primitives

**`consult_peer(target, scope, query, attachments[])`** — generalizes
today's knowledge-agent consults and same-pipeline asides. `target` is an
agent address: `(agent_type, pipeline_id?, agent_id?)`. Without
`pipeline_id`, routes to the natural same-pipeline peer or knowledge agent
(today's behavior). With a foreign `pipeline_id`, routes cross-pipeline.
Knowledge agents are just one valid target type.

**`challenge_peer(target_activity_id, evidence, alternative, resolution_hint)`**
— generalizes today's `challenge_agent`. Targets *a fabric activity*, not
an agent directly. The fabric resolves the activity's author and pipeline.
Same-pipeline target collapses to today's deterministic protocol behavior;
cross-pipeline target engages the new path.

Targeting an *activity* rather than an agent is the design hinge. Disputes
are anchored to a concrete claim, decision, artifact, or commitment — never
vague — and the addressing works uniformly whether the target is one row
over or three pipelines away.

### Delivery: ambient, asynchronous, never interrupting

Cross-pipeline challenges and consults are themselves fabric activities
(`challenge_emitted`, `consult_emitted`) with a `target_agent_address`
denormalized column. The target's ambient context envelope surfaces them in
its next tool-result response (see Vector 3 above).

Critical properties:

- **The target is never preempted.** It finishes its current tool call and
  reads the inbound items on its next turn. No cross-pipeline call
  interrupts work in progress.
- **Asynchronous resolution.** Target responds (or doesn't) by emitting
  `challenge_response` / `consult_response` activities. Initiator sees the
  response in *their* next envelope. Both pipelines proceed independently
  between exchanges.
- **Durable.** Activities are append-only on SQLite. If a target agent is
  mid-turn or dormant when the inbound activity is emitted, it's still
  there when they next read ambient context. No lost messages.
- **No new transport.** The ambient-context envelope mechanism is the same
  delivery channel; cross-pipeline items are extra rows in a slice the
  agent already reads.

### Liveness — when the target is dormant

Sylk's activation system has Cold/Cool/Warm/Hot tiers. Cross-pipeline
addressing handles dormant targets without thrash:

- **Hot/Warm target**: inbound activity surfaces in next natural turn. No
  extra promotion.
- **Cool target**: inbound activity stays durable; target sees it when next
  promoted by its own pipeline's natural cadence. No automatic wake.
- **Cold target**: same as Cool. No wake.
- **Override**: only the inspector (at finalize/audit time) and the
  orchestrator (at DAG step boundaries) can request promotion-with-context
  for stuck cross-pipeline disputes — and only when the dispute is on the
  audit/grade-blocking path.
- **Stale handling**: each cross-pipeline activity carries an optional
  `deadline_at`. Past the deadline, the fabric emits a `consult_unanswered`
  or `challenge_unresolved` activity. Inspector picks these up at audit;
  initiator gets it in their envelope and can fall back.

This intentionally trades latency for two safety properties: cross-pipeline
traffic can never starve a pipeline's primary work, and foreign pipelines
can never blow up another pipeline's resource envelope.

### Resolution semantics — four outcomes for challenges

| Resolution     | Meaning                                                                  | Side effect                                                                                                                                    |
|----------------|--------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------|
| Yield + adopt  | "Your evidence is decisive; I supersede my activity with yours."        | Target emits a `superseded_by` link; manifest reconciliation handles promotion automatically. Both pipelines align on next read.              |
| Defend         | "I have counter-evidence; my activity stands."                          | Target emits `challenge_response{resolution=defend, counter_evidence=...}`. Activity unchanged. Dispute remains open; visible to inspector.   |
| Scope-split    | "We're both right, just at different scopes."                           | Target emits `scope_partition` carrying narrowed scopes for both pipelines. Manifest emits two narrowed activities replacing the broad one.    |
| Escalate       | "This needs adjudication beyond us."                                    | Target emits `escalation_requested` to architect (or human). Architect sees the dispute in their ambient context. Inspector tracks resolution. |

Cross-pipeline consult responses are simpler: `respond` (with content) or
`decline` (with reason). Decline is acceptable — the structured form of
"I can't help right now," strictly better than silence.

### Throttling — three layers

Without backpressure, ten pipelines could fire ten challenges each per turn
at one specialist. The fabric's existing **hotness lens** prevents pile-on:

1. **Pre-emit advice** (initiator side). When ambient context shows a
   scope is hot, envelope adds: "consider adopting an existing thread or
   waiting for resolution." Soft guidance, not a gate.
2. **Per-target inbound cap** (delivery side). Each agent's ambient
   envelope surfaces at most N inbound challenges + M inbound consults per
   turn (config defaults: 3, 5). Excess spills to an `overflow_queue`
   activity surfaced to the inspector — pile-on becomes an audit signal.
3. **Per-initiator outbound cap.** A single agent can have at most K
   outstanding cross-pipeline asks (defaults: 3 challenges, 5 consults).
   Beyond the cap, the skill returns a soft refusal — *not a precondition
   gate*: it's a flow-control gate on the cross-pipeline action itself,
   not on the agent's primary work. The constitutional rule (no skill
   blocks primary work) is preserved because primary work continues
   regardless of cross-pipeline queue depth.

### Inspector becomes the cross-pipeline audit role

`inspect_open_activity(scope)` returns cross-pipeline state as first-class:

- Open inbound challenges in scope at finalize time → blocking quality issue.
- Stale cross-pipeline consults (unanswered past deadline) → may indicate
  unaware target; inspector can use `request_promotion_with_context`.
- Resolved disputes with `defend` → visible quality signal — inspector
  decides accept-divergence or escalate-to-architect.
- Scope-splits → audit confirms partition is coherent.
- Cross-pipeline consult responses → recorded in causal trail; inspector
  verifies work properly incorporated peer guidance.

### Knowledge agents as routers + oracles

When a knowledge agent receives a `consult_peer(target=librarian, scope=…)`,
it can respond with both static knowledge *and* a routing hint pointing at
peer specialists actively working in adjacent scope:

```
librarian response:
  static_knowledge: "pytest-fixtures pattern A is the recommended approach for…"
  active_peers:
    • tester-pipeline-2 has been working on this exact scope for 3 minutes;
      committed to fixtures-pattern-A; consider consulting them for live state
    • engineer-pipeline-1 declared module_layout=src-layout in adjacent scope;
      relevant if you're sharing fixture imports
  proactive_advisories_to_emit:
    • notify tester-pipeline-3 (also working on fixtures): "consider this thread"
```

Librarian evolves from "answer the question" to "answer the question +
connect you to the peers who are answering it in the live system."

### What collapses (special cases become uniform routing)

| Today's special case                                | After cross-pipeline unification                                                    |
|-----------------------------------------------------|------------------------------------------------------------------------------------|
| `challenge_agent` (same-pipeline only)              | Routing case of `challenge_peer` where target activity author is same-pipeline. Same code path, same protocol behavior. |
| `consult_librarian_style` etc.                      | Routing case of `consult_peer` where target is a knowledge agent. Same code path. |
| Architect escalation (special bus path)             | `consult_peer(architect, …)` or `challenge_peer(charter_activity_id, …)`. Architect's ambient context surfaces escalations like any other inbound. |
| `coord_request_review` (orchestrator-routed)        | Special case of `consult_peer(reviewer_target, scope, "review this", attachments=[artifact_activity_id])`. Coord internals stay sovereign. |
| Cross-pipeline coordination via manifest only       | Manifest auto-publishes (existing); cross-pipeline challenges target manifest activities directly; converges naturally. |

We don't delete existing implementations — they stay as the deterministic
same-pipeline / same-system code paths. Cross-pipeline is the *new* case
the unified primitives handle. The skill surface narrows; the capability
widens.

---

## Part 4 — Chokepoint instrumentation for total granular capture

### The principle

The original FABRIC.md design captured every action by forcing every
operation through one substrate. We've kept sovereign systems intact —
which means losing that totalism unless we instrument differently.

The fix: **chokepoint instrumentation.** Sylk's codebase already funnels
every action through ~10 well-known abstractions. Instrument each one. CI
lints enforce that no new code escapes capture. Result: brute-force
totalism without central storage.

### The chokepoints

| Chokepoint                            | What flows through it                                  | Today                          |
|---------------------------------------|--------------------------------------------------------|--------------------------------|
| `database.RunInWriteTx`               | Every persistent state mutation across every store     | Wraps writes; doesn't emit     |
| `core/events.ChannelBus.Publish`      | Every cross-agent message                              | Routes; doesn't emit           |
| `providers.ProviderAdapter.Generate`/`Stream` | Every LLM round-trip across providers          | Calls provider; doesn't emit   |
| `versioning.FileAccess` (Read/Write/Delete) | Every workspace file operation                  | Mediates; doesn't emit         |
| `purevfs.ExecutionBroker.Run`         | Every command execution under strict-disk              | Brokers; doesn't emit          |
| `agents/shared/tool_publish.go`       | Every tool invocation start/complete                   | Publishes to bus; doesn't durably emit |
| Activation tier transitions           | Every Cold↔Cool↔Warm↔Hot promotion/demotion          | Logs; doesn't emit             |
| Stream event emission                 | Every LLM streaming event                              | Drives UI; doesn't durably emit |
| `commandapproval.Authorize`           | Every guardian decision                                | Returns verdict; doesn't emit  |
| Tracked goroutines                    | Every long-lived goroutine                             | Tracked but not in fabric      |

Each one is the *only* way that class of action can happen in sylk. Wire
each to emit a typed activity and you have universal capture by
construction.

### The instrumentation pattern

```go
// Before:
func RunInWriteTx(ctx context.Context, fn func(tx *bun.Tx) error) error {
    // existing logic
}

// After:
func RunInWriteTx(ctx context.Context, fn func(tx *bun.Tx) error) error {
    span := fabric.StartSpan(ctx, fabric.ActionStoreWriteCommitted, payload)
    defer span.End()
    // existing logic — unchanged
}
```

Same shape for every chokepoint. The span carries FabricContext from
`context.Context`, so causal linking is automatic. Each chokepoint emission
records: ActionKind, timing, success/error, resolved scope, FabricContext,
and per-chokepoint typed payload.

### Resolution tiers — totalism without storage explosion

Every activity carries a `resolution` field that determines storage and
lifetime:

| Resolution | Examples                                                                  | Storage destination                                          | Retention                  |
|------------|---------------------------------------------------------------------------|--------------------------------------------------------------|----------------------------|
| **Atomic** | LLM chunk received, cache hit, bus message in-flight, file read, broker stdout byte-batch | Ristretto only (in-memory ring buffer per session) | Bounded — last N seconds or N MB per session |
| **Fine**   | Tool call complete, file write, command exec complete, guardian decision, bus delivery complete, cache miss with fetch | Ristretto + ephemeral SQLite table partitioned by hour | 24h then drop |
| **Medium** | Skill invocation complete, LLM round-trip complete, bus topic publish, store write transaction | SQLite (`agent_activity` durable) + Ristretto + Bleve | Session lifetime + harvest to Memory Forest |
| **Coarse** | Decision declared, challenge issued, plan ratified, validation accepted, charter narrowed | SQLite + Ristretto + Bleve + vectorgraphdb embed + Memory Forest precedent candidate | Permanent (subject to forest gardening) |

Resolution is set by the chokepoint based on ActionKind — not by callers.
Small, fixed table mapping ActionKind → Resolution.

Lenses query against resolution-appropriate tiers:
- "What's happened in the last 5 seconds?" → reads Atomic + Fine from Ristretto, sub-µs
- "What's happened in this session?" → reads Medium + Coarse from SQLite, indexed
- "What patterns appear across sessions?" → reads Coarse from Memory Forest

### CI lints — the totalism contract

| Lint                       | What it forbids                                                                                            | Why                                                              |
|----------------------------|------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------|
| `nodirectdbwrite`          | `db.Exec`, `db.NewInsert`, `tx.NewUpdate` outside `database.RunInWriteTx`                                 | Every write must go through the chokepoint                       |
| `nodirectbuspub`           | Raw bus push outside `events.Publisher.Publish` (the fabric-emitting wrapper)                             | Every message must traverse the instrumented chokepoint          |
| `nodirectprovidercall`     | `anthropic.Generate`, `google.Generate`, etc. outside `providers.ProviderAdapter`                         | Every LLM call must route through the wrapper                    |
| `nodirectfileaccess`       | `os.Open`, `os.WriteFile`, etc. outside `versioning.FileAccess` (with allowlist for boundary code)        | Every file op must route through the wrapper                     |
| `noskillwithoutactionkind` | A skill registration without an associated `ActionKind` constant                                          | Every new skill must declare its emission contract               |
| `notrackedgoroutine`       | Goroutine spawn outside the registered-goroutine helpers                                                  | Already a project rule; tighten via lint                         |
| `nofabricprecondition`     | A skill execution path that calls `fabric.Query*` before its primary work                                 | Constitutional rule — no gates                                   |

The user's existing `core/ci/analyzers/nodirectexec/` is the template.
Same `analysistest.Run` / allowlist pattern works for each new lint.

### What this captures that was previously missed

- **Every cross-agent message.** Routing decisions, reroutes, failed
  deliveries, queue overflows — inspectable from data, not logs.
- **Every LLM round-trip.** Token usage, retries, model swaps. Anthropic
  529 retries diagnosable via one fabric query.
- **Every file operation.** Provenance becomes mechanical: causal_trace
  shows the chain of skills that pulled a file.
- **Every command execution.** Argv, exit code, duration, scope, causal
  antecedent. Strict-disk broker mandate becomes mechanically auditable.
- **Every cache event.** Performance hotspots become differential queries.
- **Every activation tier transition.** Demotion-related bugs become
  one-query diagnostics.
- **Every guardian decision.** Post-hoc query "every denial in this
  session, why, what was the agent trying to do."
- **Every error.** Errors become a queryable surface, not log-only.
- **Every stream event.** Cross-pipeline observability of agent state in a
  single lens read.
- **Every goroutine lifecycle.** Untracked goroutines automatically
  detectable.

### Novel capabilities at this granularity

- **Exact replay.** Atomic + Fine resolutions capture every chunk, every
  cache event, every bus delivery in order. Replay is exact, not
  approximate.
- **Cost attribution.** Token usage tied to causal chains. "This request
  cost 47K tokens, broken down by agent, by skill, by retry."
- **Performance hotspot detection.** Differential lens computes per-skill
  p50/p99 conditional on causal context.
- **Cache-hit correlation.** "Were the decisions in this session made on
  cache-hot or cache-cold data?"
- **Causal anomaly detection.** Memory Forest learns successful causal
  chain shapes; current divergence flags for inspector.
- **Network introspection.** "Show me every Publish to topic X with
  target_subs=0." That class of bug stops being detective work.
- **File-access provenance.** Mechanical answer to "who clobbered my file?"
- **LLM behavior analysis.** Every retry, every model swap queryable.
- **Real-time anomaly streams.** "Wake me when X happens" for arbitrary X.
- **Bitemporal queries with depth.** "As of activity X, what did pipeline
  3 see in its ambient context?" Inspector audits become forensic-quality.

---

## Implementation tier list

Each tier independently shippable, each delivers value before the next
starts, no tier breaks any sovereign system.

### Tier 0 — Delete the broken JIT gate

- Delete `requireTestFrameworkDecision` and its `write_test` precheck call site
- Strip the gate-related prompt section from `prompts/tester/pipeline_task_system.md`
- The race condition disappears immediately; manifest is purely additive

### Tier 1 — Foundation

- `core/activity/` package: typed primitive, ActionKind taxonomy
  (semantic + infrastructural), Resolution tiers, FabricContext, append
  API, lens interface, evidence types
- `agent_activity` table on orchestrator BunSQLite (migration)
- Hot Ristretto cache + lens-result memoization
- Subscriber registration (channel-based)
- Dual-tier storage: Ristretto bounded ring buffer for Atomic/Fine; SQLite
  for Medium/Coarse
- One end-to-end smoke test

### Tier 1.5 — Universal chokepoint instrumentation

- `core/activity/span.go`: `StartSpan(ctx, ActionKind, payload) Span` /
  `Span.End()` / `Span.EndWithError(err)`. Reads FabricContext from `ctx`.
- ActionKind taxonomy expanded for infrastructural kinds
- Wrap each chokepoint with span:
  - `database.RunInWriteTx`
  - `events.ChannelBus.Publish` / `Deliver`
  - `providers.ProviderAdapter.Generate` / `Stream`
  - `versioning.FileAccess.Read` / `Write` / `Delete`
  - `purevfs.ExecutionBroker.Run`
  - `commandapproval.Authorize`
  - Activation tier helpers
  - Tracked-goroutine helpers
  - Tool-publish (`agents/shared/tool_publish.go`)
  - Error wrapping helpers

### Tier 1.6 — CI lints

- `nodirectdbwrite`, `nodirectbuspub`, `nodirectprovidercall`,
  `nodirectfileaccess` analyzers
- `noskillwithoutactionkind` analyzer
- `nofabricprecondition` analyzer
- All wired into the existing analyzer test harness

### Tier 2 — Per-system amplifiers (additive, any order)

For each sovereign store, a small PR adds one emission point inside its
existing `RunInWriteTx` plus exposes one new lens:

- Manifest amplifier → `ManifestWithCausalContext` lens
- Coord amplifier → `ScopeHotness` and `ArtifactLineage` lenses
- Pipeline-protocol amplifier → `ProtocolPatternMatch` lens
- Validation/holds/remediation amplifier → `RemediationPriors` lens
- Sidecar scribe amplifier → `TranscriptSearch` lens

Each amplifier is reversible by removing one emit call and one lens
registration.

### Tier 3 — FabricContext propagation

- `WithFabricContext` / `FabricContextFromContext` helpers (pattern from
  `versioning.SessionIDFromContext`)
- Propagation through bus messages (header field)
- Propagation through skill calls
- Causal DAG builds itself

### Tier 4 — Auto-publish on existing skills

- Tester: `detect_test_harness`, `plan_tests`, `write_test`,
  `finalize_pipeline`
- Engineer: `discover_project_tools`, `discover_code_patterns`, `format`,
  `lint`, `write_pipeline_file`, handoff
- Designer: `component_search`, `component_create`,
  `token_validate`/`a11y_audit`
- Inspector: `define_criteria`, `validate_criteria`, `grade_task_quality`
- Architect: `propose_plan`, `commit_plan`, plan acceptance
  (`charter_ratified`)
- Orchestrator: `ingest_plan`, `execute_dag`, coord skills
- Knowledge agents: every consult response auto-publishes
  `advisory_emitted`
- Guardian: `command_approved` / `command_denied`
- Guide: `route_classified` / `steering_emitted`

### Tier 5 — Ambient context envelope

- `session.AmbientFor(agent, scope, time)` lens
- Wire envelope into every agent's tool-result path
- Bounded by per-target/per-initiator caps

### Tier 6 — Generalized dispute augmentation

- `challenge_agent` → `challenge_peer` (keep skill name; same-pipeline
  behavior unchanged; add cross-pipeline routing case)
- Knowledge-agent consults → `consult_peer`
- Cross-pipeline routing through the bus
- Resolution semantics in pipeline-protocol (defend / yield / scope-split /
  escalate)
- Throttling — three-layer caps

### Tier 7 — Uniform prompt section

- The "you live in a fabric" section added to every agent's system prompt
- Inspector + knowledge-agent extra clauses

### Tier 8 — Knowledge-agent integration

- Librarian/academic/archivalist subscribe to fabric for live awareness
- Their consult-response shape gains `active_peers` and
  `proactive_advisories_to_emit` fields
- Proactive notification via `notification_emitted` activities

### Tier 9 — Inspector audit upgrade

- `inspect_open_activity(scope)` skill on inspector (generalizes
  `inspect_decision_conflicts` to all activity kinds)
- Inspector prompt teaches the audit responsibility

### Tier 10 — Cross-cutting indexers

- Bleve subscriber: indexes Medium/Coarse activities for full-text search
- vectorgraphdb subscriber: embeds Medium/Coarse activities for semantic +
  graph queries
- `find_related_activity` and `peers.SimilarActivity` lenses light up

### Tier 11 — Memory Forest harvest

- Subscriber for `precedent_emitted` + entire causal chains terminating in
  successful task acceptance
- Future sessions ask "show me the reasoning chain of past sessions that
  solved problems like this"
- Cross-session collaboration shapes harvested as precedent type

### Tier 12 — Advanced lenses (ongoing)

- Bitemporal queries
- Counterfactual replay (dev tooling)
- Contention prediction
- TUI Decisions / Causality panels

---

## Key design properties

**Maximally correct.** One write path, write-once records, monotonic IDs,
immutable history. Cross-store consistency is impossible to violate because
sovereign stores own their own data; the fabric is a derived projection
emitted in the same transaction.

**Robust.** Ristretto rebuilds from SQLite on restart; lenses are pure
functions over the table; subscribers can crash and replay from
`(session_id, timestamp > last_seen)`. Inherits the SQLite WAL +
busy_timeout + writeMu retries shipped in the recent SQLite-locked fix.
No coordinator goroutines (Ristretto TTL handles in-flight expiry).

**Performant.** Hot path is one extra SQLite row inside an existing
transaction + one Ristretto put. Lens reads memoized at the
`(lens, query, last_activity_seen)` level; repeated reads sub-µs. Indexed
cold reads on `(session_id, action_kind, timestamp)` and denormalized
columns are fast even at 100k+ activities per session. Bleve / vectorgraphdb
writes are async and batched; never block the activity write.

**Resource-efficient.** Atomic-tier firehose lives in Ristretto with
bounded retention. Only Medium/Coarse activities reach SQLite. Bleve and
vectorgraphdb already index large corpora — adding the activity stream is
marginal cost. Memory Forest only ingests the precedent subset, so its
size stays bounded.

**Agentic.** Five-vector awareness model means every agent gets fabric
context at multiple resolutions: ambient (passive), active queries
(pulled), proactive notifications (pushed), system-prompt teaching, skill
description annotations. The collaboration model is uniform across all
pipeline agents — query, adopt, or challenge with evidence. Knowledge
agents become live participants. The causal graph makes "why is the system
in this state" answerable by walking edges.

**Net-additive.** The fabric can be turned off (skip emissions, skip the
table, skip lenses) and every working system keeps working unchanged.
That's the safety property given how much correctness work the existing
systems already carry. When it's on, every system becomes more capable
than it could be alone.

---

## End-to-end scenario: three pipelines collaborating

1. **T+0** — User asks for "add billing API tests + a new build pipeline."
   Guide classifies and routes to architect. Routing emits
   `route_classified`.
2. **T+0.5s** — Architect's planner runs. Each plan step emits
   `propose_plan`. Plan acceptance emits `charter_ratified` carrying:
   `test_framework=pytest` (Charter), `build_backend=hatchling` (Charter),
   `module_layout=src-layout` (Charter). All Consensus.
3. **T+1s** — Orchestrator ingests plan. `dag_ingested`. DAG executes.
   Layer 1 dispatches to engineer-pipeline-1 + tester-pipeline-1 +
   designer-pipeline-1 in parallel.
4. **T+2s** — Engineer-pipeline-1's `discover_project_tools` returns.
   Auto-publishes Hint `build_backend=hatchling` at `services/billing/`
   (matches Charter, equivalent — recorded as corroboration).
5. **T+3s** — Tester-pipeline-1's `detect_test_harness` returns.
   Auto-publishes Tentative `test_framework=pytest` at
   `services/billing/tests/`. Equivalent with Charter — promoted toward
   Committed automatically by manifest amplifier's existing reconciliation.
6. **T+4s** — Tester-pipeline-2 starts on `services/api/v2/`. First tool
   response carries `<ambient_context>`: "Charter says pytest at project
   root; Committed pytest at services/billing/tests/." Tester-pipeline-2
   recognizes its v2-sandbox constraint conflicts with pytest.
7. **T+4.2s** — Tester-pipeline-2 issues
   `challenge_peer(charter_activity_id, evidence=sandbox-loader-incompat)`.
   The challenge is itself an activity; its `Caused` field auto-links to
   the ambient_context that surfaced the conflict (FabricContext propagation
   made this free).
8. **T+5.2s** — Concurrently, engineer-pipeline-3 is introducing a new
   sandbox runner for v2. Sees in its own ambient context:
   "tester-pipeline-2 just challenged the Charter on sandbox-loader
   grounds." Engineer-pipeline-3 issues
   `consult_peer(target=tester-pipeline-2, scope=services/api/v2/, query="what's the exact loader-API your alternative needs?")`.
   Cross-pipeline path engaged — engineer talking to tester directly.
9. **T+6s** — Tester-pipeline-2 finishes its tool call, reads ambient
   envelope, sees the inbound consult. Responds with the unittest loader
   API requirements as `consult_response`.
10. **T+6.5s** — Engineer-pipeline-3 sees the response, adapts its sandbox
    runner to accommodate both loaders. Emits `discover_project_tools` Hint
    reflecting dual-loader support. Tester-pipeline-2 sees:
    "engineer-pipeline-3 now supports your loader requirement" — its
    challenge gained stronger evidence.
11. **T+7s** — Architect reads inbound challenge envelope. Sees expanded
    evidence. Issues
    `challenge_response{resolution=scope-split, partition=[pytest@root_except_v2, unittest@v2]}`.
    Manifest emits two narrowed Charter activities.
12. **T+7.5s** — All pipelines see resolution. Tester-pipeline-1 (billing,
    unaffected) keeps pytest. Tester-pipeline-2 (v2) gets official unittest
    commitment. Engineer-pipeline-3 keeps dual-loader runner. Coherence
    achieved through three pipelines collaborating, not one architect
    dictating.
13. **T+8s** — Librarian, tailing the fabric, observes the multi-party
    convergence. Pushes `proactive_advisory` to designer-pipeline-1: "v2
    scope uses unittest fixtures per tester-pipeline-2's commitment."
14. **T+45s** — Pipeline inspector for billing scope finalizes. Calls
    `inspect_open_activity(scope=services/billing/)`. Zero open
    cross-pipeline disputes, zero unanswered consults, all activity
    coherent with narrowed Charter. Accepts.
15. **T+50s** — Memory Forest harvests the full causal graph (charter →
    cross-pipeline challenge → cross-pipeline consult → engineering
    adaptation → architect scope-split → librarian proactive notification →
    coherent multi-pipeline acceptance) as a single high-quality precedent.

Throughout: **zero gates fired.** Every agent did its primary work
uninterrupted. Every action — semantic + infrastructural — was captured at
appropriate resolution. Future sessions inherit this collaboration shape
as precedent.

---

## What ships first (PR sequence)

1. Tier 0 (delete the broken gate) — small, safe, immediate
2. Tier 1 (foundation: `core/activity/`, table, basic store)
3. Tier 1.5 first chokepoint: `database.RunInWriteTx` — proves the pattern
4. Tier 1.6 first lint: `nodirectdbwrite` — locks the pattern in
5. Tier 1.5 remaining chokepoints — bus, provider, file access, broker, guardian
6. Tier 1.6 remaining lints
7. Tier 3 (FabricContext propagation)
8. Tier 2 first amplifier: manifest — proves the lens model
9. Tier 4 (auto-publish on existing skills, agent by agent)
10. Tier 5 (ambient context envelope)
11. Tier 6 (cross-pipeline primitives)
12. Tier 7 (uniform prompt section)
13. Tier 8 (knowledge-agent integration)
14. Tier 9 (inspector audit upgrade)
15. Tier 10 (Bleve + vectorgraphdb subscribers)
16. Tier 11 (Memory Forest harvest)
17. Tier 12 (advanced lenses, ongoing)

Each step independently shippable, reversible, and net-additive to the
codebase.
