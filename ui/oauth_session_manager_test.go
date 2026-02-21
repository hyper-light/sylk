package ui

import "testing"

func TestOAuthSessionManager_BeginCancelsCurrentFlow(t *testing.T) {
	mgr := newOAuthSessionManager()

	googleFlow := mgr.Begin("google")
	cancelled := false
	if !mgr.Attach("google", googleFlow, func() { cancelled = true }) {
		t.Fatal("expected attach to succeed")
	}

	openAIFlow := mgr.Begin("openai")
	if !cancelled {
		t.Fatal("expected prior provider flow to be cancelled")
	}
	if openAIFlow == googleFlow {
		t.Fatalf("expected new flow id, got %d", openAIFlow)
	}
}

func TestOAuthSessionManager_CompleteRejectsStaleFlow(t *testing.T) {
	mgr := newOAuthSessionManager()

	flowID := mgr.Begin("openai")
	if mgr.Complete("openai", flowID+1) {
		t.Fatal("expected stale completion to be rejected")
	}
	if !mgr.Complete("openai", flowID) {
		t.Fatal("expected active flow completion to succeed")
	}
}

func TestOAuthSessionManager_AbortRemovesMatchingFlow(t *testing.T) {
	mgr := newOAuthSessionManager()

	flowID := mgr.Begin("google")
	if !mgr.Attach("google", flowID, nil) {
		t.Fatal("expected attach to succeed")
	}
	mgr.Abort("google", flowID, nil)
	if mgr.Complete("google", flowID) {
		t.Fatal("expected aborted flow to be removed")
	}
}
