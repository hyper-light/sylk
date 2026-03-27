package providers

import "encoding/json"

// ProviderMetadataNativeWebSearchCallsKey stores serializable native web-search
// calls extracted from provider responses. Academic uses this to enforce
// search-backed research without relying on provider-specific raw metadata.
const ProviderMetadataNativeWebSearchCallsKey = "native_web_search_calls"

// NativeWebSearchCall captures one provider-native web search invocation in a
// provider-agnostic format.
type NativeWebSearchCall struct {
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider,omitempty"`
	Status   string `json:"status,omitempty"`
	Action   string `json:"action,omitempty"`
	Query    string `json:"query,omitempty"`
	URL      string `json:"url,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
}

// ToolCall converts the native web-search call into a generic tool-call shape
// so the existing tool-call UI can render it without a special case.
func (c NativeWebSearchCall) ToolCall() ToolCall {
	return ToolCall{
		ID:        c.ID,
		Name:      "web_search",
		Arguments: c.ArgumentsJSON(),
	}
}

// ArgumentsJSON returns a compact JSON form used for summaries and UI display.
func (c NativeWebSearchCall) ArgumentsJSON() string {
	payload := map[string]string{}
	if c.Query != "" {
		payload["query"] = c.Query
	}
	if c.Action != "" {
		payload["action"] = c.Action
	}
	if c.URL != "" {
		payload["url"] = c.URL
	}
	if c.Pattern != "" {
		payload["pattern"] = c.Pattern
	}
	if c.Status != "" {
		payload["status"] = c.Status
	}
	if len(payload) == 0 {
		return "{}"
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// DecodeNativeWebSearchCalls extracts provider-agnostic native web-search calls
// from response metadata. It accepts both in-memory typed values and
// JSON-round-tripped map/slice forms.
func DecodeNativeWebSearchCalls(metadata map[string]any) []NativeWebSearchCall {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata[ProviderMetadataNativeWebSearchCallsKey]
	if !ok || raw == nil {
		return nil
	}
	if typed, ok := raw.([]NativeWebSearchCall); ok {
		return append([]NativeWebSearchCall(nil), typed...)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var calls []NativeWebSearchCall
	if err := json.Unmarshal(data, &calls); err != nil {
		return nil
	}
	return calls
}
