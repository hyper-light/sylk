package forest

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

func BenchmarkPhase456NodeProjection(b *testing.B) {
	forest, db := newTestForestWithConfig(b, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	for i := 0; i < b.N; i++ {
		delta := canonicalDeltaForForestTest(claims.DeltaActionClaimPosted, uint64(i+1))
		claimID := "bench-claim-" + stableID(delta.Key)
		delta.Refs = []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}}
		delta.Key = claims.BuildCanonicalDeltaKeyForSequence(delta.Action, delta.SessionID, delta.BoardID, delta.Sequence, delta.Refs, delta.Delivery)
		if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err != nil {
			b.Fatalf("append delta: %v", err)
		}
	}
	b.ResetTimer()
	if _, err := forest.RunNodeProjection(context.Background(), b.N); err != nil {
		b.Fatalf("node projection: %v", err)
	}
}

func BenchmarkPhase456ClusterMaintenance(b *testing.B) {
	forest, db := newTestForestWithConfig(b, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	nodes := insertPhase456Nodes(b, db, b.N+1)
	for i := 0; i < b.N; i++ {
		insertPhase456Edge(b, db, nodes[i].ID, nodes[i+1].ID, ForestEdgeSimilarity, 1)
	}
	b.ResetTimer()
	if _, err := forest.RunClusterMaintenance(context.Background(), b.N); err != nil {
		b.Fatalf("cluster maintenance: %v", err)
	}
}

func BenchmarkPhase456EcologicalSubstrate(b *testing.B) {
	forest, db := newTestForestWithConfig(b, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	insertPhase456Nodes(b, db, b.N)
	b.ResetTimer()
	if _, err := forest.RunSubstrateMaintenanceForSession(context.Background(), "session-phase456", b.N); err != nil {
		b.Fatalf("substrate maintenance: %v", err)
	}
}
