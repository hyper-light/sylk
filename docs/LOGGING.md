# Logging — Per-Agent Observability

A unified, structured logging scheme for the Sylk framework: per-session, per-agent, per-pipeline directories with daily + size-based rotation, content-addressed LLM capture, mandatory redaction, and a `sylk trace` CLI that reconstructs cross-agent timelines from a single correlation ID.

---

## Problem

Current logging is sparse and fragmented:

- `~/.sylk/logs/` holds ad-hoc per-agent debug files (`architect_debug.log`, `guardian_debug.log`, `ui_events.log`) that live outside the session scheme, never rotate, and don't carry correlation metadata.
- `.sylk/sessions/{sid}/agents/{agent}/logs/` is wired in `core/agentlog/session_logger.go:87` but most sessions on disk show only `mailbox/` — the logs either aren't flushed or aren't being written in the first place.
- Tool calls capture args and output via `msg.ToolCallEventMsg` for UI rendering, but nothing durable records the full I/O, parent/child relationships, or which LLM round-trip emitted the call.
- LLM round-trips are opaque — we cannot replay the exact prompt, tool definitions, or message history that produced a given action.
- There is no redaction layer. Any secret that appears in a tool arg, output, or LLM message is persisted verbatim.

The result: when an agent hits an unexpected state, the logs we have are insufficient to reconstruct what the model saw or why it acted. Diagnosis becomes guesswork.

This doc defines the target scheme. It extends — not replaces — the existing `core/agentlog` package.

---

## Design Principles

- **One directory per session, per agent, per day.** Paths encode scope; filenames encode stream. `ls` sorted lexicographically gives chronological order.
- **Everything is structured JSONL.** No freeform text files. Developers can still emit "printf-style" debug via `LogDebug` — the output is a JSONL record with `level: "debug"` and a `msg` field. `jq` becomes the universal reader.
- **Redaction is mandatory and at the writer.** Secrets never touch disk. The redactor runs once per record, not at read time.
- **Every record carries correlation IDs.** A record with no `correlation_id`, `tool_call_key`, or `llm_call_id` is a bug — reconstruction depends on these threading through context.
- **Content-addressing for repetitive payloads.** System prompts and tool definitions are stored once per session in a content-addressed corpus; per-call records reference by SHA256. LLM logs stay manageable even at high call volume.
- **Rotation is both time and size.** Daily directories bound retention windows; 64 MB per-file caps pathological days.
- **Pipeline-scoped logs nest under the agent.** A pipeline agent's pipeline-specific events live at `agents/{agent}/pipelines/{pid}/logs/`, not at the session root — debugging follows agent ownership, not global time.

---

## On-Disk Layout

```
.sylk/sessions/{sid}/
  meta.json
  agents/
    {agentName}/
      logs/
        2026-04-21/
          events.000.jsonl      # structured events (info/warn/error/debug)
          tools.000.jsonl       # one record per tool call (Phase 0 + Phase 1)
          llm.000.jsonl         # llm_start + llm_complete records
          bus.000.jsonl         # per-bus-message trace (opt-in)
          events.001.jsonl      # size-roll continuation
          ...
        2026-04-22/
          events.000.jsonl
          ...
      pipelines/{pid}/
        logs/
          2026-04-21/
            events.000.jsonl
            tools.000.jsonl
            llm.000.jsonl
            ...
      wal/                      # unchanged; checkpoint-rolled, not day-rolled
      mailbox/                  # unchanged

  prompts/                      # content-addressed corpus (shared across agents)
    {sha256}.txt                # full system prompts, tool definition bundles

~/.sylk/
  logs/
    boot/
      2026-04-21/
        events.000.jsonl        # pre-session-binding events only
```

