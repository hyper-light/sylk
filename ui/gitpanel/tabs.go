package gitpanel

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/theme"
)

// tabLabels maps each GitTab to its display label. Derived from the enum.
var tabLabels = [tabCount]string{
	TabCommits:     "Commits",
	TabBranches:    "Branches",
	TabTags:        "Tags",
	TabUncommitted: "Uncommitted",
}

// TabBarNaturalWidth returns the minimum content width to render all tabs
// on a single line without wrapping. Derived from tabLabels.
func TabBarNaturalWidth() int {
	w := 0
	for i := range tabCount {
		w += len(tabLabels[GitTab(i)]) + 2 // " Label "
	}
	w += (int(tabCount) - 1) * 3 // " | " separators
	return w
}

// renderTabBar renders a simple tab bar without close icons.
//
//	Commits | Branches | Uncommitted
//
// The active tab is styled with Primary foreground + bold; inactive tabs use
// Muted foreground. Separators are rendered in the Border color. The line is
// padded to fill the full width.
func renderTabBar(activeTab GitTab, width int, th *theme.Theme) string {
	p := th.Palette

	activeStyle := lipgloss.NewStyle().
		Foreground(p.Primary).
		Bold(true)

	inactiveStyle := lipgloss.NewStyle().
		Foreground(p.Muted)

	sep := lipgloss.NewStyle().
		Foreground(p.Border).
		Render(" \u2502 ")

	var parts []string

	for i := range tabCount {
		tab := GitTab(i)
		label := " " + tabLabels[tab] + " "

		if tab == activeTab {
			parts = append(parts, activeStyle.Render(label))
		} else {
			parts = append(parts, inactiveStyle.Render(label))
		}
	}

	content := strings.Join(parts, sep)

	return padToWidth(content, width, p)
}
