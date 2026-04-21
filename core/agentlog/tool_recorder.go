package agentlog

import (
	"encoding/json"
	"time"
)

// ToolRecorder writes Phase 0 (start) and Phase 1 (complete) records
// for every tool call an agent invokes. Two records per call is
// intentional: if the process crashes between dispatch and
// completion, the start record preserves exactly what was attempted.
// The records share a tool_call_key and are reconciled at read time
// by the sylk trace CLI.
//
// A ToolRecorder wraps a StreamWriter targeting tools.{nnn}.jsonl
// within the agent's log directory tree. Writes are best-effort — a
// failure to serialize or append does not halt the tool loop.
type ToolRecorder struct {
	writer *StreamWriter
}

// NewToolRecorder returns a ToolRecorder that writes through the
// given StreamWriter. The writer should target the "tools" stream
// name. A nil writer yields a recorder whose methods are all no-ops,
// which is convenient for tests and unconfigured agents.
func NewToolRecorder(w *StreamWriter) *ToolRecorder {
	return &ToolRecorder{writer: w}
}

// ToolCallPhase is "start" or "complete". Values are fixed so the
// wire format is stable across release boundaries.
type ToolCallPhase string

const (
	ToolCallPhaseStart    ToolCallPhase = "start"
	ToolCallPhaseComplete ToolCallPhase = "complete"
)

// ToolInterAgentRef captures the inter-agent metadata stamped on a
// consult / challenge / approval / store tool call. Mirrors the
// shape used by the UI's InterAgentToolEventMsg but without a UI
// dependency.
type ToolInterAgentRef struct {
	Kind       string   `json:"kind,omitempty"`
	AgentTypes []string `json:"agent_types,omitempty"`
	ThreadKey  string   `json:"thread_key,omitempty"`
	Status     string   `json:"status,omitempty"`
}

// ToolStartRecord is the Phase 0 record written when a tool call
// dispatches. Field names are stable; additive changes only.
type ToolStartRecord struct {
	Timestamp          time.Time          `json:"ts"`
	Phase              ToolCallPhase      `json:"phase"`
	ToolCallKey        string             `json:"tool_call_key"`
	ParentToolCallKey  string             `json:"parent_tool_call_key,omitempty"`
	CorrelationID      string             `json:"correlation_id,omitempty"`
	LLMCallID          string             `json:"llm_call_id,omitempty"`
	CausedByActivity   string             `json:"caused_by_activity,omitempty"`
	AgentID            string             `json:"agent_id,omitempty"`
	AgentType          string             `json:"agent_type,omitempty"`
	PipelineID         string             `json:"pipeline_id,omitempty"`
	SessionID          string             `json:"session_id,omitempty"`
	ToolName           string             `json:"tool_name"`
	Args               json.RawMessage    `json:"args,omitempty"`
	InterAgent         *ToolInterAgentRef `json:"inter_agent,omitempty"`
	StartedAt          time.Time          `json:"started_at,omitempty"`
}

// ToolCompleteRecord is the Phase 1 record written when a tool call
// returns, errors, or is explicitly cancelled. child_tool_call_keys
// is populated when the caller tracks nested dispatches; if not
// known it can be left empty and reconstructed from
// parent_tool_call_key backreferences at trace time.
type ToolCompleteRecord struct {
	Timestamp            time.Time          `json:"ts"`
	Phase                ToolCallPhase      `json:"phase"`
	ToolCallKey          string             `json:"tool_call_key"`
	CorrelationID        string             `json:"correlation_id,omitempty"`
	LLMCallID            string             `json:"llm_call_id,omitempty"`
	AgentID              string             `json:"agent_id,omitempty"`
	AgentType            string             `json:"agent_type,omitempty"`
	PipelineID           string             `json:"pipeline_id,omitempty"`
	SessionID            string             `json:"session_id,omitempty"`
	ToolName             string             `json:"tool_name"`
	Output               json.RawMessage    `json:"output,omitempty"`
	OutputTruncatedFrom  int                `json:"output_truncated_from,omitempty"`
	DurationMs           int64              `json:"duration_ms"`
	Success              bool               `json:"success"`
	Error                string             `json:"error,omitempty"`
	InterAgent           *ToolInterAgentRef `json:"inter_agent,omitempty"`
	ChildToolCallKeys    []string           `json:"child_tool_call_keys,omitempty"`
}

// Start writes a Phase 0 record. Safe to call on a nil recorder
// (no-op). A nil args value is omitted; a non-nil value is marshaled
// and written through the underlying writer's redactor.
func (r *ToolRecorder) Start(record ToolStartRecord) error {
	if r == nil || r.writer == nil {
		return nil
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	record.Phase = ToolCallPhaseStart
	return r.writer.Write(record)
}

// Complete writes a Phase 1 record. Safe to call on a nil recorder.
func (r *ToolRecorder) Complete(record ToolCompleteRecord) error {
	if r == nil || r.writer == nil {
		return nil
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	record.Phase = ToolCallPhaseComplete
	return r.writer.Write(record)
}

// MarshalToolArgs serializes an arbitrary args value to JSON for
// embedding in a ToolStartRecord.Args field. Returns a zero-length
// message on marshal failure rather than propagating the error —
// logging must not block the tool loop.
func MarshalToolArgs(args any) json.RawMessage {
	if args == nil {
		return nil
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return json.RawMessage(data)
}

// TruncateOutput returns the given output data and a "truncated from"
// count suitable for embedding in ToolCompleteRecord. When the input
// exceeds the cap it is truncated with a trailing ellipsis marker
// and the original length is returned so trace tooling can flag
// that the stored form is abbreviated.
func TruncateOutput(output json.RawMessage, cap int) (json.RawMessage, int) {
	if cap <= 0 || len(output) <= cap {
		return output, 0
	}
	original := len(output)
	truncated := make(json.RawMessage, 0, cap+len(outputTruncationMarker))
	truncated = append(truncated, output[:cap]...)
	truncated = append(truncated, outputTruncationMarker...)
	return truncated, original
}

var outputTruncationMarker = []byte(`"[TRUNCATED]"`)
