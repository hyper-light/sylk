# CLAIMS_UI: Claims-Driven Responsive UI Architecture

## Architectural goal

Claims is the single substrate for backend correctness AND UI rendering. There
is no separate UI signal pipeline. There is no `GuideBridge` consuming bus
streams. There is no `agent_state` parallel transport. There are no legacy
non-claims display writes.

The UI participates on the claims plane as a subscriber — same primitives,
same topics, same delta shapes as every other agent. Agents narrate their own
progress explicitly from their tool loops; the relation graph carries
parent-child nesting; `Context` fields on Claim and Testament carry mutable
narrative; `agent_state` artifacts carry the immutable history.

This delivers what every prior approach has missed:
1. **One source of truth** — every UI row corresponds to a board entity.
2. **Replay correctness** — restart a session, replay claims, UI rebuilds.
3. **Audit / time-travel** — every "what was on screen at T?" is queryable.
4. **Async-by-design with deterministic LLM-POV** — agents yield at the
   runtime layer; the UI sees continuous progress via `Context` updates.
5. **No special UI plumbing** — same skills/relations/roles abstractions
   cover both peer agents and the human-facing UI.

## Why this design

### Why `Context` on Claim AND Testament (not just one)

They answer different questions about the same moment:

- **Claim `Context`** — "what is this claim's owner *doing right now*?"
  Mutable narrative of in-progress work. Updates throughout the lifecycle.
  Examples: architect's planning claim → "Mapping out dependencies" →
  "Awaiting librarian response" → "Generating tasks". Sealed only on
  terminal status transition.

- **Testament `Context`** — "what is this testament *concluding*?"
  Mutable narrative of synthesis-in-progress; immutable on flush. The
  developing-conclusion view, distinct from activity. Examples: librarian's
  response testament → "Drafting response: 3 prior CLI patterns" →
  "Highlighting argparse + click trade-off" → "Final: recommend argparse
  with structured logging caveat".

Both are needed because a row representing an open claim displays activity
narration, while a row representing the testament being assembled displays
the developing conclusion. On testament flush, the conclusion seals while
the claim continues (or transitions terminal).

### Why `agent_state` artifacts complement Context

`Context` answers "what's now"; artifacts answer "what was the trace". A
`Context` field overwrites; an artifact appends. We need both:

- Context alone loses history — only ever displays the current value.
- Artifacts alone force the UI to scan-for-latest on every render — wasteful
  and racy.
- Together: Context is the cheap O(1) "what's happening now" handle;
  artifacts are the structured immutable trace for replay/audit/timeline.

### Why owner/target IDs + relations carry nesting (not a parallel `BranchRef` field)

Claims already encode the structural truth. Adding `ParentCorrelationID` as
a separate field on UI messages would duplicate what `RelationshipCausedBy`,
`RelationshipClaim`, `RelationshipDependsOn`, and the issuer/subject pair
already say. The bridge resolves nesting by walking the relation graph the
same way an audit agent or query skill would.

### Why agents push narration explicitly (not implicit instrumentation)

Implicit instrumentation (TestamentAccumulator capturing artifacts
side-effect-style) gives a *delayed* picture — the UI sees nothing until
flush. Explicit push from the agent's tool loop gives a *continuous*
picture — every transition is announced the moment it happens. This is the
discipline sylk-clone uses (`publishAgentState`, `EmitToolCall`) and the
discipline that produces a responsive UI.

The runtime cannot capture everything implicitly because:
- Some transitions are agent-internal (between LLM turns, during synthesis).
- The Detail text is human-readable narrative the agent must compose.
- The categorical state (Reasoning vs ToolExecuting vs DispatchingToPeer)
  depends on the agent's own knowledge of what it's about to do.

## Substrate enrichment (Phase 1)

### 1.1 — `Context string` on Claim and Testament

`core/claims/types.go`. Add `Context string` to both struct types. Document
the semantics. Wire serialization.

### 1.2 — `ArtifactKindAgentState = "agent_state"`

`core/claims/types.go`. New artifact kind. Reference = human-readable
status. Metadata carries categorical `state`, `peer_agent_type`,
`peer_correlation_id`, `at`. Ephemeral=false (durable trace).

### 1.3 — Board mutation API

`core/claims/board.go`:

```go
func (b *ClaimsBoard) SetClaimContext(ctx context.Context, claimID, value string) error
func (b *ClaimsBoard) SetTestamentContext(ctx context.Context, testamentID, value string) error
```

