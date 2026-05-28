package claims

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceDispatcherSuccessPostsTestamentAndSatisfiesReceiptClaim(t *testing.T) {
	board, claimID := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		return ServiceClaimResult{Summary: "service done", Artifacts: []*Artifact{{Kind: ArtifactKindReadiness, Reference: "ready"}}}, nil
	})
	dispatcher := newTestServiceDispatcher(t, board, participant, handler)
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDelta(board, claimID, participant)); err != nil {
		t.Fatalf("DispatchDelta: %v", err)
	}
	claim := assertServiceClaimLifecycle(t, board, claimID, ClaimLifecycleSatisfied)
	if claim.Status != ClaimStatusAccepted {
		t.Fatalf("claim status = %s, want accepted", claim.Status)
	}
	testaments := board.TestamentsByClaim(claimID)
	if len(testaments) != 1 {
		t.Fatalf("testaments by claim = %d, want 1", len(testaments))
	}
	if testaments[0].LifecycleStatus != TestamentLifecycleValidated {
		t.Fatalf("testament lifecycle = %s, want validated", testaments[0].LifecycleStatus)
	}
}

func TestServiceDispatcherHandlerErrorRecordsLifecycleFailure(t *testing.T) {
	board, claimID := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		return ServiceClaimResult{}, errors.New("handler unavailable")
	})
	dispatcher := newTestServiceDispatcher(t, board, participant, handler)
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDelta(board, claimID, participant)); err != nil {
		t.Fatalf("DispatchDelta: %v", err)
	}
	assertServiceClaimLifecycle(t, board, claimID, ClaimLifecycleValidationErrored)
	if len(board.TestamentsByClaim(claimID)) == 0 {
		t.Fatal("handler error produced no error testament")
	}
}

func TestServiceDispatcherDeduplicatesDeltaKey(t *testing.T) {
	board, claimID := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	count := 0
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		count++
		return ServiceClaimResult{Summary: "service done"}, nil
	})
	dispatcher := newTestServiceDispatcher(t, board, participant, handler)
	delta := serviceClaimPostedDelta(board, claimID, participant)
	if err := dispatcher.DispatchDelta(context.Background(), delta); err != nil {
		t.Fatalf("DispatchDelta first: %v", err)
	}
	if err := dispatcher.DispatchDelta(context.Background(), delta); err != nil {
		t.Fatalf("DispatchDelta second: %v", err)
	}
	if count != 1 {
		t.Fatalf("handler calls = %d, want 1", count)
	}
}

func TestServiceDispatcherRejectsInvalidConfigAndMisdirectedDelta(t *testing.T) {
	board, claimID := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		return ServiceClaimResult{}, nil
	})
	for name, cfg := range map[string]ServiceDispatcherConfig{
		"nil board":    {Scope: immediateScope{}, Participant: participant, Handler: handler},
		"nil scope":    {Board: board, Participant: participant, Handler: handler},
		"nil handler":  {Board: board, Scope: immediateScope{}, Participant: participant},
		"invalid part": {Board: board, Scope: immediateScope{}, Handler: handler},
	} {
		if _, err := NewServiceDispatcher(cfg); !errors.Is(err, ErrServiceDispatcherInvalid) && !errors.Is(err, ErrParticipantRegistrationInvalid) {
			t.Fatalf("%s constructor error = %v, want dispatcher/participant invalid", name, err)
		}
	}
	dispatcher := newTestServiceDispatcher(t, board, participant, handler)
	delta := serviceClaimPostedDelta(board, claimID, participant)
	delta.Delivery.To = []AgentRef{DegradedAgentRef("other_service", "test")}
	if err := dispatcher.DispatchDelta(context.Background(), delta); !errors.Is(err, ErrServiceDispatcherInvalid) {
		t.Fatalf("misdirected DispatchDelta error = %v, want invalid", err)
	}
}

