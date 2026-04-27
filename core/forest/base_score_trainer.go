package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
)

// ────────────────────────────────────────────────────────────────────
// Issue #8 — base score trainer (tracked goroutine)
// ────────────────────────────────────────────────────────────────────
//
// Tracks on m.wg, lease-coordinated through forest_projector_state,
// drain-then-sleep cadence. Errors are returned AND logged AND
// written as artifacts on the projector_state lease row (last_error /
// last_error_at) so operators can audit training health.
//
// No defensive recover. The function bodies provably can't panic:
// every slice access is bounded by len, every map write is into an
// initialized map, no type assertions, no division without explicit
// zero-guard.

const (
	// projectorBaseScoreTrainerName is the canonical lease holder
	// name for the trainer goroutine in forest_projector_state.
	projectorBaseScoreTrainerName = "base_score_trainer"
)

// startBaseScoreTrainer launches the tracked trainer goroutine.
// Mirrors the implicit-negative sweeper's lifecycle pattern:
// m.wg.Add(1), defer Done, exit on stopCh / runCtx cancellation.
func (m *MemoryForest) startBaseScoreTrainer() {
	if m == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runBaseScoreTrainerLoop()
	}()
}

// runBaseScoreTrainerLoop drives training under lease coordination
// on a time-based cadence (m.baseScoreTrainInterval, default 1h).
// Always sleeps the interval after a cycle — training is bounded by
// wall-clock not queue depth, because training examples are read,
// not consumed (a cycle that returns 606 examples will return the
// same 606 next cycle, so a "drain" loop would spin forever).
//
// Newly-arrived examples are picked up on the next interval — there
// is no value in re-training within seconds of the previous cycle:
// the SGD result over the same data is deterministic up to the small
// stochastic component, and the model store rejects non-improving
// candidates anyway.
func (m *MemoryForest) runBaseScoreTrainerLoop() {
	for {
		if m.shouldStopProjector() {
			return
		}
		if err := m.acquireBaseScoreTrainerLease(); err != nil {
			if errors.Is(err, errLeaseHeldByOther) {
				if !m.sleepProjector(projectorWaitForLease) {
					return
				}
				continue
			}
			slog.Warn("forest_base_score_trainer_lease_failed",
				"holder", m.projectorID, "err", err.Error())
			m.recordBaseScoreTrainerArtifact(err)
			if !m.sleepProjector(m.baseScoreTrainInterval) {
				return
			}
			continue
		}

		if _, err := m.runBaseScoreTrainOnce(m.runCtx); err != nil {
			slog.Warn("forest_base_score_trainer_failed",
				"err", err.Error())
			m.recordBaseScoreTrainerArtifact(err)
		}
		if !m.sleepProjector(m.baseScoreTrainInterval) {
			return
		}
	}
}

// acquireBaseScoreTrainerLease takes (or renews) the trainer's lease
// row in forest_projector_state. Reuses the same UPDATE-where-expired
// pattern as the branch projector + AntiPattern promoter.
func (m *MemoryForest) acquireBaseScoreTrainerLease() error {
	if err := m.ensureProjectorRow(projectorBaseScoreTrainerName); err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	expires := now + int64(projectorLeaseDuration.Seconds())
	res, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    leader_holder      = ?,
		       leader_lease_until = ?,
		       health_status      = ?,
		       updated_at         = ?
		WHERE  projector_name      = ?
		  AND  (leader_lease_until < ? OR leader_holder = ?)
	`, m.projectorID, expires, string(ProjectorHealthRunning), now,
		projectorBaseScoreTrainerName, now, m.projectorID)
	if err != nil {
		return fmt.Errorf("acquire base-score trainer lease: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("base-score trainer lease rows affected: %w", err)
	}
	if affected == 0 {
		return errLeaseHeldByOther
	}
	return nil
}

// recordBaseScoreTrainerArtifact writes the failure as an artifact on
// the trainer's projector_state row. Errors flow through the durable
// audit channel (last_error + last_error_at), not just slog. The
// write itself is best-effort — a failure here is logged but doesn't
// loop.
func (m *MemoryForest) recordBaseScoreTrainerArtifact(cause error) {
	if cause == nil || m.runCtx == nil || m.runCtx.Err() != nil {
		return
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return
	}
	msg := truncateError(cause.Error(), projectorErrTruncate)
	now := time.Now().UTC().Unix()
	if _, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    last_error    = ?,
		       last_error_at = ?,
		       updated_at    = ?
		WHERE  projector_name = ?
	`, msg, now, now, projectorBaseScoreTrainerName); err != nil {
		slog.Warn("forest_base_score_trainer_record_error_failed",
			"err", err.Error())
	}
}

