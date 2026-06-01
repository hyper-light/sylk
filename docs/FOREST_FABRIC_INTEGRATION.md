# Forest ↔ Fabric Integration

Prior to this work the Memory Forest ate a narrow diet: four ActionKinds at Coarse resolution only (`precedent_emitted`, `decision_promoted` + Consensus, `validation_accepted`, `charter_ratified`). Tool call telemetry, LLM round-trips, consult/challenge lifecycles, artifact emissions, knowledge pushes — none of it reached the forest. The forest's retrieval quality was capped by its impoverished signal set.

This integration closes that gap across five tiers, all landed together.

## Architecture

The forest has two sources of fabric context observations now, not one. Claim,
testament, artifact, and validation lifecycle truth does not use this path; it
enters through canonical claims deltas and the append-only `forest_ledger`.

```
┌────────────────────────────────────────────────────────────────────┐
│                      activity.Append(ctx, a)                       │
└──────┬──────────────────────────────────────────────────┬──────────┘
       │                                                  │
       ▼                                                  ▼
┌──────────────┐              wraps            ┌────────────────────┐
│ SubscribingSink├─────────────────────────────┤ fabriclog.RecordingSink │
└──────┬───────┘                               └─────────┬──────────┘
       │ fanout                                          │ passthrough
       ▼                                                 ▼
┌──────────────┐                            ┌──────────────────────┐
│ForestSubscriber│ (primary feed: kind-based │ fabriclog.FabricLogger│
│ .electCandidate│  allowlist, ~18 kinds)    │ + ForestFabricBridge  │
└──────┬───────┘                             │ (secondary feed: fans │
       │                                     │  consume/resolve into │
       │                                     │  same harvest path)   │
       │                                     └──────────┬───────────┘
       └──────────────┬──────────────────────────────────┘
                      ▼
          ┌────────────────────┐
          │ fabric observer fn │  (context/traversal ledger records only)
          └────────────────────┘
```

Both feeds converge on the same `HarvestFunc` signature so fabric observation code doesn't need to know which source dispatched a candidate. The registered forest function is a `FabricContextObserver`, not a claims lifecycle harvester.

## The five tiers

### Tier 1+2 — widened ActionKind allowlist

`ForestSubscriber.electCandidate` (`core/activity/activitystore/forest_subscriber.go`) no longer gates on `Resolution.ShouldHarvestForest()`. Eligibility is decided purely on the ActionKind allowlist, which now has five categories:

1. **Explicit precedent + consensus** — precedent_emitted, decision_promoted (consensus), validation_accepted, charter_ratified
2. **Lifecycle closures** — consult_response, challenge_response, validation_rejected, remediation_resolved
3. **Acceptance + knowledge push** — plan_ratified, decision_declared, advisory_emitted, proactive_advisory, narration_emitted
4. **Artifacts + review** — artifact_published, review_completed
5. **Operational primitives** — tool_call_completed, llm_response_completed, forest_consult_emitted

