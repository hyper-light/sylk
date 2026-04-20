package cache

import (
	"sync"
	"testing"
)

func TestTinyLFU_AdmitsRepeatAccesses(t *testing.T) {
	tl := NewTinyLFU(64)
	key := []byte("frequent-key")

	for range 5 {
		tl.Increment(key)
	}
	if !tl.AdmitForPromotion(key) {
		t.Errorf("expected AdmitForPromotion=true after 5 increments, got false (estimate=%d)", tl.Estimate(key))
	}
}

func TestTinyLFU_RejectsInfrequent(t *testing.T) {
	tl := NewTinyLFU(64)
	key := []byte("rare-key")
	tl.Increment(key)
	if tl.AdmitForPromotion(key) {
		t.Errorf("expected AdmitForPromotion=false after 1 increment, got true (estimate=%d)", tl.Estimate(key))
	}
}

func TestTinyLFU_EstimateSaturates(t *testing.T) {
	tl := NewTinyLFU(64)
	key := []byte("hot-key")
	for range 100 {
		tl.Increment(key)
	}
	if tl.Estimate(key) != tinylfuMaxCount {
		t.Errorf("estimate after 100 increments = %d, want saturation at %d", tl.Estimate(key), tinylfuMaxCount)
	}
}

func TestTinyLFU_DistinctKeysIndependent(t *testing.T) {
	tl := NewTinyLFU(64)
	hot := []byte("hot")
	cold := []byte("cold")
	for range 10 {
		tl.Increment(hot)
	}
	if tl.Estimate(hot) <= tl.Estimate(cold) {
		t.Errorf("hot estimate (%d) should exceed cold estimate (%d)", tl.Estimate(hot), tl.Estimate(cold))
	}
}

func TestTinyLFU_ConcurrentSafe(t *testing.T) {
	tl := NewTinyLFU(64)
	const goroutines = 16
	const ops = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			key := []byte{byte(idx)}
			for range ops {
				tl.Increment(key)
				_ = tl.Estimate(key)
			}
		}(i)
	}
	wg.Wait()
}

func TestTinyLFU_AgesCounters(t *testing.T) {
	tl := NewTinyLFU(8) // tiny capacity → fast aging
	key := []byte("k")
	for range 20 {
		tl.Increment(key)
	}
	beforeAge := tl.Estimate(key)
	// Drive enough writes to trigger aging.
	for range int(tl.resetThreshold) + 1 {
		tl.Increment([]byte{1, 2, 3})
	}
	afterAge := tl.Estimate(key)
	if afterAge >= beforeAge {
		t.Errorf("aging did not decrement counters: before=%d after=%d", beforeAge, afterAge)
	}
}

func TestRoundUpPowerOfTwo(t *testing.T) {
	cases := map[int]int{
		1:  1,
		2:  2,
		3:  4,
		5:  8,
		17: 32,
	}
	for in, want := range cases {
		if got := roundUpPowerOfTwo(in); got != want {
			t.Errorf("roundUpPowerOfTwo(%d) = %d, want %d", in, got, want)
		}
	}
}
