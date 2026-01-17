# Architecture Gaps - Priority Fix List

This document tracks gaps in ARCHITECTURE.md that need to be addressed, prioritized for backend-first development.

---

## TIER 1: Agent Infrastructure (Immediate)
*Core infrastructure for agents to function reliably*

### 1.1 LLM API Management
- [x] Rate limiting (token bucket per provider) → TODO 0.10
- [x] Retry with exponential backoff → TODO 0.10
- [x] Token budget per session/task → TODO 0.13
- [x] Context window overflow handling → TODO 0.14
- [x] Multi-provider support (Anthropic, Google, OpenAI) → TODO 0.7
- [x] Cost tracking per session → TODO 0.13
- [x] Request queuing under load → TODO 0.9
- [x] Timeout handling → TODO 0.10, 0.11

### 1.2 Concurrency Model
- [x] Agent executor pool design → TODO 0.17 Goroutine Model (standalone agents unlimited, pipelines bounded)
- [x] Pipeline scheduler → TODO 0.18 Pipeline Scheduler (N_CPU_CORES limit, priority queue)
- [x] Channel-based message passing → TODO 0.11 Signal Bus, 0.21 Adaptive Channels
- [x] Synchronization primitives → TODO 0.11, 0.12 (Signal ack, checkpointing)
- [x] Deadlock prevention strategy → TODO 0.19 (user preemption), 0.20 (staging isolation), 0.21 (adaptive channels)
- [x] Backpressure handling → TODO 0.9 Priority Queue, 0.10 Rate Limiter, 0.19 Dual Queue Gate
- [x] Worker pool sizing → TODO 0.18 (N_CPU_CORES for pipelines only)

### 1.3 State Persistence Strategy
- [x] In-memory vs. persisted boundaries → ARCHITECTURE.md Concurrency section (staging vs working dir)
- [x] Write-ahead logging for crash recovery → TODO 0.22 Write-Ahead Log
- [x] Checkpoint strategy (when/what) → TODO 0.12, 0.23 Checkpointer (5s interval, pause triggers)
- [x] State serialization format → TODO 0.23 (JSON checkpoint with version/hash)
- [x] Recovery procedures → TODO 0.12, 0.24 Recovery Manager (checkpoint + WAL replay)
- [x] Corruption detection → TODO 0.22 (CRC per entry), 0.23 (checkpoint hash validation)

### 1.4 Error Propagation & Recovery
- [x] Error type taxonomy (transient, permanent, user-fixable) → TODO 0.26 (5-tier: +ExternalRateLimit, +ExternalDegrading)
- [x] Retry policies per error type → TODO 0.28 (per-tier configurable policies)
- [x] Escalation paths (agent → architect → user) → TODO 0.30 (token-budgeted Architect workarounds)
- [x] Circuit breaker pattern → TODO 0.29 (per-resource configurable thresholds)
- [x] Partial failure handling → TODO 0.30, 0.31 (user choice + retry briefing)
- [x] Rollback strategies → TODO 0.32 (4-layer rollback with history preservation)

### 1.5 Resource Constraints
- [x] Memory limits and monitoring → TODO 0.33 (per-component budgets, token-weighted eviction)
- [x] Concurrent operation limits → TODO 0.34 (file/network/subprocess pools with user reservation)
- [x] Disk usage quotas → TODO 0.35 (auto-scaling bounded percentage)
- [x] Graceful degradation under pressure → TODO 0.37 (priority-based pause/resume)
- [x] Resource acquisition ordering (prevent deadlock) → TODO 0.36 (Resource Broker, all-or-nothing)

---

## TIER 2: Tool Execution Layer (Soon)
*Infrastructure for agents to execute external tools*

### 2.1 Subprocess Management
- [x] Process spawning abstraction → TODO 0.43 (hybrid direct/shell execution)
- [x] Output streaming (stdout/stderr) → TODO 0.46 (tee to user + buffer)
- [x] Exit code handling → TODO 0.43 (full context, agent interprets)
- [x] Timeout enforcement → TODO 0.44 (adaptive timeout with noise detection)
- [x] Kill signal propagation → TODO 0.45 (SIGINT→SIGTERM→SIGKILL escalation)
- [x] Orphan process prevention → TODO 0.43 (process groups Unix, Job Objects Windows)
- [x] Environment variable handling → TODO 0.43 (curated inherit with blocklist)
- [x] Working directory management → TODO 0.43, 0.49 (boundary validation)

