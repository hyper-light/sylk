package concurrency

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// W4N2 Tests: Legacy Unbounded Mode Deprecation and Minimum Bound Enforcement
//
// These tests verify that:
// 1. Legacy mode (MaxOverflowSize=0) now enforces a default bound
// 2. Bounded mode properly enforces limits
// 3. Graceful handling when bounds are exceeded
// 4. Race conditions during concurrent push operations
// 5. Edge cases at and around the boundary

// =============================================================================
// UnboundedChannel Tests
// =============================================================================

// TestW4N2_UnboundedChannel_BoundedModeEnforcesLimit verifies that when
// MaxOverflowSize is explicitly set, the bound is enforced.
func TestW4N2_UnboundedChannel_BoundedModeEnforcesLimit(t *testing.T) {
	const boundLimit = 100
	const channelCap = 10

	cfg := UnboundedChannelConfig{
		ChannelCapacity:    channelCap,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest, // Reject new messages when full
	}

	uc := NewUnboundedChannelWithConfig[int](cfg)
	defer uc.Close()

	// Fill channel buffer first
	for i := 0; i < channelCap; i++ {
		if err := uc.Send(i); err != nil {
			t.Fatalf("send to channel buffer failed at %d: %v", i, err)
		}
	}

	// Fill overflow buffer up to the limit
	for i := 0; i < boundLimit; i++ {
		if err := uc.Send(channelCap + i); err != nil {
			t.Fatalf("send to overflow failed at %d: %v", i, err)
		}
	}

	// Verify overflow is at capacity
	stats := uc.Stats()
	if stats.OverflowLen != boundLimit {
		t.Errorf("expected overflow len %d, got %d", boundLimit, stats.OverflowLen)
	}
	if stats.OverflowCapacity != boundLimit {
		t.Errorf("expected overflow capacity %d, got %d", boundLimit, stats.OverflowCapacity)
	}

	// Next send should fail with ErrOverflowFull
	err := uc.Send(999)
	if err != ErrOverflowFull {
		t.Errorf("expected ErrOverflowFull, got %v", err)
	}

	// Verify dropped count increased
	stats = uc.Stats()
	if stats.DroppedCount != 1 {
		t.Errorf("expected dropped count 1, got %d", stats.DroppedCount)
	}
}

// TestW4N2_UnboundedChannel_LegacyModeEnforcesDefault verifies that when
// MaxOverflowSize=0 (legacy mode), the default bound is now enforced.
func TestW4N2_UnboundedChannel_LegacyModeEnforcesDefault(t *testing.T) {
	cfg := UnboundedChannelConfig{
		ChannelCapacity:    10,
		MaxOverflowSize:    0, // Legacy mode - should be upgraded to default
		OverflowDropPolicy: DropNewest,
	}

	uc := NewUnboundedChannelWithConfig[int](cfg)
	defer uc.Close()

	// Verify that the channel now uses bounded overflow
	stats := uc.Stats()
	if !stats.IsBounded {
		t.Error("expected channel to be bounded after legacy mode upgrade")
	}

	// The capacity should be the default (10000)
	if stats.OverflowCapacity != defaultMaxOverflowSize {
		t.Errorf("expected overflow capacity %d, got %d",
			defaultMaxOverflowSize, stats.OverflowCapacity)
	}
}

