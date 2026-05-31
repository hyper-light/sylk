package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
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

func TestContainerIdentityAgentRefResolverResolvesBootstrapAndInfrastructureParticipants(t *testing.T) {
	identity := bootstrapResolverTestIdentity(t)
	registry := container.NewAgentIdentityRegistryWithUID(identity.IdentityRegistryUID, []string{"architect", "guide"})
	resolver := containerIdentityAgentRefResolver{registry: registry, bootIdentity: identity}
	sessionID := "resolver-session"

	assertResolvedAgentRef(t, resolver, sessionID, "architect", "architect", claims.ParticipantCategoryAgent, map[string]string{"session_id": sessionID})
	assertResolvedAgentRef(t, resolver, sessionID, boot.SystemGuideAgentID, boot.SystemGuideAgentID, claims.ParticipantCategoryAgent, map[string]string{"session_id": sessionID})
	assertResolvedAgentRef(t, resolver, sessionID, identity.BootSequencerUID, identity.BootSequencerUID, claims.ParticipantCategorySystem, map[string]string{"process_uid": identity.ProcessUID, "session_id": sessionID})
	assertResolvedAgentRef(t, resolver, sessionID, boot.BootSequencerAgentID, identity.BootSequencerUID, claims.ParticipantCategorySystem, map[string]string{"process_uid": identity.ProcessUID, "session_id": sessionID})
	assertResolvedAgentRef(t, resolver, sessionID, identity.IdentityRegistryUID, identity.IdentityRegistryUID, claims.ParticipantCategorySystem, map[string]string{"process_uid": identity.ProcessUID, "session_id": sessionID})

	assertResolvedInfrastructureRef(t, resolver, sessionID, boot.ActivationControllerAgentID, claims.ParticipantCategoryService, map[string]string{"session_id": sessionID})
	assertResolvedInfrastructureRef(t, resolver, sessionID, boot.SystemSessionManagerAgentID, claims.ParticipantCategoryService, map[string]string{"process_uid": identity.ProcessUID})
	assertResolvedInfrastructureRef(t, resolver, sessionID, boot.SystemFabricSubscriberAgentID, claims.ParticipantCategoryService, map[string]string{"session_id": sessionID})
	assertResolvedInfrastructureRef(t, resolver, sessionID, boot.SystemBusAdministratorAgentID, claims.ParticipantCategorySystem, map[string]string{"process_uid": identity.ProcessUID})
	assertResolvedInfrastructureRef(t, resolver, sessionID, boot.SystemMemoryForestAgentID, claims.ParticipantCategoryService, map[string]string{"session_id": sessionID})
	assertResolvedInfrastructureRef(t, resolver, sessionID, boot.SystemUIBridgeID, claims.ParticipantCategoryService, map[string]string{"session_id": sessionID})
	assertResolvedInfrastructureRef(t, resolver, sessionID, boot.SystemCanonicalDeltaObserverID, claims.ParticipantCategoryService, map[string]string{"session_id": sessionID})

	if _, ok := resolver.ResolveAgentRef(context.Background(), sessionID, "unknown"); ok {
		t.Fatal("ResolveAgentRef unknown participant returned ok, want false")
	}
	if _, ok := resolver.ResolveAgentRef(context.Background(), "", boot.ActivationControllerAgentID); ok {
		t.Fatal("ResolveAgentRef session-scoped service without session returned ok, want false")
	}
}

