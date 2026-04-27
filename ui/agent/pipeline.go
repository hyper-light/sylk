package agent

import (
	"fmt"
	"math"
	"strconv"
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

const pipelineProgressGlyph = "\u25a0" // ■

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

// pipelineHeaderIndent renders the pipeline subheading gutter within the Pipelines section.
const pipelineHeaderIndent = " \u2502  " // " │  "

// variantPrefix is the dotted tree connector for variant rows.
const variantPrefix = " \u2502  \u250a " // " │  ┊ "

// renderPipelineRow renders a pipeline header row.
// Layout: [indicator] [task-id] [status]   [progress-bar] [loop/max]
// The progress bar and counter are right-aligned; the task-id and status are
// truncated as needed so the row always fits on a single line.
// When activeColor is non-empty the indicator and task-id use the holographic
// group color; the task-id additionally gets a ripple shimmer via anim.
func renderPipelineRow(pl *PipelineState, width int, elapsed time.Duration, grad *theme.Gradient, th *theme.Theme, selected bool, activeColor lipgloss.Color, anim AnimState, collapsed bool) string {
	prefixPlain := pipelineHeaderPrefix(collapsed)
	prefix := renderTreePrefix(prefixPlain, activeColor, th)

	bar := renderProgressBar(pl.Status, elapsed, grad, th)
	loopStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	loopText := formatPipelineCounterLabel(pl)
	loopLabel := loopStyle.Render(loopText)
	rightWidth := progressBarCells + 1 + lipgloss.Width(loopText) // bar + space + counter

	available := width - lipgloss.Width(prefixPlain) - rightWidth - 1
	if available < 1 {
		available = 1
	}

	taskLabel, _ := fitPipelineHeaderInline(pipelineDisplayLabel(pl), "", available)
	name := renderPipelineTaskLabel(taskLabel, "", selected, activeColor, anim, th)

	leftPlainWidth := lipgloss.Width(taskLabel)
	left := prefix + name

	pad := width - lipgloss.Width(prefixPlain) - leftPlainWidth - rightWidth
	if pad < 1 {
		pad = 1
	}

	return left + strings.Repeat(" ", pad) + bar + " " + loopLabel
}

func pipelineRowLineCount(pl *PipelineState, width int) int {
	if pl == nil {
		return 0
	}
	return 1
}

func pipelineHeaderPrefix(collapsed bool) string {
	marker := "\u2500" // ─
	if collapsed {
		marker = "+"
	}
	return pipelineHeaderIndent + marker + " "
}

// fitPipelineHeaderInline returns task label and status sized to fit
// within available width (which excludes the prefix and the right-aligned
// bar+counter). Status is shrunk before the task label, and dropped entirely
// when there is not enough room to show it meaningfully.
func fitPipelineHeaderInline(taskLabel, statusText string, available int) (string, string) {
	if available <= 0 {
		return "", ""
	}
	task := truncate(taskLabel, min(24, available))
	status := truncate(statusText, 12)

	taskW := lipgloss.Width(task)
	statusW := lipgloss.Width(status)
	sep := 0
	if statusW > 0 {
		sep = 1
	}
	if taskW+sep+statusW <= available {
		return task, status
	}

	if statusW > 0 {
		minTaskBudget := min(taskW, max(6, available/2))
		statusBudget := available - minTaskBudget - 1
		if statusBudget >= 1 {
			status = truncate(status, statusBudget)
			statusW = lipgloss.Width(status)
			taskBudget := available - statusW - 1
			if taskBudget >= 1 {
				return truncate(task, taskBudget), status
			}
		}
	}

	return truncate(task, available), ""
}

func renderPipelineTaskLabel(taskLabel, styledName string, selected bool, activeColor lipgloss.Color, anim AnimState, th *theme.Theme) string {
	if taskLabel == "" {
		return ""
	}
	if stripANSIForPipelineWidth(styledName) == taskLabel {
		return styledName
	}
	if activeColor != "" && anim.Ripple {
		hGrad := anim.RippleGrad
		if selected && anim.HolographicGrad != nil {
			hGrad = anim.HolographicGrad
		}
		if hGrad != nil {
			return "\x1b[1m" + theme.RenderRippleText(taskLabel, anim.Elapsed, hGrad, 0)
		}
	}
	nameStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	if !selected {
		nameStyle = lipgloss.NewStyle().Foreground(th.Palette.Muted).Bold(true)
	}
	return nameStyle.Render(taskLabel)
}

func stripANSIForPipelineWidth(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// renderProgressBar renders a 4-cell shimmer progress bar.
// All cells use the same square glyph so the bar doesn't visually collapse as
// phases advance. Future phases still animate, but on a dimmed version of the
// active gradient.
func renderProgressBar(status string, elapsed time.Duration, grad *theme.Gradient, th *theme.Theme) string {
	filled := activePipelineCells(status)

	var cells [progressBarCells]string
	for i := range progressBarCells {
		offset := float64(i) / float64(progressBarCells)
		phase := elapsed.Seconds() * 0.5
		t := math.Mod(offset+phase, 1.0)
		if t < 0 {
			t += 1.0
		}
		color := grad.Sample(time.Duration(t * float64(grad.Duration())))
		if i >= filled {
			color = blendPipelineColor(color, th.Palette.Muted, 0.72)
		}
		cells[i] = lipgloss.NewStyle().Foreground(color).Bold(true).Render(pipelineProgressGlyph)
	}

	return strings.Join(cells[:], "")
}

func activePipelineCells(status string) int {
	if isTerminalPipelineStatus(status) {
		return progressBarCells
	}
	filled := tddPhaseIndex(status) + 1
	if filled < 0 {
		return 0
	}
	if filled > progressBarCells {
		return progressBarCells
	}
	return filled
}

func formatPipelineCounterLabel(pl *PipelineState) string {
	if pl == nil {
		return "0/0"
	}
	if pl.ClaimsTotalCount > 0 {
		return fmt.Sprintf("%d/%d", pl.ClaimsAccepted, pl.ClaimsTotalCount)
	}
	if pl.MaxLoops > 0 {
		loopCount := pl.LoopCount
		if loopCount < 0 {
			loopCount = 0
		}
		return fmt.Sprintf("%d/%d", loopCount, pl.MaxLoops)
	}
	return fmt.Sprintf("%d/%d", activePipelineCells(pl.Status), progressBarCells)
}

func blendPipelineColor(base, target lipgloss.Color, targetWeight float64) lipgloss.Color {
	if targetWeight <= 0 {
		return base
	}
	if targetWeight >= 1 {
		return target
	}
	br, bg, bb := parsePipelineColor(base)
	tr, tg, tb := parsePipelineColor(target)
	keepWeight := 1.0 - targetWeight
	r := uint8(float64(br)*keepWeight + float64(tr)*targetWeight + 0.5)
	g := uint8(float64(bg)*keepWeight + float64(tg)*targetWeight + 0.5)
	b := uint8(float64(bb)*keepWeight + float64(tb)*targetWeight + 0.5)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}

func parsePipelineColor(color lipgloss.Color) (uint8, uint8, uint8) {
	s := strings.TrimPrefix(string(color), "#")
	if len(s) != 6 {
		return 0, 0, 0
	}
	r, err := strconv.ParseUint(s[0:2], 16, 8)
	if err != nil {
		return 0, 0, 0
	}
	g, err := strconv.ParseUint(s[2:4], 16, 8)
	if err != nil {
		return 0, 0, 0
	}
	b, err := strconv.ParseUint(s[4:6], 16, 8)
	if err != nil {
		return 0, 0, 0
	}
	return uint8(r), uint8(g), uint8(b)
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
	if pl == nil {
		return ""
	}
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
	if v == nil {
		return ""
	}
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
