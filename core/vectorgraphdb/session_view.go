package vectorgraphdb

import (
	"errors"
	"sync"
)

// ErrViewClosed is returned when operations are attempted on a closed view.
var ErrViewClosed = errors.New("session view is closed")

// IsolationLevel controls what data a session can see.
type IsolationLevel int

const (
	// IsolationReadCommitted sees all committed data from all sessions.
	IsolationReadCommitted IsolationLevel = iota

	// IsolationRepeatableRead provides snapshot at view creation for consistent reads.
	IsolationRepeatableRead

	// IsolationSessionLocal only sees own session's data plus global data.
	IsolationSessionLocal
)

// String returns the string representation of the isolation level.
func (il IsolationLevel) String() string {
	switch il {
	case IsolationReadCommitted:
		return "read_committed"
	case IsolationRepeatableRead:
		return "repeatable_read"
	case IsolationSessionLocal:
		return "session_local"
	default:
		return "unknown"
	}
}

// IsValid returns true if the isolation level is a recognized value.
func (il IsolationLevel) IsValid() bool {
	switch il {
	case IsolationReadCommitted, IsolationRepeatableRead, IsolationSessionLocal:
		return true
	default:
		return false
	}
}

// NodeFilter defines criteria for filtering nodes in queries.
type NodeFilter struct {
	Domains   []Domain
	NodeTypes []NodeType
	SessionID string // Filter to specific session (empty = all sessions)
	Limit     int
}

// SessionScopedView provides isolated access to VectorGraphDB.
type SessionScopedView struct {
	sessionID   string
	db          *VectorGraphDB
	snapshotMgr SnapshotManager
	snapshot    Snapshot       // for repeatable reads
	liveIndex   VectorIndex    // for read committed
	isolation   IsolationLevel
	optTx       *OptimisticTx // for write operations
	closed      bool
	mu          sync.RWMutex
}

// SessionViewConfig holds configuration for creating a SessionScopedView.
type SessionViewConfig struct {
	SessionID      string
	DB             *VectorGraphDB
	SnapshotMgr    SnapshotManager
	LiveIndex      VectorIndex
	Isolation      IsolationLevel
	VersionedStore *VersionedNodeStore
}

// NewSessionScopedView creates an isolated view for a session.
func NewSessionScopedView(cfg SessionViewConfig) *SessionScopedView {
	view := &SessionScopedView{
		sessionID:   cfg.SessionID,
		db:          cfg.DB,
		snapshotMgr: cfg.SnapshotMgr,
		liveIndex:   cfg.LiveIndex,
		isolation:   cfg.Isolation,
	}

	if cfg.Isolation == IsolationRepeatableRead && cfg.SnapshotMgr != nil {
		view.snapshot = cfg.SnapshotMgr.CreateSnapshot()
	}

	return view
}

// SessionID returns the session ID for this view.
func (v *SessionScopedView) SessionID() string {
	return v.sessionID
}

// Isolation returns the isolation level for this view.
func (v *SessionScopedView) Isolation() IsolationLevel {
	return v.isolation
}

// IsClosed returns true if the view has been closed.
func (v *SessionScopedView) IsClosed() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.closed
}

// QueryNodes returns nodes filtered by isolation level.
func (v *SessionScopedView) QueryNodes(filter NodeFilter) ([]*GraphNode, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return nil, ErrViewClosed
	}

	return v.executeQuery(filter)
}

// executeQuery runs the query with appropriate isolation semantics.
func (v *SessionScopedView) executeQuery(filter NodeFilter) ([]*GraphNode, error) {
	ns := NewNodeStore(v.db, nil)
	nodes, err := v.fetchNodesFromDB(ns, filter)
	if err != nil {
		return nil, err
	}

	return v.applyIsolationFilter(nodes), nil
}

// fetchNodesFromDB retrieves nodes from the database based on filter.
func (v *SessionScopedView) fetchNodesFromDB(ns *NodeStore, filter NodeFilter) ([]*GraphNode, error) {
	if len(filter.Domains) == 0 || len(filter.NodeTypes) == 0 {
		return v.fetchAllNodes(ns, filter.Limit)
	}

	return v.fetchFilteredNodes(ns, filter)
}

