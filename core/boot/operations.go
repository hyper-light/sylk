package boot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	BootSequencerAgentID          = "sys:boot_sequencer"
	IdentityRegistryAgentID       = "sys:identity_registry"
	ActivationControllerAgentID   = "sys:activation_controller"
	SystemBusAdministratorAgentID = "sys:bus_administrator"
	SystemSessionManagerAgentID   = "sys:session_manager"
	SystemFabricSubscriberAgentID = "sys:fabric_subscriber"

	BootPhaseDurableSubstrate   BootOperationPhase = "durable_substrate"
	BootPhaseSystemParticipants BootOperationPhase = "system_participants"

	bootPhase1Order = 1
	bootPhase2Order = 2

	bootOperationKeyPrefix = "boot.operations"
	bootValidationQuality  = "receipt.received"
	bootTaskID             = "boot"
	phase0BoardIDPrefix    = "boot.phase0"
	processScopeSegment    = "proc"

	artifactKindBootReadiness = "boot_readiness"
	artifactKindBootFailure   = "boot_failure"
)

var (
	ErrBootOperationsBoardRequired = errors.New("boot operations require a claims board")
	ErrBootPhaseNotSatisfied       = errors.New("required boot phase is not satisfied")
	ErrBootProcessStartRequired    = errors.New("boot process start time is required")
	ErrBootProcessUIDRequired      = errors.New("boot process uid is required")
	ErrBootProcessUIDInvalid       = errors.New("boot process uid contains invalid routing characters")
	ErrBootReadinessIncomplete     = errors.New("boot readiness is incomplete")
)

type BootOperationPhase string

type ProcessIdentity struct {
	ProcessUID          string
	BootSequencerUID    string
	IdentityRegistryUID string
	StartedAt           time.Time
}

type Phase0Config struct {
	StartedAt  time.Time
	ProcessUID string
	BoardID    string
	Scope      claims.ScopeProvider
}

type Phase0Result struct {
	Identity ProcessIdentity
	Board    *claims.ClaimsBoard
}

type OperationsConfig struct {
	Board           *claims.ClaimsBoard
	ProcessIdentity ProcessIdentity
	ProcessUID      string
}

type OperationsSequencer struct {
	board    *claims.ClaimsBoard
	identity ProcessIdentity
	mu       sync.Mutex
}

type Phase1Status struct {
	WALOpened      bool
	GuideBusOpened bool
	WALReplayed    bool
	WALPath        string
	ReplaySequence uint64
	Context        map[string]any
}

type SystemParticipantActivation struct {
	ParticipantID   string
	ParticipantType string
	Ready           bool
	Context         map[string]any
}

type Phase2Status struct {
	Participants []SystemParticipantActivation
	Context      map[string]any
}

type PhaseCommitResult struct {
	Phase                   BootOperationPhase
	ClaimID                 string
	TestamentID             string
	ParticipantClaimIDs     []string
	ParticipantTestamentIDs []string
}

type bootClaimSpec struct {
	actionType            claims.ActionType
	key                   string
	issuer                string
	subject               string
	title                 string
	description           string
	validationDescription string
	priority              int
}

type bootTestamentSpec struct {
	key         string
	agentID     string
	validatorID string
	summary     string
	duration    time.Duration
	artifacts   []*claims.Artifact
}

func NewOperationsSequencer(cfg OperationsConfig) (*OperationsSequencer, error) {
	if cfg.Board == nil {
		return nil, ErrBootOperationsBoardRequired
	}
	identity, err := operationsProcessIdentity(cfg)
	if err != nil {
		return nil, err
	}
	return &OperationsSequencer{board: cfg.Board, identity: identity}, nil
}

func InitializePhase0(cfg Phase0Config) (Phase0Result, error) {
	identity, err := phase0ProcessIdentity(cfg)
	if err != nil {
		return Phase0Result{}, err
	}
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   firstNonEmptyString(strings.TrimSpace(cfg.BoardID), phase0BoardID(identity.ProcessUID)),
		TaskID:    bootTaskID,
		Scope:     cfg.Scope,
		SessionID: "",
	})
	return Phase0Result{Identity: identity, Board: board}, nil
}

func NewProcessIdentity(startedAt time.Time) (ProcessIdentity, error) {
	if startedAt.IsZero() {
		return ProcessIdentity{}, ErrBootProcessStartRequired
	}
	return ProcessIdentityFromUID(fmt.Sprintf("%d", startedAt.UTC().UnixNano()), startedAt.UTC())
}

