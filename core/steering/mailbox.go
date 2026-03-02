package steering

import "sync"

// SteeringMailbox is a bounded ring buffer for pending user steering commands.
// Multiple producers (bus handler goroutine, UI), single consumer (tool loop
// goroutine). No channels, no goroutines — mutex-guarded only.
type SteeringMailbox struct {
	mu    sync.Mutex
	ring  [mailboxCapacity]Command
	head  int // Index of oldest entry.
	tail  int // Index of next write slot.
	count int
}

// Deposit adds a command to the mailbox. Non-blocking.
// If full, overwrites the oldest entry (newest steering always wins).
func (m *SteeringMailbox) Deposit(cmd Command) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ring[m.tail] = cmd
	m.tail = (m.tail + 1) % mailboxCapacity

	if m.count < mailboxCapacity {
		m.count++
		return
	}
	// Overflow: advance head past the overwritten entry.
	m.head = m.tail
}

// Drain returns all pending commands in FIFO order and resets the buffer.
// Returns nil if the mailbox is empty.
func (m *SteeringMailbox) Drain() []Command {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.count == 0 {
		return nil
	}

	cmds := make([]Command, m.count)
	for i := range m.count {
		cmds[i] = m.ring[(m.head+i)%mailboxCapacity]
	}

	m.head = 0
	m.tail = 0
	m.count = 0
	return cmds
}

// Pending returns the number of queued commands without draining.
func (m *SteeringMailbox) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}
