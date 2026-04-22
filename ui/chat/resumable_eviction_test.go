package chat

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// TestResumablePrimary_EvictedOnSiblingAgentTurn reproduces the
// "chat panel jumps back" bug: Inspector → Tester → Inspector.
//
// Before the fix the second Inspector's StreamStart latched onto the
// first Inspector's entry via reusableResumablePrimaryEntry (match
// by agent_type/session/task), inserting new tool calls back at the
// old block position.
//
// After the fix the Tester's entry append evicts the Inspector's
// resumable primary because Tester's correlation is not a consult/
// challenge child of the Inspector. The second Inspector's
// StreamStart then falls through to the new-entry path and appends
// its own block at the tail of history.
func TestResumablePrimary_EvictedOnSiblingAgentTurn(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	// Seed an Inspector entry and register it as a resumable
	// primary (mimicking completion after the first inspector turn).
	inspectorCID := "corr-inspector-first"
	inspector := &ChatEntry{
		ID:            "inspector-1",
		Timestamp:     time.Now(),
		CorrelationID: inspectorCID,
		Source:        SourceAgent,
		AgentType:     "inspector-pipeline",
		AgentID:       "inspector-pipeline",
		TaskID:        "task_hello_cli",
		SessionID:     "s1",
		Content:       "Initial inspector audit.",
		Streaming:     false,
		Height:        -1,
	}
	m.PushEntry(inspector)
	inspectorIdx := m.history.Len() - 1
	slot := &streamSlot{
		accumulator: NewStreamAccumulator(inspectorIdx),
		agentID:     "inspector-pipeline",
		thinkingIdx: inspectorIdx,
		renderState: &streamRenderState{},
	}
	m.recordResumableCompletedPrimary(inspectorCID, slot)
	if _, ok := m.resumablePrimaries[inspectorCID]; !ok {
		t.Fatal("setup: inspector primary not registered")
	}

	// Now push a Tester entry as a sibling top-level turn.
	testerCID := "corr-tester-handoff"
	tester := &ChatEntry{
		ID:            "tester-1",
		Timestamp:     time.Now().Add(100 * time.Millisecond),
		CorrelationID: testerCID,
		Source:        SourceAgent,
		AgentType:     "tester-pipeline",
		AgentID:       "tester-pipeline",
		TaskID:        "task_hello_cli",
		SessionID:     "s1",
		Content:       "Tester validating.",
		Streaming:     false,
		Height:        -1,
	}
	m.PushEntry(tester)

	if _, ok := m.resumablePrimaries[inspectorCID]; ok {
		t.Fatal("expected Inspector's resumable primary to be evicted when a Tester sibling entry appends below")
	}

	// Now the second Inspector turn arrives via StreamStart. With
	// the primary evicted, reusableResumablePrimaryEntry must NOT
	// match — the handler should create a new entry at the tail.
	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector-second",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		TaskID:        "task_hello_cli",
	})
	m = comp.(*Model)

	if m.history.Len() != 3 {
		t.Fatalf("history len = %d, want 3 (inspector1, tester, inspector2)", m.history.Len())
	}
	last := m.history.Get(m.history.Len() - 1)
	if last == nil {
		t.Fatal("no tail entry")
	}
	if last.CorrelationID != "corr-inspector-second" {
		t.Errorf("tail entry correlation = %q, want corr-inspector-second (new block, not a jump back)", last.CorrelationID)
	}
	// The original Inspector entry must retain its original
	// correlation ID — the new turn did not rewrite it.
	first := m.history.Get(0)
	if first == nil || first.CorrelationID != inspectorCID {
		t.Errorf("first entry correlation = %q, want %q (original inspector preserved)", first.CorrelationID, inspectorCID)
	}
}

// TestResumablePrimary_PreservedOnChildConsult verifies the fix
// does NOT break the legitimate within-turn continuity case. When a
// consult/challenge child entry appends below an inspector primary,
// the primary stays valid so the eventual continuation (via
// ContinuationOfCorrelationID) can still resume the original
// inspector entry rather than creating a spurious second block.
func TestResumablePrimary_PreservedOnChildConsult(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	inspectorCID := "corr-inspector"
	inspector := &ChatEntry{
		ID:            "inspector",
		Timestamp:     time.Now(),
		CorrelationID: inspectorCID,
		Source:        SourceAgent,
		AgentType:     "inspector-pipeline",
		AgentID:       "inspector-pipeline",
		TaskID:        "task_x",
		SessionID:     "s1",
		Height:        -1,
	}
	m.PushEntry(inspector)
	inspectorIdx := m.history.Len() - 1
	slot := &streamSlot{
		accumulator: NewStreamAccumulator(inspectorIdx),
		agentID:     "inspector-pipeline",
		thinkingIdx: inspectorIdx,
		renderState: &streamRenderState{},
	}
	m.recordResumableCompletedPrimary(inspectorCID, slot)

	// Simulate the inspector emitting a consult/challenge —
	// the child CID gets registered as owned by inspectorCID.
	childCID := "corr-challenge-child"
	m.noteResumableChildOwner(childCID, inspectorCID)

	// Now the child entry (e.g. the respondent agent's own
	// top-level appearance) lands in history. Because it's a
	// registered child, eviction must NOT fire.
	child := &ChatEntry{
		ID:            "child",
		Timestamp:     time.Now().Add(50 * time.Millisecond),
		CorrelationID: childCID,
		Source:        SourceAgent,
		AgentType:     "tester-pipeline",
		AgentID:       "tester-pipeline",
		TaskID:        "task_x",
		SessionID:     "s1",
		Height:        -1,
	}
	m.PushEntry(child)

	if _, ok := m.resumablePrimaries[inspectorCID]; !ok {
		t.Error("inspector primary should survive a legitimate child consult/challenge append")
	}
}

// TestEvictStaleResumablePrimariesForAppend_NoOpEmpty covers the
// nil-safe and empty-map short-circuits so the helper stays cheap
// on the common path (most pushes have no pending primaries).
func TestEvictStaleResumablePrimariesForAppend_NoOpEmpty(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	// No resumable primaries registered; helper must not panic or
	// create the map.
	m.evictStaleResumablePrimariesForAppend("corr-x")
	if m.resumablePrimaries == nil {
		return // acceptable: helper is a pure no-op
	}
	if len(m.resumablePrimaries) != 0 {
		t.Errorf("empty-map case should not add entries; got %d", len(m.resumablePrimaries))
	}
}
