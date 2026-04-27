package ui

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/session"
	chatpkg "github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/component"
	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/mattn/go-sqlite3"
)

type mockMemoryViewService struct {
	snapshot *forest.ViewSnapshot
}

func (m *mockMemoryViewService) Snapshot(context.Context, forest.ViewSnapshotInput) (*forest.ViewSnapshot, error) {
	if m.snapshot == nil {
		return &forest.ViewSnapshot{}, nil
	}
	return m.snapshot, nil
}

func newMemoryTestApp(t *testing.T) *AppModel {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	mgr := session.NewManager(session.DefaultManagerConfig())
	sess, err := mgr.Create(context.Background(), session.DefaultConfig())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.Switch(sess.ID()); err != nil {
		t.Fatalf("switch session: %v", err)
	}

	cfg := DefaultConfig()
	cfg.ProjectRoot = t.TempDir()

	// Issue #11 Phase 3: Decision is collapsed into Intent. Mock
	// snapshot uses the canonical 5-family taxonomy.
	snapshot := &forest.ViewSnapshot{
		SessionID:        sess.ID(),
		SelectedBranchID: "decision-2",
		Trees: []forest.ViewTree{
			{
				Family: forest.TreeFamilyIntent,
				Label:  "Intent Tree",
				Nodes: []forest.ViewNode{
					{ID: "decision-1", Title: "Baseline"},
					{ID: "decision-2", ParentID: "decision-1", Title: "Timeout Cap"},
				},
			},
			{
				Family: forest.TreeFamilyOutcome,
				Label:  "Outcome Tree",
				Nodes: []forest.ViewNode{
					{ID: "outcome-1", Title: "Pass"},
				},
			},
		},
	}

	return New(context.Background(), cfg, Deps{
		NerdFontsDetected: true,
		SessionManager:    mgr,
		Forest:            &mockMemoryViewService{snapshot: snapshot},
	})
}

func TestAltMTogglesMemoryMode(t *testing.T) {
	app := newMemoryTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 36}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}, Alt: true}
	model, _ := app.Update(key)
	app = model.(*AppModel)

	if app.viewMode != ViewMemory {
		t.Fatalf("view mode = %v, want ViewMemory", app.viewMode)
	}
	if app.focus.Current() != component.FocusChat {
		t.Fatalf("focus = %v, want FocusChat", app.focus.Current())
	}
	if !strings.Contains(app.statusBar.View(), "MEMR") {
		t.Fatalf("status bar = %q, want MEMR", app.statusBar.View())
	}
	if !strings.Contains(app.panelContent(component.FocusChat), "◈") {
		t.Fatalf("memory canvas missing active node: %q", app.panelContent(component.FocusChat))
	}
	if !strings.Contains(app.renderLeftPanel(app.config.Theme()), "Intent Tree") {
		t.Fatalf("left panel missing tree header: %q", app.renderLeftPanel(app.config.Theme()))
	}

	model, _ = app.Update(key)
	app = model.(*AppModel)
	if app.viewMode != ViewChat {
		t.Fatalf("view mode after toggle = %v, want ViewChat", app.viewMode)
	}
}

func TestAltMIndexesChatHistoryIntoForest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mgr := session.NewManager(session.DefaultManagerConfig())
	sess, err := mgr.Create(context.Background(), session.DefaultConfig())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.Switch(sess.ID()); err != nil {
		t.Fatalf("switch session: %v", err)
	}

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("seed nodes table: %v", err)
	}

	// SynchronousProjection makes AppendEvent → branch projection
	// happen inline inside RecordContent, so the immediate Snapshot
	// after toggling memory mode sees every just-ingested chat event.
	// The async projector path is correct production behavior but
	// races the test's expectations within a single tick.
	forestSvc, err := forest.New(forest.Config{DB: db, SynchronousProjection: true})
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	t.Cleanup(func() { _ = forestSvc.Close() })

	cfg := DefaultConfig()
	cfg.ProjectRoot = t.TempDir()

	app := New(context.Background(), cfg, Deps{
		NerdFontsDetected: true,
		SessionManager:    mgr,
		Forest:            forestSvc,
	})
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 36}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:        "user-1",
		Timestamp: time.Unix(100, 0),
		Source:    chatpkg.SourceUser,
		SessionID: sess.ID(),
		Content:   "make retries safer",
		Height:    -1,
	})
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:        "architect-1",
		Timestamp: time.Unix(200, 0),
		Source:    chatpkg.SourceAgent,
		AgentID:   "architect",
		AgentType: "architect",
		SessionID: sess.ID(),
		Content:   "Reuse capped backoff and preserve scope.",
		Height:    -1,
	})

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}, Alt: true}
	model, _ := app.Update(key)
	app = model.(*AppModel)

	canvas := app.panelContent(component.FocusChat)
	if strings.Contains(canvas, "MEMORY FOREST EMPTY") {
		t.Fatalf("memory canvas stayed empty after ingest: %q", canvas)
	}
	if !strings.Contains(canvas, "<⟡>") && !strings.Contains(canvas, "◈") && !strings.Contains(canvas, "⬢") && !strings.Contains(canvas, "⬡") {
		t.Fatalf("memory canvas missing rendered forest structure: %q", canvas)
	}

	left := app.renderLeftPanel(app.config.Theme())
	// Issue #11 Phase 3: user prompts and architect-authored chat
	// entries both route to the Intent tree (architect's prior
	// Decision-family routing was collapsed into Intent).
	if !strings.Contains(left, "Intent Tree") {
		t.Fatalf("left panel missing intent tree: %q", left)
	}
}

