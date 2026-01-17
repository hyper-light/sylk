package mitigations

import (
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/google/uuid"
)

// TrustHierarchy implements trust scoring based on source reliability.
type TrustHierarchy struct {
	db           *vectorgraphdb.VectorGraphDB
	provenance   *ProvenanceTracker
	freshness    *FreshnessTracker
	auditMu      sync.RWMutex
	auditLog     []*TrustAuditEntry
	maxAuditLog  int
	trustCache   map[string]*TrustInfo
	cacheMu      sync.RWMutex
	maxCacheSize int
	cacheOrder   []string
}

// NewTrustHierarchy creates a new TrustHierarchy.
func NewTrustHierarchy(
	db *vectorgraphdb.VectorGraphDB,
	provenance *ProvenanceTracker,
	freshness *FreshnessTracker,
) *TrustHierarchy {
	return &TrustHierarchy{
		db:           db,
		provenance:   provenance,
		freshness:    freshness,
		auditLog:     make([]*TrustAuditEntry, 0),
		maxAuditLog:  10000,
		trustCache:   make(map[string]*TrustInfo),
		maxCacheSize: 10000,
		cacheOrder:   make([]string, 0),
	}
}

// GetTrustInfo retrieves trust information for a node.
func (t *TrustHierarchy) GetTrustInfo(nodeID string) (*TrustInfo, error) {
	t.cacheMu.RLock()
	if cached, ok := t.trustCache[nodeID]; ok {
		t.cacheMu.RUnlock()
		return cached, nil
	}
	t.cacheMu.RUnlock()

	return t.computeTrust(nodeID)
}

func (t *TrustHierarchy) computeTrust(nodeID string) (*TrustInfo, error) {
	ns := vectorgraphdb.NewNodeStore(t.db, nil)
	node, err := ns.GetNode(nodeID)
	if err != nil {
		return nil, err
	}

	info := &TrustInfo{NodeID: nodeID}

	info.TrustLevel = t.determineTrustLevel(node)
	info.BaseScore = info.TrustLevel.Score()

	info.FreshnessBoost = t.computeFreshnessBoost(nodeID)
	info.VerifyBoost = t.computeVerifyBoost(nodeID)
	info.CrossRefBoost = t.computeCrossRefBoost(nodeID)

	info.TrustScore = t.combineScores(info)
	info.EffectiveScore = info.TrustScore

	t.cacheMu.Lock()
	t.addToCache(nodeID, info)
	t.cacheMu.Unlock()

	return info, nil
}

func (t *TrustHierarchy) addToCache(nodeID string, info *TrustInfo) {
	if _, exists := t.trustCache[nodeID]; !exists {
		if len(t.trustCache) >= t.maxCacheSize {
			t.evictOldestCacheEntry()
		}
		t.cacheOrder = append(t.cacheOrder, nodeID)
	}
	t.trustCache[nodeID] = info
}

func (t *TrustHierarchy) evictOldestCacheEntry() {
	if len(t.cacheOrder) == 0 {
		return
	}
	oldest := t.cacheOrder[0]
	t.cacheOrder = t.cacheOrder[1:]
	delete(t.trustCache, oldest)
}

func (t *TrustHierarchy) determineTrustLevel(node *vectorgraphdb.GraphNode) TrustLevel {
	if node.Domain == vectorgraphdb.DomainCode {
		return TrustVerifiedCode
	}

	if node.Domain == vectorgraphdb.DomainHistory {
		return t.determinHistoryTrustLevel(node)
	}

	if node.Domain == vectorgraphdb.DomainAcademic {
		return t.determineAcademicTrustLevel(node)
	}

	return TrustUnknown
}

func (t *TrustHierarchy) determinHistoryTrustLevel(node *vectorgraphdb.GraphNode) TrustLevel {
	age := time.Since(node.CreatedAt)
	if age < 24*time.Hour {
		return TrustRecentHistory
	}
	return TrustOldHistory
}

func (t *TrustHierarchy) determineAcademicTrustLevel(node *vectorgraphdb.GraphNode) TrustLevel {
	if nodeType, ok := node.Metadata["type"].(string); ok {
		if nodeType == "official_doc" || nodeType == "api_doc" {
			return TrustOfficialDocs
		}
	}
	return TrustExternalArticle
}

func (t *TrustHierarchy) computeFreshnessBoost(nodeID string) float64 {
	if t.freshness == nil {
		return 1.0
	}

	info, err := t.freshness.GetFreshness(nodeID)
	if err != nil {
		return 1.0
	}

	return freshnessScoreToBoost(info.FreshnessScore)
}

