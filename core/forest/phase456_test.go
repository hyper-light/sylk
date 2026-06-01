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
	if len(packets) != 1 || packets[0].Node.ID != nodes[0].ID {
		t.Fatalf("packets = %+v, want node-backed packet for %s", packets, nodes[0].ID)
	}
	assertTableAbsent(t, db, "forest_branches")
}

func TestPhase456ProductionAppendEventProjectsNodesWithoutBranches(t *testing.T) {
	forest, db := newPhase456ProductionForest(t)
	defer forest.Close()
	defer db.Close()

	err := forest.AppendEvent(context.Background(), &Event{
		SessionID:  "session-phase456-production",
		BranchID:   "branch-production-node",
		AgentID:    "engineer",
		AgentType:  "engineer",
		EventType:  EventTypeClaimAccepted,
		Family:     TreeFamilyEvidence,
		Scope:      ScopeEpisodic,
		Confidence: 0.9,
		Salience:   0.8,
		Title:      "production node claim",
		Summary:    "production retrieval should use node graph",
		Timestamp:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	var nodeRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_nodes WHERE session_id = ?`, "session-phase456-production").Scan(&nodeRows); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	assertTableAbsent(t, db, "forest_branches")
	if nodeRows == 0 {
		t.Fatalf("nodeRows=%d, want node projection without branch writes", nodeRows)
	}
	packets, err := forest.Retrieve(context.Background(), Query{SessionID: "session-phase456-production", Query: "production node", Limit: 2})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(packets) == 0 || packets[0].Node.ID == "" {
		t.Fatalf("packets = %+v, want node-backed packet", packets)
	}
}

func TestPhase456RetrieveForestHydratesEvidenceAndChargesResources(t *testing.T) {
	forest, db := newPhase456ProductionForest(t)
	defer forest.Close()
	defer db.Close()

	claim := canonicalDeltaForForestTest(claims.DeltaActionClaimPosted, 210)
	claim.Refs = []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-forest-packet"}}
	claim.Context = map[string]any{"claim": map[string]any{"id": "claim-forest-packet", "title": "Packet hydration"}}
	claim.Key = claims.BuildCanonicalDeltaKeyForSequence(claim.Action, claim.SessionID, claim.BoardID, claim.Sequence, claim.Refs, claim.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), claim); err != nil {
		t.Fatalf("append claim: %v", err)
	}
	artifact := canonicalDeltaForForestTest(claims.DeltaActionArtifactGenerated, 211)
	artifact.Refs = []claims.DeltaRef{
		{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-forest-packet"},
		{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-forest-packet"},
	}
	artifact.Context = map[string]any{"artifact": map[string]any{"id": "artifact-forest-packet", "kind": "patch", "content_hash": "sha256:packet"}}
	artifact.Key = claims.BuildCanonicalDeltaKeyForSequence(artifact.Action, artifact.SessionID, artifact.BoardID, artifact.Sequence, artifact.Refs, artifact.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), artifact); err != nil {
		t.Fatalf("append artifact: %v", err)
	}
	validation := canonicalDeltaForForestTest(claims.DeltaActionValidationEvaluated, 212)
	validation.Refs = []claims.DeltaRef{
		{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-forest-packet"},
		{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-forest-packet"},
		{Role: "validation", Type: claims.RelatedTypeValidation, ID: "validation-forest-packet"},
	}
	validation.Context = map[string]any{"validation": map[string]any{"id": "validation-forest-packet", "type": "programmatic", "target_artifact_id": "artifact-forest-packet"}}
	validation.Key = claims.BuildCanonicalDeltaKeyForSequence(validation.Action, validation.SessionID, validation.BoardID, validation.Sequence, validation.Refs, validation.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), validation); err != nil {
		t.Fatalf("append validation: %v", err)
	}
	if _, err := forest.RunNodeProjection(context.Background(), 16); err != nil {
		t.Fatalf("project nodes: %v", err)
	}
	insertPhase456ClusterForAllNodes(t, db, "cluster-packet")
	packets, err := forest.RetrieveForest(context.Background(), Query{SessionID: claim.SessionID, Query: "packet hydration", AgentID: "agent-resource", Limit: 8})
	if err != nil {
		t.Fatalf("retrieve forest: %v", err)
	}
	packet := forestPacketForSubject(packets, claims.RelatedTypeClaim, "claim-forest-packet")
	if packet == nil {
		t.Fatalf("packets = %+v, want claim packet", packets)
	}
	if len(packet.Artifacts) == 0 || len(packet.Validations) == 0 || len(packet.ClusterIDs) == 0 {
		t.Fatalf("packet hydration incomplete: artifacts=%d validations=%d clusters=%d", len(packet.Artifacts), len(packet.Validations), len(packet.ClusterIDs))
	}
	assertResourceRows(t, db, "node", packet.Node.ID, "agent-resource", "retrieval_exposure", 1)
	assertResourceRows(t, db, "cluster", "cluster-packet", "agent-resource", "retrieval_exposure", 1)
}

func TestPhase456NodeProjectionUsesPartitionOffsets(t *testing.T) {
	forest, db := newPhase456ProductionForest(t)
	defer forest.Close()
	defer db.Close()

	first, err := forest.AppendLedgerRecord(context.Background(), phase456LedgerRecord("session-partition-a", "claim-partition-a"))
	if err != nil {
		t.Fatalf("append first: %v", err)
	}
	second, err := forest.AppendLedgerRecord(context.Background(), phase456LedgerRecord("session-partition-b", "claim-partition-b"))
	if err != nil {
		t.Fatalf("append second: %v", err)
	}
	partitionB := string(LedgerSourceClaimsDelta) + ":session:session-partition-b"
	_, err = db.Exec(`
		INSERT INTO forest_projection_offsets
			(projection_name, source_kind, last_seq, last_applied_at, policy_version, health_status, updated_at)
		VALUES (?, ?, ?, ?, ?, 'healthy', ?)
		ON CONFLICT(projection_name) DO UPDATE SET last_seq = excluded.last_seq
	`, projectorNodeGraphName, string(LedgerSourceClaimsDelta), second.Seq, time.Now().UTC().Unix(), nodeGraphProjectionPolicy, time.Now().UTC().Unix())
	if err != nil {
		t.Fatalf("seed global offset: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO forest_node_projection_partitions
			(source_partition, last_seq, policy_version, health_status, updated_at)
		VALUES (?, 0, ?, 'idle', ?)
	`, partitionB, nodeGraphProjectionPolicy, time.Now().UTC().Unix()); err != nil {
		t.Fatalf("seed partition offset: %v", err)
	}
	result, err := forest.RunNodeProjection(context.Background(), 8)
	if err != nil {
		t.Fatalf("run projection: %v", err)
	}
	if result.RecordsProcessed == 0 || first.Seq >= second.Seq {
		t.Fatalf("projection result = %+v first=%d second=%d", result, first.Seq, second.Seq)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_nodes WHERE subject_id = ? AND source_partition = ?`, "claim-partition-b", partitionB).Scan(&count); err != nil {
		t.Fatalf("count partition node: %v", err)
	}
	if count != 1 {
		t.Fatalf("partition-backed projection count = %d, want 1", count)
	}
}

func TestPhase456CriticalProjectionFailureDoesNotAdvanceOffset(t *testing.T) {
	forest, db := newPhase456ProductionForest(t)
	defer forest.Close()
	defer db.Close()
	record := forestLedgerProjectionRecord{
		Seq:         99,
		ID:          "ledger-poison",
		SourceKind:  string(LedgerSourceClaimsDelta),
		SourceID:    "poison",
		SourceKey:   "poison-critical",
		EventKind:   "claim.posted",
		SessionID:   "session-poison",
		SubjectType: claims.RelatedTypeClaim,
		SubjectID:   "claim-poison",
		OccurredAt:  time.Now().UTC(),
	}
	var err error
	for i := 0; i < nodeProjectionRetryLimit(); i++ {
		_, err = forest.recordNodeProjectionFailure(context.Background(), record, errors.New("unsupported poison node"))
	}
	if err == nil {
		t.Fatal("critical projection failure returned nil")
	}
	var critical, resolvedAt int
	var artifactID string
	if err := db.QueryRow(`
		SELECT critical, resolved_at, artifact_id
		FROM forest_projection_failures
		WHERE projection_name = ? AND source_key = ?
	`, projectorNodeGraphName, record.SourceKey).Scan(&critical, &resolvedAt, &artifactID); err != nil {
		t.Fatalf("load failure: %v", err)
	}
	if critical != 1 || resolvedAt != 0 || artifactID == "" {
		t.Fatalf("failure critical=%d resolvedAt=%d artifactID=%q, want unresolved critical artifact", critical, resolvedAt, artifactID)
	}
	var offsetRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_projection_offsets WHERE projection_name = ? AND last_seq >= ?`, projectorNodeGraphName, record.Seq).Scan(&offsetRows); err != nil {
		t.Fatalf("count offsets: %v", err)
	}
	if offsetRows != 0 {
		t.Fatalf("critical failure advanced offset rows = %d, want 0", offsetRows)
	}
}

func TestPhase456BridgePOIAndClusterLineage(t *testing.T) {
	neighborIndex := NewMockNeighborIndex(t)
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, NeighborIndex: neighborIndex})
	defer forest.Close()
	defer db.Close()
	nodes := insertPhase456Nodes(t, db, 4)
	neighbors := map[string][]NodeNeighbor{
		nodes[0].ID: {{NodeID: nodes[1].ID, Score: 0.9, Reason: "mockery-density"}, {NodeID: nodes[2].ID, Score: 0.9, Reason: "mockery-density"}},
		nodes[3].ID: {{NodeID: nodes[2].ID, Score: 0.95, Reason: "mockery-density"}, {NodeID: nodes[1].ID, Score: 0.95, Reason: "mockery-density"}},
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
	if result.LineageRecorded == 0 {
		t.Fatalf("lineage recorded = %d, want speciation/merge lineage", result.LineageRecorded)
	}
	var stableNames int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_clusters WHERE stable_name != ''`).Scan(&stableNames); err != nil {
		t.Fatalf("count stable names: %v", err)
	}
	if stableNames == 0 {
		t.Fatal("expected validated density cluster to pass stable-name gate")
	}
	insertPhase456Edge(t, db, nodes[0].ID, nodes[3].ID, ForestEdgeTraversal, 1)
	bridges, err := forest.refreshBridgeNodesAndPOI(context.Background(), 8)
	if err != nil {
		t.Fatalf("refresh bridges/poi: %v", err)
	}
	if bridges.BridgeNodesUpdated == 0 || bridges.POIUpdated == 0 {
		t.Fatalf("bridge result = %+v, want bridge and poi rows", bridges)
	}
	assertPOIReason(t, db, "bridge_node")
	assertPOIReason(t, db, "validation_hub")
}

func TestPhase456EcologyThresholdsResourcesAndRetention(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	if err := validateChannelSpec(substrateChannelSpec{Channel: "bad", Source: "source", Sink: "sink", Conservation: "rule", LowerBound: -1, UpperBound: 1}); err == nil {
		t.Fatal("invalid channel bounds accepted")
	}
	nodes := insertPhase456Nodes(t, db, 3)
	insertPhase456ClusterForNodes(t, db, "cluster-ecology", nodes)
	oldBucket := activationBucket(time.Now().UTC().Unix()) - activationHistoryRetentionBuckets(defaultEcologyPolicy()) - int64(nodeProjectionRetryLimit())
	if _, err := db.Exec(`
		INSERT INTO forest_activation_history
			(scope_type, scope_id, channel, bucket, exposure_count, threshold, policy_version, updated_at)
		VALUES ('node', ?, ?, ?, 1, 0.5, ?, ?)
	`, nodes[0].ID, SubstrateChannelConfidence, oldBucket, ecologyPolicyVersion, time.Now().UTC().Unix()); err != nil {
		t.Fatalf("seed old activation history: %v", err)
	}
	if _, err := forest.RunSubstrateMaintenanceForSession(context.Background(), "session-phase456", 8); err != nil {
		t.Fatalf("substrate maintenance: %v", err)
	}
	var oldRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_activation_history WHERE bucket = ?`, oldBucket).Scan(&oldRows); err != nil {
		t.Fatalf("count old history: %v", err)
	}
	if oldRows != 0 {
		t.Fatalf("old activation rows = %d, want pruned", oldRows)
	}
	var minThreshold, maxThreshold float64
	if err := db.QueryRow(`SELECT MIN(adaptive_threshold), MAX(adaptive_threshold) FROM forest_substrate_field`).Scan(&minThreshold, &maxThreshold); err != nil {
		t.Fatalf("threshold bounds: %v", err)
	}
	if minThreshold < 0 || maxThreshold > 1 {
		t.Fatalf("threshold bounds = [%f,%f], want [0,1]", minThreshold, maxThreshold)
	}
	if _, err := forest.RetrieveForest(context.Background(), Query{SessionID: "session-phase456", Query: "retry", AgentID: "agent-ecology", Limit: 2}); err != nil {
		t.Fatalf("retrieve forest: %v", err)
	}
	assertResourceRows(t, db, "cluster", "cluster-ecology", "agent-ecology", "retrieval_exposure", 1)
	assertResourceRows(t, db, "cluster", "cluster-ecology", "", "substrate_budget", 1)
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

