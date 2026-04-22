package bridge

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/ui/msg"
)

// TestConvertData_ProducesStreamChunkMsg pins the bridge's
// StreamEventData → StreamChunkMsg conversion. This is the hinge
// that takes a per-chunk text payload off the bus and turns it into
// the tea.Msg the chat panel consumes. Without this test, a silent
// regression in convertData (dropping Text, mis-keying CorrelationID,
// changing the message type) would only surface by the chat panel
// rendering blank.
func TestConvertData_ProducesStreamChunkMsg(t *testing.T) {
	d := &streamDispatcher{sessionID: "session-xyz", correlationID: "corr-abc"}
	event := &guide.StreamEvent{
		Type: guide.StreamEventData,
		Text: "searching the index",
	}

	got := convertData(d, event)
	chunk, ok := got.(msg.StreamChunkMsg)
	if !ok {
		t.Fatalf("convertData returned %T, want msg.StreamChunkMsg", got)
	}
	if chunk.Text != "searching the index" {
		t.Errorf("chunk.Text = %q, want %q", chunk.Text, "searching the index")
	}
	if chunk.CorrelationID != "corr-abc" {
		t.Errorf("chunk.CorrelationID = %q, want corr-abc", chunk.CorrelationID)
	}
	if chunk.SessionID != "session-xyz" {
		t.Errorf("chunk.SessionID = %q, want session-xyz", chunk.SessionID)
	}
}

// TestConvertData_EmptyTextProducesEmptyChunkMsg pins the edge case:
// the bridge does NOT filter empty text here — the gating happens
// upstream at dispatchStream (guide.go:668) where text=="" AND
// earlyUsage==0 chunks are skipped. convertData itself is expected
// to produce the message unconditionally so the caller retains
// control over the suppression policy.
func TestConvertData_EmptyTextProducesEmptyChunkMsg(t *testing.T) {
	d := &streamDispatcher{sessionID: "s1", correlationID: "c1"}
	event := &guide.StreamEvent{Type: guide.StreamEventData, Text: ""}
	got := convertData(d, event)
	chunk, ok := got.(msg.StreamChunkMsg)
	if !ok {
		t.Fatalf("convertData returned %T, want msg.StreamChunkMsg", got)
	}
	if chunk.Text != "" {
		t.Errorf("chunk.Text = %q, want empty", chunk.Text)
	}
}
