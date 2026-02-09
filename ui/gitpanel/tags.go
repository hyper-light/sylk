package gitpanel

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/theme"
)

// tagEntry represents a single tag in the tags list.
type tagEntry struct {
	name      string
	shortHash string
	subject   string
	time      time.Time
	annotated bool
}

func (e tagEntry) FilterText() string  { return e.name + " " + e.subject }
func (e tagEntry) SortKey() string     { return e.name }
func (e tagEntry) SortTime() time.Time { return e.time }

// tagColWidths holds pre-computed max column widths for aligned rendering.
type tagColWidths struct {
	name int // max tag name character width
}

// tagsTab holds the list state for the Tags tab.
type tagsTab struct {
	listState
	colWidths tagColWidths
}

// computeColWidths scans all entries and records the widest name column
// so every row can be padded to the same alignment.
func (tt *tagsTab) computeColWidths() {
	var maxName int
	for _, e := range tt.entries {
		te, ok := e.(tagEntry)
		if !ok {
			continue
		}
		if nw := len(te.name); nw > maxName {
			maxName = nw
		}
	}
	tt.colWidths = tagColWidths{name: maxName}
}

// loadTags fetches tags from the git client and converts them to tagEntry values.
func loadTags(gc *git.GitClient) []tagEntry {
	infos, err := gc.ListTags()
	if err != nil {
		return nil
	}

	entries := make([]tagEntry, 0, len(infos))
	for _, ti := range infos {
		entries = append(entries, tagEntry{
			name:      ti.Name,
			shortHash: ti.ShortHash,
			subject:   ti.Subject,
			time:      ti.AuthorTime,
			annotated: ti.Annotated,
		})
	}

	return entries
}

// renderTagEntry renders a single tag row with per-segment background
// highlighting when selected.
//
// Format: [marker] name  shortHash  subject (truncated)  relativeTime
func renderTagEntry(e tagEntry, selected bool, width int, th *theme.Theme, cols tagColWidths) string {
	p := th.Palette

	markerStyle := lipgloss.NewStyle().Foreground(p.Muted)
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

	// Annotated tags get a filled marker; lightweight tags get an outline.
	marker := markerStyle.Render("\u25cb") + padStyle.Render(" ") // ○
	if e.annotated {
		marker = markerStyle.Render("\u25cf") + padStyle.Render(" ") // ●
	}

	// Name padded to fixed column width.
	name := nameStyle.Render(e.name)
	if pad := cols.name - len(e.name); pad > 0 {
		name += padStyle.Render(strings.Repeat(" ", pad))
	}

	hash := hashStyle.Render(e.shortHash)
	relTime := timeStyle.Render(relativeTime(e.time))

	// Subject fills remaining space after fixed-width columns.
	markerWidth := 2
	hashWidth := len(e.shortHash)
	timeWidth := lipgloss.Width(relTime)
	separators := 6 // two-space gaps between columns
	fixedWidth := markerWidth + cols.name + hashWidth + timeWidth + separators
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
