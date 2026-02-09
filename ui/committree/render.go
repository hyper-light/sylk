package committree

import (
	"fmt"
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

// renderNode renders a single commit card with a tree connector below it.
// Selected cards use Primary border; unselected use a muted border.
//
// Layout (selected):
//
//	╭──────────────────────────────────────────╮
//	│ ● abc1234 (main)       +12 -3    2h ago │
//	│   Add git mode scaffolding               │
//	╰──────────────────────────────────────────╯
//	  │
//
// Layout (unselected):
//
//	╭──────────────────────────────────────────╮
//	│ ● def5678              +245 -0    3d ago │
//	│   Fix bug in parser                      │
//	╰──────────────────────────────────────────╯
//	  │
func renderNode(n TreeNode, selected bool, width int, th *theme.Theme, isLast bool) []string {
	p := th.Palette

	// Border consumes 2 columns (left + right border characters).
	const borderCols = 2
	innerWidth := max(width-borderCols, 0)

	// Build the two content lines for the card.
	header := buildHeaderLine(n, innerWidth, p)
	subject := buildSubjectLine(n, innerWidth, p)
	content := header + "\n" + subject

	// Select border color based on selection state.
	borderColor := p.Border
	if selected {
		borderColor = p.Primary
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(innerWidth)

	rendered := boxStyle.Render(content)
	cardLines := strings.Split(rendered, "\n")

	// Build edge connector line (or blank for the last node).
	edgeStyle := lipgloss.NewStyle().Foreground(p.Border)
	var connector string
	if !isLast {
		connector = "  " + edgeStyle.Render(edgeGlyph)
	}

	lines := make([]string, 0, nodeHeight)
	for _, cl := range cardLines {
		lines = append(lines, padLine(cl, width))
	}
	lines = append(lines, padLine(connector, width))

	// Clamp to exactly nodeHeight lines.
	for len(lines) < nodeHeight {
		lines = append(lines, strings.Repeat(" ", max(width, 0)))
	}
	if len(lines) > nodeHeight {
		lines = lines[:nodeHeight]
	}

	return lines
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
	maxBranchCols      = 2  // max offshoot cards per row
	minBranchCardWidth = 24 // minimum usable card width
	primaryWidthPct    = 70 // primary card width as percentage of panel
	offshootWidthPct   = 55 // single-column offshoot width as percentage
)

// branchCols returns how many offshoot cards fit side-by-side.
func branchCols(width int) int {
	return clampInt((width+branchCardGap)/(minBranchCardWidth+branchCardGap), 1, maxBranchCols)
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

// actionBlockedReason returns the reason for whichever action is currently
// blocked, preferring the commit reason for HEAD and the switch reason
// for non-HEAD branches.
func (e *branchExpansion) actionBlockedReason(isHead bool) string {
	if r := e.commitBlockedReason(isHead); r != "" {
		return r
	}
	return e.switchBlockedReason(isHead)
}

// buildBranchCard returns the rendered lines for a single branch card.
// Normal cards produce 4 lines; expanded cards produce 6–7 depending on
// whether a blocked-reason line is needed.
func buildBranchCard(b BranchNode, selected bool, innerWidth int, p theme.Palette,
	trunkInner int, hasTrunkTop, hasTrunkBot bool,
	expanded bool, exp *branchExpansion, wt workingTreeState) []string {

	borderColor := p.Border
	if selected {
		borderColor = p.Primary
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

	topBorder := buildCardBorder("╭", "╮", innerWidth, bSt, trunkInner, hasTrunkTop)

	lines := []string{
		topBorder,
		bSt.Render("│") + header + bSt.Render("│"),
		bSt.Render("│") + subject + bSt.Render("│"),
	}

	if expanded && exp != nil {
		mutedSt := lipgloss.NewStyle().Foreground(p.Muted)
		divider := bSt.Render("├") + mutedSt.Render(strings.Repeat("╌", innerWidth)) + bSt.Render("┤")
		content := bSt.Render("│") + padContent(buildExpContentLine(b, exp, innerWidth, p), innerWidth) + bSt.Render("│")
		lines = append(lines, divider, content)

		// Commit input line (HEAD only, shown after selecting [Commit]).
		// While visible, suppresses the blocked-reason line.
		if exp.commitInput && b.IsHead {
			inputLine := bSt.Render("│") + padContent(buildCommitInputLine(exp, innerWidth, p), innerWidth) + bSt.Render("│")
			lines = append(lines, inputLine)
		} else if reason := exp.actionBlockedReason(b.IsHead); reason != "" {
			reasonSt := lipgloss.NewStyle().Foreground(p.Warning)
			reasonLine := bSt.Render("│") + padContent(" "+reasonSt.Render(reason), innerWidth) + bSt.Render("│")
			lines = append(lines, reasonLine)
		}
	}

	var botBorder string
	if hasTrunkBot {
		botBorder = buildCardBorder("╰", "╯", innerWidth, bSt, trunkInner, true)
	} else {
		botBorder = bSt.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	}
	lines = append(lines, botBorder)

	return lines
}

// buildExpContentLine renders the single expanded info row:
//
//	" N commits  [Switch]  [Delete]"
//	" N commits  [dirty]  [Switch]  [Delete]"
//
// Left-aligned with 1-space indent. Stats and actions on a single line.
func buildExpContentLine(b BranchNode, exp *branchExpansion, width int, p theme.Palette) string {
	countStyle := lipgloss.NewStyle().Foreground(p.Foreground)
	count := itoa(b.CommitCount)
	if b.CommitCountCapped {
		count += "+"
	}
	text := " " + countStyle.Render(count+" commits")

	if b.IsHead {
		if exp.wt.conflicts {
			st := lipgloss.NewStyle().Foreground(p.Error)
			text += "  " + st.Render("[conflicts]")
		} else if exp.wt.dirty {
			st := lipgloss.NewStyle().Foreground(p.Warning)
			text += "  " + st.Render("[dirty]")
		}
	}

	// [Commit] badge — HEAD only, enabled when staged files exist.
	if b.IsHead {
		commitEn := exp.hasStagedFiles
		commitLabel := renderActionBadge("Commit", p.Secondary, commitEn, exp.selectedActionID == branchActionCommit, p)
		text += "  " + commitLabel
	}

	// [Switch] badge — hidden for HEAD (already on this branch).
	if !b.IsHead {
		switchEn := exp.switchEnabled(b.IsHead)
		switchLabel := renderActionBadge("Switch", p.Success, switchEn, exp.selectedActionID == branchActionSwitch, p)
		text += "  " + switchLabel
	}

	// [Delete] badge — hidden for HEAD and default branch.
	if !b.IsHead && exp.deleteVisible(b.Name) {
		deleteEn := exp.deleteEnabled(b.Name, b.IsHead)
		deleteLabel := renderActionBadge("Delete", p.Error, deleteEn, exp.selectedActionID == branchActionDelete, p)
		text += " " + deleteLabel
	}

	return text
}

// renderActionBadge renders a single [Label] badge with appropriate styling.
// accent is the semantic color for the action (e.g. Success for Switch, Error
// for Delete). Four visual states:
//   - enabled + selected:  accent foreground, bold
//   - enabled + unselected: muted text
//   - disabled + selected:  muted text with selection background
//   - disabled + unselected: muted text
func renderActionBadge(label string, accent lipgloss.Color, enabled, selected bool, p theme.Palette) string {
	st := lipgloss.NewStyle().Foreground(p.Muted)
	switch {
	case enabled && selected:
		st = lipgloss.NewStyle().Foreground(accent).Bold(true)
	case selected:
		st = lipgloss.NewStyle().Foreground(p.Muted).Background(p.Selection)
	}
	return st.Render("[" + label + "]")
}

// spinnerFrames are the braille spinner glyphs used during commit progress.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// buildCommitInputLine renders the inline commit input row. The content
// depends on the commit phase:
//
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

// renderOffshootRow renders a row of 1–2 offshoot branch cards side by side.
// selectedCol is the selected card index within this row (-1 if none).
// expandedCol is the expanded card index within this row (-1 if none).
func renderOffshootRow(row []BranchNode, selectedCol, expandedCol int, exp *branchExpansion, cardWidth, width int, th *theme.Theme, hasTrunkAbove bool, wt workingTreeState) []string {
	p := th.Palette
	cols := len(row)
	trunkPos := width / 2
	trunkSt := lipgloss.NewStyle().Foreground(p.Border)

	// Compute card positions (centered group).
	totalContent := cols*cardWidth + max(cols-1, 0)*branchCardGap
	leftMargin := max((width-totalContent)/2, 0)

	const borderCols = 2
	innerWidth := max(cardWidth-borderCols, 0)

	cardLefts := make([]int, cols)
	cardCenters := make([]int, cols)
	for i := range cols {
		cardLefts[i] = leftMargin + i*(cardWidth+branchCardGap)
		cardCenters[i] = cardLefts[i] + cardWidth/2
	}

	// Build each card's lines (variable height).
	cardSlices := make([][]string, cols)
	maxCardHeight := 0
	for i, b := range row {
		selected := i == selectedCol
		isExpanded := i == expandedCol
		hasTrunkBot := cols == 1
		trunkInner := trunkPos - cardLefts[i] - 1
		cardSlices[i] = buildBranchCard(b, selected, innerWidth, p,
			trunkInner, hasTrunkAbove && cols == 1, hasTrunkBot,
			isExpanded, exp, wt)
		maxCardHeight = max(maxCardHeight, len(cardSlices[i]))
	}

	// Merge card lines horizontally with trunk in gap.
	// When one card is shorter (not expanded) and a sibling is expanded,
	// draw a vertical connector from the shorter card's center down to
	// the merge line so the tree graphic stays connected.
	lines := make([]string, 0, maxCardHeight+2)
	for lineIdx := range maxCardHeight {
		var buf strings.Builder
		for i := range cols {
			target := cardLefts[i]
			current := lipgloss.Width(buf.String())
			for current < target {
				if hasTrunkAbove && cols > 1 && current == trunkPos {
					buf.WriteString(trunkSt.Render("│"))
				} else {
					buf.WriteByte(' ')
				}
				current++
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
		}
		lines = append(lines, padLine(buf.String(), width))
	}

	// Merge line (or trunk for single card).
	if cols == 1 {
		trunk := strings.Repeat(" ", trunkPos) + trunkSt.Render("│")
		lines = append(lines, padLine(trunk, width))
	} else {
		lines = append(lines, renderMergeLine(cardCenters, trunkPos, width, th))
	}

	// Trunk line.
	trunk := strings.Repeat(" ", trunkPos) + trunkSt.Render("│")
	lines = append(lines, padLine(trunk, width))

	return lines
}

// renderPrimaryRow renders the primary branch card centered.
func renderPrimaryRow(b BranchNode, selected, expanded bool, exp *branchExpansion, width int, th *theme.Theme, hasOffshoots bool, wt workingTreeState) []string {
	p := th.Palette
	cardWidth := primaryCardWidth(width)
	const borderCols = 2
	innerWidth := max(cardWidth-borderCols, 0)
	leftPad := max((width-cardWidth)/2, 0)
	trunkInner := width/2 - leftPad - 1

	cardLines := buildBranchCard(b, selected, innerWidth, p,
		trunkInner, hasOffshoots, false,
		expanded, exp, wt)

	pad := strings.Repeat(" ", leftPad)
	blank := strings.Repeat(" ", max(width, 0))

	lines := make([]string, 0, len(cardLines)+2)
	for _, cl := range cardLines {
		lines = append(lines, padLine(pad+cl, width))
	}
	// Pad to at least branchRowHeight.
	for len(lines) < branchRowHeight {
		lines = append(lines, blank)
	}

	return lines
}

// renderMergeLine draws a horizontal connector from the leftmost card center
// to the rightmost card center, with ┬ at the trunk position.
func renderMergeLine(centers []int, trunkPos, width int, th *theme.Theme) string {
	st := lipgloss.NewStyle().Foreground(th.Palette.Border)
	leftC := centers[0]
	rightC := centers[len(centers)-1]

	var raw strings.Builder
	for col := leftC; col <= rightC; col++ {
		switch col {
		case leftC:
			raw.WriteString("╰")
		case rightC:
			raw.WriteString("╯")
		case trunkPos:
			raw.WriteString("┬")
		default:
			raw.WriteString("─")
		}
	}

	leftPad := strings.Repeat(" ", leftC)
	return padLine(leftPad+st.Render(raw.String()), width)
}

// buildCardBorder constructs a horizontal border with an optional trunk connector.
// For top borders (left="╭") inserts ┴; for bottom borders (left="╰") inserts ┬.
func buildCardBorder(left, right string, innerWidth int, bSt lipgloss.Style, trunkPos int, hasTrunk bool) string {
	if !hasTrunk || trunkPos < 0 || trunkPos >= innerWidth {
		return bSt.Render(left + strings.Repeat("─", innerWidth) + right)
	}
	connector := "┬"
	if left == "╭" {
		connector = "┴"
	}
	return bSt.Render(left + strings.Repeat("─", trunkPos) + connector + strings.Repeat("─", innerWidth-trunkPos-1) + right)
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
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(ts)

	const minGap = 2
	gap := max(availWidth-leftWidth-rightWidth, minGap)

	return left + strings.Repeat(" ", gap) + ts
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

// buildHeaderLine assembles the styled header content:
//
//	● abc1234 (main)  +12 -3  2h ago
func buildHeaderLine(n TreeNode, availWidth int, p theme.Palette) string {
	markerStyle := commitMarkerStyle(n, p)
	marker := markerStyle.Render(commitMarker)

	hashStyle := lipgloss.NewStyle().Foreground(p.Muted)
	hash := hashStyle.Render(n.ShortHash)

	stats := formatStats(n.Additions, n.Deletions, &p)

	timeStyle := lipgloss.NewStyle().Foreground(p.Muted)
	ts := timeStyle.Render(relativeTime(n.AuthorTime))

	// Right side: stats + gap + time.
	right := stats + "  " + ts
	rightWidth := lipgloss.Width(right)

	// Left side: marker + space + hash + optional branch.
	left := marker + " " + hash
	if n.Branch != "" {
		branchStyle := lipgloss.NewStyle().Foreground(p.Secondary).Bold(true)
		left += " " + branchStyle.Render("("+n.Branch+")")
	}
	leftWidth := lipgloss.Width(left)

	// Gap fills the space between left and right.
	// Minimum gap of 2 characters for readability.
	const minGap = 2
	gap := max(availWidth-leftWidth-rightWidth, minGap)

	return left + strings.Repeat(" ", gap) + right
}

// buildSubjectLine renders the commit subject indented to align with the
// header text (past the marker glyph).
func buildSubjectLine(n TreeNode, availWidth int, p theme.Palette) string {
	// Indent by marker width + space = 2 columns to align with hash.
	const indent = 2
	subjectStyle := lipgloss.NewStyle().Foreground(p.Foreground)

	// Right-align the author name.
	authorStyle := lipgloss.NewStyle().Foreground(p.Muted)
	author := authorStyle.Render(n.Author)
	authorWidth := lipgloss.Width(author)

	subjectMax := max(availWidth-indent-authorWidth-2, 0)
	subject := truncateSubject(n.Subject, subjectMax)

	left := strings.Repeat(" ", indent) + subjectStyle.Render(subject)
	leftWidth := lipgloss.Width(left)

	gap := max(availWidth-leftWidth-authorWidth, 1)
	return left + strings.Repeat(" ", gap) + author
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

// padLine pads or truncates a single styled line to exactly width visible columns.
func padLine(line string, width int) string {
	vis := lipgloss.Width(line)
	switch {
	case vis < width:
		return line + strings.Repeat(" ", width-vis)
	case vis > width && width > 0:
		return truncateStyled(line, width)
	default:
		return line
	}
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
