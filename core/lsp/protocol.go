package lsp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------------------
// LSP method constants
// ---------------------------------------------------------------------------

const (
	MethodInitialize   = "initialize"
	MethodInitialized  = "initialized"
	MethodShutdown     = "shutdown"
	MethodExit         = "exit"
	MethodDidOpen      = "textDocument/didOpen"
	MethodDidChange    = "textDocument/didChange"
	MethodDidSave      = "textDocument/didSave"
	MethodDidClose     = "textDocument/didClose"
	MethodPublishDiags = "textDocument/publishDiagnostics"
	MethodCompletion         = "textDocument/completion"
	MethodHover              = "textDocument/hover"
	MethodDefinition         = "textDocument/definition"
	MethodDocumentHighlight  = "textDocument/documentHighlight"
	MethodReferences         = "textDocument/references"
)

// ---------------------------------------------------------------------------
// TextDocumentSyncKind
// ---------------------------------------------------------------------------

// TextDocumentSyncKind defines how the client reports document changes.
type TextDocumentSyncKind int

const (
	SyncNone        TextDocumentSyncKind = 0
	SyncFull        TextDocumentSyncKind = 1
	SyncIncremental TextDocumentSyncKind = 2
)

// ---------------------------------------------------------------------------
// Initialize
// ---------------------------------------------------------------------------

// InitializeParams is the request body for "initialize".
type InitializeParams struct {
	ProcessID             int                    `json:"processId"`
	RootURI               string                 `json:"rootUri"`
	Capabilities          ClientCapabilities     `json:"capabilities"`
	ClientInfo            *ClientInfo            `json:"clientInfo,omitempty"`
	InitializationOptions map[string]interface{} `json:"initializationOptions,omitempty"`
}

// ClientInfo identifies the editor to the language server.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ClientCapabilities declares what the client supports.
type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument,omitempty"`
}

// TextDocumentClientCapabilities groups text-document-related capabilities.
type TextDocumentClientCapabilities struct {
	Synchronization    TextDocumentSyncClientCapabilities      `json:"synchronization,omitempty"`
	PublishDiags       PublishDiagnosticsClientCapabilities     `json:"publishDiagnostics,omitempty"`
	Completion         CompletionClientCapabilities             `json:"completion,omitempty"`
	Hover              HoverClientCapabilities                  `json:"hover,omitempty"`
	Definition         DefinitionClientCapabilities             `json:"definition,omitempty"`
	DocumentHighlight  DocumentHighlightClientCapabilities      `json:"documentHighlight,omitempty"`
	References         ReferencesClientCapabilities             `json:"references,omitempty"`
}

// CompletionClientCapabilities describes completion support.
type CompletionClientCapabilities struct {
	DynamicRegistration bool                      `json:"dynamicRegistration,omitempty"`
	CompletionItem      CompletionItemClientCaps  `json:"completionItem,omitempty"`
}

// CompletionItemClientCaps describes per-item completion capabilities.
type CompletionItemClientCaps struct {
	SnippetSupport bool `json:"snippetSupport,omitempty"`
}

// HoverClientCapabilities describes hover support.
type HoverClientCapabilities struct {
	DynamicRegistration bool     `json:"dynamicRegistration,omitempty"`
	ContentFormat       []string `json:"contentFormat,omitempty"`
}

// DefinitionClientCapabilities describes go-to-definition support.
type DefinitionClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// DocumentHighlightClientCapabilities describes document highlight support.
type DocumentHighlightClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// ReferencesClientCapabilities describes find-references support.
type ReferencesClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// TextDocumentSyncClientCapabilities describes sync support.
type TextDocumentSyncClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
	DidSave             bool `json:"didSave,omitempty"`
}

// PublishDiagnosticsClientCapabilities describes diagnostic support.
type PublishDiagnosticsClientCapabilities struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
}

// InitializeResult is the response body for "initialize".
type InitializeResult struct {
	Capabilities ServerCaps `json:"capabilities"`
}

