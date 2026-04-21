package shared

import (
	"context"
	"strings"
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

// TokenCount safely extracts the full usage from a possibly-nil
// providers.Response. Returns a zero Usage when resp is nil.
func TokenCount(resp *providers.Response) providers.Usage {
	if resp == nil {
		return providers.Usage{}
	}
	return resp.Usage
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

// LogLLMCallStartedFromContext records the moment an LLM call is dispatched
// to the provider — paired with LogLLMCallFromContext at completion. Without
// this start record, hung or long-running LLM calls produce no observable
// trace in the WAL until they return (or never do). Captures the model
// name, message count, and tools-offered count so the JSONL log shows the
// shape of the request that may be stuck. No-op when ctx lacks LogMeta.
func LogLLMCallStartedFromContext(ctx context.Context, req *providers.Request) {
	if req == nil {
		return
	}
	m := LogMetaFromContext(ctx)
	if m.EventLogger == nil {
		return
	}
	payload := map[string]any{
		"model":          req.Model,
		"messages_count": len(req.Messages),
		"tools_offered":  len(req.Tools),
	}
	LogAgentEvent(m.EventLogger, agentlog.EventLLMRequestSent, m.AgentID, m.SessionID, m.CorrID, "info", payload)
}

// LogLLMCallFromContext extracts LogMeta from context and records an LLM call.
func LogLLMCallFromContext(ctx context.Context, model string, resp *providers.Response, dur time.Duration, err error) {
	m := LogMetaFromContext(ctx)
	if m.EventLogger == nil {
		return
	}
	usage := TokenCount(resp)
	LogLLMCall(m.EventLogger, m.CorrID, m.AgentID, m.SessionID, model, &usage, dur, err)
	if resp != nil {
		m.EventLogger.LogEvent(agentlog.JSONLEntry{
			Timestamp: time.Now(),
			Level:     "debug",
			Agent:     m.AgentID,
			SessionID: m.SessionID,
			Event:     "llm_response_shape",
			CorrID:    m.CorrID,
			Data:      buildLLMResponseShape(resp),
		})
	}
	if resp != nil && strings.TrimSpace(resp.Content) != "" {
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
		return
	}
	if resp != nil && strings.TrimSpace(resp.Thinking) != "" {
		m.EventLogger.LogEvent(agentlog.JSONLEntry{
			Timestamp: time.Now(),
			Level:     "debug",
			Agent:     m.AgentID,
			SessionID: m.SessionID,
			Event:     "llm_thinking_preview",
			CorrID:    m.CorrID,
			Data: map[string]any{
				"thinking_preview": truncate(resp.Thinking, 512),
			},
		})
	}
}

func buildLLMResponseShape(resp *providers.Response) map[string]any {
	if resp == nil {
		return map[string]any{
			"response_nil": true,
		}
	}
	data := map[string]any{
		"model":           resp.Model,
		"stop_reason":     resp.StopReason,
		"content_len":     len(resp.Content),
		"thinking_len":    len(resp.Thinking),
		"tool_call_count": len(resp.ToolCalls),
	}
	if toolNames := llmToolCallNames(resp.ToolCalls); len(toolNames) > 0 {
		data["tool_call_names"] = toolNames
	}
	if metadata := resp.ProviderMetadata; len(metadata) > 0 {
		if itemTypes := metadataStringSlice(metadata["output_item_types"]); len(itemTypes) > 0 {
			data["output_item_types"] = itemTypes
		}
		if itemCount, ok := metadata["output_item_count"]; ok {
			data["output_item_count"] = itemCount
		}
		if typeCounts, ok := metadata["output_item_type_counts"]; ok {
			data["output_item_type_counts"] = typeCounts
		}
		if streamFallback, ok := metadata["stream_fallback"]; ok {
			data["stream_fallback"] = streamFallback
		}
		if eventCount, ok := metadata["openai_stream_event_count"]; ok {
			data["openai_stream_event_count"] = eventCount
		}
		if eventTypes := metadataStringSlice(metadata["openai_stream_event_types"]); len(eventTypes) > 0 {
			data["openai_stream_event_types"] = eventTypes
		}
		if typeCounts, ok := metadata["openai_stream_event_type_counts"]; ok {
			data["openai_stream_event_type_counts"] = typeCounts
		}
		if outputCount, ok := metadata["openai_stream_completion_output_count"]; ok {
			data["openai_stream_completion_output_count"] = outputCount
		}
		if recovered, ok := metadata["stream_completion_recovered"]; ok {
			data["stream_completion_recovered"] = recovered
		}
	}
	return data
}

func llmToolCallNames(calls []providers.ToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = "<unnamed>"
		}
		names = append(names, name)
	}
	return names
}

