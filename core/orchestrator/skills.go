package orchestrator

import (
	"context"
	"encoding/json"

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

	o.skills.Load("query_task")
	o.skills.Load("query_workflow")
	o.skills.Load("push_status")
	o.skills.Load("generate_summary")
	o.skills.Load("report_failure")
	o.skills.Load("submit_task_event")
	o.skills.Load("archivalist_request")
}

func queryTaskSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("query_task").
		Description("Query the status of a specific task by ID.").
		Domain("orchestration").
		Keywords("task", "status", "query").
		Priority(90).
		StringParam("task_id", "Task ID to query", true).
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
		Description("Query the status of a specific workflow by ID.").
		Domain("orchestration").
		Keywords("workflow", "status", "query").
		Priority(90).
		StringParam("workflow_id", "Workflow ID to query", true).
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
		Description("Push a status update for a task.").
		Domain("orchestration").
		Keywords("status", "update", "push").
		Priority(80).
		StringParam("task_id", "Task ID to update", true).
		StringParam("status", "New status (pending, running, completed, failed)", true).
		StringParam("message", "Optional status message", false).
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
		Description("Generate a summary of current orchestrator state.").
		Domain("orchestration").
		Keywords("summary", "report", "overview").
		Priority(70).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return o.GetSummary(ctx)
		}).
		Build()
}

func reportFailureSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("report_failure").
		Description("Report a task failure with details.").
		Domain("orchestration").
		Keywords("failure", "error", "report").
		Priority(85).
		StringParam("task_id", "Task ID that failed", true).
		StringParam("error", "Error message", true).
		StringParam("agent_id", "Agent that reported the failure", false).
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
				go o.submitTaskEvent(task)
			}

			return map[string]any{"reported": true, "task_id": params.TaskID}, nil
		}).
		Build()
}

func submitTaskEventSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("submit_task_event").
		Description("Submit a task event to Archivalist for a terminal task state.").
		Domain("orchestration").
		Keywords("submit", "event", "archivalist").
		Priority(80).
		StringParam("task_id", "Task ID to submit event for", true).
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

			go o.submitTaskEvent(task)
			return map[string]any{"submitted": true, "task_id": params.TaskID}, nil
		}).
		Build()
}

func archivalistRequestSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("archivalist_request").
		Description("Request data from Archivalist, such as failure patterns.").
		Domain("orchestration").
		Keywords("archivalist", "query", "failures", "patterns").
		Priority(75).
		StringParam("query_type", "Type of query: failures, patterns, history", true).
		StringParam("agent_id", "Filter by agent ID", false).
		IntParam("limit", "Maximum results to return", false).
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
