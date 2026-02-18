package committree

import (
	"fmt"
	"slices"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// nodeHeight is the number of terminal rows consumed per commit node.
// Derived from: card top border(1) + header(1) + subject(1) +
// card bottom border(1) + edge connector(1) = 5.
const nodeHeight = 5

// commitMarker is the Unicode bullet used as the commit graph node glyph.
const commitMarker = "●"

// edgeGlyph is the vertical connector drawn between commit cards.
const edgeGlyph = "│"

// ellipsis is appended when a string is truncated to fit available width.
const ellipsis = "..."

// minLayoutGap is the minimum gap between left-aligned and right-aligned
// segments in a card content line. Ensures readability when content fills
// the card width.
const minLayoutGap = 2

// diffSelection records one commit chosen in diff selection mode.
type diffSelection struct {
	hash    string
	nodeIdx int
}

// diffOverlay carries the diff-mode visual state for a single card.
// When active is true, the card's border is colored and an icon is shown.
type diffOverlay struct {
	active bool
	idx    int // selection index: determines color + icon
}

// diffSelectionColors maps selection index to a palette color accessor.
// The array length defines the maximum number of simultaneous selections.
var diffSelectionColors = [...]func(theme.Palette) lipgloss.Color{
	func(p theme.Palette) lipgloss.Color { return p.Primary },   // blue
	func(p theme.Palette) lipgloss.Color { return p.Success },   // green
	func(p theme.Palette) lipgloss.Color { return p.Peach },     // peach
	func(p theme.Palette) lipgloss.Color { return p.Secondary }, // mauve
	func(p theme.Palette) lipgloss.Color { return p.Teal },      // teal
	func(p theme.Palette) lipgloss.Color { return p.Warning },   // yellow
	func(p theme.Palette) lipgloss.Color { return p.Lavender },  // lavender
	func(p theme.Palette) lipgloss.Color { return p.Accent },    // pink
}

// diffSelectionIcons are the circled-number glyphs shown in the top-left
// corner of diff-selected cards, indexed by selection order.
var diffSelectionIcons = [...]string{"①", "②", "③", "④", "⑤", "⑥", "⑦", "⑧"}

// diffColorForIdx returns the border color for a given diff selection index.
func diffColorForIdx(idx int, p theme.Palette) lipgloss.Color {
	return diffSelectionColors[idx%len(diffSelectionColors)](p)
}

// laneColors is the palette of colors assigned to graph lanes.
// Cycled by lane index mod len. Chosen for contrast and consistency
// with the Catppuccin-based theme palette.
var laneColors = []func(theme.Palette) lipgloss.Color{
	func(p theme.Palette) lipgloss.Color { return p.Primary },    // blue
	func(p theme.Palette) lipgloss.Color { return p.Secondary },  // mauve
	func(p theme.Palette) lipgloss.Color { return p.Success },    // green
	func(p theme.Palette) lipgloss.Color { return p.Warning },    // yellow
	func(p theme.Palette) lipgloss.Color { return p.Teal },       // teal
	func(p theme.Palette) lipgloss.Color { return p.Peach },      // peach
	func(p theme.Palette) lipgloss.Color { return p.Lavender },   // lavender
}

// laneColorForIdx returns the color for a given lane index.
func laneColorForIdx(idx int, p theme.Palette) lipgloss.Color {
	return laneColors[idx%len(laneColors)](p)
}

// renderNode renders a single commit card centered in the panel with trunk
// connectors matching the branch tree card layout. When graph is non-nil,
// falls back to the full-width DAG gutter layout.
//
// Centered layout (no graph):
//
//	           ╭──────────────────────────╮
//	           │ ● abc1234  +12 -3  2h ago│
//	           │   Add git mode scaffoldin│
//	           ╰────────────┬─────────────╯
//	                        │
//	           ╭────────────┴─────────────╮
//	           │ ● def5678       3h ago   │
//	           │   Fix edge case          │
//	           ╰──────────────────────────╯
func renderNode(n TreeNode, selected bool, width int, th *theme.Theme, isFirst, isLast bool, graph *GraphRow, maxLane int, diff diffOverlay) []string {
	if graph != nil {
		return renderNodeDAG(n, selected, width, th, isLast, graph, maxLane, diff)
	}

	p := th.Palette
	cardWidth := primaryCardWidth(width)
	const borderCols = 2
	innerWidth := max(cardWidth-borderCols, 0)
	leftPad := trunkAlignedMargin(width, cardWidth)
	trunkPos := width / 2
	trunkInner := trunkPos - leftPad - 1

	borderColor := p.Border
	switch {
	case selected:
		borderColor = p.Rosewater
	case diff.active:
		borderColor = diffColorForIdx(diff.idx, p)
	}
	bSt := lipgloss.NewStyle().Foreground(borderColor)

	header := padContent(buildHeaderLine(n, innerWidth, p), innerWidth)
	subject := padContent(buildSubjectLine(n, innerWidth, p), innerWidth)

	var icon string
	var iconSt lipgloss.Style
	if diff.active {
		icon = diffSelectionIcons[diff.idx%len(diffSelectionIcons)]
		iconSt = lipgloss.NewStyle().Foreground(diffColorForIdx(diff.idx, p))
	}

	topBorder := buildCardBorder("╭", "╮", innerWidth, bSt, trunkInner, !isFirst, icon, iconSt)
	botBorder := buildCardBorder("╰", "╯", innerWidth, bSt, trunkInner, !isLast, "", lipgloss.Style{})

	pad := strings.Repeat(" ", leftPad)
	vis := leftPad + cardWidth

	trunkSt := lipgloss.NewStyle().Foreground(p.Border)
	var trunkLine string
	if !isLast {
		trunk := strings.Repeat(" ", trunkPos) + trunkSt.Render("│")
		trunkLine = padRight(trunk, trunkPos+1, width)
	} else {
		trunkLine = strings.Repeat(" ", max(width, 0))
	}

	return []string{
		padRight(pad+topBorder, vis, width),
		padRight(pad+bSt.Render("│")+header+bSt.Render("│"), vis, width),
		padRight(pad+bSt.Render("│")+subject+bSt.Render("│"), vis, width),
		padRight(pad+botBorder, vis, width),
		trunkLine,
	}
}

// renderNodeDAG renders a commit card with a DAG graph gutter prepended.
func renderNodeDAG(n TreeNode, selected bool, width int, th *theme.Theme, isLast bool, graph *GraphRow, maxLane int, diff diffOverlay) []string {
	p := th.Palette
	gutterWidth := (maxLane + 1) * laneWidth
	const borderCols = 2
	cardWidth := max(width-gutterWidth, borderCols+1)
	innerWidth := max(cardWidth-borderCols, 0)

	borderColor := p.Border
	switch {
	case selected:
		borderColor = p.Rosewater
	case diff.active:
		borderColor = diffColorForIdx(diff.idx, p)
	}
	bSt := lipgloss.NewStyle().Foreground(borderColor)

	header := padContent(buildHeaderLine(n, innerWidth, p), innerWidth)
	subject := padContent(buildSubjectLine(n, innerWidth, p), innerWidth)

	var topBorder string
	if diff.active && innerWidth > 0 {
		icon := diffSelectionIcons[diff.idx%len(diffSelectionIcons)]
		iconSt := lipgloss.NewStyle().Foreground(diffColorForIdx(diff.idx, p))
		topBorder = iconSt.Render(icon) + bSt.Render(strings.Repeat("─", innerWidth)+"╮")
	} else {
		topBorder = bSt.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	}
	botBorder := bSt.Render("╰" + strings.Repeat("─", innerWidth) + "╯")

	var connector string
	if !isLast {
		edgeSt := lipgloss.NewStyle().Foreground(p.Border)
		connector = "  " + edgeSt.Render(edgeGlyph)
	}
	connectorVis := 0
	if connector != "" {
		connectorVis = 3
	}
	blank := strings.Repeat(" ", max(cardWidth, 0))

	cardLines := []string{
		topBorder,
		bSt.Render("│") + header + bSt.Render("│"),
		bSt.Render("│") + subject + bSt.Render("│"),
		botBorder,
		padRight(connector, connectorVis, cardWidth),
	}
	for len(cardLines) < nodeHeight {
		cardLines = append(cardLines, blank)
	}
	if len(cardLines) > nodeHeight {
		cardLines = cardLines[:nodeHeight]
	}

	lines := make([]string, nodeHeight)
	for lineIdx := range nodeHeight {
		gutter := renderGraphGutter(*graph, lineIdx, maxLane, p)
		lines[lineIdx] = padLine(gutter+cardLines[lineIdx], width)
	}
	return lines
}

// renderGraphGutter renders the gutter segment for a single line of a card.
// lineIdx ranges 0..nodeHeight-1 corresponding to:
//
//	0: top border    — │ for active lanes, space at commit lane
//	1: header line   — ● at commit lane, │ elsewhere, ╮─ for merges
//	2: subject line  — │ for all active lanes
//	3: bottom border — │ for all active lanes
//	4: connector     — │ for continuing lanes
func renderGraphGutter(row GraphRow, lineIdx int, maxLane int, p theme.Palette) string {
	var buf strings.Builder
	lanes := row.Lanes
	cols := maxLane + 1

	for lane := range cols {
		glyph := LaneEmpty
		if lane < len(lanes) {
			glyph = lanes[lane]
		}
		color := laneColorForIdx(lane, p)
		st := lipgloss.NewStyle().Foreground(color)

		ch := renderLaneCell(glyph, lane, row.CommitLane, lineIdx, st, p)
		buf.WriteString(ch)
		// Pad to laneWidth (glyph is 1 col, pad with space).
		for i := 1; i < laneWidth; i++ {
			buf.WriteByte(' ')
		}
	}
	return buf.String()
}

// renderLaneCell returns the single-character glyph for a lane cell.
func renderLaneCell(glyph LaneGlyph, lane, commitLane, lineIdx int, st lipgloss.Style, p theme.Palette) string {
	switch glyph {
	case LaneCommit:
		return renderCommitLaneCell(lane, commitLane, lineIdx, st)
	case LanePass:
		return st.Render("│")
	case LaneMergeRight:
		return renderMergeCell(lineIdx, st)
	case LaneBranchLeft:
		return renderBranchCell(lineIdx, st)
	case LaneHoriz:
		return renderHorizCell(lineIdx, st)
	default:
		return " "
	}
}

// renderCommitLaneCell renders the cell at the commit's own lane.
func renderCommitLaneCell(lane, commitLane, lineIdx int, st lipgloss.Style) string {
	if lane == commitLane && lineIdx == 1 {
		return st.Render(commitMarker)
	}
	return st.Render("│")
}

// renderMergeCell renders a merge-right (╮) on the header line, │ elsewhere.
func renderMergeCell(lineIdx int, st lipgloss.Style) string {
	if lineIdx == 1 {
		return st.Render("╮")
	}
	return st.Render("│")
}

// renderBranchCell renders a branch-left (╰) on the connector line, │ elsewhere.
func renderBranchCell(lineIdx int, st lipgloss.Style) string {
	if lineIdx == 4 {
		return st.Render("╰")
	}
	return st.Render("│")
}

// renderHorizCell renders a horizontal connector (─) on the header line, space elsewhere.
func renderHorizCell(lineIdx int, st lipgloss.Style) string {
	if lineIdx == 1 {
		return st.Render("─")
	}
	return " "
}

// =============================================================================
// Branch Tree Layout
// =============================================================================
//
// Offshoot branches are rendered side-by-side in rows (max 2 per row).
// The default branch is rendered as a wider, centered card at the bottom.
// A vertical trunk line connects all rows through merge lines.
//
// Layout (2 offshoots + primary):
//
//	╭────────────────╮ ╭────────────────╮
//	│ feat/a   1w ago│ │ feat/b   3d ago│
//	│  abc90  Fix X  │ │  def56  Fix Y  │
//	╰────────────────╯ ╰────────────────╯
//	      ╰──────────┬──────────╯
//	                 │
//	    ╭────────────┴───────────────╮
//	    │ ● main               2h ago│
//	    │   ghi90  Merge feature     │
//	    ╰────────────────────────────╯

// branchRowHeight is the number of terminal rows per visual row.
// Derived from: card(4) + merge/connector(1) + trunk/blank(1) = 6.
const branchRowHeight = 6

// Layout constants for branch tree rendering.
const (
	branchCardGap      = 3  // gap between side-by-side cards (includes trunk)
	minBranchCardWidth = 24 // minimum usable card width
	primaryWidthPct    = 70 // primary card width as percentage of panel
	offshootWidthPct   = 55 // single-column offshoot width as percentage
)

// trunkAlignedMargin returns the left margin that centers contentWidth within
// panelWidth such that margin + contentWidth/2 == panelWidth/2. This avoids
// the off-by-one that (panelWidth - contentWidth) / 2 produces when the two
// widths have different parities.
func trunkAlignedMargin(panelWidth, contentWidth int) int {
	return max(panelWidth/2-contentWidth/2, 0)
}

// branchCols returns how many offshoot cards fit side-by-side.
// Derived from: cols * minBranchCardWidth + (cols-1) * branchCardGap <= width.
func branchCols(width int) int {
	return max((width+branchCardGap)/(minBranchCardWidth+branchCardGap), 1)
}

// offshootCardWidth returns the width of each offshoot card.
func offshootCardWidth(width, cols int) int {
	if cols <= 1 {
		return clampInt(width*offshootWidthPct/100, minBranchCardWidth, width)
	}
	totalGap := (cols - 1) * branchCardGap
	return max((width-totalGap)/cols, minBranchCardWidth)
}

// primaryCardWidth returns the width of the primary branch card.
func primaryCardWidth(width int) int {
	return clampInt(width*primaryWidthPct/100, minBranchCardWidth, width)
}

// workingTreeState holds the working tree dirty/conflicts flags, used by all
// cards for dot color and by expanded cards for action enablement.
type workingTreeState struct {
	dirty     bool
	conflicts bool
}

// branchExpansion holds the rendering state for an expanded branch card.
type branchExpansion struct {
	wt               workingTreeState
	defaultBranch    string      // name of the repository default branch
	selectedActionID int         // resolved action ID (not index)
	hasStagedFiles   bool        // uncommitted tab has staged files
	commitInput      bool        // commit message input is active
	commitPhase      commitPhase // idle / in-progress / succeeded
	commitMsg        string      // current commit message text
	commitCursor     int         // cursor position in commit message
	commitSpinner    int         // spinner frame index
	cursorVisible    bool        // blink phase: true = show cursor
	deleteConfirm    bool        // delete confirmation prompt is active
	deleteConfirmYes bool        // true = (y)es highlighted, false = (n)o highlighted
}

// switchEnabled reports whether the Switch action is usable.
func (e *branchExpansion) switchEnabled(isHead bool) bool {
	return !isHead && !e.wt.dirty && !e.wt.conflicts
}

// deleteEnabled reports whether the Delete action is usable.
// Delete is allowed regardless of dirty state, but never for the HEAD or
// default branch.
func (e *branchExpansion) deleteEnabled(name string, isHead bool) bool {
	return !isHead && name != e.defaultBranch
}

// deleteVisible reports whether the Delete badge should be shown at all.
// The default branch never shows a delete option.
func (e *branchExpansion) deleteVisible(name string) bool {
	return name != e.defaultBranch
}

// switchBlockedReason returns a human-readable reason why Switch is blocked,
// or empty if it is not blocked.
func (e *branchExpansion) switchBlockedReason(isHead bool) string {
	switch {
	case isHead:
		return ""
	case e.wt.conflicts:
		return "resolve conflicts first"
	case e.wt.dirty:
		return "commit or stash changes first"
	default:
		return ""
	}
}

// commitBlockedReason returns a human-readable reason why Commit is blocked,
// or empty if it is not blocked.
func (e *branchExpansion) commitBlockedReason(isHead bool) string {
	if !isHead {
		return ""
	}
	if !e.hasStagedFiles {
		return "stage files in uncommitted tab"
	}
	return ""
}

// actionBlockedReason returns the reason the currently selected action is
// blocked, or empty if the selected action is not blocked.
func (e *branchExpansion) actionBlockedReason(isHead bool) string {
	switch e.selectedActionID {
	case branchActionCommit:
		return e.commitBlockedReason(isHead)
	case branchActionSwitch:
		return e.switchBlockedReason(isHead)
	default:
		return ""
	}
}

// wrapText performs word-boundary wrapping of text into lines of at most
// maxWidth visible columns. Words that exceed maxWidth are placed on their
// own line (padContent will truncate them).
func wrapText(text string, maxWidth int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > maxWidth {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	return append(lines, line)
}

// buildBranchCard returns the rendered lines for a single branch card.
// Normal cards produce 4 lines; expanded cards produce 6–7 depending on
// whether a blocked-reason line is needed.
func buildBranchCard(b BranchNode, selected bool, innerWidth int, p theme.Palette,
	trunkInnerTop, trunkInnerBot int, hasTrunkTop, hasTrunkBot bool,
	expanded bool, exp *branchExpansion, wt workingTreeState, diff diffOverlay) []string {

	borderColor := p.Border
	switch {
	case selected:
		borderColor = p.Rosewater
	case diff.active:
		borderColor = diffColorForIdx(diff.idx, p)
	}
	bSt := lipgloss.NewStyle().Foreground(borderColor)

	header := padContent(buildBranchHeaderLine(b, innerWidth, p, wt), innerWidth)
	subject := padContent(buildBranchSubjectLine(b, innerWidth, p), innerWidth)

	// Arrow icon on the right edge of the subject line: ▸ collapsed, ▾ expanded.
	arrowSt := lipgloss.NewStyle().Foreground(p.Muted)
	arrow := "▸"
	if expanded {
		arrow = "▾"
	}
	if innerWidth >= 3 {
		subject = truncateStyled(subject, innerWidth-2) + " " + arrowSt.Render(arrow)
	}

	var icon string
	var iconSt lipgloss.Style
	if diff.active {
		icon = diffSelectionIcons[diff.idx%len(diffSelectionIcons)]
		iconSt = lipgloss.NewStyle().Foreground(diffColorForIdx(diff.idx, p))
	}

	topBorder := buildCardBorder("╭", "╮", innerWidth, bSt, trunkInnerTop, hasTrunkTop, icon, iconSt)

	lines := []string{
		topBorder,
		bSt.Render("│") + header + bSt.Render("│"),
		bSt.Render("│") + subject + bSt.Render("│"),
	}

	if expanded && exp != nil {
		mutedSt := lipgloss.NewStyle().Foreground(p.Muted)
		divider := bSt.Render("├") + mutedSt.Render(strings.Repeat("╌", innerWidth)) + bSt.Render("┤")
		lines = append(lines, divider)
		for _, cl := range buildExpandedLines(b, exp, innerWidth, p) {
			lines = append(lines, bSt.Render("│")+padContent(cl, innerWidth)+bSt.Render("│"))
		}

		// Inline prompts: delete confirmation, commit input, or blocked reason.
		if exp.deleteConfirm {
			confirmLine := bSt.Render("│") + padContent(buildDeleteConfirmLine(exp.deleteConfirmYes, innerWidth, p), innerWidth) + bSt.Render("│")
			lines = append(lines, confirmLine)
		} else if exp.commitInput && b.IsHead {
			inputLine := bSt.Render("│") + padContent(buildCommitInputLine(exp, innerWidth, p), innerWidth) + bSt.Render("│")
			lines = append(lines, inputLine)
		} else if reason := exp.actionBlockedReason(b.IsHead); reason != "" {
			reasonSt := lipgloss.NewStyle().Foreground(p.Warning)
			// Word-wrap the reason text to fit the card (1-col indent).
			for _, wl := range wrapText(reason, innerWidth-1) {
				rl := bSt.Render("│") + padContent(" "+reasonSt.Render(wl), innerWidth) + bSt.Render("│")
				lines = append(lines, rl)
			}
		}
	}

	var botBorder string
	if hasTrunkBot {
		botBorder = buildCardBorder("╰", "╯", innerWidth, bSt, trunkInnerBot, true, "", lipgloss.Style{})
	} else {
		botBorder = bSt.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	}
	lines = append(lines, botBorder)

	return lines
}

// visibleActions returns the ordered action IDs for a branch card.
// HEAD shows [Commit]; default shows [Switch]; others show [Switch, Delete].
func visibleActions(b BranchNode, defaultBranch string) []int {
	if b.IsHead {
		return []int{branchActionCommit}
	}
	if defaultBranch == b.Name {
		return []int{branchActionSwitch}
	}
	return []int{branchActionSwitch, branchActionDelete}
}

// badgePlacement describes the position of one action badge in the
// expanded card's flow layout.
type badgePlacement struct {
	line int // line offset relative to first badge line
	x    int // column offset within card content
	w    int // visual width of badge label
}

// computeBadgeLayout flows badges left-to-right within width, wrapping to
// new lines when the next badge would exceed the available space.
// firstLineStart is the column where badge placement begins on line 0
// (typically the stats text width). Wrapped lines start at column 1.
// Returns per-badge placements and the number of lines occupied (≥1 when
// actions is non-empty, 0 when empty).
func computeBadgeLayout(actions []int, width, firstLineStart int) ([]badgePlacement, int) {
	if len(actions) == 0 {
		return nil, 0
	}
	placements := make([]badgePlacement, 0, len(actions))
	line := 0
	x := firstLineStart
	for _, actionID := range actions {
		gap := actionBadgeGap(actionID)
		bw := len(actionBadgeLabel(actionID))
		if x > 1 && x+gap+bw > width {
			line++
			x = 1
		}
		// No leading gap at line start — badges sit flush left.
		if x <= 1 {
			gap = 0
		}
		x += gap
		placements = append(placements, badgePlacement{line: line, x: x, w: bw})
		x += bw
	}
	return placements, line + 1
}

// buildExpandedLines renders the expanded card content lines. Badges are
// placed on the stats line when they all fit; otherwise they move to a
// dedicated line below stats, wrapping within that line only if needed.
func buildExpandedLines(b BranchNode, exp *branchExpansion, innerWidth int, p theme.Palette) []string {
	statsLine := buildExpStatsLine(b, exp, p)
	statsWidth := lipgloss.Width(statsLine)

	actions := visibleActions(b, exp.defaultBranch)
	if len(actions) == 0 {
		return []string{statsLine}
	}

	// Try placing all badges on the stats line.
	placements, badgeLineCount := computeBadgeLayout(actions, innerWidth, statsWidth)
	badgesInline := badgeLineCount == 1

	if !badgesInline {
		// Badges don't all fit on the stats line — move them to their
		// own row and allow wrapping within that dedicated row.
		const badgeIndent = 1
		placements, badgeLineCount = computeBadgeLayout(actions, innerWidth, badgeIndent)
	}

	lines := make([]string, 0, 1+badgeLineCount)

	if badgesInline {
		// Single output line: stats + badges.
		lines = append(lines, renderBadgeLine(statsLine, statsWidth, placements, 0, actions, b, exp, p))
	} else {
		// Stats on their own line, dashed divider, then badges.
		mutedSt := lipgloss.NewStyle().Foreground(p.Muted)
		divider := mutedSt.Render(strings.Repeat("-", innerWidth))
		lines = append(lines, statsLine)
		lines = append(lines, divider)
		for li := range badgeLineCount {
			lines = append(lines, renderBadgeLine("", 0, placements, li, actions, b, exp, p))
		}
		lines = append(lines, divider)
	}

	debugLogExpandedLines(lines, innerWidth)

	return lines
}

// renderBadgeLine builds a single line containing badge renders at their
// computed placements. prefix is prepended (e.g. statsLine for inline mode).
func renderBadgeLine(prefix string, prefixWidth int, placements []badgePlacement, lineIdx int, actions []int, b BranchNode, exp *branchExpansion, p theme.Palette) string {
	var buf strings.Builder
	col := 0
	if prefix != "" {
		buf.WriteString(prefix)
		col = prefixWidth
	}
	for pi, bp := range placements {
		if bp.line != lineIdx {
			continue
		}
		if bp.x > col {
			buf.WriteString(strings.Repeat(" ", bp.x-col))
			col = bp.x
		}
		buf.WriteString(renderExpActionBadge(actions[pi], b, exp, p))
		col += bp.w
	}
	return buf.String()
}

// buildExpStatsLine renders the stats portion of the expanded content:
// commit count, optional behind count, and optional dirty/conflicts status.
func buildExpStatsLine(b BranchNode, exp *branchExpansion, p theme.Palette) string {
	countStyle := lipgloss.NewStyle().Foreground(p.Foreground)
	count := itoa(b.CommitCount)
	if b.CommitCountCapped {
		count += "+"
	}
	text := " " + countStyle.Render(count+" commits")

	if b.BehindCount > 0 {
		behind := itoa(b.BehindCount)
		if b.BehindCountCapped {
			behind += "+"
		}
		behindStyle := lipgloss.NewStyle().Foreground(p.Warning)
		text += "  " + behindStyle.Render(behind+" behind")
	}

	if b.IsHead {
		if exp.wt.conflicts {
			st := lipgloss.NewStyle().Foreground(p.Error)
			text += "  " + st.Render("[conflicts]")
		} else if exp.wt.dirty {
			st := lipgloss.NewStyle().Foreground(p.Warning)
			text += "  " + st.Render("[dirty]")
		}
	}

	return text
}

// renderExpActionBadge renders a single action badge with appropriate
// styling based on the action type and current expansion state.
func renderExpActionBadge(actionID int, b BranchNode, exp *branchExpansion, p theme.Palette) string {
	highlighted := exp.selectedActionID == actionID
	switch actionID {
	case branchActionCommit:
		return renderActionBadge("Commit", p.Secondary, exp.hasStagedFiles,
			highlighted, highlighted && exp.commitInput, p)
	case branchActionSwitch:
		return renderActionBadge("Switch", p.Success, exp.switchEnabled(b.IsHead),
			highlighted, false, p)
	case branchActionDelete:
		return renderActionBadge("Delete", p.Error, exp.deleteEnabled(b.Name, b.IsHead),
			highlighted, highlighted && exp.deleteConfirm, p)
	default:
		return ""
	}
}

// actionBadgeLabel returns the bracket-wrapped label for an action ID.
func actionBadgeLabel(actionID int) string {
	switch actionID {
	case branchActionCommit:
		return "[Commit]"
	case branchActionSwitch:
		return "[Switch]"
	case branchActionDelete:
		return "[Delete]"
	default:
		return ""
	}
}

// actionBadgeGap returns the column gap before an action badge.
func actionBadgeGap(actionID int) int {
	if actionID == branchActionDelete {
		return 1
	}
	return 2
}

// actionLinePrefixWidth computes the column width of the non-badge prefix
// on the expanded content line: " N commits" + optional behind + optional status.
func actionLinePrefixWidth(b BranchNode, dirty, conflicts bool) int {
	count := itoa(b.CommitCount)
	if b.CommitCountCapped {
		count += "+"
	}
	w := 1 + len(count+" commits")
	if b.BehindCount > 0 {
		behind := itoa(b.BehindCount)
		if b.BehindCountCapped {
			behind += "+"
		}
		w += 2 + len(behind+" behind")
	}
	if b.IsHead {
		if conflicts {
			w += 2 + len("[conflicts]")
		} else if dirty {
			w += 2 + len("[dirty]")
		}
	}
	return w
}

// cardActionLine is the card line index where action badges start.
// Derived from: top(0) + header(1) + subject(2) + divider(3) → actions(4).
const cardActionLine = 4

// cardHitKind identifies the type of clickable region in an expanded card.
type cardHitKind int

const (
	hitActionBadge cardHitKind = iota
	hitDeleteYes
	hitDeleteNo
	hitCommitInput
)

// cardHitRegion describes a single clickable area within an expanded card.
type cardHitRegion struct {
	Y    int         // card line index (0-indexed from top border)
	XMin int         // left column (inclusive), relative to content start (after left │)
	XMax int         // right column (exclusive)
	Kind cardHitKind
	Idx  int         // action index (hitActionBadge only)
}

// Delete confirm layout constants shared between rendering and click detection.
const (
	deleteConfirmPromptFull    = "Delete this branch?"
	deleteConfirmPromptCompact = "Delete?"
	deleteConfirmYesLabel      = "(y)es"
	deleteConfirmNoLabel       = "(n)o"
	deleteConfirmGap           = 2
	deleteConfirmGapCompact    = 1
)

// deleteConfirmLayout holds the adaptive positions for the confirm line.
type deleteConfirmLayout struct {
	prompt   string
	gap      int
	yesStart int
	noStart  int
}

// computeDeleteConfirmLayout picks the prompt and gap that fit the card width,
// returning the X offsets for (y)es and (n)o.
func computeDeleteConfirmLayout(width int) deleteConfirmLayout {
	// Try full layout first: " Delete this branch?  (y)es  (n)o"
	full := tryDeleteLayout(deleteConfirmPromptFull, deleteConfirmGap, width)
	if full.yesStart > 0 {
		return full
	}
	// Compact layout: " Delete? (y)es (n)o"
	return tryDeleteLayout(deleteConfirmPromptCompact, deleteConfirmGapCompact, width)
}

// tryDeleteLayout checks if the prompt+gap+labels fit within width.
// Returns a layout with yesStart=0 if it doesn't fit.
func tryDeleteLayout(prompt string, gap, width int) deleteConfirmLayout {
	yesStart := 1 + len(prompt) + gap
	noStart := yesStart + len(deleteConfirmYesLabel) + gap
	totalWidth := noStart + len(deleteConfirmNoLabel)
	if totalWidth > width {
		return deleteConfirmLayout{}
	}
	return deleteConfirmLayout{
		prompt:   prompt,
		gap:      gap,
		yesStart: yesStart,
		noStart:  noStart,
	}
}

// commitInputLabelWidth is the visual width of the " Message: " label.
const commitInputLabelWidth = 10

// renderActionBadge renders a single [Label] badge with appropriate styling.
// accent is the semantic color for the action (e.g. Success for Switch, Error
// for Delete). highlighted means the cursor is on this badge; active means the
// action is engaged (e.g. commit input or delete confirm visible). Bold only
// applies when active. Disabled+highlighted shows muted with selection
// background so the user can see the cursor position.
func renderActionBadge(label string, accent lipgloss.Color, enabled, highlighted, active bool, p theme.Palette) string {
	st := lipgloss.NewStyle().Foreground(p.Muted)
	switch {
	case enabled && highlighted:
		st = lipgloss.NewStyle().Foreground(accent).Bold(active)
	case !enabled && highlighted:
		st = lipgloss.NewStyle().Foreground(p.Muted).Background(p.Selection)
	}
	return st.Render("[" + label + "]")
}

// spinnerFrames are the braille spinner glyphs used during commit progress.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// buildCommitInputLine renders the inline commit input row. The content
// depends on the commit phase:
//
// buildDeleteConfirmLine renders the inline delete confirmation prompt with
// toggleable (y)es / (n)o options. The prompt adapts to the card width:
// full ("Delete this branch?") when space allows, compact ("Delete?") otherwise.
//
//	wide:   " Delete this branch?  (y)es  (n)o"
//	narrow: " Delete? (y)es (n)o"
func buildDeleteConfirmLine(yesSelected bool, width int, p theme.Palette) string {
	lay := computeDeleteConfirmLayout(width)
	promptSt := lipgloss.NewStyle().Foreground(p.Muted)

	yesAccent := lipgloss.NewStyle().Foreground(p.Success).Bold(true)
	noAccent := lipgloss.NewStyle().Foreground(p.Error).Bold(true)
	muted := lipgloss.NewStyle().Foreground(p.Muted)

	var yes, no string
	if yesSelected {
		yes = yesAccent.Render("(y)es")
		no = muted.Render("(n)o")
	} else {
		yes = muted.Render("(y)es")
		no = noAccent.Render("(n)o")
	}

	gap := strings.Repeat(" ", lay.gap)
	return " " + promptSt.Render(lay.prompt) + gap + yes + gap + no
}

//	idle:        " Message: ▏some commit text"
//	in-progress: " ⠹ Committing..."
//	succeeded:   " ✓ Committed"
//	failed:      " ✕ error reason"
func buildCommitInputLine(exp *branchExpansion, width int, p theme.Palette) string {
	switch exp.commitPhase {
	case commitInProgress:
		spinSt := lipgloss.NewStyle().Foreground(p.Primary)
		textSt := lipgloss.NewStyle().Foreground(p.Muted)
		frame := spinnerFrames[exp.commitSpinner%len(spinnerFrames)]
		return " " + spinSt.Render(frame) + " " + textSt.Render("Committing...")

	case commitSucceeded:
		iconSt := lipgloss.NewStyle().Foreground(p.Success)
		textSt := lipgloss.NewStyle().Foreground(p.Success)
		return " " + iconSt.Render("✓") + " " + textSt.Render("Committed")

	case commitFailed:
		iconSt := lipgloss.NewStyle().Foreground(p.Error)
		textSt := lipgloss.NewStyle().Foreground(p.Error)
		// Truncate error to fit within the card.
		const prefix = 4 // " ✕ " = icon + spaces
		errMsg := truncateSubject(exp.commitMsg, max(width-prefix, 0))
		return " " + iconSt.Render("✕") + " " + textSt.Render(errMsg)

	default:
		return buildCommitIdleLine(exp, width, p)
	}
}

// buildCommitIdleLine renders the text input row with a blinking cursor.
func buildCommitIdleLine(exp *branchExpansion, width int, p theme.Palette) string {
	labelSt := lipgloss.NewStyle().Foreground(p.Muted)
	textSt := lipgloss.NewStyle().Foreground(p.Foreground)
	cursorSt := lipgloss.NewStyle().Foreground(p.Primary)

	label := labelSt.Render(" Message: ")
	labelWidth := lipgloss.Width(label)

	runes := []rune(exp.commitMsg)
	cursor := clampInt(exp.commitCursor, 0, len(runes))

	before := string(runes[:cursor])
	after := ""
	if cursor < len(runes) {
		after = string(runes[cursor:])
	}

	var cursorGlyph string
	if exp.cursorVisible {
		cursorGlyph = cursorSt.Render("▏")
	}

	content := label + textSt.Render(before) + cursorGlyph + textSt.Render(after)

	if vis := lipgloss.Width(content); vis > width {
		avail := max(width-labelWidth-1, 0)
		if len([]rune(before)) > avail {
			trimmed := string([]rune(before)[len([]rune(before))-avail:])
			content = label + textSt.Render(trimmed) + cursorGlyph + textSt.Render(after)
		}
		content = truncateStyled(content, width)
	}

	return content
}

// composeOffshootRow composites pre-rendered card line slices into a row
// with merge lines and connectors. cardSlices contains one entry per card
// in the row, already rendered by buildBranchCard (possibly cached).
// hasTrunkAbove draws a trunk │ in the card gap when a depth-0 row above
// produced a trunk line. mergeTarget is the column position the merge line
// connects to: trunkPos for depth-0 rows, parent's card center for depth > 0.
// mergeAboveCols lists column positions with active merge connectors from
// sibling rows above that must pass through this row's card area.
func composeOffshootRow(cardSlices [][]string, colIndices []int, cardWidth, gridCols, width int, th *theme.Theme, hasTrunkAbove bool, mergeTarget int, mergeAboveCols []int) []string {
	p := th.Palette
	cols := len(cardSlices)
	trunkPos := width / 2
	trunkSt := lipgloss.NewStyle().Foreground(p.Border)

	// Compute card positions using fixed grid so columns align across rows.
	totalContent := gridCols*cardWidth + max(gridCols-1, 0)*branchCardGap
	leftMargin := trunkAlignedMargin(width, totalContent)

	cardLefts := make([]int, cols)
	cardCenters := make([]int, cols)
	for i, ci := range colIndices {
		cardLefts[i] = leftMargin + ci*(cardWidth+branchCardGap)
		cardCenters[i] = cardLefts[i] + cardWidth/2
	}

	// Merge card lines horizontally with trunk in gap.
	maxCardHeight := 0
	for _, cs := range cardSlices {
		maxCardHeight = max(maxCardHeight, len(cs))
	}

	lines := make([]string, 0, maxCardHeight+2)
	for lineIdx := range maxCardHeight {
		var buf strings.Builder
		col := 0
		for i := range cols {
			target := cardLefts[i]
			for col < target {
				if (hasTrunkAbove && col == trunkPos) || slices.Contains(mergeAboveCols, col) {
					buf.WriteString(trunkSt.Render("│"))
				} else {
					buf.WriteByte(' ')
				}
				col++
			}
			if lineIdx < len(cardSlices[i]) {
				buf.WriteString(cardSlices[i][lineIdx])
			} else {
				// Shorter card — draw vertical connector at card center.
				centerOff := cardWidth / 2
				buf.WriteString(strings.Repeat(" ", centerOff))
				buf.WriteString(trunkSt.Render("│"))
				buf.WriteString(strings.Repeat(" ", max(cardWidth-centerOff-1, 0)))
			}
			col += cardWidth
		}
		// Fill remaining space, drawing trunk at trunkPos if needed.
		for col < width {
			if (hasTrunkAbove && col == trunkPos) || slices.Contains(mergeAboveCols, col) {
				buf.WriteString(trunkSt.Render("│"))
			} else {
				buf.WriteByte(' ')
			}
			col++
		}
		lines = append(lines, buf.String())
	}

	// For off-center single cards, draw a vertical connector from the
	// card's center down to the merge line so the tree stays connected.
	if cols == 1 && cardCenters[0] != mergeTarget {
		center := cardCenters[0]
		var connBuf strings.Builder
		for c := range width {
			if c == center || (hasTrunkAbove && c == trunkPos) || slices.Contains(mergeAboveCols, c) {
				connBuf.WriteString(trunkSt.Render("│"))
			} else {
				connBuf.WriteByte(' ')
			}
		}
		lines = append(lines, connBuf.String())
	}

	// Merge line: connects card center(s) to the merge target.
	// When a single card is centered on the target, a simple vertical
	// suffices; otherwise draw a horizontal merge connector.
	if cols == 1 && cardCenters[0] == mergeTarget {
		lines = append(lines, buildConnectorLine(mergeTarget, trunkPos, hasTrunkAbove, mergeAboveCols, width, trunkSt))
	} else {
		lines = append(lines, renderMergeLine(cardCenters, mergeTarget, mergeAboveCols, width, th))
	}

	// Vertical connector below the merge line.
	lines = append(lines, buildConnectorLine(mergeTarget, trunkPos, hasTrunkAbove, mergeAboveCols, width, trunkSt))

	return lines
}

// buildConnectorLine draws a vertical connector at target, optionally also
// drawing the trunk at trunkPos when hasTrunkAbove is true. extraCols are
// additional column positions that need pass-through connectors. The line
// is padded to exactly width columns.
func buildConnectorLine(target, trunkPos int, hasTrunkAbove bool, extraCols []int, width int, trunkSt lipgloss.Style) string {
	var buf strings.Builder
	for c := range width {
		if c == target || (hasTrunkAbove && c == trunkPos) || slices.Contains(extraCols, c) {
			buf.WriteString(trunkSt.Render("│"))
		} else {
			buf.WriteByte(' ')
		}
	}
	return buf.String()
}

// composePrimaryRow composes a pre-rendered primary branch card into a row
// with centering and padding. cardLines are already rendered by buildBranchCard
// (possibly cached).
func composePrimaryRow(cardLines []string, width int) []string {
	cardWidth := primaryCardWidth(width)
	leftPad := trunkAlignedMargin(width, cardWidth)
	blank := strings.Repeat(" ", max(width, 0))

	vis := leftPad + cardWidth
	pad := strings.Repeat(" ", leftPad)
	lines := make([]string, 0, len(cardLines)+2)
	for _, cl := range cardLines {
		lines = append(lines, padRight(pad+cl, vis, width))
	}
	// Pad to at least branchRowHeight.
	for len(lines) < branchRowHeight {
		lines = append(lines, blank)
	}

	return lines
}

// renderMergeLine draws a horizontal connector from the leftmost card center
// to the rightmost card center, with ┬ at the trunk position.
// mergeAboveCols are column positions with active connectors from sibling
// rows above; they pass through the merge line as vertical connectors.
func renderMergeLine(centers []int, mergeTarget int, mergeAboveCols []int, width int, th *theme.Theme) string {
	st := lipgloss.NewStyle().Foreground(th.Palette.Border)

	centerSet := make(map[int]struct{}, len(centers))
	for _, c := range centers {
		centerSet[c] = struct{}{}
	}

	// Span must include all card centers AND the merge target.
	leftmost := mergeTarget
	rightmost := mergeTarget
	for _, c := range centers {
		leftmost = min(leftmost, c)
		rightmost = max(rightmost, c)
	}

	// Build three sections: pre-span, span, post-span.
	var buf strings.Builder

	// Pre-span: spaces with merge-above pass-through.
	for col := range leftmost {
		if slices.Contains(mergeAboveCols, col) {
			buf.WriteString(st.Render("│"))
		} else {
			buf.WriteByte(' ')
		}
	}

	// Span: merge bar with merge-above intersections.
	var raw strings.Builder
	for col := leftmost; col <= rightmost; col++ {
		_, isCenter := centerSet[col]
		isMergeTarget := col == mergeTarget
		isMergeAbove := slices.Contains(mergeAboveCols, col)
		up := isCenter || isMergeAbove
		down := isMergeTarget || isMergeAbove
		left := col > leftmost
		right := col < rightmost
		raw.WriteString(mergeConnector(up, down, left, right))
	}
	buf.WriteString(st.Render(raw.String()))

	// Post-span: spaces with merge-above pass-through.
	for col := rightmost + 1; col < width; col++ {
		if slices.Contains(mergeAboveCols, col) {
			buf.WriteString(st.Render("│"))
		} else {
			buf.WriteByte(' ')
		}
	}

	return buf.String()
}

// mergeConnector returns the box-drawing character for a merge line position.
func mergeConnector(up, down, left, right bool) string {
	switch {
	case up && down:
		switch {
		case left && right:
			return "┼"
		case right:
			return "├"
		case left:
			return "┤"
		default:
			return "│"
		}
	case up && !left && !right:
		return "│"
	case up && left && right:
		return "┴"
	case up && right:
		return "╰"
	case up && left:
		return "╯"
	case down && left && right:
		return "┬"
	case down && right:
		return "╭"
	case down && left:
		return "╮"
	default:
		return "─"
	}
}

// buildCardBorder constructs a horizontal border with an optional trunk connector.
// For top borders (left="╭") inserts ┴; for bottom borders (left="╰") inserts ┬.
// When icon is non-empty and this is a top border, the icon replaces the first
// dash after the corner, rendered with iconSt for a distinct color.
func buildCardBorder(left, right string, innerWidth int, bSt lipgloss.Style, trunkPos int, hasTrunk bool, icon string, iconSt lipgloss.Style) string {
	if icon != "" && left == "╭" && innerWidth > 0 {
		return buildCardBorderWithIcon(left, right, innerWidth, bSt, trunkPos, hasTrunk, icon, iconSt)
	}
	if !hasTrunk || trunkPos < 0 || trunkPos >= innerWidth {
		return bSt.Render(left + strings.Repeat("─", innerWidth) + right)
	}
	connector := "┬"
	if left == "╭" {
		connector = "┴"
	}
	return bSt.Render(left + strings.Repeat("─", trunkPos) + connector + strings.Repeat("─", innerWidth-trunkPos-1) + right)
}

// buildCardBorderWithIcon renders a top border with a colored icon glyph
// replacing the left corner character (╭). The inner dashes remain at full
// width so the card dimensions are unchanged.
func buildCardBorderWithIcon(left, right string, innerWidth int, bSt lipgloss.Style, trunkPos int, hasTrunk bool, icon string, iconSt lipgloss.Style) string {
	connector := "┴"
	if !hasTrunk || trunkPos < 0 || trunkPos >= innerWidth {
		return iconSt.Render(icon) + bSt.Render(strings.Repeat("─", innerWidth)+right)
	}
	return iconSt.Render(icon) +
		bSt.Render(strings.Repeat("─", trunkPos)+connector+strings.Repeat("─", innerWidth-trunkPos-1)+right)
}

// renderCommitCard produces a 4-line card for a commit in the DAG
// trunk-and-offshoots layout. The card has trunk connectors in its top/bottom
// borders matching the branch tree card style. Used for both trunk and
// offshoot commit cards.
//
// Layout (4 lines):
//
//	╭────────────┴─────────────╮
//	│ ● abc1234  +12 -3  2h ago│
//	│   Fix login bug  John Doe│
//	╰────────────┬─────────────╯
func renderCommitCard(n TreeNode, selected bool, innerWidth int, p theme.Palette,
	trunkInnerTop, trunkInnerBot int, hasTrunkTop, hasTrunkBot bool,
	diff diffOverlay) []string {

	borderColor := p.Border
	switch {
	case selected:
		borderColor = p.Rosewater
	case diff.active:
		borderColor = diffColorForIdx(diff.idx, p)
	}
	bSt := lipgloss.NewStyle().Foreground(borderColor)

	header := padContent(buildHeaderLine(n, innerWidth, p), innerWidth)
	subject := padContent(buildSubjectLine(n, innerWidth, p), innerWidth)

	var icon string
	var iconSt lipgloss.Style
	if diff.active {
		icon = diffSelectionIcons[diff.idx%len(diffSelectionIcons)]
		iconSt = lipgloss.NewStyle().Foreground(diffColorForIdx(diff.idx, p))
	}

	topBorder := buildCardBorder("╭", "╮", innerWidth, bSt, trunkInnerTop, hasTrunkTop, icon, iconSt)

	var botBorder string
	if hasTrunkBot {
		botBorder = buildCardBorder("╰", "╯", innerWidth, bSt, trunkInnerBot, true, "", lipgloss.Style{})
	} else {
		botBorder = bSt.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	}

	return []string{
		topBorder,
		bSt.Render("│") + header + bSt.Render("│"),
		bSt.Render("│") + subject + bSt.Render("│"),
		botBorder,
	}
}

// padContent pads styled content to exactly width visible columns.
func padContent(content string, width int) string {
	vis := lipgloss.Width(content)
	if vis < width {
		return content + strings.Repeat(" ", width-vis)
	}
	if vis > width {
		return truncateStyled(content, width)
	}
	return content
}

// clampInt constrains v to the range [lo, hi].
func clampInt(v, lo, hi int) int {
	return max(min(v, hi), lo)
}

// layoutLine arranges a left-aligned and right-aligned styled segment
// within a fixed-width line. When both segments cannot fit with the
// minimum gap, the left segment is truncated. The right segment is
// never truncated — it represents fixed metadata (time, author, stats).
func layoutLine(left, right string, width int) string {
	rw := lipgloss.Width(right)
	maxLeft := width - rw - minLayoutGap
	left = truncateStyled(left, max(maxLeft, 0))
	lw := lipgloss.Width(left)
	gap := max(width-lw-rw, minLayoutGap)
	return left + strings.Repeat(" ", gap) + right
}

// buildBranchHeaderLine assembles: ● branchName          2h ago
// The dot color reflects working tree state for the HEAD branch:
// conflicts → red, dirty → yellow, clean → primary.
func buildBranchHeaderLine(b BranchNode, availWidth int, p theme.Palette, wt workingTreeState) string {
	markerStyle := lipgloss.NewStyle().Foreground(p.Muted)
	if b.IsHead {
		switch {
		case wt.conflicts:
			markerStyle = lipgloss.NewStyle().Foreground(p.Error).Bold(true)
		case wt.dirty:
			markerStyle = lipgloss.NewStyle().Foreground(p.Warning).Bold(true)
		default:
			markerStyle = lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
		}
	}
	marker := markerStyle.Render(commitMarker)

	nameStyle := lipgloss.NewStyle().Foreground(p.Foreground).Bold(true)
	if b.IsHead {
		nameStyle = nameStyle.Foreground(p.Primary)
	}
	name := nameStyle.Render(b.Name)

	timeStyle := lipgloss.NewStyle().Foreground(p.Muted)
	ts := timeStyle.Render(relativeTime(b.AuthorTime))

	left := marker + " " + name
	return layoutLine(left, ts, availWidth)
}

// buildBranchSubjectLine assembles: (indent) shortHash  subject
func buildBranchSubjectLine(b BranchNode, availWidth int, p theme.Palette) string {
	const indent = 2
	hashStyle := lipgloss.NewStyle().Foreground(p.Muted)
	hash := hashStyle.Render(b.ShortHash)
	hashWidth := lipgloss.Width(hash)

	subjectStyle := lipgloss.NewStyle().Foreground(p.Foreground)
	subjectMax := max(availWidth-indent-hashWidth-2, 0)
	subject := truncateSubject(b.Subject, subjectMax)

	return strings.Repeat(" ", indent) + hash + "  " + subjectStyle.Render(subject)
}

// buildHeaderLine assembles the commit card header:
//
//	● abc1234 (main)          2h ago
//
// Left: marker + hash + optional branch tag.
// Right: relative time.
// Matches the branch card header layout (identifier + time).
func buildHeaderLine(n TreeNode, availWidth int, p theme.Palette) string {
	marker := commitMarkerStyle(n, p).Render(commitMarker)
	hash := lipgloss.NewStyle().Foreground(p.Muted).Render(n.ShortHash)

	left := marker + " " + hash
	if n.Branch != "" {
		branchStyle := lipgloss.NewStyle().Foreground(p.Secondary).Bold(true)
		left += " " + branchStyle.Render("("+n.Branch+")")
	}

	ts := lipgloss.NewStyle().Foreground(p.Muted).Render(relativeTime(n.AuthorTime))

	return layoutLine(left, ts, availWidth)
}

// buildSubjectLine renders the commit card subject line:
//
//	  Fix login bug          +12 -3  John Doe
//
// Left: indented subject text (truncates to fit).
// Right: diff stats + author name (grouped, never truncated).
func buildSubjectLine(n TreeNode, availWidth int, p theme.Palette) string {
	const indent = 2
	subjectStyle := lipgloss.NewStyle().Foreground(p.Foreground)

	stats := formatStats(n.Additions, n.Deletions, &p)
	author := lipgloss.NewStyle().Foreground(p.Muted).Render(n.Author)
	right := stats + "  " + author

	// Compute max subject width from remaining space after right side + gap.
	rightWidth := lipgloss.Width(right)
	subjectMax := max(availWidth-indent-rightWidth-minLayoutGap, 0)
	subject := truncateSubject(n.Subject, subjectMax)

	left := strings.Repeat(" ", indent) + subjectStyle.Render(subject)

	return layoutLine(left, right, availWidth)
}

// commitMarkerStyle returns the style for the commit marker glyph.
// Branch-head commits use Primary; merge commits use Secondary; others use Muted.
func commitMarkerStyle(n TreeNode, p theme.Palette) lipgloss.Style {
	switch {
	case n.Branch != "":
		return lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
	case n.IsMerge:
		return lipgloss.NewStyle().Foreground(p.Secondary)
	default:
		return lipgloss.NewStyle().Foreground(p.Muted)
	}
}

// formatStats renders the addition/deletion counts with semantic colors.
// Format: "+N -M" where N is green (Success) and M is red (Error).
func formatStats(additions, deletions int, p *theme.Palette) string {
	addStyle := lipgloss.NewStyle().Foreground(p.Success)
	delStyle := lipgloss.NewStyle().Foreground(p.Error)

	return addStyle.Render(fmt.Sprintf("+%d", additions)) +
		" " +
		delStyle.Render(fmt.Sprintf("-%d", deletions))
}

// truncateSubject truncates a subject string to maxWidth, appending an
// ellipsis if the original exceeds the limit.
func truncateSubject(subject string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}

	runes := []rune(subject)
	ellipsisLen := len([]rune(ellipsis))

	if len(runes) <= maxWidth {
		return subject
	}

	if maxWidth <= ellipsisLen {
		return string(runes[:maxWidth])
	}

	return string(runes[:maxWidth-ellipsisLen]) + ellipsis
}

// padRight pads or truncates a styled line to exactly width visible columns.
// vis is the caller-computed visible column count, avoiding lipgloss.Width.
func padRight(line string, vis, width int) string {
	switch {
	case vis < width:
		return line + strings.Repeat(" ", width-vis)
	case vis > width && width > 0:
		return truncateStyled(line, width)
	default:
		return line
	}
}

// padLine pads or truncates a single styled line to exactly width visible columns.
func padLine(line string, width int) string {
	return padRight(line, lipgloss.Width(line), width)
}

// truncateStyled truncates a styled string to maxWidth visible columns.
// It walks rune-by-rune, skipping ANSI escape sequences, and cuts at the
// column boundary.
func truncateStyled(s string, maxWidth int) string {
	var (
		out   strings.Builder
		col   int
		inESC bool
	)
	for _, r := range s {
		if r == '\x1b' {
			inESC = true
			out.WriteRune(r)
			continue
		}
		if inESC {
			out.WriteRune(r)
			// ESC sequences end at a letter (A-Z, a-z).
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inESC = false
			}
			continue
		}
		if col >= maxWidth {
			break
		}
		out.WriteRune(r)
		col++
	}
	// Emit a reset if we truncated mid-style.
	if col >= maxWidth {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

// applyBounceShift displaces rendered lines by offset rows to produce an
// overscroll bounce effect. Positive offset shifts content up (bottom bounce),
// negative shifts down (top bounce).
func applyBounceShift(lines []string, offset, maxLines int, emptyLine string) []string {
	if offset == 0 || maxLines <= 0 {
		return lines
	}
	absOffset := offset
	if absOffset < 0 {
		absOffset = -absOffset
	}
	absOffset = min(absOffset, maxLines)

	result := make([]string, maxLines)

	if offset > 0 {
		// Bottom bounce: shift content up, pad bottom with empty lines.
		shift := min(absOffset, len(lines))
		copied := copy(result, lines[shift:])
		for i := copied; i < maxLines; i++ {
			result[i] = emptyLine
		}
	} else {
		// Top bounce: empty lines at top, content fills remaining space.
		for i := range absOffset {
			result[i] = emptyLine
		}
		remaining := maxLines - absOffset
		src := lines
		if len(src) > remaining {
			src = src[:remaining]
		}
		copy(result[absOffset:], src)
		for i := absOffset + len(src); i < maxLines; i++ {
			result[i] = emptyLine
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Toolbar rendering
// ---------------------------------------------------------------------------

// toolbarButtonDef maps a toolbar button ID to its icon and label.
type toolbarButtonDef struct {
	icon   string
	label  string
	accent func(theme.Palette) lipgloss.Color
}

// toolbarDefs maps button IDs to their display properties.
var toolbarDefs = map[int]toolbarButtonDef{
	toolbarCreate:     {icon: "＋", label: "Create", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	toolbarMerge:      {icon: "⇞", label: "Merge", accent: func(p theme.Palette) lipgloss.Color { return p.Peach }},
	toolbarBack:       {icon: "←", label: "Back", accent: func(p theme.Palette) lipgloss.Color { return p.Foreground }},
	toolbarDiff:       {icon: "⊟", label: "Diff", accent: func(p theme.Palette) lipgloss.Color { return p.HoverAccent }},
	toolbarDiffOk:     {icon: "✓", label: "Ok", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	toolbarDiffCancel: {icon: "✕", label: "Abort", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
	toolbarMergeOk:    {icon: "✓", label: "Ok", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	toolbarMergeAbort: {icon: "✕", label: "Abort", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
	toolbarPull:       {icon: "↓", label: "Pull", accent: func(p theme.Palette) lipgloss.Color { return p.Teal }},
	toolbarPush:       {icon: "↑", label: "Push", accent: func(p theme.Palette) lipgloss.Color { return p.Lavender }},
	toolbarReset:      {icon: "⟲", label: "Reset", accent: func(p theme.Palette) lipgloss.Color { return p.Warning }},
	toolbarResetHard:  {icon: "↩", label: "Hard", accent: func(p theme.Palette) lipgloss.Color { return p.Secondary }},
	toolbarResetMixed: {icon: "⇠", label: "Mixed", accent: func(p theme.Palette) lipgloss.Color { return p.Warning }},
	toolbarResetSoft:  {icon: "⇜", label: "Soft", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	toolbarResetAbort: {icon: "✕", label: "Abort", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
	toolbarRevert:      {icon: "↶", label: "Revert", accent: func(p theme.Palette) lipgloss.Color { return p.Peach }},
	toolbarRevertOk:    {icon: "✓", label: "Ok", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	toolbarRevertAbort: {icon: "✕", label: "Abort", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
}

// toolbarCellWidths returns the visual width of each button cell.
// Used by both the renderer and hit-tester for consistent layout.
func toolbarCellWidths(buttons []int) []int {
	widths := make([]int, len(buttons))
	for i, id := range buttons {
		def := toolbarDefs[id]
		widths[i] = lipgloss.Width(" " + def.icon + " " + def.label + " ")
	}
	return widths
}

// renderToolbarButtons renders a right-aligned toolbar as two rows:
// a horizontal divider with ┬ separators, and a content row with │
// separators. The panel border provides the right and bottom edges.
// hoverIdx highlights a button on mouse hover (-1 = no hover).
// activeIdx marks a button as toggled on (-1 = none active); active buttons
// render with bold + accent to distinguish them from merely focused buttons.
func renderToolbarButtons(buttons []int, selectedIdx int, focused bool, hoverIdx int, activeIdx int, disabled map[int]bool, modeLabel string, width int, p theme.Palette) string {
	if len(buttons) == 0 {
		empty := strings.Repeat(" ", max(width, 0))
		return empty + "\n" + empty
	}

	bSt := lipgloss.NewStyle().Foreground(p.Border)
	labelSt := lipgloss.NewStyle().Foreground(p.Muted)
	widths := toolbarCellWidths(buttons)

	// Measure each button's inner content with styling.
	type btnMeasure struct {
		inner string // styled content (no separators)
		width int    // visible column width including padding
	}
	cells := make([]btnMeasure, len(buttons))
	totalInner := 0
	for i, id := range buttons {
		def := toolbarDefs[id]
		active := i == activeIdx
		selected := focused && i == selectedIdx
		hovered := i == hoverIdx && !selected && !active
		fg := p.Muted
		if disabled[i] {
			fg = p.Subtle
		} else if active || selected || hovered {
			fg = def.accent(p)
		}
		text := lipgloss.NewStyle().Foreground(fg).Bold(active && !disabled[i]).Render(def.icon + " " + def.label)
		padded := " " + text + " "
		cells[i] = btnMeasure{inner: padded, width: widths[i]}
		totalInner += widths[i]
	}

	// Total button group width: separators + cell widths.
	// Left separator │ before first button, │ between buttons; right edge = panel border.
	groupWidth := len(buttons) + totalInner
	leftPad := max(width-groupWidth, 0)

	// Row 1: top border spanning only the button group width.
	var div strings.Builder
	for i := range cells {
		if i == 0 {
			div.WriteString("╭")
		} else {
			div.WriteString("┬")
		}
		div.WriteString(strings.Repeat("─", widths[i]))
	}
	row1 := strings.Repeat(" ", leftPad) + bSt.Render(div.String())
	if row1Vis := lipgloss.Width(row1); row1Vis < width {
		row1 += strings.Repeat(" ", width-row1Vis)
	}

	// Row 2: mode label on the left, button content with │ separators on the right.
	var content strings.Builder
	styledLabel := labelSt.Render(" " + modeLabel)
	labelWidth := lipgloss.Width(styledLabel)
	content.WriteString(styledLabel)
	content.WriteString(strings.Repeat(" ", max(leftPad-labelWidth, 0)))
	for _, c := range cells {
		content.WriteString(bSt.Render("│"))
		content.WriteString(c.inner)
	}
	row2 := content.String()
	if vis := lipgloss.Width(row2); vis < width {
		row2 += strings.Repeat(" ", width-vis)
	}

	return row1 + "\n" + row2
}

// renderDiffToolbar renders the diff-mode toolbar: left buttons (or hint text)
// on the left, right buttons on the right. Uses a unified index space where
// indices 0..len(left)-1 are left buttons and len(left).. are right buttons.
func renderDiffToolbar(leftButtons, rightButtons []int, selectedIdx int, focused bool,
	hoverIdx int, activeIdx int, hint string, width int, p theme.Palette) string {

	bSt := lipgloss.NewStyle().Foreground(p.Border)

	// Measure right button group.
	type btnMeasure struct {
		inner string
		width int
	}
	rWidths := toolbarCellWidths(rightButtons)
	rCells := make([]btnMeasure, len(rightButtons))
	rTotalInner := 0
	leftCount := len(leftButtons)
	for i, id := range rightButtons {
		def := toolbarDefs[id]
		uIdx := leftCount + i
		active := uIdx == activeIdx
		selected := focused && uIdx == selectedIdx
		hovered := uIdx == hoverIdx
		fg := p.Muted
		if active || selected || hovered {
			fg = def.accent(p)
		}
		text := lipgloss.NewStyle().Foreground(fg).Bold(active).Render(def.icon + " " + def.label)
		padded := " " + text + " "
		rCells[i] = btnMeasure{inner: padded, width: rWidths[i]}
		rTotalInner += rWidths[i]
	}
	rGroupWidth := len(rightButtons) + rTotalInner

	// Measure left button group (if present).
	var lCells []btnMeasure
	var lWidths []int
	if len(leftButtons) > 0 {
		lWidths = toolbarCellWidths(leftButtons)
		lCells = make([]btnMeasure, len(leftButtons))
		for i, id := range leftButtons {
			def := toolbarDefs[id]
			uIdx := i
			selected := focused && uIdx == selectedIdx
			hovered := uIdx == hoverIdx
			fg := p.Muted
			if hovered || selected {
				fg = def.accent(p)
			}
			text := lipgloss.NewStyle().Foreground(fg).Render(def.icon + " " + def.label)
			padded := " " + text + " "
			lCells[i] = btnMeasure{inner: padded, width: lWidths[i]}
		}
	}

	rightLeftPad := max(width-rGroupWidth, 0)

	// Row 1: top borders for left group (left-aligned) and right group (right-aligned).
	var row1 strings.Builder
	if len(lCells) > 0 {
		var lDiv strings.Builder
		for i, w := range lWidths {
			if i > 0 {
				lDiv.WriteString("┬")
			}
			lDiv.WriteString(strings.Repeat("─", w))
		}
		lDiv.WriteString("╮")
		lBorder := bSt.Render(lDiv.String())
		row1.WriteString(lBorder)
		lBorderVis := lipgloss.Width(lBorder)
		gap := max(rightLeftPad-lBorderVis, 0)
		row1.WriteString(strings.Repeat(" ", gap))
	} else {
		row1.WriteString(strings.Repeat(" ", rightLeftPad))
	}
	var rDiv strings.Builder
	for i, w := range rWidths {
		if i == 0 {
			rDiv.WriteString("╭")
		} else {
			rDiv.WriteString("┬")
		}
		rDiv.WriteString(strings.Repeat("─", w))
	}
	row1.WriteString(bSt.Render(rDiv.String()))
	row1Str := row1.String()
	if vis := lipgloss.Width(row1Str); vis < width {
		row1Str += strings.Repeat(" ", width-vis)
	}

	// Row 2: left content + gap + right content.
	var row2 strings.Builder
	if len(lCells) > 0 {
		for i, c := range lCells {
			if i > 0 {
				row2.WriteString(bSt.Render("│"))
			}
			row2.WriteString(c.inner)
		}
		row2.WriteString(bSt.Render("│"))
		leftContentVis := lipgloss.Width(row2.String())
		gap := max(rightLeftPad-leftContentVis, 0)
		row2.WriteString(strings.Repeat(" ", gap))
	} else {
		// Render hint text on the left.
		labelSt := lipgloss.NewStyle().Foreground(p.Muted)
		styledHint := labelSt.Render(" " + hint)
		hintVis := lipgloss.Width(styledHint)
		row2.WriteString(styledHint)
		row2.WriteString(strings.Repeat(" ", max(rightLeftPad-hintVis, 0)))
	}
	for _, c := range rCells {
		row2.WriteString(bSt.Render("│"))
		row2.WriteString(c.inner)
	}
	row2Str := row2.String()
	if vis := lipgloss.Width(row2Str); vis < width {
		row2Str += strings.Repeat(" ", width-vis)
	}

	return row1Str + "\n" + row2Str
}

// renderCreateInputToolbar renders the toolbar with a branch name input on
// the left and buttons on the right. The Create button is highlighted to
// show it triggered the input. No separate top divider for the input area.
func renderCreateInputToolbar(buttons []int, name string, cursor int, cursorVisible bool, width int, p theme.Palette) string {
	bSt := lipgloss.NewStyle().Foreground(p.Border)
	widths := toolbarCellWidths(buttons)

	// Measure button group (same as renderToolbarButtons).
	type btnMeasure struct {
		inner string
		width int
	}
	cells := make([]btnMeasure, len(buttons))
	totalInner := 0
	for i, id := range buttons {
		def := toolbarDefs[id]
		// Highlight the Create button to show it's the active action.
		active := id == toolbarCreate
		fg := p.Muted
		if active {
			fg = def.accent(p)
		}
		text := lipgloss.NewStyle().Foreground(fg).Bold(active).Render(def.icon + " " + def.label)
		padded := " " + text + " "
		cells[i] = btnMeasure{inner: padded, width: widths[i]}
		totalInner += widths[i]
	}
	groupWidth := len(buttons) + totalInner
	leftPad := max(width-groupWidth, 0)

	// Row 1: top border spans only the button group (same as normal toolbar).
	var div strings.Builder
	for i := range cells {
		if i == 0 {
			div.WriteString("╭")
		} else {
			div.WriteString("┬")
		}
		div.WriteString(strings.Repeat("─", widths[i]))
	}
	row1 := strings.Repeat(" ", leftPad) + bSt.Render(div.String())
	if row1Vis := lipgloss.Width(row1); row1Vis < width {
		row1 += strings.Repeat(" ", width-row1Vis)
	}

	// Row 2: input field on the left, buttons on the right.
	labelSt := lipgloss.NewStyle().Foreground(p.Foreground)
	textSt := lipgloss.NewStyle().Foreground(p.Foreground)
	cursorSt := lipgloss.NewStyle().Foreground(p.Primary)

	label := labelSt.Render(" Name: ")

	runes := []rune(name)
	cur := min(max(cursor, 0), len(runes))
	before := textSt.Render(string(runes[:cur]))
	after := textSt.Render(string(runes[cur:]))

	curGlyph := ""
	if cursorVisible {
		curGlyph = cursorSt.Render("▏")
	}

	inputContent := label + before + curGlyph + after
	inputVis := lipgloss.Width(inputContent)

	// Build button group for row 2.
	var btnContent strings.Builder
	for _, c := range cells {
		btnContent.WriteString(bSt.Render("│"))
		btnContent.WriteString(c.inner)
	}
	btnStr := btnContent.String()

	// Pad input to fill the space left of the buttons.
	if inputVis < leftPad {
		inputContent += strings.Repeat(" ", leftPad-inputVis)
	}

	row2 := inputContent + btnStr
	if vis := lipgloss.Width(row2); vis < width {
		row2 += strings.Repeat(" ", width-vis)
	}

	return row1 + "\n" + row2
}