// ServerCaps is the raw capabilities shape from the server's initialize response.
// Fields use any because some capabilities are polymorphic (bool or object).
type ServerCaps struct {
	TextDocumentSync    json.RawMessage `json:"textDocumentSync,omitempty"`
	HoverProvider       json.RawMessage `json:"hoverProvider,omitempty"`
	CompletionProvider  json.RawMessage `json:"completionProvider,omitempty"`
	DefinitionProvider  json.RawMessage `json:"definitionProvider,omitempty"`
	ReferencesProvider  json.RawMessage `json:"referencesProvider,omitempty"`
	DocumentSymbol      json.RawMessage `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbol     json.RawMessage `json:"workspaceSymbolProvider,omitempty"`
	CodeActionProvider         json.RawMessage `json:"codeActionProvider,omitempty"`
	RenameProvider             json.RawMessage `json:"renameProvider,omitempty"`
	DiagnosticProvider         json.RawMessage `json:"diagnosticProvider,omitempty"`
	DocumentHighlightProvider  json.RawMessage `json:"documentHighlightProvider,omitempty"`
}

// ---------------------------------------------------------------------------
// Document sync params
// ---------------------------------------------------------------------------

// DidOpenParams is the notification body for "textDocument/didOpen".
type DidOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// TextDocumentItem describes a document being opened.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// DidChangeParams is the notification body for "textDocument/didChange".
type DidChangeParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// VersionedTextDocumentIdentifier identifies a specific version of a document.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// TextDocumentContentChangeEvent represents a document content change.
// For full-sync mode, this is just the complete new text.
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// DidSaveParams is the notification body for "textDocument/didSave".
type DidSaveParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         string                 `json:"text,omitempty"`
}

// TextDocumentIdentifier identifies a document by URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// DidCloseParams is the notification body for "textDocument/didClose".
type DidCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// ---------------------------------------------------------------------------
// Diagnostics (server → client notification)
// ---------------------------------------------------------------------------

// PublishDiagnosticsParams is the wire format for textDocument/publishDiagnostics.
type PublishDiagnosticsParams struct {
	URI         string               `json:"uri"`
	Diagnostics []ProtocolDiagnostic `json:"diagnostics"`
}

// ProtocolDiagnostic is the wire format of a single LSP diagnostic.
type ProtocolDiagnostic struct {
	Range              ProtocolRange        `json:"range"`
	Severity           int                  `json:"severity,omitempty"`
	Code               json.RawMessage      `json:"code,omitempty"`
	Source             string               `json:"source,omitempty"`
	Message            string               `json:"message"`
	RelatedInformation []ProtocolRelatedInfo `json:"relatedInformation,omitempty"`
}

// ProtocolRange is the wire format of a text range.
type ProtocolRange struct {
	Start ProtocolPosition `json:"start"`
	End   ProtocolPosition `json:"end"`
}

// ProtocolPosition is the wire format of a position (0-based line + character).
type ProtocolPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// ProtocolRelatedInfo is the wire format of a related diagnostic location.
type ProtocolRelatedInfo struct {
	Location ProtocolLocation `json:"location"`
	Message  string           `json:"message"`
}

// ProtocolLocation is the wire format of a document location.
type ProtocolLocation struct {
	URI   string        `json:"uri"`
	Range ProtocolRange `json:"range"`
}

// ---------------------------------------------------------------------------
// Conversions: wire types → existing domain types
// ---------------------------------------------------------------------------

// ToServerCapabilities maps the raw wire ServerCaps to the domain
// ServerCapabilities type from types.go.
func ToServerCapabilities(sc ServerCaps) ServerCapabilities {
	return ServerCapabilities{
		HoverProvider:              isTruthy(sc.HoverProvider),
		DefinitionProvider:         isTruthy(sc.DefinitionProvider),
		ReferencesProvider:         isTruthy(sc.ReferencesProvider),
		CompletionProvider:         isTruthy(sc.CompletionProvider),
		DiagnosticProvider:         isTruthy(sc.DiagnosticProvider),
		DocumentSymbolProvider:     isTruthy(sc.DocumentSymbol),
		WorkspaceSymbolProvider:    isTruthy(sc.WorkspaceSymbol),
		CodeActionProvider:         isTruthy(sc.CodeActionProvider),
		RenameProvider:             isTruthy(sc.RenameProvider),
		DocumentHighlightProvider:  isTruthy(sc.DocumentHighlightProvider),
	}
}

// ToSyncKind extracts the TextDocumentSyncKind from the polymorphic
// textDocumentSync capability. The spec allows this to be either a bare
// integer or an object with a "change" field.
func ToSyncKind(raw json.RawMessage) TextDocumentSyncKind {
	if len(raw) == 0 {
		return SyncNone
	}
	// Try bare integer first.
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return TextDocumentSyncKind(n)
	}
	// Try object with "change" field.
	var obj struct {
		Change int `json:"change"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return TextDocumentSyncKind(obj.Change)
	}
	return SyncNone
}

