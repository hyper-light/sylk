package forest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (m *MemoryForest) hasRetrievalNodes(ctx context.Context, sessionID string) (bool, error) {
	var branchRows int
	if err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_branches
		WHERE session_id = ? OR ? = ''
	`, normalizeForestSessionID(sessionID), strings.TrimSpace(sessionID)).Scan(&branchRows); err != nil {
		return false, fmt.Errorf("count retrieval branches: %w", err)
	}
	if branchRows > 0 {
		return false, nil
	}
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

func (m *MemoryForest) retrieveNodePackets(ctx context.Context, query Query) ([]*BranchPacket, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = m.substrateLimit
	}
	m.substrateStateMu.RLock()
	rows, err := m.db.QueryContext(ctx, `
		SELECT n.node_id, n.node_kind, n.source_partition, n.subject_id, n.session_id, n.task_id,
		       n.title, n.summary, n.confidence, n.salience, n.utility, n.evidence_grade,
		       n.first_seen_at, n.last_seen_at, n.source_seq,
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
		return nil, fmt.Errorf("query node retrieval packets: %w", err)
	}
	packets := make([]*BranchPacket, 0, limit)
	for rows.Next() {
		packet, err := scanNodePacket(rows, query)
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
		return nil, fmt.Errorf("close node retrieval packets: %w", err)
	}
	m.substrateStateMu.RUnlock()
	sort.SliceStable(packets, func(i, j int) bool {
		if packets[i].Score.Total == packets[j].Score.Total {
			return packets[i].Branch.ID < packets[j].Branch.ID
		}
		return packets[i].Score.Total > packets[j].Score.Total
	})
	if len(packets) > limit {
		packets = packets[:limit]
	}
	if err := m.recordNodeRetrievalAccounting(ctx, packets); err != nil {
		return nil, err
	}
	return packets, nil
}

func scanNodePacket(rows interface {
	Scan(dest ...any) error
}, query Query) (*BranchPacket, error) {
	var (
		id, kind, partition, subjectID, sessionID, taskID string
		title, summary, grade                             string
		confidence, salience, utility                     float64
		firstSeen, lastSeen, seq                          int64
		fieldConfidence, validation, suppression          float64
	)
	if err := rows.Scan(&id, &kind, &partition, &subjectID, &sessionID, &taskID, &title, &summary,
		&confidence, &salience, &utility, &grade, &firstSeen, &lastSeen, &seq,
		&fieldConfidence, &validation, &suppression); err != nil {
		return nil, fmt.Errorf("scan node retrieval packet: %w", err)
	}
	family := treeFamilyForNodeKind(ForestNodeKind(kind))
	queryMatch := nodeQueryMatch(query.Query, title, summary)
	substrate := clampFinite01(fieldConfidence + validation - suppression)
	total := clamp01((queryMatch + confidence + salience + utility + substrate) / float64(len(defaultEcologyPolicy().Channels)-3))
	branch := &Branch{
		ID:             id,
		RootID:         stableID("node_root", partition, string(family)),
		Family:         family,
		Scope:          ScopeEpisodic,
		State:          branchStateForEvidence(EvidenceGrade(grade)),
		SessionID:      sessionID,
		TaskID:         taskID,
		IntentID:       subjectID,
		Title:          title,
		Summary:        summary,
		Confidence:     confidence,
		Salience:       salience,
		Utility:        utility,
		CreatedAt:      time.Unix(firstSeen, 0).UTC(),
		UpdatedAt:      time.Unix(lastSeen, 0).UTC(),
		LastAppliedSeq: seq,
	}
	return &BranchPacket{
		Branch: branch,
		Support: []PacketEvidence{{
			ContentID:  id,
			Summary:    summary,
			Confidence: confidence,
			Salience:   salience,
			Timestamp:  branch.UpdatedAt,
		}},
		Score: PacketScore{
			Total:            total,
			Base:             total,
			QueryMatch:       queryMatch,
			Evidence:         validation,
			Substrate:        substrate,
			Frontier:         substrate,
			Confidence:       confidence,
			Recency:          nodeRecencyScore(branch.UpdatedAt),
			Utility:          utility,
			Salience:         salience,
			Conflict:         suppression,
			ScopeSafety:      1,
			InhibitionSafety: 1 - suppression,
			RiskPenalty:      suppression,
		},
		Source: RetrievalSourcePrimary,
	}, nil
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

func (m *MemoryForest) recordNodeRetrievalAccounting(ctx context.Context, packets []*BranchPacket) error {
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
	now := time.Now().UTC().Unix()
	for _, packet := range packets {
		if packet == nil || packet.Branch == nil {
			continue
		}
		sourceKey := "retrieval:" + packet.Branch.ID + ":" + fmt.Sprint(now)
		if err := recordResourceBalanceTx(ctx, tx, "node", packet.Branch.ID, "retrieval_exposure", -packet.Score.Total, sourceKey, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit node retrieval accounting tx: %w", err)
	}
	return nil
}
