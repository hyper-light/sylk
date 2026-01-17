package errors

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkCircuitBreaker_Allow_Closed(b *testing.B) {
	cb := NewCircuitBreaker("bench", DefaultCircuitBreakerConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.Allow()
	}
}

func BenchmarkCircuitBreaker_Allow_Closed_Parallel(b *testing.B) {
	cb := NewCircuitBreaker("bench", DefaultCircuitBreakerConfig())
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cb.Allow()
		}
	})
}

func BenchmarkCircuitBreaker_RecordResult_Success(b *testing.B) {
	cb := NewCircuitBreaker("bench", DefaultCircuitBreakerConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.RecordResult(true)
	}
}

func BenchmarkCircuitBreaker_RecordResult_Parallel(b *testing.B) {
	cb := NewCircuitBreaker("bench", DefaultCircuitBreakerConfig())
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		success := true
		for pb.Next() {
			cb.RecordResult(success)
			success = !success
		}
	})
}

func BenchmarkCircuitBreaker_State(b *testing.B) {
	cb := NewCircuitBreaker("bench", DefaultCircuitBreakerConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.State()
	}
}

func BenchmarkCircuitBreaker_State_Parallel(b *testing.B) {
	cb := NewCircuitBreaker("bench", DefaultCircuitBreakerConfig())
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = cb.State()
		}
	})
}

func BenchmarkCircuitRegistry_Get_Existing(b *testing.B) {
	r := NewCircuitRegistry()
	r.Get("test-resource")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Get("test-resource")
	}
}

func BenchmarkCircuitRegistry_Get_Existing_Parallel(b *testing.B) {
	r := NewCircuitRegistry()
	r.Get("test-resource")
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r.Get("test-resource")
		}
	})
}

func BenchmarkCircuitRegistry_Get_New(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := NewCircuitRegistry()
		r.Get("test-resource")
	}
}

func BenchmarkTransientTracker_Record(b *testing.B) {
	tracker := NewTransientTracker(DefaultTransientTrackerConfig())
	err := ErrTemporaryFailure
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.Record(err)
	}
}

func BenchmarkTransientTracker_Record_Parallel(b *testing.B) {
	tracker := NewTransientTracker(DefaultTransientTrackerConfig())
	err := ErrTemporaryFailure
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tracker.Record(err)
		}
	})
}

func BenchmarkWorkaroundBudget_CanSpend(b *testing.B) {
	wb := NewWorkaroundBudget(DefaultWorkaroundBudgetConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.CanSpend(100, TierTransient)
	}
}

func BenchmarkWorkaroundBudget_CanSpend_Parallel(b *testing.B) {
	wb := NewWorkaroundBudget(DefaultWorkaroundBudgetConfig())
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			wb.CanSpend(100, TierTransient)
		}
	})
}

func BenchmarkWorkaroundBudget_Spend(b *testing.B) {
	wb := NewWorkaroundBudget(DefaultWorkaroundBudgetConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.Spend(1)
	}
}

func BenchmarkErrorClassifier_Classify_RateLimit(b *testing.B) {
	c := NewErrorClassifier()
	err := ErrRateLimited
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Classify(err)
	}
}

func BenchmarkErrorClassifier_Classify_Unknown(b *testing.B) {
	c := NewErrorClassifier()
	err := &TieredError{Message: "some random error"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Classify(err)
	}
}

func BenchmarkErrorClassifier_Classify_Parallel(b *testing.B) {
	c := NewErrorClassifier()
	err := ErrRateLimited
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Classify(err)
		}
	})
}

func BenchmarkRetryExecutor_Execute_NoRetry(b *testing.B) {
	e := NewRetryExecutor(nil)
	fn := func() error { return nil }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.Execute(nil, TierTransient, fn)
	}
}

func BenchmarkMutexCounter(b *testing.B) {
	var mu sync.Mutex
	var counter int
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mu.Lock()
		counter++
		mu.Unlock()
	}
	_ = counter
}

func BenchmarkMutexCounter_Parallel(b *testing.B) {
	var mu sync.Mutex
	var counter int
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.Lock()
			counter++
			mu.Unlock()
		}
	})
	_ = counter
}

func BenchmarkAtomicCounter(b *testing.B) {
	var counter int64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		atomic.AddInt64(&counter, 1)
	}
}

func BenchmarkAtomicCounter_Parallel(b *testing.B) {
	var counter int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			atomic.AddInt64(&counter, 1)
		}
	})
}

func BenchmarkRWMutex_ReadHeavy(b *testing.B) {
	var mu sync.RWMutex
	var value int
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.RLock()
			_ = value
			mu.RUnlock()
		}
	})
}

func BenchmarkMutex_ReadHeavy(b *testing.B) {
	var mu sync.Mutex
	var value int
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.Lock()
			_ = value
			mu.Unlock()
		}
	})
}

func BenchmarkTimeNow(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = time.Now()
	}
}

func BenchmarkTimeSince(b *testing.B) {
	start := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = time.Since(start)
	}
}
