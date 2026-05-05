// Package mcp implements Sylk's Model Context Protocol capability plane.
//
// MCP is integrated as a first-class capability plane that:
//   - compiles remote tools into core/skills.Skill instances governed by core/toolruntime,
//   - routes mutating operations through the canonical core/agents/guardian approval surface,
//   - persists every operation to a session-scoped durable journal before transport semantics
//     decide success or failure,
//   - feeds resources, prompts, and outcome signals into core/knowledge and core/forest,
//   - exposes Sylk's own skills as an MCP server bidirectionally without bypassing any
//     of the above.
//
// Wire format follows the MCP 2024-11-05 / 2025-03-26 JSON-RPC 2.0 envelope. The package
// is transport-agnostic: stdio, streamable-HTTP, SSE, and in-process transports satisfy
// the same Transport interface.
package mcp

import (
	"encoding/json"
	"errors"
)

// JSONRPCVersion is the only supported wire-protocol version.
const JSONRPCVersion = "2.0"

// ProtocolVersionLatest pins to the most recent stable spec revision Sylk understands.
// Initialize negotiation accepts older revisions if the peer pins a lower one.
const (
	ProtocolVersion20241105 = "2024-11-05"
	ProtocolVersion20250326 = "2025-03-26"
	ProtocolVersionLatest   = ProtocolVersion20250326
)

// CapabilityKind partitions exported MCP surfaces.
type CapabilityKind string

const (
	CapabilityTool     CapabilityKind = "tool"
	CapabilityResource CapabilityKind = "resource"
	CapabilityPrompt   CapabilityKind = "prompt"
)

// Message is the JSON-RPC 2.0 envelope used in both directions.
//
// Per the spec, a message is exactly one of: request (id+method),
// notification (method, no id), response (id + result), or
// error response (id + error). The validator enforces that.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *RequestID      `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RequestID matches MCP's permissive id type: strings or integers.
// Booleans and floats are rejected; null on a request id is rejected too
// (notifications are signaled by an absent id field, not a null one).
type RequestID struct {
	str   string
	num   int64
	isStr bool
	isNum bool
}

// NewStringRequestID constructs a string-typed request id.
func NewStringRequestID(s string) RequestID { return RequestID{str: s, isStr: true} }

// NewIntRequestID constructs an integer-typed request id.
func NewIntRequestID(n int64) RequestID { return RequestID{num: n, isNum: true} }

// IsString reports whether the id is a string.
func (r RequestID) IsString() bool { return r.isStr }

// IsInt reports whether the id is an integer.
func (r RequestID) IsInt() bool { return r.isNum }

// String returns the request id as a string regardless of underlying kind.
func (r RequestID) String() string {
	if r.isStr {
		return r.str
	}
	if r.isNum {
		return formatInt(r.num)
	}
	return ""
}

// MarshalJSON serializes the id as either a string or a number.
func (r RequestID) MarshalJSON() ([]byte, error) {
	switch {
	case r.isStr:
		return json.Marshal(r.str)
	case r.isNum:
		return json.Marshal(r.num)
	}
	return []byte("null"), nil
}

// UnmarshalJSON accepts string or integer ids per the JSON-RPC 2.0 spec.
func (r *RequestID) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return errors.New("mcp: request id must not be null")
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		r.str = s
		r.isStr = true
		r.isNum = false
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return errors.New("mcp: request id must be string or integer")
	}
	r.num = n
	r.isNum = true
	r.isStr = false
	return nil
}

// RPCError mirrors the JSON-RPC 2.0 error envelope.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error satisfies the error interface for transport-side handling.
func (e *RPCError) Error() string { return e.Message }

// Standard MCP / JSON-RPC error codes. Mirroring spec values rather than re-inventing.
const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// MCP-specific error codes start at -32000 (the JSON-RPC server-error band).
const (
	ErrCodeMCPCapabilityNotFound = -32000
	ErrCodeMCPGuardianDenied     = -32001
	ErrCodeMCPSessionExpired     = -32002
	ErrCodeMCPElicitationCancel  = -32003
	ErrCodeMCPCatalogStale       = -32004
)

