package claims_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

const validatorTestByteLimit int64 = 1 << 20

func TestRecoverServiceWorkRedispatchesDeterministicClaimWithMockery(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "recovery-board", SessionID: "recovery-session", TaskID: "task"})
	participant := recoveryParticipant(t, claims.HandlerDeterminismPure)
	claimID := postMockeryInfrastructureClaim(t, board, participant.RouteKey, claims.ProviderGatewayToolComplete, map[string]any{"model": "mock"})

	artifact := &claims.Artifact{ArtifactName: "recovered", Kind: claims.ArtifactKindReadiness, Reference: "recovered"}
	if err := claims.SetArtifactData(artifact, claims.PresentationEvidenceArtifactData{Kind: "recovery", Reference: "recovered"}); err != nil {
		t.Fatalf("SetArtifactData: %v", err)
	}
	handler := &claimsmocks.ServiceHandler{}
	handler.On("HandleServiceClaim", mock.Anything, mock.MatchedBy(func(req claims.ServiceClaimRequest) bool {
		return req.Claim != nil && req.Claim.ID == claimID && req.Participant.UID == participant.UID
	})).Return(claims.ServiceClaimResult{Summary: "recovered", Artifacts: []*claims.Artifact{artifact}}, nil).Once()

	scope := &claimsmocks.ScopeProvider{}
	scope.On("Go", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(2).(func(context.Context) error)
		if err := fn(context.Background()); err != nil {
			t.Errorf("recovery dispatch: %v", err)
		}
	}).Return(nil).Once()

	dispatcher, err := claims.NewServiceDispatcher(claims.ServiceDispatcherConfig{Board: board, Scope: scope, Participant: participant, Handler: handler})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	registry := claims.NewServiceDispatcherRegistry()
	if err := registry.Register(board.SessionID(), dispatcher); err != nil {
		t.Fatalf("Register dispatcher: %v", err)
	}

	result, err := claims.RecoverServiceWork(context.Background(), claims.ServiceRecoveryOptions{
		Board:        board,
		Dispatchers:  registry,
		Participants: []claims.ParticipantRegistration{participant},
	})
	if err != nil {
		t.Fatalf("RecoverServiceWork: %v", err)
	}
	if result.Redispatched != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if got := board.TestamentsByClaim(claimID); len(got) != 1 {
		t.Fatalf("testaments = %d, want 1", len(got))
	}
	handler.AssertExpectations(t)
	scope.AssertExpectations(t)
}

func TestRecoverServiceWorkRedispatchesReadyValidatorWithMockery(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "validator-recovery-board", SessionID: "validator-recovery-session", TaskID: "task"})
	claimID, validationID := postTypedValidationClaim(t, board, claims.ValidationStatusReady)
	resultArtifact := typedReadinessArtifact(t, "validator-recovered", "validator recovered")
	handler := &claimsmocks.ValidatorHandler{}
	handler.On("ValidateArtifact", mock.Anything, mock.MatchedBy(func(req claims.ValidatorHandlerRequest) bool {
		return req.Validation.ID == validationID && req.Artifact.ArtifactName == "readiness"
	})).Return(claims.ValidatorHandlerResult{ResultArtifact: resultArtifact}, nil).Once()
	dispatcher := mockBoardValidatorDispatcher(t, board, handler, claims.HandlerDeterminismPure)

	result, err := claims.RecoverServiceWork(context.Background(), claims.ServiceRecoveryOptions{
		Board:               board,
		ValidatorDispatcher: dispatcher,
	})
	if err != nil {
		t.Fatalf("RecoverServiceWork: %v", err)
	}
	if result.ValidationRedispatched != 1 || result.ValidationFailed != 0 {
		t.Fatalf("validator recovery result = %+v", result)
	}
	validation, claim, ok := board.CloneValidation(validationID)
	if !ok || claim.ID != claimID || validation.Status != claims.ValidationStatusValidated {
		t.Fatalf("validation after recovery = claim:%+v validation:%+v ok:%v", claim, validation, ok)
	}
	handler.AssertExpectations(t)
}

