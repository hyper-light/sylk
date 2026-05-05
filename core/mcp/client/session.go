// Package client implements per-server MCP client sessions.
//
// One Session corresponds to one connected MCP server. The session owns:
//   - the Transport,
//   - request/response correlation across the wire,
//   - capability negotiation,
//   - notification dispatch (list_changed, progress, log),
//   - the inbound dispatcher goroutine (registered with the caller's scope so
//     leak detection still applies).
//
// Tool/resource/prompt compilation happens above this layer in core/mcp/compiler;
// session.go only knows how to speak the wire protocol.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/transport"
)

// Session is one client-side MCP connection.
type Session struct {
	cfg SessionConfig
	tx  transport.Transport

	// pending holds in-flight outgoing requests keyed by request id.
	pendingMu sync.Mutex
	pending   map[string]chan *mcp.Message

	// notif is the outbound notifications fan-out. Subscribe attaches a
	// caller-owned channel that receives every notification. The channel
	// MUST be drained promptly; the session uses non-blocking sends and
	// drops on a full subscriber to protect the dispatcher.
	notifMu     sync.RWMutex
	notifSubs   map[uint64]chan<- *mcp.Message
	notifNextID atomic.Uint64

	// state lifecycle
	startedMu sync.Mutex
	started   bool
	closeMu   sync.Mutex
	closed    bool

	// negotiation outputs
	negotiated   atomic.Bool
	serverInfo   atomic.Pointer[mcp.Implementation]
	serverCaps   atomic.Pointer[mcp.ServerCapabilities]
	instructions atomic.Pointer[string]

	// idCounter generates monotonically increasing integer ids unique
	// to this session — sufficient because id collision only matters
	// per-connection per the JSON-RPC spec.
	idCounter atomic.Int64

	// requestTimeout is the per-request soft cap. A caller's ctx still
	// dominates; this is only hit when the caller passed context.Background.
	requestTimeout time.Duration
}

// SessionConfig configures a Session.
type SessionConfig struct {
	// ServerID is the canonical id Sylk uses for this server (catalog id).
	ServerID string
	// Transport carries the wire protocol.
	Transport transport.Transport
	// Scope owns the dispatcher goroutine.
	Scope *concurrency.GoroutineScope
	// ClientInfo is sent in the initialize handshake.
	ClientInfo mcp.Implementation
	// ClientCapabilities is sent in the initialize handshake.
	ClientCapabilities mcp.ClientCapabilities
	// ProtocolVersion pins the negotiated protocol version, defaulting
	// to mcp.ProtocolVersionLatest when empty.
	ProtocolVersion string
	// RequestTimeout caps a single request when the caller did not bring
	// their own deadline. Required to avoid pending-map growth on a hung
	// server.
	RequestTimeout time.Duration
}

const defaultRequestTimeout = 60 * time.Second

// NewSession constructs an unstarted session.
func NewSession(cfg SessionConfig) (*Session, error) {
	if cfg.Transport == nil {
		return nil, errors.New("mcp client: transport is required")
	}
	if cfg.Scope == nil {
		return nil, errors.New("mcp client: scope is required")
	}
	if cfg.ServerID == "" {
		return nil, errors.New("mcp client: server id is required")
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	if cfg.ProtocolVersion == "" {
		cfg.ProtocolVersion = mcp.ProtocolVersionLatest
	}
	return &Session{
		cfg:            cfg,
		tx:             cfg.Transport,
		pending:        make(map[string]chan *mcp.Message),
		notifSubs:      make(map[uint64]chan<- *mcp.Message),
		requestTimeout: timeout,
	}, nil
}

// Start brings up the transport, spawns the inbound dispatcher, and
// performs the initialize handshake.
func (s *Session) Start(ctx context.Context) error {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()
	if s.started {
		return errors.New("mcp client: session already started")
	}
	if err := s.tx.Start(ctx); err != nil {
		return fmt.Errorf("mcp client: transport start: %w", err)
	}
	if err := s.cfg.Scope.Go("mcp.client.dispatcher."+s.cfg.ServerID, 0, s.runDispatcher); err != nil {
		return fmt.Errorf("mcp client: schedule dispatcher: %w", err)
	}
	s.started = true
	if err := s.handshake(ctx); err != nil {
		_ = s.Close()
		return err
	}
	return nil
}

// Close tears down the session, transport, and dispatcher.
func (s *Session) Close() error {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return nil
	}
	s.closed = true
	s.closeMu.Unlock()
	s.failPending(errors.New("mcp client: session closed"))
	return s.tx.Close()
}

// IsClosed reports whether Close has been called.
func (s *Session) IsClosed() bool {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	return s.closed
}

// ServerID returns the catalog identifier of this connection.
func (s *Session) ServerID() string { return s.cfg.ServerID }

// ServerInfo returns the server info learned during initialize.
func (s *Session) ServerInfo() mcp.Implementation {
	info := s.serverInfo.Load()
	if info == nil {
		return mcp.Implementation{}
	}
	return *info
}

// Capabilities returns the negotiated server capabilities.
func (s *Session) Capabilities() mcp.ServerCapabilities {
	caps := s.serverCaps.Load()
	if caps == nil {
		return mcp.ServerCapabilities{}
	}
	return *caps
}

// Instructions returns server-provided usage instructions, if any.
func (s *Session) Instructions() string {
	ptr := s.instructions.Load()
	if ptr == nil {
		return ""
	}
	return *ptr
}

