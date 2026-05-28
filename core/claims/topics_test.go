package claims

import (
	"strings"
	"testing"
)

func TestInboxTopic(t *testing.T) {
	got := InboxTopic("sess-1", "eng-a3f2", RelationshipSubject, ActionTypeTask)
	want := "claims.sess-1.inbox.eng-a3f2." + RelationshipSubject + "." + string(ActionTypeTask)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestClaimStatusTopic(t *testing.T) {
	got := ClaimStatusTopic("s", "c1", ClaimStatusTestified)
	want := "claims.s.claim.c1." + string(ClaimStatusTestified)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestValidationTopic(t *testing.T) {
	got := ValidationTopic("s", "v1", ValidationStatusPassed)
	want := "claims.s.validation.v1." + string(ValidationStatusPassed)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestPhaseTopic(t *testing.T) {
	got := PhaseTopic("s", BoardPhaseValidation)
	want := "claims.s.phase." + string(BoardPhaseValidation)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestCanonicalTopics(t *testing.T) {
	if got, want := CanonicalAgentTopic("sess", "uid.1", DeltaActionClaimPosted), "claims.sess.agent.uid_1.claim_posted"; got != want {
		t.Fatalf("agent topic got %q want %q", got, want)
	}
	if got, want := CanonicalAgentTypeTopic("sess", "librarian", DeltaActionClaimPosted), "claims.sess.agent_type.librarian.claim_posted"; got != want {
		t.Fatalf("agent type topic got %q want %q", got, want)
	}
	if got, want := CanonicalClaimTopic("sess", "c1", DeltaActionTestamentPosted), "claims.sess.claim.c1.testament_posted"; got != want {
		t.Fatalf("claim topic got %q want %q", got, want)
	}
	if got, want := CanonicalValidationTopic("sess", "v1", DeltaActionValidationEvaluated), "claims.sess.validation.v1.validation_evaluated"; got != want {
		t.Fatalf("validation topic got %q want %q", got, want)
	}
	if got, want := CanonicalArtifactTopic("sess", "a1", DeltaActionArtifactAttached), "claims.sess.artifact.a1.artifact_attached"; got != want {
		t.Fatalf("artifact topic got %q want %q", got, want)
	}
	if got, want := CanonicalBoardTopic("sess", "b1", DeltaActionClaimProgressed), "claims.sess.board.b1.claim_progressed"; got != want {
		t.Fatalf("board topic got %q want %q", got, want)
	}
}

func TestCanonicalPatterns(t *testing.T) {
	if got, want := CanonicalAgentActionPattern("sess", "uid", DeltaActionClaimPosted), "claims.sess.agent.uid.claim_posted"; got != want {
		t.Fatalf("agent pattern got %q want %q", got, want)
	}
	if got, want := CanonicalClaimActionPattern("sess", "*", DeltaActionTestamentPosted), "claims.sess.claim.*.testament_posted"; got != want {
		t.Fatalf("claim pattern got %q want %q", got, want)
	}
	if got, want := CanonicalSessionPattern("sess"), "claims.sess.*.*.*"; got != want {
		t.Fatalf("session pattern got %q want %q", got, want)
	}
}

func TestCanonicalTopicHelpersCoverEveryLifecycleAction(t *testing.T) {
	for _, action := range KnownDeltaActions() {
		for name, topic := range map[string]string{
			"agent":      CanonicalAgentActionPattern("sess", "uid", action),
			"agent_type": CanonicalAgentTypeActionPattern("sess", "architect", action),
			"claim":      CanonicalClaimActionPattern("sess", "claim-1", action),
			"board":      CanonicalBoardActionPattern("sess", "board-1", action),
		} {
			parts := strings.Split(topic, ".")
			if len(parts) != 5 {
				t.Fatalf("%s topic for %q has %d segments: %q", name, action, len(parts), topic)
			}
			if parts[4] != normalizeTopicSegment(string(action)) {
				t.Fatalf("%s topic for %q final segment = %q", name, action, parts[4])
			}
		}
	}
}

func TestAgentInboxPattern(t *testing.T) {
	got := AgentInboxPattern("", "eng-a3f2")
	// Empty session should wildcard.
	want := "claims.*.inbox.eng-a3f2.*.*"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAgentInboxRelationshipPattern(t *testing.T) {
	got := AgentInboxRelationshipPattern("sess", "eng", "subject")
	want := "claims.sess.inbox.eng.subject.*"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestClaimStatusPattern(t *testing.T) {
	got := ClaimStatusPattern("sess", ClaimStatusTestified)
	want := "claims.sess.claim.*." + string(ClaimStatusTestified)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestValidationVerdictPattern(t *testing.T) {
	got := ValidationVerdictPattern("sess", ValidationStatusFailed)
	want := "claims.sess.validation.*." + string(ValidationStatusFailed)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNormalizeTopicSegment_CollapsesSeparators(t *testing.T) {
	got := normalizeTopicSegment("with spaces.and.dots")
	if strings.Contains(got, ".") {
		t.Errorf("expected dots replaced, got %q", got)
	}
	if strings.Contains(got, " ") {
		t.Errorf("expected spaces replaced, got %q", got)
	}
}

func TestNormalizeTopicSegment_Empty(t *testing.T) {
	if got := normalizeTopicSegment(""); got != "_" {
		t.Errorf("empty: got %q want _", got)
	}
	if got := normalizeTopicSegment("   "); got != "_" {
		t.Errorf("whitespace: got %q want _", got)
	}
}

func TestWildcardOrSegment(t *testing.T) {
	if wildcardOrSegment("") != TopicWildcard {
		t.Error("empty should wildcard")
	}
	if wildcardOrSegment("sess") != "sess" {
		t.Error("non-empty should pass through")
	}
}
