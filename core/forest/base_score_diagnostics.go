package forest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #8 — diagnostics surface
// ────────────────────────────────────────────────────────────────────
//
// Read-only operator queries:
//
//   BaseScoreModelInfo()              — current champion + challenger
//   BaseScoreVariantStatsSince(since) — per-variant retrieval +
//                                       outcome attribution
//   ScoreComponentStatsSince(since)   — per-component contribution +
//                                       pruning candidates
//
// Every query is bounded, every scan is checked, every result is
// nil-safe. Errors flow back through Go's error idiom; the
// diagnostic surface NEVER panics.

// BaseScoreModelInfoSnapshot bundles the active models + cache state
// so operators can audit "which model is serving traffic?" in one
// query.
type BaseScoreModelInfoSnapshot struct {
	Champion               *BaseScoreModel `json:"champion,omitempty"`
	Challenger             *BaseScoreModel `json:"challenger,omitempty"`
	UsingHardcodedDefaults bool            `json:"using_hardcoded_defaults"`
}

// BaseScoreModelInfo returns the active champion + challenger from
// the cache. No DB round-trip — reads the atomic.Pointer holdings.
// Always nil-safe.
func (m *MemoryForest) BaseScoreModelInfo() BaseScoreModelInfoSnapshot {
	if m == nil || m.baseScoreCache == nil {
		return BaseScoreModelInfoSnapshot{UsingHardcodedDefaults: true}
	}
	return BaseScoreModelInfoSnapshot{
		Champion:               m.baseScoreCache.Champion(),
		Challenger:             m.baseScoreCache.Challenger(),
		UsingHardcodedDefaults: m.baseScoreCache.Champion() == nil,
	}
}

// BaseScoreVariantStats reports per-variant retrieval + outcome
// counts so operators can compare champion vs challenger empirically.
type BaseScoreVariantStats struct {
	Variant            BaseScoreVariant `json:"variant"`
	Version            int64            `json:"version,omitempty"`
	RetrievalCount     int64            `json:"retrieval_count"`
	MeanCandidates     float64          `json:"mean_candidates"`
	MeanReturned       float64          `json:"mean_returned"`
	MeanDurationMicros float64          `json:"mean_duration_micros"`
	OutcomesSuccess    int64            `json:"outcomes_success"`
	OutcomesFail       int64            `json:"outcomes_fail"`
}

// BaseScoreVariantStatsSince rolls up retrievals + outcomes per
// variant + version. Mirrors SubstrateModeStatsSince's structure;
// the query returns one row per (variant, version) pair seen in the
// window.
//
// Outcome attribution: an outcome event on a branch returned by a
// retrieval of variant V within outcomeAttributionWindow counts
// toward V's success/fail tally. Same caveats as Issue #7's stats:
// retention < attribution_window leads to undercounting old data.
func (m *MemoryForest) BaseScoreVariantStatsSince(ctx context.Context, since time.Time) ([]BaseScoreVariantStats, error) {
	if m == nil {
		return nil, nil
	}
	stats, err := m.loadBaseScoreVariantRetrievalStats(ctx, since)
	if err != nil {
		return nil, err
	}
	if len(stats) == 0 {
		return nil, nil
	}
	if err := m.attachBaseScoreVariantOutcomeCounts(ctx, since, stats); err != nil {
		// Outcome attribution failure is logged but doesn't blank
		// out the retrieval-side stats.
		if m.logger != nil {
			m.logger.Debug("forest: base-score variant outcome rollup failed",
				"err", err.Error())
		}
	}
	return stats, nil
}

func (m *MemoryForest) loadBaseScoreVariantRetrievalStats(ctx context.Context, since time.Time) ([]BaseScoreVariantStats, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT
			base_score_variant,
			base_score_version,
			COUNT(*),
			COALESCE(AVG(candidate_count), 0),
			COALESCE(AVG(returned_count), 0),
			COALESCE(AVG(duration_micros), 0)
		FROM   forest_retrieval_events
		WHERE  requested_at >= ?
		GROUP BY base_score_variant, base_score_version
		ORDER BY base_score_variant, base_score_version
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query base-score variant stats: %w", err)
	}
	defer rows.Close()

	var out []BaseScoreVariantStats
	for rows.Next() {
		var (
			variantStr   sql.NullString
			version      int64
			count        int64
			meanCands    float64
			meanReturned float64
			meanDuration float64
		)
		if err := rows.Scan(&variantStr, &version, &count, &meanCands, &meanReturned, &meanDuration); err != nil {
			return nil, fmt.Errorf("scan base-score variant row: %w", err)
		}
		out = append(out, BaseScoreVariantStats{
			Variant:            BaseScoreVariant(variantStr.String),
			Version:            version,
			RetrievalCount:     count,
			MeanCandidates:     meanCands,
			MeanReturned:       meanReturned,
			MeanDurationMicros: meanDuration,
		})
	}
	return out, rows.Err()
}