func TestRecoverServiceWorkDoesNotReplayUnsafeNondeterministicClaim(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "recovery-board", SessionID: "recovery-session", TaskID: "task"})
	participant := recoveryParticipant(t, claims.HandlerDeterminismNondeterministic)
	if err := board.PostAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:         "unsafe-nondeterministic",
		Title:      "Unsafe nondeterministic work",
		ActionType: claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: participant.RouteKey, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		ExpectedToolCalls: []claims.ExpectedToolCall{{Tool: claims.ProviderGatewayToolComplete}},
		Validations:       []*claims.Validation{{ID: "receipt", Type: claims.ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}

	result, err := claims.RecoverServiceWork(context.Background(), claims.ServiceRecoveryOptions{
		Board:        board,
		Dispatchers:  claims.NewServiceDispatcherRegistry(),
		Participants: []claims.ParticipantRegistration{participant},
	})
	if err != nil {
		t.Fatalf("RecoverServiceWork: %v", err)
	}
	if result.Failed != 1 || result.Redispatched != 0 {
		t.Fatalf("result = %+v", result)
	}
	claim, ok := board.CloneClaim("unsafe-nondeterministic")
	if !ok || claim.LifecycleStatus != claims.ClaimLifecycleValidationErrored {
		t.Fatalf("claim after recovery = %+v", claim)
	}
}

func TestRecoverServiceWorkRedispatchesSideEffectClaimWithRecoveryIdempotencyArtifact(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "recovery-board", SessionID: "recovery-session", TaskID: "task"})
	participant := recoveryParticipant(t, claims.HandlerDeterminismSideEffect)
	claimID := postMockeryInfrastructureClaim(t, board, participant.RouteKey, claims.ProviderGatewayToolComplete, map[string]any{"model": "mock"})
	recordGeneratedRecoveryIdempotencyArtifact(t, board, claimID, participant, "service-recovery-key")

	artifact := typedReadinessArtifact(t, "side-effect-recovered", "safe side effect recovered")
	handler := &claimsmocks.ServiceHandler{}
	handler.On("HandleServiceClaim", mock.Anything, mock.MatchedBy(func(req claims.ServiceClaimRequest) bool {
		return req.Claim != nil && req.Claim.ID == claimID
	})).Return(claims.ServiceClaimResult{Summary: "recovered", Artifacts: []*claims.Artifact{artifact}}, nil).Once()
	scope := &claimsmocks.ScopeProvider{}
	scope.On("Go", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(2).(func(context.Context) error)
		if err := fn(context.Background()); err != nil {
			t.Errorf("recovery dispatch: %v", err)
		}
	}).Return(nil).Once()
	dispatcher, err := claims.NewServiceDispatcher(claims.ServiceDispatcherConfig{Board: board, Scope: scope, Participant: participant, Handler: handler})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	registry := claims.NewServiceDispatcherRegistry()
	if err := registry.Register(board.SessionID(), dispatcher); err != nil {
		t.Fatalf("Register dispatcher: %v", err)
	}

	result, err := claims.RecoverServiceWork(context.Background(), claims.ServiceRecoveryOptions{
		Board:        board,
		Dispatchers:  registry,
		Participants: []claims.ParticipantRegistration{participant},
	})
	if err != nil {
		t.Fatalf("RecoverServiceWork: %v", err)
	}
	if result.Redispatched != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	handler.AssertExpectations(t)
	scope.AssertExpectations(t)
}

func TestRecoverServiceWorkDoesNotReplayNondeterministicValidator(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "validator-recovery-board", SessionID: "validator-recovery-session", TaskID: "task"})
	_, validationID := postTypedValidationClaim(t, board, claims.ValidationStatusValidating)
	handler := &claimsmocks.ValidatorHandler{}
	dispatcher := mockBoardValidatorDispatcher(t, board, handler, claims.HandlerDeterminismNondeterministic)
	result, err := claims.RecoverServiceWork(context.Background(), claims.ServiceRecoveryOptions{
		Board:               board,
		ValidatorDispatcher: dispatcher,
	})
	if err != nil {
		t.Fatalf("RecoverServiceWork: %v", err)
	}
	if result.ValidationFailed != 1 || result.ValidationRedispatched != 0 {
		t.Fatalf("validator recovery result = %+v", result)
	}
	validation, _, ok := board.CloneValidation(validationID)
	if !ok || !validation.Status.IsNegativeTerminal() {
		t.Fatalf("validation after unsafe recovery = %+v ok:%v", validation, ok)
	}
	handler.AssertNotCalled(t, "ValidateArtifact", mock.Anything, mock.Anything)
}

