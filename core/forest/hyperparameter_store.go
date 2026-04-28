package forest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// hyperparameterStoreSchema is the bbolt-style single-row table that
// persists the active HyperParameters snapshot. Single row keyed by
// the literal "active" — the tuner is the only writer and it
// always replaces the row in full (atomic swap semantics).
//
// We persist the snapshot as JSON in `payload` rather than spreading
// values across columns. Justification: hyperparameters evolve over
// time; new fields will be added; stable column-by-column schema
// would require a migration per addition. JSON-in-a-blob accepts
// new fields without migrations and the snapshot is small (≪ 1 KiB).
//
// Concurrency: SQLite serializes writes via the existing forest
// connection; the tuner's atomic.Pointer swap guarantees no reader
// observes a partial write.
const hyperparameterStoreSchema = `
CREATE TABLE IF NOT EXISTS forest_hyperparameters (
	id            TEXT NOT NULL PRIMARY KEY,
	snapshot_id   INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL,
	samples_seen  INTEGER NOT NULL DEFAULT 0,
	payload       TEXT NOT NULL
);
`

// hyperparameterStoreRowID is the literal primary-key value. Single
// row per forest — the table is degenerate but using a real row
// (rather than a config-style key/value bucket) lets us roll out
// future per-session or per-version snapshots by adding rows.
const hyperparameterStoreRowID = "active"

// HyperParameterStore persists HyperParameters snapshots to the
// forest's content database. One snapshot at a time: write replaces
// the active row atomically inside the surrounding bolt transaction;
// readers always see the current snapshot or the prior one, never a
// partial mix.
//
// The store is decoupled from HyperParameterTuner so tests can
// exercise persistence in isolation and so future tooling (audit
// CLIs, snapshot migration scripts) can read the data without
// instantiating the full forest runtime.
type HyperParameterStore struct {
	db *sql.DB
}

// NewHyperParameterStore wraps an existing forest *sql.DB. Caller
// owns the DB lifecycle; the store does not Close it.
func NewHyperParameterStore(db *sql.DB) *HyperParameterStore {
	return &HyperParameterStore{db: db}
}

// EnsureSchema creates the forest_hyperparameters table if missing.
// Idempotent. Called once during forest open.
func (s *HyperParameterStore) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("hyperparameter store: nil db")
	}
	if _, err := s.db.ExecContext(ctx, hyperparameterStoreSchema); err != nil {
		return fmt.Errorf("create forest_hyperparameters table: %w", err)
	}
	return nil
}

// Load returns the persisted active snapshot, or (nil, nil) when no
// row exists. Validation runs before return — a corrupt persisted
// snapshot returns nil + error so the caller falls back to
// placeholder/workload-init.
func (s *HyperParameterStore) Load(ctx context.Context) (*HyperParameters, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("hyperparameter store: nil db")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT snapshot_id, updated_at, samples_seen, payload
		 FROM forest_hyperparameters WHERE id = ?`,
		hyperparameterStoreRowID,
	)
	var (
		snapshotID  int64
		updatedAtNs int64
		samples     int64
		payload     string
	)
	switch err := row.Scan(&snapshotID, &updatedAtNs, &samples, &payload); err {
	case nil:
		// fall through
	case sql.ErrNoRows:
		return nil, nil
	default:
		return nil, fmt.Errorf("load hyperparameters row: %w", err)
	}

	hp, err := decodeHyperParameters([]byte(payload))
	if err != nil {
		return nil, fmt.Errorf("decode persisted hyperparameters: %w", err)
	}
	hp.SnapshotID = uint64(snapshotID)
	hp.UpdatedAt = time.Unix(0, updatedAtNs).UTC()
	hp.SamplesObservedAtFit = samples

	if err := hp.Validate(); err != nil {
		return nil, fmt.Errorf("persisted hyperparameters invalid: %w", err)
	}
	return hp, nil
}

// Save persists the snapshot, replacing the active row. The caller
// is responsible for ensuring the snapshot has a fresh SnapshotID
// (the tuner increments before save). Validates before writing —
// invalid snapshots are rejected rather than persisted.
func (s *HyperParameterStore) Save(ctx context.Context, hp *HyperParameters) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("hyperparameter store: nil db")
	}
	if err := hp.Validate(); err != nil {
		return fmt.Errorf("save hyperparameters: %w", err)
	}
	payload, err := encodeHyperParameters(hp)
	if err != nil {
		return fmt.Errorf("encode hyperparameters: %w", err)
	}
	updatedAtNs := hp.UpdatedAt.UnixNano()
	if hp.UpdatedAt.IsZero() {
		updatedAtNs = time.Now().UTC().UnixNano()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO forest_hyperparameters (id, snapshot_id, updated_at, samples_seen, payload)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			snapshot_id  = excluded.snapshot_id,
			updated_at   = excluded.updated_at,
			samples_seen = excluded.samples_seen,
			payload      = excluded.payload
	`, hyperparameterStoreRowID, int64(hp.SnapshotID), updatedAtNs, hp.SamplesObservedAtFit, string(payload))
	if err != nil {
		return fmt.Errorf("upsert hyperparameters row: %w", err)
	}
	return nil
}

