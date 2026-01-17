package archivalist

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Query Optimizer
// =============================================================================

// QueryOptimizer provides query planning and optimization
type QueryOptimizer struct {
	mu sync.RWMutex

	// Index statistics for cost estimation
	indexStats IndexStatistics

	// Query plan cache
	planCache map[string]*QueryPlan

	// Configuration
	config QueryOptimizerConfig

	// Statistics
	stats queryOptimizerStatsInternal
}

// QueryOptimizerConfig configures the optimizer
type QueryOptimizerConfig struct {
	// Maximum plans to cache
	MaxCachedPlans int `json:"max_cached_plans"`

	// Plan cache TTL
	PlanCacheTTL time.Duration `json:"plan_cache_ttl"`

	// Cost threshold for index usage
	IndexCostThreshold float64 `json:"index_cost_threshold"`

	// Enable parallel execution
	EnableParallel bool `json:"enable_parallel"`

	// Maximum parallel workers
	MaxParallelWorkers int `json:"max_parallel_workers"`
}

// DefaultQueryOptimizerConfig returns sensible defaults
func DefaultQueryOptimizerConfig() QueryOptimizerConfig {
	return QueryOptimizerConfig{
		MaxCachedPlans:     1000,
		PlanCacheTTL:       5 * time.Minute,
		IndexCostThreshold: 0.3,
		EnableParallel:     true,
		MaxParallelWorkers: 4,
	}
}

// IndexStatistics contains index statistics for cost estimation
type IndexStatistics struct {
	TotalEntries      int64                 `json:"total_entries"`
	EntriesByCategory map[Category]int64    `json:"entries_by_category"`
	EntriesBySource   map[SourceModel]int64 `json:"entries_by_source"`
	EntriesBySession  map[string]int64      `json:"entries_by_session"`
	AvgEntriesPerDay  float64               `json:"avg_entries_per_day"`
	IndexSelectivity  map[string]float64    `json:"index_selectivity"`
}

// queryOptimizerStatsInternal holds atomic counters
type queryOptimizerStatsInternal struct {
	totalQueries    int64
	plannedQueries  int64
	cacheHits       int64
	cacheMisses     int64
	parallelQueries int64
}

// QueryOptimizerStats contains optimizer statistics
type QueryOptimizerStats struct {
	TotalQueries    int64   `json:"total_queries"`
	PlannedQueries  int64   `json:"planned_queries"`
	CacheHits       int64   `json:"cache_hits"`
	CacheMisses     int64   `json:"cache_misses"`
	CacheHitRate    float64 `json:"cache_hit_rate"`
	ParallelQueries int64   `json:"parallel_queries"`
}

// NewQueryOptimizer creates a new query optimizer
func NewQueryOptimizer(config QueryOptimizerConfig) *QueryOptimizer {
	if config.MaxCachedPlans == 0 {
		config = DefaultQueryOptimizerConfig()
	}

	return &QueryOptimizer{
		indexStats: IndexStatistics{
			EntriesByCategory: make(map[Category]int64),
			EntriesBySource:   make(map[SourceModel]int64),
			EntriesBySession:  make(map[string]int64),
			IndexSelectivity:  make(map[string]float64),
		},
		planCache: make(map[string]*QueryPlan),
		config:    config,
	}
}

// =============================================================================
// Query Planning
// =============================================================================

// QueryPlan represents an optimized query execution plan
type QueryPlan struct {
	// Original query
	Query ArchiveQuery `json:"query"`

	// Planned operations in order
	Operations []QueryOperation `json:"operations"`

	// Estimated cost
	EstimatedCost float64 `json:"estimated_cost"`

	// Estimated rows returned
	EstimatedRows int64 `json:"estimated_rows"`

	// Whether to use parallel execution
	Parallel bool `json:"parallel"`

	// Plan creation time
	CreatedAt time.Time `json:"created_at"`

	// Plan cache key
	CacheKey string `json:"cache_key"`
}

// QueryOperation represents a single operation in a query plan
type QueryOperation struct {
	// Operation type
	Type QueryOperationType `json:"type"`

	// Index to use (if applicable)
	Index string `json:"index,omitempty"`

	// Filter values
	Values []string `json:"values,omitempty"`

	// Estimated selectivity (0-1)
	Selectivity float64 `json:"selectivity"`

	// Estimated cost
	Cost float64 `json:"cost"`
}