// TestW4N2_UnboundedChannel_GracefulHandlingWhenBoundExceeded verifies
// graceful behavior when attempting to exceed the bound.
func TestW4N2_UnboundedChannel_GracefulHandlingWhenBoundExceeded(t *testing.T) {
	const boundLimit = 50
	const channelCap = 5
	const totalSends = 200 // Much more than bound

	cfg := UnboundedChannelConfig{
		ChannelCapacity:    channelCap,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	uc := NewUnboundedChannelWithConfig[int](cfg)
	defer uc.Close()

	var errCount int
	for i := 0; i < totalSends; i++ {
		if err := uc.Send(i); err == ErrOverflowFull {
			errCount++
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Should have dropped messages beyond channel+overflow capacity
	expectedDrops := totalSends - channelCap - boundLimit
	if errCount != expectedDrops {
		t.Errorf("expected %d dropped messages, got %d", expectedDrops, errCount)
	}

	// Verify total messages in channel
	stats := uc.Stats()
	totalMessages := stats.ChannelLen + stats.OverflowLen
	expectedTotal := channelCap + boundLimit
	if totalMessages != expectedTotal {
		t.Errorf("expected total %d messages, got %d", expectedTotal, totalMessages)
	}

	// Channel should still be operational
	msg, ok := uc.TryReceive()
	if !ok {
		t.Fatal("receive after overflow exceeded failed: no message available")
	}
	if msg != 0 { // First message should be 0
		t.Errorf("expected first message 0, got %d", msg)
	}
}

// TestW4N2_UnboundedChannel_ConcurrentPushDuringBoundCheck verifies no race
// conditions occur during concurrent push operations near the bound.
func TestW4N2_UnboundedChannel_ConcurrentPushDuringBoundCheck(t *testing.T) {
	const boundLimit = 100
	const numGoroutines = 20
	const sendsPerGoroutine = 50

	cfg := UnboundedChannelConfig{
		ChannelCapacity:    10,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	uc := NewUnboundedChannelWithConfig[int](cfg)
	defer uc.Close()

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failCount atomic.Int64

	// Start many goroutines sending concurrently
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < sendsPerGoroutine; i++ {
				msg := goroutineID*sendsPerGoroutine + i
				if err := uc.Send(msg); err == nil {
					successCount.Add(1)
				} else if err == ErrOverflowFull {
					failCount.Add(1)
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(g)
	}

	wg.Wait()

	totalAttempts := int64(numGoroutines * sendsPerGoroutine)
	actualSuccess := successCount.Load()
	actualFail := failCount.Load()

	if actualSuccess+actualFail != totalAttempts {
		t.Errorf("message count mismatch: success=%d, fail=%d, total=%d",
			actualSuccess, actualFail, totalAttempts)
	}

	stats := uc.Stats()
	totalInChannel := int64(stats.ChannelLen + stats.OverflowLen)

	if totalInChannel != actualSuccess {
		t.Errorf("expected %d messages in channel, got %d", actualSuccess, totalInChannel)
	}
}

// TestW4N2_UnboundedChannel_ExactlyAtBound verifies behavior when exactly at
// the bound.
func TestW4N2_UnboundedChannel_ExactlyAtBound(t *testing.T) {
	const boundLimit = 20
	const channelCap = 5

	cfg := UnboundedChannelConfig{
		ChannelCapacity:    channelCap,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	uc := NewUnboundedChannelWithConfig[int](cfg)
	defer uc.Close()

	// Fill exactly to capacity
	totalCapacity := channelCap + boundLimit
	for i := 0; i < totalCapacity; i++ {
		if err := uc.Send(i); err != nil {
			t.Fatalf("send %d should succeed, got: %v", i, err)
		}
	}

	stats := uc.Stats()
	if stats.ChannelLen != channelCap {
		t.Errorf("expected channel len %d, got %d", channelCap, stats.ChannelLen)
	}
	if stats.OverflowLen != boundLimit {
		t.Errorf("expected overflow len %d, got %d", boundLimit, stats.OverflowLen)
	}
	if stats.DroppedCount != 0 {
		t.Errorf("expected no drops when exactly at bound, got %d", stats.DroppedCount)
	}
}

// TestW4N2_UnboundedChannel_OneOverBound verifies behavior when one over the
// bound.
func TestW4N2_UnboundedChannel_OneOverBound(t *testing.T) {
	const boundLimit = 20
	const channelCap = 5

	cfg := UnboundedChannelConfig{
		ChannelCapacity:    channelCap,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	uc := NewUnboundedChannelWithConfig[int](cfg)
	defer uc.Close()

	// Fill exactly to capacity
	totalCapacity := channelCap + boundLimit
	for i := 0; i < totalCapacity; i++ {
		if err := uc.Send(i); err != nil {
			t.Fatalf("send %d should succeed, got: %v", i, err)
		}
	}

	// One more should fail
	err := uc.Send(totalCapacity)
	if err != ErrOverflowFull {
		t.Errorf("expected ErrOverflowFull for message at bound+1, got %v", err)
	}

	stats := uc.Stats()
	if stats.DroppedCount != 1 {
		t.Errorf("expected 1 drop for one over bound, got %d", stats.DroppedCount)
	}
}

// =============================================================================
// AdaptiveChannel Tests
// =============================================================================

// TestW4N2_AdaptiveChannel_BoundedModeEnforcesLimit verifies that when
// MaxOverflowSize is explicitly set, the bound is enforced.
func TestW4N2_AdaptiveChannel_BoundedModeEnforcesLimit(t *testing.T) {
	const boundLimit = 50
	const channelSize = 10

	cfg := AdaptiveChannelConfig{
		MinSize:            channelSize,
		MaxSize:            channelSize,
		InitialSize:        channelSize,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	ac := NewAdaptiveChannel[int](cfg)
	defer ac.Close()

	// Fill channel buffer first
	for i := 0; i < channelSize; i++ {
		if err := ac.Send(i); err != nil {
			t.Fatalf("send to channel buffer failed at %d: %v", i, err)
		}
	}

	// Fill overflow buffer up to the limit
	for i := 0; i < boundLimit; i++ {
		if err := ac.Send(channelSize + i); err != nil {
			t.Fatalf("send to overflow failed at %d: %v", i, err)
		}
	}

	// Verify overflow is at capacity
	stats := ac.Stats()
	if stats.OverflowCount != boundLimit {
		t.Errorf("expected overflow count %d, got %d", boundLimit, stats.OverflowCount)
	}

	// Next send should fail with ErrOverflowFull
	err := ac.Send(999)
	if err != ErrOverflowFull {
		t.Errorf("expected ErrOverflowFull, got %v", err)
	}
}

// TestW4N2_AdaptiveChannel_LegacyModeEnforcesDefault verifies that when
// MaxOverflowSize=0 (legacy mode), the default bound is now enforced.
func TestW4N2_AdaptiveChannel_LegacyModeEnforcesDefault(t *testing.T) {
	cfg := AdaptiveChannelConfig{
		MinSize:            16,
		MaxSize:            64,
		InitialSize:        16,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    0, // Legacy mode - should be upgraded to default
		OverflowDropPolicy: DropNewest,
	}

	ac := NewAdaptiveChannel[int](cfg)
	defer ac.Close()

	// Verify that the channel now uses bounded overflow
	stats := ac.Stats()
	if stats.OverflowCapacity != DefaultAdaptiveMaxOverflow {
		t.Errorf("expected overflow capacity %d, got %d",
			DefaultAdaptiveMaxOverflow, stats.OverflowCapacity)
	}
}

// TestW4N2_AdaptiveChannel_GracefulHandlingWhenBoundExceeded verifies
// graceful behavior when attempting to exceed the bound.
func TestW4N2_AdaptiveChannel_GracefulHandlingWhenBoundExceeded(t *testing.T) {
	const boundLimit = 30
	const channelSize = 5
	const totalSends = 100

	cfg := AdaptiveChannelConfig{
		MinSize:            channelSize,
		MaxSize:            channelSize,
		InitialSize:        channelSize,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	ac := NewAdaptiveChannel[int](cfg)
	defer ac.Close()

	var errCount int
	for i := 0; i < totalSends; i++ {
		if err := ac.Send(i); err == ErrOverflowFull {
			errCount++
		} else if err != nil && err != ErrSendTimeout {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Should have dropped messages beyond channel+overflow capacity
	expectedDrops := totalSends - channelSize - boundLimit
	if errCount != expectedDrops {
		t.Errorf("expected %d dropped messages, got %d", expectedDrops, errCount)
	}

	// Channel should still be operational
	msg, ok := ac.TryReceive()
	if !ok {
		t.Fatal("receive after overflow exceeded failed: no message available")
	}
	if msg != 0 {
		t.Errorf("expected first message 0, got %d", msg)
	}
}

// TestW4N2_AdaptiveChannel_ConcurrentPushDuringBoundCheck verifies no race
// conditions occur during concurrent push operations near the bound.
func TestW4N2_AdaptiveChannel_ConcurrentPushDuringBoundCheck(t *testing.T) {
	const boundLimit = 100
	const numGoroutines = 20
	const sendsPerGoroutine = 50

	cfg := AdaptiveChannelConfig{
		MinSize:            10,
		MaxSize:            10,
		InitialSize:        10,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	ac := NewAdaptiveChannel[int](cfg)
	defer ac.Close()

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failCount atomic.Int64

	// Start many goroutines sending concurrently
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < sendsPerGoroutine; i++ {
				msg := goroutineID*sendsPerGoroutine + i
				err := ac.Send(msg)
				if err == nil {
					successCount.Add(1)
				} else if err == ErrOverflowFull || err == ErrSendTimeout {
					failCount.Add(1)
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(g)
	}

	wg.Wait()

	totalAttempts := int64(numGoroutines * sendsPerGoroutine)
	actualSuccess := successCount.Load()
	actualFail := failCount.Load()

	if actualSuccess+actualFail != totalAttempts {
		t.Errorf("message count mismatch: success=%d, fail=%d, total=%d",
			actualSuccess, actualFail, totalAttempts)
	}

	stats := ac.Stats()
	totalInChannel := int64(stats.MessageCount + stats.OverflowCount)

	if totalInChannel != actualSuccess {
		t.Errorf("expected %d messages in channel, got %d", actualSuccess, totalInChannel)
	}
}

// TestW4N2_AdaptiveChannel_ExactlyAtBound verifies behavior when exactly at
// the bound.
func TestW4N2_AdaptiveChannel_ExactlyAtBound(t *testing.T) {
	const boundLimit = 20
	const channelSize = 5

	cfg := AdaptiveChannelConfig{
		MinSize:            channelSize,
		MaxSize:            channelSize,
		InitialSize:        channelSize,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	ac := NewAdaptiveChannel[int](cfg)
	defer ac.Close()

	// Fill exactly to capacity
	totalCapacity := channelSize + boundLimit
	for i := 0; i < totalCapacity; i++ {
		if err := ac.Send(i); err != nil {
			t.Fatalf("send %d should succeed, got: %v", i, err)
		}
	}

	stats := ac.Stats()
	if stats.MessageCount != channelSize {
		t.Errorf("expected channel len %d, got %d", channelSize, stats.MessageCount)
	}
	if stats.OverflowCount != boundLimit {
		t.Errorf("expected overflow len %d, got %d", boundLimit, stats.OverflowCount)
	}
	if stats.DroppedCount != 0 {
		t.Errorf("expected no drops when exactly at bound, got %d", stats.DroppedCount)
	}
}

// TestW4N2_AdaptiveChannel_OneOverBound verifies behavior when one over the
// bound.
func TestW4N2_AdaptiveChannel_OneOverBound(t *testing.T) {
	const boundLimit = 20
	const channelSize = 5

	cfg := AdaptiveChannelConfig{
		MinSize:            channelSize,
		MaxSize:            channelSize,
		InitialSize:        channelSize,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropNewest,
	}

	ac := NewAdaptiveChannel[int](cfg)
	defer ac.Close()

	// Fill exactly to capacity
	totalCapacity := channelSize + boundLimit
	for i := 0; i < totalCapacity; i++ {
		if err := ac.Send(i); err != nil {
			t.Fatalf("send %d should succeed, got: %v", i, err)
		}
	}

	// One more should fail
	err := ac.Send(totalCapacity)
	if err != ErrOverflowFull {
		t.Errorf("expected ErrOverflowFull for message at bound+1, got %v", err)
	}

	stats := ac.Stats()
	if stats.DroppedCount != 1 {
		t.Errorf("expected 1 drop for one over bound, got %d", stats.DroppedCount)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

// TestW4N2_Integration_BothChannelsUseDefaultBound ensures both channel types
// behave consistently when legacy mode is used.
func TestW4N2_Integration_BothChannelsUseDefaultBound(t *testing.T) {
	// Create UnboundedChannel with legacy config
	ucCfg := UnboundedChannelConfig{
		ChannelCapacity:    10,
		MaxOverflowSize:    0, // Legacy
		OverflowDropPolicy: DropNewest,
	}
	uc := NewUnboundedChannelWithConfig[int](ucCfg)
	defer uc.Close()

	// Create AdaptiveChannel with legacy config
	acCfg := AdaptiveChannelConfig{
		MinSize:            10,
		MaxSize:            10,
		InitialSize:        10,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    0, // Legacy
		OverflowDropPolicy: DropNewest,
	}
	ac := NewAdaptiveChannel[int](acCfg)
	defer ac.Close()

	// Both should have bounded overflow now
	ucStats := uc.Stats()
	acStats := ac.Stats()

	if !ucStats.IsBounded {
		t.Error("UnboundedChannel should be bounded after legacy mode upgrade")
	}

	if ucStats.OverflowCapacity != defaultMaxOverflowSize {
		t.Errorf("UnboundedChannel: expected capacity %d, got %d",
			defaultMaxOverflowSize, ucStats.OverflowCapacity)
	}

	if acStats.OverflowCapacity != DefaultAdaptiveMaxOverflow {
		t.Errorf("AdaptiveChannel: expected capacity %d, got %d",
			DefaultAdaptiveMaxOverflow, acStats.OverflowCapacity)
	}
}

// TestW4N2_DropOldestPolicy verifies that DropOldest policy works correctly
// with the bounded overflow.
func TestW4N2_DropOldestPolicy(t *testing.T) {
	const boundLimit = 5
	const channelCap = 2

	cfg := UnboundedChannelConfig{
		ChannelCapacity:    channelCap,
		MaxOverflowSize:    boundLimit,
		OverflowDropPolicy: DropOldest, // Evict oldest when full
	}

	uc := NewUnboundedChannelWithConfig[int](cfg)
	defer uc.Close()

	// Fill to capacity
	totalCapacity := channelCap + boundLimit
	for i := 0; i < totalCapacity; i++ {
		if err := uc.Send(i); err != nil {
			t.Fatalf("send %d should succeed: %v", i, err)
		}
	}

	// Send one more - should succeed (oldest dropped)
	if err := uc.Send(999); err != nil {
		t.Errorf("with DropOldest, send should succeed: %v", err)
	}

	// Verify dropped count increased
	stats := uc.Stats()
	if stats.DroppedCount != 1 {
		t.Errorf("expected 1 drop with DropOldest, got %d", stats.DroppedCount)
	}
}