func TestRecoverServiceWorkRedispatchesSideEffectValidatorWithRecoveryIdempotencyArtifact(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "validator-recovery-board", SessionID: "validator-recovery-session", TaskID: "task"})
	claimID, validationID := postRecoveryIdempotencyValidationClaim(t, board, claims.ValidationStatusValidating)
	handler := &claimsmocks.ValidatorHandler{}
	handler.On("ValidateArtifact", mock.Anything, mock.MatchedBy(func(req claims.ValidatorHandlerRequest) bool {
		return req.Validation.ID == validationID && req.Artifact.ArtifactName == claims.ArtifactKindRecoveryIdempotency
	})).Return(claims.ValidatorHandlerResult{ResultArtifact: typedReadinessArtifact(t, "validator-result", "safe validator replay")}, nil).Once()
	registry := claims.NewValidatorRegistry()
	_, err := registry.Register(claims.ValidatorRegistration{
		ValidatorID:        "mock.validator",
		ValidationType:     claims.ValidationTypeContract,
		Determinism:        claims.HandlerDeterminismSideEffect,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: claims.ArtifactKindRecoveryIdempotency,
		ArtifactDataType:   claims.ArtifactDataTypeRecoveryIdempotency,
		ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
		Handler:            handler,
	})
	if err != nil {
		t.Fatalf("Register validator: %v", err)
	}
	dispatcher, err := claims.NewBoardValidatorDispatcher(claims.BoardValidatorDispatcherConfig{
		Board:                board,
		Registry:             registry,
		MaxInputBytes:        validatorTestByteLimit,
		MaxOutputBytes:       validatorTestByteLimit,
		ApprovedValidatorIDs: map[string]bool{"mock.validator": true},
	})
	if err != nil {
		t.Fatalf("NewBoardValidatorDispatcher: %v", err)
	}

	result, err := claims.RecoverServiceWork(context.Background(), claims.ServiceRecoveryOptions{
		Board:               board,
		ValidatorDispatcher: dispatcher,
	})
	if err != nil {
		t.Fatalf("RecoverServiceWork: %v", err)
	}
	if result.ValidationRedispatched != 1 || result.ValidationFailed != 0 {
		t.Fatalf("validator recovery result = %+v", result)
	}
	validation, claim, ok := board.CloneValidation(validationID)
	if !ok || claim.ID != claimID || validation.Status != claims.ValidationStatusValidated {
		t.Fatalf("validation after recovery = claim:%+v validation:%+v ok:%v", claim, validation, ok)
	}
	handler.AssertExpectations(t)
}

func TestRecoverServiceWorkRecoversPendingContinuations(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "continuation-recovery-board", SessionID: "continuation-recovery-session", TaskID: "task"})
	result, err := claims.RecoverServiceWork(context.Background(), claims.ServiceRecoveryOptions{
		Board:         board,
		Continuations: []claims.PendingContinuationRecoverer{continuationRecoverer{count: 3}},
	})
	if err != nil {
		t.Fatalf("RecoverServiceWork: %v", err)
	}
	if result.ContinuationsRecovered != 3 || result.ContinuationRecovererOK != 1 {
		t.Fatalf("continuation recovery result = %+v", result)
	}
}

func TestRecoverServiceWorkUsesMockeryContinuationRecoverer(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "continuation-recovery-board", SessionID: "continuation-recovery-session", TaskID: "task"})
	recoverer := &claimsmocks.PendingContinuationRecoverer{}
	recoverer.On("RecoverPendingContinuations", mock.Anything).Return(2).Once()
	result, err := claims.RecoverServiceWork(context.Background(), claims.ServiceRecoveryOptions{
		Board:         board,
		Continuations: []claims.PendingContinuationRecoverer{recoverer},
	})
	if err != nil {
		t.Fatalf("RecoverServiceWork: %v", err)
	}
	if result.ContinuationsRecovered != 2 || result.ContinuationRecovererOK != 1 {
		t.Fatalf("continuation recovery result = %+v", result)
	}
	recoverer.AssertExpectations(t)
}

func TestCancelClaimTreeCancelsLeavesBeforeParents(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "cancel-board", SessionID: "cancel-session", TaskID: "task"})
	postCancellationClaim(t, board, "root", "")
	postCancellationClaim(t, board, "child", "root")
	result, err := claims.CancelClaimTree(context.Background(), claims.ClaimCancellationOptions{
		Board:        board,
		RootClaimIDs: []string{"root"},
		Reason:       "user interrupt",
	})
	if err != nil {
		t.Fatalf("CancelClaimTree: %v", err)
	}
	if len(result.CancelledClaimIDs) != 2 || result.CancelledClaimIDs[0] != "child" || result.CancelledClaimIDs[1] != "root" {
		t.Fatalf("cancel order = %+v", result.CancelledClaimIDs)
	}
	for _, claimID := range []string{"root", "child"} {
		claim, _ := board.CloneClaim(claimID)
		if !claim.LifecycleStatus.IsTerminal() {
			t.Fatalf("claim %s not terminal: %s", claimID, claim.LifecycleStatus)
		}
	}
}

