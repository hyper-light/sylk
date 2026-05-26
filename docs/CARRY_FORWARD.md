# Carry Forward

## 1. Problem

Planning currently repeats work across phases and turns. In a first phase, an
agent may consult a peer, inspect the workspace, or synthesize useful evidence.
In a later phase, the same agent sometimes behaves as if that evidence was not
available and performs effectively the same work again.

The core problem is not peer routing. It is same-agent continuity:

1. Durable findings exist on the claims board as testaments and artifacts.
2. A later turn needs those findings as an explicit working set.
3. The agent needs a deterministic boundary for "what has already been carried
   forward" versus "what new testaments/artifacts appeared since then."

The carry-forward mechanism solves that by using the claims system itself. It
does not add a separate memory store. It produces new testaments with artifacts
that summarize and cite earlier testaments/artifacts.

## 2. Claims Model

This design follows `docs/CLAIMS.md`:

- An Action is a set of claims or testaments issued together.
- A Claim is a precise assertion or instruction with validations.
- A Testament is the response to a claim.
- An Artifact is the proof attached to a testament.
- Claims are the constraints. The board is the state machine.

Carry-forward does not carry claims as evidence. It carries testaments and
artifacts.

Claims still matter: a carry-forward action should issue a claim that constrains
the carry-forward work. The result of satisfying that claim is a continuity
testament with artifacts.

## 3. Design Principle

Every phase boundary should be able to answer:

> What durable testaments/artifacts should this agent reuse later, and through
> what board sequence have they already been incorporated?

The answer is itself a testament.

A carry-forward testament:

- summarizes reusable working context;
- attaches structured artifacts for recall;
- relates to source testaments/artifacts with `derived_from`;
- advances a cursor artifact so future scans do not start from scratch;
- may `amends` or `supersedes` the prior continuity testament.

## 4. Continuity Topic

Carry-forward is scoped by a stable topic, not by vague chronology.

Examples:

- `python_cli_planning`
- `interrupt_handling_debugging`
- `consult_completion_root_cause`
- `plan:<plan_id>`
- `request:<root_correlation_id>`

The topic should be stable across the phases that need to share evidence. Once
a plan exists, the topic should normally include or map to the `plan_id`.

## 5. Continuity Claim

The carry-forward skill creates or reuses a claim whose job is to constrain the
continuity write.

Example claim:

```text
Title:
Carry forward durable Architect working evidence for python_cli_planning.

Description:
Summarize durable testaments and artifacts produced since the last continuity
cursor for python_cli_planning. Preserve only reusable findings that later
planning phases should consume. Reference source testaments/artifacts with
derived_from relations and advance the continuity cursor.

Scope:
- continuity: architect_working_context
- agent: architect
- session: default
- topic: python_cli_planning
- request_correlation: <root correlation id>
- plan: <plan id, when known>

Validations:
- receipt: A continuity testament is submitted.
- inspection: The testament references source testaments/artifacts via
  derived_from and excludes transient progress-only artifacts.
```

The claim is not the memory. It is the instruction for producing the memory.

## 6. Continuity Testament

The continuity testament is the durable carried-forward unit.

Example:

```text
Summary:
Carried forward Python CLI planning evidence: no existing Python package
structure was found; use a minimal pyproject-based CLI with a pytest smoke test.

Relations:
- claim -> <continuity_claim_id>
- derived_from -> <source_testament_id>
- derived_from -> <source_artifact_id>
- amends -> <prior_continuity_testament_id>
```

Use `amends` when the new testament extends prior carried context. Use
`supersedes` when it replaces prior carried context because the old summary is
stale or contradicted.

## 7. Continuity Artifacts

The continuity testament should attach these artifact kinds.

### `working_context`

Human-readable durable summary for later turns.

```json
{
  "kind": "working_context",
  "reference": "No existing Python package structure was found. Prefer a minimal pyproject CLI with src layout only if packaging is required; otherwise keep a flat hello.py plus tests.",
  "metadata": {
    "topic": "python_cli_planning",
    "freshness_horizon": "15m",
    "confidence": "committed"
  }
}
```

### `evidence_digest`

Structured findings that a planner can consume without re-reading every source.

```json
{
  "kind": "evidence_digest",
  "reference": "python_cli_planning evidence digest",
  "metadata": {
    "findings": [
      {
        "summary": "No setup.py/setup.cfg files were found.",
        "source_artifact_ids": ["artifact_..."],
        "relevance": "repo structure"
      },
      {
        "summary": "Search for pytest/pyproject/requirements did not reveal an existing Python project.",
        "source_artifact_ids": ["artifact_..."],
        "relevance": "existing infrastructure"
      }
    ]
  }
}
```

### `continuity_cursor`

The deterministic scan boundary.

```json
{
  "kind": "continuity_cursor",
  "reference": "carried forward through board sequence 1842",
  "metadata": {
    "topic": "python_cli_planning",
    "from_sequence": 1710,
    "through_sequence": 1842,
    "source_testament_ids": ["testament_..."],
    "source_artifact_ids": ["artifact_..."]
  }
}
```

### `session_cursor`

The cross-session chain boundary.

```json
{
  "kind": "session_cursor",
  "reference": "continuity spine node for architect/python_cli_planning",
  "metadata": {
    "agent_id": "architect",
    "topic": "python_cli_planning",
    "session_id": "default",
    "board_id": "session-default",
    "through_sequence": 1842,
    "previous_session_id": "prior-session",
    "previous_continuity_testament_id": "testament_...",
    "previous_board_id": "session-prior-session",
    "session_boundary_index": 3
  }
}
```

`session_cursor` is separate from `continuity_cursor` because they answer
different questions. `continuity_cursor` says which board sequence range was
incorporated. `session_cursor` says how this continuity testament connects to
the previous session's continuity testament.

### `projection_receipt`

The deterministic ingest receipt.

```json
{
  "kind": "projection_receipt",
  "reference": "claims knowledge projection completed",
  "metadata": {
    "document_id": "claims_testament_testament_...",
    "document_path": "claims/default/architect/python_cli_planning/testament_....md",
    "graph_node_ids": ["claim:...", "testament:...", "artifact:..."],
    "projector": "claims_knowledge_mirror",
    "source_board_id": "session-default",
    "source_sequence": 1842
  }
}
```

This artifact is not required for the agent to recall its own carried context.
It exists to make deterministic projection failures visible as claims-board
evidence instead of hidden log messages.

### `source_index`

Audit-friendly source map.

```json
{
  "kind": "source_index",
  "reference": "source testament/artifact index for python_cli_planning",
  "metadata": {
    "sources": [
      {
        "testament_id": "testament_...",
        "artifact_ids": ["artifact_..."],
        "agent_id": "librarian",
        "reason": "repo discovery result used by plan design"
      }
    ]
  }
}
```

## 8. Cursor Semantics

The agent should not decide how far back to look from time or message count.
The carry-forward skill owns the boundary.

Algorithm:

1. Find the latest continuity testament for `(agent, session, topic)`.
2. Read its `continuity_cursor` artifact.
3. Let `from_sequence = cursor.through_sequence`.
4. Compute `through_sequence` as the board high-water sequence.
5. Scan only testaments/artifacts where:
   - `Sequence > from_sequence`
   - `Sequence <= through_sequence`
6. Filter for durable relevance.
7. Submit a new continuity testament with a cursor through
   `through_sequence`.

The board high-water sequence can be computed as the maximum `Sequence` observed
across actions, claims, testaments, and artifacts in the projection, or exposed
as a board accessor in the implementation.

If no continuity cursor exists, use an explicit initial anchor:

- the current request correlation sequence, if known;
- the plan creation sequence, if known;
- otherwise the earliest relevant sequence in the session/topic.

The first run should be conservative. It should carry only clearly relevant
testaments/artifacts and then establish the cursor for future incremental runs.

## 9. What Gets Carried

Candidate source objects are testaments and artifacts, not claims.

Carry forward:

- peer consultation answers;
- workspace discovery results;
- plan phase summaries;
- research findings;
- decisions and constraints that future phases must preserve;
- error artifacts that explain a durable blocker;
- artifacts with file references, structured findings, or response payloads.

