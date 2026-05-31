package forest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #9 Phase B — Calibration tracking
// ────────────────────────────────────────────────────────────────────
//
// Closes the loop between predictions and outcomes. The forest
// already records every per-candidate prediction (predicted_utility
// in forest_retrieval_candidates) and every outcome (forest_events
// rows of EventTypeOutcomeRecorded). Calibration joins them within
// an attribution window and reports:
//
//   - Reliability buckets (predicted vs observed)
//   - ECE (expected calibration error)
//   - MCE (max calibration error)
//   - Brier score (mean squared error of probabilities)
//   - AUC (ranking quality / discrimination)
//
// Plus retrieval-quality metrics that operate over RANKED results:
//   - precision@K
//   - MRR (mean reciprocal rank)
//   - NDCG@5 (normalized discounted cumulative gain)
//
// Every metric is bounds-checked on the empty case (returns 0
// rather than panicking on division-by-zero). Read-only paths; no
// goroutines spawned.

const (
	// defaultCalibrationBuckets bins predictions into 10 equal-width
	// reliability buckets [0, 0.1), [0.1, 0.2), ..., [0.9, 1.0]. 10
	// is the canonical choice for ECE reporting.
	defaultCalibrationBuckets = 10

	// defaultCalibrationWindow is the lookback used by the per-
	// advisory ModelCalibration cache when no explicit window is
	// supplied. 7d gives stable metrics at typical retrieval volumes
	// without including stale-data bias.
	defaultCalibrationWindow = 7 * 24 * time.Hour

	// defaultRetrievalQualityKs is the set of K values reported by
	// RetrievalQualitySince's PrecisionAtK map.
	defaultRetrievalQualityK1 = 1
	defaultRetrievalQualityK3 = 3
	defaultRetrievalQualityK5 = 5
)

// calibrationOutcomeWindowSeconds returns the same attribution
// window substrate stats use, expressed in seconds for the SQL
// timestamp arithmetic. Single source of truth: the
// outcomeAttributionWindow constant.
func calibrationOutcomeWindowSeconds() int64 {
	return int64(outcomeAttributionWindow.Seconds())
}

// CalibrationSnapshot captures the per-model calibration profile
// over a window. Read by operators + by the ModelCalibration cache
// (Phase B.4) to annotate outgoing packets.
type CalibrationSnapshot struct {
	Model        string              `json:"model"` // "booster" | "base_scorer"
	ModelVersion int64               `json:"model_version,omitempty"`
	WindowStart  time.Time           `json:"window_start"`
	WindowEnd    time.Time           `json:"window_end"`
	SampleCount  int64               `json:"sample_count"`
	Buckets      []ReliabilityBucket `json:"buckets"`
	ECE          float64             `json:"ece"`
	MCE          float64             `json:"mce"`
	Brier        float64             `json:"brier"`
	AUC          float64             `json:"auc"`
}

// ReliabilityBucket is one bin of predictions. PredictedMean is the
// average prediction in the bin; ObservedRate is the fraction with
// status=succeeded. Gap = |predicted - observed|. The ECE rolls
// these gaps up weighted by sample count.
type ReliabilityBucket struct {
	LowerBound    float64 `json:"lower_bound"`
	UpperBound    float64 `json:"upper_bound"`
	SampleCount   int64   `json:"sample_count"`
	PredictedMean float64 `json:"predicted_mean"`
	ObservedRate  float64 `json:"observed_rate"`
	Gap           float64 `json:"gap"`
}

// RetrievalQualityStats reports end-to-end retrieval quality. Joins
// per-retrieval candidate ranks with outcome events to derive
// precision@K + MRR + NDCG.
type RetrievalQualityStats struct {
	WindowStart     time.Time       `json:"window_start"`
	WindowEnd       time.Time       `json:"window_end"`
	RetrievalCount  int64           `json:"retrieval_count"`
	PrecisionAtK    map[int]float64 `json:"precision_at_k"`
	MRR             float64         `json:"mrr"`
	NDCGAt5         float64         `json:"ndcg_at_5"`
	OutcomeCoverage float64         `json:"outcome_coverage"`
}

