package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
)

type mockClaimProposalSink struct {
	mock.Mock
}

func (m *mockClaimProposalSink) ProposeClaim(ctx context.Context, proposal ForestClaimProposal) error {
	args := m.Called(ctx, proposal)
	return args.Error(0)
}

func TestPhase789AntigenicReplayQuarantineRecoveryAndDecay(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	failed := insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "validation-failed",
		Session: "session-phase789-antigenic",
		Kind:    ForestNodeValidation,
		Grade:   EvidenceGradeFailed,
		Seq:     1,
	})

	first, err := forest.RunAntigenicMaintenanceForSession(context.Background(), failed.SessionID, nodeProjectionRetryLimit())
	if err != nil {
		t.Fatalf("run antigenic first: %v", err)
	}
	if first.AntigenicInserted == 0 {
		t.Fatal("expected failed validation to insert antigenic vector")
	}
	second, err := forest.RunAntigenicMaintenanceForSession(context.Background(), failed.SessionID, nodeProjectionRetryLimit())
	if err != nil {
		t.Fatalf("run antigenic replay: %v", err)
	}
	if second.AntigenicInserted != 0 {
		t.Fatalf("replay inserted duplicate antigenic vectors: %+v", second)
	}

	normal, err := forest.RetrieveForest(context.Background(), Query{SessionID: failed.SessionID, Query: "failed", Limit: nodeProjectionRetryLimit()})
	if err != nil {
		t.Fatalf("retrieve normal: %v", err)
	}
	if packetByNodeID(normal, failed.ID) != nil {
		t.Fatal("quarantined failed validation leaked into normal retrieval")
	}
	audit, err := forest.RetrieveForest(context.Background(), Query{SessionID: failed.SessionID, Query: "failed", Limit: nodeProjectionRetryLimit(), IncludeCounterEvidence: true})
	if err != nil {
		t.Fatalf("retrieve audit: %v", err)
	}
	quarantined := packetByNodeID(audit, failed.ID)
	if quarantined == nil || !quarantined.Quarantine.Quarantined || len(quarantined.CounterEvidence) == 0 {
		t.Fatalf("audit packet missing quarantine/counter evidence: %+v", quarantined)
	}

	remediation := insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "remediation-new-validation",
		Session: failed.SessionID,
		Kind:    ForestNodeRemediation,
		Grade:   EvidenceGradeValidated,
		Seq:     2,
	})
	insertPhase456Edge(t, db, remediation.ID, failed.ID, ForestEdgeRemediation, 1)
	recovery, err := forest.RunAntigenicMaintenanceForSession(context.Background(), failed.SessionID, nodeProjectionRetryLimit())
	if err != nil {
		t.Fatalf("run antigenic recovery: %v", err)
	}
	if recovery.ImmunityInserted == 0 {
		t.Fatal("expected remediation evidence to insert immunity vector")
	}
	recovered, err := forest.RetrieveForest(context.Background(), Query{SessionID: failed.SessionID, Query: "failed", Limit: nodeProjectionRetryLimit()})
	if err != nil {
		t.Fatalf("retrieve recovered: %v", err)
	}
	if packetByNodeID(recovered, failed.ID) == nil {
		t.Fatal("new remediation evidence did not recover quarantined node for normal retrieval")
	}
	if vectorDecayWeight(time.Now().Add(-time.Duration(activationHistoryRetentionBuckets(defaultEcologyPolicy())*activationBucketSpanSeconds(defaultEcologyPolicy()))*time.Second).Unix(), time.Now().Unix()) >= 1 {
		t.Fatal("old vector evidence did not decay")
	}
}

func TestPhase789AntigenicConcurrentContradictionAndRemediationConverges(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	failed := insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "race-failed-validation",
		Session: "session-phase789-race",
		Kind:    ForestNodeValidation,
		Grade:   EvidenceGradeFailed,
		Seq:     10,
	})
	remediation := insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "race-remediation",
		Session: failed.SessionID,
		Kind:    ForestNodeRemediation,
		Grade:   EvidenceGradeValidated,
		Seq:     11,
	})
	insertPhase456Edge(t, db, remediation.ID, failed.ID, ForestEdgeRemediation, 1)

	errs := make(chan error, nodeProjectionRetryLimit())
	var wg sync.WaitGroup
	for i := 0; i < nodeProjectionRetryLimit(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := forest.RunAntigenicMaintenanceForSession(context.Background(), failed.SessionID, nodeProjectionRetryLimit())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent antigenic maintenance: %v", err)
		}
	}
	assertVectorCount(t, db, "forest_antigenic_vectors", "node", failed.ID, CorruptionFailedValidation, 1)
	assertVectorCount(t, db, "forest_immunity_vectors", "node", failed.ID, ImmunityResolvedContradiction, 1)
}

