package claims

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	IdentityRegistryToolAllocate = "identity_registry_allocate"
	IdentityRegistryToolLookup   = "identity_registry_lookup"
	IdentityRegistryToolLineage  = "identity_registry_lineage"

	defaultIdentityRegistryMaxRecords = 4096
)

var (
	ErrIdentityRegistryServiceInvalid = errors.New("identity registry service invalid")
	ErrIdentityRegistryNotFound       = errors.New("identity registry record not found")
	ErrIdentityUIDCollision           = errors.New("identity uid collision")
	ErrIdentityGenerationRegression   = errors.New("identity generation regression")
)

type IdentityRegistryServiceConfig struct {
	MaxRecords int
}

type IdentityRegistryService struct {
	mu         sync.RWMutex
	maxRecords int
	byUID      map[string]IdentityAllocationArtifactData
}

func NewIdentityRegistryService(cfg IdentityRegistryServiceConfig) *IdentityRegistryService {
	maxRecords := cfg.MaxRecords
	if maxRecords <= 0 {
		maxRecords = defaultIdentityRegistryMaxRecords
	}
	return &IdentityRegistryService{
		maxRecords: maxRecords,
		byUID:      make(map[string]IdentityAllocationArtifactData, maxRecords),
	}
}

func (s *IdentityRegistryService) HandleServiceClaim(ctx context.Context, req ServiceClaimRequest) (ServiceClaimResult, error) {
	if err := validateIdentityRegistryService(s); err != nil {
		return identityAllocationFailureResult(ExpectedToolCall{Tool: "identity_registry"}, err)
	}
	if req.Claim == nil {
		return identityAllocationFailureResult(ExpectedToolCall{Tool: "identity_registry"}, fmt.Errorf("%w: claim is required", ErrIdentityRegistryServiceInvalid))
	}
	call, err := identityRegistryCall(req.Claim)
	if err != nil {
		return identityAllocationFailureResult(ExpectedToolCall{Tool: "identity_registry"}, err)
	}
	return s.handleIdentityRegistryCall(ctx, call)
}

func (s *IdentityRegistryService) ServiceTools() []string {
	return []string{IdentityRegistryToolAllocate, IdentityRegistryToolLineage, IdentityRegistryToolLookup}
}

func (s *IdentityRegistryService) allocate(ctx context.Context, input IdentityAllocationArtifactData) (IdentityAllocationArtifactData, error) {
	if err := validateIdentityRegistryService(s); err != nil {
		return IdentityAllocationArtifactData{}, err
	}
	if err := ctxOrBackground(ctx).Err(); err != nil {
		return IdentityAllocationArtifactData{}, err
	}
	normalized, err := normalizeIdentityAllocation(input)
	if err != nil {
		return IdentityAllocationArtifactData{}, err
	}
	return s.storeAllocation(normalized)
}

func (s *IdentityRegistryService) storeAllocation(normalized IdentityAllocationArtifactData) (IdentityAllocationArtifactData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.byUID[normalized.UID]; ok {
		merged, err := mergeIdentityGeneration(existing, normalized)
		if err != nil {
			return IdentityAllocationArtifactData{}, err
		}
		s.byUID[normalized.UID] = merged
		return merged, nil
	}
	if len(s.byUID) >= s.maxRecords {
		return IdentityAllocationArtifactData{}, fmt.Errorf("%w: max records reached", ErrIdentityRegistryServiceInvalid)
	}
	s.byUID[normalized.UID] = normalized
	return cloneIdentityAllocation(normalized), nil
}

