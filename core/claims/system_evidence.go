package claims

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	SystemEvidenceKindSession = "session_evidence"
	SystemEvidenceKindFabric  = "fabric_evidence"
	SystemEvidenceKindBus     = "bus_transport_evidence"

	systemEvidenceValidationQuality = "system.evidence.received"
	systemEvidenceKeyPrefix         = "system.evidence"
	systemEvidenceDefaultActor      = "sys:session_manager"
	systemEvidenceFabricActor       = "sys:fabric_subscriber"
	systemEvidenceBusActor          = "sys:bus_administrator"
)

type SystemEvidenceOptions struct {
	Board          *ClaimsBoard
	ActorID        string
	SubjectID      string
	SessionID      string
	Operation      string
	Status         string
	ArtifactKind   string
	ArtifactName   string
	Reference      string
	IdempotencyKey string
	Metadata       map[string]any
}

type SystemEvidenceResult struct {
	ClaimID     string
	TestamentID string
}

func RecordSessionLifecycleEvidence(ctx context.Context, board *ClaimsBoard, operation, sessionID string, metadata map[string]any) (SystemEvidenceResult, error) {
	return RecordSystemEvidence(ctx, SystemEvidenceOptions{
		Board:        board,
		ActorID:      systemEvidenceDefaultActor,
		SubjectID:    systemEvidenceDefaultActor,
		SessionID:    sessionID,
		Operation:    operation,
		Status:       "recorded",
		ArtifactKind: SystemEvidenceKindSession,
		ArtifactName: "session_" + sanitizeSystemEvidenceSegment(operation),
		Reference:    "session " + strings.TrimSpace(operation),
		Metadata:     metadata,
	})
}

func RecordFabricSubscriptionEvidence(ctx context.Context, board *ClaimsBoard, operation, sessionID string, metadata map[string]any) (SystemEvidenceResult, error) {
	return RecordSystemEvidence(ctx, SystemEvidenceOptions{
		Board:        board,
		ActorID:      systemEvidenceFabricActor,
		SubjectID:    systemEvidenceFabricActor,
		SessionID:    sessionID,
		Operation:    operation,
		Status:       "recorded",
		ArtifactKind: SystemEvidenceKindFabric,
		ArtifactName: "fabric_" + sanitizeSystemEvidenceSegment(operation),
		Reference:    "fabric " + strings.TrimSpace(operation),
		Metadata:     metadata,
	})
}

func RecordBusTransportEvidence(ctx context.Context, board *ClaimsBoard, operation, sessionID string, metadata map[string]any) (SystemEvidenceResult, error) {
	if board != nil {
		board.RecordNotificationError("bus transport " + strings.TrimSpace(operation))
	}
	metadata = normalizeBusEvidenceMetadata(metadata)
	return RecordSystemEvidence(ctx, SystemEvidenceOptions{
		Board:        board,
		ActorID:      systemEvidenceBusActor,
		SubjectID:    systemEvidenceBusActor,
		SessionID:    sessionID,
		Operation:    operation,
		Status:       "recorded",
		ArtifactKind: SystemEvidenceKindBus,
		ArtifactName: "bus_" + sanitizeSystemEvidenceSegment(operation),
		Reference:    "bus " + strings.TrimSpace(operation),
		Metadata:     metadata,
	})
}

func normalizeBusEvidenceMetadata(metadata map[string]any) map[string]any {
	out := cloneMetadata(metadata)
	if _, ok := out["dropped_count"]; !ok {
		if dropped, exists := out["dropped"]; exists {
			out["dropped_count"] = dropped
		}
	}
	return out
}

