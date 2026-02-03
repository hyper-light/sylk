package sylkdir

import (
	"container/heap"
	"context"
	"math"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

// Segment represents a contiguous text region between structural boundaries.
// RightStrength is the boundary strength at the segment's right edge
// (separating it from the next segment). Last segment has RightStrength 0.
type Segment struct {
	Start         uint32
	End           uint32
	RightStrength uint32
}

// SplitAtBoundaries splits content at all tagged boundary offsets.
// Discards whitespace-only segments. Carries boundary strength to segments.
func SplitAtBoundaries(content []byte, boundaries []TaggedBoundary) []Segment {
	if len(boundaries) == 0 {
		seg := trimWhitespaceSegment(content, 0, uint32(len(content)))
		if seg.Start >= seg.End {
			return nil
		}
		return []Segment{{Start: 0, End: uint32(len(content)), RightStrength: 0}}
	}

	var segments []Segment
	pos := uint32(0)

	for _, b := range boundaries {
		if b.Offset <= pos || b.Offset > uint32(len(content)) {
			continue
		}
		seg := trimWhitespaceSegment(content, pos, b.Offset)
		if seg.Start < seg.End {
			segments = append(segments, Segment{
				Start:         pos,
				End:           b.Offset,
				RightStrength: b.Strength, // Strength of the boundary at this segment's right edge.
			})
		}
		pos = b.Offset
	}

	// Last segment from final boundary to end of content.
	if pos < uint32(len(content)) {
		seg := trimWhitespaceSegment(content, pos, uint32(len(content)))
		if seg.Start < seg.End {
			segments = append(segments, Segment{
				Start:         pos,
				End:           uint32(len(content)),
				RightStrength: 0,
			})
		}
	}

	return segments
}

// trimWhitespaceSegment checks if segment [start, end) is whitespace-only.
func trimWhitespaceSegment(content []byte, start, end uint32) Segment {
	for i := start; i < end; i++ {
		if content[i] != ' ' && content[i] != '\t' && content[i] != '\n' && content[i] != '\r' {
			return Segment{Start: start, End: end}
		}
	}
	return Segment{Start: end, End: end} // Empty: signal to discard.
}

// SegmentMerger performs similarity-ordered agglomerative merging of segments.
type SegmentMerger struct {
	similarity    embedder.Embedder
	ceiling       uint32
	baseThreshold float64 // 2/sqrt(dimension) — statistical significance.
}

// NewSegmentMerger creates a merger using the given similarity embedder and byte ceiling.
func NewSegmentMerger(sim embedder.Embedder, ceiling uint32) *SegmentMerger {
	dim := float64(sim.Dimension())
	return &SegmentMerger{
		similarity:    sim,
		ceiling:       ceiling,
		baseThreshold: 2.0 / math.Sqrt(dim),
	}
}

// MergeResult holds the output of an agglomerative merge: the surviving segments
// and their corresponding embeddings. Embeddings are parallel to Segments —
// result.Embeddings[i] is the embedding for result.Segments[i].
type MergeResult struct {
	Segments   []Segment
	Embeddings [][]float32
}

// Merge performs similarity-ordered agglomerative merge on segments.
// Returns the merged segments with their final embeddings, enabling callers
// to reuse embeddings downstream (e.g., for vector storage) without re-computation.
func (m *SegmentMerger) Merge(ctx context.Context, content []byte, segments []Segment) MergeResult {
	if len(segments) <= 1 {
		return m.embedSingleResult(ctx, content, segments)
	}
	totalSize := segments[len(segments)-1].End - segments[0].Start
	if totalSize <= m.ceiling {
		merged := []Segment{{
			Start:         segments[0].Start,
			End:           segments[len(segments)-1].End,
			RightStrength: segments[len(segments)-1].RightStrength,
		}}
		return m.embedSingleResult(ctx, content, merged)
	}

	state := m.initMergeState(ctx, content, segments)
	if state == nil {
		return MergeResult{Segments: segments}
	}
	return m.runMerge(ctx, state)
}

// MergeWithEmbeddings performs agglomerative merge using pre-computed embeddings.
// embeddings[i] corresponds to segments[i]. Avoids any EmbedBatch calls.
func (m *SegmentMerger) MergeWithEmbeddings(ctx context.Context, content []byte, segments []Segment, embeddings [][]float32) MergeResult {
	if len(segments) <= 1 {
		return MergeResult{Segments: segments, Embeddings: embeddings}
	}
	totalSize := segments[len(segments)-1].End - segments[0].Start
	if totalSize <= m.ceiling {
		merged := []Segment{{
			Start:         segments[0].Start,
			End:           segments[len(segments)-1].End,
			RightStrength: segments[len(segments)-1].RightStrength,
		}}
		emb := weightedAvgAllSegments(embeddings, segments)
		return MergeResult{Segments: merged, Embeddings: [][]float32{emb}}
	}
	state := m.initMergeStateFromEmbeddings(segments, embeddings)
	return m.runMerge(ctx, state)
}

// weightedAvgAllSegments computes a size-weighted average of all embeddings, L2-normalized.
func weightedAvgAllSegments(embeddings [][]float32, segments []Segment) []float32 {
	dim := len(embeddings[0])
	result := make([]float32, dim)
	var totalSize float64
	for _, seg := range segments {
		totalSize += float64(seg.End - seg.Start)
	}
	for i, emb := range embeddings {
		w := float64(segments[i].End-segments[i].Start) / totalSize
		for j, v := range emb {
			result[j] += float32(w * float64(v))
		}
	}
	var sumSq float64
	for _, v := range result {
		sumSq += float64(v) * float64(v)
	}
	if sumSq > 0 {
		invNorm := float32(1.0 / math.Sqrt(sumSq))
		for i := range result {
			result[i] *= invNorm
		}
	}
	return result
}

// embedSingleResult embeds a trivial result (0 or 1 segments) and returns it.
func (m *SegmentMerger) embedSingleResult(ctx context.Context, content []byte, segments []Segment) MergeResult {
	if len(segments) == 0 {
		return MergeResult{}
	}
	texts := make([]string, len(segments))
	for i, seg := range segments {
		texts[i] = string(content[seg.Start:seg.End])
	}
	embs, err := m.similarity.EmbedBatch(ctx, texts)
	if err != nil {
		return MergeResult{Segments: segments}
	}
	return MergeResult{Segments: segments, Embeddings: embs}
}

// mergeState holds the linked list + heap state for the merge algorithm.
type mergeState struct {
	segments   []Segment
	embeddings [][]float32
	active     []bool
	next       []int
	prev       []int
	h          pairHeap
	threshold  float64
	scaleFactor float64
}

// initMergeState embeds all segments then builds merge state.
func (m *SegmentMerger) initMergeState(ctx context.Context, content []byte, segments []Segment) *mergeState {
	texts := make([]string, len(segments))
	for i, seg := range segments {
		texts[i] = string(content[seg.Start:seg.End])
	}

	embeddings, err := m.similarity.EmbedBatch(ctx, texts)
	if err != nil {
		return nil
	}
	return m.buildMergeState(segments, embeddings)
}

// initMergeStateFromEmbeddings builds merge state from pre-computed embeddings.
// Caller must ensure no concurrent access to the embedding sub-slices —
// merge mutates them in-place via weightedAvgNorm during segment merging.
func (m *SegmentMerger) initMergeStateFromEmbeddings(segments []Segment, embeddings [][]float32) *mergeState {
	return m.buildMergeState(segments, embeddings)
}

// buildMergeState constructs the linked list + heap from segments and embeddings.
func (m *SegmentMerger) buildMergeState(segments []Segment, embeddings [][]float32) *mergeState {
	n := len(segments)
	state := &mergeState{
		segments:   make([]Segment, n),
		embeddings: embeddings,
		active:     make([]bool, n),
		next:       make([]int, n),
		prev:       make([]int, n),
	}
	copy(state.segments, segments)

	for i := range n {
		state.active[i] = true
		state.next[i] = i + 1
		state.prev[i] = i - 1
	}
	state.next[n-1] = -1
	state.prev[0] = -1

	// Compute initial adjacent similarities for scaleFactor.
	sims := make([]float64, 0, n-1)
	for i := range n - 1 {
		sim := dotSimilarity(embeddings[i], embeddings[i+1])
		sims = append(sims, sim)
	}

	state.scaleFactor = stddev(sims)
	state.threshold = m.baseThreshold

	// Build max-heap.
	state.h = make(pairHeap, 0, n-1)
	for i := range n - 1 {
		heap.Push(&state.h, mergePair{
			similarity: sims[i],
			left:       i,
			right:      i + 1,
		})
	}

	return state
}

// runMerge pops the heap and merges pairs until no more valid merges exist.
func (m *SegmentMerger) runMerge(ctx context.Context, state *mergeState) MergeResult {
	for state.h.Len() > 0 {
		if ctx.Err() != nil {
			break
		}
		pair := heap.Pop(&state.h).(mergePair)
		m.processPair(state, pair)
	}
	return m.collectActive(state)
}

// processPair evaluates and potentially executes a single merge.
func (m *SegmentMerger) processPair(state *mergeState, pair mergePair) {
	if !isValidPair(state, pair) {
		return
	}
	strength := float64(state.segments[pair.left].RightStrength)
	required := state.threshold + strength*state.scaleFactor
	if pair.similarity <= required {
		return
	}
	mergedSize := state.segments[pair.right].End - state.segments[pair.left].Start
	if mergedSize > m.ceiling {
		return
	}
	m.executeMerge(state, pair)
}

// isValidPair checks that both segments are still active and adjacent.
func isValidPair(state *mergeState, pair mergePair) bool {
	if !state.active[pair.left] || !state.active[pair.right] {
		return false
	}
	return state.next[pair.left] == pair.right
}

// executeMerge extends the left segment to absorb the right, relinks the list,
// computes a size-weighted average embedding, and pushes new neighbor pairs.
func (m *SegmentMerger) executeMerge(state *mergeState, pair mergePair) {
	left, right := pair.left, pair.right

	// Capture sizes before extending left (needed for weighted average).
	sizeL := state.segments[left].End - state.segments[left].Start
	sizeR := state.segments[right].End - state.segments[right].Start

	// Extend left segment to absorb right.
	state.segments[left].End = state.segments[right].End
	state.segments[left].RightStrength = state.segments[right].RightStrength

	// Deactivate right.
	state.active[right] = false

	// Relink.
	state.next[left] = state.next[right]
	if state.next[right] >= 0 {
		state.prev[state.next[right]] = left
	}

	// Approximate merged embedding via size-weighted average + L2 renormalize.
	// Exact embeddings are computed after the merge loop in reembedResult.
	weightedAvgNorm(state.embeddings[left], state.embeddings[right], sizeL, sizeR)

	m.pushNeighborPairs(state, left)
}

// pushNeighborPairs pushes new heap entries for the merged segment's neighbors.
func (m *SegmentMerger) pushNeighborPairs(state *mergeState, idx int) {
	if state.prev[idx] >= 0 {
		sim := dotSimilarity(state.embeddings[state.prev[idx]], state.embeddings[idx])
		heap.Push(&state.h, mergePair{
			similarity: sim,
			left:       state.prev[idx],
			right:      idx,
		})
	}
	if state.next[idx] >= 0 {
		sim := dotSimilarity(state.embeddings[idx], state.embeddings[state.next[idx]])
		heap.Push(&state.h, mergePair{
			similarity: sim,
			left:       idx,
			right:      state.next[idx],
		})
	}
}

// collectActive returns the remaining active segments with their embeddings.
func (m *SegmentMerger) collectActive(state *mergeState) MergeResult {
	var segs []Segment
	var embs [][]float32
	for i, active := range state.active {
		if active {
			segs = append(segs, state.segments[i])
			embs = append(embs, state.embeddings[i])
		}
	}
	return MergeResult{Segments: segs, Embeddings: embs}
}

// dotSimilarity computes dot product of two L2-normalized vectors (= cosine similarity).
func dotSimilarity(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// weightedAvgNorm computes a size-weighted average of dst and src in-place on dst,
// then L2-normalizes. Used for approximate merge-decision embeddings.
func weightedAvgNorm(dst, src []float32, sizeA, sizeB uint32) {
	total := float64(sizeA + sizeB)
	wA := float64(sizeA) / total
	wB := float64(sizeB) / total
	var sumSq float64
	for i := range dst {
		v := wA*float64(dst[i]) + wB*float64(src[i])
		dst[i] = float32(v)
		sumSq += v * v
	}
	if sumSq == 0 {
		return
	}
	invNorm := float32(1.0 / math.Sqrt(sumSq))
	for i := range dst {
		dst[i] *= invNorm
	}
}

// stddev computes the sample standard deviation of a float64 slice.
func stddev(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))

	var sumSq float64
	for _, v := range vals {
		d := v - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(vals)))
}

// ---------------------------------------------------------------------------
// Max-heap of merge candidate pairs, ordered by similarity (descending).
// ---------------------------------------------------------------------------

type mergePair struct {
	similarity float64
	left       int
	right      int
}

type pairHeap []mergePair

func (h pairHeap) Len() int            { return len(h) }
func (h pairHeap) Less(i, j int) bool  { return h[i].similarity > h[j].similarity }
func (h pairHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *pairHeap) Push(x any)         { *h = append(*h, x.(mergePair)) }
func (h *pairHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
