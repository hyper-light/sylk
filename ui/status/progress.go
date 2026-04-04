package status

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// IndexPhase identifies which knowledge indexing phase is active.
type IndexPhase int

const (
	PhaseNone   IndexPhase = iota
	PhaseLoad              // Scanning / loading files
	PhaseEmbed             // Parse + chunk + embed
	PhaseCommit            // Committing to global stores
	PhaseIndex             // Building Bleve index (background)
	PhaseGraph             // Building knowledge graph (future)
	PhaseDone              // Completed (shown briefly before hiding)
)

// phaseLabels maps each phase to a fixed 4-char display label.
var phaseLabels = [...]string{
	PhaseNone:   "",
	PhaseLoad:   "load",
	PhaseEmbed:  "embd",
	PhaseCommit: "cmmt",
	PhaseIndex:  "indx",
	PhaseGraph:  "grph",
	PhaseDone:   "done",
}

// phaseOrder defines the sequential progression of indexing stages.
// Each stage occupies an equal fraction of the overall bar.
// Length is derived at init; no magic constants.
var phaseOrder = [...]IndexPhase{PhaseLoad, PhaseEmbed, PhaseCommit, PhaseIndex}

// phasePosition maps each active phase to its 0-based index in phaseOrder.
// Built once at init from phaseOrder so lookups are O(1).
var phasePosition [PhaseDone]int

func init() {
	// -1 sentinel for phases not in the order (PhaseNone, PhaseGraph, etc.).
	for i := range phasePosition {
		phasePosition[i] = -1
	}
	for i, p := range phaseOrder {
		phasePosition[p] = i
	}
}

// barWidth is the cell count per stage segment in the progress bar.
// Total visual cells = barWidth * len(phaseOrder).
const barWidth = 12

// doneDuration is how long the "done" state persists before auto-hiding.
const doneDuration = 1 * time.Second

// lerpSnap is the threshold below which displayFrac snaps to targetFrac.
// Derived from the smallest visible step: 1 cell out of total cells.
var lerpSnap = 1.0 / float64(barWidth)

// lerpRate controls how fast displayFrac chases targetFrac per tick.
// At 10fps (100ms ticks), 0.45 closes the gap in ~3 frames (~300-350ms).
const lerpRate = 0.45

// ProgressDisplay renders a thin segmented progress bar with phase label
// for the status bar's right section. The bar is divided into equal
// segments (one per stage), separated by space gaps. An exponential
// ease-out interpolation smooths transitions between stages.
type ProgressDisplay struct {
	phase   IndexPhase
	current int64 // progress within the active phase
	total   int64 // total work within the active phase

	// Smooth animation: displayFrac is what's rendered, targetFrac is
	// the goal computed from phase/current/total. Tick() advances
	// displayFrac toward targetFrac each decor frame.
	displayFrac float64
	targetFrac  float64

	doneUntil time.Time // hold "done" briefly before hiding

	filledStyle lipgloss.Style
	emptyStyle  lipgloss.Style
	labelStyle  lipgloss.Style
}

// NewProgressDisplay creates a ProgressDisplay with the given styles.
// filled: style for completed bar cells (green/normal).
// empty: style for remaining bar cells (muted).
// label: style for the phase label.
func NewProgressDisplay(filled, empty, label lipgloss.Style) *ProgressDisplay {
	return &ProgressDisplay{
		filledStyle: filled,
		emptyStyle:  empty,
		labelStyle:  label,
	}
}

// Update sets the active phase and its local progress counters.
// Pipeline phases that fire after completion should pass current=1, total=1.
// The background indexer passes real indexed/total counts.
func (pd *ProgressDisplay) Update(phase IndexPhase, current, total int64) {
	pd.phase = phase
	pd.current = current
	pd.total = total
	pd.targetFrac = pd.computeTarget()
}

// Tick advances the displayed fraction toward the target using exponential
// ease-out. Called by the status bar decor tick (~10fps). Returns true if
// the display changed (caller should mark dirty).
func (pd *ProgressDisplay) Tick() bool {
	if pd.displayFrac == pd.targetFrac {
		return false
	}
	gap := pd.targetFrac - pd.displayFrac
	if math.Abs(gap) < lerpSnap {
		pd.displayFrac = pd.targetFrac
		return true
	}
	pd.displayFrac += gap * lerpRate
	return true
}

