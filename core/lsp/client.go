package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// ---------------------------------------------------------------------------
// Capacity limits
// ---------------------------------------------------------------------------

// maxPendingRequests caps the number of in-flight requests awaiting responses.
// Derived from: typical editor usage has < 10 concurrent LSP requests;
// 64 provides generous headroom for burst scenarios.
const maxPendingRequests = 64

// diagnosticBufferSize is the channel capacity for outbound diagnostic notifications.
// Derived from: one diagnostic set per open file, typical project < 50 files.
const diagnosticBufferSize = 128

// initializeTimeout is the time allowed for the initialize handshake.
// Derived from: gopls typically initializes in < 5s even on large codebases.
const initializeTimeout = 30 * time.Second

// shutdownTimeout is the time allowed for the shutdown/exit sequence.
// Derived from: most servers respond to shutdown within 500ms; 2s gives headroom.
const shutdownTimeout = 2 * time.Second

// readLoopTimeout is the max lifetime for the reader goroutine.
// Derived from: language servers run for the duration of the editing session.
const readLoopTimeout = 24 * time.Hour

// Sentinel errors.
var (
	errTooManyRequests = errors.New("too many pending requests")
	errClientStopped   = errors.New("client stopped")
	errClientNotReady  = errors.New("client not ready")
)

// ---------------------------------------------------------------------------
// pendingRequest
// ---------------------------------------------------------------------------

// pendingRequest tracks a single in-flight request awaiting its response.
type pendingRequest struct {
	ch     chan Response
	method string
}

// ---------------------------------------------------------------------------
// notifyHandler
// ---------------------------------------------------------------------------

// notifyHandler processes a single server-to-client notification.
type notifyHandler func(c *Client, params json.RawMessage)

// notifyEntry pairs a method name with its handler.
type notifyEntry struct {
	Method  string
	Handler notifyHandler
}

