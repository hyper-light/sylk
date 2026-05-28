package claims

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestFixtureClaimPassesBoardValidationAndDurableReplay(t *testing.T) {
	dir := t.TempDir()
	cfg := ClaimsBoardConfig{BoardID: "artifact-board", TaskID: "task", SessionID: "session", SessionDir: filepath.Join(dir, "session")}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("open durable: %v", err)
	}
	ctx := context.Background()
	claim := fixtureClaimWithTypedValidation()
	if err := db.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}); err != nil {
		t.Fatalf("post action: %v", err)
	}
	testament := fixtureMultiArtifactTestament(t, DefaultTypeRegistry())
	if _, err := db.GenerateTestamentAction(ctx, Action{AgentID: "engineer", Type: ActionTypeTestament}, []Testament{testament}, GenerateTestamentActionOptions{}); err != nil {
		t.Fatalf("generate testament: %v", err)
	}
	artifactID := testament.Artifacts[0].ID
	stored, ok := db.Board().CloneArtifact(artifactID)
	if !ok {
		t.Fatalf("artifact %s not stored", artifactID)
	}
	if stored.Status != ArtifactStatusGenerated || stored.ClaimID != artifactFixtureClaimID || stored.ContentHash == "" {
		t.Fatalf("stored artifact = %+v", stored)
	}
	db.Close()

	reopened, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("reopen durable: %v", err)
	}
	defer reopened.Close()
	replayed, ok := reopened.Board().CloneArtifact(artifactID)
	if !ok {
		t.Fatalf("artifact %s not replayed", artifactID)
	}
	if replayed.Status != stored.Status || replayed.ContentHash != stored.ContentHash || len(replayed.StatusHistory) != len(stored.StatusHistory) {
		t.Fatalf("replayed artifact mismatch: got %+v want %+v", replayed, stored)
	}
	value, err := ArtifactData[PlanMarkdownArtifactData](replayed)
	if err != nil {
		t.Fatalf("typed data after replay: %v", err)
	}
	if value.Markdown != "# Plan" {
		t.Fatalf("replayed typed value = %+v", value)
	}
}

func TestConcurrentProjectionClonesDoNotExposeArtifactOrValidationSlices(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "projection-board", TaskID: "task", SessionID: "session"})
	ctx := context.Background()
	claim := fixtureClaimWithTypedValidation()
	if err := board.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}); err != nil {
		t.Fatalf("post action: %v", err)
	}
	testament := fixtureMultiArtifactTestament(t, DefaultTypeRegistry())
	if _, err := board.GenerateTestamentAction(ctx, Action{AgentID: "engineer", Type: ActionTypeTestament}, []Testament{testament}, GenerateTestamentActionOptions{}); err != nil {
		t.Fatalf("generate testament: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			proj := board.Projection()
			if len(proj.Claims) == 0 || len(proj.Testaments) == 0 {
				t.Errorf("missing projection data")
				return
			}
			proj.Claims[0].Validations[0].StatusHistory = append(proj.Claims[0].Validations[0].StatusHistory, StatusChange{Reason: "mutated"})
			proj.Testaments[0].Artifacts[0].StatusHistory = append(proj.Testaments[0].Artifacts[0].StatusHistory, StatusChange{Reason: "mutated"})
		}()
	}
	wg.Wait()

	claimClone, ok := board.CloneClaim(artifactFixtureClaimID)
	if !ok {
		t.Fatal("claim clone missing")
	}
	if len(claimClone.Validations[0].StatusHistory) != 1 {
		t.Fatalf("board validation history mutated through projection: %+v", claimClone.Validations[0].StatusHistory)
	}
	artifactClone, ok := board.CloneArtifact(testament.Artifacts[0].ID)
	if !ok {
		t.Fatal("artifact clone missing")
	}
	if len(artifactClone.StatusHistory) != 1 {
		t.Fatalf("board artifact history mutated through projection: %+v", artifactClone.StatusHistory)
	}
}
