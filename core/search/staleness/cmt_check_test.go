package staleness

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Mock CMT Hash Provider
// =============================================================================

// testCMTProvider implements CMTHashProvider for testing.
type testCMTProvider struct {
	hash Hash
	size int64
	mu   sync.RWMutex
}

func newTestCMTProvider(hashStr string, size int64) *testCMTProvider {
	return &testCMTProvider{
		hash: makeTestHash(hashStr),
		size: size,
	}
}

func (m *testCMTProvider) RootHash() Hash {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hash
}

func (m *testCMTProvider) Size() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

func (m *testCMTProvider) SetHash(hashStr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hash = makeTestHash(hashStr)
}

func (m *testCMTProvider) SetSize(size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.size = size
}

// makeTestHash creates a Hash from a string for testing.
func makeTestHash(s string) Hash {
	return sha256.Sum256([]byte(s))
}

// =============================================================================
// NewCMTRootCheck Tests
// =============================================================================

func TestNewCMTRootCheck_Success(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, err := NewCMTRootCheck(provider)

	if err != nil {
		t.Fatalf("NewCMTRootCheck() error = %v", err)
	}
	if check == nil {
		t.Fatal("NewCMTRootCheck() returned nil")
	}
}

func TestNewCMTRootCheck_NilCMT(t *testing.T) {
	t.Parallel()

	check, err := NewCMTRootCheck(nil)

	if err != ErrCMTUnavailable {
		t.Errorf("NewCMTRootCheck(nil) error = %v, want %v", err, ErrCMTUnavailable)
	}
	if check != nil {
		t.Errorf("NewCMTRootCheck(nil) returned non-nil check")
	}
}

func TestNewCMTRootCheck_CapturesInitialHash(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	storedHash := check.StoredHash()
	expectedHash := makeTestHash("initial")

	if storedHash != expectedHash {
		t.Errorf("StoredHash() = %x, want %x", storedHash, expectedHash)
	}
}

// =============================================================================
// Check Tests - Fresh Detection
// =============================================================================

func TestCMTRootCheck_Check_Fresh(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("content", 10)
	check, _ := NewCMTRootCheck(provider)

	result, err := check.Check(context.Background())

	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Level != Fresh {
		t.Errorf("Check() Level = %v, want %v", result.Level, Fresh)
	}
	if result.Strategy != CMTRoot {
		t.Errorf("Check() Strategy = %v, want %v", result.Strategy, CMTRoot)
	}
	if result.Confidence != 1.0 {
		t.Errorf("Check() Confidence = %v, want 1.0", result.Confidence)
	}
}

func TestCMTRootCheck_Check_FreshAfterUpdate(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	// Change the provider hash
	provider.SetHash("changed")

	// First check should be stale
	result1, _ := check.Check(context.Background())
	if result1.Level == Fresh {
		t.Errorf("First Check() should be stale")
	}

	// Update stored hash
	check.UpdateStoredHash()

	// Second check should be fresh
	result2, _ := check.Check(context.Background())
	if result2.Level != Fresh {
		t.Errorf("Check() after UpdateStoredHash() Level = %v, want %v", result2.Level, Fresh)
	}
}

// =============================================================================
// Check Tests - Stale Detection
// =============================================================================

func TestCMTRootCheck_Check_Stale(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	// Simulate a change in the CMT
	provider.SetHash("modified")

	result, err := check.Check(context.Background())

	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Level == Fresh {
		t.Errorf("Check() Level = %v, want stale", result.Level)
	}
	if result.Strategy != CMTRoot {
		t.Errorf("Check() Strategy = %v, want %v", result.Strategy, CMTRoot)
	}
	if result.Level.IsFresh() {
		t.Errorf("IsFresh() = true, want false")
	}
}

func TestCMTRootCheck_Check_StaleReturnsModeratelyStale(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	provider.SetHash("different")

	result, _ := check.Check(context.Background())

	if result.Level != ModeratelyStale {
		t.Errorf("Check() Level = %v, want %v", result.Level, ModeratelyStale)
	}
}

