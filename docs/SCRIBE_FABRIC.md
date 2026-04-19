# Scribe + Activity Fabric Integration

A complete architecture and implementation plan for integrating sylk's
per-agent scribe sidecars with the Activity Fabric — preserving and
strengthening the scribe's role as **the authoritative biographer for
its specific agent replica** while adding the scribe's voice as **a
typed, queryable, embeddable participant in the fabric's causal graph**.

This document is the single source of truth for the scribe-fabric
integration. It supplements `docs/FABRIC.md` and inherits its design
principles (sovereign systems, no gates, ambient awareness, chokepoint
totalism). See `docs/FABRIC.md` for the broader fabric architecture.

---

## 1. Premise and goals

The scribe is **the only component in sylk whose entire job is the
longitudinal observation of one specific agent.** It must remain:

- **The agent's authoritative biographer.** When ambiguity exists about
  what an agent did, why, in what order, or with what reasoning, the
  scribe's narrative is the canonical answer. This is its inward
  identity — the LLM-for-another-LLM role we're keeping and improving.
- **The system's narrative voice over the causal graph for that agent.**
  Other agents, the inspector, knowledge agents, the chat panel, and
  Memory Forest see the scribe's narrations as typed, queryable fabric
  activities. This is its outward identity — the new role the fabric
  enables.

These are **complementary, not alternative**. One LLM call per batch
produces both: a structured commentary stored through the existing
archivalist + knowledge-mirror path (inward), and a typed
`narration_emitted` fabric activity referencing the same content
(outward). Same content, two consumers.

### Goals

1. **Preserve and strengthen the inward biographer role.** Every agent
   replica gets a dedicated, voice-stable narrative life captured by
   its scribe and persisted across restarts.
2. **Open the scribe's voice to the rest of the system.** Narrations
   become typed fabric activities consumable via ambient context,
   inspector audit, knowledge-agent subscribers, semantic search,
   precedent harvest.
3. **Replace push-based feeding with fabric subscription.** Scribes
   observe the structured activity stream their agent emits, not just
   the agent's final user-facing response.
4. **Keep total observation: most-any agent activity narrates.** The
   scribe sees and narrates the comprehensive Fine+Medium+Coarse stream
   for its parent (Atomic-tier events stay out — too high-volume).
5. **Bound LLM cost via batching, not by skipping activities.** One
   narration per turn-equivalent batch, not one per discrete activity.
6. **Reuse existing persistence, don't re-create it.** The archivalist
   + knowledge-mirror path already persists commentary into the
   document DB and knowledge graph; the scribe-fabric integration adds
   one fabric-side projection alongside, never a parallel storage layer.
7. **Cross-replica continuity becomes a query.** The next replica
   queries its prior lives via the agent's own `recall_my_history`
   skill, hitting the existing archivalist + knowledge surface.

### Non-goals

- Replacing the archivalist's session store, knowledge mirror, or
  knowledge runtime ingestion path. All three stay sovereign.
- Replacing the existing handoff bridge. It remains as a fast-path
  warm-transfer optimization; cold-start replicas inherit via fabric +
  knowledge query, not via the bridge.
- Eliminating the scribe's LLM tool loop. The scribe still runs an
  LLM to produce structured commentary; we just feed it richer input
  and fan its output into one extra surface (the fabric activity).
- Per-event narration. Atomic-tier events (LLM chunks, cache hits,
  per-byte file reads) never trigger an LLM call.

---

## 2. Current state (what already exists)

This section documents what is already in place so the design can build
on it, not duplicate it.

### 2.1 Scribe lifecycle and structure

- **`agents/scribe/scribe.go`** — per-agent-type Scribe struct with
  workstream isolation by `ParentCorrelationID`, single-threaded feed
  loop, bounded buffer (32 deep, drop-on-full), per-workstream TTL
  (15 min), max 64 workstreams (LRU eviction).
- **`agents/shared/agent_pod.go`** — `Scribe` interface
  (`Start`/`Stop`/`Feed`), `ScribeFeed` struct, `AgentPod.startScribes()`
  spawns one scribe per member type during pre-activation.
- **`agents/shared/agent_pod.go:732`** — `agentPod.FeedScribe(agentType,
  userRequest, agentResponse, parentCorrelationID)` is the push API
  every parent agent calls after a turn completes. ~10 callsites across
  agent files.

### 2.2 Scribe processing

- **`agents/scribe/scribe.go:processFeed`** — accumulates feed into
  workstream history (ring-buffered: 50 turn pairs = 100 messages),
  calls `generateCommentary` to produce structured JSON via LLM tool
  loop.
- **`agents/scribe/scribe.go:408`** — `switch parentAgentType` produces
  per-agent system prompt customizations (architect gets plan_delta,
  engineer gets files/tests, etc.). Hardcoded.
- **`agents/scribe/skills.go:165`** — `storeArchivalistSkill` is the
  required terminal tool — every scribe turn calls `store_archivalist`
  exactly once with the structured commentary object.

### 2.3 Storage path (the part we keep and reuse)

- **`agents/scribe/skills.go:228`** — `storeCommentaryInArchivalist`
  publishes a `RouteRequest` over the channel bus to the archivalist's
  store route, fire-and-forget.
- **`agents/archivalist/`** — ingests the commentary as an `Entry` with
  `source_type="scribe"` metadata. Per-session store. Token-threshold
  driven L1→L2 archive flush.
- **`agents/archivalist/knowledge_mirror.go:17`** —
  `mirrorStoredEntryToKnowledge` auto-mirrors scribe-tagged entries
  into the Knowledge Runtime as a markdown document at
  `archivalist/scribe/<agent_type>/<entry_id>.md`. Lands in the
  document DB and the search index; standard knowledge-runtime
  ingestion picks up entities/relationships/embeddings.

This is the existing biographic substrate. **The scribe-fabric
integration does not replace any of it; it adds one fabric-side
projection alongside.**

### 2.4 Handoff bridge

- **`agents/scribe/scribe.go:517`** — `HandoffInjectable` interface;
  `InjectPreparedContext` seeds new scribe with archivalist brief
  extracted from prior instance. Becomes a fast-path warm-transfer in
  the new design; cold restarts inherit via fabric + knowledge query.

---

## 3. The dual-identity model

### 3.1 Inward identity — the biographer

The scribe carries the **cognitive continuity** of one specific agent
replica:

- Every meaningful turn of that agent: tool calls, decisions, retries,
  changes of mind, abandoned approaches, final outputs.
- The agent's reasoning trail as the scribe infers it from the
  structured activity stream — including the things that didn't make
  it into the agent's final user-facing text.
- Voice that's stable for the agent type. Architect-scribe sounds like
  the architect's biographer. Tester-scribe sounds like the tester's.
- Survives across replicas. When a replica is demoted, stopped, and
  later re-promoted (or replaced entirely), the next replica reads its
  prior lives from the archivalist + knowledge runtime via
  `recall_my_history`.

The biographer's content lives in:

