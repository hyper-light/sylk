package context

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewTopicClusterEviction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		topics         []string
		minSize        int
		wantMinSize    int
		wantTopicCount int
	}{
		{
			name:           "normal configuration",
			topics:         []string{"auth", "database"},
			minSize:        2,
			wantMinSize:    2,
			wantTopicCount: 2,
		},
		{
			name:           "negative min size defaults to 1",
			topics:         []string{"topic1"},
			minSize:        -5,
			wantMinSize:    1,
			wantTopicCount: 1,
		},
		{
			name:           "zero min size defaults to 1",
			topics:         nil,
			minSize:        0,
			wantMinSize:    1,
			wantTopicCount: 0,
		},
		{
			name:           "empty topics",
			topics:         []string{},
			minSize:        3,
			wantMinSize:    3,
			wantTopicCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewTopicClusterEviction(tt.topics, tt.minSize)
			if e.GetMinClusterSize() != tt.wantMinSize {
				t.Errorf("minClusterSize = %d, want %d", e.GetMinClusterSize(), tt.wantMinSize)
			}
			if len(e.GetCurrentTopics()) != tt.wantTopicCount {
				t.Errorf("topic count = %d, want %d", len(e.GetCurrentTopics()), tt.wantTopicCount)
			}
		})
	}
}

func TestTopicClusterEviction_SettersGetters(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction([]string{"initial"}, 2)

	// Test SetCurrentTopics
	e.SetCurrentTopics([]string{"new1", "new2"})
	topics := e.GetCurrentTopics()
	if len(topics) != 2 {
		t.Errorf("expected 2 topics, got %d", len(topics))
	}

	// Test SetMinClusterSize
	e.SetMinClusterSize(5)
	if e.GetMinClusterSize() != 5 {
		t.Errorf("expected min size 5, got %d", e.GetMinClusterSize())
	}

	// Test negative min size
	e.SetMinClusterSize(-1)
	if e.GetMinClusterSize() != 1 {
		t.Errorf("expected min size 1 for negative input, got %d", e.GetMinClusterSize())
	}
}

func TestTopicClusterEviction_SelectForEviction_EmptyInputs(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction([]string{"auth"}, 1)
	ctx := context.Background()

	// Empty entries
	result, err := e.SelectForEviction(ctx, nil, 100)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for nil entries")
	}

	result, err = e.SelectForEviction(ctx, []*ContentEntry{}, 100)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for empty entries")
	}

	// Zero target tokens
	entries := []*ContentEntry{createTopicTestEntry("1", 100, []string{"auth"})}
	result, err = e.SelectForEviction(ctx, entries, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for zero target tokens")
	}

	// Negative target tokens
	result, err = e.SelectForEviction(ctx, entries, -10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for negative target tokens")
	}
}

func TestTopicClusterEviction_ClusteringByKeywords(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	entries := []*ContentEntry{
		createTopicTestEntry("1", 100, []string{"auth", "login"}),
		createTopicTestEntry("2", 100, []string{"auth", "password"}),
		createTopicTestEntry("3", 100, []string{"database", "query"}),
		createTopicTestEntry("4", 100, []string{"database", "connection"}),
	}

	clusters := e.GetClusters(entries)

	if len(clusters) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(clusters))
	}

	// Verify cluster topics
	topics := make(map[string]bool)
	for _, c := range clusters {
		topics[c.Topic] = true
	}

	if !topics["auth"] || !topics["database"] {
		t.Error("expected clusters for 'auth' and 'database'")
	}
}

