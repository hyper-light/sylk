# UI Design — Cycles, Handoffs, and the Claims-Driven Activity Tree

## 1. Motivation

The terminal UI's left panel (agent list) and center panel (chat / activity tree) used to render agent activity using ad-hoc correlation IDs threaded through tool calls, LLM dispatches, and child-agent invocations. After the migration to the claims-based execution model (`docs/CLAIMS.md`), the correlation-ID plumbing was unwound, but the equivalent claims-driven attribution was never fully wired in. The result is two reproducible bug classes:

1. **Wrong activity indication.** The left panel's "active" icon flips on for activity that isn't true top-level work (a guardian check inbound to an idle agent, a stale stream chunk, a tool failure with no surrounding cycle), and conversely fails to flip off cleanly because completion correlation drifts.
2. **Lost child-agent + tool-call tree.** The chat panel still has the rendering capability (it draws indented `└─` children under tool-call rows for consults, challenges, and guardian checks), but the system feeding it no longer provides per-row parent attribution. So the tree collapses into a flat sequence and tool calls frequently appear stuck "running" because their completion event never matches their start.

The user-visible target is the rendering contract already supported by `ui/chat/`: top-level agent rows for active work, indented child-agent blocks under the tool call or claim that invoked them, paired start/complete states for every tool call, and a clean handoff that opens a *new* top-level row rather than nesting.

The architectural rule, which this doc operationalizes: **the backend owns parent/child semantics. The UI just renders.** The chat panel must never walk Relations, never decide what counts as a child, never compute cycle membership. It receives messages whose fields already encode the answer.

---

## 2. Concepts

### 2.1 Cycle

A **cycle** is the contiguous span where a single agent owns top-level work. Cycles are the unit the left panel lists ("active agents") and the unit the chat panel uses as the root of a tree ("top-level agent header").

A cycle:
- **Opens** when an agent acquires top-level responsibility — either by accepting a fresh top-level claim (no parent within an active cycle) or by being the successor of a `handoff` action.
- **Stays open** while the owner has any open subject claim (work directed at it) or any in-flight transitively-caused child work (consults, challenges, guardian checks, tool calls anywhere in the subtree).
- **Closes** under one of two terminal conditions:
  - **Drain**: the owner has no remaining open subject claims AND every started tool/child artifact in its transitive subtree has been matched by a completion artifact.
  - **Handoff**: a successor claim arrives carrying `Relation{handoff_from}` pointing at this cycle's root. The cycle closes *immediately* — see §2.2 close-immediately rule.

The owner of a cycle is one agent. A cycle is identified by `CycleID` — by convention, the ID of the root claim that opened the cycle.

### 2.2 Handoff

A **handoff** cleanly transfers top-level responsibility from one agent to another. It is encoded as an action with `ActionType=handoff` whose claim carries a `Relation{handoff_from}` pointing at the predecessor's cycle root claim ID.

A handoff is the only kind of `caused_by` link that **terminates** the predecessor's cycle and **opens** a fresh top-level cycle for the successor. The new cycle is rendered as a *new* top-level row, never nested under the predecessor.

Handoff has a strict precondition (§4.4): the predecessor must have zero open child work and zero in-flight tool calls before issuing the handoff.

**Close-immediately rule.** When the resolver observes `handoff_from` arriving on a successor claim, the predecessor's cycle terminates *immediately*, regardless of the predecessor's root claim status. Rationale:

- **Deterministic**: a single signal closes the cycle. No race between "is the root claim closed yet?" and "did the handoff arrive yet?"
- **Idempotent**: a duplicated handoff delta is a no-op on an already-closed cycle.
- **Replay-safe**: any projection replay sees the `handoff_from` relation on the successor and can directly infer the predecessor's cycle terminated at that point. No transitive walk required.
- **Matches the contract**: `HandoffEligible` already proved at handoff time that the predecessor has zero open child work + zero in-flight artifacts. The only thing that could keep the cycle "open" under a dual-condition drain is the root claim itself, and the handoff *is* the agent's signal that they're done with it for ownership purposes.

**Dual-relation cross-validation.** Every handoff successor claim MUST carry both `Relation{handoff_from}` (pointing at the predecessor cycle's root claim ID) AND `claim.ActionType == ActionTypeHandoff`. The bridge cross-validates these on every claim with `ActionType=handoff`; mismatch is logged and the claim is treated as a top-level cycle (defensive handling — the claim still renders, but the predecessor's cycle does not close from this signal). This prevents a single misconfiguration from corrupting the cycle state.

### 2.3 Child agent

A **child agent** is an agent acting as the subject of an open claim issued by another agent within an active cycle. Examples: an agent consulting a peer (consult target is a child), challenging a peer (challenge target is a child), or invoking a guardian check (guardian is a child).

Child agents:
- Render under the parent's tree at the row that invoked them, with the indented `└─` graphic the chat panel already supports.
- Cannot themselves issue handoff while serving as a child (the handoff precondition catches this, see §4.4).
- Are not considered "done" — their row stays in the in-progress visual state — until *all* of their own child work (their tool calls, their nested consults, their guardian checks) has completed.

### 2.4 Artifact lifecycle (started + completed)

Tool calls, consult/challenge dispatches, and guardian checks all follow a uniform two-artifact lifecycle:

- **Started artifact.** Emitted at the *moment of invocation*: for tool calls, the moment the runtime calls into the tool function; for child-agent dispatches, the moment the parent posts the claim against the child. Carries `Kind="tool_started"`, `"consult_started"`, `"challenge_started"`, or `"guardian_check_started"`.
- **Completed artifact.** Emitted at the *moment of completion*: for tool calls, the instant the tool function returns to the runtime (before any skill-level result processing); for child-agent dispatches, the instant the child's testament closes the claim. Carries the matching `*_completed` kind, an `Outcome` field (`success`, `failure`, `timeout`, `cancelled`), and a `Relation{completes}` whose `Related` is the started artifact's ID.

This pair is the source of truth for the chat panel's "running → done" visual transition. The completion artifact's `completes` relation is the *only* matching mechanism — no fuzzy matching by name + agent + timestamp.

### 2.5 Attribution: AgentID, Owner, Target

Every claim, testament, and artifact carries three distinguishable agent identities:

| Field | Meaning | Source |
|-------|---------|--------|
| `AgentID` | Which agent instance posted this record | Direct field on `Claim` / `Testament` / `Artifact` |
| Owner | Which agent is the canonical issuer of this work | `Relation{issuer}` — `IssuerAgentID(relations)` |
| Target | Which agent must respond / execute | `Relation{subject}` — `SubjectAgentID(relations)` |

