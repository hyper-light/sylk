package forest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (m *MemoryForest) hasRetrievalNodes(ctx context.Context, sessionID string) (bool, error) {
	var count int
	err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_nodes
		WHERE session_id = ? OR ? = ''
	`, normalizeForestSessionID(sessionID), strings.TrimSpace(sessionID)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count retrieval nodes: %w", err)
	}
	return count > 0, nil
}

func (m *MemoryForest) shouldRetrieveFromNodes(ctx context.Context, sessionID string) (bool, error) {
	hasNodes, err := m.hasRetrievalNodes(ctx, sessionID)
	if err != nil || !hasNodes {
		return false, err
	}
	if !m.legacyBranchReadModel {
		return true, nil
	}
	hasBranches, err := m.hasRetrievalBranches(ctx, sessionID)
	if err != nil {
		return false, err
	}
	return !hasBranches, nil
}

func (m *MemoryForest) hasRetrievalBranches(ctx context.Context, sessionID string) (bool, error) {
	var count int
	err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_branches
		WHERE session_id = ? OR ? = ''
	`, normalizeForestSessionID(sessionID), strings.TrimSpace(sessionID)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count retrieval branches: %w", err)
	}
	return count > 0, nil
}

func (m *MemoryForest) retrieveNodePackets(ctx context.Context, query Query) ([]*BranchPacket, error) {
	forestPackets, err := m.retrieveForestPackets(ctx, query)
	if err != nil {
		return nil, err
	}
	packets := make([]*BranchPacket, 0, len(forestPackets))
	for _, packet := range forestPackets {
		packets = append(packets, branchPacketFromForestPacket(packet, query))
	}
	if err := m.recordForestRetrievalAccounting(ctx, query, forestPackets); err != nil {
		return nil, err
	}
	return packets, nil
}

func (m *MemoryForest) retrieveForestPackets(ctx context.Context, query Query) ([]*ForestPacket, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = m.substrateLimit
	}
	m.substrateStateMu.RLock()
	rows, err := m.db.QueryContext(ctx, `
		SELECT n.node_id, n.node_kind, n.source_partition, n.subject_type, n.subject_id, n.session_id, n.task_id,
		       n.title, n.summary, n.confidence, n.salience, n.utility, n.evidence_grade,
		       n.first_seen_at, n.last_seen_at, n.source_seq, n.status, n.novelty,
		       COALESCE(f_conf.value, 0), COALESCE(f_val.value, 0), COALESCE(f_sup.value, 0)
		FROM forest_nodes n
		LEFT JOIN forest_substrate_field f_conf
			ON f_conf.scope_type = 'node' AND f_conf.scope_id = n.node_id AND f_conf.channel = ?
		LEFT JOIN forest_substrate_field f_val
			ON f_val.scope_type = 'node' AND f_val.scope_id = n.node_id AND f_val.channel = ?
		LEFT JOIN forest_substrate_field f_sup
			ON f_sup.scope_type = 'node' AND f_sup.scope_id = n.node_id AND f_sup.channel = ?
		WHERE (n.session_id = ? OR ? = '')
		ORDER BY n.source_seq DESC, n.last_seen_at DESC
		LIMIT ?
	`, SubstrateChannelConfidence, SubstrateChannelValidation, SubstrateChannelSuppression,
		normalizeForestSessionID(query.SessionID), strings.TrimSpace(query.SessionID), limit*len(defaultEcologyPolicy().Channels))
	if err != nil {
		m.substrateStateMu.RUnlock()
		return nil, fmt.Errorf("query forest packets: %w", err)
	}
	packets := make([]*ForestPacket, 0, limit)
	for rows.Next() {
		packet, err := scanForestPacketBase(rows, query)
		if err != nil {
			rows.Close()
			m.substrateStateMu.RUnlock()
			return nil, err
		}
		packets = append(packets, packet)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		m.substrateStateMu.RUnlock()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		m.substrateStateMu.RUnlock()
		return nil, fmt.Errorf("close forest packets: %w", err)
	}
	m.substrateStateMu.RUnlock()
	hydrated := make([]*ForestPacket, 0, len(packets))
	for _, packet := range packets {
		if err := m.hydrateForestPacket(ctx, packet, query, limit); err != nil {
			return nil, err
		}
		if packet.Quarantine.Quarantined && !query.IncludeCounterEvidence {
			continue
		}
		hydrated = append(hydrated, packet)
	}
	packets = hydrated
	sort.SliceStable(packets, func(i, j int) bool {
		if packets[i].Score == packets[j].Score {
			return packets[i].Node.ID < packets[j].Node.ID
		}
		return packets[i].Score > packets[j].Score
	})
	if len(packets) > limit {
		packets = packets[:limit]
	}
	return packets, nil
}