func TestCancelClaimTreeCallsMockeryWorkCanceller(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "cancel-board", SessionID: "cancel-session", TaskID: "task"})
	postCancellationClaim(t, board, "root", "")
	canceller := &claimsmocks.ClaimWorkCanceller{}
	canceller.On("CancelClaimWork", mock.Anything, "root", "user interrupt").Return(true, nil).Once()
	result, err := claims.CancelClaimTree(context.Background(), claims.ClaimCancellationOptions{
		Board:          board,
		RootClaimIDs:   []string{"root"},
		Reason:         "user interrupt",
		WorkCancellers: []claims.ClaimWorkCanceller{canceller},
	})
	if err != nil {
		t.Fatalf("CancelClaimTree: %v", err)
	}
	if len(result.CancelledClaimIDs) != 1 || result.CancelledClaimIDs[0] != "root" {
		t.Fatalf("cancellation result = %+v", result)
	}
	canceller.AssertExpectations(t)
}

func TestCancelClaimTreeRecordsMissingRootEvidence(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "cancel-board", SessionID: "cancel-session", TaskID: "task"})
	result, err := claims.CancelClaimTree(context.Background(), claims.ClaimCancellationOptions{
		Board:         board,
		RootClaimIDs:  []string{"missing-root"},
		OriginClaimID: "origin",
		Reason:        "user interrupt",
	})
	if err != nil {
		t.Fatalf("CancelClaimTree: %v", err)
	}
	if len(result.MissingClaimIDs) != 1 || result.MissingClaimIDs[0] != "missing-root" {
		t.Fatalf("missing roots = %+v", result.MissingClaimIDs)
	}
	if !projectionHasMissingCancellationRoot(board.Projection(), "missing-root") {
		t.Fatalf("missing-root evidence not recorded: %+v", board.Projection().Testaments)
	}
}

func TestRunReplayAuditComparesDeterministicHandlerWithMockery(t *testing.T) {
	source := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "source-board", SessionID: "audit-session", TaskID: "task"})
	audit := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "audit-board", SessionID: "audit-session", TaskID: "task"})
	participant := recoveryParticipant(t, claims.HandlerDeterminismPure)
	claimID := postMockeryInfrastructureClaim(t, source, participant.RouteKey, claims.ProviderGatewayToolComplete, map[string]any{"model": "mock"})
	artifact := replayAuditArtifact(t)
	submitServiceTestamentForReplayAudit(t, source, claimID, participant.RouteKey, artifact)

	handler := &claimsmocks.ServiceHandler{}
	handler.On("HandleServiceClaim", mock.Anything, mock.Anything).Return(claims.ServiceClaimResult{Summary: "matched", Artifacts: []*claims.Artifact{replayAuditArtifact(t)}}, nil).Once()
	result, err := claims.RunReplayAudit(context.Background(), claims.ReplayAuditOptions{
		SourceBoard:  source,
		AuditBoard:   audit,
		Participants: []claims.ParticipantRegistration{participant},
		Handlers:     map[string]claims.ServiceHandler{participant.UID: handler},
	})
	if err != nil {
		t.Fatalf("RunReplayAudit: %v", err)
	}
	if result.Matched != 1 || result.Diverged != 0 || result.AuditClaimID == "" {
		t.Fatalf("audit result = %+v", result)
	}
	handler.AssertExpectations(t)
}