// QueryOperationType defines types of query operations
type QueryOperationType string

const (
	OpScanAll       QueryOperationType = "scan_all"
	OpIndexCategory QueryOperationType = "index_category"
	OpIndexSource   QueryOperationType = "index_source"
	OpIndexSession  QueryOperationType = "index_session"
	OpIndexID       QueryOperationType = "index_id"
	OpFilterDate    QueryOperationType = "filter_date"
	OpFilterText    QueryOperationType = "filter_text"
	OpSort          QueryOperationType = "sort"
	OpLimit         QueryOperationType = "limit"
)

// Plan creates an optimized query plan
func (qo *QueryOptimizer) Plan(query ArchiveQuery) *QueryPlan {
	atomic.AddInt64(&qo.stats.totalQueries, 1)

	cacheKey := qo.generateCacheKey(query)
	if plan, ok := qo.getCachedPlan(cacheKey); ok {
		return plan
	}

	atomic.AddInt64(&qo.stats.cacheMisses, 1)
	atomic.AddInt64(&qo.stats.plannedQueries, 1)

	plan := qo.generatePlan(query)
	plan.CacheKey = cacheKey
	qo.cachePlan(cacheKey, plan)

	return plan
}

func (qo *QueryOptimizer) getCachedPlan(cacheKey string) (*QueryPlan, bool) {
	qo.mu.RLock()
	plan, ok := qo.planCache[cacheKey]
	if !ok {
		qo.mu.RUnlock()
		return nil, false
	}
	isFresh := time.Since(plan.CreatedAt) < qo.config.PlanCacheTTL
	qo.mu.RUnlock()
	if !isFresh {
		return nil, false
	}
	atomic.AddInt64(&qo.stats.cacheHits, 1)
	return plan, true
}

func (qo *QueryOptimizer) cachePlan(cacheKey string, plan *QueryPlan) {
	qo.mu.Lock()
	qo.planCache[cacheKey] = plan
	qo.evictOldPlansLocked()
	qo.mu.Unlock()
}

// generatePlan creates a new query plan
func (qo *QueryOptimizer) generatePlan(query ArchiveQuery) *QueryPlan {
	operations := make([]QueryOperation, 0)
	cost := 0.0
	estimatedRows := qo.initialRowEstimate()

	primaryOp := qo.selectPrimaryOperation(query, estimatedRows)
	operations, cost, estimatedRows = appendOperation(operations, cost, estimatedRows, primaryOp)
	operations, cost, estimatedRows = qo.addFilters(query, primaryOp.Type, operations, cost, estimatedRows)
	operations, cost, estimatedRows = qo.addSort(operations, cost, estimatedRows)
	operations, cost, estimatedRows = qo.addLimit(query, operations, cost, estimatedRows)

	parallel := qo.shouldRunParallel(cost, estimatedRows)
	qo.trackParallel(parallel)

	return &QueryPlan{
		Query:         query,
		Operations:    operations,
		EstimatedCost: cost,
		EstimatedRows: estimatedRows,
		Parallel:      parallel,
		CreatedAt:     time.Now(),
	}
}

func (qo *QueryOptimizer) initialRowEstimate() int64 {
	qo.mu.RLock()
	totalEntries := qo.indexStats.TotalEntries
	qo.mu.RUnlock()

	if totalEntries == 0 {
		return 1000
	}
	return totalEntries
}

func appendOperation(operations []QueryOperation, cost float64, rows int64, op QueryOperation) ([]QueryOperation, float64, int64) {
	operations = append(operations, op)
	cost += op.Cost
	rows = int64(float64(rows) * op.Selectivity)
	return operations, cost, rows
}

func (qo *QueryOptimizer) addFilters(query ArchiveQuery, primaryType QueryOperationType, operations []QueryOperation, cost float64, rows int64) ([]QueryOperation, float64, int64) {
	operations, cost, rows = qo.addCategoryFilter(query, primaryType, operations, cost, rows)
	operations, cost, rows = qo.addSourceFilter(query, primaryType, operations, cost, rows)
	operations, cost, rows = qo.addDateFilter(query, operations, cost, rows)
	operations, cost, rows = qo.addTextFilter(query, operations, cost, rows)
	return operations, cost, rows
}

