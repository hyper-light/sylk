package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #5 — Cold-start fallback + session priming
// ────────────────────────────────────────────────────────────────────
//
// Two mechanisms address the "empty forest on new session" failure
// mode:
//
//   - Global priors fallback: when the primary retrieval pipeline
//     returns fewer than `Limit` packets, we backfill from a
//     forest-wide ranking by (success_rate * salience) scoped to the
//     query's family and (when supplied) agent_type. Filled packets
//     are tagged Source=GlobalPrior so the audit trail captures how
//     often the forest is in cold-start mode.
//
//   - PrimeSession: copies the most-active branches from the most
//     recent prior session of the same agent_type into the new
//     session's canopy. Idempotent; safe to call multiple times.

// fillWithGlobalPriors backfills `packets` up to `limit` with
// global-prior branches when primary retrieval is short. Returns the
// extended packet slice. The primary set is preserved at the front;
// global priors are appended after it.
//
// Best-effort: a query failure logs and returns the input unchanged.
// Cold-start is observational; a failure shouldn't break retrieval.
func (m *MemoryForest) fillWithGlobalPriors(ctx context.Context, query Query, packets []*BranchPacket, limit int) []*BranchPacket {
	if m == nil || limit <= 0 || len(packets) >= limit {
		return packets
	}
	missing := limit - len(packets)
	excluded := branchIDSet(packets)

	priors, err := m.loadGlobalPriors(ctx, query, missing, excluded)
	if err != nil {
		if m.logger != nil {
			m.logger.Debug("forest: global priors load failed", "err", err.Error())
		}
		return packets
	}
	if len(priors) == 0 {
		return packets
	}
	for _, branch := range priors {
		packets = append(packets, &BranchPacket{
			Branch: branch,
			Score: PacketScore{
				Total:    branch.Salience * branch.SuccessRate,
				Base:     branch.Salience * branch.SuccessRate,
				Salience: branch.Salience,
			},
			Source: RetrievalSourceGlobalPrior,
		})
	}
	return packets
}

// loadGlobalPriors returns up to `limit` branches ordered by
// success_rate * salience DESC, scoped by family + agent_type when
// the query supplies them. Excluded IDs are filtered out so the
// fallback never produces duplicates of the primary set.
//
// agent_type is matched permissively: branches authored by the
// requested agent_type AND branches with empty agent_type (global
// branches, used for catalog primers) are both eligible. This keeps
// truly empty forests from returning nothing.
func (m *MemoryForest) loadGlobalPriors(ctx context.Context, query Query, limit int, excluded map[string]struct{}) ([]*Branch, error) {
	clauses := []string{"state != ?"}
	args := []any{string(BranchStateSuperseded)}

	if len(query.Families) > 0 {
		placeholders := make([]string, 0, len(query.Families))
		for _, family := range query.Families {
			placeholders = append(placeholders, "?")
			args = append(args, string(family))
		}
		clauses = append(clauses, "family IN ("+strings.Join(placeholders, ",")+")")
	}
	if at := normalizeRequesterAgentType(query.AgentType); at != "" {
		clauses = append(clauses, "(agent_type = ? OR agent_type = '')")
		args = append(args, at)
	}
	clauses = append(clauses, "support_count > 0")

	q := `
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata,
		       last_applied_seq, constraint_severity
		FROM   forest_branches
		WHERE  ` + strings.Join(clauses, " AND ") + `
		ORDER BY (success_rate * salience) DESC, salience DESC, updated_at DESC
		LIMIT  ?
	`
	args = append(args, limit+len(excluded))

	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query global priors: %w", err)
	}
	defer rows.Close()

	out := make([]*Branch, 0, limit)
	for rows.Next() {
		branch, scanErr := scanBranch(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if branch == nil {
			continue
		}
		if _, dup := excluded[branch.ID]; dup {
			continue
		}
		out = append(out, branch)
		if len(out) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate global priors: %w", err)
	}
	return out, nil
}

// branchIDSet collects the branch IDs already present on a packet
// slice. nil-tolerant.
func branchIDSet(packets []*BranchPacket) map[string]struct{} {
	out := make(map[string]struct{}, len(packets))
	for _, p := range packets {
		if p == nil || p.Branch == nil {
			continue
		}
		out[p.Branch.ID] = struct{}{}
	}
	return out
}

// tagPrimaryRetrievalSource stamps Source=Primary on every packet
// whose Source is still empty. Called after the primary scoring
// pipeline finishes; subsequent fillWithGlobalPriors /
// reserveAntiPrecedentSlots stamp their own values, so the dispatch
// is once-per-packet and preserves prior tagging.
func tagPrimaryRetrievalSource(packets []*BranchPacket) {
	for _, p := range packets {
		if p == nil {
			continue
		}
		if p.Source == "" {
			p.Source = RetrievalSourcePrimary
		}
	}
}

// ────────────────────────────────────────────────────────────────────
// PrimeSession
// ────────────────────────────────────────────────────────────────────

