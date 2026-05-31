package claims

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	SessionManagerToolOpenSession   = "open_session"
	SessionManagerToolCloseSession  = "close_session"
	SessionManagerToolPersist       = "persist"
	SessionManagerToolRestore       = "restore"
	SessionManagerToolSwitchSession = "switch_session"

	FabricSubscriberToolAttach       = "attach"
	FabricSubscriberToolDetach       = "detach"
	FabricSubscriberToolQueryLens    = "query_lens"
	FabricSubscriberToolHarvest      = "harvest"
	FabricSubscriberToolTestDelivery = "test_delivery"

	BusAdministratorToolRegisterTopic  = "register_topic"
	BusAdministratorToolAdjustCapacity = "adjust_capacity"
	BusAdministratorToolDrain          = "drain"
	BusAdministratorToolOverflow       = "overflow"
	BusAdministratorToolSubscriberFail = "subscriber_failure"
)

const (
	systemParticipantSession = "session_manager"
	systemParticipantFabric  = "fabric_subscriber"
	systemParticipantBus     = "bus_administrator"
	systemParticipantGeneric = "generic_system_evidence"

	GenericSystemEvidenceToolRecord = "record_evidence"
)

type SystemParticipantServiceConfig struct {
	ParticipantID string
	Kind          string
	MaxRecords    int
	Clock         ClaimsClock
}

type SystemParticipantService struct {
	mu            sync.Mutex
	participantID string
	kind          string
	maxRecords    int
	clock         ClaimsClock
	seen          map[string]struct{}
	order         []string
}

type GenericSystemEvidenceService struct{}

