package session_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/session"
)

func TestManager_CreateWiresDurableSessionBoard(t *testing.T) {
	root := t.TempDir()
	mgr := session.NewManager(session.DefaultManagerConfig())
	s, err := mgr.Create(context.Background(), session.Config{
		ID:                 "durable-session",
		Name:               "durable-session",
		PersistenceEnabled: true,
		PersistencePath:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	board := s.ClaimsBoard()
	if board == nil {
		t.Fatal("session claims board is nil")
	}
	if err := board.PostAction(context.Background(), claims.Action{AgentID: "guide", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:          "claim-1",
		AgentID:     "guide",
		Title:       "Persist root claim",
		Description: "Verify the session root board is WAL-backed.",
		Relations: []claims.Relation{{
			Related:      "guide",
			RelatedType:  claims.RelatedTypeAgent,
			Relationship: claims.RelationshipIssuer,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Close(s.ID()); err != nil {
		t.Fatal(err)
	}

	walPath := filepath.Join(root, "durable-session", "protocols", "claims_board", "session-durable-session", "wal", "events.wal.jsonl")
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("claims WAL is empty")
	}
}
