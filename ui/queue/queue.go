package queue

import "time"

// MaxCapacity is the hard upper bound on queued entries.
const MaxCapacity = 32

// Queue is a bounded FIFO of prompt entries with pause/cancel semantics.
// Not safe for concurrent use — callers must synchronize externally.
type Queue struct {
	entries  []Entry
	capacity int
	paused   bool
}

// New creates a Queue with the given capacity (clamped to [1, MaxCapacity]).
func New(capacity int) Queue {
	capacity = max(min(capacity, MaxCapacity), 1)
	return Queue{
		entries:  make([]Entry, 0, capacity),
		capacity: capacity,
	}
}

// Enqueue adds a pending prompt to the tail of the queue.
// Returns the created Entry and true on success, or a zero Entry and false
// if the queue is at capacity.
func (q *Queue) Enqueue(id, text, targetAgent, sessionID string) (Entry, bool) {
	if len(q.entries) >= q.capacity {
		return Entry{}, false
	}
	e := Entry{
		ID:          id,
		Text:        text,
		TargetAgent: targetAgent,
		SessionID:   sessionID,
		State:       StatePending,
		CreatedAt:   time.Now(),
	}
	q.entries = append(q.entries, e)
	return e, true
}

// Peek returns the head entry without removing it.
// Returns false if the queue is empty.
func (q *Queue) Peek() (Entry, bool) {
	if len(q.entries) == 0 {
		return Entry{}, false
	}
	return q.entries[0], true
}

// Advance removes terminal head entries and returns the first pending entry.
// Returns false if no pending entry is available.
func (q *Queue) Advance() (Entry, bool) {
	q.compact()
	for i := range q.entries {
		if q.entries[i].State == StatePending {
			return q.entries[i], true
		}
	}
	return Entry{}, false
}

// AdvanceReady returns the first pending entry per distinct target agent,
// skipping agents that are already busy. The isBusy predicate receives a
// target agent ID and returns true if that agent has an active stream.
// At most one entry per agent is returned to prevent dispatch conflicts.
func (q *Queue) AdvanceReady(isBusy func(agentID string) bool) []Entry {
	q.compact()
	claimed := make(map[string]bool)
	var ready []Entry
	for i := range q.entries {
		e := &q.entries[i]
		if e.State != StatePending {
			continue
		}
		if claimed[e.TargetAgent] || isBusy(e.TargetAgent) {
			continue
		}
		claimed[e.TargetAgent] = true
		ready = append(ready, *e)
	}
	return ready
}

// Cancel marks the entry with the given ID as Cancelled.
// Only pending entries can be cancelled. Returns true if cancelled.
func (q *Queue) Cancel(id string) bool {
	for i := range q.entries {
		if q.entries[i].ID == id && q.entries[i].State == StatePending {
			q.entries[i].State = StateCancelled
			q.compact()
			return true
		}
	}
	return false
}

// CancelAll cancels all pending entries and returns the count cancelled.
func (q *Queue) CancelAll() int {
	n := 0
	for i := range q.entries {
		if q.entries[i].State == StatePending {
			q.entries[i].State = StateCancelled
			n++
		}
	}
	q.compact()
	return n
}

// TogglePause flips the paused flag and returns the new state.
func (q *Queue) TogglePause() bool {
	q.paused = !q.paused
	return q.paused
}

// SetPaused sets the paused state directly.
func (q *Queue) SetPaused(p bool) {
	q.paused = p
}

// Reorder moves the pending entry with the given ID by delta positions
// within the pending portion of the queue. Negative delta moves toward
// the head; positive toward the tail. Non-pending entries are skipped.
func (q *Queue) Reorder(id string, delta int) {
	if delta == 0 {
		return
	}

	// Collect indices of pending entries.
	pending := q.pendingIndices()
	if len(pending) < 2 {
		return
	}

	// Find the target within pending.
	pos := -1
	for i, idx := range pending {
		if q.entries[idx].ID == id {
			pos = i
			break
		}
	}
	if pos < 0 {
		return
	}

	newPos := max(min(pos+delta, len(pending)-1), 0)
	if newPos == pos {
		return
	}

	// Bubble the element step-by-step through adjacent swaps so
	// intermediate entries shift correctly.
	step := 1
	if newPos < pos {
		step = -1
	}
	for cur := pos; cur != newPos; cur += step {
		next := cur + step
		srcIdx := pending[cur]
		dstIdx := pending[next]
		q.entries[srcIdx], q.entries[dstIdx] = q.entries[dstIdx], q.entries[srcIdx]
	}
}