// =============================================================================
// Check Tests - Context Cancellation
// =============================================================================

func TestCMTRootCheck_Check_CanceledContext(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("content", 10)
	check, _ := NewCMTRootCheck(provider)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := check.Check(ctx)

	if err != context.Canceled {
		t.Errorf("Check() error = %v, want %v", err, context.Canceled)
	}
	if result != nil {
		t.Errorf("Check() result = %v, want nil", result)
	}
}

func TestCMTRootCheck_Check_DeadlineExceeded(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("content", 10)
	check, _ := NewCMTRootCheck(provider)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	result, err := check.Check(ctx)

	if err != context.DeadlineExceeded {
		t.Errorf("Check() error = %v, want %v", err, context.DeadlineExceeded)
	}
	if result != nil {
		t.Errorf("Check() result = %v, want nil", result)
	}
}

// =============================================================================
// StoredHash and SetStoredHash Tests
// =============================================================================

func TestCMTRootCheck_SetStoredHash(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	newHash := makeTestHash("custom")
	check.SetStoredHash(newHash)

	storedHash := check.StoredHash()
	if storedHash != newHash {
		t.Errorf("StoredHash() = %x, want %x", storedHash, newHash)
	}
}

func TestCMTRootCheck_UpdateStoredHash(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	provider.SetHash("updated")
	check.UpdateStoredHash()

	storedHash := check.StoredHash()
	expectedHash := makeTestHash("updated")

	if storedHash != expectedHash {
		t.Errorf("StoredHash() after update = %x, want %x", storedHash, expectedHash)
	}
}

// =============================================================================
// CurrentHash Tests
// =============================================================================

func TestCMTRootCheck_CurrentHash(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("content", 10)
	check, _ := NewCMTRootCheck(provider)

	currentHash := check.CurrentHash()
	expectedHash := makeTestHash("content")

	if currentHash != expectedHash {
		t.Errorf("CurrentHash() = %x, want %x", currentHash, expectedHash)
	}
}

func TestCMTRootCheck_CurrentHash_ReflectsChanges(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	provider.SetHash("changed")

	currentHash := check.CurrentHash()
	expectedHash := makeTestHash("changed")

	if currentHash != expectedHash {
		t.Errorf("CurrentHash() = %x, want %x", currentHash, expectedHash)
	}
}

// =============================================================================
// IsEmpty Tests
// =============================================================================

func TestCMTRootCheck_IsEmpty_True(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("empty", 0)
	check, _ := NewCMTRootCheck(provider)

	if !check.IsEmpty() {
		t.Errorf("IsEmpty() = false, want true")
	}
}

func TestCMTRootCheck_IsEmpty_False(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("content", 10)
	check, _ := NewCMTRootCheck(provider)

	if check.IsEmpty() {
		t.Errorf("IsEmpty() = true, want false")
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestCMTRootCheck_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = check.Check(context.Background())
		}()
	}

	// Concurrent updates
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			check.SetStoredHash(makeTestHash(string(rune('a' + i%26))))
		}(i)
	}

	wg.Wait()
}

func TestCMTRootCheck_ConcurrentCheckAndUpdate(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			provider.SetHash("version" + string(rune('0'+i%10)))
			check.UpdateStoredHash()
		}
	}()

	// Reader goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = check.Check(context.Background())
			}
		}()
	}

	wg.Wait()
}

// =============================================================================
// Empty CMT Tests
// =============================================================================

func TestCMTRootCheck_EmptyCMT_Fresh(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("", 0)
	check, _ := NewCMTRootCheck(provider)

	result, err := check.Check(context.Background())

	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Level != Fresh {
		t.Errorf("Check() Level = %v, want %v", result.Level, Fresh)
	}
}

