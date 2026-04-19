package orchestrator

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/manifest"
)

func newManifestTestStore(t *testing.T) *DecisionManifestStore {
	t.Helper()
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate orchestrator store: %v", err)
	}
	ms, err := NewDecisionManifestStore(store.db)
	if err != nil {
		t.Fatalf("NewDecisionManifestStore: %v", err)
	}
	if err := ms.Migrate(); err != nil {
		t.Fatalf("Migrate decision manifest: %v", err)
	}
	return ms
}

func TestDecisionManifest_DeclareThenQueryReturnsTheValue(t *testing.T) {
	store := newManifestTestStore(t)
	ctx := context.Background()
	author := manifest.AgentRef{AgentID: "tester-pipeline-1", AgentType: "tester-pipeline"}

	// Register the test_framework domain so compatibility predicate exists.
	manifest.RegisterDomain(manifest.DomainSpec{
		Name:                  "test_framework",
		RecommendedDimensions: []manifest.Dimension{manifest.DimensionLanguage},
		Compatibility: func(a, b string) manifest.Compatibility {
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	declared, err := store.Declare(ctx, "sess-1", author, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
		Evidence:   []string{"forest hint"},
	})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if declared.Conflict != nil {
		t.Fatalf("first declaration should have no conflict, got %+v", declared.Conflict)
	}

	q, err := store.Query(ctx, "sess-1", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope: manifest.Scope{
			manifest.DimensionLanguage: "python",
			manifest.DimensionPath:     "tests/test_init.py",
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if q.Winner == nil || q.Winner.Value != "pytest" {
		t.Fatalf("Winner = %+v, want pytest", q.Winner)
	}
}

// TestDecisionManifest_TwoTestersConvergeOnSameValue mirrors the screenshot
// scenario: parallel pipeline testers each declaring a test framework. The
// second declaration with the same value must be detected as Equivalent
// and promote the existing decision toward Consensus, not produce a
// duplicate or a spurious conflict.
func TestDecisionManifest_TwoTestersConvergeOnSameValue(t *testing.T) {
	store := newManifestTestStore(t)
	ctx := context.Background()

	manifest.RegisterDomain(manifest.DomainSpec{
		Name: "test_framework",
		Compatibility: func(a, b string) manifest.Compatibility {
			if a == b {
				return manifest.CompatibilityEquivalent
			}
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	pipelineA := manifest.AgentRef{AgentID: "tester-A", AgentType: "tester-pipeline"}
	pipelineB := manifest.AgentRef{AgentID: "tester-B", AgentType: "tester-pipeline"}

	declA, err := store.Declare(ctx, "sess-converge", pipelineA, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	})
	if err != nil {
		t.Fatalf("Declare A: %v", err)
	}
	if declA.Decision.Confidence != manifest.ConfidenceTentative {
		t.Fatalf("A confidence = %s, want tentative", declA.Decision.Confidence)
	}

	declB, err := store.Declare(ctx, "sess-converge", pipelineB, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	})
	if err != nil {
		t.Fatalf("Declare B: %v", err)
	}
	if declB.Conflict == nil {
		t.Fatal("expected an Equivalent conflict on independent corroboration, got nil")
	}
	if declB.Conflict.Kind != manifest.ConflictEquivalent {
		t.Fatalf("conflict.Kind = %s, want equivalent", declB.Conflict.Kind)
	}
	if declB.Conflict.Existing == nil || declB.Conflict.Existing.Confidence != manifest.ConfidenceCommitted {
		t.Fatalf("existing decision should have been promoted Tentative→Committed, got %+v", declB.Conflict.Existing)
	}

	// Now a query for the same scope should resolve to pytest with the
	// promoted Committed confidence.
	q, err := store.Query(ctx, "sess-converge", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  manifest.Scope{manifest.DimensionLanguage: "python"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if q.Winner == nil || q.Winner.Value != "pytest" {
		t.Fatalf("Winner = %+v, want pytest", q.Winner)
	}
}

// TestDecisionManifest_TwoTestersIncompatibleValuesProduceConflict is the
// inverse: two parallel testers picking incompatible frameworks. The
// system must surface this so the second tester can adopt or challenge.
func TestDecisionManifest_TwoTestersIncompatibleValuesProduceConflict(t *testing.T) {
	store := newManifestTestStore(t)
	ctx := context.Background()

	manifest.RegisterDomain(manifest.DomainSpec{
		Name: "test_framework",
		Compatibility: func(a, b string) manifest.Compatibility {
			if a == b {
				return manifest.CompatibilityEquivalent
			}
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	pipelineA := manifest.AgentRef{AgentID: "tester-A", AgentType: "tester-pipeline"}
	pipelineB := manifest.AgentRef{AgentID: "tester-B", AgentType: "tester-pipeline"}

	if _, err := store.Declare(ctx, "sess-clash", pipelineA, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	}); err != nil {
		t.Fatalf("Declare A: %v", err)
	}

	declB, err := store.Declare(ctx, "sess-clash", pipelineB, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "unittest",
		Confidence: manifest.ConfidenceCommitted,
	})
	if err != nil {
		t.Fatalf("Declare B: %v", err)
	}
	if declB.Conflict == nil || declB.Conflict.Kind != manifest.ConflictIncompatible {
		t.Fatalf("expected ConflictIncompatible, got %+v", declB.Conflict)
	}
	// Confidence MUST be downgraded to Tentative when the declaration
	// conflicts — the agent cannot commit on a contested value.
	if declB.Decision.Confidence != manifest.ConfidenceTentative {
		t.Fatalf("B confidence = %s, want forced-back to tentative on incompatibility", declB.Decision.Confidence)
	}

	// Query: the first-mover (A) wins the resolution because of policy.
	q, err := store.Query(ctx, "sess-clash", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  manifest.Scope{manifest.DimensionLanguage: "python"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if q.Winner == nil || q.Winner.Value != "pytest" {
		t.Fatalf("Winner = %+v, want pytest (first-mover)", q.Winner)
	}
}

// TestDecisionManifest_TentativeCacheGateExpiresEntries verifies the
// Ristretto TTL gate: a Tentative entry stops appearing in queries once
// its cache entry has been evicted. Promoted (Committed / Consensus)
// entries bypass the gate and remain visible regardless of cache state.
// This replaces the old reaper-goroutine path with cache-driven expiry.
func TestDecisionManifest_TentativeCacheGateExpiresEntries(t *testing.T) {
	store := newManifestTestStore(t)
	ctx := context.Background()

	manifest.RegisterDomain(manifest.DomainSpec{
		Name: "test_framework",
		Compatibility: func(a, b string) manifest.Compatibility {
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	author := manifest.AgentRef{AgentID: "tester-1", AgentType: "tester-pipeline"}

	tentative, err := store.Declare(ctx, "sess-cache", author, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	})
	if err != nil {
		t.Fatalf("Declare tentative: %v", err)
	}

	committed, err := store.Declare(ctx, "sess-cache", author, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "rust"},
		Value:      "cargo-test",
		Confidence: manifest.ConfidenceCommitted,
	})
	if err != nil {
		t.Fatalf("Declare committed: %v", err)
	}

	// Wait for Ristretto to make the SetWithTTL writes visible — the
	// cache buffers writes for a brief window before they become readable.
	store.tentativeAlive.Wait()

	// Pre-eviction: the tentative entry is alive and queryable.
	if q, err := store.Query(ctx, "sess-cache", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  manifest.Scope{manifest.DimensionLanguage: "python"},
	}); err != nil {
		t.Fatalf("Query python (pre-evict): %v", err)
	} else if q.Winner == nil || q.Winner.ID != tentative.Decision.ID {
		t.Fatalf("pre-eviction python winner = %+v, want %s", q.Winner, tentative.Decision.ID)
	}

	// Simulate TTL eviction by removing the gate entry deterministically.
	// In production the Ristretto cache evicts after tentativeTTL with no
	// goroutine; this test simulates that boundary deterministically.
	store.tentativeAlive.Del(tentative.Decision.ID)
	store.tentativeAlive.Wait()

	// Post-eviction: the tentative entry is filtered from results even
	// though its SQLite row still exists (audit history preserved).
	if q, err := store.Query(ctx, "sess-cache", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  manifest.Scope{manifest.DimensionLanguage: "python"},
	}); err != nil {
		t.Fatalf("Query python (post-evict): %v", err)
	} else if q.Winner != nil {
		t.Fatalf("post-eviction python winner = %+v, want nil (cache gate dropped it)", q.Winner)
	}

	// Committed entry must remain visible — the gate doesn't affect it.
	if q, err := store.Query(ctx, "sess-cache", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  manifest.Scope{manifest.DimensionLanguage: "rust"},
	}); err != nil {
		t.Fatalf("Query rust: %v", err)
	} else if q.Winner == nil || q.Winner.ID != committed.Decision.ID {
		t.Fatalf("rust winner = %+v, want committed entry %s", q.Winner, committed.Decision.ID)
	}
}

// TestDecisionManifest_PromotionRemovesCacheGate verifies that when an
// equivalent declaration corroborates a Tentative entry — promoting it
// to Committed — the cache gate is removed so the row remains queryable
// independent of TTL.
func TestDecisionManifest_PromotionRemovesCacheGate(t *testing.T) {
	store := newManifestTestStore(t)
	ctx := context.Background()

	manifest.RegisterDomain(manifest.DomainSpec{
		Name: "test_framework",
		Compatibility: func(a, b string) manifest.Compatibility {
			if a == b {
				return manifest.CompatibilityEquivalent
			}
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	pipelineA := manifest.AgentRef{AgentID: "tester-A", AgentType: "tester-pipeline"}
	pipelineB := manifest.AgentRef{AgentID: "tester-B", AgentType: "tester-pipeline"}

	declA, err := store.Declare(ctx, "sess-promote", pipelineA, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	})
	if err != nil {
		t.Fatalf("Declare A: %v", err)
	}

	// B declares the same value → promotes A from Tentative to Committed.
	if _, err := store.Declare(ctx, "sess-promote", pipelineB, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceTentative,
	}); err != nil {
		t.Fatalf("Declare B: %v", err)
	}
	store.tentativeAlive.Wait()

	// After promotion, deleting A's gate entry must not affect query
	// visibility — A's confidence is now Committed and the gate doesn't
	// apply to non-Tentative rows.
	store.tentativeAlive.Del(declA.Decision.ID)
	store.tentativeAlive.Wait()

	if q, err := store.Query(ctx, "sess-promote", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  manifest.Scope{manifest.DimensionLanguage: "python"},
	}); err != nil {
		t.Fatalf("Query: %v", err)
	} else if q.Winner == nil || q.Winner.Value != "pytest" {
		t.Fatalf("Winner after promotion + gate deletion = %+v, want pytest (promotion bypasses gate)", q.Winner)
	}
}

// TestDecisionManifest_DisjointPathsCoexist verifies that decisions with
// non-overlapping path scopes don't trigger conflicts. Two testers in two
// different services of a monorepo independently picking different
// frameworks is legitimate.
func TestDecisionManifest_DisjointPathsCoexist(t *testing.T) {
	store := newManifestTestStore(t)
	ctx := context.Background()

	manifest.RegisterDomain(manifest.DomainSpec{
		Name: "test_framework",
		Compatibility: func(a, b string) manifest.Compatibility {
			return manifest.CompatibilityIncompatible
		},
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	apiTester := manifest.AgentRef{AgentID: "t-api", AgentType: "tester-pipeline"}
	billingTester := manifest.AgentRef{AgentID: "t-billing", AgentType: "tester-pipeline"}

	if _, err := store.Declare(ctx, "sess-mono", apiTester, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python", manifest.DimensionPath: "services/api/"},
		Value:      "pytest",
		Confidence: manifest.ConfidenceCommitted,
	}); err != nil {
		t.Fatalf("Declare api: %v", err)
	}

	declBilling, err := store.Declare(ctx, "sess-mono", billingTester, manifest.DeclareDecisionInput{
		Domain:     "test_framework",
		Scope:      manifest.Scope{manifest.DimensionLanguage: "python", manifest.DimensionPath: "services/billing/"},
		Value:      "unittest",
		Confidence: manifest.ConfidenceCommitted,
	})
	if err != nil {
		t.Fatalf("Declare billing: %v", err)
	}
	if declBilling.Conflict != nil {
		t.Fatalf("disjoint paths should not produce conflict, got %+v", declBilling.Conflict)
	}

	apiQuery, _ := store.Query(ctx, "sess-mono", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  manifest.Scope{manifest.DimensionLanguage: "python", manifest.DimensionPath: "services/api/tests/test_login.py"},
	})
	if apiQuery.Winner == nil || apiQuery.Winner.Value != "pytest" {
		t.Fatalf("api query winner = %+v, want pytest", apiQuery.Winner)
	}

	billingQuery, _ := store.Query(ctx, "sess-mono", manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  manifest.Scope{manifest.DimensionLanguage: "python", manifest.DimensionPath: "services/billing/tests/test_invoice.py"},
	})
	if billingQuery.Winner == nil || billingQuery.Winner.Value != "unittest" {
		t.Fatalf("billing query winner = %+v, want unittest", billingQuery.Winner)
	}
}
