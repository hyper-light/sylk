package input

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestHighlightSlashCommand_StylesCommandArgumentSeparately(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
	})

	m := New(theme.DefaultDark(), 16)
	m.SetSlashValidator(func(cmd string) bool { return cmd == "debug-ui" })

	rendered := m.highlightSlashCommand("/debug-ui enable")
	if got := stripANSIInput(rendered); got != "/debug-ui enable" {
		t.Fatalf("stripANSIInput(rendered) = %q, want %q", got, "/debug-ui enable")
	}
	cmdIdx := strings.Index(rendered, "/debug-ui")
	argIdx := strings.Index(rendered, "enable")
	if cmdIdx < 0 || argIdx < 0 || argIdx <= cmdIdx {
		t.Fatalf("rendered missing command or argument styling anchors: %q", rendered)
	}
	if !strings.Contains(rendered[:cmdIdx], "\x1b[") {
		t.Fatalf("expected command token to be styled, got %q", rendered)
	}
	if !strings.Contains(rendered[:argIdx], "\x1b[") {
		t.Fatalf("expected styled prefix before argument token, got %q", rendered)
	}
	if !strings.Contains(rendered, "/debug-ui\x1b[0m \x1b[") {
		t.Fatalf("expected command reset, plain separator, and argument styling boundary, got %q", rendered)
	}
	if strings.Count(rendered, "\x1b[") < 2 {
		t.Fatalf("expected at least separate command and argument styles, got %q", rendered)
	}
}

func stripANSIInput(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSITerminatorInput(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func isCSITerminatorInput(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}
