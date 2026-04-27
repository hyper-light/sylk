package forest

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #9 Phase B.3 — End-to-end retrieval quality metrics
// ────────────────────────────────────────────────────────────────────
//
// precision@K, MRR, NDCG@5, outcome coverage. Joins per-retrieval
// rank positions with outcome events to derive ranking quality.
//
// Bounded SQL + bounds-checked Go aggregation. Empty windows return
// zeroed metrics, not errors. No goroutines, no panic surface.

// RetrievalQualitySince computes ranking quality over the window.
// Walks each retrieval audit + its candidate ranks, joins with
// outcome events to identify which RETURNED candidates received a
// success outcome within the attribution window, then computes the
// rank-aware metrics.
//
// One SQL pass loads (retrieval_event_id, branch_id, rank_position)
// for all RETURNED candidates whose retrieval is in the window.
// A second pass loads the outcome status per (session_id, branch_id)
// in the same window. Per-retrieval aggregation in Go.
func (m *MemoryForest) RetrievalQualitySince(ctx context.Context, since time.Time) (RetrievalQualityStats, error) {
	stats := RetrievalQualityStats{
		WindowStart:  since,
		WindowEnd:    time.Now().UTC(),
		PrecisionAtK: map[int]float64{},
	}
	if m == nil {
		return stats, nil
	}
	rankings, err := m.loadReturnedCandidateRankings(ctx, since)
	if err != nil {
		return stats, err
	}
	if len(rankings) == 0 {
		return stats, nil
	}
	successes, err := m.loadSuccessfulOutcomes(ctx, since)
	if err != nil {
		return stats, err
	}
	stats = aggregateRetrievalQuality(rankings, successes, since, stats.WindowEnd)
	return stats, nil
}

// candidateRanking is one (retrieval, branch) row with its rank
// position. RetrievalQualitySince walks these grouped by retrieval
// to compute per-retrieval precision@K + MRR + NDCG.
type candidateRanking struct {
	retrievalEventID string
	sessionID        string
	branchID         string
	rankPosition     int
	requestedAt      time.Time
}

// outcomeKey identifies a (session, branch) pair for outcome lookup.
type outcomeKey struct {
	sessionID string
	branchID  string
}