func metadataStringSlice(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		if len(typed) == 0 {
			return nil
		}
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			text, ok := value.(string)
			if !ok {
				continue
			}
			text = strings.TrimSpace(text)
			if text != "" {
				out = append(out, text)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
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
func LogLLMCall(el *agentlog.SessionEventLogger, corrID, agentID, sessionID, model string, usage *providers.Usage, dur time.Duration, err error) {
	if el == nil {
		return
	}

	inputTok, outputTok := 0, 0
	if usage != nil {
		inputTok = usage.InputTokens
		outputTok = usage.OutputTokens
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

	data := map[string]any{
		"model":         model,
		"input_tokens":  inputTok,
		"output_tokens": outputTok,
	}
	if usage != nil {
		if usage.CacheReadTokens > 0 {
			data["cache_read_tokens"] = usage.CacheReadTokens
		}
		if usage.CacheWriteTokens > 0 {
			data["cache_write_tokens"] = usage.CacheWriteTokens
		}
		if usage.ReasoningTokens > 0 {
			data["reasoning_tokens"] = usage.ReasoningTokens
		}
	}

	entry := agentlog.JSONLEntry{
		Timestamp:  time.Now(),
		Level:      "info",
		Agent:      agentID,
		SessionID:  sessionID,
		Event:      "llm_call",
		EventCode:  eventType,
		CorrID:     corrID,
		DurationNs: dur.Nanoseconds(),
		Data:       data,
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
	case "write_file", "write_pipeline_file", "write_global_file":
		return agentlog.EventToolWriteFile
	case "glob":
		return agentlog.EventToolGlob
	case "grep":
		return agentlog.EventToolGrep
	case "bash":
		return agentlog.EventToolExec
	case "edit_file", "edit_pipeline_file", "edit_global_file", "delete_pipeline_file", "delete_global_file":
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

// LogInfo records an informational event. Use for expected-but-noteworthy
// operations that aren't errors (e.g. tool-call delivery after stream
// terminal, recovery paths, late-arriving events).
func LogInfo(el *agentlog.SessionEventLogger, agentID, sessionID, corrID, msg string, data map[string]any) {
	if el == nil {
		return
	}

	el.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "info",
		Agent:     agentID,
		SessionID: sessionID,
		Event:     msg,
		EventCode: agentlog.EventSkillInvoked,
		CorrID:    corrID,
		Data:      data,
	})
}

// LogThinkingChunk records a sampled thinking-stream chunk to the agent's
// WAL/JSONL log. The caller is expected to have already throttled emission
// (typically via ThoughtEmitter, which only returns non-empty at sentence
// boundaries) so this fires at most once per logical sentence rather than
// per provider chunk. Captures byte size and a short preview so the WAL
// shows liveness without recording the full content. No-op when ctx
// lacks LogMeta.
func LogThinkingChunk(ctx context.Context, chunk string) {
	if chunk == "" {
		return
	}
	m := LogMetaFromContext(ctx)
	if m.EventLogger == nil {
		return
	}
	LogAgentEvent(m.EventLogger, agentlog.EventLLMStreamChunk,
		m.AgentID, m.SessionID, m.CorrID, "info",
		map[string]any{
			"thinking_size": len(chunk),
			"preview":       truncate(chunk, 240),
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
