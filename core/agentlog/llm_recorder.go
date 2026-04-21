package agentlog

import (
	"encoding/json"
	"time"
)

// LLMRecorder writes one record at the start of an LLM round-trip
// and one when it completes. Start records preserve the full input
// context so that a mid-flight crash still leaves enough information
// to replay what the model was asked. Complete records carry the
// response, any emitted tool-call keys, usage, and duration.
//
// System prompts and tool definition bundles are large and repeat
// across every call an agent makes. To keep the stream compact, the
// recorder stores them once per session in a content-addressed
// corpus (see PromptCorpus) and references them by SHA256 from the
// start record. The inline messages array remains in-record because
// it varies per call.
type LLMRecorder struct {
	writer *StreamWriter
	corpus *PromptCorpus
	cfg    LLMRecorderConfig
}

// LLMRecorderConfig controls compaction. Zero values fall back to
// the design-doc defaults: 16 KiB per-message truncation, inline
// messages, corpus-addressed prompts.
type LLMRecorderConfig struct {
	TruncateMessageAt      int
	InlineMessages         bool
	ContentAddressPrompts  bool
}

const defaultTruncateMessageAt = 16 * 1024

// NewLLMRecorder constructs a recorder bound to the given writer
// (stream name "llm") and corpus. Either argument may be nil; a nil
// writer yields a no-op recorder and a nil corpus disables
// content-addressing (prompts inline verbatim).
func NewLLMRecorder(w *StreamWriter, corpus *PromptCorpus, cfg LLMRecorderConfig) *LLMRecorder {
	if cfg.TruncateMessageAt <= 0 {
		cfg.TruncateMessageAt = defaultTruncateMessageAt
	}
	// Defaults: both compaction features on.
	if !cfg.InlineMessages {
		cfg.InlineMessages = true
	}
	if !cfg.ContentAddressPrompts {
		cfg.ContentAddressPrompts = corpus != nil
	}
	return &LLMRecorder{writer: w, corpus: corpus, cfg: cfg}
}

// LLMStartRecord captures what the model is being asked. The system
// prompt and tool definition bundle are written to the corpus once
// per session and referenced by SHA256 hash; the full text of each
// can be recovered via PromptCorpus.Get.
type LLMStartRecord struct {
	Timestamp             time.Time     `json:"ts"`
	Event                 string        `json:"event"`
	LLMCallID             string        `json:"llm_call_id"`
	CorrelationID         string        `json:"correlation_id,omitempty"`
	AgentID               string        `json:"agent_id,omitempty"`
	AgentType             string        `json:"agent_type,omitempty"`
	PipelineID            string        `json:"pipeline_id,omitempty"`
	SessionID             string        `json:"session_id,omitempty"`
	Model                 string        `json:"model,omitempty"`
	Provider              string        `json:"provider,omitempty"`
	SystemPromptSHA256    string        `json:"system_prompt_sha256,omitempty"`
	SystemPrompt          string        `json:"system_prompt,omitempty"`
	ToolDefinitionsSHA256 string        `json:"tool_definitions_sha256,omitempty"`
	ToolDefinitionCount   int           `json:"tool_definition_count,omitempty"`
	Messages              []LLMMessage  `json:"messages,omitempty"`
	MessagesApproxTokens  int           `json:"messages_approx_tokens,omitempty"`
	RequestedMaxTokens    int           `json:"requested_max_tokens,omitempty"`
}

// LLMMessage is the minimal per-message shape embedded in
// LLMStartRecord.Messages. Content is a string (provider formats
// differ; callers should serialize rich content to a string before
// recording). Tool calls/results produced by the model in prior
// turns can be inlined via Extras which is opaque to the recorder.
type LLMMessage struct {
	Role    string          `json:"role"`
	Content string          `json:"content,omitempty"`
	Extras  json.RawMessage `json:"extras,omitempty"`
}

// LLMCompleteRecord captures what the model produced. Empty on
// error; the Error field describes the failure.
type LLMCompleteRecord struct {
	Timestamp        time.Time    `json:"ts"`
	Event            string       `json:"event"`
	LLMCallID        string       `json:"llm_call_id"`
	CorrelationID    string       `json:"correlation_id,omitempty"`
	AgentID          string       `json:"agent_id,omitempty"`
	AgentType        string       `json:"agent_type,omitempty"`
	PipelineID       string       `json:"pipeline_id,omitempty"`
	SessionID        string       `json:"session_id,omitempty"`
	ResponseText     string       `json:"response_text,omitempty"`
	ToolCallsEmitted []string     `json:"tool_calls_emitted,omitempty"`
	Thinking         string       `json:"thinking,omitempty"`
	Usage            *LLMUsage    `json:"usage,omitempty"`
	DurationMs       int64        `json:"duration_ms"`
	Error            string       `json:"error,omitempty"`
}

// LLMUsage mirrors the token accounting commonly returned by
// providers. Nil fields are omitted.
type LLMUsage struct {
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
}

// Start writes an llm_start record. When content-addressing is
// enabled, SystemPrompt and ToolDefinitions on the input are moved
// into the corpus and the record receives SHA references instead of
// inline text. Messages are truncated per cfg.TruncateMessageAt.
//
// Start is safe to call on a nil recorder (no-op).
func (r *LLMRecorder) Start(record LLMStartRecord, toolDefinitionsJSON string) error {
	if r == nil || r.writer == nil {
		return nil
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	record.Event = "llm_start"

	if r.cfg.ContentAddressPrompts && r.corpus != nil {
		if hash, err := r.corpus.Put(record.SystemPrompt); err == nil && hash != "" {
			record.SystemPromptSHA256 = hash
			record.SystemPrompt = ""
		}
		if hash, err := r.corpus.Put(toolDefinitionsJSON); err == nil && hash != "" {
			record.ToolDefinitionsSHA256 = hash
		}
	}

	record.Messages = truncateMessages(record.Messages, r.cfg.TruncateMessageAt)

	return r.writer.Write(record)
}

// Complete writes an llm_complete record. Safe to call on a nil
// recorder (no-op).
func (r *LLMRecorder) Complete(record LLMCompleteRecord) error {
	if r == nil || r.writer == nil {
		return nil
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	record.Event = "llm_complete"
	return r.writer.Write(record)
}

// truncateMessages applies a per-message soft cap. Messages whose
// Content exceeds the cap are replaced with a head-plus-tail
// elision — the first half and last half of the cap, separated by
// a marker — which preserves enough context for replay while
// bounding record size.
func truncateMessages(msgs []LLMMessage, cap int) []LLMMessage {
	if cap <= 0 || len(msgs) == 0 {
		return msgs
	}
	out := make([]LLMMessage, len(msgs))
	for i, m := range msgs {
		if len(m.Content) <= cap {
			out[i] = m
			continue
		}
		half := (cap - len(messageTruncationMarker)) / 2
		if half < 1 {
			half = 1
		}
		head := m.Content[:half]
		tail := m.Content[len(m.Content)-half:]
		out[i] = LLMMessage{
			Role:    m.Role,
			Content: head + messageTruncationMarker + tail,
			Extras:  m.Extras,
		}
	}
	return out
}

const messageTruncationMarker = "\n[... elided — see corpus or increase TruncateMessageAt ...]\n"