Each updates the field, invalidates projection cache, calls the amplifier.

### 1.4 — Bus deltas

`core/claims/deltas.go`:

```go
type ClaimContextDelta struct {
    ClaimID       string
    SessionID     string
    AgentID       string
    Context       string
    EmittedAt     time.Time
    TransitionID  int64  // monotonic per-claim ordering
}

type TestamentContextDelta struct {
    TestamentID   string  // empty until flush
    AccumulatorID string  // stable across the request
    SessionID     string
    AgentID       string
    Context       string
    EmittedAt     time.Time
    TransitionID  int64
}
```

`core/claims/board_amplifier.go`:

```go
func (a *BoardAmplifier) PublishClaimContextDelta(ctx context.Context, delta ClaimContextDelta)
func (a *BoardAmplifier) PublishTestamentContextDelta(ctx context.Context, delta TestamentContextDelta)
```

`core/claims/topics.go`:

```go
func ClaimContextTopic(sessionID, claimID string) string
func TestamentContextTopic(sessionID, testamentID string) string
func ClaimContextPattern(sessionFilter, claimFilter string) string
func TestamentContextPattern(sessionFilter, testamentFilter string) string
```

Per-claim and per-testament `TransitionID` counters on the board.

### 1.5 — Accumulator Context

`core/claims/testament_accumulator.go`:

```go
func (a *TestamentAccumulator) SetContext(ctx context.Context, value string)
```

Updates accumulator-local Context (`atomic.Pointer[string]`), emits
`TestamentContextDelta` keyed by accumulator ID. On `Flush`:
1. Final Context value seals onto submitted Testament.
2. Final `TestamentContextDelta` emitted with both `AccumulatorID` and
   real `TestamentID` so the UI can rebind row from synthetic anchor.

### 1.6 — Inbox patterns + `RoleObserver`

`core/claims/inbox.go`:

```go
RoleObserver  // wildcard "see everything in this session for rendering"
```

`InboxPatternsFor(RoleObserver, sessionID, agentID)` returns:
- `ClaimContextPattern(sessionID, "*")`
- `TestamentContextPattern(sessionID, "*")`
- `ClaimStatusPattern(sessionID, "*")` for every status
- `AgentInboxActionPattern(sessionID, "*", "*", "*")` for every directed claim
- The amplifier's `BoardMutationDelta` topic

`matchesStandingSubscription` extended to admit `ClaimContextDelta` and
`TestamentContextDelta` for `RoleObserver`.

## Agent narration discipline (Phase 2)

### 2.1 — Canonical state taxonomy

`agents/shared/agent_state.go`:

```go
type AgentActivityState string

const (
    AgentStateReasoning              AgentActivityState = "reasoning"
    AgentStateToolExecuting          AgentActivityState = "tool_executing"
    AgentStateDispatchingToPeer      AgentActivityState = "dispatching_to_peer"
    AgentStateAwaitingPeerResponse   AgentActivityState = "awaiting_peer_response"
    AgentStateConsultingPeer         AgentActivityState = "consulting_peer"
    AgentStateChallengingPeer        AgentActivityState = "challenging_peer"
    AgentStateAwaitingGuardian       AgentActivityState = "awaiting_guardian"
    AgentStateReceiving              AgentActivityState = "receiving"
    AgentStateSynthesizing           AgentActivityState = "synthesizing"
    AgentStateComplete               AgentActivityState = "complete"
    AgentStateErrored                AgentActivityState = "errored"
)

type PeerRef struct {
    AgentType     string
    CorrelationID string
    ClaimID       string
}
```

### 2.2 — `RecordAgentState` helper

`agents/shared/agent_state.go`:

```go
func RecordAgentState(
    ctx context.Context,
    board *claims.ClaimsBoard,
    claimID string,
    detail string,
    state AgentActivityState,
    peer *PeerRef,
)
```

Single call point per state transition. Writes both surfaces:
1. `board.SetClaimContext(ctx, claimID, detail)` — mutable narrative.
2. `acc.RecordArtifact(...)` with `Kind=ArtifactKindAgentState`,
   `Reference=detail`, metadata carrying `state`, `peer_agent_type`,
   `peer_correlation_id`, `at`. Ephemeral=false.

### 2.3 — Push points per agent

Every agent's tool loop pushes at:

| Transition | State | Detail example |
|---|---|---|
| Entry into processClaimsEntry / handleBusRequest | Reasoning | "Acknowledging request" |
| Before each tool call | ToolExecuting | "<tool name>: <summary>" |
| Before consult_peer dispatch | DispatchingToPeer | "Dispatching to librarian" |
| During consult wait | AwaitingPeerResponse | "Awaiting librarian response" |
| Before challenge_peer dispatch | ChallengingPeer | "Challenging tester" |
| Before guardian-check | AwaitingGuardian | "Awaiting guardian for file_edit" |
| On consult/challenge/guardian resume | Receiving | "Received from librarian" |
| Final synthesis phase | Synthesizing | "Composing response" |
| Terminal completion | Complete | "Response complete" |
| Error | Errored | error summary |

Wire these into:
- `agents/architect/planner_conversation.go` (compose_with_tools turns)
- `agents/architect/protocol_runtime.go` (planning protocol turns)
- `agents/architect/tool_loop.go` (tool dispatch wrapping)
- Each consultee's `processClaimsEntry` (`librarian`, `archivalist`,
  `academic`, `architect`, `engineer`, `designer`, `inspector/*`,
  `tester/*`)
- `agents/orchestrator/llm_loop.go` and `consultation_bus.go`
- `agents/guardian/conversation.go`

### 2.4 — Thinking watchdog port

`agents/shared/thinking_watchdog.go`:

```go
func StartThinkingWatchdog(ctx context.Context, board, claimID string, agentID string) (cancel func())
```

Auto-fires `Reasoning` periodically (default 5s) if no transition has
fired within the window. Hooked into each agent's request handler via a
deferred `cancel` returned at start.

Port from sylk-clone's `agents/shared/thinking_watchdog.go`.

### 2.5 — Testament Context narration

Every agent that produces a non-trivial testament calls
`acc.SetContext(ctx, ...)` at synthesis points:

- `acc.SetContext(ctx, "Drafting response: 3 prior CLI patterns relevant")`
- `acc.SetContext(ctx, "Highlighting argparse + click trade-off")`
- `acc.SetContext(ctx, "Recommend argparse with structured logging caveat")`

Final value seals on `Flush`.

## Relation-graph-driven nesting (Phase 3)

### 3.1 — Bridge: deeper relation walk

`ui/bridge/claims.go` `routeArtifactLocked`. When emitting
`ClaimArtifactAddedMsg.ParentRowID`:

1. Direct lookup: `claimToInvocationArtifact[claimID]`.
2. Fallback: walk `RelationshipCausedBy` ancestors via projection until a
   match. Cache hits per session for O(1) repeats.
3. Also resolve `RelationshipDependsOn` artifact references via the same
   walk.

### 3.2 — Owner/target attribution on every UI message

Every `ClaimsAgentStatusMsg`, `ClaimArtifactAddedMsg`,
`ClaimArtifactCompletedMsg`, `ClaimContextUpdatedMsg`,
`TestamentContextUpdatedMsg` carries:

- `OwnerAgentID` — the cycle owner per `cycleOwnerFor`
- `OwnerAgentType` — slug derived from owner ID
- `TargetAgentID` — claim subject
- `TargetAgentType` — slug derived from target ID

Resolved from the source claim's relations. No UUID-literal fallbacks.

### 3.3 — Chat panel `claimRows` index

`ui/chat/model.go`:

```go
type Model struct {
    // ...
    claimRows       map[string]rowID   // claim ID → rendered row
    accumulatorRows map[string]rowID   // in-flight testament accumulator → row
    testamentRows   map[string]rowID   // submitted testament ID → row
}
```

Behaviors:
- `ClaimArtifactAddedMsg` with `ParentRowID != ""` → child row indented
  under that artifact's row. Register in `claimRows[ClaimID] = rowID`.
- `ClaimArtifactAddedMsg` whose `ClaimID` matches `claimRows` → render as
  further-nested child of registered row.
- `ClaimContextUpdatedMsg` whose `ClaimID` matches `claimRows` → update
  that row's `ThinkingStatus` text in place. No new row.
- `ClaimArtifactCompletedMsg` → flip terminal visual on row matched by
  `StartArtifactID`.

### 3.4 — Chat panel testament-in-flight rendering

