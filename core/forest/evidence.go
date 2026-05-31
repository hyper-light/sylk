package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	artifactEdgeClaim       = "attached_to_claim"
	artifactEdgeTestament   = "attached_to_testament"
	artifactEdgeValidation  = "validation_result_for"
	artifactEdgeValidates   = "validates"
	artifactEdgeInvalidates = "invalidates"
)

func projectCanonicalDeltaEvidenceTx(ctx context.Context, tx *sql.Tx, delta claims.CanonicalDelta, ledgerID string, seq int64, payloadHash string) error {
	if _, ok := claims.DeltaActionArtifactLifecycleStatus(delta.Action); ok {
		return projectArtifactDeltaTx(ctx, tx, delta, ledgerID, seq, payloadHash)
	}
	if _, ok := claims.DeltaActionValidationLifecycleStatus(delta.Action); ok {
		return projectValidationDeltaTx(ctx, tx, delta, ledgerID, seq, payloadHash)
	}
	if delta.Action == claims.DeltaActionValidationEvaluated {
		return projectValidationDeltaTx(ctx, tx, delta, ledgerID, seq, payloadHash)
	}
	return nil
}

func projectArtifactDeltaTx(ctx context.Context, tx *sql.Tx, delta claims.CanonicalDelta, ledgerID string, seq int64, payloadHash string) error {
	artifactID := delta.RefID("artifact", claims.RelatedTypeArtifact)
	if artifactID == "" {
		return recordEvidenceErrorTx(ctx, tx, ledgerID, claims.RelatedTypeArtifact, "", "missing_artifact_ref", "artifact lifecycle delta has no artifact ref", seq)
	}
	current, err := currentEvidenceSequenceTx(ctx, tx, "forest_artifacts", "artifact_id", artifactID)
	if err != nil {
		return err
	}
	if current > seq {
		return recordEvidenceErrorTx(ctx, tx, ledgerID, claims.RelatedTypeArtifact, artifactID, "sequence_regression", "artifact lifecycle sequence regressed", seq)
	}
	artifact := mapFromContext(delta.Context, "artifact")
	claim := mapFromContext(delta.Context, "claim")
	testament := mapFromContext(delta.Context, "testament")
	status, _ := claims.DeltaActionArtifactLifecycleStatus(delta.Action)
	statusText := firstNonEmptyString(string(status), mapString(artifact, "status"))
	claimID := firstNonEmptyString(delta.RefID("claim", claims.RelatedTypeClaim), mapString(artifact, "claim_id"), mapString(claim, "id"))
	testamentID := firstNonEmptyString(delta.RefID("testament", claims.RelatedTypeTestament), mapString(artifact, "testament_id"), mapString(testament, "id"))
	validationID := delta.RefID("validation", claims.RelatedTypeValidation)
	contentHash := mapString(artifact, "content_hash")
	contentRef := firstNonEmptyString(mapString(artifact, "reference"), mapString(artifact, "content_ref"))
	if contentHash == "" && contentRef == "" {
		contentRef = "unavailable:canonical_delta_context"
	}
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_artifacts
			(artifact_id, claim_id, testament_id, validation_id, generator_participant,
			 artifact_name, artifact_kind, data_type, content_hash, content_ref, status,
			 validation_status, last_sequence, first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?)
		ON CONFLICT(artifact_id) DO UPDATE SET
			claim_id = excluded.claim_id,
			testament_id = excluded.testament_id,
			validation_id = CASE WHEN excluded.validation_id != '' THEN excluded.validation_id ELSE forest_artifacts.validation_id END,
			generator_participant = excluded.generator_participant,
			artifact_name = excluded.artifact_name,
			artifact_kind = excluded.artifact_kind,
			data_type = excluded.data_type,
			content_hash = excluded.content_hash,
			content_ref = excluded.content_ref,
			status = excluded.status,
			last_sequence = excluded.last_sequence,
			last_seen_at = excluded.last_seen_at,
			payload_hash = excluded.payload_hash,
			metadata = excluded.metadata
	`, artifactID, claimID, testamentID, validationID, actorRoute(delta.Actor),
		mapString(artifact, "name"), mapString(artifact, "kind"), mapString(artifact, "data_type"),
		contentHash, contentRef, statusText, seq, now, now, payloadHash, marshalJSON(artifact)); err != nil {
		return fmt.Errorf("upsert forest artifact evidence: %w", err)
	}
	if claimID != "" {
		if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeClaim, claims.RelatedTypeClaim, claimID, ledgerID, seq); err != nil {
			return err
		}
	}
	if testamentID != "" {
		if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeTestament, claims.RelatedTypeTestament, testamentID, ledgerID, seq); err != nil {
			return err
		}
	}
	if validationID != "" {
		if err := insertArtifactEdgeTx(ctx, tx, artifactID, artifactEdgeValidation, claims.RelatedTypeValidation, validationID, ledgerID, seq); err != nil {
			return err
		}
	}
	return nil
}

func projectValidationDeltaTx(ctx context.Context, tx *sql.Tx, delta claims.CanonicalDelta, ledgerID string, seq int64, payloadHash string) error {
	validationID := delta.ValidationID()
	if validationID == "" {
		return recordEvidenceErrorTx(ctx, tx, ledgerID, claims.RelatedTypeValidation, "", "missing_validation_ref", "validation delta has no validation ref", seq)
	}
	current, err := currentEvidenceSequenceTx(ctx, tx, "forest_validations", "validation_id", validationID)
	if err != nil {
		return err
	}
	if current > seq {
		return recordEvidenceErrorTx(ctx, tx, ledgerID, claims.RelatedTypeValidation, validationID, "sequence_regression", "validation lifecycle sequence regressed", seq)
	}
	validation := mapFromContext(delta.Context, "validation")
	claim := mapFromContext(delta.Context, "claim")
	transition := mapFromContext(delta.Context, "transition")
	status := validationStatusText(delta, validation)
	claimID := firstNonEmptyString(delta.ClaimID(), mapString(validation, "claim_id"), mapString(claim, "id"))
	targetArtifactID := firstNonEmptyString(delta.RefID("artifact", claims.RelatedTypeArtifact), mapString(validation, "target_artifact_id"))
	resultArtifactID := mapString(validation, "result_artifact_id")
	required := mapBool(validation, "required")
	validationType := firstNonEmptyString(mapString(validation, "type"), mapString(validation, "validation_type"))
	failureReason := firstNonEmptyString(mapString(transition, "reason"), mapNestedString(delta.Context, "error", "description"), mapString(validation, "reason"))
	if validationTerminal(status) && targetArtifactID == "" && !receiptValidation(validationType) && delta.Action != claims.DeltaActionValidationEvaluated {
		if err := recordEvidenceErrorTx(ctx, tx, ledgerID, claims.RelatedTypeValidation, validationID, "missing_target_artifact", "terminal validation lifecycle delta has no target artifact", seq); err != nil {
			return err
		}
	}
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_validations
			(validation_id, claim_id, target_artifact_id, evaluator_participant, validation_type,
			 status, result_artifact_id, failure_reason, required, last_sequence,
			 first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(validation_id) DO UPDATE SET
			claim_id = excluded.claim_id,
			target_artifact_id = excluded.target_artifact_id,
			evaluator_participant = excluded.evaluator_participant,
			validation_type = excluded.validation_type,
			status = excluded.status,
			result_artifact_id = excluded.result_artifact_id,
			failure_reason = excluded.failure_reason,
			required = excluded.required,
			last_sequence = excluded.last_sequence,
			last_seen_at = excluded.last_seen_at,
			payload_hash = excluded.payload_hash,
			metadata = excluded.metadata
	`, validationID, claimID, targetArtifactID, actorRoute(delta.Actor), validationType, status,
		resultArtifactID, failureReason, boolInt(required), seq, now, now, payloadHash, marshalJSON(validation)); err != nil {
		return fmt.Errorf("upsert forest validation evidence: %w", err)
	}
	if targetArtifactID != "" {
		edgeKind := artifactEdgeValidates
		if validationFailureStatus(status) {
			edgeKind = artifactEdgeInvalidates
		}
		if err := insertArtifactEdgeTx(ctx, tx, targetArtifactID, edgeKind, claims.RelatedTypeValidation, validationID, ledgerID, seq); err != nil {
			return err
		}
	}
	if err := updateValidationPatternTx(ctx, tx, delta, validationType, targetArtifactID, status, validationID); err != nil {
		return err
	}
	return nil
}