func freshnessScoreToBoost(score float64) float64 {
	switch {
	case score > 0.8:
		return 1.1
	case score > 0.5:
		return 1.0
	default:
		return 0.9
	}
}

func (t *TrustHierarchy) computeVerifyBoost(nodeID string) float64 {
	if t.provenance == nil {
		return 1.0
	}

	provs, err := t.provenance.GetProvenance(nodeID)
	if err != nil || len(provs) == 0 {
		return 1.0
	}

	return confidenceToVerifyBoost(provs[0].Confidence)
}

func confidenceToVerifyBoost(confidence float64) float64 {
	switch {
	case confidence > 0.9:
		return 1.15
	case confidence > 0.7:
		return 1.05
	default:
		return 1.0
	}
}

func (t *TrustHierarchy) computeCrossRefBoost(nodeID string) float64 {
	if t.provenance == nil {
		return 0
	}

	provs, err := t.provenance.GetProvenance(nodeID)
	if err != nil {
		return 0
	}

	return computeCrossRefBoostFromProvs(provs)
}

func computeCrossRefBoostFromProvs(provs []*Provenance) float64 {
	uniqueSources := countUniqueSources(provs)
	boost := float64(uniqueSources-1) * 0.1
	return min(boost, 0.3)
}

func countUniqueSources(provs []*Provenance) int {
	sources := make(map[string]bool)
	for _, p := range provs {
		sources[string(p.SourceType)] = true
	}
	return len(sources)
}

func (t *TrustHierarchy) combineScores(info *TrustInfo) float64 {
	score := info.BaseScore * info.FreshnessBoost * info.VerifyBoost
	score += info.CrossRefBoost
	if score > 1.0 {
		return 1.0
	}
	return score
}

// ApplyTrust applies trust scores to search results.
func (t *TrustHierarchy) ApplyTrust(results []vectorgraphdb.SearchResult) []vectorgraphdb.SearchResult {
	trusted := make([]vectorgraphdb.SearchResult, len(results))
	for i, r := range results {
		trusted[i] = t.applyTrustToResult(r)
	}
	return trusted
}

func (t *TrustHierarchy) applyTrustToResult(result vectorgraphdb.SearchResult) vectorgraphdb.SearchResult {
	info, err := t.GetTrustInfo(result.Node.ID)
	if err != nil {
		return result
	}

	return vectorgraphdb.SearchResult{
		Node:       result.Node,
		Similarity: result.Similarity * info.EffectiveScore,
	}
}

// Promote increases the trust level of a node.
func (t *TrustHierarchy) Promote(nodeID, reason, actor string) error {
	info, err := t.GetTrustInfo(nodeID)
	if err != nil {
		return err
	}

	oldLevel := info.TrustLevel
	newLevel := promoteTrustLevel(oldLevel)

	if newLevel == oldLevel {
		return nil
	}

	t.recordAudit(nodeID, "promote", oldLevel, newLevel, reason, actor)
	t.invalidateCache(nodeID)

	return nil
}

var trustPromotionMap = map[TrustLevel]TrustLevel{
	TrustUnknown:         TrustLLMInference,
	TrustLLMInference:    TrustExternalArticle,
	TrustExternalArticle: TrustOldHistory,
	TrustOldHistory:      TrustOfficialDocs,
	TrustOfficialDocs:    TrustRecentHistory,
	TrustRecentHistory:   TrustVerifiedCode,
}

func promoteTrustLevel(current TrustLevel) TrustLevel {
	if next, ok := trustPromotionMap[current]; ok {
		return next
	}
	return current
}

// Demote decreases the trust level of a node.
func (t *TrustHierarchy) Demote(nodeID, reason, actor string) error {
	info, err := t.GetTrustInfo(nodeID)
	if err != nil {
		return err
	}

	oldLevel := info.TrustLevel
	newLevel := demoteTrustLevel(oldLevel)

	if newLevel == oldLevel {
		return nil
	}

	t.recordAudit(nodeID, "demote", oldLevel, newLevel, reason, actor)
	t.invalidateCache(nodeID)

	return nil
}

var trustDemotionMap = map[TrustLevel]TrustLevel{
	TrustVerifiedCode:    TrustRecentHistory,
	TrustRecentHistory:   TrustOfficialDocs,
	TrustOfficialDocs:    TrustOldHistory,
	TrustOldHistory:      TrustExternalArticle,
	TrustExternalArticle: TrustLLMInference,
	TrustLLMInference:    TrustUnknown,
}

func demoteTrustLevel(current TrustLevel) TrustLevel {
	if next, ok := trustDemotionMap[current]; ok {
		return next
	}
	return current
}