### 2.2 Filesystem Operations
- [x] Read/write/delete abstractions → TODO 0.49 (hybrid: direct reads, abstracted writes)
- [x] Permission checking → TODO 0.49 (pre-check + try-handle)
- [x] Temp file lifecycle → TODO 0.49 (per-session/pipeline hierarchy)
- [x] File locking strategy → Handled by staging isolation (0.20)
- [x] Symlink handling → TODO 0.49 (boundary-aware, block escapes)
- [x] Path normalization (cross-platform) → TODO 0.49 (filepath package as skill/tool)

### 2.3 Output Capture & Parsing
- [x] Structured output capture → TODO 0.46 (streaming + buffer)
- [x] Stream vs. buffered modes → TODO 0.46 (hybrid: stream to user, buffer for agent)
- [x] Output size limits → TODO 0.46 (smart truncation, keep important lines)
- [x] Parsing for common tools (go, npm, git) → TODO 0.47 (parser registry)
- [x] Error extraction patterns → TODO 0.46 (importance detector)

### 2.4 Tool Cancellation
- [x] Context propagation to subprocesses → TODO 0.50 (cascading timeouts)
- [x] Graceful vs. forced termination → TODO 0.45 (3-stage kill sequence)
- [x] Cleanup after cancellation → TODO 0.50 (best-effort within budget)
- [x] Partial result handling → TODO 0.50 (preserve + mark as partial)

### 2.5 Multi-Session Coordination (NEW)
- [x] Session registry → TODO 0.39 (SQLite WAL)
- [x] Fair share allocation → TODO 0.40 (activity-weighted)
- [x] Cross-session signaling → TODO 0.41 (fsnotify-based)
- [x] Cross-session resource pools → TODO 0.42 (preemption across sessions)

### 2.6 Tool Optimization (NEW)
- [x] Output caching → TODO 0.51 (deterministic tool cache)
- [x] Invocation batching → TODO 0.52 (multi-file tools)
- [x] Streaming parsing → TODO 0.53 (real-time error detection)
- [x] Parse template learning → TODO 0.48 (LLM-learned patterns via Archivalist)

---

## TIER 3: Storage & Configuration (After Core Works)
*Local storage layout and configuration*

### 3.1 Directory Layout
- [x] `~/.sylk/` structure definition → XDG semantics with platform-native defaults (TODO 0.61)
- [x] Session storage location → data/sessions/{id}/ (TODO 0.61)
- [x] Cache directory → cache/ with TTL tiers (hot/warm/cold) (TODO 0.61)
- [x] Log directory → state/logs/ with dual-write (human + structured) (TODO 0.61)
- [x] Database location → data/shared/ + data/projects/{hash}/ (TODO 0.64)
- [x] Temp directory → state/temp/ + $TMPDIR/sylk-{id}/ hybrid (TODO 0.61)

