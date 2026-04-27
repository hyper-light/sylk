package forest

import (
	"context"
	"fmt"
	"strings"
)

// ────────────────────────────────────────────────────────────────────
// Issue #6 — Anti-precedent quota in Retrieve
// ────────────────────────────────────────────────────────────────────
//
// A reserved channel inside Retrieve that always surfaces matching
// anti-precedents (Family=AntiPattern, or Family=Outcome with
// Status=Failed) regardless of IncludeCounterEvidence. Slots are
// inserted at the bottom of the result so they don't displace
// high-relevance positives — the agent gets "5 worked, 2 failed"
// rather than choosing.
//
// Independent of the cooldown/MMR layer above it: anti-precedents
// are not subject to MMR or cooldown — they're a separate signal
// orthogonal to "what's relevant right now."

const (
	// defaultAntiPrecedentMaxSlots is the maximum number of anti-
	// precedent slots reserved per retrieval. Bounded so a noisy
	// failure history can't crowd out positives.
	defaultAntiPrecedentMaxSlots = 2

	// antiPrecedentSlotRatio caps slots at limit/N — for small Limit
	// queries we don't want anti-precedents to dominate.
	antiPrecedentSlotRatio = 4
)

// reserveAntiPrecedentSlots replaces the bottom-K of `packets` with
// matching anti-precedents (when any exist). Returns the modified
// slice; the caller treats it as opaque. When no anti-precedents
// match the query, the input is returned unchanged.
//
// Slot count = min(defaultAntiPrecedentMaxSlots, limit/antiPrecedentSlotRatio, len(packets)/2).
// We only displace primary packets when at least 2× the slot count
// remain — anti-precedents complement positives, never replace them.
//
// Best-effort: a query failure logs and returns the input unchanged.
func (m *MemoryForest) reserveAntiPrecedentSlots(ctx context.Context, query Query, packets []*BranchPacket, limit int) []*BranchPacket {
	if m == nil || limit <= 0 || len(packets) == 0 {
		return packets
	}
	slots := antiPrecedentSlotCount(limit, len(packets))
	if slots == 0 {
		return packets
	}
	excluded := branchIDSet(packets)

	branches, err := m.loadAntiPrecedentBranches(ctx, query, slots, excluded)
	if err != nil {
		if m.logger != nil {
			m.logger.Debug("forest: anti-precedent load failed", "err", err.Error())
		}
		return packets
	}
	if len(branches) == 0 {
		return packets
	}
	insertions := make([]*BranchPacket, 0, len(branches))
	for _, branch := range branches {
		insertions = append(insertions, &BranchPacket{
			Branch: branch,
			Score: PacketScore{
				Total:    branch.Salience,
				Base:     branch.Salience,
				Salience: branch.Salience,
			},
			Source: RetrievalSourceAntiPrecedent,
		})
	}
	return swapBottomKWithAntiPrecedents(packets, insertions, len(insertions))
}

// antiPrecedentSlotCount returns how many slots should be reserved.
// Constrained by the per-query cap, the limit/ratio rule, and the
// "never displace more than half the result" rule.
func antiPrecedentSlotCount(limit, available int) int {
	slots := defaultAntiPrecedentMaxSlots
	if r := limit / antiPrecedentSlotRatio; r < slots {
		slots = r
	}
	if h := available / 2; h < slots {
		slots = h
	}
	if slots < 0 {
		slots = 0
	}
	return slots
}

// swapBottomKWithAntiPrecedents replaces the last `n` entries of
// `packets` with the supplied anti-precedent insertions. When n
// exceeds len(packets) we just append.
func swapBottomKWithAntiPrecedents(packets, insertions []*BranchPacket, n int) []*BranchPacket {
	if n <= 0 {
		return packets
	}
	if n > len(packets) {
		return append(packets, insertions...)
	}
	out := make([]*BranchPacket, 0, len(packets))
	out = append(out, packets[:len(packets)-n]...)
	out = append(out, insertions...)
	return out
}

// loadAntiPrecedentBranches returns up to `limit` branches that
// qualify as anti-precedents, ranked by (failure_count * salience).
// Eligibility = Family=AntiPattern OR (Family=Outcome AND
// failure_count > success_count).
//
// agent_type is matched permissively: branches authored by the
// requested agent_type AND branches with empty agent_type are both
// eligible. No session scoping — anti-precedents are intentionally
// cross-session signal.
func (m *MemoryForest) loadAntiPrecedentBranches(ctx context.Context, query Query, limit int, excluded map[string]struct{}) ([]*Branch, error) {
	clauses := []string{
		"state != ?",
		"(family = ? OR (family = ? AND failure_count > success_count))",
		"failure_count > 0",
	}
	args := []any{
		string(BranchStateSuperseded),
		string(TreeFamilyAntiPattern),
		string(TreeFamilyOutcome),
	}
	if at := normalizeRequesterAgentType(query.AgentType); at != "" {
		clauses = append(clauses, "(agent_type = ? OR agent_type = '')")
		args = append(args, at)
	}

	q := `
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata,
		       last_applied_seq, constraint_severity
		FROM   forest_branches
		WHERE  ` + strings.Join(clauses, " AND ") + `
		ORDER BY (failure_count * salience) DESC, failure_count DESC, updated_at DESC
		LIMIT  ?
	`
	args = append(args, limit+len(excluded))

	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query anti-precedent branches: %w", err)
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
		return nil, fmt.Errorf("iterate anti-precedent rows: %w", err)
	}
	return out, nil
}
