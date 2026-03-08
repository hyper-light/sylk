package agent

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// TDD phase mapping
// ---------------------------------------------------------------------------

// tddPhases maps progress bar positions to the actual core TDD phase statuses.
var tddPhases = [4]string{
	"defining_criteria",
	"creating_tests",
	"executing",
	"validating",
}

// progressBarCells is the fixed number of cells in the pipeline shimmer bar.
const progressBarCells = 4

// tddPhaseIndex returns the 0-based index for a TDD phase status, or -1.
func tddPhaseIndex(status string) int {
	for i, phase := range tddPhases {
		if status == phase {
			return i
		}
	}
	return -1
}

// isTerminalPipelineStatus reports whether a pipeline status is final.
func isTerminalPipelineStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Variant state icons
// ---------------------------------------------------------------------------

// variantStateIcons maps variant state strings to display glyphs.
var variantStateIcons = map[string]string{
	"created":   theme.IconIdle,
	"active":    theme.IconActing,
	"suspended": theme.IconWaiting,
	"complete":  theme.IconSuccess,
	"failed":    theme.IconError,
	"merging":   theme.IconHandoff,
	"merged":    theme.IconSuccess,
	"cancelled": theme.IconError,
}

// ---------------------------------------------------------------------------
// Pipeline header rendering
// ---------------------------------------------------------------------------

// pipelinePrefix is the tree connector for pipeline member agents.
const pipelinePrefix = " \u2502 " // " │ "

// pipelineHeaderPrefix renders a pipeline subheading within the Pipelines section.
const pipelineHeaderPrefix = " \u2502  \u2500 " // " │  ─ "

// variantPrefix is the dotted tree connector for variant rows.
const variantPrefix = " \u2502  \u250a " // " │  ┊ "

// renderPipelineRow renders a pipeline header row.
// Layout: [indicator] [task-id] [status] [progress-bar] [loop/max]
// When activeColor is non-empty the indicator and task-id use the holographic
// group color; the task-id additionally gets a ripple shimmer via anim.
func renderPipelineRow(pl *PipelineState, width int, elapsed time.Duration, grad *theme.Gradient, th *theme.Theme, selected bool, activeColor lipgloss.Color, anim AnimState) string {
	// Task ID — always bold, matching renderSectionHeader.
	taskLabel := truncate(pipelineDisplayLabel(pl), 24)
	var name string
	if activeColor != "" && anim.Ripple {
		hGrad := anim.RippleGrad
		if selected && anim.HolographicGrad != nil {
			hGrad = anim.HolographicGrad
		}
		if hGrad != nil {
			name = "\x1b[1m" + theme.RenderRippleText(taskLabel, anim.Elapsed, hGrad, 0)
		}
	}
	if name == "" {
		nameStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
		if !selected {
			nameStyle = lipgloss.NewStyle().Foreground(th.Palette.Muted).Bold(true)
		}
		name = nameStyle.Render(taskLabel)
	}
	prefix := renderTreePrefix(pipelineHeaderPrefix, activeColor, th)

	// Status label.
	statusStyle := lipgloss.NewStyle().Foreground(th.Palette.Subtext)
	statusLabel := statusStyle.Render(truncate(pl.Status, 12))

	// Progress bar.
	bar := renderProgressBar(pl.Status, elapsed, grad, th)

	// Loop counter.
	loopStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	loopLabel := loopStyle.Render(fmt.Sprintf("%d/%d", pl.LoopCount, pl.MaxLoops))

	return fmt.Sprintf("%s%s %s %s %s", prefix, name, statusLabel, bar, loopLabel)
}

// renderProgressBar renders a 5-cell shimmer progress bar.
// Filled cells use gradient sampling; empty cells use muted "░".
func renderProgressBar(status string, elapsed time.Duration, grad *theme.Gradient, th *theme.Theme) string {
	filled := tddPhaseIndex(status) + 1
	if filled < 0 {
		filled = 0
	}
	if isTerminalPipelineStatus(status) {
		filled = progressBarCells
	}

	emptyCell := lipgloss.NewStyle().Foreground(th.Palette.Muted).Render("\u2591") // ░

	var cells [progressBarCells]string
	for i := range progressBarCells {
		if i < filled {
			// Shimmer: sample gradient at position+time offset.
			offset := float64(i) / float64(progressBarCells)
			phase := elapsed.Seconds() * 0.5
			t := math.Mod(offset+phase, 1.0)
			if t < 0 {
				t += 1.0
			}
			color := grad.Sample(time.Duration(t * float64(grad.Duration())))
			cells[i] = lipgloss.NewStyle().Foreground(color).Render("\u25fc") // ◼
		} else {
			cells[i] = emptyCell
		}
	}

	return strings.Join(cells[:], "")
}

// ---------------------------------------------------------------------------
// Variant row rendering
// ---------------------------------------------------------------------------

