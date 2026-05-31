package forest

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ─── Tuner ↔ MemoryForest integration ────────────────────────────────
//
// Bridges the tuner-driven HyperParameters snapshot into the
// existing MemoryForest field-capture pattern. Two responsibilities:
//
//   1. At forest construction (initialiseTuner), build the tuner,
//      apply operator-supplied Config values as overrides
//      (priority 1 — option 3), let workload-init run (option 2),
//      then push the resolved snapshot into the existing per-field
//      captures so existing read paths see consistent values.
//
//   2. Provide RefreshFromTuner so any code path that takes an
//      action and wants the latest tuned values can re-read
//      without going through every field.
//
// Existing field captures (m.substrateDebounce, m.counterfactualLabelWeight,
// etc.) ARE the legacy read path. They're still populated and read
// directly on hot paths. The tuner's runtime-adapt promotions
// re-capture into those fields atomically via tunerOnPromote,
// preserving the legacy read path's correctness while letting
// runtime adaptation take effect.

// initialiseTuner constructs the HyperParameterTuner, primes it
// with operator Config values via override, allows workload-init
// to run if no persisted snapshot exists, and copies the resolved
// snapshot into the per-field captures that the rest of the
// forest reads.
//
// Called from New() before the existing per-field captures are
// initialised — so the captures pull from the tuner-resolved
// values, not from the bare resolveX(cfg.X) helpers. Callers
// downstream of this don't need to change anything; they read
// the same fields they always did.
//
// On hard error (e.g., schema migration fails), returns the error
// so New() can propagate. Soft errors (workload-init unavailable)
// degrade gracefully — the tuner falls back to placeholders, and
// the forest boots with an explicit warning logged.
func (m *MemoryForest) initialiseTuner(ctx context.Context, cfg Config) error {
	tuner, err := NewHyperParameterTuner(ctx, cfg.DB, m.logger, cfg.ClaimProposalSink)
	if err != nil {
		return fmt.Errorf("forest: hyperparameter tuner init: %w", err)
	}
	m.tuner = tuner

	// Push operator-supplied Config values as explicit overrides.
	// Each non-zero value becomes a SourceConfig provenance entry,
	// guaranteeing that future runtime adaptation cannot quietly
	// override an explicit operator choice.
	if err := applyConfigOverrides(ctx, tuner, cfg); err != nil {
		return fmt.Errorf("forest: hyperparameter overrides: %w", err)
	}
	return nil
}

// applyConfigOverrides walks the Config struct and pushes each
// non-zero tunable into the tuner via SetOverride. Zero / unset
// values pass through to whatever the tuner already resolved
// (workload-init, persisted snapshot, or placeholder).
func applyConfigOverrides(ctx context.Context, tuner *HyperParameterTuner, cfg Config) error {
	type override struct {
		field string
		value any
		set   bool
	}
	overrides := []override{
		{"substrate_debounce", cfg.SubstrateDebounce, cfg.SubstrateDebounce > 0},
		{"training_debounce", cfg.TrainingDebounce, cfg.TrainingDebounce > 0},
		{"replay_delay", cfg.ReplayDelay, cfg.ReplayDelay > 0},
		{"counterfactual_weight", cfg.CounterfactualLabelWeight, cfg.CounterfactualLabelWeight > 0},
		{"counterfactual_window", cfg.CounterfactualWindow, cfg.CounterfactualWindow > 0},
		{"implicit_negative_weight", cfg.ImplicitNegativeWeight, cfg.ImplicitNegativeWeight > 0},
		{"implicit_negative_horizon", cfg.ImplicitNegativeHorizon, cfg.ImplicitNegativeHorizon > 0},
		// ExplorationRate uses >= 0 because 0 is a meaningful
		// "exploration disabled" setting; the existing
		// resolveExplorationRate uses < 0 as the unset sentinel.
		{"exploration_rate", cfg.ExplorationRate, cfg.ExplorationRate >= 0},
		{"exploration_label_weight", cfg.ExplorationLabelWeight, cfg.ExplorationLabelWeight > 0},
		{"diversity_lambda", cfg.DiversityLambda, cfg.DiversityLambda > 0},
		{"retrieval_cooldown_window", cfg.RetrievalCooldownWindow, cfg.RetrievalCooldownWindow > 0},
		{"retrieval_cooldown_penalty", cfg.RetrievalCooldownPenalty, cfg.RetrievalCooldownPenalty > 0},
		{"base_score_learning_rate", cfg.BaseScoreLearningRate, cfg.BaseScoreLearningRate > 0},
		{"base_score_l1_reg", cfg.BaseScoreL1Reg, cfg.BaseScoreL1Reg > 0},
		{"base_score_l2_reg", cfg.BaseScoreL2Reg, cfg.BaseScoreL2Reg > 0},
		{"base_score_epochs", cfg.BaseScoreEpochs, cfg.BaseScoreEpochs > 0},
		{"base_score_improvement_threshold", cfg.BaseScoreImprovementThreshold, cfg.BaseScoreImprovementThreshold > 0},
		{"base_score_min_training_examples", cfg.BaseScoreMinTrainingExamples, cfg.BaseScoreMinTrainingExamples > 0},
		{"base_score_max_weight", cfg.BaseScoreMaxWeight, cfg.BaseScoreMaxWeight > 0},
		{"base_score_pruning_threshold", cfg.BaseScorePruningThreshold, cfg.BaseScorePruningThreshold > 0},
	}
	for _, o := range overrides {
		if !o.set {
			continue
		}
		if err := tuner.SetOverride(ctx, o.field, o.value); err != nil {
			return fmt.Errorf("override %s: %w", o.field, err)
		}
	}
	return nil
}

