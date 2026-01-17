package mitigations

import (
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// Tokenizer estimates token counts for content.
type Tokenizer interface {
	EstimateTokens(content string) int
}

// DefaultTokenizer provides basic token estimation.
type DefaultTokenizer struct{}

// EstimateTokens estimates tokens using word-based approximation.
func (t *DefaultTokenizer) EstimateTokens(content string) int {
	words := len(strings.Fields(content))
	return int(float64(words) * 1.3)
}

// Embedder generates embeddings for content.
type Embedder interface {
	Embed(content string) ([]float32, error)
}

// ContextQualityScorer optimizes context selection for LLM prompts.
type ContextQualityScorer struct {
	db        *vectorgraphdb.VectorGraphDB
	weights   QualityWeights
	tokenizer Tokenizer
	freshness *FreshnessTracker
	trust     *TrustHierarchy
}

// NewContextQualityScorer creates a new ContextQualityScorer.
func NewContextQualityScorer(
	db *vectorgraphdb.VectorGraphDB,
	weights QualityWeights,
	freshness *FreshnessTracker,
	trust *TrustHierarchy,
) *ContextQualityScorer {
	return &ContextQualityScorer{
		db:        db,
		weights:   weights,
		tokenizer: &DefaultTokenizer{},
		freshness: freshness,
		trust:     trust,
	}
}

// Score computes quality score for a single node.
func (s *ContextQualityScorer) Score(node *vectorgraphdb.GraphNode, querySimilarity float64) (*ContextItem, error) {
	item := &ContextItem{
		Node:     node,
		Selected: false,
	}

	item.Components = s.computeComponents(node, querySimilarity)
	item.QualityScore = s.computeQualityScore(item.Components)
	item.TokenCount = s.estimateTokens(node)
	item.ScorePerToken = s.computeScorePerToken(item.QualityScore, item.TokenCount)

	return item, nil
}

func (s *ContextQualityScorer) computeComponents(node *vectorgraphdb.GraphNode, querySimilarity float64) QualityComponents {
	components := QualityComponents{
		Relevance: querySimilarity,
	}

	components.Freshness = s.computeFreshnessComponent(node.ID)
	components.Trust = s.computeTrustComponent(node.ID)
	components.Density = s.computeDensityComponent(node)
	components.Redundancy = 0

	return components
}

func (s *ContextQualityScorer) computeFreshnessComponent(nodeID string) float64 {
	if s.freshness == nil {
		return 0.8
	}

	info, err := s.freshness.GetFreshness(nodeID)
	if err != nil {
		return 0.8
	}
	return info.FreshnessScore
}

func (s *ContextQualityScorer) computeTrustComponent(nodeID string) float64 {
	if s.trust == nil {
		return 0.7
	}

	info, err := s.trust.GetTrustInfo(nodeID)
	if err != nil {
		return 0.7
	}
	return info.TrustScore
}

func (s *ContextQualityScorer) computeDensityComponent(node *vectorgraphdb.GraphNode) float64 {
	content := extractNodeContent(node)
	tokens := s.tokenizer.EstimateTokens(content)
	if tokens == 0 {
		return 0.5
	}

	uniqueWords := countUniqueWords(content)
	density := float64(uniqueWords) / float64(tokens)

	if density > 0.8 {
		return 1.0
	}
	if density > 0.5 {
		return 0.8
	}
	return 0.6
}

func extractNodeContent(node *vectorgraphdb.GraphNode) string {
	if content, ok := node.Metadata["content"].(string); ok {
		return content
	}
	if desc, ok := node.Metadata["description"].(string); ok {
		return desc
	}
	return ""
}

func countUniqueWords(content string) int {
	words := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(content)) {
		words[word] = true
	}
	return len(words)
}