func currentEvidenceSequenceTx(ctx context.Context, tx *sql.Tx, table, idColumn, id string) (int64, error) {
	query := fmt.Sprintf("SELECT last_sequence FROM %s WHERE %s = ?", table, idColumn)
	var current int64
	err := tx.QueryRowContext(ctx, query, id).Scan(&current)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load evidence sequence: %w", err)
	}
	return current, nil
}

func insertArtifactEdgeTx(ctx context.Context, tx *sql.Tx, artifactID, edgeKind, targetType, targetID, ledgerID string, seq int64) error {
	if artifactID == "" || edgeKind == "" || targetType == "" || targetID == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_artifact_edges
			(artifact_id, edge_kind, target_type, target_id, source_ledger_id, sequence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, artifactID, edgeKind, targetType, targetID, ledgerID, seq, time.Now().UTC().Unix()); err != nil {
		return fmt.Errorf("insert artifact evidence edge: %w", err)
	}
	return nil
}

func recordEvidenceErrorTx(ctx context.Context, tx *sql.Tx, ledgerID, entityType, entityID, kind, message string, seq int64) error {
	id := "evidence_error_" + stableID(ledgerID, entityType, entityID, kind, message)
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_evidence_errors
			(id, ledger_id, entity_type, entity_id, error_kind, message, sequence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, ledgerID, entityType, entityID, kind, message, seq, time.Now().UTC().Unix()); err != nil {
		return fmt.Errorf("record evidence projection error: %w", err)
	}
	return nil
}