func TestPhase789OutbreakCreatesSingleGuardianProposal(t *testing.T) {
	sink := &mockClaimProposalSink{}
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, ClaimProposalSink: sink})
	defer forest.Close()
	defer db.Close()

	nodes := insertPhase789FailedCluster(t, db, "session-phase789-outbreak", "cluster-outbreak", nodeProjectionRetryLimit())
	insertPhase789Bridge(t, db, nodes[0].ID, "cluster-outbreak", "cluster-adjacent", 1)
	sink.On("ProposeClaim", mock.Anything, mock.MatchedBy(func(proposal ForestClaimProposal) bool {
		return proposal.ClusterID == "cluster-outbreak" &&
			proposal.Dimension == CorruptionFailedValidation &&
			proposal.GuardianReviewRequired &&
			len(proposal.EvidenceRefs) >= nodeProjectionRetryLimit()
	})).Return(nil).Once()

	if _, err := forest.RunAntigenicMaintenanceForSession(context.Background(), "session-phase789-outbreak", nodeProjectionRetryLimit()*nodeProjectionRetryLimit()); err != nil {
		t.Fatalf("run antigenic: %v", err)
	}
	first, err := forest.RunOutbreakMaintenance(context.Background(), nodeProjectionRetryLimit())
	if err != nil {
		t.Fatalf("run outbreak first: %v", err)
	}
	if first.ProposalsEmitted != 1 {
		t.Fatalf("proposals emitted = %d, want 1", first.ProposalsEmitted)
	}
	second, err := forest.RunOutbreakMaintenance(context.Background(), nodeProjectionRetryLimit())
	if err != nil {
		t.Fatalf("run outbreak replay: %v", err)
	}
	if second.ProposalsEmitted != 0 {
		t.Fatalf("replayed outbreak emitted duplicate proposal: %+v", second)
	}
	sink.AssertExpectations(t)
	sink.AssertNumberOfCalls(t, "ProposeClaim", 1)
	assertOutbreakStatus(t, db, "cluster-outbreak", CorruptionFailedValidation, OutbreakStatusProposed, true)
	assertProposalArtifactExists(t, db, "cluster-outbreak", CorruptionFailedValidation)
}

func TestPhase789OutbreakRejectsLowEvidenceAndSupersedesRecoveredCandidate(t *testing.T) {
	sink := &mockClaimProposalSink{}
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, ClaimProposalSink: sink})
	defer forest.Close()
	defer db.Close()

	insertPhase789FailedCluster(t, db, "session-phase789-low", "cluster-low", nodeProjectionRetryLimit()-1)
	if _, err := forest.RunAntigenicMaintenanceForSession(context.Background(), "session-phase789-low", nodeProjectionRetryLimit()); err != nil {
		t.Fatalf("run low antigenic: %v", err)
	}
	low, err := forest.RunOutbreakMaintenance(context.Background(), nodeProjectionRetryLimit())
	if err != nil {
		t.Fatalf("run low outbreak: %v", err)
	}
	if low.ProposalsEmitted != 0 || low.OutbreaksUpdated != 0 {
		t.Fatalf("low-evidence outbreak was not rejected: %+v", low)
	}

	recoveredNodes := insertPhase789FailedCluster(t, db, "session-phase789-recovered", "cluster-recovered", nodeProjectionRetryLimit())
	for i, failed := range recoveredNodes {
		remediation := insertPhase789Node(t, db, phase789NodeSpec{
			ID:      fmt.Sprintf("recovered-remediation-%d", i),
			Session: failed.SessionID,
			Kind:    ForestNodeRemediation,
			Grade:   EvidenceGradeValidated,
			Seq:     int64(100 + i),
		})
		insertPhase456Edge(t, db, remediation.ID, failed.ID, ForestEdgeRemediation, 1)
	}
	if _, err := forest.RunAntigenicMaintenanceForSession(context.Background(), "session-phase789-recovered", nodeProjectionRetryLimit()*nodeProjectionRetryLimit()); err != nil {
		t.Fatalf("run recovered antigenic: %v", err)
	}
	recovered, err := forest.RunOutbreakMaintenance(context.Background(), nodeProjectionRetryLimit()*nodeProjectionRetryLimit())
	if err != nil {
		t.Fatalf("run recovered outbreak: %v", err)
	}
	if recovered.Superseded == 0 || recovered.ProposalsEmitted != 0 {
		t.Fatalf("recovered outbreak not superseded without proposal: %+v", recovered)
	}
	assertOutbreakStatus(t, db, "cluster-recovered", CorruptionFailedValidation, OutbreakStatusSuperseded, false)
	assertOutbreakReason(t, db, "cluster-recovered", "immunity_recovered_before_dispatch")
	sink.AssertNotCalled(t, "ProposeClaim", mock.Anything, mock.Anything)
}

