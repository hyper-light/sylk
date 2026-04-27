package forest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #7 — SubstrateModeStatsSince diagnostic
// ────────────────────────────────────────────────────────────────────
//
// Per-mode rollup of retrieval volume + outcome attribution. Operators
// read this to compare modes empirically: same input distribution
// should produce comparable outcome rates if the modes are
// equivalent; a wider gap means one mode is steering retrieval better.
//
// Outcome attribution is best-effort: we count outcome events that
// landed on a branch returned by a retrieval of the given mode within
// the configured outcomeAttributionWindow. This is not a causal claim
// (the agent may have acted on the branch independently), but it's
// the right denominator/numerator for relative-mode comparison since
// the same correlation rule applies to every mode.

const (
	// outcomeAttributionWindow is how long after a retrieval an
	// outcome event on one of its returned branches is attributed to
	// that retrieval. 24h matches the counterfactual labeling window
	// — outcomes beyond this point are too far removed to reasonably
	// blame on the prior retrieval.
	outcomeAttributionWindow = 24 * time.Hour
)

// SubstrateModeStats reports per-mode retrieval + outcome counts so
// operators can compare modes in production. Used to drive the
// "earn its keep" decision for Issue #7.
type SubstrateModeStats struct {
	// Mode is the substrate mode this row aggregates. Empty string
	// represents legacy retrievals from before Issue #7 shipped.
	Mode SubstrateMode `json:"mode"`

	// RetrievalCount is the number of retrievals in the window that
	// ran with this mode.
	RetrievalCount int64 `json:"retrieval_count"`

	// MeanCandidates / MeanReturned roll up scoring breadth + result
	// breadth — useful for spotting modes that produce different
	// candidate-pool sizes (e.g., warmth_only might have different
	// distribution than pagerank).
	MeanCandidates float64 `json:"mean_candidates"`
	MeanReturned   float64 `json:"mean_returned"`

	// MeanDurationMicros is the average end-to-end Retrieve latency
	// in microseconds. Reads forest_retrieval_events.duration_micros
	// directly — captures the cost difference between modes.
	MeanDurationMicros float64 `json:"mean_duration_micros"`

	// OutcomesSuccess / OutcomesFail count outcome events that
	// landed on a branch returned by a retrieval of this mode within
	// outcomeAttributionWindow. Best-effort attribution; not a
	// causal claim but consistent across modes so the ratio is
	// meaningful for relative comparison.
	OutcomesSuccess int64 `json:"outcomes_success"`
	OutcomesFail    int64 `json:"outcomes_fail"`
}

// SubstrateModeStatsSince returns one SubstrateModeStats per mode
// observed in the window. The empty-mode row (legacy retrievals)
// is included so operators can see how much of the historical data
// is uncategorized.
//
// One SQL round-trip for retrieval stats, one for outcome
// attribution; merged in Go. The outcome query uses the existing
// forest_retrieval_candidates JOIN forest_events index path —
// session-scoped, indexed on (session_id, branch_id, retrieval_at
// DESC) which is exactly the access pattern.
//
// IMPORTANT — retention interaction: outcome attribution joins
// forest_retrieval_candidates, which the background pruner
// (RetrievalCandidatesRetention, default 7d) trims. If retention <
// outcomeAttributionWindow (24h), outcomes for retrievals older than
// retention are silently undercounted. Operators running comparisons
// across long windows should ensure retention >= attribution window;
// the resolver default (7d) comfortably exceeds the 24h attribution.
func (m *MemoryForest) SubstrateModeStatsSince(ctx context.Context, since time.Time) ([]SubstrateModeStats, error) {
	if m == nil {
		return nil, nil
	}
	stats, err := m.loadSubstrateModeRetrievalStats(ctx, since)
	if err != nil {
		return nil, err
	}
	if len(stats) == 0 {
		return nil, nil
	}
	if err := m.attachSubstrateModeOutcomeCounts(ctx, since, stats); err != nil {
		// Outcome-side failure shouldn't blank out the retrieval-side
		// stats. Log via debug logger and return what we have.
		if m.logger != nil {
			m.logger.Debug("forest: substrate-mode outcome rollup failed",
				"err", err.Error())
		}
	}
	return stats, nil
}