func ProcessIdentityFromUID(processUID string, startedAt time.Time) (ProcessIdentity, error) {
	uid, err := normalizeProcessUID(processUID)
	if err != nil {
		return ProcessIdentity{}, err
	}
	return ProcessIdentity{
		ProcessUID:          uid,
		BootSequencerUID:    processScopedAgentID(BootSequencerAgentID, uid),
		IdentityRegistryUID: processScopedAgentID(IdentityRegistryAgentID, uid),
		StartedAt:           startedAt.UTC(),
	}, nil
}

func (id ProcessIdentity) Validate() error {
	if _, err := normalizeProcessUID(id.ProcessUID); err != nil {
		return err
	}
	if strings.TrimSpace(id.BootSequencerUID) == "" || strings.TrimSpace(id.IdentityRegistryUID) == "" {
		return ErrBootProcessUIDRequired
	}
	return nil
}

func NewSystemParticipantActivation(participantID, participantType string, ready bool, context map[string]any) SystemParticipantActivation {
	return SystemParticipantActivation{
		ParticipantID:   strings.TrimSpace(participantID),
		ParticipantType: strings.TrimSpace(participantType),
		Ready:           ready,
		Context:         cloneMetadata(context),
	}
}

func RequiredSystemParticipants() []SystemParticipantActivation {
	return []SystemParticipantActivation{
		NewSystemParticipantActivation(SystemBusAdministratorAgentID, "bus_administrator", true, nil),
		NewSystemParticipantActivation(SystemSessionManagerAgentID, "session_manager", true, nil),
		NewSystemParticipantActivation(SystemFabricSubscriberAgentID, "fabric_subscriber", true, nil),
	}
}

func (s *OperationsSequencer) CommitPhase1(ctx context.Context, status Phase1Status) (PhaseCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := time.Now()
	claim, err := s.ensureClaimActive(ctx, phaseClaimSpec(BootPhaseDurableSubstrate, bootPhase1Order, s.identity.BootSequencerUID))
	if err != nil {
		return PhaseCommitResult{Phase: BootPhaseDurableSubstrate}, err
	}
	if err := validatePhase1Status(status); err != nil {
		testamentID, failureErr := s.completeClaimFailure(ctx, claim.ID, phaseFailureTestamentSpec(BootPhaseDurableSubstrate, err, status.Context, s.identity))
		return phaseResult(BootPhaseDurableSubstrate, claim.ID, testamentID, nil, nil), errors.Join(err, failureErr)
	}
	testamentID, err := s.completeClaimSuccess(ctx, claim.ID, phase1TestamentSpec(status, time.Since(start), s.identity))
	return phaseResult(BootPhaseDurableSubstrate, claim.ID, testamentID, nil, nil), err
}

func (s *OperationsSequencer) CommitPhase2(ctx context.Context, status Phase2Status) (PhaseCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requirePhaseSatisfied(BootPhaseDurableSubstrate); err != nil {
		return PhaseCommitResult{Phase: BootPhaseSystemParticipants}, err
	}
	if existing := s.phaseResultIfSatisfied(BootPhaseSystemParticipants); existing.ClaimID != "" {
		return existing, nil
	}
	participantClaimIDs, participantTestamentIDs, err := s.commitSystemParticipants(ctx, status.Participants)
	if err != nil {
		claim, claimErr := s.ensureClaimActive(ctx, phaseClaimSpec(BootPhaseSystemParticipants, bootPhase2Order, s.identity.BootSequencerUID))
		if claimErr != nil {
			return PhaseCommitResult{Phase: BootPhaseSystemParticipants, ParticipantClaimIDs: participantClaimIDs, ParticipantTestamentIDs: participantTestamentIDs}, errors.Join(err, claimErr)
		}
		testamentID, failureErr := s.completeClaimFailure(ctx, claim.ID, phaseFailureTestamentSpec(BootPhaseSystemParticipants, err, status.Context, s.identity))
		return phaseResult(BootPhaseSystemParticipants, claim.ID, testamentID, participantClaimIDs, participantTestamentIDs), errors.Join(err, failureErr)
	}
	start := time.Now()
	claim, err := s.ensureClaimActive(ctx, phaseClaimSpec(BootPhaseSystemParticipants, bootPhase2Order, s.identity.BootSequencerUID))
	if err != nil {
		return PhaseCommitResult{Phase: BootPhaseSystemParticipants, ParticipantClaimIDs: participantClaimIDs, ParticipantTestamentIDs: participantTestamentIDs}, err
	}
	testamentID, err := s.completeClaimSuccess(ctx, claim.ID, phase2TestamentSpec(status, time.Since(start), s.identity))
	return phaseResult(BootPhaseSystemParticipants, claim.ID, testamentID, participantClaimIDs, participantTestamentIDs), err
}