func updateValidationPatternTx(ctx context.Context, tx *sql.Tx, delta claims.CanonicalDelta, validationType, artifactID, status, validationID string) error {
	if validationType == "" || !validationTerminal(status) {
		return nil
	}
	claimAction := mapNestedString(delta.Context, "claim", "action")
	artifactKind := ""
	if artifactID != "" {
		_ = tx.QueryRowContext(ctx, `SELECT artifact_kind FROM forest_artifacts WHERE artifact_id = ?`, artifactID).Scan(&artifactKind)
	}
	key := stableID("validation_pattern", claimAction, artifactKind, validationType)
	success, failure := 0, 0
	if validationFailureStatus(status) {
		failure = 1
	} else {
		success = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_validation_patterns
			(pattern_key, claim_action, artifact_kind, validation_type, success_count, failure_count, last_validation_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pattern_key) DO UPDATE SET
			success_count = success_count + excluded.success_count,
			failure_count = failure_count + excluded.failure_count,
			last_validation_id = excluded.last_validation_id,
			updated_at = excluded.updated_at
	`, key, claimAction, artifactKind, validationType, success, failure, validationID, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("update validation pattern: %w", err)
	}
	return nil
}

func validationStatusText(delta claims.CanonicalDelta, validation map[string]any) string {
	if status, ok := claims.DeltaActionValidationLifecycleStatus(delta.Action); ok {
		return string(status)
	}
	if delta.Action == claims.DeltaActionValidationEvaluated {
		return firstNonEmptyString(mapString(validation, "status"), mapString(validation, "verdict"))
	}
	return mapString(validation, "status")
}

func validationTerminal(status string) bool {
	switch claims.ValidationStatus(strings.TrimSpace(status)) {
	case claims.ValidationStatusValidated,
		claims.ValidationStatusValidationFailed,
		claims.ValidationStatusValidationFailedNotRequired,
		claims.ValidationStatusErrored,
		claims.ValidationStatusErroredNotRequired,
		claims.ValidationStatusQualityBarValidationFailed,
		claims.ValidationStatusQualityBarValidationFailedNotRequired,
		claims.ValidationStatusPassed,
		claims.ValidationStatusFailed,
		claims.ValidationStatusSkipped:
		return true
	default:
		return false
	}
}

func validationFailureStatus(status string) bool {
	s := claims.ValidationStatus(strings.TrimSpace(status))
	return s.IsBlockingFailure() || s.IsOptionalFailure() || s == claims.ValidationStatusFailed
}

func receiptValidation(validationType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(validationType))
	return normalized == "receipt" || strings.Contains(normalized, "receipt")
}

func mapFromContext(context map[string]any, key string) map[string]any {
	if context == nil {
		return nil
	}
	if typed, ok := context[key].(map[string]any); ok {
		return typed
	}
	return nil
}

func mapString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch raw := values[key].(type) {
	case string:
		return strings.TrimSpace(raw)
	case fmt.Stringer:
		return strings.TrimSpace(raw.String())
	default:
		return ""
	}
}

func mapNestedString(values map[string]any, outer, inner string) string {
	return mapString(mapFromContext(values, outer), inner)
}

func mapBool(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	switch raw := values[key].(type) {
	case bool:
		return raw
	case string:
		return strings.EqualFold(strings.TrimSpace(raw), "true")
	default:
		return false
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func actorRoute(ref claims.AgentRef) string {
	ref = ref.Normalized()
	return ref.RouteKey()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
