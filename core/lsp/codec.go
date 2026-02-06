package lsp

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ---------------------------------------------------------------------------
// RequestID — polymorphic JSON-RPC 2.0 ID (integer or string)
// ---------------------------------------------------------------------------

// RequestID is a JSON-RPC 2.0 request identifier that can be either an
// integer or a string, per the specification.
type RequestID struct {
	intVal int64
	strVal string
	isStr  bool
}

// IntID creates an integer request ID.
func IntID(n int64) RequestID {
	return RequestID{intVal: n}
}

// StringID creates a string request ID.
func StringID(s string) RequestID {
	return RequestID{strVal: s, isStr: true}
}

// Int returns the integer value and true if the ID is numeric.
func (id RequestID) Int() (int64, bool) {
	if id.isStr {
		return 0, false
	}
	return id.intVal, true
}

// String returns a human-readable representation of the ID.
func (id RequestID) String() string {
	if id.isStr {
		return id.strVal
	}
	return strconv.FormatInt(id.intVal, 10)
}

// MarshalJSON encodes the ID as either a JSON number or JSON string.
func (id RequestID) MarshalJSON() ([]byte, error) {
	if id.isStr {
		return json.Marshal(id.strVal)
	}
	return json.Marshal(id.intVal)
}

// UnmarshalJSON decodes a JSON number or string into the ID.
func (id *RequestID) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty request ID")
	}
	// JSON string starts with quote.
	if data[0] == '"' {
		id.isStr = true
		return json.Unmarshal(data, &id.strVal)
	}
	id.isStr = false
	return json.Unmarshal(data, &id.intVal)
}

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 message types
// ---------------------------------------------------------------------------

const jsonrpcVersion = "2.0"

// Request is a JSON-RPC 2.0 request (has ID, expects a response).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      RequestID       `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// NewRequest builds a Request with the given ID, method, and marshaled params.
func NewRequest(id RequestID, method string, params any) (Request, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return Request{}, err
	}
	return Request{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Method:  method,
		Params:  raw,
	}, nil
}

// Response is a JSON-RPC 2.0 response correlated to a Request by ID.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      RequestID       `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (no ID, no response expected).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// NewNotification builds a Notification with the given method and marshaled params.
func NewNotification(method string, params any) (Notification, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return Notification{}, err
	}
	return Notification{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  raw,
	}, nil
}

// RPCError is the error payload in a JSON-RPC 2.0 response.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// ---------------------------------------------------------------------------
// incomingMessage — lightweight classifier for dispatching raw JSON
// ---------------------------------------------------------------------------

// incomingMessage carries just enough to classify a raw JSON-RPC message
// as a Request, Response, or Notification without fully decoding it.
type incomingMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
}

// messageKind enumerates JSON-RPC 2.0 message classifications.
type messageKind int

const (
	kindResponse     messageKind = iota // has id, no method
	kindRequest                         // has id and method
	kindNotification                    // has method, no id
	kindUnknown                         // unclassifiable
)

// Classify determines the message kind from a partially-decoded incoming message.
func (m *incomingMessage) Classify() messageKind {
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	hasMethod := m.Method != ""

	if hasID && !hasMethod {
		return kindResponse
	}
	if hasID && hasMethod {
		return kindRequest
	}
	if !hasID && hasMethod {
		return kindNotification
	}
	return kindUnknown
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// marshalParams marshals params to json.RawMessage. Nil params returns nil
// (omitted in the JSON output).
func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}
