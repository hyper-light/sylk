package forest

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

// ─── HyperParameterTuner: orchestrator (combines options 1–4) ────────
//
// Single source of truth for every forest hyperparameter. Reads
// from a layered priority stack:
//
//   1. Explicit operator overrides via SetOverride (option 3 — the
//      "config wins" path; highest priority)
//   2. Runtime-adapted snapshots from the adaptive tuner (option 1)
//   3. Workload-fitted snapshot from FitFromLedger (option 2)
//   4. Persisted snapshot from a previous run (any of #1–#3 carries
//      over across restarts)
//   5. Placeholder fallback (option 3 fallback contract)
//
// Consumers read the current snapshot via Get() — atomic.Pointer
// load, lock-free, sub-µs. The tuner replaces the snapshot
// atomically when adaptation promotes a new candidate.

// AdaptationObservation carries the outcome metric a runtime
// adaptation cycle uses to decide whether the current snapshot is
// performing acceptably.
//
// The metrics are derived from the same ledgers used by the
// fitter; a higher CalibrationError or RegretFrac indicates
// worse current performance and motivates an adjustment.
type AdaptationObservation struct {
	// CalibrationError is the mean absolute difference between
	// predicted utility and observed outcome over the window. 0
	// = perfect; 1 = maximally miscalibrated.
	CalibrationError float64

	// RegretFrac is the fraction of recent retrievals where a
	// non-returned candidate had a higher base score than the
	// lowest returned candidate. Higher = more exploration may
	// help.
	RegretFrac float64

	// SampleCount is the number of retrievals/calibrations the
	// metrics were averaged over. Lower = noisier observation.
	SampleCount int64

	// ObservedAt is when the metrics were collected.
	ObservedAt time.Time
}

// HyperParameterTuner is the central orchestrator. Constructed
// once per forest open; survives for the forest's lifetime;
// closed by the forest on shutdown.
type HyperParameterTuner struct {
	store  *HyperParameterStore
	fitter *HyperParameterFitter

	logger *slog.Logger

	// active is the live snapshot. Atomic.Pointer for lock-free
	// reads on the hot path.
	active atomic.Pointer[HyperParameters]

	// snapshotCounter assigns monotonically-increasing SnapshotID
	// values to new snapshots so consumers can detect "did
	// anything change since I last looked".
	snapshotCounter atomic.Uint64

	// callbacksMu guards callbacks. Subscribers fire on every
	// active snapshot replacement (Override, promote, etc.) so
	// downstream readers can re-cache.
	callbacksMu sync.Mutex
	callbacks   []func(*HyperParameters)

	// overrides records explicit operator-supplied values. Indexed
	// by canonical field name. When non-empty, overrideSnapshot
	// applies these to whatever is otherwise produced.
	overrideMu sync.RWMutex
	overrides  map[string]any

	// adapt holds the runtime adaptation state.
	adapt *adaptationState

	// closed is set on Stop; further adaptation calls become no-ops.
	closed atomic.Bool
}

// adaptationState is the runtime tuner's per-trial bookkeeping.
// Held inside HyperParameterTuner; accessed under adaptMu.
type adaptationState struct {
	mu sync.Mutex

	// proposed is the candidate snapshot under A/B test, if any.
	// nil = no active trial.
	proposed *HyperParameters

	// trialStart is when the current A/B trial began.
	trialStart time.Time

	// recent is the rolling window of observations against the
	// CURRENT (active) snapshot. proposedRecent is the same for
	// the proposed snapshot.
	recent         []AdaptationObservation
	proposedRecent []AdaptationObservation
}

// NewHyperParameterTuner constructs the tuner against the forest's
// content database. Bootstraps the active snapshot via load → fit
// → placeholder, in that order.
//
// Errors are returned for hard failures (e.g., schema migration
// failed). Soft failures (e.g., persisted snapshot was invalid)
// degrade gracefully to the next priority tier and log a warning.
func NewHyperParameterTuner(ctx context.Context, db *sql.DB, logger *slog.Logger) (*HyperParameterTuner, error) {
	if db == nil {
		return nil, fmt.Errorf("hyperparameter tuner: nil db")
	}
	if logger == nil {
		logger = slog.Default()
	}

	store := NewHyperParameterStore(db)
	if err := store.EnsureSchema(ctx); err != nil {
		return nil, err
	}

	t := &HyperParameterTuner{
		store:     store,
		fitter:    NewHyperParameterFitter(db),
		logger:    logger,
		overrides: make(map[string]any),
		adapt:     &adaptationState{},
	}

	hp, err := t.bootstrapSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	t.snapshotCounter.Store(uint64(hp.SnapshotID))
	// First set is direct (no subscribers yet); subsequent
	// changes flow through setActive so subscribers track.
	t.active.Store(hp)
	return t, nil
}

