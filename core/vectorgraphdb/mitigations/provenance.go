package mitigations

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/google/uuid"
)

// ProvenanceTracker tracks the source and verification chain for stored information.
type ProvenanceTracker struct {
	db              *vectorgraphdb.VectorGraphDB
	cacheMu         sync.RWMutex
	provenanceCache map[string][]*Provenance
	maxCacheSize    int
	cacheOrder      []string
}

// NewProvenanceTracker creates a new ProvenanceTracker.
func NewProvenanceTracker(db *vectorgraphdb.VectorGraphDB) *ProvenanceTracker {
	return &ProvenanceTracker{
		db:              db,
		provenanceCache: make(map[string][]*Provenance),
		maxCacheSize:    10000,
		cacheOrder:      make([]string, 0),
	}
}

// RecordProvenance records provenance for a node.
func (p *ProvenanceTracker) RecordProvenance(nodeID string, prov *Provenance) error {
	if prov.ID == "" {
		prov.ID = uuid.New().String()
	}
	prov.NodeID = nodeID

	provJSON, err := json.Marshal(prov)
	if err != nil {
		return fmt.Errorf("marshal provenance: %w", err)
	}

	_, err = p.db.DB().Exec(`
		INSERT INTO provenance (id, node_id, source_type, source_id, confidence, verified_at, verifier)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, prov.ID, prov.NodeID, prov.SourceType, prov.SourceID, prov.Confidence,
		prov.VerifiedAt.Format(time.RFC3339), string(provJSON))
	if err != nil {
		return fmt.Errorf("insert provenance: %w", err)
	}

	p.invalidateCache(nodeID)
	return nil
}

func (p *ProvenanceTracker) invalidateCache(nodeID string) {
	p.cacheMu.Lock()
	delete(p.provenanceCache, nodeID)
	p.removeFromCacheOrder(nodeID)
	p.cacheMu.Unlock()
}

func (p *ProvenanceTracker) removeFromCacheOrder(nodeID string) {
	for i, id := range p.cacheOrder {
		if id == nodeID {
			p.cacheOrder = append(p.cacheOrder[:i], p.cacheOrder[i+1:]...)
			return
		}
	}
}

// GetProvenance retrieves all provenance records for a node.
func (p *ProvenanceTracker) GetProvenance(nodeID string) ([]*Provenance, error) {
	p.cacheMu.RLock()
	if cached, ok := p.provenanceCache[nodeID]; ok {
		p.cacheMu.RUnlock()
		return cached, nil
	}
	p.cacheMu.RUnlock()

	return p.loadProvenance(nodeID)
}

func (p *ProvenanceTracker) loadProvenance(nodeID string) ([]*Provenance, error) {
	rows, err := p.db.DB().Query(`
		SELECT id, node_id, source_type, source_id, confidence, verified_at, verifier
		FROM provenance WHERE node_id = ? ORDER BY verified_at DESC
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query provenance: %w", err)
	}
	defer rows.Close()

	var provs []*Provenance
	for rows.Next() {
		prov, err := p.scanProvenance(rows)
		if err != nil {
			return nil, err
		}
		provs = append(provs, prov)
	}

	p.cacheMu.Lock()
	p.addToCache(nodeID, provs)
	p.cacheMu.Unlock()

	return provs, rows.Err()
}

func (p *ProvenanceTracker) addToCache(nodeID string, provs []*Provenance) {
	if _, exists := p.provenanceCache[nodeID]; !exists {
		if len(p.provenanceCache) >= p.maxCacheSize {
			p.evictOldestCacheEntry()
		}
		p.cacheOrder = append(p.cacheOrder, nodeID)
	}
	p.provenanceCache[nodeID] = provs
}

func (p *ProvenanceTracker) evictOldestCacheEntry() {
	if len(p.cacheOrder) == 0 {
		return
	}
	oldest := p.cacheOrder[0]
	p.cacheOrder = p.cacheOrder[1:]
	delete(p.provenanceCache, oldest)
}

