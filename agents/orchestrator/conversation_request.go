package orchestrator

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
)

// orchestratorConversationRequest holds runtime fields for a conversation turn.
type orchestratorConversationRequest struct {
	SessionID     string
	UserQuery     string
	AgentIDs      []string
	Workflows     []workflowSnap
	Tasks         []taskSnap
	Health        healthSnap
	Alerts        []alertSnap
	DAGs          []DAGSnap
	Stats         snapshotStats
	History       []guide.ConversationTurn
	OnChunk       func(string)
}

// buildConversationRequest extracts runtime context from the orchestrator state
// and the forwarded request. Caller must NOT hold o.mu.
func (o *Orchestrator) buildConversationRequest(req *guide.ForwardedRequest) orchestratorConversationRequest {
	o.mu.RLock()
	agentIDs := make([]string, 0, len(o.knownAgents))
	for id := range o.knownAgents {
		agentIDs = append(agentIDs, id)
	}
	workflows := collectWorkflowSnaps(o.state.Workflows)
	tasks := collectTaskSnaps(o.state.Tasks)
	health := collectHealthSnap(o.healthMonitor)
	alerts := collectAlertSnaps(o.healthMonitor)
	stats := snapshotStats{
		Completed: o.state.Stats.CompletedTasks,
		Failed:    o.state.Stats.FailedTasks,
		Submitted: o.state.Stats.EventsSubmitted,
	}
	var dags []DAGSnap
	if o.dagBridge != nil {
		dags = o.dagBridge.DAGSnapshots(snapshotMaxDAGs)
	}
	o.mu.RUnlock()

	cr := orchestratorConversationRequest{
		SessionID: o.config.SessionID,
		UserQuery: strings.TrimSpace(req.Input),
		AgentIDs:  agentIDs,
		Workflows: workflows,
		Tasks:     tasks,
		Health:    health,
		Alerts:    alerts,
		DAGs:      dags,
		Stats:     stats,
		History:   req.ConversationHistory,
	}

	return normalizeConversationRequest(cr)
}

// normalizeConversationRequest trims, clamps, and deduplicates the request fields.
func normalizeConversationRequest(cr orchestratorConversationRequest) orchestratorConversationRequest {
	cr.UserQuery = strings.TrimSpace(cr.UserQuery)
	cr.SessionID = strings.TrimSpace(cr.SessionID)

	// Deduplicate agent IDs.
	seen := make(map[string]struct{}, len(cr.AgentIDs))
	deduped := make([]string, 0, len(cr.AgentIDs))
	for _, id := range cr.AgentIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		deduped = append(deduped, trimmed)
	}
	cr.AgentIDs = deduped

	// Clamp snapshot lists.
	if len(cr.Workflows) > snapshotMaxWorkflows {
		cr.Workflows = cr.Workflows[:snapshotMaxWorkflows]
	}
	if len(cr.Tasks) > snapshotMaxTasks {
		cr.Tasks = cr.Tasks[:snapshotMaxTasks]
	}
	if len(cr.Alerts) > snapshotMaxAlerts {
		cr.Alerts = cr.Alerts[:snapshotMaxAlerts]
	}
	if len(cr.DAGs) > snapshotMaxDAGs {
		cr.DAGs = cr.DAGs[:snapshotMaxDAGs]
	}

	return cr
}

// buildConversationUserPrompt formats the runtime context and user query
// into the user message for the LLM conversation call.
func buildConversationUserPrompt(cr orchestratorConversationRequest) string {
	var b strings.Builder

	// Runtime context block.
	b.WriteString("## Runtime Context\n\n")

	// Agent awareness.
	b.WriteString(fmt.Sprintf("Registered agents: %d", len(cr.AgentIDs)))
	if len(cr.AgentIDs) > 0 {
		b.WriteString(fmt.Sprintf(" (%s)", strings.Join(cr.AgentIDs, ", ")))
	}
	b.WriteByte('\n')

	// Workflow summary.
	running, completed, failed := countWorkflowsByStatus(cr.Workflows)
	b.WriteString(fmt.Sprintf("Workflows: %d total (%d running, %d completed, %d failed)\n",
		len(cr.Workflows), running, completed, failed))

	// Task summary.
	tRunning, tCompleted, tFailed, tPending := countTasksByStatus(cr.Tasks)
	b.WriteString(fmt.Sprintf("Tasks: %d tracked (%d running, %d completed, %d failed, %d pending)\n",
		len(cr.Tasks), tRunning, tCompleted, tFailed, tPending))

	// Health summary.
	b.WriteString(fmt.Sprintf("Health: %d healthy, %d degraded, %d unhealthy, %d critical\n",
		cr.Health.Healthy, cr.Health.Degraded, cr.Health.Unhealthy, cr.Health.Critical))

	// Active alerts.
	if len(cr.Alerts) > 0 {
		b.WriteString(fmt.Sprintf("Active alerts: %d\n", len(cr.Alerts)))
		for _, alert := range cr.Alerts {
			b.WriteString(fmt.Sprintf("  - [%s] %s: %s\n", alert.Type, alert.AgentID, alert.Message))
		}
	}

	// DAG summary.
	if len(cr.DAGs) > 0 {
		b.WriteString(fmt.Sprintf("Active DAGs: %d\n", len(cr.DAGs)))
		for _, d := range cr.DAGs {
			b.WriteString(fmt.Sprintf("  - %s (plan=%s, state=%s, progress=%.0f%%)\n",
				d.ID, d.PlanID, d.State, d.Progress*100))
		}
	}

	// Stats.
	b.WriteString(fmt.Sprintf("Lifetime: %d completed, %d failed, %d events submitted\n",
		cr.Stats.Completed, cr.Stats.Failed, cr.Stats.Submitted))

	b.WriteString("\n---\n\n")

	// User query.
	b.WriteString("## User Query\n\n")
	b.WriteString(cr.UserQuery)

	return b.String()
}

func countWorkflowsByStatus(workflows []workflowSnap) (running, completed, failed int) {
	for _, wf := range workflows {
		switch wf.Status {
		case WorkflowStatusRunning:
			running++
		case WorkflowStatusCompleted:
			completed++
		case WorkflowStatusFailed:
			failed++
		}
	}
	return
}

func countTasksByStatus(tasks []taskSnap) (running, completed, failed, pending int) {
	for _, t := range tasks {
		switch t.Status {
		case TaskStatusRunning:
			running++
		case TaskStatusCompleted:
			completed++
		case TaskStatusFailed:
			failed++
		case TaskStatusPending, TaskStatusQueued:
			pending++
		}
	}
	return
}