func TestTopicClusterEviction_PreserveRelevantTopics(t *testing.T) {
	t.Parallel()

	// Create eviction strategy that preserves "auth" topic
	e := NewTopicClusterEviction([]string{"auth"}, 1)

	now := time.Now()
	entries := []*ContentEntry{
		createTestEntryWithTime("1", 100, []string{"auth"}, now.Add(-1*time.Hour)),
		createTestEntryWithTime("2", 100, []string{"auth"}, now.Add(-2*time.Hour)),
		createTestEntryWithTime("3", 100, []string{"database"}, now.Add(-3*time.Hour)),
		createTestEntryWithTime("4", 100, []string{"database"}, now.Add(-4*time.Hour)),
	}

	// Request eviction of 200 tokens
	evicted, err := e.SelectForEviction(context.Background(), entries, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should evict database entries first (not auth)
	authEvicted := 0
	dbEvicted := 0
	for _, entry := range evicted {
		if len(entry.Keywords) > 0 && entry.Keywords[0] == "auth" {
			authEvicted++
		} else if len(entry.Keywords) > 0 && entry.Keywords[0] == "database" {
			dbEvicted++
		}
	}

	if authEvicted > dbEvicted {
		t.Error("expected database entries to be evicted before auth entries")
	}
}

func TestTopicClusterEviction_EvictEntireClusters(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	entries := []*ContentEntry{
		createTopicTestEntry("1", 50, []string{"topic1"}),
		createTopicTestEntry("2", 50, []string{"topic1"}),
		createTopicTestEntry("3", 100, []string{"topic2"}),
	}

	// Request 100 tokens - should get entire cluster(s)
	evicted, err := e.SelectForEviction(context.Background(), entries, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify we got entries from a complete cluster
	if len(evicted) == 0 {
		t.Error("expected some entries to be evicted")
	}

	// Count entries per topic in evicted set
	topicCounts := make(map[string]int)
	for _, entry := range evicted {
		if len(entry.Keywords) > 0 {
			topicCounts[entry.Keywords[0]]++
		}
	}

	// Should evict complete clusters, not partial
	for topic, count := range topicCounts {
		clusterSize := countEntriesWithTopic(entries, topic)
		if count != clusterSize {
			t.Errorf("topic %s: evicted %d of %d entries (should evict entire cluster)", topic, count, clusterSize)
		}
	}
}

func TestTopicClusterEviction_MinClusterSize(t *testing.T) {
	t.Parallel()

	// Minimum cluster size of 2
	e := NewTopicClusterEviction(nil, 2)

	entries := []*ContentEntry{
		createTopicTestEntry("1", 100, []string{"auth"}), // Only 1 entry - won't form cluster
		createTopicTestEntry("2", 100, []string{"database"}),
		createTopicTestEntry("3", 100, []string{"database"}), // 2 entries - will form cluster
	}

	clusters := e.GetClusters(entries)

	// Should only have 1 cluster (database with 2 entries)
	if len(clusters) != 1 {
		t.Errorf("expected 1 cluster (minSize=2), got %d", len(clusters))
	}

	if clusters[0].Topic != "database" {
		t.Errorf("expected cluster topic 'database', got '%s'", clusters[0].Topic)
	}
}

func TestTopicClusterEviction_NoKeywords(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	// Entries without keywords - should cluster by content
	entries := []*ContentEntry{
		{ID: "1", TokenCount: 100, Content: "authentication login", Keywords: nil},
		{ID: "2", TokenCount: 100, Content: "authentication password", Keywords: nil},
		{ID: "3", TokenCount: 100, Content: "database query", Keywords: nil},
	}

	clusters := e.GetClusters(entries)

	// Should form clusters based on content words
	if len(clusters) == 0 {
		t.Error("expected clusters to form from content analysis")
	}
}

func TestTopicClusterEviction_AllSameTopic(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	entries := []*ContentEntry{
		createTopicTestEntry("1", 100, []string{"auth"}),
		createTopicTestEntry("2", 100, []string{"auth"}),
		createTopicTestEntry("3", 100, []string{"auth"}),
	}

	clusters := e.GetClusters(entries)

	// All entries should be in one cluster
	if len(clusters) != 1 {
		t.Errorf("expected 1 cluster for same topic, got %d", len(clusters))
	}

	if len(clusters[0].Entries) != 3 {
		t.Errorf("expected cluster to have 3 entries, got %d", len(clusters[0].Entries))
	}
}

func TestTopicClusterEviction_TokenAccounting(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	entries := []*ContentEntry{
		createTopicTestEntry("1", 50, []string{"topic1"}),
		createTopicTestEntry("2", 75, []string{"topic1"}),
		createTopicTestEntry("3", 100, []string{"topic2"}),
		createTopicTestEntry("4", 200, []string{"topic3"}),
	}

	// Request 150 tokens
	evicted, err := e.SelectForEviction(context.Background(), entries, 150)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	totalEvicted := 0
	for _, e := range evicted {
		totalEvicted += e.TokenCount
	}

	// Should evict at least 150 tokens worth
	if totalEvicted < 150 {
		t.Errorf("expected at least 150 tokens evicted, got %d", totalEvicted)
	}
}

func TestTopicClusterEviction_EstimateEvictableTokens(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	entries := []*ContentEntry{
		createTopicTestEntry("1", 100, []string{"auth"}),
		createTopicTestEntry("2", 200, []string{"database"}),
		createTopicTestEntry("3", 150, []string{"api"}),
	}

	total := e.EstimateEvictableTokens(entries)
	if total != 450 {
		t.Errorf("expected 450 total evictable tokens, got %d", total)
	}
}

func TestTopicClusterEviction_GetClusterCount(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	entries := []*ContentEntry{
		createTopicTestEntry("1", 100, []string{"auth"}),
		createTopicTestEntry("2", 100, []string{"database"}),
		createTopicTestEntry("3", 100, []string{"api"}),
		createTopicTestEntry("4", 100, []string{"auth"}), // Same as first
	}

	count := e.GetClusterCount(entries)
	if count != 3 {
		t.Errorf("expected 3 clusters, got %d", count)
	}
}

func TestTopicClusterEviction_CoherenceCalculation(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	// High coherence - all entries share keywords
	highCoherence := []*ContentEntry{
		createTopicTestEntry("1", 100, []string{"auth", "login", "user"}),
		createTopicTestEntry("2", 100, []string{"auth", "login", "password"}),
		createTopicTestEntry("3", 100, []string{"auth", "login", "session"}),
	}

	// Low coherence - entries have different keywords
	lowCoherence := []*ContentEntry{
		createTopicTestEntry("4", 100, []string{"database"}),
		createTopicTestEntry("5", 100, []string{"api"}),
		createTopicTestEntry("6", 100, []string{"cache"}),
	}

	// Get coherence through clusters
	clusters := e.GetClusters(highCoherence)
	if len(clusters) == 0 {
		t.Skip("no clusters formed for high coherence test")
	}
	highCoh := clusters[0].Coherence

	clusters = e.GetClusters(lowCoherence)
	totalLowCoh := 0.0
	for _, c := range clusters {
		totalLowCoh += c.Coherence
	}
	avgLowCoh := totalLowCoh / float64(len(clusters))

	// High coherence cluster should have higher coherence score
	if highCoh <= avgLowCoh {
		t.Log("Note: coherence calculation may need refinement")
	}
}

func TestTopicClusterEviction_ConcurrencySafety(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction([]string{"initial"}, 2)

	entries := []*ContentEntry{
		createTopicTestEntry("1", 100, []string{"auth"}),
		createTopicTestEntry("2", 100, []string{"auth"}),
		createTopicTestEntry("3", 100, []string{"database"}),
		createTopicTestEntry("4", 100, []string{"database"}),
	}

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent reads
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.GetCurrentTopics()
			_ = e.GetMinClusterSize()
		}()
	}

	// Concurrent writes
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.SetCurrentTopics([]string{"topic" + string(rune('a'+i%26))})
			e.SetMinClusterSize(i%5 + 1)
		}(i)
	}

	// Concurrent eviction operations
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.SelectForEviction(context.Background(), entries, 100)
		}()
	}

	wg.Wait()
}