Do not carry forward:

- routine progress narration;
- spinner/thinking/status-only artifacts;
- duplicate source artifacts already covered by the latest continuity testament;
- stale evidence unless the continuity testament explicitly marks it stale;
- claims by themselves.

## 10. Idempotence

Idempotence comes from `derived_from` relations and the cursor.

A source testament/artifact is already incorporated if any current continuity
testament for the same topic has a `derived_from` relation to it, or if it is at
or below the latest cursor's `through_sequence` and was not selected because it
was irrelevant.

Re-running `carry_forward` over the same window should either:

- return the existing continuity testament, or
- submit a no-op continuity testament only if there is a useful reason to record
  that no durable evidence was found.

Prefer not to submit no-op testaments unless a caller needs a cursor advanced
for a noisy window.

## 11. Recall Semantics

Recall should read continuity testaments, not rescan the whole board.

Algorithm:

1. Locate continuity testaments for `(agent, session, topic)`.
2. Prefer the latest non-superseded testament.
3. Read `working_context`, `evidence_digest`, and `source_index` artifacts.
4. Return the digest plus source IDs.
5. If the digest is fresh and relevant, use it before consulting peers or
   re-running workspace searches.

Recall can traverse `derived_from` when the agent needs source detail, but the
normal planning path should consume the digest directly.

For cross-session recall, the skill should not fan out blindly over every old
session. It should walk a continuity spine:

1. Find the latest continuity testament for `(agent, topic)`.
2. Read its `continuity_cursor` and `session_cursor` artifacts.
3. Walk the `amends` or `supersedes` lineage backward.
4. Stop after `lookback_sessions` session boundaries, or earlier when the
   requested source budget is exhausted.
5. Return compact artifacts first.
6. Hydrate original source testaments/artifacts only when requested.

The session count is therefore an explicit recall parameter, not something the
agent infers from message count or wall-clock age.

## 12. Skill Shape

The skill names should describe the actual operation: preserving and recalling
testaments/artifacts.

### `carry_forward`

Description:

```text
Carry forward durable testaments and artifacts from this agent's recent work.

Use this at turn and phase boundaries after a useful consult, discovery, or
planning step. The skill reads the latest continuity cursor for the topic,
scans only new board testaments/artifacts since that cursor, selects durable
relevant evidence, and submits a new continuity testament with working-context,
evidence-digest, source-index, and continuity-cursor artifacts.

Do not carry forward claims as evidence. Claims constrain the carry-forward
work; testaments and artifacts are the content being preserved.
```

Parameters:

```text
topic: Stable continuity topic. Required.
session_id: Session scope. Optional when available from context.
plan_id: Plan scope, when known. Optional.
request_correlation_id: Root request correlation, when known. Optional.
agent: Agent whose continuity is being preserved. Defaults to caller.
mode: "advance" | "preview". Default "advance".
max_sources: Maximum source testaments/artifacts to include. Optional.
freshness_horizon: How long the resulting digest remains fresh. Optional.
```

Return:

```json
{
  "topic": "python_cli_planning",
  "continuity_testament_id": "testament_...",
  "from_sequence": 1710,
  "through_sequence": 1842,
  "source_testament_ids": ["testament_..."],
  "source_artifact_ids": ["artifact_..."],
  "carried": true,
  "summary": "..."
}
```

### `recall_forward`

Description:

```text
Recall carried-forward testaments and artifacts for this agent/topic.

Use this before planning, design, or any repeated discovery. If the recalled
evidence is fresh and relevant, reuse it. Do not repeat the underlying consult
or search unless the carried evidence is stale, contradicted, missing, or too
broad for the current decision.

For cross-session recall, prefer the local continuity spine. Walk backward
across at most lookback_sessions session boundaries using session_cursor
artifacts and amends/supersedes relations. Consult Archivalist only when local
continuity is missing, too sparse, cross-agent, or semantic rather than direct
self-history.
```

Parameters:

```text
topic: Stable continuity topic. Required.
session_id: Session scope. Optional when available from context.
plan_id: Plan scope, when known. Optional.
agent: Agent whose carried context should be recalled. Defaults to caller.
lookback_sessions: Number of prior sessions to traverse. Default 0.
max_items: Maximum continuity items to return. Default 8.
include_sources: "digest" | "source_index" | "full". Default "digest".
```

Return:

```json
{
  "topic": "python_cli_planning",
  "continuity_testament_id": "testament_...",
  "fresh": true,
  "working_context": "...",
  "evidence_digest": [],
  "source_testament_ids": ["testament_..."],
  "source_artifact_ids": ["artifact_..."],
  "through_sequence": 1842,
  "sessions_traversed": 1,
  "projection_document_ids": ["claims_testament_testament_..."]
}
```

## 13. Prompt Wording

Planner and agent prompts should make the phase boundary explicit.

```text
Before starting a new planning phase, call recall_forward for the active topic.
Use fresh carried-forward testaments/artifacts before consulting peers or
searching the workspace. If the recalled evidence answers the question, do not
repeat the underlying work.

After a consult, workspace discovery, research step, or planning phase produces
durable findings, call carry_forward. Carry forward testaments and artifacts,
not claims. The carry-forward skill will create the constraining claim and
submit a continuity testament with derived_from relations to the source
testaments/artifacts.
```

Planning protocol wording:

```text
1. recall_forward(topic=<active request/topic>)
2. If recalled evidence is fresh and relevant, use it.
3. If evidence is missing, stale, contradicted, or too broad, perform the
   targeted consult/search.
4. carry_forward(topic=<active request/topic>) after useful evidence appears.
5. Continue analyze/design/generate using the recalled or newly carried
   evidence.
```

## 14. Planning Duplicate Work Fix

For the repeated planning issue:

1. The first phase consults the Librarian and receives useful testaments and
   artifacts.
2. The Architect calls `carry_forward(topic=python_cli_planning)`.
3. The carry-forward testament stores:
   - the repo discovery digest;
   - source testament/artifact IDs;
   - a cursor through the current board sequence.
4. The second planning phase begins with `recall_forward`.
5. The planner sees the repo discovery digest and source IDs.
6. It does not consult the Librarian again unless the digest is stale,
   contradicted, missing, or too broad.

The fix is not "remember the chat text." The fix is to make the Architect
submit and recall continuity testaments that cite the actual testaments and
artifacts from the board.

## 15. Updated Architecture

Carry-forward requires a durable claims board. Scribes, Archivalist, the
document DB, the knowledge graph, Fabric, and Memory Forest all amplify or
index claims-board state, but none of them should be the canonical source of
claims/testaments/artifacts.

The architecture is:

```text
DurableClaimsBoard WAL
  -> claims board projection
  -> claims outbox / sequence tailer
     -> Fabric activity projection
     -> ClaimsKnowledgeMirror
        -> document DB / Bleve
        -> knowledge graph nodes and edges
     -> Memory Forest harvester
     -> Scribe batch observer
        -> Archivalist Entry
        -> Archivalist knowledge mirror
        -> narration_emitted activity
```

The invariant is strict:

> A claim, testament, or artifact is durable only when it has committed to the
> claims board WAL. Every other surface is a replayable projection or an
> agentic interpretation.

### 15.1 Canonical Store

The durable claims board owns:

- actions;
- claims;
- validations;
- testaments;
- artifacts;
- relations;
- board phase;
- board sequence;
- notification/projection error artifacts.

The board must expose a durable owner, not just a raw in-memory board pointer.
Mutating operations must pass through the durable owner so WAL append happens
before mutation:

- `PostAction`;
- `SubmitTestaments`;
- `EvaluateValidation`;
- `RejectClaim`;
- phase transitions;
- board completion.

Returning only `*ClaimsBoard` from a session is insufficient if callers can
mutate it directly and bypass the WAL. The implementation should either expose
a small durable board interface backed by `DurableBoard`, or make the session
root board itself WAL-backed internally.

Read-only operations may use the underlying projection once the durable owner
has applied the mutation.

### 15.2 Deterministic Projections

Deterministic projection is event driven and replayable. It should not require
an LLM, routing through Guide, or a peer consultation. The projector consumes
committed board events in sequence and writes derived state.

