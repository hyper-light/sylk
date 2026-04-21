# Pipeline Tabs — Chat Panel Design

A tabbed chat panel that separates global agent conversation from per-pipeline conversation, auto-closes pipeline tabs on successful Operational Transform hand-off, and preserves closed-tab history across restarts for later review.

---

## Problem

The chat panel today is a single stream. When multiple pipelines run in parallel (the scheduler allows up to `MaxConcurrent = CPU count` concurrent pipelines — see `core/concurrency/scheduler.go:27–37`), output from every agent across every pipeline interleaves in one view. Users cannot visually separate "what is the orchestrator doing right now" from "what is the `fix-redis-leak` pipeline doing right now," and when three pipelines each stream tool calls and thinking text simultaneously the view becomes unreadable.

The goal is a chat panel that:

1. Splits the single stream into one **Global** tab (for global/non-pipeline-scoped agents) plus one tab **per live pipeline**.
2. Closes pipeline tabs automatically when a pipeline successfully hands off to the Operational Transform.
3. Leaves tabs open on any non-success outcome (failure, pause, kill, crash) until the user dismisses them.
4. Lets the user re-open closed tabs to review their chat history — across process restarts.

---

## Design Principles

- **Tabs are filter views over one shared log, not separate buffers.** The existing ring buffer (`ui/chat/history.go:184–193`) stays as the single source of truth. Tabs are lenses that select messages by a grouping key; no message is duplicated across tabs.
- **Grouping key is `PipelineID`.** Every `ChatEntry` is tagged with its owning pipeline (or empty for global). Tab membership is derivable from that single field — no cross-layer lookups at render time.
- **Reuse the existing tab bar.** `ui/tabbar/tabbar.go` already handles layout, overflow (◀/▶ scrolling), hit-testing, close buttons, modified indicators, and `DimActive` for multi-pane setups. It is file-path-centric today; a minimal extension (optional `Label` and `Icon` fields on `Tab`) makes it reusable for chat tabs without forking.
- **Close on success only.** OT hand-off auto-closes with a grace period. Everything else (failure, pause, kill, crash) leaves the tab open until the user dismisses it. Manual dismissal and auto-close both archive the tab for reopen.
- **Persistence mirrors the existing session pattern.** Archives live in `.sylk/chat-archive/` as atomic-write JSON files, matching `core/session/persistence.go:56` conventions — project-scoped, durable, human-readable.

---

## Tab Model

### The tab set

| Position | Tab | Lifetime | Closeable |
|---|---|---|---|
| Leftmost, pinned | **Global** | Permanent | No |
| Left-to-right by pipeline start time | **Pipeline N** | First message → OT hand-off (auto) or user close | Yes |

### What each tab shows

- **Global** — entries with no `PipelineID`. This covers orchestrator top-level routing, architect global-review, system messages, user input before dispatch, and any agent in a global group (e.g. `global`, `knowledge` in the left-panel agent list).
- **Pipeline tabs** — entries whose `PipelineID` matches the tab's pipeline. The label is the pipeline's task slug (e.g. `fix-redis-leak`), not the raw ID. The icon reflects pipeline state or agent type.

### Visual layout

```
 Global │  fix-redis-leak ●  │  refactor-auth  │  ingest-backfill ✕   ⟲
        ─────────────────────────────────────
```

- `●` — unread-messages indicator on non-active tabs (reuses the existing Modified dot).
- `✕` — close button on pipeline tabs (Global has no close zone).
- `⟲` — affordance at the right end of the bar that opens the reopen drawer. Badge-count when archives exist.
- Active tab receives the gap in the underline, matching the editor's tab bar.

---

## Tab Bar Rendering

The existing `ui/tabbar` package is reused with one small extension:

```go
type Tab struct {
    Path        string  // existing — file-centric
    Modified    bool    // existing — reused for unread indicator
    LabelPrefix string  // existing

    // New, optional:
    Label string // when set, overrides filepath.Base(Path) for display
    Icon  string // when set, overrides filetree.FileIcon lookup
}
```

When `Label` or `Icon` is populated the tabbar uses it directly; when empty the existing path-based behavior is preserved. Editor and diff-view tabs continue to work unchanged. Chat tabs populate `Label` with the pipeline slug (or `"Global"`) and `Icon` with a chat/pipeline glyph from the theme.

The tabbar's width budgeting, overflow, close-zone hit-testing, `DimActive`, and `Focused` behaviors all apply unchanged.

---

## Input Routing

The rule is literal: **the selected agent in the left panel is the destination for the next user message.** The chat panel does not infer a destination from the user's current tab focus; it reads the left-panel selection.

```
user presses send →
    destination agent = left-panel selected agent
    message flows to that agent's chat stream
    chat tab containing that stream auto-switches to become active
```