// notifyTable is the table-driven dispatch for server notifications.
var notifyTable = []notifyEntry{
	{Method: MethodPublishDiags, Handler: handlePublishDiagnostics},
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client manages the lifecycle and communication with a single language
// server process. It is safe for concurrent use.
type Client struct {
	definition  *LanguageServerDefinition
	projectRoot string
	syncKind    TextDocumentSyncKind
	documents   *DocumentTracker

	// Process management.
	cmd    *exec.Cmd
	stdin  io.WriteCloser // server stdin pipe (closed on Stop to unblock writes)
	stdout io.ReadCloser  // server stdout pipe (closed on Stop to unblock reads)

	// Transport.
	transport *Transport

	// Request correlation.
	mu      sync.Mutex
	nextID  int64
	pending map[int64]pendingRequest

	// Server capabilities (set after initialize).
	capabilities  ServerCapabilities
	triggerChars  []string // completion trigger characters from server

	// Status (atomic for lock-free reads).
	status atomic.Int32

	// Outbound diagnostic channel (consumed by Manager/Bridge).
	diagnostics chan DiagnosticResult
	dropped     atomic.Int64

	// Notification dispatch (built from notifyTable).
	notifyMap map[string]notifyHandler

	// Lifecycle.
	scope *concurrency.GoroutineScope
	done  chan struct{}
}

// NewClient creates a Client for the given server definition and project root.
// The client starts in StatusStarting state. Call Start() to launch the server.
func NewClient(
	def *LanguageServerDefinition,
	projectRoot string,
	scope *concurrency.GoroutineScope,
) *Client {
	nm := make(map[string]notifyHandler, len(notifyTable))
	for _, entry := range notifyTable {
		nm[entry.Method] = entry.Handler
	}

	c := &Client{
		definition:  def,
		projectRoot: projectRoot,
		documents:   NewDocumentTracker(),
		pending:     make(map[int64]pendingRequest, maxPendingRequests),
		diagnostics: make(chan DiagnosticResult, diagnosticBufferSize),
		notifyMap:   nm,
		scope:       scope,
		done:        make(chan struct{}),
	}
	c.status.Store(int32(StatusStarting))
	return c
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Start launches the server subprocess, starts the background read loop,
// and performs the initialize handshake. The read loop must be running
// before initialize so that the response to the initialize request is
// actually consumed from the transport.
func (c *Client) Start(ctx context.Context) error {
	if err := c.startProcess(); err != nil {
		c.setStatus(StatusError)
		return fmt.Errorf("start server %s: %w", c.definition.ID, err)
	}

	desc := "lsp.readloop." + string(c.definition.ID)
	if err := c.scope.Go(desc, readLoopTimeout, c.readLoop); err != nil {
		c.killProcess()
		c.setStatus(StatusError)
		return fmt.Errorf("start readloop %s: %w", c.definition.ID, err)
	}

	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()

	if err := c.initialize(initCtx); err != nil {
		close(c.done)
		c.killProcess()
		c.setStatus(StatusError)
		return fmt.Errorf("initialize %s: %w", c.definition.ID, err)
	}

	c.setStatus(StatusReady)
	return nil
}

// Stop performs the LSP shutdown/exit sequence and cleans up the process.
func (c *Client) Stop() error {
	if c.Status() == StatusStopped {
		return nil
	}

	// Attempt graceful shutdown while the readloop is still running so it
	// can dispatch the server's shutdown response.
	shutdownErr := c.shutdownExit()

	// Signal readloop to exit, then close pipes to unblock any
	// blocked reads/writes in the readloop before killing the process.
	close(c.done)
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	c.killProcess()
	c.setStatus(StatusStopped)
	c.cancelPending()
	close(c.diagnostics)
	return shutdownErr
}

// Diagnostics returns the read-only channel of diagnostic results.
func (c *Client) Diagnostics() <-chan DiagnosticResult {
	return c.diagnostics
}

// Status returns the current client status.
func (c *Client) Status() ClientStatus {
	return ClientStatus(c.status.Load())
}

// ServerID returns the server definition ID.
func (c *Client) ServerID() ServerID {
	return c.definition.ID
}

// Capabilities returns the negotiated server capabilities.
func (c *Client) Capabilities() ServerCapabilities {
	return c.capabilities
}

// SyncKind returns the negotiated document sync kind.
func (c *Client) SyncKind() TextDocumentSyncKind {
	return c.syncKind
}

// ProjectRoot returns the project root this client serves.
func (c *Client) ProjectRoot() string {
	return c.projectRoot
}

// DroppedDiagnostics returns the number of diagnostic updates dropped
// due to channel backpressure.
func (c *Client) DroppedDiagnostics() int64 {
	return c.dropped.Load()
}

// TriggerCharacters returns the completion trigger characters negotiated
// during the initialize handshake (e.g., ".", ":").
func (c *Client) TriggerCharacters() []string {
	return c.triggerChars
}

// ---------------------------------------------------------------------------
// LSP request methods (Tier 2)
// ---------------------------------------------------------------------------

// requestTimeout bounds individual LSP requests (completion, hover, definition).
// Derived from: gopls typically responds in < 2s; 10s handles slow servers.
const requestTimeout = 10 * time.Second

// Completion sends a textDocument/completion request and returns the items.
func (c *Client) Completion(ctx context.Context, filePath string, line, character int) ([]CompletionItem, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	lineText := c.documents.LineText(uri, line)
	utf16Col := RuneOffsetToUTF16(lineText, character)

	params := CompletionParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     ProtocolPosition{Line: line, Character: utf16Col},
		},
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodCompletion, params)
	if err != nil {
		return nil, fmt.Errorf("completion request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	// Response can be []CompletionItem or CompletionList.
	var list ProtocolCompletionList
	if json.Unmarshal(resp.Result, &list) == nil && len(list.Items) > 0 {
		return ToCompletionItems(list.Items), nil
	}
	var items []ProtocolCompletionItem
	if json.Unmarshal(resp.Result, &items) == nil {
		return ToCompletionItems(items), nil
	}
	return nil, nil
}

// Hover sends a textDocument/hover request and returns the result.
func (c *Client) Hover(ctx context.Context, filePath string, line, character int) (*HoverResult, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	lineText := c.documents.LineText(uri, line)
	utf16Col := RuneOffsetToUTF16(lineText, character)

	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     ProtocolPosition{Line: line, Character: utf16Col},
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodHover, params)
	if err != nil {
		return nil, fmt.Errorf("hover request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	var ph ProtocolHoverResult
	if err := json.Unmarshal(resp.Result, &ph); err != nil {
		return nil, fmt.Errorf("decode hover: %w", err)
	}
	return ToHoverResult(ph), nil
}

// Definition sends a textDocument/definition request and returns locations.
func (c *Client) Definition(ctx context.Context, filePath string, line, character int) ([]Location, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	lineText := c.documents.LineText(uri, line)
	utf16Col := RuneOffsetToUTF16(lineText, character)

	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     ProtocolPosition{Line: line, Character: utf16Col},
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodDefinition, params)
	if err != nil {
		return nil, fmt.Errorf("definition request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	// Response can be Location, []Location, or []LocationLink.
	// We normalize to []Location.
	var locs []ProtocolLocation
	if json.Unmarshal(resp.Result, &locs) == nil {
		return toLocationSlice(locs), nil
	}
	var single ProtocolLocation
	if json.Unmarshal(resp.Result, &single) == nil {
		return toLocationSlice([]ProtocolLocation{single}), nil
	}
	return nil, nil
}

// DocumentHighlight sends a textDocument/documentHighlight request and returns highlights.
func (c *Client) DocumentHighlight(ctx context.Context, filePath string, line, character int) ([]DocumentHighlight, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	lineText := c.documents.LineText(uri, line)
	utf16Col := RuneOffsetToUTF16(lineText, character)

	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     ProtocolPosition{Line: line, Character: utf16Col},
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodDocumentHighlight, params)
	if err != nil {
		return nil, fmt.Errorf("documentHighlight request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	var items []ProtocolDocumentHighlight
	if err := json.Unmarshal(resp.Result, &items); err != nil {
		return nil, fmt.Errorf("decode documentHighlight: %w", err)
	}
	return ToDocumentHighlights(items), nil
}

// toLocationSlice converts wire locations to domain Location.
func toLocationSlice(plocs []ProtocolLocation) []Location {
	result := make([]Location, len(plocs))
	for i, pl := range plocs {
		result[i] = Location{
			URI:   pl.URI,
			Range: toCoreRange(pl.Range),
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Document sync notifications
// ---------------------------------------------------------------------------

// NotifyDidOpen tells the server a document was opened.
func (c *Client) NotifyDidOpen(filePath, languageID, text string) error {
	if c.Status() != StatusReady {
		return errClientNotReady
	}
	uri := PathToFileURI(filePath)
	version, ok := c.documents.Open(uri, languageID, text)
	if !ok {
		return nil // already open or at capacity
	}
	return c.sendNotification(MethodDidOpen, DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: languageID,
			Version:    version,
			Text:       text,
		},
	})
}

// NotifyDidChange tells the server a document's content changed (full sync).
func (c *Client) NotifyDidChange(filePath, newText string) error {
	if c.Status() != StatusReady {
		return errClientNotReady
	}
	uri := PathToFileURI(filePath)
	version, ok := c.documents.Change(uri, newText)
	if !ok {
		return nil // not tracked
	}
	return c.sendNotification(MethodDidChange, DidChangeParams{
		TextDocument: VersionedTextDocumentIdentifier{
			URI:     uri,
			Version: version,
		},
		ContentChanges: []TextDocumentContentChangeEvent{
			{Text: newText},
		},
	})
}

// NotifyDidSave tells the server a document was saved.
func (c *Client) NotifyDidSave(filePath, text string) error {
	if c.Status() != StatusReady {
		return errClientNotReady
	}
	uri := PathToFileURI(filePath)
	if !c.documents.Save(uri) {
		return nil // not tracked
	}
	return c.sendNotification(MethodDidSave, DidSaveParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Text:         text,
	})
}

// NotifyDidClose tells the server a document was closed.
func (c *Client) NotifyDidClose(filePath string) error {
	if c.Status() != StatusReady {
		return errClientNotReady
	}
	uri := PathToFileURI(filePath)
	if !c.documents.Close(uri) {
		return nil // not tracked
	}
	return c.sendNotification(MethodDidClose, DidCloseParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	})
}

// ---------------------------------------------------------------------------
// Process management
// ---------------------------------------------------------------------------

// startProcess launches the server subprocess and wires stdin/stdout pipes.
func (c *Client) startProcess() error {
	c.cmd = exec.Command(resolveCommand(c.definition.Command), c.definition.Args...)
	c.cmd.Env = os.Environ()

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	// Discard stderr to avoid blocking.
	c.cmd.Stderr = nil

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	c.stdin = stdin
	c.stdout = stdout
	c.transport = NewTransport(stdout, stdin)
	return nil
}

// killTimeout bounds how long we wait for a killed process to exit.
// Derived from: after SIGKILL, process exit is near-instant on Linux;
// 1s provides headroom for slow I/O drain.
const killTimeout = 1 * time.Second

// killProcess terminates the server process if it's still running.
func (c *Client) killProcess() {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	_ = c.cmd.Process.Kill()

	// Wait with a timeout to avoid blocking shutdown if pipes won't drain.
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(killTimeout):
	}
}

// ---------------------------------------------------------------------------
// Initialize handshake
// ---------------------------------------------------------------------------

// initialize performs the initialize/initialized handshake with the server.
func (c *Client) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   PathToFileURI(c.projectRoot),
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Synchronization: TextDocumentSyncClientCapabilities{
					DidSave: true,
				},
				PublishDiags: PublishDiagnosticsClientCapabilities{
					RelatedInformation: true,
				},
				Completion: CompletionClientCapabilities{
					CompletionItem: CompletionItemClientCaps{
						SnippetSupport: false,
					},
				},
				Hover: HoverClientCapabilities{
					ContentFormat: []string{"markdown", "plaintext"},
				},
				Definition:        DefinitionClientCapabilities{},
				DocumentHighlight: DocumentHighlightClientCapabilities{},
			},
		},
		ClientInfo: &ClientInfo{
			Name:    "sylk",
			Version: "0.1.0",
		},
		InitializationOptions: c.definition.InitializationOptions,
	}

	resp, err := c.sendRequest(ctx, MethodInitialize, params)
	if err != nil {
		return fmt.Errorf("initialize request: %w", err)
	}
	if resp.Error != nil {
		return resp.Error
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}

	c.capabilities = ToServerCapabilities(result.Capabilities)
	c.syncKind = ToSyncKind(result.Capabilities.TextDocumentSync)
	c.triggerChars = CompletionTriggerChars(result.Capabilities.CompletionProvider)

	// Send "initialized" notification.
	return c.sendNotification(MethodInitialized, struct{}{})
}

