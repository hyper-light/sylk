package providers

import (
	"testing"
	"time"
)

func TestStreamAccumulator_ThinkingAccumulation(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Add(&StreamChunk{Type: ChunkTypeStart, Timestamp: time.Now()})
	acc.Add(&StreamChunk{Type: ChunkTypeThought, Text: "Let me think", Timestamp: time.Now()})
	acc.Add(&StreamChunk{Type: ChunkTypeThought, Text: " about this.", Timestamp: time.Now()})
	acc.Add(&StreamChunk{Type: ChunkTypeText, Text: "Hello world", Timestamp: time.Now()})
	acc.Add(&StreamChunk{Type: ChunkTypeEnd, Usage: &Usage{OutputTokens: 10}, StopReason: StopReasonEndTurn, Timestamp: time.Now()})

	resp := acc.Response()
	if resp.Content != "Hello world" {
		t.Fatalf("Content = %q, want %q", resp.Content, "Hello world")
	}
	if resp.Thinking != "Let me think about this." {
		t.Fatalf("Thinking = %q, want %q", resp.Thinking, "Let me think about this.")
	}
}

func TestStreamAccumulator_ThinkingAccessor(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Add(&StreamChunk{Type: ChunkTypeThought, Text: "  reasoning  ", Timestamp: time.Now()})

	if got := acc.Thinking(); got != "reasoning" {
		t.Fatalf("Thinking() = %q, want %q (trimmed)", got, "reasoning")
	}
}

func TestStreamAccumulator_ThinkingResetOnRetry(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Add(&StreamChunk{Type: ChunkTypeThought, Text: "first attempt", Timestamp: time.Now()})
	// Retry resets everything.
	acc.Add(&StreamChunk{Type: ChunkTypeStart, Timestamp: time.Now()})
	acc.Add(&StreamChunk{Type: ChunkTypeThought, Text: "second attempt", Timestamp: time.Now()})
	acc.Add(&StreamChunk{Type: ChunkTypeEnd, Usage: &Usage{OutputTokens: 5}, StopReason: StopReasonEndTurn, Timestamp: time.Now()})

	resp := acc.Response()
	if resp.Thinking != "second attempt" {
		t.Fatalf("Thinking = %q, want %q (reset on retry)", resp.Thinking, "second attempt")
	}
}

func TestStreamAccumulator_EmptyThinking(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Add(&StreamChunk{Type: ChunkTypeStart, Timestamp: time.Now()})
	acc.Add(&StreamChunk{Type: ChunkTypeText, Text: "no thoughts", Timestamp: time.Now()})
	acc.Add(&StreamChunk{Type: ChunkTypeEnd, Usage: &Usage{OutputTokens: 5}, StopReason: StopReasonEndTurn, Timestamp: time.Now()})

	resp := acc.Response()
	if resp.Thinking != "" {
		t.Fatalf("Thinking = %q, want empty", resp.Thinking)
	}
}

func TestStreamAccumulator_NativeWebSearchDoesNotBecomeExecutableToolCall(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Add(&StreamChunk{Type: ChunkTypeStart, Timestamp: time.Now()})
	acc.Add(&StreamChunk{
		Type: ChunkTypeToolStart,
		ToolCall: &ToolCallChunk{
			ID:   "ws_1",
			Name: "web_search",
			Kind: ToolKindNativeWebSearch,
		},
		Timestamp: time.Now(),
	})
	acc.Add(&StreamChunk{
		Type: ChunkTypeToolDelta,
		ToolCall: &ToolCallChunk{
			ID:             "ws_1",
			Kind:           ToolKindNativeWebSearch,
			ArgumentsDelta: `{"query":"python packaging pep 621","action":"search"}`,
		},
		Timestamp: time.Now(),
	})
	acc.Add(&StreamChunk{
		Type:       ChunkTypeEnd,
		Usage:      &Usage{OutputTokens: 5},
		StopReason: StopReasonEndTurn,
		Timestamp:  time.Now(),
	})

	resp := acc.Response()
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("tool calls = %#v, want no executable tool calls", resp.ToolCalls)
	}
	calls := DecodeNativeWebSearchCalls(resp.ProviderMetadata)
	if len(calls) != 1 {
		t.Fatalf("native search call count = %d, want 1", len(calls))
	}
	if calls[0].Query != "python packaging pep 621" {
		t.Fatalf("query = %q, want %q", calls[0].Query, "python packaging pep 621")
	}
}

func TestStreamAccumulator_PreservesNativeWebSearchResultsMetadata(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Add(&StreamChunk{Type: ChunkTypeStart, Timestamp: time.Now()})
	acc.Add(&StreamChunk{
		Type:       ChunkTypeEnd,
		StopReason: StopReasonEndTurn,
		Usage:      &Usage{OutputTokens: 1},
		ProviderData: map[string]any{
			ProviderMetadataNativeWebSearchResultsKey: []NativeWebSearchResult{
				{
					SearchID: "search_1",
					Provider: "anthropic",
					Source:   "search_result",
					Query:    "python packaging pep 621",
					URL:      "https://peps.python.org/pep-0621/",
					Title:    "PEP 621",
					PageAge:  "30d",
				},
			},
		},
		Timestamp: time.Now(),
	})

	resp := acc.Response()
	results := DecodeNativeWebSearchResults(resp.ProviderMetadata)
	if len(results) != 1 {
		t.Fatalf("native search result count = %d, want 1", len(results))
	}
	if results[0].URL != "https://peps.python.org/pep-0621/" {
		t.Fatalf("url = %q, want https://peps.python.org/pep-0621/", results[0].URL)
	}
	if results[0].Title != "PEP 621" {
		t.Fatalf("title = %q, want PEP 621", results[0].Title)
	}
}
