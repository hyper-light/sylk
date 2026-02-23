---
name: pipeline-monitoring
description: Monitor pipeline agent health, task update buffers, and live agent state. Use when checking agent health, investigating task progress, querying pipeline agents for diagnostic state, or reviewing historical health trends. Covers query_agent_health, query_health_history, query_buffer, and query_pipeline_state skills.
---

# Pipeline Monitoring

Observe pipeline agent health and task execution progress across the system.

## Capabilities
- Query agent health metrics from hot cache or live monitor
- Query historical health trends via Archivalist
- Read per-task update buffers (hot circular buffer with SQLite cold fallback)
- Send live diagnostic queries to pipeline agents

## Decision framework

### Health degradation
1. Query agent health via `query_agent_health`
2. Broadcast a warning via `broadcast_status`
3. If persistent, escalate to architect via `escalate_to_architect`

### Task investigation
1. Query the task update buffer via `query_buffer` for incremental progress
2. If buffer is empty, query the pipeline agent directly via `query_pipeline_state`
3. Cross-reference with `query_task` for the orchestrator's task record

### Historical analysis
1. Query health history via `query_health_history` for trends
2. Query Archivalist for failure patterns via `archivalist_request`

## Skill reference
| Skill | Purpose |
|-------|---------|
| `query_agent_health` | Current health from cache or monitor |
| `query_health_history` | Historical health from Archivalist |
| `query_buffer` | Per-task update stream (hot then cold) |
| `query_pipeline_state` | Live query to a pipeline agent |