// runBaseScoreTrainOnce performs one training cycle:
//
//  1. Sample up to baseScoreTrainBatch labeled examples
//  2. Run baseScoreEpochs of SGD with L1+L2 regularization
//  3. Compute training-set accuracy (proxy)
//  4. Save the trained model; promote if accuracy beats current
//     champion by ≥ improveDelta and we have ≥ minTraining examples
//  5. Hydrate the cache so subsequent retrievals see the new model
//
// Returns the number of trained examples (0 = idle, no work done).
// Errors are returned to the caller for artifact recording.
func (m *MemoryForest) runBaseScoreTrainOnce(ctx context.Context) (int, error) {
	examples, err := m.loadBaseScoreTrainingExamples(ctx, m.baseScoreTrainBatch)
	if err != nil {
		return 0, err
	}
	if len(examples) == 0 {
		return 0, nil
	}

	champion, _, err := m.baseScoreStore.LoadActive(ctx)
	if err != nil {
		return 0, fmt.Errorf("load champion for training baseline: %w", err)
	}

	initWeights, initBias := initialTrainingWeights(champion)
	trained := trainBaseScoreSGD(
		examples,
		initWeights,
		initBias,
		m.baseScoreEpochs,
		m.baseScoreLearningRate,
		m.baseScoreL1Reg,
		m.baseScoreL2Reg,
		m.baseScoreMaxWeight,
	)
	accuracy := evaluateBaseScoreAccuracy(examples, trained.weights, trained.bias)

	maxVersion, err := m.baseScoreStore.MaxVersion(ctx)
	if err != nil {
		return 0, err
	}
	model := &BaseScoreModel{
		ID:           "base_score_" + uuid.NewString(),
		Version:      maxVersion + 1,
		Weights:      trained.weights,
		Bias:         trained.bias,
		L1Norm:       trained.l1Norm,
		Accuracy:     accuracy,
		TrainingSize: int64(len(examples)),
		TrainedAt:    time.Now().UTC(),
	}

	enoughData := int64(len(examples)) >= int64(m.baseScoreMinTraining)
	beatsChampion := champion == nil || accuracy >= champion.Accuracy+m.baseScoreImproveDelta

	if enoughData && beatsChampion {
		model.Role = BaseScoreRoleChampion
		if err := m.baseScoreStore.Save(ctx, model); err != nil {
			return 0, err
		}
		if err := m.baseScoreStore.PromoteChampion(ctx, model.ID); err != nil {
			return 0, err
		}
	} else {
		// Save without promotion — the trained model becomes a row in
		// the table but doesn't serve traffic. Operator may promote
		// manually or future training cycles may produce a stronger
		// candidate.
		if err := m.baseScoreStore.Save(ctx, model); err != nil {
			return 0, err
		}
	}

	if _, err := m.hydrateBaseScoreCache(ctx); err != nil {
		// Cache hydration is best-effort; the new model is already
		// durable on disk. Log and continue — next training cycle
		// (or any process that calls hydrateBaseScoreCache) will
		// install it.
		slog.Warn("forest_base_score_trainer_hydrate_failed",
			"err", err.Error())
	}
	return len(examples), nil
}

// baseScoreTrainingExample is one (component-vector, outcome-label) pair
// sourced from forest_training_examples. The 13 components are the
// first 13 entries of the 53-D feature vector recorded at retrieval
// time — same ordering as BaseScoreComponentNames.
type baseScoreTrainingExample struct {
	components [BaseScoreComponentCount]float64
	label      float64 // 0 = failure, 1 = success
	weight     float64 // training-time weight (e.g., counterfactual rows < 1.0)
}