### 3.2 Configuration Schema
- [x] Config file format (YAML/TOML/JSON) → YAML with JSON Schema validation (TODO 0.62)
- [x] Schema definition → Full JSON Schema with IDE autocomplete (TODO 0.62)
- [x] Validation rules → Warn and use defaults (don't refuse to start) (TODO 0.62)
- [x] Default values → Documented in schema, code defaults (TODO 0.62)
- [x] Environment variable overrides → SYLK_* flat + _FILE for complex (TODO 0.62)
- [x] Project-local config (`.sylk/`) → config.yaml (committed) + local/ (gitignored) (TODO 0.61, 0.62)

### 3.3 Credential Storage
- [x] API key encryption at rest → AES-256-GCM with machine-bound key (TODO 0.63)
- [x] Keychain/credential manager integration → Platform-native with encrypted fallback (TODO 0.63)
- [x] Key rotation support → Verify before saving, no backup of old keys (TODO 0.63)
- [x] Multiple provider credentials → Named profiles + project-scoped selection (TODO 0.63)

### 3.4 Database Management
- [x] SQLite location and naming → system.db (global), knowledge.db + index.db (per-project) (TODO 0.64)
- [x] Migration strategy → Embedded versioning, backup before destructive (TODO 0.64)
- [x] Backup/restore → Periodic + pre-session + pre-migration (TODO 0.65)
- [x] Corruption recovery → Continuous integrity + auto-restore + rebuild (TODO 0.66)

---

## TIER 4: Security Model (Before External Use)
*Security boundaries and audit*

### 4.1 Agent Permission Boundaries
- [x] File access restrictions per agent → TODO 0.68 (PathPerm with Read/Write/Delete, role-based)
- [x] Network access restrictions → TODO 0.68 (domain allowlist, per-project persistent)
- [x] Process execution restrictions → TODO 0.68 (command allowlist, ignores args, safe defaults)
- [x] Permission inheritance in pipelines → TODO 0.68 (WorkflowPermissions, pre-declared + runtime escalation)

### 4.2 Sandboxing Strategy
- [x] Generated code execution isolation → TODO 0.69 (OS sandbox: bubblewrap/Seatbelt, OFF by default)
- [x] Resource limits for executed code → TODO 0.69 (CPU, memory, files, processes configurable)
- [x] Filesystem isolation → TODO 0.69 (VFS layer with CoW, path boundary enforcement)
- [x] Network isolation → TODO 0.69 (network proxy with domain allowlist)

### 4.3 Audit Logging
- [x] What actions to log → TODO 0.70 (all: permission, file, process, network, llm, session, config)
- [x] Log format and storage → TODO 0.70 (append-only JSONL with cryptographic chaining)
- [x] Retention policy → TODO 0.70 (indefinite until explicit purge)
- [x] Query interface → TODO 0.71 (CLI with rich filters: category, severity, time, target pattern)

### 4.4 Session Isolation
- [x] Multi-user considerations → ARCHITECTURE.md (single-user assumption, no OS-level isolation)
- [x] Session data privacy → TODO 0.73 (project-scoped sharing, session knowledge read-only to others)
- [x] Credential isolation → TODO 0.72 (profile override, temp credentials not shared)

---

## TIER 5: CLI/Terminal Layer (Frontend)
*User-facing presentation layer*

### 5.1 Command Structure
- [ ] Command taxonomy
- [ ] Argument parsing
- [ ] Subcommand design
- [ ] Global flags
- [ ] Help system

### 5.2 Terminal Rendering
- [ ] Progress indicators
- [ ] Streaming output
- [ ] Color/formatting
- [ ] Terminal size handling
- [ ] Markdown rendering

### 5.3 User Interaction
- [ ] Prompt design
- [ ] Approval workflows
- [ ] Input validation
- [ ] Selection interfaces

### 5.4 Signal Handling
- [ ] SIGINT (Ctrl-C)
- [ ] SIGTERM
- [ ] Graceful shutdown sequence
- [ ] State preservation on interrupt

### 5.5 Shell Integration
- [ ] Tab completion
- [ ] Shell aliases
- [ ] Prompt integration

---

## TIER 6: Polish & Distribution (Post-Launch)
*Improvements after initial release*

### 6.1 Offline Mode
- [ ] Offline detection
- [ ] Graceful degradation
- [ ] Sync on reconnect

### 6.2 Multi-Platform
- [ ] macOS specifics
- [ ] Linux specifics
- [ ] Windows specifics
- [ ] ARM vs x86

### 6.3 Updates & Distribution
- [ ] Version checking
- [ ] Self-update mechanism
- [ ] Migration scripts
- [ ] Release channels

### 6.4 Observability
- [ ] Structured logging
- [ ] Debug modes
- [ ] Performance profiling
- [ ] Optional telemetry

---

## Progress Tracking

| Tier | Status | Notes |
|------|--------|-------|
| 1. Agent Infrastructure | COMPLETE | 1.1 LLM API → 0.7-0.16; 1.2 Concurrency → 0.17-0.25; 1.3 State → 0.22-0.24; 1.4 Errors → 0.26-0.32; 1.5 Resources → 0.33-0.38; **Multi-Session Amendments → 0.55-0.60** |
| 2. Tool Execution | COMPLETE | 2.1-2.4 Subprocess/FS/Output/Cancel → 0.43-0.50; 2.5 Multi-Session → 0.39-0.42; 2.6 Optimization → 0.51-0.53, 0.54 |
| 3. Storage & Config | COMPLETE | 3.1 Directory → 0.61; 3.2 Config → 0.62; 3.3 Credentials → 0.63; 3.4 Database → 0.64-0.66; Integration → 0.67. **NO DEPS on Tier 1/2 - fully parallel** |
| 4. Security Model | COMPLETE | 4.1 Permissions → 0.68; 4.2 Sandboxing → 0.69; 4.3 Audit → 0.70-0.71; 4.4 Session → 0.72-0.73; Integration → 0.74. **NO DEPS on Tier 1/2/3 - fully parallel** |
| 5. CLI/Terminal | NOT STARTED | |
| 6. Polish | NOT STARTED | |