func (m *MemoryForest) attachBaseScoreVariantOutcomeCounts(ctx context.Context, since time.Time, stats []BaseScoreVariantStats) error {
	for i := range stats {
		succ, fail, err := m.countBaseScoreVariantOutcomes(ctx, stats[i].Variant, stats[i].Version, since)
		if err != nil {
			return err
		}
		stats[i].OutcomesSuccess = succ
		stats[i].OutcomesFail = fail
	}
	return nil
}

// countBaseScoreVariantOutcomes joins canonical ledger outcome rows against
// retrieval candidates produced by retrievals of the given variant/version
// within the attribution window. Payload status is parsed in Go (no SQLite
// json1 dependency).
func (m *MemoryForest) countBaseScoreVariantOutcomes(ctx context.Context, variant BaseScoreVariant, version int64, since time.Time) (int64, int64, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT p.payload
		FROM   forest_ledger e
		JOIN   forest_ledger_payloads p ON p.ledger_id = e.id
		JOIN   forest_retrieval_candidates rc
		    ON rc.session_id = e.session_id
		   AND rc.branch_id  = e.subject_id
		   AND rc.returned   = 1
		JOIN   forest_retrieval_events re
		    ON re.id = rc.retrieval_event_id
		WHERE  e.source_kind       = ?
		  AND  e.subject_type      = 'branch'
		  AND  e.event_kind        = 'outcome_recorded'
		  AND  re.base_score_variant = ?
		  AND  re.base_score_version = ?
		  AND  re.requested_at >= ?
		  AND  e.occurred_at BETWEEN re.requested_at AND (re.requested_at + ?)
	`, string(LedgerSourceForestEvent), string(variant), version, since.Unix(), int64(outcomeAttributionWindow.Seconds()))
	if err != nil {
		return 0, 0, fmt.Errorf("query base-score variant outcomes: %w", err)
	}
	defer rows.Close()
	var success, fail int64
	for rows.Next() {
		var payload sql.NullString
		if err := rows.Scan(&payload); err != nil {
			return 0, 0, fmt.Errorf("scan outcome payload: %w", err)
		}
		switch outcomeStatusFromPayloadJSON(payload.String) {
		case OutcomeStatusSucceeded:
			success++
		case OutcomeStatusFailed:
			fail++
		}
	}
	return success, fail, rows.Err()
}

// ScoreComponentStat reports per-component aggregate behavior so
// operators can detect components that contribute little to ranking
// (pruning candidates) or that dominate (potentially mis-tuned).
type ScoreComponentStat struct {
	Name               string  `json:"name"`
	Index              int     `json:"index"`
	HardcodedWeight    float64 `json:"hardcoded_weight"`
	LearnedWeight      float64 `json:"learned_weight"`
	MeanContribution   float64 `json:"mean_contribution"`
	StddevContribution float64 `json:"stddev_contribution"`
	SampleCount        int64   `json:"sample_count"`
	PruningCandidate   bool    `json:"pruning_candidate"`
}

// ScoreComponentStatsSince computes per-component contribution
// statistics from the audit ledger over the supplied window. Reads
// candidates_blob (JSON) and computes (weight × component_value) per
// candidate; aggregates mean + stddev per component.
//
// Component pruning signal: components whose absolute learned weight
// is below baseScorePruningThresh are flagged. Operators decide
// whether to drop the column from defaultScoreWeights + the feature
// pipeline.
//
// Bounded by limit on the SELECT — 5000 events sampled per call by
// default. Larger windows just sample more events.
func (m *MemoryForest) ScoreComponentStatsSince(ctx context.Context, since time.Time) ([]ScoreComponentStat, error) {
	if m == nil {
		return nil, nil
	}
	weights := m.activeBaseScoreWeights()
	contributions := newComponentContributionAccumulator()

	rows, err := m.db.QueryContext(ctx, `
		SELECT candidates_blob
		FROM   forest_retrieval_events
		WHERE  requested_at >= ?
		LIMIT  5000
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query candidates blobs for component stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var blob string
		if err := rows.Scan(&blob); err != nil {
			return nil, fmt.Errorf("scan candidates blob: %w", err)
		}
		contributions.absorb(blob, weights)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates blobs: %w", err)
	}
	return contributions.snapshot(weights, m.hyperparams().BaseScorePruningThreshold), nil
}

