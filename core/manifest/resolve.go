package manifest

import (
	"sort"
	"strings"
)

// ScopeMatchesQuery reports whether a decision with decisionScope applies
// to a query at queryScope. Match semantics:
//
//   - Every dimension on decisionScope must be satisfiable by queryScope.
//   - DimensionPath uses prefix-match: queryScope[path] must start with
//     decisionScope[path]. Empty / unset queryScope[path] never matches a
//     non-empty decisionScope[path].
//   - All other dimensions use exact equality.
//   - Dimensions present on queryScope but absent on decisionScope are
//     ignored (the decision is "more ambient" along that axis).
//
// An empty decisionScope matches any query (ambient decision).
func ScopeMatchesQuery(decisionScope, queryScope Scope) bool {
	for dim, want := range decisionScope {
		got, present := queryScope[dim]
		if !present {
			return false
		}
		if dim == DimensionPath {
			if !pathPrefixMatch(want, got) {
				return false
			}
			continue
		}
		if got != want {
			return false
		}
	}
	return true
}

// ScopesOverlap reports whether two scopes COULD apply at any common
// query coordinate. Used for conflict detection at declaration time:
// two decisions in the same domain with overlapping scopes and
// incompatible values produce a ConflictIncompatible.
//
//   - For each dimension present in both scopes:
//   - DimensionPath: one prefix must contain the other (or be equal).
//   - Other dimensions: exact equality.
//   - Dimensions present in only one scope don't disqualify overlap (the
//     overlap exists at any query carrying the union of dimensions).
func ScopesOverlap(a, b Scope) bool {
	for dim, aVal := range a {
		bVal, present := b[dim]
		if !present {
			continue
		}
		if dim == DimensionPath {
			if !pathPrefixMatch(aVal, bVal) && !pathPrefixMatch(bVal, aVal) {
				return false
			}
			continue
		}
		if aVal != bVal {
			return false
		}
	}
	return true
}

// pathPrefixMatch reports whether candidate is at-or-under prefix.
// Both inputs are normalized: trailing slash on prefix doesn't matter,
// and an empty prefix matches every candidate.
func pathPrefixMatch(prefix, candidate string) bool {
	prefix = strings.TrimSpace(prefix)
	candidate = strings.TrimSpace(candidate)
	if prefix == "" {
		return true
	}
	if candidate == "" {
		return false
	}
	// Normalize trailing slash for prefix-match correctness so that a
	// scope path of "src" doesn't also match "src_legacy/foo.go".
	prefix = strings.TrimRight(prefix, "/")
	if candidate == prefix {
		return true
	}
	return strings.HasPrefix(candidate, prefix+"/")
}

// Specificity is the number of typed dimensions on a scope. Higher is
// more specific. Used as the primary ranking key in the
// SpecificityFirstMover policy.
func Specificity(s Scope) int {
	return len(s)
}

// ResolveWinner picks the winning decision from candidates per the
// policy. Candidates should already be filtered to those whose scopes
// match the query (see FilterMatching). Returns nil for an empty input.
func ResolveWinner(policy ResolutionPolicy, candidates []*Decision) *Decision {
	if len(candidates) == 0 {
		return nil
	}
	switch policy {
	case ResolvePolicyLatestWins:
		return latestDeclared(candidates)
	case ResolvePolicyAuthorityFirst:
		return authorityFirst(candidates)
	case ResolvePolicyExplicitConsensusRequired:
		return explicitConsensusOnly(candidates)
	case ResolvePolicySpecificityFirstMover, "":
		return specificityFirstMover(candidates)
	}
	return specificityFirstMover(candidates)
}

// FilterMatching returns the subset of decisions whose scopes match
// queryScope. The returned slice is in arbitrary order; ResolveWinner
// applies the policy-specific ranking.
func FilterMatching(decisions []*Decision, queryScope Scope) []*Decision {
	out := make([]*Decision, 0, len(decisions))
	for _, d := range decisions {
		if d == nil {
			continue
		}
		if ScopeMatchesQuery(d.Scope, queryScope) {
			out = append(out, d)
		}
	}
	return out
}

// SortBySpecificity orders decisions most-specific first, with stable
// tiebreaking by declaration time ascending. Used by query results to
// surface "the winner is X; here's what else applied" in a sensible
// order for human + LLM inspection.
func SortBySpecificity(decisions []*Decision) {
	sort.SliceStable(decisions, func(i, j int) bool {
		si, sj := Specificity(decisions[i].Scope), Specificity(decisions[j].Scope)
		if si != sj {
			return si > sj
		}
		return decisions[i].DeclaredAt.Before(decisions[j].DeclaredAt)
	})
}

func specificityFirstMover(candidates []*Decision) *Decision {
	best := candidates[0]
	for _, c := range candidates[1:] {
		if isMoreSpecificFirstMover(c, best) {
			best = c
		}
	}
	return best
}

func isMoreSpecificFirstMover(candidate, current *Decision) bool {
	cs, cur := Specificity(candidate.Scope), Specificity(current.Scope)
	if cs != cur {
		return cs > cur
	}
	// Tiebreaker: first-mover (earlier declaration wins). Equal times
	// fall through to the existing winner.
	return candidate.DeclaredAt.Before(current.DeclaredAt)
}

func latestDeclared(candidates []*Decision) *Decision {
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.DeclaredAt.After(best.DeclaredAt) {
			best = c
		}
	}
	return best
}

// authorityFirst preference: architect Charter (TODO when Charter ships)
// > Consensus > Committed > Tentative; ties fall through to specificity.
// In phase 1 (no Charter yet) this collapses to confidence-rank then
// specificity then first-mover.
func authorityFirst(candidates []*Decision) *Decision {
	best := candidates[0]
	for _, c := range candidates[1:] {
		if confidenceRank(c.Confidence) > confidenceRank(best.Confidence) {
			best = c
			continue
		}
		if confidenceRank(c.Confidence) == confidenceRank(best.Confidence) && isMoreSpecificFirstMover(c, best) {
			best = c
		}
	}
	return best
}

// explicitConsensusOnly returns nil unless a Consensus decision exists.
// This is the safe default for high-risk domains where silent
// convergence on Tentative declarations would be dangerous.
func explicitConsensusOnly(candidates []*Decision) *Decision {
	var consensusDecisions []*Decision
	for _, c := range candidates {
		if c.Confidence == ConfidenceConsensus {
			consensusDecisions = append(consensusDecisions, c)
		}
	}
	if len(consensusDecisions) == 0 {
		return nil
	}
	return specificityFirstMover(consensusDecisions)
}

func confidenceRank(c Confidence) int {
	switch c {
	case ConfidenceConsensus:
		return 4
	case ConfidenceCommitted:
		return 3
	case ConfidenceTentative:
		return 2
	case ConfidenceHint:
		return 1
	}
	return 0
}
