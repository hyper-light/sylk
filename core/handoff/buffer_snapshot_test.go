package handoff

import (
	"sync"
	"testing"
	"time"
)

func TestBufferRing_AppendAndSnapshot(t *testing.T) {
	ring := NewBufferRing(200_000)

	seg := &BufferSegment{
		Messages:   []Message{{Role: "turn", Content: "hello", TokenCount: 100}},
		TokenCount: 100,
		SealedAt:   time.Now(),
	}

	ring.Append(seg, 20_000)
	ring.PublishSnapshot(20_000)

	snap := ring.Snapshot()
	if snap == nil {
		t.Fatal("snapshot should not be nil after Append+Publish")
	}
	if len(snap.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(snap.Segments))
	}
	if snap.TotalTokens != 100 {
		t.Errorf("total tokens: got %d, want 100", snap.TotalTokens)
	}
	if snap.SequenceNum != 1 {
		t.Errorf("sequence: got %d, want 1", snap.SequenceNum)
	}
}

func TestBufferRing_EvictsOnBudget(t *testing.T) {
	ring := NewBufferRing(200_000)
	budget := 500

	// Fill with 5 segments of 100 tokens each (total 500, at budget).
	for range 5 {
		seg := &BufferSegment{
			Messages:   []Message{{Role: "turn", TokenCount: 100}},
			TokenCount: 100,
			SealedAt:   time.Now(),
		}
		ring.Append(seg, budget)
	}

	stats := ring.Stats()
	if stats.TotalTokens > budget {
		t.Errorf("total tokens %d exceeds budget %d", stats.TotalTokens, budget)
	}

	// Add one more — should evict oldest.
	seg := &BufferSegment{
		Messages:   []Message{{Role: "turn", TokenCount: 100}},
		TokenCount: 100,
		SealedAt:   time.Now(),
	}
	ring.Append(seg, budget)

	stats = ring.Stats()
	if stats.TotalTokens > budget {
		t.Errorf("total tokens %d exceeds budget %d after eviction", stats.TotalTokens, budget)
	}
}

func TestBufferRing_EvictsOnCapacity(t *testing.T) {
	// Small context window → small maxSegments.
	ring := NewBufferRing(20_000)

	maxSeg := ring.maxSegments
	budget := 1_000_000 // Very high budget — only capacity matters.

	for range maxSeg + 5 {
		seg := &BufferSegment{
			Messages:   []Message{{Role: "turn", TokenCount: 10}},
			TokenCount: 10,
			SealedAt:   time.Now(),
		}
		ring.Append(seg, budget)
	}

	if ring.count > maxSeg {
		t.Errorf("count %d exceeds maxSegments %d", ring.count, maxSeg)
	}
}

func TestBufferRing_TrimToBudget(t *testing.T) {
	ring := NewBufferRing(200_000)

	for range 10 {
		seg := &BufferSegment{
			Messages:   []Message{{Role: "turn", TokenCount: 100}},
			TokenCount: 100,
			SealedAt:   time.Now(),
		}
		ring.Append(seg, 10_000) // Large budget.
	}

	if ring.currentTokens != 1000 {
		t.Fatalf("expected 1000 tokens, got %d", ring.currentTokens)
	}

	ring.TrimToBudget(500)
	if ring.currentTokens > 500 {
		t.Errorf("after trim: got %d tokens, want <= 500", ring.currentTokens)
	}
}

func TestBufferRing_SegmentOrdering(t *testing.T) {
	ring := NewBufferRing(200_000)

	for i := range 5 {
		seg := &BufferSegment{
			Messages:   []Message{{Role: "turn", Content: string(rune('A' + i)), TokenCount: 100}},
			TokenCount: 100,
			SealedAt:   time.Now(),
		}
		ring.Append(seg, 20_000)
	}

	ring.PublishSnapshot(20_000)
	snap := ring.Snapshot()

	if len(snap.Segments) != 5 {
		t.Fatalf("expected 5 segments, got %d", len(snap.Segments))
	}

	// Segments should be oldest→newest with increasing sequence numbers.
	for i := 1; i < len(snap.Segments); i++ {
		if snap.Segments[i].SequenceNum <= snap.Segments[i-1].SequenceNum {
			t.Errorf("segments not ordered: seq[%d]=%d <= seq[%d]=%d",
				i, snap.Segments[i].SequenceNum, i-1, snap.Segments[i-1].SequenceNum)
		}
	}
}

func TestBufferRing_Clear(t *testing.T) {
	ring := NewBufferRing(200_000)

	seg := &BufferSegment{
		Messages:   []Message{{Role: "turn", TokenCount: 100}},
		TokenCount: 100,
		SealedAt:   time.Now(),
	}
	ring.Append(seg, 20_000)
	ring.PublishSnapshot(20_000)

	ring.Clear()

	if ring.count != 0 {
		t.Errorf("count after Clear: got %d", ring.count)
	}
	if ring.currentTokens != 0 {
		t.Errorf("tokens after Clear: got %d", ring.currentTokens)
	}
	if snap := ring.Snapshot(); snap != nil {
		t.Error("snapshot should be nil after Clear")
	}
}

func TestBufferRing_ConcurrentSnapshotRead(t *testing.T) {
	ring := NewBufferRing(200_000)

	// Writer goroutine.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 100 {
			seg := &BufferSegment{
				Messages:   []Message{{Role: "turn", TokenCount: 10}},
				TokenCount: 10,
				SealedAt:   time.Now(),
				SequenceNum: uint64(i),
			}
			ring.Append(seg, 20_000)
			ring.PublishSnapshot(20_000)
		}
	}()

	// Reader goroutine — concurrent snapshot reads.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			snap := ring.Snapshot()
			if snap != nil {
				// Just access fields to trigger race detector.
				_ = snap.TotalTokens
				_ = len(snap.Segments)
			}
		}
	}()

	wg.Wait()
}

func TestSegmentTokenTarget(t *testing.T) {
	tests := []struct {
		contextWindow int
		want          int
	}{
		{200_000, 2_000},
		{1_000_000, 10_000},
		{20_000, 200},
		{10_000, 200}, // Clamped to min 20_000.
	}

	for _, tt := range tests {
		got := SegmentTokenTarget(tt.contextWindow)
		if got != tt.want {
			t.Errorf("SegmentTokenTarget(%d): got %d, want %d", tt.contextWindow, got, tt.want)
		}
	}
}
