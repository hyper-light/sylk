package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	SubstrateChannelAttention     = "attention"
	SubstrateChannelConfidence    = "confidence"
	SubstrateChannelContradiction = "contradiction"
	SubstrateChannelNovelty       = "novelty"
	SubstrateChannelUtility       = "utility"
	SubstrateChannelValidation    = "validation_energy"
	SubstrateChannelSuppression   = "suppression"
	SubstrateChannelRecovery      = "recovery"

	ecologyPolicyVersion = "ecology_v1"
)

type substrateChannelSpec struct {
	Channel       string
	Source        string
	Sink          string
	LowerBound    float64
	UpperBound    float64
	Conservation  string
	PolicyVersion string
}

type EcologyPolicy struct {
	PolicyVersion string
	Channels      []substrateChannelSpec
}

func defaultEcologyPolicy() EcologyPolicy {
	return EcologyPolicy{
		PolicyVersion: ecologyPolicyVersion,
		Channels: []substrateChannelSpec{
			{Channel: SubstrateChannelAttention, Source: "retrieval exposure and graph centrality", Sink: "adaptive threshold normalization", LowerBound: 0, UpperBound: 1, Conservation: "normalized per affected component", PolicyVersion: ecologyPolicyVersion},
			{Channel: SubstrateChannelConfidence, Source: "claim, artifact, and validation confidence", Sink: "contradiction and failed validation", LowerBound: 0, UpperBound: 1, Conservation: "bounded max with validation support", PolicyVersion: ecologyPolicyVersion},
			{Channel: SubstrateChannelContradiction, Source: "contradiction edges and failed validation", Sink: "remediation and recovery", LowerBound: 0, UpperBound: 1, Conservation: "bounded pressure", PolicyVersion: ecologyPolicyVersion},
			{Channel: SubstrateChannelNovelty, Source: "new source facts and sparse neighborhoods", Sink: "repeated exposure", LowerBound: 0, UpperBound: 1, Conservation: "decays through activation history", PolicyVersion: ecologyPolicyVersion},
			{Channel: SubstrateChannelUtility, Source: "successful outcomes and durable artifact use", Sink: "weak exposure", LowerBound: 0, UpperBound: 1, Conservation: "bounded credit", PolicyVersion: ecologyPolicyVersion},
			{Channel: SubstrateChannelValidation, Source: "validation records and validation edges", Sink: "expiry and contradiction", LowerBound: 0, UpperBound: 1, Conservation: "idempotent by source evidence", PolicyVersion: ecologyPolicyVersion},
			{Channel: SubstrateChannelSuppression, Source: "contradiction and corruption pressure", Sink: "recovery evidence", LowerBound: 0, UpperBound: 1, Conservation: "routing-only; never deletes evidence", PolicyVersion: ecologyPolicyVersion},
			{Channel: SubstrateChannelRecovery, Source: "remediation and later validation", Sink: "resource accounting normalization", LowerBound: 0, UpperBound: 1, Conservation: "bounded recovery credit", PolicyVersion: ecologyPolicyVersion},
		},
	}
}

func seedForestSubstrateChannels(db *sql.DB) error {
	policy := defaultEcologyPolicy()
	for _, channel := range policy.Channels {
		if err := validateChannelSpec(channel); err != nil {
			return err
		}
		if _, err := db.Exec(`
			INSERT INTO forest_substrate_channels
				(channel, source_description, sink_description, lower_bound, upper_bound, conservation_rule, policy_version, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(channel) DO UPDATE SET
				source_description = excluded.source_description,
				sink_description = excluded.sink_description,
				lower_bound = excluded.lower_bound,
				upper_bound = excluded.upper_bound,
				conservation_rule = excluded.conservation_rule,
				policy_version = excluded.policy_version,
				updated_at = excluded.updated_at
		`, channel.Channel, channel.Source, channel.Sink, channel.LowerBound, channel.UpperBound,
			channel.Conservation, channel.PolicyVersion, time.Now().UTC().Unix()); err != nil {
			return fmt.Errorf("seed substrate channel %s: %w", channel.Channel, err)
		}
	}
	return nil
}