For a consult `A → B`: claim has `AgentID=A's worker, owner=A, target=B`. The testament B emits in response has `AgentID=B's worker, owner=B`. This is what lets the bridge attribute child rows: an artifact emitted under a testament with `owner=B` belongs to B's lane in the tree, even if B's worker is a replica with a different `AgentID`.

### 2.6 Relations table

The Relations actually used for parent/child attribution:

| Relation | Direction | Used for |
|----------|-----------|----------|
| `issuer` | Points to the agent that issued | Owner attribution (cycle-owner identity) |
| `subject` | Points to the agent that must respond | Target attribution (child-agent identity) |
| `caused_by` | Child → parent claim | Nesting within a cycle (consult/challenge under a tool call or claim) |
| `handoff_from` | Successor → predecessor claim | Cycle termination + new-cycle opening |
| `completes` | Completion artifact → started artifact | Pairing the two halves of a tool/child lifecycle |
| `claim` | Testament/artifact → its claim | Resolving "which claim does this artifact belong to" |

`caused_by` already exists (`core/claims/types.go:57`). `handoff_from` and `completes` are added in Phase 0.

---

## 3. The data the UI consumes

### 3.1 `ClaimsAgentStatusMsg` (cycle edge events)

Emitted by the bridge on cycle open/close edge transitions. The agent panel uses this as the **sole** authoritative source of `Status=Acting` vs `Status=Idle`.

```go
type ClaimsAgentStatusMsg struct {
    AgentID      string  // canonical agent identity (cycle owner)
    SessionID    string
    Active       bool    // true on cycle open; false on full drain
    CycleID      string  // root claim ID that anchors this cycle
    OpenCount    int     // count of open subject claims for the owner (debugging)
    Reason       string  // one-line summary (e.g., the cycle root claim's title)
}
```

### 3.2 `ClaimArtifactAddedMsg` (start of a row)

Emitted by the bridge when a *started* artifact lands. Opens a row in the chat tree.

```go
type ClaimArtifactAddedMsg struct {
    ArtifactID       string         // unique ID — the row key
    CycleID          string         // which top-level cycle this row lives under
    ParentRowID      string         // immediate parent within the cycle (artifact ID, or empty for cycle-root rows)
    ClaimID          string         // claim this artifact's testament responds to
    OwnerAgentID     string         // canonical owner of the parent claim
    OwnerAgentType   string         // type label rendered in child-agent blocks ("guardian", "tester-pipeline", ...)
    TargetAgentID    string         // target of the parent claim (subject)
    Kind             string         // "tool_started" | "consult_started" | "challenge_started" | "guardian_check_started" | "cycle_root" | ...
    Reference        string         // tool name, consult title, etc.
    Metadata         map[string]any // kind-specific structured detail
    CreatedAt        time.Time
}
```

### 3.3 `ClaimArtifactCompletedMsg` (close of a row)

Emitted by the bridge when a *completion* artifact lands carrying `Relation{completes}`. The chat panel matches by `StartArtifactID` and flips the row to its terminal visual.

```go
type ClaimArtifactCompletedMsg struct {
    StartArtifactID  string         // matches the ClaimArtifactAddedMsg.ArtifactID
    CycleID          string         // same cycle (denormalized for routing convenience)
    Outcome          string         // "success" | "failure" | "timeout" | "cancelled"
    Duration         time.Duration  // tool-call wall-clock from start to completion
    Summary          string         // one-line result summary (truncated tool output, child testament summary)
    Metadata         map[string]any
    CompletedAt      time.Time
}
```

### 3.4 The chat panel's rendering rule

With these three messages, the chat panel renders mechanically:

- `ClaimsAgentStatusMsg{Active=true}` → open a new top-level row in the chat tree at depth 0, keyed by `CycleID`. Header reads `<cycle-root-title>: <OwnerAgentType>`.
- `ClaimsAgentStatusMsg{Active=false}` → close the cycle's row (visual finalization; the row stays in scrollback).
- `ClaimArtifactAddedMsg` with `ParentRowID == ""` → top-level row inside the cycle (a tool call directly off the agent, or a direct claim).
- `ClaimArtifactAddedMsg` with `ParentRowID != ""` → nested under the row whose `ArtifactID == ParentRowID`, with the `└─` graphic.
- `Kind` selects icon and label style (tool icon for `tool_started`, child-agent label for `consult_started` / `challenge_started` / `guardian_check_started`).
- `ClaimArtifactCompletedMsg` → look up the row by `StartArtifactID`, flip its visual to terminal (success/failure/timeout/cancelled), stamp `Duration`.

The panel does **no** Relation walking, **no** parent inference, **no** correlation matching beyond exact ID equality.

---

## 4. Backend contracts

### 4.1 Centralized artifact lifecycle: where it lives

Three centralized seams emit the started/completed artifact pair on behalf of all skills:

| Seam | Started emitted at | Completed emitted at |
|------|--------------------|----------------------|
| Tool-execution loop (agent runtime) | The instant the runtime calls into the tool function | The instant the tool function returns to the runtime, *before* skill-level result processing |
| Consult / challenge dispatch helper (`agents/shared/`) | The instant the parent posts the consult/challenge claim | When the bridge observes the child's testament close the claim |
| Guardian check helper | The instant the parent invokes the guardian check | When the bridge observes the guardian's testament close the claim |

**Rule:** No skill ever emits an artifact pair itself. If a skill's behavior is missing from the rendered tree, the fix is to ensure the skill goes through one of these three seams — never to add per-skill artifact emission.

### 4.2 Completion is non-conditional

The completed artifact MUST be emitted on every termination path of a started artifact:
- Normal return → `Outcome=success`.
- Returned error → `Outcome=failure`.
- Timeout / context cancellation → `Outcome=timeout` or `cancelled`.
- Panic recovery → `Outcome=failure` with panic detail in `Metadata`.

This is enforced by structuring the centralized wrap as `defer`-based: the started artifact is emitted before invoking the wrapped function, and the completed artifact is emitted from a deferred closure that captures the outcome. There is no path from "started emitted" to "no completion emitted" short of process termination.

### 4.3 Handoff precondition

A single shared precondition checker, called by every handoff site and by a board-level guard:

```go
// HandoffEligible returns nil if the agent may post a handoff action,
// or a structured rejection error otherwise. Both checks are board
// queries — the agent does not need any local state.
func HandoffEligible(board *claims.ClaimsBoard, agentID string) error {
    // 1. Open child work: any open claim where I am the issuer.
    // 2. In-flight as child: any open claim where I am the subject AND
    //    the issuer is another agent.
    // 3. In-flight tool calls: any started artifact in my open testaments
    //    without a matching completion artifact.
}
```

