package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/architect"
)

// buildIngestionSummary transforms a plan handoff input and its ingestion
// result into a natural language prompt for the orchestrator's LLM. The
// summary describes the plan structure so the LLM can acknowledge receipt
// and summarize the execution strategy.
//
// On success, the summary includes plan ID, DAG ID, workflow ID, task
// breakdown with dependencies, and the critical path.
// On error, the summary includes the failure reason so the LLM can explain
// what went wrong.
func buildIngestionSummary(input string, result any) string {
	resultMap, ok := result.(map[string]any)
	if !ok {
		return buildGenericIngestionSummary(result)
	}

	if ingested, _ := resultMap["ingested"].(bool); ingested {
		return buildSuccessSummary(input, resultMap)
	}
	return buildErrorSummary(input, resultMap)
}

// buildSuccessSummary formats a successful plan ingestion for the LLM.
// Re-unmarshals the handoff to extract task details (cheap — already
// validated by ingestPlan, ~microseconds vs the LLM call that follows).
func buildSuccessSummary(input string, result map[string]any) string {
	var b strings.Builder

	planID, _ := result["plan_id"].(string)
	dagID, _ := result["dag_id"].(string)
	wfID, _ := result["workflow_id"].(string)
	taskCount, _ := result["task_count"].(int)
	layerCount, _ := result["layer_count"].(int)

	b.WriteString("A plan has been received from the architect and ingested for execution.\n\n")
	fmt.Fprintf(&b, "Plan: %s (DAG %s, Workflow %s)\n", planID, dagID, wfID)
	fmt.Fprintf(&b, "Tasks: %d tasks across %d layers\n", taskCount, layerCount)

	// Parse handoff for task-level detail.
	var handoff architect.PlanHandoff
	if err := json.Unmarshal([]byte(input), &handoff); err == nil {
		if handoff.Query != "" {
			fmt.Fprintf(&b, "Query: %q\n", handoff.Query)
		}
		if handoff.Trigger != "" {
			fmt.Fprintf(&b, "Trigger: %s\n", handoff.Trigger)
		}

		b.WriteString("\nTask breakdown:\n")
		for i, t := range handoff.Tasks {
			fmt.Fprintf(&b, "%d. [%s] %s (complexity: %s, agent: %s)\n",
				i+1, t.ID, t.Name, t.Complexity, t.AgentType)
			if len(t.Dependencies) == 0 {
				b.WriteString("   Dependencies: none\n")
			} else {
				fmt.Fprintf(&b, "   Dependencies: %s\n", strings.Join(t.Dependencies, ", "))
			}
		}

		if len(handoff.CriticalPath) > 0 {
			fmt.Fprintf(&b, "\nCritical path: %s\n", strings.Join(handoff.CriticalPath, " -> "))
		}
	}

	b.WriteString("\nAcknowledge receipt of this plan and summarize your understanding of the tasks and their relationships.")

	return b.String()
}

// buildErrorSummary formats a failed plan ingestion for the LLM so it
// can explain the failure conversationally.
func buildErrorSummary(input string, result map[string]any) string {
	var b strings.Builder

	b.WriteString("A plan handoff was received from the architect but ingestion failed.\n")

	if errMsg, _ := result["error"].(string); errMsg != "" {
		fmt.Fprintf(&b, "Error: %s\n", errMsg)
	}

	// Try to extract the plan ID for context even though ingestion failed.
	var handoff architect.PlanHandoff
	if err := json.Unmarshal([]byte(input), &handoff); err == nil && handoff.PlanID != "" {
		fmt.Fprintf(&b, "Plan ID: %s\n", handoff.PlanID)
	}

	b.WriteString("\nExplain this failure and suggest what the architect should fix.")

	return b.String()
}

// buildGenericIngestionSummary handles the edge case where the ingestion
// result is not a map (e.g. a typed error). Falls back to a simple prompt.
func buildGenericIngestionSummary(result any) string {
	return fmt.Sprintf(
		"A plan handoff was received from the architect. Ingestion result: %v\n\n"+
			"Summarize the current state of plan processing.",
		result,
	)
}
