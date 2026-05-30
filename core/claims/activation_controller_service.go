package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	ActivationControllerToolActivate   = "activation_controller_activate"
	ActivationControllerToolDeactivate = "activation_controller_deactivate"
	ActivationControllerToolQueryTier  = "activation_controller_query_tier"

	defaultActivationControllerMaxRecords = 4096
)

var ErrActivationControllerServiceInvalid = errors.New("activation controller service invalid")

type ActivationControllerServiceConfig struct {
	MaxRecords int
}

type ActivationControllerService struct {
	mu         sync.RWMutex
	maxRecords int
	records    map[string]ActivationRecordArtifactData
}

func NewActivationControllerService(cfg ActivationControllerServiceConfig) *ActivationControllerService {
	maxRecords := cfg.MaxRecords
	if maxRecords <= 0 {
		maxRecords = defaultActivationControllerMaxRecords
	}
	return &ActivationControllerService{
		maxRecords: maxRecords,
		records:    make(map[string]ActivationRecordArtifactData, maxRecords),
	}
}

func (s *ActivationControllerService) HandleServiceClaim(ctx context.Context, req ServiceClaimRequest) (ServiceClaimResult, error) {
	if err := validateActivationControllerService(s); err != nil {
		return ServiceClaimResult{}, err
	}
	if req.Claim == nil {
		return ServiceClaimResult{}, fmt.Errorf("%w: claim is required", ErrActivationControllerServiceInvalid)
	}
	call, err := activationControllerCall(req.Claim)
	if err != nil {
		return ServiceClaimResult{}, err
	}
	return s.handleActivationControllerCall(ctx, call)
}