// ---------------------------------------------------------------------------
// Read loop (runs under GoroutineScope)
// ---------------------------------------------------------------------------

// readLoop reads messages from the transport and dispatches them.
func (c *Client) readLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return nil
		default:
		}

		data, err := c.transport.ReadMessage()
		if err != nil {
			select {
			case <-c.done:
				return nil // expected on shutdown
			default:
				c.setStatus(StatusError)
				return fmt.Errorf("read: %w", err)
			}
		}

		c.dispatchIncoming(data)
	}
}

// dispatchIncoming classifies and routes a raw JSON-RPC message.
func (c *Client) dispatchIncoming(data []byte) {
	var msg incomingMessage
	if json.Unmarshal(data, &msg) != nil {
		return
	}

	switch msg.Classify() {
	case kindResponse:
		var resp Response
		if json.Unmarshal(data, &resp) == nil {
			c.dispatchResponse(resp)
		}
	case kindNotification:
		var notif Notification
		if json.Unmarshal(data, &notif) == nil {
			c.dispatchNotification(notif.Method, notif.Params)
		}
	case kindRequest:
		// Server-to-client requests (e.g., window/showMessageRequest).
		// For now, respond with method-not-found to avoid blocking the server.
		c.handleServerRequest(data)
	case kindUnknown:
		// Discard unclassifiable messages.
	}
}