func newPhase456ProductionForest(t testing.TB) (*MemoryForest, *sql.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", stableID("forest-phase456-production", t.Name()))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("seed nodes table: %v", err)
	}
	forest, err := New(Config{DB: db, SynchronousProjection: true, DisableBackgroundWorkers: true})
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	return forest, db
}

func phase456LedgerRecord(sessionID, claimID string) LedgerRecord {
	return LedgerRecord{
		SourceKind:  LedgerSourceClaimsDelta,
		SourceID:    "delta-" + claimID,
		SourceKey:   "phase456:" + sessionID + ":" + claimID,
		EventKind:   string(claims.DeltaActionClaimPosted),
		SessionID:   sessionID,
		SubjectType: claims.RelatedTypeClaim,
		SubjectID:   claimID,
		Actor:       claims.DegradedAgentRef("engineer", "phase456 test"),
		OccurredAt:  time.Now().UTC(),
		Payload: map[string]any{
			"claim": map[string]any{"id": claimID, "title": claimID},
		},
		Refs: []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}},
	}
}

func forestPacketForSubject(packets []*ForestPacket, subjectType, subjectID string) *ForestPacket {
	for _, packet := range packets {
		if packet == nil {
			continue
		}
		if packet.Node.SourcePartition != "reference" && packet.Node.Subject.Type == subjectType && packet.Node.Subject.ID == subjectID {
			return packet
		}
	}
	return nil
}

