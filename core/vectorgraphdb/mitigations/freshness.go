package mitigations

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// FreshnessTracker tracks data freshness and applies temporal decay.
type FreshnessTracker struct {
	db          *vectorgraphdb.VectorGraphDB
	decayConfig DecayConfig
	scannerMu   sync.RWMutex
	staleNodes  map[string]*FreshnessInfo
	maxStale    int
	staleOrder  []string
	stopChan    chan struct{}
	stopOnce    sync.Once
}

// NewFreshnessTracker creates a new FreshnessTracker.
func NewFreshnessTracker(db *vectorgraphdb.VectorGraphDB, config DecayConfig) *FreshnessTracker {
	return &FreshnessTracker{
		db:          db,
		decayConfig: config,
		staleNodes:  make(map[string]*FreshnessInfo),
		maxStale:    10000,
		staleOrder:  make([]string, 0),
		stopChan:    make(chan struct{}),
	}
}

// GetFreshness retrieves freshness information for a node.
func (f *FreshnessTracker) GetFreshness(nodeID string) (*FreshnessInfo, error) {
	f.scannerMu.RLock()
	if info, ok := f.staleNodes[nodeID]; ok {
		f.scannerMu.RUnlock()
		return info, nil
	}
	f.scannerMu.RUnlock()

	return f.computeFreshness(nodeID)
}

func (f *FreshnessTracker) computeFreshness(nodeID string) (*FreshnessInfo, error) {
	ns := vectorgraphdb.NewNodeStore(f.db, nil)
	node, err := ns.GetNode(nodeID)
	if err != nil {
		return nil, err
	}

	info := &FreshnessInfo{
		NodeID:       nodeID,
		LastVerified: node.UpdatedAt,
		IsStale:      false,
		DecayRate:    f.getDecayRate(node.Domain),
	}

	info.FreshnessScore = f.computeScore(node, info.DecayRate)
	info.IsStale = info.FreshnessScore < 0.5
	info.NextScanAt = time.Now().Add(computeScanInterval(info.FreshnessScore))

	return info, nil
}

func (f *FreshnessTracker) getDecayRate(domain vectorgraphdb.Domain) float64 {
	switch domain {
	case vectorgraphdb.DomainCode:
		return f.decayConfig.CodeDecayRate
	case vectorgraphdb.DomainHistory:
		return f.decayConfig.HistoryDecayRate
	case vectorgraphdb.DomainAcademic:
		return f.decayConfig.AcademicDecayRate
	default:
		return f.decayConfig.HistoryDecayRate
	}
}

func (f *FreshnessTracker) computeScore(node *vectorgraphdb.GraphNode, decayRate float64) float64 {
	ageHours := time.Since(node.UpdatedAt).Hours()
	decayScore := math.Exp(-decayRate * ageHours)

	accessRecency := time.Since(node.UpdatedAt).Hours()
	accessBoost := computeAccessBoost(accessRecency)

	return decayScore * accessBoost
}

func computeAccessBoost(recencyHours float64) float64 {
	if recencyHours < 1 {
		return 1.1
	}
	if recencyHours < 24 {
		return 1.05
	}
	if recencyHours < 168 {
		return 1.0
	}
	return 0.95
}

func computeScanInterval(freshnessScore float64) time.Duration {
	if freshnessScore > 0.8 {
		return 24 * time.Hour
	}
	if freshnessScore > 0.5 {
		return 6 * time.Hour
	}
	return 1 * time.Hour
}

// ApplyDecay applies freshness decay to search results.
func (f *FreshnessTracker) ApplyDecay(results []vectorgraphdb.SearchResult) []vectorgraphdb.SearchResult {
	decayed := make([]vectorgraphdb.SearchResult, len(results))
	for i, r := range results {
		decayed[i] = f.applyDecayToResult(r)
	}
	return decayed
}

func (f *FreshnessTracker) applyDecayToResult(result vectorgraphdb.SearchResult) vectorgraphdb.SearchResult {
	info, err := f.GetFreshness(result.Node.ID)
	if err != nil {
		return result
	}

	decayed := vectorgraphdb.SearchResult{
		Node:       result.Node,
		Similarity: result.Similarity * info.FreshnessScore,
	}
	return decayed
}

// MarkStale marks a node as stale.
func (f *FreshnessTracker) MarkStale(nodeID string) error {
	info, err := f.computeFreshness(nodeID)
	if err != nil {
		return err
	}

	info.IsStale = true
	info.FreshnessScore = 0.3

	f.scannerMu.Lock()
	f.addStaleNode(nodeID, info)
	f.scannerMu.Unlock()
	return nil
}

func (f *FreshnessTracker) addStaleNode(nodeID string, info *FreshnessInfo) {
	if _, exists := f.staleNodes[nodeID]; !exists {
		if len(f.staleNodes) >= f.maxStale {
			f.evictOldestStale()
		}
		f.staleOrder = append(f.staleOrder, nodeID)
	}
	f.staleNodes[nodeID] = info
}

