package forest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	projectorNodeGraphName     = "node_graph_projector"
	nodeGraphProjectionPolicy  = "node_graph_v1"
	nodeProjectionFailureKind  = "node_projection_failed"
	nodeProjectionFailureEvent = "forest.node_projection_failed"
)

type NodeProjectionResult struct {
	RecordsProcessed int
	NodesUpserted    int
	EdgesUpserted    int
	Failures         int
	LastSeq          int64
}

type forestNodeProjectionOps struct {
	Nodes []ForestNode
	Edges []ForestEdge
}

type forestLedgerProjectionRecord struct {
	Seq              int64
	ID               string
	SourceKind       string
	SourceID         string
	SourceKey        string
	EventKind        string
	SessionID        string
	TaskID           string
	BoardID          string
	SubjectType      string
	SubjectID        string
	ActorUID         string
	ActorType        string
	ActorCategory    string
	OccurredAt       time.Time
	PayloadHash      string
	PayloadJSON      string
	Refs             []claims.DeltaRef
	DeliveryRoutes   []string
	DeliveryRelation string
}

func (m *MemoryForest) startNodeProjector() {
	if m == nil {
		return
	}
	m.startWorker(projectorNodeGraphName, projectorBatchSize, func(ctx context.Context) error {
		m.runNodeProjectorLoop(ctx)
		return nil
	})
}

func (m *MemoryForest) runNodeProjectorLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-m.projectorWake:
		case <-time.After(projectorRenewInterval):
		}
		for {
			result, err := m.runNodeProjection(ctx, projectorBatchSize)
			if err != nil {
				m.logger.Warn("forest_node_projector_failed", "err", err.Error())
				break
			}
			if result.RecordsProcessed > 0 {
				if _, err := m.runClusterMaintenance(ctx, m.substrateLimit); err != nil {
					m.logger.Warn("forest_cluster_maintenance_failed", "err", err.Error())
				}
				if _, err := m.runSubstrateMaintenance(ctx, m.substrateLimit); err != nil {
					m.logger.Warn("forest_ecological_substrate_failed", "err", err.Error())
				}
			}
			if result.RecordsProcessed < projectorBatchSize {
				break
			}
		}
	}
}

func (m *MemoryForest) RunNodeProjection(ctx context.Context, limit int) (NodeProjectionResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()
	return m.runNodeProjection(ctx, limit)
}

func (m *MemoryForest) projectNodeGraphThroughSeq(ctx context.Context, targetSeq int64) error {
	if targetSeq <= 0 {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastSeq, err := m.nodeProjectionOffset(ctx)
		if err != nil {
			return err
		}
		if lastSeq >= targetSeq {
			return nil
		}
		result, err := m.runNodeProjection(ctx, projectorBatchSize)
		if err != nil {
			return err
		}
		if result.RecordsProcessed == 0 && result.Failures == 0 {
			return fmt.Errorf("node graph projection made no progress toward seq %d from %d", targetSeq, lastSeq)
		}
	}
}

func (m *MemoryForest) runNodeProjection(ctx context.Context, limit int) (NodeProjectionResult, error) {
	if limit <= 0 {
		limit = projectorBatchSize
	}
	lastSeq, err := m.nodeProjectionOffset(ctx)
	if err != nil {
		return NodeProjectionResult{}, err
	}
	records, err := m.loadLedgerProjectionRecords(ctx, lastSeq, limit)
	if err != nil {
		return NodeProjectionResult{}, err
	}
	if len(records) == 0 {
		return NodeProjectionResult{LastSeq: lastSeq}, nil
	}

	var total NodeProjectionResult
	for _, record := range records {
		applied, err := m.applyNodeProjectionRecord(ctx, record)
		if err != nil {
			return total, err
		}
		total.RecordsProcessed++
		total.NodesUpserted += applied.NodesUpserted
		total.EdgesUpserted += applied.EdgesUpserted
		total.Failures += applied.Failures
		total.LastSeq = record.Seq
	}
	return total, nil
}

