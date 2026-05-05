package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// FrameReader reads newline-delimited JSON-RPC messages from an io.Reader.
//
// MCP's stdio transport is one JSON message per line. Streamable-HTTP and SSE
// transports parse messages out of higher-level frames before delegating to
// this codec. Buffer growth is bounded by maxFrameBytes — required so a peer
// (or attacker) can't exhaust memory by streaming an unterminated line.
type FrameReader struct {
	scanner *bufio.Scanner
	closer  io.Closer
}

// FrameWriter emits newline-delimited JSON-RPC messages.
type FrameWriter struct {
	writer io.Writer
	closer io.Closer
	enc    *json.Encoder
}

// maxFrameBytes caps a single MCP frame at 16 MiB. The constant is derived
// from the resource-read out-of-band handoff: any payload larger than this
// must be served as a resource reference, not inlined into a tool/prompt
// response. Keeping the codec's hard cap aligned with the blob-store
// threshold means the only way a frame exceeds it is a buggy peer.
const maxFrameBytes = 16 * 1024 * 1024

// NewFrameReader wraps r in a Scanner sized for MCP frames.
func NewFrameReader(r io.Reader) *FrameReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	closer, _ := r.(io.Closer)
	return &FrameReader{scanner: scanner, closer: closer}
}

// Read returns the next decoded message or io.EOF on clean stream end.
func (f *FrameReader) Read() (*Message, error) {
	if !f.scanner.Scan() {
		if err := f.scanner.Err(); err != nil {
			return nil, fmt.Errorf("mcp: framing error: %w", err)
		}
		return nil, io.EOF
	}
	bytes := f.scanner.Bytes()
	if len(bytes) == 0 {
		return f.Read()
	}
	msg := &Message{}
	if err := json.Unmarshal(bytes, msg); err != nil {
		return nil, fmt.Errorf("mcp: decode message: %w", err)
	}
	if err := validateMessage(msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// Close closes the underlying reader if it is closeable.
func (f *FrameReader) Close() error {
	if f.closer != nil {
		return f.closer.Close()
	}
	return nil
}

// NewFrameWriter wraps w with an internal encoder configured to emit one
// JSON object per line.
func NewFrameWriter(w io.Writer) *FrameWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	closer, _ := w.(io.Closer)
	return &FrameWriter{writer: w, closer: closer, enc: enc}
}

// Write serializes msg as a single newline-terminated JSON line.
func (f *FrameWriter) Write(msg *Message) error {
	if err := validateMessage(msg); err != nil {
		return err
	}
	return f.enc.Encode(msg)
}

// Close closes the underlying writer if it is closeable.
func (f *FrameWriter) Close() error {
	if f.closer != nil {
		return f.closer.Close()
	}
	return nil
}

// validateMessage enforces JSON-RPC 2.0 envelope invariants. Each branch is
// a single conditional so cyclomatic complexity stays trivial.
func validateMessage(m *Message) error {
	if m == nil {
		return errors.New("mcp: nil message")
	}
	if m.JSONRPC != JSONRPCVersion {
		return fmt.Errorf("mcp: jsonrpc version mismatch: got %q, want %q", m.JSONRPC, JSONRPCVersion)
	}
	return validateMessageShape(m)
}

func validateMessageShape(m *Message) error {
	hasID := m.ID != nil
	hasMethod := m.Method != ""
	hasResult := len(m.Result) > 0
	hasError := m.Error != nil
	if hasMethod && (hasResult || hasError) {
		return errors.New("mcp: a method message must not also carry a result or error")
	}
	if !hasMethod && !hasResult && !hasError {
		return errors.New("mcp: message must be a request, notification, or response")
	}
	if !hasMethod && !hasID {
		return errors.New("mcp: response without id")
	}
	if hasMethod && IsNotification(m.Method) && hasID {
		return fmt.Errorf("mcp: notification %q must not carry an id", m.Method)
	}
	return nil
}

// NewRequest constructs an initialized JSON-RPC request envelope.
func NewRequest(id RequestID, method string, params any) (*Message, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	return &Message{JSONRPC: JSONRPCVersion, ID: &id, Method: method, Params: raw}, nil
}

// NewNotification constructs an envelope without an id (notification).
func NewNotification(method string, params any) (*Message, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	return &Message{JSONRPC: JSONRPCVersion, Method: method, Params: raw}, nil
}

// NewResponse constructs a successful response envelope.
func NewResponse(id RequestID, result any) (*Message, error) {
	raw, err := marshalParams(result)
	if err != nil {
		return nil, err
	}
	return &Message{JSONRPC: JSONRPCVersion, ID: &id, Result: raw}, nil
}

// NewErrorResponse constructs a JSON-RPC error response envelope.
func NewErrorResponse(id RequestID, code int, message string, data any) (*Message, error) {
	rpcErr := &RPCError{Code: code, Message: message}
	if data != nil {
		raw, err := marshalParams(data)
		if err != nil {
			return nil, err
		}
		rpcErr.Data = raw
	}
	return &Message{JSONRPC: JSONRPCVersion, ID: &id, Error: rpcErr}, nil
}

func marshalParams(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(v)
}