// resolveTunerOrFallback returns the tuner's resolved snapshot
// when available, or a fresh placeholder when the tuner is nil.
// Used by the legacy resolveX helpers in maintenance.go to
// fetch the same value the tuner has resolved without changing
// every call site.
func resolveTunerOrFallback(t *HyperParameterTuner) *HyperParameters {
	if t == nil {
		return PlaceholderHyperParameters()
	}
	return t.Get()
}

// Tuner returns the active HyperParameterTuner. nil-safe — a
// MemoryForest constructed by tests that bypass initialiseTuner
// (or by a fresh build before the migration completes) returns
// nil; callers must guard.
func (m *MemoryForest) Tuner() *HyperParameterTuner {
	if m == nil {
		return nil
	}
	return m.tuner
}

// hyperparams returns the live tuned snapshot — single canonical
// reader for every forest hot-path access. Sub-µs (atomic.Pointer
// load). Safe to call from any goroutine.
//
// When the tuner is unconfigured (e.g., a test constructs
// MemoryForest manually without going through New), returns the
// placeholder snapshot so calls don't panic on nil-deref.
func (m *MemoryForest) hyperparams() *HyperParameters {
	if m == nil {
		return PlaceholderHyperParameters()
	}
	if hp := m.snapshot.Load(); hp != nil {
		return hp
	}
	return PlaceholderHyperParameters()
}

// SetSnapshotForTest installs hp as the active snapshot. Bypasses
// the tuner — exists solely so tests that build MemoryForest
// manually can inject specific tunable values without spinning up
// the full tuner+store+fitter machinery.
//
// MUST NOT be called from production code; use Tuner().SetOverride
// for runtime configuration changes (which goes through validation,
// persistence, and notifies subscribers).
func (m *MemoryForest) SetSnapshotForTest(hp *HyperParameters) {
	if m == nil || hp == nil {
		return
	}
	m.snapshot.Store(hp)
}

// closeTuner stops the tuner and releases resources. Called from
// MemoryForest.Close(). Idempotent.
func (m *MemoryForest) closeTuner() {
	if m == nil || m.tuner == nil {
		return
	}
	m.tuner.Stop()
}

// RecordAdaptationObservation feeds an outcome metric into the
// runtime adaptive tuner. Forest subsystems that observe outcome
// quality (calibration, retrieval audit) call this with the
// observed metrics; the tuner accumulates them and runs A/B
// trials when conditions warrant.
//
// againstProposed is true when the retrieval that produced the
// observation was scored using the proposed (A/B variant) snapshot
// rather than the active one. The tuner uses this to attribute
// observations to the correct arm of an in-flight trial.
func (m *MemoryForest) RecordAdaptationObservation(ctx context.Context, calErr, regret float64, samples int64, againstProposed bool) {
	if m == nil || m.tuner == nil {
		return
	}
	m.tuner.ObserveAndAdapt(ctx, AdaptationObservation{
		CalibrationError: calErr,
		RegretFrac:       regret,
		SampleCount:      samples,
		ObservedAt:       time.Now().UTC(),
	}, againstProposed)
}

