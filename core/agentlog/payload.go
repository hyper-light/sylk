package agentlog

// LifecyclePayload records agent lifecycle transitions.
type LifecyclePayload struct {
	Phase   string `json:"phase"`
	Details string `json:"details,omitempty"`
}

// BusMessagePayload records bus message events.
type BusMessagePayload struct {
	MessageID     string `json:"msg_id"`
	CorrelationID string `json:"corr_id"`
	MessageType   string `json:"msg_type"`
	SourceAgent   string `json:"src,omitempty"`
	TargetAgent   string `json:"tgt,omitempty"`
	Topic         string `json:"topic"`
	DurationNs    int64  `json:"dur_ns,omitempty"`
	Error         string `json:"err,omitempty"`
}

// LLMPayload records LLM API call metrics.
type LLMPayload struct {
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	InputTokens  int    `json:"in_tok"`
	OutputTokens int    `json:"out_tok"`
	DurationNs   int64  `json:"dur_ns"`
	Error        string `json:"err,omitempty"`
}

// SkillPayload records skill invocation events.
type SkillPayload struct {
	Name       string `json:"name"`
	DurationNs int64  `json:"dur_ns"`
	Error      string `json:"err,omitempty"`
}

// ErrorPayload records agent-level errors.
type ErrorPayload struct {
	Error string `json:"error"`
	Stack string `json:"stack,omitempty"`
}

// RegistryPayload records agent registration events.
type RegistryPayload struct {
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	Action    string `json:"action"` // "registered" | "unregistered"
}

// AuthPayload records credential change events.
type AuthPayload struct {
	Provider  string `json:"provider"`
	Method    string `json:"method"`
	Available bool   `json:"available"`
}