func (p *ProvenanceTracker) scanProvenance(row interface{ Scan(...any) error }) (*Provenance, error) {
	var prov Provenance
	var verifiedAt, verifierJSON string

	err := row.Scan(&prov.ID, &prov.NodeID, &prov.SourceType, &prov.SourceID,
		&prov.Confidence, &verifiedAt, &verifierJSON)
	if err != nil {
		return nil, fmt.Errorf("scan provenance: %w", err)
	}

	prov.VerifiedAt, _ = time.Parse(time.RFC3339, verifiedAt)
	return &prov, nil
}

// GetProvenanceChain retrieves the verification chain for a node.
func (p *ProvenanceTracker) GetProvenanceChain(nodeID string, depth int) (*ProvenanceChain, error) {
	ns := vectorgraphdb.NewNodeStore(p.db, nil)
	node, err := ns.GetNode(nodeID)
	if err != nil {
		return nil, err
	}

	provs, err := p.GetProvenance(nodeID)
	if err != nil {
		return nil, err
	}

	chain := &ProvenanceChain{
		Node:  node,
		Chain: make([]*Provenance, 0),
	}

	if len(provs) > 0 {
		chain.DirectProvenance = provs[0]
	}

	chain.Chain, chain.TransitiveConf = p.buildChain(provs, depth)
	return chain, nil
}

func (p *ProvenanceTracker) buildChain(provs []*Provenance, maxDepth int) ([]*Provenance, float64) {
	if len(provs) == 0 {
		return nil, 0.5
	}

	chain := make([]*Provenance, 0)
	conf := 1.0

	for i, prov := range provs {
		if i >= maxDepth {
			break
		}
		chain = append(chain, prov)
		conf *= prov.Confidence
	}

	return chain, conf
}

// FindBySource finds all nodes from a specific source.
func (p *ProvenanceTracker) FindBySource(sourceType SourceType, sourceID string) ([]*vectorgraphdb.GraphNode, error) {
	rows, err := p.db.DB().Query(`
		SELECT n.id, n.domain, n.node_type, n.content_hash, n.metadata, n.created_at, n.updated_at, n.accessed_at
		FROM nodes n
		JOIN provenance p ON n.id = p.node_id
		WHERE p.source_type = ? AND p.source_id = ?
	`, sourceType, sourceID)
	if err != nil {
		return nil, fmt.Errorf("query by source: %w", err)
	}
	defer rows.Close()

	return p.scanNodes(rows)
}