// hyperparameterPayload is the JSON-serialized shape of a snapshot.
// Field names use snake_case to match allHyperParameterFields and
// any future external tooling. Provenance is encoded as a sibling
// map keyed by the same field names.
type hyperparameterPayload struct {
	SubstrateDebounceNs        int64                           `json:"substrate_debounce_ns"`
	TrainingDebounceNs         int64                           `json:"training_debounce_ns"`
	ReplayDelayNs              int64                           `json:"replay_delay_ns"`
	MaintenanceRetryNs         int64                           `json:"maintenance_retry_ns"`
	CounterfactualWeight       float64                         `json:"counterfactual_weight"`
	CounterfactualWindowNs     int64                           `json:"counterfactual_window_ns"`
	ImplicitNegativeWeight     float64                         `json:"implicit_negative_weight"`
	ImplicitNegativeHorizonNs  int64                           `json:"implicit_negative_horizon_ns"`
	ExplorationRate            float64                         `json:"exploration_rate"`
	ExplorationLabelWeight     float64                         `json:"exploration_label_weight"`
	DiversityLambda            float64                         `json:"diversity_lambda"`
	RetrievalCooldownPenalty   float64                         `json:"retrieval_cooldown_penalty"`
	RetrievalCooldownWindow    int                             `json:"retrieval_cooldown_window"`
	BaseScoreLearningRate      float64                         `json:"base_score_learning_rate"`
	BaseScoreL1Reg             float64                         `json:"base_score_l1_reg"`
	BaseScoreL2Reg             float64                         `json:"base_score_l2_reg"`
	BaseScoreEpochs            int                             `json:"base_score_epochs"`
	BaseScoreImprovementThresh float64                         `json:"base_score_improvement_threshold"`
	BaseScoreMinTraining       int                             `json:"base_score_min_training_examples"`
	BaseScoreMaxWeight         float64                         `json:"base_score_max_weight"`
	BaseScorePruningThreshold  float64                         `json:"base_score_pruning_threshold"`
	Provenance                 map[string]HyperParameterSource `json:"provenance"`
}

func encodeHyperParameters(hp *HyperParameters) ([]byte, error) {
	if hp == nil {
		return nil, fmt.Errorf("nil snapshot")
	}
	p := hyperparameterPayload{
		SubstrateDebounceNs:        hp.SubstrateDebounce.Nanoseconds(),
		TrainingDebounceNs:         hp.TrainingDebounce.Nanoseconds(),
		ReplayDelayNs:              hp.ReplayDelay.Nanoseconds(),
		MaintenanceRetryNs:         hp.MaintenanceRetry.Nanoseconds(),
		CounterfactualWeight:       hp.CounterfactualWeight,
		CounterfactualWindowNs:     hp.CounterfactualWindow.Nanoseconds(),
		ImplicitNegativeWeight:     hp.ImplicitNegativeWeight,
		ImplicitNegativeHorizonNs:  hp.ImplicitNegativeHorizon.Nanoseconds(),
		ExplorationRate:            hp.ExplorationRate,
		ExplorationLabelWeight:     hp.ExplorationLabelWeight,
		DiversityLambda:            hp.DiversityLambda,
		RetrievalCooldownPenalty:   hp.RetrievalCooldownPenalty,
		RetrievalCooldownWindow:    hp.RetrievalCooldownWindow,
		BaseScoreLearningRate:      hp.BaseScoreLearningRate,
		BaseScoreL1Reg:             hp.BaseScoreL1Reg,
		BaseScoreL2Reg:             hp.BaseScoreL2Reg,
		BaseScoreEpochs:            hp.BaseScoreEpochs,
		BaseScoreImprovementThresh: hp.BaseScoreImprovementThreshold,
		BaseScoreMinTraining:       hp.BaseScoreMinTrainingExamples,
		BaseScoreMaxWeight:         hp.BaseScoreMaxWeight,
		BaseScorePruningThreshold:  hp.BaseScorePruningThreshold,
		Provenance:                 hp.Provenance,
	}
	return json.Marshal(p)
}

func decodeHyperParameters(raw []byte) (*HyperParameters, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	var p hyperparameterPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	hp := &HyperParameters{
		SubstrateDebounce:             time.Duration(p.SubstrateDebounceNs),
		TrainingDebounce:              time.Duration(p.TrainingDebounceNs),
		ReplayDelay:                   time.Duration(p.ReplayDelayNs),
		MaintenanceRetry:              time.Duration(p.MaintenanceRetryNs),
		CounterfactualWeight:          p.CounterfactualWeight,
		CounterfactualWindow:          time.Duration(p.CounterfactualWindowNs),
		ImplicitNegativeWeight:        p.ImplicitNegativeWeight,
		ImplicitNegativeHorizon:       time.Duration(p.ImplicitNegativeHorizonNs),
		ExplorationRate:               p.ExplorationRate,
		ExplorationLabelWeight:        p.ExplorationLabelWeight,
		DiversityLambda:               p.DiversityLambda,
		RetrievalCooldownPenalty:      p.RetrievalCooldownPenalty,
		RetrievalCooldownWindow:       p.RetrievalCooldownWindow,
		BaseScoreLearningRate:         p.BaseScoreLearningRate,
		BaseScoreL1Reg:                p.BaseScoreL1Reg,
		BaseScoreL2Reg:                p.BaseScoreL2Reg,
		BaseScoreEpochs:               p.BaseScoreEpochs,
		BaseScoreImprovementThreshold: p.BaseScoreImprovementThresh,
		BaseScoreMinTrainingExamples:  p.BaseScoreMinTraining,
		BaseScoreMaxWeight:            p.BaseScoreMaxWeight,
		BaseScorePruningThreshold:     p.BaseScorePruningThreshold,
		Provenance:                    p.Provenance,
	}
	return hp, nil
}
