package status

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/msg"
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

func TestStatusBarClearsExpiredDoneProgressOnFinalDecorTick(t *testing.T) {
	th := theme.New(theme.DarkPalette)
	mgr := session.NewManager(session.DefaultManagerConfig())
	m := New(th, mgr)

	m.SetSize(80, 1)
	m.progress.Clear()
	m.progress.displayFrac = 1
	m.progress.doneUntil = time.Now().Add(-time.Second)
	m.viewDirty = false

	if !m.IsAnimating() {
		t.Fatal("IsAnimating() = false; want pending expiry to keep one final decor tick alive")
	}

	comp, _ := m.Update(msg.DecorTickMsg{Time: time.Now()})
	m = comp.(*Model)

	if m.progress.phase != PhaseNone {
		t.Fatalf("progress phase = %v, want %v after expiry tick", m.progress.phase, PhaseNone)
	}
	if !m.viewDirty {
		t.Fatal("viewDirty = false; want redraw after clearing expired done progress")
	}
	if got := m.progress.View(); got != "" {
		t.Fatalf("progress view = %q, want empty after expiry", got)
	}
}
