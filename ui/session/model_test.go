package session

import (
	"context"
	"testing"

	coresession "github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestCreateSessionSwitchesToNewSession(t *testing.T) {
	t.Chdir(t.TempDir())
	mgr := coresession.NewManager(coresession.DefaultManagerConfig())
	t.Cleanup(func() { _ = mgr.Shutdown() })
	cfg := coresession.DefaultConfig()
	cfg.ID = "create-session-switch-old"
	cfg.Name = "old"
	cfg.PersistencePath = t.TempDir()
	defaultSession, err := mgr.Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create default session: %v", err)
	}
	if err := mgr.Switch(defaultSession.ID()); err != nil {
		t.Fatalf("switch default session: %v", err)
	}

	model := New(mgr, theme.New(theme.DarkPalette))
	model.createSession()

	active, ok := mgr.GetActive()
	if !ok || active == nil {
		t.Fatal("expected active session after create")
	}
	if active.ID() == defaultSession.ID() {
		t.Fatalf("active session = %q, want newly-created session", active.ID())
	}
	selected, ok := model.selectedSummary()
	if !ok {
		t.Fatal("expected selected session after create")
	}
	if selected.ID != active.ID() || !selected.Active {
		t.Fatalf("selected session = %+v, active ID = %q", selected, active.ID())
	}
}