func TestRunReplayAuditComparesDeterministicValidatorWithMockery(t *testing.T) {
	source := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "validator-source-board", SessionID: "audit-session", TaskID: "task"})
	audit := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "validator-audit-board", SessionID: "audit-session", TaskID: "task"})
	claimID, validationID := postTypedValidationClaim(t, source, claims.ValidationStatusReady)
	target := validationTargetArtifact(t, source, claimID, validationID)
	storedResult := typedReadinessArtifact(t, "validator-result", "validator matched")
	if err := source.BeginValidation(context.Background(), claimID, validationID, "mock.validator", target.ID); err != nil {
		t.Fatalf("BeginValidation: %v", err)
	}
	if err := source.CompleteValidationLifecycle(context.Background(), claimID, validationID, "mock.validator", claims.ValidationStatusValidated, claims.ValidationLifecycleOptions{
		Reason:           "stored validator result",
		TargetArtifactID: target.ID,
		ResultArtifact:   storedResult,
	}); err != nil {
		t.Fatalf("CompleteValidationLifecycle: %v", err)
	}

	handler := &claimsmocks.ValidatorHandler{}
	handler.On("ValidateArtifact", mock.Anything, mock.MatchedBy(func(req claims.ValidatorHandlerRequest) bool {
		return req.Validation.ID == validationID && req.Artifact.ID == target.ID
	})).Return(claims.ValidatorHandlerResult{ResultArtifact: typedReadinessArtifact(t, "validator-result", "validator matched")}, nil).Once()
	registry := mockValidatorRegistry(t, handler, claims.HandlerDeterminismPure)
	result, err := claims.RunReplayAudit(context.Background(), claims.ReplayAuditOptions{
		SourceBoard:       source,
		AuditBoard:        audit,
		ValidatorRegistry: registry,
	})
	if err != nil {
		t.Fatalf("RunReplayAudit: %v", err)
	}
	if result.Matched != 1 || result.Diverged != 0 || result.AuditClaimID == "" {
		t.Fatalf("validator audit result = %+v", result)
	}
	handler.AssertExpectations(t)
}

func TestInfrastructureTelemetryIsBoundedAndIncludesRollout(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "telemetry-board", SessionID: "telemetry-session", TaskID: "task"})
	registry := claims.NewParticipantRegistry()
	for _, route := range []string{"a", "b"} {
		participant, err := claims.NewParticipantRegistration(claims.ParticipantCategoryService, "sys:"+route, map[string]string{"session": "telemetry"}, 2, 1, time.Second, claims.HandlerDeterminismPure, []claims.ActionType{claims.ActionTypeTask})
		if err != nil {
			t.Fatalf("participant %s: %v", route, err)
		}
		if _, err := registry.Register(participant); err != nil {
			t.Fatalf("register %s: %v", route, err)
		}
	}
	snapshot := claims.InfrastructureTelemetry(context.Background(), claims.InfrastructureTelemetryOptions{
		Board:               board,
		ParticipantRegistry: registry,
		ParticipantLimit:    1,
		ArtifactTypeLimit:   1,
	})
	if len(snapshot.Participants) != 1 || len(snapshot.ArtifactTypes) != 1 {
		t.Fatalf("snapshot not bounded: %+v", snapshot)
	}
	if snapshot.FeatureFlags[claims.EnvClaimsOutbox] == "" || snapshot.BoardID != board.BoardID() {
		t.Fatalf("snapshot missing rollout/board identity: %+v", snapshot)
	}
}

func TestSessionShutdownCoordinatorOrdersResourcesAndIsIdempotent(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "shutdown-board", SessionID: "shutdown-session", TaskID: "task"})
	postCancellationClaim(t, board, "active", "")
	recorder := &shutdownRecorder{}
	coordinator := claims.NewSessionShutdownCoordinator()
	result, err := coordinator.Shutdown(context.Background(), claims.SessionShutdownOptions{
		Board:              board,
		Inboxes:            []claims.ShutdownInbox{shutdownInbox{recorder: recorder}},
		Scopes:             []claims.ShutdownScope{shutdownScope{recorder: recorder}},
		Continuations:      []claims.ShutdownContinuationStore{shutdownContinuation{recorder: recorder}},
		ActiveRootClaimIDs: []string{"active"},
	})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if len(result.Cancelled) != 1 || result.Cancelled[0] != "active" {
		t.Fatalf("shutdown cancellation = %+v", result)
	}
	if got := recorder.String(); got != "inbox,signal,continuation,scope" {
		t.Fatalf("resource order = %s", got)
	}
	second, err := coordinator.Shutdown(context.Background(), claims.SessionShutdownOptions{Board: board})
	if err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if !second.AlreadyStopped {
		t.Fatalf("second shutdown should report AlreadyStopped: %+v", second)
	}
}

