package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #9 Phase A — Health surface
// ────────────────────────────────────────────────────────────────────
//
// Operator-facing read surface that exposes the structural and
// operational state of the forest in one snapshot. Every probe is
// a bounded SQL read; every result is bounds-checked in Go; no
// goroutines spawned, no panic surface.
//
// Status aggregation (see classifyHealthStatus): the snapshot's
// overall Status is the worst of (subsystem statuses, schema-drift,
// spot-check mismatch rate, latency thresholds).

// HealthStatus is the operational tier of a subsystem (or the
// aggregate forest). Higher severity wins in aggregation.
type HealthStatus string

const (
	// HealthStatusOK — subsystem operating within expected bounds.
	HealthStatusOK HealthStatus = "ok"
	// HealthStatusDegraded — recent error / latency spike / lag, but
	// still serving. Operators investigate at leisure.
	HealthStatusDegraded HealthStatus = "degraded"
	// HealthStatusUnhealthy — subsystem halted, schema drift, or
	// projection corruption. Action required.
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

// HealthSnapshot is the top-level result of Health(). Captures
// per-subsystem status, schema integrity, ledger spot-checks, and
// latency percentiles. One Health() call ≈ a handful of indexed
// SQL reads — call as often as operator polling cadence demands.
type HealthSnapshot struct {
	Status         HealthStatus      `json:"status"`
	Subsystems     []SubsystemHealth `json:"subsystems"`
	Schema         SchemaHealth      `json:"schema"`
	SpotChecks     SpotCheckResults  `json:"spot_checks"`
	LatencyP50µs   int64             `json:"latency_p50_micros"`
	LatencyP95µs   int64             `json:"latency_p95_micros"`
	LatencyP99µs   int64             `json:"latency_p99_micros"`
	LatencySamples int64             `json:"latency_samples"`
	GeneratedAt    time.Time         `json:"generated_at"`
}

// SubsystemHealth is per-subsystem operational state. Most fields
// are optional — Extras carries subsystem-specific data that doesn't
// fit the common shape (e.g., page-rank cache occupancy).
type SubsystemHealth struct {
	Name           string         `json:"name"`
	Status         HealthStatus   `json:"status"`
	LastAppliedSeq int64          `json:"last_applied_seq,omitempty"`
	LastError      string         `json:"last_error,omitempty"`
	LastErrorAt    time.Time      `json:"last_error_at,omitempty"`
	LeaseHolder    string         `json:"lease_holder,omitempty"`
	LeaseUntil     time.Time      `json:"lease_until,omitempty"`
	QueueDepth     int            `json:"queue_depth,omitempty"`
	InFlight       int64          `json:"in_flight,omitempty"`
	Extras         map[string]any `json:"extras,omitempty"`
}

// SchemaHealth reports schema-version integrity. ExpectedHash is
// what this build's expectedSchemaHash() computes; AppliedHash is
// what the latest forest_schema_versions row records. Mismatch =
// drift (DB migrated by a different build).
type SchemaHealth struct {
	Status          HealthStatus `json:"status"`
	ExpectedHash    string       `json:"expected_hash"`
	AppliedHash     string       `json:"applied_hash"`
	AppliedAt       time.Time    `json:"applied_at,omitempty"`
	MissingTriggers []string     `json:"missing_triggers,omitempty"`
}

// SpotCheckResults captures the ledger-vs-projection consistency
// sample. Mismatched > 0 indicates corrupted projections — operator
// investigates which branches and re-replays from forest_events.
type SpotCheckResults struct {
	Status     HealthStatus     `json:"status"`
	Sampled    int              `json:"sampled"`
	Mismatched int              `json:"mismatched"`
	Mismatches []SpotCheckIssue `json:"mismatches,omitempty"`
}

// SpotCheckIssue is one branch whose persisted counters disagree
// with what we re-derive from forest_events. The Detail field
// names which counter mismatched.
type SpotCheckIssue struct {
	BranchID string `json:"branch_id"`
	Detail   string `json:"detail"`
}

const (
	// healthLatencyWindow bounds the latency-percentile sample to
	// recent retrievals. 1h captures enough volume in any
	// reasonably-active forest without scanning the full ledger.
	healthLatencyWindow = 1 * time.Hour

	// healthLatencySampleCap caps the sample count so a busy session
	// doesn't make Health() expensive.
	healthLatencySampleCap = 5000

	// healthSpotCheckSize is the default count of branches randomly
	// sampled for the projection-consistency check.
	healthSpotCheckSize = 32

	// healthRecentErrorWindow is the age threshold below which a
	// projector_state.last_error_at is considered "fresh enough to
	// degrade overall status."
	healthRecentErrorWindow = 5 * time.Minute

	// healthSpotCheckMismatchDegradeThreshold is the fraction of
	// sampled branches with mismatched counters above which we
	// declare the spot-check status degraded.
	healthSpotCheckMismatchDegradeThreshold = 0.05

	// healthSpotCheckMismatchUnhealthyThreshold escalates to
	// unhealthy at this rate or higher.
	healthSpotCheckMismatchUnhealthyThreshold = 0.20

	// healthLatencyDegradeP99 / healthLatencyUnhealthyP99 are absolute
	// p99 ceilings for the operational tiers.
	healthLatencyDegradeP99   = 500_000   // 0.5s
	healthLatencyUnhealthyP99 = 2_000_000 // 2s
)

// Health composes the per-subsystem probes, schema verifier, spot-
// checker, and latency percentiles into one snapshot. Every step is
// a bounded read that returns its own error → status mapping rather
// than aborting the whole probe; partial Health() data is more
// useful to operators than no data at all.
func (m *MemoryForest) Health(ctx context.Context) HealthSnapshot {
	if m == nil {
		return HealthSnapshot{
			Status:      HealthStatusUnhealthy,
			GeneratedAt: time.Now().UTC(),
		}
	}
	snapshot := HealthSnapshot{
		GeneratedAt: time.Now().UTC(),
	}
	snapshot.Subsystems = m.probeAllSubsystems(ctx)
	snapshot.Schema = m.probeSchemaHealth(ctx)
	snapshot.SpotChecks = m.runSpotChecks(ctx, healthSpotCheckSize)
	snapshot.LatencyP50µs, snapshot.LatencyP95µs, snapshot.LatencyP99µs, snapshot.LatencySamples = m.computeLatencyPercentiles(ctx)
	snapshot.Status = aggregateHealthStatus(snapshot)
	return snapshot
}

// aggregateHealthStatus picks the worst tier across every input
// signal. unhealthy > degraded > ok.
func aggregateHealthStatus(s HealthSnapshot) HealthStatus {
	worst := HealthStatusOK
	worsen := func(candidate HealthStatus) {
		if healthStatusSeverity(candidate) > healthStatusSeverity(worst) {
			worst = candidate
		}
	}
	for _, sub := range s.Subsystems {
		worsen(sub.Status)
	}
	worsen(s.Schema.Status)
	worsen(s.SpotChecks.Status)
	if s.LatencyP99µs >= healthLatencyUnhealthyP99 && s.LatencySamples > 0 {
		worsen(HealthStatusUnhealthy)
	} else if s.LatencyP99µs >= healthLatencyDegradeP99 && s.LatencySamples > 0 {
		worsen(HealthStatusDegraded)
	}
	return worst
}

// healthStatusSeverity assigns a sortable rank to each tier so the
// aggregator can pick "worst." Higher rank = worse.
func healthStatusSeverity(s HealthStatus) int {
	switch s {
	case HealthStatusUnhealthy:
		return 2
	case HealthStatusDegraded:
		return 1
	}
	return 0
}

// classifyProjectorStatus maps a projector_state row into a
// HealthStatus tier. Halted = unhealthy. Recent error = degraded.
// Else ok.
func classifyProjectorStatus(healthCol string, lastErrorAt time.Time, now time.Time) HealthStatus {
	if healthCol == string(ProjectorHealthHalted) {
		return HealthStatusUnhealthy
	}
	if !lastErrorAt.IsZero() && now.Sub(lastErrorAt) < healthRecentErrorWindow {
		return HealthStatusDegraded
	}
	return HealthStatusOK
}

// probeAllSubsystems issues one row-per-subsystem read against
// forest_projector_state for the lease-coordinated subsystems +
// embeds in-process state for the queue-driven (audit drainer) and
// cache-driven (page rank) ones.
func (m *MemoryForest) probeAllSubsystems(ctx context.Context) []SubsystemHealth {
	now := time.Now().UTC()
	leaseSubsystems := []string{
		projectorBranchName,
		projectorRetrievalCandidatesName,
		projectorAntiPatternName,
		projectorRetrievalCandidatesPrunerName,
		projectorBaseScoreTrainerName,
	}
	out := make([]SubsystemHealth, 0, len(leaseSubsystems)+3)
	for _, name := range leaseSubsystems {
		out = append(out, m.probeProjectorRow(ctx, name, now))
	}
	out = append(out, m.probeRuntime())
	out = append(out, m.probeAuditDrainer())
	out = append(out, m.probePageRankCache())
	out = append(out, m.probeSubstrateState(ctx, now))
	return out
}

func (m *MemoryForest) probeRuntime() SubsystemHealth {
	snapshot := m.RuntimeSnapshot()
	sub := SubsystemHealth{
		Name:   "forest_runtime",
		Status: HealthStatusOK,
		Extras: map[string]any{
			"workers": len(snapshot.Workers),
			"queues":  len(snapshot.Queues),
			"tickers": len(snapshot.Tickers),
			"leases":  len(snapshot.Leases),
		},
	}
	if snapshot.TimeoutError != "" {
		worsenSubsystemStatus(&sub, HealthStatusUnhealthy)
		sub.LastError = snapshot.TimeoutError
		sub.LastErrorAt = snapshot.TimeoutAt
	}
	for _, worker := range snapshot.Workers {
		if worker.Status == RuntimeWorkerPanicked || worker.Status == RuntimeWorkerErrored {
			worsenSubsystemStatus(&sub, HealthStatusUnhealthy)
			if sub.LastError == "" {
				sub.LastError = worker.Name + ": " + worker.LastError
				sub.LastErrorAt = worker.LastErrorAt
			}
		}
	}
	var overflows uint64
	for _, queue := range snapshot.Queues {
		overflows += queue.Overflows
	}
	if overflows > 0 {
		worsenSubsystemStatus(&sub, HealthStatusDegraded)
		sub.Extras["queue_overflows"] = overflows
	}
	return sub
}

// probeProjectorRow reads one forest_projector_state row and maps
// it to SubsystemHealth. A missing row is treated as "subsystem not
// yet started" (ok with empty fields), not an error.
func (m *MemoryForest) probeProjectorRow(ctx context.Context, name string, now time.Time) SubsystemHealth {
	row := m.db.QueryRowContext(ctx, `
		SELECT last_applied_seq, last_applied_at, leader_holder,
		       leader_lease_until, health_status, last_error,
		       last_error_at
		FROM   forest_projector_state
		WHERE  projector_name = ?
	`, name)
	var (
		lastApplied   int64
		lastAppliedAt int64
		leaseHolder   sql.NullString
		leaseUntil    int64
		healthCol     sql.NullString
		lastError     sql.NullString
		lastErrorAt   int64
	)
	err := row.Scan(&lastApplied, &lastAppliedAt, &leaseHolder, &leaseUntil, &healthCol, &lastError, &lastErrorAt)
	switch {
	case err == nil:
		return SubsystemHealth{
			Name:           name,
			Status:         classifyProjectorStatus(healthCol.String, time.Unix(lastErrorAt, 0).UTC(), now),
			LastAppliedSeq: lastApplied,
			LastError:      lastError.String,
			LastErrorAt:    time.Unix(lastErrorAt, 0).UTC(),
			LeaseHolder:    leaseHolder.String,
			LeaseUntil:     time.Unix(leaseUntil, 0).UTC(),
		}
	case errors.Is(err, sql.ErrNoRows):
		return SubsystemHealth{Name: name, Status: HealthStatusOK}
	default:
		return SubsystemHealth{
			Name:      name,
			Status:    HealthStatusDegraded,
			LastError: fmt.Sprintf("probe failed: %s", err.Error()),
		}
	}
}

// probeAuditDrainer reports the in-memory audit drainer state. No
// SQL — purely in-process counters + queue introspection. Status
// is monotonic: each signal can only worsen the tier, never improve
// it. Without that, "queue 80% full" would silently downgrade the
// "drainer closed" tier from unhealthy back to degraded.
func (m *MemoryForest) probeAuditDrainer() SubsystemHealth {
	sub := SubsystemHealth{Name: "retrieval_audit_drainer", Status: HealthStatusOK}
	if m.retrievalAuditQueue == nil {
		// Synchronous-projection mode: no drainer goroutine.
		sub.Extras = map[string]any{"mode": "synchronous"}
		return sub
	}
	sub.QueueDepth = len(m.retrievalAuditQueue)
	sub.InFlight = m.retrievalAuditInFlight.Load()
	if m.retrievalAuditClosed.Load() {
		worsenSubsystemStatus(&sub, HealthStatusUnhealthy)
		sub.LastError = "audit drainer closed"
	}
	if cap(m.retrievalAuditQueue) > 0 {
		fill := float64(len(m.retrievalAuditQueue)) / float64(cap(m.retrievalAuditQueue))
		if fill > 0.8 {
			worsenSubsystemStatus(&sub, HealthStatusDegraded)
		}
	}
	return sub
}

// worsenSubsystemStatus updates a subsystem's Status to `candidate`
// only when candidate is strictly worse than the current Status.
// Prevents downgrades when multiple signals fire on the same
// subsystem (e.g., drainer closed + queue near-full).
func worsenSubsystemStatus(sub *SubsystemHealth, candidate HealthStatus) {
	if sub == nil {
		return
	}
	if healthStatusSeverity(candidate) > healthStatusSeverity(sub.Status) {
		sub.Status = candidate
	}
}

// probePageRankCache embeds PageRankCacheStats output as a
// subsystem entry.
func (m *MemoryForest) probePageRankCache() SubsystemHealth {
	stats := m.PageRankCacheStats()
	return SubsystemHealth{
		Name:   "page_rank_cache",
		Status: HealthStatusOK,
		Extras: map[string]any{
			"session_count": stats.SessionCount,
			"capacity":      stats.Capacity,
		},
	}
}

// probeSubstrateState counts dirty + ready sessions in
// forest_substrate_sessions so operators can see whether the
// substrate maintenance loop is keeping up.
func (m *MemoryForest) probeSubstrateState(ctx context.Context, now time.Time) SubsystemHealth {
	row := m.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN dirty = 1 THEN 1 ELSE 0 END), 0) AS dirty_count,
			COALESCE(SUM(CASE WHEN dirty = 0 THEN 1 ELSE 0 END), 0) AS clean_count,
			COALESCE(MAX(dirty_at), 0)
		FROM forest_substrate_sessions
	`)
	var dirty, clean, maxDirtyAt int64
	if err := row.Scan(&dirty, &clean, &maxDirtyAt); err != nil {
		return SubsystemHealth{
			Name:      "substrate",
			Status:    HealthStatusDegraded,
			LastError: fmt.Sprintf("substrate probe: %s", err.Error()),
		}
	}
	status := HealthStatusOK
	if maxDirtyAt > 0 && now.Sub(time.Unix(maxDirtyAt, 0).UTC()) > 5*time.Minute {
		status = HealthStatusDegraded
	}
	return SubsystemHealth{
		Name:   "substrate",
		Status: status,
		Extras: map[string]any{
			"dirty_count":  dirty,
			"clean_count":  clean,
			"max_dirty_at": time.Unix(maxDirtyAt, 0).UTC(),
		},
	}
}

// ────────────────────────────────────────────────────────────────────
// Schema verifier
// ────────────────────────────────────────────────────────────────────

// expectedAppendOnlyTriggers enumerates every append-only trigger
// this build expects to find in sqlite_master. Used by
// probeSchemaHealth to detect missing triggers (a migration that
// failed mid-run, or a DB attached from another build).
var expectedAppendOnlyTriggers = []string{
	"forest_events_no_update",
	"forest_events_no_delete",
	"forest_ledger_no_update",
	"forest_ledger_no_delete",
	"forest_ledger_payloads_no_update",
	"forest_ledger_payloads_no_delete",
	"forest_ledger_refs_no_update",
	"forest_ledger_refs_no_delete",
	"forest_ledger_delivery_no_update",
	"forest_ledger_delivery_no_delete",
	"forest_retrieval_events_no_update",
	"forest_retrieval_events_no_delete",
	"forest_events_archive_no_update",
	"forest_events_archive_no_delete",
	"forest_retrieval_events_archive_no_update",
	"forest_retrieval_events_archive_no_delete",
}

// probeSchemaHealth compares the recorded schema hash against the
// expected hash and verifies all required triggers exist. Drift =
// unhealthy. Missing triggers = unhealthy.
func (m *MemoryForest) probeSchemaHealth(ctx context.Context) SchemaHealth {
	expected := expectedSchemaHash()
	out := SchemaHealth{
		Status:       HealthStatusOK,
		ExpectedHash: expected,
	}
	row := m.db.QueryRowContext(ctx, `
		SELECT schema_hash, applied_at
		FROM   forest_schema_versions
		ORDER BY version DESC
		LIMIT  1
	`)
	var (
		appliedHash string
		appliedAt   int64
	)
	switch err := row.Scan(&appliedHash, &appliedAt); {
	case err == nil:
		out.AppliedHash = appliedHash
		out.AppliedAt = time.Unix(appliedAt, 0).UTC()
	case errors.Is(err, sql.ErrNoRows):
		// First-time install with no recorded version. The
		// migration step in ensureSchema is supposed to write one;
		// if it's missing now, schema integrity is suspect.
		out.Status = HealthStatusDegraded
	default:
		out.Status = HealthStatusDegraded
		return out
	}
	if out.AppliedHash != "" && out.AppliedHash != expected {
		out.Status = HealthStatusUnhealthy
	}
	missing, err := m.findMissingTriggers(ctx)
	if err != nil {
		out.Status = HealthStatusDegraded
	}
	if len(missing) > 0 {
		out.MissingTriggers = missing
		out.Status = HealthStatusUnhealthy
	}
	return out
}

// findMissingTriggers queries sqlite_master and returns any
// expectedAppendOnlyTriggers that aren't present.
func (m *MemoryForest) findMissingTriggers(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT name FROM sqlite_master WHERE type = 'trigger'
	`)
	if err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	defer rows.Close()
	present := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan trigger: %w", err)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var missing []string
	for _, expected := range expectedAppendOnlyTriggers {
		if _, ok := present[expected]; !ok {
			missing = append(missing, expected)
		}
	}
	return missing, nil
}

