package orchestrator

import (
	"fmt"
	"sort"
	"strings"
)

// formatTasksAsText formats all tasks into a human-readable summary.
func formatTasksAsText(tasks map[string]*TaskRecord) string {
	if len(tasks) == 0 {
		return "No tasks currently registered."
	}

	byStatus := groupTasksByStatus(tasks)
	statusOrder := []TaskStatus{
		TaskStatusRunning, TaskStatusQueued, TaskStatusPending,
		TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled,
		TaskStatusTimedOut, TaskStatusRetrying,
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Tasks** (%d total)\n", len(tasks)))

	for _, status := range statusOrder {
		group := byStatus[status]
		if len(group) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("\n_%s_ (%d):\n", status, len(group)))
		for _, t := range group {
			b.WriteString(fmt.Sprintf("- **%s** — %s", t.ID, t.Name))
			if t.Description != "" {
				b.WriteString(": ")
				b.WriteString(truncateForDisplay(t.Description, 80))
			}
			b.WriteByte('\n')
		}
	}

	return strings.TrimSpace(b.String())
}

// groupTasksByStatus groups tasks by their status, sorted by priority descending.
func groupTasksByStatus(tasks map[string]*TaskRecord) map[TaskStatus][]*TaskRecord {
	grouped := make(map[TaskStatus][]*TaskRecord, 8)
	for _, t := range tasks {
		grouped[t.Status] = append(grouped[t.Status], t)
	}
	for status := range grouped {
		sort.Slice(grouped[status], func(i, j int) bool {
			a, b := grouped[status][i], grouped[status][j]
			if a.Priority != b.Priority {
				return a.Priority > b.Priority
			}
			return a.Name < b.Name
		})
	}
	return grouped
}

// formatSingleTaskAsText formats a single task into readable text.
func formatSingleTaskAsText(task *TaskRecord) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**%s** — %s\n", task.ID, task.Name))
	b.WriteString(fmt.Sprintf("Status: %s\n", task.Status))
	if task.Description != "" {
		b.WriteString(fmt.Sprintf("Description: %s\n", task.Description))
	}
	if task.WorkflowID != "" {
		b.WriteString(fmt.Sprintf("Workflow: %s\n", task.WorkflowID))
	}
	if task.AssignedAgentID != "" {
		b.WriteString(fmt.Sprintf("Assigned to: %s\n", task.AssignedAgentID))
	}
	if task.Error != "" {
		b.WriteString(fmt.Sprintf("Error: %s\n", task.Error))
	}
	b.WriteString(fmt.Sprintf("Attempts: %d/%d", task.Attempts, task.MaxAttempts))
	return strings.TrimSpace(b.String())
}

// formatWorkflowsAsText formats all workflows into readable text.
func formatWorkflowsAsText(workflows map[string]*WorkflowState) string {
	if len(workflows) == 0 {
		return "No workflows currently registered."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Workflows** (%d total)\n", len(workflows)))

	for _, wf := range workflows {
		b.WriteByte('\n')
		b.WriteString(formatSingleWorkflowAsText(wf))
		b.WriteByte('\n')
	}

	return strings.TrimSpace(b.String())
}

// formatSingleWorkflowAsText formats a single workflow into readable text.
func formatSingleWorkflowAsText(wf *WorkflowState) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**%s** — %s\n", wf.ID, wf.Name))
	b.WriteString(fmt.Sprintf("Status: %s", wf.Status))
	if wf.Progress > 0 {
		b.WriteString(fmt.Sprintf(" (%.0f%%)", wf.Progress*100))
	}
	b.WriteByte('\n')
	if wf.Description != "" {
		b.WriteString(fmt.Sprintf("Description: %s\n", truncateForDisplay(wf.Description, 120)))
	}
	total := len(wf.TaskIDs)
	completed := len(wf.CompletedIDs)
	failed := len(wf.FailedIDs)
	pending := len(wf.PendingIDs)
	b.WriteString(fmt.Sprintf("Tasks: %d total, %d completed, %d failed, %d pending",
		total, completed, failed, pending))
	return strings.TrimSpace(b.String())
}

// formatSummaryAsText formats an OrchestratorSummary into readable text.
func formatSummaryAsText(summary *OrchestratorSummary) string {
	var b strings.Builder
	b.WriteString(summary.Overview)
	b.WriteByte('\n')

	wf := summary.Workflows
	if wf.Total > 0 {
		b.WriteString(fmt.Sprintf("\nWorkflows: %d total (%d running, %d completed, %d failed)",
			wf.Total, wf.Running, wf.Completed, wf.Failed))
		for _, aw := range wf.ActiveWorkflows {
			b.WriteString(fmt.Sprintf("\n- %s: %s", aw.Name, aw.Status))
			if aw.Progress > 0 {
				b.WriteString(fmt.Sprintf(" (%.0f%%)", aw.Progress*100))
			}
		}
	}

	ts := summary.Tasks
	if ts.Total > 0 {
		b.WriteString(fmt.Sprintf("\nTasks: %d total (%d running, %d pending, %d completed, %d failed)",
			ts.Total, ts.Running, ts.Pending, ts.Completed, ts.Failed))
	}

	for _, rec := range summary.Recommendations {
		b.WriteString(fmt.Sprintf("\n- %s", rec))
	}

	return strings.TrimSpace(b.String())
}

// formatFailurePatternsAsText formats failure patterns into readable text.
func formatFailurePatternsAsText(patterns []FailurePattern) string {
	if len(patterns) == 0 {
		return "No failure patterns detected."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Failure Patterns** (%d)\n", len(patterns)))

	for _, p := range patterns {
		b.WriteString(fmt.Sprintf("\n- **%s** [%s]: %s", p.Type, p.Severity, p.Description))
		if p.Occurrences > 1 {
			b.WriteString(fmt.Sprintf(" (%d occurrences)", p.Occurrences))
		}
		if p.Recommendation != "" {
			b.WriteString(fmt.Sprintf("\n  Recommendation: %s", p.Recommendation))
		}
	}

	return strings.TrimSpace(b.String())
}

// formatStateOverviewAsText formats the orchestrator state for recall queries.
func formatStateOverviewAsText(state *State) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Orchestrator State**\n\nWorkflows: %d, Tasks: %d",
		len(state.Workflows), len(state.Tasks)))

	if state.Stats.CompletedTasks > 0 || state.Stats.FailedTasks > 0 {
		b.WriteString(fmt.Sprintf("\nCompleted: %d, Failed: %d",
			state.Stats.CompletedTasks, state.Stats.FailedTasks))
	}

	for _, wf := range state.Workflows {
		b.WriteString(fmt.Sprintf("\n\n%s", formatSingleWorkflowAsText(wf)))
	}

	return strings.TrimSpace(b.String())
}

// truncateForDisplay truncates a string for display purposes.
func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