// calibrationSample is one (prediction, outcome) pair pulled from
// the audit + outcome join. label = 1 if outcome.status=succeeded,
// else 0. Both fields bounded; the binner ignores any sample
// outside [0,1].
type calibrationSample struct {
	prediction float64
	label      float64
}

// BoosterCalibrationSince computes the booster's calibration over
// the window [since, now]. Pulls (predicted_utility, outcome) pairs
// for every returned candidate whose retrieval was followed by an
// outcome event on the branch within the attribution window.
func (m *MemoryForest) BoosterCalibrationSince(ctx context.Context, since time.Time) (CalibrationSnapshot, error) {
	if m == nil {
		return CalibrationSnapshot{}, nil
	}
	samples, err := m.loadBoosterCalibrationSamples(ctx, since)
	if err != nil {
		return CalibrationSnapshot{}, err
	}
	return buildCalibrationSnapshot("booster", 0, since, samples), nil
}

// BaseScoreCalibrationSince computes the base scorer's calibration.
// Pairs forest_retrieval_candidates.base_score with outcome status.
// base_score isn't bounded to [0,1] — it's a sum of weight·component
// products plus bias — so we apply a sigmoid before binning so the
// reliability diagram lives on the same scale as the booster's.
func (m *MemoryForest) BaseScoreCalibrationSince(ctx context.Context, since time.Time) (CalibrationSnapshot, error) {
	if m == nil {
		return CalibrationSnapshot{}, nil
	}
	champion, _, err := m.baseScoreStore.LoadActive(ctx)
	if err != nil {
		return CalibrationSnapshot{}, err
	}
	samples, err := m.loadBaseScoreCalibrationSamples(ctx, since)
	if err != nil {
		return CalibrationSnapshot{}, err
	}
	var version int64
	if champion != nil {
		version = champion.Version
	}
	return buildCalibrationSnapshot("base_scorer", version, since, samples), nil
}

// loadBoosterCalibrationSamples pulls (predicted_utility, label)
// pairs from the audit + outcome join. Only RETURNED candidates are
// counted — non-returned candidates were never seen by the agent,
// so their predictions can't be validated.
//
// Outcome status is parsed in Go (json.Unmarshal of payload) so the
// query doesn't depend on SQLite's json1 extension.
func (m *MemoryForest) loadBoosterCalibrationSamples(ctx context.Context, since time.Time) ([]calibrationSample, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT rc.predicted_utility, p.payload
		FROM   forest_retrieval_candidates rc
		JOIN   forest_retrieval_events re ON re.id = rc.retrieval_event_id
		JOIN   forest_ledger e
		    ON e.session_id    = rc.session_id
		   AND e.subject_type  = 'branch'
		   AND e.subject_id    = rc.branch_id
		   AND e.source_kind   = ?
		JOIN   forest_ledger_payloads p ON p.ledger_id = e.id
		WHERE  e.event_kind = 'outcome_recorded'
		  AND  rc.returned    = 1
		  AND  re.requested_at >= ?
		  AND  e.occurred_at BETWEEN re.requested_at AND (re.requested_at + ?)
	`, string(LedgerSourceForestEvent), since.Unix(), calibrationOutcomeWindowSeconds())
	if err != nil {
		return nil, fmt.Errorf("query booster calibration samples: %w", err)
	}
	defer rows.Close()
	return collectCalibrationSamples(rows)
}

// loadBaseScoreCalibrationSamples pulls (base_score, label) pairs.
// base_score is then sigmoid-transformed before binning.
func (m *MemoryForest) loadBaseScoreCalibrationSamples(ctx context.Context, since time.Time) ([]calibrationSample, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT rc.base_score, p.payload
		FROM   forest_retrieval_candidates rc
		JOIN   forest_retrieval_events re ON re.id = rc.retrieval_event_id
		JOIN   forest_ledger e
		    ON e.session_id    = rc.session_id
		   AND e.subject_type  = 'branch'
		   AND e.subject_id    = rc.branch_id
		   AND e.source_kind   = ?
		JOIN   forest_ledger_payloads p ON p.ledger_id = e.id
		WHERE  e.event_kind = 'outcome_recorded'
		  AND  rc.returned    = 1
		  AND  re.requested_at >= ?
		  AND  e.occurred_at BETWEEN re.requested_at AND (re.requested_at + ?)
	`, string(LedgerSourceForestEvent), since.Unix(), calibrationOutcomeWindowSeconds())
	if err != nil {
		return nil, fmt.Errorf("query base-score calibration samples: %w", err)
	}
	defer rows.Close()
	samples, err := collectCalibrationSamples(rows)
	if err != nil {
		return nil, err
	}
	// Map raw base scores into [0,1] via sigmoid for binning.
	for i := range samples {
		samples[i].prediction = baseScoreSigmoid(samples[i].prediction)
	}
	return samples, nil
}

