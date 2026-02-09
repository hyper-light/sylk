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
// Derived from: trunk spacer(1) + angled arm(1) + card (4 lines) = 6.
const branchNodeHeight = 6

// trunkCols is the column width of the tree trunk + arm indent.
// Layout: col0 = trunk glyph, col1-3 = arm/indent (4 total).
const trunkCols = 4

// renderBranchNode renders a single branch as a bordered card connected
// to a vertical trunk via an angled arm, forming a visual tree.
//
// Layout (first + non-last):
//
//	├──╮
//	│  ╭──────────────────────────────╮
//	│  │ ● main             2h ago   │
//	│  │   abc1234  Fix cursor blink │
//	│  ╰──────────────────────────────╯
//	│
//
// Layout (middle):
//
//	│
//	├──╮
//	│  ╭──────────────────────────────╮
//	│  │ ● ivf              3d ago   │
//	│  │   def5678  Add new thing    │
//	│  ╰──────────────────────────────╯
//
// Layout (last):
//
//	│
//	╰──╮
//	   ╭──────────────────────────────╮
//	   │ ● feature/xyz       1w ago  │
//	   │   abc9012  Fix edge case    │
//	   ╰──────────────────────────────╯
//
func renderBranchNode(b BranchNode, selected bool, width int, th *theme.Theme, isFirst, isLast bool) []string {
	p := th.Palette

	armColor := p.Border
	if selected {
		armColor = p.Primary
	}
	armStyle := lipgloss.NewStyle().Foreground(armColor)
	trunkStyle := lipgloss.NewStyle().Foreground(p.Border)

	// Card area excludes the trunk prefix columns.
	cardWidth := max(width-trunkCols, 0)
	const borderCols = 2
	innerWidth := max(cardWidth-borderCols, 0)

	header := buildBranchHeaderLine(b, innerWidth, p)
	subject := buildBranchSubjectLine(b, innerWidth, p)

	borderColor := p.Border
	if selected {
		borderColor = p.Primary
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(innerWidth)

	rendered := boxStyle.Render(header + "\n" + subject)
	cardLines := strings.Split(rendered, "\n")

	lines := make([]string, 0, branchNodeHeight)
	blankIndent := strings.Repeat(" ", trunkCols)

	// Line 0: trunk spacer (blank for first node, │ otherwise).
	if isFirst {
		lines = append(lines, padLine("", width))
	} else {
		lines = append(lines, padLine(trunkStyle.Render("│"), width))
	}

	// Line 1: angled arm (├──╮ or ╰──╮).
	if isLast {
		lines = append(lines, padLine(armStyle.Render("╰──╮"), width))
	} else {
		lines = append(lines, padLine(armStyle.Render("├──╮"), width))
	}

	// Lines 2-5: card with trunk prefix (│ + space) or blank indent.
	for _, cl := range cardLines {
		var prefix string
		if isLast {
			prefix = blankIndent
		} else {
			prefix = trunkStyle.Render("│") + strings.Repeat(" ", trunkCols-1)
		}
		lines = append(lines, padLine(prefix+cl, width))
	}

	// Clamp to exactly branchNodeHeight.
	for len(lines) < branchNodeHeight {
		lines = append(lines, strings.Repeat(" ", max(width, 0)))
	}
	if len(lines) > branchNodeHeight {
		lines = lines[:branchNodeHeight]
	}

	return lines
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