- **archivalist `Entry` records** with `source_type="scribe"`,
  `origin_agent_type=<agent>`, `replica_generation=<N>`. The
  per-session store is the durable base; L2 archive flush handles
  long-running sessions.
- **Knowledge runtime documents** (markdown, mirrored from the entries)
  at `archivalist/scribe/<agent>/<entry_id>.md`. The document DB and
  search index pick these up automatically.
- **Knowledge graph** entities and relationships extracted from the
  mirrored markdown by the standard knowledge-runtime ingestion path.

### 3.2 Outward identity — the fabric narrator

The same commentary content emits as a typed fabric activity:

- **`ActionNarrationEmitted` (Coarse resolution)** — visible to:
  - Peer pipelines via ambient context envelope (`AmbientFor` lens).
  - Inspector via `inspect_open_activity` audit.
  - Knowledge agents tailing the fabric for proactive notification
    candidates.
  - Memory Forest via the existing `ForestSubscriber` when the
    narration is precedent-flagged.
  - Bleve subscriber for full-text search.
  - vectorgraphdb subscriber for semantic similarity.
  - The chat panel for native scribe-view rendering.
- **Back-reference to the archivalist entry** via `SourceTable` +
  `SourceID` so consumers can join from the fabric to the canonical
  archivalist document.
- **Causal links** via `Caused` and `CausalChain` populated from the
  causal context the scribe walked while composing the narration.

### 3.3 One LLM call, two surfaces

Both surfaces are populated by the same scribe LLM call. The output
flow is:

```
scribe LLM tool loop completes
       │
       ▼
storeCommentaryInArchivalist(commentary, feed)
       │
       ├──► (existing) bus.Publish RouteRequest → archivalist
       │           │
       │           ▼
       │     archivalist Entry { source_type=scribe, replica_generation=N }
       │           │
       │           ▼
       │     mirrorStoredEntryToKnowledge → markdown in document DB
       │           │
       │           ▼
       │     knowledge runtime ingestion → entities + embeddings + graph
       │
       └──► (NEW) emitNarration(commentary, archivalist_entry_id)
                   │
                   ▼
                fabric.Append(narration_emitted) {
                  Resolution: Coarse,
                  SourceTable: "archivalist_entries",
                  SourceID: <entry_id>,
                  Caused: <last_observed_activity_id>,
                  CausalChain: <walked from observed batch>,
                }
                   │
                   └──► DualTierSink fan-out:
                          ├─ SQLite agent_activity (durable)
                          ├─ Ristretto cache (recent-by-ID)
                          ├─ Bleve indexer (Coarse → indexed)
                          ├─ vectorgraphdb subscriber (Coarse → embedded)
                          └─ ForestSubscriber (precedent-flagged → harvest)
```

Same content, both sides see it. No double LLM cost. No duplicated
storage — the archivalist entry is canonical, the fabric activity
points back to it.

---

## 4. Cadence: ambient, batched, comprehensive

### 4.1 Resolution-filtered subscription

Each scribe subscribes to the Activity Fabric as a `Subscriber`
filtered by:

- `actor_agent_type == self.parentAgentType` (its agent only)
- `resolution >= Fine` (Atomic stays out — too high-volume to narrate
  individually)

This replaces the current `FeedScribe` push pattern entirely. The
~10 scattered `FeedScribe` callsites collapse to one subscription
registration per scribe in `installActivityFabric`.

### 4.2 Batch triggers

The scribe accumulates incoming activities into a per-workstream
buffer. An LLM narration fires on whichever trigger hits first:

| Trigger | Description | Default |
|---|---|---|
| **Turn boundary** | Parent agent's tool-loop completes (signaled by the agent emitting a terminal activity such as `handoff_next`, `finalize_pipeline`, `validate_work`) | Always on |
| **Causal closing** | A `*_completed` / `*_resolved` / `*_accepted` activity closes a previously-in-flight one in this batch | Always on |
| **Batch size** | N activities accumulated since the last narration | 10 |
| **Batch window** | T seconds elapsed since first activity in batch | 5s |
| **Periodic synthesis** | Long-window rolling summary on top of recent batch narrations | 5 min |

A typical agent turn (e.g., a tester run) produces one narration via
the turn-boundary trigger. A bursty multi-tool turn might produce two
or three (batch-size or causal-closing triggered). An idle period
gets one synthesis narration per periodic window.

### 4.3 Per-agent profile overrides

Per-agent `ScribeProfile` (see §6) can override:

- `ResolutionFilter` (defaults to `Resolution >= Fine`, can be tightened)
- `BatchSize` / `BatchWindow` (cost vs. latency tuning)
- `IgnoreKinds` (specific kinds the agent's scribe never narrates)
- `EmphasizeKinds` (kinds that always trigger immediate narration even
  mid-batch — e.g., escalations, errors)
- `PeriodicWindow`

### 4.4 What "comprehensive" looks like in practice

A tester turn after Tier-4 auto-publish wiring emits roughly:

| Activity | Resolution | Scribe sees? |
|---|---|---|
| `tool_call_started` (detect_test_harness) | Fine | yes |
| `llm_request_emitted` / `llm_response_completed` (harness LLM) | Medium | yes |
| `file_read` × 8 (project files) | Atomic | no (too high-volume) |
| `decision_declared` (test_framework=pytest, Tentative) | Coarse | yes |
| `tool_call_completed` (detect_test_harness) | Fine | yes — also a causal-closing trigger |
| `tool_call_started` (write_test) | Fine | yes |
| `file_written` (test file) | Fine | yes |
| `decision_declared` (test_framework=pytest, Committed — auto-promotion) | Coarse | yes |
| `tool_call_completed` (write_test) | Fine | yes — causal-closing |
| `tool_call_started` (run_test_suite) | Fine | yes |
| `command_executed` (pytest invocation) | Fine | yes |
| `tool_call_completed` (run_test_suite) | Fine | yes — causal-closing |
| `handoff_next` or `finalize_pipeline` | Coarse | yes — turn boundary |

~14 Fine+Medium+Coarse activities the scribe sees in ~10 seconds. **One
narration covers all of them**, grounded in the structured stream.

A real narration produced from this batch:

> "Tester detected pytest as the project harness (consistent with
> Charter at services/billing/), authored `test_login.py` via
> write_test, ran the suite — all 4 cases passed in 1.3s. The
> framework decision auto-promoted Tentative→Committed when write_test
> completed. No challenges raised in scope; finalized cleanly.
> Replica-3 has now run 7 successful tester turns since boot."

That narration is informationally dense in a way today's "summarize the
agent's response text" cannot be — because it's grounded in the typed
activity stream, the causal chain, and the replica's longitudinal
context, not in a paraphrased response blob.

---

## 5. Architecture and data flow

### 5.1 Components

