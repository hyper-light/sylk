package concurrency

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// PF.6.4 - Concurrency Stress Test Suite
// ============================================================================
//
// This suite tests the concurrency primitives under high contention scenarios.
// Run with: go test -race -timeout=60s -run=Stress ./core/concurrency/... -v
//
// Test categories:
// 1. Bounded overflow buffer under high contention (1000+ goroutines)
// 2. Adaptive channel with concurrent sends/receives
// 3. Unbounded channel overflow behavior
// 4. Race conditions with concurrent read/write/close
// 5. Deadlock detection scenarios
// 6. Goroutine leak verification

const (
	stressGoroutines     = 1000
	stressItemsPerWorker = 100
	stressTimeout        = 30 * time.Second
)

// ============================================================================
// 1. Bounded Overflow Buffer - High Contention Tests
// ============================================================================

func TestStress_BoundedOverflow_HighContention_DropOldest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	bo := NewBoundedOverflow[int](100, DropOldest)
	var wg sync.WaitGroup
	var totalAdded atomic.Int64

	runStressProducers(ctx, &wg, bo, &totalAdded)
	waitWithContext(ctx, t, &wg)
	verifyBoundedOverflowInvariants(t, bo, totalAdded.Load())
}

func TestStress_BoundedOverflow_HighContention_DropNewest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	bo := NewBoundedOverflow[int](100, DropNewest)
	var wg sync.WaitGroup
	var totalAdded atomic.Int64

	runStressProducers(ctx, &wg, bo, &totalAdded)
	waitWithContext(ctx, t, &wg)
	verifyBoundedOverflowInvariants(t, bo, totalAdded.Load())
}

func TestStress_BoundedOverflow_MixedOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	bo := NewBoundedOverflow[int](50, DropOldest)
	var wg sync.WaitGroup
	var added, taken atomic.Int64

	runBoundedProducers(ctx, &wg, bo, &added, 500, 50)
	runBoundedConsumers(ctx, &wg, bo, &taken, 500, 50)
	waitWithContext(ctx, t, &wg)

	remaining := int64(bo.Len())
	dropped := bo.DroppedCount()
	verifyMixedOpsInvariants(t, added.Load(), taken.Load(), remaining, dropped)
}

// ============================================================================
// 2. Adaptive Channel - Concurrent Send/Receive Tests
// ============================================================================

func TestStress_AdaptiveChannel_ConcurrentSendReceive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	config := AdaptiveChannelConfig{
		MinSize:       16,
		MaxSize:       512,
		InitialSize:   32,
		AllowOverflow: true,
		SendTimeout:   10 * time.Millisecond,
	}
	ch := NewAdaptiveChannelWithContext[int](ctx, config)
	defer ch.Close()

	var wg sync.WaitGroup
	var sent, received atomic.Int64

	runAdaptiveSenders(ctx, &wg, ch, &sent, 100, 50)
	runAdaptiveReceivers(ctx, &wg, ch, &received, 100, 50)
	waitWithContext(ctx, t, &wg)
	verifyAdaptiveChannelInvariants(t, ch, sent.Load(), received.Load())
}

func TestStress_AdaptiveChannel_ResizeDuringLoad(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	config := AdaptiveChannelConfig{
		MinSize:       4,
		MaxSize:       256,
		InitialSize:   8,
		AllowOverflow: true,
		SendTimeout:   5 * time.Millisecond,
	}
	ch := NewAdaptiveChannelWithContext[int](ctx, config)
	defer ch.Close()

	var wg sync.WaitGroup
	var sent, received atomic.Int64
	totalMessages := 500

	runResizeSender(ctx, &wg, ch, &sent, totalMessages)
	runResizeReceiver(ctx, &wg, ch, &received, totalMessages)
	waitWithContext(ctx, t, &wg)
	verifyResizeInvariants(t, ch, sent.Load(), received.Load())
}

// ============================================================================
// 3. Unbounded Channel - Overflow Behavior Tests
// ============================================================================

func TestStress_UnboundedChannel_OverflowUnderLoad(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	config := UnboundedChannelConfig{
		ChannelCapacity:    16,
		MaxOverflowSize:    1000,
		OverflowDropPolicy: DropOldest,
	}
	uc := NewUnboundedChannelWithConfig[int](config)
	defer uc.Close()

	var wg sync.WaitGroup
	var sent, received atomic.Int64

	runUnboundedSenders(ctx, &wg, uc, &sent, 100, 100)
	runUnboundedReceivers(ctx, &wg, uc, &received, 50, 200)
	waitWithContext(ctx, t, &wg)
	verifyUnboundedChannelInvariants(t, uc, sent.Load(), received.Load())
}