// fetchAllNodes retrieves all nodes up to the limit.
func (v *SessionScopedView) fetchAllNodes(ns *NodeStore, limit int) ([]*GraphNode, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := v.db.db.Query(`
		SELECT id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by
		FROM nodes LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return ns.scanNodes(rows)
}

// fetchFilteredNodes retrieves nodes matching domain/type filters.
func (v *SessionScopedView) fetchFilteredNodes(ns *NodeStore, filter NodeFilter) ([]*GraphNode, error) {
	limit := normalizeLimit(filter.Limit)
	allNodes, err := v.collectNodesByFilter(ns, filter, limit)
	if err != nil {
		return nil, err
	}
	return v.truncateToLimit(allNodes, limit), nil
}

// normalizeLimit returns a valid limit value.
func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	return limit
}

// collectNodesByFilter gathers nodes matching all domain/type combinations.
func (v *SessionScopedView) collectNodesByFilter(ns *NodeStore, filter NodeFilter, limit int) ([]*GraphNode, error) {
	var allNodes []*GraphNode
	for _, domain := range filter.Domains {
		nodes, err := v.collectNodesForDomain(ns, domain, filter.NodeTypes, limit)
		if err != nil {
			return nil, err
		}
		allNodes = append(allNodes, nodes...)
	}
	return allNodes, nil
}

// collectNodesForDomain gathers nodes for a single domain across all node types.
func (v *SessionScopedView) collectNodesForDomain(ns *NodeStore, domain Domain, nodeTypes []NodeType, limit int) ([]*GraphNode, error) {
	var nodes []*GraphNode
	for _, nodeType := range nodeTypes {
		batch, err := ns.GetNodesByType(domain, nodeType, limit)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, batch...)
	}
	return nodes, nil
}

// truncateToLimit limits the result set to the specified size.
func (v *SessionScopedView) truncateToLimit(nodes []*GraphNode, limit int) []*GraphNode {
	if len(nodes) <= limit {
		return nodes
	}
	return nodes[:limit]
}

// applyIsolationFilter applies isolation-level-specific filtering.
func (v *SessionScopedView) applyIsolationFilter(nodes []*GraphNode) []*GraphNode {
	if v.isolation != IsolationSessionLocal {
		return nodes
	}

	return v.filterBySession(nodes)
}

// filterBySession filters nodes to only show session-local + global data.
func (v *SessionScopedView) filterBySession(nodes []*GraphNode) []*GraphNode {
	filtered := make([]*GraphNode, 0, len(nodes))
	for _, node := range nodes {
		if v.isVisibleToSession(node) {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// isVisibleToSession checks if a node is visible under SessionLocal isolation.
func (v *SessionScopedView) isVisibleToSession(node *GraphNode) bool {
	// Global data (no session) is visible to all
	if node.SessionID == "" {
		return true
	}
	// Session's own data is visible
	return node.SessionID == v.sessionID
}

// Search performs vector search respecting isolation level.
func (v *SessionScopedView) Search(query []float32, k int) []SearchResult {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return nil
	}

	return v.executeSearch(query, k)
}

// executeSearch performs the search with appropriate isolation.
func (v *SessionScopedView) executeSearch(query []float32, k int) []SearchResult {
	if v.isolation == IsolationRepeatableRead && v.snapshot != nil {
		return v.searchSnapshot(query, k)
	}

	return v.searchLiveIndex(query, k)
}

// searchSnapshot searches the frozen snapshot.
func (v *SessionScopedView) searchSnapshot(query []float32, k int) []SearchResult {
	hnswResults := v.snapshot.Search(query, k, nil)
	return v.convertResults(hnswResults)
}

// searchLiveIndex searches the live index.
func (v *SessionScopedView) searchLiveIndex(query []float32, k int) []SearchResult {
	if v.liveIndex == nil {
		return nil
	}

	hnswResults := v.liveIndex.Search(query, k, nil)
	results := v.convertLiveResults(hnswResults)

	if v.isolation == IsolationSessionLocal {
		return v.filterSearchResults(results)
	}

	return results
}

// convertResults converts snapshot search results to SearchResult.
func (v *SessionScopedView) convertResults(snapshotResults []SnapshotSearchResult) []SearchResult {
	results := make([]SearchResult, 0, len(snapshotResults))
	ns := NewNodeStore(v.db, nil)

	for _, sr := range snapshotResults {
		result := v.loadSearchResult(ns, sr.ID, sr.Similarity)
		if result != nil {
			results = append(results, *result)
		}
	}

	return results
}

// convertLiveResults converts live index search results.
func (v *SessionScopedView) convertLiveResults(indexResults []SnapshotSearchResult) []SearchResult {
	results := make([]SearchResult, 0, len(indexResults))
	ns := NewNodeStore(v.db, nil)

	for _, ir := range indexResults {
		result := v.loadSearchResult(ns, ir.ID, ir.Similarity)
		if result != nil {
			results = append(results, *result)
		}
	}

	return results
}

// loadSearchResult loads a node and creates a SearchResult.
func (v *SessionScopedView) loadSearchResult(ns *NodeStore, id string, similarity float64) *SearchResult {
	node, err := ns.GetNode(id)
	if err != nil {
		return nil
	}
	return &SearchResult{Node: node, Similarity: similarity}
}

// filterSearchResults filters search results by session.
func (v *SessionScopedView) filterSearchResults(results []SearchResult) []SearchResult {
	filtered := make([]SearchResult, 0, len(results))
	for _, r := range results {
		if v.isVisibleToSession(r.Node) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// BeginWrite starts an optimistic transaction for writing.
func (v *SessionScopedView) BeginWrite(store *VersionedNodeStore) *OptimisticTx {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed || store == nil {
		return nil
	}

	v.optTx = store.BeginOptimistic(v.sessionID)
	return v.optTx
}

// GetTransaction returns the current transaction if one exists.
func (v *SessionScopedView) GetTransaction() *OptimisticTx {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.optTx
}

// CommitWrite commits pending writes.
func (v *SessionScopedView) CommitWrite() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return ErrViewClosed
	}

	if v.optTx == nil {
		return nil
	}

	err := v.optTx.Commit()
	v.optTx = nil
	return err
}

// RollbackWrite rolls back pending writes.
func (v *SessionScopedView) RollbackWrite() {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.optTx != nil {
		v.optTx.Rollback()
		v.optTx = nil
	}
}

// Close releases resources (snapshot, transaction).
func (v *SessionScopedView) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return nil // idempotent
	}

	v.releaseResources()
	v.closed = true
	return nil
}

// releaseResources cleans up snapshot and transaction.
func (v *SessionScopedView) releaseResources() {
	if v.snapshot != nil && v.snapshotMgr != nil {
		v.snapshotMgr.ReleaseSnapshot(v.snapshot.ID())
		v.snapshot = nil
	}

	if v.optTx != nil {
		v.optTx.Rollback()
		v.optTx = nil
	}
}
