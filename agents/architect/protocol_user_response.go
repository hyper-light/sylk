package architect

import (
	"fmt"
	"strings"
)

// fallbackReadyUserResponse generates a deterministic user-facing response
// when a plan is ready but the LLM compose step failed (e.g. context cancelled).
func fallbackReadyUserResponse(_ *Requirements, plan *DesignPlan) string {
	if plan == nil || len(plan.Tasks) == 0 {
		return "I've prepared a plan, but couldn't generate a summary. Please review the plan details above."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("I've prepared a plan with %d tasks", len(plan.Tasks)))
	layers := planLayerCount(plan)
	if layers > 1 {
		b.WriteString(fmt.Sprintf(" across %d execution layers", layers))
	}
	b.WriteString(".\n\n")
	b.WriteString(formatPlanForChat(plan))
	return b.String()
}

// planLayerCount returns the number of execution layers in the plan's workflow,
// or 0 if no workflow is set.
func planLayerCount(plan *DesignPlan) int {
	if plan == nil || plan.Workflow == nil {
		return 0
	}
	return len(plan.Workflow.ExecutionLayers)
}

// firstTaskName returns the name of the first task in the plan, or an empty
// string if the plan has no tasks.
func firstTaskName(plan *DesignPlan) string {
	if plan == nil || len(plan.Tasks) == 0 {
		return ""
	}
	return plan.Tasks[0].Name
}