func TestSessionShutdownCoordinatorUsesMockeryResources(t *testing.T) {
	recorder := &shutdownRecorder{}
	inbox := &claimsmocks.ShutdownInbox{}
	inbox.On("Close").Run(func(mock.Arguments) { recorder.add("inbox") }).Return(nil).Once()
	scope := &claimsmocks.ShutdownScope{}
	scope.On("SignalShutdown").Run(func(mock.Arguments) { recorder.add("signal") }).Return().Once()
	scope.On("Shutdown", mock.Anything, mock.Anything).Run(func(mock.Arguments) { recorder.add("scope") }).Return(nil).Once()
	store := &claimsmocks.ShutdownContinuationStore{}
	store.On("Stop", "session shutdown").Run(func(mock.Arguments) { recorder.add("continuation") }).Return().Once()
	coordinator := claims.NewSessionShutdownCoordinator()
	result, err := coordinator.Shutdown(context.Background(), claims.SessionShutdownOptions{
		SessionID:     "shutdown-session",
		Inboxes:       []claims.ShutdownInbox{inbox},
		Scopes:        []claims.ShutdownScope{scope},
		Continuations: []claims.ShutdownContinuationStore{store},
	})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := recorder.String(); got != "inbox,signal,continuation,scope" {
		t.Fatalf("resource order = %s result=%+v", got, result)
	}
	inbox.AssertExpectations(t)
	scope.AssertExpectations(t)
	store.AssertExpectations(t)
}

func recoveryParticipant(t *testing.T, determinism claims.HandlerDeterminism) claims.ParticipantRegistration {
	t.Helper()
	participant, err := claims.NewParticipantRegistration(claims.ParticipantCategoryService, "sys:provider_gateway", map[string]string{"session": "recovery"}, 8, 1, time.Second, determinism, []claims.ActionType{claims.ActionTypeTask})
	if err != nil {
		t.Fatalf("NewParticipantRegistration: %v", err)
	}
	return participant
}

func postCancellationClaim(t *testing.T, board *claims.ClaimsBoard, claimID, parentID string) {
	t.Helper()
	relations := []claims.Relation{
		{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		{Related: "worker", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
	}
	if parentID != "" {
		relations = append(relations, claims.Relation{Related: parentID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipCausedBy})
	}
	if err := board.PostAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:          claimID,
		Title:       claimID,
		ActionType:  claims.ActionTypeTask,
		Relations:   relations,
		Validations: []*claims.Validation{{ID: claimID + "-receipt", Type: claims.ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}); err != nil {
		t.Fatalf("PostAction %s: %v", claimID, err)
	}
}

func replayAuditArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	artifact := &claims.Artifact{ArtifactName: "audit", Kind: claims.ArtifactKindReadiness, Reference: "stable", Metadata: map[string]any{"timestamp": time.Now().UTC().String()}}
	if err := claims.SetArtifactData(artifact, claims.PresentationEvidenceArtifactData{Kind: "audit", Reference: "stable"}); err != nil {
		t.Fatalf("SetArtifactData: %v", err)
	}
	return artifact
}

func submitServiceTestamentForReplayAudit(t *testing.T, board *claims.ClaimsBoard, claimID, agentID string, artifact *claims.Artifact) {
	t.Helper()
	generated, err := board.GenerateTestamentAction(context.Background(), claims.Action{AgentID: agentID, Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:   agentID,
		Summary:   "stable",
		Relations: []claims.Relation{{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim}},
		Artifacts: []*claims.Artifact{artifact},
	}}, claims.GenerateTestamentActionOptions{IdempotencyKey: "audit-" + claimID})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	testamentID := generated.Testaments[0].ID
	if err := board.PostGeneratedTestament(context.Background(), testamentID, agentID, claims.TestamentPostOptions{}); err != nil {
		t.Fatalf("PostGeneratedTestament: %v", err)
	}
}

func postTypedValidationClaim(t *testing.T, board *claims.ClaimsBoard, status claims.ValidationStatus) (string, string) {
	t.Helper()
	validationID := "mock-validator-" + string(status)
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeTask}, []claims.Claim{{
		Title:       "Validate typed artifact",
		Description: "Validator recovery should reconcile typed artifact evidence.",
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "sys:validator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{
			ID:                 validationID,
			Type:               claims.ValidationTypeContract,
			ValidatorID:        "mock.validator",
			TargetArtifactName: "readiness",
			ArtifactDataType:   claims.ArtifactDataTypePresentationEvidence,
			ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
			Required:           true,
			Description:        "typed artifact contract",
			Timeout:            time.Second,
		}},
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "typed-validation-" + string(status)})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "issuer", claims.ClaimPostOptions{Reason: "validator recovery test"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	artifact := typedReadinessArtifact(t, "readiness", "target ready")
	generatedTestament, err := board.GenerateTestamentAction(context.Background(), claims.Action{AgentID: "sys:validator", Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:   "sys:validator",
		Summary:   "target artifact",
		Relations: []claims.Relation{{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim}},
		Artifacts: []*claims.Artifact{artifact},
	}}, claims.GenerateTestamentActionOptions{IdempotencyKey: "typed-validation-testament-" + string(status)})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	targetArtifactID := generatedTestament.Testaments[0].Artifacts[0].ID
	if err := board.PostGeneratedTestament(context.Background(), generatedTestament.Testaments[0].ID, "sys:validator", claims.TestamentPostOptions{}); err != nil {
		t.Fatalf("PostGeneratedTestament: %v", err)
	}
	advanceValidationStatus(t, board, claimID, validationID, targetArtifactID, status)
	return claimID, validationID
}

