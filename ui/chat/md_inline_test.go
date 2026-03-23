package chat

import (
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestSplitTextIntoRuns_StylesPriorityBadges(t *testing.T) {
	th := theme.DefaultDark()
	styles := newChatMdStyles(th, th.AgentMessage)

	runs := splitTextIntoRuns("[must] ship it", styles.text, styles)
	if len(runs) < 3 {
		t.Fatalf("expected multiple runs, got %d", len(runs))
	}
	if runs[0].text != "[must]" {
		t.Fatalf("first run text = %q, want [must]", runs[0].text)
	}
	if !runs[0].atomic {
		t.Fatal("expected priority badge run to be atomic")
	}
	if runs[0].width <= len("[must]") {
		t.Fatalf("expected padded badge width > %d, got %d", len("[must]"), runs[0].width)
	}
}