func (qo *QueryOptimizer) addCategoryFilter(query ArchiveQuery, primaryType QueryOperationType, operations []QueryOperation, cost float64, rows int64) ([]QueryOperation, float64, int64) {
	if len(query.Categories) == 0 || primaryType == OpIndexCategory {
		return operations, cost, rows
	}
	op := QueryOperation{
		Type:        OpFilterDate,
		Values:      categoriesToStrings(query.Categories),
		Selectivity: qo.estimateCategorySelectivity(query.Categories),
		Cost:        0.01,
	}
	return appendOperation(operations, cost, rows, op)
}

func (qo *QueryOptimizer) addSourceFilter(query ArchiveQuery, primaryType QueryOperationType, operations []QueryOperation, cost float64, rows int64) ([]QueryOperation, float64, int64) {
	if len(query.Sources) == 0 || primaryType == OpIndexSource {
		return operations, cost, rows
	}
	op := QueryOperation{
		Type:        OpFilterDate,
		Values:      sourcesToStrings(query.Sources),
		Selectivity: qo.estimateSourceSelectivity(query.Sources),
		Cost:        0.01,
	}
	return appendOperation(operations, cost, rows, op)
}

func (qo *QueryOptimizer) addDateFilter(query ArchiveQuery, operations []QueryOperation, cost float64, rows int64) ([]QueryOperation, float64, int64) {
	if query.Since == nil && query.Until == nil {
		return operations, cost, rows
	}
	op := QueryOperation{
		Type:        OpFilterDate,
		Selectivity: qo.estimateDateSelectivity(query.Since, query.Until),
		Cost:        0.02,
	}
	return appendOperation(operations, cost, rows, op)
}

func (qo *QueryOptimizer) addTextFilter(query ArchiveQuery, operations []QueryOperation, cost float64, rows int64) ([]QueryOperation, float64, int64) {
	if query.SearchText == "" {
		return operations, cost, rows
	}
	op := QueryOperation{
		Type:        OpFilterText,
		Values:      []string{query.SearchText},
		Selectivity: 0.1,
		Cost:        0.5,
	}
	return appendOperation(operations, cost, rows, op)
}

func (qo *QueryOptimizer) addSort(operations []QueryOperation, cost float64, rows int64) ([]QueryOperation, float64, int64) {
	return appendOperation(operations, cost, rows, QueryOperation{Type: OpSort, Cost: 0.1})
}

func (qo *QueryOptimizer) addLimit(query ArchiveQuery, operations []QueryOperation, cost float64, rows int64) ([]QueryOperation, float64, int64) {
	if query.Limit <= 0 {
		return operations, cost, rows
	}
	operations, cost, rows = appendOperation(operations, cost, rows, QueryOperation{Type: OpLimit, Values: []string{}, Cost: 0.01})
	limit := int64(query.Limit)
	if limit < rows {
		rows = limit
	}
	return operations, cost, rows
}

func (qo *QueryOptimizer) shouldRunParallel(cost float64, rows int64) bool {
	return qo.config.EnableParallel && cost > 0.5 && rows > 100
}

func (qo *QueryOptimizer) trackParallel(parallel bool) {
	if parallel {
		atomic.AddInt64(&qo.stats.parallelQueries, 1)
	}
}

// selectPrimaryOperation chooses the best primary access method
func (qo *QueryOptimizer) selectPrimaryOperation(query ArchiveQuery, totalEntries int64) QueryOperation {
	if op, ok := qo.fastIDLookup(query, totalEntries); ok {
		return op
	}

	candidates := qo.collectPrimaryCandidates(query)
	if len(candidates) > 0 {
		return selectLowestCost(candidates)
	}

	return defaultScanOperation()
}