// ToDiagnosticResult converts wire-format diagnostics to the domain
// DiagnosticResult type from types.go.
func ToDiagnosticResult(serverID ServerID, params PublishDiagnosticsParams) DiagnosticResult {
	diags := make([]Diagnostic, len(params.Diagnostics))
	for i, pd := range params.Diagnostics {
		diags[i] = toCoreDiagnostic(pd)
	}
	return DiagnosticResult{
		ServerID:    serverID,
		FilePath:    FileURIToPath(params.URI),
		Diagnostics: diags,
	}
}

// toCoreDiagnostic converts a single wire diagnostic to the domain type.
func toCoreDiagnostic(pd ProtocolDiagnostic) Diagnostic {
	return Diagnostic{
		Range:              toCoreRange(pd.Range),
		Severity:           DiagnosticSeverity(pd.Severity),
		Code:               extractCode(pd.Code),
		Source:             pd.Source,
		Message:            pd.Message,
		RelatedInformation: toCoreRelatedInfo(pd.RelatedInformation),
	}
}

// toCoreRange converts a wire range to the domain Range.
func toCoreRange(pr ProtocolRange) Range {
	return Range{
		Start: Position{Line: pr.Start.Line, Character: pr.Start.Character},
		End:   Position{Line: pr.End.Line, Character: pr.End.Character},
	}
}

// toCoreRelatedInfo converts wire related info to domain types.
func toCoreRelatedInfo(infos []ProtocolRelatedInfo) []DiagnosticRelatedInformation {
	if len(infos) == 0 {
		return nil
	}
	result := make([]DiagnosticRelatedInformation, len(infos))
	for i, info := range infos {
		result[i] = DiagnosticRelatedInformation{
			Location: Location{
				URI:   info.Location.URI,
				Range: toCoreRange(info.Location.Range),
			},
			Message: info.Message,
		}
	}
	return result
}

// extractCode extracts the diagnostic code from the polymorphic JSON field.
// LSP allows codes to be either a string or a number.
func extractCode(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return fmt.Sprintf("%d", n)
	}
	return ""
}

// isTruthy returns true if the raw JSON is non-null and non-false.
// Capability fields can be true, a non-null object, or absent.
func isTruthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	s := string(raw)
	return s != "false" && s != "null"
}

// ---------------------------------------------------------------------------
// Completion (textDocument/completion)
// ---------------------------------------------------------------------------

// CompletionTriggerKind indicates how a completion request was triggered.
type CompletionTriggerKind int

const (
	// CompletionTriggerInvoked is explicitly invoked (e.g., Ctrl+Space).
	CompletionTriggerInvoked CompletionTriggerKind = 1
	// CompletionTriggerChar is triggered by a trigger character.
	CompletionTriggerChar CompletionTriggerKind = 2
	// CompletionTriggerIncomplete is re-triggered for incomplete results.
	CompletionTriggerIncomplete CompletionTriggerKind = 3
)

// TextDocumentPositionParams identifies a position in a text document.
// Shared by hover, definition, and other position-based requests.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     ProtocolPosition       `json:"position"`
}

// CompletionParams is the request body for textDocument/completion.
type CompletionParams struct {
	TextDocumentPositionParams
	Context *CompletionContext `json:"context,omitempty"`
}

// CompletionContext provides additional information about the context
// in which a completion request was triggered.
type CompletionContext struct {
	TriggerKind      CompletionTriggerKind `json:"triggerKind"`
	TriggerCharacter string                `json:"triggerCharacter,omitempty"`
}

// ProtocolCompletionList is the wire format for a completion response
// when the server returns a list wrapper.
type ProtocolCompletionList struct {
	IsIncomplete bool                      `json:"isIncomplete"`
	Items        []ProtocolCompletionItem  `json:"items"`
}

// ProtocolCompletionItem is the wire format of a single completion item.
type ProtocolCompletionItem struct {
	Label            string          `json:"label"`
	Kind             int             `json:"kind,omitempty"`
	Detail           string          `json:"detail,omitempty"`
	Documentation    json.RawMessage `json:"documentation,omitempty"`
	InsertText       string          `json:"insertText,omitempty"`
	InsertTextFormat int             `json:"insertTextFormat,omitempty"`
	SortText         string          `json:"sortText,omitempty"`
	FilterText       string          `json:"filterText,omitempty"`
	TextEdit         *ProtocolTextEdit `json:"textEdit,omitempty"`
}

