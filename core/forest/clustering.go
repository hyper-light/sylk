package forest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	clusterPolicyVersion = "cluster_v1"
	clusterStatusActive  = "active"
)

type NodeNeighbor struct {
	NodeID string
	Score  float64
	Reason string
}

//go:generate mockery --name=NeighborIndex --inpackage --output=. --filename=mock_neighbor_index_test.go --outpkg=forest
type NeighborIndex interface {
	Neighbors(ctx context.Context, node ForestNode, limit int) ([]NodeNeighbor, error)
}

type ClusterMaintenanceResult struct {
	ClustersUpdated    int
	MembershipsUpdated int
	BridgeNodesUpdated int
	POIUpdated         int
	LineageRecorded    int
}

func (m *MemoryForest) RunClusterMaintenance(ctx context.Context, limit int) (ClusterMaintenanceResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()
	return m.runClusterMaintenance(ctx, limit)
}

func (m *MemoryForest) runClusterMaintenance(ctx context.Context, limit int) (ClusterMaintenanceResult, error) {
	if limit <= 0 {
		limit = m.substrateLimit
	}
	nodes, err := m.loadClusterSeedNodes(ctx, limit)
	if err != nil {
		return ClusterMaintenanceResult{}, err
	}
	var result ClusterMaintenanceResult
	for _, node := range nodes {
		updated, err := m.clusterAroundNode(ctx, node, limit)
		if err != nil {
			if recordErr := m.recordMaintenanceLedger(ctx, "cluster_neighbor_failure", err); recordErr != nil {
				m.logger.Warn("forest_cluster_failure_artifact_failed", "err", recordErr.Error())
			}
			return result, err
		}
		result.ClustersUpdated += updated.ClustersUpdated
		result.MembershipsUpdated += updated.MembershipsUpdated
		result.LineageRecorded += updated.LineageRecorded
	}
	bridges, err := m.refreshBridgeNodesAndPOI(ctx, limit)
	if err != nil {
		return result, err
	}
	result.BridgeNodesUpdated = bridges.BridgeNodesUpdated
	result.POIUpdated = bridges.POIUpdated
	return result, nil
}