func scanForestPacketBase(rows interface {
	Scan(dest ...any) error
}, query Query) (*ForestPacket, error) {
	var (
		id, kind, partition, subjectType, subjectID, sessionID, taskID string
		title, summary, grade, status                                  string
		confidence, salience, utility, novelty                         float64
		firstSeen, lastSeen, seq                                       int64
		fieldConfidence, validation, suppression                       float64
	)
	if err := rows.Scan(&id, &kind, &partition, &subjectType, &subjectID, &sessionID, &taskID, &title, &summary,
		&confidence, &salience, &utility, &grade, &firstSeen, &lastSeen, &seq,
		&status, &novelty, &fieldConfidence, &validation, &suppression); err != nil {
		return nil, fmt.Errorf("scan forest packet: %w", err)
	}
	queryMatch := nodeQueryMatch(query.Query, title, summary)
	substrate := clampFinite01(fieldConfidence + validation - suppression)
	total := averageClamped01(queryMatch, confidence, salience, utility, substrate)
	return &ForestPacket{
		Node: ForestNode{
			ID:              id,
			Kind:            ForestNodeKind(kind),
			SourcePartition: partition,
			SourceSeq:       seq,
			Subject:         ForestSubjectRef{Type: subjectType, ID: subjectID},
			SessionID:       sessionID,
			TaskID:          taskID,
			Title:           title,
			Summary:         summary,
			EvidenceGrade:   EvidenceGrade(grade),
			Confidence:      confidence,
			Salience:        salience,
			Utility:         utility,
			Novelty:         novelty,
			Status:          status,
			FirstSeenAt:     time.Unix(firstSeen, 0).UTC(),
			LastSeenAt:      time.Unix(lastSeen, 0).UTC(),
			Metadata: map[string]any{
				"query_match":       queryMatch,
				"substrate":         substrate,
				"validation_energy": validation,
				"suppression":       suppression,
			},
		},
		Score: total,
		PolicyVersion: ecologyPolicyVersion,
	}, nil
}

func branchPacketFromForestPacket(packet *ForestPacket, query Query) *BranchPacket {
	if packet == nil {
		return nil
	}
	node := packet.Node
	family := treeFamilyForNodeKind(node.Kind)
	queryMatch := nodeQueryMatch(query.Query, node.Title, node.Summary)
	substrate := metadataFloat(node.Metadata, "substrate")
	validation := metadataFloat(node.Metadata, "validation_energy")
	suppression := metadataFloat(node.Metadata, "suppression")
	branch := &Branch{
		ID:             node.ID,
		RootID:         stableID("node_root", node.SourcePartition, string(family)),
		Family:         family,
		Scope:          ScopeEpisodic,
		State:          branchStateForEvidence(node.EvidenceGrade),
		SessionID:      node.SessionID,
		TaskID:         node.TaskID,
		IntentID:       node.Subject.ID,
		Title:          node.Title,
		Summary:        node.Summary,
		Confidence:     node.Confidence,
		Salience:       node.Salience,
		Utility:        node.Utility,
		CreatedAt:      node.FirstSeenAt,
		UpdatedAt:      node.LastSeenAt,
		LastAppliedSeq: node.SourceSeq,
	}
	return &BranchPacket{
		Branch: branch,
		Support: []PacketEvidence{{
			ContentID:  node.ID,
			Summary:    node.Summary,
			Confidence: node.Confidence,
			Salience:   node.Salience,
			Timestamp:  branch.UpdatedAt,
		}},
		Score: PacketScore{
			Total:            packet.Score,
			Base:             packet.Score,
			QueryMatch:       queryMatch,
			Evidence:         validation,
			Substrate:        substrate,
			Frontier:         substrate,
			Confidence:       node.Confidence,
			Recency:          nodeRecencyScore(branch.UpdatedAt),
			Utility:          node.Utility,
			Salience:         node.Salience,
			Conflict:         suppression,
			ScopeSafety:      1,
			InhibitionSafety: 1 - suppression,
			RiskPenalty:      suppression,
		},
		Source: RetrievalSourcePrimary,
	}
}