func (m *MemoryForest) applyNodeProjectionRecord(ctx context.Context, record forestLedgerProjectionRecord) (NodeProjectionResult, error) {
	ops, err := nodeProjectionOpsForLedgerRecord(record)
	if err != nil {
		return m.recordNodeProjectionFailure(ctx, record, err)
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeProjectionResult{}, fmt.Errorf("begin node projection tx: %w", err)
	}
	defer tx.Rollback()

	var result NodeProjectionResult
	for _, node := range ops.Nodes {
		inserted, err := upsertForestNodeTx(ctx, tx, node)
		if err != nil {
			return NodeProjectionResult{}, err
		}
		if inserted {
			result.NodesUpserted++
		}
	}
	for _, edge := range ops.Edges {
		inserted, err := upsertForestEdgeTx(ctx, tx, edge)
		if err != nil {
			return NodeProjectionResult{}, err
		}
		if inserted {
			result.EdgesUpserted++
		}
	}
	if record.SessionID != "" {
		if err := markSubstrateDirtyTx(ctx, tx, record.SessionID, record.OccurredAt); err != nil {
			return NodeProjectionResult{}, err
		}
	}
	if err := markNodeProjectionOffsetTx(ctx, tx, record.Seq, record.SourcePartition(), nodeGraphProjectionPolicy); err != nil {
		return NodeProjectionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return NodeProjectionResult{}, fmt.Errorf("commit node projection tx: %w", err)
	}
	result.LastSeq = record.Seq
	return result, nil
}

func (m *MemoryForest) recordNodeProjectionFailure(ctx context.Context, record forestLedgerProjectionRecord, cause error) (NodeProjectionResult, error) {
	attempts, err := m.incrementNodeProjectionFailure(ctx, record, cause)
	if err != nil {
		return NodeProjectionResult{}, err
	}
	if attempts < nodeProjectionRetryLimit() {
		return NodeProjectionResult{}, fmt.Errorf("node projection retry %d/%d for %s: %w", attempts, nodeProjectionRetryLimit(), record.SourceKey, cause)
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeProjectionResult{}, fmt.Errorf("begin node projection failure tx: %w", err)
	}
	defer tx.Rollback()
	artifactID := "projection_failure:" + stableID(projectorNodeGraphName, record.SourceKey)
	if err := upsertProjectionFailureArtifactTx(ctx, tx, record, artifactID, cause); err != nil {
		return NodeProjectionResult{}, err
	}
	if err := resolveNodeProjectionFailureTx(ctx, tx, record, artifactID); err != nil {
		return NodeProjectionResult{}, err
	}
	if err := markNodeProjectionOffsetTx(ctx, tx, record.Seq, record.SourcePartition(), nodeGraphProjectionPolicy); err != nil {
		return NodeProjectionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return NodeProjectionResult{}, fmt.Errorf("commit node projection failure tx: %w", err)
	}
	return NodeProjectionResult{Failures: 1, LastSeq: record.Seq}, nil
}

