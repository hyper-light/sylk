package ui

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/msg"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTokenCounterAccumulatesAcrossStreamsAndRestarts(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	_, _ = app.Update(msg.StreamStartMsg{CorrelationID: "corr-1", AgentID: "architect", AgentType: "architect"})
	_, _ = app.Update(msg.StreamCompleteMsg{CorrelationID: "corr-1", InputTokens: 50, OutputTokens: 20})
	if got := app.statusBar.View(); !strings.Contains(got, "↓50/↑20") {
		t.Fatalf("status bar after first stream = %q, want first stream totals", got)
	}

	_, _ = app.Update(msg.StreamStartMsg{CorrelationID: "corr-2", AgentID: "librarian", AgentType: "librarian"})
	_, _ = app.Update(msg.StreamChunkMsg{CorrelationID: "corr-2", Text: "abcd"})
	if got := app.statusBar.View(); !strings.Contains(got, "↓50/↑21") {
		t.Fatalf("status bar after second stream chunk = %q, want accumulated totals", got)
	}

	// Retry / fallback within the same logical stream must keep accumulating.
	_, _ = app.Update(msg.StreamStartMsg{CorrelationID: "corr-2", AgentID: "librarian", AgentType: "librarian"})
	_, _ = app.Update(msg.StreamChunkMsg{CorrelationID: "corr-2", Text: "abcd"})
	if got := app.statusBar.View(); !strings.Contains(got, "↓50/↑22") {
		t.Fatalf("status bar after restarted stream chunk = %q, want accumulated totals", got)
	}
}

func TestTokenCounterPrefersHigherLiveStreamTotalsOverLaggingBusTotals(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.totalPromptTokens = 80
	app.totalCompletionTokens = 35
	app.busInputTokens = 40
	app.busOutputTokens = 10
	app.updateTokenDisplay()

	if got := app.statusBar.View(); !strings.Contains(got, "↓80/↑35") {
		t.Fatalf("status bar with lagging bus totals = %q, want live stream totals", got)
	}

	app.busInputTokens = 120
	app.busOutputTokens = 60
	app.updateTokenDisplay()
	if got := app.statusBar.View(); !strings.Contains(got, "↓120/↑60") {
		t.Fatalf("status bar with higher bus totals = %q, want higher totals reflected", got)
	}
}