func TestPhase789RetrievalPacketHydratesEvidenceRiskAndProposalSections(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	claim := insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "packet-claim-node",
		Session: "session-phase789-packet",
		Kind:    ForestNodeClaim,
		Grade:   EvidenceGradeValidated,
		Subject: ForestSubjectRef{Type: "claim", ID: "claim-packet"},
		Seq:     200,
	})
	other := insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "packet-support-node",
		Session: claim.SessionID,
		Kind:    ForestNodeArtifact,
		Grade:   EvidenceGradeValidated,
		Seq:     201,
	})
	insertPhase456Edge(t, db, claim.ID, other.ID, ForestEdgeSupport, 1)
	insertPhase456ClusterForNodes(t, db, "cluster-packet", []ForestNode{claim, other})
	insertPhase789ArtifactEvidence(t, db, "artifact-packet", "claim-packet")
	insertPhase789ValidationEvidence(t, db, "validation-packet", "claim-packet", "artifact-packet", "validated")
	insertPhase789POI(t, db, "poi-packet", "cluster-packet", claim.ID)
	insertPhase789Bridge(t, db, claim.ID, "cluster-packet", "cluster-neighbor", 1)
	insertPhase789Outbreak(t, db, "cluster-packet", CorruptionContradiction, "proposal-packet", true)

	packets, err := forest.RetrieveForest(context.Background(), Query{
		SessionID:              claim.SessionID,
		Query:                  "packet claim",
		Limit:                  nodeProjectionRetryLimit() * nodeProjectionRetryLimit(),
		IncludeCounterEvidence: true,
	})
	if err != nil {
		t.Fatalf("retrieve forest packet: %v", err)
	}
	packet := packetByNodeID(packets, claim.ID)
	if packet == nil {
		t.Fatal("expected claim packet")
	}
	if len(packet.ClusterIDs) == 0 || len(packet.Paths) == 0 || len(packet.Artifacts) == 0 || len(packet.Validations) == 0 {
		t.Fatalf("packet missing cluster/path/artifact/validation sections: %+v", packet)
	}
	if len(packet.POIs) == 0 || len(packet.BridgeRisks) == 0 || len(packet.ProposedClaims) == 0 {
		t.Fatalf("packet missing poi/bridge/proposal sections: %+v", packet)
	}
	if len(packet.Evidence) < nodeProjectionRetryLimit() {
		t.Fatalf("packet evidence refs too sparse: %+v", packet.Evidence)
	}
	if !packet.BridgeRisks[0].GuardianReviewRequired || !packet.ProposedClaims[0].GuardianReviewRequired {
		t.Fatalf("bridge/proposal did not require Guardian review: %+v", packet)
	}
}