func (m *MemoryForest) incrementNodeProjectionFailure(ctx context.Context, record forestLedgerProjectionRecord, cause error) (int, error) {
	now := time.Now().UTC().Unix()
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO forest_projection_failures
			(projection_name, source_key, source_seq, source_partition, attempts, failure_kind, message, critical, last_attempt_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, 0, ?)
		ON CONFLICT(projection_name, source_key) DO UPDATE SET
			attempts = forest_projection_failures.attempts + 1,
			source_seq = excluded.source_seq,
			source_partition = excluded.source_partition,
			failure_kind = excluded.failure_kind,
			message = excluded.message,
			last_attempt_at = excluded.last_attempt_at
	`, projectorNodeGraphName, record.SourceKey, record.Seq, record.SourcePartition(), nodeProjectionFailureKind, truncateRuntimeError(cause.Error()), now)
	if err != nil {
		return 0, fmt.Errorf("record node projection failure: %w", err)
	}
	var attempts int
	if err := m.db.QueryRowContext(ctx, `
		SELECT attempts
		FROM forest_projection_failures
		WHERE projection_name = ? AND source_key = ?
	`, projectorNodeGraphName, record.SourceKey).Scan(&attempts); err != nil {
		return 0, fmt.Errorf("load node projection failure attempts: %w", err)
	}
	return attempts, nil
}

func nodeProjectionRetryLimit() int {
	return len([]ForestEdgeKind{ForestEdgeCausality, ForestEdgeSupport, ForestEdgeValidation})
}

func (m *MemoryForest) nodeProjectionOffset(ctx context.Context) (int64, error) {
	var lastSeq int64
	err := m.db.QueryRowContext(ctx, `
		SELECT last_seq
		FROM forest_projection_offsets
		WHERE projection_name = ?
	`, projectorNodeGraphName).Scan(&lastSeq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load node projection offset: %w", err)
	}
	return lastSeq, nil
}

func (m *MemoryForest) loadLedgerProjectionRecords(ctx context.Context, afterSeq int64, limit int) ([]forestLedgerProjectionRecord, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT l.seq, l.id, l.source_kind, l.source_id, l.source_key, l.event_kind,
		       l.session_id, l.task_id, l.board_id, l.subject_type, l.subject_id,
		       l.actor_uid, l.actor_type, l.actor_category, l.occurred_at,
		       l.payload_hash, COALESCE(p.payload, '')
		FROM forest_ledger l
		LEFT JOIN forest_ledger_payloads p ON p.ledger_id = l.id
		WHERE l.seq > ?
		ORDER BY l.seq ASC
		LIMIT ?
	`, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("query ledger projection records: %w", err)
	}
	defer rows.Close()

	var records []forestLedgerProjectionRecord
	for rows.Next() {
		var occurredAt int64
		record := forestLedgerProjectionRecord{}
		if err := rows.Scan(&record.Seq, &record.ID, &record.SourceKind, &record.SourceID, &record.SourceKey,
			&record.EventKind, &record.SessionID, &record.TaskID, &record.BoardID, &record.SubjectType,
			&record.SubjectID, &record.ActorUID, &record.ActorType, &record.ActorCategory, &occurredAt,
			&record.PayloadHash, &record.PayloadJSON); err != nil {
			return nil, fmt.Errorf("scan ledger projection record: %w", err)
		}
		record.OccurredAt = time.Unix(occurredAt, 0).UTC()
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := m.attachProjectionRecordRefs(ctx, records); err != nil {
		return nil, err
	}
	return records, nil
}

func (m *MemoryForest) attachProjectionRecordRefs(ctx context.Context, records []forestLedgerProjectionRecord) error {
	for i := range records {
		refs, err := m.loadLedgerRefs(ctx, records[i].ID)
		if err != nil {
			return err
		}
		records[i].Refs = refs
		routes, relation, err := m.loadLedgerDeliveryRoutes(ctx, records[i].ID)
		if err != nil {
			return err
		}
		records[i].DeliveryRoutes = routes
		records[i].DeliveryRelation = relation
	}
	return nil
}

func (m *MemoryForest) loadLedgerRefs(ctx context.Context, ledgerID string) ([]claims.DeltaRef, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT role, ref_type, ref_id
		FROM forest_ledger_refs
		WHERE ledger_id = ?
		ORDER BY ref_index ASC
	`, ledgerID)
	if err != nil {
		return nil, fmt.Errorf("query ledger refs: %w", err)
	}
	defer rows.Close()
	var refs []claims.DeltaRef
	for rows.Next() {
		var ref claims.DeltaRef
		if err := rows.Scan(&ref.Role, &ref.Type, &ref.ID); err != nil {
			return nil, fmt.Errorf("scan ledger ref: %w", err)
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (m *MemoryForest) loadLedgerDeliveryRoutes(ctx context.Context, ledgerID string) ([]string, string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT relationship, participant_route_key
		FROM forest_ledger_delivery
		WHERE ledger_id = ?
		ORDER BY delivery_index ASC
	`, ledgerID)
	if err != nil {
		return nil, "", fmt.Errorf("query ledger delivery: %w", err)
	}
	defer rows.Close()
	var routes []string
	var relation string
	for rows.Next() {
		var route string
		if err := rows.Scan(&relation, &route); err != nil {
			return nil, "", fmt.Errorf("scan ledger delivery: %w", err)
		}
		if strings.TrimSpace(route) != "" {
			routes = append(routes, route)
		}
	}
	return dedupeStrings(routes), relation, rows.Err()
}