func (m *MemoryForest) loadClusterSeedNodes(ctx context.Context, limit int) ([]ForestNode, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT node_id, node_kind, source_kind, source_partition, source_key, source_seq,
		       subject_type, subject_id, session_id, task_id, title, summary, evidence_grade,
		       confidence, salience, utility, novelty, status, policy_version,
		       first_seen_at, last_seen_at, payload_hash, metadata
		FROM forest_nodes
		ORDER BY last_seen_at DESC, source_seq DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query cluster seed nodes: %w", err)
	}
	defer rows.Close()
	var nodes []ForestNode
	for rows.Next() {
		node, err := scanForestNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func scanForestNode(rows interface {
	Scan(dest ...any) error
}) (ForestNode, error) {
	var firstSeen, lastSeen int64
	var metadata string
	var nodeKind, evidenceGrade string
	node := ForestNode{}
	if err := rows.Scan(&node.ID, &nodeKind, &node.SourceKind, &node.SourcePartition, &node.SourceKey, &node.SourceSeq,
		&node.Subject.Type, &node.Subject.ID, &node.SessionID, &node.TaskID, &node.Title, &node.Summary, &evidenceGrade,
		&node.Confidence, &node.Salience, &node.Utility, &node.Novelty, &node.Status, &node.PolicyVersion,
		&firstSeen, &lastSeen, &node.PayloadHash, &metadata); err != nil {
		return ForestNode{}, fmt.Errorf("scan forest node: %w", err)
	}
	node.Kind = ForestNodeKind(nodeKind)
	node.EvidenceGrade = EvidenceGrade(evidenceGrade)
	node.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
	node.LastSeenAt = time.Unix(lastSeen, 0).UTC()
	return node, nil
}

func (m *MemoryForest) clusterAroundNode(ctx context.Context, node ForestNode, limit int) (ClusterMaintenanceResult, error) {
	neighbors, err := m.neighborsForCluster(ctx, node, limit)
	if err != nil {
		return ClusterMaintenanceResult{}, err
	}
	members := clusterMembersFromNeighbors(node, neighbors)
	if len(members) == 0 {
		return ClusterMaintenanceResult{}, nil
	}
	clusterID := stableClusterID(members)
	metrics := clusterMetricsFromMembers(ctx, m.db, clusterID, members)
	now := time.Now().UTC().Unix()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return ClusterMaintenanceResult{}, fmt.Errorf("begin cluster tx: %w", err)
	}
	defer tx.Rollback()
	existed, firstSeen, err := loadClusterExistenceTx(ctx, tx, clusterID)
	if err != nil {
		return ClusterMaintenanceResult{}, err
	}
	priorClusters, err := loadPriorClustersForMembersTx(ctx, tx, sortedClusterMemberIDs(members), clusterID)
	if err != nil {
		return ClusterMaintenanceResult{}, err
	}
	if !existed {
		firstSeen = now
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_clusters
			(cluster_id, policy_version, status, candidate_name, stable_name, signature, first_seen_at, last_seen_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cluster_id) DO UPDATE SET
			status = excluded.status,
			candidate_name = excluded.candidate_name,
			stable_name = CASE WHEN excluded.stable_name != '' THEN excluded.stable_name ELSE forest_clusters.stable_name END,
			last_seen_at = excluded.last_seen_at,
			metadata = excluded.metadata
	`, clusterID, clusterPolicyVersion, clusterStatusActive, candidateClusterName(members), stableClusterName(metrics),
		strings.Join(sortedClusterMemberIDs(members), ","), now, now, marshalJSON(metrics)); err != nil {
		return ClusterMaintenanceResult{}, fmt.Errorf("upsert forest cluster: %w", err)
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_cluster_membership
				(cluster_id, node_id, membership_weight, reason, source_seq, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(cluster_id, node_id) DO UPDATE SET
				membership_weight = excluded.membership_weight,
				reason = excluded.reason,
				source_seq = excluded.source_seq,
				updated_at = excluded.updated_at
		`, clusterID, member.NodeID, member.Score, member.Reason, node.SourceSeq, now); err != nil {
			return ClusterMaintenanceResult{}, fmt.Errorf("upsert cluster membership: %w", err)
		}
	}
	if err := upsertClusterMetricsTx(ctx, tx, metrics, now); err != nil {
		return ClusterMaintenanceResult{}, err
	}
	lineageCount, err := recordClusterLineageTx(ctx, tx, clusterID, priorClusters, !existed, firstSeen, now, node.SourceSeq)
	if err != nil {
		return ClusterMaintenanceResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClusterMaintenanceResult{}, fmt.Errorf("commit cluster tx: %w", err)
	}
	return ClusterMaintenanceResult{ClustersUpdated: 1, MembershipsUpdated: len(members), LineageRecorded: lineageCount}, nil
}

func (m *MemoryForest) neighborsForCluster(ctx context.Context, node ForestNode, limit int) ([]NodeNeighbor, error) {
	if m.neighborIndex != nil {
		return m.neighborIndex.Neighbors(ctx, node, limit)
	}
	return m.graphNeighbors(ctx, node.ID, limit)
}

