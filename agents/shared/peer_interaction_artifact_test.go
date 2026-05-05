package shared

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

// EmitPeerInteractionStarted contract tests (UI_DESIGN.md §2.4 + §4.1).
// The bridge will pair the eventual completion via Relation{completes}
// once the issued claim closes; here we only verify the started side.

func newAccumulatorContext() (context.Context, *claims.TestamentAccumulator) {
	acc := claims.NewTestamentAccumulator("issuer", "test-session")
	ctx := claims.WithTestamentAccumulator(context.Background(), acc)
	return ctx, acc
}

func TestEmitPeerInteractionStarted_Consult(t *testing.T) {
	ctx, acc := newAccumulatorContext()
	id := EmitPeerInteractionStarted(ctx, PeerInteractionKindConsult, "issuer", "claim-1", "designer", "How should we shape error states?")
	if id == "" {
		t.Fatal("expected non-empty started artifact ID")
	}
	arts := acc.Artifacts()
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	a := arts[0]
	if a.ID != id {
		t.Fatalf("artifact.ID = %q, want %q", a.ID, id)
	}
	if a.Kind != "consult_started" {
		t.Fatalf("artifact.Kind = %q, want consult_started", a.Kind)
	}
	if a.Reference != "designer" {
		t.Fatalf("artifact.Reference = %q, want designer", a.Reference)
	}
	if got := a.Metadata["claim_id"]; got != "claim-1" {
		t.Fatalf("metadata.claim_id = %v, want claim-1", got)
	}
	if got := a.Metadata["target"]; got != "designer" {
		t.Fatalf("metadata.target = %v, want designer", got)
	}
}

func TestEmitPeerInteractionStarted_Challenge(t *testing.T) {
	ctx, acc := newAccumulatorContext()
	id := EmitPeerInteractionStarted(ctx, PeerInteractionKindChallenge, "issuer", "claim-2", "engineer", "Token validation has a timing side-channel.")
	if id == "" {
		t.Fatal("expected non-empty started artifact ID")
	}
	if got := acc.Artifacts()[0].Kind; got != "challenge_started" {
		t.Fatalf("Kind = %q, want challenge_started", got)
	}
}

func TestEmitPeerInteractionStarted_NoAccumulator(t *testing.T) {
	id := EmitPeerInteractionStarted(context.Background(), PeerInteractionKindConsult, "issuer", "claim-1", "designer", "q")
	if id != "" {
		t.Fatalf("expected empty ID without accumulator, got %q", id)
	}
}

func TestEmitPeerInteractionStarted_EmptyClaimID_NoArtifact(t *testing.T) {
	ctx, acc := newAccumulatorContext()
	id := EmitPeerInteractionStarted(ctx, PeerInteractionKindConsult, "issuer", "", "designer", "q")
	if id != "" {
		t.Fatalf("expected empty ID for empty claimID, got %q", id)
	}
	if n := acc.Len(); n != 0 {
		t.Fatalf("expected 0 artifacts, got %d", n)
	}
}