// loadSubstrateModeRetrievalStats issues the retrieval-side aggregate.
// COALESCE wraps AVG so a mode with zero rows in the window doesn't
// scan-rejection on NULL.
func (m *MemoryForest) loadSubstrateModeRetrievalStats(ctx context.Context, since time.Time) ([]SubstrateModeStats, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT
			substrate_mode,
			COUNT(*),
			COALESCE(AVG(candidate_count), 0),
			COALESCE(AVG(returned_count), 0),
			COALESCE(AVG(duration_micros), 0)
		FROM   forest_retrieval_events
		WHERE  requested_at >= ?
		GROUP BY substrate_mode
		ORDER BY substrate_mode
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query substrate mode retrieval stats: %w", err)
	}
	defer rows.Close()
	var out []SubstrateModeStats
	for rows.Next() {
		var (
			mode         sql.NullString
			count        int64
			meanCands    float64
			meanReturned float64
			meanDuration float64
		)
		if err := rows.Scan(&mode, &count, &meanCands, &meanReturned, &meanDuration); err != nil {
			return nil, fmt.Errorf("scan substrate mode row: %w", err)
		}
		out = append(out, SubstrateModeStats{
			Mode:               SubstrateMode(mode.String),
			RetrievalCount:     count,
			MeanCandidates:     meanCands,
			MeanReturned:       meanReturned,
			MeanDurationMicros: meanDuration,
		})
	}
	return out, rows.Err()
}

// attachSubstrateModeOutcomeCounts populates OutcomesSuccess /
// OutcomesFail on each row. Issued as one SQL per mode (small fixed
// set) rather than one giant join, keeping each query simple and
// indexable. The query JOINs:
//
//	forest_events e (outcome)
//	  on e.session_id = rc.session_id
//	  AND e.branch_id = rc.branch_id
//	JOIN forest_retrieval_candidates rc
//	  on rc.retrieval_event_id = re.id
//	JOIN forest_retrieval_events re
//	  WHERE re.substrate_mode = :mode
//	    AND re.requested_at >= :since
//	    AND e.timestamp BETWEEN re.requested_at AND re.requested_at + :window
func (m *MemoryForest) attachSubstrateModeOutcomeCounts(ctx context.Context, since time.Time, stats []SubstrateModeStats) error {
	for i := range stats {
		succ, fail, err := m.countSubstrateModeOutcomes(ctx, stats[i].Mode, since)
		if err != nil {
			return err
		}
		stats[i].OutcomesSuccess = succ
		stats[i].OutcomesFail = fail
	}
	return nil
}

// countSubstrateModeOutcomes runs the per-mode outcome-attribution
// query. Returns (success, fail) counts.
//
// Payload status is parsed in Go (not via SQLite json_extract) so the
// diagnostic doesn't require the json1 SQLite extension. The query
// returns raw payload strings; we unmarshal each row's status and
// bucket it. Bounded by the outcome-attribution window so the row
// count stays manageable.
func (m *MemoryForest) countSubstrateModeOutcomes(ctx context.Context, mode SubstrateMode, since time.Time) (int64, int64, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT e.payload
		FROM   forest_events e
		JOIN   forest_retrieval_candidates rc
		    ON rc.session_id = e.session_id
		   AND rc.branch_id  = e.branch_id
		   AND rc.returned   = 1
		JOIN   forest_retrieval_events re
		    ON re.id = rc.retrieval_event_id
		WHERE  e.event_type    = 'outcome_recorded'
		  AND  re.substrate_mode = ?
		  AND  re.requested_at >= ?
		  AND  e.timestamp BETWEEN re.requested_at AND (re.requested_at + ?)
	`, string(mode), since.Unix(), int64(outcomeAttributionWindow.Seconds()))
	if err != nil {
		return 0, 0, fmt.Errorf("query substrate mode outcomes: %w", err)
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

// outcomeStatusFromPayloadJSON unmarshals a JSON-encoded outcome
// payload and returns the status. Empty/invalid payloads return
// OutcomeStatusMixed (matching outcomeStatusFromPayload's default).
func outcomeStatusFromPayloadJSON(raw string) OutcomeStatus {
	if raw == "" {
		return OutcomeStatusMixed
	}
	var parsed struct {
		Status OutcomeStatus `json:"status"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return OutcomeStatusMixed
	}
	if parsed.Status == "" {
		return OutcomeStatusMixed
	}
	return parsed.Status
}