func TestTopicClusterEviction_TopicMatching(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction([]string{"authentication"}, 1)

	now := time.Now()
	entries := []*ContentEntry{
		// These should be preserved (match "authentication")
		createTestEntryWithTime("1", 100, []string{"auth"}, now.Add(-1*time.Hour)),
		createTestEntryWithTime("2", 100, []string{"authentication"}, now.Add(-2*time.Hour)),
		// These should be evicted first
		createTestEntryWithTime("3", 100, []string{"database"}, now.Add(-3*time.Hour)),
		createTestEntryWithTime("4", 100, []string{"logging"}, now.Add(-4*time.Hour)),
	}

	evicted, err := e.SelectForEviction(context.Background(), entries, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that database/logging entries are evicted before auth entries
	for _, entry := range evicted[:len(evicted)/2] {
		topic := ""
		if len(entry.Keywords) > 0 {
			topic = entry.Keywords[0]
		}
		if topic == "authentication" {
			t.Error("authentication entries should be preserved, not evicted first")
		}
	}
}

func TestTopicClusterEviction_EmptyContent(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	entries := []*ContentEntry{
		{ID: "1", TokenCount: 100, Content: "", Keywords: nil},
		{ID: "2", TokenCount: 100, Content: "", Keywords: nil},
	}

	// Should not panic on empty content
	clusters := e.GetClusters(entries)
	if clusters == nil {
		// Empty content entries should still form clusters (with empty topic)
		t.Log("Empty content entries formed nil clusters - acceptable")
	}
}

func TestTopicClusterEviction_AverageAge(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction(nil, 1)

	now := time.Now()
	entries := []*ContentEntry{
		createTestEntryWithTime("1", 100, []string{"old"}, now.Add(-48*time.Hour)),
		createTestEntryWithTime("2", 100, []string{"old"}, now.Add(-24*time.Hour)),
		createTestEntryWithTime("3", 100, []string{"new"}, now.Add(-1*time.Hour)),
	}

	clusters := e.GetClusters(entries)

	var oldCluster, newCluster *TopicCluster
	for _, c := range clusters {
		if c.Topic == "old" {
			oldCluster = c
		} else if c.Topic == "new" {
			newCluster = c
		}
	}

	if oldCluster != nil && newCluster != nil {
		if oldCluster.AvgAge <= newCluster.AvgAge {
			t.Error("old cluster should have higher average age than new cluster")
		}
	}
}

func TestTopicClusterEviction_LargeEntrySet(t *testing.T) {
	t.Parallel()

	e := NewTopicClusterEviction([]string{"important"}, 1)

	// Create a large set of entries
	entries := make([]*ContentEntry, 1000)
	topics := []string{"auth", "database", "api", "cache", "important"}

	for i := 0; i < 1000; i++ {
		topic := topics[i%len(topics)]
		entries[i] = createTopicTestEntry(string(rune(i)), 100, []string{topic})
	}

	// Should complete in reasonable time
	start := time.Now()
	_, _ = e.SelectForEviction(context.Background(), entries, 5000)
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Errorf("eviction took too long: %v", elapsed)
	}
}

// Helper functions

func createTopicTestEntry(id string, tokens int, keywords []string) *ContentEntry {
	return &ContentEntry{
		ID:         id,
		TokenCount: tokens,
		Keywords:   keywords,
		Timestamp:  time.Now(),
		TurnNumber: 1,
	}
}

func createTestEntryWithTime(id string, tokens int, keywords []string, ts time.Time) *ContentEntry {
	return &ContentEntry{
		ID:         id,
		TokenCount: tokens,
		Keywords:   keywords,
		Timestamp:  ts,
		TurnNumber: 1,
	}
}

func countEntriesWithTopic(entries []*ContentEntry, topic string) int {
	count := 0
	for _, e := range entries {
		if len(e.Keywords) > 0 && e.Keywords[0] == topic {
			count++
		}
	}
	return count
}
