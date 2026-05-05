package claims

import (
	"context"
	"sync"
	"testing"
)

// captureBus is a minimal DeltaBus that accumulates every published
// (topic, delta) pair and also dispatches to handlers subscribed via
// matching patterns. Wildcard matching is reduced to "*" matching a
// single segment anywhere in the pattern.
type captureBus struct {
	mu        sync.Mutex
	published []publishedDelta
	patterns  map[string][]DeltaHandler
}

type publishedDelta struct {
	topic string
	delta Delta
}

func newCaptureBus() *captureBus {
	return &captureBus{patterns: make(map[string][]DeltaHandler)}
}

func (b *captureBus) PublishDelta(_ context.Context, topic string, delta Delta) error {
	b.mu.Lock()
	b.published = append(b.published, publishedDelta{topic: topic, delta: delta})
	handlers := b.matchingHandlers(topic)
	b.mu.Unlock()
	for _, h := range handlers {
		h(delta)
	}
	return nil
}

func (b *captureBus) SubscribeDelta(pattern string, h DeltaHandler) (DeltaSubscription, error) {
	b.mu.Lock()
	b.patterns[pattern] = append(b.patterns[pattern], h)
	b.mu.Unlock()
	return &captureSub{bus: b, pattern: pattern}, nil
}

func (b *captureBus) matchingHandlers(topic string) []DeltaHandler {
	var out []DeltaHandler
	for pattern, handlers := range b.patterns {
		if topicMatchesPattern(topic, pattern) {
			out = append(out, handlers...)
		}
	}
	return out
}

func (b *captureBus) Published() []publishedDelta {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]publishedDelta, len(b.published))
	copy(out, b.published)
	return out
}

func (b *captureBus) filterPublishedByKind(kind string) []publishedDelta {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []publishedDelta
	for _, p := range b.published {
		if p.delta.DeltaKind() == kind {
			out = append(out, p)
		}
	}
	return out
}

type captureSub struct{ bus *captureBus; pattern string }

func (s *captureSub) Topic() string      { return s.pattern }
func (s *captureSub) Unsubscribe() error { return nil }

// topicMatchesPattern returns true when topic matches pattern with
// '*' wildcarding a single segment. Segments are '.'-separated.
func topicMatchesPattern(topic, pattern string) bool {
	topicParts := splitTopic(topic)
	patternParts := splitTopic(pattern)
	if len(topicParts) != len(patternParts) {
		return false
	}
	for i, p := range patternParts {
		if p == TopicWildcard {
			continue
		}
		if p != topicParts[i] {
			return false
		}
	}
	return true
}

func splitTopic(t string) []string {
	var out []string
	start := 0
	for i := 0; i < len(t); i++ {
		if t[i] == '.' {
			out = append(out, t[start:i])
			start = i + 1
		}
	}
	return append(out, t[start:])
}

// ────────────────────────────────────────────────────────────────────

func newBusBackedBoard(t *testing.T, bus DeltaBus) *ClaimsBoard {
	t.Helper()
	return NewClaimsBoard(ClaimsBoardConfig{
		SessionID:  "sess",
		PipelineID: "pipe",
		TaskID:     "task",
		DeltaBus:   bus,
		Scope:      &testScope{},
	})
}

func TestIntegration_PostActionEmitsInboxDelta(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)
	err := board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "Feature X",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	inbox := bus.filterPublishedByKind(DeltaKindInbox)
	if len(inbox) != 1 {
		t.Fatalf("expected 1 inbox delta, got %d", len(inbox))
	}
	d := inbox[0].delta.(InboxDelta)
	if d.AgentID != "eng" {
		t.Errorf("expected AgentID=eng, got %q", d.AgentID)
	}
	if d.IssuerAgentID != "inspector" {
		t.Errorf("expected issuer=inspector, got %q", d.IssuerAgentID)
	}
	wantTopic := InboxTopic("sess", "eng", RelationshipSubject, ActionTypeTask)
	if inbox[0].topic != wantTopic {
		t.Errorf("topic: got %q want %q", inbox[0].topic, wantTopic)
	}
}

