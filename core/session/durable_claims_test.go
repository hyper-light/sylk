package session_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/session"
)

type countingProjector struct {
	count atomic.Int64
}

func (p *countingProjector) Name() string { return "counting" }

func (p *countingProjector) Project(_ context.Context, _ *claims.ClaimsOutboxRecord, _ *claims.ClaimsBoard) error {
	p.count.Add(1)
	return nil
}

type sessionTestDeltaBus struct{}

func (sessionTestDeltaBus) PublishDelta(context.Context, string, claims.Delta) error {
	return nil
}

func (sessionTestDeltaBus) SubscribeDelta(pattern string, _ claims.DeltaHandler) (claims.DeltaSubscription, error) {
	return sessionTestSubscription{topic: pattern}, nil
}

type sessionTestSubscription struct {
	topic string
}

func (s sessionTestSubscription) Topic() string      { return s.topic }
func (s sessionTestSubscription) Unsubscribe() error { return nil }

func TestManager_CreateWiresDurableSessionBoard(t *testing.T) {
	root := t.TempDir()
	mgr := session.NewManager(session.DefaultManagerConfig())
	s, err := mgr.Create(context.Background(), session.Config{
		ID:                 "durable-session",
		Name:               "durable-session",
		PersistenceEnabled: true,
		PersistencePath:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	board := s.ClaimsBoard()
	if board == nil {
		t.Fatal("session claims board is nil")
	}
	if err := board.PostAction(context.Background(), claims.Action{AgentID: "guide", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:          "claim-1",
		AgentID:     "guide",
		Title:       "Persist root claim",
		Description: "Verify the session root board is WAL-backed.",
		Relations: []claims.Relation{{
			Related:      "guide",
			RelatedType:  claims.RelatedTypeAgent,
			Relationship: claims.RelationshipIssuer,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Close(s.ID()); err != nil {
		t.Fatal(err)
	}

	walPath := filepath.Join(root, "durable-session", "protocols", "claims_board", "session-durable-session", "wal", "events.wal.jsonl")
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("claims WAL is empty")
	}
}

func TestManager_CreateWiresConfiguredClaimsProjectors(t *testing.T) {
	projector := &countingProjector{}
	mgr := session.NewManager(session.ManagerConfig{
		ClaimsProjectors: []claims.ClaimsProjector{projector},
	})
	s, err := mgr.Create(context.Background(), session.Config{
		ID:                 "projected-session",
		Name:               "projected-session",
		PersistenceEnabled: true,
		PersistencePath:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close(s.ID())
	if err := s.ClaimsBoard().PostAction(context.Background(), claims.Action{AgentID: "guide", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:          "claim-1",
		AgentID:     "guide",
		Title:       "Project root claim",
		Description: "Verify configured projectors are attached.",
	}}); err != nil {
		t.Fatal(err)
	}
	owner := s.DurableClaimsBoard()
	if owner == nil {
		t.Fatal("durable board owner missing")
	}
	owner.DrainOutbox(context.Background(), 32)
	if got := projector.count.Load(); got == 0 {
		t.Fatal("configured projector was not invoked")
	}
}

func TestFeatureFlags_ProjectorsDisabled(t *testing.T) {
	root := t.TempDir()
	projector := &countingProjector{}
	rollout := claims.DefaultRolloutConfig()
	rollout.ClaimsOutbox = false
	mgr := session.NewManager(session.ManagerConfig{
		ClaimsProjectors: []claims.ClaimsProjector{projector},
		ClaimsRollout:    &rollout,
	})
	s, err := mgr.Create(context.Background(), session.Config{
		ID:                 "board-only-session",
		Name:               "board-only-session",
		PersistenceEnabled: true,
		PersistencePath:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimsBoard().PostAction(context.Background(), claims.Action{AgentID: "guide", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:          "claim-1",
		AgentID:     "guide",
		Title:       "Persist without projectors",
		Description: "Board writes must survive with projectors disabled.",
	}}); err != nil {
		t.Fatal(err)
	}
	owner := s.DurableClaimsBoard()
	if owner == nil {
		t.Fatal("durable board owner missing")
	}
	if got := owner.DrainOutbox(context.Background(), 32); got != 0 {
		t.Fatalf("drain outbox = %d, want 0 when outbox disabled", got)
	}
	if projector.count.Load() != 0 {
		t.Fatal("projector invoked despite disabled outbox rollout")
	}
	health := owner.ProjectionHealth()
	if health.FeatureFlags[claims.EnvClaimsOutbox] != "0" {
		t.Fatalf("health flags = %+v, want outbox disabled", health.FeatureFlags)
	}
	if err := mgr.Close(s.ID()); err != nil {
		t.Fatal(err)
	}
	if !claims.DurableBoardWALExists(filepath.Join(root, "board-only-session"), "session-board-only-session") {
		t.Fatal("claims WAL missing after disabling projectors")
	}
}

func TestManager_CreateWiresClaimsOperationsRuntimeDependencies(t *testing.T) {
	ctx := context.Background()
	scope := concurrency.NewGoroutineScope(ctx, "session-test", nil)
	t.Cleanup(func() { _ = scope.Shutdown(time.Millisecond, time.Millisecond) })
	resolver := claims.AgentRefResolverFunc(func(_ context.Context, sessionID, agentID string) (claims.AgentRef, bool) {
		return claims.AgentRef{
			UID:        agentID + "-uid",
			Type:       agentID,
			Name:       agentID,
			Category:   string(claims.ParticipantCategoryAgent),
			Generation: claims.InitialParticipantGeneration,
			Labels:     map[string]string{"session_id": sessionID},
		}.Normalized(), true
	})
	mgr := session.NewManager(session.ManagerConfig{
		Scope:            scope,
		DeltaBus:         sessionTestDeltaBus{},
		AgentRefResolver: resolver,
	})
	s, err := mgr.Create(ctx, session.Config{
		ID:                 "runtime-wired-session",
		Name:               "runtime-wired-session",
		PersistenceEnabled: true,
		PersistencePath:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close(s.ID()) })
	snap := s.ClaimsBoard().WiringSnapshot()
	if !snap.HasScope || !snap.HasDeltaBus || !snap.HasAgentRefResolver {
		t.Fatalf("claims board wiring = %+v, want scope, delta bus, and agent ref resolver", snap)
	}
	if snap.SessionID != s.ID() {
		t.Fatalf("snapshot session id = %q, want %q", snap.SessionID, s.ID())
	}
}
