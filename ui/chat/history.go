package chat

import (
	"sync"
	"time"
)

// defaultCapacity is the ring buffer size when no capacity is provided.
// Derived from common terminal session lengths (10k messages covers
// multi-hour sessions without unbounded growth).
const defaultCapacity = 10_000

// ChatSource identifies the originator of a chat entry.
type ChatSource int

const (
	SourceUser   ChatSource = iota
	SourceAgent
	SourceSystem
	SourceTool
	SourceError
)

// CodeRegion describes a code block's position within a rendered entry.
// Line indices are relative to the entry's RenderedLines slice.
type CodeRegion struct {
	Start   int    // First rendered line (inclusive).
	End     int    // Last rendered line (exclusive).
	Content string // Raw code without fence markers.
}

// ChatEntry represents a single message in the chat history.
type ChatEntry struct {
	ID            string
	Timestamp     time.Time
	Source        ChatSource
	AgentType     string
	AgentID       string
	SessionID     string
	Content       string       // Raw content (markdown).
	RenderedLines []string     // Cached rendered output lines (lazily computed).
	CodeRegions   []CodeRegion // Cached code block positions (lazily computed).
	Height        int          // Cached line count (-1 means not yet computed).
	Streaming     bool         // True while still receiving chunks.
	Importance    float64

	// Thinking indicator state.
	ThinkingText    string        // Animated spinner + message (set by model on tick).
	ThinkingColor   string        // Hex color for gradient animation (e.g. "#89b4fa"); empty = use theme default.
	ThinkingElapsed time.Duration // Elapsed from stream start to first chunk (0 = no thinking recorded).
}

// History is a bounded ring buffer of chat entries.
// It prevents unbounded growth by overwriting the oldest entries when full.
// All methods are safe for concurrent use.
type History struct {
	mu       sync.RWMutex
	entries  []ChatEntry
	head     int // Next write position.
	count    int // Number of valid entries (<= capacity).
	capacity int
}

// NewHistory returns a History with the given maximum capacity.
// If capacity is zero or negative, defaultCapacity is used.
func NewHistory(capacity int) *History {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &History{
		entries:  make([]ChatEntry, capacity),
		capacity: capacity,
	}
}

// Push appends an entry to the ring buffer, overwriting the oldest if full.
func (h *History) Push(entry *ChatEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.entries[h.head] = *entry
	h.head = (h.head + 1) % h.capacity
	if h.count < h.capacity {
		h.count++
	}
}

// Len returns the number of valid entries.
func (h *History) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.count
}

// Get returns the entry at the given logical index (0 = oldest visible).
// Returns nil if index is out of range.
func (h *History) Get(index int) *ChatEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if index < 0 || index >= h.count {
		return nil
	}
	physical := h.logicalToPhysical(index)
	entry := h.entries[physical]
	return &entry
}

// Last returns the most recent entry, or nil if empty.
func (h *History) Last() *ChatEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.count == 0 {
		return nil
	}
	physical := h.logicalToPhysical(h.count - 1)
	entry := h.entries[physical]
	return &entry
}

// Range iterates entries from index start (inclusive) to end (exclusive),
// calling fn for each entry. Iteration stops if fn returns false.
func (h *History) Range(start, end int, fn func(index int, entry *ChatEntry) bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	start = max(start, 0)
	end = min(end, h.count)

	for i := start; i < end; i++ {
		physical := h.logicalToPhysical(i)
		entry := h.entries[physical]
		if !fn(i, &entry) {
			return
		}
	}
}

// InvalidateRender clears the cached rendered lines for the entry at the
// given logical index, forcing re-render on the next view pass.
func (h *History) InvalidateRender(index int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if index < 0 || index >= h.count {
		return
	}
	physical := h.logicalToPhysical(index)
	h.entries[physical].RenderedLines = nil
	h.entries[physical].CodeRegions = nil
	h.entries[physical].Height = -1
}

// Full reports whether the ring buffer is at capacity.
// The next Push will evict the oldest entry.
func (h *History) Full() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.count == h.capacity
}

// logicalToPhysical converts a logical index (0 = oldest) to a physical
// ring buffer index. Caller must hold at least a read lock.
func (h *History) logicalToPhysical(logical int) int {
	// When buffer is not full, oldest is at index 0.
	// When full, oldest is at head (the next to be overwritten).
	start := 0
	if h.count == h.capacity {
		start = h.head
	}
	return (start + logical) % h.capacity
}
