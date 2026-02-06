package lsp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// ---------------------------------------------------------------------------
// Capacity limits
// ---------------------------------------------------------------------------

// maxActiveClients caps the number of simultaneous language server connections.
// Derived from: typical polyglot project uses 2–3 servers; 16 supports
// extreme cases.
const maxActiveClients = 16

// managerDiagBufferSize is the aggregated diagnostics channel capacity.
// Derived from: at most maxActiveClients × diagnosticBufferSize events
// in flight; 256 balances memory with throughput.
const managerDiagBufferSize = 256

// fanInTimeout is the max lifetime of a fan-in goroutine.
// Derived from: runs for the duration of the client's lifetime.
const fanInTimeout = 24 * time.Hour

// Sentinel errors.
var (
	errTooManyClients = errors.New("too many active LSP clients")
	errNoServer       = errors.New("no suitable language server found")
)

// ---------------------------------------------------------------------------
// clientKey
// ---------------------------------------------------------------------------

// clientKey uniquely identifies an LSP client by server + project root.
type clientKey struct {
	serverID    ServerID
	projectRoot string
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager manages multiple LSP clients and routes document events to
// the appropriate server. It is safe for concurrent use.
type Manager struct {
	mu       sync.RWMutex
	clients  map[clientKey]*Client
	selector *LSPSelector
	scope    *concurrency.GoroutineScope

	// Aggregated diagnostics channel from all active clients.
	diagnostics chan DiagnosticResult
	dropped     atomic.Int64
}

// NewManager creates a Manager with the default server selector.
func NewManager(scope *concurrency.GoroutineScope) *Manager {
	return &Manager{
		clients:     make(map[clientKey]*Client, maxActiveClients),
		selector:    NewLSPSelector(),
		scope:       scope,
		diagnostics: make(chan DiagnosticResult, managerDiagBufferSize),
	}
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Diagnostics returns the aggregated diagnostics channel from all clients.
func (m *Manager) Diagnostics() <-chan DiagnosticResult {
	return m.diagnostics
}

// DroppedDiagnostics returns the total number of diagnostics dropped
// across all fan-in goroutines due to channel backpressure.
func (m *Manager) DroppedDiagnostics() int64 {
	return m.dropped.Load()
}

// EnsureClient finds or starts the appropriate LSP client for a file.
// Returns nil, nil if no suitable server is detected for the file type.
func (m *Manager) EnsureClient(ctx context.Context, projectRoot, filePath string) (*Client, error) {
	def := m.selector.SelectServer(projectRoot, filePath)
	if def == nil {
		return nil, nil
	}

	key := clientKey{serverID: def.ID, projectRoot: projectRoot}

	// Fast path: client already running.
	m.mu.RLock()
	existing, ok := m.clients[key]
	m.mu.RUnlock()
	if ok && existing.Status() == StatusReady {
		return existing, nil
	}

	return m.startClient(ctx, def, projectRoot, key)
}

// NotifyDidOpen sends textDocument/didOpen to the appropriate client.
// Starts a client if one isn't running yet for this file type.
func (m *Manager) NotifyDidOpen(ctx context.Context, projectRoot, filePath, languageID, content string) error {
	client, err := m.EnsureClient(ctx, projectRoot, filePath)
	if err != nil {
		return err
	}
	if client == nil {
		return nil
	}
	return client.NotifyDidOpen(filePath, languageID, content)
}

// NotifyDidChange sends textDocument/didChange to the appropriate client.
func (m *Manager) NotifyDidChange(ctx context.Context, projectRoot, filePath, content string) error {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil
	}
	return client.NotifyDidChange(filePath, content)
}

// NotifyDidSave sends textDocument/didSave to the appropriate client.
func (m *Manager) NotifyDidSave(ctx context.Context, projectRoot, filePath, content string) error {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil
	}
	return client.NotifyDidSave(filePath, content)
}

// NotifyDidClose sends textDocument/didClose to the appropriate client.
func (m *Manager) NotifyDidClose(ctx context.Context, projectRoot, filePath string) error {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil
	}
	return client.NotifyDidClose(filePath)
}

// ---------------------------------------------------------------------------
// LSP request forwarding (Tier 2)
// ---------------------------------------------------------------------------

// Completion sends a completion request to the appropriate client.
func (m *Manager) Completion(ctx context.Context, projectRoot, filePath string, line, character int) ([]CompletionItem, error) {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil, nil
	}
	return client.Completion(ctx, filePath, line, character)
}

// Hover sends a hover request to the appropriate client.
func (m *Manager) Hover(ctx context.Context, projectRoot, filePath string, line, character int) (*HoverResult, error) {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil, nil
	}
	return client.Hover(ctx, filePath, line, character)
}

// Definition sends a definition request to the appropriate client.
func (m *Manager) Definition(ctx context.Context, projectRoot, filePath string, line, character int) ([]Location, error) {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil, nil
	}
	return client.Definition(ctx, filePath, line, character)
}

