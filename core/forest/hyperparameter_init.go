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
//
// logger receives one info-level event per grid-search candidate
// (validation MAE) plus one info-level event per fitted field
// summarizing the choice. Pass nil to disable.
func (f *HyperParameterFitter) FitFromLedger(ctx context.Context, logger fitLogger) (*HyperParameters, bool, error) {
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

	// 4. Learning weights — grid search with held-out validation.
	if cWeight, samples, ok, err := f.fitCounterfactualWeight(ctx, logger); err != nil {
		return nil, false, fmt.Errorf("fit counterfactual weight: %w", err)
	} else if ok {
		hp.CounterfactualWeight = cWeight
		hp.Provenance["counterfactual_weight"] = SourceWorkloadInit
		totalSamples += samples
		anyFitted = true
	}

	if iWeight, samples, ok, err := f.fitImplicitNegativeWeight(ctx, logger); err != nil {
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
// picks the one that minimizes held-out validation MAE of the base
// scorer trained with that weight applied to counterfactual rows.
// The grid is coarse — 0.1 .. 0.9 in 0.1 steps — because finer
// granularity isn't supported by realistic sample sizes
// (statistical power requires thousands of examples per candidate).
//
// Each candidate runs an SGD training pass with placeholder
// hyperparameters (learning rate, L1/L2 reg, epochs from
// PlaceholderHyperParameters) over the training split, then scores
// against the held-out validation split using mean absolute error
// of the predicted vs observed utility. Validation labels are
// explicit-only — counterfactual / implicit-negative rows are
// noisy estimates and the whole point of validation is to score
// against ground truth.
func (f *HyperParameterFitter) fitCounterfactualWeight(ctx context.Context, logger fitLogger) (float64, int64, bool, error) {
	return f.fitLabelWeightByValidation(ctx, "counterfactual", logger)
}

func (f *HyperParameterFitter) fitImplicitNegativeWeight(ctx context.Context, logger fitLogger) (float64, int64, bool, error) {
	return f.fitLabelWeightByValidation(ctx, "implicit_negative", logger)
}

// fitLogger is the minimal logger interface the fitter needs. Lets
// callers pass the forest's *slog.Logger or anything compatible
// without coupling the fitter to slog directly.
type fitLogger interface {
	Info(msg string, args ...any)
}

type labeledTrainingRow struct {
	example     baseScoreTrainingExample
	labelSource string
}

// fitLabelWeightByValidation runs a real grid search: for each
// candidate weight w, it trains a base-score SGD model over the 90%
// training split with rows of label_source=`source` weighted at w
// (explicit at 1.0; other sources at their stored weight), then
// evaluates validation MAE on the 10% held-out split (explicit
// labels only). Returns the w with lowest validation MAE.
//
// Validation set must contain at least valFloor explicit rows; if
// not, we don't have enough ground truth to discriminate weights
// and the function returns ok=false (caller falls back to placeholder).
//
// Logs each candidate's validation MAE at info so operators can
// see why a weight was picked and whether the spread between
// candidates is meaningful.
func (f *HyperParameterFitter) fitLabelWeightByValidation(ctx context.Context, source string, logger fitLogger) (float64, int64, bool, error) {
	// Pull every labeled row in one query (capped at fitTrainingPullCap
	// to keep boot cost bounded).
	const fitTrainingPullCap = 50000
	const valFloor = 200          // need ≥ valFloor explicit rows for stable validation MAE
	const valFraction = 0.1       // 90/10 train/val split
	const splitSeed = uint64(1) // deterministic split per fit

	rows, err := f.db.QueryContext(ctx, `
		SELECT features, utility_label, label_weight, label_source
		FROM   forest_training_examples
		WHERE  utility_label IS NOT NULL
		  AND  label_source IN (?, ?, ?)
		ORDER BY updated_at DESC
		LIMIT  ?
	`, string(LabelSourceExplicit), string(LabelSourceCounterfactual), string(LabelSourceImplicitNegative), fitTrainingPullCap)
	if err != nil {
		return 0, 0, false, nil
	}
	defer rows.Close()

	all := make([]labeledTrainingRow, 0, fitTrainingPullCap)
	candidateN := int64(0)
	explicitN := int64(0)
	for rows.Next() {
		var (
			featuresBlob []byte
			utility      sql.NullFloat64
			weight       sql.NullFloat64
			labelSrc     string
		)
		if err := rows.Scan(&featuresBlob, &utility, &weight, &labelSrc); err != nil {
			return 0, 0, false, fmt.Errorf("scan training example: %w", err)
		}
		if !utility.Valid {
			continue
		}
		components, ok := decodeFirstNFloat32sToFloat64(featuresBlob, BaseScoreComponentCount)
		if !ok {
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
		all = append(all, labeledTrainingRow{
			example: baseScoreTrainingExample{
				components: arr,
				label:      clamp01(utility.Float64),
				weight:     w,
			},
			labelSource: labelSrc,
		})
		switch labelSrc {
		case string(LabelSourceExplicit):
			explicitN++
		case source:
			candidateN++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, false, fmt.Errorf("iterate training examples: %w", err)
	}
	if candidateN < minSamplesForFit || explicitN < minSamplesForFit {
		return 0, 0, false, nil
	}

	// Deterministic 90/10 split. We split the explicit subset
	// separately so the validation set is guaranteed explicit-only.
	trainRows, valRows := splitForFitValidation(all, valFraction, splitSeed)
	valExplicit := filterExplicit(valRows)
	if int64(len(valExplicit)) < valFloor {
		return 0, 0, false, nil
	}

	placeholder := PlaceholderHyperParameters()
	bestW := 0.5
	bestVal := math.Inf(1)
	for w := 0.1; w <= 0.9+1e-9; w += 0.1 {
		examples := buildWeightedExamples(trainRows, source, w)
		initWeights, initBias := initialTrainingWeights(nil)
		model := trainBaseScoreSGD(
			examples,
			initWeights, initBias,
			placeholder.BaseScoreEpochs,
			placeholder.BaseScoreLearningRate,
			placeholder.BaseScoreL1Reg,
			placeholder.BaseScoreL2Reg,
			placeholder.BaseScoreMaxWeight,
		)
		valMAE := validationMAE(valExplicit, model.weights, model.bias)
		if logger != nil {
			logger.Info("hyperparameter grid candidate evaluated",
				"source", source,
				"weight", fmt.Sprintf("%.2f", w),
				"val_mae", valMAE,
				"train_n", len(examples),
				"val_n", len(valExplicit),
			)
		}
		if valMAE < bestVal {
			bestVal = valMAE
			bestW = w
		}
	}
	if logger != nil {
		logger.Info("hyperparameter grid winner",
			"source", source,
			"weight", fmt.Sprintf("%.2f", bestW),
			"val_mae", bestVal,
			"train_n", len(trainRows),
			"val_n", len(valExplicit),
		)
	}
	return bestW, candidateN + explicitN, true, nil
}

// splitForFitValidation partitions rows deterministically into
// train/val using a hash of the row index seeded with splitSeed.
// Validation fraction is approximate (rows whose hashed index falls
// below the threshold land in val).
func splitForFitValidation(rows []labeledTrainingRow, valFraction float64, seed uint64) (train, val []labeledTrainingRow) {
	if valFraction <= 0 || valFraction >= 1 {
		return rows, nil
	}
	threshold := uint64(valFraction * float64(math.MaxUint64))
	train = make([]labeledTrainingRow, 0, len(rows))
	val = make([]labeledTrainingRow, 0, int(float64(len(rows))*valFraction)+1)
	for i, r := range rows {
		// Splitmix64 — fast, deterministic given (seed, index).
		x := seed + uint64(i)*0x9E3779B97F4A7C15
		x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
		x = (x ^ (x >> 27)) * 0x94D049BB133111EB
		x = x ^ (x >> 31)
		if x < threshold {
			val = append(val, r)
		} else {
			train = append(train, r)
		}
	}
	return train, val
}

// filterExplicit returns only rows whose label_source = explicit.
func filterExplicit(rows []labeledTrainingRow) []baseScoreTrainingExample {
	out := make([]baseScoreTrainingExample, 0, len(rows))
	for _, r := range rows {
		if r.labelSource == string(LabelSourceExplicit) {
			out = append(out, r.example)
		}
	}
	return out
}

// buildWeightedExamples copies rows into a baseScoreTrainingExample
// slice, overriding the per-row weight to candidateWeight when the
// row's label_source matches `source`. Other rows keep the weight
// stored on the row (typically 1.0 for explicit, the configured
// implicit_negative_weight or counterfactual_weight for the
// observation-time write). This is what the SGD trainer consumes.
func buildWeightedExamples(rows []labeledTrainingRow, source string, candidateWeight float64) []baseScoreTrainingExample {
	out := make([]baseScoreTrainingExample, len(rows))
	for i, r := range rows {
		ex := r.example
		if r.labelSource == source {
			ex.weight = candidateWeight
		}
		out[i] = ex
	}
	return out
}

// validationMAE returns the mean absolute error between predicted
// utility (sigmoid of the linear combination) and ground-truth
// label across the validation set. Bounded to [0, 1] given that
// both pred and label are.
func validationMAE(examples []baseScoreTrainingExample, weights [BaseScoreComponentCount]float64, bias float64) float64 {
	if len(examples) == 0 {
		return math.Inf(1)
	}
	var sum float64
	for k := range examples {
		ex := &examples[k]
		pred := baseScoreSigmoid(dotComponents(weights, ex.components) + bias)
		d := pred - ex.label
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return sum / float64(len(examples))
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