func (s *OperationsSequencer) commitSystemParticipants(ctx context.Context, participants []SystemParticipantActivation) ([]string, []string, error) {
	normalized, err := validatePhase2Participants(participants)
	if err != nil {
		return nil, nil, err
	}
	claimIDs := make([]string, 0, len(normalized))
	testamentIDs := make([]string, 0, len(normalized))
	for _, participant := range normalized {
		claimID, testamentID, err := s.commitSystemParticipant(ctx, participant)
		claimIDs = appendNonEmpty(claimIDs, claimID)
		testamentIDs = appendNonEmpty(testamentIDs, testamentID)
		if err != nil {
			return claimIDs, testamentIDs, err
		}
	}
	return claimIDs, testamentIDs, nil
}

func (s *OperationsSequencer) commitSystemParticipant(ctx context.Context, participant SystemParticipantActivation) (string, string, error) {
	claim, err := s.ensureClaimActive(ctx, participantClaimSpec(participant, s.identity.BootSequencerUID))
	if err != nil {
		return "", "", err
	}
	if !participant.Ready {
		err := fmt.Errorf("%w: %s not ready", ErrBootReadinessIncomplete, participant.ParticipantID)
		testamentID, failureErr := s.completeClaimFailure(ctx, claim.ID, participantFailureTestamentSpec(participant, err, s.identity.BootSequencerUID))
		return claim.ID, testamentID, errors.Join(err, failureErr)
	}
	testamentID, err := s.completeClaimSuccess(ctx, claim.ID, participantSuccessTestamentSpec(participant, s.identity.BootSequencerUID))
	return claim.ID, testamentID, err
}

func (s *OperationsSequencer) ensureClaimActive(ctx context.Context, spec bootClaimSpec) (*claims.Claim, error) {
	generated, err := s.board.GenerateClaimAction(ctx, claims.Action{AgentID: spec.issuer, Type: spec.actionType, Priority: spec.priority}, []claims.Claim{newBootClaim(spec)}, claims.GenerateClaimActionOptions{IdempotencyKey: spec.key, Reason: "boot operation claim generated"})
	if err != nil {
		return nil, err
	}
	claimID := generated.Claims[0].ID
	if err := s.postClaimIfGenerated(ctx, claimID, spec.issuer); err != nil {
		return nil, err
	}
	if err := s.acknowledgeClaimIfPosted(ctx, claimID, spec.subject); err != nil {
		return nil, err
	}
	if err := s.progressClaimIfOpen(ctx, claimID, spec.subject); err != nil {
		return nil, err
	}
	claim, ok := s.board.CloneClaim(claimID)
	if !ok {
		return nil, fmt.Errorf("boot claim %q disappeared after activation", claimID)
	}
	return claim, nil
}

func (s *OperationsSequencer) postClaimIfGenerated(ctx context.Context, claimID, actorID string) error {
	claim, ok := s.board.CloneClaim(claimID)
	if !ok || claim.LifecycleStatus != claims.ClaimLifecycleGenerated {
		return nil
	}
	err := s.board.PostGeneratedClaim(ctx, claimID, actorID, claims.ClaimPostOptions{Reason: "boot operation claim posted"})
	return ignorePostRace(err, func() bool { return s.claimBeyondGenerated(claimID) })
}

func (s *OperationsSequencer) acknowledgeClaimIfPosted(ctx context.Context, claimID, receiverID string) error {
	claim, ok := s.board.CloneClaim(claimID)
	if !ok || claim.LifecycleStatus != claims.ClaimLifecyclePosted {
		return nil
	}
	err := s.board.AcknowledgeClaimReceipt(ctx, claimID, receiverID)
	return ignorePostRace(err, func() bool { return s.claimBeyondPosted(claimID) })
}

func (s *OperationsSequencer) progressClaimIfOpen(ctx context.Context, claimID, actorID string) error {
	claim, ok := s.board.CloneClaim(claimID)
	if !ok || claim.Status.IsTerminal() || claim.LifecycleStatus == claims.ClaimLifecycleProgressed {
		return nil
	}
	err := s.board.UpdateClaimProgress(ctx, claimID, claims.ClaimProgressUpdate{WorkSummary: "boot operation in progress"}, actorID)
	return ignorePostRace(err, func() bool { return s.claimBeyondReceived(claimID) })
}

