package boot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestBootSequencerServiceHappyNegativeAndCapacityPaths(t *testing.T) {
	service := NewBootSequencerService(BootSequencerServiceConfig{ProcessUID: "proc-1", MaxRecords: len([]string{"setup"}), Clock: fixedBootServiceClock})
	result, err := service.HandleServiceClaim(context.Background(), claims.ServiceClaimRequest{Claim: bootServiceClaim(BootToolSetup, map[string]any{"phase": "setup"})})
	if err != nil {
		t.Fatalf("HandleServiceClaim setup: %v", err)
	}
	data := bootPhaseArtifactData(t, result.Artifacts[0])
	if data.Phase != "setup" || data.PhaseOrder != bootPhaseOrder("setup") || data.Status != claims.InfrastructureStatusOK || !data.Ready {
		t.Fatalf("setup data = %+v, want ready setup evidence", data)
	}
	if result.Artifacts[0].Kind != claims.ArtifactKindBootSetupComplete {
		t.Fatalf("setup artifact kind = %q, want %q", result.Artifacts[0].Kind, claims.ArtifactKindBootSetupComplete)
	}

	capacityResult, err := service.HandleServiceClaim(context.Background(), claims.ServiceClaimRequest{Claim: bootServiceClaim(BootToolDetect, map[string]any{"phase": "detect"})})
	if err != nil {
		t.Fatalf("HandleServiceClaim detect: %v", err)
	}
	capacityData := bootPhaseArtifactData(t, capacityResult.Artifacts[0])
	if capacityData.Status != claims.InfrastructureStatusFailed || capacityData.FailureReason == "" {
		t.Fatalf("capacity data = %+v, want bounded capacity failure", capacityData)
	}

	unknownResult, err := service.HandleServiceClaim(context.Background(), claims.ServiceClaimRequest{Claim: bootServiceClaim("unknown", nil)})
	if err != nil {
		t.Fatalf("HandleServiceClaim unknown: %v", err)
	}
	unknownData := bootPhaseArtifactData(t, unknownResult.Artifacts[0])
	if unknownData.Status != claims.InfrastructureStatusFailed || unknownData.FailureReason == "" {
		t.Fatalf("unknown tool data = %+v, want failure artifact", unknownData)
	}
}

func TestBootSequencerValidatorsPassFailAndRegister(t *testing.T) {
	artifact, err := claims.NewBootPhaseArtifact(claims.BootPhaseArtifactData{
		Phase:       "allocate",
		PhaseOrder:  bootPhaseOrder("allocate"),
		ProcessUID:  "proc-1",
		Duration:    time.Millisecond,
		Ready:       true,
		PriorPhases: []string{"setup", "detect"},
		Status:      claims.InfrastructureStatusOK,
	})
	if err != nil {
		t.Fatalf("NewBootPhaseArtifact: %v", err)
	}
	for _, validator := range []claims.ValidatorHandler{
		BootAllocateValidator{},
		BootPhaseOrderValidator{},
		BootDurationValidator{MaxDuration: time.Second},
	} {
		result, err := validator.ValidateArtifact(context.Background(), claims.ValidatorHandlerRequest{Artifact: artifact})
		if err != nil || result.Error != nil {
			t.Fatalf("%T result=%+v err=%v, want pass", validator, result, err)
		}
	}

	result, err := BootIngestValidator{}.ValidateArtifact(context.Background(), claims.ValidatorHandlerRequest{Artifact: artifact})
	if err != nil {
		t.Fatalf("BootIngestValidator: %v", err)
	}
	if result.Error == nil {
		t.Fatal("BootIngestValidator passed allocate evidence")
	}

	registry := claims.NewValidatorRegistry()
	if err := RegisterDefaultBootValidators(registry); err != nil {
		t.Fatalf("RegisterDefaultBootValidators: %v", err)
	}
	if err := RegisterDefaultBootValidators(registry); err != nil {
		t.Fatalf("RegisterDefaultBootValidators duplicate-compatible: %v", err)
	}
	if got, want := len(registry.List()), len(defaultBootValidatorRegistrations()); got != want {
		t.Fatalf("registered validators = %d, want %d", got, want)
	}
}

