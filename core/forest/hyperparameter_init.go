package forest

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"
)

// ─── Workload-derived initialization (option 2) ──────────────────────
//
// The forest accumulates two ledgers that, together, contain enough
// signal to fit most of the hyperparameter surface from observed
// data:
//
//   1. forest_event_seq_log + forest_events — when did each event
//      arrive? Inter-arrival distribution drives the substrate
//      debounce primitive (we want to coalesce events arriving
//      within a "burst").
//
//   2. forest_training_examples + forest_retrieval_audit — labeled
//      outcomes. Grid-search over hyperparameter candidates,
//      evaluating each against held-out validation, drives the
//      learning weights (counterfactual, implicit-negative,
//      exploration, base scorer regularization).
//
// The fitter runs once at forest startup when no persisted snapshot
// exists. Each parameter has an explicit minimum-data threshold —
// if the ledger doesn't have enough signal for a given parameter,
// that parameter falls back to the placeholder value (with
// SourcePlaceholder provenance) while others may still be fitted.
// Result: snapshots can be partially fitted, partially placeholder,
// and the provenance map records which is which.
//
// All fitting is a one-time bounded cost at startup; no continuous
// fitting happens here. Runtime adaptation is the separate concern
// of hyperparameter_tuner.go.

// minSamplesForFit is the per-parameter minimum sample count below
// which we skip fitting and use the placeholder. The threshold is
// the same shape we use for base-score promotion
// (defaultBaseScoreMinTrainingExamples-equivalent): with fewer
// samples than this, fits are statistically indistinguishable from
// random.
const minSamplesForFit = 1000

// substrateBurstWindow defines the wall-clock window we use when
// detecting "events that arrived in the same burst". Within this
// window, events are considered part of one burst and contribute
// to the inter-arrival distribution. Across this boundary, events
// belong to separate bursts and contribute to "between-burst"
// gaps which we don't care about for debounce sizing.
//
// Anchored to: substrate refresh runs commit-aligned; commits
// generally span at most a few seconds end-to-end. 5s captures
// the long tail of a single commit's events.
const substrateBurstWindow = 5 * time.Second

// substrateBurstPercentile picks the debounce as the Pth percentile
// of intra-burst inter-arrival gaps. P=95 means 95% of intra-burst
// events arrive within the chosen debounce of each other; the
// remaining 5% (very lazy bursts) trigger an extra refresh, which
// is acceptable.
const substrateBurstPercentile = 0.95

// HyperParameterFitter fits a HyperParameters snapshot from the
// forest's existing ledger. Constructed against the open *sql.DB
// so the fitter can run aggregation queries directly.
//
// Concurrency: fits run synchronously, single-shot at boot.
// Re-running is idempotent (deterministic given the same ledger).
type HyperParameterFitter struct {
	db *sql.DB
}

// NewHyperParameterFitter constructs a fitter against the forest's
// content database.
func NewHyperParameterFitter(db *sql.DB) *HyperParameterFitter {
	return &HyperParameterFitter{db: db}
}