func (s *OperationsSequencer) completeClaimSuccess(ctx context.Context, claimID string, spec bootTestamentSpec) (string, error) {
	testamentID, err := s.ensureTestamentPosted(ctx, claimID, spec)
	if err != nil {
		return testamentID, err
	}
	return testamentID, s.validateReceiptTestament(ctx, claimID, testamentID, firstNonEmptyString(spec.validatorID, spec.agentID), claims.ValidationStatusPassed, claims.TestamentLifecycleValidated, "boot operation satisfied")
}

func (s *OperationsSequencer) completeClaimFailure(ctx context.Context, claimID string, spec bootTestamentSpec) (string, error) {
	testamentID, err := s.ensureTestamentPosted(ctx, claimID, spec)
	if err != nil {
		return testamentID, err
	}
	return testamentID, s.validateReceiptTestament(ctx, claimID, testamentID, firstNonEmptyString(spec.validatorID, spec.agentID), claims.ValidationStatusFailed, claims.TestamentLifecycleValidationFailed, "boot operation failed")
}

func (s *OperationsSequencer) ensureTestamentPosted(ctx context.Context, claimID string, spec bootTestamentSpec) (string, error) {
	generated, err := s.board.GenerateTestamentAction(ctx, claims.Action{AgentID: spec.agentID, Type: claims.ActionTypeTestament, Status: claims.ActionStatusComplete}, []claims.Testament{newBootTestament(claimID, spec)}, claims.GenerateTestamentActionOptions{IdempotencyKey: spec.key, Reason: "boot operation testament generated"})
	if err != nil {
		return "", err
	}
	testamentID := generated.Testaments[0].ID
	if err := s.postTestamentIfGenerated(ctx, testamentID, spec.agentID); err != nil {
		return testamentID, err
	}
	return testamentID, nil
}

func (s *OperationsSequencer) postTestamentIfGenerated(ctx context.Context, testamentID, actorID string) error {
	testament, ok := s.board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus != claims.TestamentLifecycleGenerated {
		return nil
	}
	err := s.board.PostGeneratedTestament(ctx, testamentID, actorID, claims.TestamentPostOptions{Reason: "boot operation testament posted"})
	return ignorePostRace(err, func() bool { return s.testamentBeyondGenerated(testamentID) })
}

func (s *OperationsSequencer) validateReceiptTestament(ctx context.Context, claimID, testamentID, actorID string, validationStatus claims.ValidationStatus, testamentStatus claims.TestamentLifecycleStatus, reason string) error {
	if err := s.acknowledgeTestamentIfPosted(ctx, testamentID, actorID); err != nil {
		return err
	}
	if err := s.beginTestamentValidationIfReceived(ctx, testamentID, actorID); err != nil {
		return err
	}
	if err := s.evaluateReceiptValidations(ctx, claimID, actorID, validationStatus, reason); err != nil {
		return err
	}
	return s.completeTestamentValidationIfValidating(ctx, testamentID, actorID, testamentStatus, reason)
}

func (s *OperationsSequencer) acknowledgeTestamentIfPosted(ctx context.Context, testamentID, actorID string) error {
	testament, ok := s.board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus != claims.TestamentLifecyclePosted {
		return nil
	}
	err := s.board.AcknowledgeTestamentReceipt(ctx, testamentID, actorID)
	return ignorePostRace(err, func() bool { return s.testamentBeyondPosted(testamentID) })
}

func (s *OperationsSequencer) beginTestamentValidationIfReceived(ctx context.Context, testamentID, actorID string) error {
	testament, ok := s.board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus != claims.TestamentLifecycleReceived {
		return nil
	}
	err := s.board.BeginTestamentValidation(ctx, testamentID, actorID)
	return ignorePostRace(err, func() bool { return s.testamentBeyondReceived(testamentID) })
}

func (s *OperationsSequencer) completeTestamentValidationIfValidating(ctx context.Context, testamentID, actorID string, to claims.TestamentLifecycleStatus, reason string) error {
	testament, ok := s.board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus != claims.TestamentLifecycleValidating {
		return terminalTestamentError(testament)
	}
	return s.board.CompleteTestamentValidation(ctx, testamentID, actorID, to, reason)
}

