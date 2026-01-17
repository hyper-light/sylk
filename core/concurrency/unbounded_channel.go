package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
)

// UnboundedChannel is a channel that NEVER blocks the sender and NEVER drops
// messages. It is designed for user-facing input where message loss is
// unacceptable. Messages are buffered in an overflow slice when the channel
// is full.
type UnboundedChannel[T any] struct {
	ch       chan T
	overflow []T
	mu       sync.Mutex
	cond     *sync.Cond

	closed    atomic.Bool
	closeOnce sync.Once

	sendCount atomic.Int64
	recvCount atomic.Int64
}

const defaultUnboundedCapacity = 64

// NewUnboundedChannel creates a new unbounded channel with default capacity.
func NewUnboundedChannel[T any]() *UnboundedChannel[T] {
	return NewUnboundedChannelWithCapacity[T](defaultUnboundedCapacity)
}

// NewUnboundedChannelWithCapacity creates a new unbounded channel with
// specified fast-path capacity.
func NewUnboundedChannelWithCapacity[T any](capacity int) *UnboundedChannel[T] {
	if capacity <= 0 {
		capacity = defaultUnboundedCapacity
	}
	uc := &UnboundedChannel[T]{
		ch:       make(chan T, capacity),
		overflow: make([]T, 0),
	}
	uc.cond = sync.NewCond(&uc.mu)
	return uc
}

// Send adds a message to the channel. It NEVER blocks and NEVER drops
// messages. If the channel buffer is full, the message is stored in the
// overflow slice.
func (uc *UnboundedChannel[T]) Send(msg T) error {
	if uc.closed.Load() {
		return ErrChannelClosed
	}

	uc.sendCount.Add(1)

	// Fast path: try non-blocking send to channel
	select {
	case uc.ch <- msg:
		return nil
	default:
		// Channel full, use overflow
	}

	// Slow path: add to overflow slice
	uc.mu.Lock()
	if uc.closed.Load() {
		uc.mu.Unlock()
		return ErrChannelClosed
	}
	uc.overflow = append(uc.overflow, msg)
	uc.cond.Signal() // Wake up any waiting receivers
	uc.mu.Unlock()

	return nil
}

// Receive retrieves a message from the channel. It blocks until a message
// is available or the context is cancelled. Messages are drained from the
// channel buffer first (to maintain FIFO), then from the overflow slice.
func (uc *UnboundedChannel[T]) Receive() (T, error) {
	return uc.ReceiveWithContext(context.Background())
}

// ReceiveWithContext retrieves a message from the channel with context
// cancellation support. FIFO ordering is maintained: channel buffer is
// drained first (oldest messages), then overflow buffer.
func (uc *UnboundedChannel[T]) ReceiveWithContext(ctx context.Context) (T, error) {
	var zero T

	// Check for immediate closure
	if uc.isClosedAndEmpty() {
		return zero, ErrChannelClosed
	}

	// Try non-blocking receive from channel first (maintains FIFO - channel
	// has older messages than overflow)
	select {
	case msg, ok := <-uc.ch:
		if ok {
			uc.recvCount.Add(1)
			return msg, nil
		}
		// Channel closed, check overflow
		if msg, ok := uc.drainOverflow(); ok {
			uc.recvCount.Add(1)
			return msg, nil
		}
		return zero, ErrChannelClosed
	default:
		// Channel empty, try overflow
	}

	// Check overflow (newer messages that couldn't fit in channel)
	if msg, ok := uc.drainOverflow(); ok {
		uc.recvCount.Add(1)
		return msg, nil
	}

	// Both empty, block on channel with context
	select {
	case msg, ok := <-uc.ch:
		if !ok {
			// Channel closed, final overflow check
			if msg, ok := uc.drainOverflow(); ok {
				uc.recvCount.Add(1)
				return msg, nil
			}
			return zero, ErrChannelClosed
		}
		uc.recvCount.Add(1)
		return msg, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// TryReceive attempts a non-blocking receive. Returns false if no message
// is immediately available. FIFO ordering: channel first, then overflow.
func (uc *UnboundedChannel[T]) TryReceive() (T, bool) {
	var zero T

	// Try channel first (older messages)
	select {
	case msg, ok := <-uc.ch:
		if ok {
			uc.recvCount.Add(1)
			return msg, true
		}
		// Channel closed, fall through to overflow
	default:
		// Channel empty, fall through to overflow
	}

	// Try overflow (newer messages)
	if msg, ok := uc.drainOverflow(); ok {
		uc.recvCount.Add(1)
		return msg, true
	}

	return zero, false
}

// Len returns the total number of pending messages (channel + overflow).
func (uc *UnboundedChannel[T]) Len() int {
	uc.mu.Lock()
	total := len(uc.ch) + len(uc.overflow)
	uc.mu.Unlock()
	return total
}

// ChannelLen returns the number of messages in the channel buffer.
func (uc *UnboundedChannel[T]) ChannelLen() int {
	return len(uc.ch)
}

// OverflowLen returns the number of messages in the overflow buffer.
func (uc *UnboundedChannel[T]) OverflowLen() int {
	uc.mu.Lock()
	length := len(uc.overflow)
	uc.mu.Unlock()
	return length
}

// Stats returns channel statistics.
func (uc *UnboundedChannel[T]) Stats() UnboundedChannelStats {
	uc.mu.Lock()
	stats := UnboundedChannelStats{
		ChannelLen:   len(uc.ch),
		OverflowLen:  len(uc.overflow),
		SendCount:    uc.sendCount.Load(),
		ReceiveCount: uc.recvCount.Load(),
		IsClosed:     uc.closed.Load(),
	}
	uc.mu.Unlock()
	return stats
}

// UnboundedChannelStats contains statistics for an unbounded channel.
type UnboundedChannelStats struct {
	ChannelLen   int
	OverflowLen  int
	SendCount    int64
	ReceiveCount int64
	IsClosed     bool
}

// Close closes the channel. After closing, Send returns ErrChannelClosed
// but Receive can still drain pending messages.
func (uc *UnboundedChannel[T]) Close() {
	uc.closeOnce.Do(func() {
		uc.closed.Store(true)
		uc.mu.Lock()
		close(uc.ch)
		uc.cond.Broadcast() // Wake up all waiting receivers
		uc.mu.Unlock()
	})
}

// IsClosed returns true if the channel has been closed.
func (uc *UnboundedChannel[T]) IsClosed() bool {
	return uc.closed.Load()
}

// drainOverflow removes and returns the first message from the overflow
// slice, if any.
func (uc *UnboundedChannel[T]) drainOverflow() (T, bool) {
	var zero T

	uc.mu.Lock()
	defer uc.mu.Unlock()

	if len(uc.overflow) == 0 {
		return zero, false
	}

	msg := uc.overflow[0]
	uc.overflow[0] = zero // Clear reference for GC
	uc.overflow = uc.overflow[1:]
	return msg, true
}

// isClosedAndEmpty returns true if the channel is closed and has no
// pending messages.
func (uc *UnboundedChannel[T]) isClosedAndEmpty() bool {
	if !uc.closed.Load() {
		return false
	}

	uc.mu.Lock()
	empty := len(uc.ch) == 0 && len(uc.overflow) == 0
	uc.mu.Unlock()
	return empty
}