func branchStateForEvidence(grade EvidenceGrade) BranchState {
	switch grade {
	case EvidenceGradeValidated:
		return BranchStateValidated
	case EvidenceGradeContradicted, EvidenceGradeFailed:
		return BranchStateContradicted
	default:
		return BranchStateActive
	}
}

func (m *MemoryForest) hydrateForestPacket(ctx context.Context, packet *ForestPacket, query Query, limit int) error {
	if packet == nil {
		return nil
	}
	clusterIDs, err := m.clusterIDsForNode(ctx, packet.Node.ID, limit)
	if err != nil {
		return err
	}
	edges, err := m.edgesForNodePacket(ctx, packet.Node.ID, limit)
	if err != nil {
		return err
	}
	artifacts, err := m.artifactsForNodePacket(ctx, packet.Node)
	if err != nil {
		return err
	}
	validations, err := m.validationsForNodePacket(ctx, packet.Node)
	if err != nil {
		return err
	}
	quarantine, err := m.quarantineForNode(ctx, packet.Node.ID)
	if err != nil {
		return err
	}
	pois, err := m.poisForForestPacket(ctx, packet.Node.ID, clusterIDs, limit)
	if err != nil {
		return err
	}
	bridgeRisks, err := m.bridgeRisksForForestPacket(ctx, packet.Node.ID, clusterIDs, limit)
	if err != nil {
		return err
	}
	proposedClaims, err := m.proposedClaimsForClusters(ctx, clusterIDs, limit)
	if err != nil {
		return err
	}
	packet.ClusterIDs = clusterIDs
	packet.Edges = edges
	packet.Artifacts = artifacts
	packet.Validations = validations
	packet.Quarantine = quarantine
	packet.Paths = forestPathsForEdges(packet.Node.ID, edges)
	packet.POIs = pois
	packet.BridgeRisks = bridgeRisks
	packet.ProposedClaims = proposedClaims
	packet.CounterEvidence = counterEvidenceForPacket(packet, query.IncludeCounterEvidence)
	packet.Evidence = evidenceForPacket(packet)
	packet.RiskScore = forestPacketRisk(packet)
	packet.ValidationNeed = forestPacketValidationNeed(packet)
	packet.PolicyVersion = forestProjectionVersionPhase789
	packet.AssemblyTimestamp = time.Now().UTC()
	if len(packet.Evidence) == 0 {
		return fmt.Errorf("forest packet %s has no evidence binding", packet.Node.ID)
	}
	packet.Score = clampFinite01(packet.Score * (1 - packet.Quarantine.Factor))
	return nil
}