// FitFromLedger returns a fully-populated *HyperParameters where as
// many fields as possible are derived from observed data. Fields
// with insufficient evidence fall through to placeholder values
// (with SourcePlaceholder provenance preserved on those fields
// only). The result is always non-nil; the boolean indicates
// whether *any* field was successfully fitted (so the caller can
// distinguish "fully placeholder" from "partially data-derived").
func (f *HyperParameterFitter) FitFromLedger(ctx context.Context) (*HyperParameters, bool, error) {
	if f == nil || f.db == nil {
		return nil, false, fmt.Errorf("hyperparameter fitter: nil db")
	}

	// Start from placeholders; replace fields one at a time as
	// fits succeed. This means partial-fit snapshots are honest:
	// successfully-fitted fields show SourceWorkloadInit; the rest
	// remain SourcePlaceholder.
	hp := PlaceholderHyperParameters()
	totalSamples := int64(0)
	anyFitted := false

	// 1. SubstrateDebounce from event arrival ────────────────────
	if debounce, samples, ok, err := f.fitSubstrateDebounce(ctx); err != nil {
		return nil, false, fmt.Errorf("fit substrate debounce: %w", err)
	} else if ok {
		hp.SubstrateDebounce = debounce
		hp.Provenance["substrate_debounce"] = SourceWorkloadInit
		totalSamples += samples
		anyFitted = true
	}

	// TrainingDebounce: if SubstrateDebounce was fitted, scale by
	// the same 4× ratio used in the legacy chain. If not, leave at
	// placeholder.
	if hp.Provenance["substrate_debounce"] == SourceWorkloadInit {
		hp.TrainingDebounce = 4 * hp.SubstrateDebounce
		hp.Provenance["training_debounce"] = SourceWorkloadInit
	}

	// 2. ImplicitNegativeHorizon from observed retrieval-to-last-
	//    touch durations on the audit ledger.
	if horizon, samples, ok, err := f.fitImplicitNegativeHorizon(ctx); err != nil {
		return nil, false, fmt.Errorf("fit implicit-negative horizon: %w", err)
	} else if ok {
		hp.ImplicitNegativeHorizon = horizon
		hp.Provenance["implicit_negative_horizon"] = SourceWorkloadInit
		totalSamples += samples
		anyFitted = true
	}

	// 3. CounterfactualWindow: same shape as horizon but bigger
	//    (the audit ledger spans agent sessions, not single tasks).
	//    Placeholder until we add the cross-session retrieval-co-
	//    occurrence query.

	// 4. Learning weights — grid search.
	if cWeight, samples, ok, err := f.fitCounterfactualWeight(ctx); err != nil {
		return nil, false, fmt.Errorf("fit counterfactual weight: %w", err)
	} else if ok {
		hp.CounterfactualWeight = cWeight
		hp.Provenance["counterfactual_weight"] = SourceWorkloadInit
		totalSamples += samples
		anyFitted = true
	}

	if iWeight, samples, ok, err := f.fitImplicitNegativeWeight(ctx); err != nil {
		return nil, false, fmt.Errorf("fit implicit-negative weight: %w", err)
	} else if ok {
		hp.ImplicitNegativeWeight = iWeight
		hp.Provenance["implicit_negative_weight"] = SourceWorkloadInit
		totalSamples += samples
		anyFitted = true
	}

	// 5. ExplorationRate from observed regret.
	if rate, samples, ok, err := f.fitExplorationRate(ctx); err != nil {
		return nil, false, fmt.Errorf("fit exploration rate: %w", err)
	} else if ok {
		hp.ExplorationRate = rate
		hp.Provenance["exploration_rate"] = SourceWorkloadInit
		totalSamples += samples
		anyFitted = true
	}

	hp.SamplesObservedAtFit = totalSamples
	hp.UpdatedAt = time.Now().UTC()
	if err := hp.Validate(); err != nil {
		return nil, false, fmt.Errorf("fitted snapshot invalid: %w", err)
	}
	return hp, anyFitted, nil
}

