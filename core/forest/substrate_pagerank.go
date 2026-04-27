package forest

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ────────────────────────────────────────────────────────────────────
// Issue #7 Layer 2 — Personalized PageRank substrate
// ────────────────────────────────────────────────────────────────────
//
// Replaces the substrate.go 4-iteration diffusion + per-edge rewrite
// with personalized PageRank over the relay graph:
//
//   r ← α · Mᵀ · r + (1-α) · v
//
// where α=0.85 (Brin/Page), Mᵀ is the column-stochastic transition
// matrix derived from relay edge weights, v is the personalization
// vector (uniform over canopy roots), and r is the rank vector
// computed via power-method iteration until L1-delta < epsilon.
//
// Substrate signals are derived projections of r:
//
//   Potential       = r[branch]
//   Frontier        = r[branch] · (1 - access_pressure)
//   Inhibition      = ∑ inhibitor_edge · r[neighbor] (contradicts/supersedes)
//   ConductanceMass = ∑ outbound_edge · r[branch]
//
// Cached per (session_id, graph_version): the cache is exact
// (version-keyed, not TTL'd) so a graph mutation invalidates
// reliably. LRU-bounded so memory stays flat under session churn.

const (
	// pageRankAlpha is the damping factor — the per-step probability
	// of following an edge vs teleporting back to the personalization
	// vector. 0.85 is the standard Brin/Page value with extensive
	// theoretical and empirical justification.
	pageRankAlpha = 0.85

	// pageRankConvergenceEpsilon is the L1 norm of (rₜ - rₜ₋₁) below
	// which we declare convergence. 1e-6 is conservative for typical
	// graphs (10²–10⁵ nodes); convergence usually within 20–40 iter.
	pageRankConvergenceEpsilon = 1e-6

	// pageRankMaxIterations bounds the power-method loop. Beyond this
	// we return the current vector with a warning — guarantees
	// bounded latency even on pathological graphs.
	pageRankMaxIterations = 100

	// pageRankCacheCapacity is the LRU cache size: number of distinct
	// (session_id, graph_version) entries kept in memory. Each entry
	// is O(N) floats where N = branch count for the session; for
	// typical N=10³ that's ~8KB per entry. 64 entries ≈ 512KB total.
	pageRankCacheCapacity = 64

	// pageRankInhibitionRelations enumerates the relay relations that
	// produce inhibition signal (rather than support). Substrate
	// inhibition was a hand-tuned blend; here we model it directly
	// as graph influence from contradiction-bearing neighbors.
	pageRankInhibitionContradicts = "contradicts"
	pageRankInhibitionSupersedes  = "supersedes"
)

// loadPageRankSubstrateSignals is the dispatch entry for
// SubstrateModePageRank. Delegates to the Layer 3 incremental cache
// so per-session graph mutations only push mass localized to
// changed-edge endpoints — vastly cheaper than the Layer 2 full-
// recompute on every graph_version advance.
func (m *MemoryForest) loadPageRankSubstrateSignals(
	ctx context.Context,
	branches []*Branch,
	canopy *Canopy,
) (map[string]substrateSignal, error) {
	return m.loadIncrementalPageRankSubstrateSignals(ctx, branches, canopy)
}

// projectCachedSignals returns the subset of the cached map keyed by
// the branches the current Retrieve cares about. The cached entry
// holds signals for ALL branches in the session at recompute time;
// a later retrieval may only need a subset.
//
// Branches not present in the cache (e.g., loaded from outside this
// session, or added after compute time and not yet reflected in the
// graph_version-keyed cache) get a zero substrateSignal so scoring
// can proceed without surprise omissions. The next graph_version
// advance will trigger a recompute that includes them.
func projectCachedSignals(cached map[string]substrateSignal, branches []*Branch) map[string]substrateSignal {
	out := make(map[string]substrateSignal, len(branches))
	for _, b := range branches {
		if b == nil {
			continue
		}
		if sig, ok := cached[b.ID]; ok {
			out[b.ID] = sig
			continue
		}
		out[b.ID] = substrateSignal{}
	}
	return out
}