// renderVariantRow renders a single variant sub-row.
// Layout: [┊ ] [icon] [var_xxxx] [state] [message]
// When activeColor is non-empty the dotted prefix and short-ID use the
// holographic group color, and the ID gets a ripple shimmer via anim.
func renderVariantRow(v *VariantState, width int, th *theme.Theme, selected bool, activeColor lipgloss.Color, anim AnimState) string {
	prefixColor := th.Palette.Subtle
	if activeColor != "" {
		prefixColor = activeColor
	}
	prefix := lipgloss.NewStyle().Foreground(prefixColor).Render(variantPrefix)

	icon := variantStateIcons[v.State]
	if icon == "" {
		icon = theme.IconIdle
	}
	iconStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	if activeColor != "" {
		iconStyle = lipgloss.NewStyle().Foreground(activeColor)
	}
	if selected {
		iconStyle = th.AgentActive
	}
	iconStr := iconStyle.Render(icon)

	// Short ID: var_ + first 4 chars.
	shortID := v.ID
	if len(shortID) > 8 {
		shortID = "var_" + shortID[len(shortID)-4:]
	}

	var idStr string
	if activeColor != "" && anim.Ripple {
		hGrad := anim.RippleGrad
		if selected && anim.HolographicGrad != nil {
			hGrad = anim.HolographicGrad
		}
		if hGrad != nil {
			idStr = theme.RenderRippleText(shortID, anim.Elapsed, hGrad, 0)
		}
	}
	if idStr == "" {
		idStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
		if selected {
			idStyle = idStyle.Bold(true)
		}
		idStr = idStyle.Render(shortID)
	}

	stateStyle := lipgloss.NewStyle().Foreground(th.Palette.Subtext)
	stateStr := stateStyle.Render(v.State)

	// Remaining space for message.
	prefixW := lipgloss.Width(prefix)
	fixedW := prefixW + 1 + lipgloss.Width(shortID) + 1 + lipgloss.Width(v.State) + 3 // spaces
	msgWidth := width - fixedW
	message := truncate(v.Message, msgWidth)
	msgStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	msgStr := msgStyle.Render(message)

	return fmt.Sprintf("%s%s %s %s %s", prefix, iconStr, idStr, stateStr, msgStr)
}

// ---------------------------------------------------------------------------
// Expanded detail views
// ---------------------------------------------------------------------------

// renderExpandedPipeline renders the detail view for a pipeline.
func renderExpandedPipeline(pl *PipelineState, width, height int, th *theme.Theme) string {
	labelStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	valueStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)

	lines := []string{
		fmt.Sprintf("  %s %s", labelStyle.Render("Task:"), valueStyle.Render(pl.TaskID)),
	}
	if pl.TaskLabel != "" && pl.TaskLabel != pl.TaskID {
		lines = append(lines, fmt.Sprintf("  %s %s", labelStyle.Render("Label:"), valueStyle.Render(pl.TaskLabel)))
	}
	lines = append(lines,
		fmt.Sprintf("  %s %s", labelStyle.Render("Status:"), valueStyle.Render(pl.Status)),
		fmt.Sprintf("  %s %s", labelStyle.Render("Phase:"), valueStyle.Render(tddPhaseLabel(pl.Status))),
		fmt.Sprintf("  %s %s", labelStyle.Render("Loop:"), valueStyle.Render(fmt.Sprintf("%d / %d", pl.LoopCount, pl.MaxLoops))),
		fmt.Sprintf("  %s %s", labelStyle.Render("Worker:"), valueStyle.Render(pl.WorkerType)),
		renderDetailSeparator(width, th),
		fmt.Sprintf("  %s %d agent(s)", labelStyle.Render("Members:"), len(pl.Members)),
	)

	end := min(len(lines), height)
	return strings.Join(lines[:end], "\n")
}

// renderExpandedVariant renders the detail view for a variant.
func renderExpandedVariant(v *VariantState, width, height int, th *theme.Theme) string {
	labelStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	valueStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)

	lines := []string{
		fmt.Sprintf("  %s %s", labelStyle.Render("Variant:"), valueStyle.Render(v.ID)),
		fmt.Sprintf("  %s %s", labelStyle.Render("Name:"), valueStyle.Render(v.Name)),
		fmt.Sprintf("  %s %s", labelStyle.Render("State:"), valueStyle.Render(v.State)),
		fmt.Sprintf("  %s %s", labelStyle.Render("Pipeline:"), valueStyle.Render(v.PipelineID)),
		renderDetailSeparator(width, th),
		fmt.Sprintf("  %s %s", labelStyle.Render("Message:"), valueStyle.Render(v.Message)),
	}

	end := min(len(lines), height)
	return strings.Join(lines[:end], "\n")
}

// tddPhaseLabel returns a human-readable label for a TDD status.
func tddPhaseLabel(status string) string {
	idx := tddPhaseIndex(status)
	if idx < 0 {
		return status
	}
	return fmt.Sprintf("%s (%d/%d)", status, idx+1, progressBarCells)
}