func TestAltMIndexesInterAgentConsultKnowledgeIntoForest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mgr := session.NewManager(session.DefaultManagerConfig())
	sess, err := mgr.Create(context.Background(), session.DefaultConfig())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.Switch(sess.ID()); err != nil {
		t.Fatalf("switch session: %v", err)
	}

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("seed nodes table: %v", err)
	}

	// SynchronousProjection makes AppendEvent → branch projection
	// happen inline inside RecordContent, so the immediate Snapshot
	// after toggling memory mode sees every just-ingested chat event.
	// The async projector path is correct production behavior but
	// races the test's expectations within a single tick.
	forestSvc, err := forest.New(forest.Config{DB: db, SynchronousProjection: true})
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	t.Cleanup(func() { _ = forestSvc.Close() })

	cfg := DefaultConfig()
	cfg.ProjectRoot = t.TempDir()

	app := New(context.Background(), cfg, Deps{
		NerdFontsDetected: true,
		SessionManager:    mgr,
		Forest:            forestSvc,
	})
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 36}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:        "architect-1",
		Timestamp: time.Unix(200, 0),
		Source:    chatpkg.SourceAgent,
		AgentID:   "architect",
		AgentType: "architect",
		SessionID: sess.ID(),
		Content:   "Consulting knowledge agents.",
		Height:    -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-knowledge",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					Status:     chatpkg.InterAgentToolDone,
					AgentTypes: []string{"librarian", "academic", "archivalist"},
					Children: []chatpkg.InterAgentChildActivity{
						{
							CorrelationID: "consult-lib-1",
							AgentID:       "librarian",
							AgentType:     "librarian",
							ResultSummary: "Located the existing retry helper in queue workers.",
							Completed:     true,
						},
						{
							CorrelationID: "consult-acad-1",
							AgentID:       "academic",
							AgentType:     "academic",
							ResultSummary: "Backoff with jitter is recommended for saturation control.",
							Completed:     true,
						},
						{
							CorrelationID: "consult-arch-1",
							AgentID:       "archivalist",
							AgentType:     "archivalist",
							ResultSummary: "Prior rollout failed when timeout caps were omitted.",
							Completed:     true,
						},
					},
				},
			},
		},
	})

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}, Alt: true}
	model, _ := app.Update(key)
	app = model.(*AppModel)

	if app.memoryView == nil {
		t.Fatal("memory view was not initialized")
	}
	sections := app.memoryView.Sections()
	flattened := make([]string, 0, 8)
	for _, section := range sections {
		flattened = append(flattened, section.Label)
		for _, entry := range section.Entries {
			flattened = append(flattened, entry.Title)
		}
	}
	rendered := strings.Join(flattened, "\n")
	if !strings.Contains(rendered, "Evidence Tree") {
		t.Fatalf("memory sections missing evidence tree after consult ingest: %q", rendered)
	}
	if !strings.Contains(rendered, "Outcome Tree") {
		t.Fatalf("memory sections missing outcome tree after consult ingest: %q", rendered)
	}
	if !strings.Contains(rendered, "Located the existing retry helper") {
		t.Fatalf("librarian consult summary missing from memory sections: %q", rendered)
	}
	if !strings.Contains(rendered, "Backoff with jitter is recommended") {
		t.Fatalf("academic consult summary missing from memory sections: %q", rendered)
	}
	if !strings.Contains(rendered, "Prior rollout failed when timeout caps were omitted") {
		t.Fatalf("archivalist consult summary missing from memory sections: %q", rendered)
	}
}

func TestMemoryModeEmptyStateFailsLoudly(t *testing.T) {
	app := newMemoryTestApp(t)
	app.deps.Forest = &mockMemoryViewService{snapshot: &forest.ViewSnapshot{}}
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 36}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}, Alt: true}
	model, _ := app.Update(key)
	app = model.(*AppModel)

	canvas := app.panelContent(component.FocusChat)
	if !strings.Contains(canvas, "MEMORY FOREST EMPTY") {
		t.Fatalf("canvas = %q, want explicit empty-state text", canvas)
	}
	left := app.renderLeftPanel(app.config.Theme())
	if !strings.Contains(left, "no indexed branches") {
		t.Fatalf("left panel = %q, want explicit empty-state text", left)
	}
}