// SubscribeNotifications attaches a non-blocking notification channel.
// Returns a function to unsubscribe.
func (s *Session) SubscribeNotifications(ch chan<- *mcp.Message) (unsubscribe func()) {
	id := s.notifNextID.Add(1)
	s.notifMu.Lock()
	s.notifSubs[id] = ch
	s.notifMu.Unlock()
	return func() {
		s.notifMu.Lock()
		delete(s.notifSubs, id)
		s.notifMu.Unlock()
	}
}

// runDispatcher reads frames off the transport and either resolves a pending
// request or fans out the notification.
func (s *Session) runDispatcher(ctx context.Context) error {
	for {
		msg, err := s.tx.Receive(ctx)
		if err != nil {
			s.failPending(err)
			return nil
		}
		s.dispatchInbound(msg)
	}
}

func (s *Session) dispatchInbound(msg *mcp.Message) {
	if msg.ID != nil && (len(msg.Result) > 0 || msg.Error != nil) {
		s.resolvePending(msg)
		return
	}
	if msg.Method != "" && msg.ID == nil {
		s.fanoutNotification(msg)
		return
	}
	if msg.Method != "" && msg.ID != nil {
		// Server→client request (e.g. elicitation/create, sampling/createMessage).
		s.fanoutNotification(msg)
		return
	}
}

func (s *Session) resolvePending(msg *mcp.Message) {
	key := msg.ID.String()
	s.pendingMu.Lock()
	ch, ok := s.pending[key]
	if ok {
		delete(s.pending, key)
	}
	s.pendingMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

func (s *Session) fanoutNotification(msg *mcp.Message) {
	s.notifMu.RLock()
	defer s.notifMu.RUnlock()
	for _, sub := range s.notifSubs {
		select {
		case sub <- msg:
		default:
			// Slow subscriber: drop rather than block the dispatcher.
			// Subscribers that need lossless delivery should buffer
			// or upgrade to a per-server queue.
		}
	}
}

func (s *Session) failPending(err error) {
	s.pendingMu.Lock()
	pending := s.pending
	s.pending = make(map[string]chan *mcp.Message)
	s.pendingMu.Unlock()
	for _, ch := range pending {
		errResp, _ := mcp.NewErrorResponse(mcp.NewIntRequestID(0), mcp.ErrCodeInternalError, err.Error(), nil)
		select {
		case ch <- errResp:
		default:
		}
	}
}

// Call sends a request and waits for the matching response. The caller's
// context dominates; on cancellation the request slot is released and the
// reply (if it arrives later) is dropped.
func (s *Session) Call(ctx context.Context, method string, params, result any) error {
	if s.IsClosed() {
		return transport.ErrTransportClosed
	}
	id := s.nextID()
	req, err := mcp.NewRequest(id, method, params)
	if err != nil {
		return err
	}
	respCh := make(chan *mcp.Message, 1)
	s.pendingMu.Lock()
	s.pending[id.String()] = respCh
	s.pendingMu.Unlock()
	if err := s.tx.Send(ctx, req); err != nil {
		s.cancelPending(id.String())
		return err
	}
	return s.awaitResponse(ctx, id.String(), respCh, result)
}

func (s *Session) awaitResponse(
	ctx context.Context,
	key string,
	respCh chan *mcp.Message,
	result any,
) error {
	deadline, cancel := s.deadlineFor(ctx)
	defer cancel()
	select {
	case <-deadline.Done():
		s.cancelPending(key)
		if errors.Is(deadline.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("mcp client: request timeout after %s", s.requestTimeout)
		}
		return deadline.Err()
	case msg := <-respCh:
		return decodeResult(msg, result)
	}
}

func (s *Session) deadlineFor(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, s.requestTimeout)
}

func decodeResult(msg *mcp.Message, result any) error {
	if msg.Error != nil {
		return msg.Error
	}
	if result == nil || len(msg.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(msg.Result, result); err != nil {
		return fmt.Errorf("mcp client: decode result: %w", err)
	}
	return nil
}

func (s *Session) cancelPending(key string) {
	s.pendingMu.Lock()
	delete(s.pending, key)
	s.pendingMu.Unlock()
}

// Notify sends a notification (no response expected).
func (s *Session) Notify(ctx context.Context, method string, params any) error {
	if s.IsClosed() {
		return transport.ErrTransportClosed
	}
	msg, err := mcp.NewNotification(method, params)
	if err != nil {
		return err
	}
	return s.tx.Send(ctx, msg)
}

func (s *Session) nextID() mcp.RequestID {
	return mcp.NewIntRequestID(s.idCounter.Add(1))
}

// handshake performs the initialize / initialized round-trip.
func (s *Session) handshake(ctx context.Context) error {
	params := mcp.InitializeParams{
		ProtocolVersion: s.cfg.ProtocolVersion,
		Capabilities:    s.cfg.ClientCapabilities,
		ClientInfo:      s.cfg.ClientInfo,
	}
	var result mcp.InitializeResult
	if err := s.Call(ctx, mcp.MethodInitialize, params, &result); err != nil {
		return fmt.Errorf("mcp client: initialize: %w", err)
	}
	info := result.ServerInfo
	caps := result.Capabilities
	instr := result.Instructions
	s.serverInfo.Store(&info)
	s.serverCaps.Store(&caps)
	s.instructions.Store(&instr)
	s.negotiated.Store(true)
	return s.Notify(ctx, mcp.MethodInitialized, struct{}{})
}
