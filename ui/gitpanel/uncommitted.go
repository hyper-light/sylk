package gitpanel

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/theme"
)

// uncommittedEntry represents a single file with uncommitted changes.
type uncommittedEntry struct {
	path   string
	status string // status code: M, A, D, ?, !
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

// uncommittedTab holds the list state for the Uncommitted files tab.
type uncommittedTab struct {
	listState
	stagingStates  []StagingState // parallel to listState.entries
	optionsFocused bool           // focus on [All] options bar
}

// allStaged returns true when every entry is in StagingStaged state.
func (ut *uncommittedTab) allStaged() bool {
	if len(ut.stagingStates) == 0 {
		return false
	}
	for _, s := range ut.stagingStates {
		if s != StagingStaged {
			return false
		}
	}
	return true
}

// toggleAll sets all staging states to StagingStaged, or back to
// StagingDefault if all are already staged.
func (ut *uncommittedTab) toggleAll() {
	target := StagingStaged
	if ut.allStaged() {
		target = StagingDefault
	}
	for i := range ut.stagingStates {
		ut.stagingStates[i] = target
	}
}

// loadUncommitted fetches uncommitted files with real status codes
// from the git client using the go-git native API.
func loadUncommitted(gc *git.GitClient) []uncommittedEntry {
	statuses, err := gc.UncommittedFileStatuses()
	if err != nil {
		return nil
	}

	entries := make([]uncommittedEntry, 0, len(statuses))
	for path, status := range statuses {
		entries = append(entries, uncommittedEntry{
			path:   path,
			status: status,
		})
	}

	return entries
}

// renderUncommittedEntry renders a single uncommitted file row with
// per-segment background highlighting and optional staging icon.
//
// Layout (default):  [M] path/to/file.go
// Layout (staged):   ✓ [M] path/to/file.go
// Layout (excluded): ✕ [M] path/to/file.go
func renderUncommittedEntry(e uncommittedEntry, staging StagingState, selected bool, width int, th *theme.Theme) string {
	p := th.Palette

	badgeColor := statusBadgeColor(e.status, p)
	badgeStyle := lipgloss.NewStyle().Foreground(badgeColor)
	pathStyle := lipgloss.NewStyle().Foreground(p.Foreground)
	padStyle := lipgloss.NewStyle()

	// Staging icon styles.
	stagedStyle := lipgloss.NewStyle().Foreground(p.Success)
	excludedStyle := lipgloss.NewStyle().Foreground(p.Error)

	if selected {
		bg := p.Selection
		badgeStyle = badgeStyle.Background(bg)
		pathStyle = pathStyle.Background(bg)
		padStyle = padStyle.Background(bg)
		stagedStyle = stagedStyle.Background(bg)
		excludedStyle = excludedStyle.Background(bg)
	}

	// Staging icon column — always 2 chars wide for alignment.
	var iconPrefix string
	const iconWidth = 2
	switch staging {
	case StagingStaged:
		iconPrefix = stagedStyle.Render("\u2713") + padStyle.Render(" ")
	case StagingExcluded:
		iconPrefix = excludedStyle.Render("\u2715") + padStyle.Render(" ")
	default:
		iconPrefix = padStyle.Render("  ")
	}

	badge := badgeStyle.Render("[" + e.status + "]")
	badgeWidth := lipgloss.Width(badge)

	// Available width for the path after icon + badge + spaces.
	const separator = 1
	pathWidth := width - iconWidth - badgeWidth - separator

	path := e.path
	if pathWidth > 0 {
		path = truncateString(path, pathWidth)
	} else {
		path = ""
	}

	sep := padStyle.Render(" ")
	content := iconPrefix + badge + sep + pathStyle.Render(path)

	lineWidth := lipgloss.Width(content)
	padCount := max(width-lineWidth, 0)
	if padCount > 0 {
		content += padStyle.Render(strings.Repeat(" ", padCount))
	}

	return content
}

// renderOptionsBar renders the staging options row for the uncommitted tab.
// Contains [All] toggle to stage all files.
func renderOptionsBar(ut *uncommittedTab, width int, th *theme.Theme) string {
	p := th.Palette
	allActive := ut.allStaged()
	style := badgeStyle(p, allActive, ut.optionsFocused)
	badge := style.Render("[All]")
	content := " " + badge
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