Projection outputs:

- Fabric activities for visibility and ambient lenses;
- document DB records for full-text search;
- Bleve indexes for fast keyword/facet lookup;
- knowledge graph nodes for semantic and relational traversal;
- Memory Forest events for precedent recall;
- projection receipts or error artifacts back onto the claims board.

Projection inputs should be the full board entity, not only the thin Fabric
payload. Fabric can locate an entity, but the projector should hydrate the
canonical claim/testament/artifact from the durable board before writing richer
documents or graph edges.

### 15.3 Agentic Projections

Scribes and Archivalist perform semantic compression and narrative curation.
They should observe deterministic projections and board events, then produce
testaments/artifacts of their own.

Agentic outputs include:

- scribe narrations;
- Archivalist entries;
- session summaries;
- handoff context;
- precedent notes;
- carry-forward continuity testaments;
- stale-context warnings;
- curated cross-session summaries.

Agentic outputs must link back to source board entities:

- `derived_from -> <source_testament_id>`;
- `derived_from -> <source_artifact_id>`;
- `amends -> <prior_continuity_testament_id>`;
- metadata fields such as `source_board_id`, `source_sequence`,
  `source_session_id`, and `projection_document_id`.

Scribes and Archivalist amplify meaning. They do not make base claims durable.

### 15.4 Archivalist Integration

Archivalist already owns session archive storage, document mirroring, and access
to committed knowledge search. Carry-forward should use that strength without
turning every claim mutation into an Archivalist LLM task.

The design adds an Archivalist-owned deterministic claims ingestor:

```text
Claim/Testament/Artifact committed
  -> ClaimsKnowledgeMirror maps entity to markdown document
  -> UpsertTextDocument(document_id, path, content, metadata)
  -> knowledge runtime updates document DB, Bleve, embeddings, graph
  -> optional projection_receipt artifact
```

This is not a `consult_peer(archivalist, ...)` flow. It is a direct
deterministic ingest path analogous to `mirrorStoredEntryToKnowledge`, but for
claims-board entities.

Archivalist consultation remains useful when an agent asks:

- "What happened across unrelated sessions?";
- "Find similar prior work semantically.";
- "Synthesize the history of this failure mode.";
- "Compare my direct continuity spine with global knowledge."

It should not be the first mechanism for recalling the agent's own
carried-forward artifacts.

### 15.5 Knowledge Graph and Document DB Integration

Claims, testaments, and artifacts should become first-class knowledge objects.

Document records:

- `claims/<session>/<agent>/<topic>/claim_<id>.md`
- `claims/<session>/<agent>/<topic>/testament_<id>.md`
- `claims/<session>/<agent>/<topic>/artifact_<id>.md`
- `claims/<session>/<agent>/<topic>/continuity_<testament_id>.md`

Document metadata:

```text
entity_type: claim | testament | artifact | continuity
entity_id: <claim/testament/artifact id>
session_id: <session>
board_id: <board>
agent_id: <agent>
topic: <continuity topic, when known>
sequence: <board sequence>
relations: <JSON relation list or normalized relation fields>
claim_id: <claim id, when applicable>
testament_id: <testament id, when applicable>
artifact_kind: <kind, for artifacts>
ephemeral: true | false
source: claims_board
```

Graph nodes:

- `claim:<id>`;
- `testament:<id>`;
- `artifact:<id>`;
- `continuity_topic:<agent>/<topic>`;
- `session:<id>`;
- `board:<id>`.

Graph edges:

- `claim -> testament` from `RelationshipClaim`;
- `testament -> artifact` from structural ownership;
- `continuity -> source` from `derived_from`;
- `continuity -> continuity` from `amends` and `supersedes`;
- `entity -> session`;
- `entity -> board`;
- `entity -> agent`.

The graph and documents are projections. They can be rebuilt from durable board
events.

### 15.6 Fabric and Forest Integration

Fabric remains the observation and coordination layer. It answers questions
such as:

- "What claim/testament/artifact activity happened recently?";
- "Which agent emitted this testament?";
- "Which board/session/topic does this activity belong to?";
- "What activity should a scribe narrate?"

Memory Forest should harvest accepted claims and continuity testaments as
precedents. The higher-value harvest unit is the full chain:

```text
claim -> testament -> artifacts -> validations -> outcome
```

not just a single raw activity. Therefore the Forest harvester should hydrate
from the durable board when the activity indicates a harvest-worthy entity.

### 15.7 Failure Semantics

Durability failures are fatal to the mutation.

Projection failures are non-fatal but visible:

- retry from outbox;
- record projector error metrics;
- attach `projection_error` artifacts when errors affect agent-visible recall;
- never mark a projection receipt successful before the downstream write
  commits.

Agentic failures are non-fatal and should produce ordinary error artifacts or
Archivalist entries:

- scribe narration failed;
- Archivalist ingest unavailable;
- knowledge search unavailable;
- semantic summary refused or timed out.

## 16. Cross-Session Lookback

An agent looking back some number of sessions should not consult Archivalist by
default. It should first use its own direct continuity spine.

### 16.1 Continuity Spine

The continuity spine is a chain of continuity testaments for one `(agent,
topic)` pair.

Each node contains:

- `working_context`;
- `evidence_digest`;
- `source_index`;
- `continuity_cursor`;
- `session_cursor`;
- optional `projection_receipt`;
- `amends` or `supersedes` relation to the previous continuity testament.

The latest node is the entry point. `recall_forward(lookback_sessions=3)` walks
backward until it has crossed three distinct previous session IDs or the chain
ends.

### 16.2 Direct Recall Policy

Use direct recall when:

- the agent asks about its own prior work;
- the topic is stable;
- the needed context is likely in previous carry-forward artifacts;
- the caller wants bounded lookback by session count;
- the caller needs source IDs and artifacts, not a narrative summary.

Use Archivalist or broader knowledge recall when:

- the topic is not known;
- the question is cross-agent;
- the query is semantic rather than lineage-based;
- the continuity spine is missing;
- the requested lookback exceeds the local durable board retention policy;
- the caller needs synthesized history across many sessions.

### 16.3 Recall Return Shape

```json
{
  "topic": "python_cli_planning",
  "agent_id": "architect",
  "lookback_sessions": 2,
  "sessions_traversed": ["session-current", "session-prior"],
  "continuity": [
    {
      "testament_id": "testament_current",
      "session_id": "session-current",
      "through_sequence": 1842,
      "working_context": "...",
      "evidence_digest": [],
      "source_index": [],
      "projection_document_id": "claims_continuity_testament_current"
    }
  ],
  "fallback_used": false,
  "fallback_reason": ""
}
```

### 16.4 Prompt Wording

```text
When you need your own prior work, call recall_forward before consulting
Archivalist. Set lookback_sessions to the number of prior sessions you need.
Use the returned continuity testaments and artifacts as your first source of
truth. Consult Archivalist only if direct continuity is absent, stale,
cross-agent, or too narrow for the question.
```

## 17. Phased Plan

The phases below are ordered so the system becomes correct before it becomes
powerful. Durability comes first. Search, graph, scribe amplification, and
cross-session recall build on top of that durable base.

Every phase item includes unit tests, integration tests with `vektra/mockery`
mocks, and end-to-end tests. End-to-end cases must include happy path,
negative path, race condition, deadlock/shutdown, edge case, replay, and
large-input coverage where applicable.

For every implementation PR, the test table for each phase item must include:

- at least one unit happy-path case;
- at least one unit negative/error case;
- at least one unit edge case;
- at least one integration test whose collaborators are generated with
  `vektra/mockery`;
- at least one integration negative/error case;
- at least one E2E happy-path case;
- at least one E2E negative/error case;
- one race-condition case run under `go test -race` when the item touches
  concurrency, locks, queues, subscriptions, or replay;
- one deadlock/shutdown case when the item touches goroutines, projectors,
  WALs, subscribers, long-running calls, or context cancellation;
- one replay/idempotency case when the item writes durable data or projections;
- one large-input or high-volume case when the item handles artifacts,
  documents, outbox batches, or search results.

If any category is genuinely not applicable, the test plan for that item must
state why and identify the nearest adjacent coverage.