Behaviors:
- `TestamentContextUpdatedMsg` with only `AccumulatorID` set → create or
  update an in-flight testament row under the appropriate parent (looked
  up via the accumulator's bound claim → `claimRows`).
- Subsequent delta with `TestamentID` filled → rebind the row from
  `accumulatorRows[AccumulatorID]` → `testamentRows[TestamentID]`.

### 3.5 — Agent panel TaskSummary from Context

`ui/agent/model.go` adds `handleClaimContextUpdated` and
`handleTestamentContextUpdated`. For an agent's currently-active claim
(matched by `OwnerAgentID == agent.ID`), update that row's `TaskSummary`
in place from the message's Context field.

This replaces the legacy non-claims display writes (already removed in
prior work) with the claims-driven equivalent.

## UI as a claims participant (Phase 4)

### 4.1 — UI registers a `ClaimsInbox`

`ui/bridge/claims.go` refactor: instead of subscribing ad-hoc to
`BoardMutationDelta`, the UI uses `shared.WireClaimsIntake`:

```go
inbox := shared.WireClaimsIntake(shared.ClaimsIntakeConfig{
    AgentID:      "tui",
    SessionID:    sessionID,
    Role:         claims.RoleObserver | claims.RoleAuditor,
    Bus:          bus,
    Board:        board,
    Scope:        scope,
    ProcessEntry: b.processClaimsEntry,  // converts deltas → tea.Msg
    Identity:     nil,  // UI doesn't have an agent identity
    Factory:      nil,
})
```

`ProcessEntry` is the function that, given a `claims.GraphEntryPoint`,
emits the appropriate Bubble Tea messages and sends them to the program.
This replaces the bridge's current direct subscription model.

### 4.2 — `RoleObserver` definition

Already designed in Phase 1.6. Subscribes to wildcard patterns for
context deltas, claim status, every directed inbox delta, and the board
mutation delta firehose.

### 4.3 — UI publishes back via the claims plane

UI emissions are claims:
- User prompt → `postUserPromptAction` (already exists, posts
  `ActionTypePrompt` with issuer="guide", subject=target).
- User accept/modify/reject on a plan → architect's self-corrective
  claims (per the plan-decision flow: architect issues
  `ActionTypeCorrective` with `plan_modification` or `plan_abandon`
  artifact when the user's decision arrives via bus from the UI).
- Session switch → `ActionTypeBoot` or `ActionTypeActivation` claim.
- Interrupt → `ActionTypeCorrective` claim with cancel scope.

### 4.4 — Bridge dissolves

`ui/bridge/claims.go` shrinks to:
- `ClaimsBridge` struct holds the inbox, the cycle resolver, the row
  caches.
- `Start` calls `WireClaimsIntake` and `inbox.Start()`.
- `Stop` calls `inbox.Close()`.
- `processClaimsEntry` is the `ProcessEntry` callback — converts
  `GraphEntryPoint` to `tea.Msg`s and sends.

The cycle resolver becomes an internal helper. `claimToInvocationArtifact`
becomes an internal index.

## Yield-resume completion (Phase 5)

### 5.1 — Consult yield-resume verification

Already mostly done. Verify `RouteSync` actually returns when the
consultee responds (the architect-ends-with-no-output bug we identified).
Trace via lifecycle log.

On resume: emit `Receiving` state push with detail "Received from
`<peer>`"; inject the consultee's testament summary as the consult_peer
tool's tool result. The LLM never sees a ticket.

### 5.2 — Challenge yield-resume

`agents/shared/cross_pipeline_skills.go challengePeerSkill` currently
returns a result map immediately. Refactor:

1. Post challenge claim; capture claim ID.
2. Emit `EmitPeerInteractionStarted(ctx, PeerInteractionKindChallenge,
   ...)` (already does).
3. Emit `ChallengingPeer` state push: `RecordAgentState(...)`.
4. Yield via `ContinuationStore.AwaitConsultsOrYield` with the challenge
   ID treated as a consult ID for resume routing.
5. Resume injects the challenged peer's testament summary as the
   challenge_peer tool's result.

### 5.3 — Guardian-check runtime-layer yield-resume

`core/toolruntime/runtime.go obtainGuardianGrant` becomes async:

1. Post guardian-check claim with subject=guardian; capture claim ID.
2. Emit `AwaitingGuardian` state push on the calling agent's claim.
3. Stamp `parent_claim_id` on grant request envelope (Layer 3 propagation).
4. Yield the calling agent's tool runtime via
   `ContinuationStore.AwaitConsultsOrYield`.
5. On guardian's testament arrival, resume; the grant verdict drives
   whether the gated tool fires or returns a denial as the tool result.

Guardian's `processClaimsEntry` (already wired via
`NewClaimsEntryAccumulator`) binds testament to the guardian-check claim
→ bridge nests guardian's own tool tree under the calling agent's
`guardian_check_started` row.

## Legacy cleanup (Phase 6)

### 6.1 — Remove legacy display-state writes from agent panel

Already done in prior work. Verify `ActivityEventMsg`,
`StreamProgressMsg`, `StreamStartMsg`, `StreamCompleteMsg`,
`ToolCallEventMsg` handlers in `ui/agent/model.go` no longer write
display state. Replica counts and context usage migrate to claim
artifact metadata in a follow-up pass.

### 6.2 — Remove legacy display-state writes from chat panel

`ui/chat/model.go` consumers of stream events become no-ops or get
removed. Chat panel state is exclusively driven by claims-derived
messages.

### 6.3 — Remove unused/replaced infrastructure

- `agents/shared/await_consults_skill.go` — already deregistered, remove
  the type entirely.
- `runLegacyConsultWait` — once every agent has `ContinuationStore +
  WithTurnContext` wired (already true), the legacy fallback is
  unreachable; delete.
- `ui/bridge/guide.go` — verify nowhere instantiated; delete.
- Stream-event publishing in agents that no longer drives any UI
  consumer — review per call site.

### 6.4 — Remove diagnostic instrumentation

- `// DIAG-1` lines (TaskSummary writer logging) — remove.
- `// DEBUG-storm` lines — remove.

## Validation (Phase 7)

### 7.1 — Bridge integration tests

- Architect consults librarian → bridge emits nested
  `ClaimArtifactAddedMsg` for librarian's tool calls under architect's
  `consult_started`.
- Agent calls `RecordAgentState` → bridge emits
  `ClaimContextUpdatedMsg` with proper attribution; subsequent calls
  update existing UI row, don't create new rows.
- Testament accumulator `SetContext` mid-flight → bridge emits
  `TestamentContextUpdatedMsg` with `AccumulatorID`; on `Flush` →
  final delta with `TestamentID` and same `AccumulatorID`.
- `RoleObserver` subscription delivers wildcard claim/testament context
  deltas.

### 7.2 — End-to-end scenario tests

- "User prompt → architect plans → consults librarian → librarian's tool
  calls nest under consult row → architect resumes → architect
  dispatches to orchestrator → orchestrator ingests deterministically →
  execution shown."
- "Plan rejection: architect emits self-corrective claim, drops cached
  prepared state at orchestrator (via testament observation)."
- "Architect's plan-finalize testament + plan_handoff_payload artifact
  → orchestrator's deterministic ingest from handoff claim with
  depends_on artifact reference."

### 7.3 — Replay test

Start a session, drive a complex consult chain, snapshot the board,
restart, verify the UI rebuilds the entire chat tree with correct
nesting from board projection alone.

## Implementation ordering

| Day | Phase | Output |
|---|---|---|
| 1 | 1.1–1.5 | Substrate: Context fields, agent_state kind, mutation API, deltas, accumulator updates |
| 2 | 1.6 + 2.1–2.2 | Inbox patterns + RoleObserver; canonical state taxonomy; RecordAgentState helper |
| 3 | 2.3 (architect, librarian, orchestrator), 2.4 watchdog | Top three agents narrate; watchdog auto-fills silence |
| 4 | 2.3 (rest of agents), 2.5 testament Context | All 14 agents narrating; testament Context at synthesis points |
| 5 | 3.1–3.5 | Bridge resolves nesting via relation graph; chat + agent panels render nested + update in place |
| 6 | 4.1–4.4 | UI is a registered claims participant; bridge dissolves into inbox handler |
| 7 | 5.1–5.2 | Consult + challenge yield-resume verified end-to-end |
| 8 | 5.3 | Guardian-check runtime-layer yield-resume |
| 9 | 6.1–6.4 | Legacy cleanup |
| 10 | 7.1–7.3 | Test sweep + replay validation |

## Outcome

- Every UI row (chat tree + agent panel) flows from
  claims/testaments/artifacts/contexts.
- Every nesting decision flows from the relation graph —
  owner/target/caused_by/handoff_from/depends_on.
- Every status update flows from explicit agent push, with watchdog
  backup.
- The UI is a claims participant — registers like any agent, subscribes
  to topics, processes deltas.
- Sessions replay correctly from the durable board.
- Audit answers "what did the architect see at T?" by walking artifacts
  and contexts.
- The architect's "ends with no output" failure becomes impossible —
  narration is mandatory at every transition; if anything goes silent,
  the watchdog fills it.
