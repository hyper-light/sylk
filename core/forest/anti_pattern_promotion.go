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
// Issue #6 — AntiPattern promotion projector
// ────────────────────────────────────────────────────────────────────
//
// Periodic scanner that promotes Failed-outcome branches with
// sufficient negative evidence into a sibling AntiPattern branch.
// AntiPatterns survive across sessions and become the durable
// "this approach has failed" signal that the Retrieve anti-precedent
// quota surfaces. Idempotent via a NOT-EXISTS subquery on
// (family=antipattern, parent_id=source.id).
//
// Modeled after the implicit-negative sweeper:
//   - tracked goroutine on m.wg, drains on Close()
//   - panic recovery at goroutine boundary
//   - drain-then-sleep cadence: a full batch loops immediately, a
//     partial batch sleeps until next interval
//   - per-source-branch transaction: promotion + idempotency check in
//     one tx so a crash window can't double-promote.

const (
	// projectorAntiPatternName is the canonical lease holder name for
	// the AntiPattern promoter row in forest_projector_state. Reuses
	// the same coordination table the branch + retrieval-candidates
	// projectors do; multi-process deployments contend on this row,
	// so only one process runs the promotion scan per lease window.
	projectorAntiPatternName = "antipattern_promoter"

	// defaultAntiPatternFailureThreshold is the minimum failure_count
	// before a branch becomes eligible. Below this, the evidence is
	// considered noise — bad luck rather than a pattern.
	defaultAntiPatternFailureThreshold = 3

	// defaultAntiPatternFailureRate is the minimum failure_count /
	// (success_count + failure_count) before promotion. 0.6 keeps
	// frequently-failed branches in scope while ignoring branches
	// that fail less than half the time.
	defaultAntiPatternFailureRate = 0.6

	// defaultAntiPatternPromotionInterval is the cadence of the
	// promotion scanner. 10 minutes is long enough to amortize across
	// many ingested events; short enough that a sustained failure
	// pattern is surfaced within an hour.
	defaultAntiPatternPromotionInterval = 10 * time.Minute

	// antiPatternPromotionBatchSize bounds the per-cycle transaction
	// scope. Each candidate is one tx, so this controls the upper
	// bound on per-cycle DB writes.
	antiPatternPromotionBatchSize = 32

	// antiPatternSaliencePivot caps how much weight the source's
	// failure_count contributes to the AntiPattern's salience —
	// avoids a 1000-failure branch producing a salience of 1000.
	antiPatternSaliencePivot = 16.0
)

func resolveAntiPatternFailureThreshold(n int) int {
	if n <= 0 {
		return defaultAntiPatternFailureThreshold
	}
	return n
}

// resolveAntiPatternFailureRate clamps to [0,1].
func resolveAntiPatternFailureRate(r float64) float64 {
	if r <= 0 {
		return defaultAntiPatternFailureRate
	}
	if r > 1 {
		return 1
	}
	return r
}

func resolveAntiPatternPromotionInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultAntiPatternPromotionInterval
	}
	return d
}

// startAntiPatternPromoter launches the tracked goroutine that drives
// the AntiPattern promotion scanner. Tracked on m.wg so Close()
// drains it.
//
// No defensive recover: every promotion code path is bounds-checked
// with explicit error returns. A runtime panic here would indicate
// a real bug — surface it as a crash, don't swallow.
func (m *MemoryForest) startAntiPatternPromoter() {
	if m == nil {
		return
	}
	m.registerRuntimeTicker("anti_pattern_promoter_interval", m.antiPatternPromotionInterval)
	m.startWorker("anti_pattern_promoter", antiPatternPromotionBatchSize, func(context.Context) error {
		m.runAntiPatternPromoterLoop()
		return nil
	})
}

