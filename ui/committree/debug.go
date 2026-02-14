package committree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// debugLogPath is the file where click debug logs are written.
var debugLogPath = filepath.Join(os.TempDir(), "sylk_click_debug.log")

// debugLog appends a timestamped line to the debug log file.
func debugLog(format string, args ...any) {
	f, err := os.OpenFile(debugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(f, "[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

// debugLogClick logs the full click pipeline for branch view clicks.
func (m *Model) debugLogClick(viewX, viewY int) {
	topPad := m.viewTopPad()
	debugLog("=== BRANCH CLICK ===")
	debugLog("  raw: viewX=%d viewY=%d", viewX, viewY)
	debugLog("  viewTopPad=%d  adjustedY=%d", topPad, viewY-topPad)
	debugLog("  panel: width=%d height=%d contentHeight=%d", m.width, m.height, m.contentHeight())
	debugLog("  expandedIdx=%d branchIdx=%d branchCount=%d", m.expandedIdx, m.branchIdx, len(m.branches))
}

// debugLogCardHit logs the card-relative coordinates and hit test details.
func (m *Model) debugLogCardHit(targetIdx, localY, localX, cardLeft, cardW, cardH int) {
	debugLog("  card target: idx=%d cardLeft=%d cardW=%d cardH=%d", targetIdx, cardLeft, cardW, cardH)
	debugLog("  card-relative: localY=%d localX=%d", localY, localX)
	debugLog("  expanded guard: localY>=3=%v localY<cardH=%v", localY >= 3, localY < cardH)

	if m.expandedIdx != targetIdx {
		debugLog("  (not the expanded card)")
		return
	}

	// Log badge layout details.
	b := m.branches[m.expandedIdx]
	innerWidth := m.expandedCardInnerWidth()
	actions := visibleActions(b, m.defaultBranch)
	statsWidth := actionLinePrefixWidth(b, m.workingDirty, m.workingConflicts)

	debugLog("  badge layout inputs: innerWidth=%d statsWidth=%d actions=%v", innerWidth, statsWidth, actionNames(actions))

	// Also log the rendering-side statsWidth for comparison.
	exp := m.buildExpansion(false)
	if exp != nil {
		statsLine := buildExpStatsLine(b, exp, m.theme.Palette)
		renderStatsWidth := lipgloss.Width(statsLine)
		debugLog("  render statsWidth=%d (lipgloss.Width) vs hit statsWidth=%d (actionLinePrefixWidth)", renderStatsWidth, statsWidth)
	}

	placements, badgeLineCount := computeBadgeLayout(actions, innerWidth, statsWidth)
	debugLog("  badge placements (badgeLineCount=%d):", badgeLineCount)
	for i, bp := range placements {
		debugLog("    [%d] %s: line=%d x=%d w=%d (xRange=[%d,%d))",
			i, actionBadgeLabel(actions[i]), bp.line, bp.x, bp.w, bp.x, bp.x+bp.w)
	}

	// Log hit regions.
	regions := m.expandedHitRegions()
	debugLog("  hit regions (%d):", len(regions))
	for i, r := range regions {
		debugLog("    [%d] Y=%d XMin=%d XMax=%d kind=%d idx=%d",
			i, r.Y, r.XMin, r.XMax, r.Kind, r.Idx)
	}

	// Log match result.
	matched := false
	for _, r := range regions {
		if localY == r.Y && localX >= r.XMin && localX < r.XMax {
			debugLog("  >>> HIT: region Y=%d X=[%d,%d) kind=%d idx=%d", r.Y, r.XMin, r.XMax, r.Kind, r.Idx)
			matched = true
			break
		}
	}
	if !matched {
		debugLog("  >>> MISS: no region matched localY=%d localX=%d", localY, localX)
		// Log nearest regions for diagnosis.
		for _, r := range regions {
			yMatch := localY == r.Y
			xMatch := localX >= r.XMin && localX < r.XMax
			debugLog("    region Y=%d X=[%d,%d): yMatch=%v xMatch=%v (deltaY=%d)",
				r.Y, r.XMin, r.XMax, yMatch, xMatch, localY-r.Y)
		}
	}
}

// debugLogExpandedLines logs the actual rendered expanded content lines.
func debugLogExpandedLines(lines []string, innerWidth int) {
	debugLog("  rendered expanded lines (%d):", len(lines))
	for i, line := range lines {
		vis := lipgloss.Width(line)
		debugLog("    line[%d]: visWidth=%d innerWidth=%d content=%q", i, vis, innerWidth, stripAnsi(line))
	}
}

// stripAnsi removes ANSI escape sequences for readable debug output.
func stripAnsi(s string) string {
	var buf strings.Builder
	inESC := false
	for _, r := range s {
		if r == '\x1b' {
			inESC = true
			continue
		}
		if inESC {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inESC = false
			}
			continue
		}
		buf.WriteRune(r)
	}
	return buf.String()
}

// actionNames returns human-readable names for a slice of action IDs.
func actionNames(actions []int) string {
	names := make([]string, len(actions))
	for i, id := range actions {
		names[i] = actionBadgeLabel(id)
	}
	return strings.Join(names, ", ")
}
