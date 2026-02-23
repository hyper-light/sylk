package guide

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type guideDiagnosticReport struct {
	Query                   string            `json:"query,omitempty"`
	SessionID               string            `json:"session_id,omitempty"`
	PendingRequests         int               `json:"pending_requests"`
	RegisteredAgentCount    int               `json:"registered_agent_count"`
	ActiveConversationAgent string            `json:"active_conversation_agent,omitempty"`
	ActiveConversationTurns int               `json:"active_conversation_turns,omitempty"`
	TracePath               string            `json:"trace_path,omitempty"`
	LastTrace               *geminiTraceBrief `json:"last_trace,omitempty"`
	LastErrorTrace          *geminiTraceBrief `json:"last_error_trace,omitempty"`
	RecoveryHints           []string          `json:"recovery_hints,omitempty"`
}

type geminiTraceBrief struct {
	Timestamp string `json:"timestamp,omitempty"`
	Component string `json:"component,omitempty"`
	Event     string `json:"event,omitempty"`
	Preview   string `json:"preview,omitempty"`
}

func buildGuideDiagnosticReport(g *Guide, query string, sessionID string) guideDiagnosticReport {
	report := guideDiagnosticReport{
		Query:         strings.TrimSpace(query),
		SessionID:     strings.TrimSpace(sessionID),
		RecoveryHints: defaultGuideRecoveryHints(),
	}
	if g != nil {
		request := g.newGuideSelfResponseRequest("status", sessionID)
		report.PendingRequests = request.PendingRequests
		report.RegisteredAgentCount = len(sanitizedGuideAgentIDs(request.RegisteredAgentIDs))
		report.ActiveConversationAgent = strings.TrimSpace(request.ActiveConversationAgent)
		report.ActiveConversationTurns = request.ActiveConversationTurns
	}

	report.TracePath = geminiTraceFilePath()
	if record, err := latestGeminiTraceRecord(); err == nil && record != nil {
		report.LastTrace = traceRecordBrief(record)
	}
	if record, err := latestGeminiTraceErrorRecord(); err == nil && record != nil {
		report.LastErrorTrace = traceRecordBrief(record)
	}
	return report
}

func traceRecordBrief(record *geminiTraceRecord) *geminiTraceBrief {
	if record == nil {
		return nil
	}
	return &geminiTraceBrief{
		Timestamp: strings.TrimSpace(record.Timestamp),
		Component: strings.TrimSpace(record.Component),
		Event:     strings.TrimSpace(record.Event),
		Preview:   tracePreview(fmt.Sprintf("%v", record.Fields), 180),
	}
}

func formatGuideDiagnosticReport(report guideDiagnosticReport) string {
	lines := []string{
		"Guide self-diagnostic summary:",
		fmt.Sprintf("- pending_requests: %d", report.PendingRequests),
		fmt.Sprintf("- registered_agents: %d", report.RegisteredAgentCount),
	}
	if report.ActiveConversationAgent != "" {
		lines = append(lines, fmt.Sprintf("- active_conversation_agent: %s", report.ActiveConversationAgent))
	}
	if report.ActiveConversationTurns > 0 {
		lines = append(lines, fmt.Sprintf("- active_conversation_turns: %d", report.ActiveConversationTurns))
	}
	if report.TracePath == "" {
		lines = append(lines, "- gemini_trace: disabled (set SYLK_GEMINI_TRACE=1 or SYLK_GEMINI_TRACE_FILE=/path/to/trace.log)")
	} else {
		lines = append(lines, fmt.Sprintf("- gemini_trace_file: %s", report.TracePath))
		if report.LastTrace != nil {
			lines = append(lines, fmt.Sprintf("- last_trace_event: %s.%s @ %s", report.LastTrace.Component, report.LastTrace.Event, report.LastTrace.Timestamp))
		}
		if report.LastErrorTrace != nil {
			lines = append(lines, fmt.Sprintf("- last_error_event: %s.%s @ %s", report.LastErrorTrace.Component, report.LastErrorTrace.Event, report.LastErrorTrace.Timestamp))
		}
	}
	if len(report.RecoveryHints) > 0 {
		lines = append(lines, "- recovery_hints: "+strings.Join(report.RecoveryHints, " | "))
	}
	return strings.Join(lines, "\n")
}

func defaultGuideRecoveryHints() []string {
	return []string{
		"Retry the request once to rule out transient provider errors.",
		"If the issue repeats, inspect recent Gemini trace events for stream_no_candidates or request_error.",
		"Switch to a simpler phrasing to confirm classification/self-response path health.",
	}
}

func latestGeminiTraceRecord() (*geminiTraceRecord, error) {
	path := strings.TrimSpace(geminiTraceFilePath())
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return newestTraceRecordFromBytes(data, nil), nil
}

func latestGeminiTraceErrorRecord() (*geminiTraceRecord, error) {
	path := strings.TrimSpace(geminiTraceFilePath())
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return newestTraceRecordFromBytes(data, isErrorLikeTraceEvent), nil
}

func newestTraceRecordFromBytes(data []byte, keep func(*geminiTraceRecord) bool) *geminiTraceRecord {
	lines := bytes.Split(data, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(string(lines[i]))
		if line == "" {
			continue
		}
		var record geminiTraceRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if keep != nil && !keep(&record) {
			continue
		}
		return &record
	}
	return nil
}

func isErrorLikeTraceEvent(record *geminiTraceRecord) bool {
	if record == nil {
		return false
	}
	event := strings.ToLower(strings.TrimSpace(record.Event))
	switch {
	case strings.Contains(event, "error"):
		return true
	case strings.Contains(event, "fail"):
		return true
	case strings.Contains(event, "no_candidates"):
		return true
	default:
		return false
	}
}