// runAntiPatternPromoterLoop drives the scan on the configured
// interval under lease coordination. The lease is acquired (or
// renewed) at the top of each cycle; a non-leader process logs once
// and waits projectorWaitForLease before retrying — bounded so a
// dead leader is taken over within projectorLeaseDuration.
//
// Drain-then-sleep cadence: a full batch re-loops immediately under
// the same lease; a partial batch sleeps until the next interval.
// shouldStopProjector wins over both the lease wait and the inter-
// cycle sleep so Close() drains promptly.
func (m *MemoryForest) runAntiPatternPromoterLoop() {
	for {
		if m.shouldStopProjector() {
			return
		}
		if err := m.acquireAntiPatternPromoterLease(); err != nil {
			if errors.Is(err, errLeaseHeldByOther) {
				if !m.sleepProjector(projectorWaitForLease) {
					return
				}
				continue
			}
			slog.Warn("forest_antipattern_lease_failed",
				"holder", m.projectorID, "err", err.Error())
			if !m.sleepProjector(m.antiPatternPromotionInterval) {
				return
			}
			continue
		}
		processed, err := m.runAntiPatternPromotionOnce(m.runCtx)
		if err != nil {
			slog.Warn("forest_antipattern_promotion_failed",
				"err", err.Error())
		}
		if processed >= antiPatternPromotionBatchSize {
			continue
		}
		if !m.sleepProjector(m.antiPatternPromotionInterval) {
			return
		}
	}
}