func TestPipelineBootRecorderUsesGeneratedLifecycleAndTypedEvidence(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "pipeline-boot", SessionID: "pipeline-session", TaskID: "boot"})
	claimID := postBootPhaseClaim(board, "setup", bootPhaseOrder("setup"))
	if claimID == "" {
		t.Fatal("postBootPhaseClaim returned empty claim id")
	}
	submitBootPhaseTestament(board, "setup", claimID, time.Millisecond, nil)
	claim := assertClaimLifecycle(t, board, claimID, claims.ClaimLifecycleSatisfied)
	if got, want := claims.SubjectAgentID(claim.Relations), bootClaimsAgentID(board); got != want {
		t.Fatalf("claim subject = %q, want process-scoped boot sequencer %q", got, want)
	}
	testaments := board.TestamentsByClaim(claimID)
	if len(testaments) != 1 {
		t.Fatalf("testaments = %d, want 1", len(testaments))
	}
	if testaments[0].AgentID != bootClaimsAgentID(board) {
		t.Fatalf("testament agent = %q, want %q", testaments[0].AgentID, bootClaimsAgentID(board))
	}
	if testaments[0].LifecycleStatus != claims.TestamentLifecycleValidated {
		t.Fatalf("testament lifecycle = %s, want validated", testaments[0].LifecycleStatus)
	}
	data := bootPhaseArtifactData(t, testaments[0].Artifacts[0])
	if data.Phase != "setup" || data.Status != claims.InfrastructureStatusOK || data.ProcessUID != board.SessionID() {
		t.Fatalf("boot data = %+v, want typed setup success", data)
	}
}

func TestBootSequencerServiceDispatchWithMockeryBusScopeAndRace(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "boot-dispatch", SessionID: "boot-session", TaskID: "boot"})
	participant, err := claims.NewParticipantRegistration(claims.ParticipantCategorySystem, BootSequencerAgentID, map[string]string{"process_uid": "proc-1"}, len(bootPhaseSequence()), 1, time.Second, claims.HandlerDeterminismSideEffect, []claims.ActionType{claims.ActionTypeBoot})
	if err != nil {
		t.Fatalf("NewParticipantRegistration: %v", err)
	}

	subscription := &claimsmocks.DeltaSubscription{}
	subscription.On("Unsubscribe").Return(nil).Once()
	subscriber := &claimsmocks.DeltaSubscriber{}
	var captured claims.DeltaHandler
	subscriber.On("SubscribeDelta", claims.CanonicalAgentRefTopic(board.SessionID(), participant.AgentRef(), claims.DeltaActionClaimPosted), mock.Anything).Run(func(args mock.Arguments) {
		captured = args.Get(1).(claims.DeltaHandler)
	}).Return(subscription, nil).Once()

	scope := &claimsmocks.ScopeProvider{}
	scope.On("Go", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(2).(func(context.Context) error)
		if err := fn(context.Background()); err != nil {
			t.Errorf("boot service dispatch: %v", err)
		}
	}).Return(nil).Once()

	dispatcher, err := claims.NewServiceDispatcher(claims.ServiceDispatcherConfig{
		Board:       board,
		Subscriber:  subscriber,
		Scope:       scope,
		Participant: participant,
		Handler:     NewBootSequencerService(BootSequencerServiceConfig{ProcessUID: "proc-1"}),
	})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if captured == nil {
		t.Fatal("mockery subscriber did not capture boot service handler")
	}
	claimID := postBootServiceClaim(t, board, BootSequencerAgentID, BootToolSetup, map[string]any{"phase": "setup", "process_uid": "proc-1"})
	captured(bootClaimPostedDelta(board, claimID, participant, claims.ActionTypeBoot))
	assertClaimLifecycle(t, board, claimID, claims.ClaimLifecycleSatisfied)
	artifact := bootServiceTestamentArtifact(t, board, claimID)
	if data := bootPhaseArtifactData(t, artifact); data.Phase != "setup" || data.Status != claims.InfrastructureStatusOK {
		t.Fatalf("boot data = %+v, want dispatch setup success", data)
	}

	if err := dispatcher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	subscriber.AssertExpectations(t)
	subscription.AssertExpectations(t)
	scope.AssertExpectations(t)
}

