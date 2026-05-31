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
	if err := tx.Commit(); err != nil {
		return ClusterMaintenanceResult{}, fmt.Errorf("commit cluster tx: %w", err)
	}
	return ClusterMaintenanceResult{ClustersUpdated: 1, MembershipsUpdated: len(members)}, nil
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
	if metrics.MemberCount == 0 {
		return ""
	}
	evidenceGate := metrics.ValidationDensity >= metrics.ContradictionLoad
	cohesionGate := metrics.Cohesion >= metrics.Novelty
	if evidenceGate && cohesionGate {
		return "validated " + metrics.ClusterID[:12]
	}
	return ""
}

func (m *MemoryForest) refreshBridgeNodesAndPOI(ctx context.Context, limit int) (ClusterMaintenanceResult, error) {
	bridges, err := m.computeBridgeCandidates(ctx, limit)
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
		if _, err := tx.ExecContext(ctx, `
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
		`, "poi:"+bridge.id, bridge.sourceClusterID, bridge.nodeID, "bridge_node", bridge.score, marshalJSON(bridge),
			now+int64(limit), bridge.maxSeq, clusterPolicyVersion, now); err != nil {
			return ClusterMaintenanceResult{}, fmt.Errorf("upsert poi cache: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return ClusterMaintenanceResult{}, fmt.Errorf("commit bridge tx: %w", err)
	}
	return ClusterMaintenanceResult{BridgeNodesUpdated: len(bridges), POIUpdated: len(bridges)}, nil
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
		}
		candidate.crossEdgeCount++
		candidate.edgeIDs = append(candidate.edgeIDs, edgeID)
		candidate.score += clampFinite01(weight)
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
		candidate.score = clamp01(candidate.score / float64(candidate.crossEdgeCount))
		candidate.traversalFrequency = clamp01(candidate.traversalFrequency)
		candidate.validationSupport = clamp01(candidate.validationSupport)
		candidate.contradictionRisk = clamp01(candidate.contradictionRisk)
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
