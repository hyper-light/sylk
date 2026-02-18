package gitpanel

import (
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/theme"
)

// uncommittedEntry represents a single file with uncommitted changes.
type uncommittedEntry struct {
	path    string
	status  string // status code: M, A, D, ?, !
	staging StagingState
}

func (e uncommittedEntry) FilterText() string  { return e.path }
func (e uncommittedEntry) SortKey() string     { return e.path }
func (e uncommittedEntry) SortTime() time.Time { return time.Time{} }

// StagingState represents the user's staging intent for an uncommitted file.
type StagingState int8

const (
	StagingDefault  StagingState = iota // no interaction — natural status
	StagingStaged                       // marked for staging (green ✓)
	StagingExcluded                     // explicitly excluded (red ✕)
	stagingStateCount                   // sentinel for cycling
)

// optionFocus tracks which badge in the options bar is focused.
type optionFocus int8

const (
	optionNone    optionFocus = iota // no options bar focus
	optionAll                        // [All] badge focused
	optionStash                      // [Stash] badge focused
	optionUnstash                    // [Unstash] badge focused
)

// uncommittedTab holds the list state for the Uncommitted files tab.
type uncommittedTab struct {
	listState
	stagingStates []StagingState     // parallel to listState.entries
	optionFocus   optionFocus        // which options badge is focused
	hasStash      bool               // true when git stash list is non-empty
	allEntries    []uncommittedEntry // full cached set from initial LoadData
	allStaging    []StagingState     // staging states parallel to allEntries
}

// allStaged returns true when every entry is in StagingStaged state.
func (ut *uncommittedTab) allStaged() bool {
	if len(ut.allStaging) == 0 {
		return false
	}
	for _, s := range ut.allStaging {
		if s != StagingStaged {
			return false
		}
	}
	return true
}

// toggleAll sets all staging states to StagingStaged, or back to
// StagingDefault if all are already staged. Propagates to allStaging
// so the state survives window eviction.
func (ut *uncommittedTab) toggleAll() {
	target := StagingStaged
	if ut.allStaged() {
		target = StagingDefault
	}
	for i := range ut.allStaging {
		ut.allStaging[i] = target
	}
	for i := range ut.stagingStates {
		ut.stagingStates[i] = target
	}
	// Sync entry staging fields for sort consistency.
	for i := range ut.entries {
		if ue, ok := ut.entries[i].(uncommittedEntry); ok {
			ue.staging = target
			ut.entries[i] = ue
		}
	}
}

// loadUncommitted fetches uncommitted files with real status codes
// from the git client using the go-git native API.
func loadUncommitted(gc *git.GitBus) ([]uncommittedEntry, error) {
	statuses, _, err := gc.UncommittedFileStatuses()
	if err != nil {
		return nil, err
	}

	entries := make([]uncommittedEntry, 0, len(statuses))
	for path, status := range statuses {
		entries = append(entries, uncommittedEntry{
			path:   path,
			status: status,
		})
	}

	// Stable default order: sort by path so map iteration randomness
	// doesn't cause visible reordering across reloads.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})

	return entries, nil
}

// renderUncommittedCell renders a single column cell for an uncommitted entry.
func renderUncommittedCell(e uncommittedEntry, staging StagingState, colID ColumnID, width int, selected bool, th *theme.Theme) string {
	p := th.Palette
	padStyle := cellPadStyle(selected, p)

	switch colID {
	case "icon":
		return renderStagingIcon(staging, width, selected, p)

	case "status":
		color := statusBadgeColor(e.status, p)
		style := lipgloss.NewStyle().Foreground(color)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell("["+e.status+"]", width, style)

	case "path":
		style := lipgloss.NewStyle().Foreground(p.Foreground)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(e.path, width, style)

	default:
		return padStyle.Render(strings.Repeat(" ", width))
	}
}

// renderStagingIcon renders the staging state icon cell.
func renderStagingIcon(staging StagingState, width int, selected bool, p theme.Palette) string {
	padStyle := cellPadStyle(selected, p)

	switch staging {
	case StagingStaged:
		style := lipgloss.NewStyle().Foreground(p.Success)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(" \u2713", width, style)
	case StagingExcluded:
		style := lipgloss.NewStyle().Foreground(p.Error)
		if selected {
			style = style.Background(p.Selection)
		}
		return fitCell(" \u2715", width, style)
	default:
		return padStyle.Render(strings.Repeat(" ", width))
	}
}

// renderOptionsBar renders the staging options row for the uncommitted tab.
// Contains [All] toggle, [Stash] action, and conditionally [Unstash].
func renderOptionsBar(ut *uncommittedTab, width int, th *theme.Theme) string {
	p := th.Palette
	allActive := ut.allStaged()
	allStyle := badgeStyle(p, allActive, ut.optionFocus == optionAll)
	stashStyle := badgeStyle(p, false, ut.optionFocus == optionStash)
	content := " " + allStyle.Render("[All]") + " " + stashStyle.Render("[Stash]")
	if ut.hasStash {
		unstashStyle := badgeStyle(p, false, ut.optionFocus == optionUnstash)
		content += " " + unstashStyle.Render("[Unstash]")
	}
	return padToWidth(content, width, p)
}

// statusBadgeColor returns the palette color for a git status code.
func statusBadgeColor(status string, p theme.Palette) lipgloss.Color {
	switch status {
	case "M":
		return p.Warning
	case "A":
		return p.Success
	case "D":
		return p.Error
	case "!":
		return p.Error
	default: // "?" (untracked) and anything else
		return p.Muted
	}
}