func TestPhase789CursorImmutableValidatedAndCompacted(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	nodes := make([]ForestNode, 0, nodeProjectionRetryLimit()+1)
	for i := 0; i <= nodeProjectionRetryLimit(); i++ {
		nodes = append(nodes, insertPhase789Node(t, db, phase789NodeSpec{
			ID:      fmt.Sprintf("cursor-node-%d", i),
			Session: "session-phase789-cursor",
			Kind:    ForestNodeClaim,
			Grade:   EvidenceGradeValidated,
			Seq:     int64(300 + i),
		}))
	}
	packets := make([]*ForestPacket, 0, len(nodes))
	for i, node := range nodes {
		packets = append(packets, phase789Packet(node, float64(i+1)))
	}
	packets[len(packets)-1].BridgeRisks = []ForestBridgeRisk{{
		BridgeID:        "bridge-cursor",
		NodeID:          nodes[len(nodes)-1].ID,
		SourceClusterID: "cluster-source",
		TargetClusterID: "cluster-target",
	}}
	cursor, err := forest.CreateForestCursor(context.Background(), ForestCursorInput{
		SessionID: "session-phase789-cursor",
		AgentID:   "agent-cursor",
		Packets:   packets,
		Limit:     nodeProjectionRetryLimit(),
	})
	if err != nil {
		t.Fatalf("create cursor: %v", err)
	}
	expectedTop := compactStrings([]string{nodes[len(nodes)-1].ID, nodes[len(nodes)-2].ID, nodes[len(nodes)-3].ID}, nodeProjectionRetryLimit())
	if strings.Join(cursor.ActiveNodeIDs, ",") != strings.Join(expectedTop, ",") {
		t.Fatalf("cursor compaction not deterministic top-k: got %+v want %+v", cursor.ActiveNodeIDs, expectedTop)
	}
	loaded, err := forest.LoadForestCursor(context.Background(), cursor.ID)
	if err != nil {
		t.Fatalf("load cursor: %v", err)
	}
	if strings.Join(loaded.ActiveNodeIDs, ",") != strings.Join(cursor.ActiveNodeIDs, ",") {
		t.Fatalf("loaded cursor mismatch: %+v != %+v", loaded.ActiveNodeIDs, cursor.ActiveNodeIDs)
	}
	crossings, ok := loaded.Metadata["bridge_crossings"].([]any)
	if !ok || len(crossings) != 1 {
		t.Fatalf("cursor bridge crossing metadata missing source/target clusters: %#v", loaded.Metadata["bridge_crossings"])
	}
	crossing, ok := crossings[0].(map[string]any)
	if !ok || crossing["source_cluster_id"] != "cluster-source" || crossing["target_cluster_id"] != "cluster-target" {
		t.Fatalf("cursor bridge crossing source/target mismatch: %#v", crossings[0])
	}
	if _, err := db.Exec(`UPDATE forest_cursors SET no_cursor_reason = 'mutated' WHERE cursor_id = ?`, cursor.ID); err == nil {
		t.Fatal("cursor update unexpectedly succeeded")
	}
	_, err = forest.CreateForestCursor(context.Background(), ForestCursorInput{
		SessionID: "session-phase789-cursor",
		Packets: []*ForestPacket{{
			Node:     ForestNode{ID: "missing-cursor-node"},
			Evidence: []ForestEvidence{{RefType: "node", RefID: "missing-cursor-node"}},
			Score:    1,
		}},
		Limit: nodeProjectionRetryLimit(),
	})
	if err == nil {
		t.Fatal("cursor with nonexistent node was accepted")
	}
}

func TestPhase789RoleProjectionContracts(t *testing.T) {
	node := ForestNode{ID: "role-node", Kind: ForestNodeClaim, Title: "Role packet", EvidenceGrade: EvidenceGradeValidated}
	packet := phase789Packet(node, 1)
	packet.Quarantine = ForestQuarantine{Quarantined: true, Factor: 1}
	packet.ValidationNeed = 1
	cursor := &ForestCursor{ID: "cursor-role", RiskFlags: []string{"suppression.active"}}
	roles := []string{"architect", "engineer", "tester", "guardian", "inspector", "librarian", "academic", "designer", "orchestrator", "scribe", "archivalist", "guide"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			first, err := BuildRoleForestProjection(role, []*ForestPacket{packet}, cursor, nodeProjectionRetryLimit())
			if err != nil {
				t.Fatalf("build projection: %v", err)
			}
			second, err := BuildRoleForestProjection(role, []*ForestPacket{packet}, cursor, nodeProjectionRetryLimit())
			if err != nil {
				t.Fatalf("build projection again: %v", err)
			}
			if first.Text != second.Text {
				t.Fatalf("projection not deterministic:\n%s\n---\n%s", first.Text, second.Text)
			}
			if !strings.Contains(first.Text, "evidence_refs:") || !strings.Contains(first.Text, "risk_flags:") {
				t.Fatalf("projection missing evidence/risk sections: %q", first.Text)
			}
		})
	}
	if _, err := BuildRoleForestProjection("engineer", []*ForestPacket{{Node: node, Score: 1}}, cursor, nodeProjectionRetryLimit()); err == nil {
		t.Fatal("projection accepted packet without evidence refs")
	}
}

type phase789NodeSpec struct {
	ID      string
	Session string
	Kind    ForestNodeKind
	Grade   EvidenceGrade
	Subject ForestSubjectRef
	Seq     int64
}

