package boot

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestInitializePhase0CreatesProcessScopedIdentityAndInMemoryBoard(t *testing.T) {
	startedAt := time.Date(2026, time.May, 28, 12, 34, 56, 789, time.UTC)
	result, err := InitializePhase0(Phase0Config{StartedAt: startedAt})
	if err != nil {
		t.Fatalf("InitializePhase0: %v", err)
	}
	wantProcessUID := strconv.FormatInt(startedAt.UnixNano(), 10)
	if result.Identity.ProcessUID != wantProcessUID {
		t.Fatalf("process uid = %q, want %q", result.Identity.ProcessUID, wantProcessUID)
	}
	if got, want := result.Identity.BootSequencerUID, "sys:boot_sequencer:proc/"+wantProcessUID; got != want {
		t.Fatalf("boot sequencer uid = %q, want %q", got, want)
	}
	if got, want := result.Identity.IdentityRegistryUID, "sys:identity_registry:proc/"+wantProcessUID; got != want {
		t.Fatalf("identity registry uid = %q, want %q", got, want)
	}
	if result.Board == nil {
		t.Fatal("phase 0 board is nil")
	}
	if result.Board.SessionDir() != "" {
		t.Fatalf("phase 0 board session dir = %q, want in-memory", result.Board.SessionDir())
	}
	assertProjectionCounts(t, result.Board, 0, 0)
}

func TestInitializePhase0RequiresStartEntropy(t *testing.T) {
	_, err := InitializePhase0(Phase0Config{})
	if !errors.Is(err, ErrBootProcessStartRequired) {
		t.Fatalf("InitializePhase0 error = %v, want ErrBootProcessStartRequired", err)
	}
}

func TestProcessIdentityRejectsRoutableSeparatorCharacters(t *testing.T) {
	_, err := ProcessIdentityFromUID("bad/uid", time.Time{})
	if !errors.Is(err, ErrBootProcessUIDInvalid) {
		t.Fatalf("ProcessIdentityFromUID error = %v, want ErrBootProcessUIDInvalid", err)
	}
}

func TestOperationsPhase1CommitsDurableLifecycleAndReplaysIdempotently(t *testing.T) {
	cfg := operationsDurableConfig(t)
	db, seq := openOperationsBoard(t, cfg)
	result, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board()))
	if err != nil {
		t.Fatalf("CommitPhase1: %v", err)
	}
	assertClaimSatisfied(t, db.Board(), result.ClaimID)
	assertTestamentValidated(t, db.Board(), result.TestamentID, "boot.phase_1_complete")
	assertBootClaimUsesProcessScopedSequencer(t, db.Board(), result.ClaimID, testProcessIdentity())
	assertProjectionCounts(t, db.Board(), 1, 1)
	if err := db.Close(); err != nil {
		t.Fatalf("close first durable board: %v", err)
	}

	reopened, seq := openOperationsBoard(t, cfg)
	t.Cleanup(func() { _ = reopened.Close() })
	again, err := seq.CommitPhase1(context.Background(), readyPhase1Status(reopened.Board()))
	if err != nil {
		t.Fatalf("CommitPhase1 replay: %v", err)
	}
	if again.ClaimID != result.ClaimID || again.TestamentID != result.TestamentID {
		t.Fatalf("idempotent replay result = (%s,%s), want (%s,%s)", again.ClaimID, again.TestamentID, result.ClaimID, result.TestamentID)
	}
	assertClaimSatisfied(t, reopened.Board(), again.ClaimID)
	assertProjectionCounts(t, reopened.Board(), 1, 1)
}

func TestOperationsPhase1ReadinessFailurePostsFailedTestament(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	result, err := seq.CommitPhase1(context.Background(), Phase1Status{GuideBusOpened: true, WALReplayed: true})
	if !errors.Is(err, ErrBootReadinessIncomplete) {
		t.Fatalf("CommitPhase1 error = %v, want ErrBootReadinessIncomplete", err)
	}
	assertClaimLifecycle(t, db.Board(), result.ClaimID, claims.ClaimLifecycleValidationFailed)
	assertTestamentLifecycle(t, db.Board(), result.TestamentID, claims.TestamentLifecycleValidationFailed, "boot.phase_1_failed")
	assertProjectionCounts(t, db.Board(), 1, 1)
}

