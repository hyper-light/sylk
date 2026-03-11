package orchestrator

import (
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
)

func logTraceEvent(
	el *agentlog.SessionEventLogger,
	level, agentID, sessionID, event string,
	eventCode agentlog.EventType,
	data map[string]any,
) {
	if el == nil {
		return
	}

	el.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     agentID,
		SessionID: sessionID,
		Event:     event,
		EventCode: eventCode,
		Data:      data,
	})
}

func (b *DAGBridge) logTrace(event string, eventCode agentlog.EventType, data map[string]any) {
	logTraceEvent(b.eventLogger, "debug", b.agentID, b.sessionID, event, eventCode, data)
}

func (o *Orchestrator) logTrace(event string, eventCode agentlog.EventType, data map[string]any) {
	if o == nil || o.steering == nil {
		return
	}
	logTraceEvent(o.steering.EventLogger(), "debug", o.config.AgentID, o.config.SessionID, event, eventCode, data)
}

func (d *BusNodeDispatcher) logTrace(event string, eventCode agentlog.EventType, data map[string]any) {
	logTraceEvent(d.eventLogger, "debug", d.agentID, d.sessionID, event, eventCode, data)
}
