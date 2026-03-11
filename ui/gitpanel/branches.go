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

	additions, deletions                    int
	filesAdded, filesModified, filesDeleted int
	hasFileStats                            bool // true when file-level stats are populated
	hasLineStats                            bool // true when line-level stats are populated

	// Upstream divergence (from ComputeDivergenceBatch).
	upstream       string // e.g. "origin/main"
	aheadCount     int
	behindUpstream int
	hasDivergence  bool // true when divergence data is populated
	hasBranchStash bool // true when a branch-associated stash exists
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

type branchColumnRenderer func(branchEntry, int, bool, theme.Palette, bool) string

var branchCellRenderers = map[ColumnID]branchColumnRenderer{
	"status":         renderBranchStatusCell,
	"name":           renderBranchNameCell,
	"hash":           renderBranchHashCell,
	"subject":        renderBranchSubjectCell,
	"time":           renderBranchTimeCell,
	"commits":        renderBranchCommitsCell,
	"changed":        renderBranchChangedCell,
	"added":          renderBranchAddedCell,
	"removed":        renderBranchRemovedCell,
	"files":          renderBranchFilesCell,
	"files_added":    renderBranchFilesAddedCell,
	"files_modified": renderBranchFilesModifiedCell,
	"files_deleted":  renderBranchFilesDeletedCell,
}

// loadBranches fetches branches from the git client and converts them to
// branchEntry values. The HEAD branch is annotated with working tree state.
// statLevel controls whether diff stats are computed inline.
func loadBranches(gc *git.GitBus, statLevel git.DiffStatLevel) []branchEntry {
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
	greyOut := dirtyTree && !e.isHead
	render, ok := branchCellRenderers[colID]
	if !ok {
		return blankBranchCell(width, selected, p)
	}
	return render(e, width, selected, p, greyOut)
}

func blankBranchCell(width int, selected bool, p theme.Palette) string {
	return cellPadStyle(selected, p).Render(strings.Repeat(" ", width))
}

func renderBranchStatusCell(e branchEntry, width int, selected bool, p theme.Palette, _ bool) string {
	return renderBranchBadge(e, width, selected, p)
}

func renderBranchNameCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(branchNameLabel(e, p), width, branchNameStyle(e, p, selected, greyOut))
}

func renderBranchHashCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(e.shortHash, width, branchCellStyle(p.Primary, selected, p, greyOut))
}

func renderBranchSubjectCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(e.subject, width, branchCellStyle(p.Subtext, selected, p, greyOut))
}

func renderBranchTimeCell(e branchEntry, width int, selected bool, p theme.Palette, _ bool) string {
	return fitCell(relativeTime(e.time), width, selectedCellStyle(p.Muted, selected, p))
}

func renderBranchCommitsCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(branchCommitSummary(e), width, branchCellStyle(p.Muted, selected, p, greyOut))
}

func renderBranchChangedCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(strconv.Itoa(e.additions+e.deletions), width, branchCellStyle(p.Muted, selected, p, greyOut))
}

func renderBranchAddedCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(strconv.Itoa(e.additions), width, branchCellStyle(p.Success, selected, p, greyOut))
}

func renderBranchRemovedCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(strconv.Itoa(e.deletions), width, branchCellStyle(p.Error, selected, p, greyOut))
}

func renderBranchFilesCell(e branchEntry, width int, selected bool, p theme.Palette, _ bool) string {
	return fitCell(strconv.Itoa(e.filesAdded+e.filesModified+e.filesDeleted), width, selectedCellStyle(p.Muted, selected, p))
}

func renderBranchFilesAddedCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(strconv.Itoa(e.filesAdded), width, branchCellStyle(p.Success, selected, p, greyOut))
}

func renderBranchFilesModifiedCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(strconv.Itoa(e.filesModified), width, branchCellStyle(p.Warning, selected, p, greyOut))
}

func renderBranchFilesDeletedCell(e branchEntry, width int, selected bool, p theme.Palette, greyOut bool) string {
	return fitCell(strconv.Itoa(e.filesDeleted), width, branchCellStyle(p.Error, selected, p, greyOut))
}

func branchNameStyle(e branchEntry, p theme.Palette, selected, greyOut bool) lipgloss.Style {
	base := p.Foreground
	if e.isHead {
		base = p.Primary
	}
	return branchCellStyle(base, selected, p, greyOut).Bold(true)
}

func branchCellStyle(base lipgloss.Color, selected bool, p theme.Palette, greyOut bool) lipgloss.Style {
	if greyOut {
		base = p.Muted
	}
	return selectedCellStyle(base, selected, p)
}

func selectedCellStyle(base lipgloss.Color, selected bool, p theme.Palette) lipgloss.Style {
	style := lipgloss.NewStyle().Foreground(base)
	if selected {
		style = style.Background(p.Selection)
	}
	return style
}

func branchNameLabel(e branchEntry, p theme.Palette) string {
	label := " " + e.name + renderDivergenceSuffix(e, p)
	if e.hasBranchStash {
		label += " " + lipgloss.NewStyle().Foreground(p.Lavender).Render("S")
	}
	return label
}

func branchCommitSummary(e branchEntry) string {
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
	return text
}

// renderDivergenceSuffix returns a styled ahead/behind indicator for upstream divergence.
// Returns "" when no divergence data is available or branch is in sync.
func renderDivergenceSuffix(e branchEntry, p theme.Palette) string {
	if !e.hasDivergence || (e.aheadCount == 0 && e.behindUpstream == 0) {
		return ""
	}
	var parts []string
	if e.aheadCount > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(p.Teal).Render(
			"^"+strconv.Itoa(e.aheadCount)))
	}
	if e.behindUpstream > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(p.Peach).Render(
			"v"+strconv.Itoa(e.behindUpstream)))
	}
	return " " + strings.Join(parts, " ")
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