- Switching/highlighting an agent in the left panel **does not** move the chat tab focus on its own. Browsing agents is free; only sending relocates tab focus.
- On send, the chat tab that will receive the message becomes active. If no tab yet exists for the destination, it is created (pipeline case) or the Global tab is focused (global agent case).
- Incoming messages on non-active tabs light the unread dot. Clicking a tab clears it.

---

## Tab Lifecycle

### Opening

A pipeline tab opens when the **first `ChatEntry` with a previously-unseen `PipelineID` arrives**. The tab is appended to the right of existing pipeline tabs (ordered by start time), but is **not** auto-focused. The unread dot turns on so the user knows activity started there.

### Closing

Close triggers are exhaustive — two conditions, nothing else:

| Trigger | Behavior |
|---|---|
| `PipelineProtocolActionOT` for this pipeline | Auto-close after a short grace period (~3s). During the grace period, a terminal "✓ handed off to Operational Transform" line is appended to the tab so the final state is visible before the tab disappears. |
| User clicks `✕` | Close immediately. Pipeline itself is not affected (dismissing a view does not kill the pipeline). |

All other states — `Failed`, `Paused`, `Stopped` with non-success reason, `Killing`, crash — **leave the tab open**. The user reviews and dismisses when ready.

Both close paths archive the tab for later reopen.

### OT hand-off signal

The hand-off is already surfaced by the orchestrator. The chat tab router subscribes to the same event path:

- `agents/shared/pipeline_protocol.go:59` — `PipelineProtocolActionOT` is the terminal action type.
- `agents/orchestrator/task_router.go` — checks `turnResp.Action.Type == agentshared.PipelineProtocolActionOT` and triggers `publishOTGlobalFollowupRequest`.
- `core/events` activity stream carries the hand-off with `ot_handoff_followup` metadata.

The chat tab router listens on that stream. When a hand-off fires for a pipeline matching an open tab, it schedules the grace-period auto-close.

---

## Reopen UI

### Entry points

Three entry points, one drawer:

1. **`⟲` affordance** at the right end of the tab bar, after the last pipeline tab. Shows a count badge when new archives exist since the drawer was last opened.
2. **Keyboard shortcut** — `Alt+Shift+T`.
3. **Command palette** — `Chat: Reopen Closed Tab…`.

### The drawer

A modal panel anchored under the tab bar. Archives are grouped by outcome, newest first:

```
 ⟲ Recently closed
 ───────────────────────────────────────────
  ✓ Handed off
    fix-redis-leak       closed 2m ago   · 4m42s · 38 msgs
    docs-reindex         closed 11m ago  · 1m08s ·  9 msgs

  · Dismissed
    flaky-test-repro     closed 1d ago   · 3m40s · 12 msgs
 ───────────────────────────────────────────
  [ Clear all handed-off ]          Esc to close
```

Each row shows: pipeline label, closed-at (relative), duration, message count, and — for dismissed entries — a short reason if available. Enter or click reopens the selected archive. `Clear all handed-off` prunes all `✓` entries (failed/dismissed retained).

### Reopened tab

- Reappears in the tab bar with a **status badge** in place of the close icon: `✓` for OT hand-off, `·` for dismissed.
- **Read-only.** No new messages will ever arrive for this pipeline; input is disabled with a muted hint:
  > *"This pipeline has ended. Start a new one from the left panel."*
- Can be closed again like any other tab. Closing a reopened archive does **not** remove it from the archive — the tab reference disappears, the archive file remains.
- Tool-call results (file diffs, command outputs) reflect state at the time of capture. File and command references may be stale if the underlying files have changed. The reopened tab displays an empty-state banner on first view:
  > *"This is a historical transcript. File and tool-call references reflect state at the time."*

---

## Persistence

### Storage layout

Archives live under the project root, mirroring the existing session persistence pattern (`core/session/persistence.go:56`, `.sylk/sessions/{id}.json`):

```
.sylk/
  chat-archive/
    index.json              # ordered list for the drawer
    {pipelineID}.json       # one file per closed tab
```

- **`index.json`** — compact metadata per entry: `pipelineID`, label, outcome, `started_at`, `closed_at`, `message_count`, agent list, reason (for dismissed/failed). This is what the drawer renders. Small and cheap to read on every drawer open.
- **`{pipelineID}.json`** — the full filtered `ChatEntry` slice plus a metadata header (outcome, reason, OT hand-off message, duration). Only read when the user actually reopens this specific archive.

### Serialization

- JSON, using the existing atomic-write pattern (write to temp file, rename) — same as `core/session/persistence.go:72–80`.
- Single-writer per `pipelineID` — no locking needed.
- Index writes are append-then-rewrite of `index.json`; this file is bounded (see retention below) so rewrite cost stays constant.

