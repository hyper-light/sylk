package safechan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSendSucceedsWhenChannelNotFull(t *testing.T) {
	ch := make(chan int, 1)
	ctx := context.Background()

	err := Send(ctx, ch, 42)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	val := <-ch
	if val != 42 {
		t.Fatalf("expected 42, got %d", val)
	}
}

func TestSendRespectsContextCancellation(t *testing.T) {
	ch := make(chan int)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Send(ctx, ch, 42)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSendBlocksUntilContextTimeout(t *testing.T) {
	ch := make(chan int)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Send(ctx, ch, 42)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("expected to block for at least 10ms, blocked for %v", elapsed)
	}
}

func TestRecvSucceedsWhenDataAvailable(t *testing.T) {
	ch := make(chan int, 1)
	ch <- 42
	ctx := context.Background()

	val, err := Recv(ctx, ch)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if val != 42 {
		t.Fatalf("expected 42, got %d", val)
	}
}

func TestRecvRespectsContextCancellation(t *testing.T) {
	ch := make(chan int)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Recv(ctx, ch)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRecvReturnsErrorOnClosedChannel(t *testing.T) {
	ch := make(chan int)
	close(ch)
	ctx := context.Background()

	_, err := Recv(ctx, ch)
	if !errors.Is(err, ErrChannelClosed) {
		t.Fatalf("expected ErrChannelClosed, got %v", err)
	}
}

func TestRecvBlocksUntilContextTimeout(t *testing.T) {
	ch := make(chan int)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Recv(ctx, ch)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("expected to block for at least 10ms, blocked for %v", elapsed)
	}
}

func TestSleepCompletesAfterDuration(t *testing.T) {
	ctx := context.Background()
	start := time.Now()

	err := Sleep(ctx, 20*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected to sleep for at least 20ms, slept for %v", elapsed)
	}
}

func TestSleepRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Sleep(ctx, 1*time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSleepRespectsContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Sleep(ctx, 1*time.Hour)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("expected to block for at least 10ms, blocked for %v", elapsed)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected to cancel quickly, took %v", elapsed)
	}
}

func TestConcurrentSendRecv(t *testing.T) {
	ch := make(chan int)
	ctx := context.Background()
	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := Send(ctx, ch, i); err != nil {
				t.Errorf("send error: %v", err)
				return
			}
		}
		close(ch)
	}()

	go func() {
		defer wg.Done()
		count := 0
		for {
			_, err := Recv(ctx, ch)
			if errors.Is(err, ErrChannelClosed) {
				break
			}
			if err != nil {
				t.Errorf("recv error: %v", err)
				return
			}
			count++
		}
		if count != 100 {
			t.Errorf("expected 100 values, got %d", count)
		}
	}()

	wg.Wait()
}