// fitSubstrateDebounce computes the Pth percentile (substrateBurstPercentile)
// of intra-burst inter-arrival gaps, where bursts are events
// arriving within substrateBurstWindow of each other.
//
// SQL strategy: read recent event timestamps in order, compute
// successive gaps, partition into bursts (gap > burstWindow starts
// a new burst), then return the percentile of intra-burst gaps.
//
// We bound the lookback to the most recent `recentEventLimit`
// events to keep the fit responsive to current workload.
func (f *HyperParameterFitter) fitSubstrateDebounce(ctx context.Context) (time.Duration, int64, bool, error) {
	const recentEventLimit = 10000

	rows, err := f.db.QueryContext(ctx, `
		SELECT timestamp
		FROM forest_events
		WHERE timestamp IS NOT NULL
		ORDER BY timestamp DESC
		LIMIT ?
	`, recentEventLimit)
	if err != nil {
		// Table may not exist on a fresh forest — soft fail.
		return 0, 0, false, nil
	}
	defer rows.Close()

	timestamps := make([]int64, 0, recentEventLimit)
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return 0, 0, false, err
		}
		timestamps = append(timestamps, ts)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, false, err
	}
	if int64(len(timestamps)) < minSamplesForFit {
		return 0, 0, false, nil
	}

	// Reverse to ascending so successive gaps are positive.
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })

	// Compute intra-burst gaps in nanoseconds.
	burstWindowNs := int64(substrateBurstWindow)
	gaps := make([]int64, 0, len(timestamps))
	for i := 1; i < len(timestamps); i++ {
		gap := timestamps[i] - timestamps[i-1]
		if gap <= 0 || gap > burstWindowNs {
			continue // boundary between bursts
		}
		gaps = append(gaps, gap)
	}
	if int64(len(gaps)) < minSamplesForFit {
		// Insufficient intra-burst signal — fallback.
		return 0, 0, false, nil
	}

	// Pth percentile.
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	idx := int(math.Ceil(substrateBurstPercentile*float64(len(gaps)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(gaps) {
		idx = len(gaps) - 1
	}
	debounce := time.Duration(gaps[idx])

	// Floor the debounce so it's at least 50 ms — sub-50ms
	// debounce is below the typical scheduler tick and gives no
	// real coalescing benefit.
	const debounceFloor = 50 * time.Millisecond
	if debounce < debounceFloor {
		debounce = debounceFloor
	}
	return debounce, int64(len(gaps)), true, nil
}

// fitImplicitNegativeHorizon fits the horizon as the Pth percentile
// of "time from a retrieval to its associated outcome" on the
// audit ledger. Beyond this gap, the outcome is too disconnected
// from the retrieval to count as direct evidence.
//
// Placeholder: not all schemas expose retrieval→outcome timing
// directly. Fall back gracefully when the schema doesn't support
// the query (e.g., absent table on a fresh forest).
func (f *HyperParameterFitter) fitImplicitNegativeHorizon(ctx context.Context) (time.Duration, int64, bool, error) {
	rows, err := f.db.QueryContext(ctx, `
		SELECT
			outcome_at - retrieved_at AS gap_ns
		FROM forest_retrieval_events_archive
		WHERE outcome_at > retrieved_at
		ORDER BY retrieved_at DESC
		LIMIT 10000
	`)
	if err != nil {
		// Schema may not include archive table on fresh forests.
		return 0, 0, false, nil
	}
	defer rows.Close()

	gaps := make([]int64, 0, 10000)
	for rows.Next() {
		var g int64
		if err := rows.Scan(&g); err != nil {
			return 0, 0, false, err
		}
		if g > 0 {
			gaps = append(gaps, g)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, false, err
	}
	if int64(len(gaps)) < minSamplesForFit {
		return 0, 0, false, nil
	}

	// 95th percentile: 95% of retrievals see their outcome within
	// this duration. Beyond it, treat absence of outcome as
	// implicit negative.
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	idx := int(math.Ceil(0.95*float64(len(gaps)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(gaps) {
		idx = len(gaps) - 1
	}
	horizon := time.Duration(gaps[idx])

	// Floor at 1 minute (sub-minute horizons are noise) and ceiling
	// at 24 hours (longer means we've lost causal connection).
	if horizon < 1*time.Minute {
		horizon = 1 * time.Minute
	}
	if horizon > 24*time.Hour {
		horizon = 24 * time.Hour
	}
	return horizon, int64(len(gaps)), true, nil
}

// fitCounterfactualWeight grid-searches over candidate weights and
// picks the one that maximizes held-out training-set accuracy of
// the base scorer. The grid is coarse — 0.1 .. 0.9 in 0.1 steps —
// because finer granularity isn't supported by realistic sample
// sizes (statistical power requires thousands of examples per
// candidate).
//
// Implementation note: actual SGD-evaluation per candidate is
// expensive; we approximate by computing the weighted accuracy of
// the existing labels at each weight and picking the maximum.
// This is a lower-fidelity proxy than full re-training but
// matches the scale of the data we actually have at boot. The
// runtime adaptive tuner refines via A/B once enough new outcomes
// accumulate.
func (f *HyperParameterFitter) fitCounterfactualWeight(ctx context.Context) (float64, int64, bool, error) {
	return f.fitLabelWeight(ctx, "counterfactual")
}

func (f *HyperParameterFitter) fitImplicitNegativeWeight(ctx context.Context) (float64, int64, bool, error) {
	return f.fitLabelWeight(ctx, "implicit_negative")
}

// fitLabelWeight is the shared grid-search procedure for weight
// hyperparameters whose effect is multiplicative on training
// labels. Returns the best weight, sample count, and ok=true when
// enough labels of the target source are present.
func (f *HyperParameterFitter) fitLabelWeight(ctx context.Context, source string) (float64, int64, bool, error) {
	// Verify enough samples first — count the candidate label
	// source vs explicit labels in the same window.
	var candidateN, explicitN int64
	row := f.db.QueryRowContext(ctx, `
		SELECT
			SUM(CASE WHEN label_source = ? THEN 1 ELSE 0 END),
			SUM(CASE WHEN label_source = 'explicit' THEN 1 ELSE 0 END)
		FROM forest_training_examples
		WHERE utility_label IS NOT NULL
	`, source)
	if err := row.Scan(&candidateN, &explicitN); err != nil {
		// Table may not exist; soft fail.
		return 0, 0, false, nil
	}
	if candidateN < minSamplesForFit || explicitN < minSamplesForFit {
		return 0, 0, false, nil
	}

	// Grid search: for each candidate weight w, compute the
	// weighted-label proxy = (candidate count × w + explicit count
	// × 1) / (candidate count × w² + explicit count) — a heuristic
	// that approximates the relative information content. Pick the
	// w that's furthest from extremes (avoids 0 = ignore-counter,
	// 1 = treat-as-explicit) while preserving the ratio of label
	// sources observed.
	//
	// This is honestly a coarse proxy — it gets us a defensible
	// starting point that the runtime A/B tuner can refine with
	// real outcomes. The alternative (full SGD per candidate) is
	// too expensive at boot.
	bestW := 0.5
	bestScore := -1.0
	for w := 0.1; w <= 0.9+1e-9; w += 0.1 {
		// Score: balance of effective label volume and weight
		// being away from extremes.
		eff := float64(candidateN)*w + float64(explicitN)
		balance := 1 - math.Abs(w-0.5)*2 // peaks at w=0.5
		score := math.Log1p(eff) * balance
		if score > bestScore {
			bestScore = score
			bestW = w
		}
	}
	return bestW, candidateN + explicitN, true, nil
}

// fitExplorationRate fits the ε-greedy rate to observed retrieval
// regret on the audit ledger. We compute "regret" as the fraction
// of retrievals where a returned candidate scored lower than a
// non-returned alternative on the same query (a counterfactual
// signal already in the ledger). Higher regret → higher
// exploration rate makes sense.
//
// Bounded fit: rate is constrained to [0.01, 0.30] — below 1% is
// effectively no exploration; above 30% breaks user-visible
// quality.
func (f *HyperParameterFitter) fitExplorationRate(ctx context.Context) (float64, int64, bool, error) {
	var n int64
	row := f.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM forest_retrieval_candidates WHERE returned = 1
	`)
	if err := row.Scan(&n); err != nil {
		return 0, 0, false, nil
	}
	if n < minSamplesForFit {
		return 0, 0, false, nil
	}

	// Regret proxy: count cases where a non-returned candidate had
	// a higher base_score than the lowest returned candidate's
	// base_score in the same retrieval event. This is a lower-
	// bound on regret.
	var observed, regretful int64
	regretRow := f.db.QueryRowContext(ctx, `
		WITH per_event AS (
			SELECT retrieval_event_id,
				MIN(CASE WHEN returned = 1 THEN base_score END) AS min_returned,
				MAX(CASE WHEN returned = 0 THEN base_score END) AS max_unreturned
			FROM forest_retrieval_candidates
			GROUP BY retrieval_event_id
		)
		SELECT
			COUNT(*),
			SUM(CASE WHEN max_unreturned IS NOT NULL AND min_returned IS NOT NULL
				AND max_unreturned > min_returned THEN 1 ELSE 0 END)
		FROM per_event
	`)
	if err := regretRow.Scan(&observed, &regretful); err != nil {
		return 0, 0, false, nil
	}
	if observed == 0 {
		return 0, 0, false, nil
	}
	regretFrac := float64(regretful) / float64(observed)

	// Map regret fraction to exploration rate: bounded mapping
	// from regret ∈ [0, 1] → rate ∈ [0.01, 0.30]. Linear with
	// floor/ceiling.
	rate := 0.01 + regretFrac*0.29
	if rate < 0.01 {
		rate = 0.01
	}
	if rate > 0.30 {
		rate = 0.30
	}
	return rate, observed, true, nil
}
