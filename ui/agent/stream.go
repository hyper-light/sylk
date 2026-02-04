package agent

import (
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// streamCapacity is the maximum events held per agent.
// Derived from typical agent task lifecycles: 100 events covers
// the full lifecycle of most agent tasks (start, decisions, tool
// calls, results, completion) without unbounded growth.
const streamCapacity = 100

// AgentEvent represents a single event in an agent's event stream.
type AgentEvent struct {
	Timestamp time.Time
	EventType events.EventType
	Content   string
	Outcome   events.EventOutcome
}

// AgentEventStream is a bounded, concurrent-safe ring buffer of
// AgentEvent entries for a single agent.
type AgentEventStream struct {
	mu       sync.RWMutex
	events   []AgentEvent
	head     int // Next write position.
	count    int // Number of valid entries (<= streamCapacity).
	capacity int
}

// NewAgentEventStream returns an event stream with bounded capacity.
func NewAgentEventStream() *AgentEventStream {
	return &AgentEventStream{
		events:   make([]AgentEvent, streamCapacity),
		capacity: streamCapacity,
	}
}

// Push appends an event, overwriting the oldest if the buffer is full.
func (s *AgentEventStream) Push(ev AgentEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events[s.head] = ev
	s.head = (s.head + 1) % s.capacity
	if s.count < s.capacity {
		s.count++
	}
}

// Len returns the number of valid events in the stream.
func (s *AgentEventStream) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

// Range iterates events from index start (inclusive) to end (exclusive),
// calling fn for each. Iteration stops if fn returns false.
func (s *AgentEventStream) Range(start, end int, fn func(index int, ev *AgentEvent) bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	start = max(start, 0)
	end = min(end, s.count)

	for i := start; i < end; i++ {
		physical := s.logicalToPhysical(i)
		ev := s.events[physical]
		if !fn(i, &ev) {
			return
		}
	}
}

// Last returns the most recent N events, ordered oldest-first.
// If n exceeds the number of stored events, all events are returned.
func (s *AgentEventStream) Last(n int) []AgentEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n = min(n, s.count)
	if n <= 0 {
		return nil
	}

	result := make([]AgentEvent, n)
	startIdx := s.count - n
	for i := range n {
		physical := s.logicalToPhysical(startIdx + i)
		result[i] = s.events[physical]
	}
	return result
}

// Get returns a copy of the event at the given logical index (0 = oldest).
// Returns the event and true if the index is valid, or a zero value and false otherwise.
func (s *AgentEventStream) Get(index int) (AgentEvent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if index < 0 || index >= s.count {
		return AgentEvent{}, false
	}
	return s.events[s.logicalToPhysical(index)], true
}

// Full reports whether the ring buffer is at capacity and will evict on the next Push.
func (s *AgentEventStream) Full() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count == s.capacity
}

// logicalToPhysical converts a logical index (0 = oldest) to a
// physical ring buffer index. Caller must hold at least a read lock.
func (s *AgentEventStream) logicalToPhysical(logical int) int {
	start := 0
	if s.count == s.capacity {
		start = s.head
	}
	return (start + logical) % s.capacity
}
