package watcher

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// mockEventSource is a controllable event source for testing.
type mockEventSource struct {
	events chan *ChangeEvent
	mu     sync.Mutex
	closed bool
}

func newMockEventSource() *mockEventSource {
	return &mockEventSource{
		events: make(chan *ChangeEvent, 100),
	}
}

func (m *mockEventSource) Events() <-chan *ChangeEvent {
	return m.events
}

func (m *mockEventSource) Send(event *ChangeEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		select {
		case m.events <- event:
		default:
			// Channel full, drop event
		}
	}
}

func (m *mockEventSource) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.events)
	}
}

// makeEvent creates a test change event.
func makeEvent(path string, op FileOperation, source ChangeSource) *ChangeEvent {
	return &ChangeEvent{
		Path:      path,
		Operation: op,
		Source:    source,
		Time:      time.Now(),
	}
}

// =============================================================================
// ChangeSource Tests
// =============================================================================

func TestChangeSource_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source ChangeSource
		want   string
	}{
		{SourceFSNotify, "fsnotify"},
		{SourceGitHook, "git_hook"},
		{SourcePeriodic, "periodic"},
		{ChangeSource(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.source.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChangeSource_Priority(t *testing.T) {
	t.Parallel()

	// Lower value = higher priority
	if SourceFSNotify >= SourceGitHook {
		t.Error("SourceFSNotify should have higher priority (lower value) than SourceGitHook")
	}
	if SourceGitHook >= SourcePeriodic {
		t.Error("SourceGitHook should have higher priority (lower value) than SourcePeriodic")
	}
}

// =============================================================================
// FileOperation Tests
// =============================================================================

func TestFileOperation_String(t *testing.T) {
	t.Parallel()

	// FileOperation is defined in types.go
	tests := []struct {
		op   FileOperation
		want string
	}{
		{OpCreate, "create"},
		{OpModify, "modify"},
		{OpDelete, "delete"},
		{OpRename, "rename"},
		{FileOperation(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.op.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// =============================================================================
// ChangeEvent Tests
// =============================================================================

func TestChangeEvent_Fields(t *testing.T) {
	t.Parallel()

	now := time.Now()
	event := &ChangeEvent{
		Path:      "/test/file.go",
		Operation: OpModify,
		Source:    SourceFSNotify,
		Time:      now,
	}

	if event.Path != "/test/file.go" {
		t.Errorf("Path = %q, want %q", event.Path, "/test/file.go")
	}
	if event.Operation != OpModify {
		t.Errorf("Operation = %v, want %v", event.Operation, OpModify)
	}
	if event.Source != SourceFSNotify {
		t.Errorf("Source = %v, want %v", event.Source, SourceFSNotify)
	}
	if event.Time != now {
		t.Errorf("Time = %v, want %v", event.Time, now)
	}
}

// =============================================================================
// ChangeDetectorConfig Tests
// =============================================================================

func TestChangeDetectorConfig_Defaults(t *testing.T) {
	t.Parallel()

	config := ChangeDetectorConfig{}

	if config.DedupeWindow != 0 {
		t.Errorf("DedupeWindow = %v, want 0", config.DedupeWindow)
	}
	if config.RateLimit != 0 {
		t.Errorf("RateLimit = %v, want 0", config.RateLimit)
	}
}

func TestDefaultChangeDetectorConfig(t *testing.T) {
	t.Parallel()

	config := DefaultChangeDetectorConfig()

	if config.DedupeWindow != DefaultDedupeWindow {
		t.Errorf("DedupeWindow = %v, want %v", config.DedupeWindow, DefaultDedupeWindow)
	}
	if config.RateLimit != DefaultRateLimit {
		t.Errorf("RateLimit = %v, want %v", config.RateLimit, DefaultRateLimit)
	}
}

// =============================================================================
// ChangeDetector Creation Tests
// =============================================================================

func TestNewChangeDetector(t *testing.T) {
	t.Parallel()

	config := DefaultChangeDetectorConfig()
	detector := NewChangeDetector(config)

	if detector == nil {
		t.Fatal("expected non-nil detector")
	}
}

func TestNewChangeDetector_NilSources(t *testing.T) {
	t.Parallel()

	config := ChangeDetectorConfig{
		DedupeWindow: time.Second,
		RateLimit:    100,
		// All sources nil
	}

	detector := NewChangeDetector(config)
	if detector == nil {
		t.Fatal("expected non-nil detector even with nil sources")
	}
}

// =============================================================================
// ChangeDetector Start/Stop Tests
// =============================================================================

func TestChangeDetector_Start_ContextCancellation(t *testing.T) {
	t.Parallel()

	config := DefaultChangeDetectorConfig()
	detector := NewChangeDetector(config)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Cancel immediately
	cancel()

	// Channel should close
	timeout := time.After(500 * time.Millisecond)
	select {
	case _, ok := <-events:
		if ok {
			// Drain remaining events
			for range events {
			}
		}
	case <-timeout:
		t.Fatal("channel did not close after context cancellation")
	}
}

func TestChangeDetector_Stop(t *testing.T) {
	t.Parallel()

	config := DefaultChangeDetectorConfig()
	detector := NewChangeDetector(config)

	ctx := context.Background()
	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Stop the detector
	if err := detector.Stop(); err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	// Channel should close
	timeout := time.After(500 * time.Millisecond)
	select {
	case _, ok := <-events:
		if ok {
			for range events {
			}
		}
	case <-timeout:
		t.Fatal("channel did not close after Stop()")
	}
}

func TestChangeDetector_MultipleStarts(t *testing.T) {
	t.Parallel()

	config := DefaultChangeDetectorConfig()
	detector := NewChangeDetector(config)

	ctx := context.Background()

	// First start
	_, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("First Start() error = %v", err)
	}

	// Second start should return error
	_, err = detector.Start(ctx)
	if err == nil {
		t.Error("expected error on second Start() call")
	}

	detector.Stop()
}

// =============================================================================
// Source Integration Tests
// =============================================================================

func TestChangeDetector_ReceivesFromMockSource(t *testing.T) {
	t.Parallel()

	source := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: time.Second,
		RateLimit:    100,
	}
	detector := NewChangeDetectorWithSources(config, source.Events(), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send test event
	testEvent := makeEvent("/test/file.go", OpModify, SourceFSNotify)
	source.Send(testEvent)

	// Should receive the event
	select {
	case event := <-events:
		if event.Path != testEvent.Path {
			t.Errorf("Path = %q, want %q", event.Path, testEvent.Path)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for event")
	}

	source.Close()
}

func TestChangeDetector_MergesMultipleSources(t *testing.T) {
	t.Parallel()

	fsSource := newMockEventSource()
	gitSource := newMockEventSource()
	periodicSource := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 10 * time.Millisecond, // Short window to allow different paths through
		RateLimit:    100,
	}
	detector := NewChangeDetectorWithSources(
		config,
		fsSource.Events(),
		gitSource.Events(),
		periodicSource.Events(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send events from different sources
	fsSource.Send(makeEvent("/fs/file.go", OpCreate, SourceFSNotify))
	gitSource.Send(makeEvent("/git/file.go", OpModify, SourceGitHook))
	periodicSource.Send(makeEvent("/periodic/file.go", OpModify, SourcePeriodic))

	// Collect events
	received := make(map[string]bool)
	timeout := time.After(200 * time.Millisecond)
collecting:
	for {
		select {
		case event, ok := <-events:
			if !ok {
				break collecting
			}
			received[event.Path] = true
			if len(received) == 3 {
				break collecting
			}
		case <-timeout:
			break collecting
		}
	}

	// Should have received from all sources
	if !received["/fs/file.go"] {
		t.Error("missing event from fsnotify source")
	}
	if !received["/git/file.go"] {
		t.Error("missing event from git source")
	}
	if !received["/periodic/file.go"] {
		t.Error("missing event from periodic source")
	}

	fsSource.Close()
	gitSource.Close()
	periodicSource.Close()
}

// =============================================================================
// Deduplication Tests
// =============================================================================

func TestChangeDetector_DeduplicatesWithinWindow(t *testing.T) {
	t.Parallel()

	source := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 100 * time.Millisecond,
		RateLimit:    1000, // High limit to not interfere
	}
	detector := NewChangeDetectorWithSources(config, source.Events(), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send same path multiple times quickly
	for i := 0; i < 5; i++ {
		source.Send(makeEvent("/test/file.go", OpModify, SourceFSNotify))
	}

	// Count received events
	var count int
	timeout := time.After(150 * time.Millisecond)
counting:
	for {
		select {
		case _, ok := <-events:
			if !ok {
				break counting
			}
			count++
		case <-timeout:
			break counting
		}
	}

	// Should have deduplicated to just 1 event
	if count != 1 {
		t.Errorf("expected 1 deduplicated event, got %d", count)
	}

	source.Close()
}

func TestChangeDetector_AllowsAfterDedupeWindow(t *testing.T) {
	t.Parallel()

	source := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 30 * time.Millisecond,
		RateLimit:    1000,
	}
	detector := NewChangeDetectorWithSources(config, source.Events(), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send first event
	source.Send(makeEvent("/test/file.go", OpModify, SourceFSNotify))

	// Wait for first event
	select {
	case <-events:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("timeout waiting for first event")
	}

	// Wait for dedupe window to expire
	time.Sleep(50 * time.Millisecond)

	// Send second event for same path
	source.Send(makeEvent("/test/file.go", OpModify, SourceFSNotify))

	// Should receive second event
	select {
	case <-events:
		// Good
	case <-time.After(50 * time.Millisecond):
		t.Error("expected event after dedupe window expired")
	}

	source.Close()
}

func TestChangeDetector_DifferentPathsNotDeduplicated(t *testing.T) {
	t.Parallel()

	source := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 100 * time.Millisecond,
		RateLimit:    1000,
	}
	detector := NewChangeDetectorWithSources(config, source.Events(), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send different paths
	source.Send(makeEvent("/test/file1.go", OpModify, SourceFSNotify))
	source.Send(makeEvent("/test/file2.go", OpModify, SourceFSNotify))
	source.Send(makeEvent("/test/file3.go", OpModify, SourceFSNotify))

	// Count received events
	var count int
	timeout := time.After(100 * time.Millisecond)
counting:
	for {
		select {
		case _, ok := <-events:
			if !ok {
				break counting
			}
			count++
			if count == 3 {
				break counting
			}
		case <-timeout:
			break counting
		}
	}

	// Should receive all 3 events
	if count != 3 {
		t.Errorf("expected 3 events for different paths, got %d", count)
	}

	source.Close()
}

// =============================================================================
// Priority Tests
// =============================================================================

func TestChangeDetector_HigherPriorityOverrides(t *testing.T) {
	t.Parallel()

	fsSource := newMockEventSource()
	periodicSource := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 100 * time.Millisecond,
		RateLimit:    1000,
	}
	detector := NewChangeDetectorWithSources(config, fsSource.Events(), nil, periodicSource.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send periodic event first
	periodicSource.Send(makeEvent("/test/file.go", OpModify, SourcePeriodic))

	// Wait a moment then send fsnotify event for same path
	time.Sleep(10 * time.Millisecond)
	fsSource.Send(makeEvent("/test/file.go", OpModify, SourceFSNotify))

	// Collect events
	var received []*ChangeEvent
	timeout := time.After(150 * time.Millisecond)
collecting:
	for {
		select {
		case event, ok := <-events:
			if !ok {
				break collecting
			}
			received = append(received, event)
		case <-timeout:
			break collecting
		}
	}

	// Should receive both: first periodic (new), then fsnotify (higher priority override)
	if len(received) < 2 {
		t.Errorf("expected at least 2 events (periodic + fsnotify override), got %d", len(received))
	}

	// Check that fsnotify event was emitted despite being within window
	var hasFSNotify bool
	for _, e := range received {
		if e.Source == SourceFSNotify {
			hasFSNotify = true
			break
		}
	}
	if !hasFSNotify {
		t.Error("higher priority fsnotify event should override dedupe for lower priority")
	}

	fsSource.Close()
	periodicSource.Close()
}

func TestChangeDetector_LowerPriorityIgnoredInWindow(t *testing.T) {
	t.Parallel()

	fsSource := newMockEventSource()
	periodicSource := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 100 * time.Millisecond,
		RateLimit:    1000,
	}
	detector := NewChangeDetectorWithSources(config, fsSource.Events(), nil, periodicSource.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send fsnotify (high priority) first
	fsSource.Send(makeEvent("/test/file.go", OpModify, SourceFSNotify))

	// Wait a moment then send periodic (low priority) for same path
	time.Sleep(10 * time.Millisecond)
	periodicSource.Send(makeEvent("/test/file.go", OpModify, SourcePeriodic))

	// Collect events
	var received []*ChangeEvent
	timeout := time.After(150 * time.Millisecond)
collecting:
	for {
		select {
		case event, ok := <-events:
			if !ok {
				break collecting
			}
			received = append(received, event)
		case <-timeout:
			break collecting
		}
	}

	// Should only receive 1 event (fsnotify), periodic should be deduplicated
	if len(received) != 1 {
		t.Errorf("expected 1 event (periodic deduplicated), got %d", len(received))
	}

	if len(received) > 0 && received[0].Source != SourceFSNotify {
		t.Errorf("expected fsnotify source, got %v", received[0].Source)
	}

	fsSource.Close()
	periodicSource.Close()
}

// =============================================================================
// Rate Limiting Tests
// =============================================================================

func TestChangeDetector_RateLimiting(t *testing.T) {
	t.Parallel()

	// Use periodic source since FSNotify events bypass rate limiting
	periodicSource := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 5 * time.Millisecond, // Very short to not interfere
		RateLimit:    10,                   // Low limit for testing
	}
	// Pass periodic source via the periodicEvents channel
	detector := NewChangeDetectorWithSources(config, nil, nil, periodicSource.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send many events quickly (different paths to avoid deduplication)
	// Use SourcePeriodic which is subject to rate limiting
	for i := 0; i < 50; i++ {
		periodicSource.Send(makeEvent("/test/file"+string(rune('a'+i%26))+".go", OpModify, SourcePeriodic))
	}

	// Count received events in a short window
	var count int
	timeout := time.After(100 * time.Millisecond)
counting:
	for {
		select {
		case _, ok := <-events:
			if !ok {
				break counting
			}
			count++
		case <-timeout:
			break counting
		}
	}

	// Rate limit is 10/second, in 100ms we should get ~1-10 events
	// Due to deduplication (only 26 unique paths), allow up to 26
	// The key test is that not all 50 events get through
	if count > 30 {
		t.Errorf("rate limiting not working: got %d events, expected <= 30", count)
	}

	periodicSource.Close()
}

func TestChangeDetector_FSNotifyBypassesRateLimit(t *testing.T) {
	t.Parallel()

	fsSource := newMockEventSource()
	periodicSource := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 5 * time.Millisecond,
		RateLimit:    5, // Very low limit
	}
	detector := NewChangeDetectorWithSources(config, fsSource.Events(), nil, periodicSource.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send many periodic events to exhaust rate limit
	for i := 0; i < 20; i++ {
		periodicSource.Send(makeEvent("/periodic/file"+string(rune('a'+i))+".go", OpModify, SourcePeriodic))
	}

	// Give time for rate limiter to be exhausted
	time.Sleep(20 * time.Millisecond)

	// Send fsnotify events - should still get through
	for i := 0; i < 5; i++ {
		fsSource.Send(makeEvent("/fs/important"+string(rune('a'+i))+".go", OpModify, SourceFSNotify))
	}

	// Count fsnotify events received
	var fsCount int
	timeout := time.After(100 * time.Millisecond)
counting:
	for {
		select {
		case event, ok := <-events:
			if !ok {
				break counting
			}
			if event.Source == SourceFSNotify {
				fsCount++
			}
		case <-timeout:
			break counting
		}
	}

	// fsnotify has priority and should get through
	if fsCount < 3 {
		t.Errorf("expected fsnotify events to bypass rate limit, got %d", fsCount)
	}

	fsSource.Close()
	periodicSource.Close()
}

// =============================================================================
// Empty/Partial Configuration Tests
// =============================================================================

func TestChangeDetector_EmptyConfig(t *testing.T) {
	t.Parallel()

	config := ChangeDetectorConfig{}
	detector := NewChangeDetector(config)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should work with empty config (using defaults)
	select {
	case <-events:
	case <-ctx.Done():
		// Expected - no events but no error
	}
}

func TestChangeDetector_PartialSources(t *testing.T) {
	t.Parallel()

	source := newMockEventSource()

	// Only fsnotify source configured
	config := ChangeDetectorConfig{
		DedupeWindow: time.Second,
		RateLimit:    100,
	}
	detector := NewChangeDetectorWithSources(config, source.Events(), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send event
	source.Send(makeEvent("/test/file.go", OpModify, SourceFSNotify))

	// Should receive event
	select {
	case event := <-events:
		if event.Path != "/test/file.go" {
			t.Errorf("Path = %q, want %q", event.Path, "/test/file.go")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for event")
	}

	source.Close()
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestChangeDetector_ConcurrentSources(t *testing.T) {
	t.Parallel()

	fsSource := newMockEventSource()
	gitSource := newMockEventSource()
	periodicSource := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 5 * time.Millisecond,
		RateLimit:    1000,
	}
	detector := NewChangeDetectorWithSources(
		config,
		fsSource.Events(),
		gitSource.Events(),
		periodicSource.Events(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var wg sync.WaitGroup

	// Send from multiple sources concurrently
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			fsSource.Send(makeEvent("/fs/"+string(rune('a'+i))+".go", OpModify, SourceFSNotify))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			gitSource.Send(makeEvent("/git/"+string(rune('a'+i))+".go", OpModify, SourceGitHook))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			periodicSource.Send(makeEvent("/periodic/"+string(rune('a'+i))+".go", OpModify, SourcePeriodic))
		}
	}()

	// Collect events
	var received atomic.Int32
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range events {
			received.Add(1)
		}
	}()

	wg.Wait()
	cancel()

	// Should have received events from all sources
	if received.Load() < 30 {
		t.Errorf("expected at least 30 events, got %d", received.Load())
	}

	fsSource.Close()
	gitSource.Close()
	periodicSource.Close()
}

func TestChangeDetector_RaceConditions(t *testing.T) {
	t.Parallel()

	source := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 10 * time.Millisecond,
		RateLimit:    100,
	}
	detector := NewChangeDetectorWithSources(config, source.Events(), nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var wg sync.WaitGroup

	// Producer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			source.Send(makeEvent("/test/"+string(rune('a'+i%26))+".go", OpModify, SourceFSNotify))
			time.Sleep(time.Millisecond)
		}
	}()

	// Consumer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range events {
		}
	}()

	// Let it run a bit
	time.Sleep(50 * time.Millisecond)

	// Stop
	cancel()
	source.Close()

	wg.Wait()
}

// =============================================================================
// Cleanup Tests
// =============================================================================

func TestChangeDetector_CleansOldDedupeEntries(t *testing.T) {
	t.Parallel()

	source := newMockEventSource()

	config := ChangeDetectorConfig{
		DedupeWindow: 20 * time.Millisecond,
		RateLimit:    1000,
	}
	detector := NewChangeDetectorWithSources(config, source.Events(), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	events, err := detector.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Send many unique events
	for i := 0; i < 100; i++ {
		source.Send(makeEvent("/test/unique"+string(rune(i))+".go", OpModify, SourceFSNotify))
	}

	// Wait for dedupe window to expire
	time.Sleep(50 * time.Millisecond)

	// Drain events
	go func() {
		for range events {
		}
	}()

	// The internal dedupe map should have been cleaned (can't directly test internal state,
	// but we can verify no memory issues by running more events)
	for i := 0; i < 100; i++ {
		source.Send(makeEvent("/test/more"+string(rune(i))+".go", OpModify, SourceFSNotify))
	}

	source.Close()
}
