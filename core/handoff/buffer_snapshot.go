package handoff

import (
	"sync/atomic"
	"time"
)

// BufferSegment is an immutable chunk of buffer content.
// Once sealed, its contents are never modified.
type BufferSegment struct {
	Messages    []Message            `json:"messages"`
	Summary     string               `json:"summary"`
	ToolStates  map[string]ToolState `json:"tool_states,omitempty"`
	TokenCount  int                  `json:"token_count"`
	SealedAt    time.Time            `json:"sealed_at"`
	SequenceNum uint64               `json:"sequence_num"`
}

// COWSnapshot is an immutable snapshot of the buffer ring, published atomically.
// Readers obtain this via lock-free atomic.Pointer load — zero contention.
type COWSnapshot struct {
	Segments    []*BufferSegment `json:"segments"`
	TotalTokens int              `json:"total_tokens"`
	TokenBudget int              `json:"token_budget"`
	CreatedAt   time.Time        `json:"created_at"`
	SequenceNum uint64           `json:"sequence_num"`
}

// BufferRingStats contains diagnostic information about the ring buffer.
type BufferRingStats struct {
	SegmentCount  int    `json:"segment_count"`
	MaxSegments   int    `json:"max_segments"`
	TotalTokens   int    `json:"total_tokens"`
	SequenceNum   uint64 `json:"sequence_num"`
}

// BufferRing manages the segment ring with COW snapshot publication.
// All write methods (Append, TrimToBudget, Clear) are called only from
// the ParallelBuffer's processLoop goroutine — single writer, no locks
// on the write path.
type BufferRing struct {
	segments      []*BufferSegment
	maxSegments   int
	headIdx       int
	count         int
	sequenceNum   uint64
	currentTokens int

	// Lock-free snapshot for readers.
	snapshot atomic.Pointer[COWSnapshot]
}

// NewBufferRing creates a ring buffer. Segment capacity is derived from
// the context window:
//
//	segmentTokenTarget = contextWindow / 100
//	maxSegments = max(5, contextWindow / (segmentTokenTarget * 2))
func NewBufferRing(contextWindow int) *BufferRing {
	if contextWindow < 20_000 {
		contextWindow = 20_000
	}

	segTarget := contextWindow / 100
	maxSeg := contextWindow / (segTarget * 2)
	if maxSeg < 5 {
		maxSeg = 5
	}

	return &BufferRing{
		segments:    make([]*BufferSegment, maxSeg),
		maxSegments: maxSeg,
	}
}

// SegmentTokenTarget returns the derived segment token target for the
// given context window. Used by ParallelBuffer to know when to seal.
func SegmentTokenTarget(contextWindow int) int {
	if contextWindow < 20_000 {
		contextWindow = 20_000
	}
	return contextWindow / 100
}

// Append adds a sealed segment, evicting the oldest if at capacity or
// over the token budget. Called only from processLoop (single writer).
func (r *BufferRing) Append(seg *BufferSegment, tokenBudget int) {
	if seg == nil {
		return
	}

	r.sequenceNum++
	seg.SequenceNum = r.sequenceNum

	// Evict oldest segments while over budget or at capacity.
	for r.count > 0 && (r.count >= r.maxSegments || r.currentTokens+seg.TokenCount > tokenBudget) {
		r.evictOldest()
	}

	// Write into the ring at headIdx.
	r.segments[r.headIdx] = seg
	r.headIdx = (r.headIdx + 1) % r.maxSegments
	r.count++
	r.currentTokens += seg.TokenCount
}

// PublishSnapshot creates and atomically publishes a new COWSnapshot.
// Called only from processLoop after Append or TrimToBudget.
func (r *BufferRing) PublishSnapshot(tokenBudget int) {
	segs := r.orderedSegments()

	snap := &COWSnapshot{
		Segments:    segs,
		TotalTokens: r.currentTokens,
		TokenBudget: tokenBudget,
		CreatedAt:   time.Now(),
		SequenceNum: r.sequenceNum,
	}

	r.snapshot.Store(snap)
}

// Snapshot returns the latest published snapshot (lock-free read).
func (r *BufferRing) Snapshot() *COWSnapshot {
	return r.snapshot.Load()
}

// TrimToBudget evicts oldest segments until TotalTokens <= budget.
// Called only from processLoop when elastic sizer shrinks.
func (r *BufferRing) TrimToBudget(budget int) {
	for r.count > 0 && r.currentTokens > budget {
		r.evictOldest()
	}
}

// Clear resets the ring (called after handoff completes).
func (r *BufferRing) Clear() {
	for i := range r.segments {
		r.segments[i] = nil
	}
	r.headIdx = 0
	r.count = 0
	r.currentTokens = 0
	r.sequenceNum = 0
	r.snapshot.Store(nil)
}

// Stats returns current ring statistics.
func (r *BufferRing) Stats() BufferRingStats {
	return BufferRingStats{
		SegmentCount: r.count,
		MaxSegments:  r.maxSegments,
		TotalTokens:  r.currentTokens,
		SequenceNum:  r.sequenceNum,
	}
}

// evictOldest removes the oldest segment from the ring.
func (r *BufferRing) evictOldest() {
	// Oldest is at (headIdx - count) mod maxSegments.
	oldestIdx := (r.headIdx - r.count + r.maxSegments) % r.maxSegments
	evicted := r.segments[oldestIdx]
	r.segments[oldestIdx] = nil
	r.count--
	if evicted != nil {
		r.currentTokens -= evicted.TokenCount
	}
}

// orderedSegments returns a copy of the segments slice ordered oldest→newest.
func (r *BufferRing) orderedSegments() []*BufferSegment {
	if r.count == 0 {
		return nil
	}

	result := make([]*BufferSegment, 0, r.count)
	startIdx := (r.headIdx - r.count + r.maxSegments) % r.maxSegments

	for i := range r.count {
		idx := (startIdx + i) % r.maxSegments
		if r.segments[idx] != nil {
			result = append(result, r.segments[idx])
		}
	}

	return result
}
