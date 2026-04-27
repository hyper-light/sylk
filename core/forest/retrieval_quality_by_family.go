package forest

import (
	"context"
	"fmt"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #11 Phase 1.4 — Per-family retrieval quality diagnostic
// ────────────────────────────────────────────────────────────────────
//
// Operator-driven diagnostic that breaks down RetrievalQualitySince
// metrics by TreeFamily. Powers the Phase 2 measurement period that
// validates whether each family carries distinct retrieval-quality
// signal — the data that drives the Phase 3 merge decisions.
//
// Read-only, no goroutines, panic-free. Bounded SQL with empty-window
// short-circuit.

// RetrievalQualityByFamily reports per-family precision@K + MRR +
// NDCG + outcome coverage. Same shape as RetrievalQualityStats but
// scoped to retrievals whose RETURNED candidates' branch family
// matches `Family`.
type RetrievalQualityByFamily struct {
	Family          TreeFamily      `json:"family"`
	RetrievalCount  int64           `json:"retrieval_count"`
	PrecisionAtK    map[int]float64 `json:"precision_at_k"`
	MRR             float64         `json:"mrr"`
	NDCGAt5         float64         `json:"ndcg_at_5"`
	OutcomeCoverage float64         `json:"outcome_coverage"`
}

// RetrievalQualityByFamilySince computes per-family retrieval
// quality over the window. Issues one combined query that JOINs
// candidates with branches (for family) and outcomes (for ranking
// quality), then partitions per-family in Go.
//
// Operators read this to detect whether dropped-candidate families
// (Decision/Preference/Capability/Opportunity/Conflict) have
// distinguishable quality profiles vs the kept families
// (Intent/Constraint/Evidence/Outcome/AntiPattern). If a family's
// quality is statistically indistinguishable from a sibling, it's
// a candidate for merging in Phase 3.
func (m *MemoryForest) RetrievalQualityByFamilySince(ctx context.Context, since time.Time) ([]RetrievalQualityByFamily, error) {
	if m == nil {
		return nil, nil
	}
	rankings, err := m.loadFamilyTaggedRankings(ctx, since)
	if err != nil {
		return nil, err
	}
	if len(rankings) == 0 {
		return nil, nil
	}
	successes, err := m.loadSuccessfulOutcomes(ctx, since)
	if err != nil {
		return nil, err
	}
	return aggregateQualityByFamily(rankings, successes), nil
}

// familyTaggedRanking is candidateRanking + the branch's TreeFamily,
// loaded via a single JOIN so we don't issue a per-branch query.
type familyTaggedRanking struct {
	candidateRanking
	family TreeFamily
}

// loadFamilyTaggedRankings issues the JOIN'd query: for every
// RETURNED candidate in the window, fetch its rank position +
// owning branch's family. Sorted by retrieval_event_id + rank so
// per-retrieval grouping is contiguous.
func (m *MemoryForest) loadFamilyTaggedRankings(ctx context.Context, since time.Time) ([]familyTaggedRanking, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT rc.retrieval_event_id, rc.session_id, rc.branch_id,
		       rc.rank_position, re.requested_at, b.family
		FROM   forest_retrieval_candidates rc
		JOIN   forest_retrieval_events re ON re.id = rc.retrieval_event_id
		LEFT JOIN forest_branches b ON b.id = rc.branch_id
		WHERE  rc.returned    = 1
		  AND  re.requested_at >= ?
		ORDER BY rc.retrieval_event_id, rc.rank_position
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query family-tagged rankings: %w", err)
	}
	defer rows.Close()
	var out []familyTaggedRanking
	for rows.Next() {
		var (
			r      familyTaggedRanking
			rawAt  int64
			family string
		)
		if err := rows.Scan(&r.retrievalEventID, &r.sessionID, &r.branchID, &r.rankPosition, &rawAt, &family); err != nil {
			return nil, fmt.Errorf("scan family-tagged ranking: %w", err)
		}
		r.requestedAt = time.Unix(rawAt, 0).UTC()
		r.family = TreeFamily(family)
		out = append(out, r)
	}
	return out, rows.Err()
}

// aggregateQualityByFamily partitions rankings by family, then runs
// the same per-retrieval evaluation as RetrievalQualitySince inside
// each partition. Result has one entry per observed family.
func aggregateQualityByFamily(
	rankings []familyTaggedRanking,
	successes map[outcomeKey]time.Time,
) []RetrievalQualityByFamily {
	byFamily := groupRankingsByFamily(rankings)
	out := make([]RetrievalQualityByFamily, 0, len(byFamily))
	for family, familyRankings := range byFamily {
		stats := perFamilyQuality(family, familyRankings, successes)
		out = append(out, stats)
	}
	return out
}

// groupRankingsByFamily partitions the input by `family`. Each
// partition's candidate rankings are kept in their original order
// (sorted by retrieval_event_id + rank_position from the SQL).
func groupRankingsByFamily(rankings []familyTaggedRanking) map[TreeFamily][]familyTaggedRanking {
	out := make(map[TreeFamily][]familyTaggedRanking)
	for _, r := range rankings {
		out[r.family] = append(out[r.family], r)
	}
	return out
}

// perFamilyQuality runs the same precision@K / MRR / NDCG analysis
// per-family. Re-uses aggregateRetrievalQuality's helpers via a
// projection back to the candidateRanking shape that
// groupRankingsByRetrieval expects.
func perFamilyQuality(
	family TreeFamily,
	rankings []familyTaggedRanking,
	successes map[outcomeKey]time.Time,
) RetrievalQualityByFamily {
	flat := make([]candidateRanking, 0, len(rankings))
	for _, r := range rankings {
		flat = append(flat, r.candidateRanking)
	}
	groups := groupRankingsByRetrieval(flat)
	stats := RetrievalQualityByFamily{
		Family:       family,
		PrecisionAtK: map[int]float64{},
	}
	if len(groups) == 0 {
		return stats
	}
	stats.RetrievalCount = int64(len(groups))
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
	total := float64(stats.RetrievalCount)
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
