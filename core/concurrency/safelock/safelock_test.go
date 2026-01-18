package safelock

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMutexLockUnlock(t *testing.T) {
	m := NewMutex()
	ctx := context.Background()

	err := m.Lock(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	m.Unlock()
}

func TestMutexLockRespectsContextCancellation(t *testing.T) {
	m := NewMutex()
	ctx := context.Background()

	err := m.Lock(ctx)
	if err != nil {
		t.Fatalf("expected no error on first lock, got %v", err)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err = m.Lock(cancelCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	m.Unlock()
}

func TestMutexLockRespectsContextTimeout(t *testing.T) {
	m := NewMutex()
	ctx := context.Background()

	err := m.Lock(ctx)
	if err != nil {
		t.Fatalf("expected no error on first lock, got %v", err)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = m.Lock(timeoutCtx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("expected to block for at least 10ms, blocked for %v", elapsed)
	}

	m.Unlock()
}

func TestTryLockReturnsFalseWhenLocked(t *testing.T) {
	m := NewMutex()
	ctx := context.Background()

	err := m.Lock(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if m.TryLock() {
		t.Fatal("expected TryLock to return false when locked")
	}

	m.Unlock()
}

func TestTryLockReturnsTrueWhenUnlocked(t *testing.T) {
	m := NewMutex()

	if !m.TryLock() {
		t.Fatal("expected TryLock to return true when unlocked")
	}

	m.Unlock()
}

func TestTryLockAcquiresLock(t *testing.T) {
	m := NewMutex()

	if !m.TryLock() {
		t.Fatal("expected TryLock to return true")
	}

	if m.TryLock() {
		t.Fatal("expected second TryLock to return false")
	}

	m.Unlock()

	if !m.TryLock() {
		t.Fatal("expected TryLock to return true after unlock")
	}

	m.Unlock()
}

func TestConcurrentMutexAccess(t *testing.T) {
	m := NewMutex()
	ctx := context.Background()
	var counter int64
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := m.Lock(ctx); err != nil {
					t.Errorf("lock error: %v", err)
					return
				}
				atomic.AddInt64(&counter, 1)
				m.Unlock()
			}
		}()
	}

	wg.Wait()

	if counter != 10000 {
		t.Fatalf("expected counter to be 10000, got %d", counter)
	}
}

func TestMutexProtectsCriticalSection(t *testing.T) {
	m := NewMutex()
	ctx := context.Background()
	var sharedValue int
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := m.Lock(ctx); err != nil {
					t.Errorf("lock error: %v", err)
					return
				}
				temp := sharedValue
				time.Sleep(time.Microsecond)
				sharedValue = temp + 1
				m.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if sharedValue != 1000 {
		t.Fatalf("expected sharedValue to be 1000, got %d (race condition detected)", sharedValue)
	}
}

func TestMutexWithContextCancellationDuringContention(t *testing.T) {
	m := NewMutex()
	ctx := context.Background()

	err := m.Lock(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(delay time.Duration) {
			defer wg.Done()
			cancelCtx, cancel := context.WithTimeout(context.Background(), delay)
			defer cancel()
			results <- m.Lock(cancelCtx)
		}(time.Duration(10*(i+1)) * time.Millisecond)
	}

	wg.Wait()
	close(results)

	for err := range results {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", err)
		}
	}

	m.Unlock()
}
