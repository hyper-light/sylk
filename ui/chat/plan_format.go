package chat

import (
	"strconv"
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

// estimatePlanSize returns a rough byte estimate for the rendered markdown.
// Avoids repeated Builder reallocations for typical plan sizes.
func estimatePlanSize(taskCount int) int {
	// Header (~32) + per-task (~384) + footer (~48).
	return 80 + taskCount*384
}

// formatPlanMarkdown renders a PlanUpdateMsg as a markdown string suitable
// for the chat panel's existing CommonMark renderer.
func formatPlanMarkdown(update msg.PlanUpdateMsg) string {
	var b strings.Builder
	b.Grow(estimatePlanSize(len(update.Tasks)))

	b.WriteString("## Plan\n\n**Status:** ")
	b.WriteString(update.Status)
	b.WriteString("\n\n")

	if len(update.Tasks) == 0 {
		return b.String()
	}

	// Build task lookup for layer-based ordering.
	taskByID := make(map[string]msg.PlanTaskSnapshot, len(update.Tasks))
	for i := range update.Tasks {
		taskByID[update.Tasks[i].ID] = update.Tasks[i]
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
		b.WriteString(strconv.Itoa(layers))
		b.WriteString(" layers, ")
		b.WriteString(strconv.Itoa(tasks))
		b.WriteString(" tasks\n")
	} else {
		b.WriteString(strconv.Itoa(tasks))
		b.WriteString(" tasks\n")
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
			b.WriteString("**Layer ")
			b.WriteString(strconv.Itoa(layerIdx + 1))
			b.WriteString("**\n\n")
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

	// Append any tasks not in any layer (defensive).
	totalIDs := 0
	for _, layer := range layers {
		totalIDs += len(layer)
	}
	if totalIDs < len(allTasks) {
		layerSet := make(map[string]struct{}, totalIDs)
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
}

// formatPlanFlat renders tasks as a simple numbered list.
func formatPlanFlat(b *strings.Builder, tasks []msg.PlanTaskSnapshot) {
	b.WriteString("### Tasks\n\n")
	for i := range tasks {
		formatPlanTask(b, tasks[i], i+1)
	}
}

// formatPlanTask renders a single task entry.
func formatPlanTask(b *strings.Builder, task msg.PlanTaskSnapshot, num int) {
	icon := planStatusIcon(task.Status)

	// Header line: **1. Task Name** `agent-type` ○
	b.WriteString("**")
	b.WriteString(strconv.Itoa(num))
	b.WriteString(". ")
	b.WriteString(task.Name)
	b.WriteString("** `")
	b.WriteString(task.AgentType)
	b.WriteString("` ")
	b.WriteString(icon)

	// Dependencies suffix.
	if len(task.Dependencies) > 0 {
		b.WriteString("  →  after ")
		b.WriteString(strings.Join(task.Dependencies, ", "))
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
			b.WriteString("- ")
			if ac.Priority != "" {
				b.WriteByte('[')
				b.WriteString(ac.Priority)
				b.WriteString("] ")
			}
			b.WriteString("Given ")
			b.WriteString(ac.Given)
			b.WriteString(" / When ")
			b.WriteString(ac.When)
			b.WriteString(" / Then ")
			b.WriteString(ac.Then)
			b.WriteByte('\n')
		}
	}

	// Affected files.
	if len(task.AffectedFiles) > 0 {
		b.WriteString("\n**Files:**")
		for _, f := range task.AffectedFiles {
			b.WriteString(" `")
			b.WriteString(f.Path)
			b.WriteString("` (")
			b.WriteString(f.Operation)
			b.WriteByte(')')
		}
		b.WriteByte('\n')
	}

	// Implementation guide.
	if task.ImplementationGuide != "" {
		b.WriteString("\n**Guide:**\n\n")
		b.WriteString(task.ImplementationGuide)
		b.WriteByte('\n')
	}

	// Guidelines.
	if len(task.Guidelines) > 0 {
		b.WriteString("\n**Guidelines:**\n\n")
		for _, g := range task.Guidelines {
			b.WriteString("- ")
			b.WriteString(g)
			b.WriteByte('\n')
		}
	}

	// Status message (error or progress note).
	if task.StatusMessage != "" {
		b.WriteString("\n> ")
		b.WriteString(task.StatusMessage)
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
}
