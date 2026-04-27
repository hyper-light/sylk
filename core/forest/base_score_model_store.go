package forest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #8 — BaseScoreModelStore: persistence for trained scorers
// ────────────────────────────────────────────────────────────────────
//
// SQLite-backed CRUD over forest_base_score_models. Every code path
// is bounds-checked, every nil case returns an error, every result
// is validated before construction. The store does not panic.

// errBaseScoreNoChampion is returned when the active-models query
// finds no row tagged champion. Callers treat this as a cold-start
// signal — fall back to hardcoded defaults until a model is trained
// and promoted.
var errBaseScoreNoChampion = errors.New("no champion base-score model")

// errBaseScoreInvalidWeights signals that a deserialized weights
// payload doesn't match the BaseScoreComponentCount-length contract.
// Callers must reject the model rather than serve a malformed scorer.
var errBaseScoreInvalidWeights = errors.New("invalid base-score weights length")

// baseScoreModelStore wraps *sql.DB with the schema-aware methods
// needed by the trainer + serving cache. Stateless: every method is
// safe to call concurrently from multiple goroutines.
type baseScoreModelStore struct {
	db *sql.DB
}

func newBaseScoreModelStore(db *sql.DB) *baseScoreModelStore {
	return &baseScoreModelStore{db: db}
}

// LoadActive returns (champion, challenger) — either may be nil if
// no model is currently tagged with that role. Cold-start: both nil
// + nil error.
func (s *baseScoreModelStore) LoadActive(ctx context.Context) (*BaseScoreModel, *BaseScoreModel, error) {
	if s == nil || s.db == nil {
		return nil, nil, errors.New("base-score store not initialized")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, version, weights_blob, bias, l1_norm, accuracy,
		       training_size, trained_at, role
		FROM   forest_base_score_models
		WHERE  role IN (?, ?)
	`, string(BaseScoreRoleChampion), string(BaseScoreRoleChallenger))
	if err != nil {
		return nil, nil, fmt.Errorf("query active base-score models: %w", err)
	}
	defer rows.Close()

	var champion, challenger *BaseScoreModel
	for rows.Next() {
		model, scanErr := scanBaseScoreRow(rows)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		switch model.Role {
		case BaseScoreRoleChampion:
			champion = model
		case BaseScoreRoleChallenger:
			challenger = model
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate active base-score models: %w", err)
	}
	return champion, challenger, nil
}

// LoadByVersion fetches one model row by its numeric version.
// Returns (nil, nil) if no row matches — used by the audit
// reconstructor to map a recorded base_score_version back to its
// weights.
func (s *baseScoreModelStore) LoadByVersion(ctx context.Context, version int64) (*BaseScoreModel, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("base-score store not initialized")
	}
	if version <= 0 {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, version, weights_blob, bias, l1_norm, accuracy,
		       training_size, trained_at, role
		FROM   forest_base_score_models
		WHERE  version = ?
	`, version)
	model, err := scanBaseScoreSingleRow(row)
	switch {
	case err == nil:
		return model, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	default:
		return nil, err
	}
}