Skill-level enforcement: all handoff skills call `HandoffEligible` before posting the action. Failures surface as a structured rejection in the agent's testament so the agent can correct course.

Board-level enforcement: `ClaimsBoard.PostAction` rejects any action with `ActionType=handoff` for which `HandoffEligible` fails. Belt-and-suspenders against bypass.

### 4.4 Cycle-owner constraint on handoff

The handoff precondition checker also rejects handoff if the agent is currently a child (open `subject=me` claim from a peer issuer). This converts "child agents cannot handoff" from a per-skill rule into a single board-derived check.

### 4.5 Caused-by on dispatched claims

Every dispatcher site that posts a claim *as part of* executing an enclosing claim must stamp `Relation{caused_by}` on the new claim, pointing at the enclosing claim's ID:

| Site | File | Caused-by source |
|------|------|------------------|
| Architect dispatching to engineer/designer/etc. | `agents/architect/claims_testimony.go` | The architect's currently-executing plan claim |
| Orchestrator dispatching pipeline workers | `agents/orchestrator/task_dispatch_claims.go` | The orchestrator's currently-executing task claim |
| Guide routing user prompts | `agents/guide/claims_testimony.go` | The user prompt action's claim |
| Cross-pipeline consults | `agents/shared/cross_pipeline_skills.go:415` | The agent's currently-executing claim |
| Cross-pipeline challenges | `agents/shared/cross_pipeline_skills.go:251` | Same |
| Orchestrator health consults | `agents/orchestrator/health_monitor.go:145` | Same |

Source of "currently-executing claim" is `WithParentClaimID(ctx)` (already exists in `agents/shared/forwarded_request_claim.go`). Phase 3 makes this read uniformly.

### 4.6 Handoff-from on handoff actions

Every handoff site sets `ActionType=handoff` and adds `Relation{handoff_from}` whose `Related` is the predecessor cycle's root claim ID. The bridge uses this as the cycle-termination + cycle-opening signal.

---

## 4.7 Fabric as a co-equal enforcement plane

Fabric is the inter-agent activity / inbox propagation substrate. It propagates `activity.Append` events and inbox deltas independently from the board. An agent that bypasses `board.PostAction` and emits a handoff via Fabric directly would slip past the board guard. Fabric must catch this with three independent layers, each enforcing the same handoff invariant from a different angle:

### 4.7.1 Fabric publish-side handoff guard

Wrap every `activity.Append` site that carries handoff semantics with `claims.HandoffEligible(board, agentID)`. Implementation: a small middleware in `core/activity/` that classifies the event by its `ActionKind` (or subject metadata flagged as `handoff=true`) and either propagates it or drops it with a structured rejection logged to the publishing agent's testament accumulator as a `handoff_rejected_by_publish_guard` artifact. Catches: agent dispatching handoff via Fabric while open work exists.

### 4.7.2 Fabric receive-side verification

When an agent's request handler receives a forwarded envelope tagged as a handoff (`ForwardedRequest.Metadata["handoff_from_claim_id"]` set, OR the envelope's claim relations carry `handoff_from`), the receiver-side runtime in `agents/shared/forwarded_request_claim.go` verifies the predecessor's cycle is actually drained on the board *before* invoking the handler. If not, the envelope is dropped and the predecessor's testament accumulator records a `handoff_rejected_by_receiver` artifact. Catches: racy delivery, out-of-order envelope ordering, attempts to handoff to an agent before predecessor work cleared.

### 4.7.3 Fabric envelope cycle-context propagation

`ParentClaimID` lives on ctx as a Go-context value — process-local. When an agent dispatches to another agent via Fabric, the receiver's ctx has no parent claim ID, so `caused_by` attribution at the receiver's downstream dispatches breaks (children render as fresh top-level cycles instead of nesting). Fix: every Fabric envelope (`guide.RouteRequest`, `ForwardedRequest`, inbox deltas) carries three extra metadata fields:

| Field | Meaning |
|---|---|
| `parent_claim_id` | The claim the envelope is dispatched in service of. Receiver re-stamps via `claims.WithParentClaimID(ctx, ...)` so `caused_by` attribution works on downstream claims posted by the receiver. |
| `cycle_id` | Direct cycle attribution without walking the chain. The receiver's bridge uses this as a hint to skip relation traversal. |
| `handoff_from_claim_id` | Non-empty only when the envelope is a handoff. Triggers receive-side verification (§4.7.2). |

The receiver's intake (centralized in `BeginForwardedRequestAccumulator`) reads these and re-stamps ctx accordingly. Result: cycle attribution is end-to-end correct *across* agents, not just within one agent.

### 4.7.4 Why all three together is the right answer

| Bypass attempt | Caught by |
|---|---|
| Agent posts handoff action directly to board | Board guard (§4.3, P2.3) |
| Agent skill calls `HandoffEligible` and ignores the result | Board guard |
| Agent publishes handoff via Fabric activity without going through board | Fabric publish-side guard (§4.7.1, P4.5a) |
| Fabric delivers handoff envelope before predecessor's cycle drained | Fabric receive-side verification (§4.7.2, P4.5b) |
| Cross-agent dispatch loses parent claim attribution | Fabric envelope metadata propagation (§4.7.3, P4.5c) |

The cycle resolver becomes a passive consumer of well-formed events: its correctness depends only on the input being well-formed, and three independent layers ensure that.

### 4.7.5 Performance

- Resolver: O(1) per event, single mutex.
- Board guard: one projection read per handoff post (already done; bounded by board size).
- Fabric publish guard: one `HandoffEligible` per handoff publish — same bound.
- Fabric receive verification: one board projection lookup per handoff envelope received — same bound.
- Envelope metadata propagation: three string fields per envelope. Negligible.

