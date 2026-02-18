package gitpanel

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/theme"
)

// branchEntry represents a single branch in the branches list.
type branchEntry struct {
	name         string
	hash         string // full commit hash for stat lookups
	shortHash    string
	subject      string
	time         time.Time
	isHead       bool
	uncommitted  bool // working tree has uncommitted changes (HEAD only)
	hasConflicts bool // working tree has merge conflicts (HEAD only)

	commitCount       int  // commits unique to this branch vs. default
	commitCountCapped bool // true if count hit the counting limit
	behindCount       int  // commits in default not reachable from this branch
	behindCountCapped bool // true if behind count hit the counting limit

	additions, deletions                   int
	filesAdded, filesModified, filesDeleted int
	hasFileStats bool // true when file-level stats are populated
	hasLineStats bool // true when line-level stats are populated
}

func (e branchEntry) FilterText() string  { return e.name + " " + e.subject }
func (e branchEntry) SortKey() string     { return e.name }
func (e branchEntry) SortTime() time.Time { return e.time }

// branchesTab holds the list state for the Branches tab.
type branchesTab struct {
	listState
	allEntries   []branchEntry     // full cached set from initial LoadData
	dirtyTree    bool              // cached: HEAD has uncommitted or conflicts
	statsLoading bool              // true while async stat load is in flight
	statsEpoch   time.Time         // animation epoch for stat spinners
	statsLevel   git.DiffStatLevel // highest level loaded into entries
}

// branchCommitLimit is the maximum commits counted per branch before capping.
const branchCommitLimit = 1000

// loadBranches fetches branches from the git client and converts them to
// branchEntry values. The HEAD branch is annotated with working tree state.
// statLevel controls whether diff stats are computed inline.
func loadBranches(gc *git.GitClient, statLevel git.DiffStatLevel) []branchEntry {
	infos, err := gc.ListBranches()
	if err != nil {
		return nil
	}

	// Check working tree status to annotate the HEAD branch.
	var dirty, conflicts bool
	statuses, _, sErr := gc.UncommittedFileStatuses()
	if sErr == nil {
		dirty = len(statuses) > 0
		for _, s := range statuses {
			if s == "!" {
				conflicts = true
				break
			}
		}
	}

	hashes := make([]string, len(infos))
	for i, bi := range infos {
		hashes[i] = bi.Hash
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

	defaultBranch := gc.DefaultBranch()

	// Count unique commits per branch in a single batch (builds the base
	// reachable set once instead of once per branch).
	branchNames := make([]string, len(infos))
	for i, bi := range infos {
		branchNames[i] = bi.Name
	}
	commitCounts := gc.CountBranchOnlyCommitsBatch(branchNames, defaultBranch, branchCommitLimit)

	entries := make([]branchEntry, 0, len(infos))
	for _, bi := range infos {
		e := branchEntry{
			name:      bi.Name,
			hash:      bi.Hash,
			shortHash: bi.ShortHash,
			subject:   bi.Subject,
			time:      bi.AuthorTime,
			isHead:    bi.IsHead,
		}
		if bi.IsHead {
			e.uncommitted = dirty
			e.hasConflicts = conflicts
		}
		if bc, ok := commitCounts[bi.Name]; ok {
			e.commitCount = bc.Count
			e.commitCountCapped = bc.Capped
			e.behindCount = bc.Behind
			e.behindCountCapped = bc.BehindCapped
		}
		if ds, ok := lineSummaries[bi.Hash]; ok {
			e.additions = ds.Additions
			e.deletions = ds.Deletions
			e.filesAdded = ds.FilesAdded
			e.filesModified = ds.FilesModified
			e.filesDeleted = ds.FilesDeleted
			e.hasFileStats = true
			e.hasLineStats = true
		} else if fs, ok := fileSummaries[bi.Hash]; ok {
			e.filesAdded = fs.FilesAdded
			e.filesModified = fs.FilesModified
			e.filesDeleted = fs.FilesDeleted
			e.hasFileStats = true
		}
		entries = append(entries, e)
	}

	return entries
}

// renderBranchCell renders a single column cell for a branch entry.
func renderBranchCell(e branchEntry, colID ColumnID, width int, selected bool, th *theme.Theme, dirtyTree bool) string {
	p := th.Palette
	padStyle := cellPadStyle(selected, p)
	greyOut := dirtyTree && !e.isHead

	switch colID {
	case "status":
		return renderBranchBadge(e, width, selected, p)

	case "name":
		style := lipgloss.NewStyle().Foreground(p.Foreground).Bold(true)
		if e.isHead {
			style = style.Foreground(p.Primary)
		}
		if greyOut {
			style = style.Foreground(p.Muted)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(" "+e.name, width, style)

	case "hash":
		style := lipgloss.NewStyle().Foreground(p.Primary)
		if greyOut {
			style = style.Foreground(p.Muted)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(e.shortHash, width, style)

	case "subject":
		style := lipgloss.NewStyle().Foreground(p.Subtext)
		if greyOut {
			style = style.Foreground(p.Muted)
		}
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

	case "commits":
		text := strconv.Itoa(e.commitCount)
		if e.commitCountCapped {
			text += "+"
		}
		if e.behindCount > 0 {
			behind := strconv.Itoa(e.behindCount)
			if e.behindCountCapped {
				behind += "+"
			}
			text += " -" + behind
		}
		style := lipgloss.NewStyle().Foreground(p.Muted)
		if greyOut {
			style = style.Foreground(p.Muted)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(text, width, style)

	case "changed":
		style := lipgloss.NewStyle().Foreground(p.Muted)
		if greyOut {
			style = style.Foreground(p.Muted)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.additions+e.deletions), width, style)

	case "added":
		style := lipgloss.NewStyle().Foreground(p.Success)
		if greyOut {
			style = style.Foreground(p.Muted)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.additions), width, style)

	case "removed":
		style := lipgloss.NewStyle().Foreground(p.Error)
		if greyOut {
			style = style.Foreground(p.Muted)
		}
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
		if greyOut {
			style = style.Foreground(p.Muted)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.filesAdded), width, style)

	case "files_modified":
		style := lipgloss.NewStyle().Foreground(p.Warning)
		if greyOut {
			style = style.Foreground(p.Muted)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.filesModified), width, style)

	case "files_deleted":
		style := lipgloss.NewStyle().Foreground(p.Error)
		if greyOut {
			style = style.Foreground(p.Muted)
		}
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(strconv.Itoa(e.filesDeleted), width, style)

	default:
		return padStyle.Render(strings.Repeat(" ", width))
	}
}

// renderBranchBadge renders the status badge cell for a branch entry.
func renderBranchBadge(e branchEntry, width int, selected bool, p theme.Palette) string {
	padStyle := cellPadStyle(selected, p)

	if e.hasConflicts {
		style := lipgloss.NewStyle().Foreground(p.Error)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell("[conflicts]", width, style)
	}
	if e.uncommitted {
		style := lipgloss.NewStyle().Foreground(p.Warning)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell("[dirty]", width, style)
	}

	return padStyle.Render(strings.Repeat(" ", width))
}