// loadReturnedCandidateRankings pulls every RETURNED candidate
// from the window. Sorted by retrieval seq + rank position so the
// per-retrieval aggregation can walk a contiguous block per
// retrieval without hash-grouping.
func (m *MemoryForest) loadReturnedCandidateRankings(ctx context.Context, since time.Time) ([]candidateRanking, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT rc.retrieval_event_id, rc.session_id, rc.branch_id,
		       rc.rank_position, re.requested_at
		FROM   forest_retrieval_candidates rc
		JOIN   forest_retrieval_events re ON re.id = rc.retrieval_event_id
		WHERE  rc.returned    = 1
		  AND  re.requested_at >= ?
		ORDER BY rc.retrieval_event_id, rc.rank_position
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query candidate rankings: %w", err)
	}
	defer rows.Close()
	var out []candidateRanking
	for rows.Next() {
		var (
			r           candidateRanking
			requestedAt int64
		)
		if err := rows.Scan(&r.retrievalEventID, &r.sessionID, &r.branchID, &r.rankPosition, &requestedAt); err != nil {
			return nil, fmt.Errorf("scan candidate ranking: %w", err)
		}
		r.requestedAt = time.Unix(requestedAt, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadSuccessfulOutcomes returns outcome events with status=succeeded
// in the window, mapped by (session_id, branch_id). Returns the
// minimum timestamp per key so attribution-window checks pick the
// earliest outcome (consistent with "first acted upon" semantics in
// MRR).
func (m *MemoryForest) loadSuccessfulOutcomes(ctx context.Context, since time.Time) (map[outcomeKey]time.Time, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT session_id, branch_id, timestamp, payload
		FROM   forest_events
		WHERE  event_type = 'outcome_recorded'
		  AND  timestamp >= ?
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query outcome events: %w", err)
	}
	defer rows.Close()
	out := make(map[outcomeKey]time.Time)
	for rows.Next() {
		var (
			sessionID string
			branchID  string
			timestamp int64
			payload   sql.NullString
		)
		if err := rows.Scan(&sessionID, &branchID, &timestamp, &payload); err != nil {
			return nil, fmt.Errorf("scan outcome row: %w", err)
		}
		if outcomeStatusFromPayloadJSON(payload.String) != OutcomeStatusSucceeded {
			continue
		}
		key := outcomeKey{sessionID: sessionID, branchID: branchID}
		ts := time.Unix(timestamp, 0).UTC()
		if existing, ok := out[key]; !ok || ts.Before(existing) {
			out[key] = ts
		}
	}
	return out, rows.Err()
}

// aggregateRetrievalQuality groups rankings by retrieval, evaluates
// hit@K + reciprocal-rank + DCG per retrieval, then averages.
//
// "Hit at rank r": the candidate at rank r received a success
// outcome within the attribution window starting from the
// retrieval's requested_at.
func aggregateRetrievalQuality(
	rankings []candidateRanking,
	successes map[outcomeKey]time.Time,
	windowStart, windowEnd time.Time,
) RetrievalQualityStats {
	stats := RetrievalQualityStats{
		WindowStart:  windowStart,
		WindowEnd:    windowEnd,
		PrecisionAtK: map[int]float64{},
	}
	groups := groupRankingsByRetrieval(rankings)
	if len(groups) == 0 {
		return stats
	}

	var (
		retrievalsWithOutcome int
		precisionSums         = map[int]float64{
			defaultRetrievalQualityK1: 0,
			defaultRetrievalQualityK3: 0,
			defaultRetrievalQualityK5: 0,
		}
		mrrSum  float64
		ndcgSum float64
	)
	for _, group := range groups {
		hasOutcome, hits, firstHitRank := evaluateRetrieval(group, successes)
		if hasOutcome {
			retrievalsWithOutcome++
			if firstHitRank > 0 {
				mrrSum += 1.0 / float64(firstHitRank)
			}
			ndcgSum += ndcgAtK(hits, defaultRetrievalQualityK5)
		}
		for k := range precisionSums {
			precisionSums[k] += precisionAtK(hits, k)
		}
	}
	total := float64(len(groups))
	stats.RetrievalCount = int64(len(groups))
	for k, sum := range precisionSums {
		stats.PrecisionAtK[k] = sum / total
	}
	if retrievalsWithOutcome > 0 {
		stats.MRR = mrrSum / float64(retrievalsWithOutcome)
		stats.NDCGAt5 = ndcgSum / float64(retrievalsWithOutcome)
	}
	stats.OutcomeCoverage = float64(retrievalsWithOutcome) / total
	return stats
}

// groupRankingsByRetrieval splits a sorted slice (by retrieval_event_id)
// into per-retrieval groups. Pre-sorting is enforced by the SQL query
// so this is O(N).
func groupRankingsByRetrieval(rankings []candidateRanking) [][]candidateRanking {
	if len(rankings) == 0 {
		return nil
	}
	out := make([][]candidateRanking, 0, 32)
	current := []candidateRanking{rankings[0]}
	for i := 1; i < len(rankings); i++ {
		if rankings[i].retrievalEventID != current[0].retrievalEventID {
			out = append(out, current)
			current = []candidateRanking{rankings[i]}
			continue
		}
		current = append(current, rankings[i])
	}
	out = append(out, current)
	return out
}

// evaluateRetrieval returns (hasAttributableOutcome, hits, firstHitRank).
// `hits` is a bool slice indexed by rank-1 (so hits[0] = rank 1's
// success). `firstHitRank` is the rank of the first hit (1-based) or
// 0 if none.
//
// "Has outcome" means at least one returned candidate received a
// success outcome within the attribution window. Retrievals with no
// attributable outcome are excluded from MRR + NDCG averages
// (counted in OutcomeCoverage instead).
func evaluateRetrieval(group []candidateRanking, successes map[outcomeKey]time.Time) (bool, []bool, int) {
	if len(group) == 0 {
		return false, nil, 0
	}
	hits := make([]bool, len(group))
	firstHitRank := 0
	hasOutcome := false
	requestedAt := group[0].requestedAt
	attributionEnd := requestedAt.Add(outcomeAttributionWindow)

	for i, c := range group {
		key := outcomeKey{sessionID: c.sessionID, branchID: c.branchID}
		ts, ok := successes[key]
		if !ok {
			continue
		}
		if ts.Before(requestedAt) || ts.After(attributionEnd) {
			continue
		}
		hits[i] = true
		hasOutcome = true
		if firstHitRank == 0 {
			firstHitRank = c.rankPosition
		}
	}
	return hasOutcome, hits, firstHitRank
}

// precisionAtK = (number of hits in top K) / K. K bounded to
// len(hits); 0 if K <= 0.
func precisionAtK(hits []bool, k int) float64 {
	if k <= 0 || len(hits) == 0 {
		return 0
	}
	if k > len(hits) {
		k = len(hits)
	}
	var hit int
	for i := 0; i < k; i++ {
		if hits[i] {
			hit++
		}
	}
	return float64(hit) / float64(k)
}

// ndcgAtK = DCG@K / IDCG@K where DCG = Σ (rel_i / log2(i+1)) for
// 1-based rank i. With binary relevance and at most one hit per
// rank, IDCG@K is achieved when all hits are at the top of the
// ranking — equal to DCG of `min(K, hit_count)` consecutive 1s.
func ndcgAtK(hits []bool, k int) float64 {
	if k <= 0 || len(hits) == 0 {
		return 0
	}
	limit := k
	if limit > len(hits) {
		limit = len(hits)
	}
	var dcg, hitCount float64
	for i := 0; i < limit; i++ {
		if hits[i] {
			dcg += 1.0 / math.Log2(float64(i+2))
			hitCount++
		}
	}
	if hitCount == 0 {
		return 0
	}
	idealHits := int(hitCount)
	if idealHits > limit {
		idealHits = limit
	}
	var idcg float64
	for i := 0; i < idealHits; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}