func TestStress_UnboundedChannel_BoundedOverflowPressure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	config := UnboundedChannelConfig{
		ChannelCapacity:    8,
		MaxOverflowSize:    100,
		OverflowDropPolicy: DropNewest,
	}
	uc := NewUnboundedChannelWithConfig[int](config)
	defer uc.Close()

	var wg sync.WaitGroup
	var sent, errors atomic.Int64

	runOverflowPressureSenders(ctx, &wg, uc, &sent, &errors, 200, 100)
	waitWithContext(ctx, t, &wg)

	stats := uc.Stats()
	t.Logf("sent: %d, dropped: %d, errors: %d", sent.Load(), stats.DroppedCount, errors.Load())
}

// ============================================================================
// 4. Race Condition Tests - Concurrent Read/Write/Close
// ============================================================================

func TestStress_BoundedOverflow_RaceConditions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	bo := NewBoundedOverflow[int](20, DropOldest)
	var wg sync.WaitGroup

	runRaceAdders(ctx, &wg, bo, 100, 20)
	runRaceTakers(ctx, &wg, bo, 50, 40)
	runRacePeekers(ctx, &wg, bo, 20, 50)
	runRaceStatsReaders(ctx, &wg, bo, 10, 100)
	waitWithContext(ctx, t, &wg)
}

func TestStress_AdaptiveChannel_ConcurrentOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	for i := 0; i < 100; i++ {
		runAdaptiveConcurrentOps(ctx)
	}
}

func TestStress_UnboundedChannel_ConcurrentOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stressTimeout)
	defer cancel()

	for i := 0; i < 100; i++ {
		runUnboundedConcurrentOps(ctx)
	}
}

// ============================================================================
// 5. Deadlock Detection Tests
// ============================================================================

func TestStress_BoundedOverflow_BlockPolicy_NoDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bo := NewBoundedOverflow[int](10, Block)
	var wg sync.WaitGroup

	runBlockConsumers(ctx, &wg, bo, 10, 40)
	time.Sleep(time.Millisecond)
	runBlockProducers(ctx, &wg, bo, 10, 20)
	scheduleClose(bo, 500*time.Millisecond)
	verifyNoDeadlock(ctx, t, &wg)
}

func TestStress_AdaptiveChannel_BlockingSend_NoDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config := AdaptiveChannelConfig{
		MinSize:       2,
		MaxSize:       8,
		InitialSize:   2,
		AllowOverflow: false,
		SendTimeout:   0,
	}
	ch := NewAdaptiveChannelWithContext[int](ctx, config)
	defer ch.Close()

	var wg sync.WaitGroup
	runBlockingSenders(ctx, &wg, ch, 10, 20)
	runBlockingReceivers(ctx, &wg, ch, 10, 20)
	verifyNoDeadlock(ctx, t, &wg)
}

// ============================================================================
// 6. Goroutine Leak Verification Tests
// ============================================================================

func TestStress_BoundedOverflow_NoGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()
	runBoundedOverflowLeakTest()
	verifyNoGoroutineLeak(t, initialGoroutines, 100*time.Millisecond)
}

func TestStress_AdaptiveChannel_NoGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()
	runAdaptiveChannelLeakTest()
	verifyNoGoroutineLeak(t, initialGoroutines, 200*time.Millisecond)
}

func TestStress_UnboundedChannel_NoGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()
	runUnboundedChannelLeakTest()
	verifyNoGoroutineLeak(t, initialGoroutines, 100*time.Millisecond)
}

// ============================================================================
// Helper Functions - Bounded Overflow Producers/Consumers
// ============================================================================

func runStressProducers(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], totalAdded *atomic.Int64) {
	for i := 0; i < stressGoroutines; i++ {
		wg.Add(1)
		go stressProducerWorker(ctx, wg, bo, totalAdded, i)
	}
}

func stressProducerWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], totalAdded *atomic.Int64, id int) {
	defer wg.Done()
	for j := 0; j < stressItemsPerWorker; j++ {
		if ctx.Err() != nil {
			return
		}
		bo.Add(id*stressItemsPerWorker + j)
		totalAdded.Add(1)
	}
}

func runBoundedProducers(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], added *atomic.Int64, count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go boundedProducerWorker(ctx, wg, bo, added, i, items)
	}
}

func boundedProducerWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], added *atomic.Int64, id, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		bo.Add(id*items + j)
		added.Add(1)
	}
}

func runBoundedConsumers(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], taken *atomic.Int64, count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go boundedConsumerWorker(ctx, wg, bo, taken, items)
	}
}

func boundedConsumerWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], taken *atomic.Int64, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		if _, ok := bo.Take(); ok {
			taken.Add(1)
		}
	}
}

func verifyBoundedOverflowInvariants(t *testing.T, bo *BoundedOverflow[int], total int64) {
	inBuffer := int64(bo.Len())
	dropped := bo.DroppedCount()

	if inBuffer > int64(bo.Cap()) {
		t.Errorf("buffer exceeds capacity: %d > %d", inBuffer, bo.Cap())
	}
	if inBuffer+dropped != total {
		t.Errorf("accounting error: inBuffer(%d) + dropped(%d) = %d, expected %d", inBuffer, dropped, inBuffer+dropped, total)
	}
}

func verifyMixedOpsInvariants(t *testing.T, added, taken, remaining, dropped int64) {
	if added-taken-remaining-dropped != 0 {
		t.Errorf("invariant violated: added(%d) - taken(%d) - remaining(%d) - dropped(%d) != 0", added, taken, remaining, dropped)
	}
}

// ============================================================================
// Helper Functions - Race Test Workers
// ============================================================================

func runRaceAdders(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go raceAdderWorker(ctx, wg, bo, i, items)
	}
}

func raceAdderWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], id, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		bo.Add(id*items + j)
	}
}

func runRaceTakers(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go raceTakerWorker(ctx, wg, bo, items)
	}
}

func raceTakerWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		bo.Take()
	}
}

func runRacePeekers(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go racePeekerWorker(ctx, wg, bo, items)
	}
}

func racePeekerWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		bo.Peek()
	}
}

func runRaceStatsReaders(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go raceStatsWorker(ctx, wg, bo, items)
	}
}

func raceStatsWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		bo.Stats()
	}
}

// ============================================================================
// Helper Functions - Block Policy Deadlock Test
// ============================================================================

func runBlockConsumers(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go blockConsumerWorker(ctx, wg, bo, items)
	}
}

func blockConsumerWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		bo.Take()
		time.Sleep(10 * time.Microsecond)
	}
}

func runBlockProducers(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go blockProducerWorker(ctx, wg, bo, i, items)
	}
}

func blockProducerWorker(ctx context.Context, wg *sync.WaitGroup, bo *BoundedOverflow[int], id, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		bo.Add(id*items + j)
	}
}

func scheduleClose(bo *BoundedOverflow[int], delay time.Duration) {
	go func() {
		time.Sleep(delay)
		bo.Close()
	}()
}

// ============================================================================
// Helper Functions - Bounded Overflow Leak Test
// ============================================================================

func runBoundedOverflowLeakTest() {
	for i := 0; i < 50; i++ {
		runSingleBoundedOverflowIteration()
	}
}

func runSingleBoundedOverflowIteration() {
	bo := NewBoundedOverflow[int](100, DropOldest)
	var wg sync.WaitGroup

	for j := 0; j < 10; j++ {
		wg.Add(1)
		go leakTestWorker(&wg, bo)
	}

	wg.Wait()
	bo.Close()
	bo.Drain()
}

func leakTestWorker(wg *sync.WaitGroup, bo *BoundedOverflow[int]) {
	defer wg.Done()
	for k := 0; k < 100; k++ {
		bo.Add(k)
	}
}

// ============================================================================
// Helper Functions - Adaptive Channel
// ============================================================================

func runAdaptiveSenders(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], sent *atomic.Int64, count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go adaptiveSenderWorker(ctx, wg, ch, sent, i, items)
	}
}

func adaptiveSenderWorker(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], sent *atomic.Int64, id, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		if err := ch.SendTimeout(id*items+j, 10*time.Millisecond); err == nil {
			sent.Add(1)
		}
	}
}

func runAdaptiveReceivers(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], received *atomic.Int64, count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go adaptiveReceiverWorker(ctx, wg, ch, received, items)
	}
}

func adaptiveReceiverWorker(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], received *atomic.Int64, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		if _, err := ch.ReceiveTimeout(10 * time.Millisecond); err == nil {
			received.Add(1)
		}
	}
}

func runResizeSender(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], sent *atomic.Int64, total int) {
	wg.Add(1)
	go resizeSenderWorker(ctx, wg, ch, sent, total)
}

