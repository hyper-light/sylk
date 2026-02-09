# WAL + Adaptive Delta-Checkpoint: Implementation TODO

> **Status**: DEFERRED — design complete, implementation pending.
> **Full plan**: `.claude/plans/tidy-weaving-pumpkin.md`

---

## WAL Surface Map

| # | WAL | Location | Exists? | Phase |
|---|-----|----------|---------|-------|
| 1 | Session Version WAL | `sessions/{id}/wal/` | NO | A |
| 2 | IVF Vector WAL | `knowledge/vectors/ivf.wal.*` | YES (`vamana/ivf/wal.go`) | A (integrate) |
| 3 | Global Commit WAL | `knowledge/wal/` | NO | A |
| 4 | Bleve | `bleve/index/documents.bleve` | Bleve-internal (Scorch) | B (wire lifecycle) |
| 5 | Global Edge Shard WAL | `knowledge/edges/shard_NNNN/*.wal` | NO | Future (Phase 7+) |
| 6 | Session State WAL | orchestration layer | NO | Future |
| 7 | TC Computation WAL | `tc_computation.wal` | NO | Future |

**Reuse strategy**: WALs 1, 2, 3 share the `vamana/wal` wire format. Extend `OpType` enum rather than inventing a new binary format.

---

## Phase A: Write-Ahead Logs

### A.1 Extend `vamana/wal/types.go` OpType Enum

- [ ] Add session version ops: `OpNodeInsert(16)`, `OpNodeDelete(17)`, `OpEdgeInsert(18)`, `OpEdgeUpdate(19)`, `OpEdgeDelete(20)`, `OpVectorInsert(21)`, `OpDocInsert(22)`
- [ ] Add global commit ops: `OpCommitBegin(32)`, `OpCommitNode(33)`, `OpCommitEdge(34)`, `OpCommitIndex(35)`, `OpCommitEnd(36)`
- [ ] Add structural markers: `OpVersionCheckpoint(48)`, `OpSessionCommit(49)`, `OpShardSeal(50)`
- [ ] Update `Valid()` and `String()` methods
- [ ] Update `types_test.go`

### A.2 Session Version WAL

- [ ] `core/storage/sylkdir/session_wal.go` — wrapper around `ivf.WAL` for session mutations
  - Init WAL at `sessions/ses_NNN/wal/`
  - Replay: dispatch by `OpType` to version store in-memory indices
  - Close: sync and release
- [ ] `core/storage/sylkdir/session_wal_test.go` — append all entity types, replay, CRC, concurrent writers, segment rotation
- [ ] Modify `version_store.go` write path: WAL append + in-memory index (defer `*.bin` to compaction)
- [ ] Modify `session_store.go`: add WAL field to Session, init on Create/Load
- [ ] Modify `sylkdir.go` Init: create `wal/` in session dirs

### A.3 Global Commit WAL

- [ ] `core/storage/sylkdir/commit_wal.go` — crash-atomic CommitToGlobal
  - `OpCommitBegin` → log all nodes/edges → `OpCommitEnd` → fsync → materialize → GC
  - Recovery: if `OpCommitEnd` absent, discard incomplete commit
- [ ] `core/storage/sylkdir/commit_wal_test.go` — atomic commit, crash-mid-commit recovery, double-commit prevention
- [ ] Modify `commit.go`: wrap CommitToGlobal with commit WAL
- [ ] Modify `sylkdir.go` Init: create `knowledge/wal/`

### A.4 IVF WAL Integration

- [ ] Wire IVF WAL into checkpoint controller (monitor WAL size as recovery debt)
- [ ] On session commit: push staged vectors to IVF via `StitchBatch`
- [ ] IVF WAL already handles its own durability — no structural changes needed

### A.5 Bleve Lifecycle Wiring

- [ ] On `CommitToGlobal`: index `CommitResult.StagedDocs` into `BleveStore`
- [ ] On crash recovery: re-index docs from commits that completed but weren't indexed in Bleve
- [ ] No per-session Bleve index — sessions use `VersionDocStore` JSONL; Bleve holds only committed data

---

## Phase B: Adaptive Checkpoint Controller

### B.1 Calibration Probe

- [ ] `core/storage/sylkdir/calibration.go`
  - `CalibrationResult`: fsync latency, replay throughput, seq read speed, random write speed
  - Quick probe every startup (<100ms): 5x (write 4KB, fsync), median
  - Full probe (first run + every 50th): + 256KB sequential read, 10x 4KB scattered writes
  - EMA smoothing: `alpha = 2/(ProbeCount+1)`
  - Persist `.sylk/calibration.json`
