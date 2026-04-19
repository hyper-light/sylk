package bridge

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/msg"
)

// TestSessionSwitchedMsgFromEvent_FiresOnEventSwitched is the UI-13
// filter contract: only session.EventSwitched produces a narrowed
// SessionSwitchedMsg. A regression that leaked other lifecycle
// events through this path would cause the AppModel to clobber
// editor state on every pause/resume.
func TestSessionSwitchedMsgFromEvent_FiresOnEventSwitched(t *testing.T) {
	event := &session.Event{
		Type:      session.EventSwitched,
		SessionID: "sess-new",
		Timestamp: time.Now(),
	}
	got, ok := sessionSwitchedMsgFromEvent(event, "sess-old")
	if !ok {
		t.Fatal("expected SessionSwitchedMsg for EventSwitched, got ok=false")
	}
	if got.ActiveID != "sess-new" {
		t.Errorf("ActiveID = %q, want sess-new", got.ActiveID)
	}
	if got.PreviousID != "sess-old" {
		t.Errorf("PreviousID = %q, want sess-old", got.PreviousID)
	}
	if got.Event != event {
		t.Error("Event pointer not propagated to SessionSwitchedMsg")
	}
}

// TestSessionSwitchedMsgFromEvent_SkipsNonSwitchEvents verifies the
// negative path: every non-switch event type must yield ok=false so
// subscribers don't receive spurious switch signals.
func TestSessionSwitchedMsgFromEvent_SkipsNonSwitchEvents(t *testing.T) {
	types := []session.EventType{
		session.EventCreated,
		session.EventStarted,
		session.EventPaused,
		session.EventResumed,
		session.EventSuspended,
		session.EventRestored,
		session.EventCompleted,
		session.EventFailed,
		session.EventClosed,
	}
	for _, typ := range types {
		event := &session.Event{Type: typ, SessionID: "sess-x"}
		if _, ok := sessionSwitchedMsgFromEvent(event, ""); ok {
			t.Errorf("non-switch type %q leaked past the filter", typ)
		}
	}
}

// TestSessionSwitchedMsgFromEvent_NilAndEmptyIDAreRejected protects
// the caller against spurious messages with no actionable payload.
// An empty ActiveID would produce a no-op switch that still triggers
// expensive AppModel reset work downstream.
func TestSessionSwitchedMsgFromEvent_NilAndEmptyIDAreRejected(t *testing.T) {
	if _, ok := sessionSwitchedMsgFromEvent(nil, "sess-old"); ok {
		t.Error("nil event leaked past the filter")
	}
	event := &session.Event{Type: session.EventSwitched, SessionID: ""}
	if _, ok := sessionSwitchedMsgFromEvent(event, "sess-old"); ok {
		t.Error("empty SessionID leaked past the filter")
	}
}

// TestSessionSwitchedMsgFromEvent_FirstActivationHasNoPrevious
// documents the bootstrap path: the very first session switch
// happens with previousActiveID=="" and that is the correct
// PreviousID value on the message — subscribers treat "" as "no
// prior session" (don't try to persist to a nonexistent dir).
func TestSessionSwitchedMsgFromEvent_FirstActivationHasNoPrevious(t *testing.T) {
	event := &session.Event{Type: session.EventSwitched, SessionID: "sess-first"}
	got, ok := sessionSwitchedMsgFromEvent(event, "")
	if !ok {
		t.Fatal("first activation should still produce a switch msg")
	}
	if got.PreviousID != "" {
		t.Errorf("PreviousID = %q, want empty on first activation", got.PreviousID)
	}
	if got.ActiveID != "sess-first" {
		t.Errorf("ActiveID = %q, want sess-first", got.ActiveID)
	}
	// Silence the unused-import lint in msg without creating a
	// second assertion — the type check is intentional.
	_ = msg.SessionSwitchedMsg{}
}
