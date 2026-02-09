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

// branchNodeHeight is the number of terminal rows consumed per branch node.
// Derived from: top border(1) + header(1) + subject(1) + bottom border(1) + trunk/blank(1) = 5.
const branchNodeHeight = 5

// Layout proportions for branch tree cards. Offshoot branches are narrower
// than the HEAD branch to visually emphasize the primary branch.
const (
	offshootWidthPct   = 55 // offshoot card width as percentage of panel
	headWidthPct       = 70 // HEAD card width as percentage of panel
	minBranchCardWidth = 24 // minimum usable card width
)

// renderBranchNode renders a single branch as a centered bordered card
// connected by a vertical trunk line through the card borders.
//
// Layout (first, offshoot):
//
//	        ╭─────────────────────────╮
//	        │ feature/xyz       1w ago│
//	        │   abc9012  Fix edge     │
//	        ╰───────────┬─────────────╯
//	                    │
//
// Layout (middle, offshoot):
//
//	        ╭───────────┴─────────────╮
//	        │ bugfix/abc        3d ago│
//	        │   def5678  Fix login    │
//	        ╰───────────┬─────────────╯
//	                    │
//
// Layout (last, HEAD — wider, centered):
//
//	    ╭───────────────┴─────────────────╮
//	    │ ● main                    2h ago│
//	    │   ghi9012  Merge feature        │
//	    ╰─────────────────────────────────╯
//
func renderBranchNode(b BranchNode, selected bool, width int, th *theme.Theme, isFirst, isLast bool) []string {
	p := th.Palette

	// Card width: HEAD is wider than offshoots.
	pct := offshootWidthPct
	if b.IsHead {
		pct = headWidthPct
	}
	cardWidth := clampInt(width*pct/100, minBranchCardWidth, width)

	const borderCols = 2
	innerWidth := max(cardWidth-borderCols, 0)

	// Center the card horizontally.
	leftPad := max((width-cardWidth)/2, 0)

	// Trunk position within the inner border content.
	trunkAbs := width / 2
	trunkInner := trunkAbs - leftPad - 1

	// Border color: primary when selected, muted otherwise.
	borderColor := p.Border
	if selected {
		borderColor = p.Primary
	}
	bSt := lipgloss.NewStyle().Foreground(borderColor)
	trunkSt := lipgloss.NewStyle().Foreground(p.Border)

	// Card content padded to exact inner width.
	header := padContent(buildBranchHeaderLine(b, innerWidth, p), innerWidth)
	subject := padContent(buildBranchSubjectLine(b, innerWidth, p), innerWidth)

	// Borders with trunk connectors (┴ top, ┬ bottom).
	top := buildCardBorder("╭", "╮", innerWidth, bSt, trunkInner, !isFirst)
	bottom := buildCardBorder("╰", "╯", innerWidth, bSt, trunkInner, !isLast)

	pad := strings.Repeat(" ", leftPad)

	// Trunk connector between nodes (blank for last).
	var trunkLine string
	if !isLast {
		trunkLine = strings.Repeat(" ", trunkAbs) + trunkSt.Render("│")
	}

	return []string{
		padLine(pad+top, width),
		padLine(pad+bSt.Render("│")+header+bSt.Render("│"), width),
		padLine(pad+bSt.Render("│")+subject+bSt.Render("│"), width),
		padLine(pad+bottom, width),
		padLine(trunkLine, width),
	}
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
func buildBranchHeaderLine(b BranchNode, availWidth int, p theme.Palette) string {
	markerStyle := lipgloss.NewStyle().Foreground(p.Muted)
	if b.IsHead {
		markerStyle = lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
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