func TestCMTRootCheck_EmptyCMT_BecomesPopulated(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("", 0)
	check, _ := NewCMTRootCheck(provider)

	// Empty CMT is fresh
	result1, _ := check.Check(context.Background())
	if result1.Level != Fresh {
		t.Errorf("Empty CMT should be fresh")
	}

	// Populate the CMT
	provider.SetHash("content")
	provider.SetSize(10)

	// Now should be stale
	result2, _ := check.Check(context.Background())
	if result2.Level == Fresh {
		t.Errorf("Populated CMT should be stale after change")
	}
}

// =============================================================================
// StalenessReport Tests
// =============================================================================

func TestCMTRootCheck_Result_HasTimestamp(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("content", 10)
	check, _ := NewCMTRootCheck(provider)

	before := time.Now()
	result, _ := check.Check(context.Background())
	after := time.Now()

	if result.DetectedAt.Before(before) || result.DetectedAt.After(after) {
		t.Errorf("DetectedAt = %v, want between %v and %v", result.DetectedAt, before, after)
	}
}

func TestCMTRootCheck_Result_IsFresh(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("content", 10)
	check, _ := NewCMTRootCheck(provider)

	result, _ := check.Check(context.Background())

	if !result.IsFresh() {
		t.Errorf("IsFresh() = false, want true")
	}
}

func TestCMTRootCheck_Result_IsStale(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	provider.SetHash("changed")
	result, _ := check.Check(context.Background())

	if result.IsFresh() {
		t.Errorf("IsFresh() = true, want false")
	}
	if !result.Level.IsStale() {
		t.Errorf("Level.IsStale() = false, want true")
	}
}

func TestCMTRootCheck_Result_HasDuration(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("content", 10)
	check, _ := NewCMTRootCheck(provider)

	result, _ := check.Check(context.Background())

	if result.Duration < 0 {
		t.Errorf("Duration = %v, want >= 0", result.Duration)
	}
}

func TestCMTRootCheck_Result_NeedsReindex(t *testing.T) {
	t.Parallel()

	provider := newTestCMTProvider("initial", 10)
	check, _ := NewCMTRootCheck(provider)

	// Fresh result should not need reindex
	freshResult, _ := check.Check(context.Background())
	if freshResult.NeedsReindex() {
		t.Errorf("Fresh result should not need reindex")
	}

	// Stale result should need reindex
	provider.SetHash("changed")
	staleResult, _ := check.Check(context.Background())
	if !staleResult.NeedsReindex() {
		t.Errorf("ModeratelyStale result should need reindex")
	}
}

// =============================================================================
// Hash.IsZero Tests
// =============================================================================

func TestHash_IsZero_True(t *testing.T) {
	t.Parallel()

	var h Hash
	if !h.IsZero() {
		t.Errorf("zero hash should return true")
	}
}

func TestHash_IsZero_False(t *testing.T) {
	t.Parallel()

	h := makeTestHash("content")
	if h.IsZero() {
		t.Errorf("non-zero hash should return false")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkCMTRootCheck_Check(b *testing.B) {
	provider := newTestCMTProvider("content", 1000)
	check, _ := NewCMTRootCheck(provider)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = check.Check(ctx)
	}
}

func BenchmarkCMTRootCheck_Check_Stale(b *testing.B) {
	provider := newTestCMTProvider("initial", 1000)
	check, _ := NewCMTRootCheck(provider)
	provider.SetHash("changed")
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = check.Check(ctx)
	}
}

func BenchmarkCMTRootCheck_UpdateStoredHash(b *testing.B) {
	provider := newTestCMTProvider("content", 1000)
	check, _ := NewCMTRootCheck(provider)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check.UpdateStoredHash()
	}
}

func BenchmarkCMTRootCheck_ConcurrentCheck(b *testing.B) {
	provider := newTestCMTProvider("content", 1000)
	check, _ := NewCMTRootCheck(provider)
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = check.Check(ctx)
		}
	})
}
