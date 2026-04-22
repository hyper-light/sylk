package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// TestReplicaStreams_ArbitraryConcurrentLibrariansProduceDistinctRows
// pins the N-concurrent-replica invariant: however many librarian
// replicas stream in parallel, the chat panel renders that many
// distinct rows — not a consolidated subset. Identity
// disambiguation happens via RuntimeAgentID (e.g.
// "librarian#replica-corr-N"); each replica carries its own
// correlation ID. The test runs several sizes to make it obvious
// the design isn't hard-coded to a particular fan-out.
func TestReplicaStreams_ArbitraryConcurrentLibrariansProduceDistinctRows(t *testing.T) {
	for _, n := range []int{1, 3, 7, 16, 32} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			// Start with a history capacity deliberately smaller than n
			// to also exercise the grow-to-protect-streaming path.
			initialCap := 4
			if initialCap >= n {
				initialCap = n / 2
				if initialCap < 1 {
					initialCap = 1
				}
			}
			m := New(theme.DefaultDark(), initialCap)
			m.SetSize(120, 40)

			for i := range n {
				comp, _ := m.Update(msg.StreamStartMsg{
					SessionID:      "s1",
					CorrelationID:  fmt.Sprintf("corr-lib-%d", i),
					AgentID:        "librarian",
					RuntimeAgentID: fmt.Sprintf("librarian#replica-corr-%d", i),
					AgentType:      "librarian",
				})
				m = comp.(*Model)
			}

			if m.history.Len() != n {
				t.Fatalf("n=%d: history len = %d, want %d distinct replica rows", n, m.history.Len(), n)
			}

			seen := make(map[string]bool, n)
			for i := range n {
				entry := m.history.Get(i)
				if entry == nil {
					t.Fatalf("n=%d: entry %d missing", n, i)
				}
				if entry.AgentType != "librarian" {
					t.Fatalf("n=%d: entry %d AgentType = %q, want librarian", n, i, entry.AgentType)
				}
				if entry.RuntimeAgentID == "" {
					t.Fatalf("n=%d: entry %d RuntimeAgentID empty — replica identity lost", n, i)
				}
				if seen[entry.RuntimeAgentID] {
					t.Fatalf("n=%d: duplicate RuntimeAgentID %q on row %d — replicas consolidated", n, entry.RuntimeAgentID, i)
				}
				seen[entry.RuntimeAgentID] = true
			}
		})
	}
}

// TestResumablePrimary_ReplicasDoNotCrossConsolidate verifies the
// replica-scoping rule in resumablePrimaryMatches: a completed
// librarian replica's resumable-primary slot must not match a
// different replica's start, even though they share AgentType.
func TestResumablePrimary_ReplicasDoNotCrossConsolidate(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(120, 40)

	firstCID := "corr-lib-first"
	first := &ChatEntry{
		ID:             "lib-1",
		Timestamp:      time.Now(),
		CorrelationID:  firstCID,
		Source:         SourceAgent,
		AgentType:      "librarian",
		AgentID:        "librarian",
		RuntimeAgentID: "librarian#replica-corr-first",
		SessionID:      "s1",
		Content:        "First replica done.",
		Streaming:      false,
		Height:         -1,
	}
	m.PushEntry(first)
	firstIdx := m.history.Len() - 1
	slot := &streamSlot{
		accumulator: NewStreamAccumulator(firstIdx),
		agentID:     "librarian",
		thinkingIdx: firstIdx,
		renderState: &streamRenderState{},
	}
	m.recordResumableCompletedPrimary(firstCID, slot)

	// Second replica start carries a DIFFERENT runtime agent ID. The
	// matcher must reject the first replica's primary — otherwise
	// the second replica's chunks land on the first replica's row.
	start := msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-lib-second",
		AgentID:        "librarian",
		RuntimeAgentID: "librarian#replica-corr-second",
		AgentType:      "librarian",
	}
	if m.resumablePrimaryMatches(firstCID, start) {
		t.Fatal("resumablePrimaryMatches returned true for mismatched replica — replicas cross-consolidated")
	}
}