func (s *ActivationControllerService) activate(ctx context.Context, input ActivationRecordArtifactData) (ActivationRecordArtifactData, error) {
	if err := validateActivationControllerService(s); err != nil {
		return ActivationRecordArtifactData{}, err
	}
	if err := ctxOrBackground(ctx).Err(); err != nil {
		return ActivationRecordArtifactData{}, err
	}
	input.Operation = firstNonEmpty(input.Operation, "activate")
	record, err := normalizeActivationRecord(input, true)
	if err != nil {
		return ActivationRecordArtifactData{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[record.ParticipantID]; !exists && len(s.records) >= s.maxRecords {
		return ActivationRecordArtifactData{}, fmt.Errorf("%w: max records reached", ErrActivationControllerServiceInvalid)
	}
	s.records[record.ParticipantID] = record
	return record, nil
}

func (s *ActivationControllerService) deactivate(ctx context.Context, participantID, reason string) (ActivationRecordArtifactData, error) {
	return s.deactivateRecord(ctx, ActivationRecordArtifactData{ParticipantID: participantID, Operation: "deactivate", FailureReason: reason})
}

func (s *ActivationControllerService) deactivateRecord(ctx context.Context, input ActivationRecordArtifactData) (ActivationRecordArtifactData, error) {
	if err := validateActivationControllerService(s); err != nil {
		return ActivationRecordArtifactData{}, err
	}
	if err := ctxOrBackground(ctx).Err(); err != nil {
		return ActivationRecordArtifactData{}, err
	}
	input.ParticipantID = strings.TrimSpace(input.ParticipantID)
	if input.ParticipantID == "" {
		return ActivationRecordArtifactData{}, fmt.Errorf("%w: participant id is required", ErrActivationControllerServiceInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prior := s.records[input.ParticipantID]
	input.Operation = firstNonEmpty(input.Operation, "deactivate")
	input.ParticipantType = firstNonEmpty(input.ParticipantType, prior.ParticipantType)
	input.PreviousTier = firstNonEmpty(input.PreviousTier, prior.Tier)
	input.Tier = firstNonEmpty(input.Tier, input.TargetTier, prior.Tier)
	input.TargetTier = firstNonEmpty(input.TargetTier, input.Tier)
	if input.Operation == "deactivate" {
		input.Ready = false
		input.ReplicaCount = 0
	}
	record, err := normalizeActivationRecord(input, false)
	if err != nil {
		return ActivationRecordArtifactData{}, err
	}
	if _, exists := s.records[record.ParticipantID]; !exists && len(s.records) >= s.maxRecords {
		return ActivationRecordArtifactData{}, fmt.Errorf("%w: max records reached", ErrActivationControllerServiceInvalid)
	}
	s.records[input.ParticipantID] = record
	return record, nil
}

func (s *ActivationControllerService) queryTier(ctx context.Context, participantID string) (ActivationRecordArtifactData, bool) {
	if s == nil || ctxOrBackground(ctx).Err() != nil {
		return ActivationRecordArtifactData{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[strings.TrimSpace(participantID)]
	return record, ok
}

func (s *ActivationControllerService) handleActivationControllerCall(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	switch strings.TrimSpace(call.Tool) {
	case ActivationControllerToolActivate:
		return s.handleActivate(ctx, call)
	case ActivationControllerToolDeactivate:
		return s.handleDeactivate(ctx, call)
	case ActivationControllerToolQueryTier:
		return s.handleQueryTier(ctx, call)
	default:
		return ServiceClaimResult{}, fmt.Errorf("%w: unknown activation operation %q", ErrActivationControllerServiceInvalid, call.Tool)
	}
}

func (s *ActivationControllerService) handleActivate(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	input, err := activationRecordFromArgs(call.Arguments)
	if err != nil {
		return ServiceClaimResult{}, err
	}
	record, err := s.activate(ctx, input)
	if err != nil {
		return ServiceClaimResult{}, err
	}
	return activationRecordResult(record, "activation ready ")
}

func (s *ActivationControllerService) handleDeactivate(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	record, err := activationRecordFromArgs(call.Arguments)
	if err != nil {
		return ServiceClaimResult{}, err
	}
	record.ParticipantID = firstNonEmpty(record.ParticipantID, stringArg(call.Arguments, "participant_id"))
	record.FailureReason = firstNonEmpty(record.FailureReason, stringArg(call.Arguments, "failure_reason"), stringArg(call.Arguments, "reason"))
	record, err = s.deactivateRecord(ctx, record)
	if err != nil {
		return ServiceClaimResult{}, err
	}
	return activationRecordResult(record, "activation deactivated ")
}

func (s *ActivationControllerService) handleQueryTier(ctx context.Context, call ExpectedToolCall) (ServiceClaimResult, error) {
	record, ok := s.queryTier(ctx, stringArg(call.Arguments, "participant_id"))
	if ok {
		record.Operation = "query_tier"
		return activationRecordResult(record, "activation queried ")
	}
	record, err := activationRecordFromArgs(call.Arguments)
	if err != nil {
		return ServiceClaimResult{}, err
	}
	record.Operation = firstNonEmpty(record.Operation, "query_tier")
	record, err = normalizeActivationRecord(record, false)
	if err != nil {
		return ServiceClaimResult{}, fmt.Errorf("%w: %s", ErrActivationControllerServiceInvalid, stringArg(call.Arguments, "participant_id"))
	}
	return activationRecordResult(record, "activation queried ")
}

func activationControllerCall(claim *Claim) (ExpectedToolCall, error) {
	for _, call := range claim.ExpectedToolCalls {
		switch strings.TrimSpace(call.Tool) {
		case ActivationControllerToolActivate, ActivationControllerToolDeactivate, ActivationControllerToolQueryTier:
			return call, nil
		}
	}
	return ExpectedToolCall{}, fmt.Errorf("%w: activation controller expected tool call is required", ErrActivationControllerServiceInvalid)
}

func activationRecordFromArgs(args map[string]any) (ActivationRecordArtifactData, error) {
	var out ActivationRecordArtifactData
	if err := decodeArgs(args, &out); err != nil {
		return out, fmt.Errorf("%w: %v", ErrActivationControllerServiceInvalid, err)
	}
	return out, nil
}

func normalizeActivationRecord(in ActivationRecordArtifactData, activating bool) (ActivationRecordArtifactData, error) {
	in.ParticipantID = strings.TrimSpace(in.ParticipantID)
	in.ParticipantType = strings.TrimSpace(in.ParticipantType)
	in.Operation = strings.TrimSpace(in.Operation)
	in.Tier = strings.TrimSpace(in.Tier)
	in.PreviousTier = strings.TrimSpace(in.PreviousTier)
	in.TargetTier = strings.TrimSpace(in.TargetTier)
	in.FailureReason = strings.TrimSpace(in.FailureReason)
	if in.ParticipantID == "" {
		return ActivationRecordArtifactData{}, fmt.Errorf("%w: participant id is required", ErrActivationControllerServiceInvalid)
	}
	if activating && in.FailureReason == "" && in.ReplicaCount <= 0 {
		return ActivationRecordArtifactData{}, fmt.Errorf("%w: replica count must be positive", ErrActivationControllerServiceInvalid)
	}
	if in.Duration < 0 {
		return ActivationRecordArtifactData{}, fmt.Errorf("%w: activation duration must be non-negative", ErrActivationControllerServiceInvalid)
	}
	if activating {
		in.Ready = in.FailureReason == ""
	}
	return in, nil
}

func activationRecordResult(record ActivationRecordArtifactData, prefix string) (ServiceClaimResult, error) {
	artifacts, err := activationRecordArtifacts(record)
	if err != nil {
		return ServiceClaimResult{}, err
	}
	return ServiceClaimResult{Summary: prefix + record.ParticipantID, Artifacts: artifacts}, nil
}

func validateActivationControllerService(s *ActivationControllerService) error {
	if s == nil || s.maxRecords <= 0 || s.records == nil {
		return fmt.Errorf("%w: service is not initialized", ErrActivationControllerServiceInvalid)
	}
	return nil
}