func (f *FreshnessTracker) evictOldestStale() {
	if len(f.staleOrder) == 0 {
		return
	}
	oldest := f.staleOrder[0]
	f.staleOrder = f.staleOrder[1:]
	delete(f.staleNodes, oldest)
}

// RefreshNode re-verifies and updates a stale node.
func (f *FreshnessTracker) RefreshNode(ctx context.Context, nodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ns := vectorgraphdb.NewNodeStore(f.db, nil)
	if err := ns.TouchNode(nodeID); err != nil {
		return err
	}

	f.scannerMu.Lock()
	delete(f.staleNodes, nodeID)
	f.removeFromStaleOrder(nodeID)
	f.scannerMu.Unlock()

	return nil
}

func (f *FreshnessTracker) removeFromStaleOrder(nodeID string) {
	for i, id := range f.staleOrder {
		if id == nodeID {
			f.staleOrder = append(f.staleOrder[:i], f.staleOrder[i+1:]...)
			return
		}
	}
}

// FindStaleNodes finds nodes that are stale for a domain.
func (f *FreshnessTracker) FindStaleNodes(domain vectorgraphdb.Domain, threshold time.Duration) ([]*vectorgraphdb.GraphNode, error) {
	cutoff := time.Now().Add(-threshold).Format(time.RFC3339)

	rows, err := f.db.DB().Query(`
		SELECT id, domain, node_type, content_hash, metadata, created_at, updated_at
		FROM nodes WHERE domain = ? AND updated_at < ? LIMIT 1000
	`, domain, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return f.scanStaleNodes(rows)
}

func (f *FreshnessTracker) scanStaleNodes(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*vectorgraphdb.GraphNode, error) {
	var nodes []*vectorgraphdb.GraphNode
	for rows.Next() {
		node, err := f.scanNodeRow(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (f *FreshnessTracker) scanNodeRow(row interface{ Scan(...any) error }) (*vectorgraphdb.GraphNode, error) {
	var node vectorgraphdb.GraphNode
	var metadataJSON, createdAt, updatedAt string

	err := row.Scan(&node.ID, &node.Domain, &node.NodeType, &node.ContentHash,
		&metadataJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	node.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	node.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &node, nil
}

// StartScanner starts the background staleness scanner.
func (f *FreshnessTracker) StartScanner(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	f.scanLoop(ctx, ticker)
}

func (f *FreshnessTracker) scanLoop(ctx context.Context, ticker *time.Ticker) {
	for !f.shouldStopScanning(ctx, ticker) {
		f.runScan()
	}
}

func (f *FreshnessTracker) shouldStopScanning(ctx context.Context, ticker *time.Ticker) bool {
	select {
	case <-ctx.Done():
		return true
	case <-f.stopChan:
		return true
	case <-ticker.C:
		return false
	}
}

func (f *FreshnessTracker) runScan() {
	domains := []vectorgraphdb.Domain{
		vectorgraphdb.DomainCode,
		vectorgraphdb.DomainHistory,
		vectorgraphdb.DomainAcademic,
	}

	for _, domain := range domains {
		threshold := f.getStalenessThreshold(domain)
		nodes, err := f.FindStaleNodes(domain, threshold)
		if err != nil {
			continue
		}
		f.markNodesStale(nodes)
	}
}

func (f *FreshnessTracker) getStalenessThreshold(domain vectorgraphdb.Domain) time.Duration {
	switch domain {
	case vectorgraphdb.DomainCode:
		return 7 * 24 * time.Hour
	case vectorgraphdb.DomainHistory:
		return 30 * 24 * time.Hour
	case vectorgraphdb.DomainAcademic:
		return 90 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

func (f *FreshnessTracker) markNodesStale(nodes []*vectorgraphdb.GraphNode) {
	f.scannerMu.Lock()
	defer f.scannerMu.Unlock()

	for _, node := range nodes {
		info := &FreshnessInfo{
			NodeID:         node.ID,
			LastVerified:   node.UpdatedAt,
			IsStale:        true,
			FreshnessScore: 0.3,
			DecayRate:      f.getDecayRate(node.Domain),
		}
		f.addStaleNode(node.ID, info)
	}
}

// StopScanner stops the background scanner. Safe to call multiple times.
func (f *FreshnessTracker) StopScanner() {
	f.stopOnce.Do(func() {
		close(f.stopChan)
	})
}

// GetStaleCount returns the number of known stale nodes.
func (f *FreshnessTracker) GetStaleCount() int {
	f.scannerMu.RLock()
	defer f.scannerMu.RUnlock()
	return len(f.staleNodes)
}

// Close releases resources. Stops the scanner and clears caches.
func (f *FreshnessTracker) Close() {
	f.StopScanner()
	f.scannerMu.Lock()
	f.staleNodes = make(map[string]*FreshnessInfo)
	f.staleOrder = make([]string, 0)
	f.scannerMu.Unlock()
}