func (s *OperationsSequencer) evaluateReceiptValidations(ctx context.Context, claimID, actorID string, status claims.ValidationStatus, reason string) error {
	claim, ok := s.board.CloneClaim(claimID)
	if !ok {
		return fmt.Errorf("boot claim %q not found for receipt validation", claimID)
	}
	for _, validation := range claim.Validations {
		if validation == nil || validation.Type != claims.ValidationTypeReceipt {
			continue
		}
		if validation.Status == claims.ValidationStatusPassed && status == claims.ValidationStatusPassed {
			continue
		}
		if validation.Status.IsTerminal() {
			return fmt.Errorf("boot claim %q receipt validation %q already terminal: %s", claimID, validation.ID, validation.Status)
		}
		if err := s.board.EvaluateValidation(ctx, claimID, validation.ID, claims.StatusChange{AgentID: actorID, To: string(status), Reason: reason}); err != nil {
			return err
		}
	}
	return nil
}

func (s *OperationsSequencer) requirePhaseSatisfied(phase BootOperationPhase) error {
	if s.phaseResultIfSatisfied(phase).ClaimID == "" {
		return fmt.Errorf("%w: %s", ErrBootPhaseNotSatisfied, phase)
	}
	return nil
}

func (s *OperationsSequencer) phaseResultIfSatisfied(phase BootOperationPhase) PhaseCommitResult {
	claimID, testamentID := s.findSatisfiedClaimAndTestament(phaseClaimKey(phase))
	return phaseResult(phase, claimID, testamentID, nil, nil)
}

func (s *OperationsSequencer) findSatisfiedClaimAndTestament(key string) (string, string) {
	proj := s.board.Projection()
	claimID := ""
	for _, claim := range proj.Claims {
		if claim.IdempotencyKey == key && claim.LifecycleStatus == claims.ClaimLifecycleSatisfied {
			claimID = claim.ID
			break
		}
	}
	return claimID, testamentIDForClaim(proj, claimID)
}

func (s *OperationsSequencer) claimBeyondGenerated(claimID string) bool {
	claim, ok := s.board.CloneClaim(claimID)
	return ok && claim.LifecycleStatus != claims.ClaimLifecycleGenerated
}

func (s *OperationsSequencer) claimBeyondPosted(claimID string) bool {
	claim, ok := s.board.CloneClaim(claimID)
	return ok && claim.LifecycleStatus != claims.ClaimLifecyclePosted
}

func (s *OperationsSequencer) claimBeyondReceived(claimID string) bool {
	claim, ok := s.board.CloneClaim(claimID)
	return ok && claim.LifecycleStatus != claims.ClaimLifecyclePosted && claim.LifecycleStatus != claims.ClaimLifecycleReceived
}

func (s *OperationsSequencer) testamentBeyondGenerated(testamentID string) bool {
	testament, ok := s.board.CloneTestament(testamentID)
	return ok && testament.LifecycleStatus != claims.TestamentLifecycleGenerated
}

func (s *OperationsSequencer) testamentBeyondPosted(testamentID string) bool {
	testament, ok := s.board.CloneTestament(testamentID)
	return ok && testament.LifecycleStatus != claims.TestamentLifecyclePosted
}

func (s *OperationsSequencer) testamentBeyondReceived(testamentID string) bool {
	testament, ok := s.board.CloneTestament(testamentID)
	return ok && testament.LifecycleStatus != claims.TestamentLifecycleReceived
}

func phaseClaimSpec(phase BootOperationPhase, order int, bootSequencerUID string) bootClaimSpec {
	return bootClaimSpec{
		actionType:            claims.ActionTypeBoot,
		key:                   phaseClaimKey(phase),
		issuer:                bootSequencerUID,
		subject:               bootSequencerUID,
		title:                 fmt.Sprintf("Boot phase %d: %s", order, phase),
		description:           fmt.Sprintf("Satisfy boot phase %d (%s) before subsequent phases start.", order, phase),
		validationDescription: fmt.Sprintf("Boot phase %d produced a completion testament.", order),
		priority:              order,
	}
}

func participantClaimSpec(participant SystemParticipantActivation, bootSequencerUID string) bootClaimSpec {
	return bootClaimSpec{
		actionType:            claims.ActionTypeActivation,
		key:                   participantClaimKey(participant.ParticipantID),
		issuer:                bootSequencerUID,
		subject:               ActivationControllerAgentID,
		title:                 "Activate " + participant.ParticipantType,
		description:           "Activation controller must activate and report readiness for " + participant.ParticipantID + ".",
		validationDescription: "Activation readiness testament received for " + participant.ParticipantID + ".",
		priority:              bootPhase2Order,
	}
}

