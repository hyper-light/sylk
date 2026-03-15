package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// ToolCallPhase distinguishes start from completion events.
type ToolCallPhase int

const (
	// ToolCallStart is emitted when a tool call begins execution.
	ToolCallStart ToolCallPhase = 0

	// ToolCallComplete is emitted when a tool call finishes (success or failure).
	ToolCallComplete ToolCallPhase = 1
)

// maxArgsSummaryLen is the maximum character length for a compact args summary.
const maxArgsSummaryLen = 60

// maxOutputBytes is the default truncation limit for tool output.
const maxOutputBytes = 512

// priorityArgKeys are extracted first for compact summaries, in order.
var priorityArgKeys = [...]string{
	"path", "file_path", "pattern", "query", "command", "url",
	"name", "content", "message",
}

// ToolCallEvent carries timing and metadata for a single tool invocation.
type ToolCallEvent struct {
	ToolName    string        `json:"tool_name"`
	ArgsSummary string        `json:"args_summary"`
	FullArgs    string        `json:"full_args"`
	Output      string        `json:"output"`
	ErrorMsg    string        `json:"error_msg"`
	AgentID     string        `json:"agent_id"`
	Phase       ToolCallPhase `json:"phase"`
	StartedAt   time.Time     `json:"started_at"`
	Duration    time.Duration `json:"duration"`
	Success     bool          `json:"success"`
}

// ToolCallEmitter is a callback that publishes a tool call event to the bus.
type ToolCallEmitter func(ToolCallEvent)

type toolCallEmitterKey struct{}

// WithToolCallEmitter attaches a ToolCallEmitter to a context.
func WithToolCallEmitter(ctx context.Context, emitter ToolCallEmitter) context.Context {
	return context.WithValue(ctx, toolCallEmitterKey{}, emitter)
}

// EmitToolCall invokes the emitter attached to ctx, if present.
func EmitToolCall(ctx context.Context, event ToolCallEvent) {
	emitter, ok := ctx.Value(toolCallEmitterKey{}).(ToolCallEmitter)
	if !ok || emitter == nil {
		return
	}
	emitter(event)
}

// TimedToolCall wraps a tool call execution with start/complete event emission.
// The execute callback performs the actual tool invocation.
func TimedToolCall(
	ctx context.Context,
	agentID string,
	call providers.ToolCall,
	execute func() (string, error),
) (string, error) {
	summary := SummarizeToolArgs(call.Name, call.Arguments)
	fullArgs := PrettyPrintArgs(call.Arguments)
	start := time.Now()

	EmitToolCall(ctx, ToolCallEvent{
		Phase:       ToolCallStart,
		ToolName:    call.Name,
		ArgsSummary: summary,
		FullArgs:    fullArgs,
		AgentID:     agentID,
		StartedAt:   start,
	})

	result, err := execute()
	output, success, errorMsg := toolCallCompletionOutcome(result, err)

	event := ToolCallEvent{
		Phase:       ToolCallComplete,
		ToolName:    call.Name,
		ArgsSummary: summary,
		FullArgs:    fullArgs,
		Output:      TruncateOutput(output, maxOutputBytes),
		AgentID:     agentID,
		StartedAt:   start,
		Duration:    time.Since(start),
		Success:     success,
		ErrorMsg:    errorMsg,
	}
	EmitToolCall(ctx, event)

	return result, err
}

func toolCallCompletionOutcome(result string, err error) (string, bool, string) {
	if err == nil {
		return result, true, ""
	}
	if payload, ok := toolCallControlPayload(err); ok {
		if strings.TrimSpace(result) == "" {
			result = payload
		}
		return result, true, ""
	}
	return result, false, err.Error()
}

func toolCallControlPayload(err error) (string, bool) {
	switch {
	case errors.Is(err, skills.ErrRerouteRequested):
		return `{"rerouted":true}`, true
	case errors.Is(err, skills.ErrDelegatedRequested):
		if payload, marshalErr := skills.MarshalDelegatedPayload(err); marshalErr == nil && strings.TrimSpace(payload) != "" {
			return payload, true
		}
		return `{"delegated":true}`, true
	default:
		return "", false
	}
}

// PrettyPrintArgs formats JSON args with indentation for the expanded view.
// Returns the original string if parsing fails.
func PrettyPrintArgs(rawJSON string) string {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" || rawJSON == "{}" {
		return rawJSON
	}
	var parsed any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return rawJSON
	}
	indented, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return rawJSON
	}
	return string(indented)
}

// TruncateOutput returns the first maxBytes bytes of s, appending "..." if truncated.
// Avoids splitting multi-byte runes by backing up to the last valid boundary.
func TruncateOutput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 3 {
		return "..."
	}
	// Back up to avoid splitting a UTF-8 sequence.
	cut := maxBytes - 3
	for cut > 0 && cut < len(s) && !isUTF8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// isUTF8Start reports whether b is either ASCII or the start of a multi-byte rune.
func isUTF8Start(b byte) bool {
	return b&0xC0 != 0x80
}

// SummarizeToolArgs extracts a compact one-liner from JSON args.
// Priority keys (path, pattern, query, command, etc.) are checked first.
// Falls back to the first string value found. Truncated to maxArgsSummaryLen.
func SummarizeToolArgs(toolName, rawJSON string) string {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" || rawJSON == "{}" {
		return ""
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return ""
	}

	// Check priority keys first.
	for _, key := range priorityArgKeys {
		if val, ok := parsed[key]; ok {
			if s := stringifyArgValue(val); s != "" {
				return truncateArgSummary(key + "=" + s)
			}
		}
	}

	// Fallback: first string value.
	for key, val := range parsed {
		if s := stringifyArgValue(val); s != "" {
			return truncateArgSummary(key + "=" + s)
		}
	}

	return ""
}

// stringifyArgValue converts a JSON value to a compact string representation.
func stringifyArgValue(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// truncateArgSummary truncates an argument summary line to maxArgsSummaryLen.
func truncateArgSummary(s string) string {
	runes := []rune(s)
	if len(runes) <= maxArgsSummaryLen {
		return s
	}
	return string(runes[:maxArgsSummaryLen-1]) + "…"
}