```
┌──────────────────────────────────────────────────────────────────┐
│                        Activity Fabric                           │
│                                                                  │
│   Parent Agent emits activities (decisions, tool calls, etc.)    │
│                          │                                       │
│                          ▼                                       │
│              SubscribingSink (DualTierSink)                      │
│                          │                                       │
│                          ├──► SQLite (durable)                   │
│                          ├──► Ristretto (hot)                    │
│                          ├──► Bleve (full-text)                  │
│                          ├──► vectorgraphdb (semantic + graph)   │
│                          ├──► ForestSubscriber (harvest)         │
│                          └──► Scribe (one per parent agent type) │
│                                       │                          │
└───────────────────────────────────────┼──────────────────────────┘
                                        │
                  fabric subscription, filtered by:
                    actor_agent_type == self.parentAgentType
                    resolution >= Fine
                                        │
                                        ▼
┌──────────────────────────────────────────────────────────────────┐
│                Scribe (per-agent-type sidecar)                   │
│                                                                  │
│   per-workstream batch buffer (keyed by ParentCorrelationID)     │
│              │                                                   │
│              ▼ (trigger fires: turn boundary / causal closing /  │
│                batch size / window / periodic synthesis)         │
│              │                                                   │
│      LLM tool loop with batched activity context                 │
│              │                                                   │
│              ├──► causal_trace lens (grounding)                  │
│              ├──► recall_my_history (continuity)                 │
│              ├──► forest skills (cross-session precedent)        │
│              │                                                   │
│              ▼                                                   │
│      structured commentary { summary, progress, decisions,       │
│                             state, risk, handoff_context,        │
│                             details, precedent_worthy?,          │
│                             precedent_why? }                     │
│              │                                                   │
└──────────────┼───────────────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────────────┐
│                 storeCommentaryInArchivalist()                   │
│                          │                                       │
│              ┌───────────┴────────────┐                          │
│              ▼                        ▼                          │
│   (existing)                       (NEW)                         │
│   archivalist Entry                fabric.Append()               │
│   { source_type=scribe,            ActionNarrationEmitted        │
│     origin_agent_type,             { Resolution: Coarse,         │
│     replica_generation=N,            Caused: last_seen,          │
│     ... }                            CausalChain: walked,        │
│              │                       SourceTable: "archivalist", │
│              ▼                       SourceID: <entry_id>,       │
│   knowledge_mirror →                 Subject: { actor scope } }  │
│   markdown in document DB            │                           │
│              │                       │                           │
│              ▼                       │                           │
│   knowledge runtime ingestion        │                           │
│   (entities, relationships,          │                           │
│   embeddings, search index)          │                           │
│                                      │                           │
│                                      └──► fabric subscribers     │
│                                          (Bleve, vectorgraphdb,  │
│                                           Forest, peers via      │
│                                           ambient context)       │
│                                                                  │
│   If precedent_worthy:                                           │
│     fabric.Append(ActionPrecedentEmitted) → ForestSubscriber     │
│     auto-harvest candidate                                       │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### 5.2 Data flows

**Inward (biographer) flow:**

1. Activity emitted by parent agent → SubscribingSink → fan-out.
2. Scribe subscriber receives, accumulates in workstream batch.
3. Trigger fires (turn boundary / causal closing / size / window).
4. Scribe LLM consumes batched activities + causal context lens +
   `recall_my_history` continuity + per-agent profile prompt.
5. Scribe LLM emits structured commentary with optional precedent flag.
6. `storeCommentaryInArchivalist` publishes via existing bus path.
7. Archivalist persists Entry; knowledge mirror creates markdown
   document; knowledge runtime ingests for entities + embeddings + graph.
8. The agent's biography surface grows by one entry, queryable via the
   existing knowledge-recall paths and via the new `recall_my_history`
   skill.

**Outward (fabric narrator) flow:**

1. After step 6 above succeeds and we have an archivalist entry ID,
   the scribe additionally calls `fabric.Append` with a typed
   `narration_emitted` activity referencing that entry.
2. SubscribingSink fans the narration to: SQLite durable storage,
   Ristretto hot cache, Bleve index, vectorgraphdb embed.
3. Peer pipelines see the narration in their next ambient context
   envelope (via `AmbientFor` lens) when scope/peer-relationship
   filters match.
4. Inspector audit (`inspect_open_activity`) surfaces narrations as
   evidence at finalize time.
5. Knowledge agents tailing the fabric process narrations as candidate
   inputs for proactive advisories.
6. Memory Forest's `ForestSubscriber` evaluates precedent_flagged
   narrations as harvest candidates.
7. The chat panel, when the scribe-view feature lands, tails narrations
   to render the agent's voice alongside its raw output.

**Cross-replica continuity flow:**

1. New replica boots. Replica generation increments.
2. New scribe instance starts; calls `recall_my_history` on its parent
   agent's behalf with `replica_generations=[prior]` to retrieve the
   prior life's narrative summary from archivalist + knowledge runtime.
3. The retrieved summary seeds the scribe's prompt context as
   "previously, on this agent's life."
4. Handoff bridge, if available, additionally seeds a fast-path warm
   transfer (in-memory). Bridge becomes optional — the fabric +
   knowledge runtime is the durable continuity layer.

---

## 6. Per-agent ScribeProfile registry

### 6.1 The structural change

Replace the hardcoded `switch parentAgentType` in
`agents/scribe/scribe.go:408` with a declarative registry. Each agent
type registers its `ScribeProfile`; new agent types add a profile next
to their other registrations rather than touching scribe code.

```go
// agents/scribe/profile.go
type ScribeProfile struct {
    AgentType         string
    ResolutionFilter  activity.Resolution // default Fine
    IgnoreKinds       []activity.ActionKind
    EmphasizeKinds    []activity.ActionKind // immediate-narration triggers
    BatchSize         int     // default 10
    BatchWindow       time.Duration // default 5s
    PeriodicWindow    time.Duration // default 5min
    PromptModule      string  // named prompt module: prompts/scribe/profiles/<agent>.md
    OutputSchema      map[string]any // role-specific commentary fields
    PrecedentRules    []PrecedentRule
}

type PrecedentRule struct {
    // When this pattern of activities + commentary appears, mark the
    // narration as precedent-worthy.
    Description string
    Predicate   func(commentary map[string]any, batch []activity.AgentActivity) bool
}
```

### 6.2 Profile registry

```go
// agents/scribe/profile_registry.go
var registry = map[string]ScribeProfile{}

