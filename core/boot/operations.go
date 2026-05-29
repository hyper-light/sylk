package boot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	BootSequencerAgentID           = "sys:boot_sequencer"
	IdentityRegistryAgentID        = "sys:identity_registry"
	ActivationControllerAgentID    = "sys:activation_controller"
	SystemBusAdministratorAgentID  = "sys:bus_administrator"
	SystemSessionManagerAgentID    = "sys:session_manager"
	SystemFabricSubscriberAgentID  = "sys:fabric_subscriber"
	SystemKnowledgeGraphReaderID   = "sys:knowledge_graph_reader"
	SystemKnowledgeGraphWriterID   = "sys:knowledge_graph_writer"
	SystemDocumentDBReaderID       = "sys:document_db_reader"
	SystemDocumentDBWriterID       = "sys:document_db_writer"
	SystemMemoryForestAgentID      = "sys:memory_forest"
	SystemVFSPipelineProvisionerID = "sys:vfs_pipeline_provisioner"
	SystemVFSToolProvisionerID     = "sys:vfs_tool_provisioner"
	SystemVFSGlobalProvisionerID   = "sys:vfs_global_provisioner"
	SystemDAGProcessorID           = "sys:dag_processor"
	SystemToolRuntimeID            = "sys:tool_runtime"
	SystemProviderGatewayID        = "sys:provider_gateway"
	SystemGuardianServiceID        = "sys:guardian_service"
	SystemGuideAgentID             = "guide"
	SystemUIBridgeID               = "sys:ui_bridge"
	SystemCanonicalDeltaObserverID = "sys:canonical_delta_observer"

	BootPhaseDurableSubstrate   BootOperationPhase = "durable_substrate"
	BootPhaseSystemParticipants BootOperationPhase = "system_participants"
	BootPhaseKnowledgeBackends  BootOperationPhase = "knowledge_backends"
	BootPhaseInfrastructure     BootOperationPhase = "infrastructure_services"
	BootPhaseAgentActivation    BootOperationPhase = "agent_activation"
	BootPhaseUserSurfaces       BootOperationPhase = "user_facing_surfaces"
	BootPhaseComplete           BootOperationPhase = "boot_complete"

	bootPhase1Order = 1
	bootPhase2Order = 2
	bootPhase3Order = 3
	bootPhase4Order = 4
	bootPhase5Order = 5
	bootPhase6Order = 6
	bootPhase7Order = 7

	bootOperationKeyPrefix = "boot.operations"
	bootValidationQuality  = "receipt.received"
	bootTaskID             = "boot"
	phase0BoardIDPrefix    = "boot.phase0"
	processScopeSegment    = "proc"
	bootServiceQueueFloor  = 4
	bootServiceConcurrency = 1
	bootServiceTimeout     = time.Second

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
	Board                     *claims.ClaimsBoard
	ProcessIdentity           ProcessIdentity
	ProcessUID                string
	ServiceDispatcherRegistry *claims.ServiceDispatcherRegistry
	ServiceSubscriber         claims.DeltaSubscriber
	ServiceScope              claims.ScopeProvider
	ServiceDispatchers        []ServiceDispatcherBootRegistration
}

type OperationsSequencer struct {
	board                     *claims.ClaimsBoard
	identity                  ProcessIdentity
	serviceDispatcherRegistry *claims.ServiceDispatcherRegistry
	serviceSubscriber         claims.DeltaSubscriber
	serviceScope              claims.ScopeProvider
	serviceDispatchers        []ServiceDispatcherBootRegistration
	mu                        sync.Mutex
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
	SubjectID       string
	Ready           bool
	Context         map[string]any
}

type Phase2Status struct {
	Participants []SystemParticipantActivation
	Context      map[string]any
}

type Phase3Status struct {
	Backends []SystemParticipantActivation
	Context  map[string]any
}

type Phase4Status struct {
	Services []SystemParticipantActivation
	Context  map[string]any
}

type Phase5Status struct {
	Agents  []SystemParticipantActivation
	Context map[string]any
}

type Phase6Status struct {
	Surfaces []SystemParticipantActivation
	Context  map[string]any
}

type Phase7Status struct {
	Context map[string]any
}

type BootPhaseHealth struct {
	Phase          BootOperationPhase
	Outcome        string
	ClaimID        string
	TestamentID    string
	Duration       time.Duration
	ReadinessCount int
	FailureCount   int
	Warnings       []string
}

type BootHealth struct {
	BoardID           string
	SessionID         string
	HighWaterSequence uint64
	OutboxLag         uint64
	TerminalFailures  int
	Phases            []BootPhaseHealth
}

type PhaseCommitResult struct {
	Phase                   BootOperationPhase
	ClaimID                 string
	TestamentID             string
	ParticipantClaimIDs     []string
	ParticipantTestamentIDs []string
}

type ServiceDispatcherBootRegistration struct {
	Phase         BootOperationPhase
	ParticipantID string
	Participant   claims.ParticipantRegistration
	Handler       claims.ServiceHandler
	Subscriber    claims.DeltaSubscriber
	Scope         claims.ScopeProvider
	Optional      bool
	Context       map[string]any
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

type readinessPhaseConfig struct {
	phase          BootOperationPhase
	order          int
	prerequisite   BootOperationPhase
	participants   []SystemParticipantActivation
	requiredIDs    []string
	context        map[string]any
	defaultSubject string
	summary        string
	extraArtifacts []*claims.Artifact
}

func NewOperationsSequencer(cfg OperationsConfig) (*OperationsSequencer, error) {
	if cfg.Board == nil {
		return nil, ErrBootOperationsBoardRequired
	}
	identity, err := operationsProcessIdentity(cfg)
	if err != nil {
		return nil, err
	}
	registry := cfg.ServiceDispatcherRegistry
	if registry == nil {
		registry = claims.NewServiceDispatcherRegistry()
	}
	scope := cfg.ServiceScope
	if scope == nil {
		scope = bootInlineScope{}
	}
	seq := &OperationsSequencer{
		board:                     cfg.Board,
		identity:                  identity,
		serviceDispatcherRegistry: registry,
		serviceSubscriber:         cfg.ServiceSubscriber,
		serviceScope:              scope,
	}
	seq.serviceDispatchers = seq.normalizeServiceDispatcherSpecs(cfg.ServiceDispatchers)
	return seq, nil
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
		SubjectID:       ActivationControllerAgentID,
		Ready:           ready,
		Context:         cloneMetadata(context),
	}
}

