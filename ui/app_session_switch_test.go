package ui

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/msg"
)

func TestAppSessionSwitchedClearsSessionLocalChatAndDialogs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	mgr := session.NewManager(session.DefaultManagerConfig())
	t.Cleanup(func() { _ = mgr.Shutdown() })
	oldCfg := session.DefaultConfig()
	oldCfg.ID = "app-switch-old"
	oldCfg.Name = "old"
	oldCfg.PersistencePath = t.TempDir()
	defaultSession, err := mgr.Create(context.Background(), oldCfg)
	if err != nil {
		t.Fatalf("create old session: %v", err)
	}
	nextCfg := session.DefaultConfig()
	nextCfg.ID = "app-switch-new"
	nextCfg.Name = "new"
	nextCfg.PersistencePath = oldCfg.PersistencePath
	nextSession, err := mgr.Create(context.Background(), nextCfg)
	if err != nil {
		t.Fatalf("create next session: %v", err)
	}
	if err := mgr.Switch(defaultSession.ID()); err != nil {
		t.Fatalf("switch default session: %v", err)
	}

	cfg := DefaultConfig()
	cfg.ProjectRoot = t.TempDir()
	app := New(context.Background(), cfg, Deps{
		NerdFontsDetected: true,
		SessionManager:    mgr,
	})
	app.chat.PushEntry(&chat.ChatEntry{
		ID:        "old-entry",
		Source:    chat.SourceAgent,
		SessionID: defaultSession.ID(),
		Content:   "old session content",
	})
	app.planApproval = &planApprovalState{}
	app.planApprovalQ = []*planapproval.Proposal{{PlanID: "queued-old-plan"}}

	if err := mgr.Switch(nextSession.ID()); err != nil {
		t.Fatalf("switch next session: %v", err)
	}
	model, _ := app.Update(msg.SessionSwitchedMsg{
		PreviousID: defaultSession.ID(),
		ActiveID:   nextSession.ID(),
	})
	app = model.(*AppModel)

	if entries := app.chat.SnapshotEntries(); len(entries) != 0 {
		t.Fatalf("chat entries after session switch = %d, want 0: %+v", len(entries), entries)
	}
	if app.planApproval != nil || len(app.planApprovalQ) != 0 {
		t.Fatalf("plan approval state leaked across session switch: active=%v queued=%d", app.planApproval != nil, len(app.planApprovalQ))
	}
}

func TestAppSkipsOldSessionChatMessagesAfterSwitch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	mgr := session.NewManager(session.DefaultManagerConfig())
	t.Cleanup(func() { _ = mgr.Shutdown() })
	oldCfg := session.DefaultConfig()
	oldCfg.ID = "app-filter-old"
	oldCfg.Name = "old"
	oldCfg.PersistencePath = t.TempDir()
	defaultSession, err := mgr.Create(context.Background(), oldCfg)
	if err != nil {
		t.Fatalf("create old session: %v", err)
	}
	nextCfg := session.DefaultConfig()
	nextCfg.ID = "app-filter-new"
	nextCfg.Name = "new"
	nextCfg.PersistencePath = oldCfg.PersistencePath
	nextSession, err := mgr.Create(context.Background(), nextCfg)
	if err != nil {
		t.Fatalf("create next session: %v", err)
	}
	if err := mgr.Switch(nextSession.ID()); err != nil {
		t.Fatalf("switch next session: %v", err)
	}

	cfg := DefaultConfig()
	cfg.ProjectRoot = t.TempDir()
	app := New(context.Background(), cfg, Deps{
		NerdFontsDetected: true,
		SessionManager:    mgr,
	})

	model, _ := app.Update(msg.ClaimPresentationMsg{
		SessionID:  defaultSession.ID(),
		CycleID:    "old-cycle",
		SourceType: "artifact",
		SourceID:   "old-plan",
		Title:      "Plan",
		Content:    "old session plan",
		Format:     "markdown",
	})
	app = model.(*AppModel)
	if entries := app.chat.SnapshotEntries(); len(entries) != 0 {
		t.Fatalf("old-session presentation reached chat: %+v", entries)
	}

	model, _ = app.Update(msg.ClaimPresentationMsg{
		SessionID:  nextSession.ID(),
		CycleID:    "new-cycle",
		SourceType: "artifact",
		SourceID:   "new-plan",
		Title:      "Plan",
		Content:    "new session plan",
		Format:     "markdown",
	})
	app = model.(*AppModel)
	if entries := app.chat.SnapshotEntries(); len(entries) != 1 || entries[0].SessionID != nextSession.ID() {
		t.Fatalf("new-session presentation not routed to chat: %+v", entries)
	}
}
