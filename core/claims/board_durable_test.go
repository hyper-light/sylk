package claims

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDurableBoardInMemory(t *testing.T) {
	db, err := OpenDurableBoard(ClaimsBoardConfig{BoardID: "board-1", TaskID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")}); err != nil {
		t.Fatal(err)
	}

	if p := db.Board().Projection(); p.TotalClaims != 1 {
		t.Errorf("TotalClaims: %d", p.TotalClaims)
	}
}

func TestDurableBoardPersistAndRecover(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "session")

	cfg := ClaimsBoardConfig{
		BoardID: "board-1", PipelineID: "pipe-1",
		TaskID: "task-1", SessionID: "ses-1", SessionDir: sessionDir,
	}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	c1 := testClaimWithValidations("c1", "first claim")
	c2 := testClaim("c2", "second claim")
	if err := db.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{c1, c2}); err != nil {
		t.Fatal(err)
	}

	testament := Testament{
		AgentID: "engineer-b", Summary: "done",
		Relations: []Relation{{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
	}
	if err := db.SubmitTestaments(ctx, Action{AgentID: "engineer-b"}, []Testament{testament}); err != nil {
		t.Fatal(err)
	}

	p1 := db.Board().Projection()
	db.Close()

	db2, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	p2 := db2.Board().Projection()
	if p2.TotalClaims != p1.TotalClaims {
		t.Errorf("recover: TotalClaims=%d, want %d", p2.TotalClaims, p1.TotalClaims)
	}
	if p2.TestifiedCount != p1.TestifiedCount {
		t.Errorf("recover: TestifiedCount=%d, want %d", p2.TestifiedCount, p1.TestifiedCount)
	}
}

func TestDurableBoardSnapshotRecovery(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "session")

	cfg := ClaimsBoardConfig{BoardID: "board-1", TaskID: "task-1", SessionDir: sessionDir}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		c := Claim{Title: "claim", Relations: []Relation{
			{Related: "a", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		}}
		db.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{c})
	}

	if err := db.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}

	// Post one more after snapshot.
	db.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{{
		Title: "post-snapshot", Relations: []Relation{
			{Related: "a", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		},
	}})

	p1 := db.Board().Projection()
	db.Close()

	snapshotPath := filepath.Join(sessionDir, "protocols", walNamespace, "board-1", "projection.snapshot.json")
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		t.Fatal("snapshot file should exist")
	}

	db2, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	p2 := db2.Board().Projection()
	if p2.TotalClaims != p1.TotalClaims {
		t.Errorf("snapshot recover: TotalClaims=%d, want %d", p2.TotalClaims, p1.TotalClaims)
	}
}

func TestDurableBoardWALFirstThenMutate(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "session")

	cfg := ClaimsBoardConfig{BoardID: "board-1", TaskID: "task-1", SessionDir: sessionDir}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	db.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")})
	db.Close()

	// Verify WAL file has content.
	walPath := filepath.Join(sessionDir, "protocols", walNamespace, "board-1", "wal", "events.wal.jsonl")
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("WAL file should have content")
	}

	// Recover and verify.
	db2, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if p := db2.Board().Projection(); p.TotalClaims != 1 {
		t.Errorf("recovered TotalClaims: %d, want 1", p.TotalClaims)
	}
}

func TestDurableBoardPhaseTransitionPersistence(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "session")

	cfg := ClaimsBoardConfig{BoardID: "board-1", TaskID: "task-1", SessionDir: sessionDir}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	c := testClaimWithValidations("c1", "claim")
	db.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{c})

	db.SubmitTestaments(ctx, Action{AgentID: "e"}, []Testament{{
		AgentID: "e", Summary: "done",
		Relations: []Relation{{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
	}})
	db.TransitionToValidation(ctx)

	// Evaluate all validations via the board (not durable — validations live on claims).
	claim := db.Board().claims["c1"]
	for _, v := range claim.Validations {
		db.EvaluateValidation(ctx, "c1", v.ID, StatusChange{
			To: string(ValidationStatusPassed), AgentID: "inspector-a",
		})
	}

	db.MarkComplete(ctx)
	db.Close()

	db2, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if db2.Board().Phase() != BoardPhaseComplete {
		t.Errorf("recovered phase: %s, want complete", db2.Board().Phase())
	}
}