Total worst-case overhead per handoff: 3 board projection reads. Each is a single mutex acquire over an already-cached map. Sub-microsecond on any realistic board. Zero impact on hot paths (consult/challenge/tool call don't hit any of these).

---

## 5. The bridge as cycle resolver

The bridge (`ui/bridge/claims.go`) becomes the *sole* component that walks Relations and computes cycle membership. It has access to `BoardMutationDelta` (current) and the full `ClaimsBoardProjection` (for graph walks during attribution).

### 5.1 State

```go
type cycleState struct {
    CycleID       string  // root claim ID
    OwnerAgentID  string
    OpenSubjectClaims map[string]struct{}  // claim IDs where owner is subject (delegated *to* owner) or issuer with no testament yet
    InFlightArtifacts map[string]struct{}  // started artifact IDs without a matching completion in the subtree
    ChildClaims   map[string]struct{}      // transitively caused_by claims still open
}

type bridgeState struct {
    cycles       map[string]*cycleState  // CycleID → state
    agentCycle   map[string]string       // agentID → currently-active CycleID (one per agent)
    artifactCycle map[string]string      // artifactID → CycleID (for completion routing)
    artifactParent map[string]string     // artifactID → parent artifact ID (for ParentRowID)
}
```

### 5.2 Per-delta processing

For each `BoardMutationDelta`:

1. **`claim_created`:** If the claim has no `caused_by` and no `handoff_from`, it opens a new cycle for its owner — emit `ClaimsAgentStatusMsg{Active=true, CycleID=<claim.ID>}`. If it has `handoff_from`, close the predecessor cycle (after verifying drain) and open a new cycle. If it has `caused_by`, attach to the parent's cycle, register in `childClaims`.
2. **`testament_submitted`:** A testament closes a claim. If it carries new artifacts, emit `ClaimArtifactAddedMsg` for each *started* artifact (with cycle/parent attribution) and `ClaimArtifactCompletedMsg` for each completion artifact (resolved via `Relation{completes}`).
3. **`claim_status_changed` to closed:** Remove the claim from `openSubjectClaims` and `childClaims` for its cycle. Check cycle drain: if all open work resolved, emit `ClaimsAgentStatusMsg{Active=false}` and tear down the cycle entry.
4. **`claim_rejected`:** Same drain check; rejection counts as closure for cycle purposes (per the memory rule, rejection is not a VFS terminal but is a claim terminal).

### 5.3 Parent attribution algorithm

Given a started artifact `A` belonging to testament `T` responding to claim `C`:

- `A.OwnerAgentID = IssuerAgentID(C.Relations)`
- `A.TargetAgentID = SubjectAgentID(C.Relations)`
- `A.CycleID` is read directly from `claimCycle[C.ID]` (computed at `claim_created` time via `caused_by` / `handoff_from` walk; cached thereafter).
- `A.ParentRowID` is the answer to "which previous *_started artifact should this row nest beneath?" Three cases:
  - **A is a tool/LLM emission directly on the cycle owner's accumulator** (the artifact's claim IS the cycle root, or any claim the cycle owner is processing): `ParentRowID = ""` — the row renders flat under the cycle's top-level row.
  - **A is a peer-interaction `*_started`** (consult/challenge/guardian-check) emitted by the cycle owner: `ParentRowID = ""` — the peer-interaction itself is a top-level child of the cycle. The bridge ALSO indexes `claimToInvocationArtifact[child_claim_id] = A.ID` so artifacts emitted by the responding agent on the child claim's testament can find it.
  - **A is emitted by a responding agent on a child claim's testament** (e.g., engineer's tool calls responding to architect's consult claim): `ParentRowID = claimToInvocationArtifact[A's claim ID]` — the row nests beneath the originating peer-interaction artifact, producing the `└─ guardian` / `└─ tester-pipeline` style child-agent block from the screenshot.

The walk is bounded: cycle depth is small (single-digit in practice) and the bridge caches per-claim cycle membership in `artifactCycle` plus per-claim invocation backref in `claimToInvocationArtifact`. Both maps are cleaned up on cycle teardown so memory stays bounded across long sessions.

### 5.4 Liveness

The bridge runs the cycle resolver on each delta in the same goroutine that consumes the board's delta channel. Per the async-by-default memory, the channel is bounded; the resolver itself is non-blocking (no I/O, no locks beyond the bridge's own mutex). Edge-emitting messages are enqueued via the existing `b.enqueue` path which already routes through the Tea program.

### 5.5 Recovery

On UI start (or session switch), the bridge replays the current `ClaimsBoardProjection` to reconstruct cycle state from scratch — `SwitchSession` already exists for this. Replay is idempotent: emit `ClaimsAgentStatusMsg{Active=true}` for every currently-open cycle, then emit `ClaimArtifactAddedMsg` for every open started artifact. Completed artifacts that have already paired do not need replay messages — the chat panel's reset clears prior rows.

---

## 6. UI-side changes

### 6.1 `ui/msg/msg.go`

Extend `ClaimArtifactAddedMsg` with the fields in §3.2. Add `ClaimArtifactCompletedMsg` (§3.3). Extend `ClaimsAgentStatusMsg` with `CycleID` (§3.1).

### 6.2 `ui/bridge/claims.go`

Implement the cycle resolver (§5). Subscribe to projection (already done) and emit the three message types.

### 6.3 `ui/agent/model.go`

- `handleClaimsAgentStatus` already exists and is the sole Status writer — extend to record `CycleID` for the agent panel's row metadata.
- `handleClaimArtifactAdded` becomes a thin shim that pushes to the per-agent feed using the new fields directly (no synthesis of a fake `ActivityEventMsg`).
- New `handleClaimArtifactCompleted` flips the per-agent feed entry to its terminal visual.
- **Remove** the `eventTypeToStatus` terminal-event mappings that still flip Status outside of `handleClaimsAgentStatus` (per Explore report §4.6: tool-failure path at `ui/agent/model.go:981`, terminal mappings at `ui/agent/model.go:2116-2118`).

### 6.4 `ui/chat/model.go`

Add `case msg.ClaimArtifactAddedMsg` and `case msg.ClaimArtifactCompletedMsg` to the `Update` switch. Implementation pushes/closes rows in the existing tree renderer, keyed by `CycleID` and `ParentRowID`. The renderer already supports indented child blocks; this just feeds it the right keys.

---

## 7. Implementation plan

The plan is sequenced so each phase produces a working, testable system. Phases 0–2 are pure backend; Phase 3 wires dispatchers; Phase 4 builds the resolver; Phases 5–6 add the UI surface; Phase 7 deletes the legacy paths; Phase 8 hardens.

Each item lists files, acceptance criteria, and the test classes that apply. Test classes:
- **Unit** — in-package, fast, no goroutines beyond the SUT's own.
- **Integration** — multiple packages, real dependencies (real board, real bridge), in-process.
- **E2E** — TUI-level, drives a real session and asserts on rendered output.
- **Race** — `go test -race`; covers any item that touches shared state.
- **Leak** — goroutine + memory leak detection (`goleak.VerifyNone`, allocation budget assertions); covers any item that spawns goroutines or allocates per-event state.
- **Edge** — boundary conditions explicitly enumerated per item.
- **Negative** — error / rejection / cancellation paths.
- **Performance** — `go test -bench` with explicit ns/op + alloc/op budget; covers hot paths (per-delta processing, per-tool-call wrap).

### Phase 0 — Foundation: relation + action constants

#### P0.1 Add `RelationshipHandoffFrom` and `RelationshipCompletes` constants

**Files:** `core/claims/types.go`

**Acceptance criteria:**
- `RelationshipHandoffFrom = "handoff_from"` constant exists alongside other relationship constants in the existing block.
- `RelationshipCompletes = "completes"` constant exists in the same block.
- Helper `HandoffFromClaimID(relations []Relation) string` exists and returns the `Related` field of the first `handoff_from` Relation, or empty string.
- Helper `CompletesArtifactID(relations []Relation) string` exists with the same shape.
- `go vet ./...` passes; `go build ./...` passes.

**Tests:**
- Unit: `TestHandoffFromClaimID_Empty`, `TestHandoffFromClaimID_Single`, `TestHandoffFromClaimID_MultipleReturnsFirst`. Same three for `CompletesArtifactID`. **No** other test classes apply (pure function, no state).

#### P0.2 Add `ActionTypeHandoff`

**Files:** `core/claims/types.go`

**Acceptance criteria:**
- `ActionTypeHandoff ActionType = "handoff"` constant exists in the existing `ActionType` block.
- All existing exhaustive switches over `ActionType` (run `gofmt -d` + grep for `switch.*ActionType`) either explicitly handle `handoff` or are documented as not exhaustive.

**Tests:**
- Unit: `TestActionTypeHandoff_StringMatches` (single equality assertion). Exhaustiveness check by an `exhaustive`-tagged lint pass in CI; if no such lint exists, a manual `TestActionType_AllConstantsHandled` table-driven test that iterates a slice of all known `ActionType` values and asserts each is handled by `ActionType.String()` (already implicit) and by any switch surfaced via reflection/grep.

### Phase 1 — Centralized artifact lifecycle infrastructure

#### P1.1 Tool-execution loop wrap

**Files:** Agent runtime tool dispatch path. The exact file is determined during this item — search for the central tool-call invocation in the agent runtime; expected location is the per-agent loop that calls into registered skill functions.

**Acceptance criteria:**
- Every tool invocation in the agent runtime is wrapped by a single `WrapToolInvocation(ctx, board, claimID, toolName, fn)` function.
- The wrap emits a `started` artifact (`Kind="tool_started"`, `Reference=toolName`, `Metadata` includes input args) onto the agent's current testament accumulator BEFORE calling `fn`.
- The wrap emits a `completed` artifact (`Kind="tool_completed"`, `Relation{completes}` to the started artifact ID, `Metadata` includes outcome + duration) IMMEDIATELY after `fn` returns, BEFORE any skill-level result processing.
- Errors, timeouts, panics all emit completion (deferred closure ensures this).
- Removing the wrap from any one tool call site fails a test that asserts every registered tool goes through the wrap (introspection over the tool registry).

**Tests:**
- Unit: `TestWrapToolInvocation_EmitsStartedBeforeFn`, `TestWrapToolInvocation_EmitsCompletedAfterFn`, `TestWrapToolInvocation_StartedArtifactIDMatchesCompletesRelation`, `TestWrapToolInvocation_ErrorEmitsFailureCompletion`, `TestWrapToolInvocation_PanicEmitsFailureCompletion`, `TestWrapToolInvocation_ContextCancelEmitsCancelledCompletion`, `TestWrapToolInvocation_TimeoutEmitsTimeoutCompletion`.
- Integration: `TestToolDispatch_AllRegisteredToolsWrapped` — iterate the runtime's tool registry, invoke each with a no-op test fixture, assert each produces a started+completed pair.
- Race: run the unit tests under `-race` with concurrent invocations of `WrapToolInvocation` against the same accumulator (assert no shared-state corruption).
- Leak: `goleak.VerifyNone` around 10k invocations; assert no goroutine growth.
- Edge: zero-duration tool, tool returning a 1MB result, tool returning nil, tool with a name containing slashes/spaces.
- Negative: invocation where the testament accumulator is missing — must fail loudly (not silently drop the artifact pair).
- Performance: `BenchmarkWrapToolInvocation_NoOp` — overhead budget < 5 µs/invocation, < 256 B/invocation. `BenchmarkWrapToolInvocation_1KBResult` — same overhead, allocation grows only by result size.

#### P1.2 Consult / challenge dispatch wrap

**Files:** `agents/shared/cross_pipeline_skills.go`, `agents/shared/forwarded_request_claim.go`

**Acceptance criteria:**
- A single `DispatchPeerInteraction(ctx, board, parentClaimID, kind, target, postFn)` function exists where `kind ∈ {"consult", "challenge"}`.
- It emits a `started` artifact (`Kind="consult_started"` or `"challenge_started"`, `Reference=target`) onto the parent's testament accumulator before invoking `postFn`.
- `postFn` is the existing claim-post path — it returns the new claim ID.
- The `started` artifact records the new claim ID in `Metadata["claim_id"]` so the bridge can pair the eventual completion (which arrives via the bridge observing the child's testament).
- The bridge (Phase 4) emits the `completed` artifact when it sees the child's testament close the claim, NOT this helper. The helper does NOT block waiting for completion.
- Every existing consult/challenge call site in `agents/` (grep `ActionTypeChallenge` and `ActionTypeConsultation`) routes through `DispatchPeerInteraction`. A linter test enumerates these call sites and asserts each is via the helper.

**Tests:**
- Unit: `TestDispatchPeerInteraction_EmitsStarted`, `TestDispatchPeerInteraction_RecordsClaimID`, `TestDispatchPeerInteraction_PassesThroughPostFnError`, `TestDispatchPeerInteraction_KindSelectsArtifactKind`.
- Integration: `TestDispatchPeerInteraction_BridgeCompletesPair` — full board + bridge in process, post a parent claim, dispatch a consult, simulate the child posting a testament, assert the bridge emits a `ClaimArtifactCompletedMsg` whose `StartArtifactID` matches the started artifact.
- Race: concurrent dispatches from the same parent against multiple targets.
- Leak: 10k dispatches, no goroutine growth, accumulator memory drains after `Flush`.
- Edge: dispatch to self (same agent type as parent — must be allowed), dispatch with empty target (must be rejected loudly), dispatch where the parent claim is already closed (must be rejected loudly).
- Negative: `postFn` returns error → started artifact is rolled back via a completion with `Outcome=failure`, `Metadata["error"]` set.
- Performance: `BenchmarkDispatchPeerInteraction` — < 50 µs/dispatch, < 1 KB/dispatch (excluding the claim itself).

#### P1.3 Guardian check wrap

**Files:** `agents/shared/` (likely a new `guardian_check.go` or extension of an existing helper) and `agents/guardian/skills_*.go`.

**Acceptance criteria:**
- Same shape as P1.2 but for guardian checks (`Kind="guardian_check_started"` / `"guardian_check_completed"`).
- Every site in the codebase that dispatches a guardian check goes through this wrap.

**Tests:** Identical structure to P1.2.

#### P1.4 Make wraps the only path

**Files:** Various skills that currently emit ad-hoc activity events for tool-call completion or child invocation.

**Acceptance criteria:**
- Grep for any direct `EventTypeToolResult`, `EventTypeAgentAction`, `publishActivity(... ToolResult ...)` emission outside of the three wraps. Each removed.
- A linter test (`agents/lint_no_direct_completion_emit_test.go`) enumerates known files and asserts none emit completion artifacts directly.

**Tests:**
- Unit: linter test (above).
- Integration: pre/post fixture test — record all artifacts emitted during a representative session; assert every started artifact has exactly one matching completion.

### Phase 2 — Handoff precondition enforcement

#### P2.1 `HandoffEligible` checker

**Files:** `core/claims/handoff.go` (new)

**Acceptance criteria:**
- Function signature exactly as in §4.3.
- Returns `nil` when all three conditions pass.
- Returns a typed `HandoffNotEligibleError` with fields `{Reason, OpenChildClaims, OpenAsSubjectFromIssuers, InFlightToolArtifacts}` populated for diagnostics.
- Function is pure read-only against the board — no mutation, no allocation beyond the error path.

**Tests:**
- Unit: `TestHandoffEligible_NoOpenWork`, `TestHandoffEligible_RejectsOnOpenIssuedClaim`, `TestHandoffEligible_RejectsOnOpenSubjectFromPeer`, `TestHandoffEligible_RejectsOnInFlightToolArtifact`, `TestHandoffEligible_AllowsOpenSubjectFromSelf` (self-issued claims don't make the agent a "child").
- Integration: `TestHandoffEligible_AgainstRealBoard_TableDriven` — fixture builds a board with various combinations and asserts the checker matches.
- Race: concurrent checker calls during board mutations (must produce a consistent answer for any single call's snapshot, even if a follow-up call returns differently — no torn reads).
- Edge: agent with no claims at all (returns nil), agent with a self-issued open claim (returns nil), agent with an open challenge as subject from itself (returns nil — self-challenge is not "being a child").
- Negative: nil board → returns explicit error, not panic.
- Performance: `BenchmarkHandoffEligible_LargeBoard` — board with 1000 claims, < 100 µs/check.

#### P2.2 Skill-level enforcement at every handoff site

**Files:** Every handoff skill — grep `handoff` in `agents/architect/`, `agents/orchestrator/`, `agents/guide/`, `agents/academic/`, `agents/shared/`.

**Acceptance criteria:**
- Every handoff site calls `HandoffEligible` immediately before posting the handoff action.
- Failure path posts a structured rejection in the agent's testament (so the agent can see and respond) and returns without posting the action.
- A linter test enumerates handoff sites and asserts each calls `HandoffEligible`.

**Tests:**
- Unit per site: a test that injects a board state failing one of the three checks and asserts the skill does not post the handoff action.
- Integration: `TestHandoffSites_UniformRejection` — drive each agent through a state where it would otherwise handoff, but with a deliberate open child claim. Assert no handoff action is created.
- Negative: agent that ignores the rejection and tries to post the action directly (covered by P2.3).

#### P2.3 Board-level handoff guard

**Files:** `core/claims/board.go` — `PostAction` method.

**Acceptance criteria:**
- `PostAction` checks `HandoffEligible` for any action with `ActionType=handoff` (looking up the issuer agent).
- Failure returns a typed error; the action is not persisted.
- Existing `PostAction` callers handle the error (return propagation; no swallowing).

**Tests:**
- Unit: `TestBoard_PostAction_RejectsHandoffWhenIneligible`.
- Integration: `TestBoard_PostAction_RejectsBypassAttempt` — construct a synthetic handoff action that would fail the precondition, assert the board rejects.
- Race: concurrent `PostAction` of handoff + concurrent `PostAction` of a child claim that would *make* the handoff ineligible. Either ordering must be consistent (no handoff posted while a child claim is in-flight in the same goroutine-visible moment).
- Performance: `BenchmarkBoard_PostAction_HandoffPath` — overhead added by the guard < 50 µs.

### Phase 3 — Dispatcher relations

#### P3.1 `caused_by` on every dispatched claim

**Files:** Per the table in §4.5.

**Acceptance criteria:**
- Every dispatcher site that posts a claim while executing within an enclosing claim adds `Relation{Related: <enclosing claim ID>, RelatedType: "claim", Relationship: "caused_by"}`.
- Source of "enclosing claim ID" is `ParentClaimIDFromContext(ctx)` (rename of existing `WithParentClaimID` if necessary; the read side becomes the canonical lookup).
- Sites where there is genuinely no enclosing claim (e.g., a top-level claim posted by Guide on a fresh user prompt) explicitly comment `// no enclosing claim — top-level cycle root`.
- A linter test enumerates dispatcher sites and asserts each either adds `caused_by` OR carries the explicit comment.