// loadBaseScoreTrainingExamples queries forest_training_examples for
// rows with explicit labels, sampling the most recent batch. Reads
// the features BLOB and decodes the first 13 entries as the
// component vector. utility_label is the outcome target.
//
// Bounds: every slice access is checked. A row with a malformed
// features blob is silently skipped (logged at debug); the rest of
// the batch proceeds.
func (m *MemoryForest) loadBaseScoreTrainingExamples(ctx context.Context, limit int) ([]baseScoreTrainingExample, error) {
	if m == nil || m.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		return nil, nil
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT features, utility_label, label_weight
		FROM   forest_training_examples
		WHERE  utility_label IS NOT NULL
		  AND  label_source IN (?, ?, ?)
		ORDER BY updated_at DESC
		LIMIT  ?
	`, string(LabelSourceExplicit), string(LabelSourceCounterfactual), string(LabelSourceImplicitNegative), limit)
	if err != nil {
		return nil, fmt.Errorf("query base-score training examples: %w", err)
	}
	defer rows.Close()

	out := make([]baseScoreTrainingExample, 0, limit)
	for rows.Next() {
		var (
			featuresBlob []byte
			utility      sql.NullFloat64
			weight       sql.NullFloat64
		)
		if err := rows.Scan(&featuresBlob, &utility, &weight); err != nil {
			return nil, fmt.Errorf("scan training example: %w", err)
		}
		if !utility.Valid {
			continue
		}
		components, ok := decodeFirstNFloat32sToFloat64(featuresBlob, BaseScoreComponentCount)
		if !ok {
			// Malformed feature blob: log debug and skip. Rest of
			// the batch is still useful.
			if m.logger != nil {
				m.logger.Debug("forest: base-score training example skipped (malformed features blob)")
			}
			continue
		}
		w := 1.0
		if weight.Valid && weight.Float64 > 0 {
			w = weight.Float64
		}
		var arr [BaseScoreComponentCount]float64
		for i := 0; i < BaseScoreComponentCount; i++ {
			arr[i] = components[i]
		}
		out = append(out, baseScoreTrainingExample{
			components: arr,
			label:      clamp01(utility.Float64),
			weight:     w,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate training examples: %w", err)
	}
	return out, nil
}

// decodeFirstNFloat32sToFloat64 reads the first n float32 values from
// a packFloat32s blob (used by recordRetrievalExamples) and returns
// them as float64. Returns ok=false if the blob is too short or
// otherwise malformed — caller skips the row rather than panicking.
func decodeFirstNFloat32sToFloat64(blob []byte, n int) ([]float64, bool) {
	if n <= 0 {
		return nil, false
	}
	const sizeofFloat32 = 4
	required := n * sizeofFloat32
	if len(blob) < required {
		return nil, false
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		offset := i * sizeofFloat32
		bits := uint32(blob[offset]) |
			uint32(blob[offset+1])<<8 |
			uint32(blob[offset+2])<<16 |
			uint32(blob[offset+3])<<24
		out[i] = float64(math.Float32frombits(bits))
	}
	return out, true
}

// trainedBaseScoreModel bundles the SGD output: the final weight
// vector + bias + L1 norm (the sum of |weights|, useful for diagnostic
// rollups + pruning candidate detection).
type trainedBaseScoreModel struct {
	weights [BaseScoreComponentCount]float64
	bias    float64
	l1Norm  float64
}

// initialTrainingWeights returns the starting point for SGD: the
// current champion's weights (warm start) or hardcoded defaults
// (cold start). Bias starts at 0.
func initialTrainingWeights(champion *BaseScoreModel) ([BaseScoreComponentCount]float64, float64) {
	var w [BaseScoreComponentCount]float64
	if champion != nil {
		w = champion.Weights
		return w, champion.Bias
	}
	for i := 0; i < BaseScoreComponentCount && i < len(defaultScoreWeights); i++ {
		w[i] = float64(defaultScoreWeights[i])
	}
	return w, 0
}

// trainBaseScoreSGD runs `epochs` passes of stochastic gradient
// descent over the supplied examples. Optimizes the logistic loss:
//
//   y_pred = σ(w·x + b)
//   loss   = -y·log(y_pred) - (1-y)·log(1-y_pred)
//
// Per-example update with L1 + L2 regularization:
//
//   w_i ← w_i - lr · ((y_pred - y)·x_i  +  λ_L2·w_i  +  λ_L1·sign(w_i))
//   b   ← b   - lr · (y_pred - y)
//
// After every step weights are clamped to ±maxWeight so a pathological
// outlier batch can't produce unbounded values. No nil access; no
// division by zero (sigmoid is well-bounded).
func trainBaseScoreSGD(
	examples []baseScoreTrainingExample,
	initWeights [BaseScoreComponentCount]float64,
	initBias float64,
	epochs int,
	learningRate, l1Reg, l2Reg, maxWeight float64,
) trainedBaseScoreModel {
	weights := initWeights
	bias := initBias

	if len(examples) == 0 || epochs <= 0 {
		return trainedBaseScoreModel{
			weights: weights,
			bias:    bias,
			l1Norm:  weightVectorL1(weights),
		}
	}
	for epoch := 0; epoch < epochs; epoch++ {
		for k := range examples {
			ex := &examples[k]
			pred := baseScoreSigmoid(dotComponents(weights, ex.components) + bias)
			err := pred - ex.label
			step := learningRate * ex.weight
			for i := 0; i < BaseScoreComponentCount; i++ {
				gradient := err*ex.components[i] +
					l2Reg*weights[i] +
					l1Reg*signFloat64(weights[i])
				weights[i] -= step * gradient
				weights[i] = clampFloat64(weights[i], -maxWeight, maxWeight)
			}
			bias -= step * err
		}
	}
	return trainedBaseScoreModel{
		weights: weights,
		bias:    bias,
		l1Norm:  weightVectorL1(weights),
	}
}

// evaluateBaseScoreAccuracy computes a simple accuracy proxy: the
// fraction of examples where the sigmoid prediction agrees with the
// ground-truth label after thresholding at 0.5. Bounded to [0,1].
// Used by the promotion gate (improvement threshold).
func evaluateBaseScoreAccuracy(examples []baseScoreTrainingExample, weights [BaseScoreComponentCount]float64, bias float64) float64 {
	if len(examples) == 0 {
		return 0
	}
	correct := 0
	for k := range examples {
		ex := &examples[k]
		pred := baseScoreSigmoid(dotComponents(weights, ex.components) + bias)
		predLabel := 0.0
		if pred >= 0.5 {
			predLabel = 1.0
		}
		groundLabel := 0.0
		if ex.label >= 0.5 {
			groundLabel = 1.0
		}
		if predLabel == groundLabel {
			correct++
		}
	}
	return float64(correct) / float64(len(examples))
}

// dotComponents returns Σ(weights_i × components_i).
func dotComponents(weights, components [BaseScoreComponentCount]float64) float64 {
	var sum float64
	for i := 0; i < BaseScoreComponentCount; i++ {
		sum += weights[i] * components[i]
	}
	return sum
}

// baseScoreSigmoid implements the logistic function. Bounded output ∈ (0,1)
// for any finite real input. Saturates safely at extremes — Go's
// math.Exp returns +Inf for large positives and 0 for large
// negatives, both of which produce well-defined sigmoid output.
func baseScoreSigmoid(z float64) float64 {
	if z >= 0 {
		exp := math.Exp(-z)
		return 1.0 / (1.0 + exp)
	}
	exp := math.Exp(z)
	return exp / (1.0 + exp)
}

// signFloat64 returns -1, 0, or 1 — used in the L1 regularization
// gradient. Zero handling is deliberate: a weight at exactly 0 has
// no L1 push (subgradient is in [-1, 1]; we use 0 as the canonical
// representative).
func signFloat64(x float64) float64 {
	if x > 0 {
		return 1
	}
	if x < 0 {
		return -1
	}
	return 0
}

// clampFloat64 bounds x to [lo, hi]. Used to clamp weights after
// every SGD step.
func clampFloat64(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// weightVectorL1 returns Σ|weights_i| — the L1 norm. Reported on
// trained models so operators can detect when L1 reg has driven
// many weights toward zero (component pruning signal).
func weightVectorL1(weights [BaseScoreComponentCount]float64) float64 {
	var sum float64
	for i := 0; i < BaseScoreComponentCount; i++ {
		sum += math.Abs(weights[i])
	}
	return sum
}