func TestOperationsPhase1PostFailureDoesNotSatisfyDurableSubstrate(t *testing.T) {
	cfg := operationsDurableConfig(t)
	cfg.ClaimPostPolicy = claims.ClaimPostPolicyFunc(func(context.Context, claims.ClaimPostPolicyRequest) claims.ClaimPostPolicyDecision {
		return claims.ClaimPostPolicyDecision{Allowed: false, Reason: "denied by boot policy", FailureKind: claims.ArtifactKindPolicyDenied}
	})
	db, err := claims.OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seq, err := NewOperationsSequencer(OperationsConfig{Board: db.Board(), ProcessIdentity: testProcessIdentity()})
	if err != nil {
		t.Fatalf("NewOperationsSequencer: %v", err)
	}

	result, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board()))
	if err == nil || !strings.Contains(err.Error(), "denied by boot policy") {
		t.Fatalf("CommitPhase1 error = %v, want policy denial", err)
	}
	if result.ClaimID != "" || result.TestamentID != "" {
		t.Fatalf("CommitPhase1 result = %+v, want no satisfied phase artifacts", result)
	}
	projection := db.Board().Projection()
	if got := len(projection.Claims); got != 1 {
		t.Fatalf("projection claims = %d, want 1", got)
	}
	assertClaimLifecycle(t, db.Board(), projection.Claims[0].ID, claims.ClaimLifecyclePostFailed)
	if _, err := seq.CommitPhase2(context.Background(), Phase2Status{Participants: RequiredSystemParticipants()}); !errors.Is(err, ErrBootPhaseNotSatisfied) {
		t.Fatalf("CommitPhase2 after failed phase1 error = %v, want ErrBootPhaseNotSatisfied", err)
	}
}

func TestOperationsPhase2ActivatesRequiredSystemParticipantsAndCompletes(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	if _, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board())); err != nil {
		t.Fatalf("CommitPhase1: %v", err)
	}
	result, err := seq.CommitPhase2(context.Background(), Phase2Status{Participants: RequiredSystemParticipants()})
	if err != nil {
		t.Fatalf("CommitPhase2: %v", err)
	}
	if got, want := len(result.ParticipantClaimIDs), len(RequiredSystemParticipants()); got != want {
		t.Fatalf("participant claims = %d, want %d", got, want)
	}
	assertClaimSatisfied(t, db.Board(), result.ClaimID)
	assertTestamentValidated(t, db.Board(), result.TestamentID, "boot.phase_2_complete")
	for _, claimID := range result.ParticipantClaimIDs {
		assertActivationClaimSatisfied(t, db.Board(), claimID)
	}
	assertProjectionCounts(t, db.Board(), 5, 5)
}

func TestOperationsPhase2RequiresPhase1Satisfied(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	_, err := seq.CommitPhase2(context.Background(), Phase2Status{Participants: RequiredSystemParticipants()})
	if !errors.Is(err, ErrBootPhaseNotSatisfied) {
		t.Fatalf("CommitPhase2 error = %v, want ErrBootPhaseNotSatisfied", err)
	}
	assertProjectionCounts(t, db.Board(), 0, 0)
}

func TestOperationsPhase2ParticipantFailureFailsPhase(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	if _, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board())); err != nil {
		t.Fatalf("CommitPhase1: %v", err)
	}
	participants := RequiredSystemParticipants()
	participants[len(participants)-1].Ready = false
	result, err := seq.CommitPhase2(context.Background(), Phase2Status{Participants: participants})
	if !errors.Is(err, ErrBootReadinessIncomplete) {
		t.Fatalf("CommitPhase2 error = %v, want ErrBootReadinessIncomplete", err)
	}
	assertClaimLifecycle(t, db.Board(), result.ClaimID, claims.ClaimLifecycleValidationFailed)
	assertTestamentLifecycle(t, db.Board(), result.TestamentID, claims.TestamentLifecycleValidationFailed, "boot.phase_2_failed")
	assertClaimLifecycle(t, db.Board(), result.ParticipantClaimIDs[len(result.ParticipantClaimIDs)-1], claims.ClaimLifecycleValidationFailed)
	health := bootPhaseHealthByPhase(t, seq.BootHealth(), BootPhaseSystemParticipants)
	if health.FailureCount == 0 || len(health.Warnings) == 0 {
		t.Fatalf("phase 2 health = %+v, want failure count and warnings", health)
	}
	assertProjectionCounts(t, db.Board(), 5, 5)
}

