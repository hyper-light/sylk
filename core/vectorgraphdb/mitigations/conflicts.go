package mitigations

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/google/uuid"
)

// ContentAnalyzer analyzes content for contradiction detection.
type ContentAnalyzer interface {
	CompareContent(a, b string) float64
}

// DefaultContentAnalyzer provides basic content comparison.
type DefaultContentAnalyzer struct{}

// CompareContent compares two strings for similarity (Jaccard index).
func (d *DefaultContentAnalyzer) CompareContent(a, b string) float64 {
	setA := makeWordSet(a)
	setB := makeWordSet(b)

	intersection := 0
	for word := range setA {
		if setB[word] {
			intersection++
		}
	}

	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func makeWordSet(s string) map[string]bool {
	words := make(map[string]bool)
	current := ""
	for _, r := range s {
		current = processRune(r, current, words)
	}
	addWord(current, words)
	return words
}

func processRune(r rune, current string, words map[string]bool) string {
	if isAlphanumeric(r) {
		return current + string(r)
	}
	addWord(current, words)
	return ""
}

func addWord(word string, words map[string]bool) {
	if word != "" {
		words[word] = true
	}
}

func isAlphanumeric(r rune) bool {
	return isLetter(r) || isDigit(r)
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// HNSWSimilaritySearcher interface for similarity search.
type HNSWSimilaritySearcher interface {
	Search(query []float32, k int, filter *vectorgraphdb.HNSWSearchFilter) []vectorgraphdb.HNSWSearchResult
	GetVector(id string) ([]float32, error)
}

// ConflictDetector detects contradictions between stored information.
type ConflictDetector struct {
	db              *vectorgraphdb.VectorGraphDB
	hnsw            HNSWSimilaritySearcher
	semanticThresh  float64
	contentAnalyzer ContentAnalyzer
	conflictsMu     sync.RWMutex
	conflicts       map[string]*Conflict
	maxConflicts    int
	conflictOrder   []string
}

// ConflictDetectorConfig configures the conflict detector.
type ConflictDetectorConfig struct {
	SemanticThreshold float64
}

// DefaultConflictDetectorConfig returns default configuration.
func DefaultConflictDetectorConfig() ConflictDetectorConfig {
	return ConflictDetectorConfig{
		SemanticThreshold: 0.9,
	}
}

// NewConflictDetector creates a new ConflictDetector.
func NewConflictDetector(
	db *vectorgraphdb.VectorGraphDB,
	hnsw HNSWSimilaritySearcher,
	config ConflictDetectorConfig,
) *ConflictDetector {
	return &ConflictDetector{
		db:              db,
		hnsw:            hnsw,
		semanticThresh:  config.SemanticThreshold,
		contentAnalyzer: &DefaultContentAnalyzer{},
		conflicts:       make(map[string]*Conflict),
		maxConflicts:    10000,
		conflictOrder:   make([]string, 0),
	}
}

// DetectOnInsert checks for conflicts when inserting a new node.
func (d *ConflictDetector) DetectOnInsert(
	ctx context.Context,
	node *vectorgraphdb.GraphNode,
	embedding []float32,
) ([]*Conflict, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	similarNodes := d.findSimilarNodes(embedding)
	return d.analyzeForConflicts(node, similarNodes)
}

func (d *ConflictDetector) findSimilarNodes(embedding []float32) []vectorgraphdb.HNSWSearchResult {
	if d.hnsw == nil {
		return nil
	}
	return d.hnsw.Search(embedding, 10, &vectorgraphdb.HNSWSearchFilter{
		MinSimilarity: d.semanticThresh,
	})
}

func (d *ConflictDetector) analyzeForConflicts(
	newNode *vectorgraphdb.GraphNode,
	similar []vectorgraphdb.HNSWSearchResult,
) ([]*Conflict, error) {
	conflicts := make([]*Conflict, 0)
	ns := vectorgraphdb.NewNodeStore(d.db, nil)

	for _, s := range similar {
		if conflict := d.analyzeNodePair(newNode, s, ns); conflict != nil {
			conflicts = append(conflicts, conflict)
		}
	}

	return conflicts, nil
}

func (d *ConflictDetector) analyzeNodePair(newNode *vectorgraphdb.GraphNode, s vectorgraphdb.HNSWSearchResult, ns *vectorgraphdb.NodeStore) *Conflict {
	if s.ID == newNode.ID {
		return nil
	}

	existingNode, err := ns.GetNode(s.ID)
	if err != nil {
		return nil
	}

	conflict := d.detectConflict(newNode, existingNode, s.Similarity)
	if conflict != nil {
		d.storeConflict(conflict)
	}
	return conflict
}

func (d *ConflictDetector) detectConflict(
	nodeA, nodeB *vectorgraphdb.GraphNode,
	similarity float64,
) *Conflict {
	contentA := extractContent(nodeA)
	contentB := extractContent(nodeB)

	contentSim := d.contentAnalyzer.CompareContent(contentA, contentB)

	if similarity > 0.9 && contentSim < 0.5 {
		return d.createConflict(nodeA.ID, nodeB.ID, ConflictSemantic, similarity,
			"High embedding similarity but low content match")
	}

	if d.isTemporalConflict(nodeA, nodeB, contentSim) {
		return d.createConflict(nodeA.ID, nodeB.ID, ConflictTemporal, similarity,
			"Same topic with different answers at different times")
	}

	return nil
}

func extractContent(node *vectorgraphdb.GraphNode) string {
	if content, ok := node.Metadata["content"].(string); ok {
		return content
	}
	if desc, ok := node.Metadata["description"].(string); ok {
		return desc
	}
	data, _ := json.Marshal(node.Metadata)
	return string(data)
}

func (d *ConflictDetector) isTemporalConflict(nodeA, nodeB *vectorgraphdb.GraphNode, contentSim float64) bool {
	sameSubject := d.hasSameSubject(nodeA, nodeB)
	temporalGap := absTimeDiff(nodeA.CreatedAt, nodeB.CreatedAt) > 24*time.Hour
	significantDiff := contentSim < 0.7

	return sameSubject && temporalGap && significantDiff
}

func (d *ConflictDetector) hasSameSubject(nodeA, nodeB *vectorgraphdb.GraphNode) bool {
	subjectA, _ := nodeA.Metadata["subject"].(string)
	subjectB, _ := nodeB.Metadata["subject"].(string)
	return subjectA != "" && subjectA == subjectB
}

func absTimeDiff(a, b time.Time) time.Duration {
	diff := a.Sub(b)
	if diff < 0 {
		return -diff
	}
	return diff
}

func (d *ConflictDetector) createConflict(
	nodeAID, nodeBID string,
	conflictType ConflictType,
	similarity float64,
	details string,
) *Conflict {
	return &Conflict{
		ID:           uuid.New().String(),
		NodeAID:      nodeAID,
		NodeBID:      nodeBID,
		ConflictType: conflictType,
		Similarity:   similarity,
		Details:      details,
		DetectedAt:   time.Now(),
	}
}

func (d *ConflictDetector) storeConflict(conflict *Conflict) {
	d.conflictsMu.Lock()
	d.addConflict(conflict)
	d.conflictsMu.Unlock()
}

func (d *ConflictDetector) addConflict(conflict *Conflict) {
	if _, exists := d.conflicts[conflict.ID]; !exists {
		if len(d.conflicts) >= d.maxConflicts {
			d.evictOldestResolved()
		}
		d.conflictOrder = append(d.conflictOrder, conflict.ID)
	}
	d.conflicts[conflict.ID] = conflict
}

func (d *ConflictDetector) evictOldestResolved() {
	for i, id := range d.conflictOrder {
		if c, ok := d.conflicts[id]; ok && c.Resolution != nil {
			d.conflictOrder = append(d.conflictOrder[:i], d.conflictOrder[i+1:]...)
			delete(d.conflicts, id)
			return
		}
	}
	d.evictOldest()
}

func (d *ConflictDetector) evictOldest() {
	if len(d.conflictOrder) == 0 {
		return
	}
	oldest := d.conflictOrder[0]
	d.conflictOrder = d.conflictOrder[1:]
	delete(d.conflicts, oldest)
}

// Resolve resolves a conflict with the given resolution.
func (d *ConflictDetector) Resolve(conflictID string, resolution Resolution) error {
	d.conflictsMu.Lock()
	defer d.conflictsMu.Unlock()

	conflict, ok := d.conflicts[conflictID]
	if !ok {
		return fmt.Errorf("conflict not found: %s", conflictID)
	}

	conflict.Resolution = &resolution
	now := time.Now()
	conflict.ResolvedAt = &now

	return nil
}

// GetActiveConflicts returns unresolved conflicts.
func (d *ConflictDetector) GetActiveConflicts(limit int) []*Conflict {
	d.conflictsMu.RLock()
	defer d.conflictsMu.RUnlock()

	active := make([]*Conflict, 0, limit)
	for _, c := range d.conflicts {
		if c.Resolution == nil {
			active = append(active, c)
			if len(active) >= limit {
				break
			}
		}
	}
	return active
}

// GetConflictsForNode returns conflicts involving a specific node.
func (d *ConflictDetector) GetConflictsForNode(nodeID string) []*Conflict {
	d.conflictsMu.RLock()
	defer d.conflictsMu.RUnlock()

	nodeConflicts := make([]*Conflict, 0)
	for _, c := range d.conflicts {
		if c.NodeAID == nodeID || c.NodeBID == nodeID {
			nodeConflicts = append(nodeConflicts, c)
		}
	}
	return nodeConflicts
}

// GetConflictStats returns aggregate conflict statistics.
func (d *ConflictDetector) GetConflictStats() *ConflictStats {
	d.conflictsMu.RLock()
	defer d.conflictsMu.RUnlock()

	stats := &ConflictStats{
		ByType:       make(map[ConflictType]int64),
		ByResolution: make(map[Resolution]int64),
	}

	var totalResolutionTime time.Duration
	resolvedCount := 0

	for _, c := range d.conflicts {
		d.updateStatsForConflict(stats, c, &totalResolutionTime, &resolvedCount)
	}

	stats.AvgResolutionTime = computeAvgResolutionTime(totalResolutionTime, resolvedCount)
	return stats
}

func (d *ConflictDetector) updateStatsForConflict(stats *ConflictStats, c *Conflict, totalTime *time.Duration, resolvedCount *int) {
	stats.TotalConflicts++
	stats.ByType[c.ConflictType]++

	if c.Resolution == nil {
		stats.UnresolvedCount++
		return
	}
	stats.ByResolution[*c.Resolution]++
	if c.ResolvedAt != nil {
		*totalTime += c.ResolvedAt.Sub(c.DetectedAt)
		*resolvedCount++
	}
}

func computeAvgResolutionTime(total time.Duration, count int) time.Duration {
	if count == 0 {
		return 0
	}
	return total / time.Duration(count)
}

// AnnotateResults adds conflict information to query results.
func (d *ConflictDetector) AnnotateResults(results []vectorgraphdb.SearchResult) []AnnotatedSearchResult {
	annotated := make([]AnnotatedSearchResult, len(results))
	for i, r := range results {
		conflicts := d.GetConflictsForNode(r.Node.ID)
		annotated[i] = AnnotatedSearchResult{
			SearchResult: r,
			Conflicts:    conflicts,
			HasConflict:  len(conflicts) > 0,
		}
	}
	return annotated
}

// AnnotatedSearchResult is a search result with conflict information.
type AnnotatedSearchResult struct {
	vectorgraphdb.SearchResult
	Conflicts   []*Conflict `json:"conflicts,omitempty"`
	HasConflict bool        `json:"has_conflict"`
}

// ScanForConflicts performs a batch scan for conflicts in a domain.
func (d *ConflictDetector) ScanForConflicts(ctx context.Context, domain vectorgraphdb.Domain) ([]*Conflict, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ns := vectorgraphdb.NewNodeStore(d.db, nil)
	allTypes := vectorgraphdb.ValidNodeTypesForDomain(domain)
	var nodes []*vectorgraphdb.GraphNode
	for _, nodeType := range allTypes {
		batch, err := ns.GetNodesByType(domain, nodeType, 1000)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, batch...)
	}
	return d.crossCheckNodes(ctx, nodes)
}

func (d *ConflictDetector) crossCheckNodes(ctx context.Context, nodes []*vectorgraphdb.GraphNode) ([]*Conflict, error) {
	conflicts := make([]*Conflict, 0)

	for i := range nodes {
		if err := ctx.Err(); err != nil {
			return conflicts, err
		}
		d.checkAgainstRemaining(nodes, i, &conflicts)
	}

	return conflicts, nil
}

func (d *ConflictDetector) checkAgainstRemaining(nodes []*vectorgraphdb.GraphNode, i int, conflicts *[]*Conflict) {
	for _, node := range nodes[i+1:] {
		if conflict := d.detectConflict(nodes[i], node, 0.9); conflict != nil {
			*conflicts = append(*conflicts, conflict)
			d.storeConflict(conflict)
		}
	}
}

// AutoResolve attempts automatic resolution for clear cases.
func (d *ConflictDetector) AutoResolve(conflictID string) (bool, error) {
	d.conflictsMu.Lock()
	conflict, ok := d.conflicts[conflictID]
	d.conflictsMu.Unlock()

	if !ok {
		return false, fmt.Errorf("conflict not found: %s", conflictID)
	}

	if conflict.Resolution != nil {
		return false, nil
	}

	resolution, canResolve := d.determineAutoResolution(conflict)
	if !canResolve {
		return false, nil
	}

	return true, d.Resolve(conflictID, resolution)
}

func (d *ConflictDetector) determineAutoResolution(conflict *Conflict) (Resolution, bool) {
	ns := vectorgraphdb.NewNodeStore(d.db, nil)

	nodeA, errA := ns.GetNode(conflict.NodeAID)
	nodeB, errB := ns.GetNode(conflict.NodeBID)

	if errA != nil || errB != nil {
		return "", false
	}

	return resolveByAge(nodeA.UpdatedAt, nodeB.UpdatedAt)
}

func resolveByAge(timeA, timeB time.Time) (Resolution, bool) {
	ageDiff := timeA.Sub(timeB)
	oneWeek := 7 * 24 * time.Hour
	if ageDiff > oneWeek || ageDiff < -oneWeek {
		return ResolutionKeepNewer, true
	}
	return "", false
}

func (d *ConflictDetector) Close() {
	d.conflictsMu.Lock()
	d.conflicts = make(map[string]*Conflict)
	d.conflictOrder = make([]string, 0)
	d.conflictsMu.Unlock()
}

// CleanResolved removes all resolved conflicts.
func (d *ConflictDetector) CleanResolved() int {
	d.conflictsMu.Lock()
	defer d.conflictsMu.Unlock()

	removed := 0
	newOrder := make([]string, 0, len(d.conflictOrder))

	for _, id := range d.conflictOrder {
		if c, ok := d.conflicts[id]; ok && c.Resolution != nil {
			delete(d.conflicts, id)
			removed++
		} else {
			newOrder = append(newOrder, id)
		}
	}
	d.conflictOrder = newOrder
	return removed
}