// dispatchResponse delivers a response to its waiting request goroutine.
func (c *Client) dispatchResponse(resp Response) {
	id, ok := resp.ID.Int()
	if !ok {
		return
	}

	c.mu.Lock()
	pr, found := c.pending[id]
	if found {
		delete(c.pending, id)
	}
	c.mu.Unlock()

	if found {
		pr.ch <- resp
	}
}

// dispatchNotification routes a notification through the notifyMap.
func (c *Client) dispatchNotification(method string, params json.RawMessage) {
	handler, ok := c.notifyMap[method]
	if !ok {
		return
	}
	handler(c, params)
}

// handleServerRequest responds to server-initiated requests with an empty result.
func (c *Client) handleServerRequest(data []byte) {
	var req Request
	if json.Unmarshal(data, &req) != nil {
		return
	}
	resp := Response{
		JSONRPC: jsonrpcVersion,
		ID:      req.ID,
		Result:  json.RawMessage("null"),
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = c.transport.WriteMessage(raw)
}

// ---------------------------------------------------------------------------
// Request / Notification sending
// ---------------------------------------------------------------------------

// sendRequest sends a request and blocks until a response is received or
// the context is cancelled.
func (c *Client) sendRequest(ctx context.Context, method string, params any) (Response, error) {
	c.mu.Lock()
	if len(c.pending) >= maxPendingRequests {
		c.mu.Unlock()
		return Response{}, errTooManyRequests
	}
	c.nextID++
	id := c.nextID
	ch := make(chan Response, 1)
	c.pending[id] = pendingRequest{ch: ch, method: method}
	c.mu.Unlock()

	req, err := NewRequest(IntID(id), method, params)
	if err != nil {
		c.removePending(id)
		return Response{}, fmt.Errorf("marshal request: %w", err)
	}

	raw, err := json.Marshal(req)
	if err != nil {
		c.removePending(id)
		return Response{}, fmt.Errorf("encode request: %w", err)
	}

	if err := c.transport.WriteMessage(raw); err != nil {
		c.removePending(id)
		return Response{}, fmt.Errorf("write request: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		c.removePending(id)
		return Response{}, ctx.Err()
	}
}

// sendNotification sends a notification (no response expected).
func (c *Client) sendNotification(method string, params any) error {
	notif, err := NewNotification(method, params)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	raw, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}
	return c.transport.WriteMessage(raw)
}

// removePending removes a pending request entry to prevent map growth.
func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// cancelPending closes all pending response channels to unblock waiters.
func (c *Client) cancelPending() {
	c.mu.Lock()
	for id, pr := range c.pending {
		close(pr.ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Shutdown
// ---------------------------------------------------------------------------

// shutdownExit sends the shutdown request and exit notification.
func (c *Client) shutdownExit() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	resp, err := c.sendRequest(ctx, MethodShutdown, nil)
	if err != nil {
		return fmt.Errorf("shutdown request: %w", err)
	}
	if resp.Error != nil {
		return resp.Error
	}

	return c.sendNotification(MethodExit, nil)
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// setStatus atomically updates the client status.
func (c *Client) setStatus(s ClientStatus) {
	c.status.Store(int32(s))
}

// ---------------------------------------------------------------------------
// Notification handlers
// ---------------------------------------------------------------------------

// handlePublishDiagnostics converts wire diagnostics and sends them to
// the diagnostics channel.
func handlePublishDiagnostics(c *Client, params json.RawMessage) {
	var p PublishDiagnosticsParams
	if json.Unmarshal(params, &p) != nil {
		return
	}

	result := ToDiagnosticResult(c.definition.ID, p)

	select {
	case c.diagnostics <- result:
	default:
		c.dropped.Add(1)
	}
}