func TestOperationsPhases3Through7CommitFullBootLifecycle(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	commitPhase1And2(t, db.Board(), seq)

	phase3, err := seq.CommitPhase3(context.Background(), Phase3Status{Backends: RequiredKnowledgeBackends()})
	if err != nil {
		t.Fatalf("CommitPhase3: %v", err)
	}
	phase4, err := seq.CommitPhase4(context.Background(), Phase4Status{Services: RequiredInfrastructureServices()})
	if err != nil {
		t.Fatalf("CommitPhase4: %v", err)
	}
	phase5, err := seq.CommitPhase5(context.Background(), Phase5Status{Agents: RequiredAlwaysHotAgents()})
	if err != nil {
		t.Fatalf("CommitPhase5: %v", err)
	}
	phase6, err := seq.CommitPhase6(context.Background(), Phase6Status{Surfaces: RequiredUserFacingSurfaces()})
	if err != nil {
		t.Fatalf("CommitPhase6: %v", err)
	}
	phase7, err := seq.CommitPhase7(context.Background(), Phase7Status{})
	if err != nil {
		t.Fatalf("CommitPhase7: %v", err)
	}

	for _, result := range []PhaseCommitResult{phase3, phase4, phase5, phase6, phase7} {
		assertClaimSatisfied(t, db.Board(), result.ClaimID)
		assertTestamentLifecycle(t, db.Board(), result.TestamentID, claims.TestamentLifecycleValidated, expectedBootSummary(result.Phase))
	}
	health := seq.BootHealth()
	if got, want := len(health.Phases), len(allBootPhases()); got != want {
		t.Fatalf("boot health phases = %d, want %d", got, want)
	}
	for _, phase := range health.Phases {
		if phase.Outcome == "missing" {
			t.Fatalf("phase %s missing from health", phase.Phase)
		}
	}
}

func TestOperationsPhase4RegistersDispatchersAndClosesRegistry(t *testing.T) {
	cfg := operationsDurableConfig(t)
	db, err := claims.OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	registry := claims.NewServiceDispatcherRegistry()
	seq, err := NewOperationsSequencer(OperationsConfig{Board: db.Board(), ProcessIdentity: testProcessIdentity(), ServiceDispatcherRegistry: registry})
	if err != nil {
		t.Fatalf("NewOperationsSequencer: %v", err)
	}
	commitPhase1And2(t, db.Board(), seq)
	if _, err := seq.CommitPhase3(context.Background(), Phase3Status{Backends: RequiredKnowledgeBackends()}); err != nil {
		t.Fatalf("CommitPhase3: %v", err)
	}
	phase4, err := seq.CommitPhase4(context.Background(), Phase4Status{Services: RequiredInfrastructureServices()})
	if err != nil {
		t.Fatalf("CommitPhase4: %v", err)
	}
	wantDispatchers := len(RequiredKnowledgeBackends()) + len(RequiredInfrastructureServices()) + 2
	if got := len(registry.SessionParticipantUIDs(db.Board().SessionID())); got != wantDispatchers {
		t.Fatalf("registered dispatchers = %d, want %d", got, wantDispatchers)
	}
	assertPhaseTestamentHasDispatcherReadiness(t, db.Board(), phase4.TestamentID, len(RequiredInfrastructureServices()))
	again, err := seq.CommitPhase4(context.Background(), Phase4Status{Services: RequiredInfrastructureServices()})
	if err != nil {
		t.Fatalf("CommitPhase4 replay: %v", err)
	}
	if again.ClaimID != phase4.ClaimID || again.TestamentID != phase4.TestamentID {
		t.Fatalf("phase4 replay = (%s,%s), want (%s,%s)", again.ClaimID, again.TestamentID, phase4.ClaimID, phase4.TestamentID)
	}
	if got := len(registry.SessionParticipantUIDs(db.Board().SessionID())); got != wantDispatchers {
		t.Fatalf("registered dispatchers after replay = %d, want %d", got, wantDispatchers)
	}
	if err := seq.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := registry.SessionParticipantUIDs(db.Board().SessionID()); len(got) != 0 {
		t.Fatalf("registered dispatchers after Close = %v, want empty", got)
	}
}