func newBootClaim(spec bootClaimSpec) claims.Claim {
	return claims.Claim{
		Title:       spec.title,
		Description: spec.description,
		ActionType:  spec.actionType,
		Priority:    spec.priority,
		Relations: []claims.Relation{
			{Related: spec.issuer, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: spec.subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{
			ID:          validationIDForKey(spec.key),
			Type:        claims.ValidationTypeReceipt,
			Required:    true,
			Description: spec.validationDescription,
			QualityBar:  bootValidationQuality,
			Status:      claims.ValidationStatusPending,
		}},
	}
}

func newBootTestament(claimID string, spec bootTestamentSpec) claims.Testament {
	return claims.Testament{
		AgentID:    spec.agentID,
		Summary:    spec.summary,
		Confidence: "high",
		Duration:   spec.duration,
		Artifacts:  spec.artifacts,
		Relations:  claimRelation(claimID),
	}
}

func phase1TestamentSpec(status Phase1Status, dur time.Duration, identity ProcessIdentity) bootTestamentSpec {
	return bootTestamentSpec{
		key:         phaseTestamentKey(BootPhaseDurableSubstrate, "complete"),
		agentID:     identity.BootSequencerUID,
		validatorID: identity.BootSequencerUID,
		summary:     "boot.phase_1_complete",
		duration:    dur,
		artifacts: []*claims.Artifact{
			phaseTimingArtifact(string(BootPhaseDurableSubstrate), dur),
			readinessArtifact(identity.BootSequencerUID, "claims_wal", status.WALOpened, phase1Metadata(status, identity)),
			readinessArtifact(identity.BootSequencerUID, "guide_event_bus", status.GuideBusOpened, processMetadata(identity)),
			readinessArtifact(identity.BootSequencerUID, "wal_replay", status.WALReplayed, mergeMetadata(processMetadata(identity), map[string]any{"replay_sequence": status.ReplaySequence})),
		},
	}
}

func phase2TestamentSpec(status Phase2Status, dur time.Duration, identity ProcessIdentity) bootTestamentSpec {
	return bootTestamentSpec{
		key:         phaseTestamentKey(BootPhaseSystemParticipants, "complete"),
		agentID:     identity.BootSequencerUID,
		validatorID: identity.BootSequencerUID,
		summary:     "boot.phase_2_complete",
		duration:    dur,
		artifacts:   phase2Artifacts(status, dur, identity),
	}
}

func phaseFailureTestamentSpec(phase BootOperationPhase, cause error, metadata map[string]any, identity ProcessIdentity) bootTestamentSpec {
	return bootTestamentSpec{
		key:         phaseTestamentKey(phase, "failed"),
		agentID:     identity.BootSequencerUID,
		validatorID: identity.BootSequencerUID,
		summary:     fmt.Sprintf("boot.phase_%d_failed", phaseOrder(phase)),
		artifacts: []*claims.Artifact{
			failureArtifact(identity.BootSequencerUID, cause, mergeMetadata(metadata, mergeMetadata(processMetadata(identity), map[string]any{"phase": string(phase)}))),
		},
	}
}

func participantSuccessTestamentSpec(participant SystemParticipantActivation, bootSequencerUID string) bootTestamentSpec {
	return bootTestamentSpec{
		key:         participantTestamentKey(participant.ParticipantID, "complete"),
		agentID:     ActivationControllerAgentID,
		validatorID: bootSequencerUID,
		summary:     "boot.participant_activated." + participant.ParticipantType,
		artifacts: []*claims.Artifact{
			readinessArtifact(ActivationControllerAgentID, participant.ParticipantID, true, participantMetadata(participant)),
		},
	}
}

func participantFailureTestamentSpec(participant SystemParticipantActivation, cause error, bootSequencerUID string) bootTestamentSpec {
	return bootTestamentSpec{
		key:         participantTestamentKey(participant.ParticipantID, "failed"),
		agentID:     ActivationControllerAgentID,
		validatorID: bootSequencerUID,
		summary:     "boot.participant_failed." + participant.ParticipantType,
		artifacts: []*claims.Artifact{
			failureArtifact(ActivationControllerAgentID, cause, participantMetadata(participant)),
		},
	}
}

func phase2Artifacts(status Phase2Status, dur time.Duration, identity ProcessIdentity) []*claims.Artifact {
	artifacts := []*claims.Artifact{phaseTimingArtifact(string(BootPhaseSystemParticipants), dur)}
	for _, participant := range status.Participants {
		artifacts = append(artifacts, readinessArtifact(identity.BootSequencerUID, participant.ParticipantID, participant.Ready, mergeMetadata(participantMetadata(participant), processMetadata(identity))))
	}
	return artifacts
}

func readinessArtifact(agentID, name string, ready bool, metadata map[string]any) *claims.Artifact {
	return &claims.Artifact{
		Kind:      artifactKindBootReadiness,
		Reference: fmt.Sprintf("%s ready=%t", name, ready),
		AgentID:   agentID,
		Metadata:  mergeMetadata(metadata, map[string]any{"name": name, "ready": ready}),
	}
}

func failureArtifact(agentID string, cause error, metadata map[string]any) *claims.Artifact {
	message := "unknown boot failure"
	if cause != nil {
		message = cause.Error()
	}
	return &claims.Artifact{
		Kind:      artifactKindBootFailure,
		Reference: message,
		AgentID:   agentID,
		Metadata:  mergeMetadata(metadata, map[string]any{"error": message}),
	}
}

func validatePhase1Status(status Phase1Status) error {
	missing := missingReadiness(map[string]bool{
		"claims_wal":      status.WALOpened,
		"guide_event_bus": status.GuideBusOpened,
		"wal_replay":      status.WALReplayed,
	})
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrBootReadinessIncomplete, strings.Join(missing, ", "))
}

