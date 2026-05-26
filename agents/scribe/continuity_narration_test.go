package scribe

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
)

func TestScribeContinuityNarrationLinksSourceAndStoresArchivalistMetadata(t *testing.T) {
	sessionID := "scribe-continuity-" + time.Now().Format("150405.000000000")
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-" + sessionID,
		SessionID: sessionID,
		TaskID:    "task-1",
	})
	if err := claims.DefaultSessionBoardRegistry().Register(sessionID, board); err != nil {
		t.Fatal(err)
	}
	defer claims.DefaultSessionBoardRegistry().Remove(sessionID)

	if err := board.PostAction(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:          "source-claim",
		AgentID:     "architect",
		Title:       "Plan CLI",
		Description: "Collect reusable planning evidence.",
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := board.SubmitTestaments(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:    "architect",
		Summary:    "Planning evidence collected.",
		Confidence: "high",
		Relations: []claims.Relation{
			{Related: "source-claim", RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
		},
		Artifacts: []*claims.Artifact{{
			AgentID:   "architect",
			Kind:      "workspace_read",
			Reference: "pyproject.toml already exposes a console script entry point.",
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	carry, err := claims.CarryForward(context.Background(), board, claims.CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := newMockBus()
	s := &Scribe{
		id:              "scribe-architect-test",
		parentAgentType: "architect",
		sessionID:       sessionID,
		bus:             bus,
	}
	s.running.Store(true)

	s.Receive(context.Background(), activity.AgentActivity{
		SessionID: activity.SessionID(sessionID),
		Actor: activity.Actor{
			AgentID:   "architect",
			AgentType: "architect",
		},
		Action:     activity.ActionTestamentSubmitted,
		Resolution: activity.ResolutionCoarse,
		Subject: activity.Subject{Coordinates: map[string]string{
			"testament_id": carry.TestamentID,
		}},
	})

	if bus.publishCount() != 1 {
		t.Fatalf("archivalist publish count = %d, want 1", bus.publishCount())
	}
	msg := bus.lastMessage()
	if msg == nil || msg.Metadata["continuity_testament_id"] != carry.TestamentID {
		t.Fatalf("archivalist message missing continuity metadata: %#v", msg)
	}
	found := false
	for _, projected := range board.Projection().Testaments {
		tst, ok := board.CloneTestament(projected.ID)
		if !ok || tst.AgentID != "scribe-architect" {
			continue
		}
		if !claims.HasRelation(tst.Relations, claims.RelationshipDerivedFrom, carry.TestamentID) {
			continue
		}
		found = true
		if !testamentHasArtifactKind(tst, scribeArtifactContinuityNarration) {
			t.Fatalf("scribe continuity testament missing narration artifact: %+v", tst.Artifacts)
		}
		if !testamentHasArtifactKind(tst, scribeArtifactArchivalistReceipt) {
			t.Fatalf("scribe continuity testament missing archivalist receipt: %+v", tst.Artifacts)
		}
		if !strings.Contains(tst.Summary, "python cli plan") {
			t.Fatalf("scribe summary missing topic: %q", tst.Summary)
		}
	}
	if !found {
		t.Fatalf("did not find scribe continuity narration derived from %s", carry.TestamentID)
	}
}

func TestScribeContinuityNarrationRecordsArchivalistFailureAsArtifact(t *testing.T) {
	sessionID := "scribe-continuity-fail-" + time.Now().Format("150405.000000000")
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "board-" + sessionID, SessionID: sessionID})
	if err := claims.DefaultSessionBoardRegistry().Register(sessionID, board); err != nil {
		t.Fatal(err)
	}
	defer claims.DefaultSessionBoardRegistry().Remove(sessionID)
	if err := board.PostAction(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTask}, []claims.Claim{{ID: "source-claim", Title: "Evidence"}}); err != nil {
		t.Fatal(err)
	}
	if err := board.SubmitTestaments(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID: "architect",
		Summary: "Evidence collected.",
		Relations: []claims.Relation{
			{Related: "source-claim", RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
		},
		Artifacts: []*claims.Artifact{{Kind: "workspace_read", Reference: "Existing CLI code path."}},
	}}); err != nil {
		t.Fatal(err)
	}
	carry, err := claims.CarryForward(context.Background(), board, claims.CarryForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Scribe{id: "scribe-architect-test", parentAgentType: "architect", sessionID: sessionID}
	s.running.Store(true)
	s.Receive(context.Background(), activity.AgentActivity{
		SessionID:  activity.SessionID(sessionID),
		Actor:      activity.Actor{AgentID: "architect", AgentType: "architect"},
		Action:     activity.ActionTestamentSubmitted,
		Resolution: activity.ResolutionCoarse,
		Subject:    activity.Subject{Coordinates: map[string]string{"testament_id": carry.TestamentID}},
	})

	for _, projected := range board.Projection().Testaments {
		tst, ok := board.CloneTestament(projected.ID)
		if !ok || tst.AgentID != "scribe-architect" || !claims.HasRelation(tst.Relations, claims.RelationshipDerivedFrom, carry.TestamentID) {
			continue
		}
		if !testamentHasArtifactKind(tst, claims.ArtifactKindErrorDiagnostic) {
			t.Fatalf("scribe continuity testament should preserve archivalist failure as error artifact: %+v", tst.Artifacts)
		}
		return
	}
	t.Fatalf("did not find scribe continuity narration derived from %s", carry.TestamentID)
}

func testamentHasArtifactKind(t *claims.Testament, kind string) bool {
	for _, artifact := range t.Artifacts {
		if artifact != nil && artifact.Kind == kind {
			return true
		}
	}
	return false
}