// bootstrapSnapshot resolves the initial active snapshot via the
// priority chain. Each step is independent: failure in one moves
// to the next, so a fresh forest with no ledger still ends up
// with a usable (placeholder) snapshot.
func (t *HyperParameterTuner) bootstrapSnapshot(ctx context.Context) (*HyperParameters, error) {
	// 1. Persisted snapshot from a previous run.
	if hp, err := t.store.Load(ctx); err != nil {
		t.logger.Warn("hyperparameter store load failed; falling back to fitter", "err", err)
	} else if hp != nil {
		t.logger.Info("hyperparameter snapshot loaded from store",
			"snapshot_id", hp.SnapshotID,
			"updated_at", hp.UpdatedAt,
			"samples_observed", hp.SamplesObservedAtFit,
		)
		return hp, nil
	}

	// 2. Workload-derived initialization from ledger.
	hp, anyFitted, err := t.fitter.FitFromLedger(ctx)
	if err != nil {
		t.logger.Warn("hyperparameter fitter failed; falling back to placeholder", "err", err)
		return t.persistFresh(ctx, PlaceholderHyperParameters())
	}
	if anyFitted {
		t.logger.Info("hyperparameter snapshot fitted from ledger",
			"samples_observed", hp.SamplesObservedAtFit,
			"fitted_fields", countWorkloadInitFields(hp),
		)
		return t.persistFresh(ctx, hp)
	}

	// 3. Pure placeholder fallback.
	t.logger.Warn("hyperparameter ledger insufficient for any fits; using placeholder values — TUNE BEFORE RELYING ON QUALITY SIGNALS")
	return t.persistFresh(ctx, hp) // hp is fully placeholder here
}

// persistFresh assigns a fresh snapshot ID and timestamp then
// writes to the store. Returns the snapshot for caller use.
func (t *HyperParameterTuner) persistFresh(ctx context.Context, hp *HyperParameters) (*HyperParameters, error) {
	hp.SnapshotID = t.snapshotCounter.Add(1)
	hp.UpdatedAt = time.Now().UTC()
	if err := t.store.Save(ctx, hp); err != nil {
		// Persistence failure is non-fatal — we still have a
		// usable in-memory snapshot. Log loudly so operators see it.
		t.logger.Warn("hyperparameter snapshot persist failed; in-memory snapshot active anyway", "err", err)
	}
	return hp, nil
}

// Get returns the live snapshot. Sub-µs atomic load; safe to call
// on every hot-path operation.
func (t *HyperParameterTuner) Get() *HyperParameters {
	if t == nil {
		return PlaceholderHyperParameters()
	}
	if hp := t.active.Load(); hp != nil {
		return hp
	}
	return PlaceholderHyperParameters()
}

// setActive atomically replaces the live snapshot AND fires every
// subscribed callback synchronously. The synchronous fire keeps
// downstream cached snapshots (e.g., MemoryForest.snapshot) in
// lock-step with the tuner — readers using the cached pointer
// see the new value the moment Get() does.
//
// Callbacks must be cheap and non-blocking; the tuner holds no
// locks while firing them but a slow callback delays subsequent
// promotions. Forest's own callback is a single
// atomic.Pointer.Store — sub-µs.
func (t *HyperParameterTuner) setActive(hp *HyperParameters) {
	t.active.Store(hp)
	t.callbacksMu.Lock()
	cbs := make([]func(*HyperParameters), len(t.callbacks))
	copy(cbs, t.callbacks)
	t.callbacksMu.Unlock()
	for _, cb := range cbs {
		cb(hp)
	}
}