func validateChannelSpec(spec substrateChannelSpec) error {
	spec.Channel = strings.TrimSpace(spec.Channel)
	if spec.Channel == "" {
		return errors.New("substrate channel is required")
	}
	if math.IsNaN(spec.LowerBound) || math.IsNaN(spec.UpperBound) || math.IsInf(spec.LowerBound, 0) || math.IsInf(spec.UpperBound, 0) {
		return fmt.Errorf("substrate channel %s has invalid bounds", spec.Channel)
	}
	if spec.LowerBound < 0 || spec.UpperBound > 1 || spec.LowerBound >= spec.UpperBound {
		return fmt.Errorf("substrate channel %s bounds must be within [0,1] and lower < upper", spec.Channel)
	}
	if strings.TrimSpace(spec.Source) == "" || strings.TrimSpace(spec.Sink) == "" || strings.TrimSpace(spec.Conservation) == "" {
		return fmt.Errorf("substrate channel %s requires source, sink, and conservation descriptions", spec.Channel)
	}
	return nil
}

func (m *MemoryForest) refreshSubstrateChannelState(ctx context.Context, sessionID string, limit int) (SubstrateResult, error) {
	nodes, err := m.loadSubstrateNodes(ctx, sessionID, limit)
	if err != nil {
		return SubstrateResult{}, err
	}
	if len(nodes) == 0 {
		return SubstrateResult{}, nil
	}
	edges, err := m.loadSubstrateNodeEdges(ctx, sortedForestNodeIDs(nodes))
	if err != nil {
		return SubstrateResult{}, err
	}
	fields := computeSubstrateFields(nodes, edges)
	now := time.Now().UTC().Unix()
	m.substrateStateMu.Lock()
	defer m.substrateStateMu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SubstrateResult{}, fmt.Errorf("begin ecological substrate tx: %w", err)
	}
	defer tx.Rollback()
	for nodeID, channelValues := range fields {
		threshold := adaptiveThreshold(channelValues)
		for channel, value := range channelValues {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO forest_substrate_field
					(scope_type, scope_id, channel, value, adaptive_threshold, policy_version, source_seq, updated_at, metadata)
				VALUES ('node', ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(scope_type, scope_id, channel, policy_version) DO UPDATE SET
					value = excluded.value,
					adaptive_threshold = excluded.adaptive_threshold,
					source_seq = excluded.source_seq,
					updated_at = excluded.updated_at,
					metadata = excluded.metadata
			`, nodeID, channel, clampFinite01(value), threshold, ecologyPolicyVersion, maxNodeSeq(nodes, nodeID), now, marshalJSON(channelValues)); err != nil {
				return SubstrateResult{}, fmt.Errorf("upsert substrate field: %w", err)
			}
			if err := updateActivationHistoryTx(ctx, tx, "node", nodeID, channel, value, threshold, now); err != nil {
				return SubstrateResult{}, err
			}
		}
		if err := recordResourceBalanceTx(ctx, tx, "node", nodeID, "substrate_update", resourceDelta(channelValues), "substrate:"+nodeID+":"+fmt.Sprint(maxNodeSeq(nodes, nodeID)), now); err != nil {
			return SubstrateResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SubstrateResult{}, fmt.Errorf("commit ecological substrate tx: %w", err)
	}
	return SubstrateResult{StatesUpdated: len(fields), EdgesUpdated: len(edges), FrontiersUpdated: len(fields)}, nil
}

func (m *MemoryForest) loadSubstrateNodes(ctx context.Context, sessionID string, limit int) ([]ForestNode, error) {
	sessionID = normalizeForestSessionID(sessionID)
	rows, err := m.db.QueryContext(ctx, `
		SELECT node_id, node_kind, source_kind, source_partition, source_key, source_seq,
		       subject_type, subject_id, session_id, task_id, title, summary, evidence_grade,
		       confidence, salience, utility, novelty, status, policy_version,
		       first_seen_at, last_seen_at, payload_hash, metadata
		FROM forest_nodes
		WHERE session_id = ? OR ? = 'global'
		ORDER BY source_seq DESC, last_seen_at DESC
		LIMIT ?
	`, sessionID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query substrate nodes: %w", err)
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

func (m *MemoryForest) loadSubstrateNodeEdges(ctx context.Context, nodeIDs []string) ([]ForestEdge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	placeholders, args := placeholdersForStrings(nodeIDs)
	args = append(args, nodeIDsToAny(nodeIDs)...)
	rows, err := m.db.QueryContext(ctx, `
		SELECT edge_id, source_node_id, target_node_id, edge_kind, source_kind, source_partition,
		       source_key, source_seq, weight, evidence_grade, created_at, metadata
		FROM forest_node_edges
		WHERE source_node_id IN (`+placeholders+`) OR target_node_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query substrate node edges: %w", err)
	}
	defer rows.Close()
	var edges []ForestEdge
	for rows.Next() {
		var edge ForestEdge
		var kind, grade string
		var createdAt int64
		var metadata string
		if err := rows.Scan(&edge.ID, &edge.SourceNodeID, &edge.TargetNodeID, &kind, &edge.SourceKind, &edge.SourcePartition,
			&edge.SourceKey, &edge.SourceSeq, &edge.Weight, &grade, &createdAt, &metadata); err != nil {
			return nil, fmt.Errorf("scan substrate node edge: %w", err)
		}
		edge.Kind = ForestEdgeKind(kind)
		edge.EvidenceGrade = EvidenceGrade(grade)
		edge.CreatedAt = time.Unix(createdAt, 0).UTC()
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func nodeIDsToAny(ids []string) []any {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	return args
}

func computeSubstrateFields(nodes []ForestNode, edges []ForestEdge) map[string]map[string]float64 {
	fields := make(map[string]map[string]float64, len(nodes))
	for _, node := range nodes {
		fields[node.ID] = sourceInjection(node)
	}
	diffuseSubstrate(fields, edges)
	normalizeSubstrateFields(fields)
	return fields
}

func sourceInjection(node ForestNode) map[string]float64 {
	validation := 0.0
	if node.Kind == ForestNodeValidation || node.EvidenceGrade == EvidenceGradeValidated {
		validation = 1
	}
	contradiction := 0.0
	if node.Kind == ForestNodeContradiction || node.EvidenceGrade == EvidenceGradeContradicted || node.EvidenceGrade == EvidenceGradeFailed {
		contradiction = 1
	}
	recovery := 0.0
	if node.Kind == ForestNodeRemediation {
		recovery = clamp01(node.Utility + validation)
	}
	return map[string]float64{
		SubstrateChannelAttention:     clamp01(node.Salience),
		SubstrateChannelConfidence:    clamp01(node.Confidence + validation - contradiction),
		SubstrateChannelContradiction: contradiction,
		SubstrateChannelNovelty:       clamp01(node.Novelty),
		SubstrateChannelUtility:       clamp01(node.Utility),
		SubstrateChannelValidation:    validation,
		SubstrateChannelSuppression:   clamp01(contradiction - recovery),
		SubstrateChannelRecovery:      recovery,
	}
}

func diffuseSubstrate(fields map[string]map[string]float64, edges []ForestEdge) {
	if len(edges) == 0 {
		return
	}
	next := cloneSubstrateFields(fields)
	for _, edge := range edges {
		source, okSource := fields[edge.SourceNodeID]
		target, okTarget := fields[edge.TargetNodeID]
		if !okSource || !okTarget {
			continue
		}
		weight := clampFinite01(edge.Weight)
		for channel, sourceValue := range source {
			next[edge.TargetNodeID][channel] += sourceValue * weight
		}
		for channel, targetValue := range target {
			next[edge.SourceNodeID][channel] += targetValue * weight
		}
	}
	for nodeID, values := range next {
		for channel, value := range values {
			values[channel] = value / (1 + degreeForNode(edges, nodeID, channel))
		}
	}
	for nodeID, values := range next {
		fields[nodeID] = values
	}
}

func cloneSubstrateFields(fields map[string]map[string]float64) map[string]map[string]float64 {
	cloned := make(map[string]map[string]float64, len(fields))
	for nodeID, values := range fields {
		cloned[nodeID] = make(map[string]float64, len(values))
		for channel, value := range values {
			cloned[nodeID][channel] = value
		}
	}
	return cloned
}

func degreeForNode(edges []ForestEdge, nodeID, channel string) float64 {
	degree := 0.0
	for _, edge := range edges {
		if edge.SourceNodeID == nodeID || edge.TargetNodeID == nodeID {
			degree += clampFinite01(edge.Weight)
		}
	}
	if degree == 0 {
		return 0
	}
	return degree
}

func normalizeSubstrateFields(fields map[string]map[string]float64) {
	for _, values := range fields {
		contradiction := clampFinite01(values[SubstrateChannelContradiction])
		recovery := clampFinite01(values[SubstrateChannelRecovery])
		values[SubstrateChannelSuppression] = clamp01(contradiction - recovery)
		for channel, value := range values {
			values[channel] = clampFinite01(value)
		}
	}
}

func adaptiveThreshold(values map[string]float64) float64 {
	if len(values) == 0 {
		return 0
	}
	samples := make([]float64, 0, len(values))
	for _, value := range values {
		samples = append(samples, clampFinite01(value))
	}
	sort.Float64s(samples)
	return samples[len(samples)/2]
}

func maxNodeSeq(nodes []ForestNode, nodeID string) int64 {
	var maxSeq int64
	for _, node := range nodes {
		if node.ID == nodeID && node.SourceSeq > maxSeq {
			maxSeq = node.SourceSeq
		}
	}
	return maxSeq
}

func updateActivationHistoryTx(ctx context.Context, tx *sql.Tx, scopeType, scopeID, channel string, value, threshold float64, now int64) error {
	bucket := activationBucket(now)
	weak := 0
	success := 0
	if value <= threshold {
		weak = 1
	}
	if value > threshold {
		success = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_activation_history
			(scope_type, scope_id, channel, bucket, exposure_count, success_count,
			 weak_exposure_count, threshold, policy_version, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)
		ON CONFLICT(scope_type, scope_id, channel, bucket, policy_version) DO UPDATE SET
			exposure_count = forest_activation_history.exposure_count + 1,
			success_count = forest_activation_history.success_count + excluded.success_count,
			weak_exposure_count = forest_activation_history.weak_exposure_count + excluded.weak_exposure_count,
			threshold = excluded.threshold,
			updated_at = excluded.updated_at
	`, scopeType, scopeID, channel, bucket, success, weak, threshold, ecologyPolicyVersion, now)
	if err != nil {
		return fmt.Errorf("update activation history: %w", err)
	}
	return nil
}

func activationBucket(unix int64) int64 {
	policy := defaultEcologyPolicy()
	if len(policy.Channels) == 0 {
		return unix
	}
	return unix / int64(len(policy.Channels))
}

func resourceDelta(values map[string]float64) float64 {
	credit := values[SubstrateChannelValidation] + values[SubstrateChannelRecovery] + values[SubstrateChannelUtility]
	charge := values[SubstrateChannelAttention] + values[SubstrateChannelContradiction] + values[SubstrateChannelSuppression]
	return clampFiniteSigned(credit - charge)
}

func clampFiniteSigned(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

func recordResourceBalanceTx(ctx context.Context, tx *sql.Tx, scopeType, scopeID, eventKind string, amount float64, sourceKey string, now int64) error {
	var prior sql.NullFloat64
	if err := tx.QueryRowContext(ctx, `
		SELECT balance_after
		FROM forest_resource_accounting
		WHERE scope_type = ? AND scope_id = ? AND policy_version = ?
		ORDER BY recorded_at DESC
		LIMIT 1
	`, scopeType, scopeID, ecologyPolicyVersion).Scan(&prior); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("load resource balance: %w", err)
	}
	balance := amount
	if prior.Valid {
		balance = clampFiniteSigned(prior.Float64 + amount)
	}
	accountingID := stableID("resource", scopeType, scopeID, eventKind, sourceKey, ecologyPolicyVersion)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_resource_accounting
			(accounting_id, scope_type, scope_id, event_kind, amount, balance_after,
			 source_key, policy_version, recorded_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope_type, scope_id, event_kind, source_key, policy_version) DO NOTHING
	`, accountingID, scopeType, scopeID, eventKind, amount, balance, sourceKey, ecologyPolicyVersion, now, "{}"); err != nil {
		return fmt.Errorf("record resource accounting: %w", err)
	}
	return nil
}
