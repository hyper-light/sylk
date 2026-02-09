package gitpanel

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/theme"
)

// commitEntry represents a single commit in the commits list.
type commitEntry struct {
	hash      string
	shortHash string
	subject   string
	author    string
	time      time.Time
	isMerge   bool
}

func (e commitEntry) FilterText() string  { return e.shortHash + " " + e.subject + " " + e.author }
func (e commitEntry) SortKey() string     { return e.subject }
func (e commitEntry) SortTime() time.Time { return e.time }

// commitColWidths holds pre-computed max column widths for aligned rendering.
type commitColWidths struct {
	author int // max author name character width
}

// commitsTab holds the list state for the Commits tab.
type commitsTab struct {
	listState
	colWidths commitColWidths
}

// computeColWidths scans all entries and records the widest author column
// so every row can be padded to the same alignment.
func (ct *commitsTab) computeColWidths() {
	var maxAuthor int
	for _, e := range ct.entries {
		ce, ok := e.(commitEntry)
		if !ok {
			continue
		}
		if aw := len(ce.author); aw > maxAuthor {
			maxAuthor = aw
		}
	}
	ct.colWidths = commitColWidths{author: maxAuthor}
}

// loadCommits fetches commits from the git client and converts them to
// commitEntry values.
func loadCommits(gc *git.GitClient) []commitEntry {
	const maxCommits = 500

	infos, err := gc.GetAllCommits(maxCommits)
	if err != nil {
		return nil
	}

	entries := make([]commitEntry, 0, len(infos))
	for _, ci := range infos {
		entries = append(entries, commitEntry{
			hash:      ci.Hash,
			shortHash: ci.ShortHash,
			subject:   ci.Subject,
			author:    ci.Author,
			time:      ci.AuthorTime,
			isMerge:   ci.IsMergeCommit(),
		})
	}

	return entries
}

// renderCommitEntry renders a single commit row with per-segment background
// highlighting when selected.
//
// Format: shortHash  subject (truncated)  author  relativeTime
func renderCommitEntry(e commitEntry, selected bool, width int, th *theme.Theme, cols commitColWidths) string {
	p := th.Palette

	hashStyle := lipgloss.NewStyle().Foreground(p.Primary)
	if e.isMerge {
		hashStyle = lipgloss.NewStyle().Foreground(p.Secondary)
	}
	authorStyle := lipgloss.NewStyle().Foreground(p.Muted)
	timeStyle := lipgloss.NewStyle().Foreground(p.Muted)
	subjectStyle := lipgloss.NewStyle().Foreground(p.Foreground)
	padStyle := lipgloss.NewStyle()

	if selected {
		bg := p.Selection
		hashStyle = hashStyle.Background(bg)
		authorStyle = authorStyle.Background(bg)
		timeStyle = timeStyle.Background(bg)
		subjectStyle = subjectStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}

	hash := hashStyle.Render(e.shortHash)

	// Author padded to fixed column width.
	author := authorStyle.Render(e.author)
	if pad := cols.author - len(e.author); pad > 0 {
		author += padStyle.Render(strings.Repeat(" ", pad))
	}

	relTime := timeStyle.Render(relativeTime(e.time))

	// Subject fills remaining space after fixed-width columns.
	hashWidth := len(e.shortHash)
	timeWidth := lipgloss.Width(relTime)
	separators := 6 // two-space gaps between columns
	fixedWidth := hashWidth + cols.author + timeWidth + separators
	subjectWidth := width - fixedWidth

	subject := e.subject
	if subjectWidth > 0 {
		subject = truncateString(subject, subjectWidth)
	} else {
		subject = ""
	}

	sep := padStyle.Render("  ")
	content := hash + sep + subjectStyle.Render(subject) + sep + author + sep + relTime

	lineWidth := lipgloss.Width(content)
	padCount := max(width-lineWidth, 0)
	if padCount > 0 {
		content += padStyle.Render(strings.Repeat(" ", padCount))
	}

	return content
}

// relativeTime formats a time as a human-friendly relative duration.
func relativeTime(t time.Time) string {
	d := time.Since(t)

	const (
		day   = 24 * time.Hour
		month = 30 * day
		year  = 365 * day
	)

	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < day:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < month:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < year:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// truncateString truncates s to at most maxWidth visible characters,
// appending an ellipsis if truncated.
func truncateString(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}

	if maxWidth <= 1 {
		return string(runes[:maxWidth])
	}

	return string(runes[:maxWidth-1]) + "\u2026"
}

// padToWidthPlain pads a rendered string with spaces to fill width, without
// applying any style to the padding (caller applies background).
func padToWidthPlain(content string, width int) string {
	contentWidth := lipgloss.Width(content)
	if contentWidth >= width {
		return content
	}

	return content + strings.Repeat(" ", width-contentWidth)
}