func (m *MemoryForest) clusterIDsForNode(ctx context.Context, nodeID string, limit int) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT cluster_id
		FROM forest_cluster_membership
		WHERE node_id = ?
		ORDER BY membership_weight DESC, cluster_id ASC
		LIMIT ?
	`, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("query node clusters: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan node cluster: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (m *MemoryForest) edgesForNodePacket(ctx context.Context, nodeID string, limit int) ([]ForestEdge, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT edge_id, source_node_id, target_node_id, edge_kind, source_kind, source_partition,
		       source_key, source_seq, weight, evidence_grade, created_at, metadata
		FROM forest_node_edges
		WHERE source_node_id = ? OR target_node_id = ?
		ORDER BY weight DESC, source_seq DESC, edge_id ASC
		LIMIT ?
	`, nodeID, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("query node packet edges: %w", err)
	}
	defer rows.Close()
	var edges []ForestEdge
	for rows.Next() {
		edge, err := scanForestEdge(rows)
		if err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func scanForestEdge(rows interface {
	Scan(dest ...any) error
}) (ForestEdge, error) {
	var kind, grade string
	var createdAt int64
	var metadata string
	edge := ForestEdge{}
	if err := rows.Scan(&edge.ID, &edge.SourceNodeID, &edge.TargetNodeID, &kind, &edge.SourceKind, &edge.SourcePartition,
		&edge.SourceKey, &edge.SourceSeq, &edge.Weight, &grade, &createdAt, &metadata); err != nil {
		return ForestEdge{}, fmt.Errorf("scan forest edge: %w", err)
	}
	edge.Kind = ForestEdgeKind(kind)
	edge.EvidenceGrade = EvidenceGrade(grade)
	edge.CreatedAt = time.Unix(createdAt, 0).UTC()
	edge.Metadata = map[string]any{}
	if strings.TrimSpace(metadata) != "" {
		_ = unmarshalJSON(metadata, &edge.Metadata)
	}
	return edge, nil
}

func (m *MemoryForest) artifactsForNodePacket(ctx context.Context, node ForestNode) ([]ArtifactEvidenceRecord, error) {
	switch node.Subject.Type {
	case "artifact":
		return m.QueryArtifactEvidence(ctx, ArtifactEvidenceFilter{ArtifactID: node.Subject.ID, Limit: 8})
	case "claim":
		return m.QueryArtifactEvidence(ctx, ArtifactEvidenceFilter{ClaimID: node.Subject.ID, Limit: 8})
	default:
		return nil, nil
	}
}

func (m *MemoryForest) validationsForNodePacket(ctx context.Context, node ForestNode) ([]ValidationEvidenceRecord, error) {
	switch node.Subject.Type {
	case "validation":
		return m.QueryValidationEvidence(ctx, ValidationEvidenceFilter{ValidationID: node.Subject.ID, Limit: 8})
	case "artifact":
		return m.QueryValidationEvidence(ctx, ValidationEvidenceFilter{ArtifactID: node.Subject.ID, Limit: 8})
	case "claim":
		return m.QueryValidationEvidence(ctx, ValidationEvidenceFilter{ClaimID: node.Subject.ID, Limit: 8})
	default:
		return nil, nil
	}
}

func forestPathsForEdges(nodeID string, edges []ForestEdge) []ForestPath {
	paths := make([]ForestPath, 0, len(edges))
	for _, edge := range edges {
		other := edge.TargetNodeID
		if other == nodeID {
			other = edge.SourceNodeID
		}
		paths = append(paths, ForestPath{
			PathID:      stableID("forest_path", nodeID, edge.ID),
			NodeIDs:     []string{nodeID, other},
			EdgeIDs:     []string{edge.ID},
			Kinds:       []ForestEdgeKind{edge.Kind},
			Weight:      clampFinite01(edge.Weight),
			Explanation: string(edge.Kind) + " edge",
		})
	}
	return paths
}

func evidenceForPacket(packet *ForestPacket) []ForestEvidence {
	if packet == nil {
		return nil
	}
	evidence := []ForestEvidence{{
		RefType:    "node",
		RefID:      packet.Node.ID,
		NodeID:     packet.Node.ID,
		Grade:      packet.Node.EvidenceGrade,
		Summary:    firstNonEmptyString(packet.Node.Summary, packet.Node.Title),
		Confidence: packet.Node.Confidence,
	}}
	for _, artifact := range packet.Artifacts {
		evidence = append(evidence, ForestEvidence{
			RefType:    "artifact",
			RefID:      artifact.ArtifactID,
			NodeID:     packet.Node.ID,
			ArtifactID: artifact.ArtifactID,
			Grade:      packet.Node.EvidenceGrade,
			Summary:    firstNonEmptyString(artifact.ArtifactName, artifact.ArtifactKind, artifact.ContentHash),
			Confidence: packet.Node.Confidence,
		})
	}
	for _, validation := range packet.Validations {
		evidence = append(evidence, ForestEvidence{
			RefType:      "validation",
			RefID:        validation.ValidationID,
			NodeID:       packet.Node.ID,
			ValidationID: validation.ValidationID,
			Grade:        validationEvidenceGrade(validation.Status),
			Summary:      firstNonEmptyString(validation.ValidationType, validation.FailureReason, validation.Status),
			Confidence:   validationEvidenceConfidence(validation.Status),
		})
	}
	return evidence
}

func counterEvidenceForPacket(packet *ForestPacket, includeQuarantine bool) []ForestEvidence {
	if packet == nil {
		return nil
	}
	counter := make([]ForestEvidence, 0, len(packet.Edges)+len(packet.Quarantine.EvidenceRefs))
	for _, edge := range packet.Edges {
		if edge.Kind != ForestEdgeContradiction && edge.Kind != ForestEdgeSuppression {
			continue
		}
		counter = append(counter, ForestEvidence{
			RefType:    "edge",
			RefID:      edge.ID,
			NodeID:     packet.Node.ID,
			EdgeID:     edge.ID,
			Grade:      edge.EvidenceGrade,
			Summary:    string(edge.Kind) + " counter-evidence",
			Confidence: edge.Weight,
			Counter:    true,
		})
	}
	if includeQuarantine || packet.Quarantine.Quarantined {
		for _, ref := range packet.Quarantine.EvidenceRefs {
			counter = append(counter, ForestEvidence{
				RefType:    "quarantine",
				RefID:      ref,
				NodeID:     packet.Node.ID,
				Grade:      EvidenceGradeContradicted,
				Summary:    "quarantine evidence",
				Confidence: packet.Quarantine.Factor,
				Counter:    true,
			})
		}
	}
	return counter
}

func validationEvidenceGrade(status string) EvidenceGrade {
	status = strings.ToLower(strings.TrimSpace(status))
	if strings.Contains(status, "fail") || strings.Contains(status, "reject") {
		return EvidenceGradeFailed
	}
	if strings.Contains(status, "valid") || strings.Contains(status, "pass") {
		return EvidenceGradeValidated
	}
	return EvidenceGradeObserved
}

func validationEvidenceConfidence(status string) float64 {
	if validationEvidenceGrade(status) == EvidenceGradeValidated {
		return 1
	}
	if validationEvidenceGrade(status) == EvidenceGradeFailed {
		return 0
	}
	return defaultConfidence(0)
}

func forestPacketRisk(packet *ForestPacket) float64 {
	if packet == nil {
		return 0
	}
	bridgeRisk := 0.0
	for _, risk := range packet.BridgeRisks {
		if risk.ContradictionRisk > bridgeRisk {
			bridgeRisk = risk.ContradictionRisk
		}
	}
	return averageClamped01(packet.Quarantine.Factor, bridgeRisk, float64(len(packet.CounterEvidence))/float64(len(defaultEcologyPolicy().Channels)))
}

func forestPacketValidationNeed(packet *ForestPacket) float64 {
	if packet == nil {
		return 0
	}
	if len(packet.Validations) == 0 {
		return clampFinite01(1 - packet.Node.Confidence + packet.RiskScore)
	}
	return clampFinite01(packet.RiskScore)
}

func (m *MemoryForest) quarantineForNode(ctx context.Context, nodeID string) (ForestQuarantine, error) {
	corruption, dimensions, refs, err := m.vectorAggregate(ctx, "forest_antigenic_vectors", "quarantine_factor", "node", nodeID)
	if err != nil {
		return ForestQuarantine{}, err
	}
	immunity, _, recoveryRefs, err := m.vectorAggregate(ctx, "forest_immunity_vectors", "recovery_factor", "node", nodeID)
	if err != nil {
		return ForestQuarantine{}, err
	}
	factor := clampFinite01(corruption - immunity)
	threshold := 1 / float64(nodeProjectionRetryLimit())
	return ForestQuarantine{
		Quarantined:      factor >= threshold,
		Factor:           factor,
		Corruption:       clampFinite01(corruption),
		Immunity:          clampFinite01(immunity),
		Dimensions:       dimensions,
		EvidenceRefs:      dedupeStrings(append(refs, recoveryRefs...)),
		RecoveryRequired:  factor >= threshold && immunity == 0,
	}, nil
}

func (m *MemoryForest) vectorAggregate(ctx context.Context, table, factorColumn, scopeType, scopeID string) (float64, []string, []string, error) {
	if !knownVectorTable(table, factorColumn) {
		return 0, nil, nil, fmt.Errorf("unknown vector table %s.%s", table, factorColumn)
	}
	rows, err := m.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT dimension, magnitude, %s, evidence_refs
		FROM %s
		WHERE scope_type = ? AND scope_id = ? AND policy_version = ?
	`, factorColumn, table), scopeType, scopeID, antigenicPolicyVersion)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("query vector aggregate: %w", err)
	}
	defer rows.Close()
	total := 0.0
	var dimensions []string
	var refs []string
	for rows.Next() {
		var dimension, encodedRefs string
		var magnitude, factor float64
		if err := rows.Scan(&dimension, &magnitude, &factor, &encodedRefs); err != nil {
			return 0, nil, nil, fmt.Errorf("scan vector aggregate: %w", err)
		}
		total += clampFinite01(magnitude) * clampFinite01(factor)
		dimensions = append(dimensions, dimension)
		refs = append(refs, decodeStringList(encodedRefs)...)
	}
	return clampFinite01(total), dedupeStrings(dimensions), dedupeStrings(refs), rows.Err()
}

func knownVectorTable(table, column string) bool {
	return (table == "forest_antigenic_vectors" && column == "quarantine_factor") ||
		(table == "forest_immunity_vectors" && column == "recovery_factor")
}

func (m *MemoryForest) poisForForestPacket(ctx context.Context, nodeID string, clusterIDs []string, limit int) ([]ForestPOI, error) {
	ids := append([]string{nodeID}, clusterIDs...)
	placeholders, args := placeholdersForStrings(ids)
	rows, err := m.db.QueryContext(ctx, `
		SELECT poi_id, cluster_id, node_id, reason, priority, invalidation_sequence
		FROM forest_poi_cache
		WHERE node_id = ? OR cluster_id IN (`+placeholders+`)
		ORDER BY priority DESC, updated_at DESC
		LIMIT ?
	`, append([]any{nodeID}, append(args, limit)...)...)
	if err != nil {
		return nil, fmt.Errorf("query packet poi: %w", err)
	}
	defer rows.Close()
	var pois []ForestPOI
	for rows.Next() {
		var poi ForestPOI
		if err := rows.Scan(&poi.POIID, &poi.ClusterID, &poi.NodeID, &poi.Reason, &poi.Priority, &poi.InvalidationSequence); err != nil {
			return nil, fmt.Errorf("scan packet poi: %w", err)
		}
		pois = append(pois, poi)
	}
	return pois, rows.Err()
}

func (m *MemoryForest) bridgeRisksForForestPacket(ctx context.Context, nodeID string, clusterIDs []string, limit int) ([]ForestBridgeRisk, error) {
	if len(clusterIDs) == 0 {
		return m.bridgeRisksForNodeOnly(ctx, nodeID, limit)
	}
	placeholders, args := placeholdersForStrings(clusterIDs)
	rows, err := m.db.QueryContext(ctx, `
		SELECT bridge_id, node_id, source_cluster_id, target_cluster_id, bridge_score,
		       contradiction_risk, source_edge_ids
		FROM forest_bridge_nodes
		WHERE node_id = ? OR source_cluster_id IN (`+placeholders+`) OR target_cluster_id IN (`+placeholders+`)
		ORDER BY bridge_score DESC, updated_at DESC
		LIMIT ?
	`, append([]any{nodeID}, append(append(args, args...), limit)...)...)
	if err != nil {
		return nil, fmt.Errorf("query packet bridge risk: %w", err)
	}
	defer rows.Close()
	var risks []ForestBridgeRisk
	for rows.Next() {
		var risk ForestBridgeRisk
		var edges string
		if err := rows.Scan(&risk.BridgeID, &risk.NodeID, &risk.SourceClusterID, &risk.TargetClusterID, &risk.Score, &risk.ContradictionRisk, &edges); err != nil {
			return nil, fmt.Errorf("scan packet bridge risk: %w", err)
		}
		risk.GuardianReviewRequired = risk.ContradictionRisk >= 1/float64(nodeProjectionRetryLimit())
		risk.SourceEdgeIDs = decodeStringList(edges)
		risks = append(risks, risk)
	}
	return risks, rows.Err()
}

func (m *MemoryForest) bridgeRisksForNodeOnly(ctx context.Context, nodeID string, limit int) ([]ForestBridgeRisk, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT bridge_id, node_id, source_cluster_id, target_cluster_id, bridge_score,
		       contradiction_risk, source_edge_ids
		FROM forest_bridge_nodes
		WHERE node_id = ?
		ORDER BY bridge_score DESC, updated_at DESC
		LIMIT ?
	`, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("query node bridge risk: %w", err)
	}
	defer rows.Close()
	var risks []ForestBridgeRisk
	for rows.Next() {
		var risk ForestBridgeRisk
		var edges string
		if err := rows.Scan(&risk.BridgeID, &risk.NodeID, &risk.SourceClusterID, &risk.TargetClusterID, &risk.Score, &risk.ContradictionRisk, &edges); err != nil {
			return nil, fmt.Errorf("scan node bridge risk: %w", err)
		}
		risk.GuardianReviewRequired = risk.ContradictionRisk >= 1/float64(nodeProjectionRetryLimit())
		risk.SourceEdgeIDs = decodeStringList(edges)
		risks = append(risks, risk)
	}
	return risks, rows.Err()
}

