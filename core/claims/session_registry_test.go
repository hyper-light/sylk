package claims

import (
	"context"
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

func TestSessionRegistriesEmitDurableEvidenceWhenRequested(t *testing.T) {
	registry := &SessionBoardRegistry{boards: make(map[string]*ClaimsBoard)}
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "session-evidence-board", SessionID: "session", TaskID: "task"})
	evidence := SessionRegistryEvidenceOptions{Board: board, Reason: "test session lifecycle", Metadata: map[string]any{"source": "test"}}
	if err := registry.RegisterWithEvidence(context.Background(), "session", board, evidence); err != nil {
		t.Fatalf("RegisterWithEvidence: %v", err)
	}
	if got := registry.Lookup("session"); got != board {
		t.Fatalf("Lookup returned %p, want %p", got, board)
	}
	replacement := NewClaimsBoard(ClaimsBoardConfig{BoardID: "session-evidence-replay", SessionID: "session", TaskID: "task"})
	if err := registry.ReplaceForReasonWithEvidence(context.Background(), "session", replacement, "session replay", SessionRegistryEvidenceOptions{Board: replacement}); err != nil {
		t.Fatalf("ReplaceForReasonWithEvidence: %v", err)
	}
	if err := registry.RemoveWithEvidence(context.Background(), "session", SessionRegistryEvidenceOptions{Board: replacement, Reason: "shutdown"}); err != nil {
		t.Fatalf("RemoveWithEvidence: %v", err)
	}
	assertSystemEvidenceSatisfied(t, board, "session_create")
	assertSystemEvidenceSatisfied(t, replacement, "session_recover")
	assertSystemEvidenceSatisfied(t, replacement, "session_close")
	if err := registry.RemoveWithEvidence(context.Background(), "session", SessionRegistryEvidenceOptions{}); err != nil {
		t.Fatalf("idempotent RemoveWithEvidence: %v", err)
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

func TestSessionInboxRegistryEvidenceAndDuplicateGuard(t *testing.T) {
	registry := &SessionInboxRegistry{inboxes: make(map[string]*ClaimsInbox)}
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "inbox-evidence-board", SessionID: "session", TaskID: "task"})
	evidence := SessionRegistryEvidenceOptions{Board: board, Reason: "attach inbox"}
	if err := registry.RegisterWithEvidence(context.Background(), "session", "agent", &ClaimsInbox{}, evidence); err != nil {
		t.Fatalf("RegisterWithEvidence: %v", err)
	}
	if err := registry.RegisterWithEvidence(context.Background(), "session", "agent", &ClaimsInbox{}, SessionRegistryEvidenceOptions{Board: board}); !errors.Is(err, ErrSessionInboxAlreadyRegistered) {
		t.Fatalf("duplicate RegisterWithEvidence error = %v, want ErrSessionInboxAlreadyRegistered", err)
	}
	if err := registry.ReplaceForReasonWithEvidence(context.Background(), "session", "agent", &ClaimsInbox{}, "credential refresh", SessionRegistryEvidenceOptions{Board: board}); err != nil {
		t.Fatalf("ReplaceForReasonWithEvidence: %v", err)
	}
	if err := registry.RemoveWithEvidence(context.Background(), "session", "agent", SessionRegistryEvidenceOptions{Board: board, Reason: "detach inbox"}); err != nil {
		t.Fatalf("RemoveWithEvidence: %v", err)
	}
	assertSystemEvidenceSatisfied(t, board, "fabric_attach")
	assertSystemEvidenceSatisfied(t, board, "fabric_recover")
	assertSystemEvidenceSatisfied(t, board, "fabric_detach")
}

func TestFabricAndBusEvidenceHelpersProduceTypedArtifacts(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "fabric-bus-evidence", SessionID: "session", TaskID: "task"})
	if _, err := RecordFabricSubscriptionEvidence(context.Background(), board, "test_delivery", "session", map[string]any{"topic": "claims.*"}); err != nil {
		t.Fatalf("RecordFabricSubscriptionEvidence: %v", err)
	}
	if _, err := RecordBusTransportEvidence(context.Background(), board, "overflow", "session", map[string]any{"dropped": 3}); err != nil {
		t.Fatalf("RecordBusTransportEvidence: %v", err)
	}
	if got := board.Projection().NotificationErrors; len(got) == 0 {
		t.Fatal("bus evidence did not surface notification telemetry")
	}
	assertSystemEvidenceSatisfied(t, board, "fabric_test_delivery")
	assertSystemEvidenceSatisfied(t, board, "bus_overflow")
}

func assertSystemEvidenceSatisfied(t *testing.T, board *ClaimsBoard, artifactName string) {
	t.Helper()
	found := false
	for _, testament := range board.Projection().Testaments {
		for _, artifact := range testament.Artifacts {
			if artifact == nil || artifact.ArtifactName != artifactName {
				continue
			}
			found = true
			assertSystemEvidenceArtifactData(t, artifactName, artifact)
		}
	}
	if !found {
		t.Fatalf("system evidence artifact %q not found", artifactName)
	}
	for _, claim := range board.Projection().Claims {
		if claim.LifecycleStatus != ClaimLifecycleSatisfied {
			t.Fatalf("claim %s lifecycle = %s, want satisfied", claim.ID, claim.LifecycleStatus)
		}
	}
}

func assertSystemEvidenceArtifactData(t *testing.T, artifactName string, artifact *Artifact) {
	t.Helper()
	switch artifact.DataType {
	case ArtifactDataTypeSessionLifecycle:
		if _, err := ArtifactData[SessionLifecycleArtifactData](artifact); err != nil {
			t.Fatalf("ArtifactData(%s): %v", artifactName, err)
		}
	case ArtifactDataTypeFabricSubscription:
		if _, err := ArtifactData[FabricSubscriptionArtifactData](artifact); err != nil {
			t.Fatalf("ArtifactData(%s): %v", artifactName, err)
		}
	case ArtifactDataTypeBusTransport:
		if _, err := ArtifactData[BusTransportArtifactData](artifact); err != nil {
			t.Fatalf("ArtifactData(%s): %v", artifactName, err)
		}
	default:
		t.Fatalf("artifact %s data type = %s, want system participant data type", artifactName, artifact.DataType)
	}
}
