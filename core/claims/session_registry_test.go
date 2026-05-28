package claims

import (
	"errors"
	"sync"
	"testing"
)

const sessionRegistryConcurrentSwitchAttempts = 64

func TestSessionBoardRegistryRegisterLookupReplaceAndRemove(t *testing.T) {
	registry := &SessionBoardRegistry{boards: make(map[string]*ClaimsBoard)}
	first := NewClaimsBoard(ClaimsBoardConfig{BoardID: "board-one", SessionID: "session", TaskID: "task"})
	if err := registry.Register("session", first); err != nil {
		t.Fatalf("Register first: %v", err)
	}
	if got := registry.Lookup("session"); got != first {
		t.Fatalf("Lookup returned %p, want %p", got, first)
	}
	if err := registry.Register("session", NewClaimsBoard(ClaimsBoardConfig{BoardID: "board-two", SessionID: "session", TaskID: "task"})); !errors.Is(err, ErrSessionBoardAlreadyRegistered) {
		t.Fatalf("duplicate Register error = %v, want ErrSessionBoardAlreadyRegistered", err)
	}
	replacement := NewClaimsBoard(ClaimsBoardConfig{BoardID: "board-replay", SessionID: "session", TaskID: "task"})
	registry.ReplaceForReason("session", replacement, "session replay")
	if got := registry.Lookup("session"); got != replacement {
		t.Fatalf("Lookup after replace returned %p, want %p", got, replacement)
	}
	registry.Remove("session")
	registry.Remove("session")
	if got := registry.Lookup("session"); got != nil {
		t.Fatalf("Lookup after idempotent remove returned %p, want nil", got)
	}
}

func TestSessionInboxRegistryRegisterLookupRemoveAndConcurrentSwitch(t *testing.T) {
	registry := &SessionInboxRegistry{inboxes: make(map[string]*ClaimsInbox)}
	first := &ClaimsInbox{}
	registry.Register("session", "agent", first)
	if got := registry.Lookup("session", "agent"); got != first {
		t.Fatalf("Lookup returned %p, want %p", got, first)
	}
	second := &ClaimsInbox{}
	registry.Register("session", "agent", second)
	if got := registry.Lookup("session", "agent"); got != second {
		t.Fatalf("Lookup after replacement returned %p, want %p", got, second)
	}
	var wg sync.WaitGroup
	for range make([]struct{}, sessionRegistryConcurrentSwitchAttempts) {
		wg.Add(2)
		go func() {
			defer wg.Done()
			registry.Register("session", "agent", &ClaimsInbox{})
		}()
		go func() {
			defer wg.Done()
			registry.Remove("session", "agent")
			_ = registry.Lookup("session", "agent")
		}()
	}
	wg.Wait()
	registry.Remove("session", "agent")
	registry.Remove("session", "agent")
	if got := registry.Lookup("session", "agent"); got != nil {
		t.Fatalf("Lookup after repeated remove returned %p, want nil", got)
	}
}