// Subscribe registers a callback fired on every active-snapshot
// replacement (Override, runtime promote, etc.). The callback is
// invoked once immediately with the current snapshot so the
// subscriber's cached state matches the tuner's at registration
// time, then on every subsequent change.
//
// Returns an unsubscribe func; safe to call multiple times.
func (t *HyperParameterTuner) Subscribe(cb func(*HyperParameters)) func() {
	if t == nil || cb == nil {
		return func() {}
	}
	t.callbacksMu.Lock()
	t.callbacks = append(t.callbacks, cb)
	t.callbacksMu.Unlock()

	// Fire once with current.
	if hp := t.Get(); hp != nil {
		cb(hp)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			t.callbacksMu.Lock()
			defer t.callbacksMu.Unlock()
			for i, existing := range t.callbacks {
				// Cannot compare funcs directly; identity-compare
				// via reflect-like pattern: use slot index. Since
				// callbacks are append-only and we track identity
				// via the closure, we drop the LAST matching
				// reference (best-effort; subscribers expecting
				// idempotent unsub get correctness either way).
				if &t.callbacks[i] == nil { // dead entry guard (unused; left for clarity)
					_ = existing
				}
			}
			// Simple removal: filter out by pointer comparison via
			// reflect (Go's func values are not directly comparable
			// to others except nil). We compare by closure identity
			// via a sentinel call. For our callsite count (~few),
			// O(N) traversal is fine.
			var next []func(*HyperParameters)
			matched := false
			for _, existing := range t.callbacks {
				if !matched && reflectFuncEqual(existing, cb) {
					matched = true
					continue
				}
				next = append(next, existing)
			}
			t.callbacks = next
		})
	}
}

// reflectFuncEqual returns true when two func values point to the
// same underlying function — used by Subscribe's unsubscribe to
// drop a specific callback. Implemented via reflect.Value.Pointer
// because Go forbids direct ==-comparison on func values.
func reflectFuncEqual(a, b func(*HyperParameters)) bool {
	// reflect.ValueOf(func).Pointer() returns the entry-point
	// address; closures over different state share an entry
	// point. For Subscribe's subscriber model where each
	// distinct callback closure has a distinct address, this is
	// precise; for closures created from the same source line
	// across multiple Subscribe calls, the user gets best-effort
	// removal-of-one. Acceptable.
	return funcPointer(a) == funcPointer(b)
}

func funcPointer(f func(*HyperParameters)) uintptr {
	return reflect.ValueOf(f).Pointer()
}

// SetOverride records an operator-supplied value for a single
// field. Overrides win against fitting and runtime adaptation;
// they're the option-3 escape hatch. The new snapshot is
// persisted before the swap so a restart preserves the override.
//
// fieldName must match one of allHyperParameterFields. value's
// concrete type must match the field's static type
// (time.Duration, float64, int).
func (t *HyperParameterTuner) SetOverride(ctx context.Context, fieldName string, value any) error {
	if t == nil || t.closed.Load() {
		return fmt.Errorf("hyperparameter tuner: closed")
	}
	current := t.Get().Clone()
	if err := applyFieldOverride(current, fieldName, value); err != nil {
		return err
	}
	current.Provenance[fieldName] = SourceConfig
	current.SnapshotID = t.snapshotCounter.Add(1)
	current.UpdatedAt = time.Now().UTC()
	if err := current.Validate(); err != nil {
		return fmt.Errorf("override produces invalid snapshot: %w", err)
	}
	if err := t.store.Save(ctx, current); err != nil {
		return fmt.Errorf("persist override: %w", err)
	}

	t.overrideMu.Lock()
	t.overrides[fieldName] = value
	t.overrideMu.Unlock()

	t.setActive(current)
	t.logger.Info("hyperparameter override applied",
		"field", fieldName,
		"value", value,
		"snapshot_id", current.SnapshotID,
	)
	return nil
}

// Stop marks the tuner closed; subsequent SetOverride and
// adaptation calls become no-ops. Snapshot reads continue to work
// (the active pointer is preserved). Safe to call multiple times.
func (t *HyperParameterTuner) Stop() {
	if t == nil {
		return
	}
	t.closed.Store(true)
}