// SessionPrimingRequest carries the inputs to PrimeSession. AgentType
// scopes the prior-session lookup; Family filters which branches
// migrate. Limit caps the number of branches copied (default
// defaultSessionPrimingLimit).
type SessionPrimingRequest struct {
	SessionID string
	AgentType string
	Families  []TreeFamily
	Horizon   CanopyHorizon
	Limit     int
}

// SessionPrimingResult reports what PrimeSession copied.
type SessionPrimingResult struct {
	SourceSessionID string
	BranchesCopied  int
	CanopyKey       string
}

const (
	// defaultSessionPrimingLimit caps how many high-activity branches
	// are copied from a prior session into the new canopy. Bounded so
	// a noisy prior doesn't blow up the new session's canopy.
	defaultSessionPrimingLimit = 16

	// sessionPrimingMaxLimit prevents a caller-supplied Limit from
	// requesting an unreasonable number.
	sessionPrimingMaxLimit = 64
)

// errPrimingNoPriorSession is returned by PrimeSession when no prior
// session of the matching agent_type exists. Callers can ignore this
// — it's expected on day-zero of a brand-new agent type.
var errPrimingNoPriorSession = errors.New("no prior session found for priming")

// PrimeSession copies the top-N most-active branches from the most-
// recent prior session of the same agent_type into the new session's
// canopy. Idempotent — running it twice for the same target session
// is safe (the second canopy upsert overwrites the first with the
// same result, no duplicate branches inserted).
//
// One transaction wraps the canopy upsert so a partial prime never
// lands. The branch-row migration is implicit: branches already exist
// in forest_branches; PrimeSession just creates a new canopy that
// references them as RootIDs. No event projection, no warmth
// reinforcement — agents have to act on the canopy for usage signals
// to land.
func (m *MemoryForest) PrimeSession(ctx context.Context, req SessionPrimingRequest) (SessionPrimingResult, error) {
	var result SessionPrimingResult
	if m == nil {
		return result, errors.New("forest is nil")
	}
	if req.SessionID == "" {
		return result, errors.New("priming session id is required")
	}
	limit := resolveSessionPrimingLimit(req.Limit)
	horizon := req.Horizon
	if horizon == "" {
		horizon = CanopyHorizonSession
	}

	source, err := m.findRecentPriorSession(ctx, req.SessionID, normalizeRequesterAgentType(req.AgentType))
	if err != nil {
		return result, err
	}
	if source == "" {
		return result, errPrimingNoPriorSession
	}
	result.SourceSessionID = source

	rootIDs, err := m.loadPrimingRootIDs(ctx, source, req.Families, limit)
	if err != nil {
		return result, err
	}
	if len(rootIDs) == 0 {
		return result, nil
	}

	canopyKey := primingCanopyKey(req.SessionID, horizon)
	if err := m.upsertPrimingCanopy(ctx, canopyKey, req.SessionID, horizon, rootIDs); err != nil {
		return result, err
	}
	result.BranchesCopied = len(rootIDs)
	result.CanopyKey = canopyKey
	return result, nil
}

func resolveSessionPrimingLimit(limit int) int {
	if limit <= 0 {
		return defaultSessionPrimingLimit
	}
	if limit > sessionPrimingMaxLimit {
		return sessionPrimingMaxLimit
	}
	return limit
}

// findRecentPriorSession locates the most-recent session_id (by max
// branch updated_at) authored by the same agent_type, excluding the
// target session. agent_type=="" matches any session — used when the
// caller doesn't know the agent_type ahead of time.
func (m *MemoryForest) findRecentPriorSession(ctx context.Context, targetSessionID, agentType string) (string, error) {
	clauses := []string{"session_id != ?", "session_id != ''"}
	args := []any{targetSessionID}
	if agentType != "" {
		clauses = append(clauses, "(agent_type = ? OR agent_type = '')")
		args = append(args, agentType)
	}
	q := `
		SELECT session_id
		FROM   forest_branches
		WHERE  ` + strings.Join(clauses, " AND ") + `
		GROUP BY session_id
		ORDER BY MAX(updated_at) DESC
		LIMIT  1
	`
	row := m.db.QueryRowContext(ctx, q, args...)
	var sessionID string
	switch err := row.Scan(&sessionID); {
	case err == nil:
		return sessionID, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	default:
		return "", fmt.Errorf("find prior session: %w", err)
	}
}

