package claims

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeltaInterfaceMethods(t *testing.T) {
	t.Run("inbox", func(t *testing.T) {
		d := InboxDelta{
			ClaimID:      "c1",
			AgentID:      "a1",
			Relationship: RelationshipSubject,
			Sequence:     5,
			SessionID:    "sess",
		}
		assertDelta(t, d, DeltaKindInbox, "inbox|c1|a1|"+RelationshipSubject, 5, "sess")
	})
	t.Run("testament", func(t *testing.T) {
		d := TestamentDelta{TestamentID: "t1", Sequence: 9, SessionID: "sess"}
		assertDelta(t, d, DeltaKindTestament, "testament|t1", 9, "sess")
	})
	t.Run("validation", func(t *testing.T) {
		d := ValidationDelta{ValidationID: "v1", Sequence: 3, SessionID: "sess"}
		assertDelta(t, d, DeltaKindValidation, "validation|v1", 3, "sess")
	})
	t.Run("claim_status", func(t *testing.T) {
		d := ClaimStatusDelta{ClaimID: "c1", ToStatus: ClaimStatusRejected, Sequence: 7, SessionID: "sess"}
		assertDelta(t, d, DeltaKindClaimStatus, "claim_status|c1|"+string(ClaimStatusRejected), 7, "sess")
	})
	t.Run("phase", func(t *testing.T) {
		d := PhaseDelta{BoardID: "b1", ToPhase: BoardPhaseValidation, Sequence: 11, SessionID: "sess"}
		assertDelta(t, d, DeltaKindPhase, "phase|b1|"+string(BoardPhaseValidation), 11, "sess")
	})
}

func assertDelta(t *testing.T, d Delta, wantKind, wantKey string, wantSeq uint64, wantSession string) {
	t.Helper()
	if d.DeltaKind() != wantKind {
		t.Errorf("kind: got %q want %q", d.DeltaKind(), wantKind)
	}
	if d.DeltaKey() != wantKey {
		t.Errorf("key: got %q want %q", d.DeltaKey(), wantKey)
	}
	if d.DeltaSequence() != wantSeq {
		t.Errorf("sequence: got %d want %d", d.DeltaSequence(), wantSeq)
	}
	if d.DeltaSessionID() != wantSession {
		t.Errorf("session: got %q want %q", d.DeltaSessionID(), wantSession)
	}
}

func TestMarshalUnmarshalDelta_RoundTrip(t *testing.T) {
	cases := []Delta{
		InboxDelta{
			SessionID: "s", BoardID: "b", ActionID: "a", ClaimID: "c",
			Sequence: 1, EmittedAt: time.Unix(100, 0).UTC(),
			Relationship: RelationshipSubject, AgentID: "eng",
			ActionKind: ActionTypeTask, ValidationCount: 2,
		},
		TestamentDelta{
			SessionID: "s", BoardID: "b", ClaimID: "c", TestamentID: "t",
			Sequence: 2, Verdict: TestamentVerdictWorkComplete,
			ArtifactCount: 1, ArtifactKinds: []string{"code_reference"},
		},
		ValidationDelta{
			SessionID: "s", BoardID: "b", ClaimID: "c", ValidationID: "v",
			Sequence: 3, Verdict: "passed", EvaluatorAgentID: "inspector",
			RemainingOnClaim: 0, ClaimAutoAccepted: true,
		},
		ClaimStatusDelta{
			SessionID: "s", BoardID: "b", ClaimID: "c", Sequence: 4,
			FromStatus: ClaimStatusPending, ToStatus: ClaimStatusInProgress,
			AgentID: "eng",
		},
		PhaseDelta{
			SessionID: "s", BoardID: "b", TaskID: "t", Sequence: 5,
			FromPhase: BoardPhaseImplementation, ToPhase: BoardPhaseValidation,
			Iteration: 0,
		},
	}
	for _, original := range cases {
		data, err := MarshalDelta(original)
		if err != nil {
			t.Fatalf("marshal %T: %v", original, err)
		}
		decoded, err := UnmarshalDelta(data)
		if err != nil {
			t.Fatalf("unmarshal %T: %v", original, err)
		}
		if decoded.DeltaKind() != original.DeltaKind() {
			t.Errorf("kind mismatch after round-trip: %q vs %q", decoded.DeltaKind(), original.DeltaKind())
		}
		if decoded.DeltaKey() != original.DeltaKey() {
			t.Errorf("key mismatch: %q vs %q", decoded.DeltaKey(), original.DeltaKey())
		}
		if decoded.DeltaSequence() != original.DeltaSequence() {
			t.Errorf("seq mismatch: %d vs %d", decoded.DeltaSequence(), original.DeltaSequence())
		}
	}
}

func TestMarshalDelta_NilRejected(t *testing.T) {
	if _, err := MarshalDelta(nil); err == nil {
		t.Fatal("expected error for nil delta")
	}
}

func TestUnmarshalDelta_UnknownKind(t *testing.T) {
	data, _ := json.Marshal(DeltaEnvelope{Kind: "invented", Payload: json.RawMessage(`{}`)})
	if _, err := UnmarshalDelta(data); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestDeriveTestamentVerdict(t *testing.T) {
	cases := []struct {
		name      string
		artifacts []*Artifact
		want      string
	}{
		{"empty", nil, TestamentVerdictWorkComplete},
		{"all_success", []*Artifact{{Kind: "code_reference"}, {Kind: "test_output"}}, TestamentVerdictWorkComplete},
		{"all_error", []*Artifact{{Kind: ArtifactKindError}, {Kind: ArtifactKindErrorTrace}}, TestamentVerdictError},
		{"partial", []*Artifact{{Kind: "code_reference"}, {Kind: ArtifactKindError}}, TestamentVerdictPartial},
		{"diagnostic_is_error", []*Artifact{{Kind: ArtifactKindErrorDiagnostic}}, TestamentVerdictError},
	}
	for _, tc := range cases {
		got := DeriveTestamentVerdict(tc.artifacts)
		if got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestCollectArtifactKinds(t *testing.T) {
	artifacts := []*Artifact{
		{Kind: "code_reference"},
		{Kind: "test_output"},
		{Kind: "code_reference"}, // duplicate — should not repeat
		nil,
		{Kind: ""}, // empty — should skip
	}
	kinds := CollectArtifactKinds(artifacts)
	if len(kinds) != 2 {
		t.Fatalf("expected 2 distinct kinds, got %v", kinds)
	}
	if kinds[0] != "code_reference" || kinds[1] != "test_output" {
		t.Errorf("unexpected order: %v", kinds)
	}
}
