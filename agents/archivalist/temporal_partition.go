package archivalist

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// =============================================================================
// Temporal Partitioning
// =============================================================================

// PartitionGranularity defines the time granularity for partitions
type PartitionGranularity string

const (
	PartitionHourly  PartitionGranularity = "hourly"
	PartitionDaily   PartitionGranularity = "daily"
	PartitionWeekly  PartitionGranularity = "weekly"
	PartitionMonthly PartitionGranularity = "monthly"
)

// TemporalPartitionManager manages time-based entry partitions
type TemporalPartitionManager struct {
	mu sync.RWMutex

	// Partitions by key (e.g., "2024-01-15" for daily)
	partitions map[string]*TemporalPartition

	// Partition index for range queries
	sortedKeys []string

	// Configuration
	config TemporalPartitionConfig

	// Statistics
	stats TemporalPartitionStats
}

// TemporalPartitionConfig configures partition management
type TemporalPartitionConfig struct {
	// Granularity for partitioning
	Granularity PartitionGranularity `json:"granularity"`

	// Maximum entries per partition before splitting
	MaxEntriesPerPartition int `json:"max_entries_per_partition"`

	// Maximum partitions to keep in memory
	MaxPartitionsInMemory int `json:"max_partitions_in_memory"`

	// Whether to auto-compact old partitions
	AutoCompact bool `json:"auto_compact"`

	// Age threshold for compaction
	CompactThreshold time.Duration `json:"compact_threshold"`
}

// DefaultTemporalPartitionConfig returns sensible defaults
func DefaultTemporalPartitionConfig() TemporalPartitionConfig {
	return TemporalPartitionConfig{
		Granularity:            PartitionDaily,
		MaxEntriesPerPartition: 10000,
		MaxPartitionsInMemory:  30,
		AutoCompact:            true,
		CompactThreshold:       7 * 24 * time.Hour,
	}
}

