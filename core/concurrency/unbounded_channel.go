package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
)

const (
	defaultUnboundedCapacity    = 64
	defaultMaxOverflowSize      = 10000
	unlimitedOverflow       int = 0
)

// UnboundedChannelConfig configures an UnboundedChannel.
type UnboundedChannelConfig struct {
	// ChannelCapacity is the size of the fast-path Go channel buffer.
	// Default: 64
	ChannelCapacity int

	// MaxOverflowSize is the maximum number of items in the overflow buffer.
	// Set to 0 for unlimited (legacy behavior, not recommended).
	// Default: 10000
	MaxOverflowSize int

	// OverflowDropPolicy determines behavior when overflow is full.
	// Only applies when MaxOverflowSize > 0.
	// Default: DropNewest (reject new messages when full)
	OverflowDropPolicy DropPolicy
}

// DefaultUnboundedChannelConfig returns sensible defaults for UnboundedChannel.
func DefaultUnboundedChannelConfig() UnboundedChannelConfig {
	return UnboundedChannelConfig{
		ChannelCapacity:    defaultUnboundedCapacity,
		MaxOverflowSize:    defaultMaxOverflowSize,
		OverflowDropPolicy: DropNewest,
	}
}

// UnboundedChannel is a channel that NEVER blocks the sender. It is designed
// for user-facing input where blocking is unacceptable. Messages are buffered
// in an overflow buffer when the channel is full.
//
// With MaxOverflowSize > 0, the overflow buffer is bounded to prevent memory
// exhaustion. When the overflow is full, behavior depends on OverflowDropPolicy:
//   - DropOldest: evict oldest message to make room for new one
//   - DropNewest: reject new message (returns ErrOverflowFull from Send)
//   - Block: wait until space is available (not recommended for unbounded semantics)
//
// With MaxOverflowSize = 0, the overflow grows without bound (legacy behavior).
type UnboundedChannel[T any] struct {
	ch             chan T
	boundedOvfl    *BoundedOverflow[T] // Used when MaxOverflowSize > 0
	unboundedOvfl  []T                 // Used when MaxOverflowSize = 0 (legacy)
	maxOverflowSz  int
	mu             sync.Mutex
	cond           *sync.Cond

	closed    atomic.Bool
	closeOnce sync.Once

	sendCount    atomic.Int64
	recvCount    atomic.Int64
	droppedCount atomic.Int64
}

// NewUnboundedChannel creates a new unbounded channel with default capacity
// and bounded overflow (10000 items max, DropNewest policy).
func NewUnboundedChannel[T any]() *UnboundedChannel[T] {
	return NewUnboundedChannelWithConfig[T](DefaultUnboundedChannelConfig())
}

// NewUnboundedChannelWithCapacity creates a new unbounded channel with
// specified fast-path capacity and bounded overflow (10000 items max).
// Deprecated: Use NewUnboundedChannelWithConfig for full control.
func NewUnboundedChannelWithCapacity[T any](capacity int) *UnboundedChannel[T] {
	cfg := DefaultUnboundedChannelConfig()
	cfg.ChannelCapacity = capacity
	return NewUnboundedChannelWithConfig[T](cfg)
}

// NewUnboundedChannelWithConfig creates a new unbounded channel with the
// specified configuration.
func NewUnboundedChannelWithConfig[T any](config UnboundedChannelConfig) *UnboundedChannel[T] {
	if config.ChannelCapacity <= 0 {
		config.ChannelCapacity = defaultUnboundedCapacity
	}

	uc := &UnboundedChannel[T]{
		ch:            make(chan T, config.ChannelCapacity),
		maxOverflowSz: config.MaxOverflowSize,
	}
	uc.cond = sync.NewCond(&uc.mu)

	if config.MaxOverflowSize > 0 {
		// Bounded overflow mode
		uc.boundedOvfl = NewBoundedOverflow[T](config.MaxOverflowSize, config.OverflowDropPolicy)
	} else {
		// Legacy unbounded mode (not recommended)
		uc.unboundedOvfl = make([]T, 0)
	}

	return uc
}