func nodeProjectionOpsForLedgerRecord(record forestLedgerProjectionRecord) (forestNodeProjectionOps, error) {
	subject := record.Subject()
	kind := forestNodeKindForRecord(record)
	grade := evidenceGradeForRecord(record)
	node := ForestNode{
		Kind:            kind,
		SourceKind:      record.SourceKind,
		SourcePartition: record.SourcePartition(),
		SourceKey:       record.SourceKey,
		SourceSeq:       record.Seq,
		Subject:         subject,
		SessionID:       record.SessionID,
		TaskID:          record.TaskID,
		Title:           nodeTitleForRecord(record, kind),
		Summary:         nodeSummaryForRecord(record),
		EvidenceGrade:   grade,
		Confidence:      recordConfidence(record),
		Salience:        recordSalience(record),
		Utility:         recordUtility(record),
		Novelty:         recordNovelty(record),
		Status:          statusForRecord(record),
		PolicyVersion:   nodeGraphProjectionPolicy,
		FirstSeenAt:     record.OccurredAt,
		LastSeenAt:      record.OccurredAt,
		PayloadHash:     record.PayloadHash,
		Metadata: map[string]any{
			"event_kind": record.EventKind,
			"ledger_id":  record.ID,
			"board_id":   record.BoardID,
		},
	}
	node, err := normalizeForestNode(node)
	if err != nil {
		return forestNodeProjectionOps{}, err
	}
	ops := forestNodeProjectionOps{Nodes: []ForestNode{node}}

	if actor := record.ActorSubject(); actor.ID != "" {
		actorNode := referenceNodeForRecord(record, ForestNodeAgent, actor, EvidenceGradeObserved)
		ops.Nodes = append(ops.Nodes, actorNode)
		ops.Edges = append(ops.Edges, edgeForRecord(record, actorNode.ID, node.ID, ForestEdgeCausality, EvidenceGradeObserved, recordConfidence(record)))
	}
	for _, route := range record.DeliveryRoutes {
		participant := ForestSubjectRef{Type: "participant", ID: route}
		participantNode := referenceNodeForRecord(record, ForestNodeAgent, participant, EvidenceGradeReference)
		ops.Nodes = append(ops.Nodes, participantNode)
		ops.Edges = append(ops.Edges, edgeForRecord(record, node.ID, participantNode.ID, ForestEdgeTraversal, EvidenceGradeObserved, recordSalience(record)))
	}
	for _, ref := range record.Refs {
		targetSubject := ForestSubjectRef{Type: strings.TrimSpace(ref.Type), ID: strings.TrimSpace(ref.ID)}
		if targetSubject.Type == "" || targetSubject.ID == "" {
			continue
		}
		target := referenceNodeForRecord(record, forestNodeKindForSubject(targetSubject.Type, ref.Role), targetSubject, EvidenceGradeReference)
		ops.Nodes = append(ops.Nodes, target)
		ops.Edges = append(ops.Edges, edgeForRecord(record, node.ID, target.ID, edgeKindForRef(record, ref), grade, recordSalience(record)))
	}
	return dedupeNodeProjectionOps(ops)
}

