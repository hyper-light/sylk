package shared

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

func TestAttachCausedByFromContext_NoParent_NoMutation(t *testing.T) {
	rel := []claims.Relation{
		{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
	}
	got := AttachCausedByFromContext(context.Background(), rel)
	if len(got) != 1 {
		t.Fatalf("expected unchanged 1 relation, got %d", len(got))
	}
}

func TestAttachCausedByFromContext_WithParent_AppendsRelation(t *testing.T) {
	ctx := claims.WithParentClaimID(context.Background(), "parent-claim-1")
	rel := []claims.Relation{
		{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
	}
	got := AttachCausedByFromContext(ctx, rel)
	if len(got) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(got))
	}
	if got[1].Relationship != claims.RelationshipCausedBy {
		t.Fatalf("expected caused_by relation, got %q", got[1].Relationship)
	}
	if got[1].Related != "parent-claim-1" {
		t.Fatalf("expected related=parent-claim-1, got %q", got[1].Related)
	}
}

func TestAttachCausedByFromContext_Idempotent(t *testing.T) {
	ctx := claims.WithParentClaimID(context.Background(), "parent-claim-1")
	rel := []claims.Relation{
		{Related: "parent-claim-1", RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipCausedBy},
	}
	got := AttachCausedByFromContext(ctx, rel)
	if len(got) != 1 {
		t.Fatalf("expected idempotent (1 relation), got %d", len(got))
	}
}

func TestAttachHandoffFrom_NoPredecessor_NoMutation(t *testing.T) {
	rel := []claims.Relation{
		{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
	}
	got := AttachHandoffFrom(rel, "")
	if len(got) != 1 {
		t.Fatalf("expected unchanged 1 relation, got %d", len(got))
	}
	got = AttachHandoffFrom(rel, "   ")
	if len(got) != 1 {
		t.Fatalf("whitespace predecessorID should be no-op, got %d relations", len(got))
	}
}

func TestAttachHandoffFrom_AppendsRelation(t *testing.T) {
	rel := []claims.Relation{
		{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
	}
	got := AttachHandoffFrom(rel, "predecessor-cycle-root")
	if len(got) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(got))
	}
	if got[1].Relationship != claims.RelationshipHandoffFrom {
		t.Fatalf("expected handoff_from, got %q", got[1].Relationship)
	}
	if got[1].Related != "predecessor-cycle-root" {
		t.Fatalf("expected related=predecessor-cycle-root, got %q", got[1].Related)
	}
	if got[1].RelatedType != claims.RelatedTypeClaim {
		t.Fatalf("expected RelatedType=claim, got %q", got[1].RelatedType)
	}
}

func TestPredecessorCycleRootClaimID_FromCtx(t *testing.T) {
	ctx := claims.WithParentClaimID(context.Background(), "cycle-root-1")
	if got := predecessorCycleRootClaimID(ctx); got != "cycle-root-1" {
		t.Fatalf("predecessorCycleRootClaimID = %q, want cycle-root-1", got)
	}
	if got := predecessorCycleRootClaimID(context.Background()); got != "" {
		t.Fatalf("expected empty without parent, got %q", got)
	}
	if got := predecessorCycleRootClaimID(nil); got != "" {
		t.Fatalf("expected empty for nil ctx, got %q", got)
	}
}
