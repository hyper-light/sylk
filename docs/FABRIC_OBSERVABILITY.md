# Fabric Observability

The Activity Fabric (`core/activity` + `core/activity/activitystore`) is sylk's cross-agent coordination substrate. Every agent publishes activities to it and reads from it via lenses. The fabric's own storage (SQLite + cache + subscribers) owns the source of truth for **what** happened.

Fabric Observability (`core/fabriclog`) captures the **interaction pattern**: who publishes, who reads, which publications get consumed by which readers, how long publish → consume takes, which lifecycle resolutions close out which challenges and consults, and where the fabric dead-letters.

The output is a single session-global append-only JSONL stream at:

```
.sylk/sessions/{sessionID}/fabric/{YYYY-MM-DD}/fabric.{nnn}.jsonl
```

Rotation is daily + 64 MiB (via `agentlog.StreamWriter`). The stream is session-global rather than per-agent because the fabric IS cross-agent — per-agent fragmentation would lose the interaction graph.

## Record kinds

Six kinds model every observable fabric interaction:

| Kind             | Emitted when                                                | Notable fields                                             |
| ---------------- | ----------------------------------------------------------- | ---------------------------------------------------------- |
| `fabric_publish` | Every `activity.Append`                                     | full redacted `AgentActivity`, publisher, scope            |
| `fabric_read`    | Every `Source.FilterActivities / GetActivity / LatestActivityID` | lens name, alias, filter, returned IDs, elapsed, error     |
| `fabric_consume` | One per activity ID returned by a read                      | `reader_record_seq`, `publish_seq`, publish→consume latency |
| `fabric_ambient` | Every `fabric.AppendAmbientContext` attachment              | tool name, activity IDs attached to tool tail              |
| `fabric_resolve` | Every publish where `Resolves != nil`                       | resolver + resolved activity IDs, publish→resolve latency  |
| `fabric_drop`    | Buffered writer overflow or sink close mid-emit             | reason, cumulative dropped count                           |

`fabric_consume` is the join record — it's how you answer "who actually saw what I published?". Without it you'd have to reconstruct the answer by replaying filters against the publish stream, which is lossy when filter semantics drift.

## Correctness guarantees

- **Per-session monotonic `seq`.** Every record through the same `FabricLogger` sees a strictly-increasing `seq`, so consumers can reconstruct total ordering without wall-clock trust. Tests verify no gaps and no duplicates under 8-way concurrent publishing.
- **Self-contained records.** `fabric_publish` carries the full redacted activity payload inline. Analysis tools don't have to cross-reference per-agent event streams or the SQLite activity store.
- **Redaction at the boundary.** The existing `agentlog.Redactor` chain runs once, at the write boundary, so the log is safe to render.
- **Causal chain preserved.** Each record carries `correlation_id` / `parent_activity_id` / `resolves` pointers where applicable; graph reconstruction is a single pass.

## Robustness guarantees

- **Async bounded buffer.** The logger owns a 4096-deep channel feeding a single drain goroutine. Publishes / reads never block on disk I/O. Overflow emits a `fabric_drop` record (periodically, not per-drop, to avoid amplification) so dead-lettering is observable rather than silent.
- **Core fabric stays on the fast path.** `RecordingSink` and `RecordingSource` are transparent passthroughs — the inner sink/source is invoked first, observability is additive. A nil `FabricLogger` is a no-op passthrough so tests and non-observability contexts behave identically.
- **Close drains.** `FabricLogger.Close()` drains the buffer before returning. Idempotent. Hooked into the orchestrator's `Stop` path via `uninstallFabricObservability`.
- **Rotation + recovery.** Daily + 64 MiB rotation via `agentlog.StreamWriter`. No crash-recovery of dropped records — the SQLite activity store is the authoritative path.

## Wiring

1. **`installActivityFabric`** (`agents/orchestrator/fabric_install.go`) constructs a `FabricLogger` at `SessionFabricPath(sessionID)`, then wraps the installed Sink + Source with `fabriclog.RecordingSink` / `fabriclog.RecordingSource` before calling `activity.SetDefaultSink` / `activity.SetDefaultSource`. This makes observability automatic for every agent in the process.
2. **`fabric.AppendAmbientContext`** (`core/fabric/ambient_envelope.go`) emits `fabric_ambient` records after every attempt (including rate-limited skips), and attaches reader identity + lens alias to the ctx so the underlying `FilterActivities` calls performed by the ambient lens are attributed to the calling agent.
3. **Orchestrator shutdown** calls `uninstallFabricObservability` to drain the async buffer before the process exits.

## Reader-identity propagation

Reader identity travels through `context.Context` so the `RecordingSource` can attribute reads and consumes to the calling agent:

```go
ctx = fabriclog.WithReader(ctx, fabriclog.AgentRef{
    AgentID:    "engineer-1",
    AgentType:  "engineer",
    PipelineID: "task_42",
})
ctx = fabriclog.WithLensAlias(ctx, "WhatAreTheyDoing")
results, err := src.FilterActivities(ctx, filter)
```

Lens callers that don't wrap ctx produce records with empty `Agent` fields — the record still captures the filter + returned IDs, just without caller attribution.

## Analysis CLI

```
sylk trace fabric summary               # counts, top actors / scopes / action kinds
sylk trace fabric unread                # publications with no consume edge
sylk trace fabric lifecycles            # consult / challenge end-to-end
sylk trace fabric follow <activity-id>  # full publish → consume → resolve chain
sylk trace fabric scopes                # per-scope activity + publisher / consumer sets
```

`--session <id>` picks a specific session; omitted, the CLI infers the most-recently-modified session that has a fabric log.

## Disk cost

Expect roughly 3–5× the per-agent events log volume. Full payload on publish + one consume record per activity × per reader is the dominant cost. For a 4-hour session with six agents, this is on the order of 100–500 MB pre-rotation. The tradeoff was deliberate: the fabric log is the single most useful observability artifact for answering "why are agents (not) consulting the knowledge agents?", "are ambient envelopes actually reaching the LLM?", and "which publications go unread?"

## What this does NOT capture

- **Fabric storage internals.** Sub-tier behavior of the DualTierSink, SQLite write paths, Ristretto hit/miss — those live in the storage package's own metrics.
- **Tool-call lifecycle.** Already covered by per-agent `tools.{nnn}.jsonl` via `ToolRecorder`.
- **LLM round-trips.** Already covered by per-agent `llm.{nnn}.jsonl` via `LLMRecorder`.

These are complementary streams; correlate via `activity_id` (fabric) and `tool_call_key` / `llm_call_id` (agent logs) as needed.