- [ ] `core/storage/sylkdir/calibration_test.go`

### B.2 Checkpoint Controller

- [ ] `core/storage/sylkdir/checkpoint_controller.go`
  - **Trigger inequality**: `D_wal/(1-c(t)) > tau(c)/(1+mu(t))`
    - `D_wal = WAL_bytes / replay_throughput` (recovery debt)
    - `c(t) = blocked_writers / total_writers` (contention ratio, capped at 0.99)
    - `tau(c) = fsync_latency * F * (1+c(t))` where F=10 (interactive) or F=K*10 (batch)
    - `mu(t)` = memory pressure [0,1]
  - **Granularity selection**:
    - Light: `D_wal < fsync_latency` — fsync only
    - Standard: seal + compact + truncate + patch bump
    - Full Merge: standard + rewrite for locality (`write_amp > seq_read/rand_write`)
  - Non-blocking advisory lock (`syscall.Flock` + `LOCK_NB`)
  - Signal handler: `SIGINT`/`SIGTERM` → fsync active segment → exit (no compaction)
- [ ] `core/storage/sylkdir/checkpoint_controller_test.go`

### B.3 DeltaTracker

- [ ] `core/storage/sylkdir/delta_tracker.go`
  - Atomic counters: `nodesCreated`, `edgesCreated`, `edgesModified`, `vectorsCreated`, `docsBytes`
  - `lastCheckpointAt`, `lastCheckpointVer`
  - Reset protocol: snapshot counters → Store(0) → persist `delta/tracker.json` v2
  - Backward compatible with v1 (missing `schema_version` → migrate)
- [ ] `core/storage/sylkdir/delta_tracker_test.go`

### B.4 Memory Pressure

- [ ] `core/storage/sylkdir/mem_pressure.go`
  - `runtime.MemStats`: `HeapInuse / HeapSys`
  - Linux: `/proc/meminfo` → `1 - (MemAvailable / MemTotal)`
  - Darwin: `sysctl vm.page_pageable_internal_count`
  - Return `max(go_pressure, sys_pressure)`, capped at 1.0
- [ ] `core/storage/sylkdir/mem_pressure_test.go`

### B.5 Integration Points

- [ ] `SessionStore.Create()`: create `wal/` dir, init session WAL, init DeltaTracker, init CheckpointController, register signal handler
- [ ] `SessionStore.Load()`: replay session WAL → rebuild indices, measure replay throughput → update calibration
- [ ] `SessionIngestion.IngestCodeGraph()` / `IngestWithContent()`: call `ShouldCheckpoint()` after batch writes
- [ ] `Session.Checkpoint()`: controller executes compaction by granularity, resets DeltaTracker
- [ ] `CommitToGlobal()`: use commit WAL, index staged docs in Bleve, push staged vectors to IVF

---

## Verification

```bash
# Phase A
go test -v -run "TestWAL|TestSessionWAL|TestCommitWAL" ./core/storage/sylkdir/... ./core/vectorgraphdb/vamana/...
go test -race -run "TestWAL|TestSessionWAL" ./core/storage/sylkdir/...
go test -bench="BenchmarkWAL" -benchmem ./core/storage/sylkdir/... ./core/vectorgraphdb/vamana/...

# Phase B
go test -v -run "TestCheckpoint|TestDeltaTracker|TestCalibration|TestMemPressure" ./core/storage/sylkdir/...
go test -bench="BenchmarkCheckpoint|BenchmarkCalibration" -benchmem ./core/storage/sylkdir/...

# Full regression
go test -v ./core/storage/sylkdir/...
```

---

## Key Design Decisions

1. **No hardcoded thresholds** — all parameters derived from runtime calibration (fsync latency, replay throughput, memory pressure)
2. **Reuse existing WAL** — `vamana/wal/types.go` wire format + `vamana/ivf/wal.go` implementation; extend `OpType` enum
3. **WAL first, then checkpoint** — Phase A provides durability; Phase B adds adaptive auto-checkpointing on top
4. **Commit atomicity via WAL markers** — `OpCommitBegin`/`OpCommitEnd` bracket; absent end = incomplete = discard on recovery
5. **No per-session Bleve** — sessions use JSONL in version folders; Bleve indexes only committed global data
6. **Signal-aware** — `SIGINT`/`SIGTERM` trigger emergency fsync, no compaction under signal pressure