### Phase 1: Durable Session Claims Board

#### 1.1 Replace Session Root Board Construction With a Durable Owner

Purpose:

Session root claims boards must survive process restarts and must be reopenable
for prior-session recall. The current in-memory construction cannot support
cross-session hydration because old board objects disappear when the process
exits.

Design:

- Introduce a session-level durable claims board owner.
- Store each session board under the session directory, for example:
  `.sylk/sessions/<session_id>/claims/session-<session_id>/wal/events.wal.jsonl`.
- The owner exposes mutation methods matching the current claims board API.
- Session code hands agents a mutation-safe interface, not a raw mutable
  in-memory board.
- Read-only methods continue to expose projection and graph traversal.
- The existing board registry stores the durable-backed board view.

Example:

```go
type BoardWriter interface {
    PostAction(context.Context, claims.Action, []claims.Claim) error
    SubmitTestaments(context.Context, claims.Action, []claims.Testament) error
    EvaluateValidation(context.Context, string, string, claims.StatusChange) error
    RejectClaim(context.Context, string, claims.StatusChange, *claims.Action, []claims.Claim) error
}
```

Acceptance criteria:

- Every session root board mutation appends a WAL record before in-memory state
  changes.
- No production code path can mutate the session root board while bypassing the
  durable owner.
- Reopening a session reconstructs actions, claims, testaments, artifacts,
  validations, relations, phase, and sequence.
- Board IDs remain stable across reopen.
- Sequence numbers remain monotonic after replay.
- Existing in-process subscriptions still receive deltas after durable-backed
  mutation.
- A failed WAL append prevents the mutation.
- A successful WAL append followed by process crash replays into the expected
  board state.

Test cases:

- Unit, happy path: `TestSessionDurableBoard_PostActionWritesWALFirst`.
- Unit, artifact path: `TestSessionDurableBoard_SubmitTestamentsWritesArtifactsToWAL`.
- Unit, replay path: `TestSessionDurableBoard_ReopenReplaysClaimsAndTestaments`.
- Unit, edge path: `TestSessionDurableBoard_ReopenPreservesRelationsIndex`.
- Unit, sequence path: `TestSessionDurableBoard_ReopenPreservesSequenceHighWater`.
- Unit, negative path: `TestSessionDurableBoard_WALAppendFailureRejectsMutation`.
- Unit, corruption path: `TestSessionDurableBoard_CorruptTrailingWALEntryReportsRecoverableError`.
- Integration with `vektra/mockery`: mock the WAL appender and assert mutation
  is not called when append fails.
- Integration with `vektra/mockery`: mock the delta bus and assert deltas
  publish after durable mutation.
- Integration with `vektra/mockery`: mock the session manager registry and
  assert the registered board is durable-backed.
- Integration with `vektra/mockery`: mock a subscriber that blocks briefly and
  assert mutation returns within the non-blocking dispatch budget.
- E2E, happy path: start a session, post a claim, submit a testament with
  artifacts, stop the runtime, reopen the session, and recall the testament by
  ID.
- E2E, negative path: simulate WAL directory permission failure and assert the
  user-visible mutation fails rather than producing partial board state.
- E2E, replay path: crash after WAL append and before subscription dispatch;
  reopen and verify board state exists even if no subscriber saw the original
  event.
- E2E, race path: attempt concurrent submissions from multiple agents; verify
  all testaments are present and sequences are unique under `go test -race`.
- E2E, deadlock path: shut down while a write and projection read are in flight;
  assert shutdown drains or cancels cleanly.
- E2E, edge path: create two sessions with the same topic and verify no
  cross-session leakage.

#### 1.2 Add a Board High-Water Read

Purpose:

Carry-forward needs a deterministic scan boundary. Computing the high-water
sequence by walking every object is possible but inefficient and easy to get
wrong when new entity types are added.

Design:

- Add a read-only `HighWaterSequence()` accessor.
- The value returns the latest committed board sequence.
- It is safe to call concurrently with mutations.
- The value reflects replayed WAL state after reopen.

Example:

```go
through := board.HighWaterSequence()
cursor := ContinuityCursor{FromSequence: previous, ThroughSequence: through}
```

Acceptance criteria:

- New boards return zero or the documented initial sequence.
- After each action, claim, testament, artifact, validation, and phase mutation,
  the high-water sequence equals the maximum entity sequence.
- Replayed boards return the same high-water value as before shutdown.
- The accessor does not allocate large projections.
- Concurrent readers never observe a decreasing value.

Test cases:

- Unit, happy path: `TestBoardHighWaterSequence_PostAction`.
- Unit, artifact path: `TestBoardHighWaterSequence_SubmitTestamentsWithArtifacts`.
- Unit, replay path: `TestBoardHighWaterSequence_Replay`.
- Unit, race path: `TestBoardHighWaterSequence_ConcurrentReads`.
- Unit, edge path: `TestBoardHighWaterSequence_EmptyBoard`.
- Integration with `vektra/mockery`: mock a carry-forward scanner and assert it
  receives the high-water value from the accessor, not a projection walk.
- E2E, happy path: run a multi-phase planning flow and assert each continuity
  cursor advances to an observed high-water sequence.
- E2E, race path: submit artifacts while repeatedly reading high-water under
  the race detector.

#### 1.3 Make Durable and Projection Errors Visible

Purpose:

Durability and replay errors must not disappear into logs. Agents need visible
artifacts when projection or replay problems affect recall reliability.

Design:

- Record non-fatal projection errors as board notification errors.
- Convert recall-affecting errors into `projection_error` artifacts.
- Keep fatal WAL errors as returned errors.
- Include source sequence, projector name, entity ID, retry count, and last
  error.
- Deduplicate repeated projector errors for the same `(board, sequence,
  projector)` tuple.

Example:

```json
{
  "kind": "projection_error",
  "reference": "claims_knowledge_mirror failed: committed ingest unavailable",
  "metadata": {
    "projector": "claims_knowledge_mirror",
    "board_id": "session-default",
    "sequence": 1842,
    "entity_id": "testament_...",
    "retry_count": 3
  }
}
```

Acceptance criteria:

- WAL write errors return to caller and do not create fake success artifacts.
- Projection errors do not roll back durable board mutations.
- Repeated projection failures are deduplicated or coalesced to avoid artifact
  spam.
- Recall reports when a continuity testament exists but its document/graph
  projection failed.
- Projection success after failure creates a `projection_receipt` or otherwise
  clears the warning state.

Test cases:

- Unit, negative path: `TestDurableMutation_WALErrorNoProjectionErrorArtifact`.
- Unit, dedupe path: `TestProjectionErrorArtifact_DedupesByEntityAndProjector`.
- Unit, recall path: `TestRecallForward_ReportsProjectionError`.
- Integration with `vektra/mockery`: mock a knowledge writer returning an error
  and assert `projection_error` artifact creation.
- Integration with `vektra/mockery`: mock a retrying projector and assert
  successful retry records a receipt.
- E2E, negative path: disable the knowledge backend, submit continuity, verify
  recall warns about projection failure.
- E2E, recovery path: re-enable the backend, replay the outbox, and verify the
  receipt replaces or amends the error state.
- E2E, deadlock path: projection error recording must not recursively block on
  the same projector worker.

### Phase 2: Claims Outbox and Deterministic Projection Pipeline

#### 2.1 Add a Durable Claims Outbox

Purpose:

Projection should be reliable and replayable. Directly projecting from hot
mutation code makes failures difficult to retry and couples board write latency
to downstream systems.

Design:

- Append an outbox record for every committed board mutation.
- Use `(board_id, sequence, entity_type, entity_id, mutation_kind)` as the
  idempotency key.
- Store projection status per projector:
  `pending`, `in_progress`, `succeeded`, `failed_retryable`,
  `failed_terminal`.
- Projectors claim work with leases to avoid duplicate workers.
- Leases expire so crashed projectors do not permanently block work.
- Scans are bounded and ordered by `(board_id, sequence)`.

Example:

```json
{
  "board_id": "session-default",
  "session_id": "default",
  "sequence": 1842,
  "entity_type": "testament",
  "entity_id": "testament_...",
  "mutation_kind": "testament_submitted",
  "projectors": {
    "fabric": "pending",
    "knowledge": "pending",
    "forest": "pending"
  }
}
```

Acceptance criteria:

- Every durable mutation creates exactly one outbox record per sequence.
- Replaying the WAL does not duplicate outbox records.
- Projector status is idempotent.
- Failed projectors can retry without re-running succeeded projectors unless
  explicitly requested.
- Lease expiration is deterministic and testable.
- Outbox scanning is bounded and paginated.
- Queue pressure never blocks the canonical board mutation after WAL commit.

Test cases:

- Unit, happy path: `TestClaimsOutbox_InsertIdempotent`.
- Unit, replay path: `TestClaimsOutbox_ReplayDoesNotDuplicate`.
- Unit, status path: `TestClaimsOutbox_ProjectorStatusTransitions`.
- Unit, lease path: `TestClaimsOutbox_LeaseExpires`.
- Unit, pagination path: `TestClaimsOutbox_PaginationStableUnderWrites`.
- Integration with `vektra/mockery`: mock two projectors and assert one failure
  does not block the other.
- Integration with `vektra/mockery`: mock a projector crash after lease claim;
  assert a later worker can reclaim.
- Integration with `vektra/mockery`: mock duplicate delivery and assert the
  idempotency key suppresses duplicate writes.
- E2E, happy path: submit many claims/testaments while projectors run
  concurrently and verify all outbox records settle.
- E2E, negative path: kill the runtime mid-projection, restart, and assert all
  pending records eventually reach terminal success or visible failure.
- E2E, race path: run multiple projector workers against the same outbox under
  the race detector and assert no duplicate successful writes.
- E2E, deadlock path: run shutdown while a worker holds a lease and verify
  shutdown either drains or releases cleanly.
- E2E, edge path: create an outbox record for a deleted or missing entity and
  assert terminal failure includes enough diagnostic data.

#### 2.2 Fabric Projection From Outbox

Purpose:

Fabric remains the observation layer and should receive claims-board activities
from committed board events.

Design:

- Move or supplement the current board amplifier with an outbox-backed Fabric
  projector.
- Hydrate the full entity from the durable board.
- Emit Fabric activity with enough metadata to locate the canonical entity:
  `board_id`, `session_id`, `sequence`, `entity_type`, `entity_id`,
  `claim_id`, `testament_id`, `topic`, when available.
- Keep payload compact but more useful than the current thin summary.
- Use stable activity IDs derived from board identity and sequence.

Example:

```json
{
  "action": "testament_submitted",
  "source_table": "claims_board",
  "source_id": "testament_...",
  "subject": {
    "target_artifact": "testament_...",
    "coordinates": {
      "board_id": "session-default",
      "session_id": "default",
      "sequence": "1842",
      "entity_type": "testament",
      "topic": "python_cli_planning"
    }
  }
}
```

Acceptance criteria:

- Fabric activity is emitted only after the board event is durable.
- Activity `SourceTable` and `SourceID` identify the canonical board entity.
- Activity subject coordinates include board and task/session scope.
- Replayed outbox records do not duplicate Fabric activities.
- Existing UI and ambient lenses continue to function.
- Scribe batch observer can identify continuity-relevant activities.

Test cases:

- Unit, happy path: `TestFabricProjector_ClaimIssuedPayload`.
- Unit, testament path: `TestFabricProjector_TestamentSubmittedPayload`.
- Unit, artifact path: `TestFabricProjector_ArtifactPublishedPayload`.
- Unit, idempotency path: `TestFabricProjector_IdempotentActivityID`.
- Unit, edge path: `TestFabricProjector_SkipsEphemeralArtifactWhenPolicySaysSkip`.
- Integration with `vektra/mockery`: mock `activity.Append` or the sink and
  assert exact payload fields.
- Integration with `vektra/mockery`: mock durable board hydration failure and
  assert retryable projection status.
- E2E, happy path: submit a continuity testament and query Fabric by
  topic/agent/session.
- E2E, replay path: restart and replay projection; assert no duplicate
  activity rows.
- E2E, race path: submit artifacts while projector drains and assert stable
  ordering by sequence.
- E2E, deadlock path: block the activity sink and verify outbox backpressure
  does not block board writes indefinitely.

#### 2.3 Claims Knowledge Mirror

Purpose:

The document DB, Bleve, and knowledge graph should index claims/testaments and
artifacts directly. Scribe commentary alone is not enough because it is
narrative and selective, while carry-forward needs exact source IDs and
relations.

Design:

- Add `ClaimsKnowledgeMirror`.
- Consume outbox records for claims, testaments, artifacts, validation verdicts,
  and continuity testaments.
- Hydrate the full entity from the durable board.
- Render deterministic markdown documents.
- Call `UpsertTextDocument` with stable document IDs and paths.
- Write graph node/edge metadata using the same canonical keys.
- Mark success with `projection_receipt` metadata and outbox projector status.

Example:

```markdown
# Testament testament_...

agent_id: architect
session_id: default
board_id: session-default
sequence: 1842
confidence: committed
topic: python_cli_planning
relations: [...]

## Summary

Carried forward Python CLI planning evidence...

## Artifacts

- working_context: ...
- evidence_digest: ...
- source_index: ...
```

Acceptance criteria:

- Claim documents include title, description, scope, validations, relations, and
  status.
- Testament documents include summary, confidence, context, relations, and
  artifact references.
- Artifact documents or attachments include kind, reference, metadata,
  ephemeral flag, parent testament ID, and content hash when available.
- Continuity documents are filterable by agent/topic/session.
- Document IDs are stable across replay.
- Re-ingest replaces the prior document rather than creating duplicates.
- Ephemeral artifacts follow retention/indexing policy.
- Projection errors are retryable and visible.

Test cases:

- Unit, claim rendering: `TestClaimsKnowledgeMirror_RenderClaimDocument`.
- Unit, testament rendering: `TestClaimsKnowledgeMirror_RenderTestamentDocument`.
- Unit, continuity rendering: `TestClaimsKnowledgeMirror_RenderContinuityDocument`.
- Unit, artifact rendering: `TestClaimsKnowledgeMirror_RenderArtifactAttachment`.
- Unit, idempotency: `TestClaimsKnowledgeMirror_StableDocumentID`.
- Unit, metadata: `TestClaimsKnowledgeMirror_MetadataIncludesRelations`.
- Unit, edge path: `TestClaimsKnowledgeMirror_SkipsOrMarksEphemeralArtifacts`.
- Unit, negative path: `TestClaimsKnowledgeMirror_UpsertFailureRetryable`.
- Integration with `vektra/mockery`: mock
  `CommittedKnowledgeWriter.UpsertTextDocument` and assert document ID, path,
  content, doc type, language, domain, and metadata.
- Integration with `vektra/mockery`: mock board hydration and assert the mirror
  never indexes from Fabric payload alone.
- Integration with `vektra/mockery`: mock graph writer and assert relations
  produce typed edges.
- Integration with `vektra/mockery`: mock partial document success and graph
  failure; assert document and graph projector statuses diverge correctly.
- E2E, happy path: submit a claim/testament/artifact, wait for projection,
  search Bleve for testament summary text, and verify returned metadata points
  to the board entity.
- E2E, graph path: query the knowledge graph from continuity testament to
  source artifacts via `derived_from`.
- E2E, replay path: delete derived indexes, replay projections, and verify
  identical documents and graph edges are rebuilt.
- E2E, race path: project many artifacts while new ones arrive and assert
  stable idempotency.
- E2E, deadlock path: stall the committed knowledge backend and verify worker
  cancellation releases locks.
- E2E, edge path: project a very large artifact reference and verify truncation
  policy preserves source identity.

### Phase 3: Carry-Forward Skill Implementation

#### 3.1 Implement `carry_forward`

Purpose:

Agents need a deterministic way to preserve durable testaments/artifacts at
phase and turn boundaries.

Design:

- Register `carry_forward` as a shared skill for agents with claims-board
  access.
- Resolve caller agent, session, board, topic, plan ID, and correlation ID from
  context and parameters.