func (s *IdentityRegistryService) Lookup(ctx context.Context, uid string) (IdentityAllocationArtifactData, bool) {
	if s == nil || ctxOrBackground(ctx).Err() != nil {
		return IdentityAllocationArtifactData{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.byUID[strings.TrimSpace(uid)]
	return cloneIdentityAllocation(record), ok
}

func (s *IdentityRegistryService) lineage(ctx context.Context, uid string) (IdentityLineageArtifactData, error) {
	if err := validateIdentityRegistryService(s); err != nil {
		return IdentityLineageArtifactData{}, err
	}
	if err := ctxOrBackground(ctx).Err(); err != nil {
		return IdentityLineageArtifactData{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return identityLineageLocked(s.byUID, strings.TrimSpace(uid))
}

func (s *IdentityRegistryService) handleIdentityRegistryCall(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	switch strings.TrimSpace(call.Tool) {
	case IdentityRegistryToolAllocate:
		return s.handleAllocate(ctx, call)
	case IdentityRegistryToolLookup:
		return s.handleLookup(ctx, call)
	case IdentityRegistryToolLineage:
		return s.handleLineage(ctx, call)
	default:
		return identityAllocationFailureResult(call, fmt.Errorf("%w: unknown identity operation %q", ErrIdentityRegistryServiceInvalid, call.Tool))
	}
}

func (s *IdentityRegistryService) handleAllocate(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	input, err := identityAllocationFromArgs(call.Arguments)
	if err != nil {
		return identityAllocationFailureResult(call, err)
	}
	record, err := s.allocate(ctx, input)
	if err != nil {
		return identityAllocationFailureResult(call, err)
	}
	artifact, err := identityAllocationArtifact(record)
	return serviceClaimResultWithArtifact("identity allocated "+record.UID, artifact, err, "identity_allocation_artifact_error", record.UID)
}

func (s *IdentityRegistryService) handleLookup(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	uid := stringArg(call.Arguments, "uid")
	record, ok := s.Lookup(ctx, uid)
	if !ok {
		return identityAllocationFailureResult(call, fmt.Errorf("%w: %s", ErrIdentityRegistryNotFound, uid))
	}
	artifact, err := identityAllocationArtifact(record)
	return serviceClaimResultWithArtifact("identity found "+record.UID, artifact, err, "identity_lookup_artifact_error", record.UID)
}

func (s *IdentityRegistryService) handleLineage(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	lineage, err := s.lineage(ctx, stringArg(call.Arguments, "uid"))
	if err != nil {
		return identityLineageFailureResult(call, err)
	}
	artifact, err := identityLineageArtifact(lineage)
	return serviceClaimResultWithArtifact("identity lineage "+lineage.UID, artifact, err, "identity_lineage_artifact_error", lineage.UID)
}

func identityRegistryCall(claim *Claim) (ExpectedToolCall, error) {
	for _, call := range claim.ExpectedToolCalls {
		switch strings.TrimSpace(call.Tool) {
		case IdentityRegistryToolAllocate, IdentityRegistryToolLookup, IdentityRegistryToolLineage:
			return call, nil
		}
	}
	return ExpectedToolCall{}, fmt.Errorf("%w: identity registry expected tool call is required", ErrIdentityRegistryServiceInvalid)
}

func identityAllocationFromArgs(args map[string]any) (IdentityAllocationArtifactData, error) {
	var out IdentityAllocationArtifactData
	if err := decodeArgs(args, &out); err != nil {
		return out, fmt.Errorf("%w: %v", ErrIdentityRegistryServiceInvalid, err)
	}
	return out, nil
}

func decodeArgs(args map[string]any, out any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func normalizeIdentityAllocation(in IdentityAllocationArtifactData) (IdentityAllocationArtifactData, error) {
	in.Category = ParticipantCategory(strings.TrimSpace(string(in.Category)))
	if in.Category == "" {
		in.Category = ParticipantCategoryService
	}
	in.RouteKey = strings.TrimSpace(in.RouteKey)
	in.ParticipantType = firstNonEmpty(strings.TrimSpace(in.ParticipantType), in.RouteKey)
	in.ParentUID = strings.TrimSpace(in.ParentUID)
	in.Scope = cloneStringMap(in.Scope)
	if in.RouteKey == "" {
		return IdentityAllocationArtifactData{}, fmt.Errorf("%w: route key is required", ErrIdentityRegistryServiceInvalid)
	}
	uid, err := DeriveParticipantUID(in.Category, in.RouteKey, in.Scope)
	if err != nil {
		return IdentityAllocationArtifactData{}, err
	}
	if strings.TrimSpace(in.UID) != "" && strings.TrimSpace(in.UID) != uid {
		return IdentityAllocationArtifactData{}, fmt.Errorf("%w: got %s want %s", ErrIdentityUIDCollision, in.UID, uid)
	}
	in.UID = uid
	return in, nil
}

func mergeIdentityGeneration(existing, candidate IdentityAllocationArtifactData) (IdentityAllocationArtifactData, error) {
	if !sameIdentityAllocationScope(existing, candidate) {
		return IdentityAllocationArtifactData{}, ErrIdentityUIDCollision
	}
	if candidate.Generation < existing.Generation {
		return IdentityAllocationArtifactData{}, ErrIdentityGenerationRegression
	}
	if candidate.Generation > existing.Generation {
		existing.Generation = candidate.Generation
	}
	return cloneIdentityAllocation(existing), nil
}

func sameIdentityAllocationScope(a, b IdentityAllocationArtifactData) bool {
	if a.Category != b.Category || a.RouteKey != b.RouteKey || a.ParentUID != b.ParentUID {
		return false
	}
	return stringMapsEqual(a.Scope, b.Scope)
}

func identityLineageLocked(records map[string]IdentityAllocationArtifactData, uid string) (IdentityLineageArtifactData, error) {
	record, ok := records[uid]
	if !ok {
		return IdentityLineageArtifactData{}, fmt.Errorf("%w: %s", ErrIdentityRegistryNotFound, uid)
	}
	seen := make(map[string]bool, len(records))
	lineage := []string{record.UID}
	current := record.ParentUID
	for current != "" {
		if seen[current] {
			return IdentityLineageArtifactData{}, fmt.Errorf("%w: lineage cycle at %s", ErrIdentityRegistryServiceInvalid, current)
		}
		seen[current] = true
		parent, ok := records[current]
		if !ok {
			return IdentityLineageArtifactData{UID: uid, Lineage: lineage, Terminal: false}, nil
		}
		lineage = append(lineage, parent.UID)
		current = parent.ParentUID
	}
	return IdentityLineageArtifactData{UID: uid, Lineage: lineage, Terminal: true}, nil
}

func identityAllocationArtifact(data IdentityAllocationArtifactData) (*Artifact, error) {
	artifact := &Artifact{ArtifactName: "identity_allocation", Kind: identityAllocationArtifactKind(data), Reference: firstNonEmpty(data.UID, data.RouteKey, data.Operation)}
	if err := SetArtifactData(artifact, data); err != nil {
		return nil, err
	}
	appendInfrastructureArtifactFailure(artifact, data.FailureReason)
	return artifact, nil
}

func identityLineageArtifact(data IdentityLineageArtifactData) (*Artifact, error) {
	artifact := &Artifact{ArtifactName: "identity_lineage", Kind: identityLineageArtifactKind(data), Reference: data.UID}
	if err := SetArtifactData(artifact, data); err != nil {
		return nil, err
	}
	appendInfrastructureArtifactFailure(artifact, data.FailureReason)
	return artifact, nil
}

func activationRecordArtifact(data ActivationRecordArtifactData) (*Artifact, error) {
	artifact := &Artifact{ArtifactName: "activation_record", Kind: activationRecordArtifactKind(data), Reference: data.ParticipantID}
	if err := SetArtifactData(artifact, data); err != nil {
		return nil, err
	}
	appendInfrastructureArtifactFailure(artifact, data.FailureReason)
	return artifact, nil
}

func activationRecordArtifacts(data ActivationRecordArtifactData) ([]*Artifact, error) {
	primary, err := activationRecordArtifact(data)
	if err != nil {
		return nil, err
	}
	artifacts := []*Artifact{primary}
	if data.Ready && data.ReplicaCount > 0 && primary.Kind != ArtifactKindReplicaSet {
		replica := &Artifact{ArtifactName: "replica_set", Kind: ArtifactKindReplicaSet, Reference: data.ParticipantID}
		if err := SetArtifactData(replica, data); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, replica)
	}
	return artifacts, nil
}

func activationRecordArtifactKind(data ActivationRecordArtifactData) string {
	if strings.TrimSpace(data.FailureReason) != "" {
		return ArtifactKindActivationFailure
	}
	switch strings.TrimSpace(data.Operation) {
	case "deactivate", "tier_transition":
		return ArtifactKindTierTransition
	case "replica_set":
		return ArtifactKindReplicaSet
	default:
		return ArtifactKindActivationRecord
	}
}

func identityAllocationFailureResult(call ExpectedToolCall, cause error) (ServiceClaimResult, error) {
	cause = infrastructureServiceCause(cause)
	data, _ := identityAllocationFromArgs(call.Arguments)
	data.Operation = firstNonEmpty(data.Operation, call.Tool, "identity_registry")
	data.RouteKey = firstNonEmpty(data.RouteKey, stringArg(call.Arguments, "route_key"), data.Operation)
	data.UID = firstNonEmpty(strings.TrimSpace(data.UID), stringArg(call.Arguments, "uid"), data.RouteKey)
	data.FailureReason = cause.Error()
	artifact, err := identityAllocationArtifact(data)
	return serviceClaimResultWithArtifact("identity "+data.Operation+" failed", artifact, err, "identity_allocation_failure_artifact_error", data.UID)
}

func identityLineageFailureResult(call ExpectedToolCall, cause error) (ServiceClaimResult, error) {
	cause = infrastructureServiceCause(cause)
	data := IdentityLineageArtifactData{UID: firstNonEmpty(stringArg(call.Arguments, "uid"), call.ID, call.Tool), Terminal: false, FailureReason: cause.Error()}
	artifact, err := identityLineageArtifact(data)
	return serviceClaimResultWithArtifact("identity lineage failed", artifact, err, "identity_lineage_failure_artifact_error", data.UID)
}

func identityAllocationArtifactKind(data IdentityAllocationArtifactData) string {
	if strings.TrimSpace(data.FailureReason) != "" {
		return ArtifactKindErrorDiagnostic
	}
	return ArtifactKindAgentID
}

func identityLineageArtifactKind(data IdentityLineageArtifactData) string {
	if strings.TrimSpace(data.FailureReason) != "" {
		return ArtifactKindErrorDiagnostic
	}
	return ArtifactKindAgentID
}

func appendInfrastructureArtifactFailure(artifact *Artifact, reason string) {
	reason = strings.TrimSpace(reason)
	if artifact == nil || reason == "" {
		return
	}
	artifact.Errors = append(artifact.Errors, &ArtifactError{Category: ArtifactErrorCategoryInternal, Description: reason, OccurredAt: time.Now().UTC()})
}

func serviceClaimResultWithArtifact(summary string, artifact *Artifact, err error, fallbackName, reference string) (ServiceClaimResult, error) {
	if err == nil && artifact != nil {
		return ServiceClaimResult{Summary: summary, Artifacts: []*Artifact{artifact}}, nil
	}
	if err == nil {
		err = fmt.Errorf("%w: artifact is nil", ErrInfrastructureServiceInvalid)
	}
	fallback := &Artifact{ArtifactName: fallbackName, Kind: ArtifactKindErrorDiagnostic, Reference: firstNonEmpty(reference, fallbackName)}
	fallback.Errors = append(fallback.Errors, &ArtifactError{Category: ArtifactErrorCategoryInternal, Description: err.Error(), OccurredAt: time.Now().UTC()})
	setErr := SetArtifactData(fallback, PresentationEvidenceArtifactData{Kind: ArtifactKindErrorDiagnostic, Reference: fallback.Reference, Title: fallbackName, Metadata: map[string]any{"failure_reason": err.Error()}})
	if setErr != nil {
		fallback.Errors = append(fallback.Errors, &ArtifactError{Category: ArtifactErrorCategoryInternal, Description: setErr.Error(), OccurredAt: time.Now().UTC()})
	}
	return ServiceClaimResult{Summary: firstNonEmpty(summary, fallbackName) + " artifact_error", Artifacts: []*Artifact{fallback}}, nil
}

func serviceClaimResultWithArtifacts(summary string, artifacts []*Artifact, err error, fallbackName, reference string) (ServiceClaimResult, error) {
	if err != nil {
		return serviceClaimResultWithArtifact(summary, nil, err, fallbackName, reference)
	}
	if len(artifacts) == 0 {
		return serviceClaimResultWithArtifact(summary, nil, fmt.Errorf("%w: artifacts are empty", ErrInfrastructureServiceInvalid), fallbackName, reference)
	}
	return ServiceClaimResult{Summary: summary, Artifacts: artifacts}, nil
}

func infrastructureServiceCause(cause error) error {
	if cause != nil {
		return cause
	}
	return ErrInfrastructureServiceInvalid
}

func cloneIdentityAllocation(in IdentityAllocationArtifactData) IdentityAllocationArtifactData {
	in.Scope = cloneStringMap(in.Scope)
	return in
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func validateIdentityRegistryService(s *IdentityRegistryService) error {
	if s == nil || s.maxRecords <= 0 || s.byUID == nil {
		return fmt.Errorf("%w: service is not initialized", ErrIdentityRegistryServiceInvalid)
	}
	return nil
}
