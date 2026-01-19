// Package context provides ES.3 TopicClusterEviction for the Context Virtualization System.
// TopicClusterEviction clusters entries by topic/keywords and evicts entire clusters
// that are less relevant to the current context.
package context

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// TopicCluster represents a group of related content entries.
type TopicCluster struct {
	Topic       string           // Primary topic/keyword for the cluster
	Entries     []*ContentEntry  // Entries in this cluster
	TotalTokens int              // Sum of tokens in the cluster
	AvgAge      time.Duration    // Average age of entries
	Coherence   float64          // How related entries are (0.0-1.0)
}

// TopicClusterEviction implements EvictionStrategy by clustering entries by topic
// and evicting entire clusters that are less relevant to the current context.
type TopicClusterEviction struct {
	mu             sync.RWMutex
	currentTopics  []string // Topics to preserve (high relevance)
	minClusterSize int      // Minimum entries to form a cluster
}

// NewTopicClusterEviction creates a new TopicClusterEviction with configuration.
func NewTopicClusterEviction(currentTopics []string, minClusterSize int) *TopicClusterEviction {
	if minClusterSize < 1 {
		minClusterSize = 1
	}
	return &TopicClusterEviction{
		currentTopics:  copyTopics(currentTopics),
		minClusterSize: minClusterSize,
	}
}

// copyTopics creates a copy of the topics slice.
func copyTopics(topics []string) []string {
	if topics == nil {
		return nil
	}
	result := make([]string, len(topics))
	copy(result, topics)
	return result
}

// SetCurrentTopics updates the topics to preserve.
func (e *TopicClusterEviction) SetCurrentTopics(topics []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.currentTopics = copyTopics(topics)
}

// GetCurrentTopics returns the current topics to preserve.
func (e *TopicClusterEviction) GetCurrentTopics() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return copyTopics(e.currentTopics)
}

// SetMinClusterSize updates the minimum cluster size.
func (e *TopicClusterEviction) SetMinClusterSize(size int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if size < 1 {
		size = 1
	}
	e.minClusterSize = size
}

// GetMinClusterSize returns the minimum cluster size.
func (e *TopicClusterEviction) GetMinClusterSize() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.minClusterSize
}

// Name returns the strategy identifier.
func (e *TopicClusterEviction) Name() string {
	return "topic_cluster"
}

// Priority returns the strategy priority. Lower values indicate higher priority.
func (e *TopicClusterEviction) Priority() int {
	return 50
}

// SelectForEviction selects entries to evict by clustering and evicting
// entire clusters that are least relevant to current topics.
func (e *TopicClusterEviction) SelectForEviction(
	_ context.Context,
	entries []*ContentEntry,
	targetTokens int,
) ([]*ContentEntry, error) {
	if len(entries) == 0 || targetTokens <= 0 {
		return nil, nil
	}

	clusters := e.clusterByTopic(entries)
	e.calculateClusterMetrics(clusters)
	e.sortClustersByEvictionPriority(clusters)

	return e.selectClustersForEviction(clusters, targetTokens), nil
}

// clusterByTopic groups entries by their primary topic/keyword.
func (e *TopicClusterEviction) clusterByTopic(entries []*ContentEntry) []*TopicCluster {
	topicMap := make(map[string][]*ContentEntry)

	for _, entry := range entries {
		topic := e.extractPrimaryTopic(entry)
		topicMap[topic] = append(topicMap[topic], entry)
	}

	return e.buildClusters(topicMap)
}

// extractPrimaryTopic returns the primary topic for an entry.
func (e *TopicClusterEviction) extractPrimaryTopic(entry *ContentEntry) string {
	if entry == nil {
		return ""
	}

	if len(entry.Keywords) > 0 {
		return strings.ToLower(entry.Keywords[0])
	}

	return e.extractTopicFromContent(entry.Content)
}

// extractTopicFromContent extracts a topic from content when no keywords exist.
func (e *TopicClusterEviction) extractTopicFromContent(content string) string {
	if content == "" {
		return ""
	}

	words := strings.Fields(strings.ToLower(content))
	if len(words) == 0 {
		return ""
	}

	return findMostSignificantWord(words)
}

// findMostSignificantWord returns the first word longer than 4 chars, or first word.
func findMostSignificantWord(words []string) string {
	for _, word := range words {
		if len(word) > 4 {
			return word
		}
	}
	return words[0]
}

// buildClusters creates TopicCluster objects from the topic map.
func (e *TopicClusterEviction) buildClusters(topicMap map[string][]*ContentEntry) []*TopicCluster {
	e.mu.RLock()
	minSize := e.minClusterSize
	e.mu.RUnlock()

	var clusters []*TopicCluster
	for topic, entryList := range topicMap {
		cluster := buildSingleCluster(topic, entryList, minSize)
		if cluster != nil {
			clusters = append(clusters, cluster)
		}
	}
	return clusters
}

// buildSingleCluster creates a cluster if it meets the minimum size requirement.
func buildSingleCluster(topic string, entries []*ContentEntry, minSize int) *TopicCluster {
	if len(entries) < minSize {
		return nil
	}

	return &TopicCluster{
		Topic:       topic,
		Entries:     entries,
		TotalTokens: CalculateTotalTokens(entries),
	}
}

