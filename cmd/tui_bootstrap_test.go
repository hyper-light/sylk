package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/session"
)

type testBackgroundWaiter struct {
	ready chan struct{}
}

func (w *testBackgroundWaiter) Ready() <-chan struct{} { return w.ready }
func (w *testBackgroundWaiter) Progress() (indexed, total int64) {
	return 0, 0
}
func (w *testBackgroundWaiter) OnProgress(func(indexed, total int64)) {}

type bootstrapTestDeltaBus struct{}

func (bootstrapTestDeltaBus) PublishDelta(context.Context, string, claims.Delta) error {
	return nil
}

func (bootstrapTestDeltaBus) SubscribeDelta(pattern string, _ claims.DeltaHandler) (claims.DeltaSubscription, error) {
	return bootstrapTestSubscription{topic: pattern}, nil
}

type bootstrapTestSubscription struct {
	topic string
}

func (s bootstrapTestSubscription) Topic() string      { return s.topic }
func (s bootstrapTestSubscription) Unsubscribe() error { return nil }

func TestWaitForBootBleveReady_PromotesFull(t *testing.T) {
	store := knowledge.NewKnowledgeStore(nil, nil)
	waiter := &testBackgroundWaiter{ready: make(chan struct{})}
	store.PromotePartial(query.NewBleveSearcher(nil), waiter, nil)
	close(waiter.ready)

	if err := waitForBootBleveReady(context.Background(), store); err != nil {
		t.Fatalf("waitForBootBleveReady() error = %v", err)
	}

	if got := store.Level(); got != knowledge.ReadinessFull {
		t.Fatalf("knowledge level = %v, want %v", got, knowledge.ReadinessFull)
	}
}

func TestWaitForBootBleveReady_CanceledContextSkipsPromotion(t *testing.T) {
	store := knowledge.NewKnowledgeStore(nil, nil)
	waiter := &testBackgroundWaiter{ready: make(chan struct{})}
	store.PromotePartial(query.NewBleveSearcher(nil), waiter, nil)
	close(waiter.ready)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := waitForBootBleveReady(ctx, store); err != nil {
		t.Fatalf("waitForBootBleveReady() error = %v", err)
	}

	if got := store.Level(); got != knowledge.ReadinessPartial {
		t.Fatalf("knowledge level = %v, want %v", got, knowledge.ReadinessPartial)
	}
}

func TestValidateBootstrapClaimsWiringRejectsMissingRuntimeDependencies(t *testing.T) {
	s := session.NewSession(session.Config{ID: "bad-claims-wiring", Name: "bad"})
	s.SetClaimsBoard(claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "session-bad-claims-wiring",
		SessionID: s.ID(),
	}))
	if err := validateBootstrapClaimsWiring(s); err == nil {
		t.Fatal("validateBootstrapClaimsWiring returned nil, want missing runtime dependency error")
	}
}

func TestValidateBootstrapClaimsWiringAcceptsProductionDependencies(t *testing.T) {
	ctx := context.Background()
	scope := concurrency.NewGoroutineScope(ctx, "bootstrap-test", nil)
	t.Cleanup(func() { _ = scope.Shutdown(time.Millisecond, time.Millisecond) })
	resolver := claims.AgentRefResolverFunc(func(_ context.Context, _ string, agentID string) (claims.AgentRef, bool) {
		return claims.AgentRef{
			UID:        agentID,
			Type:       agentID,
			Category:   string(claims.ParticipantCategoryAgent),
			Generation: claims.InitialParticipantGeneration,
		}.Normalized(), true
	})
	s := session.NewSession(session.Config{ID: "good-claims-wiring", Name: "good"})
	s.SetClaimsBoard(claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:          "session-good-claims-wiring",
		SessionID:        s.ID(),
		Scope:            &concurrency.ScopeAdapter{Scope: scope},
		DeltaBus:         bootstrapTestDeltaBus{},
		AgentRefResolver: resolver,
	}))
	if err := validateBootstrapClaimsWiring(s); err != nil {
		t.Fatalf("validateBootstrapClaimsWiring: %v", err)
	}
}
