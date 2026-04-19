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
	SourceUser ChatSource = iota
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

// ToolCallRecord tracks a single tool invocation for inline display.
type ToolCallRecord struct {
	ToolCallKey         string
	ToolName            string
	ArgsSummary         string // Compact one-liner (from SummarizeToolArgs).
	FullArgs            string // Pretty-printed JSON (for expanded view).
	Output              string // Truncated tool output (max 512 chars).
	ErrorMsg            string // Error message on failure.
	StartedAt           time.Time
	Duration            time.Duration
	Success             bool
	Completed           bool
	SyntheticCompletion bool
	Expanded            bool // Toggle state for expand/collapse.

	// InterAgent renders a compact tree row for consultations, challenges,
	// responses, and validations instead of the generic tool-call block.
	InterAgent *InterAgentTool

	// OrphanedAtRender is a transient flag the renderer sets on a per-frame
	// copy when the row has been pending for more than the orphan threshold
	// AND the agent has emitted enough subsequent tool events to suggest the
	// agent has moved on without seeing this call's Complete. The duration
	// formatter swaps the live spinner for a `?` glyph so the user is not
	// misled into thinking the tool is still genuinely running. This does
	// NOT mutate the persisted row — `renderToolCalls` makes a local copy
	// before stuffing the flag, so toggling is purely cosmetic.
	OrphanedAtRender bool
}

type ToolCallSubregionKind string

const (
	ToolCallSubregionOverflow  ToolCallSubregionKind = "overflow"
	ToolCallSubregionChildTool ToolCallSubregionKind = "child_tool"
)

type ToolCallSubregion struct {
	Start            int
	End              int
	Kind             ToolCallSubregionKind
	ChildIndex       int
	ChildToolCallIdx int
	ChildPath        []int
	InterAgentPath   []int
}

type InterAgentToolKind string

const (
	InterAgentToolConsult   InterAgentToolKind = "consult"
	InterAgentToolChallenge InterAgentToolKind = "challenge"
	InterAgentToolApproval  InterAgentToolKind = "approval"
	InterAgentToolStore     InterAgentToolKind = "store"
)

type InterAgentToolStatus string

const (
	InterAgentToolPending InterAgentToolStatus = "pending"
	InterAgentToolDone    InterAgentToolStatus = "done"
	InterAgentToolFailed  InterAgentToolStatus = "failed"
)

// InterAgentChildActivity captures nested child-agent activity that should
// render beneath an inter-agent consult/challenge/approval branch instead of as a
// top-level chat entry.
type InterAgentChildActivity struct {
	CorrelationID     string
	AgentID           string
	AgentType         string
	ThinkingStartedAt time.Time
	ThinkingText      string
	ThinkingStatus    string
	ThinkingColor     string
	ToolCalls         []ToolCallRecord
	ToolCallsExpanded bool
	ResultSummary     string
	Completed         bool
	Failed            bool
}

// InterAgentTool describes a single-line inter-agent exchange shown beneath
// the active thinking block. AgentTypes are rendered in their normal colors.
// Children capture the nested work performed by the consulted/challenged
// agent so that the parent agent remains the sole top-level chat entry.
//
// RepeatCount tracks consecutive same-template emissions that the protocol
// allowed through (e.g., audit-cycle repeats where targets + summary are
// byte-identical to the prior row). RepeatCount==0 or 1 renders as a single
// row; values ≥2 suffix the headline with "×N" so the pattern is visible
// without spawning duplicate rows.
type InterAgentTool struct {
	Kind        InterAgentToolKind
	ThreadKey   string
	AgentTypes  []string
	Summary     string
	Status      InterAgentToolStatus
	Children    []InterAgentChildActivity
	RepeatCount int
}

// ToolCallRegion describes a tool call block's position within rendered lines.
type ToolCallRegion struct {
	Start      int // First rendered line (inclusive).
	End        int // Last rendered line (exclusive).
	RecordIdx  int // Index into ChatEntry.ToolCalls.
	Subregions []ToolCallSubregion
}

// ChatEntry represents a single message in the chat history.
type ChatEntry struct {
	ID        string
	Timestamp time.Time
	// CorrelationID is the entry's primary stream identity, assigned at first
	// stream. AdditionalCorrelationIDs holds CIDs of any subsequent streams
	// that took over this entry (e.g., a top-level-transfer continuation
	// after an inter-agent excursion). Lookups by either field resolve to
	// this entry — necessary because tool-call Phase=1 events can arrive
	// under the OLD CID after the entry has been resumed under a NEW CID;
	// without multi-CID lookup those events strand and their spinners never
	// clear.
	CorrelationID            string
	AdditionalCorrelationIDs []string
	Source                   ChatSource
	AgentType                string
	AgentID                  string
	TaskID                   string
	TaskName                 string
	TaskSlug                 string
	SessionID                string
	Content       string       // Raw content (markdown).
	RenderedLines []string     // Cached rendered output lines (lazily computed).
	CodeRegions   []CodeRegion // Cached code block positions (lazily computed).
	Height        int          // Cached line count (-1 means not yet computed).
	Streaming     bool         // True while still receiving chunks.
	Importance    float64

	// Tool call visualization.
	ToolCalls       []ToolCallRecord // Inline tool call records (bounded by agent MaxToolRuns).
	ToolCallRegions []ToolCallRegion // Cached tool call positions (lazily computed with RenderedLines).

	// Thinking indicator state.
	ThinkingText    string        // Animated spinner + timer line (set by model on tick).
	ThinkingStatus  string        // Status message shown below spinner (e.g. "Thinking...", "Handing off to architect...").
	ThinkingColor   string        // Hex color for gradient animation (e.g. "#89b4fa"); empty = use theme default.
	ThinkingElapsed time.Duration // Elapsed from stream start to first chunk (0 = no thinking recorded).

	// Steering state.
	SteeringPending bool // True while steering text awaits agent acknowledgment (renders holographic).
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

// Clear resets the ring buffer, discarding all entries.
func (h *History) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	clear(h.entries)
	h.head = 0
	h.count = 0
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

// UpdateAt atomically modifies the entry at the given logical index.
// The callback receives a pointer to the entry for in-place mutation.
// Returns false if the index is out of range.
func (h *History) UpdateAt(index int, fn func(*ChatEntry)) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if index < 0 || index >= h.count {
		return false
	}
	physical := h.logicalToPhysical(index)
	fn(&h.entries[physical])
	return true
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
	h.entries[physical].ToolCallRegions = nil
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
