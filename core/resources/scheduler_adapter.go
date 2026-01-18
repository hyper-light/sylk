package resources

import (
	"github.com/adalundhe/sylk/core/concurrency"
)

type ActivePipelineInfo struct {
	PipelineID string
	SessionID  string
	Priority   concurrency.PipelinePriority
}

type ActivePipelineInfoProvider interface {
	GetActivePipelines() []ActivePipelineInfo
}

type SchedulerAdapter struct {
	provider ActivePipelineInfoProvider
}

func NewSchedulerAdapter(provider ActivePipelineInfoProvider) *SchedulerAdapter {
	return &SchedulerAdapter{provider: provider}
}

func (a *SchedulerAdapter) GetActiveSortedByPriority() []PipelineInfo {
	active := a.provider.GetActivePipelines()
	result := make([]PipelineInfo, len(active))

	for i, p := range active {
		result[i] = PipelineInfo{
			PipelineID: p.PipelineID,
			SessionID:  p.SessionID,
			Priority:   p.Priority,
		}
	}

	return result
}

var _ ActivePipelineProvider = (*SchedulerAdapter)(nil)