func TestIntegration_SubmitTestamentsEmitsTestamentDelta(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)
	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "Feature",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
		}},
	)
	proj := board.Projection()
	claimID := proj.Claims[0].ID

	err := board.SubmitTestaments(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "eng"},
		[]Testament{{
			Summary: "Done",
			AgentID: "eng",
			Relations: []Relation{
				{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			},
			Artifacts: []*Artifact{
				{Kind: "code_reference", Reference: "foo.go:1-10"},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	testaments := bus.filterPublishedByKind(DeltaKindTestament)
	if len(testaments) != 1 {
		t.Fatalf("expected 1 testament delta, got %d", len(testaments))
	}
	d := testaments[0].delta.(TestamentDelta)
	if d.Verdict != TestamentVerdictWorkComplete {
		t.Errorf("expected work_complete, got %q", d.Verdict)
	}
	if d.IssuerAgentID != "inspector" {
		t.Errorf("issuer: %q", d.IssuerAgentID)
	}
}

func TestIntegration_EvaluateValidationEmitsValidationDelta(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)
	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "F",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
			Validations: []*Validation{{Description: "correct", Required: true}},
		}},
	)
	proj := board.Projection()
	claimID := proj.Claims[0].ID
	validationID := proj.Claims[0].Validations[0].ID

	err := board.EvaluateValidation(context.Background(), claimID, validationID, StatusChange{
		To: string(ValidationStatusPassed), AgentID: "inspector", Reason: "lgtm",
	})
	if err != nil {
		t.Fatal(err)
	}
	deltas := bus.filterPublishedByKind(DeltaKindValidation)
	if len(deltas) < 1 {
		t.Fatalf("expected at least 1 validation delta, got %d", len(deltas))
	}
	d := deltas[0].delta.(ValidationDelta)
	if !d.ClaimAutoAccepted {
		t.Error("expected auto-accept with single-validation claim")
	}
	if d.Verdict != string(ValidationStatusPassed) {
		t.Errorf("verdict: %q", d.Verdict)
	}
}

func TestIntegration_RejectClaimEmitsStatusDelta(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)
	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "F",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
		}},
	)
	claimID := board.Projection().Claims[0].ID
	err := board.RejectClaim(context.Background(), claimID,
		StatusChange{AgentID: "inspector", Reason: "incomplete"},
		nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	statuses := bus.filterPublishedByKind(DeltaKindClaimStatus)
	if len(statuses) != 1 {
		t.Fatalf("expected 1 claim_status delta, got %d", len(statuses))
	}
	d := statuses[0].delta.(ClaimStatusDelta)
	if d.ToStatus != ClaimStatusRejected {
		t.Errorf("to_status: %q", d.ToStatus)
	}
}

func TestIntegration_InboxReceivesDirectedInboxDelta(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)

	inbox, err := NewClaimsInbox(InboxConfig{
		AgentID: "eng", SessionID: "sess", Subscriber: bus, Board: board,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.Start(nil); err != nil {
		t.Fatal(err)
	}

	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "F",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
		}},
	)

	// The inbox pattern AgentInboxPattern("sess","eng") must match
	// the published topic, so at least one delta should buffer.
	if inbox.Len() == 0 {
		t.Errorf("expected inbox to receive directed delta")
	}
}

func TestIntegration_PhaseTransitionEmitsPhaseDelta(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)

	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "F",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
		}},
	)
	claimID := board.Projection().Claims[0].ID
	_ = board.SubmitTestaments(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "eng"},
		[]Testament{{
			Summary: "Done", AgentID: "eng",
			Relations: []Relation{
				{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			},
		}},
	)

	if err := board.TransitionToValidation(context.Background()); err != nil {
		t.Fatal(err)
	}
	phases := bus.filterPublishedByKind(DeltaKindPhase)
	if len(phases) == 0 {
		t.Fatal("expected phase delta")
	}
	d := phases[0].delta.(PhaseDelta)
	if d.ToPhase != BoardPhaseValidation {
		t.Errorf("phase: %q", d.ToPhase)
	}
}