- Locate the latest continuity testament for `(agent, session, topic)`.
- Read its cursor.
- Scan only new testaments/artifacts after the cursor through the board
  high-water sequence.
- Filter out transient/progress-only evidence.
- Submit a continuity claim if one does not already exist for the topic.
- Submit a continuity testament with `working_context`, `evidence_digest`,
  `source_index`, `continuity_cursor`, and `session_cursor`.
- Add `derived_from` relations to source testaments/artifacts.
- Add `amends` or `supersedes` relation to prior continuity testament.

Example:

```json
{
  "topic": "python_cli_planning",
  "mode": "advance",
  "max_sources": 8,
  "freshness_horizon": "30m"
}
```

Acceptance criteria:

- The skill never treats claims alone as carried evidence.
- The skill advances from the latest cursor, not from message history.
- Re-running over the same window is idempotent.
- The skill can run in `preview` mode without mutating the board.
- The skill can run in `advance` mode and submit exactly one continuity
  testament when useful evidence exists.
- Source selection is bounded by `max_sources`.
- The returned summary includes source IDs and cursor bounds.
- Existing testaments are not modified; corrections produce new testaments.

Test cases:

- Unit, no prior state: `TestCarryForward_NoPriorCursor_UsesInitialAnchor`.
- Unit, cursor path: `TestCarryForward_UsesLatestCursor`.
- Unit, consult path: `TestCarryForward_SelectsConsultTestament`.
- Unit, workspace path: `TestCarryForward_SelectsWorkspaceArtifact`.
- Unit, negative path: `TestCarryForward_ExcludesProgressNarration`.
- Unit, negative path: `TestCarryForward_ExcludesSpinnerStatusArtifacts`.
- Unit, idempotency: `TestCarryForward_IdempotentSameWindow`.
- Unit, preview path: `TestCarryForward_PreviewDoesNotMutate`.
- Unit, write path: `TestCarryForward_AdvanceWritesContinuityArtifacts`.
- Unit, lineage path: `TestCarryForward_AmendsPriorContinuity`.
- Unit, stale path: `TestCarryForward_SupersedesStaleContinuity`.
- Integration with `vektra/mockery`: mock board provider and assert the skill
  calls read APIs before mutation.
- Integration with `vektra/mockery`: mock durable writer and assert submitted
  testament contains required artifact kinds and relations.
- Integration with `vektra/mockery`: mock a source selector and assert duplicate
  source artifacts are not selected.
- Integration with `vektra/mockery`: mock a projection receipt provider and
  assert projected document IDs are included when available.
- E2E, happy path: run a planning phase with Librarian consultation, call
  `carry_forward`, start a second phase, and verify the digest exists.
- E2E, race path: run two concurrent `carry_forward` calls for the same topic
  and assert only one effective continuity node or a deterministic amends chain
  is produced.
- E2E, interrupt path: interrupt carry-forward midway; replay board and outbox
  and verify either no partial mutation exists or the mutation is complete and
  projectable.
- E2E, edge path: carry forward evidence containing a very large artifact and
  verify source index stores references without overflowing skill response
  budgets.
- E2E, deadlock path: block projection receipt lookup and verify the skill can
  still complete the board mutation without waiting forever.

#### 3.2 Implement Source Selection and Evidence Digest Policy

Purpose:

The skill must preserve reusable evidence without turning continuity into a dump
of every event.

Design:

- Classify candidate source testaments/artifacts by kind, confidence, relation,
  action type, artifact kind, freshness, and whether downstream phases are
  likely to reuse them.
- Prefer peer consultation answers, workspace discovery, research findings,
  validated decisions, blockers, and durable file/test evidence.
- Down-rank routine status, progress-only notes, acknowledgement rows,
  duplicated artifacts, and ephemeral operational details.
- Record why each source was selected in `source_index`.

Example source index entry:

```json
{
  "testament_id": "testament_librarian_response",
  "artifact_ids": ["artifact_workspace_read_digest"],
  "agent_id": "librarian",
  "reason": "repo discovery result used by plan design",
  "selected_by": "peer_consult_answer_policy"
}
```

Acceptance criteria:

- Each source has a reason.
- Each digest finding cites at least one source artifact or source testament.
- The policy is deterministic for the same board projection.
- The policy has explicit handling for empty/noisy windows.
- The policy exposes enough metadata for later tuning.
- The policy never includes a source that was already incorporated by the
  latest non-superseded continuity testament unless the new testament marks it
  stale, contradicted, or revalidated.

Test cases:

- Unit, ranking: `TestEvidenceSelector_PrefersPeerAnswerOverToolStatus`.
- Unit, dedupe: `TestEvidenceSelector_DedupesSameArtifactReference`.
- Unit, blocker path: `TestEvidenceSelector_IncludesErrorBlocker`.
- Unit, ephemeral path: `TestEvidenceSelector_ExcludesEphemeralUnlessImportant`.
- Unit, ordering: `TestEvidenceSelector_StableOrdering`.
- Unit, edge path: `TestEvidenceSelector_EmptyWindow`.
- Integration with `vektra/mockery`: mock a board projection containing mixed
  source types and assert exact selected source IDs.
- Integration with `vektra/mockery`: mock stale freshness policy and assert
  supersede/amend recommendation.
- E2E, noisy session: create many progress/status artifacts and a small number
  of useful testaments; assert continuity digest remains compact and useful.
- E2E, race path: add a source artifact while selection is running and assert
  cursor bounds define whether it is included or deferred.

### Phase 4: `recall_forward` and Cross-Session Continuity

#### 4.1 Implement Same-Session Recall

Purpose:

Agents need a cheap first step before repeating work.

Design:

- Locate latest continuity testament for `(agent, session, topic)`.
- Prefer non-superseded testament.
- Return `working_context`, `evidence_digest`, source IDs, cursor, and
  freshness metadata.
- Support `include_sources=digest`, `source_index`, and `full`.
- Report projection lag or projection errors without failing direct board
  recall.

Example:

```json
{
  "topic": "python_cli_planning",
  "include_sources": "source_index"
}
```

Acceptance criteria:

- Recall returns an empty but successful result when no continuity exists.
- Recall prefers latest non-superseded continuity testament.
- Recall reports stale/contradicted status when metadata indicates it.
- `include_sources=full` hydrates source testaments/artifacts from the durable
  board.
- Recall does not consult peers.
- Recall is bounded and does not rescan the whole board when a continuity
  testament exists.

Test cases:

- Unit, empty path: `TestRecallForward_NoContinuity`.
- Unit, latest path: `TestRecallForward_LatestNonSuperseded`.
- Unit, default path: `TestRecallForward_ReturnsDigestOnlyByDefault`.
- Unit, source index path: `TestRecallForward_IncludeSourceIndex`.
- Unit, full hydration path: `TestRecallForward_IncludeFullHydratesSources`.
- Unit, stale path: `TestRecallForward_StaleMetadata`.
- Integration with `vektra/mockery`: mock durable board reader and assert exact
  entity IDs requested.
- Integration with `vektra/mockery`: mock projection document lookup and assert
  document IDs are returned when present.
- E2E, happy path: first phase carries forward, second phase recalls, and
  planner prompt uses recalled context without repeating the consult.
- E2E, negative path: request an unknown topic and verify no peer consult is
  performed.
- E2E, race path: recall while a new continuity testament is being submitted
  and assert either old or new consistent result, never a partial artifact set.
- E2E, deadlock path: recall during session shutdown and verify bounded return.

#### 4.2 Implement Cross-Session Recall

Purpose:

Agents should look back across a bounded number of prior sessions without
paying the cost of an Archivalist consult when direct continuity exists.

Design:

- Add `lookback_sessions`.
- Resolve latest continuity node for `(agent, topic)` from current board,
  Fabric index, or deterministic document metadata.
- Hydrate each continuity testament from its durable board.
- Follow `session_cursor.previous_continuity_testament_id` and
  `amends`/`supersedes` relations.
- Stop at the requested session count.
- Return compact continuity nodes in newest-to-oldest order.
- Report when fallback to Archivalist/knowledge search would be appropriate,
  but do not silently consult unless the skill contract explicitly allows it.

