package recovery

import (
	"sync"
	"testing"
)

func TestRingBuffer_BasicPushAndLast(t *testing.T) {
	rb := NewRingBuffer[int](5)

	rb.Push(1)
	rb.Push(2)
	rb.Push(3)

	got := rb.Last(3)
	want := []int{1, 2, 3}

	if len(got) != len(want) {
		t.Fatalf("Last(3) len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Last(3)[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestRingBuffer_OverwriteOldest(t *testing.T) {
	rb := NewRingBuffer[int](3)

	rb.Push(1)
	rb.Push(2)
	rb.Push(3)
	rb.Push(4)
	rb.Push(5)

	got := rb.All()
	want := []int{3, 4, 5}

	if len(got) != len(want) {
		t.Fatalf("All() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("All()[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestRingBuffer_LastMoreThanCount(t *testing.T) {
	rb := NewRingBuffer[int](10)

	rb.Push(1)
	rb.Push(2)

	got := rb.Last(100)
	if len(got) != 2 {
		t.Fatalf("Last(100) len = %d, want 2", len(got))
	}
}

func TestRingBuffer_LastZeroOrNegative(t *testing.T) {
	rb := NewRingBuffer[int](5)
	rb.Push(1)

	if got := rb.Last(0); got != nil {
		t.Errorf("Last(0) = %v, want nil", got)
	}
	if got := rb.Last(-1); got != nil {
		t.Errorf("Last(-1) = %v, want nil", got)
	}
}

func TestRingBuffer_EmptyBuffer(t *testing.T) {
	rb := NewRingBuffer[int](5)

	if got := rb.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0", got)
	}
	if got := rb.Last(1); got != nil {
		t.Errorf("Last(1) on empty = %v, want nil", got)
	}
	if got := rb.All(); got != nil {
		t.Errorf("All() on empty = %v, want nil", got)
	}
}

func TestRingBuffer_Clear(t *testing.T) {
	rb := NewRingBuffer[int](5)
	rb.Push(1)
	rb.Push(2)
	rb.Push(3)

	rb.Clear()

	if got := rb.Len(); got != 0 {
		t.Errorf("Len() after Clear() = %d, want 0", got)
	}
	if got := rb.All(); got != nil {
		t.Errorf("All() after Clear() = %v, want nil", got)
	}
}

func TestRingBuffer_Cap(t *testing.T) {
	rb := NewRingBuffer[int](42)
	if got := rb.Cap(); got != 42 {
		t.Errorf("Cap() = %d, want 42", got)
	}
}

func TestRingBuffer_DefaultCapacity(t *testing.T) {
	rb := NewRingBuffer[int](0)
	if got := rb.Cap(); got != 100 {
		t.Errorf("Cap() with 0 init = %d, want 100", got)
	}

	rb2 := NewRingBuffer[int](-5)
	if got := rb2.Cap(); got != 100 {
		t.Errorf("Cap() with -5 init = %d, want 100", got)
	}
}

func TestRingBuffer_ConcurrentAccess(t *testing.T) {
	rb := NewRingBuffer[int](100)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rb.Push(base*100 + j)
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = rb.Last(10)
				_ = rb.Len()
				_ = rb.All()
			}
		}()
	}

	wg.Wait()

	if rb.Len() != 100 {
		t.Errorf("Len() = %d, want 100", rb.Len())
	}
}

func TestRingBuffer_WrapAround(t *testing.T) {
	rb := NewRingBuffer[int](4)

	for i := 1; i <= 10; i++ {
		rb.Push(i)
	}

	got := rb.All()
	want := []int{7, 8, 9, 10}

	if len(got) != len(want) {
		t.Fatalf("All() after wrap len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("All()[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}