**Tests:**
- Unit per site: assert claim emitted under a parent ctx has the `caused_by` relation; assert claim emitted under a clean ctx does not.
- Integration: `TestDispatchChain_RelationsForm_ATree` — drive a session with architect → engineer → engineer-consults-designer; assert the resulting board's relation graph forms the expected tree (engineer's claim has `caused_by=architect's plan claim`; designer's claim has `caused_by=engineer's claim`).

#### P3.2 `handoff_from` + `ActionType=handoff` on every handoff action

**Files:** Same handoff sites as P2.2.

**Acceptance criteria:**
- Every handoff site sets `ActionType=handoff` and posts a single claim with `Relation{handoff_from}` pointing at the predecessor's cycle root claim ID.
- A linter test enumerates handoff sites and asserts each sets both.

**Tests:**
- Unit per site.
- Integration: `TestHandoffChain_FormsCycleSequence` — drive a four-agent handoff chain (matching the screenshot in this design discussion); assert four cycle-root claims exist, each with `handoff_from` pointing at the prior, and the relation chain is unbroken.

### Phase 4 — Bridge cycle resolver

#### P4.1 Cycle state machine

**Files:** `ui/bridge/claims.go`

**Acceptance criteria:**
- `bridgeState` (§5.1) added to the existing `ClaimsBridge` struct.
- Per-delta processing (§5.2) implemented in `onDelta`, replacing the current single-purpose handlers.
- Cycle open/close emits `ClaimsAgentStatusMsg` with `Active` and `CycleID`.
- All state mutations occur inside the existing `b.mu` mutex; `goleak` shows no extra goroutines.

**Tests:**
- Unit: `TestCycleResolver_OpenOnFreshTopLevelClaim`, `TestCycleResolver_AttachesChildToParent`, `TestCycleResolver_ClosesCycleOnFullDrain`, `TestCycleResolver_HandoffClosesPredecessorOpensSuccessor`, `TestCycleResolver_RejectionCountsAsClosure`, `TestCycleResolver_DeepCausedByChain`.
- Integration: `TestBridge_FullSessionReplay` — real session, real board, assert emitted message stream matches an expected golden sequence.
- Race: concurrent deltas from board + concurrent reads from agent panel — `-race` clean.
- Leak: drive 10k claim lifecycles through the bridge; assert `bridgeState` map sizes drain to zero after final drain; no goroutine leaks.
- Edge: cycle root that is ALSO a handoff successor (must be a single open event, not double-opened); `caused_by` claim whose parent doesn't yet exist (out-of-order delta) — must defer attribution until parent appears, NOT drop the artifact.
- Negative: claim with `handoff_from` to a non-existent predecessor — log + treat as a fresh top-level cycle.
- Performance: `BenchmarkCycleResolver_Delta` — < 10 µs/delta, < 200 B/delta.

#### P4.2 Parent attribution algorithm

**Files:** `ui/bridge/claims.go`

**Acceptance criteria:**
- Algorithm in §5.3 implemented.
- Cached in `artifactCycle` and `artifactParent` after first computation.
- Walks bounded by a sanity limit (e.g., 64 hops) — beyond which the artifact is attributed to the cycle root with a warning emitted via the existing debug log.

**Tests:**
- Unit: table-driven over canonical shapes (linear chain, branching tree, handoff boundary).
- Edge: cycle in the relation graph (shouldn't happen, but defensive — must terminate).
- Performance: `BenchmarkParentAttribution_DeepChain` — < 5 µs at depth 16.

#### P4.3 Recovery on session switch

**Files:** `ui/bridge/claims.go` (`SwitchSession`)

**Acceptance criteria:**
- `SwitchSession` rebuilds `bridgeState` from the new session's projection and emits replay messages (§5.5).
- Replay is idempotent: a second `SwitchSession` to the same session produces identical state.

**Tests:**
- Unit: `TestSwitchSession_RebuildsCycleStateFromProjection`, `TestSwitchSession_Idempotent`.
- Integration: switch into a session mid-cycle; assert the agent panel and chat panel both show the in-flight cycle correctly.
- Leak: switch sessions 1000 times; assert no map growth, no goroutine growth.

### Phase 5 — UI message types and routing

#### P5.1 Extend `ClaimArtifactAddedMsg`, add `ClaimArtifactCompletedMsg`, extend `ClaimsAgentStatusMsg`

**Files:** `ui/msg/msg.go`

**Acceptance criteria:**
- Field shapes exactly per §3.
- All existing references compile; tests in `ui/agent/` and `ui/app_decor_test.go` updated to set the new fields.

**Tests:**
- Unit: round-trip serialization tests if any (these are pure structs — minimal).
- Build: `go build ./...` passes; `go vet ./...` passes; existing tests pass.

#### P5.2 Routing in `ui/app.go`

**Files:** `ui/app.go`

**Acceptance criteria:**
- `ClaimArtifactCompletedMsg` is routed to **both** `chat` and `agent panel` (mirrors the existing `ClaimArtifactAddedMsg` route).
- `ClaimsAgentStatusMsg` is routed to the agent panel (already in place per recent work; verify).

**Tests:**
- Unit: `TestAppRouting_ClaimArtifactCompleted_RoutesToBothPanels`.

### Phase 6 — Chat panel handler

#### P6.1 Add cases to `ui/chat/model.go` `Update`

**Files:** `ui/chat/model.go`

**Acceptance criteria:**
- `case msg.ClaimArtifactAddedMsg` calls a new `handleClaimArtifactAdded` that pushes a row to the tree using `(CycleID, ParentRowID, ArtifactID, Kind, OwnerAgentType, Reference, Metadata)`.
- `case msg.ClaimArtifactCompletedMsg` calls a new `handleClaimArtifactCompleted` that finds the row by `StartArtifactID` and flips it to its terminal visual.
- The existing tree renderer's child-block code path (the one that renders `└─` for nested agents) is invoked when `ParentRowID != ""` and `Kind` is one of the child-agent kinds.
- No Relation walking, no parent inference: the handlers use only the message fields.

**Tests:**
- Unit: `TestChat_HandleClaimArtifactAdded_OpensRow`, `TestChat_HandleClaimArtifactAdded_NestsUnderParent`, `TestChat_HandleClaimArtifactCompleted_FlipsRowVisual`, `TestChat_HandleClaimArtifactCompleted_OrphanedCompletionLogged` (completion arrives without a matching start — log + drop, do not panic).
- Integration: drive a full cycle from board → bridge → chat, assert rendered output matches a golden snapshot of the screenshot's tree shape.
- E2E: render the four-handoff chain into the TUI, assert four top-level rows appear in scrollback in the correct order with their child trees intact.
- Edge: cycle that opens and closes within a single delta batch (start + complete arrive interleaved); cycle with 100 nested children (deep tree); two cycles for two different agents active simultaneously (independent rendering).
- Performance: `BenchmarkChat_HandleArtifactAdded` — < 20 µs/row, < 512 B/row.

#### P6.2 Demote agent-panel synthesis path

**Files:** `ui/agent/model.go` `handleClaimArtifactAdded`

**Acceptance criteria:**
- The current implementation (synthesizes an `ActivityEventMsg` and pushes via `pushAgentEvent`) is replaced with a direct push using the new fields.
- No double-handling: an artifact arriving via this path does NOT also arrive via `handleActivity`.

**Tests:**
- Unit: existing agent-panel tests updated.
- Integration: `TestAgentPanel_NoDoubleRenderOfArtifact` — assert each artifact appears exactly once in the per-agent feed.

### Phase 7 — Status leak removal

#### P7.1 Remove `eventTypeToStatus` terminal mappings

**Files:** `ui/agent/model.go:406` (the map) and `ui/agent/model.go:2116-2118` (the consumer in `handleActivity`).

**Acceptance criteria:**
- The map is deleted entirely OR shrunk to only event types that drive `ActivityState` (not `Status`).
- `handleActivity` no longer flips `agent.Status` for any incoming event.
- All `Status` writes in the entire `ui/agent/` package go through `applyAgentLifecycleUpdate` with `Source == agentLifecycleSourceClaims` (the sole writer), `agentLifecycleSourceDemotion` (for explicit user-driven demotion), or are explicitly tagged as exceptions in code review.

**Tests:**
- Unit: `TestAgentPanel_StatusUnchangedByActivityEvent` — fire every `EventType` value and assert `Status` is unchanged.
- Integration: drive a full cycle and assert the only Status transitions observed are `Waiting → Acting → Idle` (or terminal Success/Error from claim outcome).
- E2E: confirm the activity icon flips on/off cleanly across a real session matching the screenshot.

#### P7.2 Remove direct `Status = StatusError` on tool failure

**Files:** `ui/agent/model.go:981`

**Acceptance criteria:**
- The direct write is removed. Tool failure is communicated via the cycle: the failed tool's completion artifact carries `Outcome=failure`; the cycle's terminal state reflects the eventual claim outcome.
- Tests that previously asserted `StatusError` immediately after tool failure are updated to assert against the `ClaimArtifactCompletedMsg{Outcome:"failure"}` path instead.

**Tests:**
- Unit: updated existing tool-failure test.
- Integration: `TestToolFailure_DoesNotImmediatelyFlipAgentStatus`.

### Phase 8 — Hardening and verification

#### P8.1 End-to-end golden test against the screenshot

**Files:** `ui/chat/model_test.go` or new `ui/app_e2e_test.go`

**Acceptance criteria:**
- A golden test reproduces the four-handoff chain from the design discussion (Guardian → Inspector → Orchestrator → Inspector) and asserts the rendered output matches a stored snapshot character-for-character.
- A second golden test reproduces the nested tree (top-level Inspector with `tester-pipeline` child block containing nested guardian command-approval) and asserts the snapshot.

**Tests:**
- E2E (the goldens themselves).

#### P8.2 Property tests for cycle invariants

**Files:** `ui/bridge/claims_property_test.go` (new)

**Acceptance criteria:**
- Property test: for any random sequence of claim-creation, testament-submission, status-change, and handoff events that respects the protocol, the bridge's emitted `ClaimsAgentStatusMsg` stream satisfies: every `Active=true` for a given `(AgentID, CycleID)` is followed by exactly one `Active=false` for the same `(AgentID, CycleID)` (modulo end-of-test pruning).
- Property test: for any random sequence, the count of `ClaimArtifactAddedMsg` whose `Kind` is a `*_started` matches the count of `ClaimArtifactCompletedMsg`.

**Tests:**
- Property: above two properties under `quick.Check` or `gopter` with N=10000.

#### P8.3 Goroutine + memory budget

**Files:** `ui/bridge/claims_perf_test.go` (new)

**Acceptance criteria:**
- 1000 cycles × 10 children × 5 tool calls each driven through the bridge.
- Total goroutine count never exceeds baseline + 4.
- Total resident `bridgeState` allocation drains to within 1 KB of pre-test baseline after final drain.
- Per-delta latency 99p < 50 µs.

**Tests:**
- Leak + Performance (combined).

#### P8.4 Negative-path coverage matrix

**Files:** `ui/bridge/claims_negative_test.go` (new)

**Acceptance criteria:** Each row produces the expected behavior:

| Scenario | Expected behavior |
|----------|-------------------|
| Started artifact with no testament context | Logged and dropped; no UI message emitted |
| Completion artifact with no matching start | Logged; emitted as a no-op `ClaimArtifactCompletedMsg` (chat panel ignores) |
| `caused_by` to a deleted claim | Treated as top-level (cycle root) within owner's cycle |
| `handoff_from` to a deleted claim | Treated as fresh cycle start |
| Cycle drained with one orphan in-flight artifact (started but never completed) | Cycle does NOT close until orphan is also closed via timeout sweep (10 min) |
| Bridge receives delta for a session that was just torn down | Logged and dropped; no panic |

**Tests:**
- Negative: one test per row.

#### P8.5 Documentation and audit

**Files:** This doc; CLAIMS.md cross-reference; per-agent code comments.

**Acceptance criteria:**
- This doc is reviewed and merged.
- CLAIMS.md gains a single forward reference to UI_DESIGN.md §5 (cycle resolver) and §4.3 (handoff precondition).
- Each centralized seam (P1.1, P1.2, P1.3, P2.1) has a one-paragraph code comment naming the seam and pointing at this doc's section.

---

## 8. Verification milestones

After each phase, the following must hold before proceeding:

- **After Phase 1:** Every tool call in a representative session produces exactly one start + one completion artifact pair on the board. No skill-level emissions remain.
- **After Phase 2:** No handoff action is ever persisted while the issuer has open child work. Linter test passes.
- **After Phase 3:** Relation graph for any session forms a connected tree per cycle (verified by an integration test that walks `caused_by` from any claim to a cycle root in O(depth)).
- **After Phase 4:** Bridge emits `ClaimsAgentStatusMsg` on every cycle edge and `ClaimArtifact{Added,Completed}Msg` paired with stable IDs.
- **After Phase 6:** Chat panel renders the screenshot's tree shape from a golden fixture.
- **After Phase 7:** `Status` writes outside `handleClaimsAgentStatus` and `Demote*` are zero (grep + lint test).
- **After Phase 8:** Performance and leak budgets met; property tests green.

---

## 9. Non-goals

- **Persisting cycle state outside the bridge.** Cycles are a UI-side abstraction derived from the board. Restart recovers via projection replay (§5.5), not via cycle-state durability.
- **Multiple concurrent cycles per agent.** A single agent has at most one active cycle at any time. If this needs to change, the bridge state machine becomes a graph rather than a map; out of scope here.
- **Cycle persistence across session boundaries.** A session switch tears down all cycle state and replays from the new session's projection.
- **Per-skill artifact emission.** All artifact emission goes through the three centralized seams. Skills never emit artifacts directly.
- **UI-side relation walking.** The chat and agent panels never call `IssuerAgentID`, `SubjectAgentID`, `FindRelation`, or any other relation helper. The bridge is the sole consumer of Relations.