func (s *ContextQualityScorer) computeQualityScore(c QualityComponents) float64 {
	score := s.weights.Relevance*c.Relevance +
		s.weights.Freshness*c.Freshness +
		s.weights.Trust*c.Trust +
		s.weights.Density*c.Density -
		s.weights.Redundancy*c.Redundancy

	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func (s *ContextQualityScorer) estimateTokens(node *vectorgraphdb.GraphNode) int {
	content := extractNodeContent(node)
	baseTokens := s.tokenizer.EstimateTokens(content)
	overhead := 20
	return baseTokens + overhead
}

func (s *ContextQualityScorer) computeScorePerToken(score float64, tokens int) float64 {
	if tokens == 0 {
		return 0
	}
	return score / float64(tokens)
}

// SelectContext selects optimal context items within a token budget.
func (s *ContextQualityScorer) SelectContext(
	results []vectorgraphdb.SearchResult,
	tokenBudget int,
) ([]*ContextItem, int, error) {
	items := s.scoreAllResults(results)
	s.applyRedundancyPenalty(items)
	s.sortByScorePerToken(items)

	selected, usedTokens := s.greedySelect(items, tokenBudget)
	remainingBudget := tokenBudget - usedTokens

	return selected, remainingBudget, nil
}

func (s *ContextQualityScorer) scoreAllResults(results []vectorgraphdb.SearchResult) []*ContextItem {
	items := make([]*ContextItem, 0, len(results))
	for _, r := range results {
		item, err := s.Score(r.Node, r.Similarity)
		if err != nil {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (s *ContextQualityScorer) applyRedundancyPenalty(items []*ContextItem) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			similarity := s.computeContentSimilarity(items[i].Node, items[j].Node)
			if similarity > 0.7 {
				items[j].Components.Redundancy += similarity * 0.3
				items[j].QualityScore = s.computeQualityScore(items[j].Components)
				items[j].ScorePerToken = s.computeScorePerToken(items[j].QualityScore, items[j].TokenCount)
			}
		}
	}
}

func (s *ContextQualityScorer) computeContentSimilarity(a, b *vectorgraphdb.GraphNode) float64 {
	contentA := extractNodeContent(a)
	contentB := extractNodeContent(b)

	wordsA := buildWordSet(contentA)
	wordsB := buildWordSet(contentB)

	intersection := 0
	for word := range wordsA {
		if wordsB[word] {
			intersection++
		}
	}

	union := len(wordsA) + len(wordsB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func buildWordSet(content string) map[string]bool {
	words := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(content)) {
		words[word] = true
	}
	return words
}

func (s *ContextQualityScorer) sortByScorePerToken(items []*ContextItem) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].ScorePerToken > items[j].ScorePerToken
	})
}

func (s *ContextQualityScorer) greedySelect(items []*ContextItem, budget int) ([]*ContextItem, int) {
	selected := make([]*ContextItem, 0)
	usedTokens := 0

	for _, item := range items {
		if usedTokens+item.TokenCount > budget {
			continue
		}

		item.Selected = true
		item.Reason = "selected for high quality-per-token"
		selected = append(selected, item)
		usedTokens += item.TokenCount
	}

	return selected, usedTokens
}

// ScoreBatch scores multiple nodes in batch.
func (s *ContextQualityScorer) ScoreBatch(
	nodes []*vectorgraphdb.GraphNode,
	similarities []float64,
) ([]*ContextItem, error) {
	if len(nodes) != len(similarities) {
		return nil, nil
	}

	items := make([]*ContextItem, 0, len(nodes))
	for i, node := range nodes {
		item, err := s.Score(node, similarities[i])
		if err != nil {
			continue
		}
		items = append(items, item)
	}

	return items, nil
}

// OptimizeSelection performs knapsack-style optimization for context selection.
func (s *ContextQualityScorer) OptimizeSelection(
	items []*ContextItem,
	budget int,
) ([]*ContextItem, float64) {
	n := len(items)
	if n == 0 || budget <= 0 {
		return nil, 0
	}

	if budget > 10000 {
		return s.greedyOptimize(items, budget)
	}

	return s.dpOptimize(items, budget)
}

