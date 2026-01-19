// Package context provides adaptive context management for multi-agent systems.
// AR.2.1: SafeChan Observation Channel
package context

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/concurrency/safechan"
)

const defaultBufferSize = 100

var ErrChannelAlreadyClosed = errors.New("channel already closed")

// ObservationChannel provides a safe, context-aware channel for EpisodeObservation.
type ObservationChannel struct {
	ch         chan EpisodeObservation
	bufferSize int
	closed     atomic.Bool
	mu         sync.RWMutex
}

// NewObservationChannel creates a new buffered observation channel.
// If bufferSize <= 0, it defaults to 100.
func NewObservationChannel(bufferSize int) *ObservationChannel {
	if bufferSize <= 0 {
		bufferSize = defaultBufferSize
	}
	return &ObservationChannel{
		ch:         make(chan EpisodeObservation, bufferSize),
		bufferSize: bufferSize,
	}
}

// Send sends an observation to the channel, respecting context cancellation.
// Returns error if context cancelled or channel closed.
func (oc *ObservationChannel) Send(ctx context.Context, obs EpisodeObservation) error {
	if oc.IsClosed() {
		return safechan.ErrChannelClosed
	}
	return safechan.Send(ctx, oc.ch, obs)
}

// Receive receives an observation from the channel, respecting context cancellation.
// Returns error if context cancelled or channel closed.
func (oc *ObservationChannel) Receive(ctx context.Context) (EpisodeObservation, error) {
	return safechan.Recv(ctx, oc.ch)
}

// TrySend attempts a non-blocking send.
// Returns false if the channel is full or closed.
func (oc *ObservationChannel) TrySend(obs EpisodeObservation) bool {
	if oc.IsClosed() {
		return false
	}
	select {
	case oc.ch <- obs:
		return true
	default:
		return false
	}
}

// Close closes the channel gracefully.
// Returns error if already closed.
func (oc *ObservationChannel) Close() error {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	if oc.closed.Load() {
		return ErrChannelAlreadyClosed
	}
	oc.closed.Store(true)
	close(oc.ch)
	return nil
}

// IsClosed returns whether the channel is closed.
func (oc *ObservationChannel) IsClosed() bool {
	return oc.closed.Load()
}

// Len returns the current number of items in the buffer.
func (oc *ObservationChannel) Len() int {
	return len(oc.ch)
}