// Send adds a message to the channel. It NEVER blocks the sender.
// If the channel buffer is full, the message is stored in the overflow buffer.
//
// When using bounded overflow (MaxOverflowSize > 0):
//   - With DropNewest policy: returns ErrOverflowFull if overflow is full
//   - With DropOldest policy: evicts oldest message, always succeeds
//   - With Block policy: blocks until space available (not recommended)
//
// When using unbounded overflow (MaxOverflowSize = 0): always succeeds but
// may cause memory exhaustion under sustained high load.
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

	// Slow path: add to overflow buffer
	uc.mu.Lock()
	if uc.closed.Load() {
		uc.mu.Unlock()
		return ErrChannelClosed
	}

	var added bool
	if uc.boundedOvfl != nil {
		// Bounded overflow mode
		added = uc.boundedOvfl.Add(msg)
		if !added {
			// DropNewest policy or closed - message was dropped
			uc.droppedCount.Add(1)
			uc.mu.Unlock()
			return ErrOverflowFull
		}
	} else {
		// Legacy unbounded mode
		uc.unboundedOvfl = append(uc.unboundedOvfl, msg)
	}

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
	total := len(uc.ch) + uc.overflowLenLocked()
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
	length := uc.overflowLenLocked()
	uc.mu.Unlock()
	return length
}

// overflowLenLocked returns the overflow length. Caller must hold mu.
func (uc *UnboundedChannel[T]) overflowLenLocked() int {
	if uc.boundedOvfl != nil {
		return uc.boundedOvfl.Len()
	}
	return len(uc.unboundedOvfl)
}

// Stats returns channel statistics.
func (uc *UnboundedChannel[T]) Stats() UnboundedChannelStats {
	uc.mu.Lock()
	var overflowCap int
	var droppedFromOvfl int64

	if uc.boundedOvfl != nil {
		overflowCap = uc.boundedOvfl.Cap()
		droppedFromOvfl = uc.boundedOvfl.DroppedCount()
	} else {
		overflowCap = -1 // Unbounded
	}

	stats := UnboundedChannelStats{
		ChannelLen:       len(uc.ch),
		OverflowLen:      uc.overflowLenLocked(),
		OverflowCapacity: overflowCap,
		SendCount:        uc.sendCount.Load(),
		ReceiveCount:     uc.recvCount.Load(),
		DroppedCount:     uc.droppedCount.Load() + droppedFromOvfl,
		IsClosed:         uc.closed.Load(),
		IsBounded:        uc.boundedOvfl != nil,
	}
	uc.mu.Unlock()
	return stats
}

// UnboundedChannelStats contains statistics for an unbounded channel.
type UnboundedChannelStats struct {
	ChannelLen       int   // Messages in the Go channel buffer
	OverflowLen      int   // Messages in the overflow buffer
	OverflowCapacity int   // Max overflow capacity (-1 if unbounded)
	SendCount        int64 // Total messages sent
	ReceiveCount     int64 // Total messages received
	DroppedCount     int64 // Messages dropped due to overflow full
	IsClosed         bool
	IsBounded        bool // True if using bounded overflow
}

// Close closes the channel. After closing, Send returns ErrChannelClosed
// but Receive can still drain pending messages.
func (uc *UnboundedChannel[T]) Close() {
	uc.closeOnce.Do(func() {
		uc.closed.Store(true)
		uc.mu.Lock()
		close(uc.ch)
		if uc.boundedOvfl != nil {
			uc.boundedOvfl.Close()
		}
		uc.cond.Broadcast() // Wake up all waiting receivers
		uc.mu.Unlock()
	})
}

// IsClosed returns true if the channel has been closed.
func (uc *UnboundedChannel[T]) IsClosed() bool {
	return uc.closed.Load()
}

// drainOverflow removes and returns the first message from the overflow
// buffer, if any.
func (uc *UnboundedChannel[T]) drainOverflow() (T, bool) {
	var zero T

	uc.mu.Lock()
	defer uc.mu.Unlock()

	if uc.boundedOvfl != nil {
		// Bounded overflow mode
		return uc.boundedOvfl.Take()
	}

	// Legacy unbounded mode
	if len(uc.unboundedOvfl) == 0 {
		return zero, false
	}

	msg := uc.unboundedOvfl[0]
	uc.unboundedOvfl[0] = zero // Clear reference for GC
	uc.unboundedOvfl = uc.unboundedOvfl[1:]
	return msg, true
}

// isClosedAndEmpty returns true if the channel is closed and has no
// pending messages.
func (uc *UnboundedChannel[T]) isClosedAndEmpty() bool {
	if !uc.closed.Load() {
		return false
	}

	uc.mu.Lock()
	empty := len(uc.ch) == 0 && uc.overflowLenLocked() == 0
	uc.mu.Unlock()
	return empty
}