func (m *MemoryForest) graphNeighbors(ctx context.Context, nodeID string, limit int) ([]NodeNeighbor, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT neighbor_id, MAX(weight) AS score, GROUP_CONCAT(edge_kind)
		FROM (
			SELECT target_node_id AS neighbor_id, weight, edge_kind
			FROM forest_node_edges
			WHERE source_node_id = ?
			UNION ALL
			SELECT source_node_id AS neighbor_id, weight, edge_kind
			FROM forest_node_edges
			WHERE target_node_id = ?
		)
		WHERE neighbor_id != ?
		GROUP BY neighbor_id
		ORDER BY score DESC, neighbor_id ASC
		LIMIT ?
	`, nodeID, nodeID, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("query graph neighbors: %w", err)
	}
	defer rows.Close()
	var neighbors []NodeNeighbor
	for rows.Next() {
		var n NodeNeighbor
		if err := rows.Scan(&n.NodeID, &n.Score, &n.Reason); err != nil {
			return nil, fmt.Errorf("scan graph neighbor: %w", err)
		}
		n.Score = clampFinite01(n.Score)
		neighbors = append(neighbors, n)
	}
	return neighbors, rows.Err()
}

func clusterMembersFromNeighbors(seed ForestNode, neighbors []NodeNeighbor) []NodeNeighbor {
	threshold := densityThreshold(neighbors)
	members := []NodeNeighbor{{NodeID: seed.ID, Score: 1, Reason: "seed"}}
	for _, neighbor := range neighbors {
		if strings.TrimSpace(neighbor.NodeID) == "" {
			continue
		}
		if neighbor.Score < threshold {
			continue
		}
		neighbor.Score = clampFinite01(neighbor.Score)
		members = append(members, neighbor)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].Score == members[j].Score {
			return members[i].NodeID < members[j].NodeID
		}
		return members[i].Score > members[j].Score
	})
	return members
}

func densityThreshold(neighbors []NodeNeighbor) float64 {
	if len(neighbors) == 0 {
		return 1
	}
	scores := make([]float64, 0, len(neighbors))
	for _, neighbor := range neighbors {
		scores = append(scores, clampFinite01(neighbor.Score))
	}
	sort.Float64s(scores)
	return scores[len(scores)/2]
}

func stableClusterID(members []NodeNeighbor) string {
	return stableID("forest_cluster", clusterPolicyVersion, strings.Join(sortedClusterMemberIDs(members), ","))
}

func sortedClusterMemberIDs(members []NodeNeighbor) []string {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.NodeID)
	}
	sort.Strings(ids)
	return ids
}

type clusterMetrics struct {
	ClusterID         string
	PolicyVersion     string
	MemberCount       int
	Cohesion          float64
	ValidationDensity float64
	ContradictionLoad float64
	Novelty           float64
	Utility           float64
	DecayPressure     float64
	SourceMemberIDs   []string
}

func clusterMetricsFromMembers(ctx context.Context, db *sql.DB, clusterID string, members []NodeNeighbor) clusterMetrics {
	metrics := clusterMetrics{
		ClusterID:       clusterID,
		PolicyVersion:   clusterPolicyVersion,
		MemberCount:     len(members),
		SourceMemberIDs: sortedClusterMemberIDs(members),
	}
	if len(members) == 0 || db == nil {
		return metrics
	}
	totalWeight := 0.0
	for _, member := range members {
		totalWeight += clampFinite01(member.Score)
	}
	metrics.Cohesion = clamp01(totalWeight / float64(len(members)))
	stats := loadClusterNodeStats(ctx, db, metrics.SourceMemberIDs)
	metrics.ValidationDensity = stats.validationDensity
	metrics.ContradictionLoad = stats.contradictionLoad
	metrics.Novelty = stats.novelty
	metrics.Utility = stats.utility
	metrics.DecayPressure = stats.decayPressure
	return metrics
}

type clusterNodeStats struct {
	validationDensity float64
	contradictionLoad float64
	novelty           float64
	utility           float64
	decayPressure     float64
}

func loadClusterNodeStats(ctx context.Context, db *sql.DB, nodeIDs []string) clusterNodeStats {
	if len(nodeIDs) == 0 {
		return clusterNodeStats{}
	}
	placeholders, args := placeholdersForStrings(nodeIDs)
	row := db.QueryRowContext(ctx, `
		SELECT
			AVG(CASE WHEN node_kind = 'validation' OR evidence_grade = 'validated' THEN 1.0 ELSE 0.0 END),
			AVG(CASE WHEN node_kind = 'contradiction' OR evidence_grade IN ('contradicted', 'failed') THEN 1.0 ELSE 0.0 END),
			AVG(novelty),
			AVG(utility),
			AVG(CASE WHEN status = 'dormant' THEN 1.0 ELSE 0.0 END)
		FROM forest_nodes
		WHERE node_id IN (`+placeholders+`)
	`, args...)
	stats := clusterNodeStats{}
	_ = row.Scan(&stats.validationDensity, &stats.contradictionLoad, &stats.novelty, &stats.utility, &stats.decayPressure)
	stats.validationDensity = clampFinite01(stats.validationDensity)
	stats.contradictionLoad = clampFinite01(stats.contradictionLoad)
	stats.novelty = clampFinite01(stats.novelty)
	stats.utility = clampFinite01(stats.utility)
	stats.decayPressure = clampFinite01(stats.decayPressure)
	return stats
}

func upsertClusterMetricsTx(ctx context.Context, tx *sql.Tx, metrics clusterMetrics, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_cluster_metrics
			(cluster_id, policy_version, member_count, cohesion, validation_density,
			 contradiction_load, novelty, utility, decay_pressure, computed_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cluster_id) DO UPDATE SET
			policy_version = excluded.policy_version,
			member_count = excluded.member_count,
			cohesion = excluded.cohesion,
			validation_density = excluded.validation_density,
			contradiction_load = excluded.contradiction_load,
			novelty = excluded.novelty,
			utility = excluded.utility,
			decay_pressure = excluded.decay_pressure,
			computed_at = excluded.computed_at,
			metadata = excluded.metadata
	`, metrics.ClusterID, metrics.PolicyVersion, metrics.MemberCount, metrics.Cohesion, metrics.ValidationDensity,
		metrics.ContradictionLoad, metrics.Novelty, metrics.Utility, metrics.DecayPressure, now, marshalJSON(metrics))
	if err != nil {
		return fmt.Errorf("upsert cluster metrics: %w", err)
	}
	return nil
}