// collectCalibrationSamples is shared by the per-model loaders.
// Iterates rows of (prediction, payload) pairs, parses each
// payload's status field, builds a sample slice. Predictions outside
// [0,1] are bounded; payloads that don't deserialize (or carry
// neither succeeded nor failed) are skipped.
func collectCalibrationSamples(rows *sql.Rows) ([]calibrationSample, error) {
	var out []calibrationSample
	for rows.Next() {
		var (
			prediction float64
			payload    sql.NullString
		)
		if err := rows.Scan(&prediction, &payload); err != nil {
			return nil, fmt.Errorf("scan calibration sample: %w", err)
		}
		status := outcomeStatusFromPayloadJSON(payload.String)
		switch status {
		case OutcomeStatusSucceeded:
			out = append(out, calibrationSample{
				prediction: clamp01(prediction),
				label:      1,
			})
		case OutcomeStatusFailed:
			out = append(out, calibrationSample{
				prediction: clamp01(prediction),
				label:      0,
			})
		default:
			// Mixed / Ignored / unknown — not a binary outcome,
			// can't fit the calibration framework. Skip.
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate calibration samples: %w", err)
	}
	return out, nil
}

// buildCalibrationSnapshot does the actual metric math: bins
// predictions, computes ECE/MCE/Brier/AUC, packages into a snapshot.
// Pure function over the sample slice — no DB.
func buildCalibrationSnapshot(model string, version int64, since time.Time, samples []calibrationSample) CalibrationSnapshot {
	now := time.Now().UTC()
	out := CalibrationSnapshot{
		Model:        model,
		ModelVersion: version,
		WindowStart:  since,
		WindowEnd:    now,
		SampleCount:  int64(len(samples)),
		Buckets:      computeReliabilityBuckets(samples, defaultCalibrationBuckets),
	}
	out.ECE = expectedCalibrationError(out.Buckets, len(samples))
	out.MCE = maximumCalibrationError(out.Buckets)
	out.Brier = brierScore(samples)
	out.AUC = areaUnderROC(samples)
	return out
}

// computeReliabilityBuckets bins predictions into `n` equal-width
// bins covering [0,1]. Bin i covers [i/n, (i+1)/n) with bin n-1
// inclusive at the upper end so prediction=1.0 lands in the last
// bin. Returns one ReliabilityBucket per bin (all bins are returned
// even when empty so the diagram has a consistent shape).
func computeReliabilityBuckets(samples []calibrationSample, n int) []ReliabilityBucket {
	if n <= 0 {
		n = defaultCalibrationBuckets
	}
	buckets := make([]ReliabilityBucket, n)
	predictionSums := make([]float64, n)
	labelSums := make([]float64, n)
	for i := 0; i < n; i++ {
		buckets[i].LowerBound = float64(i) / float64(n)
		buckets[i].UpperBound = float64(i+1) / float64(n)
	}
	for _, s := range samples {
		idx := bucketIndex(s.prediction, n)
		buckets[idx].SampleCount++
		predictionSums[idx] += s.prediction
		labelSums[idx] += s.label
	}
	for i := 0; i < n; i++ {
		count := buckets[i].SampleCount
		if count == 0 {
			continue
		}
		buckets[i].PredictedMean = predictionSums[i] / float64(count)
		buckets[i].ObservedRate = labelSums[i] / float64(count)
		gap := buckets[i].PredictedMean - buckets[i].ObservedRate
		if gap < 0 {
			gap = -gap
		}
		buckets[i].Gap = gap
	}
	return buckets
}

// bucketIndex maps a prediction in [0,1] to its bin index. Out-of-
// range inputs are clamped: <0 → 0, ≥1 → n-1. n is the bucket count.
func bucketIndex(prediction float64, n int) int {
	if n <= 0 {
		return 0
	}
	if prediction < 0 {
		return 0
	}
	if prediction >= 1 {
		return n - 1
	}
	idx := int(prediction * float64(n))
	if idx >= n {
		idx = n - 1
	}
	if idx < 0 {
		idx = 0
	}
	return idx
}

// expectedCalibrationError = Σ_i (count_i / total) × gap_i.
// Returns 0 on empty input.
func expectedCalibrationError(buckets []ReliabilityBucket, totalSamples int) float64 {
	if totalSamples <= 0 {
		return 0
	}
	var ece float64
	total := float64(totalSamples)
	for _, b := range buckets {
		if b.SampleCount == 0 {
			continue
		}
		weight := float64(b.SampleCount) / total
		ece += weight * b.Gap
	}
	return ece
}

// maximumCalibrationError = max_i gap_i. Returns 0 on empty input.
func maximumCalibrationError(buckets []ReliabilityBucket) float64 {
	var mce float64
	for _, b := range buckets {
		if b.SampleCount == 0 {
			continue
		}
		if b.Gap > mce {
			mce = b.Gap
		}
	}
	return mce
}

// brierScore = mean((prediction - label)²). Range [0,1] for
// well-formed inputs (predictions in [0,1], labels in {0,1}).
// Lower = better calibrated.
func brierScore(samples []calibrationSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		diff := s.prediction - s.label
		sum += diff * diff
	}
	return sum / float64(len(samples))
}

