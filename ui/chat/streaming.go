package chat

import (
	"strings"
	"sync"
	"time"
)

// StreamAccumulator buffers streaming tokens from an LLM response
// and tracks the associated entry in History for incremental updates.
type StreamAccumulator struct {
	mu         sync.Mutex
	entryIndex int // Logical index in History of the streaming entry.
	content    strings.Builder
	lastChunk  time.Time
	done       bool
}

// NewStreamAccumulator creates an accumulator that tracks the entry
// at the given logical index in History.
func NewStreamAccumulator(entryIndex int) *StreamAccumulator {
	return &StreamAccumulator{
		entryIndex: entryIndex,
		lastChunk:  time.Now(),
	}
}

// Append adds a text chunk to the accumulated content and records
// the timestamp of the most recent chunk.
func (sa *StreamAccumulator) Append(text string) {
	sa.mu.Lock()
	defer sa.mu.Unlock()

	sa.content.WriteString(text)
	sa.lastChunk = time.Now()
}

// Replace overwrites the accumulated content with the given text.
// Used when the agent provides authoritative final text to correct
// any dropped or reordered streaming chunks.
func (sa *StreamAccumulator) Replace(text string) {
	sa.mu.Lock()
	defer sa.mu.Unlock()

	sa.content.Reset()
	sa.content.WriteString(text)
}

// Complete marks the stream as finished. After this call, IsDone
// returns true and no further Append calls should be made.
func (sa *StreamAccumulator) Complete() {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	sa.done = true
}

// Content returns the accumulated text so far.
func (sa *StreamAccumulator) Content() string {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.content.String()
}

// IsDone reports whether the stream has been completed.
func (sa *StreamAccumulator) IsDone() bool {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.done
}

// EntryIndex returns the logical History index of the streaming entry.
func (sa *StreamAccumulator) EntryIndex() int {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.entryIndex
}

// LastChunk returns the timestamp of the most recent chunk.
func (sa *StreamAccumulator) LastChunk() time.Time {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.lastChunk
}
