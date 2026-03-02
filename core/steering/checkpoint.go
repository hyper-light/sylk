package steering

import (
	"fmt"
	"sync"
	"time"
)

// CheckpointStore is a per-request bounded ring buffer of conversation
// state snapshots. Thread-safe for concurrent capture and lookup.
type CheckpointStore struct {
	mu    sync.Mutex
	ring  [maxCheckpoints]Checkpoint
	head  int // Index of oldest entry.
	tail  int // Index of next write slot.
	count int
}

// Capture creates a new checkpoint at the current turn boundary.
// If snapshotter is non-nil, calls SnapshotState to capture agent-specific
// state. Returns the created checkpoint.
func (s *CheckpointStore) Capture(turn, msgCount int, phase string, snapshotter StateSnapshotter) Checkpoint {
	var agentState []byte
	if snapshotter != nil {
		agentState, _ = snapshotter.SnapshotState()
	}

	cp := Checkpoint{
		ID:           fmt.Sprintf("cp_%06d", turn),
		Turn:         turn,
		MessageCount: msgCount,
		AgentState:   agentState,
		Phase:        phase,
		Timestamp:    time.Now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.ring[s.tail] = cp
	s.tail = (s.tail + 1) % maxCheckpoints

	if s.count < maxCheckpoints {
		s.count++
		return cp
	}
	// Overflow: advance head past the overwritten entry.
	s.head = s.tail
	return cp
}

// FindByID returns the checkpoint with the given ID, or nil if not found.
func (s *CheckpointStore) FindByID(id string) *Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.count {
		idx := (s.head + i) % maxCheckpoints
		if s.ring[idx].ID == id {
			cp := s.ring[idx]
			return &cp
		}
	}
	return nil
}

// FindBefore returns the latest checkpoint where MessageCount <= position.
// Used for edit: find the checkpoint before the edited message.
// Returns nil if no such checkpoint exists.
func (s *CheckpointStore) FindBefore(position int) *Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Iterate in reverse (newest first) for efficiency.
	for i := s.count - 1; i >= 0; i-- {
		idx := (s.head + i) % maxCheckpoints
		if s.ring[idx].MessageCount <= position {
			cp := s.ring[idx]
			return &cp
		}
	}
	return nil
}

// Latest returns the most recently captured checkpoint, or nil if empty.
func (s *CheckpointStore) Latest() *Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.count == 0 {
		return nil
	}

	idx := (s.tail - 1 + maxCheckpoints) % maxCheckpoints
	cp := s.ring[idx]
	return &cp
}

// All returns all checkpoints in chronological order.
func (s *CheckpointStore) All() []Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.count == 0 {
		return nil
	}

	out := make([]Checkpoint, s.count)
	for i := range s.count {
		out[i] = s.ring[(s.head+i)%maxCheckpoints]
	}
	return out
}

// Count returns the number of stored checkpoints.
func (s *CheckpointStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}