Existing `~/.sylk/logs/*_debug.log` files are retired — see [Migration](#migration).

---

## The Five Streams

Each day directory contains up to five stream files. Files are JSONL, one record per line, UTF-8.

### `events.jsonl`

The primary stream. Everything that isn't a tool-call record, LLM trace, or bus message lands here:

```jsonc
{
  "ts": "2026-04-21T14:32:01.234Z",
  "level": "info",              // "debug" | "info" | "warn" | "error"
  "event": "route_request_sent",
  "msg": "routed consult to librarian",
  "agent_id": "engineer-0",
  "agent_type": "engineer",
  "session_id": "sess-xyz",
  "pipeline_id": "pipe-42",     // "" if not pipeline-scoped
  "correlation_id": "corr-abc",
  "tool_call_key": "consult_peer_a1b2",  // optional — set when the event is tool-scoped
  "llm_call_id": "llm-9f8e",             // optional — set when the event is LLM-scoped
  "fields": { ... }             // arbitrary structured payload (redacted)
}
```

`LogInfo`, `LogWarning`, `LogError`, `LogDebug` all write here. `event` is a machine-readable key (stable enum); `msg` is a human-readable one-line summary; `fields` holds variable structured payload.

### `tools.jsonl`

One or more records per tool call. The canonical debugging stream for agent behavior.

A **Phase 0** (start) record is written when the tool call dispatches:

```jsonc
{
  "ts": "...",
  "phase": "start",
  "tool_call_key": "consult_peer_a1b2",
  "parent_tool_call_key": "architect_plan_ffe0",  // for nested branches
  "correlation_id": "corr-abc",
  "llm_call_id": "llm-9f8e",                      // which LLM round-trip emitted this
  "caused_by_activity": "act_123",                // if dispatched from an activity
  "agent_type": "engineer",
  "agent_id": "engineer-0",
  "pipeline_id": "pipe-42",
  "tool_name": "consult_peer",
  "args": { ... },                                // full args, redacted
  "inter_agent": {                                // populated for consult/challenge/approval/store
    "kind": "consult",
    "agent_types": ["librarian"],
    "thread_key": "..."
  },
  "started_at": "..."
}
```

A **Phase 1** (complete) record is written when the call returns:

```jsonc
{
  "ts": "...",
  "phase": "complete",
  "tool_call_key": "consult_peer_a1b2",
  "correlation_id": "corr-abc",
  "agent_type": "engineer",
  "tool_name": "consult_peer",
  "output": { ... },                              // full output, redacted
  "output_truncated_from": 45021,                 // bytes before truncation, if any
  "duration_ms": 184,
  "success": true,
  "error": null,
  "inter_agent": { "status": "done", ... },
  "child_tool_call_keys": ["..."]                 // tool calls this one spawned
}
```

Two records per call is intentional: if the process crashes mid-call, the start record preserves exactly what was attempted. The `sylk trace` CLI reconciles pairs by `tool_call_key`.

### `llm.jsonl`

One record at round-trip start, one at completion. Content-addressed to stay compact.

**`llm_start`:**

```jsonc
{
  "ts": "...",
  "event": "llm_start",
  "llm_call_id": "llm-9f8e",
  "correlation_id": "corr-abc",
  "agent_type": "engineer",
  "agent_id": "engineer-0",
  "model": "claude-opus-4-7",
  "provider": "anthropic",
  "system_prompt_sha256": "abc123...",            // resolves to prompts/{sha}.txt
  "tool_definitions_sha256": "def456...",         // resolves to prompts/{sha}.txt
  "tool_definition_count": 47,
  "messages": [ ... ],                            // full history, redacted, inline
  "messages_approx_tokens": 4231,
  "requested_max_tokens": 8192
}
```

**`llm_complete`:**

```jsonc
{
  "ts": "...",
  "event": "llm_complete",
  "llm_call_id": "llm-9f8e",
  "correlation_id": "corr-abc",
  "response_text": "...",
  "tool_calls_emitted": ["consult_peer_a1b2", "update_claim_ffe0"],
  "thinking": "...",                              // if captured
  "usage": { "input_tokens": 4231, "output_tokens": 812, "cache_read": 3800 },
  "duration_ms": 2104,
  "error": null
}
```

On crash, the `llm_start` alone tells you what the model was asked. This is often enough to diagnose "why did the model do that" even without the response.

### `bus.jsonl` (opt-in)

Wire-level trace of every guide bus message in/out of the agent. Verbose. Off unless `logging.streams.bus: true`. One record per published or received message, including type, correlation, source, target, payload summary (not full payload — that's in `tools.jsonl` / `llm.jsonl`).

### Retired

The freeform `debug.log` is gone. `LogDebug(...)` writes structured records to `events.jsonl` with `level: "debug"`.

---

## Rotation

### Daily rotation

At every write, the logger resolves its current path as `logs/{YYYY-MM-DD}/{stream}.{nnn}.jsonl` using the session's timezone (captured in `meta.json`). The first write of a new day closes the previous handle and opens a fresh file under the new date directory, creating it if needed.

Rollover is clock-driven but lazy: we don't rotate on a timer, only when a write actually crosses the midnight boundary. This avoids empty files on idle days.

### Size rotation

Each stream file caps at **64 MB**. When a write would push the file over, the logger closes it and opens `{stream}.{nnn+1}.jsonl` in the same day directory. Numbering is three-digit, zero-padded, starting from `000` — so `events.000.jsonl`, `events.001.jsonl`, etc. Three digits handles 64 GB per stream per day, which is absurdly generous.

The currently active file within a day is always the highest-numbered one. `tail -f logs/{date}/events.*.jsonl | head -n 1` is a valid "follow latest" pattern.

### WAL is not rotated this way

`agents/{agent}/wal/` stays flat. WAL files are checkpoint-rolled based on durable-event count, not time or size. Forcing them into day directories would create cross-day handles for long-running protocols. Logging and WAL live side by side; they don't share rotation policy.

---

## Redaction

### Pipeline

Every record passes through a `Redactor` chain before serialization:

```go
type Redactor interface {
    RedactString(s string) string
    RedactValue(v any) any   // recursive; walks maps/slices
}
```

Three built-in redactors, applied in order:

1. **PatternRedactor** — regex library for known secret shapes:
   - Anthropic keys: `sk-ant-[A-Za-z0-9_-]+`
   - OpenAI keys: `sk-(proj-)?[A-Za-z0-9_-]{20,}`
   - AWS access keys: `AKIA[0-9A-Z]{16}`
   - GitHub tokens: `gh[pousr]_[A-Za-z0-9_]{36,}`
   - JWTs: `ey[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`
   - Bearer header: `(?i)bearer\s+[A-Za-z0-9._-]+`
   - Long opaque strings in suspicious context: `(?i)(token|secret|password|api[_-]?key)[\s:="']+\S{12,}`
2. **EnvValueRedactor** — at startup, snapshots values for configured env vars and replaces literal occurrences. Catches secrets embedded in args or outputs even when they don't match a recognizable prefix.
3. **FieldNameRedactor** — on structured JSON, redacts values at keys matching a denylist (`authorization`, `api_key`, `token`, `password`, `secret`) regardless of value shape.

Replacement is `[REDACTED]` by default, with an optional suffix of a short SHA-256 prefix (`[REDACTED:a1b2c3]`) so the same value is recognizable across records without exposing the value itself.

### Config

```yaml
logging:
  redact:
    env_vars: [ANTHROPIC_API_KEY, OPENAI_API_KEY]
    field_names: [authorization, api_key, token, password, secret]
    patterns: []                  # custom additions
    include_fingerprint: true     # append last-6 of sha256
  streams:
    llm: true                     # default-on; stream_start + stream_complete only
    bus: false                    # default-off; very verbose
```

### Enforcement boundary

The redactor runs at the writer, not at the caller. Callers pass raw structures; the writer redacts once. This prevents the bug where one code path forgets to redact — if the data got to the writer, it gets redacted.

---

## LLM Log Compaction

LLM calls are frequent and repetitive. A naive per-call dump captures the same system prompt and tool definition bundle thousands of times. Content-addressing avoids this:

1. On first write of a session, the logger hashes the system prompt and tool definition JSON and writes each to `.sylk/sessions/{sid}/prompts/{sha256}.txt`.
2. Subsequent `llm_start` records reference by `system_prompt_sha256` and `tool_definitions_sha256` — the full text is never re-embedded.
3. Message history (per-call, not dedupable) stays inline in `llm_start`.
4. Redaction runs on both the stored prompt text and the inline message history.

Size impact: a busy agent doing 500 LLM calls/day with a 30 KB system prompt and 60 KB of tool definitions would write ~45 MB/day if inlined. With content-addressing, each prompt/tool-bundle pair is written once (~90 KB), and per-call overhead drops to the message history alone.

`sylk trace` resolves SHA references by reading the corpus files — transparent to the user.

---

## Correlation Thread

Three IDs must flow through agent context for cross-agent reconstruction to work:

- **`correlation_id`** — per-request chain, set at the originating boundary and propagated through route metadata (`shared.StreamMetadataFromContext`).
- **`tool_call_key`** — per-tool-call lifecycle ID, set by `shared.WithActiveToolCall` before skill handler invocation.
- **`llm_call_id`** — per-LLM-round-trip ID, set at the top of the provider call and propagated to all tool-call events emitted in response to that round-trip.

Every log record carries whichever of these apply. The existing `SessionEventLogger` already reads `correlation_id` from context via `LogMetaFromContext`; we add the same for `tool_call_key` and `llm_call_id`.

A dropped correlation is a logging bug — records without any thread ID should be rare and warrant a warning in the logger itself (meta-log).

---

## `sylk trace` CLI

A subcommand of the main CLI that reconstructs a timeline from a correlation ID (or tool-call key, or LLM call ID, or session ID).

```
$ sylk trace corr-abc

14:32:00.120  engineer        llm-call        llm-9f8e           2.1s   4231 in / 812 out
14:32:01.234    consult_peer  tool-start      consult_peer_a1b2         → librarian
14:32:01.234      (branch)    route-request                             → librarian
14:32:01.320        librarian llm-call        llm-7a2c           0.8s   2104 in / 412 out
14:32:01.420        librarian read_file       read_ff012         12ms
14:32:01.418    consult_peer  tool-complete   consult_peer_a1b2         ok, 184ms
14:32:01.445  engineer        update_claim    upd_4455                  ok
```

Implementation is straightforward: walk `.sylk/sessions/{sid}/*/logs/*/{events,tools,llm}.*.jsonl`, filter by correlation ID (transitively — follow `parent_tool_call_key` and `caused_by_activity` chains), sort by timestamp, render as an indented timeline.

### Commands

- `sylk trace <correlation-id>` — full timeline for a correlation chain
- `sylk trace tool <tool-call-key>` — timeline scoped to one tool call
- `sylk trace llm <llm-call-id>` — one LLM round-trip with all emitted tool calls
- `sylk trace session <session-id> --agent <name>` — everything an agent did in a session
- `sylk trace session <session-id> --pipeline <pid>` — everything in a pipeline's lifetime

### No index

Reconstruction is entirely file-scan-based. Even at large session sizes (a day's worth is ~few hundred MB), `jq` over 10–50 JSONL files completes in under a second. No background indexer, no schema migration, no consistency window.

If performance becomes a problem later we can add an index — but today's bottleneck is availability of data, not retrieval speed.

---

## Config

```yaml
logging:
  enabled: true                       # kill switch
  rotation:
    max_bytes_per_file: 67108864      # 64 MiB
    timezone: "local"                 # or "UTC"; captured in session meta.json
  streams:
    events: true
    tools: true
    llm: true
    bus: false
  llm:
    content_address_prompts: true     # write system_prompt + tool_defs to prompts/
    inline_messages: true             # keep message history in-record (cannot be addressed)
    truncate_message_text_at: 16384   # per-message soft cap; "..." suffix on truncation
    truncate_tool_output_at: 65536    # output field in tools.jsonl
  redact:
    env_vars: [ANTHROPIC_API_KEY, OPENAI_API_KEY]
    field_names: [authorization, api_key, token, password, secret]
    patterns: []
    include_fingerprint: true
```

Agent-level overrides (`agents.engineer.logging.streams.bus: true`) layered on top of defaults.

---

## Migration

### Existing `~/.sylk/logs/*_debug.log`

Retired. These files are unstructured and out of scheme. Replaced by:

- Pre-session-bind events → `~/.sylk/logs/boot/{date}/events.jsonl` (the only user-level stream that remains).
- Everything else → `.sylk/sessions/{sid}/agents/{agent}/logs/{date}/events.jsonl` once the agent binds to a session.

Existing files are not migrated automatically; they can be deleted or archived by the user.

### Existing `SessionEventLogger`

The type stays and gains:

- A `streamKind` field so a single logger can emit to events / tools / llm / bus with a consistent correlation thread.
- A rotation-aware `writer` that resolves the current path on every append.
- A redactor chain installed at construction.

The public API (`LogInfo`, `LogWarning`, etc. in `agents/shared/request_logger.go`) keeps its shape; new helpers are added for tool-call and LLM streams.

### Existing WAL path

Unchanged. `ResolveSessionWALDir` stays in `core/agentlog/wal.go`.

---

## Scaffolding to Build

In rough dependency order:

1. **`core/agentlog/redactor.go`** — `Redactor` interface, three built-in implementations, chain, fingerprint helper.
2. **`core/agentlog/writer.go`** — `StreamWriter` with daily + size rotation; opens/closes file handles; takes a `Redactor`.
3. **`core/agentlog/corpus.go`** — content-addressed prompt store (`prompts/{sha}.txt`), dedupe-on-write.
4. **`core/agentlog/session_logger.go`** — extend existing to use the new writer and emit via stream kinds.
5. **`core/agentlog/tool_recorder.go`** — new; writes Phase 0 / Phase 1 tool records.
6. **`core/agentlog/llm_recorder.go`** — new; writes `llm_start` / `llm_complete`.
7. **Context helpers** — `LLMCallFromContext`, `WithLLMCall`, mirror of existing `ActiveToolCallFromContext`.
8. **Agent integration** — each agent's tool loop calls `tool_recorder.Phase0(...)` / `.Phase1(...)`; each provider adapter calls `llm_recorder.Start(...)` / `.Complete(...)`.
9. **`cmd/trace/`** — `sylk trace` subcommand.
10. **Config plumbing** — `logging.*` in `config.yaml`, agent-level overrides.
11. **Retirement of `~/.sylk/logs/*_debug.log`** — delete the direct-file-opens, route all remaining calls through the new logger.

Order 1–4 is the minimum to get working events/debug streams with redaction and rotation; 5–8 is the tool-call and LLM capture that solves the diagnostic problem; 9 is the payoff; 10–11 is polish.

---

## Open Questions

1. **Message history truncation strategy.** When a `messages` field exceeds the configured cap, do we (a) truncate tail-only, (b) keep first + last N with an elision marker in the middle, or (c) keep full and rely on 64 MB size rotation to bound damage? I'd default to (b) — first + last is usually what you need for replay.
2. **Redactor performance.** Regex redaction on large outputs (test logs, file dumps) has real cost. Do we cap redaction input size (e.g. skip regex scan on strings > 256 KB, still run field-name redaction) or accept the cost?
3. **Replay verification.** Do we want `sylk trace replay <llm-call-id>` that re-runs the captured LLM call against the current model to diff behavior, or is that out of scope for this round?