func (qo *QueryOptimizer) fastIDLookup(query ArchiveQuery, totalEntries int64) (QueryOperation, bool) {
	if len(query.IDs) == 0 {
		return QueryOperation{}, false
	}
	return QueryOperation{
		Type:        OpIndexID,
		Index:       "id",
		Values:      query.IDs,
		Selectivity: float64(len(query.IDs)) / float64(totalEntries),
		Cost:        0.001 * float64(len(query.IDs)),
	}, true
}

func (qo *QueryOptimizer) collectPrimaryCandidates(query ArchiveQuery) []QueryOperation {
	candidates := make([]QueryOperation, 0)
	candidates = qo.addCategoryCandidate(candidates, query)
	candidates = qo.addSourceCandidate(candidates, query)
	candidates = qo.addSessionCandidate(candidates, query)
	return candidates
}

func (qo *QueryOptimizer) addCategoryCandidate(candidates []QueryOperation, query ArchiveQuery) []QueryOperation {
	if len(query.Categories) == 0 {
		return candidates
	}
	selectivity := qo.estimateCategorySelectivity(query.Categories)
	return append(candidates, QueryOperation{
		Type:        OpIndexCategory,
		Index:       "category",
		Values:      categoriesToStrings(query.Categories),
		Selectivity: selectivity,
		Cost:        selectivity * 0.1,
	})
}

func (qo *QueryOptimizer) addSourceCandidate(candidates []QueryOperation, query ArchiveQuery) []QueryOperation {
	if len(query.Sources) == 0 {
		return candidates
	}
	selectivity := qo.estimateSourceSelectivity(query.Sources)
	return append(candidates, QueryOperation{
		Type:        OpIndexSource,
		Index:       "source",
		Values:      sourcesToStrings(query.Sources),
		Selectivity: selectivity,
		Cost:        selectivity * 0.1,
	})
}

func (qo *QueryOptimizer) addSessionCandidate(candidates []QueryOperation, query ArchiveQuery) []QueryOperation {
	if len(query.SessionIDs) == 0 {
		return candidates
	}
	selectivity := qo.estimateSessionSelectivity(query.SessionIDs)
	return append(candidates, QueryOperation{
		Type:        OpIndexSession,
		Index:       "session",
		Values:      query.SessionIDs,
		Selectivity: selectivity,
		Cost:        selectivity * 0.1,
	})
}

func selectLowestCost(candidates []QueryOperation) QueryOperation {
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Cost < candidates[j].Cost
	})
	return candidates[0]
}

func defaultScanOperation() QueryOperation {
	return QueryOperation{
		Type:        OpScanAll,
		Selectivity: 1.0,
		Cost:        1.0,
	}
}

// =============================================================================
// Selectivity Estimation
// =============================================================================

func (qo *QueryOptimizer) estimateCategorySelectivity(categories []Category) float64 {
	qo.mu.RLock()
	defer qo.mu.RUnlock()

	if qo.indexStats.TotalEntries == 0 {
		return 0.1
	}

	var total int64
	for _, cat := range categories {
		total += qo.indexStats.EntriesByCategory[cat]
	}

	selectivity := float64(total) / float64(qo.indexStats.TotalEntries)
	if selectivity == 0 {
		selectivity = 0.1 // Default estimate
	}
	return selectivity
}

func (qo *QueryOptimizer) estimateSourceSelectivity(sources []SourceModel) float64 {
	qo.mu.RLock()
	defer qo.mu.RUnlock()

	if qo.indexStats.TotalEntries == 0 {
		return 0.1
	}

	var total int64
	for _, src := range sources {
		total += qo.indexStats.EntriesBySource[src]
	}

	selectivity := float64(total) / float64(qo.indexStats.TotalEntries)
	if selectivity == 0 {
		selectivity = 0.1
	}
	return selectivity
}

func (qo *QueryOptimizer) estimateSessionSelectivity(sessionIDs []string) float64 {
	qo.mu.RLock()
	defer qo.mu.RUnlock()

	if qo.indexStats.TotalEntries == 0 {
		return 0.1
	}

	var total int64
	for _, id := range sessionIDs {
		total += qo.indexStats.EntriesBySession[id]
	}

	selectivity := float64(total) / float64(qo.indexStats.TotalEntries)
	if selectivity == 0 {
		selectivity = 0.01 // Sessions are usually selective
	}
	return selectivity
}