func dedupeNodeProjectionOps(ops forestNodeProjectionOps) (forestNodeProjectionOps, error) {
	nodes := make(map[string]ForestNode, len(ops.Nodes))
	for _, node := range ops.Nodes {
		normalized, err := normalizeForestNode(node)
		if err != nil {
			return forestNodeProjectionOps{}, err
		}
		nodes[normalized.ID] = normalized
	}
	edges := make(map[string]ForestEdge, len(ops.Edges))
	for _, edge := range ops.Edges {
		normalized, err := normalizeForestEdge(edge)
		if err != nil {
			return forestNodeProjectionOps{}, err
		}
		edges[normalized.ID] = normalized
	}
	result := forestNodeProjectionOps{
		Nodes: make([]ForestNode, 0, len(nodes)),
		Edges: make([]ForestEdge, 0, len(edges)),
	}
	for _, node := range nodes {
		result.Nodes = append(result.Nodes, node)
	}
	for _, edge := range edges {
		result.Edges = append(result.Edges, edge)
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	return result, nil
}

func (r forestLedgerProjectionRecord) Subject() ForestSubjectRef {
	if strings.TrimSpace(r.SubjectType) != "" && strings.TrimSpace(r.SubjectID) != "" {
		return ForestSubjectRef{Type: r.SubjectType, ID: r.SubjectID}
	}
	for _, ref := range r.Refs {
		if strings.TrimSpace(ref.Type) != "" && strings.TrimSpace(ref.ID) != "" {
			return ForestSubjectRef{Type: ref.Type, ID: ref.ID}
		}
	}
	return ForestSubjectRef{Type: "ledger", ID: r.ID}
}

func (r forestLedgerProjectionRecord) ActorSubject() ForestSubjectRef {
	route := firstNonEmptyString(r.ActorUID, r.ActorType)
	if route == "" {
		return ForestSubjectRef{}
	}
	return ForestSubjectRef{Type: "participant", ID: route}
}

func (r forestLedgerProjectionRecord) SourcePartition() string {
	if strings.TrimSpace(r.BoardID) != "" {
		return r.SourceKind + ":board:" + r.BoardID
	}
	if strings.TrimSpace(r.SessionID) != "" {
		return r.SourceKind + ":session:" + normalizeForestSessionID(r.SessionID)
	}
	return r.SourceKind + ":global"
}

func referenceNodeForRecord(record forestLedgerProjectionRecord, kind ForestNodeKind, subject ForestSubjectRef, grade EvidenceGrade) ForestNode {
	node := ForestNode{
		Kind:            kind,
		SourceKind:      record.SourceKind,
		SourcePartition: "reference",
		SourceKey:       "ref:" + subject.Type + ":" + subject.ID,
		SourceSeq:       record.Seq,
		Subject:         subject,
		SessionID:       record.SessionID,
		TaskID:          record.TaskID,
		Title:           subject.ID,
		Summary:         subject.Type + ":" + subject.ID,
		EvidenceGrade:   grade,
		Confidence:      recordConfidence(record),
		Salience:        recordSalience(record),
		Utility:         recordUtility(record),
		Novelty:         recordNovelty(record),
		PolicyVersion:   nodeGraphProjectionPolicy,
		FirstSeenAt:     record.OccurredAt,
		LastSeenAt:      record.OccurredAt,
		PayloadHash:     record.PayloadHash,
		Metadata:        map[string]any{"reference": true},
	}
	normalized, err := normalizeForestNode(node)
	if err != nil {
		return node
	}
	return normalized
}

func edgeForRecord(record forestLedgerProjectionRecord, sourceNodeID, targetNodeID string, kind ForestEdgeKind, grade EvidenceGrade, weight float64) ForestEdge {
	edge := ForestEdge{
		SourceNodeID:    sourceNodeID,
		TargetNodeID:    targetNodeID,
		Kind:            kind,
		SourceKind:      record.SourceKind,
		SourcePartition: record.SourcePartition(),
		SourceKey:       record.SourceKey,
		SourceSeq:       record.Seq,
		Weight:          weight,
		EvidenceGrade:   grade,
		CreatedAt:       record.OccurredAt,
		Metadata:        map[string]any{"ledger_id": record.ID, "event_kind": record.EventKind},
	}
	normalized, err := normalizeForestEdge(edge)
	if err != nil {
		return edge
	}
	return normalized
}

func forestNodeKindForRecord(record forestLedgerProjectionRecord) ForestNodeKind {
	eventKind := strings.ToLower(record.EventKind)
	switch {
	case strings.Contains(eventKind, "skill"):
		return ForestNodeSkillCandidate
	case strings.Contains(eventKind, "policy"):
		return ForestNodePolicyTrial
	case strings.Contains(eventKind, "contradiction") || strings.Contains(eventKind, "validation_failed"):
		return ForestNodeContradiction
	case strings.Contains(eventKind, "validation"):
		return ForestNodeValidation
	case strings.Contains(eventKind, "artifact"):
		return ForestNodeArtifact
	case strings.Contains(eventKind, "testament"):
		return ForestNodeTestament
	case strings.Contains(eventKind, "outcome") || strings.Contains(eventKind, "satisfied"):
		return ForestNodeOutcome
	case strings.Contains(eventKind, "remediation"):
		return ForestNodeRemediation
	case strings.Contains(eventKind, "claim"):
		return ForestNodeClaim
	case strings.Contains(eventKind, "content"):
		return ForestNodeContent
	case record.SourceKind == string(LedgerSourceMaintenance):
		return ForestNodeMaintenance
	default:
		return forestNodeKindForSubject(record.SubjectType, "")
	}
}

func forestNodeKindForSubject(subjectType, role string) ForestNodeKind {
	subjectType = strings.ToLower(strings.TrimSpace(subjectType))
	role = strings.ToLower(strings.TrimSpace(role))
	switch {
	case subjectType == claims.RelatedTypeClaim:
		return ForestNodeClaim
	case subjectType == claims.RelatedTypeArtifact:
		return ForestNodeArtifact
	case subjectType == claims.RelatedTypeValidation:
		return ForestNodeValidation
	case subjectType == claims.RelatedTypeTestament:
		return ForestNodeTestament
	case subjectType == "content":
		return ForestNodeContent
	case subjectType == "agent" || subjectType == "participant":
		return ForestNodeAgent
	case strings.Contains(role, "remed"):
		return ForestNodeRemediation
	default:
		return ForestNodeInteraction
	}
}

func edgeKindForRef(record forestLedgerProjectionRecord, ref claims.DeltaRef) ForestEdgeKind {
	role := strings.ToLower(ref.Role)
	eventKind := strings.ToLower(record.EventKind)
	switch {
	case strings.Contains(role, "contradict") || strings.Contains(eventKind, "contradiction"):
		return ForestEdgeContradiction
	case strings.Contains(role, "validation") || strings.Contains(eventKind, "validation"):
		return ForestEdgeValidation
	case strings.Contains(role, "supersede") || strings.Contains(role, "lineage"):
		return ForestEdgeLineage
	case strings.Contains(role, "remed"):
		return ForestEdgeRemediation
	case strings.Contains(role, "suppress"):
		return ForestEdgeSuppression
	case strings.Contains(role, "travers"):
		return ForestEdgeTraversal
	case strings.Contains(role, "co") && strings.Contains(role, "use"):
		return ForestEdgeCoUse
	case strings.Contains(role, "similar"):
		return ForestEdgeSimilarity
	default:
		return ForestEdgeSupport
	}
}

func evidenceGradeForRecord(record forestLedgerProjectionRecord) EvidenceGrade {
	eventKind := strings.ToLower(record.EventKind)
	switch {
	case strings.Contains(eventKind, "validated") || strings.Contains(eventKind, "satisfied"):
		return EvidenceGradeValidated
	case strings.Contains(eventKind, "failed") || strings.Contains(eventKind, "rejected") || strings.Contains(eventKind, "errored"):
		return EvidenceGradeFailed
	case strings.Contains(eventKind, "contradiction"):
		return EvidenceGradeContradicted
	case strings.Contains(eventKind, "generated"):
		return EvidenceGradeGenerated
	default:
		return EvidenceGradeObserved
	}
}

func nodeTitleForRecord(record forestLedgerProjectionRecord, kind ForestNodeKind) string {
	payload := payloadMap(record.PayloadJSON)
	for _, path := range [][]string{{"claim", "title"}, {"artifact", "name"}, {"validation", "type"}, {"transition", "status"}} {
		if value := nestedPayloadString(payload, path...); value != "" {
			return value
		}
	}
	return strings.TrimSpace(string(kind) + " " + record.Subject().ID)
}

func nodeSummaryForRecord(record forestLedgerProjectionRecord) string {
	payload := payloadMap(record.PayloadJSON)
	for _, path := range [][]string{{"claim", "description"}, {"artifact", "summary"}, {"validation", "reason"}, {"transition", "reason"}} {
		if value := nestedPayloadString(payload, path...); value != "" {
			return value
		}
	}
	return record.EventKind
}

func statusForRecord(record forestLedgerProjectionRecord) string {
	payload := payloadMap(record.PayloadJSON)
	for _, path := range [][]string{{"transition", "status"}, {"artifact", "status"}, {"validation", "status"}, {"claim", "status"}} {
		if value := nestedPayloadString(payload, path...); value != "" {
			return value
		}
	}
	parts := strings.Split(record.EventKind, ".")
	return parts[len(parts)-1]
}

func recordConfidence(record forestLedgerProjectionRecord) float64 {
	grade := evidenceGradeForRecord(record)
	if grade == EvidenceGradeValidated {
		return 1
	}
	if grade == EvidenceGradeFailed || grade == EvidenceGradeContradicted {
		return 0
	}
	return defaultConfidence(0)
}

func recordSalience(record forestLedgerProjectionRecord) float64 {
	if len(record.Refs) == 0 {
		return defaultSalience(0)
	}
	return clamp01(float64(len(record.Refs)) / float64(len(record.Refs)+1))
}

func recordUtility(record forestLedgerProjectionRecord) float64 {
	eventKind := strings.ToLower(record.EventKind)
	if strings.Contains(eventKind, "satisfied") || strings.Contains(eventKind, "validated") {
		return 1
	}
	if strings.Contains(eventKind, "failed") || strings.Contains(eventKind, "rejected") {
		return 0
	}
	return clamp01(recordSalience(record) * defaultConfidence(0))
}

func recordNovelty(record forestLedgerProjectionRecord) float64 {
	if record.Seq <= 0 {
		return 0
	}
	return 1 / float64(1+len(record.Refs))
}

func payloadMap(payload string) map[string]any {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil
	}
	return decoded
}