func NewServiceParticipantReadiness(participantID, participantType string, ready bool, context map[string]any) SystemParticipantActivation {
	participant := NewSystemParticipantActivation(participantID, participantType, ready, context)
	participant.SubjectID = strings.TrimSpace(participantID)
	return participant
}

func RequiredSystemParticipants() []SystemParticipantActivation {
	return []SystemParticipantActivation{
		NewSystemParticipantActivation(SystemBusAdministratorAgentID, "bus_administrator", true, nil),
		NewSystemParticipantActivation(SystemSessionManagerAgentID, "session_manager", true, nil),
		NewSystemParticipantActivation(SystemFabricSubscriberAgentID, "fabric_subscriber", true, nil),
	}
}

func RequiredKnowledgeBackends() []SystemParticipantActivation {
	return []SystemParticipantActivation{
		NewServiceParticipantReadiness(SystemKnowledgeGraphReaderID, "knowledge_graph_reader", true, nil),
		NewServiceParticipantReadiness(SystemKnowledgeGraphWriterID, "knowledge_graph_writer", true, nil),
		NewServiceParticipantReadiness(SystemDocumentDBReaderID, "document_db_reader", true, nil),
		NewServiceParticipantReadiness(SystemDocumentDBWriterID, "document_db_writer", true, nil),
		NewServiceParticipantReadiness(SystemMemoryForestAgentID, "memory_forest", true, nil),
	}
}

func RequiredInfrastructureServices() []SystemParticipantActivation {
	return []SystemParticipantActivation{
		NewServiceParticipantReadiness(SystemVFSPipelineProvisionerID, "vfs_pipeline_provisioner", true, nil),
		NewServiceParticipantReadiness(SystemVFSToolProvisionerID, "vfs_tool_provisioner", true, nil),
		NewServiceParticipantReadiness(SystemVFSGlobalProvisionerID, "vfs_global_provisioner", true, nil),
		NewServiceParticipantReadiness(SystemDAGProcessorID, "dag_processor", true, nil),
		NewServiceParticipantReadiness(SystemToolRuntimeID, "tool_runtime", true, nil),
		NewServiceParticipantReadiness(SystemProviderGatewayID, "provider_gateway", true, nil),
		NewServiceParticipantReadiness(SystemGuardianServiceID, "guardian_service", true, nil),
	}
}

func RequiredAlwaysHotAgents() []SystemParticipantActivation {
	return []SystemParticipantActivation{
		NewSystemParticipantActivation(SystemGuideAgentID, "guide", true, nil),
	}
}