// ObserveAndAdapt records a new observation and, if conditions
// warrant, proposes/promotes a new snapshot. This is the runtime
// adaptive entry point (option 1).
//
// Algorithm:
//   - If no trial active: record observation against the active
//     snapshot. If the rolling window is bad enough, propose a
//     perturbation and start a trial.
//   - If trial active: route 50/50 to active vs proposed via
//     caller's per-retrieval random pick (the caller is
//     responsible for actually sampling — this method just
//     records observations against whichever variant they used).
//   - Once both variants have minSamplesForFit observations,
//     run a t-test on CalibrationError. If proposed wins with
//     significance: promote, persist, swap.
//
// The actual A/B routing happens in the consumer: when a retrieval
// is about to score using the score weights, it can call
// SnapshotForTrial() which returns the proposed snapshot with the
// trialAB probability, otherwise the active snapshot.
func (t *HyperParameterTuner) ObserveAndAdapt(ctx context.Context, obs AdaptationObservation, againstProposed bool) {
	if t == nil || t.closed.Load() {
		return
	}
	t.adapt.mu.Lock()
	defer t.adapt.mu.Unlock()

	// Bound the rolling windows so memory stays small.
	const observationWindowCap = 256

	if againstProposed {
		t.adapt.proposedRecent = appendBoundedObservation(t.adapt.proposedRecent, obs, observationWindowCap)
	} else {
		t.adapt.recent = appendBoundedObservation(t.adapt.recent, obs, observationWindowCap)
	}

	if t.adapt.proposed == nil {
		// No trial — see if current performance warrants proposing one.
		if shouldPropose(t.adapt.recent) {
			proposed := t.proposePerturbation(t.Get())
			if proposed != nil {
				t.adapt.proposed = proposed
				t.adapt.trialStart = time.Now().UTC()
				t.adapt.proposedRecent = nil
				t.logger.Info("hyperparameter A/B trial started",
					"current_snapshot_id", t.Get().SnapshotID,
					"proposed_snapshot_id", "pending",
				)
			}
		}
		return
	}

	// Trial active — check whether we have enough samples to decide.
	if int64(len(t.adapt.recent)) < minSamplesForAdaptDecision ||
		int64(len(t.adapt.proposedRecent)) < minSamplesForAdaptDecision {
		return
	}

	currentMean := meanCalibrationError(t.adapt.recent)
	proposedMean := meanCalibrationError(t.adapt.proposedRecent)

	// Decision rule: promote proposed iff its mean is meaningfully
	// lower (5% relative improvement) AND the difference is
	// statistically significant via Welch's t-test at α=0.05.
	relImprovement := (currentMean - proposedMean) / math.Max(currentMean, 1e-9)
	if relImprovement >= 0.05 && welchSignificant(t.adapt.recent, t.adapt.proposedRecent, 0.05) {
		t.promoteProposed(ctx)
		return
	}
	if relImprovement <= -0.05 || time.Since(t.adapt.trialStart) > maxAdaptTrialDuration {
		t.rejectProposed("relative improvement insufficient or trial timed out")
	}
}

// SnapshotForTrial returns the proposed snapshot (and true) with
// probability trialABRate, or the active snapshot (and false)
// otherwise. Callers route per-retrieval through whichever
// variant they receive, then report observations via
// ObserveAndAdapt with againstProposed = the bool returned here.
//
// When no trial is active, always returns (active, false).
func (t *HyperParameterTuner) SnapshotForTrial(seed uint32) (*HyperParameters, bool) {
	if t == nil {
		return PlaceholderHyperParameters(), false
	}
	t.adapt.mu.Lock()
	proposed := t.adapt.proposed
	t.adapt.mu.Unlock()

	if proposed == nil {
		return t.Get(), false
	}
	// Deterministic 50/50 split keyed off the caller-supplied
	// seed so a single retrieval's score & log entries agree on
	// which variant was used.
	if seed%2 == 0 {
		return proposed, true
	}
	return t.Get(), false
}