### Write triggers

Archives are written at the moment a tab closes:

| Close trigger | Write timing |
|---|---|
| OT hand-off (auto-close) | After the grace period expires, before the tab is removed from the bar |
| User manual close | Immediately on `✕` click |

No periodic checkpointing. If sylk crashes mid-pipeline the tab history is lost — matches today's behavior for the in-memory ring buffer and avoids write amplification during active pipelines.

### Scoping

Project-level, inherited from `.sylk/`. Archives from one repo do not appear in another's drawer. This matches how sessions and the knowledge forest are already scoped.

### Retention

A sweep runs on app start and after each archive write:

- Keep archives younger than **30 days** OR the newest **50 entries**, whichever preserves more.
- Pruned files are deleted. The corresponding `index.json` entries are removed.
- `Clear all handed-off` in the drawer performs a targeted prune of all `✓` entries on demand (failed/dismissed entries retained).

### Survival across restarts

On launch, the chat tab router:

1. Reads `.sylk/chat-archive/index.json` (if present).
2. Runs the retention sweep, pruning stale entries.
3. Exposes the remaining entries to the drawer on demand.

No archived tab is auto-reopened on startup. Users must explicitly pick archives from the drawer. This avoids surprise — starting sylk does not bring back yesterday's completed work as live-looking tabs.

### What the archive does not preserve

- **Tool-call references** (file diffs, command outputs) are stored as they were captured at the time the `ChatEntry` was created. They are not re-resolved on reopen. If the underlying files have been renamed, deleted, or edited, the archive shows historical state; it does not reconcile.
- **Streaming state** is not preserved. If a message was mid-stream at tab close (should only happen on manual close), the partial content is archived as-is and rendered without a live cursor.
- **Agent-internal state, tool VFS views, memory forest rows** are not embedded. The archive is the chat transcript, not a full session snapshot. For full session recovery, the existing `core/session/persistence.go` path is used.

---

## Data Model Changes

### `ChatEntry`

Add one provenance field:

```go
type ChatEntry struct {
    // ... existing fields (CorrelationID, AgentType, AgentID, TaskID,
    //     SessionID, Source, Content, ToolCalls, ...)

    PipelineID string // "" for global; non-empty for pipeline-scoped entries
}
```

This sits alongside the existing provenance tags (`AgentID`, `TaskID`, `SessionID`) and follows their conventions. Empty string = global; any non-empty value routes to the matching pipeline tab.

### `tabbar.Tab`

Two optional fields (already described above):

```go
type Tab struct {
    Path        string
    Modified    bool
    LabelPrefix string

    Label string // override display name
    Icon  string // override icon glyph
}
```

Existing callers unchanged; chat tabs populate `Label` and `Icon` directly.

### `ChatTabRouter` (new)

A new component owning:

- The ordered tab set (Global + live pipelines + reopened archives).
- The `PipelineID → tab` mapping.
- Subscription to the OT hand-off event stream (grace-period auto-close).
- The archive reader/writer (read `index.json`, write `{pipelineID}.json` + index update on close).
- Filter function: `Tab → []ChatEntry` applied to the ring buffer for render.
- Retention sweep on startup and after each archive write.

Lives under `ui/chat/` alongside the existing history. Has no knowledge of `core/concurrency` internals beyond the event stream it subscribes to.

---

## Reuse vs New Code

| Component | Status |
|---|---|
| `ui/chat/history.go` ring buffer | Reused unchanged — still the single source of truth for live messages |
| `ui/tabbar/tabbar.go` | Reused with two new optional fields on `Tab` (`Label`, `Icon`) |
| `core/events` OT hand-off subscription | Reused — already emitted by `publishOTGlobalFollowupRequest` |
| Atomic JSON write pattern from `core/session/persistence.go` | Reused for archive files |
| `ChatEntry.PipelineID` | **New** field |
| `ChatTabRouter` | **New** component in `ui/chat/` |
| Reopen drawer UI | **New** modal in `ui/chat/` (can reuse `ui/modal` primitives) |
| `.sylk/chat-archive/` directory + retention sweep | **New**, mirroring session layout |

---

## Open Question

**Archive location — project-local or user-level?**

The default in this design is project-local: `.sylk/chat-archive/`. This matches sessions and keeps archives tied to the repo they describe.

The alternative is user-level: `~/.sylk/chat-archive/{project-hash}/`. This survives `.sylk/` being deleted as a project reset and centralizes storage — useful if users routinely clean per-project state but want to retain chat history.

Project-local is recommended unless there is a specific workflow where users reset `.sylk/` and still want reopen history to persist.
