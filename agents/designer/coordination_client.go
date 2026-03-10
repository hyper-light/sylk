package designer

import (
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
)

func (d *Designer) coordinationClient() shared.CoordinationClient {
	return shared.CoordinationClient{
		BusProvider:     func() guide.EventBus { return d.bus },
		SourceAgentID:   func() string { return d.id },
		SourceAgentType: func() string { return "designer" },
		SessionID:       func() string { return d.config.SessionID },
		RegisterPending: d.registerPendingWait,
		ClearPending:    d.clearPendingWait,
		Timeout:         shared.DefaultConsultationTimeout,
	}
}

func (d *Designer) currentTaskName() string {
	return firstNonEmptyCoordinationName(d.pipelineName, d.pipelineSlug)
}