func resizeSenderWorker(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], sent *atomic.Int64, total int) {
	defer wg.Done()
	for i := 0; i < total; i++ {
		if ctx.Err() != nil {
			return
		}
		if err := ch.SendTimeout(i, 50*time.Millisecond); err == nil {
			sent.Add(1)
		}
	}
}

func runResizeReceiver(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], received *atomic.Int64, total int) {
	wg.Add(1)
	go resizeReceiverWorker(ctx, wg, ch, received, total)
}

func resizeReceiverWorker(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], received *atomic.Int64, total int) {
	defer wg.Done()
	for received.Load() < int64(total) {
		if ctx.Err() != nil {
			return
		}
		if _, err := ch.ReceiveTimeout(50 * time.Millisecond); err == nil {
			received.Add(1)
		}
	}
}

func verifyAdaptiveChannelInvariants(t *testing.T, ch *AdaptiveChannel[int], sent, received int64) {
	stats := ch.Stats()
	remaining := int64(stats.MessageCount + stats.OverflowCount)
	if sent < received+remaining {
		t.Errorf("sent(%d) < received(%d) + remaining(%d)", sent, received, remaining)
	}
}

func verifyResizeInvariants(t *testing.T, ch *AdaptiveChannel[int], sent, received int64) {
	remaining := ch.Len() + ch.OverflowLen()
	if sent != received+int64(remaining) {
		t.Errorf("message loss: sent(%d) != received(%d) + remaining(%d)", sent, received, remaining)
	}
}

func runAdaptiveConcurrentOps(ctx context.Context) {
	config := DefaultAdaptiveChannelConfig()
	ch := NewAdaptiveChannelWithContext[int](ctx, config)
	defer ch.Close()

	var wg sync.WaitGroup
	startConcurrentAdaptiveWorkers(&wg, ch)
	wg.Wait()
}

func startConcurrentAdaptiveWorkers(wg *sync.WaitGroup, ch *AdaptiveChannel[int]) {
	for s := 0; s < 5; s++ {
		wg.Add(1)
		go concurrentAdaptiveSender(wg, ch, s)
	}
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go concurrentAdaptiveReceiver(wg, ch)
	}
}

func concurrentAdaptiveSender(wg *sync.WaitGroup, ch *AdaptiveChannel[int], id int) {
	defer wg.Done()
	for i := 0; i < 20; i++ {
		ch.SendTimeout(id*20+i, time.Millisecond)
	}
}

func concurrentAdaptiveReceiver(wg *sync.WaitGroup, ch *AdaptiveChannel[int]) {
	defer wg.Done()
	for i := 0; i < 20; i++ {
		ch.ReceiveTimeout(time.Millisecond)
	}
}

func runBlockingSenders(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go blockingSenderWorker(ctx, wg, ch, i, items)
	}
}

func blockingSenderWorker(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], id, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		ch.SendWithContext(ctx, id*items+j)
	}
}

func runBlockingReceivers(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go blockingReceiverWorker(ctx, wg, ch, items)
	}
}

func blockingReceiverWorker(ctx context.Context, wg *sync.WaitGroup, ch *AdaptiveChannel[int], items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		ch.ReceiveWithContext(ctx)
	}
}

func runAdaptiveChannelLeakTest() {
	for i := 0; i < 50; i++ {
		runSingleAdaptiveLeakIteration()
	}
}

func runSingleAdaptiveLeakIteration() {
	ctx, cancel := context.WithCancel(context.Background())
	config := DefaultAdaptiveChannelConfig()
	ch := NewAdaptiveChannelWithContext[int](ctx, config)

	var wg sync.WaitGroup
	for j := 0; j < 5; j++ {
		wg.Add(1)
		go adaptiveLeakWorker(&wg, ch)
	}

	wg.Wait()
	cancel()
	ch.Close()
}

func adaptiveLeakWorker(wg *sync.WaitGroup, ch *AdaptiveChannel[int]) {
	defer wg.Done()
	for k := 0; k < 20; k++ {
		ch.SendTimeout(k, time.Millisecond)
	}
}

// ============================================================================
// Helper Functions - Unbounded Channel
// ============================================================================

func runUnboundedSenders(ctx context.Context, wg *sync.WaitGroup, uc *UnboundedChannel[int], sent *atomic.Int64, count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go unboundedSenderWorker(ctx, wg, uc, sent, i, items)
	}
}

