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

// Gather returns cached items that match the prefix. Each returned item's
// Word is prefixed with the dot-qualifier (e.g. "fmt.") so that
// acceptCompletion replaces the full typed prefix correctly.
func (s *LSPSource) Gather(ctx CompletionContext) []CompletionItem {
	s.mu.RLock()
	items := s.items
	s.mu.RUnlock()

	if ctx.Prefix == "" || len(items) == 0 {
		return nil
	}
	qualifier, filterPrefix := lspSplitPrefix(ctx.Prefix)
	return filterLSPItems(items, qualifier, filterPrefix)
}

// lspSplitPrefix splits a completion prefix into a dot-qualifier and the
// trailing symbol portion. For "fmt.Pr" it returns ("fmt.", "Pr"). When
// there is no dot, qualifier is empty and the full prefix is returned.
func lspSplitPrefix(prefix string) (qualifier, filter string) {
	if idx := strings.LastIndex(prefix, "."); idx >= 0 {
		return prefix[:idx+1], prefix[idx+1:]
	}
	return "", prefix
}

// filterLSPItems returns items whose Word starts with filterPrefix,
// prepending qualifier to each item's Word so the engine's startCol-based
// replacement covers the full typed prefix (e.g. "fmt." + "Println").
// An empty filterPrefix (user just typed the dot) matches all items.
func filterLSPItems(items []CompletionItem, qualifier, filterPrefix string) []CompletionItem {
	if filterPrefix == "" {
		return qualifyItems(items, qualifier)
	}
	prefixLower := strings.ToLower(filterPrefix)
	var result []CompletionItem
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item.Word), prefixLower) {
			qualified := item
			qualified.Word = qualifier + item.Word
			result = append(result, qualified)
		}
	}
	return result
}

// qualifyItems returns a copy of all items with qualifier prepended to Word.
func qualifyItems(items []CompletionItem, qualifier string) []CompletionItem {
	result := make([]CompletionItem, len(items))
	for i, item := range items {
		item.Word = qualifier + item.Word
		result[i] = item
	}
	return result
}