// ProtocolTextEdit is the wire format of a text edit within a completion item.
type ProtocolTextEdit struct {
	Range   ProtocolRange `json:"range"`
	NewText string        `json:"newText"`
}

// ---------------------------------------------------------------------------
// Hover (textDocument/hover)
// ---------------------------------------------------------------------------

// ProtocolHoverResult is the wire format for textDocument/hover response.
type ProtocolHoverResult struct {
	Contents ProtocolHoverContents `json:"contents"`
	Range    *ProtocolRange        `json:"range,omitempty"`
}

// ProtocolHoverContents handles the polymorphic "contents" field.
// LSP allows MarkupContent, MarkedString, or an array of MarkedString.
// We normalize to MarkupContent during conversion.
type ProtocolHoverContents struct {
	Kind  string `json:"kind,omitempty"`
	Value string `json:"value,omitempty"`
}

// UnmarshalJSON handles the polymorphic hover contents field.
func (c *ProtocolHoverContents) UnmarshalJSON(data []byte) error {
	// Try MarkupContent first: {"kind":"markdown","value":"..."}
	var mc struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if json.Unmarshal(data, &mc) == nil && mc.Value != "" {
		c.Kind = mc.Kind
		c.Value = mc.Value
		return nil
	}
	// Try bare string (MarkedString shorthand).
	var s string
	if json.Unmarshal(data, &s) == nil {
		c.Kind = "plaintext"
		c.Value = s
		return nil
	}
	// Try array of MarkedString/string.
	var arr []json.RawMessage
	if json.Unmarshal(data, &arr) == nil {
		var buf strings.Builder
		for i, raw := range arr {
			if i > 0 {
				buf.WriteString("\n\n")
			}
			var str string
			if json.Unmarshal(raw, &str) == nil {
				buf.WriteString(str)
				continue
			}
			var ms struct {
				Language string `json:"language"`
				Value    string `json:"value"`
			}
			if json.Unmarshal(raw, &ms) == nil {
				buf.WriteString("```")
				buf.WriteString(ms.Language)
				buf.WriteString("\n")
				buf.WriteString(ms.Value)
				buf.WriteString("\n```")
			}
		}
		c.Kind = "markdown"
		c.Value = buf.String()
		return nil
	}
	return nil
}

// ---------------------------------------------------------------------------
// Document Highlight (textDocument/documentHighlight)
// ---------------------------------------------------------------------------

// ProtocolDocumentHighlight is the wire format of a single document highlight.
type ProtocolDocumentHighlight struct {
	Range ProtocolRange `json:"range"`
	Kind  int           `json:"kind,omitempty"`
}

// ToDocumentHighlights converts wire-format highlights to domain types.
func ToDocumentHighlights(items []ProtocolDocumentHighlight) []DocumentHighlight {
	result := make([]DocumentHighlight, len(items))
	for i, ph := range items {
		kind := DocumentHighlightKind(ph.Kind)
		if kind == 0 {
			kind = HighlightText
		}
		result[i] = DocumentHighlight{
			Range: toCoreRange(ph.Range),
			Kind:  kind,
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// References (textDocument/references)
// ---------------------------------------------------------------------------

// ReferenceParams is the request body for textDocument/references.
type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

// ReferenceContext controls what references are returned.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ---------------------------------------------------------------------------
// Completion provider capabilities
// ---------------------------------------------------------------------------

// CompletionProviderCaps extracts trigger characters from the CompletionProvider
// capability. Returns nil if absent.
func CompletionTriggerChars(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var opts struct {
		TriggerCharacters []string `json:"triggerCharacters"`
	}
	if json.Unmarshal(raw, &opts) != nil {
		return nil
	}
	return opts.TriggerCharacters
}

// ---------------------------------------------------------------------------
// URI helpers
// ---------------------------------------------------------------------------

// FileURIToPath converts a "file:///path" URI to an OS path.
func FileURIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return strings.TrimPrefix(uri, "file://")
	}
	return u.Path
}

// PathToFileURI converts an absolute OS path to a "file:///path" URI.
func PathToFileURI(path string) string {
	return "file://" + path
}