// activeBaseScoreWeights returns the current champion's weights as
// float64, or hardcoded defaults if no champion is loaded. Used by
// ScoreComponentStatsSince + the pruning diagnostic.
func (m *MemoryForest) activeBaseScoreWeights() [BaseScoreComponentCount]float64 {
	var out [BaseScoreComponentCount]float64
	if m == nil || m.baseScoreCache == nil {
		copyDefaultsAsFloat64(&out)
		return out
	}
	champion := m.baseScoreCache.Champion()
	if champion == nil {
		copyDefaultsAsFloat64(&out)
		return out
	}
	return champion.Weights
}

func copyDefaultsAsFloat64(out *[BaseScoreComponentCount]float64) {
	for i := 0; i < BaseScoreComponentCount && i < len(defaultScoreWeights); i++ {
		out[i] = float64(defaultScoreWeights[i])
	}
}

// componentContributionAccumulator builds running mean + variance
// per component using Welford's algorithm — O(1) memory per
// component, numerically stable.
type componentContributionAccumulator struct {
	count [BaseScoreComponentCount]int64
	mean  [BaseScoreComponentCount]float64
	m2    [BaseScoreComponentCount]float64
}

func newComponentContributionAccumulator() *componentContributionAccumulator {
	return &componentContributionAccumulator{}
}

// absorb decodes one candidates_blob and folds each candidate's
// per-component contribution into the running stats. Malformed blobs
// are silently skipped — the rest of the window still aggregates
// correctly.
func (a *componentContributionAccumulator) absorb(blob string, weights [BaseScoreComponentCount]float64) {
	if a == nil || blob == "" {
		return
	}
	var candidates []RetrievalAuditCandidate
	if err := json.Unmarshal([]byte(blob), &candidates); err != nil {
		return
	}
	for k := range candidates {
		c := &candidates[k]
		// Components is a string→float64 map keyed by the same
		// names as BaseScoreComponentNames. Look up each component
		// by name; missing entries score 0 (treated as no signal).
		for i, name := range BaseScoreComponentNames {
			value := c.Components[name]
			contribution := weights[i] * value
			a.foldOne(i, contribution)
		}
	}
}

// foldOne updates mean + variance via Welford's recurrence:
//
//	delta  = value - mean
//	mean  += delta / count
//	m2    += delta * (value - mean_after)
func (a *componentContributionAccumulator) foldOne(idx int, value float64) {
	if idx < 0 || idx >= BaseScoreComponentCount {
		return
	}
	a.count[idx]++
	delta := value - a.mean[idx]
	a.mean[idx] += delta / float64(a.count[idx])
	delta2 := value - a.mean[idx]
	a.m2[idx] += delta * delta2
}

// snapshot produces the final ScoreComponentStat list. Stddev is
// computed from running M2 / (count - 1); single-sample components
// report stddev=0.
func (a *componentContributionAccumulator) snapshot(weights [BaseScoreComponentCount]float64, pruningThresh float64) []ScoreComponentStat {
	out := make([]ScoreComponentStat, 0, BaseScoreComponentCount)
	for i := 0; i < BaseScoreComponentCount; i++ {
		var stddev float64
		if a.count[i] > 1 {
			stddev = math.Sqrt(a.m2[i] / float64(a.count[i]-1))
		}
		hardcoded := float64(0)
		if i < len(defaultScoreWeights) {
			hardcoded = float64(defaultScoreWeights[i])
		}
		learned := weights[i]
		out = append(out, ScoreComponentStat{
			Name:               BaseScoreComponentNames[i],
			Index:              i,
			HardcodedWeight:    hardcoded,
			LearnedWeight:      learned,
			MeanContribution:   a.mean[i],
			StddevContribution: stddev,
			SampleCount:        a.count[i],
			PruningCandidate:   math.Abs(learned) < pruningThresh,
		})
	}
	return out
}