// ────────────────────────────────────────────────────────────────────
// Spot checks (ledger ↔ projection consistency)
// ────────────────────────────────────────────────────────────────────

// runSpotChecks samples up to `sampleSize` branches uniformly at
// random and re-derives their counters from forest_events. Mismatches
// indicate projection corruption (a branch projector applied an
// event but didn't update one of the projected counters; or the
// projection was hand-edited).
func (m *MemoryForest) runSpotChecks(ctx context.Context, sampleSize int) SpotCheckResults {
	if sampleSize <= 0 {
		sampleSize = healthSpotCheckSize
	}
	branches, err := m.sampleBranchesForSpotCheck(ctx, sampleSize)
	if err != nil {
		return SpotCheckResults{Status: HealthStatusDegraded}
	}
	out := SpotCheckResults{
		Status:  HealthStatusOK,
		Sampled: len(branches),
	}
	for i := range branches {
		issue, ok := m.spotCheckOneBranch(ctx, &branches[i])
		if !ok {
			continue
		}
		out.Mismatched++
		// Cap reported mismatches at a reasonable number so the
		// snapshot doesn't blow up if every branch is bad.
		if len(out.Mismatches) < 16 {
			out.Mismatches = append(out.Mismatches, issue)
		}
	}
	if out.Sampled > 0 {
		rate := float64(out.Mismatched) / float64(out.Sampled)
		switch {
		case rate >= healthSpotCheckMismatchUnhealthyThreshold:
			out.Status = HealthStatusUnhealthy
		case rate >= healthSpotCheckMismatchDegradeThreshold:
			out.Status = HealthStatusDegraded
		}
	}
	return out
}

