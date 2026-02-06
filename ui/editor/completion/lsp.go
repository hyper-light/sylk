package completion

import (
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/lsp"
)

// KindLSP classifies items originating from a language server.
const KindLSP CompletionKind = 100

func init() {
	kindNameTable[KindLSP] = "LSP"
}

// LSPSource provides completion items from a language server. Items are
// populated asynchronously by the app layer and consumed by the engine.
type LSPSource struct {
	mu    sync.RWMutex
	items []CompletionItem
}

// NewLSPSource creates an empty LSP completion source.
func NewLSPSource() *LSPSource {
	return &LSPSource{}
}

// ID returns the source identifier.
func (s *LSPSource) ID() string { return "lsp" }

// SetItems replaces the cached LSP completion items. Called by the app
// layer when an LSP completion response arrives.
func (s *LSPSource) SetItems(lspItems []lsp.CompletionItem) {
	items := make([]CompletionItem, len(lspItems))
	for i, li := range lspItems {
		items[i] = CompletionItem{
			Word:  li.InsertText,
			Kind:  KindLSP,
			Menu:  li.Detail,
			Info:  li.Label,
			Score: 200 - i, // preserve server ordering
		}
	}
	s.mu.Lock()
	s.items = items
	s.mu.Unlock()
}

// Clear removes all cached items.
func (s *LSPSource) Clear() {
	s.mu.Lock()
	s.items = nil
	s.mu.Unlock()
}

// Gather returns cached items that match the prefix.
func (s *LSPSource) Gather(ctx CompletionContext) []CompletionItem {
	s.mu.RLock()
	items := s.items
	s.mu.RUnlock()

	if ctx.Prefix == "" || len(items) == 0 {
		return nil
	}

	prefixLower := strings.ToLower(ctx.Prefix)
	var result []CompletionItem
	for _, item := range items {
		wordLower := strings.ToLower(item.Word)
		if strings.HasPrefix(wordLower, prefixLower) {
			result = append(result, item)
		}
	}
	return result
}