func candidateClusterName(members []NodeNeighbor) string {
	if len(members) == 0 {
		return ""
	}
	return "cluster " + stableID(strings.Join(sortedClusterMemberIDs(members), ","))[:12]
}

func stableClusterName(metrics clusterMetrics) string {
	if metrics.MemberCount < nodeProjectionRetryLimit() {
		return ""
	}
	evidenceGate := metrics.ValidationDensity > 0 && metrics.ValidationDensity >= metrics.ContradictionLoad
	cohesionGate := metrics.Cohesion >= metrics.Novelty
	if evidenceGate && cohesionGate {
		return "validated " + metrics.ClusterID[:12]
	}
	return ""
}

func loadClusterExistenceTx(ctx context.Context, tx *sql.Tx, clusterID string) (bool, int64, error) {
	var firstSeen int64
	err := tx.QueryRowContext(ctx, `
		SELECT first_seen_at
		FROM forest_clusters
		WHERE cluster_id = ?
	`, clusterID).Scan(&firstSeen)
	if err == sql.ErrNoRows {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("load cluster existence: %w", err)
	}
	return true, firstSeen, nil
}

func loadPriorClustersForMembersTx(ctx context.Context, tx *sql.Tx, memberIDs []string, targetClusterID string) ([]string, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}
	placeholders, args := placeholdersForStrings(memberIDs)
	args = append(args, targetClusterID)
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT cluster_id
		FROM forest_cluster_membership
		WHERE node_id IN (`+placeholders+`) AND cluster_id != ?
		ORDER BY cluster_id ASC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query prior clusters for lineage: %w", err)
	}
	defer rows.Close()
	var clusters []string
	for rows.Next() {
		var clusterID string
		if err := rows.Scan(&clusterID); err != nil {
			return nil, fmt.Errorf("scan prior cluster lineage: %w", err)
		}
		clusters = append(clusters, clusterID)
	}
	return clusters, rows.Err()
}

