package archivalist

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type PartitionGranularity string

const (
	PartitionHourly  PartitionGranularity = "hourly"
	PartitionDaily   PartitionGranularity = "daily"
	PartitionWeekly  PartitionGranularity = "weekly"
	PartitionMonthly PartitionGranularity = "monthly"
)

type TemporalPartitionManager struct {
	mu sync.RWMutex

	partitions map[string]*TemporalPartition
	sortedKeys []string
	config     TemporalPartitionConfig
	stats      TemporalPartitionStats
}

type TemporalPartitionConfig struct {
	Granularity            PartitionGranularity `json:"granularity"`
	MaxEntriesPerPartition int                  `json:"max_entries_per_partition"`
	MaxPartitionsInMemory  int                  `json:"max_partitions_in_memory"`
	AutoCompact            bool                 `json:"auto_compact"`
	CompactThreshold       time.Duration        `json:"compact_threshold"`
}

func DefaultTemporalPartitionConfig() TemporalPartitionConfig {
	return TemporalPartitionConfig{
		Granularity:            PartitionDaily,
		MaxEntriesPerPartition: 10000,
		MaxPartitionsInMemory:  30,
		AutoCompact:            true,
		CompactThreshold:       7 * 24 * time.Hour,
	}
}

type TemporalPartition struct {
	mu sync.RWMutex

	Key       string    `json:"key"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`

	EntryIDs  []string `json:"entry_ids"`
	entrySet  map[string]bool
	entryTime map[string]time.Time

	EntryCount     int        `json:"entry_count"`
	TotalTokens    int        `json:"total_tokens"`
	IsCompacted    bool       `json:"is_compacted"`
	CompactedAt    *time.Time `json:"compacted_at,omitempty"`
	LastAccessTime time.Time  `json:"last_access_time"`
}

type TemporalPartitionStats struct {
	TotalPartitions   int   `json:"total_partitions"`
	TotalEntries      int   `json:"total_entries"`
	CompactedCount    int   `json:"compacted_count"`
	ActivePartitions  int   `json:"active_partitions"`
	QueriesExecuted   int64 `json:"queries_executed"`
	PartitionsScanned int64 `json:"partitions_scanned"`
	CacheHits         int64 `json:"cache_hits"`
}

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

func weekStart(year, week int) time.Time {
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	daysSinceMonday := int(jan4.Weekday()+6) % 7
	week1Start := jan4.AddDate(0, 0, -daysSinceMonday)
	return week1Start.AddDate(0, 0, (week-1)*7)
}

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
			entryTime:      make(map[string]time.Time),
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
		partition.entryTime[entryID] = timestamp
		partition.EntryCount++
		partition.TotalTokens += tokens
		pm.stats.TotalEntries++
	}
	partition.LastAccessTime = time.Now()
	partition.mu.Unlock()

	if len(pm.partitions) > pm.config.MaxPartitionsInMemory {
		pm.evictOldestPartitionLocked()
	}

	return nil
}

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
		delete(partition.entryTime, entryID)
		partition.EntryCount--
		pm.stats.TotalEntries--

		newIDs := make([]string, 0, len(partition.EntryIDs)-1)
		for _, id := range partition.EntryIDs {
			if id != entryID {
				newIDs = append(newIDs, id)
			}
		}
		partition.EntryIDs = newIDs
	}
	partition.mu.Unlock()

	if partition.EntryCount == 0 {
		delete(pm.partitions, key)
		pm.removeSortedKey(key)
		pm.stats.TotalPartitions--
		pm.stats.ActivePartitions--
	}
}

func (pm *TemporalPartitionManager) QueryTimeRange(start, end time.Time) []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	pm.stats.QueriesExecuted++

	var results []string
	startKey := pm.GeneratePartitionKey(start)
	endKey := pm.GeneratePartitionKey(end)

	for _, key := range pm.sortedKeys {
		if key >= startKey && key <= endKey {
			partition := pm.partitions[key]
			if partition != nil {
				pm.stats.PartitionsScanned++
				partition.mu.RLock()
				for _, id := range partition.EntryIDs {
					entryTime, ok := partition.entryTime[id]
					if !ok {
						entryTime = partition.StartTime
					}
					if !entryTime.Before(start) && !entryTime.After(end) {
						results = append(results, id)
					}
				}
				partition.LastAccessTime = time.Now()
				partition.mu.RUnlock()
			}
		}
	}

	return results
}

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

func (pm *TemporalPartitionManager) CompactOldPartitions() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	threshold := time.Now().Add(-pm.config.CompactThreshold)
	compacted := 0

	for _, partition := range pm.partitions {
		partition.mu.Lock()
		if !partition.IsCompacted && !partition.LastAccessTime.After(threshold) {
			partition.IsCompacted = true
			now := time.Now()
			partition.CompactedAt = &now
			compacted++
			pm.stats.CompactedCount++
			pm.stats.ActivePartitions--
		}
		partition.mu.Unlock()

		if compacted >= 10 {
			break
		}
	}

	return compacted
}

func (pm *TemporalPartitionManager) insertSortedKey(key string) {
	idx := sort.SearchStrings(pm.sortedKeys, key)
	pm.sortedKeys = append(pm.sortedKeys, "")
	copy(pm.sortedKeys[idx+1:], pm.sortedKeys[idx:])
	pm.sortedKeys[idx] = key
}

func (pm *TemporalPartitionManager) removeSortedKey(key string) {
	idx := sort.SearchStrings(pm.sortedKeys, key)
	if idx < len(pm.sortedKeys) && pm.sortedKeys[idx] == key {
		pm.sortedKeys = append(pm.sortedKeys[:idx], pm.sortedKeys[idx+1:]...)
	}
}

func (pm *TemporalPartitionManager) evictOldestPartitionLocked() {
	if len(pm.sortedKeys) == 0 {
		return
	}

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

func (pm *TemporalPartitionManager) Stats() TemporalPartitionStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.stats
}

func (pm *TemporalPartitionManager) GetAllPartitionKeys() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]string, len(pm.sortedKeys))
	copy(result, pm.sortedKeys)
	return result
}

func (pm *TemporalPartitionManager) PartitionCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.partitions)
}
