package login

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderOAuthBody_RendersOAuthURLOnSingleLineWithInlineCopy(t *testing.T) {
	p := New(theme.DefaultDark(), nil, nil, nil)
	p.SetSize(34, 20)
	p.step = stepOAuth
	p.provider = "anthropic"
	p.oauth = "Authorize Anthropic in your browser:"
	p.oauthURL = "https://console.anthropic.com/oauth/authorize?client_id=test-client&response_type=code&redirect_uri=https%3A%2F%2Fconsole.anthropic.com%2Foauth%2Fcode%2Fcallback&scope=org%3Aadmin&state=state-token"

	lines := p.renderOAuthBody(32, false)
	var copyRows []int
	for i, line := range lines {
		if strings.Contains(line, copyIcon) {
			copyRows = append(copyRows, i)
		}
	}
	if len(copyRows) != 1 {
		t.Fatalf("expected exactly one inline copy row, got %d rows in:\n%s", len(copyRows), strings.Join(lines, "\n"))
	}
	if copyRows[0] != 4 {
		t.Fatalf("copy row index = %d, want 4", copyRows[0])
	}
	if !strings.Contains(lines[copyRows[0]], "https://") {
		t.Fatalf("expected URL preview on inline copy row, got %q", lines[copyRows[0]])
	}
	copyStart := 1 + max(p.inputFieldWidth()-copyIconWidth, 10)
	if !p.hitTestCopyURL(copyStart) {
		t.Fatalf("expected hitTestCopyURL to accept inline copy button at x=%d", copyStart)
	}
	if p.hitTestCopyURL(copyStart - 1) {
		t.Fatalf("expected hitTestCopyURL to reject URL field content at x=%d", copyStart-1)
	}
}

func TestRenderOAuthBody_OAuthCodeInputRowRemainsBelowURLRow(t *testing.T) {
	p := New(theme.DefaultDark(), nil, nil, nil)
	p.SetSize(34, 20)
	p.step = stepOAuth
	p.provider = "anthropic"
	p.oauth = "Authorize Anthropic in your browser:"
	p.oauthURL = "https://console.anthropic.com/oauth/authorize?client_id=test-client&response_type=code&redirect_uri=https%3A%2F%2Fconsole.anthropic.com%2Foauth%2Fcode%2Fcallback&scope=org%3Aadmin&state=state-token"
	p.oauthCodeMode = true
	p.oauthPrompt = "Paste the authorization code shown in your browser."

	_ = p.renderOAuthBody(32, false)
	if p.copyURLRowY != 6 {
		t.Fatalf("copyURLRowY = %d, want 6", p.copyURLRowY)
	}
	if p.inputRowY != 10 {
		t.Fatalf("inputRowY = %d, want 10", p.inputRowY)
	}
	pasteStart := 1 + max(p.inputFieldWidth()-pasteIconWidth, 10)
	if got := p.hitTestOAuthCodeIcon(pasteStart); got != focusPaste {
		t.Fatalf("hitTestOAuthCodeIcon(%d) = %v, want %v", pasteStart, got, focusPaste)
	}
}

func TestRenderOAuthBody_OAuthCodeFieldScrollsWithCursor(t *testing.T) {
	p := New(theme.DefaultDark(), nil, nil, nil)
	p.SetSize(22, 20)
	p.step = stepOAuth
	p.provider = "anthropic"
	p.oauthCodeMode = true
	p.oauthPrompt = "Paste the authorization code shown in your browser."
	p.input = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	p.cursor = len(p.input)
	p.btnFocus = focusInput

	lines := p.renderOAuthBody(20, true)
	tailLine := ansi.Strip(lines[p.inputRowY-2])
	if !strings.Contains(tailLine, "QRSTUVWXYZ") {
		t.Fatalf("expected tail of auth code near end cursor, got %q", tailLine)
	}
	if strings.Contains(tailLine, "ABCDEFGHIJ") {
		t.Fatalf("expected leading auth code text to be scrolled off, got %q", tailLine)
	}

	p.cursor = 0
	lines = p.renderOAuthBody(20, true)
	headLine := ansi.Strip(lines[p.inputRowY-2])
	if !strings.Contains(headLine, "ABCDEFGHIJ") {
		t.Fatalf("expected head of auth code near home cursor, got %q", headLine)
	}
}

func TestRenderOAuthBody_OAuthCodeFieldViewportDoesNotJumpOnCursorBlink(t *testing.T) {
	p := New(theme.DefaultDark(), nil, nil, nil)
	p.SetSize(22, 20)
	p.step = stepOAuth
	p.provider = "anthropic"
	p.oauthCodeMode = true
	p.oauthPrompt = "Paste the authorization code shown in your browser."
	p.input = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	p.cursor = len(p.input)
	p.btnFocus = focusInput

	linesVisible := p.renderOAuthBody(20, true)
	linesHidden := p.renderOAuthBody(20, false)

	visibleLine := ansi.Strip(linesVisible[p.inputRowY-2])
	hiddenLine := ansi.Strip(linesHidden[p.inputRowY-2])
	if !strings.Contains(visibleLine, "QRSTUVWXYZ") {
		t.Fatalf("expected tail of auth code with visible cursor, got %q", visibleLine)
	}
	if !strings.Contains(hiddenLine, "QRSTUVWXYZ") {
		t.Fatalf("expected tail of auth code with hidden cursor, got %q", hiddenLine)
	}
	if strings.Contains(hiddenLine, "ABCDEFGHIJ") {
		t.Fatalf("expected hidden-cursor viewport to stay on tail, got %q", hiddenLine)
	}
}
