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

func (b *captureBus) filterPublishedByTopic(topic string) []publishedDelta {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []publishedDelta
	for _, p := range b.published {
		if p.topic == topic {
			out = append(out, p)
		}
	}
	return out
}

type captureSub struct {
	bus     *captureBus
	pattern string
}

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

func TestIntegration_PostActionEmitsClaimPostedDelta(t *testing.T) {
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
	topic := CanonicalAgentTypeTopic("sess", "eng", DeltaActionClaimPosted)
	published := bus.filterPublishedByTopic(topic)
	if len(published) != 1 {
		t.Fatalf("expected 1 claim.posted delta, got %d", len(published))
	}
	d := published[0].delta.(CanonicalDelta)
	if !d.DeliveredTo("eng") {
		t.Errorf("delivery missing eng: %+v", d.Delivery)
	}
	if d.Actor.RouteKey() != "inspector" {
		t.Errorf("expected issuer=inspector, got %q", d.Actor.RouteKey())
	}
	if published[0].topic != topic {
		t.Errorf("topic: got %q want %q", published[0].topic, topic)
	}
}

func TestIntegration_PostActionEmitsCanonicalClaimPosted(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)
	err := board.PostAction(context.Background(),
		Action{Type: ActionTypeConsultation, AgentID: "architect"},
		[]Claim{{
			Title:       "Inspect project shape",
			Description: "Find Python project files.",
			Relations: []Relation{
				{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
			ExpectedToolCalls: []ExpectedToolCall{{
				Tool:     "workspace_read",
				Required: true,
				Arguments: map[string]any{
					"op": "glob",
				},
			}},
			Validations: []*Validation{{Type: ValidationTypeReceipt, Required: true}},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	agentTopic := CanonicalAgentTypeTopic("sess", "librarian", DeltaActionClaimPosted)
	published := bus.filterPublishedByTopic(agentTopic)
	if len(published) != 1 {
		t.Fatalf("expected 1 canonical posted delta on %s, got %d", agentTopic, len(published))
	}
	d := published[0].delta.(CanonicalDelta)
	if d.Action != DeltaActionClaimPosted {
		t.Fatalf("action: %s", d.Action)
	}
	if d.RefID("claim", RelatedTypeClaim) == "" {
		t.Fatalf("claim ref missing: %+v", d.Refs)
	}
	if !d.DeliveredTo("librarian") {
		t.Fatalf("delivery missing librarian: %+v", d.Delivery)
	}
	boardTopic := CanonicalBoardTopic("sess", board.BoardID(), DeltaActionClaimPosted)
	if got := bus.filterPublishedByTopic(boardTopic); len(got) != 1 {
		t.Fatalf("expected board topic mirror on %s, got %d", boardTopic, len(got))
	}
	claimCtx := d.Context["claim"].(map[string]any)
	if claimCtx["action"] != string(ActionTypeConsultation) {
		t.Fatalf("claim action context = %#v", claimCtx["action"])
	}
	tools := claimCtx["expected_tool_calls"].([]map[string]any)
	if len(tools) != 1 || tools[0]["tool"] != "workspace_read" {
		t.Fatalf("expected tools missing: %#v", tools)
	}
}

func TestIntegration_PostActionRoutesCanonicalPostedByResolvedUID(t *testing.T) {
	bus := newCaptureBus()
	resolver := AgentRefResolverFunc(func(_ context.Context, sessionID, agentID string) (AgentRef, bool) {
		if sessionID == "sess" && agentID == "librarian" {
			return AgentRef{
				UID:        "uid-librarian",
				Namespace:  "sess",
				Pod:        "knowledge",
				Name:       "librarian",
				Type:       "librarian",
				Category:   "knowledge",
				Generation: 1,
				Model:      "claude",
			}, true
		}
		if sessionID == "sess" && agentID == "architect" {
			return AgentRef{
				UID:        "uid-architect",
				Namespace:  "sess",
				Pod:        "global",
				Name:       "architect",
				Type:       "architect",
				Category:   "pipeline",
				Generation: 1,
				Model:      "claude",
			}, true
		}
		return AgentRef{}, false
	})
	board := NewClaimsBoard(ClaimsBoardConfig{
		SessionID:        "sess",
		PipelineID:       "pipe",
		TaskID:           "task",
		DeltaBus:         bus,
		Scope:            &testScope{},
		AgentRefResolver: resolver,
	})
	err := board.PostAction(context.Background(),
		Action{Type: ActionTypeConsultation, AgentID: "architect"},
		[]Claim{{
			Title: "Inspect project shape",
			Relations: []Relation{
				{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	uidTopic := CanonicalAgentTopic("sess", "uid-librarian", DeltaActionClaimPosted)
	published := bus.filterPublishedByTopic(uidTopic)
	if len(published) != 1 {
		t.Fatalf("expected UID topic %s, got %d", uidTopic, len(published))
	}
	d := published[0].delta.(CanonicalDelta)
	if d.Actor.UID != "uid-architect" {
		t.Fatalf("actor not resolved: %+v", d.Actor)
	}
	if d.Delivery == nil || len(d.Delivery.To) != 1 || d.Delivery.To[0].UID != "uid-librarian" {
		t.Fatalf("delivery not resolved: %+v", d.Delivery)
	}
	if got := bus.filterPublishedByTopic(CanonicalAgentTypeTopic("sess", "librarian", DeltaActionClaimPosted)); len(got) != 0 {
		t.Fatalf("resolved route also emitted degraded type topic: %d", len(got))
	}
}

func TestCanonicalDeltaProjector_ReplaysClaimPostedWithStableKey(t *testing.T) {
	directBus := newCaptureBus()
	board := newBusBackedBoard(t, directBus)
	err := board.PostAction(context.Background(),
		Action{Type: ActionTypeConsultation, AgentID: "architect"},
		[]Claim{{
			Title: "Inspect project shape",
			Relations: []Relation{
				{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	claim := board.Projection().Claims[0]
	topic := CanonicalAgentTypeTopic("sess", "librarian", DeltaActionClaimPosted)
	direct := directBus.filterPublishedByTopic(topic)
	if len(direct) != 1 {
		t.Fatalf("direct canonical delta missing on %s", topic)
	}
	replayBus := newCaptureBus()
	record := ClaimsOutboxRecord{
		BoardID:      board.BoardID(),
		SessionID:    board.SessionID(),
		TaskID:       board.TaskID(),
		Sequence:     claim.Sequence,
		EntityType:   "claim",
		EntityID:     claim.ID,
		MutationKind: string(DeltaActionClaimPosted),
		CreatedAt:    claim.Created,
	}
	if err := NewCanonicalDeltaProjector(replayBus, nil).Project(context.Background(), &record, board); err != nil {
		t.Fatal(err)
	}
	replayed := replayBus.filterPublishedByTopic(topic)
	if len(replayed) != 1 {
		t.Fatalf("replayed canonical delta missing on %s", topic)
	}
	directDelta := direct[0].delta.(CanonicalDelta)
	replayedDelta := replayed[0].delta.(CanonicalDelta)
	if replayedDelta.DeltaKey() != directDelta.DeltaKey() {
		t.Fatalf("replayed key changed: %q != %q", replayedDelta.DeltaKey(), directDelta.DeltaKey())
	}
	if replayedDelta.DeltaID == directDelta.DeltaID {
		t.Fatalf("replay should have a distinct event id with the same idempotency key")
	}
}

func TestIntegration_SystemInternalActionDoesNotEmitCanonicalPosted(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)
	err := board.PostAction(context.Background(),
		Action{Type: ActionTypeConsultContinuation, AgentID: "architect"},
		[]Claim{{
			Title: "Internal continuation",
			Relations: []Relation{
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := bus.filterPublishedByKind(string(DeltaActionClaimPosted)); len(got) != 0 {
		t.Fatalf("system internal emitted canonical posted deltas: %d", len(got))
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

	testaments := bus.filterPublishedByTopic(CanonicalClaimTopic("sess", claimID, DeltaActionTestamentPosted))
	if len(testaments) != 1 {
		t.Fatalf("expected 1 testament posted delta, got %d", len(testaments))
	}
	d := testaments[0].delta.(CanonicalDelta)
	if d.Action != DeltaActionTestamentPosted {
		t.Errorf("expected testament.posted, got %q", d.Action)
	}
	if !d.DeliveredTo("inspector") {
		t.Errorf("issuer delivery missing: %+v", d.Delivery)
	}
}

func TestIntegration_SubmitTestamentsEmitsCanonicalTestamentValidationAndTransitions(t *testing.T) {
	bus := newCaptureBus()
	board := newBusBackedBoard(t, bus)
	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeConsultation, AgentID: "architect"},
		[]Claim{{
			Title: "Consult librarian",
			Relations: []Relation{
				{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
			ExpectedToolCalls: []ExpectedToolCall{{
				Tool:     "workspace_read",
				Required: true,
				Arguments: map[string]any{
					"op": "glob",
				},
			}},
			Validations: []*Validation{{Type: ValidationTypeReceipt, Required: true}},
		}},
	)
	claimID := board.Projection().Claims[0].ID
	err := board.SubmitTestaments(context.Background(),
		Action{Type: ActionTypeTestament, AgentID: "librarian"},
		[]Testament{{
			Summary: "No Python files exist.",
			AgentID: "librarian",
			Relations: []Relation{
				{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			},
			Artifacts: []*Artifact{{Kind: "response_text", Reference: "No Python files exist."}},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	testamentTopic := CanonicalClaimTopic("sess", claimID, DeltaActionTestamentPosted)
	if got := bus.filterPublishedByTopic(testamentTopic); len(got) != 1 {
		t.Fatalf("canonical testament on claim topic count = %d", len(got))
	} else {
		d := got[0].delta.(CanonicalDelta)
		claimCtx := d.Context["claim"].(map[string]any)
		tools := claimCtx["expected_tool_calls"].([]map[string]any)
		if len(tools) != 1 || tools[0]["tool"] != "workspace_read" {
			t.Fatalf("testament delta missing claim expected tools: %#v", claimCtx["expected_tool_calls"])
		}
	}
	validationID := board.Projection().Claims[0].Validations[0].ID
	if got := bus.filterPublishedByTopic(CanonicalValidationTopic("sess", validationID, DeltaActionValidationEvaluated)); len(got) != 1 {
		t.Fatalf("canonical validation topic count = %d", len(got))
	}
	if got := bus.filterPublishedByTopic(CanonicalClaimTopic("sess", claimID, DeltaActionValidationEvaluated)); len(got) != 1 {
		t.Fatalf("canonical claim validation topic count = %d", len(got))
	}
	transitions := bus.filterPublishedByKind(string(DeltaActionClaimSatisfied))
	if len(transitions) != 2 {
		t.Fatalf("expected satisfied transition on claim+board topics, got %d", len(transitions))
	}
	var accepted bool
	for _, transition := range transitions {
		if transition.delta.(CanonicalDelta).ClaimToStatus() == ClaimStatusAccepted {
			accepted = true
		}
	}
	if !accepted {
		t.Fatalf("accepted transition missing")
	}
}

func TestIntegration_EvaluateValidationEmitsCanonicalValidationDelta(t *testing.T) {
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
	if legacy := bus.filterPublishedByKind(DeltaKindValidation); len(legacy) != 0 {
		t.Fatalf("legacy validation deltas published: %d", len(legacy))
	}
	deltas := bus.filterPublishedByTopic(CanonicalValidationTopic("sess", validationID, DeltaActionValidationEvaluated))
	if len(deltas) != 1 {
		t.Fatalf("expected 1 canonical validation delta, got %d", len(deltas))
	}
	d := deltas[0].delta.(CanonicalDelta)
	validationCtx := d.Context["validation"].(map[string]any)
	if validationCtx["claim_auto_accepted"] != true {
		t.Error("expected auto-accept with single-validation claim")
	}
	if validationCtx["status"] != string(ValidationStatusPassed) {
		t.Errorf("status: %q", validationCtx["status"])
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
	statuses := bus.filterPublishedByKind(string(DeltaActionClaimValidationFailed))
	if len(statuses) != 2 {
		t.Fatalf("expected claim.validation_failed on claim+board topics, got %d", len(statuses))
	}
	d := statuses[0].delta.(CanonicalDelta)
	if d.ClaimToStatus() != ClaimStatusRejected {
		t.Errorf("to_status: %q", d.ClaimToStatus())
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
	if inbox.Len() != 1 {
		t.Errorf("canonical+legacy duplicate should dedup to one entry, got %d", inbox.Len())
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