// TestHistory_StreamingEntryIsNotEvictedOnFullBuffer verifies the
// pipeline-lifecycle-disk-commit analog applied to chat: a still-
// streaming entry at the oldest slot must not be silently dropped
// when the buffer is full. The buffer grows instead so in-flight
// chunks keep their target row.
func TestHistory_StreamingEntryIsNotEvictedOnFullBuffer(t *testing.T) {
	h := NewHistory(2)
	streaming := &ChatEntry{ID: "streaming", Streaming: true, Height: -1}
	nonStreaming := &ChatEntry{ID: "done", Streaming: false, Height: -1}
	h.Push(streaming)
	h.Push(nonStreaming)
	if !h.Full() {
		t.Fatal("setup: history not full")
	}
	if !h.OldestIsStreaming() {
		t.Fatal("setup: oldest entry should be the streaming one")
	}

	// Grow before push, simulating what handleStreamStart does.
	for h.Full() && h.OldestIsStreaming() {
		h.Grow(1)
	}
	h.Push(&ChatEntry{ID: "newcomer", Height: -1})

	// All three original entries (streaming, done, newcomer) must
	// survive — the streaming one cannot have been evicted.
	if h.Len() != 3 {
		t.Fatalf("history len = %d, want 3 after growth-protected push", h.Len())
	}
	if got := h.Get(0); got == nil || got.ID != "streaming" {
		t.Fatalf("oldest entry after growth = %+v, want id=streaming", got)
	}
}

// TestStreamChunks_MultiChunkAccumulationLandsInViewport pins the
// single-stream accumulation invariant: successive text chunks for
// the same correlation ID append to that entry's content, and the
// assembled text reaches the rendered viewport after StreamComplete
// fires the final sync. The existing TestPushEntryInvalidatesCachedView
// covers a single chunk; this extends the coverage to N chunks.
func TestStreamChunks_MultiChunkAccumulationLandsInViewport(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(120, 40)

	start := msg.StreamStartMsg{SessionID: "s1", CorrelationID: "c1", AgentID: "librarian", AgentType: "librarian"}
	comp, _ := m.Update(start)
	m = comp.(*Model)

	chunks := []string{"Reading ", "the index ", "for matches."}
	for _, text := range chunks {
		comp, _ = m.Update(msg.StreamChunkMsg{SessionID: "s1", CorrelationID: "c1", Text: text})
		m = comp.(*Model)
	}

	comp, _ = m.Update(msg.StreamCompleteMsg{SessionID: "s1", CorrelationID: "c1"})
	m = comp.(*Model)

	view := m.View()
	want := strings.Join(chunks, "")
	if !strings.Contains(view, want) {
		t.Fatalf("view missing accumulated stream text\nwant substring: %q\nview: %s", want, view)
	}

	entry := m.history.Last()
	if entry == nil {
		t.Fatal("no entry after stream complete")
	}
	if !strings.Contains(entry.Content, want) {
		t.Fatalf("entry.Content = %q, want to contain %q", entry.Content, want)
	}
}