func insertPhase789Node(t testing.TB, db *sql.DB, spec phase789NodeSpec) ForestNode {
	t.Helper()
	if spec.Subject.Type == "" {
		spec.Subject = ForestSubjectRef{Type: "test", ID: spec.ID}
	}
	now := time.Now().UTC()
	node := ForestNode{
		Kind:            spec.Kind,
		SourceKind:      "test",
		SourcePartition: "test:" + spec.Session,
		SourceKey:       "phase789:" + spec.ID,
		SourceSeq:       spec.Seq,
		Subject:         spec.Subject,
		SessionID:       spec.Session,
		Title:           spec.ID,
		Summary:         spec.ID + " summary",
		EvidenceGrade:   spec.Grade,
		Confidence:      1,
		Salience:        1,
		Utility:         1,
		Novelty:         1,
		FirstSeenAt:     now,
		LastSeenAt:      now,
		PolicyVersion:   nodeGraphProjectionPolicy,
	}
	normalized, err := normalizeForestNode(node)
	if err != nil {
		t.Fatalf("normalize node: %v", err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin node tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := upsertForestNodeTx(context.Background(), tx, normalized); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit node: %v", err)
	}
	return normalized
}

func insertPhase789FailedCluster(t testing.TB, db *sql.DB, sessionID, clusterID string, count int) []ForestNode {
	t.Helper()
	nodes := make([]ForestNode, 0, count)
	for i := 0; i < count; i++ {
		nodes = append(nodes, insertPhase789Node(t, db, phase789NodeSpec{
			ID:      fmt.Sprintf("%s-failed-%d", clusterID, i),
			Session: sessionID,
			Kind:    ForestNodeValidation,
			Grade:   EvidenceGradeFailed,
			Seq:     int64(i + 1),
		}))
	}
	insertPhase456ClusterForNodes(t, db, clusterID, nodes)
	return nodes
}

func insertPhase789Bridge(t testing.TB, db *sql.DB, nodeID, sourceClusterID, targetClusterID string, contradictionRisk float64) {
	t.Helper()
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		INSERT INTO forest_bridge_nodes
			(bridge_id, node_id, source_cluster_id, target_cluster_id, bridge_score,
			 cross_edge_count, traversal_frequency, validation_support, contradiction_risk,
			 source_edge_ids, policy_version, updated_at, metadata)
		VALUES (?, ?, ?, ?, 1, 1, 1, 0, ?, 'edge:test', ?, ?, '{}')
		ON CONFLICT(node_id, source_cluster_id, target_cluster_id) DO UPDATE SET
			contradiction_risk = excluded.contradiction_risk,
			updated_at = excluded.updated_at
	`, stableID("bridge-test", nodeID, sourceClusterID, targetClusterID), nodeID, sourceClusterID, targetClusterID, contradictionRisk, clusterPolicyVersion, now); err != nil {
		t.Fatalf("insert bridge: %v", err)
	}
}

func insertPhase789ArtifactEvidence(t testing.TB, db *sql.DB, artifactID, claimID string) {
	t.Helper()
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		INSERT INTO forest_artifacts
			(artifact_id, claim_id, generator_participant, artifact_name, artifact_kind,
			 data_type, content_hash, content_ref, status, validation_status,
			 last_sequence, first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES (?, ?, 'tester', 'packet artifact', 'patch', 'text', 'sha256:packet',
			'ref:packet', 'generated', 'validated', 1, ?, ?, 'hash:packet', '{}')
	`, artifactID, claimID, now, now); err != nil {
		t.Fatalf("insert artifact evidence: %v", err)
	}
}

func insertPhase789ValidationEvidence(t testing.TB, db *sql.DB, validationID, claimID, artifactID, status string) {
	t.Helper()
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		INSERT INTO forest_validations
			(validation_id, claim_id, target_artifact_id, evaluator_participant,
			 validation_type, status, result_artifact_id, failure_reason, required,
			 last_sequence, first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES (?, ?, ?, 'tester', 'unit', ?, '', '', 1, 2, ?, ?, 'hash:validation', '{}')
	`, validationID, claimID, artifactID, status, now, now); err != nil {
		t.Fatalf("insert validation evidence: %v", err)
	}
}

func insertPhase789POI(t testing.TB, db *sql.DB, poiID, clusterID, nodeID string) {
	t.Helper()
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		INSERT INTO forest_poi_cache
			(poi_id, cluster_id, node_id, reason, priority, source_metrics,
			 expires_at, invalidation_sequence, policy_version, updated_at)
		VALUES (?, ?, ?, 'high_validation_need', 1, '{}', ?, 3, ?, ?)
	`, poiID, clusterID, nodeID, now+activationBucketSpanSeconds(defaultEcologyPolicy()), clusterPolicyVersion, now); err != nil {
		t.Fatalf("insert poi: %v", err)
	}
}

