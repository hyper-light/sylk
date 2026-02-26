package status

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestAuthIconsInStatusBar(t *testing.T) {
	th := theme.New(theme.DarkPalette)
	mgr := session.NewManager(session.DefaultManagerConfig())
	m := New(th, mgr)

	// Simulate what app.go does
	m.SetNerdFonts(false)
	m.SetAuthStatus("google", true)
	m.SetAuthStatus("anthropic", false)
	m.SetAuthStatus("openai", true)
	m.SetSize(120, 1)

	view := m.View()

	// Strip ANSI for content check
	stripped := stripANSI(view)
	t.Logf("Raw view length: %d", len(view))
	t.Logf("Stripped view: [%s]", stripped)
	t.Logf("Stripped length: %d", len(stripped))

	if !strings.Contains(stripped, "G") {
		t.Error("missing Google icon 'G' in status bar")
	}
	if !strings.Contains(stripped, "A!") {
		t.Error("missing Anthropic icon 'A!' in status bar")
	}
	if !strings.Contains(stripped, "O") {
		t.Error("missing OpenAI icon 'O' in status bar")
	}

	// Check renderRight directly
	right := m.renderRight()
	strippedRight := stripANSI(right)
	t.Logf("renderRight stripped: [%s]", strippedRight)

	// Verify auth comes before tokens
	authIdx := strings.Index(strippedRight, "G")
	tokenIdx := strings.Index(strippedRight, "τ")
	if authIdx < 0 {
		t.Fatal("auth icons not found in renderRight()")
	}
	if tokenIdx < 0 {
		t.Fatal("token display not found in renderRight()")
	}
	if authIdx >= tokenIdx {
		t.Errorf("auth icons at %d should appear before tokens at %d", authIdx, tokenIdx)
	}

	// Also test the AuthIconDisplay in isolation
	auth := m.authIcons.View()
	strippedAuth := stripANSI(auth)
	t.Logf("authIcons.View() stripped: [%s]", strippedAuth)
	if strippedAuth == "" {
		t.Error("authIcons.View() returned empty string")
	}
}

func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if i < len(s)-1 && s[i] == '\033' && s[i+1] == '[' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++ // skip 'm'
			}
		} else {
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}