func RecordSystemEvidence(ctx context.Context, opts SystemEvidenceOptions) (SystemEvidenceResult, error) {
	opts = normalizeSystemEvidenceOptions(opts)
	if err := validateSystemEvidenceOptions(opts); err != nil {
		return SystemEvidenceResult{}, err
	}
	participant, err := systemEvidenceParticipant(opts)
	if err != nil {
		return SystemEvidenceResult{}, err
	}
	handler := systemEvidenceHandler(opts)
	if handler == nil {
		return SystemEvidenceResult{}, fmt.Errorf("system evidence handler is required for %s", opts.ArtifactKind)
	}
	result, err := InvokeServiceClaim(ctx, ServiceInvocationOptions{
		Board:          opts.Board,
		Handler:        handler,
		Participant:    participant,
		IssuerID:       opts.ActorID,
		SubjectID:      opts.SubjectID,
		Title:          "Record " + opts.Operation,
		Description:    "Record durable claims-plane evidence for " + opts.Operation + ".",
		ActionType:     ActionTypeTask,
		ExpectedCall:   ExpectedToolCall{ID: opts.IdempotencyKey, Tool: systemEvidenceTool(opts), Arguments: systemEvidenceArguments(opts)},
		IdempotencyKey: opts.IdempotencyKey,
		Reason:         "system evidence service claim generated",
	})
	if err != nil {
		return SystemEvidenceResult{ClaimID: result.ClaimID}, err
	}
	return SystemEvidenceResult{ClaimID: result.ClaimID, TestamentID: result.TestamentID}, nil
}

func systemEvidenceParticipant(opts SystemEvidenceOptions) (ParticipantRegistration, error) {
	kind := systemEvidenceServiceKind(opts.ArtifactKind)
	tools := systemParticipantTools(kind)
	scope := systemEvidenceScope(opts, kind)
	category := ParticipantCategoryService
	determinism := HandlerDeterminismSideEffect
	if kind == systemParticipantFabric {
		determinism = HandlerDeterminismContent
	}
	if kind == systemParticipantBus {
		category = ParticipantCategorySystem
	}
	return NewParticipantRegistration(
		category,
		opts.SubjectID,
		scope,
		len(tools)*len(systemParticipantRecordClasses()),
		len(tools),
		time.Duration(len(tools)*len(systemParticipantRecordClasses()))*time.Second,
		determinism,
		[]ActionType{ActionTypeTask},
	)
}

func systemEvidenceHandler(opts SystemEvidenceOptions) ServiceHandler {
	switch systemEvidenceServiceKind(opts.ArtifactKind) {
	case systemParticipantSession:
		return NewSessionManagerService(SystemParticipantServiceConfig{ParticipantID: opts.SubjectID})
	case systemParticipantFabric:
		return NewFabricSubscriberService(SystemParticipantServiceConfig{ParticipantID: opts.SubjectID})
	case systemParticipantBus:
		return NewBusAdministratorService(SystemParticipantServiceConfig{ParticipantID: opts.SubjectID})
	case systemParticipantGeneric:
		return NewGenericSystemEvidenceService()
	default:
		return nil
	}
}

func systemEvidenceServiceKind(artifactKind string) string {
	switch strings.TrimSpace(artifactKind) {
	case SystemEvidenceKindSession:
		return systemParticipantSession
	case SystemEvidenceKindFabric:
		return systemParticipantFabric
	case SystemEvidenceKindBus:
		return systemParticipantBus
	default:
		return systemParticipantGeneric
	}
}

func systemEvidenceScope(opts SystemEvidenceOptions, kind string) map[string]string {
	switch kind {
	case systemParticipantFabric:
		return map[string]string{"session_id": opts.SessionID}
	case systemParticipantBus:
		return map[string]string{"process_uid": firstNonEmpty(systemEvidenceMetadataString(opts.Metadata, "process_uid"), opts.SessionID, "process")}
	case systemParticipantGeneric:
		return map[string]string{"process_uid": firstNonEmpty(systemEvidenceMetadataString(opts.Metadata, "process_uid"), opts.SessionID, "process")}
	default:
		return map[string]string{"process_uid": firstNonEmpty(systemEvidenceMetadataString(opts.Metadata, "process_uid"), "process")}
	}
}

func systemEvidenceTool(opts SystemEvidenceOptions) string {
	operation := strings.TrimSpace(opts.Operation)
	switch systemEvidenceServiceKind(opts.ArtifactKind) {
	case systemParticipantGeneric:
		return GenericSystemEvidenceToolRecord
	case systemParticipantFabric:
		switch operation {
		case "detach":
			return FabricSubscriberToolDetach
		case "query_lens":
			return FabricSubscriberToolQueryLens
		case "harvest":
			return FabricSubscriberToolHarvest
		case "test_delivery":
			return FabricSubscriberToolTestDelivery
		default:
			return FabricSubscriberToolAttach
		}
	case systemParticipantBus:
		switch operation {
		case "register_topic":
			return BusAdministratorToolRegisterTopic
		case "drain":
			return BusAdministratorToolDrain
		case "overflow":
			return BusAdministratorToolOverflow
		case "subscriber_failure":
			return BusAdministratorToolSubscriberFail
		default:
			return BusAdministratorToolAdjustCapacity
		}
	default:
		switch operation {
		case "close":
			return SessionManagerToolCloseSession
		case "persist":
			return SessionManagerToolPersist
		case "recover", "restore":
			return SessionManagerToolRestore
		case "switch":
			return SessionManagerToolSwitchSession
		default:
			return SessionManagerToolOpenSession
		}
	}
}

