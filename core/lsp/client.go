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

// stderrDrainSize caps stderr capture to prevent unbounded memory growth.
// Derived from: typical LSP server stderr is mostly warnings/debug under 64 KiB.
const stderrDrainSize = 64 * 1024

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
	stderr io.ReadCloser  // server stderr pipe (drained for diagnostics)

	// Transport.
	transport *Transport

	// Request correlation.
	mu      sync.Mutex
	nextID  int64
	pending map[int64]pendingRequest

	// Server capabilities (set after initialize).
	capabilities  ServerCapabilities
	triggerChars          []string // completion trigger characters from server
	sigHelpTriggerChars   []string // signature help trigger characters
	sigHelpRetriggerChars []string // signature help retrigger characters

	// Status (atomic for lock-free reads).
	status atomic.Int32

	// Outbound diagnostic channel (consumed by Manager/Bridge).
	diagnostics  chan DiagnosticResult
	dropped      atomic.Int64
	stderrOutput string // captured server stderr (bounded by stderrDrainSize)

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
		c.closePipes()
		c.killProcess()
		c.setStatus(StatusError)
		return fmt.Errorf("start readloop %s: %w", c.definition.ID, err)
	}

	// Start stderr drain (non-fatal if scope rejects).
	stderrDesc := "lsp.stderr." + string(c.definition.ID)
	_ = c.scope.Go(stderrDesc, readLoopTimeout, c.drainStderr)

	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()

	if err := c.initialize(initCtx); err != nil {
		close(c.done)
		c.closePipes()
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

	// Signal readloop and stderr drain to exit, then close pipes to
	// unblock any blocked I/O before killing the process.
	close(c.done)
	c.closePipes()
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

// SignatureHelpTriggerCharacters returns the trigger characters for
// signature help negotiated during initialize.
func (c *Client) SignatureHelpTriggerCharacters() []string {
	return c.sigHelpTriggerChars
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

// SignatureHelp sends a textDocument/signatureHelp request.
func (c *Client) SignatureHelp(ctx context.Context, filePath string, line, character int) (*SignatureHelp, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	lineText := c.documents.LineText(uri, line)
	utf16Col := RuneOffsetToUTF16(lineText, character)

	params := SignatureHelpParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     ProtocolPosition{Line: line, Character: utf16Col},
		},
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodSignatureHelp, params)
	if err != nil {
		return nil, fmt.Errorf("signatureHelp request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	var ph ProtocolSignatureHelp
	if err := json.Unmarshal(resp.Result, &ph); err != nil {
		return nil, fmt.Errorf("decode signatureHelp: %w", err)
	}
	return ToSignatureHelp(ph), nil
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

// References sends a textDocument/references request and returns locations.
func (c *Client) References(ctx context.Context, filePath string, line, character int, includeDeclaration bool) ([]Location, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	lineText := c.documents.LineText(uri, line)
	utf16Col := RuneOffsetToUTF16(lineText, character)

	params := ReferenceParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     ProtocolPosition{Line: line, Character: utf16Col},
		},
		Context: ReferenceContext{
			IncludeDeclaration: includeDeclaration,
		},
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodReferences, params)
	if err != nil {
		return nil, fmt.Errorf("references request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	var locs []ProtocolLocation
	if err := json.Unmarshal(resp.Result, &locs); err != nil {
		return nil, fmt.Errorf("decode references: %w", err)
	}
	return toLocationSlice(locs), nil
}

// DocumentSymbol sends a textDocument/documentSymbol request and returns symbols.
func (c *Client) DocumentSymbol(ctx context.Context, filePath string) ([]DocumentSymbol, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	params := struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
	}{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodDocumentSymbol, params)
	if err != nil {
		return nil, fmt.Errorf("documentSymbol request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	// Try hierarchical format first (modern servers).
	var hierarchical []ProtocolDocumentSymbol
	if json.Unmarshal(resp.Result, &hierarchical) == nil && len(hierarchical) > 0 {
		// Distinguish from flat format: hierarchical has selectionRange.
		if hierarchical[0].SelectionRange != (ProtocolRange{}) || hierarchical[0].Range != (ProtocolRange{}) {
			return toDocumentSymbols(hierarchical), nil
		}
	}

	// Fallback: flat format (legacy servers).
	var flat []ProtocolSymbolInformation
	if err := json.Unmarshal(resp.Result, &flat); err != nil {
		return nil, fmt.Errorf("decode documentSymbol: %w", err)
	}
	return symbolInfoToDocumentSymbols(flat), nil
}

// Format sends a textDocument/formatting request and returns text edits.
func (c *Client) Format(ctx context.Context, filePath string, tabSize int, insertSpaces bool) ([]TextEdit, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	params := DocumentFormattingParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Options: ProtocolFormattingOptions{
			TabSize:      tabSize,
			InsertSpaces: insertSpaces,
		},
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodFormatting, params)
	if err != nil {
		return nil, fmt.Errorf("formatting request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	var edits []ProtocolTextEdit
	if err := json.Unmarshal(resp.Result, &edits); err != nil {
		return nil, fmt.Errorf("decode formatting: %w", err)
	}
	return toTextEdits(edits), nil
}

// PrepareRename sends a textDocument/prepareRename request to check
// whether the symbol at the given position can be renamed.
func (c *Client) PrepareRename(ctx context.Context, filePath string, line, character int) (*PrepareRenameResult, error) {
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

	resp, err := c.sendRequest(reqCtx, MethodPrepareRename, params)
	if err != nil {
		return nil, fmt.Errorf("prepareRename request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	var pr ProtocolPrepareRenameResult
	if err := json.Unmarshal(resp.Result, &pr); err != nil {
		return nil, fmt.Errorf("decode prepareRename: %w", err)
	}
	return ToPrepareRenameResult(pr), nil
}

// Rename sends a textDocument/rename request and returns the workspace edit.
func (c *Client) Rename(ctx context.Context, filePath string, line, character int, newName string) (*WorkspaceEdit, error) {
	if c.Status() != StatusReady {
		return nil, errClientNotReady
	}
	uri := PathToFileURI(filePath)
	lineText := c.documents.LineText(uri, line)
	utf16Col := RuneOffsetToUTF16(lineText, character)

	params := ProtocolRenameParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     ProtocolPosition{Line: line, Character: utf16Col},
		NewName:      newName,
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.sendRequest(reqCtx, MethodRename, params)
	if err != nil {
		return nil, fmt.Errorf("rename request: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if string(resp.Result) == "null" {
		return nil, nil
	}

	var pw ProtocolWorkspaceEdit
	if err := json.Unmarshal(resp.Result, &pw); err != nil {
		return nil, fmt.Errorf("decode rename: %w", err)
	}
	return ToWorkspaceEdit(pw), nil
}

// toTextEdits converts wire text edits to domain TextEdit.
func toTextEdits(pedits []ProtocolTextEdit) []TextEdit {
	result := make([]TextEdit, len(pedits))
	for i, pe := range pedits {
		result[i] = TextEdit{
			Range:   toCoreRange(pe.Range),
			NewText: pe.NewText,
		}
	}
	return result
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
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr
	c.transport = NewTransport(stdout, stdin)
	return nil
}

// closePipes closes stdin, stdout, and stderr pipes to unblock any
// goroutines blocked on pipe I/O. Must be called before killProcess
// so that cmd.Wait() returns promptly.
func (c *Client) closePipes() {
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.stderr != nil {
		_ = c.stderr.Close()
	}
}

// killProcess terminates the server process if it's still running.
// Pipes must be closed before calling this method so that cmd.Wait()
// returns promptly after SIGKILL (no untracked goroutine needed).
func (c *Client) killProcess() {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
}

// drainStderr reads from the server's stderr pipe and stores the output
// for diagnostic purposes. Exits when the pipe is closed (via closePipes).
func (c *Client) drainStderr(_ context.Context) error {
	if c.stderr == nil {
		return nil
	}
	var output []byte
	buf := make([]byte, 4096)
	for len(output) < stderrDrainSize {
		n, err := c.stderr.Read(buf)
		if n > 0 {
			remaining := stderrDrainSize - len(output)
			if n > remaining {
				n = remaining
			}
			output = append(output, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	if len(output) > 0 {
		c.mu.Lock()
		c.stderrOutput = string(output)
		c.mu.Unlock()
	}
	return nil
}

// StderrOutput returns the captured server stderr output.
func (c *Client) StderrOutput() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stderrOutput
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
				References:        ReferencesClientCapabilities{},
				DocumentSymbol: DocumentSymbolClientCapabilities{
					HierarchicalDocumentSymbolSupport: true,
				},
				SignatureHelp: SignatureHelpClientCapabilities{
					SignatureInformation: &SignatureInfoClientCaps{
						ParameterInformation: &ParamInfoClientCaps{
							LabelOffsetSupport: true,
						},
					},
				},
				Rename: RenameClientCapabilities{
					PrepareSupport: true,
				},
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
	c.sigHelpTriggerChars, c.sigHelpRetriggerChars = SignatureHelpTriggerChars(result.Capabilities.SignatureHelpProvider)

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
		if json.Unmarshal(data, &resp) != nil {
			// Malformed response: clean up the pending entry using the
			// partially-parsed ID so the caller gets an error instead
			// of hanging until timeout.
			c.cleanupByRawID(msg.ID)
			return
		}
		c.dispatchResponse(resp)
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

// cleanupByRawID attempts to clean up a pending request whose response
// failed full decoding, using the raw ID from the partial parse.
func (c *Client) cleanupByRawID(rawID json.RawMessage) {
	var id RequestID
	if json.Unmarshal(rawID, &id) != nil {
		return
	}
	intID, ok := id.Int()
	if !ok {
		return
	}
	c.mu.Lock()
	pr, found := c.pending[intID]
	if found {
		delete(c.pending, intID)
	}
	c.mu.Unlock()
	if found {
		pr.ch <- Response{
			Error: &RPCError{Code: -32603, Message: "malformed response from server"},
		}
	}
}

// cancelPending sends an error response to all pending waiters so callers
// receive a typed error rather than a zero-value Response from a closed channel.
func (c *Client) cancelPending() {
	c.mu.Lock()
	for id, pr := range c.pending {
		pr.ch <- Response{
			Error: &RPCError{Code: -32099, Message: "client stopped"},
		}
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