func TestOperationsServiceDispatcherRegistrationFailureFailsPhaseAndBlocksLater(t *testing.T) {
	db, err := claims.OpenDurableBoard(operationsDurableConfig(t))
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	badSpec := ServiceDispatcherBootRegistration{
		Phase:         BootPhaseInfrastructure,
		ParticipantID: SystemDAGProcessorID,
		Participant:   claims.ParticipantRegistration{},
	}
	seq, err := NewOperationsSequencer(OperationsConfig{Board: db.Board(), ProcessIdentity: testProcessIdentity(), ServiceDispatchers: []ServiceDispatcherBootRegistration{badSpec}})
	if err != nil {
		t.Fatalf("NewOperationsSequencer: %v", err)
	}
	commitPhase1And2(t, db.Board(), seq)
	if _, err := seq.CommitPhase3(context.Background(), Phase3Status{Backends: RequiredKnowledgeBackends()}); err != nil {
		t.Fatalf("CommitPhase3: %v", err)
	}
	result, err := seq.CommitPhase4(context.Background(), Phase4Status{Services: RequiredInfrastructureServices()})
	if !errors.Is(err, claims.ErrParticipantRegistrationInvalid) {
		t.Fatalf("CommitPhase4 error = %v, want participant registration invalid", err)
	}
	assertClaimLifecycle(t, db.Board(), result.ClaimID, claims.ClaimLifecycleValidationFailed)
	if _, err := seq.CommitPhase5(context.Background(), Phase5Status{Agents: RequiredAlwaysHotAgents()}); !errors.Is(err, ErrBootPhaseNotSatisfied) {
		t.Fatalf("CommitPhase5 after failed phase4 error = %v, want ErrBootPhaseNotSatisfied", err)
	}
}

func TestOperationsServiceDispatcherRegistrationUsesMockerySubscriber(t *testing.T) {
	cfg := operationsDurableConfig(t)
	db, err := claims.OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	participant := testBootServiceParticipant(t, db.Board(), SystemProviderGatewayID)
	sub := &claimsmocks.DeltaSubscription{}
	sub.On("Unsubscribe").Return(nil).Once()
	subscriber := &claimsmocks.DeltaSubscriber{}
	wantTopic := claims.CanonicalAgentRefTopic(db.Board().SessionID(), participant.AgentRef(), claims.DeltaActionClaimPosted)
	subscriber.On("SubscribeDelta", wantTopic, mock.Anything).Return(sub, nil).Once()
	handler := &claimsmocks.ServiceHandler{}
	scope := &claimsmocks.ScopeProvider{}
	spec := ServiceDispatcherBootRegistration{
		Phase:         BootPhaseInfrastructure,
		ParticipantID: SystemProviderGatewayID,
		Participant:   participant,
		Subscriber:    subscriber,
		Scope:         scope,
		Handler:       handler,
	}
	registry := claims.NewServiceDispatcherRegistry()
	seq, err := NewOperationsSequencer(OperationsConfig{Board: db.Board(), ProcessIdentity: testProcessIdentity(), ServiceDispatcherRegistry: registry, ServiceDispatchers: []ServiceDispatcherBootRegistration{spec}})
	if err != nil {
		t.Fatalf("NewOperationsSequencer: %v", err)
	}
	commitPhase1And2(t, db.Board(), seq)
	if _, err := seq.CommitPhase3(context.Background(), Phase3Status{Backends: RequiredKnowledgeBackends()}); err != nil {
		t.Fatalf("CommitPhase3: %v", err)
	}
	if _, err := seq.CommitPhase4(context.Background(), Phase4Status{Services: RequiredInfrastructureServices()}); err != nil {
		t.Fatalf("CommitPhase4: %v", err)
	}
	if _, ok := registry.Lookup(db.Board().SessionID(), participant.UID); !ok {
		t.Fatal("mockery-registered dispatcher not found")
	}
	if err := seq.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	subscriber.AssertExpectations(t)
	sub.AssertExpectations(t)
	handler.AssertNotCalled(t, "HandleServiceClaim", mock.Anything, mock.Anything)
	scope.AssertNotCalled(t, "Go", mock.Anything, mock.Anything, mock.Anything)
}

func TestOperationsPhase3RequiresPhase2Satisfied(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	if _, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board())); err != nil {
		t.Fatalf("CommitPhase1: %v", err)
	}
	_, err := seq.CommitPhase3(context.Background(), Phase3Status{Backends: RequiredKnowledgeBackends()})
	if !errors.Is(err, ErrBootPhaseNotSatisfied) {
		t.Fatalf("CommitPhase3 error = %v, want ErrBootPhaseNotSatisfied", err)
	}
}