func TestBootSequencerServiceConcurrentRecordsDoNotRace(t *testing.T) {
	service := NewBootSequencerService(BootSequencerServiceConfig{ProcessUID: "proc-1", MaxRecords: len(bootPhaseSequence())})
	var wg sync.WaitGroup
	for _, phase := range bootPhaseSequence() {
		wg.Add(1)
		go func(phase string) {
			defer wg.Done()
			_, err := service.recordPhase(context.Background(), claims.BootPhaseArtifactData{
				Phase:       phase,
				PhaseOrder:  bootPhaseOrder(phase),
				ProcessUID:  "proc-1",
				Ready:       true,
				PriorPhases: pipelinePriorPhases(phase),
				Status:      claims.InfrastructureStatusOK,
			}, nil)
			if err != nil {
				t.Errorf("RecordPhase %s: %v", phase, err)
			}
		}(phase)
	}
	wg.Wait()
}

func bootServiceClaim(tool string, args map[string]any) *claims.Claim {
	return &claims.Claim{
		ID:         "claim-" + tool,
		ActionType: claims.ActionTypeBoot,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: BootSequencerAgentID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		ExpectedToolCalls: []claims.ExpectedToolCall{{ID: "call-" + tool, Tool: tool, Arguments: args}},
	}
}

func postBootServiceClaim(t *testing.T, board *claims.ClaimsBoard, subject, tool string, args map[string]any) string {
	t.Helper()
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeBoot}, []claims.Claim{{
		Title:       "Run boot service",
		Description: "Boot sequencer should record a phase.",
		ActionType:  claims.ActionTypeBoot,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		ExpectedToolCalls: []claims.ExpectedToolCall{{ID: "call-" + tool, Tool: tool, Arguments: args}},
		Validations:       []*claims.Validation{{ID: "receipt", Type: claims.ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "boot-dispatch-" + tool})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "issuer", claims.ClaimPostOptions{Reason: "boot dispatch"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return claimID
}

func bootClaimPostedDelta(board *claims.ClaimsBoard, claimID string, participant claims.ParticipantRegistration, action claims.ActionType) claims.CanonicalDelta {
	return claims.NewCanonicalDelta(
		claims.DeltaActionClaimPosted,
		board.SessionID(),
		board.BoardID(),
		board.HighWaterSequence(),
		time.Now(),
		claims.DegradedAgentRef("issuer", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}},
		&claims.DeltaDelivery{To: []claims.AgentRef{participant.AgentRef()}, Relationship: claims.RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": claimID, "action": string(action)}},
	)
}

func bootServiceTestamentArtifact(t *testing.T, board *claims.ClaimsBoard, claimID string) *claims.Artifact {
	t.Helper()
	for _, testament := range board.TestamentsByClaim(claimID) {
		for _, artifact := range testament.Artifacts {
			if artifact != nil && artifact.ArtifactName == "boot_phase" {
				return artifact
			}
		}
	}
	t.Fatalf("boot_phase artifact for claim %s not found", claimID)
	return nil
}

func bootPhaseArtifactData(t *testing.T, artifact *claims.Artifact) claims.BootPhaseArtifactData {
	t.Helper()
	data, err := claims.ArtifactData[claims.BootPhaseArtifactData](artifact)
	if err != nil {
		t.Fatalf("boot phase artifact data: %v", err)
	}
	return data
}

func fixedBootServiceClock() time.Time {
	return time.Unix(1, 0).UTC()
}
