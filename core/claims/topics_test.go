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