// IsAnimating reports whether the display fraction is still catching up
// to the target, requiring continued ticks.
func (pd *ProgressDisplay) IsAnimating() bool {
	return pd.displayFrac != pd.targetFrac
}

// NeedsTick reports whether a decor tick is still required, either because
// the display is animating toward its target or because a terminal done
// state still needs expiry processing.
func (pd *ProgressDisplay) NeedsTick() bool {
	return pd.IsAnimating() || (pd.phase == PhaseDone && !pd.doneUntil.IsZero())
}

// Clear transitions to PhaseDone with a brief hold before hiding.
func (pd *ProgressDisplay) Clear() {
	pd.phase = PhaseDone
	pd.current = 0
	pd.total = 0
	pd.targetFrac = 1.0
	pd.doneUntil = time.Now().Add(doneDuration)
}

// IsActive reports whether the progress bar should be visible.
func (pd *ProgressDisplay) IsActive() bool {
	if pd.phase == PhaseNone {
		return false
	}
	if pd.phase == PhaseDone {
		return !pd.doneUntil.IsZero() && time.Now().Before(pd.doneUntil)
	}
	return true
}

// CheckExpiry transitions from PhaseDone to PhaseNone when the hold expires.
// Returns true if state changed (caller should mark dirty).
func (pd *ProgressDisplay) CheckExpiry(now time.Time) bool {
	if pd.phase != PhaseDone {
		return false
	}
	if pd.doneUntil.IsZero() || !now.Before(pd.doneUntil) {
		pd.phase = PhaseNone
		pd.current = 0
		pd.total = 0
		pd.displayFrac = 0
		pd.targetFrac = 0
		pd.doneUntil = time.Time{}
		return true
	}
	return false
}

// View renders the progress display as a segmented bar across all stages.
// Each stage occupies cellsPerStage cells, separated by a space gap.
// Uses displayFrac (smoothed) rather than targetFrac for rendering.
// Returns empty string when inactive.
func (pd *ProgressDisplay) View() string {
	if !pd.IsActive() {
		return ""
	}

	stageCount := len(phaseOrder)
	cellsPerStage := barWidth / stageCount
	totalCells := cellsPerStage * stageCount
	filled := min(int(pd.displayFrac*float64(totalCells)), totalCells)

	var bar strings.Builder
	for s := range stageCount {
		if s > 0 {
			bar.WriteByte(' ')
		}
		segStart := s * cellsPerStage
		segFilled := min(max(filled-segStart, 0), cellsPerStage)
		tipInSeg := filled >= segStart && filled < segStart+cellsPerStage && filled < totalCells
		emptyCount := cellsPerStage - segFilled
		if tipInSeg {
			emptyCount--
		}

		if segFilled > 0 {
			bar.WriteString(pd.filledStyle.Render(strings.Repeat("━", segFilled)))
		}
		if tipInSeg {
			bar.WriteString(pd.filledStyle.Render("╸"))
		}
		if emptyCount > 0 {
			bar.WriteString(pd.emptyStyle.Render(strings.Repeat("─", emptyCount)))
		}
	}

	label := pd.labelStyle.Render(phaseLabels[pd.phase])
	pct := pd.labelStyle.Render(pd.percentageStr())
	return label + " " + bar.String() + " " + pct
}

// computeTarget returns the target fraction [0, 1] from the current
// phase and local progress.
func (pd *ProgressDisplay) computeTarget() float64 {
	if pd.phase == PhaseDone {
		return 1.0
	}
	pos := phasePosition[pd.phase]
	if pos < 0 {
		return 0
	}

	stageCount := float64(len(phaseOrder))
	completed := float64(pos) / stageCount

	var local float64
	if pd.total > 0 {
		local = float64(pd.current) / float64(pd.total)
		if local > 1 {
			local = 1
		}
	}

	return completed + local/stageCount
}

// percentageStr returns a right-aligned percentage string like " 62%".
func (pd *ProgressDisplay) percentageStr() string {
	pct := int(pd.displayFrac * 100)
	if pct > 100 {
		pct = 100
	}
	switch {
	case pct < 10:
		return "  " + itoa(pct) + "%"
	case pct < 100:
		return " " + itoa(pct) + "%"
	default:
		return "100%"
	}
}

// itoa is a minimal int-to-string for small non-negative values.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
