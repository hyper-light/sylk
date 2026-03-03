package shared

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
)

// truncate returns the first max bytes of s, appending "…" if truncated.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// TokenCount safely extracts input and output token counts from a
// possibly-nil providers.Response.
func TokenCount(resp *providers.Response) (int, int) {
	if resp == nil {
		return 0, 0
	}
	return resp.Usage.InputTokens, resp.Usage.OutputTokens
}

// LogAgentEvent is a generic dual-write helper that records an agent-specific
// event in both the binary WAL and the JSONL structured log. Nil-safe: if el
// is nil the call is a no-op.
func LogAgentEvent(
	el *agentlog.SessionEventLogger,
	eventType agentlog.EventType,
	agentID, sessionID, corrID, level string,
	data any,
) {
	if el == nil {
		return
	}

	el.LogWALJSON(eventType, data)

	el.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     agentID,
		SessionID: sessionID,
		Event:     eventType.String(),
		EventCode: eventType,
		CorrID:    corrID,
		Data:      data,
	})
}

// LogLLMCallFromContext extracts LogMeta from context and records an LLM call.
func LogLLMCallFromContext(ctx context.Context, model string, resp *providers.Response, dur time.Duration, err error) {
	m := LogMetaFromContext(ctx)
	if m.EventLogger == nil {
		return
	}
	in, out := TokenCount(resp)
	LogLLMCall(m.EventLogger, m.CorrID, m.AgentID, m.SessionID, model, in, out, dur, err)
	if resp != nil && resp.Content != "" {
		m.EventLogger.LogEvent(agentlog.JSONLEntry{
			Timestamp: time.Now(),
			Level:     "debug",
			Agent:     m.AgentID,
			SessionID: m.SessionID,
			Event:     "llm_response_preview",
			CorrID:    m.CorrID,
			Data: map[string]any{
				"response_preview": truncate(resp.Content, 512),
			},
		})
	}
}

// LogIncomingRequest records a forwarded request arrival in both the binary WAL
// and the JSONL structured log.
func LogIncomingRequest(el *agentlog.SessionEventLogger, fwd *guide.ForwardedRequest, agentID string) {
	if el == nil || fwd == nil {
		return
	}

	walPayload := struct {
		CorrelationID string `json:"correlation_id"`
		AgentID       string `json:"agent_id"`
		SessionID     string `json:"session_id"`
		Intent        string `json:"intent"`
		Domain        string `json:"domain"`
		InputLen      int    `json:"input_len"`
		FireAndForget bool   `json:"fire_and_forget"`
	}{
		CorrelationID: fwd.CorrelationID,
		AgentID:       agentID,
		SessionID:     fwd.SessionID,
		Intent:        string(fwd.Intent),
		Domain:        string(fwd.Domain),
		InputLen:      len(fwd.Input),
		FireAndForget: fwd.FireAndForget,
	}
	el.LogWALJSON(agentlog.EventBusRequestReceived, walPayload)

	el.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "info",
		Agent:     agentID,
		SessionID: fwd.SessionID,
		Event:     "request_received",
		EventCode: agentlog.EventBusRequestReceived,
		CorrID:    fwd.CorrelationID,
		Data: map[string]any{
			"intent":          string(fwd.Intent),
			"domain":          string(fwd.Domain),
			"input_len":       len(fwd.Input),
			"input_preview":   truncate(fwd.Input, 512),
			"fire_and_forget": fwd.FireAndForget,
			"source_agent":    fwd.SourceAgentID,
		},
	})
}

// LogResponse records a completed response in both the binary WAL and the JSONL
// structured log.
func LogResponse(el *agentlog.SessionEventLogger, corrID, agentID, sessionID string, dur time.Duration, err error) {
	if el == nil {
		return
	}

	walPayload := struct {
		CorrelationID string `json:"correlation_id"`
		AgentID       string `json:"agent_id"`
		SessionID     string `json:"session_id"`
		DurationMs    int64  `json:"duration_ms"`
		Success       bool   `json:"success"`
		Error         string `json:"error,omitempty"`
	}{
		CorrelationID: corrID,
		AgentID:       agentID,
		SessionID:     sessionID,
		DurationMs:    dur.Milliseconds(),
		Success:       err == nil,
	}
	if err != nil {
		walPayload.Error = err.Error()
	}

	eventType := agentlog.EventBusResponseSent
	if err != nil {
		eventType = agentlog.EventBusErrorSent
	}
	el.LogWALJSON(eventType, walPayload)

	entry := agentlog.JSONLEntry{
		Timestamp:  time.Now(),
		Level:      "info",
		Agent:      agentID,
		SessionID:  sessionID,
		Event:      "response_sent",
		EventCode:  eventType,
		CorrID:     corrID,
		DurationNs: dur.Nanoseconds(),
	}
	if err != nil {
		entry.Level = "error"
		entry.Event = "response_error"
		entry.Error = err.Error()
		entry.Data = map[string]any{
			"error_detail": err.Error(),
		}
	}
	el.LogEvent(entry)
}