func (m *MemoryForest) proposedClaimsForClusters(ctx context.Context, clusterIDs []string, limit int) ([]ForestClaimProposalTemplate, error) {
	if len(clusterIDs) == 0 {
		return nil, nil
	}
	placeholders, args := placeholdersForStrings(clusterIDs)
	args = append(args, limit)
	rows, err := m.db.QueryContext(ctx, `
		SELECT proposed_claim_id, cluster_id, dimension, evidence_refs, counter_evidence_refs,
		       guardian_review_required
		FROM forest_outbreaks
		WHERE status = ? AND cluster_id IN (`+placeholders+`)
		ORDER BY growth_rate DESC, updated_at DESC
		LIMIT ?
	`, append([]any{OutbreakStatusProposed}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("query proposed claims: %w", err)
	}
	defer rows.Close()
	var proposals []ForestClaimProposalTemplate
	for rows.Next() {
		var proposal ForestClaimProposalTemplate
		var evidenceRefs, counterRefs string
		var guardian int
		if err := rows.Scan(&proposal.ProposalID, &proposal.ClusterID, &proposal.Dimension, &evidenceRefs, &counterRefs, &guardian); err != nil {
			return nil, fmt.Errorf("scan proposed claim: %w", err)
		}
		proposal.Summary = "Remediate " + proposal.Dimension + " in cluster " + proposal.ClusterID
		proposal.EvidenceRefs = decodeStringList(evidenceRefs)
		proposal.CounterEvidenceRefs = decodeStringList(counterRefs)
		proposal.GuardianReviewRequired = guardian != 0
		proposals = append(proposals, proposal)
	}
	return proposals, rows.Err()
}

func nodeQueryMatch(query, title, summary string) float64 {
	query = normalizeText(query)
	if query == "" {
		return defaultSalience(0)
	}
	haystack := normalizeText(title + " " + summary)
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return defaultSalience(0)
	}
	matches := 0
	for _, term := range terms {
		if strings.Contains(haystack, term) {
			matches++
		}
	}
	return clamp01(float64(matches) / float64(len(terms)))
}

