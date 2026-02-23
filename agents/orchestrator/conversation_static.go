package orchestrator

import (
	"fmt"
	"strings"
)

// tryStaticOrchestratorReply returns a fast, deterministic response for trivial
// queries that don't need an LLM call. Returns ("", false) for queries that
// require LLM processing.
func tryStaticOrchestratorReply(cr orchestratorConversationRequest) (string, bool) {
	query := normalizeOrchestratorQuery(cr.UserQuery)
	if query == "" {
		return staticOrchestratorOnlineReply(cr), true
	}
	if isOrchestratorGreetingQuery(query) {
		return staticOrchestratorGreetingReply(cr), true
	}
	if isOrchestratorStatusQuery(query) {
		return staticOrchestratorStatusReply(cr), true
	}
	if isOrchestratorWorkflowQuery(query) {
		return staticOrchestratorWorkflowReply(cr), true
	}
	if isOrchestratorHealthQuery(query) {
		return staticOrchestratorHealthReply(cr), true
	}
	if isOrchestratorDAGQuery(query) {
		return staticOrchestratorDAGReply(cr), true
	}
	return "", false
}

func normalizeOrchestratorQuery(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

// --- Query classifiers ---

func isOrchestratorGreetingQuery(query string) bool {
	// Short greeting tokens only — don't match "hello, what's the status?"
	tokens := strings.Fields(query)
	if len(tokens) == 0 || len(tokens) > 3 {
		return false
	}
	first := tokens[0]
	return first == "hello" || first == "hi" || first == "hey" || first == "howdy" || first == "yo"
}

func isOrchestratorStatusQuery(query string) bool {
	return orchestratorContainsAny(query, "status", "overview", "what's going on", "what is going on", "sitrep")
}

func isOrchestratorWorkflowQuery(query string) bool {
	return orchestratorContainsAny(query, "workflow", "pipeline")
}

func isOrchestratorHealthQuery(query string) bool {
	return orchestratorContainsAny(query, "health", "alert", "degraded", "unhealthy", "critical")
}

func isOrchestratorDAGQuery(query string) bool {
	return orchestratorContainsAny(query, "dag", "execution")
}

// --- Static reply builders ---

func staticOrchestratorOnlineReply(cr orchestratorConversationRequest) string {
	return fmt.Sprintf("Orchestrator online. %d agents registered, %d workflows tracked, %d tasks in state.",
		len(cr.AgentIDs), len(cr.Workflows), len(cr.Tasks))
}

func staticOrchestratorGreetingReply(cr orchestratorConversationRequest) string {
	running, _, failed := countWorkflowsByStatus(cr.Workflows)
	base := fmt.Sprintf("Hello. Orchestrator is operational with %d agents registered.", len(cr.AgentIDs))
	if running > 0 || failed > 0 {
		base += fmt.Sprintf(" %d workflows running, %d failed.", running, failed)
	}
	if cr.Health.Critical > 0 {
		base += fmt.Sprintf(" Warning: %d agents in critical state.", cr.Health.Critical)
	}
	return base
}

func staticOrchestratorStatusReply(cr orchestratorConversationRequest) string {
	running, completed, failed := countWorkflowsByStatus(cr.Workflows)
	tRunning, tCompleted, tFailed, tPending := countTasksByStatus(cr.Tasks)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("System status: %d agents registered.\n", len(cr.AgentIDs)))
	b.WriteString(fmt.Sprintf("Workflows: %d total (%d running, %d completed, %d failed).\n",
		len(cr.Workflows), running, completed, failed))
	b.WriteString(fmt.Sprintf("Tasks: %d tracked (%d running, %d completed, %d failed, %d pending).\n",
		len(cr.Tasks), tRunning, tCompleted, tFailed, tPending))
	b.WriteString(fmt.Sprintf("Health: %d healthy, %d degraded, %d unhealthy, %d critical.",
		cr.Health.Healthy, cr.Health.Degraded, cr.Health.Unhealthy, cr.Health.Critical))

	if len(cr.Alerts) > 0 {
		b.WriteString(fmt.Sprintf("\nActive alerts: %d.", len(cr.Alerts)))
	}
	if len(cr.DAGs) > 0 {
		b.WriteString(fmt.Sprintf("\nActive DAGs: %d.", len(cr.DAGs)))
	}
	return b.String()
}

func staticOrchestratorWorkflowReply(cr orchestratorConversationRequest) string {
	if len(cr.Workflows) == 0 {
		return "No workflows currently tracked."
	}
	running, completed, failed := countWorkflowsByStatus(cr.Workflows)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Workflows: %d total (%d running, %d completed, %d failed).\n",
		len(cr.Workflows), running, completed, failed))
	for _, wf := range cr.Workflows {
		b.WriteString(fmt.Sprintf("- %s: %s (%.0f%% complete", wf.ID, wf.Status, wf.Progress*100))
		if wf.Phase != "" {
			b.WriteString(fmt.Sprintf(", phase: %s", wf.Phase))
		}
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func staticOrchestratorHealthReply(cr orchestratorConversationRequest) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Agent health: %d healthy, %d degraded, %d unhealthy, %d critical.",
		cr.Health.Healthy, cr.Health.Degraded, cr.Health.Unhealthy, cr.Health.Critical))
	if len(cr.Alerts) > 0 {
		b.WriteString(fmt.Sprintf("\nActive alerts (%d):", len(cr.Alerts)))
		for _, alert := range cr.Alerts {
			b.WriteString(fmt.Sprintf("\n- [%s] %s: %s", alert.Type, alert.AgentID, alert.Message))
		}
	}
	return b.String()
}

func staticOrchestratorDAGReply(cr orchestratorConversationRequest) string {
	if len(cr.DAGs) == 0 {
		return "No DAG executions currently active."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Active DAGs: %d\n", len(cr.DAGs)))
	for _, d := range cr.DAGs {
		b.WriteString(fmt.Sprintf("- %s: state=%s, plan=%s, layer %d/%d (%.0f%% complete)",
			d.ID, d.State, d.PlanID, d.CurrentLayer, d.TotalLayers, d.Progress*100))
		if d.NodesFailed > 0 {
			b.WriteString(fmt.Sprintf(", %d nodes failed", d.NodesFailed))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// orchestratorContainsAny returns true if query contains any of the fragments.
func orchestratorContainsAny(query string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(query, fragment) {
			return true
		}
	}
	return false
}
