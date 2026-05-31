package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/stretchr/testify/mock"
)

func TestPhase456NodeProjectionMapsLedgerKindsIdempotently(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	claim := canonicalDeltaForForestTest(claims.DeltaActionClaimPosted, 10)
	claim.Refs = []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-node"}}
	claim.Context = map[string]any{"claim": map[string]any{"id": "claim-node", "title": "Retry work"}}
	claim.Key = claims.BuildCanonicalDeltaKeyForSequence(claim.Action, claim.SessionID, claim.BoardID, claim.Sequence, claim.Refs, claim.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), claim); err != nil {
		t.Fatalf("append claim: %v", err)
	}

	artifact := canonicalDeltaForForestTest(claims.DeltaActionArtifactGenerated, 11)
	artifact.Refs = []claims.DeltaRef{
		{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-node"},
		{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-node"},
	}
	artifact.Context = map[string]any{"artifact": map[string]any{"id": "artifact-node", "kind": "patch", "content_hash": "sha256:node"}}
	artifact.Key = claims.BuildCanonicalDeltaKeyForSequence(artifact.Action, artifact.SessionID, artifact.BoardID, artifact.Sequence, artifact.Refs, artifact.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), artifact); err != nil {
		t.Fatalf("append artifact: %v", err)
	}

	validation := canonicalDeltaForForestTest(claims.DeltaActionValidationValidationFailed, 12)
	validation.Refs = []claims.DeltaRef{
		{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-node"},
		{Role: "validation", Type: claims.RelatedTypeValidation, ID: "validation-node"},
		{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-node"},
	}
	validation.Context = map[string]any{"validation": map[string]any{"id": "validation-node", "type": "programmatic", "target_artifact_id": "artifact-node"}}
	validation.Key = claims.BuildCanonicalDeltaKeyForSequence(validation.Action, validation.SessionID, validation.BoardID, validation.Sequence, validation.Refs, validation.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), validation); err != nil {
		t.Fatalf("append validation: %v", err)
	}

	first, err := forest.RunNodeProjection(context.Background(), 16)
	if err != nil {
		t.Fatalf("run node projection: %v", err)
	}
	if first.RecordsProcessed != 3 {
		t.Fatalf("records processed = %d, want 3", first.RecordsProcessed)
	}
	checksum1, err := NodeGraphChecksum(context.Background(), db)
	if err != nil {
		t.Fatalf("checksum 1: %v", err)
	}
	second, err := forest.RunNodeProjection(context.Background(), 16)
	if err != nil {
		t.Fatalf("rerun node projection: %v", err)
	}
	if second.RecordsProcessed != 0 {
		t.Fatalf("rerun processed = %d, want 0", second.RecordsProcessed)
	}
	checksum2, err := NodeGraphChecksum(context.Background(), db)
	if err != nil {
		t.Fatalf("checksum 2: %v", err)
	}
	if checksum1 != checksum2 {
		t.Fatalf("projection not idempotent: %s != %s", checksum1, checksum2)
	}

	assertPrimaryNodeKindCount(t, db, ForestNodeClaim, 1)
	assertPrimaryNodeKindCount(t, db, ForestNodeArtifact, 1)
	assertPrimaryNodeKindCount(t, db, ForestNodeContradiction, 1)
	assertEdgeKindCount(t, db, ForestEdgeValidation, 1)
}

func TestPhase456UnsupportedNodeKindRejectedBeforeWrite(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	_, err = upsertForestNodeTx(context.Background(), tx, ForestNode{
		Kind:       ForestNodeKind("unsupported"),
		SourceKind: "test",
		SourceKey:  "bad",
		Subject:    ForestSubjectRef{Type: "test", ID: "bad"},
	})
	if err == nil {
		t.Fatal("unsupported node kind accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_nodes`).Scan(&count); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if count != 0 {
		t.Fatalf("node rows = %d, want 0", count)
	}
}

func TestPhase456ConcurrentNodeProjectionConverges(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	for i := 0; i < 8; i++ {
		delta := canonicalDeltaForForestTest(claims.DeltaActionClaimPosted, uint64(100+i))
		claimID := fmt.Sprintf("claim-race-%d", i)
		delta.Refs = []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}}
		delta.Key = claims.BuildCanonicalDeltaKeyForSequence(delta.Action, delta.SessionID, delta.BoardID, delta.Sequence, delta.Refs, delta.Delivery)
		if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err != nil {
			t.Fatalf("append delta %d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := forest.RunNodeProjection(context.Background(), 4)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent projection: %v", err)
		}
	}
	if _, err := forest.RunNodeProjection(context.Background(), 16); err != nil {
		t.Fatalf("final projection: %v", err)
	}
	assertPrimaryNodeKindCount(t, db, ForestNodeClaim, 8)
}