func recordClusterLineageTx(ctx context.Context, tx *sql.Tx, targetClusterID string, priorClusters []string, created bool, firstSeen, now, sourceSeq int64) (int, error) {
	count := 0
	if created {
		if err := insertClusterLineageTx(ctx, tx, "speciation", "", targetClusterID, firstSeen, now, sourceSeq, "new density cluster"); err != nil {
			return 0, err
		}
		count++
	}
	for _, prior := range priorClusters {
		if err := insertClusterLineageTx(ctx, tx, "merge", prior, targetClusterID, firstSeen, now, sourceSeq, "overlapping member density"); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func insertClusterLineageTx(ctx context.Context, tx *sql.Tx, operation, sourceClusterID, targetClusterID string, firstSeen, now, sourceSeq int64, reason string) error {
	lineageID := stableID("cluster_lineage", clusterPolicyVersion, operation, sourceClusterID, targetClusterID, fmt.Sprint(sourceSeq))
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_cluster_lineage
			(lineage_id, operation, source_cluster_id, target_cluster_id, source_ledger_id,
			 policy_version, reason, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(lineage_id) DO UPDATE SET
			reason = excluded.reason,
			metadata = excluded.metadata
	`, lineageID, operation, sourceClusterID, targetClusterID, fmt.Sprint(sourceSeq),
		clusterPolicyVersion, reason, now, marshalJSON(map[string]any{
			"first_seen_at": firstSeen,
			"source_seq":    sourceSeq,
		}))
	if err != nil {
		return fmt.Errorf("insert cluster lineage: %w", err)
	}
	return nil
}

func (m *MemoryForest) refreshBridgeNodesAndPOI(ctx context.Context, limit int) (ClusterMaintenanceResult, error) {
	bridges, err := m.computeBridgeCandidates(ctx, limit)
	if err != nil {
		return ClusterMaintenanceResult{}, err
	}
	pois, err := m.computePOICandidates(ctx, limit)
	if err != nil {
		return ClusterMaintenanceResult{}, err
	}
	now := time.Now().UTC().Unix()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return ClusterMaintenanceResult{}, fmt.Errorf("begin bridge tx: %w", err)
	}
	defer tx.Rollback()
	for _, bridge := range bridges {
		if bridge.crossEdgeCount <= 0 {
			return ClusterMaintenanceResult{}, fmt.Errorf("bridge %s has no cross-cluster evidence", bridge.nodeID)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_bridge_nodes
				(bridge_id, node_id, source_cluster_id, target_cluster_id, bridge_score,
				 cross_edge_count, traversal_frequency, validation_support, contradiction_risk,
				 source_edge_ids, policy_version, updated_at, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(node_id, source_cluster_id, target_cluster_id) DO UPDATE SET
				bridge_score = excluded.bridge_score,
				cross_edge_count = excluded.cross_edge_count,
				traversal_frequency = excluded.traversal_frequency,
				validation_support = excluded.validation_support,
				contradiction_risk = excluded.contradiction_risk,
				source_edge_ids = excluded.source_edge_ids,
				policy_version = excluded.policy_version,
				updated_at = excluded.updated_at,
				metadata = excluded.metadata
		`, bridge.id, bridge.nodeID, bridge.sourceClusterID, bridge.targetClusterID, bridge.score, bridge.crossEdgeCount,
			bridge.traversalFrequency, bridge.validationSupport, bridge.contradictionRisk, strings.Join(bridge.edgeIDs, ","),
			clusterPolicyVersion, now, marshalJSON(bridge)); err != nil {
			return ClusterMaintenanceResult{}, fmt.Errorf("upsert bridge node: %w", err)
		}
		if err := upsertPOICacheTx(ctx, tx, poiCandidate{
			id:                  "poi:" + bridge.id,
			clusterID:           bridge.sourceClusterID,
			nodeID:              bridge.nodeID,
			reason:              "bridge_node",
			priority:            bridge.score,
			sourceMetrics:       bridge,
			expiresAt:           now + int64(limit),
			invalidationSeq:     bridge.maxSeq,
			sourcePolicyVersion: clusterPolicyVersion,
		}, now); err != nil {
			return ClusterMaintenanceResult{}, err
		}
	}
	for _, poi := range pois {
		if err := upsertPOICacheTx(ctx, tx, poi, now); err != nil {
			return ClusterMaintenanceResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ClusterMaintenanceResult{}, fmt.Errorf("commit bridge tx: %w", err)
	}
	return ClusterMaintenanceResult{BridgeNodesUpdated: len(bridges), POIUpdated: len(bridges) + len(pois)}, nil
}

type bridgeCandidate struct {
	id                 string
	nodeID             string
	sourceClusterID    string
	targetClusterID    string
	score              float64
	crossEdgeCount     int
	traversalFrequency float64
	validationSupport  float64
	contradictionRisk  float64
	edgeIDs            []string
	edgeKindWeights    map[string]float64
	maxSeq             int64
}

func (m *MemoryForest) computeBridgeCandidates(ctx context.Context, limit int) ([]bridgeCandidate, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT e.edge_id, e.source_node_id, e.target_node_id, e.edge_kind, e.weight, e.source_seq,
		       sm.cluster_id, tm.cluster_id
		FROM forest_node_edges e
		JOIN forest_cluster_membership sm ON sm.node_id = e.source_node_id
		JOIN forest_cluster_membership tm ON tm.node_id = e.target_node_id
		WHERE sm.cluster_id != tm.cluster_id
		ORDER BY e.weight DESC, e.source_seq DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query bridge candidates: %w", err)
	}
	defer rows.Close()
	byKey := map[string]bridgeCandidate{}
	for rows.Next() {
		var edgeID, sourceNode, targetNode, edgeKind, sourceCluster, targetCluster string
		var weight float64
		var seq int64
		if err := rows.Scan(&edgeID, &sourceNode, &targetNode, &edgeKind, &weight, &seq, &sourceCluster, &targetCluster); err != nil {
			return nil, fmt.Errorf("scan bridge candidate: %w", err)
		}
		nodeID := sourceNode
		key := nodeID + ":" + sourceCluster + ":" + targetCluster
		candidate := byKey[key]
		if candidate.id == "" {
			candidate.id = stableID("bridge", key)
			candidate.nodeID = nodeID
			candidate.sourceClusterID = sourceCluster
			candidate.targetClusterID = targetCluster
			candidate.edgeKindWeights = map[string]float64{}
		}
		if candidate.edgeKindWeights == nil {
			candidate.edgeKindWeights = map[string]float64{}
		}
		candidate.crossEdgeCount++
		candidate.edgeIDs = append(candidate.edgeIDs, edgeID)
		edgeWeight := clampFinite01(weight)
		candidate.score += edgeWeight
		candidate.edgeKindWeights[edgeKind] += edgeWeight
		if edgeKind == string(ForestEdgeTraversal) {
			candidate.traversalFrequency += weight
		}
		if edgeKind == string(ForestEdgeValidation) {
			candidate.validationSupport += weight
		}
		if edgeKind == string(ForestEdgeContradiction) {
			candidate.contradictionRisk += weight
		}
		if seq > candidate.maxSeq {
			candidate.maxSeq = seq
		}
		byKey[key] = candidate
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	bridges := make([]bridgeCandidate, 0, len(byKey))
	for _, candidate := range byKey {
		meanWeight := candidate.score / float64(candidate.crossEdgeCount)
		diversity := float64(len(candidate.edgeKindWeights)) / float64(len(allForestEdgeKinds()))
		candidate.traversalFrequency = clamp01(candidate.traversalFrequency / float64(candidate.crossEdgeCount))
		candidate.validationSupport = clamp01(candidate.validationSupport / float64(candidate.crossEdgeCount))
		candidate.contradictionRisk = clamp01(candidate.contradictionRisk / float64(candidate.crossEdgeCount))
		candidate.score = averageClamped01(meanWeight, diversity, candidate.traversalFrequency, candidate.validationSupport, 1-candidate.contradictionRisk)
		bridges = append(bridges, candidate)
	}
	sort.Slice(bridges, func(i, j int) bool {
		if bridges[i].score == bridges[j].score {
			return bridges[i].id < bridges[j].id
		}
		return bridges[i].score > bridges[j].score
	})
	return bridges, nil
}

type poiCandidate struct {
	id                  string
	clusterID           string
	nodeID              string
	reason              string
	priority            float64
	sourceMetrics       any
	expiresAt           int64
	invalidationSeq     int64
	sourcePolicyVersion string
}

func (m *MemoryForest) computePOICandidates(ctx context.Context, limit int) ([]poiCandidate, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT cm.cluster_id, cm.node_id, n.node_kind, n.evidence_grade, n.confidence,
		       n.salience, n.utility, n.novelty, n.source_seq, met.validation_density,
		       met.contradiction_load, met.utility
		FROM forest_cluster_membership cm
		JOIN forest_nodes n ON n.node_id = cm.node_id
		LEFT JOIN forest_cluster_metrics met ON met.cluster_id = cm.cluster_id
		ORDER BY cm.membership_weight DESC, n.source_seq DESC, cm.cluster_id ASC, cm.node_id ASC
		LIMIT ?
	`, limit*len(defaultEcologyPolicy().Channels))
	if err != nil {
		return nil, fmt.Errorf("query poi candidates: %w", err)
	}
	defer rows.Close()
	byID := map[string]poiCandidate{}
	now := time.Now().UTC().Unix()
	for rows.Next() {
		candidate, ok, err := scanPOICandidate(rows, now, limit)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		current := byID[candidate.id]
		if candidate.priority >= current.priority {
			byID[candidate.id] = candidate
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pois := make([]poiCandidate, 0, len(byID))
	for _, candidate := range byID {
		pois = append(pois, candidate)
	}
	sort.Slice(pois, func(i, j int) bool {
		if pois[i].priority == pois[j].priority {
			return pois[i].id < pois[j].id
		}
		return pois[i].priority > pois[j].priority
	})
	if len(pois) > limit {
		pois = pois[:limit]
	}
	return pois, nil
}

func scanPOICandidate(rows interface {
	Scan(dest ...any) error
}, now int64, limit int) (poiCandidate, bool, error) {
	var clusterID, nodeID, kind, grade string
	var sourceSeq int64
	var confidence, salience, utility, novelty, validationDensity, contradictionLoad, clusterUtility float64
	if err := rows.Scan(&clusterID, &nodeID, &kind, &grade, &confidence, &salience, &utility, &novelty,
		&sourceSeq, &validationDensity, &contradictionLoad, &clusterUtility); err != nil {
		return poiCandidate{}, false, fmt.Errorf("scan poi candidate: %w", err)
	}
	reason, priority := poiReasonAndPriority(ForestNodeKind(kind), EvidenceGrade(grade), confidence, salience, utility, novelty, validationDensity, contradictionLoad, clusterUtility)
	if reason == "" {
		return poiCandidate{}, false, nil
	}
	return poiCandidate{
		id:                  "poi:" + stableID("cluster_poi", clusterID, nodeID, reason),
		clusterID:           clusterID,
		nodeID:              nodeID,
		reason:              reason,
		priority:            priority,
		expiresAt:           now + int64(limit),
		invalidationSeq:     sourceSeq,
		sourcePolicyVersion: clusterPolicyVersion,
		sourceMetrics: map[string]any{
			"confidence":         confidence,
			"salience":           salience,
			"utility":            utility,
			"novelty":            novelty,
			"validation_density": validationDensity,
			"contradiction_load": contradictionLoad,
			"cluster_utility":    clusterUtility,
		},
	}, true, nil
}

func poiReasonAndPriority(kind ForestNodeKind, grade EvidenceGrade, confidence, salience, utility, novelty, validationDensity, contradictionLoad, clusterUtility float64) (string, float64) {
	if kind == ForestNodeContradiction || grade == EvidenceGradeContradicted || grade == EvidenceGradeFailed {
		return "contradiction_center", averageClamped01(contradictionLoad, salience, 1-confidence)
	}
	if kind == ForestNodeValidation || grade == EvidenceGradeValidated {
		return "validation_hub", averageClamped01(validationDensity, confidence, salience)
	}
	if kind == ForestNodeArtifact && utility >= clusterUtility {
		return "high_value_artifact", averageClamped01(utility, salience, confidence)
	}
	if contradictionLoad > validationDensity && confidence < salience {
		return "brittle_node", averageClamped01(contradictionLoad, salience, novelty)
	}
	return "", 0
}

func upsertPOICacheTx(ctx context.Context, tx *sql.Tx, poi poiCandidate, now int64) error {
	policyVersion := firstNonEmptyString(poi.sourcePolicyVersion, clusterPolicyVersion)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_poi_cache
			(poi_id, cluster_id, node_id, reason, priority, source_metrics, expires_at,
			 invalidation_sequence, policy_version, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(poi_id) DO UPDATE SET
			priority = excluded.priority,
			source_metrics = excluded.source_metrics,
			expires_at = excluded.expires_at,
			invalidation_sequence = excluded.invalidation_sequence,
			policy_version = excluded.policy_version,
			updated_at = excluded.updated_at
	`, poi.id, poi.clusterID, poi.nodeID, poi.reason, clampFinite01(poi.priority), marshalJSON(poi.sourceMetrics),
		poi.expiresAt, poi.invalidationSeq, policyVersion, now)
	if err != nil {
		return fmt.Errorf("upsert poi cache: %w", err)
	}
	return nil
}

func placeholdersForStrings(values []string) (string, []any) {
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, value := range values {
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	return strings.Join(placeholders, ","), args
}

func (m *MemoryForest) recordMaintenanceLedger(ctx context.Context, kind string, cause error) error {
	_, err := m.AppendLedgerRecord(ctx, LedgerRecord{
		SourceKind:  LedgerSourceMaintenance,
		SourceID:    kind,
		SourceKey:   kind + ":" + stableID(time.Now().UTC().Format(time.RFC3339Nano), cause.Error()),
		EventKind:   kind,
		SessionID:   "global",
		SubjectType: "maintenance",
		SubjectID:   kind,
		Actor:       claims.DegradedAgentRef("forest", "maintenance"),
		OccurredAt:  time.Now().UTC(),
		Payload: map[string]any{
			"error": cause.Error(),
		},
	})
	return err
}