// MaxVersion returns the largest version number in the table.
// Returns 0 (and no error) when the table is empty — first training
// run uses version 1.
func (s *baseScoreModelStore) MaxVersion(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("base-score store not initialized")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM forest_base_score_models
	`)
	var v int64
	if err := row.Scan(&v); err != nil {
		return 0, fmt.Errorf("max base-score version: %w", err)
	}
	return v, nil
}

// Save inserts a new model row. Caller is responsible for assigning
// a fresh version (typically MaxVersion + 1) and a stable ID. Idempotent
// via the version UNIQUE index — re-saving the same version returns
// an error so the trainer can detect concurrent training conflicts.
func (s *baseScoreModelStore) Save(ctx context.Context, model *BaseScoreModel) error {
	if s == nil || s.db == nil {
		return errors.New("base-score store not initialized")
	}
	if model == nil {
		return errors.New("nil base-score model")
	}
	weightsJSON, err := encodeBaseScoreWeights(model.Weights)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO forest_base_score_models
		(id, version, weights_blob, bias, l1_norm, accuracy,
		 training_size, trained_at, role)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		model.ID,
		model.Version,
		weightsJSON,
		model.Bias,
		model.L1Norm,
		model.Accuracy,
		model.TrainingSize,
		model.TrainedAt.UTC().Unix(),
		string(model.Role),
	)
	if err != nil {
		return fmt.Errorf("insert base-score model: %w", err)
	}
	return nil
}

// PromoteChampion atomically demotes the current champion (if any)
// and elevates `model` to champion role. The challenger flag is
// left untouched. One transaction, both operations or neither.
func (s *baseScoreModelStore) PromoteChampion(ctx context.Context, modelID string) error {
	if s == nil || s.db == nil {
		return errors.New("base-score store not initialized")
	}
	if modelID == "" {
		return errors.New("empty model id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin promote tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE forest_base_score_models
		SET    role = ''
		WHERE  role = ?
	`, string(BaseScoreRoleChampion)); err != nil {
		return fmt.Errorf("demote champion: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE forest_base_score_models
		SET    role = ?
		WHERE  id = ?
	`, string(BaseScoreRoleChampion), modelID); err != nil {
		return fmt.Errorf("promote model %s: %w", modelID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit promote tx: %w", err)
	}
	return nil
}

// SetChallenger assigns the challenger role to `modelID`, demoting
// any prior challenger. One transaction; idempotent.
func (s *baseScoreModelStore) SetChallenger(ctx context.Context, modelID string) error {
	if s == nil || s.db == nil {
		return errors.New("base-score store not initialized")
	}
	if modelID == "" {
		return errors.New("empty model id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin challenger tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE forest_base_score_models
		SET    role = ''
		WHERE  role = ?
	`, string(BaseScoreRoleChallenger)); err != nil {
		return fmt.Errorf("demote challenger: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE forest_base_score_models
		SET    role = ?
		WHERE  id = ?
	`, string(BaseScoreRoleChallenger), modelID); err != nil {
		return fmt.Errorf("set challenger %s: %w", modelID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit challenger tx: %w", err)
	}
	return nil
}

// scanBaseScoreRow decodes one row of forest_base_score_models from
// a *sql.Rows cursor. Used by LoadActive's iteration loop.
func scanBaseScoreRow(rows *sql.Rows) (*BaseScoreModel, error) {
	var (
		id           string
		version      int64
		weightsJSON  string
		bias         float64
		l1Norm       float64
		accuracy     float64
		trainingSize int64
		trainedAt    int64
		roleStr      string
	)
	if err := rows.Scan(&id, &version, &weightsJSON, &bias, &l1Norm, &accuracy, &trainingSize, &trainedAt, &roleStr); err != nil {
		return nil, fmt.Errorf("scan base-score row: %w", err)
	}
	return buildBaseScoreModel(id, version, weightsJSON, bias, l1Norm, accuracy, trainingSize, trainedAt, roleStr)
}

// scanBaseScoreSingleRow decodes one row from a *sql.Row (single-row
// query). The pattern is parallel to scanBaseScoreRow.
func scanBaseScoreSingleRow(row *sql.Row) (*BaseScoreModel, error) {
	var (
		id           string
		version      int64
		weightsJSON  string
		bias         float64
		l1Norm       float64
		accuracy     float64
		trainingSize int64
		trainedAt    int64
		roleStr      string
	)
	if err := row.Scan(&id, &version, &weightsJSON, &bias, &l1Norm, &accuracy, &trainingSize, &trainedAt, &roleStr); err != nil {
		return nil, err
	}
	return buildBaseScoreModel(id, version, weightsJSON, bias, l1Norm, accuracy, trainingSize, trainedAt, roleStr)
}

// buildBaseScoreModel constructs a validated BaseScoreModel from raw
// scan outputs. Returns errBaseScoreInvalidWeights if the JSON
// payload deserializes to a wrong-length slice.
func buildBaseScoreModel(
	id string,
	version int64,
	weightsJSON string,
	bias, l1Norm, accuracy float64,
	trainingSize, trainedAt int64,
	roleStr string,
) (*BaseScoreModel, error) {
	weights, err := decodeBaseScoreWeights(weightsJSON)
	if err != nil {
		return nil, err
	}
	return &BaseScoreModel{
		ID:           id,
		Version:      version,
		Weights:      weights,
		Bias:         bias,
		L1Norm:       l1Norm,
		Accuracy:     accuracy,
		TrainingSize: trainingSize,
		TrainedAt:    time.Unix(trainedAt, 0).UTC(),
		Role:         BaseScoreRole(roleStr),
	}, nil
}

// encodeBaseScoreWeights JSON-encodes the fixed-length weight array.
// Returns an error if marshalling fails.
func encodeBaseScoreWeights(weights [BaseScoreComponentCount]float64) (string, error) {
	data, err := json.Marshal(weights)
	if err != nil {
		return "", fmt.Errorf("marshal base-score weights: %w", err)
	}
	return string(data), nil
}

// decodeBaseScoreWeights JSON-decodes a weight array, validating
// length. A wrong-length payload returns errBaseScoreInvalidWeights
// rather than silently truncating or padding.
func decodeBaseScoreWeights(raw string) ([BaseScoreComponentCount]float64, error) {
	var zero [BaseScoreComponentCount]float64
	if raw == "" {
		return zero, fmt.Errorf("decode base-score weights: empty payload")
	}
	var slice []float64
	if err := json.Unmarshal([]byte(raw), &slice); err != nil {
		return zero, fmt.Errorf("decode base-score weights: %w", err)
	}
	if len(slice) != BaseScoreComponentCount {
		return zero, fmt.Errorf("%w: got %d, want %d", errBaseScoreInvalidWeights, len(slice), BaseScoreComponentCount)
	}
	var out [BaseScoreComponentCount]float64
	for i := 0; i < BaseScoreComponentCount; i++ {
		out[i] = slice[i]
	}
	return out, nil
}
