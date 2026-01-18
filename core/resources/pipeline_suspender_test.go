package resources

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPipelineProvider struct {
	pipelines []PipelineInfo
}

func (m *mockPipelineProvider) GetActiveSortedByPriority() []PipelineInfo {
	return m.pipelines
}

func setupSuspenderTest(t *testing.T, pipelines []PipelineInfo) (*PipelineSuspender, *signal.SignalBus, chan signal.SignalMessage) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	t.Cleanup(func() { bus.Close() })

	received := make(chan signal.SignalMessage, 10)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.PausePipeline, signal.ResumePipeline},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	provider := &mockPipelineProvider{pipelines: pipelines}
	suspender := NewPipelineSuspender(provider, bus)

	return suspender, bus, received
}

func TestPipelineSuspender_InitialState(t *testing.T) {
	suspender, _, _ := setupSuspenderTest(t, nil)

	assert.Equal(t, 0, suspender.PausedCount())
	assert.False(t, suspender.IsPaused("any"))
}

func TestPipelineSuspender_PauseLowestPriority(t *testing.T) {
	pipelines := []PipelineInfo{
		{PipelineID: "p-low", SessionID: "s1", Priority: concurrency.PriorityLow},
		{PipelineID: "p-normal", SessionID: "s1", Priority: concurrency.PriorityNormal},
		{PipelineID: "p-high", SessionID: "s1", Priority: concurrency.PriorityHigh},
	}
	suspender, _, received := setupSuspenderTest(t, pipelines)

	paused := suspender.PauseLowestPriority()

	require.NotNil(t, paused)
	assert.Equal(t, "p-low", paused.ID)
	assert.Equal(t, concurrency.PriorityLow, paused.Priority)
	assert.Equal(t, 1, suspender.PausedCount())
	assert.True(t, suspender.IsPaused("p-low"))

	select {
	case msg := <-received:
		assert.Equal(t, signal.PausePipeline, msg.Signal)
		assert.Equal(t, "p-low", msg.TargetID)
		assert.True(t, msg.RequiresAck)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No signal received")
	}
}

func TestPipelineSuspender_PauseLowestPriority_SkipsAlreadyPaused(t *testing.T) {
	pipelines := []PipelineInfo{
		{PipelineID: "p1", SessionID: "s1", Priority: concurrency.PriorityLow},
		{PipelineID: "p2", SessionID: "s1", Priority: concurrency.PriorityNormal},
	}
	suspender, _, received := setupSuspenderTest(t, pipelines)

	suspender.PauseLowestPriority()
	<-received

	paused := suspender.PauseLowestPriority()

	require.NotNil(t, paused)
	assert.Equal(t, "p2", paused.ID, "Should pause next lowest after first is paused")
	assert.Equal(t, 2, suspender.PausedCount())
}

func TestPipelineSuspender_PauseLowestPriority_NoActive(t *testing.T) {
	suspender, _, _ := setupSuspenderTest(t, nil)

	paused := suspender.PauseLowestPriority()

	assert.Nil(t, paused)
	assert.Equal(t, 0, suspender.PausedCount())
}

func TestPipelineSuspender_PauseLowestPriority_AllPaused(t *testing.T) {
	pipelines := []PipelineInfo{
		{PipelineID: "p1", SessionID: "s1", Priority: concurrency.PriorityLow},
	}
	suspender, _, received := setupSuspenderTest(t, pipelines)

	suspender.PauseLowestPriority()
	<-received

	paused := suspender.PauseLowestPriority()

	assert.Nil(t, paused, "Should return nil when all pipelines already paused")
}

func TestPipelineSuspender_ResumeAll(t *testing.T) {
	pipelines := []PipelineInfo{
		{PipelineID: "p1", SessionID: "s1", Priority: concurrency.PriorityLow},
		{PipelineID: "p2", SessionID: "s1", Priority: concurrency.PriorityNormal},
	}
	suspender, _, received := setupSuspenderTest(t, pipelines)

	suspender.PauseLowestPriority()
	suspender.PauseLowestPriority()
	<-received
	<-received

	assert.Equal(t, 2, suspender.PausedCount())

	suspender.ResumeAll()

	assert.Equal(t, 0, suspender.PausedCount())
	assert.False(t, suspender.IsPaused("p1"))
	assert.False(t, suspender.IsPaused("p2"))

	resumeCount := 0
	timeout := time.After(100 * time.Millisecond)
	for resumeCount < 2 {
		select {
		case msg := <-received:
			if msg.Signal == signal.ResumePipeline {
				resumeCount++
			}
		case <-timeout:
			t.Fatalf("Expected 2 resume signals, got %d", resumeCount)
		}
	}
}

func TestPipelineSuspender_ResumeHighestPriority(t *testing.T) {
	pipelines := []PipelineInfo{
		{PipelineID: "p-low", SessionID: "s1", Priority: concurrency.PriorityLow},
		{PipelineID: "p-high", SessionID: "s1", Priority: concurrency.PriorityHigh},
	}
	suspender, _, received := setupSuspenderTest(t, pipelines)

	suspender.PauseLowestPriority()
	suspender.PauseLowestPriority()
	<-received
	<-received

	resumed := suspender.ResumeHighestPriority()

	require.NotNil(t, resumed)
	assert.Equal(t, "p-high", resumed.ID)
	assert.Equal(t, 1, suspender.PausedCount())
	assert.False(t, suspender.IsPaused("p-high"))
	assert.True(t, suspender.IsPaused("p-low"))

	select {
	case msg := <-received:
		assert.Equal(t, signal.ResumePipeline, msg.Signal)
		assert.Equal(t, "p-high", msg.TargetID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No resume signal")
	}
}

func TestPipelineSuspender_ResumeHighestPriority_NoPaused(t *testing.T) {
	suspender, _, _ := setupSuspenderTest(t, nil)

	resumed := suspender.ResumeHighestPriority()

	assert.Nil(t, resumed)
}

func TestPipelineSuspender_ConcurrentOperations(t *testing.T) {
	pipelines := make([]PipelineInfo, 20)
	for i := 0; i < 20; i++ {
		pipelines[i] = PipelineInfo{
			PipelineID: string(rune('a' + i)),
			SessionID:  "s1",
			Priority:   concurrency.PipelinePriority(i % 4),
		}
	}
	suspender, _, _ := setupSuspenderTest(t, pipelines)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			suspender.PauseLowestPriority()
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = suspender.PausedCount()
			suspender.ResumeHighestPriority()
		}()
	}

	wg.Wait()

	assert.GreaterOrEqual(t, suspender.PausedCount(), 0)
}

func TestPipelineSuspender_PausedAt(t *testing.T) {
	pipelines := []PipelineInfo{
		{PipelineID: "p1", SessionID: "s1", Priority: concurrency.PriorityNormal},
	}
	suspender, _, received := setupSuspenderTest(t, pipelines)

	before := time.Now()
	paused := suspender.PauseLowestPriority()
	after := time.Now()
	<-received

	require.NotNil(t, paused)
	assert.True(t, paused.PausedAt.After(before) || paused.PausedAt.Equal(before))
	assert.True(t, paused.PausedAt.Before(after) || paused.PausedAt.Equal(after))
}
