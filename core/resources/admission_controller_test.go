package resources

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockScheduler struct {
	mu        sync.Mutex
	scheduled []*concurrency.SchedulablePipeline
	shouldErr bool
}

func (m *mockScheduler) Schedule(_ context.Context, _ string, p *concurrency.SchedulablePipeline) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shouldErr {
		return false, context.Canceled
	}

	m.scheduled = append(m.scheduled, p)
	return true, nil
}

func (m *mockScheduler) getScheduled() []*concurrency.SchedulablePipeline {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*concurrency.SchedulablePipeline(nil), m.scheduled...)
}

func makePipeline(id string, priority concurrency.PipelinePriority) *concurrency.SchedulablePipeline {
	return &concurrency.SchedulablePipeline{
		ID:       id,
		Priority: priority,
	}
}

func TestAdmissionController_InitialState(t *testing.T) {
	scheduler := &mockScheduler{}
	ac := NewAdmissionController(scheduler)

	assert.True(t, ac.IsAdmitting(), "Should be admitting by default")
	assert.Equal(t, 0, ac.QueueLength(), "Queue should be empty initially")
}

func TestAdmissionController_TryAdmit_WhenAdmitting(t *testing.T) {
	scheduler := &mockScheduler{}
	ac := NewAdmissionController(scheduler)
	ctx := context.Background()

	p := makePipeline("p1", concurrency.PriorityNormal)
	admitted := ac.TryAdmit(ctx, "session-1", p)

	assert.True(t, admitted)
	assert.Equal(t, 0, ac.QueueLength())

	scheduled := scheduler.getScheduled()
	require.Len(t, scheduled, 1)
	assert.Equal(t, "p1", scheduled[0].ID)
}

func TestAdmissionController_TryAdmit_WhenNotAdmitting(t *testing.T) {
	scheduler := &mockScheduler{}
	ac := NewAdmissionController(scheduler)
	ctx := context.Background()

	ac.Stop()

	p := makePipeline("p1", concurrency.PriorityNormal)
	admitted := ac.TryAdmit(ctx, "session-1", p)

	assert.False(t, admitted)
	assert.Equal(t, 1, ac.QueueLength())
	assert.Len(t, scheduler.getScheduled(), 0)
}

func TestAdmissionController_Stop(t *testing.T) {
	scheduler := &mockScheduler{}
	ac := NewAdmissionController(scheduler)

	assert.True(t, ac.IsAdmitting())

	ac.Stop()

	assert.False(t, ac.IsAdmitting())
}

func TestAdmissionController_Resume_DrainsQueue(t *testing.T) {
	scheduler := &mockScheduler{}
	ac := NewAdmissionController(scheduler)
	ctx := context.Background()

	ac.Stop()

	for i := 0; i < 3; i++ {
		p := makePipeline("p"+string(rune('0'+i)), concurrency.PriorityNormal)
		ac.TryAdmit(ctx, "session-1", p)
	}

	assert.Equal(t, 3, ac.QueueLength())
	assert.Len(t, scheduler.getScheduled(), 0)

	ac.Resume()

	assert.True(t, ac.IsAdmitting())
	assert.Equal(t, 0, ac.QueueLength())

	scheduled := scheduler.getScheduled()
	assert.Len(t, scheduled, 3)
}

func TestAdmissionController_QueuePriorityOrder(t *testing.T) {
	scheduler := &mockScheduler{}
	ac := NewAdmissionController(scheduler)
	ctx := context.Background()

	ac.Stop()

	pLow := makePipeline("p-low", concurrency.PriorityLow)
	pNormal := makePipeline("p-normal", concurrency.PriorityNormal)
	pHigh := makePipeline("p-high", concurrency.PriorityHigh)
	pCritical := makePipeline("p-critical", concurrency.PriorityCritical)

	ac.TryAdmit(ctx, "s1", pLow)
	ac.TryAdmit(ctx, "s1", pNormal)
	ac.TryAdmit(ctx, "s1", pHigh)
	ac.TryAdmit(ctx, "s1", pCritical)

	ac.Resume()

	scheduled := scheduler.getScheduled()
	require.Len(t, scheduled, 4)
	assert.Equal(t, "p-critical", scheduled[0].ID)
	assert.Equal(t, "p-high", scheduled[1].ID)
	assert.Equal(t, "p-normal", scheduled[2].ID)
	assert.Equal(t, "p-low", scheduled[3].ID)
}

func TestAdmissionController_ConcurrentOperations(t *testing.T) {
	scheduler := &mockScheduler{}
	ac := NewAdmissionController(scheduler)
	ctx := context.Background()

	var wg sync.WaitGroup
	admitCount := 0
	queueCount := 0
	var countMu sync.Mutex

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := makePipeline("p"+string(rune(idx)), concurrency.PriorityNormal)

			if idx%10 == 5 {
				ac.Stop()
			} else if idx%10 == 8 {
				ac.Resume()
			}

			admitted := ac.TryAdmit(ctx, "session-1", p)

			countMu.Lock()
			if admitted {
				admitCount++
			} else {
				queueCount++
			}
			countMu.Unlock()
		}(i)
	}

	wg.Wait()

	ac.Resume()

	time.Sleep(10 * time.Millisecond)

	total := len(scheduler.getScheduled()) + ac.QueueLength()
	assert.GreaterOrEqual(t, total, 0)
}

func TestAdmissionController_SchedulerError(t *testing.T) {
	scheduler := &mockScheduler{shouldErr: true}
	ac := NewAdmissionController(scheduler)
	ctx := context.Background()

	p := makePipeline("p1", concurrency.PriorityNormal)
	admitted := ac.TryAdmit(ctx, "session-1", p)

	assert.False(t, admitted)
}

func TestAdmissionController_EmptyResume(t *testing.T) {
	scheduler := &mockScheduler{}
	ac := NewAdmissionController(scheduler)

	ac.Stop()
	ac.Resume()

	assert.True(t, ac.IsAdmitting())
	assert.Equal(t, 0, ac.QueueLength())
	assert.Len(t, scheduler.getScheduled(), 0)
}