func TestPhase456ClusterMaintenanceWithMockNeighborIndex(t *testing.T) {
	neighborIndex := NewMockNeighborIndex(t)
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, NeighborIndex: neighborIndex})
	defer forest.Close()
	defer db.Close()

	nodes := insertPhase456Nodes(t, db, 3)
	neighbors := map[string][]NodeNeighbor{
		nodes[0].ID: {
			{NodeID: nodes[1].ID, Score: 0.9, Reason: "mockery-semantic-neighbor"},
			{NodeID: nodes[2].ID, Score: 0.8, Reason: "mockery-semantic-neighbor"},
		},
	}
	neighborIndex.EXPECT().
		Neighbors(mock.Anything, mock.AnythingOfType("forest.ForestNode"), 8).
		RunAndReturn(func(_ context.Context, node ForestNode, _ int) ([]NodeNeighbor, error) {
			return neighbors[node.ID], nil
		}).
		Times(len(nodes))

	result, err := forest.RunClusterMaintenance(context.Background(), 8)
	if err != nil {
		t.Fatalf("cluster maintenance: %v", err)
	}
	if result.ClustersUpdated == 0 || result.MembershipsUpdated < 3 {
		t.Fatalf("cluster result = %+v, want cluster with 3 memberships", result)
	}
}

func TestPhase456ClusterNeighborFailureRecordsMaintenanceLedger(t *testing.T) {
	neighborIndex := NewMockNeighborIndex(t)
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, NeighborIndex: neighborIndex})
	defer forest.Close()
	defer db.Close()
	insertPhase456Nodes(t, db, 1)

	neighborErr := errors.New("neighbor index unavailable")
	neighborIndex.EXPECT().
		Neighbors(mock.Anything, mock.AnythingOfType("forest.ForestNode"), 8).
		Return(nil, neighborErr).
		Once()

	if _, err := forest.RunClusterMaintenance(context.Background(), 8); err == nil {
		t.Fatal("cluster maintenance succeeded with failing neighbor index")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_ledger WHERE event_kind = 'cluster_neighbor_failure'`).Scan(&count); err != nil {
		t.Fatalf("count maintenance ledger: %v", err)
	}
	if count != 1 {
		t.Fatalf("maintenance ledger rows = %d, want 1", count)
	}
}

func TestPhase456EcologicalSubstrateChannelsAndResourceAccounting(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	nodes := insertPhase456Nodes(t, db, 2)
	insertPhase456Edge(t, db, nodes[0].ID, nodes[1].ID, ForestEdgeValidation, 1)

	result, err := forest.RunSubstrateMaintenanceForSession(context.Background(), "session-phase456", 8)
	if err != nil {
		t.Fatalf("substrate maintenance: %v", err)
	}
	if result.StatesUpdated != 2 {
		t.Fatalf("states updated = %d, want 2", result.StatesUpdated)
	}
	var confidence, validation float64
	if err := db.QueryRow(`
		SELECT value FROM forest_substrate_field
		WHERE scope_type = 'node' AND scope_id = ? AND channel = ?
	`, nodes[0].ID, SubstrateChannelConfidence).Scan(&confidence); err != nil {
		t.Fatalf("load confidence channel: %v", err)
	}
	if err := db.QueryRow(`
		SELECT value FROM forest_substrate_field
		WHERE scope_type = 'node' AND scope_id = ? AND channel = ?
	`, nodes[0].ID, SubstrateChannelValidation).Scan(&validation); err != nil {
		t.Fatalf("load validation channel: %v", err)
	}
	if confidence <= 0 || validation <= 0 {
		t.Fatalf("confidence=%f validation=%f, want positive", confidence, validation)
	}
	var resourceRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_resource_accounting`).Scan(&resourceRows); err != nil {
		t.Fatalf("count resource accounting: %v", err)
	}
	if resourceRows == 0 {
		t.Fatal("expected resource accounting rows")
	}
}

