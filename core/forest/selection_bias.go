package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Counterfactual labeling expansion (Issue #3 mechanism A)
// ────────────────────────────────────────────────────────────────────
//
// When an outcome event names branch B, label B explicitly (existing
// behavior) AND every other candidate that was scored alongside B in
// recent retrievals. Counterfactual rows get a lower weight so the
// trainer treats them as soft signal, not hard label. This breaks
// the "only chosen branches get labels" feedback loop.

// labelExamplesCounterfactualResult bundles the explicit + counter-
// factual change counts so the projector can decide whether to
// schedule a training cycle.
type labelExamplesCounterfactualResult struct {
	Explicit       int64
	Counterfactual int64
}

// labelExamplesForOutcomeWithCounterfactuals replaces the legacy
// labelExamplesForOutcome on the projector's hot path. Performs the
// explicit label first, then walks forest_retrieval_candidates to
// label co-candidates from recent retrievals.
//
// The counterfactual label is the INVERSE of the explicit label in
// utility/risk axes, attenuated by counterfactualLabelWeight: a
// branch that lost to a successful one is a soft-negative; a branch
// that lost to a failed one is a soft-positive. This is the
// unbiased lift signal counterfactual evaluation produces.
func (m *MemoryForest) labelExamplesForOutcomeWithCounterfactuals(ctx context.Context, branchID, sessionID string, status OutcomeStatus) (labelExamplesCounterfactualResult, error) {
	var result labelExamplesCounterfactualResult
	if branchID == "" {
		return result, nil
	}
	if sessionID == "" {
		sessionID = "global"
	}

	explicit, err := m.applyExplicitLabel(ctx, branchID, sessionID, status)
	if err != nil {
		return result, err
	}
	result.Explicit = explicit

	counterfactual, err := m.applyCounterfactualLabels(ctx, branchID, sessionID, status)
	if err != nil {
		// Counterfactual labeling failure is non-fatal — the
		// explicit label already landed. Log and continue.
		slog.Warn("forest_counterfactual_label_failed",
			"branch_id", branchID,
			"session_id", sessionID,
			"err", err.Error(),
		)
	}
	result.Counterfactual = counterfactual
	return result, nil
}

// applyExplicitLabel marks training rows for branch B in the current
// session as having an explicit outcome. label_source ∈ {'',
// 'counterfactual', 'implicit_negative'} is overwritten to
// 'explicit'; if it's already 'explicit' we leave it so a re-fired
// outcome doesn't churn updated_at unnecessarily.
//
// Explicit outcomes are ground truth and supersede any earlier
// counterfactual estimate or implicit-negative inference: the
// utility/risk values are overwritten unconditionally rather than
// COALESCE-preserved. Soft labels left in place would mis-credit the
// trainer (label_source says 'explicit' but the value came from an
// uncertain counterfactual).
//
// Rows whose retrieval_mode = 'exploration' get the LabelSourceExplicit
// status too — exploration is the *retrieval* mode, not the label
// source — but downstream weighting reads label_weight (which the
// retrieval-record step already set high for exploration).
func (m *MemoryForest) applyExplicitLabel(ctx context.Context, branchID, sessionID string, status OutcomeStatus) (int64, error) {
	utility, risk := outcomeLabels(status)
	res, err := m.db.ExecContext(ctx, `
		UPDATE forest_training_examples
		SET    utility_label = ?,
		       risk_label    = ?,
		       label_source  = ?,
		       updated_at    = ?
		WHERE  branch_id = ?
		  AND  session_id = ?
		  AND  label_source != ?
	`,
		utility, risk,
		string(LabelSourceExplicit),
		time.Now().UTC().Unix(),
		branchID, sessionID,
		string(LabelSourceExplicit),
	)
	if err != nil {
		return 0, fmt.Errorf("apply explicit label: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("explicit label rows affected: %w", err)
	}
	return changed, nil
}

// applyCounterfactualLabels finds retrievals where branchID was a
// candidate within the configured window, then labels every co-
// candidate row that hasn't yet been explicitly labeled. Co-candidates
// receive the inverse outcome at counterfactualLabelWeight magnitude.
//
// SQL strategy:
//
//	1. find recent retrievals containing branchID via the candidates
//	   projection (indexed on branch_id);
//	2. for each retrieval, UPDATE forest_training_examples rows whose
//	   retrieval_id matches and whose branch_id != branchID, IF the
//	   row hasn't been explicitly labeled yet.
//
// The denormalized retrieval_id on forest_training_examples (set
// at recordRetrievalExamples time) is the join key.
// applyCounterfactualLabels labels co-candidates from recent
// retrievals where branchID was scored. Joins via audit_event_id —
// every training row produced by Retrieve carries the audit ID of
// the retrieval that produced it, and forest_retrieval_candidates
// is keyed by retrieval_event_id (same value).
func (m *MemoryForest) applyCounterfactualLabels(ctx context.Context, branchID, sessionID string, status OutcomeStatus) (int64, error) {
	if m.counterfactualLabelWeight <= 0 {
		return 0, nil
	}
	since := time.Now().UTC().Add(-m.counterfactualWindow).Unix()
	utility, risk := outcomeLabels(invertOutcomeStatus(status))

	res, err := m.db.ExecContext(ctx, `
		UPDATE forest_training_examples
		SET    utility_label = COALESCE(utility_label, ?),
		       risk_label    = COALESCE(risk_label, ?),
		       label_source  = ?,
		       label_weight  = ?,
		       updated_at    = ?
		WHERE  branch_id != ?
		  AND  session_id = ?
		  AND  audit_event_id != ''
		  AND  (utility_label IS NULL OR risk_label IS NULL)
		  AND  label_source NOT IN (?, ?)
		  AND  audit_event_id IN (
		      SELECT DISTINCT rc.retrieval_event_id
		      FROM   forest_retrieval_candidates rc
		      WHERE  rc.branch_id = ?
		        AND  rc.session_id = ?
		        AND  rc.retrieval_at >= ?
		  )
	`,
		utility, risk,
		string(LabelSourceCounterfactual),
		m.counterfactualLabelWeight,
		time.Now().UTC().Unix(),
		branchID, sessionID,
		string(LabelSourceExplicit), string(LabelSourceCounterfactual),
		branchID, sessionID, since,
	)
	if err != nil {
		return 0, fmt.Errorf("apply counterfactual labels: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counterfactual rows affected: %w", err)
	}
	return changed, nil
}

// invertOutcomeStatus flips success ↔ failure for counterfactual
// labeling. A branch that lost to a successful winner is a soft
// negative (would have been worse); a branch that lost to a failed
// winner is a soft positive (might have been better).
func invertOutcomeStatus(status OutcomeStatus) OutcomeStatus {
	switch status {
	case OutcomeStatusSucceeded:
		return OutcomeStatusFailed
	case OutcomeStatusFailed:
		return OutcomeStatusSucceeded
	}
	return OutcomeStatusMixed
}


// ────────────────────────────────────────────────────────────────────
// ε-greedy exploration (Issue #3 mechanism C)
// ────────────────────────────────────────────────────────────────────

// shouldExplore samples the per-forest RNG to decide whether the
// current Retrieve call should be an exploration sample. Mutex-
// guarded because math/rand sources are not goroutine-safe.
func (m *MemoryForest) shouldExplore() bool {
	if m == nil || m.explorationRate <= 0 {
		return false
	}
	if m.explorationRate >= 1 {
		return true
	}
	m.explorationRngMu.Lock()
	defer m.explorationRngMu.Unlock()
	return m.explorationRng.Float64() < m.explorationRate
}

// explorationShuffleAndPick replaces the top-K with a random sample
// from the broader candidate pool. Returns a NEW slice of packets in
// random order; the input slice is unchanged so the audit population
// pass still sees the full ranked set with their original ranks.
//
// When len(packets) <= limit, the entire pool is the sample.
// Implemented via partial Fisher–Yates so we only randomize the
// first `limit` slots.
func (m *MemoryForest) explorationShuffleAndPick(packets []*BranchPacket, limit int) []*BranchPacket {
	if limit <= 0 || len(packets) == 0 {
		return packets
	}
	picked := make([]*BranchPacket, len(packets))
	copy(picked, packets)
	if limit > len(picked) {
		limit = len(picked)
	}
	m.explorationRngMu.Lock()
	defer m.explorationRngMu.Unlock()
	for i := 0; i < limit; i++ {
		j := i + m.explorationRng.Intn(len(picked)-i)
		picked[i], picked[j] = picked[j], picked[i]
	}
	return picked[:limit]
}

// ────────────────────────────────────────────────────────────────────
// Implicit-negative sweeper (Issue #3 mechanism B)
// ────────────────────────────────────────────────────────────────────

// implicitNegativeSweepBatchSize bounds the number of retrievals
// processed per sweep cycle. Keeps the transaction small.
const implicitNegativeSweepBatchSize = 64

// startImplicitNegativeSweeper launches the tracked goroutine that
// periodically scans forest_retrieval_events for unswept retrievals
// older than the configured horizon and labels their returned
// branches as Ignored. Tracked on m.wg so Close() drains.
//
// No defensive recover: every code path in the sweeper has explicit
// error returns + bounds-checked slice access + nil-guarded pointer
// dereference, so a runtime panic would indicate a real bug we want
// surfaced as a process crash, not silently swallowed.
func (m *MemoryForest) startImplicitNegativeSweeper() {
	if m == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runImplicitNegativeSweeperLoop()
	}()
}

// runImplicitNegativeSweeperLoop drives the sweep on the configured
// interval. Drain-then-sleep: when a batch comes back full we
// immediately re-loop instead of sleeping, so a sudden burst of
// retrievals can't outrun the sweeper. Only when the batch is partial
// (work caught up) do we sleep until the next interval.
func (m *MemoryForest) runImplicitNegativeSweeperLoop() {
	for {
		if m.shouldStopProjector() {
			return
		}
		processed, err := m.runImplicitNegativeSweepOnceCounted(m.runCtx)
		if err != nil {
			slog.Warn("forest_implicit_negative_sweep_failed",
				"err", err.Error())
		}
		if processed >= implicitNegativeSweepBatchSize {
			continue
		}
		if !m.sleepProjector(m.implicitNegativeSweepInterval) {
			return
		}
	}
}

// runImplicitNegativeSweepOnce performs one pass: find unswept
// retrievals older than horizon whose returned branches saw no
// outcome event, label them Ignored, and mark the retrieval as
// swept. Idempotent via swept_at watermark.
func (m *MemoryForest) runImplicitNegativeSweepOnce(ctx context.Context) error {
	_, err := m.runImplicitNegativeSweepOnceCounted(ctx)
	return err
}

// runImplicitNegativeSweepOnceCounted is the count-aware variant the
// sweeper loop uses to detect a saturated batch. Returns the number
// of candidates processed (regardless of per-candidate success).
func (m *MemoryForest) runImplicitNegativeSweepOnceCounted(ctx context.Context) (int, error) {
	cutoff := time.Now().UTC().Add(-m.implicitNegativeHorizon).Unix()
	candidates, err := m.loadUnsweptRetrievals(ctx, cutoff, implicitNegativeSweepBatchSize)
	if err != nil {
		return 0, fmt.Errorf("load unswept retrievals: %w", err)
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	now := time.Now().UTC()
	for i := range candidates {
		if err := m.processOneSweepCandidate(ctx, &candidates[i], now); err != nil {
			slog.Warn("forest_implicit_negative_sweep_candidate_failed",
				"retrieval_id", candidates[i].id,
				"err", err.Error())
		}
	}
	return len(candidates), nil
}

// sweepCandidate is the projection of a single unswept retrieval
// pulled by loadUnsweptRetrievals.
type sweepCandidate struct {
	id        string
	sessionID string
	requested time.Time
}

// loadUnsweptRetrievals returns retrievals older than cutoff that
// have no row in forest_retrieval_sweep_state. Uses LEFT JOIN +
// IS NULL because forest_retrieval_events is append-only — sweep
// state lives in the sibling table, not on the events row itself.
func (m *MemoryForest) loadUnsweptRetrievals(ctx context.Context, cutoff int64, limit int) ([]sweepCandidate, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT e.id, e.session_id, e.requested_at
		FROM   forest_retrieval_events e
		LEFT JOIN forest_retrieval_sweep_state s ON s.retrieval_event_id = e.id
		WHERE  s.retrieval_event_id IS NULL
		  AND  e.requested_at <= ?
		ORDER BY e.requested_at ASC
		LIMIT  ?
	`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sweepCandidate
	for rows.Next() {
		var (
			c  sweepCandidate
			ts int64
		)
		if err := rows.Scan(&c.id, &c.sessionID, &ts); err != nil {
			return nil, fmt.Errorf("scan unswept retrieval: %w", err)
		}
		c.requested = time.Unix(ts, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// sqlExec abstracts the subset of *sql.DB / *sql.Tx that the sweep
// helpers need so they can run inside a single transaction (atomic
// per candidate) without code duplication.
type sqlExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// processOneSweepCandidate inspects a single retrieval and applies
// implicit-negative labels if no explicit outcome has been recorded
// for any of its returned branches. Marks the retrieval swept
// regardless so we don't re-evaluate.
//
// All four sub-operations run in a single transaction — a crash
// between the label apply and the sweep marker would otherwise leave
// the retrieval labeled-but-unmarked, causing a re-sweep next cycle.
// The applyImplicitNegative SQL is idempotent on row state, but we
// avoid the wasted churn (and avoid fooling diagnostics into thinking
// labels are still being produced).
func (m *MemoryForest) processOneSweepCandidate(ctx context.Context, c *sweepCandidate, now time.Time) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sweep tx: %w", err)
	}
	defer tx.Rollback()

	branchIDs, err := returnedBranchesForRetrievalTx(ctx, tx, c.id)
	if err != nil {
		return err
	}
	hasExplicit, err := anyExplicitLabelTx(ctx, tx, c.sessionID, branchIDs)
	if err != nil {
		return err
	}
	if !hasExplicit {
		if err := m.applyImplicitNegativeTx(ctx, tx, c.sessionID, branchIDs); err != nil {
			return err
		}
	}
	if err := markRetrievalSweptTx(ctx, tx, c.id, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sweep tx: %w", err)
	}
	return nil
}

func returnedBranchesForRetrievalTx(ctx context.Context, exec sqlExec, retrievalID string) ([]string, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT branch_id
		FROM   forest_retrieval_candidates
		WHERE  retrieval_event_id = ?
		  AND  returned = 1
	`, retrievalID)
	if err != nil {
		return nil, fmt.Errorf("load returned branches: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func anyExplicitLabelTx(ctx context.Context, exec sqlExec, sessionID string, branchIDs []string) (bool, error) {
	if len(branchIDs) == 0 {
		return false, nil
	}
	placeholders, args := branchSetPlaceholders(branchIDs, sessionID)
	query := `
		SELECT 1
		FROM   forest_training_examples
		WHERE  session_id = ?
		  AND  label_source = 'explicit'
		  AND  branch_id IN (` + placeholders + `)
		LIMIT  1
	`
	row := exec.QueryRowContext(ctx, query, args...)
	var found int
	switch err := row.Scan(&found); {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("check explicit label: %w", err)
	}
}

func (m *MemoryForest) applyImplicitNegativeTx(ctx context.Context, exec sqlExec, sessionID string, branchIDs []string) error {
	if len(branchIDs) == 0 {
		return nil
	}
	utility, risk := outcomeLabels(OutcomeStatusIgnored)
	placeholders, args := branchSetPlaceholders(branchIDs, sessionID)
	tail := []any{
		utility, risk,
		string(LabelSourceImplicitNegative),
		m.implicitNegativeWeight,
		time.Now().UTC().Unix(),
	}
	args = append(tail, args...)
	args = append(args, string(LabelSourceExplicit), string(LabelSourceCounterfactual))
	query := `
		UPDATE forest_training_examples
		SET    utility_label = COALESCE(utility_label, ?),
		       risk_label    = COALESCE(risk_label, ?),
		       label_source  = ?,
		       label_weight  = ?,
		       updated_at    = ?
		WHERE  session_id = ?
		  AND  branch_id IN (` + placeholders + `)
		  AND  (utility_label IS NULL OR risk_label IS NULL)
		  AND  label_source NOT IN (?, ?)
	`
	if _, err := exec.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("apply implicit negative label: %w", err)
	}
	return nil
}

// markRetrievalSweptTx records the sweep marker. Lives on a sibling
// table because forest_retrieval_events is append-only (any UPDATE
// fires the trigger ABORT). UPSERT keeps the operation idempotent.
func markRetrievalSweptTx(ctx context.Context, exec sqlExec, retrievalID string, now time.Time) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO forest_retrieval_sweep_state
		(retrieval_event_id, swept_at)
		VALUES (?, ?)
		ON CONFLICT (retrieval_event_id) DO UPDATE SET
			swept_at = excluded.swept_at
	`, retrievalID, now.Unix())
	if err != nil {
		return fmt.Errorf("mark retrieval swept: %w", err)
	}
	return nil
}

// branchSetPlaceholders builds the ` ?, ?, ? ` placeholder string
// for a SQL IN clause and the corresponding args slice with the
// session_id prefix. Callers prepend additional bindings as needed.
func branchSetPlaceholders(branchIDs []string, sessionID string) (string, []any) {
	parts := make([]string, len(branchIDs))
	args := make([]any, 0, len(branchIDs)+1)
	args = append(args, sessionID)
	for i, id := range branchIDs {
		parts[i] = "?"
		args = append(args, id)
	}
	return strings.Join(parts, ","), args
}

// ────────────────────────────────────────────────────────────────────
// Diagnostics surface
// ────────────────────────────────────────────────────────────────────

// TrainingLabelStatsSince returns counts of training examples per
// label source created or updated within the time window. Used by
// operators to verify selection-bias-fix mechanisms are accumulating
// signal as expected.
func (m *MemoryForest) TrainingLabelStatsSince(ctx context.Context, since time.Time) (TrainingLabelStats, error) {
	var stats TrainingLabelStats
	if m == nil {
		return stats, nil
	}
	// COALESCE wraps SUM so an empty result set (no rows match
	// since-cutoff) returns 0 instead of NULL — the int64 Scan
	// target rejects NULL.
	//
	// Exploration is reported as the count of rows that ran via
	// exploration retrieval, regardless of which outcome label
	// landed (explicit, counterfactual, implicit_negative). It's a
	// separate axis from label_source.
	row := m.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN label_source = 'explicit'          THEN 1 ELSE 0 END), 0) AS explicit,
			COALESCE(SUM(CASE WHEN label_source = 'counterfactual'    THEN 1 ELSE 0 END), 0) AS counterfactual,
			COALESCE(SUM(CASE WHEN label_source = 'implicit_negative' THEN 1 ELSE 0 END), 0) AS implicit_negative,
			COALESCE(SUM(CASE WHEN retrieval_mode = 'exploration'     THEN 1 ELSE 0 END), 0) AS exploration
		FROM forest_training_examples
		WHERE updated_at >= ?
	`, since.Unix())
	if err := row.Scan(&stats.Explicit, &stats.Counterfactual, &stats.ImplicitNegative, &stats.Exploration); err != nil {
		return stats, fmt.Errorf("aggregate training label stats: %w", err)
	}
	return stats, nil
}
