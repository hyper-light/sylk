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

func TestTokenCounterAddsBackgroundBusTotalsToStreamedSessionTotals(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.totalPromptTokens = 80
	app.totalCompletionTokens = 35
	_, _ = app.Update(msg.TokenUsageMsg{
		AgentID:         "guardian",
		InputTokens:     40,
		OutputTokens:    10,
		CacheReadTokens: 0,
	})
	if got := app.statusBar.View(); !strings.Contains(got, "↓120/↑45") {
		t.Fatalf("status bar with background bus totals = %q, want summed totals", got)
	}
}

func TestTokenCounterPreservesSessionTotalsAcrossAgentTransitionsSharingCorrelation(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	sharedCID := "corr-shared"
	architectText := strings.Repeat("a", 200) // ~50 tokens with fallback counter.

	_, _ = app.Update(msg.StreamStartMsg{
		CorrelationID: sharedCID,
		AgentID:       "architect",
		AgentType:     "architect",
	})
	_, _ = app.Update(msg.StreamChunkMsg{
		CorrelationID: sharedCID,
		Text:          architectText,
		InputTokens:   100,
	})
	if got := app.statusBar.View(); !strings.Contains(got, "↓100/↑50") {
		t.Fatalf("status bar after architect stream = %q, want architect totals", got)
	}

	_, _ = app.Update(msg.StreamStartMsg{
		CorrelationID: sharedCID,
		AgentID:       "engineer",
		AgentType:     "engineer",
	})
	_, _ = app.Update(msg.StreamCompleteMsg{
		CorrelationID: sharedCID,
		AgentID:       "engineer",
		AgentType:     "engineer",
		InputTokens:   20,
		OutputTokens:  5,
	})

	if got := app.statusBar.View(); !strings.Contains(got, "↓120/↑55") {
		t.Fatalf("status bar after shared-correlation agent transition = %q, want cumulative totals preserved", got)
	}
}

func TestTokenCounterDoesNotDoubleCountBusUsageForActiveStream(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	_, _ = app.Update(msg.StreamStartMsg{
		CorrelationID: "corr-live",
		AgentID:       "architect",
		AgentType:     "architect",
	})
	_, _ = app.Update(msg.TokenUsageMsg{
		AgentID:      "architect",
		InputTokens:  50,
		OutputTokens: 20,
	})
	_, _ = app.Update(msg.StreamCompleteMsg{
		CorrelationID: "corr-live",
		AgentID:       "architect",
		AgentType:     "architect",
		InputTokens:   50,
		OutputTokens:  20,
	})

	if got := app.statusBar.View(); !strings.Contains(got, "↓50/↑20") {
		t.Fatalf("status bar after overlapping stream+bus usage = %q, want no double count", got)
	}
}