func postRecoveryIdempotencyValidationClaim(t *testing.T, board *claims.ClaimsBoard, status claims.ValidationStatus) (string, string) {
	t.Helper()
	validationID := "mock-validator-recovery-idempotency-" + string(status)
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeTask}, []claims.Claim{{
		Title:       "Validate recovery idempotency artifact",
		Description: "Validator recovery may replay only when the target artifact proves idempotency.",
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "sys:validator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{
			ID:                 validationID,
			Type:               claims.ValidationTypeContract,
			ValidatorID:        "mock.validator",
			TargetArtifactName: claims.ArtifactKindRecoveryIdempotency,
			ArtifactDataType:   claims.ArtifactDataTypeRecoveryIdempotency,
			ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
			Required:           true,
			Description:        "recovery idempotency contract",
			Timeout:            time.Second,
		}},
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "recovery-idempotency-validation-" + string(status)})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "issuer", claims.ClaimPostOptions{Reason: "validator recovery idempotency test"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	participant := claims.ParticipantRegistration{UID: "mock.validator", RouteKey: "mock.validator"}
	artifact := recoveryIdempotencyArtifact(t, claimID, participant, "validator-recovery-key")
	generatedTestament, err := board.GenerateTestamentAction(context.Background(), claims.Action{AgentID: "mock.validator", Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:   "mock.validator",
		Summary:   "recovery idempotency target artifact",
		Relations: []claims.Relation{{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim}},
		Artifacts: []*claims.Artifact{artifact},
	}}, claims.GenerateTestamentActionOptions{IdempotencyKey: "recovery-idempotency-validation-testament-" + string(status)})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	targetArtifactID := generatedTestament.Testaments[0].Artifacts[0].ID
	if err := board.PostGeneratedTestament(context.Background(), generatedTestament.Testaments[0].ID, "mock.validator", claims.TestamentPostOptions{}); err != nil {
		t.Fatalf("PostGeneratedTestament: %v", err)
	}
	advanceValidationStatus(t, board, claimID, validationID, targetArtifactID, status)
	return claimID, validationID
}

func advanceValidationStatus(t *testing.T, board *claims.ClaimsBoard, claimID, validationID, artifactID string, status claims.ValidationStatus) {
	t.Helper()
	if status == claims.ValidationStatusReady {
		return
	}
	if err := board.BeginValidation(context.Background(), claimID, validationID, "mock.validator", artifactID); err != nil {
		t.Fatalf("BeginValidation: %v", err)
	}
	if status == claims.ValidationStatusValidatingQualityBar {
		if err := board.BeginValidationQualityBar(context.Background(), claimID, validationID, "mock.validator", artifactID); err != nil {
			t.Fatalf("BeginValidationQualityBar: %v", err)
		}
	}
}

func mockBoardValidatorDispatcher(t *testing.T, board *claims.ClaimsBoard, handler claims.ValidatorHandler, determinism claims.HandlerDeterminism) *claims.BoardValidatorDispatcher {
	t.Helper()
	registry := mockValidatorRegistry(t, handler, determinism)
	dispatcher, err := claims.NewBoardValidatorDispatcher(claims.BoardValidatorDispatcherConfig{
		Board:                board,
		Registry:             registry,
		MaxInputBytes:        validatorTestByteLimit,
		MaxOutputBytes:       validatorTestByteLimit,
		ApprovedValidatorIDs: map[string]bool{"mock.validator": true},
	})
	if err != nil {
		t.Fatalf("NewBoardValidatorDispatcher: %v", err)
	}
	return dispatcher
}

func mockValidatorRegistry(t *testing.T, handler claims.ValidatorHandler, determinism claims.HandlerDeterminism) *claims.ValidatorRegistry {
	t.Helper()
	registry := claims.NewValidatorRegistry()
	_, err := registry.Register(claims.ValidatorRegistration{
		ValidatorID:        "mock.validator",
		ValidationType:     claims.ValidationTypeContract,
		Determinism:        determinism,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "readiness",
		ArtifactDataType:   claims.ArtifactDataTypePresentationEvidence,
		ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
		Handler:            handler,
	})
	if err != nil {
		t.Fatalf("Register validator: %v", err)
	}
	return registry
}