// DocumentHighlight sends a documentHighlight request to the appropriate client.
func (m *Manager) DocumentHighlight(ctx context.Context, projectRoot, filePath string, line, character int) ([]DocumentHighlight, error) {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil, nil
	}
	return client.DocumentHighlight(ctx, filePath, line, character)
}

// SignatureHelp sends a signature help request to the appropriate client.
func (m *Manager) SignatureHelp(ctx context.Context, projectRoot, filePath string, line, character int) (*SignatureHelp, error) {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil, nil
	}
	return client.SignatureHelp(ctx, filePath, line, character)
}

// SignatureHelpTriggerCharacters returns signature help trigger characters
// for the server handling the given file.
func (m *Manager) SignatureHelpTriggerCharacters(projectRoot, filePath string) []string {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil
	}
	return client.SignatureHelpTriggerCharacters()
}

// References sends a references request to the appropriate client.
func (m *Manager) References(ctx context.Context, projectRoot, filePath string, line, character int, includeDeclaration bool) ([]Location, error) {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil, nil
	}
	return client.References(ctx, filePath, line, character, includeDeclaration)
}

// DocumentSymbol sends a documentSymbol request to the appropriate client.
func (m *Manager) DocumentSymbol(ctx context.Context, projectRoot, filePath string) ([]DocumentSymbol, error) {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil, nil
	}
	return client.DocumentSymbol(ctx, filePath)
}

// Format sends a formatting request to the appropriate client.
func (m *Manager) Format(ctx context.Context, projectRoot, filePath string, tabSize int, insertSpaces bool) ([]TextEdit, error) {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil, errNoServer
	}
	return client.Format(ctx, filePath, tabSize, insertSpaces)
}

// TriggerCharacters returns completion trigger characters for the server
// handling the given file. Returns nil if no client is active.
func (m *Manager) TriggerCharacters(projectRoot, filePath string) []string {
	client := m.clientForFile(projectRoot, filePath)
	if client == nil {
		return nil
	}
	return client.TriggerCharacters()
}

// SuggestServer returns a server definition that matches the file but
// isn't currently available. Returns nil if no installable match exists.
func (m *Manager) SuggestServer(root, filePath string) *LanguageServerDefinition {
	return m.selector.SuggestServer(root, filePath)
}

// managerShutdownTimeout bounds the total time for all clients to stop.
// Derived from: shutdownTimeout(2s) + killTimeout(1s) + margin.
const managerShutdownTimeout = 4 * time.Second

// Shutdown stops all active clients concurrently and closes the aggregated
// diagnostics channel so bridge drain goroutines can exit.
func (m *Manager) Shutdown() error {
	m.mu.Lock()
	clients := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = make(map[clientKey]*Client, maxActiveClients)
	m.mu.Unlock()

	defer close(m.diagnostics)

	if len(clients) == 0 {
		return nil
	}

	errs := make([]error, len(clients))
	var wg sync.WaitGroup
	wg.Add(len(clients))
	for i, c := range clients {
		go func() {
			defer wg.Done()
			errs[i] = c.Stop()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(managerShutdownTimeout):
		return fmt.Errorf("lsp manager shutdown timed out with %d clients", len(clients))
	}
	return errors.Join(errs...)
}

// ClientCount returns the number of active clients.
func (m *Manager) ClientCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients)
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

// startClient creates, starts, and registers a new Client.
func (m *Manager) startClient(
	ctx context.Context,
	def *LanguageServerDefinition,
	projectRoot string,
	key clientKey,
) (*Client, error) {
	m.mu.Lock()
	// Double-check under write lock.
	if existing, ok := m.clients[key]; ok && existing.Status() == StatusReady {
		m.mu.Unlock()
		return existing, nil
	}
	if len(m.clients) >= maxActiveClients {
		m.mu.Unlock()
		return nil, errTooManyClients
	}
	m.mu.Unlock()

	client := NewClient(def, projectRoot, m.scope)
	if err := client.Start(ctx); err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.clients[key] = client
	m.mu.Unlock()

	m.fanInDiagnostics(client)
	return client, nil
}

// clientForFile looks up an existing running client for a file.
func (m *Manager) clientForFile(projectRoot, filePath string) *Client {
	def := m.selector.SelectServer(projectRoot, filePath)
	if def == nil {
		return nil
	}

	key := clientKey{serverID: def.ID, projectRoot: projectRoot}

	m.mu.RLock()
	client, ok := m.clients[key]
	m.mu.RUnlock()

	if !ok || client.Status() != StatusReady {
		return nil
	}
	return client
}

// fanInDiagnostics launches a tracked goroutine that reads from a client's
// diagnostics channel and forwards to the manager's aggregated channel.
func (m *Manager) fanInDiagnostics(client *Client) {
	desc := fmt.Sprintf("lsp.fanin.%s", client.ServerID())
	_ = m.scope.Go(desc, fanInTimeout, func(ctx context.Context) error {
		ch := client.Diagnostics()
		for {
			select {
			case diag, ok := <-ch:
				if !ok {
					return nil
				}
				select {
				case m.diagnostics <- diag:
				default:
					m.dropped.Add(1)
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
}