func TestBootstrapOperationsPhase1And2CommitWithStrictTUIResolver(t *testing.T) {
	ctx := context.Background()
	identity := bootstrapResolverTestIdentity(t)
	resolver := containerIdentityAgentRefResolver{
		registry:     container.NewAgentIdentityRegistryWithUID(identity.IdentityRegistryUID, []string{"guide"}),
		bootIdentity: identity,
	}
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:          "bootstrap-strict-resolver",
		SessionID:        "bootstrap-session",
		TaskID:           "boot",
		AgentRefResolver: resolver,
	})
	seq, err := boot.NewOperationsSequencer(boot.OperationsConfig{Board: board, ProcessIdentity: identity})
	if err != nil {
		t.Fatalf("NewOperationsSequencer: %v", err)
	}
	phase1, err := seq.CommitPhase1(ctx, boot.Phase1Status{WALOpened: true, GuideBusOpened: true, WALReplayed: true})
	if err != nil {
		t.Fatalf("CommitPhase1 with strict resolver: %v", err)
	}
	assertBootstrapClaimLifecycle(t, board, phase1.ClaimID, claims.ClaimLifecycleSatisfied)
	assertBootstrapTestamentLifecycle(t, board, phase1.TestamentID, claims.TestamentLifecycleValidated)

	phase2, err := seq.CommitPhase2(ctx, boot.Phase2Status{Participants: boot.RequiredSystemParticipants()})
	if err != nil {
		t.Fatalf("CommitPhase2 with strict resolver: %v", err)
	}
	assertBootstrapClaimLifecycle(t, board, phase2.ClaimID, claims.ClaimLifecycleSatisfied)
	assertBootstrapTestamentLifecycle(t, board, phase2.TestamentID, claims.TestamentLifecycleValidated)
	for _, claimID := range phase2.ParticipantClaimIDs {
		assertBootstrapClaimLifecycle(t, board, claimID, claims.ClaimLifecycleSatisfied)
	}
	phase3, err := seq.CommitPhase3(ctx, boot.Phase3Status{Backends: boot.RequiredKnowledgeBackends()})
	if err != nil {
		t.Fatalf("CommitPhase3 with strict resolver: %v", err)
	}
	assertBootstrapClaimLifecycle(t, board, phase3.ClaimID, claims.ClaimLifecycleSatisfied)
	phase4, err := seq.CommitPhase4(ctx, boot.Phase4Status{Services: boot.RequiredInfrastructureServices()})
	if err != nil {
		t.Fatalf("CommitPhase4 with strict resolver: %v", err)
	}
	assertBootstrapClaimLifecycle(t, board, phase4.ClaimID, claims.ClaimLifecycleSatisfied)
	phase5, err := seq.CommitPhase5(ctx, boot.Phase5Status{Agents: boot.RequiredAlwaysHotAgents()})
	if err != nil {
		t.Fatalf("CommitPhase5 with strict resolver: %v", err)
	}
	assertBootstrapClaimLifecycle(t, board, phase5.ClaimID, claims.ClaimLifecycleSatisfied)
	phase6, err := seq.CommitPhase6(ctx, boot.Phase6Status{Surfaces: boot.RequiredUserFacingSurfaces()})
	if err != nil {
		t.Fatalf("CommitPhase6 with strict resolver: %v", err)
	}
	assertBootstrapClaimLifecycle(t, board, phase6.ClaimID, claims.ClaimLifecycleSatisfied)
	phase7, err := seq.CommitPhase7(ctx, boot.Phase7Status{})
	if err != nil {
		t.Fatalf("CommitPhase7 with strict resolver: %v", err)
	}
	assertBootstrapClaimLifecycle(t, board, phase7.ClaimID, claims.ClaimLifecycleSatisfied)
}

func bootstrapResolverTestIdentity(t *testing.T) boot.ProcessIdentity {
	t.Helper()
	identity, err := boot.ProcessIdentityFromUID("resolver-proc", time.Date(2026, time.May, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ProcessIdentityFromUID: %v", err)
	}
	return identity
}

func assertResolvedInfrastructureRef(t *testing.T, resolver containerIdentityAgentRefResolver, sessionID, participantID string, category claims.ParticipantCategory, scope map[string]string) {
	t.Helper()
	uid, err := claims.DeriveParticipantUID(category, participantID, scope)
	if err != nil {
		t.Fatalf("DeriveParticipantUID(%s): %v", participantID, err)
	}
	assertResolvedAgentRef(t, resolver, sessionID, participantID, uid, category, scope)
}

func assertResolvedAgentRef(t *testing.T, resolver containerIdentityAgentRefResolver, sessionID, agentID, uid string, category claims.ParticipantCategory, labels map[string]string) {
	t.Helper()
	ref, ok := resolver.ResolveAgentRef(context.Background(), sessionID, agentID)
	if !ok {
		t.Fatalf("ResolveAgentRef(%q) returned false", agentID)
	}
	if ref.UID != uid || ref.Category != string(category) {
		t.Fatalf("ResolveAgentRef(%q) = uid:%q category:%q, want uid:%q category:%q", agentID, ref.UID, ref.Category, uid, category)
	}
	for key, value := range labels {
		if ref.Labels[key] != value {
			t.Fatalf("ResolveAgentRef(%q) labels[%q] = %q, want %q (labels=%#v)", agentID, key, ref.Labels[key], value, ref.Labels)
		}
	}
	if err := ref.Validate(); err != nil {
		t.Fatalf("ResolveAgentRef(%q) produced invalid ref: %v", agentID, err)
	}
}

func assertBootstrapClaimLifecycle(t *testing.T, board *claims.ClaimsBoard, claimID string, status claims.ClaimLifecycleStatus) {
	t.Helper()
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("claim %q not found", claimID)
	}
	if claim.LifecycleStatus != status {
		t.Fatalf("claim %q lifecycle = %s, status = %s, validations = %d, want lifecycle %s", claimID, claim.LifecycleStatus, claim.Status, len(claim.Validations), status)
	}
}

func assertBootstrapTestamentLifecycle(t *testing.T, board *claims.ClaimsBoard, testamentID string, status claims.TestamentLifecycleStatus) {
	t.Helper()
	testament, ok := board.CloneTestament(testamentID)
	if !ok {
		t.Fatalf("testament %q not found", testamentID)
	}
	if testament.LifecycleStatus != status {
		t.Fatalf("testament %q lifecycle = %s, want %s", testamentID, testament.LifecycleStatus, status)
	}
}