func unboundedSenderWorker(ctx context.Context, wg *sync.WaitGroup, uc *UnboundedChannel[int], sent *atomic.Int64, id, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		if err := uc.Send(id*items + j); err == nil {
			sent.Add(1)
		}
	}
}

func runUnboundedReceivers(ctx context.Context, wg *sync.WaitGroup, uc *UnboundedChannel[int], received *atomic.Int64, count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go unboundedReceiverWorker(ctx, wg, uc, received, items)
	}
}

func unboundedReceiverWorker(ctx context.Context, wg *sync.WaitGroup, uc *UnboundedChannel[int], received *atomic.Int64, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		ctxRecv, cancelRecv := context.WithTimeout(ctx, 10*time.Millisecond)
		if _, err := uc.ReceiveWithContext(ctxRecv); err == nil {
			received.Add(1)
		}
		cancelRecv()
	}
}

func runOverflowPressureSenders(ctx context.Context, wg *sync.WaitGroup, uc *UnboundedChannel[int], sent, errors *atomic.Int64, count, items int) {
	for i := 0; i < count; i++ {
		wg.Add(1)
		go overflowPressureWorker(ctx, wg, uc, sent, errors, i, items)
	}
}

func overflowPressureWorker(ctx context.Context, wg *sync.WaitGroup, uc *UnboundedChannel[int], sent, errors *atomic.Int64, id, items int) {
	defer wg.Done()
	for j := 0; j < items; j++ {
		if ctx.Err() != nil {
			return
		}
		if err := uc.Send(id*items + j); err != nil {
			errors.Add(1)
		} else {
			sent.Add(1)
		}
	}
}

func verifyUnboundedChannelInvariants(t *testing.T, uc *UnboundedChannel[int], sent, received int64) {
	stats := uc.Stats()
	remaining := int64(stats.ChannelLen + stats.OverflowLen)
	dropped := stats.DroppedCount
	accounted := received + remaining + dropped
	if accounted > sent {
		t.Errorf("accounting error: received(%d) + remaining(%d) + dropped(%d) > sent(%d)", received, remaining, dropped, sent)
	}
}

func runUnboundedConcurrentOps(ctx context.Context) {
	uc := NewUnboundedChannel[int]()
	defer uc.Close()

	var wg sync.WaitGroup
	startConcurrentUnboundedWorkers(&wg, uc)
	wg.Wait()
}

func startConcurrentUnboundedWorkers(wg *sync.WaitGroup, uc *UnboundedChannel[int]) {
	for s := 0; s < 5; s++ {
		wg.Add(1)
		go concurrentUnboundedSender(wg, uc, s)
	}
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go concurrentUnboundedReceiver(wg, uc)
	}
}

func concurrentUnboundedSender(wg *sync.WaitGroup, uc *UnboundedChannel[int], id int) {
	defer wg.Done()
	for i := 0; i < 20; i++ {
		uc.Send(id*20 + i)
	}
}

func concurrentUnboundedReceiver(wg *sync.WaitGroup, uc *UnboundedChannel[int]) {
	defer wg.Done()
	for i := 0; i < 20; i++ {
		uc.TryReceive()
	}
}

func runUnboundedChannelLeakTest() {
	for i := 0; i < 50; i++ {
		runSingleUnboundedLeakIteration()
	}
}

func runSingleUnboundedLeakIteration() {
	uc := NewUnboundedChannel[int]()

	var wg sync.WaitGroup
	for j := 0; j < 5; j++ {
		wg.Add(1)
		go unboundedLeakWorker(&wg, uc)
	}

	wg.Wait()
	uc.Close()
}

func unboundedLeakWorker(wg *sync.WaitGroup, uc *UnboundedChannel[int]) {
	defer wg.Done()
	for k := 0; k < 20; k++ {
		uc.Send(k)
	}
}

// ============================================================================
// Common Helper Functions
// ============================================================================

func waitWithContext(ctx context.Context, t *testing.T, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-ctx.Done():
		t.Fatal("test timed out - possible deadlock")
	}
}

func verifyNoDeadlock(ctx context.Context, t *testing.T, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no deadlock
	case <-ctx.Done():
		t.Fatal("potential deadlock detected - test timed out")
	}
}

func verifyNoGoroutineLeak(t *testing.T, initial int, settleTime time.Duration) {
	time.Sleep(settleTime)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	final := runtime.NumGoroutine()
	leaked := final - initial

	if leaked > 5 {
		t.Errorf("goroutine leak: started with %d, ended with %d (leaked %d)", initial, final, leaked)
	}
}