// MarkDispatching transitions a pending entry to StateDispatching.
func (q *Queue) MarkDispatching(id string) {
	if e := q.find(id); e != nil && e.State == StatePending {
		e.State = StateDispatching
		e.DispatchedAt = time.Now()
	}
}

// MarkActive transitions a dispatching entry to StateActive with a correlation ID.
func (q *Queue) MarkActive(id, correlationID string) {
	if e := q.find(id); e != nil && e.State == StateDispatching {
		e.State = StateActive
		e.CorrelationID = correlationID
	}
}

// MarkCompleted transitions an active entry to StateCompleted.
func (q *Queue) MarkCompleted(id string) {
	if e := q.find(id); e != nil && (e.State == StateActive || e.State == StateDispatching) {
		e.State = StateCompleted
	}
}

// MarkFailed transitions an active or dispatching entry to StateFailed.
func (q *Queue) MarkFailed(id string) {
	if e := q.find(id); e != nil && (e.State == StateActive || e.State == StateDispatching) {
		e.State = StateFailed
	}
}

// Len returns the total number of entries (all states).
func (q *Queue) Len() int { return len(q.entries) }

// PendingCount returns the number of pending entries.
func (q *Queue) PendingCount() int {
	n := 0
	for i := range q.entries {
		if q.entries[i].State == StatePending {
			n++
		}
	}
	return n
}

// IsEmpty reports whether the queue has no entries.
func (q *Queue) IsEmpty() bool { return len(q.entries) == 0 }

// IsPaused reports whether the queue is paused.
func (q *Queue) IsPaused() bool { return q.paused }

// PendingEntries returns a copy of all pending entries in queue order.
func (q *Queue) PendingEntries() []Entry {
	out := make([]Entry, 0, q.PendingCount())
	for _, e := range q.entries {
		if e.State == StatePending {
			out = append(out, e)
		}
	}
	return out
}

// ActiveEntry returns the currently active or dispatching entry, if any.
func (q *Queue) ActiveEntry() (Entry, bool) {
	for _, e := range q.entries {
		if e.State == StateActive || e.State == StateDispatching {
			return e, true
		}
	}
	return Entry{}, false
}

// ActiveEntryByCorrelation returns the active entry matching the given
// correlation ID, or false if none matches.
func (q *Queue) ActiveEntryByCorrelation(correlationID string) (Entry, bool) {
	for _, e := range q.entries {
		if (e.State == StateActive || e.State == StateDispatching) && e.CorrelationID == correlationID {
			return e, true
		}
	}
	return Entry{}, false
}

// Find returns a pointer to the entry with the given ID, or nil.
func (q *Queue) Find(id string) *Entry {
	return q.find(id)
}

// find returns a pointer to the entry with the given ID, or nil.
func (q *Queue) find(id string) *Entry {
	for i := range q.entries {
		if q.entries[i].ID == id {
			return &q.entries[i]
		}
	}
	return nil
}

// compact removes all terminal entries from the queue.
func (q *Queue) compact() {
	n := 0
	for i := range q.entries {
		if !q.entries[i].State.IsTerminal() {
			q.entries[n] = q.entries[i]
			n++
		}
	}
	clear(q.entries[n:])
	q.entries = q.entries[:n]
}

// pendingIndices returns the indices of all pending entries.
func (q *Queue) pendingIndices() []int {
	out := make([]int, 0, len(q.entries))
	for i := range q.entries {
		if q.entries[i].State == StatePending {
			out = append(out, i)
		}
	}
	return out
}
