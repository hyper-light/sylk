package forest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ForestCursorInput struct {
	SessionID      string          `json:"session_id,omitempty"`
	TaskID         string          `json:"task_id,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	Packets        []*ForestPacket `json:"packets,omitempty"`
	Limit          int             `json:"limit,omitempty"`
	NoCursorReason string          `json:"no_cursor_reason,omitempty"`
}

func (m *MemoryForest) CreateForestCursor(ctx context.Context, input ForestCursorInput) (*ForestCursor, error) {
	cursor, err := buildForestCursor(input)
	if err != nil {
		return nil, err
	}
	if err := m.validateForestCursor(ctx, cursor); err != nil {
		return nil, err
	}
	if err := m.insertForestCursor(ctx, cursor); err != nil {
		return nil, err
	}
	if err := m.emitForestCursorLedger(ctx, cursor); err != nil {
		return nil, err
	}
	return cursor, nil
}

func (m *MemoryForest) LoadForestCursor(ctx context.Context, cursorID string) (*ForestCursor, error) {
	cursorID = strings.TrimSpace(cursorID)
	if cursorID == "" {
		return nil, fmt.Errorf("cursor id is required")
	}
	row := m.db.QueryRowContext(ctx, `
		SELECT cursor_id, session_id, task_id, agent_id, turn_id, active_cluster_ids,
		       active_node_ids, poi_refs, bridge_refs, risk_flags, no_cursor_reason,
		       policy_version, created_at, metadata
		FROM forest_cursors
		WHERE cursor_id = ?
	`, cursorID)
	cursor, err := scanForestCursor(row)
	if err != nil {
		return nil, err
	}
	return cursor, nil
}

func buildForestCursor(input ForestCursorInput) (*ForestCursor, error) {
	input.SessionID = normalizeForestSessionID(input.SessionID)
	input.TaskID = normalizeForestTaskID(input.TaskID)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.TurnID = strings.TrimSpace(input.TurnID)
	budget := cursorBudget(input.Limit)
	packets := compactCursorPackets(input.Packets, budget)
	cursor := &ForestCursor{
		SessionID:        input.SessionID,
		TaskID:           input.TaskID,
		AgentID:          input.AgentID,
		TurnID:           input.TurnID,
		ActiveClusterIDs: cursorClusterIDs(packets, budget),
		ActiveNodeIDs:    cursorNodeIDs(packets, budget),
		POIRefs:          cursorPOIRefs(packets, budget),
		BridgeRefs:       cursorBridgeRefs(packets, budget),
		RiskFlags:        cursorRiskFlags(packets, strings.TrimSpace(input.NoCursorReason), budget),
		NoCursorReason:   strings.TrimSpace(input.NoCursorReason),
		PolicyVersion:    forestProjectionVersionPhase789,
		CreatedAt:        time.Now().UTC(),
		Metadata: map[string]any{
			"packet_count":     len(input.Packets),
			"budget":           budget,
			"bridge_crossings": cursorBridgeCrossings(packets, budget),
		},
	}
	if len(packets) == 0 && cursor.NoCursorReason == "" {
		cursor.NoCursorReason = "no_packets"
	}
	cursor.ID = stableID("forest_cursor", cursor.SessionID, cursor.TaskID, cursor.AgentID, cursor.TurnID,
		encodeStringList(cursor.ActiveNodeIDs), encodeStringList(cursor.ActiveClusterIDs), encodeStringList(cursor.RiskFlags))
	return cursor, nil
}

func cursorBudget(limit int) int {
	if limit > 0 {
		return limit
	}
	return len(defaultEcologyPolicy().Channels) * nodeProjectionRetryLimit()
}

func compactCursorPackets(packets []*ForestPacket, budget int) []*ForestPacket {
	filtered := make([]*ForestPacket, 0, len(packets))
	for _, packet := range packets {
		if packet != nil && packet.Node.ID != "" {
			filtered = append(filtered, packet)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Score == filtered[j].Score {
			return filtered[i].Node.ID < filtered[j].Node.ID
		}
		return filtered[i].Score > filtered[j].Score
	})
	if len(filtered) > budget {
		filtered = filtered[:budget]
	}
	return filtered
}

func cursorNodeIDs(packets []*ForestPacket, budget int) []string {
	ids := make([]string, 0, len(packets))
	for _, packet := range packets {
		ids = append(ids, packet.Node.ID)
	}
	return compactStrings(ids, budget)
}

func cursorClusterIDs(packets []*ForestPacket, budget int) []string {
	var ids []string
	for _, packet := range packets {
		ids = append(ids, packet.ClusterIDs...)
	}
	return compactStrings(ids, budget)
}

func cursorPOIRefs(packets []*ForestPacket, budget int) []string {
	var ids []string
	for _, packet := range packets {
		for _, poi := range packet.POIs {
			ids = append(ids, poi.POIID)
		}
	}
	return compactStrings(ids, budget)
}

func cursorBridgeRefs(packets []*ForestPacket, budget int) []string {
	var ids []string
	for _, packet := range packets {
		for _, risk := range packet.BridgeRisks {
			ids = append(ids, risk.BridgeID)
		}
	}
	return compactStrings(ids, budget)
}

func cursorBridgeCrossings(packets []*ForestPacket, budget int) []map[string]string {
	type crossing struct {
		BridgeID        string
		NodeID          string
		SourceClusterID string
		TargetClusterID string
	}
	seen := make(map[string]struct{})
	crossings := make([]crossing, 0)
	for _, packet := range packets {
		for _, risk := range packet.BridgeRisks {
			if risk.BridgeID == "" || risk.SourceClusterID == "" || risk.TargetClusterID == "" {
				continue
			}
			item := crossing{
				BridgeID:        risk.BridgeID,
				NodeID:          firstNonEmptyString(risk.NodeID, packet.Node.ID),
				SourceClusterID: risk.SourceClusterID,
				TargetClusterID: risk.TargetClusterID,
			}
			key := strings.Join([]string{item.BridgeID, item.NodeID, item.SourceClusterID, item.TargetClusterID}, "\x00")
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			crossings = append(crossings, item)
		}
	}
	sort.SliceStable(crossings, func(i, j int) bool {
		left := strings.Join([]string{crossings[i].BridgeID, crossings[i].SourceClusterID, crossings[i].TargetClusterID, crossings[i].NodeID}, "\x00")
		right := strings.Join([]string{crossings[j].BridgeID, crossings[j].SourceClusterID, crossings[j].TargetClusterID, crossings[j].NodeID}, "\x00")
		return left < right
	})
	if len(crossings) > budget {
		crossings = crossings[:budget]
	}
	out := make([]map[string]string, 0, len(crossings))
	for _, item := range crossings {
		out = append(out, map[string]string{
			"bridge_id":         item.BridgeID,
			"node_id":           item.NodeID,
			"source_cluster_id": item.SourceClusterID,
			"target_cluster_id": item.TargetClusterID,
		})
	}
	return out
}

func cursorRiskFlags(packets []*ForestPacket, reason string, budget int) []string {
	var flags []string
	if reason != "" {
		flags = append(flags, "no_cursor:"+reason)
	}
	for _, packet := range packets {
		if packet.Quarantine.Quarantined {
			flags = append(flags, "suppression.active")
		}
		if packet.RiskScore > packet.ValidationNeed {
			flags = append(flags, "context.under_review")
		}
		for _, risk := range packet.BridgeRisks {
			if risk.BridgeID != "" {
				flags = append(flags, "bridge.crossed")
			}
		}
	}
	return compactStrings(flags, budget)
}

func compactStrings(values []string, budget int) []string {
	values = dedupeStrings(values)
	sort.Strings(values)
	if len(values) > budget {
		return append([]string(nil), values[:budget]...)
	}
	return values
}

func (m *MemoryForest) validateForestCursor(ctx context.Context, cursor *ForestCursor) error {
	if cursor == nil {
		return fmt.Errorf("cursor is required")
	}
	for _, nodeID := range cursor.ActiveNodeIDs {
		exists, err := m.forestNodeExists(ctx, nodeID)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("cursor references nonexistent node %s", nodeID)
		}
	}
	return nil
}

func (m *MemoryForest) forestNodeExists(ctx context.Context, nodeID string) (bool, error) {
	var count int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM forest_nodes WHERE node_id = ?`, nodeID).Scan(&count); err != nil {
		return false, fmt.Errorf("count cursor node: %w", err)
	}
	return count > 0, nil
}

