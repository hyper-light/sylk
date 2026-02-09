package motion

// jumpListCapacity is the maximum number of entries in the jump list.
// Derived from vim's default jump list size.
const jumpListCapacity = 100

// JumpEntry records a cursor position for the jump list.
type JumpEntry struct {
	File string
	Line int
	Col  int
}

// JumpList maintains a bounded list of jump positions with forward/backward
// navigation. It is implemented as a ring buffer with a moving cursor.
type JumpList struct {
	entries []JumpEntry
	head    int // index of the next write slot
	count   int // number of valid entries
	cursor  int // current navigation position (relative to oldest entry)
}

// NewJumpList creates an empty jump list.
func NewJumpList() *JumpList {
	return &JumpList{
		entries: make([]JumpEntry, jumpListCapacity),
	}
}

// Push records the current position before a jump. Entries after the current
// cursor position are discarded (forward history is lost on a new jump).
func (jl *JumpList) Push(entry JumpEntry) {
	// Discard any forward entries beyond the cursor.
	jl.count = jl.cursor
	// Write at the head position.
	idx := jl.slotIndex(jl.count)
	jl.entries[idx] = entry
	jl.count++
	// If we've exceeded capacity, advance the head to drop the oldest.
	if jl.count > jumpListCapacity {
		jl.head = (jl.head + 1) % jumpListCapacity
		jl.count = jumpListCapacity
	}
	jl.cursor = jl.count
}

// Back moves backward in the jump list (Ctrl+O) and returns the entry.
func (jl *JumpList) Back() (JumpEntry, bool) {
	if jl.cursor <= 0 {
		return JumpEntry{}, false
	}
	jl.cursor--
	return jl.entryAt(jl.cursor), true
}

// Forward moves forward in the jump list (Ctrl+I) and returns the entry.
func (jl *JumpList) Forward() (JumpEntry, bool) {
	if jl.cursor >= jl.count-1 {
		return JumpEntry{}, false
	}
	jl.cursor++
	return jl.entryAt(jl.cursor), true
}

// CanBack reports whether a backward jump is available.
func (jl *JumpList) CanBack() bool {
	return jl.cursor > 0
}

// Len returns the number of entries in the jump list.
func (jl *JumpList) Len() int {
	return jl.count
}

// Entries returns a copy of all jump list entries in chronological order.
func (jl *JumpList) Entries() []JumpEntry {
	result := make([]JumpEntry, jl.count)
	for i := range jl.count {
		result[i] = jl.entryAt(i)
	}
	return result
}

// slotIndex returns the physical ring buffer index for a logical position.
func (jl *JumpList) slotIndex(logicalPos int) int {
	return (jl.head + logicalPos) % jumpListCapacity
}

// entryAt returns the entry at the given logical position.
func (jl *JumpList) entryAt(logicalPos int) JumpEntry {
	return jl.entries[jl.slotIndex(logicalPos)]
}