// areaUnderROC computes AUC via the Mann-Whitney rank statistic.
// Returns 0.5 (random discrimination) when only one class is
// present in the samples; returns 0 on empty input. AUC=1 means
// the model perfectly ranks positives above negatives.
//
// Implementation: sort samples by prediction; compute Σ ranks of
// positives; convert to U statistic; normalize by N_pos × N_neg.
func areaUnderROC(samples []calibrationSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	indexed := make([]int, len(samples))
	for i := range indexed {
		indexed[i] = i
	}
	sort.Slice(indexed, func(a, b int) bool {
		return samples[indexed[a]].prediction < samples[indexed[b]].prediction
	})
	var (
		positives       int
		negatives       int
		positiveRankSum float64
	)
	// Compute average rank for ties: walk groups of equal prediction
	// values and assign each member of a tie group the average rank.
	for i := 0; i < len(indexed); {
		j := i
		for j < len(indexed) && samples[indexed[j]].prediction == samples[indexed[i]].prediction {
			j++
		}
		// indices [i, j) form a tie group at ranks [i+1, j+1).
		avgRank := (float64(i+1) + float64(j)) / 2.0
		for k := i; k < j; k++ {
			s := samples[indexed[k]]
			if s.label >= 0.5 {
				positives++
				positiveRankSum += avgRank
			} else {
				negatives++
			}
		}
		i = j
	}
	if positives == 0 || negatives == 0 {
		return 0.5
	}
	uStatistic := positiveRankSum - float64(positives)*float64(positives+1)/2.0
	auc := uStatistic / (float64(positives) * float64(negatives))
	if auc < 0 {
		auc = 0
	}
	if auc > 1 {
		auc = 1
	}
	return auc
}