func nestedPayloadString(payload map[string]any, path ...string) string {
	var current any = payload
	for _, key := range path {
		next, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = next[key]
	}
	switch v := current.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func upsertForestNodeTx(ctx context.Context, tx *sql.Tx, node ForestNode) (bool, error) {
	normalized, err := normalizeForestNode(node)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO forest_nodes
			(node_id, node_kind, source_kind, source_partition, source_key, source_seq,
			 subject_type, subject_id, session_id, task_id, title, summary, evidence_grade,
			 confidence, salience, utility, novelty, status, policy_version, first_seen_at,
			 last_seen_at, payload_hash, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			source_seq = CASE WHEN excluded.source_seq > forest_nodes.source_seq THEN excluded.source_seq ELSE forest_nodes.source_seq END,
			title = CASE WHEN excluded.title != '' THEN excluded.title ELSE forest_nodes.title END,
			summary = CASE WHEN excluded.summary != '' THEN excluded.summary ELSE forest_nodes.summary END,
			evidence_grade = excluded.evidence_grade,
			confidence = CASE WHEN excluded.confidence > forest_nodes.confidence THEN excluded.confidence ELSE forest_nodes.confidence END,
			salience = CASE WHEN excluded.salience > forest_nodes.salience THEN excluded.salience ELSE forest_nodes.salience END,
			utility = CASE WHEN excluded.utility > forest_nodes.utility THEN excluded.utility ELSE forest_nodes.utility END,
			novelty = CASE WHEN excluded.novelty > forest_nodes.novelty THEN excluded.novelty ELSE forest_nodes.novelty END,
			status = CASE WHEN excluded.status != '' THEN excluded.status ELSE forest_nodes.status END,
			policy_version = excluded.policy_version,
			last_seen_at = CASE WHEN excluded.last_seen_at > forest_nodes.last_seen_at THEN excluded.last_seen_at ELSE forest_nodes.last_seen_at END,
			payload_hash = CASE WHEN excluded.payload_hash != '' THEN excluded.payload_hash ELSE forest_nodes.payload_hash END,
			metadata = CASE WHEN excluded.metadata != '' THEN excluded.metadata ELSE forest_nodes.metadata END
	`, normalized.ID, string(normalized.Kind), normalized.SourceKind, normalized.SourcePartition, normalized.SourceKey, normalized.SourceSeq,
		normalized.Subject.Type, normalized.Subject.ID, normalized.SessionID, normalized.TaskID, normalized.Title, normalized.Summary,
		string(normalized.EvidenceGrade), normalized.Confidence, normalized.Salience, normalized.Utility, normalized.Novelty, normalized.Status,
		normalized.PolicyVersion, normalized.FirstSeenAt.Unix(), normalized.LastSeenAt.Unix(), normalized.PayloadHash, marshalJSON(normalized.Metadata))
	if err != nil {
		return false, fmt.Errorf("upsert forest node: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

func upsertForestEdgeTx(ctx context.Context, tx *sql.Tx, edge ForestEdge) (bool, error) {
	normalized, err := normalizeForestEdge(edge)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO forest_node_edges
			(edge_id, source_node_id, target_node_id, edge_kind, source_kind, source_partition,
			 source_key, source_seq, weight, evidence_grade, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(edge_id) DO UPDATE SET
			source_seq = CASE WHEN excluded.source_seq > forest_node_edges.source_seq THEN excluded.source_seq ELSE forest_node_edges.source_seq END,
			weight = CASE WHEN excluded.weight > forest_node_edges.weight THEN excluded.weight ELSE forest_node_edges.weight END,
			evidence_grade = excluded.evidence_grade,
			metadata = CASE WHEN excluded.metadata != '' THEN excluded.metadata ELSE forest_node_edges.metadata END
	`, normalized.ID, normalized.SourceNodeID, normalized.TargetNodeID, string(normalized.Kind), normalized.SourceKind, normalized.SourcePartition,
		normalized.SourceKey, normalized.SourceSeq, normalized.Weight, string(normalized.EvidenceGrade), normalized.CreatedAt.Unix(), marshalJSON(normalized.Metadata))
	if err != nil {
		return false, fmt.Errorf("upsert forest edge: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

func markNodeProjectionOffsetTx(ctx context.Context, tx *sql.Tx, seq int64, partition, policy string) error {
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_projection_offsets
			(projection_name, source_kind, last_seq, last_applied_at, policy_version, health_status, updated_at)
		VALUES (?, ?, ?, ?, ?, 'healthy', ?)
		ON CONFLICT(projection_name) DO UPDATE SET
			last_seq = excluded.last_seq,
			last_applied_at = excluded.last_applied_at,
			policy_version = excluded.policy_version,
			health_status = excluded.health_status,
			last_error = '',
			updated_at = excluded.updated_at
	`, projectorNodeGraphName, LedgerSourceClaimsDelta, seq, now, policy, now); err != nil {
		return fmt.Errorf("mark node projection offset: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_node_projection_partitions
			(source_partition, last_seq, last_projected_at, policy_version, health_status, updated_at)
		VALUES (?, ?, ?, ?, 'healthy', ?)
		ON CONFLICT(source_partition) DO UPDATE SET
			last_seq = CASE WHEN excluded.last_seq > forest_node_projection_partitions.last_seq THEN excluded.last_seq ELSE forest_node_projection_partitions.last_seq END,
			last_projected_at = excluded.last_projected_at,
			policy_version = excluded.policy_version,
			health_status = excluded.health_status,
			last_error = '',
			updated_at = excluded.updated_at
	`, partition, seq, now, policy, now); err != nil {
		return fmt.Errorf("mark node projection partition offset: %w", err)
	}
	return nil
}

func upsertProjectionFailureArtifactTx(ctx context.Context, tx *sql.Tx, record forestLedgerProjectionRecord, artifactID string, cause error) error {
	now := time.Now().UTC().Unix()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_artifacts
			(artifact_id, generator_participant, artifact_kind, data_type, content_ref, status,
			 validation_status, last_sequence, first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES (?, ?, 'projection_failure', 'error', ?, 'generated', 'failed', ?, ?, ?, ?, ?)
		ON CONFLICT(artifact_id) DO UPDATE SET
			last_sequence = excluded.last_sequence,
			last_seen_at = excluded.last_seen_at,
			metadata = excluded.metadata
	`, artifactID, projectorNodeGraphName, "ledger:"+"forest_ledger/"+record.ID, record.Seq, now, now, record.PayloadHash, marshalJSON(map[string]any{
		"projection": projectorNodeGraphName,
		"source_key": record.SourceKey,
		"error":      cause.Error(),
		"event_kind": record.EventKind,
	}))
	if err != nil {
		return fmt.Errorf("upsert projection failure artifact: %w", err)
	}
	return nil
}

func resolveNodeProjectionFailureTx(ctx context.Context, tx *sql.Tx, record forestLedgerProjectionRecord, artifactID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE forest_projection_failures
		SET artifact_id = ?, resolved_at = ?
		WHERE projection_name = ? AND source_key = ?
	`, artifactID, time.Now().UTC().Unix(), projectorNodeGraphName, record.SourceKey)
	if err != nil {
		return fmt.Errorf("resolve node projection failure: %w", err)
	}
	return nil
}

func NodeGraphChecksum(ctx context.Context, db *sql.DB) (string, error) {
	if db == nil {
		return "", errors.New("db is required")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT 'n:' || node_id || ':' || node_kind || ':' || subject_type || ':' || subject_id
		FROM forest_nodes
		UNION ALL
		SELECT 'e:' || edge_id || ':' || source_node_id || ':' || target_node_id || ':' || edge_kind
		FROM forest_node_edges
		ORDER BY 1
	`)
	if err != nil {
		return "", fmt.Errorf("query node graph checksum rows: %w", err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var part string
		if err := rows.Scan(&part); err != nil {
			return "", fmt.Errorf("scan node graph checksum row: %w", err)
		}
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return stableID("node_graph_checksum", strings.Join(parts, "\n")), nil
}