func insertPhase789Outbreak(t testing.TB, db *sql.DB, clusterID, dimension, proposalID string, guardian bool) {
	t.Helper()
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		INSERT INTO forest_outbreaks
			(outbreak_id, cluster_id, dimension, growth_rate, evidence_refs, counter_evidence_refs,
			 status, proposed_claim_id, guardian_review_required, source_seq, policy_version,
			 first_seen_at, updated_at, metadata)
		VALUES (?, ?, ?, 1, 'node:packet', '', ?, ?, ?, ?, ?, ?, ?, '{}')
		ON CONFLICT(cluster_id, dimension, policy_version) DO UPDATE SET
			status = excluded.status,
			proposed_claim_id = excluded.proposed_claim_id
	`, stableID("outbreak-test", clusterID, dimension), clusterID, dimension, OutbreakStatusProposed, proposalID, boolInt(guardian), now, antigenicPolicyVersion, now, now); err != nil {
		t.Fatalf("insert outbreak: %v", err)
	}
}

func phase789Packet(node ForestNode, score float64) *ForestPacket {
	return &ForestPacket{
		Node: node,
		Evidence: []ForestEvidence{{
			RefType: "node",
			RefID:   node.ID,
			NodeID:  node.ID,
			Grade:   node.EvidenceGrade,
			Summary: node.Summary,
		}},
		Score: score,
	}
}

func packetByNodeID(packets []*ForestPacket, nodeID string) *ForestPacket {
	for _, packet := range packets {
		if packet != nil && packet.Node.ID == nodeID {
			return packet
		}
	}
	return nil
}

func assertVectorCount(t testing.TB, db *sql.DB, table, scopeType, scopeID, dimension string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s
		WHERE scope_type = ? AND scope_id = ? AND dimension = ? AND policy_version = ?
	`, table), scopeType, scopeID, dimension, antigenicPolicyVersion).Scan(&count); err != nil {
		t.Fatalf("count vector rows: %v", err)
	}
	if count != want {
		t.Fatalf("%s %s/%s/%s count = %d, want %d", table, scopeType, scopeID, dimension, count, want)
	}
}

func assertOutbreakStatus(t testing.TB, db *sql.DB, clusterID, dimension, status string, guardian bool) {
	t.Helper()
	var gotStatus string
	var gotGuardian int
	if err := db.QueryRow(`
		SELECT status, guardian_review_required
		FROM forest_outbreaks
		WHERE cluster_id = ? AND dimension = ? AND policy_version = ?
	`, clusterID, dimension, antigenicPolicyVersion).Scan(&gotStatus, &gotGuardian); err != nil {
		t.Fatalf("query outbreak status: %v", err)
	}
	if gotStatus != status || (gotGuardian != 0) != guardian {
		t.Fatalf("outbreak status/guardian = %s/%v, want %s/%v", gotStatus, gotGuardian != 0, status, guardian)
	}
}

func assertOutbreakReason(t testing.TB, db *sql.DB, clusterID, reason string) {
	t.Helper()
	var metadata string
	if err := db.QueryRow(`
		SELECT metadata
		FROM forest_outbreaks
		WHERE cluster_id = ? AND policy_version = ?
	`, clusterID, antigenicPolicyVersion).Scan(&metadata); err != nil {
		t.Fatalf("query outbreak metadata: %v", err)
	}
	if !strings.Contains(metadata, reason) {
		t.Fatalf("outbreak metadata %q missing reason %q", metadata, reason)
	}
}

func assertProposalArtifactExists(t testing.TB, db *sql.DB, clusterID, dimension string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM forest_artifacts
		WHERE artifact_kind = 'remediation_claim_proposal'
		  AND content_ref LIKE 'forest_outbreak:%'
		  AND metadata LIKE ?
	`, "%"+clusterID+"%"+dimension+"%").Scan(&count); err != nil {
		t.Fatalf("count proposal artifacts: %v", err)
	}
	if count != 1 {
		t.Fatalf("proposal artifact count = %d, want 1", count)
	}
}