func insertPhase456ClusterForAllNodes(t testing.TB, db *sql.DB, clusterID string) {
	t.Helper()
	rows, err := db.Query(`
		SELECT node_id, 1.0
		FROM forest_nodes
		WHERE source_partition != 'reference'
		ORDER BY node_id ASC
	`)
	if err != nil {
		t.Fatalf("query cluster nodes: %v", err)
	}
	defer rows.Close()
	var nodes []ForestNode
	for rows.Next() {
		var id string
		var weight float64
		if err := rows.Scan(&id, &weight); err != nil {
			t.Fatalf("scan cluster node: %v", err)
		}
		nodes = append(nodes, ForestNode{ID: id, SourceSeq: int64(len(nodes) + 1), Utility: weight})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate cluster nodes: %v", err)
	}
	insertPhase456ClusterForNodes(t, db, clusterID, nodes)
}

func insertPhase456ClusterForNodes(t testing.TB, db *sql.DB, clusterID string, nodes []ForestNode) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin cluster insert tx: %v", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	if _, err := tx.Exec(`
		INSERT INTO forest_clusters
			(cluster_id, policy_version, status, candidate_name, stable_name, signature, first_seen_at, last_seen_at, metadata)
		VALUES (?, ?, ?, ?, '', ?, ?, ?, '{}')
		ON CONFLICT(cluster_id) DO UPDATE SET last_seen_at = excluded.last_seen_at
	`, clusterID, clusterPolicyVersion, clusterStatusActive, clusterID, clusterID, now, now); err != nil {
		t.Fatalf("insert cluster: %v", err)
	}
	for _, node := range nodes {
		if _, err := tx.Exec(`
			INSERT INTO forest_cluster_membership
				(cluster_id, node_id, membership_weight, reason, source_seq, updated_at)
			VALUES (?, ?, 1, 'test', ?, ?)
			ON CONFLICT(cluster_id, node_id) DO UPDATE SET updated_at = excluded.updated_at
		`, clusterID, node.ID, node.SourceSeq, now); err != nil {
			t.Fatalf("insert cluster membership: %v", err)
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO forest_cluster_metrics
			(cluster_id, policy_version, member_count, cohesion, validation_density,
			 contradiction_load, novelty, utility, decay_pressure, computed_at, metadata)
		VALUES (?, ?, ?, 1, 1, 0, 0.5, 0.8, 0, ?, '{}')
		ON CONFLICT(cluster_id) DO UPDATE SET computed_at = excluded.computed_at
	`, clusterID, clusterPolicyVersion, len(nodes), now); err != nil {
		t.Fatalf("insert cluster metrics: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit cluster insert: %v", err)
	}
}

func assertResourceRows(t testing.TB, db *sql.DB, scopeType, scopeID, agentID, eventKind string, wantAtLeast int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM forest_resource_accounting
		WHERE scope_type = ? AND scope_id = ? AND agent_id = ? AND event_kind = ?
	`, scopeType, scopeID, agentID, eventKind).Scan(&count); err != nil {
		t.Fatalf("count resource rows: %v", err)
	}
	if count < wantAtLeast {
		t.Fatalf("resource rows for %s/%s agent=%s event=%s = %d, want at least %d", scopeType, scopeID, agentID, eventKind, count, wantAtLeast)
	}
}

func assertPOIReason(t testing.TB, db *sql.DB, reason string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_poi_cache WHERE reason = ?`, reason).Scan(&count); err != nil {
		t.Fatalf("count poi reason %s: %v", reason, err)
	}
	if count == 0 {
		t.Fatalf("poi reason %s not recorded", reason)
	}
}