type genericSystemEvidenceArgs struct {
	Operation    string         `json:"operation,omitempty"`
	Status       string         `json:"status,omitempty"`
	ArtifactKind string         `json:"artifact_kind,omitempty"`
	ArtifactName string         `json:"artifact_name,omitempty"`
	Reference    string         `json:"reference,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

func NewSessionManagerService(cfg SystemParticipantServiceConfig) *SystemParticipantService {
	cfg.Kind = systemParticipantSession
	cfg.ParticipantID = firstNonEmpty(strings.TrimSpace(cfg.ParticipantID), systemEvidenceDefaultActor)
	return newSystemParticipantService(cfg)
}

func NewFabricSubscriberService(cfg SystemParticipantServiceConfig) *SystemParticipantService {
	cfg.Kind = systemParticipantFabric
	cfg.ParticipantID = firstNonEmpty(strings.TrimSpace(cfg.ParticipantID), systemEvidenceFabricActor)
	return newSystemParticipantService(cfg)
}

func NewBusAdministratorService(cfg SystemParticipantServiceConfig) *SystemParticipantService {
	cfg.Kind = systemParticipantBus
	cfg.ParticipantID = firstNonEmpty(strings.TrimSpace(cfg.ParticipantID), systemEvidenceBusActor)
	return newSystemParticipantService(cfg)
}

func NewGenericSystemEvidenceService() *GenericSystemEvidenceService {
	return &GenericSystemEvidenceService{}
}

func newSystemParticipantService(cfg SystemParticipantServiceConfig) *SystemParticipantService {
	tools := systemParticipantTools(cfg.Kind)
	maxRecords := cfg.MaxRecords
	if maxRecords <= 0 {
		maxRecords = len(tools) * len(systemParticipantRecordClasses())
	}
	return &SystemParticipantService{
		participantID: strings.TrimSpace(cfg.ParticipantID),
		kind:          strings.TrimSpace(cfg.Kind),
		maxRecords:    maxRecords,
		clock:         firstNonNilClock(cfg.Clock),
		seen:          make(map[string]struct{}, maxRecords),
	}
}

func (s *SystemParticipantService) HandleServiceClaim(ctx context.Context, req ServiceClaimRequest) (ServiceClaimResult, error) {
	if s == nil || s.maxRecords <= 0 || s.seen == nil {
		return systemParticipantFailureResult(systemParticipantGeneric, ExpectedToolCall{Tool: GenericSystemEvidenceToolRecord}, fmt.Errorf("%w: system participant service is not initialized", ErrInfrastructureServiceInvalid))
	}
	if req.Claim == nil {
		return systemParticipantFailureResult(s.kind, ExpectedToolCall{Tool: systemParticipantFallbackTool(s.kind)}, fmt.Errorf("%w: claim is required", ErrInfrastructureServiceInvalid))
	}
	call, err := systemParticipantCall(req.Claim, systemParticipantTools(s.kind))
	if err != nil {
		return systemParticipantFailureResult(s.kind, ExpectedToolCall{Tool: systemParticipantFallbackTool(s.kind)}, err)
	}
	switch s.kind {
	case systemParticipantSession:
		return s.handleSessionLifecycle(ctxOrBackground(ctx), call, req)
	case systemParticipantFabric:
		return s.handleFabricSubscription(ctxOrBackground(ctx), call)
	case systemParticipantBus:
		return s.handleBusTransport(ctxOrBackground(ctx), call)
	default:
		return systemParticipantFailureResult(s.kind, call, fmt.Errorf("%w: unknown system participant kind %q", ErrInfrastructureServiceInvalid, s.kind))
	}
}

func (s *SystemParticipantService) ServiceTools() []string {
	if s == nil {
		return nil
	}
	return systemParticipantTools(s.kind)
}

func (s *GenericSystemEvidenceService) HandleServiceClaim(_ context.Context, req ServiceClaimRequest) (ServiceClaimResult, error) {
	if req.Claim == nil {
		return genericSystemEvidenceFailureResult(ExpectedToolCall{Tool: GenericSystemEvidenceToolRecord}, fmt.Errorf("%w: claim is required", ErrInfrastructureServiceInvalid))
	}
	call, err := systemParticipantCall(req.Claim, systemParticipantTools(systemParticipantGeneric))
	if err != nil {
		return genericSystemEvidenceFailureResult(ExpectedToolCall{Tool: GenericSystemEvidenceToolRecord}, err)
	}
	var args genericSystemEvidenceArgs
	if err := decodeArgs(call.Arguments, &args); err != nil {
		return genericSystemEvidenceFailureResult(call, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err))
	}
	kind := firstNonEmpty(args.ArtifactKind, ArtifactKindReadiness)
	reference := firstNonEmpty(args.Reference, args.Operation, "system evidence")
	artifact := &Artifact{
		ArtifactName: firstNonEmpty(args.ArtifactName, sanitizeSystemEvidenceSegment(reference)),
		Kind:         kind,
		Reference:    reference,
	}
	metadata := mergeMetadata(args.Metadata, map[string]any{
		"operation": args.Operation,
		"status":    args.Status,
	})
	if err := SetArtifactData(artifact, PresentationEvidenceArtifactData{Kind: kind, Reference: reference, Title: artifact.ArtifactName, Metadata: metadata}); err != nil {
		return genericSystemEvidenceFailureResult(call, err)
	}
	return ServiceClaimResult{Summary: "system evidence " + reference, Artifacts: []*Artifact{artifact}}, nil
}

func (s *GenericSystemEvidenceService) ServiceTools() []string {
	return systemParticipantTools(systemParticipantGeneric)
}

func systemParticipantFailureResult(kind string, call ExpectedToolCall, cause error) (ServiceClaimResult, error) {
	cause = infrastructureServiceCause(cause)
	switch strings.TrimSpace(kind) {
	case systemParticipantSession:
		data := failSessionLifecycleData(call, cause)
		artifact, err := sessionLifecycleArtifact(data, systemArtifactName(call, "session_"+sanitizeSystemEvidenceSegment(data.Operation)), systemArtifactReference(call, "session "+data.Operation), systemArtifactKindOverride(call))
		return serviceClaimResultWithArtifact("session "+data.Operation+" failed", artifact, err, "session_lifecycle_failure_artifact_error", data.SessionID)
	case systemParticipantFabric:
		data := failFabricSubscriptionData(call, cause)
		artifact, err := fabricSubscriptionArtifact(data, systemArtifactName(call, "fabric_"+sanitizeSystemEvidenceSegment(data.Operation)), systemArtifactReference(call, "fabric "+data.Operation), systemArtifactKindOverride(call))
		return serviceClaimResultWithArtifact("fabric "+data.Operation+" failed", artifact, err, "fabric_subscription_failure_artifact_error", data.Topic)
	case systemParticipantBus:
		data := failBusTransportData(call, cause)
		artifact, err := busTransportArtifact(data, systemArtifactName(call, "bus_"+sanitizeSystemEvidenceSegment(data.Operation)), systemArtifactReference(call, "bus "+data.Operation), systemArtifactKindOverride(call))
		return serviceClaimResultWithArtifact("bus "+data.Operation+" failed", artifact, err, "bus_transport_failure_artifact_error", data.Topic)
	default:
		return genericSystemEvidenceFailureResult(call, cause)
	}
}

func genericSystemEvidenceFailureResult(call ExpectedToolCall, cause error) (ServiceClaimResult, error) {
	cause = infrastructureServiceCause(cause)
	reference := firstNonEmpty(systemArtifactReference(call, ""), call.Tool, GenericSystemEvidenceToolRecord)
	artifact := &Artifact{ArtifactName: firstNonEmpty(systemArtifactName(call, ""), "system_evidence_failure"), Kind: ArtifactKindErrorDiagnostic, Reference: reference}
	artifact.Errors = append(artifact.Errors, &ArtifactError{Category: ArtifactErrorCategoryInternal, Description: cause.Error(), OccurredAt: time.Now().UTC()})
	err := SetArtifactData(artifact, PresentationEvidenceArtifactData{
		Kind:      ArtifactKindErrorDiagnostic,
		Reference: reference,
		Title:     artifact.ArtifactName,
		Metadata: map[string]any{
			"failure_reason": cause.Error(),
			"operation":      firstNonEmpty(stringArg(call.Arguments, "operation"), call.Tool),
		},
	})
	return serviceClaimResultWithArtifact("system evidence "+reference+" failed", artifact, err, "system_evidence_failure_artifact_error", reference)
}

func systemParticipantFallbackTool(kind string) string {
	tools := systemParticipantTools(kind)
	if len(tools) == 0 {
		return GenericSystemEvidenceToolRecord
	}
	return tools[0]
}

func (s *SystemParticipantService) handleSessionLifecycle(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeSessionLifecycleData(ctx, call, req)
	if err != nil {
		data = failSessionLifecycleData(call, err)
	}
	if !s.record(systemParticipantRecordKey(data.SessionID, data.Operation, data.BoardID)) {
		data.FailureReason = "session lifecycle record capacity reached"
		data.Status = InfrastructureStatusFailed
		data.Active = false
	}
	artifact, err := sessionLifecycleArtifact(data, systemArtifactName(call, "session_"+sanitizeSystemEvidenceSegment(data.Operation)), systemArtifactReference(call, "session "+data.Operation), systemArtifactKindOverride(call))
	return serviceClaimResultWithArtifact("session "+data.Operation+" "+data.Status, artifact, err, "session_lifecycle_artifact_error", data.SessionID)
}

func (s *SystemParticipantService) handleFabricSubscription(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	data, err := normalizeFabricSubscriptionData(ctx, call)
	if err != nil {
		data = failFabricSubscriptionData(call, err)
	}
	if !s.record(systemParticipantRecordKey(data.SessionID, data.AgentID, data.Operation, data.Topic)) {
		data.FailureReason = "fabric subscription record capacity reached"
		data.Status = InfrastructureStatusFailed
		data.Attached = false
	}
	artifact, err := fabricSubscriptionArtifact(data, systemArtifactName(call, "fabric_"+sanitizeSystemEvidenceSegment(data.Operation)), systemArtifactReference(call, "fabric "+data.Operation), systemArtifactKindOverride(call))
	return serviceClaimResultWithArtifact("fabric "+data.Operation+" "+data.Status, artifact, err, "fabric_subscription_artifact_error", data.Topic)
}

func (s *SystemParticipantService) handleBusTransport(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	data, err := normalizeBusTransportData(ctx, call)
	if err != nil {
		data = failBusTransportData(call, err)
	}
	if !s.record(systemParticipantRecordKey(data.ProcessUID, data.Topic, data.Operation)) {
		data.FailureReason = "bus transport record capacity reached"
		data.Status = InfrastructureStatusFailed
	}
	artifact, err := busTransportArtifact(data, systemArtifactName(call, "bus_"+sanitizeSystemEvidenceSegment(data.Operation)), systemArtifactReference(call, "bus "+data.Operation), systemArtifactKindOverride(call))
	return serviceClaimResultWithArtifact("bus "+data.Operation+" "+data.Status, artifact, err, "bus_transport_artifact_error", data.Topic)
}

func (s *SystemParticipantService) record(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[key]; ok {
		return true
	}
	if len(s.order) >= s.maxRecords {
		return false
	}
	s.seen[key] = struct{}{}
	s.order = append(s.order, key)
	return true
}

func normalizeSessionLifecycleData(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (SessionLifecycleArtifactData, error) {
	var data SessionLifecycleArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, stringArg(call.Arguments, "operation"), sessionOperationForTool(call.Tool))
	data.SessionID = firstNonEmpty(data.SessionID, stringArg(call.Arguments, "session_id"), reqSessionID(req))
	data.BoardID = firstNonEmpty(data.BoardID, stringArg(call.Arguments, "board_id"), boardIDForSystemParticipant(req.Board))
	data.Metadata = cloneMetadata(data.Metadata)
	data.Active = boolArgDefault(call.Arguments, "active", sessionActiveByOperation(data.Operation))
	data.Persisted = boolArgDefault(call.Arguments, "persisted", data.Persisted || data.Operation == "persist")
	data.Restored = boolArgDefault(call.Arguments, "restored", data.Restored || data.Operation == "restore" || data.Operation == "recover")
	data.StaleReplaced = boolArgDefault(call.Arguments, "stale_replaced", data.StaleReplaced || data.Operation == "recover")
	data.FailureReason = firstNonEmpty(data.FailureReason, stringArg(call.Arguments, "failure_reason"))
	if data.SessionID == "" {
		data.FailureReason = firstNonEmpty(data.FailureReason, "session id is required")
	}
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Active = false
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeFabricSubscriptionData(ctx context.Context, call ExpectedToolCall) (FabricSubscriptionArtifactData, error) {
	var data FabricSubscriptionArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, stringArg(call.Arguments, "operation"), fabricOperationForTool(call.Tool))
	data.SessionID = firstNonEmpty(data.SessionID, stringArg(call.Arguments, "session_id"))
	data.AgentID = firstNonEmpty(data.AgentID, stringArg(call.Arguments, "agent_id"))
	data.Topic = firstNonEmpty(data.Topic, stringArg(call.Arguments, "topic"))
	data.LensQueryShape = firstNonEmpty(data.LensQueryShape, stringArg(call.Arguments, "lens_query_shape"))
	data.SubscriptionState = firstNonEmpty(data.SubscriptionState, fabricSubscriptionState(data.Operation))
	data.Metadata = cloneMetadata(data.Metadata)
	data.Attached = boolArgDefault(call.Arguments, "attached", fabricAttachedByOperation(data.Operation))
	data.FailureReason = firstNonEmpty(data.FailureReason, stringArg(call.Arguments, "failure_reason"), fabricFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Attached = false
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeBusTransportData(ctx context.Context, call ExpectedToolCall) (BusTransportArtifactData, error) {
	var data BusTransportArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, stringArg(call.Arguments, "operation"), busOperationForTool(call.Tool))
	data.ProcessUID = firstNonEmpty(data.ProcessUID, stringArg(call.Arguments, "process_uid"))
	data.Topic = firstNonEmpty(data.Topic, stringArg(call.Arguments, "topic"))
	data.Metadata = cloneMetadata(data.Metadata)
	data.Drained = boolArgDefault(call.Arguments, "drained", data.Drained || data.Operation == "drain")
	data.FailureReason = firstNonEmpty(data.FailureReason, stringArg(call.Arguments, "failure_reason"), busFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func failSessionLifecycleData(call ExpectedToolCall, err error) SessionLifecycleArtifactData {
	return SessionLifecycleArtifactData{Operation: sessionOperationForTool(call.Tool), SessionID: stringArg(call.Arguments, "session_id"), Active: false, Status: InfrastructureStatusFailed, FailureReason: err.Error()}
}

func failFabricSubscriptionData(call ExpectedToolCall, err error) FabricSubscriptionArtifactData {
	return FabricSubscriptionArtifactData{Operation: fabricOperationForTool(call.Tool), SessionID: stringArg(call.Arguments, "session_id"), Attached: false, Status: InfrastructureStatusFailed, FailureReason: err.Error()}
}

func failBusTransportData(call ExpectedToolCall, err error) BusTransportArtifactData {
	return BusTransportArtifactData{Operation: busOperationForTool(call.Tool), Topic: stringArg(call.Arguments, "topic"), Status: InfrastructureStatusFailed, FailureReason: err.Error()}
}

func sessionLifecycleArtifact(data SessionLifecycleArtifactData, artifactName, reference string, kindOverride ...string) (*Artifact, error) {
	kind := ArtifactKindSessionState
	switch data.Operation {
	case "create", "open_session", "switch":
		kind = ArtifactKindSessionHandle
	case "persist":
		kind = ArtifactKindPersistOutcome
	case "restore", "recover":
		kind = ArtifactKindRestoreOutcome
	}
	kind = firstSystemArtifactKind(kindOverride, kind)
	return systemParticipantArtifact(artifactName, kind, reference, data.FailureReason, data)
}

func fabricSubscriptionArtifact(data FabricSubscriptionArtifactData, artifactName, reference string, kindOverride ...string) (*Artifact, error) {
	kind := ArtifactKindSubscriptionState
	switch data.Operation {
	case "query_lens":
		kind = ArtifactKindLensQueryResult
	case "harvest":
		kind = ArtifactKindHarvestOutcome
	}
	kind = firstSystemArtifactKind(kindOverride, kind)
	return systemParticipantArtifact(artifactName, kind, reference, data.FailureReason, data)
}

func busTransportArtifact(data BusTransportArtifactData, artifactName, reference string, kindOverride ...string) (*Artifact, error) {
	kind := ArtifactKindCapacityRecord
	switch data.Operation {
	case "register_topic":
		kind = ArtifactKindTopicRegistration
	case "drain":
		kind = ArtifactKindDrainOutcome
	}
	kind = firstSystemArtifactKind(kindOverride, kind)
	return systemParticipantArtifact(artifactName, kind, reference, data.FailureReason, data)
}

func systemParticipantArtifact[T any](artifactName, kind, reference, failure string, data T) (*Artifact, error) {
	if failure != "" {
		kind = ArtifactKindErrorDiagnostic
	}
	artifact := &Artifact{ArtifactName: artifactName, Kind: kind, Reference: reference}
	if strings.TrimSpace(failure) != "" {
		artifact.Errors = append(artifact.Errors, &ArtifactError{Category: ArtifactErrorCategoryInternal, Description: strings.TrimSpace(failure), OccurredAt: time.Now().UTC()})
	}
	return artifact, SetArtifactData(artifact, data)
}

func systemParticipantCall(claim *Claim, allowed []string) (ExpectedToolCall, error) {
	for _, call := range claim.ExpectedToolCalls {
		if stringInList(call.Tool, allowed) {
			return call, nil
		}
	}
	return ExpectedToolCall{}, fmt.Errorf("%w: expected system participant tool call is required", ErrInfrastructureServiceInvalid)
}

func systemParticipantTools(kind string) []string {
	switch kind {
	case systemParticipantSession:
		return []string{SessionManagerToolOpenSession, SessionManagerToolCloseSession, SessionManagerToolPersist, SessionManagerToolRestore, SessionManagerToolSwitchSession}
	case systemParticipantFabric:
		return []string{FabricSubscriberToolAttach, FabricSubscriberToolDetach, FabricSubscriberToolQueryLens, FabricSubscriberToolHarvest, FabricSubscriberToolTestDelivery}
	case systemParticipantBus:
		return []string{BusAdministratorToolRegisterTopic, BusAdministratorToolAdjustCapacity, BusAdministratorToolDrain, BusAdministratorToolOverflow, BusAdministratorToolSubscriberFail}
	case systemParticipantGeneric:
		return []string{GenericSystemEvidenceToolRecord}
	default:
		return nil
	}
}

func systemParticipantRecordClasses() []string {
	return []string{InfrastructureStatusOK, InfrastructureStatusFailed, InfrastructureStatusInterrupted}
}

func sessionOperationForTool(tool string) string {
	switch strings.TrimSpace(tool) {
	case SessionManagerToolOpenSession:
		return "create"
	case SessionManagerToolCloseSession:
		return "close"
	case SessionManagerToolSwitchSession:
		return "switch"
	case SessionManagerToolPersist, SessionManagerToolRestore:
		return strings.TrimSpace(tool)
	default:
		return strings.TrimSpace(tool)
	}
}

func fabricOperationForTool(tool string) string {
	return strings.TrimSpace(tool)
}

func busOperationForTool(tool string) string {
	return strings.TrimSpace(tool)
}

func sessionActiveByOperation(operation string) bool {
	switch strings.TrimSpace(operation) {
	case "close":
		return false
	default:
		return true
	}
}

func fabricAttachedByOperation(operation string) bool {
	return strings.TrimSpace(operation) != "detach"
}

func fabricSubscriptionState(operation string) string {
	if strings.TrimSpace(operation) == "detach" {
		return "detached"
	}
	return "attached"
}

func fabricFailureReason(data FabricSubscriptionArtifactData) string {
	if data.SessionID == "" {
		return "session id is required"
	}
	if data.Operation == "attach" && !data.Attached {
		return "subscription not attached"
	}
	return ""
}

func busFailureReason(data BusTransportArtifactData) string {
	if data.SubscriberError != "" {
		return data.SubscriberError
	}
	if data.Operation == "overflow" && data.DroppedCount != 0 {
		return "bus transport overflow"
	}
	if data.Capacity > 0 && data.QueueDepth > data.Capacity {
		return "bus capacity exceeded"
	}
	return ""
}

func systemArtifactName(call ExpectedToolCall, fallback string) string {
	return firstNonEmpty(stringArg(call.Arguments, "artifact_name"), fallback)
}

func systemArtifactReference(call ExpectedToolCall, fallback string) string {
	return firstNonEmpty(stringArg(call.Arguments, "reference"), fallback)
}

func systemArtifactKindOverride(call ExpectedToolCall) string {
	kind := stringArg(call.Arguments, "artifact_kind")
	switch kind {
	case "", SystemEvidenceKindSession, SystemEvidenceKindFabric, SystemEvidenceKindBus:
		return ""
	default:
		return kind
	}
}

func firstSystemArtifactKind(overrides []string, fallback string) string {
	if len(overrides) == 0 {
		return fallback
	}
	return firstNonEmpty(overrides[0], fallback)
}

func systemParticipantRecordKey(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, sanitizeSystemEvidenceSegment(value))
	}
	return strings.Join(parts, ":")
}

func boardIDForSystemParticipant(board *ClaimsBoard) string {
	if board == nil {
		return ""
	}
	return board.BoardID()
}

func systemEvidenceClockNow(clock ClaimsClock) time.Time {
	return firstNonNilClock(clock).Now()
}
