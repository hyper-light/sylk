package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/msg"
	tea "github.com/charmbracelet/bubbletea"
)

func newResizeTestApp(t *testing.T) *AppModel {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.ProjectRoot = t.TempDir()
	return New(context.Background(), cfg, Deps{
		NerdFontsDetected: true,
		SessionManager:    session.NewManager(session.DefaultManagerConfig()),
	})
}

func TestHandleResizeRelayoutsVerticalResizeImmediately(t *testing.T) {
	app := newResizeTestApp(t)

	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 40}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	before := app.chat.ViewportHeight()
	if before <= 0 {
		t.Fatalf("initial chat viewport height = %d, want > 0", before)
	}
	_ = app.View()

	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 48}); cmd != nil {
		t.Fatalf("vertical resize command = %v, want nil", cmd)
	}
	after := app.chat.ViewportHeight()
	if after <= before {
		t.Fatalf("chat viewport height = %d, want > %d", after, before)
	}
	if app.comp.HasCache() {
		t.Fatal("expected compositor cache invalidated after resize")
	}
	if lines := strings.Count(app.View(), "\n") + 1; lines != 48 {
		t.Fatalf("rendered line count = %d, want 48", lines)
	}
}

func TestHandleResizeSkipsDuplicateSize(t *testing.T) {
	app := newResizeTestApp(t)

	size := tea.WindowSizeMsg{Width: 100, Height: 32}
	if cmd := app.handleResize(size); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	_ = app.View()
	version := app.comp.Snapshot().Version
	height := app.chat.ViewportHeight()

	if cmd := app.handleResize(size); cmd != nil {
		t.Fatalf("duplicate resize command = %v, want nil", cmd)
	}
	if app.comp.Snapshot().Version != version {
		t.Fatalf("frame version = %d, want %d", app.comp.Snapshot().Version, version)
	}
	if app.chat.ViewportHeight() != height {
		t.Fatalf("chat viewport height = %d, want %d", app.chat.ViewportHeight(), height)
	}
}

func TestDecorTickWithoutActiveDecorKeepsViewClean(t *testing.T) {
	app := newResizeTestApp(t)

	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	_ = app.View()
	app.viewDirty = false

	_, _ = app.Update(msg.DecorTickMsg{Time: time.Now(), Gen: app.decorGen})
	if app.viewDirty {
		t.Fatal("expected decor tick without active animation to keep the view clean")
	}
}

func TestDecorTickQuiescesDuringResize(t *testing.T) {
	app := newResizeTestApp(t)

	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 40}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	app.chat.BeginThinking("engineer")
	_ = app.View()

	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 39}); cmd != nil {
		t.Fatalf("resize command = %v, want nil", cmd)
	}
	_ = app.View()
	app.viewDirty = false

	_, _ = app.Update(msg.DecorTickMsg{Time: time.Now(), Gen: app.decorGen})
	if app.viewDirty {
		t.Fatal("expected decor animation to stay quiesced during resize")
	}

	app.resizeFreezeUntil = time.Now().Add(-time.Millisecond)
	_, _ = app.Update(msg.DecorTickMsg{Time: time.Now().Add(time.Second), Gen: app.decorGen})
	if !app.viewDirty {
		t.Fatal("expected decor animation to resume after resize quiesce")
	}
}

func TestViewPinsCursorVisibleDuringResize(t *testing.T) {
	app := newResizeTestApp(t)

	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	_ = app.View()

	app.blinkEpoch = time.Now().Add(-blinkHalfPeriod)
	app.cursorVisible = false
	app.lastRenderedPhase = false

	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 31}); cmd != nil {
		t.Fatalf("resize command = %v, want nil", cmd)
	}
	_ = app.View()

	if !app.cursorVisible {
		t.Fatal("expected cursor to stay visible during resize quiesce")
	}
	if !app.lastRenderedPhase {
		t.Fatal("expected last rendered cursor phase to be visible during resize quiesce")
	}
	if app.blinkDirty {
		t.Fatal("expected resize quiesce to suppress blink dirtiness")
	}
}