// spotCheckBranch is the trimmed projection of a sampled branch
// that the spot-checker compares against re-derived counters.
type spotCheckBranch struct {
	id           string
	supportCount int
	counterCount int
}

// sampleBranchesForSpotCheck picks `n` random non-superseded branches.
// Uses ORDER BY RANDOM() + LIMIT — fine for `n` in the tens, far
// below SQLite's threshold for that pattern's cost.
func (m *MemoryForest) sampleBranchesForSpotCheck(ctx context.Context, n int) ([]spotCheckBranch, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, support_count, counter_count
		FROM   forest_branches
		WHERE  state != ?
		ORDER BY RANDOM()
		LIMIT  ?
	`, string(BranchStateSuperseded), n)
	if err != nil {
		return nil, fmt.Errorf("sample branches: %w", err)
	}
	defer rows.Close()
	var out []spotCheckBranch
	for rows.Next() {
		var b spotCheckBranch
		if err := rows.Scan(&b.id, &b.supportCount, &b.counterCount); err != nil {
			return nil, fmt.Errorf("scan sample branch: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// spotCheckOneBranch re-derives expected support_count + counter_count
// from forest_events and compares them to the persisted projection.
// Returns (issue, true) if either counter mismatches; (zero, false)
// otherwise.
//
// support_count derivation: every event_type other than five
// non-incrementing cases (contradiction, ecology_pruned,
// ecology_regrown, replay_promoted, replay_consolidated) increments
// support in the branch projector. The replay event types mutate
// scope + utility but deliberately do NOT touch the support
// counter (see service.go applyEvent — replay cases skip the
// SupportCount++ that the default branch performs).
//
// counter_count derivation: contradiction events only.
//
// success_count / failure_count are not spot-checked here because
// they require parsing the outcome payload's status field, which
// involves either json1 (a SQLite extension we don't depend on) or
// per-row Go-side decode (more I/O than the spot-check budget). A
// later phase can extend the spot-check to those columns once we
// have a payload parser surface.
func (m *MemoryForest) spotCheckOneBranch(ctx context.Context, b *spotCheckBranch) (SpotCheckIssue, bool) {
	row := m.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE
				WHEN event_kind NOT IN (?, ?, ?, ?, ?) THEN 1 ELSE 0 END), 0) AS support_count,
			COALESCE(SUM(CASE
				WHEN event_kind = ? THEN 1 ELSE 0 END), 0) AS counter_count
		FROM   forest_ledger
		WHERE  source_kind = ?
		  AND  subject_type = 'branch'
		  AND  subject_id = ?
	`,
		string(EventTypeContradiction),
		string(EventTypeEcologyPruned),
		string(EventTypeEcologyRegrown),
		string(EventTypeReplayPromoted),
		string(EventTypeReplayConsolidated),
		string(EventTypeContradiction),
		string(LedgerSourceForestEvent),
		b.id,
	)
	var derivedSupport, derivedCounter int
	if err := row.Scan(&derivedSupport, &derivedCounter); err != nil {
		// Read failure → undeterminable for this branch. Don't
		// flag as mismatch; the next spot-check sample will pick
		// up other branches.
		return SpotCheckIssue{}, false
	}
	mismatches := make([]string, 0, 2)
	if derivedSupport != b.supportCount {
		mismatches = append(mismatches, fmt.Sprintf("support_count: persisted=%d derived=%d", b.supportCount, derivedSupport))
	}
	if derivedCounter != b.counterCount {
		mismatches = append(mismatches, fmt.Sprintf("counter_count: persisted=%d derived=%d", b.counterCount, derivedCounter))
	}
	if len(mismatches) == 0 {
		return SpotCheckIssue{}, false
	}
	return SpotCheckIssue{
		BranchID: b.id,
		Detail:   strings.Join(mismatches, "; "),
	}, true
}