func validatePhase2Participants(participants []SystemParticipantActivation) ([]SystemParticipantActivation, error) {
	normalized := normalizeParticipants(participants)
	missing := missingParticipantDefinitions(normalized)
	if len(missing) != 0 {
		return nil, fmt.Errorf("%w: %s", ErrBootReadinessIncomplete, strings.Join(missing, ", "))
	}
	return normalized, nil
}

func normalizeParticipants(participants []SystemParticipantActivation) []SystemParticipantActivation {
	byID := make(map[string]SystemParticipantActivation, len(participants))
	for _, participant := range participants {
		id := strings.TrimSpace(participant.ParticipantID)
		if id == "" {
			continue
		}
		participant.ParticipantID = id
		participant.ParticipantType = firstNonEmptyString(participant.ParticipantType, id)
		participant.Context = cloneMetadata(participant.Context)
		byID[id] = participant
	}
	out := make([]SystemParticipantActivation, 0, len(requiredParticipantIDs()))
	for _, id := range requiredParticipantIDs() {
		out = append(out, byID[id])
	}
	return out
}

func missingParticipantDefinitions(participants []SystemParticipantActivation) []string {
	missing := make([]string, 0, len(participants))
	for _, participant := range participants {
		if participant.ParticipantID == "" || participant.ParticipantType == "" {
			missing = append(missing, "unknown_participant")
		}
	}
	return missing
}

func missingReadiness(readiness map[string]bool) []string {
	missing := make([]string, 0, len(readiness))
	for name, ready := range readiness {
		if !ready {
			missing = append(missing, name)
		}
	}
	return missing
}

func terminalTestamentError(testament *claims.Testament) error {
	if testament == nil || testament.LifecycleStatus == claims.TestamentLifecycleValidated {
		return nil
	}
	if testament.LifecycleStatus.IsTerminal() {
		return fmt.Errorf("boot testament %q is terminal: %s", testament.ID, testament.LifecycleStatus)
	}
	return nil
}

func ignorePostRace(err error, ok func() bool) error {
	if err == nil || (ok != nil && ok()) {
		return nil
	}
	return err
}

func phaseResult(phase BootOperationPhase, claimID, testamentID string, participantClaims, participantTestaments []string) PhaseCommitResult {
	return PhaseCommitResult{
		Phase:                   phase,
		ClaimID:                 claimID,
		TestamentID:             testamentID,
		ParticipantClaimIDs:     append([]string(nil), participantClaims...),
		ParticipantTestamentIDs: append([]string(nil), participantTestaments...),
	}
}

func participantMetadata(participant SystemParticipantActivation) map[string]any {
	return mergeMetadata(participant.Context, map[string]any{
		"participant_id":   participant.ParticipantID,
		"participant_type": participant.ParticipantType,
		"ready":            participant.Ready,
	})
}

func phase1Metadata(status Phase1Status, identity ProcessIdentity) map[string]any {
	return mergeMetadata(mergeMetadata(status.Context, processMetadata(identity)), map[string]any{
		"wal_path":        status.WALPath,
		"replay_sequence": status.ReplaySequence,
	})
}

func processMetadata(identity ProcessIdentity) map[string]any {
	return map[string]any{
		"process_uid":           identity.ProcessUID,
		"boot_sequencer_uid":    identity.BootSequencerUID,
		"identity_registry_uid": identity.IdentityRegistryUID,
	}
}