The Resolution tier is now a property of storage retention (how long the fabric's SQLite keeps the activity), not a gate on forest harvesting. A Fine-tier tool_call_completed still reaches the forest; the forest's own persistent store keeps the record even after the fabric ages it out at 24h.

### Tier 3 — tool-call + LLM completion emission

LLM round-trips already emit `ActionLLMResponseCompleted` via the provider-instrumented chokepoint at `core/providers/provider_instrumented.go`. No changes needed there.

Tool completions emit `ActionToolCallCompleted` from `emitToolCompleteRecord` at `agents/shared/tool_timing.go:emitToolCallActivity`. Every `TimedToolCall` return produces:

- Actor: `{AgentID from LogMeta, AgentType from fabric baggage}`
- Subject: `{TargetArtifact: toolName, PathPrefix: extracted scope hint from tool args}`
- Payload: `{tool_call_key, duration_ms, success, error?, llm_call_id?, correlation_id?}`
- Caused: threaded from ctx or from the Tier 5 consult tracker

### Tier 4 — fabriclog as secondary forest feed

`core/fabriclog/forest_bridge.go` adds a `ForestFabricBridge` that subscribes to `FabricLogger` events. It reacts to two record kinds:

- **fabric_consume** — when an activity is read via a lens, the bridge looks up the original activity in the logger's recent-publish cache. If eligible, it forwards the original to the forest harvest with a reason that captures who read it, via which lens, and with what publish→consume latency.
- **fabric_resolve** — when an activity resolves another, the bridge forwards the RESOLVED activity (if eligible) so the forest sees reinforced evidence that the prior commitment actually closed out.

Two feeds into the forest means the same activity can be harvested multiple times (once from the publish chokepoint, again from consume/resolve). The forest's own dedup / salience gardening treats this as corroborating evidence rather than duplication — recurring references raise a branch's hotness.

The bridge is wired in `agents/orchestrator/fabric_install.go` alongside the existing ForestSubscriber.

### Tier 5 — consult → outcome linkage

New ActionKind: `ActionForestConsultEmitted` at Medium resolution.

The `*_forest_consult` skill handler (`core/context/skills/forest_role_skills.go:emitForestConsultActivity`) emits one of these activities per consult, carrying purpose, query, returned branch IDs, and intent metadata. The activity ID is returned in `ForestRoleOutput.ConsultActivityID` so callers can explicitly thread it.

Linkage to subsequent outcomes works by a **process-wide tracker** (`core/activity/consult_link.go`):

- After emission, the handler calls `activity.RecordForestConsult(sessionID, agentID, consultID)`.
- The tracker stores the consult under `(sessionID, agentID)` with a 60-second TTL.
- Every subsequent activity emitter that cares about consult linkage (starting with `emitToolCallActivity`) calls `activity.EnrichCausationFromConsult(&a)` before appending. If the activity's `Caused` is nil and a consult is present in the tracker, the emitter auto-populates `Caused` back to the consult.
- Stronger upstream causation (e.g. explicit ctx-carried consult ID via `WithForestConsultID`, or span-parent causation) always wins — the tracker is a best-effort fallback.

The linkage is **best-effort, not strict**:

- 60s window is generous for a consult → implementation sequence but does not hold state indefinitely.
- Last-write-wins per `(sessionID, agentID)` because agents that consult twice in quick succession are typically refining the same question.
- Missing links simply don't get populated; the consult is still recorded as an activity, so manual chain reconstruction remains possible.

## What this buys

- The forest learns which **tool shapes** solved which problem classes (Tier 3).
- The forest learns which **consults produced which outcomes** (Tier 5 + Tier 4 consume feed).
- The forest learns which **published activities actually got consumed** vs sat unread — reinforcement precedent (Tier 4 consume feed).
- The forest learns which **lifecycle edges resolved** and how long they took — timing precedent (Tier 4 resolve feed).
- The forest learns from **knowledge-agent advisories and narrations** — the soft signal channel the knowledge agents push through the fabric (Tier 1+2).

## Correctness + robustness

- All emission paths call `activity.Append`, which is contractually best-effort (never blocks the hot path).
- The FabricLogger's async bounded buffer continues to absorb subscribers without backpressure — bridge panics are recovered so a single subscriber can't take down the drain.
- The consult tracker uses lazy TTL eviction on read; no background GC goroutine, no unbounded growth.
- All new paths are race-clean under `go test -race`.

## Storage impact

- Forest ledger grows roughly **5–8×** its pre-change size at Coarse due to the widened allowlist and the Tier 3 tool/LLM emissions. The forest's own gardening layer decides which branches stay hot vs age out, so this is a one-time corpus expansion, not unbounded growth.
- Fabric SQLite sizes are unchanged — Resolution tiers weren't modified. The forest harvest copies activities into its own persistent store rather than keeping references to fabric rows, so the forest remains self-contained even when fabric ages out Fine-tier activities.