Example:

```json
{
  "topic": "python_cli_planning",
  "lookback_sessions": 2,
  "include_sources": "digest"
}
```

Acceptance criteria:

- `lookback_sessions=0` reads only current session.
- `lookback_sessions=1` may include one prior session boundary.
- Missing prior session board is reported as a partial result, not a hard
  failure when current continuity is usable.
- Broken spine links are reported with source testament ID and session ID.
- Cross-session recall never scans all sessions without a bounded query.
- Results are stable and deterministic.
- Fabric and document DB may locate nodes, but canonical hydration comes from
  the durable board when available.

Test cases:

- Unit, zero lookback: `TestRecallForwardCrossSession_ZeroLookback`.
- Unit, prior session: `TestRecallForwardCrossSession_OnePriorSession`.
- Unit, bounded path: `TestRecallForwardCrossSession_StopsAtLimit`.
- Unit, partial path: `TestRecallForwardCrossSession_MissingPriorBoardPartial`.
- Unit, broken lineage: `TestRecallForwardCrossSession_BrokenAmendsLink`.
- Unit, superseded path: `TestRecallForwardCrossSession_SupersededNodeSkipped`.
- Integration with `vektra/mockery`: mock a session board opener and assert only
  expected session IDs are opened.
- Integration with `vektra/mockery`: mock Fabric index lookup and assert it is
  used only to locate nodes, not as source hydration.
- Integration with `vektra/mockery`: mock Archivalist fallback policy and assert
  no consult occurs by default.
- E2E, happy path: create three sessions, carry forward the same topic in each,
  then recall with lookback `0`, `1`, and `2`.
- E2E, negative path: delete the middle session board and verify partial recall
  plus diagnostic.
- E2E, replay path: rebuild document/graph projections from WAL and verify
  cross-session recall can locate the latest node again.
- E2E, race path: run concurrent session close and cross-session recall; assert
  no deadlock and either pre-close or post-close consistent result.
- E2E, edge path: a continuity node points to a prior session with the same
  topic but different agent; assert recall rejects the cross-agent link unless
  explicitly requested.

#### 4.3 Prompt and Tool-Use Enforcement

Purpose:

The skill only helps if agents call it before repeating work.

Design:

- Update agent prompts to require `recall_forward` before planning/design when a
  stable topic exists.
- Update planning protocol wording to call `carry_forward` after useful
  consult/discovery/research.
- Add evaluator checks that flag repeated consult/search when fresh carried
  evidence was available.
- Keep wording precise: carry testaments/artifacts, not claims.

Example wording:

```text
Before repeating a consult, search, design pass, or plan phase for the same
topic, call recall_forward. If fresh carried testaments/artifacts answer the
question, reuse them and cite their source IDs.
```

Acceptance criteria:

- Architect, Librarian, Academic, Inspector, Tester, Engineer, Designer, Guide,
  and Guardian prompts mention direct carried evidence appropriately.
- Prompts distinguish direct continuity recall from Archivalist consult.
- Prompts do not tell agents to carry claims as evidence.
- Repeated-work evaluator can cite the missed continuity testament.

Test cases:

- Unit, prompt snapshot: required wording appears for each agent.
- Unit, policy path: `recall_forward` is visible to relevant agents.
- Integration with `vektra/mockery`: mock a provider response that attempts
  repeat consult and assert evaluator or steering prompt recommends
  `recall_forward`.
- E2E, happy path: in a two-phase plan, assert the second phase calls
  `recall_forward` before consulting Librarian again.
- E2E, negative path: when recall returns stale/contradicted evidence, assert
  the agent is allowed to consult/search again and then carry forward the new
  evidence.

### Phase 5: Scribe and Archivalist Amplification

#### 5.1 Scribe Observes Claims Continuity Events

Purpose:

Scribes should provide narrative continuity over claim/testament/artifact work
without replacing deterministic durability.

Design:

- Scribe batch observer consumes Fabric activities or outbox notifications for
  continuity-relevant events.
- Scribe narration references claim/testament/artifact IDs explicitly.
- Scribe submits narration testament with `derived_from` relations to source
  continuity testament and key source artifacts.
- Scribe stores commentary through Archivalist as today.

Example narration metadata:

```json
{
  "source_type": "scribe",
  "parent_agent": "architect",
  "topic": "python_cli_planning",
  "source_continuity_testament_id": "testament_...",
  "source_board_id": "session-default"
}
```

Acceptance criteria:

- Scribe narration never creates the only copy of a claim/testament/artifact.
- Narration artifacts include source board IDs and entity IDs.
- Scribe commentary is mirrored to knowledge as a narrative document.
- Scribe failure does not block carry-forward or recall.
- Scribe output can be joined back to source continuity through metadata or
  relations.

Test cases:

- Unit, source link: `TestScribeNarration_IncludesClaimEntityIDs`.
- Unit, lineage: `TestScribeNarration_DerivedFromContinuity`.
- Unit, negative path: `TestScribeNarration_FailureCreatesErrorArtifact`.
- Integration with `vektra/mockery`: mock Archivalist action publish and assert
  metadata includes session, topic, parent agent, and source continuity
  testament.
- Integration with `vektra/mockery`: mock claims board writer and assert
  narration testament relations are present.
- E2E, happy path: produce carry-forward continuity, wait for scribe narration,
  search knowledge for the narrative, and verify it links back to the
  continuity testament.
- E2E, negative path: make Archivalist unavailable and verify direct
  `recall_forward` still works from the board.
- E2E, race path: scribe observes a batch while carry-forward amends the same
  topic; assert narration cites a consistent continuity node.
- E2E, deadlock path: scribe LLM timeout must not block outbox projection or
  board mutation.

#### 5.2 Archivalist Claims Ingest and Query Enrichment

Purpose:

Archivalist should enrich recall with global and semantic context when direct
continuity is insufficient.

Design:

- Add deterministic claims ingest under Archivalist ownership.
- Add query filters for claim/testament/artifact documents by agent, topic,
  session, board, relation, and entity type.
- Allow `recall_forward` to report available Archivalist enrichment without
  invoking an LLM consult.
- Keep explicit `consult_peer(archivalist, ...)` for synthesis tasks.

Example metadata query:

```json
{
  "entity_type": "continuity",
  "agent_id": "architect",
  "topic": "python_cli_planning",
  "limit": 5
}
```

Acceptance criteria:

- Archivalist can query claim/testament documents by metadata.
- Archivalist can search continuity documents semantically.
- Direct recall does not require Archivalist LLM.
- Archivalist consult can cite exact board entity IDs.
- Query enrichment distinguishes deterministic document hits from agentic
  narrative entries.

Test cases:

- Unit, topic query: `TestArchivalistClaimsQuery_ByTopic`.
- Unit, agent query: `TestArchivalistClaimsQuery_ByAgent`.
- Unit, relation query: `TestArchivalistClaimsQuery_ByRelation`.
- Unit, no-LLM path: `TestArchivalistClaimsQuery_NoLLMForMetadataLookup`.
- Integration with `vektra/mockery`: mock committed knowledge backend and assert
  query filters map to metadata.
- Integration with `vektra/mockery`: mock Archivalist consult response and
  assert citations include entity IDs.
- E2E, happy path: carry continuity across sessions, delete local direct index
  but keep committed knowledge, and verify Archivalist metadata query can
  locate documents.
- E2E, semantic path: ask a semantic cross-agent question and verify
  Archivalist consult uses claim/testament documents as evidence.
- E2E, negative path: no matching documents returns empty structured result,
  not fabricated continuity.
- E2E, race path: query while claims ingest is catching up and verify freshness
  metadata reports lag.

### Phase 6: Projection Rebuild, Repair, and Operations

#### 6.1 Projection Rebuild Command

Purpose:

Because document DB, graph, Fabric, and Forest are projections, operators need a
way to rebuild them from durable claims board WALs.

Design:

- Add a rebuild command or internal maintenance skill.
- Select sessions, boards, projectors, sequence ranges, and dry-run mode.
- Clear or replace derived records by stable document/activity/event IDs.
- Emit rebuild report as a claims testament or Archivalist entry.
- Resume from last successful sequence when interrupted.