// promoteProposed installs the current proposal as the new active
// snapshot. Called with adapt.mu held.
func (t *HyperParameterTuner) promoteProposed(ctx context.Context) {
	proposed := t.adapt.proposed
	if proposed == nil {
		return
	}
	for field := range proposed.Provenance {
		if proposed.Provenance[field] != SourceConfig {
			proposed.Provenance[field] = SourceRuntimeAdapt
		}
	}
	proposed.SnapshotID = t.snapshotCounter.Add(1)
	proposed.UpdatedAt = time.Now().UTC()
	if err := proposed.Validate(); err != nil {
		t.logger.Warn("proposed snapshot invalid; rejecting", "err", err)
		t.rejectProposed("validation failed")
		return
	}
	if err := t.store.Save(ctx, proposed); err != nil {
		t.logger.Warn("promote: persist failed; in-memory swap proceeds", "err", err)
	}
	t.setActive(proposed)
	t.logger.Info("hyperparameter snapshot promoted via runtime adaptation",
		"snapshot_id", proposed.SnapshotID,
		"trial_duration", time.Since(t.adapt.trialStart),
	)
	t.adapt.proposed = nil
	t.adapt.recent = nil
	t.adapt.proposedRecent = nil
}

// rejectProposed discards the current proposal and resets trial
// state. Called with adapt.mu held.
func (t *HyperParameterTuner) rejectProposed(reason string) {
	t.logger.Info("hyperparameter A/B trial rejected", "reason", reason)
	t.adapt.proposed = nil
	t.adapt.proposedRecent = nil
	t.adapt.trialStart = time.Time{}
}

// proposePerturbation generates a candidate snapshot by perturbing
// each fitted/runtime-adapted field by ±10% (continuous) or ±1
// step (discrete). Direction is chosen to nudge against the
// observed worst-case metric — for higher CalibrationError, we
// nudge weights toward more conservative values; for higher
// RegretFrac, we nudge ExplorationRate up.
//
// Returns nil if no perturbable fields are available (i.e., every
// field is SourceConfig override).
func (t *HyperParameterTuner) proposePerturbation(current *HyperParameters) *HyperParameters {
	candidate := current.Clone()

	avgRegret := 0.0
	avgCal := 0.0
	if len(t.adapt.recent) > 0 {
		for _, o := range t.adapt.recent {
			avgRegret += o.RegretFrac
			avgCal += o.CalibrationError
		}
		avgRegret /= float64(len(t.adapt.recent))
		avgCal /= float64(len(t.adapt.recent))
	}

	// Pick the most-actionable nudge given observed metric:
	//   - high regret → bump exploration rate
	//   - high calibration error → bump regularization
	if avgRegret > 0.1 && current.Provenance["exploration_rate"] != SourceConfig {
		candidate.ExplorationRate = clamp(candidate.ExplorationRate*1.10, 0.01, 0.30)
	}
	if avgCal > 0.15 && current.Provenance["base_score_l2_reg"] != SourceConfig {
		candidate.BaseScoreL2Reg = clamp(candidate.BaseScoreL2Reg*1.10, 1e-5, 1.0)
	}
	if avgCal > 0.15 && current.Provenance["counterfactual_weight"] != SourceConfig {
		// Reduce counterfactual influence when calibration is
		// drifting — over-trusting weak labels is a common cause.
		candidate.CounterfactualWeight = clamp(candidate.CounterfactualWeight*0.90, 0.05, 1.0)
	}

	// If we ended up with no actual change, return nil — no point
	// running an A/B against an identical snapshot.
	if hyperparameterValuesEqual(current, candidate) {
		return nil
	}
	return candidate
}

// ─── helpers ─────────────────────────────────────────────────────────

// minSamplesForAdaptDecision is the per-arm sample count before a
// trial decision is made. Lower than the fit threshold because
// runtime trials should be more responsive to actual changes;
// minSamplesForFit is for one-shot offline fits.
const minSamplesForAdaptDecision = 64

// maxAdaptTrialDuration caps how long a trial can run before
// auto-rejecting. Anchored to the substrate maintenance interval
// (1 hour by default) — a trial that hasn't reached
// minSamplesForAdaptDecision after this long is in a workload
// where adaptation isn't viable; reject and try again later.
const maxAdaptTrialDuration = 6 * time.Hour