// calculateClusterMetrics computes AvgAge and Coherence for each cluster.
func (e *TopicClusterEviction) calculateClusterMetrics(clusters []*TopicCluster) {
	now := time.Now()
	for _, cluster := range clusters {
		cluster.AvgAge = calculateAverageAge(cluster.Entries, now)
		cluster.Coherence = calculateCoherence(cluster.Entries)
	}
}

// calculateAverageAge computes the average age of entries in a cluster.
func calculateAverageAge(entries []*ContentEntry, now time.Time) time.Duration {
	if len(entries) == 0 {
		return 0
	}

	var totalAge time.Duration
	for _, entry := range entries {
		totalAge += now.Sub(entry.Timestamp)
	}
	return totalAge / time.Duration(len(entries))
}

// calculateCoherence computes how related entries are (based on keyword overlap).
func calculateCoherence(entries []*ContentEntry) float64 {
	if len(entries) < 2 {
		return 1.0
	}

	allKeywords := collectAllKeywords(entries)
	if len(allKeywords) == 0 {
		return 0.5 // Default coherence when no keywords
	}

	return calculateKeywordOverlap(entries, allKeywords)
}

// collectAllKeywords gathers unique keywords from all entries.
func collectAllKeywords(entries []*ContentEntry) map[string]int {
	keywords := make(map[string]int)
	for _, entry := range entries {
		for _, kw := range entry.Keywords {
			keywords[strings.ToLower(kw)]++
		}
	}
	return keywords
}

// calculateKeywordOverlap computes the overlap ratio for coherence.
func calculateKeywordOverlap(entries []*ContentEntry, allKeywords map[string]int) float64 {
	shared := countSharedKeywords(allKeywords, len(entries))
	if len(allKeywords) == 0 {
		return 0.5
	}
	return float64(shared) / float64(len(allKeywords))
}

// countSharedKeywords counts keywords appearing in multiple entries.
func countSharedKeywords(keywords map[string]int, entryCount int) int {
	shared := 0
	threshold := entryCount / 2
	if threshold < 1 {
		threshold = 1
	}

	for _, count := range keywords {
		if count > threshold {
			shared++
		}
	}
	return shared
}

// sortClustersByEvictionPriority sorts clusters so eviction candidates come first.
func (e *TopicClusterEviction) sortClustersByEvictionPriority(clusters []*TopicCluster) {
	e.mu.RLock()
	topics := e.currentTopics
	e.mu.RUnlock()

	sort.Slice(clusters, func(i, j int) bool {
		scoreI := calculateEvictionScore(clusters[i], topics)
		scoreJ := calculateEvictionScore(clusters[j], topics)
		return scoreI > scoreJ // Higher score = evict first
	})
}

// calculateEvictionScore computes how evictable a cluster is.
// Higher score means more likely to be evicted.
func calculateEvictionScore(cluster *TopicCluster, preserveTopics []string) float64 {
	score := 0.0

	// Penalize clusters matching current topics (lower eviction priority)
	if isRelevantTopic(cluster.Topic, preserveTopics) {
		score -= 100.0
	}

	// Older clusters are more evictable
	score += cluster.AvgAge.Hours() * 0.1

	// High coherence clusters make better eviction candidates
	score += cluster.Coherence * 20.0

	return score
}

// isRelevantTopic checks if a cluster topic matches any preserved topic.
func isRelevantTopic(clusterTopic string, preserveTopics []string) bool {
	normalizedCluster := strings.ToLower(clusterTopic)
	for _, pt := range preserveTopics {
		if topicsMatch(normalizedCluster, strings.ToLower(pt)) {
			return true
		}
	}
	return false
}

// topicsMatch checks if two topics are related.
func topicsMatch(topic1, topic2 string) bool {
	if topic1 == "" || topic2 == "" {
		return false
	}
	return topic1 == topic2 || strings.Contains(topic1, topic2) || strings.Contains(topic2, topic1)
}

// selectClustersForEviction selects entire clusters until target tokens reached.
func (e *TopicClusterEviction) selectClustersForEviction(
	clusters []*TopicCluster,
	targetTokens int,
) []*ContentEntry {
	var selected []*ContentEntry
	tokensSelected := 0

	for _, cluster := range clusters {
		if tokensSelected >= targetTokens {
			break
		}
		selected = append(selected, cluster.Entries...)
		tokensSelected += cluster.TotalTokens
	}

	return selected
}

// EstimateEvictableTokens returns total tokens available for eviction.
func (e *TopicClusterEviction) EstimateEvictableTokens(entries []*ContentEntry) int {
	clusters := e.clusterByTopic(entries)

	total := 0
	for _, cluster := range clusters {
		total += cluster.TotalTokens
	}
	return total
}

// GetClusterCount returns the number of clusters that would be formed.
func (e *TopicClusterEviction) GetClusterCount(entries []*ContentEntry) int {
	clusters := e.clusterByTopic(entries)
	return len(clusters)
}

// GetClusters returns the clusters for inspection (useful for testing/debugging).
func (e *TopicClusterEviction) GetClusters(entries []*ContentEntry) []*TopicCluster {
	clusters := e.clusterByTopic(entries)
	e.calculateClusterMetrics(clusters)
	return clusters
}