// TemporalPartition represents a time-bounded partition of entries
type TemporalPartition struct {
	mu sync.RWMutex

	// Partition key (formatted time string)
	Key string `json:"key"`

	// Time bounds
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`

	// Entry IDs in this partition
	EntryIDs []string `json:"entry_ids"`

	// Quick lookup by ID
	entrySet map[string]bool

	// Statistics
	EntryCount     int        `json:"entry_count"`
	TotalTokens    int        `json:"total_tokens"`
	IsCompacted    bool       `json:"is_compacted"`
	CompactedAt    *time.Time `json:"compacted_at,omitempty"`
	LastAccessTime time.Time  `json:"last_access_time"`
}

// TemporalPartitionStats contains partition manager statistics
type TemporalPartitionStats struct {
	TotalPartitions   int   `json:"total_partitions"`
	TotalEntries      int   `json:"total_entries"`
	CompactedCount    int   `json:"compacted_count"`
	ActivePartitions  int   `json:"active_partitions"`
	QueriesExecuted   int64 `json:"queries_executed"`
	PartitionsScanned int64 `json:"partitions_scanned"`
	CacheHits         int64 `json:"cache_hits"`
}

// NewTemporalPartitionManager creates a new partition manager
func NewTemporalPartitionManager(config TemporalPartitionConfig) *TemporalPartitionManager {
	if config.MaxEntriesPerPartition == 0 {
		config = DefaultTemporalPartitionConfig()
	}

	return &TemporalPartitionManager{
		partitions: make(map[string]*TemporalPartition),
		sortedKeys: make([]string, 0),
		config:     config,
	}
}

// =============================================================================
// Partition Key Generation
// =============================================================================

// GeneratePartitionKey creates a partition key for a given time
func (pm *TemporalPartitionManager) GeneratePartitionKey(t time.Time) string {
	switch pm.config.Granularity {
	case PartitionHourly:
		return t.Format("2006-01-02-15")
	case PartitionDaily:
		return t.Format("2006-01-02")
	case PartitionWeekly:
		year, week := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", year, week)
	case PartitionMonthly:
		return t.Format("2006-01")
	default:
		return t.Format("2006-01-02")
	}
}

// GetPartitionBounds returns start and end times for a partition key
func (pm *TemporalPartitionManager) GetPartitionBounds(key string) (time.Time, time.Time) {
	var start, end time.Time

	switch pm.config.Granularity {
	case PartitionHourly:
		start, _ = time.Parse("2006-01-02-15", key)
		end = start.Add(time.Hour)
	case PartitionDaily:
		start, _ = time.Parse("2006-01-02", key)
		end = start.AddDate(0, 0, 1)
	case PartitionWeekly:
		var year, week int
		fmt.Sscanf(key, "%d-W%d", &year, &week)
		start = weekStart(year, week)
		end = start.AddDate(0, 0, 7)
	case PartitionMonthly:
		start, _ = time.Parse("2006-01", key)
		end = start.AddDate(0, 1, 0)
	default:
		start, _ = time.Parse("2006-01-02", key)
		end = start.AddDate(0, 0, 1)
	}

	return start, end
}

// weekStart returns the start of the given ISO week
func weekStart(year, week int) time.Time {
	// January 4th is always in week 1
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	// Days since Monday
	daysSinceMonday := int(jan4.Weekday()+6) % 7
	// Start of week 1
	week1Start := jan4.AddDate(0, 0, -daysSinceMonday)
	// Start of requested week
	return week1Start.AddDate(0, 0, (week-1)*7)
}

// =============================================================================
// Partition Operations
// =============================================================================

// AddEntry adds an entry to the appropriate partition
func (pm *TemporalPartitionManager) AddEntry(entryID string, timestamp time.Time, tokens int) error {
	key := pm.GeneratePartitionKey(timestamp)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	partition, ok := pm.partitions[key]
	if !ok {
		start, end := pm.GetPartitionBounds(key)
		partition = &TemporalPartition{
			Key:            key,
			StartTime:      start,
			EndTime:        end,
			EntryIDs:       make([]string, 0, 100),
			entrySet:       make(map[string]bool),
			LastAccessTime: time.Now(),
		}
		pm.partitions[key] = partition
		pm.insertSortedKey(key)
		pm.stats.TotalPartitions++
		pm.stats.ActivePartitions++
	}

	partition.mu.Lock()
	if !partition.entrySet[entryID] {
		partition.EntryIDs = append(partition.EntryIDs, entryID)
		partition.entrySet[entryID] = true
		partition.EntryCount++
		partition.TotalTokens += tokens
		pm.stats.TotalEntries++
	}
	partition.LastAccessTime = time.Now()
	partition.mu.Unlock()

	// Check if we need to evict old partitions
	if len(pm.partitions) > pm.config.MaxPartitionsInMemory {
		pm.evictOldestPartitionLocked()
	}

	return nil
}

// RemoveEntry removes an entry from its partition
func (pm *TemporalPartitionManager) RemoveEntry(entryID string, timestamp time.Time) {
	key := pm.GeneratePartitionKey(timestamp)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	partition, ok := pm.partitions[key]
	if !ok {
		return
	}

	partition.mu.Lock()
	if partition.entrySet[entryID] {
		delete(partition.entrySet, entryID)
		partition.EntryCount--
		pm.stats.TotalEntries--

		// Rebuild entry ID list
		newIDs := make([]string, 0, len(partition.EntryIDs)-1)
		for _, id := range partition.EntryIDs {
			if id != entryID {
				newIDs = append(newIDs, id)
			}
		}
		partition.EntryIDs = newIDs
	}
	partition.mu.Unlock()

	// Remove empty partitions
	if partition.EntryCount == 0 {
		delete(pm.partitions, key)
		pm.removeSortedKey(key)
		pm.stats.TotalPartitions--
		pm.stats.ActivePartitions--
	}
}

// =============================================================================
// Query Operations
// =============================================================================

// QueryTimeRange returns entry IDs within the given time range
func (pm *TemporalPartitionManager) QueryTimeRange(start, end time.Time) []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	pm.stats.QueriesExecuted++

	var results []string
	startKey := pm.GeneratePartitionKey(start)
	endKey := pm.GeneratePartitionKey(end)

	// Find partitions in range
	for _, key := range pm.sortedKeys {
		if key >= startKey && key <= endKey {
			partition := pm.partitions[key]
			if partition != nil {
				pm.stats.PartitionsScanned++
				partition.mu.RLock()
				results = append(results, partition.EntryIDs...)
				partition.LastAccessTime = time.Now()
				partition.mu.RUnlock()
			}
		}
	}

	return results
}

// GetPartition returns a specific partition by key
func (pm *TemporalPartitionManager) GetPartition(key string) *TemporalPartition {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	partition := pm.partitions[key]
	if partition != nil {
		pm.stats.CacheHits++
		partition.mu.Lock()
		partition.LastAccessTime = time.Now()
		partition.mu.Unlock()
	}
	return partition
}

// GetRecentPartitions returns the N most recent partitions
func (pm *TemporalPartitionManager) GetRecentPartitions(n int) []*TemporalPartition {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	count := n
	if count > len(pm.sortedKeys) {
		count = len(pm.sortedKeys)
	}

	results := make([]*TemporalPartition, 0, count)
	for i := len(pm.sortedKeys) - 1; i >= 0 && len(results) < count; i-- {
		key := pm.sortedKeys[i]
		if partition := pm.partitions[key]; partition != nil {
			results = append(results, partition)
		}
	}

	return results
}

// =============================================================================
// Compaction
// =============================================================================

// CompactPartition marks a partition as compacted
func (pm *TemporalPartitionManager) CompactPartition(key string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	partition, ok := pm.partitions[key]
	if !ok {
		return fmt.Errorf("partition not found: %s", key)
	}

	partition.mu.Lock()
	if !partition.IsCompacted {
		partition.IsCompacted = true
		now := time.Now()
		partition.CompactedAt = &now
		pm.stats.CompactedCount++
		pm.stats.ActivePartitions--
	}
	partition.mu.Unlock()

	return nil
}

// CompactOldPartitions compacts partitions older than the threshold
func (pm *TemporalPartitionManager) CompactOldPartitions() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	threshold := time.Now().Add(-pm.config.CompactThreshold)
	compacted := 0

	for _, partition := range pm.partitions {
		partition.mu.Lock()
		if !partition.IsCompacted && partition.EndTime.Before(threshold) {
			partition.IsCompacted = true
			now := time.Now()
			partition.CompactedAt = &now
			compacted++
			pm.stats.CompactedCount++
			pm.stats.ActivePartitions--
		}
		partition.mu.Unlock()

		// Don't compact too many at once
		if compacted >= 10 {
			break
		}
	}

	return compacted
}

// =============================================================================
// Maintenance
// =============================================================================

// insertSortedKey inserts a key maintaining sort order
func (pm *TemporalPartitionManager) insertSortedKey(key string) {
	idx := sort.SearchStrings(pm.sortedKeys, key)
	pm.sortedKeys = append(pm.sortedKeys, "")
	copy(pm.sortedKeys[idx+1:], pm.sortedKeys[idx:])
	pm.sortedKeys[idx] = key
}

// removeSortedKey removes a key from the sorted list
func (pm *TemporalPartitionManager) removeSortedKey(key string) {
	idx := sort.SearchStrings(pm.sortedKeys, key)
	if idx < len(pm.sortedKeys) && pm.sortedKeys[idx] == key {
		pm.sortedKeys = append(pm.sortedKeys[:idx], pm.sortedKeys[idx+1:]...)
	}
}

// evictOldestPartitionLocked evicts the oldest partition (must hold lock)
func (pm *TemporalPartitionManager) evictOldestPartitionLocked() {
	if len(pm.sortedKeys) == 0 {
		return
	}

	// Find oldest non-compacted partition
	var oldestKey string
	var oldestAccess time.Time

	for _, key := range pm.sortedKeys {
		partition := pm.partitions[key]
		if partition == nil {
			continue
		}

		partition.mu.RLock()
		access := partition.LastAccessTime
		partition.mu.RUnlock()

		if oldestKey == "" || access.Before(oldestAccess) {
			oldestKey = key
			oldestAccess = access
		}
	}

	if oldestKey != "" {
		partition := pm.partitions[oldestKey]
		if partition != nil {
			partition.mu.Lock()
			pm.stats.TotalEntries -= partition.EntryCount
			partition.mu.Unlock()
		}

		delete(pm.partitions, oldestKey)
		pm.removeSortedKey(oldestKey)
		pm.stats.TotalPartitions--
		pm.stats.ActivePartitions--
	}
}

// Stats returns partition manager statistics
func (pm *TemporalPartitionManager) Stats() TemporalPartitionStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.stats
}

// GetAllPartitionKeys returns all partition keys in sorted order
func (pm *TemporalPartitionManager) GetAllPartitionKeys() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]string, len(pm.sortedKeys))
	copy(result, pm.sortedKeys)
	return result
}

// PartitionCount returns the current number of partitions
func (pm *TemporalPartitionManager) PartitionCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.partitions)
}
