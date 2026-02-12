package gitpanel

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/theme"
)

// commitEntry represents a single commit in the commits list.
type commitEntry struct {
	hash            string
	shortHash       string
	subject         string
	author          string
	time            time.Time
	isMerge         bool
	isHead          bool // true for the HEAD commit (first in git log output)
	isDefaultBranch bool // true for the default branch tip commit

	additions, deletions                   int
	filesAdded, filesModified, filesDeleted int
	hasFileStats bool // true when file-level stats are populated
	hasLineStats bool // true when line-level stats are populated
}

func (e commitEntry) FilterText() string  { return e.shortHash + " " + e.subject + " " + e.author }
func (e commitEntry) SortKey() string     { return e.subject }
func (e commitEntry) SortTime() time.Time { return e.time }

// commitsTab holds the list state for the Commits tab.
type commitsTab struct {
	listState
	statsLoading bool              // true while async stat load is in flight
	statsEpoch   time.Time         // animation epoch for stat spinners
	statsLevel   git.DiffStatLevel // highest level loaded into entries
}

// loadCommitsPage fetches a page of commits from the git client.
// skip is the number of commits to skip; pageSize is the page size.
// statLevel controls whether diff stats are computed inline.
// Returns (entries, hasMore). isHead is only true for the very first
// commit when skip == 0.
func loadCommitsPage(gc *git.GitClient, defaultTipHash string, skip, pageSize int, statLevel git.DiffStatLevel) ([]commitEntry, bool) {
	infos, hasMore, err := gc.GetAllCommitsPage(skip, pageSize)
	if err != nil {
		return nil, false
	}

	hashes := make([]string, len(infos))
	for i, ci := range infos {
		hashes[i] = ci.Hash
	}

	// Fetch stats only at the requested level.
	var lineSummaries map[string]git.DiffSummary
	var fileSummaries map[string]git.FileSummary
	switch statLevel {
	case git.DiffStatLines:
		lineSummaries = gc.GetCommitDiffSummaries(hashes)
	case git.DiffStatFiles:
		fileSummaries = gc.GetCommitFileSummaries(hashes)
	}

	entries := make([]commitEntry, 0, len(infos))
	for i, ci := range infos {
		ce := commitEntry{
			hash:            ci.Hash,
			shortHash:       ci.ShortHash,
			subject:         ci.Subject,
			author:          ci.Author,
			time:            ci.AuthorTime,
			isMerge:         ci.IsMergeCommit(),
			isHead:          skip == 0 && i == 0,
			isDefaultBranch: ci.Hash == defaultTipHash,
		}
		if ds, ok := lineSummaries[ci.Hash]; ok {
			ce.additions = ds.Additions
			ce.deletions = ds.Deletions
			ce.filesAdded = ds.FilesAdded
			ce.filesModified = ds.FilesModified
			ce.filesDeleted = ds.FilesDeleted
			ce.hasFileStats = true
			ce.hasLineStats = true
		} else if fs, ok := fileSummaries[ci.Hash]; ok {
			ce.filesAdded = fs.FilesAdded
			ce.filesModified = fs.FilesModified
			ce.filesDeleted = fs.FilesDeleted
			ce.hasFileStats = true
		}
		entries = append(entries, ce)
	}

	return entries, hasMore
}

// renderCommitCell renders a single column cell for a commit entry.
func renderCommitCell(e commitEntry, colID ColumnID, width int, selected bool, th *theme.Theme) string {
	p := th.Palette
	padStyle := cellPadStyle(selected, p)

	switch colID {
	case "hash":
		style := lipgloss.NewStyle().Foreground(p.Primary)
		if e.isHead {
			style = lipgloss.NewStyle().Foreground(p.Secondary).Bold(true)
		} else if e.isDefaultBranch {
			style = lipgloss.NewStyle().Foreground(p.Teal).Bold(true)
		} else if e.isMerge {
			style = lipgloss.NewStyle().Foreground(p.Secondary)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(" "+e.shortHash, width, style)

	case "subject":
		style := lipgloss.NewStyle().Foreground(p.Foreground)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(e.subject, width, style)

	case "author":
		style := lipgloss.NewStyle().Foreground(p.Muted)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(e.author, width, style)

	case "time":
		style := lipgloss.NewStyle().Foreground(p.Muted)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(relativeTime(e.time), width, style)

	case "changed":
		style := lipgloss.NewStyle().Foreground(p.Muted)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.additions+e.deletions), width, style)

	case "added":
		style := lipgloss.NewStyle().Foreground(p.Success)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.additions), width, style)

	case "removed":
		style := lipgloss.NewStyle().Foreground(p.Error)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.deletions), width, style)

	case "files":
		style := lipgloss.NewStyle().Foreground(p.Muted)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.filesAdded+e.filesModified+e.filesDeleted), width, style)

	case "files_added":
		style := lipgloss.NewStyle().Foreground(p.Success)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.filesAdded), width, style)

	case "files_modified":
		style := lipgloss.NewStyle().Foreground(p.Warning)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.filesModified), width, style)

	case "files_deleted":
		style := lipgloss.NewStyle().Foreground(p.Error)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.filesDeleted), width, style)

	default:
		return padStyle.Render(strings.Repeat(" ", width))
	}
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

// cellPadStyle returns a plain padding style, with selection background
// when the row is selected.
func cellPadStyle(selected bool, p theme.Palette) lipgloss.Style {
	s := lipgloss.NewStyle()
	if selected {
		s = s.Background(p.Selection)
	}
	return s
}