func TestOperationsPhase3MissingBackendFailsAndBlocksPhase4(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	commitPhase1And2(t, db.Board(), seq)

	backends := RequiredKnowledgeBackends()
	result, err := seq.CommitPhase3(context.Background(), Phase3Status{Backends: backends[:len(backends)-1]})
	if !errors.Is(err, ErrBootReadinessIncomplete) {
		t.Fatalf("CommitPhase3 error = %v, want ErrBootReadinessIncomplete", err)
	}
	assertClaimLifecycle(t, db.Board(), result.ClaimID, claims.ClaimLifecycleValidationFailed)
	_, err = seq.CommitPhase4(context.Background(), Phase4Status{Services: RequiredInfrastructureServices()})
	if !errors.Is(err, ErrBootPhaseNotSatisfied) {
		t.Fatalf("CommitPhase4 error = %v, want ErrBootPhaseNotSatisfied", err)
	}
}

func TestOperationsPhase3ConcurrentRetriesRemainIdempotent(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	commitPhase1And2(t, db.Board(), seq)
	attempts := RequiredKnowledgeBackends()
	errs := make(chan error, len(attempts))
	ids := make(chan string, len(attempts))
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := seq.CommitPhase3(context.Background(), Phase3Status{Backends: RequiredKnowledgeBackends()})
			errs <- err
			ids <- result.ClaimID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("concurrent CommitPhase3 claim id = %s, want %s", id, first)
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("CommitPhase3 concurrent error: %v", err)
		}
	}
}

func TestOperationsPhase1ConcurrentCallsRemainIdempotent(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	attempts := RequiredSystemParticipants()
	errs := make(chan error, len(attempts))
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board()))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("CommitPhase1 concurrent error: %v", err)
		}
	}
	assertProjectionCounts(t, db.Board(), 1, 1)
}

func commitPhase1And2(t *testing.T, board *claims.ClaimsBoard, seq *OperationsSequencer) {
	t.Helper()
	if _, err := seq.CommitPhase1(context.Background(), readyPhase1Status(board)); err != nil {
		t.Fatalf("CommitPhase1: %v", err)
	}
	if _, err := seq.CommitPhase2(context.Background(), Phase2Status{Participants: RequiredSystemParticipants()}); err != nil {
		t.Fatalf("CommitPhase2: %v", err)
	}
}

func expectedBootSummary(phase BootOperationPhase) string {
	switch phase {
	case BootPhaseKnowledgeBackends:
		return "boot.phase_3_complete"
	case BootPhaseInfrastructure:
		return "boot.phase_4_complete"
	case BootPhaseAgentActivation:
		return "boot.phase_5_complete"
	case BootPhaseUserSurfaces:
		return "boot.phase_6_complete"
	case BootPhaseComplete:
		return "boot.satisfied"
	default:
		return ""
	}
}

func operationsDurableConfig(t *testing.T) claims.ClaimsBoardConfig {
	t.Helper()
	return claims.ClaimsBoardConfig{
		BoardID:       "operations-board",
		SessionID:     "operations-session",
		TaskID:        "boot",
		SessionDir:    t.TempDir(),
		DisableOutbox: true,
	}
}

func openOperationsBoard(t *testing.T, cfg claims.ClaimsBoardConfig) (*claims.DurableBoard, *OperationsSequencer) {
	t.Helper()
	db, err := claims.OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	seq, err := NewOperationsSequencer(OperationsConfig{Board: db.Board(), ProcessIdentity: testProcessIdentity()})
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewOperationsSequencer: %v", err)
	}
	return db, seq
}

func testBootServiceParticipant(t *testing.T, board *claims.ClaimsBoard, participantID string) claims.ParticipantRegistration {
	t.Helper()
	participant, err := claims.NewParticipantRegistration(
		claims.ParticipantCategoryService,
		participantID,
		sessionScopeKeys(board, testProcessIdentity()),
		bootServiceQueueFloor,
		bootServiceConcurrency,
		bootServiceTimeout,
		claims.HandlerDeterminismNondeterministic,
		[]claims.ActionType{claims.ActionTypeTask},
	)
	if err != nil {
		t.Fatalf("NewParticipantRegistration: %v", err)
	}
	return participant
}

func testProcessIdentity() ProcessIdentity {
	return ProcessIdentity{
		ProcessUID:          "testproc",
		BootSequencerUID:    "sys:boot_sequencer:proc/testproc",
		IdentityRegistryUID: "sys:identity_registry:proc/testproc",
	}
}

func readyPhase1Status(board *claims.ClaimsBoard) Phase1Status {
	return Phase1Status{
		WALOpened:      true,
		GuideBusOpened: true,
		WALReplayed:    true,
		WALPath:        board.SessionDir(),
		ReplaySequence: board.HighWaterSequence(),
	}
}

