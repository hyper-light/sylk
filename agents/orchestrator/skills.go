package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

func (o *Orchestrator) registerCoreSkills() {
	o.skills.Register(queryTaskSkill(o))
	o.skills.Register(queryWorkflowSkill(o))
	o.skills.Register(pushStatusSkill(o))
	o.skills.Register(generateSummarySkill(o))
	o.skills.Register(reportFailureSkill(o))
	o.skills.Register(submitTaskEventSkill(o))
	o.skills.Register(archivalistRequestSkill(o))
	o.skills.Register(readPlanFileSkill(o))
	o.skills.Register(escalateToArchitectSkill(o))
	o.skills.Register(broadcastStatusSkill(o))
	o.skills.Register(queryAgentHealthSkill(o))
	o.skills.Register(queryHealthHistorySkill(o))

	// DAG execution skills (registered after store/bridge are wired)
	o.skills.Register(executeDAGSkill(o))
	o.skills.Register(cancelDAGSkill(o))
	o.skills.Register(modifyDAGSkill(o))
	o.skills.Register(queryBufferSkill(o))
	o.skills.Register(queryPipelineStateSkill(o))
	o.skills.Register(queryDAGStatusSkill(o))

	// Plan ingestion and analysis skills
	o.skills.Register(ingestPlanSkill(o))
	o.skills.Register(analyzePlanSkill(o))

	// Diagnostics
	o.skills.Register(shared.NewSelfDiagnosticSkill(&orchestratorDiag{o: o}))

	for _, name := range orchestratorCoreSkillNames() {
		o.skills.Load(name)
	}
}

func orchestratorCoreSkillNames() []string {
	return []string{
		"query_task", "query_workflow", "push_status",
		"generate_summary", "report_failure",
		"submit_task_event", "archivalist_request",
		"read_plan_file", "escalate_to_architect",
		"broadcast_status", "query_agent_health",
		"query_health_history",
		"execute_dag", "cancel_dag", "modify_dag",
		"query_buffer", "query_pipeline_state",
		"query_dag_status",
		"ingest_plan", "analyze_plan",
		"self_diagnostic",
	}
}

type orchestratorDiag struct{ o *Orchestrator }

func (d *orchestratorDiag) AgentName() string  { return "orchestrator" }
func (d *orchestratorDiag) SessionID() string  { return "" }
func (d *orchestratorDiag) LogsDir() string    { return shared.LogsDirForAgent(d.o.steering.SessionDir(), "orchestrator") }
func (d *orchestratorDiag) EventLogger() *agentlog.SessionEventLogger { return d.o.steering.EventLogger() }
func (d *orchestratorDiag) RecoveryHints() []string                   { return nil }

func (d *orchestratorDiag) PeerLogsDirs() map[string]string {
	sessionDir := d.o.steering.SessionDir()
	if sessionDir == "" {
		return nil
	}
	peers := make(map[string]string)
	for _, name := range []string{"engineer", "designer", "inspector_pipeline", "tester_pipeline"} {
		dir := shared.LogsDirForAgent(sessionDir, name)
		if dir != "" {
			peers[name] = dir
		}
	}
	return peers
}

func (d *orchestratorDiag) AgentSpecificDiagnostics() map[string]any {
	d.o.mu.RLock()
	defer d.o.mu.RUnlock()
	result := map[string]any{
		"running": d.o.running,
	}
	if d.o.dagBridge != nil {
		result["dag_bridge_active"] = true
	}
	return result
}

func queryTaskSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("query_task").
		Description("Query the status of a specific task by ID. Returns the full TaskRecord including status, assigned agent, timing, result, and error.").
		Domain("orchestration").
		Keywords("task", "status", "query", "lookup").
		Priority(90).
		Usage("Use when you need point-in-time state for a single task. Do NOT use for streaming progress — use query_buffer instead. Do NOT use for DAG-level progress — use query_dag_status instead.").
		StringParam("task_id", "Task ID to query", true).
		Example(`{"task_id": "task_abc123"}`).
		BestPractice("Always check the found field before reading task data.").
		BestPractice("For bulk task state, prefer generate_summary over querying tasks individually.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			o.mu.RLock()
			defer o.mu.RUnlock()

			task, ok := o.state.Tasks[params.TaskID]
			if !ok {
				return map[string]any{"found": false, "task_id": params.TaskID}, nil
			}
			return map[string]any{"found": true, "task": task}, nil
		}).
		Build()
}

func queryWorkflowSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("query_workflow").
		Description("Query the status of a specific workflow by ID. Returns progress percentage, phase, and associated task IDs.").
		Domain("orchestration").
		Keywords("workflow", "status", "query", "progress").
		Priority(90).
		Usage("Use when you need overall workflow completion state or need to identify which tasks belong to a workflow. For cross-workflow overview, use generate_summary instead.").
		StringParam("workflow_id", "Workflow ID to query", true).
		Example(`{"workflow_id": "wf_plan_789"}`).
		BestPractice("Check the found field before reading workflow data.").
		BestPractice("Drill into individual tasks with query_task using the task IDs from the workflow response.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				WorkflowID string `json:"workflow_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			o.mu.RLock()
			defer o.mu.RUnlock()

			wf, ok := o.state.Workflows[params.WorkflowID]
			if !ok {
				return map[string]any{"found": false, "workflow_id": params.WorkflowID}, nil
			}
			return map[string]any{"found": true, "workflow": wf}, nil
		}).
		Build()
}

func pushStatusSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("push_status").
		Description("Push a status update for a task. Buffers the update and flushes to processing when the buffer threshold is reached.").
		Domain("orchestration").
		Keywords("status", "update", "push", "task").
		Priority(80).
		Usage("Use when a pipeline agent reports incremental progress or a status transition for a task. The status field must be a valid TaskStatus value. Do NOT use for terminal states — use report_failure for failures.").
		StringParam("task_id", "Task ID to update", true).
		StringParam("status", "New status: pending, queued, running, completed, failed, timed_out, cancelled", true).
		StringParam("message", "Optional status message describing what changed", false).
		Example(`{"task_id": "task_abc123", "status": "running", "message": "Compiling module 3/5"}`).
		BestPractice("Use report_failure instead of push_status with status=failed — report_failure also records health metrics and submits events.").
		BestPractice("Do not push status updates for tasks that do not exist in state — they will be silently ignored.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID   string  `json:"task_id"`
				Status   string  `json:"status"`
				Message  string  `json:"message"`
				Progress float64 `json:"progress"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			update := &StatusUpdate{
				TaskID:   params.TaskID,
				Status:   TaskStatus(params.Status),
				Message:  params.Message,
				Progress: params.Progress,
			}

			o.PushStatusUpdate(update)
			return map[string]any{"pushed": true, "task_id": params.TaskID}, nil
		}).
		Build()
}

func generateSummarySkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("generate_summary").
		Description("Generate a comprehensive summary of current orchestrator state including workflows, tasks, health, and statistics.").
		Domain("orchestration").
		Keywords("summary", "report", "overview", "state").
		Priority(70).
		Usage("Use when you need a holistic view of the system. Returns workflow counts, task counts by status, health summary, and event statistics. No parameters required. Do NOT call repeatedly in a tight loop — the summary is computed on each call.").
		Example(`{}`).
		BestPractice("Use this for periodic status broadcasts or when the architect requests an overall status update.").
		BestPractice("For single-task or single-workflow queries, use the targeted query skills instead.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return o.GetSummary(ctx)
		}).
		Build()
}

func reportFailureSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("report_failure").
		Description("Report a task failure with error details. Updates task state, increments failure counters, records health metrics, and submits the terminal event to Archivalist.").
		Domain("orchestration").
		Keywords("failure", "error", "report", "fail", "crash").
		Priority(85).
		Usage("Use when a pipeline agent or DAG node fails. This is the preferred way to record failures because it also updates health metrics for the assigned agent. Do NOT use push_status with status=failed — use this skill instead.").
		StringParam("task_id", "Task ID that failed", true).
		StringParam("error", "Error message describing the failure", true).
		StringParam("agent_id", "Agent that reported the failure (enables health tracking)", false).
		Example(`{"task_id": "task_abc123", "error": "compilation failed: syntax error at line 42", "agent_id": "engineer"}`).
		BestPractice("Always provide agent_id when available — without it, health metrics are not updated for the responsible agent.").
		BestPractice("After reporting a failure, consider escalating to the architect if the failure is part of a critical DAG path.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID  string `json:"task_id"`
				Error   string `json:"error"`
				AgentID string `json:"agent_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			o.mu.Lock()
			task, ok := o.state.Tasks[params.TaskID]
			if ok {
				task.Status = TaskStatusFailed
				task.Error = params.Error
				o.state.Stats.FailedTasks++
			}
			o.mu.Unlock()

			if params.AgentID != "" {
				o.healthMonitor.RecordTaskFailed(params.AgentID, params.TaskID, params.Error)
			}

			if ok {
				o.submitTaskEventAsync(task)
			}

			return map[string]any{"reported": true, "task_id": params.TaskID}, nil
		}).
		Build()
}

func submitTaskEventSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("submit_task_event").
		Description("Submit a terminal task event to Archivalist for historical storage. Only works for tasks in terminal states (completed, failed, timed_out, cancelled).").
		Domain("orchestration").
		Keywords("submit", "event", "archivalist", "terminal", "history").
		Priority(80).
		Usage("Use when a task reaches a terminal state and you need to ensure Archivalist has the event recorded. Note: report_failure already calls this automatically. Only invoke directly if a terminal task was not previously submitted.").
		StringParam("task_id", "Task ID to submit event for", true).
		Example(`{"task_id": "task_abc123"}`).
		BestPractice("Check the response — submitted will be false with a reason if the task is not found or not in a terminal state.").
		BestPractice("Do not call this for tasks already submitted — check EventSubmitted on the task record first.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			o.mu.RLock()
			task, ok := o.state.Tasks[params.TaskID]
			o.mu.RUnlock()

			if !ok {
				return map[string]any{"submitted": false, "reason": "task not found"}, nil
			}

			if !task.Status.IsTerminal() {
				return map[string]any{"submitted": false, "reason": "task not in terminal state"}, nil
			}

			o.submitTaskEventAsync(task)
			return map[string]any{"submitted": true, "task_id": params.TaskID}, nil
		}).
		Build()
}

func archivalistRequestSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("archivalist_request").
		Description("Query Archivalist for failure patterns, historical data, or trend analysis. Currently supports the 'failures' query type.").
		Domain("orchestration").
		Keywords("archivalist", "query", "failures", "patterns", "history").
		Priority(75).
		Usage("Use when investigating recurring failures or when the architect needs historical context for planning. The agent_id filter narrows results to a specific agent. Supported query_type values: failures.").
		StringParam("query_type", "Type of query: failures", true).
		StringParam("agent_id", "Filter results by agent ID", false).
		IntParam("limit", "Maximum results to return (default 10)", false).
		Example(`{"query_type": "failures", "agent_id": "engineer", "limit": 5}`).
		Example(`{"query_type": "failures"}`).
		BestPractice("Use the agent_id filter when investigating a specific agent's failure patterns — unfiltered queries may return noise from unrelated agents.").
		BestPractice("Start with a small limit and increase only if the initial results are insufficient.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				QueryType string `json:"query_type"`
				AgentID   string `json:"agent_id"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.Limit == 0 {
				params.Limit = 10
			}

			switch params.QueryType {
			case "failures":
				query := FailureQuery{Limit: params.Limit}
				if params.AgentID != "" {
					query.AgentIDs = []string{params.AgentID}
				}
				patterns, err := o.QueryArchivalistForFailures(ctx, query)
				if err != nil {
					return nil, err
				}
				return map[string]any{"patterns": patterns, "count": len(patterns)}, nil

			default:
				return map[string]any{"error": "unsupported query type", "supported": []string{"failures"}}, nil
			}
		}).
		Build()
}

func readPlanFileSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("read_plan_file").
		Description("Read an architect plan file for crash recovery or execution context. Restricted to .sylk/sessions/*/plans/*.md files only — path traversal is blocked.").
		Domain("orchestration").
		Keywords("plan", "read", "file", "crash", "recovery").
		Priority(70).
		Usage("Use during crash recovery to reload the architect's plan context, or when you need to verify the plan associated with a running DAG. The path must be under .sylk/sessions/<session_id>/plans/ and end with .md. Paths containing '..' are rejected.").
		StringParam("file_path", "Path to plan file (must be under .sylk/sessions/<session_id>/plans/ and end with .md)", true).
		Example(`{"file_path": ".sylk/sessions/default/plans/add_user_auth_abc123.md"}`).
		BestPractice("Never construct file_path from untrusted input — always use plan IDs from known DAG executions or workflow state.").
		BestPractice("If the file is not found, the plan may have been cleaned up — check the DAG execution store for the plan_json snapshot instead.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			cleaned := filepath.Clean(params.FilePath)
			if !isValidPlanPath(cleaned) {
				return nil, fmt.Errorf("access denied: path must be under .sylk/sessions/*/plans/ and end with .md")
			}

			data, err := os.ReadFile(cleaned)
			if err != nil {
				return nil, fmt.Errorf("read plan file: %w", err)
			}
			return map[string]any{"path": cleaned, "content": string(data)}, nil
		}).
		Build()
}

// isValidPlanPath validates that a path targets .sylk/sessions/*/plans/*.md with no traversal.
func isValidPlanPath(cleaned string) bool {
	if !strings.HasSuffix(cleaned, ".md") {
		return false
	}
	if strings.Contains(cleaned, "..") {
		return false
	}
	return strings.Contains(cleaned, ".sylk/sessions/") && strings.Contains(cleaned, "/plans/")
}

func escalateToArchitectSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("escalate_to_architect").
		Description("Send an escalation message to the architect agent via the event bus. Use for critical failures, persistent health degradation, or DAG execution problems that require architect intervention.").
		Domain("orchestration").
		Keywords("escalate", "architect", "alert", "critical", "warning").
		Priority(95).
		Usage("Use when a situation requires architect attention. Severity 'warning' is for degraded conditions that may self-resolve. Severity 'critical' is for failures requiring immediate intervention. Do NOT escalate for transient errors that resolve within one health check cycle.").
		StringParam("reason", "Clear description of what happened and why escalation is needed", true).
		StringParam("severity", "Escalation severity: warning or critical", true).
		StringParam("context", "Additional diagnostic context (error messages, agent IDs, DAG IDs)", false).
		Example(`{"reason": "Agent 'engineer' has failed 3 consecutive tasks", "severity": "warning", "context": "Error rate: 0.75, last error: compilation timeout"}`).
		Example(`{"reason": "DAG dag_abc123 failed on critical path node", "severity": "critical", "context": "Node: build_core, Error: out of memory"}`).
		BestPractice("Include actionable context — the architect needs enough information to decide whether to retry, modify the DAG, or abort.").
		BestPractice("Use warning for health degradation trends. Use critical only for failures that block execution or affect multiple tasks.").
		BestPractice("Do not escalate the same issue repeatedly — check if an escalation for this agent or DAG was already sent.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Reason   string `json:"reason"`
				Severity string `json:"severity"`
				Context  string `json:"context"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.Severity != "warning" && params.Severity != "critical" {
				return nil, fmt.Errorf("severity must be 'warning' or 'critical', got %q", params.Severity)
			}

			return o.doEscalateToArchitect(params.Reason, params.Severity, params.Context)
		}).
		Build()
}