func (p *ProvenanceTracker) scanNodes(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*vectorgraphdb.GraphNode, error) {
	var nodes []*vectorgraphdb.GraphNode
	for rows.Next() {
		node, err := p.scanNodeRow(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (p *ProvenanceTracker) scanNodeRow(row interface{ Scan(...any) error }) (*vectorgraphdb.GraphNode, error) {
	var node vectorgraphdb.GraphNode
	var metadataJSON, createdAt, updatedAt, accessedAt string

	err := row.Scan(&node.ID, &node.Domain, &node.NodeType, &node.ContentHash,
		&metadataJSON, &createdAt, &updatedAt, &accessedAt)
	if err != nil {
		return nil, fmt.Errorf("scan node: %w", err)
	}

	if err := json.Unmarshal([]byte(metadataJSON), &node.Metadata); err != nil {
		node.Metadata = make(map[string]any)
	}
	node.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	node.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	node.AccessedAt, _ = time.Parse(time.RFC3339, accessedAt)

	return &node, nil
}

// InvalidateBySource marks all nodes from a source as stale.
func (p *ProvenanceTracker) InvalidateBySource(sourceID string) error {
	_, err := p.db.DB().Exec(`
		UPDATE nodes SET updated_at = ? 
		WHERE id IN (SELECT node_id FROM provenance WHERE source_id = ?)
	`, time.Now().Add(-365*24*time.Hour).Format(time.RFC3339), sourceID)
	if err != nil {
		return fmt.Errorf("invalidate by source: %w", err)
	}

	p.cacheMu.Lock()
	p.provenanceCache = make(map[string][]*Provenance)
	p.cacheMu.Unlock()

	return nil
}

// GenerateCitation generates a citation string for a node.
func (p *ProvenanceTracker) GenerateCitation(nodeID string) (string, error) {
	chain, err := p.GetProvenanceChain(nodeID, 3)
	if err != nil {
		return "", err
	}

	if chain.DirectProvenance == nil {
		return fmt.Sprintf("[%s: unverified source]", nodeID), nil
	}

	prov := chain.DirectProvenance
	return p.formatCitation(prov, chain.TransitiveConf), nil
}

func (p *ProvenanceTracker) formatCitation(prov *Provenance, transitiveConf float64) string {
	verifiedStr := "unverified"
	if prov.VerifiedAt.After(time.Time{}) {
		verifiedStr = prov.VerifiedAt.Format("2006-01-02")
	}

	return fmt.Sprintf("[Source: %s (%s), Confidence: %.0f%%, Verified: %s]",
		prov.SourceType, prov.SourceID, transitiveConf*100, verifiedStr)
}

// FormatCitationInline generates an inline citation format.
func (p *ProvenanceTracker) FormatCitationInline(nodeID string) (string, error) {
	provs, err := p.GetProvenance(nodeID)
	if err != nil {
		return "", err
	}
	if len(provs) == 0 {
		return "(unverified)", nil
	}

	prov := provs[0]
	return fmt.Sprintf("(%s, %.0f%% confident)", prov.SourceType, prov.Confidence*100), nil
}

// FormatCitationStructured generates a structured citation for API use.
func (p *ProvenanceTracker) FormatCitationStructured(nodeID string) (map[string]any, error) {
	chain, err := p.GetProvenanceChain(nodeID, 5)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"node_id":         nodeID,
		"transitive_conf": chain.TransitiveConf,
		"chain_length":    len(chain.Chain),
	}

	if chain.DirectProvenance != nil {
		result["source_type"] = chain.DirectProvenance.SourceType
		result["source_id"] = chain.DirectProvenance.SourceID
		result["confidence"] = chain.DirectProvenance.Confidence
		result["verified_at"] = chain.DirectProvenance.VerifiedAt
	}

	return result, nil
}

// DetectCircularVerification checks if adding a verification would create a cycle.
func (p *ProvenanceTracker) DetectCircularVerification(nodeID string, verifierNodeID string) (bool, error) {
	visited := make(map[string]bool)
	return p.hasPath(verifierNodeID, nodeID, visited, 10)
}

func (p *ProvenanceTracker) hasPath(from, to string, visited map[string]bool, maxDepth int) (bool, error) {
	if p.shouldTerminateSearch(from, to, visited, maxDepth) {
		return from == to, nil
	}
	visited[from] = true

	provs, err := p.GetProvenance(from)
	if err != nil {
		return false, err
	}

	return p.searchVerifiers(provs, to, visited, maxDepth)
}

func (p *ProvenanceTracker) shouldTerminateSearch(from, to string, visited map[string]bool, maxDepth int) bool {
	return maxDepth <= 0 || from == to || visited[from]
}

func (p *ProvenanceTracker) searchVerifiers(provs []*Provenance, to string, visited map[string]bool, maxDepth int) (bool, error) {
	for _, prov := range provs {
		for _, verifier := range prov.VerifiedBy {
			if hasPath, _ := p.hasPath(verifier, to, visited, maxDepth-1); hasPath {
				return true, nil
			}
		}
	}
	return false, nil
}

func (p *ProvenanceTracker) Close() {
	p.cacheMu.Lock()
	p.provenanceCache = make(map[string][]*Provenance)
	p.cacheOrder = make([]string, 0)
	p.cacheMu.Unlock()
}
