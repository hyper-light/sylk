package forest

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// ────────────────────────────────────────────────────────────────────
// Issue #4 — Diversity reranking + retrieval cooldown
// ────────────────────────────────────────────────────────────────────
//
// Two read-only mechanisms layered on top of the base scoring stage,
// ordered so cooldown shapes the relevance signal that MMR consumes:
//
//	cooldown:    score_i' = score_i * (1 - penalty * recency_i)
//	mmr-rerank:  pick = argmax_c [ λ·score' - (1-λ)·max_s sim(c,s) ]
//
// Both run inside the synchronous Retrieve flow — no goroutines, no
// new state. Cooldown reads from forest_retrieval_candidates (already
// projected). MMR uses the 53-D feature vector each candidate already
// carries.

// applyRetrievalCooldownPenalty scales each packet's Total/Base score
// proportional to how often it appeared in the last
// retrievalCooldownWindow retrievals for the same session. A branch
// that appeared in every recent retrieval is scaled by
// (1 - retrievalCooldownPenalty); a branch that never appeared is
// untouched.
//
// One batched query: identify the most-recent N retrieval events for
// the session, then count per-branch appearances among the candidate
// pool. O(K + N) work in SQL; one round-trip.
//
// Best-effort: a query failure logs a warning and leaves scores
// untouched. Cooldown is observational and shouldn't break retrieval.
func (m *MemoryForest) applyRetrievalCooldownPenalty(ctx context.Context, sessionID string, packets []*BranchPacket) {
	if m == nil || len(packets) == 0 {
		return
	}
	if m.retrievalCooldownPenalty <= 0 || m.retrievalCooldownWindow <= 0 {
		return
	}
	if sessionID == "" {
		return
	}
	branchIDs := make([]string, 0, len(packets))
	for _, p := range packets {
		if p == nil || p.Branch == nil {
			continue
		}
		branchIDs = append(branchIDs, p.Branch.ID)
	}
	if len(branchIDs) == 0 {
		return
	}
	recency, err := m.loadRecentRetrievalRecency(ctx, sessionID, branchIDs, m.retrievalCooldownWindow)
	if err != nil {
		if m.logger != nil {
			m.logger.Debug("forest: retrieval cooldown query failed", "err", err.Error())
		}
		return
	}
	if len(recency) == 0 {
		return
	}
	window := float64(m.retrievalCooldownWindow)
	for _, p := range packets {
		if p == nil || p.Branch == nil {
			continue
		}
		count, ok := recency[p.Branch.ID]
		if !ok || count <= 0 {
			continue
		}
		ratio := float64(count) / window
		if ratio > 1 {
			ratio = 1
		}
		scale := 1 - m.retrievalCooldownPenalty*ratio
		if scale < 0 {
			scale = 0
		}
		p.Score.Total *= scale
		p.Score.Base *= scale
	}
}

// loadRecentRetrievalRecency returns map[branchID] -> count of recent
// retrievals that included the branch. The "recent" set is the last
// `window` retrieval audit events for the session, ordered by seq DESC.
//
// Uses the existing forest_retrieval_event_seq_log + indexed
// forest_retrieval_candidates JOIN; one round-trip. The IN-clause is
// placeholder-bound rather than concatenated to avoid SQL-injection
// surface even though branch IDs are internal.
func (m *MemoryForest) loadRecentRetrievalRecency(ctx context.Context, sessionID string, branchIDs []string, window int) (map[string]int, error) {
	placeholders, args := buildBranchInArgs(sessionID, branchIDs, window)
	// Only count appearances where the candidate was actually
	// returned (rc.returned = 1). Pool-membership doesn't reinforce —
	// the agent didn't see candidates that ranked outside top-K.
	query := `
		SELECT rc.branch_id, COUNT(*) AS hits
		FROM   forest_retrieval_candidates rc
		WHERE  rc.session_id = ?
		  AND  rc.branch_id IN (` + placeholders + `)
		  AND  rc.returned = 1
		  AND  rc.retrieval_event_id IN (
		      SELECT e.id
		      FROM   forest_retrieval_events e
		      JOIN   forest_retrieval_event_seq_log s ON s.event_id = e.id
		      WHERE  e.session_id = ?
		      ORDER BY s.seq DESC
		      LIMIT  ?
		  )
		GROUP BY rc.branch_id
	`
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query retrieval recency: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int, len(branchIDs))
	for rows.Next() {
		var (
			id   string
			hits int
		)
		if err := rows.Scan(&id, &hits); err != nil {
			return nil, fmt.Errorf("scan retrieval recency row: %w", err)
		}
		out[id] = hits
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval recency rows: %w", err)
	}
	return out, nil
}

