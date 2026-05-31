package forest

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const defaultEvidenceQueryLimit = projectorBatchSize

type ArtifactEvidenceFilter struct {
	ClaimID    string
	ArtifactID string
	Status     string
	Kind       string
	Limit      int
}

type ArtifactEvidenceRecord struct {
	ArtifactID           string
	ClaimID              string
	TestamentID          string
	ValidationID         string
	GeneratorParticipant string
	ArtifactName         string
	ArtifactKind         string
	DataType             string
	ContentHash          string
	ContentRef           string
	Status               string
	ValidationStatus     string
	LastSequence         int64
	FirstSeenAt          time.Time
	LastSeenAt           time.Time
	Edges                []ArtifactEvidenceEdge
}

type ArtifactEvidenceEdge struct {
	ArtifactID string
	Kind       string
	TargetType string
	TargetID   string
	Sequence   int64
	CreatedAt  time.Time
}

type ValidationEvidenceFilter struct {
	ClaimID      string
	ValidationID string
	ArtifactID   string
	Status       string
	Limit        int
}

type ValidationEvidenceRecord struct {
	ValidationID         string
	ClaimID              string
	TargetArtifactID     string
	EvaluatorParticipant string
	ValidationType       string
	Status               string
	ResultArtifactID     string
	FailureReason        string
	Required             bool
	LastSequence         int64
	FirstSeenAt          time.Time
	LastSeenAt           time.Time
}

func (m *MemoryForest) QueryArtifactEvidence(ctx context.Context, filter ArtifactEvidenceFilter) ([]ArtifactEvidenceRecord, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("memory forest is required")
	}
	where, args := artifactEvidenceWhere(filter)
	rows, err := m.db.QueryContext(ctx, `
		SELECT artifact_id, claim_id, testament_id, validation_id,
		       generator_participant, artifact_name, artifact_kind, data_type,
		       content_hash, content_ref, status, validation_status,
		       last_sequence, first_seen_at, last_seen_at
		FROM forest_artifacts
		`+where+`
		ORDER BY last_sequence DESC, artifact_id ASC
		LIMIT ?
	`, append(args, evidenceQueryLimit(filter.Limit))...)
	if err != nil {
		return nil, fmt.Errorf("query artifact evidence: %w", err)
	}
	defer rows.Close()

	records := make([]ArtifactEvidenceRecord, 0)
	for rows.Next() {
		var record ArtifactEvidenceRecord
		var firstSeen, lastSeen int64
		if err := rows.Scan(
			&record.ArtifactID,
			&record.ClaimID,
			&record.TestamentID,
			&record.ValidationID,
			&record.GeneratorParticipant,
			&record.ArtifactName,
			&record.ArtifactKind,
			&record.DataType,
			&record.ContentHash,
			&record.ContentRef,
			&record.Status,
			&record.ValidationStatus,
			&record.LastSequence,
			&firstSeen,
			&lastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan artifact evidence: %w", err)
		}
		record.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
		record.LastSeenAt = time.Unix(lastSeen, 0).UTC()
		edges, err := m.queryArtifactEvidenceEdges(ctx, record.ArtifactID)
		if err != nil {
			return nil, err
		}
		record.Edges = edges
		records = append(records, record)
	}
	return records, rows.Err()
}

func artifactEvidenceWhere(filter ArtifactEvidenceFilter) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 4)
	add := func(column, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		clauses = append(clauses, column+" = ?")
		args = append(args, value)
	}
	add("claim_id", filter.ClaimID)
	add("artifact_id", filter.ArtifactID)
	add("status", filter.Status)
	add("artifact_kind", filter.Kind)
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func (m *MemoryForest) queryArtifactEvidenceEdges(ctx context.Context, artifactID string) ([]ArtifactEvidenceEdge, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT artifact_id, edge_kind, target_type, target_id, sequence, created_at
		FROM forest_artifact_edges
		WHERE artifact_id = ?
		ORDER BY sequence DESC, edge_kind ASC, target_type ASC, target_id ASC
	`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query artifact evidence edges: %w", err)
	}
	defer rows.Close()
	edges := make([]ArtifactEvidenceEdge, 0)
	for rows.Next() {
		var edge ArtifactEvidenceEdge
		var createdAt int64
		if err := rows.Scan(&edge.ArtifactID, &edge.Kind, &edge.TargetType, &edge.TargetID, &edge.Sequence, &createdAt); err != nil {
			return nil, fmt.Errorf("scan artifact evidence edge: %w", err)
		}
		edge.CreatedAt = time.Unix(createdAt, 0).UTC()
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func (m *MemoryForest) QueryValidationEvidence(ctx context.Context, filter ValidationEvidenceFilter) ([]ValidationEvidenceRecord, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("memory forest is required")
	}
	where, args := validationEvidenceWhere(filter)
	rows, err := m.db.QueryContext(ctx, `
		SELECT validation_id, claim_id, target_artifact_id,
		       evaluator_participant, validation_type, status,
		       result_artifact_id, failure_reason, required,
		       last_sequence, first_seen_at, last_seen_at
		FROM forest_validations
		`+where+`
		ORDER BY last_sequence DESC, validation_id ASC
		LIMIT ?
	`, append(args, evidenceQueryLimit(filter.Limit))...)
	if err != nil {
		return nil, fmt.Errorf("query validation evidence: %w", err)
	}
	defer rows.Close()

	records := make([]ValidationEvidenceRecord, 0)
	for rows.Next() {
		var record ValidationEvidenceRecord
		var required int
		var firstSeen, lastSeen int64
		if err := rows.Scan(
			&record.ValidationID,
			&record.ClaimID,
			&record.TargetArtifactID,
			&record.EvaluatorParticipant,
			&record.ValidationType,
			&record.Status,
			&record.ResultArtifactID,
			&record.FailureReason,
			&required,
			&record.LastSequence,
			&firstSeen,
			&lastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan validation evidence: %w", err)
		}
		record.Required = required != 0
		record.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
		record.LastSeenAt = time.Unix(lastSeen, 0).UTC()
		records = append(records, record)
	}
	return records, rows.Err()
}

func validationEvidenceWhere(filter ValidationEvidenceFilter) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 4)
	add := func(column, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		clauses = append(clauses, column+" = ?")
		args = append(args, value)
	}
	add("claim_id", filter.ClaimID)
	add("validation_id", filter.ValidationID)
	add("target_artifact_id", filter.ArtifactID)
	add("status", filter.Status)
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func evidenceQueryLimit(limit int) int {
	if limit <= 0 {
		return defaultEvidenceQueryLimit
	}
	return limit
}