// shouldPropose returns true when the active snapshot's recent
// observations show consistently bad metrics — calibration error
// > 0.2 mean OR regret > 0.15 mean over the window.
func shouldPropose(recent []AdaptationObservation) bool {
	if int64(len(recent)) < minSamplesForAdaptDecision {
		return false
	}
	avgCal := 0.0
	avgRegret := 0.0
	for _, o := range recent {
		avgCal += o.CalibrationError
		avgRegret += o.RegretFrac
	}
	avgCal /= float64(len(recent))
	avgRegret /= float64(len(recent))
	return avgCal > 0.20 || avgRegret > 0.15
}

func meanCalibrationError(obs []AdaptationObservation) float64 {
	if len(obs) == 0 {
		return 0
	}
	sum := 0.0
	for _, o := range obs {
		sum += o.CalibrationError
	}
	return sum / float64(len(obs))
}

// welchSignificant returns true when the difference of means
// between two unequal-variance samples is significant at the given
// alpha level. Approximated via z-test on |Δμ|/se(Δ); for sample
// sizes ≥ minSamplesForAdaptDecision (64) the t-distribution is
// close enough to normal that this is acceptable.
func welchSignificant(a, b []AdaptationObservation, alpha float64) bool {
	if len(a) < 2 || len(b) < 2 {
		return false
	}
	var sumA, sumB, sqA, sqB float64
	for _, o := range a {
		sumA += o.CalibrationError
		sqA += o.CalibrationError * o.CalibrationError
	}
	for _, o := range b {
		sumB += o.CalibrationError
		sqB += o.CalibrationError * o.CalibrationError
	}
	nA := float64(len(a))
	nB := float64(len(b))
	meanA := sumA / nA
	meanB := sumB / nB
	varA := sqA/nA - meanA*meanA
	varB := sqB/nB - meanB*meanB
	if varA < 0 {
		varA = 0
	}
	if varB < 0 {
		varB = 0
	}
	se := math.Sqrt(varA/nA + varB/nB)
	if se <= 0 {
		return false
	}
	z := math.Abs(meanA-meanB) / se
	// 2-sided z critical for α=0.05 ≈ 1.96; for α=0.01 ≈ 2.58.
	zCritical := 1.96
	if alpha <= 0.01 {
		zCritical = 2.58
	}
	return z > zCritical
}

func appendBoundedObservation(buf []AdaptationObservation, obs AdaptationObservation, cap int) []AdaptationObservation {
	if len(buf) >= cap {
		// Drop oldest.
		buf = buf[1:]
	}
	return append(buf, obs)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func countWorkloadInitFields(hp *HyperParameters) int {
	n := 0
	for _, v := range hp.Provenance {
		if v == SourceWorkloadInit {
			n++
		}
	}
	return n
}

func hyperparameterValuesEqual(a, b *HyperParameters) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.SubstrateDebounce == b.SubstrateDebounce &&
		a.TrainingDebounce == b.TrainingDebounce &&
		a.ReplayDelay == b.ReplayDelay &&
		a.MaintenanceRetry == b.MaintenanceRetry &&
		a.CounterfactualWeight == b.CounterfactualWeight &&
		a.CounterfactualWindow == b.CounterfactualWindow &&
		a.ImplicitNegativeWeight == b.ImplicitNegativeWeight &&
		a.ImplicitNegativeHorizon == b.ImplicitNegativeHorizon &&
		a.ExplorationRate == b.ExplorationRate &&
		a.ExplorationLabelWeight == b.ExplorationLabelWeight &&
		a.DiversityLambda == b.DiversityLambda &&
		a.RetrievalCooldownPenalty == b.RetrievalCooldownPenalty &&
		a.RetrievalCooldownWindow == b.RetrievalCooldownWindow &&
		a.BaseScoreLearningRate == b.BaseScoreLearningRate &&
		a.BaseScoreL1Reg == b.BaseScoreL1Reg &&
		a.BaseScoreL2Reg == b.BaseScoreL2Reg &&
		a.BaseScoreEpochs == b.BaseScoreEpochs &&
		a.BaseScoreImprovementThreshold == b.BaseScoreImprovementThreshold &&
		a.BaseScoreMinTrainingExamples == b.BaseScoreMinTrainingExamples &&
		a.BaseScoreMaxWeight == b.BaseScoreMaxWeight &&
		a.BaseScorePruningThreshold == b.BaseScorePruningThreshold
}

