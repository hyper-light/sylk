package knowledgeruntime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

type fakeClaimsKnowledgeWriter struct {
	requests []*TextDocumentIngestRequest
}

func (f *fakeClaimsKnowledgeWriter) UpsertTextDocument(_ context.Context, req *TextDocumentIngestRequest) error {
	f.requests = append(f.requests, req)
	return nil
}

func TestClaimsKnowledgeMirror_ProjectsClaimDocument(t *testing.T) {
	writer := &fakeClaimsKnowledgeWriter{}
	db, err := claims.OpenDurableBoard(claims.ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []claims.ClaimsProjector{
			NewClaimsKnowledgeMirror(writer),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Board().PostAction(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:          "claim-1",
		AgentID:     "architect",
		Title:       "Carry forward planning evidence",
		Description: "Persist claim and testament material for the next turn.",
		Relations: []claims.Relation{{
			Related:      "architect",
			RelatedType:  claims.RelatedTypeAgent,
			Relationship: claims.RelationshipIssuer,
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	var found *TextDocumentIngestRequest
	for _, req := range writer.requests {
		if req.Metadata["entity_type"] == "claim" && req.Metadata["claim_id"] == "claim-1" {
			found = req
			break
		}
	}
	if found == nil {
		t.Fatalf("claim document not projected; got %d requests", len(writer.requests))
	}
	if found.DocumentID == "" || found.Path == "" {
		t.Fatalf("document identity missing: id=%q path=%q", found.DocumentID, found.Path)
	}
	if found.Metadata["source"] != "claims_board" {
		t.Fatalf("source metadata = %q, want claims_board", found.Metadata["source"])
	}
}