func (o *Orchestrator) doEscalateToArchitect(reason, severity, context string) (any, error) {
	if o.bus == nil || !o.running {
		return nil, fmt.Errorf("orchestrator not running")
	}

	escalationInput := fmt.Sprintf("[ORCHESTRATOR ESCALATION - %s] %s", strings.ToUpper(severity), reason)
	if context != "" {
		escalationInput += "\n\nContext: " + context
	}

	req := &guide.RouteRequest{
		Input:           escalationInput,
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		TargetAgentID:   "architect",
		FireAndForget:   true,
		SessionID:       o.config.SessionID,
		Timestamp:       time.Now(),
	}

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = map[string]any{
		"escalation_severity": severity,
		"escalation_reason":   reason,
	}

	if err := o.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("publish escalation: %w", err)
	}
	return map[string]any{"escalated": true, "severity": severity, "target": "architect"}, nil
}

func broadcastStatusSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("broadcast_status").
		Description("Publish a status broadcast visible to the UI and other agents on the orchestrator.status topic.").
		Domain("orchestration").
		Keywords("broadcast", "status", "notify", "announce", "ui").
		Priority(75).
		Usage("Use for user-visible status updates: DAG started/completed, health warnings, or milestone notifications. Level 'info' for normal progress, 'warning' for degraded conditions, 'error' for failures. Do NOT use for internal orchestrator state changes that users do not need to see.").
		StringParam("message", "Human-readable status message to broadcast", true).
		StringParam("level", "Broadcast level: info, warning, or error", true).
		Example(`{"message": "DAG dag_abc123 completed: 12/12 nodes succeeded", "level": "info"}`).
		Example(`{"message": "Agent 'engineer' health degraded: error rate 60%", "level": "warning"}`).
		BestPractice("Keep messages concise and actionable — they appear in the UI status panel.").
		BestPractice("Use info for normal progress milestones, warning for conditions that may need attention, error for failures requiring user awareness.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Message string `json:"message"`
				Level   string `json:"level"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			switch params.Level {
			case "info", "warning", "error":
			default:
				return nil, fmt.Errorf("level must be 'info', 'warning', or 'error', got %q", params.Level)
			}

			if o.bus == nil || !o.running {
				return nil, fmt.Errorf("orchestrator not running")
			}

			msg := &guide.Message{
				ID:            generateMessageID(),
				Type:          guide.MessageTypeResponse,
				SourceAgentID: o.config.AgentID,
				Payload:       params.Message,
				Metadata: map[string]any{
					"level":  params.Level,
					"source": "orchestrator",
				},
				Timestamp: time.Now(),
			}

			if err := o.bus.Publish("orchestrator.status", msg); err != nil {
				return nil, fmt.Errorf("publish broadcast: %w", err)
			}
			return map[string]any{"broadcast": true, "level": params.Level}, nil
		}).
		Build()
}

func queryAgentHealthSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("query_agent_health").
		Description("Get health metrics for a specific agent or all agents. Reads from Ristretto hot cache first, falls back to live HealthMonitor. Response includes source field for observability.").
		Domain("monitoring").
		Keywords("health", "agent", "metrics", "status", "check").
		Priority(85).
		Usage("Use when investigating agent reliability or before routing work to an agent. Omit agent_id to get the latest health check result for all agents. The source field indicates whether data came from cache (fast) or monitor (live).").
		StringParam("agent_id", "Agent ID to query (omit for all agents)", false).
		Example(`{"agent_id": "engineer"}`).
		Example(`{}`).
		BestPractice("Check the source field — 'cache' results are from the last health check cycle (up to 10s old), 'monitor' results are computed live.").
		BestPractice("For historical trends, use query_health_history instead of repeated calls to this skill.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				AgentID string `json:"agent_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.AgentID != "" {
				if cached, ok := o.healthCache.GetAgent(params.AgentID); ok {
					return map[string]any{"found": true, "agent": cached, "source": "cache"}, nil
				}
				metrics := o.healthMonitor.GetAgentHealth(params.AgentID)
				if metrics == nil {
					return map[string]any{"found": false, "agent_id": params.AgentID}, nil
				}
				return map[string]any{"found": true, "agent": metrics, "source": "monitor"}, nil
			}

			if cached, ok := o.healthCache.GetLatest(); ok {
				return map[string]any{"result": cached, "source": "cache"}, nil
			}
			summary := o.healthMonitor.GetSummary()
			return map[string]any{"summary": summary, "source": "monitor"}, nil
		}).
		Build()
}

func queryHealthHistorySkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("query_health_history").
		Description("Query Archivalist for historical health check results. Returns asynchronously via Archivalist. Use for trend analysis and identifying degradation patterns.").
		Domain("monitoring").
		Keywords("health", "history", "archivalist", "trend", "pattern").
		Priority(75).
		Usage("Use when you need health trends over time — not for current state (use query_agent_health instead). The query is submitted asynchronously to Archivalist; results arrive via bus. Provide since as ISO 8601 to narrow the time range.").
		StringParam("agent_id", "Filter by agent ID", false).
		StringParam("since", "ISO 8601 start time (e.g., 2025-01-15T10:00:00Z)", false).
		IntParam("limit", "Maximum results (default 20)", false).
		Example(`{"agent_id": "engineer", "since": "2025-01-15T10:00:00Z", "limit": 10}`).
		Example(`{"limit": 50}`).
		BestPractice("Narrow the time range with since to avoid retrieving the entire health history.").
		BestPractice("This is fire-and-forget — the response arrives asynchronously. Do not block waiting for results.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				AgentID string `json:"agent_id"`
				Since   string `json:"since"`
				Limit   int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.Limit == 0 {
				params.Limit = 20
			}

			if o.bus == nil {
				return nil, fmt.Errorf("orchestrator not running")
			}

			metadata := map[string]any{
				"query_type": "health_history",
				"limit":      params.Limit,
			}
			if params.AgentID != "" {
				metadata["agent_id"] = params.AgentID
			}
			if params.Since != "" {
				metadata["since"] = params.Since
			}

			req := &guide.RouteRequest{
				Input:           "query health check history",
				SourceAgentID:   o.config.AgentID,
				SourceAgentName: "orchestrator",
				TargetAgentID:   "archivalist",
				FireAndForget:   true,
				SessionID:       o.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(generateMessageID(), req)
			msg.Metadata = metadata

			if err := o.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish health history query: %w", err)
			}

			return map[string]any{
				"submitted": true,
				"query":     metadata,
			}, nil
		}).
		Build()
}
