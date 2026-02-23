package chat

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/msg"
)

// planStatusIcon maps a task status string to a Unicode icon.
func planStatusIcon(status string) string {
	switch status {
	case "pending":
		return "○"
	case "queued", "running":
		return "◉"
	case "completed":
		return "✓"
	case "failed":
		return "✕"
	case "blocked":
		return "◌"
	case "skipped":
		return "─"
	default:
		return "○"
	}
}

// formatPlanMarkdown renders a PlanUpdateMsg as a markdown string suitable
// for the chat panel's existing CommonMark renderer.
func formatPlanMarkdown(update msg.PlanUpdateMsg) string {
	var b strings.Builder

	b.WriteString("## Plan\n\n")
	b.WriteString(fmt.Sprintf("**Status:** %s\n\n", update.Status))

	if len(update.Tasks) == 0 {
		return b.String()
	}

	// Build task lookup for layer-based ordering.
	taskByID := make(map[string]msg.PlanTaskSnapshot, len(update.Tasks))
	for _, t := range update.Tasks {
		taskByID[t.ID] = t
	}

	// Determine whether to render by execution layers or flat list.
	if len(update.ExecutionLayers) > 1 {
		formatPlanLayered(&b, update.ExecutionLayers, taskByID, update.Tasks)
	} else {
		formatPlanFlat(&b, update.Tasks)
	}

	// Execution summary.
	layers := len(update.ExecutionLayers)
	tasks := len(update.Tasks)
	b.WriteString("\n### Execution\n\n")
	if layers > 1 {
		b.WriteString(fmt.Sprintf("%d layers, %d tasks\n", layers, tasks))
	} else {
		b.WriteString(fmt.Sprintf("%d tasks\n", tasks))
	}

	return b.String()
}

// formatPlanLayered renders tasks grouped by execution layer.
func formatPlanLayered(b *strings.Builder, layers [][]string, taskByID map[string]msg.PlanTaskSnapshot, allTasks []msg.PlanTaskSnapshot) {
	b.WriteString("### Tasks\n\n")
	taskNum := 1
	for layerIdx, layer := range layers {
		if layerIdx > 0 {
			b.WriteString("\n---\n\n")
		}
		if len(layers) > 1 {
			b.WriteString(fmt.Sprintf("**Layer %d**\n\n", layerIdx+1))
		}
		for _, id := range layer {
			task, ok := taskByID[id]
			if !ok {
				continue
			}
			formatPlanTask(b, task, taskNum)
			taskNum++
		}
	}

	// Append any tasks not in any layer (shouldn't happen, but defensive).
	layerSet := make(map[string]struct{}, len(allTasks))
	for _, layer := range layers {
		for _, id := range layer {
			layerSet[id] = struct{}{}
		}
	}
	for _, task := range allTasks {
		if _, inLayer := layerSet[task.ID]; inLayer {
			continue
		}
		formatPlanTask(b, task, taskNum)
		taskNum++
	}
}

// formatPlanFlat renders tasks as a simple numbered list.
func formatPlanFlat(b *strings.Builder, tasks []msg.PlanTaskSnapshot) {
	b.WriteString("### Tasks\n\n")
	for i, task := range tasks {
		formatPlanTask(b, task, i+1)
	}
}

// formatPlanTask renders a single task entry.
func formatPlanTask(b *strings.Builder, task msg.PlanTaskSnapshot, num int) {
	icon := planStatusIcon(task.Status)

	// Header line: **1. Task Name** `agent-type` ○
	b.WriteString(fmt.Sprintf("**%d. %s** `%s` %s", num, task.Name, task.AgentType, icon))

	// Dependencies suffix.
	if len(task.Dependencies) > 0 {
		b.WriteString(fmt.Sprintf("  →  after %s", strings.Join(task.Dependencies, ", ")))
	}
	b.WriteByte('\n')

	// Description.
	if desc := strings.TrimSpace(task.Description); desc != "" {
		b.WriteByte('\n')
		b.WriteString(desc)
		b.WriteByte('\n')
	}

	// Acceptance criteria.
	if len(task.AcceptanceCriteria) > 0 {
		b.WriteString("\n**Acceptance Criteria:**\n\n")
		for _, ac := range task.AcceptanceCriteria {
			prefix := ""
			if ac.Priority != "" {
				prefix = fmt.Sprintf("[%s] ", ac.Priority)
			}
			b.WriteString(fmt.Sprintf("- %sGiven %s / When %s / Then %s\n", prefix, ac.Given, ac.When, ac.Then))
		}
	}

	// Affected files.
	if len(task.AffectedFiles) > 0 {
		b.WriteString("\n**Files:**")
		for _, f := range task.AffectedFiles {
			b.WriteString(fmt.Sprintf(" `%s` (%s)", f.Path, f.Operation))
		}
		b.WriteByte('\n')
	}

	// Status message (error or progress note).
	if task.StatusMessage != "" {
		b.WriteString(fmt.Sprintf("\n> %s\n", task.StatusMessage))
	}

	b.WriteByte('\n')
}