func nodeRecencyScore(at time.Time) float64 {
	if at.IsZero() {
		return 0
	}
	age := time.Since(at)
	if age <= 0 {
		return 1
	}
	return clamp01(1 / (1 + age.Hours()/24))
}

func averageClamped01(values ...float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += clampFinite01(value)
	}
	return clamp01(total / float64(len(values)))
}

func (m *MemoryForest) recordForestRetrievalAccounting(ctx context.Context, query Query, packets []*ForestPacket) error {
	if len(packets) == 0 {
		return nil
	}
	m.substrateStateMu.Lock()
	defer m.substrateStateMu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node retrieval accounting tx: %w", err)
	}
	defer tx.Rollback()
	nowTime := time.Now().UTC()
	now := nowTime.Unix()
	callKey := stableID("retrieval_exposure", normalizeForestSessionID(query.SessionID), strings.TrimSpace(query.AgentID), strings.TrimSpace(query.Query), fmt.Sprint(nowTime.UnixNano()))
	for _, packet := range packets {
		if packet == nil || strings.TrimSpace(packet.Node.ID) == "" {
			continue
		}
		sourceKey := "retrieval:" + callKey + ":" + packet.Node.ID
		if err := recordResourceBalanceWithAgentTx(ctx, tx, "node", packet.Node.ID, query.AgentID, "retrieval_exposure", -clampFinite01(packet.Score), sourceKey, now); err != nil {
			return err
		}
		if err := recordClusterExposureTx(ctx, tx, packet.Node.ID, query.AgentID, packet.Score, sourceKey, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit node retrieval accounting tx: %w", err)
	}
	return nil
}