// loadPrimingRootIDs returns up to `limit` branch IDs from the source
// session, ordered by (success_rate * access_count) DESC. These
// become the RootIDs of the new canopy.
func (m *MemoryForest) loadPrimingRootIDs(ctx context.Context, sourceSessionID string, families []TreeFamily, limit int) ([]string, error) {
	clauses := []string{"session_id = ?", "state != ?"}
	args := []any{sourceSessionID, string(BranchStateSuperseded)}

	// Canonicalize incoming families so callers passing deprecated
	// values (TreeFamilyDecision/Preference/Capability/Opportunity/
	// Conflict) map to the consolidated families that AppendEvent
	// actually persists post-Phase-3.
	families = canonicalizeFamilies(families)
	if len(families) > 0 {
		placeholders := make([]string, 0, len(families))
		for _, family := range families {
			placeholders = append(placeholders, "?")
			args = append(args, string(family))
		}
		clauses = append(clauses, "family IN ("+strings.Join(placeholders, ",")+")")
	}

	q := `
		SELECT id
		FROM   forest_branches
		WHERE  ` + strings.Join(clauses, " AND ") + `
		ORDER BY (success_rate * access_count) DESC, updated_at DESC
		LIMIT  ?
	`
	args = append(args, limit)

	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("load priming root ids: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan priming root id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// primingCanopyKey builds a deterministic canopy key for a primed
// canopy. Re-running PrimeSession for the same (target, horizon)
// upserts the same canopy row instead of growing a per-call list of
// canopies.
//
// The "primed" segment is also the marker readers use to detect a
// primed canopy — see isPrimedCanopy.
func primingCanopyKey(sessionID string, horizon CanopyHorizon) string {
	return stableID("canopy", "primed", sessionID, string(horizon))
}

// isPrimedCanopy returns true when the canopy was created by
// PrimeSession (vs an active-session canopy built by retrieval).
// stableID is a hash so we can't string-match the "primed" segment
// from the key itself; we rely on the canopy's RootIDs being
// loaded by the priming path. Callers detect primed canopies by
// canopy_key equality with primingCanopyKey for the session.
func isPrimedCanopy(c *Canopy, sessionID string) bool {
	if c == nil || sessionID == "" {
		return false
	}
	return c.Key == primingCanopyKey(sessionID, c.Horizon)
}

// tagPrimedRetrievalSource overrides the Source on packets whose
// branches surfaced from a primed canopy. Runs after
// tagPrimaryRetrievalSource so we promote Primary → Primed for
// matching packets and leave already-tagged sources (Anti-Precedent,
// Global Prior) alone.
//
// "Surfaced from a primed canopy" means the packet's Branch.RootID
// or Branch.ID appears in the canopy's RootIDs. This is best-effort:
// a branch ranked into the result independently of canopy match
// would also be tagged Primed if its RootID happens to be in the
// primed roots — that's the right answer because the agent saw it
// via the primed seed.
func tagPrimedRetrievalSource(packets []*BranchPacket, canopy *Canopy, sessionID string) {
	if !isPrimedCanopy(canopy, sessionID) {
		return
	}
	roots := make(map[string]struct{}, len(canopy.RootIDs))
	for _, id := range canopy.RootIDs {
		roots[id] = struct{}{}
	}
	for _, p := range packets {
		if p == nil || p.Branch == nil {
			continue
		}
		if p.Source != RetrievalSourcePrimary {
			continue
		}
		if _, hit := roots[p.Branch.ID]; hit {
			p.Source = RetrievalSourcePrimed
			continue
		}
		if _, hit := roots[p.Branch.RootID]; hit {
			p.Source = RetrievalSourcePrimed
		}
	}
}

// upsertPrimingCanopy writes the canopy row in one transaction. The
// rootIDs list is JSON-encoded into the existing root_ids column so
// the schema doesn't need to change; the Source field on the canopy
// is implicit in the canopy_key prefix ("canopy:primed:...").
func (m *MemoryForest) upsertPrimingCanopy(ctx context.Context, canopyKey, sessionID string, horizon CanopyHorizon, rootIDs []string) error {
	rootJSON, err := encodePrimingRootIDs(rootIDs)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin priming tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO forest_canopies
		(canopy_key, session_id, task_id, intent_id, horizon, root_ids, summary, updated_at)
		VALUES (?, ?, '', '', ?, ?, ?, ?)
		ON CONFLICT (canopy_key) DO UPDATE SET
			session_id = excluded.session_id,
			horizon    = excluded.horizon,
			root_ids   = excluded.root_ids,
			summary    = excluded.summary,
			updated_at = excluded.updated_at
	`, canopyKey, sessionID, string(horizon), rootJSON, "primed-from-prior-session", now)
	if err != nil {
		return fmt.Errorf("upsert priming canopy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit priming tx: %w", err)
	}
	return nil
}

// encodePrimingRootIDs JSON-encodes the root IDs list so it round-
// trips through the existing forest_canopies.root_ids TEXT column.
// Empty slice → "[]" so the JSON round-trip in retrieval matches the
// standard non-null shape that the canopy reader expects.
func encodePrimingRootIDs(ids []string) (string, error) {
	if len(ids) == 0 {
		return "[]", nil
	}
	encoded := marshalJSON(ids)
	if encoded == "" {
		return "", fmt.Errorf("marshal priming root ids returned empty payload for %d ids", len(ids))
	}
	return encoded, nil
}
