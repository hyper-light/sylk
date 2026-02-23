package guide

import "testing"

func TestSessionRouter_SessionStateCreatesSession(t *testing.T) {
	sr := NewSessionRouter(nil)
	cache, prefs, hasCache, hasPrefs := sr.sessionState("session-ctx")
	if !hasCache || !hasPrefs {
		t.Fatalf("expected session cache and prefs to exist")
	}
	if cache == nil || prefs == nil {
		t.Fatalf("expected non-nil session cache and prefs")
	}
	if stats := sr.Stats(); stats.ActiveSessions != 1 {
		t.Fatalf("active sessions = %d, want 1", stats.ActiveSessions)
	}
}