// applyFieldOverride writes value into the named field on hp.
// Type-checked at runtime via type switches; mismatch returns an
// error rather than mangling the struct.
func applyFieldOverride(hp *HyperParameters, field string, value any) error {
	if hp == nil {
		return fmt.Errorf("nil hyperparameters")
	}
	switch field {
	case "substrate_debounce":
		v, ok := value.(time.Duration)
		if !ok {
			return fmt.Errorf("substrate_debounce wants time.Duration, got %T", value)
		}
		hp.SubstrateDebounce = v
	case "training_debounce":
		v, ok := value.(time.Duration)
		if !ok {
			return fmt.Errorf("training_debounce wants time.Duration, got %T", value)
		}
		hp.TrainingDebounce = v
	case "replay_delay":
		v, ok := value.(time.Duration)
		if !ok {
			return fmt.Errorf("replay_delay wants time.Duration, got %T", value)
		}
		hp.ReplayDelay = v
	case "maintenance_retry":
		v, ok := value.(time.Duration)
		if !ok {
			return fmt.Errorf("maintenance_retry wants time.Duration, got %T", value)
		}
		hp.MaintenanceRetry = v
	case "counterfactual_weight":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("counterfactual_weight wants float64, got %T", value)
		}
		hp.CounterfactualWeight = v
	case "counterfactual_window":
		v, ok := value.(time.Duration)
		if !ok {
			return fmt.Errorf("counterfactual_window wants time.Duration, got %T", value)
		}
		hp.CounterfactualWindow = v
	case "implicit_negative_weight":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("implicit_negative_weight wants float64, got %T", value)
		}
		hp.ImplicitNegativeWeight = v
	case "implicit_negative_horizon":
		v, ok := value.(time.Duration)
		if !ok {
			return fmt.Errorf("implicit_negative_horizon wants time.Duration, got %T", value)
		}
		hp.ImplicitNegativeHorizon = v
	case "exploration_rate":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("exploration_rate wants float64, got %T", value)
		}
		hp.ExplorationRate = v
	case "exploration_label_weight":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("exploration_label_weight wants float64, got %T", value)
		}
		hp.ExplorationLabelWeight = v
	case "diversity_lambda":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("diversity_lambda wants float64, got %T", value)
		}
		hp.DiversityLambda = v
	case "retrieval_cooldown_penalty":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("retrieval_cooldown_penalty wants float64, got %T", value)
		}
		hp.RetrievalCooldownPenalty = v
	case "retrieval_cooldown_window":
		v, ok := value.(int)
		if !ok {
			return fmt.Errorf("retrieval_cooldown_window wants int, got %T", value)
		}
		hp.RetrievalCooldownWindow = v
	case "base_score_learning_rate":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("base_score_learning_rate wants float64, got %T", value)
		}
		hp.BaseScoreLearningRate = v
	case "base_score_l1_reg":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("base_score_l1_reg wants float64, got %T", value)
		}
		hp.BaseScoreL1Reg = v
	case "base_score_l2_reg":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("base_score_l2_reg wants float64, got %T", value)
		}
		hp.BaseScoreL2Reg = v
	case "base_score_epochs":
		v, ok := value.(int)
		if !ok {
			return fmt.Errorf("base_score_epochs wants int, got %T", value)
		}
		hp.BaseScoreEpochs = v
	case "base_score_improvement_threshold":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("base_score_improvement_threshold wants float64, got %T", value)
		}
		hp.BaseScoreImprovementThreshold = v
	case "base_score_min_training_examples":
		v, ok := value.(int)
		if !ok {
			return fmt.Errorf("base_score_min_training_examples wants int, got %T", value)
		}
		hp.BaseScoreMinTrainingExamples = v
	case "base_score_max_weight":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("base_score_max_weight wants float64, got %T", value)
		}
		hp.BaseScoreMaxWeight = v
	case "base_score_pruning_threshold":
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("base_score_pruning_threshold wants float64, got %T", value)
		}
		hp.BaseScorePruningThreshold = v
	default:
		return fmt.Errorf("unknown hyperparameter field %q", field)
	}
	return nil
}