func (qo *QueryOptimizer) estimateDateSelectivity(since, until *time.Time) float64 {
	if since == nil && until == nil {
		return 1.0
	}

	avgPerDay, totalEntries := qo.dateSelectivityStats()
	if avgPerDay == 0 || totalEntries == 0 {
		return 0.5
	}

	days := estimateDateRangeDays(since, until)
	selectivity := (days * avgPerDay) / float64(totalEntries)
	return clampSelectivity(selectivity, 0.01, 1.0)
}

func (qo *QueryOptimizer) dateSelectivityStats() (float64, int64) {
	qo.mu.RLock()
	avgPerDay := qo.indexStats.AvgEntriesPerDay
	totalEntries := qo.indexStats.TotalEntries
	qo.mu.RUnlock()
	return avgPerDay, totalEntries
}

func estimateDateRangeDays(since, until *time.Time) float64 {
	if since != nil && until != nil {
		return until.Sub(*since).Hours() / 24
	}
	if since != nil {
		return time.Since(*since).Hours() / 24
	}
	return 30
}

func clampSelectivity(value, min, max float64) float64 {
	if value > max {
		return max
	}
	if value < min {
		return min
	}
	return value
}

// =============================================================================
// Cache Management
// =============================================================================

func (qo *QueryOptimizer) generateCacheKey(query ArchiveQuery) string {
	// Simple hash based on query parameters
	key := ""

	for _, cat := range query.Categories {
		key += string(cat) + ","
	}
	key += "|"

	for _, src := range query.Sources {
		key += string(src) + ","
	}
	key += "|"

	for _, id := range query.SessionIDs {
		key += id + ","
	}
	key += "|"

	if query.Since != nil {
		key += query.Since.Format(time.RFC3339)
	}
	key += "|"

	if query.Until != nil {
		key += query.Until.Format(time.RFC3339)
	}
	key += "|"

	key += query.SearchText

	return key
}

func (qo *QueryOptimizer) evictOldPlansLocked() {
	if len(qo.planCache) <= qo.config.MaxCachedPlans {
		return
	}

	// Find and remove oldest plans
	type planAge struct {
		key       string
		createdAt time.Time
	}

	plans := make([]planAge, 0, len(qo.planCache))
	for key, plan := range qo.planCache {
		plans = append(plans, planAge{key: key, createdAt: plan.CreatedAt})
	}

	sort.Slice(plans, func(i, j int) bool {
		return plans[i].createdAt.Before(plans[j].createdAt)
	})

	// Remove oldest 10%
	toRemove := len(plans) / 10
	if toRemove < 1 {
		toRemove = 1
	}

	for i := 0; i < toRemove; i++ {
		delete(qo.planCache, plans[i].key)
	}
}

// =============================================================================
// Statistics Management
// =============================================================================

// UpdateStatistics updates index statistics for better planning
func (qo *QueryOptimizer) UpdateStatistics(stats IndexStatistics) {
	qo.mu.Lock()
	defer qo.mu.Unlock()

	qo.indexStats = stats

	// Clear plan cache when statistics change significantly
	qo.planCache = make(map[string]*QueryPlan)
}

// Stats returns optimizer statistics
func (qo *QueryOptimizer) Stats() QueryOptimizerStats {
	totalQueries := atomic.LoadInt64(&qo.stats.totalQueries)
	cacheHits := atomic.LoadInt64(&qo.stats.cacheHits)

	var hitRate float64
	if totalQueries > 0 {
		hitRate = float64(cacheHits) / float64(totalQueries)
	}

	return QueryOptimizerStats{
		TotalQueries:    totalQueries,
		PlannedQueries:  atomic.LoadInt64(&qo.stats.plannedQueries),
		CacheHits:       cacheHits,
		CacheMisses:     atomic.LoadInt64(&qo.stats.cacheMisses),
		CacheHitRate:    hitRate,
		ParallelQueries: atomic.LoadInt64(&qo.stats.parallelQueries),
	}
}

// ClearCache clears the query plan cache
func (qo *QueryOptimizer) ClearCache() {
	qo.mu.Lock()
	defer qo.mu.Unlock()
	qo.planCache = make(map[string]*QueryPlan)
}
