package status

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestStatusBarPreservesTokenDisplayUnderTightWidth(t *testing.T) {
	th := theme.New(theme.DarkPalette)
	mgr := session.NewManager(session.DefaultManagerConfig())
	m := New(th, mgr)

	m.SetSize(42, 1)
	m.SetEngagedAgent("architect")
	m.SetPrompt("dispatching delegated plan acceptance callback")
	m.SetNerdFonts(false)
	m.SetAuthStatus("google", true)
	m.SetAuthStatus("anthropic", true)
	m.SetAuthStatus("openai", true)
	m.SetTokens(12345, 6789, 0, 0)

	view := stripANSI(m.View())
	if !strings.Contains(view, "↓12.3k/↑6.8k") {
		t.Fatalf("status bar lost token display: %q", view)
	}
}