func typedReadinessArtifact(t *testing.T, name, reference string) *claims.Artifact {
	t.Helper()
	artifact := &claims.Artifact{ArtifactName: name, Kind: claims.ArtifactKindReadiness, Reference: reference}
	if err := claims.SetArtifactData(artifact, claims.PresentationEvidenceArtifactData{Kind: claims.ArtifactKindReadiness, Reference: reference}); err != nil {
		t.Fatalf("SetArtifactData: %v", err)
	}
	return artifact
}

func recordGeneratedRecoveryIdempotencyArtifact(t *testing.T, board *claims.ClaimsBoard, claimID string, participant claims.ParticipantRegistration, key string) {
	t.Helper()
	artifact := recoveryIdempotencyArtifact(t, claimID, participant, key)
	if _, err := board.GenerateArtifact(context.Background(), *artifact, "sys:claims_recovery", claims.ArtifactLifecycleOptions{Reason: "recovery idempotency evidence generated"}); err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}
}

func recoveryIdempotencyArtifact(t *testing.T, claimID string, participant claims.ParticipantRegistration, key string) *claims.Artifact {
	t.Helper()
	artifact := &claims.Artifact{
		ArtifactName:  claims.ArtifactKindRecoveryIdempotency,
		Kind:          claims.ArtifactKindRecoveryIdempotency,
		AgentID:       "sys:claims_recovery",
		ParticipantID: participant.RouteKey,
		ClaimID:       claimID,
		Reference:     key,
		Relations:     []claims.Relation{{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipDerivedFrom}},
	}
	if err := claims.SetArtifactData(artifact, claims.RecoveryIdempotencyArtifactData{
		Key:            key,
		ParticipantID:  participant.UID,
		Scope:          claimID,
		Operation:      "recovery_redispatch",
		IssuedBy:       "sys:claims_recovery",
		IssuedAt:       time.Now().UTC(),
		SafetyEvidence: "idempotent operation key is durable",
	}); err != nil {
		t.Fatalf("SetArtifactData: %v", err)
	}
	return artifact
}

func validationTargetArtifact(t *testing.T, board *claims.ClaimsBoard, claimID, validationID string) *claims.Artifact {
	t.Helper()
	validation, _, ok := board.CloneValidation(validationID)
	if !ok {
		t.Fatalf("CloneValidation %s failed", validationID)
	}
	for _, testament := range board.TestamentsByClaim(claimID) {
		for _, artifact := range testament.Artifacts {
			if artifact.ArtifactName == validation.TargetArtifactName {
				return artifact
			}
		}
	}
	t.Fatalf("target artifact for %s not found", validationID)
	return nil
}

func projectionHasMissingCancellationRoot(proj *claims.ClaimsBoardProjection, claimID string) bool {
	if proj == nil {
		return false
	}
	for _, testament := range proj.Testaments {
		for _, artifact := range testament.Artifacts {
			if artifact == nil || artifact.Kind != claims.ArtifactKindErrorDiagnostic {
				continue
			}
			if value, _ := artifact.Metadata["missing_claim_id"].(string); value == claimID {
				return true
			}
		}
	}
	return false
}

type continuationRecoverer struct {
	count int
}

func (r continuationRecoverer) RecoverPendingContinuations(context.Context) int {
	return r.count
}

type shutdownRecorder struct {
	mu    sync.Mutex
	steps []string
}

func (r *shutdownRecorder) add(step string) {
	r.mu.Lock()
	r.steps = append(r.steps, step)
	r.mu.Unlock()
}

func (r *shutdownRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.steps, ",")
}

type shutdownInbox struct{ recorder *shutdownRecorder }

func (i shutdownInbox) Close() error {
	i.recorder.add("inbox")
	return nil
}

type shutdownScope struct{ recorder *shutdownRecorder }

func (s shutdownScope) SignalShutdown() {
	s.recorder.add("signal")
}

func (s shutdownScope) Shutdown(time.Duration, time.Duration) error {
	s.recorder.add("scope")
	return nil
}

type shutdownContinuation struct{ recorder *shutdownRecorder }

func (s shutdownContinuation) Stop(string) {
	s.recorder.add("continuation")
}