func mergeMetadata(a, b map[string]any) map[string]any {
	out := cloneMetadata(a)
	for key, value := range b {
		out[key] = value
	}
	return out
}

func cloneMetadata(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func appendNonEmpty(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	return append(values, value)
}

func phaseClaimKey(phase BootOperationPhase) string {
	return strings.Join([]string{bootOperationKeyPrefix, string(phase), "claim"}, ":")
}

func phaseTestamentKey(phase BootOperationPhase, outcome string) string {
	return strings.Join([]string{bootOperationKeyPrefix, string(phase), strings.TrimSpace(outcome), "testament"}, ":")
}

func participantClaimKey(participantID string) string {
	return strings.Join([]string{bootOperationKeyPrefix, string(BootPhaseSystemParticipants), sanitizeKeyPart(participantID), "claim"}, ":")
}

func participantTestamentKey(participantID, outcome string) string {
	return strings.Join([]string{bootOperationKeyPrefix, string(BootPhaseSystemParticipants), sanitizeKeyPart(participantID), strings.TrimSpace(outcome), "testament"}, ":")
}

func validationIDForKey(key string) string {
	return sanitizeKeyPart(key) + "_receipt"
}

func sanitizeKeyPart(value string) string {
	return strings.NewReplacer(":", "_", "/", "_", " ", "_").Replace(strings.TrimSpace(value))
}

func requiredParticipantIDs() []string {
	return []string{SystemBusAdministratorAgentID, SystemSessionManagerAgentID, SystemFabricSubscriberAgentID}
}

func phaseOrder(phase BootOperationPhase) int {
	switch phase {
	case BootPhaseDurableSubstrate:
		return bootPhase1Order
	case BootPhaseSystemParticipants:
		return bootPhase2Order
	default:
		return 0
	}
}

func defaultBootProcessUID(board *claims.ClaimsBoard) string {
	if board == nil {
		return "default"
	}
	uid := sanitizeKeyPart(strings.Join([]string{board.SessionID(), board.BoardID()}, "_"))
	return firstNonEmptyString(uid, "default")
}

func operationsProcessIdentity(cfg OperationsConfig) (ProcessIdentity, error) {
	if strings.TrimSpace(cfg.ProcessIdentity.ProcessUID) != "" {
		return validateOrRepairProcessIdentity(cfg.ProcessIdentity)
	}
	if strings.TrimSpace(cfg.ProcessUID) != "" {
		return ProcessIdentityFromUID(cfg.ProcessUID, time.Time{})
	}
	return ProcessIdentityFromUID(defaultBootProcessUID(cfg.Board), time.Time{})
}

func phase0ProcessIdentity(cfg Phase0Config) (ProcessIdentity, error) {
	if strings.TrimSpace(cfg.ProcessUID) != "" {
		return ProcessIdentityFromUID(cfg.ProcessUID, cfg.StartedAt)
	}
	return NewProcessIdentity(cfg.StartedAt)
}

func validateOrRepairProcessIdentity(identity ProcessIdentity) (ProcessIdentity, error) {
	uid, err := normalizeProcessUID(identity.ProcessUID)
	if err != nil {
		return ProcessIdentity{}, err
	}
	identity.ProcessUID = uid
	identity.BootSequencerUID = firstNonEmptyString(identity.BootSequencerUID, processScopedAgentID(BootSequencerAgentID, uid))
	identity.IdentityRegistryUID = firstNonEmptyString(identity.IdentityRegistryUID, processScopedAgentID(IdentityRegistryAgentID, uid))
	return identity, identity.Validate()
}

func normalizeProcessUID(processUID string) (string, error) {
	uid := strings.TrimSpace(processUID)
	if uid == "" {
		return "", ErrBootProcessUIDRequired
	}
	if strings.ContainsAny(uid, ":/ \t\r\n") {
		return "", ErrBootProcessUIDInvalid
	}
	return uid, nil
}

func processScopedAgentID(baseAgentID, processUID string) string {
	return strings.Join([]string{baseAgentID, processScopeSegment + "/" + processUID}, ":")
}

func phase0BoardID(processUID string) string {
	return strings.Join([]string{phase0BoardIDPrefix, sanitizeKeyPart(processUID)}, ".")
}

func testamentIDForClaim(proj *claims.ClaimsBoardProjection, claimID string) string {
	if proj == nil || claimID == "" {
		return ""
	}
	for _, testament := range proj.Testaments {
		if claims.ClaimIDFromRelations(testament.Relations) == claimID {
			return testament.ID
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
