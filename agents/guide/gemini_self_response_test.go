package guide

import (
	"context"
	"strings"
	"testing"
)

func TestEmitGuideThoughtUsesContextEmitter(t *testing.T) {
	var captured string
	ctx := withGuideThoughtEmitter(context.Background(), func(thought string) {
		captured = thought
	})

	emitGuideThought(ctx, "  drafted approach  ")

	if captured != "drafted approach" {
		t.Fatalf("captured = %q, want %q", captured, "drafted approach")
	}
}

func TestAppendThoughtDeltaAccumulatesFragments(t *testing.T) {
	var thought strings.Builder

	if got := appendThoughtDelta(&thought, "Clar"); got != "Clar" {
		t.Fatalf("appendThoughtDelta(Clar) = %q, want %q", got, "Clar")
	}
	if got := appendThoughtDelta(&thought, "ifying questions"); got != "Clarifying questions" {
		t.Fatalf("appendThoughtDelta(fragment) = %q, want %q", got, "Clarifying questions")
	}
	if got := appendThoughtDelta(&thought, "."); got != "Clarifying questions." {
		t.Fatalf("appendThoughtDelta(period) = %q, want %q", got, "Clarifying questions.")
	}
}
