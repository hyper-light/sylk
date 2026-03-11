package gitpanel

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestRenderBranchCellNameIncludesBranchMetadata(t *testing.T) {
	rendered := renderBranchCell(branchEntry{
		name:           "feature/refactor",
		isHead:         true,
		hasDivergence:  true,
		aheadCount:     3,
		behindUpstream: 1,
		hasBranchStash: true,
	}, "name", 48, false, theme.DefaultDark(), false)

	for _, want := range []string{"feature/refactor", "^3", "v1", "S"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered cell missing %q: %q", want, rendered)
		}
	}
	if got, want := lipgloss.Width(rendered), 48; got != want {
		t.Fatalf("rendered width = %d, want %d", got, want)
	}
}

func TestRenderBranchCellUnknownColumnFallsBackToBlankCell(t *testing.T) {
	rendered := renderBranchCell(branchEntry{name: "main"}, "unknown", 7, false, theme.DefaultDark(), false)
	if got, want := lipgloss.Width(rendered), 7; got != want {
		t.Fatalf("rendered width = %d, want %d", got, want)
	}
}