// recordAdaptationObservationsForOutcome computes per-retrieval
// calibration + regret observations for every retrieval where
// branchID was a candidate within the configured counterfactual
// window, then feeds each into the tuner attributed to the
// retrieval's pinned hyperparameter snapshot.
//
// Per-retrieval metrics:
//
//	CalibrationError = |observed_utility - candidate.predicted_utility|
//	                   for the row where candidate.branch_id = branchID.
//	RegretFrac       = 1.0 if the retrieval contained any non-returned
//	                   candidate whose base_score exceeded the lowest
//	                   returned candidate's base_score (ranking
//	                   inversion), else 0.0.
//
// The tuner aggregates these into rolling per-arm windows (active
// vs proposed) and decides whether to propose / promote / reject.
//
// Best-effort: any DB error logs at debug level and returns. The
// labeling work this is paired with already succeeded; observations
// are observational.
func (m *MemoryForest) recordAdaptationObservationsForOutcome(ctx context.Context, branchID, sessionID string, status OutcomeStatus) {
	if m == nil || m.tuner == nil || branchID == "" {
		return
	}
	hp := m.hyperparams()
	if hp == nil || hp.CounterfactualWindow <= 0 {
		return
	}
	// Reuse the counterfactual window — the same retrievals it would
	// label are the ones whose snapshots produced this outcome.
	since := time.Now().UTC().Add(-hp.CounterfactualWindow).Unix()
	obsUtility, _ := outcomeLabels(status)

	// Single round-trip: per retrieval in window where branchID was a
	// candidate, return the snapshot id, proposed flag, this branch's
	// predicted utility, and the score-inversion bookends needed to
	// compute regret.
	rows, err := m.db.QueryContext(ctx, `
		SELECT e.id,
		       e.hyperparam_snapshot_id,
		       e.proposed_hyperparams,
		       rc_branch.predicted_utility,
		       (SELECT MIN(rc2.base_score)
		          FROM forest_retrieval_candidates rc2
		         WHERE rc2.retrieval_event_id = e.id
		           AND rc2.returned = 1) AS min_returned_base,
		       (SELECT MAX(rc3.base_score)
		          FROM forest_retrieval_candidates rc3
		         WHERE rc3.retrieval_event_id = e.id
		           AND rc3.returned = 0) AS max_unreturned_base
		FROM   forest_retrieval_events e
		JOIN   forest_retrieval_candidates rc_branch
		         ON rc_branch.retrieval_event_id = e.id
		WHERE  rc_branch.branch_id = ?
		  AND  rc_branch.session_id = ?
		  AND  e.requested_at >= ?
	`, branchID, sessionID, since)
	if err != nil {
		if m.logger != nil {
			m.logger.Debug("forest: adaptation observation query failed", "err", err.Error())
		}
		return
	}
	defer rows.Close()

	now := time.Now().UTC()
	for rows.Next() {
		var (
			eventID         string
			snapshotID      int64
			proposedInt     int64
			predictedUtil   float64
			minReturnedBase sql.NullFloat64
			maxUnreturnedB  sql.NullFloat64
		)
		if err := rows.Scan(&eventID, &snapshotID, &proposedInt, &predictedUtil, &minReturnedBase, &maxUnreturnedB); err != nil {
			if m.logger != nil {
				m.logger.Debug("forest: adaptation observation scan failed", "err", err.Error())
			}
			continue
		}
		calErr := obsUtility - predictedUtil
		if calErr < 0 {
			calErr = -calErr
		}
		regret := 0.0
		if maxUnreturnedB.Valid && minReturnedBase.Valid && maxUnreturnedB.Float64 > minReturnedBase.Float64 {
			regret = 1.0
		}
		m.tuner.ObserveAndAdapt(ctx, AdaptationObservation{
			CalibrationError: calErr,
			RegretFrac:       regret,
			SampleCount:      1,
			EvidenceRefs:     []string{"forest_retrieval_event:" + eventID},
			ObservedAt:       now,
		}, proposedInt != 0)
	}
	if err := rows.Err(); err != nil && m.logger != nil {
		m.logger.Debug("forest: adaptation observation iterate failed", "err", err.Error())
	}
}