func RequiredUserFacingSurfaces() []SystemParticipantActivation {
	return []SystemParticipantActivation{
		NewServiceParticipantReadiness(SystemUIBridgeID, "ui_bridge", true, nil),
		NewServiceParticipantReadiness(SystemCanonicalDeltaObserverID, "canonical_delta_observer", true, nil),
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
	return s.commitReadinessPhase(ctx, readinessPhaseConfig{
		phase:          BootPhaseSystemParticipants,
		order:          bootPhase2Order,
		prerequisite:   BootPhaseDurableSubstrate,
		participants:   status.Participants,
		requiredIDs:    requiredParticipantIDs(BootPhaseSystemParticipants),
		context:        status.Context,
		defaultSubject: ActivationControllerAgentID,
		summary:        "boot.phase_2_complete",
	})
}

func (s *OperationsSequencer) CommitPhase3(ctx context.Context, status Phase3Status) (PhaseCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitReadinessPhase(ctx, readinessPhaseConfig{
		phase:          BootPhaseKnowledgeBackends,
		order:          bootPhase3Order,
		prerequisite:   BootPhaseSystemParticipants,
		participants:   status.Backends,
		requiredIDs:    requiredParticipantIDs(BootPhaseKnowledgeBackends),
		context:        status.Context,
		defaultSubject: "",
		summary:        "boot.phase_3_complete",
	})
}

func (s *OperationsSequencer) CommitPhase4(ctx context.Context, status Phase4Status) (PhaseCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitReadinessPhase(ctx, readinessPhaseConfig{
		phase:          BootPhaseInfrastructure,
		order:          bootPhase4Order,
		prerequisite:   BootPhaseKnowledgeBackends,
		participants:   status.Services,
		requiredIDs:    requiredParticipantIDs(BootPhaseInfrastructure),
		context:        status.Context,
		defaultSubject: "",
		summary:        "boot.phase_4_complete",
	})
}

func (s *OperationsSequencer) CommitPhase5(ctx context.Context, status Phase5Status) (PhaseCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitReadinessPhase(ctx, readinessPhaseConfig{
		phase:          BootPhaseAgentActivation,
		order:          bootPhase5Order,
		prerequisite:   BootPhaseInfrastructure,
		participants:   status.Agents,
		requiredIDs:    requiredParticipantIDs(BootPhaseAgentActivation),
		context:        status.Context,
		defaultSubject: ActivationControllerAgentID,
		summary:        "boot.phase_5_complete",
	})
}

func (s *OperationsSequencer) CommitPhase6(ctx context.Context, status Phase6Status) (PhaseCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitReadinessPhase(ctx, readinessPhaseConfig{
		phase:          BootPhaseUserSurfaces,
		order:          bootPhase6Order,
		prerequisite:   BootPhaseAgentActivation,
		participants:   status.Surfaces,
		requiredIDs:    requiredParticipantIDs(BootPhaseUserSurfaces),
		context:        status.Context,
		defaultSubject: "",
		summary:        "boot.phase_6_complete",
	})
}

func (s *OperationsSequencer) CommitPhase7(ctx context.Context, status Phase7Status) (PhaseCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requirePhaseSatisfied(BootPhaseUserSurfaces); err != nil {
		return PhaseCommitResult{Phase: BootPhaseComplete}, err
	}
	if existing := s.phaseResultIfSatisfied(BootPhaseComplete); existing.ClaimID != "" {
		return existing, nil
	}
	start := time.Now()
	claim, err := s.ensureClaimActive(ctx, phaseClaimSpec(BootPhaseComplete, bootPhase7Order, s.identity.BootSequencerUID))
	if err != nil {
		return PhaseCommitResult{Phase: BootPhaseComplete}, err
	}
	spec := phase7TestamentSpec(status, time.Since(start), s.identity, s.bootPhaseSummary())
	testamentID, err := s.completeClaimSuccess(ctx, claim.ID, spec)
	return phaseResult(BootPhaseComplete, claim.ID, testamentID, nil, nil), err
}

func (s *OperationsSequencer) BootHealth() BootHealth {
	if s == nil || s.board == nil {
		return BootHealth{}
	}
	return BootHealthFromBoard(s.board)
}

func (s *OperationsSequencer) Close() error {
	if s == nil || s.serviceDispatcherRegistry == nil || s.board == nil {
		return nil
	}
	return s.serviceDispatcherRegistry.CloseSession(s.board.SessionID())
}

func BootHealthFromBoard(board *claims.ClaimsBoard) BootHealth {
	if board == nil {
		return BootHealth{}
	}
	projectionHealth := board.ProjectionHealth()
	return BootHealth{
		BoardID:           board.BoardID(),
		SessionID:         board.SessionID(),
		HighWaterSequence: board.HighWaterSequence(),
		OutboxLag:         projectionHealth.MaxLag,
		TerminalFailures:  projectionHealth.TerminalFailures,
		Phases:            bootPhaseHealthFromProjection(board.Projection()),
	}
}

func (s *OperationsSequencer) commitReadinessPhase(ctx context.Context, cfg readinessPhaseConfig) (PhaseCommitResult, error) {
	if err := s.requirePhaseSatisfied(cfg.prerequisite); err != nil {
		return PhaseCommitResult{Phase: cfg.phase}, err
	}
	if existing := s.phaseResultIfSatisfied(cfg.phase); existing.ClaimID != "" {
		_, err := s.registerServiceDispatchersForPhase(ctx, cfg, nil)
		if err != nil {
			return existing, err
		}
		return existing, nil
	}
	participants, err := validatePhaseParticipants(cfg.phase, cfg.participants, cfg.requiredIDs)
	if err != nil {
		return s.commitPhaseFailure(ctx, cfg, nil, nil, err)
	}
	participantClaimIDs, participantTestamentIDs, err := s.commitPhaseParticipants(ctx, cfg, participants)
	if err != nil {
		return s.commitPhaseFailure(ctx, cfg, participantClaimIDs, participantTestamentIDs, err)
	}
	dispatcherArtifacts, err := s.registerServiceDispatchersForPhase(ctx, cfg, participants)
	if err != nil {
		return s.commitPhaseFailure(ctx, cfg, participantClaimIDs, participantTestamentIDs, err)
	}
	cfg.extraArtifacts = dispatcherArtifacts
	start := time.Now()
	claim, err := s.ensureClaimActive(ctx, phaseClaimSpec(cfg.phase, cfg.order, s.identity.BootSequencerUID))
	if err != nil {
		return PhaseCommitResult{Phase: cfg.phase, ParticipantClaimIDs: participantClaimIDs, ParticipantTestamentIDs: participantTestamentIDs}, err
	}
	testamentID, err := s.completeClaimSuccess(ctx, claim.ID, phaseReadinessTestamentSpec(cfg, participants, time.Since(start), s.identity))
	return phaseResult(cfg.phase, claim.ID, testamentID, participantClaimIDs, participantTestamentIDs), err
}

func (s *OperationsSequencer) commitPhaseFailure(ctx context.Context, cfg readinessPhaseConfig, participantClaims, participantTestaments []string, cause error) (PhaseCommitResult, error) {
	claim, claimErr := s.ensureClaimActive(ctx, phaseClaimSpec(cfg.phase, cfg.order, s.identity.BootSequencerUID))
	if claimErr != nil {
		return PhaseCommitResult{Phase: cfg.phase, ParticipantClaimIDs: participantClaims, ParticipantTestamentIDs: participantTestaments}, errors.Join(cause, claimErr)
	}
	testamentID, failureErr := s.completeClaimFailure(ctx, claim.ID, phaseFailureTestamentSpec(cfg.phase, cause, cfg.context, s.identity))
	return phaseResult(cfg.phase, claim.ID, testamentID, participantClaims, participantTestaments), errors.Join(cause, failureErr)
}

func (s *OperationsSequencer) commitPhaseParticipants(ctx context.Context, cfg readinessPhaseConfig, participants []SystemParticipantActivation) ([]string, []string, error) {
	claimIDs := make([]string, 0, len(participants))
	testamentIDs := make([]string, 0, len(participants))
	for _, participant := range participants {
		claimID, testamentID, err := s.commitPhaseParticipant(ctx, cfg, participant)
		claimIDs = appendNonEmpty(claimIDs, claimID)
		testamentIDs = appendNonEmpty(testamentIDs, testamentID)
		if err != nil {
			return claimIDs, testamentIDs, err
		}
	}
	return claimIDs, testamentIDs, nil
}

func (s *OperationsSequencer) commitPhaseParticipant(ctx context.Context, cfg readinessPhaseConfig, participant SystemParticipantActivation) (string, string, error) {
	claim, err := s.ensureClaimActive(ctx, participantClaimSpec(cfg.phase, cfg.order, participant, s.identity.BootSequencerUID, cfg.defaultSubject))
	if err != nil {
		return "", "", err
	}
	if !participant.Ready {
		err := fmt.Errorf("%w: %s not ready", ErrBootReadinessIncomplete, participant.ParticipantID)
		testamentID, failureErr := s.completeClaimFailure(ctx, claim.ID, participantFailureTestamentSpec(cfg.phase, participant, err, s.identity.BootSequencerUID))
		return claim.ID, testamentID, errors.Join(err, failureErr)
	}
	testamentID, err := s.completeClaimSuccess(ctx, claim.ID, participantSuccessTestamentSpec(cfg.phase, participant, s.identity.BootSequencerUID))
	return claim.ID, testamentID, err
}

func (s *OperationsSequencer) registerServiceDispatchersForPhase(ctx context.Context, cfg readinessPhaseConfig, participants []SystemParticipantActivation) ([]*claims.Artifact, error) {
	specs := s.serviceDispatcherSpecsForPhase(cfg.phase)
	if len(specs) == 0 {
		return nil, nil
	}
	byParticipant := participantsByID(participants)
	artifacts := make([]*claims.Artifact, 0, len(specs))
	for _, spec := range specs {
		artifact, err := s.ensureServiceDispatcher(ctx, spec, byParticipant)
		if err != nil && !spec.Optional {
			return artifacts, err
		}
		if err != nil {
			artifact = dispatcherFailureArtifact(s.identity.BootSequencerUID, cfg.phase, spec, err)
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func (s *OperationsSequencer) ensureServiceDispatcher(ctx context.Context, spec ServiceDispatcherBootRegistration, participants map[string]SystemParticipantActivation) (*claims.Artifact, error) {
	spec = s.normalizeServiceDispatcherSpec(spec)
	participant, err := serviceDispatcherParticipant(spec)
	if err != nil {
		return nil, err
	}
	if dispatcher, ok := s.serviceDispatcherRegistry.Lookup(s.board.SessionID(), participant.UID); ok {
		return dispatcherReadinessArtifact(s.identity.BootSequencerUID, spec.Phase, dispatcher, "existing", spec, participants), nil
	}
	dispatcher, err := claims.NewServiceDispatcher(claims.ServiceDispatcherConfig{
		Board:       s.board,
		Subscriber:  firstNonNilSubscriber(spec.Subscriber, s.serviceSubscriber),
		Scope:       firstNonNilScope(spec.Scope, s.serviceScope),
		Participant: participant,
		Handler:     spec.Handler,
		SessionID:   s.board.SessionID(),
	})
	if err != nil {
		return nil, err
	}
	if err := s.serviceDispatcherRegistry.Register(s.board.SessionID(), dispatcher); err != nil {
		_ = dispatcher.Close()
		if errors.Is(err, claims.ErrServiceDispatcherAlreadyRegistered) {
			existing, _ := s.serviceDispatcherRegistry.Lookup(s.board.SessionID(), participant.UID)
			return dispatcherReadinessArtifact(s.identity.BootSequencerUID, spec.Phase, existing, "existing", spec, participants), nil
		}
		return nil, err
	}
	if err := dispatcher.Start(ctx); err != nil {
		s.serviceDispatcherRegistry.Remove(s.board.SessionID(), participant.UID)
		_ = dispatcher.Close()
		return nil, err
	}
	return dispatcherReadinessArtifact(s.identity.BootSequencerUID, spec.Phase, dispatcher, "registered", spec, participants), nil
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

func participantClaimSpec(phase BootOperationPhase, order int, participant SystemParticipantActivation, bootSequencerUID, defaultSubject string) bootClaimSpec {
	subject := firstNonEmptyString(participant.SubjectID, defaultSubject, participant.ParticipantID)
	return bootClaimSpec{
		actionType:            claims.ActionTypeActivation,
		key:                   participantClaimKey(phase, participant.ParticipantID),
		issuer:                bootSequencerUID,
		subject:               subject,
		title:                 "Activate " + participant.ParticipantType,
		description:           "Activation controller must activate and report readiness for " + participant.ParticipantID + ".",
		validationDescription: "Activation readiness testament received for " + participant.ParticipantID + ".",
		priority:              order,
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
	return phaseReadinessTestamentSpec(readinessPhaseConfig{phase: BootPhaseSystemParticipants, summary: "boot.phase_2_complete"}, status.Participants, dur, identity)
}

func phaseReadinessTestamentSpec(cfg readinessPhaseConfig, participants []SystemParticipantActivation, dur time.Duration, identity ProcessIdentity) bootTestamentSpec {
	artifacts := phaseReadinessArtifacts(cfg.phase, participants, dur, identity)
	artifacts = append(artifacts, cfg.extraArtifacts...)
	return bootTestamentSpec{
		key:         phaseTestamentKey(cfg.phase, "complete"),
		agentID:     identity.BootSequencerUID,
		validatorID: identity.BootSequencerUID,
		summary:     cfg.summary,
		duration:    dur,
		artifacts:   artifacts,
	}
}

func phase7TestamentSpec(status Phase7Status, dur time.Duration, identity ProcessIdentity, phases []BootPhaseHealth) bootTestamentSpec {
	return bootTestamentSpec{
		key:         phaseTestamentKey(BootPhaseComplete, "satisfied"),
		agentID:     identity.BootSequencerUID,
		validatorID: identity.BootSequencerUID,
		summary:     "boot.satisfied",
		duration:    dur,
		artifacts: []*claims.Artifact{
			phaseTimingArtifact(string(BootPhaseComplete), dur),
			readinessArtifact(identity.BootSequencerUID, "boot_complete", true, mergeMetadata(status.Context, map[string]any{
				"phase_count": len(phases),
				"phases":      bootPhaseHealthMetadata(phases),
			})),
		},
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

func participantSuccessTestamentSpec(phase BootOperationPhase, participant SystemParticipantActivation, bootSequencerUID string) bootTestamentSpec {
	actorID := participantActorID(participant)
	return bootTestamentSpec{
		key:         participantTestamentKey(phase, participant.ParticipantID, "complete"),
		agentID:     actorID,
		validatorID: bootSequencerUID,
		summary:     "boot.participant_activated." + participant.ParticipantType,
		artifacts: []*claims.Artifact{
			readinessArtifact(actorID, participant.ParticipantID, true, participantMetadata(participant)),
		},
	}
}

func participantFailureTestamentSpec(phase BootOperationPhase, participant SystemParticipantActivation, cause error, bootSequencerUID string) bootTestamentSpec {
	actorID := participantActorID(participant)
	return bootTestamentSpec{
		key:         participantTestamentKey(phase, participant.ParticipantID, "failed"),
		agentID:     actorID,
		validatorID: bootSequencerUID,
		summary:     "boot.participant_failed." + participant.ParticipantType,
		artifacts: []*claims.Artifact{
			failureArtifact(actorID, cause, participantMetadata(participant)),
		},
	}
}

func phaseReadinessArtifacts(phase BootOperationPhase, participants []SystemParticipantActivation, dur time.Duration, identity ProcessIdentity) []*claims.Artifact {
	artifacts := []*claims.Artifact{phaseTimingArtifact(string(phase), dur)}
	for _, participant := range participants {
		artifacts = append(artifacts, readinessArtifact(identity.BootSequencerUID, participant.ParticipantID, participant.Ready, mergeMetadata(participantMetadata(participant), processMetadata(identity))))
	}
	return artifacts
}

func dispatcherReadinessArtifact(agentID string, phase BootOperationPhase, dispatcher *claims.ServiceDispatcher, outcome string, spec ServiceDispatcherBootRegistration, participants map[string]SystemParticipantActivation) *claims.Artifact {
	participant := claims.ParticipantRegistration{}
	if dispatcher != nil {
		participant = dispatcher.Participant()
	}
	metadata := dispatcherMetadata(phase, participant, outcome, spec, participants)
	return readinessArtifact(agentID, "service_dispatcher."+sanitizeKeyPart(dispatcherParticipantID(spec, participant)), true, metadata)
}

func dispatcherFailureArtifact(agentID string, phase BootOperationPhase, spec ServiceDispatcherBootRegistration, cause error) *claims.Artifact {
	metadata := mergeMetadata(spec.Context, map[string]any{
		"phase":              string(phase),
		"participant_id":     strings.TrimSpace(spec.ParticipantID),
		"service_dispatcher": true,
		"registration":       "failed",
		"ready":              false,
	})
	return failureArtifact(agentID, cause, metadata)
}

func dispatcherMetadata(phase BootOperationPhase, participant claims.ParticipantRegistration, outcome string, spec ServiceDispatcherBootRegistration, participants map[string]SystemParticipantActivation) map[string]any {
	participantID := dispatcherParticipantID(spec, participant)
	metadata := mergeMetadata(spec.Context, map[string]any{
		"phase":              string(phase),
		"participant_id":     participantID,
		"participant_uid":    participant.UID,
		"route_key":          participant.RouteKey,
		"category":           string(participant.Category),
		"service_dispatcher": true,
		"registration":       outcome,
		"topics":             dispatcherTopicsFor(participant, spec),
	})
	if readiness, ok := participants[participantID]; ok {
		metadata["participant_type"] = readiness.ParticipantType
		metadata["participant_ready"] = readiness.Ready
	}
	return metadata
}

func dispatcherTopicsFor(participant claims.ParticipantRegistration, spec ServiceDispatcherBootRegistration) []string {
	sessionID := ""
	if value, ok := spec.Context["session_id"].(string); ok {
		sessionID = value
	}
	if sessionID == "" {
		sessionID = participant.ScopeKeys["session_id"]
	}
	if participant.UID == "" {
		return nil
	}
	return []string{claims.CanonicalAgentRefTopic(sessionID, participant.AgentRef(), claims.DeltaActionClaimPosted)}
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
	return validatePhaseParticipants(BootPhaseSystemParticipants, participants, requiredParticipantIDs(BootPhaseSystemParticipants))
}

func validatePhaseParticipants(phase BootOperationPhase, participants []SystemParticipantActivation, requiredIDs []string) ([]SystemParticipantActivation, error) {
	normalized := normalizeParticipants(participants, requiredIDs)
	missing := missingParticipantDefinitions(normalized)
	if len(missing) != 0 {
		return nil, fmt.Errorf("%w: %s", ErrBootReadinessIncomplete, strings.Join(missing, ", "))
	}
	return normalizeParticipantSubjects(phase, normalized), nil
}

func (s *OperationsSequencer) normalizeServiceDispatcherSpecs(overrides []ServiceDispatcherBootRegistration) []ServiceDispatcherBootRegistration {
	defaults := s.defaultServiceDispatcherSpecs()
	byKey := make(map[string]ServiceDispatcherBootRegistration, len(defaults)+len(overrides))
	for _, spec := range defaults {
		byKey[serviceDispatcherSpecKey(spec)] = spec
	}
	for _, spec := range overrides {
		spec = s.normalizeServiceDispatcherSpec(spec)
		byKey[serviceDispatcherSpecKey(spec)] = spec
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ServiceDispatcherBootRegistration, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out
}

func (s *OperationsSequencer) defaultServiceDispatcherSpecs() []ServiceDispatcherBootRegistration {
	specs := []ServiceDispatcherBootRegistration{
		s.identityRegistryDispatcherSpec(),
		s.activationControllerDispatcherSpec(),
	}
	specs = append(specs, s.readinessDispatcherSpecs(BootPhaseKnowledgeBackends, RequiredKnowledgeBackends())...)
	specs = append(specs, s.readinessDispatcherSpecs(BootPhaseInfrastructure, RequiredInfrastructureServices())...)
	specs = append(specs, s.readinessDispatcherSpecs(BootPhaseUserSurfaces, RequiredUserFacingSurfaces())...)
	return specs
}

func (s *OperationsSequencer) identityRegistryDispatcherSpec() ServiceDispatcherBootRegistration {
	participant := mustBootParticipant(claims.ParticipantCategorySystem, IdentityRegistryAgentID, processScopeKeys(s.identity), claims.HandlerDeterminismPure, []claims.ActionType{claims.ActionTypeTask})
	return ServiceDispatcherBootRegistration{Phase: BootPhaseSystemParticipants, ParticipantID: IdentityRegistryAgentID, Participant: participant, Handler: claims.NewIdentityRegistryService(claims.IdentityRegistryServiceConfig{}), Context: s.bootServiceContext(IdentityRegistryAgentID)}
}

func (s *OperationsSequencer) activationControllerDispatcherSpec() ServiceDispatcherBootRegistration {
	participant := mustBootParticipant(claims.ParticipantCategoryService, ActivationControllerAgentID, sessionScopeKeys(s.board, s.identity), claims.HandlerDeterminismSideEffect, []claims.ActionType{claims.ActionTypeTask})
	return ServiceDispatcherBootRegistration{Phase: BootPhaseSystemParticipants, ParticipantID: ActivationControllerAgentID, Participant: participant, Handler: claims.NewActivationControllerService(claims.ActivationControllerServiceConfig{}), Context: s.bootServiceContext(ActivationControllerAgentID)}
}

func (s *OperationsSequencer) readinessDispatcherSpecs(phase BootOperationPhase, participants []SystemParticipantActivation) []ServiceDispatcherBootRegistration {
	out := make([]ServiceDispatcherBootRegistration, 0, len(participants))
	for _, participant := range participants {
		reg := mustBootParticipant(claims.ParticipantCategoryService, participant.ParticipantID, sessionScopeKeys(s.board, s.identity), bootServiceDeterminism(participant.ParticipantID), []claims.ActionType{claims.ActionTypeTask})
		handler := claims.NewDefaultInfrastructureServiceHandler(participant.ParticipantID)
		if handler == nil {
			handler = bootReadinessServiceHandler{ParticipantID: participant.ParticipantID, ParticipantType: participant.ParticipantType}
		}
		out = append(out, ServiceDispatcherBootRegistration{Phase: phase, ParticipantID: participant.ParticipantID, Participant: reg, Handler: handler, Context: s.bootServiceContext(participant.ParticipantID)})
	}
	return out
}

func (s *OperationsSequencer) normalizeServiceDispatcherSpec(spec ServiceDispatcherBootRegistration) ServiceDispatcherBootRegistration {
	spec.ParticipantID = firstNonEmptyString(spec.ParticipantID, spec.Participant.RouteKey, spec.Participant.UID)
	spec.Context = mergeMetadata(s.bootServiceContext(spec.ParticipantID), spec.Context)
	return spec
}

func normalizeParticipants(participants []SystemParticipantActivation, requiredIDs []string) []SystemParticipantActivation {
	byID := make(map[string]SystemParticipantActivation, len(participants))
	for _, participant := range participants {
		id := strings.TrimSpace(participant.ParticipantID)
		if id == "" {
			continue
		}
		participant.ParticipantID = id
		participant.ParticipantType = firstNonEmptyString(participant.ParticipantType, id)
		participant.Context = cloneMetadata(participant.Context)
		byID[id] = mergeParticipant(byID[id], participant)
	}
	if len(requiredIDs) == 0 {
		return sortedParticipants(byID)
	}
	out := make([]SystemParticipantActivation, 0, len(requiredIDs)+len(byID))
	for _, id := range requiredIDs {
		out = append(out, byID[id])
		delete(byID, id)
	}
	out = append(out, sortedParticipants(byID)...)
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

func normalizeParticipantSubjects(_ BootOperationPhase, participants []SystemParticipantActivation) []SystemParticipantActivation {
	out := make([]SystemParticipantActivation, 0, len(participants))
	for _, participant := range participants {
		participant.SubjectID = firstNonEmptyString(participant.SubjectID, participant.ParticipantID)
		out = append(out, participant)
	}
	return out
}

func mergeParticipant(existing, candidate SystemParticipantActivation) SystemParticipantActivation {
	if strings.TrimSpace(existing.ParticipantID) == "" {
		return candidate
	}
	existing.ParticipantType = firstNonEmptyString(existing.ParticipantType, candidate.ParticipantType)
	existing.SubjectID = firstNonEmptyString(existing.SubjectID, candidate.SubjectID)
	existing.Ready = existing.Ready && candidate.Ready
	existing.Context = mergeMetadata(existing.Context, candidate.Context)
	return existing
}

func sortedParticipants(byID map[string]SystemParticipantActivation) []SystemParticipantActivation {
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]SystemParticipantActivation, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
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
		"subject_id":       participant.SubjectID,
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

func participantActorID(participant SystemParticipantActivation) string {
	return firstNonEmptyString(participant.SubjectID, ActivationControllerAgentID)
}

func participantClaimKey(phase BootOperationPhase, participantID string) string {
	return strings.Join([]string{bootOperationKeyPrefix, string(phase), sanitizeKeyPart(participantID), "claim"}, ":")
}

func participantTestamentKey(phase BootOperationPhase, participantID, outcome string) string {
	return strings.Join([]string{bootOperationKeyPrefix, string(phase), sanitizeKeyPart(participantID), strings.TrimSpace(outcome), "testament"}, ":")
}

func validationIDForKey(key string) string {
	return sanitizeKeyPart(key) + "_receipt"
}

func sanitizeKeyPart(value string) string {
	return strings.NewReplacer(":", "_", "/", "_", " ", "_").Replace(strings.TrimSpace(value))
}

func requiredParticipantIDs(phase BootOperationPhase) []string {
	switch phase {
	case BootPhaseSystemParticipants:
		return participantIDs(RequiredSystemParticipants())
	case BootPhaseKnowledgeBackends:
		return participantIDs(RequiredKnowledgeBackends())
	case BootPhaseInfrastructure:
		return participantIDs(RequiredInfrastructureServices())
	case BootPhaseAgentActivation:
		return participantIDs(RequiredAlwaysHotAgents())
	case BootPhaseUserSurfaces:
		return participantIDs(RequiredUserFacingSurfaces())
	default:
		return nil
	}
}

func (s *OperationsSequencer) serviceDispatcherSpecsForPhase(phase BootOperationPhase) []ServiceDispatcherBootRegistration {
	if s == nil {
		return nil
	}
	out := make([]ServiceDispatcherBootRegistration, 0)
	for _, spec := range s.serviceDispatchers {
		if spec.Phase == phase {
			out = append(out, spec)
		}
	}
	return out
}

func serviceDispatcherParticipant(spec ServiceDispatcherBootRegistration) (claims.ParticipantRegistration, error) {
	if err := spec.Participant.Validate(); err != nil {
		return claims.ParticipantRegistration{}, err
	}
	return spec.Participant, nil
}

func serviceDispatcherSpecKey(spec ServiceDispatcherBootRegistration) string {
	return string(spec.Phase) + "|" + sanitizeKeyPart(firstNonEmptyString(spec.ParticipantID, spec.Participant.RouteKey, spec.Participant.UID))
}

func dispatcherParticipantID(spec ServiceDispatcherBootRegistration, participant claims.ParticipantRegistration) string {
	return firstNonEmptyString(spec.ParticipantID, participant.RouteKey, participant.UID, "unknown")
}

func participantsByID(participants []SystemParticipantActivation) map[string]SystemParticipantActivation {
	out := make(map[string]SystemParticipantActivation, len(participants))
	for _, participant := range participants {
		if participant.ParticipantID != "" {
			out[participant.ParticipantID] = participant
		}
	}
	return out
}

func mustBootParticipant(category claims.ParticipantCategory, routeKey string, scope map[string]string, determinism claims.HandlerDeterminism, actions []claims.ActionType) claims.ParticipantRegistration {
	participant, err := claims.NewParticipantRegistration(category, routeKey, scope, bootServiceQueueCapacity(actions), bootServiceConcurrency, bootServiceTimeout, determinism, actions)
	if err != nil {
		panic(err)
	}
	return participant
}

func bootServiceQueueCapacity(actions []claims.ActionType) int {
	capacity := len(actions) + len(actions) + bootServiceQueueFloor
	if capacity < bootServiceQueueFloor {
		return bootServiceQueueFloor
	}
	return capacity
}

func bootServiceDeterminism(participantID string) claims.HandlerDeterminism {
	switch participantID {
	case SystemProviderGatewayID:
		return claims.HandlerDeterminismNondeterministic
	case SystemVFSPipelineProvisionerID, SystemVFSToolProvisionerID, SystemVFSGlobalProvisionerID, SystemDAGProcessorID, SystemToolRuntimeID, SystemGuardianServiceID:
		return claims.HandlerDeterminismSideEffect
	default:
		return claims.HandlerDeterminismContent
	}
}

func (s *OperationsSequencer) bootServiceContext(participantID string) map[string]any {
	return map[string]any{
		"participant_id": participantID,
		"process_uid":    s.identity.ProcessUID,
		"session_id":     s.board.SessionID(),
	}
}

func processScopeKeys(identity ProcessIdentity) map[string]string {
	return map[string]string{"process_uid": identity.ProcessUID}
}

func sessionScopeKeys(board *claims.ClaimsBoard, identity ProcessIdentity) map[string]string {
	sessionID := ""
	if board != nil {
		sessionID = board.SessionID()
	}
	if sessionID == "" {
		return processScopeKeys(identity)
	}
	return map[string]string{"session_id": sessionID}
}

func firstNonNilSubscriber(values ...claims.DeltaSubscriber) claims.DeltaSubscriber {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonNilScope(values ...claims.ScopeProvider) claims.ScopeProvider {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func participantIDs(participants []SystemParticipantActivation) []string {
	ids := make([]string, 0, len(participants))
	for _, participant := range participants {
		ids = append(ids, participant.ParticipantID)
	}
	return ids
}

func phaseOrder(phase BootOperationPhase) int {
	switch phase {
	case BootPhaseDurableSubstrate:
		return bootPhase1Order
	case BootPhaseSystemParticipants:
		return bootPhase2Order
	case BootPhaseKnowledgeBackends:
		return bootPhase3Order
	case BootPhaseInfrastructure:
		return bootPhase4Order
	case BootPhaseAgentActivation:
		return bootPhase5Order
	case BootPhaseUserSurfaces:
		return bootPhase6Order
	case BootPhaseComplete:
		return bootPhase7Order
	default:
		return 0
	}
}

func (s *OperationsSequencer) bootPhaseSummary() []BootPhaseHealth {
	if s == nil || s.board == nil {
		return nil
	}
	return bootPhaseHealthFromProjection(s.board.Projection())
}

func bootPhaseHealthFromProjection(proj *claims.ClaimsBoardProjection) []BootPhaseHealth {
	if proj == nil {
		return nil
	}
	byClaim := testamentsByClaimID(proj)
	out := make([]BootPhaseHealth, 0, len(allBootPhases()))
	for _, phase := range allBootPhases() {
		claim := claimForPhase(proj, phase)
		out = append(out, bootPhaseHealthForClaim(phase, claim, byClaim[claimID(claim)]))
	}
	return out
}

func bootPhaseHealthForClaim(phase BootOperationPhase, claim *claims.Claim, testament *claims.Testament) BootPhaseHealth {
	health := BootPhaseHealth{Phase: phase, Outcome: "pending"}
	if claim == nil {
		health.Outcome = "missing"
		health.Warnings = []string{"phase claim missing"}
		return health
	}
	health.ClaimID = claim.ID
	health.Outcome = string(claim.LifecycleStatus)
	if testament == nil {
		health.Warnings = []string{"phase testament missing"}
		return health
	}
	health.TestamentID = testament.ID
	health.Duration = testament.Duration
	health.ReadinessCount, health.FailureCount = countBootArtifacts(testament.Artifacts)
	health.Warnings = bootPhaseWarnings(testament.Artifacts)
	return health
}

func countBootArtifacts(artifacts []*claims.Artifact) (int, int) {
	readiness := 0
	failures := 0
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		readiness += boolToInt(artifact.Kind == artifactKindBootReadiness || artifact.Kind == claims.ArtifactKindReadiness)
		failures += boolToInt(artifact.Kind == artifactKindBootFailure || artifact.Kind == claims.ArtifactKindErrorDiagnostic || artifact.Kind == claims.ArtifactKindError)
	}
	return readiness, failures
}

func bootPhaseWarnings(artifacts []*claims.Artifact) []string {
	warnings := make([]string, 0)
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		if bootArtifactReadyFalse(artifact) {
			warnings = append(warnings, bootArtifactName(artifact)+" ready=false")
		}
		if artifact.Kind == artifactKindBootFailure || artifact.Kind == claims.ArtifactKindErrorDiagnostic || artifact.Kind == claims.ArtifactKindError {
			warnings = append(warnings, firstNonEmptyString(artifact.Reference, "boot failure"))
		}
	}
	return warnings
}

func bootArtifactReadyFalse(artifact *claims.Artifact) bool {
	ready, ok := artifact.Metadata["ready"].(bool)
	return ok && !ready
}

func bootArtifactName(artifact *claims.Artifact) string {
	if artifact == nil {
		return "unknown"
	}
	if name, ok := artifact.Metadata["name"].(string); ok {
		return firstNonEmptyString(name, artifact.ArtifactName, artifact.Kind, "unknown")
	}
	return firstNonEmptyString(artifact.ArtifactName, artifact.Kind, "unknown")
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func claimForPhase(proj *claims.ClaimsBoardProjection, phase BootOperationPhase) *claims.Claim {
	key := phaseClaimKey(phase)
	for i := range proj.Claims {
		if proj.Claims[i].IdempotencyKey == key {
			return &proj.Claims[i]
		}
	}
	return nil
}

func testamentsByClaimID(proj *claims.ClaimsBoardProjection) map[string]*claims.Testament {
	out := make(map[string]*claims.Testament, len(proj.Testaments))
	for i := range proj.Testaments {
		testament := &proj.Testaments[i]
		out[claims.ClaimIDFromRelations(testament.Relations)] = testament
	}
	return out
}

func claimID(claim *claims.Claim) string {
	if claim == nil {
		return ""
	}
	return claim.ID
}

func allBootPhases() []BootOperationPhase {
	return []BootOperationPhase{
		BootPhaseDurableSubstrate,
		BootPhaseSystemParticipants,
		BootPhaseKnowledgeBackends,
		BootPhaseInfrastructure,
		BootPhaseAgentActivation,
		BootPhaseUserSurfaces,
		BootPhaseComplete,
	}
}

func bootPhaseHealthMetadata(phases []BootPhaseHealth) []map[string]any {
	out := make([]map[string]any, 0, len(phases))
	for _, phase := range phases {
		out = append(out, map[string]any{
			"phase":           string(phase.Phase),
			"outcome":         phase.Outcome,
			"claim_id":        phase.ClaimID,
			"testament_id":    phase.TestamentID,
			"readiness_count": phase.ReadinessCount,
			"failure_count":   phase.FailureCount,
		})
	}
	return out
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

type bootInlineScope struct{}

func (bootInlineScope) Go(_ string, _ time.Duration, fn func(context.Context) error) error {
	return fn(context.Background())
}

type bootReadinessServiceHandler struct {
	ParticipantID   string
	ParticipantType string
}

func (h bootReadinessServiceHandler) HandleServiceClaim(_ context.Context, req claims.ServiceClaimRequest) (claims.ServiceClaimResult, error) {
	participantID := firstNonEmptyString(h.ParticipantID, req.Participant.RouteKey)
	participantType := firstNonEmptyString(h.ParticipantType, participantID)
	artifact := &claims.Artifact{
		ArtifactName: "service_readiness",
		Kind:         claims.ArtifactKindReadiness,
		Reference:    participantID + " ready",
		Metadata: map[string]any{
			"participant_id":   participantID,
			"participant_type": participantType,
			"claim_id":         claimIDForReadiness(req.Claim),
		},
	}
	err := claims.SetArtifactData(artifact, claims.KnowledgeReadinessArtifactData{
		Component:  participantID,
		QualityBar: "service_dispatch.ready",
		Reference:  artifact.Reference,
		Metadata:   artifact.Metadata,
	})
	if err != nil {
		return claims.ServiceClaimResult{}, err
	}
	return claims.ServiceClaimResult{Summary: "service readiness " + participantID, Artifacts: []*claims.Artifact{artifact}}, nil
}

func claimIDForReadiness(claim *claims.Claim) string {
	if claim == nil {
		return ""
	}
	return claim.ID
}
