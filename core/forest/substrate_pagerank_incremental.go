package forest

import (
	"context"
	"fmt"
	"sync"
)

// ────────────────────────────────────────────────────────────────────
// Issue #7 Layer 3 — Approximate Personalized PageRank (push)
// ────────────────────────────────────────────────────────────────────
//
// Layer 2 cached the full PageRank vector keyed by graph_version. Any
// graph mutation (new event → graph_version++) invalidated the entry
// and forced a full power-iteration recompute. For long-running
// sessions with frequent events, that's the hot path — every edge
// weight bump triggers O(M·iter) work.
//
// Layer 3 keeps cached state per-session, NOT per-version. When the
// session's branch projection seq advances, we identify edges whose
// last_applied_seq > our cursor, update their normalized weights,
// "unsettle" the affected endpoints by moving their settled rank
// (r) back to residual (p), and run the Andersen-Chung-Lang push
// algorithm until residual is below ε.
//
// Push is O(work-done): only nodes whose residual exceeds ε·d_u get
// touched. A small localized change → small recompute. A large
// change degenerates to O(M·iter), no worse than Layer 2's full
// recompute.
//
// Concurrency: per-session sync.Mutex serializes pushes for one
// session; different sessions push concurrently. Cache lookup is
// briefly held, then released — push runs under the per-session
// lock only.

const (
	// pageRankIncrementalEpsilon is the residual-per-degree bound
	// below which a node is considered settled. Smaller = tighter
	// approximation, more push work. 1e-6 ≈ matches Layer 2's
	// convergence delta.
	pageRankIncrementalEpsilon = 1e-6

	// pageRankPushBudget caps push iterations per reconcile call.
	// Beyond budget we return the current vector (bounded staleness).
	// 100k operations is far above the typical ~1000 needed for
	// localized changes; pathological cases (full graph reset) hit
	// the cap and accept a single Retrieve seeing partial signal —
	// the next call resumes pushing from where we stopped.
	pageRankPushBudget = 100000
)

// pageRankSessionState holds the durable per-session PageRank state.
// Mutated under mu; the cache itself never reads through to fields,
// only adds/evicts entries.
type pageRankSessionState struct {
	mu sync.Mutex

	sessionID             string
	lastReconciledSeq     int64

	// Graph adjacency at the last reconcile point. Mutated in place
	// when reconcile detects edge changes.
	branchIDs       []string
	indexByID       map[string]int
	outEdges        [][]pageRankEdge // normalized weights
	rawWeights      [][]pageRankEdge // raw weights for rebuilds
	outDegrees      []float64
	inhibitorIn     [][]pageRankEdge
	conductances    [][]pageRankEdge
	personalization []float64

	// PageRank state. r is the served signal; p is unsettled mass.
	// invariant: a node is "settled" when p[u]/d[u] ≤ epsilon.
	r []float64
	p []float64
}

// pageRankIncrementalCache maps sessionID → state with LRU eviction.
// Entries are pointers so mutations under per-state mu don't require
// re-inserting into the map.
type pageRankIncrementalCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*pageRankSessionState
	order    []string
}

func newPageRankIncrementalCache(capacity int) *pageRankIncrementalCache {
	if capacity <= 0 {
		capacity = pageRankCacheCapacity
	}
	return &pageRankIncrementalCache{
		capacity: capacity,
		entries:  make(map[string]*pageRankSessionState, capacity),
		order:    make([]string, 0, capacity),
	}
}

// get returns the cached state for sessionID, promoting it to MRU.
// Returns (nil, false) on miss.
func (c *pageRankIncrementalCache) get(sessionID string) (*pageRankSessionState, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.entries[sessionID]
	if !ok {
		return nil, false
	}
	c.promoteToMRU(sessionID)
	return state, true
}