func RegisterProfile(p ScribeProfile)
func ProfileFor(agentType string) ScribeProfile // returns default if unregistered
```

Each agent registers via init() in `agents/scribe/profiles/<agent>.go`:

```go
// agents/scribe/profiles/architect.go
func init() {
    scribe.RegisterProfile(scribe.ScribeProfile{
        AgentType:        "architect",
        EmphasizeKinds:   []activity.ActionKind{
            activity.ActionPlanRatified,
            activity.ActionCharterRatified,
            activity.ActionEscalationRequested,
        },
        PromptModule:     "architect",
        OutputSchema: map[string]any{
            "plan_delta":     "Plan-level changes since last narration",
            "dependencies":   "Cross-agent dependencies introduced or resolved",
            "assumptions":    "Assumptions surfaced or invalidated",
            "handoff_delta":  "What downstream agents need to inherit",
        },
        PrecedentRules: []PrecedentRule{
            {
                Description: "Cross-pipeline scope split resolved cleanly",
                Predicate:   precedentScopeSplitResolution,
            },
        },
    })
}
```

### 6.3 Prompt modules

Per-agent prompt modules live in `prompts/scribe/profiles/<agent>.md`,
embedded via the existing `prompts.MustLoad("scribe", "profiles/<agent>")`
path. Each module defines the agent-specific voice, focus areas, and
expected commentary shape.

A common header (`prompts/scribe/system_base.md`) is concatenated in
front of every per-agent module so all scribes share core conventions
(structured output, batched-narration framing, causal-context grounding,
voice stability).

---

## 7. Replica-generation continuity

### 7.1 Tracking replica generation

Each agent instance's `id` is unique per process restart. The fabric
needs a way to distinguish "this current life" from "prior lives" of
the same logical agent.

Add `replica_generation` to the agent's metadata at scribe-startup
time. The number is monotonic per `(session_id, agent_type)` —
incremented every time a new scribe instance starts for that
combination. Persisted in `.sylk/sessions/{sid}/agents/{agent_type}/replica_counter`
as a tiny atomic file (read, increment, write, fsync).

### 7.2 Carrying replica generation into the storage path

The metadata bag the scribe attaches in `storeCommentaryInArchivalist`
gains one field:

```go
metadata["replica_generation"] = N
```

The existing knowledge mirror passes the metadata through unchanged.
The mirrored markdown gains a YAML front-matter block carrying the
same field for queryability via the document DB.

The fabric `narration_emitted` activity carries the same field in its
payload. Subject's coordinates also include it for indexed lookup:

```go
subject.Coordinates["replica_generation"] = strconv.Itoa(N)
```

### 7.3 Cross-replica queries

Querying across replica generations becomes:

```go
// archivalist consultation, scoped:
query := "source_type:scribe AND origin_agent_type:architect AND replica_generation:[3 TO 4]"

// fabric lens:
filter := activity.QueryFilter{
    SessionID:   sid,
    ActionKinds: []activity.ActionKind{activity.ActionNarrationEmitted},
    SubjectDomain: "architect",  // promoted from Subject.Coordinates
}
// then filter results client-side by Subject.Coordinates["replica_generation"]
```

Both surfaces (knowledge runtime documents + fabric activities) carry
the same metadata, so callers can use whichever path fits — knowledge
agents typically query the document DB; the fabric awareness skills
typically query the activity stream.

---

## 8. The `recall_my_history` skill

### 8.1 What it does

A new skill registered on every parent agent. The agent itself can ask
its biographer:

> "Have I tried this approach in this turn already?"
>
> "What was my reasoning for the choice I'm about to revisit?"
>
> "Did I just commit to X earlier and forget?"

### 8.2 Skill contract

```go
skills.NewSkill("recall_my_history").
    Description("Ask your scribe for a structured digest of your prior work this session — what you decided, why, what you tried, what worked, what didn't. Useful when about to make a decision you may have made before, or when you suspect you're repeating yourself.").
    Domain("memory").
    Keywords("memory", "history", "biography", "prior", "self").
    Priority(85).
    Usage("Use when you suspect you've covered ground before, want to confirm a prior decision, or need to know what reasoning led to a state you're now revisiting.").
    StringParam("scope", "Optional path-prefix scope to narrow recall.", false).
    IntParam("since_minutes", "Lookback window in minutes. Default 60.", false).
    ArrayParam("replica_generations", "Replica generations to include (default: current + most recent prior).", "integer", false).
    IntParam("max_entries", "Maximum biography entries to return. Default 20.", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        // composes a query against:
        //   1. archivalist consultation surface (source_type:scribe + origin_agent_type:<self>)
        //   2. knowledge runtime document search (markdown corpus)
        //   3. optionally enriches with fabric narration_emitted activities for cross-cutting context
        // returns a structured digest the agent's LLM can ground subsequent reasoning in
    }).
    Build()
