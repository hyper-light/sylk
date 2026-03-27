package shared

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithoutDeadlineCancellation_IgnoresParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	ctx, release := WithoutDeadlineCancellation(parent)
	defer release()

	time.Sleep(40 * time.Millisecond)

	select {
	case <-ctx.Done():
		t.Fatalf("context canceled unexpectedly: %v", context.Cause(ctx))
	default:
	}
}

func TestWithoutDeadlineCancellation_PropagatesExplicitCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx, release := WithoutDeadlineCancellation(parent)
	defer release()

	cancel()

	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for explicit cancellation")
	}

	if !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Fatalf("cause = %v, want context.Canceled", context.Cause(ctx))
	}
}