Example:

```text
rebuild_claims_projections(session_id=default, projectors=["knowledge","forest"], from_sequence=1700, dry_run=false)
```

Acceptance criteria:

- Rebuild can target one board, one session, all sessions, or a sequence range.
- Rebuild is idempotent.
- Dry run reports expected work without writing.
- Rebuild does not mutate canonical board entities except optional repair
  receipts.
- Rebuild can resume after interruption.
- Rebuild reports stale or missing boards explicitly.

Test cases:

- Unit, dry run: `TestProjectionRebuild_DryRun`.
- Unit, idempotency: `TestProjectionRebuild_Idempotent`.
- Unit, range: `TestProjectionRebuild_SequenceRange`.
- Unit, resume: `TestProjectionRebuild_ResumeAfterInterruption`.
- Integration with `vektra/mockery`: mock projectors and assert requested
  projector set is honored.
- Integration with `vektra/mockery`: mock document DB and graph stores and
  assert stable IDs are reused.
- E2E, happy path: delete derived indexes, run rebuild, and verify
  recall/search works again.
- E2E, interrupt path: interrupt rebuild midway and resume.
- E2E, race path: run rebuild concurrently with live board writes and verify no
  duplicated projection rows.
- E2E, deadlock path: rebuild should not hold a board read lock while waiting
  on slow external projection writes.
- E2E, edge path: rebuild a session containing legacy or partially corrupt WAL
  entries and verify diagnostics identify skipped records.

#### 6.2 Monitoring and Backpressure

Purpose:

Projection and scribe pipelines must be observable. Slow projectors should not
silently degrade recall.

Design:

- Track outbox lag by projector and board.
- Track retry counts, terminal failures, lease expirations, queue depth, and
  average projection latency.
- Surface recall warnings when deterministic projection is behind.
- Provide operator diagnostics in the TUI or health history.

Example health row:

```json
{
  "projector": "claims_knowledge_mirror",
  "board_id": "session-default",
  "pending": 14,
  "oldest_pending_sequence": 1820,
  "last_error": "committed ingest unavailable"
}
```

Acceptance criteria:

- Metrics identify which projector is lagging.
- Recall reports when using board data while projection is stale.
- Queue pressure does not block board writes.
- Terminal projection failures produce visible artifacts or health entries.
- Metrics are bounded and do not grow per entity without retention.

Test cases:

- Unit, lag metric: `TestProjectionMetrics_Lag`.
- Unit, retry metric: `TestProjectionMetrics_RetryCount`.
- Unit, recall warning: `TestRecallForward_ProjectionLagWarning`.
- Integration with `vektra/mockery`: mock metrics sink and assert expected
  counters/gauges.
- Integration with `vektra/mockery`: mock a saturated projector queue and assert
  board mutation still succeeds.
- E2E, lag path: artificially slow the knowledge mirror and verify
  carry-forward works while recall reports projection lag.
- E2E, failure path: cause repeated projection failure and verify health
  diagnostics include projector, board, sequence, entity ID, and last error.
- E2E, race path: metrics scraping during projection churn does not race.
- E2E, deadlock path: health rendering must not wait on projector worker locks.

### Phase 7: Migration and Compatibility

#### 7.1 Existing Session Compatibility

Purpose:

The system must handle sessions created before durable claims boards and before
continuity testaments existed.

Design:

- Detect legacy sessions with no durable board WAL.
- Allow recall to return empty direct continuity plus an Archivalist/knowledge
  fallback recommendation.
- Optionally synthesize a first continuity testament from existing scribe
  narrations and Archivalist entries, but mark it as reconstructed.
- Do not pretend reconstructed continuity has exact source artifacts unless the
  source entity IDs are known.

Example reconstructed marker:

```json
{
  "kind": "continuity_cursor",
  "metadata": {
    "reconstructed": true,
    "source": "archivalist_scribe_entries",
    "exact_claim_sources": false
  }
}
```

Acceptance criteria:

- Legacy sessions do not crash `recall_forward`.
- Reconstructed continuity is clearly marked.
- Reconstructed continuity never fabricates source testament IDs.
- New durable sessions interoperate with old Archivalist entries.
- Users and agents can tell exact board continuity from reconstructed narrative
  continuity.

Test cases:

- Unit, missing board: `TestLegacyRecall_NoDurableBoard`.
- Unit, reconstruction: `TestLegacyRecall_ReconstructedContinuityMarked`.
- Unit, source integrity: `TestLegacyRecall_DoesNotFabricateSources`.
- Integration with `vektra/mockery`: mock Archivalist entries with and without
  source entity IDs.
- Integration with `vektra/mockery`: mock knowledge search fallback and assert
  recommendation text is precise.
- E2E, legacy only: run recall in a workspace with old sessions only.
- E2E, mixed mode: create a new durable session after legacy sessions and verify
  the new continuity spine starts cleanly while fallback can still find old
  narrative memory.
- E2E, negative path: corrupted legacy metadata returns diagnostics rather than
  reconstructed source IDs.

#### 7.2 Rollout Gates

Purpose:

Durability and projection changes are foundational. Rollout should be guarded
and reversible.

Design:

- Add feature flags for durable session boards, outbox projectors,
  claims-knowledge mirror, cross-session recall, and scribe continuity
  narration.
- Default to dual-write or shadow projection before making deterministic
  projection authoritative for recall warnings.
- Provide a rollback path that preserves WAL files.

Example flags:

```text
SYLK_DURABLE_SESSION_CLAIMS=1
SYLK_CLAIMS_OUTBOX=1
SYLK_CLAIMS_KNOWLEDGE_MIRROR=shadow
SYLK_RECALL_FORWARD_CROSS_SESSION=0
```

Acceptance criteria:

- Each feature can be enabled independently in tests.
- Disabling projectors does not disable canonical board durability.
- Rollback does not delete WALs or projected documents.
- Shadow mode reports diffs between old and new projection paths.
- Flags are included in diagnostic output.

Test cases:

- Unit, board only: `TestFeatureFlags_DurableBoardOnly`.
- Unit, projector disabled: `TestFeatureFlags_ProjectorsDisabled`.
- Unit, shadow path: `TestFeatureFlags_ShadowProjectionDiff`.
- Integration with `vektra/mockery`: mock config provider and assert each flag
  changes only its intended path.
- E2E, board only: run with durability on and projectors off; recall from board
  works.
- E2E, catch-up: enable projectors mid-session; outbox catches up.
- E2E, rollback: disable projectors after failures; board writes continue.
- E2E, race path: toggle shadow projection during high write volume and assert
  no duplicate canonical mutations.

## 18. Acceptance Matrix

The implementation is complete only when all of these system-level criteria are
true:

- A claim/testament/artifact survives restart because it was written to the
  durable claims board WAL.
- A continuity testament can be recalled in the same session without consulting
  peers or Archivalist.
- A continuity testament can be recalled across bounded prior sessions by
  walking `session_cursor` and `amends`/`supersedes`.
- Document DB and knowledge graph projections can be rebuilt from durable board
  events.
- Scribe and Archivalist outputs link back to exact source board entities.
- Projection failures are visible and retryable.
- Agent prompts require recall before repeated work.
- Repeated planning phases reuse carried-forward evidence instead of repeating
  consults/searches when fresh evidence exists.
- Tests cover happy paths, negative paths, replay, race, deadlock, corrupted
  WAL, missing projection, broken lineage, large artifacts, legacy sessions,
  and concurrent writers.

## 19. Remaining Design Decisions

- Whether the session root board should expose a new durable interface or make
  the current board type WAL-backed internally.
- Whether continuity claims should use `ActionTypeArchival`,
  `ActionTypeTestament`, or a new action type such as `ActionTypeContinuity`.
- Whether no-op cursor advancement should be allowed.
- Whether freshness belongs only in artifact metadata or should also be
  reflected in claim validations.
- Whether reconstructed legacy continuity should be automated or only produced
  on explicit user/agent request.
- How long deterministic projections may lag before recall warns by default.
- Which artifacts should become full documents versus attachments versus graph
  metadata only.
- How aggressively to supersede old continuity testaments versus amending them.