func (m *MemoryForest) insertForestCursor(ctx context.Context, cursor *ForestCursor) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO forest_cursors
			(cursor_id, session_id, task_id, agent_id, turn_id, active_cluster_ids,
			 active_node_ids, poi_refs, bridge_refs, risk_flags, no_cursor_reason,
			 policy_version, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cursor_id) DO NOTHING
	`, cursor.ID, cursor.SessionID, cursor.TaskID, cursor.AgentID, cursor.TurnID,
		encodeStringList(cursor.ActiveClusterIDs), encodeStringList(cursor.ActiveNodeIDs),
		encodeStringList(cursor.POIRefs), encodeStringList(cursor.BridgeRefs),
		encodeStringList(cursor.RiskFlags), cursor.NoCursorReason, cursor.PolicyVersion,
		cursor.CreatedAt.Unix(), marshalJSON(cursor.Metadata))
	if err != nil {
		return fmt.Errorf("insert forest cursor: %w", err)
	}
	return nil
}

func scanForestCursor(row interface{ Scan(dest ...any) error }) (*ForestCursor, error) {
	var clusters, nodes, pois, bridges, risks, metadata string
	var createdAt int64
	cursor := &ForestCursor{}
	if err := row.Scan(&cursor.ID, &cursor.SessionID, &cursor.TaskID, &cursor.AgentID, &cursor.TurnID,
		&clusters, &nodes, &pois, &bridges, &risks, &cursor.NoCursorReason,
		&cursor.PolicyVersion, &createdAt, &metadata); err != nil {
		return nil, fmt.Errorf("scan forest cursor: %w", err)
	}
	cursor.ActiveClusterIDs = decodeStringList(clusters)
	cursor.ActiveNodeIDs = decodeStringList(nodes)
	cursor.POIRefs = decodeStringList(pois)
	cursor.BridgeRefs = decodeStringList(bridges)
	cursor.RiskFlags = decodeStringList(risks)
	cursor.CreatedAt = time.Unix(createdAt, 0).UTC()
	cursor.Metadata = map[string]any{}
	if metadata != "" {
		_ = unmarshalJSON(metadata, &cursor.Metadata)
	}
	return cursor, nil
}

func (m *MemoryForest) emitForestCursorLedger(ctx context.Context, cursor *ForestCursor) error {
	for _, eventKind := range cursorLedgerKinds(cursor) {
		if _, err := m.AppendLedgerRecord(ctx, LedgerRecord{
			SourceKind:  LedgerSourceMaintenance,
			SourceID:    cursor.ID,
			SourceKey:   "cursor:" + cursor.ID + ":" + eventKind,
			EventKind:   eventKind,
			SessionID:   firstNonEmptyString(cursor.SessionID, "global"),
			TaskID:      cursor.TaskID,
			SubjectType: "forest_cursor",
			SubjectID:   cursor.ID,
			OccurredAt:  cursor.CreatedAt,
			Payload:     cursor,
		}); err != nil {
			return err
		}
	}
	return nil
}

func cursorLedgerKinds(cursor *ForestCursor) []string {
	var kinds []string
	for _, flag := range cursor.RiskFlags {
		switch flag {
		case "bridge.crossed", "context.under_review", "suppression.active":
			kinds = append(kinds, flag)
		}
	}
	return compactStrings(kinds, cursorBudget(0))
}
