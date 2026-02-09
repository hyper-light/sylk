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
	name         string
	shortHash    string
	subject      string
	time         time.Time
	isHead       bool
	uncommitted  bool // working tree has uncommitted changes (HEAD only)
	hasConflicts bool // working tree has merge conflicts (HEAD only)
}

func (e branchEntry) FilterText() string  { return e.name + " " + e.subject }
func (e branchEntry) SortKey() string     { return e.name }
func (e branchEntry) SortTime() time.Time { return e.time }

// branchColWidths holds pre-computed max column widths for aligned rendering.
type branchColWidths struct {
	prefix int // max badge+space width
	name   int // max branch name character width
}

// branchesTab holds the list state for the Branches tab.
type branchesTab struct {
	listState
	colWidths branchColWidths
}

// computeColWidths scans all entries and records the widest prefix and name
// columns so every row can be padded to the same alignment.
func (bt *branchesTab) computeColWidths() {
	var maxPrefix, maxName int
	for _, e := range bt.entries {
		be, ok := e.(branchEntry)
		if !ok {
			continue
		}
		pw := 1 // leading space for clean / non-HEAD
		if be.hasConflicts {
			pw = len("[conflicts]") + 1
		} else if be.uncommitted {
			pw = len("[dirty]") + 1
		}
		if pw > maxPrefix {
			maxPrefix = pw
		}
		if nw := len(be.name); nw > maxName {
			maxName = nw
		}
	}
	bt.colWidths = branchColWidths{prefix: maxPrefix, name: maxName}
}

// loadBranches fetches branches from the git client and converts them to
// branchEntry values. The HEAD branch is annotated with working tree state.
func loadBranches(gc *git.GitClient) []branchEntry {
	infos, err := gc.ListBranches()
	if err != nil {
		return nil
	}

	// Check working tree status to annotate the HEAD branch.
	var dirty, conflicts bool
	statuses, sErr := gc.UncommittedFileStatuses()
	if sErr == nil {
		dirty = len(statuses) > 0
		for _, s := range statuses {
			if s == "!" {
				conflicts = true
				break
			}
		}
	}

	entries := make([]branchEntry, 0, len(infos))
	for _, bi := range infos {
		e := branchEntry{
			name:      bi.Name,
			shortHash: bi.ShortHash,
			subject:   bi.Subject,
			time:      bi.AuthorTime,
			isHead:    bi.IsHead,
		}
		if bi.IsHead {
			e.uncommitted = dirty
			e.hasConflicts = conflicts
		}
		entries = append(entries, e)
	}

	return entries
}

// renderBranchEntry renders a single branch row with per-segment background
// highlighting when selected.
//
// Layout: [badge] name  shortHash  subject  relativeTime
//
// Status badge on the left (HEAD only):
//   - [dirty]     in Warning — uncommitted changes
//   - [conflicts] in Error   — merge conflicts
//   - (space)                — clean or non-HEAD
//
// The HEAD branch name is rendered in Primary color.
func renderBranchEntry(e branchEntry, selected bool, width int, th *theme.Theme, cols branchColWidths, dirtyTree bool) string {
	p := th.Palette

	nameStyle := lipgloss.NewStyle().Foreground(p.Foreground).Bold(true)
	if e.isHead {
		nameStyle = nameStyle.Foreground(p.Primary)
	}

	hashStyle := lipgloss.NewStyle().Foreground(p.Primary)
	subjectStyle := lipgloss.NewStyle().Foreground(p.Subtext)
	timeStyle := lipgloss.NewStyle().Foreground(p.Muted)
	padStyle := lipgloss.NewStyle()

	if selected {
		bg := p.Selection
		nameStyle = nameStyle.Background(bg)
		hashStyle = hashStyle.Background(bg)
		subjectStyle = subjectStyle.Background(bg)
		timeStyle = timeStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}

	// Grey out non-HEAD branches when checkout is blocked.
	if dirtyTree && !e.isHead {
		nameStyle = nameStyle.Foreground(p.Muted)
		hashStyle = hashStyle.Foreground(p.Muted)
		subjectStyle = subjectStyle.Foreground(p.Muted)
		timeStyle = timeStyle.Foreground(p.Muted)
	}

	// Status badge on the left, padded to fixed column width.
	var prefix string
	var actualPrefixW int
	if e.hasConflicts {
		bSt := lipgloss.NewStyle().Foreground(p.Error)
		if selected {
			bSt = bSt.Background(p.Selection)
		}
		prefix = bSt.Render("[conflicts]") + padStyle.Render(" ")
		actualPrefixW = len("[conflicts]") + 1
	} else if e.uncommitted {
		bSt := lipgloss.NewStyle().Foreground(p.Warning)
		if selected {
			bSt = bSt.Background(p.Selection)
		}
		prefix = bSt.Render("[dirty]") + padStyle.Render(" ")
		actualPrefixW = len("[dirty]") + 1
	} else {
		prefix = padStyle.Render(" ")
		actualPrefixW = 1
	}
	if pad := cols.prefix - actualPrefixW; pad > 0 {
		prefix += padStyle.Render(strings.Repeat(" ", pad))
	}

	// Name padded to fixed column width.
	name := nameStyle.Render(e.name)
	if pad := cols.name - len(e.name); pad > 0 {
		name += padStyle.Render(strings.Repeat(" ", pad))
	}

	hash := hashStyle.Render(e.shortHash)
	relTime := timeStyle.Render(relativeTime(e.time))

	// Subject fills remaining space after fixed-width columns.
	hashWidth := len(e.shortHash)
	timeWidth := lipgloss.Width(relTime)
	separators := 6 // two-space gaps between columns
	fixedWidth := cols.prefix + cols.name + hashWidth + timeWidth + separators
	subjectWidth := width - fixedWidth

	subject := e.subject
	if subjectWidth > 0 {
		subject = truncateString(subject, subjectWidth)
	} else {
		subject = ""
	}

	sep := padStyle.Render("  ")
	content := prefix + name + sep + hash + sep + subjectStyle.Render(subject) + sep + relTime

	lineWidth := lipgloss.Width(content)
	padCount := max(width-lineWidth, 0)
	if padCount > 0 {
		content += padStyle.Render(strings.Repeat(" ", padCount))
	}

	return content
}
