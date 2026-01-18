package resources

import (
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
)

type mockActiveProvider struct {
	pipelines []ActivePipelineInfo
}

func (m *mockActiveProvider) GetActivePipelines() []ActivePipelineInfo {
	return m.pipelines
}

func TestSchedulerAdapter_GetActiveSortedByPriority(t *testing.T) {
	provider := &mockActiveProvider{
		pipelines: []ActivePipelineInfo{
			{PipelineID: "p1", SessionID: "s1", Priority: concurrency.PriorityLow},
			{PipelineID: "p2", SessionID: "s1", Priority: concurrency.PriorityHigh},
			{PipelineID: "p3", SessionID: "s2", Priority: concurrency.PriorityNormal},
		},
	}

	adapter := NewSchedulerAdapter(provider)
	result := adapter.GetActiveSortedByPriority()

	assert.Len(t, result, 3)
	assert.Equal(t, "p1", result[0].PipelineID)
	assert.Equal(t, "p2", result[1].PipelineID)
	assert.Equal(t, "p3", result[2].PipelineID)
}

func TestSchedulerAdapter_Empty(t *testing.T) {
	provider := &mockActiveProvider{pipelines: nil}
	adapter := NewSchedulerAdapter(provider)

	result := adapter.GetActiveSortedByPriority()

	assert.Empty(t, result)
}

func TestSchedulerAdapter_ImplementsInterface(t *testing.T) {
	provider := &mockActiveProvider{}
	adapter := NewSchedulerAdapter(provider)

	var _ ActivePipelineProvider = adapter
}