// acquireAntiPatternPromoterLease takes (or renews) the lease for the
// AntiPattern promoter. Reuses ensureProjectorRow + the same UPDATE-
// where-expired pattern as the branch projector. Returns
// errLeaseHeldByOther when another process holds an unexpired lease.
//
// Unlike streaming projectors, the promoter doesn't track a
// watermark — promotion is idempotent (NOT EXISTS guard), so the
// scanner can safely re-run a batch after a lease handoff. We just
// need the lease to avoid running redundant scans across processes.
func (m *MemoryForest) acquireAntiPatternPromoterLease() error {
	if err := m.ensureProjectorRow(projectorAntiPatternName); err != nil {
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
		projectorAntiPatternName, now, m.projectorID)
	if err != nil {
		return fmt.Errorf("acquire antipattern lease: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return errLeaseHeldByOther
	}
	return nil
}

// runAntiPatternPromotionOnce performs one promotion pass: find
// eligible source branches, promote each into a sibling AntiPattern.
// Returns the number of source branches considered (regardless of
// per-candidate success).
func (m *MemoryForest) runAntiPatternPromotionOnce(ctx context.Context) (int, error) {
	candidates, err := m.loadAntiPatternPromotionCandidates(ctx, antiPatternPromotionBatchSize)
	if err != nil {
		return 0, fmt.Errorf("load promotion candidates: %w", err)
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	for i := range candidates {
		if err := m.promoteOneAntiPattern(ctx, &candidates[i]); err != nil {
			slog.Warn("forest_antipattern_promote_failed",
				"source_branch", candidates[i].ID,
				"err", err.Error())
		}
	}
	return len(candidates), nil
}

// loadAntiPatternPromotionCandidates returns up to `limit` source
// branches that:
//
//	(a) are Outcome-family branches with failures dominating
//	    successes,
//	(b) carry at least antiPatternFailureThreshold failures, AND
//	(c) have no existing AntiPattern projection (parent_id link).
//
// Multi-process-safe: the NOT EXISTS subquery is re-evaluated inside
// promoteOneAntiPattern's transaction so a concurrent promoter on
// another process can't double-promote.
func (m *MemoryForest) loadAntiPatternPromotionCandidates(ctx context.Context, limit int) ([]Branch, error) {
	q := `
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata,
		       last_applied_seq, constraint_severity
		FROM   forest_branches src
		WHERE  family = ?
		  AND  state != ?
		  AND  failure_count >= ?
		  AND  failure_count > (success_count + failure_count) * ?
		  AND  NOT EXISTS (
		      SELECT 1 FROM forest_branches a
		      WHERE a.family = ?
		        AND a.parent_id = src.id
		  )
		ORDER BY failure_count DESC, updated_at ASC
		LIMIT  ?
	`
	rows, err := m.db.QueryContext(ctx, q,
		string(TreeFamilyOutcome),
		string(BranchStateSuperseded),
		m.antiPatternFailureThreshold,
		m.antiPatternFailureRate,
		string(TreeFamilyAntiPattern),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query promotion candidates: %w", err)
	}
	defer rows.Close()

	out := make([]Branch, 0, limit)
	for rows.Next() {
		branch, scanErr := scanBranch(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if branch == nil {
			continue
		}
		out = append(out, *branch)
	}
	return out, rows.Err()
}

// promoteOneAntiPattern wraps the idempotency re-check + insertion in
// a single transaction. The re-check inside the tx defends against
// two concurrent promoters racing to promote the same source.
func (m *MemoryForest) promoteOneAntiPattern(ctx context.Context, source *Branch) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin promotion tx: %w", err)
	}
	defer tx.Rollback()

	exists, err := antiPatternExistsForSourceTx(ctx, tx, source.ID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	projection := buildAntiPatternProjection(source, time.Now().UTC())
	if err := upsertBranchTx(ctx, tx, projection); err != nil {
		return fmt.Errorf("insert antipattern projection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit promotion tx: %w", err)
	}
	return nil
}

func antiPatternExistsForSourceTx(ctx context.Context, tx *sql.Tx, sourceID string) (bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM   forest_branches
		WHERE  family = ? AND parent_id = ?
		LIMIT  1
	`, string(TreeFamilyAntiPattern), sourceID)
	var found int
	switch err := row.Scan(&found); {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("check antipattern existence: %w", err)
	}
}

// buildAntiPatternProjection composes the AntiPattern branch from a
// source. The AntiPattern is a sibling — same RootID, parent_id =
// source.id — with Family=AntiPattern, Scope=Semantic (cross-session
// durable signal), and salience scaled to the source's failure
// volume. Counters reset to zero on the projection — agents shouldn't
// stack new failures into the AntiPattern itself.
func buildAntiPatternProjection(source *Branch, now time.Time) *Branch {
	salience := computeAntiPatternSalience(source)
	titleSuffix := strings.TrimSpace(source.Title)
	if titleSuffix == "" {
		titleSuffix = source.ID
	}
	summary := fmt.Sprintf("Failed %d times of %d attempts. Source: %s",
		source.FailureCount, source.SuccessCount+source.FailureCount, source.Summary)

	metadata := map[string]any{
		"source_branch_id":             source.ID,
		"source_failure_count":         source.FailureCount,
		"source_success_count":         source.SuccessCount,
		"promoted_at":                  now.Unix(),
		"source_failure_rate_promoted": failureRate(source),
	}

	return &Branch{
		ID:             stableID("antipattern", source.ID),
		RootID:         source.RootID,
		ParentID:       source.ID,
		Family:         TreeFamilyAntiPattern,
		Scope:          ScopeSemantic,
		State:          BranchStateActive,
		SessionID:      "",
		TaskID:         "",
		AgentID:        source.AgentID,
		AgentType:      source.AgentType,
		IntentID:       source.IntentID,
		Title:          "AntiPattern: " + titleSuffix,
		Summary:        summary,
		Confidence:     source.Confidence,
		Salience:       salience,
		Utility:        0,
		SuccessRate:    0,
		ScopeRisk:      source.ScopeRisk,
		ConflictScore:  source.ConflictScore,
		SupportCount:   0,
		CounterCount:   0,
		SuccessCount:   0,
		FailureCount:   0,
		AccessCount:    0,
		LastAccessedAt: now,
		CreatedAt:      now,
		UpdatedAt:      now,
		Metadata:       metadata,
	}
}

// computeAntiPatternSalience scales source salience by the volume of
// negative evidence, soft-capped at the saliencePivot so a runaway
// failure count doesn't produce a salience > 1.
func computeAntiPatternSalience(source *Branch) float64 {
	base := source.Salience
	if base <= 0 {
		base = 0.5
	}
	weight := float64(source.FailureCount) / antiPatternSaliencePivot
	if weight > 1 {
		weight = 1
	}
	salience := base*0.5 + weight*0.5
	if salience > 1 {
		salience = 1
	}
	return salience
}

// failureRate is failures / (failures + successes), bounded to [0,1].
// Returns 0 for branches with no observations to avoid surfacing
// branches that have never run at all.
func failureRate(source *Branch) float64 {
	total := source.FailureCount + source.SuccessCount
	if total <= 0 {
		return 0
	}
	r := float64(source.FailureCount) / float64(total)
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}