func TestServiceDispatcherStartSubscribesAndCloseIsIdempotent(t *testing.T) {
	board, _ := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	subErr := errors.New("unsubscribe failed")
	sub := &recordingSubscription{topic: "not-yet", err: subErr}
	subscriber := &recordingSubscriber{sub: sub}
	dispatcher, err := NewServiceDispatcher(ServiceDispatcherConfig{
		Board:       board,
		Subscriber:  subscriber,
		Scope:       immediateScope{},
		Participant: participant,
		Handler: serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
			return ServiceClaimResult{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantTopic := CanonicalAgentRefTopic(board.SessionID(), participant.AgentRef(), DeltaActionClaimPosted)
	if got := subscriber.patterns(); len(got) != 1 || got[0] != wantTopic {
		t.Fatalf("subscribed patterns = %v, want [%s]", got, wantTopic)
	}
	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if got := subscriber.patterns(); len(got) != 1 {
		t.Fatalf("second Start subscribed %d times, want 1", len(got))
	}
	if err := dispatcher.Close(); !errors.Is(err, subErr) {
		t.Fatalf("Close error = %v, want joined unsubscribe error", err)
	}
	if err := dispatcher.Close(); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
	if sub.calls.Load() != 1 {
		t.Fatalf("unsubscribe calls = %d, want 1", sub.calls.Load())
	}
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDelta(board, "missing", participant)); !errors.Is(err, ErrServiceDispatcherInvalid) {
		t.Fatalf("DispatchDelta after Close error = %v, want invalid", err)
	}
}

func TestServiceDispatcherEmptyArtifactsBecomeReadinessEvidence(t *testing.T) {
	board, claimID := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		return ServiceClaimResult{Summary: "done"}, nil
	})
	dispatcher := newTestServiceDispatcher(t, board, participant, handler)
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDelta(board, claimID, participant)); err != nil {
		t.Fatalf("DispatchDelta: %v", err)
	}
	testaments := board.TestamentsByClaim(claimID)
	if len(testaments) != 1 || len(testaments[0].Artifacts) != 1 {
		t.Fatalf("testaments/artifacts = %+v, want one readiness artifact", testaments)
	}
	artifact := testaments[0].Artifacts[0]
	if artifact.Kind != ArtifactKindReadiness || artifact.ArtifactName != "service_readiness" {
		t.Fatalf("fallback artifact = %+v, want deterministic readiness artifact", artifact)
	}
}

func TestServiceDispatcherOverflowRecordsFailureWithoutLaunchingHandler(t *testing.T) {
	board, firstClaimID := serviceDispatchBoard(t)
	secondClaimID := postServiceDispatchClaim(t, board, "service-claim-overflow")
	participant, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "service-session"}, 8, 1, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var handlerCalls atomic.Int32
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		if handlerCalls.Add(1) == 1 {
			close(started)
		}
		<-release
		return ServiceClaimResult{Summary: "done"}, nil
	})
	scope := &asyncServiceScope{}
	dispatcher, err := NewServiceDispatcher(ServiceDispatcherConfig{Board: board, Scope: scope, Participant: participant, Handler: handler})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDelta(board, firstClaimID, participant)); err != nil {
		t.Fatalf("first DispatchDelta: %v", err)
	}
	<-started
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDelta(board, secondClaimID, participant)); err != nil {
		t.Fatalf("overflow DispatchDelta: %v", err)
	}
	close(release)
	scope.wait()
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls.Load())
	}
	assertServiceClaimLifecycle(t, board, secondClaimID, ClaimLifecycleValidationErrored)
}

func serviceDispatchBoard(t *testing.T) (*ClaimsBoard, string) {
	t.Helper()
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "service-board", SessionID: "service-session", TaskID: "service-task"})
	return board, postServiceDispatchClaim(t, board, "service-claim")
}

