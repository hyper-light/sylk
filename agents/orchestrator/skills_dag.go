package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

func executeDAGSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("execute_dag").
		Description("Execute a DAG from an architect plan. Deserializes the DAG JSON, journals to WAL, persists to SQLite, and submits to the DAG scheduler for async layer-by-layer execution.").
		Domain("orchestration").
		Keywords("dag", "execute", "plan", "run", "start", "launch").
		Priority(95).
		Usage("Use when the architect dispatches a plan for execution. The dag_json must be a valid JSON-serialized DAG produced by the architect's plan builder. Execution is asynchronous — use query_dag_status to monitor progress. Do NOT call this for plans that are still being revised by the architect.").
		StringParam("plan_id", "Plan ID from the architect (used for tracking and WAL correlation)", true).
		StringParam("dag_json", "JSON-serialized DAG from the architect plan (must be valid dag.DAG JSON)", true).
		Example(`{"plan_id": "plan_abc123", "dag_json": "{\"id\":\"dag_1\",\"name\":\"build\",\"nodes\":[...],\"policy\":{...}}"}`).
		BestPractice("Always verify the plan_id corresponds to an active architect plan before executing.").
		BestPractice("The DAG is validated on deserialization — if dag_json is malformed, the error message will indicate the JSON parse failure.").
		BestPractice("After execution starts, immediately call query_dag_status to confirm the scheduler accepted the DAG.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				PlanID  string `json:"plan_id"`
				DAGJSON string `json:"dag_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			d := &dag.DAG{}
			if err := d.UnmarshalJSON([]byte(params.DAGJSON)); err != nil {
				return nil, fmt.Errorf("invalid dag_json: %w", err)
			}

			dagID, err := o.dagBridge.Execute(ctx, d, params.PlanID, o.config.SessionID)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"executed": true,
				"dag_id":   dagID,
				"plan_id":  params.PlanID,
			}, nil
		}).
		Build()
}

func cancelDAGSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("cancel_dag").
		Description("Cancel a running DAG execution. Cancels all active nodes, journals to WAL, and marks the DAG as cancelled in SQLite.").
		Domain("orchestration").
		Keywords("dag", "cancel", "stop", "abort", "halt").
		Priority(90).
		Usage("Use when the architect requests cancellation, when a critical failure makes continued execution pointless, or when the user requests a stop. Always provide a meaningful reason — it is persisted for audit. Do NOT cancel a DAG that has already completed or failed.").
		StringParam("dag_id", "DAG execution ID to cancel", true).
		StringParam("reason", "Reason for cancellation (persisted for audit trail)", true).
		Example(`{"dag_id": "dag_abc123", "reason": "Architect requested plan revision after discovering new requirements"}`).
		Example(`{"dag_id": "dag_abc123", "reason": "Critical node failure on build_core — remaining nodes depend on it"}`).
		BestPractice("Verify the DAG is still active with query_dag_status before attempting cancellation.").
		BestPractice("After cancellation, broadcast a status update so the UI reflects the change.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				DAGID  string `json:"dag_id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if err := o.dagBridge.Cancel(params.DAGID, params.Reason); err != nil {
				return nil, err
			}

			return map[string]any{
				"cancelled": true,
				"dag_id":    params.DAGID,
				"reason":    params.Reason,
			}, nil
		}).
		Build()
}

func modifyDAGSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("modify_dag").
		Description("Modify a running DAG mid-flight by adding or removing nodes. Validates the modification, journals to WAL, persists the revision to SQLite, and applies changes to the live scheduler.").
		Domain("orchestration").
		Keywords("dag", "modify", "update", "change", "add", "remove", "revision").
		Priority(85).
		Usage("Use when the architect needs to adapt a running plan — adding new tasks discovered during execution or removing tasks that are no longer needed. The modification_json must be a valid DAGModification JSON. The DAG must be actively running. Do NOT modify completed or cancelled DAGs.").
		StringParam("dag_id", "DAG execution ID to modify", true).
		StringParam("modification_json", "JSON-serialized DAGModification with add_nodes and/or remove_nodes arrays", true).
		StringParam("reason", "Reason for modification (persisted with revision for audit trail)", true).
		Example(`{"dag_id": "dag_abc123", "modification_json": "{\"add_nodes\":[{\"id\":\"lint_new\",\"agent_type\":\"inspector\",\"prompt\":\"Lint the new module\"}],\"remove_nodes\":[\"optional_docs\"]}", "reason": "New module discovered during build — needs linting, docs deferred"}`).
		BestPractice("Verify the DAG is still active with query_dag_status before modifying.").
		BestPractice("Adding nodes with dependencies on completed nodes is valid — they will execute immediately.").
		BestPractice("Removing nodes that are currently executing will cancel them.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				DAGID            string `json:"dag_id"`
				ModificationJSON string `json:"modification_json"`
				Reason           string `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			var mod DAGModification
			if err := json.Unmarshal([]byte(params.ModificationJSON), &mod); err != nil {
				return nil, fmt.Errorf("invalid modification_json: %w", err)
			}
			mod.Reason = params.Reason

			if err := o.dagBridge.Modify(params.DAGID, &mod); err != nil {
				return nil, err
			}

			return map[string]any{
				"modified": true,
				"dag_id":   params.DAGID,
				"reason":   params.Reason,
			}, nil
		}).
		Build()
}

func queryBufferSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("query_buffer").
		Description("Read the task update buffer for a specific task. Returns entries from the hot in-memory circular buffer first, falls back to cold SQLite storage. Response includes source field for observability.").
		Domain("orchestration").
		Keywords("buffer", "task", "updates", "pipeline", "progress", "stream").
		Priority(80).
		Usage("Use when you need the stream of incremental progress updates for a task — status transitions, progress percentages, messages from pipeline agents. This is the primary tool for understanding what a running task is doing. For point-in-time task state, use query_task instead.").
		StringParam("task_id", "Task ID to query updates for", true).
		IntParam("limit", "Maximum entries to return (default 20)", false).
		Example(`{"task_id": "task_abc123", "limit": 10}`).
		Example(`{"task_id": "task_abc123"}`).
		BestPractice("Start with the default limit of 20 — increase only if you need deeper history.").
		BestPractice("Check the source field: 'buffer' means entries came from the hot in-memory ring, 'empty' means no entries exist for this task.").
		BestPractice("If the buffer is empty for a task you expect to have updates, the task may not have started yet or the buffer may have been GC'd — check the task status with query_task.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID string `json:"task_id"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.Limit == 0 {
				params.Limit = 20
			}

			entries := o.bufferRegistry.Read(params.TaskID, params.Limit)

			source := "buffer"
			if len(entries) == 0 {
				source = "empty"
			}

			return map[string]any{
				"task_id": params.TaskID,
				"count":   len(entries),
				"entries": entries,
				"source":  source,
			}, nil
		}).
		Build()
}

func queryPipelineStateSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("query_pipeline_state").
		Description("Send a live query to a pipeline agent and await response. Publishes a PipelineQuery to the agent's query topic and polls for the response with a 10-second timeout.").
		Domain("orchestration").
		Keywords("pipeline", "state", "query", "agent", "live", "diagnostic").
		Priority(75).
		Usage("Use when you need real-time diagnostic state from a specific pipeline agent — such as its current work item, internal status, or output. This is a synchronous blocking call with a 10-second timeout. For cached/historical state, use query_buffer or query_agent_health instead. query_type values: status (current state), output (last output), diagnostic (internal diagnostics).").
		StringParam("agent_id", "Target agent ID to query", true).
		StringParam("query_type", "Query type: status, output, or diagnostic", true).
		StringParam("dag_id", "DAG ID context for scoping the query", false).
		StringParam("node_id", "Node ID context for scoping the query", false).
		Example(`{"agent_id": "engineer", "query_type": "status"}`).
		Example(`{"agent_id": "engineer", "query_type": "diagnostic", "dag_id": "dag_abc123", "node_id": "build_core"}`).
		BestPractice("Prefer query_buffer for task progress — this skill is for live agent diagnostics when buffer data is insufficient.").
		BestPractice("If the agent does not respond within 10 seconds, the result will indicate timeout — do not retry immediately, the agent may be overloaded.").
		BestPractice("Provide dag_id and node_id when available to scope the query to a specific execution context.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				AgentID   string `json:"agent_id"`
				QueryType string `json:"query_type"`
				DAGID     string `json:"dag_id"`
				NodeID    string `json:"node_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			o.mu.RLock()
			bus := o.bus
			o.mu.RUnlock()

			if bus == nil {
				return nil, fmt.Errorf("orchestrator not running")
			}

			queryID := uuid.New().String()
			query := &PipelineQuery{
				QueryID:   queryID,
				DAGID:     params.DAGID,
				NodeID:    params.NodeID,
				QueryType: params.QueryType,
			}

			msg := &guide.Message{
				ID:            generateMessageID(),
				Type:          guide.MessageTypePipelineQuery,
				SourceAgentID: o.config.AgentID,
				TargetAgentID: params.AgentID,
				Payload:       query,
				Timestamp:     time.Now(),
			}

			if err := bus.Publish("pipeline.query."+params.AgentID, msg); err != nil {
				return nil, fmt.Errorf("publish pipeline query: %w", err)
			}

			// Await response with timeout
			timeout := 10 * time.Second
			deadline := time.After(timeout)

			// Check for cached response periodically
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-deadline:
					return map[string]any{
						"query_id": queryID,
						"timeout":  true,
						"message":  "pipeline agent did not respond within timeout",
					}, nil
				case <-ticker.C:
					// Check if response has arrived (stored in pipeline state)
					if stateJSON, err := o.store.GetPipelineState(params.AgentID, params.DAGID, params.NodeID); err == nil && stateJSON != "" {
						var state any
						json.Unmarshal([]byte(stateJSON), &state)
						return map[string]any{
							"query_id": queryID,
							"timeout":  false,
							"state":    state,
							"source":   "store",
						}, nil
					}
				}
			}
		}).
		Build()
}

func queryDAGStatusSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("query_dag_status").
		Description("Get current status of a DAG execution. Checks the live scheduler first for active DAGs, falls back to SQLite for historical/completed DAGs. Response includes source field indicating data origin.").
		Domain("orchestration").
		Keywords("dag", "status", "progress", "execution", "layer", "nodes").
		Priority(85).
		Usage("Use when you need the overall DAG execution state — current layer, node success/failure counts, and progress percentage. For per-task detail within the DAG, use query_buffer with individual task IDs. The source field indicates whether data came from the live scheduler (active DAG) or SQLite store (completed/historical).").
		StringParam("dag_id", "DAG execution ID to query", true).
		Example(`{"dag_id": "dag_abc123"}`).
		BestPractice("Check the active field — true means the DAG is still running in the scheduler, false means it completed and was loaded from SQLite.").
		BestPractice("If found is false, the DAG ID may be incorrect or the execution may have been cleaned up.").
		BestPractice("For active DAGs, the status reflects real-time scheduler state. For historical DAGs, it reflects the final state at completion.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				DAGID string `json:"dag_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			// Try scheduler (active DAGs)
			status, err := o.dagBridge.Status(params.DAGID)
			if err == nil {
				return map[string]any{
					"found":  true,
					"active": true,
					"status": status,
					"source": "scheduler",
				}, nil
			}

			// Fall back to SQLite (historical)
			row, err := o.store.GetDAGExecution(params.DAGID)
			if err != nil {
				return nil, err
			}
			if row == nil {
				return map[string]any{"found": false, "dag_id": params.DAGID}, nil
			}

			return map[string]any{
				"found":  true,
				"active": false,
				"status": row,
				"source": "store",
			}, nil
		}).
		Build()
}