// Initialize / capability negotiation.

// InitializeParams is the body of the initialize request.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// InitializeResult is the body of the initialize response.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// Implementation identifies a named software peer.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities is the negotiation block sent to a server.
type ClientCapabilities struct {
	Roots        *RootsCapability       `json:"roots,omitempty"`
	Sampling     *SamplingCapability    `json:"sampling,omitempty"`
	Elicitation  *ElicitationCapability `json:"elicitation,omitempty"`
	Experimental map[string]any         `json:"experimental,omitempty"`
}

// ServerCapabilities is the negotiation block returned by a server.
type ServerCapabilities struct {
	Logging      *LoggingCapability       `json:"logging,omitempty"`
	Prompts      *PromptsCapability       `json:"prompts,omitempty"`
	Resources    *ResourcesCapability     `json:"resources,omitempty"`
	Tools        *ToolsCapability         `json:"tools,omitempty"`
	Completions  *CompletionsCapability   `json:"completions,omitempty"`
	Experimental map[string]any           `json:"experimental,omitempty"`
}

// RootsCapability declares whether the client honours roots/list_changed.
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// SamplingCapability is a placeholder marker; sampling carries no params here.
type SamplingCapability struct{}

// ElicitationCapability is a placeholder marker.
type ElicitationCapability struct{}

// LoggingCapability is a placeholder marker.
type LoggingCapability struct{}

// PromptsCapability declares prompt support and notification opt-ins.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability declares resource support and notification opt-ins.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// ToolsCapability declares tool support and notification opt-ins.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// CompletionsCapability declares argument-completion support.
type CompletionsCapability struct{}

// Tool / Resource / Prompt descriptors as sent on the wire.

// ToolDescriptor is the wire form of a remote tool.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *ToolHints      `json:"annotations,omitempty"`
}

// ToolHints carry the optional MCP tool hint annotations
// (readOnly, idempotent, openWorld, etc). Used as effect inference inputs.
type ToolHints struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// ResourceDescriptor is the wire form of a remote resource.
type ResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// PromptDescriptor is the wire form of a remote prompt template.
type PromptDescriptor struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Arguments   []PromptArgumentDef `json:"arguments,omitempty"`
}

// PromptArgumentDef describes one prompt argument.
type PromptArgumentDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// ContentBlock is the union returned in tool/prompt/resource responses.
// Type is required; the active payload is identified by Type.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`     // base64 for binary
	MIMEType string          `json:"mimeType,omitempty"`
	URI      string          `json:"uri,omitempty"`
	Resource *ResourceContent `json:"resource,omitempty"`
}

// ResourceContent is the inline resource form of a content block.
type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// CallToolParams is the body of tools/call.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the response of tools/call.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
	Meta    json.RawMessage `json:"_meta,omitempty"`
}

// ListToolsResult is the response of tools/list.
type ListToolsResult struct {
	Tools      []ToolDescriptor `json:"tools"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// ListResourcesResult is the response of resources/list.
type ListResourcesResult struct {
	Resources  []ResourceDescriptor `json:"resources"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

// ReadResourceParams is the body of resources/read.
type ReadResourceParams struct {
	URI string `json:"uri"`
}

// ReadResourceResult is the response of resources/read.
type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
}

// ListPromptsResult is the response of prompts/list.
type ListPromptsResult struct {
	Prompts    []PromptDescriptor `json:"prompts"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

// GetPromptParams is the body of prompts/get.
type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// GetPromptResult is the response of prompts/get.
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptMessage is one message returned by a prompt template.
type PromptMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ProgressNotification is the body of $/progress (or notifications/progress).
type ProgressNotification struct {
	ProgressToken any     `json:"progressToken"`
	Progress      float64 `json:"progress"`
	Total         float64 `json:"total,omitempty"`
}

// LogMessageNotification is the body of notifications/message.
type LogMessageNotification struct {
	Level  string          `json:"level"`
	Logger string          `json:"logger,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// formatInt avoids strconv.Itoa for a single-digit allocation; small helper.
func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