func postServiceDispatchClaim(t *testing.T, board *ClaimsBoard, key string) string {
	t.Helper()
	claim := Claim{
		Title:       "Run deterministic service",
		Description: "The service must process this claim.",
		ActionType:  ActionTypeTask,
		Relations: []Relation{
			{Related: "issuer", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "tool_runtime", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			ID:          "service-receipt",
			Type:        ValidationTypeReceipt,
			Required:    true,
			Description: "service testament received",
			QualityBar:  "receipt.received",
			Status:      ValidationStatusPending,
		}},
	}
	generated, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "issuer", Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: key})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "issuer", ClaimPostOptions{Reason: "posted"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return claimID
}

func serviceDispatchParticipant(t *testing.T) ParticipantRegistration {
	t.Helper()
	participant, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "service-session"}, 4, 1, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	return participant
}

func newTestServiceDispatcher(t *testing.T, board *ClaimsBoard, participant ParticipantRegistration, handler ServiceHandler) *ServiceDispatcher {
	t.Helper()
	dispatcher, err := NewServiceDispatcher(ServiceDispatcherConfig{Board: board, Scope: immediateScope{}, Participant: participant, Handler: handler})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	return dispatcher
}

func serviceClaimPostedDelta(board *ClaimsBoard, claimID string, participant ParticipantRegistration) CanonicalDelta {
	return NewCanonicalDelta(
		DeltaActionClaimPosted,
		board.SessionID(),
		board.BoardID(),
		board.HighWaterSequence(),
		time.Now(),
		DegradedAgentRef("issuer", "test"),
		[]DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: claimID}},
		&DeltaDelivery{To: []AgentRef{participant.AgentRef()}, Relationship: RelationshipSubject},
		map[string]any{"claim": map[string]any{"action": string(ActionTypeTask)}},
	)
}

func assertServiceClaimLifecycle(t *testing.T, board *ClaimsBoard, claimID string, want ClaimLifecycleStatus) *Claim {
	t.Helper()
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("claim %s not found", claimID)
	}
	if claim.LifecycleStatus != want {
		t.Fatalf("claim lifecycle = %s, want %s", claim.LifecycleStatus, want)
	}
	return claim
}

type serviceHandlerFunc func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error)

func (f serviceHandlerFunc) HandleServiceClaim(ctx context.Context, req ServiceClaimRequest) (ServiceClaimResult, error) {
	return f(ctx, req)
}

type immediateScope struct{}

func (immediateScope) Go(_ string, _ time.Duration, fn func(context.Context) error) error {
	return fn(context.Background())
}

type asyncServiceScope struct {
	wg sync.WaitGroup
}

func (s *asyncServiceScope) Go(_ string, _ time.Duration, fn func(context.Context) error) error {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = fn(context.Background())
	}()
	return nil
}

func (s *asyncServiceScope) wait() {
	s.wg.Wait()
}

type recordingSubscriber struct {
	mu           sync.Mutex
	sub          *recordingSubscription
	patternsSeen []string
	handlers     []DeltaHandler
}

func (s *recordingSubscriber) SubscribeDelta(pattern string, handler DeltaHandler) (DeltaSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patternsSeen = append(s.patternsSeen, pattern)
	s.handlers = append(s.handlers, handler)
	s.sub.topic = pattern
	return s.sub, nil
}

func (s *recordingSubscriber) patterns() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.patternsSeen...)
}

type recordingSubscription struct {
	topic string
	err   error
	calls atomic.Int32
}

func (s *recordingSubscription) Topic() string { return s.topic }

func (s *recordingSubscription) Unsubscribe() error {
	s.calls.Add(1)
	return s.err
}

func TestServiceDispatcherPanicFailureStackIsBounded(t *testing.T) {
	stack := boundedServiceFailureStack([]byte(strings.Repeat("x", serviceFailureStackLimitBytes+1)))
	if len(stack) <= serviceFailureStackLimitBytes || !strings.Contains(stack, "...truncated") {
		t.Fatalf("bounded stack length=%d suffix=%q, want truncated marker", len(stack), stack[len(stack)-12:])
	}
}