func TestPhase456RetrieveUsesNodesWithoutBranches(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	nodes := insertPhase456Nodes(t, db, 1)
	result, err := forest.RunSubstrateMaintenanceForSession(context.Background(), "session-phase456", 8)
	if err != nil {
		t.Fatalf("substrate: %v", err)
	}
	if result.StatesUpdated == 0 {
		t.Fatal("expected substrate field state")
	}
	packets, err := forest.Retrieve(context.Background(), Query{SessionID: "session-phase456", Query: "retry", Limit: 4})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(packets) != 1 || packets[0].Branch.ID != nodes[0].ID {
		t.Fatalf("packets = %+v, want node-backed packet for %s", packets, nodes[0].ID)
	}
	var branchRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_branches`).Scan(&branchRows); err != nil {
		t.Fatalf("count branches: %v", err)
	}
	if branchRows != 0 {
		t.Fatalf("branch rows = %d, want node retrieval without branches", branchRows)
	}
}

func assertPrimaryNodeKindCount(t *testing.T, db *sql.DB, kind ForestNodeKind, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_nodes WHERE node_kind = ? AND source_partition != 'reference'`, string(kind)).Scan(&got); err != nil {
		t.Fatalf("count node kind %s: %v", kind, err)
	}
	if got != want {
		t.Fatalf("node kind %s count = %d, want %d", kind, got, want)
	}
}

func assertEdgeKindCount(t *testing.T, db *sql.DB, kind ForestEdgeKind, wantAtLeast int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_node_edges WHERE edge_kind = ?`, string(kind)).Scan(&got); err != nil {
		t.Fatalf("count edge kind %s: %v", kind, err)
	}
	if got < wantAtLeast {
		t.Fatalf("edge kind %s count = %d, want at least %d", kind, got, wantAtLeast)
	}
}

func insertPhase456Nodes(t testing.TB, db *sql.DB, count int) []ForestNode {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin node insert tx: %v", err)
	}
	defer tx.Rollback()
	nodes := make([]ForestNode, 0, count)
	now := time.Now().UTC()
	for i := 0; i < count; i++ {
		kind := ForestNodeClaim
		grade := EvidenceGradeValidated
		if i == count-1 && count > 1 {
			kind = ForestNodeValidation
			grade = EvidenceGradeValidated
		}
		node := ForestNode{
			Kind:            kind,
			SourceKind:      "test",
			SourcePartition: "test:session-phase456",
			SourceKey:       fmt.Sprintf("node-source-%d", i),
			SourceSeq:       int64(i + 1),
			Subject:         ForestSubjectRef{Type: "test", ID: fmt.Sprintf("node-%d", i)},
			SessionID:       "session-phase456",
			Title:           fmt.Sprintf("retry node %d", i),
			Summary:         "retry validation evidence",
			EvidenceGrade:   grade,
			Confidence:      0.9,
			Salience:        0.8,
			Utility:         0.7,
			Novelty:         0.6,
			FirstSeenAt:     now,
			LastSeenAt:      now,
			PolicyVersion:   nodeGraphProjectionPolicy,
		}
		normalized, err := normalizeForestNode(node)
		if err != nil {
			t.Fatalf("normalize node: %v", err)
		}
		if _, err := upsertForestNodeTx(context.Background(), tx, normalized); err != nil {
			t.Fatalf("insert node: %v", err)
		}
		nodes = append(nodes, normalized)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit nodes: %v", err)
	}
	return nodes
}

func insertPhase456Edge(t testing.TB, db *sql.DB, source, target string, kind ForestEdgeKind, weight float64) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin edge tx: %v", err)
	}
	defer tx.Rollback()
	edge := ForestEdge{
		SourceNodeID:  source,
		TargetNodeID:  target,
		Kind:          kind,
		SourceKind:    "test",
		SourceKey:     "edge-source",
		Weight:        weight,
		EvidenceGrade: EvidenceGradeValidated,
		CreatedAt:     time.Now().UTC(),
	}
	if _, err := upsertForestEdgeTx(context.Background(), tx, edge); err != nil {
		t.Fatalf("insert edge: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit edge: %v", err)
	}
}