// loadAllSessionBranches returns every active branch in the session.
// Used by the PageRank computation so the cache holds full-session
// signals (any subset retrieval can then read from the cache without
// missing signals for branches it hasn't seen).
func (m *MemoryForest) loadAllSessionBranches(ctx context.Context, sessionID string) ([]*Branch, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata,
		       last_applied_seq, constraint_severity
		FROM   forest_branches
		WHERE  session_id = ?
		  AND  state != ?
	`, sessionID, string(BranchStateSuperseded))
	if err != nil {
		return nil, fmt.Errorf("load session branches for pagerank: %w", err)
	}
	defer rows.Close()
	var out []*Branch
	for rows.Next() {
		branch, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		if branch != nil {
			out = append(out, branch)
		}
	}
	return out, rows.Err()
}

// computePageRankSignals runs the full PR computation for one session
// and converts the resulting rank vector to substrate signals.
func (m *MemoryForest) computePageRankSignals(
	ctx context.Context,
	sessionID string,
	branches []*Branch,
	canopyRoots map[string]struct{},
) (map[string]substrateSignal, error) {
	if len(branches) == 0 {
		return map[string]substrateSignal{}, nil
	}
	graph, err := m.loadPageRankGraph(ctx, sessionID, branches)
	if err != nil {
		return nil, err
	}
	personalization := buildPersonalizationVector(branches, canopyRoots)
	rankVector := powerIteratePageRank(graph, personalization)
	return graph.signalsFromRank(rankVector, branches), nil
}

// pageRankGraph is the in-memory representation: dense index → ID
// mapping plus sparse adjacency lists per relation type.
type pageRankGraph struct {
	indexByID    map[string]int
	branchIDs    []string
	outEdges     [][]pageRankEdge // outgoing edges per source index
	inhibitorIn  [][]pageRankEdge // contradicts/supersedes incoming edges
	outDegrees   []float64        // sum of outgoing weights (for dangling correction)
	conductances [][]pageRankEdge // outgoing edges with their raw weights for ConductanceMass
}

// pageRankEdge is one entry in an adjacency list. Weight is the
// normalized fraction of the source's outgoing mass that flows here.
type pageRankEdge struct {
	target int
	weight float64
}

// loadPageRankGraph builds the per-session graph from forest_relay_edges.
// O(M) work where M is the edge count; one indexed query.
func (m *MemoryForest) loadPageRankGraph(ctx context.Context, sessionID string, branches []*Branch) (*pageRankGraph, error) {
	graph := &pageRankGraph{
		indexByID:    make(map[string]int, len(branches)),
		branchIDs:    make([]string, len(branches)),
		outEdges:     make([][]pageRankEdge, len(branches)),
		inhibitorIn:  make([][]pageRankEdge, len(branches)),
		outDegrees:   make([]float64, len(branches)),
		conductances: make([][]pageRankEdge, len(branches)),
	}
	for i, b := range branches {
		graph.indexByID[b.ID] = i
		graph.branchIDs[i] = b.ID
	}
	if err := m.populatePageRankAdjacency(ctx, sessionID, graph, branches); err != nil {
		return nil, err
	}
	graph.normalizeOutgoing()
	return graph, nil
}

// populatePageRankAdjacency scans forest_relay_edges where either
// endpoint is in the session's branches. Bidirectional fill: edges
// are not symmetric in semantic but the adjacency is built so power
// iteration sees the right transitions.
func (m *MemoryForest) populatePageRankAdjacency(ctx context.Context, sessionID string, graph *pageRankGraph, branches []*Branch) error {
	if len(branches) == 0 {
		return nil
	}
	placeholders := make([]string, len(branches))
	args := make([]any, 0, len(branches))
	for i, b := range branches {
		placeholders[i] = "?"
		args = append(args, b.ID)
	}
	q := `
		SELECT source_branch_id, target_branch_id, relation, weight
		FROM   forest_relay_edges
		WHERE  source_branch_id IN (` + strings.Join(placeholders, ",") + `)
		   OR  target_branch_id IN (` + strings.Join(placeholders, ",") + `)
	`
	// Duplicate args list for the second IN clause.
	args = append(args, args[:len(branches)]...)

	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("query pagerank adjacency: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			source   string
			target   string
			relation string
			weight   float64
		)
		if err := rows.Scan(&source, &target, &relation, &weight); err != nil {
			return fmt.Errorf("scan pagerank edge: %w", err)
		}
		srcIdx, srcOK := graph.indexByID[source]
		tgtIdx, tgtOK := graph.indexByID[target]
		if !srcOK || !tgtOK {
			continue // edge crosses out of the session subgraph
		}
		graph.outEdges[srcIdx] = append(graph.outEdges[srcIdx], pageRankEdge{target: tgtIdx, weight: weight})
		graph.outDegrees[srcIdx] += weight
		graph.conductances[srcIdx] = append(graph.conductances[srcIdx], pageRankEdge{target: tgtIdx, weight: weight})
		if relation == pageRankInhibitionContradicts || relation == pageRankInhibitionSupersedes {
			graph.inhibitorIn[tgtIdx] = append(graph.inhibitorIn[tgtIdx], pageRankEdge{target: srcIdx, weight: weight})
		}
	}
	return rows.Err()
}

// normalizeOutgoing converts raw weights to outgoing fractions so
// each node's outEdges sum to 1.0 (or 0.0 for dangling nodes). This
// is what makes Mᵀ column-stochastic.
func (g *pageRankGraph) normalizeOutgoing() {
	for srcIdx, edges := range g.outEdges {
		mass := g.outDegrees[srcIdx]
		if mass <= 0 {
			continue
		}
		for j := range edges {
			edges[j].weight /= mass
		}
		g.outEdges[srcIdx] = edges
	}
}

// powerIteratePageRank runs the personalized PageRank power-method
// loop until convergence or maxIterations. Returns the rank vector.
//
// Dangling-mass correction: for nodes with zero outgoing edges, we
// distribute their mass uniformly via the personalization vector
// (the standard PR-with-personalization handling).
func powerIteratePageRank(g *pageRankGraph, personalization []float64) []float64 {
	n := len(g.branchIDs)
	if n == 0 {
		return nil
	}
	r := make([]float64, n)
	copy(r, personalization)

	for iter := 0; iter < pageRankMaxIterations; iter++ {
		next := make([]float64, n)

		// Damped teleport.
		teleport := 1.0 - pageRankAlpha
		for i := 0; i < n; i++ {
			next[i] = teleport * personalization[i]
		}

		// Edge-flow contribution.
		danglingMass := 0.0
		for srcIdx, edges := range g.outEdges {
			if len(edges) == 0 || g.outDegrees[srcIdx] == 0 {
				danglingMass += r[srcIdx]
				continue
			}
			ri := r[srcIdx]
			for _, edge := range edges {
				next[edge.target] += pageRankAlpha * ri * edge.weight
			}
		}

		// Dangling-mass redistribution: spread proportionally to
		// personalization (i.e., toward canopy roots).
		if danglingMass > 0 {
			for i := 0; i < n; i++ {
				next[i] += pageRankAlpha * danglingMass * personalization[i]
			}
		}

		// Convergence check via L1 delta.
		delta := 0.0
		for i := 0; i < n; i++ {
			delta += math.Abs(next[i] - r[i])
		}
		r = next
		if delta < pageRankConvergenceEpsilon {
			break
		}
	}
	return r
}

// signalsFromRank converts the rank vector to substrate signals on
// the requested branch subset.
func (g *pageRankGraph) signalsFromRank(rank []float64, branches []*Branch) map[string]substrateSignal {
	out := make(map[string]substrateSignal, len(branches))
	for _, b := range branches {
		idx, ok := g.indexByID[b.ID]
		if !ok {
			continue
		}
		out[b.ID] = substrateSignal{
			Potential:       rank[idx],
			Frontier:        rank[idx] * (1 - accessPressure(b)),
			Inhibition:      g.computeInhibition(idx, rank),
			ConductanceMass: g.computeConductanceMass(idx, rank),
		}
	}
	return out
}

// computeInhibition sums the rank-weighted contribution of incoming
// inhibitor edges (contradicts, supersedes). High inhibition means
// the branch is being pushed against by influential neighbors.
// All accesses are bounds-checked so malformed adjacency (edge.target
// pointing outside the rank vector) is silently skipped.
func (g *pageRankGraph) computeInhibition(idx int, rank []float64) float64 {
	if idx < 0 || idx >= len(g.inhibitorIn) {
		return 0
	}
	var sum float64
	for _, edge := range g.inhibitorIn[idx] {
		if edge.target < 0 || edge.target >= len(rank) {
			continue
		}
		sum += edge.weight * rank[edge.target]
	}
	if sum < 0 {
		return 0
	}
	if sum > 1 {
		return 1
	}
	return sum
}

// computeConductanceMass sums the rank-weighted outgoing mass — a
// measure of how much "current" this branch sources into the graph.
func (g *pageRankGraph) computeConductanceMass(idx int, rank []float64) float64 {
	if idx < 0 || idx >= len(g.conductances) {
		return 0
	}
	var sum float64
	for _, edge := range g.conductances[idx] {
		if edge.target < 0 || edge.target >= len(rank) {
			continue
		}
		sum += edge.weight * rank[edge.target]
	}
	return sum
}

// buildPersonalizationVector produces a normalized teleport
// distribution. Default: uniform over canopy roots' branches.
// Fallback: uniform over all branches when no canopy roots are
// supplied (cold-start session, semantic-only retrieval).
func buildPersonalizationVector(branches []*Branch, canopyRoots map[string]struct{}) []float64 {
	n := len(branches)
	v := make([]float64, n)
	if n == 0 {
		return v
	}
	rootMatches := 0
	for i, b := range branches {
		if _, hit := canopyRoots[b.RootID]; hit {
			v[i] = 1.0
			rootMatches++
		}
	}
	if rootMatches == 0 {
		// Uniform fallback.
		share := 1.0 / float64(n)
		for i := range v {
			v[i] = share
		}
		return v
	}
	share := 1.0 / float64(rootMatches)
	for i := range v {
		if v[i] > 0 {
			v[i] = share
		}
	}
	return v
}

// canopyRootSetFromCanopy lifts the canopy root list into a set for
// O(1) membership lookup. nil-tolerant.
func canopyRootSetFromCanopy(c *Canopy) map[string]struct{} {
	if c == nil {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(c.RootIDs))
	for _, id := range c.RootIDs {
		out[id] = struct{}{}
	}
	return out
}

// groupBranchesBySession partitions a branch slice by SessionID for
// per-session PR computation. Sessions with empty SessionID are
// grouped under "" (semantic / global-prior branches).
func groupBranchesBySession(branches []*Branch) map[string][]*Branch {
	out := make(map[string][]*Branch)
	for _, b := range branches {
		if b == nil {
			continue
		}
		out[b.SessionID] = append(out[b.SessionID], b)
	}
	// Stable ordering for tests: re-sort each session's branches by ID.
	for _, group := range out {
		sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
	}
	return out
}

