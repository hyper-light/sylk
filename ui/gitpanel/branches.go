package gitpanel

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/theme"
)

// branchEntry represents a single branch in the branches list.
type branchEntry struct {
	name      string
	shortHash string
	subject   string
	time      time.Time
	isHead    bool
}

func (e branchEntry) FilterText() string  { return e.name + " " + e.subject }
func (e branchEntry) SortKey() string     { return e.name }
func (e branchEntry) SortTime() time.Time { return e.time }

// branchesTab holds the list state for the Branches tab.
type branchesTab struct {
	listState
}

// loadBranches fetches branches from the git client and converts them to
// branchEntry values.
func loadBranches(gc *git.GitClient) []branchEntry {
	infos, err := gc.ListBranches()
	if err != nil {
		return nil
	}

	entries := make([]branchEntry, 0, len(infos))
	for _, bi := range infos {
		entries = append(entries, branchEntry{
			name:      bi.Name,
			shortHash: bi.ShortHash,
			subject:   bi.Subject,
			time:      bi.AuthorTime,
			isHead:    bi.IsHead,
		})
	}

	return entries
}

// renderBranchEntry renders a single branch row with per-segment background
// highlighting when selected.
//
// Format: [marker] name  shortHash  subject (truncated)  relativeTime
func renderBranchEntry(e branchEntry, selected bool, width int, th *theme.Theme) string {
	p := th.Palette

	markerStyle := lipgloss.NewStyle().Foreground(p.Success)
	nameStyle := lipgloss.NewStyle().Foreground(p.Foreground).Bold(true)
	hashStyle := lipgloss.NewStyle().Foreground(p.Primary)
	subjectStyle := lipgloss.NewStyle().Foreground(p.Subtext)
	timeStyle := lipgloss.NewStyle().Foreground(p.Muted)
	padStyle := lipgloss.NewStyle()

	if selected {
		bg := p.Selection
		markerStyle = markerStyle.Background(bg)
		nameStyle = nameStyle.Background(bg)
		hashStyle = hashStyle.Background(bg)
		subjectStyle = subjectStyle.Background(bg)
		timeStyle = timeStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}

	marker := padStyle.Render("  ")
	if e.isHead {
		marker = markerStyle.Render("\u25cf") + padStyle.Render(" ")
	}

	name := nameStyle.Render(e.name)
	hash := hashStyle.Render(e.shortHash)
	relTime := timeStyle.Render(relativeTime(e.time))

	// Calculate space for subject after fixed-width columns.
	markerWidth := 2
	nameWidth := lipgloss.Width(name)
	hashWidth := len(e.shortHash)
	timeWidth := lipgloss.Width(relTime)
	separators := 6 // two-space gaps between columns
	fixedWidth := markerWidth + nameWidth + hashWidth + timeWidth + separators
	subjectWidth := width - fixedWidth

	subject := e.subject
	if subjectWidth > 0 {
		subject = truncateString(subject, subjectWidth)
	} else {
		subject = ""
	}

	sep := padStyle.Render("  ")
	content := marker + name + sep + hash + sep + subjectStyle.Render(subject) + sep + relTime

	lineWidth := lipgloss.Width(content)
	padCount := max(width-lineWidth, 0)
	if padCount > 0 {
		content += padStyle.Render(strings.Repeat(" ", padCount))
	}

	return content
}