// ────────────────────────────────────────────────────────────────────
// Latency percentiles
// ────────────────────────────────────────────────────────────────────

// computeLatencyPercentiles samples recent retrieval audit events
// and returns p50/p95/p99 of duration_micros + the sample count.
// Bounded by healthLatencyWindow + healthLatencySampleCap.
func (m *MemoryForest) computeLatencyPercentiles(ctx context.Context) (int64, int64, int64, int64) {
	since := time.Now().UTC().Add(-healthLatencyWindow).Unix()
	rows, err := m.db.QueryContext(ctx, `
		SELECT duration_micros
		FROM   forest_retrieval_events
		WHERE  requested_at >= ?
		ORDER BY requested_at DESC
		LIMIT  ?
	`, since, healthLatencySampleCap)
	if err != nil {
		return 0, 0, 0, 0
	}
	defer rows.Close()
	samples := make([]int64, 0, healthLatencySampleCap)
	for rows.Next() {
		var d int64
		if err := rows.Scan(&d); err != nil {
			return 0, 0, 0, 0
		}
		if d < 0 {
			continue
		}
		samples = append(samples, d)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, 0
	}
	if len(samples) == 0 {
		return 0, 0, 0, 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return percentileOfSorted(samples, 0.50),
		percentileOfSorted(samples, 0.95),
		percentileOfSorted(samples, 0.99),
		int64(len(samples))
}

// percentileOfSorted picks the value at index (q · len) using
// nearest-rank — simple, well-defined, no interpolation.
// Bounds-checked: returns 0 on empty input.
func percentileOfSorted(samples []int64, q float64) int64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	idx := int(q * float64(n))
	if idx >= n {
		idx = n - 1
	}
	if idx < 0 {
		idx = 0
	}
	return samples[idx]
}