func (t *TrustHierarchy) recordAudit(nodeID, action string, oldLevel, newLevel TrustLevel, reason, actor string) {
	entry := &TrustAuditEntry{
		ID:        uuid.New().String(),
		NodeID:    nodeID,
		Action:    action,
		OldLevel:  oldLevel,
		NewLevel:  newLevel,
		Reason:    reason,
		Actor:     actor,
		Timestamp: time.Now(),
	}

	t.auditMu.Lock()
	t.addToAuditLog(entry)
	t.auditMu.Unlock()
}

func (t *TrustHierarchy) addToAuditLog(entry *TrustAuditEntry) {
	t.auditLog = append(t.auditLog, entry)
	if len(t.auditLog) > t.maxAuditLog {
		t.auditLog = t.auditLog[len(t.auditLog)-t.maxAuditLog:]
	}
}

func (t *TrustHierarchy) invalidateCache(nodeID string) {
	t.cacheMu.Lock()
	delete(t.trustCache, nodeID)
	t.removeFromCacheOrder(nodeID)
	t.cacheMu.Unlock()
}

func (t *TrustHierarchy) removeFromCacheOrder(nodeID string) {
	for i, id := range t.cacheOrder {
		if id == nodeID {
			t.cacheOrder = append(t.cacheOrder[:i], t.cacheOrder[i+1:]...)
			return
		}
	}
}

// GetTrustHistory retrieves the trust audit history for a node.
func (t *TrustHierarchy) GetTrustHistory(nodeID string) []*TrustAuditEntry {
	t.auditMu.RLock()
	defer t.auditMu.RUnlock()

	var history []*TrustAuditEntry
	for _, entry := range t.auditLog {
		if entry.NodeID == nodeID {
			history = append(history, entry)
		}
	}
	return history
}

// ExportTrustReport exports trust statistics.
func (t *TrustHierarchy) ExportTrustReport() (*TrustReport, error) {
	auditCount := t.getAuditCount()
	return t.buildTrustReport(auditCount), nil
}

func (t *TrustHierarchy) getAuditCount() int {
	t.auditMu.RLock()
	defer t.auditMu.RUnlock()
	return len(t.auditLog)
}

func (t *TrustHierarchy) buildTrustReport(auditCount int) *TrustReport {
	t.cacheMu.RLock()
	defer t.cacheMu.RUnlock()

	report := &TrustReport{
		TotalNodes:    len(t.trustCache),
		ByLevel:       make(map[TrustLevel]int),
		AvgTrustScore: 0,
		AuditEntries:  auditCount,
	}

	var totalScore float64
	for _, info := range t.trustCache {
		report.ByLevel[info.TrustLevel]++
		totalScore += info.TrustScore
	}

	if report.TotalNodes > 0 {
		report.AvgTrustScore = totalScore / float64(report.TotalNodes)
	}

	return report
}

// TrustReport contains aggregate trust metrics.
type TrustReport struct {
	TotalNodes    int                `json:"total_nodes"`
	ByLevel       map[TrustLevel]int `json:"by_level"`
	AvgTrustScore float64            `json:"avg_trust_score"`
	AuditEntries  int                `json:"audit_entries"`
}

// FilterByMinTrust filters results to those meeting a minimum trust level.
func (t *TrustHierarchy) FilterByMinTrust(results []vectorgraphdb.SearchResult, minLevel TrustLevel) []vectorgraphdb.SearchResult {
	filtered := make([]vectorgraphdb.SearchResult, 0)
	for _, r := range results {
		info, err := t.GetTrustInfo(r.Node.ID)
		if err != nil {
			continue
		}
		if info.TrustLevel >= minLevel {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// ComputeAggregateTrust computes trust for a set of nodes.
func (t *TrustHierarchy) ComputeAggregateTrust(nodeIDs []string) (float64, error) {
	if len(nodeIDs) == 0 {
		return 0, fmt.Errorf("no nodes provided")
	}

	var total float64
	for _, id := range nodeIDs {
		info, err := t.GetTrustInfo(id)
		if err != nil {
			continue
		}
		total += info.TrustScore
	}

	return total / float64(len(nodeIDs)), nil
}

func (t *TrustHierarchy) Close() {
	t.cacheMu.Lock()
	t.trustCache = make(map[string]*TrustInfo)
	t.cacheOrder = make([]string, 0)
	t.cacheMu.Unlock()

	t.auditMu.Lock()
	t.auditLog = make([]*TrustAuditEntry, 0)
	t.auditMu.Unlock()
}