func systemEvidenceArguments(opts SystemEvidenceOptions) map[string]any {
	args := cloneMetadata(opts.Metadata)
	args["operation"] = opts.Operation
	args["status"] = opts.Status
	args["session_id"] = opts.SessionID
	args["artifact_kind"] = opts.ArtifactKind
	args["artifact_name"] = opts.ArtifactName
	args["reference"] = opts.Reference
	args["metadata"] = cloneMetadata(opts.Metadata)
	if opts.Status == InfrastructureStatusFailed || opts.Status == "failed" {
		args["failure_reason"] = firstNonEmpty(systemEvidenceMetadataString(opts.Metadata, "failure_reason"), opts.Status)
	}
	if opts.Board != nil {
		args["board_id"] = opts.Board.BoardID()
	}
	if dropped, ok := opts.Metadata["dropped"]; ok {
		args["dropped_count"] = dropped
	}
	return args
}

func systemEvidenceMetadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func normalizeSystemEvidenceOptions(opts SystemEvidenceOptions) SystemEvidenceOptions {
	opts.ActorID = firstNonEmpty(strings.TrimSpace(opts.ActorID), systemEvidenceDefaultActor)
	opts.SubjectID = firstNonEmpty(strings.TrimSpace(opts.SubjectID), opts.ActorID)
	opts.SessionID = firstNonEmpty(strings.TrimSpace(opts.SessionID), boardSessionID(opts.Board))
	opts.Operation = firstNonEmpty(strings.TrimSpace(opts.Operation), "record")
	opts.Status = firstNonEmpty(strings.TrimSpace(opts.Status), "recorded")
	opts.ArtifactKind = firstNonEmpty(strings.TrimSpace(opts.ArtifactKind), SystemEvidenceKindSession)
	opts.ArtifactName = firstNonEmpty(strings.TrimSpace(opts.ArtifactName), sanitizeSystemEvidenceSegment(opts.Operation))
	opts.Reference = firstNonEmpty(strings.TrimSpace(opts.Reference), opts.Operation+" "+opts.Status)
	opts.IdempotencyKey = firstNonEmpty(strings.TrimSpace(opts.IdempotencyKey), systemEvidenceKey(opts))
	opts.Metadata = cloneMetadata(opts.Metadata)
	return opts
}

func validateSystemEvidenceOptions(opts SystemEvidenceOptions) error {
	if opts.Board == nil {
		return fmt.Errorf("system evidence board is required")
	}
	if opts.ActorID == "" || opts.SubjectID == "" || opts.Operation == "" || opts.IdempotencyKey == "" {
		return fmt.Errorf("system evidence actor, subject, operation, and idempotency key are required")
	}
	return nil
}

func systemEvidenceMetadata(opts SystemEvidenceOptions) map[string]any {
	return mergeMetadata(opts.Metadata, map[string]any{
		"actor_id":    opts.ActorID,
		"subject_id":  opts.SubjectID,
		"session_id":  opts.SessionID,
		"operation":   opts.Operation,
		"status":      opts.Status,
		"recorded_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func systemEvidenceKey(opts SystemEvidenceOptions) string {
	return strings.Join([]string{
		systemEvidenceKeyPrefix,
		sanitizeSystemEvidenceSegment(opts.SessionID),
		sanitizeSystemEvidenceSegment(opts.ArtifactKind),
		sanitizeSystemEvidenceSegment(opts.Operation),
	}, ":")
}

func validationIDForSystemEvidence(key string) string {
	return sanitizeSystemEvidenceSegment(key) + "_receipt"
}

func sanitizeSystemEvidenceSegment(value string) string {
	value = strings.NewReplacer(":", "_", "/", "_", " ", "_", "\t", "_", "\n", "_", "\r", "_").Replace(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	return value
}

func boardSessionID(board *ClaimsBoard) string {
	if board == nil {
		return ""
	}
	return board.SessionID()
}
