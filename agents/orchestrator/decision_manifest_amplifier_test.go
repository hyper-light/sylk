package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/manifest"
)

// TestManifestAmplifier_EmitsDecisionDeclared verifies the manifest
// amplifier emits a fabric activity for every successful Declare. The
// emission is post-commit (durable manifest write is the source of
// truth); the activity carries SourceTable + SourceID so consumers
// can join back to the canonical row.
func TestManifestAmplifier_EmitsDecisionDeclared(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	store := newManifestTestStore(t)
	ctx := context.Background()
	manifest.RegisterDomain(manifest.DomainSpec{
		Name: "test_framework_amp",
		Compatibility: func(a, b string) manifest.Compatibility {
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	declared, err := store.Declare(ctx, "sess-amp", manifest.AgentRef{AgentID: "tester-1", AgentType: "tester-pipeline"}, manifest.DeclareDecisionInput{
		Domain:     "test_framework_amp",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}

	emitted := col.FilterByKind(activity.ActionDecisionDeclared)
	if len(emitted) == 0 {
		t.Fatal("Declare must emit ActionDecisionDeclared")
	}
	last := emitted[len(emitted)-1]
	if last.SourceTable != "decision_manifest" {
		t.Errorf("SourceTable = %q, want decision_manifest", last.SourceTable)
	}
	if last.SourceID != declared.Decision.ID {
		t.Errorf("SourceID = %q, want %q", last.SourceID, declared.Decision.ID)
	}
	if last.Subject.Domain != "test_framework_amp" {
		t.Errorf("Subject.Domain = %q, want test_framework_amp", last.Subject.Domain)
	}
	if last.Confidence != activity.ConfidenceTentative {
		t.Errorf("Confidence = %q, want tentative", last.Confidence)
	}
	if !strings.Contains(string(last.Payload), "pytest") {
		t.Errorf("payload should embed value; got %s", string(last.Payload))
	}
}

// TestManifestAmplifier_EmitsPromotionOnCorroboration verifies the
// promotion path emits an additional decision_promoted activity
// when an Equivalent corroboration drives Tentative → Committed.
func TestManifestAmplifier_EmitsPromotionOnCorroboration(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	store := newManifestTestStore(t)
	ctx := context.Background()
	manifest.RegisterDomain(manifest.DomainSpec{
		Name: "test_framework_promo",
		Compatibility: func(a, b string) manifest.Compatibility {
			if a == b {
				return manifest.CompatibilityEquivalent
			}
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	authorA := manifest.AgentRef{AgentID: "tester-A", AgentType: "tester-pipeline"}
	authorB := manifest.AgentRef{AgentID: "tester-B", AgentType: "tester-pipeline"}

	if _, err := store.Declare(ctx, "sess-promo", authorA, manifest.DeclareDecisionInput{
		Domain:     "test_framework_promo",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	}); err != nil {
		t.Fatalf("Declare A: %v", err)
	}

	declB, err := store.Declare(ctx, "sess-promo", authorB, manifest.DeclareDecisionInput{
		Domain:     "test_framework_promo",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	})
	if err != nil {
		t.Fatalf("Declare B: %v", err)
	}
	if declB.Conflict == nil || declB.Conflict.Kind != manifest.ConflictEquivalent {
		t.Fatalf("expected Equivalent conflict; got %+v", declB.Conflict)
	}

	if col.CountByKind(activity.ActionDecisionDeclared) != 2 {
		t.Fatalf("expected 2 ActionDecisionDeclared activities (one per Declare), got %d", col.CountByKind(activity.ActionDecisionDeclared))
	}
	if col.CountByKind(activity.ActionDecisionPromoted) != 1 {
		t.Fatalf("expected 1 ActionDecisionPromoted activity, got %d", col.CountByKind(activity.ActionDecisionPromoted))
	}
}

// TestManifestAmplifier_EmissionFailureDoesNotBlockManifest verifies
// the amplifier is genuinely additive: even if the fabric sink is
// nil/discard, the manifest write still succeeds. The fabric is a
// secondary substrate; failure to emit must never block the primary
// store.
func TestManifestAmplifier_EmissionFailureDoesNotBlockManifest(t *testing.T) {
	prev := activity.SetDefaultSink(nil) // discard sink
	defer activity.SetDefaultSink(prev)

	store := newManifestTestStore(t)
	ctx := context.Background()
	manifest.RegisterDomain(manifest.DomainSpec{
		Name: "test_framework_discard",
		Compatibility: func(a, b string) manifest.Compatibility {
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	if _, err := store.Declare(ctx, "sess-discard", manifest.AgentRef{AgentID: "x", AgentType: "tester-pipeline"}, manifest.DeclareDecisionInput{
		Domain:     "test_framework_discard",
		Scope:      manifest.Scope{},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	}); err != nil {
		t.Fatalf("Declare must succeed under discard sink: %v", err)
	}
}
