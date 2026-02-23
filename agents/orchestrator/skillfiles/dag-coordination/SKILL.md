---
name: dag-coordination
description: Coordinate DAG execution lifecycle from architect plans. Use when the architect dispatches a plan for execution, when a running DAG needs modification or cancellation, or when DAG progress needs monitoring. Covers execute_dag, cancel_dag, modify_dag, and query_dag_status skills.
---

# DAG Coordination

Manage the full lifecycle of architect plan execution through the DAG scheduler.

## Capabilities
- Execute DAGs from architect plan dispatch (via bus or direct skill invocation)
- Cancel running DAGs with reason tracking
- Modify running DAGs mid-flight (add/remove nodes) at architect request
- Query DAG status from the live scheduler or historical SQLite store
- Recover incomplete DAGs after crash via WAL replay

## Decision framework

### DAG execution request
1. Execute via `execute_dag` with the plan's DAG JSON
2. Monitor progress via `query_dag_status`
3. Query pipeline agent buffers via `query_buffer` for per-task detail
4. On completion or failure, broadcast status

### DAG modification
1. Apply via `modify_dag` with the architect's modification payload
2. Monitor the modified DAG for stability via `query_dag_status`

### DAG failure
1. Query status via `query_dag_status`
2. Query task buffers for failed nodes via `query_buffer`
3. Escalate to architect with failure context via `escalate_to_architect`

## Skill reference
| Skill | Purpose |
|-------|---------|
| `execute_dag` | Submit DAG to scheduler for async execution |
| `cancel_dag` | Cancel a running DAG with reason |
| `modify_dag` | Add/remove nodes mid-flight |
| `query_dag_status` | Get live or historical DAG status |