// TestStreamChunks_ConcurrentRepliasDoNotCrossContaminate pins the
// multi-replica chunk-routing invariant: when two replicas stream in
// parallel and their chunks interleave on the bus, each replica's
// text lands in its own entry — never in a sibling's. The earlier
// row-creation test proves distinct entries exist; this proves the
// chunk payloads route correctly.
func TestStreamChunks_ConcurrentReplicasDoNotCrossContaminate(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(160, 40)

	replicas := []struct {
		cid     string
		runtime string
		chunks  []string
	}{
		{"corr-a", "librarian#replica-corr-a", []string{"alpha-1 ", "alpha-2 ", "alpha-3"}},
		{"corr-b", "librarian#replica-corr-b", []string{"beta-1 ", "beta-2 ", "beta-3"}},
	}

	for _, r := range replicas {
		comp, _ := m.Update(msg.StreamStartMsg{
			SessionID:      "s1",
			CorrelationID:  r.cid,
			AgentID:        "librarian",
			RuntimeAgentID: r.runtime,
			AgentType:      "librarian",
		})
		m = comp.(*Model)
	}

	// Interleave chunks across replicas to simulate concurrent delivery.
	maxChunks := 0
	for _, r := range replicas {
		if len(r.chunks) > maxChunks {
			maxChunks = len(r.chunks)
		}
	}
	for i := range maxChunks {
		for _, r := range replicas {
			if i >= len(r.chunks) {
				continue
			}
			comp, _ := m.Update(msg.StreamChunkMsg{
				SessionID:     "s1",
				CorrelationID: r.cid,
				Text:          r.chunks[i],
			})
			m = comp.(*Model)
		}
	}

	// Complete each stream so final sync lands.
	for _, r := range replicas {
		comp, _ := m.Update(msg.StreamCompleteMsg{SessionID: "s1", CorrelationID: r.cid})
		m = comp.(*Model)
	}

	entriesByCID := map[string]*ChatEntry{}
	for i := range m.history.Len() {
		e := m.history.Get(i)
		if e == nil {
			continue
		}
		entriesByCID[e.CorrelationID] = e
	}
	for _, r := range replicas {
		entry := entriesByCID[r.cid]
		if entry == nil {
			t.Fatalf("no entry for cid %q", r.cid)
		}
		wantOwn := strings.Join(r.chunks, "")
		if !strings.Contains(entry.Content, wantOwn) {
			t.Fatalf("entry %q Content missing own text\nwant: %q\ngot: %q", r.cid, wantOwn, entry.Content)
		}
		// Cross-contamination check: no sibling's text should appear.
		for _, other := range replicas {
			if other.cid == r.cid {
				continue
			}
			for _, fragment := range other.chunks {
				if strings.Contains(entry.Content, strings.TrimSpace(fragment)) {
					t.Fatalf("entry %q contains sibling fragment %q — cross-contamination\nfull content: %q",
						r.cid, fragment, entry.Content)
				}
			}
		}
	}
}

// TestStreamChunks_UnknownCorrelationIDDropsSilently pins the
// defensive invariant: a chunk whose correlation ID doesn't map to
// an open stream slot is dropped without panic and without mutating
// any other entry. Covers the chatDebugLog "NO_SLOT — chunk
// dropped" branch at model.go:handleStreamChunk.
func TestStreamChunks_UnknownCorrelationIDDropsSilently(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(120, 40)

	// Open one legitimate stream and push a chunk.
	comp, _ := m.Update(msg.StreamStartMsg{SessionID: "s1", CorrelationID: "known", AgentID: "librarian", AgentType: "librarian"})
	m = comp.(*Model)
	comp, _ = m.Update(msg.StreamChunkMsg{SessionID: "s1", CorrelationID: "known", Text: "legit "})
	m = comp.(*Model)

	// Stray chunk with unknown correlation ID — must not crash and
	// must not contaminate the known entry.
	comp, _ = m.Update(msg.StreamChunkMsg{SessionID: "s1", CorrelationID: "orphan-cid", Text: "INVADER"})
	m = comp.(*Model)

	// Finalize the legitimate stream so content syncs.
	comp, _ = m.Update(msg.StreamCompleteMsg{SessionID: "s1", CorrelationID: "known"})
	m = comp.(*Model)

	if m.history.Len() != 1 {
		t.Fatalf("history len = %d, want 1 (orphan chunk must not create a new entry)", m.history.Len())
	}
	entry := m.history.Last()
	if entry == nil {
		t.Fatal("no entry after stream complete")
	}
	if strings.Contains(entry.Content, "INVADER") {
		t.Fatalf("entry.Content leaked orphan chunk text: %q", entry.Content)
	}
	if !strings.Contains(entry.Content, "legit") {
		t.Fatalf("entry.Content missing legitimate text: %q", entry.Content)
	}
}