```

### 8.3 Implementation: a thin wrapper over existing surfaces

The skill delegates to existing infrastructure:

- **Primary surface**: archivalist consultation (`agents/archivalist/consultation.go`) constrained to scribe-tagged entries by origin_agent_type and replica_generation.
- **Secondary enrichment**: knowledge runtime document search for cases where the agent wants markdown-shape recall instead of structured archivalist Entry shape.
- **Tertiary**: fabric `narration_emitted` activities matching the agent's actor type, for cross-pipeline context the archivalist alone wouldn't have (other agents' narrations about adjacent work).

The skill produces a digest combining all three sources, ranked by
recency × scope-relevance × replica-relevance.

### 8.4 Where the skill is registered

In each agent's `registerCoreSkills()`. The wiring follows the same
pattern already established for `AwarenessSkills` and
`CrossPipelineSkills`:

```go
for _, skill := range agentshared.RecallSkills(agentshared.RecallSkillConfig{
    SessionID: func() string { return e.config.SessionID },
    AgentID:   func() string { return e.id },
    AgentType: func() string { return "engineer" },
    // routes through existing archivalist + knowledge runtime
}) {
    e.skills.Register(skill)
}
```

`RecallSkillConfig` is the new shared wiring helper. Internally it
composes against the archivalist client + knowledge runtime — both
already wired into agent pods.

---

## 9. Fabric `narration_emitted` projection

### 9.1 New ActionKind

Add to `core/activity/action_kind.go`:

```go
const ActionNarrationEmitted ActionKind = "narration_emitted"
```

`ResolutionFor(ActionNarrationEmitted)` returns `ResolutionCoarse` —
durable in SQLite agent_activity, Bleve-indexed, vectorgraphdb-embedded,
ForestSubscriber-evaluated.

### 9.2 Activity shape

```go
activity.AgentActivity{
    ID:         activity.NewActivityID(),
    SessionID:  activity.SessionID(sid),
    Timestamp:  time.Now(),
    Resolution: activity.ResolutionCoarse,
    Action:     activity.ActionNarrationEmitted,
    Actor: activity.Actor{
        AgentID:   scribeID,           // the scribe instance
        AgentType: "scribe-" + parentAgentType,
        // PipelineID copied from the parent agent if applicable
    },
    Subject: activity.Subject{
        Domain:     parentAgentType,           // "architect" / "engineer" / etc.
        PathPrefix: scopeDerivedFromBatch,     // best-effort scope extracted from the batch
        Coordinates: map[string]string{
            "replica_generation":     strconv.Itoa(N),
            "parent_correlation_id":  parentCorrelationID,
            "trigger":                triggerName, // turn_boundary | causal_closing | size | window | periodic
        },
    },
    Payload:     marshaledCommentary,  // the same JSON commentary stored in archivalist
    Caused:      lastObservedActivityID,
    CausalChain: chainWalkedFromBatch,
    State:       activity.StatePoint,
    SourceTable: "archivalist_entries",
    SourceID:    archivalistEntryID,
}
```

### 9.3 Causal linking

The scribe walks the causal context of the batch when composing the
narration. The `Caused` field on the narration activity is the most
recent observed activity in the batch (the "tip" of what's being
narrated). The `CausalChain` carries the lineage so consumers can
walk backward via `causal_trace`.

This gives `causal_trace(narration_id)` the ability to return the full
causal antecedents of a narration — an inspector quoting a narration
in audit can also surface the structured chain that produced it.

### 9.4 Emission timing

The emission happens **after** the archivalist entry persists, so the
fabric activity can reference a real `entry_id`. The scribe's
`storeCommentaryInArchivalist` is fire-and-forget over the bus today;
to emit the fabric activity with a real ID we either:

**Option A (preferred)**: Make the archivalist's store-route response
synchronous enough to return the entry ID before the scribe fires the
fabric activity. The archivalist already produces an `Entry` record
with an ID at ingestion time; surfacing it in the route response is a
small change.

**Option B (fallback)**: Generate a deterministic entry ID client-side
(e.g., `scribe_<agentType>_<replicaGen>_<timestamp_ns>`), pass it as
the desired ID in the route request, and the archivalist honors it.
The fabric activity uses the same ID for `SourceID`. No async
round-trip needed.

The implementation will use Option B because it simplifies the cadence
and removes a synchronous dependency on the archivalist.

---

## 10. Precedent emission

### 10.1 The optional fourth output

The scribe LLM's commentary structure gains two optional fields:

```json
{
  "summary": "...",
  "progress": "...",
  "decisions": "...",
  "state": "...",
  "risk": "...",
  "handoff_context": "...",
  "details": { ... },
  "precedent_worthy": true,
  "precedent_why": "Cross-pipeline scope-split resolved a Charter conflict in 2 messages; pattern worth harvesting for future framework disputes."
}
```

When `precedent_worthy: true`, the scribe additionally emits:

```go
activity.AgentActivity{
    Action: activity.ActionPrecedentEmitted,
    Resolution: activity.ResolutionCoarse,
    // ... actor, subject, payload from the narration ...
    Caused: <narration_id>,  // the narration is what flagged this as precedent
}
```

### 10.2 What picks it up

The existing `ForestSubscriber` (built in Tier 11) already accepts
`ActionPrecedentEmitted` as a harvest candidate via its
`electCandidate` rule. With scribes emitting them, the precedent loop
that today never naturally closes finally does.

### 10.3 Per-agent precedent rules

`ScribeProfile.PrecedentRules` (declared per agent, see §6.1) is a
predicate set the scribe can consult to deterministically suggest
precedent flagging. The LLM still has the final say (the `precedent_worthy`
field is its decision), but the rules surface candidate patterns so
the LLM doesn't have to discover them from scratch.

Example rules:

| Agent | Rule | Predicate |
|---|---|---|
| Architect | "Charter ratification" | batch contains ActionCharterRatified |
| Architect | "Plan stalled, recovered cleanly" | batch contains ActionPlanProposed → ActionPlanRatified after a recovery activity |
| Tester | "Cross-pipeline scope-split" | batch contains ActionScopePartitioned with multiple actor pipelines |
| Engineer | "Build backend chosen + first commit succeeded" | batch contains build_backend Committed + first artifact_published |
| Inspector | "Audit caught divergence and resolved" | batch contains inspect_open_activity → request_correction → challenge_response |

These rules live alongside the agent's profile registration.

---

## 11. Causal narrative grounding

### 11.1 The lens used

When the scribe composes a narration, it queries
`lenses.CausalContext(batchTipActivityID)` to walk backward through
the causal DAG. The walked ancestors become input context to the
scribe's LLM prompt, framed as "this batch happened because of these
prior chains."

### 11.2 Why this matters

Today's scribe paraphrases the agent's final response. It cannot say
"this decision happened because this challenge was raised because this
peer's commitment conflicted with the project Charter." That's the
chain, and the fabric *has* the chain via Caused/Resolves/CausalChain.

With causal grounding the scribe's narrations become genuinely
diagnostic: not "the agent did X" but "the agent did X because the
sequence Y → Z → W converged at this point." That diagnostic quality
is what makes narrations valuable as inspector audit evidence,
knowledge-agent input, and Memory Forest precedent.

### 11.3 Cross-cutting awareness

For high-priority narrations (e.g., when `EmphasizeKinds` triggered),
the scribe can also query `lenses.AmbientFor(scope)` to enrich the
narration with cross-pipeline context: "while this happened, peers in
adjacent scopes were doing X." This makes the scribe's narration the
synthesis layer between its own agent's life and the parallel
universe of peer activity.

### 11.4 Bounded grounding

The grounding query is bounded — `causal_trace` walks at most 32
ancestors (configurable per profile); `AmbientFor` returns a bounded
envelope. The scribe's prompt size stays under control even when the
batch is large.

---

## 12. Implementation phases

Each phase independently shippable, each delivers value before the
next starts, no phase breaks any sovereign system.

### Phase 0 — Replica generation tracking

- Add `replica_generation` atomic counter file at
  `.sylk/sessions/{sid}/agents/{agent_type}/replica_counter`.
- Increment on scribe construction; pass into `Scribe` struct.
- Surface in metadata of every `storeCommentaryInArchivalist` call.

Existing knowledge mirror passes metadata through unchanged. Existing
queries can already filter by metadata. **Immediately useful** — even
without any other phase, the document DB and knowledge graph become
queryable by replica generation.

### Phase 1 — Fabric subscription replaces FeedScribe push

- Add `Scribe.Receive(ctx, activity)` method conforming to
  `activitystore.Subscriber`.
- In `installActivityFabric`, register each scribe as a Subscriber
  with `actor_agent_type` filter.
- Scribe's existing workstream batching reframes around incoming
  activities instead of `Feed` calls. Workstream key becomes the
  parent's `pipeline_id` (or correlation_id where pipeline isn't
  applicable).
- The 10+ `FeedScribe(...)` callsites in agent files become no-ops
  initially; scribe gets all needed input from the fabric subscription.
- `FeedScribe` stays as a deprecated method (logs a warning) so we can
  decommission the callsites incrementally without breaking anything.

### Phase 2 — Batched cadence

- Replace per-feed LLM trigger with the cadence policy described in §4.2.
- Default `BatchSize=10`, `BatchWindow=5s`, turn-boundary and
  causal-closing always on, periodic synthesis at 5 min.
- Add per-profile overrides via `ScribeProfile.BatchSize` etc.
- The narration LLM call still produces the existing structured
  commentary shape; the only difference is what's in its prompt
  context (batched activities + causal context, not a single
  user_request/agent_response pair).

### Phase 3 — Fabric `narration_emitted` projection

- Add `ActionNarrationEmitted` to ActionKind taxonomy with
  `ResolutionCoarse`.
- Modify `storeCommentaryInArchivalist` to additionally
  `activity.Append` a `narration_emitted` activity with the
  back-reference to the archivalist entry ID (Option B from §9.4 —
  client-side deterministic ID).
- Activity carries actor (the scribe), subject (Domain=parentAgentType,
  Coordinates including replica_generation), payload (same commentary
  JSON), and causal links (walked from batch).
- Existing fabric subscribers (Bleve, vectorgraphdb, ForestSubscriber)
  pick narrations up automatically.

### Phase 4 — `ScribeProfile` registry

- Build `agents/scribe/profile.go` with `ScribeProfile`,
  `RegisterProfile`, `ProfileFor`.
- Migrate the hardcoded `switch parentAgentType` in
  `scribe.go:408` into per-agent files at
  `agents/scribe/profiles/<agent>.go`. Each file registers via
  init().
- Per-agent prompt modules at `prompts/scribe/profiles/<agent>.md`
  embedded and loaded via the existing `prompts.MustLoad` pattern.
- Common base prompt at `prompts/scribe/system_base.md`; per-agent
  modules concatenate after.

### Phase 5 — `recall_my_history` skill

- Build `agents/shared/recall_skills.go` with
  `RecallSkillConfig` and `RecallSkills(...)` returning the
  `recall_my_history` skill.
- Skill internally:
  - Queries archivalist consultation with `source_type:scribe AND
    origin_agent_type:<self.agentType> AND replica_generation:[N..]`
  - Queries knowledge runtime document search for markdown-shape recall
  - Queries fabric `narration_emitted` activities for cross-cutting
    context
  - Returns a combined digest ranked by recency × scope × replica-relevance
- Wire onto every agent's `registerCoreSkills()` (mechanical: same
  pattern as awareness skills).

### Phase 6 — Causal grounding in scribe prompts

- Scribe LLM prompt enriched with output of
  `lenses.CausalContext(batchTip)` — walked ancestors framed as "this
  batch happened because of these prior chains."
- For high-priority narrations (`EmphasizeKinds` triggered), also
  enrich with `lenses.AmbientFor(scope)` for cross-pipeline context.
- Bounded by max-ancestors and envelope-size config in the profile.

### Phase 7 — Precedent emission

- Extend scribe commentary schema with optional `precedent_worthy:
  bool, precedent_why: string` fields.
- When `precedent_worthy: true`, scribe additionally emits
  `ActionPrecedentEmitted` linked to the narration as `Caused`.
- Per-agent `PrecedentRules` declared in the ScribeProfile surface
  candidate patterns to the LLM via prompt — the LLM still decides.
- ForestSubscriber already harvests `ActionPrecedentEmitted` candidates.

### Phase 8 — Cross-replica continuity via fabric query

- New scribe instance on replica boot calls
  `recall_my_history(replica_generations=[prior])` on its parent
  agent's behalf to seed its prompt context with prior life summary.
- Handoff bridge stays as fast-path warm transfer; cold restarts now
  reliably inherit via this query path.

### Phase 9 — Chat panel scribe view (optional, UX-bound)

- TUI tails `narration_emitted` activities and renders them in a
  dedicated scribe band per agent. Out of scope for the core fabric
  integration but enabled by all the above.

### Phase 10 — Deprecation cleanup

- Remove deprecated `FeedScribe` callsites from agent files.
- Remove the no-op feed handler (subscription is now the only input
  path).
- Remove the hardcoded `switch parentAgentType` in `scribe.go` (fully
  superseded by ScribeProfile registry).

---

## 13. What stays / what changes / what gets removed

### Stays unchanged

- **Per-agent-type scribe instances** spawned per `AgentPod`.
- **Workstream isolation by ParentCorrelationID** within a scribe.
- **Structured commentary shape** the scribe produces (summary,
  progress, decisions, state, risk, handoff_context, details).
- **`storeCommentaryInArchivalist` call** as the terminal step of a
  scribe turn — but the cadence of when it fires changes.
- **Archivalist `Entry` ingestion** path with `source_type=scribe` tag.
- **`mirrorStoredEntryToKnowledge`** auto-mirror to knowledge runtime
  as markdown.
- **Knowledge runtime ingestion** — markdown to entities/embeddings/graph.
- **Handoff bridge** as a warm-transfer fast path (now optional;
  cold-start uses fabric query).
- **`store_archivalist` skill** as the LLM-callable terminal — but the
  scribe LLM only runs at batched-narration triggers, not per FeedScribe.

### Changes

- **Input source**: `FeedScribe` push (final response blob) →
  fabric subscription (full structured activity stream filtered to
  Resolution >= Fine).
- **LLM trigger cadence**: per-FeedScribe → batched (turn boundary /
  causal closing / size / window / periodic).
- **Per-agent customization**: hardcoded switch in scribe.go →
  declarative `ScribeProfile` registry with per-agent prompt modules.
- **Output count**: 1 (archivalist entry) → 2 (archivalist entry +
  fabric `narration_emitted` activity referencing it).
- **Cross-replica continuity**: handoff-bridge-only (or cold-start) →
  fabric query via `recall_my_history` + handoff bridge as fast path.
- **Precedent emission**: never naturally fires → scribe is the natural
  emitter, ForestSubscriber harvests.
- **Commentary schema**: gains `precedent_worthy` + `precedent_why`
  optional fields.
- **Metadata**: gains `replica_generation`.

### Gets removed (in Phase 10)

- The 10+ scattered `FeedScribe(...)` callsites in agent files.
- The no-op feed handler in `Scribe.Start()` (was a stub for the
  bus subscription that never did anything).
- The hardcoded `switch parentAgentType` in `scribe.go:408` (replaced
  by ScribeProfile registry).
- Per-workstream 15-min TTL becomes less important (cross-replica
  continuity is now substrate-backed); tunable per profile.

---

## 14. Test strategy

Each phase ships with non-happy-path tests covering:

### Phase 0
- Replica counter increments correctly across scribe restarts.
- Counter file survives session lifecycle.
- Concurrent scribe boots in the same session don't collide.

### Phase 1
- Scribe receives activities filtered by actor_agent_type.
- Scribe ignores activities from other agent types.
- Scribe ignores Atomic-resolution activities.
- Scribe panic in a subscriber doesn't break the SubscribingSink.

### Phase 2
- Turn-boundary trigger fires on terminal activities (handoff_next,
  finalize_pipeline, etc.).
- Causal-closing trigger fires when a `*_completed` resolves a prior
  in-flight in the batch.
- Batch-size trigger fires after N activities.
- Batch-window trigger fires after T seconds even with fewer than N.
- Triggers are mutually-non-blocking — no double narration if multiple
  fire near-simultaneously (mutex-guarded narration call).
- Periodic synthesis fires at the configured interval even with no
  recent activities.

### Phase 3
- Every commentary written to archivalist also produces a
  `narration_emitted` fabric activity.
- The activity's SourceTable + SourceID match the archivalist entry.
- The activity's Caused links to the most recent observed activity.
- Bleve subscriber indexes the narration.
- ForestSubscriber considers narrations as candidates only when
  precedent-flagged.

### Phase 4
- Each registered ScribeProfile is loadable and produces the expected
  per-agent prompt.
- Unregistered agent types fall back to a default profile (empty
  EmphasizeKinds, default cadence, generic prompt).
- ProfileFor is concurrent-safe.

### Phase 5
- `recall_my_history` returns digest combining all three sources
  (archivalist + knowledge + fabric).
- Replica-generation filtering works: prior generations only,
  current+prior, all generations.
- Scope filter narrows results correctly.
- Empty-history case returns empty, not error.

### Phase 6
- Causal context is included in the narration LLM prompt.
- Bounded by max-ancestors per profile.
- Empty causal chain (root activity) handled gracefully.

### Phase 7
- `precedent_worthy: true` triggers `ActionPrecedentEmitted` emission.
- Per-agent `PrecedentRules` predicates are evaluated and surfaced in
  the prompt.
- ForestSubscriber harvest counter increments for precedent narrations.

### Phase 8
- New scribe boot on a session with prior replicas inherits prior
  narrative via `recall_my_history`.
- Cold restart with no prior replicas yields empty inheritance, no
  error.
- Handoff bridge inheritance is preferred when both are available
  (bridge is fast-path).

### Cross-cutting

- `Activity Fabric` continues to pass full-suite regression tests after
  each phase.
- Existing scribe tests (workstream isolation, backpressure, handoff
  bridge) continue to pass.
- Existing archivalist tests (knowledge mirror, session store, L2
  flush) continue to pass.

---

## 15. Risks and mitigations

### Risk 1 — Token cost regression

Scribes today fire LLM calls per-FeedScribe; new model fires per-batch.
Naively this could be the same cost or higher (richer input prompt).

**Mitigation**: The batched-narration prompt includes the structured
activity stream (compact JSON) + causal context (bounded) instead of
verbose response prose. Net token delta per narration is roughly
neutral. Cost regression risk is low in steady state and can be tuned
via per-profile `BatchSize` / `BatchWindow`.

### Risk 2 — Latency added to archivalist write path

Adding a fabric activity emission alongside the existing archivalist
publish adds one SQLite write + Ristretto put on the critical path.

**Mitigation**: Both already happen on the orchestrator's BunSQLite
handle; the additional row is single-digit-ms. The archivalist publish
itself remains fire-and-forget over the bus. The scribe LLM call
dominates latency by orders of magnitude — the fabric write is noise.

### Risk 3 — Subscription storm at replica boot

When a new replica starts, its scribe becomes a fresh subscriber to
the SubscribingSink. If the parent agent's pipeline is already busy,
the scribe sees an immediate burst of activities to process.

**Mitigation**: Initial batch window honors the same triggers as
steady-state (size/window). Bursts produce one narration per batch,
not one per activity. The scribe's existing per-workstream batching
already handles burst ingest gracefully.

### Risk 4 — Replica-generation counter race

Two scribe boots in the same session within milliseconds could collide
on the counter file.

**Mitigation**: Use atomic file-rename pattern (write to .tmp, fsync,
rename) plus an exclusive flock. Conflicts are extremely rare in
practice (one boot per agent type per session) and the pattern is
already used elsewhere in sylk for similar counter files.

### Risk 5 — Chronological ordering of cross-source recall

`recall_my_history` returns a digest combining archivalist entries,
knowledge runtime documents, and fabric activities. Each has its own
timestamp semantics.

**Mitigation**: Normalize to UTC and present a single chronological
ordering. Entries from different sources at the same logical moment
(an entry + its mirrored markdown + its narration activity) are
collapsed into one digest item via shared SourceID linkage.

### Risk 6 — Disagreement between archivalist Entry and fabric activity

Both store the same commentary; if they diverge (e.g., bug in
serialization), consumers see different content depending on which
surface they query.

**Mitigation**: The fabric activity's `Payload` is exactly the same
JSON that's persisted to the archivalist entry. Both are produced by
a single `json.Marshal` call. The fabric activity's SourceID points to
the archivalist entry, so consumers detecting divergence can always
fall back to the canonical source. A test asserts byte-identity in
the happy path.

### Risk 7 — Handoff bridge becomes redundant (and rusty)

If cold-start inheritance via `recall_my_history` is reliable, the
handoff bridge code path may become rarely exercised and accumulate
bit rot.

**Mitigation**: Keep the bridge as a tested fast-path optimization —
it provides lower-latency warm transfer when both old and new replicas
are alive simultaneously. Periodic synthetic test exercises both
paths to keep the bridge code from rotting.

---

## 16. Key design properties (against the project bar)

**Maximally correct.** One structured commentary per narration; one
canonical archivalist entry; one fabric activity referencing that
entry by ID. No duplicated state across surfaces. The fabric activity
and the archivalist entry are produced from the same `json.Marshal`
call — divergence is impossible by construction.

**Robust.** The fabric activity emission is post-archivalist-publish —
if the fabric write fails, the archivalist entry still exists. The
archivalist publish is fire-and-forget over the bus — if the publish
fails, the scribe's LLM call still produced something the agent's
biographer remembers. Cross-replica continuity is substrate-backed
(archivalist + knowledge runtime + fabric); even if one of the three
is unavailable, the other two carry the narrative.

**Performant.** Batched LLM calls bound the narration cost to one per
turn-equivalent instead of one per discrete event. The fabric write is
a single SQLite row + Ristretto put — single-digit-ms.

**Resource-efficient.** No new persistence layer — the substrate
already exists (archivalist session store, knowledge runtime, fabric
agent_activity table). Only the typed projection is new. Memory Forest
harvest of precedent narrations is bounded by the precedent_worthy
flag. No background goroutines specific to scribes.

**Agentic.** Scribes give every agent a dedicated, voice-stable
biographer that the agent itself can consult via `recall_my_history`.
Other agents see scribe narrations in ambient context — the
narrative voice becomes ambient. Knowledge agents harvest narrations
as proactive-advisory candidates. Memory Forest learns from narration-
flagged precedent. The scribe is no longer "a write-only side
effect"; it's a participant.

**Net-additive.** All existing scribe paths continue to work during
the migration. FeedScribe stays as a deprecated no-op until Phase 10.
The fabric activity emission can be turned off (skip the
`activity.Append` call) and the archivalist + knowledge surface keeps
working. The handoff bridge stays as a warm fast-path. Nothing breaks.

---

## 17. End-to-end scenario

To make the design concrete, here's a complete walk-through of one
batch narration in the new system:

**Setup**: Session `sess-billing` is in progress. Architect has just
ratified a Charter establishing pytest as the project test framework.
Tester replica 3 (scribe instance `scribe-tester-pipeline-rep3`) is
running pipeline `pipe-7` for `services/billing/`.

**T+0**: Tester's `detect_test_harness` skill fires. The scribe (a
fabric Subscriber filtered to `actor_agent_type=tester-pipeline`,
Resolution≥Fine) receives:
- `tool_call_started{action: detect_test_harness}` (Fine)
- `llm_request_emitted{model: claude-opus-4-7}` (Medium)
- `llm_response_completed{model: claude-opus-4-7, tokens_in: 4200, tokens_out: 850, retries: 0}` (Medium)
- `decision_declared{domain: test_framework, value: pytest, confidence: tentative}` (Coarse)
- `decision_promoted{from: tentative, to: committed}` (Coarse — auto-promotion via Charter alignment)
- `tool_call_completed{action: detect_test_harness}` (Fine)

Scribe's batch buffer holds 6 activities.

**T+2.3s**: Tester's `write_test` skill fires. Scribe receives 4 more
activities (start, file_written, decision_promoted to Consensus on
artifact, completed). Batch buffer at 10 — **batch-size trigger fires**.

**T+2.4s**: Scribe LLM tool loop begins:
- Prompt context: per-agent profile prompt (tester-pipeline), the 10 batched
  activities as structured JSON, causal context walked from the batch
  tip (showing the Charter ratification chain), recall hint summarizing
  prior batch narrations from this turn (none — this is the first batch).
- LLM produces structured commentary:

```json
{
  "summary": "Tester detected pytest harness, authored test_login.py, decision auto-promoted Tentative→Committed via Charter alignment, then promoted to Consensus when artifact published.",
  "progress": "Pipeline 7 in services/billing/ now has its first authored test; framework is locked at Consensus.",
  "decisions": "test_framework=pytest at services/billing/ — adopted from Charter; corroborated by write_test artifact.",
  "state": "Awaiting run_test_suite to validate the authored test.",
  "risk": "None observed; LLM round-trip was clean (1 retry attempt budget consumed).",
  "handoff_context": "Replica-3 has authored 1 test under the established pytest harness; next replica should resume from run_test_suite if interrupted.",
  "details": {
    "files_authored": ["services/billing/tests/test_login.py"],
    "framework_decision_id": "dec_a8c2",
    "auto_promotion_chain": ["dec_71f0 (Tentative)", "dec_a8c2 (Committed)", "dec_a8c2 (Consensus)"],
    "tokens_consumed": 5050
  },
  "precedent_worthy": false
}
```

**T+2.6s**: Scribe calls `storeCommentaryInArchivalist` with the
commentary + metadata `{source_type: "scribe", origin_agent_type:
"tester-pipeline", replica_generation: 3, batch_trigger:
"batch_size"}`. The archivalist publishes the route-request via bus;
returns deterministic entry ID `scribe_tester-pipeline_rep3_1747836342000123456`.

**T+2.65s**: Scribe additionally calls `activity.Append(narration_emitted)`:
- `Resolution`: Coarse
- `Subject.Domain`: "tester-pipeline"
- `Subject.PathPrefix`: "services/billing/"
- `Subject.Coordinates`: `{replica_generation: "3", parent_correlation_id: "corr-123", trigger: "batch_size"}`
- `Payload`: same commentary JSON
- `Caused`: ID of the last `tool_call_completed{write_test}` activity in the batch
- `CausalChain`: walked back through the batch tip
- `SourceTable`: "archivalist_entries"
- `SourceID`: `scribe_tester-pipeline_rep3_1747836342000123456`

**T+2.66s**: SubscribingSink fans the narration:
- DualTierSink writes the row to SQLite agent_activity (durable).
- Ristretto puts it in the recent-by-ID hot cache.
- BleveSubscriber indexes the prose for full-text search.
- vectorgraphdb embeds the payload for semantic similarity.
- ForestSubscriber evaluates: precedent_worthy was false → not
  harvested.

**T+2.7s**: Archivalist's bus consumer ingests the route request.
Entry persisted with the deterministic ID. Knowledge mirror creates
markdown at `archivalist/scribe/tester-pipeline/scribe_tester-pipeline_rep3_1747836342000123456.md`.
Knowledge runtime ingestion picks up the markdown — entities (test
file, framework, pipeline, scope) and relationships (authored-in,
governed-by-Charter) extracted; embedding indexed.

**T+5s**: Engineer in pipeline 8 (`services/billing/api/`) calls a
tool. Its tool result includes an `<ambient_context>` envelope. The
`AmbientFor` lens runs and surfaces:

```
<ambient_context>
  scope: services/billing/api/
  in_flight_activities: 1
    • tester-pipeline running in services/billing/ (1m ago)
  recent_peer_commitments:
    • test_framework=pytest at services/billing/ (Consensus, by tester-pipeline)
  advisories: 1
    • scribe-tester-pipeline (replica 3): "Tester detected pytest harness,
      authored test_login.py, decision auto-promoted via Charter alignment..."
      (narration, 2s ago)