// putIfAbsent stores a new entry only when no entry exists for the
// sessionID. Returns the stored entry (caller's or pre-existing).
// LRU eviction kicks in when at capacity.
func (c *pageRankIncrementalCache) putIfAbsent(sessionID string, state *pageRankSessionState) *pageRankSessionState {
	if c == nil {
		return state
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[sessionID]; ok {
		c.promoteToMRU(sessionID)
		return existing
	}
	if len(c.entries) >= c.capacity {
		c.evictLRU()
	}
	c.entries[sessionID] = state
	c.order = append(c.order, sessionID)
	return state
}

func (c *pageRankIncrementalCache) promoteToMRU(sessionID string) {
	for i, k := range c.order {
		if k != sessionID {
			continue
		}
		c.order = append(c.order[:i], c.order[i+1:]...)
		c.order = append(c.order, sessionID)
		return
	}
}

func (c *pageRankIncrementalCache) evictLRU() {
	if len(c.order) == 0 {
		return
	}
	victim := c.order[0]
	c.order = c.order[1:]
	delete(c.entries, victim)
}

// ────────────────────────────────────────────────────────────────────
// State construction + initial fill
// ────────────────────────────────────────────────────────────────────

// initPageRankSessionState builds a fresh session state by loading
// the full graph and running power iteration to convergence. The
// resulting state has r=true_PR, p=0, lastReconciledSeq=current.
//
// Used on cache miss — the first Retrieve for a session pays the
// full-recompute cost. Subsequent Retrievals against the same
// session benefit from incremental reconciliation.
func (m *MemoryForest) initPageRankSessionState(
	ctx context.Context,
	sessionID string,
	canopyRoots map[string]struct{},
) (*pageRankSessionState, error) {
	branches, err := m.loadAllSessionBranches(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("init: load session branches: %w", err)
	}
	if len(branches) == 0 {
		// Empty session — empty state. Cached so the next call
		// short-circuits without re-querying.
		return &pageRankSessionState{
			sessionID:         sessionID,
			lastReconciledSeq: m.currentBranchProjectionSeq(ctx),
		}, nil
	}
	graph, err := m.loadPageRankGraph(ctx, sessionID, branches)
	if err != nil {
		return nil, fmt.Errorf("init: load graph: %w", err)
	}
	personalization := buildPersonalizationVector(branches, canopyRoots)
	rank := powerIteratePageRank(graph, personalization)

	state := &pageRankSessionState{
		sessionID:         sessionID,
		lastReconciledSeq: m.currentBranchProjectionSeq(ctx),
		branchIDs:         graph.branchIDs,
		indexByID:         graph.indexByID,
		outEdges:          graph.outEdges,
		rawWeights:        m.captureRawWeights(graph),
		outDegrees:        graph.outDegrees,
		inhibitorIn:       graph.inhibitorIn,
		conductances:      graph.conductances,
		personalization:   personalization,
		r:                 rank,
		p:                 make([]float64, len(graph.branchIDs)),
	}
	return state, nil
}

// captureRawWeights snapshots the raw (un-normalized) edge weights
// alongside the normalized graph. Reconcile needs raw weights to
// detect "this edge's weight changed" — comparing normalized weights
// would conflate genuine changes with renormalization shifts.
//
// loadPageRankGraph already mutates outEdges[].weight to be
// normalized. We grab a parallel copy of the raw weights (recovered
// by re-multiplying by outDegrees). For an edge i in source u,
// raw_weight = normalized_weight × outDegrees[u].
func (m *MemoryForest) captureRawWeights(graph *pageRankGraph) [][]pageRankEdge {
	out := make([][]pageRankEdge, len(graph.outEdges))
	for src, edges := range graph.outEdges {
		raw := make([]pageRankEdge, len(edges))
		for i, e := range edges {
			raw[i] = pageRankEdge{
				target: e.target,
				weight: e.weight * graph.outDegrees[src],
			}
		}
		out[src] = raw
	}
	return out
}

// ────────────────────────────────────────────────────────────────────
// Reconcile — apply edge changes since cursor
// ────────────────────────────────────────────────────────────────────

// reconcile detects edges whose last_applied_seq > state.lastReconciledSeq,
// applies the changes (graph mutation + endpoint reset r→p), then
// runs push to convergence. Bumps lastReconciledSeq. Caller must
// hold state.mu.
//
// Strategy:
//
//  1. Detect session-branch growth: new branches added to the
//     session AFTER state init aren't in the cached subgraph;
//     edge-change reconcile silently skips edges referencing them.
//     If the live branch count differs from the cached count, the
//     subgraph is stale — re-init in place and skip edge reconcile
//     (init captures everything fresh).
//  2. Otherwise: query forest_relay_edges where last_applied_seq >
//     cursor. We do NOT pre-filter by session here; the index on
//     last_applied_seq keeps the scan tight, and we filter in-Go
//     to edges whose endpoints are in this session's index.
//  3. For each changed edge, update the raw weight + normalized
//     weight, recomputing outDegrees for the affected source. Mark
//     affected endpoints (source + target) for reset.
//  4. For each affected endpoint, move r[u] → p[u] (add to residual,
//     zero settled rank). The push invariant is preserved.
//  5. Run push under the new graph until residual is settled or
//     budget exhausted.
//
// Idempotent — re-running with no new changes is a no-op.
func (m *MemoryForest) reconcilePageRankState(
	ctx context.Context,
	state *pageRankSessionState,
) error {
	currentSeq := m.currentBranchProjectionSeq(ctx)
	if currentSeq <= state.lastReconciledSeq {
		return nil
	}
	stale, err := m.detectSubgraphStaleness(ctx, state)
	if err != nil {
		return fmt.Errorf("reconcile: detect staleness: %w", err)
	}
	if stale {
		if err := m.reinitPageRankStateInPlace(ctx, state); err != nil {
			return fmt.Errorf("reconcile: reinit stale state: %w", err)
		}
		state.lastReconciledSeq = currentSeq
		return nil
	}
	changes, err := m.loadRelayEdgeChanges(ctx, state.lastReconciledSeq, currentSeq)
	if err != nil {
		return fmt.Errorf("reconcile: load edge changes: %w", err)
	}
	if len(changes) == 0 {
		state.lastReconciledSeq = currentSeq
		return nil
	}
	affected := state.applyEdgeChanges(changes)
	if len(affected) > 0 {
		state.unsettleNodes(affected)
		state.runPushBudgeted(pageRankIncrementalEpsilon, pageRankPushBudget)
	}
	state.lastReconciledSeq = currentSeq
	return nil
}

// detectSubgraphStaleness compares the live count of session branches
// against the cached count. A mismatch means the session has grown
// (or shrunk via supersedes) and our subgraph view is incomplete —
// re-init is the safe answer because edge-change reconcile alone
// can't introduce new index slots without breaking the per-node
// slices' length invariant.
func (m *MemoryForest) detectSubgraphStaleness(ctx context.Context, state *pageRankSessionState) (bool, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM   forest_branches
		WHERE  session_id = ?
		  AND  state != ?
	`, state.sessionID, string(BranchStateSuperseded))
	var liveCount int
	if err := row.Scan(&liveCount); err != nil {
		return false, fmt.Errorf("count session branches: %w", err)
	}
	return liveCount != len(state.branchIDs), nil
}

// reinitPageRankStateInPlace rebuilds every field of state from the
// live graph. Caller holds state.mu so external readers see a
// consistent snapshot — they wait, then read the fresh state.
//
// Personalization is left as the original (canopy roots from init);
// the canopy doesn't churn often, and re-deriving it during reconcile
// would require threading canopyRoots through reconcile (currently
// only the init path receives them). If canopy mutations become a
// real concern, surface canopy-version tracking on the state and
// rederive when canopy advances.
func (m *MemoryForest) reinitPageRankStateInPlace(ctx context.Context, state *pageRankSessionState) error {
	branches, err := m.loadAllSessionBranches(ctx, state.sessionID)
	if err != nil {
		return fmt.Errorf("reinit: load session branches: %w", err)
	}
	if len(branches) == 0 {
		// Session emptied. Reset all slices to zero-length so the
		// state is consistent with the empty subgraph; readers see
		// the zero substrateSignal default for every requested
		// branch (signalsFor's not-in-index path).
		state.branchIDs = nil
		state.indexByID = map[string]int{}
		state.outEdges = nil
		state.rawWeights = nil
		state.outDegrees = nil
		state.inhibitorIn = nil
		state.conductances = nil
		state.r = nil
		state.p = nil
		return nil
	}
	graph, err := m.loadPageRankGraph(ctx, state.sessionID, branches)
	if err != nil {
		return fmt.Errorf("reinit: load graph: %w", err)
	}
	personalization := state.personalization
	if len(personalization) != len(branches) {
		// Personalization length must match new branch count. If we
		// don't have a current canopy snapshot, fall back to uniform
		// personalization — the next canopy-aware retrieval will
		// build a properly personalized vector when the cache evicts.
		personalization = uniformPersonalizationVector(len(branches))
	}
	rank := powerIteratePageRank(graph, personalization)

	state.branchIDs = graph.branchIDs
	state.indexByID = graph.indexByID
	state.outEdges = graph.outEdges
	state.rawWeights = m.captureRawWeights(graph)
	state.outDegrees = graph.outDegrees
	state.inhibitorIn = graph.inhibitorIn
	state.conductances = graph.conductances
	state.personalization = personalization
	state.r = rank
	state.p = make([]float64, len(graph.branchIDs))
	return nil
}

// uniformPersonalizationVector returns a uniform distribution over
// n nodes. Used as a fallback when reinit happens without a fresh
// canopy.
func uniformPersonalizationVector(n int) []float64 {
	if n <= 0 {
		return nil
	}
	share := 1.0 / float64(n)
	out := make([]float64, n)
	for i := range out {
		out[i] = share
	}
	return out
}

// pageRankRelayEdgeChange is one row from the changed-edge query.
// Distinct from pageRankEdge (which uses int indices) — these are
// fresh from DB, identified by branch ID.
type pageRankRelayEdgeChange struct {
	source     string
	target     string
	relation   string
	weight     float64
	appliedSeq int64
}

// loadRelayEdgeChanges fetches relay edges with last_applied_seq in
// the half-open range (cursor, currentSeq]. The index on (
// last_applied_seq) keeps this scan small even on busy systems.
//
// Bounded by a per-call limit so a sudden burst doesn't make a
// single Retrieve do unbounded work; if more changes exist, the
// next Retrieve picks them up via cursor advance. The limit is
// generous (10k) so typical busy windows clear in one pass.
func (m *MemoryForest) loadRelayEdgeChanges(ctx context.Context, afterSeq, throughSeq int64) ([]pageRankRelayEdgeChange, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT source_branch_id, target_branch_id, relation, weight, last_applied_seq
		FROM   forest_relay_edges
		WHERE  last_applied_seq > ?
		  AND  last_applied_seq <= ?
		ORDER BY last_applied_seq ASC
		LIMIT  10000
	`, afterSeq, throughSeq)
	if err != nil {
		return nil, fmt.Errorf("query relay edge changes: %w", err)
	}
	defer rows.Close()
	var out []pageRankRelayEdgeChange
	for rows.Next() {
		var c pageRankRelayEdgeChange
		if err := rows.Scan(&c.source, &c.target, &c.relation, &c.weight, &c.appliedSeq); err != nil {
			return nil, fmt.Errorf("scan relay edge change: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// applyEdgeChanges updates the in-memory graph for each change whose
// endpoints are in the cached session subgraph. Returns the set of
// node indices whose state must be reset (source + target of every
// applied change).
//
// For each change:
//   - Update / insert the edge in rawWeights[srcIdx]
//   - Recompute outDegrees[srcIdx] = sum of raw weights
//   - Re-normalize outEdges[srcIdx] from rawWeights[srcIdx] / outDegrees
//   - Update inhibitor + conductance lists for contradicts/supersedes
//   - Add (srcIdx, tgtIdx) to the affected set
//
// Edges crossing out of the subgraph (one endpoint not in indexByID)
// are silently skipped — they don't influence this session's PR.
func (s *pageRankSessionState) applyEdgeChanges(changes []pageRankRelayEdgeChange) map[int]struct{} {
	affected := make(map[int]struct{})
	for _, change := range changes {
		srcIdx, srcOK := s.indexByID[change.source]
		tgtIdx, tgtOK := s.indexByID[change.target]
		if !srcOK || !tgtOK {
			continue
		}
		// Defensive bounds check: indexByID is built from branchIDs
		// at init/reinit time, and per-node slices are sized to the
		// same length. If any future mutation breaks the invariant,
		// we'd otherwise corrupt memory or panic on slice access.
		if !s.indexInBounds(srcIdx) || !s.indexInBounds(tgtIdx) {
			continue
		}
		s.upsertRawEdge(srcIdx, tgtIdx, change.weight)
		s.normalizeOutgoingForSource(srcIdx)
		s.upsertRelationLists(srcIdx, tgtIdx, change.weight, change.relation)
		affected[srcIdx] = struct{}{}
		affected[tgtIdx] = struct{}{}
	}
	return affected
}

// indexInBounds asserts that idx is a valid index into every per-
// node slice. The init + reinit paths size r/p/outEdges/rawWeights/
// outDegrees/inhibitorIn/conductances to len(branchIDs). This guard
// is the safety net if any future code path desynchronizes those.
func (s *pageRankSessionState) indexInBounds(idx int) bool {
	if idx < 0 {
		return false
	}
	return idx < len(s.r) &&
		idx < len(s.p) &&
		idx < len(s.outDegrees) &&
		idx < len(s.outEdges) &&
		idx < len(s.rawWeights) &&
		idx < len(s.inhibitorIn) &&
		idx < len(s.conductances)
}

// upsertRawEdge inserts a new edge or replaces an existing edge's
// weight in rawWeights[srcIdx]. The list is small (per-source out-
// degree typically < 50) so linear search is fine.
func (s *pageRankSessionState) upsertRawEdge(srcIdx, tgtIdx int, weight float64) {
	for i, e := range s.rawWeights[srcIdx] {
		if e.target == tgtIdx {
			s.rawWeights[srcIdx][i].weight = weight
			return
		}
	}
	s.rawWeights[srcIdx] = append(s.rawWeights[srcIdx], pageRankEdge{target: tgtIdx, weight: weight})
}

// normalizeOutgoingForSource recomputes outDegrees[srcIdx] from raw
// weights and rebuilds outEdges[srcIdx] as the normalized list.
// Single-source rebuild — unaffected sources are untouched.
func (s *pageRankSessionState) normalizeOutgoingForSource(srcIdx int) {
	var mass float64
	for _, e := range s.rawWeights[srcIdx] {
		mass += e.weight
	}
	s.outDegrees[srcIdx] = mass
	if mass <= 0 {
		s.outEdges[srcIdx] = nil
		return
	}
	rebuilt := make([]pageRankEdge, len(s.rawWeights[srcIdx]))
	for i, e := range s.rawWeights[srcIdx] {
		rebuilt[i] = pageRankEdge{target: e.target, weight: e.weight / mass}
	}
	s.outEdges[srcIdx] = rebuilt
}

// upsertRelationLists keeps inhibitor + conductance adjacency in
// sync with rawWeights. Conductance mirrors raw weights; inhibitor
// is filtered to contradicts/supersedes only.
func (s *pageRankSessionState) upsertRelationLists(srcIdx, tgtIdx int, weight float64, relation string) {
	s.upsertRelationEdge(s.conductances, srcIdx, tgtIdx, weight)
	if relation == pageRankInhibitionContradicts || relation == pageRankInhibitionSupersedes {
		// Inhibitor is INCOMING at the target.
		s.upsertRelationEdge(s.inhibitorIn, tgtIdx, srcIdx, weight)
	}
}

// upsertRelationEdge mutates a per-node adjacency slice. Same
// semantics as upsertRawEdge: linear search, in-place update or
// append.
func (s *pageRankSessionState) upsertRelationEdge(adj [][]pageRankEdge, srcIdx, tgtIdx int, weight float64) {
	for i, e := range adj[srcIdx] {
		if e.target == tgtIdx {
			adj[srcIdx][i].weight = weight
			return
		}
	}
	adj[srcIdx] = append(adj[srcIdx], pageRankEdge{target: tgtIdx, weight: weight})
}

// unsettleNodes moves settled rank back to residual at the given
// nodes. Standard preconditioner before push — gives the algorithm
// fresh mass to redistribute through the new graph topology.
//
// The push invariant `r + push(p)·M_new = true_PR_under_M_new` is
// re-established by re-running push to convergence after this.
func (s *pageRankSessionState) unsettleNodes(nodes map[int]struct{}) {
	for idx := range nodes {
		if idx < 0 || idx >= len(s.p) || idx >= len(s.r) {
			continue
		}
		s.p[idx] += s.r[idx]
		s.r[idx] = 0
	}
}

// ────────────────────────────────────────────────────────────────────
// Push algorithm — Andersen-Chung-Lang
// ────────────────────────────────────────────────────────────────────

// runPushBudgeted runs worklist-based push until either residual is
// below epsilon at every node, or `budget` operations are spent.
// Returns the number of push operations performed.
//
// The algorithm:
//
//  1. Build initial worklist: every node where p[u]/d[u] > epsilon
//     (or for dangling nodes with d[u]=0, p[u] > epsilon).
//  2. While worklist non-empty AND budget > 0:
//     - pop a node u
//     - settle u: r[u] += (1-α)·p[u]; redistribute α·p[u] over
//       neighbors via M_uv; zero p[u]
//     - any neighbor whose residual now exceeds threshold gets
//       added to the worklist
//
// Convergence: each push reduces total residual by a factor of α.
// Bounded work per change: only nodes touched by the change (and
// their neighborhoods) appear in the worklist.
func (s *pageRankSessionState) runPushBudgeted(epsilon float64, budget int) int {
	n := len(s.r)
	if n == 0 {
		return 0
	}
	worklist := make([]int, 0, n)
	inWorklist := make([]bool, n)

	// Seed: any node already over threshold.
	for u := 0; u < n; u++ {
		if s.nodeOverThreshold(u, epsilon) {
			worklist = append(worklist, u)
			inWorklist[u] = true
		}
	}
	ops := 0
	for len(worklist) > 0 && ops < budget {
		u := worklist[0]
		worklist = worklist[1:]
		inWorklist[u] = false

		touched := s.pushOne(u)
		ops++

		for _, v := range touched {
			if inWorklist[v] {
				continue
			}
			if s.nodeOverThreshold(v, epsilon) {
				worklist = append(worklist, v)
				inWorklist[v] = true
			}
		}
	}
	return ops
}

// nodeOverThreshold returns true when node u still needs pushing
// (p[u]/d[u] > epsilon, or for dangling nodes p[u] > epsilon).
// Guards against any per-slice length divergence — even if a future
// mutation leaves p / outDegrees out of sync, the bounds check
// prevents a slice-index panic.
func (s *pageRankSessionState) nodeOverThreshold(u int, epsilon float64) bool {
	if u < 0 || u >= len(s.p) || u >= len(s.outDegrees) {
		return false
	}
	if s.outDegrees[u] <= 0 {
		return s.p[u] > epsilon
	}
	return s.p[u]/s.outDegrees[u] > epsilon
}

// pushOne settles node u by transferring (1-α)·p[u] to r[u] and
// redistributing α·p[u] across neighbors (or, for dangling nodes,
// across the personalization vector).
//
// Returns the indices of nodes whose residual was modified — caller
// uses these to repopulate the worklist. All slice accesses are
// bounds-checked: a malformed graph (edge.target outside the rank
// vector, or a personalization entry past len(p)) is silently
// skipped rather than panicking.
func (s *pageRankSessionState) pushOne(u int) []int {
	if u < 0 || u >= len(s.p) || u >= len(s.r) || u >= len(s.outDegrees) {
		return nil
	}
	rho := s.p[u]
	if rho <= 0 {
		return nil
	}
	s.r[u] += (1 - pageRankAlpha) * rho
	if s.outDegrees[u] <= 0 {
		// Dangling node: redistribute α·rho over the personalization
		// vector. All nodes with personalization > 0 are touched.
		s.p[u] = 0
		share := pageRankAlpha * rho
		touched := make([]int, 0)
		for v, weight := range s.personalization {
			if v < 0 || v >= len(s.p) {
				continue
			}
			if weight <= 0 {
				continue
			}
			s.p[v] += share * weight
			touched = append(touched, v)
		}
		return touched
	}
	s.p[u] = 0
	teleport := pageRankAlpha * rho
	touched := make([]int, 0, len(s.outEdges[u]))
	for _, edge := range s.outEdges[u] {
		if edge.target < 0 || edge.target >= len(s.p) {
			continue
		}
		s.p[edge.target] += teleport * edge.weight
		touched = append(touched, edge.target)
	}
	return touched
}

// ────────────────────────────────────────────────────────────────────
// Read entry point — get-or-init, reconcile, project signals
// ────────────────────────────────────────────────────────────────────

// loadIncrementalPageRankSubstrateSignals replaces the version-keyed
// flow with a per-session incremental flow. Cache hits avoid the
// power-iteration recompute; a session whose graph_version advanced
// only does work proportional to changed-edge locality.
func (m *MemoryForest) loadIncrementalPageRankSubstrateSignals(
	ctx context.Context,
	branches []*Branch,
	canopy *Canopy,
) (map[string]substrateSignal, error) {
	result := make(map[string]substrateSignal, len(branches))
	if len(branches) == 0 {
		return result, nil
	}
	bySession := groupBranchesBySession(branches)
	canopyRoots := canopyRootSetFromCanopy(canopy)

	for sessionID, sessionBranches := range bySession {
		sigs, err := m.incrementalPageRankSignalsForSession(ctx, sessionID, sessionBranches, canopyRoots)
		if err != nil {
			return nil, fmt.Errorf("session %s pagerank: %w", sessionID, err)
		}
		for id, sig := range sigs {
			result[id] = sig
		}
	}
	return result, nil
}

// incrementalPageRankSignalsForSession returns substrate signals for
// the given session's subset of branches via the incremental cache.
// Holds the per-session mutex during reconcile so concurrent
// retrievals against the same session serialize through one push.
func (m *MemoryForest) incrementalPageRankSignalsForSession(
	ctx context.Context,
	sessionID string,
	branches []*Branch,
	canopyRoots map[string]struct{},
) (map[string]substrateSignal, error) {
	cache := m.pageRankIncrementalCache
	if state, ok := cache.get(sessionID); ok {
		return m.serveFromState(ctx, state, branches)
	}
	fresh, err := m.initPageRankSessionState(ctx, sessionID, canopyRoots)
	if err != nil {
		return nil, err
	}
	state := cache.putIfAbsent(sessionID, fresh)
	return m.serveFromState(ctx, state, branches)
}

// serveFromState locks the per-session state, reconciles any pending
// changes, and projects signals for the requested branch subset.
func (m *MemoryForest) serveFromState(
	ctx context.Context,
	state *pageRankSessionState,
	branches []*Branch,
) (map[string]substrateSignal, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := m.reconcilePageRankState(ctx, state); err != nil {
		// Reconcile failure shouldn't break retrieval; fall back to
		// the (possibly stale) signals already in the state.
		if m.logger != nil {
			m.logger.Debug("forest: pagerank reconcile failed",
				"session_id", state.sessionID, "err", err.Error())
		}
	}
	return state.signalsFor(branches), nil
}

// signalsFor projects state's r/inhibition/conductance into substrate
// signals for the requested branch subset. Branches not in the
// state's index get a zero substrateSignal (matching the Layer 2
// projectCachedSignals semantics).
func (s *pageRankSessionState) signalsFor(branches []*Branch) map[string]substrateSignal {
	out := make(map[string]substrateSignal, len(branches))
	for _, b := range branches {
		if b == nil {
			continue
		}
		idx, ok := s.indexByID[b.ID]
		if !ok {
			out[b.ID] = substrateSignal{}
			continue
		}
		out[b.ID] = substrateSignal{
			Potential:       s.r[idx],
			Frontier:        s.r[idx] * (1 - accessPressure(b)),
			Inhibition:      s.computeInhibition(idx),
			ConductanceMass: s.computeConductanceMass(idx),
		}
	}
	return out
}

func (s *pageRankSessionState) computeInhibition(idx int) float64 {
	if idx < 0 || idx >= len(s.inhibitorIn) {
		return 0
	}
	var sum float64
	for _, edge := range s.inhibitorIn[idx] {
		if edge.target < 0 || edge.target >= len(s.r) {
			continue
		}
		sum += edge.weight * s.r[edge.target]
	}
	if sum < 0 {
		return 0
	}
	if sum > 1 {
		return 1
	}
	return sum
}

func (s *pageRankSessionState) computeConductanceMass(idx int) float64 {
	if idx < 0 || idx >= len(s.conductances) {
		return 0
	}
	var sum float64
	for _, edge := range s.conductances[idx] {
		if edge.target < 0 || edge.target >= len(s.r) {
			continue
		}
		sum += edge.weight * s.r[edge.target]
	}
	return sum
}

// ────────────────────────────────────────────────────────────────────
// Diagnostics — operator-visible stats
// ────────────────────────────────────────────────────────────────────

// PageRankCacheStats reports cache occupancy + per-session staleness
// so operators can verify the incremental path is hitting cache.
// Read-only; safe to call concurrently with serving traffic.
type PageRankCacheStats struct {
	SessionCount  int                            `json:"session_count"`
	Capacity      int                            `json:"capacity"`
	Sessions      []PageRankCacheSessionStat     `json:"sessions"`
}

type PageRankCacheSessionStat struct {
	SessionID         string `json:"session_id"`
	BranchCount       int    `json:"branch_count"`
	LastReconciledSeq int64  `json:"last_reconciled_seq"`
}

// PageRankCacheStats returns a snapshot of the incremental cache.
// Useful as a Health() input or for ad-hoc operator queries.
func (m *MemoryForest) PageRankCacheStats() PageRankCacheStats {
	if m == nil || m.pageRankIncrementalCache == nil {
		return PageRankCacheStats{}
	}
	c := m.pageRankIncrementalCache
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := PageRankCacheStats{
		SessionCount: len(c.entries),
		Capacity:     c.capacity,
		Sessions:     make([]PageRankCacheSessionStat, 0, len(c.entries)),
	}
	for _, sid := range c.order {
		state, ok := c.entries[sid]
		if !ok {
			continue
		}
		// state.mu briefly so int64 reads are atomic and slice
		// length is consistent. Lock order (cache.mu → state.mu) is
		// safe because no path acquires state.mu before cache.mu;
		// serve paths take cache.mu, release, then take state.mu.
		state.mu.Lock()
		stat := PageRankCacheSessionStat{
			SessionID:         sid,
			BranchCount:       len(state.branchIDs),
			LastReconciledSeq: state.lastReconciledSeq,
		}
		state.mu.Unlock()
		stats.Sessions = append(stats.Sessions, stat)
	}
	return stats
}