func (s *ContextQualityScorer) greedyOptimize(items []*ContextItem, budget int) ([]*ContextItem, float64) {
	sorted := make([]*ContextItem, len(items))
	copy(sorted, items)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ScorePerToken > sorted[j].ScorePerToken
	})

	selected := make([]*ContextItem, 0)
	totalScore := 0.0
	usedTokens := 0

	for _, item := range sorted {
		if usedTokens+item.TokenCount <= budget {
			selected = append(selected, item)
			totalScore += item.QualityScore
			usedTokens += item.TokenCount
		}
	}

	return selected, totalScore
}

func (s *ContextQualityScorer) dpOptimize(items []*ContextItem, budget int) ([]*ContextItem, float64) {
	n := len(items)
	dp := s.initDPTable(n, budget)
	s.fillDPTable(dp, items, budget)

	selected := s.backtrackSelection(items, dp, budget)
	return selected, dp[n][budget]
}

func (s *ContextQualityScorer) initDPTable(n, budget int) [][]float64 {
	dp := make([][]float64, n+1)
	for i := range dp {
		dp[i] = make([]float64, budget+1)
	}
	return dp
}

func (s *ContextQualityScorer) fillDPTable(dp [][]float64, items []*ContextItem, budget int) {
	for i := 1; i < len(dp); i++ {
		item := items[i-1]
		for w := 0; w <= budget; w++ {
			dp[i][w] = s.computeDPCell(dp, i, w, item)
		}
	}
}

func (s *ContextQualityScorer) computeDPCell(dp [][]float64, i, w int, item *ContextItem) float64 {
	exclude := dp[i-1][w]
	if item.TokenCount > w {
		return exclude
	}
	include := dp[i-1][w-item.TokenCount] + item.QualityScore
	return max(exclude, include)
}

func (s *ContextQualityScorer) backtrackSelection(
	items []*ContextItem,
	dp [][]float64,
	budget int,
) []*ContextItem {
	selected := make([]*ContextItem, 0)
	w := budget

	for i := len(items); i > 0 && w > 0; i-- {
		if dp[i][w] != dp[i-1][w] {
			item := items[i-1]
			item.Selected = true
			selected = append(selected, item)
			w -= item.TokenCount
		}
	}

	return selected
}

// GetQualityMetrics returns quality scoring statistics.
func (s *ContextQualityScorer) GetQualityMetrics(items []*ContextItem) *QualityMetrics {
	metrics := &QualityMetrics{}

	if len(items) == 0 {
		return metrics
	}

	for _, item := range items {
		s.accumulateItemMetrics(metrics, item)
	}

	s.computeAverages(metrics)
	return metrics
}

func (s *ContextQualityScorer) accumulateItemMetrics(metrics *QualityMetrics, item *ContextItem) {
	metrics.TotalItems++
	metrics.TotalTokens += item.TokenCount
	metrics.TotalScore += item.QualityScore

	if item.Selected {
		metrics.SelectedItems++
		metrics.SelectedTokens += item.TokenCount
		metrics.SelectedScore += item.QualityScore
	}
}

func (s *ContextQualityScorer) computeAverages(metrics *QualityMetrics) {
	metrics.AvgScore = metrics.TotalScore / float64(metrics.TotalItems)
	if metrics.SelectedItems > 0 {
		metrics.AvgSelectedScore = metrics.SelectedScore / float64(metrics.SelectedItems)
	}
}

// QualityMetrics contains quality scoring statistics.
type QualityMetrics struct {
	TotalItems       int     `json:"total_items"`
	SelectedItems    int     `json:"selected_items"`
	TotalTokens      int     `json:"total_tokens"`
	SelectedTokens   int     `json:"selected_tokens"`
	TotalScore       float64 `json:"total_score"`
	SelectedScore    float64 `json:"selected_score"`
	AvgScore         float64 `json:"avg_score"`
	AvgSelectedScore float64 `json:"avg_selected_score"`
}
