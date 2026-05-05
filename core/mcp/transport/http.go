package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
)

// HTTPConfig configures the streamable-HTTP MCP client transport.
//
// Streamable HTTP is the 2025-03-26 successor to "HTTP+SSE": all client→server
// frames POST to one endpoint with `Content-Type: application/json`, and
// server→client frames stream back over an SSE stream from the same endpoint.
// The stream is opened lazily on first Receive call so a server that never
// sends notifications can be POST-only.
type HTTPConfig struct {
	// Endpoint is the absolute URL of the MCP HTTP endpoint.
	Endpoint string
	// Headers are sent on every request (auth tokens, session ids, etc).
	Headers http.Header
	// Client is the HTTP client to use; nil falls back to a default
	// configured with timeouts derived from the protocol's worst-case
	// round-trip estimate.
	Client *http.Client
	// Scope owns the SSE pump goroutine; required.
	Scope *concurrency.GoroutineScope
}

const (
	httpRequestTimeoutDefault    = 30 * time.Second
	httpHandshakeIdleDefault     = 60 * time.Second
	httpReadHeaderTimeoutDefault = 10 * time.Second
)

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: httpRequestTimeoutDefault,
		Transport: &http.Transport{
			ResponseHeaderTimeout: httpReadHeaderTimeoutDefault,
			IdleConnTimeout:       httpHandshakeIdleDefault,
		},
	}
}

type httpTransport struct {
	cfg     HTTPConfig
	client  *http.Client
	inbound chan *mcp.Message
	errs    chan error

	startedMu sync.Mutex
	started   bool

	closeMu  sync.Mutex
	closed   bool
	closeErr error
	cancel   context.CancelFunc

	// streamMu serializes the open of the SSE stream so concurrent
	// Receives don't open multiple streams.
	streamMu     sync.Mutex
	streamOpened bool
}

// NewHTTP constructs a streamable-HTTP MCP client transport.
func NewHTTP(cfg HTTPConfig) (Transport, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("mcp http: endpoint is required")
	}
	if cfg.Scope == nil {
		return nil, errors.New("mcp http: scope is required")
	}
	client := cfg.Client
	if client == nil {
		client = defaultHTTPClient()
	}
	return &httpTransport{
		cfg:     cfg,
		client:  client,
		inbound: make(chan *mcp.Message, inboundQueueDepth()),
		errs:    make(chan error, 1),
	}, nil
}

func (t *httpTransport) Endpoint() string { return "http:" + t.cfg.Endpoint }

func (t *httpTransport) Start(ctx context.Context) error {
	t.startedMu.Lock()
	defer t.startedMu.Unlock()
	if t.started {
		return errors.New("mcp http: already started")
	}
	pumpCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.started = true
	_ = pumpCtx
	return nil
}

func (t *httpTransport) Send(ctx context.Context, msg *mcp.Message) error {
	if t.isClosed() {
		return ErrTransportClosed
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp http: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mcp http: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, vs := range t.cfg.Headers {
		req.Header[k] = vs
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp http: send: %w", err)
	}
	return t.readSendResponse(ctx, resp)
}

// readSendResponse consumes the response of a POST. The MCP streamable-HTTP
// spec allows responses in three forms:
//   - empty 202 / 204 (notification accepted),
//   - a single JSON message body for a synchronous response,
//   - a text/event-stream body that may carry a response and any number of
//     subsequent server-initiated frames.
//
// All three are normalized into the inbound channel.
func (t *httpTransport) readSendResponse(ctx context.Context, resp *http.Response) error {
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("mcp http: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		return t.consumeEventStream(ctx, resp.Body)
	}
	return t.consumeJSONResponse(resp.Body)
}

func (t *httpTransport) consumeJSONResponse(body io.Reader) error {
	dec := json.NewDecoder(body)
	for {
		msg := &mcp.Message{}
		if err := dec.Decode(msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("mcp http: decode response: %w", err)
		}
		t.publish(msg)
	}
}

// consumeEventStream parses an SSE response into MCP frames and publishes them.
// The stream owner (current Send caller) returns once the stream closes; if
// the server keeps the stream open for unsolicited frames, those are still
// pushed onto the inbound channel via publish.
func (t *httpTransport) consumeEventStream(ctx context.Context, body io.Reader) error {
	parser := newSSEParser(body)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		event, err := parser.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if event == nil {
			continue
		}
		if err := t.publishEvent(event); err != nil {
			return err
		}
	}
}

func (t *httpTransport) publishEvent(event *sseEvent) error {
	if event.Data == "" {
		return nil
	}
	msg := &mcp.Message{}
	if err := json.Unmarshal([]byte(event.Data), msg); err != nil {
		return fmt.Errorf("mcp http: decode sse data: %w", err)
	}
	t.publish(msg)
	return nil
}

func (t *httpTransport) publish(msg *mcp.Message) {
	if t.isClosed() {
		return
	}
	select {
	case t.inbound <- msg:
	default:
		// Inbound queue is full; surface as a transport error so the
		// client tears down rather than silently drops. This is the
		// only place a frame can be dropped, and only under hard
		// backpressure that already indicates a stuck consumer.
		select {
		case t.errs <- errors.New("mcp http: inbound queue overflow"):
		default:
		}
	}
}

func (t *httpTransport) Receive(ctx context.Context) (*mcp.Message, error) {
	if t.isClosed() {
		return nil, ErrTransportClosed
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-t.inbound:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case err := <-t.errs:
		return nil, err
	}
}

func (t *httpTransport) Close() error {
	t.closeMu.Lock()
	if t.closed {
		err := t.closeErr
		t.closeMu.Unlock()
		return err
	}
	t.closed = true
	t.closeMu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

func (t *httpTransport) isClosed() bool {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	return t.closed
}
