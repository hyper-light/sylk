package cache

import (
	"testing"
	"time"
)

func TestNewCacheStats(t *testing.T) {
	stats := NewCacheStats()

	if stats == nil {
		t.Fatal("NewCacheStats returned nil")
	}
	if stats.Hits() != 0 {
		t.Error("Initial hits should be 0")
	}
	if stats.Misses() != 0 {
		t.Error("Initial misses should be 0")
	}
	if stats.Sets() != 0 {
		t.Error("Initial sets should be 0")
	}
	if stats.Evictions() != 0 {
		t.Error("Initial evictions should be 0")
	}
}

func TestCacheStats_RecordHit(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordHit()
	stats.RecordHit()
	stats.RecordHit()

	if stats.Hits() != 3 {
		t.Errorf("Hits() = %d, want 3", stats.Hits())
	}
}

func TestCacheStats_RecordMiss(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordMiss()
	stats.RecordMiss()

	if stats.Misses() != 2 {
		t.Errorf("Misses() = %d, want 2", stats.Misses())
	}
}

func TestCacheStats_RecordSet(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordSet()

	if stats.Sets() != 1 {
		t.Errorf("Sets() = %d, want 1", stats.Sets())
	}
}

func TestCacheStats_RecordEviction(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordEviction()
	stats.RecordEviction()

	if stats.Evictions() != 2 {
		t.Errorf("Evictions() = %d, want 2", stats.Evictions())
	}
}

func TestCacheStats_Total(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordHit()
	stats.RecordHit()
	stats.RecordMiss()

	if stats.Total() != 3 {
		t.Errorf("Total() = %d, want 3", stats.Total())
	}
}

func TestCacheStats_HitRate(t *testing.T) {
	stats := NewCacheStats()

	// Empty stats
	if stats.HitRate() != 0 {
		t.Error("HitRate should be 0 when empty")
	}

	// 2 hits, 2 misses = 50%
	stats.RecordHit()
	stats.RecordHit()
	stats.RecordMiss()
	stats.RecordMiss()

	rate := stats.HitRate()
	if rate != 0.5 {
		t.Errorf("HitRate() = %f, want 0.5", rate)
	}
}

func TestCacheStats_HitRate_AllHits(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordHit()
	stats.RecordHit()
	stats.RecordHit()

	if stats.HitRate() != 1.0 {
		t.Errorf("HitRate() = %f, want 1.0", stats.HitRate())
	}
}

func TestCacheStats_HitRate_AllMisses(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordMiss()
	stats.RecordMiss()

	if stats.HitRate() != 0.0 {
		t.Errorf("HitRate() = %f, want 0.0", stats.HitRate())
	}
}

func TestCacheStats_Uptime(t *testing.T) {
	stats := NewCacheStats()

	time.Sleep(10 * time.Millisecond)

	uptime := stats.Uptime()
	if uptime < 10*time.Millisecond {
		t.Errorf("Uptime = %v, should be >= 10ms", uptime)
	}
}

func TestCacheStats_Reset(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordHit()
	stats.RecordMiss()
	stats.RecordSet()
	stats.RecordEviction()

	stats.Reset()

	if stats.Hits() != 0 {
		t.Error("Hits should be 0 after reset")
	}
	if stats.Misses() != 0 {
		t.Error("Misses should be 0 after reset")
	}
	if stats.Sets() != 0 {
		t.Error("Sets should be 0 after reset")
	}
	if stats.Evictions() != 0 {
		t.Error("Evictions should be 0 after reset")
	}
}

func TestCacheStats_Snapshot(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordHit()
	stats.RecordMiss()

	snapshot := stats.Snapshot()

	// Modify original
	stats.RecordHit()

	// Snapshot should not change
	if snapshot.Hits() != 1 {
		t.Error("Snapshot should not be affected by changes to original")
	}
	if stats.Hits() != 2 {
		t.Error("Original should be updated")
	}
}

func TestCacheStats_ToSnapshot(t *testing.T) {
	stats := NewCacheStats()

	stats.RecordHit()
	stats.RecordHit()
	stats.RecordMiss()
	stats.RecordSet()

	snapshot := stats.ToSnapshot()

	if snapshot.Hits != 2 {
		t.Errorf("Hits = %d, want 2", snapshot.Hits)
	}
	if snapshot.Misses != 1 {
		t.Errorf("Misses = %d, want 1", snapshot.Misses)
	}
	if snapshot.Sets != 1 {
		t.Errorf("Sets = %d, want 1", snapshot.Sets)
	}
	if snapshot.Total != 3 {
		t.Errorf("Total = %d, want 3", snapshot.Total)
	}

	expectedRate := 2.0 / 3.0
	if snapshot.HitRate < expectedRate-0.01 || snapshot.HitRate > expectedRate+0.01 {
		t.Errorf("HitRate = %f, want ~%f", snapshot.HitRate, expectedRate)
	}

	if snapshot.UptimeStr == "" {
		t.Error("UptimeStr should not be empty")
	}
}

func TestCacheStats_ConcurrentAccess(t *testing.T) {
	stats := NewCacheStats()

	done := make(chan struct{})

	// Concurrent writers
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				stats.RecordHit()
				stats.RecordMiss()
				stats.RecordSet()
				stats.RecordEviction()
			}
			done <- struct{}{}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Check totals
	if stats.Hits() != 1000 {
		t.Errorf("Hits = %d, want 1000", stats.Hits())
	}
	if stats.Misses() != 1000 {
		t.Errorf("Misses = %d, want 1000", stats.Misses())
	}
}