</ambient_context>
```

The engineer's LLM sees the scribe's narration in its ambient context
and knows not just *that* the tester chose pytest, but *the reasoning
chain* the tester's biographer narrated — Charter alignment, auto-
promotion, no retry concerns. The engineer adapts its own work
accordingly.

**T+30s**: Tester replica 3 is demoted (idle timeout). Scribe instance
stops cleanly. Workstream state evicted from memory.

**T+5min**: Tester replica 4 boots for the same pipeline. New scribe
instance `scribe-tester-pipeline-rep4` starts. Replica counter
increments. Scribe queries the parent's `recall_my_history` to get
prior life summary:

```
Prior replica narratives for tester-pipeline in sess-billing:
  Replica 3 (1 narration, 30s ago):
    "Tester detected pytest harness, authored test_login.py..."
    files_authored: ["services/billing/tests/test_login.py"]
    framework_decision_id: dec_a8c2
    handoff_context: "Replica-3 has authored 1 test under the established
                      pytest harness; next replica should resume from
                      run_test_suite if interrupted."
```

The new replica's scribe seeds its prompt context with this prior life
summary. When the new tester takes its first turn, it knows from its
biographer that there's already a test authored and the framework is
at Consensus.

**T+5d**: Memory Forest harvest cycle runs. Past sessions' narrations
that were precedent-flagged form a corpus. Future sessions facing
similar Charter-driven framework alignment retrieve these precedents
to inform their own first-turn decisions.

This is the system getting smarter from its own narrative voice — the
property the dual-identity model exists to enable.

---

## 18. Summary

The scribe is sylk's authoritative biographer for one specific agent
replica, and — with the changes in this design — also the typed
narrative voice for that agent in the Activity Fabric.

The integration is **net-additive**: the existing archivalist + knowledge
mirror + knowledge runtime path stays unchanged and remains the
canonical persistence for scribe content. The fabric integration adds
one typed projection alongside, enabling cross-cutting consumption
(peer awareness, inspector audit, knowledge-agent harvest, semantic
search, chat panel render, Memory Forest precedent) without
duplicating storage or LLM cost.

The scribe stops being "an LLM that paraphrases another LLM" and
becomes "the system's narrative voice over its own causal graph for
this specific agent replica" — both inward (the agent's biographer the
agent itself can consult) and outward (the typed voice the rest of
the system can quote, query, embed, and learn from).

Implementation is staged across 10 phases. Each ships independently,
each delivers value before the next starts, no phase breaks any
sovereign system. The substrate the scribe needs already exists in
the codebase from prior fabric work; the remaining work is wiring,
cadence, and one new ActionKind.
