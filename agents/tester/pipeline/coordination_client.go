package pipeline

import (
	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func (pt *PipelineTester) coordinationClient() agentshared.CoordinationClient {
	return agentshared.CoordinationClient{
		BusProvider:     func() guide.EventBus { return pt.bus },
		SourceAgentID:   func() string { return pt.id },
		SourceAgentType: func() string { return "tester-pipeline" },
		SessionID:       func() string { return pt.config.SessionID },
		RegisterPending: pt.registerPendingWait,
		ClearPending:    pt.clearPendingWait,
		Timeout:         agentshared.DefaultConsultationTimeout,
	}
}

func (pt *PipelineTester) currentTaskName() string {
	return firstNonEmptyCoordinationName(pt.pipelineName, pt.pipelineSlug)
}
