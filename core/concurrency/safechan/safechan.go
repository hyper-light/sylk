package safechan

import (
	"context"
	"errors"
	"time"
)

var ErrChannelClosed = errors.New("channel closed")

// Send sends on channel, respecting context cancellation
func Send[T any](ctx context.Context, ch chan<- T, value T) error {
	select {
	case ch <- value:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Recv receives from channel, respecting context cancellation
func Recv[T any](ctx context.Context, ch <-chan T) (T, error) {
	select {
	case v, ok := <-ch:
		if !ok {
			var zero T
			return zero, ErrChannelClosed
		}
		return v, nil
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Sleep sleeps, but wakes on context cancellation
func Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
