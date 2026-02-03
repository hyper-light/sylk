package input

// InputHistory stores previously submitted prompts for recall.
// Bounded ring buffer to prevent unbounded growth.
type InputHistory struct {
	entries  []string
	head     int
	count    int
	capacity int
	cursor   int    // -1 when not navigating, 0..count-1 when navigating
	draft    string // saves current input when user starts navigating history
}

// NewInputHistory creates an InputHistory with the given capacity.
// The capacity is clamped to a minimum of 1.
func NewInputHistory(capacity int) *InputHistory {
	capacity = max(capacity, 1)
	return &InputHistory{
		entries:  make([]string, capacity),
		capacity: capacity,
		cursor:   -1,
	}
}

// Push adds a prompt to the history ring buffer and resets the navigation cursor.
// Duplicate consecutive entries are suppressed.
func (h *InputHistory) Push(text string) {
	if text == "" {
		return
	}
	if h.count > 0 && h.entries[h.newestSlot()] == text {
		h.cursor = -1
		return
	}
	slot := (h.head + h.count) % h.capacity
	if h.count == h.capacity {
		// Buffer full -- overwrite oldest and advance head.
		slot = h.head
		h.head = (h.head + 1) % h.capacity
	} else {
		h.count++
	}
	h.entries[slot] = text
	h.cursor = -1
}

// Up moves to an older history entry. It returns the entry text and true,
// or ("", false) when there are no older entries.
func (h *InputHistory) Up(currentDraft string) (string, bool) {
	if h.count == 0 {
		return "", false
	}
	if h.cursor == -1 {
		h.draft = currentDraft
		h.cursor = 0
	} else if h.cursor < h.count-1 {
		h.cursor++
	} else {
		return h.entryAt(h.cursor), false
	}
	return h.entryAt(h.cursor), true
}

// Down moves to a newer history entry. When the cursor reaches -1 (newest),
// it returns the saved draft. Returns ("", false) when already at newest.
func (h *InputHistory) Down() (string, bool) {
	if h.cursor <= -1 {
		return "", false
	}
	h.cursor--
	if h.cursor == -1 {
		return h.draft, true
	}
	return h.entryAt(h.cursor), true
}

// Reset resets the navigation cursor to -1 (not navigating).
func (h *InputHistory) Reset() {
	h.cursor = -1
}

// Len returns the number of entries in the history.
func (h *InputHistory) Len() int {
	return h.count
}

// newestSlot returns the ring-buffer index of the most recently pushed entry.
func (h *InputHistory) newestSlot() int {
	return (h.head + h.count - 1) % h.capacity
}

// entryAt returns the entry at the given cursor offset (0 = newest).
func (h *InputHistory) entryAt(offset int) string {
	idx := (h.head + h.count - 1 - offset) % h.capacity
	return h.entries[idx]
}