func assertProjectionCounts(t *testing.T, board *claims.ClaimsBoard, claimsCount, testamentCount int) {
	t.Helper()
	proj := board.Projection()
	if proj.TotalClaims != claimsCount || proj.TotalTestaments != testamentCount {
		t.Fatalf("projection counts = claims:%d testaments:%d, want claims:%d testaments:%d", proj.TotalClaims, proj.TotalTestaments, claimsCount, testamentCount)
	}
}

func bootPhaseHealthByPhase(t *testing.T, health BootHealth, phase BootOperationPhase) BootPhaseHealth {
	t.Helper()
	for _, candidate := range health.Phases {
		if candidate.Phase == phase {
			return candidate
		}
	}
	t.Fatalf("boot health missing phase %s: %+v", phase, health.Phases)
	return BootPhaseHealth{}
}

func assertActivationClaimSatisfied(t *testing.T, board *claims.ClaimsBoard, claimID string) {
	t.Helper()
	claim := assertClaimLifecycle(t, board, claimID, claims.ClaimLifecycleSatisfied)
	if claim.ActionType != claims.ActionTypeActivation {
		t.Fatalf("claim %s action type = %s, want activation", claimID, claim.ActionType)
	}
	if got := claims.SubjectAgentID(claim.Relations); got != ActivationControllerAgentID {
		t.Fatalf("claim %s subject = %s, want %s", claimID, got, ActivationControllerAgentID)
	}
}

func assertBootClaimUsesProcessScopedSequencer(t *testing.T, board *claims.ClaimsBoard, claimID string, identity ProcessIdentity) {
	t.Helper()
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("claim %s not found", claimID)
	}
	if got := claims.IssuerAgentID(claim.Relations); got != identity.BootSequencerUID {
		t.Fatalf("claim %s issuer = %s, want %s", claimID, got, identity.BootSequencerUID)
	}
	if got := claims.SubjectAgentID(claim.Relations); got != identity.BootSequencerUID {
		t.Fatalf("claim %s subject = %s, want %s", claimID, got, identity.BootSequencerUID)
	}
}

func assertClaimSatisfied(t *testing.T, board *claims.ClaimsBoard, claimID string) {
	t.Helper()
	claim := assertClaimLifecycle(t, board, claimID, claims.ClaimLifecycleSatisfied)
	if claim.Status != claims.ClaimStatusAccepted {
		t.Fatalf("claim %s status = %s, want accepted", claimID, claim.Status)
	}
	for _, validation := range claim.Validations {
		if validation.Status != claims.ValidationStatusPassed {
			t.Fatalf("claim %s validation %s = %s, want passed", claimID, validation.ID, validation.Status)
		}
	}
}

func assertClaimLifecycle(t *testing.T, board *claims.ClaimsBoard, claimID string, want claims.ClaimLifecycleStatus) *claims.Claim {
	t.Helper()
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("claim %s not found", claimID)
	}
	if claim.LifecycleStatus != want {
		t.Fatalf("claim %s lifecycle = %s, want %s", claimID, claim.LifecycleStatus, want)
	}
	return claim
}

func assertTestamentValidated(t *testing.T, board *claims.ClaimsBoard, testamentID, summary string) {
	t.Helper()
	assertTestamentLifecycle(t, board, testamentID, claims.TestamentLifecycleValidated, summary)
}

func assertTestamentLifecycle(t *testing.T, board *claims.ClaimsBoard, testamentID string, want claims.TestamentLifecycleStatus, summary string) *claims.Testament {
	t.Helper()
	testament, ok := board.CloneTestament(testamentID)
	if !ok {
		t.Fatalf("testament %s not found", testamentID)
	}
	if testament.LifecycleStatus != want {
		t.Fatalf("testament %s lifecycle = %s, want %s", testamentID, testament.LifecycleStatus, want)
	}
	if testament.Summary != summary {
		t.Fatalf("testament %s summary = %q, want %q", testamentID, testament.Summary, summary)
	}
	return testament
}

func assertPhaseTestamentHasDispatcherReadiness(t *testing.T, board *claims.ClaimsBoard, testamentID string, want int) {
	t.Helper()
	testament, ok := board.CloneTestament(testamentID)
	if !ok {
		t.Fatalf("testament %s not found", testamentID)
	}
	got := 0
	for _, artifact := range testament.Artifacts {
		if artifact == nil {
			continue
		}
		if ready, _ := artifact.Metadata["service_dispatcher"].(bool); ready {
			got++
		}
	}
	if got != want {
		t.Fatalf("service dispatcher readiness artifacts = %d, want %d", got, want)
	}
}
