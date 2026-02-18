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

// tagsTab holds the list state for the Tags tab.
type tagsTab struct {
	listState
	allEntries []tagEntry // full cached set from initial LoadData
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

// renderTagCell renders a single column cell for a tag entry.
func renderTagCell(e tagEntry, colID ColumnID, width int, selected bool, th *theme.Theme) string {
	p := th.Palette
	padStyle := cellPadStyle(selected, p)

	switch colID {
	case "marker":
		style := lipgloss.NewStyle().Foreground(p.Muted)
		if selected {
			style = style.Background(p.Selection)
		}
		marker := "\u25cb" // ○ lightweight
		if e.annotated {
			marker = "\u25cf" // ● annotated
		}
		return fitCell(marker, width, style)

	case "name":
		style := lipgloss.NewStyle().Foreground(p.Foreground).Bold(true)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(e.name, width, style)

	case "hash":
		style := lipgloss.NewStyle().Foreground(p.Primary)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(e.shortHash, width, style)

	case "subject":
		style := lipgloss.NewStyle().Foreground(p.Subtext)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(e.subject, width, style)

	case "time":
		style := lipgloss.NewStyle().Foreground(p.Muted)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(relativeTime(e.time), width, style)

	default:
		return padStyle.Render(strings.Repeat(" ", width))
	}
}