// buildBranchInArgs returns the (placeholders, args) pair for a
// session-scoped IN-clause query: args are
// [sessionID, branchID..., sessionID, window].
func buildBranchInArgs(sessionID string, branchIDs []string, window int) (string, []any) {
	parts := make([]string, len(branchIDs))
	args := make([]any, 0, len(branchIDs)+3)
	args = append(args, sessionID)
	for i, id := range branchIDs {
		parts[i] = "?"
		args = append(args, id)
	}
	args = append(args, sessionID, window)
	return strings.Join(parts, ","), args
}

// applyMMRReranking reorders `packets` by Maximal Marginal Relevance
// over the existing ranking. Returns a new slice in MMR order; the
// input is unchanged so the audit population pass still sees the
// original ranks. When len(packets) <= limit there's no diversity
// choice to make and the input is returned unchanged.
//
// Iterative greedy: at each step we pick the un-selected candidate
// `c` that maximizes
//
//	λ * score(c) - (1-λ) * max_{s∈selected} cosine(features(c), features(s))
//
// O(limit · |pool|) work — bounded by the candidate-pool size. λ=1
// degenerates to pure top-K (no diversity); λ=0 to "always avoid the
// most-similar already-picked" — useful for tests but not what the
// tunable defaults to.
func (m *MemoryForest) applyMMRReranking(packets []*BranchPacket, featureByBranch map[string][]float32, limit int) []*BranchPacket {
	if m == nil || len(packets) == 0 || limit <= 0 {
		return packets
	}
	if m.diversityLambda >= 1 {
		return packets
	}
	if limit >= len(packets) {
		return packets
	}
	out := make([]*BranchPacket, 0, len(packets))
	used := make([]bool, len(packets))

	first := highestRelevance(packets, used)
	if first < 0 {
		return packets
	}
	out = append(out, packets[first])
	used[first] = true

	lambda := m.diversityLambda
	for len(out) < limit {
		next := bestMMRPick(packets, used, out, featureByBranch, lambda)
		if next < 0 {
			break
		}
		out = append(out, packets[next])
		used[next] = true
	}
	for i := range packets {
		if !used[i] {
			out = append(out, packets[i])
		}
	}
	return out
}

// highestRelevance returns the index of the un-used packet with the
// largest Score.Total. -1 if none left.
func highestRelevance(packets []*BranchPacket, used []bool) int {
	best := -1
	for i, p := range packets {
		if used[i] || p == nil {
			continue
		}
		if best < 0 || p.Score.Total > packets[best].Score.Total {
			best = i
		}
	}
	return best
}

// bestMMRPick returns the index of the un-used packet with the
// largest MMR score against the already-selected set.
func bestMMRPick(packets []*BranchPacket, used []bool, selected []*BranchPacket, featureByBranch map[string][]float32, lambda float64) int {
	best := -1
	bestScore := 0.0
	for i, p := range packets {
		if used[i] || p == nil || p.Branch == nil {
			continue
		}
		penalty := maxCosineToSelected(featureByBranch[p.Branch.ID], selected, featureByBranch)
		mmr := lambda*p.Score.Total - (1-lambda)*penalty
		if best < 0 || mmr > bestScore {
			best = i
			bestScore = mmr
		}
	}
	return best
}

// maxCosineToSelected returns the largest cosine similarity between
// the candidate feature vector and any already-selected packet's
// feature vector. 0 when either vector is missing or zero-norm.
func maxCosineToSelected(candidate []float32, selected []*BranchPacket, featureByBranch map[string][]float32) float64 {
	if len(candidate) == 0 {
		return 0
	}
	candNorm := vectorNorm(candidate)
	if candNorm == 0 {
		return 0
	}
	worst := 0.0
	for _, s := range selected {
		if s == nil || s.Branch == nil {
			continue
		}
		other := featureByBranch[s.Branch.ID]
		if len(other) == 0 {
			continue
		}
		otherNorm := vectorNorm(other)
		if otherNorm == 0 {
			continue
		}
		sim := vectorDot(candidate, other) / (candNorm * otherNorm)
		if sim > worst {
			worst = sim
		}
	}
	return worst
}

// vectorDot computes the dot product. Iterates min(len(a),len(b)) so
// length-mismatched vectors don't panic.
func vectorDot(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// vectorNorm computes ||v||_2.
func vectorNorm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return 0
	}
	return math.Sqrt(sum)
}