// LogLLMCall records an LLM request/response cycle.
func LogLLMCall(el *agentlog.SessionEventLogger, corrID, agentID, sessionID, model string, inputTok, outputTok int, dur time.Duration, err error) {
	if el == nil {
		return
	}

	walPayload := struct {
		CorrelationID string `json:"correlation_id"`
		Model         string `json:"model"`
		InputTokens   int    `json:"input_tokens"`
		OutputTokens  int    `json:"output_tokens"`
		DurationMs    int64  `json:"duration_ms"`
		Error         string `json:"error,omitempty"`
	}{
		CorrelationID: corrID,
		Model:         model,
		InputTokens:   inputTok,
		OutputTokens:  outputTok,
		DurationMs:    dur.Milliseconds(),
	}
	if err != nil {
		walPayload.Error = err.Error()
	}

	eventType := agentlog.EventLLMResponseReceived
	if err != nil {
		eventType = agentlog.EventLLMError
	}
	el.LogWALJSON(eventType, walPayload)

	entry := agentlog.JSONLEntry{
		Timestamp:  time.Now(),
		Level:      "info",
		Agent:      agentID,
		SessionID:  sessionID,
		Event:      "llm_call",
		EventCode:  eventType,
		CorrID:     corrID,
		DurationNs: dur.Nanoseconds(),
		Data: map[string]any{
			"model":         model,
			"input_tokens":  inputTok,
			"output_tokens": outputTok,
		},
	}
	if err != nil {
		entry.Level = "error"
		entry.Event = "llm_error"
		entry.Error = err.Error()
	}
	el.LogEvent(entry)
}

// ToolNameToEventType maps a tool call name to the corresponding engineer EventType.
func ToolNameToEventType(name string) agentlog.EventType {
	switch name {
	case "read_file":
		return agentlog.EventToolReadFile
	case "write_file":
		return agentlog.EventToolWriteFile
	case "glob":
		return agentlog.EventToolGlob
	case "grep":
		return agentlog.EventToolGrep
	case "run_command":
		return agentlog.EventToolExec
	case "edit_file":
		return agentlog.EventToolEditFile
	case "ask_user_clarification":
		return agentlog.EventToolAskUser
	default:
		return agentlog.EventSkillInvoked
	}
}

// LogWarning records a warning event.
func LogWarning(el *agentlog.SessionEventLogger, agentID, sessionID, corrID, msg string, data map[string]any) {
	if el == nil {
		return
	}

	el.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "warn",
		Agent:     agentID,
		SessionID: sessionID,
		Event:     msg,
		EventCode: agentlog.EventError,
		CorrID:    corrID,
		Data:      data,
	})
}

// LogStatusUpdate records an agent status change.
func LogStatusUpdate(el *agentlog.SessionEventLogger, agentID, sessionID, status string) {
	if el == nil {
		return
	}

	var eventCode agentlog.EventType
	switch status {
	case "activated", "busy":
		eventCode = agentlog.EventActivated
	case "ready", "idle":
		eventCode = agentlog.EventReady
	case "stopping":
		eventCode = agentlog.EventStopping
	case "stopped":
		eventCode = agentlog.EventStopped
	default:
		eventCode = agentlog.EventReady
	}

	el.LogWALJSON(eventCode, struct {
		AgentID   string `json:"agent_id"`
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}{
		AgentID:   agentID,
		SessionID: sessionID,
		Status:    status,
	})

	el.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "info",
		Agent:     agentID,
		SessionID: sessionID,
		Event:     "status_" + status,
		EventCode: eventCode,
	})
}
